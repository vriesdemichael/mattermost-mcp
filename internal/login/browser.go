// Package login obtains a Mattermost session the way a person gets one: by
// logging in, in a browser, with whatever single sign-on and second factor
// their organisation asks for (ADR-019).
//
// It starts a Chrome or Edge already installed on the machine, with a profile
// of its own that is thrown away afterwards, opens the server's login page,
// and watches the browser's cookies through the DevTools protocol until
// Mattermost sets its session cookie, MMAUTHTOKEN. mm-mcp downloads no browser
// and drives none of the login page itself: the person logs in.
package login

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoBrowser is the answer when no Chrome or Edge is installed where they
// usually are.
var ErrNoBrowser = errors.New("no Chrome or Edge is installed where mm-mcp looks for one")

// System is what finding a browser reads of the machine, so a test can give
// it another.
type System struct {
	GOOS     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Exists   func(string) bool
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
	}
}

// FindBrowser is the browser to log in with: the one named, by path or by
// command, or else the first Chrome, Edge or Chromium installed where its
// installer puts it.
func FindBrowser(system System, named string) (string, error) {
	if named = strings.TrimSpace(named); named != "" {
		if isPath(named) && system.Exists(named) {
			return named, nil
		}
		if path, err := system.LookPath(named); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("the browser %s is not a program mm-mcp can find", named)
	}
	for _, candidate := range candidates(system) {
		if isPath(candidate) {
			if system.Exists(candidate) {
				return candidate, nil
			}
			continue
		}
		if path, err := system.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", ErrNoBrowser
}

// candidates are where the browsers' installers put them, Chrome first.
func candidates(system System) []string {
	switch system.GOOS {
	case "windows":
		var out []string
		for _, root := range []string{system.Getenv("ProgramFiles"), system.Getenv("ProgramFiles(x86)"), system.Getenv("LOCALAPPDATA")} {
			if root == "" {
				continue
			}
			out = append(out,
				root+`\Google\Chrome\Application\chrome.exe`,
				root+`\Microsoft\Edge\Application\msedge.exe`,
			)
		}
		return out
	case "darwin":
		var out []string
		for _, app := range []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Chromium.app/Contents/MacOS/Chromium",
		} {
			out = append(out, "/Applications/"+app)
			if home := system.Getenv("HOME"); home != "" {
				out = append(out, home+"/Applications/"+app)
			}
		}
		return out
	default:
		return []string{"google-chrome", "google-chrome-stable", "microsoft-edge", "microsoft-edge-stable", "chromium", "chromium-browser"}
	}
}

// isPath reports whether a browser is named by a path rather than a command
// to look for on the PATH. The paths are written for the system they are on,
// which a test may not be running on.
func isPath(name string) bool { return strings.ContainsAny(name, `/\`) }
