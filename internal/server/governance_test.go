package server_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/vriesdemichael/mattermost-mcp/internal/config"
	"github.com/vriesdemichael/mattermost-mcp/internal/server"
)

// Invariants every MCP tool is held to (ADR-021, ADR-015).

var writingConfig = config.Config{URL: "https://chat.example.com", Token: "t", AllowWrites: true}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEveryToolDeclaresItsHintsAndTitle(t *testing.T) {
	t.Parallel()
	tools := listTools(t, writingConfig)
	if len(tools) == 0 {
		t.Fatal("the server offers no tools; the check has nothing to hold")
	}
	for _, tool := range tools {
		hints := tool.Annotations
		switch {
		case tool.Title == "":
			t.Errorf("%s has no title", tool.Name)
		case hints == nil:
			t.Errorf("%s declares no annotations", tool.Name)
		case hints.DestructiveHint == nil || hints.OpenWorldHint == nil:
			t.Errorf("%s leaves destructiveHint or openWorldHint unset", tool.Name)
		}
	}
}

func TestNoToolIsOpenWorld(t *testing.T) {
	t.Parallel()
	for _, tool := range listTools(t, writingConfig) {
		if tool.Annotations != nil && tool.Annotations.OpenWorldHint != nil && *tool.Annotations.OpenWorldHint {
			t.Errorf("%s is annotated open-world", tool.Name)
		}
	}
}

func TestAReadOnlyServerListsOnlyReadOnlyTools(t *testing.T) {
	t.Parallel()
	tools := listTools(t, readOnlyConfig)
	if len(tools) == 0 {
		t.Fatal("the read-only server lists no tools at all")
	}
	for _, tool := range tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is offered by a read-only server", tool.Name)
		}
	}
}

func TestAllowingWritesAddsExactlyTheToolsThatWrite(t *testing.T) {
	t.Parallel()
	var readNames, writeNames []string
	for _, spec := range server.AllSpecs() {
		if spec.ReadOnly() {
			readNames = append(readNames, spec.Tool.Name)
		} else {
			writeNames = append(writeNames, spec.Tool.Name)
		}
	}
	names := func(cfg config.Config) []string {
		var out []string
		for _, tool := range listTools(t, cfg) {
			out = append(out, tool.Name)
		}
		slices.Sort(out)
		return out
	}
	want := slices.Sorted(slices.Values(readNames))
	if got := names(readOnlyConfig); !slices.Equal(got, want) {
		t.Errorf("read-only server offers %v, want %v", got, want)
	}
	want = slices.Sorted(slices.Values(append(readNames, writeNames...)))
	if got := names(writingConfig); !slices.Equal(got, want) {
		t.Errorf("writing server offers %v, want %v", got, want)
	}
}

// toolsCalledByLiveTests reads the tool names the live suite passes to
// CallTool, as the Name of an mcp.CallToolParams literal.
func toolsCalledByLiveTests(t *testing.T) map[string]bool {
	t.Helper()
	called := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "tests", "live", "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := literal.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "CallToolParams" {
				return true
			}
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok || field.Key.(*ast.Ident).Name != "Name" {
					continue
				}
				if value, ok := field.Value.(*ast.BasicLit); ok && value.Kind == token.STRING {
					name, _ := strconv.Unquote(value.Value)
					called[name] = true
				}
			}
			return true
		})
	}
	return called
}

func TestEveryToolIsCalledByALiveTest(t *testing.T) {
	t.Parallel()
	called := toolsCalledByLiveTests(t)
	// A scan that has stopped matching finds nothing; refuse to pass on that.
	if len(called) == 0 {
		t.Fatal("found no mcp.CallToolParams{Name: \"...\"} in tests/live; the scan has stopped matching")
	}
	for _, spec := range server.AllSpecs() {
		if !called[spec.Tool.Name] {
			t.Errorf("no live test calls %s (ADR-004)", spec.Tool.Name)
		}
	}
}

var documentedTool = regexp.MustCompile("(?m)^`([a-z_]+)`: ")

func TestTheToolsPageDocumentsEveryToolAndOnlyThose(t *testing.T) {
	t.Parallel()
	page, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "site", "tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	var documented, offered []string
	for _, match := range documentedTool.FindAllStringSubmatch(string(page), -1) {
		documented = append(documented, match[1])
	}
	if len(documented) == 0 {
		t.Fatal("found no tool on the tools page; the scan has stopped matching")
	}
	for _, spec := range server.AllSpecs() {
		offered = append(offered, spec.Tool.Name)
	}
	slices.Sort(documented)
	slices.Sort(offered)
	if !slices.Equal(documented, offered) {
		t.Fatalf("the tools page documents %s; the server has %s",
			strings.Join(documented, ", "), strings.Join(offered, ", "))
	}
}
