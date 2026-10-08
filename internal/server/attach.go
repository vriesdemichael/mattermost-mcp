package server

import (
	"bytes"
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
// Only the read that uploads holds a file's bytes; the others keep its size,
// its first bytes and its digest.

// The most files one post carries, which is Mattermost's own limit, and the
// largest a file may be: from disk, Mattermost's default upload limit; as text
// the model writes, far more than a model writes.
const (
	maxAttachments       = 10
	maxAttachmentBytes   = 100 << 20
	maxWrittenAttachment = 1 << 20
)

// sniffBytes is how much of a file's start its type is read from, which is all
// http.DetectContentType looks at.
const sniffBytes = 512

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
	size   int64
	head   []byte   // the first sniffBytes, which its type is read from
	sum    [32]byte // its SHA-256
	data   []byte   // its bytes, when it was read to be uploaded
}

// kind is a file's type, as its name says and, where they disagree, as its
// bytes say: a key file named holiday.jpg reads as text, not as a picture.
func (a attachment) kind() string {
	byBytes := http.DetectContentType(a.head)
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
// not read or Mattermost would not take. keep holds each file's bytes, for the
// read that uploads them.
func loadAttachments(cfg config.Config, files []attachFile, keep bool) ([]attachment, error) {
	if len(files) > maxAttachments {
		return nil, fmt.Errorf("a post carries at most %d files, not %d", maxAttachments, len(files))
	}
	loaded := make([]attachment, 0, len(files))
	for i, file := range files {
		switch {
		case file.Path != "" && file.Content != "":
			return nil, fmt.Errorf("file %d: give path or content, not both", i+1)
		case file.Path != "":
			read, err := readLocal(cfg, file, keep)
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
			data := []byte(file.Content)
			loaded = append(loaded, attachment{
				name: name, size: int64(len(data)), head: data[:min(len(data), sniffBytes)], sum: sha256.Sum256(data), data: data,
			})
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
// the machine sign in to host with the person's credentials. A file where
// credentials are kept, or one that holds a private key or this server's own
// token, is refused too: a model can be talked into attaching one by what it
// reads, and the person may tick the box without reading the path.
func readLocal(cfg config.Config, file attachFile, keep bool) (attachment, error) {
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
	if err := notASecretStore(real); err != nil {
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
	read := attachment{source: real}
	digest := sha256.New()
	scan := newSecretScan(cfg.Token)
	var kept bytes.Buffer
	sinks := []io.Writer{digest, scan, &headWriter{head: &read.head}}
	if keep {
		sinks = append(sinks, &kept)
	}
	// The size is what is read, not what the file system says: some files
	// report none and hold plenty.
	read.size, err = io.Copy(io.MultiWriter(sinks...), io.LimitReader(opened, maxAttachmentBytes+1))
	switch {
	case err != nil:
		return attachment{}, fmt.Errorf("reading %s: %w", real, err)
	case read.size > maxAttachmentBytes:
		return attachment{}, fmt.Errorf("%s is more than the %s a file may be", real, size(maxAttachmentBytes))
	case scan.found != "":
		return attachment{}, fmt.Errorf("%s holds %s, so it is not attached. If the person means to share it, they attach it in Mattermost themselves", real, scan.found)
	}
	copy(read.sum[:], digest.Sum(nil))
	if keep {
		read.data = kept.Bytes()
	}
	given := file.Name
	if strings.TrimSpace(given) == "" {
		given = filepath.Base(real)
	}
	if read.name, err = fileName(given); err != nil {
		return attachment{}, err
	}
	return read, nil
}

// headWriter keeps the first sniffBytes written to it.
type headWriter struct{ head *[]byte }

func (w *headWriter) Write(p []byte) (int, error) {
	if room := sniffBytes - len(*w.head); room > 0 {
		*w.head = append(*w.head, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

// privateKeyMarker ends the first line of every PEM private key: RSA, EC,
// OpenSSH, PKCS #8, encrypted or not.
const privateKeyMarker = "PRIVATE KEY-----"

// secretScan looks through what is written to it for a private key or the
// server's own token, across the boundaries of the writes.
type secretScan struct {
	needles map[string]string // what to find -> how a refusal names it
	longest int
	tail    []byte
	found   string
}

func newSecretScan(token string) *secretScan {
	scan := &secretScan{needles: map[string]string{privateKeyMarker: "a private key"}}
	// A token too short to be one would be found in ordinary text.
	if len(token) >= 16 {
		scan.needles[token] = "the token this server acts with"
	}
	for needle := range scan.needles {
		scan.longest = max(scan.longest, len(needle))
	}
	return scan
}

func (s *secretScan) Write(p []byte) (int, error) {
	if s.found != "" {
		return len(p), nil
	}
	window := make([]byte, 0, len(s.tail)+len(p))
	window = append(append(window, s.tail...), p...)
	for needle, what := range s.needles {
		if bytes.Contains(window, []byte(needle)) {
			s.found = what
			return len(p), nil
		}
	}
	keep := min(len(window), s.longest-1)
	s.tail = append(s.tail[:0], window[len(window)-keep:]...)
	return len(p), nil
}

// secretDirectories are where credentials are kept: a file under one is not
// attached, whatever it is called.
var secretDirectories = map[string]bool{
	".ssh": true, ".gnupg": true, ".aws": true, ".azure": true, ".kube": true, ".docker": true,
	"gcloud": true, ".password-store": true, "keychains": true, "keyrings": true,
}

// secretFiles hold credentials by what they are. An MCP client's configuration
// holds this server's token in its env block.
var secretFiles = map[string]bool{
	".netrc": true, "_netrc": true, ".git-credentials": true, ".npmrc": true, ".pypirc": true, ".pgpass": true,
	"claude_desktop_config.json": true, ".claude.json": true, "mcp.json": true, "mcp_config.json": true,
	"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true,
	"login data": true, "cookies": true, "key4.db": true, "logins.json": true,
}

// secretExtensions are kinds of file that are key stores.
var secretExtensions = map[string]bool{".p12": true, ".pfx": true, ".kdbx": true, ".keychain": true, ".keychain-db": true, ".jks": true}

// notASecretStore refuses a path where credentials are kept.
func notASecretStore(path string) error {
	parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	for _, dir := range parts[:len(parts)-1] {
		if secretDirectories[strings.ToLower(dir)] {
			return fmt.Errorf("%s is in %s, where credentials are kept, so it is not attached. If the person means to share it, they attach it in Mattermost themselves", path, dir)
		}
	}
	name := strings.ToLower(parts[len(parts)-1])
	sample := strings.HasSuffix(name, ".example") || strings.HasSuffix(name, ".sample") || strings.HasSuffix(name, ".template")
	envFile := name == ".env" || strings.HasPrefix(name, ".env.") && !sample
	if secretFiles[name] || secretExtensions[filepath.Ext(name)] || envFile {
		return fmt.Errorf("%s is a file credentials are kept in, so it is not attached. If the person means to share it, they attach it in Mattermost themselves", path)
	}
	return nil
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
		b.WriteString(file.name + "=" + hex.EncodeToString(file.sum[:]) + "\n")
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
		fmt.Fprintf(&b, "\n- %s (%s, %s)", file.name, size(file.size), file.kind())
		if file.source != "" {
			fmt.Fprintf(&b, ", read from %s", file.source)
		} else {
			b.WriteString(", written for this post")
		}
	}
	return b.String()
}

// carrying names the files a post carries for the checkbox, with the path of
// any read from the person's disk, so a tick made out of habit still passes
// over where a file came from.
func carrying(files []attachment) string {
	var local []string
	for _, file := range files {
		if file.source != "" {
			local = append(local, file.source)
		}
	}
	count := fmt.Sprintf("%d %s", len(files), plural(int64(len(files)), "file", "files"))
	switch {
	case len(files) == 0:
		return ""
	case len(local) == 0:
		return " with " + count
	case len(files) == 1:
		return " with " + local[0] + " from this computer"
	default:
		return fmt.Sprintf(" with %s, %d from this computer: %s", count, len(local), strings.Join(local, ", "))
	}
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
