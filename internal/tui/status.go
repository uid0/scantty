package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

type StatusBar struct {
	width     int
	connOMS   bool
	connFK    bool
	scanner   string
	unread    int
	degraded  []omsapi.ServiceStatus
	message   string
	msgLevel  StatusLevel
	msgExpiry time.Time
}

func NewStatusBar() StatusBar {
	return StatusBar{scanner: "idle"}
}

func (s *StatusBar) SetWidth(w int)          { s.width = w }
func (s *StatusBar) SetOMSConn(ok bool)      { s.connOMS = ok }
func (s *StatusBar) SetForgeKeyConn(ok bool) { s.connFK = ok }
func (s *StatusBar) SetScanner(state string) { s.scanner = state }
func (s *StatusBar) SetUnread(n int)         { s.unread = n }

// SetDegradedServices records which external capabilities the backend reports
// as down, for the chip in View. An empty slice removes it — including the case
// where the status endpoint itself became unreachable, which is UNKNOWN and must
// not keep flying a warning nobody can act on.
func (s *StatusBar) SetDegradedServices(rows []omsapi.ServiceStatus) { s.degraded = rows }

func (s *StatusBar) Flash(text string, level StatusLevel, ttl time.Duration) {
	s.message = text
	s.msgLevel = level
	if ttl <= 0 {
		ttl = 4 * time.Second
	}
	s.msgExpiry = time.Now().Add(ttl)
}

func conn(name string, ok bool) string {
	if ok {
		return StyleStatusOK.Render(fmt.Sprintf("● %s", name))
	}
	return StyleStatusError.Render(fmt.Sprintf("○ %s", name))
}

// contextLine is the left-hand run: connection state, scanner, unread, and the
// degraded-services chip. `short` swaps the chip for its label-less form, and
// `minimal` keeps only the connection indicators beside it — the two rungs a
// narrow terminal climbs down rather than wrapping the bar onto a second row.
func (s StatusBar) contextLine(short, minimal bool) string {
	parts := []string{
		conn("OMS", s.connOMS),
		conn("FK", s.connFK),
	}
	if !minimal {
		parts = append(parts, StyleStatusInfo.Render(fmt.Sprintf("scanner: %s", s.scanner)))
		if s.unread > 0 {
			parts = append(parts, StyleStatusWarn.Render(fmt.Sprintf("📬 %d unread", s.unread)))
		}
	}
	// The degraded chip goes LAST in the run, next to the hints — a warning
	// that displaced the connection indicators would make the operator hunt for
	// the one piece of state that is on this bar every second of every day. It
	// is also the LAST thing dropped: a bar too narrow for "scanner: idle" is
	// still wide enough that email being down matters.
	chip := serviceStatusSummary(s.degraded)
	if short {
		chip = serviceStatusSummaryShort(s.degraded)
	}
	if chip != "" {
		parts = append(parts, StyleStatusWarn.Render(chip))
	}
	return strings.Join(parts, "  ")
}

// contextLadder is the left-hand run in decreasing order of detail. View walks
// it until something fits.
func (s StatusBar) contextLadder() []string {
	out := []string{s.contextLine(false, false)}
	for _, candidate := range []string{
		s.contextLine(true, false),
		s.contextLine(true, true),
	} {
		if candidate != out[len(out)-1] {
			out = append(out, candidate)
		}
	}
	return out
}

func (s StatusBar) View() string {
	context := s.contextLine(false, false)

	// avail is the usable width for a single content line inside
	// StyleStatusBar's Padding(0, 1); content wider than this wraps (and its
	// tail scrolls off the bottom of the frame), so everything below is kept
	// within avail.
	avail := s.width - 2
	if avail < 1 {
		avail = 1
	}

	// Active status/error message. Ian's rule: errors show on the BOTTOM
	// line, LEFT-justified, and COMPLETE — never clipped. So the message owns
	// the line; the connection/scanner context only rides along on the right
	// when the whole message still leaves room for it.
	if s.message != "" && time.Now().Before(s.msgExpiry) {
		msg := RenderStatus(s.message, s.msgLevel)
		msgLen := lenVis(msg)
		if gap := avail - msgLen - lenVis(context); gap >= 2 {
			body := msg + strings.Repeat(" ", gap) + context
			return StyleStatusBar.Width(s.width).Render(body)
		}
		if msgLen > avail {
			// Genuinely wider than the terminal (rare): clip at the true edge
			// so the bar stays a single line instead of wrapping the tail out
			// of view. Width() would word-wrap and hide the remainder.
			return StyleStatusBar.MaxWidth(s.width).Render(msg)
		}
		// Message fits on its own; Width() left-justifies and pads it out.
		return StyleStatusBar.Width(s.width).Render(msg)
	}

	// No active message: connection/scanner context on the left, key hints on
	// the right.
	//
	// A body wider than the bar does not clip — lipgloss WRAPS it, which grows
	// the frame by a row and scrolls the nav off the top — so this walks down a
	// ladder until something fits. The hints go first (static help, also on the
	// welcome screen), then the chip's label, then the scanner/unread run. The
	// degraded chip itself is what everything else is sacrificed for.
	hints := StyleMuted.Render("q quit · tab nav · / search")
	ladder := s.contextLadder()
	context, right := ladder[0], hints
	fits := func(left, r string) bool { return lenVis(left)+lenVis(r)+1 <= avail }
	if !fits(context, right) {
		right = ""
		for _, candidate := range ladder {
			context = candidate
			if fits(context, right) {
				break
			}
		}
	}

	gap := avail - lenVis(context) - lenVis(right)
	if gap < 1 {
		gap = 1
	}
	body := context + strings.Repeat(" ", gap) + right
	if lenVis(body) > avail {
		// Even the last rung is too wide (a very narrow terminal): clip at the
		// true edge rather than let Width() wrap the tail onto a second row —
		// the same treatment an over-long message gets above.
		return StyleStatusBar.MaxWidth(s.width).Render(body)
	}
	return StyleStatusBar.Width(s.width).Render(body)
}

func lenVis(s string) int {
	n := 0
	skip := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			skip = true
		case skip && r == 'm':
			skip = false
		case !skip:
			n++
		}
	}
	return n
}
