package doctor

import (
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vriesdemichael/mm-mcp/internal/config"
)

// Terminal is the check that says whose environment `mm-mcp doctor` read: the
// terminal's, which is not the one an MCP client starts mm-mcp with.
func Terminal() Check {
	return ok("environment", "read from this terminal. Your MCP client starts mm-mcp with the env block in its own configuration, "+
		"which this terminal does not see; where the two differ, the diagnose tool, called from the client, checks the client's.")
}

// Settings checks each variable mm-mcp reads, through lookup, as mm-mcp serve
// reads them. urlGiven says MM_URL came from --url. A variable that is unset
// and has a default is left out, except MM_MCP_ALLOW_WRITES, which decides
// whether anything can be written at all.
func Settings(lookup func(string) string, urlGiven bool) []Check {
	checks := []Check{address(lookup(config.EnvURL), urlGiven), token(lookup(config.EnvToken))}
	askRaw, forceRaw := given(lookup(config.EnvAskBeforeWrites)), given(lookup(config.EnvForceHumanInTheLoop))
	ask, askErr := true, error(nil)
	if askRaw != "" {
		ask, askErr = config.ParseBool(config.EnvAskBeforeWrites, askRaw)
	}
	writes := "writes are allowed: the tools that post and change Mattermost are offered, and each change others see asks first"
	if askErr == nil && !ask {
		writes = "writes are allowed: the tools that post and change Mattermost are offered, and the MCP client's own approval is the check before each change others see"
	}
	checks = append(checks, boolean(config.EnvAllowWrites, lookup(config.EnvAllowWrites), false,
		writes, "writes are not allowed: only the tools that read are offered"))
	if askRaw != "" {
		checks = append(checks, boolean(config.EnvAskBeforeWrites, askRaw, true,
			"mm-mcp asks you before each change others see",
			"mm-mcp asks nothing before a change others see: the MCP client's own approval of each tool call is the only check, "+
				"and a client that approves tools by itself posts as you with nobody seeing it first"))
	}
	if forceRaw != "" {
		checks = append(checks, forced(forceRaw, ask))
	}
	if raw := lookup(config.EnvMarkAIGenerated); strings.TrimSpace(raw) != "" {
		checks = append(checks, boolean(config.EnvMarkAIGenerated, raw, true,
			"posts and edits the model writes are marked as written with AI",
			"posts and edits the model writes are not marked as written with AI"))
	}
	if raw := strings.TrimSpace(lookup(config.EnvDownloadDir)); raw != "" && !config.Placeholder(raw) {
		checks = append(checks, downloadDir(raw))
	}
	if raw := strings.TrimSpace(lookup(config.EnvCAFile)); raw != "" && !config.Placeholder(raw) {
		checks = append(checks, caFile(raw))
	}
	if lookup(config.EnvBlockExternalNetwork) == "1" {
		checks = append(checks, Check{
			Name: config.EnvBlockExternalNetwork, Status: Warning,
			Detail: "is 1, so mm-mcp reaches no address but this machine's",
			Next:   "It is for mm-mcp's own tests; unset it.",
		})
	}
	return checks
}

// given is a variable's value, or "" when it is unset or a placeholder the
// MCP client left unexpanded.
func given(raw string) string {
	if config.Placeholder(raw) {
		return ""
	}
	return strings.TrimSpace(raw)
}

// forced checks MM_MCP_FORCE_HUMAN_IN_THE_LOOP, which mm-mcp refuses while
// it asks before writes itself (ADR-033).
func forced(raw string, ask bool) Check {
	check := boolean(config.EnvForceHumanInTheLoop, raw, false,
		"each tool that would ask is marked for the MCP client to ask a person on every call, whatever its permission rules allow",
		"the MCP client's own permission rules decide whether it asks before a change others see")
	if value, err := config.ParseBool(config.EnvForceHumanInTheLoop, raw); err == nil && value && ask {
		return Check{
			Name: config.EnvForceHumanInTheLoop, Status: Failed,
			Detail: fmt.Sprintf("is true while %s is not false, so mm-mcp does not start: you would be asked twice for one write", config.EnvAskBeforeWrites),
			Next:   fmt.Sprintf("Set %s=false, so that the MCP client asks instead of mm-mcp, or unset %s.", config.EnvAskBeforeWrites, config.EnvForceHumanInTheLoop),
		}
	}
	return check
}

func address(raw string, urlGiven bool) Check {
	switch {
	case config.Placeholder(raw):
		return Check{
			Name: config.EnvURL, Status: Failed,
			Detail: fmt.Sprintf("is %s, a placeholder the MCP client left unexpanded, which reads as unset", strings.TrimSpace(raw)),
			Next:   "Fill in the server's address in the client's settings for mm-mcp, or pass --url.",
		}
	case strings.TrimSpace(raw) == "":
		return Check{
			Name: config.EnvURL, Status: Failed, Detail: "is not set",
			Next: "Pass --url with the address you open Mattermost at, such as https://chat.example.com, as your MCP client's env block sets MM_URL.",
		}
	}
	parsed, err := config.ParseURL(raw)
	if err != nil {
		return Check{Name: config.EnvURL, Status: Failed, Detail: err.Error(), Next: "Use the address you open Mattermost at in a browser."}
	}
	from := "from the environment"
	if urlGiven {
		from = "from --url"
	}
	return ok(config.EnvURL, "%s, %s", parsed, from)
}

func token(raw string) Check {
	switch {
	case config.Placeholder(raw):
		return ok(config.EnvToken, "is %s, a placeholder the MCP client left unexpanded, which reads as unset: the login `mm-mcp login` stored for %s is used", strings.TrimSpace(raw), config.EnvURL)
	case strings.TrimSpace(raw) == "":
		return ok(config.EnvToken, "is not set: the login `mm-mcp login` stored for %s is used", config.EnvURL)
	default:
		return ok(config.EnvToken, "is set: mm-mcp acts with that token, and not with a login it stored")
	}
}

// boolean checks a true-or-false variable, which reads as unset, and so as
// fallback, when it is empty or a placeholder.
func boolean(name, raw string, fallback bool, on, off string) Check {
	unset := off
	if fallback {
		unset = on
	}
	value, err := config.ParseBool(name, raw)
	switch {
	case config.Placeholder(raw):
		return ok(name, "is %s, a placeholder the MCP client left unexpanded, which reads as unset: %s", strings.TrimSpace(raw), unset)
	case strings.TrimSpace(raw) == "":
		return ok(name, "is not set, so %s", unset)
	case err != nil:
		return Check{Name: name, Status: Failed, Detail: err.Error(), Next: "Use true or false."}
	case value:
		return ok(name, "%s", on)
	default:
		return ok(name, "%s", off)
	}
}

func downloadDir(dir string) Check {
	if !filepath.IsAbs(dir) {
		return Check{Name: config.EnvDownloadDir, Status: Failed, Detail: fmt.Sprintf("is %q, not a full path", dir), Next: "Give the full path of the directory."}
	}
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return Check{Name: config.EnvDownloadDir, Status: Warning, Detail: fmt.Sprintf("%s cannot be used: %v", dir, err), Next: "Create the directory, or name one that exists; save_file writes there."}
	case !info.IsDir():
		return Check{Name: config.EnvDownloadDir, Status: Warning, Detail: dir + " is a file, not a directory", Next: "Name a directory; save_file writes there."}
	}
	return ok(config.EnvDownloadDir, "save_file writes to %s", dir)
}

func caFile(path string) Check {
	pem, err := os.ReadFile(path) //nolint:gosec // the person names the file, in MM_MCP_CA_FILE
	if err != nil {
		return Check{Name: config.EnvCAFile, Status: Failed, Detail: fmt.Sprintf("%s cannot be read: %v", path, err), Next: "Name a PEM file mm-mcp can read."}
	}
	if !x509.NewCertPool().AppendCertsFromPEM(pem) {
		return Check{Name: config.EnvCAFile, Status: Failed, Detail: path + " holds no PEM-encoded certificate", Next: "Name a file of certificates in PEM form, each between BEGIN CERTIFICATE and END CERTIFICATE lines."}
	}
	return ok(config.EnvCAFile, "the certificate authorities in %s are trusted beside the system's", path)
}

// Loaded describes the configuration a running server loaded: what it acts
// with, and what it offers.
func Loaded(cfg config.Config, where string) []Check {
	credential := "the token in " + config.EnvToken
	if cfg.TokenStored {
		credential = fmt.Sprintf("the login `mm-mcp login` stored for %s, in %s", cfg.URL, where)
	}
	checks := []Check{
		ok("environment", "the one the MCP client started this server with"),
		ok(config.EnvURL, "%s", cfg.URL),
		ok("credential source", "%s", credential),
	}
	switch {
	case cfg.AllowWrites && cfg.ForceHumanInTheLoop:
		checks = append(checks,
			ok(config.EnvAllowWrites, "writes are allowed: the tools that post and change Mattermost are offered"),
			ok(config.EnvAskBeforeWrites, "is false: mm-mcp asks nothing before a change others see"),
			ok(config.EnvForceHumanInTheLoop, "is true: each tool that would ask is marked for the MCP client to ask a person on every call, whatever its permission rules allow"))
	case cfg.AllowWrites && cfg.SkipAsking:
		checks = append(checks,
			ok(config.EnvAllowWrites, "writes are allowed: the tools that post and change Mattermost are offered"),
			ok(config.EnvAskBeforeWrites, "is false: mm-mcp asks nothing before a change others see, and the MCP client's own approval of each tool call is the only check"))
	case cfg.AllowWrites:
		checks = append(checks, ok(config.EnvAllowWrites, "writes are allowed: the tools that post and change Mattermost are offered, and each change others see asks first"))
	default:
		checks = append(checks, ok(config.EnvAllowWrites, "writes are not allowed: only the tools that read are offered"))
	}
	if !cfg.MarkAIGenerated {
		checks = append(checks, ok(config.EnvMarkAIGenerated, "posts and edits the model writes are not marked as written with AI"))
	}
	if cfg.CAFile != "" {
		checks = append(checks, ok(config.EnvCAFile, "the certificate authorities in %s are trusted beside the system's", cfg.CAFile))
	}
	if cfg.Local {
		dir := cfg.DownloadDir
		if dir == "" {
			dir = "your Downloads directory"
		}
		checks = append(checks, ok("transport", "stdio, on the person's own machine: save_file and attaching a file from a path are offered, and save_file writes to %s", dir))
	} else {
		checks = append(checks, ok("transport", "Streamable HTTP: the tools that read or write this machine's files are not offered"))
	}
	return checks
}
