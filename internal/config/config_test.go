package config_test

import (
	"fmt"
	"path/filepath"
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

func TestARelativeDownloadDirectoryIsRefused(t *testing.T) {
	t.Parallel()
	if _, err := config.FromEnv(testsupport.Env(with(config.EnvDownloadDir, "downloads"))); err == nil || !strings.Contains(err.Error(), "full path") {
		t.Fatalf("got %v", err)
	}
	absolute, _ := filepath.Abs("downloads")
	if cfg, err := config.FromEnv(testsupport.Env(with(config.EnvDownloadDir, absolute))); err != nil || cfg.DownloadDir != absolute {
		t.Fatalf("got %q, %v", cfg.DownloadDir, err)
	}
}

func TestAStoredLoginIsUsedOnlyWhenNoTokenIsSet(t *testing.T) {
	t.Parallel()
	stored := func(address string) (string, bool, error) {
		if address == "https://chat.example.com" {
			return "stored-token", true, nil
		}
		return "", false, nil
	}
	noToken := map[string]string{config.EnvURL: "https://chat.example.com/"}

	cfg, err := config.Load(testsupport.Env(noToken), stored)
	if err != nil || cfg.Token != "stored-token" || !cfg.TokenStored {
		t.Fatalf("with a stored login: %v, %v", cfg, err)
	}
	cfg, err = config.Load(testsupport.Env(valid()), stored)
	if err != nil || cfg.Token != "token-value" || cfg.TokenStored {
		t.Fatalf("with MM_TOKEN set: %v, %v", cfg, err)
	}
	_, err = config.Load(testsupport.Env(map[string]string{config.EnvURL: "https://other.example.com"}), stored)
	if err == nil || !strings.Contains(err.Error(), "mm-mcp login --url https://other.example.com") {
		t.Fatalf("with nothing stored: %v", err)
	}
	_, err = config.Load(testsupport.Env(noToken), func(string) (string, bool, error) { return "", false, fmt.Errorf("locked") })
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("with the store unreadable: %v", err)
	}
}

func TestAPlaceholderTheClientLeftIsReadAsUnset(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(testsupport.Env(map[string]string{
		config.EnvURL: "https://chat.example.com", config.EnvToken: "${user_config.mm_token}", config.EnvAllowWrites: "${user_config.allow_writes}",
	}), func(string) (string, bool, error) { return "stored", true, nil })
	if err != nil || cfg.Token != "stored" || !cfg.TokenStored || cfg.AllowWrites {
		t.Fatalf("got %v, %v", cfg, err)
	}
}
