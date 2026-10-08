//go:build live

package live

import (
	"bytes"
	"context"
	"encoding/pem"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// behindOwnAuthority is the live Mattermost behind HTTPS whose certificate an
// authority of its own signed, as an organisation signs its own, and a PEM file
// of that authority for MM_MCP_CA_FILE.
func behindOwnAuthority(t *testing.T) (string, string) {
	t.Helper()
	target, err := url.Parse(liveURL)
	check(t, err)
	server := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(server.Close)
	authority := filepath.Join(t.TempDir(), "authority.pem")
	check(t, os.WriteFile(authority, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600))
	return server.URL, authority
}

func TestLoginServeAndLogoutTrustTheAuthorityInTheCAFile(t *testing.T) {
	t.Parallel()
	address, authority := behindOwnAuthority(t)
	user := seedUser(t, admin(t))
	pat := personalAccessToken(t, admin(t), user.Id).Token
	stored := map[string]string{}
	run := func(env map[string]string, args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := cli.Run(t.Context(), args, cli.Deps{
			Getenv: testsupport.Env(env), Stdout: &stdout, Stderr: &stderr,
			Credentials: &cli.Credentials{
				Load:   func(address string) (string, bool, error) { token, ok := stored[address]; return token, ok, nil },
				Store:  func(address, token string) error { stored[address] = token; return nil },
				Delete: func(address string) error { delete(stored, address); return nil },
				Where:  "the test's memory",
			},
			Login: &cli.Login{Secret: func(string) (string, error) { return pat, nil }, Line: noTerminal},
			Serve: func(context.Context, *mcp.Server, cli.ServeOptions) error { return nil },
		})
		return code, stdout.String(), stderr.String()
	}
	trusting := map[string]string{config.EnvURL: address, config.EnvCAFile: authority}

	// Without the authority, nothing reaches the server.
	if code, _, stderr := run(map[string]string{config.EnvURL: address}, "login", "--with", "paste"); code != cli.ExitFailure || len(stored) != 0 {
		t.Fatalf("login without the authority: exit %d, stored %v\n%s", code, stored, stderr)
	}

	code, stdout, stderr := run(trusting, "login", "--with", "paste")
	if code != cli.ExitOK || stored[address] != pat || strings.Contains(stderr, "warning") {
		t.Fatalf("login trusting the authority: exit %d, stored %v\n%s%s", code, stored, stdout, stderr)
	}
	if code, _, stderr := run(trusting, "serve"); code != cli.ExitOK || !strings.Contains(stderr, "as @"+user.Username) {
		t.Fatalf("serve trusting the authority: exit %d\n%s", code, stderr)
	}
	if code, _, stderr := run(trusting, "logout"); code != cli.ExitOK || len(stored) != 0 || strings.Contains(stderr, "certificate") {
		t.Fatalf("logout trusting the authority: exit %d, stored %v\n%s", code, stored, stderr)
	}
}
