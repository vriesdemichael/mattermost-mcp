package adr_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/adr"
)

const good = adr.FrontMatter + "# ADR-007: A rule\n\nThe rule, and why it holds.\n\n## Not chosen\n\n- **Another way**: why not.\n"

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAWellFormedRecordParses(t *testing.T) {
	t.Parallel()
	record, err := adr.Parse(write(t, t.TempDir(), "007-a-rule.md", good))
	if err != nil || record.Number != 7 || record.Title != "A rule" || record.Label() != "ADR-007" {
		t.Fatalf("got %+v, %v", record, err)
	}
}

func TestAMalformedRecordIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, text, complaint string }{
		{"7-a-rule.md", good, "NNN-slug.md"},
		{"007-A-Rule.md", good, "NNN-slug.md"},
		{"007-a-rule.md", strings.TrimPrefix(good, adr.FrontMatter), "front matter"},
		{"007-a-rule.md", strings.Replace(good, "ADR-007", "ADR-008", 1), "titled ADR-008"},
		{"007-a-rule.md", strings.Replace(good, "# ADR-007: A rule", "# A rule", 1), "# ADR-NNN: Title"},
		{"007-a-rule.md", strings.Replace(good, "## Not chosen", "## Context", 1), "'## Not chosen'"},
		{"007-a-rule.md", strings.Replace(good, "- **Another way**: why not.", "- another way", 1), "Not chosen entry"},
		{"007-a-rule.md", good + "\n", "exactly one newline"},
		{"007-a-rule.md", strings.ReplaceAll(good, "\n", "\r\n"), "carriage return"},
		{"007-a-rule.md", adr.FrontMatter + "# ADR-007: A rule\n", "states no rule"},
	}
	for _, c := range cases {
		_, err := adr.Parse(write(t, t.TempDir(), c.name, c.text))
		if err == nil || !strings.Contains(err.Error(), c.complaint) {
			t.Errorf("%s: got %v, want a complaint about %s", c.name, err, c.complaint)
		}
	}
}

func TestTwoRecordsMayNotShareANumber(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "007-a-rule.md", good)
	write(t, dir, "007-another-rule.md", good)
	if _, err := adr.LoadAll(dir); err == nil || !strings.Contains(err.Error(), "share ADR-007") {
		t.Fatalf("got %v", err)
	}
}

func TestTheIndexListsRecordsInNumberOrder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, dir, "010-later.md", strings.Replace(good, "ADR-007: A rule", "ADR-010: Later", 1))
	write(t, dir, "002-earlier.md", strings.Replace(good, "ADR-007: A rule", "ADR-002: Earlier", 1))
	write(t, dir, adr.IndexName, "ignored")
	records, err := adr.LoadAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	index := adr.RenderIndex(records)
	if strings.Index(index, "[ADR-002: Earlier](002-earlier.md)") > strings.Index(index, "[ADR-010: Later](010-later.md)") {
		t.Fatalf("out of order:\n%s", index)
	}
}
