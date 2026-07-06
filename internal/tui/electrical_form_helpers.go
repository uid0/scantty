// Shared render/help helpers for the three electrical CRUD forms
// (electrical_forms.go). Factored here so PowerPanel / PowerBreaker /
// PowerCircuit forms render selects, toggles, the picker sub-phase and the
// field-help line identically without three copies of the same code.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

// elecVisibleRows returns how many field rows fit the current terminal height,
// reserving chrome for the help line, scroll indicators, spacing and status.
func elecVisibleRows(terminalHeight int) int {
	const chrome = 6
	avail := screenBodyHeight(terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

// indexOfField returns the slice index of field id in a visible-field list, or
// -1. Used by the breaker form's rebuildFields to keep the cursor anchored on
// the same field across a conditional show/hide.
func indexOfField(fields []int, id int) int {
	for i, f := range fields {
		if f == id {
			return i
		}
	}
	return -1
}

func elecToggleLabel(on bool) string {
	if on {
		return StyleStatusOK.Render("[x] yes")
	}
	return StyleMuted.Render("[ ] no")
}

func elecSelectLabel(opts []selectOption, idx int) string {
	if idx >= 0 && idx < len(opts) {
		return "‹ " + opts[idx].label + " ›"
	}
	return ""
}

// elecFieldHelp builds the top help line, tailoring the leading verb to the
// focused field's kind.
func elecFieldHelp(kindOf func(int) assetFieldKind, current func() (int, bool)) string {
	kindHelp := "type to edit"
	if id, ok := current(); ok {
		switch kindOf(id) {
		case akToggle:
			kindHelp = "space toggle"
		case akSelect:
			kindHelp = "space/←→ change"
		case akPicker:
			kindHelp = "space to pick"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

// elecPickView renders the single-value picker sub-phase shared by all three
// forms (all electrical FKs are int pks → itemPickOption rows).
func elecPickView(what string, search *textinput.Model, typing bool, opts []itemPickOption, cursor int) string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick "+what+" — j/k move · / filter · enter select · esc back") + "\n\n")
	if typing || search.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + search.View() + "\n\n")
	}
	if len(opts) == 0 {
		b.WriteString(StyleMuted.Render("(no matches)"))
		return b.String()
	}
	const window = 12
	start, end := fieldWindow(cursor, len(opts), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == cursor {
			caret = "  ▸ "
		}
		opt := opts[i]
		switch {
		case i == cursor:
			b.WriteString(StyleSidebarItemActive.Render(caret+opt.label) + "\n")
		case opt.clear:
			b.WriteString(caret + StyleMuted.Render(opt.label) + "\n")
		default:
			b.WriteString(caret + opt.label + "\n")
		}
	}
	if end < len(opts) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(opts)-end)) + "\n")
	}
	return b.String()
}
