package server

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
)

// Every tool that writes asks the person to confirm each call before it acts,
// through the MCP client (ADR-021). The question is an elicitation with one
// required checkbox and no default, so accepting takes a tick: a client whose
// approvals are set to automatic accepts a form with no fields without anyone
// seeing it. A client that cannot show a form is refused with the error MCP
// defines for a missing client capability, and nothing is written.
//
// A server configured with MM_MCP_ASK_BEFORE_WRITES=false asks nothing, and
// leaves the check to the client's own approval of the tool call (ADR-033).
//
// A client on the 2026-07-28 revision sees two calls: the first answers with
// the form and a signed request state, and the retry carries the answer and
// the state back. A client on an earlier revision sees one call, during which
// the SDK sends the form itself and runs the handler again with the answer.

// confirmKey names the form's one field, and the answer among a retried call's
// input responses.
const confirmKey = "confirm"

// confirmationTTL bounds how long an unanswered confirmation stays valid. It
// covers a person reading the question, not a call parked for later.
const confirmationTTL = 10 * time.Minute

// confirmation is what the person is asked.
type confirmation struct {
	// Message says what will happen, as the client shows it.
	Message string
	// Label is the checkbox's title. It names what will be done, so a tick made
	// out of habit still passes over it.
	Label string
}

// asking wraps a write tool's handler so that a call runs only once the person
// has accepted it. confirm builds the question from the call's input, reading
// Mattermost for what the person must see that the input does not carry; an
// error ends the call before anyone is asked.
func asking[In, Out any](tool string, confirm func(context.Context, *mcp.CallToolRequest, In) (confirmation, error), handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return askingBound(tool, nil, confirm, handler)
}

// askingBound is asking for a call whose input names something that can
// change between the question and the answer: a file on disk, the post to be
// deleted and the replies that go with it, the person a username means. bind
// fingerprints what the call acts on, and an answer accepts the call only
// while the fingerprint is what it was when the person was asked. The handler
// is given the fingerprint (see stillAsAsked), so it can check what it is
// about to act on against it once more, after the answer.
func askingBound[In, Out any](tool string, bind func(context.Context, *mcp.CallToolRequest, In) (string, error), confirm func(context.Context, *mcp.CallToolRequest, In) (confirmation, error), handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		var none Out
		if skipping(ctx) {
			return handler(ctx, request, input)
		}
		if !canConfirm(request) {
			return nil, none, missingElicitation(tool)
		}
		digest, err := inputDigest(input)
		if err != nil {
			return nil, none, fmt.Errorf("%s: %w", tool, err)
		}
		if bind != nil {
			bound, err := bind(ctx, request, input)
			if err != nil {
				return nil, none, err
			}
			sum := sha256.Sum256([]byte(digest + "\n" + bound))
			digest = hex.EncodeToString(sum[:])
			ctx = context.WithValue(ctx, boundKey{}, bound)
		}
		answer, answered := request.Params.InputResponses[confirmKey]
		state := request.Params.RequestState

		ask := func() (*mcp.CallToolResult, Out, error) {
			question, err := confirm(ctx, request, input)
			if err != nil {
				return nil, none, err
			}
			sealed, err := confirmations.seal(tool, digest)
			if err != nil {
				return nil, none, fmt.Errorf("%s: %w", tool, err)
			}
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{confirmKey: confirmationForm(question)},
				RequestState:  sealed,
			}, none, nil
		}

		switch {
		case state == "" && answered:
			// An answer with no question behind it would let a client accept a
			// call nobody was shown.
			return nil, none, invalidConfirmation("the answer came without the request state its question carried")
		case state == "":
			return ask()
		}
		expired, err := confirmations.open(state, tool, digest)
		if err != nil {
			return nil, none, err
		}
		if !answered {
			// The client retried without the answer; the specification asks the
			// server to ask again rather than fail.
			return ask()
		}
		result, ok := answer.(*mcp.ElicitResult)
		if !ok {
			return nil, none, invalidConfirmation("the answer is not an elicitation result")
		}
		if err := confirmations.consume(state); err != nil {
			return nil, none, err
		}
		// A refusal stands however late it comes: telling the model to ask again
		// would put the question back to someone who said no.
		switch {
		case result.Action == "cancel":
			return nil, none, fmt.Errorf("%s did not run: the person closed the question without answering. Do not call it again unless they ask", tool)
		case result.Action != "accept" || result.Content[confirmKey] != true:
			return nil, none, declined(tool)
		case expired:
			return nil, none, fmt.Errorf("%s did not run: the person accepted after the question expired. Call it again to ask again", tool)
		}
		return handler(ctx, request, input)
	}
}

// declined is the refusal of a call whose question was answered no. mm-mcp
// cannot tell a person's no from a client that answers the question itself
// without showing it, as the Claude desktop app's Code tab does, so the model
// is told both, and what the person can do in the second case (ADR-033).
func declined(tool string) error {
	return fmt.Errorf("%s did not run, and nothing was written: the confirmation was answered no. Either the person declined, "+
		"or their MCP client answered the question without showing it to them. Do not call it again unless they ask. If they "+
		"say they saw no question, tell them their client does not show mm-mcp's questions: save_draft can put a message in "+
		"their Mattermost message box for them to send, or they can set %s=false so that their client's own approval of "+
		"each tool call is the check instead", tool, config.EnvAskBeforeWrites)
}

// skippingKey marks a call whose server is configured not to ask: the
// person's MCP client approves each tool call itself, or nobody does
// (MM_MCP_ASK_BEFORE_WRITES=false, ADR-033). A call without it asks.
type skippingKey struct{}

// skipping reports whether this call's server is configured not to ask.
func skipping(ctx context.Context) bool {
	skip, _ := ctx.Value(skippingKey{}).(bool)
	return skip
}

// boundKey carries a call's fingerprint from askingBound to its handler.
type boundKey struct{}

// stillAsAsked refuses to act when what a handler is about to act on is no
// longer what the person was asked about: now is its fingerprint, made as bind
// makes it. bind read it when the answer arrived; the handler reads it again
// just before it acts, which closes the time between the two.
func stillAsAsked(ctx context.Context, tool, now string) error {
	if bound, ok := ctx.Value(boundKey{}).(string); ok && bound != now {
		return fmt.Errorf("%s did not run: what it acts on changed after the person was asked. Call it again to ask again", tool)
	}
	return nil
}

// confirmationForm is the question: the message, and one required checkbox.
func confirmationForm(c confirmation) *mcp.ElicitParams {
	return &mcp.ElicitParams{
		Message: c.Message,
		RequestedSchema: &jsonschema.Schema{
			Type:       "object",
			Properties: map[string]*jsonschema.Schema{confirmKey: {Type: "boolean", Title: c.Label}},
			Required:   []string{confirmKey},
		},
	}
}

// canConfirm reports whether this call's client declared form elicitation. The
// 2026-07-28 revision declares capabilities per request, so it is asked of the
// call itself. An empty elicitation object means form mode.
func canConfirm(request *mcp.CallToolRequest) bool {
	capabilities := request.ClientCapabilities()
	if capabilities == nil || capabilities.Elicitation == nil {
		return false
	}
	return capabilities.Elicitation.Form != nil || capabilities.Elicitation.URL == nil
}

// missingElicitation is MissingRequiredClientCapability, naming form
// elicitation, as the 2026-07-28 schema defines it. Every client gets it,
// whatever revision it speaks: a person who cannot be asked uses a client that
// can, or posts in Mattermost themselves.
func missingElicitation(tool string) error {
	return &jsonrpc.Error{
		Code: mcp.CodeMissingRequiredClientCapabilities,
		Message: fmt.Sprintf("%s did not run, and nothing was written: it asks the person to confirm it, and this MCP client cannot show the question "+
			"(it does not support form elicitation). Tell the person so. They can post from Mattermost themselves, use an MCP client that "+
			"supports elicitation, or have save_draft put the message in their Mattermost message box for them to send", tool),
		Data: json.RawMessage(`{"requiredCapabilities":{"elicitation":{"form":{}}}}`),
	}
}

// invalidConfirmation refuses a retry that does not match the question it
// claims to answer. An honest client never sends one.
func invalidConfirmation(reason string) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "the confirmation cannot be used: " + reason}
}

// inputDigest fingerprints a call's input, so an answer accepts only the call
// it was asked about.
func inputDigest(input any) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("cannot fingerprint the call for its confirmation: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type sealedConfirmation struct {
	Tool    string `json:"t"`
	Digest  string `json:"d"`
	Nonce   string `json:"n"`
	Expires int64  `json:"e"`
}

// confirmationSeal signs request states and remembers which were answered.
// The specification treats a request state as written by an attacker: a client
// could otherwise answer "accept" for a call nobody was shown. The key is drawn
// when the process starts and never leaves it.
type confirmationSeal struct {
	key      []byte
	mu       sync.Mutex
	answered map[string]int64 // nonce -> expiry, Unix seconds
	now      func() time.Time
}

var confirmations = newConfirmationSeal()

func newConfirmationSeal() *confirmationSeal {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("drawing the confirmation key: %v", err))
	}
	return &confirmationSeal{key: key, answered: map[string]int64{}, now: time.Now}
}

func (s *confirmationSeal) seal(tool, digest string) (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("drawing a confirmation nonce: %w", err)
	}
	payload, err := json.Marshal(sealedConfirmation{
		Tool: tool, Digest: digest, Nonce: hex.EncodeToString(nonce), Expires: s.now().Add(confirmationTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(s.mac(payload)), nil
}

// open verifies a request state against the call it came back with, and
// reports whether it expired. A state that does not verify, names another tool
// or input, or was answered already is refused; expiry is not, because a
// person who answered late still answered.
func (s *confirmationSeal) open(state, tool, digest string) (bool, error) {
	sealed, err := s.verify(state)
	if err != nil {
		return false, err
	}
	switch {
	case sealed.Tool != tool:
		return false, invalidConfirmation("it was issued for another tool")
	case sealed.Digest != digest:
		return false, invalidConfirmation("the call, or what it acts on, changed since the person was asked")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, used := s.answered[sealed.Nonce]; used {
		return false, invalidConfirmation("it was answered already")
	}
	return s.now().Unix() > sealed.Expires, nil
}

// consume marks a request state answered, so its answer cannot be sent again.
func (s *confirmationSeal) consume(state string) error {
	sealed, err := s.verify(state)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for nonce, expires := range s.answered {
		if s.now().Unix() > expires {
			delete(s.answered, nonce)
		}
	}
	if _, used := s.answered[sealed.Nonce]; used {
		return invalidConfirmation("it was answered already")
	}
	s.answered[sealed.Nonce] = sealed.Expires
	return nil
}

func (s *confirmationSeal) verify(state string) (sealedConfirmation, error) {
	encodedPayload, encodedMAC, found := strings.Cut(state, ".")
	if !found {
		return sealedConfirmation{}, invalidConfirmation("its request state is malformed")
	}
	// Strict, so a state has one spelling.
	encoding := base64.RawURLEncoding.Strict()
	payload, err := encoding.DecodeString(encodedPayload)
	if err != nil {
		return sealedConfirmation{}, invalidConfirmation("its request state is malformed")
	}
	mac, err := encoding.DecodeString(encodedMAC)
	if err != nil {
		return sealedConfirmation{}, invalidConfirmation("its request state is malformed")
	}
	if !hmac.Equal(mac, s.mac(payload)) {
		return sealedConfirmation{}, invalidConfirmation("its request state was not issued by this server")
	}
	var sealed sealedConfirmation
	if err := json.Unmarshal(payload, &sealed); err != nil {
		return sealedConfirmation{}, invalidConfirmation("its request state is malformed")
	}
	return sealed, nil
}

func (s *confirmationSeal) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, s.key)
	h.Write(payload)
	return h.Sum(nil)
}
