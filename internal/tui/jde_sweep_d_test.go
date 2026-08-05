// The STORAGE & FACILITIES family of forms on the columnar "JD Edwards" layer
// (sc-6qsk, sweep D of the sc-h412 redesign): the slot sheet, the rack
// generator, the staff assignment, the project-storage intake, the location
// sheet and the maker-box sheet.
//
// These hold all six to the same contract sweeps A, B and C hold their families
// to, because the persistent action bar is now the only place an operator can
// learn what a key does:
//
//	the sheet is columnar   — every field row hangs off ONE leader column
//	the bar is persistent   — same two rows of the pane on every frame
//	the bar is honest       — a key on it works here, and a key that works
//	                          here is on it
//	no letter accelerators  — a stray letter on a row with no input does
//	                          NOTHING rather than firing something invisible
//	rows fit the pane       — clampToBox TRUNCATES an over-wide row, so a hint
//	                          that does not fit is a hint silently lost
//	                          (sc-ye0i's test, carried forward as instructed)
//
// Plus the two this batch owns:
//
//	one column for the racking — the four STORAGE screens are reached from one
//	                             another, so they share a column; the location
//	                             and maker-box sheets are not in that family
//	                             and must not be widened to join it
//	the level list folds       — the generator's a/e/x/j/k became the trailing
//	                             "(add a level)" row, Ctrl-E, and a remove row
//	                             inside the level's OWN editor (sweep B's fold)
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// jdeSweepDWidth is the terminal the family is measured at — the width sc-akwv
// eyeballed every screen at, and what screenBodyWidth turns into the pane.
const jdeSweepDWidth = 110

// jdeSweepDCase is one converted form, loaded and ready to drive.
type jdeSweepDCase struct {
	name      string
	screen    Screen
	setCursor func(row int)
	rowCount  int
	kinds     map[jdeRowKind]int // kind -> a row of that kind (absent = none)
	// saveVerb is what the bar calls Enter here: these forms do not all "save"
	// (a rack is GENERATED, a slot is ASSIGNED), and the bar has to say what the
	// key actually does.
	saveVerb string
	// pickerVerb is what the bar calls Ctrl-E on the picker row.
	pickerVerb string
	// storageFamily marks the four screens that share ONE label column.
	storageFamily bool
}

func jdeSweepDCases(t *testing.T) []jdeSweepDCase {
	t.Helper()
	size := tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight}
	sigs := []omsapi.SIG{{ID: 3, Name: "Woodshop"}, {ID: 4, Name: "Metal shop"}}

	slot := NewStorageSlotFormScreen(Deps{}, "")
	slot.loading = false
	slot.sigs = sigs
	slot.sigsReady = true
	slot.Update(size)

	gen := NewStorageSlotGenerateScreen(Deps{}, 1)
	gen.sigs = sigs
	gen.sigsReady = true
	gen.Update(size)

	assign := NewStorageAssignFormScreen(Deps{}, "1A1", nil)
	assign.sigs = sigs
	assign.sigsReady = true
	assign.Update(size)

	intake := NewProjectStorageFormScreen(Deps{})
	intake.slotsReady = true
	intake.slots = []omsapi.StorageSlot{
		{ID: 1, Code: "1A1", IsActive: true},
		{ID: 2, Code: "1A2", IsActive: true, RequiresPalletJack: true},
	}
	intake.Update(size)

	loc := NewLocationFormScreen(Deps{}, "")
	loc.loading = false
	loc.locations = []omsapi.Location{{ID: 7, Name: "Bay 4"}, {ID: 8, Name: "Mezzanine"}}
	loc.Update(size)

	box := NewMakerBoxFormScreen(Deps{}, 0)
	box.Update(size)

	return []jdeSweepDCase{
		{
			name:      "storage slot",
			screen:    slot,
			setCursor: func(row int) { slot.cursor = row; slot.syncFocus() },
			rowCount:  len(slot.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, slot.fields, ssfRack),
				jdeRowChoice: elecRowOf(t, slot.fields, ssfPalletJack),
				jdeRowPicker: elecRowOf(t, slot.fields, ssfOwningGroup),
			},
			saveVerb:      "Save",
			pickerVerb:    "Pick",
			storageFamily: true,
		},
		{
			name:      "rack generate",
			screen:    gen,
			setCursor: func(row int) { gen.cursor = row; gen.syncFocus() },
			rowCount:  len(gen.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, gen.fields, sgRack),
				jdeRowPicker: elecRowOf(t, gen.fields, sgOwningGroup),
			},
			saveVerb:      "Generate",
			pickerVerb:    "Pick",
			storageFamily: true,
		},
		{
			name:      "storage assign",
			screen:    assign,
			setCursor: func(row int) { assign.cursor = row; assign.syncFocus() },
			rowCount:  len(assign.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, assign.fields, safLabel),
				jdeRowChoice: elecRowOf(t, assign.fields, safType),
				jdeRowPicker: elecRowOf(t, assign.fields, safGroup),
			},
			saveVerb:      "Assign",
			pickerVerb:    "Pick",
			storageFamily: true,
		},
		{
			name:      "project storage intake",
			screen:    intake,
			setCursor: func(row int) { intake.cursor = row; intake.syncFocus() },
			rowCount:  len(intake.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, intake.fields, psfUsername),
				jdeRowPicker: elecRowOf(t, intake.fields, psfSlot),
			},
			saveVerb:      "Save",
			pickerVerb:    "Pick",
			storageFamily: true,
		},
		{
			name:      "location",
			screen:    loc,
			setCursor: func(row int) { loc.cursor = row; loc.syncFocus() },
			rowCount:  len(loc.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, loc.fields, lfName),
				jdeRowChoice: elecRowOf(t, loc.fields, lfIsActive),
				jdeRowPicker: elecRowOf(t, loc.fields, lfParent),
			},
			saveVerb:   "Save",
			pickerVerb: "Pick",
		},
		{
			name:      "maker box",
			screen:    box,
			setCursor: func(row int) { box.cursor = row; box.syncFocus() },
			rowCount:  len(box.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, box.fields, mbfBinID),
				jdeRowChoice: elecRowOf(t, box.fields, mbfStatus),
			},
			saveVerb: "Save",
		},
	}
}

// TestJDESweepD_FormsAreColumnar: every field row hangs off the same leader
// column, and the labels are RIGHT-aligned into it — that is what makes a block
// of fields read as one sheet rather than a ragged list.
func TestJDESweepD_FormsAreColumnar(t *testing.T) {
	for _, tc := range jdeSweepDCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.screen.View()
			col, rows := jdeLeaderColumn(out)
			for _, line := range strings.Split(out, "\n") {
				at := strings.Index(line, jdeLeader)
				if at >= 0 && at != col {
					t.Errorf("a field row breaks the column (%d, want %d): %q", at, col, line)
				}
			}
			if rows == 0 {
				t.Errorf("no columnar rows rendered:\n%s", out)
			}
			padded := false
			for _, line := range strings.Split(out, "\n") {
				at := strings.Index(line, jdeLeader)
				if at < 0 || at != col {
					continue
				}
				label := strings.TrimSuffix(line[:at], " ")
				if len(label) > len(jdeIndent) && strings.HasPrefix(label, jdeIndent+" ") {
					padded = true
				}
			}
			if !padded {
				t.Errorf("no label is left-padded — are they right-aligned?\n%s", out)
			}
		})
	}
}

// TestJDESweepD_StorageFamilySharesOneColumn is this batch's headline. The four
// storage screens are reached from one another — the slots list opens the sheet
// and the generator, the slot detail opens the assign form and the stint
// occupying the slot, and the intake claims a slot back — so a column computed
// per-sheet would shift the form sideways as the operator walks the racking.
func TestJDESweepD_StorageFamilySharesOneColumn(t *testing.T) {
	want, wantFrom := -1, ""
	for _, tc := range jdeSweepDCases(t) {
		if !tc.storageFamily {
			continue
		}
		col, rows := jdeLeaderColumn(tc.screen.View())
		if rows == 0 {
			t.Fatalf("%s rendered no columnar rows", tc.name)
		}
		if want < 0 {
			want, wantFrom = col, tc.name
			continue
		}
		if col != want {
			t.Errorf("%s puts its leader at column %d but %s puts it at %d — the "+
				"racking shares ONE column", tc.name, col, wantFrom, want)
		}
	}
	// And it is the shared width itself, not four coincidences.
	if got := len(jdeIndent) + storageLabelWidth; got != want {
		t.Errorf("the rendered column is %d but storageLabelWidth implies %d", want, got)
	}
	// The width has to come from the widest label in the FAMILY, not one form's.
	widest := 0
	for _, labels := range []map[int]string{
		storageSlotFieldLabel, storageGenFieldLabel,
		storageAssignFieldLabel, projectStorageFieldLabel,
	} {
		for _, l := range labels {
			if n := lipgloss.Width(l); n > widest {
				widest = n
			}
		}
	}
	if widest > jdeLabelMaxWidth {
		widest = jdeLabelMaxWidth
	}
	if storageLabelWidth != widest {
		t.Errorf("storageLabelWidth = %d, want the family's widest label %d", storageLabelWidth, widest)
	}
}

// TestJDESweepD_OutsidersKeepTheirOwnColumn is the other half of that contract.
// Locations hang off inventory and a maker box off the bin list; neither is ever
// seen beside the racking, so folding maker box's 23-column "Conversion
// completed at" into the shared column would shove every storage row right to
// line up with a sheet the operator never sees next to it.
func TestJDESweepD_OutsidersKeepTheirOwnColumn(t *testing.T) {
	if makerBoxLabelWidth <= storageLabelWidth {
		t.Fatalf("this test assumes the maker-box sheet has the wider labels "+
			"(maker box %d, storage %d) — if that changed, the sharing argument needs re-checking",
			makerBoxLabelWidth, storageLabelWidth)
	}
	for _, tc := range jdeSweepDCases(t) {
		if tc.storageFamily {
			continue
		}
		col, _ := jdeLeaderColumn(tc.screen.View())
		if col == len(jdeIndent)+storageLabelWidth {
			t.Errorf("%s is drawing the racking's shared column (%d) — it is not in that family", tc.name, col)
		}
	}
	if got := jdeLabelWidth(jdeLabelFields(locationFieldLabel)); got != locationLabelWidth {
		t.Errorf("locationLabelWidth = %d, want its own widest label %d", locationLabelWidth, got)
	}
	if got := jdeLabelWidth(jdeLabelFields(makerBoxFieldLabel)); got != makerBoxLabelWidth {
		t.Errorf("makerBoxLabelWidth = %d, want its own widest label %d", makerBoxLabelWidth, got)
	}
}

// TestJDESweepD_RowsFitTheBody: clampToBox TRUNCATES an over-wide line rather
// than wrapping it, so a row that does not fit loses its tail with nothing on
// screen to say so — which for these forms means a silently missing HINT. A
// wide input area and a long note cannot both fit; this is what decides which
// one gives way. Carried over from sc-ye0i, which is where it caught two real
// overruns while it was being written.
func TestJDESweepD_RowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(jdeSweepDWidth)
	for _, tc := range jdeSweepDCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for row := 0; row < tc.rowCount; row++ {
				tc.setCursor(row)
				for _, line := range strings.Split(tc.screen.View(), "\n") {
					if w := lipgloss.Width(line); w > budget {
						t.Errorf("row %d: a line is %d wide but the pane is %d — it will be clipped: %q",
							row, w, budget, line)
					}
				}
			}
		})
	}
}

// TestJDESweepD_PickerRowsFitTheBody is the same check for the open pickers,
// whose notes and filter row are ours to keep inside the pane too.
func TestJDESweepD_PickerRowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(jdeSweepDWidth)
	for _, tc := range jdeSweepDCases(t) {
		row, ok := tc.kinds[jdeRowPicker]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(row)
			tc.screen.Update(ccCtrlEKey())
			for _, line := range strings.Split(tc.screen.View(), "\n") {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("a picker line is %d wide but the pane is %d: %q", w, budget, line)
				}
			}
			tc.screen.Update(ccEscKey())
		})
	}
}

// TestJDESweepD_BarIsPinnedToTheBottomOfThePane: "persistent" means the bar is
// on the same two rows on every frame — if the body were allowed to set the
// height, the bar would walk up and down as fields gained option strips.
func TestJDESweepD_BarIsPinnedToTheBottomOfThePane(t *testing.T) {
	for _, tc := range jdeSweepDCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for row := 0; row < tc.rowCount; row++ {
				tc.setCursor(row)
				lines := strings.Split(tc.screen.View(), "\n")
				if want := screenBodyHeight(jdeSweepHeight); len(lines) != want {
					t.Fatalf("row %d: view is %d rows, want the pane's budget of %d", row, len(lines), want)
				}
				if rule := lines[len(lines)-2]; strings.Trim(rule, "-") != "" || rule == "" {
					t.Errorf("row %d: the second-to-last row should be the bar's rule, got %q", row, rule)
				}
				if want := "Enter=" + tc.saveVerb; !strings.Contains(lines[len(lines)-1], want) {
					t.Errorf("row %d: the key line should offer %q, got %q", row, want, lines[len(lines)-1])
				}
			}
		})
	}
}

// TestJDESweepD_BarNamesOnlyTheKeysThatApply is the contract that replaces the
// letter accelerators: a key on the bar works here, and a key that works here is
// on the bar.
func TestJDESweepD_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	for _, tc := range jdeSweepDCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				tc.setCursor(row)
				bar := jdeBarLine(tc.screen.View())
				for _, want := range []string{"Enter=" + tc.saveVerb, "Esc=Cancel", "UP/DN=Fields"} {
					if !strings.Contains(bar, want) {
						t.Errorf("row %d: bar should always offer %q: %q", row, want, bar)
					}
				}
				switch kind {
				case jdeRowText:
					for _, deny := range []string{"Ctrl-E", "←→"} {
						if strings.Contains(bar, deny) {
							t.Errorf("a text row must not offer %q: %q", deny, bar)
						}
					}
				case jdeRowChoice:
					if !strings.Contains(bar, "←→=Change") {
						t.Errorf("a choice row should offer ←→=Change: %q", bar)
					}
					if strings.Contains(bar, "Ctrl-E") {
						t.Errorf("a choice row has nothing to open: %q", bar)
					}
				case jdeRowPicker:
					if !strings.Contains(bar, "Ctrl-E="+tc.pickerVerb) {
						t.Errorf("a picker row should offer Ctrl-E=%s: %q", tc.pickerVerb, bar)
					}
					if strings.Contains(bar, "←→") {
						t.Errorf("a picker row has nothing to cycle: %q", bar)
					}
				}
			}
		})
	}
}

// TestJDESweepD_NoLetterAcceleratorsRemain is the guard for the whole point of
// the redesign: space-opens-the-picker, the pickers' j/k and "/", and the level
// list's a/e/x are gone, so a stray letter on a row with no input must do
// NOTHING.
func TestJDESweepD_NoLetterAcceleratorsRemain(t *testing.T) {
	letters := []rune{'a', 'c', 'd', 'e', 'g', 'j', 'k', 'n', 'o', 'v', 'w', 'x', 'J', 'K'}
	for _, tc := range jdeSweepDCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				if kind == jdeRowText {
					continue // a letter on a text row is text, not a command
				}
				tc.setCursor(row)
				before := tc.screen.View()
				for _, letter := range letters {
					tc.screen.Update(runeKey(letter))
					if got := tc.screen.View(); got != before {
						t.Fatalf("row %d: %q changed the screen — no letter is bound here:\n%s", row, letter, got)
					}
				}
			}
		})
	}
}

// TestJDESweepD_LettersOnATextRowAreTyped is the other half: the reduced scheme
// took the accelerators away, not the ability to type.
func TestJDESweepD_LettersOnATextRowAreTyped(t *testing.T) {
	for _, tc := range jdeSweepDCases(t) {
		row, ok := tc.kinds[jdeRowText]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(row)
			before := tc.screen.View()
			tc.screen.Update(runeKey('w'))
			if tc.screen.View() == before {
				t.Errorf("a letter on a text row should be typed into it:\n%s", tc.screen.View())
			}
		})
	}
}

// TestJDESweepD_CtrlEOpensThePicker: EDIT opens whatever the highlighted row IS,
// and a text row has nothing to open — it must not fall through to something.
// Space used to be the opener on all of these forms; it is now filter text.
func TestJDESweepD_CtrlEOpensThePicker(t *testing.T) {
	for _, tc := range jdeSweepDCases(t) {
		row, ok := tc.kinds[jdeRowPicker]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(tc.kinds[jdeRowText])
			before := tc.screen.View()
			tc.screen.Update(ccCtrlEKey())
			if tc.screen.View() != before {
				t.Errorf("ctrl+e on a text row should do nothing:\n%s", tc.screen.View())
			}

			// Space no longer opens anything — it is a character now.
			tc.setCursor(row)
			before = tc.screen.View()
			tc.screen.Update(spaceKey())
			if tc.screen.View() != before {
				t.Errorf("space on a picker row should no longer open it:\n%s", tc.screen.View())
			}

			tc.screen.Update(ccCtrlEKey())
			out := tc.screen.View()
			if !strings.Contains(out, "Filter") {
				t.Fatalf("ctrl+e on a picker row should open its picker:\n%s", out)
			}
			bar := jdeBarLine(out)
			for _, want := range []string{"Esc=Cancel", "UP/DN=Move"} {
				if !strings.Contains(bar, want) {
					t.Errorf("the picker bar should offer %q: %q", want, bar)
				}
			}
			tc.screen.Update(ccEscKey())
		})
	}
}

// TestJDESweepD_PickerFiltersAsYouType: the filter is always live, so there is
// no mode to enter — which is what let j/k/"/" leave. Esc returns to the form
// without choosing, and what was typed is cleared.
func TestJDESweepD_PickerFiltersAsYouType(t *testing.T) {
	s := NewStorageSlotFormScreen(Deps{}, "")
	s.loading = false
	s.sigs = []omsapi.SIG{{ID: 3, Name: "Woodshop"}, {ID: 4, Name: "Metal shop"}}
	s.sigsReady = true
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, ssfOwningGroup)
	s.syncFocus()
	s.Update(ccCtrlEKey())
	if s.phase != slotPhasePick {
		t.Fatalf("ctrl+e should open the picker, phase=%v", s.phase)
	}

	for _, r := range "met" {
		s.Update(runeKey(r))
	}
	// The clear row survives every query: detaching the owner is an action, not
	// one of the values being searched.
	if len(s.pickRows) != 2 || !s.pickRows[0].clear || s.pickRows[1].label != "Metal shop" {
		t.Fatalf("typing should filter and keep the clear row, got %+v", s.pickRows)
	}
	if out := s.View(); !strings.Contains(out, "met") {
		t.Errorf("the pinned filter row should show what was typed:\n%s", out)
	}

	// j and k are LETTERS again: they go into the filter like any other, rather
	// than moving the selection behind the operator's back.
	at := s.pickCursor
	for _, r := range "jk" {
		s.Update(runeKey(r))
	}
	if got := s.pickSearch.Value(); got != "metjk" {
		t.Errorf("j/k should be typed into the filter, got %q", got)
	}
	if s.pickCursor != at {
		t.Errorf("j/k must not move the selection, cursor %d → %d", at, s.pickCursor)
	}

	// Esc leaves without choosing, and clears what was typed.
	s.Update(ccEscKey())
	if s.phase != slotPhaseForm || s.owningGroupID != nil {
		t.Errorf("esc should return to the form without choosing (phase=%v, id=%v)", s.phase, s.owningGroupID)
	}
	if s.pickSearch.Value() != "" {
		t.Errorf("the filter should be cleared on close, got %q", s.pickSearch.Value())
	}
}

// TestJDESweepD_PickerCursorClampsAtTheClearRow: a picker list is a set of
// choices, not a ring. Running off the bottom must not reappear at the "(none)"
// row, which on the owner picker RELEASES the reservation.
func TestJDESweepD_PickerCursorClampsAtTheClearRow(t *testing.T) {
	s := NewLocationFormScreen(Deps{}, "")
	s.loading = false
	s.locations = []omsapi.Location{{ID: 7, Name: "Bay 4"}, {ID: 8, Name: "Mezzanine"}}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, lfParent)
	s.syncFocus()
	s.Update(ccCtrlEKey())
	// Row 0 is the clear row; rows 1..2 are locations.
	for i := 0; i < 10; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if s.pickCursor != len(s.pickOptions)-1 {
		t.Errorf("down should stop at the last option, got %d of %d", s.pickCursor, len(s.pickOptions))
	}
	for i := 0; i < 10; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if s.pickCursor != 0 {
		t.Errorf("up should stop at the first option, got %d", s.pickCursor)
	}
}

// TestJDESweepD_LevelListIsASublistFold: the generator's level list took sweep
// B's sub-list fold. Adding is the trailing ROW (not `a`), editing is Ctrl-E on
// a row (not `e`/enter), removing is a row of the level's OWN editor (not `x`),
// and Enter and Esc are BOTH done — nothing is written until the rack is
// generated, so leaving the list has nothing to cancel.
func TestJDESweepD_LevelListIsASublistFold(t *testing.T) {
	s := NewStorageSlotGenerateScreen(Deps{}, 1)
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	s.levels = []storageGenLevelRow{{level: "A", positions: 4}, {level: "B", positions: 6}}
	s.openLevels()

	// The add row is one PAST the last level, and it is always reachable.
	for i := 0; i < 10; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if s.levelCursor != len(s.levels) {
		t.Fatalf("down should stop on the add row (%d), got %d", len(s.levels), s.levelCursor)
	}
	out := s.View()
	if !strings.Contains(out, "(add a level)") {
		t.Errorf("the list should end with the add row:\n%s", out)
	}
	if bar := jdeBarLine(out); !strings.Contains(bar, "Ctrl-E=Add a level") ||
		!strings.Contains(bar, "Enter=Done") || !strings.Contains(bar, "Esc=Done") {
		t.Errorf("the add row's bar should offer Ctrl-E=Add a level and two Dones: %q", bar)
	}

	// On a level, Ctrl-E EDITS — and the bar says so.
	s.levelCursor = 0
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "Ctrl-E=Edit") {
		t.Errorf("a level row's bar should offer Ctrl-E=Edit: %q", bar)
	}
	// No letter does anything here any more.
	before := s.View()
	for _, r := range []rune{'a', 'e', 'x', 'd', 'j', 'k'} {
		s.Update(runeKey(r))
		if s.View() != before {
			t.Fatalf("%q is still bound in the level list:\n%s", r, s.View())
		}
	}

	s.Update(ccCtrlEKey())
	if s.phase != genPhaseLevelRow || s.rowIndex != 0 {
		t.Fatalf("ctrl+e should open level 0's editor, phase=%v index=%d", s.phase, s.rowIndex)
	}
	// The remove row exists only for a level that is already there, and Ctrl-E
	// on it is what drops the level.
	if s.levelEditRows() != genRowFieldCount {
		t.Fatalf("an existing level's editor should offer the remove row")
	}
	s.rowCursor = genRowRemove
	out = s.View()
	if !strings.Contains(out, "Remove this level") {
		t.Errorf("the editor should carry the remove row:\n%s", out)
	}
	if bar := jdeBarLine(out); !strings.Contains(bar, "Ctrl-E=Remove") {
		t.Errorf("the remove row's bar should offer Ctrl-E=Remove: %q", bar)
	}
	s.Update(ccCtrlEKey())
	if len(s.levels) != 1 || s.levels[0].level != "B" {
		t.Errorf("the remove row should drop level A, got %+v", s.levels)
	}

	// Enter is DONE on the list, not a save — the run is what writes.
	s.Update(ccEnterKey())
	if s.phase != genPhaseForm {
		t.Errorf("enter should leave the list, phase=%v", s.phase)
	}
}

// TestJDESweepD_AddedLevelHasNoRemoveRow: a level being ADDED is not in the
// rack yet, so there is nothing for a remove row to drop — offering one would
// be a key that does nothing, which the bar contract forbids.
func TestJDESweepD_AddedLevelHasNoRemoveRow(t *testing.T) {
	s := NewStorageSlotGenerateScreen(Deps{}, 1)
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	s.openLevels()
	s.Update(ccCtrlEKey()) // the add row
	if s.rowIndex != -1 {
		t.Fatalf("the add row should open an ADD, got index %d", s.rowIndex)
	}
	if s.levelEditRows() != genRowFieldCount-1 {
		t.Errorf("an added level should have %d rows, got %d", genRowFieldCount-1, s.levelEditRows())
	}
	out := s.View()
	if strings.Contains(out, "Remove this level") {
		t.Errorf("an added level must not offer a remove row:\n%s", out)
	}
	if bar := jdeBarLine(out); strings.Contains(bar, "Ctrl-E") {
		t.Errorf("nothing on an added level's editor opens anything: %q", bar)
	}
}

// TestJDESweepD_GenerateResultScrolls: an idempotent run's created / skipped /
// without-tag lists are the whole point of the screen, and a 200-slot rack has
// more of them than the pane is tall — so the report is a scrollable body with
// its own bar rather than a wall that clampToBox silently cuts.
func TestJDESweepD_GenerateResultScrolls(t *testing.T) {
	s := NewStorageSlotGenerateScreen(Deps{}, 1)
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: 18})
	created := make([]string, 30)
	for i := range created {
		created[i] = "1A" + string(rune('0'+i%10))
	}
	s.Update(storageGenDoneMsg{result: &omsapi.GenerateRackResult{
		Rack: 1, CreatedCount: len(created), Created: created,
		SkippedCount: 2, Skipped: []string{"1B1", "1B2"},
		WithoutTag: []string{"1C9"},
	}})
	if s.phase != genPhaseResult {
		t.Fatalf("a finished run should show its report, phase=%v", s.phase)
	}
	out := s.View()
	if want := screenBodyHeight(18); len(strings.Split(out, "\n")) != want {
		t.Errorf("the report should fill exactly the pane's %d rows", want)
	}
	budget := screenBodyWidth(jdeSweepDWidth)
	for _, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > budget {
			t.Errorf("a report line is %d wide but the pane is %d: %q", w, budget, line)
		}
	}
	bar := jdeBarLine(out)
	for _, want := range []string{"Enter=Back to the rack", "Esc=Back", "UP/DN=Scroll", "PgUp/PgDn=Page"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the report bar should offer %q: %q", want, bar)
		}
	}
	// Scrolling clamps at both ends rather than wrapping.
	for i := 0; i < 60; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	atEnd := s.resultCursor
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	if s.resultCursor != atEnd {
		t.Errorf("scrolling past the end should clamp, %d → %d", atEnd, s.resultCursor)
	}
	for i := 0; i < 60; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if s.resultCursor != 0 {
		t.Errorf("scrolling past the top should clamp at 0, got %d", s.resultCursor)
	}
}

// TestJDESweepD_SlotPickerRetryIsOnTheBarOnlyWhenItApplies: the intake's slot
// list is a staff surface while the claim itself is not, so a 403 has to be
// retryable. `g` was a letter accelerator and the always-live filter owns every
// letter now, so the retry became Ctrl-R — named on the bar, and ONLY where
// there is something to retry.
func TestJDESweepD_SlotPickerRetryIsOnTheBarOnlyWhenItApplies(t *testing.T) {
	ok := NewProjectStorageFormScreen(Deps{})
	ok.slotsReady = true
	ok.slots = []omsapi.StorageSlot{{ID: 1, Code: "1A1", IsActive: true}}
	ok.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	ok.cursor = indexOfField(ok.fields, psfSlot)
	ok.syncFocus()
	ok.Update(ccCtrlEKey())
	if bar := jdeBarLine(ok.View()); strings.Contains(bar, "Ctrl-R") {
		t.Errorf("a healthy list has nothing to retry: %q", bar)
	}

	failed := NewProjectStorageFormScreen(Deps{})
	failed.slotsErr = "oms: http 403: forbidden"
	failed.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	failed.cursor = indexOfField(failed.fields, psfSlot)
	failed.syncFocus()
	failed.Update(ccCtrlEKey())
	out := failed.View()
	if bar := jdeBarLine(out); !strings.Contains(bar, "Ctrl-R=Retry list") {
		t.Errorf("a failed list should offer Ctrl-R on the bar: %q", bar)
	}
	if !strings.Contains(out, "type the code off the card") {
		t.Errorf("the note should still point at the typed-code hatch:\n%s", out)
	}
	// And `g` — the old retry key — is filter text now.
	failed.Update(runeKey('g'))
	if got := failed.pickSearch.Value(); got != "g" {
		t.Errorf("g should be typed into the filter, got %q", got)
	}
}

// TestJDESweepD_PagingClamps: a page is for covering ground in a sheet taller
// than the pane; one that wrapped would lose the operator's place. The maker-box
// sheet has twelve fields, which is what makes it the one that needs paging.
func TestJDESweepD_PagingClamps(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: 18})
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "PgUp/PgDn=Page") {
		t.Fatalf("a sheet taller than the pane should offer paging: %q", bar)
	}
	for i := 0; i < 20; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if s.cursor != len(s.fields)-1 {
		t.Errorf("paging down should stop at the last row, got %d of %d", s.cursor, len(s.fields))
	}
	for i := 0; i < 20; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if s.cursor != 0 {
		t.Errorf("paging up should stop at the first row, got %d", s.cursor)
	}
}

// TestJDESweepD_SlotCodeIsARowOfTheSheet: the slot's code is DERIVED from the
// three components below it, and it used to be a header line above the form.
// It is now a dimmed, non-navigable row in the sheet (sweep B's device-type
// fold) so the operator ties it to the fields that compute it — and the cursor
// still never lands on it.
func TestJDESweepD_SlotCodeIsARowOfTheSheet(t *testing.T) {
	s := NewStorageSlotFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	s.inputs[ssfRack].SetValue("1")
	s.inputs[ssfLevel].SetValue("a")
	s.inputs[ssfPosition].SetValue("12")

	out := s.View()
	if !strings.Contains(out, "Code"+jdeLeader) {
		t.Errorf("the sheet should carry a Code row:\n%s", out)
	}
	if !strings.Contains(out, "1A12") {
		t.Errorf("the Code row should preview what the components spell:\n%s", out)
	}
	if !strings.Contains(out, "save allocates an AprilTag") {
		t.Errorf("a create should say the save allocates a marker:\n%s", out)
	}
	// It is not navigable: every cursor position is one of the real fields.
	body := s.formLines()
	for row := 0; row < len(s.fields); row++ {
		first, _ := body.block(row)
		if first == 0 {
			t.Errorf("row %d resolved to the top of the body — is the Code row navigable?", row)
		}
	}
	// An EDIT says what changing a component costs instead.
	e := NewStorageSlotFormScreen(Deps{}, "1A1")
	e.loading = false
	e.Update(tea.WindowSizeMsg{Width: jdeSweepDWidth, Height: jdeSweepHeight})
	if out := e.View(); !strings.Contains(out, "renames the slot") {
		t.Errorf("an edit should warn that a component change renames the slot:\n%s", out)
	}
}
