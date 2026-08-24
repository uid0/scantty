package tui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// The rules the add-line flow is held to, asserted as RULES rather than one
// entry at a time — and every set they need is DERIVED from its authority, so
// an omission fails the build instead of failing silently.
//
// Three separate defects reached an operator's terminal in this package because
// a sweep was reading a hand-kept roster the key was not in (AGENTS.md: `N` on
// the purchasing list, `tab` on the supplier picker, `enter` on the
// supplier-switch confirm). So here:
//
//	PHASES come from the poAddPhase iota, walked to poAddPhaseCount.
//	KEYS   come from poKeySpace() — every printable ASCII rune plus the named
//	       specials — because no authority for "the keys a terminal can send"
//	       can be derived, so the answer is to press the whole space.
//	FIELDS come from reflect over the screen struct: every one is either in the
//	       fingerprint or declared as something a key may move WITHOUT acting.
//	FOCUS  comes from reflect for textinput fields, because a caret lives INSIDE
//	       the value rather than beside it and the field-name check cannot see it.

// ---------------------------------------------------------------------------
// Reaching each phase
// ---------------------------------------------------------------------------

// poAddSweepFake is the catalogue every phase of the sweep is reached through.
// "widget" is a partial name on two rows (so it is ambiguous) and
// "AF-99-12-ZP-LH-HEAVY" is an exact supplier SKU on one (so it resolves) — the
// two halves of the ambiguity rule, from one fixture.
func poAddSweepFake() *poAddFake { return &poAddFake{rows: poAddRows()} }

// poAddPhaseCase is one phase of the flow, driven into a real state through
// Root.Update against the httptest fake.
type poAddPhaseCase struct {
	phase poAddPhase
	name  string
	// typing marks a phase where a text input owns the keyboard. On those the
	// rule is about COMMAND keys only: a printable rune typed into a field acts
	// by editing the value, which is what the field is FOR, so the reverse
	// direction ("a key the bar does not name must not act") cannot apply to it.
	typing bool
	// reach drives a freshly opened flow into this phase.
	reach func(t *testing.T, r Root, s *PurchaseOrderAddLineScreen) Root
}

// poAddPhasesWithoutKeys records a phase where NO key acts, so "no entry" and
// "nothing to check" cannot look the same. It is empty today, and a phase added
// to it needs a reason.
var poAddPhasesWithoutKeys = map[poAddPhase]string{}

func poAddPhaseCases() []poAddPhaseCase {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	// pressed drives with the pump, so a lookup or an add is resolved.
	pressed := func(keys ...tea.KeyMsg) func(*testing.T, Root, *PurchaseOrderAddLineScreen) Root {
		return func(t *testing.T, r Root, _ *PurchaseOrderAddLineScreen) Root {
			for _, k := range keys {
				r = key(t, r, k)
			}
			return r
		}
	}
	// inFlight drives with the pump up to the last key, and fires THAT one with
	// a bare Update so its request is genuinely still out.
	inFlight := func(keys ...tea.KeyMsg) func(*testing.T, Root, *PurchaseOrderAddLineScreen) Root {
		return func(t *testing.T, r Root, _ *PurchaseOrderAddLineScreen) Root {
			for _, k := range keys[:len(keys)-1] {
				r = key(t, r, k)
			}
			next, _ := r.Update(keys[len(keys)-1])
			return next.(Root)
		}
	}
	// typed puts an identifier in the box WITHOUT keystrokes, then drives.
	//
	// Not a shortcut around the flow: what is under test here is the key pressed
	// AFTER the reach, and every reach still goes through the real lookup and the
	// real add. Typing it a rune at a time costs a cursor-blink timer per key
	// (bubbles ticks it at 530ms and pump abandons it after 200), which this
	// sweep pays once per rebuild — and it rebuilds every time a key acts.
	typed := func(id string, drive func(*testing.T, Root, *PurchaseOrderAddLineScreen) Root) func(*testing.T, Root, *PurchaseOrderAddLineScreen) Root {
		return func(t *testing.T, r Root, s *PurchaseOrderAddLineScreen) Root {
			s.idIn.SetValue(id)
			return drive(t, r, s)
		}
	}
	return []poAddPhaseCase{
		// The identifier row is reached with something ALREADY TYPED, because on
		// an empty box enter declines with "type something first" — and a decline
		// is not an act, so the bar's "Enter=Look up" would read as dead for the
		// wrong reason.
		{poAddPhaseIdentify, "identify", true, typed("AF-77", pressed())},
		{poAddPhaseLooking, "looking", false, typed("AF-77", inFlight(enter))},
		{poAddPhaseChoose, "choose", false, typed("widget", pressed(enter))},
		{poAddPhaseConfirm, "confirm", false, typed("AF-99-12-ZP-LH-HEAVY", pressed(enter))},
		{poAddPhasePrice, "price", true, typed("AF-99-12-ZP-LH-HEAVY", pressed(enter, enter))},
		{poAddPhaseAdding, "adding", false, typed("AF-99-12-ZP-LH-HEAVY", inFlight(enter, enter, enter))},
	}
}

// TestPOAddLine_EveryPhaseIsSwept walks the poAddPhase iota to its sentinel and
// fails on any phase the sweeps below have no entry for. This is what makes
// their coverage a property of the code rather than of who last remembered to
// extend a table.
func TestPOAddLine_EveryPhaseIsSwept(t *testing.T) {
	covered := map[poAddPhase]bool{}
	for _, c := range poAddPhaseCases() {
		covered[c.phase] = true
	}
	for p := poAddPhase(0); p < poAddPhaseCount; p++ {
		if covered[p] {
			if why, ok := poAddPhasesWithoutKeys[p]; ok {
				t.Errorf("phase %v is both swept and recorded as keyless (%q)", p, why)
			}
			continue
		}
		if _, ok := poAddPhasesWithoutKeys[p]; !ok {
			t.Errorf("phase %v has no poAddPhaseCases entry and is not recorded in "+
				"poAddPhasesWithoutKeys — a key on it would be judged by nothing", p)
		}
	}
}

// ---------------------------------------------------------------------------
// The state a key may move
// ---------------------------------------------------------------------------

// poAddState is the observable state of the flow. Text inputs contribute their
// VALUE and their FOCUS, never their View: a blinking caret moving is not a key
// acting, and a fingerprint a caret can move is one any keypress passes.
func poAddState(s *PurchaseOrderAddLineScreen) string {
	var b strings.Builder
	fmt.Fprint(&b, s.phase, "|", s.pending, s.addSeq, s.lookupSeq,
		"|", s.cursor, s.chosenFrom, s.scroll, s.priceFocus, s.priceEdited,
		"|", s.idIn.Value(), s.idIn.Focused(),
		"|", s.qtyIn.Value(), s.qtyIn.Focused(),
		"|", s.costIn.Value(), s.costIn.Focused(),
		"|", len(s.added), s.terminalWidth, s.terminalHeight)
	if s.chosen != nil {
		fmt.Fprint(&b, "|chosen:", s.chosen.ItemSupplier)
	}
	if s.lookup != nil {
		fmt.Fprint(&b, "|lookup:", s.lookup.Query, len(s.lookup.Candidates), s.lookup.Resolves)
	}
	if s.po != nil {
		fmt.Fprint(&b, "|po:", len(s.po.Items))
	}
	return b.String()
}

// poAddStateFingerprinted / poAddStateDeclined classify EVERY field of the
// screen. The struct is enumerated by reflection, so a field added tomorrow
// fails by name until somebody decides which half it belongs in — the omission
// cannot be made quietly, which is the whole point.
var poAddStateFingerprinted = map[string]bool{
	"jdeScreen": true, "phase": true, "pending": true, "addSeq": true,
	"lookupSeq": true, "cursor": true, "chosen": true, "chosenFrom": true,
	"scroll": true, "priceFocus": true, "priceEdited": true,
	"idIn": true, "qtyIn": true, "costIn": true,
	"lookup": true, "added": true, "po": true,
}

// poAddStateDeclined is every field a key may move WITHOUT having acted, with
// the reason. All of them are the screen ANSWERING a key: counting them would
// call "j does nothing here" an action and invert the rule the sweep enforces.
var poAddStateDeclined = map[string]string{
	"deps":       "injected dependencies; no keystroke reaches them",
	"poID":       "the order being added to, fixed at construction",
	"poNumber":   "the order's number, fixed at construction",
	"supplier":   "the supplier's name as the order reported it, fixed at construction",
	"note":       "the screen's answer to the last keypress — a decline writes here",
	"failHead":   "the failure line's headline, written only by a reply off the wire",
	"failDetail": "the failure line's unbounded half, written with failHead",
}

func TestPOAddLineScreen_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(PurchaseOrderAddLineScreen{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		_, in := poAddStateFingerprinted[name]
		_, out := poAddStateDeclined[name]
		switch {
		case in && out:
			t.Errorf("field %q is in BOTH poAddStateFingerprinted and poAddStateDeclined", name)
		case !in && !out:
			t.Errorf("field %q is in neither poAddStateFingerprinted nor poAddStateDeclined — "+
				"a key that moves it would be judged by nothing", name)
		}
	}
	for name := range poAddStateFingerprinted {
		if !seen[name] {
			t.Errorf("poAddStateFingerprinted names %q, which the screen no longer has", name)
		}
	}
	for name := range poAddStateDeclined {
		if !seen[name] {
			t.Errorf("poAddStateDeclined names %q, which the screen no longer has", name)
		}
	}
}

// poAddFocusFingerprinted is every input whose FOCUS poAddState carries, keyed
// by the field that holds it. Derived against the struct below, because a caret
// lives inside a textinput rather than beside it: the field-name check above
// cannot see focus at all, and a key that only moves the caret INTO a field was
// invisible to every fingerprint in this package until somebody noticed.
var poAddFocusFingerprinted = map[string]bool{
	"idIn": true, "qtyIn": true, "costIn": true,
}

func TestPOAddLineScreen_EveryInputFocusIsFingerprinted(t *testing.T) {
	typ := reflect.TypeOf(PurchaseOrderAddLineScreen{})
	input := reflect.TypeOf(textinput.Model{})
	found := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		ft := f.Type
		for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array || ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft != input {
			continue
		}
		found[f.Name] = true
		if !poAddFocusFingerprinted[f.Name] {
			t.Errorf("field %q holds a textinput whose focus poAddState does not carry — "+
				"a key that only moves the caret into it would be judged by nothing", f.Name)
		}
	}
	if len(found) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}
	for name := range poAddFocusFingerprinted {
		if !found[name] {
			t.Errorf("poAddFocusFingerprinted names %q, which is no longer a textinput", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The bar names exactly the keys that work
// ---------------------------------------------------------------------------

// poAddBarKeyNames maps a bar entry's Key to the keystrokes it SPELLS, and to
// no synonyms. Credit for a synonym would be the sweep making a claim on the
// bar's behalf — the defect it exists to report, sitting inside the check.
var poAddBarKeyNames = map[string][]string{
	"Enter":     {"enter"},
	"Esc":       {"esc"},
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
	"Home/End":  {"home", "end"},
}

func poAddNamedKeys(t *testing.T, bar []actionBarItem) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	for _, it := range bar {
		keys, ok := poAddBarKeyNames[it.Key]
		if !ok {
			t.Fatalf("bar entry %q is not in poAddBarKeyNames — add it so the rule covers it", it.Key)
		}
		for _, k := range keys {
			named[k] = true
		}
	}
	return named
}

// poAddHarness serves one fake for a whole subtest and hands back a builder
// that opens a fresh flow against it, so a probe keystroke cannot leak into the
// next assertion and the sweep does not stand up a server per keypress.
//
// It constructs the flow screen directly rather than pressing `n` on the
// purchase-order sheet. That entry point is not what these sweeps are about and
// it costs a screen load plus a cursor-blink timer per rebuild;
// TestPOAddLine_TheSheetOffersItOnlyOnADraft and the end-to-end drive in
// po_add_line_test.go are what hold it.
func poAddHarness(t *testing.T, width, height int) func(*testing.T) (Root, *PurchaseOrderAddLineScreen) {
	t.Helper()
	fake := poAddSweepFake()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	return func(t *testing.T) (Root, *PurchaseOrderAddLineScreen) {
		t.Helper()
		s := NewPurchaseOrderAddLineScreen(deps, &omsapi.PurchaseOrder{
			ID: "po-1", Number: "PO-2026-0042", Status: "draft",
			SupplierDetails: "Acme Fasteners & Industrial Supply Co.",
		})
		r := newTestRoot(s)
		r.deps = deps
		next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		return next.(Root), s
	}
}

// poAddPaneSizes are the pane heights every state is checked at. 24 is the
// terminal this interface is modelled on and the one that clips; 30 is there so
// a frame cannot be tuned for the short pane.
var poAddPaneSizes = []int{24, 30}

// TestPOAddLine_EveryPhaseNamesExactlyTheKeysThatWork presses the whole key
// space at every phase, in both directions: a key the bar names must change
// something, and a key it does not name must not.
//
// A named key is probed from several positions, because a cursor at the top of
// a list cannot move up and one at the bottom cannot move down: a key is dead
// only if it does nothing from ANY of them.
//
// The reverse direction asks for no STATE change rather than no COMMAND,
// because a declining key is meant to answer with a sentence and change
// nothing — that answer is the rule this screen exists to keep, not a key that
// escaped the audit.
func TestPOAddLine_EveryPhaseNamesExactlyTheKeysThatWork(t *testing.T) {
	space := poKeySpace()
	probes := [][]string{nil, {"down"}, {"end"}}
	for _, c := range poAddPhaseCases() {
		for _, height := range poAddPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := poAddHarness(t, 80, height)
				fresh := func(t *testing.T, probe []string) (Root, *PurchaseOrderAddLineScreen) {
					t.Helper()
					r, s := build(t)
					r = c.reach(t, r, s)
					if s.phase != c.phase {
						t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
					}
					for _, p := range probe {
						next, _ := r.Update(poPhaseKeyMsg(p))
						r = next.(Root)
					}
					return r, s
				}
				_, screen := fresh(t, nil)
				bar := screen.bar()
				named := poAddNamedKeys(t, bar)

				for _, k := range space {
					if c.typing && (poIsPrintable(k) || poFieldKeys[k] || poFormNavAliases[k]) && !named[k] {
						continue
					}
					acted := false
					for _, probe := range probes {
						pr, ps := fresh(t, probe)
						before := poAddState(ps)
						_, cmd := pr.Update(poPhaseKeyMsg(k))
						if poAddState(ps) != before || poCmdActs(cmd) {
							acted = true
						}
					}
					switch {
					case named[k] && !acted:
						t.Errorf("%s names %q but pressing it changes nothing (bar: %+v)", c.name, k, bar)
					case !named[k] && acted:
						t.Errorf("%s does not name %q, but pressing it acts (bar: %+v)", c.name, k, bar)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// No two declining keys redraw the same pane
// ---------------------------------------------------------------------------

// poAddSilentKeys records, per phase, the keys that answer with NOTHING from
// that phase's resting position — and why that is honest rather than the
// reported hang.
//
// Every entry is a NAMED key resting against an edge it cannot move past. The
// highlight (or the absent "↑ more above" marker) is on the pane and visibly at
// that edge, so the press has already answered itself; a sentence there would
// be noise on a frame that is already correct. A key that is NOT named and is
// silent is the defect, and the sweep below fails on it.
var poAddSilentKeys = map[poAddPhase]map[string]string{
	poAddPhaseChoose: {
		"up": "the highlight is drawn at the top of the list and cannot move past it",
	},
	poAddPhaseConfirm: {
		"up":   "the body is at the top; no ↑ marker is drawn, so the frame already says so",
		"home": "the body is already at the top",
		"pgup": "the body is at the top",
	},
}

// TestPOAddLine_NoTwoDecliningKeysRedrawTheSamePane is the other half of "a
// keypress that changes nothing visible IS the reported bug".
//
// A decline is a PURE note write — the fingerprint sweep above proves it moves
// nothing else — so pressing key A then key B leaves the pane B alone would
// have produced. Two declining keys that share a sentence therefore redraw each
// other's pane byte for byte, which from the operator's seat is a program that
// stopped responding. Requiring the key→pane map to be INJECTIVE is that
// property checked over the whole key space at once, rather than over the pairs
// somebody thought to enumerate.
//
// It runs on the NON-TYPING phases. On a phase with a focused field the caret
// is on the pane and the field owns the keys the sweep would otherwise be
// pressing, so the property is neither true nor meaningful there.
func TestPOAddLine_NoTwoDecliningKeysRedrawTheSamePane(t *testing.T) {
	space := poKeySpace()
	for _, c := range poAddPhaseCases() {
		if c.typing {
			continue
		}
		for _, height := range poAddPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := poAddHarness(t, 80, height)
				fresh := func(t *testing.T) (Root, *PurchaseOrderAddLineScreen) {
					t.Helper()
					r, s := build(t)
					r = c.reach(t, r, s)
					if s.phase != c.phase {
						t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
					}
					return r, s
				}
				base, screen := fresh(t)
				named := poAddNamedKeys(t, screen.bar())
				resting := base.View()

				panes := map[string]string{}
				for _, k := range space {
					r, s := fresh(t)
					before := poAddState(s)
					next, cmd := r.Update(poPhaseKeyMsg(k))
					r = next.(Root)
					// "Acted" includes leaving the screen: esc hands back a
					// SwitchTo command, which changes no field here and would
					// otherwise read as a named key that answers with nothing.
					if poAddState(s) != before || poCmdActs(cmd) {
						continue // it acted; this sweep is about the ones that do not
					}
					after := r.View()
					if after == resting {
						_, ok := poAddSilentKeys[c.phase][k]
						switch {
						case !named[k]:
							t.Errorf("%s: %q is not named by the bar and answers with nothing — "+
								"a byte-for-byte identical pane reads as a wedged program", c.name, k)
						case !ok:
							t.Errorf("%s: %q is named by the bar and answers with nothing from the "+
								"resting position, and is not in poAddSilentKeys", c.name, k)
						}
						continue
					}
					if other, clash := panes[after]; clash {
						t.Errorf("%s: %q and %q redraw the same pane — the second press would "+
							"leave the first one's answer standing", c.name, other, k)
					}
					panes[after] = k
				}
				if len(panes) == 0 {
					t.Errorf("%s: no key declined at all — the sweep is looking at nothing", c.name)
				}
			})
		}
	}
}

// TestPOAddLine_EveryDeclineNamesTheKeyItAnswers is the reason the sweep above
// can pass: a lead that names the key cannot be shared by two keys. Checking
// the property AND its mechanism is deliberate — the mechanism is what a future
// change would break first, and it breaks quietly.
func TestPOAddLine_EveryDeclineNamesTheKeyItAnswers(t *testing.T) {
	build := poAddHarness(t, 80, 24)
	r, s := build(t)
	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseConfirm {
		t.Fatalf("setup landed on phase %v", s.phase)
	}
	// In SEQUENCE, with no reset between presses: two keys answering with one
	// sentence would redraw the pane the first one left.
	before := r.View()
	for _, k := range []string{"x", "z", "ctrl+t"} {
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
		if !strings.Contains(r.View(), k+" does nothing here") {
			t.Errorf("the decline for %q does not name it:\n%s", k, r.View())
		}
		if r.View() == before {
			t.Errorf("%q redrew a byte-for-byte identical pane", k)
		}
		before = r.View()
	}
	// And the way out still works after all that.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	r = next.(Root)
	_ = cmd
	if s.phase != poAddPhaseIdentify {
		t.Errorf("esc left phase %v, want the identifier row", s.phase)
	}
}

// TestPOAddLine_TheFreezeIsAnAllowList: while the add is out, the payload has
// already gone, so nothing on the screen may change under it. Written as an
// allow-list rather than a list of frozen keys, because the other way round
// froze the keys somebody thought of and left every arm added later free by
// default — which is exactly how `d` got past the New PO screen's freeze.
func TestPOAddLine_TheFreezeIsAnAllowList(t *testing.T) {
	build := poAddHarness(t, 80, 24)
	r, s := build(t)
	r = key(t, r, poRuneKey("AF-99-12-ZP-LH-HEAVY"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	// Fire the add without the pump: it is genuinely still out.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !s.pending || s.phase != poAddPhaseAdding {
		t.Fatalf("the add did not go out: pending=%v phase=%v", s.pending, s.phase)
	}
	if s.qtyIn.Focused() || s.costIn.Focused() {
		t.Error("a caret is still blinking in a field whose contents have already gone")
	}

	before := poAddState(s)
	for _, k := range poKeySpace() {
		if k == "esc" {
			continue // the one key the bar names, and the only way out
		}
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
		if got := poAddState(s); got != before {
			t.Fatalf("%q changed the screen while the add was in flight:\n%s\n%s", k, before, got)
		}
	}
	// Esc leaves, and the frame said the line may still be added rather than
	// implying it was abandoned.
	next, cmd := r.Update(poPhaseKeyMsg("a"))
	r = next.(Root)
	_ = next
	if !strings.Contains(r.View(), "frozen until the add answers") {
		t.Errorf("a frozen key did not say so:\n%s", r.View())
	}
	if cmd == nil {
		t.Error("a frozen key answered with nothing at all")
	}
}
