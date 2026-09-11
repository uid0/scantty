package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

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
	// Flattened, because the run rides the same one-row bar the message does
	// and two of its parts are strings nobody bounded: the chip's label comes
	// off the wire from OMS, and the scanner state is whatever it was set to.
	return jdeStatusOneLine(strings.Join(parts, "  "))
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

// View draws the bar: a rule and exactly ONE content row, whatever the bar has
// been handed.
//
// One row is not a style choice. Root stacks the bar under a pane sized to
// fill the rest of the terminal, so every extra row the bar draws makes the
// frame one row taller than the terminal — and bubbletea keeps the BOTTOM
// rows of a frame that is too tall, so the rows lost are the TOP ones: the
// sidebar's title, the screen's title, the head of whatever the operator was
// reading, gone for as long as the flash lasts. A 502 gateway page flashed by
// the New PO submit made the frame 26 rows on a 24-row terminal.
// TestRoot_TheFrameIsNeverTallerThanTheTerminal holds the frame to the
// terminal at every size up to 120x40. TestRoot_TheFrameSweepStatusInputsReachTheRenderedBar
// proves each public input driven by that sweep changes the rendered row, and
// TestStatusBar_AStatusCommandDispatchedThroughRootIsOneMarkedRow proves a
// Status command dispatched through Root is drawn here, flattened and marked.
//
// Every width here is CELLS, measured with lipgloss.Width — never runes. A
// double-width rune is one rune and two cells, so a bound counted in runes calls
// a line of them a fit, and lipgloss WRAPS the line it was told would fit: the
// same extra row, reached with a different alphabet. The unread chip's 📬 is
// one, and that is how waiting mail alone used to make the idle frame a row
// taller than the terminal (contextRow).
func (s StatusBar) View() string {
	// avail is the usable width for a single content line inside
	// StyleStatusBar's Padding(0, 1); content wider than this wraps onto a
	// second row, which makes the frame taller than the terminal (see above),
	// so everything below is kept within avail.
	avail := s.width - 2
	if avail < 1 {
		avail = 1
	}

	// Active status/error message. Ian's rule: errors show on the BOTTOM
	// line, LEFT-justified, and COMPLETE — never clipped. So the message owns
	// the line; the connection/scanner context only rides along on the right
	// when the whole message still leaves room for it.
	//
	// "Complete" meets a one-row bar on every error that carries an OMS
	// response body, and that is not rare: omsapi.parseError puts the ENTIRE
	// raw body into APIError.Message whenever the envelope carries no code, and
	// the failed-write flashes across the package ("save failed: ", "delete
	// failed: ", the New PO submit's "create PO failed: ") append err.Error()
	// to their words — so a gateway page arrives here whole, newlines and all.
	// Every Status() in the package reaches the operator through this one
	// function: route.go's Status is the only place a StatusMsg is built and
	// Root's dispatch the only place one is flashed. The captain's decision for that case is ONE MARKED LINE: the message
	// is flattened onto one line and, where the row cannot hold it, cut with an
	// ellipsis. The mark is what keeps the rule's point — an operator can
	// always tell that what they are reading is not all there was — where a
	// clean cut would read as the whole message.
	//
	// It is the columnar status row's convention, not a second one:
	// jdeStatusOneLine flattens BEFORE anything is measured (the other way
	// round, a line is bounded and then re-expanded), and pickerClip bounds it
	// in one forward pass, so a 20 KB body costs what the row's width costs.
	if s.message != "" && time.Now().Before(s.msgExpiry) {
		text := pickerClip(jdeStatusOneLine(s.message), avail)
		msg := RenderStatus(text, s.msgLevel)
		context := s.contextLine(false, false)
		if gap := avail - lipgloss.Width(text) - lipgloss.Width(context); gap >= 2 {
			body := msg + strings.Repeat(" ", gap) + context
			return StyleStatusBar.Width(s.width).Render(body)
		}
		// The message alone; Width() left-justifies and pads it out. A cut
		// message is already inside avail, so this never wraps.
		return StyleStatusBar.Width(s.width).Render(msg)
	}

	// No active message: connection/scanner context on the left, key hints on
	// the right.
	body := s.contextRow(avail)
	if lipgloss.Width(body) > avail {
		// Even the last rung is too wide: clip at the true edge rather than
		// let Width() wrap the tail onto a second row. Root never draws this:
		// at every width it draws a frame at all, contextRow finds a rung that
		// fits (TestStatusBar_TheContextRowFitsEveryWidthRootDraws), so the
		// clip exists only for a bar drawn on its own — which is why it is
		// left unmarked rather than cut through the rung's styling.
		return StyleStatusBar.MaxWidth(s.width).Render(body)
	}
	return StyleStatusBar.Width(s.width).Render(body)
}

// contextRow is the bar's row when no message is up: the context run on the
// left, the key hints on the right, both chosen to fit `avail` cells.
//
// A body wider than the bar does not clip — lipgloss WRAPS it, which grows the
// frame by a row and scrolls the nav off the top — so this walks down a ladder
// until something fits. The hints go first (static help, also on the welcome
// screen), then the chip's label, then the scanner/unread run. The degraded
// chip itself is what everything else is sacrificed for.
//
// Measured in cells (see View). Counted in runes, the gap below padded the row
// out to the bar's full width BY RUNE COUNT, and the unread chip's 📬 is one
// rune and two cells: with any mail waiting, every row that chose the key hints
// came out one cell past the bar and lipgloss wrapped it — the idle frame a row
// taller than the terminal at every width from 76 up, for as long as the mail
// sat there. TestStatusBar_TheContextRowFitsEveryWidthRootDraws asks this
// function directly, because View's clip keeps the bar one row whatever it is
// handed and so hides the overrun from any count of rows.
func (s StatusBar) contextRow(avail int) string {
	// The standing three: the menu, search, and back. `q quit` is gone with the
	// rest of the letters — ctrl+c still quits from anywhere and is on the
	// welcome screen; a bar this narrow spends its room on the keys an operator
	// presses every minute, not the one they press once.
	hints := StyleMuted.Render("tab menu · ctrl+k search · esc back")
	ladder := s.contextLadder()
	context, right := ladder[0], hints
	fits := func(left, r string) bool { return lipgloss.Width(left)+lipgloss.Width(r)+1 <= avail }
	if !fits(context, right) {
		right = ""
		for _, candidate := range ladder {
			context = candidate
			if fits(context, right) {
				break
			}
		}
	}

	gap := avail - lipgloss.Width(context) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return context + strings.Repeat(" ", gap) + right
}
