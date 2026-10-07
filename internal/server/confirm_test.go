package server

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The confirmation every write goes through (ADR-021), on a probe tool that
// writes nothing: what the person is asked, and that the tool runs only on a
// ticked acceptance of the very call it was asked about. The write tools' own
// questions, and that a refusal leaves Mattermost unchanged, are the live
// suite's.

type probeInput struct {
	Text string `json:"text"`
}

type probeOutput struct {
	Ran bool `json:"ran"`
}

// probe is a server with one asking tool, and how many times its handler ran.
func probe(t *testing.T) (*mcp.Server, *atomic.Int32) {
	t.Helper()
	ran := &atomic.Int32{}
	server := mcp.NewServer(&mcp.Implementation{Name: "probe", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "probe", Annotations: writes("Probe", false)}, asking("probe",
		func(_ context.Context, _ *mcp.CallToolRequest, input probeInput) (confirmation, error) {
			return confirmation{Message: "Write " + input.Text, Label: "Write " + input.Text}, nil
		},
		func(context.Context, *mcp.CallToolRequest, probeInput) (*mcp.CallToolResult, probeOutput, error) {
			ran.Add(1)
			return nil, probeOutput{Ran: true}, nil
		}))
	return server, ran
}

// connectProbe connects a client to server, speaking version when it is set.
func connectProbe(t *testing.T, server *mcp.Server, options *mcp.ClientOptions, version string) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, options).
		Connect(t.Context(), clientTransport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// answering is a client that answers every question with answer, keeping the
// questions it was asked.
func answering(answer *mcp.ElicitResult, asked *[]*mcp.ElicitParams) *mcp.ClientOptions {
	return &mcp.ClientOptions{ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		*asked = append(*asked, request.Params)
		return answer, nil
	}}
}

var ticked = &mcp.ElicitResult{Action: "accept", Content: map[string]any{confirmKey: true}}

func callProbe(t *testing.T, session *mcp.ClientSession, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	t.Helper()
	if params == nil {
		params = &mcp.CallToolParams{Name: "probe", Arguments: map[string]any{"text": "hello"}}
	}
	return session.CallTool(t.Context(), params)
}

func TestATickedAcceptanceRunsTheToolOnEveryProtocolRevision(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"2026-07-28", "2025-11-25", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			server, ran := probe(t)
			var asked []*mcp.ElicitParams
			result, err := callProbe(t, connectProbe(t, server, answering(ticked, &asked), version), nil)
			if err != nil || result.IsError {
				t.Fatalf("got %v, %+v", err, result)
			}
			if ran.Load() != 1 || len(asked) != 1 {
				t.Fatalf("ran %d times after %d questions; want once after one", ran.Load(), len(asked))
			}
			question := asked[0]
			field := question.RequestedSchema.(map[string]any)["properties"].(map[string]any)[confirmKey].(map[string]any)
			if question.Message != "Write hello" || field["type"] != "boolean" || field["title"] != "Write hello" {
				t.Fatalf("asked %q with %v", question.Message, field)
			}
			if _, hasDefault := field["default"]; hasDefault {
				t.Fatal("the checkbox has a default, so accepting would not take a tick")
			}
		})
	}
}

func TestAnythingButATickedAcceptanceLeavesTheToolUnrun(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]*mcp.ElicitResult{
		"declined":          {Action: "decline"},
		"cancelled":         {Action: "cancel"},
		"accepted unticked": {Action: "accept", Content: map[string]any{confirmKey: false}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server, ran := probe(t)
			var asked []*mcp.ElicitParams
			result, err := callProbe(t, connectProbe(t, server, answering(answer, &asked), ""), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError || ran.Load() != 0 {
				t.Fatalf("ran %d times; result %+v", ran.Load(), result)
			}
			if text := result.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, "Do not call it again") {
				t.Fatalf("the refusal does not tell the model to leave it: %s", text)
			}
		})
	}
}

func TestAClientThatCannotAskIsRefusedWithTheMissingCapabilityError(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"2026-07-28", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			t.Parallel()
			server, ran := probe(t)
			_, err := callProbe(t, connectProbe(t, server, nil, version), nil)
			if code := errorCode(err); code != mcp.CodeMissingRequiredClientCapabilities {
				t.Fatalf("got %v (code %d); want %d", err, code, mcp.CodeMissingRequiredClientCapabilities)
			}
			if ran.Load() != 0 {
				t.Fatal("the tool ran")
			}
		})
	}
}

func errorCode(err error) int64 {
	var rpc *jsonrpc.Error
	if errors.As(err, &rpc) {
		return rpc.Code
	}
	return 0
}

// byHand is a client that can be asked but answers by hand, so a test can send
// the retry it likes.
func byHand(t *testing.T) (*mcp.ClientSession, *atomic.Int32) {
	t.Helper()
	server, ran := probe(t)
	options := &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return nil, errors.New("answered by hand")
		},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	}
	return connectProbe(t, server, options, ""), ran
}

// ask makes the first call and returns its request state.
func ask(t *testing.T, session *mcp.ClientSession, text string) string {
	t.Helper()
	result, err := callProbe(t, session, &mcp.CallToolParams{Name: "probe", Arguments: map[string]any{"text": text}})
	if err != nil || result.RequestState == "" || result.InputRequests[confirmKey] == nil {
		t.Fatalf("the first call did not ask: %v, %+v", err, result)
	}
	return result.RequestState
}

func retry(t *testing.T, session *mcp.ClientSession, text, state string, answer mcp.InputResponse) (*mcp.CallToolResult, error) {
	t.Helper()
	params := &mcp.CallToolParams{Name: "probe", Arguments: map[string]any{"text": text}, RequestState: state}
	if answer != nil {
		params.InputResponses = mcp.InputResponseMap{confirmKey: answer}
	}
	return callProbe(t, session, params)
}

func TestAnAnswerAcceptsOnlyTheCallItWasAskedAboutAndOnlyOnce(t *testing.T) {
	t.Parallel()
	session, ran := byHand(t)

	state := ask(t, session, "hello")
	if _, err := retry(t, session, "something else", state, ticked); errorCode(err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("an answer for another input: %v", err)
	}
	if _, err := retry(t, session, "hello", "", ticked); errorCode(err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("an answer without its state: %v", err)
	}
	payload, mac, _ := strings.Cut(state, ".")
	if _, err := retry(t, session, "hello", payload+"x."+mac, ticked); errorCode(err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("a tampered state: %v", err)
	}
	if ran.Load() != 0 {
		t.Fatal("the tool ran on an answer that was not for it")
	}

	if result, err := retry(t, session, "hello", state, ticked); err != nil || result.IsError {
		t.Fatalf("the honest answer: %v, %+v", err, result)
	}
	if _, err := retry(t, session, "hello", state, ticked); errorCode(err) != jsonrpc.CodeInvalidParams {
		t.Fatalf("the same answer again: %v", err)
	}
	if ran.Load() != 1 {
		t.Fatalf("ran %d times; want once", ran.Load())
	}
}

// The SDK's client checks an answer against the form before sending it; a
// client that does not still gets nowhere with an acceptance left unticked.
func TestAnAcceptanceWithoutTheTickSentByHandLeavesTheToolUnrun(t *testing.T) {
	t.Parallel()
	session, ran := byHand(t)
	state := ask(t, session, "hello")
	result, err := retry(t, session, "hello", state, &mcp.ElicitResult{Action: "accept", Content: map[string]any{}})
	if err != nil || !result.IsError || ran.Load() != 0 {
		t.Fatalf("got %v, %+v, ran %d", err, result, ran.Load())
	}
}

func TestARetryWithoutTheAnswerAsksAgain(t *testing.T) {
	t.Parallel()
	session, ran := byHand(t)
	state := ask(t, session, "hello")
	result, err := retry(t, session, "hello", state, nil)
	if err != nil || result.RequestState == "" || ran.Load() != 0 {
		t.Fatalf("got %v, %+v, ran %d", err, result, ran.Load())
	}
}

func TestALateAcceptanceIsNotActedOnButALateRefusalStands(t *testing.T) {
	t.Parallel()
	seal := newConfirmationSeal()
	state, err := seal.seal("probe", "digest")
	if err != nil {
		t.Fatal(err)
	}
	if expired, err := seal.open(state, "probe", "digest"); err != nil || expired {
		t.Fatalf("a fresh state: expired %v, %v", expired, err)
	}
	seal.now = func() time.Time { return time.Now().Add(confirmationTTL + time.Minute) }
	if expired, err := seal.open(state, "probe", "digest"); err != nil || !expired {
		t.Fatalf("a stale state: expired %v, %v", expired, err)
	}
	if _, err := seal.open(state, "other", "digest"); err == nil {
		t.Fatal("a state opened for another tool")
	}
	if _, err := newConfirmationSeal().open(state, "probe", "digest"); err == nil {
		t.Fatal("a state opened by a server that did not issue it")
	}
}

func TestAConfirmationQuotesTheStartOfAPostOnOneLine(t *testing.T) {
	t.Parallel()
	if got := excerpt("first line\n\n  second"); got != "first line second" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("é", excerptLength+5)
	if got := excerpt(long); got != strings.Repeat("é", excerptLength)+"…" {
		t.Errorf("got %d runes", len([]rune(got)))
	}
}

func TestAnEmojiIsNamedWithoutItsColonsOrByItsCharacter(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		":thumbsup:": "thumbsup", " eyes ": "eyes", "+1": "+1", "Tada": "tada",
		"👍": "+1", "✅": "white_check_mark", "❤️": "heart", "\u2764": "heart", "\u2714": "heavy_check_mark",
	} {
		if got, err := emojiName(in); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "été"} {
		if got, err := emojiName(in); err == nil {
			t.Errorf("%q named %q", in, got)
		}
	}
}

func TestAConfirmationNamesAChannelAsThePersonKnowsIt(t *testing.T) {
	t.Parallel()
	self, other := model.NewId(), model.NewId()
	names := map[string]string{self: "me", other: "bob"}
	for _, c := range []struct {
		channel *model.Channel
		want    string
	}{
		{&model.Channel{Type: model.ChannelTypeOpen, DisplayName: "Town Square"}, "~Town Square"},
		{&model.Channel{Type: model.ChannelTypePrivate, DisplayName: "Secret"}, "~Secret"},
		{&model.Channel{Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(self, other)}, "your direct message with @bob"},
		{&model.Channel{Type: model.ChannelTypeDirect, Name: model.GetDMNameFromIds(self, self)}, "your direct message to yourself"},
		{&model.Channel{Type: model.ChannelTypeGroup, DisplayName: "bob, me, sue"}, "the group message with bob, me, sue"},
	} {
		if got := place(c.channel, self, names); got != c.want {
			t.Errorf("%s %q: got %q, want %q", c.channel.Type, c.channel.DisplayName, got, c.want)
		}
	}
}

// Not parallel: it moves the clock of the one seal every call shares, which no
// parallel test sees, since those wait for the sequential ones to finish.
func TestAnAnswerAfterTheQuestionExpiredRunsNothingAndARefusalStillStands(t *testing.T) {
	session, ran := byHand(t)
	accepted, declined := ask(t, session, "late yes"), ask(t, session, "late no")
	confirmations.now = func() time.Time { return time.Now().Add(confirmationTTL + time.Minute) }
	t.Cleanup(func() { confirmations.now = time.Now })

	result, err := retry(t, session, "late yes", accepted, ticked)
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "expired") {
		t.Fatalf("a late acceptance: %v, %+v", err, result)
	}
	result, err = retry(t, session, "late no", declined, &mcp.ElicitResult{Action: "decline"})
	if err != nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "declined") {
		t.Fatalf("a late refusal: %v, %+v", err, result)
	}
	if ran.Load() != 0 {
		t.Fatal("the tool ran on an answer to a question that had expired")
	}
}

func TestAMentionIsFoundAsMattermostFindsIt(t *testing.T) {
	t.Parallel()
	words := func(message string) []string {
		var out []string
		for _, m := range mentions(message) {
			out = append(out, m.word)
		}
		return out
	}
	for message, want := range map[string][]string{
		"thanks @here.":                {"@here."},
		"_@channel_ and @all-hands":    {"@channel_", "@all-hands"},
		"cc @john/@jane":               {"@john", "@jane"},
		"x.@all and a@b.com":           {"@all"},
		"`@code` and ``a @b``":         nil,
		"~~~\n@dataclass\n~~~":         nil,
		"text\n\n    @Override\n":      nil,
		":smile: @bob: and @bob again": {"@bob:", "@bob"},
	} {
		if got := words(message); !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", message, got, want)
		}
	}
	m := mentions("ping @here.")[0]
	if m.special() != "here" || !slices.Equal(m.names, []string{"here.", "here"}) {
		t.Errorf("@here. reads as %+v", m)
	}
	if special := mentions("@all-hands")[0].special(); special != "" {
		t.Errorf("@all-hands reads as a mention of %s", special)
	}
}
