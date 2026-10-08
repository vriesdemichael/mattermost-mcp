package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Belonging to channels, making them, marking them read, and the user's
// status: what a person does in Mattermost's sidebar rather than in a post.

// channelUse is GetChannel, for a tool that reads the channel it acts on to
// name it in the question.
func channelUse() Use {
	return Use{Operation: "GetChannel", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}}
}

// channelLabel names a channel as the person knows it: ~display name, and its team.
func channelLabel(channel Channel) string {
	label := "~" + oneLine(channel.DisplayName)
	if channel.Team != "" {
		label += " in " + oneLine(channel.Team)
	}
	return label
}

// teamChannel is the channel an id means, refused when it is a direct or
// group message, which nobody joins or leaves, or archived.
func teamChannel(ctx context.Context, client *mattermost.Client, channelID string) (Channel, error) {
	channel, err := findChannel(ctx, client, channelID, "")
	switch {
	case err != nil:
		return Channel{}, err
	case channel.TeamID == "":
		return Channel{}, fmt.Errorf("%s is a %s message, which belongs to no team; nobody joins or leaves one", oneLine(channel.DisplayName), channel.Type)
	case channel.Archived:
		return Channel{}, fmt.Errorf("%s is archived: it can be read, but nobody joins, leaves or is added to it", channelLabel(channel))
	}
	return channel, nil
}

// Membership is the user's place in a channel after a tool changed it.
type Membership struct {
	ChannelID string   `json:"channel_id"`
	Channel   string   `json:"channel" jsonschema:"the channel's display name"`
	Team      string   `json:"team,omitempty"`
	Member    bool     `json:"member" jsonschema:"whether the user belongs to the channel now"`
	Added     []string `json:"added,omitempty" jsonschema:"the usernames of the people added"`
}

type channelInput struct {
	ChannelID string `json:"channel_id" jsonschema:"the channel"`
}

func joinChannelSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "join_channel",
			Description: "Join a public channel of one of the user's teams, so they can post in it and it shows in their sidebar. " +
				"Everyone in the channel sees that they joined, so the person is asked to confirm first; if they decline or close the " +
				"question, do not try again unless they ask. A private channel is joined only by being added.",
			Annotations: writes("Join channel", true),
		},
		uses([]Use{
			{
				Operation: "AddChannelMember",
				Params: map[string]Coverage{
					"channel_id":        SetBy("channel_id"),
					"body.user_id":      Fixed("the user's own id", "a person joins a channel as themselves"),
					"body.user_ids":     Omitted("the user joins alone"),
					"body.post_root_id": Omitted("joining is not a reply to a thread"),
				},
			},
			channelUse(),
		}),
		func(clientFor ClientFor) mcp.ToolHandlerFor[channelInput, Membership] {
			return asking("join_channel",
				func(ctx context.Context, request *mcp.CallToolRequest, input channelInput) (confirmation, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					channel, err := teamChannel(ctx, client, input.ChannelID)
					if err != nil {
						return confirmation{}, err
					}
					if channel.Member != nil && *channel.Member {
						return confirmation{}, fmt.Errorf("the user belongs to %s already", channelLabel(channel))
					}
					self, err := client.Me(ctx)
					if err != nil {
						return confirmation{}, err
					}
					return confirmation{
						Message: fmt.Sprintf("Join %s as @%s. Everyone in the channel sees that you joined.", channelLabel(channel), self.Username),
						Label:   "Join " + channelLabel(channel),
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input channelInput) (*mcp.CallToolResult, Membership, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Membership{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return nil, Membership{}, err
					}
					if err := client.AddToChannel(ctx, input.ChannelID, []string{self.Id}); err != nil {
						return nil, Membership{}, err
					}
					return nil, membership(ctx, client, input.ChannelID, true, nil), nil
				})
		},
	)
}

// membership is what a tool answers once it changed who is in a channel.
func membership(ctx context.Context, client *mattermost.Client, channelID string, member bool, added []string) Membership {
	out := Membership{ChannelID: channelID, Member: member, Added: added}
	if channel, err := findChannel(ctx, client, channelID, ""); err == nil {
		out.Channel, out.Team = channel.DisplayName, channel.Team
	}
	return out
}

func leaveChannelSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "leave_channel",
			Description: "Leave a channel the user belongs to. Everyone in it sees that they left, and a private channel can be joined " +
				"again only by being added, so the person is asked to confirm first; if they decline or close the question, do not try " +
				"again unless they ask. Mattermost keeps everyone in a team's town square.",
			Annotations: overwrites("Leave channel"),
		},
		uses([]Use{
			{
				Operation: "RemoveUserFromChannel",
				Params: map[string]Coverage{
					"channel_id": SetBy("channel_id"),
					"user_id":    Fixed("the user's own id", "a person leaves a channel as themselves; removing others is not offered"),
				},
			},
			channelUse(),
		}),
		func(clientFor ClientFor) mcp.ToolHandlerFor[channelInput, Membership] {
			return asking("leave_channel",
				func(ctx context.Context, request *mcp.CallToolRequest, input channelInput) (confirmation, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					channel, err := teamChannel(ctx, client, input.ChannelID)
					if err != nil {
						return confirmation{}, err
					}
					if channel.Member == nil || !*channel.Member {
						return confirmation{}, fmt.Errorf("the user does not belong to %s", channelLabel(channel))
					}
					self, err := client.Me(ctx)
					if err != nil {
						return confirmation{}, err
					}
					again := "You can join it again."
					if channel.Type == "private" {
						again = "It is private: you can come back only when someone adds you."
					}
					return confirmation{
						Message: fmt.Sprintf("Leave %s as @%s. Everyone in the channel sees that you left. %s", channelLabel(channel), self.Username, again),
						Label:   "Leave " + channelLabel(channel),
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input channelInput) (*mcp.CallToolResult, Membership, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Membership{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return nil, Membership{}, err
					}
					out := membership(ctx, client, input.ChannelID, false, nil)
					if err := client.Leave(ctx, input.ChannelID, self.Id); err != nil {
						return nil, Membership{}, err
					}
					return nil, out, nil
				})
		},
	)
}

type addMembersInput struct {
	ChannelID string   `json:"channel_id" jsonschema:"the channel to add them to"`
	Usernames []string `json:"usernames" jsonschema:"the people to add, at most 20, each by username (with or without @), user id or email address"`
}

func addChannelMembersSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "add_channel_members",
			Description: "Add people to a channel the user belongs to. Each is notified, and everyone in the channel sees who was added, " +
				"so the person is asked to confirm first, seeing who goes where; if they decline or close the question, do not try " +
				"again unless they ask. Someone outside the channel's team cannot be added.",
			Annotations: writes("Add channel members", true),
		},
		uses([]Use{
			{
				Operation: "AddChannelMember",
				Params: map[string]Coverage{
					"channel_id":        SetBy("channel_id"),
					"body.user_id":      SetBy("usernames"),
					"body.user_ids":     SetBy("usernames"),
					"body.post_root_id": Omitted("people are added to the channel, not to a thread"),
				},
			},
			channelUse(),
			{Operation: "GetUsersByIds", Params: map[string]Coverage{"since": Omitted("the tool reads each person as they are now")}},
		}, peopleUses("usernames")),
		func(clientFor ClientFor) mcp.ToolHandlerFor[addMembersInput, Membership] {
			// What the call adds is bound to whom the usernames mean when the
			// person is asked.
			people := func(ctx context.Context, request *mcp.CallToolRequest, input addMembersInput) (*mattermost.Client, []*model.User, error) {
				switch {
				case len(input.Usernames) == 0:
					return nil, nil, fmt.Errorf("give the usernames of the people to add")
				case len(input.Usernames) > 20:
					return nil, nil, fmt.Errorf("add at most 20 people at once, not %d", len(input.Usernames))
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, nil, err
				}
				users, err := lookUpUsers(ctx, client, input.Usernames)
				return client, users, err
			}
			ids := func(users []*model.User) []string {
				out := make([]string, 0, len(users))
				for _, user := range users {
					out = append(out, user.Id)
				}
				slices.Sort(out)
				return slices.Compact(out)
			}
			return askingBound("add_channel_members",
				func(ctx context.Context, request *mcp.CallToolRequest, input addMembersInput) (string, error) {
					_, users, err := people(ctx, request, input)
					if err != nil {
						return "", err
					}
					return strings.Join(ids(users), ","), nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input addMembersInput) (confirmation, error) {
					client, users, err := people(ctx, request, input)
					if err != nil {
						return confirmation{}, err
					}
					channel, err := teamChannel(ctx, client, input.ChannelID)
					if err != nil {
						return confirmation{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return confirmation{}, err
					}
					var names []string
					for _, user := range users {
						names = append(names, "@"+user.Username)
					}
					names = slices.Compact(names)
					return confirmation{
						Message: fmt.Sprintf("Add %s to %s, as @%s. Each is notified, and everyone in the channel sees who was added.",
							strings.Join(names, ", "), channelLabel(channel), self.Username),
						Label: fmt.Sprintf("Add %s to %s", peopleCount(int64(len(names))), channelLabel(channel)),
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input addMembersInput) (*mcp.CallToolResult, Membership, error) {
					client, users, err := people(ctx, request, input)
					if err != nil {
						return nil, Membership{}, err
					}
					if err := stillAsAsked(ctx, "add_channel_members", strings.Join(ids(users), ",")); err != nil {
						return nil, Membership{}, err
					}
					if err := client.AddToChannel(ctx, input.ChannelID, ids(users)); err != nil {
						return nil, Membership{}, err
					}
					var added []string
					for _, user := range users {
						if !slices.Contains(added, user.Username) {
							added = append(added, user.Username)
						}
					}
					return nil, membership(ctx, client, input.ChannelID, true, added), nil
				})
		},
	)
}

type createChannelInput struct {
	TeamID      string `json:"team_id" jsonschema:"the team to make the channel in"`
	DisplayName string `json:"display_name" jsonschema:"the channel's name as people see it, such as Release Planning"`
	Private     bool   `json:"private,omitempty" jsonschema:"make a private channel, which only the people added see; a public one, which anyone in the team can find and join, when not given"`
	Purpose     string `json:"purpose,omitempty" jsonschema:"what the channel is for, shown when people browse channels"`
	Header      string `json:"header,omitempty" jsonschema:"the text at the top of the channel, in Mattermost Markdown"`
}

func createChannelSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "create_channel",
			Description: "Create a channel in one of the user's teams, public unless private is true, with the user as its first member. " +
				"Its address is made from its name. The person is asked to confirm first, seeing the name, the team and who can find it; " +
				"if they decline or close the question, do not try again unless they ask. add_channel_members adds people to it.",
			Annotations: writes("Create channel", false),
		},
		[]Use{
			{
				Operation: "CreateChannel",
				Params: map[string]Coverage{
					"body.team_id":      SetBy("team_id"),
					"body.display_name": SetBy("display_name"),
					"body.name": Fixed("the display name in lower case, a dash for each run of other characters",
						"the address Mattermost's own app makes from a name; a name with no letters or digits in it gets a random one"),
					"body.type":                  SetBy("private"),
					"body.purpose":               SetBy("purpose"),
					"body.header":                SetBy("header"),
					"body.default_category_name": Omitted("the channel goes in the sidebar where Mattermost puts a new one"),
					"body.managed_category_name": Omitted("sidebar categories an administrator manages are a licensed feature"),
				},
			},
			{Operation: "GetTeam", Params: map[string]Coverage{"team_id": SetBy("team_id")}},
			{Operation: "GetUser", Params: map[string]Coverage{"user_id": Fixed("me", "the channel is made under the user's name, and the question says whose")}},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[createChannelInput, Channel] {
			return asking("create_channel",
				func(ctx context.Context, request *mcp.CallToolRequest, input createChannelInput) (confirmation, error) {
					if strings.TrimSpace(input.DisplayName) == "" {
						return confirmation{}, fmt.Errorf("give the channel a name")
					}
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					team, err := client.Team(ctx, input.TeamID)
					if err != nil {
						return confirmation{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return confirmation{}, err
					}
					kind, who := "public", "Anyone in the team can find and join it."
					if input.Private {
						kind, who = "private", "Only the people added to it see it."
					}
					message := fmt.Sprintf("Create the %s channel ~%s in %s, as @%s. %s", kind, oneLine(input.DisplayName), oneLine(team.DisplayName), self.Username, who)
					if input.Purpose != "" {
						message += "\n\nIts purpose: " + input.Purpose
					}
					if input.Header != "" {
						message += "\n\nIts header: " + input.Header
					}
					return confirmation{
						Message: message,
						Label:   fmt.Sprintf("Create the %s channel ~%s in %s", kind, oneLine(input.DisplayName), oneLine(team.DisplayName)),
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input createChannelInput) (*mcp.CallToolResult, Channel, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Channel{}, err
					}
					created, err := client.CreateChannel(ctx, mattermost.NewChannel{
						TeamID: input.TeamID, Name: channelAddress(input.DisplayName), DisplayName: strings.TrimSpace(input.DisplayName),
						Private: input.Private, Purpose: input.Purpose, Header: input.Header,
					})
					if err != nil {
						return nil, Channel{}, err
					}
					member := true
					out := toChannel(created, "", nil)
					out.Member = &member
					if team, err := client.Team(ctx, created.TeamId); err == nil {
						out.Team = team.DisplayName
					}
					return nil, out, nil
				})
		},
	)
}

// channelAddress is the name in a channel's address made from its display
// name, as Mattermost's own app makes it: lower case, a dash for each run of
// anything but letters and digits, and a random one when nothing is left.
func channelAddress(display string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(display)) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	address := strings.Trim(b.String(), "-")
	if len(address) > model.ChannelNameMaxLength {
		address = strings.Trim(address[:model.ChannelNameMaxLength], "-")
	}
	if len(address) < 2 {
		return model.NewId()
	}
	return address
}

// Read is a channel the user marked read.
type Read struct {
	ChannelID string `json:"channel_id"`
	Read      bool   `json:"read"`
}

func markChannelReadSpec() Spec {
	return unasked(toolSpec(
		&mcp.Tool{
			Name: "mark_channel_read",
			Description: "Mark a channel read for the user, as opening it in Mattermost does, clearing its unread count and mentions. " +
				"Only when the person asks for it: reading a channel for them is not their reading it, so never mark one read because you read it.",
			Annotations: personal("Mark channel read"),
		},
		[]Use{
			{
				Operation: "ViewChannel",
				Params: map[string]Coverage{
					"user_id":                          Fixed("me", "a person marks channels read for themselves"),
					"body.channel_id":                  SetBy("channel_id"),
					"body.prev_channel_id":             Omitted("Mattermost marks the channel left read too; the tool leaves no channel"),
					"body.collapsed_threads_supported": Undocumented("", "api4's viewChannel reads it to mark threads read with the channel when true; Mattermost's client sends false, and threads keep their own unread state"),
				},
			},
			{Operation: "GetUser", Params: map[string]Coverage{"user_id": Fixed("me", "the channel is marked read for the user")}},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[channelInput, Read] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input channelInput) (*mcp.CallToolResult, Read, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Read{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Read{}, err
				}
				if err := client.MarkRead(ctx, self.Id, input.ChannelID); err != nil {
					return nil, Read{}, err
				}
				return nil, Read{ChannelID: input.ChannelID, Read: true}, nil
			}
		},
	), "what the person has read is theirs alone, and they asked for it to be marked")
}

// statusNames are the presences a person sets, by what Mattermost calls them.
var statusNames = map[string]string{
	model.StatusOnline:  "Online",
	model.StatusAway:    "Away",
	model.StatusDnd:     "Do not disturb",
	model.StatusOffline: "Offline",
}

type setStatusInput struct {
	Status    string `json:"status,omitempty" jsonschema:"online, away, dnd for do not disturb, or offline"`
	DNDUntil  string `json:"dnd_until,omitempty" jsonschema:"when do not disturb ends: a time with an offset, or a time without one in the person's timezone"`
	Text      string `json:"text,omitempty" jsonschema:"a status message, such as In a meeting"`
	Emoji     string `json:"emoji,omitempty" jsonschema:"the emoji shown with the status message, by name, such as calendar"`
	TextUntil string `json:"text_until,omitempty" jsonschema:"when the status message is cleared: a time with an offset, or a time without one in the person's timezone; it stays when not given"`
	ClearText bool   `json:"clear_text,omitempty" jsonschema:"clear the status message"`
}

// Status is the user's presence and status message, as a tool set them.
type Status struct {
	Status    string `json:"status,omitempty"`
	DNDUntil  string `json:"dnd_until,omitempty"`
	Text      string `json:"text,omitempty"`
	Emoji     string `json:"emoji,omitempty"`
	TextUntil string `json:"text_until,omitempty"`
	Cleared   bool   `json:"cleared,omitempty" jsonschema:"the status message was cleared"`
}

func setStatusSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "set_status",
			Description: "Set the user's status, as everyone sees it beside their name: online, away, do not disturb (until a time, " +
				"with dnd_until) or offline, and a status message with an emoji, until a time or until changed, or clear_text to clear it. " +
				"Everyone sees it, so the person is asked to confirm first; if they decline or close the question, do not try again unless they ask.",
			Annotations: writes("Set status", true),
		},
		[]Use{
			{
				Operation: "UpdateUserStatus",
				Params: map[string]Coverage{
					"user_id":           Fixed("me", "a person sets their own status"),
					"body.user_id":      Fixed("the user's own id", "a person sets their own status"),
					"body.status":       SetBy("status"),
					"body.dnd_end_time": SetBy("dnd_until"),
				},
			},
			{
				Operation: "UpdateUserCustomStatus",
				Params: map[string]Coverage{
					"user_id":         Fixed("me", "a person sets their own status message"),
					"body.text":       SetBy("text"),
					"body.emoji":      SetBy("emoji"),
					"body.expires_at": SetBy("text_until"),
					"body.duration":   Fixed("date_and_time when text_until is given", "the message is cleared at the time given, as Mattermost's own \"Choose date and time\" does"),
				},
			},
			{Operation: "UnsetUserCustomStatus", Params: map[string]Coverage{"user_id": Fixed("me", "a person clears their own status message")}},
			{Operation: "GetUser", Params: map[string]Coverage{"user_id": Fixed("me", "a time without an offset is read in the person's timezone, and the question says whose status changes")}},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[setStatusInput, Status] {
			plan := func(ctx context.Context, request *mcp.CallToolRequest, input setStatusInput) (*mattermost.Client, *model.User, Status, time.Time, time.Time, error) {
				var none Status
				status := strings.ToLower(strings.TrimSpace(input.Status))
				switch {
				case status != "" && statusNames[status] == "":
					return nil, nil, none, time.Time{}, time.Time{}, fmt.Errorf("status is online, away, dnd or offline, not %q", input.Status)
				case input.DNDUntil != "" && status != model.StatusDnd:
					return nil, nil, none, time.Time{}, time.Time{}, fmt.Errorf("dnd_until goes with status dnd")
				case input.ClearText && (input.Text != "" || input.Emoji != "" || input.TextUntil != ""):
					return nil, nil, none, time.Time{}, time.Time{}, fmt.Errorf("clear_text clears the status message; give it without text, emoji or text_until")
				case input.TextUntil != "" && input.Text == "" && input.Emoji == "":
					return nil, nil, none, time.Time{}, time.Time{}, fmt.Errorf("text_until goes with a status message: give text or emoji")
				case status == "" && input.Text == "" && input.Emoji == "" && !input.ClearText:
					return nil, nil, none, time.Time{}, time.Time{}, fmt.Errorf("give a status, a status message, or clear_text")
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, nil, none, time.Time{}, time.Time{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, nil, none, time.Time{}, time.Time{}, err
				}
				zone, err := userZone(self)
				if err != nil {
					zone = time.UTC
				}
				until := func(name, at string) (time.Time, error) {
					if at == "" {
						return time.Time{}, nil
					}
					parsed, err := localTime(at, zone)
					switch {
					case err != nil:
						return time.Time{}, fmt.Errorf("%s must be a time such as 2026-10-12T17:00 or 2026-10-12T17:00:00+02:00, not %q", name, at)
					case !parsed.After(time.Now()):
						return time.Time{}, fmt.Errorf("%s is not in the future", name)
					}
					return parsed, nil
				}
				dndEnd, err := until("dnd_until", input.DNDUntil)
				if err != nil {
					return nil, nil, none, time.Time{}, time.Time{}, err
				}
				// Mattermost keeps the end of do not disturb to the minute.
				dndEnd = dndEnd.Truncate(time.Minute)
				textEnd, err := until("text_until", input.TextUntil)
				if err != nil {
					return nil, nil, none, time.Time{}, time.Time{}, err
				}
				emoji := ""
				if input.Emoji != "" {
					if emoji, err = emojiName(input.Emoji); err != nil {
						return nil, nil, none, time.Time{}, time.Time{}, err
					}
				}
				out := Status{Status: status, Text: strings.TrimSpace(input.Text), Emoji: emoji, Cleared: input.ClearText}
				if !dndEnd.IsZero() {
					out.DNDUntil = dndEnd.In(zone).Format(time.RFC3339)
				}
				if !textEnd.IsZero() {
					out.TextUntil = textEnd.In(zone).Format(time.RFC3339)
				}
				return client, self, out, dndEnd, textEnd, nil
			}
			return asking("set_status",
				func(ctx context.Context, request *mcp.CallToolRequest, input setStatusInput) (confirmation, error) {
					_, self, planned, _, _, err := plan(ctx, request, input)
					if err != nil {
						return confirmation{}, err
					}
					var changes []string
					if planned.Status != "" {
						change := "your status to " + statusNames[planned.Status]
						if planned.DNDUntil != "" {
							change += " until " + planned.DNDUntil
						}
						changes = append(changes, change)
					}
					if planned.Text != "" || planned.Emoji != "" {
						change := "your status message to "
						if planned.Emoji != "" {
							change += ":" + planned.Emoji + ": "
						}
						change += "“" + oneLine(planned.Text) + "”"
						if planned.TextUntil != "" {
							change += " until " + planned.TextUntil
						}
						changes = append(changes, change)
					}
					if planned.Cleared {
						changes = append(changes, "clear your status message")
					}
					what := strings.Join(changes, ", and ")
					return confirmation{
						Message: fmt.Sprintf("Set, as @%s, %s. Everyone sees it beside your name.", self.Username, what),
						Label:   "Set " + what,
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input setStatusInput) (*mcp.CallToolResult, Status, error) {
					client, self, planned, dndEnd, textEnd, err := plan(ctx, request, input)
					if err != nil {
						return nil, Status{}, err
					}
					if planned.Status != "" {
						if _, err := client.SetStatus(ctx, self.Id, planned.Status, dndEnd); err != nil {
							return nil, Status{}, err
						}
					}
					if planned.Text != "" || planned.Emoji != "" {
						if _, err := client.SetCustomStatus(ctx, self.Id, planned.Emoji, planned.Text, textEnd); err != nil {
							return nil, Status{}, err
						}
					}
					if planned.Cleared {
						if err := client.ClearCustomStatus(ctx, self.Id); err != nil {
							return nil, Status{}, err
						}
					}
					return nil, planned, nil
				})
		},
	), map[string]string{"clear_text": "chooses UnsetUserCustomStatus"})
}
