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
	"github.com/vriesdemichael/mm-mcp/internal/doctor"
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
	// Browsers finds the browsers a login window could use, without starting
	// any, for `mm-mcp doctor` to name.
	Browsers func() ([]login.Browser, error)
}

// ErrNoInput is the answer to a prompt in a terminal with nothing to read.
var ErrNoInput = errors.New("nothing could be read here: run mm-mcp login in a terminal you can type in")

// ProcessLogin reaches the person through their own browser and terminal.
func ProcessLogin(stdin *os.File, stderr io.Writer) *Login {
	reader := bufio.NewReader(stdin)
	line := func(prompt string) (string, error) {
		fmt.Fprint(stderr, prompt)
		text, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && text == "" {
			// As in an AI agent's shell, which nobody types into.
			return "", ErrNoInput
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(text), nil
	}
	return &Login{
		Open:     func(_ context.Context, address string) error { return login.OpenInBrowser(address) },
		Window:   windowLogin,
		Browsers: func() ([]login.Browser, error) { return login.FindBrowsers(login.ThisSystem(), "") },
		Line:     line,
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
	transport, err := loginTransport(deps)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitConfig
	}
	waiting, cancel := context.WithTimeout(ctx, LoginTimeout)
	defer cancel()
	httpClient := &http.Client{Transport: transport, Timeout: mattermost.RequestTimeout, CheckRedirect: network.SameOriginRedirects}

	signIn, err := login.Discover(waiting, httpClient, address)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: warning: %v; trying a browser window\n", err)
		signIn = login.SignIn{SSO: []string{"single sign-on"}}
	}
	client := storedClient(deps, address, given)
	plan := routes(signIn, client, given.with)
	// failed is why each way tried did not log in, to sum up at the end, where
	// the person reads, after the browser windows and prompts in between.
	var failed []string
	for _, route := range plan {
		token, oauthClient, err := runRoute(waiting, deps, route, address, signIn, client, given, httpClient)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %s: %v\n", routeNames[route], err)
			failed = append(failed, routeNames[route]+": "+err.Error())
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				break
			}
			continue
		}
		user, _, err := mattermost.New(address, token, transport).Check(ctx)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %s: the token does not work: %v\n", routeNames[route], err)
			failed = append(failed, routeNames[route]+": the token does not work: "+err.Error())
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
		if deps.Credentials.StoreOrigin != nil {
			if err := deps.Credentials.StoreOrigin(address, route); err != nil {
				fmt.Fprintf(deps.Stderr, "mm-mcp: warning: how this login was made could not be kept, so mm-mcp logout will end it at Mattermost: %v\n", err)
			}
		}
		fmt.Fprintf(deps.Stdout, "Logged in to %s as @%s. The session is kept in %s.\n"+
			"mm-mcp serve uses it whenever MM_URL is %s and MM_TOKEN is not set. When Mattermost ends the session, run mm-mcp login again.\n",
			address, user.Username, deps.Credentials.Where, address)
		return ExitOK
	}
	fmt.Fprint(deps.Stderr, advice(signIn, client, given.with, address, failed))
	return ExitFailure
}

// routeNames are the ways to log in as the person reads them.
var routeNames = map[string]string{
	WithOAuth:    "OAuth",
	WithWindow:   "browser window",
	WithPassword: "password",
	WithPaste:    "pasted token",
}

// routes are the ways to try, in order: the one asked for alone; OAuth alone
// where the server offers it; or else a browser window for single sign-on, the
// terminal for a server that keeps passwords, and a pasted token last.
func routes(signIn login.SignIn, client login.Client, with string) []string {
	if with != "" {
		return []string{with}
	}
	// Where the server offers OAuth, an administrator opened that way in, and
	// it is the only one tried; another is used only when named (ADR-034).
	if offersOAuth(signIn, client) {
		return []string{WithOAuth}
	}
	var plan []string
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
		return "", login.Client{}, errors.New("not available here")
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
			return "", login.Client{}, err
		}
		return token.Access, token.Client, nil
	case WithWindow:
		fmt.Fprintf(deps.Stderr, "Opening Mattermost's login page in a browser window of its own. Log in there as you always do; the window closes once you have.\n")
		token, err := deps.Login.Window(ctx, given.browser, address+"/login", address, deps.Stderr)
		if err != nil {
			return "", login.Client{}, err
		}
		return token, login.Client{}, nil
	case WithPassword:
		token, err := passwordRoute(ctx, deps, address, httpClient.Transport)
		if err != nil {
			return "", login.Client{}, err
		}
		return token, login.Client{}, nil
	default:
		fmt.Fprintf(deps.Stderr, "Paste a personal access token, a bot's token, or the value of the MMAUTHTOKEN cookie your browser holds for %s.\n"+
			"Paste it here only, never into a conversation with an AI agent: it is everything you can do in Mattermost.\n", address)
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
func passwordRoute(ctx context.Context, deps Deps, address string, transport http.RoundTripper) (string, error) {
	id, err := deps.Login.Line("Email address or username: ")
	switch {
	case err != nil:
		return "", err
	case id == "":
		return "", errors.New("no login was given")
	}
	password, err := deps.Login.Secret("Password: ")
	if err != nil {
		return "", err
	}
	token, err := mattermost.PasswordLogin(ctx, address, transport, id, password, "")
	if errors.Is(err, mattermost.ErrMFARequired) {
		code, codeErr := deps.Login.Line("Code from your authenticator app: ")
		if codeErr != nil {
			return "", codeErr
		}
		token, err = mattermost.PasswordLogin(ctx, address, transport, id, password, code)
	}
	return token, err
}

// advice is what to do when no way worked, for the server as it signs people in,
// after why each way tried failed.
func advice(signIn login.SignIn, client login.Client, with, address string, failed []string) string {
	var b strings.Builder
	b.WriteString("\nmm-mcp found no way to log in to " + address + " that worked:\n")
	for _, why := range failed {
		b.WriteString("  - " + why + "\n")
	}
	b.WriteString("\nWhat works from here:\n")
	if with == "" && offersOAuth(signIn, client) {
		b.WriteString("  - Another way: your server offers OAuth, so mm-mcp tried only that. Name another to try it:\n" +
			"      mm-mcp login --with window, --with password or --with paste --url " + address + "\n")
	}
	b.WriteString("  - A browser window: install Playwright's Chromium, a browser of its own in your user folder, with\n" +
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
var windowLogin = WindowLogin(login.Options{}, nil)

// WindowLogin is the Login.Window that starts browsers with options, and, when
// ready is given, hands it each window before waiting, as a test that logs in
// from the page does.
func WindowLogin(options login.Options, ready func(context.Context, login.Window) error) func(context.Context, string, string, string, io.Writer) (string, error) {
	return func(ctx context.Context, browser, address, server string, progress io.Writer) (string, error) {
		return tryBrowsers(ctx, browser, address, server, progress, options, ready)
	}
}

func tryBrowsers(ctx context.Context, browser, address, server string, progress io.Writer, options login.Options, ready func(context.Context, login.Window) error) (string, error) {
	browsers, err := login.FindBrowsers(login.ThisSystem(), browser)
	if err != nil {
		return "", err
	}
	var blocked []string
	for _, candidate := range browsers {
		window, err := login.Start(ctx, candidate, address, options)
		var stopped *login.BlockedError
		if errors.As(err, &stopped) {
			fmt.Fprintf(progress, "mm-mcp: %v; close its window if one opened. Trying the next browser.\n", err)
			blocked = append(blocked, err.Error())
			continue
		}
		if err != nil {
			return "", err
		}
		if window.Unsandboxed() {
			fmt.Fprintf(progress, "mm-mcp: %s runs without its sandbox, which this system refuses it; the window shows only your login page and closes once you have logged in.\n", candidate.Name)
		}
		if ready != nil {
			if err := ready(ctx, window); err != nil {
				window.Close()
				return "", err
			}
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
	origin := ""
	if deps.Credentials.LoadOrigin != nil {
		origin, _ = deps.Credentials.LoadOrigin(address)
	}
	// A token the person pasted is theirs: a personal access token, a bot's, or
	// their browser's own session, which ending it would log them out of.
	if origin != WithPaste {
		transport, err := loginTransport(deps)
		if err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
			return ExitConfig
		}
		// Ending the session at Mattermost is best done, not required: one
		// that expired already is gone there too.
		if err := mattermost.New(address, token, transport).Logout(ctx); err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: warning: Mattermost did not end the session: %v\n", err)
		}
	}
	if err := deps.Credentials.Delete(address); err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	}
	if deps.Credentials.StoreOrigin != nil {
		_ = deps.Credentials.StoreOrigin(address, "")
	}
	if origin == WithPaste {
		fmt.Fprintf(deps.Stdout, "Forgot the token you pasted for %s. It still works at Mattermost: revoke a personal access token under Profile > Security, or log out of the browser a cookie came from.\n", address)
		return ExitOK
	}
	fmt.Fprintf(deps.Stdout, "Logged out of %s, and the session is forgotten.\n", address)
	return ExitOK
}

// loginTransport is the transport a login and a logout reach the server with:
// the one mm-mcp serve and doctor use, trusting the certificate authorities in
// MM_MCP_CA_FILE beside the system's when it is set.
func loginTransport(deps Deps) (http.RoundTripper, error) {
	caFile := strings.TrimSpace(deps.Getenv(config.EnvCAFile))
	if config.Placeholder(caFile) {
		caFile = ""
	}
	return doctor.Transport(config.Config{CAFile: caFile})
}

// offersOAuth reports whether mm-mcp can log in through the server's OAuth
// service: it is on, and mm-mcp may register itself or has an app to log in as.
func offersOAuth(signIn login.SignIn, client login.Client) bool {
	return signIn.OAuth && (signIn.OAuthRegistering || client.ID != "")
}
