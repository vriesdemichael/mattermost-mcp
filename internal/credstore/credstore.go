// Package credstore keeps the session token `mm-mcp login` obtained in the
// operating system's own credential store: the Windows Credential Manager,
// the macOS keychain, or the Secret Service of a Linux desktop (ADR-019). A
// token is stored per Mattermost server, under the server's address.
package credstore

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
)

// service is the name the tokens are stored under.
const service = "mm-mcp"

// Store keeps token for the server at address.
func Store(address, token string) error {
	if err := keyring.Set(service, key(address), token); err != nil {
		return fmt.Errorf("storing the token in the system's credential store: %w", err)
	}
	return nil
}

// Load is the token stored for the server at address, and whether there is one.
func Load(address string) (string, bool, error) {
	token, err := keyring.Get(service, key(address))
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("reading the system's credential store: %w", err)
	}
	return token, true, nil
}

// Delete forgets the token stored for the server at address; there being
// none is not an error.
func Delete(address string) error {
	if err := keyring.Delete(service, key(address)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("removing the token from the system's credential store: %w", err)
	}
	return nil
}

// Key is the name a token for the server at address is stored under, for a
// person to find it in the store.
func Key(address string) string { return key(address) }

// key is the server's address as a token is stored under it: without a
// trailing slash, its scheme and host in lower case.
func key(address string) string {
	address = strings.TrimRight(strings.TrimSpace(address), "/")
	scheme, rest, found := strings.Cut(address, "://")
	if !found {
		return address
	}
	host, path, _ := strings.Cut(rest, "/")
	if path != "" {
		path = "/" + path
	}
	return strings.ToLower(scheme) + "://" + strings.ToLower(host) + path
}

// Where names the system's credential store, for a person to find a token in.
func Where() string { return where(runtime.GOOS) }

func where(goos string) string {
	switch goos {
	case "windows":
		return "the Windows Credential Manager, as " + service
	case "darwin":
		return "your login keychain, as " + service
	default:
		return "your desktop's Secret Service, such as GNOME Keyring or KWallet, as " + service
	}
}

// clientService is the name the OAuth client a login used is stored under,
// to log in as again: its id and callback, which are not secrets.
const clientService = "mm-mcp-oauth-client"

// StoreClient keeps the OAuth client mm-mcp logged in to the server at
// address as, encoded by the caller.
func StoreClient(address, client string) error {
	if err := keyring.Set(clientService, key(address), client); err != nil {
		return fmt.Errorf("storing the OAuth client in the system's credential store: %w", err)
	}
	return nil
}

// LoadClient is the OAuth client stored for the server at address, and
// whether there is one.
func LoadClient(address string) (string, bool, error) {
	client, err := keyring.Get(clientService, key(address))
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("reading the system's credential store: %w", err)
	}
	return client, true, nil
}

// originService is the name how a stored token was obtained is stored under:
// the way `mm-mcp login` logged in, so `mm-mcp logout` ends at Mattermost only
// a session mm-mcp made, never a token the person pasted, which may be their
// browser's own session.
const originService = "mm-mcp-origin"

// StoreOrigin keeps how the token for the server at address was obtained, or
// forgets it when origin is empty.
func StoreOrigin(address, origin string) error {
	if origin == "" {
		if err := keyring.Delete(originService, key(address)); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("removing from the system's credential store: %w", err)
		}
		return nil
	}
	if err := keyring.Set(originService, key(address), origin); err != nil {
		return fmt.Errorf("storing in the system's credential store: %w", err)
	}
	return nil
}

// LoadOrigin is how the token for the server at address was obtained, or empty
// when that is not known, as for a token stored before it was kept.
func LoadOrigin(address string) (string, error) {
	origin, err := keyring.Get(originService, key(address))
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("reading the system's credential store: %w", err)
	}
	return origin, nil
}
