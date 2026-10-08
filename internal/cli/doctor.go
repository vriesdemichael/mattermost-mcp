package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/doctor"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// runDoctor checks the setup from this terminal: the configuration, the
// stored login, the way to the server, the credential at Mattermost, and how
// `mm-mcp login` would log in. It reads and changes nothing else: not
// Mattermost, not the credential store, and it starts no browser.
func runDoctor(ctx context.Context, args []string, deps Deps) int {
	flags := flag.NewFlagSet("mm-mcp doctor", flag.ContinueOnError)
	flags.SetOutput(deps.Stderr)
	given := flags.String("url", "", "the address you open Mattermost at; MM_URL when not given")
	asJSON := flags.Bool("json", false, "print the checks as JSON, for an agent to read")
	if err := flags.Parse(args); err != nil {
		return ExitConfig
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(deps.Stderr, "mm-mcp doctor takes no arguments, got %q\n", flags.Args())
		return ExitConfig
	}
	lookup := deps.Getenv
	urlGiven := strings.TrimSpace(*given) != ""
	if urlGiven {
		lookup = func(name string) string {
			if name == config.EnvURL {
				return *given
			}
			return deps.Getenv(name)
		}
	}

	checking, cancel := context.WithTimeout(ctx, PreflightTimeout)
	defer cancel()
	report := diagnose(checking, lookup, urlGiven, deps)

	if *asJSON {
		encoder := json.NewEncoder(deps.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report.Result()); err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
			return ExitFailure
		}
	} else {
		report.Write(deps.Stdout)
	}
	if report.Failed() {
		return ExitFailure
	}
	return ExitOK
}

func diagnose(ctx context.Context, lookup func(string) string, urlGiven bool, deps Deps) doctor.Report {
	var report doctor.Report
	report.Add(doctor.Build(), doctor.Terminal())
	report.Add(doctor.Settings(lookup, urlGiven)...)

	var store doctor.Store
	if deps.Credentials != nil {
		store = doctor.Store{Load: deps.Credentials.Load, LoadClient: deps.Credentials.LoadClient, Where: deps.Credentials.Where}
	}
	address, err := config.ParseURL(lookup(config.EnvURL))
	if err != nil || config.Placeholder(lookup(config.EnvURL)) {
		report.Add(doctor.Check{Name: "server", Status: doctor.Skipped, Detail: "there is no address to check"})
		return report
	}
	token := lookup(config.EnvToken)
	report.Add(doctor.Login(address, strings.TrimSpace(token) != "" && !config.Placeholder(token), store)...)
	if unencrypted(address) {
		report.Add(doctor.Check{
			Name: "encryption", Status: doctor.Warning,
			Detail: fmt.Sprintf("%s is plain http, so the token crosses the network unencrypted", address),
			Next:   "Use the https address, if the server has one.",
		})
	}
	if proxy := doctor.Proxy(address); proxy != nil {
		report.Add(*proxy)
	}

	transport := network.NewSafeTransport()
	cfg, err := config.Load(lookup, store.Load)
	if err == nil {
		transport, err = doctor.Transport(cfg)
	}
	if err != nil {
		if !report.Failed() {
			// Every reason config.Load refuses is checked above; this is one
			// that is not, said as mm-mcp serve says it.
			report.Add(doctor.Check{Name: "configuration", Status: doctor.Failed, Detail: err.Error()})
		}
		report.Add(doctor.Check{Name: "server", Status: doctor.Skipped, Detail: "mm-mcp serve would not start with the configuration above, so the credential was not tried"})
	} else {
		client := mattermost.New(cfg.URL, cfg.Token, transport)
		connection := doctor.Connect(ctx, client, cfg)
		report.Add(connection.Checks...)
		report.Add(doctor.Teams(ctx, client, connection.User))
		if !reached(connection) {
			report.Add(doctor.Check{Name: "login", Status: doctor.Skipped, Detail: "the server could not be asked how people sign in"})
			return report
		}
	}
	report.Add(loginChecks(ctx, address, transport, deps)...)
	return report
}

// reached reports whether the server answered as Mattermost.
func reached(connection doctor.Connection) bool {
	for _, check := range connection.Checks {
		if check.Name == "server" {
			return check.Status == doctor.OK
		}
	}
	return false
}

// loginChecks say how `mm-mcp login` would log in to the server at address,
// from what the server tells anyone, without logging in.
func loginChecks(ctx context.Context, address string, transport http.RoundTripper, deps Deps) []doctor.Check {
	const name = "login"
	httpClient := &http.Client{Transport: transport, Timeout: mattermost.RequestTimeout, CheckRedirect: network.SameOriginRedirects}
	signIn, err := login.Discover(ctx, httpClient, address)
	if err != nil {
		return []doctor.Check{{Name: name, Status: doctor.Warning, Detail: fmt.Sprintf("how the server lets people sign in could not be read: %v", err), Next: doctor.Unreachable(err, address)}}
	}
	var client login.Client
	if deps.Credentials != nil {
		client = storedClient(deps, address, loginFlags{})
	}
	plan := routes(signIn, client, "")
	checks := []doctor.Check{{
		Name: name, Status: doctor.OK,
		Detail: fmt.Sprintf("%s; `mm-mcp login` would try %s", signsIn(signIn), describeRoutes(plan)),
	}}
	for _, route := range plan {
		switch route {
		case WithOAuth:
			checks = append(checks, callbackPort(client))
		case WithWindow:
			checks = append(checks, browsers(deps))
		}
	}
	return checks
}

// signsIn says how a server lets people sign in.
func signsIn(signIn login.SignIn) string {
	var ways []string
	if signIn.Password {
		ways = append(ways, "a password")
	}
	if signIn.LDAP {
		ways = append(ways, "LDAP")
	}
	ways = append(ways, signIn.SSO...)
	said := "the server offers no way to sign in on its login page"
	if len(ways) > 0 {
		said = "people sign in with " + strings.Join(ways, ", ")
	}
	switch {
	case signIn.OAuth && signIn.OAuthRegistering:
		said += "; its OAuth service lets mm-mcp register itself"
	case signIn.OAuth:
		said += "; its OAuth service is on, but lets no client register itself"
	default:
		said += "; its OAuth service is off"
	}
	return said
}

func describeRoutes(plan []string) string {
	described := map[string]string{
		WithOAuth:    "OAuth in your own browser",
		WithWindow:   "a browser window of its own",
		WithPassword: "your password in the terminal",
		WithPaste:    "a token you paste",
	}
	var said []string
	for _, route := range plan {
		said = append(said, described[route])
	}
	return strings.Join(said, ", then ")
}

// callbackPort checks that the port an OAuth login listens on for its
// callback is free on this machine.
func callbackPort(client login.Client) doctor.Check {
	port := login.DefaultCallbackPort
	if callback, err := url.Parse(client.Callback); err == nil {
		if n, err := strconv.Atoi(callback.Port()); err == nil {
			port = n
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return doctor.Check{
			Name: "OAuth callback", Status: doctor.Warning,
			Detail: fmt.Sprintf("port %d on 127.0.0.1, where an OAuth login waits for Mattermost to send the browser back, is taken: %v", port, err),
			Next:   "Close what listens there, or log in with --callback-port and an OAuth app registered with that port.",
		}
	}
	_ = listener.Close()
	return doctor.Check{Name: "OAuth callback", Status: doctor.OK, Detail: fmt.Sprintf("port %d on 127.0.0.1 is free for the login's callback", port)}
}

// browsers names the browsers a login window could use.
func browsers(deps Deps) doctor.Check {
	const name = "login browsers"
	if deps.Login == nil || deps.Login.Browsers == nil {
		return doctor.Check{Name: name, Status: doctor.Skipped, Detail: "this build cannot open a login window"}
	}
	found, err := deps.Login.Browsers()
	switch {
	case errors.Is(err, login.ErrNoBrowser) || err == nil && len(found) == 0:
		return doctor.Check{
			Name: name, Status: doctor.Warning,
			Detail: "no Chrome, Edge, Chromium or Firefox was found for a login window",
			Next:   "Install Playwright's Chromium with `npx playwright install chromium`, or name a browser with `mm-mcp login --browser <path>`.",
		}
	case err != nil:
		return doctor.Check{Name: name, Status: doctor.Warning, Detail: err.Error()}
	}
	names := make([]string, 0, len(found))
	for _, browser := range found {
		names = append(names, browser.String())
	}
	return doctor.Check{
		Name: name, Status: doctor.OK,
		Detail: "a login window tries, in turn: " + strings.Join(names, "; ") + ". A company policy can still forbid one to be watched; `mm-mcp login` then tries the next.",
	}
}
