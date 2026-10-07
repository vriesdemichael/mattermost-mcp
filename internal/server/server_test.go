package server_test

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// What the server offers a client, read through an in-memory MCP client. No
// tool is called here: a call reaches Mattermost, and that is the live suite's
// (ADR-004).

var readOnlyConfig = config.Config{URL: "https://chat.example.com", Token: "t"}

func connect(t *testing.T, cfg config.Config) *mcp.ClientSession {
	t.Helper()
	client := mattermost.New(cfg.URL, cfg.Token, network.NewSafeTransport())
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.New(cfg, server.Single(client)).Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func listTools(t *testing.T, cfg config.Config) []*mcp.Tool {
	t.Helper()
	result, err := connect(t, cfg).ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return result.Tools
}

func TestTheServerIntroducesItself(t *testing.T) {
	t.Parallel()
	result := connect(t, readOnlyConfig).InitializeResult()
	if result.ServerInfo.Name != server.Name || result.Instructions != server.Instructions {
		t.Fatalf("got %+v", result)
	}
}

func TestGetMeIsOfferedWithAnOutputSchema(t *testing.T) {
	t.Parallel()
	for _, tool := range listTools(t, readOnlyConfig) {
		if tool.Name == "get_me" {
			if tool.OutputSchema == nil {
				t.Fatal("get_me declares no output schema")
			}
			return
		}
	}
	t.Fatal("get_me is not offered")
}
