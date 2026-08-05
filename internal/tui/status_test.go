package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// stripStatusANSI drops ANSI escape sequences so tests can assert on the
// visible text the operator actually sees.
func stripStatusANSI(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// statusContentLine returns the status bar's content row (the line after
// StyleStatusBar's top border) with ANSI stripped.
func statusContentLine(view string) string {
	lines := strings.Split(view, "\n")
	if len(lines) < 2 {
		return stripStatusANSI(view)
	}
	return stripStatusANSI(lines[1])
}

// TestStatusBar_LongMessageCompleteAndLeftJustified is the load-bearing guard
// for issue-B (Ian 07-07): a status/error message must render on the bottom
// line, LEFT-justified, and COMPLETE — never clipped mid-string the way the
// old right-justified layout truncated "(no detail screen yet)" to
// "(no detail screen".
func TestStatusBar_LongMessageCompleteAndLeftJustified(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(120)
	sb.SetOMSConn(true)
	sb.SetForgeKeyConn(true)
	msg := "inventory 0f8e2a11-3c4d-4e5f-9a0b-1c2d3e4f5a6b (no detail screen yet)"
	sb.Flash(msg, StatusWarn, time.Minute)

	view := sb.View()
	visible := stripStatusANSI(view)

	// Complete: the whole message survives, not a truncated prefix.
	if !strings.Contains(visible, msg) {
		t.Fatalf("status view dropped part of the message.\nwant substring: %q\ngot: %q", msg, visible)
	}

	// Left-justified: the message begins the content line (only the 1-col
	// StyleStatusBar padding precedes it), ahead of any connection context.
	content := statusContentLine(view)
	if got := strings.TrimLeft(content, " "); !strings.HasPrefix(got, msg) {
		t.Fatalf("message is not left-justified; content = %q", content)
	}

	// Connection context still rides along, but to the RIGHT of the message.
	if mi, ci := strings.Index(content, msg), strings.Index(content, "scanner:"); ci >= 0 && ci < mi {
		t.Fatalf("connection context should follow the message, not precede it; content = %q", content)
	}

	// Single content line — the message tail must not wrap off the frame
	// (border line + exactly one content line == one newline).
	if n := strings.Count(view, "\n"); n != 1 {
		t.Fatalf("status bar should be top border + one content line (1 newline); got %d:\n%q", n, view)
	}
}

// TestStatusBar_MessageWinsOverContext: when a message is long enough that it
// and the connection context cannot both fit, the MESSAGE wins — it renders in
// full and the context yields, rather than the message losing its tail.
func TestStatusBar_MessageWinsOverContext(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80) // avail = 78; message (69) fits, but not alongside context.
	sb.SetOMSConn(true)
	sb.SetForgeKeyConn(true)
	msg := "inventory 0f8e2a11-3c4d-4e5f-9a0b-1c2d3e4f5a6b (no detail screen yet)"
	sb.Flash(msg, StatusError, time.Minute)

	view := sb.View()
	visible := stripStatusANSI(view)

	if !strings.Contains(visible, msg) {
		t.Fatalf("message clipped when it should win the line.\nwant: %q\ngot: %q", msg, visible)
	}
	if strings.Contains(visible, "scanner:") {
		t.Fatalf("context should yield to the full message; got %q", visible)
	}
	content := statusContentLine(view)
	if got := strings.TrimLeft(content, " "); !strings.HasPrefix(got, msg) {
		t.Fatalf("message is not left-justified; content = %q", content)
	}
	if n := strings.Count(view, "\n"); n != 1 {
		t.Fatalf("expected a single content line; got %d newlines:\n%q", n, view)
	}
}

// TestStatusBar_MessageLevelColorPreserved: the message keeps its level
// coloring (StatusError vs StatusInfo render distinctly). lipgloss strips color
// when stdout is not a TTY, so force a color profile for the assertion.
func TestStatusBar_MessageLevelColorPreserved(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prev)

	msg := "could not save item"

	errBar := NewStatusBar()
	errBar.SetWidth(80)
	errBar.Flash(msg, StatusError, time.Minute)
	errView := errBar.View()

	infoBar := NewStatusBar()
	infoBar.SetWidth(80)
	infoBar.Flash(msg, StatusInfo, time.Minute)
	infoView := infoBar.View()

	// colorError is "203"; the error message must carry that foreground.
	if !strings.Contains(errView, "203") {
		t.Fatalf("error-level message lost its color; view = %q", errView)
	}
	// Different levels must render differently (coloring is applied, not lost).
	if errView == infoView {
		t.Fatalf("StatusError and StatusInfo rendered identically; level coloring not preserved")
	}
}

// TestStatusBar_NoMessageUnchanged: with no active message the bar still shows
// connection/scanner context and the key hints, on a single content line.
func TestStatusBar_NoMessageUnchanged(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	sb.SetOMSConn(true)
	sb.SetForgeKeyConn(false)

	view := sb.View()
	visible := stripStatusANSI(view)

	for _, want := range []string{"OMS", "FK", "scanner:", "tab menu"} {
		if !strings.Contains(visible, want) {
			t.Fatalf("no-message status bar missing %q; got %q", want, visible)
		}
	}
	if n := strings.Count(view, "\n"); n != 1 {
		t.Fatalf("no-message status bar should be a single content line; got %d newlines:\n%q", n, view)
	}
}

// TestStatusBar_ExpiredMessageFallsBackToHints: once a flashed message expires
// the bar reverts to the default hint state (no stale message shown).
func TestStatusBar_ExpiredMessageFallsBackToHints(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(80)
	// Flash clamps a non-positive ttl to its 4s default, so drive the expiry
	// directly to simulate a message whose window has already passed.
	sb.message = "transient note"
	sb.msgLevel = StatusInfo
	sb.msgExpiry = time.Now().Add(-time.Minute)

	visible := stripStatusANSI(sb.View())
	if strings.Contains(visible, "transient note") {
		t.Fatalf("expired message should not render; got %q", visible)
	}
	if !strings.Contains(visible, "tab menu") {
		t.Fatalf("expected default hints after expiry; got %q", visible)
	}
}
