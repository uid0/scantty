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

// openChain enters the chain list sub-phase.
func (s *InventoryItemFormScreen) openChain() {
	s.phase = itemFormPhaseChain
	s.chainRowErr = ""
	if s.chainCursor >= len(s.packRows) {
		s.chainCursor = len(s.packRows) - 1
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

// updateChainPhase drives the rung list: j/k move, a add, enter/e edit, x/d
// remove, J/K reorder, esc back to the form.
func (s *InventoryItemFormScreen) updateChainPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.closeChain()
		return s, nil
	case "j", "down":
		if s.chainCursor < len(s.packRows)-1 {
			s.chainCursor++
		}
	case "k", "up":
		if s.chainCursor > 0 {
			s.chainCursor--
		}
	case "a":
		s.openChainRow(-1)
		return s, textinput.Blink
	case "enter", "e":
		if len(s.packRows) > 0 {
			s.openChainRow(s.chainCursor)
			return s, textinput.Blink
		}
	case "x", "d":
		s.removeChainRow(s.chainCursor)
	case "J":
		s.moveChainRow(s.chainCursor, +1)
	case "K":
		s.moveChainRow(s.chainCursor, -1)
	}
	return s, nil
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

// openChainRow enters the per-rung editor. index -1 adds a new rung (appended at
// the innermost end, which is where a base unit belongs).
func (s *InventoryItemFormScreen) openChainRow(index int) {
	s.phase = itemFormPhaseChainRow
	s.chainRowEditing = index
	s.chainRowErr = ""
	s.chainRowOnUnits = false

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
	s.chainRowName.Focus()
	s.chainRowUnits.Blur()
}

// updateChainRowPhase drives the per-rung editor: tab/↑↓ switch field, enter
// commits, esc abandons the edit.
func (s *InventoryItemFormScreen) updateChainRowPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = itemFormPhaseChain
		s.chainRowName.Blur()
		s.chainRowUnits.Blur()
		s.chainRowErr = ""
		return s, nil
	case "tab", "down", "shift+tab", "up":
		s.chainRowOnUnits = !s.chainRowOnUnits
		if s.chainRowOnUnits {
			s.chainRowName.Blur()
			s.chainRowUnits.Focus()
		} else {
			s.chainRowUnits.Blur()
			s.chainRowName.Focus()
		}
		return s, textinput.Blink
	case "enter":
		return s, s.commitChainRow()
	}

	var cmd tea.Cmd
	if s.chainRowOnUnits {
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
	} else {
		s.chainRowName, cmd = s.chainRowName.Update(m)
	}
	return s, cmd
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
	s.chainRowName.Blur()
	s.chainRowUnits.Blur()
	return nil
}

// chainSummary is the one-line value the fPackChain form row shows: the rungs
// outermost-first, or the "counted in base units" state an item with no chain is
// in.
func (s *InventoryItemFormScreen) chainSummary() string {
	if len(s.packRows) == 0 {
		return StyleMuted.Render(fmt.Sprintf("(none — counted in %s)", pluralizeUnit(s.baseUnitValue(), 2)))
	}
	names := make([]string, 0, len(s.packRows))
	for _, row := range s.packRows {
		name := strings.TrimSpace(row.name)
		if name == "" {
			name = "(unnamed)"
		}
		names = append(names, name)
	}
	return fmt.Sprintf("%s  ", strings.Join(names, " › ")) +
		StyleMuted.Render(fmt.Sprintf("(%d %s)", len(names), plural("level", len(names))))
}

// countLevelSummary is the value the fCountLevel row shows — the picked rung, or
// the reason there is nothing to pick (the web renders the same two placeholders).
func (s *InventoryItemFormScreen) countLevelSummary() string {
	idx := packagingRowIndex(s.packRows, s.countLevelKey)
	if idx >= 0 {
		name := strings.TrimSpace(s.packRows[idx].name)
		if name == "" {
			name = "(unnamed)"
		}
		return "‹ " + name + " ›"
	}
	if len(s.packRows) == 0 {
		return StyleMuted.Render("(add a packaging level first)")
	}
	return StyleMuted.Render("(none selected)")
}

// viewChain renders the rung list, with the derived "1 case = 10 reams" ratio per
// row and the whole-chain validation messages beneath — the same guidance the
// web editor puts under its table.
func (s *InventoryItemFormScreen) viewChain() string {
	unit := s.baseUnitValue()
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Packaging chain") + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf(
		"Largest package first, ending with the base unit. Each level says how many %s it holds — a case of 10 reams of 100 sheets is 1000, 100, 1.",
		pluralizeUnit(unit, 2))) + "\n\n")

	if len(s.packRows) == 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf(
			"No packaging levels — this item is counted in %s.", pluralizeUnit(unit, 2))) + "\n")
	}
	for i, row := range s.packRows {
		caret := "    "
		if i == s.chainCursor {
			caret = "  ▸ "
		}
		name := strings.TrimSpace(row.name)
		if name == "" {
			name = StyleMuted.Render("(unnamed)")
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
			b.WriteString(StyleSidebarItemActive.Render(caret+line) + "\n")
		} else {
			b.WriteString(caret + line + "\n")
		}
	}

	for _, msg := range validatePackagingChain(s.packRows) {
		b.WriteString("\n" + StyleStatusWarn.Render("! "+msg))
	}
	if s.chainRowErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.chainRowErr))
	}
	b.WriteString("\n\n" + StyleMuted.Render(
		"a add · enter/e edit · x remove · J/K move · j/k select · esc done"))
	return b.String()
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

// viewChainRow renders the per-rung editor.
func (s *InventoryItemFormScreen) viewChainRow() string {
	unit := s.baseUnitValue()
	title := "Edit packaging level"
	if s.chainRowEditing < 0 {
		title = "Add packaging level"
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(title) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf(
		"How many %s one of these holds. The innermost level holds 1.", pluralizeUnit(unit, 2))) + "\n\n")

	rows := []struct {
		label string
		view  string
		on    bool
	}{
		{"Level name", s.chainRowName.View(), !s.chainRowOnUnits},
		{capitalizeFirst(pluralizeUnit(unit, 2)) + " held", s.chainRowUnits.View(), s.chainRowOnUnits},
	}
	for _, row := range rows {
		caret := "  "
		if row.on {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(row.label+": ") + row.view + "\n")
	}
	if s.chainRowErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.chainRowErr) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab/↑↓ move · enter save level · esc cancel"))
	return b.String()
}
