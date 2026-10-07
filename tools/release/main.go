// Command release decides the next release from Conventional Commits and writes its notes.
//
//	go run ./tools/release version
//	go run ./tools/release notes -version v0.2.0 -previous-tag v0.1.0
//
// Which commit types release, and what counts as breaking, is read in this
// package and only here, so the version and the notes cannot disagree about a
// release (ADR-013). `version` prints its decision and, in GitHub Actions,
// writes it to GITHUB_OUTPUT.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	fieldSeparator  = "\x1f"
	recordSeparator = "\x1e"
)

func main() {
	if len(os.Args) < 2 {
		fail(fmt.Errorf("usage: release version | release notes -version vX.Y.Z [-previous-tag vA.B.C]"))
	}
	switch os.Args[1] {
	case "version":
		fail(decide())
	case "notes":
		flags := flag.NewFlagSet("release notes", flag.ExitOnError)
		version := flags.String("version", "", "the release, vX.Y.Z")
		previous := flags.String("previous-tag", "", "the release before it, if any")
		repository := flags.String("repository-url", "https://github.com/vriesdemichael/mm-mcp", "for commit links")
		output := flags.String("output", "RELEASE_NOTES.md", "where to write the notes")
		_ = flags.Parse(os.Args[2:])
		fail(notes(*version, *previous, *repository, *output))
	default:
		fail(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "release: %v\n", err)
		os.Exit(1)
	}
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	return string(out), err
}

func previousTag() (*Version, string, error) {
	out, err := git("tag", "--merged", "HEAD", "--list", "v*")
	if err != nil {
		return nil, "", err
	}
	var best *Version
	tag := ""
	for _, candidate := range strings.Fields(out) {
		v, err := ParseVersion(candidate)
		if err != nil {
			continue
		}
		if best == nil || v.Major > best.Major || (v.Major == best.Major && (v.Minor > best.Minor || (v.Minor == best.Minor && v.Patch > best.Patch))) {
			best, tag = &v, candidate
		}
	}
	return best, tag, nil
}

func commitsSince(tag string) ([]Commit, error) {
	if _, err := git("rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		// A repository with no commits releases nothing.
		return nil, nil
	}
	revision := "HEAD"
	if tag != "" {
		revision = tag + "..HEAD"
	}
	out, err := git("log", "--format=%H"+fieldSeparator+"%s"+fieldSeparator+"%b"+recordSeparator, revision)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, entry := range strings.Split(out, recordSeparator) {
		fields := strings.SplitN(strings.Trim(entry, "\n"), fieldSeparator, 3)
		if len(fields) != 3 {
			continue
		}
		if commit, ok := ParseCommit(fields[0], fields[1], fields[2]); ok {
			commits = append(commits, commit)
		}
	}
	return commits, nil
}

func decide() error {
	previous, tag, err := previousTag()
	if err != nil {
		return err
	}
	commits, err := commitsSince(tag)
	if err != nil {
		return err
	}
	bump := BumpNone
	for _, c := range commits {
		bump = max(bump, c.Bump())
	}
	next, releases := Next(previous, bump)
	outputs := map[string]string{"should_release": "false", "version": "", "previous_tag": tag}
	if releases {
		outputs["should_release"], outputs["version"] = "true", next.String()
	}
	var lines []string
	for _, key := range []string{"should_release", "version", "previous_tag"} {
		lines = append(lines, key+"="+outputs[key])
	}
	fmt.Println(strings.Join(lines, "\n"))
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //nolint:gosec // the path Actions gives
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		_, err = file.WriteString(strings.Join(lines, "\n") + "\n")
		return err
	}
	return nil
}

func notes(version, previous, repository, output string) error {
	if _, err := ParseVersion(version); err != nil {
		return err
	}
	commits, err := commitsSince(previous)
	if err != nil {
		return err
	}
	intro, _ := os.ReadFile(filepath.Join("docs", "release-notes", version+".md")) //nolint:gosec // a fixed directory
	return os.WriteFile(output, []byte(RenderNotes(version, commits, repository, string(intro))), 0o600)
}
