package login

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// A Chromium (Chrome, Edge, Chromium, Playwright's) is watched through the
// DevTools protocol. Chrome refuses remote debugging on a person's everyday
// profile, so the window always has a profile of its own.

type chromiumWindow struct {
	browser Browser
	process *exec.Cmd
	profile string
	conn    *websocket.Conn
	session string // the session attached to its page, once there is one

	mu      sync.Mutex
	nextID  int
	waiting map[int]chan cdpReply
	closed  chan struct{}
	exited  chan struct{}
}

type cdpReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func startChromium(ctx context.Context, browser Browser, profile, address string, options Options) (*chromiumWindow, error) {
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
	// A blank page first, and the address through DevTools once connected:
	// Chromium 153 never requests an http page given on its command line.
	args = append(args, "about:blank")
	process := exec.CommandContext(ctx, browser.Path, args...) //nolint:gosec // the browser the person has, or names
	if err := process.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", browser, err)
	}
	w := &chromiumWindow{browser: browser, process: process, profile: profile, waiting: map[int]chan cdpReply{}, closed: make(chan struct{}), exited: make(chan struct{})}
	go func() {
		_ = process.Wait()
		close(w.exited)
	}()
	endpoint, err := w.devToolsEndpoint(ctx)
	if err != nil {
		w.Close()
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, endpoint, nil) //nolint:bodyclose // a websocket's response has no body to close
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("connecting to %s: %w", browser.Name, err)
	}
	w.conn = conn
	go w.read()
	if err := w.Navigate(ctx, address); err != nil {
		w.Close()
		return nil, fmt.Errorf("opening %s in %s: %w", address, browser.Name, err)
	}
	return w, nil
}

// devToolsEndpoint waits for the browser to write the port it listens on, as
// --remote-debugging-port=0 has it do, into the profile.
func (w *chromiumWindow) devToolsEndpoint(ctx context.Context) (string, error) {
	deadline := time.Now().Add(startTimeout)
	file := filepath.Join(w.profile, "DevToolsActivePort")
	for {
		if raw, err := os.ReadFile(file); err == nil { //nolint:gosec // a file in the profile mm-mcp made
			if endpoint, err := ParseActivePort(string(raw)); err == nil {
				return endpoint, nil
			}
		}
		// The program started may exit at once and leave the browser to a
		// process of its own, as Edge's launcher does; the port file still
		// comes, so only the deadline ends the wait.
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			reason := fmt.Sprintf("opened no DevTools port within %s; a company policy that forbids remote debugging does that", startTimeout)
			select {
			case <-w.exited:
				reason = "exited as it started, without opening its DevTools port"
			default:
			}
			return "", &BlockedError{Browser: w.browser, Reason: reason}
		}
	}
}

// ParseActivePort reads the DevToolsActivePort file a Chromium writes: its
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

func (w *chromiumWindow) read() {
	defer close(w.closed)
	for {
		var message struct {
			ID int `json:"id"`
			cdpReply
		}
		if err := w.conn.ReadJSON(&message); err != nil {
			return
		}
		if message.ID == 0 {
			continue // an event; none is listened for
		}
		w.mu.Lock()
		waiting := w.waiting[message.ID]
		delete(w.waiting, message.ID)
		w.mu.Unlock()
		if waiting != nil {
			waiting <- message.cdpReply
		}
	}
}

// call sends one DevTools command, to the browser or, with a session id, to
// one of its pages, and reads its result into out.
func (w *chromiumWindow) call(ctx context.Context, sessionID, method string, params, out any) error {
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	answer := make(chan cdpReply, 1)
	w.waiting[id] = answer
	command := map[string]any{"id": id, "method": method, "params": params}
	if sessionID != "" {
		command["sessionId"] = sessionID
	}
	err := w.conn.WriteJSON(command)
	w.mu.Unlock()
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
	case <-w.closed:
		return ErrBrowserClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *chromiumWindow) WaitForToken(ctx context.Context, address string) (string, error) {
	return waitForToken(ctx, w.closed, address, w.Cookies)
}

func (w *chromiumWindow) Cookies(ctx context.Context) ([]Cookie, error) {
	var got struct {
		Cookies []struct {
			Name   string `json:"name"`
			Value  string `json:"value"`
			Domain string `json:"domain"`
			Path   string `json:"path"`
		} `json:"cookies"`
	}
	if err := w.call(ctx, "", "Storage.getCookies", map[string]any{}, &got); err != nil {
		return nil, err
	}
	cookies := make([]Cookie, 0, len(got.Cookies))
	for _, c := range got.Cookies {
		cookies = append(cookies, Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path})
	}
	return cookies, nil
}

// page is a session attached to the browser's first page, which a browser
// that has just started may not have opened yet.
func (w *chromiumWindow) page(ctx context.Context) (string, error) {
	w.mu.Lock()
	session := w.session
	w.mu.Unlock()
	if session != "" {
		return session, nil
	}
	deadline := time.Now().Add(startTimeout)
	for {
		var targets struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
			} `json:"targetInfos"`
		}
		if err := w.call(ctx, "", "Target.getTargets", map[string]any{}, &targets); err != nil {
			return "", err
		}
		for _, target := range targets.TargetInfos {
			if target.Type != "page" {
				continue
			}
			var attached struct {
				SessionID string `json:"sessionId"`
			}
			if err := w.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, &attached); err != nil {
				return "", err
			}
			w.mu.Lock()
			w.session = attached.SessionID
			w.mu.Unlock()
			return attached.SessionID, nil
		}
		if time.Now().After(deadline) {
			return "", errors.New("the browser has no page open")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (w *chromiumWindow) Navigate(ctx context.Context, address string) error {
	session, err := w.page(ctx)
	if err != nil {
		return err
	}
	return w.call(ctx, session, "Page.navigate", map[string]any{"url": address}, nil)
}

func (w *chromiumWindow) Evaluate(ctx context.Context, script string) error {
	session, err := w.page(ctx)
	if err != nil {
		return err
	}
	var evaluated struct {
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := w.call(ctx, session, "Runtime.evaluate", map[string]any{"expression": script, "awaitPromise": true}, &evaluated); err != nil {
		return err
	}
	if details := evaluated.ExceptionDetails; details != nil {
		if details.Exception != nil && details.Exception.Description != "" {
			return fmt.Errorf("the script failed: %s", details.Exception.Description)
		}
		return fmt.Errorf("the script failed: %s", details.Text)
	}
	return nil
}

func (w *chromiumWindow) Close() {
	if w.conn != nil {
		closing, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = w.call(closing, "", "Browser.close", map[string]any{}, nil)
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
