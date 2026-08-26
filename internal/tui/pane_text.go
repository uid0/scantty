// Pane-local text bounds — the folding, clipping and row-fitting every screen
// in this package shares, columnar or not.
//
// It lived in po_create_pickers.go until the New PO conversion (sc-jde-poc),
// which is why everything here is named for the pickers it was written for.
// That was already the wrong home: jde_form.go's own status bound calls
// cellPrefix, receive_form.go and po_add_line.go fold their notes with
// pickerWrap, and list.go — which is deliberately NOT on the columnar layer and
// must not have to join it to be legible at 80 columns — folds its footer with
// pickerHint. Left where it was, the last two unconverted screens looked like
// they were carrying a design system of their own; moved here, what is shared
// is visibly shared and what was local to those two files went with them.
//
// Nothing was rewritten in the move. The one thing worth knowing before
// touching any of it is why cellPrefix exists beside layout.go's
// truncateVisible: truncateVisible drops ONE rune off the end and re-measures
// the whole remaining string, so it is O(n²), and every bound here is handed
// OMS response bodies — omsapi.parseError puts the ENTIRE raw payload into
// APIError.Message whenever the JSON envelope carries no code. Measured on a
// 20 KB gateway page: 711ms for one clip, 1.5s to fold a fifth of it. These
// walk FORWARD and stop when the budget is spent, so their cost is the budget
// rather than the length of what they were handed.

package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Saying something — the rule these pickers broke
// ---------------------------------------------------------------------------

// Every operator action on these screens that performs work off the terminal
// must report that it is WORKING, and must report FAILURE. A key that declines
// to act must say why. The pickers used to answer several keys with a bare
// `return s, nil`, which redraws a screen byte-for-byte identical to the one
// before the press — and from the operator's seat a keystroke that changes
// nothing and says nothing is indistinguishable from a wedged program.
//
// That is what the "the screen just hangs after I press enter" report actually
// was. Nothing was blocked and nothing was in flight: enter inside the item
// picker's search box only CLOSED the box (the pick needed a second enter), and
// enter over an empty filtered list returned nil. Both redrew the same pixels,
// so the operator concluded the program had stopped. The three states below —
// working, succeeded, failed — are now all visible, and no arm of these
// switches is allowed to be silent.

// pickerNote is a picker's own answer to the last keypress, rendered in the
// screen BODY. The status bar carries the same words, but a Flash expires after
// four seconds (status.go) and the operator who pressed enter and saw nothing is
// exactly the operator still staring at the picker a minute later — so the body
// line is the one that has to survive.
type pickerNote struct {
	text  string
	level StatusLevel
}

// render styles the note for the BODY, folded to the pane by pickerWrap. text
// may also carry explicit newlines, which stay as forced breaks.
//
// Folding is not cosmetic here. Root.View() TRUNCATES the pane rather than
// wrapping it, and the tail of one of these sentences is where the key that
// gets the operator OUT of the state is named — so a clipped hint is the
// silence this whole file exists to remove, wearing a tick mark, and it is
// worse than no hint at all because the operator believes they read it.
func (n pickerNote) render() string {
	return strings.Join(n.renderLines(pickerPaneWidth), "\n")
}

// renderLines is render's whole body, folded to the width the CALLER has rather
// than to the 51 columns the narrowest supported terminal gives.
//
// It exists because the columnar screens draw the same note into a pane whose
// width they read off the live terminal (po_add_line.go's noteLines), and
// clipping a note to 51 cells on a 120-column terminal throws away what the
// pane had room for. Only the width differs, so only the width is a parameter:
// the mark, the styling and the first-line-versus-continuation split live here
// once, and a new StatusLevel is added in one place.
//
// `width` is the room the note has BEFORE the mark, which this function
// subtracts, so a caller budgets against its own pane and nothing else.
func (n pickerNote) renderLines(width int) []string {
	if n.text == "" {
		return nil
	}
	mark, style := "", StyleMuted
	switch n.level {
	case StatusError:
		mark, style = "✗ ", StyleStatusError
	case StatusWarn:
		mark, style = "! ", StyleStatusWarn
	case StatusOK:
		mark, style = "✓ ", StyleStatusOK
	}
	// The mark eats two cells of the first line. Budgeting it off every line is
	// two columns conservative on the continuations and costs nothing.
	if mark != "" {
		width -= lipgloss.Width(mark)
	}
	lines := pickerWrap(n.text, width)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, style.Render(mark+line))
			continue
		}
		// Continuation lines are muted and already indented by pickerWrap:
		// they carry the way OUT of the state, not the state itself.
		out = append(out, StyleMuted.Render(line))
	}
	return out
}

// flash is the note reduced to ONE line for the status bar, which has no room
// for the continuation.
func (n pickerNote) flash() string {
	if i := strings.IndexByte(n.text, '\n'); i >= 0 {
		return n.text[:i]
	}
	return n.text
}

// say records the note and flashes the same words on the status bar. Both, not
// either: the bar is where an operator's eye already goes for "did that work",
// and the body line is what is still there once the flash has gone.
func (n *pickerNote) say(text string, level StatusLevel) tea.Cmd {
	n.text, n.level = text, level
	return Status(n.flash(), level)
}

// cellPrefix returns the longest prefix of text that draws within max CELLS.
// It is the measurement half of every bound on these screens, and it makes ONE
// forward pass: it stops as soon as the budget is spent, so its cost is the
// budget rather than the length of what it was handed.
//
// That is the whole reason it exists beside truncateVisible (layout.go), which
// does the same job by dropping ONE rune off the end and re-measuring the whole
// remaining string — O(n²) with an O(n) allocation per step. Harmless on a
// label; not on the values these screens clip, because omsapi.parseError puts
// the ENTIRE raw response body into APIError.Message whenever the JSON envelope
// carries no code, and the source chooser re-renders the row carrying it about
// ten times per frame. Measured against a 20 KB gateway page: 711ms for one
// clip, and 1.5s for one fold of a 5 KB unspaced token — seconds of freeze per
// keystroke, which is the symptom this whole change exists to remove.
//
// Escape sequences are stepped over rather than measured, and the scan only
// ever returns on a boundary between them, so a cut never splits one and never
// bleeds colour into the next column — the property truncateVisible's doc
// comment is about. A rune's width is asked of lipgloss one rune at a time,
// which over-counts a multi-rune grapheme cluster (an emoji built from a ZWJ
// run) rather than under-counting it: the error is on the side of clipping
// early, so the result is never WIDER than the budget it was given.
func cellPrefix(text string, max int) string {
	if max <= 0 {
		return ""
	}
	const (
		plain = iota
		afterEsc
		inEsc
	)
	state, used := plain, 0
	for i, r := range text {
		switch state {
		case afterEsc:
			state = inEsc
			continue
		case inEsc:
			if (r >= 0x40 && r <= 0x7e) || r == 0x07 {
				state = plain
			}
			continue
		}
		if r == 0x1b {
			state = afterEsc
			continue
		}
		w := lipgloss.Width(string(r))
		if used+w > max {
			return text[:i]
		}
		used += w
	}
	return text
}

// pickerClip bounds an operator-supplied string before it goes into a note. The
// pane is 51 columns at the terminal's narrowest supported width and Root.View()
// truncates, so an unbounded search term or supplier name would push the rest of
// the sentence — the part naming the key to press — off the right edge.
//
// `max` is CELLS, not runes, because every caller computes its budget in cells
// (lipgloss.Width against pickerPaneWidth). Counting runes here made the bound
// disagree with the budget it was asked for: one CJK or emoji rune is two
// cells, so a clipped value could render twice as wide as the room reserved for
// it and clampToBox would take the tail — the very cut the clip exists to stop,
// reached with a different alphabet.
//
// Nothing here measures the WHOLE string: cellPrefix stops at the budget, so
// clipping a multi-KB OMS error body costs the same as clipping a supplier
// name. An unbounded value reaching a clip must stay cheap, because the row
// carrying one is redrawn on every keystroke.
func pickerClip(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if head := cellPrefix(text, max); head == text {
		return text
	}
	if max <= 1 {
		return cellPrefix(text, max)
	}
	return cellPrefix(text, max-1) + "…"
}

func (n *pickerNote) clear() { n.text, n.level = "", StatusInfo }

// pickerPaneWidth is the columns a picker frame actually gets at the narrowest
// terminal this project checks against: Root.View() clamps the body to
// screenBodyWidth(80) = 51 (AGENTS.md) and TRUNCATES what does not fit.
//
// Every note and every fixed hint on these screens is folded to it rather than
// hand-counted against it. Hand-counting is what produced the class of bug this
// constant exists to close — a note reads fine at the width its author had in
// mind and then grows a prefix, a supplier name or a match count and silently
// loses the key it was written to name.
var pickerPaneWidth = screenBodyWidth(80)

// pickerWrap folds text onto as many lines as it needs to fit width, and
// indents every line after the first by two so a folded sentence still reads as
// one. Explicit newlines in text are forced breaks.
//
// It folds at the " · " joints these hints are built from before it falls back
// to spaces, because those joints separate whole claims ("b picks another line
// source", "esc cancels the order") and a claim split across two lines is
// harder to read than one claim per line. The separator itself is dropped at a
// fold — the indent already says the line is a continuation.
func pickerWrap(text string, width int) []string {
	const indent = "  "
	if width < 12 {
		width = 12
	}
	var out []string
	budget := func() int {
		if len(out) == 0 {
			return width
		}
		return width - lipgloss.Width(indent)
	}
	push := func(line string) {
		if len(out) == 0 {
			out = append(out, line)
			return
		}
		out = append(out, indent+line)
	}
	for _, para := range strings.Split(text, "\n") {
		cur := ""
		flush := func() {
			if cur != "" {
				push(cur)
				cur = ""
			}
		}
		for _, seg := range strings.Split(para, " · ") {
			if seg == "" {
				continue
			}
			if cur != "" && lipgloss.Width(cur)+3+lipgloss.Width(seg) <= budget() {
				cur += " · " + seg
				continue
			}
			flush()
			if lipgloss.Width(seg) <= budget() {
				cur = seg
				continue
			}
			// One claim too long for a line of its own — fold it on spaces
			// rather than let clampToBox take the end off it.
			for _, word := range pickerWords(seg, width-lipgloss.Width(indent)) {
				switch {
				case cur == "":
					cur = word
				case lipgloss.Width(cur)+1+lipgloss.Width(word) <= budget():
					cur += " " + word
				default:
					flush()
					cur = word
				}
			}
		}
		flush()
	}
	return out
}

// pickerWords splits a run of text into pieces no wider than width, breaking
// mid-token when a token is wider than that on its own. The tokens that need it
// are not English: a failed lookup carries whatever OMS put in the response
// body, and a URL or an unspaced JSON blob has nowhere to fold. Better to break
// one in the middle than to hand clampToBox a 200-cell line and lose all but
// the first 51 of it.
func pickerWords(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	var out []string
	for _, word := range strings.Fields(text) {
		for {
			// The cut is measured in CELLS: slicing `width` RUNES off a
			// double-width token would hand back a piece up to twice the line
			// it was cut to fit. cellPrefix walks forward and stops at the
			// budget, so a token is split in one pass over it rather than one
			// pass per piece — an unspaced 5 KB body took 1.5 seconds to fold
			// when each piece re-measured the rest of the token.
			head := cellPrefix(word, width)
			if head == word || head == "" {
				break
			}
			out = append(out, head)
			word = word[len(head):]
		}
		if word != "" {
			out = append(out, word)
		}
	}
	return out
}

// pickerHint renders a fixed muted line — a way out, a working line, a summary,
// a list's action bar — folded to the pane. Nothing writes such a line directly
// any more: routing them all through one function is what keeps the next one
// from being the one that overruns.
//
// Named for the pickers it was written for, but it is the project's pane-local
// folder generally, and deliberately outside internal/tui/jde_form.go: the list
// screens and the New PO help line are not on the columnar layer and must not
// have to join it just to be legible at 80 columns.
func pickerHint(text string) string {
	lines := pickerWrap(text, pickerPaneWidth)
	for i, line := range lines {
		lines[i] = StyleMuted.Render(line)
	}
	return strings.Join(lines, "\n")
}

// windowedListCaretCells is the fixed gutter every row carries — four cells,
// highlighted ("  ▸ ") or not ("    ").
const windowedListCaretCells = 4

// windowedListRoom is the cells a formatted row may draw into.
//
// The HIGHLIGHT's padding is reserved on every row, not only the highlighted
// one: StyleSidebarItemActive pads what it wraps, so a row that fits until it
// is selected is a row the pane cuts on exactly the press that stages it — and
// the padding is asked of the style rather than counted, the same way
// renderCart asks.
// `width` is the pane the row is actually drawn into, not the 51-column pane
// this project checks against: clipping a name to 45 cells on a 120-column
// terminal discards what the pane had room for (paneWidth, po_create.go).
func windowedListRoom(width int) int {
	room := width - windowedListCaretCells - StyleSidebarItemActive.GetHorizontalPadding()
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	return room
}

// poRowDropMark is what a row leaves behind when it gives a trailer up. A
// caller that bounds its own identifiers reserves these cells too: the mark is
// spent out of the same `room` everything else is measured against, and a row
// that overflows BECAUSE it said it was short is the defect twice over.
const poRowDropMark = "  …"

// poFitRow assembles one windowed-list row inside `room` cells with a STATED
// order of sacrifice, the same shape the cart row gives its own parts.
//
// Every part of the row is one of two things and there is no third: a BOUNDED
// IDENTIFIER, or a FACT THAT NEVER GIVES. `name` is an identifier and is what
// this function abbreviates — a shortened one is still recognisable beside the
// code the operator typed. `facts` never give: they are the price and the
// quantity, the numbers a picker exists to be read for, and a number cut by
// clampToBox is worse than an absent one because "@ 3." reads as a whole
// price. trailers are the row's decorations and are dropped from the LAST one
// backwards, keeping the columns that remain in the order they were written —
// column position is how a columnar row is read.
//
// So `facts` must be BOUNDED BY ITS CALLER, and a caller that puts an
// OMS-supplied string in there has not bounded the row: a bound expressed in
// terms of an unbounded value is not a bound. An item's SKU and an asset's tag
// are identifiers that happen to sit in the facts column, and each is clipped
// against what the price and the name's floor leave before it ever gets here —
// a 32-cell manufacturer part number used to push the unit price off the pane
// and draw "@ 3.".
//
// Whatever it shortens says so: the name keeps pickerClip's ellipsis, and a
// dropped trailer leaves one of its own at the end of the row, so a row that
// gave something up never reads as a whole one.
func poFitRow(room int, name, facts string, trailers ...string) string {
	need := lipgloss.Width(name)
	if need > poHeaderValueFloor {
		need = poHeaderValueFloor
	}
	tail := func(n int) string { return strings.Join(trailers[:n], "") }
	keep, dropped := len(trailers), ""
	for keep > 0 {
		spent := need + lipgloss.Width(facts) + lipgloss.Width(tail(keep)) + lipgloss.Width(dropped)
		if spent <= room {
			break
		}
		keep--
		// Only a trailer that CARRIED something leaves a mark. Most rows offer
		// an empty trailer (an item with no case pack, an asset with no serial)
		// and a row that marked one of those would be claiming a cut nobody
		// made — and paying three cells of the very budget it is short of for
		// the claim. Spaced off the column before it, because an ellipsis
		// butted against the last surviving fact reads as THAT fact having been
		// cut, which is the mangled-value defect this bound exists to stop.
		if trailers[keep] != "" {
			dropped = poRowDropMark
		}
	}
	suffix := facts + tail(keep) + dropped
	space := room - lipgloss.Width(suffix)
	if space < need {
		space = need
	}
	return pickerClip(name, space) + suffix
}
