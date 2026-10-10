//go:build live

package live

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/server"
)

// Links out and in (ADR-030): every post, channel and team an answer describes
// carries the link it opens at, made from names, and every id argument takes
// each kind of link Mattermost shows a person.

// places is a team, a channel in it, a direct and a group message, with a post
// in each, seen by user.
type places struct {
	user, other, third *model.User
	team               *model.Team
	channel            *model.Channel
	direct, group      *model.Channel
	posts              map[string]*model.Post // by channel id
}

func seedPlaces(t *testing.T, admin *model.Client4, message string) places {
	t.Helper()
	p := places{user: seedUser(t, admin), other: seedUser(t, admin), third: seedUser(t, admin), posts: map[string]*model.Post{}}
	p.team = seedTeam(t, admin, p.user, p.other, p.third)
	p.channel = seedChannel(t, admin, p.team, p.user, p.other)
	author := clientAs(t, p.other)
	var err error
	p.direct, _, err = author.CreateDirectChannel(t.Context(), p.other.Id, p.user.Id)
	check(t, err)
	p.group, _, err = author.CreateGroupChannel(t.Context(), []string{p.user.Id, p.other.Id, p.third.Id})
	check(t, err)
	for _, channel := range []*model.Channel{p.channel, p.direct, p.group} {
		p.posts[channel.Id] = postAs(t, author, channel.Id, "", message)
	}
	return p
}

// links is the link each place opens at: a direct or group message's in the
// user's one team, which is their first.
func (p places) links() map[string]string {
	team := liveURL + "/" + p.team.Name
	return map[string]string{
		p.channel.Id: team + "/channels/" + p.channel.Name,
		p.direct.Id:  team + "/messages/@" + p.other.Username,
		p.group.Id:   team + "/messages/" + p.group.Name,
	}
}

func TestAnswersLinkEveryPlaceByItsNames(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	term := word(t)
	p := seedPlaces(t, admin, "a post about "+term)
	session := sessionFor(t, admin, p.user)
	teamLink := liveURL + "/" + p.team.Name
	links := p.links()

	var teams server.Teams
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_user_teams"}), &teams)
	if len(teams.Teams) != 1 || teams.Teams[0].URL != teamLink {
		t.Errorf("get_user_teams links %+v; want %s", teams.Teams, teamLink)
	}
	var team server.Team
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_team_info", Arguments: map[string]any{"team": p.team.Id}}), &team)
	if team.URL != teamLink {
		t.Errorf("get_team_info links %q", team.URL)
	}

	listed := getUserChannels(t, session, nil)
	for id, want := range links {
		channel := listed[id]
		if channel.URL != want {
			t.Errorf("get_user_channels links %s as %q; want %q", channel.Name, channel.URL, want)
		}
		// No link but a post's has an id in it.
		for _, internal := range []string{id, p.user.Id, p.other.Id, p.third.Id} {
			if strings.Contains(channel.URL, internal) {
				t.Errorf("%s is linked with the id %s: %s", channel.Name, internal, channel.URL)
			}
		}

		read := readChannel(t, session, map[string]any{"channel_id": id})
		post := read.Posts[len(read.Posts)-1]
		if read.ChannelURL != want || post.ID != p.posts[id].Id || post.URL != teamLink+"/pl/"+post.ID || post.ChannelURL != "" {
			t.Errorf("read_channel %s links the channel %q and its post %q, %q", id, read.ChannelURL, post.URL, post.ChannelURL)
		}

		var thread server.PostWithThread
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": post.ID}}), &thread)
		if thread.Post == nil || thread.Post.URL != post.URL || thread.Post.ChannelURL != want {
			t.Errorf("read_post links the post as %+v", thread.Post)
		}

		// A link given out is a link taken in: it reads the channel it opens.
		if back := readChannel(t, session, map[string]any{"channel_id": want}); back.ChannelID != id {
			t.Errorf("%s read %s", want, back.ChannelID)
		}
	}

	// Posts from several channels each name their channel's link.
	eventually(t, 30*time.Second, func() (bool, string) {
		var found server.SearchResults
		structured(t, callTool(t, session, &mcp.CallToolParams{Name: "search_posts", Arguments: map[string]any{"terms": term}}), &found)
		if len(found.Posts) != len(links) {
			return false, fmt.Sprint(len(found.Posts), " posts")
		}
		for _, post := range found.Posts {
			if want := links[post.ChannelID]; post.ChannelURL != want || post.URL != teamLink+"/pl/"+post.ID {
				t.Errorf("search_posts links %s's channel %q, want %q, and the post %q", post.ID, post.ChannelURL, want, post.URL)
			}
		}
		return true, ""
	})
}

func TestEveryToolTakesEachKindOfLink(t *testing.T) {
	t.Parallel()
	admin := admin(t)
	p := seedPlaces(t, admin, "a post to find by its link")
	joinable := seedChannel(t, admin, p.team, p.other)
	archived := seedChannel(t, admin, p.team, p.user, p.other)
	_, err := admin.DeleteChannel(t.Context(), archived.Id)
	check(t, err)
	reply := postAs(t, clientAs(t, p.other), p.channel.Id, p.posts[p.channel.Id].Id, "a reply to find by its link")
	_, fileID := attachAs(t, clientAs(t, p.other), p.channel.Id, "found.txt", []byte("found by its link\n"))
	session, _ := writingSession(t, admin, p.user, accept)
	teamLink := liveURL + "/" + p.team.Name
	channelLink, joinableLink := teamLink+"/channels/"+p.channel.Name, teamLink+"/channels/"+joinable.Name
	post := p.posts[p.channel.Id]

	// Every tool that takes a channel, by its link.
	for _, call := range []*mcp.CallToolParams{
		{Name: "read_channel", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "read_unread", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "list_pinned_posts", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "get_channel_stats", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "get_channel_info", Arguments: map[string]any{"channel": channelLink}},
		{Name: "search_users", Arguments: map[string]any{"term": p.other.Username, "channel_id": channelLink}},
		{Name: "search_posts", Arguments: map[string]any{"terms": "link", "in": channelLink}},
		{Name: "search_files", Arguments: map[string]any{"terms": "found", "in": channelLink}},
		{Name: "list_mentions", Arguments: map[string]any{"in": channelLink}},
		{Name: "mark_channel_read", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "typing", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "typing", Arguments: map[string]any{"channel_id": channelLink, "stop": true}},
		{Name: "save_draft", Arguments: map[string]any{"channel_id": channelLink, "message": "a draft found by its link"}},
		{Name: "delete_draft", Arguments: map[string]any{"channel_id": channelLink}},
		{Name: "add_channel_members", Arguments: map[string]any{"channel_id": channelLink, "usernames": []string{p.third.Username}}},
		{Name: "join_channel", Arguments: map[string]any{"channel_id": joinableLink}},
		{Name: "leave_channel", Arguments: map[string]any{"channel_id": joinableLink}},
		{Name: "create_post", Arguments: map[string]any{"channel_id": channelLink, "message": "posted by the channel's link"}},
		// A team by its link, or by any link into it.
		{Name: "list_team_channels", Arguments: map[string]any{"team_id": teamLink}},
		{Name: "list_archived_channels", Arguments: map[string]any{"team_id": channelLink}},
		{Name: "get_team_info", Arguments: map[string]any{"team": teamLink + "/pl/" + post.Id}},
		// A post by each of its links.
		{Name: "read_post", Arguments: map[string]any{"post_id": teamLink + "/pl/" + post.Id}},
		{Name: "read_post", Arguments: map[string]any{"post_id": liveURL + "/_redirect/pl/" + post.Id}},
		{Name: "read_post", Arguments: map[string]any{"post_id": teamLink + "/threads/" + post.Id}},
		{Name: "read_post", Arguments: map[string]any{"post_id": channelLink + "/" + reply.Id}},
		{Name: "create_post", Arguments: map[string]any{"channel_id": channelLink, "root_id": teamLink + "/pl/" + post.Id, "message": "replied by the post's link"}},
		// A file by its link.
		{Name: "read_file", Arguments: map[string]any{"file_id": liveURL + "/api/v4/files/" + fileID + "/preview"}},
	} {
		if result := callTool(t, session, call); result.IsError {
			t.Errorf("%s %v: %s", call.Name, call.Arguments, errorText(result))
		}
	}

	// Each kind of link to a channel names it.
	for given, want := range map[string]string{
		teamLink + "/messages/@" + p.other.Username: p.direct.Id,
		teamLink + "/messages/" + p.other.Id:        p.direct.Id,
		teamLink + "/channels/" + p.direct.Name:     p.direct.Id,
		teamLink + "/messages/" + p.group.Name:      p.group.Id,
		teamLink + "/channels/" + p.channel.Id:      p.channel.Id,
		channelLink + "/" + post.Id:                 p.channel.Id,
		strings.ToUpper(liveURL[:8]) + liveURL[8:] + "/" + p.team.Name + "/channels/" + p.channel.Name: p.channel.Id,
	} {
		if read := readChannel(t, session, map[string]any{"channel_id": given}); read.ChannelID != want {
			t.Errorf("%s read %s; want %s", given, read.ChannelID, want)
		}
	}
	var found server.Channel
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_channel_info", Arguments: map[string]any{"channel": teamLink + "/channels/" + archived.Name}}), &found)
	if found.ID != archived.Id || !found.Archived {
		t.Errorf("an archived channel's link finds %+v", found)
	}
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_channel_info", Arguments: map[string]any{"channel": joinableLink}}), &found)
	if found.ID != joinable.Id || found.Member == nil || *found.Member {
		t.Errorf("the link of a channel the user left finds %+v", found)
	}
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_channel_info", Arguments: map[string]any{"channel": teamLink + "/channels/" + p.channel.Id}}), &found)
	if found.ID != p.channel.Id || found.URL != channelLink {
		t.Errorf("a link with the channel's id finds %+v", found)
	}
	// An open team the user is not in is found by its link, as by its name.
	outside, _, err := admin.CreateTeam(t.Context(), &model.Team{Name: uniqueName("team"), DisplayName: "Outside", Type: model.TeamOpen, AllowOpenInvite: true})
	check(t, err)
	t.Cleanup(func() { _, _ = admin.SoftDeleteTeam(context.Background(), outside.Id) })
	var team server.Team
	structured(t, callTool(t, session, &mcp.CallToolParams{Name: "get_team_info", Arguments: map[string]any{"team": liveURL + "/" + outside.Name}}), &team)
	if team.ID != outside.Id || team.URL != liveURL+"/"+outside.Name {
		t.Errorf("an open team's link finds %+v", team)
	}

	// A link to another server, of another kind, or to nothing is refused,
	// and says why.
	stranger := seedUser(t, admin)
	for _, refused := range []struct {
		call *mcp.CallToolParams
		says string
	}{
		{&mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": "https://chat.elsewhere.example/" + p.team.Name + "/channels/" + p.channel.Name}}, "is a link to chat.elsewhere.example"},
		{&mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": teamLink + "/pl/" + post.Id}}, "is a post's link, not a channel's"},
		{&mcp.CallToolParams{Name: "read_post", Arguments: map[string]any{"post_id": channelLink}}, "is a channel's link, not a post's"},
		{&mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": teamLink + "/channels/" + p.channel.Name + "-gone"}}, "no channel is at"},
		{&mcp.CallToolParams{Name: "read_channel", Arguments: map[string]any{"channel_id": teamLink + "/messages/@" + stranger.Username}}, "no direct message with @" + stranger.Username},
		{&mcp.CallToolParams{Name: "list_team_channels", Arguments: map[string]any{"team_id": liveURL + "/" + p.team.Name + "x"}}, "which the user is not in"},
		{&mcp.CallToolParams{Name: "read_file", Arguments: map[string]any{"file_id": teamLink + "/pl/" + post.Id}}, "is a post's link, not a file's"},
		{&mcp.CallToolParams{Name: "get_team_info", Arguments: map[string]any{"team": liveURL + "/" + p.team.Name + "-gone"}}, "no team is at"},
		{&mcp.CallToolParams{Name: "get_team_info", Arguments: map[string]any{"team": liveURL + "/_redirect/pl/" + post.Id}}, "is a post's link, not a team's"},
	} {
		result := callTool(t, session, refused.call)
		if !result.IsError || !strings.Contains(errorText(result), refused.says) {
			t.Errorf("%s %v: %s; want it refused saying %q", refused.call.Name, refused.call.Arguments, errorText(result), refused.says)
		}
	}
}
