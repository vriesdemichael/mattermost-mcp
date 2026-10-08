package mattermost

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// The writes the tools make, each behind the person's confirmation (ADR-021).
// Each is one Client4 call, and an operation a tool declares in its Uses.

// NewPost is a post to create: a message in a channel, as a reply in RootID's
// thread when it is set, with the files uploaded for it and its properties.
type NewPost struct {
	ChannelID string
	RootID    string
	Message   string
	FileIDs   []string
	Props     model.StringInterface
}

// CreatePost posts a message. CreatePost.
func (c *Client) CreatePost(ctx context.Context, post NewPost) (*model.Post, error) {
	created := &model.Post{ChannelId: post.ChannelID, RootId: post.RootID, Message: post.Message, FileIds: post.FileIDs}
	if post.Props != nil {
		created.SetProps(post.Props)
	}
	return result(c.api.CreatePost(ctx, created))
}

// Upload uploads a file to a channel, to be attached to a post there, and
// returns its id. UploadFile.
func (c *Client) Upload(ctx context.Context, channelID, name string, data []byte) (string, error) {
	uploaded, err := result(c.transfer.UploadFile(ctx, data, channelID, name))
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

// EditPost replaces a post's text, and its properties when props is not nil.
// PatchPost.
func (c *Client) EditPost(ctx context.Context, postID, message string, props model.StringInterface) (*model.Post, error) {
	patch := &model.PostPatch{Message: &message}
	if props != nil {
		patch.Props = &props
	}
	return result(c.api.PatchPost(ctx, postID, patch))
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

// DeleteDraft deletes the user's draft in a channel, or in a thread when
// rootID is set. Client4's DeleteDraft drops the thread and would delete the
// channel's draft instead, so a thread's goes to its own route.
// DeleteDraft, DeleteDraftForThread.
func (c *Client) DeleteDraft(ctx context.Context, userID, channelID, rootID string) error {
	if rootID == "" {
		_, response, err := c.api.DeleteDraft(ctx, userID, channelID, "")
		return done(response, err)
	}
	r, err := c.api.DoAPIDelete(ctx, "/users/"+url.PathEscape(userID)+"/channels/"+url.PathEscape(channelID)+"/drafts/"+url.PathEscape(rootID))
	if r != nil {
		defer func() { _ = r.Body.Close() }()
	}
	return done(model.BuildResponse(r), err)
}

// Remind has Mattermost remind the user of a post at a time. SetPostReminder.
func (c *Client) Remind(ctx context.Context, userID, postID string, at time.Time) error {
	return done(c.api.SetPostReminder(ctx, &model.PostReminder{UserId: userID, PostId: postID, TargetTime: at.Unix()}))
}
