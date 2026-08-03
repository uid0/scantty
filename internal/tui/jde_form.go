// Columnar green-screen form primitives — the shared layer of the ScanTTY
// "JD Edwards" redesign (sc-h412). po_edit.go is the pilot that uses it; the
// other hand-rolled renderField implementations (asset_form.go,
// storage_slot_form.go, project_storage_form.go, …) repoint here in later
// beads, which is why nothing in this file knows what a purchase order is.
//
// The look is JD Edwards World: field labels right-aligned into one common
// column, a dotted leader, then the input area — text as an underscored /
// reverse-video run, a bounded choice set as "< value >" — with the focused
// row picked out in the accent colour. A PERSISTENT action bar sits at the
// bottom naming exactly the keys that apply, so nothing on the screen depends
// on a letter accelerator the operator has to have memorised:
//
//	     Supplier ..... Acme Bolt Co._________
//	 Date ordered ..... 2026-08-02__            YYYY-MM-DD
//	     Priority ..... < Normal >
//	Payment terms ..... < Net 30 >
//	------------------------------------------------------
//	 Enter=Save   Esc=Exit   UP/DN=Fields   Ctrl-E=Edit
//
// Three pieces live here and are meant to be used together:
//
//	jdeField / renderJDEField  — one columnar row.
//	jdeLines                   — the body an operator scrolls, built line by
//	                             line and tagged with the navigable row each
//	                             line belongs to, so Window() can keep the
//	                             cursor's whole block on screen at a FIXED
//	                             height (which is what lets the bar be
//	                             persistent rather than "wherever the content
//	                             happened to end").
//	renderActionBar            — the bar itself.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// jdeFieldKind is what a columnar row holds. Only three kinds exist because
// only three behave differently: something you type into, a bounded set you
// cycle in place, and a value some other gesture changes (a picker, an action).
type jdeFieldKind int

const (
	// jdeText is a bubbles textinput. Value is its already-rendered View(),
	// which carries the cursor, so this file never re-styles it — it only
	// fills the rest of the input area.
	jdeText jdeFieldKind = iota
	// jdeChoice is a fixed choice set, drawn "< value >" — the universal
	// signal for ←/→.
	jdeChoice
	// jdeValue is a value this row does not itself edit: a picker's current
	// selection, a status, anything the action bar's Ctrl-E opens.
	jdeValue
)

// jdeField is one row of the columnar block.
type jdeField struct {
	Label string
	Kind  jdeFieldKind
	// Value is plain text for jdeChoice / jdeValue, and the pre-rendered
	// textinput View() for jdeText.
	Value string
	// Width is the input area a jdeText row fills with underscores (unfocused)
	// or a reverse-video run (focused). Zero takes jdeFieldWidth.
	Width int
	// Hint is a muted note drawn after the input area — a date format, a
	// unit, "optional". It lives here rather than in Label so a long note
	// cannot widen the label column and push every field right.
	Hint string
	// Dim renders the value muted: an empty state ("(none)"), not a value.
	Dim     bool
	Focused bool
}

const (
	// jdeIndent is the left margin every columnar row shares.
	jdeIndent = "  "
	// jdeLeader is the dotted leader between the label column and the input
	// area. Fixed-length, as in JD Edwards World — the labels are what is
	// right-aligned, not the dots.
	jdeLeader = " ..... "
	// jdeFieldWidth is the default input area for a text row.
	jdeFieldWidth = 22
	// jdeLabelMaxWidth caps the label column so one verbose label cannot
	// shove every input area off a narrow terminal.
	jdeLabelMaxWidth = 26
)

// jdeLabelWidth returns the label column shared by every field passed in.
// Callers that render several blocks which must line up (a metadata band and
// an associations band, say) compute it ONCE over all of them and pass it to
// renderJDEField, rather than letting each block find its own column.
func jdeLabelWidth(groups ...[]jdeField) int {
	w := 0
	for _, group := range groups {
		for _, f := range group {
			if n := lipgloss.Width(f.Label); n > w {
				w = n
			}
		}
	}
	if w > jdeLabelMaxWidth {
		w = jdeLabelMaxWidth
	}
	return w
}

// renderJDEField draws one row: the right-aligned label, the leader, the input
// area, and any hint.
func renderJDEField(f jdeField, labelWidth int) string {
	label := f.Label
	if n := lipgloss.Width(label); n > labelWidth {
		label = truncateVisible(label, labelWidth)
	}
	label = padCell(label, labelWidth, alignRight)
	if f.Focused {
		label = StyleJDELabelFocused.Render(label)
	} else {
		label = StyleJDELabel.Render(label)
	}

	row := jdeIndent + label + StyleJDELeader.Render(jdeLeader) + jdeFieldArea(f)
	if f.Hint != "" {
		row += "  " + StyleJDEHint.Render(f.Hint)
	}
	return row
}

// jdeFieldArea renders the input area alone — the part right of the leader.
func jdeFieldArea(f jdeField) string {
	switch f.Kind {
	case jdeChoice:
		body := "< " + f.Value + " >"
		if f.Focused {
			return StyleJDEFieldFocused.Render(body)
		}
		return StyleJDEBracket.Render("< ") + jdeValueText(f) + StyleJDEBracket.Render(" >")

	case jdeValue:
		if f.Focused {
			return StyleJDEFieldFocused.Render(f.Value)
		}
		return jdeValueText(f)

	default:
		// A text row: the textinput has drawn the value and its own cursor, so
		// all that is left is to fill the field out to its width. Unfocused
		// that fill is the underscored run of a green-screen form; focused it
		// is a solid reverse-video field, which is what makes the row the
		// operator is standing in unmistakable without moving any columns.
		width := f.Width
		if width <= 0 {
			width = jdeFieldWidth
		}
		fill := width - lipgloss.Width(f.Value)
		if fill < 0 {
			fill = 0
		}
		if f.Focused {
			return f.Value + StyleJDEFieldFocused.Render(strings.Repeat(" ", fill))
		}
		return f.Value + StyleJDEInput.Render(strings.Repeat("_", fill))
	}
}

func jdeValueText(f jdeField) string {
	if f.Dim {
		return StyleMuted.Render(f.Value)
	}
	return f.Value
}

// jdeInputValue is what a jdeText row should carry for a bubbles textinput:
// the live View() when the row has focus — it draws the cursor — and the plain
// value when it does not. A blurred textinput still renders a cursor cell, and
// in a columnar form that reads as a stray gap between the value and the
// underscores filling the rest of the field. A field showing its placeholder
// keeps View(), which is the only thing that renders one.
func jdeInputValue(ti textinput.Model, focused bool) string {
	if focused || (ti.Value() == "" && ti.Placeholder != "") {
		return ti.View()
	}
	return ti.Value()
}

// renderJDEFields is the convenience form: one block, its own label column.
func renderJDEFields(fields []jdeField) []string {
	w := jdeLabelWidth(fields)
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = renderJDEField(f, w)
	}
	return out
}

// ---------------------------------------------------------------------------
// The scrollable body
// ---------------------------------------------------------------------------

// jdeNoRow tags a line that belongs to no navigable row — a heading, a rule, a
// column header, a blank separator.
const jdeNoRow = -1

// jdeLines accumulates a form body line by line, remembering which navigable
// row each line belongs to. A row may own several lines (a field plus the
// option strip under it, a detail line plus what it was ordered for), and
// Window keeps the whole block on screen rather than the first line of it.
type jdeLines struct {
	text []string
	row  []int
}

// Add appends a line that belongs to no row.
func (l *jdeLines) Add(text string) { l.AddRow(jdeNoRow, text) }

// AddRow appends a line belonging to navigable row `row`.
func (l *jdeLines) AddRow(row int, text string) {
	l.text = append(l.text, text)
	l.row = append(l.row, row)
}

func (l *jdeLines) Len() int { return len(l.text) }

// block returns the [first,last] line indexes owned by `row`. A row with no
// lines (nothing is selected, or the row scrolled out of existence) reports the
// top of the body, which is where a cursor with nothing to show belongs.
func (l *jdeLines) block(row int) (int, int) {
	first, last := -1, -1
	for i, r := range l.row {
		if r != row {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return 0, 0
	}
	return first, last
}

// rowsIn counts the distinct navigable rows with a line in [start,end) — what
// a PgUp/PgDn step is worth.
func (l *jdeLines) rowsIn(start, end int) int {
	seen := map[int]bool{}
	for i := start; i < end && i < len(l.row); i++ {
		if r := l.row[i]; r != jdeNoRow {
			seen[r] = true
		}
	}
	return len(seen)
}

// Window returns EXACTLY `avail` lines (or every line, when they all fit)
// positioned so the cursor row's whole block is visible, plus how many
// navigable rows that window holds.
//
// The height is exact because the action bar underneath has to sit at the
// bottom of the pane on every frame — a body that shrank and grew with its
// content would walk the bar up and down the screen. When the body overflows,
// the first and last of the `avail` lines are spent on the "more above / more
// below" indicators (blank when there is nothing in that direction), so the
// count never changes with the scroll position either.
func (l *jdeLines) Window(cursorRow, avail int) ([]string, int) {
	n := len(l.text)
	if avail <= 0 {
		return nil, 0
	}
	if n <= avail {
		out := make([]string, n, avail)
		copy(out, l.text)
		return out, l.rowsIn(0, n)
	}

	first, last := l.block(cursorRow)
	if avail <= 2 {
		// No room for indicators: show the cursor's own lines and nothing else.
		start := first
		if start+avail > n {
			start = n - avail
		}
		return append([]string(nil), l.text[start:start+avail]...), l.rowsIn(start, start+avail)
	}

	body := avail - 2
	start := first - (body-1)/2
	if start+body > n {
		start = n - body
	}
	if start < 0 {
		start = 0
	}
	// Prefer the END of the block when it cannot all fit; then pull back to the
	// start, which is where the focused field itself is drawn.
	if last >= start+body {
		start = last - body + 1
	}
	if first < start {
		start = first
	}
	if start < 0 {
		start = 0
	}
	if start+body > n {
		start = n - body
	}
	end := start + body

	out := make([]string, 0, avail)
	if start > 0 {
		out = append(out, StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)))
	} else {
		out = append(out, "")
	}
	out = append(out, l.text[start:end]...)
	if end < n {
		out = append(out, StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", n-end)))
	} else {
		out = append(out, "")
	}
	return out, l.rowsIn(start, end)
}

// ---------------------------------------------------------------------------
// The persistent action bar
// ---------------------------------------------------------------------------

// actionBarItem is one key and what it does right now. Callers build the slice
// per frame from the phase and the focused row: the bar's contract is that
// every key on it works where it is shown, and every key that works is on it.
type actionBarItem struct {
	Key   string
	Label string
}

// renderActionBar draws the rule and the key line — actionBarRows tall, always.
// Items are joined with a wide gutter that tightens before anything is dropped,
// because a key that fell off the bar is a key the operator cannot discover.
func renderActionBar(width int, items []actionBarItem) string {
	if width < 8 {
		width = 8
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if it.Key == "" {
			continue
		}
		part := StyleActionBarKey.Render(it.Key)
		if it.Label != "" {
			part += StyleActionBar.Render("=" + it.Label)
		}
		parts = append(parts, part)
	}
	keys := ""
	for _, gutter := range []string{"   ", "  ", " "} {
		keys = strings.Join(parts, gutter)
		if lipgloss.Width(keys)+len(jdeIndent) <= width {
			break
		}
	}
	return StyleActionBarRule.Render(strings.Repeat("-", width)) + "\n" + jdeIndent + keys
}
