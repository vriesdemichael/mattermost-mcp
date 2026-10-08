package credstore

import (
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
