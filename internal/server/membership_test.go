package server

import (
	"strings"
	"testing"
)

// A channel's address, made from its name as Mattermost's own app makes it.
func TestAChannelsAddressIsMadeFromItsName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{
		"Release Planning":      "release-planning",
		"  Q3 / Roadmap!  ":     "q3-roadmap",
		"Café ☕ talk":           "caf-talk",
		strings.Repeat("a", 80): strings.Repeat("a", 64),
	} {
		if got := channelAddress(name); got != want {
			t.Errorf("%q: got %q, want %q", name, got, want)
		}
	}
	// Nothing to make an address of: a random one, as Mattermost's app does.
	if got := channelAddress("☕☕"); len(got) != 26 {
		t.Errorf("a name without letters got %q", got)
	}
}
