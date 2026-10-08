// Package cli is the mm-mcp command line: start the server over stdio or HTTP.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/credstore"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
	"github.com/vriesdemichael/mm-mcp/internal/server"
	"github.com/vriesdemichael/mm-mcp/internal/version"
)

// Exit codes.
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitConfig  = 2
)

// Defaults for the HTTP transport.
const (
	DefaultHTTPHost = "127.0.0.1"
	DefaultHTTPPort = 8765
	HTTPPath        = "/mcp"
)

// ServeOptions says how to serve: over "stdio" or "http", and for HTTP, where.
type ServeOptions struct {
	Transport string
	Address   string
}

// Deps is what Run takes from the process, so a test can give it something else.
type Deps struct {
	Getenv func(string) string
	Stdout io.Writer
	Stderr io.Writer
	Serve  func(context.Context, *mcp.Server, ServeOptions) error
	// Credentials keeps the tokens `mm-mcp login` obtains; nil keeps none.
	Credentials *Credentials
	// Login is how `mm-mcp login` reaches the person: their browser, a
	// browser window of its own, and the terminal.
	Login *Login
}

// Credentials is where `mm-mcp login` keeps a token per server, and the OAuth
// client it logged in as.
type Credentials struct {
	Load        config.Stored
	Store       func(address, token string) error
	Delete      func(address string) error
	LoadClient  func(address string) (string, bool, error)
	StoreClient func(address, client string) error
	// Where names the store, for a person to find it.
	Where string
}

// ProcessDeps is the real process: its environment, its streams, a real
// listener, the system's credential store and its browser.
func ProcessDeps() Deps {
	return Deps{
		Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, Serve: Serve,
		Credentials: &Credentials{
			Load: credstore.Load, Store: credstore.Store, Delete: credstore.Delete,
			LoadClient: credstore.LoadClient, StoreClient: credstore.StoreClient, Where: credstore.Where(),
		},
		Login: ProcessLogin(os.Stdin, os.Stderr),
	}
}

const usage = `mm-mcp is an MCP server for Mattermost.

Usage:
  mm-mcp serve [--transport stdio|http] [--host 127.0.0.1] [--port 8765]
  mm-mcp login [--url https://chat.example.com] [--with oauth|window|password|paste]
               [--browser path] [--client-id id] [--callback-port 8766]
  mm-mcp logout [--url https://chat.example.com]
  mm-mcp version
  mm-mcp help

Credentials never come from a flag: set MM_URL and MM_TOKEN in the MCP
client's env block, or set only MM_URL after logging in once with
mm-mcp login, which logs you in the way your server allows: through your own
browser where it offers OAuth, a browser window of its own for single sign-on,
your password in the terminal, or a token you paste. It keeps the session in
the system's credential store. MM_MCP_ALLOW_WRITES=true offers
the tools that change Mattermost. A change others see asks before it acts;
following a thread, saving a post, a draft and the typing indicator do not.
`

// Run runs the command line and returns the process's exit code.
func Run(ctx context.Context, args []string, deps Deps) int {
	if len(args) == 0 {
		fmt.Fprint(deps.Stderr, usage)
		return ExitConfig
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Fprintf(deps.Stdout, "mm-mcp %s\n", version.Version)
		return ExitOK
	case "help", "--help", "-h":
		fmt.Fprint(deps.Stdout, usage)
		return ExitOK
	case "serve":
		return serve(ctx, args[1:], deps)
	case "login":
		return logIn(ctx, args[1:], deps)
	case "logout":
		return logOut(ctx, args[1:], deps)
	default:
		fmt.Fprintf(deps.Stderr, "mm-mcp: unknown command %q\n\n%s", args[0], usage)
		return ExitConfig
	}
}

func serve(ctx context.Context, args []string, deps Deps) int {
	flags := flag.NewFlagSet("mm-mcp serve", flag.ContinueOnError)
	flags.SetOutput(deps.Stderr)
	transport := flags.String("transport", "stdio", "stdio for a client that starts the server itself, http for Streamable HTTP")
	host := flags.String("host", DefaultHTTPHost, "loopback address to bind with --transport http")
	port := flags.Int("port", DefaultHTTPPort, "port to bind with --transport http")
	if err := flags.Parse(args); err != nil {
		return ExitConfig
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(deps.Stderr, "mm-mcp serve takes no arguments, got %q\n", flags.Args())
		return ExitConfig
	}
	if *transport != "stdio" && *transport != "http" {
		fmt.Fprintf(deps.Stderr, "mm-mcp: --transport must be stdio or http, not %q\n", *transport)
		return ExitConfig
	}
	if *transport == "http" && !IsLoopback(*host) {
		// Over HTTP the server acts as its one configured identity for anyone who
		// reaches the port, and it authenticates no client yet (ADR-020).
		fmt.Fprintf(deps.Stderr, "mm-mcp: refusing to bind %s: the HTTP transport serves only a loopback address until it authenticates its clients\n", *host)
		return ExitConfig
	}

	var stored config.Stored
	if deps.Credentials != nil {
		stored = deps.Credentials.Load
	}
	cfg, err := config.Load(deps.Getenv, stored)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitConfig
	}
	cfg.Local = *transport == "stdio"
	httpTransport := network.NewSafeTransport()
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err == nil {
			err = network.TrustCertificates(httpTransport, pem)
		}
		if err != nil {
			fmt.Fprintf(deps.Stderr, "mm-mcp: %s %s cannot be used: %v\n", config.EnvCAFile, cfg.CAFile, err)
			return ExitConfig
		}
	}
	if unencrypted(cfg.URL) {
		fmt.Fprintf(deps.Stderr, "mm-mcp: warning: %s is plain http, so the token in %s crosses the network unencrypted; use the https address if the server has one\n", cfg.URL, config.EnvToken)
	}
	client := mattermost.New(cfg.URL, cfg.Token, httpTransport)
	if code, ok := preflight(ctx, client, cfg, deps.Stderr); !ok {
		return code
	}
	mcpServer := server.New(cfg, server.Single(client))

	options := ServeOptions{Transport: *transport, Address: net.JoinHostPort(*host, strconv.Itoa(*port))}
	if options.Transport == "http" {
		fmt.Fprintf(deps.Stderr, "mm-mcp: serving MCP over Streamable HTTP at http://%s%s\n", options.Address, HTTPPath)
	}
	if err := deps.Serve(ctx, mcpServer, options); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	}
	return ExitOK
}

// PreflightTimeout bounds the check of the server and the credential at start.
const PreflightTimeout = 15 * time.Second

// preflight asks Mattermost who the credential belongs to before serving, so a
// wrong address or a refused token stops the server at start with what to fix,
// in the client's log, instead of failing every tool call later. A server that
// cannot be reached is only warned about: a laptop starts its MCP client before
// its VPN, and the tools work once Mattermost can be reached.
func preflight(ctx context.Context, client *mattermost.Client, cfg config.Config, stderr io.Writer) (int, bool) {
	checking, cancel := context.WithTimeout(ctx, PreflightTimeout)
	defer cancel()
	user, serverVersion, err := client.Check(checking)
	var answered *mattermost.Error
	var redirect *network.RedirectError
	switch {
	case err == nil:
	case errors.As(err, &answered) && answered.Status == http.StatusUnauthorized && cfg.TokenStored:
		fmt.Fprintf(stderr, "mm-mcp: %s refused the session `mm-mcp login` stored: it expired or was logged out. Run `mm-mcp login --url %s` again, and restart.\n", cfg.URL, cfg.URL)
		return ExitConfig, false
	case errors.As(err, &answered) && answered.Status == http.StatusUnauthorized:
		fmt.Fprintf(stderr, "mm-mcp: %s refused the token in %s: it is wrong, was revoked, or is a session that expired. Get a new one, with `mm-mcp login` or as a personal access token, and restart.\n", cfg.URL, config.EnvToken)
		return ExitConfig, false
	case errors.Is(err, mattermost.ErrNotMattermost),
		errors.As(err, &answered) && (answered.Status == http.StatusNotFound || answered.ID == "" && answered.Status < http.StatusInternalServerError):
		fmt.Fprintf(stderr, "mm-mcp: %s does not answer as a Mattermost server (%v). Set %s to the address you open Mattermost at in a browser.\n", cfg.URL, err, config.EnvURL)
		return ExitConfig, false
	case errors.As(err, &redirect):
		fmt.Fprintf(stderr, "mm-mcp: %v\n", err)
		return ExitConfig, false
	case errors.As(err, &answered):
		fmt.Fprintf(stderr, "mm-mcp: warning: checking the token at %s failed: %v\n", cfg.URL, err)
		return ExitOK, true
	default:
		fmt.Fprintf(stderr, "mm-mcp: warning: %v; serving anyway, and each tool says so until Mattermost can be reached\n", err)
		return ExitOK, true
	}
	if mattermost.OlderThanSupported(serverVersion) {
		fmt.Fprintf(stderr, "mm-mcp: warning: %s runs Mattermost %s, older than %s, the oldest release mm-mcp supports; some tools may fail\n", cfg.URL, serverVersion, mattermost.OldestSupported)
	}
	fmt.Fprintf(stderr, "mm-mcp: connected to %s as @%s\n", cfg.URL, user.Username)
	return ExitOK, true
}

// unencrypted reports whether address is plain http to a host beyond this machine.
func unencrypted(address string) bool {
	parsed, err := url.Parse(address)
	return err == nil && parsed.Scheme == "http" && !IsLoopback(parsed.Hostname())
}

// IsLoopback reports whether host names this machine only.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Serve runs the server until ctx ends or the client goes away.
func Serve(ctx context.Context, mcpServer *mcp.Server, options ServeOptions) error {
	if options.Transport == "stdio" {
		return mcpServer.Run(ctx, &mcp.StdioTransport{})
	}
	httpServer := &http.Server{Addr: options.Address, Handler: HTTPHandler(mcpServer), ReadHeaderTimeout: 10 * time.Second}
	stopped := make(chan error, 1)
	go func() { stopped <- httpServer.ListenAndServe() }()
	select {
	case err := <-stopped:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown) //nolint:contextcheck // the request context has ended; shutdown needs its own
	}
}

// HTTPHandler serves mcpServer over Streamable HTTP at HTTPPath.
func HTTPHandler(mcpServer *mcp.Server) http.Handler {
	mux := http.NewServeMux()
	// A web page the person opens can send requests to a loopback port. The
	// SDK refuses a Host that is not loopback, which stops DNS rebinding; the
	// cross-origin protection refuses a request a browser marks as sent from
	// another origin, by its Origin or Sec-Fetch-Site header.
	mux.Handle(HTTPPath, http.NewCrossOriginProtection().Handler(
		mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "mm-mcp serves MCP at "+HTTPPath+", not here", http.StatusNotFound)
	})
	return mux
}
