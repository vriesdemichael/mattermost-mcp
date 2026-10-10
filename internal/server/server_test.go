package server_test

import (
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
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

// A server that does not ask says so; one that cannot write has nothing to say.
func TestAServerThatDoesNotAskSaysSo(t *testing.T) {
	t.Parallel()
	skipping := config.Config{URL: "https://chat.example.com", Token: "t", AllowWrites: true, SkipAsking: true}
	if got := connect(t, skipping).InitializeResult().Instructions; got != server.Instructions+"\n\n"+server.SkippingInstructions {
		t.Errorf("writing without asking: %q", got)
	}
	skipping.AllowWrites = false
	if got := connect(t, skipping).InitializeResult().Instructions; got != server.Instructions {
		t.Errorf("read-only: %q", got)
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

// A file is offered as a resource on every server, read-only or not, under
// an address of its own scheme that names the server (ADR-029).
func TestAFileIsOfferedAsAResourceOfItsServer(t *testing.T) {
	t.Parallel()
	for _, cfg := range []config.Config{readOnlyConfig, writingConfig, {URL: "http://Localhost:8065/mm/", Token: "t"}} {
		session := connect(t, cfg)
		if session.InitializeResult().Capabilities.Completions == nil {
			t.Errorf("%s: no completions offered", cfg.URL)
		}
		result, err := session.ListResourceTemplates(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			readOnlyConfig.URL:          "mattermost://chat.example.com/files/{file_id}",
			"http://Localhost:8065/mm/": "mattermost://localhost:8065/mm/files/{file_id}",
		}[cfg.URL]
		if len(result.ResourceTemplates) != 1 || result.ResourceTemplates[0].URITemplate != want {
			t.Errorf("%s: got %+v, want %s", cfg.URL, result.ResourceTemplates, want)
		}
	}
}

// What is not a file of this server is not found, and nothing asks
// Mattermost: the unit tests block the network, which would fail otherwise.
func TestAResourceThatIsNoFileOfThisServerIsNotFound(t *testing.T) {
	t.Parallel()
	session := connect(t, readOnlyConfig)
	for _, uri := range []string{
		"mattermost://chat.example.com/files/not-an-id",
		"mattermost://other.example.com/files/abcdefghijklmnopqrstuvwxyz",
		"mattermost://chat.example.com/posts/abcdefghijklmnopqrstuvwxyz",
	} {
		_, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: uri})
		var refused *jsonrpc.Error
		if !errors.As(err, &refused) || refused.Code != jsonrpc.CodeInvalidParams || refused.Message != "Resource not found" {
			t.Errorf("%s: got %v; want resource not found", uri, err)
		}
	}
}

// Only the file template's argument is completed; anything else gets no
// suggestion, without asking Mattermost.
func TestOnlyAFilesArgumentIsCompleted(t *testing.T) {
	t.Parallel()
	session := connect(t, readOnlyConfig)
	for _, params := range []*mcp.CompleteParams{
		{Ref: &mcp.CompleteReference{Type: "ref/resource", URI: "mattermost://other.example.com/files/{file_id}"}, Argument: mcp.CompleteParamsArgument{Name: "file_id", Value: "report"}},
		{Ref: &mcp.CompleteReference{Type: "ref/resource", URI: "mattermost://chat.example.com/files/{file_id}"}, Argument: mcp.CompleteParamsArgument{Name: "other", Value: "report"}},
		{Ref: &mcp.CompleteReference{Type: "ref/resource", URI: "mattermost://chat.example.com/files/{file_id}"}, Argument: mcp.CompleteParamsArgument{Name: "file_id", Value: " "}},
		{Ref: &mcp.CompleteReference{Type: "ref/resource", URI: "mattermost://chat.example.com/files/{file_id}"}, Argument: mcp.CompleteParamsArgument{Name: "file_id", Value: "abcdefghijklmnopqrstuvwxyz"}},
		{Ref: &mcp.CompleteReference{Type: "ref/prompt", Name: "anything"}, Argument: mcp.CompleteParamsArgument{Name: "file_id", Value: "report"}},
	} {
		result, err := session.Complete(t.Context(), params)
		if err != nil || len(result.Completion.Values) != 0 {
			t.Errorf("%+v %+v: got %+v, %v", params.Ref, params.Argument, result, err)
		}
	}
}
