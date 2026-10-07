package server

import (
	"context"

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
