package login

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/version"
)

// A login's own requests, which do not go through the Mattermost client, name
// mm-mcp as the client's do.

func TestALoginsRequestsNameMmMcp(t *testing.T) {
	t.Parallel()
	request, err := newRequest(t.Context(), http.MethodGet, "https://chat.example.com/api/v4/config/client", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := request.Header.Get("User-Agent"); got != version.UserAgent() || !strings.HasPrefix(got, "mm-mcp/") {
		t.Errorf("User-Agent %q, want %q", got, version.UserAgent())
	}
	if _, err := newRequest(t.Context(), http.MethodGet, "https://[chat.example.com", nil); err == nil {
		t.Error("an address that does not parse made a request")
	}

	// Every request this package makes is built by newRequest, so none leaves
	// without the name.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		uses := strings.Count(string(source), "http.NewRequest")
		if file == "signin.go" {
			uses-- // newRequest itself
		}
		if uses != 0 {
			t.Errorf("%s builds a request without newRequest, so it does not name mm-mcp", file)
		}
	}
}
