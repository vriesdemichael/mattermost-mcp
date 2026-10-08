package doctor

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

func TestMain(m *testing.M) { testsupport.SealedMain(m) }

const secret = "the-token-no-check-may-show"

func find(t *testing.T, checks []Check, name string) Check {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("no %s check among %+v", name, checks)
	return Check{}
}

func storeOf(found bool, err error) Store {
	return Store{
		Load: func(string) (string, bool, error) {
			if found {
				return secret, true, err
			}
			return "", false, err
		},
		Where: "the test's store",
	}
}

func TestALoginIsCheckedForEveryWayTheStoreAndMMTokenCanStand(t *testing.T) {
	t.Parallel()
	const address = "https://Chat.example.com/"
	cases := []struct {
		name     string
		found    bool
		err      error
		tokenSet bool
		status   Status
		says     string
	}{
		{"stored and used", true, nil, false, OK, "a login is stored"},
		{"stored and hidden by MM_TOKEN", true, nil, true, Warning, "MM_TOKEN is set, and wins"},
		{"none, and MM_TOKEN set", false, nil, true, OK, "none is needed"},
		{"none, and no MM_TOKEN", false, nil, false, Failed, "MM_TOKEN is not set"},
		{"the store fails, MM_TOKEN set", false, errors.New("no Secret Service"), true, Warning, "could not be read"},
		{"the store fails, no MM_TOKEN", false, errors.New("no Secret Service"), false, Failed, "could not be read"},
	}
	for _, c := range cases {
		check := Login(address, c.tokenSet, storeOf(c.found, c.err))[0]
		if check.Status != c.status || !strings.Contains(check.Detail, c.says) {
			t.Errorf("%s: got %+v", c.name, check)
		}
		if c.status != OK && check.Next == "" {
			t.Errorf("%s: a %s check says nothing to do", c.name, c.status)
		}
		if strings.Contains(check.Detail+check.Next, secret) {
			t.Errorf("%s: the check shows the token", c.name)
		}
	}
}

func TestALoginNamesTheKeyItWasLookedForUnder(t *testing.T) {
	t.Parallel()
	check := Login("https://Chat.Example.com/", false, storeOf(false, nil))[0]
	if !strings.Contains(check.Detail, "under https://chat.example.com,") || !strings.Contains(check.Detail, "the test's store") {
		t.Fatalf("got %+v", check)
	}
}

func TestALoginWithoutAStoreIsSkipped(t *testing.T) {
	t.Parallel()
	if check := Login("https://chat.example.com", false, Store{})[0]; check.Status != Skipped {
		t.Fatalf("got %+v", check)
	}
}

func TestTheOAuthClientALoginUsedIsNamed(t *testing.T) {
	t.Parallel()
	store := storeOf(true, nil)
	store.LoadClient = func(string) (string, bool, error) {
		return `{"client_id":"abc123","callback":"http://127.0.0.1:8766/callback"}`, true, nil
	}
	checks := Login("https://chat.example.com", false, store)
	if got := find(t, checks, "OAuth client"); !strings.Contains(got.Detail, "abc123") {
		t.Fatalf("got %+v", got)
	}
}

func TestStoreAdviceFitsTheSystem(t *testing.T) {
	t.Parallel()
	for goos, says := range map[string]string{"windows": "Credential Manager", "darwin": "keychain", "linux": "D-Bus"} {
		if advice := storeAdvice(goos); !strings.Contains(advice, says) || !strings.Contains(advice, config.EnvToken) {
			t.Errorf("%s: %s", goos, advice)
		}
	}
}

func TestSettingsSayWhatEachVariableDoes(t *testing.T) {
	t.Parallel()
	checks := Settings(testsupport.Env(map[string]string{
		config.EnvURL:                  "https://chat.example.com/api/v4",
		config.EnvToken:                secret,
		config.EnvAllowWrites:          "maybe",
		config.EnvBlockExternalNetwork: "1",
	}), false)
	if got := find(t, checks, config.EnvURL); got.Status != OK || !strings.Contains(got.Detail, "https://chat.example.com, from the environment") {
		t.Errorf("MM_URL: %+v", got)
	}
	if got := find(t, checks, config.EnvToken); got.Status != OK || strings.Contains(got.Detail, secret) {
		t.Errorf("MM_TOKEN: %+v", got)
	}
	if got := find(t, checks, config.EnvAllowWrites); got.Status != Failed {
		t.Errorf("MM_MCP_ALLOW_WRITES: %+v", got)
	}
	if got := find(t, checks, config.EnvBlockExternalNetwork); got.Status != Warning {
		t.Errorf("MM_MCP_BLOCK_EXTERNAL_NETWORK: %+v", got)
	}
}

func TestAPlaceholderReadsAsUnsetAndIsNamed(t *testing.T) {
	t.Parallel()
	checks := Settings(testsupport.Env(map[string]string{
		config.EnvURL:   "${user_config.mm_url}",
		config.EnvToken: "${user_config.mm_token}",
	}), false)
	if got := find(t, checks, config.EnvURL); got.Status != Failed || !strings.Contains(got.Detail, "${user_config.mm_url}") {
		t.Errorf("MM_URL: %+v", got)
	}
	if got := find(t, checks, config.EnvToken); got.Status != OK || !strings.Contains(got.Detail, "placeholder") {
		t.Errorf("MM_TOKEN: %+v", got)
	}
}

func TestAnUnsetAddressSaysToPassURL(t *testing.T) {
	t.Parallel()
	got := find(t, Settings(testsupport.Env(nil), false), config.EnvURL)
	if got.Status != Failed || !strings.Contains(got.Next, "--url") {
		t.Fatalf("got %+v", got)
	}
}

func TestAnAddressGivenWithURLSaysSo(t *testing.T) {
	t.Parallel()
	got := find(t, Settings(testsupport.Env(map[string]string{config.EnvURL: "https://chat.example.com"}), true), config.EnvURL)
	if !strings.Contains(got.Detail, "from --url") {
		t.Fatalf("got %+v", got)
	}
}

func TestTheFilesTheSettingsNameAreChecked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	checks := Settings(testsupport.Env(map[string]string{
		config.EnvURL:         "https://chat.example.com",
		config.EnvCAFile:      notPEM,
		config.EnvDownloadDir: filepath.Join(dir, "missing"),
	}), false)
	if got := find(t, checks, config.EnvCAFile); got.Status != Failed || !strings.Contains(got.Detail, "no PEM-encoded certificate") {
		t.Errorf("CA file: %+v", got)
	}
	if got := find(t, checks, config.EnvDownloadDir); got.Status != Warning {
		t.Errorf("download directory: %+v", got)
	}
	relative := find(t, Settings(testsupport.Env(map[string]string{config.EnvDownloadDir: "downloads"}), false), config.EnvDownloadDir)
	if relative.Status != Failed {
		t.Errorf("relative download directory: %+v", relative)
	}
}

func TestAProxyIsNamedWithoutItsPassword(t *testing.T) {
	t.Parallel()
	proxy, err := url.Parse("http://someone:hunter2@proxy.example.com:3128")
	if err != nil {
		t.Fatal(err)
	}
	check := proxyCheck("https://chat.example.com", proxy, nil)
	if check == nil || strings.Contains(check.Detail, "hunter2") || strings.Contains(check.Detail, "someone") || !strings.Contains(check.Detail, "proxy.example.com:3128") {
		t.Fatalf("got %+v", check)
	}
	if proxyCheck("https://chat.example.com", nil, nil) != nil {
		t.Fatal("no proxy is not a check")
	}
	if failed := proxyCheck("https://chat.example.com", nil, errors.New("invalid proxy address \"http://someone:hunter2@x\"")); failed.Status != Failed || strings.Contains(failed.Detail, "hunter2") {
		t.Fatalf("got %+v", failed)
	}
}

func TestEachWayAServerCannotBeReachedHasItsOwnAdvice(t *testing.T) {
	t.Parallel()
	const address = "https://chat.example.com"
	wrap := func(err error) error {
		return fmt.Errorf("could not reach Mattermost: %w", &url.Error{Op: "Get", URL: address, Err: err})
	}
	cases := map[string]struct {
		err  error
		says string
	}{
		"DNS":               {&net.DNSError{Err: "no such host", Name: "chat.example.com", IsNotFound: true}, "does not resolve"},
		"refused":           {&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, "accepted the connection"},
		"proxy":             {&net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New("connection refused")}, "proxy"},
		"unknown authority": {x509.UnknownAuthorityError{}, config.EnvCAFile},
		"another name":      {x509.HostnameError{Host: "chat.example.com", Certificate: &x509.Certificate{}}, "another name"},
		"expired":           {x509.CertificateInvalidError{Reason: x509.Expired, Cert: &x509.Certificate{}}, "expired"},
		"timeout":           {context.DeadlineExceeded, "in time"},
		"blocked":           {&network.ExternalNetworkBlockedError{Host: "chat.example.com"}, config.EnvBlockExternalNetwork},
	}
	for name, c := range cases {
		if advice := Unreachable(wrap(c.err), address); !strings.Contains(advice, c.says) {
			t.Errorf("%s: %s", name, advice)
		}
	}
}

func TestAReportCountsWhatFailed(t *testing.T) {
	t.Parallel()
	var report Report
	if got := report.Result(); got.Checks == nil || got.Summary != "Every check passed." {
		t.Fatalf("an empty report: %+v", got)
	}
	report.Add(ok("a", "fine"), Check{Name: "b", Status: Warning, Detail: "hm"})
	if report.Failed() || !strings.Contains(report.Summary(), "1 warning") {
		t.Fatalf("got %s", report.Summary())
	}
	report.Add(Check{Name: "c", Status: Failed, Detail: "broken", Next: "fix it"})
	var printed bytes.Buffer
	report.Write(&printed)
	if !report.Failed() || !strings.Contains(printed.String(), "failed   c: broken\n         -> fix it\n") || !strings.Contains(printed.String(), "1 check failed") {
		t.Fatalf("got\n%s", printed.String())
	}
}

func TestTheLoadedConfigurationNamesItsCredentialWithoutShowingIt(t *testing.T) {
	t.Parallel()
	checks := Loaded(config.Config{URL: "https://chat.example.com", Token: secret, TokenStored: true, MarkAIGenerated: true, Local: true}, "the test's store")
	for _, check := range checks {
		if strings.Contains(check.Detail, secret) {
			t.Fatalf("%s shows the token", check.Name)
		}
	}
	if got := find(t, checks, "credential source"); !strings.Contains(got.Detail, "the login `mm-mcp login` stored") {
		t.Fatalf("got %+v", got)
	}
}

// Who asks before a write is reported as mm-mcp reads it, and the one
// combination mm-mcp refuses fails with what to change (ADR-033).
func TestSettingsSayWhoAsksBeforeAWrite(t *testing.T) {
	t.Parallel()
	settings := func(ask, force string) []Check {
		return Settings(testsupport.Env(map[string]string{
			config.EnvURL: "https://chat.example.com", config.EnvAllowWrites: "true",
			config.EnvAskBeforeWrites: ask, config.EnvForceHumanInTheLoop: force,
		}), false)
	}
	checks := settings("", "")
	if got := find(t, checks, config.EnvAllowWrites); !strings.Contains(got.Detail, "asks first") {
		t.Errorf("asking by default: %+v", got)
	}
	for _, name := range []string{config.EnvAskBeforeWrites, config.EnvForceHumanInTheLoop} {
		for _, check := range checks {
			if check.Name == name {
				t.Errorf("unset, and still reported: %+v", check)
			}
		}
	}
	checks = settings("false", "")
	if got := find(t, checks, config.EnvAllowWrites); !strings.Contains(got.Detail, "client's own approval") {
		t.Errorf("not asking: %+v", got)
	}
	if got := find(t, checks, config.EnvAskBeforeWrites); got.Status != OK || !strings.Contains(got.Detail, "asks nothing") {
		t.Errorf("not asking: %+v", got)
	}
	if got := find(t, settings("false", "true"), config.EnvForceHumanInTheLoop); got.Status != OK || !strings.Contains(got.Detail, "on every call") {
		t.Errorf("forcing a human in the loop: %+v", got)
	}
	for _, ask := range []string{"", "true"} {
		got := find(t, settings(ask, "true"), config.EnvForceHumanInTheLoop)
		if got.Status != Failed || !strings.Contains(got.Next, config.EnvAskBeforeWrites+"=false") {
			t.Errorf("forcing while asking %q: %+v", ask, got)
		}
	}
}

func TestTheLoadedConfigurationSaysWhoAsksBeforeAWrite(t *testing.T) {
	t.Parallel()
	loaded := func(skip, force bool) []Check {
		return Loaded(config.Config{URL: "https://chat.example.com", AllowWrites: true, SkipAsking: skip, ForceHumanInTheLoop: force}, "")
	}
	if got := find(t, loaded(false, false), config.EnvAllowWrites); !strings.Contains(got.Detail, "asks first") {
		t.Errorf("asking: %+v", got)
	}
	if got := find(t, loaded(true, false), config.EnvAskBeforeWrites); !strings.Contains(got.Detail, "the only check") {
		t.Errorf("not asking: %+v", got)
	}
	if got := find(t, loaded(true, true), config.EnvForceHumanInTheLoop); !strings.Contains(got.Detail, "on every call") {
		t.Errorf("forcing a human in the loop: %+v", got)
	}
}
