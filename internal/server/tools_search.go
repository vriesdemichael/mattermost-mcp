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

// The most posts a page of a search holds, and how many when not told; and
// the most matches Mattermost's database search, which Team Edition uses,
// answers with: the most recent searchReach, all at once whatever the page
// size, which the tool pages through itself.
const (
	maxPostsPerSearch     = 100
	defaultPostsPerSearch = 20
	searchReach           = 100
)

// capped is what a search says when it found as many as Mattermost's search
// gives, which means older matches may exist that no page reaches.
type capped struct {
	Capped bool `json:"capped,omitempty" jsonschema:"Mattermost's search answered with the most it gives, the 100 most recent matches; older ones may exist that no page reaches: narrow the search, for instance with before"`
}

// SearchResults is a page of posts a search found.
type SearchResults struct {
	Posts []Post `json:"posts" jsonschema:"the posts, most recent first, each with its channel and team"`
	capped
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

// fromUses are the operations that check a search's from filter names someone.
func fromUses() []Use {
	return []Use{
		{Operation: "GetUsersByUsernames", Params: map[string]Coverage{}},
		{Operation: "SearchUsers", Params: suggestionSearch(Fixed("starts of an unknown username, longest first", "finds the usernames closest to one that is unknown, among however many share its first letters"))},
	}
}

// mattermostID is the shape of every id Mattermost gives out.
var mattermostID = regexp.MustCompile(`^[a-z0-9]{26}$`)

// searchTerms is the terms with each filter written in Mattermost's syntax,
// and the team to search. A channel's name means a channel only within its
// team, and Mattermost's search across every team ignores in:, so a search in
// a channel finds the channel, by id or by name, and searches its team.
func searchTerms(ctx context.Context, client *mattermost.Client, terms, teamID string, filters searchFilters) (string, string, int, error) {
	parts := []string{strings.TrimSpace(terms)}
	if from := strings.TrimPrefix(strings.TrimSpace(filters.From), "@"); from != "" {
		if strings.Contains(from, "@") {
			return "", "", 0, fmt.Errorf("from takes a username, not %q; get_users finds the username of an email address", from)
		}
		// Mattermost searches for a username nobody has and finds nothing; the
		// tool refuses it with the closest usernames instead.
		people, err := lookUpUsers(ctx, client, []string{from})
		if err != nil {
			return "", "", 0, err
		}
		parts = append(parts, "from:"+people[0].Username)
	}
	if strings.TrimSpace(filters.In) != "" {
		channel, err := findChannel(ctx, client, filters.In, teamID)
		if err != nil {
			return "", "", 0, err
		}
		// Mattermost searches only the channels the person belongs to.
		if channel.Member != nil && !*channel.Member {
			return "", "", 0, fmt.Errorf("%s is a channel the person does not belong to, and Mattermost searches only their own; read it with read_channel instead", oneLine(channel.DisplayName))
		}
		parts = append(parts, "in:"+channel.Name)
		if channel.TeamID != "" {
			teamID = channel.TeamID
		}
	}
	var day time.Time
	for _, filter := range []struct{ name, value string }{{"before", filters.Before}, {"after", filters.After}, {"on", filters.On}} {
		if filter.value == "" {
			continue
		}
		parsed, err := time.Parse(time.DateOnly, filter.value)
		if err != nil {
			return "", "", 0, fmt.Errorf("%s must be a day as YYYY-MM-DD, not %q", filter.name, filter.value)
		}
		day = parsed
		parts = append(parts, filter.name+":"+filter.value)
	}
	joined := strings.TrimSpace(strings.Join(parts, " "))
	if joined == "" {
		return "", "", 0, fmt.Errorf("give terms to search for, or a filter such as from or in")
	}
	offset := 0
	if !day.IsZero() {
		// A day is the person's own day, as it is in Mattermost's search box,
		// not UTC's: "yesterday" ends at their midnight.
		self, err := client.Me(ctx)
		if err != nil {
			return "", "", 0, err
		}
		zone, err := userZone(self)
		if err != nil {
			return "", "", 0, err
		}
		_, offset = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, zone).Zone()
	}
	return joined, teamID, offset, nil
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
			"body.per_page":                 Fixed("100", "the database search ignores it and answers with its 100 most recent matches; the tool pages through them itself"),
			"body.time_zone_offset":         Fixed("the offset of the person's timezone on the day given", "on:, before: and after: days are the person's own, as in Mattermost's search box; 0 when no day is given"),
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
				"in which channel and when, through from, in, before, after and on. Search for @username to find where someone was mentioned. " +
				"Mattermost's search finds the 100 most recent matches at most; capped says when it did, and before reaches older ones.",
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
		}, channelLookupUses("in", SetBy("team_id")), fromUses(), describeUses(true)),
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
				terms, teamID, offset, err := searchTerms(ctx, client, input.Terms, input.TeamID, input.searchFilters)
				if err != nil {
					return nil, SearchResults{}, err
				}
				list, err := client.SearchPosts(ctx, mattermost.Search{
					TeamID: teamID, Terms: terms, MatchAny: input.MatchAny, Page: 0, PerPage: maxPostsPerSearch, TimeOffset: offset,
				})
				if err != nil {
					return nil, SearchResults{}, err
				}
				// The database search answers with its matches at once; a page is a
				// slice of them, which a search run again for the next page finds the
				// same unless posts were written or deleted since.
				found := newestFirst(orderedPosts(list, false))
				page, next := offsetPage(found, at, limit)
				posts, err := describePosts(ctx, client, page)
				if err != nil {
					return nil, SearchResults{}, err
				}
				return nil, SearchResults{Posts: posts, capped: capped{len(found) >= searchReach}, pageInfo: pageInfo{NextCursor: next}}, nil
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
