package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	description = "An MCP server for Mattermost"
	license     = "Apache-2.0"
)

var releaseVersion = regexp.MustCompile(`^v(\d+\.\d+\.\d+)$`)

// ParseSums reads a checksum manifest, one "<sha256>  <file>" line per file,
// into the checksum of each file by name.
func ParseSums(raw []byte) (map[string]string, error) {
	sums := map[string]string{}
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 || len(fields[0]) != 64 {
			return nil, fmt.Errorf("checksum line %q is not \"<sha256>  <file>\"", strings.TrimSpace(line))
		}
		sums[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	return sums, nil
}

// archive is one platform's release archive: where it is downloaded from and
// its checksum.
type archive struct {
	URL    string
	SHA256 string
}

// archives finds each platform's archive in the checksum manifest. A platform
// the manifest lacks is an error, never a package without it.
func archives(version, repository string, sums map[string]string, extension string, platforms ...string) (string, map[string]archive, error) {
	match := releaseVersion.FindStringSubmatch(version)
	if match == nil {
		return "", nil, fmt.Errorf("version %q is not vX.Y.Z", version)
	}
	bare := match[1]
	found := map[string]archive{}
	for _, platform := range platforms {
		name := fmt.Sprintf("mm-mcp_%s_%s.%s", bare, platform, extension)
		sum, ok := sums[name]
		if !ok {
			return "", nil, fmt.Errorf("the checksum manifest has no %s", name)
		}
		found[platform] = archive{
			URL:    fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repository, version, name),
			SHA256: sum,
		}
	}
	return bare, found, nil
}

// Homebrew writes the tap's formula: the archive for each of macOS and Linux on
// Apple silicon or ARM and on Intel, and a test that the installed binary
// reports the release's version.
func Homebrew(version, repository string, sums map[string]string) ([]byte, error) {
	bare, found, err := archives(version, repository, sums, "tar.gz", "darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64")
	if err != nil {
		return nil, err
	}
	var formula strings.Builder
	fmt.Fprintf(&formula, `class MmMcp < Formula
  desc %q
  homepage "https://github.com/%s"
  version %q
  license %q

`, description, repository, bare, license)
	for _, system := range []struct{ name, goos string }{{"macos", "darwin"}, {"linux", "linux"}} {
		fmt.Fprintf(&formula, "  on_%s do\n", system.name)
		for _, cpu := range []struct{ name, goarch string }{{"arm", "arm64"}, {"intel", "amd64"}} {
			a := found[system.goos+"_"+cpu.goarch]
			fmt.Fprintf(&formula, "    on_%s do\n      url %q\n      sha256 %q\n    end\n", cpu.name, a.URL, a.SHA256)
		}
		formula.WriteString("  end\n\n")
	}
	formula.WriteString(`  def install
    bin.install "mm-mcp"
  end

  test do
    assert_match "mm-mcp v#{version}", shell_output("#{bin}/mm-mcp version")
  end
end
`)
	return []byte(formula.String()), nil
}

type scoopArchitecture struct {
	URL  string `json:"url"`
	Hash string `json:"hash,omitempty"`
}

type scoopManifest struct {
	Version      string                       `json:"version"`
	Description  string                       `json:"description"`
	Homepage     string                       `json:"homepage"`
	License      string                       `json:"license"`
	Architecture map[string]scoopArchitecture `json:"architecture"`
	Bin          string                       `json:"bin"`
	Checkver     map[string]string            `json:"checkver"`
	Autoupdate   struct {
		Architecture map[string]scoopArchitecture `json:"architecture"`
	} `json:"autoupdate"`
}

// Scoop writes the bucket's manifest: the Windows archive for amd64 and arm64,
// and how Scoop finds and fetches a newer release on its own.
func Scoop(version, repository string, sums map[string]string) ([]byte, error) {
	bare, found, err := archives(version, repository, sums, "zip", "windows_amd64", "windows_arm64")
	if err != nil {
		return nil, err
	}
	homepage := "https://github.com/" + repository
	manifest := scoopManifest{
		Version:     bare,
		Description: description,
		Homepage:    homepage,
		License:     license,
		Architecture: map[string]scoopArchitecture{
			"64bit": {URL: found["windows_amd64"].URL, Hash: found["windows_amd64"].SHA256},
			"arm64": {URL: found["windows_arm64"].URL, Hash: found["windows_arm64"].SHA256},
		},
		Bin:      "mm-mcp.exe",
		Checkver: map[string]string{"github": homepage},
	}
	download := homepage + "/releases/download/v$version/mm-mcp_$version_windows_"
	manifest.Autoupdate.Architecture = map[string]scoopArchitecture{
		"64bit": {URL: download + "amd64.zip"},
		"arm64": {URL: download + "arm64.zip"},
	}
	var document bytes.Buffer
	encoder := json.NewEncoder(&document)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		return nil, err
	}
	return document.Bytes(), nil
}
