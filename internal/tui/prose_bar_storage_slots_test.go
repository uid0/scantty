package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_storage_slots_test.go — the fixtures for StorageSlotsScreen, taken
// out of proseBarUnconverted on its own because its work was its own shape.
//
// THE SHAPE. A windowed cursor list whose window was budgeted by
//
//	const chrome = 6 // header + blank + help + overlay room
//
// — one constant counting the bar AND the room slotCardPrompt's print overlay
// needs, so neither could be derived while the other was folded into it. Both
// were wrong: the bar is a folded record several rows tall at 80 columns, and the
// print prompt takes five. The bar's cost is its ceiling now (ceilingBar), an
// overlay's is what it draws (StorageSlotsScreen.foot), and the frame spends
// whichever foot is up before the window gets a line (proseFlatListFrameFoot).
//
// slotCardPrompt ITSELF STAYS A RECORDED EXCEPTION. It is the overlay, not this
// list: it names its own keys, the list's proseBar answers nil while it is up,
// and the footer sweeps never read it. What this list owes it is ROOM, and
// TestProseBarStorageSlots_EveryOverlaySurvivesTheRowsAtEveryDrawablePane is
// where that is held.
//
// MEASURED AT HEAD BEFORE THE CONVERSION, at 80x24 (an 18-row pane): the
// oversized-row fixture handed over 50 rows with `r refresh` off the pane; the
// print prompt opened on a 45-slot list handed over 21, with `esc cancel` off
// it; and every row's tag was past the 51st cell, cut with no mark.

// proseBarStorageSlotFixtures is the list in every state its bar changes shape
// in.
func proseBarStorageSlotFixtures() []proseBarFixture {
	const scopeBox = "the rack-scope box has the focus, so every printable key is a character in it"
	return []proseBarFixture{
		// Nothing selected and no scope: `c` is off, `p` is on because the cursor
		// has a rack to print.
		{
			name: "storage slots", recv: "StorageSlotsScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarStorageSlots(proseBarFlatLongRows), "j") },
		},
		// A selection: `c` comes on.
		{
			name: "storage slots/selected", recv: "StorageSlotsScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarStorageSlots(proseBarFlatLongRows), "j"), " ")
			},
		},
		{
			name: "storage slots/one row", recv: "StorageSlotsScreen",
			build:    func() proseBarScreen { return proseBarStorageSlots(1) },
			immobile: "one slot, so there is nowhere for the cursor to go",
		},
		{
			name: "storage slots/empty", recv: "StorageSlotsScreen",
			build: func() proseBarScreen { return proseBarStorageSlots(0) },
			immobile: "no slots — the point of this fixture: the movement segment, every row " +
				"action and `p` (nothing to print) must be absent, and `n`, `b`, `f`, `/`, `r` " +
				"and `esc` named",
		},
		// Scoped to a rack that holds nothing: `p` is back, printing the rack.
		{
			name: "storage slots/empty, scoped to a rack", recv: "StorageSlotsScreen",
			build: func() proseBarScreen {
				s := proseBarPress(proseBarStorageSlots(0), "/", "9", "enter")
				next, _ := s.Update(storageSlotsLoadedMsg{})
				return next.(proseBarScreen)
			},
			immobile: "no slots in the rack, so nothing for the cursor to move across",
		},
		{
			name: "storage slots/scope prompt", recv: "StorageSlotsScreen", typing: scopeBox,
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarStorageSlots(proseBarFlatLongRows), "j"), "/")
			},
			immobile: scopeBox,
		},
		// A slot code typed where a rack belongs: the refusal is a row of the foot.
		{
			name: "storage slots/scope prompt, refused", recv: "StorageSlotsScreen", typing: scopeBox,
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarStorageSlots(proseBarFlatLongRows), "j"),
					"/", "1", "A", "1", "enter")
			},
			immobile: scopeBox,
		},
		{
			name: "storage slots/oversized row", recv: "StorageSlotsScreen",
			build:    func() proseBarScreen { return proseBarStorageSlotsOversized() },
			immobile: "one slot, so there is nowhere for the cursor to go",
		},
	}
}

// TestProseBarStorageSlots_AnOversizedRowFitsAndMarksItsCut: a slot whose stored
// group name is taller than the pane is clipped to what the bar leaves, the cut
// is MARKED, and the bar is the last thing on the pane.
//
// WATCHED FAILING at HEAD before the conversion: 50 rows against 18, and no
// `r refresh` on the clipped pane.
func TestProseBarStorageSlots_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	s := proseBarSize(proseBarStorageSlotsOversized(), width, height)
	if !proseBarFrameFits(s, height) {
		t.Fatalf("an oversized row pushed the frame past %dx%d, taking the bar with it:\n%s",
			width, height, stripANSI(s.View()))
	}
	got := stripANSI(s.View())
	if !strings.Contains(got, " more lines") {
		t.Fatalf("an oversized row was clipped with no mark saying so:\n%s", got)
	}
	if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
		t.Fatalf("the bar is not the last thing on the pane:\n%s", got)
	}
}

// TestProseBarStorageSlots_EveryOverlaySurvivesTheRowsAtEveryDrawablePane: the
// print prompt, the delete confirm and the scope prompt's refusal each reach the
// operator whole — the words that answer the prompt included — and the frame
// carrying them fits, at every pane Root draws that is tall enough for the
// overlay and the least window the list will draw.
//
// THE BOUNDARY IS DERIVED from the same state built over one ordinary row, plus
// both scroll markers and the rows the window's floor adds past one — the
// reason TestProseBarFootPrompts_TheFootSurvivesTheRowsAtEveryDrawablePane gives
// for not scoping by "wherever the frame fits", which passes over exactly the
// panes an unbudgeted overlay pushes the frame past. Both sides must be reached.
//
// WATCHED FAILING with the overlay's rows left out of the budget (foot answering
// the bar's ceiling for every foot): the three print-prompt states over a long
// list, the delete confirm and the scope refusal, at every width.
func TestProseBarStorageSlots_EveryOverlaySurvivesTheRowsAtEveryDrawablePane(t *testing.T) {
	long := func(keys ...string) func() proseBarScreen {
		return func() proseBarScreen {
			return proseBarPress(proseBarStorageSlots(proseBarFlatLongRows), append([]string{"j", "j", "j"}, keys...)...)
		}
	}
	least := func(keys ...string) func() proseBarScreen {
		return func() proseBarScreen { return proseBarPress(proseBarStorageSlots(1), keys...) }
	}
	printWords := []string{"Print slot cards", "enter render · esc cancel", "Avery 5388"}
	cases := []struct {
		name  string
		build func() proseBarScreen
		least func() proseBarScreen
		want  []string
	}{
		{name: "print prompt, a rack", build: long("p"), least: least("p"),
			want: append([]string{"Include retired slots"}, printWords...)},
		{name: "print prompt, a selection", build: long(" ", "j", " ", "p"), least: least(" ", "p"),
			want: printWords},
		{name: "print prompt, rendering", build: long("p", "enter"), least: least("p", "enter"),
			want: []string{"Print slot cards", "Rendering sheet…"}},
		{
			name:  "print prompt, oversized row",
			build: func() proseBarScreen { return proseBarPress(proseBarStorageSlotsOversized(), "p") },
			least: least("p"), want: printWords,
		},
		{name: "delete confirm, occupied", build: long("x"), least: least("x"),
			want: []string{"Delete slot", "The backend will refuse", "Retiring it instead", "y delete · n/esc cancel"}},
		{name: "deleting", build: long("x", "y"), least: least("x", "y"),
			want: []string{"Deleting…"}},
		{name: "scope prompt, refused", build: long("/", "1", "A", "1", "enter"),
			least: least("/", "1", "A", "1", "enter"),
			want:  []string{"Scope to rack:", "✗ scope is a rack", "esc cancel"}},
	}
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			held, short := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					least := proseBarSize(c.least(), w, h)
					s := proseBarSize(c.build(), w, h)
					// The header folds, and "45 slots · 2 selected" can take a row
					// "1 slots · 1 selected" does not, so the head is counted as each
					// frame draws it rather than assumed equal.
					cells := proseBarCells(w)
					headRows := strings.Count(s.(*StorageSlotsScreen).head(cells), "\n") -
						strings.Count(least.(*StorageSlotsScreen).head(cells), "\n")
					need := lipgloss.Height(least.View()) + headRows + 2 + (proseListWindowFloor - 1)
					if screenBodyHeight(h) < need || screenBodyRows(h) < need {
						short++
						continue
					}
					held++
					if !proseBarFrameFits(s, h) {
						t.Errorf("at %dx%d the frame needs %d rows and the pane has %d, so the overlay "+
							"is what clampToBox takes:\n%s", w, h, lipgloss.Height(s.View()), screenBodyRows(h),
							stripANSI(s.View()))
						continue
					}
					pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
					for _, want := range c.want {
						if !strings.Contains(pane, want) {
							t.Errorf("at %dx%d the pane does not carry %q:\n%s", w, h, want, pane)
						}
					}
				}
			}
			if held == 0 || short == 0 {
				t.Errorf("%s reached %d panes past the boundary and %d short of it, so one side "+
					"was never tested", c.name, held, short)
			}
		})
	}
}

// TestProseBarStorageSlots_EveryRowFitsThePaneAndKeepsItsTag: a row is never
// wider than the pane, and the tag — the fact a warden scans the rack for —
// survives on every row at every drawable width, where the fixed 42-cell
// occupancy column used to push it past the 51st cell on every row.
func TestProseBarStorageSlots_EveryRowFitsThePaneAndKeepsItsTag(t *testing.T) {
	for _, w := range jdeDrawableWidths() {
		s := proseBarSize(proseBarStorageSlots(proseBarFlatLongRows), w, 40)
		cells := proseBarCells(w)
		for i, slot := range s.(*StorageSlotsScreen).rows {
			row := s.(*StorageSlotsScreen).renderRow(i, cells)
			if got := lipgloss.Width(row); got > cells {
				t.Fatalf("at width %d row %d is %d cells on a %d-cell pane:\n%s", w, i, got, cells, stripANSI(row))
			}
			if want := fmt.Sprintf("tag %d", *slot.AprilTagID); !strings.Contains(stripANSI(row), want) {
				t.Fatalf("at width %d row %d lost %q:\n%s", w, i, want, stripANSI(row))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// proseBarStorageSlots is a rack of `n` slots at the lengths OMS really serves:
// every third one — starting with the one the long fixture's cursor is walked to
// — holds a member's project under a full name, every fifth is a committee
// holding, every fourth is retired, and all of them carry an owning group.
func proseBarStorageSlots(n int) *StorageSlotsScreen {
	rows := make([]omsapi.StorageSlot, 0, n)
	for i := 0; i < n; i++ {
		tag := 1000 + i
		slot := omsapi.StorageSlot{
			ID: i + 1, Code: fmt.Sprintf("%dB%d", 12+i%3, i+1), Rack: 12 + i%3, Level: "B", Position: i + 1,
			IsActive: i%4 != 2, AprilTagID: &tag, RequiresPalletJack: i%2 == 1,
			OwningGroupName: "Metal shop welding SIG",
		}
		switch {
		case i%3 == 0:
			slot.IsOccupied = true
			slot.CurrentStint = &omsapi.StorageSlotOccupant{
				StintID: fmt.Sprintf("PS-AB23CD%02d", i), Username: "welder07",
				DisplayName: "Alex Hernandez-Whitfield", ProjectTitle: "Go-kart chassis jig, second revision",
			}
		case i%5 == 0:
			slot.IsOccupied = true
			slot.CurrentAssignment = &omsapi.StorageSlotAssignmentSummary{
				TypeLetter: "C", OccupantDisplay: "Facilities and safety committee",
			}
		}
		rows = append(rows, slot)
	}
	s := NewStorageSlotsScreen(Deps{})
	next, _ := s.Update(storageSlotsLoadedMsg{rows: rows})
	return next.(*StorageSlotsScreen)
}

// proseBarStorageSlotsOversized is one slot whose group name is taller than any
// pane — a stored newline is kept, not flattened.
func proseBarStorageSlotsOversized() *StorageSlotsScreen {
	s := proseBarStorageSlots(1)
	s.rows[0].OwningGroupName = proseBarTallName()
	return s
}

// Compile-time: the list states the record contract.
var _ proseBarScreen = (*StorageSlotsScreen)(nil)
