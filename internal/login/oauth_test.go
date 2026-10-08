package login

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// The OAuth login where no request reaches Mattermost: the callback page, the
// PKCE pair, and what is refused before anything is sent.

func callback(t *testing.T, query string) (int, callbackResult, bool) {
	t.Helper()
	results := make(chan callbackResult, 1)
	recorder := httptest.NewRecorder()
	callbackHandler("the-state", results).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/callback?"+query, nil))
	select {
	case got := <-results:
		return recorder.Code, got, true
	default:
		return recorder.Code, callbackResult{}, false
	}
}

func TestTheCallbackTakesTheCodeOfThisLoginOnly(t *testing.T) {
	t.Parallel()
	if code, got, ended := callback(t, "state=the-state&code=abc"); code != http.StatusOK || !ended || got.code != "abc" || got.err != nil {
		t.Errorf("the answer: %d %+v %v", code, got, ended)
	}
	if code, _, ended := callback(t, "state=another&code=abc"); code != http.StatusBadRequest || ended {
		t.Errorf("another login's answer: %d, ended %v", code, ended)
	}
	if _, got, ended := callback(t, "state=the-state&error=access_denied&error_description=no"); !ended || got.err == nil || !strings.Contains(got.err.Error(), "access_denied") {
		t.Errorf("a refusal: %+v %v", got, ended)
	}
	if _, got, ended := callback(t, "state=the-state"); !ended || got.err == nil {
		t.Errorf("no code: %+v %v", got, ended)
	}
	recorder := httptest.NewRecorder()
	callbackHandler("the-state", make(chan callbackResult, 1)).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/elsewhere", nil))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("another path: %d", recorder.Code)
	}
}

func TestThePKCEChallengeIsTheVerifiersDigest(t *testing.T) {
	t.Parallel()
	verifier, challenge := pkce()
	sum := sha256.Sum256([]byte(verifier))
	if challenge != base64.RawURLEncoding.EncodeToString(sum[:]) || len(verifier) < 43 {
		t.Errorf("verifier %q, challenge %q", verifier, challenge)
	}
	if other, _ := pkce(); other == verifier {
		t.Error("two logins drew the same verifier")
	}
}

func TestOAuthRefusesWhatItCannotUseBeforeSendingAnything(t *testing.T) {
	t.Parallel()
	never := func(string) error { t.Error("a browser was opened"); return nil }
	base := OAuth{HTTP: &http.Client{Transport: network.NewSafeTransport()}, Address: "https://chat.example.com", Open: never}

	elsewhere := base
	elsewhere.Client = Client{ID: "app", Callback: "http://192.0.2.1:8766/callback"}
	if _, err := elsewhere.Login(t.Context()); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("a callback beyond the machine: %v", err)
	}
	if _, err := base.Login(t.Context()); !errors.Is(err, ErrNoOAuthClient) {
		t.Errorf("no client and no registering: %v", err)
	}
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	busy := base
	busy.Client = Client{ID: "app", Callback: "http://" + taken.Addr().String() + "/callback"}
	if _, err := busy.Login(t.Context()); err == nil || !strings.Contains(err.Error(), "another program") {
		t.Errorf("a callback port in use: %v", err)
	}
	// Registering reaches the server, which the unit tests do not.
	registering := base
	registering.Register = true
	if _, err := registering.Login(t.Context()); err == nil || !strings.Contains(err.Error(), "registering") {
		t.Errorf("registering with no server: %v", err)
	}
}

func TestDiscoveringAServerThatCannotBeReachedSaysSo(t *testing.T) {
	t.Parallel()
	if _, err := Discover(t.Context(), &http.Client{Transport: network.NewSafeTransport()}, "https://chat.example.com"); err == nil {
		t.Error("a server nobody reached was described")
	}
}
