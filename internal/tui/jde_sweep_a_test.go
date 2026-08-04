// The inventory family of forms on the columnar "JD Edwards" layer (sc-dnhx,
// sweep A of the sc-h412 redesign): the item form, category, supplier, the
// item↔supplier link and asset parts.
//
// These hold all five to the same contract the pilot's own tests hold PO edit
// to, because the persistent action bar is now the only place an operator can
// learn what a key does:
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

	"github.com/uid0/scantty/internal/omsapi"
)

// jdeRowKind is what the cursor is standing on, which is what decides the bar.
type jdeRowKind int

const (
	jdeRowText jdeRowKind = iota
	jdeRowChoice
	jdeRowPicker
)

// jdeSweepCase is one converted form, loaded and ready to drive.
type jdeSweepCase struct {
	name string
	// screen is the form under test; setCursor puts it on a row, and kinds says
	// what kind each of those rows is.
	screen    Screen
	setCursor func(row int)
	rowCount  int
	kinds     map[jdeRowKind]int // kind -> a row of that kind (absent = none)
	// pickerVerb is what the bar calls Ctrl-E on the picker row.
	pickerVerb string
}

const jdeSweepHeight = 34

func jdeSweepCases(t *testing.T) []jdeSweepCase {
	t.Helper()
	size := tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight}

	item := NewInventoryItemFormScreen(Deps{}, "")
	item.loading = false
	item.categories = []omsapi.Category{{ID: 1, Name: "Paper"}}
	item.locations = []omsapi.Location{{ID: 2, Name: "Bay 4"}}
	item.Update(size)

	cat := NewCategoryFormScreen(Deps{}, "")
	cat.loading = false
	cat.categories = []omsapi.Category{{ID: 1, Name: "Hardware"}}
	cat.Update(size)

	sup := NewSupplierFormScreen(Deps{}, "")
	sup.Update(size)

	link := NewItemSupplierFormScreen(Deps{}, "itm-1", "Copy paper", nil)
	link.loading = false
	link.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}, {ID: 5, Name: "Beta"}}
	link.Update(size)

	part := NewAssetPartFormScreen(Deps{}, "asset-9", "Haas Mill", "")
	part.loading = false
	part.items = []omsapi.Item{{ID: "i1", Name: "Coolant filter", SKU: "CF-1"}}
	part.Update(size)

	return []jdeSweepCase{
		{
			name:      "inventory item",
			screen:    item,
			setCursor: func(row int) { item.cursor = row; item.syncFocus() },
			rowCount:  len(item.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   itemRowOf(t, item, fName),
				jdeRowChoice: itemRowOf(t, item, fCountMode),
				jdeRowPicker: itemRowOf(t, item, fCategory),
			},
			pickerVerb: "Pick",
		},
		{
			name:      "category",
			screen:    cat,
			setCursor: func(row int) { cat.cursor = row; cat.syncFocus() },
			rowCount:  len(cat.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   0, // cfName
				jdeRowPicker: 3, // cfParent
			},
			pickerVerb: "Pick",
		},
		{
			name:      "supplier",
			screen:    sup,
			setCursor: func(row int) { sup.cursor = row; sup.syncFocus() },
			rowCount:  len(sup.fields),
			kinds: map[jdeRowKind]int{
				jdeRowText:   0, // sfName
				jdeRowChoice: 1, // sfType
			},
		},
		{
			name:      "item supplier link",
			screen:    link,
			setCursor: func(row int) { link.cursor = row; link.syncFocus() },
			rowCount:  len(link.fields),
			kinds: map[jdeRowKind]int{
				jdeRowPicker: 0, // isSupplier
				jdeRowText:   1, // isSKU
				jdeRowChoice: 7, // isPrimary
			},
			pickerVerb: "Pick",
		},
		{
			name:      "asset part",
			screen:    part,
			setCursor: func(row int) { part.cursor = row; part.syncFocus() },
			rowCount:  len(part.fields),
			kinds: map[jdeRowKind]int{
				jdeRowPicker: 0, // apfPart
				jdeRowText:   1, // apfQuantity
				jdeRowChoice: 2, // apfRequired
			},
			pickerVerb: "Pick",
		},
	}
}

// itemRowOf finds the item form's cursor position for a field id — its visible
// rows are conditional, so the positions are not constants.
func itemRowOf(t *testing.T, s *InventoryItemFormScreen, id int) int {
	t.Helper()
	for i, fid := range s.fields {
		if fid == id {
			return i
		}
	}
	t.Fatalf("field %d is not visible on the item form", id)
	return 0
}

func jdeBarLine(view string) string {
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	return lines[len(lines)-1]
}

// TestJDESweepA_FormsAreColumnar: every field row hangs off the same leader
// column — that is what makes a block of fields read as one sheet rather than a
// ragged list.
func TestJDESweepA_FormsAreColumnar(t *testing.T) {
	for _, tc := range jdeSweepCases(t) {
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
			// And the labels are RIGHT-aligned into that column, which is what
			// makes the leader read as one edge rather than a ragged margin: a
			// label shorter than the widest one is padded on its LEFT.
			short, padded := "", false
			for _, line := range strings.Split(out, "\n") {
				at := strings.Index(line, jdeLeader)
				if at < 0 || at != col {
					continue
				}
				label := strings.TrimSuffix(line[:at], " ")
				if len(label) > len(jdeIndent) && strings.HasPrefix(label, jdeIndent+" ") {
					padded, short = true, label
				}
			}
			if !padded {
				t.Errorf("no label is left-padded — are they right-aligned? (%q)\n%s", short, out)
			}
		})
	}
}

// TestJDESweepA_ItemFormHasBandHeadings: twenty-eight fields in one undivided
// run is a wall. The sheet breaks into the sections a printed form would have,
// and each heading is drawn where its band starts.
func TestJDESweepA_ItemFormHasBandHeadings(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 90})
	s.setCursorToField(fIsHazardous)
	s.flipToggle(fIsHazardous)
	s.setCursorToField(fIsSerialized)
	s.flipToggle(fIsSerialized)

	out := s.View()
	at := -1
	for _, band := range []itemFieldBand{
		itemBandDetails, itemBandStock, itemBandPackaging,
		itemBandPlacement, itemBandHazard, itemBandSerial, itemBandStatus,
	} {
		label := itemBandLabel[band]
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

// TestJDESweepA_BarIsPinnedToTheBottomOfThePane: "persistent" means the bar is on
// the same two rows on every frame — if the body were allowed to set the height,
// the bar would walk up and down as fields gained and lost their option strips.
func TestJDESweepA_BarIsPinnedToTheBottomOfThePane(t *testing.T) {
	for _, tc := range jdeSweepCases(t) {
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
					t.Errorf("row %d: the last row should be the key line, got %q", row, lines[len(lines)-1])
				}
			}
		})
	}
}

// TestJDESweepA_BarNamesOnlyTheKeysThatApply is the contract that replaces the
// letter accelerators: a key on the bar works here, and a key that works here is
// on the bar.
func TestJDESweepA_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	always := []string{"Enter=Save", "Esc=Cancel", "UP/DN=Fields"}
	for _, tc := range jdeSweepCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			for kind, row := range tc.kinds {
				tc.setCursor(row)
				bar := jdeBarLine(tc.screen.View())
				for _, want := range always {
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

// TestJDESweepA_NoLetterAcceleratorsRemain is the guard for the whole point of
// the redesign: the space-opens-the-picker and the pickers' j/k are gone, and a
// stray letter on a row with no input must do NOTHING rather than fire an action
// the operator cannot see advertised.
func TestJDESweepA_NoLetterAcceleratorsRemain(t *testing.T) {
	letters := []rune{'a', 'c', 'd', 'e', 'g', 'j', 'k', 'n', 'v', 'w', 'x', 'J', 'K'}
	for _, tc := range jdeSweepCases(t) {
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

// TestJDESweepA_LettersOnATextRowAreTyped is the other half: the reduced scheme
// took the accelerators away, not the ability to type.
func TestJDESweepA_LettersOnATextRowAreTyped(t *testing.T) {
	for _, tc := range jdeSweepCases(t) {
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

// TestJDESweepA_CtrlEOpensThePicker: EDIT opens whatever the highlighted row IS,
// and a text row has nothing to open — it must not fall through to something.
func TestJDESweepA_CtrlEOpensThePicker(t *testing.T) {
	for _, tc := range jdeSweepCases(t) {
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

// TestJDESweepA_PickerFiltersAsYouType: the filter is always live, so there is no
// mode to enter — which is what let j/k/"/" leave. Esc returns to the form
// without choosing.
func TestJDESweepA_PickerFiltersAsYouType(t *testing.T) {
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Copy paper", nil)
	s.loading = false
	s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}, {ID: 5, Name: "Beta"}}
	s.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})
	s.cursor = 0
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != isPhaseSupplierPick {
		t.Fatalf("ctrl+e should open the picker, phase=%v", s.phase)
	}

	// Typing narrows immediately.
	for _, r := range "bet" {
		s.Update(runeKey(r))
	}
	if len(s.pickOptions) != 1 || s.pickOptions[0].id != 5 {
		t.Fatalf("typing should filter, got %+v", s.pickOptions)
	}
	if out := s.View(); !strings.Contains(out, "bet") {
		t.Errorf("the pinned filter row should show what was typed:\n%s", out)
	}

	// j and k are LETTERS again: they go into the filter like any other, rather
	// than moving the selection behind the operator's back.
	at := s.pickCursor
	for _, r := range "jk" {
		s.Update(runeKey(r))
	}
	if got := s.pickSearch.Value(); got != "betjk" {
		t.Errorf("j/k should be typed into the filter, got %q", got)
	}
	if s.pickCursor != at {
		t.Errorf("j/k must not move the selection, cursor %d → %d", at, s.pickCursor)
	}

	// Esc chooses nothing and lands back on the form.
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != isPhaseForm {
		t.Errorf("esc should return to the form, phase=%v", s.phase)
	}
	if s.supplierID != nil {
		t.Errorf("esc must not choose anything, got %v", s.supplierID)
	}
	if s.pickSearch.Value() != "" {
		t.Errorf("the filter should not survive the picker, got %q", s.pickSearch.Value())
	}
}

// TestJDESweepA_PickerCursorClampsAndStaysOnScreen: a picker list is a set of
// choices, not a ring — running off the bottom must not reappear at the "(none)"
// row that clears the field. And the frame has to keep the selection visible,
// however long the list is.
func TestJDESweepA_PickerCursorClampsAndStaysOnScreen(t *testing.T) {
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Haas Mill", "")
	s.loading = false
	for i := 1; i <= 40; i++ {
		s.items = append(s.items, omsapi.Item{ID: "i" + strings.Repeat("x", i), Name: "Part " + strings.Repeat("x", i)})
	}
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
	s.cursor = 0
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})

	for i := 0; i < 100; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if s.pickCursor != len(s.pickOptions)-1 {
		t.Errorf("down should clamp at the last option, got %d of %d", s.pickCursor, len(s.pickOptions))
	}
	for i := 0; i < 100; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if s.pickCursor != 0 {
		t.Errorf("up should clamp at the first option, got %d", s.pickCursor)
	}

	for _, cursor := range []int{0, 20, len(s.pickOptions) - 1} {
		s.pickCursor = cursor
		out := s.View()
		if !strings.Contains(out, s.pickOptions[cursor].label) {
			t.Errorf("cursor %d: the selection scrolled off the pane:\n%s", cursor, out)
		}
		if lines := strings.Split(out, "\n"); len(lines) != screenBodyHeight(20) {
			t.Errorf("cursor %d: picker is %d rows, want the pane's budget of %d", cursor, len(lines), screenBodyHeight(20))
		}
	}
}

// TestJDESweepA_PagingClampsInsteadOfWrapping: paging covers ground in a body
// taller than the pane, so overshooting has to stop at the end — a page that
// wrapped would lose the operator's place rather than save them keystrokes. Only
// the item form is long enough to need it, and only it advertises the key.
func TestJDESweepA_PagingClampsInsteadOfWrapping(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 20})

	s.cursor = 0
	s.syncFocus()
	if bar := jdeBarLine(s.viewForm()); !strings.Contains(bar, "PgUp/PgDn=Page") {
		t.Errorf("a windowed body should advertise paging: %q", bar)
	}
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursor == 0 {
		t.Fatal("pgdn should move the cursor")
	}
	for i := 0; i < 20; i++ {
		s.updateFormPhase(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if s.cursor != len(s.fields)-1 {
		t.Errorf("pgdn should clamp to the last row, got %d of %d", s.cursor, len(s.fields))
	}
	for i := 0; i < 20; i++ {
		s.updateFormPhase(tea.KeyMsg{Type: tea.KeyPgUp})
	}
	if s.cursor != 0 {
		t.Errorf("pgup should clamp to the first row, got %d", s.cursor)
	}

	// A body that fits needs no paging key.
	short := NewSupplierFormScreen(Deps{}, "")
	short.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	if bar := jdeBarLine(short.View()); strings.Contains(bar, "PgUp") {
		t.Errorf("a body that fits needs no paging key: %q", bar)
	}
}

// TestJDESweepA_ChainEditorHasNoLetterKeysLeft: the packaging chain's a / e / x /
// d / J / K became rows and arrows. Each of those letters must now do nothing,
// and what they did must still be reachable.
func TestJDESweepA_ChainEditorHasNoLetterKeysLeft(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	s.packNextKey = 2
	s.openChain()
	s.chainCursor = 0

	before := s.View()
	for _, letter := range []rune{'a', 'e', 'x', 'd', 'j', 'k', 'J', 'K'} {
		s.Update(runeKey(letter))
		if s.phase != itemFormPhaseChain {
			t.Fatalf("%q opened phase %v — no letter is bound on the chain list", letter, s.phase)
		}
		if got := s.View(); got != before {
			t.Fatalf("%q changed the chain list:\n%s", letter, got)
		}
	}

	// The bar names what IS bound, and each of those works.
	bar := jdeBarLine(before)
	for _, want := range []string{"Enter=Done", "Esc=Done", "UP/DN=Levels", "Ctrl-E=Edit level", "←→=Move level"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the chain bar should offer %q: %q", want, bar)
		}
	}

	// → moves a level along the chain (which reads left to right).
	s.Update(tea.KeyMsg{Type: tea.KeyRight})
	if s.packRows[1].key != 1 {
		t.Errorf("→ should move the level inward, rows = %+v", s.packRows)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if s.packRows[0].key != 1 {
		t.Errorf("← should move it back, rows = %+v", s.packRows)
	}

	// Ctrl-E on a level opens its editor; on the trailing add row it adds one.
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseChainRow || s.chainRowEditing != 0 {
		t.Fatalf("ctrl+e should edit the highlighted level; phase=%v idx=%d", s.phase, s.chainRowEditing)
	}
	if got := s.chainRowName.Value(); got != "case" {
		t.Errorf("the editor should be prefilled from that level, name = %q", got)
	}
	s.Update(ccEscKey())

	for s.chainCursor < s.chainAddRow() {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "Ctrl-E=Add level") ||
		strings.Contains(bar, "←→") {
		t.Errorf("the add row adds and has nothing to move: %q", bar)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseChainRow || s.chainRowEditing != -1 {
		t.Fatalf("ctrl+e on the add row should open a NEW level; phase=%v idx=%d", s.phase, s.chainRowEditing)
	}
}

// TestJDESweepA_ChainRowEditorCarriesRemove: x did not disappear — it became a
// row of the level's own editor, which is where the thing being removed is on
// screen. A level being ADDED has no such row, because there is nothing to drop.
func TestJDESweepA_ChainRowEditorCarriesRemove(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
	s.packRows = []packagingRow{
		{key: 1, name: "case", baseUnits: 100},
		{key: 2, name: "bag", baseUnits: 1},
	}
	s.packNextKey = 2
	s.openChain()

	// Adding: two rows, no remove.
	s.openChainRow(-1)
	if got := s.chainRowCount(); got != chainRowFieldCount-1 {
		t.Errorf("an added level has %d rows, want %d", got, chainRowFieldCount-1)
	}
	if strings.Contains(s.View(), "Remove this level") {
		t.Errorf("there is nothing to remove while adding:\n%s", s.View())
	}

	// Editing: three, and the third removes it.
	s.openChainRow(0)
	seen := map[int]bool{}
	for i := 0; i < chainRowFieldCount; i++ {
		seen[s.chainRowFocus] = true
		s.updateChainRowPhase(tea.KeyMsg{Type: tea.KeyDown})
	}
	if len(seen) != chainRowFieldCount {
		t.Errorf("nav reached %d of %d editor rows", len(seen), chainRowFieldCount)
	}

	s.chainRowFocus = chainRowFieldRemove
	s.syncChainRowFocus()
	if bar := jdeBarLine(s.View()); !strings.Contains(bar, "Ctrl-E=Remove") {
		t.Errorf("the remove row should advertise the key: %q", bar)
	}
	s.updateChainRowPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != itemFormPhaseChain {
		t.Fatalf("removing should return to the list, phase=%v", s.phase)
	}
	if len(s.packRows) != 1 || s.packRows[0].name != "bag" {
		t.Errorf("the level should be gone, rows = %+v", s.packRows)
	}
	// And the bar on the two text rows does NOT offer a remove that isn't there.
	s.openChainRow(0)
	s.chainRowFocus = chainRowFieldName
	s.syncChainRowFocus()
	if bar := jdeBarLine(s.View()); strings.Contains(bar, "Ctrl-E") {
		t.Errorf("a text row of the editor opens nothing: %q", bar)
	}
}

// TestJDESweepA_EnterSavesFromAnyRow: SUBMIT means the same thing everywhere on a
// sheet, so an operator never has to navigate back to a text field to save what
// they typed. The link form is the sharp case — its supplier row used to swallow
// enter to open a picker.
func TestJDESweepA_EnterSavesFromAnyRow(t *testing.T) {
	for _, row := range []int{isSupplier, isSKU, isPrimary} {
		s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Copy paper", nil)
		s.loading = false
		s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}}
		id := 4
		s.supplierID = &id
		s.inputs[isSKU].SetValue("SKU-1")
		s.cursor = row
		s.syncFocus()

		if _, cmd := s.updateFormPhase(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
			t.Errorf("row %d: enter should submit the link", row)
		}
		if s.phase != isPhaseForm {
			t.Errorf("row %d: enter must not open a picker, phase=%v", row, s.phase)
		}
	}
}

// TestJDESweepA_ChoiceRowsCycleInPlace: a bounded set stays on its row rather
// than opening a sub-phase, and a bool is just a two-value set.
func TestJDESweepA_ChoiceRowsCycleInPlace(t *testing.T) {
	sup := NewSupplierFormScreen(Deps{}, "")
	sup.Update(tea.WindowSizeMsg{Width: 110, Height: jdeSweepHeight})
	sup.cursor = 1 // sfType
	sup.syncFocus()
	start := supplierTypeOptions[sup.typeIdx].value
	sup.updateFormPhase(tea.KeyMsg{Type: tea.KeyRight})
	if supplierTypeOptions[sup.typeIdx].value == start {
		t.Fatal("→ should advance the choice")
	}
	sup.updateFormPhase(tea.KeyMsg{Type: tea.KeyLeft})
	if got := supplierTypeOptions[sup.typeIdx].value; got != start {
		t.Errorf("← should walk back, got %q want %q", got, start)
	}
	// The whole set is drawn under the focused row, so it is never cycled blind.
	if out := sup.View(); !strings.Contains(out, "Online") || !strings.Contains(out, "National") {
		t.Errorf("the focused choice row should show its option strip:\n%s", out)
	}

	// A yes/no flips whichever way it is cycled, and gets no strip — "< Yes >"
	// already says what the other value is.
	sup.cursor = 4 // sfTaxFree
	sup.syncFocus()
	for _, key := range []tea.KeyType{tea.KeyRight, tea.KeyLeft, tea.KeySpace} {
		was := sup.taxFree
		sup.updateFormPhase(tea.KeyMsg{Type: key})
		if sup.taxFree == was {
			t.Errorf("%v should flip the yes/no", key)
		}
	}
}
