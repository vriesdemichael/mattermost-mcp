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
		if _, err := loadAttachments(c.cfg, c.files); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: got %v; want it to say %q", name, err, c.says)
		}
	}
	loaded, err := loadAttachments(local, []attachFile{{Path: onDisk}, {Name: "b.md", Content: "# b"}})
	if err != nil || len(loaded) != 2 || loaded[0].name != "a.txt" || loaded[0].source != onDisk || loaded[1].source != "" {
		t.Fatalf("got %+v, %v", loaded, err)
	}
	if fingerprint(loaded) == fingerprint(loaded[:1]) {
		t.Error("two different sets of files share a fingerprint")
	}
}
