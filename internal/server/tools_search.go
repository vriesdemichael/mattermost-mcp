package server

import (
	"context"
	"fmt"
	"strings"

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

// FoundPost is a post a search found, with the channel it is in.
type FoundPost struct {
	Post
	Channel string `json:"channel" jsonschema:"the channel's display name, or the other person's username for a direct message"`
}

// SearchResults is what a search found.
type SearchResults struct {
	Posts []FoundPost `json:"posts" jsonschema:"the posts found, as Mattermost ranks them, most recent first"`
	// Truncated says more matched than the limit let through.
	Truncated bool `json:"truncated" jsonschema:"more posts matched than the limit let through: narrow the terms, or raise the limit"`
}

type searchPostsInput struct {
	Terms    string `json:"terms" jsonschema:"what to search for, in Mattermost's search syntax: words, \"a quoted phrase\", -excluded, #hashtag, @username for mentions, and from:username, in:channel-name, on:, before: and after: with a YYYY-MM-DD date"`
	TeamID   string `json:"team_id,omitempty" jsonschema:"search only this team; every team the user is in when not given"`
	MatchAny bool   `json:"match_any,omitempty" jsonschema:"find posts with any of the words rather than all of them"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many posts to return, at most 100; 20 when not given"`
}

func searchPostsSpec() Spec {
	coverage := func(teamID Coverage) map[string]Coverage {
		params := map[string]Coverage{
			"body.terms":        SetBy("terms"),
			"body.is_or_search": SetBy("match_any"),
			"body.page": Fixed("0", "Mattermost's database search, which Team Edition uses, answers the first page with every match "+
				"and later pages with nothing; only Elasticsearch, a licensed feature, pages (the live suite shows both)"),
			"body.per_page":                 Fixed("100", "the database search ignores it; the tool asks for its own maximum and applies limit to the answer"),
			"body.time_zone_offset":         Fixed("0", "on:, before: and after: dates are read in UTC, the zone every time the tools return is in"),
			"body.include_deleted_channels": Omitted("archived channels are left out of a search, as they are out of list_channels"),
		}
		if teamID.How != "" {
			params["team_id"] = teamID
		}
		return params
	}
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "search_posts",
			Description: "Search the messages this server's user can read, across every team or one, with Mattermost's search syntax. " +
				"Search for @username to find mentions of someone, and from:username for what they wrote.",
			Annotations: readOnly("Search posts"),
		},
		[]Use{
			{
				Operation: "SearchPostsInAllTeams",
				Params:    coverage(Coverage{}),
				Releases: "11.7's router serves POST /api/v4/posts/search, as its route table shows; only its specification leaves it out. " +
					"The tool calls it the same way on every supported release, and the live suite runs it on both.",
			},
			{Operation: "SearchPosts", Params: coverage(SetBy("team_id"))},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the user's own id tells which side of a direct message is the other person")},
			},
			{
				Operation: "GetChannelsForUser",
				Params: map[string]Coverage{
					"user_id":         Fixed("me", "a result names the channel it is in, from the channels the user belongs to"),
					"last_delete_at":  Fixed("0", "archived channels are left out of a search"),
					"include_deleted": Omitted("archived channels are left out of a search"),
				},
			},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each author's username, whenever they changed")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[searchPostsInput, SearchResults] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input searchPostsInput) (*mcp.CallToolResult, SearchResults, error) {
				limit := input.Limit
				switch {
				case strings.TrimSpace(input.Terms) == "":
					return nil, SearchResults{}, fmt.Errorf("give terms to search for")
				case limit == 0:
					limit = defaultPostsPerSearch
				case limit < 0 || limit > maxPostsPerSearch:
					return nil, SearchResults{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxPostsPerSearch, limit)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, SearchResults{}, err
				}
				list, err := client.SearchPosts(ctx, mattermost.Search{
					TeamID: input.TeamID, Terms: input.Terms, MatchAny: input.MatchAny, Page: 0, PerPage: maxPostsPerSearch,
				})
				if err != nil {
					return nil, SearchResults{}, err
				}
				posts, err := listedPosts(ctx, client, list, false)
				if err != nil {
					return nil, SearchResults{}, err
				}
				names, err := channelNames(ctx, client)
				if err != nil {
					return nil, SearchResults{}, err
				}
				out := SearchResults{Posts: []FoundPost{}, Truncated: len(posts) > limit}
				for _, post := range posts {
					if len(out.Posts) == limit {
						break
					}
					out.Posts = append(out.Posts, FoundPost{Post: post, Channel: names[post.ChannelID]})
				}
				return nil, out, nil
			}
		},
	), map[string]string{
		"limit": "returns at most this many of the posts Mattermost found, and says so when it cut some off",
	})
}

// channelNames is the name a person knows each of the user's channels by: its
// display name, or the other person's username for a direct message.
func channelNames(ctx context.Context, client *mattermost.Client) (map[string]string, error) {
	self, err := client.Me(ctx)
	if err != nil {
		return nil, err
	}
	channels, err := client.Channels(ctx)
	if err != nil {
		return nil, err
	}
	var others []string
	for _, channel := range channels {
		others = append(others, channel.GetOtherUserIdForDM(self.Id))
	}
	people, err := usernames(ctx, client, others)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(channels))
	for _, channel := range channels {
		names[channel.Id] = toChannel(channel, self.Id, people).DisplayName
	}
	return names, nil
}
