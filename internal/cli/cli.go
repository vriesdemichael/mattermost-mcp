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
	"os"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
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
}

// ProcessDeps is the real process: its environment, its streams, and a real listener.
func ProcessDeps() Deps {
	return Deps{Getenv: os.Getenv, Stdout: os.Stdout, Stderr: os.Stderr, Serve: Serve}
}

const usage = `mm-mcp is an MCP server for Mattermost.

Usage:
  mm-mcp serve [--transport stdio|http] [--host 127.0.0.1] [--port 8765]
  mm-mcp version

Credentials come from the environment, never from a flag: set MM_URL and
MM_TOKEN in the MCP client's env block. MM_MCP_ALLOW_WRITES=true offers the
tools that post, and each of them asks before it acts.
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

	cfg, err := config.FromEnv(deps.Getenv)
	if err != nil {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitConfig
	}
	client := mattermost.New(cfg.URL, cfg.Token, network.NewSafeTransport())
	mcpServer := server.New(cfg, server.Single(client))

	options := ServeOptions{Transport: *transport, Address: net.JoinHostPort(*host, strconv.Itoa(*port))}
	if err := deps.Serve(ctx, mcpServer, options); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(deps.Stderr, "mm-mcp: %v\n", err)
		return ExitFailure
	}
	return ExitOK
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
	mux := http.NewServeMux()
	mux.Handle(HTTPPath, mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil))
	httpServer := &http.Server{Addr: options.Address, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
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
