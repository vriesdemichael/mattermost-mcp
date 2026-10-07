//go:build live

package live

import (
	"context"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// dm, group_message, and what every tool that takes a message checks first:
// its length, its mentions, and the AI marker it carries (ADR-030, ADR-031).

// slipOf is a username with its last letter changed: a typo of a real name.
func slipOf(username string) string {
	return username[:len(username)-1] + "q"
}

func sendDM(t *testing.T, session *mcp.ClientSession, arguments map[string]any) server.Post {
	t.Helper()
	var posted server.Post
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "dm", Arguments: arguments}), &posted)
	return posted
}

func TestDMMessagesOnePersonOrTheUserThemselvesWithAFile(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	session, questions := writingSession(t, admin, user, accept)

	posted := sendDM(t, session, map[string]any{
		"username": "@" + other.Username, "message": "are you free at three?",
		"files": []map[string]any{{"name": "agenda.md", "content": "1. Roadmap\n"}},
	})
	question := questions.only(t)
	mustContain(t, "question", question.Message, "your direct message with @"+other.Username, "agenda.md")
	direct, _, err := clientAs(t, other).CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	if posted.ChannelID != direct.Id || posted.Channel != other.Username || len(posted.Files) != 1 {
		t.Fatalf("posted %+v; want the direct message %s with one file", posted, direct.Id)
	}

	toSelf := sendDM(t, session, map[string]any{"message": "note to self"})
	self, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, user.Id)
	check(t, err)
	if toSelf.ChannelID != self.Id || toSelf.Channel != "yourself" {
		t.Fatalf("a dm without a username went to %+v; want the user's own %s", toSelf, self.Id)
	}
}

func TestDMToANameNobodyHasSuggestsTheClosestAndAsksNothing(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	session, questions := writingSession(t, admin, user, accept)

	result := callTool(t, session, &mcp.CallToolParams{Name: "dm", Arguments: map[string]any{"username": slipOf(other.Username), "message": "hi"}})
	if !result.IsError || !strings.Contains(errorText(result), `"`+other.Username+`"`) {
		t.Fatalf("got %s; want the closest username, %s", errorText(result), other.Username)
	}
	noQuestions(t, questions)
}

func TestGroupMessageMessagesThePeopleTogetherAndOnlyAGroup(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, first, second := seedUser(t, admin), seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, first, second)
	session, questions := writingSession(t, admin, user, accept)

	var posted server.Post
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "group_message", Arguments: map[string]any{
		"usernames": []string{first.Username, "@" + second.Username}, "message": "kickoff at ten",
		"files": []map[string]any{{"name": "plan.txt", "content": "step one\n"}},
	}}), &posted)
	mustContain(t, "question", questions.only(t).Message, "@"+first.Username, "@"+second.Username, "plan.txt")
	group, _, err := clientAs(t, first).CreateGroupChannel(t.Context(), []string{user.Id, first.Id, second.Id})
	check(t, err)
	if posted.ChannelID != group.Id || len(posted.Files) != 1 {
		t.Fatalf("posted %+v; want the group message %s with one file", posted, group.Id)
	}

	for _, names := range [][]string{{first.Username}, {first.Username, slipOf(second.Username)}} {
		if result := callTool(t, session, &mcp.CallToolParams{Name: "group_message", Arguments: map[string]any{
			"usernames": names, "message": "x",
		}}); !result.IsError {
			t.Errorf("a group message with %v was sent", names)
		}
	}
}

func TestAMessageMentioningSomeoneNobodyIsIsRefusedWithTheClosestNames(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	session, questions := writingSession(t, admin, user, accept)

	result := callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: map[string]any{
		"channel_id": channel.Id, "message": "thanks @" + slipOf(other.Username) + "!",
	}})
	if !result.IsError || !strings.Contains(errorText(result), `"`+other.Username+`"`) {
		t.Fatalf("got %s; want the closest username, %s", errorText(result), other.Username)
	}
	noQuestions(t, questions)
	if posts := messagesIn(t, admin, channel.Id); len(posts) != 0 {
		t.Fatalf("the channel holds %d posts", len(posts))
	}
}

func TestAMessageToEveryoneSaysHowManyItReachesAndADeactivatedMentionIsNoted(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other, gone := seedUser(t, admin), seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other, gone)
	channel := seedChannel(t, admin, team, user, other, gone)
	_, err := admin.DeleteUser(t.Context(), gone.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)

	postMessage(t, session, map[string]any{
		"channel_id": channel.Id,
		"message":    "@here the build is green, thanks @" + other.Username + " and @" + gone.Username + ". `@nobody` is code.",
	})
	mustContain(t, "question", questions.only(t).Message,
		"@here notifies everyone in the channel who is online", "people in ~"+channel.DisplayName,
		"@"+gone.Username+" is deactivated")
}

func TestAMessageLongerThanTheServerTakesIsRefusedWithItsLength(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	session, questions := writingSession(t, admin, user, accept)

	result := callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: map[string]any{
		"channel_id": channel.Id, "message": strings.Repeat("é", 16384),
	}})
	if !result.IsError || !strings.Contains(errorText(result), "16384 characters") || !strings.Contains(errorText(result), "16383") {
		t.Fatalf("got %s; want the length and the limit", errorText(result))
	}
	noQuestions(t, questions)
}

func TestAPostAndAnEditAreMarkedAsWrittenWithAIKeepingOtherProps(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	session, questions := writingSession(t, admin, user, accept)

	posted := postMessage(t, session, map[string]any{"channel_id": channel.Id, "message": "written with help"})
	mustContain(t, "question", questions.only(t).Message, "marked as written with AI")
	stored, _, err := admin.GetPost(t.Context(), posted.ID, "")
	check(t, err)
	if stored.GetProp("ai_generated_by") != user.Id || stored.GetProp("ai_generated_by_username") != user.Username || !posted.AIGenerated {
		t.Fatalf("the post's props are %v; the tool says ai_generated %v", stored.GetProps(), posted.AIGenerated)
	}

	written := &model.Post{ChannelId: channel.Id, Message: "by hand"}
	written.AddProp("from_integration", "kept")
	own, _, err := clientAs(t, user).CreatePost(t.Context(), written)
	check(t, err)
	// The edit mentions everyone online and a colleague, so its check reads
	// both, as a new post's does.
	callTool(t, session, &mcp.CallToolParams{Name: "update_post", Arguments: map[string]any{
		"post_id": own.Id, "message": "@here edited with help, @" + other.Username,
	}})
	edited, _, err := admin.GetPost(t.Context(), own.Id, "")
	check(t, err)
	if edited.GetProp("ai_generated_by") != user.Id || edited.GetProp("from_integration") != "kept" {
		t.Fatalf("the edited post's props are %v", edited.GetProps())
	}
	if result := callTool(t, session, &mcp.CallToolParams{Name: "update_post", Arguments: map[string]any{
		"post_id": own.Id, "message": "and @" + slipOf(other.Username),
	}}); !result.IsError {
		t.Error("an edit mentioning someone nobody is was accepted")
	}
}

func TestTheAIMarkerCanBeTurnedOff(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	questions := &asked{}
	session := mcpWith(t, config.Config{
		URL: liveURL, Token: personalAccessToken(t, admin, user.Id).Token, AllowWrites: true, MarkAIGenerated: false,
	}, &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return questions.answer(accept)(request.Params), nil
	}})

	posted := postMessage(t, session, map[string]any{"channel_id": channel.Id, "message": "unmarked"})
	if strings.Contains(questions.only(t).Message, "AI") || posted.AIGenerated {
		t.Fatalf("an unmarked post was marked or said to be: %+v", posted)
	}
}

func TestAddReactionTakesTheEmojiItselfAndRefusesAnUnknownNameWithTheClosest(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, other), channel.Id, "", "shipped")
	session, questions := writingSession(t, admin, user, accept)

	var reaction server.Reaction
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "add_reaction", Arguments: map[string]any{"post_id": post.Id, "emoji": "\U0001F44D"}}), &reaction)
	if reaction.EmojiName != "+1" {
		t.Fatalf("the emoji itself was stored as %q", reaction.EmojiName)
	}
	result := callTool(t, session, &mcp.CallToolParams{Name: "add_reaction", Arguments: map[string]any{"post_id": post.Id, "emoji": "thumbsupp"}})
	if !result.IsError || !strings.Contains(errorText(result), `"thumbsup"`) {
		t.Fatalf("an unknown name got %s; want thumbsup suggested", errorText(result))
	}
	questions.only(t)
}
