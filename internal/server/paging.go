package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
)

// Paging (ADR-032). Every tool that answers with a list takes limit and cursor
// and answers with next_cursor, which is empty on the last page. A cursor is
// opaque to the model: it names the tool and the arguments that chose the
// list, so it continues that list and no other, and says where to continue,
// in whatever way the list's Mattermost endpoint pages: from a post, from a
// thread, by page, or by offset into an answer Mattermost gives whole.

// pageArgs is the cursor argument of every tool that lists.
type pageArgs struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"continue a list where the last answer stopped: pass its next_cursor, with the same other arguments"`
}

// pageInfo is what every list says of what follows it.
type pageInfo struct {
	NextCursor string `json:"next_cursor,omitempty" jsonschema:"more follow: pass this as cursor, with the same other arguments, for the next page; absent on the last page"`
}

// pagingShapes says what the paging arguments do, for a tool where they set no
// parameter of their own.
var pagingShapes = map[string]string{
	"cursor": "continues the list from where the answer that gave it stopped",
	"limit":  "returns at most this many, and a cursor for the rest",
}

// position is where a list continues.
type position struct {
	Tool  string `json:"t"`
	Query string `json:"q"`
	// Offset counts the items already returned, for a list read by page or
	// given whole.
	Offset int `json:"o,omitempty"`
	// After is the last item returned, and At its time, for a list read from
	// an item; Back says the list is read towards older items.
	After string `json:"a,omitempty"`
	At    int64  `json:"c,omitempty"`
	Back  bool   `json:"b,omitempty"`
}

// listQuery fingerprints what chose a list: the tool's input without its
// cursor and limit, so a cursor stays good when the page size changes and is
// refused for another list.
func listQuery(input any) string {
	value := reflect.ValueOf(input)
	copied := reflect.New(value.Type()).Elem()
	copied.Set(value)
	clearPaging(copied)
	encoded, _ := json.Marshal(copied.Interface())
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}

// clearPaging empties the cursor and limit fields of an input, embedded ones
// included.
func clearPaging(value reflect.Value) {
	if value.Kind() != reflect.Struct {
		return
	}
	for i := range value.NumField() {
		field := value.Field(i)
		switch name := value.Type().Field(i).Name; {
		case name == "Cursor" || name == "Limit":
			field.Set(reflect.Zero(field.Type()))
		case value.Type().Field(i).Anonymous:
			clearPaging(field)
		}
	}
}

// openCursor is where a list continues: its start when cursor is empty.
func openCursor(tool string, input any, cursor string) (position, error) {
	query := listQuery(input)
	if cursor == "" {
		return position{Tool: tool, Query: query}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	var at position
	if err == nil {
		err = json.Unmarshal(raw, &at)
	}
	switch {
	case err != nil:
		return position{}, fmt.Errorf("the cursor is not one %s gave; pass next_cursor as it came", tool)
	case at.Tool != tool:
		return position{}, fmt.Errorf("the cursor continues %s, not %s", at.Tool, tool)
	case at.Query != query:
		return position{}, fmt.Errorf("the cursor continues a list asked for with other arguments; pass the same arguments as the call that gave it, or leave cursor out to start over")
	}
	return at, nil
}

// String is the cursor that continues from here.
func (p position) String() string {
	encoded, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// limitOf is a tool's limit argument, its default when not given, refused
// beyond its maximum.
func limitOf(limit, fallback, most int) (int, error) {
	switch {
	case limit == 0:
		return fallback, nil
	case limit < 0 || limit > most:
		return 0, fmt.Errorf("limit must be between 1 and %d, not %d", most, limit)
	}
	return limit, nil
}

// offsetPage is a page of a list Mattermost gives whole, and the cursor for
// the rest.
func offsetPage[T any](all []T, at position, limit int) ([]T, string) {
	start := min(at.Offset, len(all))
	end := min(start+limit, len(all))
	page := all[start:end]
	if end == len(all) {
		return page, ""
	}
	at.Offset = end
	return page, at.String()
}

// serverPageSize is the most items Mattermost gives in one page, and what the
// tools ask for when they read a list's pages.
const serverPageSize = 200

// allPages is every item of a list Mattermost gives a page at a time. Its pages
// follow an order two items can share a place in, such as a time to the
// millisecond, and it settles such ties afresh for each request, so a page
// boundary can repeat one item and skip another. The whole list, put in an
// order with no ties, pages the same every time it is read.
func allPages[T any](ctx context.Context, fetch func(ctx context.Context, page, perPage int) ([]T, error)) ([]T, error) {
	var all []T
	for page := 0; ; page++ {
		items, err := fetch(ctx, page, serverPageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < serverPageSize {
			return all, nil
		}
	}
}

// wholeMilliseconds cuts a page Mattermost read on from a post, given in the
// order it reads on, the post it would go on from last, to at most limit items
// that end where a millisecond does, and says whether more follow. Mattermost
// reads on from a post by its time alone, so a page that ended inside a
// millisecond would lose the rest of it; cut back, the next page reads that
// millisecond whole. Mattermost is asked for one item more than the page holds
// where it can be, which says whether the page ends inside a millisecond; a
// page of Mattermost's most that more follow is cut back to before its last
// millisecond. A page all of one millisecond that more may follow cannot be
// cut back, and is answered as it is, not whole: the caller reads that
// millisecond whole with throughMillisecond.
func wholeMilliseconds[T any](page []T, limit int, more bool, at func(T) int64) (kept []T, follows, whole bool) {
	if len(page) > limit {
		next := page[limit]
		page, more = page[:limit], true
		if at(next) != at(page[limit-1]) {
			return page, true, true
		}
	}
	if !more || len(page) == 0 {
		return page, more, true
	}
	edge := at(page[len(page)-1])
	end := len(page)
	for end > 0 && at(page[end-1]) == edge {
		end--
	}
	if end == 0 {
		return page, true, false
	}
	return page[:end], true, true
}

// throughMillisecond is the items of a page, in the order it reads on, up to
// and with the last of the given millisecond, and whether more follow: a page
// that holds one millisecond whole, however many items were written in it.
func throughMillisecond[T any](page []T, edge int64, more bool, at func(T) int64) ([]T, bool) {
	end := 0
	for i, item := range page {
		if at(item) == edge {
			end = i + 1
		}
	}
	return page[:end], more || end < len(page)
}

// postTime is when a post was written, which Mattermost pages a channel by.
func postTime(post *model.Post) int64 { return post.CreateAt }

// backwards is a copy of items in the opposite order.
func backwards[T any](items []T) []T {
	reversed := slices.Clone(items)
	slices.Reverse(reversed)
	return reversed
}
