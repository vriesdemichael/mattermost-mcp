//go:build live

package live

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// list_mentions, and the person's own timezone in a search's days.

func listMentions(t *testing.T, session *mcp.ClientSession, arguments map[string]any) server.Mentions {
	t.Helper()
	var found server.Mentions
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_mentions", Arguments: arguments}), &found)
	return found
}

func TestListMentionsFindsWhatMentionsThePersonAsMattermostWouldNotifyThem(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	// A first name nobody else has, which the person's settings say mentions them.
	first := strings.ToUpper(word(t)[:1]) + word(t)[1:]
	props := user.NotifyProps
	props[model.FirstNameNotifyProp] = "true"
	_, _, err := admin.PatchUser(t.Context(), user.Id, &model.UserPatch{FirstName: &first, NotifyProps: props})
	check(t, err)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	them, us := clientAs(t, other), clientAs(t, user)
	byName := postAs(t, them, channel.Id, "", "@"+user.Username+" can you look at the release?")
	byFirstName := postAs(t, them, channel.Id, "", "What do you think, "+first+"?")
	everyone := postAs(t, them, channel.Id, "", "@channel standup in five")
	postAs(t, them, channel.Id, "", "nothing for anyone here")
	postAs(t, them, channel.Id, "", "`@"+user.Username+"` in code mentions nobody")
	postAs(t, us, channel.Id, "", "@"+user.Username+" a note to myself")
	session := sessionFor(t, admin, user)
	want := []string{everyone.Id, byFirstName.Id, byName.Id}

	var found server.Mentions
	eventually(t, 30*time.Second, func() (bool, string) {
		found = listMentions(t, session, map[string]any{"in": channel.Id})
		var ids []string
		for _, post := range found.Posts {
			ids = append(ids, post.ID)
		}
		return slices.Equal(ids, want), fmt.Sprint(ids)
	})
	for _, key := range []string{"@" + user.Username, first, "@channel"} {
		if !slices.Contains(found.MentionKeys, key) {
			t.Errorf("the mention keys %v leave out %s", found.MentionKeys, key)
		}
	}

	// Across every team, with nothing to narrow it.
	if all := listMentions(t, session, map[string]any{}); len(all.Posts) == 0 {
		t.Error("across every team found no mention")
	}

	// By channel name, in one team, and from the person who wrote them.
	inTeam := listMentions(t, session, map[string]any{"in": "~" + channel.Name, "team_id": team.Id, "from": other.Username})
	if len(inTeam.Posts) != 3 {
		t.Errorf("in by name, in the team, from %s: %d posts", other.Username, len(inTeam.Posts))
	}
	if result := callTool(t, session, &mcp.CallToolParams{Name: "list_mentions", Arguments: map[string]any{"from": other.Username + "x"}}); !result.IsError ||
		!strings.Contains(errorText(result), other.Username) {
		t.Errorf("a from nobody has: %s", errorText(result))
	}
}

func TestASearchsDaysAreThePersonsOwn(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	// A zone whose date differs from UTC's now: fourteen hours ahead late in
	// UTC's day, eleven behind early in it.
	now := time.Now().UTC()
	zoneName := "Pacific/Kiritimati"
	if now.Hour() < 10 {
		zoneName = "Pacific/Pago_Pago"
	}
	zone, err := time.LoadLocation(zoneName)
	check(t, err)
	_, _, err = admin.PatchUser(t.Context(), user.Id, &model.UserPatch{Timezone: model.StringMap{
		"useAutomaticTimezone": "false", "manualTimezone": zoneName, "automaticTimezone": "",
	}})
	check(t, err)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	term := word(t)
	postAs(t, clientAs(t, user), channel.Id, "", term+" today")
	session := sessionFor(t, admin, user)
	search := func(day string) int {
		var found server.SearchResults
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"terms": term, "on": day}}), &found)
		return len(found.Posts)
	}
	theirs, utc := time.Now().In(zone).Format(time.DateOnly), time.Now().UTC().Format(time.DateOnly)
	if theirs == utc {
		t.Fatalf("%s and UTC share the date %s; the test needs them apart", zoneName, utc)
	}

	eventually(t, 30*time.Second, func() (bool, string) {
		n := search(theirs)
		return n == 1, fmt.Sprintf("%d posts on %s in %s", n, theirs, zoneName)
	})
	if n := search(utc); n != 0 {
		t.Errorf("on UTC's date, %s, found %d posts; the person's date is %s", utc, n, theirs)
	}
	var me server.UserSummary
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_me", Arguments: map[string]any{}}), &me)
	if me.Timezone != zoneName {
		t.Errorf("get_me names the timezone %q", me.Timezone)
	}
}

func TestSearchesAndReadsRefuseWhatTheyCannotReadAndSayWhy(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	session := sessionFor(t, admin, user)

	for _, c := range []struct {
		call *mcp.CallToolParams
		says string
	}{
		{&mcp.CallToolParams{Name: "list_mentions", Arguments: map[string]any{"limit": 1000}}, "limit"},
		{&mcp.CallToolParams{Name: "list_mentions", Arguments: map[string]any{"cursor": "not-a-cursor"}}, "cursor"},
		{&mcp.CallToolParams{Name: "list_mentions", Arguments: map[string]any{"on": "yesterday"}}, "YYYY-MM-DD"},
		{&mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"terms": "x", "from": "someone@example.com"}}, "takes a username"},
		{&mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": channel.Id, "since": "last tuesday"}}, "since must be"},
		{&mcp.CallToolParams{Name: "list_team_channels", Arguments: map[string]any{"team_id": "no team is called this"}}, "no team is called"},
	} {
		result := callTool(t, session, c.call)
		if !result.IsError || !strings.Contains(errorText(result), c.says) {
			t.Errorf("%s %v: got %q; want it refused saying %q", c.call.Name, c.call.Arguments, errorText(result), c.says)
		}
	}
}
