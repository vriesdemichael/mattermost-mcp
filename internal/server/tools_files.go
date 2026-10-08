package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/fileview"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Files reach the model as content it can read, converted here, and reach the
// disk only from a server that runs on the person's machine (ADR-029).

// pdfType is a PDF's media type, which read_file returns whole when small.
const pdfType = "application/pdf"

// FileImage says how the image read_file returned compares with the one the
// file holds: scaling can leave small text illegible.
type FileImage struct {
	Width            int    `json:"width" jsonschema:"the image's width in pixels, upright"`
	Height           int    `json:"height" jsonschema:"the image's height in pixels, upright"`
	Turned           bool   `json:"turned" jsonschema:"true when the image was stored turned or mirrored and was turned upright"`
	Scaled           bool   `json:"scaled" jsonschema:"true when the image returned is smaller than the one stored, so small text in it may no longer be legible"`
	ReturnedWidth    int    `json:"returned_width"`
	ReturnedHeight   int    `json:"returned_height"`
	ReturnedMIMEType string `json:"returned_mime_type" jsonschema:"differs from mime_type when the image was encoded again, or converted from a format clients do not take"`
	ReturnedSize     int    `json:"returned_size" jsonschema:"in bytes"`
	Frames           int    `json:"frames,omitempty" jsonschema:"how many frames an animated image has; only the first is returned"`
	Pages            int    `json:"pages,omitempty" jsonschema:"how many pages a multi-page TIFF has; only the first is returned"`
}

// FileContent is what read_file found a file to be, and what came back of it.
type FileContent struct {
	FileID   string `json:"file_id"`
	Name     string `json:"name"`
	PostID   string `json:"post_id,omitempty" jsonschema:"the post the file is attached to"`
	Kind     string `json:"kind" jsonschema:"what the file is, which decides what came back: text, a window of its lines; document, a window of the text extracted from a Word, PowerPoint or Excel file; archive, a window of the listing of a zip or tar archive's entries; image, the image, in the content beside this; audio and video, the file itself beside its description when it is small enough (see media_returned); binary, a description of its type and size only; too_large, over the most this tool reads, so not read"`
	MIMEType string `json:"mime_type,omitempty" jsonschema:"the file's type, read from its bytes"`
	Size     int64  `json:"size" jsonschema:"in bytes"`
	WebURL   string `json:"web_url,omitempty" jsonschema:"the post the file is attached to, for a person to open"`
	// The window's fields are pointers, so they are there, zero or not, for a
	// file read as lines, and absent for one that is not.
	Content       *string    `json:"content,omitempty" jsonschema:"the window's lines without their numbers"`
	StartLine     *int       `json:"start_line,omitempty" jsonschema:"the first line in the window, counting from 1"`
	EndLine       *int       `json:"end_line,omitempty" jsonschema:"the last line in the window"`
	TotalLines    *int       `json:"total_lines,omitempty" jsonschema:"how many lines the whole text has"`
	NextStartLine *int       `json:"next_start_line,omitempty" jsonschema:"where the next window starts: pass it as start_line for the lines that follow; absent when this window reaches the end"`
	Image         *FileImage `json:"image,omitempty"`
	MediaReturned *bool      `json:"media_returned,omitempty" jsonschema:"for audio and video: whether the file itself came back in the content"`
	// DocumentReturned says a PDF came back whole, for a client that reads it.
	DocumentReturned *bool `json:"document_returned,omitempty" jsonschema:"for a PDF: whether the file itself came back in the content, as an embedded resource a client that reads PDFs can read"`
}

type readFileInput struct {
	FileID    string `json:"file_id" jsonschema:"the file, as a post's files or search_files give it"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"the first line of the window, counting from 1 (the default); an answer that stops short of the end gives next_start_line, the value to pass here for the lines that follow"`
	LineCount int    `json:"line_count,omitempty" jsonschema:"how many lines the window holds: 500 by default, at most 2000; a window also stops at 32 KiB of text"`
}

// fileOperationUses are the operations that read one file.
func fileOperationUses() []Use {
	return []Use{
		{Operation: "GetFileInfo", Params: map[string]Coverage{"file_id": SetBy("file_id")}},
		{Operation: "GetFile", Params: map[string]Coverage{"file_id": SetBy("file_id")}},
	}
}

func readFileSpec() Spec {
	return shaping(configuredToolSpec(
		&mcp.Tool{
			Name: "read_file",
			Description: "Read a file attached to a post. Text comes back as a window of numbered lines: start_line and line_count " +
				"choose it, and each answer says which lines it holds and where the next window starts. A Word, PowerPoint or Excel file " +
				"comes back as the text extracted from it, and an archive (zip, tar, tar.gz, tar.bz2) as a listing of its entries, both in " +
				"the same windows. An image (PNG, JPEG, GIF, WebP, BMP, TIFF) comes back as an image, turned upright and scaled down when it " +
				"is large, with a note saying so. Small audio, video and PDF files come back as themselves beside a description. Any other " +
				fmt.Sprintf("file is described by its type and size, and a file over %d MiB is described without being read.", fileview.MaxFileBytes>>20),
			Annotations: readOnly("Read file"),
		},
		fileOperationUses(),
		func(clientFor ClientFor, cfg config.Config) mcp.ToolHandlerFor[readFileInput, FileContent] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input readFileInput) (*mcp.CallToolResult, FileContent, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, FileContent{}, err
				}
				info, err := client.FileInfo(ctx, input.FileID)
				if err != nil {
					return nil, FileContent{}, err
				}
				view := fileview.Request{
					Name:      info.Name,
					WebURL:    client.Permalink(info.PostId),
					StartLine: input.StartLine,
					LineCount: input.LineCount,
				}
				if err := view.Validate(); err != nil {
					return nil, FileContent{}, err
				}
				var read fileview.View
				var pdf []byte
				if info.Size > fileview.MaxFileBytes {
					// Described rather than refused: the model needs to know the
					// file is there and too large, not nothing.
					read = fileview.TooLarge(view, fileview.MaxFileBytes, info.Size)
				} else {
					data, err := client.File(ctx, input.FileID)
					if err != nil {
						return nil, FileContent{}, err
					}
					if read, err = fileview.Read(ctx, view, data); err != nil {
						return nil, FileContent{}, err
					}
					// By its bytes or by what Mattermost recorded: a PDF that is all
					// text reads as text, and is still a PDF to a client.
					if (read.MIMEType == pdfType || info.MimeType == pdfType) && len(data) <= fileview.MediaBytes {
						pdf = data
					}
				}
				result, out := fileResult(input.FileID, info.PostId, view, read, pdf)
				return result, out, nil
			}
		},
	), map[string]string{
		"start_line": "chooses the window of lines the converted text is returned in",
		"line_count": "chooses the window of lines the converted text is returned in",
	})
}

// fileResult puts a view of a file into a tool result. The text is what the
// model reads, so it is the content itself, an image follows it, and the
// structured answer carries the same facts for a client that parses them.
func fileResult(fileID, postID string, request fileview.Request, view fileview.View, pdf []byte) (*mcp.CallToolResult, FileContent) {
	out := FileContent{
		FileID:   fileID,
		Name:     request.Name,
		PostID:   postID,
		Kind:     string(view.Kind),
		MIMEType: view.MIMEType,
		Size:     view.Size,
		WebURL:   request.WebURL,
	}
	if window := view.Window; window != nil {
		content, start, end, total := window.Content, window.StartLine, window.EndLine, window.TotalLines
		out.Content, out.StartLine, out.EndLine, out.TotalLines = &content, &start, &end, &total
		if next := window.NextStartLine; next > 0 {
			out.NextStartLine = &next
		}
	}
	content := []mcp.Content{&mcp.TextContent{Text: view.Text}}
	if image := view.Image; image != nil {
		content = append(content, &mcp.ImageContent{Data: image.Data, MIMEType: image.MIMEType})
		out.Image = &FileImage{
			Width: image.Width, Height: image.Height, Turned: image.Turned, Scaled: image.Scaled,
			ReturnedWidth: image.ReturnedWidth, ReturnedHeight: image.ReturnedHeight,
			ReturnedMIMEType: image.MIMEType, ReturnedSize: len(image.Data),
			Frames: image.Frames, Pages: image.Pages,
		}
	}
	if view.Kind == fileview.KindAudio || view.Kind == fileview.KindVideo {
		returned := view.Media != nil
		out.MediaReturned = &returned
	}
	if pdf != nil {
		// Converting a PDF is the client's to do: one that reads PDFs reads it
		// from the resource, and one that does not still has the description.
		content = append(content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
			URI: "mm-mcp://files/" + fileID, MIMEType: pdfType, Blob: pdf,
		}})
		returned := true
		out.DocumentReturned = &returned
	}
	if media := view.Media; media != nil {
		if view.Kind == fileview.KindVideo {
			// MCP has no video content; an embedded resource carries any bytes
			// with their type. Its address names the file without being one a
			// client could fetch, which would need the credential.
			content = append(content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI: "mm-mcp://files/" + fileID, MIMEType: media.MIMEType, Blob: media.Data,
			}})
		} else {
			content = append(content, &mcp.AudioContent{Data: media.Data, MIMEType: media.MIMEType})
		}
	}
	return &mcp.CallToolResult{Content: content}, out
}

// FoundFile is a file a search found, with where it was posted.
type FoundFile struct {
	Attachment
	PostID    string `json:"post_id"`
	ChannelID string `json:"channel_id"`
	Channel   string `json:"channel" jsonschema:"the channel's display name; a direct message is named after the person on the other side"`
	Team      string `json:"team,omitempty"`
	Author    string `json:"author" jsonschema:"the username of whoever posted it"`
	CreatedAt string `json:"created_at"`
}

// FileResults is a page of files a search found.
type FileResults struct {
	Files []FoundFile `json:"files" jsonschema:"newest first"`
	capped
	pageInfo
}

type searchFilesInput struct {
	Terms string `json:"terms,omitempty" jsonschema:"words in the file's name, or in its text where the server extracts it; ext:pdf for a type"`
	searchFilters
	TeamID   string `json:"team_id,omitempty" jsonschema:"search one team only; every team when not given"`
	MatchAny bool   `json:"match_any,omitempty" jsonschema:"find files matching any of the words rather than all of them"`
	Limit    int    `json:"limit,omitempty" jsonschema:"how many files a page holds, at most 100; 20 when not given"`
	pageArgs
}

func searchFilesSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "search_files",
			Description: "Search the files attached to posts the user can read: by name, by text where the server extracts it, by type with " +
				"ext:pdf, and by who posted them, in which channel and when, through from, in, before, after and on. Each file comes " +
				"with the post and channel it is in; read_file reads it. Mattermost's search finds the 100 most recent matches at most; " +
				"capped says when it did, and before reaches older ones.",
			Annotations: readOnly("Search files"),
		},
		uses([]Use{
			{
				Operation: "SearchFiles",
				Params: map[string]Coverage{
					"team_id":                       SetBy("team_id"),
					"body.terms":                    SetBy("terms"),
					"body.is_or_search":             SetBy("match_any"),
					"body.page":                     Fixed("0", "Team Edition's database search answers the first page with every match, and later pages with nothing"),
					"body.per_page":                 Fixed(fmt.Sprint(searchReach), "the database search answers with its 100 most recent matches whatever it is asked; the tool pages through them itself"),
					"body.time_zone_offset":         Fixed("the offset of the person's timezone on the day given", "on:, before: and after: days are the person's own, as in Mattermost's search box; 0 when no day is given"),
					"body.include_deleted_channels": Omitted("archived channels are left out of a search, as they are out of get_user_channels"),
				},
			},
		}, channelLookupUses("in", SetBy("team_id")), fromUses(), describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[searchFilesInput, FileResults] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input searchFilesInput) (*mcp.CallToolResult, FileResults, error) {
				limit, err := limitOf(input.Limit, defaultPostsPerSearch, maxPostsPerSearch)
				if err != nil {
					return nil, FileResults{}, err
				}
				at, err := openCursor("search_files", input, input.Cursor)
				if err != nil {
					return nil, FileResults{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, FileResults{}, err
				}
				terms, teamID, offset, err := searchTerms(ctx, client, input.Terms, input.TeamID, input.searchFilters)
				if err != nil {
					return nil, FileResults{}, err
				}
				found, err := client.SearchFiles(ctx, mattermost.FileSearch{
					TeamID: teamID, Terms: terms, MatchAny: input.MatchAny, PerPage: maxPostsPerSearch, TimeOffset: offset,
				})
				if err != nil {
					return nil, FileResults{}, err
				}
				// The database search answers with every match at once; a page is a
				// slice of it.
				var all []*model.FileInfo
				for _, id := range found.Order {
					if file := found.FileInfos[id]; file != nil {
						all = append(all, file)
					}
				}
				// Most recent first, and by id between files of the same millisecond,
				// so a page is the same slice each time the search is run.
				slices.SortStableFunc(all, func(a, b *model.FileInfo) int {
					if a.CreateAt != b.CreateAt {
						return int(b.CreateAt - a.CreateAt)
					}
					return strings.Compare(a.Id, b.Id)
				})
				files, next := offsetPage(all, at, limit)
				// A file is named where it was posted as a post is: its channel,
				// team and author, read once for all of them.
				posted := make([]*model.Post, 0, len(files))
				for _, file := range files {
					posted = append(posted, &model.Post{ChannelId: file.ChannelId, UserId: file.CreatorId})
				}
				described, err := describePosts(ctx, client, posted)
				if err != nil {
					return nil, FileResults{}, err
				}
				out := FileResults{Files: make([]FoundFile, 0, len(files)), capped: capped{len(all) >= searchReach}, pageInfo: pageInfo{NextCursor: next}}
				for i, file := range files {
					out.Files = append(out.Files, FoundFile{
						Attachment: toAttachment(file),
						PostID:     file.PostId,
						ChannelID:  file.ChannelId,
						Channel:    described[i].Channel,
						Team:       described[i].Team,
						Author:     described[i].Author,
						CreatedAt:  timestamp(file.CreateAt),
					})
				}
				return nil, out, nil
			}
		},
	), withShapes(searchFilterShapes, pagingShapes))
}

// maxSavedFileBytes is the largest file save_file writes: Mattermost's own
// default limit on an upload. It is held in memory on the way to the disk.
const maxSavedFileBytes = 100 << 20

// SavedFile is a file save_file wrote.
type SavedFile struct {
	FileID string `json:"file_id"`
	Path   string `json:"path" jsonschema:"where the file was written, on the person's machine"`
	Size   int64  `json:"size" jsonschema:"in bytes"`
	// AlreadySaved says this server saved the file there before, unchanged, so
	// it wrote no second copy.
	AlreadySaved bool   `json:"already_saved,omitempty" jsonschema:"true when the file was saved there before and no second copy was written"`
	Note         string `json:"note,omitempty"`
}

// savedFiles remembers where this process saved each file, so asking again
// for one already saved answers with it rather than filling the disk with
// copies.
type savedFiles struct {
	mu    sync.Mutex
	paths map[string]string
}

var saved = &savedFiles{paths: map[string]string{}}

// lookup is where a file was saved before, in dir, when it is still there at
// its size.
func (s *savedFiles) lookup(fileID, dir string, size int64) (string, bool) {
	s.mu.Lock()
	path, ok := s.paths[fileID]
	s.mu.Unlock()
	if !ok || filepath.Dir(path) != dir {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != size {
		return "", false
	}
	return path, true
}

func (s *savedFiles) remember(fileID, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paths[fileID] = path
}

type saveFileInput struct {
	FileID string `json:"file_id" jsonschema:"the file to save, as a post's files or search_files give it"`
}

func saveFileSpec() Spec {
	return local(configuredToolSpec(
		&mcp.Tool{
			Name: "save_file",
			Description: "Save a file attached to a post into the person's download directory on this machine, under its own name, " +
				"and answer with the path. An existing file is never overwritten: a number is added to the name instead, " +
				"and saving the same file again answers with the copy already saved. " +
				"Use read_file to read a file; save_file is for when the person wants the file itself.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Save file",
				ReadOnlyHint:    false,
				DestructiveHint: ptr(false),
				IdempotentHint:  false,
				OpenWorldHint:   ptr(false),
			},
		},
		fileOperationUses(),
		func(clientFor ClientFor, cfg config.Config) mcp.ToolHandlerFor[saveFileInput, SavedFile] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input saveFileInput) (*mcp.CallToolResult, SavedFile, error) {
				dir, err := downloadDir(cfg)
				if err != nil {
					return nil, SavedFile{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, SavedFile{}, err
				}
				info, err := client.FileInfo(ctx, input.FileID)
				if err != nil {
					return nil, SavedFile{}, err
				}
				if info.Size > maxSavedFileBytes {
					return nil, SavedFile{}, fmt.Errorf("%s is %d MiB, more than the %d MiB save_file writes; the person can download it from the post",
						info.Name, info.Size>>20, maxSavedFileBytes>>20)
				}
				if path, ok := saved.lookup(input.FileID, dir, info.Size); ok {
					return nil, SavedFile{FileID: input.FileID, Path: path, Size: info.Size, AlreadySaved: true}, nil
				}
				data, err := client.File(ctx, input.FileID)
				if err != nil {
					return nil, SavedFile{}, err
				}
				path, err := writeNew(dir, info.Name, data)
				if err != nil {
					return nil, SavedFile{}, err
				}
				saved.remember(input.FileID, path)
				answer := SavedFile{FileID: input.FileID, Path: path, Size: int64(len(data))}
				if err := markDownloaded(path, client.Permalink(info.PostId), cfg.URL); err != nil {
					answer.Note = "The file could not be marked as downloaded from the internet (" + err.Error() + "), so the system will not warn before it is opened."
				}
				return nil, answer, nil
			}
		},
	))
}

// downloadDir is where save_file writes: the configured directory, or the
// person's Downloads directory, made when it is not there yet.
func downloadDir(cfg config.Config) (string, error) {
	dir := cfg.DownloadDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("finding the Downloads directory: %w; set %s", err, config.EnvDownloadDir)
		}
		dir = filepath.Join(home, "Downloads")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("making the download directory: %w", err)
	}
	return dir, nil
}

// safeName is a file's name as save_file writes it: its last element only,
// with what Windows forbids in a name, control characters and invisible
// formatting characters, which can make one name look like another, replaced,
// so a name from Mattermost cannot reach outside the download directory. A
// name the system keeps for a device, such as NUL or COM1 on Windows, gets a
// leading underscore, so the file is written rather than sent to the device.
func safeName(name string) string {
	name = filepath.Base(filepath.Clean("/" + strings.ReplaceAll(name, "\\", "/")))
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" || name == "_" {
		return "file"
	}
	if !filepath.IsLocal(name) || reservedOnWindows(name) {
		return "_" + name
	}
	return name
}

// reservedOnWindows reports whether a name is one Windows keeps for a device,
// with or without an extension, which files saved here may be carried to.
func reservedOnWindows(name string) bool {
	stem, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch stem {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// writeNew writes data to a file of its own in dir: name, or name with a
// number added when that is taken. It never opens an existing file.
func writeNew(dir, name string, data []byte) (string, error) {
	name = safeName(name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 1; n <= 1000; n++ {
		candidate := name
		if n > 1 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		path := filepath.Join(dir, candidate)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640) //nolint:gosec // the name is made safe and the directory is the person's own
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("writing %s: %w", path, err)
		}
		return path, nil
	}
	return "", fmt.Errorf("a thousand files named like %s are in %s already", name, dir)
}
