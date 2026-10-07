package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// sums is a checksum manifest as GoReleaser writes it for v1.2.3, with each
// file's checksum made from its name so a test can tell them apart.
func sums(t *testing.T, leave string) map[string]string {
	t.Helper()
	var manifest strings.Builder
	for _, platform := range []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64"} {
		for _, extension := range []string{"tar.gz", "deb", "rpm"} {
			if platform[:6] == "darwin" && extension != "tar.gz" {
				continue
			}
			name := "mm-mcp_1.2.3_" + platform + "." + extension
			if name != leave {
				fmt.Fprintf(&manifest, "%s  %s\n", checksum(name), name)
			}
		}
	}
	for _, platform := range []string{"windows_amd64", "windows_arm64"} {
		name := "mm-mcp_1.2.3_" + platform + ".zip"
		if name != leave {
			fmt.Fprintf(&manifest, "%s  %s\n", checksum(name), name)
		}
	}
	parsed, err := ParseSums([]byte(manifest.String()))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func checksum(name string) string {
	return fmt.Sprintf("%064x", len(name)*7919+int(name[len(name)-8]))
}

func TestTheFormulaInstallsEachPlatformsArchiveAndTestsTheVersion(t *testing.T) {
	t.Parallel()
	formula, err := Homebrew("v1.2.3", "vriesdemichael/mm-mcp", sums(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	text := string(formula)
	for _, want := range []string{
		"class MmMcp < Formula",
		`version "1.2.3"`,
		`license "Apache-2.0"`,
		`bin.install "mm-mcp"`,
		`assert_match "mm-mcp v#{version}", shell_output("#{bin}/mm-mcp version")`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the formula lacks %s:\n%s", want, text)
		}
	}
	for _, platform := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		name := "mm-mcp_1.2.3_" + platform + ".tar.gz"
		block := fmt.Sprintf("url %q\n      sha256 %q", "https://github.com/vriesdemichael/mm-mcp/releases/download/v1.2.3/"+name, checksum(name))
		if !strings.Contains(text, block) {
			t.Errorf("the formula does not pair %s with its checksum:\n%s", name, text)
		}
	}
	if strings.Contains(text, ".deb") || strings.Contains(text, ".rpm") || strings.Contains(text, "windows") {
		t.Errorf("the formula names something other than the macOS and Linux archives:\n%s", text)
	}
}

func TestTheScoopManifestInstallsBothWindowsArchives(t *testing.T) {
	t.Parallel()
	raw, err := Scoop("v1.2.3", "vriesdemichael/mm-mcp", sums(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	var manifest scoopManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.2.3" || manifest.Bin != "mm-mcp.exe" || manifest.License != "Apache-2.0" {
		t.Errorf("got %+v", manifest)
	}
	for architecture, platform := range map[string]string{"64bit": "windows_amd64", "arm64": "windows_arm64"} {
		name := "mm-mcp_1.2.3_" + platform + ".zip"
		want := scoopArchitecture{URL: "https://github.com/vriesdemichael/mm-mcp/releases/download/v1.2.3/" + name, Hash: checksum(name)}
		if manifest.Architecture[architecture] != want {
			t.Errorf("%s: got %+v, want %+v", architecture, manifest.Architecture[architecture], want)
		}
		update := manifest.Autoupdate.Architecture[architecture].URL
		if strings.ReplaceAll(update, "$version", "1.2.3") != want.URL {
			t.Errorf("%s: autoupdate fetches %s, not %s", architecture, update, want.URL)
		}
	}
	if manifest.Checkver["github"] != "https://github.com/vriesdemichael/mm-mcp" {
		t.Errorf("checkver is %v", manifest.Checkver)
	}
}

func TestAPlatformMissingFromTheManifestIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := Homebrew("v1.2.3", "vriesdemichael/mm-mcp", sums(t, "mm-mcp_1.2.3_linux_arm64.tar.gz")); err == nil || !strings.Contains(err.Error(), "mm-mcp_1.2.3_linux_arm64.tar.gz") {
		t.Errorf("a formula without linux_arm64: %v", err)
	}
	if _, err := Scoop("v1.2.3", "vriesdemichael/mm-mcp", sums(t, "mm-mcp_1.2.3_windows_arm64.zip")); err == nil || !strings.Contains(err.Error(), "mm-mcp_1.2.3_windows_arm64.zip") {
		t.Errorf("a manifest without windows_arm64: %v", err)
	}
}

func TestOnlyAReleaseVersionIsAccepted(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"1.2.3", "v1.2", "v1.2.3-rc.1", "dev", ""} {
		if _, err := Homebrew(version, "vriesdemichael/mm-mcp", sums(t, "")); err == nil {
			t.Errorf("Homebrew took version %q", version)
		}
		if _, err := Scoop(version, "vriesdemichael/mm-mcp", sums(t, "")); err == nil {
			t.Errorf("Scoop took version %q", version)
		}
	}
}

func TestAMalformedChecksumLineIsRefused(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"abc  mm-mcp_1.2.3_linux_amd64.tar.gz\n", strings.Repeat("a", 64) + "\n"} {
		if _, err := ParseSums([]byte(line)); err == nil {
			t.Errorf("took %q", line)
		}
	}
	parsed, err := ParseSums([]byte(strings.Repeat("b", 64) + " *mm-mcp_1.2.3_windows_amd64.zip\n\n"))
	if err != nil || parsed["mm-mcp_1.2.3_windows_amd64.zip"] != strings.Repeat("b", 64) {
		t.Errorf("a binary-mode line: %v %v", parsed, err)
	}
}
