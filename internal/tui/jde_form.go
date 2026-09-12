// Columnar green-screen form primitives — the shared layer of the ScanTTY
// "JD Edwards" redesign (sc-h412). po_edit.go is the pilot that uses it, and
// every other converted sheet draws its rows through `AddFittedFields` /
// `AddFittedField` here rather than rolling its own — which is why nothing in
// this file knows what a purchase order is. The roster of which sheets those
// are is not written down anywhere: it is every type embedding jdeScreen, and
// `jdeScreenFixtures` (jde_pane_fit_test.go) derives it from the package
// source and fails on an omission.
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
//	renderActionBarWrapped     — the bar itself, folded onto as many lines as
//	                             its keys need.
//	jdeScreen                  — the pane geometry and the framing that puts a
//	                             body, a status line and the bar together.
//	                             bodyAvailForBar is the ONE answer to how many
//	                             rows a body gets, bodyScrollsForBar the one
//	                             answer to whether it moves, frameDrawn the one
//	                             answer to whether it is on the pane at all, and
//	                             statusRow the only way to draw the row above
//	                             the bar — a sheet that could build any of them
//	                             itself would build it wrong eventually, and did
//	                             (sc-jde-lift).
//	jdePickList                — the "filter and choose one" sub-phase every
//	                             foreign-key row opens.
package tui

import (
	"fmt"
	"strings"
	"unicode"
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
	avail := bodyWidth - (len(jdeIndent) + labelWidth + len(jdeLeader)) - jdeSwatchCost(f)
	if avail < 1 {
		avail = 1
	}
	if width > avail {
		width = avail
	}
	return width
}

// jdeSwatchCost is what a row's sample spends: the two-column gutter plus the
// sample itself, or nothing when there is none.
//
// It is RESERVED rather than ignored because the swatch is drawn LAST, after
// the fill and after any hint, so a fill sized without it is a fill that pushes
// the sample off the pane — and the sample is the row's whole answer to "which
// colour is this?". jdeColorRow drops the HINT when a sample is present, which
// is what left slack enough for the overrun to stay latent: the hint was paying
// for the sample by accident.
func jdeSwatchCost(f jdeField) int {
	if f.Swatch == "" {
		return 0
	}
	return 2 + lipgloss.Width(f.Swatch)
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

func fitFactCell(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	for _, r := range s {
		if unicode.IsDigit(r) {
			return paneCutMark
		}
	}
	return fitCell(s, w)
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
	// lead is 1 + the index of the line DeclareLead named, so the zero value
	// means "nothing declared"; leadSite is the name the declaration was made
	// under. Both are read by the sweeps and by nothing that draws.
	lead     int
	leadSite string
}

// Add appends a line that belongs to no row.
func (l *jdeLines) Add(text string) { l.AddRow(jdeNoRow, text) }

// DeclareLead names the NEXT line appended as the one this body must keep on
// every pane it is drawn in, under a site name unique to the call.
//
// It is for a body drawn through a PINNED window — one whose cursor's block is
// the whole body, the shape of every body with one navigable row — and the
// reason is Window's own arithmetic rather than a style. Window anchors on the
// cursor's block and, when that block will not fit, keeps its START; nothing
// scrolls inside a block. A block that starts at line 0 and ends at the last
// line can therefore only ever be drawn from line 0, so that line is the only
// one EVERY drawable pane keeps — the shortest pane the layer will draw leaves
// the body one row. Whatever the operator cannot do without has to be that line,
// and that is a choice about MEANING only the builder can make: the focused box
// a scanner fires into, the fact a summary exists to report. On a body whose
// window is not pinned the declaration says nothing and nothing reads it.
//
// It records the choice rather than enforcing it, because the check needs the
// rendered pane and the builder does not have one. What holds it is the
// receiving form's sweep (TestReceive_ABodyNoKeyCanMoveLeadsWithWhatTheOperatorNeeds),
// which derives its set from the built body and the row body() anchors on, and
// fails on a pinned body that declares nothing, on a declaration that is not the
// body's first line, and on a lead that is not whole on the clipped pane. A site
// name is what lets TestReceive_EveryLeadDeclarationIsJudgedOnAPinnedBody find,
// from the source, a declaration no swept state reaches or one only ever seen on
// a body the cursor moves. The receiving form is the only screen that declares
// one today, and that sweep is scoped to it.
func (l *jdeLines) DeclareLead(site string) {
	l.lead, l.leadSite = len(l.text)+1, site
}

// Lead is the index of the declared lead line and the site that declared it, or
// ok false when nothing was declared.
func (l *jdeLines) Lead() (index int, site string, ok bool) {
	return l.lead - 1, l.leadSite, l.lead > 0
}

// AddRow appends a line belonging to navigable row `row`.
func (l *jdeLines) AddRow(row int, text string) {
	l.text = append(l.text, text)
	l.row = append(l.row, row)
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
// jde:layer-only — a sheet asks bodyAvailForBar.
func (g jdeScreen) paneRows() int {
	if g.terminalHeight <= 0 {
		return 0
	}
	return screenBodyRows(g.terminalHeight)
}

// bodyWidth is the columns the body has, or 0 when the width is not known yet —
// which callers read as "do not truncate", the same way bodyAvailForBar()==0
// means "do not window".
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

// jdeBodyAvail is the number of lines the frames window a body into, given a
// pinned header of headerRows lines — see bodyAvailForBar for the one way a
// sheet asks it.
//
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
// "nothing can scroll", which is what bodyScrollsForBar reads it as.
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
//
// block is set on every row of a block added with addFitted, and it is the
// same pointer across them: that identity is how jdeFitHeader knows which rows
// are one value to be re-drawn together rather than independent facts to be
// dropped one at a time.
type jdeHeadRow struct {
	Text  string
	Rank  jdeHeadRank
	block *jdeHeadBlock
}

// jdeHeadBlock is what a fitted block hands the layer: the means to draw itself
// again at fewer rows. See addFitted.
type jdeHeadBlock struct {
	refit func(rows int) []string
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

// addFitted appends a block that is ONE VALUE folded across rows — an error
// body, a note, a caveat — and hands the layer the means to draw it again at
// fewer rows: refit(n) is that value rendered into at most n rows, with the cut
// it now makes marked.
//
// jdeFitHeader gives ground from the END within a rank, which is right for a
// run of independent facts — dropping the order row claims nothing about the
// supplier row above it — and wrong for rows that are one value, because what
// it leaves is a FRAGMENT that reads as the whole. Two shapes of that were on
// the add-line screen's failure frame at once. failDetailLines spends the last
// of an error body's rows on "… more of the error than this pane can hold", so
// the trim took the MARK first and left `oms: http 502: <!DOCTYPE` at 80x17,
// reading as the complete reason: the helper had marked the cut, and the layer
// then cut the mark. And the note above it folds to four rows, so at 80x11 it
// was cut to "✗ could not tell whether Acme Fasteners &" with nothing saying
// the rest of the sentence existed. Every call site already went through a
// bounded renderer; the loss lived HERE, in the one function that decides how
// many of a block's rows are drawn, and here is the only place it is put right
// once rather than per screen.
//
// So a fitted block is never trimmed row by row. When jdeFitHeader keeps SOME
// of its rows it asks refit for exactly that many and draws what comes back in
// their place, and the block marks its cut the way its own renderer already
// marks one (foldKeepRows, failDetailLines) — not with a second mark invented by
// the layer. Kept whole, it is drawn as built; kept not at all, it is absent
// rather than a fragment, which is the same claim a dropped independent row
// makes.
//
// `lead` ranks the first row and `rest` every row after it, because a note's
// first line is often the one row the builder marks essential and its fold is
// context: trimmed from the end, the kept rows of a block are still its HEAD.
// The block's height as BUILT is what feeds the budget, so fitting one moves
// nothing about how many rows the header claims — refit is only ever asked for
// fewer. It must return at most n rows; a longer answer is cut to n and a
// shorter one padded, because header + body must still be EXACTLY the budget.
func (h jdeHeader) addFitted(lead, rest jdeHeadRank, lines []string, refit func(rows int) []string) jdeHeader {
	if len(lines) == 0 {
		return h
	}
	if refit == nil || len(lines) == 1 {
		// One row has no fragment to leave: it is drawn whole or not at all.
		return h.add(lead, lines[0]).add(rest, lines[1:]...)
	}
	b := &jdeHeadBlock{refit: refit}
	for i, line := range lines {
		rank := rest
		if i == 0 {
			rank = lead
		}
		h = append(h, jdeHeadRow{Text: line, Rank: rank, block: b})
	}
	return h
}

// addFittedBlock is addBlock for a fitted block: the separator comes with it,
// and nothing at all is appended when the block is empty.
func (h jdeHeader) addFittedBlock(rank jdeHeadRank, lines []string, refit func(rows int) []string) jdeHeader {
	if len(lines) == 0 {
		return h
	}
	return h.add(jdeHeadDecorative, "").addFitted(rank, rank, lines, refit)
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
//
// A dropped row is claimed about by nothing — a window promises a remainder
// and a header does not — so a trim that takes WHOLE values leaves nothing
// looking complete that is not. A block added with addFitted is the one shape
// where a trim would take PART of a value, and it is re-drawn at the rows it
// kept rather than cut, so its own mark says what went (addFitted).
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
	for i := 0; i < len(header); {
		b := header[i].block
		if b == nil {
			if kept[i] {
				out = append(out, header[i].Text)
			}
			i++
			continue
		}
		end, n := i, 0
		for ; end < len(header) && header[end].block == b; end++ {
			if kept[end] {
				n++
			}
		}
		switch {
		case n == end-i:
			for _, row := range header[i:end] {
				out = append(out, row.Text)
			}
		case n > 0:
			out = append(out, jdeRefitRows(b.refit, n)...)
		}
		i = end
	}
	return out
}

// jdeRefitRows asks a fitted block for n rows and holds it to exactly n, so the
// header still comes to the budget jdeBodyAvail windowed the body against.
//
// Cutting an over-long answer here would be an UNMARKED cut, and it is a
// geometry backstop rather than a bound: every refit in the package is built
// on foldKeepRows or failDetailLines, both of which answer at most the rows
// they are asked for, so no screen reaches it.
func jdeRefitRows(refit func(rows int) []string, n int) []string {
	rows := refit(n)
	if len(rows) > n {
		rows = rows[:n]
	}
	for len(rows) < n {
		rows = append(rows, "")
	}
	return rows
}

// bodyAvailForBar is how many lines a frame windows its body into, given a
// pinned header of headerRows lines and the bar it is about to draw. Zero means
// the terminal is UNSIZED — see jdeBodyAvail.
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

// bodyScrollsForBar reports whether `body` actually MOVES in the frame that is
// about to be drawn.
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

// bodyPagesForBar is the paging pair's ONE question, asked by the BAR that
// claims PgUp/PgDn and by the ARM behind them: the body must MOVE and there must
// be another row to LAND on.
//
// TWO conditions, because the keys make two claims and both have to hold.
// bodyScrollsForBar answers the first. The second is the one that kept being
// left out: PgUp/PgDn do not scroll the body, they move the CURSOR
// (jdePageCursor) and the window follows it, so a page can only do anything when
// there is a second row to move to. The two agree in almost every state and come
// apart in the state a list SPENDS MOST OF ITS LIFE IN — the empty one. An
// unopened kit list is a heading, its guidance and the trailing "(add a
// component)" row: at 80x11 through 80x17 the body outruns the window while the
// add row is the only row a cursor can stand on, so the bar named the pair and a
// page moved nothing, on the default state of a new inventory item. The
// packaging chain and the storage level list did the same at their own heights.
//
// `count` is the NAVIGABLE ROW COUNT — what the paging arm passes pageRow —
// rather than the body's line count, and the difference is the whole point: a
// list's lines include its heading and its guidance, and a cursor cannot stand
// on either.
//
// It is here rather than in the sheets because three of them had already worked
// it out for themselves (po_create's bodyPagesFor, receive_form's qtyPagesFor,
// service_status_screen nesting its paging entry inside `len(services) > 1`) and
// thirty had not. Both readers ask THIS, so a bar's claim and the key behind it
// cannot part company.
func (g jdeScreen) bodyPagesForBar(body *jdeLines, count, headerRows int, items []actionBarItem) bool {
	return count > 1 && g.bodyScrollsForBar(body, headerRows, items)
}

// windowRowsForBar is how many navigable rows the pane is currently showing —
// computed from the same lines the frame draws, so a page moves by exactly what
// the operator can see rather than by a guessed constant. headerRows is what a
// pinned header costs the body (0 when there is none). Never less than one.
//
// `items` carries the same obligation it does on bodyAvailForBar: it must be
// the bar that is about to be DRAWN, and where the answer feeds the bar's own
// contents the caller passes the bar WITH the scroll keys on it, because the
// tallest bar is the fixed point.
//
// It exists because the first sheet framing with frameWrapped that needed a
// page step had no layer variant to call and inlined the two lines instead. That is how the ~50 copies sc-jde-lift had to unpick began — not
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

// ---------------------------------------------------------------------------
// Movement: gated on DRAWABILITY, not on scrollability
// ---------------------------------------------------------------------------
//
// A pane too short to carry the bar, the status row and one row of the screen's
// own content is REFUSED: the frames draw jdeTooShort and nothing else. Nothing
// about that refusal used to reach the sheets' key handlers, so every movement
// key went on acting on a screen nobody was looking at — `end` on the
// purchase-order detail's order pad set padScroll to the end of a forty-line
// pad against a pane showing the notice, and Up/Down walked the cursor through
// the fields of every form in the program. Growing the terminal back landed the
// operator somewhere they never navigated to, which is standing rule 4: never
// silently discard or overwrite where they were.
//
// The handlers used to read bodyScrollsForBar, which asks a DIFFERENT question
// — is there more content than fits — and answers yes at a refused height as
// readily as at any other. The two questions have to stay apart, and the reason
// is the refusal NOTICE:
//
//   - the BAR goes on claiming the scroll keys at a refused height, because it
//     is not drawn there and its only remaining job is to be MEASURED. Dropping
//     the keys shrinks the bar, jdeTooShortRows then names a height one row
//     short of one that works, and the operator resizes to precisely what the
//     screen asked for and is refused again. That defect has been shipped once
//     already (see jdeBodyAvail) and making bodyScrollsForBar answer false when
//     refused would ship it a second time.
//   - the HANDLERS stop, because a key whose whole effect is a position change
//     has nothing to show for itself on a pane that is not being drawn.
//
// So drawability is asked separately, here, and every arm that moves the
// operator's place goes through frameDrawn — directly, or through one of the
// three cursor primitives under it. A sheet that restated the rule would
// eventually restate it differently; TestJDEForm_ARefusedPaneKeepsTheOperatorsPlace
// walks every columnar screen at every refused height to prove none of them has.
//
// THE RULE IS ONE STATEMENT, NOT TWO: a movement key acts when the frame is
// DRAWN and — for a PAGE — when the body MOVES. Drawability is asked of the bar
// really drawn, scrolling of the CEILING bar, and an unsized terminal answers as
// it always has, which is that both are yes and clampToBox decides.
//
// WHERE THE PAIR IS ASKED. pageRow asks both, and it is the DEFAULT: a screen
// with no further condition on its pager reaches for it and gets the whole rule,
// which is what a new screen should do. A screen carrying a condition pageRow
// CANNOT EXPRESS asks the two directly and SAYS WHY AT THAT SITE — so the
// exception is self-evident where it occurs instead of being tracked in a roster
// somewhere else, and the next such screen documents itself by following the
// rule rather than by being added to a list.
//
// The condition that recurs is a THIRD fact pageRow's single bool cannot carry:
// "refused" and "nothing to page" come back as the same false, and several
// screens must answer those two differently — silence on a refused pane (a
// movement arm's whole product was the position), a decline note when the body
// simply does not move (a key the operator pressed on a frame they can see).
// po_create's bodyPagesFor and receive_form's qtyPagesFor add a second one on
// top: a page moves the CURSOR, so there must be another row to LAND on, which
// is a different question from whether the body overflows and comes apart from
// it in states those screens have by design.
//
// The scroll half lived in the SHEETS until it did not: they bound pgup/pgdown
// unconditionally in their key switches while naming the pair only when the body
// overflowed, so on any pane tall enough to hold the whole body the bar rightly
// said nothing and PgDn still walked the cursor to the last row. Two sheets then
// spelled the conjunction for themselves, which closed the class at two of
// thirty-two sites and is how the ~50 per-sheet scroll copies sc-jde-lift had to
// unpick began — one too small to be worth a shared function, with the same
// argument available to the next forty-nine. It is the layer's now, and
// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves holds the
// biconditional over every columnar screen so site thirty-three cannot reopen
// it.
//
// An OFFSET still has no combined helper and still must not get one — see the
// paragraph above moveRow. A page is different because pageRow already had the
// body and the header rows in hand: the only thing it lacked was the second bar.
//
// A GATED MOVEMENT ARM ANSWERS WITH NOTHING — not even a note saying the key
// declined. Its whole effect WAS the position, so once the move is refused
// there is nothing left to report; and a note is not drawn on a frame the layer
// refused while it IS drawn when the terminal grows back, so it would arrive as
// a reply to a press the operator has moved on from. The notice is the standing
// answer for the whole pane and it is one the operator can act on, which is
// what makes that silence legitimate.
//
// THE BOUNDARY IS WHAT THE KEY DID, NOT WHAT THE PANE IS. A key that DECLINES
// AND ANSWERS is not gated and must not be: an empty picker list, a search that
// matched nothing, a page at either edge: its answer is the visible change rule
// 1 requires and it belongs on the surface #155 gave it, which the layer cannot
// trim. Read after the pane grows back it is later than ideal, and far better
// than a key that never speaks — gating those arms would silence them at
// exactly the heights that work made them speak. So the silence above is the
// rule for arms whose only product is a POSITION, and for no others.
//
// TYPING is deliberately NOT gated, and the asymmetry is the point. A movement
// key's whole effect IS the position, so declining it preserves what the
// operator had; a typed rune's effect is the VALUE, and declining that would
// DISCARD input — the same rule broken in the direction it forbids most
// strongly, and a barcode scanner firing into a short pane is exactly the case.
// The notice tells the operator the pane is too short; it does not eat what
// they type.

// frameDrawn reports whether the frame this header and this bar describe is
// DRAWN on the pane the terminal currently gives. It is the exact negation of
// the refusal the frames make, asked of the same function they ask (tooShort),
// so a handler and a frame cannot disagree about whether anything is on screen.
//
// An UNSIZED terminal answers TRUE: there is no pane to be too short, every
// screen in the app draws whole and clampToBox decides. That is what keeps a
// screen driven without a WindowSizeMsg behaving exactly as it always has.
//
// `items` must be the bar the frame will really draw — not the ceiling bar the
// budget helpers are measured against. tooShort is monotone in the bar's
// height, so gating on a TALLER bar than the frame draws would decline a key at
// a height where the frame is drawn: a named key doing nothing on a visible
// pane, which is rule 1 broken in the other direction.
func (g jdeScreen) frameDrawn(headerRows int, items []actionBarItem) bool {
	return !g.tooShort(actionBarRowsFor(g.barWidth(), items), headerRows)
}

// THERE IS DELIBERATELY NO COMBINED "does this move AND is it drawn" HELPER for
// a SCROLL OFFSET, and the reason is worth keeping because it looks like an
// obvious tidy-up. The two questions are asked of DIFFERENT bars: the scroll
// answer is measured against the CEILING bar (the one WITH the scroll keys on
// it, because the tallest bar is the fixed point — bodyScrollsForBar), while
// drawability must be asked of the bar the frame really DRAWS (frameDrawn). One
// helper taking one `items` would have to get one of them wrong. The
// offset-scrolling sheets therefore spell the conjunction themselves, once each
// and named — po_detail's sheetMoves and padMoves and po_add_line's
// confirmScrolls are the shape, and every sheet that has grown an offset since
// (the two removal confirms, the slot-generate run report) names its own pair
// the same way — and each names the pair it is the handler's half of. What IS
// shared once the two gates have answered is jdeScrollStep, the key-to-offset
// mapping. (po_add_line's OTHER
// pager, keyChoose, moves a CURSOR through the candidate list and belongs to the
// paragraph above rather than to this one: it asks the two directly because it
// answers "nothing to page" out loud, not because it scrolls an offset.)

// moveRow steps a FIELD-form cursor by delta and WRAPS, and reports whether the
// move happened at all.
//
// Wrapping is the field form's rule and clamping is the list's (see
// jdeClampPick): a short form has no edge worth defending, and Down on the last
// of four fields belongs on the first. The false return is the refusal — a pane
// the frame is not drawn on, or a form with FEWER THAN TWO rows (jdeRowMoves,
// which is also what the bar asks before it names UP/DN) — and a sheet must
// leave EVERYTHING alone when it comes back, the focus included: re-focusing the
// same field is not a no-op if it restarts a caret blink or clears a selection.
func (g jdeScreen) moveRow(cursor, count, delta, headerRows int, items []actionBarItem) (int, bool) {
	if !jdeRowMoves(count) || !g.frameDrawn(headerRows, items) {
		return cursor, false
	}
	return (cursor + delta%count + count) % count, true
}

// pickRow steps a LIST cursor by delta and CLAMPS, and reports whether the move
// happened at all. jdeClampPick carries why a picker may not wrap; frameDrawn
// carries why it may not move at all on a refused pane; jdeRowMoves carries why
// a list of ONE reports false rather than clamping back onto the row it was
// already standing on — and why that is the same question the bar asks.
func (g jdeScreen) pickRow(cursor, count, delta, headerRows int, items []actionBarItem) (int, bool) {
	if !jdeRowMoves(count) || !g.frameDrawn(headerRows, items) {
		return cursor, false
	}
	return jdeClampPick(cursor+delta, count), true
}

// pageRow moves a cursor one PAGE through `body` and reports whether it moved.
//
// It is the ONE place both halves of the paging rule are asked, and it takes TWO
// bars because the two questions are asked of different ones:
//
//   - `drawn` is the bar the frame really DRAWS, and it answers DRAWABILITY.
//     tooShort is monotone in bar height, so gating on a taller bar would
//     decline a key at a height where the frame is on the pane — a named key
//     doing nothing on a pane the operator is looking at.
//   - `ceiling` is the bar WITH the paging keys on it, and it answers whether
//     a page DOES anything — bodyPagesForBar, which is the body moving AND
//     there being another row to land on. Naming the keys costs cells, cells
//     fold the bar onto another row, and a folded bar leaves the body one row
//     fewer, so the tallest bar is the fixed point and the answer cannot
//     oscillate between frames. It is the same obligation bodyAvailForBar and
//     bodyScrollsForBar already carry. The `count` handed in is the same one
//     the page steps through, so the bar's claim and this gate are the same
//     expression over the same numbers.
//
// The ceiling is THREADED rather than synthesised here. Appending a generic
// {"PgUp/PgDn", "Page"} to `drawn` would measure a bar no screen draws: the
// label differs per sheet — "Page", "Unit", "Last unit" — so the width, and
// therefore the fold, would differ from the real ceiling. An approximate fixed
// point is not a fixed point.
//
// WHY THE SCROLL HALF IS HERE AND NOT IN THE SHEETS. It was in two of them, and
// two is how the ~50 per-sheet copies sc-jde-lift had to unpick began: one that
// looked too small to be worth a shared function, with the same argument
// available to the next forty-nine. Before it moved here, every columnar sheet
// bound pgup/pgdown unconditionally while naming the pair only when the body
// overflowed, so on any pane tall enough to hold the whole body the bar rightly
// said nothing and PgDn still walked the cursor to the last row — 3254 of 7102
// drawn (screen, width, height) triples, which is what
// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves reports when the gate
// below is removed.
//
// AN UNSIZED TERMINAL IS NOT A SHORT PANE. bodyScrollsForBar answers false when
// there is no pane, because there is no window to overflow — but the layer's
// standing answer for an unsized screen is "draw the whole thing and let
// clampToBox decide", which is the same reason frameDrawn answers TRUE there. So
// the scroll half is skipped rather than answered from geometry that does not
// exist, and a screen driven without a WindowSizeMsg pages exactly as it always
// has.
//
// The STEP is the window's own row count (windowRowsForBar) measured against the
// bar really drawn, so a page covers exactly what the operator can see.
func (g jdeScreen) pageRow(body *jdeLines, cursor, count, dir, headerRows int, drawn, ceiling []actionBarItem) (int, bool) {
	if count <= 0 || !g.frameDrawn(headerRows, drawn) {
		return cursor, false
	}
	if g.paneRows() > 0 && !g.bodyPagesForBar(body, count, headerRows, ceiling) {
		return cursor, false
	}
	return jdePageCursor(cursor, count, g.windowRowsForBar(body, cursor, headerRows, drawn), dir), true
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
// purchasing screens bounded their message with a local helper of their own,
// and the other converted sheets handed an unbounded OMS error straight to a
// row that cannot fold (sc-jde-lift).
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
//
// Every character that would put the row on a second line becomes one space:
// the line breaks — "\r\n", "\n" and a bare "\r", and the vertical tab and form
// feed a terminal also moves the cursor down for — and the TAB. A tab breaks
// nothing, but lipgloss.Width measures it as NO cells while every lipgloss
// Render draws it as four, so a row the bound measured as fitting is drawn
// wider than the pane and wraps onto the next line anyway. Root's status bar
// flattens with this too (StatusBar.View), so both status surfaces keep one
// convention; TestRoot_TheFrameIsNeverTallerThanTheTerminal is where a shape
// this misses makes the frame grow.
func jdeStatusOneLine(s string) string {
	if !strings.ContainsAny(s, jdeStatusBreaks) {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(jdeStatusBreaks, r) {
			return ' '
		}
		return r
	}, s)
}

// jdeStatusBreaks is every character jdeStatusOneLine turns into a space.
const jdeStatusBreaks = "\r\n\t" + paneVerticalBreaks

// frame assembles a phase: the windowed body, padded out to the pane's budget,
// then the status line, then the persistent action bar at the bottom.
func (g jdeScreen) frame(body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	return g.frameWithHeader(nil, body, cursorRow, status, items)
}

// frameWithHeader is frame with lines PINNED above the scrollable body — a
// picker's filter box, the record a sub-form is amending. They cost the body its
// height and are drawn on every frame, so what the operator typed into the
// filter cannot scroll away under a long list.
//
// It is frameWrapped, and the two used to differ in the ONE way that mattered:
// this one drew renderActionBar, which puts every key on a single line,
// tightens the gutter until they fit and then lets the line RUN PAST the pane.
// At 80 columns the pane is 51 and eleven form screens name more than that —
// MaintenanceItemFormScreen's bar is 63 cells — so clampToBox cut the tail and
// the keys on it were unnamed while they went on working. That is the same
// defect renderActionBar's gutter loop exists to prevent, arriving one step
// later, and the fix is the bar that WRAPS: it costs the body only the rows the
// keys really need, and bodyAvailForBar is measured against the wrapped height
// so the frame still fills the pane exactly.
//
// Sheets keep calling frame / frameWithHeader because that is what they read as
// — a form does not care that its bar could fold — and nothing else about the
// two survives.
func (g jdeScreen) frameWithHeader(header jdeHeader, body *jdeLines, cursorRow int, status string, items []actionBarItem) string {
	return g.frameWrapped(header, body, cursorRow, status, items)
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
	//
	// AND IT IS FITTED, NOT MERELY CAPPED. jdePaneFieldWidth caps the 30-column
	// fill against the pane and renderJDEField then appends the hint AFTER it,
	// so this row was 70 cells on all nineteen picker sites while the pane at 80
	// columns is 51 — the widest single class in jdeRowsPastThePane, and the one
	// jdeOverWideEssentialRows recorded as the layer's to fix. The fit trades
	// the fill against the hint and folds the hint underneath when neither will
	// fit, which is what the fill is FOR.
	filterLabelW := jdeLabelWidth([]jdeField{filter})
	fitted, filterNotes := jdeFitRow(filter, filterLabelW, bodyWidth)
	header := jdeHeader(nil).
		add(jdeHeadDecorative, head).
		add(jdeHeadEssential, renderJDEField(fitted, filterLabelW, bodyWidth))
	// A folded hint is CONTEXT: the box is the row the operator cannot do
	// without, and jdeMinBudget lets a header mark exactly one row essential.
	header = header.add(jdeHeadContext, filterNotes...).add(jdeHeadDecorative, "")
	if p.Note != "" {
		// FOLDED against the live pane rather than written straight out: these
		// notes run to 81 cells ("Row 1 is no slot — ad-hoc storage. A claimed
		// slot wins over the location below.") against the 51 an 80-column
		// terminal gives, and clampToBox cut them mid-sentence with no mark.
		// addFittedBlock so a header trim re-draws the fold into the rows it
		// kept and marks its own cut, rather than leaving a fragment that reads
		// as the whole note (jdeHeader.addFitted).
		refit := func(rows int) []string { return jdeCaveatLinesIn(p.Note, bodyWidth, rows) }
		header = header.addFitted(jdeHeadContext, jdeHeadContext, refit(0), refit).
			add(jdeHeadDecorative, "")
	}

	body := &jdeLines{}
	if p.Count == 0 {
		empty := p.Empty
		if empty == "" {
			empty = "(no matches)"
		}
		// Folded, for the reason p.Note is: an empty-state sentence is prose
		// and a pane is 51 cells at the width this interface is designed to.
		for _, line := range jdeCaveatLines(empty, bodyWidth) {
			body.Add(line)
		}
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

// ---------------------------------------------------------------------------
// The movement pair's one question
// ---------------------------------------------------------------------------

// jdeRowMoves is UP/DN's ONE question, asked by the BAR that claims the pair
// and by the ARM behind it: there must be a SECOND row to move to.
//
// It is bodyPagesForBar's first conjunct, pulled out and named, and it is here
// for the same reason that one is: three sheets had worked it out for
// themselves (po_attachments' listMoves, service_status_screen nesting its
// entry inside len(services) > 1, po_create's barItems on rowCount() > 1) and
// thirty had not — while jdePickBarWith, the bar EVERY columnar picker draws,
// appended {"UP/DN", "Move"} with no condition on it at all.
//
// WHY count > 1 AND NOT count > 0, which is what the arms used to ask. A LIST
// cursor CLAMPS (jdeClampPick) and a FIELD cursor WRAPS (moveRow), and at a
// count of one both land back on the row they started on: the clamp returns the
// only index there is, the wrap is modulo one. So the arm returned ok, the sheet
// stored a cursor identical to the one it had, no note was written, and the pane
// redrew byte for byte under a bar saying UP/DN=Move — standing rule 1 broken by
// arithmetic, on the state a list SPENDS MOST OF ITS LIFE IN. It is reachable
// with no fixture at all: filter a single-select picker to a query nothing
// matches and the synthetic "(none)" row is left standing (it is prepended
// before the filter runs), so Count is 1 and Down clamps to where it was.
//
// COUNT ZERO IS THE SAME ANSWER FOR A DIFFERENT REASON and is deliberately not
// spelled separately: a cursor with nothing to point at cannot move either.
//
// DRAWABILITY IS NOT PART OF IT. A refused pane draws no bar to make a claim
// with (jdeTooShort replaces it), so the bar's question is about the ROWS alone;
// the arms ask frameDrawn on top, as they always have.
func jdeRowMoves(count int) bool { return count > 1 }

// jdeCeilingRows is the row count that names every movement key: the count a
// CEILING bar is measured at.
//
// A ceiling bar exists because naming keys costs cells, cells fold the bar onto
// another row, and a folded bar leaves the body one row fewer — so every budget
// is measured against the TALLEST bar the screen can draw, or the answer
// oscillates between frames. UP/DN can now come OFF a bar, so the ceiling has to
// put it back: measured at the live count, a picker filtered down to one row
// would budget against a bar shorter than the one a second row restores.
//
// Two rather than a bool, so the ceiling goes through the same predicate the
// live bar does and cannot drift from it.
const jdeCeilingRows = 2

// jdePickBar is the bar every picker draws: Enter and Esc always, plus the
// movement keys the picker's CURRENT option count can honour.
//
// `count` is the option count the cursor is standing in, and it is an ARGUMENT
// rather than something this file could work out, for the reason jdePickList
// takes Count/Label/Dim: the layer does not know what is being picked. Every
// caller has it in hand — it is the same len() it hands jdePickList.Count and
// bodyPagesForBar — so passing it costs nothing and makes the bar's claim and
// the arm behind it the same expression over the same number.
func jdePickBar(verb string, count int, paging bool) []actionBarItem {
	return jdePickBarWith(verb, "Cancel", count, paging)
}

// jdePickBarWith is jdePickBar for a picker whose Esc does not mean "cancel".
// A MULTI picker applies each toggle as it is made, so there is nothing left to
// undo and leaving IS the commit — the bar has to say "Done" or it is lying
// about what the key does. (Enter toggles there, because the always-live filter
// owns space; sweep B's required-certifications row is the first of these.)
//
// UP/DN IS CONDITIONAL NOW, and this is the site AGENTS.md recorded the gap at:
// it appended the pair with no condition on it while being the bar EVERY
// columnar picker draws, so one edit here is one edit and not one per screen.
// jdeRowMoves carries why a count of one is the state it fails in.
func jdePickBarWith(commit, cancel string, count int, paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", commit}, {"Esc", cancel}}
	if jdeRowMoves(count) {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// jdeMoveItem is the UP/DN entry for a CURSOR over `count` rows — and nothing
// at all where a move cannot happen.
//
// It exists so a bar builder that is not a picker's asks the SAME question
// jdePickBarWith does rather than appending the pair as a literal. The paging
// pair already had that shape (bodyPagesForBar, asked by the bar and by
// pageRow); UP/DN did not, and the sites that had worked the condition out for
// themselves — po_attachments' listMoves, service_status_screen nesting its
// entry inside len(services) > 1, po_create's barItems on rowCount() > 1 —
// were three out of thirty.
//
// The LABEL is the caller's because it names what is being moved through
// ("Levels", "Components", "Lines", the sub-list's own noun) and the layer does
// not know; the CONDITION is not, for the reason jdeRowMoves gives.
//
// A SCROLL offset is not this: {"UP/DN", "Scroll"} moves a window over a
// read-only body rather than a cursor over rows, so its question is
// bodyScrollsForBar and the sheets that draw one ask it themselves (po_detail's
// sheetMoves, po_add_line's confirmScrolls) because the two questions are asked
// of different bars.
func jdeMoveItem(label string, count int) []actionBarItem {
	if !jdeRowMoves(count) {
		return nil
	}
	return []actionBarItem{{"UP/DN", label}}
}

// jdePickBarCeiling is the TALLEST bar this picker can draw: every movement key
// on it, whatever the live option count is.
//
// It is what a BUDGET is measured against — bodyAvailForBar, bodyScrollsForBar
// and bodyPagesForBar all carry the obligation that the bar handed to them be
// the tallest the screen can draw, because a bar that folds onto another row
// leaves the body one row fewer and the answer must not oscillate between
// frames. Before UP/DN could come off a bar the live bar WAS the ceiling for
// everything but paging; now a picker filtered down to one row draws a shorter
// bar than a second row restores, so the ceiling has to name the pair back.
func jdePickBarCeiling(commit, cancel string) []actionBarItem {
	return jdePickBarWith(commit, cancel, jdeCeilingRows, true)
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

// ---------------------------------------------------------------------------
// A bar with more keys than one line holds
// ---------------------------------------------------------------------------
//
// A bar that puts every item on ONE line, tightening the gutter until they fit,
// is the whole story for a form: the pilot's bar names four or five keys and the
// widest of them still fits the 49 columns an 80-column terminal leaves the
// pane. That is what every frame in the program used to draw.
//
// A VIEWING screen is the case it does not cover. A purchase order's detail
// (po_detail.go) carries a dozen order-level commands at once — receive, edit,
// attachments, ship, send, confirm, deliver, void, order pad, refresh, plus
// scrolling — and no tightening puts twelve of them on one 49-column line. The
// line simply ran off the end of the pane and clampToBox cut it, which is the
// exact failure the gutter loop exists to prevent: "a key that fell off the bar
// is a key the operator cannot discover".
//
// So the bar WRAPS instead. That is also what the real thing does — a JD
// Edwards World screen carries two or three rows of F-key legend under the
// rule, not one — and it costs the body only the rows the keys actually need.
//
// actionBarKeyLines below is now the ONE bar renderer in the program, and the
// single-line renderer it replaced is gone rather than kept beside it: two
// renderers is two answers to "how tall is the bar", and every budget in this
// file is measured against that number. Nothing is lost by the merge, because
// its first branch IS the old renderer — the same gutter tightening, returning
// one line whenever the items fit on one — so a form whose bar already fitted
// draws byte for byte what it drew before.

// actionBarKeyLines lays the bar's items out over as many lines as it takes,
// each already carrying jdeIndent. One line is returned whenever the items fit
// on one, by the tighten-the-gutter rule the single-line renderer used before
// this absorbed it — so a bar with few enough keys renders identically.
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
	// One line if they fit on one, gutter tightening first — byte-identical to
	// what the single-line renderer drew for every bar that was already legible,
	// which is what let it be deleted rather than kept beside this.
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

// renderActionBarWrapped is the program's ONE bar renderer: the rule, then every
// key line actionBarKeyLines lays out.
//
// "Wrapped" names what it can do rather than what it always does, and there is
// no non-wrapping sibling to reach for instead — actionBarKeyLines returns a
// single line whenever the keys fit on one, by the same gutter tightening the
// separate one-line renderer used before this absorbed it, so a bar that already
// fitted draws byte for byte what it drew then.
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
// `avail` is what bodyAvailForBar answers, and zero from it has exactly one
// meaning now: the terminal is UNSIZED, so there is no pane to
// window against and nothing can scroll. A pinned header that fills the pane
// used to be a second cause and is not — read jdeBodyAvail for why, and for why a
// pane the frame is REFUSED on is not one either. That second correction is
// what stops this answer flipping at the refusal boundary and shrinking the bar
// the refusal notice is measured against.
//
// jde:layer-only — a sheet asks bodyScrollsForBar.
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
// THE SECOND SENTENCE HAS BEEN WRONG TWICE, in opposite directions, and both
// wordings are worth keeping because the next edit to it will be a third.
//
// It first said "They still work", which an operator reads as "so go ahead and
// press them" — and pressing them was exactly what they must not do, because
// Update was untouched and every key acted on a screen that was not drawn.
// It then said so in as many words: the keys act, invisibly, and `end` on the
// order pad throws away where you had scrolled to. That was HONEST and it was
// not a fix — it described a loss that was still happening.
//
// The loss is gone now (see the movement block above: every arm that moves the
// operator's place is gated on frameDrawn), so the sentence says what is true
// NOW — the keys that move you are held, and where you are is kept — and no
// longer describes a defect the code does not have. A notice claiming a loss
// that cannot happen is the same kind of lie as one denying a loss that can.
//
// It does NOT claim that nothing at all happens. TYPING is not gated, and nor
// is esc: the way out of a pane too short to work in must stay open, or the
// refusal is one an operator cannot act on. Esc is named for that reason and
// it is the only key named here — a legend is what this notice exists to
// withhold, and the one key that gets the operator out of the state is not a
// legend.
//
// WHERE THE GATE IS, so the next reader does not go looking for it here. It is
// on the MOVEMENT PRIMITIVES (frameDrawn / moveRow / pickRow / pageRow, above),
// because the drift happens in the SHEET's handler before a frame is ever
// called and there is nothing this function could preserve that has not already
// been destroyed. Two narrower answers were tried and rejected:
//
//   - frameScrolled already hands the offset back untouched on this path, which
//     faithfully preserves a value the handler overwrote a moment earlier.
//   - a layer-side stash of the last DRAWN offset fixes the two frameScrolled
//     screens and leaves the CLASS — the same drift hits the CURSOR on every
//     frame / frameWrapped screen, which is thirty more of them.
//
// It is bounded in both axes by the layer, not by clampToBox: `rows` lines of
// at most `width` cells. A notice that was itself cut would be the defect it
// exists to report — and the HEIGHT FACT leads, because a one-row pane keeps
// only the first line and the height is the one thing on here that can be acted
// on.
//
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
		"No keys are named: the action bar would be cut. Moving keys are held "+
			"until it fits, so you come back where you were. Esc still leaves.", width)...)
	if len(lines) > rows {
		// MARK the cut. A pane of three rows keeps the height fact and two
		// lines of the sentence under it, and a sentence cut clean reads as a
		// finished one — the operator is told "Moving keys are held until it
		// fits, so you come" and has no way to know a clause is missing. Every
		// other bound on these screens carries its ellipsis; this one is the
		// notice the operator can act on, so it carries one too.
		lines = foldKeepRows(lines, rows, width)
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
		// one row (bodyAvailForBar), so there is always something to clamp
		// against.
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

// jdeScrollStep is what one movement key does to a read-only body's OFFSET: the
// offset analogue of pickRow / moveRow / pageRow, which move a cursor.
//
// It exists because the switch it replaces had already been written out by hand
// THREE times across TWO files — po_detail's handleSheetKey and its order pad's
// handleOrderPadKey, and po_add_line's keyConfirm — and this change puts three
// more sites on the same footing: the two removal confirms and the slot-generate
// run report, none of which had an offset to map a key onto before it. Three
// copies of one mapping with three more arriving is how the ~50 copies of the
// scroll ARITHMETIC that sc-jde-lift had to unpick began, and a count is the
// kind of recorded history a later reader greps: six pre-existing copies is not
// what they would find. Whether a key acts at all is still the SHEET's question:
// the two gates
// (is the frame drawn, does the body move) are asked of different bars and are
// spelled at each site.
//
// The CLAMP is deliberately one-sided. Zero is the top and can be answered from
// nothing; the bottom depends on the pane, which only the frame knows, so
// frameScrolled clamps what it is about to draw and hands the offset back for
// the sheet to store. `end` therefore asks for the WHOLE body and is brought
// back by the frame — which is what makes "↓ 0 more below" impossible.
//
// It carries no layer-only marker, and the omission is deliberate rather than an
// oversight: that marker names the BUDGET answers a sheet may not compute for
// itself, and the sweep derived from it (TestJDEForm_NoSheetAnswersTheScroll-
// QuestionItself) forbids a sheet from naming one at all. This is the opposite
// kind of helper — a primitive the sheets are meant to reach for, like pickRow
// and pageRow, neither of which is marked either. Do not add the marker to make
// it look consistent with its neighbours; it would forbid every call site.
func jdeScrollStep(key string, offset, lines, step int) int {
	switch key {
	case "up":
		offset--
	case "down":
		offset++
	case "pgup":
		offset -= step
	case "pgdown":
		offset += step
	case "home":
		offset = 0
	case "end":
		offset = lines
	}
	if offset < 0 {
		return 0
	}
	return offset
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
// A jdeValue or jdeChoice row has no fill to shrink, so it gives ground in the
// other order and jdeFitValueRow is that half — read it for why.
//
// bodyWidth of 0 means the pane is not sized yet, which — as everywhere in this
// file — means "do not truncate".
func jdeFitRow(f jdeField, labelWidth, bodyWidth int) (jdeField, []string) {
	if bodyWidth <= 0 {
		return f, nil
	}
	if f.Kind != jdeText {
		return jdeFitValueRow(f, labelWidth, bodyWidth)
	}
	lead := len(jdeIndent) + labelWidth + len(jdeLeader) + jdeSwatchCost(f)
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

// jdeFitValueRow is jdeFitRow for a row with no input area: a jdeValue reading
// or a jdeChoice set. It is the half the layer used to leave to the sheets, and
// leaving it to them is why twenty-six columnar screens drew a row past the pane
// at 80 columns — the width the whole interface is designed to.
//
// jdePaneFieldWidth caps a TEXT row's fill and says in as many words that a
// value row's width "is a content decision each sheet already makes for itself".
// The sheets did not make it. `Affects ..... Sending notifications, reorder
// alerts, and receipts` is 67 cells against the 51 an 80-column terminal gives
// and `Max load ..... 80% of breaker amperage  set by the backend on save` is
// 81; clampToBox took the tail off both with no mark, so a reading stopped
// mid-word and read as finished.
//
// THE GIVE-ORDER IS THE OPPOSITE WAY ROUND FROM A TEXT ROW, and the reason is
// what each part IS. On a text row the fill gives first because the fill is
// decoration — underscores, or the reverse-video run that says "type here" —
// and shortening it loses nothing at all. A value row has no such part: the
// VALUE is the content and the HINT is fixed prose about the row. So the HINT
// gives first, by FOLDING under the row (jdeNoteLines), which costs a line and
// loses nothing; only when the value still will not fit on its own is it
// CLIPPED, with fitCell's ellipsis saying so. Folding narrow costs a line,
// clipping narrow destroys the tail — so the lossless move comes first.
//
// A jdeChoice draws its value inside "< " and " >", and the brackets are what
// says the row is a SET rather than a reading, so they are reserved and the
// value inside them is what gives. A Swatch is reserved too (jdeSwatchCost).
func jdeFitValueRow(f jdeField, labelWidth, bodyWidth int) (jdeField, []string) {
	room := bodyWidth - (len(jdeIndent) + labelWidth + len(jdeLeader)) - jdeSwatchCost(f)
	deco := 0
	if f.Kind == jdeChoice {
		deco = lipgloss.Width("< ") + lipgloss.Width(" >")
	}
	hintCost := 0
	if f.Hint != "" {
		hintCost = 2 + lipgloss.Width(f.Hint)
	}
	if lipgloss.Width(f.Value)+deco+hintCost <= room {
		return f, nil
	}
	var notes []string
	if f.Hint != "" {
		note := f.Hint
		f.Hint = ""
		notes = jdeNoteLines(note, labelWidth, bodyWidth)
	}
	avail := room - deco
	if avail < 1 {
		// A pane this narrow is not reachable under the size contract, but a
		// clip to nothing would be a value gone with no mark saying so — the
		// one reading a truncation must never produce. One cell is the mark.
		avail = 1
	}
	if lipgloss.Width(f.Value) > avail {
		f.Value = fitFactCell(f.Value, avail)
	}
	return f, notes
}

// AddFittedFields appends one navigable row per field, numbered from rowBase,
// each sized to the pane by jdeFitRow — and any hint that had to be folded is
// added as further lines of the SAME row, so the window keeps a field and the
// note explaining it on screen together. A rowBase of jdeNoRow means the whole
// band is read-only, so EVERY line of it is tagged jdeNoRow rather than
// -1, 0, 1… — see jdeRowAt.
//
// IT IS THE ONLY BAND BUILDER, and the unfitted AddFields beside it is gone
// rather than deprecated. Fitting was a LAYOUT DECISION a sheet opted into, and
// the sheets that never did are exactly the twenty-six screens
// jdeRowsPastThePane recorded as drawing a row past the pane at 80 columns: the
// fill was capped by jdePaneFieldWidth and the hint after it was not, so the
// only thing on the row saying what to type was the thing clampToBox took. An
// opt-in whose every caller should have opted in is not a decision, it is a
// trap, so there is nothing left to opt out of.
func (l *jdeLines) AddFittedFields(fields []jdeField, labelWidth, bodyWidth, rowBase int) {
	for i, f := range fields {
		l.AddFittedField(jdeRowAt(rowBase, i), f, labelWidth, bodyWidth)
	}
}

// AddFittedField is AddFittedFields for ONE field at a row the caller names.
//
// It exists because most of this package's forms do not draw a band of fields
// and nothing else: they loop over their own field list and hang an option
// strip, a derived preview or a note off whichever row is focused, so they
// cannot hand the whole band to AddFittedFields. Every one of those loops called
// renderJDEField directly, which is the FLOOR (jdePaneFieldWidth caps the fill
// and nothing else) rather than the FIT — so the hint drawn after the fill was
// past the pane on every one of them, at every width, and clampToBox took it
// with no mark. `Manual PDF path ..... ____  absolute local path` is 72 cells
// against the 51 an 80-column terminal gives.
//
// A row is drawn HERE rather than at each site so that the fold's extra lines
// are tagged with the SAME row as the field they belong to: jdeLines.Window
// anchors on the cursor's block, so a note that took a row number of its own
// would be a line the cursor could stand on with nothing to type into.
func (l *jdeLines) AddFittedField(row int, f jdeField, labelWidth, bodyWidth int) {
	fitted, notes := jdeFitRow(f, labelWidth, bodyWidth)
	l.AddRow(row, renderJDEField(fitted, labelWidth, bodyWidth))
	for _, note := range notes {
		l.AddRow(row, note)
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
	return jdeCaveatLinesIn(note, bodyWidth, 0)
}

// jdeCaveatLinesIn is jdeCaveatLines drawn into at most `rows` lines, the last
// of them marked where that drops any (foldKeepRows); zero or less is "no
// limit". It is the refit a pinned header re-draws a caveat with when the pane
// cannot give it every row (jdeHeader.addFitted): the header used to drop the
// caveat's tail rows, and a caveat's tail is where the remedy falls.
func jdeCaveatLinesIn(note string, bodyWidth, rows int) []string {
	return jdeCaveatLinesStyled(note, bodyWidth, rows, StyleMuted)
}

// jdeCaveatLinesStyled is jdeCaveatLinesIn in a style of the caller's choosing.
//
// A caveat is usually muted, but a VALIDATION message is not a standing note —
// it says the sheet will not save — and it is drawn in StyleStatusWarn. Those
// messages were the last unfolded prose in the columnar layer: the packaging
// chain's `! Packaging level "Case" must hold fewer base units than "Pallet"
// that contains it.` is 85 cells against the 51 an 80-column terminal gives,
// and one of them is the header's ESSENTIAL row, so what clampToBox cut was the
// one row the builder promised the operator would keep.
func jdeCaveatLinesStyled(note string, bodyWidth, rows int, style lipgloss.Style) []string {
	width := 0
	if bodyWidth > 0 {
		if width = bodyWidth - len(jdeIndent); width < 1 {
			width = 1
		}
	}
	wrapped := jdeWrapNote(note, width)
	if rows > 0 && len(wrapped) > rows {
		wrapped = foldKeepRows(wrapped, rows, width)
	}
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, jdeIndent+style.Render(line))
	}
	return out
}
