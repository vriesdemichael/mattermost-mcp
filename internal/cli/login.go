package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/login"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// LoginTimeout bounds how long `mm-mcp login` waits for the person to log in.
const LoginTimeout = 10 * time.Minute

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

// logIn opens the person's browser at the server's login page, waits for them
// to log in, checks the session with Mattermost, and stores it (ADR-019).
func logIn(ctx context.Context, args []string, deps Deps) int {
	flags := flag.NewFlagSet("mm-mcp login", flag.ContinueOnError)
	flags.SetOutput(deps.Stderr)
	browser := flags.String("browser", "", "the Chrome, Edge or Chromium to log in with, by path or command; the first one installed when not given")
	address, ok := addressFlag(flags, args, deps)
	if !ok {
		return ExitConfig
	}
	if deps.Credentials == nil || deps.Login == nil {
		fmt.Fprintln(deps.Stderr, "mm-mcp: this build cannot log in")
		return ExitFailure
	}
	fmt.Fprintf(deps.Stderr, "Opening Mattermost at %s in a browser window of its own. Log in there as you always do; the window closes once you have.\n", address)
	waiting, cancel := context.WithTimeout(ctx, LoginTimeout)
	defer cancel()
	token, err := deps.Login(waiting, *browser, address)
	switch {
	case errors.Is(err, login.ErrNoBrowser):
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v. Install Chrome or Edge, or name one with --browser.\n", err)
		return ExitFailure
	case errors.Is(err, context.DeadlineExceeded):
		fmt.Fprintf(deps.Stderr, "mm-mcp: nobody logged in within %s.\n", LoginTimeout)
		return ExitFailure
	case err != nil:
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	}
	user, _, err := mattermost.New(address, token, network.NewSafeTransport()).Check(ctx)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: the session the browser holds does not work: %v\n", err)
		return ExitFailure
	}
	if err := deps.Credentials.Store(address, token); err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(deps.Stdout, "Logged in to %s as @%s. The session is kept in %s.\n"+
		"mm-mcp serve uses it whenever MM_URL is %s and MM_TOKEN is not set. When Mattermost ends the session, run mm-mcp login again.\n",
		address, user.Username, deps.Credentials.Where, address)
	return ExitOK
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

// browserLogin finds the person's browser, opens it at the login page, and
// waits for Mattermost's session cookie.
func browserLogin(ctx context.Context, browser, address string) (string, error) {
	path, err := login.FindBrowser(login.ThisSystem(), browser)
	if err != nil {
		return "", err
	}
	session, err := login.Start(ctx, path, address+"/login", login.Options{})
	if err != nil {
		return "", err
	}
	defer session.Close()
	return session.WaitForToken(ctx, address)
}
