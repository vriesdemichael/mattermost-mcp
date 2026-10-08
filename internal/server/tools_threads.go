package server

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

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

// Threads is a page of the threads the user follows.
type Threads struct {
	Threads []ThreadSummary `json:"threads" jsonschema:"most recently replied to first"`
	pageInfo
}

type listThreadsInput struct {
	TeamID     string `json:"team_id,omitempty" jsonschema:"keep to one team's threads, with those in direct and group messages; every team's when not given"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"keep the threads with replies the person has not read"`
	Limit      int    `json:"limit,omitempty" jsonschema:"how many threads a page holds, at most 100; 20 when not given"`
	pageArgs
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
					"user_id":  Fixed("me", "the threads are the ones the user follows"),
					"team_id":  SetBy("team_id"),
					"per_page": Fixed(fmt.Sprint(serverPageSize), "the tool reads every followed thread, a page of Mattermost's at a time, and pages through them itself"),
					"before": Undocumented("", "getThreadsForUser in server/channels/api4/user.go reads before, the last thread of the page before, and pages by it; "+
						"the specification leaves it out"),
					"extended": Fixed("true", "each thread says who took part"),
					"since":    Omitted("a sync for clients that already hold the threads"),
					"deleted":  Omitted("a deleted thread is gone for the person too"),
					"page": Omitted("the specification documents it, but getThreadsForUser does not read it: Mattermost pages threads by before, " +
						"which the tool sends instead"),
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
				limit, err := limitOf(input.Limit, defaultThreads, maxThreads)
				if err != nil {
					return nil, Threads{}, err
				}
				at, err := openCursor("list_threads", input, input.Cursor)
				if err != nil {
					return nil, Threads{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Threads{}, err
				}
				followed, err := followedThreads(ctx, client, input.TeamID)
				if err != nil {
					return nil, Threads{}, err
				}
				var matching []*model.ThreadResponse
				for _, thread := range followed {
					if !input.UnreadOnly || thread.UnreadReplies > 0 {
						matching = append(matching, thread)
					}
				}
				kept, next := offsetPage(matching, at, limit)
				started := make([]*model.Post, 0, len(kept))
				for _, thread := range kept {
					started = append(started, thread.Post)
				}
				posts, err := describePosts(ctx, client, started)
				if err != nil {
					return nil, Threads{}, err
				}
				out := Threads{Threads: make([]ThreadSummary, 0, len(kept)), pageInfo: pageInfo{NextCursor: next}}
				for i, thread := range kept {
					out.Threads = append(out.Threads, toThreadSummary(thread, posts[i]))
				}
				return nil, out, nil
			}
		},
	), withShapes(pagingShapes, map[string]string{
		"unread_only": "keeps the threads Mattermost counts unread replies in",
	}))
}

// followedThreads is every thread the user follows in one team, or in all of
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
		// Every followed thread of the team, a page at a time: a page that is
		// not full is the last.
		for before := ""; ; {
			page, err := client.Threads(ctx, self.Id, team, serverPageSize, before)
			if err != nil {
				return nil, err
			}
			// Mattermost reads on from a thread by when it was last replied to
			// alone; a millisecond the page is cut back from is read whole next.
			read, more, _ := wholeMilliseconds(page.Threads, serverPageSize, len(page.Threads) == serverPageSize,
				func(thread *model.ThreadResponse) int64 { return thread.LastReplyAt })
			added := 0
			for _, thread := range read {
				if !seen[thread.PostId] && thread.Post != nil {
					seen[thread.PostId] = true
					threads = append(threads, thread)
					added++
				}
			}
			if !more {
				break
			}
			// before is undocumented: should a release stop reading it, the same
			// page would come back for ever.
			if added == 0 {
				return nil, fmt.Errorf("the next page of followed threads came back the same as the last: Mattermost no longer pages them by before")
			}
			before = read[len(read)-1].PostId
		}
	}
	// Most recently replied to first, and by id between threads replied to in
	// the same millisecond, so a page is the same slice each time.
	slices.SortStableFunc(threads, func(a, b *model.ThreadResponse) int {
		if a.LastReplyAt != b.LastReplyAt {
			return int(b.LastReplyAt - a.LastReplyAt)
		}
		return strings.Compare(a.PostId, b.PostId)
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
	// The post the threads view carries counts no replies; the thread does.
	started.ReplyCount, started.LastReplyAt = thread.ReplyCount, timestamp(thread.LastReplyAt)
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
