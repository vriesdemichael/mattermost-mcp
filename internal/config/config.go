// Package config reads what the server is configured with from its environment.
//
// The environment is the only source today. No flag carries a credential
// (ADR-019): a flag is visible in the process list and in the MCP client's
// argument array, where the env block is not.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// The variables mm-mcp reads.
const (
	EnvURL                  = "MM_URL"
	EnvToken                = "MM_TOKEN"
	EnvAllowWrites          = "MM_MCP_ALLOW_WRITES"
	EnvDownloadDir          = "MM_MCP_DOWNLOAD_DIR"
	EnvBlockExternalNetwork = "MM_MCP_BLOCK_EXTERNAL_NETWORK"
)

// EnvironmentVariables is every variable mm-mcp reads. The unit-test seal
// empties each of them, and a governance test fails when the source names one
// that is not listed here (ADR-006).
var EnvironmentVariables = []string{EnvURL, EnvToken, EnvAllowWrites, EnvDownloadDir, EnvBlockExternalNetwork}

// Config is a server's configuration.
type Config struct {
	URL         string
	Token       string
	AllowWrites bool
	// DownloadDir is where save_file writes; empty means the person's
	// Downloads directory.
	DownloadDir string
	// Local says the server runs on the person's own machine, for the one client
	// that started it over stdio. It comes from how the server is served, not
	// from the environment, and only a local server offers the tools that read
	// or write this machine's files.
	Local bool
}

// String leaves the token out, so a Config can be logged or printed in an error.
func (c Config) String() string {
	return fmt.Sprintf("Config{URL: %q, AllowWrites: %t, DownloadDir: %q, Local: %t}", c.URL, c.AllowWrites, c.DownloadDir, c.Local)
}

// GoString is String, so %#v leaves the token out too.
func (c Config) GoString() string { return c.String() }

// Error says what in the environment keeps the server from starting.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &Error{msg: fmt.Sprintf(format, args...)} }

// FromEnv reads a Config through lookup, which returns "" for a variable that is not set.
func FromEnv(lookup func(string) string) (Config, error) {
	address, err := parseURL(lookup(EnvURL))
	if err != nil {
		return Config{}, err
	}
	token := strings.TrimSpace(lookup(EnvToken))
	if token == "" {
		return Config{}, errorf("%s is not set. Put a personal access token, a bot token or a session token in the MCP client's env block for this server.", EnvToken)
	}
	allowWrites, err := ParseBool(EnvAllowWrites, lookup(EnvAllowWrites))
	if err != nil {
		return Config{}, err
	}
	return Config{URL: address, Token: token, AllowWrites: allowWrites, DownloadDir: strings.TrimSpace(lookup(EnvDownloadDir))}, nil
}

// ParseBool reads the usual spellings of true and false; empty is false.
func ParseBool(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "", "0", "false", "no", "off":
		return false, nil
	default:
		return false, errorf("%s must be true or false, not %q.", name, raw)
	}
}

func parseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errorf("%s is not set. Set it to the Mattermost server's address.", EnvURL)
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return "", errorf("%s must be an http or https address, not %q.", EnvURL, raw)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(raw, "#") || strings.Contains(raw, "?") {
		return "", errorf("%s must not carry a query or a fragment: %q.", EnvURL, raw)
	}
	return strings.TrimRight(raw, "/"), nil
}
