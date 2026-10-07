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
}

func readChannelSpec() Spec {
	return toolSpec(
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
				list, err := client.Posts(ctx, mattermost.PostsPage{ChannelID: input.ChannelID, PerPage: limit, Before: input.Before, After: input.After})
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
	)
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
