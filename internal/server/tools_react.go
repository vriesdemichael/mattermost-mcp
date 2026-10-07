package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Reactions, by an emoji's name or the emoji itself. Mattermost refuses a name
// it does not know; the tool refuses it first, with the closest names, so the
// model corrects itself on the next call (ADR-030).

// Reaction is a reaction the user added or removed.
type Reaction struct {
	PostID    string `json:"post_id"`
	EmojiName string `json:"emoji_name"`
}

type reactionInput struct {
	PostID string `json:"post_id" jsonschema:"the post to react to"`
	Emoji  string `json:"emoji" jsonschema:"the emoji: its name, such as thumbsup, white_check_mark or eyes, with or without colons, or the emoji itself, such as 👍"`
}

// systemEmojiNames maps each of Mattermost's own emoji, by its code points as
// Mattermost writes them, to its names.
var systemEmojiNames = sync.OnceValue(func() map[string][]string {
	byCode := map[string][]string{}
	for name, code := range model.SystemEmojis {
		byCode[code] = append(byCode[code], name)
	}
	for _, names := range byCode {
		slices.Sort(names)
	}
	return byCode
})

// emojiCodes are an emoji character as Mattermost may name its code points:
// each in lower-case hex, joined by dashes, as given, without the variation
// selector that asks for emoji presentation, and with it after the first,
// since Mattermost keeps some emoji one way and some the other, and people
// type them both ways.
func emojiCodes(emoji string) []string {
	var as, without []string
	for _, r := range emoji {
		as = append(as, fmt.Sprintf("%x", r))
		if r != '\ufe0f' {
			without = append(without, fmt.Sprintf("%x", r))
		}
	}
	withSelector := append([]string{without[0], "fe0f"}, without[1:]...)
	return []string{strings.Join(as, "-"), strings.Join(without, "-"), strings.Join(withSelector, "-")}
}

// emojiAliases are the names of the same emoji as name: the name itself, and
// every other name Mattermost gives the same code points, such as +1 and
// thumbsup.
func emojiAliases(name string) []string {
	code, ok := model.SystemEmojis[name]
	if !ok {
		return []string{name}
	}
	return systemEmojiNames()[code]
}

// emojiName is an emoji's name as Mattermost stores it: a name without the
// colons a message writes around it, or the name of an emoji character.
func emojiName(emoji string) (string, error) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		return "", fmt.Errorf("give the emoji, by its name or as itself")
	}
	if strings.IndexFunc(emoji, func(r rune) bool { return r > unicode.MaxASCII }) < 0 {
		return strings.ToLower(strings.Trim(emoji, ":")), nil
	}
	if len([]rune(emoji)) == 0 {
		return "", fmt.Errorf("give the emoji, by its name or as itself")
	}
	for _, code := range emojiCodes(emoji) {
		if names := systemEmojiNames()[code]; len(names) > 0 {
			return names[0], nil
		}
	}
	return "", fmt.Errorf("%s is not an emoji Mattermost has; give one by its name, such as thumbsup", emoji)
}

// knownEmoji refuses an emoji name Mattermost would refuse: one neither of its
// own nor a custom emoji of the server's, with the closest names of both.
// GetEmojiByName, AutocompleteEmoji.
func knownEmoji(ctx context.Context, client *mattermost.Client, name string) error {
	if model.IsSystemEmojiName(name) {
		return nil
	}
	if _, err := client.Emoji(ctx, name); err == nil {
		return nil
	} else if !notFound(err) {
		return err
	}
	candidates := make([]string, 0, len(model.SystemEmojis))
	for system := range model.SystemEmojis {
		candidates = append(candidates, system)
	}
	if custom, err := client.AutocompleteEmoji(ctx, firstWord(name)); err == nil {
		for _, emoji := range custom {
			candidates = append(candidates, emoji.Name)
		}
	}
	if near := closest(name, candidates, 5); len(near) > 0 {
		return fmt.Errorf("no emoji is called %q on this server; the closest are %s", name, quoteAll(near))
	}
	return fmt.Errorf("no emoji is called %q on this server", name)
}

// reaction is the name the user reacted to the post with under the emoji, or
// any other name of it, or empty when they did not: a reaction made as
// thumbsup is taken back when asked for +1, or for 👍.
func reaction(p postInContext, emoji string) string {
	if p.post.Metadata == nil {
		return ""
	}
	aliases := emojiAliases(emoji)
	for _, reaction := range p.post.Metadata.Reactions {
		if reaction.UserId == p.self.Id && slices.Contains(aliases, reaction.EmojiName) {
			return reaction.EmojiName
		}
	}
	return ""
}

// reactionQuestion reads the post a reaction is about and asks what ask builds
// from it.
func reactionQuestion(clientFor ClientFor, ask func(context.Context, *mattermost.Client, postInContext, string) (confirmation, error)) func(context.Context, *mcp.CallToolRequest, reactionInput) (confirmation, error) {
	return func(ctx context.Context, request *mcp.CallToolRequest, input reactionInput) (confirmation, error) {
		emoji, err := emojiName(input.Emoji)
		if err != nil {
			return confirmation{}, err
		}
		client, err := clientFor(ctx, request)
		if err != nil {
			return confirmation{}, err
		}
		p, err := readPostInContext(ctx, client, input.PostID)
		if err != nil {
			return confirmation{}, err
		}
		return ask(ctx, client, p, emoji)
	}
}

func addReactionSpec() Spec {
	return toolSpec(
		&mcp.Tool{
			Name: "add_reaction",
			Description: "React to a post with an emoji, given by name or as the emoji itself, under the user's name. An unknown name " +
				"is refused with the closest ones. The person is asked to confirm each reaction, seeing the emoji and the post, before it " +
				"is added; if they decline or close the question, do not react again unless they ask.",
			Annotations: writes("Add reaction", true),
		},
		uses([]Use{
			{
				Operation: "SaveReaction",
				Params: map[string]Coverage{
					"body.post_id":    SetBy("post_id"),
					"body.emoji_name": SetBy("emoji"),
					"body.user_id":    Fixed("the user's own id", "a user reacts as themselves; Mattermost refuses a reaction for anyone else"),
					"body.create_at":  Omitted("Mattermost records when the reaction was made"),
				},
			},
			{Operation: "GetEmojiByName", Params: map[string]Coverage{"emoji_name": SetBy("emoji")}},
			{Operation: "AutocompleteEmoji", Params: map[string]Coverage{"name": Fixed("the start of an unknown name", "finds the server's custom emoji closest to it")}},
		}, postContextUses()),
		func(clientFor ClientFor) mcp.ToolHandlerFor[reactionInput, Reaction] {
			return asking("add_reaction",
				reactionQuestion(clientFor, func(ctx context.Context, client *mattermost.Client, p postInContext, emoji string) (confirmation, error) {
					if err := knownEmoji(ctx, client, emoji); err != nil {
						return confirmation{}, err
					}
					return confirmation{
						Message: fmt.Sprintf("React as @%s with :%s: to @%s's post in %s:\n“%s”",
							p.self.Username, emoji, p.author, p.in, excerpt(p.post.Message)),
						Label: fmt.Sprintf("Add :%s: to @%s's post", emoji, p.author),
					}, nil
				}),
				func(ctx context.Context, request *mcp.CallToolRequest, input reactionInput) (*mcp.CallToolResult, Reaction, error) {
					emoji, err := emojiName(input.Emoji)
					if err != nil {
						return nil, Reaction{}, err
					}
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Reaction{}, err
					}
					self, err := client.Me(ctx)
					if err != nil {
						return nil, Reaction{}, err
					}
					reaction, err := client.React(ctx, self.Id, input.PostID, emoji)
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
			Description: "Take back one of the user's reactions to a post, given by name or as the emoji itself. The person is asked to " +
				"confirm, seeing the emoji and the post; if they decline or close the question, do not try again unless they ask.",
			Annotations: overwrites("Remove reaction"),
		},
		uses([]Use{{
			Operation: "DeleteReaction",
			Params: map[string]Coverage{
				"user_id":    Fixed("me", "a user takes back only their own reaction"),
				"post_id":    SetBy("post_id"),
				"emoji_name": SetBy("emoji"),
			},
		}}, postContextUses()),
		func(clientFor ClientFor) mcp.ToolHandlerFor[reactionInput, Reaction] {
			return asking("remove_reaction",
				reactionQuestion(clientFor, func(_ context.Context, _ *mattermost.Client, p postInContext, emoji string) (confirmation, error) {
					made := reaction(p, emoji)
					if made == "" {
						return confirmation{}, fmt.Errorf("@%s has not reacted with :%s: to that post", p.self.Username, emoji)
					}
					return confirmation{
						Message: fmt.Sprintf("Take back your :%s: on @%s's post in %s:\n“%s”", made, p.author, p.in, excerpt(p.post.Message)),
						Label:   fmt.Sprintf("Remove your :%s: from @%s's post", made, p.author),
					}, nil
				}),
				func(ctx context.Context, request *mcp.CallToolRequest, input reactionInput) (*mcp.CallToolResult, Reaction, error) {
					emoji, err := emojiName(input.Emoji)
					if err != nil {
						return nil, Reaction{}, err
					}
					client, err := clientFor(ctx, request)
					if err != nil {
						return nil, Reaction{}, err
					}
					p, err := readPostInContext(ctx, client, input.PostID)
					if err != nil {
						return nil, Reaction{}, err
					}
					made := reaction(p, emoji)
					if made == "" {
						return nil, Reaction{}, fmt.Errorf("@%s has not reacted with :%s: to that post", p.self.Username, emoji)
					}
					if err := client.Unreact(ctx, p.self.Id, input.PostID, made); err != nil {
						return nil, Reaction{}, err
					}
					return nil, Reaction{PostID: input.PostID, EmojiName: made}, nil
				})
		},
	)
}
