package server

import (
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vriesdemichael/mm-mcp/internal/doctor"
)

func answered(action string, ticked bool) *mcp.ElicitResult {
	return &mcp.ElicitResult{Action: action, Content: map[string]any{confirmKey: ticked}}
}

func TestTheTestQuestionTellsAPersonFromTheClient(t *testing.T) {
	t.Parallel()
	slow := askedAt(time.Now().Add(-10 * time.Second))
	fast := askedAt(time.Now())
	cases := []struct {
		name   string
		answer *mcp.ElicitResult
		state  string
		status doctor.Status
		says   string
	}{
		{"accepted and ticked", answered("accept", true), slow, doctor.OK, "reach them"},
		{"accepted at once and ticked", answered("accept", true), fast, doctor.OK, "reach them"},
		{"accepted unticked", answered("accept", false), slow, doctor.Warning, "without the box ticked"},
		{"declined at once", answered("decline", false), fast, doctor.Failed, "answered it itself"},
		{"cancelled at once", answered("cancel", false), fast, doctor.Failed, "answered it itself"},
		{"declined after a while", answered("decline", false), slow, doctor.Warning, "declined after"},
		{"closed after a while", answered("cancel", false), slow, doctor.Warning, "closed without an answer"},
	}
	for _, c := range cases {
		got := answerToTestQuestion(c.answer, c.state)
		if got.Status != c.status || !strings.Contains(got.Detail, c.says) {
			t.Errorf("%s: got %+v", c.name, got)
		}
	}
}

func TestATestQuestionsTimeCannotBeForged(t *testing.T) {
	t.Parallel()
	state := askedAt(time.Now().Add(-time.Minute))
	payload, _, _ := strings.Cut(state, ".")
	_, mac, _ := strings.Cut(askedAt(time.Now()), ".")
	for _, forged := range []string{payload + "." + mac, "", "not-a-state", payload} {
		if _, ok := whenAsked(forged); ok {
			t.Errorf("%q was read as a time", forged)
		}
	}
	if asked, ok := whenAsked(state); !ok || time.Since(asked) < 59*time.Second {
		t.Fatalf("the genuine state read as %v, %t", asked, ok)
	}
	// A decline with no time it can trust is not called the client's own.
	if got := answerToTestQuestion(answered("decline", false), "forged"); got.Status != doctor.Warning || !strings.Contains(got.Detail, "an unknown time") {
		t.Fatalf("got %+v", got)
	}
}
