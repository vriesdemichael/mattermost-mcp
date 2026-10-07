package server

import (
	"context"
	"fmt"
	"maps"
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
	RootID    string `json:"root_id" jsonschema:"the post that started the thread; read_thread reads it whole"`
	ChannelID string `json:"channel_id"`
	Channel   string `json:"channel" jsonschema:"the channel's display name, or the other person's username for a direct message"`
	// Started is the thread's first post.
	Started        Post     `json:"started" jsonschema:"the post that started the thread"`
	ReplyCount     int64    `json:"reply_count"`
	LastReplyAt    string   `json:"last_reply_at,omitempty"`
	Participants   []string `json:"participants" jsonschema:"the usernames of the people who replied"`
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

// channelNameUses are the operations channelNames calls.
func channelNameUses(why string) []Use {
	return []Use{
		{
			Operation: "GetUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "the user's own id tells which side of a direct message is the other person")},
		},
		{
			Operation: "GetChannelsForUser",
			Params: map[string]Coverage{
				"user_id":         Fixed("me", why),
				"last_delete_at":  Fixed("0", "archived channels are left out, as they are out of list_channels"),
				"include_deleted": Omitted("archived channels are left out, as they are out of list_channels"),
			},
		},
		{
			Operation: "GetUsersByIds",
			Params:    map[string]Coverage{"since": Omitted("the tool reads each username, whenever it changed")},
		},
	}
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
		append([]Use{
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
		}, channelNameUses("a thread names the channel it is in, from the channels the user belongs to")...),
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
				names, err := channelNames(ctx, client)
				if err != nil {
					return nil, Threads{}, err
				}
				authors := make([]string, 0, len(followed))
				for _, thread := range followed {
					authors = append(authors, thread.Post.UserId)
				}
				people, err := usernames(ctx, client, authors)
				if err != nil {
					return nil, Threads{}, err
				}
				out := Threads{Threads: []ThreadSummary{}}
				for _, thread := range followed {
					if input.UnreadOnly && thread.UnreadReplies == 0 {
						continue
					}
					if len(out.Threads) == limit {
						out.Truncated = true
						break
					}
					out.Threads = append(out.Threads, toThreadSummary(thread, names, people))
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

// toThreadSummary is a followed thread as list_threads returns it, with authors'
// usernames from people and from the participants Mattermost lists whole.
func toThreadSummary(thread *model.ThreadResponse, channels, people map[string]string) ThreadSummary {
	names := maps.Clone(people)
	participants := []string{}
	for _, user := range thread.Participants {
		if user == nil {
			continue
		}
		names[user.Id] = user.Username
		participants = append(participants, user.Username)
	}
	return ThreadSummary{
		RootID:         thread.PostId,
		ChannelID:      thread.Post.ChannelId,
		Channel:        channels[thread.Post.ChannelId],
		Started:        toPost(thread.Post, names),
		ReplyCount:     thread.ReplyCount,
		LastReplyAt:    timestamp(thread.LastReplyAt),
		Participants:   participants,
		UnreadReplies:  thread.UnreadReplies,
		UnreadMentions: thread.UnreadMentions,
	}
}
