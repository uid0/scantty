package tui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// list_nav_test.go — the rule that no BEHAVIOURAL sweep can hold for the whole
// app.
//
// The two behavioural sweeps each own a slice of the program and read a bar the
// way that slice draws it: jde_pane_fit_test.go's
// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves walks every type
// embedding jdeScreen and reads a []actionBarItem, and
// list_bar_honesty_test.go's TestList_FooterNamesExactlyTheKeysThatWork walks
// every *ListScreen the nav tree reaches and parses a footer STRING. Between
// them they cover the surfaces whose bar is a machine-readable RECORD.
//
// Some thirty screens are outside both, and it is not an oversight that can be
// closed by adding them to a roster: their bar is a muted literal written
// straight into a strings.Builder inside View, so there is nothing to read
// structurally and nothing to press it against. list_nav.go's package comment
// records that as the standing exclusion.
//
// What CAN be held over all of them is the VOCABULARY — which keystrokes are
// navigation at all, and which ones move nothing anywhere. This file holds the
// retired half of it by PRESSING the chords on every fixture the two sets can
// build, positively controlled by the named key that spells the same
// affordance. The surfaces outside both sets have no fixture to press, and
// list_nav_surfaces_test.go classifies them rather than letting them pass by
// nobody having looked.

// listNavChordControl is the POSITIVE CONTROL for one retired chord: the named
// key that spells the same affordance, and the probe that puts a surface in a
// state where that key has somewhere to go.
//
// WITHOUT IT THIS SWEEP WOULD BE STRICTLY WORSE THAN THE SOURCE SCAN IT
// REPLACED. "Pressing ctrl+d changed nothing" proves nothing on its own: it is
// equally true of an empty list, a one-row list, a refused pane and a form with
// nothing to page, so a sweep that only pressed the chord would go green over
// every fixture that cannot move for reasons of its own — the vacuous-fixture
// rule (AGENTS.md) in the shape that is hardest to see, because the assertion
// reads exactly right. So each case presses the CONTROL from the same state
// first: only where the control moves is "the chord did not" a claim about the
// chord.
//
// The probes are NAMED keys and never runes. A columnar picker's filter box is
// always live, so probing with `G` would type a character into the query and
// the pane would change for a reason that has nothing to do with movement.
type listNavChordControl struct {
	probe []string
	key   string
}

var listNavChordControls = map[string]listNavChordControl{
	"ctrl+u": {probe: []string{"end"}, key: "pgup"},
	"ctrl+d": {key: "pgdown"},
	"ctrl+p": {probe: []string{"end"}, key: "up"},
	"ctrl+n": {key: "down"},
}

// TestListNav_TheControlsCoverTheRetiredSet: every retired chord has a control.
//
// Without this, retiring a fifth chord and forgetting its control would leave
// the sweep below pressing it with nothing to compare against — and it would
// pass, silently, for exactly the reason the controls exist to prevent.
func TestListNav_TheControlsCoverTheRetiredSet(t *testing.T) {
	if len(listNavRetiredChords) == 0 {
		t.Fatal("listNavRetiredChords is empty, so the sweeps over it assert nothing")
	}
	for chord := range listNavRetiredChords {
		if _, ok := listNavChordControls[chord]; !ok {
			t.Errorf("%q is retired but listNavChordControls names no key that spells the "+
				"same affordance, so the behavioural sweep would press it against no "+
				"control and pass on any fixture that cannot move", chord)
		}
	}
	for chord := range listNavChordControls {
		if _, ok := listNavRetiredChords[chord]; !ok {
			t.Errorf("listNavChordControls carries a control for %q, which is not retired — "+
				"the entry has outlived the chord it was written about", chord)
		}
	}
}

// listNavPress presses a probe then one key, and reports the clipped pane and
// the place fingerprint before and after that key.
//
// THE CLIPPED PANE is what an operator can distinguish (standing rule 1) and the
// PLACE is what a pane cannot always show: a cursor moved onto a row that
// renders identically, or an offset clamped straight back, changes where the
// next key lands while the frame stays byte for byte. Both, for the reason
// jdePlaceOf gives — and which of the two a set may ASSERT on differs, see
// below.
func listNavPress(t *testing.T, s Screen, w, h int, probe []string, key string) (paneBefore, paneAfter string, placeBefore, placeAfter []int64) {
	t.Helper()
	r := jdeRootAt(t, s, w, h)
	press := func(k string) {
		next, _ := s.Update(poPickerKeyMsg(k))
		if next != nil {
			s = next
		}
	}
	for _, p := range probe {
		press(p)
	}
	paneBefore, placeBefore = r.View(), jdePlaceOf(s)
	press(key)
	return paneBefore, r.View(), placeBefore, jdePlaceOf(s)
}

// TestListNav_NoSurfaceBindsARetiredChord: no surface the app can build MOVES
// on a keystroke list_nav.go records as retired.
//
// THIS IS HALF ONE OF THE RULE, and it is the half a bar cannot report on
// itself: "the footer names a key that does nothing" is visible to anyone who
// presses it, while "a key acts and no bar in the program spells it" is
// discoverable only by pressing keys nothing told you about. It shipped for
// exactly that reason — twenty-four `case "ctrl+d", "pgdown":` arms across
// twenty-one files, none of them named by any bar, after the four purchasing
// surfaces had already been brought into line.
//
// IT IS A BEHAVIOURAL PRESS AND NOT A SOURCE SCAN, which is the repair this
// check itself needed. It used to regex the package for `case "ctrl+d":`
// literals, and a claim proved from source SHAPE is the defect this project
// keeps closing: a commented-out or unreachable arm failed it, while a chord
// bound through a helper, a key-name map or a strings.Contains dispatch passed
// it untouched. Pressing the key answers both directions, and answers them about
// the thing the operator actually meets.
//
// THREE FIXTURE SETS. Two of them draw a bar and already have a sweep that
// builds their screens: the columnar screens from jdePaneCases (every type
// embedding jdeScreen, plus its extra states) and every *ListScreen from
// listBarSurfaces, at every row count in listRowCases. The third is
// TextScroller, which draws no bar of its own and is here because it is where
// two of these chords were unbound and because it hands the whole movement
// vocabulary to every detail sheet that holds one — a binding restored there
// reaches fourteen screens and neither of the other two sets can see it. The
// surfaces outside all three have no fixture to press, and that is exactly what
// TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused classifies rather
// than hides.
//
// WHAT IS ASSERTED DIFFERS BETWEEN THE TWO SETS, and the difference is a fact
// about the surfaces rather than a weakening chosen for convenience. All four
// retired chords are ALSO bubbles' own textinput line-editing keys — ctrl+u
// deletes to the start of the line, ctrl+d the character forward, ctrl+p/ctrl+n
// walk the suggestion list — and AGENTS.md's standing rule is that a focused
// text box owns its own keys, because declining a keystroke there would DISCARD
// input rather than preserve a position. So on a columnar sheet with the caret
// in a box the PANE legitimately changes, and asserting on it would report
// correct behaviour as a defect (InventoryItemFormScreen's packaging-chain row
// is the worked example: ctrl+u empties the "Units held" box). What must not
// happen there is a MOVE, and jdePlaceOf is exactly the record of where the
// operator is — every int field of the screen whose name says it holds a
// position. A *ListScreen in browse mode has no box holding the caret, so both
// halves are asserted, and the pane is what catches a window that scrolled
// without the cursor leaving its row (windowStart is not in jdePlaceOf's
// vocabulary).
//
// Both halves are POSITIVELY CONTROLLED per case (listNavChordControl), measured
// by the SAME predicate the chord is judged by, and the controls are counted: a
// set in which no fixture could be moved by the named key fails outright rather
// than reporting coverage it does not have.
func TestListNav_NoSurfaceBindsARetiredChord(t *testing.T) {
	if len(listNavRetiredChords) == 0 {
		t.Fatal("listNavRetiredChords is empty, so this check asserted nothing")
	}

	// A pane wide and tall enough that every fixture draws its frame, so a chord
	// held by a refused-pane gate cannot be mistaken for a chord nothing binds.
	const w, h = 120, 40

	controlled := map[string]int{}

	t.Run("columnar", func(t *testing.T) {
		for _, c := range jdePaneCases() {
			for chord, ctrl := range listNavChordControls {
				_, _, cb, ca := listNavPress(t, c.mk(), w, h, ctrl.probe, ctrl.key)
				if !reflect.DeepEqual(cb, ca) {
					controlled["columnar/"+chord]++
				}
				_, _, before, after := listNavPress(t, c.mk(), w, h, ctrl.probe, chord)
				if !reflect.DeepEqual(before, after) {
					t.Errorf("%s acts on %q: it moved the operator's place from %v to %v. "+
						"That chord is retired: %s\nNo bar in this program spells a chord, so "+
						"a key that moves here is one the operator can only find by guessing — "+
						"and the same key does nothing on the surface beside it.",
						c.name, chord, before, after, listNavRetiredChords[chord])
				}
			}
		}
	})

	// THE SCROLLER IS A THIRD FIXTURE SET, and it is the one that mattered most:
	// scroll.go is where two of these four chords were actually unbound, and
	// neither set above can reach it. No TextScroller holder embeds jdeScreen or
	// is a *ListScreen — they are all prose-bar receivers — and even if one were,
	// jdePlaceOf walks the int fields of the SCREEN struct, so an offset nested
	// inside a TextScroller value is invisible to it. Restoring
	// `case "ctrl+d", "pgdown":` in Handle therefore failed nothing at all: the
	// behavioural conversion was weaker than the regex it replaced on the single
	// file the retirement touched.
	//
	// What is asserted is the OFFSET (the scroller's whole visible product) and
	// the bool Handle returns, which is its contract with the fourteen detail
	// sheets that own one — a chord answered `true` is a keystroke the host
	// swallows on behalf of a binding that no longer exists.
	t.Run("scroller", func(t *testing.T) {
		build := func() *TextScroller {
			sc := NewTextScroller(10)
			lines := make([]string, 60)
			for i := range lines {
				lines[i] = fmt.Sprintf("line %d", i+1)
			}
			sc.Set(strings.Join(lines, "\n"))
			return sc
		}
		press := func(sc *TextScroller, keys ...string) {
			for _, k := range keys {
				sc.Handle(poPickerKeyMsg(k))
			}
		}
		for chord, ctrl := range listNavChordControls {
			control := build()
			press(control, ctrl.probe...)
			at := control.offset
			if control.Handle(poPickerKeyMsg(ctrl.key)) && control.offset != at {
				controlled["scroller/"+chord]++
			}

			sc := build()
			press(sc, ctrl.probe...)
			before := sc.offset
			handled := sc.Handle(poPickerKeyMsg(chord))
			why := listNavRetiredChords[chord]
			switch {
			case sc.offset != before:
				t.Errorf("TextScroller acts on %q: it scrolled from offset %d to %d. That "+
					"chord is retired: %s", chord, before, sc.offset, why)
			case handled:
				t.Errorf("TextScroller answers %q as handled without scrolling, so every "+
					"sheet holding one swallows the key on behalf of a binding that is "+
					"gone. That chord is retired: %s", chord, why)
			}
		}
	})

	t.Run("list", func(t *testing.T) {
		for _, surface := range listBarSurfaces() {
			for state, rows := range listRowCases {
				for chord, ctrl := range listNavChordControls {
					name := surface.name + " (" + state + ")"
					cpb, cpa, cb, ca := listNavPress(t, listWithRows(surface.build, rows), w, h, ctrl.probe, ctrl.key)
					if cpb != cpa || !reflect.DeepEqual(cb, ca) {
						controlled["list/"+chord]++
					}
					pb, pa, before, after := listNavPress(t, listWithRows(surface.build, rows), w, h, ctrl.probe, chord)
					why := listNavRetiredChords[chord]
					switch {
					case pb != pa:
						t.Errorf("%s acts on %q: the pane changed. That chord is retired: %s\n"+
							"--- before\n%s\n--- after\n%s", name, chord, why, pb, pa)
					case !reflect.DeepEqual(before, after):
						t.Errorf("%s acts on %q: it moved the operator's place from %v to %v "+
							"while drawing the same pane. That chord is retired: %s",
							name, chord, before, after, why)
					}
				}
			}
		}
	})

	for chord, ctrl := range listNavChordControls {
		for _, set := range []string{"columnar", "list", "scroller"} {
			if controlled[set+"/"+chord] == 0 {
				t.Errorf("no %s fixture was moved by %q, the key that spells the same "+
					"affordance as %q — so every %q-does-nothing case above passed for a "+
					"reason unrelated to the chord, and this half of the sweep is vacuous",
					set, ctrl.key, chord, chord)
			}
		}
	}
}

// TestListNav_TheVocabularyAndTheRetiredSetAreDisjoint: a keystroke cannot be
// both the navigation set's and retired.
//
// Without it the two halves of list_nav.go could disagree — listNavHint would
// promise a key the sweep above forbids anyone to bind — and the failure would
// show up as a footer naming a key nothing answers, which is the defect rather
// than the report of it.
func TestListNav_TheVocabularyAndTheRetiredSetAreDisjoint(t *testing.T) {
	for _, m := range listNavSet() {
		for _, k := range m.Keys {
			if why, retired := listNavRetiredChords[k]; retired {
				t.Errorf("%q is in the navigation set (it is spelled by %q) and also "+
					"recorded as retired (%s) — one of the two is wrong", k, m.Hint, why)
			}
		}
	}
}

// TestListNav_EveryHintSpellsExactlyTheKeysItClaims is the transcription rule
// over list_nav.go's own table, held against the sweep's table rather than by
// eye.
//
// listBarKeyNames (list_bar_honesty_test.go) is the independent transcription of
// what a footer TOKEN spells, and the honesty sweep parses real footers through
// it. If listNavSet's hints and that table disagree, the footer this vocabulary
// builds is pressed against a claim it did not make — the sweep would either
// skip a key or credit one, and both directions hide a defect. So each hint is
// split the way listNamedKeys splits a footer segment and required to resolve to
// exactly the keys the vocabulary says it spells.
func TestListNav_EveryHintSpellsExactlyTheKeysItClaims(t *testing.T) {
	for _, m := range listNavSet() {
		got := map[string]bool{}
		fields := strings.Fields(m.Hint)
		if len(fields) == 0 {
			t.Errorf("the navigation hint %q is empty", m.Hint)
			continue
		}
		head, ok := listBarKeyNames[fields[0]]
		if !ok {
			t.Errorf("the navigation hint %q leads with the token %q, which "+
				"listBarKeyNames does not know — the honesty sweep would fail on the "+
				"footer this hint builds", m.Hint, fields[0])
			continue
		}
		for _, k := range head {
			got[k] = true
		}
		for _, f := range fields[1:] {
			for _, k := range listBarAliasKeys[f] {
				got[k] = true
			}
		}
		want := map[string]bool{}
		for _, k := range m.Keys {
			want[k] = true
		}
		if !sameKeySet(got, want) {
			t.Errorf("the navigation hint %q spells %v, but listNavSet says it is the "+
				"affordance for %v — a hint credited with a key it does not spell is "+
				"this vocabulary making a claim on the bar's behalf",
				m.Hint, sortedKeys(got), m.Keys)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameKeySet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
