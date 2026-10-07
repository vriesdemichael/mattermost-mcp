// Command packages writes what Homebrew and Scoop install a release from: the
// formula for the tap and the manifest for the bucket (ADR-023).
//
//	go run ./tools/packages homebrew -version v0.2.0 -repository vriesdemichael/mm-mcp -sums sha256sums.txt -out Formula/mm-mcp.rb
//	go run ./tools/packages scoop -version v0.2.0 -repository vriesdemichael/mm-mcp -sums sha256sums.txt -out mm-mcp.json
//
// GoReleaser can write both, but only in the run that drafts the release,
// before the SBOMs are checked and the release is made public. The tap and the
// bucket must name only a release anyone can download, so the release workflow
// writes them after it is public, from its checksum manifest, and a pull
// request does the same from the snapshot's.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: packages homebrew|scoop [flags]"))
	}
	flags := flag.NewFlagSet("packages "+os.Args[1], flag.ExitOnError)
	version := flags.String("version", "", "the release, vX.Y.Z")
	repository := flags.String("repository", "vriesdemichael/mm-mcp", "the GitHub repository, owner/name")
	sumsPath := flags.String("sums", "dist/sha256sums.txt", "the release's checksum manifest")
	out := flags.String("out", "", "the file to write")
	_ = flags.Parse(os.Args[2:])

	raw, err := os.ReadFile(*sumsPath)
	fail(err)
	sums, err := ParseSums(raw)
	fail(err)

	var document []byte
	switch os.Args[1] {
	case "homebrew":
		document, err = Homebrew(*version, *repository, sums)
	case "scoop":
		document, err = Scoop(*version, *repository, sums)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	fail(err)
	if *out == "" {
		fail(fmt.Errorf("-out is required"))
	}
	fail(os.MkdirAll(filepath.Dir(*out), 0o750))
	fail(os.WriteFile(*out, document, 0o600))
	fmt.Println(*out)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "packages: %v\n", err)
		os.Exit(1)
	}
}
