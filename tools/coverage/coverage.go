package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Block is one statement block of a Go coverage profile.
type Block struct {
	File               string
	StartLine, EndLine int
	StartCol, EndCol   int
	Statements         int
	Count              int
}

func (b Block) key() string {
	return fmt.Sprintf("%s:%d.%d,%d.%d", b.File, b.StartLine, b.StartCol, b.EndLine, b.EndCol)
}

var profileLine = regexp.MustCompile(`^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$`)

// ParseProfile reads a Go coverage profile, with each file's path made
// relative to the repository by dropping the module path.
func ParseProfile(r io.Reader, module string) ([]Block, error) {
	var blocks []Block
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		m := profileLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("not a coverage profile line: %q", line)
		}
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		blocks = append(blocks, Block{
			File:      strings.TrimPrefix(m[1], module+"/"),
			StartLine: n(2), StartCol: n(3), EndLine: n(4), EndCol: n(5),
			Statements: n(6), Count: n(7),
		})
	}
	return blocks, scanner.Err()
}

// Merge combines the profiles of several runs: a block counts as covered when
// any run covered it.
func Merge(profiles ...[]Block) []Block {
	byKey := map[string]Block{}
	for _, profile := range profiles {
		for _, block := range profile {
			if existing, ok := byKey[block.key()]; ok {
				existing.Count += block.Count
				byKey[block.key()] = existing
				continue
			}
			byKey[block.key()] = block
		}
	}
	merged := make([]Block, 0, len(byKey))
	for _, block := range byKey {
		merged = append(merged, block)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].key() < merged[j].key() })
	return merged
}

// InScope keeps the blocks of files under one of the prefixes.
func InScope(blocks []Block, prefixes []string) []Block {
	var kept []Block
	for _, block := range blocks {
		for _, prefix := range prefixes {
			if strings.HasPrefix(block.File, prefix) {
				kept = append(kept, block)
				break
			}
		}
	}
	return kept
}

// Percent is the share of statements covered, and the counts it is made of.
func Percent(blocks []Block) (percent float64, covered, total int) {
	for _, block := range blocks {
		total += block.Statements
		if block.Count > 0 {
			covered += block.Statements
		}
	}
	if total == 0 {
		return 100, 0, 0
	}
	return 100 * float64(covered) / float64(total), covered, total
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// ChangedLines reads a zero-context unified diff into the new-side lines it
// adds or changes, per file.
func ChangedLines(diff string) map[string]map[int]bool {
	changed := map[string]map[int]bool{}
	file := ""
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
			if file == "/dev/null" {
				file = ""
			}
		case strings.HasPrefix(line, "@@") && file != "":
			m := hunkHeader.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			start, _ := strconv.Atoi(m[1])
			count := 1
			if m[2] != "" {
				count, _ = strconv.Atoi(m[2])
			}
			if changed[file] == nil {
				changed[file] = map[int]bool{}
			}
			for l := start; l < start+count; l++ {
				changed[file][l] = true
			}
		}
	}
	return changed
}

// Touched keeps the blocks that hold a changed line.
func Touched(blocks []Block, changed map[string]map[int]bool) []Block {
	var touched []Block
	for _, block := range blocks {
		lines := changed[block.File]
		for l := block.StartLine; l <= block.EndLine; l++ {
			if lines[l] {
				touched = append(touched, block)
				break
			}
		}
	}
	return touched
}

// Uncovered lists the uncovered blocks as file:start-end, one file per line,
// which is what a person fixing a patch-coverage failure needs to read.
func Uncovered(blocks []Block) []string {
	byFile := map[string][]string{}
	var files []string
	for _, block := range blocks {
		if block.Count > 0 {
			continue
		}
		if byFile[block.File] == nil {
			files = append(files, block.File)
		}
		span := strconv.Itoa(block.StartLine)
		if block.EndLine != block.StartLine {
			span += "-" + strconv.Itoa(block.EndLine)
		}
		byFile[block.File] = append(byFile[block.File], span)
	}
	sort.Strings(files)
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file+":"+strings.Join(byFile[file], ","))
	}
	return out
}
