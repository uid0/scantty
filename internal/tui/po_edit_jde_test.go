// The PO-edit screen as the pilot of the columnar "JD Edwards" redesign
// (sc-h412): the look, the persistent action bar, and — the part that is easy
// to get wrong — the REDUCED key scheme. The bar is now the only place the
// operator can learn what a key does, so these tests hold it to naming exactly
// the keys that work where the cursor is standing, and hold the screen to
// binding nothing else.
package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// poJDEScreen is a sized edit screen: the columnar layer only windows and pins
// the bar once it knows how big the pane is.
func poJDEScreen(t *testing.T, height int) *PurchaseOrderEditScreen {
	t.Helper()
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
	s.Update(tea.WindowSizeMsg{Width: 120, Height: height})
	return s
}

func poJDEBarLine(view string) string {
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	return lines[len(lines)-1]
}

func TestPOEditJDE_FormIsColumnar(t *testing.T) {
	s := poJDEScreen(t, 40)
	out := s.viewForm()

	// Every field row hangs off the same leader column, headings and the grid
	// aside — that is what makes the block read as one sheet.
	col := -1
	rows := 0
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
	if want := poEditMetaCount + poEditAssocCount; rows != want {
		t.Errorf("%d columnar rows rendered, want %d (the header band plus both associations)", rows, want)
	}
	// The three terms are choice rows, drawn as "< value >" rather than typed.
	for _, want := range []string{"Priority" + jdeLeader + "< Normal >", "< — not agreed — >"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing choice row %q:\n%s", want, out)
		}
	}
	// The lines are a detail grid with a column header, not a bullet list.
	if !strings.Contains(out, "Qty") || !strings.Contains(out, "Ship date") {
		t.Errorf("the line band should be a columnar grid:\n%s", out)
	}
}

// TestPOEditJDE_BarIsPinnedToTheBottomOfThePane: "persistent" means the bar is
// on the same two rows on every frame — if the body were allowed to set the
// height, the bar would walk up and down as fields gained and lost their option
// strips.
func TestPOEditJDE_BarIsPinnedToTheBottomOfThePane(t *testing.T) {
	s := poJDEScreen(t, 40)
	for _, cursor := range []int{0, poMetaPriority, poEditMetaCount, poEditLineBase} {
		s.cursor = cursor
		s.syncFocus()
		lines := strings.Split(s.View(), "\n")
		if want := screenBodyHeight(40); len(lines) != want {
			t.Fatalf("cursor %d: view is %d rows, want the pane's budget of %d", cursor, len(lines), want)
		}
		rule := lines[len(lines)-2]
		if strings.Trim(rule, "-") != "" || rule == "" {
			t.Errorf("cursor %d: the second-to-last row should be the bar's rule, got %q", cursor, rule)
		}
		if !strings.Contains(lines[len(lines)-1], "Enter=Save") {
			t.Errorf("cursor %d: the last row should be the key line, got %q", cursor, lines[len(lines)-1])
		}
	}
}

// TestPOEditJDE_BarNamesOnlyTheKeysThatApply is the contract that replaces the
// letter accelerators: a key on the bar works here, and a key that works here
// is on the bar.
func TestPOEditJDE_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	s := poJDEScreen(t, 40)
	always := []string{"Enter=Save", "Esc=Exit", "UP/DN=Fields"}

	for _, tc := range []struct {
		name       string
		cursor     int
		want, deny []string
	}{
		{"text row", poMetaSupplierOrder, nil, []string{"Ctrl-E", "←→"}},
		{"choice row", poMetaPriority, []string{"←→=Change"}, []string{"Ctrl-E"}},
		{"association row", poEditMetaCount + poAssocRowWorkOrder, []string{"Ctrl-E=Pick"}, []string{"←→"}},
		{"line row", poEditLineBase, []string{"Ctrl-E=Edit line"}, []string{"←→"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.cursor = tc.cursor
			s.syncFocus()
			bar := poJDEBarLine(s.viewForm())
			for _, want := range append(always, tc.want...) {
				if !strings.Contains(bar, want) {
					t.Errorf("bar should offer %q: %q", want, bar)
				}
			}
			for _, deny := range tc.deny {
				if strings.Contains(bar, deny) {
					t.Errorf("bar should not offer %q here: %q", deny, bar)
				}
			}
		})
	}

	// Paging is only advertised when there is something off-screen to page to.
	if bar := poJDEBarLine(s.viewForm()); strings.Contains(bar, "PgUp") {
		t.Errorf("a body that fits needs no paging key: %q", bar)
	}
	short := poJDEScreen(t, 18)
	if bar := poJDEBarLine(short.viewForm()); !strings.Contains(bar, "PgUp/PgDn=Page") {
		t.Errorf("a windowed body should advertise paging: %q", bar)
	}
}

func TestPOEditJDE_FieldNavMovesAndWraps(t *testing.T) {
	s := poJDEScreen(t, 40)

	for _, key := range []tea.KeyMsg{{Type: tea.KeyDown}, {Type: tea.KeyTab}} {
		s.cursor = 0
		s.syncFocus()
		s.updateForm(key)
		if s.cursor != 1 {
			t.Errorf("%v should move to the next field, got %d", key, s.cursor)
		}
	}
	for _, key := range []tea.KeyMsg{{Type: tea.KeyUp}, {Type: tea.KeyShiftTab}} {
		s.cursor = 1
		s.syncFocus()
		s.updateForm(key)
		if s.cursor != 0 {
			t.Errorf("%v should move to the previous field, got %d", key, s.cursor)
		}
	}
	// Off the end and back round: the bands are one cursor, so the last line
	// row leads to the first header field.
	s.cursor = s.rowCount() - 1
	s.updateForm(tea.KeyMsg{Type: tea.KeyDown})
	if s.cursor != 0 {
		t.Errorf("nav should wrap to the top, got %d", s.cursor)
	}
	s.updateForm(tea.KeyMsg{Type: tea.KeyUp})
	if s.cursor != s.rowCount()-1 {
		t.Errorf("nav should wrap to the bottom, got %d", s.cursor)
	}
	// Only a text row owns an input; a choice or a picker row has nothing to
	// type into, so nothing is focused there.
	s.cursor = poMetaPriority
	s.syncFocus()
	for i := range s.meta {
		if s.meta[i].Focused() {
			t.Errorf("row %d should not be focused while the cursor is on a choice row", i)
		}
	}
	s.cursor = poMetaNotes
	s.syncFocus()
	if !s.meta[poMetaNotes].Focused() {
		t.Error("a text row should focus its input")
	}
}

// TestPOEditJDE_PagingClampsInsteadOfWrapping: paging covers ground in a body
// taller than the pane, so overshooting has to stop at the end — a page that
// wrapped would lose the operator's place rather than save them keystrokes.
func TestPOEditJDE_PagingClampsInsteadOfWrapping(t *testing.T) {
	s := poJDEScreen(t, 18)
	if step := s.pageStep(); step < 1 || step >= s.rowCount() {
		t.Fatalf("a page is %d rows of %d — it should be a screenful, not everything", step, s.rowCount())
	}

	s.cursor = 0
	s.updateForm(tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursor == 0 {
		t.Fatal("pgdn should move the cursor")
	}
	for i := 0; i < 10; i++ {
		s.updateForm(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if s.cursor != s.rowCount()-1 {
		t.Errorf("pgdn should clamp to the last row, got %d of %d", s.cursor, s.rowCount())
	}
	for i := 0; i < 10; i++ {
		s.updateForm(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if s.cursor != 0 {
		t.Errorf("pgup should clamp to the first row, got %d", s.cursor)
	}
}

// TestPOEditJDE_EnterSavesTheRecordFromAnyRow: SUBMIT means the same thing
// everywhere on the form, so an operator never has to navigate back to a text
// field to save what they typed.
func TestPOEditJDE_EnterSavesTheRecordFromAnyRow(t *testing.T) {
	for _, cursor := range []int{poMetaSupplierOrder, poMetaFreightTerms, poEditMetaCount + poAssocRowCommittee, poEditLineBase} {
		s, body := poTermsEditScreen(t, poTermsPO())
		s.cursor = cursor
		s.syncFocus()
		s.meta[poMetaNotes].SetValue("saved from row " + strings.TrimSpace(poMetaLabels[poMetaNotes]))

		_, cmd := s.updateForm(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatalf("cursor %d: enter should save the order header", cursor)
		}
		if msg, ok := cmd().(poEditSavedMsg); !ok || msg.err != nil {
			t.Fatalf("cursor %d: save failed: %#v", cursor, msg)
		}
		if got, _ := (*body)["notes"].(string); !strings.HasPrefix(got, "saved from row") {
			t.Errorf("cursor %d: the header PATCH did not carry the form: %v", cursor, *body)
		}
	}
}

func TestPOEditJDE_EscLeavesWithoutWriting(t *testing.T) {
	s, body := poTermsEditScreen(t, poTermsPO())
	s.meta[poMetaNotes].SetValue("not saved")
	next, cmd := s.updateForm(tea.KeyMsg{Type: tea.KeyEsc})
	if next != Screen(s) {
		t.Errorf("esc should hand back to the router, got %T", next)
	}
	if cmd == nil {
		t.Fatal("esc should navigate back to the detail screen")
	}
	if len(*body) != 0 {
		t.Errorf("esc must write nothing: %v", *body)
	}
}

// TestPOEditJDE_NoLetterAcceleratorsRemain is the guard for the whole point of
// the redesign: the keys that used to hide on the line rows (w / c / v) and the
// vim nav in the picker are gone, and a stray letter on a row with no input
// must do NOTHING rather than fire an action the operator cannot see advertised.
func TestPOEditJDE_NoLetterAcceleratorsRemain(t *testing.T) {
	s := poJDEScreen(t, 40)

	for _, band := range []struct {
		name   string
		cursor int
	}{
		{"line row", poEditLineBase},
		{"association row", poEditMetaCount + poAssocRowWorkOrder},
	} {
		for _, letter := range []rune{'w', 'c', 'v', 'e', 'j', 'k', 'g', 'x', 'n'} {
			s.cursor = band.cursor
			s.syncFocus()
			s.updateForm(runeKey(letter))
			if s.phase != poEditPhaseForm {
				t.Fatalf("%s: %q opened phase %v — no letter is bound on this screen", band.name, letter, s.phase)
			}
			if s.cursor != band.cursor {
				t.Errorf("%s: %q moved the cursor to %d", band.name, letter, s.cursor)
			}
		}
	}

	// A letter on a text row is text, not a command.
	s.cursor = poMetaSupplierOrder
	s.syncFocus()
	before := s.meta[poMetaSupplierOrder].Value()
	s.updateForm(runeKey('w'))
	if s.meta[poMetaSupplierOrder].Value() != before+"w" {
		t.Errorf("a letter on a text row should be typed into it: %q", s.meta[poMetaSupplierOrder].Value())
	}
}

// TestPOEditJDE_CtrlEOpensWhateverTheRowIs: EDIT is the one key that changes
// meaning with the cursor, which is why the bar re-labels it on every row.
func TestPOEditJDE_CtrlEOpensWhateverTheRowIs(t *testing.T) {
	s := poJDEScreen(t, 40)

	// A text row has nothing to open, and must not fall through to something.
	s.cursor = poMetaNotes
	s.syncFocus()
	if _, cmd := s.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE}); cmd != nil || s.phase != poEditPhaseForm {
		t.Errorf("ctrl+e on a text row should do nothing; phase=%v cmd=%v", s.phase, cmd != nil)
	}

	// A line row opens THAT line's editor.
	s.cursor = poEditLineBase + 1
	s.syncFocus()
	s.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseLine || s.editLineIdx != 1 {
		t.Fatalf("ctrl+e on line 2 should open its editor; phase=%v idx=%d", s.phase, s.editLineIdx)
	}
	if got := s.lineInputs[poLineEditCost].Value(); got != "20.00" {
		t.Errorf("the editor should be prefilled from that line, cost = %q", got)
	}
}

// TestPOEditJDE_LineEditorCarriesTheOldLineKeys: w / c / v did not disappear —
// each became a row of the line editor, opened with the same Ctrl-E.
func TestPOEditJDE_LineEditorCarriesTheOldLineKeys(t *testing.T) {
	s := poJDEScreen(t, 40)
	s.openLineEditor(0)

	// Nav walks all six rows, and only the first three take typing.
	seen := map[int]bool{}
	for i := 0; i < poLineEditCount; i++ {
		seen[s.lineFocus] = true
		if row, ok := s.lineInputRow(); ok != (s.lineFocus < poLineEditInputCount) {
			t.Errorf("row %d: input=%v (row %d)", s.lineFocus, ok, row)
		}
		s.updateLineEdit(tea.KeyMsg{Type: tea.KeyDown})
	}
	if len(seen) != poLineEditCount {
		t.Errorf("nav reached %d of %d line rows", len(seen), poLineEditCount)
	}

	// The status row opens the void prompt.
	s.lineFocus = poLineRowStatus
	s.syncLineFocus()
	s.updateLineEdit(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseVoidLine {
		t.Fatalf("ctrl+e on the status row should open the void prompt, phase=%v", s.phase)
	}
	// …and esc from it lands back in the line editor, not on the header form.
	s.updateVoidLine(tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != poEditPhaseLine {
		t.Errorf("esc from the void prompt should return to the line editor, phase=%v", s.phase)
	}

	// A line already voided says so rather than opening a second prompt.
	s.openLineEditor(1)
	s.lineFocus = poLineRowStatus
	s.syncLineFocus()
	if cmd := s.openLineRow(); cmd == nil {
		t.Error("voiding an already-voided line should report why")
	}
	if s.phase != poEditPhaseLine {
		t.Errorf("an already-voided line must not reopen the prompt, phase=%v", s.phase)
	}
	if out := s.viewLineEdit(); !strings.Contains(out, "voided") {
		t.Errorf("the status row should show the line is voided:\n%s", out)
	}
	if bar := poJDEBarLine(s.viewLineEdit()); strings.Contains(bar, "Void line") {
		t.Errorf("the bar should not offer a void that would be refused: %q", bar)
	}
}

// TestPOEditJDE_PickerKeepsItsSelectionOnScreen: the picker is windowed twice —
// once by the list around its own selection, once by the frame around the pane
// — and the frame has to key off the line the list actually marked, not off the
// option index, or a long list scrolls the selection out of view.
func TestPOEditJDE_PickerKeepsItsSelectionOnScreen(t *testing.T) {
	s := poJDEScreen(t, 14)
	s.assocField = poAssocFieldCommittee
	s.assocLineIdx = poAssocLineOrder
	s.assocRows = []poAssocOption{{value: "", label: "— no committee —"}}
	for i := 1; i <= 30; i++ {
		s.assocRows = append(s.assocRows, poAssocOption{value: strconv.Itoa(i), label: "Committee " + strconv.Itoa(i)})
	}
	s.phase = poEditPhaseAssoc

	for _, cursor := range []int{0, 15, 30} {
		s.assocCursor = cursor
		out := s.viewAssocPick()
		if !strings.Contains(out, s.assocRows[cursor].label) {
			t.Errorf("cursor %d: the selected row scrolled off the pane:\n%s", cursor, out)
		}
		if lines := strings.Split(out, "\n"); len(lines) != screenBodyHeight(14) {
			t.Errorf("cursor %d: picker is %d rows, want the pane's budget of %d", cursor, len(lines), screenBodyHeight(14))
		}
	}
}

// TestPOEditJDE_ChoiceRowsCycleInPlace: a bounded set stays on its row rather
// than opening a sub-phase, so ←/→ (and space) are the only keys it needs.
func TestPOEditJDE_ChoiceRowsCycleInPlace(t *testing.T) {
	s := poJDEScreen(t, 40)
	s.cursor = poMetaPriority
	s.syncFocus()

	start := s.selects[poMetaPriority].picked()
	s.updateForm(tea.KeyMsg{Type: tea.KeyRight})
	forward := s.selects[poMetaPriority].picked()
	if forward == start {
		t.Fatal("→ should advance the choice")
	}
	s.updateForm(tea.KeyMsg{Type: tea.KeyLeft})
	if got := s.selects[poMetaPriority].picked(); got != start {
		t.Errorf("← should walk back, got %q want %q", got, start)
	}
	if s.phase != poEditPhaseForm {
		t.Errorf("a choice row must not open a sub-phase, phase=%v", s.phase)
	}
	// The whole set is drawn under the focused row, so it is never cycled blind.
	if out := s.viewForm(); !strings.Contains(out, "Low") || !strings.Contains(out, "Urgent") {
		t.Errorf("the focused choice row should show its option strip:\n%s", out)
	}
}

// TestPOEditJDE_CostRowNamesItsBasis: the cost field is a total for the quantity
// ORDERED — that is what update_item divides by — while the grid's Cost column
// counts what has arrived. On a partly delivered line the two are different
// numbers, so the row says which one it is and the sub-heading names the other.
// A columnar form has no room to explain itself twice, so both live where the
// convention already puts that kind of note: the hint after the input area, and
// the muted line under the title.
func TestPOEditJDE_CostRowNamesItsBasis(t *testing.T) {
	s := poJDEScreen(t, 40)
	s.openLineEditor(1) // Gadget: ordered 2, received 2, $20.00 spent
	out := s.viewLineEdit()

	if !strings.Contains(out, "Total line cost"+jdeLeader) {
		t.Errorf("the cost row should hang off the shared leader column:\n%s", out)
	}
	for _, want := range []string{
		"$ total for all 2 ordered",
		"ordered 2 · received 2 · $20.00 spent so far",
		"A cost you do not change is not re-sent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("line editor missing %q:\n%s", want, out)
		}
	}
}

// TestPOEditJDE_CostRowOffersNoKeyWithNothingBehindIt: the bar is the only place
// the operator learns a key, so Ctrl-E is named on the cost row only when there
// is a price to offer. A line that already carries one has nothing to offer.
func TestPOEditJDE_CostRowOffersNoKeyWithNothingBehindIt(t *testing.T) {
	s := poJDEScreen(t, 40)
	s.openLineEditor(0) // Widget carries a $50.00 estimate
	if bar := poJDEBarLine(s.viewLineEdit()); strings.Contains(bar, "Ctrl-E") {
		t.Errorf("a priced line has nothing for Ctrl-E on the cost row: %q", bar)
	}
	// The three rows below the inputs still carry theirs.
	s.lineFocus = poLineRowWorkOrder
	if bar := poJDEBarLine(s.viewLineEdit()); !strings.Contains(bar, "Ctrl-E=Pick") {
		t.Errorf("the work-order row should still name Ctrl-E: %q", bar)
	}
}
