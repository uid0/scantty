// What a columnar screen does with a movement key on a pane it is not being
// drawn into.
//
// The layer REFUSES a pane too short to carry its action bar, its status row
// and one row of the screen's own content: it draws jdeTooShort and nothing
// else (jde_pane_fit_test.go holds that end of it). Nothing about that refusal
// used to reach the sheets' key handlers. Update ran exactly as it does on a
// pane twice the size, so every movement key went on acting — invisibly, on a
// frame nobody was looking at:
//
//   - `end` on the purchase-order detail's order pad set padScroll to the end
//     of a forty-line pad while the pane showed the notice. Growing the
//     terminal back landed the operator at the bottom of the pad instead of
//     where they left it.
//   - Up and Down went on walking `cursor` through the fields of every form
//     screen in the program, so an operator who dragged a terminal short,
//     pressed a key and dragged it back came back on a different field — with
//     the focus, and therefore the next thing they type, somewhere else.
//
// Both are standing rule 4: never silently discard or overwrite what the
// operator typed or where they were. The loss is silent in the strongest sense
// — the pane is a pure function of the terminal size at a refused height, so
// the press produces no change on it in either direction, and the operator has
// no way to suspect one happened.
//
// So this file asserts the rule the way an operator would state it: DRAG IT
// SHORT, PRESS THINGS, DRAG IT BACK, AND FIND WHAT YOU LEFT. It is asserted
// through the real clipped render at both ends, over every columnar screen the
// package has (jdePaneCases, derived in jde_pane_fit_test.go from the types
// that embed jdeScreen), because "apply the rule to the site that was
// reported" is the failure this project keeps paying for and the reported site
// here was one screen out of thirty-two.
package tui

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// jdeMoveTokens are the action-bar tokens that name a MOVEMENT key, and the
// keystrokes each one spells.
//
// The three tokens are transcribed from poBarKeyNames (po_view_jde_test.go),
// which is the one table both purchasing sweeps read, and the check below fails
// on any drift between them — a token that spelled something different here
// would be this sweep making a claim on the bar's behalf, which is the defect
// the bar tables exist to report.
//
// WHY THE BAR AND NOT A FIXED LIST OF KEYS. The rule under test is about the
// keys that move the operator's PLACE ON THE FRAME, and which keys those are is
// a fact about the screen: `home` scrolls the order pad and moves the CARET
// inside a focused text box on the line form, and the second is not this
// change's business — a box owns its own keys, caret included, because gating
// those would DISCARD input rather than preserve position. The bar is where a
// screen says which keys it claims, so it is what the sweep reads.
//
// It follows that a key a screen BINDS as movement and does not NAME is not
// pressed here. That is not a hole this file has to close: it is the bar
// honesty rule, and TestPOView_BarNamesExactlyTheKeysThatWork and the list
// sweep are what fail on it.
var jdeMoveTokens = map[string][]string{
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
	"Home/End":  {"home", "end"},
}

// TestJDEForm_TheMovementTokensMatchTheBarTable: the three tokens above spell
// exactly what the shared bar table says they spell.
//
// Transcribing rather than interpreting is the rule (AGENTS.md): a sweep that
// credited a token with a synonym would be making the claim it exists to check.
func TestJDEForm_TheMovementTokensMatchTheBarTable(t *testing.T) {
	for token, want := range jdeMoveTokens {
		got, ok := poBarKeyNames[token]
		if !ok {
			t.Errorf("the movement token %q is not in poBarKeyNames, so this file and the "+
				"purchasing sweeps disagree about what the bars spell", token)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the movement token %q spells %v here and %v in poBarKeyNames", token, want, got)
		}
	}
}

// jdeMoveKeysFor is the movement keys THIS screen names, anywhere on the range
// of heights it draws at.
//
// The union over heights is the point: a bar changes shape with the pane, and
// the supplier-switch confirm is the worked example — its prose fits at 80x40,
// so it names no scroll key there, and names UP/DN at every shorter height it
// still draws at. Reading the bar at one height would leave the key the defect
// lives on unpressed.
func jdeMoveKeysFor(t *testing.T, mk func() Screen, w int) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, h := range jdePaneHeights() {
		s := mk()
		jdeRootAt(t, s, w, h)
		bar := jdeBarOf(s.View())
		if bar == nil {
			continue // refused; it names nothing
		}
		text := strings.Join(bar, " ")
		for token, keys := range jdeMoveTokens {
			if !strings.Contains(text, token+"=") {
				continue
			}
			for _, k := range keys {
				seen[k] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// jdePlaceWords are the field-name substrings that mark a screen's record of
// WHERE THE OPERATOR IS. They are matched case-insensitively against the
// screen struct's own int fields, so the fingerprint below is derived from the
// screen rather than listed per screen — which is the only way a sweep over
// thirty-two of them can be trusted, since no two spell it the same way
// (cursor, pickCursor, kitCursor, rowCursor, levelCursor, resultCursor,
// padScroll, switchScroll, priceFocus, shipFocus, focused).
var jdePlaceWords = []string{"cursor", "scroll", "focus", "offset"}

// jdePlaceOf fingerprints where the operator is on a screen: every int field
// whose name says it holds a position, in declaration order.
//
// Reading it off the STRUCT rather than off the frame is deliberate and the
// two halves of the sweep need both. A frame comparison is what an operator
// would make and it is the assertion that matters; a fingerprint catches a
// position that drifted and happened to redraw the same — a cursor moved onto
// a row that renders identically, an offset clamped back on the way out — which
// is exactly the silent shape this whole file is about.
func jdePlaceOf(s Screen) []int64 {
	v := reflect.ValueOf(s)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	t := v.Type()
	var out []int64
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous || v.Field(i).Kind() != reflect.Int {
			continue
		}
		name := strings.ToLower(f.Name)
		for _, w := range jdePlaceWords {
			if strings.Contains(name, w) {
				out = append(out, v.Field(i).Int())
				break
			}
		}
	}
	return out
}

// jdeMoveTrace presses every movement key once, in sequence, and reports the
// screen after each press alongside the key that produced it.
//
// IN SEQUENCE and with no reset between them, for the reason AGENTS.md records
// about the picker notes: a sweep that rebuilds the state before every key
// tests each key against a state no operator is ever in, and the second press
// is where several of this project's defects have lived.
//
// EVERY step is reported rather than only the last, because the movement keys
// come in opposed pairs — up/down, pgup/pgdown, home/end — and a sweep that
// looked only at the end of the run would find the cursor back where it started
// having walked the whole form on the way. That is not a hypothetical either:
// it is what the first draft of this file did, and it reported thirty-two
// screens as having nothing to move.
//
// The PLACE is fingerprinted at each step rather than the screen kept, because
// a Screen is a POINTER: keeping it and reading the fingerprint afterwards
// reports the state at the END of the run for every step, which is the same
// blindness one indirection further along.
type jdeMoveStep struct {
	key   string
	place []int64
}

func jdeMoveTrace(s Screen, keys []string) ([]jdeMoveStep, Screen) {
	var out []jdeMoveStep
	for _, k := range keys {
		next, _ := s.Update(poPickerKeyMsg(k))
		if next != nil {
			s = next
		}
		out = append(out, jdeMoveStep{key: k, place: jdePlaceOf(s)})
	}
	return out, s
}

// jdeRefusedHeights are the terminal heights at which this screen draws the
// layer's refusal notice instead of a frame, at this width. Derived by asking
// the screen, exactly as jde_pane_fit_test.go's sweeps do — the set differs per
// screen, because a taller bar and a pinned header both raise the floor.
func jdeRefusedHeights(t *testing.T, mk func() Screen, w int) []int {
	t.Helper()
	var out []int
	for _, h := range jdePaneHeights() {
		s := mk()
		jdeRootAt(t, s, w, h)
		if jdeBarOf(s.View()) == nil {
			out = append(out, h)
		}
	}
	return out
}

// TestJDEForm_ARefusedPaneKeepsTheOperatorsPlace: drag the terminal short,
// press every movement key, drag it back, and the screen is exactly where it
// was.
//
// The frame comparison is made at a height the screen really draws at, on
// Root.View() so the render is the CLIPPED one, because the whole defect is
// invisible at the refused height itself: the notice is a pure function of the
// pane, so it is byte-identical before and after the press whether or not the
// press destroyed anything.
func TestJDEForm_ARefusedPaneKeepsTheOperatorsPlace(t *testing.T) {
	const tall = 40
	checked := 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			keys := jdeMoveKeysFor(t, mk, w)
			if len(keys) == 0 {
				continue // this screen names no movement key at any height
			}
			refused := jdeRefusedHeights(t, mk, w)
			for _, h := range refused {
				s := mk()
				r := jdeRootAt(t, s, w, tall)
				before, beforePlace := r.View(), jdePlaceOf(s)

				jdeRootAt(t, s, w, h)
				trace, moved := jdeMoveTrace(s, keys)
				s = moved
				for _, step := range trace {
					if !reflect.DeepEqual(beforePlace, step.place) {
						t.Errorf("%s at %dx%d (refused): %q moved the operator's place from "+
							"%v to %v on a pane drawing nothing but the too-short notice. "+
							"Growing the terminal back puts them somewhere they never "+
							"navigated to", name, w, h, step.key, beforePlace, step.place)
						break
					}
				}
				r = jdeRootAt(t, s, w, tall)
				after := r.View()
				checked++

				if before != after {
					t.Errorf("%s: pressing the movement keys at %dx%d (refused) changed what "+
						"the screen draws when the terminal grows back to %dx%d:\n--- before\n%s\n"+
						"--- after\n%s", name, w, h, w, tall, before, after)
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no columnar screen was refused at any supported size, so this sweep " +
			"asserted nothing. The shortest supported terminal is what jdePaneHeights " +
			"derives; if the layer now fits every frame into it, this test needs " +
			"rewriting rather than deleting")
	}
}

// jdeInertCases are the (screen, state) pairs where no movement key moves
// anything even on a pane that fits — so the sweep above passes over them for
// a reason that has nothing to do with the rule it is checking.
//
// A case in here is a case proving nothing, which is why each one carries a
// reason and why the check below fails on a case that is inert and NOT listed.
// The vacuous-fixture rule (AGENTS.md) is the whole point: this file's
// assertion is that a movement key does not move anything, and a fixture where
// no key moves anything at any height satisfies it without ever exercising it.
var jdeInertCases = map[string]string{
	"PurchaseOrderAddLineScreen": "the base fixture opens on the identifier row — one text box, " +
		"no cursor and no scrolled body — so its bar names no movement key at any height " +
		"and there is nothing for one to move. The phases that HAVE one are swept as " +
		"PurchaseOrderAddLineScreen/choose and /confirm.",
	"PurchaseOrderCreateScreen/item search open": "the filter box owns the keyboard while it " +
		"is open, so barItems returns early: the bar names Esc and Enter and nothing else, " +
		"and the movement keys are swallowed by the textinput rather than moving the list.",
}

// TestJDEForm_EveryRefusedPaneCaseCouldHaveMoved: at a height the screen DOES
// draw, the same key sequence moves the same fingerprint.
//
// Without this the sweep above is one long assertion that nothing happened,
// which is also what it would report if the fixtures had no movable cursor,
// if poPickerKeyMsg stopped translating, or if Update stopped being reached at
// all. This is the half that says the presses land.
func TestJDEForm_EveryRefusedPaneCaseCouldHaveMoved(t *testing.T) {
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		keys := jdeMoveKeysFor(t, mk, 80)
		moved := false
		for _, h := range jdePaneHeights() {
			s := mk()
			jdeRootAt(t, s, 80, h)
			if jdeBarOf(s.View()) == nil {
				continue // refused; there is nothing to prove the keys land on
			}
			before := jdePlaceOf(s)
			trace, _ := jdeMoveTrace(s, keys)
			for _, step := range trace {
				if !reflect.DeepEqual(before, step.place) {
					moved = true
				}
			}
			if moved {
				break
			}
		}
		reason, listed := jdeInertCases[name]
		switch {
		case moved && listed:
			t.Errorf("%s is recorded in jdeInertCases (%q) but its movement keys DO move "+
				"something at a height the screen draws. A stale exception is a case "+
				"excused from the sweep it passes", name, reason)
		case !moved && !listed:
			t.Errorf("%s: no movement key changes this screen's place at ANY height it "+
				"draws at, so the refused-pane sweep passes over it without ever "+
				"exercising the rule. Give the fixture a body it can move in, or record "+
				"it in jdeInertCases with the reason", name)
		}
	}
}
