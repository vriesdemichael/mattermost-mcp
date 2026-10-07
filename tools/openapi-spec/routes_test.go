package main

import (
	"fmt"
	"strings"
	"testing"
)

// A small api4: the public subrouters and a local-mode one in api.go, routes on
// them in a handler file, and a local-mode file that must be left out.
const apiGo = `package api4

func Init(srv *Server) {
	api.BaseRoutes.Root = srv.Router
	api.BaseRoutes.APIRoot = srv.Router.PathPrefix(model.APIURLSuffix).Subrouter()
	api.BaseRoutes.Users = api.BaseRoutes.APIRoot.PathPrefix("/users").Subrouter()
	api.BaseRoutes.User = api.BaseRoutes.APIRoot.PathPrefix("/users/{user_id:[A-Za-z0-9]+}").Subrouter()
%s
}

func InitLocal(srv *Server) {
	api.BaseRoutes.Root = srv.LocalRouter
	api.BaseRoutes.Users = api.BaseRoutes.Root.PathPrefix("/local/users").Subrouter()
}
`

const userGo = `package api4

func (api *API) InitUser() {
	api.BaseRoutes.Users.Handle("", api.APIHandler(createUser)).Methods(http.MethodPost)
	api.BaseRoutes.User.Handle("", api.APISessionRequired(getUser)).Methods(http.MethodGet, http.MethodHead)
	api.BaseRoutes.User.Handle("/image", api.APISessionRequiredTrustRequester(getProfileImage)).Methods("GET")
%s
}
`

const userLocalGo = `package api4

func (api *API) InitUserLocal() {
	api.BaseRoutes.Users.Handle("/secret", api.APILocal(secret)).Methods(http.MethodGet)
}
`

// padding gives the fixture the hundred routes the extractor needs before it
// trusts a scan, and the twenty subrouters it needs before it trusts api.go.
func padding() (subrouters, routes string) {
	var s, r strings.Builder
	for i := range 20 {
		fmt.Fprintf(&s, "\tapi.BaseRoutes.Pad%d = api.BaseRoutes.APIRoot.PathPrefix(\"/pad%d\").Subrouter()\n", i, i)
	}
	for i := range 100 {
		fmt.Fprintf(&r, "\tapi.BaseRoutes.Pad%d.Handle(\"/r%d\", api.APIHandler(h%d)).Methods(http.MethodGet)\n", i%20, i, i)
	}
	return s.String(), r.String()
}

func TestTheRouteTableIsReadFromTheRouterNotTheLocalOne(t *testing.T) {
	t.Parallel()
	subrouters, routes := padding()
	extracted, err := ExtractRoutes(map[string]string{
		"api.go":        fmt.Sprintf(apiGo, subrouters),
		"user.go":       fmt.Sprintf(userGo, routes),
		"user_local.go": userLocalGo,
		"user_test.go":  "package api4",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, route := range extracted {
		found[route.Method+" "+route.Path] = route.Handler
	}
	want := map[string]string{
		"POST /api/v4/users":                "createUser",
		"GET /api/v4/users/{user_id}":       "getUser",
		"GET /api/v4/users/{user_id}/image": "getProfileImage",
		"GET /api/v4/pad3/r23":              "h23",
	}
	for route, handler := range want {
		if found[route] != handler {
			t.Errorf("%s: got handler %q, want %q", route, found[route], handler)
		}
	}
	for route := range found {
		if strings.Contains(route, "local") || strings.Contains(route, "secret") || strings.HasPrefix(route, "HEAD") {
			t.Errorf("%s is in the table; local-mode routes and HEAD are not", route)
		}
	}
}

func TestAnApiGoWithoutThePublicRouterIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := ExtractRoutes(map[string]string{"api.go": "package api4\n\nfunc Init() {}\n"}); err == nil {
		t.Fatal("an api.go that builds no subrouters was accepted")
	}
	if _, err := ExtractRoutes(map[string]string{"user.go": userLocalGo}); err == nil {
		t.Fatal("sources without api.go were accepted")
	}
}
