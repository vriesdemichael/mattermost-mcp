package server

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Finding something by the name a person calls it (ADR-030). A tool that takes
// a name matches it without regard to case: a whole match first, then a part.
// One match is the answer. More than one is refused with each candidate, so
// the model asks or chooses by id; none is refused with the closest names, so
// it corrects itself on the next call.

// named is one thing a name may mean, with every name it goes by.
type named[T any] struct {
	value T
	names []string // what it is called: its display name, its address name
	label string   // how a candidate is shown when the name is ambiguous
}

// match finds what name means among candidates. what says what is looked
// for, for the errors, and hint says where to look instead.
func match[T any](what, name string, candidates []named[T], hint string) (T, error) {
	var none T
	wanted := strings.ToLower(strings.TrimSpace(name))
	var whole, part []named[T]
	for _, candidate := range candidates {
		matchedWhole, matchedPart := false, false
		for _, n := range candidate.names {
			lower := strings.ToLower(n)
			if lower == wanted {
				matchedWhole = true
			} else if wanted != "" && strings.Contains(lower, wanted) {
				matchedPart = true
			}
		}
		switch {
		case matchedWhole:
			whole = append(whole, candidate)
		case matchedPart:
			part = append(part, candidate)
		}
	}
	for _, found := range [][]named[T]{whole, part} {
		switch {
		case len(found) == 1:
			return found[0].value, nil
		case len(found) > 1:
			labels := make([]string, 0, len(found))
			for _, candidate := range found {
				labels = append(labels, candidate.label)
			}
			slices.Sort(labels)
			return none, fmt.Errorf("%q could mean any of %d %ss: %s. Give the id of the one meant, or ask the person which",
				name, len(found), what, strings.Join(labels, "; "))
		}
	}
	var all []string
	for _, candidate := range candidates {
		all = append(all, candidate.names...)
	}
	if close := closest(name, all, 5); len(close) > 0 {
		return none, fmt.Errorf("no %s is called %q; the closest are %s. %s", what, name, quoteAll(close), hint)
	}
	return none, fmt.Errorf("no %s is called %q. %s", what, name, hint)
}

// closest is up to n of names nearest to name, by edit distance without
// regard to case, nearest first, leaving out those too far to be a slip.
func closest(name string, names []string, n int) []string {
	wanted := strings.ToLower(name)
	type scored struct {
		name     string
		distance int
	}
	seen := map[string]bool{}
	var near []scored
	for _, candidate := range names {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		lower := strings.ToLower(candidate)
		distance := editDistance(wanted, lower)
		if strings.Contains(lower, wanted) || strings.Contains(wanted, lower) {
			distance = min(distance, 1)
		}
		// Within a third of the longer name: a typo or a forgotten word, not
		// another name altogether.
		if distance*3 <= max(utf8.RuneCountInString(wanted), utf8.RuneCountInString(lower)) {
			near = append(near, scored{candidate, distance})
		}
	}
	slices.SortStableFunc(near, func(a, b scored) int {
		if a.distance != b.distance {
			return a.distance - b.distance
		}
		return strings.Compare(a.name, b.name)
	})
	out := make([]string, 0, min(n, len(near)))
	for _, s := range near[:min(n, len(near))] {
		out = append(out, s.name)
	}
	return out
}

// editDistance is the Levenshtein distance between a and b, in runes.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	previous := make([]int, len(rb)+1)
	current := make([]int, len(rb)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		current[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			current[j] = min(previous[j]+1, current[j-1]+1, previous[j-1]+cost)
		}
		previous, current = current, previous
	}
	return previous[len(rb)]
}

func quoteAll(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return strings.Join(quoted, ", ")
}
