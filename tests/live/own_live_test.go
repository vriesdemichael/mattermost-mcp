//go:build live

package live

import (
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// typing, follow_thread, save_post, set_post_reminder and save_draft: changes only the user
// sees, or that last seconds, which do not ask (ADR-021).

// noQuestions fails the test when a tool that does not ask asked.
func noQuestions(t *testing.T, questions *asked) {
	t.Helper()
	if questions.count() != 0 {
		t.Errorf("asked %d questions; the tool does not ask", questions.count())
	}
}

// typingSeen is how a colleague's client hears someone typing: the typing
// events Mattermost sends over the websocket, here for one user.
func typingSeen(t *testing.T, watcher *model.User, typist string) <-chan *model.WebSocketEvent {
	t.Helper()
	socket, err := model.NewWebSocketClient4(strings.Replace(liveURL, "http", "ws", 1), clientAs(t, watcher).AuthToken)
	check(t, err)
	socket.Listen()
	t.Cleanup(socket.Close)
	seen := make(chan *model.WebSocketEvent, 100)
	go func() {
		for event := range socket.EventChannel {
			if event.EventType() == model.WebsocketEventTyping && event.GetData()["user_id"] == typist {
				seen <- event
			}
		}
	}()
	return seen
}

func TestTypingShowsTheUserTypingUntilTheyPost(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	root := postAs(t, clientAs(t, other), channel.Id, "", "Can someone check the build?")
	seen := typingSeen(t, other, user.Id)
	session, questions := writingSession(t, admin, user, accept)

	var shown server.Typing
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"root_id": root.Id}}), &shown)
	if !shown.Typing || shown.RootID != root.Id || shown.ChannelID != channel.Id || shown.Until == "" {
		t.Fatalf("the tool answered %+v", shown)
	}
	// One event when called, and the next a few seconds on: the indicator is
	// kept up, not sent once.
	for i := range 2 {
		select {
		case event := <-seen:
			if event.GetBroadcast().ChannelId != channel.Id || event.GetData()["parent_id"] != root.Id {
				t.Fatalf("typing shown in %s under %v", event.GetBroadcast().ChannelId, event.GetData()["parent_id"])
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("typing event %d never came", i+1)
		}
	}
	noQuestions(t, questions)

	createPost(t, session, map[string]any{"root_id": root.Id, "message": "Looking now."})
	// Absence cannot be polled for: wait out two refreshes after draining
	// what was already sent.
	for len(seen) > 0 {
		<-seen
	}
	select {
	case <-seen:
		t.Fatal("the user is still shown typing after posting")
	case <-time.After(7 * time.Second):
	}
}

func TestTypingStopsWhenToldWithoutAPost(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	session, _ := writingSession(t, admin, user, accept)

	callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"channel_id": channel.Id}})
	var stopped server.Typing
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"channel_id": channel.Id, "stop": true}}), &stopped)
	if stopped.Typing {
		t.Fatalf("the tool answered %+v after stop", stopped)
	}
}

func listThreads(t *testing.T, session *mcp.ClientSession, arguments map[string]any) map[string]server.ThreadSummary {
	t.Helper()
	var threads server.Threads
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_threads", Arguments: arguments}), &threads)
	byRoot := map[string]server.ThreadSummary{}
	for _, thread := range threads.Threads {
		byRoot[thread.RootID] = thread
	}
	return byRoot
}

func TestFollowThreadPutsItAmongTheThreadsWithItsUnreadReplies(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author := clientAs(t, other)
	root := postAs(t, author, channel.Id, "", "Postmortem notes")
	reply := postAs(t, author, channel.Id, root.Id, "first draft is up")
	session, questions := writingSession(t, admin, user, accept)

	if _, ok := listThreads(t, session, map[string]any{})[root.Id]; ok {
		t.Fatal("a thread the user never touched is listed as followed")
	}
	var followed server.Following
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "follow_thread", Arguments: map[string]any{"post_id": reply.Id}}), &followed)
	if !followed.Following || followed.RootID != root.Id {
		t.Fatalf("the tool answered %+v", followed)
	}
	postAs(t, author, channel.Id, root.Id, "second draft is up")

	thread, ok := listThreads(t, session, map[string]any{"team_id": team.Id, "unread_only": true})[root.Id]
	switch {
	case !ok:
		t.Fatal("the followed thread with a new reply is not among the unread threads")
	case thread.Started.Message != "Postmortem notes" || thread.Started.Author != other.Username || thread.Started.Channel != channel.DisplayName:
		t.Errorf("the thread reads %+v", thread)
	case thread.ReplyCount != 2 || thread.Started.ReplyCount != 2 || thread.UnreadReplies == 0 || len(thread.Participants) == 0:
		t.Errorf("the thread counts %d replies (its first post %d), %d unread, participants %v", thread.ReplyCount, thread.Started.ReplyCount, thread.UnreadReplies, thread.Participants)
	}

	callTool(t, session, &mcp.CallToolParams{Name: "follow_thread", Arguments: map[string]any{"post_id": root.Id, "following": false}})
	if _, ok := listThreads(t, session, map[string]any{})[root.Id]; ok {
		t.Fatal("the thread is still listed after unfollowing it")
	}
	noQuestions(t, questions)
}

// A direct message belongs to no team, and its threads are filed under the
// user's teams all the same.
func TestFollowThreadInADirectMessage(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	author := clientAs(t, other)
	direct, _, err := author.CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	root := postAs(t, author, direct.Id, "", "quick question")
	session, _ := writingSession(t, admin, user, accept)

	callTool(t, session, &mcp.CallToolParams{Name: "follow_thread", Arguments: map[string]any{"post_id": root.Id}})
	postAs(t, author, direct.Id, root.Id, "about the invoice")
	thread, ok := listThreads(t, session, map[string]any{})[root.Id]
	if !ok || thread.Started.Channel != other.Username {
		t.Fatalf("the direct message's thread is listed %v, as %+v", ok, thread)
	}
}

func TestSavePostKeepsItAmongTheSavedPosts(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, other), channel.Id, "", "VPN setup steps: ...")
	session, questions := writingSession(t, admin, user, accept)
	saved := func() []server.Post {
		var list server.SearchResults
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_saved", Arguments: map[string]any{}}), &list)
		return list.Posts
	}

	callTool(t, session, &mcp.CallToolParams{Name: "save_post", Arguments: map[string]any{"post_id": post.Id}})
	if got := saved(); len(got) != 1 || got[0].ID != post.Id || got[0].Channel != channel.DisplayName {
		t.Fatalf("list_saved gives %+v after saving", got)
	}
	callTool(t, session, &mcp.CallToolParams{Name: "save_post", Arguments: map[string]any{"post_id": post.Id, "saved": false}})
	if got := saved(); len(got) != 0 {
		t.Fatalf("list_saved gives %d posts after unsaving", len(got))
	}
	if result := callTool(t, session, &mcp.CallToolParams{Name: "save_post", Arguments: map[string]any{"post_id": model.NewId()}}); !result.IsError {
		t.Error("saved a post that does not exist")
	}
	noQuestions(t, questions)
}
