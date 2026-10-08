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

// pages sets an HttpOnly cookie on each page, named after it, with a value
// drawn at random.
func pages(t *testing.T) (*httptest.Server, map[string]string) {
	t.Helper()
	values := map[string]string{}
	for _, name := range []string{"first", "second"} {
		raw := make([]byte, 8)
		_, _ = rand.Read(raw)
		values[name] = hex.EncodeToString(raw)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.Trim(r.URL.Path, "/")
		if name == "" {
			name = "first"
		}
		if value, ok := values[name]; ok {
			http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>" + name + "</title><p>" + name))
	}))
	t.Cleanup(server.Close)
	return server, values
}

// eventuallyHas waits for the window to hold the cookie name with value.
func eventuallyHas(t *testing.T, window Window, name, value string) {
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
	t.Fatalf("no cookie %s=%s within 20s; the browser holds %+v", name, value, last)
}

func TestEveryBrowserIsDrivenAsALoginDrivesIt(t *testing.T) {
	server, values := pages(t)
	for _, browser := range installed(t) {
		t.Run(browser.Name+" "+browser.Path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			window, err := Start(ctx, browser, server.URL+"/", Options{Headless: true})
			if err != nil {
				t.Fatalf("starting it: %v", err)
			}
			closed := false
			defer func() {
				if !closed {
					window.Close()
				}
			}()

			eventuallyHas(t, window, "first", values["first"])
			if err := window.Navigate(ctx, server.URL+"/second"); err != nil {
				t.Fatalf("navigating: %v", err)
			}
			eventuallyHas(t, window, "second", values["second"])
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
