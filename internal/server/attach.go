package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/vriesdemichael/mm-mcp/internal/config"
)

// Files a post carries: from a path on a local server, or written by the model
// (ADR-029). They are read when the person is asked, read again when they
// accept, and uploaded only then, so a declined post leaves no file behind.

// The most files one post carries, which is Mattermost's own limit, and the
// largest a file may be: from disk, Mattermost's default upload limit; as text
// the model writes, far more than a model writes.
const (
	maxAttachments       = 10
	maxAttachmentBytes   = 100 << 20
	maxWrittenAttachment = 1 << 20
)

// attachFile is one file to attach to a post.
type attachFile struct {
	Path    string `json:"path,omitempty" jsonschema:"a file on this machine to attach, by its full path; only when the server runs on the person's own machine, over stdio"`
	Name    string `json:"name,omitempty" jsonschema:"the file's name: required with content; with path, a name to give the file instead of its own"`
	Content string `json:"content,omitempty" jsonschema:"the text of a file written for this post, such as a report or a log excerpt; give name too"`
}

// attachment is a file read for a post.
type attachment struct {
	name   string
	source string // the path it was read from, or empty for text the model wrote
	data   []byte
}

// kind is a file's type, as its name says and, where they disagree, as its
// bytes say: a key file named holiday.jpg reads as text, not as a picture.
func (a attachment) kind() string {
	byBytes := http.DetectContentType(a.data)
	byName := mime.TypeByExtension(strings.ToLower(filepath.Ext(a.name)))
	if byName == "" {
		return byBytes
	}
	family := func(kind string) string {
		head, _, _ := strings.Cut(kind, "/")
		return head
	}
	// Office documents and archives sniff as zip or octet-stream, which says
	// nothing against their names.
	if family(byName) == family(byBytes) || byBytes == "application/zip" || byBytes == "application/octet-stream" {
		return byName
	}
	return fmt.Sprintf("%s by its name, but its bytes read as %s", byName, byBytes)
}

// fileName is a name for a file as the question shows it and Mattermost stores
// it: its last element only. A name with control or invisible formatting
// characters is refused, since it could forge lines of the question or show
// one name and be another.
func fileName(name string) (string, error) {
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if i := strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }); i >= 0 {
		return "", fmt.Errorf("the file name %q holds a control or invisible character; give a plain name", name)
	}
	if name == "" || name == "." || name == "/" {
		return "", fmt.Errorf("give the file a name")
	}
	return name, nil
}

// loadAttachments reads the files a post carries, refusing what the server may
// not read or Mattermost would not take.
func loadAttachments(cfg config.Config, files []attachFile) ([]attachment, error) {
	if len(files) > maxAttachments {
		return nil, fmt.Errorf("a post carries at most %d files, not %d", maxAttachments, len(files))
	}
	loaded := make([]attachment, 0, len(files))
	for i, file := range files {
		switch {
		case file.Path != "" && file.Content != "":
			return nil, fmt.Errorf("file %d: give path or content, not both", i+1)
		case file.Path != "":
			read, err := readLocal(cfg, file)
			if err != nil {
				return nil, fmt.Errorf("file %d: %w", i+1, err)
			}
			loaded = append(loaded, read)
		case file.Content != "":
			name, err := fileName(file.Name)
			switch {
			case strings.TrimSpace(file.Name) == "":
				return nil, fmt.Errorf("file %d: give the name of the file the content is", i+1)
			case err != nil:
				return nil, fmt.Errorf("file %d: %w", i+1, err)
			case len(file.Content) > maxWrittenAttachment:
				return nil, fmt.Errorf("file %d: %s is %s, more than the %s a written file may be", i+1, name, size(int64(len(file.Content))), size(maxWrittenAttachment))
			}
			loaded = append(loaded, attachment{name: name, data: []byte(file.Content)})
		default:
			return nil, fmt.Errorf("file %d: give a path, or a name and content", i+1)
		}
	}
	return loaded, nil
}

// readLocal reads a file from this machine's disk, which only a server running
// on it, over stdio, may do: over HTTP a path names a file on wherever the
// server runs, not on the person's machine. The path is refused when it names
// another machine, a device or a file the system makes up rather than stores,
// before anything is opened: on Windows, merely looking at \\host\share makes
// the machine sign in to host with the person's credentials.
func readLocal(cfg config.Config, file attachFile) (attachment, error) {
	if !cfg.Local {
		return attachment{}, fmt.Errorf("this server does not run on the person's machine, so it attaches no file from a path; give the content of a text file instead")
	}
	path := strings.TrimSpace(file.Path)
	if err := ordinaryPath(path); err != nil {
		return attachment{}, err
	}
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == '\\') {
		home, err := os.UserHomeDir()
		if err != nil {
			return attachment{}, fmt.Errorf("finding the home directory for %s: %w", path, err)
		}
		path = filepath.Join(home, rest)
	}
	if !filepath.IsAbs(path) {
		return attachment{}, fmt.Errorf("%s is not a full path; give the whole path to the file", file.Path)
	}
	// The question shows the file itself, not a link to it.
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return attachment{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := ordinaryPath(real); err != nil {
		return attachment{}, err
	}
	info, err := os.Stat(real)
	switch {
	case err != nil:
		return attachment{}, fmt.Errorf("reading %s: %w", real, err)
	case !info.Mode().IsRegular():
		return attachment{}, fmt.Errorf("%s is not a file", real)
	}
	opened, err := os.Open(real) //nolint:gosec // the person confirms the path before anything read is sent
	if err != nil {
		return attachment{}, fmt.Errorf("reading %s: %w", real, err)
	}
	defer func() { _ = opened.Close() }()
	// The size is what is read, not what the file system says: some files
	// report none and hold plenty.
	data, err := io.ReadAll(io.LimitReader(opened, maxAttachmentBytes+1))
	switch {
	case err != nil:
		return attachment{}, fmt.Errorf("reading %s: %w", real, err)
	case len(data) > maxAttachmentBytes:
		return attachment{}, fmt.Errorf("%s is more than the %s a file may be", real, size(maxAttachmentBytes))
	}
	given := file.Name
	if strings.TrimSpace(given) == "" {
		given = filepath.Base(real)
	}
	name, err := fileName(given)
	if err != nil {
		return attachment{}, err
	}
	return attachment{name: name, source: real, data: data}, nil
}

// systemTrees hold files the system makes up rather than stores, among them a
// process's environment, which holds this server's credential (ADR-019).
var systemTrees = []string{"/proc", "/sys", "/dev"}

// ordinaryPath refuses a path that names another machine or a device, or lies
// in a tree the system makes up.
func ordinaryPath(path string) error {
	slashed := strings.ReplaceAll(path, "\\", "/")
	if strings.HasPrefix(slashed, "//") || strings.HasPrefix(filepath.VolumeName(path), "\\\\") {
		return fmt.Errorf("%s names a file on another machine or a device; attach a file on this machine", path)
	}
	for _, tree := range systemTrees {
		if slashed == tree || strings.HasPrefix(slashed, tree+"/") {
			return fmt.Errorf("%s is a file the system makes up, not one stored on disk; it is not attached", path)
		}
	}
	return nil
}

// fingerprint is what the files are, byte for byte, so an answer accepts the
// files the person was shown and not ones changed since.
func fingerprint(files []attachment) string {
	var b strings.Builder
	for _, file := range files {
		sum := sha256.Sum256(file.data)
		b.WriteString(file.name + "=" + hex.EncodeToString(sum[:]) + "\n")
	}
	return b.String()
}

// describeAttachments lists the files for the question: each one's name, size,
// type, and the path it was read from, which is what the person must see
// before a file from their disk goes to their colleagues.
func describeAttachments(files []attachment) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nAttached:")
	for _, file := range files {
		fmt.Fprintf(&b, "\n- %s (%s, %s)", file.name, size(int64(len(file.data))), file.kind())
		if file.source != "" {
			fmt.Fprintf(&b, ", read from %s", file.source)
		} else {
			b.WriteString(", written for this post")
		}
	}
	return b.String()
}

// size is a number of bytes as a person reads it.
func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
