package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/mattermost"
)

// A tool's id arguments take what a person gives as readily as an id
// (ADR-030): a channel or a team by its name, with ~ or # before it or not,
// and any of them by the link Mattermost shows for it: a channel's, a direct
// or group message's, a team's, a post's, and a file's, or a file's resource
// address (ADR-029). Every tool registers through toolSpec, which reads these
// arguments into ids before the tool runs, so no tool answers a name with
// Mattermost's "invalid id" and none has to remember to look it up. An
// ambiguous or unknown name is refused as get_channel_info and get_team_info
// refuse it, with the candidates or the closest names.

// The arguments read into ids, by their names in a tool's input.
const (
	channelArg = "channel_id"
	teamArg    = "team_id"
	postArg    = "post_id"
	rootArg    = "root_id"
	fileArg    = "file_id"
)

// bareName is a channel or team argument without the ~ or # a person may
// write before a channel's name.
func bareName(given string) string {
	return strings.TrimLeft(strings.TrimSpace(given), "~#")
}

// resolving reads a tool's id arguments into ids before its handler runs, and
// answers a list the handler left empty as [] rather than null.
//
// A tool that asks the person refuses a client that cannot be asked before
// it reads a name, which reaches Mattermost (ADR-021). On a server configured
// not to ask, skip tells the tool's confirmation to let the call through
// (ADR-033).
func resolving[In, Out any](tool string, handler mcp.ToolHandlerFor[In, Out], clientFor ClientFor, asks, skip bool) mcp.ToolHandlerFor[In, Out] {
	fields := idFields(reflect.TypeFor[In]())
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		var none Out
		if asks && !canConfirm(request) {
			return nil, none, missingElicitation(tool)
		}
		if skip {
			ctx = context.WithValue(ctx, skippingKey{}, true)
		}
		value := reflect.ValueOf(&input).Elem()
		for arg, index := range fields {
			field := value.FieldByIndex(index)
			given := field.String()
			if strings.TrimSpace(given) == "" {
				continue
			}
			resolved, err := resolveID(ctx, request, clientFor, arg, given)
			if err != nil {
				return nil, none, fmt.Errorf("%s: %w", arg, err)
			}
			field.SetString(resolved)
		}
		result, out, err := handler(ctx, request, input)
		if err != nil {
			return result, out, err
		}
		emptyLists(reflect.ValueOf(&out).Elem())
		if links := fileLinks(reflect.ValueOf(out)); result == nil && len(links) > 0 {
			// The answer as the SDK would give it, which it gives only when no
			// content is set, followed by a link to each file it names.
			text, err := json.Marshal(out)
			if err != nil {
				return nil, none, err
			}
			result = &mcp.CallToolResult{Content: append([]mcp.Content{&mcp.TextContent{Text: string(text)}}, links...)}
		}
		return result, out, nil
	}
}

// emptyLists sets each nil list among a struct's fields, embedded ones
// included, to an empty one: a model reads "channels": null as something
// missing, where "channels": [] says that nothing matched.
func emptyLists(value reflect.Value) {
	if value.Kind() != reflect.Struct {
		return
	}
	for i := range value.NumField() {
		field := value.Field(i)
		switch {
		case !value.Type().Field(i).IsExported():
		case field.Kind() == reflect.Slice && field.IsNil():
			field.Set(reflect.MakeSlice(field.Type(), 0, 0))
		case value.Type().Field(i).Anonymous:
			emptyLists(field)
		}
	}
}

// resolveID is the id an argument means.
func resolveID(ctx context.Context, request *mcp.CallToolRequest, clientFor ClientFor, arg, given string) (string, error) {
	if !isLink(given) && mattermostID.MatchString(bareName(given)) {
		return bareName(given), nil
	}
	client, err := clientFor(ctx, request)
	if err != nil {
		return "", err
	}
	switch arg {
	case postArg, rootArg:
		return postFrom(client, given)
	case fileArg:
		return fileFrom(client, given)
	case channelArg:
		// A link with a channel's id in it is taken as the id is.
		if l, ok, err := parseLink(client, given); err != nil {
			return "", err
		} else if ok && l.kind == linkChannel && mattermostID.MatchString(l.name) {
			return l.name, nil
		}
		channel, err := findChannel(ctx, client, given, "")
		return channel.ID, err
	}
	return ownTeam(ctx, client, given)
}

// ownTeam is the id of the user's team a name or a link means. A link's team
// is matched by the whole name in its address, never in part.
func ownTeam(ctx context.Context, client *mattermost.Client, name string) (string, error) {
	l, isLink, err := parseLink(client, name)
	if err != nil {
		return "", err
	}
	teams, err := client.Teams(ctx)
	if err != nil {
		return "", err
	}
	if isLink {
		wanted, err := teamOfLink(l)
		if err != nil {
			return "", err
		}
		if team := teamNamed(teams, wanted); team != nil {
			return team.Id, nil
		}
		return "", fmt.Errorf("%s is in the team %s, which the user is not in; get_user_teams lists the user's teams", l.given, wanted)
	}
	name = bareName(name)
	candidates := make([]named[string], 0, len(teams))
	for _, team := range teams {
		candidates = append(candidates, named[string]{value: team.Id, names: []string{team.DisplayName, team.Name},
			label: fmt.Sprintf("%s (%s, id %s)", team.DisplayName, team.Name, team.Id)})
	}
	return match("team", name, candidates, "get_user_teams lists the user's teams.")
}

// idFields are the string fields of an input type that idArgs names, by
// their argument name, with where each is, embedded structs searched too.
func idFields(input reflect.Type) map[string][]int {
	found := map[string][]int{}
	if input.Kind() != reflect.Struct {
		return found
	}
	var walk func(t reflect.Type, at []int)
	walk = func(t reflect.Type, at []int) {
		for i := range t.NumField() {
			field := t.Field(i)
			index := append(append([]int{}, at...), i)
			if field.Anonymous && field.Type.Kind() == reflect.Struct {
				walk(field.Type, index)
				continue
			}
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			switch name {
			case channelArg, teamArg, postArg, rootArg, fileArg:
				if field.Type.Kind() == reflect.String && field.IsExported() {
					found[name] = index
				}
			}
		}
	}
	walk(input, nil)
	return found
}

// nameUses are the operations reading the id arguments of an input type into
// ids may call, so a tool declares them (ADR-028).
func nameUses(input reflect.Type) []Use {
	fields := idFields(input)
	var out []Use
	if _, ok := fields[channelArg]; ok {
		for _, use := range channelLookupUses(channelArg, Fixed("each of the user's teams", "a channel's name is looked for in every team the user is in")) {
			// An id is taken as it is, so reading a name never reads a channel by id.
			if use.Operation != "GetChannel" {
				out = append(out, use)
			}
		}
	}
	if _, ok := fields[teamArg]; ok {
		out = append(out, Use{
			Operation: "GetTeamsForUser",
			Params:    map[string]Coverage{"user_id": Fixed("me", "a team's name is matched against the user's teams")},
		})
	}
	return out
}

// idNotes is what each id argument's description adds, so the model knows it
// need not look an id up first.
var idNotes = map[string]string{
	channelArg: "its id, its name as in its address or as shown, with ~ or # before it or not, or its link, a direct or group message's included",
	teamArg:    "its id, its name as in its address or as shown, or a link to it or into it",
	postArg:    "its id, or its link",
	rootArg:    "its id, or its link",
	fileArg:    "its id, its address as a resource, mattermost://…/files/<id>, or a link Mattermost gives to it",
}

// inputSchema is the schema the SDK would derive for In, with each id
// argument's description saying what else it takes.
func inputSchema[In any]() *jsonschema.Schema {
	schema, err := jsonschema.ForType(reflect.TypeFor[In](), &jsonschema.ForOptions{})
	if err != nil {
		panic(fmt.Sprintf("the input schema of %v: %v", reflect.TypeFor[In](), err))
	}
	for name, property := range schema.Properties {
		note, ok := idNotes[name]
		if !ok {
			continue
		}
		if property.Description == "" {
			property.Description = note
		} else {
			property.Description += " (" + note + ")"
		}
	}
	return schema
}
