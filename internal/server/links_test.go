package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
	"github.com/vriesdemichael/mm-mcp/internal/network"
)

// Links out and in, where no request is involved (ADR-030): a link is made
// from names, and read from what it says, before anything reaches Mattermost.

const (
	someID  = "abcdefghijklmnopqrstuvwxyz"
	otherID = "zyxwvutsrqponmlkjihgfedcba"
)

// clientAt is a client for the server at address, which these tests never let
// reach it.
func clientAt(address string) *mattermost.Client {
	return mattermost.New(address, "t", network.NewSafeTransport())
}

func TestAPostIsTakenByItsLinkAsByItsID(t *testing.T) {
	t.Parallel()
	for address, cases := range map[string]map[string]string{
		"https://chat.example.com": {
			someID:             someID,
			" " + someID + " ": someID,
			"https://chat.example.com/_redirect/pl/" + someID:                    someID,
			"https://chat.example.com/team/pl/" + someID + "/":                   someID,
			"https://chat.example.com/team/pl/" + someID + "?x=1#y":              someID,
			"https://chat.example.com/team/threads/" + someID:                    someID,
			"https://chat.example.com/team/channels/town-square/" + someID:       someID,
			"https://chat.example.com/team/messages/@bob/" + someID:              someID,
			"HTTPS://Chat.Example.COM/team/pl/" + someID:                         someID,
			"http://chat.example.com/team/pl/" + someID:                          someID,
			"https://chat.example.com:443/team/pl/" + someID:                     someID,
			"https://chat.example.com/team/channels/town-square/" + someID + "/": someID,
		},
		"https://chat.example.com/sub/": {
			"https://chat.example.com/sub/team/pl/" + someID:         someID,
			"https://chat.example.com/sub/_redirect/pl/" + someID:    someID,
			"https://chat.example.com/sub/team/threads/" + someID:    someID,
			"https://chat.example.com/sub/team/messages/x/" + someID: someID,
		},
	} {
		client := clientAt(address)
		for given, want := range cases {
			if got, err := postFrom(client, given); err != nil || got != want {
				t.Errorf("%s, %q: got %q, %v; want %q", address, given, got, err, want)
			}
		}
	}
}

func TestALinkToAnotherServerOrNoPostIsRefusedForAPost(t *testing.T) {
	t.Parallel()
	client := clientAt("https://chat.example.com/sub")
	for given, want := range map[string]string{
		"https://other.example.com/team/pl/" + someID:       "mm-mcp works with chat.example.com",
		"https://chat.example.com:8443/team/pl/" + someID:   "is a link to chat.example.com:8443",
		"https://chat.example.com/team/pl/" + someID:        "not a link into Mattermost",
		"https://chat.example.com/sub/team/channels/town":   "is a channel's link, not a post's",
		"https://chat.example.com/sub/team":                 "is a team's link, not a post's",
		"https://chat.example.com/sub/team/pl/not-an-id":    "not a link Mattermost gives",
		"https://chat.example.com/sub/admin_console/users":  "not a link Mattermost gives",
		"mattermost://chat.example.com/sub/files/" + someID: "is a file's link, not a post's",
	} {
		_, err := postFrom(client, given)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v; want an error saying %q", given, err, want)
		}
	}
}

func TestALinkIsReadIntoWhatItNames(t *testing.T) {
	t.Parallel()
	client := clientAt("https://chat.example.com")
	for given, want := range map[string]link{
		"https://chat.example.com/team":                                    {kind: linkTeam, team: "team"},
		"https://chat.example.com/team/":                                   {kind: linkTeam, team: "team"},
		"https://chat.example.com/team/channels/town-square":               {kind: linkChannel, team: "team", name: "town-square"},
		"https://chat.example.com/team/channels/" + someID:                 {kind: linkChannel, team: "team", name: someID},
		"https://chat.example.com/team/messages/@bob.smith":                {kind: linkMessages, team: "team", name: "@bob.smith"},
		"https://chat.example.com/team/messages/" + someID + "/" + otherID: {kind: linkMessages, team: "team", name: someID, post: otherID},
		"https://chat.example.com/team/pl/" + someID:                       {kind: linkPost, team: "team", post: someID},
		"https://chat.example.com/_redirect/pl/" + someID:                  {kind: linkPost, post: someID},
		"https://chat.example.com/api/v4/files/" + someID + "/preview":     {kind: linkFile, file: someID},
		"https://chat.example.com/files/" + someID + "/public?h=x":         {kind: linkFile, file: someID},
		"mattermost://chat.example.com/files/" + someID:                    {kind: linkFile, file: someID},
		"mattermost://CHAT.example.com/files/" + someID:                    {kind: linkFile, file: someID},
	} {
		got, ok, err := parseLink(client, given)
		want.given, _, _ = strings.Cut(strings.TrimSpace(given), "?")
		if err != nil || !ok || got != want {
			t.Errorf("%q: got %+v, %v, %v; want %+v", given, got, ok, err, want)
		}
	}
	for _, notALink := range []string{"town-square", "~town-square", "#Town Square", someID, "chat.example.com/team"} {
		if _, ok, err := parseLink(client, notALink); ok || err != nil {
			t.Errorf("%q was read as a link: %v", notALink, err)
		}
	}
	for _, wrong := range []string{
		"https://chat.example.com/team/channels",
		"https://chat.example.com/team/channels/a/b/c",
		"https://chat.example.com/team/pl/" + someID + "/more",
		"https://chat.example.com/",
		"https://chat.example.com",
		"mattermost://chat.example.com/files/not-an-id",
		"mattermost://other.example.com/files/" + someID,
	} {
		if _, ok, err := parseLink(client, wrong); !ok || err == nil {
			t.Errorf("%q was not refused", wrong)
		}
	}
}

// A refusal repeats a link without its query, where a public file link keeps
// the hash that opens the file to anyone.
func TestARefusalRepeatsNoLinksQuery(t *testing.T) {
	t.Parallel()
	_, err := fileFrom(clientAt("https://chat.example.com"), "https://other.example.com/files/"+someID+"/public?h=secret#x")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "other.example.com/files/"+someID+"/public") {
		t.Errorf("got %v", err)
	}
}

func TestAFileIsTakenByItsAddressOrItsLink(t *testing.T) {
	t.Parallel()
	client := clientAt("http://localhost:8065/mm")
	for given, want := range map[string]string{
		someID: someID,
		"mattermost://localhost:8065/mm/files/" + someID:       someID,
		"http://localhost:8065/mm/api/v4/files/" + someID:      someID,
		"http://localhost:8065/mm/files/" + someID + "/public": someID,
	} {
		if got, err := fileFrom(client, given); err != nil || got != want {
			t.Errorf("%q: got %q, %v", given, got, err)
		}
	}
	for _, refused := range []string{
		"mattermost://localhost:8065/files/" + someID,
		"mattermost://localhost/mm/files/" + someID,
		"http://localhost:8065/mm/team/pl/" + someID,
	} {
		if _, err := fileFrom(client, refused); err == nil {
			t.Errorf("%q was taken", refused)
		}
	}
	if got := fileURI(client, someID); got != "mattermost://localhost:8065/mm/files/"+someID {
		t.Errorf("a file's address: %q", got)
	}
}

func TestALinkIsMadeFromNames(t *testing.T) {
	t.Parallel()
	client := clientAt("https://chat.example.com/sub/")
	team := &model.Team{Name: "eng", DisplayName: "Engineering"}
	home := &model.Team{Name: "home", DisplayName: "Home"}
	for _, c := range []struct {
		channel *model.Channel
		other   string
		want    string
	}{
		{&model.Channel{Name: "town-square", Type: model.ChannelTypeOpen, TeamId: "t"}, "", "https://chat.example.com/sub/eng/channels/town-square"},
		{&model.Channel{Name: "secret", Type: model.ChannelTypePrivate, TeamId: "t"}, "", "https://chat.example.com/sub/eng/channels/secret"},
		{&model.Channel{Name: someID + "__" + otherID, Type: model.ChannelTypeDirect}, "bob.smith", "https://chat.example.com/sub/home/messages/@bob.smith"},
		{&model.Channel{Name: strings.Repeat("a", 40), Type: model.ChannelTypeGroup}, "", "https://chat.example.com/sub/home/messages/" + strings.Repeat("a", 40)},
	} {
		if got := channelLink(client, c.channel, team, home, c.other); got != c.want {
			t.Errorf("%s: got %q, want %q", c.channel.Name, got, c.want)
		}
	}
	// What a link is made of missing, there is none rather than one with an id.
	if got := channelLink(client, &model.Channel{Name: "x", Type: model.ChannelTypeDirect}, nil, home, ""); got != "" {
		t.Errorf("a direct message without the other's username: %q", got)
	}
	if got := channelLink(client, &model.Channel{Name: "x", Type: model.ChannelTypeGroup}, nil, nil, ""); got != "" {
		t.Errorf("a group message without a team: %q", got)
	}
	if got := channelLink(client, &model.Channel{Name: "x", Type: model.ChannelTypeOpen}, nil, home, ""); got != "" {
		t.Errorf("a channel whose team is not known: %q", got)
	}
	if got := teamLink(client, team); got != "https://chat.example.com/sub/eng" {
		t.Errorf("a team: %q", got)
	}
	if got := postLink(client, someID, team); got != "https://chat.example.com/sub/eng/pl/"+someID {
		t.Errorf("a post in a team: %q", got)
	}
	if got := postLink(client, someID, nil); got != "https://chat.example.com/sub/_redirect/pl/"+someID {
		t.Errorf("a post without a team: %q", got)
	}
	if got := postLink(client, "", team); got != "" {
		t.Errorf("no post: %q", got)
	}
	if got := client.Link("a b", "c/d"); got != "https://chat.example.com/sub/a%20b/c%2Fd" {
		t.Errorf("escaping: %q", got)
	}
}

func TestDirectMessagesOpenInThePersonsFirstTeam(t *testing.T) {
	t.Parallel()
	teams := []*model.Team{
		{Name: "zeta", DisplayName: "Beta"},
		{Name: "gamma", DisplayName: "Gamma"},
		{Name: "alpha", DisplayName: "Beta"},
	}
	if home := homeTeam(teams); home == nil || home.Name != "alpha" {
		t.Errorf("got %+v", home)
	}
	if home := homeTeam(nil); home != nil {
		t.Errorf("no teams: %+v", home)
	}
}

func TestAnAnswerLinksEachFileItNamesOnce(t *testing.T) {
	t.Parallel()
	a := Attachment{ID: someID, URI: "mattermost://h/files/" + someID, Name: "a.txt", Size: 3, MIMEType: "text/plain"}
	b := Attachment{ID: otherID, URI: "mattermost://h/files/" + otherID}
	answer := struct {
		Post   *Post
		Thread []Post
		Files  []FoundFile
		capped
		pageInfo
	}{
		Post:   &Post{Files: []Attachment{a}},
		Thread: []Post{{Files: []Attachment{a, b}}},
		Files:  []FoundFile{{Attachment: b}},
	}
	links := fileLinks(reflect.ValueOf(answer))
	if len(links) != 2 {
		t.Fatalf("got %d links", len(links))
	}
	first, second := links[0].(*mcp.ResourceLink), links[1].(*mcp.ResourceLink)
	if first.URI != a.URI || first.Name != "a.txt" || first.Size == nil || *first.Size != 3 || first.MIMEType != "text/plain" {
		t.Errorf("first: %+v", first)
	}
	if second.URI != b.URI || second.Name != otherID || second.Size != nil {
		t.Errorf("second, known by its id alone: %+v", second)
	}
}

func TestAnAnswerNamingFilesCarriesTheirLinksAfterItself(t *testing.T) {
	t.Parallel()
	type input struct{}
	file := Attachment{ID: someID, URI: "mattermost://h/files/" + someID, Name: "a.txt"}
	handler := func(context.Context, *mcp.CallToolRequest, input) (*mcp.CallToolResult, Post, error) {
		return nil, Post{ID: otherID, Files: []Attachment{file}}, nil
	}
	result, out, err := resolving("t", handler, nil, false, false)(t.Context(), &mcp.CallToolRequest{}, input{})
	if err != nil || out.ID != otherID {
		t.Fatalf("got %+v, %v", out, err)
	}
	if result == nil || len(result.Content) != 2 {
		t.Fatalf("got %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	var back Post
	if !ok || json.Unmarshal([]byte(text.Text), &back) != nil || back.ID != otherID {
		t.Errorf("the answer itself: %+v", result.Content[0])
	}
	if link, ok := result.Content[1].(*mcp.ResourceLink); !ok || link.URI != file.URI {
		t.Errorf("the file's link: %+v", result.Content[1])
	}

	// An answer without files is left for the SDK to give as it does.
	plain := func(context.Context, *mcp.CallToolRequest, input) (*mcp.CallToolResult, Post, error) {
		return nil, Post{ID: otherID}, nil
	}
	if result, _, _ := resolving("t", plain, nil, false, false)(t.Context(), &mcp.CallToolRequest{}, input{}); result != nil {
		t.Errorf("got %+v", result)
	}
}
