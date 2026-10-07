//go:build live

package live

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// search_posts, against posts each test writes with words no other test uses.

func searchPosts(t *testing.T, session *mcp.ClientSession, arguments map[string]any) server.SearchResults {
	t.Helper()
	var found server.SearchResults
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_posts", Arguments: arguments}), &found)
	return found
}

func foundMessages(found server.SearchResults) []string {
	var out []string
	for _, post := range found.Posts {
		out = append(out, post.Message)
	}
	slices.Sort(out)
	return out
}

// word is a search term no other post holds: letters only, which every search
// backend tokenises as one word.
func word(t *testing.T) string {
	t.Helper()
	return "w" + strings.ReplaceAll(uniqueName("x"), "-", "")
}

func TestSearchPostsFindsAPostWithItsAuthorAndChannel(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	term := word(t)
	posted := postAs(t, clientAs(t, other), channel.Id, "", "the "+term+" is ready")
	session := sessionFor(t, admin, user)

	var found server.SearchResults
	eventually(t, 30*time.Second, func() (bool, string) {
		found = searchPosts(t, session, map[string]any{"terms": term})
		return len(found.Posts) == 1, fmt.Sprint(foundMessages(found))
	})
	post := found.Posts[0]
	if post.ID != posted.Id || post.Author != other.Username || post.ChannelID != channel.Id || post.Channel != channel.DisplayName {
		t.Fatalf("got %+v; want %s by %s in %s", post, posted.Id, other.Username, channel.DisplayName)
	}
}

func TestSearchPostsNamesADirectMessageByThePersonOnTheOtherSide(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	author := clientAs(t, user)
	withOther, _, err := author.CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)
	toSelf, _, err := author.CreateDirectChannel(t.Context(), user.Id, user.Id)
	check(t, err)
	term := word(t)
	postAs(t, author, withOther.Id, "", term+" to them")
	postAs(t, author, toSelf.Id, "", term+" to me")
	session := sessionFor(t, admin, user)

	var found server.SearchResults
	eventually(t, 30*time.Second, func() (bool, string) {
		found = searchPosts(t, session, map[string]any{"terms": term})
		return len(found.Posts) == 2, fmt.Sprint(foundMessages(found))
	})
	want := map[string]string{withOther.Id: other.Username, toSelf.Id: "yourself"}
	for _, post := range found.Posts {
		if post.Channel != want[post.ChannelID] {
			t.Errorf("%q is in a channel named %q; want %q", post.Message, post.Channel, want[post.ChannelID])
		}
	}
}

func TestSearchPostsKeepsToOneTeamWhenTold(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team, elsewhere := seedTeam(t, admin, user), seedTeam(t, admin, user)
	here, there := seedChannel(t, admin, team, user), seedChannel(t, admin, elsewhere, user)
	term := word(t)
	author := clientAs(t, user)
	postAs(t, author, here.Id, "", term+" here")
	postAs(t, author, there.Id, "", term+" there")
	session := sessionFor(t, admin, user)

	eventually(t, 30*time.Second, func() (bool, string) {
		found := searchPosts(t, session, map[string]any{"terms": term})
		return len(found.Posts) == 2, fmt.Sprint(foundMessages(found))
	})
	inTeam := searchPosts(t, session, map[string]any{"terms": term, "team_id": team.Id})
	if got := foundMessages(inTeam); !slices.Equal(got, []string{term + " here"}) {
		t.Fatalf("team_id %s: got %v", team.Id, got)
	}
}

func TestSearchPostsMatchesAllWordsOrAny(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	one, two := word(t), word(t)
	author := clientAs(t, user)
	postAs(t, author, channel.Id, "", one+" alone")
	postAs(t, author, channel.Id, "", two+" alone")
	session := sessionFor(t, admin, user)

	eventually(t, 30*time.Second, func() (bool, string) {
		found := searchPosts(t, session, map[string]any{"terms": one + " " + two, "match_any": true})
		return len(found.Posts) == 2, fmt.Sprint(foundMessages(found))
	})
	if all := searchPosts(t, session, map[string]any{"terms": one + " " + two}); len(all.Posts) != 0 {
		t.Fatalf("all of two words no post holds together found %v", foundMessages(all))
	}
}

func TestSearchPostsFindsMentionsAndWhatSomeoneWrote(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	term := word(t)
	postAs(t, clientAs(t, other), channel.Id, "", term+" for @"+user.Username)
	postAs(t, clientAs(t, user), channel.Id, "", term+" from me")
	session := sessionFor(t, admin, user)

	eventually(t, 30*time.Second, func() (bool, string) {
		found := searchPosts(t, session, map[string]any{"terms": "@" + user.Username + " " + term})
		return slices.Equal(foundMessages(found), []string{term + " for @" + user.Username}), fmt.Sprint(foundMessages(found))
	})
	eventually(t, 30*time.Second, func() (bool, string) {
		found := searchPosts(t, session, map[string]any{"terms": "from:" + user.Username + " " + term})
		return slices.Equal(foundMessages(found), []string{term + " from me"}), fmt.Sprint(foundMessages(found))
	})
}

// Mattermost's database search, which Team Edition uses, answers the first page
// with every match and later pages with nothing, so the tool asks once and
// applies the limit itself.
func TestSearchPostsReturnsAtMostTheLimitAndSaysWhenItCutSomeOff(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	term := word(t)
	author := clientAs(t, user)
	for i := range 3 {
		postAs(t, author, channel.Id, "", fmt.Sprintf("%s number %d", term, i))
	}
	session := sessionFor(t, admin, user)

	eventually(t, 30*time.Second, func() (bool, string) {
		found := searchPosts(t, session, map[string]any{"terms": term})
		return len(found.Posts) == 3 && found.NextCursor == "", fmt.Sprint(foundMessages(found))
	})
	cut := searchPosts(t, session, map[string]any{"terms": term, "limit": 2})
	if len(cut.Posts) != 2 || cut.NextCursor == "" {
		t.Fatalf("limit 2 of 3: got %v (next cursor %q)", foundMessages(cut), cut.NextCursor)
	}
	if want := []string{term + " number 1", term + " number 2"}; !slices.Equal(foundMessages(cut), want) {
		t.Fatalf("limit 2 kept %v; want the two most recent, %v", foundMessages(cut), want)
	}
	rest := searchPosts(t, session, map[string]any{"terms": term, "limit": 2, "cursor": cut.NextCursor})
	if got := foundMessages(rest); !slices.Equal(got, []string{term + " number 0"}) || rest.NextCursor != "" {
		t.Fatalf("the next page: got %v (next cursor %q)", got, rest.NextCursor)
	}
}
