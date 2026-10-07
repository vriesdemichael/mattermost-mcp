// Command openapi-spec vendors Mattermost's OpenAPI specification for each
// supported release (ADR-026).
//
//	go run ./tools/openapi-spec refresh   # fetch and write openapi/mattermost-<stack>.json
//	go run ./tools/openapi-spec check     # fail when a vendored spec is not its stack's release
//
// Mattermost does not publish a specification per release. It keeps the
// sources in its server repository under api/v4/source, tagged with every
// release, and builds one document by concatenating them in the order
// api/Makefile lists. This does the same at the tag each test stack runs, so
// the vendored document describes the release the live suite tests. The release
// is read from the image tag in docker/<stack>/compose.yml and written into the
// document as info.x-mattermost-release; it is stated nowhere else.
//
// `refresh` needs the network; `check` does not, and runs in quality:verify.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const releaseKey = "x-mattermost-release"

var (
	stacks       = []string{"latest", "esr"}
	imageTag     = regexp.MustCompile(`(?m)image:\s*mattermost/mattermost-team-edition:(\d+\.\d+\.\d+)\s*$`)
	sourceInMake = regexp.MustCompile(`cat \$\(V4_SRC\)/([\w.-]+\.yaml) >> \$\(V4_YAML\)`)
)

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "refresh" && os.Args[1] != "check") {
		fail(fmt.Errorf("usage: openapi-spec refresh|check"))
	}
	if os.Args[1] == "refresh" {
		client := &http.Client{Timeout: time.Minute}
		for _, stack := range stacks {
			tag, err := StackRelease(stack)
			fail(err)
			document, err := assemble(client, tag)
			fail(err)
			fail(write(stack, document))
			fmt.Printf("Vendored the Mattermost %s specification as %s.\n", tag, specPath(stack))
		}
		return
	}
	var stale []string
	for _, stack := range stacks {
		want, err := StackRelease(stack)
		fail(err)
		if got := VendoredRelease(stack); got != want {
			stale = append(stale, fmt.Sprintf("%s describes %q; the %s stack runs %s", specPath(stack), got, stack, want))
		}
	}
	if len(stale) > 0 {
		fail(fmt.Errorf("%s\nrun `task openapi:refresh`", strings.Join(stale, "\n")))
	}
	fmt.Println("Each vendored specification describes the release its stack runs.")
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi-spec: %v\n", err)
		os.Exit(1)
	}
}

func specPath(stack string) string {
	return filepath.Join("openapi", "mattermost-"+stack+".json")
}

// StackRelease is the Mattermost release a test stack runs, from its compose file.
func StackRelease(stack string) (string, error) {
	compose, err := os.ReadFile(filepath.Join("docker", stack, "compose.yml")) //nolint:gosec // a fixed path
	if err != nil {
		return "", err
	}
	match := imageTag.FindSubmatch(compose)
	if match == nil {
		return "", fmt.Errorf("docker/%s/compose.yml names no mattermost-team-edition release", stack)
	}
	return string(match[1]), nil
}

// VendoredRelease is the release the vendored specification says it describes.
func VendoredRelease(stack string) string {
	raw, err := os.ReadFile(specPath(stack))
	if err != nil {
		return ""
	}
	var document struct {
		Info map[string]any `json:"info"`
	}
	if json.Unmarshal(raw, &document) != nil {
		return ""
	}
	release, _ := document.Info[releaseKey].(string)
	return release
}

// SourceOrder is the source files in the order Mattermost's api/Makefile concatenates them.
func SourceOrder(makefile string) ([]string, error) {
	matches := sourceInMake.FindAllStringSubmatch(makefile, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("api/Makefile lists no source files; its build has changed shape")
	}
	files := []string{"introduction.yaml"}
	for _, match := range matches {
		files = append(files, match[1])
	}
	return files, nil
}

func assemble(client *http.Client, tag string) (map[string]any, error) {
	fetch := func(path string) (string, error) {
		url := fmt.Sprintf("https://raw.githubusercontent.com/mattermost/mattermost/v%s/api/%s", tag, path)
		response, err := client.Get(url) //nolint:noctx // a command-line tool with a client timeout
		if err != nil {
			return "", err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s answered %s", url, response.Status)
		}
		body, err := io.ReadAll(response.Body)
		return string(body), err
	}
	makefile, err := fetch("Makefile")
	if err != nil {
		return nil, err
	}
	files, err := SourceOrder(makefile)
	if err != nil {
		return nil, err
	}
	var source strings.Builder
	for _, name := range files {
		part, err := fetch("v4/source/" + name)
		if err != nil {
			return nil, err
		}
		source.WriteString(part)
	}
	return Decode(source.String(), tag)
}

// Decode reads the assembled YAML into a document JSON can hold, and records
// the release it describes.
//
// Two source files can define the same path, each with its own operations; a
// plain decode would keep one and drop the other, or refuse. The two are merged
// instead, and each merge is reported, so no operation the specification
// documents is lost.
func Decode(source, tag string) (map[string]any, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(source), &root); err != nil {
		return nil, fmt.Errorf("the assembled specification is not YAML: %w", err)
	}
	for _, merged := range MergeDuplicateKeys(&root) {
		fmt.Printf("  %s: merged two definitions of %s\n", tag, merged)
	}
	var raw any
	if err := root.Decode(&raw); err != nil {
		return nil, fmt.Errorf("the assembled specification is not YAML: %w", err)
	}
	document, ok := StringKeys(raw).(map[string]any)
	if !ok || document["paths"] == nil {
		return nil, fmt.Errorf("the assembled document is not an OpenAPI specification")
	}
	info, _ := document["info"].(map[string]any)
	if info == nil {
		info = map[string]any{}
		document["info"] = info
	}
	info[releaseKey] = tag
	return document, nil
}

// MergeDuplicateKeys folds a key a mapping repeats into its first occurrence,
// merging the two values when both are mappings and keeping the later one
// otherwise. It returns the keys it merged.
func MergeDuplicateKeys(node *yaml.Node) []string {
	var merged []string
	for _, child := range node.Content {
		merged = append(merged, MergeDuplicateKeys(child)...)
	}
	if node.Kind != yaml.MappingNode {
		return merged
	}
	first := map[string]int{}
	var kept []*yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		at, seen := first[key.Value]
		if !seen {
			first[key.Value] = len(kept)
			kept = append(kept, key, value)
			continue
		}
		merged = append(merged, key.Value)
		existing := kept[at+1]
		if existing.Kind == yaml.MappingNode && value.Kind == yaml.MappingNode {
			existing.Content = append(existing.Content, value.Content...)
			MergeDuplicateKeys(existing)
		} else {
			kept[at+1] = value
		}
	}
	node.Content = kept
	return merged
}

// StringKeys gives every mapping string keys: YAML reads a response code such
// as 200 as an integer, and JSON keys are strings.
func StringKeys(node any) any {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			value[key] = StringKeys(child)
		}
		return value
	case map[any]any:
		converted := make(map[string]any, len(value))
		for key, child := range value {
			converted[fmt.Sprint(key)] = StringKeys(child)
		}
		return converted
	case []any:
		for i, child := range value {
			value[i] = StringKeys(child)
		}
		return value
	default:
		return node
	}
}

func write(stack string, document map[string]any) error {
	encoded, err := json.MarshalIndent(document, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll("openapi", 0o750); err != nil {
		return err
	}
	return os.WriteFile(specPath(stack), append(encoded, '\n'), 0o600)
}
