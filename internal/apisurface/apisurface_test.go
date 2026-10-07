package apisurface_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/apisurface"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

func TestMain(m *testing.M) { testsupport.SealedMain(m) }

const newest = `{
 "info": {"x-mattermost-release": "11.11.1"},
 "paths": {
  "/api/v4/users/{user_id}": {"get": {"operationId": "GetUser", "parameters": [{"name": "user_id", "in": "path"}]}},
  "/api/v4/users/{user_id}/teams": {"get": {"operationId": "GetTeamsForUser"}},
  "/api/v4/users/search": {"post": {"operationId": "SearchUsers",
    "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/UserSearch"}}}}}},
  "/api/v4/channels/{channel_id}/posts": {"get": {"operationId": "GetPostsForChannel",
    "description": "Get a page of posts.",
    "parameters": [
     {"name": "channel_id", "in": "path"},
     {"name": "page", "in": "query"},
     {"name": "include_deleted", "in": "query", "description": "__Minimum server version__: 11.9.0"}
    ]}},
  "/api/v4/recaps": {"get": {"operationId": "GetRecaps", "description": "__Minimum server version__: 11.10"}}
 },
 "components": {"schemas": {"UserSearch": {"properties": {"term": {}, "team_id": {}, "allow_inactive": {}}}}}
}`

const older = `{
 "info": {"x-mattermost-release": "11.7.11"},
 "paths": {
  "/api/v4/users/{user_id}": {"get": {"operationId": "GetUser", "parameters": [{"name": "user_id", "in": "path"}]}},
  "/api/v4/users/search": {"post": {"operationId": "SearchUsers",
    "requestBody": {"content": {"application/json": {"schema": {"properties": {"term": {}, "team_id": {}}}}}}}},
  "/api/v4/channels/{channel_id}/posts": {"get": {"operationId": "GetPostsForChannel",
    "parameters": [{"name": "channel_id", "in": "path"}, {"name": "page", "in": "query"}]}}
 }
}`

const olderRoutes = `{"release": "11.7.11", "routes": [
 {"method": "GET", "path": "/api/v4/users/{user_id}"},
 {"method": "POST", "path": "/api/v4/users/search"},
 {"method": "GET", "path": "/api/v4/channels/{channelId}/posts"}
]}`

func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T) (*apisurface.Spec, *apisurface.Spec, *apisurface.Routes) {
	t.Helper()
	latest, err := apisurface.LoadSpec(write(t, "latest.json", newest))
	if err != nil {
		t.Fatal(err)
	}
	esr, err := apisurface.LoadSpec(write(t, "esr.json", older))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := apisurface.LoadRoutes(write(t, "routes.json", olderRoutes))
	if err != nil {
		t.Fatal(err)
	}
	return latest, esr, routes
}

func TestARequestIsMatchedToItsOperationPreferringLiteralSegments(t *testing.T) {
	t.Parallel()
	latest, _, _ := load(t)
	cases := map[string]string{
		"GET /api/v4/users/me":           "GetUser",
		"GET /api/v4/users/abc123/teams": "GetTeamsForUser",
		"POST /api/v4/users/search":      "SearchUsers",
	}
	for request, want := range cases {
		method, path, _ := strings.Cut(request, " ")
		if op, ok := latest.Match(method, path); !ok || op.ID != want {
			t.Errorf("%s matched %q (%v), want %s", request, op.ID, ok, want)
		}
	}
	// GET /users/search fits /users/{user_id}, as Mattermost's router would
	// route it; a path that fits no template matches nothing.
	if op, ok := latest.Match("GET", "/api/v4/users/search"); !ok || op.ID != "GetUser" {
		t.Errorf("GET /api/v4/users/search matched %q, want GetUser as the router would", op.ID)
	}
	if _, ok := latest.Match("DELETE", "/api/v4/users/search"); ok {
		t.Error("a method the path does not have matched")
	}
	if _, ok := latest.Match("GET", "/api/v4/users//teams"); ok {
		t.Error("an empty segment filled a parameter")
	}
}

func TestAnOperationListsItsParametersAndBodyFields(t *testing.T) {
	t.Parallel()
	latest, _, _ := load(t)
	posts, _ := latest.Operation("GetPostsForChannel")
	if len(posts.Params) != 3 || posts.Params["include_deleted"].MinVersion != "11.9.0" {
		t.Fatalf("got %+v", posts.Params)
	}
	search, _ := latest.Operation("SearchUsers")
	if !slices.Equal(search.BodyFields, []string{"allow_inactive", "team_id", "term"}) {
		t.Fatalf("got %v", search.BodyFields)
	}
	teams, _ := latest.Operation("GetTeamsForUser")
	if teams.Params["user_id"].In != "path" {
		t.Fatalf("a parameter only the path names is missing: %+v", teams.Params)
	}
}

func TestTheDifferencesWithAnOlderReleaseAreNamed(t *testing.T) {
	t.Parallel()
	latest, esr, routes := load(t)
	differences := func(id string) string {
		op, ok := latest.Operation(id)
		if !ok {
			t.Fatalf("no %s", id)
		}
		return strings.Join(apisurface.Differences(op, esr, routes), "; ")
	}
	if got := differences("GetUser"); got != "" {
		t.Errorf("GetUser: got %q, want no difference", got)
	}
	if got := differences("GetPostsForChannel"); !strings.Contains(got, "parameter include_deleted arrived in 11.9.0") ||
		!strings.Contains(got, "has no parameter include_deleted") || strings.Contains(got, "does not serve") {
		t.Errorf("GetPostsForChannel: got %q", got)
	}
	if got := differences("SearchUsers"); !strings.Contains(got, "has no body field allow_inactive") {
		t.Errorf("SearchUsers: got %q", got)
	}
	if got := differences("GetRecaps"); !strings.Contains(got, "11.7.11 does not serve GET /api/v4/recaps") ||
		!strings.Contains(got, "arrived in 11.10") || !strings.Contains(got, "does not document") {
		t.Errorf("GetRecaps: got %q", got)
	}
}

func TestAFormBodyListsItsFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "spec.json")
	spec := `{"info": {"version": "1.0.0"}, "paths": {"/api/v4/files": {"post": {"operationId": "UploadFile",
    "requestBody": {"content": {"multipart/form-data": {"schema": {"properties": {"channel_id": {}, "files": {}}}}}}}}}}`
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := apisurface.LoadSpec(path)
	if err != nil {
		t.Fatal(err)
	}
	upload, ok := loaded.Operation("UploadFile")
	if !ok || !slices.Equal(upload.BodyFields, []string{"channel_id", "files"}) {
		t.Fatalf("got %+v", upload)
	}
}

// The specification gives SearchFiles to two routes; whichever is read first,
// the operation has the parameters of both.
func TestAnOperationIdOnTwoRoutesHasTheParametersOfBoth(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "spec.json")
	spec := `{"info": {"version": "1.0.0"}, "paths": {
    "/api/v4/files/search": {"post": {"operationId": "SearchFiles"}},
    "/api/v4/teams/{team_id}/files/search": {"post": {"operationId": "SearchFiles", "parameters": [{"name": "team_id", "in": "path"}]}}}}`
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		loaded, err := apisurface.LoadSpec(path)
		if err != nil {
			t.Fatal(err)
		}
		search, _ := loaded.Operation("SearchFiles")
		if _, ok := search.Params["team_id"]; !ok || search.Path != "/api/v4/files/search" {
			t.Fatalf("got %+v", search)
		}
	}
}
