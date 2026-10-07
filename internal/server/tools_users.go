package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	IsBot     bool   `json:"is_bot"`
}

func summarise(user *model.User) UserSummary {
	return UserSummary{
		ID:        user.Id,
		Username:  user.Username,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Nickname:  user.Nickname,
		Position:  user.Position,
		Email:     user.Email,
		Roles:     user.Roles,
		Locale:    user.Locale,
		IsBot:     user.IsBot,
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

type getUserInput struct {
	Username string `json:"username,omitempty" jsonschema:"the username, without the @"`
	UserID   string `json:"user_id,omitempty" jsonschema:"the user's id, such as a post's author_id"`
}

func getUserSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "get_user",
			Description: "Look up one Mattermost user by username or by id: their name, nickname, position and whether they are a bot. Give one of the two.",
			Annotations: readOnly("Get user"),
		},
		[]Use{
			{Operation: "GetUserByUsername", Params: map[string]Coverage{"username": SetBy("username")}},
			{Operation: "GetUser", Params: map[string]Coverage{"user_id": SetBy("user_id")}},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[getUserInput, UserSummary] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input getUserInput) (*mcp.CallToolResult, UserSummary, error) {
				if (input.Username == "") == (input.UserID == "") {
					return nil, UserSummary{}, fmt.Errorf("give a username or a user_id, one of the two")
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, UserSummary{}, err
				}
				var user *model.User
				if input.Username != "" {
					user, err = client.UserByUsername(ctx, strings.TrimPrefix(input.Username, "@"))
				} else {
					user, err = client.User(ctx, input.UserID)
				}
				if err != nil {
					return nil, UserSummary{}, err
				}
				return nil, summarise(user), nil
			}
		},
	)
}

// The most users one search returns, and how many when not told.
const (
	maxUsersPerSearch     = 100
	defaultUsersPerSearch = 20
)

// Users wraps a list of users.
type Users struct {
	Users []UserSummary `json:"users"`
}

type searchUsersInput struct {
	Term      string `json:"term" jsonschema:"part of a username, name, nickname or email address"`
	TeamID    string `json:"team_id,omitempty" jsonschema:"only members of this team"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"only members of this channel"`
	Limit     int    `json:"limit,omitempty" jsonschema:"how many users to return, at most 100; 20 when not given"`
}

func searchUsersSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name:        "search_users",
			Description: "Find Mattermost users whose username, name, nickname or email address contains a term, optionally only in one team or channel.",
			Annotations: readOnly("Search users"),
		},
		[]Use{{
			Operation: "SearchUsers",
			Params: map[string]Coverage{
				"body.term":              SetBy("term"),
				"body.team_id":           SetBy("team_id"),
				"body.in_channel_id":     SetBy("channel_id"),
				"body.limit":             SetBy("limit"),
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
				limit := input.Limit
				switch {
				case strings.TrimSpace(input.Term) == "":
					return nil, Users{}, fmt.Errorf("give a term to search for")
				case limit == 0:
					limit = defaultUsersPerSearch
				case limit < 0 || limit > maxUsersPerSearch:
					return nil, Users{}, fmt.Errorf("limit must be between 1 and %d, not %d", maxUsersPerSearch, limit)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Users{}, err
				}
				found, err := client.SearchUsers(ctx, &model.UserSearch{
					Term:        strings.TrimPrefix(input.Term, "@"),
					TeamId:      input.TeamID,
					InChannelId: input.ChannelID,
					Limit:       limit,
				})
				if err != nil {
					return nil, Users{}, err
				}
				out := Users{Users: []UserSummary{}}
				for _, user := range found {
					out.Users = append(out.Users, summarise(user))
				}
				return nil, out, nil
			}
		},
	)
}
