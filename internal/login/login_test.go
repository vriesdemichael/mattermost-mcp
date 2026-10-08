package login

import (
	"errors"
	"testing"
)

// Finding a browser, reading the port it writes, and finding Mattermost's
// cookie among the browser's: no browser is started here.

func system(goos string, installed ...string) System {
	have := map[string]bool{}
	for _, path := range installed {
		have[path] = true
	}
	return System{
		GOOS:   goos,
		Getenv: env{"ProgramFiles": `C:\Program Files`, "ProgramFiles(x86)": `C:\Program Files (x86)`, "HOME": "/Users/me"}.get,
		LookPath: func(name string) (string, error) {
			if have[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(path string) bool { return have[path] },
	}
}

type env map[string]string

func (e env) get(name string) string { return e[name] }

func TestTheFirstInstalledBrowserIsFoundChromeFirst(t *testing.T) {
	t.Parallel()
	edge := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	chrome := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	for _, c := range []struct {
		system System
		want   string
	}{
		{system("windows", edge), edge},
		{system("windows", edge, chrome), chrome},
		{system("darwin", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"), "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
		{system("linux", "chromium"), "/usr/bin/chromium"},
	} {
		if got, err := FindBrowser(c.system, ""); err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.system.GOOS, got, err, c.want)
		}
	}
	if _, err := FindBrowser(system("linux"), ""); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("with nothing installed: %v", err)
	}
}

func TestANamedBrowserIsTakenByPathOrCommand(t *testing.T) {
	t.Parallel()
	if got, err := FindBrowser(system("linux", "brave-browser"), "brave-browser"); err != nil || got != "/usr/bin/brave-browser" {
		t.Errorf("by command: got %q, %v", got, err)
	}
	if got, err := FindBrowser(system("linux", "/opt/brave/brave"), "/opt/brave/brave"); err != nil || got != "/opt/brave/brave" {
		t.Errorf("by path: got %q, %v", got, err)
	}
	if _, err := FindBrowser(system("linux", "chromium"), "netscape"); err == nil {
		t.Error("a browser that is not there was taken")
	}
}

func TestTheDevToolsPortFileIsRead(t *testing.T) {
	t.Parallel()
	got, err := ParseActivePort("49321\n/devtools/browser/0b1f\n")
	if err != nil || got != "ws://127.0.0.1:49321/devtools/browser/0b1f" {
		t.Errorf("got %q, %v", got, err)
	}
	for _, content := range []string{"", "49321\n", "port\n/devtools/browser/x\n", "49321\n/elsewhere\n"} {
		if _, err := ParseActivePort(content); err == nil {
			t.Errorf("%q was read", content)
		}
	}
}

func TestTheSessionCookieIsTheServersOwn(t *testing.T) {
	t.Parallel()
	cookies := []Cookie{
		{Name: "MMAUTHTOKEN", Value: "elsewhere", Domain: "other.example.com", Path: "/"},
		{Name: "MMUSERID", Value: "user", Domain: "chat.example.com", Path: "/"},
		{Name: "MMAUTHTOKEN", Value: "the-token", Domain: "chat.example.com", Path: "/"},
	}
	if got, ok := SessionToken(cookies, "https://chat.example.com"); !ok || got != "the-token" {
		t.Errorf("got %q, %v", got, ok)
	}
	if got, ok := SessionToken([]Cookie{{Name: "MMAUTHTOKEN", Value: "sub", Domain: ".example.com", Path: "/"}}, "https://chat.example.com/mattermost"); !ok || got != "sub" {
		t.Errorf("a parent domain's cookie: got %q, %v", got, ok)
	}
	if _, ok := SessionToken(cookies[:2], "https://chat.example.com"); ok {
		t.Error("a token was found where Mattermost set none")
	}
}
