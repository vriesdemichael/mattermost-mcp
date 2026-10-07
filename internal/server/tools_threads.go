package server

import (
	"context"
	"fmt"
	"slices"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// How many followed threads list_threads reads from each team: Mattermost's
// most recently active, which is where unread replies are.
const threadsPerTeam = 100

// The most threads one list_threads call returns, and how many when not told.
const (
	maxThreads     = 100
	defaultThreads = 20
)

// ThreadSummary is a thread the user follows, as the threads view shows it.
type ThreadSummary struct {
	RootID string `json:"root_id" jsonschema:"the post that started the thread; read_post reads it with every reply"`
	// Started is the thread's first post, with its channel and team.
	Started        Post     `json:"started" jsonschema:"the post that started the thread, with its channel and team"`
	ReplyCount     int64    `json:"reply_count"`
	LastReplyAt    string   `json:"last_reply_at,omitempty"`
	Participants   []string `json:"participants" jsonschema:"the usernames of the people who took part"`
	UnreadReplies  int64    `json:"unread_replies" jsonschema:"replies the person has not read in Mattermost"`
	UnreadMentions int64    `json:"unread_mentions" jsonschema:"unread replies that mention the person"`
}

// Threads is the threads the user follows.
type Threads struct {
	Threads []ThreadSummary `json:"threads" jsonschema:"most recently replied to first"`
	// Truncated says more threads matched than the limit let through.
	Truncated bool `json:"truncated" jsonschema:"more threads matched than the limit let through"`
}

type listThreadsInput struct {
	TeamID     string `json:"team_id,omitempty" jsonschema:"keep to one team's threads, with those in direct and group messages; every team's when not given"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"keep the threads with replies the person has not read"`
	Limit      int    `json:"limit,omitempty" jsonschema:"how many threads to return, at most 100; 20 when not given"`
}

func listThreadsSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "list_threads",
			Description: "The threads the user follows, as Mattermost's threads view lists them: the threads they started, replied to, " +
				"were mentioned in or chose to follow, most recently replied to first, each with who took part and how many replies and " +
				"mentions the person has not read. unread_only keeps those with unread replies. Reading them here marks nothing read.",
			Annotations: readOnly("List threads"),
		},
		uses([]Use{
			{
				Operation: "GetUserThreads",
				Params: map[string]Coverage{
					"user_id":     Fixed("me", "the threads are the ones the user follows"),
					"team_id":     SetBy("team_id"),
					"per_page":    Fixed(fmt.Sprint(threadsPerTeam), "the most recently active threads of each team, which is where unread replies are; the tool keeps the limit itself"),
					"extended":    Fixed("true", "each thread says who took part"),
					"since":       Omitted("a sync for clients that already hold the threads"),
					"deleted":     Omitted("a deleted thread is gone for the person too"),
					"page":        Omitted("the tool reads the most recently active threads, which one page holds"),
					"totalsOnly":  Omitted("the tool returns the threads, and counts what it returns"),
					"threadsOnly": Omitted("Mattermost's totals come with the threads at no cost to the answer"),
				},
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "without team_id, every team the user belongs to is read")},
			},
		}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listThreadsInput, Threads] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listThreadsInput) (*mcp.CallToolResult, Threads, error) {
				limit := input.Limit
				switch {
				case limit == 0:
					limit = defaultThreads
				case limit < 0 || limit > maxThreads:
					return nil, Threads{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxThreads, limit)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Threads{}, err
				}
				followed, err := followedThreads(ctx, client, input.TeamID)
				if err != nil {
					return nil, Threads{}, err
				}
				var kept []*model.ThreadResponse
				truncated := false
				for _, thread := range followed {
					if input.UnreadOnly && thread.UnreadReplies == 0 {
						continue
					}
					if len(kept) == limit {
						truncated = true
						break
					}
					kept = append(kept, thread)
				}
				started := make([]*model.Post, 0, len(kept))
				for _, thread := range kept {
					started = append(started, thread.Post)
				}
				posts, err := describePosts(ctx, client, started)
				if err != nil {
					return nil, Threads{}, err
				}
				out := Threads{Threads: make([]ThreadSummary, 0, len(kept)), Truncated: truncated}
				for i, thread := range kept {
					out.Threads = append(out.Threads, toThreadSummary(thread, posts[i]))
				}
				return nil, out, nil
			}
		},
	), map[string]string{
		"unread_only": "keeps the threads Mattermost counts unread replies in",
		"limit":       "returns at most this many of the threads Mattermost listed, and says so when it cut some off",
	})
}

// followedThreads is the threads the user follows in one team, or in all of
// theirs, each once, most recently replied to first. A thread in a direct or
// group message is listed under every team, so it is kept once.
func followedThreads(ctx context.Context, client *mattermost.Client, teamID string) ([]*model.ThreadResponse, error) {
	self, err := client.Me(ctx)
	if err != nil {
		return nil, err
	}
	teams := []string{teamID}
	if teamID == "" {
		all, err := client.Teams(ctx)
		if err != nil {
			return nil, err
		}
		teams = teams[:0]
		for _, team := range all {
			teams = append(teams, team.Id)
		}
	}
	seen := map[string]bool{}
	var threads []*model.ThreadResponse
	for _, team := range teams {
		page, err := client.Threads(ctx, self.Id, team, threadsPerTeam)
		if err != nil {
			return nil, err
		}
		for _, thread := range page.Threads {
			if !seen[thread.PostId] && thread.Post != nil {
				seen[thread.PostId] = true
				threads = append(threads, thread)
			}
		}
	}
	slices.SortStableFunc(threads, func(a, b *model.ThreadResponse) int {
		switch {
		case a.LastReplyAt > b.LastReplyAt:
			return -1
		case a.LastReplyAt < b.LastReplyAt:
			return 1
		default:
			return 0
		}
	})
	return threads, nil
}

// toThreadSummary is a followed thread as list_threads returns it. Mattermost's
// extended answer carries each participant whole.
func toThreadSummary(thread *model.ThreadResponse, started Post) ThreadSummary {
	participants := []string{}
	for _, user := range thread.Participants {
		if user != nil {
			participants = append(participants, user.Username)
		}
	}
	return ThreadSummary{
		RootID:         thread.PostId,
		Started:        started,
		ReplyCount:     thread.ReplyCount,
		LastReplyAt:    timestamp(thread.LastReplyAt),
		Participants:   participants,
		UnreadReplies:  thread.UnreadReplies,
		UnreadMentions: thread.UnreadMentions,
	}
}
