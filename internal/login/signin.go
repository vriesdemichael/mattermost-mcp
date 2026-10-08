package login

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vriesdemichael/mm-mcp/internal/version"
)

// SignIn is how a server lets people sign in, as it tells anyone who asks
// before they log in: its own sign-in page's settings, and whether it is an
// OAuth authorization server.
type SignIn struct {
	// Password is sign-in with an email address or a username and a password
	// Mattermost keeps. An account made through single sign-on has none.
	Password bool
	// LDAP is sign-in with a directory's username and password, which
	// Mattermost's login API takes as it takes its own.
	LDAP bool
	// SSO names the single sign-on services the login page offers.
	SSO []string
	// OAuth is the server's OAuth service: whether it is on, and whether a
	// client may register itself.
	OAuth            bool
	OAuthRegistering bool
}

// ssoServices are the client settings that offer single sign-on, by the name
// a person knows each by.
var ssoServices = []struct{ setting, name string }{
	{"EnableSaml", "SAML"},
	{"EnableSignUpWithGitLab", "GitLab"},
	{"EnableSignUpWithOpenId", "OpenID Connect"},
	{"EnableSignUpWithOffice365", "Entra ID"},
	{"EnableSignUpWithGoogle", "Google"},
}

// Discover asks the server at address how people sign in, through client,
// which needs no credential: the login page's settings, and the OAuth
// authorization server's metadata (RFC 8414).
func Discover(ctx context.Context, client *http.Client, address string) (SignIn, error) {
	var settings map[string]string
	if err := getJSON(ctx, client, address+"/api/v4/config/client?format=old", &settings); err != nil {
		return SignIn{}, fmt.Errorf("reading how %s lets people sign in: %w", address, err)
	}
	on := func(setting string) bool { return strings.EqualFold(settings[setting], "true") }
	found := SignIn{
		Password: on("EnableSignInWithEmail") || on("EnableSignInWithUsername"),
		LDAP:     on("EnableLdap"),
	}
	for _, service := range ssoServices {
		if on(service.setting) {
			found.SSO = append(found.SSO, service.name)
		}
	}
	var metadata struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		RegistrationEndpoint  string `json:"registration_endpoint"`
	}
	// An OAuth service that is off answers 501 with an error page, and a
	// server that predates the metadata answers with its web app.
	if err := getJSON(ctx, client, address+"/.well-known/oauth-authorization-server", &metadata); err == nil && metadata.AuthorizationEndpoint != "" {
		found.OAuth = true
		found.OAuthRegistering = metadata.RegistrationEndpoint != ""
	}
	return found, nil
}

func getJSON(ctx context.Context, client *http.Client, address string, out any) error {
	request, err := newRequest(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", address, response.Status)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// newRequest is a request to the server at address that names mm-mcp, as every
// request the Mattermost client sends does, so a login's requests can be told
// apart in the server's logs as well.
func newRequest(ctx context.Context, method, address string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", version.UserAgent())
	return request, nil
}
