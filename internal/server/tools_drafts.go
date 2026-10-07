package server

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Drafts: the model writes into the person's own message box, and the person
// sends. Saving one is the user's alone and does not ask; deleting one loses
// words the person may have written, so it does (ADR-021).

// Draft is a draft in the user's message box.
type Draft struct {
	ChannelID string   `json:"channel_id"`
	Channel   string   `json:"channel" jsonschema:"the channel's display name; a direct message is named after the person on the other side"`
	Team      string   `json:"team,omitempty"`
	RootID    string   `json:"root_id,omitempty" jsonschema:"the thread the draft replies in, if it does"`
	Message   string   `json:"message"`
	UpdatedAt string   `json:"updated_at,omitempty"`
	Notes     []string `json:"notes,omitempty" jsonschema:"what the message's mentions will do once sent"`
}

// draftTarget is where a draft goes: a channel, or a thread in it.
type draftTarget struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"the channel, as get_user_channels or get_channel_info gives it"`
	RootID    string `json:"root_id,omitempty" jsonschema:"the thread instead: any post in it"`
}

// draftsUses are the operations that read the user's drafts where one goes.
func draftsUses() []Use {
	return []Use{
		{
			Operation: "GetDrafts",
			Params: map[string]Coverage{
				"user_id": Fixed("me", "a user's drafts are theirs"),
				"team_id": Fixed("the channel's team", "Mattermost files a draft under its channel's team, and a direct message's under each of the user's teams"),
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
		{Operation: "GetChannel", Params: map[string]Coverage{"channel_id": SetBy("channel_id")}},
		{
			Operation: "GetTeamsForUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "a direct message belongs to no team, and its drafts are filed under the user's")},
		},
		{
			Operation: "GetUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "a user's drafts are theirs")},
		},
	}
}

// existingDraft is the user's draft in a channel or thread, or nil.
func existingDraft(ctx context.Context, client *mattermost.Client, userID, teamID, channelID, rootID string) (*model.Draft, error) {
	drafts, err := client.Drafts(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	for _, draft := range drafts {
		if draft.ChannelId == channelID && draft.RootId == rootID && strings.TrimSpace(draft.Message) != "" {
			return draft, nil
		}
	}
	return nil, nil
}

// draftsSync refuses a draft the person would never see: one on a server that
// keeps no drafts, or for a person who turned off syncing their drafts, whose
// clients keep their own. GetClientConfig, GetPreferencesByCategory.
func draftsSync(ctx context.Context, client *mattermost.Client, userID string) error {
	config, err := client.ClientConfig(ctx)
	if err != nil {
		return err
	}
	if config["AllowSyncedDrafts"] == "false" {
		return fmt.Errorf("this server does not keep drafts for its users' clients, so a draft would reach none of them; post with create_post instead, or give the person the text")
	}
	preference, err := client.Preference(ctx, userID, model.PreferenceCategoryAdvancedSettings, "sync_drafts")
	if err != nil {
		return err
	}
	if preference != nil && preference.Value == "false" {
		return fmt.Errorf("the person turned off syncing their drafts in Mattermost's settings, so a draft saved here would not reach their clients; give them the text instead")
	}
	return nil
}

type saveDraftInput struct {
	draftTarget
	Message string `json:"message" jsonschema:"the draft's text, in Mattermost Markdown"`
}

func saveDraftSpec() Spec {
	return unasked(toolSpec(
		&mcp.Tool{
			Name: "save_draft",
			Description: "Put a message in the user's message box in Mattermost, in a channel or a thread, as a draft for them to read, " +
				"change and send themselves. Nothing is sent, and only the user sees it. A different draft already there is never " +
				"replaced: the tool fails with what is there. The message is checked as a post's is. Prefer it to create_post when " +
				"the person wants the last word before anything goes out.",
			Annotations: personal("Save draft"),
		},
		uses([]Use{
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
				Operation: "GetPreferencesByCategory",
				Params: map[string]Coverage{
					"user_id":  Fixed("me", "whether drafts sync is the user's own setting"),
					"category": Fixed("advanced_settings", "where Mattermost keeps sync_drafts, the setting that keeps drafts on the server"),
				},
			},
			{
				Operation: "GetChannelStats",
				Params:    map[string]Coverage{"channel_id": Fixed("the draft's channel", "the notes say how many people @channel, @all and @here would reach")},
			},
		}, draftsUses(), messageUses(false), describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[saveDraftInput, Draft] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input saveDraftInput) (*mcp.CallToolResult, Draft, error) {
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Draft{}, err
				}
				channelID, rootID, err := threadOf(ctx, client, input.ChannelID, input.RootID)
				if err != nil {
					return nil, Draft{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Draft{}, err
				}
				if err := draftsSync(ctx, client, self.Id); err != nil {
					return nil, Draft{}, err
				}
				channel, err := client.Channel(ctx, channelID)
				if err != nil {
					return nil, Draft{}, err
				}
				checked, err := checkMessage(ctx, client, input.Message, "~"+channel.DisplayName, func(ctx context.Context) (int64, error) {
					stats, err := client.ChannelStats(ctx, channelID)
					if err != nil {
						return 0, err
					}
					return stats.MemberCount, nil
				})
				if err != nil {
					return nil, Draft{}, err
				}
				teamID, err := teamOf(ctx, client, channelID)
				if err != nil {
					return nil, Draft{}, err
				}
				// A draft is the person's unsent words; replacing one would lose them.
				if there, err := existingDraft(ctx, client, self.Id, teamID, channelID, rootID); err != nil {
					return nil, Draft{}, err
				} else if there != nil && there.Message != input.Message {
					return nil, Draft{}, fmt.Errorf("there is a draft there already, which was left as it is: “%s”. Ask the person what to do with it", excerpt(there.Message))
				}
				saved, err := client.Draft(ctx, self.Id, channelID, rootID, input.Message)
				if err != nil {
					return nil, Draft{}, err
				}
				drafts, err := describeDrafts(ctx, client, []*model.Draft{saved})
				if err != nil {
					return nil, Draft{}, err
				}
				drafts[0].Notes = checked.notes
				return nil, drafts[0], nil
			}
		},
	), "only the user sees a draft, nothing is sent until they send it, and a draft already there is never replaced")
}

// describeDrafts is drafts with their channels and teams named, as posts'
// are. GetUser, GetChannel, GetTeam, GetUsersByIds.
func describeDrafts(ctx context.Context, client *mattermost.Client, drafts []*model.Draft) ([]Draft, error) {
	posts := make([]*model.Post, 0, len(drafts))
	for _, draft := range drafts {
		posts = append(posts, &model.Post{ChannelId: draft.ChannelId, UserId: draft.UserId})
	}
	described, err := describePosts(ctx, client, posts)
	if err != nil {
		return nil, err
	}
	out := make([]Draft, 0, len(drafts))
	for i, draft := range drafts {
		out = append(out, Draft{
			ChannelID: draft.ChannelId, Channel: described[i].Channel, Team: described[i].Team,
			RootID: draft.RootId, Message: draft.Message, UpdatedAt: timestamp(draft.UpdateAt),
		})
	}
	return out, nil
}

// Drafts is a page of the user's drafts.
type Drafts struct {
	Drafts []Draft `json:"drafts" jsonschema:"most recently changed first"`
	pageInfo
}

// The most drafts one call returns, and how many when not told.
const (
	maxDrafts     = 100
	defaultDrafts = 20
)

type listDraftsInput struct {
	TeamID string `json:"team_id,omitempty" jsonschema:"only this team's drafts, with those in direct and group messages; every team's when not given"`
	Limit  int    `json:"limit,omitempty" jsonschema:"how many drafts a page holds, at most 100; 20 when not given"`
	pageArgs
}

func listDraftsSpec() Spec {
	return shaping(toolSpec(
		&mcp.Tool{
			Name:        "list_drafts",
			Description: "The drafts in the user's message boxes, in channels and threads, with where each is: what they have started and not sent.",
			Annotations: readOnly("List drafts"),
		},
		uses([]Use{
			{
				Operation: "GetDrafts",
				Params: map[string]Coverage{
					"user_id": Fixed("me", "a user's drafts are theirs"),
					"team_id": SetBy("team_id"),
				},
				Releases: "11.7 serves GET /api/v4/users/{user_id}/teams/{team_id}/drafts, as its router shows, though its specification leaves it out; nothing differs in use",
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "without team_id, every team the user belongs to is read")},
			},
		}, describeUses(true)),
		func(clientFor ClientFor) mcp.ToolHandlerFor[listDraftsInput, Drafts] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input listDraftsInput) (*mcp.CallToolResult, Drafts, error) {
				limit, err := limitOf(input.Limit, defaultDrafts, maxDrafts)
				if err != nil {
					return nil, Drafts{}, err
				}
				at, err := openCursor("list_drafts", input, input.Cursor)
				if err != nil {
					return nil, Drafts{}, err
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					return nil, Drafts{}, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, Drafts{}, err
				}
				teams := []string{input.TeamID}
				if input.TeamID == "" {
					all, err := client.Teams(ctx)
					if err != nil {
						return nil, Drafts{}, err
					}
					teams = teams[:0]
					for _, team := range all {
						teams = append(teams, team.Id)
					}
				}
				// A direct message's draft is listed under every team; keep it once.
				seen := map[string]bool{}
				var drafts []*model.Draft
				for _, team := range teams {
					found, err := client.Drafts(ctx, self.Id, team)
					if err != nil {
						return nil, Drafts{}, err
					}
					for _, draft := range found {
						if key := draft.ChannelId + "/" + draft.RootId; !seen[key] && strings.TrimSpace(draft.Message) != "" {
							seen[key] = true
							drafts = append(drafts, draft)
						}
					}
				}
				slices.SortStableFunc(drafts, func(a, b *model.Draft) int { return int(b.UpdateAt - a.UpdateAt) })
				page, next := offsetPage(drafts, at, limit)
				described, err := describeDrafts(ctx, client, page)
				if err != nil {
					return nil, Drafts{}, err
				}
				return nil, Drafts{Drafts: described, pageInfo: pageInfo{NextCursor: next}}, nil
			}
		},
	), pagingShapes)
}

func deleteDraftSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "delete_draft",
			Description: "Delete the user's draft in a channel or a thread. A draft may hold words the person wrote and has not sent, so " +
				"the person is asked to confirm, seeing the draft; if they decline or close the question, do not try again unless they ask.",
			Annotations: overwrites("Delete draft"),
		},
		uses([]Use{
			{
				Operation: "DeleteDraft",
				Params: map[string]Coverage{
					"user_id":    Fixed("me", "a user's drafts are theirs"),
					"channel_id": SetBy("channel_id"),
				},
				Releases: "11.7 serves DELETE /api/v4/users/{user_id}/channels/{channel_id}/drafts, as its router shows, though its specification leaves it out; nothing differs in use",
			},
			{
				Operation: "DeleteDraftForThread",
				Params: map[string]Coverage{
					"user_id":    Fixed("me", "a user's drafts are theirs"),
					"channel_id": SetBy("channel_id"),
					"thread_id":  SetBy("root_id"),
				},
				Releases: "11.7 serves DELETE /api/v4/users/{user_id}/channels/{channel_id}/drafts/{thread_id}, as its router shows, though its specification leaves it out; nothing differs in use",
			},
		}, draftsUses()),
		func(clientFor ClientFor) mcp.ToolHandlerFor[draftTarget, Draft] {
			find := func(ctx context.Context, client *mattermost.Client, input draftTarget) (*model.User, *model.Draft, error) {
				channelID, rootID, err := threadOf(ctx, client, input.ChannelID, input.RootID)
				if err != nil {
					return nil, nil, err
				}
				self, err := client.Me(ctx)
				if err != nil {
					return nil, nil, err
				}
				teamID, err := teamOf(ctx, client, channelID)
				if err != nil {
					return nil, nil, err
				}
				draft, err := existingDraft(ctx, client, self.Id, teamID, channelID, rootID)
				if err != nil {
					return nil, nil, err
				}
				if draft == nil {
					return nil, nil, fmt.Errorf("there is no draft there; list_drafts lists the user's drafts")
				}
				return self, draft, nil
			}
			return asking("delete_draft",
				func(ctx context.Context, request *mcp.CallToolRequest, input draftTarget) (confirmation, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return confirmation{}, err
					}
					_, draft, err := find(ctx, client, input)
					if err != nil {
						return confirmation{}, err
					}
					where := "this channel"
					if draft.RootId != "" {
						where = "this thread"
					}
					return confirmation{
						Message: fmt.Sprintf("Delete your unsent draft in %s:\n\n%s", where, draft.Message),
						Label:   "Delete the draft and its text",
					}, nil
				},
				func(ctx context.Context, request *mcp.CallToolRequest, input draftTarget) (*mcp.CallToolResult, Draft, error) {
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Draft{}, err
					}
					self, draft, err := find(ctx, client, input)
					if err != nil {
						return nil, Draft{}, err
					}
					if err := client.DeleteDraft(ctx, self.Id, draft.ChannelId, draft.RootId); err != nil {
						return nil, Draft{}, err
					}
					return nil, Draft{ChannelID: draft.ChannelId, RootID: draft.RootId, Message: draft.Message}, nil
				})
		},
	)
}
