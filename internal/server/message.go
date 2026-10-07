package server

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"

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

// mentionPattern finds @mentions as Mattermost does: an @ at the start of a
// word, then a username's letters.
var mentionPattern = regexp.MustCompile(`(?i)(?:^|[^\w@./-])@([a-z0-9][a-z0-9._-]*)`)

// codePattern finds code, fenced and inline, in which an @ mentions nobody.
var codePattern = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")

// checkedMessage is what a message will do once posted, for the question.
type checkedMessage struct {
	notes []string
}

// note is what the question says of the message, beside the message itself.
func (c checkedMessage) note() string {
	if len(c.notes) == 0 {
		return ""
	}
	return "\n\n" + strings.Join(c.notes, "\n")
}

// destinationSize says how many people a post in its place reaches, for what
// @channel, @all and @here would notify.
type destinationSize func(context.Context) (int64, error)

// checkMessage refuses a message this server would not take, or that mentions
// someone who does not exist, and says what the rest of its mentions do.
func checkMessage(ctx context.Context, client *mattermost.Client, message, where string, size destinationSize) (checkedMessage, error) {
	if strings.TrimSpace(message) == "" {
		return checkedMessage{}, fmt.Errorf("the message is empty")
	}
	limit, err := maxPostSize(ctx, client)
	if err != nil {
		return checkedMessage{}, err
	}
	if length := utf8.RuneCountInString(message); length > limit {
		return checkedMessage{}, fmt.Errorf("the message is %d characters, and this server takes at most %d. Shorten it, "+
			"post the rest as replies in its thread, or attach the full text as a file and post a summary", length, limit)
	}
	mentioned := mentions(message)
	var checked checkedMessage
	var people []string
	for _, name := range mentioned {
		if who, special := specialMentions[name]; special {
			count, err := size(ctx)
			if err != nil {
				return checkedMessage{}, err
			}
			checked.notes = append(checked.notes, fmt.Sprintf("@%s notifies %s: %s in %s.", name, who, peopleCount(count), where))
			continue
		}
		people = append(people, name)
	}
	if len(people) == 0 {
		return checked, nil
	}
	found, err := mentionedUsers(ctx, client, people)
	if err != nil {
		return checkedMessage{}, err
	}
	for _, name := range people {
		if user := found[name]; user != nil && user.DeleteAt > 0 {
			checked.notes = append(checked.notes, fmt.Sprintf("@%s is deactivated, and will not be notified.", user.Username))
		}
	}
	return checked, nil
}

func peopleCount(n int64) string {
	if n == 1 {
		return "1 person"
	}
	return strconv.FormatInt(n, 10) + " people"
}

// mentions is each name a message @mentions, lower case, once, outside code.
func mentions(message string) []string {
	var names []string
	for _, found := range mentionPattern.FindAllStringSubmatch(codePattern.ReplaceAllString(message, " "), -1) {
		name := strings.ToLower(found[1])
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}

// mentionedUsers reads the users a message mentions, by name. Mattermost
// lets a username end in a dot, a dash or an underscore, so a mention that
// ends a sentence is tried without the punctuation after it. A name nobody has
// is refused with the closest usernames.
func mentionedUsers(ctx context.Context, client *mattermost.Client, names []string) (map[string]*model.User, error) {
	var lookup []string
	for _, name := range names {
		lookup = append(lookup, name)
		if trimmed := strings.TrimRight(name, "._-"); trimmed != name && trimmed != "" {
			lookup = append(lookup, trimmed)
		}
	}
	users, err := client.UsersByUsernames(ctx, lookup)
	if err != nil {
		return nil, err
	}
	byName := map[string]*model.User{}
	for _, user := range users {
		byName[user.Username] = user
	}
	found := map[string]*model.User{}
	var unknown []string
	for _, name := range names {
		switch user := byName[name]; {
		case user != nil:
			found[name] = user
		case byName[strings.TrimRight(name, "._-")] != nil:
			found[name] = byName[strings.TrimRight(name, "._-")]
		default:
			unknown = append(unknown, "@"+name)
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("the message mentions someone nobody is called: %w", unknownUsers(ctx, client, unknown))
	}
	return found, nil
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
