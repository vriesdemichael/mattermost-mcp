package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// The tools that change what is the user's alone, or what is gone within
// seconds. They are offered only when writes are allowed, like every tool that
// changes Mattermost, but do not ask before each call (ADR-021).

// personal is the annotation of a tool that changes only what is the user's
// own, or what lasts seconds.
func personal(title string) *mcp.ToolAnnotations {
	return writes(title, true)
}

// How long a typing indicator is kept up, and how often it is sent again.
// Clients show it for a few seconds after each event, so one event is not
// enough for a message that takes longer to write; the cap ends one whose
// message never comes.
const (
	typingFor   = time.Minute
	typingEvery = 3 * time.Second
)

// typist keeps one typing indicator up.
type typist struct {
	channelID, rootID string
	stop              context.CancelFunc
}

// typists are the typing indicators this server keeps up, by channel and
// thread.
var typists = struct {
	sync.Mutex
	active map[string]*typist
}{active: map[string]*typist{}}

// startTyping shows the user typing in a channel or thread until stopTyping,
// a post there, or typingFor runs out. The first event is sent before it
// returns, so a refusal reaches the caller.
func startTyping(client *mattermost.Client, userID, channelID, rootID string) (time.Time, error) {
	if err := client.Typing(context.Background(), userID, channelID, rootID); err != nil { //nolint:contextcheck // outlives the call by design
		return time.Time{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), typingFor) //nolint:contextcheck // outlives the call by design
	until, _ := ctx.Deadline()
	key := channelID + "/" + rootID
	typists.Lock()
	if previous := typists.active[key]; previous != nil {
		previous.stop()
	}
	current := &typist{channelID: channelID, rootID: rootID, stop: cancel}
	typists.active[key] = current
	typists.Unlock()
	go func() {
		defer func() {
			cancel()
			typists.Lock()
			if typists.active[key] == current {
				delete(typists.active, key)
			}
			typists.Unlock()
		}()
		ticker := time.NewTicker(typingEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// A failure to send one event ends the indicator rather than
				// retrying: it lapses on its own within seconds.
				if client.Typing(ctx, userID, channelID, rootID) != nil {
					return
				}
			}
		}
	}()
	return until, nil
}

// stopTyping takes down every typing indicator in the channel or the thread
// named; an empty id names nothing.
func stopTyping(channelID, rootID string) {
	typists.Lock()
	defer typists.Unlock()
	for key, t := range typists.active {
		if (channelID != "" && t.channelID == channelID) || (rootID != "" && t.rootID == rootID) {
			t.stop()
			delete(typists.active, key)
		}
	}
}

// Typing is whether the user is shown typing, and until when.
type Typing struct {
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id,omitempty"`
	Typing    bool   `json:"typing"`
	Until     string `json:"until,omitempty" jsonschema:"when the indicator lapses if no post comes first"`
}

type typingInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"the channel to show the user typing in"`
	RootID    string `json:"root_id,omitempty" jsonschema:"show the user typing a reply in the thread of this post instead"`
	Stop      bool   `json:"stop,omitempty" jsonschema:"take the indicator down without posting"`
}

func typingSpec() Spec {
	return shaping(unasked(toolSpec(
		&mcp.Tool{
			Name: "typing",
			Description: "Show the user as typing in a channel, or in a thread with root_id, while a message is being written there. " +
				"The indicator stays up until post_message posts there, until stop is sent, or for one minute at most. " +
				"Call it when you start writing a message the person asked for, not for messages you only consider.",
			Annotations: personal("Show typing"),
		},
		[]Use{
			{
				Operation: "PublishUserTyping",
				Params: map[string]Coverage{
					"user_id":         Fixed("me", "only the user can be shown typing"),
					"body.channel_id": SetBy("channel_id"),
					"body.parent_id":  SetBy("root_id"),
				},
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
				Params:    map[string]Coverage{"user_id": Fixed("me", "the indicator shows the user")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[typingInput, Typing] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input typingInput) (*mcp.CallToolResult, Typing, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Typing{}, err
				}
				where, err := target(ctx, client, input.ChannelID, "", input.RootID)
				if err != nil {
					return nil, Typing{}, err
				}
				answer := Typing{ChannelID: where.channelID, RootID: where.rootID()}
				if input.Stop {
					stopTyping(where.channelID, where.rootID())
					return nil, answer, nil
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Typing{}, err
				}
				until, err := startTyping(client, self.Id, where.channelID, where.rootID())
				if err != nil {
					return nil, Typing{}, err
				}
				answer.Typing, answer.Until = true, until.UTC().Format(time.RFC3339)
				return nil, answer, nil
			}
		},
	), "a typing indicator is gone within seconds of the last event, and nothing of it is kept"),
		map[string]string{"stop": "takes the indicator down instead of putting it up"})
}

// Following is whether the user follows a thread.
type Following struct {
	RootID    string `json:"root_id"`
	Following bool   `json:"following"`
}

type followThreadInput struct {
	PostID    string `json:"post_id" jsonschema:"any post in the thread: the post that started it or one of the replies"`
	Following *bool  `json:"following,omitempty" jsonschema:"false stops following the thread; true, the default, follows it"`
}

// following is a following argument, true when left out.
func following(f *bool) bool { return f == nil || *f }

func followThreadSpec() Spec {
	return shaping(unasked(toolSpec(
		&mcp.Tool{
			Name: "follow_thread",
			Description: "Follow a thread, so its replies notify the user and it appears in list_threads, or stop following it with " +
				"following false. Only the user sees which threads they follow.",
			Annotations: personal("Follow thread"),
		},
		[]Use{
			{
				Operation: "StartFollowingThread",
				Params: map[string]Coverage{
					"user_id":   Fixed("me", "a user follows threads for themselves"),
					"team_id":   Fixed("the thread's team", "Mattermost files a thread under its channel's team, and a direct message's under any of the user's teams"),
					"thread_id": SetBy("post_id"),
				},
			},
			{
				Operation: "StopFollowingThread",
				Params: map[string]Coverage{
					"user_id":   Fixed("me", "a user follows threads for themselves"),
					"team_id":   Fixed("the thread's team", "Mattermost files a thread under its channel's team, and a direct message's under any of the user's teams"),
					"thread_id": SetBy("post_id"),
				},
			},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("post_id"),
					"include_deleted": Omitted("a deleted post's thread is gone"),
				},
			},
			{
				Operation: "GetChannel",
				Params:    map[string]Coverage{"channel_id": Fixed("the thread's channel", "its team is the thread's team")},
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a direct message belongs to no team, and Mattermost takes any of the user's")},
			},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a user follows threads for themselves")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[followThreadInput, Following] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input followThreadInput) (*mcp.CallToolResult, Following, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Following{}, err
				}
				where, err := target(ctx, client, "", "", input.PostID)
				if err != nil {
					return nil, Following{}, err
				}
				teamID, err := teamOf(ctx, client, where.channelID)
				if err != nil {
					return nil, Following{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Following{}, err
				}
				if err := client.Follow(ctx, self.Id, teamID, where.rootID(), following(input.Following)); err != nil {
					return nil, Following{}, err
				}
				return nil, Following{RootID: where.rootID(), Following: following(input.Following)}, nil
			}
		},
	), "only the user sees which threads they follow"),
		map[string]string{"following": "chooses between StartFollowingThread and StopFollowingThread"})
}

// teamOf is the team a channel's threads and drafts are filed under: its own,
// or for a direct or group message, which belongs to no team, the first of the
// user's.
func teamOf(ctx context.Context, client *mattermost.Client, channelID string) (string, error) {
	channel, err := client.Channel(ctx, channelID)
	if err != nil {
		return "", err
	}
	if channel.TeamId != "" {
		return channel.TeamId, nil
	}
	teams, err := client.Teams(ctx)
	if err != nil {
		return "", err
	}
	if len(teams) == 0 {
		return "", fmt.Errorf("the user belongs to no team, which Mattermost needs to file a direct message's thread or draft under")
	}
	return teams[0].Id, nil
}

// Saved is whether the user saved a post.
type Saved struct {
	PostID string `json:"post_id"`
	Saved  bool   `json:"saved"`
}

type savePostInput struct {
	PostID string `json:"post_id" jsonschema:"the post to save"`
	Saved  *bool  `json:"saved,omitempty" jsonschema:"false removes the post from the saved posts; true, the default, saves it"`
}

func savePostSpec() Spec {
	return shaping(unasked(toolSpec(
		&mcp.Tool{
			Name: "save_post",
			Description: "Save a post among the user's saved posts, to come back to, or remove it with saved false. " +
				"Only the user sees what they saved; list_saved reads them.",
			Annotations: personal("Save post"),
		},
		[]Use{
			{
				Operation: "UpdatePreferences",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a user saves posts for themselves")},
			},
			{
				Operation: "DeletePreferences",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a user saves posts for themselves")},
			},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("post_id"),
					"include_deleted": Omitted("a deleted post cannot be saved"),
				},
			},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a user saves posts for themselves")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[savePostInput, Saved] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input savePostInput) (*mcp.CallToolResult, Saved, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Saved{}, err
				}
				// Mattermost saves any id it is given; reading the post first
				// refuses one that is not a post the user can read.
				if _, err := client.Post(ctx, input.PostID); err != nil {
					return nil, Saved{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Saved{}, err
				}
				saved := input.Saved == nil || *input.Saved
				if err := client.Save(ctx, self.Id, input.PostID, saved); err != nil {
					return nil, Saved{}, err
				}
				return nil, Saved{PostID: input.PostID, Saved: saved}, nil
			}
		},
	), "only the user sees what they saved"),
		map[string]string{"saved": "chooses between UpdatePreferences and DeletePreferences"})
}

// Draft is a draft the user can send from Mattermost.
type Draft struct {
	ChannelID string `json:"channel_id"`
	RootID    string `json:"root_id,omitempty"`
	Message   string `json:"message"`
}

type draftMessageInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"the channel to draft a message in, as list_channels gives it"`
	ToUser    string `json:"to_user,omitempty" jsonschema:"draft in the direct message with this username instead"`
	RootID    string `json:"root_id,omitempty" jsonschema:"draft a reply in the thread of this post instead"`
	Message   string `json:"message" jsonschema:"the draft's text, in Mattermost Markdown"`
}

func draftMessageSpec() Spec {
	return unasked(toolSpec(
		&mcp.Tool{
			Name: "draft_message",
			Description: "Put a message in the user's message box in Mattermost, as a draft for them to read, change and send themselves. " +
				"Nothing is sent, and only the user sees it. A channel or thread that already holds a different draft is left alone. " +
				"Prefer it to post_message when the person wants to have the last word before anything goes out.",
			Annotations: personal("Draft message"),
		},
		append([]Use{
			{
				Operation: "UpsertDraft",
				Params: map[string]Coverage{
					"body.channel_id": SetBy("channel_id"),
					"body.root_id":    SetBy("root_id"),
					"body.message":    SetBy("message"),
					"body.file_ids":   Omitted("a draft carries text; the person attaches files in Mattermost"),
					"body.priority":   Omitted("a message's priority is the person's to set when they send it"),
					"body.props":      Omitted("props carry integrations' attachments and Mattermost's own settings; a draft is text"),
					"body.type":       Omitted("a draft is an ordinary message, which is the default"),
				},
				Releases: "11.7 serves POST /api/v4/drafts, as its router shows, though its specification leaves it out; nothing differs in use",
			},
			{
				Operation: "GetDrafts",
				Params: map[string]Coverage{
					"user_id": Fixed("me", "a user's drafts are theirs"),
					"team_id": Fixed("the channel's team", "Mattermost files a draft under its channel's team, and a direct message's under any of the user's teams"),
				},
				Releases: "11.7 serves GET /api/v4/users/{user_id}/teams/{team_id}/drafts, as its router shows, though its specification leaves it out; nothing differs in use",
			},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("root_id"),
					"include_deleted": Omitted("a deleted post has no thread to reply in"),
				},
			},
			{
				Operation: "GetChannel",
				Params:    map[string]Coverage{"channel_id": Fixed("the draft's channel", "its team is where the draft is filed")},
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a direct message belongs to no team, and Mattermost takes any of the user's")},
			},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "a user's drafts are theirs")},
			},
		}, targetUses()...),
		func(clientFor ClientFor) mcp.ToolHandlerFor[draftMessageInput, Draft] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input draftMessageInput) (*mcp.CallToolResult, Draft, error) {
				if strings.TrimSpace(input.Message) == "" {
					return nil, Draft{}, fmt.Errorf("the message is empty")
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Draft{}, err
				}
				where, err := target(ctx, client, input.ChannelID, input.ToUser, input.RootID)
				if err != nil {
					return nil, Draft{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Draft{}, err
				}
				teamID, err := teamOf(ctx, client, where.channelID)
				if err != nil {
					return nil, Draft{}, err
				}
				if err := noOtherDraft(ctx, client, self.Id, teamID, where, input.Message); err != nil {
					return nil, Draft{}, err
				}
				draft, err := client.Draft(ctx, self.Id, where.channelID, where.rootID(), input.Message)
				if err != nil {
					return nil, Draft{}, err
				}
				return nil, Draft{ChannelID: draft.ChannelId, RootID: draft.RootId, Message: draft.Message}, nil
			}
		},
	), "only the user sees a draft, and nothing is sent until they send it")
}

// noOtherDraft refuses to replace a draft the person may be writing: a draft
// is their unsent words, and replacing it would lose them.
func noOtherDraft(ctx context.Context, client *mattermost.Client, userID, teamID string, where replyTarget, message string) error {
	drafts, err := client.Drafts(ctx, userID, teamID)
	if err != nil {
		return err
	}
	for _, draft := range drafts {
		if draft.ChannelId == where.channelID && draft.RootId == where.rootID() &&
			strings.TrimSpace(draft.Message) != "" && draft.Message != message {
			return fmt.Errorf("there is a draft there already, which was left as it is: “%s”. Ask the person what to do with it", excerpt(draft.Message))
		}
	}
	return nil
}
