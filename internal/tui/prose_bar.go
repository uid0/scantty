package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// prose_bar.go — the action bar for the screens that are NOT on the columnar
// layer and are NOT a *ListScreen, as a RECORD rather than a literal.
//
// THE DEFECT THIS EXISTS FOR. Those screens write their footer as a muted
// literal straight into a strings.Builder inside View:
//
//	hint := "j/k scroll · pgup/pgdn page · r refresh · esc back"
//	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
//
// Two things follow from that shape and both of them reached an operator.
//
//  1. THE BAR OMITS KEYS THAT ACT. Most of those screens are detail sheets
//     holding a TextScroller, whose Handle binds j/k, the arrows, pgup/pgdn and
//     g/G/home/end — the whole navigation vocabulary. The literal above names
//     four of those ten, and several sibling sheets name "j/k scroll" alone
//     while the other eight work. That is the half of the standing bar rule a
//     bar cannot report on itself ("must never omit a key that will act"): the
//     operator is not told what the screen can do, so they do not do it, and the
//     only way to discover it is to press keys at random.
//
//  2. THERE IS NOTHING TO PRESS KEYS AGAINST. The two behavioural sweeps this
//     project holds the rule with read a MACHINE-READABLE bar — a
//     []actionBarItem off a columnar pane, or ListScreen.footerHint's string
//     through a transcription table. A claim assembled inside View and handed
//     straight to a renderer is invisible to both, which is why
//     listNavUnsweptReceivers (list_nav_surfaces_test.go) had to record the
//     whole class as EXCLUDED rather than sweep it.
//
// AND NAMING THE MISSING KEYS IN THE LITERAL WOULD HAVE MADE IT WORSE. 80
// columns leaves these panes 51 cells (AGENTS.md); the storage-slot sheet's
// literal is already 82 before a key is added, so clampToBox was taking its tail
// — "a bar the operator cannot read is not honest, it is absent". Lengthening an
// unfolded, unbudgeted line names the extra keys nowhere.
//
// SO THE CONVERSION IS THE FIX, and it is the one ListScreen already had: the
// bar becomes a record, the record is FOLDED against the pane the terminal
// really gave, and the row budget is DERIVED from the folded result rather than
// assumed — ListScreen.footerRows' derivation, on the sheets that never got it.
// Once the bar is a record the honesty rule is mechanical, and
// prose_bar_honesty_test.go presses the whole key space against it.
//
// IT IS NOT ONLY FOR SCROLLED SHEETS. A cursor LIST rides the same record — see
// ReorderQueueScreen, whose bar changes shape with the row's status, with
// whether a write is out and with whether the list pages — and proseNavList is
// the movement half for that shape, as proseNavScroll is for a scroller. What
// the two have in common is the only thing that matters here: the words drawn
// and the keystrokes answerable for them are ONE value.
//
// THE CONVERSION IS PARTIAL AND THE REMAINDER IS NAMED, not left over:
// proseBarUnconverted (prose_bar_honesty_test.go) carries one entry per screen
// still writing a literal, saying what the shape of its work is, and a check
// fails in both directions so neither a new straggler nor a stale entry can go
// quiet.

// proseBarItem is ONE segment of such a bar: the words the operator reads, and
// EXACTLY the keystrokes those words spell.
//
// THE KEYS ARE WHAT THE HINT SPELLS AND NEVER A SUPERSET. That is the
// transcription rule listBarKeyNames and poBarKeyNames already keep from the
// other side: a segment credited with a synonym is the code making a claim on
// the bar's behalf, which is the defect the record exists to report. `j/k ↑↓
// scroll` may claim the arrows because it SAYS them; `j/k scroll` may not.
//
// A segment with no keys is not expressible, and that is deliberate: the whole
// point of the record is that every word on the bar is answerable.
type proseBarItem struct {
	// Keys are the keystrokes, spelled as tea.KeyMsg.String() spells them.
	Keys []string
	// Hint is the words drawn, including the key names themselves.
	Hint string
}

// proseBar is a prose action bar as a record. The order is the order drawn.
type proseBar []proseBarItem

// hint is the bar as the single ` · `-joined sentence the folder takes.
func (b proseBar) hint() string {
	parts := make([]string, 0, len(b))
	for _, it := range b {
		if it.Hint == "" {
			continue
		}
		parts = append(parts, it.Hint)
	}
	return strings.Join(parts, " · ")
}

// names reports whether the bar claims this keystroke.
func (b proseBar) names(key string) bool {
	for _, it := range b {
		for _, k := range it.Keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

func proseLoadKeyHidden(loading bool, loadErr string, bar proseBar, key string) bool {
	return (loading || loadErr != "") && !bar.names(key)
}

// rows is how many rows the folded footer occupies, plus its blank separator.
//
// DERIVED FROM THE BAR THAT WILL ACTUALLY BE DRAWN, never a constant. These
// sheets all budgeted their scroller against detailFooterRows — a flat 2 — so a
// footer that folded onto a second line simply ran a row past the pane and
// clampToBox, which drops from the BOTTOM, took the last fold: the same claim
// off the other edge. It is ListScreen.footerRows' derivation and
// AssetDetailScreen already makes it by hand; this is the one copy of it.
//
// Measured at the pane the terminal REALLY gave and not at pickerPaneWidth: a
// fold is safe at the fixed 51 only while the pane HAS 51 cells.
func (b proseBar) rows(cells int) int {
	return 1 + len(pickerWrap(b.hint(), cells))
}

// render is the folded, muted footer, ready to be written under a blank line.
func (b proseBar) render(cells int) string {
	return pickerHintAt(b.hint(), cells)
}

// proseBarCells is the width one of these sheets folds and budgets against: the
// pane the terminal really gives, and pickerPaneWidth (the 51 an 80-column
// terminal leaves) while the screen is still unsized.
//
// It reads screenBodyCells — the UNFLOORED width — rather than
// screenBodyWidth, whose floor of 20 is more than Root drew at the narrowest
// widths this layer has had to survive: a bound that spends cells the pane does
// not have is not a bound. AssetDetailScreen.paneCells is the same expression,
// written before there was a shared one.
func proseBarCells(terminalWidth int) int {
	if w := screenBodyCells(terminalWidth); w > 0 {
		return w
	}
	return pickerPaneWidth
}

// proseNavScroll is the movement half of a SCROLLED sheet's bar, or nothing
// where no movement key can do anything.
//
// THE KEYS COME FROM THE APP'S ONE NAVIGATION VOCABULARY (list_nav.go) rather
// than from a literal here, so a sheet cannot invent a fourth spelling of the
// same affordance. The VERB is the parameter because what moves differs and the
// keys do not: a list moves a CURSOR through rows, a TextScroller moves a WINDOW
// over a body, and an operator reading "move" on a read-only sheet would look
// for a cursor that is not there.
//
// `scrolls` IS THE OVERFLOW QUESTION AND IT IS ASKED, NOT ASSUMED. A body that
// fits the viewport clamps every one of these keys back to offset 0
// (TextScroller.clamp forces it whenever len(lines) <= contentBudget), so the
// bar named all three segments over a pane that redrew byte for byte — standing
// rule 1, and exactly the state listNavHint carries the same argument about for
// an empty list. TextScroller.HasOverflow is the predicate, and it is exact:
// the clamp and the overflow test agree at every offset.
func proseNavScroll(scrolls bool) proseBar {
	if !scrolls {
		return nil
	}
	out := make(proseBar, 0, 3)
	for _, m := range listNavSetVerb("scroll") {
		out = append(out, proseBarItem{Keys: m.Keys, Hint: m.Hint})
	}
	return out
}

// proseNavList is the movement half of a CURSOR LIST's bar: the `move` verb, and
// the paging pair only where the list really pages.
//
// TWO THRESHOLDS, NOT ONE, and keeping them apart is the whole of it. `moves` is
// "is there a SECOND row" (listNavMoves, which carries why an empty list and a
// one-row list are the same answer); `pages` is "does the list outrun the body".
// A list of three rows on a tall pane has somewhere for `j` to go and nowhere
// for `pgdn`, so a single condition names one of the two where it does nothing.
//
// THE PAGER IS PICKED OUT BY ITS KEYSTROKES AND NOT BY ITS WORDS. Matching
// m.Hint against "pgup/pgdn page" would couple this to a wording, and the
// wording is precisely the half of a listNavMove that is allowed to change —
// listNavSetVerb exists because one of them already varies by caller.
func proseNavList(moves, pages bool) proseBar {
	if !moves {
		return nil
	}
	out := make(proseBar, 0, 3)
	for _, m := range listNavSetVerb("move") {
		if !pages && proseNavIsPager(m) {
			continue
		}
		out = append(out, proseBarItem{Keys: m.Keys, Hint: m.Hint})
	}
	return out
}

// proseNavIsPager reports whether a vocabulary entry is the paging pair, by the
// keystrokes it spells.
func proseNavIsPager(m listNavMove) bool {
	for _, k := range m.Keys {
		if k == "pgup" || k == "pgdown" {
			return true
		}
	}
	return false
}

// proseSizeScroller sets a scrolled sheet's viewport against the bar's CEILING
// — the tallest shape that bar can take — and is the ONE place that fixed point
// is computed.
//
// WHY A CEILING AND NOT THE LIVE BAR. Naming the scroll keys costs cells, cells
// fold the bar onto another row, another row leaves the body one row fewer, and
// one row fewer can turn a body that fitted into a body that overflows — which
// puts the scroll keys back on the bar. Sized against the live bar the answer
// oscillates between frames; sized against the tallest it is a fixed point. That
// is the same trade listSearchBarHint makes for the search overlay and
// jdePickBarCeiling for the columnar pickers, and the reason each of them is a
// named ceiling rather than a measurement of whatever is being drawn.
//
// The sheet passes its bar as a FUNCTION of the movement question so the
// ceiling and the drawn bar are one expression; a sheet that wrote the ceiling
// out a second time would be free to write it differently.
func proseSizeScroller(sc *TextScroller, terminalHeight, cells int, bar func(scrolls bool) proseBar) {
	sc.SetViewHeight(scrollerViewHeight(terminalHeight, bar(true).rows(cells)))
}

// proseScrollBar is the bar a scrolled sheet is DRAWING: the viewport sized
// against the bar's ceiling, then the movement question asked of the sized
// scroller.
//
// IT SIZES BEFORE IT ANSWERS, AND THAT IS WHAT MAKES THE RECORD TRUSTWORTHY.
// TextScroller.HasOverflow is a question about the viewport, so asking it of a
// scroller that has not been sized for THIS pane answers about the last one —
// and the sheets set their viewport inside View, so a record read before the
// first render answered about the constructor's defaultDetailHeight. The sweep
// caught it immediately: the project-storage sheet's bar named no movement key
// while j, down, pgdown, G and end all moved the pane. Sizing is idempotent
// given the same pane, so asking the record costs nothing and cannot disagree
// with the next render — the same "the bar and the frame read the SAME
// function" rule bodyScrollsForBar keeps on the columnar layer.
func proseScrollBar(sc *TextScroller, terminalHeight, cells int, bar func(scrolls bool) proseBar) proseBar {
	proseSizeScroller(sc, terminalHeight, cells, bar)
	return bar(sc.HasOverflow())
}

// proseScrollFrame is a scrolled sheet's whole pane: the sized body and the
// folded bar under it.
//
// The sheets call this rather than assembling it themselves so that "how many
// rows does the footer take" and "does this scroll" are answered in one place
// for all of them — the rule jde_form.go already enforces on the columnar layer
// (a sheet may not answer the scroll question itself), applied to the sheets
// that are not on it.
func proseScrollFrame(sc *TextScroller, terminalHeight, cells int, bar func(scrolls bool) proseBar) string {
	drawn := proseScrollBar(sc, terminalHeight, cells, bar)
	return sc.View() + "\n\n" + drawn.render(cells)
}

// proseBarBack is the way off a sheet that binds `backspace` beside `esc`.
//
// BOTH SPELLINGS OR NEITHER. Four screens in this package answer
// `case "esc", "backspace":` in their own key switch — AnalyticsPulseScreen is
// the converted one — and every bar in the program named only the first, so the
// second worked and was named nowhere: the omission half of the rule on a key an
// operator's hand reaches for by reflex. Where a sheet binds only `esc`,
// proseBarEsc is the segment that says so, and choosing between them is not
// taste: naming a key the screen does not bind is the OTHER half of the rule.
var proseBarBack = proseBarItem{Keys: []string{"esc", "backspace"}, Hint: "esc/backspace back"}

// proseBarEsc is the way off a sheet that binds esc alone — which, on most of
// them, means Root's dispatcher owns the back-step and the screen answers
// nothing. It is named all the same because it WORKS there, and the honesty
// sweep presses it through a real Root rather than reading it off Root's switch.
var proseBarEsc = proseBarItem{Keys: []string{"esc"}, Hint: "esc back"}

// proseBarRefresh is the reload every one of these sheets binds to `r`.
var proseBarRefresh = proseBarItem{Keys: []string{"r"}, Hint: "r refresh"}

// proseBarScreen is a screen whose action bar is a RECORD: it can be asked what
// it is claiming right now, rather than having the claim scraped off a rendered
// pane or read out of a literal inside View.
//
// The sweep (prose_bar_honesty_test.go) derives its roster from the SOURCE — the
// types that declare proseBar — rather than from this interface, because an
// interface only reports the types somebody remembered to assert against it,
// which is the hand-kept-roster failure this package keeps being bitten by. The
// interface is here so a converted screen can state the contract at compile
// time, and so the sweep can hold the value it builds without a type switch per
// screen.
//
// proseBar answers nil in the states that draw something else instead — an
// empty list, a destructive confirm — so "this state has no bar" and "this
// state's bar is empty" stay different answers. An empty bar is a defect (a
// frame that names no key at all); a nil one is a state the conversion has not
// reached, whose own key claims are still a literal inside View. A load in
// flight or failed is NOT one of those any more: its frame draws a bar of its
// own (see the load-state note below).
//
// THERE IS NO ROSTER OF THOSE STATES, and that is a decision rather than an
// omission: which states a screen is swept in is a fixture JUDGEMENT, said so
// in proseBarFixtures and bounded by the two coverage checks around it, and a
// hand-listed inventory of states would be exactly the roster this package
// keeps being bitten by. What IS mechanical is the screen: a converted one
// cannot fail to be swept at all.
type proseBarScreen interface {
	Screen
	proseBar() proseBar
}

// proseNavCursor is the movement half of a WINDOWED cursor list's bar — the
// shape a dozen-odd screens in this package share: rows drawn between an
// `↑ more above` marker and a `↓ N more below` one, with the whole navigation
// vocabulary bound in the screen's own key switch and `j/k move` the only part
// of it the footer ever said.
//
// ONE THRESHOLD HERE, WHERE proseNavList HAS TWO, AND THE MECHANISM IS WHY. On
// a list whose pager moves a WINDOW, "is there a second row" and "does the list
// outrun the body" are different questions and a single condition names one of
// them where it does nothing. On these screens the pager moves the CURSOR and
// clamps it — `s.cursor += s.windowSize` followed by a clamp to the last row —
// so `pgdn` on a three-row list standing at the top lands on row three and
// really does move the pane. There is nothing for a second threshold to
// separate: every one of these keystrokes moves exactly when there is a second
// row to move to, which is listNavMoves and nothing else.
//
// Stating that as a call into proseNavList rather than a second vocabulary is
// deliberate: the keystrokes are the app's one roster (list_nav.go) and what
// differs here is only which question gates them.
func proseNavCursor(moves bool) proseBar { return proseNavList(moves, moves) }

// proseListWindow is how many body LINES such a list may draw, with the footer's
// height taken off the top rather than assumed. It was read as a count of rows
// for as long as every row was one line; proseCursorWindow is where it is spent
// as lines, and why a row is not always one.
//
// WHAT IT REPLACES, and why the constant it replaces could not survive the
// conversion. Every one of these screens carried
//
//	const chrome = 4
//	avail := screenBodyHeight(s.terminalHeight) - chrome
//
// where the 4 is a count line, one scroll marker, the blank separator and ONE
// footer row. A folded bar is not one row: naming the eight keystrokes these
// screens bind and never said takes the hint past the 51 cells an 80-column
// pane gives, so it folds onto two or three, and a body budgeted at the old
// constant then assembles two rows more than the pane has — clampToBox drops
// from the BOTTOM, so what it takes is the fold, which is the keys the
// conversion was for. The same claim off the other edge, which is the trade
// ListScreen.footerRows already makes and these screens never did.
//
// MEASURED AGAINST THE CEILING BAR, never the one being drawn, for the reason
// proseSizeScroller gives: the movement segments come OFF a one-row list, so a
// budget taken from the live bar is a budget that changes when the list shrinks
// — and the row count is an input to what the list shows. The tallest shape the
// bar can take is a fixed point; anything else oscillates.
//
// THE FLOOR OF THREE IS PRE-EXISTING AND IS LEFT ALONE. It is the same lie
// screenBodyHeight tells and scrollerViewHeight repeats: below about ten
// terminal rows the assembled frame is taller than the pane whatever the footer
// does. Removing it means giving these screens the refusal ListScreen has
// (listTooShort), which is a decision about what a too-short list does and not
// part of making its bar honest — so it is named here rather than changed in
// passing, and proseBarFrameFits is what scopes the sweep around it.
func proseListWindow(terminalHeight, cells int, ceiling proseBar) int {
	avail := screenBodyHeight(terminalHeight) - proseListFixedRows - ceiling.rows(cells)
	if avail < proseListWindowFloor {
		avail = proseListWindowFloor
	}
	return avail
}

// proseListFixedRows is what one of these lists spends on chrome the BAR does
// not own: the count line it opens with, and BOTH scroll markers.
//
// BOTH, where the constant this replaced reserved one — and that one row is a
// defect the conversion inherited rather than introduced. `↑ more above` and
// `↓ N more below` are drawn TOGETHER for every cursor position in the middle of
// a list, which is most of them, so a budget with one row of marker slack
// assembles a frame one row taller than the pane whenever the operator has
// scrolled at all, and clampToBox drops from the BOTTOM: the last fold of the
// footer, silently. It cost a single-row literal its whole line; it would now
// cost the folded bar the segment its last keys are spelled in, which is worse
// in the same direction. Reserving the second marker costs every one of these
// lists one row of body, and a row of body is what the rule says a readable bar
// is worth.
//
// The bar's own rows — the blank separator included — are NOT in here: they are
// counted from the folded ceiling, which is the whole point of the conversion.
const proseListFixedRows = 3

// proseListWindowFloor is the fewest rows one of these lists will draw, carried
// over verbatim from the `if avail < 3` every one of them wrote out. See
// proseListWindow for why it is a lie and why closing it is separate work.
const proseListWindowFloor = 3

// proseNavStep is the movement half of a FLAT cursor list's bar: the step pair
// and nothing else, where there is a second row to step to.
//
// THE STEP PAIR ALONE BECAUSE THAT IS ALL THESE SCREENS BIND. The flat lists
// (prose_bar_flat_lists_test.go) answer j/k and the arrows in their own switch
// and nothing else of the vocabulary — no pager, no g/G/home/end — and their
// literals said `j/k move`, so the arrows moved the cursor unnamed. Naming the
// pager or the jumps here would be the OTHER half of the rule, a bar claiming
// keys the screen does not bind; binding them is a change to what the screen
// DOES and is not part of making its bar honest.
//
// Picked out of the one vocabulary by KEYSTROKE, for the reason proseNavIsPager
// gives: the wording is the half of a listNavMove allowed to change.
func proseNavStep(moves bool) proseBar {
	if !moves {
		return nil
	}
	for _, m := range listNavSetVerb("move") {
		for _, k := range m.Keys {
			if k == "j" {
				return proseBar{{Keys: m.Keys, Hint: m.Hint}}
			}
		}
	}
	return nil
}

// proseNavArrows is the movement half of a cursor surface whose TEXT BOX owns
// the letters: the arrow pair alone, where there is a second row to step to.
//
// A LIVE QUERY BOX ROUTES LETTERS TO ITSELF (AGENTS.md), so on the pickers that
// search as the operator types — the e-paper bind picker, the check-in location
// lookup — `j` and `k` are characters in the query and only the arrows move the
// cursor. proseNavStep would name `j/k` there, which is the bar claiming two
// keys the box eats; this is the same vocabulary entry with the letters taken
// OFF by keystroke, for the reason proseNavIsPager gives about matching words.
func proseNavArrows(moves bool) proseBar {
	step := proseNavStep(moves)
	if len(step) == 0 {
		return nil
	}
	var keys []string
	for _, k := range step[0].Keys {
		if k == "up" || k == "down" {
			keys = append(keys, k)
		}
	}
	return proseBar{{Keys: keys, Hint: "↑↓ move"}}
}

// proseNavTop is the jump-to-the-TOP half of the `g/G home/end top/bottom`
// segment, for a list that binds `g` and `home` and binds neither `G` nor `end`.
//
// NAMING THE WHOLE SEGMENT THERE WOULD BE THE OTHER HALF OF THE RULE. The
// vocabulary entry spells four keystrokes, and a bar may claim only the ones the
// screen answers; binding the missing pair is a change to what the screen DOES
// (out of scope for a bar conversion), so the segment is narrowed instead. Picked
// out of the one vocabulary by KEYSTROKE for the reason proseNavIsPager gives, and
// gated on a second row for the reason listNavMoves gives: on a one-row list the
// cursor is already at the top.
func proseNavTop(moves bool) proseBar {
	if !moves {
		return nil
	}
	for _, m := range listNavSetVerb("move") {
		var keys []string
		for _, k := range m.Keys {
			if k == "g" || k == "home" {
				keys = append(keys, k)
			}
		}
		if len(keys) == 2 {
			return proseBar{{Keys: keys, Hint: "g/home top"}}
		}
	}
	return nil
}

// proseMenuHotkeys is the segment naming a MENU's row letters: every letter a
// row answers, spelled in row order, and the verb they share.
//
// THE ROWS ALREADY DRAW `[p]` BESIDE EACH LABEL, AND THAT IS NOT A BAR. The
// letters on a menu row are a claim about a key exactly as a footer segment is,
// and the honesty sweep can only press against the record: a letter that moves
// the cursor onto its row and opens it, with no word on the bar for it, is the
// omission this record exists to close. So the bar spells them, DERIVED from the
// items rather than written a second time, which is what keeps a row added to a
// menu from being a key the bar has never heard of.
func proseMenuHotkeys(hotkeys []rune, verb string) proseBarItem {
	keys := make([]string, 0, len(hotkeys))
	for _, r := range hotkeys {
		keys = append(keys, string(r))
	}
	return proseBarItem{Keys: keys, Hint: strings.Join(keys, "/") + " " + verb}
}

// proseMenuSubtitle is a menu row's second line: indented under the label and
// clipped to the pane with the cut marked, LINE BY LINE.
//
// Clipped rather than left to clampToBox, which cuts from the right with no
// mark, so a subtitle cut at 51 cells reads as the whole description. Line by
// line because the window packs a row by the lines it really draws
// (proseFlatListFrame): clipping the joined text would let a subtitle with a
// newline in it pass as one line and put the window's arithmetic wrong.
func proseMenuSubtitle(subtitle string, cells int) string {
	const indent = "      "
	lines := strings.Split(subtitle, "\n")
	for i, line := range lines {
		lines[i] = indent + StyleMuted.Render(pickerClip(line, cells-len(indent)))
	}
	return strings.Join(lines, "\n")
}

// proseClipEachLine clips every line of an OMS-supplied value to `cells`, with
// each cut marked, and keeps the newlines.
//
// LINE BY LINE for the reason proseMenuSubtitle gives: the window packs a row by
// the lines it really draws, so a value clipped as ONE string would pass a stored
// newline through uncounted, and a cut taken across the joined text would leave
// every line after it drawn whole past the pane. The newline is not stripped,
// for the reason proseCursorWindow gives about drawing a value the record does
// not hold.
func proseClipEachLine(text string, cells int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = pickerClip(line, cells)
	}
	return strings.Join(lines, "\n")
}

// proseFlatCeilingRows is the row count a flat list's bar is at its TALLEST for:
// two rows is a second row to step to, and every row action is on offer from
// one. A flat list asks its bar builder for this many to get the ceiling
// proseFlatListFrame budgets against, so the ceiling and the drawn bar are one
// expression rather than a second copy of the segments.
const proseFlatCeilingRows = 2

// proseFlatListFrame is a FLAT cursor list's whole pane: the lines it opens with,
// a window of rows around the cursor between the two scroll markers, and the
// folded bar under it.
//
// WHAT "FLAT" MEANT, and why the bar could not be fixed without the body. These
// screens wrote EVERY row and then the footer, with no window and no budget, so
// a list longer than the pane pushed the footer off the bottom whatever it said
// — clampToBox drops from the bottom — and folding the bar onto more rows would
// only have pushed it further. A folded bar on a list with no budget is a bar
// that folds off the pane.
//
// THE WINDOW IS PACKED BY LINES, NOT ROWS, because their rows are not one line
// each: a donation carries a meta line, a vendor a contact line and a compliance
// line, each present or absent per row. A budget counting rows is the defect
// AssetPartsScreen is recorded for. So each row arrives RENDERED, its height is
// what it will really draw, and proseLineWindow packs them — ListScreen's
// rowsFittingFrom arithmetic, on the screens that never had it.
//
// `head` is whole lines, each ending in a newline (or empty), and is counted off
// the budget as drawn. The budget is measured against the bar's CEILING for the
// reason proseListWindow gives, and reserves BOTH markers for the reason
// proseListFixedRows gives.
//
// AN OVERSIZED ROW IS CLIPPED HERE, where the body budget is owned, rather than
// admitted whole by proseLineWindow's necessary one-row floor. API prose can
// contain newlines, so one authorization note or lockout reason can be taller
// than the pane by itself. Drawing it whole lets Root's bottom clamp erase the
// footer, while silently taking its tail makes an incomplete value look whole.
// The last available body line therefore names how many lines were omitted.
// That indicator is part of the budget, and the cursor's row remains the row
// drawn: a pathological value costs detail, never the actions that operate on
// it.
//
// `start` IS WRITTEN FROM INSIDE View, which is the precedent proseScrollBar
// set: the window is a function of the pane, the rows and the cursor, and the
// fit is idempotent given the same three — a second render walks nowhere — so
// asking it where the frame is drawn cannot disagree with the frame, where
// re-deriving it on every Update arm would be one more place to forget.
//
// AN UNSIZED SCREEN DRAWS EVERY ROW, the layer's standing answer for no pane:
// there is no window to overflow, so none is invented.
func proseFlatListFrame(head string, rows []string, cursor int, start *int, terminalHeight, cells int, ceiling, drawn proseBar) string {
	return proseFlatListFrameFoot(head, rows, cursor, start, terminalHeight, ceiling.rows(cells), drawn.render(cells))
}

// proseFlatListFrameFoot is proseFlatListFrame for a list whose FOOT is more than
// its bar: a notes box and the bar that answers it, a service notice above the
// bar, or a y/n confirm drawn under the rows in the bar's place.
//
// THOSE SCREENS DREW THE EXTRA LINES UNDER EVERY ROW WITH NO BUDGET AT ALL, so a
// list longer than the pane took the prompt off the bottom along with the keys
// that answer it — the operator pressed `n` and saw nothing open, or pressed `x`
// and was asked nothing they could read. The foot is the part that must survive,
// so it is spent FIRST and the window gets what is left: `footRows` is every row
// the foot takes, the blank line above it included, counted from what will be
// drawn (a folded bar's rows, a folded notice's) and never assumed.
//
// `foot` is written after that blank line and carries no trailing newline, which
// is the shape proseBar.render hands over — so a foot that is only a bar is
// proseFlatListFrame exactly.
func proseFlatListFrameFoot(head string, rows []string, cursor int, start *int, terminalHeight, footRows int, foot string) string {
	var b strings.Builder
	b.WriteString(head)
	from, to := 0, len(rows)
	budget := 0
	if terminalHeight > 0 {
		budget = proseFlatListBudgetRows(head, terminalHeight, footRows)
		from, to = proseLineWindow(proseRowHeights(rows), cursor, *start, budget)
		*start = from
	}
	proseWriteRows(&b, rows, from, to, budget)
	b.WriteString("\n")
	b.WriteString(foot)
	return b.String()
}

// proseFlatListBudget is how many body LINES proseFlatListFrame gives its window
// under `head`: the pane less the head as drawn, both scroll markers and the
// folded ceiling bar, floored at proseListWindowFloor.
//
// It is its own function so a list whose PAGER has to know how many rows the
// window holds — `cursor += windowSize` — can ask the same budget the frame
// draws against rather than writing the subtraction out a second time. A page
// measured against a different budget from the window it pages is a page that
// skips rows or repeats them, and nothing on the pane would say which.
func proseFlatListBudget(head string, terminalHeight, cells int, ceiling proseBar) int {
	return proseFlatListBudgetRows(head, terminalHeight, ceiling.rows(cells))
}

// proseFlatListBudgetRows is proseFlatListBudget with the foot's rows already
// counted — the one subtraction both frames spend.
func proseFlatListBudgetRows(head string, terminalHeight, footRows int) int {
	budget := screenBodyHeight(terminalHeight) - strings.Count(head, "\n") - 2 - footRows
	if budget < proseListWindowFloor {
		budget = proseListWindowFloor
	}
	return budget
}

// proseRowHeights is how many lines each rendered row really draws — the
// heights proseLineWindow packs.
func proseRowHeights(rows []string) []int {
	heights := make([]int, len(rows))
	for i, r := range rows {
		heights[i] = strings.Count(r, "\n") + 1
	}
	return heights
}

// proseCursorWindow writes the window of a WINDOWED cursor list — the recipe
// proseNavCursor and proseListWindow serve — packed by LINES into the `budget`
// proseListWindow gave it, between the two scroll markers.
//
// THOSE LISTS COUNTED THEIR WINDOW IN ROWS and drew one line a row, which was
// true of every row they had ever been shown and is not true of the data. The
// names they lead each row with are OpenMakerSuite CharFields, and nothing
// between an API write and this pane refuses a newline in one: DRF's CharField
// trims only the ends of a value, the admin's single-line input is not the only
// writer, and lipgloss keeps the newline when it styles the row. A two-line name
// made one row two lines, so a window of N rows assembled more lines than the
// pane has and clampToBox, which drops from the BOTTOM, took the footer — every
// key named nowhere. proseListWindow's budget already IS a count of body lines
// (it reserves the count line, both markers and the folded bar and gives the
// rest), so the fix is to spend it as one: proseLineWindow packs the rows by
// what each really draws, and a row taller than the budget is clipped with the
// cut named — proseFlatListFrame's answer, which is why they share
// proseWriteRows.
//
// THE NEWLINE IS NOT STRIPPED, and that is a decision rather than an omission.
// Flattening a name on display would draw a value the record does not hold and
// say nothing about it; drawing it as stored, and marking where the pane ran
// out, is what the operator can reconcile with the web app.
//
// `start` is the screen's window start. Its key arms still move it in ROWS
// (scrollIntoView), and this refits it to the cursor against the rows as drawn,
// written from inside View for the reason proseFlatListFrame gives: the fit is
// idempotent given the pane, the rows and the cursor, so a second render walks
// nowhere. With every row one line the fit IS the row window those arms already
// chose, so a list of ordinary names draws exactly what it drew before.
//
// WHAT THIS DOES NOT CHANGE is the pager: pgup/pgdn still step the cursor by the
// budget in ROWS. On a list of multi-line names that is a step longer than the
// screenful drawn, so a page can pass rows j/k still reach; deciding what a page
// of rows of different heights is worth is the question AssetPartsScreen's
// proseBarUnconverted entry already records, and it is not part of keeping the
// footer on the pane.
func proseCursorWindow(b *strings.Builder, rows []string, cursor int, start *int, budget int) {
	if extraHeadLines := strings.Count(b.String(), "\n") - 1; extraHeadLines > 0 {
		budget -= extraHeadLines
		if budget < 1 {
			budget = 1
		}
	}
	from, to := proseLineWindow(proseRowHeights(rows), cursor, *start, budget)
	*start = from
	proseWriteRows(b, rows, from, to, budget)
}

// proseWriteRows writes rows[from:to] between the scroll markers, clipping any
// row taller than `budget` lines to the budget with its last kept line naming how
// many lines were left out. A budget of zero or less draws every row whole — the
// unsized pane, where there is nothing to overflow.
//
// The mark spends a line of the budget rather than being added to it, so a
// clipped row is exactly as tall as the budget it was clipped to and the frame's
// arithmetic holds.
func proseWriteRows(b *strings.Builder, rows []string, from, to, budget int) {
	if from > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	for _, r := range rows[from:to] {
		if lines := strings.Split(r, "\n"); budget > 0 && len(lines) > budget {
			kept := budget - 1
			omitted := len(lines) - kept
			lines = append(lines[:kept], StyleMuted.Render(fmt.Sprintf("… %d more lines", omitted)))
			r = strings.Join(lines, "\n")
		}
		b.WriteString(r + "\n")
	}
	if to < len(rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(rows)-to)) + "\n")
	}
}

// proseLineWindow picks which rows of a list whose rows draw DIFFERENT numbers of
// lines are on the pane: [from, to), containing the cursor, starting no earlier
// than it has to and running off the end of the list no further than it must.
//
// It is ListScreen.scrollIntoView's walk over a slice of heights: forward until
// the cursor is inside the window that start can afford, then back while the
// rows below still reach the end of the list, so a taller pane is not spent on
// blank space. A row taller than the whole budget is still SELECTED alone — a
// window of none renders a list with no rows in it, and the walk would never
// terminate. proseFlatListFrame clips that selected row to the budget while
// visibly marking the omitted lines.
func proseLineWindow(heights []int, cursor, start, budget int) (from, to int) {
	n := len(heights)
	if n == 0 {
		return 0, 0
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= n {
		cursor = n - 1
	}
	if start > cursor {
		start = cursor
	}
	if start < 0 {
		start = 0
	}
	fits := func(from int) int {
		used, count := 0, 0
		for i := from; i < n; i++ {
			if used+heights[i] > budget {
				break
			}
			used += heights[i]
			count++
		}
		if count < 1 {
			count = 1
		}
		return count
	}
	for start < cursor && cursor >= start+fits(start) {
		start++
	}
	for start > 0 {
		prev := start - 1
		f := fits(prev)
		if prev+f < n || prev+f <= cursor {
			break
		}
		start = prev
	}
	return start, start + fits(start)
}

// proseBarFieldFocus is the focus pair of a FIELD FORM whose focus WRAPS: tab and
// the down arrow step to the next field, shift+tab and the up arrow to the one
// before, and the last field steps back onto the first.
//
// ONE SEGMENT, ALL FOUR KEYSTROKES, because on a wrapping form all four act from
// every field. The literals this replaced said `tab move` (or `tab/↑↓ move`), so
// shift+tab — and on most of them the arrows — moved the caret row unannounced.
// The verb is "field" and not "move": there is no cursor through rows here, and
// an operator reading "move" on a form looks for a list that is not there.
//
// A form whose focus CLAMPS cannot use this segment — tab on its last field does
// nothing, and naming it there is the other half of the rule — so such a form
// names the direction it can go from the field it is on (BatchScanSerialsScreen's
// setup step is the one).
var proseBarFieldFocus = proseBarItem{
	Keys: []string{"tab", "shift+tab", "up", "down"},
	Hint: "tab/shift+tab ↑↓ field",
}

// proseFormLine bounds one line of a FIELD FORM that carries a value the screen
// does not control — an OMS error body, a record name, an endpoint URL — to ONE
// row of the pane, with the cut marked.
//
// THE FORMS HAVE NO WINDOW, so their height is the sum of their lines, and every
// one of those lines is a fixed row except the ones carrying such a value. An
// error body is where it bites: omsapi.parseError puts the ENTIRE raw response
// into the message whenever the envelope carries no code, so a gateway's 502 page
// arrived as seven lines and pushed the bar under it off the bottom of the pane —
// clampToBox drops from the BOTTOM — naming every key nowhere at the moment the
// operator most needed a way out. Flattened first (jdeStatusOneLine, the status
// row's own convention, for the reason it gives about bounding before
// re-expanding) and clipped after, so the frame is as tall as the form whatever
// the server said and a value cut short says it was.
func proseFormLine(text string, cells int) string {
	return pickerClip(jdeStatusOneLine(text), cells)
}

// A LOAD IN FLIGHT AND A LOAD THAT FAILED ARE STATES WITH A BAR OF THEIR OWN.
//
// THE DEFECT. Every screen this record was built for answered nil in both, and
// drew a literal instead: `Loading donations…` alone, or the error with a muted
// "r retry · esc back" under it. The literal was written for what the frame is
// ABOUT and not for what the key switch DOES — and the switch does not stop
// answering because the rows have not arrived. On the forecasts `w` still flips
// the filter and reloads from a failed load; on every list with a create key `c`
// or `n` still opens the form; and a REFRESH that fails leaves the previous rows
// in place, so `E`, `enter` and the row's own actions still switch screens or
// write against a row the frame no longer draws. The operator was told two keys
// on a frame that worked six — the omission this record exists to close, in the
// one state the conversion had left out.
//
// THE REMEDY IS A BAR, NOT A GATE, and the difference is a decision rather than
// a preference. Declining those keys while a load is out would change what the
// screens DO, which is a key-binding change with its own trade-offs — some of it
// is plainly a recovery (`w` retrying under the other filter), some of it plainly
// is not (a write against a row nobody can see) — and it is not this record's to
// make. So each screen names exactly what its switch answers there, and the keys
// that act on rows the frame does not draw are named TRUTHFULLY, as candidates
// for gating, rather than hidden — each screen's loadBar doc says which.
//
// WHAT IS NAMED IS WHAT IS SEEN TO ACT. A key whose only effect is a field the
// frame does not draw — a cursor moving through stale rows, a delete confirm
// armed under the error — changes nothing an operator can see and issues
// nothing, so a bar naming it would name a key that visibly does nothing:
// standing rule 1 from the other side. Those are gating candidates too, and the
// sweep's doc says which instrument separates the two.

// proseBarRetry is `r` on a frame whose load FAILED, where the key's job is to
// try again. It is proseBarRefresh's keystroke with the word the failure frames
// have always used; the key is the same one either way.
var proseBarRetry = proseBarItem{Keys: []string{"r"}, Hint: "r retry"}

// proseBarReloadFor is `r` in a load state: "r refresh" while the load is out
// (a second press starts it again) and "r retry" once it has failed.
func proseBarReloadFor(failed bool) proseBarItem {
	if failed {
		return proseBarRetry
	}
	return proseBarRefresh
}

// proseLoadingFrame is a converted screen's pane while its load is in flight:
// the working line, and the bar the screen answers keys by in that state.
//
// The working line is a fixed sentence the screen owns, so it is clipped rather
// than folded — a folded working line would be a height the bar has to be
// budgeted around for no gain.
func proseLoadingFrame(working string, cells int, bar proseBar) string {
	return StyleMuted.Render(pickerClip(working, cells)) + "\n\n" + bar.render(cells)
}

// proseFailedFrame is a converted screen's pane after its load has failed: the
// failure, bounded to what the pane leaves once the bar has its rows, and the
// bar under it.
//
// BOUNDED BECAUSE IT IS AN OMS BODY, and the failure frame is where an unbounded
// one costs most. omsapi.parseError puts the ENTIRE raw payload into the message
// whenever the envelope carries no code, so a gateway's 502 page arrived as
// seven lines, and a frame that wrote it out whole pushed its own bar off the
// bottom of the pane — clampToBox drops from the BOTTOM — on exactly the frame
// whose bar says how to try again. It is flattened first (jdeStatusOneLine, for
// the reason proseFormLine gives) and then folded and cut with the cut MARKED by
// failDetailLines, the one copy of that shape (AGENTS.md).
//
// THE BAR'S ROWS COME OFF FIRST and the failure gets the rest, floored at one
// row: a failure frame that says nothing about the failure is worse than a frame
// that runs over on a pane too short for both, which is the band
// proseBarFrameFits already scopes the sweeps around. An unsized screen has no
// pane to measure, so it gets proseFailedUnsizedRows.
func proseFailedFrame(loadErr string, terminalHeight, cells int, bar proseBar) string {
	return proseRefusalFrame("Error: ", StyleStatusError, loadErr, "", terminalHeight, cells, bar)
}

// proseRefusalFrame is proseFailedFrame with the lead words, their style and a
// standing NOTE supplied: for a refusal whose frame says more than "Error:" —
// the analytics pulse's staff-only notice leads with a warning, and the badge
// enrollment failure says the screen is staff-only.
//
// THE GIVE-ORDER IS WRITTEN DOWN: the bar never gives, the lead row of the
// failure never gives, then the NOTE goes whole, and only then does the OMS body
// give, with its cut marked. The note is a fixed explanation the screen owns and
// is folded whole or not drawn — measured at 80x12 before this order existed,
// the staff-only notice's three folded rows took the bar off the pane, which is
// the one surface that says how to leave it.
func proseRefusalFrame(lead string, leadStyle lipgloss.Style, detail, note string, terminalHeight, cells int, bar proseBar) string {
	var noteLines []string
	if note != "" {
		noteLines = pickerWrap(note, cells)
	}
	rows := proseFailedUnsizedRows
	if terminalHeight > 0 {
		rows = screenBodyRows(terminalHeight) - bar.rows(cells)
		if noteRows := 1 + len(noteLines); len(noteLines) > 0 && rows-noteRows >= 1 {
			rows -= noteRows
		} else {
			noteLines = nil
		}
	}
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	for i, line := range failDetailLines(lead+jdeStatusOneLine(detail), cells, rows) {
		if i == 0 && strings.HasPrefix(line, lead) {
			line = leadStyle.Render(lead) + strings.TrimPrefix(line, lead)
		}
		b.WriteString(line + "\n")
	}
	if len(noteLines) > 0 {
		b.WriteString("\n" + StyleMuted.Render(strings.Join(noteLines, "\n")) + "\n")
	}
	return b.String() + "\n" + bar.render(cells)
}

// proseFailedUnsizedRows is how many rows a failure gets on a screen that has
// not been told its pane — enough for an ordinary sentence and the head of a
// gateway page, and a bound on a 20 KB one.
const proseFailedUnsizedRows = 6
