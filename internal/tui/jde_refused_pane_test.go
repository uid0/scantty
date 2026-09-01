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
	"unicode/utf8"
)

// jdeMoveTokens are the action-bar tokens that name a MOVEMENT key, and the
// keystrokes each one spells.
//
// They are transcribed from the package's own bar tables — poBarKeyNames
// (po_view_jde_test.go), which both purchasing sweeps read, and
// receiveBarKeyNames beside it — and the check below fails on any drift between
// them — a token that spelled something different here
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
	"PgUp":      {"pgup"},
	"Home/End":  {"home", "end"},
}

// TestJDEForm_TheMovementTokensMatchTheBarTable: every token above spells
// exactly what the shared bar table says it spells.
//
// Transcribing rather than interpreting is the rule (AGENTS.md): a sweep that
// credited a token with a synonym would be making the claim it exists to check.
//
// This direction ALONE is not enough and never was — a token the bars draw and
// this file has never heard of passes it in silence. That is what
// TestJDEForm_EveryMovementTokenABarDrawsIsInTheTable is for, and "PgUp" is the
// token it found.
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

	// The two ways a line comes OFF an order. Both are single-decision frames
	// with no cursor and no scrolled body, so their bars name no movement key at
	// any height and there is nothing for one to move — which is the point of
	// them: an irreversible destroy and a written void are answered from where
	// you stand, not navigated to.
	//
	// Neither is thereby exempt from the pane rules: they are in
	// jdeScreenStates for TestJDEForm_EveryFrameFitsEveryPane, which is where
	// the delete confirm's block — the line's identity, its numbers and two
	// folded caveats — has to give ground in order at a short pane.
	"PurchaseOrderEditScreen/delete confirm": "a confirmation, not a list: Ctrl-X and Esc " +
		"are the only keys the bar names, there is no cursor and no body to scroll, so " +
		"no movement key has anything to move.",
	"PurchaseOrderEditScreen/void prompt": "one reason box owns the keyboard, so the bar " +
		"names Enter and Esc alone and the movement keys belong to the textinput rather " +
		"than to a cursor.",

	// The MINIMAL-list states, which are inert BY CONSTRUCTION and are here for
	// a different sweep. Each has exactly one navigable row — the trailing add
	// row, or the single file — so no movement key can move anything.
	//
	// They earn their keep in
	// TestJDEForm_ThePagingPairIsNamedExactlyWhereAPageMoves, which is the sweep
	// they were added for: that is where "the body overflows" and "a page has
	// somewhere to land" come apart, and until these existed every list in
	// jdeScreenStates carried several rows and the pair could not be told apart.
	//
	// WHAT THEIR BARS CLAIM, said here because this is the one place a reader
	// meets these states: NOTHING. They used to go on naming UP/DN with nothing
	// to move — only PurchaseOrderAttachmentsScreen/one file read as this table's
	// reason described — and that gap is CLOSED now, which is why this note reads
	// as history rather than as a warning.
	//
	// THE RULE, kept here because it is what stays true as sheets are added: a
	// bar that appends the movement pair WITHOUT consulting its navigable row
	// count names a key a CLAMPING list cursor cannot honour once the list is
	// down to ONE navigable row. jdeClampPick returns the row it was handed, so
	// the cursor stays, no note is written, and the pane redraws byte for byte
	// under a bar saying UP/DN. A FIELD form is not an instance while it has two
	// or more fields: its cursor WRAPS, so UP/DN always changes the row — the
	// exemption AGENTS.md records, and jdeRowMoves covers the one-field case
	// anyway.
	//
	// HOW IT WAS CLOSED, in the shape AGENTS.md predicted. The LAYER carried most
	// of it: jdePickBarWith is the bar every columnar picker draws and it takes
	// the option count now, so that was ONE edit and not one per screen
	// (jdeRowMoves is the predicate, asked by the bar AND by pickRow/moveRow, so
	// the claim and the arm are one expression). jdeMoveItem is the same question
	// for the builders that are not pickers. The state is reachable without any
	// fixture — filter a single-select picker to a query nothing matches and the
	// synthetic "(none)" row is left standing (it is prepended before the filter
	// runs), so the count is 1.
	//
	// WHAT FOUND THE INSTANCES was not a roster:
	// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves generalised the
	// paging biconditional to the whole movement vocabulary, and it reported the
	// three that were live — the kit list, the packaging chain and the storage
	// level list, all in their EMPTY state. The others (po_edit's association
	// picker, po_add_line's choose phase, maintenance_item_form's sub-lists) came
	// off the same grep the rule above describes and are gated by the same
	// predicate, so no fixture has to reach them for them to be right.
	//
	// The convention was already kept wherever a builder consulted its count,
	// which is what it was copied from: po_attachments' listMoves(),
	// service_status_screen nesting the entry inside len(services) > 1,
	// po_create's barItems on rowCount() > 1, receive_form's reviewBarItems on
	// reviewRows() > 1.
	"InventoryItemFormScreen/kit list empty":     "one navigable row (the add row), so nothing moves",
	"InventoryItemFormScreen/chain list empty":   "one navigable row (the add row), so nothing moves",
	"StorageSlotGenerateScreen/level list empty": "one navigable row (the add row), so nothing moves",
	"PurchaseOrderAttachmentsScreen/one file":    "one file, so listMoves is false and the cursor has nowhere to go",
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

// jdeMovementKeystrokes are the keystrokes this file's rule is about: the ones
// whose whole product is where the operator is standing. Read off nothing — a
// key is on this list because moving is all it does.
var jdeMovementKeystrokes = map[string]bool{
	"up": true, "down": true, "pgup": true, "pgdown": true, "home": true, "end": true,
}

// jdeBarTokens is every KEY token a drawn bar spells, read off the bar the
// terminal really gets.
//
// A bar item renders as "Key=Label" and a key never carries a space while a
// label routinely does, so the token is the run of non-space characters ending
// at each "=" — which is what lets this walk a bar it was never told the shape
// of. That is the point: the forward direction below has to enumerate what the
// bar SAYS rather than what some table expects it to say.
func jdeBarTokens(bar []string) []string {
	var out []string
	for _, line := range bar {
		plain := stripANSI(line)
		for i, r := range plain {
			if r != '=' {
				continue
			}
			start := i
			for start > 0 {
				prev := plain[:start]
				last, size := utf8.DecodeLastRuneInString(prev)
				if last == ' ' || last == '=' {
					break
				}
				start -= size
			}
			if tok := plain[start:i]; tok != "" {
				out = append(out, tok)
			}
		}
	}
	return out
}

// jdeUnresolvedBarTokens are the tokens the columnar bars draw that neither
// transcription table has an entry for, each with the reason it cannot be
// spelling a movement key.
//
// A token is recorded here rather than skipped, so "absent" and "allowed" stay
// different states: the check below fails on a token that is in no table and no
// entry here, which is what stops a bar token quietly meaning a movement key
// nothing presses.
var jdeUnresolvedBarTokens = map[string]string{
	"←→": "the layer's arrow token for changing the VALUE on the row the cursor " +
		"already stands on — a toggle, a select, a level's place in a chain. It " +
		"moves nothing about where the operator is, which is the only thing this " +
		"file gates, and neither purchasing nor receiving binds it at all",
}

// jdeResolveBarToken says which keystrokes a drawn bar token spells, reading the
// two TRANSCRIPTION tables the package already keeps — poBarKeyNames for the
// columnar purchasing screens and receiveBarKeyNames for the receiving form.
//
// Two tables and not one because that is what the package HAS: the receiving
// flow brought its own sweep and its own table, and a third table written here
// would be a third chance to credit a bar with a claim it never made. Where they
// overlap they must agree, and the check below fails when they do not — a token
// meaning two things is exactly the drift a transcription table exists to catch.
func jdeResolveBarToken(token string) ([]string, bool) {
	po, inPO := poBarKeyNames[token]
	rc, inReceive := receiveBarKeyNames[token]
	switch {
	case inPO && inReceive:
		if !reflect.DeepEqual(po, rc) {
			return nil, false
		}
		return po, true
	case inPO:
		return po, true
	case inReceive:
		return rc, true
	}
	return nil, false
}

// TestJDEForm_TheTwoBarTablesAgreeWhereTheyOverlap: a token both transcription
// tables know spells the same keystrokes in each.
//
// jdeResolveBarToken reads both, so a disagreement between them would make the
// resolution depend on which table was consulted first — and it reports as an
// UNRESOLVED token in the sweep below, which reads as a missing entry rather
// than as the contradiction it is. This is where it says what it is.
func TestJDEForm_TheTwoBarTablesAgreeWhereTheyOverlap(t *testing.T) {
	shared := 0
	for token, po := range poBarKeyNames {
		rc, ok := receiveBarKeyNames[token]
		if !ok {
			continue
		}
		shared++
		if !reflect.DeepEqual(po, rc) {
			t.Errorf("the bar token %q spells %v in poBarKeyNames and %v in "+
				"receiveBarKeyNames — one of them is interpreting rather than "+
				"transcribing", token, po, rc)
		}
	}
	if shared == 0 {
		t.Error("the two bar tables share no token at all, so this check asserted " +
			"nothing and jdeResolveBarToken's agreement rule is untested")
	}
}

// TestJDEForm_EveryMovementTokenABarDrawsIsInTheTable is the OTHER direction of
// TestJDEForm_TheMovementTokensMatchTheBarTable, and it is the direction that
// matters.
//
// The first check proves the tokens jdeMoveTokens LISTS spell what poBarKeyNames
// says they spell. That is safe in one direction only, and it is verbatim the
// failure AGENTS.md records for listNamedKeys against listAllBarKeys: a bar
// token this file has never heard of contributes no movement key, so
// jdeMoveKeysFor returns nothing for that frame, the refused-pane sweep presses
// nothing there, and the non-vacuity half then demands a jdeInertCases entry —
// which dresses the hole up as a documented exclusion.
//
// That is not hypothetical. The receiving form's all-units-answered frame spells
// its step-back key as the token "PgUp" ALONE, and while jdeMoveTokens held only
// "PgUp/PgDn" that frame was swept for movement it could not report: PgUp moved
// serialCursor on a pane the layer refuses and nothing in this file pressed it.
//
// So the roster is DERIVED from what the bars actually draw, over every case and
// every pane the fixtures reach, and a token that resolves to a movement
// keystroke and is missing from jdeMoveTokens FAILS.
func TestJDEForm_EveryMovementTokenABarDrawsIsInTheTable(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				bar := jdeBarOf(s.View())
				if bar == nil {
					continue // refused; it draws no bar to read
				}
				for _, token := range jdeBarTokens(bar) {
					if seen[token] {
						continue
					}
					seen[token] = true
					keys, ok := jdeResolveBarToken(token)
					if !ok {
						if _, excused := jdeUnresolvedBarTokens[token]; !excused {
							t.Errorf("%s at %dx%d draws the bar token %q, which neither bar "+
								"table spells and jdeUnresolvedBarTokens does not excuse. An "+
								"unresolved token contributes no movement key, so a frame "+
								"spelling one is swept for a rule it can never report",
								name, w, h, token)
						}
						continue
					}
					moves := false
					for _, k := range keys {
						if jdeMovementKeystrokes[k] {
							moves = true
						}
					}
					if !moves {
						continue
					}
					if _, listed := jdeMoveTokens[token]; !listed {
						t.Errorf("%s at %dx%d draws the bar token %q, which the bar tables say "+
							"spells %v — a movement key — and jdeMoveTokens does not list it. "+
							"Every sweep in this file would press nothing for that frame and "+
							"report it as having nothing to move", name, w, h, token, keys)
					}
				}
			}
		}
	}
	if len(seen) == 0 {
		t.Error("no columnar bar drew a single token, so this check asserted nothing")
	}
}

// TestJDEForm_EscStillLeavesOnARefusedPane: the notice's own promise, kept.
//
// jdeTooShort tells the operator, on the pane it is replacing, that "Esc still
// leaves" — which is the whole reason the silence above is legitimate. A
// movement key answering with nothing is only acceptable while the notice is a
// standing answer the operator can ACT on, and esc is the one key that acts on
// it. A documented claim the code does not honour is a defect in either
// direction, so it is asserted rather than assumed: the gate went into 107
// handlers, and an esc arm that fell inside one by accident would leave a
// terminal dragged short with no way out and a sentence saying there is.
//
// The comparison is against the SAME screen at a height that draws, so the
// check needs no roster of what esc means per screen: some close a sub-phase,
// some pop the stack, one opens a confirm. What it forbids is esc doing
// something at a drawable height and NOTHING at a refused one.
func TestJDEForm_EscStillLeavesOnARefusedPane(t *testing.T) {
	const tall = 40
	checked := 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			ref := mk()
			jdeRootAt(t, ref, w, tall)
			place := jdePlaceOf(ref)
			nextRef, cmdRef := ref.Update(poPickerKeyMsg("esc"))
			// A screen where esc does nothing even on a drawn pane makes no
			// claim this check can hold; it is the bar sweeps' business.
			if reflect.DeepEqual(place, jdePlaceOf(nextRef)) && cmdRef == nil {
				continue
			}
			for _, h := range jdeRefusedHeights(t, mk, w) {
				s := mk()
				jdeRootAt(t, s, w, h)
				place := jdePlaceOf(s)
				next, cmd := s.Update(poPickerKeyMsg("esc"))
				checked++
				if reflect.DeepEqual(place, jdePlaceOf(next)) && cmd == nil {
					t.Errorf("%s at %dx%d: the notice drawn on that pane says \"Esc still "+
						"leaves\" and esc did nothing, while at %dx%d it acts. A terminal "+
						"dragged short would have no way out and a sentence saying it has",
						name, w, h, w, tall)
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no columnar screen was refused at any supported size with an esc that " +
			"acts, so this check asserted nothing")
	}
}
