//go:build live

package live

import (
	"fmt"

	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// What a write acts on is what the person was asked about (ADR-021): a change
// made while they are being asked stops the write. And the behaviour the
// review of the tools set right, each read back live.

// answeringAfter is a client that answers every question with answer, after
// doing meanwhile, as a colleague might act while the person reads it.
func answeringAfter(t *testing.T, admin *model.Client4, user *model.User, meanwhile func(), answer *mcp.ElicitResult) *mcp.ClientSession {
	t.Helper()
	return mcpWriting(t, personalAccessToken(t, admin, user.Id).Token, func(*mcp.ElicitParams) *mcp.ElicitResult {
		meanwhile()
		return answer
	})
}

func TestDeletePostStopsWhenAReplyArrivesWhileThePersonIsAsked(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, user), channel.Id, "", "Anyone up for lunch?")
	replier := clientAs(t, other)
	session := answeringAfter(t, admin, user, func() { postAs(t, replier, channel.Id, post.Id, "me!") }, accept)

	result, err := tryTool(t, session, &mcp.CallToolParams{Name: "delete_post", Arguments: map[string]any{"post_id": post.Id}})
	if err == nil && !result.IsError {
		t.Fatal("the post was deleted with a reply the person was never shown")
	}
	if _, _, err := admin.GetPost(t.Context(), post.Id, ""); err != nil {
		t.Fatalf("the post is gone: %v", err)
	}
}

func TestUpdatePostStopsWhenThePostChangesWhileThePersonIsAsked(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	author := clientAs(t, user)
	post := postAs(t, author, channel.Id, "", "first version")
	session := answeringAfter(t, admin, user, func() {
		post.Message = "rewritten by hand meanwhile"
		_, _, err := author.UpdatePost(t.Context(), post.Id, post)
		if err != nil {
			t.Error(err)
		}
	}, accept)

	result, err := tryTool(t, session, &mcp.CallToolParams{Name: "update_post", Arguments: map[string]any{"post_id": post.Id, "message": "the model's version"}})
	if err == nil && !result.IsError {
		t.Fatal("an edit replaced text the person was never shown")
	}
	stored, _, err := admin.GetPost(t.Context(), post.Id, "")
	check(t, err)
	if stored.Message != "rewritten by hand meanwhile" {
		t.Fatalf("the post reads %q", stored.Message)
	}
}

func TestDeleteDraftStopsWhenTheDraftChangesWhileThePersonIsAsked(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	writer, _ := writingSession(t, admin, user, accept)
	saveDraft(t, writer, map[string]any{"channel_id": channel.Id, "message": "half a thought"})
	own := clientAs(t, user)
	questions := &asked{}
	session := mcpWriting(t, personalAccessToken(t, admin, user.Id).Token, func(question *mcp.ElicitParams) *mcp.ElicitResult {
		questions.answer(accept)(question)
		_, _, err := own.UpsertDraft(t.Context(), &model.Draft{UserId: user.Id, ChannelId: channel.Id, Message: "half a thought, and the rest typed meanwhile"})
		if err != nil {
			t.Error(err)
		}
		return accept
	})

	result, err := tryTool(t, session, &mcp.CallToolParams{Name: "delete_draft", Arguments: map[string]any{"channel_id": channel.Id}})
	if err == nil && !result.IsError {
		t.Fatal("a draft was deleted with words the person was never shown")
	}
	mustContain(t, "question", questions.only(t).Message, channel.DisplayName, team.DisplayName, "half a thought")
	if got := draftsOf(t, user, team.Id)[channel.Id+"/"]; got != "half a thought, and the rest typed meanwhile" {
		t.Fatalf("the draft reads %q", got)
	}
}

func TestSaveDraftLeavesADraftWithFilesAsItIs(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	own := clientAs(t, user)
	uploaded, _, err := own.UploadFile(t.Context(), []byte("x"), channel.Id, "plan.txt")
	check(t, err)
	_, _, err = own.UpsertDraft(t.Context(), &model.Draft{UserId: user.Id, ChannelId: channel.Id, Message: "see plan", FileIds: []string{uploaded.FileInfos[0].Id}})
	check(t, err)
	session, _ := writingSession(t, admin, user, accept)

	saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "see plan"})
	if result := saveDraft(t, session, map[string]any{"channel_id": channel.Id, "message": "something else"}); !result.IsError || !strings.Contains(errorText(result), "files") {
		t.Errorf("a different draft over one with files: %s", errorText(result))
	}
	drafts, _, err := own.GetDrafts(t.Context(), user.Id, team.Id)
	check(t, err)
	if len(drafts) != 1 || len(drafts[0].FileIds) != 1 {
		t.Fatalf("the draft is %+v; want it with its file", drafts)
	}
}

func TestGroupMessageCountsEachPersonOnceAndDMTakesAnyCase(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, first, second := seedUser(t, admin), seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, first, second)
	session, _ := writingSession(t, admin, user, accept)

	// One person named twice is one person: with only one other, it is not a group.
	if result := callTool(t, session, &mcp.CallToolParams{Name: "group_message", Arguments: map[string]any{
		"usernames": []string{first.Username, first.Email}, "message": "x",
	}}); !result.IsError {
		t.Error("a group of the user and one person named twice was sent")
	}
	var posted server.Post
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "group_message", Arguments: map[string]any{
		"usernames": []string{first.Username, first.Email, strings.ToUpper(second.Username)}, "message": "hello both",
	}}), &posted)
	group, _, err := clientAs(t, first).CreateGroupChannel(t.Context(), []string{user.Id, first.Id, second.Id})
	check(t, err)
	if posted.ChannelID != group.Id {
		t.Fatalf("posted in %s; want the group of three, %s", posted.ChannelID, group.Id)
	}
	for _, name := range []string{strings.ToUpper(first.Username), second.Email} {
		if result := callTool(t, session, &mcp.CallToolParams{Name: "dm", Arguments: map[string]any{"username": name, "message": "hi"}}); result.IsError {
			t.Errorf("a dm to %s was refused: %s", name, errorText(result))
		}
	}
}

func TestAMentionEndingASentenceIsAMentionOfEveryone(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	session, questions := writingSession(t, admin, user, accept)

	createPost(t, session, map[string]any{"channel_id": channel.Id, "message": "Deploy is done, thanks @here. Code says `@nobody`; and @" + other.Username + "."})
	question := questions.only(t).Message
	mustContain(t, "question", question, "@here notifies everyone in the channel who is online")
	// The question puts what the post does before the message itself.
	if strings.Index(question, "notifies") > strings.Index(question, "Deploy is done") {
		t.Errorf("the question shows the message before what it does:\n%s", question)
	}
}

func TestRemoveReactionTakesBackAReactionMadeUnderAnotherNameOfTheEmoji(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	post := postAs(t, clientAs(t, user), channel.Id, "", "nice")
	_, _, err := clientAs(t, user).SaveReaction(t.Context(), &model.Reaction{UserId: user.Id, PostId: post.Id, EmojiName: "thumbsup"})
	check(t, err)
	session, _ := writingSession(t, admin, user, accept)

	var removed server.Reaction
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "remove_reaction", Arguments: map[string]any{"post_id": post.Id, "emoji": "\U0001F44D"}}), &removed)
	reactions, _, err := admin.GetReactions(t.Context(), post.Id)
	check(t, err)
	if removed.EmojiName != "thumbsup" || len(reactions) != 0 {
		t.Fatalf("took back %q; %d reactions left", removed.EmojiName, len(reactions))
	}
}

func TestGetTeamInfoLeavesAnAmbiguousNameAmbiguous(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	tag := word(t)
	for _, suffix := range []string{"ops", "ers"} {
		team, _, err := admin.CreateTeam(t.Context(), &model.Team{Name: uniqueName("team"), DisplayName: tag + suffix, Type: model.TeamOpen})
		check(t, err)
		t.Cleanup(func() { _, _ = admin.SoftDeleteTeam(t.Context(), team.Id) })
		_, _, err = admin.AddTeamMember(t.Context(), team.Id, user.Id)
		check(t, err)
	}
	outside, _, err := admin.CreateTeam(t.Context(), &model.Team{Name: tag, DisplayName: "Outside", Type: model.TeamOpen, AllowOpenInvite: true})
	check(t, err)
	t.Cleanup(func() { _, _ = admin.SoftDeleteTeam(t.Context(), outside.Id) })

	result := callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "get_team_info", Arguments: map[string]any{"team": tag}})
	if !result.IsError || !strings.Contains(errorText(result), "could mean any of 2") {
		t.Fatalf("an ambiguous name got %s", errorText(result))
	}
}

func TestTypingStopsWhenToldAndWhenAReplyIsDeclined(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	root := postAs(t, clientAs(t, other), channel.Id, "", "Who reviews?")
	reply := postAs(t, clientAs(t, other), channel.Id, root.Id, "anyone?")
	seen := typingSeen(t, other, user.Id)
	session, _ := writingSession(t, admin, user, decline)
	quiet := func(what string) {
		t.Helper()
		// Absence cannot be polled for: wait out two refreshes after draining
		// what was already sent.
		time.Sleep(time.Second)
		for len(seen) > 0 {
			<-seen
		}
		select {
		case <-seen:
			t.Errorf("the user is still shown typing after %s", what)
		case <-time.After(7 * time.Second):
		}
	}

	callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"channel_id": channel.Id}})
	<-seen
	callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"channel_id": channel.Id, "stop": true}})
	quiet("being told to stop")

	// Shown typing a reply named by any post in the thread, then the reply is declined.
	callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"root_id": reply.Id}})
	<-seen
	callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: map[string]any{"root_id": reply.Id, "message": "me"}})
	quiet("declining the reply")
}

func TestReadPostKeepsTheAskedForReplyInItsPlace(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	author := clientAs(t, user)
	root := postAs(t, author, channel.Id, "", "the root")
	want := []string{root.Id}
	for i := range 6 {
		want = append(want, postAs(t, author, channel.Id, root.Id, fmt.Sprintf("reply %d", i)).Id)
	}
	session := sessionFor(t, admin, user)

	for _, asked := range []string{want[2], want[4], want[6]} {
		got := flat(t, everyPage(t, session, &mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": asked}}, 2, "thread", "id"))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("asked for %s, paged through %v; want %v", asked, got, want)
		}
	}
}

func TestReadUnreadOnAChannelNeverOpenedReadsItAllNewestFirst(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	seedTeam(t, admin, user, other)
	author := clientAs(t, other)
	direct, _, err := author.CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	var posted []string
	for i := range 5 {
		posted = append(posted, postAs(t, author, direct.Id, "", fmt.Sprintf("message %d", i)).Id)
	}

	pages := everyPage(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "read_unread", Arguments: map[string]any{"channel_id": direct.Id}}, 2, "posts", "id")
	if got := flat(t, pages); !sameItems(got, posted) || len(pages) != 3 {
		t.Fatalf("paged through %v; want the five messages on three pages", pages)
	}
}

func TestSearchSaysWhenMattermostsSearchStoppedAt100(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	term := word(t)
	author := clientAs(t, user)
	inParallel(t, 101, func(i int) {
		if _, _, err := author.CreatePost(t.Context(), &model.Post{ChannelId: channel.Id, Message: fmt.Sprintf("%s %d", term, i)}); err != nil {
			t.Error(err)
		}
	})
	session := sessionFor(t, admin, user)

	var found server.SearchResults
	eventually(t, 30*time.Second, func() (bool, string) {
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"terms": term, "limit": 100}}), &found)
		return found.Capped, fmt.Sprint(len(found.Posts))
	})
	if len(found.Posts) != 100 || found.NextCursor != "" {
		t.Fatalf("found %d posts, next cursor %q; want the 100 Mattermost gives, said to be capped", len(found.Posts), found.NextCursor)
	}
}

func TestSearchFiltersRefuseWhatMattermostWouldSilentlyFindNothingFor(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	notJoined := seedChannel(t, admin, team, other)
	session := sessionFor(t, admin, user)

	for _, c := range []struct {
		params *mcp.CallToolParams
		says   string
	}{
		{&mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"terms": "x", "from": slipOf(other.Username)}}, other.Username},
		{&mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{"terms": "x", "from": slipOf(other.Username)}}, other.Username},
		{&mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"terms": "x", "in": notJoined.Id}}, "does not belong to"},
		{&mcp.CallToolParams{Name: "get_status", Arguments: map[string]any{"usernames": []string{slipOf(other.Username)}}}, other.Username},
	} {
		result := callTool(t, session, c.params)
		if !result.IsError || !strings.Contains(errorText(result), c.says) {
			t.Errorf("%s %v: got %s; want it to say %q", c.params.Name, c.params.Arguments, errorText(result), c.says)
		}
	}
}

// Mattermost answers a read since a time with 1000 changed posts at most; a
// channel with more is read back from the newest instead, seven posts in each
// millisecond, so Mattermost's pages end inside one.
func TestReadChannelSinceReachesPastMattermostsThousand(t *testing.T) {
	t.Parallel()
	const many = 1010
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	since := time.Now().UTC().Add(-time.Minute)
	base := since.UnixMilli() + 1
	inParallel(t, many, func(i int) {
		at := base + int64(i/7)
		if post, _, err := admin.CreatePost(t.Context(), &model.Post{ChannelId: channel.Id, Message: fmt.Sprintf("bulk %d", i), CreateAt: at}); err != nil || post.CreateAt != at {
			t.Errorf("posted at %v: %v", post, err)
		}
	})
	if t.Failed() {
		t.FailNow()
	}

	got := flat(t, everyPage(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{
		"channel_id": channel.Id, "since": since.Format(time.RFC3339Nano),
	}}, 200, "posts", "message"))
	bulk := 0
	for _, message := range got {
		if strings.HasPrefix(message, "bulk ") {
			bulk++
		}
	}
	if bulk != many {
		t.Fatalf("read %d of the %d posts written since", bulk, many)
	}
}
