package tui

import "strings"

// list_nav.go — the app's ONE list-navigation vocabulary.
//
// A LIST SURFACE is any screen state that presents rows the operator moves a
// cursor through. There are three kinds in this program and they draw their
// bars in three different ways — the columnar sheets build a []actionBarItem
// (jde_form.go), ListScreen builds a footer STRING (list.go), and some thirty
// screens write a muted literal straight into their View — so no single
// renderer can be made to answer for all of them. What CAN be made to answer
// for all of them is the vocabulary itself: which keystrokes move a list
// cursor, and which ones do not move anything anywhere.
//
// THE DEFECT THIS EXISTS FOR. sc-po-create-hangs unbound the emacs-style paging
// chords on the four purchasing surfaces its sweeps covered, so their bars would
// stop naming keys they did not spell. Every sibling list screen went on binding
// them: twenty-four `case "ctrl+d", "pgdown":` pairs across twenty-one files, and
// a search of the whole program for a bar that SPELLS ctrl+d or ctrl+u found
// none. So the same keystroke paged the supplier list and did nothing on the
// inventory list the operator reached it from, and which was which could only be
// discovered by pressing it — the second half of standing rule 2 ("must never
// omit a key that will act"), which is the half a bar cannot report on itself.
//
// WHY RETIRED RATHER THAN NAMED. Both directions close the rule, and the cost
// decides: 80 columns leaves a list's footer 51 cells (AGENTS.md), a chord
// spells as "ctrl+u/ctrl+d page" against "pgup/pgdn page", and a claim past the
// cut is not a claim at all. The same trade was already made and recorded for
// ListScreen's own pager and its search overlay, so retiring the rest is
// bringing the stragglers into line with the answer the app already gives —
// and it is the direction that cannot make any bar longer, which matters
// because rule 5 bites whenever a bar item is added.

// listNavMove is one movement affordance as an operator meets it on a list
// surface: the footer segment that names it, and EXACTLY the keystrokes that
// segment spells.
//
// The keys are what the segment SPELLS and never a superset. That is the
// transcription rule the bar tables already keep (listBarKeyNames,
// poBarKeyNames): a segment credited with a synonym is the code making a claim
// on the bar's behalf, which is the defect the tables exist to report.
type listNavMove struct {
	Hint string
	Keys []string
}

// listNavSet is THE navigation set for a list surface with a cursor, in footer
// order.
//
// It is a function rather than a var so a caller cannot append to it, and it is
// read by ListScreen.footerHint — this is production vocabulary, not a test
// fixture. The columnar layer spells the same affordances as bar TOKENS
// (UP/DN, PgUp/PgDn, Home/End) and binds no letter, because on a columnar
// picker the filter box is always live and a bare `j` is a character in the
// query rather than a movement key; that difference is a fact about the surface
// and each bar tells the truth about its own, which is why there is one
// vocabulary and two spellings of it rather than two vocabularies.
func listNavSet() []listNavMove {
	return []listNavMove{
		{"j/k ↑↓ move", []string{"j", "k", "up", "down"}},
		{"pgup/pgdn page", []string{"pgup", "pgdown"}},
		{"g/G home/end top/bottom", []string{"g", "G", "home", "end"}},
	}
}

// listNavHint is the movement half of a list's footer for a list of `rows`, or
// the empty string where no movement key can do anything.
//
// THE EMPTY LIST IS A STATE, NOT AN EXCEPTION. Every one of these affordances
// needs a SECOND row to be true: `j` at the bottom of a one-row list clamps onto
// the row it started on, `G` goes to the row it is already on, and a page
// clamps to the same place — no note, no highlight change, a pane redrawn byte
// for byte under a footer promising all three. A list spends much of its life
// with nothing in it (a fresh install, a filter that matched nothing, a load
// that has not answered), and that is exactly the state the claim was
// unconditional in.
//
// ONE ROW AND NO ROWS ARE THE SAME ANSWER HERE for the same reason jdeRowMoves
// gives on the columnar side: a cursor with one place to stand and a cursor with
// none both have nowhere to go. What they are NOT the same answer to is what the
// body says — "nothing here yet" and a single row are different sights — but
// that is the body's sentence, not the bar's.
func listNavHint(rows int) string {
	if !listNavMoves(rows) {
		return ""
	}
	segments := make([]string, 0, len(listNavSet()))
	for _, m := range listNavSet() {
		segments = append(segments, m.Hint)
	}
	return strings.Join(segments, " · ")
}

// listNavMoves is the movement question for a list surface, asked once: is
// there a second row to move to.
//
// It is jdeRowMoves for the surfaces that are not on the columnar layer, and it
// is deliberately the same expression rather than a call into that file: the two
// halves of the app answer to different bar renderers, and a shared helper whose
// only content is `> 1` would tie them together at the one point they do not
// need to be tied. What must not drift is the RULE, and the rule is written down
// in both places.
func listNavMoves(rows int) bool { return rows > 1 }

// listNavBinds reports whether a keystroke belongs to the navigation set — that
// is, whether pressing it on a list surface is a request to move the cursor and
// nothing else.
//
// It is what lets a surface gate its whole movement vocabulary in ONE place
// instead of repeating the count condition on six switch arms, which is how the
// condition comes to be applied to five of them.
func listNavBinds(key string) bool {
	for _, m := range listNavSet() {
		for _, k := range m.Keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

// listNavRetiredChords are the keystrokes that USED to move a list cursor on
// some screens and now move nothing anywhere, each with the reason.
//
// A retired key is recorded rather than deleted so that "never bound" and
// "deliberately unbound" stay different states, and so the check that keeps them
// unbound can say WHY rather than just failing.
// TestListNav_NoSurfaceBindsARetiredChord PRESSES every key in this map on every
// screen the two swept fixture sets can build — the columnar sheets and every
// *ListScreen — so re-introducing one, on a screen that does not exist yet
// included, fails the build rather than shipping a key nothing names. It is a
// behavioural press and not a scan of this package's source: a chord bound
// through a helper or a key-name map is invisible to a `case "ctrl+d":` regex
// and is not invisible to a keystroke.
var listNavRetiredChords = map[string]string{
	"ctrl+u": "the emacs page-up chord. No bar in the program ever spelled it, and " +
		"pgup is named on every list that pages. sc-po-create-hangs unbound it on " +
		"ListScreen's pager and on the purchasing surfaces; it stayed bound across " +
		"twenty-one sibling files, so the same key paged one list and did nothing " +
		"on the next.",
	"ctrl+d": "the emacs page-down chord, unbound for the reason ctrl+u is. It is " +
		"also the terminal's end-of-file, which is a second reason not to spend a " +
		"bar segment teaching it.",
	"ctrl+p": "the emacs previous-line chord, retired from ListScreen's search " +
		"overlay by sc-po-create-hangs. Nothing binds it now and nothing should: " +
		"up is named wherever a cursor moves.",
	"ctrl+n": "the emacs next-line chord, retired beside ctrl+p.",
}
