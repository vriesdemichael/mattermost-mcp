//go:build live

package live

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/cli"
	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/doctor"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

// mm-mcp doctor and the diagnose tool, against the live instance. Neither may
// ever show the credential it checks.

// doctorRun runs `mm-mcp doctor --json` with env, and a credential store
// holding stored, and fails the test if the output shows a token.
func doctorRun(t *testing.T, env map[string]string, stored map[string]string, tokens ...string) (int, doctor.Result) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Run(t.Context(), []string{"doctor", "--json"}, cli.Deps{
		Getenv: testsupport.Env(env),
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: &cli.Credentials{
			Load:  func(address string) (string, bool, error) { token, ok := stored[address]; return token, ok, nil },
			Where: "the test's store",
		},
	})
	for _, token := range tokens {
		if strings.Contains(stdout.String()+stderr.String(), token) {
			t.Fatalf("doctor printed a token:\n%s%s", stdout.String(), stderr.String())
		}
	}
	var result doctor.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("%v:\n%s%s", err, stdout.String(), stderr.String())
	}
	return code, result
}

func checkNamed(t *testing.T, result doctor.Result, name string) doctor.Check {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("no %s check in %+v", name, result.Checks)
	return doctor.Check{}
}

func TestDoctorWithAWorkingTokenPassesAndSaysWhoItActsAs(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	token := personalAccessToken(t, admin, user.Id).Token

	code, result := doctorRun(t, map[string]string{config.EnvURL: liveURL, config.EnvToken: token}, nil, token)

	if code != cli.ExitOK || result.Summary == "" {
		t.Fatalf("exit %d: %+v", code, result)
	}
	if got := checkNamed(t, result, "credential"); got.Status != doctor.OK || !strings.Contains(got.Detail, "@"+user.Username) {
		t.Errorf("credential: %+v", got)
	}
	if got := checkNamed(t, result, "teams"); got.Status != doctor.OK || !strings.Contains(got.Detail, team.DisplayName) {
		t.Errorf("teams: %+v", got)
	}
	// The live instance lets OAuth clients register themselves.
	if got := checkNamed(t, result, "login"); !strings.Contains(got.Detail, "OAuth in your own browser") {
		t.Errorf("login: %+v", got)
	}
}

func TestDoctorWithARefusedTokenFailsAndSaysWhatToDo(t *testing.T) {
	t.Parallel()
	const token = "not-a-token-mattermost-issued"

	code, result := doctorRun(t, map[string]string{config.EnvURL: liveURL, config.EnvToken: token}, nil, token)

	got := checkNamed(t, result, "credential")
	if code != cli.ExitFailure || got.Status != doctor.Failed || !strings.Contains(got.Detail, "refused the token in MM_TOKEN") || !strings.Contains(got.Next, "mm-mcp login") {
		t.Fatalf("exit %d: %+v", code, got)
	}
}

func TestDoctorWithAStoredLoginChecksIt(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	seedTeam(t, admin, user)
	session := sessionToken(t, user)

	code, result := doctorRun(t, map[string]string{config.EnvURL: liveURL}, map[string]string{liveURL: session}, session)

	if got := checkNamed(t, result, "stored login"); got.Status != doctor.OK {
		t.Errorf("stored login: %+v", got)
	}
	if got := checkNamed(t, result, "credential"); code != cli.ExitOK || got.Status != doctor.OK || !strings.Contains(got.Detail, "@"+user.Username) {
		t.Fatalf("exit %d: %+v", code, got)
	}
}

func TestDoctorFindsAStoredLoginMMTokenHides(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	seedTeam(t, admin, user)
	session := sessionToken(t, user)
	other := seedUser(t, admin)
	seedTeam(t, admin, other)
	token := personalAccessToken(t, admin, other.Id).Token

	_, result := doctorRun(t, map[string]string{config.EnvURL: liveURL, config.EnvToken: token}, map[string]string{liveURL: session}, session, token)

	if got := checkNamed(t, result, "stored login"); got.Status != doctor.Warning || !strings.Contains(got.Detail, "wins") {
		t.Errorf("stored login: %+v", got)
	}
	if got := checkNamed(t, result, "credential"); !strings.Contains(got.Detail, "@"+other.Username) {
		t.Errorf("credential: %+v", got)
	}
}

func TestDoctorAtAnAddressThatIsNotMattermostFails(t *testing.T) {
	t.Parallel()

	code, result := doctorRun(t, map[string]string{config.EnvURL: liveURL + "/not-mattermost", config.EnvToken: "any-token"}, nil)

	if got := checkNamed(t, result, "server"); code != cli.ExitFailure || got.Status != doctor.Failed || !strings.Contains(got.Detail, "does not answer as a Mattermost server") {
		t.Fatalf("exit %d: %+v", code, got)
	}
}

// diagnosed calls the diagnose tool and reads its answer, failing the test if
// it shows token.
func diagnosed(t *testing.T, session *mcp.ClientSession, token string, askTestQuestion bool) doctor.Result {
	t.Helper()
	result := callTool(t, session, &mcp.CallToolParams{Name: "diagnose", Arguments: map[string]any{"ask_test_question": askTestQuestion}})
	raw, err := json.Marshal(result)
	check(t, err)
	if strings.Contains(string(raw), token) {
		t.Fatalf("diagnose returned the token: %s", raw)
	}
	var answer doctor.Result
	structured(t, result, &answer)
	return answer
}

func TestDiagnoseChecksTheServerItRunsAs(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	token := personalAccessToken(t, admin, user.Id).Token

	result := diagnosed(t, mcpAs(t, token), token, false)

	for _, check := range result.Checks {
		if check.Status == doctor.Failed {
			t.Errorf("%s failed: %+v", check.Name, check)
		}
	}
	if got := checkNamed(t, result, "credential"); !strings.Contains(got.Detail, "@"+user.Username) {
		t.Errorf("credential: %+v", got)
	}
	if got := checkNamed(t, result, "teams"); !strings.Contains(got.Detail, team.DisplayName) {
		t.Errorf("teams: %+v", got)
	}
	if got := checkNamed(t, result, "MCP client"); !strings.Contains(got.Detail, "live 0") {
		t.Errorf("MCP client: %+v", got)
	}
	if got := checkNamed(t, result, "confirmations"); got.Status != doctor.OK || !strings.Contains(got.Detail, "writes are not allowed") {
		t.Errorf("confirmations: %+v", got)
	}
}

func TestDiagnoseSaysAClientThatCannotAskCannotWrite(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	token := personalAccessToken(t, admin, user.Id).Token

	result := diagnosed(t, mcpWriting(t, token, nil), token, true)

	if got := checkNamed(t, result, "confirmations"); got.Status != doctor.Failed || !strings.Contains(got.Next, "save_draft") {
		t.Errorf("confirmations: %+v", got)
	}
	if got := checkNamed(t, result, "test question"); got.Status != doctor.Skipped {
		t.Errorf("test question: %+v", got)
	}
}

func TestDiagnoseAsksATestQuestionAPersonAnswers(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	token := personalAccessToken(t, admin, user.Id).Token
	asked := 0
	session := mcpWriting(t, token, func(params *mcp.ElicitParams) *mcp.ElicitResult {
		asked++
		if !strings.Contains(params.Message, "Nothing will be posted") {
			t.Errorf("the question says %q", params.Message)
		}
		return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}
	})

	result := diagnosed(t, session, token, true)

	if got := checkNamed(t, result, "test question"); asked != 1 || got.Status != doctor.OK {
		t.Fatalf("asked %d times: %+v", asked, got)
	}
}

func TestDiagnoseTellsAQuestionTheClientDeclinedAtOnce(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	token := personalAccessToken(t, admin, user.Id).Token
	session := mcpWriting(t, token, func(*mcp.ElicitParams) *mcp.ElicitResult {
		return &mcp.ElicitResult{Action: "decline"}
	})

	result := diagnosed(t, session, token, true)

	if got := checkNamed(t, result, "test question"); got.Status != doctor.Failed || !strings.Contains(got.Detail, "answered it itself") ||
		!strings.Contains(got.Next, config.EnvAskBeforeWrites+"=false") {
		t.Fatalf("test question: %+v", got)
	}
}

// A server that leaves the asking to the client says so, whatever the client
// can show (ADR-033).
func TestDiagnoseSaysWhoAsksWhenMmMcpDoesNot(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	token := personalAccessToken(t, admin, user.Id).Token
	for _, c := range []struct {
		force  bool
		status doctor.Status
		says   string
	}{{false, doctor.OK, "the only check"}, {true, doctor.OK, "on every call"}} {
		session := mcpWith(t, config.Config{URL: liveURL, Token: token, AllowWrites: true, SkipAsking: true, ForceHumanInTheLoop: c.force}, nil)

		result := diagnosed(t, session, token, false)

		if got := checkNamed(t, result, "confirmations"); got.Status != c.status || !strings.Contains(got.Detail, c.says) {
			t.Errorf("forcing a human in the loop %t: %+v", c.force, got)
		}
	}
}
