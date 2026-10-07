package repository_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/adr"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

func TestMain(m *testing.M) { testsupport.SealedMain(m) }

var root = func() string {
	path, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	return path
}()

var recordsDir = filepath.Join(root, "docs", "site", "adr")

func records(t *testing.T) []adr.Record {
	t.Helper()
	loaded, err := adr.LoadAll(recordsDir)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// walk visits every file in the tree but dot directories, where agent worktrees
// and caches keep files that are not this checkout's, and build output.
func walk(t *testing.T, visit func(path string)) int {
	t.Helper()
	visited := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			name := entry.Name()
			if rel != "." && ((strings.HasPrefix(name, ".") && name != ".github") || (filepath.Dir(rel) == "." && (name == "site" || name == "dist"))) {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		visit(path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return visited
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // a path in the repository
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTheRepositorysRecordsLoad(t *testing.T) {
	t.Parallel()
	if got := records(t); len(got) < 20 {
		t.Fatalf("found %d records; the scan has stopped matching", len(got))
	}
}

func TestTheRecordIndexIsCurrent(t *testing.T) {
	t.Parallel()
	if read(t, filepath.Join(recordsDir, adr.IndexName)) != adr.RenderIndex(records(t)) {
		t.Fatal("docs/site/adr/index.md is stale; run `task docs:adr-index`")
	}
}

var adrMention = regexp.MustCompile(`\bADR-(\d{3})\b`)

func TestEveryADRMentionHasARecord(t *testing.T) {
	t.Parallel()
	numbers := map[string]bool{}
	for _, record := range records(t) {
		numbers[record.Label()[4:]] = true
	}
	textual := map[string]bool{".md": true, ".go": true, ".yml": true, ".yaml": true, ".json": true, ".env": true, "": true}
	var dangling []string
	visited := walk(t, func(path string) {
		if !textual[filepath.Ext(path)] || strings.HasPrefix(filepath.Base(path), "mattermost-") {
			return
		}
		for _, match := range adrMention.FindAllStringSubmatch(read(t, path), -1) {
			if !numbers[match[1]] {
				rel, _ := filepath.Rel(root, path)
				dangling = append(dangling, rel+" names ADR-"+match[1])
			}
		}
	})
	if visited < 30 {
		t.Fatalf("visited %d files; the walk has stopped matching", visited)
	}
	if len(dangling) > 0 {
		t.Fatal(strings.Join(dangling, "\n"))
	}
}

var mattermostRelease = regexp.MustCompile(`Mattermost(?: Server)? v?\d+\.\d+`)

func TestNoRecordNamesAMattermostVersion(t *testing.T) {
	t.Parallel()
	for _, record := range records(t) {
		if match := mattermostRelease.FindString(read(t, filepath.Join(recordsDir, record.File))); match != "" {
			t.Errorf("%s names %q; the supported releases live in the compose files under docker/ and the versions page (ADR-025)", record.File, match)
		}
	}
}

var listedTest = regexp.MustCompile("(?m)^- `(Test\\w+)`")

// governanceTests are the test functions in files named governance_test.go,
// which is where every governance test lives.
func governanceTests(t *testing.T) []string {
	t.Helper()
	var names []string
	walk(t, func(path string) {
		if filepath.Base(path) != "governance_test.go" {
			return
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") && fn.Name.Name != "TestMain" {
				names = append(names, fn.Name.Name)
			}
		}
	})
	slices.Sort(names)
	return names
}

func TestTheGovernanceRecordListsExactlyTheGovernanceTests(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join(recordsDir, "015-*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("found %v, %v; want ADR-015", matches, err)
	}
	var listed []string
	for _, match := range listedTest.FindAllStringSubmatch(read(t, matches[0]), -1) {
		listed = append(listed, match[1])
	}
	slices.Sort(listed)
	defined := governanceTests(t)
	if len(defined) == 0 {
		t.Fatal("found no governance tests; the scan has stopped matching")
	}
	if !slices.Equal(listed, defined) {
		t.Fatalf("ADR-015 lists:\n  %s\nthe governance_test.go files define:\n  %s",
			strings.Join(listed, "\n  "), strings.Join(defined, "\n  "))
	}
}

var pinnedAction = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40} # v\d+(\.\d+)*$`)

func TestEveryActionIsPinnedToACommit(t *testing.T) {
	t.Parallel()
	workflows, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, workflow := range workflows {
		for _, line := range strings.Split(read(t, workflow), "\n") {
			target, ok := strings.CutPrefix(strings.TrimPrefix(strings.TrimSpace(line), "- "), "uses:")
			if !ok {
				continue
			}
			target = strings.TrimSpace(target)
			if strings.HasPrefix(target, "./") {
				continue
			}
			found++
			if !pinnedAction.MatchString(target) {
				t.Errorf("%s: %s is not pinned to a release's commit with the release named beside it", filepath.Base(workflow), target)
			}
		}
	}
	if found == 0 {
		t.Fatal("found no actions in the workflows; the scan has stopped matching")
	}
}

var environmentName = regexp.MustCompile(`^MM_[A-Z0-9_]+$`)

// TestEveryVariableTheSourceNamesIsListed holds the seal to what mm-mcp reads:
// a variable the shipped code names and EnvironmentVariables leaves out would
// reach a unit test from the developer's shell (ADR-006).
func TestEveryVariableTheSourceNamesIsListed(t *testing.T) {
	t.Parallel()
	named := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					if value, err := strconv.Unquote(literal.Value); err == nil && environmentName.MatchString(value) {
						named[value] = true
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	listed := slices.Sorted(slices.Values(config.EnvironmentVariables))
	found := slices.Sorted(func(yield func(string) bool) {
		for name := range named {
			if !yield(name) {
				return
			}
		}
	})
	if !slices.Equal(found, listed) {
		t.Fatalf("the source names %v; config.EnvironmentVariables lists %v", found, listed)
	}
}

var documentedVariable = regexp.MustCompile("`(MM_[A-Z0-9_]+)`")

func TestTheConfigurationPageNamesEveryVariableAndOnlyThose(t *testing.T) {
	t.Parallel()
	documented := map[string]bool{}
	for _, match := range documentedVariable.FindAllStringSubmatch(read(t, filepath.Join(root, "docs", "site", "configuration.md")), -1) {
		documented[match[1]] = true
	}
	var names []string
	for name := range documented {
		names = append(names, name)
	}
	slices.Sort(names)
	if want := slices.Sorted(slices.Values(config.EnvironmentVariables)); !slices.Equal(names, want) {
		t.Fatalf("the configuration page documents %v; mm-mcp reads %v", names, want)
	}
}

// TestEveryTestPackageIsSealed fails a package under cmd/ or internal/ whose
// tests run without testsupport.SealedMain (ADR-006).
func TestEveryTestPackageIsSealed(t *testing.T) {
	t.Parallel()
	packages := map[string]bool{}
	sealed := map[string]bool{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return err
			}
			packages[filepath.Dir(path)] = true
			if strings.Contains(read(t, path), "testsupport.SealedMain(m)") {
				sealed[filepath.Dir(path)] = true
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(packages) < 5 {
		t.Fatalf("found %d test packages; the scan has stopped matching", len(packages))
	}
	for dir := range packages {
		if !sealed[dir] {
			rel, _ := filepath.Rel(root, dir)
			t.Errorf("%s has tests and no TestMain calling testsupport.SealedMain", rel)
		}
	}
}
