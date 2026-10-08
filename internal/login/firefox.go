package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// A Firefox is watched through WebDriver BiDi, the protocol Firefox's remote
// agent speaks. It reads every cookie, HttpOnly ones included, while the
// person uses the window as they always would.

// firefoxPrefs keep a new profile from opening pages of its own on the first
// start, which would stand between the person and the login page.
const firefoxPrefs = `user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.startup.homepage_override.mstone", "ignore");
user_pref("browser.aboutwelcome.enabled", false);
user_pref("datareporting.policy.dataSubmissionEnabled", false);
user_pref("toolkit.telemetry.reportingpolicy.firstRun", false);
user_pref("trailhead.firstrun.didSeeAboutWelcome", true);
`

type firefoxWindow struct {
	browser Browser
	process *exec.Cmd
	output  *tail
	profile string
	conn    *websocket.Conn
	context string // the browsing context of the window's tab

	mu      sync.Mutex
	nextID  int
	waiting map[int]chan bidiReply
	closed  chan struct{}
	exited  chan struct{}
}

type bidiReply struct {
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func startFirefox(ctx context.Context, browser Browser, profile, address string, options Options) (*firefoxWindow, error) {
	if err := os.WriteFile(filepath.Join(profile, "user.js"), []byte(firefoxPrefs), 0o600); err != nil {
		return nil, fmt.Errorf("making Firefox's profile: %w", err)
	}
	args := []string{"-profile", profile, "-no-remote", "-new-instance", "--remote-debugging-port", "0"}
	if runtime.GOOS == "windows" {
		// On Windows firefox.exe is a launcher that starts the browser and
		// exits; this keeps it until the browser exits, so mm-mcp can wait on it.
		args = append(args, "-wait-for-browser")
	}
	if options.Headless {
		args = append(args, "-headless")
	}
	args = append(args, address)
	// Not bound to ctx: ending a context kills only the browser's first
	// process, which leaves its others running with the profile; Close ends
	// it whole, through the browser's own protocol.
	process := exec.Command(browser.Path, args...) //nolint:gosec,noctx // the browser the person has, or names
	output := &tail{}
	process.Stdout, process.Stderr = output, output
	// A browser that hands its window to a process of its own keeps the
	// output open after the first exits; Wait does not wait for it.
	process.WaitDelay = time.Second
	if err := process.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", browser, err)
	}
	w := &firefoxWindow{browser: browser, process: process, output: output, profile: profile, waiting: map[int]chan bidiReply{}, closed: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		_ = process.Wait()
		close(w.exited)
	}()
	endpoint, err := w.agentEndpoint(ctx)
	if err != nil {
		w.Close()
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint+"/session", nil) //nolint:bodyclose // a websocket's response has no body to close
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("connecting to %s: %w", browser.Name, err)
	}
	w.conn = conn
	go w.read()
	if err := w.call(ctx, "session.new", map[string]any{"capabilities": map[string]any{}}, nil); err != nil {
		w.Close()
		return nil, err
	}
	var tree struct {
		Contexts []struct {
			Context string `json:"context"`
		} `json:"contexts"`
	}
	if err := w.call(ctx, "browsingContext.getTree", map[string]any{"maxDepth": 0}, &tree); err != nil || len(tree.Contexts) == 0 {
		w.Close()
		return nil, errors.Join(errors.New("no tab is open in Firefox"), err)
	}
	w.context = tree.Contexts[0].Context
	return w, nil
}

func (w *firefoxWindow) read() {
	defer close(w.closed)
	for {
		var message struct {
			ID *int `json:"id"`
			bidiReply
		}
		if err := w.conn.ReadJSON(&message); err != nil {
			return
		}
		if message.ID == nil || message.Type == "event" {
			continue
		}
		w.mu.Lock()
		waiting := w.waiting[*message.ID]
		delete(w.waiting, *message.ID)
		w.mu.Unlock()
		if waiting != nil {
			waiting <- message.bidiReply
		}
	}
}

func (w *firefoxWindow) call(ctx context.Context, method string, params, out any) error {
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	answer := make(chan bidiReply, 1)
	w.waiting[id] = answer
	err := w.conn.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	w.mu.Unlock()
	if err != nil {
		return ErrBrowserClosed
	}
	select {
	case got := <-answer:
		if got.Type == "error" {
			return fmt.Errorf("%s: %s: %s", method, got.Error, got.Message)
		}
		if out != nil {
			return json.Unmarshal(got.Result, out)
		}
		return nil
	case <-w.closed:
		return ErrBrowserClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *firefoxWindow) WaitForToken(ctx context.Context, address string) (string, error) {
	return waitForToken(ctx, w.closed, address, w.Cookies)
}

func (w *firefoxWindow) Cookies(ctx context.Context) ([]Cookie, error) {
	var got struct {
		Cookies []struct {
			Name  string `json:"name"`
			Value struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"value"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
		} `json:"cookies"`
	}
	if err := w.call(ctx, "storage.getCookies", map[string]any{}, &got); err != nil {
		return nil, err
	}
	cookies := make([]Cookie, 0, len(got.Cookies))
	for _, c := range got.Cookies {
		if c.Value.Type == "string" {
			cookies = append(cookies, Cookie{Name: c.Name, Value: c.Value.Value, Domain: c.Domain, Path: c.Path})
		}
	}
	return cookies, nil
}

func (w *firefoxWindow) Navigate(ctx context.Context, address string) error {
	return w.call(ctx, "browsingContext.navigate", map[string]any{"context": w.context, "url": address, "wait": "complete"}, nil)
}

func (w *firefoxWindow) Evaluate(ctx context.Context, script string) error {
	var evaluated struct {
		Type             string `json:"type"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := w.call(ctx, "script.evaluate", map[string]any{
		"expression": script, "awaitPromise": true, "target": map[string]any{"context": w.context},
	}, &evaluated); err != nil {
		return err
	}
	if evaluated.Type == "exception" {
		text := "an exception"
		if evaluated.ExceptionDetails != nil {
			text = evaluated.ExceptionDetails.Text
		}
		return fmt.Errorf("the script failed: %s", text)
	}
	return nil
}

func (w *firefoxWindow) Close() {
	if w.conn != nil {
		closing, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = w.call(closing, "browser.close", map[string]any{}, nil)
		cancel()
		_ = w.conn.Close()
	}
	select {
	case <-w.exited:
	case <-time.After(5 * time.Second):
		_ = w.process.Process.Kill()
		<-w.exited
	}
	removeProfile(w.profile)
}

// agentEndpoint waits for Firefox's remote agent to listen, and is where it
// does. Firefox writes its address into WebDriverBiDiServer.json in the
// profile; the program started may exit at once and leave the browser to a
// process of its own, so only the deadline ends the wait.
func (w *firefoxWindow) agentEndpoint(ctx context.Context) (string, error) {
	deadline := time.Now().Add(startTimeout)
	file := filepath.Join(w.profile, "WebDriverBiDiServer.json")
	for {
		if raw, err := os.ReadFile(file); err == nil { //nolint:gosec // a file in the profile mm-mcp made
			if endpoint, err := ParseBiDiServer(raw); err == nil {
				return endpoint, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			reason := fmt.Sprintf("started no remote agent within %s; a company policy that forbids remote control does that", startTimeout)
			select {
			case <-w.exited:
				reason = "exited as it started, without starting its remote agent" + w.output.said()
			default:
			}
			return "", &BlockedError{Browser: w.browser, Reason: reason}
		}
	}
}

// ParseBiDiServer reads the WebDriverBiDiServer.json file Firefox writes once
// its remote agent listens.
func ParseBiDiServer(raw []byte) (string, error) {
	var server struct {
		Host string `json:"ws_host"`
		Port int    `json:"ws_port"`
	}
	if err := json.Unmarshal(raw, &server); err != nil {
		return "", err
	}
	if server.Port <= 0 || server.Port > 65535 {
		return "", fmt.Errorf("the remote agent's file names no port: %s", raw)
	}
	host := server.Host
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	return "ws://" + net.JoinHostPort(host, strconv.Itoa(server.Port)), nil
}

func (w *firefoxWindow) Unsandboxed() bool { return false }
