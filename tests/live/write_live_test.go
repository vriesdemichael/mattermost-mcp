//go:build live

package live

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// create_post and add_reaction, each asking the person first (ADR-021). What
// they wrote, or that they wrote nothing, is read back through a client of the
// test's own, not through the tool.

// asked keeps the questions a writing session was asked.
type asked struct {
	mu        sync.Mutex
	questions []*mcp.ElicitParams
}

func (a *asked) answer(result *mcp.ElicitResult) func(*mcp.ElicitParams) *mcp.ElicitResult {
	return func(question *mcp.ElicitParams) *mcp.ElicitResult {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.questions = append(a.questions, question)
		return result
	}
}

// count is how many questions were asked.
func (a *asked) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.questions)
}

// only is the one question asked, failing the test unless exactly one was.
func (a *asked) only(t *testing.T) *mcp.ElicitParams {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.questions) != 1 {
		t.Fatalf("asked %d questions; want one", len(a.questions))
	}
	return a.questions[0]
}

var (
	accept  = &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}
	decline = &mcp.ElicitResult{Action: "decline"}
	cancel  = &mcp.ElicitResult{Action: "cancel"}
)

// writingSession is a session acting as user, with writes allowed, answering
// every question with answer.
func writingSession(t *testing.T, admin *model.Client4, user *model.User, answer *mcp.ElicitResult) (*mcp.ClientSession, *asked) {
	t.Helper()
	questions := &asked{}
	return mcpWriting(t, personalAccessToken(t, admin, user.Id).Token, questions.answer(answer)), questions
}

func createPost(t *testing.T, session *mcp.ClientSession, arguments map[string]any) server.Post {
	t.Helper()
	var posted server.Post
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: arguments}), &posted)
	return posted
}

// messagesIn is every message in a channel, read by admin.
func messagesIn(t *testing.T, admin *model.Client4, channelID string) []*model.Post {
	t.Helper()
	list, _, err := admin.GetPostsForChannel(t.Context(), channelID, 0, 200, "", false, false)
	check(t, err)
	var posts []*model.Post
	for _, id := range list.Order {
		if post := list.Posts[id]; post.Type == "" {
			posts = append(posts, post)
		}
	}
	return posts
}

func mustContain(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("the %s does not say %q:\n%s", what, want, text)
		}
	}
}

// label is the title of a question's checkbox.
func label(t *testing.T, question *mcp.ElicitParams) string {
	t.Helper()
	field := question.RequestedSchema.(map[string]any)["properties"].(map[string]any)["confirm"].(map[string]any)
	if field["type"] != "boolean" {
		t.Fatalf("the question's field is %v; want a checkbox", field)
	}
	return field["title"].(string)
}

func TestCreatePostPostsWhatThePersonAcceptedUnderTheirName(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	session, questions := writingSession(t, admin, user, accept)
	message := "Deploy is **done**.\nSee you tomorrow."

	posted := createPost(t, session, map[string]any{"channel_id": channel.Id, "message": message})

	question := questions.only(t)
	mustContain(t, "question", question.Message, "@"+user.Username, "~"+channel.DisplayName, message)
	mustContain(t, "checkbox", label(t, question), "~"+channel.DisplayName)
	if posted.Message != message || posted.Author != user.Username || posted.ChannelID != channel.Id || posted.RootID != "" {
		t.Errorf("the tool answered %+v", posted)
	}
	stored := messagesIn(t, admin, channel.Id)
	if len(stored) != 1 || stored[0].Id != posted.ID || stored[0].Message != message || stored[0].UserId != user.Id {
		t.Fatalf("the channel holds %v; want the one message, by %s", stored, user.Username)
	}
}

func TestCreatePostRepliesInTheThreadOfAnyPostInIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author := clientAs(t, other)
	root := postAs(t, author, channel.Id, "", "Who can review the release notes?")
	reply := postAs(t, author, channel.Id, root.Id, "Anyone?")
	session, questions := writingSession(t, admin, user, accept)

	posted := createPost(t, session, map[string]any{"root_id": reply.Id, "message": "I can."})

	mustContain(t, "question", questions.only(t).Message, "@"+user.Username, "@"+other.Username, "Who can review the release notes?", "I can.")
	if posted.RootID != root.Id || posted.ChannelID != channel.Id {
		t.Fatalf("the reply went to root %q in %q; want root %s in %s", posted.RootID, posted.ChannelID, root.Id, channel.Id)
	}
	thread, _, err := admin.GetPostThread(t.Context(), root.Id, "", false)
	check(t, err)
	if _, ok := thread.Posts[posted.ID]; !ok || len(thread.Order) != 3 {
		t.Fatalf("the thread holds %d posts, the reply among them %v", len(thread.Order), ok)
	}
}

func TestCreatePostRefusesAReplyToAPostInAnotherChannel(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	here, there := seedChannel(t, admin, team, user), seedChannel(t, admin, team, user)
	root := postAs(t, clientAs(t, user), there.Id, "", "over there")
	session, questions := writingSession(t, admin, user, accept)

	result := callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: map[string]any{
		"channel_id": here.Id, "root_id": root.Id, "message": "lost",
	}})
	if !result.IsError {
		t.Fatal("a reply named the wrong channel and was posted")
	}
	if questions.count() != 0 || len(messagesIn(t, admin, here.Id)) != 0 {
		t.Fatal("the person was asked, or something was posted")
	}
}

func TestCreatePostInADirectMessageNamesThePersonOnTheOtherSide(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	direct, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)

	createPost(t, session, map[string]any{"channel_id": direct.Id, "message": "lunch?"})

	question := questions.only(t)
	mustContain(t, "question", question.Message, "your direct message with @"+other.Username)
	mustContain(t, "checkbox", label(t, question), "@"+other.Username)
}

// A person who declines, closes the question, or cannot be asked, is left
// with Mattermost as it was.
func TestAWriteThePersonDidNotAcceptChangesNothing(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]*mcp.ElicitResult{"declined": decline, "cancelled": cancel, "not asked": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			admin := admin(t)
			user := seedUser(t, admin)
			team := seedTeam(t, admin, user)
			channel := seedChannel(t, admin, team, user)
			target := postAs(t, clientAs(t, user), channel.Id, "", "react to me")
			token := personalAccessToken(t, admin, user.Id).Token
			var session *mcp.ClientSession
			questions := &asked{}
			if answer == nil {
				session = mcpWriting(t, token, nil)
			} else {
				session = mcpWriting(t, token, questions.answer(answer))
			}

			for _, params := range []*mcp.CallToolParams{
				{Name: "create_post", Arguments: map[string]any{"channel_id": channel.Id, "message": "unwanted"}},
				{Name: "add_reaction", Arguments: map[string]any{"post_id": target.Id, "emoji": "thumbsup"}},
			} {
				result, err := tryTool(t, session, params)
				var refused *jsonrpc.Error
				switch {
				case answer == nil && (!errors.As(err, &refused) || refused.Code != mcp.CodeMissingRequiredClientCapabilities):
					t.Errorf("%s from a client that cannot be asked: %v; want the missing capability error", params.Name, err)
				case answer != nil && (err != nil || !result.IsError):
					t.Errorf("%s %s: %v, %+v; want a tool error", params.Name, name, err, result)
				}
			}

			// Each refusal came from the person's answer, after one question each.
			if answer != nil && questions.count() != 2 {
				t.Errorf("asked %d questions; want one for each of the two tools", questions.count())
			}
			if stored := messagesIn(t, admin, channel.Id); len(stored) != 1 {
				t.Errorf("the channel holds %d messages; want only the one the test posted", len(stored))
			}
			reactions, _, err := admin.GetReactions(context.Background(), target.Id)
			check(t, err)
			if len(reactions) != 0 {
				t.Errorf("the post has %d reactions; want none", len(reactions))
			}
		})
	}
}

// A server configured with MM_MCP_ASK_BEFORE_WRITES=false asks nothing, so a
// client that cannot be asked, or would say no, writes all the same: the
// client's own approval is the check (ADR-033).
func TestAServerThatDoesNotAskWritesWithoutAQuestion(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]*mcp.ElicitResult{"cannot be asked": nil, "would decline": decline} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			admin := admin(t)
			user := seedUser(t, admin)
			team := seedTeam(t, admin, user)
			channel := seedChannel(t, admin, team, user)
			target := postAs(t, clientAs(t, user), channel.Id, "", "react to me")
			questions := &asked{}
			var options *mcp.ClientOptions
			if answer != nil {
				options = &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
					return questions.answer(answer)(request.Params), nil
				}}
			}
			session := mcpWith(t, config.Config{
				URL: liveURL, Token: personalAccessToken(t, admin, user.Id).Token, AllowWrites: true, SkipAsking: true,
			}, options)

			var posted server.Post
			structured(t, callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: map[string]any{
				"channel_id": channel.Id, "message": "first draft",
			}}), &posted)
			callTool(t, session, &mcp.CallToolParams{Name: "update_post", Arguments: map[string]any{
				"post_id": posted.ID, "message": "posted unasked",
			}})
			callTool(t, session, &mcp.CallToolParams{Name: "add_reaction", Arguments: map[string]any{
				"post_id": target.Id, "emoji": "thumbsup",
			}})

			if questions.count() != 0 {
				t.Fatalf("asked %d questions; want none", questions.count())
			}
			stored, _, err := admin.GetPost(context.Background(), posted.ID, "")
			check(t, err)
			if stored.Message != "posted unasked" || stored.UserId != user.Id {
				t.Errorf("the post reads %q by %s", stored.Message, stored.UserId)
			}
			reactions, _, err := admin.GetReactions(context.Background(), target.Id)
			check(t, err)
			if len(reactions) != 1 || reactions[0].UserId != user.Id {
				t.Errorf("the post has reactions %+v; want the user's one", reactions)
			}
		})
	}
}

func TestAddReactionReactsAsThePersonAndAgainChangesNothing(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	target := postAs(t, clientAs(t, other), channel.Id, "", "Shipped the fix for the login loop")
	session, questions := writingSession(t, admin, user, accept)
	react := func() {
		var reaction server.Reaction
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "add_reaction", Arguments: map[string]any{
			"post_id": target.Id, "emoji": ":thumbsup:",
		}}), &reaction)
		if reaction.PostID != target.Id || reaction.EmojiName != "thumbsup" {
			t.Fatalf("the tool answered %+v", reaction)
		}
	}

	react()
	question := questions.only(t)
	mustContain(t, "question", question.Message, "@"+user.Username, ":thumbsup:", "@"+other.Username, "~"+channel.DisplayName, "Shipped the fix")
	mustContain(t, "checkbox", label(t, question), ":thumbsup:")
	react()

	reactions, _, err := admin.GetReactions(t.Context(), target.Id)
	check(t, err)
	if len(reactions) != 1 || reactions[0].UserId != user.Id || reactions[0].EmojiName != "thumbsup" {
		t.Fatalf("the post has %d reactions; want one thumbsup by %s", len(reactions), user.Username)
	}
}
