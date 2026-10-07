//go:build live

package live

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// get_me, with each kind of credential mm-mcp accepts (ADR-019).

func callGetMe(t *testing.T, session *mcp.ClientSession) (*mcp.CallToolResult, server.UserSummary) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_me", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	var user server.UserSummary
	if !result.IsError {
		raw, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &user); err != nil {
			t.Fatal(err)
		}
	}
	return result, user
}

func errorText(result *mcp.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, " ")
}

func TestGetMeWithAPersonalAccessTokenNamesTheUser(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	token := personalAccessToken(t, admin, user.Id).Token

	result, got := callGetMe(t, mcpAs(t, token))

	if result.IsError {
		t.Fatal(errorText(result))
	}
	if got.ID != user.Id || got.Username != user.Username || got.FirstName != "Live" || got.LastName != "Test" || got.IsBot {
		t.Fatalf("got %+v for %s", got, user.Username)
	}
}

func TestGetMeWithASessionTokenNamesTheUser(t *testing.T) {
	t.Parallel()
	user := seedUser(t, admin(t))

	result, got := callGetMe(t, mcpAs(t, sessionToken(t, user)))

	if result.IsError || got.ID != user.Id {
		t.Fatalf("got %+v: %s", got, errorText(result))
	}
}

func TestGetMeWithABotTokenNamesTheBot(t *testing.T) {
	t.Parallel()
	bot, token := seedBot(t, admin(t))

	result, got := callGetMe(t, mcpAs(t, token))

	if result.IsError || got.ID != bot.UserId || got.Username != bot.Username || !got.IsBot {
		t.Fatalf("got %+v: %s", got, errorText(result))
	}
}

func TestGetMeWithARevokedTokenFailsAndSaysMattermostRefused(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	token := personalAccessToken(t, admin, user.Id)
	if _, err := admin.RevokeUserAccessToken(t.Context(), token.Id); err != nil {
		t.Fatal(err)
	}

	result, _ := callGetMe(t, mcpAs(t, token.Token))

	if !result.IsError || !strings.Contains(errorText(result), "Mattermost answered 401") {
		t.Fatalf("got %+v: %s", result, errorText(result))
	}
}

func TestGetMeWithATokenMattermostNeverIssuedFails(t *testing.T) {
	t.Parallel()
	result, _ := callGetMe(t, mcpAs(t, "not-a-token-mattermost-issued"))

	if !result.IsError || !strings.Contains(errorText(result), "Mattermost answered 401") {
		t.Fatalf("got %+v: %s", result, errorText(result))
	}
}
