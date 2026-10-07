//go:build live

package live

import (
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// The read tools, against posts and memberships each test seeds.

func sessionFor(t *testing.T, admin *model.Client4, user *model.User) *mcp.ClientSession {
	t.Helper()
	return mcpAs(t, personalAccessToken(t, admin, user.Id).Token)
}

func listChannels(t *testing.T, session *mcp.ClientSession, arguments map[string]any) map[string]server.Channel {
	t.Helper()
	var channels server.Channels
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_channels", Arguments: arguments}), &channels)
	byID := map[string]server.Channel{}
	for _, channel := range channels.Channels {
		byID[channel.ID] = channel
	}
	return byID
}

func count(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

func TestListTeamsNamesTheTeamsTheUserBelongsTo(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	theirs := seedTeam(t, admin, user)
	other := seedTeam(t, admin)

	var teams server.Teams
	structured(t, callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "list_teams", Arguments: map[string]any{}}), &teams)

	var ids []string
	for _, team := range teams.Teams {
		ids = append(ids, team.ID)
		if team.ID == theirs.Id && (team.Name != theirs.Name || team.DisplayName != theirs.DisplayName) {
			t.Errorf("got %+v for %s", team, theirs.Name)
		}
	}
	if !slices.Contains(ids, theirs.Id) || slices.Contains(ids, other.Id) {
		t.Fatalf("got %v; want %s and not %s", ids, theirs.Id, other.Id)
	}
}

func TestListChannelsCountsWhatTheUserHasNotRead(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	busy := seedChannel(t, admin, team, user, other)
	session := sessionFor(t, admin, user)
	before := listChannels(t, session, map[string]any{})[busy.Id]

	author := clientAs(t, other)
	postAs(t, author, busy.Id, "", "first")
	postAs(t, author, busy.Id, "", "second")
	postAs(t, author, busy.Id, "", "a word for @"+user.Username)

	after := listChannels(t, session, map[string]any{})[busy.Id]
	if after.Name != busy.Name || after.Type != "public" || after.TeamID != team.Id {
		t.Fatalf("got %+v for %s", after, busy.Name)
	}
	if count(after.Unread) != count(before.Unread)+3 {
		t.Errorf("unread went from %d to %d after three posts", count(before.Unread), count(after.Unread))
	}
	if count(after.Mentions) != count(before.Mentions)+1 {
		t.Errorf("mentions went from %d to %d after one mention", count(before.Mentions), count(after.Mentions))
	}
	if after.LastPostAt == "" {
		t.Error("a channel just posted in has no last_post_at")
	}
}

func TestListChannelsNamesTheOtherPersonOfADirectMessage(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	author := clientAs(t, other)
	direct, _, err := author.CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	postAs(t, author, direct.Id, "", "hello")

	listed, ok := listChannels(t, sessionFor(t, admin, user), map[string]any{})[direct.Id]
	if !ok {
		t.Fatal("the direct message is not listed")
	}
	if listed.Type != "direct" || listed.DisplayName != other.Username || listed.TeamID != "" {
		t.Fatalf("got %+v; want a direct message named %s", listed, other.Username)
	}
	if count(listed.Unread) != 1 {
		t.Errorf("the direct message holds one unread post; got %d", count(listed.Unread))
	}
}

func TestListChannelsFiltersByTeamAndByWhatIsUnread(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team, elsewhere := seedTeam(t, admin, user, other), seedTeam(t, admin, user)
	busy, quiet := seedChannel(t, admin, team, user, other), seedChannel(t, admin, team, user)
	away := seedChannel(t, admin, elsewhere, user)
	postAs(t, clientAs(t, other), busy.Id, "", "news")
	reader := clientAs(t, user)
	_, _, err := reader.ViewChannel(t.Context(), user.Id, &model.ChannelView{ChannelId: quiet.Id})
	check(t, err)
	session := sessionFor(t, admin, user)

	inTeam := listChannels(t, session, map[string]any{"team_id": team.Id})
	if _, ok := inTeam[away.Id]; ok {
		t.Error("team_id kept a channel of another team")
	}
	if _, ok := inTeam[busy.Id]; !ok {
		t.Error("team_id dropped a channel of the team")
	}
	unread := listChannels(t, session, map[string]any{"unread_only": true})
	if _, ok := unread[busy.Id]; !ok {
		t.Error("unread_only dropped a channel with an unread post")
	}
	if _, ok := unread[quiet.Id]; ok {
		t.Error("unread_only kept a channel the user has read")
	}
}

func readChannel(t *testing.T, session *mcp.ClientSession, arguments map[string]any) server.ChannelPosts {
	t.Helper()
	var posts server.ChannelPosts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: arguments}), &posts)
	return posts
}

func messages(posts []server.Post) []string {
	var out []string
	for _, post := range posts {
		if post.Type == "" {
			out = append(out, post.Author+": "+post.Message)
		}
	}
	return out
}

func TestReadChannelReturnsTheConversationOldestFirstAndPagesThroughIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	them, us := clientAs(t, other), clientAs(t, user)
	first := postAs(t, them, channel.Id, "", "one")
	second := postAs(t, us, channel.Id, "", "two")
	postAs(t, them, channel.Id, "", "three")
	session := sessionFor(t, admin, user)

	latest := readChannel(t, session, map[string]any{"channel_id": channel.Id, "limit": 2})
	want := []string{user.Username + ": two", other.Username + ": three"}
	if got := messages(latest.Posts); !slices.Equal(got, want) || !latest.MoreBefore {
		t.Fatalf("the newest two: got %v (more_before %v), want %v", got, latest.MoreBefore, want)
	}

	earlier := readChannel(t, session, map[string]any{"channel_id": channel.Id, "before": second.Id})
	if got := messages(earlier.Posts); !slices.Equal(got, []string{other.Username + ": one"}) {
		t.Fatalf("before %s: got %v", second.Id, got)
	}

	later := readChannel(t, session, map[string]any{"channel_id": channel.Id, "after": first.Id})
	if got := messages(later.Posts); !slices.Equal(got, []string{user.Username + ": two", other.Username + ": three"}) {
		t.Fatalf("after %s: got %v", first.Id, got)
	}
}

func TestReadChannelRefusesBeforeAndAfterTogetherAndALimitOutOfRange(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	session := sessionFor(t, admin, user)
	for _, arguments := range []map[string]any{
		{"channel_id": "x", "before": "a", "after": "b"},
		{"channel_id": "x", "limit": 201},
	} {
		if result := callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: arguments}); !result.IsError {
			t.Errorf("%v was accepted", arguments)
		}
	}
}

func TestReadThreadReturnsTheRootAndEveryReplyWithReactions(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	them, us := clientAs(t, other), clientAs(t, user)
	root := postAs(t, them, channel.Id, "", "a question")
	reply := postAs(t, us, channel.Id, root.Id, "an answer")
	postAs(t, them, channel.Id, root.Id, "thanks")
	_, _, err := them.SaveReaction(t.Context(), &model.Reaction{UserId: other.Id, PostId: reply.Id, EmojiName: "+1"})
	check(t, err)

	var thread server.Thread
	structured(t, callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "read_thread", Arguments: map[string]any{"post_id": reply.Id}}), &thread)

	want := []string{other.Username + ": a question", user.Username + ": an answer", other.Username + ": thanks"}
	if got := messages(thread.Posts); thread.RootID != root.Id || !slices.Equal(got, want) {
		t.Fatalf("got root %s and %v; want %s and %v", thread.RootID, got, root.Id, want)
	}
	if thread.Posts[1].Reactions["+1"] != 1 || thread.Posts[1].RootID != root.Id {
		t.Fatalf("the answer reads as %+v; want one +1 and root %s", thread.Posts[1], root.Id)
	}
}

func TestGetUserFindsAUserByUsernameOrIdAndNamesOneThatDoesNotExist(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	session := sessionFor(t, admin, user)

	for _, arguments := range []map[string]any{{"username": "@" + other.Username}, {"user_id": other.Id}} {
		var found server.UserSummary
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_user", Arguments: arguments}), &found)
		if found.ID != other.Id || found.Username != other.Username || found.FirstName != "Live" {
			t.Errorf("%v: got %+v", arguments, found)
		}
	}
	missing := callTool(t, session, &mcp.CallToolParams{Name: "get_user", Arguments: map[string]any{"username": uniqueName("nobody")}})
	if !missing.IsError || !strings.Contains(errorText(missing), "Mattermost answered 404") {
		t.Fatalf("an unknown username: got %s", errorText(missing))
	}
	if both := callTool(t, session, &mcp.CallToolParams{Name: "get_user", Arguments: map[string]any{"username": "a", "user_id": "b"}}); !both.IsError {
		t.Fatal("a username and a user_id together were accepted")
	}
}

func TestSearchUsersFindsUsersByPartOfTheirNameWithinATeam(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	elsewhere := seedTeam(t, admin, user)
	session := sessionFor(t, admin, user)
	search := func(arguments map[string]any) []string {
		var found server.Users
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_users", Arguments: arguments}), &found)
		var names []string
		for _, u := range found.Users {
			names = append(names, u.Username)
		}
		return names
	}

	if got := search(map[string]any{"term": other.Username}); !slices.Contains(got, other.Username) {
		t.Errorf("searching the whole server: got %v", got)
	}
	if got := search(map[string]any{"term": other.Username, "team_id": team.Id}); !slices.Contains(got, other.Username) {
		t.Errorf("searching their team: got %v", got)
	}
	if got := search(map[string]any{"term": other.Username, "team_id": elsewhere.Id}); slices.Contains(got, other.Username) {
		t.Errorf("searching a team they are not in found them: %v", got)
	}
}
