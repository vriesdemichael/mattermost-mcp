package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Bump is how far a set of commits moves the version.
type Bump int

// The bumps, in order of size.
const (
	BumpNone Bump = iota
	BumpPatch
	BumpMinor
	BumpMajor
)

// FirstVersion is what the first release is, whatever its commits say.
var FirstVersion = Version{0, 1, 0}

var (
	subject        = regexp.MustCompile(`^([a-z]+)(?:\(([^()]*)\))?(!)?: (\S.*)$`)
	breakingFooter = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE: `)
	tagPattern     = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)
)

var releasingTypes = map[string]Bump{"feat": BumpMinor, "fix": BumpPatch, "perf": BumpPatch, "revert": BumpPatch}

// Commit is a Conventional Commit.
type Commit struct {
	SHA         string
	Type        string
	Scope       string
	Description string
	Breaking    bool
}

// Bump is what the commit calls for on its own.
func (c Commit) Bump() Bump {
	if c.Breaking {
		return BumpMajor
	}
	return releasingTypes[c.Type]
}

// ParseCommit reads a commit, and reports false for a subject that is not a Conventional Commit.
func ParseCommit(sha, subjectLine, body string) (Commit, bool) {
	match := subject.FindStringSubmatch(strings.TrimSpace(subjectLine))
	if match == nil {
		return Commit{}, false
	}
	return Commit{
		SHA:         sha,
		Type:        match[1],
		Scope:       match[2],
		Description: match[4],
		Breaking:    match[3] == "!" || breakingFooter.MatchString(body),
	}, true
}

// Version is a release version.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// ParseVersion reads a release tag such as v1.2.3.
func ParseVersion(tag string) (Version, error) {
	match := tagPattern.FindStringSubmatch(tag)
	if match == nil {
		return Version{}, fmt.Errorf("%q is not a release tag of the form vMAJOR.MINOR.PATCH", tag)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return Version{major, minor, patch}, nil
}

// Next is the version a bump releases after previous, which is nil before the
// first release. It reports false when the bump releases nothing.
//
// While the major version is 0, a breaking change bumps the minor version and
// everything else that releases bumps the patch: semver's rule for initial
// development (ADR-013).
func Next(previous *Version, bump Bump) (Version, bool) {
	if bump == BumpNone {
		return Version{}, false
	}
	if previous == nil {
		return FirstVersion, true
	}
	v := *previous
	switch {
	case v.Major == 0 && bump == BumpMajor:
		return Version{0, v.Minor + 1, 0}, true
	case v.Major == 0:
		return Version{0, v.Minor, v.Patch + 1}, true
	case bump == BumpMajor:
		return Version{v.Major + 1, 0, 0}, true
	case bump == BumpMinor:
		return Version{v.Major, v.Minor + 1, 0}, true
	default:
		return Version{v.Major, v.Minor, v.Patch + 1}, true
	}
}

var sections = []struct {
	heading string
	belongs func(Commit) bool
}{
	{"Breaking changes", func(c Commit) bool { return c.Breaking }},
	{"Features", func(c Commit) bool { return !c.Breaking && c.Type == "feat" }},
	{"Fixes", func(c Commit) bool { return !c.Breaking && c.Type == "fix" }},
	{"Performance", func(c Commit) bool { return !c.Breaking && c.Type == "perf" }},
	{"Reverts", func(c Commit) bool { return !c.Breaking && c.Type == "revert" }},
}

// RenderNotes writes a release's notes from its commits, under a hand-written
// introduction when there is one. Commits that release nothing are left out.
func RenderNotes(version string, commits []Commit, repositoryURL, intro string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", version)
	if intro = strings.TrimSpace(intro); intro != "" {
		b.WriteString(intro + "\n\n")
	}
	for _, section := range sections {
		var entries []string
		for _, c := range commits {
			if !section.belongs(c) {
				continue
			}
			scope := ""
			if c.Scope != "" {
				scope = "**" + c.Scope + ":** "
			}
			entries = append(entries, fmt.Sprintf("- %s%s ([`%s`](%s/commit/%s))", scope, c.Description, c.SHA[:7], repositoryURL, c.SHA))
		}
		if len(entries) > 0 {
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", section.heading, strings.Join(entries, "\n"))
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
