package login

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// CookieName is the cookie Mattermost's web app keeps its session token in.
const CookieName = "MMAUTHTOKEN"

// How long a browser has to start and open its remote port, and how often the
// cookies are looked at while the person logs in. A browser that opened no port
// in time is most likely forbidden to by a policy.
const (
	startTimeout = 20 * time.Second
	pollEvery    = 500 * time.Millisecond
)

// ErrBrowserClosed is the answer when the browser closes before Mattermost
// set its session cookie.
var ErrBrowserClosed = errors.New("the browser was closed before the login finished")

// BlockedError is a browser that started but would not let mm-mcp watch it:
// it opened no remote port, as a company policy that forbids remote debugging
// makes it, or it exited at once, as a browser in a snap or a Flatpak, which
// cannot use a profile outside its sandbox, does.
type BlockedError struct {
	Browser Browser
	Reason  string
	// sandbox says it exited over its sandbox, which the system refused it.
	sandbox bool
}

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%s %s", e.Browser.Name, e.Reason)
}

// Window is a browser window mm-mcp started, with a profile of its own.
type Window interface {
	// WaitForToken waits while the person logs in, and answers with the
	// session token Mattermost sets for the server at address once they have.
	WaitForToken(ctx context.Context, address string) (string, error)
	// Cookies is every cookie the browser holds, HttpOnly ones included.
	Cookies(ctx context.Context) ([]Cookie, error)
	// Navigate opens address in the window's page.
	Navigate(ctx context.Context, address string) error
	// Evaluate runs a script in the window's page and waits for the promise
	// it returns, for a test that logs in itself.
	Evaluate(ctx context.Context, script string) error
	// Unsandboxed says the system refused the browser its sandbox, and it
	// runs without one.
	Unsandboxed() bool
	// Close closes the browser and throws its profile away.
	Close()
}

// Options says how to start a browser.
type Options struct {
	// Headless starts it without a window, for a test that logs in itself.
	Headless bool
}

// Start starts browser with a profile of its own, open at address.
func Start(ctx context.Context, browser Browser, address string, options Options) (Window, error) {
	if confined(browser.Path) {
		return nil, &BlockedError{Browser: browser, Reason: "is installed as a snap or a Flatpak, whose sandbox keeps it from using a profile of mm-mcp's"}
	}
	profile, err := os.MkdirTemp("", "mm-mcp-login-")
	if err != nil {
		return nil, fmt.Errorf("making the browser's profile: %w", err)
	}
	var window Window
	switch browser.Engine {
	case Firefox:
		window, err = startFirefox(ctx, browser, profile, address, options)
	default:
		window, err = startChromium(ctx, browser, profile, address, options)
	}
	if err != nil {
		removeProfile(profile)
		return nil, err
	}
	return window, nil
}

// confined reports a browser a snap or a Flatpak runs.
func confined(path string) bool {
	slashed := strings.ReplaceAll(path, `\`, "/")
	return strings.HasPrefix(slashed, "/snap/") || strings.Contains(slashed, "/flatpak/")
}

// waitForToken polls cookies, as the window reads them, until the session
// token for address is among them, or the window closes.
func waitForToken(ctx context.Context, closed <-chan struct{}, address string, cookies func(context.Context) ([]Cookie, error)) (string, error) {
	for {
		found, err := cookies(ctx)
		if err != nil {
			return "", err
		}
		if token, ok := SessionToken(found, address); ok {
			return token, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-closed:
			return "", ErrBrowserClosed
		case <-time.After(pollEvery):
		}
	}
}

// Cookie is one cookie a browser holds.
type Cookie struct {
	Name   string
	Value  string
	Domain string
	Path   string
}

// SessionToken is the Mattermost session token among cookies for the server
// at address: MMAUTHTOKEN, set for its host, on a path its address is under.
func SessionToken(cookies []Cookie, address string) (string, bool) {
	parsed, err := url.Parse(address)
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	for _, cookie := range cookies {
		domain := strings.TrimPrefix(strings.ToLower(cookie.Domain), ".")
		if cookie.Name != CookieName || cookie.Value == "" || (host != domain && !strings.HasSuffix(host, "."+domain)) {
			continue
		}
		if cookie.Path == "" || strings.HasPrefix(path, cookie.Path) || strings.HasPrefix(cookie.Path, strings.TrimRight(path, "/")) {
			return cookie.Value, true
		}
	}
	return "", false
}

// removeProfile deletes a browser's profile, which the browser may hold files
// in for a moment after it exits.
func removeProfile(profile string) {
	for range 20 {
		if err := os.RemoveAll(profile); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// tail keeps the last of what a browser writes to its output, to say why it
// exited as it started.
type tail struct {
	mu   sync.Mutex
	kept []byte
}

const tailBytes = 4 << 10

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.kept = append(t.kept, p...)
	if len(t.kept) > tailBytes {
		t.kept = t.kept[len(t.kept)-tailBytes:]
	}
	return len(p), nil
}

// said is the last lines the browser wrote, as a sentence's end, or nothing.
func (t *tail) said() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.kept)), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}
	// A crash ends in a stack trace; the line that says why came first.
	var fatal []string
	for _, line := range lines {
		if strings.Contains(line, "FATAL") {
			fatal = append(fatal, line)
		}
	}
	if len(fatal) > 0 {
		lines = fatal
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return "; it said: " + strings.Join(lines, " | ")
}

// mentions reports whether the browser wrote word, in any case.
func (t *tail) mentions(word string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Contains(strings.ToLower(string(t.kept)), strings.ToLower(word))
}
