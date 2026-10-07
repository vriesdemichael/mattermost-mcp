package mattermost

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// The reads the tools make. Each is one Client4 call, and each is an operation
// a tool declares in its Uses (ADR-028); the live suite checks the pairing.

// me is how Mattermost names the user a credential belongs to in a path.
const me = "me"

// result returns value, or the call's failure as translate reads it.
func result[T any](value T, response *model.Response, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, translate(response, err)
	}
	return value, nil
}

// Teams is every team the identity belongs to. GetTeamsForUser.
func (c *Client) Teams(ctx context.Context) ([]*model.Team, error) {
	return result(c.api.GetTeamsForUser(ctx, me, ""))
}

// Channels is every channel the identity belongs to, in every team, direct and
// group messages included, without archived ones. GetChannelsForUser.
func (c *Client) Channels(ctx context.Context) ([]*model.Channel, error) {
	return result(c.api.GetChannelsForUserWithLastDeleteAt(ctx, me, 0))
}

// ChannelMemberships is the identity's membership of each channel it belongs
// to in a team, which holds what it has read there. GetChannelMembersForUser.
func (c *Client) ChannelMemberships(ctx context.Context, teamID string) (model.ChannelMembers, error) {
	return result(c.api.GetChannelMembersForUser(ctx, me, teamID, ""))
}

// PostsPage asks for a channel's posts: the newest perPage, or perPage before
// or after a post. GetPostsForChannel.
type PostsPage struct {
	ChannelID string
	PerPage   int
	Before    string
	After     string
}

// Posts is a page of a channel's posts, newest first in the list's order.
func (c *Client) Posts(ctx context.Context, page PostsPage) (*model.PostList, error) {
	switch {
	case page.Before != "":
		return result(c.api.GetPostsBefore(ctx, page.ChannelID, page.Before, 0, page.PerPage, "", false, false))
	case page.After != "":
		return result(c.api.GetPostsAfter(ctx, page.ChannelID, page.After, 0, page.PerPage, "", false, false))
	default:
		return result(c.api.GetPostsForChannel(ctx, page.ChannelID, 0, page.PerPage, "", false, false))
	}
}

// Thread is a post's whole thread: its root and every reply. GetPostThread.
func (c *Client) Thread(ctx context.Context, postID string) (*model.PostList, error) {
	return result(c.api.GetPostThread(ctx, postID, "", false))
}

// Users is the users with the given ids. GetUsersByIds.
func (c *Client) Users(ctx context.Context, ids []string) ([]*model.User, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return result(c.api.GetUsersByIds(ctx, ids))
}

// User is the user with the given id. GetUser.
func (c *Client) User(ctx context.Context, id string) (*model.User, error) {
	return result(c.api.GetUser(ctx, id, ""))
}

// UserByUsername is the user with the given username. GetUserByUsername.
func (c *Client) UserByUsername(ctx context.Context, username string) (*model.User, error) {
	return result(c.api.GetUserByUsername(ctx, username, ""))
}

// SearchUsers is the users a search finds. SearchUsers.
func (c *Client) SearchUsers(ctx context.Context, search *model.UserSearch) ([]*model.User, error) {
	return result(c.api.SearchUsers(ctx, search))
}
