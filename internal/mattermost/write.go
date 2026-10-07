package mattermost

import (
	"context"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// The writes the tools make, each behind the person's confirmation (ADR-021).
// Each is one Client4 call, and an operation a tool declares in its Uses.

// CreatePost posts a message in a channel, as a reply in rootID's thread when
// it is set, with the files uploaded for it. CreatePost.
func (c *Client) CreatePost(ctx context.Context, channelID, rootID, message string, fileIDs []string) (*model.Post, error) {
	return result(c.api.CreatePost(ctx, &model.Post{ChannelId: channelID, RootId: rootID, Message: message, FileIds: fileIDs}))
}

// Upload uploads a file to a channel, to be attached to a post there, and
// returns its id. UploadFile.
func (c *Client) Upload(ctx context.Context, channelID, name string, data []byte) (string, error) {
	uploaded, err := result(c.api.UploadFile(ctx, data, channelID, name))
	if err != nil {
		return "", err
	}
	if len(uploaded.FileInfos) != 1 {
		return "", fmt.Errorf("uploading %s: Mattermost answered with %d files, not 1", name, len(uploaded.FileInfos))
	}
	return uploaded.FileInfos[0].Id, nil
}

// React adds the user's reaction to a post. SaveReaction.
func (c *Client) React(ctx context.Context, userID, postID, emojiName string) (*model.Reaction, error) {
	return result(c.api.SaveReaction(ctx, &model.Reaction{UserId: userID, PostId: postID, EmojiName: emojiName}))
}

// EditPost replaces a post's text. PatchPost.
func (c *Client) EditPost(ctx context.Context, postID, message string) (*model.Post, error) {
	return result(c.api.PatchPost(ctx, postID, &model.PostPatch{Message: &message}))
}

// DeletePost deletes a post, and its replies when it starts a thread.
// DeletePost.
func (c *Client) DeletePost(ctx context.Context, postID string) error {
	return done(c.api.DeletePost(ctx, postID))
}

// Unreact removes the user's reaction from a post. DeleteReaction.
func (c *Client) Unreact(ctx context.Context, userID, postID, emojiName string) error {
	return done(c.api.DeleteReaction(ctx, &model.Reaction{UserId: userID, PostId: postID, EmojiName: emojiName}))
}

// Pin pins a post to its channel, or unpins it. PinPost, UnpinPost.
func (c *Client) Pin(ctx context.Context, postID string, pinned bool) error {
	if pinned {
		return done(c.api.PinPost(ctx, postID))
	}
	return done(c.api.UnpinPost(ctx, postID))
}

// Typing shows the user typing in a channel, or in a thread when rootID is
// set, for the few seconds clients show it. PublishUserTyping.
func (c *Client) Typing(ctx context.Context, userID, channelID, rootID string) error {
	return done(c.api.PublishUserTyping(ctx, userID, model.TypingRequest{ChannelId: channelID, ParentId: rootID}))
}

// Follow follows a thread, or stops following it. StartFollowingThread,
// StopFollowingThread.
func (c *Client) Follow(ctx context.Context, userID, teamID, rootID string, following bool) error {
	return done(c.api.UpdateThreadFollowForUser(ctx, userID, teamID, rootID, following))
}

// Save saves a post for the user, or unsaves it; Mattermost keeps saved posts
// as the user's flagged_post preferences. UpdatePreferences,
// DeletePreferences.
func (c *Client) Save(ctx context.Context, userID, postID string, saved bool) error {
	preference := model.Preferences{{UserId: userID, Category: model.PreferenceCategoryFlaggedPost, Name: postID, Value: "true"}}
	if saved {
		return done(c.api.UpdatePreferences(ctx, userID, preference))
	}
	return done(c.api.DeletePreferences(ctx, userID, preference))
}

// Draft writes the user's draft in a channel, or in a thread when rootID is
// set, replacing the one there. UpsertDraft.
func (c *Client) Draft(ctx context.Context, userID, channelID, rootID, message string) (*model.Draft, error) {
	return result(c.api.UpsertDraft(ctx, &model.Draft{UserId: userID, ChannelId: channelID, RootId: rootID, Message: message}))
}

// done is result for a call that answers with nothing but whether it worked.
func done(response *model.Response, err error) error {
	if err != nil {
		return translate(response, err)
	}
	return nil
}
