package server

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// The tools that write. Each is offered only when writes are allowed, and each
// call asks the person first, showing what will be written where and under
// whose name (ADR-021).

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

// postMessageInput is a post, or a reply.
type postMessageInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"the channel to post in, as list_channels gives it; a reply may leave it out"`
	Message   string `json:"message" jsonschema:"the text to post, in Mattermost Markdown"`
	RootID    string `json:"root_id,omitempty" jsonschema:"reply in the thread of this post: the post that started it or any reply in it"`
}

// replyTarget is where a post goes: its channel, and the thread's first post
// when it is a reply.
type replyTarget struct {
	channelID string
	root      *model.Post
}

// target reads where a post goes. A reply goes in the thread's first post's
// channel, and Mattermost takes only a thread's first post as a reply's root,
// so a reply to a reply goes in the same thread.
func target(ctx context.Context, client *mattermost.Client, input postMessageInput) (replyTarget, error) {
	if strings.TrimSpace(input.Message) == "" {
		return replyTarget{}, fmt.Errorf("the message is empty")
	}
	if input.RootID == "" {
		if input.ChannelID == "" {
			return replyTarget{}, fmt.Errorf("give channel_id to post in a channel, or root_id to reply in a thread")
		}
		return replyTarget{channelID: input.ChannelID}, nil
	}
	root, err := client.Post(ctx, input.RootID)
	if err != nil {
		return replyTarget{}, err
	}
	if root.RootId != "" {
		if root, err = client.Post(ctx, root.RootId); err != nil {
			return replyTarget{}, err
		}
	}
	if input.ChannelID != "" && input.ChannelID != root.ChannelId {
		return replyTarget{}, fmt.Errorf("post %s is in channel %s, not %s; leave channel_id out to reply in its thread", input.RootID, root.ChannelId, input.ChannelID)
	}
	return replyTarget{channelID: root.ChannelId, root: root}, nil
}

func postMessageSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "post_message",
			Description: "Post a message in a channel under the user's name, or reply in a thread with root_id. " +
				"The person is asked to confirm each post, seeing the message and where it goes, before it is sent; " +
				"if they decline or close the question, do not post it again unless they ask.",
			Annotations: writes("Post message", false),
		},
		[]Use{
			{
				Operation: "CreatePost",
				Params: map[string]Coverage{
					"body.channel_id": SetBy("channel_id"),
					"body.message":    SetBy("message"),
					"body.root_id":    SetBy("root_id"),
					"body.file_ids":   Omitted("files are uploaded and attached by a tool of their own, which mm-mcp does not have yet"),
					"body.props":      Omitted("props carry integrations' attachments and Mattermost's own settings for a post; a person's post is its text"),
					"body.metadata":   Omitted("metadata such as a post's priority is a Mattermost feature beyond posting text, which the person is not shown"),
					"set_online":      Omitted("Mattermost's default marks the user online when they post, as its own client does"),
					"silent":          Omitted("a silent post notifies nobody it mentions, which the person would have to be told about when confirming"),
				},
				Releases: "11.7 has no silent parameter, which the tool never sends",
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
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[postMessageInput, Post] {
			return asking("post_message", postConfirmation(clientFor),
				func(ctx context.Context, request *mcp.CallToolRequest, input postMessageInput) (*mcp.CallToolResult, Post, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Post{}, err
					}
					where, err := target(ctx, client, input)
					if err != nil {
						return nil, Post{}, err
					}
					rootID := ""
					if where.root != nil {
						rootID = where.root.Id
					}
					created, err := client.CreatePost(ctx, where.channelID, rootID, input.Message)
					if err != nil {
						return nil, Post{}, err
					}
					posted, err := withAuthors(ctx, client, []*model.Post{created})
					if err != nil {
						return nil, Post{}, err
					}
					return nil, posted[0], nil
				})
		},
	)
}

// postConfirmation asks to post: as whom, where, in reply to what, and the
// message as it will be sent.
func postConfirmation(clientFor ClientFor) func(context.Context, *mcp.CallToolRequest, postMessageInput) (confirmation, error) {
	return func(ctx context.Context, request *mcp.CallToolRequest, input postMessageInput) (confirmation, error) {
		client, err := clientFor(ctx, request)
		if err != nil {
			return confirmation{}, err
		}
		where, err := target(ctx, client, input)
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
				Message: fmt.Sprintf("Post as @%s in %s:\n\n%s", self.Username, in, input.Message),
				Label:   "Post this message in " + in,
			}, nil
		}
		author := names[where.root.UserId]
		if author == "" {
			author = where.root.UserId
		}
		return confirmation{
			Message: fmt.Sprintf("Reply as @%s in %s, in the thread @%s started with:\n“%s”\n\nThe reply:\n\n%s",
				self.Username, in, author, excerpt(where.root.Message), input.Message),
			Label: "Post this reply in @" + author + "'s thread",
		}, nil
	}
}

// Reaction is a reaction the user added.
type Reaction struct {
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
}

type addReactionInput struct {
	PostID    string `json:"post_id" jsonschema:"the post to react to"`
	EmojiName string `json:"emoji_name" jsonschema:"the emoji's name, such as thumbsup, white_check_mark or eyes, with or without the colons"`
}

func addReactionSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "add_reaction",
			Description: "React to a post with an emoji, under the user's name. The person is asked to confirm each reaction, " +
				"seeing the emoji and the post, before it is added; if they decline or close the question, do not react again unless they ask.",
			Annotations: writes("Add reaction", true),
		},
		[]Use{
			{
				Operation: "SaveReaction",
				Params: map[string]Coverage{
					"body.post_id":    SetBy("post_id"),
					"body.emoji_name": SetBy("emoji_name"),
					"body.user_id":    Fixed("the user's own id", "a user reacts as themselves; Mattermost refuses a reaction for anyone else"),
					"body.create_at":  Omitted("Mattermost records when the reaction was made"),
				},
			},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("post_id"),
					"include_deleted": Omitted("a deleted post cannot be reacted to"),
				},
			},
			{
				Operation: "GetChannel",
				Params:    map[string]Coverage{"channel_id": Fixed("the post's channel", "the confirmation says where the post is")},
			},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a reaction names the user who made it")},
			},
			{
				Operation: "GetUsersByIds",
				Params:    map[string]Coverage{"since": Omitted("the tool reads each username, whenever it changed")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[addReactionInput, Reaction] {
			return asking("add_reaction", reactionConfirmation(clientFor),
				func(ctx context.Context, request *mcp.CallToolRequest, input addReactionInput) (*mcp.CallToolResult, Reaction, error) {
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

// emojiName is an emoji's name as Mattermost stores it, without the colons a
// message writes around it.
func emojiName(name string) string {
	return strings.Trim(strings.TrimSpace(name), ":")
}

// reactionConfirmation asks to react: as whom, with what, to which post.
func reactionConfirmation(clientFor ClientFor) func(context.Context, *mcp.CallToolRequest, addReactionInput) (confirmation, error) {
	return func(ctx context.Context, request *mcp.CallToolRequest, input addReactionInput) (confirmation, error) {
		emoji := emojiName(input.EmojiName)
		if emoji == "" {
			return confirmation{}, fmt.Errorf("emoji_name is empty")
		}
		client, err := clientFor(ctx, request)
		if err != nil {
			return confirmation{}, err
		}
		post, err := client.Post(ctx, input.PostID)
		if err != nil {
			return confirmation{}, err
		}
		self, err := client.Me(ctx)
		if err != nil {
			return confirmation{}, err
		}
		channel, err := client.Channel(ctx, post.ChannelId)
		if err != nil {
			return confirmation{}, err
		}
		names, err := usernames(ctx, client, []string{post.UserId, otherInDirect(channel, self.Id)})
		if err != nil {
			return confirmation{}, err
		}
		names[self.Id] = self.Username
		author := names[post.UserId]
		if author == "" {
			author = post.UserId
		}
		return confirmation{
			Message: fmt.Sprintf("React as @%s with :%s: to @%s's post in %s:\n“%s”",
				self.Username, emoji, author, place(channel, self.Id, names), excerpt(post.Message)),
			Label: fmt.Sprintf("Add :%s: to @%s's post", emoji, author),
		}, nil
	}
}
