//go:build live

package live

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// mm-mcp serve checks the address and the credential before it serves.

type started struct {
	code   int
	stderr string
	served bool
}

func serveWith(t *testing.T, env map[string]string) started {
	t.Helper()
	var stdout, stderr bytes.Buffer
	result := started{}
	result.code = cli.Run(t.Context(), []string{"serve"}, cli.Deps{
		Getenv: testsupport.Env(env),
		Stdout: &stdout,
		Stderr: &stderr,
		Serve: func(context.Context, *mcp.Server, cli.ServeOptions) error {
			result.served = true
			return nil
		},
	})
	result.stderr = stderr.String()
	return result
}

func TestServeWithAWorkingTokenSaysWhoItIsConnectedAs(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)

	got := serveWith(t, map[string]string{config.EnvURL: liveURL, config.EnvToken: personalAccessToken(t, admin, user.Id).Token})

	if got.code != cli.ExitOK || !got.served || !strings.Contains(got.stderr, "connected to "+liveURL+" as @"+user.Username) {
		t.Fatalf("got %+v", got)
	}
}

func TestServeWithARefusedTokenStopsAtStartAndSaysWhatToDo(t *testing.T) {
	t.Parallel()

	got := serveWith(t, map[string]string{config.EnvURL: liveURL, config.EnvToken: "not-a-token-mattermost-issued"})

	if got.code != cli.ExitConfig || got.served || !strings.Contains(got.stderr, "refused the token in MM_TOKEN") || !strings.Contains(got.stderr, "mm-mcp login") {
		t.Fatalf("got %+v", got)
	}
}

func TestServeWithTheAPIAddressAsMMURLStillConnects(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)

	got := serveWith(t, map[string]string{config.EnvURL: liveURL + "/api/v4/", config.EnvToken: personalAccessToken(t, admin, user.Id).Token})

	if got.code != cli.ExitOK || !got.served || !strings.Contains(got.stderr, "as @"+user.Username) {
		t.Fatalf("got %+v", got)
	}
}

func TestServeAtAnAddressThatIsNotMattermostStopsAtStart(t *testing.T) {
	t.Parallel()

	got := serveWith(t, map[string]string{config.EnvURL: liveURL + "/not-mattermost", config.EnvToken: "any-token"})

	if got.code != cli.ExitConfig || got.served || !strings.Contains(got.stderr, "does not answer as a Mattermost server") {
		t.Fatalf("got %+v", got)
	}
}
