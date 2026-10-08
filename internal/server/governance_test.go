package server_test

import (
	"errors"
	"fmt"
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

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// Invariants every MCP tool is held to (ADR-021, ADR-015).

// writingConfig offers every tool: writes are allowed, and the server is local.
var writingConfig = config.Config{URL: "https://chat.example.com", Token: "t", AllowWrites: true, Local: true}

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

// toolKinds sorts the catalogue: tools that only read, tools that change
// Mattermost, and tools that write this machine's files.
func toolKinds() (reads, writes, locals []string) {
	for _, spec := range server.AllSpecs() {
		switch {
		case spec.Local:
			locals = append(locals, spec.Tool.Name)
		case spec.ReadOnly():
			reads = append(reads, spec.Tool.Name)
		default:
			writes = append(writes, spec.Tool.Name)
		}
	}
	return reads, writes, locals
}

func offeredNames(t *testing.T, cfg config.Config) []string {
	t.Helper()
	var out []string
	for _, tool := range listTools(t, cfg) {
		out = append(out, tool.Name)
	}
	slices.Sort(out)
	return out
}

func sorted(groups ...[]string) []string {
	return slices.Sorted(slices.Values(slices.Concat(groups...)))
}

func TestAllowingWritesAddsExactlyTheToolsThatWrite(t *testing.T) {
	t.Parallel()
	reads, writes, _ := toolKinds()
	if got, want := offeredNames(t, readOnlyConfig), sorted(reads); !slices.Equal(got, want) {
		t.Errorf("read-only server offers %v, want %v", got, want)
	}
	allowing := readOnlyConfig
	allowing.AllowWrites = true
	if got, want := offeredNames(t, allowing), sorted(reads, writes); !slices.Equal(got, want) {
		t.Errorf("writing server offers %v, want %v", got, want)
	}
}

func TestOnlyALocalServerOffersTheToolsThatWriteItsFiles(t *testing.T) {
	t.Parallel()
	reads, writes, locals := toolKinds()
	if len(locals) == 0 {
		t.Fatal("no tool is marked local; the check has nothing to hold")
	}
	local := readOnlyConfig
	local.Local = true
	if got, want := offeredNames(t, local), sorted(reads, locals); !slices.Equal(got, want) {
		t.Errorf("a local read-only server offers %v, want %v", got, want)
	}
	if got, want := offeredNames(t, writingConfig), sorted(reads, writes, locals); !slices.Equal(got, want) {
		t.Errorf("a local writing server offers %v, want %v", got, want)
	}
}

// TestAToolThatDoesNotAskChangesNothingOfOthers holds the tools that change
// Mattermost without asking to what ADR-021 lets them be: neither read-only
// nor destructive.
func TestAToolThatDoesNotAskChangesNothingOfOthers(t *testing.T) {
	t.Parallel()
	found := 0
	for _, spec := range server.AllSpecs() {
		if spec.Unasked == "" {
			continue
		}
		found++
		hints := spec.Tool.Annotations
		switch {
		case spec.ReadOnly() || spec.Local:
			t.Errorf("%s says why it does not ask, but it is not a tool that changes Mattermost", spec.Tool.Name)
		case hints.DestructiveHint == nil || *hints.DestructiveHint:
			t.Errorf("%s does not ask, and may destroy something", spec.Tool.Name)
		}
	}
	if found == 0 {
		t.Fatal("no tool goes without asking; the check has nothing to hold")
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

var askRule = regexp.MustCompile(`"mcp__mattermost__([a-z_]+)"`)

// TestTheConfigurationPageAsksForEveryToolThatWouldAsk holds the Claude Code
// ask rules the configuration page gives, for a server that leaves the asking
// to the client, to the tools that ask (ADR-033): one left out would post with
// nobody asked.
func TestTheConfigurationPageAsksForEveryToolThatWouldAsk(t *testing.T) {
	t.Parallel()
	page, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "site", "configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	var listed, asking []string
	for _, match := range askRule.FindAllStringSubmatch(string(page), -1) {
		listed = append(listed, match[1])
	}
	if len(listed) == 0 {
		t.Fatal("found no ask rule on the configuration page; the scan has stopped matching")
	}
	for _, spec := range server.AllSpecs() {
		if spec.Asks() {
			asking = append(asking, spec.Tool.Name)
		}
	}
	slices.Sort(listed)
	slices.Sort(asking)
	if !slices.Equal(listed, asking) {
		t.Fatalf("the configuration page asks for %s; the tools that ask are %s", strings.Join(listed, ", "), strings.Join(asking, ", "))
	}
}

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

// TestEveryToolThatWritesAsksFirst calls each write tool from a client that
// cannot be asked. A tool that asks refuses it with the missing capability
// error before reaching Mattermost; one that does not reaches for the network,
// which the unit tests block, and fails some other way (ADR-021).
func TestEveryToolThatWritesAsksFirst(t *testing.T) {
	t.Parallel()
	callEveryAskingTool(t, writingConfig, func(tool *mcp.Tool, err error) {
		var refused *jsonrpc.Error
		if !errors.As(err, &refused) || refused.Code != mcp.CodeMissingRequiredClientCapabilities {
			t.Errorf("%s, called by a client that cannot be asked, answered %v; want the missing capability error", tool.Name, err)
		}
	})
}

// TestAServerThatDoesNotAskLeavesTheCallToTheClient holds what a server that
// leaves asking to the client does with each tool that would ask (ADR-033): it
// lets a client that cannot be asked through to Mattermost, which the unit
// tests block, and tells the model that nobody is asked. Only
// MM_MCP_FORCE_HUMAN_IN_THE_LOOP_IN_CLAUDE_CODE marks those tools for Claude Code to ask on
// every call, whatever its rules allow. Without it, the client's rules decide,
// so an agent may be allowed to write with nobody watching; with mm-mcp asking,
// nothing is marked, or the person would be asked twice.
func TestAServerThatDoesNotAskLeavesTheCallToTheClient(t *testing.T) {
	t.Parallel()
	asking := map[string]bool{}
	for _, spec := range server.AllSpecs() {
		asking[spec.Tool.Name] = spec.Asks()
	}
	for _, mode := range []struct {
		name        string
		skip, force bool
	}{{"mm-mcp asking", false, false}, {"not asking", true, false}, {"forcing a human in the loop", true, true}} {
		cfg := writingConfig
		cfg.SkipAsking, cfg.ForceHumanInTheLoopInClaudeCode = mode.skip, mode.force
		if mode.skip {
			callEveryAskingTool(t, cfg, func(tool *mcp.Tool, err error) {
				var refused *jsonrpc.Error
				if err == nil || errors.As(err, &refused) && refused.Code == mcp.CodeMissingRequiredClientCapabilities {
					t.Errorf("%s, %s, answered %v; want it to reach for Mattermost", tool.Name, mode.name, err)
				}
				if !strings.Contains(tool.Description, "configured not to ask") {
					t.Errorf("%s, %s, does not tell the model that mm-mcp asks nothing: %s", tool.Name, mode.name, tool.Description)
				}
				if denied := strings.Contains(tool.Description, "if they deny it"); denied != mode.force {
					t.Errorf("%s, %s, tells the model what to do when the person denies the call: %v", tool.Name, mode.name, denied)
				}
			})
		}
		for _, tool := range listTools(t, cfg) {
			if !asking[tool.Name] && strings.Contains(tool.Description, "configured not to ask") {
				t.Errorf("%s never asks, and says it would", tool.Name)
			}
			marked := tool.Meta[server.RequiresUserInteraction] == true
			if want := asking[tool.Name] && mode.force; marked != want {
				t.Errorf("%s, %s, has its client ask on every call: %v; want %v", tool.Name, mode.name, marked, want)
			}
		}
	}
}

// callEveryAskingTool calls each tool that asks on the server cfg describes,
// from a client that cannot be asked, with "x" for each argument it requires,
// and gives check the tool as listed and what the call answered.
func callEveryAskingTool(t *testing.T, cfg config.Config, check func(tool *mcp.Tool, err error)) {
	t.Helper()
	session := connect(t, cfg)
	unasked := map[string]bool{}
	for _, spec := range server.AllSpecs() {
		unasked[spec.Tool.Name] = spec.Unasked != "" || spec.Local
	}
	written := 0
	for _, tool := range listTools(t, cfg) {
		if tool.Annotations.ReadOnlyHint || unasked[tool.Name] {
			continue
		}
		written++
		arguments := map[string]any{}
		schema := tool.InputSchema.(map[string]any)
		required, _ := schema["required"].([]any)
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range required {
			property, _ := properties[name.(string)].(map[string]any)
			if types := fmt.Sprint(property["type"]); strings.Contains(types, "array") {
				arguments[name.(string)] = []string{"x", "y"}
			} else {
				arguments[name.(string)] = "x"
			}
		}
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool.Name, Arguments: arguments})
		if err == nil && result.IsError {
			err = errors.New(result.Content[0].(*mcp.TextContent).Text)
		}
		check(tool, err)
	}
	if written == 0 {
		t.Fatal("the writing server offers no tool that writes; the check has nothing to hold")
	}
}

// bounded are the tools whose list is as long as their input asks for, or as
// the tool itself fixes, and so needs no paging.
var bounded = map[string]string{
	"diagnose":   "one entry for each check it makes, a fixed set",
	"get_users":  "one user for each reference given, at most 100",
	"get_status": "one status for each username given, at most 100",
}

// TestEveryListPagesByCursor holds every tool that answers with a list to the
// one way of paging (ADR-032): it takes cursor, and answers with next_cursor.
// A list is an answer holding an array of objects and no id of its own: a
// single post or draft names itself by its id, and its files are its own.
func TestEveryListPagesByCursor(t *testing.T) {
	t.Parallel()
	lists := 0
	for _, tool := range listTools(t, writingConfig) {
		output, _ := tool.OutputSchema.(map[string]any)
		properties, _ := output["properties"].(map[string]any)
		listing := false
		for _, property := range properties {
			described := property.(map[string]any)
			items, _ := described["items"].(map[string]any)
			if strings.Contains(fmt.Sprint(described["type"]), "array") && strings.Contains(fmt.Sprint(items["type"]), "object") {
				listing = true
			}
		}
		if _, single := properties["id"]; !listing || single {
			continue
		}
		lists++
		if reason, ok := bounded[tool.Name]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s is exempt from paging without a reason", tool.Name)
			}
			continue
		}
		input, _ := tool.InputSchema.(map[string]any)
		arguments, _ := input["properties"].(map[string]any)
		if _, ok := arguments["cursor"]; !ok {
			t.Errorf("%s answers with a list and takes no cursor", tool.Name)
		}
		if _, ok := arguments["limit"]; !ok {
			t.Errorf("%s answers with a list and takes no limit", tool.Name)
		}
		if _, ok := properties["next_cursor"]; !ok {
			t.Errorf("%s answers with a list and no next_cursor", tool.Name)
		}
	}
	if lists == 0 {
		t.Fatal("no tool answers with a list; the check has stopped matching")
	}
}

// toolKind is what a tool does beyond reading, as the tools page marks it:
// asks the person first, changes only what is theirs, or reaches their disk.
func toolKind(spec server.Spec) string {
	switch {
	case spec.Local:
		return "disk"
	case spec.Asks():
		return "asks"
	case !spec.ReadOnly():
		return "yours"
	}
	return ""
}

// TestTheToolsPageLinksAndMarksEveryTool holds the tools page's overview to
// the tools: each has a chip that links to its entry, the entry has the
// anchor, both say what kind of tool it is, and the counts add up. A tool
// added, or one that starts to ask, without its page changing would show a
// person the wrong thing.
func TestTheToolsPageLinksAndMarksEveryTool(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "site", "tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	counts := map[string]int{}
	for _, spec := range server.AllSpecs() {
		name, kind := spec.Tool.Name, toolKind(spec)
		counts[kind]++
		entry, chip := "{ #"+name+" }", "[`"+name+"`](#"+name+"){ .mm-chip }"
		if kind != "" {
			entry = "{ #" + name + " .mm-" + kind + " }"
			chip = "[`" + name + "`](#" + name + "){ .mm-chip .mm-chip--" + kind + " }"
		}
		if !regexp.MustCompile("(?m)^`" + regexp.QuoteMeta(name) + "`: .* " + regexp.QuoteMeta(entry) + "$").MatchString(page) {
			t.Errorf("the entry for %s does not end with %s", name, entry)
		}
		if strings.Count(page, chip) != 1 {
			t.Errorf("the overview does not show %s once as %s", name, chip)
		}
	}
	for _, stat := range []struct {
		label string
		count int
	}{
		{"tools", len(server.AllSpecs())},
		{"only read", counts[""]},
		{"ask you first", counts["asks"]},
		{"yours alone, not asked", counts["yours"]},
		{"saves to your disk", counts["disk"]},
	} {
		if want := fmt.Sprintf("<strong>%d</strong> %s", stat.count, stat.label); !strings.Contains(page, want) {
			t.Errorf("the counts at the top do not say %q", want)
		}
	}
}
