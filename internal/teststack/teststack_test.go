package teststack_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vriesdemichael/mm-mcp/internal/teststack"
)

func TestTheMainCheckoutKeepsTheFixedPortsAndPlainNames(t *testing.T) {
	t.Parallel()
	main := teststack.Checkout{Root: filepath.FromSlash("/src/mm-mcp")}
	for stack, port := range teststack.MainPorts {
		instance, err := teststack.Resolve(main, stack, "")
		if err != nil {
			t.Fatal(err)
		}
		if instance.Project != "mm-mcp-"+stack || instance.HostPort != port || instance.StateFile != filepath.Join(".tmp", "stack-"+stack+".env") {
			t.Errorf("%s: got %+v", stack, instance)
		}
	}
}

func TestALinkedWorktreeGetsItsOwnProjectAndAnAssignedPort(t *testing.T) {
	t.Parallel()
	one, err := teststack.Resolve(teststack.Checkout{Root: filepath.FromSlash("/src/wt/Feature_X"), Linked: true}, "latest", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := teststack.Resolve(teststack.Checkout{Root: filepath.FromSlash("/elsewhere/Feature_X"), Linked: true}, "latest", "")
	if err != nil {
		t.Fatal(err)
	}
	if one.HostPort != 0 || !strings.HasPrefix(one.Project, "mm-mcp-feature-x-") || !strings.HasSuffix(one.Project, "-latest") {
		t.Fatalf("got %+v", one)
	}
	if one.Project == other.Project {
		t.Fatalf("two worktrees of the same name share the project %s", one.Project)
	}
}

func TestAReleaseRunsBesideThePinnedStacksOnAnAssignedPort(t *testing.T) {
	t.Parallel()
	instance, err := teststack.Resolve(teststack.Checkout{Root: filepath.FromSlash("/src/mm-mcp")}, "latest", "11.9.4")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Project != "mm-mcp-release-11-9-4" || instance.HostPort != 0 || instance.StateFile != filepath.Join(".tmp", "stack-release-11.9.4.env") {
		t.Fatalf("got %+v", instance)
	}
	if override := teststack.ReleaseOverride("11.9.4"); !strings.Contains(override, "mattermost/mattermost-team-edition:11.9.4") {
		t.Fatalf("got %q", override)
	}
}

func TestAnUnknownStackOrAMalformedReleaseIsRefused(t *testing.T) {
	t.Parallel()
	main := teststack.Checkout{Root: "/src"}
	if _, err := teststack.Resolve(main, "beta", ""); err == nil {
		t.Error("an unknown stack was accepted")
	}
	for _, release := range []string{"11.9", "v11.9.4", "11.9.4-rc1", "latest"} {
		if _, err := teststack.Resolve(main, "latest", release); err == nil {
			t.Errorf("%q was accepted as a release", release)
		}
	}
}

func TestTheStateFileRecordsTheAddressItIsReadBackFrom(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".tmp", "stack-latest.env")
	if err := teststack.WriteState(path, "http://localhost:32769"); err != nil {
		t.Fatal(err)
	}
	if got, err := teststack.ReadState(path); err != nil || got != "http://localhost:32769" {
		t.Fatalf("got %q, %v", got, err)
	}
	empty := filepath.Join(t.TempDir(), "empty.env")
	if err := os.WriteFile(empty, []byte("# nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := teststack.ReadState(empty); err == nil {
		t.Fatal("a state file without an address was read as one")
	}
}
