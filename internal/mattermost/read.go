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
	// Collapsed asks for the posts that start a thread and those that reply to
	// none, without the replies, as a channel reads with collapsed reply
	// threads.
	Collapsed bool
}

// Posts is a page of a channel's posts, newest first in the list's order.
func (c *Client) Posts(ctx context.Context, page PostsPage) (*model.PostList, error) {
	switch {
	case page.Before != "":
		return result(c.api.GetPostsBefore(ctx, page.ChannelID, page.Before, 0, page.PerPage, "", page.Collapsed, false))
	case page.After != "":
		return result(c.api.GetPostsAfter(ctx, page.ChannelID, page.After, 0, page.PerPage, "", page.Collapsed, false))
	default:
		return result(c.api.GetPostsForChannel(ctx, page.ChannelID, 0, page.PerPage, "", page.Collapsed, false))
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

// Search asks Mattermost's post search.
type Search struct {
	// TeamID limits the search to one team; empty searches every team.
	TeamID     string
	Terms      string
	MatchAny   bool
	Page       int
	PerPage    int
	TimeOffset int
}

// SearchPosts is the posts a search finds, in Mattermost's order. With a team,
// SearchPosts; without one, SearchPostsInAllTeams.
func (c *Client) SearchPosts(ctx context.Context, search Search) (*model.PostList, error) {
	return result(c.api.SearchPostsWithParams(ctx, search.TeamID, &model.SearchParameter{
		Terms:          &search.Terms,
		IsOrSearch:     &search.MatchAny,
		TimeZoneOffset: &search.TimeOffset,
		Page:           &search.Page,
		PerPage:        &search.PerPage,
	}))
}

// Channel is one channel, by id. GetChannel.
func (c *Client) Channel(ctx context.Context, id string) (*model.Channel, error) {
	return result(c.api.GetChannel(ctx, id))
}

// Post is one post, by id, unless it was deleted. GetPost.
func (c *Client) Post(ctx context.Context, id string) (*model.Post, error) {
	return result(c.api.GetPost(ctx, id, ""))
}

// PinnedPosts is a channel's pinned posts. GetPinnedPosts.
func (c *Client) PinnedPosts(ctx context.Context, channelID string) (*model.PostList, error) {
	return result(c.api.GetPinnedPosts(ctx, channelID, ""))
}

// SavedPosts is a page of the posts the user saved, newest first.
// GetFlaggedPostsForUser.
func (c *Client) SavedPosts(ctx context.Context, perPage int) (*model.PostList, error) {
	return result(c.api.GetFlaggedPostsForUser(ctx, me, 0, perPage))
}

// UnreadPosts is a channel's posts around where the user stopped reading: up
// to before of the posts they have read, and up to after of those they have
// not. Asking marks nothing read. GetPostsAroundLastUnread.
func (c *Client) UnreadPosts(ctx context.Context, userID, channelID string, before, after int) (*model.PostList, error) {
	return result(c.api.GetPostsAroundLastUnread(ctx, userID, channelID, before, after, false))
}

// Threads is a page of the threads the user follows in a team, direct and
// group messages included, most recently active first, with who took part.
// GetUserThreads.
func (c *Client) Threads(ctx context.Context, userID, teamID string, perPage uint64) (*model.Threads, error) {
	return result(c.api.GetUserThreads(ctx, userID, teamID, model.GetUserThreadsOpts{PageSize: perPage, Extended: true}))
}

// Statuses is the presence of the given users. GetUsersStatusesByIds.
func (c *Client) Statuses(ctx context.Context, ids []string) ([]*model.Status, error) {
	return result(c.api.GetUsersStatusesByIds(ctx, ids))
}

// DirectChannel is the direct message between two users, created when it does
// not exist yet; creating one sends nothing. CreateDirectChannel.
func (c *Client) DirectChannel(ctx context.Context, userID, otherID string) (*model.Channel, error) {
	return result(c.api.CreateDirectChannel(ctx, userID, otherID))
}

// Membership is the user's membership of a channel, which holds when they last
// read it. GetChannelMember.
func (c *Client) Membership(ctx context.Context, channelID, userID string) (*model.ChannelMember, error) {
	return result(c.api.GetChannelMember(ctx, channelID, userID, ""))
}

// UsersByUsernames is the users with the given usernames. GetUsersByUsernames.
func (c *Client) UsersByUsernames(ctx context.Context, usernames []string) ([]*model.User, error) {
	return result(c.api.GetUsersByUsernames(ctx, usernames))
}

// Drafts is the user's drafts in a team's channels, direct and group messages
// included. GetDrafts.
func (c *Client) Drafts(ctx context.Context, userID, teamID string) ([]*model.Draft, error) {
	return result(c.api.GetDrafts(ctx, userID, teamID))
}

// FileInfo is what Mattermost knows of an uploaded file: its name, size, type
// and the post it is attached to. GetFileInfo.
func (c *Client) FileInfo(ctx context.Context, id string) (*model.FileInfo, error) {
	return result(c.api.GetFileInfo(ctx, id))
}

// File is an uploaded file's bytes, held whole; read its FileInfo first to
// know how many that is. GetFile.
func (c *Client) File(ctx context.Context, id string) ([]byte, error) {
	return result(c.api.GetFile(ctx, id))
}

// FileSearch asks Mattermost's file search.
type FileSearch struct {
	// TeamID limits the search to one team; empty searches every team.
	TeamID   string
	Terms    string
	MatchAny bool
	PerPage  int
}

// SearchFiles is the files a search finds, newest first. SearchFiles.
func (c *Client) SearchFiles(ctx context.Context, search FileSearch) (*model.FileInfoList, error) {
	page, offset := 0, 0
	return result(c.api.SearchFilesWithParams(ctx, search.TeamID, &model.SearchParameter{
		Terms:          &search.Terms,
		IsOrSearch:     &search.MatchAny,
		TimeZoneOffset: &offset,
		Page:           &page,
		PerPage:        &search.PerPage,
	}))
}
