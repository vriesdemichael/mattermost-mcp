package login

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// What a navigation decides, with a stand-in for the browser: where its page
// is after each send, and whether it answers.

// fakePage is a page that is at each address in turn, one per send, and
// counts what was asked of it.
type fakePage struct {
	after     []string // where the page is after the first, second, ... send
	sent      atomic.Int32
	refreshed atomic.Int32
	sendErr   func(attempt int) error
	hangWhere func(attempt int) bool // the page does not answer where it is
}

func (p *fakePage) navigation() navigation {
	return navigation{
		browser: "a browser", address: "https://chat.example.com/login", attempts: 3,
		leaveWithin: 50 * time.Millisecond, answerWithin: 50 * time.Millisecond, poll: 5 * time.Millisecond,
		closed: make(chan struct{}),
		send: func(context.Context) error {
			attempt := int(p.sent.Add(1))
			if p.sendErr != nil {
				return p.sendErr(attempt)
			}
			return nil
		},
		where: func(ctx context.Context) (string, error) {
			attempt := int(p.sent.Load())
			if p.hangWhere != nil && p.hangWhere(attempt) {
				<-ctx.Done()
				return "", ctx.Err()
			}
			if attempt == 0 || attempt > len(p.after) {
				return "about:blank", nil
			}
			return p.after[attempt-1], nil
		},
		refresh: func() { p.refreshed.Add(1) },
	}
}

func TestANavigationThatLandsIsSentOnce(t *testing.T) {
	t.Parallel()
	page := &fakePage{after: []string{"https://chat.example.com/login"}}
	if err := page.navigation().run(t.Context()); err != nil || page.sent.Load() != 1 || page.refreshed.Load() != 0 {
		t.Errorf("got %v, sent %d, refreshed %d", err, page.sent.Load(), page.refreshed.Load())
	}
}

func TestADroppedNavigationIsSentAgainToThePageFoundAfresh(t *testing.T) {
	t.Parallel()
	// The first send is dropped, as a browser that has just started drops it.
	page := &fakePage{after: []string{"about:blank", "https://chat.example.com/login"}}
	if err := page.navigation().run(t.Context()); err != nil || page.sent.Load() != 2 || page.refreshed.Load() != 1 {
		t.Errorf("got %v, sent %d, refreshed %d", err, page.sent.Load(), page.refreshed.Load())
	}
}

func TestAPageThatStaysBlankFailsTheNavigationSayingSo(t *testing.T) {
	t.Parallel()
	page := &fakePage{}
	err := page.navigation().run(t.Context())
	if !errors.Is(err, errStayedBlank) || !strings.Contains(err.Error(), "3 tries") || page.sent.Load() != 3 {
		t.Errorf("got %v after %d sends", err, page.sent.Load())
	}
}

func TestAPageThatGoesAwayIsFoundAfresh(t *testing.T) {
	t.Parallel()
	// The page the first send went to was closed: the browser refuses it.
	page := &fakePage{after: []string{"", "https://chat.example.com/login"}, sendErr: func(attempt int) error {
		if attempt == 1 {
			return errors.New("Page.navigate: Session with given id not found")
		}
		return nil
	}}
	if err := page.navigation().run(t.Context()); err != nil || page.refreshed.Load() != 1 {
		t.Errorf("got %v, refreshed %d", err, page.refreshed.Load())
	}
}

func TestAPageThatDoesNotAnswerIsFoundAfresh(t *testing.T) {
	t.Parallel()
	// A page the browser is closing takes a command and never answers it.
	page := &fakePage{after: []string{"", "https://chat.example.com/login"}, hangWhere: func(attempt int) bool { return attempt == 1 }}
	began := time.Now()
	if err := page.navigation().run(t.Context()); err != nil || page.refreshed.Load() != 1 {
		t.Errorf("got %v, refreshed %d", err, page.refreshed.Load())
	}
	if time.Since(began) > 5*time.Second {
		t.Errorf("an unanswered command held the navigation for %s", time.Since(began))
	}
	stuck := &fakePage{hangWhere: func(int) bool { return true }}
	if err := stuck.navigation().run(t.Context()); !errors.Is(err, errNoAnswer) {
		t.Errorf("a page that never answers: %v", err)
	}
}

func TestAClosedBrowserOrAnEndedWaitStopsTheNavigation(t *testing.T) {
	t.Parallel()
	closed := make(chan struct{})
	close(closed)
	page := &fakePage{}
	n := page.navigation()
	n.closed = closed
	if err := n.run(t.Context()); !errors.Is(err, ErrBrowserClosed) || page.sent.Load() != 1 {
		t.Errorf("a closed browser: %v after %d sends", err, page.sent.Load())
	}

	ended, cancel := context.WithCancel(t.Context())
	cancel()
	waited := &fakePage{}
	if err := waited.navigation().run(ended); !errors.Is(err, context.Canceled) || waited.refreshed.Load() != 0 {
		t.Errorf("an ended wait: %v, refreshed %d", err, waited.refreshed.Load())
	}
}

func TestTheFirstDocumentIsWaitedForOnce(t *testing.T) {
	t.Parallel()
	var settled atomic.Int32
	page := &fakePage{after: []string{"about:blank", "https://chat.example.com/login"}}
	n := page.navigation()
	n.settle = func(ctx context.Context) {
		settled.Add(1)
		if _, has := ctx.Deadline(); !has {
			t.Error("waiting for the first document has no end")
		}
	}
	if err := n.run(t.Context()); err != nil || settled.Load() != 1 {
		t.Errorf("got %v, settled %d times", err, settled.Load())
	}
}

func TestACommandThatRunsOutOfTimeIsSaidToBeUnanswered(t *testing.T) {
	t.Parallel()
	hang := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	if err := answered(t.Context(), 10*time.Millisecond, hang); !errors.Is(err, errNoAnswer) || !strings.Contains(err.Error(), "10ms") {
		t.Errorf("a command that hangs: %v", err)
	}
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if err := answered(ended, time.Second, hang); !errors.Is(err, context.Canceled) || errors.Is(err, errNoAnswer) {
		t.Errorf("the caller's own time ended: %v", err)
	}
	if err := answered(t.Context(), time.Second, func(context.Context) error { return nil }); err != nil {
		t.Errorf("a command that answers: %v", err)
	}
}

func TestAFailedNavigationSaysWhichBrowserDidNotOpenWhat(t *testing.T) {
	t.Parallel()
	if err := opening("Firefox", "https://chat.example.com/login", nil); err != nil {
		t.Errorf("a navigation that worked: %v", err)
	}
	err := opening("Firefox", "https://chat.example.com/login", errNoAnswer)
	if !errors.Is(err, errNoAnswer) || !strings.Contains(err.Error(), "Firefox did not open https://chat.example.com/login") {
		t.Errorf("got %v", err)
	}
}
