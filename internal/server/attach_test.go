package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/config"
)

// Saving and attaching files, where no request is involved: names, the disk,
// and what is refused before anyone is asked (ADR-029).

func TestASavedFilesNameCannotReachOutsideTheDownloadDirectory(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"report.pdf":            "report.pdf",
		"../../etc/passwd":      "passwd",
		`..\..\Windows\win.ini`: "win.ini",
		"a:b*c?.txt":            "a_b_c_.txt",
		"..":                    "file",
		"":                      "file",
		"trailing. ":            "trailing",
		"NUL":                   "_NUL",
		"con.txt":               "_con.txt",
		"invoice\u202efdp.exe":  "invoice_fdp.exe",
		"line\nbreak.txt":       "line_break.txt",
	} {
		if got := safeName(name); got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
}

func TestWriteNewNeverOverwritesAFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	existing := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(existing, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := writeNew(dir, "notes.txt", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "notes (2).txt") {
		t.Errorf("wrote %s", path)
	}
	if kept, _ := os.ReadFile(existing); string(kept) != "keep me" {
		t.Errorf("the existing file now holds %q", kept)
	}
}

func TestAttachmentsAreRefusedBeforeAnyoneIsAsked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	onDisk := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(onDisk, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	local := config.Config{Local: true}
	for name, c := range map[string]struct {
		cfg   config.Config
		files []attachFile
		says  string
	}{
		"a path on a server that is not local": {config.Config{}, []attachFile{{Path: onDisk}}, "does not run on the person's machine"},
		"a relative path":                      {local, []attachFile{{Path: "a.txt"}}, "not a full path"},
		"a directory":                          {local, []attachFile{{Path: dir}}, "not a file"},
		"path and content":                     {local, []attachFile{{Path: onDisk, Content: "x"}}, "not both"},
		"content without a name":               {local, []attachFile{{Content: "x"}}, "give the name"},
		"neither":                              {local, []attachFile{{Name: "x"}}, "give a path"},
		"too much written text":                {local, []attachFile{{Name: "big.txt", Content: strings.Repeat("x", maxWrittenAttachment+1)}}, "more than"},
		"too many files":                       {local, make([]attachFile, maxAttachments+1), "at most"},
	} {
		if _, err := loadAttachments(c.cfg, c.files, false); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v; want it to say %q", name, err, c.says)
		}
	}
	// The source is the file's real path, which the temporary directory's need
	// not be: /var is a link on macOS, and Windows may name it by a short name.
	real, err := filepath.EvalSymlinks(onDisk)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadAttachments(local, []attachFile{{Path: onDisk}, {Name: "b.md", Content: "# b"}}, false)
	if err != nil || len(loaded) != 2 || loaded[0].name != "a.txt" || loaded[0].source != real || loaded[1].source != "" {
		t.Fatalf("got %+v, %v", loaded, err)
	}
	if fingerprint(loaded) == fingerprint(loaded[:1]) {
		t.Error("two different sets of files share a fingerprint")
	}
}

func TestAPathToAnotherMachineADeviceOrMadeUpFileIsRefusedBeforeItIsRead(t *testing.T) {
	t.Parallel()
	for _, path := range []string{`\\attacker.example\share\x.txt`, "//attacker.example/share/x.txt", `\\?\C:\x.txt`, "/proc/self/environ", "/dev/zero", "/sys/kernel"} {
		if err := ordinaryPath(path); err == nil {
			t.Errorf("%s was taken as an ordinary file", path)
		}
		if _, err := loadAttachments(config.Config{Local: true}, []attachFile{{Path: path}}, false); err == nil {
			t.Errorf("%s was read", path)
		}
	}
	if err := ordinaryPath("/home/someone/report.pdf"); err != nil {
		t.Errorf("an ordinary path: %v", err)
	}
}

func TestAFileNameThatCouldForgeTheQuestionIsRefused(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a.txt (3 bytes), written for this post\n- id_rsa", "invoice\u202efdp.exe", "tab\there"} {
		if _, err := loadAttachments(config.Config{}, []attachFile{{Name: name, Content: "x"}}, false); err == nil {
			t.Errorf("%q was taken as a file name", name)
		}
	}
}

func TestALinkIsAttachedAsTheFileItPointsAt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("cannot make a link here: %v", err)
	}
	loaded, err := loadAttachments(config.Config{Local: true}, []attachFile{{Path: link}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(real); loaded[0].source != want || loaded[0].name != "real.txt" {
		t.Fatalf("attached %+v; want it read from %s", loaded[0], want)
	}
}

func TestAFileWhoseBytesDisagreeWithItsNameSaysSo(t *testing.T) {
	t.Parallel()
	key := attachment{name: "holiday.jpg", head: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")}
	if kind := key.kind(); !strings.Contains(kind, "image/jpeg by its name") || !strings.Contains(kind, "text/plain") {
		t.Errorf("a key named holiday.jpg reads as %q", kind)
	}
	notes := attachment{name: "notes.txt", head: []byte("plain words")}
	if kind := notes.kind(); strings.Contains(kind, "but") {
		t.Errorf("a text file reads as %q", kind)
	}
}

// TestAFileWhereCredentialsAreKeptIsNotAttached: a model can be talked into
// attaching one by what it reads, and the person may tick without reading.
func TestAFileWhereCredentialsAreKeptIsNotAttached(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	local := config.Config{Local: true, Token: "abcdefghijklmnopqrstuvwxyz"}
	write := func(rel, content string) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for name, path := range map[string]string{
		"a key in .ssh":                 write(".ssh/known_hosts", "host ssh-ed25519 AAAA"),
		"a client's configuration":      write("Claude/claude_desktop_config.json", "{}"),
		"an env file":                   write("project/.env", "A=1"),
		"a key store":                   write("certs/me.p12", "x"),
		"a private key under any name":  write("notes/holiday.txt", "-----BEGIN OPENSSH PRIVATE KEY-----\nb3Bl\n"),
		"the server's own token inside": write("notes/settings.toml", `token = "abcdefghijklmnopqrstuvwxyz"`),
	} {
		_, err := loadAttachments(local, []attachFile{{Path: path, Name: "harmless.txt"}}, false)
		if err == nil || !strings.Contains(err.Error(), "attach it in Mattermost themselves") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	for _, path := range []string{write("project/.env.example", "A="), write("notes/ssh-howto.md", "Run ssh-keygen.")} {
		if _, err := loadAttachments(local, []attachFile{{Path: path}}, false); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestTheSecretScanFindsWhatTwoReadsSplit(t *testing.T) {
	t.Parallel()
	scan := newSecretScan("abcdefghijklmnopqrstuvwxyz")
	for _, part := range []string{"before abcdefghijk", "lmnopqrstuvwxyz after"} {
		_, _ = scan.Write([]byte(part))
	}
	if scan.found != "the token this server acts with" {
		t.Fatalf("found %q", scan.found)
	}
	if short := newSecretScan("abc"); len(short.needles) != 1 {
		t.Fatalf("a three-letter token is looked for: %v", short.needles)
	}
}

func TestOnlyTheReadThatUploadsHoldsTheBytes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("r", 2000)), 0o600); err != nil {
		t.Fatal(err)
	}
	local := config.Config{Local: true}
	asked, err := loadAttachments(local, []attachFile{{Path: path}}, false)
	if err != nil {
		t.Fatal(err)
	}
	uploading, err := loadAttachments(local, []attachFile{{Path: path}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if asked[0].data != nil || len(uploading[0].data) != 2000 || asked[0].size != 2000 || len(asked[0].head) != sniffBytes {
		t.Fatalf("asked %d bytes held, %d size, %d head; uploading %d", len(asked[0].data), asked[0].size, len(asked[0].head), len(uploading[0].data))
	}
	if fingerprint(asked) != fingerprint(uploading) {
		t.Fatal("the two reads of one file disagree")
	}
}

func TestTheCheckboxNamesAFileFromThisComputerByItsPath(t *testing.T) {
	t.Parallel()
	local := attachment{name: "notes.txt", source: "/home/me/notes.txt"}
	written := attachment{name: "report.md"}
	cases := []struct {
		files []attachment
		want  string
	}{
		{nil, ""},
		{[]attachment{written}, " with 1 file"},
		{[]attachment{local}, " with /home/me/notes.txt from this computer"},
		{[]attachment{written, local}, " with 2 files, 1 from this computer: /home/me/notes.txt"},
	}
	for _, c := range cases {
		if got := carrying(c.files); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
