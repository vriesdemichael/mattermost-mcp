//go:build live

package live

import (
	"slices"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vriesdemichael/mm-mcp/internal/teststack"
)

// `task stack:up` bootstraps on every run, so bootstrapping an instance that
// already has its administrator must succeed and keep that administrator
// (ADR-007). It does not leave the record untouched: Mattermost updates a
// user's update time when they log in, which is how Bootstrap checks for one.
func TestBootstrappingABootstrappedInstanceKeepsItsAdministrator(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	before, _, err := admin.GetUserByUsername(t.Context(), teststack.AdminUsername, "")
	check(t, err)

	check(t, teststack.Bootstrap(t.Context(), liveURL))

	after, _, err := admin.GetUserByUsername(t.Context(), teststack.AdminUsername, "")
	check(t, err)
	if after.Id != before.Id || after.CreateAt != before.CreateAt {
		t.Fatalf("bootstrapping again replaced the administrator: %s became %s", before.Id, after.Id)
	}
	if !slices.Contains(strings.Fields(after.Roles), model.SystemAdminRoleId) {
		t.Fatalf("the administrator lost its role: %q", after.Roles)
	}
}
