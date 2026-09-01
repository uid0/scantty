// The asset, maintenance and climate family of forms on the columnar "JD
// Edwards" layer (sc-0zvi, sweep B of the sc-h412 redesign): the asset form, the
// PM-item form and its three sub-lists, the thermostat form, ForgeKey device
// types and the location-problem report.
//
// These hold all five to the same contract sweep A's tests hold the inventory
// family to, because the persistent action bar is now the only place an operator
// can learn what a key does:
//
//	the sheet is columnar   — every field row hangs off ONE leader column
//	the bar is persistent   — same two rows of the pane on every frame
//	the bar is honest       — a key on it works here, and a key that works
//	                          here is on it
//	no letter accelerators  — a stray letter on a row with no input does
//	                          NOTHING rather than firing something invisible
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// jdeSweepBCase is one converted form, loaded and ready to drive.
type jdeSweepBCase struct {
	name   string
	screen Screen
	// setCursor puts the form on a row; kinds says what kind each named row is.
	setCursor func(row int)
	rowCount  int
	kinds     map[jdeRowKind]int // kind -> a row of that kind (absent = none)
	// submitVerb is what the bar calls Enter on this sheet, and pickerVerb what
	// it calls Ctrl-E on the picker row.
	submitVerb string
	pickerVerb string
}

func jdeSweepBCases(t *testing.T) []jdeSweepBCase {
	t.Helper()
	size := tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight}

	asset := NewAssetFormScreen(Deps{}, "")
	asset.loading = false
	asset.categories = []omsapi.Category{{ID: 1, Name: "Machines"}}
	asset.locations = []omsapi.Location{{ID: 2, Name: "Wood shop"}}
	asset.Update(size)

	// TWO options apiece, not one. UP/DN is conditional on there being a second
	// row to move to (jdeRowMoves), so a one-option picker is a fixture that
	// cannot reach the claim the picker-bar assertion below makes — the
	// vacuous-fixture rule, in the form where the fixture stops the assertion
	// being true rather than making it trivially so. The asset case already
	// carried enough (its picker prepends a "(none)" row), which is why only
	// these two were short.
	pm := NewMaintenanceItemFormScreen(Deps{}, "")
	pm.loading = false
	pm.assets = []omsapi.Asset{
		{ID: "a1", Name: "Lathe", AssetTag: "LT-1"},
		{ID: "a2", Name: "Bandsaw", AssetTag: "BS-1"},
	}
	pm.Update(size)

	therm := NewThermostatFormScreen(Deps{}, "")
	therm.loading = false
	therm.locations = []omsapi.Location{
		{ID: 2, Name: "Wood shop", Code: "WS"},
		{ID: 3, Name: "Metal shop", Code: "MS"},
	}
	therm.Update(size)

	dtype := NewDeviceTypeFormScreen(Deps{}, 0)
	dtype.loading = false
	dtype.Update(size)

	problem := NewLocationProblemFormScreen(Deps{}, 42, "Bay 3")
	problem.Update(size)

	return []jdeSweepBCase{
		{
			name:      "asset",
			screen:    asset,
			setCursor: func(row int) { asset.cursor = row; asset.syncFocus() },
			rowCount:  len(asset.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   assetRowOf(t, asset, afName),
				jdeRowChoice: assetRowOf(t, asset, afStatus),
				jdeRowPicker: assetRowOf(t, asset, afCategory),
			},
			submitVerb: "Save",
			pickerVerb: "Pick",
		},
		{
			name:      "pm item",
			screen:    pm,
			setCursor: func(row int) { pm.cursor = row; pm.syncFocus() },
			rowCount:  len(pm.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   1, // mfTitle
				jdeRowChoice: 7, // mfIsActive
				jdeRowPicker: 0, // mfAsset
			},
			submitVerb: "Save",
			pickerVerb: "Pick",
		},
		{
			name:      "thermostat",
			screen:    therm,
			setCursor: func(row int) { therm.cursor = row; therm.syncFocus() },
			rowCount:  len(therm.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   0, // tfLabel
				jdeRowPicker: 1, // tfLocation
			},
			submitVerb: "Save",
			pickerVerb: "Pick",
		},
		{
			name:      "device type",
			screen:    dtype,
			setCursor: func(row int) { dtype.cursor = row; dtype.syncFocus() },
			rowCount:  len(dtype.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   0, // dtName
				jdeRowChoice: 1, // dtCode
			},
			submitVerb: "Save",
		},
		{
			name:      "location problem",
			screen:    problem,
			setCursor: func(row int) { problem.cursor = row; problem.syncFocus() },
			rowCount:  len(problem.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   0, // lpfDescription
				jdeRowChoice: 1, // lpfSeverity
			},
			submitVerb: "Report",
		},
	}
}

// spaceKey is the space bar as bubbletea actually delivers it: KeySpace, but
// carrying the rune, which is what a textinput inserts. A KeySpace with no
// Runes types nothing, so a test that built one would "prove" the filter
// ignores spaces.
func spaceKey() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
}

// assetRowOf finds the asset form's cursor position for a field id — its
// visible rows are conditional, so the positions are not constants.
func assetRowOf(t *testing.T, s *AssetFormScreen, id int) int {
	t.Helper()
	for i, fid := range s.fields {
		if fid == id {
			return i
		}
	}
	t.Fatalf("field %d is not visible on the asset form", id)
	return 0
}

// TestJDESweepB_FormsAreColumnar: every field row hangs off the same leader
// column, and the labels are RIGHT-aligned into it — that is what makes a block
// of fields read as one sheet rather than a ragged list.
func TestJDESweepB_FormsAreColumnar(t *testing.T) {
	for _, tc := range jdeSweepBCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.screen.View()
			col, rows := -1, 0
			for _, line := range strings.Split(out, "\n") {
				at := strings.Index(line, jdeLeader)
				if at < 0 {
					continue
				}
				rows++
				if col < 0 {
					col = at
				} else if at != col {
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

// TestJDESweepB_BarIsPinnedToTheBottomOfThePane: "persistent" means the bar is
// on the same two rows on every frame — if the body were allowed to set the
// height, the bar would walk up and down as fields gained option strips.
func TestJDESweepB_BarIsPinnedToTheBottomOfThePane(t *testing.T) {
	for _, tc := range jdeSweepBCases(t) {
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
				if want := "Enter=" + tc.submitVerb; !strings.Contains(lines[len(lines)-1], want) {
					t.Errorf("row %d: the key line should offer %q, got %q", row, want, lines[len(lines)-1])
				}
			}
		})
	}
}

// TestJDESweepB_BarNamesOnlyTheKeysThatApply is the contract that replaces the
// letter accelerators: a key on the bar works here, and a key that works here is
// on the bar.
func TestJDESweepB_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	for _, tc := range jdeSweepBCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				tc.setCursor(row)
				bar := jdeBarLine(tc.screen.View())
				for _, want := range []string{"Enter=" + tc.submitVerb, "Esc=", "UP/DN=Fields"} {
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

// TestJDESweepB_NoLetterAcceleratorsRemain is the guard for the whole point of
// the redesign: space-opens-the-picker, the sub-lists' a/e/d/x and the pickers'
// j/k are gone, and a stray letter on a row with no input must do NOTHING.
func TestJDESweepB_NoLetterAcceleratorsRemain(t *testing.T) {
	letters := []rune{'a', 'c', 'd', 'e', 'g', 'j', 'k', 'n', 'v', 'w', 'x', 'J', 'K'}
	for _, tc := range jdeSweepBCases(t) {
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

// TestJDESweepB_LettersOnATextRowAreTyped is the other half: the reduced scheme
// took the accelerators away, not the ability to type.
func TestJDESweepB_LettersOnATextRowAreTyped(t *testing.T) {
	for _, tc := range jdeSweepBCases(t) {
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

// TestJDESweepB_CtrlEOpensThePicker: EDIT opens whatever the highlighted row IS,
// and a text row has nothing to open — it must not fall through to something.
func TestJDESweepB_CtrlEOpensThePicker(t *testing.T) {
	for _, tc := range jdeSweepBCases(t) {
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

			tc.setCursor(row)
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

// TestJDESweepB_PickerFiltersAsYouType: the filter is always live, so there is
// no mode to enter — which is what let j/k/"/" leave. Esc returns to the form
// without choosing.
func TestJDESweepB_PickerFiltersAsYouType(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "")
	s.loading = false
	s.locations = []omsapi.Location{{ID: 4, Name: "Wood shop"}, {ID: 5, Name: "Metal shop"}}
	s.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})
	s.cursor = indexOfField(s.fields, tfLocation)
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != thermostatPhasePick {
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
	if s.phase != thermostatPhaseForm || s.locationID != nil {
		t.Errorf("esc should return to the form without choosing (phase=%v, id=%v)", s.phase, s.locationID)
	}
	if s.pickSearch.Value() != "" {
		t.Errorf("the filter should be cleared on close, got %q", s.pickSearch.Value())
	}
}

// TestJDESweepB_AssetSheetHasBandHeadings: thirty-one fields in one undivided
// run is a wall. The sheet breaks into the sections a printed asset record would
// have, and each heading is drawn where its band starts.
func TestJDESweepB_AssetSheetHasBandHeadings(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 90})

	out := s.View()
	at := -1
	for _, band := range []assetFieldBand{
		assetBandIdentity, assetBandClassification, assetBandAcquisition,
		assetBandOwnership, assetBandDocumentation, assetBandFacility,
		assetBandAccess, assetBandNotes,
	} {
		label := assetBandLabel[band]
		next := strings.Index(out, label)
		if next < 0 {
			t.Fatalf("band %q is not on the sheet:\n%s", label, out)
		}
		if next < at {
			t.Errorf("band %q is out of order on the sheet", label)
		}
		at = next
	}
}

// TestJDESweepB_MultiPickerTogglesWithEnter: the always-live filter owns space,
// so the certifications picker's "select" IS the toggle — and esc is DONE there,
// because every toggle was applied as it was made.
func TestJDESweepB_MultiPickerTogglesWithEnter(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")
	s.loading = false
	s.certs = []omsapi.CertificationOption{{ID: 1, Name: "Laser"}, {ID: 2, Name: "CNC mill"}}
	s.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})
	s.setCursorToField(afRequiredCerts)

	bar := jdeBarLine(s.View())
	if !strings.Contains(bar, "Ctrl-E=Choose") {
		t.Errorf("the certifications row should offer Ctrl-E=Choose: %q", bar)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != assetPhasePick {
		t.Fatalf("ctrl+e should open the multi picker, phase=%v", s.phase)
	}
	bar = jdeBarLine(s.View())
	for _, want := range []string{"Enter=Toggle", "Esc=Done"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the multi-picker bar should offer %q: %q", want, bar)
		}
	}

	// Enter toggles the highlighted certification rather than closing.
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !s.certSelected(1) || s.phase != assetPhasePick {
		t.Fatalf("enter should toggle and stay open (certs=%v, phase=%v)", s.certIDs, s.phase)
	}
	if out := s.View(); !strings.Contains(out, "[x] Laser") {
		t.Errorf("a chosen certification should be marked:\n%s", out)
	}
	// Space is filter text here, not a toggle.
	s.Update(spaceKey())
	if s.pickSearch.Value() != " " {
		t.Errorf("space should go into the always-live filter, got %q", s.pickSearch.Value())
	}
	// Esc is done, and what was toggled survives.
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != assetPhaseForm || !s.certSelected(1) {
		t.Errorf("esc should close and keep the toggles (phase=%v, certs=%v)", s.phase, s.certIDs)
	}
}

// TestJDESweepB_SublistAddIsARowNotALetter: the PM item's sub-lists took sweep
// A's chain-editor fold — `a` became a trailing "(add …)" row you navigate to,
// and Enter/Esc are both done because nothing is written until the item is.
func TestJDESweepB_SublistAddIsARowNotALetter(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})
	s.setCursorToField(mfTasks)
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "Ctrl-E=Manage") {
		t.Errorf("a sub-list row should offer Ctrl-E=Manage: %q", bar)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != mFormPhaseTaskList {
		t.Fatalf("ctrl+e should open the task list, phase=%v", s.phase)
	}

	out := s.View()
	if !strings.Contains(out, "(add a task step)") {
		t.Fatalf("the list should carry an add ROW:\n%s", out)
	}
	bar := jdeBarLine(out)
	for _, want := range []string{"Enter=Done", "Esc=Done", "Ctrl-E=Add a step"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the empty list's bar should offer %q: %q", want, bar)
		}
	}
	// AND NOT UP/DN. An empty sub-list has exactly ONE navigable row — the add
	// row the assertion above just found — so there is nowhere for the pair to
	// go: jdeClampPick hands the cursor back, no note is written, and the pane
	// redraws byte for byte. This check used to require the pair here, which is
	// the claim jdeRowMoves retired; it asserts the absence now rather than
	// dropping the token, because "the bar stopped saying it" and "the bar was
	// never asked" are different states.
	if strings.Contains(bar, "UP/DN") {
		t.Errorf("the empty list's bar names a movement pair with one navigable row "+
			"(the add row) and nothing for it to move to: %q", bar)
	}
	// `a` does nothing now; Ctrl-E on the add row is what opens the editor.
	s.Update(runeKey('a'))
	if s.phase != mFormPhaseTaskList {
		t.Errorf("`a` must not open the editor any more, phase=%v", s.phase)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != mFormPhaseTaskEdit || s.editIndex != -1 {
		t.Fatalf("ctrl+e on the add row should open a NEW step (phase=%v, index=%d)", s.phase, s.editIndex)
	}
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "Enter=Save step") {
		t.Errorf("the row editor's bar should offer Enter=Save step: %q", bar)
	}
}

// TestJDESweepB_SublistRemoveLivesInTheRowEditor: `d`/`x` are gone from the
// lists; removing is a row of the item's OWN editor, which is the only place the
// thing being dropped is on screen — and a row being ADDED has no such row.
func TestJDESweepB_SublistRemoveLivesInTheRowEditor(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})
	s.tools = []toolRow{{id: "tl-1", name: "Torque wrench", quantity: 2}}
	s.phase = mFormPhaseToolList
	s.rowCursor = 0

	// The list itself has no remove key left.
	for _, r := range []rune{'d', 'x'} {
		s.Update(runeKey(r))
	}
	if len(s.tools) != 1 {
		t.Fatalf("no letter should remove from the list, tools=%+v", s.tools)
	}

	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != mFormPhaseToolEdit || s.editIndex != 0 {
		t.Fatalf("ctrl+e on a row should edit it (phase=%v, index=%d)", s.phase, s.editIndex)
	}
	if got := s.toolEditRows(); got != toolEditFieldCount {
		t.Fatalf("an existing row's editor should carry the remove row, rows=%d", got)
	}
	s.editCursor = toolEditRemove
	out := s.View()
	if !strings.Contains(out, "Remove this tool") {
		t.Fatalf("the editor should draw the remove row:\n%s", out)
	}
	if bar := jdeBarLine(out); !strings.Contains(bar, "Ctrl-E=Remove") {
		t.Errorf("the remove row should offer Ctrl-E=Remove: %q", bar)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if len(s.tools) != 0 || s.phase != mFormPhaseToolList {
		t.Fatalf("ctrl+e on the remove row should drop it and return (tools=%+v, phase=%v)", s.tools, s.phase)
	}

	// A row being ADDED has nothing to remove, so it has no remove row and its
	// bar never offers the key.
	s.openToolEditor(-1)
	if got := s.toolEditRows(); got != toolEditFieldCount-1 {
		t.Errorf("a new row's editor should have no remove row, rows=%d", got)
	}
	s.editCursor = toolEditNotes
	if bar := jdeBarLine(s.View()); strings.Contains(bar, "Ctrl-E") {
		t.Errorf("a new row has nothing to remove: %q", bar)
	}
}

// TestJDESweepB_DeviceTypeFixedCodeIsDrawnButNotNavigable: in edit mode the code
// keeps its place in the sheet (the web's disabled input) but is not a row —
// nothing navigates to it, and the payload still omits it.
func TestJDESweepB_DeviceTypeFixedCodeIsDrawnButNotNavigable(t *testing.T) {
	s := NewDeviceTypeFormScreen(Deps{}, 7)
	s.loading = false
	s.dt = &forgekeyapi.DeviceType{ID: float64(7), Name: "Relay", Code: "power_relay", IsActive: true}
	s.hydrate()
	s.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})

	out := s.View()
	if !strings.Contains(out, "AC Relay") || !strings.Contains(out, "fixed after creation") {
		t.Fatalf("the fixed code row should be drawn:\n%s", out)
	}
	if fieldsContain(s.fields, dtCode) {
		t.Errorf("the fixed code must not be navigable")
	}
	// It shares the sheet's label column rather than hanging off its own — the
	// columnar test above only sees the rows that are navigable.
	col := -1
	for _, line := range strings.Split(out, "\n") {
		at := strings.Index(line, jdeLeader)
		if at < 0 {
			continue
		}
		if col < 0 {
			col = at
		} else if at != col {
			t.Errorf("the fixed row breaks the shared column: %q", line)
		}
	}

	// Cycling has nothing to act on here, so the bar must not offer it.
	s.cursor = 0
	if bar := jdeBarLine(s.View()); strings.Contains(bar, "←→") {
		t.Errorf("a text row must not offer ←→: %q", bar)
	}
}

// TestJDESweepB_OptionStripKeepsTheSelectionVisible is the jde_form.go extension
// sweep B needed: a device type cycles nineteen codes, and a strip clipped at the
// tail would cut the bracketed entry off entirely once the cursor moved past the
// first few — which is the one thing the strip exists to show.
func TestJDESweepB_OptionStripKeepsTheSelectionVisible(t *testing.T) {
	labels := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf"}
	for idx := range labels {
		strip := jdeOptionStrip(labels, idx, 30)
		want := "[" + labels[idx] + "]"
		if !strings.Contains(strip, want) {
			t.Errorf("idx %d: the selection %q is not in the strip %q", idx, want, strip)
		}
		if got := len([]rune(strip)); got > 30 {
			t.Errorf("idx %d: strip is %d wide, want ≤30: %q", idx, got, strip)
		}
	}
	// An unconstrained strip is still the whole set, in order.
	if got := jdeOptionStrip(labels, 0, 0); !strings.HasPrefix(got, "[alpha]") || !strings.HasSuffix(got, "golf") {
		t.Errorf("an unwindowed strip should list everything: %q", got)
	}
	// The dropped ends are admitted rather than silently cut.
	if got := jdeOptionStrip(labels, 3, 30); !strings.HasPrefix(got, "… ") || !strings.HasSuffix(got, " …") {
		t.Errorf("a windowed strip should mark both dropped ends: %q", got)
	}
	// Two values still get nothing — "< Yes >" already says what the other is.
	if got := jdeOptionStrip([]string{"Yes", "No"}, 0, 40); got != "" {
		t.Errorf("a two-value set needs no strip, got %q", got)
	}
}

// TestJDESweepB_PagingClamps: a page is for covering ground in a sheet taller
// than the pane; one that wrapped would lose the operator's place.
func TestJDESweepB_PagingClamps(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
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
