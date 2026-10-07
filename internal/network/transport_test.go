package network_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// answers records that it was reached. Its subject is the request itself --
// which host it went to -- and it claims nothing about Mattermost (ADR-005).
type answers struct{ reached bool }

func (a *answers) RoundTrip(*http.Request) (*http.Response, error) {
	a.reached = true
	return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
}

func get(t *testing.T, transport http.RoundTripper, url string) error {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err == nil {
		_ = response.Body.Close()
	}
	return err
}

func TestTheBlockRefusesAHostBeyondTheMachineAndNamesIt(t *testing.T) {
	t.Parallel()
	inner := &answers{}
	err := get(t, network.Wrap(inner, true), "https://chat.example.com/api/v4/system/ping")

	var blocked *network.ExternalNetworkBlockedError
	if !errors.As(err, &blocked) || blocked.Host != "chat.example.com" {
		t.Fatalf("got %v, want the block naming chat.example.com", err)
	}
	if inner.reached {
		t.Fatal("the request went through")
	}
}

func TestTheBlockLetsLoopbackThrough(t *testing.T) {
	t.Parallel()
	for _, url := range []string{"http://localhost:8065/", "http://127.0.0.1:8065/", "http://[::1]:8065/"} {
		inner := &answers{}
		if err := get(t, network.Wrap(inner, true), url); err != nil || !inner.reached {
			t.Errorf("%s: got %v, reached %v", url, err, inner.reached)
		}
	}
}

func TestWithoutTheBlockAnyHostIsPassedOn(t *testing.T) {
	t.Parallel()
	inner := &answers{}
	if err := get(t, network.Wrap(inner, false), "https://chat.example.com/"); err != nil || !inner.reached {
		t.Fatalf("got %v, reached %v", err, inner.reached)
	}
}

func TestTheSealTurnsTheBlockOnForTheTransportEveryClientUses(t *testing.T) {
	t.Parallel()
	var blocked *network.ExternalNetworkBlockedError
	if err := get(t, network.NewSafeTransport(), "https://chat.example.com/"); !errors.As(err, &blocked) {
		t.Fatalf("got %v, want the network block", err)
	}
}
