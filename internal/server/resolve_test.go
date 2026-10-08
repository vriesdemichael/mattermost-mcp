package server

import (
	"reflect"
	"strings"
	"testing"
)

// Reading id arguments, where no request is involved (ADR-030).

func TestAPostIsTakenByItsAddressAsByItsID(t *testing.T) {
	t.Parallel()
	const id = "abcdefghijklmnopqrstuvwxyz"
	for given, want := range map[string]string{
		id:             id,
		" " + id + " ": id,
		"https://chat.example.com/_redirect/pl/" + id:         id,
		"https://chat.example.com/team/pl/" + id + "/":        id,
		"https://chat.example.com/sub/team/pl/" + id + "?x=1": id,
		"https://chat.example.com/team/channels/town-square":  "https://chat.example.com/team/channels/town-square",
	} {
		if got := postID(given); got != want {
			t.Errorf("%q: got %q, want %q", given, got, want)
		}
	}
}

func TestAChannelsNameLosesTheSignWrittenBeforeIt(t *testing.T) {
	t.Parallel()
	for given, want := range map[string]string{"~town-square": "town-square", "#Town Square": "Town Square", " general ": "general"} {
		if got := bareName(given); got != want {
			t.Errorf("%q: got %q", given, got)
		}
	}
}

func TestTheIDArgumentsOfAnInputAreFoundInEmbeddedStructsToo(t *testing.T) {
	t.Parallel()
	type embedded struct {
		TeamID string `json:"team_id,omitempty"`
	}
	type input struct {
		ChannelID string `json:"channel_id"`
		embedded
		RootID string `json:"root_id"`
		Count  int    `json:"post_id"`
	}
	got := idFields(reflect.TypeFor[input]())
	want := map[string][]int{"channel_id": {0}, "team_id": {1, 0}, "root_id": {2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAnIDArgumentsDescriptionSaysANameWorksToo(t *testing.T) {
	t.Parallel()
	schema := inputSchema[readChannelInput]()
	if description := schema.Properties["channel_id"].Description; !strings.Contains(description, "its name") {
		t.Errorf("channel_id reads %q", description)
	}
	if description := inputSchema[readPostInput]().Properties["post_id"].Description; !strings.Contains(description, "address") {
		t.Errorf("post_id reads %q", description)
	}
}

func TestAnEmptyListIsAnsweredAsOneNotAsNull(t *testing.T) {
	t.Parallel()
	type page struct{ NextCursor string }
	type answer struct {
		Items []string
		Kept  []string
		page
		Omitted []string `json:"omitted,omitempty"`
		hidden  []string
	}
	out := answer{Kept: []string{"a"}}
	emptyLists(reflect.ValueOf(&out).Elem())
	if out.Items == nil || len(out.Kept) != 1 || out.Omitted == nil || out.hidden != nil {
		t.Fatalf("got %+v", out)
	}
}

func TestAnIDArgumentWithoutADescriptionIsGivenOne(t *testing.T) {
	t.Parallel()
	type bare struct {
		TeamID string `json:"team_id"`
	}
	if description := inputSchema[bare]().Properties["team_id"].Description; !strings.Contains(description, "its name") {
		t.Errorf("team_id reads %q", description)
	}
}

func TestPostsFromSeveralChannelsKeepTheirChannels(t *testing.T) {
	t.Parallel()
	posts := []Post{{ID: "a", ChannelID: "c1", Channel: "One"}, {ID: "b", ChannelID: "c2", Channel: "Two"}}
	channel, team, out := inOneChannel(posts)
	if channel != "" || team != "" || out[0].Channel != "One" || out[1].ChannelID != "c2" {
		t.Errorf("got %q %q %+v", channel, team, out)
	}
	if channel, _, out := inOneChannel(nil); channel != "" || len(out) != 0 {
		t.Errorf("no posts: %q %+v", channel, out)
	}
}
