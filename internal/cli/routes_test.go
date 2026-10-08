package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/login"
)

// Which ways `mm-mcp login` tries, for each way a server signs people in.
func TestTheWaysToLogInFollowWhatTheServerAllows(t *testing.T) {
	t.Parallel()
	registered := login.Client{ID: "the-app", Callback: "http://127.0.0.1:8766/callback"}
	for name, c := range map[string]struct {
		signIn login.SignIn
		client login.Client
		with   string
		want   []string
	}{
		"SAML, OAuth off":                   {login.SignIn{Password: true, SSO: []string{"SAML"}}, login.Client{}, "", []string{WithWindow, WithPassword, WithPaste}},
		"SAML, OAuth on, no registering":    {login.SignIn{SSO: []string{"SAML"}, OAuth: true}, login.Client{}, "", []string{WithWindow, WithPaste}},
		"SAML, OAuth on, an app registered": {login.SignIn{SSO: []string{"SAML"}, OAuth: true}, registered, "", []string{WithOAuth}},
		"OAuth with clients registering":    {login.SignIn{Password: true, OAuth: true, OAuthRegistering: true}, login.Client{}, "", []string{WithOAuth}},
		"passwords only":                    {login.SignIn{Password: true}, login.Client{}, "", []string{WithPassword, WithWindow, WithPaste}},
		"LDAP":                              {login.SignIn{LDAP: true}, login.Client{}, "", []string{WithPassword, WithWindow, WithPaste}},
		"the way asked for alone":           {login.SignIn{Password: true, OAuth: true, OAuthRegistering: true}, login.Client{}, WithPaste, []string{WithPaste}},
		"an app given while OAuth is off":   {login.SignIn{SSO: []string{"SAML"}}, registered, "", []string{WithWindow, WithPaste}},
		"nothing known of the server":       {login.SignIn{}, login.Client{}, "", []string{WithWindow, WithPaste}},
	} {
		if got := routes(c.signIn, c.client, c.with); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", name, got, c.want)
		}
	}
}

// Where only OAuth was tried, the advice says how to name another way.
func TestTheAdviceAfterOAuthNamesTheOtherWays(t *testing.T) {
	t.Parallel()
	offered := login.SignIn{OAuth: true, OAuthRegistering: true}
	if got := advice(offered, login.Client{}, "", "https://chat.example.com", nil); !strings.Contains(got, "--with window") {
		t.Errorf("after OAuth alone:\n%s", got)
	}
	if got := advice(login.SignIn{SSO: []string{"SAML"}}, login.Client{}, "", "https://chat.example.com", nil); strings.Contains(got, "tried only that") {
		t.Errorf("without OAuth:\n%s", got)
	}
}
