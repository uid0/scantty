package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// What the reorder queue hands over FITS THE PANE, in both axes, in every state
// it has. Measured on what the screen ASSEMBLES rather than on what Root draws,
// because after clampToBox no frame can be too big — the truncation has already
// happened, and a check that reads the clipped pane cannot fail.

// reorderPaneFixture builds a screen already holding `n` rows, sized to the
// given terminal, without going near the network. The rows carry a full-length
// name and a full-length manufacturer part number ON PURPOSE: a fixture of
// six-character values never reaches the bound under test, and an assertion
// that cannot reach its bound is vacuous however precisely it is worded.
func reorderPaneFixture(view reorderView, n, width, height int) *ReorderQueueScreen {
	s := NewReorderQueueScreen(Deps{})
	s.view = view
	s.loading = false
	for i := 1; i <= n; i++ {
		s.rows = append(s.rows, omsapi.ReorderRequest{
			ID:       i,
			Item:     fmt.Sprintf("6f1b9a0e-3c2d-4f5a-8b7c-%012d", i),
			Quantity: 1200,
			Status:   view.status(),
			Priority: "urgent",
			ItemDetails: &omsapi.ReorderItemDetails{
				Name:              "Hex head cap screw M8x40 zinc plated, grade 8.8",
				SKU:               "MFR-88421-REV-C-ZINC-PLATED-HEX",
				CurrentStock:      4,
				MinimumStock:      2500,
				PreferredSupplier: "Acme Industrial Supply Company",
			},
			EstimatedCost: omsapi.DecimalString("12345.67"),
			DaysPending:   14,
			RequestedBy:   "someone.with.a.long.name@example.com",
			RequestNotes:  "The lathe is down until these land; ask Dave which bin.",
			OrderNumber:   "PO-2026-000412",
		})
	}
	if view.status() == "" {
		s.rows[0].Status = omsapi.ReorderStatusReceived
	}
	next, _ := s.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return next.(*ReorderQueueScreen)
}

// reorderStates are the frames the screen has. Every one of them is swept,
// because the state nobody thought to check is the one that ships with no bar.
var reorderStates = map[string]func(*ReorderQueueScreen) *ReorderQueueScreen{
	"at rest": func(s *ReorderQueueScreen) *ReorderQueueScreen { return s },
	"loading": func(s *ReorderQueueScreen) *ReorderQueueScreen {
		s.loading = true
		return s
	},
	"load failed": func(s *ReorderQueueScreen) *ReorderQueueScreen {
		// An OMS failure arrives as the WHOLE raw body whenever the envelope
		// carries no code, so the fixture is a gateway page rather than a
		// sentence — the shape that took a frame over six rows elsewhere.
		s.loadErr = "oms: http 502: <!DOCTYPE html><html><head><title>502 Bad Gateway" +
			"</title></head><body><center><h1>502 Bad Gateway</h1></center>" +
			"<hr><center>nginx/1.27.3</center></body></html>"
		return s
	},
	"empty": func(s *ReorderQueueScreen) *ReorderQueueScreen {
		s.rows = nil
		return s
	},
	"receive confirm": func(s *ReorderQueueScreen) *ReorderQueueScreen {
		if len(s.rows) == 0 {
			return s
		}
		s.confirm, s.confirmID = reorderConfirmReceive, s.rows[0].IDString()
		return s
	},
	"a write in flight": func(s *ReorderQueueScreen) *ReorderQueueScreen {
		if len(s.rows) == 0 {
			return s
		}
		s.busyID, s.busyAction = s.rows[0].IDString(), "mark ordered"
		return s
	},
	"an answer standing": func(s *ReorderQueueScreen) *ReorderQueueScreen {
		s.answer, s.answerLevel = "nothing written: mark received is for ordered "+
			"requests, and this request is approved", StatusWarn
		return s
	},
}

// TestReorderQueue_NoLineRunsPastThePane is the width half. 80 columns is the
// standard this interface is built to and it leaves the pane 51 cells; a value
// that IS cut has to say so, because a line cut clean by clampToBox reads as a
// whole one — and takes its closing SGR reset with it, colouring everything
// drawn afterwards.
func TestReorderQueue_NoLineRunsPastThePane(t *testing.T) {
	for _, width := range jdeDrawableWidths() {
		room := screenBodyCells(width)
		for _, rows := range []int{1, 2, 9} {
			for name, mutate := range reorderStates {
				s := mutate(reorderPaneFixture(reorderViewApproved, rows, width, 24))
				for i, line := range strings.Split(s.View(), "\n") {
					if got := lipgloss.Width(line); got > room {
						t.Errorf("%s, %d row(s) at width %d: line %d is %d cells against %d: %q",
							name, rows, width, i, got, room, line)
					}
				}
			}
		}
	}
}

// TestReorderQueue_TheBarSurvivesEveryDrawableHeight is the height half, and it
// is about the ACTION BAR specifically: clampToBox drops from the BOTTOM, so a
// frame that assembles one row more than the pane has loses the bar — every key
// on the screen unnamed at once, on the screen whose whole subject is which key
// to press next.
//
// It is SCOPED to the heights the frame really fits (reorderFrameFits) and
// COUNTS BOTH SIDES, because a boundary that is never crossed is a way of
// asserting nothing: below it the chrome alone outruns the pane and the frame
// is documented to overrun, above it the bar is the one thing that may not give.
func TestReorderQueue_TheBarSurvivesEveryDrawableHeight(t *testing.T) {
	fits, refused := 0, 0
	for _, height := range jdePaneHeights() {
		rowsAvail := screenBodyRows(height)
		for _, rows := range []int{1, 2, 9} {
			for name, mutate := range reorderStates {
				s := mutate(reorderPaneFixture(reorderViewApproved, rows, 80, height))
				if !s.reorderFrameFits() {
					refused++
					continue
				}
				fits++
				assembled := strings.Split(s.View(), "\n")
				if len(assembled) > rowsAvail {
					t.Errorf("%s, %d row(s) at height %d: assembled %d rows into a pane of %d",
						name, rows, height, len(assembled), rowsAvail)
					continue
				}
				bar := s.barLines()
				last := strings.TrimSpace(stripANSI(bar[len(bar)-1]))
				drawn := clampToBox(s.View(), screenBodyCells(80), rowsAvail)
				if !strings.Contains(stripANSI(drawn), last) {
					t.Errorf("%s, %d row(s) at height %d: the bar's last line (%q) was clipped",
						name, rows, height, last)
				}
			}
		}
	}
	if fits == 0 || refused == 0 {
		t.Fatalf("the boundary was never crossed: %d fitting panes, %d too short — "+
			"one side unreached makes this sweep vacuous", fits, refused)
	}
}

// TestReorderQueue_TheWindowedListNamesWhatIsOffThePane. A pane that says there
// is more must name a key that fetches it, and a key it names must move what
// the operator SEES.
func TestReorderQueue_TheWindowedListNamesWhatIsOffThePane(t *testing.T) {
	s := reorderPaneFixture(reorderViewApproved, 40, 80, 24)
	if !s.reorderScrolls() {
		t.Fatalf("40 three-line rows fit a 24-row terminal? the fixture reaches no bound")
	}
	before := s.View()
	if !strings.Contains(before, "↓") {
		t.Fatalf("the pane hides rows and does not say so:\n%s", before)
	}
	bar := strings.Join(s.barSegments(), " · ")
	for _, seg := range []string{"j/k ↑↓ move", "pgup/pgdn page", "g/G home/end top/bottom"} {
		if !strings.Contains(bar, seg) {
			t.Errorf("the bar does not name %q on a list that scrolls: %s", seg, bar)
		}
	}
	next, _ := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	s = next.(*ReorderQueueScreen)
	after := s.View()
	if after == before {
		t.Error("G named on the bar and moved nothing the operator can see")
	}
	if !strings.Contains(after, "↑") {
		t.Errorf("the bottom of the list does not say what is above it:\n%s", after)
	}
}

// TestReorderQueue_TheBarIsNotNamedWhereItCannotMove is the other direction of
// the same rule, and it is the one a single-row fixture can never reach: with
// one row there is nothing to move to, so naming the movement keys would be the
// bar making a claim the arms refuse.
func TestReorderQueue_TheBarIsNotNamedWhereItCannotMove(t *testing.T) {
	s := reorderPaneFixture(reorderViewApproved, 1, 80, 24)
	bar := strings.Join(s.barSegments(), " · ")
	for _, seg := range []string{"j/k ↑↓ move", "pgup/pgdn page", "g/G home/end top/bottom"} {
		if strings.Contains(bar, seg) {
			t.Errorf("a one-row list names %q: %s", seg, bar)
		}
	}
	s = reorderPaneFixture(reorderViewApproved, 3, 80, 30)
	if s.reorderScrolls() {
		t.Skip("three rows outran a 30-row terminal; the paging claim is honest here")
	}
	if bar := strings.Join(s.barSegments(), " · "); strings.Contains(bar, "pgup/pgdn page") {
		t.Errorf("a list that fits names the paging keys: %s", bar)
	}
}

// TestReorderQueue_TheRawItemUUIDIsNeverCut. When a row carries no item_details
// expansion — the shape the anonymous create endpoint echoes — the raw item
// UUID is the only thing that can identify it, and a cut UUID matches nothing.
//
// The LAYOUT is what gives: the UUID takes a line of its own at the data
// indent, so the 51 cells an 80-column terminal leaves give it 47 for the 41 it
// needs, whatever the row number is and whatever facts would otherwise have
// shared the line. That is the claim asserted here — not merely that it fits at
// one convenient index, which a fixture of row 1 would prove by accident.
func TestReorderQueue_TheRawItemUUIDIsNeverCut(t *testing.T) {
	const uuid = "6f1b9a0e-3c2d-4f5a-8b7c-000000000042"
	s := NewReorderQueueScreen(Deps{})
	s.loading = false
	for i := 1; i <= 12; i++ {
		s.rows = append(s.rows, omsapi.ReorderRequest{
			ID: i, Item: uuid, Quantity: 1200,
			Status: omsapi.ReorderStatusApproved, Priority: "urgent",
			EstimatedCost: omsapi.DecimalString("12345.67"), DaysPending: 14,
		})
	}
	next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	s = next.(*ReorderQueueScreen)

	pane := stripANSI(s.View())
	if !strings.Contains(pane, uuid) {
		t.Fatalf("the item UUID is not on the pane whole:\n%s", pane)
	}
	// It is on a line of its OWN — nothing else competing for the cells.
	var found bool
	for _, line := range strings.Split(pane, "\n") {
		if !strings.Contains(line, uuid) {
			continue
		}
		found = true
		if strings.TrimSpace(line) != "item "+uuid {
			t.Errorf("the UUID shares its line with %q — the next value added "+
				"beside it is the one that clips it", strings.TrimSpace(line))
		}
	}
	if !found {
		t.Fatal("no line carried the UUID")
	}
	// The facts it displaced are still shown, on the lines below it.
	if flat := strings.Join(strings.Fields(pane), " "); !strings.Contains(flat, "× 1200") {
		t.Errorf("the quantity went missing when the UUID took the line:\n%s", pane)
	}
}
