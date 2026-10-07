package mattermost

import (
	"context"
	"net/http"

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

// ThreadPage asks for a stretch of a thread: perPage posts, oldest first, from
// the start or after a post and its time. GetPostThread.
type ThreadPage struct {
	PostID       string
	PerPage      int
	FromPost     string
	FromCreateAt int64
}

// Thread is a stretch of a post's thread, its first post and replies, with
// whether more follow in the list's HasNext.
func (c *Client) Thread(ctx context.Context, page ThreadPage) (*model.PostList, error) {
	return result(c.api.GetPostThreadWithOpts(ctx, page.PostID, "", model.GetPostsOptions{
		PerPage: page.PerPage, FromPost: page.FromPost, FromCreateAt: page.FromCreateAt, Direction: "down",
	}))
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

// SavedPosts is up to perPage of the posts the user saved, newest first,
// skipping the first offset of them. Mattermost's getFlaggedPostsForUser
// hands its page parameter to the database as an offset in posts, not as a
// page number, so the offset goes where the page would. GetFlaggedPostsForUser.
func (c *Client) SavedPosts(ctx context.Context, offset, perPage int) (*model.PostList, error) {
	return result(c.api.GetFlaggedPostsForUser(ctx, me, offset, perPage))
}

// Threads is a page of the threads the user follows in a team, direct and
// group messages included, most recently active first, with who took part:
// the first page, or the one after the thread before names. GetUserThreads.
func (c *Client) Threads(ctx context.Context, userID, teamID string, perPage uint64, before string) (*model.Threads, error) {
	return result(c.api.GetUserThreads(ctx, userID, teamID, model.GetUserThreadsOpts{PageSize: perPage, Extended: true, Before: before}))
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

// Team is one team, by id. GetTeam.
func (c *Client) Team(ctx context.Context, id string) (*model.Team, error) {
	return result(c.api.GetTeam(ctx, id, ""))
}

// PostsSince is every post in a channel created, edited or deleted after a
// time, in milliseconds. GetPostsForChannel, with since.
func (c *Client) PostsSince(ctx context.Context, channelID string, since int64, collapsed bool) (*model.PostList, error) {
	return result(c.api.GetPostsSince(ctx, channelID, since, collapsed))
}

// TeamByName is the team with the given address name. GetTeamByName.
func (c *Client) TeamByName(ctx context.Context, name string) (*model.Team, error) {
	return result(c.api.GetTeamByName(ctx, name, ""))
}

// SearchChannels is a team's public channels whose name or display name has
// a word starting with term. SearchChannels.
func (c *Client) SearchChannels(ctx context.Context, teamID, term string) ([]*model.Channel, error) {
	return result(c.api.SearchChannels(ctx, teamID, &model.ChannelSearch{Term: term}))
}

// PublicChannels is a page of a team's public channels. GetPublicChannelsForTeam.
func (c *Client) PublicChannels(ctx context.Context, teamID string, page, perPage int) ([]*model.Channel, error) {
	return result(c.api.GetPublicChannelsForTeam(ctx, teamID, page, perPage, ""))
}

// ArchivedChannels is a page of a team's archived channels the user may see.
// GetDeletedChannelsForTeam.
func (c *Client) ArchivedChannels(ctx context.Context, teamID string, page, perPage int) ([]*model.Channel, error) {
	return result(c.api.GetDeletedChannelsForTeam(ctx, teamID, page, perPage, ""))
}

// ChannelStats is how many members, guests, pinned posts and files a channel
// has. GetChannelStats.
func (c *Client) ChannelStats(ctx context.Context, channelID string) (*model.ChannelStats, error) {
	return result(c.api.GetChannelStats(ctx, channelID, "", false))
}

// UserByEmail is the user with the given email address, when the identity
// may see addresses. GetUserByEmail.
func (c *Client) UserByEmail(ctx context.Context, email string) (*model.User, error) {
	return result(c.api.GetUserByEmail(ctx, email, ""))
}

// ClientConfig is the part of the server's configuration it tells every
// client, such as the longest message it takes. GetClientConfig.
func (c *Client) ClientConfig(ctx context.Context) (map[string]string, error) {
	return result(c.api.GetClientConfig(ctx, ""))
}

// GroupChannel is the group message between the given users, the user among
// them, created when it does not exist yet. CreateGroupChannel.
func (c *Client) GroupChannel(ctx context.Context, userIDs []string) (*model.Channel, error) {
	return result(c.api.CreateGroupChannel(ctx, userIDs))
}

// Emoji is the server's custom emoji with the given name. GetEmojiByName.
func (c *Client) Emoji(ctx context.Context, name string) (*model.Emoji, error) {
	return result(c.api.GetEmojiByName(ctx, name))
}

// AutocompleteEmoji is the server's custom emoji whose names start with
// prefix. AutocompleteEmoji.
func (c *Client) AutocompleteEmoji(ctx context.Context, prefix string) ([]*model.Emoji, error) {
	return result(c.api.AutocompleteEmoji(ctx, prefix, ""))
}

// Preference is one of the user's preferences, or nil when they never set it.
// Mattermost answers a request for one never set with the error it gives a
// failure, so the tool reads the category, which holds only those set and is
// not found when none is. GetPreferencesByCategory.
func (c *Client) Preference(ctx context.Context, userID, category, name string) (*model.Preference, error) {
	preferences, response, err := c.api.GetPreferencesByCategory(ctx, userID, category)
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, translate(response, err)
	}
	for i := range preferences {
		if preferences[i].Name == name {
			return &preferences[i], nil
		}
	}
	return nil, nil
}
