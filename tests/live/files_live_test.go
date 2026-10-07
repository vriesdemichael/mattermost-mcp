//go:build live

package live

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// read_file, search_files, save_file, and files attached to post_message
// (ADR-029).

// localSession is a session acting as user from a server on the person's own
// machine, over stdio as far as the server knows, with writes allowed and its
// downloads going to a directory of the test's own.
func localSession(t *testing.T, admin *model.Client4, user *model.User, answer func(*mcp.ElicitParams) *mcp.ElicitResult) (*mcp.ClientSession, string) {
	t.Helper()
	downloads := t.TempDir()
	cfg := config.Config{
		URL:         liveURL,
		Token:       personalAccessToken(t, admin, user.Id).Token,
		AllowWrites: true,
		Local:       true,
		DownloadDir: downloads,
	}
	options := &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		return answer(request.Params), nil
	}}
	return mcpWith(t, cfg, options), downloads
}

// attachAs posts a message with one file, as author.
func attachAs(t *testing.T, author *model.Client4, channelID, name string, data []byte) (*model.Post, string) {
	t.Helper()
	uploaded, _, err := author.UploadFile(t.Context(), data, channelID, name)
	check(t, err)
	id := uploaded.FileInfos[0].Id
	post, _, err := author.CreatePost(t.Context(), &model.Post{ChannelId: channelID, Message: "here is " + name, FileIds: []string{id}})
	check(t, err)
	return post, id
}

func readFile(t *testing.T, session *mcp.ClientSession, arguments map[string]any) (*mcp.CallToolResult, server.FileContent) {
	t.Helper()
	result := callTool(t, session, &mcp.CallToolParams{Name: "read_file", Arguments: arguments})
	var content server.FileContent
	structured(t, result, &content)
	return result, content
}

// picture is a small PNG.
func picture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := range 40 {
		img.Set(x, 15, color.RGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	check(t, png.Encode(&b, img))
	return b.Bytes()
}

func TestReadFileGivesTextInWindowsImagesAsImagesAndDescribesTheRest(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author := clientAs(t, other)
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("line %d", i+1))
	}
	textPost, textID := attachAs(t, author, channel.Id, "log.txt", []byte(strings.Join(lines, "\n")+"\n"))
	_, pictureID := attachAs(t, author, channel.Id, "chart.png", picture(t))
	_, binaryID := attachAs(t, author, channel.Id, "blob.bin", []byte{0, 1, 2, 3, 0xff, 0xfe, 0, 0, 7})
	session := sessionFor(t, admin, user)

	_, text := readFile(t, session, map[string]any{"file_id": textID, "start_line": 11, "line_count": 10})
	switch {
	case text.Kind != "text" || text.Name != "log.txt" || text.PostID != textPost.Id:
		t.Errorf("the text file reads %+v", text)
	case text.Content == nil || !strings.HasPrefix(*text.Content, "line 11\n") || *text.StartLine != 11 || *text.EndLine != 20:
		t.Errorf("the window is %v, lines %v-%v", text.Content, text.StartLine, text.EndLine)
	case text.NextStartLine == nil || *text.NextStartLine != 21 || *text.TotalLines != 30:
		t.Errorf("the window says the next starts at %v of %v", text.NextStartLine, text.TotalLines)
	case !strings.HasSuffix(text.WebURL, "/_redirect/pl/"+textPost.Id):
		t.Errorf("the file links to %s", text.WebURL)
	}

	result, pic := readFile(t, session, map[string]any{"file_id": pictureID})
	if pic.Kind != "image" || pic.Image == nil || pic.Image.Width != 40 {
		t.Errorf("the picture reads %+v", pic)
	}
	shown := false
	for _, content := range result.Content {
		if image, ok := content.(*mcp.ImageContent); ok && len(image.Data) > 0 {
			shown = true
		}
	}
	if !shown {
		t.Error("the picture came back without an image")
	}

	if _, blob := readFile(t, session, map[string]any{"file_id": binaryID}); blob.Kind != "binary" || blob.Content != nil {
		t.Errorf("the binary file reads %+v", blob)
	}

	var read server.ChannelPosts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": channel.Id}}), &read)
	for _, post := range read.Posts {
		if post.ID == textPost.Id && (len(post.Files) != 1 || post.Files[0].ID != textID || post.Files[0].Name != "log.txt" || post.Files[0].Size == 0) {
			t.Errorf("the post lists its files as %+v", post.Files)
		}
	}
}

func TestSearchFilesFindsAFileByNameWithWhereItWasPosted(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	name := word(t) + ".csv"
	post, id := attachAs(t, clientAs(t, other), channel.Id, name, []byte("a,b\n1,2\n"))
	session := sessionFor(t, admin, user)

	var found server.FileResults
	eventually(t, 30*time.Second, func() (bool, string) {
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{
			"terms": strings.TrimSuffix(name, ".csv"), "team_id": team.Id,
		}}), &found)
		return len(found.Files) == 1, fmt.Sprint(found.Files)
	})
	file := found.Files[0]
	if file.ID != id || file.Name != name || file.PostID != post.Id || file.Channel != channel.DisplayName || file.Author != other.Username {
		t.Fatalf("found %+v", file)
	}
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{
		"terms": strings.TrimSuffix(name, ".csv"),
	}}), &found)
	if len(found.Files) != 1 {
		t.Fatalf("across every team found %d files", len(found.Files))
	}
}

func TestSaveFileWritesIntoTheDownloadDirectoryAndNeverOverwrites(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	content := []byte("quarterly numbers\n")
	_, id := attachAs(t, clientAs(t, other), channel.Id, "report.txt", content)
	session, downloads := localSession(t, admin, user, (&asked{}).answer(accept))
	save := func() server.SavedFile {
		var saved server.SavedFile
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "save_file", Arguments: map[string]any{"file_id": id}}), &saved)
		return saved
	}

	first, second := save(), save()
	if first.Path != filepath.Join(downloads, "report.txt") || second.Path != filepath.Join(downloads, "report (2).txt") {
		t.Fatalf("saved to %s and %s", first.Path, second.Path)
	}
	for _, path := range []string{first.Path, second.Path} {
		written, err := os.ReadFile(path)
		check(t, err)
		if !bytes.Equal(written, content) {
			t.Errorf("%s holds %q", path, written)
		}
	}
}

func TestPostMessageAttachesFilesShowingEachInTheQuestion(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	onDisk := filepath.Join(t.TempDir(), "results.csv")
	check(t, os.WriteFile(onDisk, []byte("test,passed\nlive,yes\n"), 0o600))
	questions := &asked{}
	session, _ := localSession(t, admin, user, questions.answer(accept))

	posted := postMessage(t, session, map[string]any{
		"channel_id": channel.Id,
		"message":    "Test results attached.",
		"files": []map[string]any{
			{"path": onDisk},
			{"name": "summary.md", "content": "# Summary\nAll green.\n"},
		},
	})

	question := questions.only(t)
	mustContain(t, "question", question.Message, "results.csv", "read from "+onDisk, "summary.md", "written for this post")
	mustContain(t, "checkbox", label(t, question), "2 files")
	infos, _, err := admin.GetFileInfosForPost(t.Context(), posted.ID, "")
	check(t, err)
	names := map[string]int64{}
	for _, info := range infos {
		names[info.Name] = info.Size
	}
	if len(infos) != 2 || names["results.csv"] != 21 || names["summary.md"] != 21 {
		t.Fatalf("the post carries %v", names)
	}
}

// A file changed between the question and the answer is not the file the
// person accepted.
func TestAFileChangedAfterThePersonWasAskedIsNotSent(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	onDisk := filepath.Join(t.TempDir(), "notes.txt")
	check(t, os.WriteFile(onDisk, []byte("what the person saw"), 0o600))
	session, _ := localSession(t, admin, user, func(*mcp.ElicitParams) *mcp.ElicitResult {
		check(t, os.WriteFile(onDisk, []byte("what was swapped in"), 0o600))
		return accept
	})

	_, err := tryTool(t, session, &mcp.CallToolParams{Name: "post_message", Arguments: map[string]any{
		"channel_id": channel.Id, "message": "notes", "files": []map[string]any{{"path": onDisk}},
	}})
	var refused *jsonrpc.Error
	if !errors.As(err, &refused) || !strings.Contains(refused.Message, "changed since the person was asked") {
		t.Fatalf("got %v; want the confirmation refused", err)
	}
	if posts := messagesIn(t, admin, channel.Id); len(posts) != 0 {
		t.Fatalf("the channel holds %d posts", len(posts))
	}
}

// Over HTTP a path names a file wherever the server runs, not on the person's
// machine.
func TestAServerNotOnThePersonsMachineAttachesNoFileFromAPath(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	onDisk := filepath.Join(t.TempDir(), "secret.txt")
	check(t, os.WriteFile(onDisk, []byte("x"), 0o600))
	session, questions := writingSession(t, admin, user, accept)

	result := callTool(t, session, &mcp.CallToolParams{Name: "post_message", Arguments: map[string]any{
		"channel_id": channel.Id, "message": "x", "files": []map[string]any{{"path": onDisk}},
	}})
	if !result.IsError || !strings.Contains(errorText(result), "does not run on the person's machine") {
		t.Fatalf("a server not on the person's machine took a file from a path: %s", errorText(result))
	}
	if len(questions.questions) != 0 || len(messagesIn(t, admin, channel.Id)) != 0 {
		t.Fatal("the person was asked, or something was posted")
	}
}
