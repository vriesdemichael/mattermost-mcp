package server

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// The tools that change what other people see. Each is offered only when
// writes are allowed, and each call asks the person first, showing what will
// change, where and under whose name (ADR-021).

// writes is the annotation of a tool that adds to Mattermost without removing
// or overwriting anything. idempotent says whether repeating a call changes
// nothing more.
func writes(title string, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    false,
		DestructiveHint: ptr(false),
		IdempotentHint:  idempotent,
		OpenWorldHint:   ptr(false),
	}
}

// overwrites is the annotation of a tool that replaces or removes something in
// Mattermost.
func overwrites(title string, idempotent bool) *mcp.ToolAnnotations {
	annotations := writes(title, idempotent)
	annotations.DestructiveHint = ptr(true)
	return annotations
}

// excerptLength is how much of someone's post a confirmation quotes, in
// characters, to say which post it means.
const excerptLength = 120

// excerpt is the start of a post's text, on one line.
func excerpt(message string) string {
	line := strings.Join(strings.Fields(message), " ")
	if utf8.RuneCountInString(line) <= excerptLength {
		return line
	}
	return string([]rune(line)[:excerptLength]) + "…"
}

// place says where a channel is, as the person knows it: a channel by its name,
// a direct message by the person on the other side.
func place(channel *model.Channel, self string, names map[string]string) string {
	switch channel.Type {
	case model.ChannelTypeDirect:
		other := otherInDirect(channel, self)
		switch {
		case other == self:
			return "your direct message to yourself"
		case names[other] != "":
			return "your direct message with @" + names[other]
		}
		return "a direct message"
	case model.ChannelTypeGroup:
		return "the group message with " + channel.DisplayName
	}
	return "~" + channel.DisplayName
}

// postInContext is a post with what a question about it names: the user, the
// post's author, and where it is.
type postInContext struct {
	post    *model.Post
	self    *model.User
	channel *model.Channel
	author  string
	in      string
}

// mine reports whether the post is the user's own.
func (p postInContext) mine() bool { return p.post.UserId == p.self.Id }

// readPostInContext reads a post with its author and channel.
// GetPost, GetUser, GetChannel, GetUsersByIds.
func readPostInContext(ctx context.Context, client *mattermost.Client, postID string) (postInContext, error) {
	post, err := client.Post(ctx, postID)
	if err != nil {
		return postInContext{}, err
	}
	self, err := client.Me(ctx)
	if err != nil {
		return postInContext{}, err
	}
	channel, err := client.Channel(ctx, post.ChannelId)
	if err != nil {
		return postInContext{}, err
	}
	names, err := usernames(ctx, client, []string{post.UserId, otherInDirect(channel, self.Id)})
	if err != nil {
		return postInContext{}, err
	}
	names[self.Id] = self.Username
	author := names[post.UserId]
	if author == "" {
		author = post.UserId
	}
	return postInContext{post: post, self: self, channel: channel, author: author, in: place(channel, self.Id, names)}, nil
}

// postContextUses are the operations readPostInContext calls, for a tool that
// reads a post given by its post_id argument.
func postContextUses() []Use {
	return []Use{
		{
			Operation: "GetPost",
			Params: map[string]Coverage{
				"post_id":         SetBy("post_id"),
				"include_deleted": Omitted("a deleted post is gone for the person too; nothing is done to it"),
			},
		},
		{
			Operation: "GetChannel",
			Params:    map[string]Coverage{"channel_id": Fixed("the post's channel", "the question says where the post is")},
		},
		{
			Operation: "GetUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "the change is made under the user's name, and the question says whose")},
		},
		{
			Operation: "GetUsersByIds",
			Params:    map[string]Coverage{"since": Omitted("the tool reads each username, whenever it changed")},
		},
	}
}

// postMessageInput is a post, or a reply.
type postMessageInput struct {
	ChannelID string       `json:"channel_id,omitempty" jsonschema:"the channel to post in, as list_channels gives it"`
	ToUser    string       `json:"to_user,omitempty" jsonschema:"post in the direct message with this username instead, opening it when there is none yet"`
	Message   string       `json:"message" jsonschema:"the text to post, in Mattermost Markdown"`
	RootID    string       `json:"root_id,omitempty" jsonschema:"reply in the thread of this post: the post that started it or any reply in it"`
	Files     []attachFile `json:"files,omitempty" jsonschema:"files to attach, at most 10: from a full path on this machine when the server runs on it, or text written for the post with a name"`
}

// replyTarget is where a post goes: its channel, and the thread's first post
// when it is a reply.
type replyTarget struct {
	channelID string
	root      *model.Post
}

// rootID is the id of the thread's first post, or empty for a post that starts
// one.
func (t replyTarget) rootID() string {
	if t.root == nil {
		return ""
	}
	return t.root.Id
}

// target reads where a post goes: a channel, a direct message with a user, or
// a thread. A reply goes in the thread's first post's channel, and Mattermost
// takes only a thread's first post as a reply's root, so a reply to a reply
// goes in the same thread. A direct message is opened when there is none yet,
// which sends nothing and shows nobody anything.
func target(ctx context.Context, client *mattermost.Client, channelID, toUser, rootID string) (replyTarget, error) {
	switch {
	case toUser != "" && (channelID != "" || rootID != ""):
		return replyTarget{}, fmt.Errorf("give to_user alone: it names the direct message, which channel_id and root_id would name again")
	case toUser != "":
		other, err := client.UserByUsername(ctx, strings.TrimPrefix(toUser, "@"))
		if err != nil {
			return replyTarget{}, err
		}
		self, err := client.Me(ctx)
		if err != nil {
			return replyTarget{}, err
		}
		direct, err := client.DirectChannel(ctx, self.Id, other.Id)
		if err != nil {
			return replyTarget{}, err
		}
		return replyTarget{channelID: direct.Id}, nil
	case rootID == "" && channelID == "":
		return replyTarget{}, fmt.Errorf("give channel_id to post in a channel, to_user for a direct message, or root_id to reply in a thread")
	case rootID == "":
		return replyTarget{channelID: channelID}, nil
	}
	root, err := client.Post(ctx, rootID)
	if err != nil {
		return replyTarget{}, err
	}
	if root.RootId != "" {
		if root, err = client.Post(ctx, root.RootId); err != nil {
			return replyTarget{}, err
		}
	}
	if channelID != "" && channelID != root.ChannelId {
		return replyTarget{}, fmt.Errorf("post %s is in channel %s, not %s; leave channel_id out to reply in its thread", rootID, root.ChannelId, channelID)
	}
	return replyTarget{channelID: root.ChannelId, root: root}, nil
}

// targetUses are the operations target calls, besides GetPost, which each tool
// declares with its own reason.
func targetUses() []Use {
	return []Use{
		{
			Operation: "GetUserByUsername",
			Params:    map[string]Coverage{"username": SetBy("to_user")},
		},
		{
			Operation: "CreateDirectChannel",
			Params:    map[string]Coverage{},
		},
	}
}

func postMessageSpec() Spec {
	return configuredToolSpec(
		&mcp.Tool{
			Name: "post_message",
			Description: "Post a message under the user's name: in a channel with channel_id, in a direct message with to_user, " +
				"or as a reply in a thread with root_id, with files attached if asked. The person is asked to confirm each post, seeing " +
				"the message, where it goes and every file it carries, before anything is sent or uploaded; " +
				"if they decline or close the question, do not post it again unless they ask. " +
				"Posting stops the typing indicator the typing tool showed there.",
			Annotations: writes("Post message", false),
		},
		append([]Use{
			{
				Operation: "CreatePost",
				Params: map[string]Coverage{
					"body.channel_id": SetBy("channel_id"),
					"body.message":    SetBy("message"),
					"body.root_id":    SetBy("root_id"),
					"body.file_ids":   SetBy("files"),
					"body.props":      Omitted("props carry integrations' attachments and Mattermost's own settings for a post; a person's post is its text"),
					"body.metadata":   Omitted("metadata such as a post's priority is a Mattermost feature beyond posting text, which the person is not shown"),
					"set_online":      Omitted("Mattermost's default marks the user online when they post, as its own client does"),
					"silent":          Omitted("a silent post notifies nobody it mentions, which the person would have to be told about when confirming"),
				},
				Releases: "11.7 has no silent parameter, which the tool never sends",
			},
			{
				Operation: "UploadFile",
				Params: map[string]Coverage{
					"body.channel_id": SetBy("channel_id"),
					"body.files":      SetBy("files"),
					"body.client_ids": Omitted("ids a client gives uploads it sends at once, to match them up; the tool uploads one file at a time"),
					"channel_id":      Omitted("Mattermost's client sends the channel as the form's channel_id field instead"),
					"filename":        Omitted("the file's name travels with its part of the form"),
				},
			},
			{
				Operation: "GetChannel",
				Params:    map[string]Coverage{"channel_id": SetBy("channel_id")},
			},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("root_id"),
					"include_deleted": Omitted("a deleted post has no thread to reply in"),
				},
			},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the confirmation names the user the post appears under")},
			},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each username, whenever it changed")},
			},
		}, targetUses()...),
		func(clientFor ClientFor, cfg config.Config) mcp.ToolHandlerFor[postMessageInput, Post] {
			bind := func(input postMessageInput) (string, error) {
				files, err := loadAttachments(cfg, input.Files)
				if err != nil {
					return "", err
				}
				return fingerprint(files), nil
			}
			ask := askingBound("post_message", bind, postConfirmation(clientFor, cfg),
				func(ctx context.Context, request *mcp.CallToolRequest, input postMessageInput) (*mcp.CallToolResult, Post, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Post{}, err
					}
					where, err := target(ctx, client, input.ChannelID, input.ToUser, input.RootID)
					if err != nil {
						return nil, Post{}, err
					}
					files, err := loadAttachments(cfg, input.Files)
					if err != nil {
						return nil, Post{}, err
					}
					fileIDs := make([]string, 0, len(files))
					for _, file := range files {
						id, err := client.Upload(ctx, where.channelID, file.name, file.data)
						if err != nil {
							return nil, Post{}, err
						}
						fileIDs = append(fileIDs, id)
					}
					created, err := client.CreatePost(ctx, where.channelID, where.rootID(), input.Message, fileIDs)
					if err != nil {
						return nil, Post{}, err
					}
					stopTyping(created.ChannelId, created.RootId)
					posted, err := withAuthors(ctx, client, []*model.Post{created})
					if err != nil {
						return nil, Post{}, err
					}
					return nil, posted[0], nil
				})
			return func(ctx context.Context, request *mcp.CallToolRequest, input postMessageInput) (*mcp.CallToolResult, Post, error) {
				result, posted, err := ask(ctx, request, input)
				// The indicator stays while the person is being asked, which is
				// when they are finishing the message, and goes once they have
				// answered either way.
				if result == nil || result.RequestState == "" {
					stopTyping(input.ChannelID, input.RootID)
				}
				return result, posted, err
			}
		},
	)
}

// postConfirmation asks to post: as whom, where, in reply to what, the
// message as it will be sent, and every file it carries.
func postConfirmation(clientFor ClientFor, cfg config.Config) func(context.Context, *mcp.CallToolRequest, postMessageInput) (confirmation, error) {
	return func(ctx context.Context, request *mcp.CallToolRequest, input postMessageInput) (confirmation, error) {
		if strings.TrimSpace(input.Message) == "" && len(input.Files) == 0 {
			return confirmation{}, fmt.Errorf("the message is empty")
		}
		files, err := loadAttachments(cfg, input.Files)
		if err != nil {
			return confirmation{}, err
		}
		attached := describeAttachments(files)
		carrying := ""
		if len(files) > 0 {
			carrying = fmt.Sprintf(" with %d %s", len(files), plural(int64(len(files)), "file", "files"))
		}
		client, err := clientFor(ctx, request)
		if err != nil {
			return confirmation{}, err
		}
		where, err := target(ctx, client, input.ChannelID, input.ToUser, input.RootID)
		if err != nil {
			return confirmation{}, err
		}
		self, err := client.Me(ctx)
		if err != nil {
			return confirmation{}, err
		}
		channel, err := client.Channel(ctx, where.channelID)
		if err != nil {
			return confirmation{}, err
		}
		wanted := []string{otherInDirect(channel, self.Id)}
		if where.root != nil {
			wanted = append(wanted, where.root.UserId)
		}
		names, err := usernames(ctx, client, wanted)
		if err != nil {
			return confirmation{}, err
		}
		names[self.Id] = self.Username
		in := place(channel, self.Id, names)
		if where.root == nil {
			return confirmation{
				Message: fmt.Sprintf("Post as @%s in %s:\n\n%s%s", self.Username, in, input.Message, attached),
				Label:   "Post this message" + carrying + " in " + in,
			}, nil
		}
		author := names[where.root.UserId]
		if author == "" {
			author = where.root.UserId
		}
		return confirmation{
			Message: fmt.Sprintf("Reply as @%s in %s, in the thread @%s started with:\n“%s”\n\nThe reply:\n\n%s%s",
				self.Username, in, author, excerpt(where.root.Message), input.Message, attached),
			Label: "Post this reply" + carrying + " in @" + author + "'s thread",
		}, nil
	}
}

type editPostInput struct {
	PostID  string `json:"post_id" jsonschema:"the post to edit, which must be the user's own"`
	Message string `json:"message" jsonschema:"the post's new text in full, in Mattermost Markdown; it replaces the old text"`
}

func editPostSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "edit_post",
			Description: "Replace the text of one of the user's own posts. Mattermost marks the post as edited. The person is asked to " +
				"confirm each edit, seeing the old text and the new; if they decline or close the question, do not edit it again unless they ask.",
			Annotations: overwrites("Edit post", true),
		},
		append([]Use{{
			Operation: "PatchPost",
			Params: map[string]Coverage{
				"post_id":            SetBy("post_id"),
				"body.message":       SetBy("message"),
				"body.file_ids":      Omitted("an edit changes the text; the attachments stay as they are"),
				"body.has_reactions": Omitted("Mattermost keeps it in step with the reactions itself"),
				"body.is_pinned":     Omitted("pin_post pins and unpins, asking about that on its own"),
				"body.props":         Omitted("props carry integrations' attachments and Mattermost's own settings for a post; an edit changes the text"),
			},
		}}, postContextUses()...),
		func(clientFor ClientFor) mcp.ToolHandlerFor[editPostInput, Post] {
			return asking("edit_post",
				func(ctx context.Context, request *mcp.CallToolRequest, input editPostInput) (confirmation, error) {
					if strings.TrimSpace(input.Message) == "" {
						return confirmation{}, fmt.Errorf("the new text is empty; delete_post removes a post")
					}
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					p, err := ownPost(ctx, client, input.PostID, "edit")
					if err != nil {
						return confirmation{}, err
					}
					if p.post.Message == input.Message {
						return confirmation{}, fmt.Errorf("the post already reads exactly so; there is nothing to edit")
					}
					return confirmation{
						Message: fmt.Sprintf("Edit your post in %s as @%s.\n\nIt reads now:\n\n%s\n\nIt will read:\n\n%s",
							p.in, p.self.Username, p.post.Message, input.Message),
						Label: "Replace the text of your post in " + p.in,
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input editPostInput) (*mcp.CallToolResult, Post, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Post{}, err
					}
					if _, err := ownPost(ctx, client, input.PostID, "edit"); err != nil {
						return nil, Post{}, err
					}
					edited, err := client.EditPost(ctx, input.PostID, input.Message)
					if err != nil {
						return nil, Post{}, err
					}
					posts, err := withAuthors(ctx, client, []*model.Post{edited})
					if err != nil {
						return nil, Post{}, err
					}
					return nil, posts[0], nil
				})
		},
	)
}

// ownPost reads a post the user is about to change, refusing one that is not
// theirs: an administrator's credential could otherwise change a colleague's
// words under the colleague's name.
func ownPost(ctx context.Context, client *mattermost.Client, postID, verb string) (postInContext, error) {
	p, err := readPostInContext(ctx, client, postID)
	if err != nil {
		return postInContext{}, err
	}
	if !p.mine() {
		return postInContext{}, fmt.Errorf("post %s is @%s's, and the tool does not %s another person's post", postID, p.author, verb)
	}
	return p, nil
}

// Deleted is what delete_post removed.
type Deleted struct {
	PostID string `json:"post_id"`
	// Replies is how many replies went with the post, which Mattermost deletes
	// with the post that started their thread.
	Replies int64 `json:"replies" jsonschema:"how many replies were deleted with it, when it started a thread"`
}

type deletePostInput struct {
	PostID string `json:"post_id" jsonschema:"the post to delete, which must be the user's own; deleting the post that starts a thread deletes its replies too"`
}

func deletePostSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "delete_post",
			Description: "Delete one of the user's own posts. Deleting the post that starts a thread deletes every reply in it, " +
				"other people's included. The person is asked to confirm each deletion, seeing the post and how many replies go with it; " +
				"if they decline or close the question, do not delete it again unless they ask.",
			Annotations: overwrites("Delete post", true),
		},
		append([]Use{{
			Operation: "DeletePost",
			Params:    map[string]Coverage{"post_id": SetBy("post_id")},
		}}, postContextUses()...),
		func(clientFor ClientFor) mcp.ToolHandlerFor[deletePostInput, Deleted] {
			return asking("delete_post",
				func(ctx context.Context, request *mcp.CallToolRequest, input deletePostInput) (confirmation, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					p, err := ownPost(ctx, client, input.PostID, "delete")
					if err != nil {
						return confirmation{}, err
					}
					message := fmt.Sprintf("Delete your post in %s:\n“%s”", p.in, excerpt(p.post.Message))
					label := "Delete your post in " + p.in
					if p.post.RootId == "" && p.post.ReplyCount > 0 {
						message += fmt.Sprintf("\n\nIt starts a thread, and its %d %s, other people's included, will be deleted with it.",
							p.post.ReplyCount, plural(p.post.ReplyCount, "reply", "replies"))
						label = fmt.Sprintf("Delete your post and the %d %s in its thread", p.post.ReplyCount, plural(p.post.ReplyCount, "reply", "replies"))
					}
					return confirmation{Message: message, Label: label}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input deletePostInput) (*mcp.CallToolResult, Deleted, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Deleted{}, err
					}
					p, err := ownPost(ctx, client, input.PostID, "delete")
					if err != nil {
						return nil, Deleted{}, err
					}
					if err := client.DeletePost(ctx, input.PostID); err != nil {
						return nil, Deleted{}, err
					}
					replies := int64(0)
					if p.post.RootId == "" {
						replies = p.post.ReplyCount
					}
					return nil, Deleted{PostID: input.PostID, Replies: replies}, nil
				})
		},
	)
}

func plural(n int64, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Reaction is a reaction the user added or removed.
type Reaction struct {
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
}

type reactionInput struct {
	PostID    string `json:"post_id" jsonschema:"the post to react to"`
	EmojiName string `json:"emoji_name" jsonschema:"the emoji's name, such as thumbsup, white_check_mark or eyes, with or without the colons"`
}

// emojiName is an emoji's name as Mattermost stores it, without the colons a
// message writes around it.
func emojiName(name string) string {
	return strings.Trim(strings.TrimSpace(name), ":")
}

// reacted reports whether the user reacted to the post with the emoji.
func reacted(p postInContext, emoji string) bool {
	if p.post.Metadata == nil {
		return false
	}
	for _, reaction := range p.post.Metadata.Reactions {
		if reaction.UserId == p.self.Id && reaction.EmojiName == emoji {
			return true
		}
	}
	return false
}

func addReactionSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "add_reaction",
			Description: "React to a post with an emoji, under the user's name. The person is asked to confirm each reaction, " +
				"seeing the emoji and the post, before it is added; if they decline or close the question, do not react again unless they ask.",
			Annotations: writes("Add reaction", true),
		},
		append([]Use{{
			Operation: "SaveReaction",
			Params: map[string]Coverage{
				"body.post_id":    SetBy("post_id"),
				"body.emoji_name": SetBy("emoji_name"),
				"body.user_id":    Fixed("the user's own id", "a user reacts as themselves; Mattermost refuses a reaction for anyone else"),
				"body.create_at":  Omitted("Mattermost records when the reaction was made"),
			},
		}}, postContextUses()...),
		func(clientFor ClientFor) mcp.ToolHandlerFor[reactionInput, Reaction] {
			return asking("add_reaction",
				reactionConfirmation(clientFor, func(p postInContext, emoji string) (confirmation, error) {
					return confirmation{
						Message: fmt.Sprintf("React as @%s with :%s: to @%s's post in %s:\n“%s”",
							p.self.Username, emoji, p.author, p.in, excerpt(p.post.Message)),
						Label: fmt.Sprintf("Add :%s: to @%s's post", emoji, p.author),
					}, nil
				}),
				func(ctx context.Context, request *mcp.CallToolRequest, input reactionInput) (*mcp.CallToolResult, Reaction, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Reaction{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return nil, Reaction{}, err
					}
					reaction, err := client.React(ctx, self.Id, input.PostID, emojiName(input.EmojiName))
					if err != nil {
						return nil, Reaction{}, err
					}
					return nil, Reaction{PostID: reaction.PostId, EmojiName: reaction.EmojiName}, nil
				})
		},
	)
}

func removeReactionSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "remove_reaction",
			Description: "Take back one of the user's reactions to a post. The person is asked to confirm, seeing the emoji and the post; " +
				"if they decline or close the question, do not try again unless they ask.",
			Annotations: overwrites("Remove reaction", true),
		},
		append([]Use{{
			Operation: "DeleteReaction",
			Params: map[string]Coverage{
				"user_id":    Fixed("me", "a user takes back only their own reaction"),
				"post_id":    SetBy("post_id"),
				"emoji_name": SetBy("emoji_name"),
			},
		}}, postContextUses()...),
		func(clientFor ClientFor) mcp.ToolHandlerFor[reactionInput, Reaction] {
			return asking("remove_reaction",
				reactionConfirmation(clientFor, func(p postInContext, emoji string) (confirmation, error) {
					if !reacted(p, emoji) {
						return confirmation{}, fmt.Errorf("@%s has not reacted with :%s: to that post", p.self.Username, emoji)
					}
					return confirmation{
						Message: fmt.Sprintf("Take back your :%s: on @%s's post in %s:\n“%s”", emoji, p.author, p.in, excerpt(p.post.Message)),
						Label:   fmt.Sprintf("Remove your :%s: from @%s's post", emoji, p.author),
					}, nil
				}),
				func(ctx context.Context, request *mcp.CallToolRequest, input reactionInput) (*mcp.CallToolResult, Reaction, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Reaction{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return nil, Reaction{}, err
					}
					emoji := emojiName(input.EmojiName)
					if err := client.Unreact(ctx, self.Id, input.PostID, emoji); err != nil {
						return nil, Reaction{}, err
					}
					return nil, Reaction{PostID: input.PostID, EmojiName: emoji}, nil
				})
		},
	)
}

// reactionConfirmation reads the post a reaction is about and asks what ask
// builds from it.
func reactionConfirmation(clientFor ClientFor, ask func(postInContext, string) (confirmation, error)) func(context.Context, *mcp.CallToolRequest, reactionInput) (confirmation, error) {
	return func(ctx context.Context, request *mcp.CallToolRequest, input reactionInput) (confirmation, error) {
		emoji := emojiName(input.EmojiName)
		if emoji == "" {
			return confirmation{}, fmt.Errorf("emoji_name is empty")
		}
		client, err := clientFor(ctx, request)
		if err != nil {
			return confirmation{}, err
		}
		p, err := readPostInContext(ctx, client, input.PostID)
		if err != nil {
			return confirmation{}, err
		}
		return ask(p, emoji)
	}
}

// Pinned is a post's pinned state after pin_post.
type Pinned struct {
	PostID string `json:"post_id"`
	Pinned bool   `json:"pinned"`
}

type pinPostInput struct {
	PostID string `json:"post_id" jsonschema:"the post to pin to its channel"`
	Pinned *bool  `json:"pinned,omitempty" jsonschema:"false unpins the post; true, the default, pins it"`
}

func pinPostSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name: "pin_post",
			Description: "Pin a post to its channel, where everyone in the channel sees it among the pinned messages, or unpin it with " +
				"pinned false. The person is asked to confirm, seeing the post; if they decline or close the question, do not try again unless they ask.",
			Annotations: writes("Pin post", true),
		},
		append([]Use{
			{Operation: "PinPost", Params: map[string]Coverage{"post_id": SetBy("post_id")}},
			{Operation: "UnpinPost", Params: map[string]Coverage{"post_id": SetBy("post_id")}},
		}, postContextUses()...),
		func(clientFor ClientFor) mcp.ToolHandlerFor[pinPostInput, Pinned] {
			return asking("pin_post",
				func(ctx context.Context, request *mcp.CallToolRequest, input pinPostInput) (confirmation, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					p, err := readPostInContext(ctx, client, input.PostID)
					if err != nil {
						return confirmation{}, err
					}
					verb := "Pin"
					if !pinning(input.Pinned) {
						verb = "Unpin"
					}
					return confirmation{
						Message: fmt.Sprintf("%s @%s's post in %s, as @%s:\n“%s”", verb, p.author, p.in, p.self.Username, excerpt(p.post.Message)),
						Label:   fmt.Sprintf("%s @%s's post in %s", verb, p.author, p.in),
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input pinPostInput) (*mcp.CallToolResult, Pinned, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Pinned{}, err
					}
					if err := client.Pin(ctx, input.PostID, pinning(input.Pinned)); err != nil {
						return nil, Pinned{}, err
					}
					return nil, Pinned{PostID: input.PostID, Pinned: pinning(input.Pinned)}, nil
				})
		},
	), map[string]string{"pinned": "chooses between PinPost and UnpinPost"})
}

// pinning is a pinned argument, true when left out.
func pinning(pinned *bool) bool { return pinned == nil || *pinned }
