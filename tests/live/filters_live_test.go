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

// read_channel from a time, the filters search_posts and search_files take,
// a PDF through read_file, and set_post_reminder (#5, #7, #8).

func TestReadChannelSinceATimeReadsWhatWasWrittenFromThenOn(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	author := clientAs(t, user)
	old := postAs(t, author, channel.Id, "", "before")
	since := time.UnixMilli(old.CreateAt + 1).UTC()
	// Mattermost times posts to the millisecond; the next is written after since.
	time.Sleep(5 * time.Millisecond)
	postAs(t, author, channel.Id, "", "after one")
	postAs(t, author, channel.Id, "", "after two")
	// An edit makes the old post changed since, which is not written since.
	old.Message = "before, edited"
	_, _, err := author.UpdatePost(t.Context(), old.Id, old)
	check(t, err)

	var read server.ChannelPosts
	structured(t, callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{
		"channel_id": channel.Id, "since": since.Format(time.RFC3339Nano),
	}}), &read)
	var got []string
	for _, post := range read.Posts {
		got = append(got, post.Message)
	}
	if strings.Join(got, "|") != "after one|after two" || read.NextCursor != "" {
		t.Fatalf("read %v (next cursor %q); want the two posts written since", got, read.NextCursor)
	}
}

func TestSearchPostsByWhoWroteThemWhereAndWhen(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	here, there := seedChannel(t, admin, team, user, other), seedChannel(t, admin, team, user, other)
	term := word(t)
	postAs(t, clientAs(t, other), here.Id, "", term+" from them here")
	postAs(t, clientAs(t, user), here.Id, "", term+" from me here")
	postAs(t, clientAs(t, other), there.Id, "", term+" from them there")
	session := sessionFor(t, admin, user)
	search := func(arguments map[string]any) []string {
		var found server.SearchResults
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_posts", Arguments: arguments}), &found)
		var messages []string
		for _, post := range found.Posts {
			messages = append(messages, post.Message)
		}
		return messages
	}

	eventually(t, 30*time.Second, func() (bool, string) {
		all := search(map[string]any{"terms": term})
		return len(all) == 3, fmt.Sprint(all)
	})
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	if got := search(map[string]any{"terms": term, "from": "@" + other.Username, "in": here.Id, "after": yesterday}); len(got) != 1 || got[0] != term+" from them here" {
		t.Errorf("from, in by id and after found %v", got)
	}
	if got := search(map[string]any{"terms": term, "in": "~" + there.Name}); len(got) != 1 || got[0] != term+" from them there" {
		t.Errorf("in by name found %v", got)
	}
	if got := search(map[string]any{"terms": term, "before": yesterday}); len(got) != 0 {
		t.Errorf("before yesterday found %v", got)
	}
	if result := callTool(t, session, &mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"after": "last tuesday"}}); !result.IsError {
		t.Error("a day not written as YYYY-MM-DD was taken")
	}
}

func TestSearchFilesByChannelAndType(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	here, there := seedChannel(t, admin, team, user), seedChannel(t, admin, team, user)
	name := word(t)
	author := clientAs(t, user)
	_, csvID := attachAs(t, author, here.Id, name+".csv", []byte("a,b\n"))
	attachAs(t, author, here.Id, name+".txt", []byte("a b\n"))
	attachAs(t, author, there.Id, name+".csv", []byte("c,d\n"))
	session := sessionFor(t, admin, user)

	var found server.FileResults
	eventually(t, 30*time.Second, func() (bool, string) {
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{
			"terms": name + " ext:csv", "in": here.Id,
		}}), &found)
		return len(found.Files) == 1, fmt.Sprint(found.Files)
	})
	if found.Files[0].ID != csvID || found.Files[0].Channel != here.DisplayName || found.Files[0].Team != team.DisplayName {
		t.Fatalf("found %+v", found.Files[0])
	}
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{
		"terms": name, "in": here.DisplayName,
	}}), &found)
	if len(found.Files) != 2 {
		t.Fatalf("in by display name found %d files; want the two posted there", len(found.Files))
	}
}

// minimalPDF is a one-page PDF that says hello.
const minimalPDF = "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
	"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"

func TestReadFileReturnsASmallPDFWholeForAClientThatReadsIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	_, id := attachAs(t, clientAs(t, user), channel.Id, "brief.pdf", []byte(minimalPDF))

	result, read := readFile(t, sessionFor(t, admin, user), map[string]any{"file_id": id})
	if read.DocumentReturned == nil || !*read.DocumentReturned {
		t.Fatalf("read %+v", read)
	}
	for _, content := range result.Content {
		if resource, ok := content.(*mcp.EmbeddedResource); ok && resource.Resource.MIMEType == "application/pdf" && string(resource.Resource.Blob) == minimalPDF {
			return
		}
	}
	t.Fatal("the PDF did not come back whole as an embedded resource")
}

func TestSetPostReminderReadsATimeInThePersonsTimezone(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	post := postAs(t, clientAs(t, user), channel.Id, "", "follow up on the invoice")
	_, _, err := admin.PatchUser(t.Context(), user.Id, &model.UserPatch{Timezone: model.StringMap{
		"useAutomaticTimezone": "false", "manualTimezone": "Asia/Tokyo",
	}})
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	check(t, err)
	tomorrow := time.Now().In(tokyo).AddDate(0, 0, 1).Format(time.DateOnly)

	var reminder server.Reminder
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "set_post_reminder", Arguments: map[string]any{
		"post_id": post.Id, "at": tomorrow + "T09:00",
	}}), &reminder)
	if reminder.RemindAt != tomorrow+"T09:00:00+09:00" || reminder.Timezone != "Asia/Tokyo" {
		t.Fatalf("got %+v; want 09:00 tomorrow in Tokyo", reminder)
	}
	if result := callTool(t, session, &mcp.CallToolParams{Name: "set_post_reminder", Arguments: map[string]any{
		"post_id": post.Id, "at": "2020-01-01T09:00:00Z",
	}}); !result.IsError {
		t.Error("a reminder in the past was set")
	}
	noQuestions(t, questions)
}
