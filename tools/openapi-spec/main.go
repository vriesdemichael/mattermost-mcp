// Command openapi-spec vendors what each supported Mattermost release says and
// does at its API (ADR-026).
//
//	go run ./tools/openapi-spec refresh   # write openapi/mattermost-<stack>.json and openapi/routes-<stack>.json
//	go run ./tools/openapi-spec check     # fail when a vendored file is not its stack's release
//
// Both come from Mattermost's repository at the tag each test stack runs, read
// from a sparse clone under .tmp/. The specification: Mattermost does not
// publish one per release; it keeps the sources under api/v4/source and builds
// one document by concatenating them in the order api/Makefile lists, and this
// does the same. The route table: every endpoint the release's own router
// registers in server/channels/api4, which is what the server serves whatever
// the specification says. The release is read from the image tag in
// docker/<stack>/compose.yml and written into both files; it is stated nowhere
// else.
//
// `refresh` needs the network; `check` does not, and runs in quality:verify.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const releaseKey = "x-mattermost-release"

var (
	stacks       = []string{"latest", "esr"}
	imageTag     = regexp.MustCompile(`(?m)image:\s*mattermost/mattermost-team-edition:(\d+\.\d+\.\d+)\s*$`)
	sourceInMake = regexp.MustCompile(`cat \$\(V4_SRC\)/([\w.-]+\.yaml) >> \$\(V4_YAML\)`)
)

// RouteTable is a release's route table as vendored.
type RouteTable struct {
	Release string  `json:"release"`
	Routes  []Route `json:"routes"`
}

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "refresh" && os.Args[1] != "check") {
		fail(fmt.Errorf("usage: openapi-spec refresh|check"))
	}
	if os.Args[1] == "refresh" {
		for _, stack := range stacks {
			tag, err := StackRelease(stack)
			fail(err)
			dir, err := checkout(tag)
			fail(err)
			document, err := assemble(dir, tag)
			fail(err)
			fail(write(specPath(stack), document))
			routes, err := routesFrom(dir)
			fail(err)
			fail(write(routesPath(stack), RouteTable{Release: tag, Routes: routes}))
			fmt.Printf("Vendored Mattermost %s: %s and %s, %d routes.\n", tag, specPath(stack), routesPath(stack), len(routes))
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
		if got := vendoredRoutesRelease(stack); got != want {
			stale = append(stale, fmt.Sprintf("%s describes %q; the %s stack runs %s", routesPath(stack), got, stack, want))
		}
	}
	if len(stale) > 0 {
		fail(fmt.Errorf("%s\nrun `task openapi:refresh`", strings.Join(stale, "\n")))
	}
	fmt.Println("Each vendored specification and route table describes the release its stack runs.")
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

func routesPath(stack string) string {
	return filepath.Join("openapi", "routes-"+stack+".json")
}

func vendoredRoutesRelease(stack string) string {
	raw, err := os.ReadFile(routesPath(stack))
	if err != nil {
		return ""
	}
	var table RouteTable
	if json.Unmarshal(raw, &table) != nil {
		return ""
	}
	return table.Release
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

// assemble builds the specification from a checkout of the release's tag.
func assemble(checkout, tag string) (map[string]any, error) {
	makefile, err := os.ReadFile(filepath.Join(checkout, "api", "Makefile"))
	if err != nil {
		return nil, err
	}
	files, err := SourceOrder(string(makefile))
	if err != nil {
		return nil, err
	}
	var source strings.Builder
	for _, name := range files {
		part, err := os.ReadFile(filepath.Join(checkout, "api", "v4", "source", name))
		if err != nil {
			return nil, err
		}
		source.Write(part)
	}
	return Decode(source.String(), tag)
}

// routesFrom extracts the route table from a checkout of the release's tag.
func routesFrom(checkout string) ([]Route, error) {
	dir := filepath.Join(checkout, "server", "channels", "api4")
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	sources := map[string]string{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sources[filepath.Base(path)] = string(raw)
	}
	return ExtractRoutes(sources)
}

// checkout is a sparse clone of Mattermost's repository at the release's tag,
// holding only the API sources, under .tmp/ and reused by a later refresh.
func checkout(tag string) (string, error) {
	dir := filepath.Join(".tmp", "mattermost-src", tag)
	if _, err := os.Stat(filepath.Join(dir, "api", "Makefile")); err == nil {
		return dir, nil
	}
	_ = os.RemoveAll(dir)
	steps := [][]string{
		{"clone", "--quiet", "--depth", "1", "--filter=blob:none", "--sparse", "--branch", "v" + tag, "https://github.com/mattermost/mattermost.git", dir},
		{"-C", dir, "sparse-checkout", "set", "api", "server/channels/api4"},
	}
	for _, args := range steps {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
	}
	return dir, nil
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

func write(path string, document any) error {
	encoded, err := json.MarshalIndent(document, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}
