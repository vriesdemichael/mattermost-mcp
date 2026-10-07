//go:build live

package live

import (
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// edit_post, delete_post, remove_reaction and pin_post: changes others see,
// each asked first (ADR-021), read back through the test's own client.

func TestEditPostReplacesTheUsersOwnTextShowingTheOldAndTheNew(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	original := postAs(t, clientAs(t, user), channel.Id, "", "Release at 3pm")
	session, questions := writingSession(t, admin, user, accept)

	var edited server.Post
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "update_post", Arguments: map[string]any{
		"post_id": original.Id, "message": "Release at 4pm",
	}}), &edited)

	question := questions.only(t)
	mustContain(t, "question", question.Message, "Release at 3pm", "Release at 4pm", "~"+channel.DisplayName)
	stored, _, err := admin.GetPost(t.Context(), original.Id, "")
	check(t, err)
	if stored.Message != "Release at 4pm" || stored.EditAt == 0 || edited.EditedAt == "" {
		t.Fatalf("the post reads %q, edited at %d; the tool answered %+v", stored.Message, stored.EditAt, edited)
	}
}

// An administrator's credential could change a colleague's words under the
// colleague's name; the tools refuse before asking.
func TestEditAndDeleteRefuseAnotherPersonsPostEvenForAnAdministrator(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	other := seedUser(t, admin)
	team := seedTeam(t, admin, other)
	channel := seedChannel(t, admin, team, other)
	theirs := postAs(t, clientAs(t, other), channel.Id, "", "my words")
	questions := &asked{}
	session := mcpWriting(t, admin.AuthToken, questions.answer(accept))

	for _, params := range []*mcp.CallToolParams{
		{Name: "update_post", Arguments: map[string]any{"post_id": theirs.Id, "message": "not my words"}},
		{Name: "delete_post", Arguments: map[string]any{"post_id": theirs.Id}},
	} {
		if result := callTool(t, session, params); !result.IsError {
			t.Errorf("%s changed another person's post", params.Name)
		}
	}
	if len(questions.questions) != 0 {
		t.Error("the person was asked about changing another person's post")
	}
	stored, _, err := admin.GetPost(t.Context(), theirs.Id, "")
	check(t, err)
	if stored.Message != "my words" {
		t.Fatalf("the post reads %q", stored.Message)
	}
}

func TestDeletePostSaysHowManyRepliesGoWithIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	root := postAs(t, clientAs(t, user), channel.Id, "", "Lunch order?")
	replier := clientAs(t, other)
	postAs(t, replier, channel.Id, root.Id, "pizza")
	postAs(t, replier, channel.Id, root.Id, "sushi")
	session, questions := writingSession(t, admin, user, accept)

	var deleted server.Deleted
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "delete_post", Arguments: map[string]any{"post_id": root.Id}}), &deleted)

	mustContain(t, "question", questions.only(t).Message, "Lunch order?", "2 replies")
	if deleted.Replies != 2 {
		t.Errorf("the tool says %d replies went; want 2", deleted.Replies)
	}
	_, response, err := admin.GetPost(t.Context(), root.Id, "")
	if err == nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("the post is still there: %v", err)
	}
	if left := messagesIn(t, admin, channel.Id); len(left) != 0 {
		t.Fatalf("the channel still holds %d posts", len(left))
	}
}

func TestRemoveReactionTakesBackOnlyTheUsersOwn(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, other), channel.Id, "", "Merged!")
	for _, who := range []*model.User{user, other} {
		_, _, err := clientAs(t, who).SaveReaction(t.Context(), &model.Reaction{UserId: who.Id, PostId: post.Id, EmojiName: "tada"})
		check(t, err)
	}
	session, questions := writingSession(t, admin, user, accept)

	if result := callTool(t, session, &mcp.CallToolParams{Name: "remove_reaction", Arguments: map[string]any{
		"post_id": post.Id, "emoji": "eyes",
	}}); !result.IsError {
		t.Error("took back a reaction the user never made")
	}
	callTool(t, session, &mcp.CallToolParams{Name: "remove_reaction", Arguments: map[string]any{"post_id": post.Id, "emoji": ":tada:"}})

	mustContain(t, "question", questions.only(t).Message, ":tada:", "@"+other.Username, "Merged!")
	reactions, _, err := admin.GetReactions(t.Context(), post.Id)
	check(t, err)
	if len(reactions) != 1 || reactions[0].UserId != other.Id {
		t.Fatalf("the post has %d reactions; want only the other person's", len(reactions))
	}
}

func TestPinPostPinsAndUnpinsWhatListPinnedReads(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, other), channel.Id, "", "The wiki is at /docs")
	session, questions := writingSession(t, admin, user, accept)
	pinned := func() []server.Post {
		var list server.PinnedPosts
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_pinned_posts", Arguments: map[string]any{"channel_id": channel.Id}}), &list)
		return list.Posts
	}

	callTool(t, session, &mcp.CallToolParams{Name: "pin_post", Arguments: map[string]any{"post_id": post.Id}})
	mustContain(t, "question", questions.only(t).Message, "Pin @"+other.Username, "The wiki is at /docs")
	if got := pinned(); len(got) != 1 || got[0].ID != post.Id || !got[0].Pinned {
		t.Fatalf("list_pinned gives %+v after pinning", got)
	}

	callTool(t, session, &mcp.CallToolParams{Name: "pin_post", Arguments: map[string]any{"post_id": post.Id, "pinned": false}})
	if got := pinned(); len(got) != 0 {
		t.Fatalf("list_pinned gives %d posts after unpinning", len(got))
	}
}
