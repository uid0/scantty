// The ELECTRICAL family of forms on the columnar "JD Edwards" layer (sc-ye0i,
// sweep C of the sc-h412 redesign): panel, breaker, circuit, outlet and
// disconnect — the power topology, top to bottom.
//
// These hold all five to the same contract sweeps A and B hold their families
// to, because the persistent action bar is now the only place an operator can
// learn what a key does:
//
//	the sheet is columnar   — every field row hangs off ONE leader column
//	the bar is persistent   — same two rows of the pane on every frame
//	the bar is honest       — a key on it works here, and a key that works
//	                          here is on it
//	no letter accelerators  — a stray letter on a row with no input does
//	                          NOTHING rather than firing something invisible
//
// Plus the two this batch owns. These five screens are reached from ONE
// another — a panel drills to its breakers, a breaker to its circuits, a
// circuit to its outlets and disconnects — so:
//
//	one column for the family — the leader lands in the same place on all
//	                            five, or walking the tree reads as the form
//	                            jumping sideways
//	rows fit the pane         — clampToBox TRUNCATES an over-wide row, so a
//	                            hint that does not fit is a hint silently lost
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// jdeSweepCWidth is the terminal the family is measured at — the width sc-akwv
// eyeballed every screen at, and what screenBodyWidth turns into the pane.
const jdeSweepCWidth = 110

// jdeSweepCCase is one converted form, loaded and ready to drive.
type jdeSweepCCase struct {
	name      string
	screen    Screen
	setCursor func(row int)
	rowCount  int
	kinds     map[jdeRowKind]int // kind -> a row of that kind (absent = none)
	// pickerVerb is what the bar calls Ctrl-E on the picker row.
	pickerVerb string
}

func jdeSweepCCases(t *testing.T) []jdeSweepCCase {
	t.Helper()
	size := tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: jdeSweepHeight}
	locs := []omsapi.Location{{ID: 3, Name: "Wood shop"}, {ID: 4, Name: "Metal shop"}}
	circuits := []omsapi.PowerCircuitDetail{
		{ID: 100, PanelID: 9, Label: "north wall", BreakerLabel: "pos 4", PanelName: "Main"},
		{ID: 101, PanelID: 9, Label: "east bench", BreakerLabel: "pos 6", PanelName: "Main"},
	}

	panel := NewPowerPanelFormScreen(Deps{}, 0)
	panel.loading = false
	panel.locations = locs
	panel.circuits = circuits
	panel.Update(size)

	// TWO options apiece. UP/DN is conditional on a second row to move to
	// (jdeRowMoves), so a one-option picker cannot reach the claim the picker-bar
	// assertion below makes — the vacuous-fixture rule in the form where the
	// fixture stops an assertion being true rather than making it trivially so.
	brk := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	brk.loading = false
	brk.panels = []omsapi.PowerPanel{
		{ID: 9, Name: "Main", LocationName: "Wood shop", PhaseConfiguration: "three"},
		{ID: 10, Name: "Sub A", LocationName: "Metal shop", PhaseConfiguration: "single"},
	}
	brk.Update(size)

	cir := NewPowerCircuitFormScreen(Deps{}, 0, 0, 3)
	cir.loading = false
	cir.breakers = []omsapi.PowerBreakerDetail{
		{ID: 5, Position: "4", Amperage: 20, PoleCount: 1, Label: "north"},
		{ID: 6, Position: "6", Amperage: 30, PoleCount: 2, Label: "east"},
	}
	cir.Update(size)

	out := NewPowerOutletFormScreen(Deps{}, 0, 0, 0)
	out.loading = false
	out.locations = locs
	out.circuits = circuits
	out.disconnects = []omsapi.DisconnectDetail{{ID: 8, Label: "welder disc", DisconnectType: "fused"}}
	out.Update(size)

	dc := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	dc.loading = false
	dc.locations = locs
	dc.circuits = circuits
	dc.lotoDevices = []omsapi.LOTODevice{
		{ID: 7, DeviceType: "breaker_lock", DeviceTypeDisplay: "Breaker lock", Label: "BL-1", Status: "available"},
		{ID: 9, DeviceType: "padlock", DeviceTypeDisplay: "Padlock", Label: "PAD-2", Status: "available"},
	}
	dc.Update(size)

	return []jdeSweepCCase{
		{
			name:      "panel",
			screen:    panel,
			setCursor: func(row int) { panel.cursor = row; panel.syncFocus() },
			rowCount:  len(panel.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, panel.fields, ppName),
				jdeRowChoice: elecRowOf(t, panel.fields, ppPhaseConfig),
				jdeRowPicker: elecRowOf(t, panel.fields, ppLocation),
			},
			pickerVerb: "Pick",
		},
		{
			name:      "breaker",
			screen:    brk,
			setCursor: func(row int) { brk.cursor = row; brk.syncFocus() },
			rowCount:  len(brk.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, brk.fields, pbPosition),
				jdeRowChoice: elecRowOf(t, brk.fields, pbPoleCount),
				jdeRowPicker: elecRowOf(t, brk.fields, pbPanel),
			},
			pickerVerb: "Pick",
		},
		{
			name:      "circuit",
			screen:    cir,
			setCursor: func(row int) { cir.cursor = row; cir.syncFocus() },
			rowCount:  len(cir.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, cir.fields, pcLabel),
				jdeRowChoice: elecRowOf(t, cir.fields, pcNeedsReview),
				jdeRowPicker: elecRowOf(t, cir.fields, pcBreaker),
			},
			pickerVerb: "Pick",
		},
		{
			name:      "outlet",
			screen:    out,
			setCursor: func(row int) { out.cursor = row; out.syncFocus() },
			rowCount:  len(out.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, out.fields, poLabel),
				jdeRowChoice: elecRowOf(t, out.fields, poOutletType),
				jdeRowPicker: elecRowOf(t, out.fields, poCircuit),
			},
			pickerVerb: "Pick",
		},
		{
			name:      "disconnect",
			screen:    dc,
			setCursor: func(row int) { dc.cursor = row; dc.syncFocus() },
			rowCount:  len(dc.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   elecRowOf(t, dc.fields, dcLabel),
				jdeRowChoice: elecRowOf(t, dc.fields, dcDisconnectType),
				jdeRowPicker: elecRowOf(t, dc.fields, dcCircuit),
			},
			pickerVerb: "Pick",
		},
	}
}

// elecRowOf finds a field id's cursor position. The breaker's visible rows are
// conditional, so these are not constants.
func elecRowOf(t *testing.T, fields []int, id int) int {
	t.Helper()
	if i := indexOfField(fields, id); i >= 0 {
		return i
	}
	t.Fatalf("field %d is not visible on this form", id)
	return 0
}

// jdeLeaderColumn returns the column every field row's leader lands in, and how
// many rows were found.
func jdeLeaderColumn(view string) (int, int) {
	col, rows := -1, 0
	for _, line := range strings.Split(view, "\n") {
		at := strings.Index(line, jdeLeader)
		if at < 0 {
			continue
		}
		rows++
		if col < 0 {
			col = at
		}
	}
	return col, rows
}

// TestJDESweepC_FormsAreColumnar: every field row hangs off the same leader
// column, and the labels are RIGHT-aligned into it — that is what makes a block
// of fields read as one sheet rather than a ragged list.
func TestJDESweepC_FormsAreColumnar(t *testing.T) {
	for _, tc := range jdeSweepCCases(t) {
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

// TestJDESweepC_LabelColumnIsSharedAcrossTheFamily is this batch's own
// contract. These five screens are reached from one another, so a column
// computed per-sheet would shift the whole form sideways as the operator walks
// panel → breaker → circuit → outlet → disconnect. One column, computed over
// all five label sets, is what stops that.
func TestJDESweepC_LabelColumnIsSharedAcrossTheFamily(t *testing.T) {
	cases := jdeSweepCCases(t)
	want, wantFrom := -1, ""
	for _, tc := range cases {
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
				"family shares ONE column", tc.name, col, wantFrom, want)
		}
	}
	// And it is the shared width itself, not five coincidences.
	if got := len(jdeIndent) + elecLabelWidth; got != want {
		t.Errorf("the rendered column is %d but elecLabelWidth implies %d", want, got)
	}
	// The width has to come from the widest label in the FAMILY, not one form's.
	widest := 0
	for _, labels := range []map[int]string{
		panelFieldLabel, breakerFieldLabel, circuitFieldLabel,
		outletFieldLabel, disconnectFieldLabel,
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
	if elecLabelWidth != widest {
		t.Errorf("elecLabelWidth = %d, want the family's widest label %d", elecLabelWidth, widest)
	}
}

// TestJDESweepC_RowsFitTheBody: clampToBox TRUNCATES an over-wide line rather
// than wrapping it, so a row that does not fit loses its tail with nothing on
// screen to say so — which for these forms means a silently missing HINT. A
// wide input area and a long note cannot both fit; this is what decides which
// one gives way.
func TestJDESweepC_RowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(jdeSweepCWidth)
	for _, tc := range jdeSweepCCases(t) {
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

// TestJDESweepC_BarIsPinnedToTheBottomOfThePane: "persistent" means the bar is
// on the same two rows on every frame — if the body were allowed to set the
// height, the bar would walk up and down as fields gained option strips.
func TestJDESweepC_BarIsPinnedToTheBottomOfThePane(t *testing.T) {
	for _, tc := range jdeSweepCCases(t) {
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
				if !strings.Contains(lines[len(lines)-1], "Enter=Save") {
					t.Errorf("row %d: the key line should offer Enter=Save, got %q", row, lines[len(lines)-1])
				}
			}
		})
	}
}

// TestJDESweepC_BarNamesOnlyTheKeysThatApply is the contract that replaces the
// letter accelerators: a key on the bar works here, and a key that works here is
// on the bar.
func TestJDESweepC_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	for _, tc := range jdeSweepCCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				tc.setCursor(row)
				bar := jdeBarLine(tc.screen.View())
				for _, want := range []string{"Enter=Save", "Esc=Cancel", "UP/DN=Fields"} {
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

// TestJDESweepC_NoLetterAcceleratorsRemain is the guard for the whole point of
// the redesign: space-opens-the-picker and the pickers' j/k are gone, and a
// stray letter on a row with no input must do NOTHING.
func TestJDESweepC_NoLetterAcceleratorsRemain(t *testing.T) {
	letters := []rune{'a', 'c', 'd', 'e', 'g', 'j', 'k', 'n', 'o', 'v', 'w', 'x', 'J', 'K'}
	for _, tc := range jdeSweepCCases(t) {
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

// TestJDESweepC_LettersOnATextRowAreTyped is the other half: the reduced scheme
// took the accelerators away, not the ability to type.
func TestJDESweepC_LettersOnATextRowAreTyped(t *testing.T) {
	for _, tc := range jdeSweepCCases(t) {
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

// TestJDESweepC_CtrlEOpensThePicker: EDIT opens whatever the highlighted row IS,
// and a text row has nothing to open — it must not fall through to something.
// Space used to be the opener on all five of these forms; it is now filter text.
func TestJDESweepC_CtrlEOpensThePicker(t *testing.T) {
	for _, tc := range jdeSweepCCases(t) {
		row, ok := tc.kinds[jdeRowPicker]
		if !ok {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.setCursor(tc.kinds[jdeRowText])
			before := tc.screen.View()
			tc.screen.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
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

			tc.screen.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
			out := tc.screen.View()
			if !strings.Contains(out, "Filter") {
				t.Fatalf("ctrl+e on a picker row should open its picker:\n%s", out)
			}
			bar := jdeBarLine(out)
			for _, want := range []string{"Enter=Select", "Esc=Cancel", "UP/DN=Move"} {
				if !strings.Contains(bar, want) {
					t.Errorf("the picker bar should offer %q: %q", want, bar)
				}
			}
		})
	}
}

// TestJDESweepC_PickerFiltersAsYouType: the filter is always live, so there is
// no mode to enter — which is what let j/k/"/" leave. Esc returns to the form
// without choosing.
func TestJDESweepC_PickerFiltersAsYouType(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 0)
	s.loading = false
	s.locations = []omsapi.Location{{ID: 4, Name: "Wood shop"}, {ID: 5, Name: "Metal shop"}}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, ppLocation)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != elecPhasePick {
		t.Fatalf("ctrl+e should open the picker, phase=%v", s.phase)
	}

	for _, r := range "met" {
		s.Update(runeKey(r))
	}
	if len(s.pickOptions) != 1 || s.pickOptions[0].label != "Metal shop" {
		t.Fatalf("typing should filter, got %+v", s.pickOptions)
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
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != elecPhaseForm || s.locationID != nil {
		t.Errorf("esc should return to the form without choosing (phase=%v, id=%v)", s.phase, s.locationID)
	}
	if s.pickSearch.Value() != "" {
		t.Errorf("the filter should be cleared on close, got %q", s.pickSearch.Value())
	}
}

// TestJDESweepC_PickerCursorClampsAtTheClearRow: a picker list is a set of
// choices, not a ring. Running off the bottom must not reappear at the
// "(none)" row, which on the fed-by picker CLEARS a field.
func TestJDESweepC_PickerCursorClampsAtTheClearRow(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 0)
	s.loading = false
	s.circuits = []omsapi.PowerCircuitDetail{
		{ID: 100, PanelID: 9, Label: "north wall"},
		{ID: 101, PanelID: 9, Label: "east bench"},
	}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, ppFedBy)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	// Row 0 is the clear row; rows 1..2 are circuits.
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

// TestJDESweepC_LOTOMultiPickerTogglesWithEnter: the always-live filter owns
// space, so the LOTO picker's "select" IS the toggle — and esc is DONE there,
// because every toggle was applied as it was made (sc-0zvi's fold).
func TestJDESweepC_LOTOMultiPickerTogglesWithEnter(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	s.loading = false
	s.lotoDevices = []omsapi.LOTODevice{
		{ID: 7, DeviceTypeDisplay: "Breaker lock", Label: "BL-1", Status: "available"},
		{ID: 9, DeviceTypeDisplay: "Padlock", Label: "PAD-2", Status: "available"},
	}
	s.Update(tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, dcLOTODevices)
	s.syncFocus()

	bar := jdeBarLine(s.View())
	if !strings.Contains(bar, "Ctrl-E=Choose") {
		t.Errorf("the LOTO row should offer Ctrl-E=Choose: %q", bar)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != elecPhasePick {
		t.Fatalf("ctrl+e should open the multi picker, phase=%v", s.phase)
	}
	bar = jdeBarLine(s.View())
	for _, want := range []string{"Enter=Toggle", "Esc=Done"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the multi-picker bar should offer %q: %q", want, bar)
		}
	}

	// Enter toggles the highlighted device rather than closing.
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !s.lotoSelected(7) || s.phase != elecPhasePick {
		t.Fatalf("enter should toggle and stay open (ids=%v, phase=%v)", s.lotoDeviceIDs, s.phase)
	}
	if out := s.View(); !strings.Contains(out, "[x] Breaker lock · BL-1") {
		t.Errorf("a chosen device should be marked:\n%s", out)
	}
	// Space is filter text here, not a toggle — which is what retired it.
	s.Update(spaceKey())
	if s.pickSearch.Value() != " " {
		t.Errorf("space should go into the always-live filter, got %q", s.pickSearch.Value())
	}
	// Esc is done, and what was toggled survives.
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != elecPhaseForm || !s.lotoSelected(7) {
		t.Errorf("esc should close and keep the toggles (phase=%v, ids=%v)", s.phase, s.lotoDeviceIDs)
	}
}

// TestJDESweepC_BreakerConditionalRowsKeepTheColumn: the critical pair and the
// review note appear and vanish as their toggles move. They are rows of the
// same sheet, so they must not compute a column of their own — and the bar
// must stay pinned across the reflow.
func TestJDESweepC_BreakerConditionalRowsKeepTheColumn(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: jdeSweepHeight})

	before, _ := jdeLeaderColumn(s.View())
	rowsBefore := len(s.fields)

	s.setCursorToField(pbIsCritical)
	s.Update(tea.KeyMsg{Type: tea.KeyRight}) // a toggle is a choice row now
	if !s.isCritical {
		t.Fatalf("←/→ should flip the critical toggle")
	}
	if len(s.fields) != rowsBefore+2 {
		t.Fatalf("critical category+note should appear, rows %d → %d", rowsBefore, len(s.fields))
	}
	if !fieldsContain(s.fields, pbCriticalCategory) || !fieldsContain(s.fields, pbCriticalNote) {
		t.Errorf("the critical pair is missing from the sheet")
	}

	out := s.View()
	after, _ := jdeLeaderColumn(out)
	if after != before {
		t.Errorf("the sheet's column moved when the critical rows appeared (%d → %d)", before, after)
	}
	if want := screenBodyHeight(jdeSweepHeight); len(strings.Split(out, "\n")) != want {
		t.Errorf("the pane should still be exactly %d rows tall", want)
	}
}

// TestJDESweepC_BreakerPhaseAutoStateIsAHint: the web form's auto-calc state
// used to be appended to the phase VALUE. Inside a "< … >" choice row that
// would sit between the brackets — and inside a focused row's reverse-video
// run — so it is a hint after the input area instead.
func TestJDESweepC_BreakerPhaseAutoStateIsAHint(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	s.loading = false
	s.panels = []omsapi.PowerPanel{{ID: 9, Name: "Main", PhaseConfiguration: "three"}}
	pid := 9
	s.panelID = &pid
	s.Update(tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: jdeSweepHeight})

	// A numeric slot derives the phase, and the row says so. syncFocus is what
	// moveCursor does on every real cursor move — setCursorToField alone leaves
	// the textinput blurred, and a blurred box swallows the keystroke.
	s.setCursorToField(pbPosition)
	s.syncFocus()
	s.Update(runeKey('5'))
	if got := s.fieldHint(pbPhase); !strings.Contains(got, "auto") || strings.Contains(got, "skipped") {
		t.Errorf("a numeric slot should report auto-calc live, got %q", got)
	}
	if got := breakerPhaseOptions[s.phaseIdx].value; got != "C" {
		t.Errorf("slot 5 on a three-phase panel is C, got %q", got)
	}
	// The value between the brackets is the phase ALONE.
	if got := s.selectValue(pbPhase); got != "C" {
		t.Errorf("the choice row's value should be the bare phase, got %q", got)
	}

	// A tandem slot cannot be derived, and the row says THAT.
	s.inputs[pbPosition].SetValue("14/16")
	if got := s.fieldHint(pbPhase); !strings.Contains(got, "skipped") {
		t.Errorf("a tandem slot should report auto-calc skipped, got %q", got)
	}
	// Pinning the phase by hand retires the note entirely.
	s.setCursorToField(pbPhase)
	s.syncFocus()
	s.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !s.phaseManuallySet {
		t.Fatalf("←/→ on the phase row should pin it")
	}
	if got := s.fieldHint(pbPhase); got != "" {
		t.Errorf("a pinned phase has no auto-calc note, got %q", got)
	}
}

// TestJDESweepC_PagingClamps: a page is for covering ground in a sheet taller
// than the pane; one that wrapped would lose the operator's place.
func TestJDESweepC_PagingClamps(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 0)
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: jdeSweepCWidth, Height: 18})
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
