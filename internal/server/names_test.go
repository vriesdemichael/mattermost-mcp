package server

import (
	"slices"
	"strings"
	"testing"
)

// Finding by name, where no request is involved (ADR-030).

func candidates(pairs ...[2]string) []named[string] {
	var out []named[string]
	for _, pair := range pairs {
		out = append(out, named[string]{value: pair[0], names: []string{pair[0], pair[1]}, label: pair[1] + " (" + pair[0] + ")"})
	}
	return out
}

var channelsByName = candidates(
	[2]string{"town-square", "Town Square"},
	[2]string{"dev-backend", "Backend Developers"},
	[2]string{"dev-frontend", "Frontend Developers"},
	[2]string{"random", "Random"},
)

func TestAWholeNameIsFoundWithoutRegardToCase(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"town-square", "TOWN SQUARE", " Random "} {
		if _, err := match("channel", name, channelsByName, ""); err != nil {
			t.Errorf("%q: %v", name, err)
		}
	}
}

func TestAPartOfOneNameFindsIt(t *testing.T) {
	t.Parallel()
	if got, err := match("channel", "backend", channelsByName, ""); err != nil || got != "dev-backend" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAWholeNameWinsOverPartsOfOthers(t *testing.T) {
	t.Parallel()
	list := candidates([2]string{"dev", "Dev"}, [2]string{"dev-ops", "Dev Ops"})
	if got, err := match("channel", "dev", list, ""); err != nil || got != "dev" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestAnAmbiguousNameListsEveryCandidate(t *testing.T) {
	t.Parallel()
	_, err := match("channel", "developers", channelsByName, "")
	if err == nil || !strings.Contains(err.Error(), "Backend Developers (dev-backend)") || !strings.Contains(err.Error(), "Frontend Developers (dev-frontend)") {
		t.Fatalf("got %v", err)
	}
}

func TestAnUnknownNameGivesTheClosestOnes(t *testing.T) {
	t.Parallel()
	_, err := match("channel", "town-sqare", channelsByName, "Try search_channels.")
	if err == nil || !strings.Contains(err.Error(), `"town-square"`) || !strings.Contains(err.Error(), "Try search_channels.") {
		t.Fatalf("got %v", err)
	}
	if _, err := match("channel", "zzzzzzzz", channelsByName, "Try search_channels."); err == nil || strings.Contains(err.Error(), "closest") {
		t.Fatalf("a name like none of them got %v", err)
	}
}

func TestClosestKeepsSlipsAndLeavesOtherNames(t *testing.T) {
	t.Parallel()
	got := closest("thumbsup", []string{"thumbsdown", "+1", "thumbs_up", "tada", "thumbsup_all"}, 3)
	if !slices.Equal(got, []string{"thumbs_up", "thumbsup_all"}) {
		t.Fatalf("got %v", got)
	}
}

func TestASuggestionSearchesLongerStartsFirst(t *testing.T) {
	t.Parallel()
	for name, want := range map[string][]string{
		"jonh.smith": {"jonh.smit", "jonh.sm", "jonh.", "jon"},
		"bob":        {"bo"},
		"al":         {"a"},
		"":           nil,
	} {
		if got := prefixes(name); !slices.Equal(got, want) {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
	// A page cut short at its limit is not the end: the shorter start is
	// searched too, and the closest of everything found is the answer.
	searched := []string{}
	near := nearestBySearch("jonh.smith", 1, nil, func(term string) ([]string, bool) {
		searched = append(searched, term)
		switch term {
		case "jonh.":
			return []string{"jonh.smyth-2"}, false
		case "jon":
			return []string{"john.smith"}, true
		}
		return nil, true
	})
	if !slices.Equal(near, []string{"john.smith"}) || !slices.Equal(searched, []string{"jonh.smit", "jonh.sm", "jonh.", "jon"}) {
		t.Errorf("found %v after searching %v", near, searched)
	}
	// A complete page with a close name ends the searches.
	searched = searched[:0]
	nearestBySearch("jonh.smith", 1, nil, func(term string) ([]string, bool) {
		searched = append(searched, term)
		return []string{"john.smith"}, true
	})
	if len(searched) != 1 {
		t.Errorf("searched %v after a complete page with a close name", searched)
	}
}
