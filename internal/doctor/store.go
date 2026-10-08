package doctor

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/credstore"
)

// Store is the credential store `mm-mcp login` keeps its logins in.
type Store struct {
	Load config.Stored
	// LoadClient finds the OAuth client a login used; nil leaves it unchecked.
	LoadClient func(address string) (string, bool, error)
	// Where names the store, for a person to find it.
	Where string
}

// Login checks the login stored for the server at address. tokenSet says
// MM_TOKEN is set, which a stored login does not override. The store is only
// read: nothing is written to it, not even to try it.
func Login(address string, tokenSet bool, store Store) []Check {
	const name = "stored login"
	if store.Load == nil {
		return []Check{skipped(name, "this server was started without a credential store")}
	}
	under := fmt.Sprintf("in %s, under %s", store.Where, credstore.Key(address))
	// The token itself is never looked at past whether there is one.
	_, found, err := store.Load(address)
	var check Check
	switch {
	case err != nil && tokenSet:
		check = Check{
			Name: name, Status: Warning,
			Detail: fmt.Sprintf("the store could not be read (%v). %s is set, so mm-mcp does not need it, but `mm-mcp login` could not keep a login here", err, config.EnvToken),
			Next:   storeAdvice(runtime.GOOS),
		}
	case err != nil:
		check = Check{
			Name: name, Status: Failed,
			Detail: fmt.Sprintf("the store could not be read, %s: %v", under, err),
			Next:   storeAdvice(runtime.GOOS),
		}
	case found && tokenSet:
		check = Check{
			Name: name, Status: Warning,
			Detail: fmt.Sprintf("a login is stored %s, but %s is set, and wins: the stored login is not used", under, config.EnvToken),
			Next:   fmt.Sprintf("To use the login, remove %s from the MCP client's env block for mm-mcp, and restart it. To keep the token, `mm-mcp logout --url %s` forgets the login.", config.EnvToken, address),
		}
	case found:
		check = ok(name, "a login is stored %s", under)
	case tokenSet:
		check = ok(name, "none is stored %s, and none is needed: %s is set", under, config.EnvToken)
	default:
		check = Check{
			Name: name, Status: Failed,
			Detail: fmt.Sprintf("none is stored %s, and %s is not set", under, config.EnvToken),
			Next: fmt.Sprintf("Run `mm-mcp login --url %s`. A login made with another spelling of the address, such as http for https or "+
				"another host name for the same server, is stored under that one: log in with the address MM_URL names.", address),
		}
	}
	checks := []Check{check}
	if store.LoadClient != nil && err == nil {
		if oauth := oauthClient(address, store); oauth != nil {
			checks = append(checks, *oauth)
		}
	}
	return checks
}

// oauthClient is the OAuth client a login to address registered or was
// given, when there is one. Its id and callback are not secrets.
func oauthClient(address string, store Store) *Check {
	raw, found, err := store.LoadClient(address)
	if err != nil || !found {
		return nil
	}
	var client struct {
		ID string `json:"client_id"`
	}
	if json.Unmarshal([]byte(raw), &client) != nil || client.ID == "" {
		return nil
	}
	check := ok("OAuth client", "`mm-mcp login` logs in to %s as the OAuth client %s", address, client.ID)
	return &check
}

// storeAdvice is what to do when the system's credential store cannot be read.
func storeAdvice(goos string) string {
	fallback := fmt.Sprintf(" Or put a personal access token or a session token in %s in the MCP client's env block instead, which needs no store.", config.EnvToken)
	switch goos {
	case "windows":
		return "Check that mm-mcp runs as the Windows user who logged in, and that Credential Manager opens." + fallback
	case "darwin":
		return "Unlock your login keychain in Keychain Access, and allow mm-mcp when macOS asks." + fallback
	default:
		return "mm-mcp reaches the Secret Service over the desktop session's D-Bus. Over SSH, in WSL, in a container, or from a " +
			"client started outside the desktop session there is none: start one, such as gnome-keyring-daemon, or log in at the desktop." + fallback
	}
}
