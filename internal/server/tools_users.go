package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// UserSummary is the part of a Mattermost user a tool returns: who the user is,
// and not their notification settings, timestamps or authentication details.
type UserSummary struct {
	ID        string `json:"id" jsonschema:"the user's id"`
	Username  string `json:"username" jsonschema:"the name people mention them by, without the @"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Nickname  string `json:"nickname"`
	Position  string `json:"position,omitempty" jsonschema:"the job title they gave, if any"`
	Email     string `json:"email" jsonschema:"empty when the server hides email addresses from this identity"`
	Roles     string `json:"roles" jsonschema:"space-separated, such as system_user or system_admin"`
	Locale    string `json:"locale"`
	Timezone  string `json:"timezone,omitempty" jsonschema:"the timezone they set in Mattermost, such as Europe/Amsterdam; the times the tools return are in it, with its offset"`
	IsBot     bool   `json:"is_bot"`
	// Deactivated users can be read but not reached.
	Deactivated bool `json:"deactivated,omitempty" jsonschema:"a deactivated user can no longer sign in or be notified"`
}

func summarise(user *model.User) UserSummary {
	return UserSummary{
		ID:          user.Id,
		Username:    user.Username,
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		Nickname:    user.Nickname,
		Position:    user.Position,
		Email:       user.Email,
		Roles:       user.Roles,
		Locale:      user.Locale,
		Timezone:    user.GetPreferredTimezone(),
		IsBot:       user.IsBot,
		Deactivated: user.DeleteAt > 0,
	}
}

type getMeInput struct{}

func getMeSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "get_me",
			Description: "Return the Mattermost user this server acts as. Call it to learn whose " +
				"channels and messages the other tools read, and under whose name anything would be posted.",
			Annotations: readOnly("Who am I"),
		},
		[]Use{{
			Operation: "GetUser",
			Params: map[string]Coverage{
				"user_id": Fixed("me", "get_me reads the user the credential belongs to"),
			},
		}},
		func(clientFor ClientFor) mcp.ToolHandlerFor[getMeInput, UserSummary] {
			return func(ctx context.Context, request *mcp.CallToolRequest, _ getMeInput) (*mcp.CallToolResult, UserSummary, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, UserSummary{}, err
				}
				user, err := client.Me(ctx)
				if err != nil {
					return nil, UserSummary{}, err
				}
				return nil, summarise(user), nil
			}
		},
	)
}

type getUsersInput struct {
	Users []string `json:"users" jsonschema:"the people to look up, at most 100, each by username (with or without @), user id, or email address"`
}

func getUsersSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "get_users",
			Description: "Look up people by username, user id or email address, any mix of them in one call: their name, nickname, position, " +
				"whether they are a bot and whether they are deactivated. A name nobody has is listed in not_found with the closest usernames; when nobody is found at all, the call is refused with them.",
			Annotations: readOnly("Get users"),
		},
		[]Use{
			{Operation: "GetUsersByIds", Params: map[string]Coverage{"since": Omitted("the tool reads each user as they are now")}},
			{Operation: "GetUsersByUsernames", Params: map[string]Coverage{}},
			{Operation: "GetUserByEmail", Params: map[string]Coverage{"email": SetBy("users")}},
			{Operation: "SearchUsers", Params: suggestionSearch(SetBy("users"))},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[getUsersInput, Users] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input getUsersInput) (*mcp.CallToolResult, Users, error) {
				switch {
				case len(input.Users) == 0:
					return nil, Users{}, fmt.Errorf("give the usernames, user ids or email addresses to look up")
				case len(input.Users) > 100:
					return nil, Users{}, fmt.Errorf("look up at most 100 people at once, not %d", len(input.Users))
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Users{}, err
				}
				found, unknown, err := resolveUsers(ctx, client, input.Users)
				switch {
				case err != nil:
					return nil, Users{}, err
				case len(found) == 0:
					return nil, Users{}, unknownUsers(ctx, client, unknown)
				}
				// One slip does not cost the people found: it is named beside them.
				out := Users{Users: make([]UserSummary, 0, len(found))}
				for _, user := range found {
					out.Users = append(out.Users, summarise(user))
				}
				for _, ref := range unknown {
					out.NotFound = append(out.NotFound, unknownUser(ctx, client, ref))
				}
				return nil, out, nil
			}
		},
	), nil)
}

// lookUpUsers is the users the given references name, in their order: an
// email address, a user id, or a username. A reference shaped like an id that
// names no user is tried as a username. Unknown references are refused
// together, each with the closest usernames.
func lookUpUsers(ctx context.Context, client *mattermost.Client, refs []string) ([]*model.User, error) {
	out, unknown, err := resolveUsers(ctx, client, refs)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		return nil, unknownUsers(ctx, client, unknown)
	}
	return out, nil
}

// resolveUsers is the users the given references name, in their order, and
// the references that name nobody.
func resolveUsers(ctx context.Context, client *mattermost.Client, refs []string) ([]*model.User, []string, error) {
	byRef := map[string]*model.User{}
	var ids, names []string
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		switch {
		case strings.Contains(strings.TrimPrefix(ref, "@"), "@"):
			user, err := client.UserByEmail(ctx, ref)
			if err != nil && !notFound(err) {
				return nil, nil, err
			}
			if user != nil {
				byRef[ref] = user
			}
		case mattermostID.MatchString(ref):
			ids = append(ids, ref)
		default:
			// Mattermost keeps usernames in lower case and matches a list of
			// them exactly.
			names = append(names, strings.ToLower(strings.TrimPrefix(ref, "@")))
		}
	}
	if len(ids) > 0 {
		users, err := client.Users(ctx, ids)
		if err != nil {
			return nil, nil, err
		}
		for _, user := range users {
			byRef[user.Id] = user
		}
		for _, id := range ids {
			if byRef[id] == nil {
				names = append(names, id)
			}
		}
	}
	if len(names) > 0 {
		users, err := client.UsersByUsernames(ctx, names)
		if err != nil {
			return nil, nil, err
		}
		for _, user := range users {
			byRef[user.Username] = user
		}
	}
	var out []*model.User
	var unknown []string
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		user := byRef[ref]
		if user == nil {
			user = byRef[strings.ToLower(strings.TrimPrefix(ref, "@"))]
		}
		if user == nil {
			unknown = append(unknown, ref)
			continue
		}
		out = append(out, user)
	}
	return out, unknown, nil
}

// unknownUsers is the error for references that name no user, with the
// closest usernames for each, so the model corrects itself.
func unknownUsers(ctx context.Context, client *mattermost.Client, unknown []string) error {
	parts := make([]string, 0, len(unknown))
	for _, ref := range unknown {
		parts = append(parts, unknownUser(ctx, client, ref))
	}
	return fmt.Errorf("no user is known as %s. search_users finds people by part of their name", strings.Join(parts, ", "))
}

// unknownUser is a reference that names nobody, quoted, with the closest
// usernames to it.
func unknownUser(ctx context.Context, client *mattermost.Client, ref string) string {
	near := nearestBySearch(strings.TrimPrefix(ref, "@"), 3, nil, func(term string) ([]string, bool) {
		users, err := client.SearchUsers(ctx, &model.UserSearch{Term: term, Limit: suggestionPage})
		if err != nil {
			return nil, false
		}
		known := make([]string, 0, len(users))
		for _, user := range users {
			known = append(known, user.Username)
		}
		return known, len(users) < suggestionPage
	})
	if len(near) > 0 {
		return fmt.Sprintf("%q (closest: %s)", ref, quoteAll(near))
	}
	return fmt.Sprintf("%q", ref)
}

// prefixes are the starts of a name to search by for the names closest to it,
// longest first: a search returns a page of matches, and on a server where
// many names share their first letters, a short start finds a page that need
// not hold the one meant. A longer start finds fewer and closer names; a
// shorter one still finds a name whose typo comes early.
func prefixes(name string) []string {
	runes := []rune(name)
	var out []string
	for _, n := range []int{len(runes) - 1, (len(runes)*2 + 2) / 3, (len(runes) + 1) / 2, 3} {
		n = min(n, len(runes))
		if n < 1 || (len(out) > 0 && len([]rune(out[len(out)-1])) <= n) {
			continue
		}
		out = append(out, string(runes[:n]))
	}
	return out
}

// nearestBySearch is up to n names closest to name, among extra and what
// searching by its prefixes finds, longest first. search answers with the
// names a term finds and whether that is all of them. The searches stop once
// one found every name that starts as the prefix does and a close name is
// among what was found: a page cut short at its limit need not hold the name
// meant, so a shorter prefix is searched too.
func nearestBySearch(name string, n int, extra []string, search func(term string) ([]string, bool)) []string {
	found := slices.Clone(extra)
	for _, prefix := range prefixes(name) {
		names, all := search(prefix)
		found = append(found, names...)
		if near := closest(name, found, n); all && len(near) > 0 {
			return near
		}
	}
	return closest(name, found, n)
}

// suggestionPage is how many names one search for suggestions reads.
const suggestionPage = 200

// The most users one search page holds, and how many when not told; and the
// most matches Mattermost's user search answers with, which is as far as its
// pages reach.
const (
	maxUsersPerSearch     = 100
	defaultUsersPerSearch = 20
	userSearchReach       = model.UserSearchMaxLimit
)

// Users wraps a list of users.
type Users struct {
	Users []UserSummary `json:"users"`
	// NotFound is what get_users was asked for that names nobody.
	NotFound []string `json:"not_found,omitempty" jsonschema:"each name asked for that nobody has, with the closest usernames"`
	pageInfo
}

type searchUsersInput struct {
	Term      string `json:"term" jsonschema:"part of a username, name, nickname or email address"`
	TeamID    string `json:"team_id,omitempty" jsonschema:"only members of this team"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"only members of this channel"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many users a page holds, at most 100; 20 when not given"`
	pageArgs
}

func searchUsersSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "search_users",
			Description: "Find Mattermost users whose username, name, nickname or email address contains a term, optionally only in one " +
				"team or channel, a page at a time. Mattermost finds at most 1000 for one term.",
			Annotations: readOnly("Search users"),
		},
		[]Use{{
			Operation: "SearchUsers",
			Params: map[string]Coverage{
				"body.term":          SetBy("term"),
				"body.team_id":       SetBy("team_id"),
				"body.in_channel_id": SetBy("channel_id"),
				"body.limit": Fixed("every match up to the end of the page asked for, at most 1000",
					"Mattermost's user search takes no offset, so a page asks for the matches before it too and keeps its own"),
				"body.allow_inactive":    Omitted("deactivated users cannot be messaged or mentioned, so the search leaves them out"),
				"body.without_team":      Omitted("finding users who belong to no team is an administrator's task"),
				"body.not_in_team_id":    Omitted("finding who is missing from a team is an administrator's task"),
				"body.not_in_channel_id": Omitted("finding who could be added to a channel belongs with a tool that adds them, which is not offered"),
				"body.in_group_id":       Omitted("groups are a licensed edition's feature, which the tests cannot reach (ADR-007)"),
				"body.group_constrained": Omitted("groups are a licensed edition's feature, which the tests cannot reach (ADR-007)"),
			},
		}},
		func(clientFor ClientFor) mcp.ToolHandlerFor[searchUsersInput, Users] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input searchUsersInput) (*mcp.CallToolResult, Users, error) {
				if strings.TrimSpace(input.Term) == "" {
					return nil, Users{}, fmt.Errorf("give a term to search for")
				}
				limit, err := limitOf(input.Limit, defaultUsersPerSearch, maxUsersPerSearch)
				if err != nil {
					return nil, Users{}, err
				}
				at, err := openCursor("search_users", input, input.Cursor)
				if err != nil {
					return nil, Users{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Users{}, err
				}
				found, err := client.SearchUsers(ctx, &model.UserSearch{
					Term:        strings.TrimPrefix(input.Term, "@"),
					TeamId:      input.TeamID,
					InChannelId: input.ChannelID,
					Limit:       min(at.Offset+limit+1, userSearchReach),
				})
				if err != nil {
					return nil, Users{}, err
				}
				page, next := offsetPage(found, at, limit)
				out := Users{Users: make([]UserSummary, 0, len(page)), pageInfo: pageInfo{NextCursor: next}}
				for _, user := range page {
					out.Users = append(out.Users, summarise(user))
				}
				return nil, out, nil
			}
		},
	), pagingShapes)
}

// UserStatus is whether someone is around: their presence and the status
// they set.
type UserStatus struct {
	Username string `json:"username"`
	// Status is Mattermost's presence: online, away, dnd or offline.
	Status         string `json:"status" jsonschema:"online, away, dnd (do not disturb) or offline"`
	SetByHand      bool   `json:"set_by_hand" jsonschema:"whether the person set the status themselves rather than Mattermost from their activity"`
	LastActivityAt string `json:"last_activity_at,omitempty"`
	DNDUntil       string `json:"dnd_until,omitempty" jsonschema:"when do not disturb ends, if the person set an end"`
	CustomEmoji    string `json:"custom_emoji,omitempty" jsonschema:"the emoji of the status message the person set, if any"`
	CustomText     string `json:"custom_text,omitempty" jsonschema:"the status message the person set, such as 'In a meeting', if any"`
	CustomUntil    string `json:"custom_until,omitempty" jsonschema:"when the status message clears, if it does"`
}

// Statuses is the status of each user asked about.
type Statuses struct {
	Users []UserStatus `json:"users"`
}

type getStatusInput struct {
	Usernames []string `json:"usernames" jsonschema:"the usernames to ask about, without the @; at most 100"`
}

func getStatusSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "get_status",
			Description: "Whether people are around: online, away, do not disturb or offline, when they were last active, and the " +
				"status message they set, such as being in a meeting or on holiday.",
			Annotations: readOnly("Get status"),
		},
		[]Use{
			{Operation: "GetUsersByUsernames", Params: map[string]Coverage{}},
			{Operation: "GetUsersStatusesByIds", Params: map[string]Coverage{}},
			zoneUse,
			{Operation: "SearchUsers", Params: suggestionSearch(Fixed("starts of an unknown username, longest first", "finds the usernames closest to one that is unknown, among however many share its first letters"))},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[getStatusInput, Statuses] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input getStatusInput) (*mcp.CallToolResult, Statuses, error) {
				switch {
				case len(input.Usernames) == 0:
					return nil, Statuses{}, fmt.Errorf("give the usernames to ask about")
				case len(input.Usernames) > 100:
					return nil, Statuses{}, fmt.Errorf("ask about at most 100 people at once, not %d", len(input.Usernames))
				}
				wanted := make([]string, 0, len(input.Usernames))
				for _, name := range input.Usernames {
					wanted = append(wanted, strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "@")))
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Statuses{}, err
				}
				users, err := client.UsersByUsernames(ctx, wanted)
				if err != nil {
					return nil, Statuses{}, err
				}
				byName := map[string]*model.User{}
				ids := make([]string, 0, len(users))
				for _, user := range users {
					byName[user.Username] = user
					ids = append(ids, user.Id)
				}
				var missing []string
				for _, name := range wanted {
					if byName[name] == nil {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					return nil, Statuses{}, unknownUsers(ctx, client, missing)
				}
				statuses, err := client.Statuses(ctx, ids)
				if err != nil {
					return nil, Statuses{}, err
				}
				byID := map[string]*model.Status{}
				for _, status := range statuses {
					byID[status.UserId] = status
				}
				zone, err := personZone(ctx, client)
				if err != nil {
					return nil, Statuses{}, err
				}
				out := Statuses{Users: []UserStatus{}}
				for _, name := range wanted {
					out.Users = append(out.Users, toStatus(byName[name], byID[byName[name].Id], zone))
				}
				return nil, out, nil
			}
		},
	), map[string]string{
		"usernames": "is the request body of GetUsersByUsernames, a JSON array of usernames, which has no fields to set by name",
	})
}

func toStatus(user *model.User, status *model.Status, zone *time.Location) UserStatus {
	out := UserStatus{Username: user.Username, Status: model.StatusOffline}
	if status != nil {
		out.Status, out.SetByHand, out.LastActivityAt = status.Status, status.Manual, timestamp(status.LastActivityAt, zone)
		if status.Status == model.StatusDnd && status.DNDEndTime > 0 {
			// Unlike Mattermost's other times, a do-not-disturb end is in seconds.
			out.DNDUntil = timestamp(status.DNDEndTime*1000, zone)
		}
	}
	if custom := user.GetCustomStatus(); custom != nil && (custom.Text != "" || custom.Emoji != "") &&
		(custom.ExpiresAt.IsZero() || custom.ExpiresAt.After(time.Now())) {
		out.CustomEmoji, out.CustomText = custom.Emoji, custom.Text
		if !custom.ExpiresAt.IsZero() {
			out.CustomUntil = custom.ExpiresAt.In(zone).Format(time.RFC3339)
		}
	}
	return out
}
