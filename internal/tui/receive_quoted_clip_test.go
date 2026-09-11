package tui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// What the operator TYPED, quoted back to them, is either whole or says it is
// not.
//
// Five sentences on the receiving form quote a typed value — a quantity that
// is not a number (twice: the line's own caveat and Enter's refusal), an expiry
// or a delivered date that is not a date, and a serial already captured on
// another unit. Every one cut the value and THEN closed the quote around it,
// and a closing quote is a claim that the value ends there: a 23-character
// serial came back as `"C02XK1ABJG5J-202" is already on unit 1`, which is a
// serial nobody typed, drawn whole, on the screen whose job is recording serials
// exactly. They go through poQuotedClip now, which keeps the ellipsis INSIDE the
// quote.
//
// Driven through the real screen and read off the clipped pane. Each case's
// value is wider than its room, so a cut IS made and the check is about how it
// is marked rather than passing because nothing was cut — the quantity and date
// boxes stop at 8 and 10 characters, so their values are full-width, which is
// what a Japanese IME types for digits.
func TestReceive_AQuotedValueTheOperatorTypedIsWholeOrMarked(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	down := tea.KeyMsg{Type: tea.KeyDown}
	const (
		wideQty  = "１２３４５６７８"   // eight runes, sixteen cells; the box takes eight
		wideDate = "２０２７－０１－３１" // ten runes, twenty cells; the box takes ten
		serial   = "C02XK1ABJG5J-2026-00017"
	)
	cases := []struct {
		name  string
		typed string
		room  int
		// says is the sentence the quote belongs to, required on the pane so a
		// case whose sentence was never drawn fails rather than passing on a
		// neighbour that quotes the same value.
		says  string
		drive func(t *testing.T) *ReceiveFormScreen
	}{
		{"a quantity that is not a number, under the line", wideQty, receiveQuotedCells, "is not a whole number, so this line cannot be sent",
			func(t *testing.T) *ReceiveFormScreen {
				r, s := receiveDrive(t, &receiveFake{}, receiveSweepLines(), 80, 30)
				r = receiveGoToLine(t, r, s, 1)
				_ = receiveTypeInto(t, r, wideQty)
				return s
			}},
		{"a quantity that is not a number, refused by enter", wideQty, receiveQuotedCells, "a quantity is a whole number, 0 or more",
			func(t *testing.T) *ReceiveFormScreen {
				r, s := receiveDrive(t, &receiveFake{}, receiveSweepLines(), 80, 30)
				r = receiveGoToLine(t, r, s, 1)
				r = receiveTypeInto(t, r, wideQty)
				_ = receiveKey(t, r, enter)
				return s
			}},
		{"an expiry that is not a date", wideDate, receiveQuotedCells, "needs an expiry written YYYY-MM-DD",
			func(t *testing.T) *ReceiveFormScreen {
				r, s := receiveDrive(t, &receiveFake{}, receiveSweepLines(), 80, 30)
				s.qty[2].SetValue("1")
				r = receiveKey(t, r, enter)
				if s.phase != phaseSerial {
					t.Fatalf("capture did not open: phase %v", s.phase)
				}
				r = receiveType(t, r, poRuneKey("SN-1"))
				r = receiveKey(t, r, down) // onto Lot
				r = receiveKey(t, r, down) // onto Expires
				r = receiveTypeInto(t, r, wideDate)
				_ = receiveKey(t, r, enter)
				return s
			}},
		{"a delivered date that is not a date", wideDate, receiveQuotedCells, "the delivered date is written YYYY-MM-DD",
			func(t *testing.T) *ReceiveFormScreen {
				r, s := receiveDrive(t, &receiveFake{}, receiveSweepLines(), 80, 30)
				r = receiveGoToLine(t, r, s, 1)
				r = receiveTypeInto(t, r, "1")
				r = receiveWalkTo(t, r, s, receiveRowDelivered, tea.KeyMsg{Type: tea.KeyUp})
				r = receiveTypeInto(t, r, wideDate)
				r = receiveKey(t, r, enter) // -> review
				_ = receiveKey(t, r, enter) // submit, which refuses the date
				return s
			}},
		{"a serial already on another unit", serial, receiveQuotedSerialCells, "is already on unit 1 for this item",
			func(t *testing.T) *ReceiveFormScreen {
				r, s := receiveDrive(t, &receiveFake{}, receiveSweepLines(), 80, 30)
				s.qty[2].SetValue("2")
				r = receiveKey(t, r, enter)
				if s.phase != phaseSerial || len(s.serialUnits) != 2 {
					t.Fatalf("capture did not open on two units: phase %v, %d", s.phase, len(s.serialUnits))
				}
				r = receiveTypeInto(t, r, serial)
				r = receiveKey(t, r, enter) // -> unit 2
				r = receiveTypeInto(t, r, serial)
				_ = receiveKey(t, r, enter) // -> review, warning
				return s
			}},
	}
	quoted := regexp.MustCompile(`"[^"]*"`)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if lipgloss.Width(strconv.Quote(c.typed)) <= c.room {
				t.Fatalf("%q fits its %d-cell room, so no cut is made and this checks nothing",
					c.typed, c.room)
			}
			s := c.drive(t)
			pane := receivePaneText(s, 80, 30)
			if !strings.Contains(pane, c.says) {
				t.Fatalf("%q is not on the pane, so the sentence under test was never "+
					"drawn:\n%s", c.says, pane)
			}
			found := 0
			for _, tok := range quoted.FindAllString(pane, -1) {
				inner := strings.TrimSuffix(strings.Trim(tok, `"`), "…")
				if inner == "" || !strings.HasPrefix(c.typed, inner) {
					continue
				}
				found++
				if inner != c.typed && !strings.HasSuffix(strings.Trim(tok, `"`), "…") {
					t.Errorf("the pane quotes %s — part of what was typed (%q), closed as "+
						"though it were all of it:\n%s", tok, c.typed, pane)
				}
			}
			if found == 0 {
				t.Fatalf("no quote of %q is on the pane, so the sentence under test was "+
					"never drawn:\n%s", c.typed, pane)
			}
		})
	}
}
