package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// The most posts one read returns, which is the most Mattermost gives in one
// page, and how many it returns when not told.
const (
	maxPostsPerRead     = 200
	defaultPostsPerRead = 30
)

// ChannelPosts is a stretch of a channel's conversation.
type ChannelPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first, as the conversation reads"`
	pageInfo
}

type readChannelInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel to read, as get_user_channels or get_channel_info gives it"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many posts a page holds, at most 200; 30 when not given"`
	Before    string `json:"before,omitempty" jsonschema:"read the posts before this post id; the next pages go further back"`
	After     string `json:"after,omitempty" jsonschema:"read the posts after this post id; the next pages come forward"`
	Since     string `json:"since,omitempty" jsonschema:"read the posts written from this time on, as an RFC 3339 time such as 2026-10-07T09:00:00Z; the next pages come forward"`
	// CollapseThreads reads the channel as collapsed reply threads show it.
	CollapseThreads bool `json:"collapse_threads,omitempty" jsonschema:"leave replies out, showing each thread by the post that started it with its reply count and last reply, as Mattermost shows a channel with collapsed reply threads"`
	pageArgs
}

// channelReadUses are GetPostsForChannel, as reading a channel by post calls it.
func channelReadUses(before, after, since, collapsed Coverage) Use {
	return Use{
		Operation: "GetPostsForChannel",
		Params: map[string]Coverage{
			"channel_id":       SetBy("channel_id"),
			"per_page":         SetBy("limit"),
			"before":           before,
			"after":            after,
			"since":            since,
			"page":             Fixed("0", "the tool pages through a channel by post, which stays right while people post; a page number shifts with every new message"),
			"include_deleted":  Omitted("deleted messages are not shown: their authors removed them"),
			"type":             Omitted("filters by message type, for Mattermost's own clients; a person reads the conversation as it is"),
			"collapsedThreads": collapsed,
		},
	}
}

func readChannelSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "read_channel",
			Description: "Read a channel's messages a page at a time, oldest first within a page: the newest ones and then further back, " +
				"or with before, after or since from a post or a time. Replies appear among the channel's messages with root_id naming " +
				"the post they answer; read_post reads one thread whole.",
			Annotations: readOnly("Read channel"),
		},
		uses([]Use{channelReadUses(SetBy("before"), SetBy("after"), SetBy("since"),
			Undocumented("collapse_threads", "getPostsForChannel in server/channels/api4/post.go reads it on both supported releases, to leave replies out"))},
			describeUses(true)),
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
				limit, err := limitOf(input.Limit, defaultPostsPerRead, maxPostsPerRead)
				if err != nil {
					return nil, ChannelPosts{}, err
				}
				at, err := openCursor("read_channel", input, input.Cursor)
				if err != nil {
					return nil, ChannelPosts{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, ChannelPosts{}, err
				}
				switch {
				case at.After != "":
					// The cursor goes on from the last post the last page held.
				case input.Since != "":
					return readSince(ctx, client, input, at, limit)
				case input.After != "":
					at.After = input.After
				default:
					at.After, at.Back = input.Before, true
				}
				return readFromPost(ctx, client, input.ChannelID, input.CollapseThreads, at, limit)
			}
		},
	), map[string]string{"cursor": "continues from the post the last page ended at, through before or after"})
}

// readFromPost reads a page of a channel from a post: back from it, or the
// newest when there is none yet, or forward from it.
func readFromPost(ctx context.Context, client *mattermost.Client, channelID string, collapsed bool, at position, limit int) (*mcp.CallToolResult, ChannelPosts, error) {
	page := mattermost.PostsPage{ChannelID: channelID, PerPage: limit, Collapsed: collapsed}
	if at.Back {
		page.Before = at.After
	} else {
		page.After = at.After
	}
	list, err := client.Posts(ctx, page)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	posts, err := postsInOrder(ctx, client, list)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	out := ChannelPosts{ChannelID: channelID, Posts: posts}
	// Mattermost names the post beyond each end of the page, when there is one.
	if more := (at.Back && list.PrevPostId != "") || (!at.Back && list.NextPostId != ""); more && len(posts) > 0 {
		next := at
		if at.Back {
			next.After = posts[0].ID
		} else {
			next.After = posts[len(posts)-1].ID
		}
		out.NextCursor = next.String()
	}
	return nil, out, nil
}

// readSince reads the posts written from a time on. Mattermost answers since
// with every post changed after it, old ones edited or deleted since included,
// so the tool keeps those written since and not deleted, oldest first, and the
// pages after the first go on from the last post by after.
func readSince(ctx context.Context, client *mattermost.Client, input readChannelInput, at position, limit int) (*mcp.CallToolResult, ChannelPosts, error) {
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
	page := written[:min(limit, len(written))]
	posts, err := describePosts(ctx, client, page)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	out := ChannelPosts{ChannelID: input.ChannelID, Posts: posts}
	if len(written) > limit {
		at.After = page[len(page)-1].Id
		out.NextCursor = at.String()
	}
	return nil, out, nil
}

// The most posts of a thread one read_post call returns, and how many when
// not told.
const (
	maxThreadPage     = 200
	defaultThreadPage = 100
)

// PostWithThread is a post, and the thread it is in.
type PostWithThread struct {
	Post   *Post  `json:"post,omitempty" jsonschema:"the post asked for; on the pages after the first, only the thread"`
	RootID string `json:"root_id" jsonschema:"the post that started the thread: the post itself when it started one"`
	Thread []Post `json:"thread,omitempty" jsonschema:"the thread's first post, then its replies, oldest first, a page at a time; left out when include_thread is false"`
	pageInfo
}

type readPostInput struct {
	PostID        string `json:"post_id" jsonschema:"the post to read: one that starts a thread, or a reply in one"`
	IncludeThread *bool  `json:"include_thread,omitempty" jsonschema:"false reads the post alone; true, the default, reads the thread it is in"`
	Limit         int    `json:"limit,omitempty" jsonschema:"how many posts of the thread a page holds, at most 200; 100 when not given"`
	pageArgs
}

func readPostSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "read_post",
			Description: "Read a post and the thread it is in: the post that started it and its replies, oldest first, a page at a time. " +
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
					"perPage":                  SetBy("limit"),
					"fromPost":                 SetBy("cursor"),
					"fromCreateAt":             Fixed("the time of the post the cursor names", "Mattermost pages a thread from a post and its time together"),
					"direction":                Fixed("down", "a thread reads oldest first"),
					"fromUpdateAt":             Omitted("a sync for clients that already hold the thread"),
					"skipFetchThreads":         Omitted("the thread's posts are what the tool reads; Mattermost's default answers with them"),
					"collapsedThreads":         Omitted("collapsed threads change how a channel lists replies, not what a thread holds"),
					"collapsedThreadsExtended": Omitted("collapsed threads change how a channel lists replies, not what a thread holds"),
					"updatesOnly":              Omitted("a sync for clients that already hold the thread"),
				},
			},
		}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[readPostInput, PostWithThread] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readPostInput) (*mcp.CallToolResult, PostWithThread, error) {
				limit, err := limitOf(input.Limit, defaultThreadPage, maxThreadPage)
				if err != nil {
					return nil, PostWithThread{}, err
				}
				at, err := openCursor("read_post", input, input.Cursor)
				if err != nil {
					return nil, PostWithThread{}, err
				}
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
					return nil, PostWithThread{Post: &posts[0], RootID: root}, nil
				}
				list, err := client.Thread(ctx, mattermost.ThreadPage{PostID: input.PostID, PerPage: limit, FromPost: at.After, FromCreateAt: at.At})
				if err != nil {
					return nil, PostWithThread{}, err
				}
				// Mattermost counts a page's replies and adds the thread's first post
				// to every page; it belongs on the first, and the page is cut to the
				// limit, the rest following from its last post.
				ordered := orderedPosts(list, true)
				if at.After != "" {
					// and the post a page goes on from opens that page too.
					ordered = slices.DeleteFunc(ordered, func(post *model.Post) bool { return post.RootId == "" || post.Id == at.After })
				}
				more := list.HasNext != nil && *list.HasNext
				if len(ordered) > limit {
					ordered, more = ordered[:limit], true
				}
				thread, err := describePosts(ctx, client, ordered)
				if err != nil {
					return nil, PostWithThread{}, err
				}
				out := PostWithThread{Thread: thread}
				for i, post := range thread {
					if post.ID == input.PostID {
						out.Post = &thread[i]
					}
					if out.RootID == "" {
						out.RootID = post.RootID
						if post.RootID == "" {
							out.RootID = post.ID
						}
					}
				}
				if more && len(ordered) > 0 {
					last := ordered[len(ordered)-1]
					at.After, at.At = last.Id, last.CreateAt
					out.NextCursor = at.String()
				}
				return nil, out, nil
			}
		},
	), map[string]string{"include_thread": "chooses between reading the post alone, GetPost, and its thread, GetPostThread"})
}

// PinnedPosts is a channel's pinned posts.
type PinnedPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first"`
	pageInfo
}

// The most pinned posts one call returns, and how many when not told.
const (
	maxPinned     = 100
	defaultPinned = 30
)

type listPinnedInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel whose pinned posts to read"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many pinned posts a page holds, at most 100; 30 when not given"`
	pageArgs
}

func listPinnedPostsSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name:        "list_pinned_posts",
			Description: "Read the posts pinned to a channel, which its members pinned for everyone to find again, a page at a time.",
			Annotations: readOnly("List pinned posts"),
		},
		uses([]Use{{Operation: "GetPinnedPosts", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}}}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listPinnedInput, PinnedPosts] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listPinnedInput) (*mcp.CallToolResult, PinnedPosts, error) {
				limit, err := limitOf(input.Limit, defaultPinned, maxPinned)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				at, err := openCursor("list_pinned_posts", input, input.Cursor)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				list, err := client.PinnedPosts(ctx, input.ChannelID)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				// Mattermost gives every pinned post at once; a page is a slice.
				ordered := orderedPosts(list, true)
				page, next := offsetPage(ordered, at, limit)
				posts, err := describePosts(ctx, client, page)
				if err != nil {
					return nil, PinnedPosts{}, err
				}
				return nil, PinnedPosts{ChannelID: input.ChannelID, Posts: posts, pageInfo: pageInfo{NextCursor: next}}, nil
			}
		},
	), pagingShapes)
}

// The most saved posts one list_saved call returns, and how many when not told.
const (
	maxSaved     = 100
	defaultSaved = 20
)

type listSavedInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"how many saved posts a page holds, at most 100; 20 when not given"`
	pageArgs
}

func listSavedSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name:        "list_saved",
			Description: "Read the posts the user saved to come back to, most recently posted first, a page at a time.",
			Annotations: readOnly("List saved posts"),
		},
		uses([]Use{{
			Operation: "GetFlaggedPostsForUser",
			Params: map[string]Coverage{
				"user_id": Fixed("me", "saved posts are the user's own"),
				"page": Fixed("the number of saved posts already read",
					"getFlaggedPostsForUser in server/channels/api4/post.go hands page to the database as an offset in posts, not a page number; "+
						"the tool reads every saved post so and pages through them itself, in an order Mattermost's own does not keep when two posts share a time"),
				"per_page":   Fixed(fmt.Sprint(serverPageSize), "the most Mattermost gives in one page"),
				"team_id":    Omitted("each saved post comes back with its channel and team"),
				"channel_id": Omitted("each saved post comes back with its channel and team"),
			},
		}}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listSavedInput, SearchResults] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listSavedInput) (*mcp.CallToolResult, SearchResults, error) {
				limit, err := limitOf(input.Limit, defaultSaved, maxSaved)
				if err != nil {
					return nil, SearchResults{}, err
				}
				at, err := openCursor("list_saved", input, input.Cursor)
				if err != nil {
					return nil, SearchResults{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, SearchResults{}, err
				}
				var all []*model.Post
				for read := 0; ; {
					list, err := client.SavedPosts(ctx, read, serverPageSize)
					if err != nil {
						return nil, SearchResults{}, err
					}
					batch := orderedPosts(list, false)
					all = append(all, batch...)
					read += len(list.Order)
					if len(list.Order) < serverPageSize {
						break
					}
				}
				page, next := offsetPage(newestFirst(uniquePosts(all)), at, limit)
				posts, err := describePosts(ctx, client, page)
				if err != nil {
					return nil, SearchResults{}, err
				}
				return nil, SearchResults{Posts: posts, pageInfo: pageInfo{NextCursor: next}}, nil
			}
		},
	), pagingShapes)
}

// How many read posts read_unread shows before the first unread one, so the
// unread ones read in context: these, or fewer to stay within the limit.
const readBeforeUnread = 3

// UnreadPosts is a channel from where the person stopped reading.
type UnreadPosts struct {
	ChannelID string `json:"channel_id"`
	Posts     []Post `json:"posts" jsonschema:"oldest first: on the first page a few the person has read, then those they have not"`
	// FirstUnreadID is the first post the person has not read.
	FirstUnreadID string `json:"first_unread_id,omitempty" jsonschema:"the first post on this page the person has not read"`
	Unread        int    `json:"unread" jsonschema:"how many of the posts on this page the person has not read"`
	pageInfo
}

type readUnreadInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel to catch up on, as get_user_channels gives it"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many posts a page holds, the read ones shown for context included, at most 200; 30 when not given"`
	pageArgs
}

func readUnreadSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "read_unread",
			Description: "Catch up on a channel from where the person stopped reading in Mattermost: a few posts they have read, then the " +
				"ones they have not, oldest first, a page at a time. It reads only; the channel stays unread for the person until they read it themselves.",
			Annotations: readOnly("Read unread posts"),
		},
		uses([]Use{
			{
				Operation: "GetPostsAroundLastUnread",
				Params: map[string]Coverage{
					"user_id":                  Fixed("me", "where the user stopped reading is theirs"),
					"channel_id":               SetBy("channel_id"),
					"limit_before":             Fixed(fmt.Sprintf("%d, or fewer within the limit", readBeforeUnread), "a few read posts put the unread ones in context"),
					"limit_after":              SetBy("limit"),
					"collapsedThreads":         Fixed("false", "replies are among the channel's posts, as read_channel shows them by default"),
					"skipFetchThreads":         Omitted("the posts are what the tool reads; Mattermost's default answers with them"),
					"collapsedThreadsExtended": Omitted("threads are not collapsed"),
				},
			},
			channelReadUses(Omitted("the unread posts are read forward, from where the person stopped"), SetBy("cursor"),
				Omitted("the unread posts are read from a post, not a time"),
				Undocumented("", "getPostsForChannel in server/channels/api4/post.go reads it; Mattermost's client sends it false, keeping replies among the posts")),
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
				limit, err := limitOf(input.Limit, defaultPostsPerRead, maxPostsPerRead)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				at, err := openCursor("read_unread", input, input.Cursor)
				if err != nil {
					return nil, UnreadPosts{}, err
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
				var list *model.PostList
				if at.After == "" {
					before := min(readBeforeUnread, limit-1)
					list, err = client.UnreadPosts(ctx, self.Id, input.ChannelID, before, limit-before)
				} else {
					list, err = client.Posts(ctx, mattermost.PostsPage{ChannelID: input.ChannelID, PerPage: limit, After: at.After})
				}
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				posts, err := postsInOrder(ctx, client, list)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				out := UnreadPosts{ChannelID: input.ChannelID, Posts: posts}
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
				if list.NextPostId != "" && len(posts) > 0 {
					at.After = posts[len(posts)-1].ID
					out.NextCursor = at.String()
				}
				return nil, out, nil
			}
		},
	), nil)
}

// uniquePosts is posts, each once.
func uniquePosts(posts []*model.Post) []*model.Post {
	seen := map[string]bool{}
	return slices.DeleteFunc(posts, func(post *model.Post) bool {
		if seen[post.Id] {
			return true
		}
		seen[post.Id] = true
		return false
	})
}

// newestFirst orders posts most recent first, and by id between posts written
// in the same millisecond, so the order is the same each time.
func newestFirst(posts []*model.Post) []*model.Post {
	slices.SortStableFunc(posts, func(a, b *model.Post) int {
		if a.CreateAt != b.CreateAt {
			return int(b.CreateAt - a.CreateAt)
		}
		return strings.Compare(a.Id, b.Id)
	})
	return posts
}

// orderedPosts is a post list's posts, each once: oldest first when
// chronological, and in the list's own order when not. Mattermost's list of
// saved posts can name a post more than once.
func orderedPosts(list *model.PostList, chronological bool) []*model.Post {
	if list == nil {
		return nil
	}
	posts := make([]*model.Post, 0, len(list.Order))
	seen := map[string]bool{}
	for _, id := range list.Order {
		if post, ok := list.Posts[id]; ok && !seen[id] {
			seen[id] = true
			posts = append(posts, post)
		}
	}
	if chronological {
		slices.SortStableFunc(posts, func(a, b *model.Post) int {
			if a.CreateAt != b.CreateAt {
				return int(a.CreateAt - b.CreateAt)
			}
			return strings.Compare(a.Id, b.Id)
		})
	}
	return posts
}
