package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Teams and channels: the ones the user belongs to, and finding others by name
// (ADR-030).

// Team is a team as a tool returns it.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name" jsonschema:"the name in the team's address"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
	Open        bool   `json:"open" jsonschema:"whether anyone on the server may join it, rather than by invitation only"`
}

func toTeam(team *model.Team) Team {
	return Team{ID: team.Id, Name: team.Name, DisplayName: team.DisplayName, Description: team.Description, Open: team.Type == model.TeamOpen && team.AllowOpenInvite}
}

// Teams wraps a list of teams, because a tool's answer is an object.
type Teams struct {
	Teams []Team `json:"teams"`
}

type noInput struct{}

func getUserTeamsSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "get_user_teams",
			Description: "List the Mattermost teams the user belongs to. A channel belongs to a team, so start here to find where a conversation is.",
			Annotations: readOnly("List the user's teams"),
		},
		[]Use{{
			Operation: "GetTeamsForUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "the tool lists the teams of the user the credential belongs to")},
		}},
		func(clientFor ClientFor) mcp.ToolHandlerFor[noInput, Teams] {
			return func(ctx context.Context, request *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, Teams, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Teams{}, err
				}
				teams, err := client.Teams(ctx)
				if err != nil {
					return nil, Teams{}, err
				}
				out := Teams{Teams: []Team{}}
				for _, team := range teams {
					out.Teams = append(out.Teams, toTeam(team))
				}
				slices.SortFunc(out.Teams, func(a, b Team) int { return strings.Compare(a.DisplayName, b.DisplayName) })
				return nil, out, nil
			}
		},
	)
}

type getTeamInfoInput struct {
	Team string `json:"team" jsonschema:"the team's id, or its name: its display name or the name in its address, whole or in part, in any case"`
}

func getTeamInfoSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "get_team_info",
			Description: "Find a team by its id or its name. A name matches the user's teams whole or in part, in any case; an open team " +
				"the user is not in is found by the exact name in its address. An ambiguous name is refused with every team it could mean.",
			Annotations: readOnly("Get team"),
		},
		[]Use{
			{Operation: "GetTeam", Params: map[string]Coverage{"team_id": SetBy("team")}},
			{
				Operation: "GetTeamByName",
				Params:    map[string]Coverage{"team_name": SetBy("team")},
				Releases:  "11.7 serves GET /api/v4/teams/name/{team_name}, as its router shows, though its specification leaves it out; nothing differs in use",
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a name is matched against the user's teams first")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[getTeamInfoInput, Team] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input getTeamInfoInput) (*mcp.CallToolResult, Team, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Team{}, err
				}
				team, err := findTeam(ctx, client, input.Team)
				if err != nil {
					return nil, Team{}, err
				}
				return nil, toTeam(team), nil
			}
		},
	)
}

// findTeam is the team an id or a name means.
func findTeam(ctx context.Context, client *mattermost.Client, name string) (*model.Team, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("give the team's id or name")
	}
	if mattermostID.MatchString(name) {
		team, err := client.Team(ctx, name)
		if err == nil {
			return team, nil
		}
		if !notFound(err) {
			return nil, err
		}
	}
	teams, err := client.Teams(ctx)
	if err != nil {
		return nil, err
	}
	var candidates []named[*model.Team]
	for _, team := range teams {
		candidates = append(candidates, named[*model.Team]{value: team, names: []string{team.DisplayName, team.Name}, label: fmt.Sprintf("%s (%s, id %s)", team.DisplayName, team.Name, team.Id)})
	}
	team, matchErr := match("team", name, candidates, "get_user_teams lists the user's teams.")
	if matchErr == nil {
		return team, nil
	}
	// A team the user is not in is found only by its exact address name, as
	// Mattermost finds it.
	if outside, err := client.TeamByName(ctx, strings.ToLower(name)); err == nil {
		return outside, nil
	}
	return nil, matchErr
}

// notFound reports whether err is Mattermost answering that nothing has that
// id, after which a string shaped like an id may still be a name.
func notFound(err error) bool {
	var answered *mattermost.Error
	return errors.As(err, &answered) && answered.Status == http.StatusNotFound
}

// Channel is a channel as a tool returns it, with what the user has not read
// in it when it is one of theirs.
type Channel struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id,omitempty" jsonschema:"the team the channel is in; empty for a direct or group message"`
	Team        string `json:"team,omitempty" jsonschema:"the team's display name"`
	Name        string `json:"name" jsonschema:"the name in the channel's address"`
	DisplayName string `json:"display_name" jsonschema:"for a direct message, the other person's username"`
	Type        string `json:"type" jsonschema:"public, private, direct or group"`
	Purpose     string `json:"purpose,omitempty"`
	Header      string `json:"header,omitempty"`
	LastPostAt  string `json:"last_post_at,omitempty"`
	Archived    bool   `json:"archived,omitempty" jsonschema:"an archived channel can be read but not posted in"`
	// Member says whether the user belongs to the channel; nil when the tool
	// did not look.
	Member *bool `json:"member,omitempty" jsonschema:"whether the user belongs to the channel"`
	// Unread and Mentions are what the user has not read; nil when Mattermost
	// gave no membership to count from.
	Unread   *int64 `json:"unread_messages,omitempty" jsonschema:"messages posted since the user last read the channel, replies included"`
	Mentions *int64 `json:"mentions,omitempty" jsonschema:"unread messages that mention the user"`
}

// Channels wraps a list of channels.
type Channels struct {
	Channels []Channel `json:"channels"`
	More     bool      `json:"more,omitempty" jsonschema:"more channels follow: ask for the next page"`
}

var channelTypes = map[model.ChannelType]string{
	model.ChannelTypeOpen:    "public",
	model.ChannelTypePrivate: "private",
	model.ChannelTypeDirect:  "direct",
	model.ChannelTypeGroup:   "group",
}

type getUserChannelsInput struct {
	TeamID     string `json:"team_id,omitempty" jsonschema:"only this team's channels, with direct and group messages, which belong to no team"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only channels with something unread"`
}

func getUserChannelsSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "get_user_channels",
			Description: "List the channels the user belongs to, direct and group messages included, most recently active first, with how many " +
				"messages and mentions are unread in each. Use it to find a channel to read, or with unread_only to see what needs attention.",
			Annotations: readOnly("List the user's channels"),
		},
		[]Use{
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the user's own id tells which side of a direct message is the other person")},
			},
			{
				Operation: "GetChannelsForUser",
				Params: map[string]Coverage{
					"user_id":         Fixed("me", "the tool lists the channels of the user the credential belongs to"),
					"last_delete_at":  Fixed("0", "archived channels are not listed: a person asking for their channels means the ones they can talk in; list_archived_channels lists the others"),
					"include_deleted": Omitted("archived channels are not listed, for the same reason"),
				},
			},
			{
				Operation: "GetChannelMembersForUser",
				Params: map[string]Coverage{
					"user_id": Fixed("me", "the unread counts are the user's own"),
					"team_id": Fixed("each team a listed channel is in", "Mattermost gives memberships, which hold the unread counts, one team at a time"),
				},
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "each channel names its team")},
			},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads the other person of each direct message, whenever they changed")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[getUserChannelsInput, Channels] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input getUserChannelsInput) (*mcp.CallToolResult, Channels, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Channels{}, err
				}
				mine, err := readMyChannels(ctx, client)
				if err != nil {
					return nil, Channels{}, err
				}
				memberships := map[string]model.ChannelMember{}
				for teamID := range mine.teams {
					members, err := client.ChannelMemberships(ctx, teamID)
					if err != nil {
						return nil, Channels{}, err
					}
					for _, member := range members {
						memberships[member.ChannelId] = member
					}
				}
				out := Channels{Channels: []Channel{}}
				for _, channel := range mine.channels {
					if input.TeamID != "" && channel.TeamId != "" && channel.TeamId != input.TeamID {
						continue
					}
					listed := mine.describe(channel)
					if member, ok := memberships[channel.Id]; ok {
						unread := max(channel.TotalMsgCount-member.MsgCount, 0)
						mentions := member.MentionCount
						listed.Unread, listed.Mentions = &unread, &mentions
					}
					if input.UnreadOnly && (listed.Unread == nil || *listed.Unread == 0) && (listed.Mentions == nil || *listed.Mentions == 0) {
						continue
					}
					out.Channels = append(out.Channels, listed)
				}
				slices.SortFunc(out.Channels, func(a, b Channel) int { return strings.Compare(b.LastPostAt, a.LastPostAt) })
				return nil, out, nil
			}
		},
	), map[string]string{
		"team_id":     "keeps the channels of that team, and the direct and group messages, which belong to none",
		"unread_only": "keeps the channels with an unread message or mention",
	})
}

// myChannels is the channels the user belongs to, with what names them: their
// teams, and the other side of each direct message.
type myChannels struct {
	self     *model.User
	channels []*model.Channel
	teams    map[string]*model.Team
	people   map[string]string
}

// readMyChannels reads the user's channels and what names them.
// GetUser, GetChannelsForUser, GetTeamsForUser, GetUsersByIds.
func readMyChannels(ctx context.Context, client *mattermost.Client) (myChannels, error) {
	self, err := client.Me(ctx)
	if err != nil {
		return myChannels{}, err
	}
	channels, err := client.Channels(ctx)
	if err != nil {
		return myChannels{}, err
	}
	teams, err := client.Teams(ctx)
	if err != nil {
		return myChannels{}, err
	}
	mine := myChannels{self: self, channels: channels, teams: map[string]*model.Team{}}
	for _, team := range teams {
		mine.teams[team.Id] = team
	}
	var others []string
	for _, channel := range channels {
		if channel.Type == model.ChannelTypeDirect {
			others = append(others, otherInDirect(channel, self.Id))
		}
	}
	if mine.people, err = usernames(ctx, client, others); err != nil {
		return myChannels{}, err
	}
	return mine, nil
}

// describe is one channel as a tool returns it, named as the person knows it.
func (m myChannels) describe(channel *model.Channel) Channel {
	out := toChannel(channel, m.self.Id, m.people)
	if team := m.teams[channel.TeamId]; team != nil {
		out.Team = team.DisplayName
	}
	return out
}

// otherInDirect is the other person in a direct message, and the user
// themselves in a direct message to themselves, which Mattermost names with
// their id on both sides and its own helper answers with nothing.
func otherInDirect(channel *model.Channel, self string) string {
	if channel.Type == model.ChannelTypeDirect && channel.Name == model.GetDMNameFromIds(self, self) {
		return self
	}
	return channel.GetOtherUserIdForDM(self)
}

func toChannel(channel *model.Channel, self string, names map[string]string) Channel {
	display := channel.DisplayName
	if channel.Type == model.ChannelTypeDirect {
		// A direct message to oneself names the user on both sides.
		switch other := otherInDirect(channel, self); {
		case other == self:
			display = "yourself"
		case names[other] != "":
			display = names[other]
		}
	}
	return Channel{
		ID:          channel.Id,
		TeamID:      channel.TeamId,
		Name:        channel.Name,
		DisplayName: display,
		Type:        channelTypes[channel.Type],
		Purpose:     channel.Purpose,
		Header:      channel.Header,
		LastPostAt:  timestamp(channel.LastPostAt),
		Archived:    channel.DeleteAt > 0,
	}
}

// channelLookupUses are the operations findChannel calls, for a channel given
// by the argument arg.
func channelLookupUses(arg string) []Use {
	return []Use{
		{Operation: "GetChannel", Params: map[string]Coverage{"channel_id": SetBy(arg)}},
		{
			Operation: "SearchChannels",
			Params: map[string]Coverage{
				"team_id":   SetBy("team_id"),
				"body.term": SetBy(arg),
			},
		},
		{
			Operation: "GetUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "the user's own id tells which side of a direct message is the other person")},
		},
		{
			Operation: "GetChannelsForUser",
			Params: map[string]Coverage{
				"user_id":         Fixed("me", "a name is matched against the user's own channels, private ones and direct messages included"),
				"last_delete_at":  Fixed("0", "archived channels are found by id, or listed by list_archived_channels"),
				"include_deleted": Omitted("archived channels are found by id, or listed by list_archived_channels"),
			},
		},
		{
			Operation: "GetTeamsForUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "public channels are searched in each of the user's teams, and each channel names its team")},
		},
		{
			Operation: "GetUsersByIds",
			Params:    map[string]Coverage{"since": Omitted("the tool reads the other person of each direct message, whenever they changed")},
		},
	}
}

// channelCandidates is the channels a name may mean: the user's own, and the
// public channels of their teams, or of one, that match the term. Mattermost's
// search matches the start of each word of a name, so a part from the middle
// of a word finds only the user's own channels.
func channelCandidates(ctx context.Context, client *mattermost.Client, mine myChannels, term, teamID string) ([]named[Channel], error) {
	seen := map[string]bool{}
	var out []named[Channel]
	add := func(channel *model.Channel, member bool) {
		if seen[channel.Id] || (teamID != "" && channel.TeamId != "" && channel.TeamId != teamID) {
			return
		}
		seen[channel.Id] = true
		described := mine.describe(channel)
		described.Member = &member
		where := described.Team
		if where == "" {
			where = described.Type + " message"
		}
		out = append(out, named[Channel]{
			value: described,
			names: []string{described.DisplayName, channel.Name},
			label: fmt.Sprintf("%s (%s, in %s, id %s)", described.DisplayName, channel.Name, where, channel.Id),
		})
	}
	for _, channel := range mine.channels {
		add(channel, true)
	}
	teams := []string{teamID}
	if teamID == "" {
		teams = teams[:0]
		for id := range mine.teams {
			teams = append(teams, id)
		}
		slices.Sort(teams)
	}
	for _, team := range teams {
		found, err := client.SearchChannels(ctx, team, term)
		if err != nil {
			return nil, err
		}
		for _, channel := range found {
			add(channel, false)
		}
	}
	return out, nil
}

// findChannel is the channel an id or a name means.
func findChannel(ctx context.Context, client *mattermost.Client, name, teamID string) (Channel, error) {
	name = strings.TrimPrefix(strings.TrimSpace(name), "~")
	if name == "" {
		return Channel{}, fmt.Errorf("give the channel's id or name")
	}
	mine, err := readMyChannels(ctx, client)
	if err != nil {
		return Channel{}, err
	}
	if mattermostID.MatchString(name) {
		channel, err := client.Channel(ctx, name)
		if err == nil {
			described := mine.describe(channel)
			member := slices.ContainsFunc(mine.channels, func(c *model.Channel) bool { return c.Id == channel.Id })
			described.Member = &member
			return described, nil
		}
		if !notFound(err) {
			return Channel{}, err
		}
	}
	candidates, err := channelCandidates(ctx, client, mine, name, teamID)
	if err != nil {
		return Channel{}, err
	}
	return match("channel", name, candidates, "search_channels finds channels by part of their name, and get_user_channels lists the user's own.")
}

type getChannelInfoInput struct {
	Channel string `json:"channel" jsonschema:"the channel's id, or its name: its display name or the name in its address, whole or in part, in any case"`
	TeamID  string `json:"team_id,omitempty" jsonschema:"look for the name in this team only; in every team of the user's when not given"`
}

func getChannelInfoSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "get_channel_info",
			Description: "Find a channel by its id or its name, among the user's own channels, private ones and direct messages included, " +
				"and the public channels of their teams. A name matches whole or in part, in any case. An ambiguous name is refused with " +
				"every channel it could mean, an unknown one with the closest names. Says whether the user belongs to the channel and whether it is archived.",
			Annotations: readOnly("Get channel"),
		},
		channelLookupUses("channel"),
		func(clientFor ClientFor) mcp.ToolHandlerFor[getChannelInfoInput, Channel] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input getChannelInfoInput) (*mcp.CallToolResult, Channel, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Channel{}, err
				}
				channel, err := findChannel(ctx, client, input.Channel, input.TeamID)
				return nil, channel, err
			}
		},
	)
}

// The most channels one search or listing returns, and how many when not told.
const (
	maxChannels     = 200
	defaultChannels = 50
)

type searchChannelsInput struct {
	Term   string `json:"term" jsonschema:"part of the channel's name"`
	TeamID string `json:"team_id,omitempty" jsonschema:"search this team only; every team of the user's when not given"`
}

func searchChannelsSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "search_channels",
			Description: "Find channels by part of their name: the user's own, private ones and direct messages included, and the public " +
				"channels of their teams they have not joined. Each says whether the user belongs to it.",
			Annotations: readOnly("Search channels"),
		},
		uses([]Use{{
			Operation: "SearchChannels",
			Params: map[string]Coverage{
				"team_id":   SetBy("team_id"),
				"body.term": SetBy("term"),
			},
		}}, channelLookupUses("term")[2:]),
		func(clientFor ClientFor) mcp.ToolHandlerFor[searchChannelsInput, Channels] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input searchChannelsInput) (*mcp.CallToolResult, Channels, error) {
				term := strings.TrimPrefix(strings.TrimSpace(input.Term), "~")
				if term == "" {
					return nil, Channels{}, fmt.Errorf("give part of the channel's name")
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Channels{}, err
				}
				mine, err := readMyChannels(ctx, client)
				if err != nil {
					return nil, Channels{}, err
				}
				candidates, err := channelCandidates(ctx, client, mine, term, input.TeamID)
				if err != nil {
					return nil, Channels{}, err
				}
				lower := strings.ToLower(term)
				out := Channels{Channels: []Channel{}}
				for _, candidate := range candidates {
					if slices.ContainsFunc(candidate.names, func(n string) bool { return strings.Contains(strings.ToLower(n), lower) }) {
						if len(out.Channels) == maxChannels {
							out.More = true
							break
						}
						out.Channels = append(out.Channels, candidate.value)
					}
				}
				return nil, out, nil
			}
		},
	)
}

type listTeamChannelsInput struct {
	TeamID string `json:"team_id" jsonschema:"the team whose channels to list"`
	Page   int    `json:"page,omitempty" jsonschema:"the page to read, from 0, when more says there are more"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many channels a page holds, at most 200; 50 when not given"`
}

// teamChannelsSpec is a tool that pages through one kind of a team's channels.
func teamChannelsSpec(name, title, description, operation, why string, list func(context.Context, *mattermost.Client, string, int, int) ([]*model.Channel, error)) Spec {
	return toolSpec(
		&mcp.Tool{Name: name, Description: description, Annotations: readOnly(title)},
		[]Use{
			{
				Operation: operation,
				Params: map[string]Coverage{
					"team_id":  SetBy("team_id"),
					"page":     SetBy("page"),
					"per_page": SetBy("limit"),
				},
			},
			{
				Operation: "GetTeam",
				Params:    map[string]Coverage{"team_id": SetBy("team_id")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[listTeamChannelsInput, Channels] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listTeamChannelsInput) (*mcp.CallToolResult, Channels, error) {
				limit := input.Limit
				switch {
				case limit == 0:
					limit = defaultChannels
				case limit < 0 || limit > maxChannels:
					return nil, Channels{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxChannels, limit)
				case input.Page < 0:
					return nil, Channels{}, fmt.Errorf("page counts from 0, not %d", input.Page)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Channels{}, err
				}
				team, err := client.Team(ctx, input.TeamID)
				if err != nil {
					return nil, Channels{}, err
				}
				channels, err := list(ctx, client, input.TeamID, input.Page, limit)
				if err != nil {
					return nil, Channels{}, fmt.Errorf("%s: %w", why, err)
				}
				out := Channels{Channels: []Channel{}, More: len(channels) == limit}
				for _, channel := range channels {
					listed := toChannel(channel, "", nil)
					listed.Team = team.DisplayName
					out.Channels = append(out.Channels, listed)
				}
				return nil, out, nil
			}
		},
	)
}

func listTeamChannelsSpec() Spec {
	return teamChannelsSpec("list_team_channels", "List a team's channels",
		"List a team's public channels, whether the user has joined them or not, by name, a page at a time.",
		"GetPublicChannelsForTeam", "listing the team's public channels",
		func(ctx context.Context, client *mattermost.Client, teamID string, page, perPage int) ([]*model.Channel, error) {
			return client.PublicChannels(ctx, teamID, page, perPage)
		})
}

func listArchivedChannelsSpec() Spec {
	return teamChannelsSpec("list_archived_channels", "List archived channels",
		"List a team's archived channels: public ones, and private ones the user belonged to, a page at a time. "+
			"An archived channel can be read with read_channel but not posted in.",
		"GetDeletedChannelsForTeam", "listing the team's archived channels",
		func(ctx context.Context, client *mattermost.Client, teamID string, page, perPage int) ([]*model.Channel, error) {
			return client.ArchivedChannels(ctx, teamID, page, perPage)
		})
}

// ChannelStats is how much a channel holds.
type ChannelStats struct {
	ChannelID   string `json:"channel_id"`
	Members     int64  `json:"members" jsonschema:"how many people belong to the channel, guests included"`
	Guests      int64  `json:"guests"`
	PinnedPosts int64  `json:"pinned_posts"`
	Files       int64  `json:"files"`
}

type channelIDInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel"`
}

func getChannelStatsSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "get_channel_stats",
			Description: "How many people belong to a channel, how many of them are guests, and how many posts are pinned and files shared in it.",
			Annotations: readOnly("Get channel stats"),
		},
		[]Use{{Operation: "GetChannelStats", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}}},
		func(clientFor ClientFor) mcp.ToolHandlerFor[channelIDInput, ChannelStats] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input channelIDInput) (*mcp.CallToolResult, ChannelStats, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, ChannelStats{}, err
				}
				stats, err := client.ChannelStats(ctx, input.ChannelID)
				if err != nil {
					return nil, ChannelStats{}, err
				}
				return nil, ChannelStats{
					ChannelID: input.ChannelID, Members: stats.MemberCount, Guests: stats.GuestCount,
					PinnedPosts: stats.PinnedPostCount, Files: stats.FilesCount,
				}, nil
			}
		},
	)
}
