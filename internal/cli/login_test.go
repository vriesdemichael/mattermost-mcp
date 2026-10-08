package cli_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// login and logout where no request reaches Mattermost: which server, what is
// tried, stored and forgotten, and what a person is told. Logging in at a
// real Mattermost is the live suite's.

type memoryStore map[string]string

func (m memoryStore) credentials() *cli.Credentials {
	return &cli.Credentials{
		Load: func(address string) (string, bool, error) {
			token, ok := m[address]
			return token, ok, nil
		},
		Store:  func(address, token string) error { m[address] = token; return nil },
		Delete: func(address string) error { delete(m, address); return nil },
		Where:  "memory",
	}
}

// person stands in for the browser and the terminal, and records what was
// tried.
type person struct {
	tried  []string
	window func() (string, error)
	secret string
}

func (p *person) login() *cli.Login {
	return &cli.Login{
		Open: func(context.Context, string) error { p.tried = append(p.tried, "open"); return nil },
		Window: func(context.Context, string, string, string, io.Writer) (string, error) {
			p.tried = append(p.tried, "window")
			if p.window == nil {
				return "", login.ErrNoBrowser
			}
			return p.window()
		},
		Line:   func(string) (string, error) { p.tried = append(p.tried, "line"); return "", nil },
		Secret: func(string) (string, error) { p.tried = append(p.tried, "secret"); return p.secret, nil },
	}
}

func runWith(t *testing.T, env map[string]string, store memoryStore, who *person, args ...string) run {
	t.Helper()
	var stdout, stderr bytes.Buffer
	deps := cli.Deps{Getenv: testsupport.Env(env), Stdout: &stdout, Stderr: &stderr, Credentials: store.credentials()}
	if who != nil {
		deps.Login = who.login()
	}
	code := cli.Run(t.Context(), args, deps)
	return run{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestLoginNeedsTheServersAddressAndAKnownWay(t *testing.T) {
	t.Parallel()
	who := &person{}
	if got := runWith(t, nil, memoryStore{}, who, "login"); got.code != cli.ExitConfig || !strings.Contains(got.stderr, "--url") {
		t.Errorf("no address: %+v", got)
	}
	if got := runWith(t, nil, memoryStore{}, who, "login", "--url", "chat.example.com"); got.code != cli.ExitConfig || !strings.Contains(got.stderr, "http or https") {
		t.Errorf("a bare host: %+v", got)
	}
	if got := runWith(t, nil, memoryStore{}, who, "login", "--url", "https://chat.example.com", "--with", "magic"); got.code != cli.ExitConfig || !strings.Contains(got.stderr, "--with") {
		t.Errorf("an unknown way: %+v", got)
	}
	if len(who.tried) != 0 {
		t.Errorf("tried %v", who.tried)
	}
}

func TestLoginTriesTheNextWayAndEndsWithWhatToDo(t *testing.T) {
	t.Parallel()
	store := memoryStore{}
	who := &person{}
	// The unit tests reach no Mattermost, so its sign-in settings are unknown:
	// a browser window first, then a pasted token, which nobody gives.
	got := runWith(t, map[string]string{config.EnvURL: "https://chat.example.com"}, store, who, "login")
	if got.code != cli.ExitFailure || len(store) != 0 || strings.Join(who.tried, ",") != "window,secret" {
		t.Fatalf("got %+v after trying %v, stored %v", got, who.tried, store)
	}
	for _, advice := range []string{"npx playwright install chromium", "--with paste", "Enable Dynamic Client Registration", "http://127.0.0.1:8766/callback"} {
		if !strings.Contains(got.stderr, advice) {
			t.Errorf("the advice leaves out %q:\n%s", advice, got.stderr)
		}
	}
}

func TestLoginStoresNothingMattermostDidNotAccept(t *testing.T) {
	t.Parallel()
	store := memoryStore{}
	who := &person{secret: "a-token"}
	got := runWith(t, nil, store, who, "login", "--url", "https://chat.example.com", "--with", "paste")
	if got.code != cli.ExitFailure || !strings.Contains(got.stderr, "does not work") || len(store) != 0 || strings.Join(who.tried, ",") != "secret" {
		t.Errorf("got %+v after trying %v, stored %v", got, who.tried, store)
	}
}

func TestLogoutForgetsTheStoredSessionEvenWhenMattermostCannotBeReached(t *testing.T) {
	t.Parallel()
	store := memoryStore{"https://chat.example.com": "a-token"}
	got := runWith(t, nil, store, nil, "logout", "--url", "https://chat.example.com/")
	if got.code != cli.ExitOK || len(store) != 0 || !strings.Contains(got.stdout, "forgotten") || !strings.Contains(got.stderr, "warning") {
		t.Errorf("got %+v, stored %v", got, store)
	}
	if again := runWith(t, nil, store, nil, "logout", "--url", "https://chat.example.com"); again.code != cli.ExitOK || !strings.Contains(again.stdout, "No login is stored") {
		t.Errorf("again: %+v", again)
	}
}

func TestServeUsesTheStoredSessionWhenNoTokenIsSet(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	served := false
	store := memoryStore{"https://chat.example.com": "stored"}
	code := cli.Run(t.Context(), []string{"serve"}, cli.Deps{
		Getenv: testsupport.Env(map[string]string{config.EnvURL: "https://chat.example.com"}), Stdout: &stdout, Stderr: &stderr,
		Credentials: store.credentials(),
		Serve:       func(context.Context, *mcp.Server, cli.ServeOptions) error { served = true; return nil },
	})
	if code != cli.ExitOK || !served {
		t.Fatalf("exit %d, served %v: %s", code, served, stderr.String())
	}
}
