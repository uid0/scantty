package tui

import "strings"

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
// proseBar answers nil in the states that draw something else instead — a load
// in flight, a failure, an empty list, a destructive confirm — so "this state
// has no bar" and "this state's bar is empty" stay different answers. An empty
// bar is a defect (a frame that names no key at all); a nil one is a state the
// conversion has not reached, whose own key claims are still a literal inside
// View.
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

// proseListWindow is how many ROWS such a list may draw, with the footer's
// height taken off the top rather than assumed.
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
