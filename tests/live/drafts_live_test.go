//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// save_draft, list_drafts and delete_draft: what the model writes into the
// person's own message box, read back as the person (#11).

// draftsOf is the user's drafts in a team, read as the user, by where they are.
func draftsOf(t *testing.T, user *model.User, teamID string) map[string]string {
	t.Helper()
	drafts, _, err := clientAs(t, user).GetDrafts(t.Context(), user.Id, teamID)
	check(t, err)
	byPlace := map[string]string{}
	for _, draft := range drafts {
		byPlace[draft.ChannelId+"/"+draft.RootId] = draft.Message
	}
	return byPlace
}

func saveDraft(t *testing.T, session *mcp.ClientSession, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	return callTool(t, session, &mcp.CallToolParams{Name: "save_draft", Arguments: arguments})
}

func TestSaveDraftLeavesDraftsWhereTheyGoAndNeverReplacesAnother(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	root := postAs(t, clientAs(t, other), channel.Id, "", "Who takes the on-call shift?")
	direct, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)

	var drafted server.Draft
	structured(t, saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "@here status: all green, thanks @" + other.Username}), &drafted)
	if drafted.Channel != channel.DisplayName || drafted.Team != team.DisplayName || len(drafted.Notes) != 1 || !strings.Contains(drafted.Notes[0], "@here") {
		t.Errorf("the channel's draft reads %+v", drafted)
	}
	structured(t, saveDraft(t, session, map[string]any{"root_id": root.Id, "message": "I can take it"}), &drafted)
	if drafted.RootID != root.Id {
		t.Fatalf("the reply was drafted %+v", drafted)
	}
	structured(t, saveDraft(t, session, map[string]any{"channel_id": direct.Id, "message": "see you at standup"}), &drafted)
	if drafted.Channel != other.Username {
		t.Errorf("the direct message's draft is named %q", drafted.Channel)
	}
	if result := saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "something else"}); !result.IsError ||
		!strings.Contains(errorText(result), "status: all green") {
		t.Errorf("a different draft got %s; want it refused, quoting the one there", errorText(result))
	}
	if result := saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "hi @" + slipOf(other.Username)}); !result.IsError {
		t.Error("a draft mentioning someone nobody is was saved")
	}

	byPlace := draftsOf(t, user, team.Id)
	switch {
	case !strings.HasPrefix(byPlace[channel.Id+"/"], "@here status"):
		t.Errorf("the channel's draft reads %q", byPlace[channel.Id+"/"])
	case byPlace[channel.Id+"/"+root.Id] != "I can take it":
		t.Errorf("the thread's draft reads %q", byPlace[channel.Id+"/"+root.Id])
	case byPlace[direct.Id+"/"] != "see you at standup":
		t.Errorf("the direct message's draft reads %q", byPlace[direct.Id+"/"])
	}
	if posts := messagesIn(t, admin, channel.Id); len(posts) != 1 {
		t.Errorf("drafting posted something: the channel holds %d posts", len(posts))
	}
	noQuestions(t, questions)
}

func TestListDraftsListsEachOnceWithWhereItIs(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	direct, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)
	session, _ := writingSession(t, admin, user, accept)
	saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "in the channel"})
	saveDraft(t, session, map[string]any{"channel_id": direct.Id, "message": "in the direct message"})

	var listed server.Drafts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_drafts", Arguments: map[string]any{}}), &listed)
	byMessage := map[string]server.Draft{}
	for _, draft := range listed.Drafts {
		if _, twice := byMessage[draft.Message]; twice {
			t.Errorf("%q is listed twice", draft.Message)
		}
		byMessage[draft.Message] = draft
	}
	if byMessage["in the channel"].Channel != channel.DisplayName || byMessage["in the direct message"].Channel != other.Username {
		t.Fatalf("listed %+v", listed.Drafts)
	}
}

func TestDeleteDraftAsksAndDeletesOnlyThatDraft(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	root := postAs(t, clientAs(t, other), channel.Id, "", "Retro notes?")
	direct, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)
	writer, _ := writingSession(t, admin, user, accept)
	saveDraft(t, writer, map[string]any{"channel_id": channel.Id, "message": "the channel's draft"})
	saveDraft(t, writer, map[string]any{"root_id": root.Id, "message": "the thread's draft"})
	saveDraft(t, writer, map[string]any{"channel_id": direct.Id, "message": "the direct draft"})

	declining, _ := writingSession(t, admin, user, decline)
	if result := callTool(t, declining, &mcp.CallToolParams{Name: "delete_draft", Arguments: map[string]any{"root_id": root.Id}}); !result.IsError {
		t.Fatal("a declined deletion ran")
	}
	deleter, questions := writingSession(t, admin, user, accept)
	callTool(t, deleter, &mcp.CallToolParams{Name: "delete_draft", Arguments: map[string]any{"root_id": root.Id}})
	mustContain(t, "question", questions.only(t).Message, "the thread's draft")
	callTool(t, deleter, &mcp.CallToolParams{Name: "delete_draft", Arguments: map[string]any{"channel_id": direct.Id}})

	byPlace := draftsOf(t, user, team.Id)
	switch {
	case byPlace[channel.Id+"/"+root.Id] != "":
		t.Error("the thread's draft is still there")
	case byPlace[channel.Id+"/"] != "the channel's draft":
		t.Errorf("deleting the thread's draft took the channel's too: it reads %q", byPlace[channel.Id+"/"])
	case byPlace[direct.Id+"/"] != "":
		t.Error("the direct message's draft is still there")
	}
	if result := callTool(t, deleter, &mcp.CallToolParams{Name: "delete_draft", Arguments: map[string]any{"root_id": root.Id}}); !result.IsError {
		t.Error("deleting a draft that is not there succeeded")
	}
}

func TestSaveDraftRefusesWhenThePersonTurnedOffSyncingDrafts(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	_, err := clientAs(t, user).UpdatePreferences(t.Context(), user.Id, model.Preferences{{
		UserId: user.Id, Category: model.PreferenceCategoryAdvancedSettings, Name: "sync_drafts", Value: "false",
	}})
	check(t, err)
	session, _ := writingSession(t, admin, user, accept)

	result := saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "unseen"})
	if !result.IsError || !strings.Contains(errorText(result), "turned off syncing") {
		t.Fatalf("got %s", errorText(result))
	}
}
