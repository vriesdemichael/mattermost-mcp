//go:build live

package live

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// join_channel, leave_channel, add_channel_members, create_channel,
// mark_channel_read and set_status.

func member(t *testing.T, admin *model.Client4, channelID, userID string) bool {
	t.Helper()
	_, response, err := admin.GetChannelMember(t.Context(), channelID, userID, "")
	if err != nil && (response == nil || response.StatusCode != 404) {
		t.Fatal(err)
	}
	return err == nil
}

func TestJoinAndLeaveAChannelByItsNameAskingEachTime(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, other)
	// A direct message, which a channel's name is matched against as well.
	_, _, err := clientAs(t, other).CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)

	var joined server.Membership
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "join_channel", Arguments: map[string]any{"channel_id": "~" + channel.Name}}), &joined)
	if !joined.Member || joined.ChannelID != channel.Id || !member(t, admin, channel.Id, user.Id) {
		t.Fatalf("joined %+v", joined)
	}
	mustContain(t, "question", questions.only(t).Message, "Join ~"+channel.DisplayName, "sees that you joined")
	if again := callTool(t, session, &mcp.CallToolParams{Name: "join_channel", Arguments: map[string]any{"channel_id": channel.Id}}); !again.IsError || !strings.Contains(errorText(again), "already") {
		t.Errorf("joining again: %s", errorText(again))
	}

	var left server.Membership
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "leave_channel", Arguments: map[string]any{"channel_id": channel.DisplayName}}), &left)
	if left.Member || member(t, admin, channel.Id, user.Id) {
		t.Fatalf("left %+v, and still a member: %v", left, member(t, admin, channel.Id, user.Id))
	}
	if questions.count() != 2 {
		t.Errorf("asked %d questions for a join and a leave", questions.count())
	}
}

func TestAddChannelMembersAddsThePeopleTheQuestionNamed(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, first, second := seedUser(t, admin), seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, first, second)
	channel := seedChannel(t, admin, team, user)
	_, _, err := clientAs(t, first).CreateDirectChannel(t.Context(), first.Id, user.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)

	var added server.Membership
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "add_channel_members", Arguments: map[string]any{
		"channel_id": channel.Name, "usernames": []string{"@" + first.Username, second.Email},
	}}), &added)

	if len(added.Added) != 2 || !member(t, admin, channel.Id, first.Id) || !member(t, admin, channel.Id, second.Id) {
		t.Fatalf("added %+v", added)
	}
	mustContain(t, "question", questions.only(t).Message, "@"+first.Username, "@"+second.Username, "~"+channel.DisplayName)
	unknown := callTool(t, session, &mcp.CallToolParams{Name: "add_channel_members", Arguments: map[string]any{
		"channel_id": channel.Id, "usernames": []string{first.Username + "x"},
	}})
	if !unknown.IsError || !strings.Contains(errorText(unknown), first.Username) {
		t.Errorf("an unknown username: %s", errorText(unknown))
	}
}

func TestCreateChannelMakesItsAddressFromItsName(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	session, questions := writingSession(t, admin, user, accept)
	display := "Release Planning " + strings.ToUpper(word(t)[:6])

	var created server.Channel
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "create_channel", Arguments: map[string]any{
		"team_id": team.DisplayName, "display_name": display, "private": true, "purpose": "plan the release",
	}}), &created)
	t.Cleanup(func() { _, _ = admin.DeleteChannel(context.Background(), created.ID) })

	if created.DisplayName != display || created.Type != "private" || created.Name != strings.ToLower(strings.ReplaceAll(display, " ", "-")) ||
		created.Member == nil || !*created.Member || !member(t, admin, created.ID, user.Id) {
		t.Fatalf("created %+v", created)
	}
	mustContain(t, "question", questions.only(t).Message, "private channel ~"+display, team.DisplayName, "plan the release")
}

func TestMarkChannelReadClearsWhatIsUnreadWithoutAsking(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	_, _, err := clientAs(t, other).CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	postAs(t, clientAs(t, other), channel.Id, "", "@"+user.Username+" news")
	// A client that cannot be asked: marking read is the person's own.
	session := mcpWriting(t, personalAccessToken(t, admin, user.Id).Token, nil)
	if unread := getUserChannels(t, session, map[string]any{"team_id": team.Id})[channel.Id]; count(unread.Unread) == 0 {
		t.Fatalf("nothing unread before: %+v", unread)
	}

	var read server.Read
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "mark_channel_read", Arguments: map[string]any{"channel_id": "#" + channel.DisplayName}}), &read)

	after := getUserChannels(t, session, map[string]any{"team_id": team.Id})[channel.Id]
	if !read.Read || count(after.Unread) != 0 || count(after.Mentions) != 0 {
		t.Fatalf("after marking it read: %+v", after)
	}
}

func TestSetStatusSetsPresenceAndAMessageEveryoneSees(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	session, questions := writingSession(t, admin, user, accept)
	until := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)

	var set server.Status
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "set_status", Arguments: map[string]any{
		"status": "dnd", "dnd_until": until.Format(time.RFC3339),
		"text": "In a meeting", "emoji": ":calendar:", "text_until": until.Format(time.RFC3339),
	}}), &set)

	status, _, err := admin.GetUserStatus(t.Context(), user.Id, "")
	check(t, err)
	if status.Status != model.StatusDnd || status.DNDEndTime != until.Truncate(time.Minute).Unix() || set.DNDUntil != until.Truncate(time.Minute).Format(time.RFC3339) {
		t.Errorf("Mattermost has the status %s until %d, the tool says until %s; want dnd until %s", status.Status, status.DNDEndTime, set.DNDUntil, until.Truncate(time.Minute))
	}
	read, _, err := admin.GetUser(t.Context(), user.Id, "")
	check(t, err)
	if custom := read.GetCustomStatus(); custom == nil || custom.Text != "In a meeting" || custom.Emoji != "calendar" {
		t.Errorf("the status message is %+v", custom)
	}
	mustContain(t, "question", questions.only(t).Message, "Do not disturb", ":calendar: “In a meeting”")

	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "set_status", Arguments: map[string]any{"status": "online", "clear_text": true}}), &set)
	read, _, err = admin.GetUser(t.Context(), user.Id, "")
	check(t, err)
	if custom := read.GetCustomStatus(); custom != nil && custom.Text != "" {
		t.Errorf("the status message is still %+v", custom)
	}
	if refused := callTool(t, session, &mcp.CallToolParams{Name: "set_status", Arguments: map[string]any{"status": "busy"}}); !refused.IsError {
		t.Error("a status Mattermost has no name for was taken")
	}
}

// refused calls a tool expecting it to be refused with what says.
func refused(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any, says string) {
	t.Helper()
	result := callTool(t, session, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if !result.IsError || !strings.Contains(errorText(result), says) {
		t.Errorf("%s %v: got %q; want it refused saying %q", name, arguments, errorText(result), says)
	}
}

func TestTheChannelAndStatusToolsRefuseWhatTheyCannotDoBeforeAsking(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, other)
	archived := seedChannel(t, admin, team, user, other)
	_, err := admin.DeleteChannel(t.Context(), archived.Id)
	check(t, err)
	direct, _, err := clientAs(t, other).CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)
	soon := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	many := make([]string, 21)
	for i := range many {
		many[i] = other.Username
	}

	refused(t, session, "join_channel", map[string]any{"channel_id": direct.Id}, "belongs to no team")
	refused(t, session, "join_channel", map[string]any{"channel_id": archived.Id}, "archived")
	refused(t, session, "leave_channel", map[string]any{"channel_id": channel.Id}, "does not belong")
	refused(t, session, "add_channel_members", map[string]any{"channel_id": channel.Id, "usernames": []string{}}, "give the usernames")
	refused(t, session, "add_channel_members", map[string]any{"channel_id": channel.Id, "usernames": many}, "at most 20")
	refused(t, session, "create_channel", map[string]any{"team_id": team.Id, "display_name": " "}, "give the channel a name")
	refused(t, session, "set_status", map[string]any{}, "give a status")
	refused(t, session, "set_status", map[string]any{"status": "online", "dnd_until": soon}, "goes with status dnd")
	refused(t, session, "set_status", map[string]any{"clear_text": true, "text": "x"}, "without text")
	refused(t, session, "set_status", map[string]any{"text_until": soon}, "give text or emoji")
	refused(t, session, "set_status", map[string]any{"status": "dnd", "dnd_until": "next week"}, "must be a time")
	refused(t, session, "set_status", map[string]any{"status": "dnd", "dnd_until": "2020-01-01T09:00"}, "not in the future")
	refused(t, session, "set_status", map[string]any{"text": "lunch", "emoji": "\U0001F9FF\U0001F9FF"}, "not an emoji")
	if n := questions.count(); n != 0 {
		t.Errorf("asked %d questions about calls refused before asking", n)
	}
}
