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
