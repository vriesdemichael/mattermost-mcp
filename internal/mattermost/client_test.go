package mattermost_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// No server is involved in these tests: which status Mattermost answers with,
// and when, is the live suite's to prove (ADR-005).

func TestAClientThatCannotReachMattermostSaysSoAndKeepsTheCause(t *testing.T) {
	t.Parallel()
	client := mattermost.New("https://chat.example.com", "token", network.NewSafeTransport())

	_, err := client.Me(t.Context())

	var blocked *network.ExternalNetworkBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("got %v, want the network block as the cause", err)
	}
	var answered *mattermost.Error
	if errors.As(err, &answered) {
		t.Fatalf("a failure to reach Mattermost read as an answer from it: %v", err)
	}
	if !strings.Contains(err.Error(), "could not reach Mattermost") {
		t.Fatalf("got %q", err)
	}
}

func TestAnErrorAnswerNamesTheStatusAndTheErrorID(t *testing.T) {
	t.Parallel()
	err := &mattermost.Error{Status: 401, ID: "api.context.session_expired.app_error", Message: "Invalid or expired session."}
	if got := err.Error(); got != "Mattermost answered 401 (api.context.session_expired.app_error): Invalid or expired session. "+mattermost.RefusedCredential {
		t.Fatalf("got %q", got)
	}
	if got := (&mattermost.Error{Status: 403, ID: "api.context.permissions.app_error", Message: "No permission."}).Error(); strings.Contains(got, "MM_TOKEN") {
		t.Fatalf("a 403 blamed the credential: %q", got)
	}
	if got := (&mattermost.Error{Status: 502, Message: "Bad Gateway"}).Error(); !strings.Contains(got, "no error id") {
		t.Fatalf("got %q", got)
	}
}

func TestAReleaseOlderThanTheOldestSupportedIsNamedSo(t *testing.T) {
	t.Parallel()
	for version, older := range map[string]bool{
		"11.6.3":                          true,
		"10.11.2.20260101.abc":            true,
		mattermost.OldestSupported + ".0": false,
		"11.11.1.18011.abc":               false,
		"12.0.0":                          false,
		"":                                false,
		"dev":                             false,
	} {
		if got := mattermost.OlderThanSupported(version); got != older {
			t.Errorf("%q: got %v", version, got)
		}
	}
}
