package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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

// TestStatusBar_EveryMessageIsOneMarkedRow is the captain's decision for a
// message the bar cannot hold — "flatten it to one marked line" — asserted at
// every width Root draws, over every shape rootFrameMessages names:
//
//   - the bar is its rule and exactly ONE content row, whatever the message
//     carries: newlines, CRLF, tabs, runes two cells wide;
//   - the row begins with the message, left-justified;
//   - a message the row cannot hold is cut with the ellipsis, and what is
//     drawn ahead of the ellipsis is the head of the flattened message — so the
//     operator can always tell there was more;
//   - a message the row CAN hold is drawn whole and gains no mark, because a
//     mark on a complete message is the same lie told the other way round.
//
// The frame-height guard (TestRoot_TheFrameIsNeverTallerThanTheTerminal) holds
// the first point through Root; this holds the other three, which a row count
// cannot see.
func TestStatusBar_EveryMessageIsOneMarkedRow(t *testing.T) {
	cut, whole := 0, 0
	for _, w := range jdeDrawableWidths() {
		avail := w - 2
		for _, m := range rootFrameMessages() {
			sb := NewStatusBar()
			sb.SetWidth(w)
			sb.Flash(m.text, StatusError, time.Minute)
			view := sb.View()
			if n := strings.Count(view, "\n"); n != 1 {
				t.Errorf("%s at width %d: the bar is %d rows, want the rule and one:\n%s",
					m.name, w, n+1, view)
				continue
			}
			// A row count cannot see a vertical tab or form feed: the terminal
			// moves its cursor down for either without a newline.
			if strings.ContainsAny(view, paneVerticalBreaks) {
				t.Errorf("%s at width %d: the bar still carries a vertical tab or form feed:\n%q",
					m.name, w, view)
				continue
			}
			flat := jdeStatusOneLine(m.text)
			row := strings.TrimPrefix(statusContentLine(view), " ")
			drawn := strings.TrimRight(row, " ")
			if lipgloss.Width(flat) <= avail {
				whole++
				// Prefix, not equality: the connection context rides on the
				// right of a message that leaves room for it.
				rest, ok := strings.CutPrefix(row, flat)
				if !ok || strings.HasPrefix(rest, "…") {
					t.Errorf("%s at width %d: a message that fits was not drawn whole and unmarked:\nwant %q\ngot  %q",
						m.name, w, flat, drawn)
				}
				continue
			}
			cut++
			head, ok := strings.CutSuffix(drawn, "…")
			if !ok {
				t.Errorf("%s at width %d: the message was cut and nothing says so:\n%q", m.name, w, drawn)
				continue
			}
			if !strings.HasPrefix(flat, head) || head == "" {
				t.Errorf("%s at width %d: what is drawn ahead of the mark is not the message's head:\n%q",
					m.name, w, drawn)
			}
			if got := lipgloss.Width(head) + 1; got > avail {
				t.Errorf("%s at width %d: the cut row is %d cells, the bar has %d", m.name, w, got, avail)
			}
		}
	}
	// Both halves have to be reached or one of them asserted nothing.
	if cut == 0 || whole == 0 {
		t.Fatalf("the sweep drew %d cut and %d whole message(s); both must be reached", cut, whole)
	}
}

// TestStatusBar_TheContextRowFitsEveryWidthRootDraws backs the claim in
// StatusBar.View that its unmarked clip is never drawn by Root: at every width
// Root draws, the row contextRow chooses fits the bar, in the quiet state and
// with the busiest context the frame sweep builds (plus a chip counting a
// thousand services, the widest count a short chip can carry here).
//
// It is asked of contextRow and not of the rendered bar ON PURPOSE. The clip
// behind it keeps the bar one row whatever contextRow hands it, so a row
// counted in runes — which the unread chip's 📬 puts a cell past the bar
// whenever the key hints are chosen — draws a frame of the right height with
// the hints cut and nothing saying so. The frame-height guard cannot see that;
// this can, and did when the fit was put back on runes.
func TestStatusBar_TheContextRowFitsEveryWidthRootDraws(t *testing.T) {
	states := []struct {
		name  string
		apply func(*StatusBar)
	}{
		{"quiet", func(sb *StatusBar) { rootFrameStatus{}.apply(sb) }},
		{"busy", func(sb *StatusBar) { rootFrameStatus{busy: true}.apply(sb) }},
		{"busy, a thousand services", func(sb *StatusBar) {
			rootFrameStatus{busy: true}.apply(sb)
			sb.SetDegradedServices(rootFrameDegraded(1000))
		}},
		// The unread chip without the busy state's other parts, so its emoji is
		// what decides the fit at some width rather than being masked by a
		// wider neighbour dropping the rung first.
		{"unread only", func(sb *StatusBar) {
			rootFrameStatus{}.apply(sb)
			sb.SetUnread(3)
		}},
	}
	for _, st := range states {
		for _, w := range jdeDrawableWidths() {
			sb := NewStatusBar()
			sb.SetWidth(w)
			st.apply(&sb)
			row := sb.contextRow(w - 2)
			if got := lipgloss.Width(row); got > w-2 {
				t.Errorf("%s at width %d: the context row is %d cells against %d — the bar clips "+
					"it with no mark:\n%q", st.name, w, got, w-2, stripStatusANSI(row))
			}
		}
	}
}

func TestStatusBar_AStatusCommandDispatchedThroughRootIsOneMarkedRow(t *testing.T) {
	r := newTestRoot(rootFrameScreen{})
	model, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = model.(Root)
	model, _ = r.Update(Status(rootFrameGateway, StatusError)())
	view := model.(Root).View()
	lines := strings.Split(view, "\n")
	if len(lines) != 24 {
		t.Fatalf("Root.View drew %d rows, want 24", len(lines))
	}
	last := strings.TrimRight(stripStatusANSI(lines[len(lines)-1]), " ")
	wantPrefix := " create PO failed: oms: http 502: <!DOCTYPE html> <html>"
	if !strings.HasPrefix(last, wantPrefix) {
		t.Errorf("last row = %q, want prefix %q", last, wantPrefix)
	}
	if !strings.HasSuffix(last, "…") {
		t.Errorf("last row = %q, want a visible truncation mark", last)
	}
}
