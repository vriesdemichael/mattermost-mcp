package login

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// Finding browsers, reading the ports they write, and finding Mattermost's
// cookie among a browser's: no browser is started here.

type env map[string]string

func (e env) get(name string) string { return e[name] }

// system is a machine of goos with the given programs installed, by path or by
// command, and Playwright's browsers among them under LOCALAPPDATA or HOME.
func system(goos string, installed ...string) System {
	have := map[string]bool{}
	for _, path := range installed {
		have[path] = true
	}
	return System{
		GOOS:   goos,
		Getenv: env{"ProgramFiles": `C:\Program Files`, "ProgramFiles(x86)": `C:\Program Files (x86)`, "LOCALAPPDATA": `C:\Users\me\AppData\Local`, "HOME": "/home/me"}.get,
		LookPath: func(name string) (string, error) {
			if have[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(path string) bool { return have[path] },
		Glob: func(pattern string) ([]string, error) {
			var out []string
			prefix, rest, _ := strings.Cut(pattern, "*")
			for path := range have {
				if strings.HasPrefix(path, prefix) && strings.HasSuffix(path, rest[strings.LastIndexAny(rest, `/\`)+1:]) {
					out = append(out, path)
				}
			}
			return out, nil
		},
	}
}

func names(browsers []Browser) []string {
	var out []string
	for _, browser := range browsers {
		out = append(out, browser.Name+" "+browser.Path)
	}
	return out
}

func TestEveryInstalledBrowserIsFoundInTheOrderToTryThem(t *testing.T) {
	t.Parallel()
	edge := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	chrome := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	firefox := `C:\Program Files\Mozilla Firefox\firefox.exe`
	older := `C:\Users\me\AppData\Local\ms-playwright\chromium-1208\chrome-win64\chrome.exe`
	newer := `C:\Users\me\AppData\Local\ms-playwright\chromium-1217\chrome-win64\chrome.exe`

	got, err := FindBrowsers(system("windows", firefox, older, edge, newer, chrome), "")

	want := []string{"Chrome " + chrome, "Edge " + edge, "Firefox " + firefox, "Playwright's Chromium " + newer, "Playwright's Chromium " + older}
	if err != nil || !slices.Equal(names(got), want) {
		t.Fatalf("got %q, %v; want %q", names(got), err, want)
	}
	if got[2].Engine != Firefox || got[0].Engine != Chromium {
		t.Errorf("engines: %+v", got)
	}
}

func TestBrowsersAreFoundOnMacOSAndLinuxToo(t *testing.T) {
	t.Parallel()
	mac, err := FindBrowsers(system("darwin", "/Applications/Firefox.app/Contents/MacOS/firefox"), "")
	if err != nil || len(mac) != 1 || mac[0].Engine != Firefox {
		t.Errorf("macOS: %q, %v", names(mac), err)
	}
	playwright := "/home/me/.cache/ms-playwright/chromium-1217/chrome-linux64/chrome"
	linux, err := FindBrowsers(system("linux", "chromium", playwright), "")
	if err != nil || !slices.Equal(names(linux), []string{"Chromium /usr/bin/chromium", "Playwright's Chromium " + playwright}) {
		t.Errorf("Linux: %q, %v", names(linux), err)
	}
	if _, err := FindBrowsers(system("linux"), ""); !errors.Is(err, ErrNoBrowser) {
		t.Errorf("with nothing installed: %v", err)
	}
}

func TestANamedBrowserIsTakenAloneByPathOrCommand(t *testing.T) {
	t.Parallel()
	got, err := FindBrowsers(system("linux", "brave-browser", "chromium"), "brave-browser")
	if err != nil || len(got) != 1 || got[0].Path != "/usr/bin/brave-browser" || got[0].Engine != Chromium {
		t.Errorf("by command: %q, %v", names(got), err)
	}
	got, err = FindBrowsers(system("linux", "/opt/firefox/firefox"), "/opt/firefox/firefox")
	if err != nil || len(got) != 1 || got[0].Engine != Firefox {
		t.Errorf("by path: %q, %v", names(got), err)
	}
	if _, err := FindBrowsers(system("linux", "chromium"), "netscape"); err == nil {
		t.Error("a browser that is not there was taken")
	}
}

func TestASandboxedBrowserIsRecognised(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]bool{
		"/snap/bin/chromium": true,
		"/var/lib/flatpak/exports/bin/org.mozilla.firefox": true,
		"/usr/bin/firefox": false,
		`C:\Program Files\Mozilla Firefox\firefox.exe`: false,
	} {
		if got := confined(path); got != want {
			t.Errorf("%s: got %v", path, got)
		}
	}
}

func TestTheRemotePortFilesAreRead(t *testing.T) {
	t.Parallel()
	got, err := ParseActivePort("49321\n/devtools/browser/0b1f\n")
	if err != nil || got != "ws://127.0.0.1:49321/devtools/browser/0b1f" {
		t.Errorf("DevToolsActivePort: %q, %v", got, err)
	}
	for _, content := range []string{"", "49321\n", "port\n/devtools/browser/x\n", "49321\n/elsewhere\n"} {
		if _, err := ParseActivePort(content); err == nil {
			t.Errorf("%q was read", content)
		}
	}
	got, err = ParseBiDiServer([]byte(`{"ws_host": "127.0.0.1", "ws_port": 50431}`))
	if err != nil || got != "ws://127.0.0.1:50431" {
		t.Errorf("WebDriverBiDiServer.json: %q, %v", got, err)
	}
	if _, err := ParseBiDiServer([]byte(`{"ws_host": "127.0.0.1"}`)); err == nil {
		t.Error("a file without a port was read")
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
