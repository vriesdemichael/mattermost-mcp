//go:build live

package live

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// mm-mcp login, with a real browser: the Chrome or Edge on this machine, run
// headless, which logs in to the live instance as a person would, by
// Mattermost's own login request from its page (ADR-019).

// browserLogsIn is a Login for the command line that starts the browser and
// logs in from the page, as the person would in its window.
func browserLogsIn(t *testing.T, username string) func(context.Context, string, string) (string, error) {
	t.Helper()
	return func(ctx context.Context, browser, address string) (string, error) {
		path, err := login.FindBrowser(login.ThisSystem(), browser)
		if err != nil {
			return "", fmt.Errorf("the live suite logs in with a Chrome or Edge on this machine: %w", err)
		}
		session, err := login.Start(ctx, path, address+"/login", login.Options{Headless: true})
		if err != nil {
			return "", err
		}
		defer session.Close()
		script := fmt.Sprintf(`(async () => {
			if (location.origin !== %q) { throw new Error("not on the login page yet"); }
			const answer = await fetch("/api/v4/users/login", {
				method: "POST",
				headers: {"Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest"},
				body: JSON.stringify({login_id: %q, password: %q}),
			});
			if (!answer.ok) { throw new Error("Mattermost answered " + answer.status); }
		})()`, address, username, fixturePassword)
		deadline := time.Now().Add(30 * time.Second)
		for {
			err := session.Evaluate(ctx, script)
			if err == nil {
				break
			}
			if time.Now().After(deadline) || errors.Is(err, login.ErrBrowserClosed) {
				return "", err
			}
			time.Sleep(200 * time.Millisecond)
		}
		return session.WaitForToken(ctx, address)
	}
}

func TestLoginStoresTheSessionServeUsesItAndLogoutEndsIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	stored := map[string]string{}
	credentials := &cli.Credentials{
		Load:   func(address string) (string, bool, error) { token, ok := stored[address]; return token, ok, nil },
		Store:  func(address, token string) error { stored[address] = token; return nil },
		Delete: func(address string) error { delete(stored, address); return nil },
		Where:  "the test's memory",
	}
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := cli.Run(t.Context(), args, cli.Deps{
			Getenv: testsupport.Env(map[string]string{config.EnvURL: liveURL}), Stdout: &stdout, Stderr: &stderr,
			Credentials: credentials, Login: browserLogsIn(t, user.Username),
			Serve: func(context.Context, *mcp.Server, cli.ServeOptions) error { return nil },
		})
		return code, stdout.String(), stderr.String()
	}

	code, stdout, stderr := run("login")
	if code != cli.ExitOK || !strings.Contains(stdout, "as @"+user.Username) {
		t.Fatalf("login: exit %d\n%s%s", code, stdout, stderr)
	}
	token := stored[liveURL]
	if token == "" {
		t.Fatalf("nothing stored for %s: %v", liveURL, stored)
	}

	if code, _, stderr := run("serve"); code != cli.ExitOK || !strings.Contains(stderr, "as @"+user.Username) {
		t.Fatalf("serve with the stored session: exit %d\n%s", code, stderr)
	}

	if code, stdout, stderr := run("logout"); code != cli.ExitOK || len(stored) != 0 || strings.Contains(stderr, "warning") {
		t.Fatalf("logout: exit %d, stored %v\n%s%s", code, stored, stdout, stderr)
	}
	_, _, err := mattermost.New(liveURL, token, network.NewSafeTransport()).Check(t.Context())
	var refused *mattermost.Error
	if !errors.As(err, &refused) || refused.Status != 401 {
		t.Fatalf("the session still works after logout: %v", err)
	}
	if code, _, stderr := run("serve"); code != cli.ExitConfig || !strings.Contains(stderr, "mm-mcp login --url "+liveURL) {
		t.Fatalf("serve after logout: exit %d\n%s", code, stderr)
	}
}
