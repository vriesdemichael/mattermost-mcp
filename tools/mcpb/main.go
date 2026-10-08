// Command mcpb makes a release's .mcpb bundles and its MCP Registry entry (ADR-023).
//
//	go run ./tools/mcpb bundle -version v0.2.0 -artifacts dist/artifacts.json -out dist
//	go run ./tools/mcpb server-json -version v0.2.0 -bundles dist -base-url <release download url>
//
// `bundle` reads the binaries GoReleaser built from its artifacts.json and packs
// each into a bundle for its platform: a zip of the manifest, from
// mcpb/manifest.json with the version, the binary's name and the platform
// stamped in, and the binary under server/. It is what `mcpb pack` makes, made
// here so a release needs no Node; CI checks one with the official tool.
// `server-json` writes server.json listing every bundle in the directory by its
// download address and SHA-256, which is how the registry names an .mcpb, with
// the environment variables the manifest sets from its user configuration.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: mcpb bundle|server-json [flags]"))
	}
	flags := flag.NewFlagSet("mcpb "+os.Args[1], flag.ExitOnError)
	version := flags.String("version", "", "the release, vX.Y.Z")
	artifacts := flags.String("artifacts", "dist/artifacts.json", "GoReleaser's list of what it built")
	out := flags.String("out", "dist", "the directory to write the bundles into")
	bundles := flags.String("bundles", "dist", "the directory holding the release's .mcpb files")
	baseURL := flags.String("base-url", "", "where the release's files are downloaded from")
	_ = flags.Parse(os.Args[2:])

	switch os.Args[1] {
	case "bundle":
		binaries, err := Binaries(read(*artifacts))
		fail(err)
		template := read("mcpb/manifest.json")
		for _, binary := range binaries {
			path, err := Bundle(template, *version, binary, *out)
			fail(err)
			fmt.Println(path)
		}
	case "server-json":
		files, err := filepath.Glob(filepath.Join(*bundles, "*.mcpb"))
		fail(err)
		hashes := map[string]string{}
		for _, file := range files {
			sum := sha256.Sum256(read(file))
			hashes[filepath.Base(file)] = hex.EncodeToString(sum[:])
		}
		document, err := ServerJSON(read("server.json"), read("mcpb/manifest.json"), *version, *baseURL, hashes)
		fail(err)
		fail(os.WriteFile("server.json", document, 0o600))
	default:
		fail(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func read(path string) []byte {
	raw, err := os.ReadFile(path) //nolint:gosec // a path in the repository or the release
	fail(err)
	return raw
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpb: %v\n", err)
		os.Exit(1)
	}
}

// platforms maps Go's operating system names to the ones a bundle declares.
var platforms = map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win32"}

func bare(version string) (string, error) {
	if !strings.HasPrefix(version, "v") || strings.Count(version, ".") != 2 {
		return "", fmt.Errorf("%q is not a release of the form vX.Y.Z", version)
	}
	return strings.TrimPrefix(version, "v"), nil
}

// Manifest is the template stamped for one release and platform.
func Manifest(template []byte, version, goos string) ([]byte, error) {
	number, err := bare(version)
	if err != nil {
		return nil, err
	}
	platform, ok := platforms[goos]
	if !ok {
		return nil, fmt.Errorf("no bundle platform for %q", goos)
	}
	var manifest map[string]any
	if err := json.Unmarshal(template, &manifest); err != nil {
		return nil, err
	}
	manifest["version"] = number
	binary := "server/mm-mcp"
	if goos == "windows" {
		binary += ".exe"
	}
	server, _ := manifest["server"].(map[string]any)
	config, _ := server["mcp_config"].(map[string]any)
	if server == nil || config == nil {
		return nil, fmt.Errorf("the template has no server.mcp_config")
	}
	server["entry_point"] = binary
	config["command"] = "${__dirname}/" + binary
	compatibility, _ := manifest["compatibility"].(map[string]any)
	if compatibility == nil {
		compatibility = map[string]any{}
		manifest["compatibility"] = compatibility
	}
	compatibility["platforms"] = []string{platform}
	manifest["tools"] = tools()
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	return append(encoded, '\n'), err
}

// EnvironmentVariable is one variable a registry package declares.
type EnvironmentVariable struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsRequired  bool   `json:"isRequired"`
	IsSecret    bool   `json:"isSecret"`
	Format      string `json:"format"`
	Default     string `json:"default,omitempty"`
}

// formats maps a bundle's user configuration types to the registry's formats.
var formats = map[string]string{"string": "string", "number": "number", "boolean": "boolean", "directory": "filepath", "file": "filepath"}

var userConfigValue = regexp.MustCompile(`^\$\{user_config\.([A-Za-z0-9_]+)\}$`)

// EnvironmentVariables reads the variables the bundle's manifest sets from its
// user configuration, so the registry entry declares what the bundle asks for
// and the two cannot drift apart (ADR-023).
func EnvironmentVariables(manifest []byte) ([]EnvironmentVariable, error) {
	var parsed struct {
		Server struct {
			MCPConfig struct {
				Env map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
		UserConfig map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
			Required    bool   `json:"required"`
			Sensitive   bool   `json:"sensitive"`
			Default     any    `json:"default"`
		} `json:"user_config"`
	}
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return nil, fmt.Errorf("the manifest: %w", err)
	}
	env := parsed.Server.MCPConfig.Env
	if len(env) == 0 {
		return nil, fmt.Errorf("the manifest sets no environment variables")
	}
	variables := make([]EnvironmentVariable, 0, len(env))
	for name, value := range env {
		match := userConfigValue.FindStringSubmatch(value)
		if match == nil {
			return nil, fmt.Errorf("the manifest sets %s to %q, not to one user_config value", name, value)
		}
		option, ok := parsed.UserConfig[match[1]]
		if !ok {
			return nil, fmt.Errorf("the manifest sets %s from user_config.%s, which it does not declare", name, match[1])
		}
		format, ok := formats[option.Type]
		if !ok {
			return nil, fmt.Errorf("user_config.%s has the type %q, which the registry has no format for", match[1], option.Type)
		}
		variable := EnvironmentVariable{Name: name, Description: option.Description, IsRequired: option.Required, IsSecret: option.Sensitive, Format: format}
		if option.Default != nil {
			variable.Default = fmt.Sprint(option.Default)
		}
		variables = append(variables, variable)
	}
	sort.Slice(variables, func(i, j int) bool { return variables[i].Name < variables[j].Name })
	return variables, nil
}

// ServerJSON is the registry entry for a release: one package per bundle,
// each named by its download address and checksum, and each declaring the
// environment variables the bundle's manifest sets.
func ServerJSON(template, manifest []byte, version, baseURL string, hashes map[string]string) ([]byte, error) {
	number, err := bare(version)
	if err != nil {
		return nil, err
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("no .mcpb bundles to list")
	}
	variables, err := EnvironmentVariables(manifest)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(template, &document); err != nil {
		return nil, err
	}
	document["version"] = number
	names := make([]string, 0, len(hashes))
	for name := range hashes {
		names = append(names, name)
	}
	sort.Strings(names)
	packages := make([]map[string]any, 0, len(names))
	for _, name := range names {
		packages = append(packages, map[string]any{
			"registryType":         "mcpb",
			"identifier":           strings.TrimRight(baseURL, "/") + "/" + name,
			"version":              number,
			"fileSha256":           hashes[name],
			"transport":            map[string]string{"type": "stdio"},
			"environmentVariables": variables,
		})
	}
	document["packages"] = packages
	encoded, err := json.MarshalIndent(document, "", "  ")
	return append(encoded, '\n'), err
}

// tools lists every tool the server has, by name and the first sentence of
// its description, so a client shows them before the bundle is installed. The
// list is the catalogue's, so it cannot fall behind it; tools_generated says
// the server answers with the tools its configuration offers.
func tools() []map[string]string {
	var out []map[string]string
	for _, spec := range server.AllSpecs() {
		description, _, _ := strings.Cut(spec.Tool.Description, ". ")
		out = append(out, map[string]string{"name": spec.Tool.Name, "description": strings.TrimSuffix(description, ".") + "."})
	}
	return out
}
