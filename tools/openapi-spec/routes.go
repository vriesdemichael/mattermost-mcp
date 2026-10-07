package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Route is one endpoint a Mattermost release serves, as its own router
// registers it in server/channels/api4.
type Route struct {
	Method  string `json:"method"`
	Path    string `json:"path"`
	Handler string `json:"handler"`
}

var pathVariable = regexp.MustCompile(`\{([A-Za-z0-9_]+):[^}]*\}`)

// normalisePath turns a gorilla/mux template into the specification's form:
// {user_id:[A-Za-z0-9]+} becomes {user_id}.
func normalisePath(path string) string {
	return pathVariable.ReplaceAllString(path, "{$1}")
}

// ExtractRoutes reads the route table from the api4 sources: api.go, where the
// subrouters are given their prefixes, and every file that adds handlers to
// them. Local-mode files, which serve the server's own socket, are left out.
func ExtractRoutes(sources map[string]string) ([]Route, error) {
	api, ok := sources["api.go"]
	if !ok {
		return nil, fmt.Errorf("the api4 sources hold no api.go")
	}
	prefixes, err := routerPrefixes(api)
	if err != nil {
		return nil, err
	}
	var routes []Route
	for name, source := range sources {
		if strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, "_local.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || strings.HasSuffix(fn.Name.Name, "Local") {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					routes = append(routes, handled(call, prefixes)...)
				}
				return true
			})
		}
	}
	if len(routes) < 100 {
		return nil, fmt.Errorf("found only %d routes; the api4 sources have changed shape", len(routes))
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return dedupe(routes), nil
}

// routerPrefixes resolves each api.BaseRoutes subrouter to its full path, from
// the function in api.go that builds them on the public router.
func routerPrefixes(source string) (map[string]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "api.go", source, 0)
	if err != nil {
		return nil, fmt.Errorf("api.go: %w", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		prefixes := map[string]string{}
		public := false
		for _, statement := range fn.Body.List {
			assign, ok := statement.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				continue
			}
			name := baseRoute(assign.Lhs[0])
			if name == "" {
				continue
			}
			if exprString(assign.Rhs[0]) == "srv.Router" {
				public = true
				prefixes[name] = ""
				continue
			}
			parent, prefix, ok := subrouter(assign.Rhs[0])
			if !ok {
				continue
			}
			if parent == "srv.Router" {
				public = true
				prefixes[name] = prefix
			} else if base, known := prefixes[parent]; known {
				prefixes[name] = base + prefix
			}
		}
		if public && len(prefixes) > 20 {
			return prefixes, nil
		}
	}
	return nil, fmt.Errorf("api.go builds no subrouters on srv.Router; its shape has changed")
}

// subrouter reads X.PathPrefix("...").Subrouter(), naming X as a base route or
// as srv.Router. model.APIURLSuffix and model.APIURLSuffixV5 are the API roots.
func subrouter(expr ast.Expr) (parent, prefix string, ok bool) {
	call, isCall := expr.(*ast.CallExpr)
	if !isCall {
		return "", "", false
	}
	sub, isSel := call.Fun.(*ast.SelectorExpr)
	if !isSel || sub.Sel.Name != "Subrouter" {
		return "", "", false
	}
	inner, isCall := sub.X.(*ast.CallExpr)
	if !isCall || len(inner.Args) != 1 {
		return "", "", false
	}
	pathPrefix, isSel := inner.Fun.(*ast.SelectorExpr)
	if !isSel || pathPrefix.Sel.Name != "PathPrefix" {
		return "", "", false
	}
	switch arg := inner.Args[0].(type) {
	case *ast.BasicLit:
		prefix, _ = strconv.Unquote(arg.Value)
	case *ast.SelectorExpr:
		switch exprString(arg) {
		case "model.APIURLSuffix":
			prefix = "/api/v4"
		case "model.APIURLSuffixV5":
			prefix = "/api/v5"
		default:
			return "", "", false
		}
	default:
		return "", "", false
	}
	if name := baseRoute(pathPrefix.X); name != "" {
		return name, prefix, true
	}
	return exprString(pathPrefix.X), prefix, true
}

// handled reads api.BaseRoutes.X.Handle("...", handler).Methods(...), the one
// shape every api4 file registers an endpoint in.
func handled(call *ast.CallExpr, prefixes map[string]string) []Route {
	methods, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || methods.Sel.Name != "Methods" {
		return nil
	}
	handle, ok := methods.X.(*ast.CallExpr)
	if !ok || len(handle.Args) != 2 {
		return nil
	}
	handleSel, ok := handle.Fun.(*ast.SelectorExpr)
	if !ok || (handleSel.Sel.Name != "Handle" && handleSel.Sel.Name != "HandleFunc") {
		return nil
	}
	base, known := prefixes[baseRoute(handleSel.X)]
	if !known {
		return nil
	}
	pathLit, ok := handle.Args[0].(*ast.BasicLit)
	if !ok {
		return nil
	}
	path, _ := strconv.Unquote(pathLit.Value)
	var routes []Route
	for _, arg := range call.Args {
		if method := methodName(arg); method != "" && method != http.MethodHead {
			routes = append(routes, Route{Method: method, Path: normalisePath(base + path), Handler: handlerName(handle.Args[1])})
		}
	}
	return routes
}

func methodName(expr ast.Expr) string {
	switch arg := expr.(type) {
	case *ast.BasicLit:
		value, _ := strconv.Unquote(arg.Value)
		return strings.ToUpper(value)
	case *ast.SelectorExpr:
		if name, ok := strings.CutPrefix(arg.Sel.Name, "Method"); ok {
			return strings.ToUpper(name)
		}
	}
	return ""
}

// handlerName is the function an endpoint runs, unwrapped from the middleware
// that wraps it, such as api.APISessionRequired(getUser).
func handlerName(expr ast.Expr) string {
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			break
		}
		expr = call.Args[0]
	}
	return exprString(expr)
}

func baseRoute(expr ast.Expr) string {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	if strings.HasSuffix(exprString(sel.X), "api.BaseRoutes") {
		return sel.Sel.Name
	}
	return ""
}

func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	default:
		return ""
	}
}

func dedupe(routes []Route) []Route {
	var kept []Route
	for i, route := range routes {
		if i > 0 && route.Method == routes[i-1].Method && route.Path == routes[i-1].Path {
			continue
		}
		kept = append(kept, route)
	}
	return kept
}
