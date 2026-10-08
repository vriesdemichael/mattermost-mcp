//go:build live

package live

import (
	"context"
	"fmt"
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

func getUserChannels(t *testing.T, session *mcp.ClientSession, arguments map[string]any) map[string]server.Channel {
	t.Helper()
	var channels server.Channels
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_user_channels", Arguments: arguments}), &channels)
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

func TestGetUserTeamsNamesTheTeamsTheUserBelongsTo(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	theirs := seedTeam(t, admin, user)
	other := seedTeam(t, admin)

	var teams server.Teams
	structured(t, callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "get_user_teams", Arguments: map[string]any{}}), &teams)

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

func TestGetUserChannelsCountsWhatTheUserHasNotRead(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	busy := seedChannel(t, admin, team, user, other)
	session := sessionFor(t, admin, user)
	before := getUserChannels(t, session, map[string]any{})[busy.Id]

	author := clientAs(t, other)
	postAs(t, author, busy.Id, "", "first")
	postAs(t, author, busy.Id, "", "second")
	postAs(t, author, busy.Id, "", "a word for @"+user.Username)

	after := getUserChannels(t, session, map[string]any{})[busy.Id]
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

func TestGetUserChannelsNamesTheOtherPersonOfADirectMessage(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	author := clientAs(t, other)
	direct, _, err := author.CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	postAs(t, author, direct.Id, "", "hello")

	listed, ok := getUserChannels(t, sessionFor(t, admin, user), map[string]any{})[direct.Id]
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

// Mattermost names a direct message to oneself with the user's id on both
// sides, which its own helper for the other person answers with nothing.
func TestGetUserChannelsNamesADirectMessageToOneselfYourself(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	seedTeam(t, admin, user)
	direct, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, user.Id)
	check(t, err)

	listed, ok := getUserChannels(t, sessionFor(t, admin, user), map[string]any{})[direct.Id]
	if !ok {
		t.Fatal("the direct message to oneself is not listed")
	}
	if listed.Type != "direct" || listed.DisplayName != "yourself" {
		t.Fatalf("got %+v; want a direct message named yourself", listed)
	}
}

func TestGetUserChannelsFiltersByTeamAndByWhatIsUnread(t *testing.T) {
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

	inTeam := getUserChannels(t, session, map[string]any{"team_id": team.Id})
	if _, ok := inTeam[away.Id]; ok {
		t.Error("team_id kept a channel of another team")
	}
	if _, ok := inTeam[busy.Id]; !ok {
		t.Error("team_id dropped a channel of the team")
	}
	unread := getUserChannels(t, session, map[string]any{"unread_only": true})
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
	if got := messages(latest.Posts); !slices.Equal(got, want) || latest.NextCursor == "" {
		t.Fatalf("the newest two: got %v (next cursor %q), want %v", got, latest.NextCursor, want)
	}
	// The channel is named once for every post, and each post links to itself.
	if latest.Channel != channel.DisplayName || latest.Team != team.DisplayName {
		t.Errorf("the answer names channel %q in team %q", latest.Channel, latest.Team)
	}
	for _, post := range latest.Posts {
		if post.Channel != "" || post.ChannelID != "" || post.Team != "" || post.URL != liveURL+"/_redirect/pl/"+post.ID {
			t.Errorf("a post reads %+v", post)
		}
	}
	older := readChannel(t, session, map[string]any{"channel_id": channel.Id, "limit": 2, "cursor": latest.NextCursor})
	if got := messages(older.Posts); !slices.Contains(got, other.Username+": one") {
		t.Fatalf("the page before the newest two: got %v", got)
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

func TestReadPostReturnsTheRootAndEveryReplyWithReactions(t *testing.T) {
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

	session := sessionFor(t, admin, user)
	var read server.PostWithThread
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": reply.Id}}), &read)

	want := []string{other.Username + ": a question", user.Username + ": an answer", other.Username + ": thanks"}
	if got := messages(read.Thread); read.RootID != root.Id || !slices.Equal(got, want) {
		t.Fatalf("got root %s and %v; want %s and %v", read.RootID, got, root.Id, want)
	}
	answer := read.Post
	switch {
	case answer.ID != reply.Id || answer.RootID != root.Id:
		t.Fatalf("the post read is %+v; want %s in %s", answer, reply.Id, root.Id)
	case !slices.Equal(answer.Reactions["+1"], []string{other.Username}):
		t.Errorf("the answer's reactions read %v; want +1 by %s", answer.Reactions, other.Username)
	case answer.Channel != channel.DisplayName || answer.Team != team.DisplayName || answer.AuthorName != "Live Test":
		t.Errorf("the answer names channel %q, team %q, author %q", answer.Channel, answer.Team, answer.AuthorName)
	}

	var alone server.PostWithThread
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": reply.Id, "include_thread": false}}), &alone)
	if alone.Post.ID != reply.Id || alone.RootID != root.Id || len(alone.Thread) != 0 {
		t.Fatalf("include_thread false read %+v", alone)
	}
}

func TestGetUsersFindsPeopleByUsernameIdOrEmailInOneCall(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other, third := seedUser(t, admin), seedUser(t, admin), seedUser(t, admin)
	// Only an administrator sees email addresses by default.
	session := mcpAs(t, admin.AuthToken)

	var found server.Users
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_users", Arguments: map[string]any{
		"users": []string{"@" + user.Username, other.Id, third.Email},
	}}), &found)
	var got []string
	for _, person := range found.Users {
		got = append(got, person.Username)
	}
	if want := []string{user.Username, other.Username, third.Username}; !slices.Equal(got, want) {
		t.Fatalf("got %v; want %v, in the order asked", got, want)
	}
	if found.Users[0].FirstName != "Live" {
		t.Errorf("got %+v", found.Users[0])
	}
}

func TestGetUsersNamesTheClosestUsernamesForOneNobodyHas(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	session := sessionFor(t, admin, user)

	// A slip in the last letter of a real username.
	slip := other.Username[:len(other.Username)-1] + "q"
	missing := callTool(t, session, &mcp.CallToolParams{Name: "get_users", Arguments: map[string]any{"users": []string{slip}}})
	if !missing.IsError || !strings.Contains(errorText(missing), `"`+other.Username+`"`) {
		t.Fatalf("an unknown username: got %s; want it to suggest %s", errorText(missing), other.Username)
	}
}

// TestTheClosestUsernameIsFoundAmongManyThatShareItsStart: more people share
// a username's first letters than one search page holds, as every jon… does in
// a large organisation, and the one meant is still suggested.
func TestTheClosestUsernameIsFoundAmongManyThatShareItsStart(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	crowd := uniqueName("crowd")
	meant := crowd + "-meant"
	for i := range 30 {
		username := fmt.Sprintf("%s-%02d", crowd, i)
		if i == 29 {
			username = meant
		}
		created, _, err := admin.CreateUser(t.Context(), &model.User{Username: username, Password: fixturePassword, Email: username + "@example.com"})
		check(t, err)
		t.Cleanup(func() { _, _ = admin.DeleteUser(context.Background(), created.Id) })
	}
	session := sessionFor(t, admin, user)

	slip := crowd + "-maent"
	missing := callTool(t, session, &mcp.CallToolParams{Name: "get_users", Arguments: map[string]any{"users": []string{slip}}})
	if !missing.IsError || !strings.Contains(errorText(missing), `"`+meant+`"`) {
		t.Fatalf("got %s; want it to suggest %s", errorText(missing), meant)
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
