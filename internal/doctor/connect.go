package doctor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// Transport is the HTTP transport mm-mcp reaches the server with: the one
// NewSafeTransport builds, trusting the certificate authorities in
// MM_MCP_CA_FILE beside the system's when it is set.
func Transport(cfg config.Config) (http.RoundTripper, error) {
	transport := network.NewSafeTransport()
	if cfg.CAFile == "" {
		return transport, nil
	}
	pem, err := os.ReadFile(cfg.CAFile)
	if err == nil {
		err = network.TrustCertificates(transport, pem)
	}
	if err != nil {
		return nil, fmt.Errorf("%s %s cannot be used: %w", config.EnvCAFile, cfg.CAFile, err)
	}
	return transport, nil
}

// Proxy is the check that names the proxy requests to address go through,
// from HTTPS_PROXY, HTTP_PROXY and NO_PROXY, without the user and password a
// proxy's address can carry. It is nil when there is none.
func Proxy(address string) *Check {
	request, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		return nil
	}
	proxy, err := http.ProxyFromEnvironment(request)
	return proxyCheck(address, proxy, err)
}

// proxyCheck is the check Proxy makes of the proxy the environment names.
// A proxy's own error can quote its address, so it is not shown.
func proxyCheck(address string, proxy *url.URL, err error) *Check {
	switch {
	case err != nil:
		return &Check{Name: "proxy", Status: Failed, Detail: "the proxy the environment names is not an address mm-mcp can use", Next: "Correct HTTPS_PROXY or HTTP_PROXY, or unset it."}
	case proxy == nil:
		return nil
	}
	shown := *proxy
	shown.User = nil
	check := ok("proxy", "requests to %s go through %s, as HTTPS_PROXY or HTTP_PROXY says", address, shown.String())
	return &check
}

// Connection is what asking Mattermost who the credential belongs to found.
type Connection struct {
	Checks []Check
	// User is who the credential belongs to, when Mattermost said.
	User *model.User
}

// Stops reports whether mm-mcp serve stops at start on what was found: the
// address is not Mattermost's, or Mattermost refused the credential.
func (c Connection) Stops() bool {
	for _, check := range c.Checks {
		if check.stops {
			return true
		}
	}
	return false
}

// Connect asks Mattermost who the credential belongs to, and checks the
// server, the credential and the release from the answer. A server that
// cannot be reached fails the check without stopping mm-mcp serve: a laptop
// starts its MCP client before its VPN, and the tools work once Mattermost can
// be reached.
func Connect(ctx context.Context, client *mattermost.Client, cfg config.Config) Connection {
	const server, credential = "server", "credential"
	user, serverVersion, err := client.Check(ctx)
	var answered *mattermost.Error
	var redirect *network.RedirectError
	switch {
	case err == nil:
	case errors.As(err, &answered) && answered.Status == http.StatusUnauthorized:
		check := Check{Name: credential, Status: Failed, stops: true}
		if cfg.TokenStored {
			check.Detail = fmt.Sprintf("%s refused the session `mm-mcp login` stored: it expired or was logged out.", cfg.URL)
			check.Next = fmt.Sprintf("Run `mm-mcp login --url %s` again, and restart.", cfg.URL)
		} else {
			check.Detail = fmt.Sprintf("%s refused the token in %s: it is wrong, was revoked, or is a session that expired.", cfg.URL, config.EnvToken)
			check.Next = "Get a new one, with `mm-mcp login` or as a personal access token, and restart."
		}
		return Connection{Checks: []Check{ok(server, "%s answers as Mattermost", cfg.URL), check}}
	case errors.Is(err, mattermost.ErrNotMattermost),
		errors.As(err, &answered) && (answered.Status == http.StatusNotFound || answered.ID == "" && answered.Status < http.StatusInternalServerError):
		return Connection{Checks: []Check{{
			Name: server, Status: Failed, stops: true,
			Detail: fmt.Sprintf("%s does not answer as a Mattermost server (%v).", cfg.URL, err),
			Next:   fmt.Sprintf("Set %s to the address you open Mattermost at in a browser.", config.EnvURL),
		}, skipped(credential, "the server did not answer as Mattermost")}}
	case errors.As(err, &redirect):
		return Connection{Checks: []Check{
			{Name: server, Status: Failed, stops: true, Detail: err.Error()},
			skipped(credential, "the server redirected elsewhere"),
		}}
	case errors.As(err, &answered):
		return Connection{Checks: []Check{
			ok(server, "%s answers as Mattermost", cfg.URL),
			{Name: credential, Status: Warning, Detail: fmt.Sprintf("checking the token at %s failed: %v", cfg.URL, err), Next: "Try again in a moment; Mattermost answered with an error of its own."},
		}}
	default:
		return Connection{Checks: []Check{
			{Name: server, Status: Failed, Detail: err.Error(), Next: Unreachable(err, cfg.URL)},
			skipped(credential, "the server could not be reached"),
		}}
	}
	checks := []Check{
		ok(server, "%s answers as Mattermost %s", cfg.URL, serverVersion),
		ok(credential, "acts as %s", who(user)),
	}
	if mattermost.OlderThanSupported(serverVersion) {
		checks = append(checks, Check{
			Name: "release", Status: Warning,
			Detail: fmt.Sprintf("%s runs Mattermost %s, older than %s, the oldest release mm-mcp supports; some tools may fail", cfg.URL, serverVersion, mattermost.OldestSupported),
		})
	}
	return Connection{Checks: checks, User: user}
}

func who(user *model.User) string {
	switch {
	case user.IsBot:
		return "@" + user.Username + ", a bot"
	case user.DeleteAt > 0:
		return "@" + user.Username + ", who is deactivated"
	default:
		return "@" + user.Username
	}
}

// Unreachable says what to do about a server that could not be reached, from
// what stopped the request.
func Unreachable(err error, address string) string {
	host := address
	if parsed, parseErr := url.Parse(address); parseErr == nil {
		host = parsed.Hostname()
	}
	const anyway = "mm-mcp serve starts anyway, and its tools work once Mattermost can be reached."
	var dns *net.DNSError
	var unknownAuthority x509.UnknownAuthorityError
	var verification *tls.CertificateVerificationError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var blocked *network.ExternalNetworkBlockedError
	var operation *net.OpError
	switch {
	case errors.As(err, &blocked):
		return fmt.Sprintf("%s is for mm-mcp's own tests; unset it.", config.EnvBlockExternalNetwork)
	case errors.As(err, &hostname):
		return fmt.Sprintf("The server's certificate is for another name than %s. Set %s to the address you open Mattermost at in a browser.", host, config.EnvURL)
	case errors.As(err, &invalid):
		return "The server's certificate is not valid now: it expired, or is not valid yet. Tell the server's administrator."
	case errors.As(err, &unknownAuthority), errors.As(err, &verification):
		return fmt.Sprintf("The server's certificate is signed by an authority this system does not trust, as when an organisation signs its own "+
			"certificates or inspects the traffic to them. Put that authority's certificate in a PEM file and set %s to it.", config.EnvCAFile)
	case errors.As(err, &operation) && operation.Op == "proxyconnect":
		return "The proxy HTTPS_PROXY or HTTP_PROXY names could not be reached. Check it, or unset it. " + anyway
	case errors.As(err, &dns):
		return sentence(fmt.Sprintf("The name %s does not resolve here. Check the address; if your Mattermost is on a company network, connect to its VPN.", host), anyway)
	case errors.Is(err, context.DeadlineExceeded) || isTimeout(err):
		return sentence(fmt.Sprintf("%s did not answer in time. If your Mattermost is on a company network, connect to its VPN.", host), anyway)
	case errors.As(err, &operation) && operation.Op == "dial":
		return sentence(fmt.Sprintf("Nothing at %s accepted the connection. Check the address and its port; if your Mattermost is on a company network, connect to its VPN.", host), anyway)
	default:
		return sentence("Check the address and your network.", anyway)
	}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// Teams checks that the user belongs to a team: one who belongs to none
// finds nothing with the tools that read channels and search, which looks
// like a fault and is not one.
func Teams(ctx context.Context, client *mattermost.Client, user *model.User) Check {
	const name = "teams"
	if user == nil {
		return skipped(name, "Mattermost did not say who the credential belongs to")
	}
	teams, err := client.Teams(ctx)
	if err != nil {
		return Check{Name: name, Status: Warning, Detail: fmt.Sprintf("the teams of @%s could not be read: %v", user.Username, err)}
	}
	if len(teams) == 0 {
		return Check{
			Name: name, Status: Warning,
			Detail: fmt.Sprintf("@%s belongs to no team, so the tools find no channel outside direct messages, and search finds nothing", user.Username),
			Next:   "Join a team in Mattermost, or ask its administrator to add you to one.",
		}
	}
	names := make([]string, 0, len(teams))
	for _, team := range teams {
		names = append(names, team.DisplayName)
	}
	const shown = 5
	listed := strings.Join(names[:min(len(names), shown)], ", ")
	if len(names) > shown {
		listed += fmt.Sprintf(" and %d more", len(names)-shown)
	}
	return ok(name, "@%s belongs to %s: %s", user.Username, plural(len(teams), "team"), listed)
}
