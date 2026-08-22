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
// Five pieces live here and are meant to be used together:
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
//	jdeScreen                  — the pane geometry and the framing that puts a
//	                             body, a status line and the bar together.
//	jdePickList                — the "filter and choose one" sub-phase every
//	                             foreign-key row opens.
package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
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
	// Swatch is a pre-styled sample of what the row's value MEANS — the one
	// today is a hex colour's ● in that colour (sc-ns53). It is a slot of its
	// own because it is the only thing on the row that carries its own colour
	// sequence, and a sequence carries its own reset: inside the input area it
	// would end the focused row's reverse-video run partway across, and inside
	// the Hint it would be eaten by StyleJDEHint.Render. So it is drawn LAST,
	// outside every styled span — the same reason category_form's parentValue
	// hands back plain text and a flag.
	Swatch string
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

// jdeLabelFields turns a form's label map into the field list jdeLabelWidth
// measures. Only the Label matters to the width, so nothing else is filled in.
// A family of screens reached from one another computes its shared column from
// these rather than pinning it to a number, so renaming a field can never
// silently break the alignment (electrical_form_helpers.go's elecLabelWidth and
// storage_form_helpers.go's storageLabelWidth are both built this way).
func jdeLabelFields(labels map[int]string) []jdeField {
	out := make([]jdeField, 0, len(labels))
	for _, l := range labels {
		out = append(out, jdeField{Label: l})
	}
	return out
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
	if f.Swatch != "" {
		row += "  " + f.Swatch
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

// jdeColorRow finishes a hex-colour text row: the sample of what the value is,
// and the format note that says what to type. They answer one question at two
// different moments — "what shape does this take?" until the value is a colour,
// "which colour is it?" once it is — so the row carries whichever one is still
// useful and never both.
//
// That is a content decision before it is a width one, but it is also what keeps
// these rows inside an 80-column pane: a colour row costs three columns for the
// sample and gives back nine for the note, where clampToBox would otherwise have
// truncated the sample straight off the end (sc-ye0i, sc-xxpa).
func jdeColorRow(f jdeField, value string) jdeField {
	if f.Swatch = hexSwatch(value); f.Swatch != "" {
		f.Hint = ""
	}
	return f
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
	return jdeEchoValue(ti)
}

// jdeEchoValue is a blurred box's value as it is allowed to be SEEN. View()
// applies the box's echo mode itself, so only this path has to: a masked field
// that gave up its mask the moment the cursor moved off it would print the
// secret on screen — the webhook sheet's HMAC secret is the first of these
// (sc-lmsi).
func jdeEchoValue(ti textinput.Model) string {
	switch ti.EchoMode {
	case textinput.EchoPassword:
		return strings.Repeat(string(ti.EchoCharacter), utf8.RuneCountInString(ti.Value()))
	case textinput.EchoNone:
		return ""
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

// jdeYesNo is how a bool reads on a choice row. A toggle IS a two-value choice
// set, so it draws "< Yes >" like every other bounded set rather than keeping a
// checkbox glyph the reduced key scheme would then have to explain separately.
func jdeYesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

// jdeStripIndent is the left pad that lines a line drawn UNDER a field up with
// the input area above it — an option strip, a derived preview.
func jdeStripIndent(labelWidth int) string {
	return strings.Repeat(" ", len(jdeIndent)+labelWidth+len(jdeLeader))
}

// jdeStripWidth is how much room such a line has, given the pane's body width
// (0 when the width is not known yet, which means "do not truncate").
func jdeStripWidth(bodyWidth, labelWidth int) int {
	if bodyWidth <= 0 {
		return 0
	}
	if w := bodyWidth - len(jdeStripIndent(labelWidth)); w > 0 {
		return w
	}
	return 0
}

// jdeNoteLines is a standing note as the dimmed lines drawn UNDER the field row
// it is about, indented to line up with the input area above them.
//
// It is the fold for a note too long to ride as a Hint. The two alternatives
// both lose: clampToBox TRUNCATES an over-wide row rather than wrapping it, so
// an oversized hint is silently cut (sc-ye0i), and a footer under the whole
// sheet is a note nobody ties to a field (sc-6qsk). bodyWidth of 0 means the
// pane's width is not known yet, which — as everywhere else in this file —
// means "do not truncate", so the note stays on one line.
func jdeNoteLines(note string, labelWidth, bodyWidth int) []string {
	wrapped := jdeWrapNote(note, jdeStripWidth(bodyWidth, labelWidth))
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, jdeStripIndent(labelWidth)+StyleMuted.Render(line))
	}
	return out
}

// jdeWrapNote greedily wraps on spaces. A single word too long for the width —
// a media URL, a JSON snippet — is ELLIPSISED rather than left to run on:
// clampToBox would cut it at the pane edge with nothing to say it had, and a
// value the operator cannot tell is truncated is worse than one they can.
func jdeWrapNote(note string, width int) []string {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil
	}
	if width <= 0 {
		return []string{note}
	}
	var out []string
	line := ""
	flush := func() {
		if line != "" {
			out = append(out, line)
			line = ""
		}
	}
	for _, word := range strings.Fields(note) {
		if lipgloss.Width(word) > width {
			flush()
			if width < 2 {
				continue
			}
			out = append(out, truncateVisible(word, width-1)+"…")
			continue
		}
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) <= width:
			line += " " + word
		default:
			flush()
			line = word
		}
	}
	flush()
	return out
}

// ---------------------------------------------------------------------------
// Detail grids
// ---------------------------------------------------------------------------
//
// A detail grid is the OTHER thing a columnar sheet holds: not fields hanging
// off the leader column, but a dense read-only listing of the records that ride
// with the one being edited — a purchase order's lines (po_edit.go), an asset's
// parts (asset_form_supplies.go, sc-hf1z), an item's suppliers
// (inventory_item_form_suppliers.go, sc-7wag). Each is a numbered row plus a
// continuation line or two of readings indented under it.

// fitCell trims a cell to `w` INCLUDING the ellipsis, so an over-long value
// cannot push the columns to its right out of alignment. truncateOneLine returns
// n+1 characters for a width of n, which is what makes it wrong here; and it
// counts runes, where a grid has to count display columns.
func fitCell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	return truncateVisible(s, w-1) + "…"
}

// jdeToken is one reading on a detail row's continuation line. It carries its
// own style rather than pre-rendered text because jdeWrapTokens does its width
// accounting on the PLAIN text and renders each token whole afterwards — which
// is what keeps a line from ever being cut through an escape sequence. Holding
// the style as a value also lets a test assert the styling contract on the
// STRUCT, which is the only place it survives (lipgloss renders flat in a test
// binary — sc-lmsi).
type jdeToken struct {
	text  string
	style lipgloss.Style
}

func (t jdeToken) render() string { return t.style.Render(t.text) }

// jdeTokenSep separates the readings on a continuation line.
const jdeTokenSep = " · "

// jdeWrapTokens lays a row's readings out under it, WRAPPING rather than
// trimming: the readings run well past a narrow pane, and the piece an ellipsis
// would eat is the LAST one — which is exactly where the thing worth acting on
// sits (an asset part's NEEDS REPLACEMENT, a supplier link's [discontinued]).
// Only a single reading longer than the whole line is trimmed, and then visibly.
//
// width is the pane's body width; 0 means it is not known yet, which — as
// everywhere else in this file — means "do not truncate". Every returned line
// carries `indent`, so the block reads as part of the row above it.
func jdeWrapTokens(tokens []jdeToken, indent string, width int) []string {
	if len(tokens) == 0 {
		return nil
	}
	avail := 0
	if width > 0 {
		if avail = width - lipgloss.Width(indent); avail < 1 {
			avail = 1
		}
	}
	sepW := lipgloss.Width(jdeTokenSep)

	var lines []string
	line, lineW := "", 0
	for _, tok := range tokens {
		if avail > 0 {
			tok.text = fitCell(tok.text, avail)
		}
		w := lipgloss.Width(tok.text)
		switch {
		case line == "":
			line, lineW = tok.render(), w
		case avail > 0 && lineW+sepW+w > avail:
			lines = append(lines, indent+line)
			line, lineW = tok.render(), w
		default:
			line += StyleMuted.Render(jdeTokenSep) + tok.render()
			lineW += sepW + w
		}
	}
	if line != "" {
		lines = append(lines, indent+line)
	}
	return lines
}

// jdeOptionStrip lists a choice row's whole option set with the current one
// bracketed: the line a form draws under the FOCUSED choice row so a short fixed
// list is never cycled blind. A two-value set gets nothing — "< Yes >" already
// says what the other value is, and the empty string is the caller's signal to
// draw no line at all.
//
// width (0 for none) is what the strip has left on the row. A set that does not
// fit is WINDOWED around the current entry rather than clipped at the tail — see
// jdeStripWindow.
func jdeOptionStrip(labels []string, idx, width int) string {
	if len(labels) < 3 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for i, l := range labels {
		if i == idx {
			parts = append(parts, "["+l+"]")
			continue
		}
		parts = append(parts, l)
	}
	strip := strings.Join(parts, jdeStripSep)
	if width <= 0 || lipgloss.Width(strip) <= width {
		return strip
	}
	return jdeStripWindow(parts, idx, width)
}

// jdeStripSep separates the entries of an option strip.
const jdeStripSep = " · "

// jdeStripWindow is the strip for a set too wide for the row: it grows outward
// from the SELECTED entry and marks each dropped end with an ellipsis.
//
// Clipping the tail instead — which is what this did first — cuts the bracketed
// entry off entirely once the cursor is past the first few options, and a strip
// that cannot show you where you are is worse than no strip at all (a device
// type cycles nineteen codes; sweep B is what found this).
func jdeStripWindow(parts []string, idx, width int) string {
	if idx < 0 || idx >= len(parts) {
		idx = 0
	}
	render := func(lo, hi int) string {
		s := strings.Join(parts[lo:hi+1], jdeStripSep)
		if lo > 0 {
			s = "… " + s
		}
		if hi < len(parts)-1 {
			s += " …"
		}
		return s
	}
	lo, hi := idx, idx
	out := render(lo, hi)
	if lipgloss.Width(out) > width {
		// The selected entry alone overflows: all that fits is the admission
		// that it does.
		if width < 2 {
			return ""
		}
		return truncateVisible(out, width-1) + "…"
	}
	for lo > 0 || hi < len(parts)-1 {
		grew := false
		if hi < len(parts)-1 {
			if next := render(lo, hi+1); lipgloss.Width(next) <= width {
				hi, out, grew = hi+1, next, true
			}
		}
		if lo > 0 {
			if next := render(lo-1, hi); lipgloss.Width(next) <= width {
				lo, out, grew = lo-1, next, true
			}
		}
		if !grew {
			break
		}
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

// AddFields appends one navigable row per field, numbered from rowBase — the
// shape of every columnar form whose rows are simply its fields. A form that has
// to interleave something (an option strip under the focused row, a derived
// preview under the one it is derived from) adds those lines itself.
func (l *jdeLines) AddFields(fields []jdeField, labelWidth, rowBase int) {
	for i, f := range fields {
		l.AddRow(rowBase+i, renderJDEField(f, labelWidth))
	}
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
// The pane a columnar screen renders into
// ---------------------------------------------------------------------------

// jdeScreen is the pane geometry every columnar screen shares: how tall the
// scrollable body may be, how wide the bar's rule is drawn, and the framing that
// puts a body, a status line and the bar together so the bar lands on the SAME
// two rows on every frame. Screens embed it, so `s.terminalHeight` and
// `s.frame(…)` read exactly as they did when the pilot carried its own copy —
// sc-h412 wrote one for PO edit, sc-dnhx made it the shared one rather than the
// sixth copy of the same fifteen lines.
type jdeScreen struct {
	terminalHeight int
	terminalWidth  int
}

// setSize records a WindowSizeMsg; every columnar screen calls this from Update.
func (g *jdeScreen) setSize(m tea.WindowSizeMsg) {
	g.terminalHeight, g.terminalWidth = m.Height, m.Width
}

// bodyRows is the height the scrollable body gets. Zero means "not known yet":
// before the first WindowSizeMsg there is no budget to window against, so the
// body renders whole and Root's clampToBox decides what fits — the same thing
// every unsized screen in the app does.
func (g jdeScreen) bodyRows() int {
	if g.terminalHeight <= 0 {
		return 0
	}
	return screenBodyHeightWithActionBar(g.terminalHeight)
}

// bodyWidth is the columns the body has, or 0 when the width is not known yet —
// which callers read as "do not truncate", the same way bodyRows()==0 means "do
// not window".
func (g jdeScreen) bodyWidth() int {
	if g.terminalWidth <= 0 {
		return 0
	}
	return screenBodyWidth(g.terminalWidth)
}

// barWidth is how wide the action bar's rule is drawn. The fallback matches an
// ordinary 100-column terminal, which is what an unsized screen is most likely
// about to become.
func (g jdeScreen) barWidth() int {
	if g.terminalWidth <= 0 {
		return 72
	}
	return screenBodyWidth(g.terminalWidth)
}

// windowRows is how many navigable rows the pane is currently showing —
// computed from the same lines View draws, so a page moves by exactly what the
// operator can see rather than by a guessed constant. headerRows is what a
// pinned header costs the body (0 when there is none). Never less than one.
func (g jdeScreen) windowRows(body *jdeLines, cursorRow, headerRows int) int {
	_, rows := body.Window(cursorRow, g.bodyRows()-headerRows)
	if rows < 1 {
		return 1
	}
	return rows
}

// jdePageCursor moves a cursor one page in `dir`. It CLAMPS where field nav
// wraps: paging is a way of covering ground in a body taller than the pane, and
// a page that jumped from the last row back to the first would lose the
// operator's place rather than save them keystrokes.
func jdePageCursor(cursor, count, step, dir int) int {
	if count <= 0 {
		return 0
	}
	next := cursor + dir*step
	if next < 0 {
		next = 0
	}
	if next > count-1 {
		next = count - 1
	}
	return next
}

// jdeClampPick clamps an option cursor into [0,count). A picker list is a set
// of choices, not a ring: running off the bottom must not reappear at the
// "(none)" row that CLEARS the field, which is what a wrap would do. count==0
// (nothing matched the filter) rests at 0, which is where a cursor with nothing
// to point at belongs.
func jdeClampPick(next, count int) int {
	if next < 0 || count <= 0 {
		return 0
	}
	if next > count-1 {
		return count - 1
	}
	return next
}

// jdeStatusLine is the one row above the bar: what is in flight, or what went
// wrong. A screen renders it on every frame, blank included, so the bar
// underneath never moves between frames.
func jdeStatusLine(saving bool, verb, errMsg string) string {
	switch {
	case saving:
		return StyleMuted.Render(verb)
	case errMsg != "":
		return StyleStatusError.Render("✗ " + errMsg)
	}
	return ""
}

// frame assembles a phase: the windowed body, padded out to the pane's budget,
// then the status line, then the persistent action bar at the bottom.
func (g jdeScreen) frame(body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	return g.frameWithHeader(nil, body, cursorRow, status, items)
}

// frameWithHeader is frame with lines PINNED above the scrollable body — a
// picker's filter box, the record a sub-form is amending. They cost the body its
// height and are drawn on every frame, so what the operator typed into the
// filter cannot scroll away under a long list.
func (g jdeScreen) frameWithHeader(header []string, body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	out := append([]string{}, header...)
	if budget := g.bodyRows(); budget > 0 {
		avail := budget - len(header)
		if avail < 1 {
			// A header taller than the whole pane still has to leave the cursor's
			// row somewhere to be drawn; the clamp below trims what is left over.
			avail = 1
		}
		lines, _ := body.Window(cursorRow, avail)
		out = append(out, lines...)
		for len(out) < budget {
			out = append(out, "")
		}
		if len(out) > budget {
			out = out[:budget]
		}
	} else {
		out = append(out, body.text...)
	}
	out = append(out, status)
	return strings.Join(out, "\n") + "\n" + renderActionBar(g.barWidth(), items)
}

// ---------------------------------------------------------------------------
// The "filter and choose one" sub-phase
// ---------------------------------------------------------------------------

// jdePickAction is what a key means inside a picker.
type jdePickAction int

const (
	// jdePickType is everything else: it goes into the filter box.
	jdePickType jdePickAction = iota
	jdePickCancel
	jdePickCommit
	jdePickMove
	jdePickPage
)

// jdePickKey classifies a key inside a picker, and says how far to move.
//
// The filter is ALWAYS live — there is no "press / to search" mode, because a
// mode is one more thing the operator has to know and the bar has nowhere to say
// it. Typing filters, the arrows move, enter takes what is highlighted, esc
// leaves without choosing. That also retires the j/k the pickers used to carry,
// which were letter accelerators wearing a vim hat.
func jdePickKey(m tea.KeyMsg) (jdePickAction, int) {
	switch m.String() {
	case "esc":
		return jdePickCancel, 0
	case "enter":
		return jdePickCommit, 0
	case "down", "tab":
		return jdePickMove, +1
	case "up", "shift+tab":
		return jdePickMove, -1
	case "pgdown":
		return jdePickPage, +1
	case "pgup":
		return jdePickPage, -1
	}
	return jdePickType, 0
}

// jdePickList is one picker, described without this file having to know what is
// being picked. Count/Label/Dim stand in for the caller's own option slice, the
// way ReportTableScreen's loaders stand in for its rows.
type jdePickList struct {
	// Title names what is being picked; For (optional) names what for.
	Title, For string
	// Note is a muted line under the title: what the "(none)" row does, why a
	// list is short.
	Note string
	// Filter is the always-live filter box. It carries the caret, so it renders
	// focused whatever the cursor is doing in the list below it.
	Filter textinput.Model
	Count  int
	Label  func(i int) string
	// Dim marks a row that is an empty state rather than a value — the synthetic
	// "(none)" row. nil means none of them are.
	Dim    func(i int) bool
	Cursor int
	// Empty is the line drawn instead of the list when nothing matches.
	Empty string
}

// render returns the pinned header and the scrollable body, ready for
// frameWithHeader. Each option line is tagged with its own index, so the frame
// windows the list around the selection with no second windowing pass.
func (p jdePickList) render() ([]string, *jdeLines) {
	head := StyleJDEHeading.Render(p.Title)
	if p.For != "" {
		head += "  " + StyleMuted.Render("for ") + p.For
	}
	// The box's own placeholder is dropped: the label says "Filter" and the hint
	// says what typing does, and a placeholder filling the input area would hide
	// the underscores that say it is empty. textinput.Model is a value type, so
	// this copy leaves the caller's box alone.
	box := p.Filter
	box.Placeholder = ""
	filter := jdeField{
		Label:   "Filter",
		Kind:    jdeText,
		Value:   jdeInputValue(box, true),
		Width:   30,
		Hint:    "type to narrow the list",
		Focused: true,
	}
	header := []string{head, renderJDEFields([]jdeField{filter})[0], ""}
	if p.Note != "" {
		header = append(header, jdeIndent+StyleMuted.Render(p.Note), "")
	}

	body := &jdeLines{}
	if p.Count == 0 {
		empty := p.Empty
		if empty == "" {
			empty = "(no matches)"
		}
		body.Add(jdeIndent + StyleMuted.Render(empty))
		return header, body
	}
	for i := 0; i < p.Count; i++ {
		label := p.Label(i)
		if i == p.Cursor {
			body.AddRow(i, StyleSidebarItemActive.Render("  ▸ "+label))
			continue
		}
		if p.Dim != nil && p.Dim(i) {
			label = StyleMuted.Render(label)
		}
		body.AddRow(i, "    "+label)
	}
	return header, body
}

// jdePickBar is the bar every picker draws: the same four keys, plus paging when
// the list is longer than the pane.
func jdePickBar(verb string, paging bool) []actionBarItem {
	return jdePickBarWith(verb, "Cancel", paging)
}

// jdePickBarWith is jdePickBar for a picker whose Esc does not mean "cancel".
// A MULTI picker applies each toggle as it is made, so there is nothing left to
// undo and leaving IS the commit — the bar has to say "Done" or it is lying
// about what the key does. (Enter toggles there, because the always-live filter
// owns space; sweep B's required-certifications row is the first of these.)
func jdePickBarWith(commit, cancel string, paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", commit}, {"Esc", cancel}, {"UP/DN", "Move"}}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
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

// ---------------------------------------------------------------------------
// A bar with more keys than one line holds
// ---------------------------------------------------------------------------
//
// renderActionBar above puts every item on ONE line and tightens the gutter
// until they fit. That is the whole story for a form: the pilot's bar names
// four or five keys and the widest of them still fits the 49 columns an
// 80-column terminal leaves the pane.
//
// A VIEWING screen is the case it does not cover. A purchase order's detail
// (po_detail.go) carries a dozen order-level commands at once — receive, edit,
// attachments, ship, send, confirm, deliver, void, order pad, refresh, plus
// scrolling — and no tightening puts twelve of them on one 49-column line. The
// line simply ran off the end of the pane and clampToBox cut it, which is the
// exact failure renderActionBar's gutter loop exists to prevent: "a key that
// fell off the bar is a key the operator cannot discover".
//
// So the bar WRAPS instead. That is also what the real thing does — a JD
// Edwards World screen carries two or three rows of F-key legend under the
// rule, not one — and it costs the body only the rows the keys actually need.
// renderActionBar is left exactly as it was, so no form that already fits
// changes by a single column.

// actionBarKeyLines lays the bar's items out over as many lines as it takes,
// each already carrying jdeIndent. One line is returned whenever the items fit
// on one, by the same tighten-the-gutter rule renderActionBar uses — so a
// screen that swaps to this bar and has few enough keys renders identically.
//
// Width accounting is done on the PLAIN "Key=Label" text and each item is
// rendered whole afterwards, for the reason jdeWrapTokens does the same: a line
// measured on styled text can be cut through an escape sequence.
func actionBarKeyLines(width int, items []actionBarItem) []string {
	if width < 8 {
		width = 8
	}
	type part struct {
		text string
		w    int
	}
	parts := make([]part, 0, len(items))
	for _, it := range items {
		if it.Key == "" {
			continue
		}
		p := part{text: StyleActionBarKey.Render(it.Key), w: len(it.Key)}
		if it.Label != "" {
			p.text += StyleActionBar.Render("=" + it.Label)
			p.w += 1 + len(it.Label)
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return []string{jdeIndent}
	}

	avail := width - len(jdeIndent)
	if avail < 1 {
		avail = 1
	}
	// One line if they fit on one, gutter tightening first — identical output to
	// renderActionBar for every bar that was already legible.
	for _, gutter := range []string{"   ", "  ", " "} {
		total := 0
		for i, p := range parts {
			if i > 0 {
				total += len(gutter)
			}
			total += p.w
		}
		if total <= avail {
			texts := make([]string, len(parts))
			for i, p := range parts {
				texts[i] = p.text
			}
			return []string{jdeIndent + strings.Join(texts, gutter)}
		}
	}

	// Wrapping. The tight gutter is kept from here on: the point of the extra
	// lines is to fit the keys, not to space them out.
	const gutter = "  "
	var (
		out  []string
		line string
		lw   int
	)
	flush := func() {
		if line != "" {
			out = append(out, jdeIndent+line)
			line, lw = "", 0
		}
	}
	for _, p := range parts {
		switch {
		case line == "":
			// An item wider than the whole line still goes out whole: a key the
			// operator cannot discover is worse than a row that runs long, and
			// clampToBox will cut only the tail of the label.
			line, lw = p.text, p.w
		case lw+len(gutter)+p.w <= avail:
			line += gutter + p.text
			lw += len(gutter) + p.w
		default:
			flush()
			line, lw = p.text, p.w
		}
	}
	flush()
	return out
}

// actionBarRowsFor is how tall renderActionBarWrapped will draw: the rule plus
// however many key lines the items need. A screen budgets its body against this
// rather than against the actionBarRows constant, or the last key line is the
// row clampToBox eats.
func actionBarRowsFor(width int, items []actionBarItem) int {
	return 1 + len(actionBarKeyLines(width, items))
}

// renderActionBarWrapped is renderActionBar for a screen whose keys need more
// than one line: the rule, then every key line.
func renderActionBarWrapped(width int, items []actionBarItem) string {
	if width < 8 {
		width = 8
	}
	return StyleActionBarRule.Render(strings.Repeat("-", width)) + "\n" +
		strings.Join(actionBarKeyLines(width, items), "\n")
}

// ---------------------------------------------------------------------------
// A read-only body the operator scrolls
// ---------------------------------------------------------------------------

// WindowFrom is Window for a body with no cursor in it — a read-only detail
// sheet, where what has to stay on screen is wherever the operator scrolled to
// rather than a row they are standing on. It returns EXACTLY `avail` lines (or
// every line, when they all fit) starting at `offset`, spending the first and
// last on the same "more above / more below" indicators Window uses so the
// count — and therefore where the action bar lands — never moves with the
// scroll position.
func (l *jdeLines) WindowFrom(offset, avail int) []string {
	n := len(l.text)
	if avail <= 0 {
		return nil
	}
	if n <= avail {
		out := make([]string, n, avail)
		copy(out, l.text)
		return out
	}
	if avail <= 2 {
		// No room for indicators: show what is under the offset and nothing else.
		start := l.ClampScroll(offset, avail)
		return append([]string(nil), l.text[start:start+avail]...)
	}

	body := avail - 2
	start := l.ClampScroll(offset, avail)
	end := start + body
	if end > n {
		end = n
	}

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
	return out
}

// ClampScroll brings a scroll offset back inside the body, given the pane
// height WindowFrom will be called with. Screens call it after every scroll key
// so the offset they hold and the one that gets drawn are never different —
// which is what makes "↓ 0 more below" impossible.
func (l *jdeLines) ClampScroll(offset, avail int) int {
	if offset < 0 {
		return 0
	}
	body := avail
	if avail > 2 {
		body = avail - 2
	}
	if max := len(l.text) - body; offset > max {
		if max < 0 {
			return 0
		}
		return max
	}
	return offset
}

// ---------------------------------------------------------------------------
// Frames for a bar that may need more than one line
// ---------------------------------------------------------------------------

// bodyRowsForBar is bodyRows for a bar of a known height: the pane, less the
// bar and the one status row above it. Zero means "not known yet", exactly as
// bodyRows does.
func (g jdeScreen) bodyRowsForBar(barRows int) int {
	if g.terminalHeight <= 0 {
		return 0
	}
	h := screenBodyHeight(g.terminalHeight) - barRows - 1
	if h < 3 {
		return 3
	}
	return h
}

// frameWrapped is frameWithHeader for a screen whose bar may wrap: the body is
// padded out to whatever the wrapped bar leaves it, so the bar still lands on
// the same rows of the pane on every frame.
func (g jdeScreen) frameWrapped(header []string, body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	barRows := actionBarRowsFor(g.barWidth(), items)
	out := append([]string{}, header...)
	if budget := g.bodyRowsForBar(barRows); budget > 0 {
		avail := budget - len(header)
		if avail < 1 {
			avail = 1
		}
		lines, _ := body.Window(cursorRow, avail)
		out = append(out, lines...)
		out = jdePadTo(out, budget)
	} else {
		out = append(out, body.text...)
	}
	out = append(out, status)
	return strings.Join(out, "\n") + "\n" + renderActionBarWrapped(g.barWidth(), items)
}

// frameScrolled is frameWrapped for a read-only body: the window is positioned
// by the operator's scroll offset rather than by a cursor row. It returns the
// clamped offset alongside the frame, so a screen that scrolled past the end
// stores back the offset that was actually drawn.
func (g jdeScreen) frameScrolled(header []string, body *jdeLines, offset int, status string, items []actionBarItem) (string, int) {
	barRows := actionBarRowsFor(g.barWidth(), items)
	out := append([]string{}, header...)
	budget := g.bodyRowsForBar(barRows)
	if budget > 0 {
		avail := budget - len(header)
		if avail < 1 {
			avail = 1
		}
		offset = body.ClampScroll(offset, avail)
		out = append(out, body.WindowFrom(offset, avail)...)
		out = jdePadTo(out, budget)
	} else {
		offset = 0
		out = append(out, body.text...)
	}
	out = append(out, status)
	return strings.Join(out, "\n") + "\n" + renderActionBarWrapped(g.barWidth(), items), offset
}

// scrollRows is how many lines a read-only body scrolls per page — one paneful
// less a line of overlap for the reader's eye, the same step TextScroller used
// before these screens moved onto the columnar layer. Never less than one.
func (g jdeScreen) scrollRows(barRows, headerRows int) int {
	avail := g.bodyRowsForBar(barRows) - headerRows
	if avail > 2 {
		avail -= 2 // the two indicator rows WindowFrom reserves
	}
	if step := avail - 1; step > 1 {
		return step
	}
	return 1
}

// jdePadTo pads (or trims) a frame's lines to exactly n rows. The bar underneath
// has to sit on the same row every frame, and a body that grew and shrank with
// its content would walk it up and down the pane.
func jdePadTo(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}
	if len(lines) > n {
		lines = lines[:n]
	}
	return lines
}

// ---------------------------------------------------------------------------
// Fitting a row to the pane
// ---------------------------------------------------------------------------

// jdeMinFieldWidth is the narrowest input area worth drawing. Below it the
// underscored run stops reading as a field at all, so the HINT gives way first.
const jdeMinFieldWidth = 10

// jdeFitRow sizes one text row to the pane, and returns any lines that have to
// be drawn under it.
//
// A columnar row is label + leader + input area + hint, and at 80 columns the
// pane is 51 wide — so a 34-column notes field with an "optional" beside it is
// already 15 columns over. clampToBox does not wrap: it cuts the tail, which is
// the HINT, so the row loses the only thing on it that said what to type.
//
// Two things give way, in this order:
//
//	the input area shrinks   — a shorter field still takes the whole value; a
//	                           bubbles textinput scrolls what does not fit.
//	the hint moves under it  — once the field is down to jdeMinFieldWidth, the
//	                           hint becomes a note line indented to the input
//	                           area (jdeNoteLines), which is the fold this layer
//	                           already uses for a note too long to ride along.
//
// bodyWidth of 0 means the pane is not sized yet, which — as everywhere in this
// file — means "do not truncate".
func jdeFitRow(f jdeField, labelWidth, bodyWidth int) (jdeField, []string) {
	if bodyWidth <= 0 || f.Kind != jdeText {
		return f, nil
	}
	lead := len(jdeIndent) + labelWidth + len(jdeLeader)
	want := f.Width
	if want <= 0 {
		want = jdeFieldWidth
	}
	hintCost := 0
	if f.Hint != "" {
		hintCost = 2 + lipgloss.Width(f.Hint)
	}
	if avail := bodyWidth - lead - hintCost; avail >= jdeMinFieldWidth || f.Hint == "" {
		if avail < 1 {
			avail = 1
		}
		if want > avail {
			want = avail
		}
		f.Width = want
		return f, nil
	}

	note := f.Hint
	f.Hint = ""
	avail := bodyWidth - lead
	if avail < 1 {
		avail = 1
	}
	if want > avail {
		want = avail
	}
	f.Width = want
	return f, jdeNoteLines(note, labelWidth, bodyWidth)
}

// AddFittedFields appends one navigable row per field, numbered from rowBase,
// each sized to the pane by jdeFitRow — and any hint that had to be folded is
// added as further lines of the SAME row, so the window keeps a field and the
// note explaining it on screen together.
func (l *jdeLines) AddFittedFields(fields []jdeField, labelWidth, bodyWidth, rowBase int) {
	for i, f := range fields {
		fitted, notes := jdeFitRow(f, labelWidth, bodyWidth)
		l.AddRow(rowBase+i, renderJDEField(fitted, labelWidth))
		for _, note := range notes {
			l.AddRow(rowBase+i, note)
		}
	}
}

// jdeCaveatLines is a standing note that belongs to the SHEET rather than to a
// field — what a phase does, what it cannot undo. jdeNoteLines indents to the
// input area because the note it draws is ABOUT the row above it; a caveat
// about the whole sheet has no such row, and indenting it to a leader column it
// is not hanging off reads as a value with a missing label.
//
// bodyWidth of 0 means the pane is not sized yet, which — as everywhere in this
// file — means "do not truncate".
func jdeCaveatLines(note string, bodyWidth int) []string {
	width := 0
	if bodyWidth > 0 {
		if width = bodyWidth - len(jdeIndent); width < 1 {
			width = 1
		}
	}
	wrapped := jdeWrapNote(note, width)
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	return out
}
