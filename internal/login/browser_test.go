//go:build browser

package login

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The browsers on this machine, driven as mm-mcp drives them: started with a
// profile of their own, a page opened and another navigated to, a script run,
// cookies read, HttpOnly ones included, and closed. `task test:browser` runs
// this on every system in CI, with Chrome, Edge, Firefox and Playwright's
// Chromium (ADR-009). The pages are this test's own, on 127.0.0.1, and set a
// cookie of their own; nothing here is Mattermost (ADR-005).

// installed is every browser FindBrowsers finds, failing the test unless the
// machine has each kind mm-mcp logs in with.
func installed(t *testing.T) []Browser {
	t.Helper()
	found, err := FindBrowsers(ThisSystem(), "")
	if err != nil {
		t.Fatalf("no browser on this machine: %v", err)
	}
	var chromium, firefox, playwright bool
	for _, browser := range found {
		switch {
		case browser.Name == "Playwright's Chromium":
			playwright = true
		case browser.Engine == Firefox:
			firefox = true
		default:
			chromium = true
		}
	}
	var missing []string
	if !chromium {
		missing = append(missing, "Chrome, Edge or Chromium")
	}
	if !firefox {
		missing = append(missing, "Firefox")
	}
	if !playwright {
		missing = append(missing, "Playwright's Chromium (npx playwright install chromium)")
	}
	if len(missing) > 0 {
		t.Fatalf("the browser tests need %s on this machine; found %v", strings.Join(missing, ", "), found)
	}
	return found
}

// asked is the paths the pages were asked for, in order, so a test that fails
// says whether the browser asked at all.
type asked struct {
	mu    sync.Mutex
	paths []string
}

func (a *asked) add(path string) { a.mu.Lock(); a.paths = append(a.paths, path); a.mu.Unlock() }

func (a *asked) String() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.paths) == 0 {
		return "nothing"
	}
	return strings.Join(a.paths, ", ")
}

// pages sets an HttpOnly cookie on each page, named after it, with a value
// drawn at random, and records what it was asked for.
func pages(t *testing.T) (*httptest.Server, map[string]string, *asked) {
	t.Helper()
	values := map[string]string{}
	for _, name := range []string{"first", "second"} {
		raw := make([]byte, 8)
		_, _ = rand.Read(raw)
		values[name] = hex.EncodeToString(raw)
	}
	requests := &asked{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.add(r.URL.Path)
		name := strings.Trim(r.URL.Path, "/")
		if name == "" {
			name = "first"
		}
		if name == "endless" {
			// Headers, then a page that never ends, until the test does.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!doctype html><title>endless</title><p>"))
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		if name == "nothing" {
			// A browser shows no page for 204, and stays where it was.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if value, ok := values[name]; ok {
			http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>" + name + "</title><p>" + name))
	}))
	t.Cleanup(server.Close)
	return server, values, requests
}

// eventuallyHas waits for the window to hold the cookie name with value.
func eventuallyHas(t *testing.T, window Window, requests *asked, name, value string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var last []Cookie
	for time.Now().Before(deadline) {
		cookies, err := window.Cookies(t.Context())
		if err != nil {
			t.Fatalf("reading the cookies: %v", err)
		}
		for _, cookie := range cookies {
			if cookie.Name == name && cookie.Value == value {
				return
			}
		}
		last = cookies
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("no cookie %s=%s within 20s; the browser holds %+v, the pages were asked for %s, and its pages are %s",
		name, value, last, requests, openPages(window))
}

func TestEveryBrowserIsDrivenAsALoginDrivesIt(t *testing.T) {
	server, values, requests := pages(t)
	for _, browser := range installed(t) {
		t.Run(browser.Name+" "+browser.Path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			window, err := Start(ctx, browser, server.URL+"/", Options{Headless: true, StartTimeout: time.Minute})
			if err != nil {
				t.Fatalf("starting it: %v", err)
			}
			closed := false
			defer func() {
				if !closed {
					window.Close()
				}
			}()

			eventuallyHas(t, window, requests, "first", values["first"])
			if err := window.Navigate(ctx, server.URL+"/second"); err != nil {
				t.Fatalf("navigating: %v", err)
			}
			eventuallyHas(t, window, requests, "second", values["second"])
			if err := window.Evaluate(ctx, `new Promise(done => setTimeout(done, 10))`); err != nil {
				t.Errorf("a script that resolves: %v", err)
			}
			if err := window.Evaluate(ctx, `Promise.reject(new Error("refused on purpose"))`); err == nil {
				t.Error("a script that rejects was taken as done")
			}

			profile := profileOf(window)
			window.Close()
			closed = true
			if _, err := window.Cookies(ctx); !errors.Is(err, ErrBrowserClosed) {
				t.Errorf("after closing it: %v", err)
			}
			// A process of the browser's left running keeps files of the
			// profile open, which on Windows keeps it from being removed.
			if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("the profile %s is still there after closing the browser: %v", profile, err)
			}
		})
	}
}

// profileOf is the profile mm-mcp made for a window.
func profileOf(window Window) string {
	switch w := window.(type) {
	case *chromiumWindow:
		return w.profile
	case *firefoxWindow:
		return w.profile
	}
	return ""
}

// openPages is every page a Chromium has open, with where each is, or what
// could not be read of a browser that is not one.
func openPages(window Window) string {
	chromium, ok := window.(*chromiumWindow)
	if !ok {
		return "not listed for this browser"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var targets struct {
		TargetInfos []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := chromium.call(ctx, "", "Target.getTargets", map[string]any{}, &targets); err != nil {
		return "unreadable: " + err.Error()
	}
	var listed []string
	for _, target := range targets.TargetInfos {
		listed = append(listed, target.Type+" "+target.URL)
	}
	return "[" + strings.Join(listed, "; ") + "]"
}

// aChromium is the first Chromium on this machine, for what every Chromium
// does alike.
func aChromium(t *testing.T) Browser {
	t.Helper()
	for _, browser := range installed(t) {
		if browser.Engine == Chromium {
			return browser
		}
	}
	t.Fatal("no Chromium on this machine")
	return Browser{}
}

// A window that stays blank is not a window that opened: the address is sent
// again, and then the window fails, rather than leave a person a blank page.
func TestAWindowThatStaysBlankIsNotOpened(t *testing.T) {
	server, _, requests := pages(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	window, err := Start(ctx, aChromium(t), server.URL+"/nothing", Options{Headless: true, StartTimeout: time.Minute})
	if err == nil {
		window.Close()
		t.Fatal("a window that stayed blank was opened")
	}
	if !errors.Is(err, errStayedBlank) || strings.Count(requests.String(), "/nothing") != navigateAttempts {
		t.Errorf("got %v; the page was asked for %s", err, requests)
	}
}

// A page the browser replaces after mm-mcp attached to it is found again: the
// address reaches the page the browser has now.
func TestANavigationReachesAPageThatReplacedTheFirst(t *testing.T) {
	server, values, requests := pages(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	window, err := Start(ctx, aChromium(t), server.URL+"/", Options{Headless: true, StartTimeout: time.Minute})
	if err != nil {
		t.Fatalf("starting it: %v", err)
	}
	defer window.Close()
	chromium := window.(*chromiumWindow)
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"targetInfos"`
	}
	if err := chromium.call(ctx, "", "Target.getTargets", map[string]any{}, &targets); err != nil {
		t.Fatal(err)
	}
	// A new page first: closing a browser's last page can close the browser.
	if err := chromium.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets.TargetInfos {
		if target.Type == "page" {
			if err := chromium.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": target.TargetID}, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := window.Navigate(ctx, server.URL+"/second"); err != nil {
		t.Fatalf("navigating after the page was replaced: %v", err)
	}
	eventuallyHas(t, window, requests, "second", values["second"])
}

// aFirefox is the first Firefox on this machine.
func aFirefox(t *testing.T) Browser {
	t.Helper()
	for _, browser := range installed(t) {
		if browser.Engine == Firefox {
			return browser
		}
	}
	t.Fatal("no Firefox on this machine")
	return Browser{}
}

// A page that never finishes loading fails a navigation within its own time,
// saying where, rather than at the caller's deadline with nothing said.
func TestFirefoxGivesUpOnAPageThatNeverLoads(t *testing.T) {
	server, _, _ := pages(t)
	defer func(was time.Duration) { firefoxLoadsWithin = was }(firefoxLoadsWithin)
	firefoxLoadsWithin = 3 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	window, err := Start(ctx, aFirefox(t), server.URL+"/", Options{Headless: true, StartTimeout: time.Minute})
	if err != nil {
		t.Fatalf("starting it: %v", err)
	}
	defer window.Close()
	began := time.Now()
	err = window.Navigate(ctx, server.URL+"/endless")
	if err == nil || !strings.Contains(err.Error(), "/endless") || time.Since(began) > 30*time.Second {
		t.Errorf("after %s: %v", time.Since(began).Round(time.Second), err)
	}
}
