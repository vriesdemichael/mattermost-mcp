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
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
// random part. Never a timestamp (ADR-008). The random part is hex with x for
// e: Postgres's text search reads digits, an e and digits, such as 27e7, as a
// number, which splits a username so that searching for a mention of it finds
// nothing, and a test would fail on the name it drew.
func uniqueName(kind string) string {
	random := make([]byte, 6)
	_, _ = rand.Read(random)
	return fmt.Sprintf("lt-%s-%s", kind, strings.ReplaceAll(hex.EncodeToString(random), "e", "x"))
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// adminSession is the system administrator's one session for the whole run.
// Mattermost claims a failed-attempt slot for every login before it checks
// the password and frees it only once the login succeeds, so more logins of
// one account in flight at once than it allows attempts lock the account out,
// with the right password. Parallel tests each logging in would do that.
var adminSession = sync.OnceValues(func() (string, error) {
	client := model.NewAPIv4Client(liveURL)
	_, _, err := client.Login(context.Background(), teststack.AdminUsername, teststack.AdminPassword)
	return client.AuthToken, err
})

// admin is a Client4 acting as the stack's system administrator.
func admin(t *testing.T) *model.Client4 {
	t.Helper()
	token, err := adminSession()
	check(t, err)
	client := model.NewAPIv4Client(liveURL)
	client.SetToken(token)
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
	return mcpWith(t, config.Config{URL: liveURL, Token: token}, nil)
}

// mcpWriting is mcpAs with writes allowed, from a client that answers every
// question with answer, which also sees the question. A nil answer is a client
// that cannot be asked at all.
func mcpWriting(t *testing.T, token string, answer func(*mcp.ElicitParams) *mcp.ElicitResult) *mcp.ClientSession {
	t.Helper()
	var options *mcp.ClientOptions
	if answer != nil {
		options = &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return answer(request.Params), nil
		}}
	}
	return mcpWith(t, config.Config{URL: liveURL, Token: token, AllowWrites: true, MarkAIGenerated: true}, options)
}

func mcpWith(t *testing.T, cfg config.Config, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	rec := &recorder{inner: network.NewSafeTransport()}
	client := mattermost.New(cfg.URL, cfg.Token, rec)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.New(cfg, server.Single(client)).Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "live", Version: "0"}, options).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	recorders.Store(session, rec)
	t.Cleanup(func() { recorders.Delete(session) })
	return session
}

// clientAs is a Client4 logged in as a seeded user, for acting as them in a
// test's setup: posting, reacting, sending a direct message.
func clientAs(t *testing.T, user *model.User) *model.Client4 {
	t.Helper()
	client := model.NewAPIv4Client(liveURL)
	_, _, err := client.Login(t.Context(), user.Username, fixturePassword)
	check(t, err)
	return client
}

// seedTeam creates an open team of this test's own, with the given users as
// members, and archives it when the test ends.
func seedTeam(t *testing.T, admin *model.Client4, members ...*model.User) *model.Team {
	t.Helper()
	name := uniqueName("team")
	team, _, err := admin.CreateTeam(t.Context(), &model.Team{Name: name, DisplayName: "Live " + name, Type: model.TeamOpen})
	check(t, err)
	t.Cleanup(func() { _, _ = admin.SoftDeleteTeam(context.Background(), team.Id) })
	for _, member := range members {
		_, _, err := admin.AddTeamMember(t.Context(), team.Id, member.Id)
		check(t, err)
	}
	return team
}

// seedChannel creates a public channel in a team, with the given users as
// members. It goes when its team is archived.
func seedChannel(t *testing.T, admin *model.Client4, team *model.Team, members ...*model.User) *model.Channel {
	t.Helper()
	name := uniqueName("channel")
	channel, _, err := admin.CreateChannel(t.Context(), &model.Channel{TeamId: team.Id, Name: name, DisplayName: "Live " + name, Type: model.ChannelTypeOpen})
	check(t, err)
	for _, member := range members {
		_, _, err := admin.AddChannelMember(t.Context(), channel.Id, member.Id)
		check(t, err)
	}
	return channel
}

// postAs posts a message in a channel, as a reply to rootID when it is set.
func postAs(t *testing.T, author *model.Client4, channelID, rootID, message string) *model.Post {
	t.Helper()
	post, _, err := author.CreatePost(t.Context(), &model.Post{ChannelId: channelID, RootId: rootID, Message: message})
	check(t, err)
	return post
}

// structured reads a tool's structured answer into out, failing the test on a
// tool error.
func structured(t *testing.T, result *mcp.CallToolResult, out any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("the tool failed: %s", errorText(result))
	}
	raw, err := json.Marshal(result.StructuredContent)
	check(t, err)
	check(t, json.Unmarshal(raw, out))
}

// eventually retries check until it reports done or the deadline passes, for
// what Mattermost does asynchronously, such as indexing a post for search
// (ADR-008). It never sleeps a fixed time in place of waiting for the result.
func eventually(t *testing.T, within time.Duration, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		done, seen := check()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("not done within %s; last saw: %s", within, seen)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// everyPage calls a list tool page by page, limit at a time, from first until
// it answers without a next_cursor (ADR-032), and returns each page's items
// by key: the field list of each answer holds them, and the field key of each
// names it. first is the first call, written as a literal so the scan that
// finds which tools the suite calls sees it.
func everyPage(t *testing.T, session *mcp.ClientSession, first *mcp.CallToolParams, limit int, list, key string) [][]string {
	t.Helper()
	asked := maps.Clone(first.Arguments.(map[string]any))
	asked["limit"] = limit
	var pages [][]string
	for range 50 {
		result := callTool(t, session, &mcp.CallToolParams{Name: first.Name, Arguments: asked})
		var answer map[string]any
		structured(t, result, &answer)
		items, _ := answer[list].([]any)
		var page []string
		for _, item := range items {
			page = append(page, fmt.Sprint(item.(map[string]any)[key]))
		}
		if len(page) > limit {
			t.Fatalf("%s gave %d items on a page of %d", first.Name, len(page), limit)
		}
		pages = append(pages, page)
		next, _ := answer["next_cursor"].(string)
		if next == "" {
			return pages
		}
		asked["cursor"] = next
	}
	t.Fatalf("%s gave a next_cursor fifty pages on", first.Name)
	return nil
}

// flat is every item of every page, in order, failing the test on one seen twice.
func flat(t *testing.T, pages [][]string) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	for _, page := range pages {
		for _, item := range page {
			if seen[item] {
				t.Fatalf("%s is on two pages: %v", item, pages)
			}
			seen[item] = true
			out = append(out, item)
		}
	}
	return out
}
