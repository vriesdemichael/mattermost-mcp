package login

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// CookieName is the cookie Mattermost's web app keeps its session token in.
const CookieName = "MMAUTHTOKEN"

// How long the browser has to start and open its DevTools port, and how
// often the cookies are looked at while the person logs in.
const (
	startTimeout = 30 * time.Second
	pollEvery    = 500 * time.Millisecond
)

// ErrBrowserClosed is the answer when the browser closes before Mattermost
// set its session cookie.
var ErrBrowserClosed = errors.New("the browser was closed before the login finished")

// Session is a browser mm-mcp started, and the DevTools connection to it.
type Session struct {
	process *exec.Cmd
	profile string
	conn    *websocket.Conn

	mu      sync.Mutex
	nextID  int
	waiting map[int]chan reply
	closed  chan struct{}
	exited  chan struct{}
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Options says how to start the browser.
type Options struct {
	// Headless starts it without a window, for a test that logs in itself.
	Headless bool
}

// Start starts the browser at path with a profile of its own, open at
// address, and connects to it.
func Start(ctx context.Context, path, address string, options Options) (*Session, error) {
	profile, err := os.MkdirTemp("", "mm-mcp-login-")
	if err != nil {
		return nil, fmt.Errorf("making the browser's profile: %w", err)
	}
	args := []string{
		"--user-data-dir=" + profile,
		"--remote-debugging-port=0",
		"--remote-allow-origins=http://127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-sync",
		"--new-window",
	}
	if options.Headless {
		args = append(args, "--headless=new", "--disable-gpu")
	}
	args = append(args, address)
	process := exec.CommandContext(ctx, path, args...) //nolint:gosec // the browser the person has, or names
	if err := process.Start(); err != nil {
		_ = os.RemoveAll(profile)
		return nil, fmt.Errorf("starting %s: %w", path, err)
	}
	s := &Session{process: process, profile: profile, waiting: map[int]chan reply{}, closed: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		_ = process.Wait()
		close(s.exited)
	}()
	endpoint, err := s.devToolsEndpoint(ctx)
	if err != nil {
		s.Close()
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil) //nolint:bodyclose // a websocket's response has no body to close
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("connecting to the browser: %w", err)
	}
	s.conn = conn
	go s.read()
	return s, nil
}

// devToolsEndpoint waits for the browser to write the port it listens on, as
// --remote-debugging-port=0 has it do, into the profile.
func (s *Session) devToolsEndpoint(ctx context.Context) (string, error) {
	deadline := time.Now().Add(startTimeout)
	file := filepath.Join(s.profile, "DevToolsActivePort")
	for {
		if raw, err := os.ReadFile(file); err == nil { //nolint:gosec // a file in the profile mm-mcp made
			if endpoint, err := ParseActivePort(string(raw)); err == nil {
				return endpoint, nil
			}
		}
		select {
		case <-s.exited:
			return "", errors.New("the browser exited as it started; is another copy of it holding its profile?")
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("the browser opened no DevTools port within %s", startTimeout)
		}
	}
}

// ParseActivePort reads the DevToolsActivePort file a browser writes: its
// port on the first line, the browser's own target on the second.
func ParseActivePort(content string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	var lines []string
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 2 {
		return "", errors.New("the DevTools port file is not written yet")
	}
	port, err := strconv.Atoi(lines[0])
	if err != nil || port <= 0 || port > 65535 || !strings.HasPrefix(lines[1], "/devtools/browser/") {
		return "", fmt.Errorf("the DevTools port file reads %q", content)
	}
	return "ws://" + net.JoinHostPort("127.0.0.1", lines[0]) + lines[1], nil
}

func (s *Session) read() {
	defer close(s.closed)
	for {
		var message struct {
			ID int `json:"id"`
			reply
		}
		if err := s.conn.ReadJSON(&message); err != nil {
			return
		}
		if message.ID == 0 {
			continue // an event; none is listened for
		}
		s.mu.Lock()
		waiting := s.waiting[message.ID]
		delete(s.waiting, message.ID)
		s.mu.Unlock()
		if waiting != nil {
			waiting <- message.reply
		}
	}
}

// call sends one DevTools command, to the browser or, with a session id, to
// one of its pages, and reads its result into out.
func (s *Session) call(ctx context.Context, sessionID, method string, params, out any) error {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	answer := make(chan reply, 1)
	s.waiting[id] = answer
	command := map[string]any{"id": id, "method": method, "params": params}
	if sessionID != "" {
		command["sessionId"] = sessionID
	}
	err := s.conn.WriteJSON(command)
	s.mu.Unlock()
	if err != nil {
		return ErrBrowserClosed
	}
	select {
	case got := <-answer:
		if got.Error != nil {
			return fmt.Errorf("%s: %s", method, got.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(got.Result, out)
		}
		return nil
	case <-s.closed:
		return ErrBrowserClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cookie is one cookie the browser holds.
type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
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

// WaitForToken waits while the person logs in, and answers with the session
// token Mattermost sets for the server at address once it does.
func (s *Session) WaitForToken(ctx context.Context, address string) (string, error) {
	for {
		var cookies struct {
			Cookies []Cookie `json:"cookies"`
		}
		if err := s.call(ctx, "", "Storage.getCookies", map[string]any{}, &cookies); err != nil {
			return "", err
		}
		if token, ok := SessionToken(cookies.Cookies, address); ok {
			return token, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-s.closed:
			return "", ErrBrowserClosed
		case <-time.After(pollEvery):
		}
	}
}

// Evaluate runs a script in the browser's first page and waits for the
// promise it returns, for a test that logs in itself.
func (s *Session) Evaluate(ctx context.Context, script string) error {
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := s.call(ctx, "", "Target.getTargets", map[string]any{}, &targets); err != nil {
		return err
	}
	for _, target := range targets.TargetInfos {
		if target.Type != "page" {
			continue
		}
		var attached struct {
			SessionID string `json:"sessionId"`
		}
		if err := s.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
			return err
		}
		var evaluated struct {
			ExceptionDetails *struct {
				Text string `json:"text"`
			} `json:"exceptionDetails"`
		}
		if err := s.call(ctx, attached.SessionID, "Runtime.evaluate", map[string]any{"expression": script, "awaitPromise": true}, &evaluated); err != nil {
			return err
		}
		if evaluated.ExceptionDetails != nil {
			return fmt.Errorf("the script failed: %s", evaluated.ExceptionDetails.Text)
		}
		return nil
	}
	return errors.New("the browser has no page open")
}

// Close closes the browser and throws its profile away.
func (s *Session) Close() {
	if s.conn != nil {
		closing, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.call(closing, "", "Browser.close", map[string]any{}, nil)
		cancel()
		_ = s.conn.Close()
	}
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
		_ = s.process.Process.Kill()
		<-s.exited
	}
	// The browser may hold files in the profile a moment after it exits.
	for range 20 {
		if err := os.RemoveAll(s.profile); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
