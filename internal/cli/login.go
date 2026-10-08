package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// LoginTimeout bounds how long `mm-mcp login` waits for the person to log in.
const LoginTimeout = 10 * time.Minute

// The ways `mm-mcp login` logs in (ADR-019).
const (
	// WithOAuth opens Mattermost's authorization page in the person's own
	// browser, where the server is an OAuth authorization server.
	WithOAuth = "oauth"
	// WithWindow opens the login page in a browser window of mm-mcp's own and
	// watches its cookies, for single sign-on without OAuth.
	WithWindow = "window"
	// WithPassword asks for the login and password in the terminal.
	WithPassword = "password"
	// WithPaste reads a token the person copied: a personal access token, a
	// bot's, or a session cookie.
	WithPaste = "paste"
)

// Login is how `mm-mcp login` reaches the person, so a test can stand in for
// the browser and the terminal.
type Login struct {
	// Open opens a page in the person's own browser.
	Open func(ctx context.Context, address string) error
	// Window opens address in a browser window of mm-mcp's own, the one
	// named or else the first that lets itself be watched, and answers with
	// the session token for server once the person has logged in there.
	Window func(ctx context.Context, browser, address, server string, progress io.Writer) (string, error)
	// Line reads a line the person types; Secret reads one without showing it.
	Line   func(prompt string) (string, error)
	Secret func(prompt string) (string, error)
}

// ProcessLogin reaches the person through their own browser and terminal.
func ProcessLogin(stdin *os.File, stderr io.Writer) *Login {
	reader := bufio.NewReader(stdin)
	line := func(prompt string) (string, error) {
		fmt.Fprint(stderr, prompt)
		text, err := reader.ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || text == "") {
			return "", err
		}
		return strings.TrimSpace(text), nil
	}
	return &Login{
		Open:   func(_ context.Context, address string) error { return login.OpenInBrowser(address) },
		Window: windowLogin,
		Line:   line,
		Secret: func(prompt string) (string, error) {
			// A terminal hides what is typed; a pipe, as an agent passes a
			// token through, is read as it is.
			if !term.IsTerminal(int(stdin.Fd())) { //nolint:gosec // a file descriptor fits an int
				return line("")
			}
			fmt.Fprint(stderr, prompt)
			secret, err := term.ReadPassword(int(stdin.Fd())) //nolint:gosec // a file descriptor fits an int
			fmt.Fprintln(stderr)
			return strings.TrimSpace(string(secret)), err
		},
	}
}

// addressFlag reads the server's address from --url, or from MM_URL.
func addressFlag(flags *flag.FlagSet, args []string, deps Deps) (string, bool) {
	raw := flags.String("url", "", "the address you open Mattermost at; MM_URL when not given")
	if err := flags.Parse(args); err != nil {
		return "", false
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(deps.Stderr, "%s takes no arguments, got %q\n", flags.Name(), flags.Args())
		return "", false
	}
	given := *raw
	if strings.TrimSpace(given) == "" {
		given = deps.Getenv(config.EnvURL)
	}
	if strings.TrimSpace(given) == "" {
		fmt.Fprintf(deps.Stderr, "mm-mcp: give the address you open Mattermost at, with --url https://chat.example.com\n")
		return "", false
	}
	address, err := config.ParseURL(given)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return "", false
	}
	return address, true
}

// loginFlags are what `mm-mcp login` takes beside the address.
type loginFlags struct {
	with     string
	browser  string
	clientID string
	port     int
}

// logIn logs the person in the way their server allows, checks the session
// with Mattermost, and stores it (ADR-019).
func logIn(ctx context.Context, args []string, deps Deps) int {
	flags := flag.NewFlagSet("mm-mcp login", flag.ContinueOnError)
	flags.SetOutput(deps.Stderr)
	var given loginFlags
	flags.StringVar(&given.with, "with", "", "how to log in: oauth, window, password or paste; chosen from what the server allows when not given")
	flags.StringVar(&given.browser, "browser", "", "the Chrome, Edge, Chromium or Firefox for --with window, by path or command; the first one that works when not given")
	flags.StringVar(&given.clientID, "client-id", "", "the id of the OAuth app an administrator registered for mm-mcp, a public client with the callback http://127.0.0.1:<port>/callback")
	flags.IntVar(&given.port, "callback-port", login.DefaultCallbackPort, "the port of that app's callback")
	address, ok := addressFlag(flags, args, deps)
	if !ok {
		return ExitConfig
	}
	switch given.with {
	case "", WithOAuth, WithWindow, WithPassword, WithPaste:
	default:
		fmt.Fprintf(deps.Stderr, "mm-mcp: --with is oauth, window, password or paste, not %q\n", given.with)
		return ExitConfig
	}
	if deps.Credentials == nil || deps.Login == nil {
		fmt.Fprintln(deps.Stderr, "mm-mcp: this build cannot log in")
		return ExitFailure
	}
	waiting, cancel := context.WithTimeout(ctx, LoginTimeout)
	defer cancel()
	httpClient := &http.Client{Transport: network.NewSafeTransport(), Timeout: mattermost.RequestTimeout, CheckRedirect: network.SameOriginRedirects}

	signIn, err := login.Discover(waiting, httpClient, address)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: warning: %v; trying a browser window\n", err)
		signIn = login.SignIn{SSO: []string{"single sign-on"}}
	}
	client := storedClient(deps, address, given)
	plan := routes(signIn, client, given.with)
	for _, route := range plan {
		token, oauthClient, err := runRoute(waiting, deps, route, address, signIn, client, given, httpClient)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %s\n", err)
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				break
			}
			continue
		}
		user, _, err := mattermost.New(address, token, network.NewSafeTransport()).Check(ctx)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: the token does not work: %v\n", err)
			continue
		}
		if err := deps.Credentials.Store(address, token); err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %v. Set the token as MM_TOKEN in the MCP client's env block instead.\n", err)
			return ExitFailure
		}
		if oauthClient.ID != "" && deps.Credentials.StoreClient != nil {
			if encoded, err := json.Marshal(oauthClient); err == nil {
				_ = deps.Credentials.StoreClient(address, string(encoded))
			}
		}
		fmt.Fprintf(deps.Stdout, "Logged in to %s as @%s. The session is kept in %s.\n"+
			"mm-mcp serve uses it whenever MM_URL is %s and MM_TOKEN is not set. When Mattermost ends the session, run mm-mcp login again.\n",
			address, user.Username, deps.Credentials.Where, address)
		return ExitOK
	}
	fmt.Fprint(deps.Stderr, advice(signIn, address))
	return ExitFailure
}

// routes are the ways to try, in order: the one asked for alone, or else
// OAuth where the server allows it, a browser window for single sign-on, the
// terminal for a server that keeps passwords, and a pasted token last.
func routes(signIn login.SignIn, client login.Client, with string) []string {
	if with != "" {
		return []string{with}
	}
	var plan []string
	if signIn.OAuth && (signIn.OAuthRegistering || client.ID != "") {
		plan = append(plan, WithOAuth)
	}
	switch {
	case len(signIn.SSO) > 0:
		plan = append(plan, WithWindow)
		if signIn.Password || signIn.LDAP {
			plan = append(plan, WithPassword)
		}
	case signIn.Password || signIn.LDAP:
		plan = append(plan, WithPassword, WithWindow)
	default:
		plan = append(plan, WithWindow)
	}
	return append(plan, WithPaste)
}

// storedClient is the OAuth client to log in as: the one given, or the one a
// login stored for the server.
func storedClient(deps Deps, address string, given loginFlags) login.Client {
	if given.clientID != "" {
		return login.Client{ID: given.clientID, Callback: "http://127.0.0.1:" + strconv.Itoa(given.port) + "/callback"}
	}
	if deps.Credentials.LoadClient == nil {
		return login.Client{}
	}
	raw, found, err := deps.Credentials.LoadClient(address)
	var client login.Client
	if err == nil && found && json.Unmarshal([]byte(raw), &client) == nil {
		return client
	}
	return login.Client{}
}

// runRoute logs in one way, and answers with the token, and the OAuth client
// it logged in as, if it did.
func runRoute(ctx context.Context, deps Deps, route, address string, signIn login.SignIn, client login.Client, given loginFlags, httpClient *http.Client) (string, login.Client, error) {
	reach := deps.Login
	if (route == WithOAuth && reach.Open == nil) || (route == WithWindow && reach.Window == nil) ||
		(route == WithPassword && (reach.Line == nil || reach.Secret == nil)) || (route == WithPaste && reach.Secret == nil) {
		return "", login.Client{}, fmt.Errorf("%s: not available here", route)
	}
	switch route {
	case WithOAuth:
		if !signIn.OAuth {
			return "", login.Client{}, errors.New("the server's OAuth service is off, so it cannot authorize mm-mcp")
		}
		fmt.Fprintf(deps.Stderr, "Opening Mattermost in your browser to authorize mm-mcp. Approve it there; this waits until you have.\n")
		token, err := login.OAuth{
			HTTP: httpClient, Address: address, Client: client, Register: signIn.OAuthRegistering,
			Open: func(page string) error { return deps.Login.Open(ctx, page) },
		}.Login(ctx)
		if err != nil {
			return "", login.Client{}, fmt.Errorf("OAuth: %w", err)
		}
		return token.Access, token.Client, nil
	case WithWindow:
		fmt.Fprintf(deps.Stderr, "Opening Mattermost's login page in a browser window of its own. Log in there as you always do; the window closes once you have.\n")
		token, err := deps.Login.Window(ctx, given.browser, address+"/login", address, deps.Stderr)
		if err != nil {
			return "", login.Client{}, fmt.Errorf("browser window: %w", err)
		}
		return token, login.Client{}, nil
	case WithPassword:
		token, err := passwordRoute(ctx, deps, address)
		if err != nil {
			return "", login.Client{}, fmt.Errorf("password: %w", err)
		}
		return token, login.Client{}, nil
	default:
		fmt.Fprintf(deps.Stderr, "Paste a personal access token, a bot's token, or the value of the MMAUTHTOKEN cookie your browser holds for %s.\n", address)
		token, err := deps.Login.Secret("Token: ")
		// A copy from a browser's cookie list brings spaces and a line end.
		token = strings.TrimSpace(token)
		switch {
		case err != nil:
			return "", login.Client{}, fmt.Errorf("reading the token: %w", err)
		case token == "":
			return "", login.Client{}, errors.New("no token was given")
		}
		return token, login.Client{}, nil
	}
}

// passwordRoute asks for the login, the password and, when the account has
// one, its second factor's code.
func passwordRoute(ctx context.Context, deps Deps, address string) (string, error) {
	id, err := deps.Login.Line("Email address or username: ")
	if err != nil || id == "" {
		return "", errors.Join(errors.New("no login was given"), err)
	}
	password, err := deps.Login.Secret("Password: ")
	if err != nil {
		return "", err
	}
	token, err := mattermost.PasswordLogin(ctx, address, network.NewSafeTransport(), id, password, "")
	if errors.Is(err, mattermost.ErrMFARequired) {
		code, codeErr := deps.Login.Line("Code from your authenticator app: ")
		if codeErr != nil {
			return "", codeErr
		}
		token, err = mattermost.PasswordLogin(ctx, address, network.NewSafeTransport(), id, password, code)
	}
	return token, err
}

// advice is what to do when no way worked, for the server as it signs people in.
func advice(signIn login.SignIn, address string) string {
	var b strings.Builder
	b.WriteString("mm-mcp could not log in. What works from here:\n")
	b.WriteString("  - A browser window: install Playwright's Chromium, which no company policy for Chrome or Edge reaches, with\n" +
		"      npx playwright install chromium\n    and run mm-mcp login again.\n")
	b.WriteString("  - A token: create a personal access token in Mattermost under Profile > Security > Personal Access Tokens,\n" +
		"    or copy the MMAUTHTOKEN cookie from your browser's developer tools while you are logged in, and run\n" +
		"      mm-mcp login --with paste --url " + address + "\n")
	if !signIn.OAuth || !signIn.OAuthRegistering {
		b.WriteString("  - For everyone at once, ask your Mattermost administrator for one of these, after which mm-mcp logs in through your own browser:\n" +
			"      turn on System Console > Integrations > Integration Management > Enable OAuth 2.0 Service Provider, and Enable Dynamic Client Registration;\n" +
			"      or register an OAuth app for mm-mcp: public client, callback http://127.0.0.1:8766/callback, and give you its client id for --client-id.\n")
	}
	return b.String()
}

// windowLogin tries each browser in turn until one lets itself be watched, and
// waits there for the person to log in.
func windowLogin(ctx context.Context, browser, address, server string, progress io.Writer) (string, error) {
	browsers, err := login.FindBrowsers(login.ThisSystem(), browser)
	if err != nil {
		return "", err
	}
	var blocked []string
	for _, candidate := range browsers {
		window, err := login.Start(ctx, candidate, address, login.Options{})
		var stopped *login.BlockedError
		if errors.As(err, &stopped) {
			fmt.Fprintf(progress, "mm-mcp: %v; close its window if one opened. Trying the next browser.\n", err)
			blocked = append(blocked, err.Error())
			continue
		}
		if err != nil {
			return "", err
		}
		token, err := window.WaitForToken(ctx, server)
		window.Close()
		return token, err
	}
	return "", fmt.Errorf("no browser would let mm-mcp watch the login: %s", strings.Join(blocked, "; "))
}

// logOut ends the stored session at Mattermost and forgets it.
func logOut(ctx context.Context, args []string, deps Deps) int {
	flags := flag.NewFlagSet("mm-mcp logout", flag.ContinueOnError)
	flags.SetOutput(deps.Stderr)
	address, ok := addressFlag(flags, args, deps)
	if !ok {
		return ExitConfig
	}
	if deps.Credentials == nil {
		fmt.Fprintln(deps.Stderr, "mm-mcp: this build keeps no logins")
		return ExitFailure
	}
	token, found, err := deps.Credentials.Load(address)
	switch {
	case err != nil:
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	case !found:
		fmt.Fprintf(deps.Stdout, "No login is stored for %s.\n", address)
		return ExitOK
	}
	// Ending the session at Mattermost is best done, not required: one that
	// expired already is gone there too.
	if err := mattermost.New(address, token, network.NewSafeTransport()).Logout(ctx); err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: warning: Mattermost did not end the session: %v\n", err)
	}
	if err := deps.Credentials.Delete(address); err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(deps.Stdout, "Logged out of %s, and the session is forgotten.\n", address)
	return ExitOK
}
