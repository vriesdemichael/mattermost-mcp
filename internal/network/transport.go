// Package network holds the one HTTP transport mm-mcp's clients are built on.
package network

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/vriesdemichael/mm-mcp/internal/config"
)

// LoopbackHosts are the hosts the network block lets through.
var LoopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// ExternalNetworkBlockedError is a request refused because it would have left
// the machine while the network block was on.
type ExternalNetworkBlockedError struct{ Host string }

func (e *ExternalNetworkBlockedError) Error() string {
	return fmt.Sprintf("refused a request to %s: %s is 1, so only the loopback addresses may be reached",
		e.Host, config.EnvBlockExternalNetwork)
}

type safeTransport struct {
	inner         http.RoundTripper
	blockExternal bool
}

func (t *safeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.blockExternal && !LoopbackHosts[request.URL.Hostname()] {
		return nil, &ExternalNetworkBlockedError{Host: request.URL.Hostname()}
	}
	return t.inner.RoundTrip(request)
}

// Wrap puts the network block in front of inner. blockExternal decides whether
// it refuses hosts beyond the machine.
func Wrap(inner http.RoundTripper, blockExternal bool) http.RoundTripper {
	return &safeTransport{inner: inner, blockExternal: blockExternal}
}

// NewSafeTransport is the transport every HTTP client in mm-mcp is built on.
// While MM_MCP_BLOCK_EXTERNAL_NETWORK is 1 it refuses every host but the
// loopback addresses, so a unit test that reaches beyond the machine fails at
// once and names the host (ADR-006).
func NewSafeTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return Wrap(http.DefaultTransport, os.Getenv(config.EnvBlockExternalNetwork) == "1")
	}
	inner := base.Clone()
	inner.ResponseHeaderTimeout = 60 * time.Second
	return Wrap(inner, os.Getenv(config.EnvBlockExternalNetwork) == "1")
}
