//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// mm-mcp login, every way, against the live instance, which offers OAuth with
// clients registering themselves (docker/mattermost.env). The browsers are the
// ones on the machine the suite runs on, headless; where a person would log in
// or approve, the test does it from the page, with Mattermost's own requests
// (ADR-019).

// browsers are the browsers on this machine; the suite needs one.
func browsers(t *testing.T) []login.Browser {
	t.Helper()
	found, err := login.FindBrowsers(login.ThisSystem(), "")
	if err != nil {
		t.Fatalf("the live suite logs in with a browser on this machine: %v", err)
	}
	return found
}

// retry runs a script in the page until it succeeds, while the page loads.
func retry(ctx context.Context, window login.Window, script string) error {
	deadline := time.Now().Add(30 * time.Second)
	for {
		err := window.Evaluate(ctx, script)
		if err == nil || time.Now().After(deadline) || errors.Is(err, login.ErrBrowserClosed) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// loginScript logs in from Mattermost's login page, with the request its web
// app sends, which sets the session cookie.
func loginScript(username string) string {
	return fmt.Sprintf(`(async () => {
		if (location.origin !== %q) { throw new Error("not on the login page yet: " + location.href); }
		const answer = await fetch("/api/v4/users/login", {
			method: "POST",
			headers: {"Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest"},
			body: JSON.stringify({login_id: %q, password: %q}),
		});
		if (!answer.ok) { throw new Error("Mattermost answered " + answer.status); }
	})()`, liveURL, username, fixturePassword)
}

// windowLogsIn is a Login.Window that logs in as username in a headless
// browser: the one named, or else browser.
func windowLogsIn(username string, browser login.Browser) func(context.Context, string, string, string, io.Writer) (string, error) {
	return func(ctx context.Context, _, address, server string, _ io.Writer) (string, error) {
		window, err := login.Start(ctx, browser, address, login.Options{Headless: true})
		if err != nil {
			return "", err
		}
		defer window.Close()
		if err := retry(ctx, window, loginScript(username)); err != nil {
			return "", err
		}
		return window.WaitForToken(ctx, server)
	}
}

// approver is the person's own browser, logged in, which approves mm-mcp when
// OAuth opens Mattermost's authorization page in it, as its consent page does.
type approver struct {
	window login.Window
	mu     sync.Mutex
	opened []string
}

func loggedInBrowser(t *testing.T, username string) *approver {
	t.Helper()
	window, err := login.Start(t.Context(), browsers(t)[0], liveURL+"/login", login.Options{Headless: true})
	check(t, err)
	t.Cleanup(window.Close)
	check(t, retry(t.Context(), window, loginScript(username)))
	return &approver{window: window}
}

func (a *approver) open(ctx context.Context, page string) error {
	a.mu.Lock()
	a.opened = append(a.opened, page)
	a.mu.Unlock()
	parsed, err := url.Parse(page)
	if err != nil {
		return err
	}
	query := parsed.Query()
	request := map[string]string{}
	for _, name := range []string{"response_type", "client_id", "redirect_uri", "state", "code_challenge", "code_challenge_method"} {
		request[name] = query.Get(name)
	}
	body := fmt.Sprintf("%q", mustJSON(request))
	// The page may still be the callback of the last login.
	if err := a.window.Navigate(ctx, liveURL+"/"); err != nil {
		return err
	}
	script := fmt.Sprintf(`(async () => {
		if (location.origin !== %q || document.readyState !== "complete") { throw new Error("not back on Mattermost yet"); }
		const csrf = (document.cookie.match(/MMCSRF=([^;]+)/) || [])[1] || "";
		const answer = await fetch("/oauth/authorize", {
			method: "POST",
			headers: {"Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest", "X-CSRF-Token": csrf},
			body: %s,
		});
		if (!answer.ok) { throw new Error("Mattermost answered " + answer.status + ": " + await answer.text()); }
		window.__redirect = (await answer.json()).redirect;
		location.href = window.__redirect;
	})()`, liveURL, body)
	return retry(ctx, a.window, script)
}

func mustJSON(value map[string]string) string {
	var b strings.Builder
	b.WriteString("{")
	first := true
	for name, v := range value {
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:%q", name, v)
	}
	b.WriteString("}")
	return b.String()
}

// loginRun runs the command line with the given login and a store in memory.
type loginRun struct {
	t           *testing.T
	stored      map[string]string
	clients     map[string]string
	login       *cli.Login
	credentials *cli.Credentials
}

func newLoginRun(t *testing.T, who *cli.Login) *loginRun {
	r := &loginRun{t: t, stored: map[string]string{}, clients: map[string]string{}, login: who}
	r.credentials = &cli.Credentials{
		Load:        func(address string) (string, bool, error) { token, ok := r.stored[address]; return token, ok, nil },
		Store:       func(address, token string) error { r.stored[address] = token; return nil },
		Delete:      func(address string) error { delete(r.stored, address); return nil },
		LoadClient:  func(address string) (string, bool, error) { c, ok := r.clients[address]; return c, ok, nil },
		StoreClient: func(address, client string) error { r.clients[address] = client; return nil },
		Where:       "the test's memory",
	}
	return r
}

func (r *loginRun) run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(r.t.Context(), args, cli.Deps{
		Getenv: testsupport.Env(map[string]string{config.EnvURL: liveURL}), Stdout: &stdout, Stderr: &stderr,
		Credentials: r.credentials, Login: r.login,
		Serve: func(context.Context, *mcp.Server, cli.ServeOptions) error { return nil },
	})
	return code, stdout.String(), stderr.String()
}

// whoIs is the user a token acts as.
func whoIs(t *testing.T, token string) string {
	t.Helper()
	user, _, err := mattermost.New(liveURL, token, network.NewSafeTransport()).Check(t.Context())
	check(t, err)
	return user.Username
}

func noTerminal(string) (string, error) { return "", errors.New("the test types nothing") }

func TestLoginThroughOAuthRegistersOnceAndAsksInThePersonsBrowser(t *testing.T) {
	t.Parallel()
	user := seedUser(t, admin(t))
	browser := loggedInBrowser(t, user.Username)
	r := newLoginRun(t, &cli.Login{Open: browser.open, Window: nil, Line: noTerminal, Secret: noTerminal})

	code, stdout, stderr := r.run("login")
	if code != cli.ExitOK || !strings.Contains(stdout, "as @"+user.Username) || len(browser.opened) != 1 {
		t.Fatalf("login: exit %d, opened %d pages\n%s%s", code, len(browser.opened), stdout, stderr)
	}
	if got := whoIs(t, r.stored[liveURL]); got != user.Username {
		t.Fatalf("the token acts as %s", got)
	}
	first := r.clients[liveURL]
	if !strings.Contains(first, `"client_id"`) {
		t.Fatalf("no client was kept to log in as again: %q", first)
	}

	// Again: the same client, not another registration.
	if code, _, stderr := r.run("login"); code != cli.ExitOK || r.clients[liveURL] != first {
		t.Fatalf("again: exit %d, client %q then %q\n%s", code, first, r.clients[liveURL], stderr)
	}
}

func TestLoginThroughOAuthAsTheAppAnAdministratorRegistered(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	// A free port, which the app's callback names and the login listens on.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	check(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	check(t, listener.Close())
	app := publicApp(t, admin, "http://127.0.0.1:"+strconv.Itoa(port)+"/callback")
	t.Cleanup(func() { _, _ = admin.DeleteOAuthApp(context.Background(), app.Id) })
	browser := loggedInBrowser(t, user.Username)
	r := newLoginRun(t, &cli.Login{Open: browser.open, Line: noTerminal, Secret: noTerminal})

	code, stdout, stderr := r.run("login", "--with", "oauth", "--client-id", app.Id, "--callback-port", strconv.Itoa(port))

	if code != cli.ExitOK || whoIs(t, r.stored[liveURL]) != user.Username || !strings.Contains(r.clients[liveURL], app.Id) {
		t.Fatalf("login: exit %d\n%s%s", code, stdout, stderr)
	}
}

func TestLoginThroughAWindowWithEveryBrowserOnThisMachine(t *testing.T) {
	t.Parallel()
	user := seedUser(t, admin(t))
	for _, browser := range browsers(t) {
		r := newLoginRun(t, &cli.Login{Window: windowLogsIn(user.Username, browser), Line: noTerminal, Secret: noTerminal})
		code, stdout, stderr := r.run("login", "--with", "window")
		if code != cli.ExitOK || whoIs(t, r.stored[liveURL]) != user.Username {
			t.Errorf("%s: exit %d\n%s%s", browser, code, stdout, stderr)
		}
	}
}

func TestLoginWithAPasswordOrAPastedToken(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	typed := &cli.Login{
		Line:   func(string) (string, error) { return user.Username, nil },
		Secret: func(string) (string, error) { return fixturePassword, nil },
	}
	r := newLoginRun(t, typed)
	if code, stdout, stderr := r.run("login", "--with", "password"); code != cli.ExitOK || whoIs(t, r.stored[liveURL]) != user.Username {
		t.Fatalf("password: exit %d\n%s%s", code, stdout, stderr)
	}

	wrong := newLoginRun(t, &cli.Login{
		Line:   func(string) (string, error) { return user.Username, nil },
		Secret: func(string) (string, error) { return "not the password", nil },
	})
	if code, _, stderr := wrong.run("login", "--with", "password"); code != cli.ExitFailure || !strings.Contains(stderr, "login was refused") || strings.Contains(stderr, "MM_TOKEN") || len(wrong.stored) != 0 {
		t.Fatalf("a wrong password: exit %d\n%s", code, stderr)
	}

	pat := personalAccessToken(t, admin, user.Id).Token
	pasted := newLoginRun(t, &cli.Login{Secret: func(string) (string, error) { return " " + pat + "\n", nil }, Line: noTerminal})
	if code, stdout, stderr := pasted.run("login", "--with", "paste"); code != cli.ExitOK || pasted.stored[liveURL] != pat {
		t.Fatalf("paste: exit %d\n%s%s", code, stdout, stderr)
	}
}

func TestServeUsesTheStoredLoginAndLogoutEndsIt(t *testing.T) {
	t.Parallel()
	user := seedUser(t, admin(t))
	r := newLoginRun(t, &cli.Login{Window: windowLogsIn(user.Username, browsers(t)[0]), Line: noTerminal, Secret: noTerminal})
	if code, stdout, stderr := r.run("login", "--with", "window"); code != cli.ExitOK {
		t.Fatalf("login: exit %d\n%s%s", code, stdout, stderr)
	}
	token := r.stored[liveURL]

	if code, _, stderr := r.run("serve"); code != cli.ExitOK || !strings.Contains(stderr, "as @"+user.Username) {
		t.Fatalf("serve with the stored session: exit %d\n%s", code, stderr)
	}
	if code, stdout, stderr := r.run("logout"); code != cli.ExitOK || len(r.stored) != 0 || strings.Contains(stderr, "warning") {
		t.Fatalf("logout: exit %d, stored %v\n%s%s", code, r.stored, stdout, stderr)
	}
	_, _, err := mattermost.New(liveURL, token, network.NewSafeTransport()).Check(t.Context())
	var refused *mattermost.Error
	if !errors.As(err, &refused) || refused.Status != 401 {
		t.Fatalf("the session still works after logout: %v", err)
	}
	if code, _, stderr := r.run("serve"); code != cli.ExitConfig || !strings.Contains(stderr, "mm-mcp login --url "+liveURL) {
		t.Fatalf("serve after logout: exit %d\n%s", code, stderr)
	}
}

// publicApp is an OAuth app a system administrator registers as a public
// client, without a secret, as the System Console's "public client" does.
// Mattermost's Go client sends no is_public, so this sends the API's request.
func publicApp(t *testing.T, admin *model.Client4, callback string) *model.OAuthApp {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"name": uniqueName("app"), "description": "mm-mcp", "homepage": "https://github.com/vriesdemichael/mm-mcp",
		"callback_urls": []string{callback}, "is_public": true,
	})
	check(t, err)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, liveURL+"/api/v4/oauth/apps", bytes.NewReader(body))
	check(t, err)
	request.Header.Set("Authorization", "Bearer "+admin.AuthToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	check(t, err)
	defer response.Body.Close()
	var app model.OAuthApp
	check(t, json.NewDecoder(response.Body).Decode(&app))
	if response.StatusCode != http.StatusCreated || app.Id == "" || app.ClientSecret != "" {
		t.Fatalf("registering a public app: %s, %+v", response.Status, app)
	}
	return &app
}
