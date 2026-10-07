package server_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/apisurface"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// What every tool calls, held to what each supported release says and serves
// (ADR-027, ADR-028).

type surface struct {
	latest, esr             *apisurface.Spec
	latestRoutes, esrRoutes *apisurface.Routes
}

func loadSurface(t *testing.T) surface {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "openapi")
	var s surface
	var err error
	if s.latest, err = apisurface.LoadSpec(filepath.Join(dir, "mattermost-latest.json")); err != nil {
		t.Fatal(err)
	}
	if s.esr, err = apisurface.LoadSpec(filepath.Join(dir, "mattermost-esr.json")); err != nil {
		t.Fatal(err)
	}
	if s.latestRoutes, err = apisurface.LoadRoutes(filepath.Join(dir, "routes-latest.json")); err != nil {
		t.Fatal(err)
	}
	if s.esrRoutes, err = apisurface.LoadRoutes(filepath.Join(dir, "routes-esr.json")); err != nil {
		t.Fatal(err)
	}
	return s
}

// inputArguments are the properties of a tool's input schema.
func inputArguments(t *testing.T, spec server.Spec) []string {
	t.Helper()
	raw, err := json.Marshal(spec.Tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range schema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// registered gives each spec its input schema, which the SDK derives when the
// tool is added to a server.
func registered(t *testing.T) []server.Spec {
	t.Helper()
	specs := server.AllSpecs()
	tools := map[string]bool{}
	for _, tool := range listTools(t, writingConfig) {
		tools[tool.Name] = true
	}
	for _, spec := range specs {
		if !tools[spec.Tool.Name] {
			t.Fatalf("%s is in the catalogue but not offered by a writing server", spec.Tool.Name)
		}
	}
	return specs
}

func TestEveryToolDeclaresTheOperationsItCalls(t *testing.T) {
	t.Parallel()
	s := loadSurface(t)
	for _, spec := range registered(t) {
		if len(spec.Uses) == 0 {
			t.Errorf("%s declares no operation it calls", spec.Tool.Name)
		}
		for _, use := range spec.Uses {
			op, ok := s.latest.Operation(use.Operation)
			switch {
			case !ok:
				t.Errorf("%s calls %s, which the %s specification does not have", spec.Tool.Name, use.Operation, s.latest.Release)
			case !s.latestRoutes.Serves(op.Method, op.Path):
				t.Errorf("%s calls %s, %s %s, which %s does not serve", spec.Tool.Name, use.Operation, op.Method, op.Path, s.latestRoutes.Release)
			}
		}
	}
}

func TestEveryParameterOfACalledOperationIsAccountedFor(t *testing.T) {
	t.Parallel()
	s := loadSurface(t)
	for _, spec := range registered(t) {
		for _, use := range spec.Uses {
			op, ok := s.latest.Operation(use.Operation)
			if !ok {
				continue // TestEveryToolDeclaresTheOperationsItCalls names it
			}
			want := map[string]bool{}
			for name := range op.Params {
				want[name] = true
			}
			for _, field := range op.BodyFields {
				want["body."+field] = true
			}
			for name := range want {
				if _, ok := use.Params[name]; !ok {
					t.Errorf("%s says nothing about %s's parameter %s; expose it, fix it or omit it with a reason", spec.Tool.Name, use.Operation, name)
				}
			}
			for name, coverage := range use.Params {
				if !want[name] {
					t.Errorf("%s accounts for %s, which %s does not have", spec.Tool.Name, name, use.Operation)
				}
				switch coverage.How {
				case "exposed":
					if coverage.Arg == "" {
						t.Errorf("%s exposes %s.%s through no argument", spec.Tool.Name, use.Operation, name)
					}
				case "fixed", "omitted":
					if strings.TrimSpace(coverage.Reason) == "" {
						t.Errorf("%s %s %s.%s without a reason", spec.Tool.Name, coverage.How, use.Operation, name)
					}
				default:
					t.Errorf("%s: %s.%s is neither exposed, fixed nor omitted", spec.Tool.Name, use.Operation, name)
				}
			}
		}
	}
}

func TestEveryToolArgumentSetsAParameterItCalls(t *testing.T) {
	t.Parallel()
	for _, spec := range registered(t) {
		exposed := map[string]bool{}
		for _, use := range spec.Uses {
			for _, coverage := range use.Params {
				if coverage.How == "exposed" {
					exposed[coverage.Arg] = true
				}
			}
		}
		arguments := inputArguments(t, specWithSchema(t, spec.Tool.Name))
		for _, arg := range arguments {
			shapes, shaping := spec.Shapes[arg]
			switch {
			case exposed[arg] && shaping:
				t.Errorf("%s both sets a parameter with %s and names it as shaping the answer", spec.Tool.Name, arg)
			case shaping && strings.TrimSpace(shapes) == "":
				t.Errorf("%s names %s as shaping the answer without saying how", spec.Tool.Name, arg)
			case !exposed[arg] && !shaping:
				t.Errorf("%s takes the argument %s, which sets no parameter it declares and shapes nothing it says", spec.Tool.Name, arg)
			}
		}
		for arg := range exposed {
			if !slices.Contains(arguments, arg) {
				t.Errorf("%s exposes a parameter through %s, which it does not take", spec.Tool.Name, arg)
			}
		}
		for arg := range spec.Shapes {
			if !slices.Contains(arguments, arg) {
				t.Errorf("%s says %s shapes its answer, but takes no such argument", spec.Tool.Name, arg)
			}
		}
	}
}

// specWithSchema is the spec as a server lists it, with the input schema the
// SDK derived.
func specWithSchema(t *testing.T, name string) server.Spec {
	t.Helper()
	for _, tool := range listTools(t, writingConfig) {
		if tool.Name == name {
			return server.Spec{Tool: tool}
		}
	}
	t.Fatalf("no tool %s", name)
	return server.Spec{}
}

func TestEveryDifferenceBetweenSupportedReleasesIsHandled(t *testing.T) {
	t.Parallel()
	s := loadSurface(t)
	page, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "site", "mattermost-releases.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range registered(t) {
		for _, use := range spec.Uses {
			op, ok := s.latest.Operation(use.Operation)
			if !ok {
				continue
			}
			differences := apisurface.Differences(op, s.esr, s.esrRoutes)
			handled := strings.TrimSpace(use.Releases) != ""
			switch {
			case len(differences) > 0 && !handled:
				t.Errorf("%s calls %s, which differs on %s, and says nothing of how it handles that:\n  %s",
					spec.Tool.Name, use.Operation, s.esr.Release, strings.Join(differences, "\n  "))
			case len(differences) == 0 && handled:
				t.Errorf("%s says how it handles %s on %s, where nothing differs", spec.Tool.Name, use.Operation, s.esr.Release)
			case len(differences) > 0 && !strings.Contains(string(page), "`"+use.Operation+"`"):
				t.Errorf("the supported releases page does not list how %s differs on %s", use.Operation, s.esr.Release)
			}
		}
	}
}

// TestALocalToolOnlyReadsMattermost holds a tool that writes this machine's
// files to reading Mattermost, which is what lets a local server offer it
// whether writes are allowed or not (ADR-029).
func TestALocalToolOnlyReadsMattermost(t *testing.T) {
	t.Parallel()
	s := loadSurface(t)
	for _, spec := range server.AllSpecs() {
		if !spec.Local {
			continue
		}
		for _, use := range spec.Uses {
			op, ok := s.latest.Operation(use.Operation)
			if ok && op.Method != "GET" && op.Method != "HEAD" {
				t.Errorf("%s is local, and calls %s, %s %s", spec.Tool.Name, use.Operation, op.Method, op.Path)
			}
		}
	}
}
