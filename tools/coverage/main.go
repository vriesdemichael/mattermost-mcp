// Command coverage applies the coverage gates to profiles already measured (ADR-014).
//
//	go run ./tools/coverage -profiles .tmp/coverage.unit.out,.tmp/coverage.live.out \
//	    -base-ref origin/next -min-global 85 -min-patch 85
//
// It merges the unit and live profiles, so a line either suite covers counts,
// then checks two things: the share of all statements under cmd/ and internal/
// that are covered, which catches erosion, and the share of the statements a
// branch changed, which catches new untested code. A patch failure names the
// uncovered changed lines. It reruns nothing, so re-checking after adding tests
// takes seconds.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const module = "github.com/vriesdemichael/mattermost-mcp"

var scope = []string{"cmd/", "internal/"}

func main() {
	profiles := flag.String("profiles", "", "comma-separated coverage profiles to merge")
	baseRef := flag.String("base-ref", "origin/next", "the branch the patch is measured against")
	minGlobal := flag.Float64("min-global", -1, "the global floor, in percent")
	minPatch := flag.Float64("min-patch", -1, "the patch threshold, in percent")
	write := flag.String("write-merged", "", "write the merged profile here, for Codecov")
	flag.Parse()
	if *profiles == "" || *minGlobal < 0 || *minPatch < 0 {
		fail(fmt.Errorf("-profiles, -min-global and -min-patch are required; the thresholds live in .github/coverage-thresholds.env"))
	}

	var all [][]Block
	for _, path := range strings.Split(*profiles, ",") {
		file, err := os.Open(path) //nolint:gosec // a path the caller names
		if err != nil {
			fail(err)
		}
		blocks, err := ParseProfile(file, module)
		_ = file.Close()
		if err != nil {
			fail(fmt.Errorf("%s: %w", path, err))
		}
		all = append(all, blocks)
	}
	merged := InScope(Merge(all...), scope)
	if *write != "" {
		fail(writeProfile(*write, merged))
	}

	failed := false
	global, covered, total := Percent(merged)
	fmt.Printf("Global coverage: %.2f%% (%d of %d statements under %s); floor %.2f%%\n",
		global, covered, total, strings.Join(scope, " and "), *minGlobal)
	if global < *minGlobal {
		fmt.Println("FAIL: global coverage is below the floor.")
		failed = true
	}

	diff, err := exec.Command("git", "diff", "--unified=0", "--no-color", *baseRef+"...HEAD", "--", "*.go").Output()
	if err != nil {
		fail(fmt.Errorf("could not diff against %s: %w", *baseRef, err))
	}
	touched := Touched(merged, ChangedLines(string(diff)))
	patch, patchCovered, patchTotal := Percent(touched)
	if patchTotal == 0 {
		fmt.Printf("Patch coverage: no coverable changed statements against %s.\n", *baseRef)
	} else {
		fmt.Printf("Patch coverage: %.2f%% (%d of %d changed statements against %s); threshold %.2f%%\n",
			patch, patchCovered, patchTotal, *baseRef, *minPatch)
		if patch < *minPatch {
			fmt.Println("FAIL: patch coverage is below the threshold. Uncovered changed lines:")
			for _, line := range Uncovered(touched) {
				fmt.Println("  " + line)
			}
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func writeProfile(path string, blocks []Block) error {
	var b strings.Builder
	b.WriteString("mode: atomic\n")
	for _, block := range blocks {
		fmt.Fprintf(&b, "%s/%s %d %d\n", module, block.key(), block.Statements, block.Count)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "coverage: %v\n", err)
		os.Exit(1)
	}
}
