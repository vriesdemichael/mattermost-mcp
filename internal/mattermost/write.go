package mattermost

import (
	"context"

	"github.com/mattermost/mattermost/server/public/model"
)

// The writes the tools make, each behind the person's confirmation (ADR-021).
// Each is one Client4 call, and an operation a tool declares in its Uses.

// CreatePost posts a message in a channel, as a reply in rootID's thread when
// it is set. CreatePost.
func (c *Client) CreatePost(ctx context.Context, channelID, rootID, message string) (*model.Post, error) {
	return result(c.api.CreatePost(ctx, &model.Post{ChannelId: channelID, RootId: rootID, Message: message}))
}

// React adds the user's reaction to a post. SaveReaction.
func (c *Client) React(ctx context.Context, userID, postID, emojiName string) (*model.Reaction, error) {
	return result(c.api.SaveReaction(ctx, &model.Reaction{UserId: userID, PostId: postID, EmojiName: emojiName}))
}
