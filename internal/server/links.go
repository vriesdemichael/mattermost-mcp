package server

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// Links, out and in (ADR-030).
//
// Every post, channel and team an answer describes comes with the address it
// opens at in Mattermost's web app, made from names as the app makes it: a
// channel's in its team, /<team>/channels/<name>, a direct message's after the
// person on the other side, /<team>/messages/@<username>. The model cites a
// place by the link it was given and never builds one from an id, which opens
// nothing a person recognises, or the wrong thing: a direct message's id is
// the whole conversation, not the post that was meant.
//
// And every id argument takes such a link, as Mattermost shows it to a person,
// read here into what it names before the tool runs, so whatever is pasted
// works without a lookup tool in between.

// Links out.

// teamLink is the address a team opens at, or empty when it is not known.
func teamLink(client *mattermost.Client, team *model.Team) string {
	if team == nil || team.Name == "" {
		return ""
	}
	return client.Link(team.Name)
}

// channelLink is the address a channel opens at: in its team, or, for a direct
// or group message, which belong to no team, in home, the team their links
// open in. other is the username on the other side of a direct message. Empty
// when what the link is made of is not known.
func channelLink(client *mattermost.Client, channel *model.Channel, team, home *model.Team, other string) string {
	switch channel.Type {
	case model.ChannelTypeDirect:
		if home == nil || other == "" {
			return ""
		}
		return client.Link(home.Name, "messages", "@"+other)
	case model.ChannelTypeGroup:
		if home == nil {
			return ""
		}
		return client.Link(home.Name, "messages", channel.Name)
	}
	if team == nil {
		return ""
	}
	return client.Link(team.Name, "channels", channel.Name)
}

// postLink is the address a post opens at: in the team its channel is in, or
// home for a direct or group message's, as Mattermost's own Copy Link gives
// it; without either, the address Mattermost sends on to a team of the
// person's. A post's link is the one link with an id in it: a post has no name.
func postLink(client *mattermost.Client, postID string, team *model.Team) string {
	if postID == "" || team == nil || team.Name == "" {
		return client.Permalink(postID)
	}
	return client.Link(team.Name, "pl", postID)
}

// homeTeam is the team a direct or group message's link opens in: the first
// of the person's teams as get_user_teams lists them, so a conversation has
// the same link in every answer. Nil when they are in no team.
func homeTeam(teams []*model.Team) *model.Team {
	var home *model.Team
	for _, team := range teams {
		if home == nil || teamOrder(team.DisplayName, team.Name, home.DisplayName, home.Name) < 0 {
			home = team
		}
	}
	return home
}

// teamOrder is the order teams are listed in: by display name, and by the
// name in their address between teams that show alike.
func teamOrder(aDisplay, aName, bDisplay, bName string) int {
	return cmp.Or(strings.Compare(aDisplay, bDisplay), strings.Compare(aName, bName))
}

// Links in.

// linkKind is what a link opens.
type linkKind string

const (
	linkTeam     linkKind = "a team's"
	linkChannel  linkKind = "a channel's"
	linkMessages linkKind = "a direct or group message's"
	linkPost     linkKind = "a post's"
	linkFile     linkKind = "a file's"
)

// link is a Mattermost link read into what it names.
type link struct {
	given string
	kind  linkKind
	// team is the name in the address of the team the link is in; empty for
	// one Mattermost sends on to a team of the person's, and for a file's.
	team string
	// name is a channel's address name, or what follows messages/: @ and a
	// username, a user's id, or a group message's name.
	name string
	// post is the post the link opens at, when it opens at one.
	post string
	// file is the file a file's link or resource address names.
	file string
}

// fileResourceScheme is the scheme of a file's resource address (ADR-029).
const fileResourceScheme = "mattermost"

// isLink reports whether an argument is written as a link, rather than as an
// id or a name.
func isLink(given string) bool {
	lower := strings.ToLower(strings.TrimSpace(given))
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, fileResourceScheme+"://")
}

// parseLink reads a link to this server into what it names. ok is false for
// an argument that is not a link; an error is a link that is to another server
// or opens nothing an id argument can name.
func parseLink(client *mattermost.Client, given string) (link, bool, error) {
	given = strings.TrimSpace(given)
	if !isLink(given) {
		return link{}, false, nil
	}
	server, err := url.Parse(client.Address())
	if err != nil {
		return link{}, true, fmt.Errorf("reading the server's address: %w", err)
	}
	address, err := url.Parse(given)
	if err != nil {
		return link{}, true, fmt.Errorf("the link given is not one that can be read: %w", err)
	}
	// What a message repeats of the link leaves out its query, where a public
	// file link keeps the hash that opens the file to anyone.
	shown := *address
	shown.RawQuery, shown.Fragment, shown.User = "", "", nil
	given = shown.String()
	if strings.EqualFold(address.Scheme, fileResourceScheme) {
		id, ok := fileResourceID(server, address)
		if !ok {
			return link{}, true, fmt.Errorf("%s is not a file's address on this server; a file's is %s", given, fileResource(server, "<file_id>"))
		}
		return link{given: given, kind: linkFile, file: id}, true, nil
	}
	if !sameServer(server, address) {
		return link{}, true, fmt.Errorf("%s is a link to %s, but mm-mcp works with %s; give the name or the id instead",
			given, address.Host, server.Host)
	}
	base := strings.TrimRight(server.Path, "/")
	rest, ok := strings.CutPrefix(address.Path+"/", base+"/")
	if !ok {
		return link{}, true, fmt.Errorf("%s is not a link into Mattermost, which this server serves at %s", given, client.Address())
	}
	var segments []string
	for _, segment := range strings.Split(rest, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	read, ok := readPath(segments)
	if !ok {
		return link{}, true, fmt.Errorf("%s is not a link Mattermost gives to a team, a channel, a direct or group message, a post or a file", given)
	}
	read.given = given
	return read, true, nil
}

// sameServer reports whether a link is to the configured server: the same
// host and port, whichever of http and https it was written with.
func sameServer(server, address *url.URL) bool {
	return strings.EqualFold(server.Hostname(), address.Hostname()) && explicitPort(server) == explicitPort(address)
}

// explicitPort is a URL's port, or empty for its scheme's own.
func explicitPort(address *url.URL) string {
	port := address.Port()
	if (port == "443" && strings.EqualFold(address.Scheme, "https")) || (port == "80" && strings.EqualFold(address.Scheme, "http")) {
		return ""
	}
	return port
}

// readPath is what the path of a link into the web app names, as the app
// routes it.
func readPath(segments []string) (link, bool) {
	id := func(at int) (string, bool) {
		if at >= len(segments) || !mattermostID.MatchString(segments[at]) {
			return "", false
		}
		return segments[at], true
	}
	switch {
	case len(segments) == 0:
		return link{}, false
	case len(segments) == 3 && segments[0] == "_redirect" && segments[1] == "pl":
		post, ok := id(2)
		return link{kind: linkPost, post: post}, ok
	case len(segments) >= 4 && segments[0] == "api" && segments[1] == "v4" && segments[2] == "files":
		file, ok := id(3)
		return link{kind: linkFile, file: file}, ok
	case len(segments) >= 2 && segments[0] == "files":
		file, ok := id(1)
		return link{kind: linkFile, file: file}, ok
	case len(segments) == 1:
		return link{kind: linkTeam, team: segments[0]}, true
	}
	in := link{team: segments[0]}
	switch segments[1] {
	case "pl", "threads":
		post, ok := id(2)
		in.kind, in.post = linkPost, post
		return in, ok && len(segments) == 3
	case "channels", "messages":
		if len(segments) < 3 || len(segments) > 4 {
			return link{}, false
		}
		in.kind, in.name = linkChannel, segments[2]
		if segments[1] == "messages" {
			in.kind = linkMessages
		}
		if len(segments) == 4 {
			post, ok := id(3)
			in.post = post
			return in, ok
		}
		return in, true
	}
	return link{}, false
}

// refuse is the error for a link of another kind than an argument takes.
func (l link) refuse(wanted linkKind, how string) error {
	return fmt.Errorf("%s is %s link, not %s; %s", l.given, l.kind, wanted, how)
}

// postFrom is the post a post argument gives: its id, or a link that opens at
// it.
func postFrom(client *mattermost.Client, given string) (string, error) {
	l, ok, err := parseLink(client, given)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return strings.TrimSpace(given), nil
	case l.post == "":
		return "", l.refuse(linkPost, "a post's link ends with /pl/ and the post's id, as Copy Link on the post gives it")
	}
	return l.post, nil
}

// fileFrom is the file a file argument gives: its id, its resource address,
// or a link Mattermost gives to it.
func fileFrom(client *mattermost.Client, given string) (string, error) {
	l, ok, err := parseLink(client, given)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return strings.TrimSpace(given), nil
	case l.kind != linkFile:
		return "", l.refuse(linkFile, "a post's files and search_files give each file's id and address")
	}
	return l.file, nil
}

// directName and groupName are the shapes of the names Mattermost gives a
// direct message, its two people's ids, and a group message, a hash of theirs.
var (
	directName = regexp.MustCompile(`^[a-z0-9]{26}__[a-z0-9]{26}$`)
	groupName  = regexp.MustCompile(`^[a-z0-9]{40}$`)
)

// channelByLink is the channel a link names, among the user's channels and
// those Mattermost finds by the names in the link.
func channelByLink(ctx context.Context, client *mattermost.Client, mine myChannels, l link) (Channel, error) {
	var channel *model.Channel
	switch l.kind {
	case linkChannel:
		switch {
		case mattermostID.MatchString(l.name):
			found, err := client.Channel(ctx, l.name)
			if err != nil {
				return Channel{}, err
			}
			channel = found
		case directName.MatchString(l.name) || groupName.MatchString(l.name):
			channel = mine.named(l.name)
		}
		if channel == nil {
			found, err := client.ChannelByNames(ctx, l.team, l.name)
			if notFound(err) {
				return Channel{}, fmt.Errorf("no channel is at %s: the team %s has no channel %s, or the user cannot see it. search_channels finds channels by part of their name",
					l.given, l.team, l.name)
			}
			if err != nil {
				return Channel{}, err
			}
			channel = found
		}
	case linkMessages:
		channel = mine.messages(l.name)
		if channel == nil {
			if username, ok := strings.CutPrefix(l.name, "@"); ok {
				return Channel{}, fmt.Errorf("the user has no direct message with @%s yet; dm starts one", username)
			}
			return Channel{}, fmt.Errorf("no direct or group message of the user's is at %s; get_user_channels lists theirs", l.given)
		}
	default:
		return Channel{}, l.refuse(linkChannel, "a channel's link has /channels/ or /messages/ after the team")
	}
	described := mine.describe(channel)
	member := slices.ContainsFunc(mine.channels, func(c *model.Channel) bool { return c.Id == channel.Id })
	described.Member = &member
	return described, nil
}

// named is the user's channel with the given address name, or nil.
func (m myChannels) named(name string) *model.Channel {
	for _, channel := range m.channels {
		if channel.Name == name {
			return channel
		}
	}
	return nil
}

// messages is the user's direct or group message that what follows messages/
// in a link names: @ and the other person's username, their id, or a group
// message's name. Nil when the user has none.
func (m myChannels) messages(identifier string) *model.Channel {
	username, byUsername := strings.CutPrefix(identifier, "@")
	for _, channel := range m.channels {
		switch {
		case channel.Type == model.ChannelTypeDirect && byUsername:
			if strings.EqualFold(m.people[otherInDirect(channel, m.self.Id)], username) {
				return channel
			}
		case channel.Type == model.ChannelTypeDirect && mattermostID.MatchString(identifier):
			if otherInDirect(channel, m.self.Id) == identifier {
				return channel
			}
		case channel.Type == model.ChannelTypeGroup && channel.Name == identifier:
			return channel
		}
	}
	return nil
}

// teamOfLink is the address name of the team a link is in, for a team
// argument: a team's link, or any link inside a team.
func teamOfLink(l link) (string, error) {
	if l.team == "" {
		return "", l.refuse(linkTeam, "a team's link is the server's address followed by the team's name")
	}
	return l.team, nil
}

// teamNamed is the user's team whose address name is name, or nil.
func teamNamed(teams []*model.Team, name string) *model.Team {
	for _, team := range teams {
		if strings.EqualFold(team.Name, name) {
			return team
		}
	}
	return nil
}
