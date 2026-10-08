//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// Every tool's id arguments take what a person gives as readily as an id: a
// channel or a team by its name, with ~ or # before it or not, and a post by
// its address (ADR-030).

func TestEveryToolTakesAChannelOrTeamByItsNameAndAPostByItsAddress(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, other), channel.Id, "", "a post to find by name")
	// A direct message, so a channel's name is matched against one as well.
	_, _, err := clientAs(t, other).CreateDirectChannel(t.Context(), other.Id, user.Id)
	check(t, err)
	session, _ := writingSession(t, admin, user, accept)
	byName, bySign, byDisplay := channel.Name, "~"+channel.Name, "#"+channel.DisplayName

	for _, call := range []*mcp.CallToolParams{
		{Name: "read_channel", Arguments: map[string]any{"channel_id": byName}},
		{Name: "read_unread", Arguments: map[string]any{"channel_id": bySign}},
		{Name: "list_pinned_posts", Arguments: map[string]any{"channel_id": byDisplay}},
		{Name: "get_channel_stats", Arguments: map[string]any{"channel_id": byName}},
		{Name: "search_users", Arguments: map[string]any{"term": other.Username, "channel_id": bySign}},
		{Name: "list_team_channels", Arguments: map[string]any{"team_id": team.DisplayName}},
		{Name: "list_archived_channels", Arguments: map[string]any{"team_id": team.Name}},
		{Name: "typing", Arguments: map[string]any{"channel_id": byName}},
		{Name: "typing", Arguments: map[string]any{"channel_id": byName, "stop": true}},
		{Name: "save_draft", Arguments: map[string]any{"channel_id": bySign, "message": "a draft found by name"}},
		{Name: "delete_draft", Arguments: map[string]any{"channel_id": byDisplay}},
		{Name: "create_post", Arguments: map[string]any{"channel_id": byDisplay, "message": "posted by the channel's name"}},
		{Name: "read_post", Arguments: map[string]any{"post_id": liveURL + "/_redirect/pl/" + post.Id}},
		{Name: "read_post", Arguments: map[string]any{"post_id": liveURL + "/" + team.Name + "/pl/" + post.Id}},
	} {
		if result := callTool(t, session, call); result.IsError {
			t.Errorf("%s %v: %s", call.Name, call.Arguments, errorText(result))
		}
	}

	var read server.ChannelPosts
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": "#" + channel.DisplayName}}), &read)
	if read.ChannelID != channel.Id || len(read.Posts) == 0 || read.Posts[len(read.Posts)-1].Message != "posted by the channel's name" {
		t.Errorf("read %s by its display name: %+v", channel.Name, read)
	}

	// A name nobody's channel has is refused with the closest, not
	// Mattermost's "invalid channel_id".
	result := callTool(t, session, &mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": channel.Name + "x"}})
	if !result.IsError || !strings.Contains(errorText(result), "closest") || strings.Contains(errorText(result), "Invalid or missing") {
		t.Errorf("an unknown channel name: %s", errorText(result))
	}
}
