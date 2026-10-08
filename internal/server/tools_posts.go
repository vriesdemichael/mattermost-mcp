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
	Channel   string `json:"channel,omitempty" jsonschema:"the channel's display name, which every post here is in"`
	Team      string `json:"team,omitempty" jsonschema:"the channel's team; empty for a direct or group message"`
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
	page := mattermost.PostsPage{ChannelID: channelID, Collapsed: collapsed}
	if at.Back {
		page.Before = at.After
	} else {
		page.After = at.After
	}
	read, more, err := readOn(func(perPage int) (*model.PostList, error) {
		page.PerPage = perPage
		return client.Posts(ctx, page)
	}, at.Back, limit)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	posts, err := describePosts(ctx, client, read)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	out := ChannelPosts{ChannelID: channelID, Posts: posts}
	out.Channel, out.Team, out.Posts = inOneChannel(out.Posts)
	if more && len(read) > 0 {
		next := at
		if at.Back {
			next.After = read[0].Id
		} else {
			next.After = read[len(read)-1].Id
		}
		out.NextCursor = next.String()
	}
	return nil, out, nil
}

// readOn is a page of posts Mattermost reads back or forward from a post,
// fetched a page of perPage at a time: oldest first, at most limit posts that
// end on a whole millisecond, and whether more follow, by the post Mattermost
// names beyond the page's end. A millisecond with more posts than the page
// holds is read whole, at Mattermost's most, on a page larger than asked for.
func readOn(fetch func(perPage int) (*model.PostList, error), back bool, limit int) ([]*model.Post, bool, error) {
	onwards := func(perPage int) ([]*model.Post, bool, error) {
		list, err := fetch(perPage)
		if err != nil {
			return nil, false, err
		}
		posts := orderedPosts(list, true)
		if back {
			return backwards(posts), list.PrevPostId != "", nil
		}
		return posts, list.NextPostId != "", nil
	}
	posts, more, err := onwards(min(limit+1, serverPageSize))
	if err != nil {
		return nil, false, err
	}
	read, more, whole := wholeMilliseconds(posts, limit, more, postTime)
	if !whole && limit < serverPageSize {
		edge := read[0].CreateAt
		if posts, more, err = onwards(serverPageSize); err != nil {
			return nil, false, err
		}
		read, more = throughMillisecond(posts, edge, more, postTime)
	}
	if back {
		read = backwards(read)
	}
	return read, more, nil
}

// sinceReach is the most posts Mattermost answers a read since a time with,
// and walkBackPages how far back, in Mattermost's pages, the tool reads a
// channel when that is not enough.
const (
	sinceReach    = 1000
	walkBackPages = 50
)

// readSince reads the posts written from a time on, oldest first; the pages
// after the first go on from the last post by after.
func readSince(ctx context.Context, client *mattermost.Client, input readChannelInput, at position, limit int) (*mcp.CallToolResult, ChannelPosts, error) {
	since, err := time.Parse(time.RFC3339, input.Since)
	if err != nil {
		return nil, ChannelPosts{}, fmt.Errorf("since must be an RFC 3339 time such as 2026-10-07T09:00:00Z, not %q", input.Since)
	}
	written, err := postsWrittenSince(ctx, client, input.ChannelID, input.CollapseThreads, since.UnixMilli())
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	page, more := firstOf(written, limit)
	posts, err := describePosts(ctx, client, page)
	if err != nil {
		return nil, ChannelPosts{}, err
	}
	out := ChannelPosts{ChannelID: input.ChannelID, Posts: posts}
	out.Channel, out.Team, out.Posts = inOneChannel(out.Posts)
	if more {
		at.After = page[len(page)-1].Id
		out.NextCursor = at.String()
	}
	return nil, out, nil
}

// firstOf is the first page of posts read whole, at most limit that end on a
// whole millisecond, or one millisecond whole however many it holds, and
// whether more follow.
func firstOf(posts []*model.Post, limit int) ([]*model.Post, bool) {
	page, more, whole := wholeMilliseconds(posts, limit, false, postTime)
	if !whole {
		return throughMillisecond(posts, page[0].CreateAt, false, postTime)
	}
	return page, more
}

// postsWrittenSince is the posts written in a channel from a time on and not
// deleted, oldest first. Mattermost answers since with up to 1000 posts
// changed after it, old ones edited or deleted since included and in no order
// that says which were left out, so the tool keeps those written since; and
// when the answer is as long as Mattermost makes it, the tool reads back from
// the newest post instead, page by page, until it passes the time.
func postsWrittenSince(ctx context.Context, client *mattermost.Client, channelID string, collapsed bool, from int64) ([]*model.Post, error) {
	keep := func(post *model.Post) bool {
		return post.CreateAt >= from && post.DeleteAt == 0 && (!collapsed || post.RootId == "")
	}
	// Mattermost compares a change's time with since strictly; a post written
	// in since's very millisecond is asked for one millisecond earlier.
	list, err := client.PostsSince(ctx, channelID, from-1, collapsed)
	if err != nil {
		return nil, err
	}
	var written []*model.Post
	if len(list.Posts) < sinceReach {
		for _, post := range list.Posts {
			if keep(post) {
				written = append(written, post)
			}
		}
	} else if written, err = writtenSince(ctx, client, channelID, collapsed, keep, from); err != nil {
		return nil, err
	}
	slices.SortStableFunc(written, func(a, b *model.Post) int {
		if a.CreateAt != b.CreateAt {
			return int(a.CreateAt - b.CreateAt)
		}
		return strings.Compare(a.Id, b.Id)
	})
	return written, nil
}

// writtenSince is the posts written from a time on, read back from the
// newest, a page at a time, until a page reaches past the time.
func writtenSince(ctx context.Context, client *mattermost.Client, channelID string, collapsed bool, keep func(*model.Post) bool, from int64) ([]*model.Post, error) {
	var written []*model.Post
	before := ""
	for range walkBackPages {
		// A millisecond the page is cut back from is read whole on the next.
		page, more, err := readOn(func(perPage int) (*model.PostList, error) {
			return client.Posts(ctx, mattermost.PostsPage{ChannelID: channelID, PerPage: perPage, Before: before, Collapsed: collapsed})
		}, true, serverPageSize)
		if err != nil {
			return nil, err
		}
		for _, post := range page {
			if keep(post) {
				written = append(written, post)
			}
		}
		if len(page) == 0 || page[0].CreateAt < from || !more {
			return written, nil
		}
		before = page[0].Id
	}
	return nil, fmt.Errorf("more than %d posts were written in the channel since %s; give a later since, or read back from the newest with read_channel",
		walkBackPages*serverPageSize, time.UnixMilli(from).UTC().Format(time.RFC3339))
}

// The most posts of a thread one read_post call returns, and how many when
// not told.
const (
	maxThreadPage     = 200
	defaultThreadPage = 100
)

// PostWithThread is a post, and the thread it is in.
type PostWithThread struct {
	Post   *Post  `json:"post,omitempty" jsonschema:"the post asked for, on every page"`
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
				// Mattermost answers a page of a thread with its replies after the
				// cursor, and adds to every page the thread's first post and the post
				// asked for, wherever they fall. A page keeps what comes after the
				// cursor, by time and then id; the post asked for stays out of it
				// until the replies reach it, and the page is cut to the limit, the
				// rest following from its last post.
				var asked *model.Post
				var page []*model.Post
				replies := 0
				for _, post := range orderedPosts(list, true) {
					if post.Id == input.PostID {
						asked = post
					}
					if at.After != "" && !(post.CreateAt > at.At || (post.CreateAt == at.At && post.Id > at.After)) {
						continue
					}
					if post.RootId != "" && post.Id != input.PostID {
						replies++
					}
					page = append(page, post)
				}
				more := list.HasNext != nil && *list.HasNext
				if asked != nil && asked.RootId != "" && more && len(page) > 0 {
					// The window is full, and the post asked for lies beyond it.
					if last := page[len(page)-1]; last.Id == asked.Id && replies >= limit {
						page = page[:len(page)-1]
					}
				}
				if len(page) > limit {
					page, more = page[:limit], true
				}
				described := page
				if asked != nil && !slices.Contains(page, asked) {
					described = append(slices.Clone(page), asked)
				}
				posts, err := describePosts(ctx, client, described)
				if err != nil {
					return nil, PostWithThread{}, err
				}
				out := PostWithThread{Thread: posts[:len(page)]}
				_, _, out.Thread = inOneChannel(out.Thread)
				for i := range posts {
					if posts[i].ID == input.PostID {
						out.Post = &posts[i]
					}
				}
				if asked != nil {
					out.RootID = asked.RootId
					if out.RootID == "" {
						out.RootID = asked.Id
					}
				}
				ordered := page
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
	Channel   string `json:"channel,omitempty" jsonschema:"the channel's display name, which every post here is in"`
	Team      string `json:"team,omitempty" jsonschema:"the channel's team; empty for a direct or group message"`
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
				out := PinnedPosts{ChannelID: input.ChannelID, pageInfo: pageInfo{NextCursor: next}}
				out.Channel, out.Team, out.Posts = inOneChannel(posts)
				return nil, out, nil
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
				// Until a page comes back empty: Mattermost leaves out of a page,
				// after counting it, posts the person may no longer read, so a short
				// page need not be the last.
				for read := 0; ; {
					list, err := client.SavedPosts(ctx, read, serverPageSize)
					if err != nil {
						return nil, SearchResults{}, err
					}
					if len(list.Order) == 0 {
						break
					}
					all = append(all, orderedPosts(list, false)...)
					read += len(list.Order)
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
	Channel   string `json:"channel,omitempty" jsonschema:"the channel's display name, which every post here is in"`
	Team      string `json:"team,omitempty" jsonschema:"the channel's team; empty for a direct or group message"`
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
				"ones they have not, oldest first, a page at a time. A channel the person never opened is all unread, and reads newest " +
				"first, the pages after going further back. It reads only; the channel stays unread for the person until they read it themselves.",
			Annotations: readOnly("Read unread posts"),
		},
		uses([]Use{
			channelReadUses(SetBy("cursor"), SetBy("cursor"),
				Fixed("when the person last read the channel", "the unread posts are those written since; Mattermost's read around the last unread post leaves out the posts written in the first unread one's millisecond"),
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
				// A channel the person never opened has nothing read in it: all of
				// it is unread, and it reads as read_channel reads it, the newest
				// first and then further back.
				neverRead := membership.LastViewedAt == 0
				at.Back = neverRead
				var page []*model.Post
				var more bool
				switch {
				case neverRead || at.After != "":
					// Each page ends on a whole millisecond, the next reading on from it.
					page, more, err = readOn(func(perPage int) (*model.PostList, error) {
						if neverRead {
							return client.Posts(ctx, mattermost.PostsPage{ChannelID: input.ChannelID, PerPage: perPage, Before: at.After})
						}
						return client.Posts(ctx, mattermost.PostsPage{ChannelID: input.ChannelID, PerPage: perPage, After: at.After})
					}, neverRead, limit)
				default:
					page, more, err = sinceLastRead(ctx, client, input.ChannelID, membership.LastViewedAt, limit)
				}
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				posts, err := describePosts(ctx, client, page)
				if err != nil {
					return nil, UnreadPosts{}, err
				}
				out := UnreadPosts{ChannelID: input.ChannelID, Posts: posts}
				out.Channel, out.Team, out.Posts = inOneChannel(out.Posts)
				for _, post := range page {
					if post.UserId == self.Id || post.CreateAt <= membership.LastViewedAt {
						continue
					}
					out.Unread++
					if out.FirstUnreadID == "" {
						out.FirstUnreadID = post.Id
					}
				}
				switch {
				case len(page) == 0:
				case neverRead && more:
					at.After = page[0].Id
					out.NextCursor = at.String()
				case !neverRead && more:
					at.After = page[len(page)-1].Id
					out.NextCursor = at.String()
				}
				return nil, out, nil
			}
		},
	), nil)
}

// sinceLastRead is the first page of a channel from where the person stopped
// reading: a few posts they read, then those written since, oldest first, and
// whether more follow. With nothing written since, it is the last few posts.
func sinceLastRead(ctx context.Context, client *mattermost.Client, channelID string, lastRead int64, limit int) ([]*model.Post, bool, error) {
	unread, err := postsWrittenSince(ctx, client, channelID, false, lastRead+1)
	if err != nil {
		return nil, false, err
	}
	context := mattermost.PostsPage{ChannelID: channelID, PerPage: min(readBeforeUnread, limit-1)}
	if len(unread) == 0 {
		context.PerPage = min(readBeforeUnread, limit)
	} else {
		// Every post before the first unread one by time was written before the
		// person last read the channel.
		context.Before = unread[0].Id
	}
	var read []*model.Post
	if context.PerPage > 0 {
		list, err := client.Posts(ctx, context)
		if err != nil {
			return nil, false, err
		}
		read = orderedPosts(list, true)
	}
	page, more := firstOf(unread, limit-len(read))
	return append(read, page...), more, nil
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
