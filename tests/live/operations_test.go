//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
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
// tool declares in its Uses, sending only parameters the tool accounts for and
// none it says it omits; and every operation a tool declares must be seen at
// least once across the suite.

var newest *apisurface.Spec

// sent is one request as the recorder saw it.
type sent struct {
	method, path string
	query        url.Values
	// body is the top-level fields of a JSON object body that hold a value
	// other than their type's zero, which is what a request sets.
	body []string
}

func (s sent) String() string { return s.method + " " + s.path }

// recorder sits in front of the transport of the client one MCP session acts
// through, and keeps the requests sent since it was last emptied.
type recorder struct {
	inner    http.RoundTripper
	mu       sync.Mutex
	requests []sent
	// last is the operations the session's last tool call reached, in order.
	last []string
}

func (r *recorder) RoundTrip(request *http.Request) (*http.Response, error) {
	record := sent{method: request.Method, path: request.URL.Path, query: request.URL.Query()}
	if request.Body != nil && strings.HasPrefix(request.Header.Get("Content-Type"), "application/json") {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		_ = request.Body.Close()
		request.Body = io.NopCloser(bytes.NewReader(raw))
		record.body = setFields(raw)
	}
	r.mu.Lock()
	r.requests = append(r.requests, record)
	r.mu.Unlock()
	return r.inner.RoundTrip(request)
}

// setFields is the top-level fields of a JSON object that hold a value other
// than their type's zero: null, false, 0, "", [] and {} set nothing.
func setFields(raw []byte) []string {
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	var fields []string
	for name, value := range object {
		if value != nil && !reflect.ValueOf(value).IsZero() && !(isCollection(value) && reflect.ValueOf(value).Len() == 0) {
			fields = append(fields, name)
		}
	}
	slices.Sort(fields)
	return fields
}

func isCollection(value any) bool {
	kind := reflect.ValueOf(value).Kind()
	return kind == reflect.Slice || kind == reflect.Map
}

func (r *recorder) take() []sent {
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

// callTool calls a tool through its session, and fails the test when the call
// fails or the tool reached Mattermost through an operation, or with a
// parameter, it does not account for.
func callTool(t *testing.T, session *mcp.ClientSession, params *mcp.CallToolParams) *mcp.CallToolResult {
	t.Helper()
	result, err := tryTool(t, session, params)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// backgroundOperations are sent by a tool after its call has returned: the
// typing indicator is kept up every few seconds, during whatever call comes
// next. They are checked when the tool that starts them is called.
var backgroundOperations = map[string]string{"PublishUserTyping": "typing"}

// tryTool is callTool for a call that may fail as a whole, such as one refused
// for a capability the client lacks; it returns that failure.
func tryTool(t *testing.T, session *mcp.ClientSession, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	t.Helper()
	value, ok := recorders.Load(session)
	if !ok {
		t.Fatal("the session was not made by mcpAs, so its requests are not recorded")
	}
	rec := value.(*recorder)
	rec.take()
	result, err := session.CallTool(t.Context(), params)
	uses := declaredUses(params.Name)
	var reached []string
	for _, request := range rec.take() {
		op, ok := newest.Match(request.method, request.path)
		if !ok {
			t.Errorf("%s sent %s, which no operation of the %s specification matches", params.Name, request, newest.Release)
			continue
		}
		if starter, background := backgroundOperations[op.ID]; background && starter != params.Name {
			continue
		}
		reached = append(reached, op.ID)
		use, declared := uses[op.ID]
		if !declared {
			t.Errorf("%s sent %s, operation %s, which it does not declare in its Uses (ADR-028)", params.Name, request, op.ID)
			continue
		}
		for _, problem := range unaccounted(use, op, request) {
			t.Errorf("%s sent %s %s (ADR-028)", params.Name, request, problem)
		}
		observedMu.Lock()
		if observed[params.Name] == nil {
			observed[params.Name] = map[string]bool{}
		}
		observed[params.Name][op.ID] = true
		observedMu.Unlock()
	}
	rec.mu.Lock()
	rec.last = reached
	rec.mu.Unlock()
	return result, err
}

// unaccounted is what a request sent that its tool does not account for: a
// query parameter set to a value other than its zero, or a body field the
// specification documents, that the tool either never names or says it
// omits. A body field the specification does not document is Mattermost's
// client's own business.
func unaccounted(use server.Use, op apisurface.Operation, request sent) []string {
	var problems []string
	for name, values := range request.query {
		if slices.ContainsFunc(values, func(v string) bool { return v != "" && v != "0" && v != "false" }) {
			coverage, named := use.Params[name]
			switch {
			case !named:
				problems = append(problems, fmt.Sprintf("with the query parameter %s=%s, which its Uses do not account for", name, strings.Join(values, ",")))
			case coverage.How == "omitted":
				problems = append(problems, fmt.Sprintf("with the query parameter %s=%s, which it says it omits", name, strings.Join(values, ",")))
			}
		}
	}
	for _, field := range request.body {
		if !slices.Contains(op.BodyFields, field) {
			continue
		}
		if coverage := use.Params["body."+field]; coverage.How == "omitted" {
			problems = append(problems, fmt.Sprintf("with the body field %s set, which it says it omits", field))
		}
	}
	return problems
}

// declaredUses is a tool's Uses by operation.
func declaredUses(tool string) map[string]server.Use {
	uses := map[string]server.Use{}
	for _, spec := range server.AllSpecs() {
		if spec.Tool.Name == tool {
			for _, use := range spec.Uses {
				uses[use.Operation] = use
			}
		}
	}
	return uses
}

// wholeSuite reports whether every live test ran, which is when an operation
// no test called means one is missing rather than filtered out.
func wholeSuite() bool {
	for _, name := range []string{"test.run", "test.skip"} {
		if filter := flag.Lookup(name); filter != nil && filter.Value.String() != "" {
			return false
		}
	}
	return true
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

// reachedLast is how many times the session's last tool call reached an
// operation, for a test of how a tool pages through Mattermost.
func reachedLast(t *testing.T, session *mcp.ClientSession, operation string) int {
	t.Helper()
	value, ok := recorders.Load(session)
	if !ok {
		t.Fatal("the session was not made by mcpAs, so its requests are not recorded")
	}
	rec := value.(*recorder)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	count := 0
	for _, reached := range rec.last {
		if reached == operation {
			count++
		}
	}
	return count
}
