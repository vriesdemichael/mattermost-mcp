package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A bug here fails quietly and wrongly, by shipping the wrong version, so the
// parsing and the arithmetic are tested although tools/ is outside the coverage
// gate (ADR-014).

func TestACommitReleasesByItsType(t *testing.T) {
	t.Parallel()
	cases := []struct {
		subject, body string
		want          Bump
	}{
		{"feat: add search", "", BumpMinor},
		{"feat(search): add search", "", BumpMinor},
		{"fix: handle an expired session", "", BumpPatch},
		{"perf: cache the user lookup", "", BumpPatch},
		{"revert: undo the cache", "", BumpPatch},
		{"docs: explain tokens", "", BumpNone},
		{"chore(deps): bump the SDK", "", BumpNone},
		{"feat!: rename get_me", "", BumpMajor},
		{"fix(auth)!: refuse a bot token", "", BumpMajor},
		{"refactor: move the client", "BREAKING CHANGE: the tool is gone", BumpMajor},
		{"refactor: move the client", "BREAKING-CHANGE: the tool is gone", BumpMajor},
		{"fix: x", "This mentions BREAKING CHANGE: in passing", BumpPatch},
	}
	for _, c := range cases {
		commit, ok := ParseCommit(strings.Repeat("a", 40), c.subject, c.body)
		if !ok || commit.Bump() != c.want {
			t.Errorf("%q: got %v (parsed %v), want %v", c.subject, commit.Bump(), ok, c.want)
		}
	}
}

func TestASubjectThatIsNotAConventionalCommitIsIgnored(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"Add search", "feat:add search", "Feat: add search", "feat(scope: add search", "merge branch"} {
		if _, ok := ParseCommit(strings.Repeat("a", 40), s, ""); ok {
			t.Errorf("%q was read as a Conventional Commit", s)
		}
	}
}

func TestTheNextVersion(t *testing.T) {
	t.Parallel()
	v := func(major, minor, patch int) *Version { return &Version{major, minor, patch} }
	cases := []struct {
		previous *Version
		bump     Bump
		want     string
	}{
		{nil, BumpNone, ""},
		{nil, BumpPatch, "v0.1.0"},
		{nil, BumpMajor, "v0.1.0"},
		{v(0, 1, 0), BumpNone, ""},
		{v(0, 1, 0), BumpPatch, "v0.1.1"},
		{v(0, 1, 3), BumpMinor, "v0.1.4"},
		{v(0, 1, 3), BumpMajor, "v0.2.0"},
		{v(1, 2, 3), BumpPatch, "v1.2.4"},
		{v(1, 2, 3), BumpMinor, "v1.3.0"},
		{v(1, 2, 3), BumpMajor, "v2.0.0"},
	}
	for _, c := range cases {
		next, ok := Next(c.previous, c.bump)
		got := ""
		if ok {
			got = next.String()
		}
		if got != c.want {
			t.Errorf("%v after %v: got %q, want %q", c.bump, c.previous, got, c.want)
		}
	}
}

func TestOnlyAPlainReleaseTagIsAVersion(t *testing.T) {
	t.Parallel()
	for _, tag := range []string{"0.1.0", "v1.2", "v01.2.3", "v1.2.3-rc.1", "release-1"} {
		if _, err := ParseVersion(tag); err == nil {
			t.Errorf("%q was read as a version", tag)
		}
	}
	if v, err := ParseVersion("v10.0.12"); err != nil || v.String() != "v10.0.12" {
		t.Errorf("got %v, %v", v, err)
	}
}

func TestTheNotesGroupCommitsAndLeaveOutWhatDoesNotRelease(t *testing.T) {
	t.Parallel()
	commits := []Commit{
		{strings.Repeat("1", 40), "feat", "search", "search messages", false},
		{strings.Repeat("2", 40), "fix", "", "read an expired session as one", false},
		{strings.Repeat("3", 40), "docs", "", "explain tokens", false},
		{strings.Repeat("4", 40), "feat", "", "rename get_me to whoami", true},
	}
	notes := RenderNotes("v0.2.0", commits, "https://example.com/repo", "Hand-written.")
	if !strings.HasPrefix(notes, "# v0.2.0\n\nHand-written.\n") {
		t.Fatalf("got:\n%s", notes)
	}
	breaking, features, fixes := strings.Index(notes, "## Breaking changes"), strings.Index(notes, "## Features"), strings.Index(notes, "## Fixes")
	if breaking >= features || features >= fixes {
		t.Fatalf("sections out of order:\n%s", notes)
	}
	if !strings.Contains(notes, "- **search:** search messages ([`1111111`](https://example.com/repo/commit/") ||
		strings.Contains(notes, "explain tokens") || strings.Count(notes, "rename get_me") != 1 {
		t.Fatalf("got:\n%s", notes)
	}
}

func TestAReleaseTagOnTheCommitReleasesThatVersionAgain(t *testing.T) {
	t.Parallel()
	asked := ""
	commits := func(tag string) ([]Commit, error) {
		asked = tag
		return nil, nil
	}
	got, err := Decide([]string{"v0.1.0", "v0.1.1", "v0.2.0", "nightly"}, []string{"v0.2.0", "nightly"}, commits)
	want := Decision{Release: true, Version: "v0.2.0", PreviousTag: "v0.1.1"}
	if err != nil || got != want {
		t.Fatalf("got %+v, %v; want %+v", got, err, want)
	}
	if asked != "" {
		t.Errorf("read the commits since %q; a tagged commit's version is already decided", asked)
	}
	if got, _ := Decide([]string{"v0.1.0"}, []string{"v0.1.0"}, commits); got != (Decision{Release: true, Version: "v0.1.0"}) {
		t.Errorf("the first release tagged again: got %+v", got)
	}
}

func TestAnUntaggedCommitReleasesWhatItsCommitsCallFor(t *testing.T) {
	t.Parallel()
	since := map[string][]Commit{
		"v0.1.1": {{SHA: strings.Repeat("1", 40), Type: "docs"}},
		"v0.2.0": {{SHA: strings.Repeat("2", 40), Type: "fix"}},
	}
	commits := func(tag string) ([]Commit, error) { return since[tag], nil }
	cases := []struct {
		reachable, onHead []string
		want              Decision
	}{
		{[]string{"v0.1.0", "v0.1.1"}, []string{"nightly"}, Decision{PreviousTag: "v0.1.1"}},
		{[]string{"v0.1.1", "v0.2.0", "v0.10.0-rc.1"}, nil, Decision{Release: true, Version: "v0.2.1", PreviousTag: "v0.2.0"}},
	}
	for _, c := range cases {
		if got, err := Decide(c.reachable, c.onHead, commits); err != nil || got != c.want {
			t.Errorf("%v with %v on HEAD: got %+v, %v; want %+v", c.reachable, c.onHead, got, err, c.want)
		}
	}
}

func TestTheDecisionIsWhatTheWorkflowReads(t *testing.T) {
	t.Parallel()
	got := Decision{Release: true, Version: "v0.2.0", PreviousTag: "v0.1.1"}.Outputs()
	if got != "should_release=true\nversion=v0.2.0\nprevious_tag=v0.1.1" {
		t.Fatalf("got %q", got)
	}
	if got := (Decision{}).Outputs(); got != "should_release=false\nversion=\nprevious_tag=" {
		t.Fatalf("got %q", got)
	}
}

// The workflow tags a commit before it publishes, so a run that fails after
// tagging leaves the tag on main's tip. This repeats that in a real repository:
// the repeated run must release the tagged version again, with the commits
// between the release before it and the tag.
func TestARunRepeatedAfterTaggingReleasesTheTaggedVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(dir, "repo")
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		command.Dir = repo
		// Nothing from the developer's git configuration: no signing, hooks or template.
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	repository := Repository{Dir: repo}
	decide := func(want Decision) {
		t.Helper()
		if got, err := repository.Decide(); err != nil || got != want {
			t.Fatalf("got %+v, %v; want %+v", got, err, want)
		}
	}

	run("init", "--quiet")
	decide(Decision{})
	run("commit", "--quiet", "--allow-empty", "-m", "feat: search messages")
	decide(Decision{Release: true, Version: "v0.1.0"})
	run("tag", "-a", "v0.1.0", "-m", "Release v0.1.0")
	decide(Decision{Release: true, Version: "v0.1.0"})

	run("commit", "--quiet", "--allow-empty", "-m", "docs: explain tokens")
	decide(Decision{PreviousTag: "v0.1.0"})
	run("commit", "--quiet", "--allow-empty", "-m", "fix: read an expired session as one")
	decide(Decision{Release: true, Version: "v0.1.1", PreviousTag: "v0.1.0"})
	run("tag", "-a", "v0.1.1", "-m", "Release v0.1.1")
	decide(Decision{Release: true, Version: "v0.1.1", PreviousTag: "v0.1.0"})

	commits, err := repository.CommitsSince("v0.1.0")
	if err != nil || len(commits) != 2 || commits[0].Type != "fix" || commits[1].Type != "docs" {
		t.Fatalf("the notes of the repeated run would list %+v, %v", commits, err)
	}
}
