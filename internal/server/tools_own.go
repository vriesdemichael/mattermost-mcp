package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
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
	userID, channelID, rootID string
	stop                      context.CancelFunc
}

// typists are the typing indicators this server keeps up, by user, channel
// and thread: a server over HTTP may act for several users (ADR-020).
var typists = struct {
	sync.Mutex
	active map[string]*typist
}{active: map[string]*typist{}}

// startTyping shows the user typing in a channel or thread until stopTyping,
// a post there, or typingFor runs out. The first event is sent within the
// call, so a refusal reaches the caller; the ones after it outlive the call.
func startTyping(ctx context.Context, client *mattermost.Client, userID, channelID, rootID string) (time.Time, error) {
	if err := client.Typing(ctx, userID, channelID, rootID); err != nil {
		return time.Time{}, err
	}
	keep, cancel := context.WithTimeout(context.Background(), typingFor) //nolint:contextcheck // outlives the call by design
	until, _ := keep.Deadline()
	key := userID + "/" + channelID + "/" + rootID
	typists.Lock()
	if previous := typists.active[key]; previous != nil {
		previous.stop()
	}
	current := &typist{userID: userID, channelID: channelID, rootID: rootID, stop: cancel}
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
			case <-keep.Done():
				return
			case <-ticker.C:
				// A failure to send one event ends the indicator rather than
				// retrying: it lapses on its own within seconds.
				if client.Typing(keep, userID, channelID, rootID) != nil {
					return
				}
			}
		}
	}()
	return until, nil
}

// stopTyping takes down the user's typing indicator in a channel, or in a
// thread of it when rootID is set.
func stopTyping(userID, channelID, rootID string) {
	key := userID + "/" + channelID + "/" + rootID
	typists.Lock()
	defer typists.Unlock()
	if t := typists.active[key]; t != nil {
		t.stop()
		delete(typists.active, key)
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
				"The indicator stays up until create_post posts there, until stop is sent, or for one minute at most. " +
				"It is optional: use it only for a long message that takes a while to write, and never for one you only consider.",
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
				channelID, rootID, err := threadOf(ctx, client, input.ChannelID, input.RootID)
				if err != nil {
					return nil, Typing{}, err
				}
				answer := Typing{ChannelID: channelID, RootID: rootID}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Typing{}, err
				}
				if input.Stop {
					stopTyping(self.Id, channelID, rootID)
					return nil, answer, nil
				}
				until, err := startTyping(ctx, client, self.Id, channelID, rootID)
				if err != nil {
					return nil, Typing{}, err
				}
				answer.Typing, answer.Until = true, until.In(zoneOf(self)).Format(time.RFC3339)
				return nil, answer, nil
			}
		},
	), "a typing indicator is gone within seconds of the last event, and nothing of it is kept"),
		map[string]string{"stop": "takes the indicator down instead of putting it up"})
}

// Following is whether the user follows a thread.
type Following struct {
	RootID    string `json:"root_id"`
	URL       string `json:"url" jsonschema:"the link of the post that started the thread"`
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
				channelID, rootID, err := threadOf(ctx, client, "", input.PostID)
				if err != nil {
					return nil, Following{}, err
				}
				teamID, err := teamOf(ctx, client, channelID)
				if err != nil {
					return nil, Following{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Following{}, err
				}
				if err := client.Follow(ctx, self.Id, teamID, rootID, following(input.Following)); err != nil {
					return nil, Following{}, err
				}
				return nil, Following{RootID: rootID, URL: client.Permalink(rootID), Following: following(input.Following)}, nil
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
	URL    string `json:"url" jsonschema:"the post's link"`
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
				return nil, Saved{PostID: input.PostID, URL: client.Permalink(input.PostID), Saved: saved}, nil
			}
		},
	), "only the user sees what they saved"),
		map[string]string{"saved": "chooses between UpdatePreferences and DeletePreferences"})
}

// threadOf is the channel and thread a channel id or a post names: for a post,
// its channel and the first post of its thread, which is how Mattermost names a
// thread. GetPost.
func threadOf(ctx context.Context, client *mattermost.Client, channelID, postID string) (string, string, error) {
	if postID == "" {
		if channelID == "" {
			return "", "", fmt.Errorf("give channel_id for a channel, or root_id for a thread")
		}
		return channelID, "", nil
	}
	post, err := client.Post(ctx, postID)
	if err != nil {
		return "", "", err
	}
	if channelID != "" && channelID != post.ChannelId {
		return "", "", fmt.Errorf("post %s is in channel %s, not %s; leave channel_id out", postID, post.ChannelId, channelID)
	}
	root := post.Id
	if post.RootId != "" {
		root = post.RootId
	}
	return post.ChannelId, root, nil
}

// Reminder is when Mattermost will remind the user of a post.
type Reminder struct {
	PostID   string `json:"post_id"`
	URL      string `json:"url" jsonschema:"the post's link"`
	RemindAt string `json:"remind_at" jsonschema:"when the reminder comes, in the person's own timezone"`
	Timezone string `json:"timezone" jsonschema:"the timezone remind_at is in, as the person set it in Mattermost"`
}

type setPostReminderInput struct {
	PostID string `json:"post_id" jsonschema:"the post to be reminded of"`
	At     string `json:"at" jsonschema:"when, as an ISO 8601 time: with an offset, such as 2026-10-12T09:00:00+02:00, or without one, such as 2026-10-12T09:00, in the person's own timezone"`
}

func setPostReminderSpec() Spec {
	return shaping(unasked(toolSpec(
		&mcp.Tool{
			Name: "set_post_reminder",
			Description: "Have Mattermost remind the user of a post at a time, as its \"Remind me\" does: a message from the system bot then. " +
				"A time without an offset is read in the person's own timezone, as set in Mattermost. Only the user is reminded.",
			Annotations: personal("Set post reminder"),
		},
		[]Use{
			{
				Operation: "SetPostReminder",
				Params: map[string]Coverage{
					"user_id":          Fixed("me", "a user is reminded for themselves"),
					"post_id":          SetBy("post_id"),
					"body.target_time": SetBy("at"),
				},
			},
			{
				Operation: "GetPost",
				Params: map[string]Coverage{
					"post_id":         SetBy("post_id"),
					"include_deleted": Omitted("a deleted post is nothing to be reminded of"),
				},
			},
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "the user's timezone reads a time without an offset")},
			},
		},
		func(clientFor ClientFor) mcp.ToolHandlerFor[setPostReminderInput, Reminder] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input setPostReminderInput) (*mcp.CallToolResult, Reminder, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Reminder{}, err
				}
				if _, err := client.Post(ctx, input.PostID); err != nil {
					return nil, Reminder{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Reminder{}, err
				}
				at, zone, err := reminderAt(input.At, self)
				if err != nil {
					return nil, Reminder{}, err
				}
				if err := client.Remind(ctx, self.Id, input.PostID, at); err != nil {
					return nil, Reminder{}, err
				}
				return nil, Reminder{PostID: input.PostID, URL: client.Permalink(input.PostID), RemindAt: at.In(zone).Format(time.RFC3339), Timezone: zone.String()}, nil
			}
		},
	), "only the user is reminded"), nil)
}

// userZone is the timezone the person set in Mattermost: the one their device
// reports when they let it, or the one they chose; UTC when they set none. A
// zone that does not load is refused, not read as UTC, which would remind the
// person at another hour than they asked for.
func userZone(user *model.User) (*time.Location, error) {
	name := user.GetPreferredTimezone()
	if name == "" {
		return time.UTC, nil
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("the person's timezone, %q, is not one this server knows; give the time with an offset, such as 2026-10-12T09:00:00+02:00", name)
	}
	return zone, nil
}

// reminderAt is when to remind, and the zone the answer says it in: a time
// with an offset as it says, and one without in the person's own zone.
func reminderAt(at string, user *model.User) (time.Time, *time.Location, error) {
	zone, zoneErr := userZone(user)
	if zoneErr != nil {
		// A time with an offset needs no zone of the person's.
		if parsed, err := reminderTime(at, time.UTC, time.Now()); err == nil && hasOffset(at) {
			return parsed, time.UTC, nil
		}
		return time.Time{}, nil, zoneErr
	}
	parsed, err := reminderTime(at, zone, time.Now())
	return parsed, zone, err
}

// hasOffset reports whether an ISO 8601 time says its own offset.
func hasOffset(at string) bool {
	at = strings.TrimSpace(at)
	if strings.HasSuffix(at, "Z") {
		return true
	}
	date, clock, found := strings.Cut(at, "T")
	return found && date != "" && strings.ContainsAny(clock, "+-")
}

// reminderTime reads an ISO 8601 time: with an offset as it says, without one
// in the person's zone. It must be in the future, and Mattermost takes it to
// the second.
func reminderTime(at string, zone *time.Location, now time.Time) (time.Time, error) {
	at = strings.TrimSpace(at)
	var parsed time.Time
	var err error
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00"} {
		if parsed, err = time.Parse(layout, at); err == nil {
			break
		}
	}
	if err != nil {
		for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
			if parsed, err = time.ParseInLocation(layout, at, zone); err == nil {
				break
			}
		}
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("at must be an ISO 8601 time such as 2026-10-12T09:00 or 2026-10-12T09:00:00+02:00, not %q", at)
	}
	if !parsed.After(now) {
		return time.Time{}, fmt.Errorf("%s is not in the future; it is %s in the person's timezone now", parsed.In(zone).Format(time.RFC3339), now.In(zone).Format(time.RFC3339))
	}
	return parsed.Truncate(time.Second), nil
}

// localTime reads a time a person writes: with an offset as it says, without
// one in zone, and a day alone as its start in zone.
func localTime(at string, zone *time.Location) (time.Time, error) {
	at = strings.TrimSpace(at)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
		if parsed, err := time.Parse(layout, at); err == nil {
			return parsed, nil
		}
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", time.DateOnly} {
		if parsed, err := time.ParseInLocation(layout, at, zone); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a time", at)
}
