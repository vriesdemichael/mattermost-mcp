package server

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Mentions is a page of the posts that mention the user.
type Mentions struct {
	Posts       []Post   `json:"posts" jsonschema:"the posts, most recent first, each with its channel and team"`
	MentionKeys []string `json:"mention_keys" jsonschema:"what mentions the person, as their notification settings say: @username, and their first name, other words and @channel, @all and @here when they turned those on"`
	capped
	pageInfo
}

type listMentionsInput struct {
	searchFilters
	TeamID string `json:"team_id,omitempty" jsonschema:"only this team; every team the user is in when not given"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many posts a page holds, at most 100; 20 when not given"`
	pageArgs
}

func listMentionsSpec() Spec {
	coverage := func(teamID Coverage) map[string]Coverage {
		params := map[string]Coverage{
			"body.terms": Fixed("the person's mention keys",
				"what mentions the person is in their notification settings, as Mattermost's own Recent Mentions searches for them"),
			"body.is_or_search": Fixed("true", "a post mentions the person by any one of their mention keys"),
			"body.page":         Fixed("0", "Mattermost's database search, which Team Edition uses, answers the first page with every match"),
			"body.per_page":     Fixed("100", "the database search answers with its 100 most recent matches; the tool pages through them itself"),
			"body.time_zone_offset": Fixed("the offset of the person's timezone on the day given",
				"on:, before: and after: days are the person's own, as in Mattermost's search box; 0 when no day is given"),
			"body.include_deleted_channels": Omitted("archived channels are left out, as they are out of get_user_channels"),
		}
		if teamID.How != "" {
			params["team_id"] = teamID
		}
		return params
	}
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "list_mentions",
			Description: "List the posts that mention the user, most recent first, as Mattermost's Recent Mentions does: by @username, " +
				"and by their first name, other words and @channel, @all and @here when their notification settings say those mention them. " +
				"The user's own posts are left out. Use it for \"who mentioned me\" or \"what did I miss\"; from, in, before, after and on narrow it. " +
				"It finds the 100 most recent matches at most; capped says when it did, and before reaches older ones. " +
				"Mattermost's database search skips \"all\" and \"here\" as too common to index, so @all and @here are found only on a server that searches with Elasticsearch.",
			Annotations: readOnly("List mentions"),
		},
		uses([]Use{
			{
				Operation: "SearchPostsInAllTeams",
				Params:    coverage(Coverage{}),
				Releases: "11.7's router serves POST /api/v4/posts/search, as its route table shows; only its specification leaves it out. " +
					"The tool calls it the same way on every supported release, and the live suite runs it on both.",
			},
			{Operation: "SearchPosts", Params: coverage(SetBy("team_id"))},
		}, channelLookupUses("in"), fromUses(), describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listMentionsInput, Mentions] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listMentionsInput) (*mcp.CallToolResult, Mentions, error) {
				limit, err := limitOf(input.Limit, defaultPostsPerSearch, maxPostsPerSearch)
				if err != nil {
					return nil, Mentions{}, err
				}
				at, err := openCursor("list_mentions", input, input.Cursor)
				if err != nil {
					return nil, Mentions{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Mentions{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Mentions{}, err
				}
				keys := mentionKeys(self)
				terms, teamID, offset, err := searchTerms(ctx, client, searchable(keys), input.TeamID, input.searchFilters)
				if err != nil {
					return nil, Mentions{}, err
				}
				list, err := client.SearchPosts(ctx, mattermost.Search{
					TeamID: teamID, Terms: terms, MatchAny: true, Page: 0, PerPage: maxPostsPerSearch, TimeOffset: offset,
				})
				if err != nil {
					return nil, Mentions{}, err
				}
				all := newestFirst(orderedPosts(list, false))
				// The search matches words, so a post that says "here" matches
				// @here; only a post that mentions the person as Mattermost would
				// notify them is kept, and none of their own.
				found := slices.DeleteFunc(slices.Clone(all), func(post *model.Post) bool {
					return post.UserId == self.Id || !mentionsOf(keys).in(post.Message)
				})
				page, next := offsetPage(found, at, limit)
				posts, err := describePosts(ctx, client, page)
				if err != nil {
					return nil, Mentions{}, err
				}
				return nil, Mentions{Posts: posts, MentionKeys: keys, capped: capped{len(all) >= searchReach}, pageInfo: pageInfo{NextCursor: next}}, nil
			}
		},
	), withShapes(searchFilterShapes, pagingShapes))
}

// mentionKeys is what mentions the user, as their notification settings say
// and Mattermost's own Recent Mentions searches for: @username always, their
// first name and other words when they set them, and @channel, @all and @here
// unless they turned channel-wide mentions off.
func mentionKeys(user *model.User) []string {
	keys := []string{"@" + user.Username}
	if user.NotifyProps[model.FirstNameNotifyProp] == "true" && strings.TrimSpace(user.FirstName) != "" {
		keys = append(keys, strings.TrimSpace(user.FirstName))
	}
	for _, key := range user.GetMentionKeys() {
		if !slices.Contains(keys, key) && key != "@"+user.Username {
			keys = append(keys, key)
		}
	}
	if user.NotifyProps[model.ChannelMentionsNotifyProp] != "false" {
		keys = append(keys, "@channel", "@all", "@here")
	}
	return keys
}

// searchable is the keys as search terms, a key of several words quoted.
func searchable(keys []string) string {
	terms := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.ContainsAny(key, " \t") {
			key = strconv.Quote(key)
		}
		terms = append(terms, key)
	}
	return strings.Join(terms, " ")
}

// mentionMatcher finds whether a message mentions the person by any of their keys.
type mentionMatcher struct {
	names []string         // the @-mentions, without the @, lower case
	words []*regexp.Regexp // the other keys, as whole words
}

func mentionsOf(keys []string) mentionMatcher {
	var m mentionMatcher
	for _, key := range keys {
		if name, ok := strings.CutPrefix(key, "@"); ok {
			m.names = append(m.names, strings.ToLower(name))
			continue
		}
		m.words = append(m.words, regexp.MustCompile(`(?i)(^|[^\pL\pN_])`+regexp.QuoteMeta(key)+`($|[^\pL\pN_])`))
	}
	return m
}

// in reports whether message mentions the person: an @-mention as Mattermost
// reads one, which code does not hold, or another key as a whole word.
func (m mentionMatcher) in(message string) bool {
	for _, found := range mentions(message) {
		for _, name := range found.names {
			if slices.Contains(m.names, name) {
				return true
			}
		}
	}
	for _, word := range m.words {
		if word.MatchString(message) {
			return true
		}
	}
	return false
}
