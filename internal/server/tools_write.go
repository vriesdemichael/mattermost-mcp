package server

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
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
// Mattermost; doing it again changes nothing more.
func overwrites(title string) *mcp.ToolAnnotations {
	annotations := writes(title, true)
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
		return "the group message with " + oneLine(channel.DisplayName)
	}
	return "~" + oneLine(channel.DisplayName)
}

// oneLine is a name someone gave, as a question shows it: control characters,
// which could start a forged line of the question, become spaces.
func oneLine(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, name)
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

// destination is where a post goes, as the question names it. A direct or
// group message is opened only once the person accepts, so a declined post
// leaves no conversation behind.
type destination struct {
	self      *model.User
	label     string
	channelID string
	root      *model.Post
	open      func(context.Context) (string, error)
	size      destinationSize
	// key names the destination for the person's answer: the channel and
	// thread, or the people a direct or group message goes to, so the answer
	// accepts a post to the people it named and not to whoever the same
	// usernames mean later.
	key string
}

// rootID is the id of the thread's first post, or empty for a post that starts
// one.
func (d destination) rootID() string {
	if d.root == nil {
		return ""
	}
	return d.root.Id
}

// channel is the channel the post goes in, opening a direct or group message.
func (d destination) channel(ctx context.Context) (string, error) {
	if d.channelID != "" {
		return d.channelID, nil
	}
	return d.open(ctx)
}

// posting is the input of a tool that posts a message somewhere.
type posting interface {
	text() string
	attached() []attachFile
	destination(ctx context.Context, client *mattermost.Client) (destination, error)
	// typingIn names the channel and thread a typing indicator may be up in.
	typingIn() (channelID, rootID string)
}

// inChannel is where a post in a known channel goes, as a reply in rootID's
// thread when it is set. Mattermost takes only a thread's first post as a
// reply's root, so a reply to a reply goes in the same thread.
// GetPost, GetUser, GetChannel, GetUsersByIds, GetChannelStats.
func inChannel(ctx context.Context, client *mattermost.Client, channelID, rootID string) (destination, error) {
	var root *model.Post
	if rootID != "" {
		post, err := client.Post(ctx, rootID)
		if err != nil {
			return destination{}, err
		}
		if post.RootId != "" {
			if post, err = client.Post(ctx, post.RootId); err != nil {
				return destination{}, err
			}
		}
		if channelID != "" && channelID != post.ChannelId {
			return destination{}, fmt.Errorf("post %s is in channel %s, not %s; leave channel_id out to reply in its thread", rootID, post.ChannelId, channelID)
		}
		root, channelID = post, post.ChannelId
	}
	if channelID == "" {
		return destination{}, fmt.Errorf("give channel_id to post in a channel, or root_id to reply in a thread")
	}
	self, err := client.Me(ctx)
	if err != nil {
		return destination{}, err
	}
	channel, err := client.Channel(ctx, channelID)
	if err != nil {
		return destination{}, err
	}
	if channel.DeleteAt > 0 {
		return destination{}, fmt.Errorf("~%s is archived: it can be read but not posted in", channel.DisplayName)
	}
	names, err := usernames(ctx, client, []string{otherInDirect(channel, self.Id)})
	if err != nil {
		return destination{}, err
	}
	return destination{
		self:      self,
		label:     place(channel, self.Id, names),
		channelID: channelID,
		key:       "channel:" + channelID + "/" + rootID,
		root:      root,
		size: func(ctx context.Context) (int64, error) {
			stats, err := client.ChannelStats(ctx, channelID)
			if err != nil {
				return 0, err
			}
			return stats.MemberCount, nil
		},
	}, nil
}

// postingSpec is a tool that posts what the person accepts: the message, the
// files it carries, and where it goes (ADR-021, ADR-029, ADR-031). channel and
// root say what sets the post's channel and thread, and inTeams whether its
// posts can be in a team's channel.
func postingSpec[In posting](tool *mcp.Tool, own []Use, channel, root Coverage, inTeams bool) Spec {
	return configuredToolSpec(tool,
		uses(own, []Use{
			{
				Operation: "CreatePost",
				Params: map[string]Coverage{
					"body.channel_id": channel,
					"body.message":    SetBy("message"),
					"body.root_id":    root,
					"body.file_ids":   SetBy("files"),
					"body.props": Fixed(`{"ai_generated_by": the user's id}`,
						"marks the post as written with AI, which Mattermost shows; MM_MCP_MARK_AI_GENERATED=false leaves it out"),
					"body.metadata": Omitted("metadata such as a post's priority is a Mattermost feature beyond posting text, which the person is not shown"),
					"set_online":    Omitted("Mattermost's default marks the user online when they post, as its own client does"),
					"silent":        Omitted("a silent post notifies nobody it mentions, which the person would have to be told about when confirming"),
				},
				Releases: "11.7 has no silent parameter, which the tool never sends",
			},
			{
				Operation: "UploadFile",
				Params: map[string]Coverage{
					"body.channel_id": channel,
					"body.files":      SetBy("files"),
					"body.client_ids": Omitted("ids a client gives uploads it sends at once, to match them up; the tool uploads one file at a time"),
					"channel_id":      Omitted("Mattermost's client sends the channel as the form's channel_id field instead"),
					"filename":        Omitted("the file's name travels with its part of the form"),
				},
			},
		}, messageUses(inTeams), describeUses(inTeams)),
		func(clientFor ClientFor, cfg config.Config) mcp.ToolHandlerFor[In, Post] {
			// What a post is bound to: the files' bytes, and where it goes.
			bound := func(files []attachment, where destination) string {
				return fingerprint(files) + "\n" + where.key
			}
			// keep holds the files' bytes, which only the read that uploads needs.
			prepare := func(ctx context.Context, request *mcp.CallToolRequest, input In, keep bool) (*mattermost.Client, []attachment, destination, error) {
				files, err := loadAttachments(cfg, input.attached(), keep)
				if err != nil {
					return nil, nil, destination{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, nil, destination{}, err
				}
				where, err := input.destination(ctx, client)
				return client, files, where, err
			}
			ask := askingBound(tool.Name,
				func(ctx context.Context, request *mcp.CallToolRequest, input In) (string, error) {
					_, files, where, err := prepare(ctx, request, input, false)
					if err != nil {
						return "", err
					}
					return bound(files, where), nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input In) (confirmation, error) {
					client, files, where, err := prepare(ctx, request, input, false)
					if err != nil {
						return confirmation{}, err
					}
					// The question shows what was fingerprinted, not a file
					// changed in between.
					if err := stillAsAsked(ctx, tool.Name, bound(files, where)); err != nil {
						return confirmation{}, err
					}
					checked, err := checkMessage(ctx, client, input.text(), where.label, where.size)
					if err != nil {
						return confirmation{}, err
					}
					return postQuestion(ctx, client, where, input.text(), files, checked, cfg.MarkAIGenerated)
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Post, error) {
					// Read once more, checked against what the person accepted, and
					// these very bytes are what is uploaded.
					client, files, where, err := prepare(ctx, request, input, true)
					if err != nil {
						return nil, Post{}, err
					}
					if err := stillAsAsked(ctx, tool.Name, bound(files, where)); err != nil {
						return nil, Post{}, err
					}
					channelID, err := where.channel(ctx)
					if err != nil {
						return nil, Post{}, err
					}
					fileIDs := make([]string, 0, len(files))
					for _, file := range files {
						id, err := client.Upload(ctx, channelID, file.name, file.data)
						if err != nil {
							return nil, Post{}, err
						}
						fileIDs = append(fileIDs, id)
					}
					created, err := client.CreatePost(ctx, mattermost.NewPost{
						ChannelID: channelID, RootID: where.rootID(), Message: input.text(), FileIDs: fileIDs,
						Props: aiMarker(cfg.MarkAIGenerated, where.self.Id),
					})
					if err != nil {
						return nil, Post{}, err
					}
					stopTyping(where.self.Id, created.ChannelId, created.RootId)
					posted, err := describePosts(ctx, client, []*model.Post{created})
					if err != nil {
						// The post exists: answer with what is known rather than an
						// error that would invite posting it again.
						return nil, Post{ID: created.Id, ChannelID: created.ChannelId, RootID: created.RootId, Message: created.Message, AuthorID: created.UserId}, nil
					}
					return nil, posted[0], nil
				})
			return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Post, error) {
				result, posted, err := ask(ctx, request, input)
				// The indicator stays while the person is being asked, which is
				// when they are finishing the message, and goes once they have
				// answered either way: a post took it down already; otherwise it
				// is found where the post would have gone.
				if (result == nil || result.RequestState == "") && posted.ID == "" {
					if client, e := clientFor(ctx, request); e == nil {
						if where, e := input.destination(ctx, client); e == nil && where.channelID != "" {
							stopTyping(where.self.Id, where.channelID, where.rootID())
						}
					}
				}
				return result, posted, err
			}
		},
	)
}

// postQuestion asks to post: as whom, where, in reply to what, what its
// mentions do, every file it carries, and then the message as it will be
// sent. What the person must not miss comes before the message, which may be
// long.
func postQuestion(ctx context.Context, client *mattermost.Client, where destination, message string, attached []attachment, checked checkedMessage, marked bool) (confirmation, error) {
	files := carrying(attached)
	summary := describeAttachments(attached) + checked.note() + aiNote(marked)
	if where.root == nil {
		return confirmation{
			Message: fmt.Sprintf("Post as @%s in %s.%s\n\nThe message:\n\n%s", where.self.Username, where.label, summary, message),
			Label:   "Post this message" + files + " in " + where.label,
		}, nil
	}
	names, err := usernames(ctx, client, []string{where.root.UserId})
	if err != nil {
		return confirmation{}, err
	}
	author := names[where.root.UserId]
	if author == "" {
		author = where.root.UserId
	}
	return confirmation{
		Message: fmt.Sprintf("Reply as @%s in %s, in the thread @%s started with:\n“%s”%s\n\nThe reply:\n\n%s",
			where.self.Username, where.label, author, excerpt(where.root.Message), summary, message),
		Label: "Post this reply" + files + " in @" + author + "'s thread",
	}, nil
}

// postingDescription ends the description of every tool that posts.
const postingDescription = " The message is Mattermost Markdown, at most as long as the server takes, and every @mention must name someone who exists. " +
	"The person is asked to confirm each post, seeing the message, where it goes and every file it carries, before anything is sent or uploaded; " +
	"if they decline or close the question, do not post it again unless they ask. Posting takes down the typing indicator shown there."

// filesField is the files argument every tool that posts takes.
type filesField struct {
	Files []attachFile `json:"files,omitempty" jsonschema:"files to attach, at most 10: from a full path on this machine when the server runs on it, or text written for the post with a name"`
}

func (f filesField) attached() []attachFile { return f.Files }

type createPostInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"the channel to post in, as get_user_channels or get_channel_info gives it"`
	RootID    string `json:"root_id,omitempty" jsonschema:"reply in the thread of this post: the post that started it or any reply in it"`
	Message   string `json:"message" jsonschema:"the text to post, in Mattermost Markdown"`
	filesField
}

func (in createPostInput) text() string { return in.Message }
func (in createPostInput) destination(ctx context.Context, client *mattermost.Client) (destination, error) {
	return inChannel(ctx, client, in.ChannelID, in.RootID)
}
func (in createPostInput) typingIn() (string, string) { return in.ChannelID, in.RootID }

func createPostSpec() Spec {
	return postingSpec[createPostInput](
		&mcp.Tool{
			Name: "create_post",
			Description: "Post a message in a channel under the user's name, or with root_id a reply in the thread of any post. " +
				"To message people directly use dm, and group_message only when the person asks for a group conversation." + postingDescription,
			Annotations: writes("Create post", false),
		},
		[]Use{
			{Operation: "GetChannel", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("root_id"),
					"include_deleted": Omitted("a deleted post has no thread to reply in"),
				},
			},
		},
		SetBy("channel_id"), SetBy("root_id"), true,
	)
}

type dmInput struct {
	Username string `json:"username,omitempty" jsonschema:"the person to message: their username, with or without @, or their email address; the user themselves when not given"`
	Message  string `json:"message" jsonschema:"the text to send, in Mattermost Markdown"`
	filesField
}

func (in dmInput) text() string               { return in.Message }
func (in dmInput) typingIn() (string, string) { return "", "" }
func (in dmInput) destination(ctx context.Context, client *mattermost.Client) (destination, error) {
	self, err := client.Me(ctx)
	if err != nil {
		return destination{}, err
	}
	other := self
	if name := strings.TrimPrefix(strings.TrimSpace(in.Username), "@"); name != "" && !strings.EqualFold(name, self.Username) {
		found, err := lookUpUsers(ctx, client, []string{name})
		if err != nil {
			return destination{}, err
		}
		other = found[0]
	}
	if other.DeleteAt > 0 {
		return destination{}, fmt.Errorf("@%s is deactivated, and cannot be messaged", other.Username)
	}
	label := "your direct message with @" + other.Username
	people := int64(2)
	if other.Id == self.Id {
		label, people = "your direct message to yourself", 1
	}
	return destination{
		self:  self,
		label: label,
		key:   "direct:" + other.Id,
		open: func(ctx context.Context) (string, error) {
			channel, err := client.DirectChannel(ctx, self.Id, other.Id)
			if err != nil {
				return "", err
			}
			return channel.Id, nil
		},
		size: func(context.Context) (int64, error) { return people, nil },
	}, nil
}

// peopleUses are the operations lookUpUsers calls for a person named by arg:
// by username, user id or email address.
func peopleUses(arg string) []Use {
	return []Use{
		{Operation: "GetUsersByUsernames", Params: map[string]Coverage{}},
		{Operation: "GetUserByEmail", Params: map[string]Coverage{"email": SetBy(arg)}},
		{Operation: "SearchUsers", Params: suggestionSearch(SetBy(arg))},
	}
}

func dmSpec() Spec {
	return postingSpec[dmInput](
		&mcp.Tool{
			Name: "dm",
			Description: "Send a direct message to one person under the user's name, or to the user themselves when no username is given. " +
				"This is how to message people: \"message Alice and Bob\" is two direct messages, one to each." + postingDescription,
			Annotations: writes("Send direct message", false),
		},
		uses(peopleUses("username"), []Use{
			{Operation: "CreateDirectChannel", Params: map[string]Coverage{}},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the message is sent under the user's name, and the question says whose")},
			},
		}),
		SetBy("username"), Omitted("a reply in a direct message's thread is a create_post with root_id"), false,
	)
}

// Mattermost's limits on a group message: at least three people and at most
// eight, the user included.
const (
	minGroupOthers = 2
	maxGroupOthers = 7
)

type groupMessageInput struct {
	Usernames []string `json:"usernames" jsonschema:"the people to message together, two to seven of them, by username, with or without @, or email address, the user left out"`
	Message   string   `json:"message" jsonschema:"the text to send, in Mattermost Markdown"`
	filesField
}

func (in groupMessageInput) text() string               { return in.Message }
func (in groupMessageInput) typingIn() (string, string) { return "", "" }
func (in groupMessageInput) destination(ctx context.Context, client *mattermost.Client) (destination, error) {
	self, err := client.Me(ctx)
	if err != nil {
		return destination{}, err
	}
	var names []string
	for _, name := range in.Usernames {
		if name = strings.TrimPrefix(strings.TrimSpace(name), "@"); name != "" && !strings.EqualFold(name, self.Username) && !containsFold(names, name) {
			names = append(names, name)
		}
	}
	if len(names) < minGroupOthers || len(names) > maxGroupOthers {
		return destination{}, fmt.Errorf("a group message is with %d to %d people besides the user, not %d; message one person with dm",
			minGroupOthers, maxGroupOthers, len(names))
	}
	people, err := lookUpUsers(ctx, client, names)
	if err != nil {
		return destination{}, err
	}
	// The same person may be named twice, by username and by email; the user
	// is in every group of theirs already.
	ids := []string{self.Id}
	var mentioned []string
	for _, person := range people {
		if slices.Contains(ids, person.Id) {
			continue
		}
		if person.DeleteAt > 0 {
			return destination{}, fmt.Errorf("@%s is deactivated, and cannot be messaged", person.Username)
		}
		ids = append(ids, person.Id)
		mentioned = append(mentioned, "@"+person.Username)
	}
	if others := len(ids) - 1; others < minGroupOthers {
		return destination{}, fmt.Errorf("a group message is with %d to %d people besides the user, and those named are %d; message one person with dm",
			minGroupOthers, maxGroupOthers, others)
	}
	sorted := slices.Sorted(slices.Values(ids))
	return destination{
		self:  self,
		label: "the group message with you and " + strings.Join(mentioned, ", "),
		key:   "group:" + strings.Join(sorted, ","),
		open: func(ctx context.Context) (string, error) {
			channel, err := client.GroupChannel(ctx, ids)
			if err != nil {
				return "", err
			}
			return channel.Id, nil
		},
		size: func(context.Context) (int64, error) { return int64(len(ids)), nil },
	}, nil
}

func containsFold(names []string, name string) bool {
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

func groupMessageSpec() Spec {
	return postingSpec[groupMessageInput](
		&mcp.Tool{
			Name: "group_message",
			Description: "Send one message to a group of two to seven people together, under the user's name, in the group conversation " +
				"Mattermost keeps for exactly those people. Only when the person asks for a group conversation: to message several people, " +
				"send each a direct message with dm." + postingDescription,
			Annotations: writes("Send group message", false),
		},
		uses(peopleUses("usernames"), []Use{
			{Operation: "CreateGroupChannel", Params: map[string]Coverage{}},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the message is sent under the user's name, and the user is in the group")},
			},
		}),
		SetBy("usernames"), Omitted("a reply in a group message's thread is a create_post with root_id"), false,
	)
}

type updatePostInput struct {
	PostID  string `json:"post_id" jsonschema:"the post to edit, which must be the user's own"`
	Message string `json:"message" jsonschema:"the post's new text in full, in Mattermost Markdown; it replaces the old text"`
}

func updatePostSpec() Spec {
	return configuredToolSpec(
		&mcp.Tool{
			Name: "update_post",
			Description: "Replace the text of one of the user's own posts; Mattermost marks the post as edited, and notifies nobody it " +
				"newly mentions. The new text must fit the server's limit. The person is asked to confirm each edit, seeing the old text " +
				"and the new; if they decline or close the question, do not edit it again unless they ask.",
			Annotations: overwrites("Update post"),
		},
		uses([]Use{{
			Operation: "PatchPost",
			Params: map[string]Coverage{
				"post_id":            SetBy("post_id"),
				"body.message":       SetBy("message"),
				"body.props":         Fixed(`the post's own props, with {"ai_generated_by": the user's id}`, "marks the post as written with AI, keeping every other property; MM_MCP_MARK_AI_GENERATED=false leaves the props as they are"),
				"body.file_ids":      Omitted("an edit changes the text; the attachments stay as they are"),
				"body.has_reactions": Omitted("Mattermost keeps it in step with the reactions itself"),
				"body.is_pinned":     Omitted("pin_post pins and unpins, asking about that on its own"),
			},
		}}, postContextUses(), lengthUses(), describeUses(true)),
		func(clientFor ClientFor, cfg config.Config) mcp.ToolHandlerFor[updatePostInput, Post] {
			// An edit is bound to the text it replaces.
			bind := func(ctx context.Context, request *mcp.CallToolRequest, input updatePostInput) (string, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return "", err
				}
				p, err := ownPost(ctx, client, input.PostID, "edit")
				if err != nil {
					return "", err
				}
				return p.post.Message, nil
			}
			return askingBound("update_post", bind,
				func(ctx context.Context, request *mcp.CallToolRequest, input updatePostInput) (confirmation, error) {
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
					// Mattermost notifies nobody of an edit, so its mentions are only text.
					if err := checkLength(ctx, client, input.Message); err != nil {
						return confirmation{}, err
					}
					_, marked := editProps(p.post, cfg.MarkAIGenerated, p.self.Id)
					return confirmation{
						Message: fmt.Sprintf("Edit your post in %s as @%s. An edit notifies nobody.%s\n\nIt reads now:\n\n%s\n\nIt will read:\n\n%s",
							p.in, p.self.Username, aiNote(marked), p.post.Message, input.Message),
						Label: "Replace the text of your post in " + p.in,
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input updatePostInput) (*mcp.CallToolResult, Post, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Post{}, err
					}
					p, err := ownPost(ctx, client, input.PostID, "edit")
					if err != nil {
						return nil, Post{}, err
					}
					if err := stillAsAsked(ctx, "update_post", p.post.Message); err != nil {
						return nil, Post{}, err
					}
					props, _ := editProps(p.post, cfg.MarkAIGenerated, p.self.Id)
					edited, err := client.EditPost(ctx, input.PostID, input.Message, props)
					if err != nil {
						return nil, Post{}, err
					}
					posts, err := describePosts(ctx, client, []*model.Post{edited})
					if err != nil {
						return nil, Post{}, err
					}
					return nil, posts[0], nil
				})
		},
	)
}

// markablePostProps are the properties an edit may send back unchanged with
// the AI marker. Mattermost sanitises the properties an edit sends, and in its
// hardened mode refuses some outright, so a post carrying any other property
// is edited without the marker rather than have it disturbed.
var markablePostProps = map[string]bool{
	model.PostPropsAIGeneratedByUserID:   true,
	model.PostPropsAIGeneratedByUsername: true,
	"disable_group_highlight":            true,
}

// editProps are the properties an edit sends, nil to leave them as they are,
// and whether they mark the post as written with AI.
func editProps(post *model.Post, mark bool, userID string) (model.StringInterface, bool) {
	if !mark {
		return nil, false
	}
	for name := range post.GetProps() {
		if !markablePostProps[name] {
			return nil, false
		}
	}
	props := model.StringInterface{}
	maps.Copy(props, post.GetProps())
	maps.Copy(props, aiMarker(true, userID))
	return props, true
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
			Annotations: overwrites("Delete post"),
		},
		uses([]Use{{
			Operation: "DeletePost",
			Params:    map[string]Coverage{"post_id": SetBy("post_id")},
		}}, postContextUses()),
		func(clientFor ClientFor) mcp.ToolHandlerFor[deletePostInput, Deleted] {
			// A deletion is bound to the post and the replies that go with it,
			// so one posted while the person was asked is not deleted unseen.
			state := func(post *model.Post) string { return fmt.Sprintf("%d\n%s", post.ReplyCount, post.Message) }
			bind := func(ctx context.Context, request *mcp.CallToolRequest, input deletePostInput) (string, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return "", err
				}
				p, err := ownPost(ctx, client, input.PostID, "delete")
				if err != nil {
					return "", err
				}
				return state(p.post), nil
			}
			return askingBound("delete_post", bind,
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
					if err := stillAsAsked(ctx, "delete_post", state(p.post)); err != nil {
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

// Pinned is a post's pinned state after pin_post.
type Pinned struct {
	PostID string `json:"post_id"`
	URL    string `json:"url" jsonschema:"the post's link"`
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
		uses([]Use{
			{Operation: "PinPost", Params: map[string]Coverage{"post_id": SetBy("post_id")}},
			{Operation: "UnpinPost", Params: map[string]Coverage{"post_id": SetBy("post_id")}},
		}, postContextUses()),
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
					return nil, Pinned{PostID: input.PostID, URL: client.Permalink(input.PostID), Pinned: pinning(input.Pinned)}, nil
				})
		},
	), map[string]string{"pinned": "chooses between PinPost and UnpinPost"})
}

// pinning is a pinned argument, true when left out.
func pinning(pinned *bool) bool { return pinned == nil || *pinned }
