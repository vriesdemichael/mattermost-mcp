package config_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

func valid() map[string]string {
	return map[string]string{config.EnvURL: "https://chat.example.com", config.EnvToken: "token-value"}
}

func with(key, value string) map[string]string {
	env := valid()
	env[key] = value
	return env
}

func TestACompleteEnvironmentConfiguresAReadOnlyServer(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromEnv(testsupport.Env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://chat.example.com" || cfg.Token != "token-value" || cfg.AllowWrites {
		t.Fatalf("got %v", cfg)
	}
}

func TestTheURLLosesItsTrailingSlash(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromEnv(testsupport.Env(with(config.EnvURL, "https://chat.example.com/sub/")))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://chat.example.com/sub" {
		t.Fatalf("got %q", cfg.URL)
	}
}

func TestTheURLLosesTheAPIPathMattermostsClientAdds(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"https://chat.example.com/api/v4":      "https://chat.example.com",
		"https://chat.example.com/API/V4/":     "https://chat.example.com",
		"https://chat.example.com/sub/api/v4/": "https://chat.example.com/sub",
		"https://chat.example.com/api/v4x":     "https://chat.example.com/api/v4x",
	} {
		cfg, err := config.FromEnv(testsupport.Env(with(config.EnvURL, raw)))
		if err != nil || cfg.URL != want {
			t.Errorf("%s: got %q, %v; want %q", raw, cfg.URL, err, want)
		}
	}
}

func TestACertificateAuthorityFileIsRead(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromEnv(testsupport.Env(with(config.EnvCAFile, " /etc/ssl/company.pem ")))
	if err != nil || cfg.CAFile != "/etc/ssl/company.pem" {
		t.Fatalf("got %q, %v", cfg.CAFile, err)
	}
}

func TestTheTokenNeverAppearsWhenTheConfigIsPrinted(t *testing.T) {
	t.Parallel()
	cfg, err := config.FromEnv(testsupport.Env(valid()))
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s"} {
		if printed := fmt.Sprintf(verb, cfg); strings.Contains(printed, "token-value") {
			t.Errorf("%s printed the token: %s", verb, printed)
		}
	}
}

func TestAnAddressThatIsNotAPlainWebAddressIsRefusedAndNamed(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"", "   ", "chat.example.com", "ftp://chat.example.com", "https://",
		"https://chat.example.com?a=1", "https://chat.example.com#top",
	} {
		_, err := config.FromEnv(testsupport.Env(with(config.EnvURL, value)))
		if err == nil || !strings.Contains(err.Error(), config.EnvURL) {
			t.Errorf("%q: got %v, want an error naming %s", value, err, config.EnvURL)
		}
	}
}

func TestAMissingTokenIsNamed(t *testing.T) {
	t.Parallel()
	_, err := config.FromEnv(testsupport.Env(with(config.EnvToken, " ")))
	if err == nil || !strings.Contains(err.Error(), config.EnvToken) {
		t.Fatalf("got %v", err)
	}
}

func TestAllowWritesReadsTheUsualSpellings(t *testing.T) {
	t.Parallel()
	for value, want := range map[string]bool{
		"true": true, "1": true, "YES": true, "on": true,
		"false": false, "0": false, "": false, "off": false,
	} {
		cfg, err := config.FromEnv(testsupport.Env(with(config.EnvAllowWrites, value)))
		if err != nil || cfg.AllowWrites != want {
			t.Errorf("%q: got %v, %v; want %v", value, cfg.AllowWrites, err, want)
		}
	}
}

func TestAllowWritesRefusesAValueItCannotRead(t *testing.T) {
	t.Parallel()
	_, err := config.FromEnv(testsupport.Env(with(config.EnvAllowWrites, "maybe")))
	if err == nil || !strings.Contains(err.Error(), config.EnvAllowWrites) {
		t.Fatalf("got %v", err)
	}
}
