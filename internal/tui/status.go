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
// degraded-services chip. `short` swaps the chip for its label-less form, which
// is what a narrow terminal gets rather than a wrapped bar.
func (s StatusBar) contextLine(short bool) string {
	parts := []string{
		conn("OMS", s.connOMS),
		conn("FK", s.connFK),
		StyleStatusInfo.Render(fmt.Sprintf("scanner: %s", s.scanner)),
	}
	if s.unread > 0 {
		parts = append(parts, StyleStatusWarn.Render(fmt.Sprintf("📬 %d unread", s.unread)))
	}
	// The degraded chip goes LAST in the run, next to the hints — a warning
	// that displaces the connection indicators would make the operator hunt for
	// the one piece of state that is on this bar every second of every day.
	chip := serviceStatusSummary(s.degraded)
	if short {
		chip = serviceStatusSummaryShort(s.degraded)
	}
	if chip != "" {
		parts = append(parts, StyleStatusWarn.Render(chip))
	}
	return strings.Join(parts, "  ")
}

func (s StatusBar) View() string {
	context := s.contextLine(false)

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
	right := StyleMuted.Render("q quit · tab nav · / search")
	// The chip is the one part of the context that can be long, and a body
	// wider than the bar does not clip — lipgloss WRAPS it, which grows the
	// frame by a row and scrolls the nav off the top. Drop to the short chip
	// before that happens; the fact survives, only the label goes.
	if lenVis(context)+lenVis(right)+1 > avail {
		context = s.contextLine(true)
	}
	gap := avail - lenVis(context) - lenVis(right)
	if gap < 1 {
		gap = 1
	}
	body := context + strings.Repeat(" ", gap) + right
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
