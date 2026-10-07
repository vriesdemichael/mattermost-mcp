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
// download address and SHA-256, which is how the registry names an .mcpb.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		document, err := ServerJSON(read("server.json"), *version, *baseURL, hashes)
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
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	return append(encoded, '\n'), err
}

// ServerJSON is the registry entry for a release: one package per bundle,
// each named by its download address and checksum.
func ServerJSON(template []byte, version, baseURL string, hashes map[string]string) ([]byte, error) {
	number, err := bare(version)
	if err != nil {
		return nil, err
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("no .mcpb bundles to list")
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
			"registryType": "mcpb",
			"identifier":   strings.TrimRight(baseURL, "/") + "/" + name,
			"version":      number,
			"fileSha256":   hashes[name],
			"transport":    map[string]string{"type": "stdio"},
		})
	}
	document["packages"] = packages
	encoded, err := json.MarshalIndent(document, "", "  ")
	return append(encoded, '\n'), err
}
