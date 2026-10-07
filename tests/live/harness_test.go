//go:build live

// Package live is the live suite: mm-mcp against a real Mattermost (ADR-004).
//
// `task test:live` starts the instance and runs this suite against it. A test
// seeds what it needs through the helpers here, under a name drawn at random,
// and removes it when it ends (ADR-008). Nothing is shared between tests, so
// they run in parallel.
//
// When no Mattermost answers, the suite fails before running anything and says
// so. It never skips: a skipped live test reads as a passing one (ADR-009).
package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/apisurface"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/server"
	"github.com/vriesdemichael/mm-mcp/internal/teststack"
)

const fixturePassword = "Live-test-password-1!"

var liveURL string

func TestMain(m *testing.M) {
	address, err := instanceURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v; start the instance with `task stack:up`\n", err)
		os.Exit(1)
	}
	liveURL = address
	probe := model.NewAPIv4Client(liveURL)
	probe.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	if status, _, err := probe.GetPing(context.Background()); err != nil || status != model.StatusOk {
		fmt.Fprintf(os.Stderr, "no Mattermost answers at %s (%v); start one with `task stack:up`\n", liveURL, err)
		os.Exit(1)
	}
	newest, err = apisurface.LoadSpec(filepath.Join("..", "..", "openapi", "mattermost-latest.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if code == 0 && wholeSuite() {
		if missing := declaredButNeverCalled(); len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "tools declare operations no live test saw them call (ADR-028):\n  %s\n", strings.Join(missing, "\n  "))
			code = 1
		}
	}
	os.Exit(code)
}

// instanceURL is the address of the instance this checkout's `task stack:up`
// started: MM_LIVE_STACK names the stack and MM_LIVE_RELEASE a release, as the
// task passes them. MM_LIVE_URL points the suite at any other instance.
func instanceURL() (string, error) {
	if address := os.Getenv("MM_LIVE_URL"); address != "" {
		return address, nil
	}
	stack := os.Getenv("MM_LIVE_STACK")
	if stack == "" {
		stack = "latest"
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return "", err
	}
	// Linked or not only changes the project name, not where the state file is.
	instance, err := teststack.Resolve(teststack.Checkout{Root: root}, stack, os.Getenv("MM_LIVE_RELEASE"))
	if err != nil {
		return "", err
	}
	return teststack.ReadState(filepath.Join(root, instance.StateFile))
}

// uniqueName is a fixture name: the prefix lt-, the kind of fixture, and a
// random part. Never a timestamp (ADR-008).
func uniqueName(kind string) string {
	random := make([]byte, 6)
	_, _ = rand.Read(random)
	return fmt.Sprintf("lt-%s-%s", kind, hex.EncodeToString(random))
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// admin is a Client4 logged in as the stack's system administrator.
func admin(t *testing.T) *model.Client4 {
	t.Helper()
	client := model.NewAPIv4Client(liveURL)
	_, _, err := client.Login(t.Context(), teststack.AdminUsername, teststack.AdminPassword)
	check(t, err)
	return client
}

// seedUser creates a user of this test's own, allowed to hold personal access
// tokens, and deactivates it when the test ends. Deactivated rather than
// deleted: permanent deletion is off by default, and a deactivated user holds
// no session and no working token.
func seedUser(t *testing.T, admin *model.Client4) *model.User {
	t.Helper()
	username := uniqueName("user")
	user, _, err := admin.CreateUser(t.Context(), &model.User{
		Username:  username,
		Password:  fixturePassword,
		Email:     username + "@example.com",
		FirstName: "Live",
		LastName:  "Test",
	})
	check(t, err)
	_, err = admin.UpdateUserRoles(t.Context(), user.Id, model.SystemUserRoleId+" "+model.SystemUserAccessTokenRoleId)
	check(t, err)
	t.Cleanup(func() { _, _ = admin.DeleteUser(context.Background(), user.Id) })
	return user
}

// seedBot creates a bot of this test's own, with a token, and disables it when the test ends.
func seedBot(t *testing.T, admin *model.Client4) (*model.Bot, string) {
	t.Helper()
	bot, _, err := admin.CreateBot(t.Context(), &model.Bot{Username: uniqueName("bot"), DisplayName: "Live test bot"})
	check(t, err)
	t.Cleanup(func() { _, _, _ = admin.DisableBot(context.Background(), bot.UserId) })
	return bot, personalAccessToken(t, admin, bot.UserId).Token
}

func personalAccessToken(t *testing.T, admin *model.Client4, userID string) *model.UserAccessToken {
	t.Helper()
	token, _, err := admin.CreateUserAccessToken(t.Context(), userID, uniqueName("token"), 0)
	check(t, err)
	return token
}

// sessionToken logs the user in as Mattermost's web app does and returns the
// session token it would keep in its MMAUTHTOKEN cookie.
func sessionToken(t *testing.T, user *model.User) string {
	t.Helper()
	client := model.NewAPIv4Client(liveURL)
	_, _, err := client.Login(t.Context(), user.Username, fixturePassword)
	check(t, err)
	return client.AuthToken
}

// mcpAs is an MCP client talking to mm-mcp in memory, which acts with token.
// Call its tools through callTool, which checks what they send (ADR-028).
func mcpAs(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	cfg := config.Config{URL: liveURL, Token: token}
	rec := &recorder{inner: network.NewSafeTransport()}
	client := mattermost.New(cfg.URL, cfg.Token, rec)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.New(cfg, server.Single(client)).Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "live", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	recorders.Store(session, rec)
	t.Cleanup(func() { recorders.Delete(session) })
	return session
}
