// Package doctor checks what mm-mcp needs to work: its configuration, the
// login it stored, the way to the server, and the credential at Mattermost.
// `mm-mcp doctor` runs the checks from a terminal, the diagnose tool from
// inside a running server, and `mm-mcp serve` the ones that decide whether it
// starts, so the three cannot disagree.
//
// No check returns a credential, or any part of one (ADR-019).
package doctor

import (
	"fmt"
	"io"
	"runtime"
	"strings"

	"github.com/vriesdemichael/mm-mcp/internal/version"
)

// Status is how a check came out.
type Status string

// The statuses a check can have.
const (
	OK      Status = "ok"
	Warning Status = "warning"
	Failed  Status = "failed"
	// Skipped is a check a failed one before it leaves without an answer.
	Skipped Status = "skipped"
)

// Check is one thing checked, what was found, and what to do about it.
type Check struct {
	Name   string `json:"name" jsonschema:"what was checked"`
	Status Status `json:"status" jsonschema:"ok, warning, failed, or skipped when a failed check before it leaves it without an answer"`
	Detail string `json:"detail" jsonschema:"what was found"`
	Next   string `json:"next_step,omitempty" jsonschema:"what to do about it"`
	// stops says mm-mcp serve stops at start on this check.
	stops bool
}

// Stops reports whether mm-mcp serve stops at start on this check.
func (c Check) Stops() bool { return c.stops }

func ok(name, detail string, args ...any) Check {
	return Check{Name: name, Status: OK, Detail: fmt.Sprintf(detail, args...)}
}

func skipped(name, because string) Check {
	return Check{Name: name, Status: Skipped, Detail: because}
}

// Report is every check made, in the order they were made.
type Report struct {
	Checks []Check `json:"checks" jsonschema:"every check, in the order made; a failed one says what to do in next_step"`
}

// Add appends checks to the report.
func (r *Report) Add(checks ...Check) { r.Checks = append(r.Checks, checks...) }

// Failed reports whether any check failed.
func (r Report) Failed() bool { return r.count(Failed) > 0 }

func (r Report) count(status Status) int {
	n := 0
	for _, check := range r.Checks {
		if check.Status == status {
			n++
		}
	}
	return n
}

// Summary is one line on how the checks came out.
func (r Report) Summary() string {
	failed, warnings := r.count(Failed), r.count(Warning)
	switch {
	case failed > 0:
		return fmt.Sprintf("%s failed, %s: mm-mcp will not work as configured until the failed checks are fixed.", plural(failed, "check"), plural(warnings, "warning"))
	case warnings > 0:
		return fmt.Sprintf("Nothing failed; %s.", plural(warnings, "warning"))
	default:
		return "Every check passed."
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// Result is a report as `mm-mcp doctor --json` and the diagnose tool give it.
type Result struct {
	Summary string  `json:"summary" jsonschema:"how the checks came out, in one line"`
	Checks  []Check `json:"checks" jsonschema:"every check, in the order made; a failed one says what to do in next_step"`
}

// Result is the report with its summary.
func (r Report) Result() Result {
	checks := r.Checks
	if checks == nil {
		checks = []Check{}
	}
	return Result{Summary: r.Summary(), Checks: checks}
}

// Write prints the report for a person to read in a terminal.
func (r Report) Write(w io.Writer) {
	for _, check := range r.Checks {
		fmt.Fprintf(w, "%-8s %s: %s\n", check.Status, check.Name, check.Detail)
		if check.Next != "" {
			fmt.Fprintf(w, "%-8s -> %s\n", "", check.Next)
		}
	}
	fmt.Fprintf(w, "\n%s\n", r.Summary())
}

// Build is the check that names the mm-mcp running, for a bug report.
func Build() Check {
	return ok("mm-mcp", "%s on %s/%s", version.Version, runtime.GOOS, runtime.GOARCH)
}

// sentence joins parts into one line, skipping empty ones.
func sentence(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, " ")
}
