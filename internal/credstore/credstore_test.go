package credstore

import (
	"errors"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/vriesdemichael/mm-mcp/internal/testsupport"
)

func TestMain(m *testing.M) {
	// The system's credential store is the person's; the tests keep their
	// tokens in memory.
	keyring.MockInit()
	testsupport.SealedMain(m)
}

func TestATokenIsStoredPerServerAndForgotten(t *testing.T) {
	if err := Store("https://Chat.Example.com/", "token-1"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://chat.example.com", "https://CHAT.example.com/"} {
		if token, ok, err := Load(address); err != nil || !ok || token != "token-1" {
			t.Errorf("%s: got %q, %v, %v", address, token, ok, err)
		}
	}
	if _, ok, err := Load("https://other.example.com"); ok || err != nil {
		t.Errorf("another server has a token: %v, %v", ok, err)
	}
	if err := Delete("https://chat.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := Load("https://chat.example.com"); ok {
		t.Error("the token is still there")
	}
	if err := Delete("https://chat.example.com"); err != nil {
		t.Errorf("forgetting what is not there: %v", err)
	}
}

func TestTheKeyKeepsAPathAsItIs(t *testing.T) {
	t.Parallel()
	if got := key("HTTPS://Chat.Example.com/Mattermost/"); got != "https://chat.example.com/Mattermost" {
		t.Errorf("got %q", got)
	}
}

func TestTheStoreIsNamedAsEachSystemCallsIt(t *testing.T) {
	t.Parallel()
	for goos, want := range map[string]string{"windows": "Credential Manager", "darwin": "keychain", "linux": "Secret Service"} {
		if got := where(goos); !strings.Contains(got, want) {
			t.Errorf("%s: %q", goos, got)
		}
	}
	if Where() == "" {
		t.Error("this system's store has no name")
	}
}

func TestHowALoginWasMadeIsKeptPerServerAndForgotten(t *testing.T) {
	if err := StoreOrigin("https://Chat.Example.com/", "paste"); err != nil {
		t.Fatal(err)
	}
	if origin, err := LoadOrigin("https://chat.example.com"); err != nil || origin != "paste" {
		t.Errorf("got %q, %v", origin, err)
	}
	if origin, err := LoadOrigin("https://other.example.com"); err != nil || origin != "" {
		t.Errorf("another server: got %q, %v", origin, err)
	}
	if err := StoreOrigin("https://chat.example.com", ""); err != nil {
		t.Fatal(err)
	}
	if origin, err := LoadOrigin("https://chat.example.com"); err != nil || origin != "" {
		t.Errorf("after forgetting: got %q, %v", origin, err)
	}
	if err := StoreOrigin("https://chat.example.com", ""); err != nil {
		t.Errorf("forgetting what is not there: %v", err)
	}
}

func TestAStoreThatFailsIsSaidToHaveFailed(t *testing.T) {
	keyring.MockInitWithError(errors.New("the store is locked"))
	t.Cleanup(keyring.MockInit)
	if err := StoreOrigin("https://chat.example.com", "paste"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("storing: %v", err)
	}
	if err := StoreOrigin("https://chat.example.com", ""); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("forgetting: %v", err)
	}
	if _, err := LoadOrigin("https://chat.example.com"); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("reading: %v", err)
	}
}
