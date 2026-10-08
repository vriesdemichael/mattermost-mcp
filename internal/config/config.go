// Package config reads what the server is configured with from its environment.
//
// The environment is the only source today. No flag carries a credential
// (ADR-019): a flag is visible in the process list and in the MCP client's
// argument array, where the env block is not.
package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// The variables mm-mcp reads.
const (
	EnvURL                  = "MM_URL"
	EnvToken                = "MM_TOKEN"
	EnvAllowWrites          = "MM_MCP_ALLOW_WRITES"
	EnvDownloadDir          = "MM_MCP_DOWNLOAD_DIR"
	EnvMarkAIGenerated      = "MM_MCP_MARK_AI_GENERATED"
	EnvBlockExternalNetwork = "MM_MCP_BLOCK_EXTERNAL_NETWORK"
	EnvCAFile               = "MM_MCP_CA_FILE"
)

// EnvironmentVariables is every variable mm-mcp reads. The unit-test seal
// empties each of them, and a governance test fails when the source names one
// that is not listed here (ADR-006).
var EnvironmentVariables = []string{EnvURL, EnvToken, EnvAllowWrites, EnvDownloadDir, EnvMarkAIGenerated, EnvBlockExternalNetwork, EnvCAFile}

// Config is a server's configuration.
type Config struct {
	URL   string
	Token string
	// TokenStored says the token is the one `mm-mcp login` stored, MM_TOKEN
	// being unset.
	TokenStored bool
	// Stored is the credential store the token was looked for in, or nil. The
	// diagnose tool reads it to find a login MM_TOKEN hides.
	Stored      Stored
	AllowWrites bool
	// MarkAIGenerated marks every post and edit the model writes as written
	// with AI, as Mattermost shows it. On unless turned off.
	MarkAIGenerated bool
	// DownloadDir is where save_file writes; empty means the person's
	// Downloads directory.
	DownloadDir string
	// CAFile is a PEM file of certificate authorities to trust beside the
	// system's, for a server whose certificate an organisation signed itself.
	CAFile string
	// Local says the server runs on the person's own machine, for the one client
	// that started it over stdio. It comes from how the server is served, not
	// from the environment, and only a local server offers the tools that read
	// or write this machine's files.
	Local bool
}

// String leaves the token out, so a Config can be logged or printed in an error.
func (c Config) String() string {
	return fmt.Sprintf("Config{URL: %q, AllowWrites: %t, MarkAIGenerated: %t, DownloadDir: %q, CAFile: %q, Local: %t}", c.URL, c.AllowWrites, c.MarkAIGenerated, c.DownloadDir, c.CAFile, c.Local)
}

// GoString is String, so %#v leaves the token out too.
func (c Config) GoString() string { return c.String() }

// Error says what in the environment keeps the server from starting.
type Error struct{ msg string }

func (e *Error) Error() string { return e.msg }

func errorf(format string, args ...any) error { return &Error{msg: fmt.Sprintf(format, args...)} }

// FromEnv reads a Config through lookup, which returns "" for a variable that is not set.
func FromEnv(lookup func(string) string) (Config, error) {
	return Load(lookup, nil)
}

// Stored finds the token `mm-mcp login` stored for the server at an address,
// and says whether there is one.
type Stored func(address string) (token string, found bool, err error)

// Load is FromEnv, with the token stored for MM_URL used when MM_TOKEN is not
// set (ADR-019). stored may be nil, and then MM_TOKEN is required.
func Load(lookup func(string) string, stored Stored) (Config, error) {
	lookup = expanded(lookup)
	address, err := parseURL(lookup(EnvURL))
	if err != nil {
		return Config{}, err
	}
	token := strings.TrimSpace(lookup(EnvToken))
	fromStore := false
	if token == "" && stored != nil {
		found := false
		token, found, err = stored(address)
		switch {
		case err != nil:
			return Config{}, errorf("%s is not set, and the token `mm-mcp login` stores could not be read: %v.", EnvToken, err)
		case found:
			fromStore = true
		}
	}
	if token == "" {
		return Config{}, errorf("%s is not set, and no login is stored for %s. Run `mm-mcp login --url %s` once, or put a personal access token, a bot token or a session token in the MCP client's env block for this server.", EnvToken, address, address)
	}
	allowWrites, err := ParseBool(EnvAllowWrites, lookup(EnvAllowWrites))
	if err != nil {
		return Config{}, err
	}
	markAI := true
	if raw := lookup(EnvMarkAIGenerated); strings.TrimSpace(raw) != "" {
		if markAI, err = ParseBool(EnvMarkAIGenerated, raw); err != nil {
			return Config{}, err
		}
	}
	downloadDir := strings.TrimSpace(lookup(EnvDownloadDir))
	if downloadDir != "" && !filepath.IsAbs(downloadDir) {
		// A relative directory would be the MCP client's working directory,
		// which nobody chose and few could name.
		return Config{}, errorf("%s must be a full path, not %q.", EnvDownloadDir, downloadDir)
	}
	return Config{
		URL: address, Token: token, TokenStored: fromStore, Stored: stored, AllowWrites: allowWrites, MarkAIGenerated: markAI,
		DownloadDir: downloadDir,
		CAFile:      strings.TrimSpace(lookup(EnvCAFile)),
	}, nil
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

// ParseURL reads an MM_URL value: an http or https address, without a query
// or the API's own /api/v4.
func ParseURL(raw string) (string, error) { return parseURL(raw) }

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
	// The API's address is a common answer to "what is the server's address",
	// and Mattermost's client adds /api/v4 itself.
	address := strings.TrimRight(raw, "/")
	if strings.HasSuffix(strings.ToLower(address), apiPath) {
		address = strings.TrimRight(address[:len(address)-len(apiPath)], "/")
	}
	return address, nil
}

// apiPath is where Mattermost serves its REST API, below its address.
const apiPath = "/api/v4"

// expanded reads a variable an MCP client left as a placeholder, such as
// ${user_config.mm_token} for a setting the person left empty, as unset.
func expanded(lookup func(string) string) func(string) string {
	return func(name string) string {
		value := lookup(name)
		if Placeholder(value) {
			return ""
		}
		return value
	}
}

// Placeholder reports whether value is a placeholder an MCP client left
// unexpanded, which mm-mcp reads as unset.
func Placeholder(value string) bool {
	trimmed := strings.TrimSpace(value)
	return strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}")
}
