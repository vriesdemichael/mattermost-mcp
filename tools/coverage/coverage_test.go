package main

import (
	"slices"
	"strings"
	"testing"
)

// This tool computes the numbers every coverage gate reads, so a bug in it makes
// CI pass when it should not. It is held to tests although tools/ is outside the
// gate it computes (ADR-014).

const unitProfile = `mode: atomic
github.com/vriesdemichael/mm-mcp/internal/a/a.go:10.1,12.2 2 1
github.com/vriesdemichael/mm-mcp/internal/a/a.go:14.1,15.2 3 0
github.com/vriesdemichael/mm-mcp/tools/x/x.go:1.1,2.2 5 0
`

const liveProfile = `mode: atomic
github.com/vriesdemichael/mm-mcp/internal/a/a.go:14.1,15.2 3 4
github.com/vriesdemichael/mm-mcp/internal/b/b.go:1.1,3.2 5 0
`

func parse(t *testing.T, profile string) []Block {
	t.Helper()
	blocks, err := ParseProfile(strings.NewReader(profile), module)
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

func TestABlockEitherSuiteCoversCountsAsCovered(t *testing.T) {
	t.Parallel()
	merged := InScope(Merge(parse(t, unitProfile), parse(t, liveProfile)), scope)
	percent, covered, total := Percent(merged)
	// a.go's two blocks are covered (5 statements), b.go's is not (5); tools/ is out of scope.
	if covered != 5 || total != 10 || percent != 50 {
		t.Fatalf("got %.2f%% (%d of %d)", percent, covered, total)
	}
}

func TestAMalformedProfileLineIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := ParseProfile(strings.NewReader("mode: set\nnot a profile line\n"), module); err == nil {
		t.Fatal("a malformed line was accepted")
	}
}

func TestChangedLinesAreReadFromTheNewSideOfTheDiff(t *testing.T) {
	t.Parallel()
	diff := `diff --git a/internal/a/a.go b/internal/a/a.go
--- a/internal/a/a.go
+++ b/internal/a/a.go
@@ -9,0 +11,2 @@ func f() {
+	x := 1
+	y := 2
@@ -20 +23 @@
-	old
+	new
diff --git a/internal/gone.go b/internal/gone.go
--- a/internal/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
`
	changed := ChangedLines(diff)
	var lines []int
	for line := range changed["internal/a/a.go"] {
		lines = append(lines, line)
	}
	slices.Sort(lines)
	if !slices.Equal(lines, []int{11, 12, 23}) || len(changed) != 1 {
		t.Fatalf("got %v", changed)
	}
}

func TestOnlyBlocksHoldingAChangedLineCountTowardsThePatch(t *testing.T) {
	t.Parallel()
	blocks := parse(t, unitProfile)
	touched := Touched(blocks, map[string]map[int]bool{"internal/a/a.go": {15: true}})
	if len(touched) != 1 || touched[0].StartLine != 14 {
		t.Fatalf("got %+v", touched)
	}
	if got := Uncovered(touched); !slices.Equal(got, []string{"internal/a/a.go:14-15"}) {
		t.Fatalf("got %v", got)
	}
}

func TestNothingToMeasureIsFullyCovered(t *testing.T) {
	t.Parallel()
	if percent, _, total := Percent(nil); percent != 100 || total != 0 {
		t.Fatalf("got %.2f%% of %d", percent, total)
	}
}
