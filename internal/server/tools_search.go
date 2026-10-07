package server

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// The most posts one search returns, and how many when not told. 100 is also
// what the tool asks Mattermost for: its database search, which Team Edition
// uses, answers with every match on the first page and ignores the page size,
// so the limit is applied to the answer.
const (
	maxPostsPerSearch     = 100
	defaultPostsPerSearch = 20
)

// SearchResults is a page of posts a search found.
type SearchResults struct {
	Posts []Post `json:"posts" jsonschema:"the posts, most recent first, each with its channel and team"`
	pageInfo
}

// searchFilters narrow a search, so the model never writes Mattermost's
// search syntax for them. search_posts and search_files share them.
type searchFilters struct {
	From   string `json:"from,omitempty" jsonschema:"only what this username wrote or posted"`
	In     string `json:"in,omitempty" jsonschema:"only in this channel: its id, or its name as in its address, such as town-square"`
	Before string `json:"before,omitempty" jsonschema:"only before this day, as YYYY-MM-DD"`
	After  string `json:"after,omitempty" jsonschema:"only after this day, as YYYY-MM-DD"`
	On     string `json:"on,omitempty" jsonschema:"only on this day, as YYYY-MM-DD"`
}

// searchFilterShapes says what each filter but in, which sets parameters of
// its own, does to the terms.
var searchFilterShapes = map[string]string{
	"from":   "adds from: to the terms",
	"before": "adds before: to the terms",
	"after":  "adds after: to the terms",
	"on":     "adds on: to the terms",
}

// mattermostID is the shape of every id Mattermost gives out.
var mattermostID = regexp.MustCompile(`^[a-z0-9]{26}$`)

// searchTerms is the terms with each filter written in Mattermost's syntax,
// and the team to search. A channel's name means a channel only within its
// team, and Mattermost's search across every team ignores in:, so a search in
// a channel finds the channel, by id or by name, and searches its team.
func searchTerms(ctx context.Context, client *mattermost.Client, terms, teamID string, filters searchFilters) (string, string, error) {
	parts := []string{strings.TrimSpace(terms)}
	if from := strings.TrimPrefix(strings.TrimSpace(filters.From), "@"); from != "" {
		parts = append(parts, "from:"+from)
	}
	if strings.TrimSpace(filters.In) != "" {
		channel, err := findChannel(ctx, client, filters.In, teamID)
		if err != nil {
			return "", "", err
		}
		parts = append(parts, "in:"+channel.Name)
		if channel.TeamID != "" {
			teamID = channel.TeamID
		}
	}
	for _, day := range []struct{ name, value string }{{"before", filters.Before}, {"after", filters.After}, {"on", filters.On}} {
		if day.value == "" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, day.value); err != nil {
			return "", "", fmt.Errorf("%s must be a day as YYYY-MM-DD, not %q", day.name, day.value)
		}
		parts = append(parts, day.name+":"+day.value)
	}
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" {
		return "", "", fmt.Errorf("give terms to search for, or a filter such as from or in")
	}
	return joined, teamID, nil
}

type searchPostsInput struct {
	Terms string `json:"terms,omitempty" jsonschema:"words to search for: \"a quoted phrase\", -excluded, #hashtag, and @username for posts that mention someone"`
	searchFilters
	TeamID   string `json:"team_id,omitempty" jsonschema:"search only this team; every team the user is in when not given"`
	MatchAny bool   `json:"match_any,omitempty" jsonschema:"find posts with any of the words rather than all of them"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many posts a page holds, at most 100; 20 when not given"`
	pageArgs
}

func searchPostsSpec() Spec {
	coverage := func(teamID Coverage) map[string]Coverage {
		params := map[string]Coverage{
			"body.terms":        SetBy("terms"),
			"body.is_or_search": SetBy("match_any"),
			"body.page": Fixed("0", "Mattermost's database search, which Team Edition uses, answers the first page with every match "+
				"and later pages with nothing; only Elasticsearch, a licensed feature, pages (the live suite shows both)"),
			"body.per_page":                 Fixed("100", "the database search ignores it and answers with every match; the tool pages through that answer itself"),
			"body.time_zone_offset":         Fixed("0", "on:, before: and after: dates are read in UTC, the zone every time the tools return is in"),
			"body.include_deleted_channels": Omitted("archived channels are left out of a search, as they are out of get_user_channels"),
		}
		if teamID.How != "" {
			params["team_id"] = teamID
		}
		return params
	}
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "search_posts",
			Description: "Search the messages the user can read, across every team or one: by words, and by who wrote them, " +
				"in which channel and when, through from, in, before, after and on. Search for @username to find where someone was mentioned.",
			Annotations: readOnly("Search posts"),
		},
		uses([]Use{
			{
				Operation: "SearchPostsInAllTeams",
				Params:    coverage(Coverage{}),
				Releases: "11.7's router serves POST /api/v4/posts/search, as its route table shows; only its specification leaves it out. " +
					"The tool calls it the same way on every supported release, and the live suite runs it on both.",
			},
			{Operation: "SearchPosts", Params: coverage(SetBy("team_id"))},
		}, channelLookupUses("in"), describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[searchPostsInput, SearchResults] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input searchPostsInput) (*mcp.CallToolResult, SearchResults, error) {
				limit, err := limitOf(input.Limit, defaultPostsPerSearch, maxPostsPerSearch)
				if err != nil {
					return nil, SearchResults{}, err
				}
				at, err := openCursor("search_posts", input, input.Cursor)
				if err != nil {
					return nil, SearchResults{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, SearchResults{}, err
				}
				terms, teamID, err := searchTerms(ctx, client, input.Terms, input.TeamID, input.searchFilters)
				if err != nil {
					return nil, SearchResults{}, err
				}
				list, err := client.SearchPosts(ctx, mattermost.Search{
					TeamID: teamID, Terms: terms, MatchAny: input.MatchAny, Page: 0, PerPage: maxPostsPerSearch,
				})
				if err != nil {
					return nil, SearchResults{}, err
				}
				// The database search answers with every match at once; a page is a
				// slice of it, which a search run again for the next page finds the
				// same unless posts were written or deleted since.
				page, next := offsetPage(newestFirst(orderedPosts(list, false)), at, limit)
				posts, err := describePosts(ctx, client, page)
				if err != nil {
					return nil, SearchResults{}, err
				}
				return nil, SearchResults{Posts: posts, pageInfo: pageInfo{NextCursor: next}}, nil
			}
		},
	), withShapes(searchFilterShapes, pagingShapes))
}

// withShapes joins maps of shaping arguments.
func withShapes(groups ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, group := range groups {
		maps.Copy(out, group)
	}
	return out
}
