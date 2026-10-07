//go:build live

package live

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Paging (ADR-032): every list, read a page at a time to its end, holds every
// item once; a cursor continues only the list it came from.

// inParallel runs work n times, eight at a time, for fixtures too many to
// seed one after the other.
func inParallel(t *testing.T, n int, work func(i int)) {
	t.Helper()
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	for i := range n {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			work(i)
		}()
	}
	wg.Wait()
}

// Mattermost gives followed threads and saved posts 200 at a time; a list
// longer than that shows the tools read past its pages. The threads are
// replied to ten in each millisecond, so one of Mattermost's pages ends inside
// one.
func TestListsLongerThanMattermostsPageAreReadToTheirEnd(t *testing.T) {
	t.Parallel()
	const many = 205
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author := clientAs(t, user)
	base := model.GetMillis() + 1000
	roots := make([]string, many)
	inParallel(t, many, func(i int) {
		root, _, err := author.CreatePost(t.Context(), &model.Post{ChannelId: channel.Id, Message: fmt.Sprintf("thread %d", i)})
		if err != nil {
			t.Error(err)
			return
		}
		roots[i] = root.Id
		reply, _, err := admin.CreatePost(t.Context(), &model.Post{ChannelId: channel.Id, RootId: root.Id, Message: "reply", CreateAt: base + int64(i/10)})
		if err != nil || reply.CreateAt != base+int64(i/10) {
			t.Errorf("replied at %v: %v", reply, err)
		}
	})
	if t.Failed() {
		t.FailNow()
	}
	var saved model.Preferences
	for _, root := range roots {
		saved = append(saved, model.Preference{UserId: user.Id, Category: model.PreferenceCategoryFlaggedPost, Name: root, Value: "true"})
	}
	// Mattermost takes at most 100 preferences in one request.
	for start := 0; start < len(saved); start += 100 {
		_, err := author.UpdatePreferences(t.Context(), user.Id, saved[start:min(start+100, len(saved))])
		check(t, err)
	}
	session := sessionFor(t, admin, user)

	threads := flat(t, everyPage(t, session, &mcp.CallToolParams{Name: "list_threads", Arguments: map[string]any{"team_id": team.Id}}, 100, "threads", "root_id"))
	if len(threads) != many || !sameItems(threads, roots) {
		t.Errorf("list_threads paged through %d threads; want the %d the user started", len(threads), many)
	}
	savedPosts := flat(t, everyPage(t, session, &mcp.CallToolParams{Name: "list_saved", Arguments: map[string]any{}}, 100, "posts", "id"))
	if len(savedPosts) != many || !sameItems(savedPosts, roots) {
		t.Errorf("list_saved paged through %d posts; want the %d saved", len(savedPosts), many)
	}
	// Two of Mattermost's pages hold them all, and a third, empty, says so: its
	// page parameter is an offset in posts, and asked as a page number it would
	// take one request a post.
	if reads := reachedLast(t, session, "GetFlaggedPostsForUser"); reads != 3 {
		t.Errorf("reading %d saved posts took %d requests; want 3", many, reads)
	}
}

func sameItems(got, want []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want)))
}

func TestReadPostPagesThroughALongThreadOldestFirst(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	author := clientAs(t, user)
	root := postAs(t, author, channel.Id, "", "the root")
	want := []string{root.Id}
	for i := range 5 {
		want = append(want, postAs(t, author, channel.Id, root.Id, fmt.Sprintf("reply %d", i)).Id)
	}

	pages := everyPage(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": want[3]}}, 2, "thread", "id")
	if got := flat(t, pages); !slices.Equal(got, want) {
		t.Fatalf("paged through %v; want the root and its replies in order, %v", pages, want)
	}
}

func TestReadUnreadPagesForwardThroughWhatWasNotRead(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	_, _, err := clientAs(t, user).ViewChannel(t.Context(), user.Id, &model.ChannelView{ChannelId: channel.Id})
	check(t, err)
	time.Sleep(5 * time.Millisecond)
	var unread []string
	author := clientAs(t, other)
	for i := range 5 {
		unread = append(unread, postAs(t, author, channel.Id, "", fmt.Sprintf("news %d", i)).Id)
	}

	got := flat(t, everyPage(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "read_unread", Arguments: map[string]any{"channel_id": channel.Id}}, 2, "posts", "id"))
	if len(got) < len(unread) || !slices.Equal(got[len(got)-len(unread):], unread) {
		t.Fatalf("paged through %v; want it to end with the unread posts in order, %v", got, unread)
	}
}

func TestEveryOtherListPagesThroughAllItHolds(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	tag := word(t)
	teams := []*model.Team{seedTeam(t, admin, user, other), seedTeam(t, admin, user), seedTeam(t, admin, user)}
	team := teams[0]
	author := clientAs(t, user)
	var channels, archived, pinned, files []string
	for i := range 3 {
		channel := namedChannel(t, admin, team, fmt.Sprintf("%s %d", tag, i), user, other)
		channels = append(channels, channel.Id)
		gone := seedChannel(t, admin, team, user)
		_, err := admin.DeleteChannel(t.Context(), gone.Id)
		check(t, err)
		archived = append(archived, gone.Id)
		post := postAs(t, author, channels[0], "", fmt.Sprintf("pinned %d", i))
		_, err = admin.PinPost(t.Context(), post.Id)
		check(t, err)
		pinned = append(pinned, post.Id)
		_, file := attachAs(t, author, channels[0], fmt.Sprintf("%s-%d.txt", tag, i), []byte("x"))
		files = append(files, file)
	}
	writer, _ := writingSession(t, admin, user, accept)
	for _, channel := range channels {
		structured(t, saveDraft(t, writer, map[string]any{"channel_id": channel, "message": "draft in " + channel}), &struct{}{})
	}
	session := sessionFor(t, admin, user)

	for _, c := range []struct {
		first     *mcp.CallToolParams
		list, key string
		want      []string
	}{
		{&mcp.CallToolParams{Name: "get_user_teams", Arguments: map[string]any{}}, "teams", "id", []string{teams[0].Id, teams[1].Id, teams[2].Id}},
		{&mcp.CallToolParams{Name: "search_channels", Arguments: map[string]any{"term": tag}}, "channels", "id", channels},
		{&mcp.CallToolParams{Name: "list_archived_channels", Arguments: map[string]any{"team_id": team.Id}}, "channels", "id", archived},
		{&mcp.CallToolParams{Name: "list_pinned_posts", Arguments: map[string]any{"channel_id": channels[0]}}, "posts", "id", pinned},
		{&mcp.CallToolParams{Name: "list_drafts", Arguments: map[string]any{"team_id": team.Id}}, "drafts", "channel_id", channels},
		{&mcp.CallToolParams{Name: "search_users", Arguments: map[string]any{"term": "lt-user", "team_id": team.Id}}, "users", "id", []string{user.Id, other.Id}},
	} {
		if got := flat(t, everyPage(t, session, c.first, 2, c.list, c.key)); !sameItems(got, c.want) {
			t.Errorf("%s paged through %v; want %v", c.first.Name, got, c.want)
		}
	}

	// The user's channels hold the three, town square and off-topic of each team.
	mine := flat(t, everyPage(t, session, &mcp.CallToolParams{Name: "get_user_channels", Arguments: map[string]any{"team_id": team.Id}}, 2, "channels", "id"))
	for _, channel := range channels {
		if !slices.Contains(mine, channel) {
			t.Errorf("get_user_channels paged past %s", channel)
		}
	}
	eventually(t, 30*time.Second, func() (bool, string) {
		got := flat(t, everyPage(t, session, &mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{"terms": tag, "team_id": team.Id}}, 2, "files", "id"))
		return sameItems(got, files), fmt.Sprint(got)
	})
}

func TestACursorContinuesOnlyTheListItCameFrom(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	seedTeam(t, admin, user)
	seedTeam(t, admin, user)
	seedTeam(t, admin, user)
	session := sessionFor(t, admin, user)

	var first struct {
		NextCursor string `json:"next_cursor"`
	}
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_user_teams", Arguments: map[string]any{"limit": 1}}), &first)
	if first.NextCursor == "" {
		t.Fatal("one team of three gave no cursor")
	}
	for _, c := range []struct {
		params *mcp.CallToolParams
		says   string
	}{
		{&mcp.CallToolParams{Name: "get_user_channels", Arguments: map[string]any{"cursor": first.NextCursor}}, "continues get_user_teams"},
		{&mcp.CallToolParams{Name: "get_user_teams", Arguments: map[string]any{"cursor": "not-a-cursor"}}, "not one"},
		{&mcp.CallToolParams{Name: "search_users", Arguments: map[string]any{"term": "a", "cursor": first.NextCursor}}, "continues get_user_teams"},
	} {
		result := callTool(t, session, c.params)
		if !result.IsError || !strings.Contains(errorText(result), c.says) {
			t.Errorf("%s with a cursor not its own: %s", c.params.Name, errorText(result))
		}
	}
	// The same arguments with another limit continue the list: the two teams
	// after the first, and nothing after them.
	var rest struct {
		Teams      []map[string]any `json:"teams"`
		NextCursor string           `json:"next_cursor"`
	}
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_user_teams", Arguments: map[string]any{"limit": 5, "cursor": first.NextCursor}}), &rest)
	if len(rest.Teams) != 2 || rest.NextCursor != "" {
		t.Fatalf("the rest with another limit: %d teams, next cursor %q; want 2 and none", len(rest.Teams), rest.NextCursor)
	}
}

// Mattermost reads on from a post by its time alone, so a page that ended
// inside a millisecond would lose the rest of it: every read back or forward
// from a post, with three posts a page and four posts in each millisecond,
// still reads every post once, a page holding a millisecond's four whole, and
// read_unread's first the two read posts before them too.
func TestAPageEndingInsideAMillisecondLosesNoPost(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	read, unread := seedChannel(t, admin, team, user), seedChannel(t, admin, team, user)
	_, _, err := clientAs(t, user).ViewChannel(t.Context(), user.Id, &model.ChannelView{ChannelId: unread.Id})
	check(t, err)
	base := model.GetMillis() + 1000
	seed := func(channelID string) []string {
		var ids []string
		for i := range 14 {
			ids = append(ids, postAt(t, admin, channelID, "", fmt.Sprintf("tied %d", i), base+int64(i/4)).Id)
		}
		return ids
	}
	inRead, inUnread := seed(read.Id), seed(unread.Id)
	session := sessionFor(t, admin, user)

	for name, c := range map[string]struct {
		args map[string]any
		tool string
		most int
		want []string
	}{
		"read_channel back from the newest": {map[string]any{"channel_id": read.Id}, "read_channel", 4, inRead},
		"read_channel forward":              {map[string]any{"channel_id": read.Id, "after": inRead[3]}, "read_channel", 4, inRead[4:]},
		"read_channel since":                {map[string]any{"channel_id": read.Id, "since": time.UnixMilli(base).UTC().Format(time.RFC3339Nano)}, "read_channel", 4, inRead},
		"read_unread never opened":          {map[string]any{"channel_id": read.Id}, "read_unread", 4, inRead},
		"read_unread forward":               {map[string]any{"channel_id": unread.Id}, "read_unread", 6, inUnread},
	} {
		// A read back to the channel's start also holds Mattermost's own
		// messages of who joined it.
		got := flat(t, pagesOfAtMost(t, session, &mcp.CallToolParams{Name: c.tool, Arguments: c.args}, 3, c.most, "posts", "id"))
		for _, id := range c.want {
			if !slices.Contains(got, id) {
				t.Errorf("%s read %d posts, not %s: %v", name, len(got), id, got)
				break
			}
		}
	}
}
