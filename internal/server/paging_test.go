package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Paging, where no request is involved (ADR-032).

type pagedInput struct {
	Term  string `json:"term"`
	Limit int    `json:"limit"`
	pageArgs
}

func TestACursorContinuesOnlyItsOwnToolAndArguments(t *testing.T) {
	t.Parallel()
	asked := pagedInput{Term: "a", Limit: 5}
	at, err := openCursor("search", asked, "")
	if err != nil {
		t.Fatal(err)
	}
	at.Offset = 5
	cursor := at.String()

	// Another limit continues the same list.
	if back, err := openCursor("search", pagedInput{Term: "a", Limit: 50, pageArgs: pageArgs{Cursor: cursor}}, cursor); err != nil || back.Offset != 5 {
		t.Fatalf("the same list with another limit: %+v, %v", back, err)
	}
	for name, c := range map[string]struct {
		tool   string
		input  pagedInput
		cursor string
		says   string
	}{
		"another tool":     {"list", asked, cursor, "continues search"},
		"another term":     {"search", pagedInput{Term: "b"}, cursor, "other arguments"},
		"not a cursor":     {"search", asked, "%%%", "not one search gave"},
		"base64, not JSON": {"search", asked, "bm90IGpzb24", "not one search gave"},
	} {
		if _, err := openCursor(c.tool, c.input, c.cursor); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v; want it to say %q", name, err, c.says)
		}
	}
}

func TestAnOffsetPageIsASliceWithACursorForTheRest(t *testing.T) {
	t.Parallel()
	all := []int{1, 2, 3, 4, 5}
	at := position{Tool: "t", Query: "q"}
	var got []int
	for range 10 {
		page, next := offsetPage(all, at, 2)
		got = append(got, page...)
		if next == "" {
			break
		}
		var err error
		if at, err = openCursorFrom(next); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(got, all) {
		t.Fatalf("paged through %v", got)
	}
	if page, next := offsetPage(all, position{Offset: 9}, 2); len(page) != 0 || next != "" {
		t.Fatalf("past the end: %v, %q", page, next)
	}
}

// openCursorFrom decodes a cursor without checking whose it is.
func openCursorFrom(cursor string) (position, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return position{}, err
	}
	var at position
	return at, json.Unmarshal(raw, &at)
}

func TestAllPagesReadsUntilAPageIsNotFull(t *testing.T) {
	t.Parallel()
	total := serverPageSize*2 + 7
	var asked []int
	all, err := allPages(context.Background(), func(_ context.Context, page, perPage int) ([]int, error) {
		asked = append(asked, page)
		start := page * perPage
		var items []int
		for i := start; i < min(start+perPage, total); i++ {
			items = append(items, i)
		}
		return items, nil
	})
	if err != nil || len(all) != total || !slices.Equal(asked, []int{0, 1, 2}) {
		t.Fatalf("read %d items over pages %v: %v", len(all), asked, err)
	}
}

func TestALimitHasADefaultAndAMaximum(t *testing.T) {
	t.Parallel()
	if got, err := limitOf(0, 20, 100); got != 20 || err != nil {
		t.Errorf("no limit: %d, %v", got, err)
	}
	for _, limit := range []int{-1, 101} {
		if _, err := limitOf(limit, 20, 100); err == nil {
			t.Errorf("limit %d was taken", limit)
		}
	}
}

func TestAPageThatWouldEndInsideAMillisecondIsCutBackToAWholeOne(t *testing.T) {
	t.Parallel()
	at := func(ms int64) int64 { return ms }
	for name, c := range map[string]struct {
		page        []int64
		limit       int
		more        bool
		want        []int64
		follows     bool
		whole       bool
		wantThrough []int64
	}{
		"the post past the page is a later millisecond": {[]int64{1, 2, 3, 4}, 3, false, []int64{1, 2, 3}, true, true, nil},
		"the post past the page shares its last one":    {[]int64{1, 2, 3, 3}, 3, false, []int64{1, 2}, true, true, nil},
		"the last page": {[]int64{1, 2, 2}, 3, false, []int64{1, 2, 2}, false, true, nil},
		"a page of Mattermost's most, more following": {[]int64{1, 2, 2}, 3, true, []int64{1}, true, true, nil},
		"one millisecond fuller than the page":        {[]int64{5, 5, 5, 5}, 3, false, []int64{5, 5, 5}, true, false, []int64{5, 5, 5, 5}},
		"empty":                                       {nil, 3, false, nil, false, true, nil},
	} {
		got, follows, whole := wholeMilliseconds(c.page, c.limit, c.more, at)
		if !slices.Equal(got, c.want) || follows != c.follows || whole != c.whole {
			t.Errorf("%s: got %v, more %v, whole %v; want %v, %v, %v", name, got, follows, whole, c.want, c.follows, c.whole)
		}
		if !whole {
			if through, _ := throughMillisecond(c.page, got[0], false, at); !slices.Equal(through, c.wantThrough) {
				t.Errorf("%s: read through the millisecond as %v; want %v", name, through, c.wantThrough)
			}
		}
	}
	if got, more := throughMillisecond([]int64{5, 5, 6}, 5, false, at); !slices.Equal(got, []int64{5, 5}) || !more {
		t.Errorf("through millisecond 5 of 5, 5, 6: got %v, more %v", got, more)
	}
}
