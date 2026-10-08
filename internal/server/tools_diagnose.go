package server

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/config"
	"github.com/vriesdemichael/mm-mcp/internal/credstore"
	"github.com/vriesdemichael/mm-mcp/internal/doctor"
)

// The diagnose tool makes the checks `mm-mcp doctor` makes, from inside the
// running server: with the environment the MCP client started it with, the
// credential it loaded, and the network as this process sees it. It adds what
// only a session shows: the client, and whether the person can be asked
// before a write.

type diagnoseInput struct {
	AskTestQuestion bool `json:"ask_test_question,omitempty" jsonschema:"show the person a test question through the MCP client, to see whether mm-mcp's questions before a write reach them; it changes nothing. Only when the person agrees, or a write was declined though they say they saw no question"`
}

// testQuestionKey names the test question among a retried call's input responses.
const testQuestionKey = "diagnose"

// shownWithin is the shortest time a person takes to read the test question
// and answer it. An answer that comes faster came from the client itself.
const shownWithin = 3 * time.Second

func diagnoseSpec() Spec {
	return shaping(configuredToolSpec(
		&mcp.Tool{
			Name: "diagnose",
			Description: "Check why mm-mcp does not work as expected: the configuration the MCP client started it with, the login it stored, " +
				"the way to the Mattermost server, the credential, the teams of the user it acts as, and this MCP client: whether it can show " +
				"the questions mm-mcp asks before each write. Each check says what was found, and a failed one what to do. Call it when a tool " +
				"fails in a way its error does not explain, or when the person asks why something does not work. It never returns the credential.",
			Annotations: readOnly("Diagnose mm-mcp"),
		},
		[]Use{
			{
				Operation: "GetUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "diagnose asks who the credential belongs to")},
			},
			{
				Operation: "GetTeamsForUser",
				Params:    map[string]Coverage{"user_id": Fixed("me", "diagnose checks that the user it acts as belongs to a team")},
			},
		},
		func(clientFor ClientFor, cfg config.Config) mcp.ToolHandlerFor[diagnoseInput, doctor.Result] {
			return func(ctx context.Context, request *mcp.CallToolRequest, input diagnoseInput) (*mcp.CallToolResult, doctor.Result, error) {
				var probe *doctor.Check
				if input.AskTestQuestion && canConfirm(request) {
					answer, answered := request.Params.InputResponses[testQuestionKey]
					if !answered {
						// The checks run once, when the answer comes back.
						return testQuestion(), doctor.Result{}, nil
					}
					probe = answerToTestQuestion(answer, request.Params.RequestState)
				}
				var report doctor.Report
				report.Add(doctor.Build())
				report.Add(doctor.Loaded(cfg, credstore.Where())...)
				report.Add(doctor.Login(cfg.URL, !cfg.TokenStored, doctor.Store{Load: cfg.Stored, Where: credstore.Where()})...)
				if proxy := doctor.Proxy(cfg.URL); proxy != nil {
					report.Add(*proxy)
				}
				client, err := clientFor(ctx, request)
				if err != nil {
					report.Add(doctor.Check{Name: "credential", Status: doctor.Failed, Detail: err.Error()})
				} else {
					connection := doctor.Connect(ctx, client, cfg)
					report.Add(connection.Checks...)
					report.Add(doctor.Teams(ctx, client, connection.User))
				}
				report.Add(clientChecks(request, cfg, input.AskTestQuestion)...)
				if probe != nil {
					report.Add(*probe)
				}
				return nil, report.Result(), nil
			}
		},
	), map[string]string{
		"ask_test_question": "asks the person a question through the MCP client and reports how it was answered; it sets no parameter",
	})
}

// clientChecks are what the session says of the MCP client: which it is, and
// whether it can ask the person before a write.
func clientChecks(request *mcp.CallToolRequest, cfg config.Config, asked bool) []doctor.Check {
	client := "the MCP client did not name itself"
	if info := request.ClientInfo(); info != nil {
		client = strings.TrimSpace(info.Name + " " + info.Version)
	}
	checks := []doctor.Check{{
		Name: "MCP client", Status: doctor.OK,
		Detail: fmt.Sprintf("%s, speaking MCP %s", client, request.ProtocolVersion()),
	}}
	const name = "confirmations"
	switch {
	case cfg.AllowWrites && cfg.ForceHumanInTheLoopInClaudeCode:
		checks = append(checks, doctor.Check{
			Name: name, Status: doctor.OK,
			Detail: fmt.Sprintf("%s is false, so mm-mcp asks nothing before a change others see, and %s marks each such tool for the MCP "+
				"client to ask the person on every call, whatever its permission rules allow; a client that does not read the mark asks as its rules say",
				config.EnvAskBeforeWrites, config.EnvForceHumanInTheLoopInClaudeCode),
		})
	case cfg.AllowWrites && cfg.SkipAsking:
		checks = append(checks, doctor.Check{
			Name: name, Status: doctor.OK,
			Detail: fmt.Sprintf("%s is false, so mm-mcp asks nothing before a change others see: the MCP client's own approval of each tool "+
				"call is the only check, and a client that approves tools by itself posts with nobody seeing it first", config.EnvAskBeforeWrites),
			Next: fmt.Sprintf("Make sure the client asks the person before these tools, or set %s=true to have it ask on every call.", config.EnvForceHumanInTheLoopInClaudeCode),
		})
	case !canConfirm(request) && cfg.AllowWrites:
		checks = append(checks, doctor.Check{
			Name: name, Status: doctor.Failed,
			Detail: "this client does not declare form elicitation, so it cannot show the question mm-mcp asks before each change others see: " +
				"every post, reply, edit, deletion, reaction, pin and channel change is refused before anything is written",
			Next: "Tell the person. They can use an MCP client that supports elicitation, post in Mattermost themselves, or have save_draft " +
				"put the message in their Mattermost message box for them to send." + letTheClientAsk,
		})
	case !canConfirm(request):
		checks = append(checks, doctor.Check{
			Name: name, Status: doctor.OK,
			Detail: "writes are not allowed, so nothing is asked; this client does not declare form elicitation, so with writes allowed it could not be asked either",
		})
	case cfg.AllowWrites && !asked:
		checks = append(checks, doctor.Check{
			Name: name, Status: doctor.OK,
			Detail: "this client declares form elicitation, so mm-mcp asks the person before each change others see. Some clients declare it and " +
				"answer the question themselves without showing it; diagnose with ask_test_question checks that the person sees it.",
		})
	case cfg.AllowWrites:
		checks = append(checks, doctor.Check{Name: name, Status: doctor.OK, Detail: "this client declares form elicitation, so mm-mcp asks the person before each change others see"})
	default:
		checks = append(checks, doctor.Check{Name: name, Status: doctor.OK, Detail: "writes are not allowed, so nothing is asked; this client declares form elicitation"})
	}
	if asked && !canConfirm(request) {
		checks = append(checks, doctor.Check{Name: "test question", Status: doctor.Skipped, Detail: "this client cannot show a question"})
	}
	return checks
}

// letTheClientAsk ends the advice to a person whose client does not show
// mm-mcp's questions (ADR-033).
const letTheClientAsk = " Or they can set " + config.EnvAskBeforeWrites + "=false, so that their client's own approval of each " +
	"tool call is the check instead."

// testQuestion is the call's answer that asks the person the test question,
// with the time it was asked, signed, as its request state.
func testQuestion() *mcp.CallToolResult {
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{testQuestionKey: &mcp.ElicitParams{
			Message: "mm-mcp is checking that its questions reach you. Nothing will be posted or changed. " +
				"Tick the box and accept, so it knows that you see what it asks before it writes in Mattermost.",
			RequestedSchema: &jsonschema.Schema{
				Type:       "object",
				Properties: map[string]*jsonschema.Schema{confirmKey: {Type: "boolean", Title: "I see this question"}},
				Required:   []string{confirmKey},
			},
		}},
		RequestState: askedAt(time.Now()),
	}
}

// askedAt is a request state that holds when the test question was asked,
// signed so a client cannot make an answer look slower than it was.
func askedAt(at time.Time) string {
	payload := []byte("diagnose:" + strconv.FormatInt(at.UnixMilli(), 10))
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(confirmations.mac(payload))
}

// whenAsked reads the time a request state from askedAt holds.
func whenAsked(state string) (time.Time, bool) {
	encodedPayload, encodedMAC, found := strings.Cut(state, ".")
	if !found {
		return time.Time{}, false
	}
	encoding := base64.RawURLEncoding.Strict()
	payload, err := encoding.DecodeString(encodedPayload)
	if err != nil {
		return time.Time{}, false
	}
	mac, err := encoding.DecodeString(encodedMAC)
	if err != nil || !hmac.Equal(mac, confirmations.mac(payload)) {
		return time.Time{}, false
	}
	millis, err := strconv.ParseInt(strings.TrimPrefix(string(payload), "diagnose:"), 10, 64)
	if err != nil || !strings.HasPrefix(string(payload), "diagnose:") {
		return time.Time{}, false
	}
	return time.UnixMilli(millis), true
}

// answerToTestQuestion says what the answer to the test question shows.
func answerToTestQuestion(answer any, state string) *doctor.Check {
	const name = "test question"
	result, ok := answer.(*mcp.ElicitResult)
	if !ok {
		return &doctor.Check{Name: name, Status: doctor.Failed, Detail: "the client's answer is not an answer to a question"}
	}
	asked, known := whenAsked(state)
	took := "an unknown time"
	fast := false
	if known {
		elapsed := time.Since(asked)
		took = elapsed.Round(100 * time.Millisecond).String()
		fast = elapsed < shownWithin
	}
	ticked := result.Content[confirmKey] == true
	switch {
	case result.Action == "accept" && ticked:
		return &doctor.Check{Name: name, Status: doctor.OK, Detail: fmt.Sprintf("the person ticked the box and accepted after %s: mm-mcp's questions before a write reach them", took)}
	case result.Action == "accept":
		return &doctor.Check{
			Name: name, Status: doctor.Warning,
			Detail: fmt.Sprintf("the question was accepted after %s without the box ticked, as a client that accepts questions on its own does; a write is refused on such an answer", took),
			Next:   "Ask the person whether they saw the question. If not, their client answers mm-mcp's questions itself, and save_draft is the way to post: the message waits in their Mattermost message box for them to send." + letTheClientAsk,
		}
	case fast:
		return &doctor.Check{
			Name: name, Status: doctor.Failed,
			Detail: fmt.Sprintf("the question was answered with %s after %s, too soon for a person to have read it: the client most likely answered it itself, without showing it, and answers every question before a write the same way", result.Action, took),
			Next:   "Tell the person their MCP client does not show mm-mcp's questions, so every write is refused. save_draft puts a message in their Mattermost message box for them to send themselves." + letTheClientAsk,
		}
	case result.Action == "cancel":
		return &doctor.Check{Name: name, Status: doctor.Warning, Detail: fmt.Sprintf("the question was closed without an answer after %s", took), Next: "Ask the person whether they saw it."}
	default:
		return &doctor.Check{
			Name: name, Status: doctor.Warning,
			Detail: fmt.Sprintf("the question was declined after %s", took),
			Next:   "Ask the person whether they saw it. If they did not, their client answered it itself, and would decline every write the same way." + letTheClientAsk,
		}
	}
}
