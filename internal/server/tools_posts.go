package server

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// The most posts one read_channel call returns, which is the most Mattermost
// gives in one page, and how many it returns when not told.
const (
	maxPostsPerRead     = 200
	defaultPostsPerRead = 30
)

// ChannelPosts is a stretch of a channel's conversation.
type ChannelPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first, as the conversation reads"`
	// MoreBefore says whether older posts may exist; read them with before set
	// to the first post's id.
	MoreBefore bool `json:"more_before" jsonschema:"whether older posts may exist: read them with before set to the first post's id"`
	// MoreAfter says whether newer posts may exist, after a read from since.
	MoreAfter bool `json:"more_after,omitempty" jsonschema:"after a read from since: whether newer posts follow; read them with after set to the last post's id"`
}

type readChannelInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel to read, as get_user_channels or get_channel_info gives it"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many posts to read, at most 200; 30 when not given"`
	Before    string `json:"before,omitempty" jsonschema:"read the posts before this post id, to page back through older ones"`
	After     string `json:"after,omitempty" jsonschema:"read the posts after this post id, to catch up from a known point"`
	Since     string `json:"since,omitempty" jsonschema:"read the posts written from this time on, oldest first, as an RFC 3339 time such as 2026-10-07T09:00:00Z"`
	// CollapseThreads reads the channel as collapsed reply threads show it.
	CollapseThreads bool `json:"collapse_threads,omitempty" jsonschema:"leave replies out, showing each thread by the post that started it with its reply count and last reply, as Mattermost shows a channel with collapsed reply threads"`
}

func readChannelSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "read_channel",
			Description: "Read a channel's messages, oldest first: the newest ones by default, those before or after a post, or those " +
				"written since a time. Replies appear among the channel's messages with root_id naming the post they answer; " +
				"read_post reads one thread whole.",
			Annotations: readOnly("Read channel"),
		},
		uses([]Use{{
			Operation: "GetPostsForChannel",
			Params: map[string]Coverage{
				"channel_id":      SetBy("channel_id"),
				"per_page":        SetBy("limit"),
				"before":          SetBy("before"),
				"after":           SetBy("after"),
				"since":           SetBy("since"),
				"page":            Fixed("0", "before and after page through a channel by post, which stays right while people post; a page number shifts with every new message"),
				"include_deleted": Omitted("deleted messages are not shown: their authors removed them"),
				"type":            Omitted("filters by message type, for Mattermost's own clients; a person reads the conversation as it is"),
			},
		}}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[readChannelInput, ChannelPosts] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readChannelInput) (*mcp.CallToolResult, ChannelPosts, error) {
				given := 0
				for _, set := range []string{input.Before, input.After, input.Since} {
					if set != "" {
						given++
					}
				}
				if given > 1 {
					return nil, ChannelPosts{}, fmt.Errorf("give one of before, after and since")
				}
				limit := input.Limit
				switch {
				case limit == 0:
					limit = defaultPostsPerRead
				case limit < 0 || limit > maxPostsPerRead:
					return nil, ChannelPosts{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxPostsPerRead, limit)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, ChannelPosts{}, err
				}
				if input.Since != "" {
					return readSince(ctx, client, input, limit)
				}
				list, err := client.Posts(ctx, mattermost.PostsPage{
					ChannelID: input.ChannelID, PerPage: limit, Before: input.Before, After: input.After, Collapsed: input.CollapseThreads,
				})
				if err != nil {
					return nil, ChannelPosts{}, err
				}
				posts, err := postsInOrder(ctx, client, list)
				if err != nil {
					return nil, ChannelPosts{}, err
				}
				return nil, ChannelPosts{ChannelID: input.ChannelID, Posts: posts, MoreBefore: len(posts) == limit}, nil
			}
		},
	), map[string]string{
		"collapse_threads": "sends collapsedThreads, which Mattermost's router reads on both supported releases though neither specification lists it",
	})
}

// readSince reads the posts written from a time on. Mattermost answers since
// with every post changed after it, old ones edited or deleted since included,
// so the tool keeps those written since and not deleted, oldest first.
func readSince(ctx context.Context, client *mattermost.Client, input readChannelInput, limit int) (*mcp.CallToolResult, ChannelPosts, error) {
	since, err := time.Parse(time.RFC3339, input.Since)
	if err != nil {
		return nil, ChannelPosts{}, fmt.Errorf("since must be an RFC 3339 time such as 2026-10-07T09:00:00Z, not %q", input.Since)
	}
	list, err := client.PostsSince(ctx, input.ChannelID, since.UnixMilli(), input.CollapseThreads)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	var written []*model.Post
	for _, post := range list.Posts {
		if post.CreateAt >= since.UnixMilli() && post.DeleteAt == 0 && (!input.CollapseThreads || post.RootId == "") {
			written = append(written, post)
		}
	}
	slices.SortStableFunc(written, func(a, b *model.Post) int { return int(a.CreateAt - b.CreateAt) })
	more := len(written) > limit
	posts, err := describePosts(ctx, client, written[:min(limit, len(written))])
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	return nil, ChannelPosts{ChannelID: input.ChannelID, Posts: posts, MoreBefore: true, MoreAfter: more}, nil
}

// PostWithThread is a post, and the thread it is in.
type PostWithThread struct {
	Post   Post   `json:"post"`
	RootID string `json:"root_id" jsonschema:"the post that started the thread: the post itself when it started one"`
	Thread []Post `json:"thread,omitempty" jsonschema:"the thread's first post, then every reply, oldest first; left out when include_thread is false"`
}

type readPostInput struct {
	PostID        string `json:"post_id" jsonschema:"the post to read: one that starts a thread, or a reply in one"`
	IncludeThread *bool  `json:"include_thread,omitempty" jsonschema:"false reads the post alone; true, the default, reads the whole thread it is in"`
}

func readPostSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "read_post",
			Description: "Read a post and the whole thread it is in: the post that started it and every reply, oldest first. " +
				"Give any post in the thread. include_thread false reads the post alone.",
			Annotations: readOnly("Read post"),
		},
		uses([]Use{
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("post_id"),
					"include_deleted": Omitted("a deleted post is gone for the person too"),
				},
			},
			{
				Operation: "GetPostThread",
				Params: map[string]Coverage{
					"post_id":                  SetBy("post_id"),
					"perPage":                  Omitted("a thread is read whole; one long enough to need paging is rare, and reading part of it misleads"),
					"fromPost":                 Omitted("paging within a thread, which the tool does not do"),
					"fromCreateAt":             Omitted("paging within a thread, which the tool does not do"),
					"fromUpdateAt":             Omitted("paging within a thread, which the tool does not do"),
					"direction":                Omitted("paging within a thread, which the tool does not do"),
					"skipFetchThreads":         Omitted("the thread's posts are what the tool reads; Mattermost's default answers with them"),
					"collapsedThreads":         Omitted("collapsed threads change how a channel lists replies, not what a thread holds"),
					"collapsedThreadsExtended": Omitted("collapsed threads change how a channel lists replies, not what a thread holds"),
					"updatesOnly":              Omitted("a sync for clients that already hold the thread"),
				},
			},
		}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[readPostInput, PostWithThread] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readPostInput) (*mcp.CallToolResult, PostWithThread, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, PostWithThread{}, err
				}
				if input.IncludeThread != nil && !*input.IncludeThread {
					post, err := client.Post(ctx, input.PostID)
					if err != nil {
						return nil, PostWithThread{}, err
					}
					posts, err := describePosts(ctx, client, []*model.Post{post})
					if err != nil {
						return nil, PostWithThread{}, err
					}
					root := post.RootId
					if root == "" {
						root = post.Id
					}
					return nil, PostWithThread{Post: posts[0], RootID: root}, nil
				}
				list, err := client.Thread(ctx, input.PostID)
				if err != nil {
					return nil, PostWithThread{}, err
				}
				thread, err := postsInOrder(ctx, client, list)
				if err != nil {
					return nil, PostWithThread{}, err
				}
				out := PostWithThread{Thread: thread}
				for _, post := range thread {
					if post.ID == input.PostID {
						out.Post = post
					}
				}
				if len(thread) > 0 {
					out.RootID = thread[0].ID
					if thread[0].RootID != "" {
						out.RootID = thread[0].RootID
					}
				}
				return nil, out, nil
			}
		},
	), map[string]string{"include_thread": "chooses between reading the post alone, GetPost, and its whole thread, GetPostThread"})
}

// PinnedPosts is a channel's pinned posts.
type PinnedPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first"`
}

type listPinnedInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel whose pinned posts to read"`
}

func listPinnedPostsSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "list_pinned_posts",
			Description: "Read the posts pinned to a channel, which its members pinned for everyone to find again.",
			Annotations: readOnly("List pinned posts"),
		},
		uses([]Use{{Operation: "GetPinnedPosts", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}}}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listPinnedInput, PinnedPosts] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listPinnedInput) (*mcp.CallToolResult, PinnedPosts, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				list, err := client.PinnedPosts(ctx, input.ChannelID)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				posts, err := postsInOrder(ctx, client, list)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				return nil, PinnedPosts{ChannelID: input.ChannelID, Posts: posts}, nil
			}
		},
	)
}

// The most saved posts one list_saved call returns, and how many when not told.
const (
	maxSaved     = 100
	defaultSaved = 20
)

type listSavedInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many saved posts to return, at most 100; 20 when not given"`
}

func listSavedSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "list_saved",
			Description: "Read the posts the user saved to come back to, most recently posted first, each with its channel.",
			Annotations: readOnly("List saved posts"),
		},
		uses([]Use{{
			Operation: "GetFlaggedPostsForUser",
			Params: map[string]Coverage{
				"user_id":    Fixed("me", "saved posts are the user's own"),
				"page":       Fixed("0", "the most recent saved posts, which one page holds"),
				"per_page":   SetBy("limit"),
				"team_id":    Omitted("a person keeps few saved posts, and each comes back with its channel and team"),
				"channel_id": Omitted("a person keeps few saved posts, and each comes back with its channel and team"),
			},
		}}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listSavedInput, SearchResults] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listSavedInput) (*mcp.CallToolResult, SearchResults, error) {
				limit := input.Limit
				switch {
				case limit == 0:
					limit = defaultSaved
				case limit < 0 || limit > maxSaved:
					return nil, SearchResults{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxSaved, limit)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, SearchResults{}, err
				}
				// One more than the limit says whether there are more.
				list, err := client.SavedPosts(ctx, limit+1)
				if err != nil {
					return nil, SearchResults{}, err
				}
				truncated := len(list.Order) > limit
				if truncated {
					list.Order = list.Order[:limit]
				}
				posts, err := listedPosts(ctx, client, list, false)
				if err != nil {
					return nil, SearchResults{}, err
				}
				return nil, SearchResults{Posts: posts, Truncated: truncated}, nil
			}
		},
	)
}

// How many read posts read_unread shows before the first unread one, so the
// unread ones read in context.
const readBeforeUnread = 3

// UnreadPosts is a channel from where the person stopped reading.
type UnreadPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first: a few the person has read, then those they have not"`
	// FirstUnreadID is the first post the person has not read.
	FirstUnreadID string `json:"first_unread_id,omitempty" jsonschema:"the first post the person has not read; empty when they have read everything"`
	Unread        int    `json:"unread" jsonschema:"how many of the posts returned the person has not read"`
	MoreAfter     bool   `json:"more_after" jsonschema:"whether newer posts follow: read them with read_channel, after set to the last post's id"`
}

type readUnreadInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel to catch up on, as get_user_channels gives it"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many unread posts to read, at most 200; 30 when not given"`
}

func readUnreadSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "read_unread",
			Description: "Catch up on a channel from where the person stopped reading in Mattermost: a few posts they have read, then the " +
				"ones they have not, oldest first. It reads only; the channel stays unread for the person until they read it themselves.",
			Annotations: readOnly("Read unread posts"),
		},
		uses([]Use{
			{
				Operation: "GetPostsAroundLastUnread",
				Params: map[string]Coverage{
					"user_id":                  Fixed("me", "where the user stopped reading is theirs"),
					"channel_id":               SetBy("channel_id"),
					"limit_before":             Fixed(fmt.Sprint(readBeforeUnread), "a few read posts put the unread ones in context"),
					"limit_after":              SetBy("limit"),
					"collapsedThreads":         Fixed("false", "replies are among the channel's posts, as read_channel shows them by default"),
					"skipFetchThreads":         Omitted("the posts are what the tool reads; Mattermost's default answers with them"),
					"collapsedThreadsExtended": Omitted("threads are not collapsed"),
				},
			},
			{
				Operation: "GetChannelMember",
				Params: map[string]Coverage{
					"channel_id": SetBy("channel_id"),
					"user_id":    Fixed("me", "the membership holds when the user last read the channel"),
				},
			},
		}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[readUnreadInput, UnreadPosts] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readUnreadInput) (*mcp.CallToolResult, UnreadPosts, error) {
				limit := input.Limit
				switch {
				case limit == 0:
					limit = defaultPostsPerRead
				case limit < 0 || limit > maxPostsPerRead:
					return nil, UnreadPosts{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxPostsPerRead, limit)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				membership, err := client.Membership(ctx, input.ChannelID, self.Id)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				list, err := client.UnreadPosts(ctx, self.Id, input.ChannelID, readBeforeUnread, limit)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				posts, err := postsInOrder(ctx, client, list)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				out := UnreadPosts{ChannelID: input.ChannelID, Posts: posts, MoreAfter: list.NextPostId != ""}
				firstUnreadAt := int64(0)
				for _, post := range list.Posts {
					if post.UserId == self.Id || post.CreateAt <= membership.LastViewedAt {
						continue
					}
					out.Unread++
					if firstUnreadAt == 0 || post.CreateAt < firstUnreadAt {
						firstUnreadAt, out.FirstUnreadID = post.CreateAt, post.Id
					}
				}
				return nil, out, nil
			}
		},
	)
}
