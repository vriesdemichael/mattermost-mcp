package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

type run struct {
	code    int
	stdout  string
	stderr  string
	served  []cli.ServeOptions
	servers int
}

func runCLI(t *testing.T, env map[string]string, args ...string) run {
	t.Helper()
	var stdout, stderr bytes.Buffer
	result := run{}
	deps := cli.Deps{
		Getenv: testsupport.Env(env),
		Stdout: &stdout,
		Stderr: &stderr,
		// Records how the server would have been started, without starting it.
		Serve: func(_ context.Context, server *mcp.Server, options cli.ServeOptions) error {
			if server != nil {
				result.servers++
			}
			result.served = append(result.served, options)
			return nil
		},
	}
	result.code = cli.Run(t.Context(), args, deps)
	result.stdout, result.stderr = stdout.String(), stderr.String()
	return result
}

var env = map[string]string{config.EnvURL: "https://chat.example.com", config.EnvToken: "token-value"}

func TestServeDefaultsToStdio(t *testing.T) {
	t.Parallel()
	got := runCLI(t, env, "serve")
	if got.code != cli.ExitOK || got.servers != 1 || got.served[0].Transport != "stdio" {
		t.Fatalf("got %+v", got)
	}
}

func TestServeOverHTTPBindsTheLoopbackAddressByDefault(t *testing.T) {
	t.Parallel()
	got := runCLI(t, env, "serve", "--transport", "http")
	if got.code != cli.ExitOK || len(got.served) != 1 || got.served[0] != (cli.ServeOptions{Transport: "http", Address: "127.0.0.1:8765"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestServeOverHTTPRefusesAnAddressBeyondTheMachine(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"0.0.0.0", "192.168.1.10", "chat.example.com", "::"} {
		got := runCLI(t, env, "serve", "--transport", "http", "--host", host)
		if got.code != cli.ExitConfig || len(got.served) != 0 || !strings.Contains(got.stderr, "refusing to bind "+host) {
			t.Errorf("%s: got %+v", host, got)
		}
	}
}

func TestEveryLoopbackSpellingIsAccepted(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"localhost", "127.0.0.1", "::1", "127.0.0.2"} {
		if !cli.IsLoopback(host) {
			t.Errorf("%s is not taken as loopback", host)
		}
	}
}

func TestAConfigurationErrorExits2AndSaysWhatIsMissing(t *testing.T) {
	t.Parallel()
	got := runCLI(t, map[string]string{config.EnvURL: "https://chat.example.com"}, "serve")
	if got.code != cli.ExitConfig || len(got.served) != 0 || !strings.Contains(got.stderr, config.EnvToken) {
		t.Fatalf("got %+v", got)
	}
}

func TestAnUnknownTransportOrArgumentIsRefused(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"serve", "--transport", "sse"},
		{"serve", "extra"},
		{"serve", "--no-such-flag"},
		{"publish"},
		{},
	} {
		if got := runCLI(t, env, args...); got.code != cli.ExitConfig || len(got.served) != 0 {
			t.Errorf("%v: got %+v", args, got)
		}
	}
}

func TestVersionAndHelpPrintAndExit0(t *testing.T) {
	t.Parallel()
	if got := runCLI(t, nil, "version"); got.code != cli.ExitOK || !strings.HasPrefix(got.stdout, "mm-mcp ") {
		t.Errorf("version: got %+v", got)
	}
	if got := runCLI(t, nil, "--help"); got.code != cli.ExitOK || !strings.Contains(got.stdout, "mm-mcp serve") {
		t.Errorf("help: got %+v", got)
	}
}

func TestServeOverHTTPAnswersMCPOnTheLoopbackAddress(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	server := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	stopped := make(chan error, 1)
	go func() { stopped <- cli.Serve(ctx, server, cli.ServeOptions{Transport: "http", Address: "127.0.0.1:0"}) }()
	cancel()
	if err := <-stopped; err != nil {
		t.Fatalf("serving over HTTP did not stop cleanly when its context ended: %v", err)
	}
}

func TestAServerThatCannotBeReachedAtStartIsWarnedAboutAndServed(t *testing.T) {
	t.Parallel()
	got := runCLI(t, env, "serve")
	if got.code != cli.ExitOK || got.servers != 1 || !strings.Contains(got.stderr, "warning: could not reach Mattermost") {
		t.Fatalf("got %+v", got)
	}
}

func TestPlainHTTPBeyondTheMachineIsWarnedAbout(t *testing.T) {
	t.Parallel()
	got := runCLI(t, map[string]string{config.EnvURL: "http://chat.example.com", config.EnvToken: "token-value"}, "serve")
	if got.code != cli.ExitOK || !strings.Contains(got.stderr, "unencrypted") {
		t.Fatalf("got %+v", got)
	}
	got = runCLI(t, map[string]string{config.EnvURL: "http://127.0.0.1:1", config.EnvToken: "token-value"}, "serve")
	if strings.Contains(got.stderr, "unencrypted") {
		t.Fatalf("a loopback address was warned about: %+v", got)
	}
}

func TestACertificateAuthorityFileThatCannotBeUsedStopsTheServer(t *testing.T) {
	t.Parallel()
	notPEM := filepath.Join(t.TempDir(), "company.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notPEM, filepath.Join(t.TempDir(), "missing.pem")} {
		got := runCLI(t, map[string]string{config.EnvURL: "https://chat.example.com", config.EnvToken: "token-value", config.EnvCAFile: path}, "serve")
		if got.code != cli.ExitConfig || got.servers != 0 || !strings.Contains(got.stderr, config.EnvCAFile) {
			t.Errorf("%s: got %+v", path, got)
		}
	}
}

func TestOverHTTPTheAddressIsPrinted(t *testing.T) {
	t.Parallel()
	got := runCLI(t, env, "serve", "--transport", "http", "--port", "9999")
	if !strings.Contains(got.stderr, "http://127.0.0.1:9999/mcp") {
		t.Fatalf("got %+v", got)
	}
}

func TestTheProcessDepsAreTheProcesss(t *testing.T) {
	t.Parallel()
	deps := cli.ProcessDeps()
	if deps.Getenv == nil || deps.Stdout == nil || deps.Stderr == nil || deps.Serve == nil {
		t.Fatalf("got %+v", deps)
	}
}
