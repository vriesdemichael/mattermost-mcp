//go:build live

package live

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/fileview"
	"github.com/vriesdemichael/mm-mcp/internal/server"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport/filefixture"
)

// A file attached to a post is an MCP resource, read through the client as
// read_file reads it, and answers point at it (ADR-029).

// liveFile is a file's resource address on the live instance.
func liveFile(id string) string {
	return "mattermost://" + strings.TrimPrefix(strings.TrimPrefix(liveURL, "http://"), "https://") + "/files/" + id
}

func TestAFileIsReadAsAResourceAsReadFileReadsIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	author := clientAs(t, other)
	text := "first line\nsecond line\n"
	textPost, textID := attachAs(t, author, channel.Id, "notes.txt", []byte(text))
	_, wordID := attachAs(t, author, channel.Id, "minutes.docx", filefixture.Word(filefixture.WordParagraph("the minutes of the meeting")))
	_, pictureID := attachAs(t, author, channel.Id, "chart.png", picture(t))
	blob := []byte{0, 1, 2, 3, 0xff, 0xfe, 0, 0, 7}
	_, blobID := attachAs(t, author, channel.Id, "blob.bin", blob)
	_, emptyID := attachAs(t, author, channel.Id, "empty.txt", []byte{})
	long := strings.Repeat("a line of a long log\n", fileview.MediaBytes/20+1)
	_, longID := attachAs(t, author, channel.Id, "long.log", []byte(long))
	_, pdfID := attachAs(t, author, channel.Id, "brief.pdf", []byte(minimalPDF))
	_, soundID := attachAs(t, author, channel.Id, "tone.wav", silence())
	_, hugeID := attachAs(t, author, channel.Id, "disk.img", make([]byte, fileview.MediaBytes+1))
	session := sessionFor(t, admin, user)

	templates := listedTemplates(t, session)
	if len(templates) != 1 || templates[0].URITemplate != liveFile("{file_id}") {
		t.Fatalf("the server lists %+v", templates)
	}

	read := func(id string) *mcp.ResourceContents {
		t.Helper()
		result, err := readResource(t, session, "file", liveFile(id))
		check(t, err)
		if len(result.Contents) != 1 || result.Contents[0].URI != liveFile(id) {
			t.Fatalf("%s read as %+v", id, result.Contents)
		}
		return result.Contents[0]
	}
	if got := read(textID); got.Text != text || !strings.HasPrefix(got.MIMEType, "text/plain") {
		t.Errorf("the text file reads %q as %s", got.Text, got.MIMEType)
	}
	if got := read(wordID); !strings.Contains(got.Text, "the minutes of the meeting") || got.MIMEType != "text/plain; charset=utf-8" {
		t.Errorf("the Word file reads %q as %s", got.Text, got.MIMEType)
	}
	if got := read(pictureID); got.MIMEType != "image/png" || !bytes.HasPrefix(got.Blob, []byte("\x89PNG")) {
		t.Errorf("the picture reads %d bytes as %s", len(got.Blob), got.MIMEType)
	}
	if got := read(blobID); !bytes.Equal(got.Blob, blob) {
		t.Errorf("the binary file reads %v as %s", got.Blob, got.MIMEType)
	}
	if got := read(emptyID); got.Text != "" || got.Blob == nil || len(got.Blob) != 0 {
		t.Errorf("the empty file reads %+v", got)
	}
	if got := read(pdfID); got.MIMEType != "application/pdf" || string(got.Blob) != minimalPDF {
		t.Errorf("the PDF reads %d bytes as %s", len(got.Blob), got.MIMEType)
	}
	if got := read(soundID); !strings.HasPrefix(got.MIMEType, "audio/") || !bytes.Equal(got.Blob, silence()) {
		t.Errorf("the sound reads %d bytes as %s", len(got.Blob), got.MIMEType)
	}

	// More than a resource holds is refused with what reads it instead.
	for id, says := range map[string]string{longID: "the text of long.log is", hugeID: "disk.img is"} {
		_, err := readResource(t, session, "file", liveFile(id))
		if err == nil || !strings.Contains(err.Error(), says) || !strings.Contains(err.Error(), "read_file reads a text in windows, and save_file saves a file") {
			t.Errorf("%s: %v", says, err)
		}
	}
	// A file nobody has is not found.
	_, err := readResource(t, session, "file", liveFile("abcdefghijklmnopqrstuvwxyz"))
	var refused *jsonrpc.Error
	if !errors.As(err, &refused) || refused.Code != jsonrpc.CodeInvalidParams || refused.Message != "Resource not found" {
		t.Errorf("a file nobody has: %v", err)
	}

	// A post names each file by its address too, and the answer links it.
	result := callTool(t, session, &mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": textPost.Id, "include_thread": false}})
	var post server.PostWithThread
	structured(t, result, &post)
	if post.Post == nil || len(post.Post.Files) != 1 || post.Post.Files[0].URI != liveFile(textID) {
		t.Fatalf("read_post names the file as %+v", post.Post)
	}
	if !slices.ContainsFunc(result.Content, func(content mcp.Content) bool {
		link, ok := content.(*mcp.ResourceLink)
		return ok && link.URI == liveFile(textID) && link.Name == "notes.txt" && link.MIMEType != ""
	}) {
		t.Errorf("read_post links no resource: %+v", result.Content)
	}

	// read_file takes the address as it takes the id.
	_, content := readFile(t, session, map[string]any{"file_id": liveFile(textID)})
	if content.FileID != textID || content.URI != liveFile(textID) || content.Content == nil || *content.Content != text {
		t.Errorf("read_file by the address: %+v", content)
	}
}

// silence is a WAV file of one second of silence at 8 kHz.
func silence() []byte {
	samples := make([]byte, 8000)
	file := []byte("RIFF")
	file = binary.LittleEndian.AppendUint32(file, uint32(36+len(samples)))
	file = append(file, "WAVEfmt "...)
	file = binary.LittleEndian.AppendUint32(file, 16)   // the format chunk's length
	file = binary.LittleEndian.AppendUint16(file, 1)    // PCM
	file = binary.LittleEndian.AppendUint16(file, 1)    // one channel
	file = binary.LittleEndian.AppendUint32(file, 8000) // samples a second
	file = binary.LittleEndian.AppendUint32(file, 8000) // bytes a second
	file = binary.LittleEndian.AppendUint16(file, 1)    // bytes a sample
	file = binary.LittleEndian.AppendUint16(file, 8)    // bits a sample
	file = append(file, "data"...)
	file = binary.LittleEndian.AppendUint32(file, uint32(len(samples)))
	return append(file, samples...)
}

func TestSearchFilesAndCompletionPointAtEachFile(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	name := word(t)
	post, id := attachAs(t, clientAs(t, other), channel.Id, name+".csv", []byte("a,b\n1,2\n"))
	session := sessionFor(t, admin, user)

	var found server.FileResults
	var result *mcp.CallToolResult
	eventually(t, 30*time.Second, func() (bool, string) {
		result = callTool(t, session, &mcp.CallToolParams{Name: "search_files", Arguments: map[string]any{"terms": name}})
		structured(t, result, &found)
		return len(found.Files) == 1, fmt.Sprint(found.Files)
	})
	file := found.Files[0]
	if file.URI != liveFile(id) || file.PostURL != liveURL+"/"+team.Name+"/pl/"+post.Id || file.ChannelURL != liveURL+"/"+team.Name+"/channels/"+channel.Name {
		t.Errorf("search_files names the file %+v", file)
	}
	if !slices.ContainsFunc(result.Content, func(content mcp.Content) bool {
		link, ok := content.(*mcp.ResourceLink)
		return ok && link.URI == liveFile(id)
	}) {
		t.Errorf("search_files links no resource: %+v", result.Content)
	}

	// A file's name, or the start of it, finds it in the client's picker.
	completed := complete(t, session, "file", "file_id", name[:len(name)-2])
	if !slices.Contains(completed.Completion.Values, id) {
		t.Errorf("completing %q gave %v; want %s", name[:len(name)-2], completed.Completion.Values, id)
	}
}

func TestSaveFileTakesAFilesAddress(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	_, id := attachAs(t, clientAs(t, other), channel.Id, "report.txt", []byte("the report\n"))
	session, _ := localSession(t, admin, user, func(*mcp.ElicitParams) *mcp.ElicitResult { return accept })

	var saved server.SavedFile
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "save_file", Arguments: map[string]any{"file_id": liveFile(id)}}), &saved)
	if saved.FileID != id || !strings.HasSuffix(saved.Path, "report.txt") {
		t.Errorf("saved %+v", saved)
	}
}
