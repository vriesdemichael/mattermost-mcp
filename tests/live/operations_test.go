//go:build live

package live

import (
	"flag"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/apisurface"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// The live half of ADR-028: every request a tool sends while it is called is
// matched to its operation in the newest specification, and must be one the
// tool declares in its Uses; and every operation a tool declares must be seen
// at least once across the suite.

var newest *apisurface.Spec

// recorder sits in front of the transport of the client one MCP session acts
// through, and keeps the requests sent since it was last emptied.
type recorder struct {
	inner    http.RoundTripper
	mu       sync.Mutex
	requests []string
}

func (r *recorder) RoundTrip(request *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request.Method+" "+request.URL.Path)
	r.mu.Unlock()
	return r.inner.RoundTrip(request)
}

func (r *recorder) take() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	taken := r.requests
	r.requests = nil
	return taken
}

var (
	recorders sync.Map // *mcp.ClientSession -> *recorder

	observedMu sync.Mutex
	observed   = map[string]map[string]bool{} // tool -> operationId -> seen
)

// callTool calls a tool through its session, and fails the test when the tool
// reached Mattermost through an operation it does not declare.
func callTool(t *testing.T, session *mcp.ClientSession, params *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	value, ok := recorders.Load(session)
	if !ok {
		t.Fatal("the session was not made by mcpAs, so its requests are not recorded")
	}
	rec := value.(*recorder)
	rec.take()
	result, err := session.CallTool(t.Context(), params)
	if err != nil {
		t.Fatal(err)
	}
	declared := declaredOperations(params.Name)
	for _, request := range rec.take() {
		method, path, _ := strings.Cut(request, " ")
		op, ok := newest.Match(method, path)
		switch {
		case !ok:
			t.Errorf("%s sent %s, which no operation of the %s specification matches", params.Name, request, newest.Release)
		case !slices.Contains(declared, op.ID):
			t.Errorf("%s sent %s, operation %s, which it does not declare in its Uses (ADR-028)", params.Name, request, op.ID)
		default:
			observedMu.Lock()
			if observed[params.Name] == nil {
				observed[params.Name] = map[string]bool{}
			}
			observed[params.Name][op.ID] = true
			observedMu.Unlock()
		}
	}
	return result
}

func declaredOperations(tool string) []string {
	var ids []string
	for _, spec := range server.AllSpecs() {
		if spec.Tool.Name == tool {
			for _, use := range spec.Uses {
				ids = append(ids, use.Operation)
			}
		}
	}
	return ids
}

// wholeSuite reports whether every live test ran, which is when an operation
// no test called means one is missing rather than filtered out.
func wholeSuite() bool {
	run := flag.Lookup("test.run")
	return run == nil || run.Value.String() == ""
}

func declaredButNeverCalled() []string {
	observedMu.Lock()
	defer observedMu.Unlock()
	var missing []string
	for _, spec := range server.AllSpecs() {
		for _, use := range spec.Uses {
			if !observed[spec.Tool.Name][use.Operation] {
				missing = append(missing, spec.Tool.Name+" declares "+use.Operation)
			}
		}
	}
	return missing
}
