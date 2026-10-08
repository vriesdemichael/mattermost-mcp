package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/doctor"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// Nothing listens on port 1, so the server cannot be reached, and the
// checks before it are what a test looks at.
const nobody = "http://127.0.0.1:1"

const hidden = "token-value-doctor-must-not-print"

type store struct {
	tokens map[string]string
	err    error
}

func runDoctor(t *testing.T, env map[string]string, stored store, args ...string) run {
	t.Helper()
	var stdout, stderr bytes.Buffer
	deps := cli.Deps{
		Getenv: testsupport.Env(env),
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: &cli.Credentials{
			Load: func(address string) (string, bool, error) {
				token, ok := stored.tokens[address]
				return token, ok, stored.err
			},
			Where: "the test's store",
		},
		Login: &cli.Login{Browsers: func() ([]login.Browser, error) { return nil, login.ErrNoBrowser }},
	}
	result := run{code: cli.Run(t.Context(), append([]string{"doctor"}, args...), deps)}
	result.stdout, result.stderr = stdout.String(), stderr.String()
	if strings.Contains(result.stdout+result.stderr, hidden) {
		t.Fatalf("doctor printed the token:\n%s%s", result.stdout, result.stderr)
	}
	return result
}

func TestDoctorWithoutAnAddressSaysToGiveOne(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, nil, store{})
	if got.code != cli.ExitFailure || !strings.Contains(got.stdout, "failed   MM_URL: is not set") || !strings.Contains(got.stdout, "--url") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorTakesTheAddressFromURL(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvToken: hidden}, store{}, "--url", nobody)
	if !strings.Contains(got.stdout, "MM_URL: "+nobody+", from --url") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorSaysTheTerminalIsNotTheClient(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: nobody, config.EnvToken: hidden}, store{})
	if !strings.Contains(got.stdout, "environment: read from this terminal") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorFailsOnAServerItCannotReachAndSaysWhy(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: nobody, config.EnvToken: hidden}, store{})
	if got.code != cli.ExitFailure || !strings.Contains(got.stdout, "failed   server:") || !strings.Contains(got.stdout, "skipped  credential:") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorFindsALoginThatMMTokenHides(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: nobody, config.EnvToken: hidden}, store{tokens: map[string]string{nobody: "a-stored-session"}})
	if !strings.Contains(got.stdout, "warning  stored login:") || !strings.Contains(got.stdout, "wins") || strings.Contains(got.stdout, "a-stored-session") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorWithNoLoginAndNoTokenSaysToLogIn(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: nobody}, store{})
	if got.code != cli.ExitFailure || !strings.Contains(got.stdout, "mm-mcp login --url "+nobody) || !strings.Contains(got.stdout, "skipped  server:") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorSaysWhenTheStoreCannotBeRead(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: nobody}, store{err: errors.New("no Secret Service on the bus")})
	if got.code != cli.ExitFailure || !strings.Contains(got.stdout, "no Secret Service on the bus") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorWarnsOfAPlainHTTPAddressBeyondTheMachine(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: "http://chat.example.com", config.EnvToken: hidden}, store{})
	if !strings.Contains(got.stdout, "warning  encryption:") {
		t.Fatalf("got %+v", got)
	}
}

func TestDoctorAsJSONGivesEveryCheck(t *testing.T) {
	t.Parallel()
	got := runDoctor(t, map[string]string{config.EnvURL: nobody, config.EnvToken: hidden}, store{}, "--json")
	var result doctor.Result
	if err := json.Unmarshal([]byte(got.stdout), &result); err != nil {
		t.Fatalf("%v:\n%s", err, got.stdout)
	}
	if got.code != cli.ExitFailure || result.Summary == "" || len(result.Checks) < 5 || result.Checks[0].Name != "mm-mcp" {
		t.Fatalf("got %+v", result)
	}
}

func TestDoctorTakesNoArguments(t *testing.T) {
	t.Parallel()
	if got := runDoctor(t, nil, store{}, "now"); got.code != cli.ExitConfig {
		t.Fatalf("got %+v", got)
	}
}

func TestHelpNamesDoctor(t *testing.T) {
	t.Parallel()
	if got := runCLI(t, nil, "help"); !strings.Contains(got.stdout, "mm-mcp doctor") {
		t.Fatalf("got %+v", got)
	}
}
