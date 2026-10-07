//go:build live

package live

import (
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// Finding teams and channels by id or by name, whole or in part, and the
// tools that list and count channels (#5, ADR-030).

// namedChannel creates a public channel with a display name of the test's own.
func namedChannel(t *testing.T, admin *model.Client4, team *model.Team, display string, members ...*model.User) *model.Channel {
	t.Helper()
	channel, _, err := admin.CreateChannel(t.Context(), &model.Channel{TeamId: team.Id, Name: uniqueName("channel"), DisplayName: display, Type: model.ChannelTypeOpen})
	check(t, err)
	for _, member := range members {
		_, _, err := admin.AddChannelMember(t.Context(), channel.Id, member.Id)
		check(t, err)
	}
	return channel
}

func channelInfo(t *testing.T, session *mcp.ClientSession, arguments map[string]any) (server.Channel, *mcp.CallToolResult) {
	t.Helper()
	result := callTool(t, session, &mcp.CallToolParams{Name: "get_channel_info", Arguments: arguments})
	var channel server.Channel
	if !result.IsError {
		structured(t, result, &channel)
	}
	return channel, result
}

func TestGetChannelInfoFindsAChannelByIdOrByNameWholeOrInPart(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	tag := word(t)
	joined := namedChannel(t, admin, team, "Release "+tag, user)
	notJoined := namedChannel(t, admin, team, "Unjoined "+tag)
	// A direct message gives the lookup the other side of one to name.
	_, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)
	session := sessionFor(t, admin, user)

	for _, name := range []string{joined.Id, joined.Name, "RELEASE " + strings.ToUpper(tag), "~" + joined.Name} {
		found, result := channelInfo(t, session, map[string]any{"channel": name})
		if result.IsError || found.ID != joined.Id || found.Member == nil || !*found.Member || found.Team != team.DisplayName {
			t.Errorf("%q found %+v: %s", name, found, errorText(result))
		}
	}
	found, result := channelInfo(t, session, map[string]any{"channel": "Unjoined " + tag, "team_id": team.Id})
	if result.IsError || found.ID != notJoined.Id || found.Member == nil || *found.Member {
		t.Errorf("a public channel the user has not joined found %+v: %s", found, errorText(result))
	}
	_, ambiguous := channelInfo(t, session, map[string]any{"channel": tag})
	if !ambiguous.IsError || !strings.Contains(errorText(ambiguous), joined.Id) || !strings.Contains(errorText(ambiguous), notJoined.Id) {
		t.Errorf("a name both channels share got %s; want both listed", errorText(ambiguous))
	}
	_, unknown := channelInfo(t, session, map[string]any{"channel": "Releese " + tag})
	if !unknown.IsError || !strings.Contains(errorText(unknown), "Release "+tag) {
		t.Errorf("a slip got %s; want the closest name", errorText(unknown))
	}
}

func TestSearchChannelsFindsJoinedAndUnjoinedChannels(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	tag := word(t)
	joined := namedChannel(t, admin, team, tag+" planning", user)
	notJoined := namedChannel(t, admin, team, tag+" archive")
	_, _, err := clientAs(t, user).CreateDirectChannel(t.Context(), user.Id, other.Id)
	check(t, err)

	var found server.Channels
	structured(t, callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "search_channels", Arguments: map[string]any{"term": tag}}), &found)
	member := map[string]bool{}
	for _, channel := range found.Channels {
		member[channel.ID] = channel.Member != nil && *channel.Member
	}
	if len(found.Channels) != 2 || !member[joined.Id] || member[notJoined.Id] {
		t.Fatalf("found %+v", found.Channels)
	}
}

func TestListTeamChannelsPagesThroughTheTeamsPublicChannels(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	for range 3 {
		seedChannel(t, admin, team)
	}
	session := sessionFor(t, admin, user)

	pages := everyPage(t, session, &mcp.CallToolParams{Name: "list_team_channels", Arguments: map[string]any{"team_id": team.Id}}, 2, "channels", "id")
	// Town square and off-topic come with every team.
	if got := flat(t, pages); len(got) != 5 || len(pages) != 3 {
		t.Fatalf("paged through %v; want 5 channels on 3 pages", pages)
	}
}

func TestAnArchivedChannelIsListedAndCannotBePostedIn(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)
	_, err := admin.DeleteChannel(t.Context(), channel.Id)
	check(t, err)
	session, questions := writingSession(t, admin, user, accept)

	var listed server.Channels
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "list_archived_channels", Arguments: map[string]any{"team_id": team.Id}}), &listed)
	if len(listed.Channels) != 1 || listed.Channels[0].ID != channel.Id || !listed.Channels[0].Archived {
		t.Fatalf("listed %+v", listed.Channels)
	}
	result := callTool(t, session, &mcp.CallToolParams{Name: "create_post", Arguments: map[string]any{"channel_id": channel.Id, "message": "hello?"}})
	if !result.IsError || !strings.Contains(errorText(result), "archived") {
		t.Fatalf("got %s", errorText(result))
	}
	noQuestions(t, questions)
}

func TestGetChannelStatsCountsMembersPinsAndFiles(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user, other := seedUser(t, admin), seedUser(t, admin)
	team := seedTeam(t, admin, user, other)
	channel := seedChannel(t, admin, team, user, other)
	post := postAs(t, clientAs(t, other), channel.Id, "", "pin me")
	_, err := admin.PinPost(t.Context(), post.Id)
	check(t, err)
	attachAs(t, clientAs(t, other), channel.Id, "notes.txt", []byte("x"))

	var stats server.ChannelStats
	structured(t, callTool(t, sessionFor(t, admin, user), &mcp.CallToolParams{Name: "get_channel_stats", Arguments: map[string]any{"channel_id": channel.Id}}), &stats)
	// The administrator who made the channel is in it too.
	if stats.Members != 3 || stats.PinnedPosts != 1 || stats.Files != 1 {
		t.Fatalf("got %+v", stats)
	}
}

func TestGetTeamInfoFindsATeamByIdOrNameAndAnOpenTeamByItsAddress(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	tag := word(t)
	mine, _, err := admin.CreateTeam(t.Context(), &model.Team{Name: uniqueName("team"), DisplayName: "Platform " + tag, Type: model.TeamOpen, AllowOpenInvite: true})
	check(t, err)
	t.Cleanup(func() { _, _ = admin.SoftDeleteTeam(t.Context(), mine.Id) })
	_, _, err = admin.AddTeamMember(t.Context(), mine.Id, user.Id)
	check(t, err)
	// Open to anyone on the server, so a user outside it may find it by its address.
	outside, _, err := admin.CreateTeam(t.Context(), &model.Team{Name: uniqueName("team"), DisplayName: "Elsewhere " + tag, Type: model.TeamOpen, AllowOpenInvite: true})
	check(t, err)
	t.Cleanup(func() { _, _ = admin.SoftDeleteTeam(t.Context(), outside.Id) })
	session := sessionFor(t, admin, user)
	teamInfo := func(name string) (server.Team, *mcp.CallToolResult) {
		result := callTool(t, session, &mcp.CallToolParams{Name: "get_team_info", Arguments: map[string]any{"team": name}})
		var team server.Team
		if !result.IsError {
			structured(t, result, &team)
		}
		return team, result
	}

	for _, name := range []string{mine.Id, mine.Name, "platform " + tag, tag} {
		if team, result := teamInfo(name); result.IsError || team.ID != mine.Id || !team.Open {
			t.Errorf("%q found %+v: %s", name, team, errorText(result))
		}
	}
	if team, result := teamInfo(outside.Name); result.IsError || team.ID != outside.Id {
		t.Errorf("an open team the user is not in found %+v: %s", team, errorText(result))
	}
}

func TestGetUserTeamsAndChannelsNameTheirTeam(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	user := seedUser(t, admin)
	team := seedTeam(t, admin, user)
	channel := seedChannel(t, admin, team, user)

	listed, ok := getUserChannels(t, sessionFor(t, admin, user), map[string]any{"team_id": team.Id})[channel.Id]
	if !ok || listed.Team != team.DisplayName {
		t.Fatalf("got %+v", listed)
	}
}
