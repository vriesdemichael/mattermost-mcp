// Command release decides the next release from Conventional Commits and writes its notes.
//
//	go run ./tools/release version
//	go run ./tools/release notes -version v0.2.0 -previous-tag v0.1.0
//
// Which commit types release, and what counts as breaking, is read in this
// package and only here, so the version and the notes cannot disagree about a
// release (ADR-013). `version` prints its decision and, in GitHub Actions,
// writes it to GITHUB_OUTPUT. A commit that already carries a release tag
// releases that version again, so a run that failed after tagging can be
// repeated.
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
	repository := Repository{}
	switch os.Args[1] {
	case "version":
		fail(writeDecision(repository))
	case "notes":
		flags := flag.NewFlagSet("release notes", flag.ExitOnError)
		version := flags.String("version", "", "the release, vX.Y.Z")
		previous := flags.String("previous-tag", "", "the release before it, if any")
		url := flags.String("repository-url", "https://github.com/vriesdemichael/mm-mcp", "for commit links")
		output := flags.String("output", "RELEASE_NOTES.md", "where to write the notes")
		_ = flags.Parse(os.Args[2:])
		fail(notes(repository, *version, *previous, *url, *output))
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

// Repository is the git repository a release is decided in; an empty Dir is
// the working directory.
type Repository struct{ Dir string }

func (r Repository) git(args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = r.Dir
	out, err := command.Output()
	return string(out), err
}

func (r Repository) hasCommits() bool {
	_, err := r.git("rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// Tags lists the tags reachable from HEAD, and the tags on HEAD itself.
func (r Repository) Tags() (reachable, onHead []string, err error) {
	if !r.hasCommits() {
		// A repository with no commits has no tags to read.
		return nil, nil, nil
	}
	out, err := r.git("tag", "--merged", "HEAD", "--list", "v*")
	if err != nil {
		return nil, nil, err
	}
	reachable = strings.Fields(out)
	if out, err = r.git("tag", "--points-at", "HEAD", "--list", "v*"); err != nil {
		return nil, nil, err
	}
	return reachable, strings.Fields(out), nil
}

// CommitsSince lists the Conventional Commits reachable from HEAD and not from
// tag, or every one when tag is empty.
func (r Repository) CommitsSince(tag string) ([]Commit, error) {
	if !r.hasCommits() {
		// A repository with no commits releases nothing.
		return nil, nil
	}
	revision := "HEAD"
	if tag != "" {
		revision = tag + "..HEAD"
	}
	out, err := r.git("log", "--format=%H"+fieldSeparator+"%s"+fieldSeparator+"%b"+recordSeparator, revision)
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

// Decide reads the repository's tags and commits into a Decision.
func (r Repository) Decide() (Decision, error) {
	reachable, onHead, err := r.Tags()
	if err != nil {
		return Decision{}, err
	}
	return Decide(reachable, onHead, r.CommitsSince)
}

func writeDecision(repository Repository) error {
	decision, err := repository.Decide()
	if err != nil {
		return err
	}
	lines := decision.Outputs()
	fmt.Println(lines)
	if path := os.Getenv("GITHUB_OUTPUT"); path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600) //nolint:gosec // the path Actions gives
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		_, err = file.WriteString(lines + "\n")
		return err
	}
	return nil
}

func notes(repository Repository, version, previous, url, output string) error {
	if _, err := ParseVersion(version); err != nil {
		return err
	}
	commits, err := repository.CommitsSince(previous)
	if err != nil {
		return err
	}
	intro, _ := os.ReadFile(filepath.Join(repository.Dir, "docs", "release-notes", version+".md")) //nolint:gosec // a fixed directory
	return os.WriteFile(output, []byte(RenderNotes(version, commits, url, string(intro))), 0o600)
}
