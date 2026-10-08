// Package login obtains a Mattermost session the way a person gets one: by
// logging in, in a browser, with whatever single sign-on and second factor
// their organisation asks for (ADR-019).
//
// Where the server offers OAuth, the person's own browser is used, whichever
// it is (oauth.go). Otherwise mm-mcp starts a browser of the machine's in a
// window of its own, with a profile that is thrown away afterwards, opens the
// server's login page, and watches the browser's cookies until Mattermost sets
// its session cookie, MMAUTHTOKEN: a Chromium, through the DevTools protocol,
// or a Firefox, through WebDriver BiDi. The person logs in; mm-mcp drives none
// of the login page itself.
package login

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ErrNoBrowser is the answer when no browser mm-mcp can watch is installed
// where it looks.
var ErrNoBrowser = errors.New("no Chrome, Edge, Chromium or Firefox is installed where mm-mcp looks for one")

// Engine is the kind of browser, which decides how mm-mcp talks to it.
type Engine int

// The engines mm-mcp can watch a login in.
const (
	Chromium Engine = iota
	Firefox
)

// Browser is one browser on the machine.
type Browser struct {
	Name   string
	Path   string
	Engine Engine
}

func (b Browser) String() string { return b.Name + " (" + b.Path + ")" }

// System is what finding a browser reads of the machine, so a test can give
// it another.
type System struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Exists   func(string) bool
	Glob     func(string) ([]string, error)
}

// ThisSystem is the machine the process runs on.
func ThisSystem() System {
	return System{
		GOOS:     runtime.GOOS,
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Exists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir()
		},
		Glob: filepath.Glob,
	}
}

// FindBrowsers is the browsers to log in with, in the order to try them: the
// one named, by path or by command, alone; or else every Chrome, Edge,
// Firefox and Chromium installed where its installer puts it, and then the
// Chromium Playwright downloaded, which a company's browser policies do not
// reach.
func FindBrowsers(system System, named string) ([]Browser, error) {
	if named = strings.TrimSpace(named); named != "" {
		path := ""
		if isPath(named) && system.Exists(named) {
			path = named
		} else if found, err := system.LookPath(named); err == nil {
			path = found
		} else {
			return nil, fmt.Errorf("the browser %s is not a program mm-mcp can find", named)
		}
		engine := Chromium
		if strings.Contains(strings.ToLower(filepath.Base(strings.ReplaceAll(path, `\`, "/"))), "firefox") {
			engine = Firefox
		}
		return []Browser{{Name: named, Path: path, Engine: engine}}, nil
	}
	var found []Browser
	seen := map[string]bool{}
	for _, candidate := range candidates(system) {
		path := ""
		if isPath(candidate.Path) {
			if system.Exists(candidate.Path) {
				path = candidate.Path
			}
		} else if resolved, err := system.LookPath(candidate.Path); err == nil {
			path = resolved
		}
		if path != "" && !seen[path] {
			seen[path] = true
			candidate.Path = path
			found = append(found, candidate)
		}
	}
	found = append(found, playwrightChromium(system)...)
	if len(found) == 0 {
		return nil, ErrNoBrowser
	}
	return found, nil
}

// candidates are where the browsers' installers put them: Chrome, Edge,
// Firefox, then any other Chromium.
func candidates(system System) []Browser {
	switch system.GOOS {
	case "windows":
		var chrome, edge, firefox []Browser
		for _, root := range []string{system.Getenv("ProgramFiles"), system.Getenv("ProgramFiles(x86)"), system.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			chrome = append(chrome, Browser{"Chrome", root + `\Google\Chrome\Application\chrome.exe`, Chromium})
			edge = append(edge, Browser{"Edge", root + `\Microsoft\Edge\Application\msedge.exe`, Chromium})
			firefox = append(firefox, Browser{"Firefox", root + `\Mozilla Firefox\firefox.exe`, Firefox})
		}
		return slices.Concat(chrome, edge, firefox)
	case "darwin":
		var out []Browser
		for _, app := range []Browser{
			{"Chrome", "Google Chrome.app/Contents/MacOS/Google Chrome", Chromium},
			{"Edge", "Microsoft Edge.app/Contents/MacOS/Microsoft Edge", Chromium},
			{"Firefox", "Firefox.app/Contents/MacOS/firefox", Firefox},
			{"Chromium", "Chromium.app/Contents/MacOS/Chromium", Chromium},
		} {
			out = append(out, Browser{app.Name, "/Applications/" + app.Path, app.Engine})
			if home := system.Getenv("HOME"); home != "" {
				out = append(out, Browser{app.Name, home + "/Applications/" + app.Path, app.Engine})
			}
		}
		return out
	default:
		return []Browser{
			{"Chrome", "google-chrome", Chromium},
			{"Chrome", "google-chrome-stable", Chromium},
			{"Edge", "microsoft-edge", Chromium},
			{"Edge", "microsoft-edge-stable", Chromium},
			{"Firefox", "firefox", Firefox},
			{"Firefox", "firefox-esr", Firefox},
			{"Chromium", "chromium", Chromium},
			{"Chromium", "chromium-browser", Chromium},
		}
	}
}

// playwrightChromium is the Chromium Playwright downloaded with
// `npx playwright install chromium`, newest first. It is a browser of the
// person's own, in their user folder, which no company policy for Chrome or
// Edge applies to.
func playwrightChromium(system System) []Browser {
	root := system.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if root == "" || root == "0" {
		switch system.GOOS {
		case "windows":
			if local := system.Getenv("LOCALAPPDATA"); local != "" {
				root = local + `\ms-playwright`
			}
		case "darwin":
			if home := system.Getenv("HOME"); home != "" {
				root = home + "/Library/Caches/ms-playwright"
			}
		default:
			if cache := system.Getenv("XDG_CACHE_HOME"); cache != "" {
				root = cache + "/ms-playwright"
			} else if home := system.Getenv("HOME"); home != "" {
				root = home + "/.cache/ms-playwright"
			}
		}
	}
	if root == "" || system.Glob == nil {
		return nil
	}
	sep := "/"
	if system.GOOS == "windows" {
		sep = `\`
	}
	var patterns []string
	switch system.GOOS {
	case "windows":
		patterns = []string{`chromium-*\chrome-win64\chrome.exe`, `chromium-*\chrome-win\chrome.exe`}
	case "darwin":
		patterns = []string{
			"chromium-*/chrome-mac*/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
			"chromium-*/chrome-mac*/Chromium.app/Contents/MacOS/Chromium",
		}
	default:
		patterns = []string{"chromium-*/chrome-linux64/chrome", "chromium-*/chrome-linux/chrome"}
	}
	var paths []string
	for _, pattern := range patterns {
		matches, _ := system.Glob(root + sep + pattern)
		for _, match := range matches {
			if !slices.Contains(paths, match) {
				paths = append(paths, match)
			}
		}
	}
	// The newest revision first: chromium-1217 before chromium-1208.
	slices.SortStableFunc(paths, func(a, b string) int { return revision(b) - revision(a) })
	out := make([]Browser, 0, len(paths))
	for _, path := range paths {
		out = append(out, Browser{Name: "Playwright's Chromium", Path: path, Engine: Chromium})
	}
	return out
}

// isPath reports whether a browser is named by a path rather than a command
// to look for on the PATH. The paths are written for the system they are on,
// which a test may not be running on.
func isPath(name string) bool { return strings.ContainsAny(name, `/\`) }

// revision is the number in a Playwright browser's folder, chromium-1217, or
// 0 when there is none.
func revision(path string) int {
	at := strings.Index(path, "chromium-")
	if at < 0 {
		return 0
	}
	n := 0
	for _, c := range path[at+len("chromium-"):] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
