// Package apisurface reads what each supported Mattermost release says and does
// at its API, from the files under openapi/ (ADR-026, ADR-027): the
// specification, for every parameter an operation takes and the release it
// arrived in, and the route table, for what the release's router really serves.
//
// It answers three questions the governance tests and the live suite ask: which
// operation a request was, which parameters an operation has, and how an
// operation differs between the oldest and the newest supported release.
package apisurface

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Operation is one operation of the specification.
type Operation struct {
	ID     string
	Method string
	Path   string
	// MinVersion is the release the specification says it arrived in, when it says.
	MinVersion string
	// Params are the path, query and header parameters, by name.
	Params map[string]Param
	// BodyFields are the top-level fields of a JSON request body.
	BodyFields []string
}

// Param is one parameter of an operation.
type Param struct {
	Name       string
	In         string
	MinVersion string
}

// Spec is one release's specification.
type Spec struct {
	Release    string
	operations []Operation
	byID       map[string]Operation
}

// Routes is one release's route table.
type Routes struct {
	Release string
	served  map[string]bool
}

// minVersion reads the specification's "__Minimum server version__: 11.9.0",
// which it writes with Markdown around it in a handful of ways.
var minVersion = regexp.MustCompile(`(?i)minimum server version[^0-9\n]{0,8}(\d+\.\d+(?:\.\d+)?)`)

var methods = []string{"get", "put", "post", "delete", "patch"}

// LoadSpec reads a vendored specification.
func LoadSpec(path string) (*Spec, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a vendored file in the repository
	if err != nil {
		return nil, err
	}
	var document struct {
		Info       map[string]any                        `json:"info"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]schema `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	release, _ := document.Info["x-mattermost-release"].(string)
	spec := &Spec{Release: release, byID: map[string]Operation{}}
	for path, item := range document.Paths {
		var shared []parameter
		if raw, ok := item["parameters"]; ok {
			_ = json.Unmarshal(raw, &shared)
		}
		for _, method := range methods {
			raw, ok := item[method]
			if !ok {
				continue
			}
			var op operation
			if err := json.Unmarshal(raw, &op); err != nil {
				return nil, fmt.Errorf("%s %s %s: %w", path, method, op.ID, err)
			}
			operation := Operation{
				ID:         op.ID,
				Method:     strings.ToUpper(method),
				Path:       path,
				MinVersion: firstVersion(op.Description),
				Params:     map[string]Param{},
				BodyFields: bodyFields(op.RequestBody, document.Components.Schemas),
			}
			for _, p := range append(slices.Clone(shared), op.Parameters...) {
				if p.Name == "" {
					continue
				}
				operation.Params[p.Name] = Param{Name: p.Name, In: p.In, MinVersion: firstVersion(p.Description)}
			}
			// The path names its own parameters, whether or not the specification
			// declares them.
			for _, name := range templateNames(path) {
				if _, ok := operation.Params[name]; !ok {
					operation.Params[name] = Param{Name: name, In: "path"}
				}
			}
			spec.operations = append(spec.operations, operation)
			if operation.ID != "" {
				spec.byID[operation.ID] = operation
			}
		}
	}
	sort.Slice(spec.operations, func(i, j int) bool {
		return spec.operations[i].Path+spec.operations[i].Method < spec.operations[j].Path+spec.operations[j].Method
	})
	return spec, nil
}

type operation struct {
	ID          string          `json:"operationId"`
	Description string          `json:"description"`
	Parameters  []parameter     `json:"parameters"`
	RequestBody json.RawMessage `json:"requestBody"`
}

type parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Description string `json:"description"`
}

type schema struct {
	Ref        string            `json:"$ref"`
	Properties map[string]schema `json:"properties"`
	AllOf      []schema          `json:"allOf"`
}

func bodyFields(raw json.RawMessage, schemas map[string]schema) []string {
	if len(raw) == 0 {
		return nil
	}
	var body struct {
		Content map[string]struct {
			Schema schema `json:"schema"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return nil
	}
	content, ok := body.Content["application/json"]
	if !ok {
		return nil
	}
	fields := map[string]bool{}
	collect(content.Schema, schemas, fields, 0)
	return slices.Sorted(func(yield func(string) bool) {
		for name := range fields {
			if !yield(name) {
				return
			}
		}
	})
}

func collect(s schema, schemas map[string]schema, into map[string]bool, depth int) {
	if depth > 5 {
		return
	}
	if name, ok := strings.CutPrefix(s.Ref, "#/components/schemas/"); ok {
		collect(schemas[name], schemas, into, depth+1)
		return
	}
	for name := range s.Properties {
		into[name] = true
	}
	for _, part := range s.AllOf {
		collect(part, schemas, into, depth+1)
	}
}

var templateName = regexp.MustCompile(`\{([^{}]+)\}`)

func templateNames(path string) []string {
	var names []string
	for _, m := range templateName.FindAllStringSubmatch(path, -1) {
		names = append(names, m[1])
	}
	return names
}

func firstVersion(text string) string {
	if m := minVersion.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// Operation is the operation with the given operationId.
func (s *Spec) Operation(id string) (Operation, bool) {
	op, ok := s.byID[id]
	return op, ok
}

// At is the operation at a method and a path template.
func (s *Spec) At(method, path string) (Operation, bool) {
	for _, op := range s.operations {
		if op.Method == method && op.Path == path {
			return op, true
		}
	}
	return Operation{}, false
}

// Match is the operation a concrete request was: the method, and a path whose
// every segment equals the template's or fills one of its parameters. Where two
// templates fit, the one with more literal segments wins, as a router decides.
func (s *Spec) Match(method, path string) (Operation, bool) {
	best, bestLiterals, found := Operation{}, -1, false
	for _, op := range s.operations {
		if op.Method != method {
			continue
		}
		if literals, ok := fits(op.Path, path); ok && literals > bestLiterals {
			best, bestLiterals, found = op, literals, true
		}
	}
	return best, found
}

func fits(template, path string) (int, bool) {
	want := strings.Split(strings.Trim(template, "/"), "/")
	got := strings.Split(strings.Trim(path, "/"), "/")
	if len(want) != len(got) {
		return 0, false
	}
	literals := 0
	for i := range want {
		if strings.HasPrefix(want[i], "{") && strings.HasSuffix(want[i], "}") {
			if got[i] == "" {
				return 0, false
			}
			continue
		}
		if want[i] != got[i] {
			return 0, false
		}
		literals++
	}
	return literals, true
}

// LoadRoutes reads a vendored route table.
func LoadRoutes(path string) (*Routes, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // a vendored file in the repository
	if err != nil {
		return nil, err
	}
	var table struct {
		Release string `json:"release"`
		Routes  []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(raw, &table); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	routes := &Routes{Release: table.Release, served: map[string]bool{}}
	for _, r := range table.Routes {
		routes.served[r.Method+" "+shape(r.Path)] = true
	}
	return routes, nil
}

// Serves reports whether the release's router serves a method and path
// template. Templates are compared by shape, so {userId} and {user_id} in the
// same place are the same route.
func (r *Routes) Serves(method, path string) bool {
	return r.served[method+" "+shape(path)]
}

func shape(path string) string {
	return templateName.ReplaceAllString(path, "{}")
}

// Differences lists how an operation of the newest release differs in an older
// one: whether the older release serves it and documents it, and which of its
// parameters and body fields it lacks or the specification says arrived later.
func Differences(newest Operation, older *Spec, olderRoutes *Routes) []string {
	var found []string
	if !olderRoutes.Serves(newest.Method, newest.Path) {
		found = append(found, fmt.Sprintf("%s does not serve %s %s", olderRoutes.Release, newest.Method, newest.Path))
	}
	if after(newest.MinVersion, older.Release) {
		found = append(found, fmt.Sprintf("the operation arrived in %s, after %s", newest.MinVersion, older.Release))
	}
	old, documented := older.At(newest.Method, newest.Path)
	if !documented {
		found = append(found, fmt.Sprintf("the %s specification does not document %s %s", older.Release, newest.Method, newest.Path))
	}
	for _, name := range slices.Sorted(keys(newest.Params)) {
		param := newest.Params[name]
		if after(param.MinVersion, older.Release) {
			found = append(found, fmt.Sprintf("parameter %s arrived in %s, after %s", name, param.MinVersion, older.Release))
		}
		if documented {
			if _, ok := old.Params[name]; !ok {
				found = append(found, fmt.Sprintf("the %s specification has no parameter %s", older.Release, name))
			}
		}
	}
	if documented {
		for _, field := range newest.BodyFields {
			if !slices.Contains(old.BodyFields, field) {
				found = append(found, fmt.Sprintf("the %s specification has no body field %s", older.Release, field))
			}
		}
	}
	return found
}

func keys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// after reports whether release a is later than release b; an empty a is not.
func after(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	pa, pb := parts(a), parts(b)
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parts(release string) [3]int {
	var out [3]int
	for i, field := range strings.SplitN(release, ".", 3) {
		out[i], _ = strconv.Atoi(field)
	}
	return out
}
