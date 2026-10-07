package server

import (
	"context"
	"fmt"

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
}

type readChannelInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel to read, as list_channels gives it"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many posts to read, at most 200; 30 when not given"`
	Before    string `json:"before,omitempty" jsonschema:"read the posts before this post id, to page back through older ones"`
	After     string `json:"after,omitempty" jsonschema:"read the posts after this post id, to catch up from a known point"`
	// CollapseThreads reads the channel as collapsed reply threads show it.
	CollapseThreads bool `json:"collapse_threads,omitempty" jsonschema:"leave replies out, showing each thread by the post that started it with its reply count and last reply, as Mattermost shows a channel with collapsed reply threads"`
}

func readChannelSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "read_channel",
			Description: "Read a channel's messages, newest first by default and returned oldest first. Replies appear among " +
				"the channel's messages with root_id naming the post they answer; read_thread reads one conversation whole.",
			Annotations: readOnly("Read channel"),
		},
		[]Use{
			{
				Operation: "GetPostsForChannel",
				Params: map[string]Coverage{
					"channel_id":      SetBy("channel_id"),
					"per_page":        SetBy("limit"),
					"before":          SetBy("before"),
					"after":           SetBy("after"),
					"page":            Fixed("0", "before and after page through a channel by post, which stays right while people post; a page number shifts with every new message"),
					"since":           Omitted("since returns every post changed after a time, edits and deletions of old posts included, which is a sync for clients, not a reading of the conversation"),
					"include_deleted": Omitted("deleted messages are not shown: their authors removed them"),
					"type":            Omitted("filters by message type, for Mattermost's own clients; a person reads the conversation as it is"),
				},
			},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each author's username, whenever they changed")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[readChannelInput, ChannelPosts] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readChannelInput) (*mcp.CallToolResult, ChannelPosts, error) {
				if input.Before != "" && input.After != "" {
					return nil, ChannelPosts{}, fmt.Errorf("give before or after, not both")
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

// Thread is one conversation: its first post and every reply.
type Thread struct {
	RootID string `json:"root_id"`
	Posts  []Post `json:"posts" jsonschema:"the first post, then every reply, oldest first"`
}

type readThreadInput struct {
	PostID string `json:"post_id" jsonschema:"any post in the thread: its first post or one of the replies"`
}

func readThreadSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "read_thread",
			Description: "Read a whole thread: the post that started it and every reply, oldest first. Give any post in the thread.",
			Annotations: readOnly("Read thread"),
		},
		[]Use{
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
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each author's username, whenever they changed")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[readThreadInput, Thread] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readThreadInput) (*mcp.CallToolResult, Thread, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Thread{}, err
				}
				list, err := client.Thread(ctx, input.PostID)
				if err != nil {
					return nil, Thread{}, err
				}
				posts, err := postsInOrder(ctx, client, list)
				if err != nil {
					return nil, Thread{}, err
				}
				root := input.PostID
				if len(posts) > 0 {
					root = posts[0].ID
					if posts[0].RootID != "" {
						root = posts[0].RootID
					}
				}
				return nil, Thread{RootID: root, Posts: posts}, nil
			}
		},
	)
}

// PinnedPosts is a channel's pinned posts.
type PinnedPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first"`
}

type listPinnedInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel whose pinned posts to read"`
}

func listPinnedSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "list_pinned",
			Description: "Read the posts pinned to a channel, which its members pinned for everyone to find again.",
			Annotations: readOnly("List pinned posts"),
		},
		[]Use{
			{Operation: "GetPinnedPosts", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each author's username, whenever they changed")},
			},
		},
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
		append([]Use{{
			Operation: "GetFlaggedPostsForUser",
			Params: map[string]Coverage{
				"user_id":    Fixed("me", "saved posts are the user's own"),
				"page":       Fixed("0", "the most recent saved posts, which one page holds"),
				"per_page":   SetBy("limit"),
				"team_id":    Omitted("a person keeps few saved posts, and each comes back with its channel"),
				"channel_id": Omitted("a person keeps few saved posts, and each comes back with its channel"),
			},
		}}, channelNameUses("a saved post names the channel it is in, from the channels the user belongs to")...),
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
				posts, err := listedPosts(ctx, client, list, false)
				if err != nil {
					return nil, SearchResults{}, err
				}
				names, err := channelNames(ctx, client)
				if err != nil {
					return nil, SearchResults{}, err
				}
				out := SearchResults{Posts: []FoundPost{}, Truncated: len(posts) > limit}
				for _, post := range posts[:min(limit, len(posts))] {
					out.Posts = append(out.Posts, FoundPost{Post: post, Channel: names[post.ChannelID]})
				}
				return nil, out, nil
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
	ChannelID string `json:"channel_id" jsonschema:"the channel to catch up on, as list_channels gives it"`
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
		[]Use{
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
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the user's own posts are never unread to them")},
			},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each author's username, whenever they changed")},
			},
		},
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
