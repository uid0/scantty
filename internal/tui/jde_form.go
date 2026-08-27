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
//	                             happened to end"). Scrolls() is that same
//	                             short-circuit asked as a question, so a bar
//	                             and a window cannot disagree about it.
//	renderActionBar            — the bar itself.
//	jdeScreen                  — the pane geometry and the framing that puts a
//	                             body, a status line and the bar together.
//	                             bodyAvail / bodyAvailForBar are the ONE answer
//	                             to how many rows a body gets, bodyScrolls /
//	                             bodyScrollsForBar the one answer to whether it
//	                             moves, and statusRow the only way to draw the
//	                             row above the bar — a sheet that could build
//	                             any of the three itself would build it wrong
//	                             eventually, and did (sc-jde-lift).
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
	// jdeText is a bubbles textinput, and the row hands this layer the BOX
	// itself in Input — never a string it rendered on its own. The layer calls
	// View() (jdeFitInputValue), because the typed value and the underscored
	// fill after it are two halves of ONE width and only jdeFieldArea knows
	// that width: a caller that pre-renders its own value leaves the layer able
	// to measure the result but not to bound it, which is how a long typed
	// value used to walk out of the pane with the caret behind it (sc-jde-tiw).
	// jdeFitInputValue sizes a COPY of the box, so this stays a pure render;
	// see the note on jdeField.Input. It is the ZERO VALUE of this type, so a
	// row that names no Kind is a text row too.
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
	// Value is plain text for jdeChoice / jdeValue. A jdeText row carries its
	// box in Input instead and leaves this empty — see the note there.
	Value string
	// Input is the bubbles box a jdeText row is typed into, and is how a text
	// row is meant to be built: hand the LAYER the box rather than the string
	// it renders to.
	//
	// It is a pointer only so that "no box" is expressible; nothing here ever
	// writes through it. jdeFitInputValue takes a COPY and sizes that, because
	// sizing is a property of the row being drawn, not of the operator's box:
	// two frames at two terminal widths must not leave the box in different
	// states, and a View() that mutated the model would be a side effect in the
	// one place bubbletea guarantees there is none.
	//
	// A caller that sets Value on a jdeText row instead is drawing its own
	// unbounded string, which is the defect this layer now owns (sc-jde-tiw):
	// jdeFitRow sized the underscored FILL and nobody sized the box, so a long
	// typed value walked straight out of the pane with the caret behind it.
	// Bounding lives here, once, so that every converted sheet gets it — and
	// so that the four purchasing sheets could delete the local copy of it that
	// had already grown two workarounds of its own.
	Input *textinput.Model
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
//
// bodyWidth is the pane the row is being drawn into, and is not optional
// decoration: it is what stops a text row from running past the edge of the
// screen. 0 means "the pane is not sized yet", which — as everywhere in this
// file — means "do not truncate". It is a parameter rather than a field on
// jdeField so that the compiler asks every sheet for it; the fill this layer
// draws was sized against a pane nobody had told it about for as long as it was
// possible not to (sc-jde-tiw).
func renderJDEField(f jdeField, labelWidth, bodyWidth int) string {
	f.Width = jdePaneFieldWidth(f, labelWidth, bodyWidth)
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

// jdePaneFieldWidth is a text row's input area, capped at what is left of the
// pane after the indent, the label column and the leader.
//
// It is the floor under the whole sizing rule, and it is deliberately separate
// from jdeFitRow. jdeFitRow is a LAYOUT decision a sheet opts into: it trades
// the field against the hint and folds the hint underneath when neither fits.
// This is not a decision at all — it is the edge of the screen.
//
// Bounding the BOX to the row is not enough on its own, which is what the first
// run at this bead got wrong. A sheet that declares a 40-column field behind a
// 30-column label has 21 columns at 80, so a box sized to 40 still hands back 40
// and clampToBox drops the other 19 — and what is drawn last, and therefore lost
// first, is the caret at the end of the value. That is the defect exactly: the
// operator is typing into a field whose cursor is not on the screen. Every
// unfitted sheet in the package had it, po_edit and the inventory forms
// included, and it is why this cap is applied on the way into every row rather
// than left to the sheets that had opted into jdeFitRow.
//
// Nothing on screen moves because of it. The fill it shortens is fill clampToBox
// was cutting anyway, and a hint sitting past the fill is still past the pane
// afterwards — which is exactly why the fold is jdeFitRow's job and not this
// one's.
//
// Only a text row has an input area to cap. A jdeValue or jdeChoice row draws
// its value at whatever width the value is, and shortening THAT is a content
// decision each sheet already makes for itself (poFitLineGrid, fitCellIf).
func jdePaneFieldWidth(f jdeField, labelWidth, bodyWidth int) int {
	width := f.Width
	if f.Kind != jdeText || bodyWidth <= 0 {
		return width
	}
	if width <= 0 {
		width = jdeFieldWidth
	}
	avail := bodyWidth - (len(jdeIndent) + labelWidth + len(jdeLeader))
	if avail < 1 {
		avail = 1
	}
	if width > avail {
		width = avail
	}
	return width
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
		// A text row: the box has drawn the value and its own cursor, so all
		// that is left is to fill the field out to its width. Unfocused that
		// fill is the underscored run of a green-screen form; focused it is a
		// solid reverse-video field, which is what makes the row the operator is
		// standing in unmistakable without moving any columns.
		//
		// The value is rendered HERE rather than by the caller because the fill
		// and the value are two halves of one width, and only this line knows
		// what that width is. jdeFitInputValue is what keeps the value from
		// eating the fill: a box left to its own devices renders its whole
		// value, so a long one used to leave fill 0 (no highlight) and a row
		// several columns past the pane (no visible caret).
		width := f.Width
		if width <= 0 {
			width = jdeFieldWidth
		}
		value := f.Value
		if f.Input != nil {
			value = jdeFitInputValue(*f.Input, f.Focused, width)
		}
		fill := width - lipgloss.Width(value)
		if fill < 0 {
			fill = 0
		}
		if f.Focused {
			return value + StyleJDEFieldFocused.Render(strings.Repeat(" ", fill))
		}
		return value + StyleJDEInput.Render(strings.Repeat("_", fill))
	}
}

// jdeFitInputValue renders one bubbles box as the string a text row hangs off,
// bounded to the input area the row was given. It is the whole of the sizing
// rule for typed values, and it applies to EVERY text row on every converted
// sheet — there is no second copy of it (sc-jde-tiw closed the one there was).
//
// The rule is one sentence: what a text row draws never exceeds its input area,
// caret included. Three separate faults hid behind that sentence, and each is
// answered by a specific line below.
//
//  1. THE BOX HAS NO VIEWPORT AT Width 0. bubbles v1.0.0 returns early from
//     handleOverflow when Width <= 0, so View() draws the ENTIRE value: a
//     60-column path typed into a 34-column field rendered 60 columns, the row
//     ran past the pane, clampToBox cut it, and the caret was off-screen — the
//     operator typing blind. Giving the box a Width gives it back the scrolling
//     window that keeps the caret in view.
//
//  2. THE CARET SITS ONE CELL PAST Width. A focused box renders Width+1
//     columns: the pad loop tops the value up to Width and the cursor takes a
//     cell of its own after it (or the pad gains one when the cursor is inside
//     the value). So the box is asked for one column LESS than the row has.
//
//  3. A BOUNDED BOX PADS, AND THE PAD IS THE FILL. bubbles pads the value out
//     to Width itself whenever the value fits, which left jdeFieldArea nothing
//     to draw — fill 0, no reverse-video block, and no way to see which row the
//     cursor was on. The local fix for that was to hand the box
//     StyleJDEFieldFocused as its TextStyle, which bubbles then applied to the
//     VALUE RUNES too, so a focused row drew its typed value fully reversed
//     where the pilot draws it plain. Both are avoided by not letting the box
//     pad at all: a value that FITS is drawn by an unbounded box (Width 0),
//     which is exactly what the pilot has always done, and the fill is left to
//     jdeFieldArea. A value that does NOT fit fills the area by itself, so
//     there is no fill to lose. Either way the value is plain text and only the
//     fill is reversed.
//
// The re-seat is not optional. bubbles computes its scrolling window in
// SetCursor, not in View, and handleOverflow only recomputes when the cursor has
// fallen OUTSIDE the current window — so a Width imposed on a box whose window
// still spans the whole value changes nothing on screen. CursorEnd() forces the
// recompute (the cursor is then past the right edge by construction) and
// SetCursor puts it back.
//
// A blurred row is bounded HERE and not by the box, because jdeInputValue hands
// back the raw value for one — jdeEchoValue reads it out directly so a masked
// field cannot give up its mask — and a raw value has had no window applied to
// it. fitCell rather than a silent cut: an operator proof-reading a path before
// pressing Enter must not be shown a shortened one that reads whole.
//
// Anything carrying an escape sequence is returned UNCUT. Cutting rendered text
// by runes drops the trailing SGR reset or lands inside a sequence, and what the
// terminal then draws is whatever state the cut left it in rather than a merely
// shortened row. What bounds those is the Width imposed before bubbles rendered
// anything, which is the only place a bound on styled output belongs.
//
// The one case it cannot hold is an input area of 1 column or less, where a
// focused box still costs 2 (a rune and its caret). That is a pane narrower than
// its own label column, and a one-column overrun is the least of what is wrong
// with it.
func jdeFitInputValue(ti textinput.Model, focused bool, area int) string {
	if area <= 0 {
		area = jdeFieldWidth
	}
	// The prompt is drawn INSIDE the box and outside its Width accounting, so
	// it comes off the budget here. Every box on these sheets clears it; the
	// arithmetic does not depend on that staying true.
	fits := area - 1 - lipgloss.Width(ti.Prompt)

	// What View() will actually draw. An empty box with a placeholder draws the
	// PLACEHOLDER — the one string on a text row that is not the value, and one
	// that is routinely longer than the field it is offered in.
	drawn := ti.Value()
	if drawn == "" && ti.Placeholder != "" {
		drawn = ti.Placeholder
	}
	if lipgloss.Width(drawn) <= fits {
		// It fits: leave the viewport OFF, which is what the pilot has always
		// done and what leaves bubbles nothing to pad — so the fill, and with it
		// the highlight, stays jdeFieldArea's to draw.
		ti.Width = 0
	} else {
		ti.Width = fits
		if ti.Width < 1 {
			ti.Width = 1
		}
	}
	pos := ti.Position()
	ti.CursorEnd()
	ti.SetCursor(pos)

	value := jdeInputValue(ti, focused)
	if strings.ContainsRune(value, '\x1b') {
		return value
	}
	return fitCell(value, area)
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

// jdeInputValue is what a jdeText row draws for a bubbles textinput: the live
// View() when the row has focus — it draws the cursor — and the plain value when
// it does not. A blurred textinput still renders a cursor cell, and in a
// columnar form that reads as a stray gap between the value and the underscores
// filling the rest of the field. A field showing its placeholder keeps View(),
// which is the only thing that renders one.
//
// It is the UNBOUNDED half of the job and is not what a screen should call:
// jdeFitInputValue wraps it with the sizing, and a row hands the layer its box
// through jdeField.Input.
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
func renderJDEFields(fields []jdeField, bodyWidth int) []string {
	w := jdeLabelWidth(fields)
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = renderJDEField(f, w, bodyWidth)
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
// STRUCT (sc-lmsi) — and, since jde_cells_test.go, on the rendered frame as
// well: lipgloss renders flat in a test binary only until the profile is
// forced.
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
//
// A rowBase of jdeNoRow means the whole band is read-only, so EVERY line of it
// is tagged jdeNoRow rather than -1, 0, 1… — see jdeRowAt.
func (l *jdeLines) AddFields(fields []jdeField, labelWidth, bodyWidth, rowBase int) {
	for i, f := range fields {
		l.AddRow(jdeRowAt(rowBase, i), renderJDEField(f, labelWidth, bodyWidth))
	}
}

// jdeRowAt numbers the i-th field of a band based at rowBase.
//
// The jdeNoRow case is the one that matters and is why this is a function: a
// band of read-only lines is added with rowBase == jdeNoRow, and rowBase+i
// there exempts only the FIRST line while handing the second row 0, the third
// row 1 and so on — the numbers the sheet's real navigable rows already own. A
// read-only line sharing a number with an input makes block() span from one to
// the other, so Window anchors on that inflated block and starts the pane on
// the read-only lines, dropping an input row the operator is typing into.
func jdeRowAt(rowBase, i int) int {
	if rowBase == jdeNoRow {
		return jdeNoRow
	}
	return rowBase + i
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

// paneRows is how many rows the content pane really gives this screen, or 0
// when the terminal has not been sized yet.
//
// It is screenBodyRows and not screenBodyHeight on purpose: the columnar frames
// pin an action bar to the BOTTOM of the pane, so they have to fill it exactly,
// and screenBodyHeight's floor of 4 over-reports by up to three rows below a
// terminal height of 10. A frame built to an over-report is a frame clampToBox
// cuts from the bottom, and the bottom is the bar.
//
// jde:layer-only — a sheet asks bodyAvail / bodyAvailForBar.
func (g jdeScreen) paneRows() int {
	if g.terminalHeight <= 0 {
		return 0
	}
	return screenBodyRows(g.terminalHeight)
}

// bodyRows is the height the scrollable body gets under a bar of the ordinary
// fixed height (renderActionBar draws actionBarRows, always). Zero means the
// same two things bodyRowsForBar's zero means, and the frames tell them apart
// the same way.
//
// jde:layer-only — a sheet asks bodyAvail.
func (g jdeScreen) bodyRows() int {
	return g.bodyRowsForBar(actionBarRows)
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

// bodyAvail is the number of lines frame / frameWithHeader will window a body
// into, given a pinned header of headerRows lines.
//
// It exists so that the frame and every question ASKED about the frame read one
// expression. frameWithHeader used to inline this, and each converted sheet then
// restated it to decide whether its bar should name the scroll keys; the
// restatements drifted (see bodyScrollsForBar).
//
// ZERO means "the body gets no rows on this pane", which is also "nothing can
// scroll", and there is now exactly ONE cause of it: an UNSIZED terminal, where
// the frame draws every line and Root's clampToBox decides. A pinned header
// cannot be a cause, because jdeBodyAvail gives the body its row and
// jdeFitHeader trims the header to pay for it; and a pane the frame is REFUSED
// on is not one either, which is the correction that makes the refusal notice
// honest — see jdeTooShortRows.
//
// It used to be able to. An earlier draft clamped to one row here, reasoning
// that the frame still has to leave the cursor's row somewhere to be drawn, and
// that draft was WRONG for the reason the answer is right now: it clamped the
// body's share without moving the header's, so jdePadTo trimmed the assembled
// frame back to the budget and took the windowed line with it — the body was
// not on screen at all, and the order pad's bar named PgUp/PgDn at 80x12 over a
// frame that does not move (TestPOView_ScrollKeysNamedExactlyWhenTheBodyMoves).
// The two halves have to move TOGETHER, which is why they are one pair of
// functions called with one budget rather than a clamp on either side.
func (g jdeScreen) bodyAvail(headerRows int) int {
	return jdeBodyAvail(g.paneRows(), g.bodyRows(), headerRows)
}

// jdeBodyAvail splits a frame's budget between the pinned header and the body,
// and it is where the SACRIFICE ORDER of a short pane is written down:
//
//	the body gives ground first, down to ONE row — and no further.
//	the pinned header gives up whatever that costs.
//	the status row and the action bar never give (see bodyRowsForBar).
//
// The floor of one is the point. A header is drawn ON EVERY FRAME and does not
// move, so a frame that spends the whole budget on it is a frame where nothing
// answers a keypress: the cursor moves, the focused box takes the operator's
// typing, and the pane redraws byte for byte. That is this project's oldest
// reported defect ("it just kinda hangs there") arriving by geometry instead of
// by a silent key arm. The receiving form is where it was measured: its header
// is a one-row note block plus a separator, so at 80x10 and 80x11 the header
// took the budget whole and the frame drew two blank rows over an order with
// three receivable lines on it.
//
// Trimming the header instead loses lines the operator can do without — the
// note has already been read, the record above a sub-form is context — while
// keeping the row they are standing on. It is the same trade the New PO
// chooser makes when it drops its title (po_create.go): give up what names
// nothing before giving up what acts.
//
// THE FLOOR HOLDS WHENEVER THE PANE IS SIZED, including on a pane the frame is
// about to be REFUSED on, and that is why `pane` is an argument rather than
// something inferred from a budget of zero.
//
// Inferring it is what made the refusal notice lie. `budget` arrives as zero
// for two unrelated reasons — an unsized terminal and a pane too short to carry
// the bar (bodyRowsForBar) — and answering zero for the second put the body's
// row back into the header's hands at exactly the heights the frame is refused
// at. Scrolls(0) is false there, so the sheet dropped the scroll keys, so the
// bar the layer was handed lost a row, so jdeTooShortRows named a height one
// row shorter than the one that works: the operator resized to precisely what
// the screen asked for and was refused again. A quantity that changes the thing
// it is measured against is this project's recurring shape, and it is broken
// here the way it is broken everywhere else — by making the reservation
// UNCONDITIONAL.
//
// Zero comes back only when there is NO PANE at all: an unsized terminal, where
// every screen in the app draws whole and clampToBox decides. That means
// "nothing can scroll", which is what bodyScrolls reads it as.
func jdeBodyAvail(pane, budget, headerRows int) int {
	if pane <= 0 {
		return 0
	}
	if avail := budget - headerRows; avail > 1 {
		return avail
	}
	return 1
}

// jdeHeadRank is how expendable one pinned-header row is, and it exists so that
// DISPLAY ORDER and SACRIFICE ORDER can be different things.
//
// They used to be the same thing, because jdeFitHeader trimmed by POSITION.
// That coupling is not a simplification, it is a trap: it forces every builder
// to choose between a header that READS correctly and one that SURVIVES
// correctly, and both screens that hit it chose reading and lost the row that
// mattered. The purchase-order pad drew its "Order pad" heading at 80x12 and
// 80x13 and dropped the ⚠ saying lines had been left out of the pad the
// operator was about to paste — a screen showing wrong data, from a header
// nobody had put in an order. The picker did the same with its filter box, and
// was fixed by INVERTING the display so the box led the block: that worked and
// it was the wrong lever, because it moved the layout of nineteen screens at
// every height to buy a row at one height.
//
// So the layer takes the rank and the builders keep their natural layout: the
// two screens above are the whole justification, and the answer is written once
// here rather than a third time in each of them. The New PO chooser is a
// CONSUMER of it — it arrived carrying a hand-rolled sacrifice order of its own
// and gave it up for this one — not the precedent for it.
//
// A RANK DOES NOT REMOVE THE SIGNIFICANCE OF ORDER WITHIN A RANK. jdeFitHeader
// gives ground from the END within each rank, so two rows that share one are
// still separated by POSITION: whichever a builder emits LAST is the one a
// short pane drops first. Wherever two rows share a rank and it matters which
// survives, that position is a decision and must be written down as one —
// otherwise somebody merging two blocks to save a separator row makes position
// the tiebreak again, which is the exact coupling this type exists to break,
// and it fails silently because a rank was declared for every row.
type jdeHeadRank int

const (
	// jdeHeadEssential is a row the operator would ACT DIFFERENTLY without: the
	// box they are typing into, the warning that what is on screen is
	// incomplete, the screen's answer to a press that declined. Last to go, and
	// a builder may not mark more of them than the smallest drawable budget can
	// hold — TestJDEForm_EveryEssentialHeaderRowIsOnThePane is where that bound
	// is enforced, because an "essential" row that gets dropped anyway is the
	// same false claim in a new place.
	jdeHeadEssential jdeHeadRank = iota
	// jdeHeadContext is a row that helps and does not decide: the supplier and
	// filename over the order pad, the tail of a folded warning, a picker's
	// standing note.
	jdeHeadContext
	// jdeHeadDecorative is a row that names nothing and carries no value — a
	// title, a blank separator. First to go.
	jdeHeadDecorative
)

// jdeHeadRow is one line of a pinned header and how expendable it is.
type jdeHeadRow struct {
	Text string
	Rank jdeHeadRank
}

// jdeHeader is a pinned header: the rows in DISPLAY order, each carrying the
// rank that decides when it is given up. A nil header is a frame with none,
// which is what most sheets pass.
type jdeHeader []jdeHeadRow

// add appends rows at one rank, in display order.
//
// A BLANK row is forced to decorative whatever rank it is added at, because a
// separator must never outlive the row it closes — AGENTS.md's separator rule
// read from the other side. It also means a builder that pads a block out to a
// reserved height (receive_form's noteLines does) does not have to strip the
// padding back off to rank it honestly.
func (h jdeHeader) add(rank jdeHeadRank, lines ...string) jdeHeader {
	for _, line := range lines {
		r := rank
		if strings.TrimSpace(line) == "" {
			r = jdeHeadDecorative
		}
		h = append(h, jdeHeadRow{Text: line, Rank: r})
	}
	return h
}

// addBlock appends a separator and then `lines`, and appends NOTHING when the
// block is empty.
//
// It exists because a builder whose blocks are conditional was writing
//
//	h = h.add(jdeHeadDecorative, "").add(rank, block...)
//
// at every site, and a block that turned out to be empty then left its
// separator behind — a blank row spent on nothing, on a pane where the row
// budget is the thing every other rule in this file is protecting. The
// separator travels with the block it opens, which is AGENTS.md's separator
// rule seen from the one side that needs the emptiness test.
func (h jdeHeader) addBlock(rank jdeHeadRank, lines []string) jdeHeader {
	if len(lines) == 0 {
		return h
	}
	return h.add(jdeHeadDecorative, "").add(rank, lines...)
}

// lines is the header as the frame draws it when nothing has to give.
func (h jdeHeader) lines() []string {
	out := make([]string, 0, len(h))
	for _, row := range h {
		out = append(out, row.Text)
	}
	return out
}

// jdeFitHeader trims a pinned header to the rows left over once the body has
// been given its share, so that header + body is EXACTLY the budget.
//
// It is the other half of jdeBodyAvail and must be read with it: the two are
// called with the same budget and the same header, so the frame cannot window
// the body against one split and draw the header against another.
//
// It gives ground by RANK, most expendable first, and WITHIN a rank from the
// END — so the head of a block still says what it is, and the rows that survive
// are the rows the builder said the operator cannot do without. What comes back
// is in DISPLAY order whatever was dropped out of the middle of it: a header
// that reordered itself as the terminal shrank would be a second layout to
// learn at exactly the sizes nobody looks at.
func jdeFitHeader(header jdeHeader, budget, avail int) []string {
	keep := budget - avail
	if keep < 0 {
		keep = 0
	}
	drop := len(header) - keep
	if drop <= 0 {
		return header.lines()
	}
	kept := make([]bool, len(header))
	for i := range kept {
		kept[i] = true
	}
	for rank := jdeHeadDecorative; rank >= jdeHeadEssential && drop > 0; rank-- {
		for i := len(header) - 1; i >= 0 && drop > 0; i-- {
			if kept[i] && header[i].Rank == rank {
				kept[i] = false
				drop--
			}
		}
	}
	out := make([]string, 0, keep)
	for i, row := range header {
		if kept[i] {
			out = append(out, row.Text)
		}
	}
	return out
}

// bodyAvailForBar is bodyAvail for the WRAPPING frames — frameWrapped and
// frameScrolled — whose bar may take several rows off the pane before the body
// gets any. Zero means the same thing it does there.
//
// `items` must be the bar that is about to be DRAWN. Where the answer feeds a
// bar's own contents (naming the scroll keys costs cells, which can fold the bar
// onto another row, which costs a body row, which can change the answer) the
// caller passes the bar WITH those keys on it: measuring against the tallest bar
// is the fixed point, because a body that overflows the smallest budget also
// overflows the larger one left when the keys are dropped, so the answer cannot
// oscillate between frames.
func (g jdeScreen) bodyAvailForBar(headerRows int, items []actionBarItem) int {
	return jdeBodyAvail(g.paneRows(), g.bodyRowsForBar(actionBarRowsFor(g.barWidth(), items)), headerRows)
}

// bodyScrolls reports whether `body` actually MOVES in the frame that frame /
// frameWithHeader is about to draw. It is the one condition a bar's claim that
// UP/DN, PgUp/PgDn or Home/End do something, and the handler behind those keys,
// both read — so the two cannot drift, and neither can drift from the window
// itself.
//
// See bodyScrollsForBar for why this belongs here rather than on the sheets.
func (g jdeScreen) bodyScrolls(body *jdeLines, headerRows int) bool {
	return body.Scrolls(g.bodyAvail(headerRows))
}

// bodyScrollsForBar is bodyScrolls for the wrapping frames.
//
// This is where a screen asks "does this scroll?", and there is deliberately no
// other way to ask: the arithmetic lived as ~50 per-sheet copies (sc-jde-lift),
// three of them recomputing the budget by hand, and a copy is how the two
// answers part company. TestJDEForm_NoSheetAnswersTheScrollQuestionItself holds
// that door shut by reading the package's own source.
//
// The rule is Window's and WindowFrom's own short-circuit, said once in
// jdeLines.Scrolls: both return the whole body untouched when it fits, ignoring
// the offset or cursor entirely, so the body moves exactly when it has MORE
// lines than the window has rows.
//
// It must NOT be asked of ClampScroll instead, on the theory that reusing the
// frame's arithmetic is safer than restating it. ClampScroll is not the frame's
// gate: it reserves the two indicator rows, so it goes positive from
// `n > avail-2` and disagreed for `n` of exactly avail-1 and avail — a two-row
// window in which the bar named three scroll pairs over a body that could not
// move. Reusing the WRONG expression is not sharing a condition, it is
// duplicating a different one.
func (g jdeScreen) bodyScrollsForBar(body *jdeLines, headerRows int, items []actionBarItem) bool {
	return body.Scrolls(g.bodyAvailForBar(headerRows, items))
}

// windowRows is how many navigable rows the pane is currently showing —
// computed from the same lines View draws, so a page moves by exactly what the
// operator can see rather than by a guessed constant. headerRows is what a
// pinned header costs the body (0 when there is none). Never less than one.
func (g jdeScreen) windowRows(body *jdeLines, cursorRow, headerRows int) int {
	_, rows := body.Window(cursorRow, g.bodyAvail(headerRows))
	if rows < 1 {
		return 1
	}
	return rows
}

// windowRowsForBar is windowRows for the WRAPPING frames — frameWrapped and
// frameScrolled — whose bar may take several rows off the pane before the body
// gets any. It pairs with windowRows exactly as bodyAvailForBar pairs with
// bodyAvail and bodyScrollsForBar with bodyScrolls, and `items` carries the
// same obligation it does there: it must be the bar that is about to be DRAWN,
// and where the answer feeds the bar's own contents the caller passes the bar
// WITH the scroll keys on it, because the tallest bar is the fixed point.
//
// It exists because the first sheet framing with frameWrapped that needed a
// page step had no layer variant to call and inlined windowRows' two lines
// instead. That is how the ~50 copies sc-jde-lift had to unpick began — not
// with fifty, but with one that was too small to be worth a shared function,
// and each of the next forty-nine had the same argument available to it. Two of
// those copies had drifted apart at the edges by the time anybody looked, and
// one had already been rewritten once for asking ClampScroll, which reserves
// the two indicator rows and so answers two lines early. A page that moves by a
// different count than the window draws walks the cursor past rows the operator
// never saw, and nothing about it looks wrong in a diff.
func (g jdeScreen) windowRowsForBar(body *jdeLines, cursorRow, headerRows int, items []actionBarItem) int {
	_, rows := body.Window(cursorRow, g.bodyAvailForBar(headerRows, items))
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

// jdeStatusErrMark and jdeStatusWarnMark are the marks the status row draws in
// FRONT of a message: the failure's "✗ ", and — on the storage family's
// composite row — the warning's "! ".
//
// They are handed to fitStatus rather than counted inside it. The branches that
// draw NO mark (the muted working line, and the storage sheets' standing note)
// have those two columns and must keep them: budgeting every branch against a
// flat two was two columns conservative on the marked rows' behalf and cut a
// 51-cell note at 49 on an 80-column pane that had room for all of it, which is
// this project's "never discard data the terminal had room to show" rule
// pointed backwards. Passing the mark ITSELF also means the reservation is
// measured from the very string that gets prepended, so a mark and its
// reservation cannot drift apart the way a constant sitting beside them can.
const (
	jdeStatusErrMark  = "✗ "
	jdeStatusWarnMark = "! "
	jdeStatusOKMark   = "✓ "
)

// statusRow is the one row above the bar: what is in flight, or what went
// wrong. A screen renders it on every frame, blank included, so the bar
// underneath never moves between frames.
//
// It is a METHOD rather than the free function it used to be because the bound
// below needs the pane, and a screen that could draw this row without the pane
// could draw it without the bound. That is what happened: only the three
// purchasing screens passed their message through a local `poStatusError`, and
// the other thirty-odd converted sheets handed an unbounded OMS error straight
// to a row that cannot fold (sc-jde-lift).
func (g jdeScreen) statusRow(saving bool, verb, errMsg string) string {
	switch {
	case saving:
		return StyleMuted.Render(g.fitStatus("", verb))
	case errMsg != "":
		return StyleStatusError.Render(g.fitStatus(jdeStatusErrMark, errMsg))
	}
	return ""
}

// statusAnswer is the status row carrying a screen's ANSWER TO THE LAST
// KEYPRESS — what the key just did, and why it declined — rather than what is
// in flight or what failed.
//
// It exists because that answer had nowhere to live that the frame cannot take
// away. A pinned header row is trimmed by jdeFitHeader the moment the pane is
// short, and a header may mark only ONE row essential, so a screen with a typed
// box and an answer to give was made to choose between them: the New PO
// pickers gave the slot to the answer, which took the SEARCH BOX off the pane
// at 80x11 through 80x13, and every rune typed into the asset search then
// redrew a byte-identical frame — rule 1 broken by geometry. Trading which row
// disappears cannot fix that in either direction; the answer needs a surface
// outside the budget, and this row is one: the frames append it unconditionally
// and it is reserved on every pane, blank included.
//
// The LEVEL carries its own mark and colour, the same four the body notes use
// (pickerNote.renderLines), so an answer reads the same wherever it is drawn.
// The message is bounded by fitStatus against the mark it will sit behind, so a
// long answer is shortened with the mark's two cells already accounted for
// rather than after the fact.
//
// One ROW, which is the whole point and also the whole cost: an answer longer
// than the pane loses its TAIL, marked by fitCell. What survives is the head —
// the clause that names the key and what it did — which is the half that tells
// two presses apart.
func (g jdeScreen) statusAnswer(level StatusLevel, msg string) string {
	if msg == "" {
		return ""
	}
	mark, style := jdeStatusMark(level)
	return style.Render(g.fitStatus(mark, msg))
}

// jdeStatusMark is the mark and colour one status LEVEL is drawn in — the same
// four pickerNote.renderLines uses in the body, said once so an answer cannot
// read as a warning on one surface and a success on the other.
func jdeStatusMark(level StatusLevel) (string, lipgloss.Style) {
	switch level {
	case StatusError:
		return jdeStatusErrMark, StyleStatusError
	case StatusWarn:
		return jdeStatusWarnMark, StyleStatusWarn
	case StatusOK:
		return jdeStatusOKMark, StyleStatusOK
	}
	return "", StyleMuted
}

// fitStatus bounds one message to the status row and returns it behind `mark`
// — the two together, because the mark is what the message's own budget is
// measured against (jdeStatusErrMark).
//
// That row is ONE row of the frame — the frames append what comes back here
// verbatim — so it cannot fold, and
// the messages that reach it are routinely wider than the pane: "cannot read
// file: open <path>: no such file or directory" is around 96 columns for an
// ordinary scan path, and an OMS dial error is longer again. Left unbounded it
// is cut by clampToBox, which drops runes off the END of a styled string and so
// takes the closing SGR reset with them, leaving the terminal red (or muted) for
// everything drawn afterwards.
//
// What the row loses by being trimmed depends on where the message came from,
// and only some of them have a toast behind them. On the purchasing screens this
// was measured line by line when the bound was written:
//
//   - The ASYNC failures — poItemShippedMsg, poVoidedMsg, poDeliveredMsg,
//     poOrderPadMsg, poAttachUploadedMsg — carry an OMS error of any length, and
//     every one of them ALSO returns Status() with the same text. Those lose
//     nothing: the full message is on the toast.
//   - The three LOCAL validation refusals set the field and return no command,
//     so there is NO toast behind them. Two are comfortably inside the bound at
//     every width (submitShip's "line number must be between 1 and N" is 35-39
//     columns, submitDeliver's "delivery date is required (YYYY-MM-DD)" is 38,
//     against a budget of 49 at 80 columns). The third is NOT: submitShip's
//     "date must be YYYY-MM-DD (or '-' to clear, blank for today)" is 58, so at
//     80 columns it is shortened and its tail is not readable anywhere.
//
// That last one is knowingly accepted rather than overlooked. It is not a
// regression — before this bound existed clampToBox cut the same row at the same
// column AND dropped the closing SGR reset — and what it loses is the tail of a
// fixed sentence whose first 49 columns still name the format. Adding a longer
// validation message on one of those three paths, or letting one interpolate
// variable text, is what would make this genuinely lossy; a toast on that path
// (a behaviour change, deliberately not made) is what would fix it.
//
// fitCell rather than a bare cut, so the row says it was shortened. bodyWidth of
// zero is "the width is not known yet", which every bound in this layer reads as
// "do not truncate" — an unsized screen must not throw away columns the terminal
// may well have. The ROW bound is not skipped there, only the width one: a
// message is flattened at every size, because a row several rows tall is wrong
// on a pane of any width.
//
// The message is cut to the budget by a FORWARD pass (cellPrefix) before fitCell
// measures it. fitCell falls back on truncateVisible, which drops ONE rune off
// the end and re-measures the whole remaining string, so what arrives here — a
// 20 KB gateway page, now that jdeStatusOneLine has made it a single line —
// would cost O(n²) and freeze the frame, which is the hang AGENTS.md records
// against the picker bounds. Two cells of slack rather than one, so a
// double-width rune cannot land the prefix exactly on the budget: fitCell would
// then return it untouched and the row would claim a whole message where a cut
// one was drawn.
func (g jdeScreen) fitStatus(mark, msg string) string {
	msg = jdeStatusOneLine(msg)
	if msg == "" {
		return ""
	}
	bodyWidth := g.bodyWidth()
	if bodyWidth <= 0 {
		return mark + msg
	}
	if avail := bodyWidth - lipgloss.Width(mark); avail > 0 {
		return mark + fitCell(cellPrefix(msg, avail+2), avail)
	}
	return mark + msg
}

// jdeStatusOneLine collapses a message onto ONE line.
//
// The status row cannot fold, and a message carrying newlines does not overflow
// the WIDTH — lipgloss.Width reports the widest LINE, and nginx's stock 502 page
// is seven lines of at most 42 columns, comfortably inside the 49 an error has
// at 80 columns — it overflows the HEIGHT. The budget above it is sized so the
// body, this one row and the action bar exactly fill the pane, so the frame runs
// over by however many lines the message brought and clampToBox, which drops
// from the BOTTOM, takes the whole action bar with it: every key on the screen
// unnamed at once, the operator left staring at HTML with nothing saying how to
// get out. omsapi.parseError puts the ENTIRE raw response body into
// APIError.Message whenever the JSON envelope carries no code, so that page
// reaches this row verbatim on any of the thirty-odd converted sheets.
//
// It runs BEFORE the width is measured. The other way round, a long single line
// is bounded and then re-expanded by the flattening, which is the same defect
// with an extra step.
func jdeStatusOneLine(s string) string {
	if !strings.ContainsAny(s, "\r\n") {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "\r", " ")
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
func (g jdeScreen) frameWithHeader(header jdeHeader, body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	if g.tooShort(actionBarRows, len(header)) {
		return g.tooShortNotice(actionBarRows, len(header))
	}
	if budget := g.bodyRows(); budget > 0 {
		// bodyAvail is the ONE answer to "how many rows does the body get", and
		// jdeFitHeader is the same split seen from the header's side, so the
		// two together are exactly the budget.
		avail := g.bodyAvail(len(header))
		out := jdeFitHeader(header, budget, avail)
		lines, _ := body.Window(cursorRow, avail)
		out = append(out, lines...)
		out = jdePadTo(out, budget)
		out = append(out, status)
		return strings.Join(out, "\n") + "\n" + renderActionBar(g.barWidth(), items)
	}
	out := header.lines()
	out = append(out, body.text...)
	out = append(out, status)
	return strings.Join(out, "\n") + "\n" + renderActionBar(g.barWidth(), items)
}

// tooShortNotice draws jdeTooShort into the pane this screen actually has.
//
// jde:layer-only — the frames call it; a sheet hands the layer a body and a bar
// and is told what could be drawn.
func (g jdeScreen) tooShortNotice(barRows, headerRows int) string {
	return jdeTooShort(g.bodyWidth(), g.paneRows(), g.terminalHeight, jdeTooShortRows(barRows, headerRows))
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
//
// bodyWidth is the pane, for the same reason renderJDEField takes one: the
// filter box is a text row like any other, and an operator who has pasted a long
// string into it must still be able to see the caret.
func (p jdePickList) render(bodyWidth int) (jdeHeader, *jdeLines) {
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
		Input:   &box,
		Width:   30,
		Hint:    "type to narrow the list",
		Focused: true,
	}
	// THE FILTER BOX IS THE ONE ROW THIS HEADER CANNOT LOSE, and it says so
	// with a RANK rather than by leading the block.
	//
	// It was measured on InventoryItemFormScreen's category picker at 80x11:
	// pane 5, a two-row bar, budget 2, the body's floor takes one, so keep is 1
	// — and while jdeFitHeader trimmed by POSITION the one row that survived was
	// the decorative "Category" while the operator typed into a box that was not
	// on the pane. The list under it is windowed on the CURSOR, which typing does
	// not move, so `b`, `o`, `l` over "Bolts" / "Bolt washers" redrew the pane
	// byte for byte — rule 1 by geometry, arriving through the header after
	// jdeBodyAvail's body floor had closed the same defect on the body side.
	//
	// The first fix INVERTED the display, drawing the filter above the title the
	// way serialBody draws a field above what identifies it. That was the wrong
	// lever and it is recorded here because the reasoning was nearly right: a
	// pinned header IS a block no key can move, so it does have one end to
	// protect — but the end that has to survive and the end that reads first are
	// not the same end, and conflating them relaid out nineteen pickers at every
	// height to buy one row at one height. It also fixed nothing for the order
	// pad, whose warning had the identical problem and would have needed the
	// identical inversion. jdeHeadRank decouples the two, so the title goes back
	// on top and is still the first row dropped.
	header := jdeHeader(nil).
		add(jdeHeadDecorative, head).
		add(jdeHeadEssential, renderJDEFields([]jdeField{filter}, bodyWidth)[0]).
		add(jdeHeadDecorative, "")
	if p.Note != "" {
		header = header.add(jdeHeadContext, jdeIndent+StyleMuted.Render(p.Note)).
			add(jdeHeadDecorative, "")
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
		p := part{text: StyleActionBarKey.Render(it.Key), w: lipgloss.Width(it.Key)}
		if it.Label != "" {
			p.text += StyleActionBar.Render("=" + it.Label)
			p.w += 1 + lipgloss.Width(it.Label)
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
//
// jde:layer-only — a sheet asks bodyAvailForBar / bodyScrollsForBar / scrollRows
// for the BAR it is about to draw, and the layer measures it.
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

// Scrolls reports whether this body MOVES in a window of `avail` lines — the
// question every action bar on a columnar screen has to answer before it names
// UP/DN, PgUp/PgDn or Home/End, and every movement handler has to answer before
// it acts.
//
// It sits here, one screenful from Window and WindowFrom, because it is those
// two functions' own short-circuit and nothing else: both return the whole body
// untouched when `n <= avail`, ignoring the offset or the cursor entirely. Read
// them together — if that short-circuit ever changes, this line is in the same
// field of view and changes with it. A copy on a sheet is not.
//
// `avail` is what bodyAvail / bodyAvailForBar answer, and zero from those has
// exactly one meaning now: the terminal is UNSIZED, so there is no pane to
// window against and nothing can scroll. A pinned header that fills the pane
// used to be a second cause and is not — read bodyAvail for why, and for why a
// pane the frame is REFUSED on is not one either. That second correction is
// what stops this answer flipping at the refusal boundary and shrinking the bar
// the refusal notice is measured against.
//
// jde:layer-only — a sheet asks bodyScrolls / bodyScrollsForBar.
func (l *jdeLines) Scrolls(avail int) bool {
	return avail > 0 && len(l.text) > avail
}

// ClampScroll brings a scroll offset back inside the body, given the pane
// height WindowFrom will be called with. No sheet calls it: WindowFrom clamps
// what it is about to draw, and frameScrolled hands the clamped offset back for
// the sheet to store, so the offset a screen holds and the one that gets drawn
// are never different — which is what makes "↓ 0 more below" impossible.
//
// `avail` is the rows the body really gets, and every caller has already
// established that it is positive: WindowFrom returns before it reaches here
// when it is not, and frameScrolled skips the clamp entirely so a pane too short
// to draw the body HOLDS the operator's position instead of resetting it. A
// guard here answering zero for that case is what reset it, and a branch nothing
// exercises is only an invitation to answer that way again.
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
// bar, less the one status row above it. It is the WHOLE budget the header and
// the body share, and it is exact — budget + 1 + barRows is the pane, so the
// assembled frame fills it and never runs over.
//
// ZERO means "this frame cannot be drawn to that budget", and the frames tell
// the two causes apart by asking paneRows:
//
//   - paneRows()==0 — the terminal is unsized. There is no budget at all, so
//     the frame draws every line and Root's clampToBox decides what fits, the
//     same thing every unsized screen in the app does.
//   - paneRows()>0 — the pane is TOO SHORT to carry the bar, the status row and
//     even one row of the screen's own content. The frame draws jdeTooShort
//     instead (see there for why nothing is better than something).
//
// It used to FLOOR at three rows, which is the defect this whole file's
// geometry exists to prevent, wearing the costume of a safety net. The floor
// does not create rows; it only makes the frame claim rows the pane does not
// have. Whenever screenBodyRows(H) < barRows+4 the assembled frame ran over and
// clampToBox — which drops from the BOTTOM — took the action bar off it: at
// 80x14 the purchase-order detail lost the last of its four key lines, at 80x12
// three of the four, and at 80x10 the entire bar including the rule, while
// every one of those keys went on working and the screen went on being the only
// place an operator could have learned about them.
//
// jde:layer-only — a sheet asks bodyAvailForBar.
func (g jdeScreen) bodyRowsForBar(barRows int) int {
	pane := g.paneRows()
	if pane <= 0 {
		return 0 // unsized
	}
	if h := pane - barRows - 1; h > 0 {
		return h
	}
	return 0 // too short — the frames draw jdeTooShort
}

// tooShort reports whether the pane is known and cannot hold everything a frame
// has to show at once. It is said here, once, so a frame cannot test it one way
// and budget the other.
//
// jde:layer-only — a sheet never asks: it hands the layer a body and a bar and
// the layer decides what is drawable.
func (g jdeScreen) tooShort(barRows, headerRows int) bool {
	return g.paneRows() > 0 && g.bodyRowsForBar(barRows) < jdeMinBudget(headerRows)
}

// jdeMinBudget is the smallest header+body budget a frame can be drawn into: one
// row of BODY always, and one row of HEADER as well whenever the screen pins one.
//
// Both floors are rule 1 — a keypress has to change something the operator can
// see — and each covers a keypress the other does not:
//
//   - the BODY row carries the cursor and the focused box, so without it a
//     press that moves or types redraws the pane byte for byte.
//   - the HEADER row carries the screen's answer to the press that DECLINED —
//     the receiving form's note is the whole of "enter needs a quantity first"
//     — so without it a refusal is silent, which is this project's oldest
//     report ("it just kinda hangs there") arriving by geometry.
//
// At a budget of one they cannot both be had, and starving either one is the
// defect the other names. So a budget of one with a header pinned is NOT
// drawable, and the frame says so instead of choosing a defect.
func jdeMinBudget(headerRows int) int {
	if headerRows > 0 {
		return 2
	}
	return 1
}

// jdeTooShortRows is the smallest pane a frame with this bar and this header can
// be drawn honestly into: the bar, the status row above it, and jdeMinBudget.
//
// THE NUMBER IT PRODUCES IS THE NUMBER THE OPERATOR IS TOLD TO RESIZE TO, so it
// has to be a height that ACTUALLY WORKS when they get there. It is computed at
// the CURRENT height from the bar the sheet built at the CURRENT height — there
// is no other bar available, because `items` is handed to the layer already
// built — so the claim needs an argument rather than a loop. Here it is, and it
// is a claim about the HEIGHT axis at a FIXED WIDTH: a bar's wrapping is a
// function of width too, and this notice only ever tells the operator to make
// the terminal taller.
//
//   - paneRows is strictly increasing in terminal height: screenBodyRows is
//     height − screenChromeRows.
//
//   - For a FIXED bar, avail is NON-DECREASING in paneRows. bodyRowsForBar is
//     pane − barRows − 1, so the budget rises with the pane, and jdeBodyAvail
//     subtracts headerRows off it.
//
//     The premise this step needs is the WEAK one — avail never goes backwards —
//     and the original wording smuggled in a stronger claim that is FALSE: that
//     the header is a constant. It is not on ReceiveFormScreen, the one screen
//     whose pinned header is a function of the pane (headerLines → noteLines /
//     failDetailLines → headerSplit → headerRoom → headerBudget, and noteRows'
//     own comment says the reservation "yields to the PANE"). The weak form
//     still holds there, and headerRoom is where to check it: it hands the
//     header `budget − receiveBodyFloor` once that clears the header's floor, so
//     the header grows by at most ONE row per pane row and the body's floor of
//     receiveBodyFloor rows is subtracted BEFORE the header is served. A header
//     that never grows faster than the budget cannot make budget − headerRows
//     shrink, so avail never goes backwards.
//
//   - Scrolls(avail) is len(text) > avail, so it is non-increasing in avail.
//
//   - A bar naming the scroll keys is taller-or-equal to the same bar without
//     them: naming them costs cells and cells only ever fold a bar onto MORE
//     rows.
//
//   - Therefore barRows is NON-INCREASING in terminal height. Going UP, the
//     scroll claim can only go true → false, never false → true.
//
// So drawable(H) is monotone: once true it stays true. And the height computed
// from the CURRENT barRows is drawable AT that height, because barRows(H′) ≤
// barRows(H) gives pane(H′) − barRows(H′) − 1 ≥ pane(H′) − barRows(H) − 1,
// which is exactly jdeMinBudget by construction. There is no loop to bound and
// nothing to converge: termination is by construction. The worst the number can
// be is an OVER-estimate, by however many rows the bar sheds on the way up —
// never an under-estimate, which is the direction that makes it a lie.
//
// The fourth step is what had to be REPAIRED for the rest to hold, and it is
// recorded in jdeBodyAvail: while a refused pane answered avail 0, the scroll
// claim went false at exactly the heights this function is asked about, the bar
// SHRANK there, and the number came out one row short. The PO-detail order pad
// at 80 columns said "needs 11 rows" at every height from 7 to 10 and was still
// refused at 11.
//
// jde:layer-only — the layer decides what is drawable.
func jdeTooShortRows(barRows, headerRows int) int {
	return barRows + 1 + jdeMinBudget(headerRows)
}

// jdeTooShort is what a columnar screen draws when the pane cannot hold its
// action bar.
//
// Drawing the frame anyway is the alternative, and it is worse in the exact way
// this project keeps paying for. The bar is the ONLY place an operator learns
// what works; a bar with rows cut off the bottom of it names some keys and
// hides the rest, silently, and the operator has no way to know which. Drawing
// the body instead and losing the bar entirely is the same trade with all of
// the bar hidden. So the frame gives up and says so: no keys named, and a
// sentence saying what is wrong and what would fix it.
//
// THE SECOND SENTENCE USED TO REASSURE, and that was the wrong claim. It said
// "They still work", which an operator reads as "so go ahead and press them" —
// and pressing them is exactly what they must not do without knowing the cost.
// Update is untouched, so every key DOES act; it acts on a screen that is not
// being drawn, so nothing on the pane shows what it did, and what it did can be
// destructive of the operator's place. `end` on the purchase-order detail's
// order pad sets padScroll to the pad's length against a pane showing nothing
// but this notice, and growing the terminal back lands them at the bottom of a
// forty-line pad instead of where they left. The wording now says that, because
// a warning an operator can act on beats a reassurance they cannot.
//
// WHY THE KEYS ARE NOT GATED HERE, so the next reader does not reopen it. Two
// things were checked before the decision:
//
//   - frameScrolled ALREADY hands the offset back untouched on this path. It is
//     not enough: the drift happens in the SHEET's handler, before the frame is
//     ever called, so the frame is faithfully preserving a value that has
//     already been destroyed. (The UNSIZED path does the opposite and resets the
//     offset to 0, so "hold it the way unsized does" would make this worse.)
//   - a layer-only stash of the last DRAWN offset would fix this instance and
//     leave the CLASS. The same thing happens to the CURSOR on every frame /
//     frameWrapped screen: at a refused height Up/Down still move it, nothing is
//     drawn, and the operator comes back on a different field. Fixing the two
//     frameScrolled screens and leaving twenty-nine cursor screens is the
//     "apply the rule to the site that was reported" failure this project keeps
//     paying for.
//
// The class fix is to gate movement on DRAWABILITY rather than on
// scrollability, which means giving bodyAvail and bodyScrolls the arguments
// they do not take across ~30 sheets. That is the same signature change the
// renderActionBar width fix needs, so the two are routed as ONE conversion
// (AGENTS.md carries it).
//
// It is bounded in both axes by the layer, not by clampToBox: `rows` lines of
// at most `width` cells. A notice that was itself cut would be the defect it
// exists to report — and the HEIGHT FACT leads, because a one-row pane keeps
// only the first line and the height is the one thing on here that can be acted
// on.
// jde:layer-only — a sheet reaches it through the frames.
func jdeTooShort(width, rows, terminalHeight, needRows int) string {
	if rows <= 0 {
		return ""
	}
	if width <= 0 {
		// bodyWidth answers 0 only on an unsized terminal, which tooShort never
		// reports on — but a fixed number here would be a second opinion about
		// how narrow a pane can get, so it borrows layout.go's own floor rather
		// than inventing one.
		width = screenBodyWidth(0)
	}
	// In TERMINAL rows, which is the only unit the operator can act on: they
	// resize a terminal, not a content pane. screenChromeRows is the inverse of
	// screenBodyRows, so the two cannot drift.
	lines := jdeWrapNote(fmt.Sprintf("Too short: needs %d rows, has %d.",
		needRows+screenChromeRows, terminalHeight), width)
	lines = append(lines, jdeWrapNote(
		"No keys are named: the action bar would be cut. Keys still act, but on a "+
			"screen that is not drawn, so you will not see what they do.", width)...)
	if len(lines) > rows {
		lines = lines[:rows]
	}
	return strings.Join(lines, "\n")
}

// frameWrapped is frameWithHeader for a screen whose bar may wrap: the body is
// padded out to whatever the wrapped bar leaves it, so the bar still lands on
// the same rows of the pane on every frame.
func (g jdeScreen) frameWrapped(header jdeHeader, body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	barRows := actionBarRowsFor(g.barWidth(), items)
	if g.tooShort(barRows, len(header)) {
		return g.tooShortNotice(barRows, len(header))
	}
	if budget := g.bodyRowsForBar(barRows); budget > 0 {
		avail := g.bodyAvailForBar(len(header), items)
		out := jdeFitHeader(header, budget, avail)
		lines, _ := body.Window(cursorRow, avail)
		out = append(out, lines...)
		out = jdePadTo(out, budget)
		out = append(out, status)
		return strings.Join(out, "\n") + "\n" + renderActionBarWrapped(g.barWidth(), items)
	}
	out := header.lines()
	out = append(out, body.text...)
	out = append(out, status)
	return strings.Join(out, "\n") + "\n" + renderActionBarWrapped(g.barWidth(), items)
}

// frameScrolled is frameWrapped for a read-only body: the window is positioned
// by the operator's scroll offset rather than by a cursor row. It returns the
// clamped offset alongside the frame, so a screen that scrolled past the end
// stores back the offset that was actually drawn.
func (g jdeScreen) frameScrolled(header jdeHeader, body *jdeLines, offset int, status string, items []actionBarItem) (string, int) {
	barRows := actionBarRowsFor(g.barWidth(), items)
	if g.tooShort(barRows, len(header)) {
		// The offset is handed back UNTOUCHED. Both callers store what they are
		// given (po_detail's padScroll, po_add_line's scroll), and a pane too
		// short to draw the body is exactly the state where clamping would
		// answer 0 and throw away where the operator had scrolled to. A terminal
		// dragged short and grown again comes back where it was. This is now the
		// ONLY path that skips the clamp: past it the body always has at least
		// one row (bodyAvail), so there is always something to clamp against.
		return g.tooShortNotice(barRows, len(header)), offset
	}
	budget := g.bodyRowsForBar(barRows)
	if budget > 0 {
		avail := g.bodyAvailForBar(len(header), items)
		out := jdeFitHeader(header, budget, avail)
		offset = body.ClampScroll(offset, avail)
		out = append(out, body.WindowFrom(offset, avail)...)
		out = jdePadTo(out, budget)
		out = append(out, status)
		return strings.Join(out, "\n") + "\n" + renderActionBarWrapped(g.barWidth(), items), offset
	}
	offset = 0
	out := header.lines()
	out = append(out, body.text...)
	out = append(out, status)
	return strings.Join(out, "\n") + "\n" + renderActionBarWrapped(g.barWidth(), items), offset
}

// scrollRows is how many lines a read-only body scrolls per page — one paneful
// less a line of overlap for the reader's eye, the same step TextScroller used
// before these screens moved onto the columnar layer. Never less than one.
//
// It takes the BAR rather than the bar's measured height so that the step, the
// window and the bar's claim that PgUp/PgDn move something are all computed from
// one expression (bodyAvailForBar). Sheets used to hand it
// actionBarRowsFor(barWidth, items) themselves, which put the bar-height half of
// the arithmetic back on the sheet.
func (g jdeScreen) scrollRows(headerRows int, items []actionBarItem) int {
	avail := g.bodyAvailForBar(headerRows, items)
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
//	the input area shrinks   — the jdeField's Width, which is the underscored
//	                           or reverse-video FILL drawn after the value.
//	the hint moves under it  — once the field is down to jdeMinFieldWidth, the
//	                           hint becomes a note line indented to the input
//	                           area (jdeNoteLines), which is the fold this layer
//	                           already uses for a note too long to ride along.
//
// What it sizes is the ROW. The box that fills it is bounded to the Width set
// here by jdeFitInputValue, at render time, for every text row on every sheet —
// including the ones that never call this function, which are bounded to the
// input area they declared instead. The two halves have to stay in step: a row
// sized here and a value sized nowhere is how a typed value used to walk out of
// the pane (sc-jde-tiw).
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
// note explaining it on screen together. A rowBase of jdeNoRow tags the whole
// band read-only, as it does in AddFields.
func (l *jdeLines) AddFittedFields(fields []jdeField, labelWidth, bodyWidth, rowBase int) {
	for i, f := range fields {
		row := jdeRowAt(rowBase, i)
		fitted, notes := jdeFitRow(f, labelWidth, bodyWidth)
		l.AddRow(row, renderJDEField(fitted, labelWidth, bodyWidth))
		for _, note := range notes {
			l.AddRow(row, note)
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
