package server

import (
	"context"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Team is a team as a tool returns it.
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name" jsonschema:"the name in the team's address"`
	DisplayName string `json:"display_name"`
	Description string `json:"description,omitempty"`
}

// Teams wraps a list of teams, because a tool's answer is an object.
type Teams struct {
	Teams []Team `json:"teams"`
}

type listTeamsInput struct{}

func listTeamsSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "list_teams",
			Description: "List the Mattermost teams this server's user belongs to. A channel belongs to a team, so start here to find where a conversation is.",
			Annotations: readOnly("List teams"),
		},
		[]Use{{
			Operation: "GetTeamsForUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "the tool lists the teams of the user the credential belongs to")},
		}},
		func(clientFor ClientFor) mcp.ToolHandlerFor[listTeamsInput, Teams] {
			return func(ctx context.Context, request *mcp.CallToolRequest, _ listTeamsInput) (*mcp.CallToolResult, Teams, error) {
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
					out.Teams = append(out.Teams, Team{ID: team.Id, Name: team.Name, DisplayName: team.DisplayName, Description: team.Description})
				}
				slices.SortFunc(out.Teams, func(a, b Team) int { return strings.Compare(a.DisplayName, b.DisplayName) })
				return nil, out, nil
			}
		},
	)
}

// Channel is a channel as a tool returns it, with what the user has not read
// in it.
type Channel struct {
	ID          string `json:"id"`
	TeamID      string `json:"team_id,omitempty" jsonschema:"the team the channel is in; empty for a direct or group message"`
	Name        string `json:"name" jsonschema:"the name in the channel's address"`
	DisplayName string `json:"display_name" jsonschema:"for a direct message, the other person's username"`
	Type        string `json:"type" jsonschema:"public, private, direct or group"`
	Purpose     string `json:"purpose,omitempty"`
	Header      string `json:"header,omitempty"`
	LastPostAt  string `json:"last_post_at,omitempty"`
	// Unread and Mentions are what the user has not read; nil when Mattermost
	// gave no membership to count from.
	Unread   *int64 `json:"unread_messages,omitempty" jsonschema:"messages posted since the user last read the channel, replies included"`
	Mentions *int64 `json:"mentions,omitempty" jsonschema:"unread messages that mention the user"`
}

// Channels wraps a list of channels.
type Channels struct {
	Channels []Channel `json:"channels"`
}

type listChannelsInput struct {
	TeamID     string `json:"team_id,omitempty" jsonschema:"only this team's channels, with direct and group messages, which belong to no team"`
	UnreadOnly bool   `json:"unread_only,omitempty" jsonschema:"only channels with something unread"`
}

var channelTypes = map[model.ChannelType]string{
	model.ChannelTypeOpen:    "public",
	model.ChannelTypePrivate: "private",
	model.ChannelTypeDirect:  "direct",
	model.ChannelTypeGroup:   "group",
}

func listChannelsSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "list_channels",
			Description: "List the channels this server's user belongs to, direct and group messages included, with how many " +
				"messages and mentions are unread in each. Use it to find a channel to read, or with unread_only to see what needs attention.",
			Annotations: readOnly("List channels"),
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
					"last_delete_at":  Fixed("0", "archived channels are not listed: a person asking for their channels means the ones they can talk in"),
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
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads the other person of each direct message, whenever they changed")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[listChannelsInput, Channels] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listChannelsInput) (*mcp.CallToolResult, Channels, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Channels{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Channels{}, err
				}
				channels, err := client.Channels(ctx)
				if err != nil {
					return nil, Channels{}, err
				}
				memberships := map[string]model.ChannelMember{}
				teams := map[string]bool{}
				for _, channel := range channels {
					if channel.TeamId != "" && !teams[channel.TeamId] {
						teams[channel.TeamId] = true
						members, err := client.ChannelMemberships(ctx, channel.TeamId)
						if err != nil {
							return nil, Channels{}, err
						}
						for _, member := range members {
							memberships[member.ChannelId] = member
						}
					}
				}
				var others []string
				for _, channel := range channels {
					if channel.Type == model.ChannelTypeDirect {
						others = append(others, otherInDirect(channel, self.Id))
					}
				}
				names, err := usernames(ctx, client, others)
				if err != nil {
					return nil, Channels{}, err
				}
				out := Channels{Channels: []Channel{}}
				for _, channel := range channels {
					if input.TeamID != "" && channel.TeamId != "" && channel.TeamId != input.TeamID {
						continue
					}
					listed := toChannel(channel, self.Id, names)
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
	}
}
