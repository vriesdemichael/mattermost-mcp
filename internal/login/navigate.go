package login

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// How long a page has to leave the blank document it starts on after it is
// sent elsewhere, how long a browser has to answer one command, and how often
// the address is sent before the window counts as failed.
const (
	leaveBlankWithin = 5 * time.Second
	answerWithin     = 10 * time.Second
	navigateAttempts = 3
)

// errStayedBlank is a page that did not leave its blank document.
var errStayedBlank = errors.New("the page stayed blank")

// errNoAnswer is a browser that did not answer a command in time, as one does
// that was sent to a page it is closing.
var errNoAnswer = errors.New("the browser did not answer")

// navigation sends a window's page to an address and makes sure it went. A
// browser that has just started can drop a navigation without an error, while
// it is still loading its first, blank document or replacing the page it
// opened with, which left a login window blank; and a command sent to a page
// it is closing is never answered. So each command has a time of its own, and
// while the page stays blank, or a command goes unanswered, the page the
// browser has now is found afresh and the address sent again. The browser
// itself is reached only through the functions, so the decision can be tested
// without one.
type navigation struct {
	browser, address string
	attempts         int
	leaveWithin      time.Duration // for the page to leave the blank document
	answerWithin     time.Duration // for the browser to answer one command
	poll             time.Duration // between looks at where the page is
	closed           <-chan struct{}

	settle  func(ctx context.Context)                 // waits for the first document, before the first send
	send    func(ctx context.Context) error           // sends the page to the address
	where   func(ctx context.Context) (string, error) // the address of the page's document
	refresh func()                                    // finds the page afresh for the next attempt
}

func (n navigation) run(ctx context.Context) error {
	var last error
	for attempt := range n.attempts {
		if attempt == 0 && n.settle != nil {
			settling, cancel := context.WithTimeout(ctx, n.leaveWithin)
			n.settle(settling)
			cancel()
		}
		if last = n.once(ctx); last == nil {
			return nil
		}
		if errors.Is(last, ErrBrowserClosed) || ctx.Err() != nil {
			return last
		}
		n.refresh()
	}
	return fmt.Errorf("%s did not open %s in %d tries: %w", n.browser, n.address, n.attempts, last)
}

func (n navigation) once(ctx context.Context) error {
	if err := answered(ctx, n.answerWithin, n.send); err != nil {
		return err
	}
	deadline := time.Now().Add(n.leaveWithin)
	for {
		var at string
		err := answered(ctx, n.answerWithin, func(ctx context.Context) error {
			var err error
			at, err = n.where(ctx)
			return err
		})
		switch {
		case err != nil:
			return err
		case at != "about:blank" && at != "":
			return nil
		case !time.Now().Before(deadline):
			return errStayedBlank
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-n.closed:
			return ErrBrowserClosed
		case <-time.After(n.poll):
		}
	}
}

// answered runs one command with a time of its own, and says a command that
// ran out of it went unanswered, while the caller's own time has not ended.
func answered(ctx context.Context, within time.Duration, command func(ctx context.Context) error) error {
	answering, cancel := context.WithTimeout(ctx, within)
	defer cancel()
	err := command(answering)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return fmt.Errorf("%w within %s", errNoAnswer, within)
	}
	return err
}

// opening is what a navigation that failed says: which browser did not open
// which address, and why.
func opening(browser, address string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s did not open %s: %w", browser, address, err)
}
