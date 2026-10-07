package main

import (
	"slices"
	"testing"
)

func TestTheWindowIsTheNewestPatchOfEachMinorBetweenTheEnds(t *testing.T) {
	t.Parallel()
	tags := []string{
		"11.6.9", "11.7.10", "11.7.11", "11.8.4", "11.8.5", "11.9.2", "11.9.2-rc1",
		"11.10.1", "11.10.3", "11.11.0", "11.11.1", "12.0.0-rc3", "12.0.0", "latest", "release-11.11",
	}
	var got []string
	for _, r := range Between(tags, Release{11, 7, 11}, Release{11, 11, 1}) {
		got = append(got, r.String())
	}
	// 11.6 is before the window, 12.0 after it, and the two ends run on every
	// pull request already; release candidates and branch tags are not releases.
	if want := []string{"11.8.5", "11.9.2", "11.10.3"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestANewerPatchOfAnEndIsRunUntilThePinMovesToIt(t *testing.T) {
	t.Parallel()
	// 11.7.12 is newer than the pinned ESR patch. It is in the window, so the
	// weekly run tests it until Dependabot's proposal moves the pin to it.
	got := Between([]string{"11.7.12", "11.11.1"}, Release{11, 7, 11}, Release{11, 11, 1})
	if len(got) != 1 || got[0] != (Release{11, 7, 12}) {
		t.Fatalf("got %v", got)
	}
}
