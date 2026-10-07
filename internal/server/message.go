package server

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/shared/markdown"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// A message the model wrote, checked before anyone is asked to post it (ADR-031):
// its length against what this server takes, and every @mention against the
// people it would notify.

// fallbackMaxPostSize is what a server takes when it does not say: the limit
// of a database whose message column was never widened.
const fallbackMaxPostSize = 4000

// specialMentions notify everyone in a channel, or everyone online in it.
var specialMentions = map[string]string{
	"channel": "everyone in the channel",
	"all":     "everyone in the channel",
	"here":    "everyone in the channel who is online",
}

// checkedMessage is what a message will do once posted, for the question.
type checkedMessage struct {
	notes []string
}

// note is what the question says of the message, before the message itself.
func (c checkedMessage) note() string {
	if len(c.notes) == 0 {
		return ""
	}
	return "\n" + strings.Join(c.notes, "\n")
}

// destinationSize says how many people a post in its place reaches, for what
// @channel, @all and @here would notify.
type destinationSize func(context.Context) (int64, error)

// checkLength refuses a message this server would not take.
// GetClientConfig.
func checkLength(ctx context.Context, client *mattermost.Client, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("the message is empty")
	}
	limit, err := maxPostSize(ctx, client)
	if err != nil {
		return err
	}
	if length := utf8.RuneCountInString(message); length > limit {
		return fmt.Errorf("the message is %d characters, and this server takes at most %d. Shorten it, "+
			"post the rest as replies in its thread, or attach the full text as a file and post a summary", length, limit)
	}
	return nil
}

// checkMessage refuses a message this server would not take, or that mentions
// someone who does not exist, and says what the rest of its mentions do once
// posted. GetClientConfig, GetUsersByUsernames, SearchUsers, and size.
func checkMessage(ctx context.Context, client *mattermost.Client, message, where string, size destinationSize) (checkedMessage, error) {
	if err := checkLength(ctx, client, message); err != nil {
		return checkedMessage{}, err
	}
	var checked checkedMessage
	var lookup []string
	var people []mention
	noted := map[string]bool{}
	for _, m := range mentions(message) {
		if special := m.special(); special != "" {
			if noted[special] {
				continue
			}
			noted[special] = true
			count, err := size(ctx)
			if err != nil {
				return checkedMessage{}, err
			}
			checked.notes = append(checked.notes, fmt.Sprintf("@%s notifies %s: %s in %s.", special, specialMentions[special], peopleCount(count), where))
			continue
		}
		people = append(people, m)
		lookup = append(lookup, m.names...)
	}
	if len(people) == 0 {
		return checked, nil
	}
	users, err := client.UsersByUsernames(ctx, lookup)
	if err != nil {
		return checkedMessage{}, err
	}
	byName := map[string]*model.User{}
	for _, user := range users {
		byName[user.Username] = user
	}
	var unknown []string
	for _, m := range people {
		user := m.user(byName)
		switch {
		case user == nil:
			unknown = append(unknown, m.word)
		case user.DeleteAt > 0 && !noted[user.Id]:
			noted[user.Id] = true
			checked.notes = append(checked.notes, fmt.Sprintf("@%s is deactivated, and will not be notified.", user.Username))
		}
	}
	if len(unknown) > 0 {
		return checkedMessage{}, fmt.Errorf("the message mentions someone nobody is called: %w", unknownUsers(ctx, client, unknown))
	}
	return checked, nil
}

func peopleCount(n int64) string {
	if n == 1 {
		return "1 person"
	}
	return strconv.FormatInt(n, 10) + " people"
}

// mention is a word that may mention someone: the word as written, and the
// names it may mean, the whole word first and then with each trailing . - : _
// taken off in turn, as Mattermost tries them.
type mention struct {
	word  string
	names []string
}

// special is the mention of everyone this is, if it is one.
func (m mention) special() string {
	for _, name := range m.names {
		if _, ok := specialMentions[name]; ok {
			return name
		}
	}
	return ""
}

// user is the user this mentions, by the first of its names someone has.
func (m mention) user(byName map[string]*model.User) *model.User {
	for _, name := range m.names {
		if user := byName[name]; user != nil {
			return user
		}
	}
	return nil
}

// mentions is each word of a message that may mention someone, once, found as
// Mattermost finds them: in the message's text as its Markdown parser reads it,
// so that code of any kind mentions nobody, split into words where a username
// cannot go on, and a word that does not start with @ split again at . - and :,
// as at the end of a sentence (getExplicitMentions and ProcessText in
// Mattermost's server).
func mentions(message string) []mention {
	var found []mention
	seen := map[string]bool{}
	add := func(word string) {
		if !strings.HasPrefix(word, "@") || len(word) < 2 || seen[strings.ToLower(word)] {
			return
		}
		seen[strings.ToLower(word)] = true
		name := strings.ToLower(word[1:])
		m := mention{word: word, names: []string{name}}
		for trimmed := name; trimmed != "" && strings.LastIndexAny(trimmed, ".-:_") == len(trimmed)-1; {
			trimmed = trimmed[:len(trimmed)-1]
			if trimmed != "" {
				m.names = append(m.names, trimmed)
			}
		}
		found = append(found, m)
	}
	process := func(text string) {
		for _, word := range strings.FieldsFunc(text, func(c rune) bool {
			return !(c == ':' || c == '.' || c == '-' || c == '_' || c == '@' || unicode.IsLetter(c) || unicode.IsNumber(c))
		}) {
			// :word: is an emoji.
			if len(word) > 1 && word[0] == ':' && word[len(word)-1] == ':' {
				continue
			}
			word = strings.TrimLeft(word, ":.-_")
			if strings.HasPrefix(word, "@") {
				add(word)
				continue
			}
			for _, part := range strings.FieldsFunc(word, func(c rune) bool { return c == '.' || c == '-' || c == ':' }) {
				add(part)
			}
		}
	}
	buffer := ""
	markdown.Inspect(message, func(node any) bool {
		text, ok := node.(*markdown.Text)
		if !ok {
			if buffer != "" {
				process(buffer)
			}
			buffer = ""
			return true
		}
		buffer += text.Text
		return false
	})
	if buffer != "" {
		process(buffer)
	}
	return found
}

// maxPostSize is the most characters this server takes in one message, as it
// tells its clients. GetClientConfig.
func maxPostSize(ctx context.Context, client *mattermost.Client) (int, error) {
	config, err := client.ClientConfig(ctx)
	if err != nil {
		return 0, err
	}
	if size, err := strconv.Atoi(config["MaxPostSize"]); err == nil && size > 0 {
		return size, nil
	}
	return fallbackMaxPostSize, nil
}

// lengthUses are the operations checkLength calls.
func lengthUses() []Use {
	return []Use{{Operation: "GetClientConfig", Params: map[string]Coverage{}}}
}

// messageUses are the operations checkMessage calls. stats says whether a
// mention of everyone is counted by reading the channel's stats, which only a
// channel that exists has.
func messageUses(stats bool) []Use {
	out := []Use{
		{Operation: "GetClientConfig", Params: map[string]Coverage{}},
		{Operation: "GetUsersByUsernames", Params: map[string]Coverage{}},
		{Operation: "SearchUsers", Params: suggestionSearch(Fixed("the start of an unknown mention", "enough to find the usernames closest to one that is unknown"))},
	}
	if stats {
		out = append(out, Use{
			Operation: "GetChannelStats",
			Params:    map[string]Coverage{"channel_id": Fixed("the post's channel", "the question says how many people @channel, @all and @here reach")},
		})
	}
	return out
}

// suggestionSearch is SearchUsers as the tools call it to suggest the usernames
// closest to one nobody has: term sets what is searched for.
func suggestionSearch(term Coverage) map[string]Coverage {
	return map[string]Coverage{
		"body.term":              term,
		"body.limit":             Fixed("20", "enough to suggest the closest usernames"),
		"body.allow_inactive":    Omitted("a suggestion is someone who can still be reached"),
		"body.team_id":           Omitted("a suggestion may be anyone the user can see"),
		"body.not_in_team_id":    Omitted("a suggestion may be anyone the user can see"),
		"body.in_channel_id":     Omitted("a suggestion may be anyone the user can see"),
		"body.not_in_channel_id": Omitted("a suggestion may be anyone the user can see"),
		"body.in_group_id":       Omitted("groups are a licensed edition's feature, which the tests cannot reach (ADR-007)"),
		"body.group_constrained": Omitted("a filter for administering group-synced teams"),
		"body.without_team":      Omitted("a filter for administering users"),
	}
}

// aiMarker is the property that marks a post as written with AI, holding the
// user it was written for, which Mattermost shows as AI-generated (ADR-031).
func aiMarker(on bool, userID string) model.StringInterface {
	if !on {
		return nil
	}
	return model.StringInterface{model.PostPropsAIGeneratedByUserID: userID}
}

// aiNote is what the question says of the marker.
func aiNote(on bool) string {
	if !on {
		return ""
	}
	return "\n\nThe post will be marked as written with AI."
}
