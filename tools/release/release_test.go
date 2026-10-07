package main

import (
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
