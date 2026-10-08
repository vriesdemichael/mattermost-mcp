package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// login and logout where no request is involved: which server, what is
// stored and forgotten, and what a person is told. Logging in at a real
// Mattermost is the live suite's.

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

func runWith(t *testing.T, env map[string]string, store memoryStore, loginWith func(context.Context, string, string) (string, error), args ...string) run {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(t.Context(), args, cli.Deps{
		Getenv: testsupport.Env(env), Stdout: &stdout, Stderr: &stderr,
		Credentials: store.credentials(), Login: loginWith,
	})
	return run{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestLoginNeedsTheServersAddress(t *testing.T) {
	t.Parallel()
	never := func(context.Context, string, string) (string, error) { t.Error("a browser was opened"); return "", nil }
	if got := runWith(t, nil, memoryStore{}, never, "login"); got.code != cli.ExitConfig || !strings.Contains(got.stderr, "--url") {
		t.Errorf("no address: %+v", got)
	}
	if got := runWith(t, nil, memoryStore{}, never, "login", "--url", "chat.example.com"); got.code != cli.ExitConfig || !strings.Contains(got.stderr, "http or https") {
		t.Errorf("a bare host: %+v", got)
	}
}

func TestLoginSaysWhatToDoWithoutABrowserAndStoresNothing(t *testing.T) {
	t.Parallel()
	store := memoryStore{}
	noBrowser := func(context.Context, string, string) (string, error) { return "", login.ErrNoBrowser }
	got := runWith(t, map[string]string{config.EnvURL: "https://chat.example.com"}, store, noBrowser, "login")
	if got.code != cli.ExitFailure || !strings.Contains(got.stderr, "--browser") || len(store) != 0 {
		t.Errorf("got %+v, stored %v", got, store)
	}
}

func TestLoginStoresNothingMattermostDidNotAccept(t *testing.T) {
	t.Parallel()
	store := memoryStore{}
	token := func(context.Context, string, string) (string, error) { return "a-token", nil }
	// The unit tests reach no Mattermost, so the session cannot be checked.
	got := runWith(t, nil, store, token, "login", "--url", "https://chat.example.com")
	if got.code != cli.ExitFailure || !strings.Contains(got.stderr, "does not work") || len(store) != 0 {
		t.Errorf("got %+v, stored %v", got, store)
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
