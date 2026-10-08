package main

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func template(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAWindowsBundleNamesItsExeAndOnlyItsPlatform(t *testing.T) {
	t.Parallel()
	raw, err := Manifest(template(t, "mcpb/manifest.json"), "v0.3.1", "windows")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
		Server  struct {
			EntryPoint string `json:"entry_point"`
			MCPConfig  struct {
				Command string            `json:"command"`
				Env     map[string]string `json:"env"`
			} `json:"mcp_config"`
		} `json:"server"`
		Compatibility struct {
			Platforms []string `json:"platforms"`
		} `json:"compatibility"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "0.3.1" || manifest.Server.EntryPoint != "server/mm-mcp.exe" ||
		manifest.Server.MCPConfig.Command != "${__dirname}/server/mm-mcp.exe" ||
		len(manifest.Compatibility.Platforms) != 1 || manifest.Compatibility.Platforms[0] != "win32" {
		t.Fatalf("got %+v", manifest)
	}
	if manifest.Server.MCPConfig.Env["MM_TOKEN"] != "${user_config.mattermost_token}" {
		t.Fatalf("the token no longer reaches the server: %v", manifest.Server.MCPConfig.Env)
	}
}

func TestAnUnknownPlatformOrVersionIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := Manifest(template(t, "mcpb/manifest.json"), "v0.3.1", "plan9"); err == nil {
		t.Error("plan9 was accepted")
	}
	if _, err := Manifest(template(t, "mcpb/manifest.json"), "0.3.1", "linux"); err == nil {
		t.Error("a version without its v was accepted")
	}
}

func TestTheRegistryEntryListsEveryBundleByAddressAndChecksum(t *testing.T) {
	t.Parallel()
	raw, err := ServerJSON(template(t, "server.json"), template(t, "mcpb/manifest.json"), "v0.3.1", "https://example.com/download/v0.3.1/",
		map[string]string{"mm-mcp_0.3.1_linux_amd64.mcpb": "aa", "mm-mcp_0.3.1_darwin_arm64.mcpb": "bb"})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Version  string `json:"version"`
		Packages []struct {
			RegistryType string `json:"registryType"`
			Identifier   string `json:"identifier"`
			FileSha256   string `json:"fileSha256"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != "0.3.1" || len(document.Packages) != 2 ||
		document.Packages[0].Identifier != "https://example.com/download/v0.3.1/mm-mcp_0.3.1_darwin_arm64.mcpb" ||
		document.Packages[0].FileSha256 != "bb" || document.Packages[1].RegistryType != "mcpb" {
		t.Fatalf("got %+v", document)
	}
	if _, err := ServerJSON(template(t, "server.json"), template(t, "mcpb/manifest.json"), "v0.3.1", "https://example.com", nil); err == nil {
		t.Fatal("an entry with no bundles was written")
	}
}

func TestEveryRegistryPackageDeclaresTheVariablesTheBundleAsksFor(t *testing.T) {
	t.Parallel()
	raw, err := ServerJSON(template(t, "server.json"), template(t, "mcpb/manifest.json"), "v0.3.1", "https://example.com",
		map[string]string{"mm-mcp_0.3.1_linux_amd64.mcpb": "aa", "mm-mcp_0.3.1_windows_arm64.mcpb": "bb"})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Packages []struct {
			EnvironmentVariables []map[string]any `json:"environmentVariables"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Packages) != 2 {
		t.Fatalf("got %d packages", len(document.Packages))
	}
	want := map[string]map[string]any{
		"MM_URL":                   {"isRequired": true, "isSecret": false, "format": "string"},
		"MM_TOKEN":                 {"isRequired": false, "isSecret": true, "format": "string"},
		"MM_MCP_ALLOW_WRITES":      {"isRequired": false, "isSecret": false, "format": "boolean", "default": "false"},
		"MM_MCP_MARK_AI_GENERATED": {"isRequired": false, "isSecret": false, "format": "boolean", "default": "true"},
		"MM_MCP_DOWNLOAD_DIR":      {"isRequired": false, "isSecret": false, "format": "filepath"},
		"MM_MCP_CA_FILE":           {"isRequired": false, "isSecret": false, "format": "filepath"},
	}
	for _, pkg := range document.Packages {
		if len(pkg.EnvironmentVariables) != len(want) {
			t.Fatalf("got %v, want %d variables", pkg.EnvironmentVariables, len(want))
		}
		for _, variable := range pkg.EnvironmentVariables {
			name, _ := variable["name"].(string)
			expected, ok := want[name]
			if !ok {
				t.Errorf("%s is declared, and the bundle does not set it", name)
				continue
			}
			if description, _ := variable["description"].(string); description == "" {
				t.Errorf("%s has no description", name)
			}
			for key, value := range expected {
				if variable[key] != value {
					t.Errorf("%s: %s is %v, want %v", name, key, variable[key], value)
				}
			}
			if _, ok := expected["default"]; !ok && variable["default"] != nil {
				t.Errorf("%s has the default %v, and the bundle gives none", name, variable["default"])
			}
		}
	}
}

func TestAVariableTheBundleDoesNotAskForIsRefused(t *testing.T) {
	t.Parallel()
	for _, manifest := range []string{
		`{"server":{"mcp_config":{"env":{"MM_URL":"https://chat.example.com"}}},"user_config":{}}`,
		`{"server":{"mcp_config":{"env":{"MM_URL":"${user_config.url}"}}},"user_config":{}}`,
		`{"server":{"mcp_config":{"env":{"MM_URL":"${user_config.url}"}}},"user_config":{"url":{"type":"colour"}}}`,
		`{"server":{"mcp_config":{}},"user_config":{}}`,
	} {
		if variables, err := EnvironmentVariables([]byte(manifest)); err == nil {
			t.Errorf("%s was read as %+v", manifest, variables)
		}
	}
}

func TestEveryBinaryGoReleaserBuiltBecomesABundleForItsPlatform(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	linux := filepath.Join(dir, "mm-mcp")
	windows := filepath.Join(dir, "mm-mcp.exe")
	for _, path := range []string{linux, windows} {
		if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	artifacts := `[
	  {"name":"mm-mcp.exe","path":"` + filepath.ToSlash(windows) + `","goos":"windows","goarch":"arm64","type":"Binary"},
	  {"name":"mm-mcp_0.3.1_linux_amd64.tar.gz","path":"x","goos":"linux","goarch":"amd64","type":"Archive"},
	  {"name":"mm-mcp","path":"` + filepath.ToSlash(linux) + `","goos":"linux","goarch":"amd64","type":"Binary"}
	]`
	binaries, err := Binaries([]byte(artifacts))
	if err != nil || len(binaries) != 2 || binaries[0].GOOS != "linux" {
		t.Fatalf("got %+v, %v", binaries, err)
	}
	for _, binary := range binaries {
		path, err := Bundle(template(t, "mcpb/manifest.json"), "v0.3.1", binary, dir)
		if err != nil {
			t.Fatal(err)
		}
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, file := range reader.File {
			names = append(names, file.Name)
			if strings.HasPrefix(file.Name, "server/") && file.Mode()&0o100 == 0 {
				t.Errorf("%s: %s is not executable", path, file.Name)
			}
		}
		_ = reader.Close()
		want := []string{"manifest.json", "server/" + filepath.Base(binary.Path)}
		if !slices.Equal(names, want) {
			t.Errorf("%s holds %v, want %v", filepath.Base(path), names, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "mm-mcp_0.3.1_windows_arm64.mcpb")); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactsWithoutBinariesAreRefused(t *testing.T) {
	t.Parallel()
	if _, err := Binaries([]byte(`[{"type":"Archive"}]`)); err == nil {
		t.Fatal("artifacts with no binaries were accepted")
	}
}
