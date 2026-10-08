//go:build live

package live

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// read_unread, read_channel's collapsed threads and get_status: catching up,
// without marking anything read (ADR-021).

func TestReadUnreadStartsWhereThePersonStoppedReadingAndMarksNothing(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author, reader := clientAs(t, other), clientAs(t, user)
	postAs(t, author, channel.Id, "", "old one")
	postAs(t, author, channel.Id, "", "old two")
	viewed(t, reader, user, channel.Id)
	// Mattermost records a read to the millisecond; the next post must come
	// after it.
	time.Sleep(5 * time.Millisecond)
	first := postAs(t, author, channel.Id, "", "new one")
	postAs(t, author, channel.Id, "", "new two")
	postAs(t, author, channel.Id, "", "new three")
	session := sessionFor(t, admin, user)

	var unread server.UnreadPosts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_unread", Arguments: map[string]any{"channel_id": channel.Id}}), &unread)

	if unread.Unread != 3 || unread.FirstUnreadID != first.Id {
		t.Fatalf("%d unread from %s; want 3 from %s", unread.Unread, unread.FirstUnreadID, first.Id)
	}
	var messages []string
	for _, post := range unread.Posts {
		if post.Type == "" {
			messages = append(messages, post.Message)
		}
	}
	if len(messages) < 4 || messages[len(messages)-1] != "new three" {
		t.Fatalf("read %v; want some read posts, then the three new ones, oldest first", messages)
	}
	left, _, err := reader.GetChannelUnread(t.Context(), channel.Id, user.Id)
	check(t, err)
	if left.MsgCount != 3 {
		t.Fatalf("the channel holds %d unread posts for the person after the tool read it; want the 3 still unread", left.MsgCount)
	}
}

func TestReadChannelCollapsesThreadsToThePostsThatStartThem(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author := clientAs(t, other)
	root := postAs(t, author, channel.Id, "", "Design review")
	postAs(t, author, channel.Id, root.Id, "slides attached")
	postAs(t, author, channel.Id, root.Id, "moved to 2pm")
	postAs(t, author, channel.Id, "", "unrelated")
	session := sessionFor(t, admin, user)

	var read server.ChannelPosts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{
		"channel_id": channel.Id, "collapse_threads": true,
	}}), &read)

	var kept []server.Post
	for _, post := range read.Posts {
		if post.Type == "" {
			kept = append(kept, post)
		}
	}
	if len(kept) != 2 || kept[0].ID != root.Id || kept[0].ReplyCount != 2 || kept[0].LastReplyAt == "" || kept[1].Message != "unrelated" {
		t.Fatalf("read %+v; want the thread's first post with its two replies counted, then the unrelated post", kept)
	}
}

func TestGetStatusSaysWhetherSomeoneIsAroundAndWhatTheySet(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	theirs := clientAs(t, other)
	_, _, err := theirs.UpdateUserStatus(t.Context(), other.Id, &model.Status{UserId: other.Id, Status: model.StatusDnd})
	check(t, err)
	_, _, err = theirs.UpdateUserCustomStatus(t.Context(), other.Id, &model.CustomStatus{Emoji: "calendar", Text: "In a meeting"})
	check(t, err)
	session := sessionFor(t, admin, user)

	var statuses server.Statuses
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_status", Arguments: map[string]any{
		"usernames": []string{"@" + other.Username},
	}}), &statuses)
	if len(statuses.Users) != 1 {
		t.Fatalf("got %+v", statuses)
	}
	got := statuses.Users[0]
	if got.Username != other.Username || got.Status != "dnd" || !got.SetByHand || got.CustomText != "In a meeting" || got.CustomEmoji != "calendar" {
		t.Fatalf("got %+v", got)
	}
	if result := callTool(t, session, &mcp.CallToolParams{Name: "get_status", Arguments: map[string]any{
		"usernames": []string{uniqueName("nobody")},
	}}); !result.IsError {
		t.Error("answered for a username nobody has")
	}
}
