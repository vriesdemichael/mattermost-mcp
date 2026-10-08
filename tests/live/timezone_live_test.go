//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// The times the tools answer with are the person's own, in the timezone they
// set in Mattermost, as they read the times they give.
func TestTimesComeBackInThePersonsTimezone(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	postAs(t, clientAs(t, user), channel.Id, "", "the release is at four")
	_, _, err := admin.PatchUser(t.Context(), user.Id, &model.UserPatch{Timezone: model.StringMap{
		"useAutomaticTimezone": "false", "manualTimezone": "Asia/Tokyo",
	}})
	check(t, err)
	session, _ := writingSession(t, admin, user, accept)

	var read server.ChannelPosts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": channel.Id}}), &read)
	if len(read.Posts) == 0 || !strings.HasSuffix(read.Posts[0].CreatedAt, "+09:00") {
		t.Errorf("read_channel's post was written at %+v; want a time in Tokyo", read.Posts)
	}

	var channels server.Channels
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_user_channels", Arguments: map[string]any{"team_id": team.Id}}), &channels)
	found := false
	for _, listed := range channels.Channels {
		if listed.ID == channel.Id {
			found = true
			if !strings.HasSuffix(listed.LastPostAt, "+09:00") {
				t.Errorf("get_user_channels says the last post was at %q; want a time in Tokyo", listed.LastPostAt)
			}
		}
	}
	if !found {
		t.Errorf("get_user_channels did not list the channel: %+v", channels.Channels)
	}

	var typing server.Typing
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "typing", Arguments: map[string]any{"channel_id": channel.Id}}), &typing)
	if !strings.HasSuffix(typing.Until, "+09:00") {
		t.Errorf("typing shows until %q; want a time in Tokyo", typing.Until)
	}
}
