package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Packaging-chain editor for the inventory item form (OMS #979 backend, web
// #983's PackagingChainEditor).
//
// Edits an item's packaging_levels as an ordered list, largest rung first —
// "case, ream, sheet". A row's POSITION is its sort_order (0 = outermost), so
// moving a row up or down is the whole of "reorder", and base_units is always
// how many BASE units one of that rung holds — which is what makes the derived
// "1 case = 10 reams" line computable without a second column to keep in sync.
//
// Two sub-phases, following the sc-ue4 nested sub-list idiom: itemFormPhaseChain
// lists the rungs, itemFormPhaseChainRow edits one. Nothing here talks to the
// API — the chain is saved nested with the item.
//
// Both render through the columnar layer and the persistent action bar
// (jde_form.go, sc-dnhx), which cost the list its letter accelerators. Where
// a / e / x / d / J / K used to hide:
//
//	a  add a level     → a trailing "(add a level)" ROW, opened with Ctrl-E like
//	                     every other row on the sheet
//	e  edit a level    → Ctrl-E on that level
//	x  remove a level  → a "Remove this level" ROW of the level's own editor,
//	                     which is where the thing being removed is on screen
//	J  move inward     → →, and K (outward) → ←, because the chain reads
//	                     left-to-right — "case › ream › sheet" — so the arrows
//	                     move a level along the chain it is drawn in
//	j/k                → the arrow keys, which is what they always meant

// chainAddRow is the cursor position of the trailing "(add a level)" row: one
// past the last rung, always present, so adding is a row you navigate to rather
// than a letter you have to know.
func (s *InventoryItemFormScreen) chainAddRow() int { return len(s.packRows) }

// onChainAddRow reports whether the cursor is standing on it.
func (s *InventoryItemFormScreen) onChainAddRow() bool { return s.chainCursor >= len(s.packRows) }

// openChain enters the chain list sub-phase.
func (s *InventoryItemFormScreen) openChain() {
	s.phase = itemFormPhaseChain
	s.chainRowErr = ""
	if s.chainCursor > s.chainAddRow() {
		s.chainCursor = s.chainAddRow()
	}
	if s.chainCursor < 0 {
		s.chainCursor = 0
	}
}

// closeChain returns to the form, re-deriving the visible field list because the
// counting-level row's contents (and the min/reorder labels) depend on the chain.
func (s *InventoryItemFormScreen) closeChain() {
	s.phase = itemFormPhaseForm
	s.chainRowErr = ""
	s.rebuildFields()
	s.syncFocus()
}

// updateChainPhase drives the rung list on the reduced key scheme: Up/Down move,
// PgUp/PgDn page, Ctrl-E opens whatever the row IS (a level's editor, or the add
// row's), ←/→ move a level along the chain, and Enter or Esc are both done — the
// chain lives in memory until the ITEM is saved, so neither writes anything and
// there is nothing to cancel.
//
// The pager is bound because chainBarItems NAMES PgUp/PgDn the moment the rung
// list outgrows the pane, and a key the bar names must do something. It was
// named and unbound: a three-rung chain overflows from 80x11 to 80x16, so the
// bar advertised a pair no handler on this phase answered.
func (s *InventoryItemFormScreen) updateChainPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter":
		s.closeChain()
		return s, nil
	case "down", "tab":
		s.moveChainCursor(+1)
	case "up", "shift+tab":
		s.moveChainCursor(-1)
	case "pgdown":
		s.pageChainCursor(+1)
	case "pgup":
		s.pageChainCursor(-1)
	case "ctrl+e":
		if s.onChainAddRow() {
			s.openChainRow(-1)
		} else {
			s.openChainRow(s.chainCursor)
		}
		return s, textinput.Blink
	case "right":
		s.moveChainRow(s.chainCursor, +1)
	case "left":
		s.moveChainRow(s.chainCursor, -1)
	}
	return s, nil
}

// moveChainCursor walks the rung list, clamping at both ends and DECLINING on a
// pane the frame is not drawn into — the highlight it would move is not on
// screen to be seen, and growing the terminal back would find it on a different
// rung.
func (s *InventoryItemFormScreen) moveChainCursor(delta int) {
	l := s.chainListLines()
	next, ok := s.pickRow(s.chainCursor, s.chainAddRow()+1, delta, 0, s.chainBar(l))
	if !ok {
		return
	}
	s.chainCursor = next
}

// pageChainCursor moves a pane's worth of rungs, clamping rather than wrapping —
// the same move jdePageCursor makes on every other columnar list, and the same
// one pageKitCursor makes on the sibling list one file over.
//
// The step is measured off the lines View actually draws, so a page covers
// exactly what the operator can see: a rung whose reading wraps costs more than
// one row and a guessed constant would skip over it. The count includes the
// trailing add row, which is the row after the last rung.
//
// Both gates are the LAYER's, which is why this reads like every other pager in
// the program: pageRow asks the bar really DRAWN whether the frame is on the
// pane and the CEILING bar whether the rungs overflow it, and declines in
// silence either way — for the reason moveChainCursor does, that a movement
// key's whole product is the position. This sheet briefly spelled the second
// half itself; jde_form.go's pageRow carries why that had to move.
func (s *InventoryItemFormScreen) pageChainCursor(dir int) {
	l := s.chainListLines()
	next, ok := s.pageRow(l, s.chainCursor, s.chainAddRow()+1, dir, 0,
		s.chainBar(l), s.chainBarItems(true))
	if !ok {
		return
	}
	s.chainCursor = next
}

// removeChainRow drops a rung. A rung that was the counting level takes the pick
// with it — leaving a dangling key would fail resolveCountLevelError at submit
// with a confusing message, so the pick is cleared here where the cause is
// obvious.
func (s *InventoryItemFormScreen) removeChainRow(index int) {
	if index < 0 || index >= len(s.packRows) {
		return
	}
	if s.packRows[index].key == s.countLevelKey {
		s.countLevelKey = 0
	}
	s.packRows = append(s.packRows[:index:index], s.packRows[index+1:]...)
	if s.chainCursor >= len(s.packRows) {
		s.chainCursor = len(s.packRows) - 1
	}
	if s.chainCursor < 0 {
		s.chainCursor = 0
	}
	s.chainRowErr = ""
}

// moveChainRow swaps a rung with its neighbour, carrying the cursor along. The
// count-level pick rides on the row KEY, so it follows the row rather than the
// position — which is the whole reason rows carry a client-side key.
func (s *InventoryItemFormScreen) moveChainRow(index, delta int) {
	target := index + delta
	if index < 0 || index >= len(s.packRows) || target < 0 || target >= len(s.packRows) {
		return
	}
	s.packRows[index], s.packRows[target] = s.packRows[target], s.packRows[index]
	s.chainCursor = target
	s.chainRowErr = ""
}

// Rows of the per-rung editor. The first two are what enter saves; the third is
// where "remove this level" lives now that the list has no letter for it — it
// sits with the level it removes, which is the only place the operator can see
// what they are about to drop.
const (
	chainRowFieldName = iota
	chainRowFieldUnits
	chainRowFieldRemove
	chainRowFieldCount
)

// openChainRow enters the per-rung editor. index -1 adds a new rung (appended at
// the innermost end, which is where a base unit belongs).
func (s *InventoryItemFormScreen) openChainRow(index int) {
	s.phase = itemFormPhaseChainRow
	s.chainRowEditing = index
	s.chainRowErr = ""
	s.chainRowFocus = chainRowFieldName

	name, units := "", ""
	if index >= 0 && index < len(s.packRows) {
		row := s.packRows[index]
		name = row.name
		if row.baseUnits > 0 {
			units = strconv.Itoa(row.baseUnits)
		}
	}
	s.chainRowName.SetValue(name)
	s.chainRowUnits.SetValue(units)
	s.syncChainRowFocus()
}

// chainRowCount is how many rows the editor offers: a rung being ADDED has
// nothing to remove yet, so it has no remove row.
func (s *InventoryItemFormScreen) chainRowCount() int {
	if s.chainRowEditing < 0 || s.chainRowEditing >= len(s.packRows) {
		return chainRowFieldCount - 1
	}
	return chainRowFieldCount
}

func (s *InventoryItemFormScreen) syncChainRowFocus() {
	s.chainRowName.Blur()
	s.chainRowUnits.Blur()
	switch s.chainRowFocus {
	case chainRowFieldName:
		s.chainRowName.Focus()
	case chainRowFieldUnits:
		s.chainRowUnits.Focus()
	}
}

// updateChainRowPhase drives the per-rung editor: Up/Down move, Enter saves the
// level, Ctrl-E on the remove row drops it, Esc abandons the edit.
func (s *InventoryItemFormScreen) updateChainRowPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = itemFormPhaseChain
		s.chainRowName.Blur()
		s.chainRowUnits.Blur()
		s.chainRowErr = ""
		return s, nil
	case "tab", "down":
		s.moveChainRowFocus(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveChainRowFocus(-1)
		return s, textinput.Blink
	case "enter":
		return s, s.commitChainRow()
	case "ctrl+e":
		if s.chainRowFocus == chainRowFieldRemove {
			// Removing is immediate, as it always was: nothing is written until
			// the item is saved, so there is nothing to undo a confirm would
			// protect — and the level is on screen while it is being dropped.
			index := s.chainRowEditing
			s.phase = itemFormPhaseChain
			s.chainRowName.Blur()
			s.chainRowUnits.Blur()
			s.removeChainRow(index)
		}
		return s, nil
	}

	var cmd tea.Cmd
	switch s.chainRowFocus {
	case chainRowFieldUnits:
		// Gate to digits so the size field only ever holds an integer; editing
		// keys (backspace/arrows) are not KeyRunes, so they pass through.
		if m.Type == tea.KeyRunes {
			for _, r := range m.Runes {
				if r < '0' || r > '9' {
					return s, nil
				}
			}
		}
		s.chainRowUnits, cmd = s.chainRowUnits.Update(m)
	case chainRowFieldName:
		s.chainRowName, cmd = s.chainRowName.Update(m)
	}
	return s, cmd
}

func (s *InventoryItemFormScreen) moveChainRowFocus(delta int) {
	_, items := s.chainRowFrame()
	next, ok := s.moveRow(s.chainRowFocus, s.chainRowCount(), delta, 0, items)
	if !ok {
		return
	}
	s.chainRowFocus = next
	s.syncChainRowFocus()
}

// commitChainRow validates and applies the edited rung, then returns to the list.
// Only the per-row rules are enforced here — the CHAIN rules (one base rung, the
// base rung last, strictly shrinking) are checked across the whole chain, and are
// reported in the list view and again at submit, so a half-built chain can be
// typed in any order.
func (s *InventoryItemFormScreen) commitChainRow() tea.Cmd {
	name := strings.TrimSpace(s.chainRowName.Value())
	if name == "" {
		s.chainRowErr = "level name is required"
		return nil
	}
	units, err := strconv.Atoi(strings.TrimSpace(s.chainRowUnits.Value()))
	if err != nil || units < 1 {
		s.chainRowErr = fmt.Sprintf("%s held must be a whole number of at least 1",
			pluralizeUnit(s.baseUnitValue(), 2))
		return nil
	}

	if s.chainRowEditing >= 0 && s.chainRowEditing < len(s.packRows) {
		s.packRows[s.chainRowEditing].name = name
		s.packRows[s.chainRowEditing].baseUnits = units
		s.chainCursor = s.chainRowEditing
	} else {
		s.packNextKey++
		s.packRows = append(s.packRows, packagingRow{key: s.packNextKey, name: name, baseUnits: units})
		s.chainCursor = len(s.packRows) - 1
	}

	s.phase = itemFormPhaseChain
	s.chainRowEditing = -1
	s.chainRowErr = ""
	s.chainRowFocus = chainRowFieldName
	s.chainRowName.Blur()
	s.chainRowUnits.Blur()
	return nil
}

// chainValue is the one-line value the fPackChain form row shows: the rungs
// outermost-first, or the "counted in base units" state an item with no chain is
// in — as PLAIN text plus whether it is an empty state, so a focused row can
// reverse-video the whole field. Styled text inside would end the highlight
// partway through it.
func (s *InventoryItemFormScreen) chainValue() (string, bool) {
	if len(s.packRows) == 0 {
		return fmt.Sprintf("(none — counted in %s)", pluralizeUnit(s.baseUnitValue(), 2)), true
	}
	names := make([]string, 0, len(s.packRows))
	for _, row := range s.packRows {
		name := strings.TrimSpace(row.name)
		if name == "" {
			name = "(unnamed)"
		}
		names = append(names, name)
	}
	return fmt.Sprintf("%s  (%d %s)", strings.Join(names, " › "), len(names), plural("level", len(names))), false
}

// countLevelValue is the value the fCountLevel row shows — the picked rung, or
// the reason there is nothing to pick (the web renders the same two
// placeholders) — and whether the row has anything to cycle through. A chain
// with no named rungs is a STATE, not a choice: the row says why there is
// nothing to pick and the bar drops ←/→, rather than offering an arrow key that
// would do nothing.
func (s *InventoryItemFormScreen) countLevelValue() (string, bool) {
	if idx := packagingRowIndex(s.packRows, s.countLevelKey); idx >= 0 {
		name := strings.TrimSpace(s.packRows[idx].name)
		if name == "" {
			name = "(unnamed)"
		}
		return name, true
	}
	if len(s.packRows) == 0 {
		return "(add a packaging level first)", false
	}
	if !chainHasNamedRow(s.packRows) {
		return "(name a packaging level first)", false
	}
	return "(none selected)", true
}

// chainHasNamedRow reports whether any rung can legally be the counting level —
// cycleCountLevel walks the NAMED rows, so a chain of unnamed ones has nothing
// for ←/→ to land on.
func chainHasNamedRow(rows []packagingRow) bool {
	for _, row := range rows {
		if strings.TrimSpace(row.name) != "" {
			return true
		}
	}
	return false
}

// viewChain renders the rung list as a columnar detail grid, with the derived
// "1 case = 10 reams" ratio per row and the whole-chain validation messages
// beneath — the same guidance the web editor puts under its table.
func (s *InventoryItemFormScreen) viewChain() string {
	l := s.chainListLines()
	return s.frame(l, s.chainCursor, "", s.chainBar(l))
}

// chainListLines is the rung list, split out of viewChain so the movement arm
// measures the same body and the same bar the frame draws.
func (s *InventoryItemFormScreen) chainListLines() *jdeLines {
	unit := s.baseUnitValue()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Packaging chain"))
	l.Add(jdeIndent + StyleMuted.Render(fmt.Sprintf(
		"Largest package first, ending with the base unit. Each level says how many %s it holds — a case of 10 reams of 100 sheets is 1000, 100, 1.",
		pluralizeUnit(unit, 2))))
	l.Add("")

	if len(s.packRows) == 0 {
		l.Add(jdeIndent + StyleMuted.Render(fmt.Sprintf(
			"No packaging levels — this item is counted in %s.", pluralizeUnit(unit, 2))))
	}
	for i, row := range s.packRows {
		name := strings.TrimSpace(row.name)
		if name == "" {
			name = "(unnamed)"
		}
		line := fmt.Sprintf("%-16s %s", name, StyleMuted.Render(fmt.Sprintf(
			"%d %s", row.baseUnits, pluralizeUnit(unit, row.baseUnits))))
		if ratio, ok := perParent(s.packRows, i); ok {
			line += StyleMuted.Render(fmt.Sprintf("  ·  = %s %s",
				trimFloat(ratio), pluralizeUnitF(strings.TrimSpace(s.packRows[i+1].name), ratio)))
		}
		if row.key == s.countLevelKey {
			line += "  " + StyleStatusOK.Render("[counted here]")
		}
		if i == s.chainCursor {
			l.AddRow(i, StyleSidebarItemActive.Render("  ▸ "+line))
			continue
		}
		l.AddRow(i, "    "+line)
	}

	// The add row is the last navigable row, always present: adding a level is
	// something you move to, not a letter you have to have memorised.
	add := "(add a level)"
	if s.onChainAddRow() {
		l.AddRow(s.chainAddRow(), StyleSidebarItemActive.Render("  ▸ "+add))
	} else {
		l.AddRow(s.chainAddRow(), "    "+StyleMuted.Render(add))
	}

	for _, msg := range validatePackagingChain(s.packRows) {
		l.Add("")
		l.Add(jdeIndent + StyleStatusWarn.Render("! "+msg))
	}
	if s.chainRowErr != "" {
		l.Add("")
		l.Add(jdeIndent + StyleStatusError.Render("✗ "+s.chainRowErr))
	}
	return l
}

// chainBar names the keys that apply where the cursor is standing. Enter and Esc
// both mean done: the chain is saved nested with the ITEM, so leaving the list
// writes nothing either way and there is nothing to cancel.
func (s *InventoryItemFormScreen) chainBar(body *jdeLines) []actionBarItem {
	return s.chainBarItems(s.bodyPagesForBar(body, s.chainAddRow()+1, 0, s.chainBarItems(true)))
}

// chainBarItems is chainBar for a given paging state, so the bar that is
// MEASURED against the pane is the bar that is drawn on it.
func (s *InventoryItemFormScreen) chainBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Done"}, {"Esc", "Done"}, {"UP/DN", "Levels"}}
	if s.onChainAddRow() {
		items = append(items, actionBarItem{"Ctrl-E", "Add level"})
	} else {
		items = append(items, actionBarItem{"Ctrl-E", "Edit level"})
		if len(s.packRows) > 1 {
			// ← is outward, → is inward — the chain reads left to right.
			items = append(items, actionBarItem{"←→", "Move level"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// capitalizeFirst upper-cases the first RUNE, not the first byte — a base unit is
// operator-typed free text and may well be multi-byte, which a byte slice would
// cut in half.
func capitalizeFirst(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+utf8.RuneLen(r):]
	}
	return s
}

// viewChainRow renders the per-rung editor as a columnar block. The frame it
// builds is split out so the movement arm can ask the layer whether that frame
// is DRAWN before it moves the caret — one builder, so the bar that is measured
// is the bar drawn.
func (s *InventoryItemFormScreen) viewChainRow() string {
	l, items := s.chainRowFrame()
	return s.frame(l, s.chainRowFocus, s.statusRow(false, "", s.chainRowErr), items)
}

func (s *InventoryItemFormScreen) chainRowFrame() (*jdeLines, []actionBarItem) {
	unit := s.baseUnitValue()
	title := "Edit packaging level"
	if s.chainRowEditing < 0 {
		title = "Add packaging level"
	}

	fields := []jdeField{
		{
			Label:   "Level name",
			Kind:    jdeText,
			Input:   &s.chainRowName,
			Hint:    "required",
			Focused: s.chainRowFocus == chainRowFieldName,
		},
		{
			Label:   capitalizeFirst(pluralizeUnit(unit, 2)) + " held",
			Kind:    jdeText,
			Input:   &s.chainRowUnits,
			Width:   10,
			Hint:    "whole number · the innermost level holds 1",
			Focused: s.chainRowFocus == chainRowFieldUnits,
		},
	}
	if s.chainRowCount() > chainRowFieldRemove {
		fields = append(fields, jdeField{
			Label:   "Remove this level",
			Kind:    jdeValue,
			Value:   "drops it from the chain",
			Dim:     true,
			Focused: s.chainRowFocus == chainRowFieldRemove,
		})
	}

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render(title))
	l.Add(jdeIndent + StyleMuted.Render(fmt.Sprintf(
		"How many %s one of these holds.", pluralizeUnit(unit, 2))))
	l.Add("")
	l.AddFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)

	items := []actionBarItem{{"Enter", "Save level"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if s.chainRowFocus == chainRowFieldRemove {
		items = append(items, actionBarItem{"Ctrl-E", "Remove"})
	}
	return l, items
}
