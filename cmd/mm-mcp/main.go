// Command mm-mcp is an MCP server for Mattermost.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/vriesdemichael/mattermost-mcp/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], cli.ProcessDeps())
	stop()
	os.Exit(code)
}
