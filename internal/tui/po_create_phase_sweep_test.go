package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// po_create_phase_sweep_test.go — the bar-honesty rule applied to EVERY phase of
// the New PO screen, over the whole key space, with both member sets DERIVED
// rather than restated.
//
// Three times on this project a key reached an operator's terminal doing
// nothing while the bar named it, and all three times the sweep that existed to
// stop it was reading a hand-maintained list that did not contain the key:
// poAllBarKeys held none of the list letters ('N new PO'), poPickerVocabulary
// held no tab (which committed the order's supplier), and the picker table held
// no poPhaseSupplierSwitch (where enter — the key that OPENED the confirm — was
// answered with nil). A roster that has to be edited in step with the code is
// the same omission waiting to happen again.
//
// So the two rosters here come from the code:
//   - PHASES from the poPhase iota, walked to the poPhaseCount sentinel. A
//     phase with no entry FAILS; a phase where genuinely no key acts is
//     recorded in poPhasesWithoutKeys, so "absent" and "empty" are different
//     states rather than the same silence.
//   - KEYS from the key SPACE, not a vocabulary: every printable ASCII rune
//     plus the named specials. There is no authoritative source to derive a
//     vocabulary from, so the answer is not to curate one — with the CHORD
//     caveat poKeySpace records.
//
// The LIST screens get the same treatment from Workspaces() in
// list_bar_honesty_test.go, and the screen's own state fields from
// reflect.TypeOf in po_create_picker_status_test.go.

// poKeySpace is every keystroke the sweep presses: printable ASCII, then the
// named keys a terminal sends that are not runes.
//
// The RUNE half is a true space — every printable ASCII character, so a letter
// bound tomorrow is pressed by this list today. The CHORD half is not, and this
// comment used to claim otherwise ("nothing is curated"), which is a documented
// claim the code does not honour and it cost real coverage: Ctrl+K and Ctrl+R
// were added to the receiving form — one closes a LINE short, one marks the
// whole ORDER received, both destructive and both behind a confirm — and
// neither appeared here, so every sweep that exists to prove "the bar names
// exactly the keys that work" pressed neither of them in EITHER direction. A
// key absent from the space is untested rather than passing, which is verbatim
// how `N` survived on the purchasing list (AGENTS.md). They are in the list
// now.
//
// The chord half stays hand-kept for one reason, recorded so the next author
// does not have to rediscover it: Ctrl+C and Ctrl+Q are a GLOBAL quit, claimed
// by Root.dispatch ahead of every screen and named by no screen's bar, so a
// chord list derived from bubbletea's own key types would report every screen
// in the program at once. Deriving it therefore needs a recorded home for the
// global keys first — the shape poPhasesWithoutKeys uses for the same kind of
// exception — and that is a change to every sweep in this package rather than
// to this function. Until then: a chord a screen binds must be added HERE, and
// the receiving sweeps' receiveNamedKeyMsg is what turns whatever name is added
// into the keystroke a terminal really sends.
func poKeySpace() []string {
	var keys []string
	for c := byte(0x20); c <= 0x7e; c++ {
		keys = append(keys, string(rune(c)))
	}
	return append(keys,
		"enter", "esc", "tab", "shift+tab",
		"up", "down", "left", "right", "home", "end", "pgup", "pgdown",
		"backspace", "delete",
		"ctrl+e", "ctrl+x", "ctrl+t", "ctrl+p", "ctrl+n",
		"ctrl+k", "ctrl+r",
	)
}

// poPhaseKeyMsg is poPickerKeyMsg. The two names are kept because the two
// sweeps read differently at their call sites, but there is ONE translation —
// the pair used to be two switches over overlapping key sets, and the picker
// one silently spelled every name it had not been taught as literal text.
func poPhaseKeyMsg(k string) tea.KeyMsg {
	return poPickerKeyMsg(k)
}

// poFieldKeys are the keys that belong to a focused text input rather than to
// the frame: on a phase where one owns the keyboard they act by editing the
// value, which is what the field is FOR, so the reverse direction ("a key the
// bar does not name must do nothing") cannot apply to them there. The forward
// direction still does — a key the bar names must still act.
var poFieldKeys = map[string]bool{
	"backspace": true, "delete": true,
	"left": true, "right": true, "home": true, "end": true,
}

func poIsPrintable(k string) bool {
	r := []rune(k)
	return len(r) == 1 && r[0] >= 0x20 && r[0] <= 0x7e
}

// poBarNamedKeys reads an action bar into the set of keystrokes it CLAIMS,
// through the ONE table both purchasing sweeps share (poBarKeyNames,
// po_view_jde_test.go).
//
// It replaces a function that had to parse PROSE. The New PO bar was a
// sentence, so a single-letter key could only be recognised at the start of a
// "key claim" segment and a bare `a` inside prose was the definite article —
// which is how `b`, bound on all three association pickers exactly as esc is
// and named by none of them, went unreported for a round. The bar is
// []actionBarItem now: the key and the label are separate fields and there is
// nothing left to parse.
func poBarNamedKeys(t *testing.T, bar []actionBarItem) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	for _, it := range bar {
		keys, ok := poBarKeyNames[it.Key]
		if !ok {
			t.Fatalf("bar entry %q is not in poBarKeyNames — add it so the rule covers it", it.Key)
		}
		for _, k := range keys {
			named[k] = true
		}
	}
	return named
}

// poBarText renders a bar as one string, for a failure message and for the
// handful of assertions that ask whether a bar advertises something at all.
func poBarText(items []actionBarItem) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.Key+"="+it.Label)
	}
	return strings.Join(parts, "  ")
}

// poPhaseCase is one phase of the New PO screen, driven into a real state
// through Root.Update against the httptest fake.
type poPhaseCase struct {
	phase poPhase
	name  string
	fake  func() *poPickFake
	// reach drives the screen from the source chooser into this phase.
	reach func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root
	// typing marks a phase where a text input owns the keyboard, so printable
	// runes and the field-editing keys act by design.
	typing bool
}

// poPhasesWithoutKeys records a phase where NO key acts, so that "no entry" and
// "nothing to check" cannot look the same. It is empty today, and a phase added
// here needs a reason.
var poPhasesWithoutKeys = map[poPhase]string{}

func poPhaseCases() []poPhaseCase {
	plain := func() *poPickFake {
		return &poPickFake{catalog: 4, assets: 3, reorder: 3, suppliers: 3,
			agreements: 1, workOrders: 2, committees: 1}
	}
	press := func(keys ...string) func(*testing.T, Root, *PurchaseOrderCreateScreen) Root {
		return func(t *testing.T, r Root, _ *PurchaseOrderCreateScreen) Root {
			for _, k := range keys {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			return r
		}
	}
	// stageOne puts exactly one CATALOG line in the cart, which is what makes
	// the supplier switch destructive and the review phase reachable.
	stageOne := func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
		r = key(t, r, poPhaseKeyMsg("i"))
		r = key(t, r, poPhaseKeyMsg("enter"))
		r = key(t, r, poPhaseKeyMsg("enter"))
		if len(s.lines) != 1 {
			t.Fatalf("setup staged %d line(s), want 1", len(s.lines))
		}
		return r
	}
	// stageTwo is what the review cart needs: with one line the cursor cannot
	// move and the bar's ↑↓ claim reads as dead for want of a row rather than
	// for want of a binding.
	stageTwo := func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
		r = stageOne(t, r, s)
		r = key(t, r, poPhaseKeyMsg("i"))
		r = key(t, r, poPhaseKeyMsg("down"))
		r = key(t, r, poPhaseKeyMsg("enter"))
		r = key(t, r, poPhaseKeyMsg("enter"))
		if len(s.lines) != 2 {
			t.Fatalf("setup staged %d line(s), want 2", len(s.lines))
		}
		return r
	}
	// submitting leaves a create POST genuinely in flight: r.Update without
	// pump, so the command is never run and nothing resolves it.
	submitting := func() func(*testing.T, Root, *PurchaseOrderCreateScreen) Root {
		return func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
			r = key(t, stageTwo(t, r, s), poPhaseKeyMsg("d"))
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			if !s.pending {
				t.Fatalf("setup did not leave a submit in flight")
			}
			return next.(Root)
		}
	}
	return []poPhaseCase{
		{poPhaseSource, "source chooser", plain, press(), false},
		{poPhaseSupplier, "supplier picker", plain, press("esc"), false},
		{poPhaseAgreement, "agreement picker", plain, press("g"), false},
		{poPhaseWorkOrder, "work-order picker", plain, press("w"), false},
		{poPhaseCommittee, "committee picker", plain, press("c"), false},
		{poPhaseReorderPick, "reorder picker", plain, press("r"), false},
		{poPhaseItemPick, "item picker", plain, press("i"), false},
		{poPhaseAssetPick, "asset picker", plain, press("a"), false},
		// Reached through the item picker rather than with `f`, so the form
		// arrives PREFILLED: on an empty freeform form enter declines with a
		// validation sentence, and a decline is not an act — the bar's "enter
		// adds to the cart" would read as dead for the wrong reason.
		{poPhaseLine, "line form", plain, press("i", "enter"), true},
		{poPhaseReview, "review cart", plain, func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
			return key(t, stageTwo(t, r, s), poPhaseKeyMsg("d"))
		}, true},
		// The same phase with the POST out: the bar drops `enter submit`, and
		// with it ctrl+e, ctrl+x and "type PO notes", for as long as those arms
		// decline. Driven with r.Update and no pump, so the request is
		// genuinely still in flight rather than resolved.
		//
		// It stays typing:true, and the two OTHER phases the freeze reaches (the
		// source chooser esc lands on, the supplier picker one `b` further) are
		// not entries here, for one reason: this sweep rebuilds the whole screen
		// for every key at every probe position, and a case whose reach stages
		// two lines costs about 210 seconds per pane height once printable runes
		// stop being skipped. Three more of those would put the package past the
		// 600-second `go test` deadline, which is a test suite nobody can run.
		// TestPOSubmit_TheFrozenPhasesNameExactlyTheKeysThatWork presses the same
		// whole key space against all three frozen states instead, in sequence
		// off ONE screen — a key that declines leaves the state it found, so the
		// next key meets the same frame, and only a key that ACTS costs a
		// rebuild. Same rule, same key space, seconds instead of minutes.
		{poPhaseReview, "review cart, submit in flight", plain, submitting(), true},
		{poPhaseSupplierSwitch, "supplier-switch confirm", plain,
			func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
				r = stageOne(t, r, s)
				r = key(t, r, poPhaseKeyMsg("esc"))
				r = key(t, r, poPhaseKeyMsg("down"))
				r = key(t, r, poPhaseKeyMsg("enter"))
				if s.phase != poPhaseSupplierSwitch {
					t.Fatalf("setup landed on phase %v, want the switch confirm", s.phase)
				}
				return r
			}, false},
	}
}

// TestPOCreate_EveryPhaseIsSwept walks the poPhase enum to its sentinel and
// fails on any phase the key sweep below has no entry for. This is the check
// that makes the sweep's coverage a property of the code rather than of who
// last remembered to extend a table.
func TestPOCreate_EveryPhaseIsSwept(t *testing.T) {
	covered := map[poPhase]bool{}
	for _, c := range poPhaseCases() {
		// More than one entry per phase is wanted, not an error: a phase has
		// STATES, and the review cart with a submit in flight names a different
		// set of keys from the review cart at rest.
		covered[c.phase] = true
	}
	for p := poPhase(0); p < poPhaseCount; p++ {
		if covered[p] {
			if why, ok := poPhasesWithoutKeys[p]; ok {
				t.Errorf("phase %v is both swept and recorded as keyless (%q)", p, why)
			}
			continue
		}
		if _, ok := poPhasesWithoutKeys[p]; !ok {
			t.Errorf("phase %v has no poPhaseCases entry and is not recorded in "+
				"poPhasesWithoutKeys — a key on it would be judged by nothing", p)
		}
	}
}

// TestPOCreate_EveryPhaseNamesExactlyTheKeysThatWork presses the whole key space
// against every phase, in both directions: a key the frame's bar names must
// change something, and a key it does not name must not.
func TestPOCreate_EveryPhaseNamesExactlyTheKeysThatWork(t *testing.T) {
	space := poKeySpace()
	for _, c := range poPhaseCases() {
		for _, height := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				fresh := func(probe []string) (Root, *PurchaseOrderCreateScreen) {
					r, s := poPickerAtSize(t, c.fake(), 80, height)
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
				_, screen := fresh(nil)
				bar := screen.bar()
				named := poBarNamedKeys(t, bar)
				poAssertFits(t, c.name, screen)

				for _, k := range space {
					if c.typing && (poIsPrintable(k) || poFieldKeys[k] || poFormNavAliases[k]) && !named[k] {
						continue
					}
					acted := false
					// Probed from more than one position: j does nothing at the
					// bottom of a list and k nothing at the top, so a key is
					// dead only if it does nothing from ANY of them. `up` is in
					// the set for the review cart, where j types into the notes
					// field and the cursor lands on the LAST line, so nothing
					// else would let `down` move.
					for i, probe := range [][]string{nil, {"down"}, {"up"}} {
						pr, ps := fresh(probe)
						before := poPickerState(ps)
						_, cmd := pr.Update(poPhaseKeyMsg(k))
						if poPickerState(ps) != before || poCmdActs(cmd) {
							acted = true
						}
						if i == 0 {
							// The NOTE a press leaves behind is a body line and
							// is held to the one-surface rule too. `d` on an
							// empty cart is the state whose note named four
							// keys, and nothing in the package had ever
							// rendered it.
							poAssertBodyNamesNoKey(t,
								fmt.Sprintf("%s after %q", c.name, k), ps, height)
						}
					}
					switch {
					case named[k] && !acted:
						t.Errorf("%s names %q but pressing it changes nothing (bar: %s)", c.name, k, poBarText(bar))
					case !named[k] && acted:
						t.Errorf("%s does not name %q, but pressing it acts (bar: %s)", c.name, k, poBarText(bar))
					}
				}
			})
		}
	}
}

// TestPOSupplierSwitch_EveryUnboundKeyAnswers is the confirm's half of "a
// keypress that changes nothing visible IS the reported bug".
//
// The frame binds exactly two keys and used to answer every other press with
// nil. enter is the one that matters: it is the key that OPENED the confirm, so
// a reflexive double-tap landed on a renderer that is a pure function of
// unchanged state — no cursor, no highlight, no focused input — and redrew the
// pane byte for byte. Deliberately still NOT bound to the destructive answer:
// ctrl+x is that key precisely so a double-tap cannot empty a half-built cart.
func TestPOSupplierSwitch_EveryUnboundKeyAnswers(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 4, suppliers: 3}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, poPhaseKeyMsg("i"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			r = key(t, r, poPhaseKeyMsg("esc"))
			r = key(t, r, poPhaseKeyMsg("down"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			if screen.phase != poPhaseSupplierSwitch {
				t.Fatalf("setup landed on phase %v, want the switch confirm", screen.phase)
			}

			before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			// In sequence, no reset between presses: two keys that answered
			// with one sentence would redraw the pane the first one left.
			for _, k := range []string{"enter", "down", "enter"} {
				lines, supplier := len(screen.lines), screen.supplierID
				r = key(t, r, poPhaseKeyMsg(k))
				if screen.phase != poPhaseSupplierSwitch {
					t.Fatalf("%q left the confirm (phase %v) — only ctrl+x and esc may", k, screen.phase)
				}
				if len(screen.lines) != lines || screen.supplierID != supplier {
					t.Fatalf("%q changed the cart or the supplier: %d line(s), supplier %d",
						k, len(screen.lines), screen.supplierID)
				}
				after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				if after == before {
					t.Errorf("%q redrew a byte-for-byte identical pane:\n%s", k, after)
				}
				before = after
				poAssertFits(t, "supplier-switch confirm after "+k, screen)
				// The decline names what the key DID; the two keys that ANSWER
				// are on the bar, which is drawn on every frame and cannot be
				// trimmed. Both are checked, because a decline on a destructive
				// confirm that left the operator without a way out would be the
				// worse half of this defect.
				poWantPaneLine(t, screen, "Ctrl-X=Drop & switch")
				poWantPaneLine(t, screen, "Esc=Keep cart")
			}

			// The way out still works after all that.
			r = key(t, r, poPhaseKeyMsg("esc"))
			_ = r
			if screen.phase != poPhaseSupplier || len(screen.lines) != 1 {
				t.Errorf("esc left phase %v with %d line(s), want the supplier picker with the cart intact",
					screen.phase, len(screen.lines))
			}
		})
	}
}

// TestPOReview_EnterWhileTheSubmitIsOutSaysSo: between pressing enter and the
// POST coming back, the review bar went on naming `enter submit` while the arm
// returned nil. Both halves are the rule — the bar drops a key it has gated,
// and the press still answers, in the BODY rather than a four-second flash the
// operator watching a slow gateway will have missed.
func TestPOReview_EnterWhileTheSubmitIsOutSaysSo(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 4}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, poPhaseKeyMsg("i"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			r = key(t, r, poPhaseKeyMsg("d"))
			if screen.phase != poPhaseReview {
				t.Fatalf("setup landed on phase %v, want review", screen.phase)
			}
			poWantPaneLine(t, screen, "Enter=Submit order")

			// r.Update without pump: the POST is genuinely still out.
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			r = next.(Root)
			if !screen.pending {
				t.Fatalf("the submit did not go out")
			}
			poRejectPaneLine(t, screen, "Enter=Submit order")
			// The working line names the WORK and the SUBJECT — "Submitting…"
			// on its own tells an operator watching a slow gateway nothing they
			// could act on — and it is the LAYER's status row, so a multi-line
			// OMS body cannot push the action bar off the bottom of the pane.
			poWantPaneLine(t, screen, poSubmitWords)

			before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			next, _ = r.Update(poPhaseKeyMsg("enter"))
			r = next.(Root)
			_ = r
			after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			if after == before {
				t.Errorf("enter over a submit already in flight redrew an identical pane:\n%s", after)
			}
			poWantPaneLine(t, screen, "enter is already in")
			poAssertFits(t, "review with a submit in flight", screen)
			// The field the operator is typing into is still on the pane.
			poWantPaneLine(t, screen, "PO notes")
		})
	}
}

// ---------------------------------------------------------------------------
// The submit's in-flight window: the cart is frozen until the POST answers
// ---------------------------------------------------------------------------

// poFrozenScreen stages two lines, sends the create POST and leaves it in
// flight, then walks `out` further keys — because the freeze has to hold on
// every phase the operator can reach while the request is out, not only the one
// they pressed enter on.
//
// The submit is fired with r.Update and NEVER pumped, so the command is never
// run and nothing resolves it: `pending` stays true for as long as the test
// wants it, which is what makes this window assertable at all.
func poFrozenScreen(t *testing.T, h int, out ...string) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	return poFrozenScreenWith(t, &poPickFake{catalog: 4, suppliers: 3,
		agreements: 1, workOrders: 2, committees: 1}, h, 2, out...)
}

// poFrozenScreenWith is the same window against a chosen supplier and cart
// size, because the fixture decides which SURFACE the frozen chooser draws. The
// standard one offers all three attribution rows, which is what pushes the cart
// into its collapsed sentence — so the cart-key hint is never rendered and an
// assertion that it drops the frozen chords passes without the hint existing.
// A supplier with no agreement, work order or committee leaves the rows listed
// and the hint on the pane, which is the state that assertion is about.
func poFrozenScreenWith(t *testing.T, fake *poPickFake, h, lines int, out ...string) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	r, screen := poPickerAtSize(t, fake, 80, h)
	stage := []string{"i", "enter", "enter"}
	if lines > 1 {
		stage = append(stage, "i", "down", "enter", "enter")
	}
	for _, k := range append(stage, "d") {
		r = key(t, r, poPhaseKeyMsg(k))
	}
	if screen.phase != poPhaseReview || len(screen.lines) != lines {
		t.Fatalf("setup landed on phase %v with %d line(s)", screen.phase, len(screen.lines))
	}
	next, _ := r.Update(poPhaseKeyMsg("enter"))
	r = next.(Root)
	if !screen.pending {
		t.Fatal("the submit did not go out")
	}
	for _, k := range out {
		r = key(t, r, poPhaseKeyMsg(k))
	}
	if !screen.pending {
		t.Fatalf("the submit resolved while walking to %v", screen.phase)
	}
	return r, screen
}

// poFrozenState is one phase the operator can be on while the POST is out.
//
// There are three, and that is not a choice: esc is deliberately NOT gated — a
// frame with no way out while a slow gateway thinks is the worse defect — so
// review's esc lands on the source chooser and its `b` on the supplier picker,
// both with the request still in flight. Freezing only the review arms would
// have left the identical defect one phase over, which is how this screen has
// been found wanting every round.
type poFrozenState struct {
	name  string
	phase poPhase
	out   []string
}

func poFrozenStates() []poFrozenState {
	return []poFrozenState{
		{"review cart", poPhaseReview, nil},
		{"source chooser", poPhaseSource, []string{"esc"}},
		{"supplier picker", poPhaseSupplier, []string{"esc", "esc"}},
	}
}

// poPhasesUnreachableWhilePending is every OTHER phase, with the reason the
// freeze cannot be standing on it. Each one is unreachable because the key that
// opens it declines while the POST is out, so this table is a claim about the
// gates — and the sweep below checks it by walking, not by trusting: any phase
// a key actually reaches mid-flight must be in poFrozenStates.
var poPhasesUnreachableWhilePending = map[poPhase]string{
	poPhaseSupplierSwitch: "only commitSupplier opens it, and enter declines on the frozen supplier picker",
	poPhaseAgreement:      "g declines on the frozen source chooser",
	poPhaseWorkOrder:      "w declines on the frozen source chooser",
	poPhaseCommittee:      "c declines on the frozen source chooser",
	poPhaseReorderPick:    "r declines on the frozen source chooser",
	poPhaseItemPick:       "i declines on the frozen source chooser",
	poPhaseAssetPick:      "a declines on the frozen source chooser",
	poPhaseLine:           "f and ctrl+e decline on the frozen source chooser, ctrl+e on review",
}

// TestPOSubmit_EveryPhaseIsClassifiedAgainstTheFreeze walks the poPhase enum to
// its sentinel: a phase is either swept as frozen or recorded as unreachable
// while a submit is out, with a reason. A phase added tomorrow fails by name
// until somebody decides which it is — the freeze was scoped by enumeration
// once already, and `d` fell outside the list somebody wrote.
func TestPOSubmit_EveryPhaseIsClassifiedAgainstTheFreeze(t *testing.T) {
	frozen := map[poPhase]bool{}
	for _, st := range poFrozenStates() {
		frozen[st.phase] = true
	}
	for p := poPhase(0); p < poPhaseCount; p++ {
		why, unreachable := poPhasesUnreachableWhilePending[p]
		switch {
		case frozen[p] && unreachable:
			t.Errorf("phase %v is both swept as frozen and recorded unreachable (%q)", p, why)
		case !frozen[p] && !unreachable:
			t.Errorf("phase %v is neither swept as frozen nor recorded unreachable while a "+
				"submit is out — a key on it would be judged by nothing", p)
		}
	}
}

// TestPOSubmit_TheFrozenPhasesNameExactlyTheKeysThatWork presses the WHOLE key
// space against each frozen phase, in both directions of the bar-honesty rule,
// and checks the payload after every single press.
//
// It presses in SEQUENCE off one screen rather than rebuilding per key, which
// is what makes the whole key space affordable here: a key that declines leaves
// the state it found (a decline writes only poStateDeclined fields), so the next
// key meets the same frame, and only a key that ACTS costs a rebuild. A named
// key that looks dead is retried from the probe positions the phase sweep uses,
// because ↑ does nothing with the highlight already at the top.
func TestPOSubmit_TheFrozenPhasesNameExactlyTheKeysThatWork(t *testing.T) {
	frozen := map[poPhase]bool{}
	for _, st := range poFrozenStates() {
		frozen[st.phase] = true
	}
	for _, st := range poFrozenStates() {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", st.name, h), func(t *testing.T) {
				build := func(probe ...string) (Root, *PurchaseOrderCreateScreen) {
					r, s := poFrozenScreen(t, h, append(append([]string{}, st.out...), probe...)...)
					if s.phase != st.phase {
						t.Fatalf("the walk landed on phase %v, want %v", s.phase, st.phase)
					}
					return r, s
				}
				r, screen := build()
				bar := screen.bar()
				named := poBarNamedKeys(t, bar)
				// Rule C, derived rather than reasoned about per frame: a
				// frozen frame must keep at least one key that goes BACK into
				// the order. Freezing the supplier picker's enter took the last
				// one there — that phase binds no b and no d, and its esc
				// leaves the screen — so the operator who wandered in mid-flight
				// could only wait or throw the POST's answer away.
				wayBack := false
				poAssertFits(t, st.name+" with a submit in flight", screen)
				// The payload finalize() copied. Nothing pressed below may move
				// any of it — that is the freeze, and it is checked on every key
				// rather than on the handful this round happened to name.
				lines, notes, supplier := len(screen.lines), screen.poNotes.Value(), screen.supplierID

				for _, k := range poKeySpace() {
					before := poPickerState(screen)
					_, cmd := r.Update(poPhaseKeyMsg(k))
					acted := poPickerState(screen) != before || poCmdActs(cmd)
					if len(screen.lines) != lines ||
						screen.poNotes.Value() != notes ||
						screen.supplierID != supplier {
						t.Fatalf("%q changed the payload while the submit was out: %d line(s), notes %q, supplier %d",
							k, len(screen.lines), screen.poNotes.Value(), screen.supplierID)
					}
					// No caret, ever, while the request is out: a focused field
					// is the screen asking for input the pending arm refuses.
					if in := poFocusedInput(screen); in != "" {
						t.Fatalf("%q left %s focused while the submit was out", k, in)
					}
					// Where a key navigated, it may only have navigated to
					// another frozen frame. This is the derivation: a phase
					// reachable mid-flight that nobody classified fails here
					// rather than quietly running unfrozen.
					if screen.phase != st.phase {
						if !frozen[screen.phase] {
							t.Fatalf("%q reached phase %v while the submit was out, and it is not "+
								"swept as frozen", k, screen.phase)
						}
						wayBack = true
					}
					if !named[k] && acted {
						t.Errorf("%s with a submit in flight does not name %q, but pressing it acts (bar: %s)",
							st.name, k, poBarText(bar))
					}
					if named[k] && !acted {
						for _, probe := range []string{"up", "down"} {
							pr, ps := build(probe)
							pb := poPickerState(ps)
							_, pc := pr.Update(poPhaseKeyMsg(k))
							if poPickerState(ps) != pb || poCmdActs(pc) {
								acted = true
								break
							}
						}
						if !acted {
							t.Errorf("%s with a submit in flight names %q but pressing it changes nothing (bar: %s)",
								st.name, k, poBarText(bar))
						}
					}
					if acted {
						// The frame has moved on, so the next key would be
						// judged against a state this one left rather than the
						// one the bar was read from.
						r, screen = build()
					}
				}
				if !wayBack {
					t.Errorf("no key on the frozen %s returns to another frozen frame — every route "+
						"out of it abandons the screen and the POST's answer with it (bar: %s)",
						st.name, poBarText(bar))
				}
			})
		}
	}
}

// TestPOSubmit_TheCartIsFrozenUntilItAnswers is the same window as a journey:
// the keys an operator actually reaches for, pressed in sequence with no state
// reset between them, with what the pane says checked after each one.
//
// finalize() copies the cart and the notes into the request, so from the moment
// the POST leaves nothing pressed on this screen can reach the order being
// created: a removal, an edit or a typed note is work the 201's navigation
// throws away, and until it lands the pane is describing a cart that is not the
// one going in. Removing the LAST line was the worst of it — removeLineAt drops
// the phase back to the source chooser, so the operator was told to add a line
// while their two-line order was already being created.
func TestPOSubmit_TheCartIsFrozenUntilItAnswers(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poFrozenScreen(t, h)

			// The bar drops every claim the freeze has made false. It may not go
			// on inviting an edit it is about to discard.
			for _, gone := range []string{"type PO notes", "ctrl+e edit it", "ctrl+x remove it", "enter submit"} {
				poRejectPaneLine(t, screen, gone)
			}

			pane := func() string { return strings.Join(poPaneLinesAt(t, screen, h), "\n") }
			before := pane()
			step := func(k, say string) {
				t.Helper()
				lines, notes := len(screen.lines), screen.poNotes.Value()
				phase := screen.phase
				next, _ := r.Update(poPhaseKeyMsg(k))
				r = next.(Root)
				if len(screen.lines) != lines || screen.poNotes.Value() != notes {
					t.Fatalf("%q changed the frozen cart: %d line(s), notes %q",
						k, len(screen.lines), screen.poNotes.Value())
				}
				if screen.phase != phase {
					t.Fatalf("%q left phase %v for %v while the submit was out", k, phase, screen.phase)
				}
				if after := pane(); after == before {
					t.Errorf("%q redrew a byte-for-byte identical pane:\n%s", k, after)
				} else {
					before = after
				}
				poWantPaneLine(t, screen, say)
				poAssertFits(t, "submit in flight after "+k, screen)
			}

			// Review: the two chords and the notes field itself.
			step("ctrl+x", "ctrl+x removes nothing")
			step("ctrl+e", "ctrl+e edits nothing")
			step("q", "q is not in the notes")
			// Reading the cart is not editing it, so the highlight still moves —
			// which is why the bar goes on naming ↑↓.
			cursor := screen.reviewCursor
			up, _ := r.Update(poPhaseKeyMsg("up"))
			r = up.(Root)
			if screen.reviewCursor == cursor {
				t.Errorf("↑ did not move the highlight while the submit was out")
			}

			// The source chooser, reached the way an operator reaches it.
			r = key(t, r, poPhaseKeyMsg("esc"))
			if screen.phase != poPhaseSource || !screen.pending {
				t.Fatalf("esc landed on phase %v with pending=%v", screen.phase, screen.pending)
			}
			// The lead belongs to the frame that declined the key. Carried
			// here it would name a key this frame does not bind — the review
			// notes arm answers `q`, the chooser has no notes at all.
			poRejectPaneLine(t, screen, "is not in the notes")
			if in := poFocusedInput(screen); in != "" {
				t.Fatalf("esc onto the frozen chooser left %s focused", in)
			}
			for _, gone := range []string{"r reorder queue", "i inventory items", "f freeform",
				"x remove", "ctrl+e edit", "g agreement", "w work order", "c committee"} {
				poRejectPaneLine(t, screen, gone)
			}
			before = pane()
			step("i", "i adds nothing")
			step("f", "f adds nothing")
			step("ctrl+x", "ctrl+x removes nothing")
			step("ctrl+e", "ctrl+e edits nothing")
			step("g", "g changes nothing")

			// `d` is on the frozen allow-list — reviewing the cart reads it —
			// so it must navigate WITHOUT focusing the notes the submit
			// blurred. It was the one arm the enumerated freeze missed: it sat
			// outside the frozen key list, the bar named it, and it put the
			// caret back into a field whose contents had already gone.
			r = key(t, r, poPhaseKeyMsg("d"))
			if screen.phase != poPhaseReview || !screen.pending {
				t.Fatalf("d landed on phase %v with pending=%v", screen.phase, screen.pending)
			}
			if in := poFocusedInput(screen); in != "" {
				t.Fatalf("d re-focused %s while the submit was out", in)
			}
			poRejectPaneLine(t, screen, "changes nothing")
			poRejectPaneLine(t, screen, "type PO notes")
			poAssertFits(t, "review reached with d while the submit was out", screen)
			// Typing there is still refused, so the blurred field is the truth.
			notes := screen.poNotes.Value()
			next, _ := r.Update(poPhaseKeyMsg("z"))
			r = next.(Root)
			if screen.poNotes.Value() != notes {
				t.Errorf("a rune reached the notes after d: %q", screen.poNotes.Value())
			}

			// The supplier picker, one step further out, where a commit would
			// re-target a request that already names the old supplier.
			r = key(t, r, poPhaseKeyMsg("esc"))
			if screen.phase != poPhaseSource {
				t.Fatalf("esc landed on phase %v, want the source chooser", screen.phase)
			}
			r = key(t, r, poPhaseKeyMsg("esc"))
			if screen.phase != poPhaseSupplier {
				t.Fatalf("esc landed on phase %v, want the supplier picker", screen.phase)
			}
			r = key(t, r, poPhaseKeyMsg("down")) // highlight a DIFFERENT supplier
			poRejectPaneLine(t, screen, "enter commits")
			supplier := screen.supplierID
			before = pane()
			step("enter", "enter commits nothing")
			if screen.supplierID != supplier {
				t.Errorf("enter committed supplier %d while the submit was out", screen.supplierID)
			}
		})
	}
}

// TestPOSubmit_AFailedSubmitHandsTheCartBack is the other half: the freeze lasts
// exactly as long as the request. A screen that stayed frozen after a 502 would
// have taken the operator's order hostage to a gateway.
func TestPOSubmit_AFailedSubmitHandsTheCartBack(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 4, failCreate: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			for _, k := range []string{"i", "enter", "enter", "i", "down", "enter", "enter", "d"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			if screen.phase != poPhaseReview || len(screen.lines) != 2 {
				t.Fatalf("setup landed on phase %v with %d line(s)", screen.phase, len(screen.lines))
			}

			r = key(t, r, poPhaseKeyMsg("enter")) // pumped: the 502 comes back
			if screen.pending {
				t.Fatal("the submit is still pending after its reply")
			}
			poWantPaneLine(t, screen, "submitting this purchase order failed")
			poWantPaneLine(t, screen, "Enter=Submit order")
			poAssertFits(t, "review after a failed submit", screen)

			// The notes take input again — the field was blurred for the flight.
			r = poType(t, r, "z")
			if screen.poNotes.Value() != "z" {
				t.Errorf("the notes hold %q after the failure, want the typed rune", screen.poNotes.Value())
			}
			next, _ := r.Update(poPhaseKeyMsg("ctrl+x"))
			r = next.(Root)
			_ = r
			if len(screen.lines) != 1 {
				t.Errorf("ctrl+x left %d line(s) after the failure, want the removal to work again",
					len(screen.lines))
			}
		})
	}
}

// poPaneCount is how many CLIPPED lines carry want — the count matters because
// the source chooser states its cart keys twice, in the action bar and in the
// hint above the rows, and a claim dropped from one surface but not the other
// is the defect this pair of tests exists for.
func poPaneCount(t *testing.T, s *PurchaseOrderCreateScreen, h int, want string) int {
	t.Helper()
	n := 0
	for _, line := range poPaneLinesAt(t, s, h) {
		if strings.Contains(line, want) {
			n++
		}
	}
	return n
}

// TestPOSubmit_TheFrozenChooserOffersTheCartChordsOnOneSurfaceOnly.
//
// This used to be a test about TWO surfaces. The source chooser stated its cart
// keys in the prose action bar at the top of the pane AND in a hint drawn three
// rows above the rows themselves, and when the freeze landed the bar dropped
// ctrl+e and x while the hint did not — one pane advertising and refusing the
// same two keys.
//
// The conversion removed the second surface rather than re-synchronising it, so
// the check is now the stronger one: exactly ONE surface names a key, the
// action bar, and no body line may name either chord in either state. The
// before-the-submit half is what makes the after half non-vacuous — a bar that
// never named them would pass the absence check on nothing.
func TestPOSubmit_TheFrozenChooserOffersTheCartChordsOnOneSurfaceOnly(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 4, suppliers: 3}
			r, screen := poPickerAtSize(t, fake, 80, h)
			// TWO lines, not one: UP/DN is named only where there is another row
			// to move onto, so a one-line cart would make the "UP/DN survives
			// the freeze" half of this pass or fail for the wrong reason.
			for _, k := range []string{"i", "enter", "enter", "i", "down", "enter", "enter"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			if screen.phase != poPhaseSource || len(screen.lines) != 2 {
				t.Fatalf("setup landed on phase %v with %d line(s)", screen.phase, len(screen.lines))
			}
			// At rest the bar names both chords, and the highlighted line is on
			// the pane for them to act on.
			bar := poBarText(screen.bar())
			for _, want := range []string{"Ctrl-E=Edit line", "Ctrl-X=Remove line"} {
				if !strings.Contains(bar, want) {
					t.Fatalf("the resting chooser bar does not name %q (%s) — the freeze "+
						"assertions below would pass on nothing", want, bar)
				}
			}
			poWantPaneLine(t, screen, "Widget")
			// …and NO body line names them, which is the second surface staying
			// gone.
			for _, chord := range []string{"ctrl+e", "ctrl+x", "Ctrl-E", "Ctrl-X"} {
				if n := poPaneBodyCount(t, screen, h, chord); n != 0 {
					t.Errorf("a body line on the resting chooser names %q %d time(s) — the bar is "+
						"the one surface that names a key:\n%s",
						chord, n, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
			}

			r = key(t, r, poPhaseKeyMsg("d"))
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			r = key(t, next.(Root), poPhaseKeyMsg("esc"))
			if screen.phase != poPhaseSource || !screen.pending {
				t.Fatalf("esc landed on phase %v with pending=%v", screen.phase, screen.pending)
			}
			// The cart is still LISTED — the window anchors on the highlighted
			// row, so it is drawn whatever the pane height — which is what makes
			// "the bar dropped the chords" a claim about the freeze rather than
			// about a collapsed block.
			poWantPaneLine(t, screen, "Widget")
			frozen := poBarText(screen.bar())
			for _, gone := range []string{"Ctrl-E=", "Ctrl-X="} {
				if strings.Contains(frozen, gone) {
					t.Errorf("the frozen chooser bar still names %q: %s", gone, frozen)
				}
			}
			// UP/DN survives: reading the cart is not changing it.
			if !strings.Contains(frozen, "UP/DN=") {
				t.Errorf("the frozen chooser dropped UP/DN, which still moves the highlight: %s", frozen)
			}
			poAssertFits(t, "frozen source chooser with a listed cart", screen)
		})
	}
}

// TestPOSubmit_TheFrozenChooserBarDropsEveryKeyItHasGated.
//
// This used to be a test about the four line-source ROWS, which stayed on the
// pane while the submit was out and each said "— off while submitting" in
// words, because colour alone is a claim nothing outside a real TTY can check.
//
// Those rows are gone: they spelled the same four keys the action bar spells,
// six rows of a twelve-row budget spent on a second copy of the bar. So the
// claim moves to the one surface that makes it, and the check gets stronger in
// the process — the bar must drop EVERY key the freeze has gated, not just the
// four that had rows, and each of those keys must still ANSWER when pressed.
func TestPOSubmit_TheFrozenChooserBarDropsEveryKeyItHasGated(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			// 36 cells, past BOTH budgets (30 keyed, 32 unkeyed at 80
			// columns), so the clip really bites and the two states really do
			// render different lengths. A short name asserts nothing here.
			fake := &poPickFake{catalog: 4, suppliers: 3,
				agreements: 1, workOrders: 2, committees: 1,
				agreementName: "Annual 2026 Structural Steel Contract"}
			r, screen := poPickerAtSize(t, fake, 80, h)
			for _, k := range []string{"i", "enter", "enter"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			// Every one of these is named at rest, which is what stops the
			// absence check below passing over a key the bar never offered.
			gated := []string{"r", "i", "a", "f", "g", "w", "c", "ctrl+e", "ctrl+x"}
			named := poBarNamedKeys(t, screen.bar())
			for _, k := range gated {
				if !named[k] {
					t.Fatalf("the resting chooser does not name %q, so the freeze check "+
						"would assert nothing about it (bar: %s)", k, poBarText(screen.bar()))
				}
			}

			r = key(t, r, poPhaseKeyMsg("d"))
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			r = key(t, next.(Root), poPhaseKeyMsg("esc"))
			if screen.phase != poPhaseSource || !screen.pending {
				t.Fatalf("esc landed on phase %v with pending=%v", screen.phase, screen.pending)
			}
			frozenBar := screen.bar()
			frozen := poBarNamedKeys(t, frozenBar)
			for _, k := range gated {
				if frozen[k] {
					t.Errorf("the frozen chooser still names %q, which declines: %s",
						k, poBarText(frozenBar))
				}
			}
			// …and each of them answers rather than going silent.
			for _, k := range gated {
				before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				nxt, _ := r.Update(poPhaseKeyMsg(k))
				r = nxt.(Root)
				if screen.phase != poPhaseSource || !screen.pending {
					t.Fatalf("%q left the frozen chooser: phase %v pending %v",
						k, screen.phase, screen.pending)
				}
				if after := strings.Join(poPaneLinesAt(t, screen, h), "\n"); after == before {
					t.Errorf("%q on the frozen chooser redrew a byte-for-byte identical pane:\n%s",
						k, after)
				}
			}
			poAssertFits(t, "frozen source chooser", screen)
		})
	}
}

// TestPOSubmit_TheFrozenSupplierPickerHasAWayBack is Rule C on the frame the
// freeze had cornered. This picker binds no `b` and no `d`, and its `esc`
// leaves the SCREEN — so declining enter outright left the operator who wandered
// here with the wait or with throwing the outcome away, on exactly the slow
// gateway the freeze is for. Enter on the row the order already carries commits
// nothing, so it is navigation and stays live; enter on any OTHER row would
// re-target the request and stays frozen.
func TestPOSubmit_TheFrozenSupplierPickerHasAWayBack(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poFrozenScreen(t, h, "esc", "esc")
			if screen.phase != poPhaseSupplier {
				t.Fatalf("the walk landed on phase %v, want the supplier picker", screen.phase)
			}
			supplier, lines := screen.supplierID, len(screen.lines)
			poWantPaneLine(t, screen, "Enter=Back to sources")

			r = key(t, r, poPhaseKeyMsg("enter"))
			if screen.phase != poPhaseSource {
				t.Fatalf("enter on the committed supplier landed on phase %v, want the source chooser",
					screen.phase)
			}
			if !screen.pending || screen.supplierID != supplier || len(screen.lines) != lines {
				t.Fatalf("enter changed the order: pending=%v supplier=%d line(s)=%d",
					screen.pending, screen.supplierID, len(screen.lines))
			}

			// A DIFFERENT row is still frozen, and the bar stops offering it.
			r = key(t, r, poPhaseKeyMsg("esc"))
			r = key(t, r, poPhaseKeyMsg("down"))
			poRejectPaneLine(t, screen, "Enter=Back to sources")
			before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			_ = next
			if screen.phase != poPhaseSupplier || screen.supplierID != supplier {
				t.Fatalf("enter on another supplier moved the order: phase %v supplier %d",
					screen.phase, screen.supplierID)
			}
			if after := strings.Join(poPaneLinesAt(t, screen, h), "\n"); after == before {
				t.Errorf("enter on another supplier redrew an identical pane:\n%s", after)
			}
			poWantPaneLine(t, screen, "enter commits nothing")
			poAssertFits(t, "frozen supplier picker", screen)
		})
	}
}

// poPaneBodyCount is poPaneCount over the pane ABOVE the action bar's rule —
// the lines the screen composed, rather than the bar it was handed.
//
// The distinction is the whole point of the tests that use it: after the
// conversion exactly one surface on this screen names a key, and that surface
// is the bar. Counting the whole pane would credit the bar's own text to the
// body and the check would pass over the defect it exists to report.
func poPaneBodyCount(t *testing.T, s *PurchaseOrderCreateScreen, h int, want string) int {
	t.Helper()
	n := 0
	for _, line := range poPaneLinesAt(t, s, h) {
		if strings.HasPrefix(strings.TrimSpace(line), "----") {
			break
		}
		if strings.Contains(line, want) {
			n++
		}
	}
	return n
}

// poAttributionRow is the pinned header row for one optional association as the
// clipped pane draws it, and whether it carries the KEY COLUMN in front of the
// label.
//
// The letter is read off the FRONT of the row rather than looked for anywhere
// on it, because the values themselves are OMS-supplied prose: an agreement
// named "Grade 8 fasteners" carries a g, a w and a c between them and a
// substring search would report a key column on every row of every frame.
func poAttributionRow(t *testing.T, s *PurchaseOrderCreateScreen, h int, label string) (row string, keyed bool, found bool) {
	t.Helper()
	key, ok := poAttributionKeys[label]
	if !ok {
		t.Fatalf("no attribution key is recorded for %q", label)
	}
	for _, line := range poPaneLinesAt(t, s, h) {
		if !strings.Contains(line, label+jdeLeader) {
			continue
		}
		return line, strings.HasPrefix(strings.TrimLeft(line, " "), key+" "), true
	}
	return "", false, false
}

// poAttributionKeys is the letter each optional association row is keyed with,
// which is also the letter the bar spells for it. Written out rather than
// derived from the label, because the agreement row is keyed `g` and no rule
// takes that from the word "Agreement".
var poAttributionKeys = map[string]string{
	poRowAgreement: "g", poRowWorkOrder: "w", poRowCommittee: "c",
}

// TestPOSubmit_TheFrozenChooserHeaderDropsTheKeyColumn is the PANE half of the
// claim TestPOSubmit_TheFrozenChooserBarDropsEveryKeyItHasGated makes about the
// bar, and it exists because that bar check alone let the defect through.
//
// Its predecessor (TestPOSubmit_TheFrozenChooserRowsSayTheyAreOff) asserted the
// pane by rejecting the line-source rows outright; those rows are gone, and
// nothing replaced the pane half. So the frozen chooser went on drawing the
// highlighted letters g / w / c beside their values while barItems had dropped
// all three and updateSourcePhase answered them with pendingDecline — one pane
// advertising and refusing the same key, which is exactly what deleting the
// line-source rows was for.
//
// BOTH directions, because an absence check on a pane that never drew the
// letters passes over everything: at rest the key column is there, while the
// submit is out it is not, and the VALUES stay in both — they are part of the
// order being created and only the affordance is false.
//
// The VALUE half asserted byte-equality at first, and passed for a reason that
// had nothing to do with the property: the two rows are clipped to DIFFERENT
// budgets (poFieldValueRoom reserves two cells for the key column, so 30 keyed
// against 32 unkeyed at 80 columns), and the fake's `Annual 1` / `Shop 1` names
// were nowhere near either. That is rule 9 in its subtler form — not a check
// that cannot fail, but one whose FIXTURE NEVER REACHES THE BOUND IT ASSERTS
// ABOUT — and it is the same family as the picker fixtures that drew seven-cell
// names. So the fixture now carries a name at the length OMS really sends, and
// the claim is the one the code actually makes: the same value, possibly LESS
// ABBREVIATED once the letter is gone, because those two freed cells belong to
// the operator's data.
func TestPOSubmit_TheFrozenChooserHeaderDropsTheKeyColumn(t *testing.T) {
	// The three optional lookups all answer, so all three rows are offered.
	// Every source-chooser fixture used to leave them at zero, which is how a
	// claim about g / w / c could be asserted against a frame that had none.
	rows := []string{poRowAgreement, poRowWorkOrder, poRowCommittee}

	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			// 36 cells, past BOTH budgets (30 keyed, 32 unkeyed at 80
			// columns), so the clip really bites and the two states really do
			// render different lengths. A short name asserts nothing here.
			fake := &poPickFake{catalog: 4, suppliers: 3,
				agreements: 1, workOrders: 2, committees: 1,
				agreementName: "Annual 2026 Structural Steel Contract"}
			r, screen := poPickerAtSize(t, fake, 80, h)
			// Commit an agreement and a work order first, so the "the values
			// stay" half is a claim about real OMS-supplied prose rather than
			// two "(none)"s comparing equal.
			for _, k := range []string{"g", "down", "enter", "w", "down", "enter"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			if screen.agreementID == nil || screen.workOrderID == "" {
				t.Fatalf("setup committed agreement=%v work order=%q",
					screen.agreementID, screen.workOrderID)
			}
			for _, k := range []string{"i", "enter", "enter"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			if screen.phase != poPhaseSource || len(screen.lines) != 1 {
				t.Fatalf("setup landed on phase %v with %d line(s)", screen.phase, len(screen.lines))
			}

			resting := map[string]string{}
			for _, label := range rows {
				line, keyed, found := poAttributionRow(t, screen, h, label)
				if !found {
					t.Fatalf("the resting chooser draws no %q row, so the freeze check below "+
						"would assert nothing:\n%s", label,
						strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
				if !keyed {
					t.Fatalf("the resting chooser draws %q without its key column, so the freeze "+
						"check below would pass on a letter that was never there:\n\t%q", label, line)
				}
				resting[label] = strings.TrimSpace(strings.SplitN(line, jdeLeader, 2)[1])
			}

			r = key(t, r, poPhaseKeyMsg("d"))
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			r = key(t, next.(Root), poPhaseKeyMsg("esc"))
			if screen.phase != poPhaseSource || !screen.pending {
				t.Fatalf("esc landed on phase %v with pending=%v", screen.phase, screen.pending)
			}
			named := poBarNamedKeys(t, screen.bar())
			for _, k := range []string{"g", "w", "c"} {
				if named[k] {
					t.Fatalf("the frozen chooser bar still names %q, so the pane is not the "+
						"surface under test: %s", k, poBarText(screen.bar()))
				}
			}
			for _, label := range rows {
				line, keyed, found := poAttributionRow(t, screen, h, label)
				if !found {
					t.Errorf("the frozen chooser dropped the %q row altogether — the value is part "+
						"of the order being created and only the affordance is false:\n%s", label,
						strings.Join(poPaneLinesAt(t, screen, h), "\n"))
					continue
				}
				if keyed {
					t.Errorf("the frozen chooser still draws %q with its key column while the bar "+
						"has dropped that key and the arm declines it:\n\t%q", label, line)
				}
				got := strings.TrimSpace(strings.SplitN(line, jdeLeader, 2)[1])
				if was := resting[label]; !poSameValueNoLessShown(was, got) {
					t.Errorf("the frozen chooser changed the %q value from %q to %q — the freeze "+
						"drops the affordance, not the fact", label, was, got)
				}
			}
			poAssertFits(t, "frozen source chooser header", screen)
		})
	}
}

// TestPOLineForm_FieldNavigationWrapsAtBothEnds holds the one place on this
// screen where the cursor WRAPS rather than clamping.
//
// The conversion routed tab / shift+tab and UP/DN through the shared
// setCursorRow, which clamps (jdeClampPick) because a LIST must not run off the
// bottom and reappear at the row that clears a field. A four-field form is the
// other case: clamped, Down on the expected-date row blurred and re-focused the
// same field — no state change, no note, and the bar naming UP/DN at that
// moment, which is rule 1 exactly. po_add_line's price rows wrap for the same
// reason, so clamping here also made the two purchasing field forms disagree.
func TestPOLineForm_FieldNavigationWrapsAtBothEnds(t *testing.T) {
	// An item-supplier line, because it is the only shape that offers all four
	// fields — the wrap on a three-field form would step over the row the
	// defect was reported on.
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poPickerAtSize(t, &poPickFake{catalog: 2, suppliers: 1}, 80, h)
			for _, k := range []string{"i", "enter"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			if screen.phase != poPhaseLine || len(screen.lineFields()) != 4 {
				t.Fatalf("setup landed on phase %v with %d field(s), want the four-field line form",
					screen.phase, len(screen.lineFields()))
			}

			for _, tc := range []struct {
				name       string
				from, want int
				press      string
			}{
				{"down off the last field", poLineFieldDate, poLineFieldDesc, "down"},
				{"tab off the last field", poLineFieldDate, poLineFieldDesc, "tab"},
				{"up off the first field", poLineFieldDesc, poLineFieldDate, "up"},
				{"shift+tab off the first field", poLineFieldDesc, poLineFieldDate, "shift+tab"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					// BOUNDED, and it fails rather than spinning: with the
					// clamp back in place tab cannot leave the last field, so
					// an unbounded walk to a starting row would hang the suite
					// instead of reporting the defect it is here for.
					for i := 0; screen.lineFocused != tc.from; i++ {
						if i >= len(screen.lineFields()) {
							t.Fatalf("tab could not reach field %d from field %d in %d press(es) — "+
								"the form's navigation does not cover its own fields",
								tc.from, screen.lineFocused, i)
						}
						r = key(t, r, poPhaseKeyMsg("tab"))
					}
					before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
					r = key(t, r, poPhaseKeyMsg(tc.press))
					if got := screen.lineFocused; got != tc.want {
						t.Errorf("%q from field %d left focus on field %d, want %d — a clamped "+
							"cursor here is a named key that does nothing",
							tc.press, tc.from, got, tc.want)
					}
					if !screen.lineInputs[screen.lineFocused].Focused() {
						t.Errorf("%q left no field focused", tc.press)
					}
					for i := range screen.lineInputs {
						if i != screen.lineFocused && screen.lineInputs[i].Focused() {
							t.Errorf("%q left a caret in field %d as well as %d",
								tc.press, i, screen.lineFocused)
						}
					}
					if after := strings.Join(poPaneLinesAt(t, screen, h), "\n"); after == before {
						t.Errorf("%q from field %d redrew a byte-for-byte identical pane:\n%s",
							tc.press, tc.from, after)
					}
				})
			}
			poAssertFits(t, "line form after wrapping", screen)
		})
	}
}

// TestPOAssetPicker_AnEscapeHeavyQueryKeepsThePageOnTheShowingRow.
//
// The `Showing` row is two things in one line: an IDENTIFIER that abbreviates
// (the query) and a FACT that never gives (which page these rows come from).
// The page is reserved BEFORE the query is clipped for exactly that reason —
// the suffix used to be appended after the clip and pushed the row six cells
// past the pane.
//
// Quoting the query AFTER clipping it puts the same defect back one expression
// along. strconv.Quote escapes, so it can return far more cells than it was
// handed: a query of backslashes doubles, a double quote becomes two cells and
// a control rune up to six, while the budget reserved exactly two for the
// quote marks. The row then runs past 51 and clampToBox takes the tail, which
// is the page — the part that was never supposed to give.
func TestPOAssetPicker_AnEscapeHeavyQueryKeepsThePageOnTheShowingRow(t *testing.T) {
	// Backslashes DOUBLE and a double quote is escaped, so this is 25 runes
	// that Quote renders as 52 cells. An asset query really can carry them: a
	// pasted Windows path is the ordinary way it happens.
	query := `\\\\\\\\\\\\\\\\\\\\\\\\"`

	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			// Ten assets over a five-row page, and the search answers with all
			// of them — OMS matches the tag and the serial too, so a query that
			// matches no NAME still comes back with a next page. That pair is
			// the state under test: a committed query AND a page to name.
			fake := &poPickFake{assets: 10, pageSize: 5, suppliers: 1,
				assetSearchServerSide: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, poPhaseKeyMsg("a"))
			if screen.phase != poPhaseAssetPick {
				t.Fatalf("'a' landed on phase %v, want the asset picker", screen.phase)
			}
			r = key(t, r, poPhaseKeyMsg("/"))
			r = poType(t, r, query)
			r = key(t, r, poPhaseKeyMsg("enter"))
			if screen.assetsQuery != query {
				t.Fatalf("the committed query is %q, want %q", screen.assetsQuery, query)
			}
			if !screen.assetsHasNext {
				t.Fatalf("the fixture answered without a next page, so no page suffix is drawn")
			}

			// Onto page 2, so the suffix names a page the operator paged TO —
			// the fact this row exists to carry.
			r = key(t, r, poPhaseKeyMsg("]"))
			if screen.assetsPage != 2 {
				t.Fatalf("']' left the picker on page %d", screen.assetsPage)
			}
			if screen.assetsQuery != query {
				t.Fatalf("paging changed the query to %q", screen.assetsQuery)
			}

			poWantPaneLine(t, screen, "· page 2")
			poAssertFits(t, "asset picker showing an escape-heavy query", screen)
		})
	}
}

// poFailMarkCount / poFailMarkBound are the two things the failure block says
// when it has dropped part of an OMS error body. They are different sentences
// because the two cuts know different things: the fold knows exactly what it
// left behind, the cellPrefix bound does not.
const (
	poFailMarkCount = "more line(s) of the error"
	poFailMarkBound = "more of the error than this pane can hold"
)

// poSubmitFailure drives a real failed submit — stage one line, review, enter —
// and hands back the screen sitting on the failure it produced.
func poSubmitFailure(t *testing.T, body string, h int) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	fake := &poPickFake{catalog: 2, reorder: 0, failCreate: true, createErrBody: body}
	r, screen := poPickerAtSize(t, fake, 80, h)
	r = poStageCostlessLine(t, r, screen)
	r = key(t, r, poPhaseKeyMsg("d"))
	r = key(t, r, poPhaseKeyMsg("enter"))
	if screen.errDetail == "" {
		t.Fatalf("the submit did not fail: phase %v errMsg %q", screen.phase, screen.errMsg)
	}
	return r, screen
}

// TestPOSubmit_AClippedFailureSaysWhatItDropped.
//
// The failure block cuts an unbounded OMS response body twice — cellPrefix
// bounds it before folding, and the fold's tail is dropped to fit three rows —
// and after the conversion it did neither out loud. The operator read three
// muted rows of `<!DOCTYPE html>…` ending mid-token with nothing saying more of
// it existed, on the one step where losing the reason costs the whole order.
// pickerFail, the renderer this block replaced, marked exactly this surface.
//
// All THREE states, because the marker's honesty is the point and two of them
// would pass on a block that always marks or never does: past the cellPrefix
// bound the count would only be true of the prefix that was folded, so the
// sentence carries no number; under it the count is exact and is named; and a
// body that fits carries no marker at all.
func TestPOSubmit_AClippedFailureSaysWhatItDropped(t *testing.T) {
	// Four thirty-cell tokens: under the cellPrefix bound (147 cells at 80
	// columns) and past what three folded rows hold, which is the only body
	// shape that reaches the exact-count arm. A gateway page cannot — it is
	// over the bound — so a fixture list of one would have left that arm untried.
	chunky := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb " +
		"cccccccccccccccccccccccccccccc dddddddddddddddddddddddddddddd"

	for _, tc := range []struct {
		name       string
		body       string
		want, deny string
	}{
		{"a gateway page past the cellPrefix bound", poGatewayHTML,
			poFailMarkBound, poFailMarkCount},
		{"a body the fold outruns", chunky,
			poFailMarkCount, poFailMarkBound},
		{"a body that fits", "the upstream server refused the connection",
			"", ""},
	} {
		for _, h := range poPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", tc.name, h), func(t *testing.T) {
				_, screen := poSubmitFailure(t, tc.body, h)

				// The block may never grow a fourth row: its height feeds
				// bodyAvailForBar, so a block that grew when a reply landed
				// could change whether the bar names PgUp/PgDn.
				if n := len(screen.failLines()); n > poFailDetailRows {
					t.Errorf("the failure block is %d rows, over its %d-row budget",
						n, poFailDetailRows)
				}

				if tc.want == "" {
					for _, mark := range []string{poFailMarkCount, poFailMarkBound} {
						if poPaneHasLine(t, screen, mark) {
							t.Errorf("a failure that fits still claims it dropped something (%q):\n%s",
								mark, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
						}
					}
					// …and the whole reason is on the pane, which is what makes
					// the absence above a fact about the marker rather than
					// about a block that drew nothing.
					poWantPaneLine(t, screen, "upstream server refused")
					poAssertFits(t, "a failure that fits", screen)
					return
				}
				if !poPaneHasLine(t, screen, tc.want) {
					t.Errorf("the failure dropped part of the body and does not say so (%q):\n%s",
						tc.want, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
				if poPaneHasLine(t, screen, tc.deny) {
					t.Errorf("the failure marks its cut with the OTHER cut's wording (%q):\n%s",
						tc.deny, strings.Join(poPaneLinesAt(t, screen, h), "\n"))
				}
				poAssertFits(t, tc.name, screen)
			})
		}
	}
}

// poDrawableHeights is every terminal height Root will draw a frame at, which
// is the range these two sweeps walk. poPaneSizes is {24, 30} and BOTH defects
// below lived under 20 — a check written at those two heights would have passed
// over each of them, which is exactly why nothing caught either.
func poDrawableHeights() []int {
	out := make([]int, 0, 23)
	for h := 8; h <= 30; h++ {
		out = append(out, h)
	}
	return out
}

// poFrameRefused reports whether the layer declined to draw this frame at all,
// which is jdeTooShort's rule and not the one these sweeps are about.
func poFrameRefused(t *testing.T, s Screen, h int) bool {
	t.Helper()
	return strings.Contains(strings.Join(poPaneLinesAt(t, s, h), "\n"), "Too short:")
}

// TestPOSearchBoxes_ADecliningKeyChangesThePaneAtEveryDrawableHeight.
//
// A picker with its search box open has nowhere to answer a declining key but
// the NOTE: pendingDecline — the one channel that reaches the layer's status
// row, which is outside the header budget and never gives — is reached from the
// supplier, source and review phases only. So while the note rode as CONTEXT
// under the box, jdeFitHeader trimmed the only answer those presses had, and
// the pane came back byte-identical: 80x11 and 80x12 on the item filter, and
// 80x11 through 80x13 on the asset search, whose header carries a row more.
// This screen's oldest report, reached by geometry rather than a missing arm.
//
// The HEIGHT is derived rather than named, because that is what made this
// invisible for nine rounds: poPaneSizes is {24, 30} and every existing sweep
// of these states runs at one of them, where the header is not trimmed at all.
// Frames the layer REFUSES to draw are skipped — jdeTooShort is its own rule —
// and everything else must answer.
func TestPOSearchBoxes_ADecliningKeyChangesThePaneAtEveryDrawableHeight(t *testing.T) {
	cases := []struct {
		name string
		// reach leaves the screen one press short of the decline.
		reach   func(t *testing.T, h int) (Root, *PurchaseOrderCreateScreen)
		decline tea.KeyMsg
	}{
		{
			name: "item filter over a query that matches nothing",
			reach: func(t *testing.T, h int) (Root, *PurchaseOrderCreateScreen) {
				t.Helper()
				r, screen := poPickerAtSize(t, &poPickFake{catalog: 4, suppliers: 1}, 80, h)
				r = key(t, r, poPhaseKeyMsg("i"))
				r = pump(t, r, nil, 0)
				r = key(t, r, poPickerKeyMsg("/"))
				r = poType(t, r, "zzz")
				if !screen.itemSuppliersTyping {
					t.Fatalf("setup did not open the item filter")
				}
				return r, screen
			},
			decline: poPickerKeyMsg("enter"),
		},
		{
			name: "asset search while one is already out",
			reach: func(t *testing.T, h int) (Root, *PurchaseOrderCreateScreen) {
				t.Helper()
				r, screen := poPickerAtSize(t, &poPickFake{catalog: 4, suppliers: 1, assets: 3}, 80, h)
				r = key(t, r, poPhaseKeyMsg("a"))
				r = pump(t, r, nil, 0)
				r = key(t, r, poPickerKeyMsg("/"))
				r = poType(t, r, "zzz")
				// UN-PUMPED, so the search is still out; enter closes the
				// box on its way, so `/` opens it again over the lookup —
				// which is the state the next enter declines from.
				next, _ := r.Update(poPickerKeyMsg("enter"))
				r = next.(Root)
				next, _ = r.Update(poPickerKeyMsg("/"))
				r = next.(Root)
				if !screen.assetsTyping || !screen.assetsLoading {
					t.Fatalf("setup left typing=%v loading=%v, want both",
						screen.assetsTyping, screen.assetsLoading)
				}
				return r, screen
			},
			decline: poPickerKeyMsg("enter"),
		},
	}

	for _, tc := range cases {
		for _, h := range poDrawableHeights() {
			t.Run(fmt.Sprintf("%s/80x%d", tc.name, h), func(t *testing.T) {
				r, screen := tc.reach(t, h)
				if poFrameRefused(t, screen, h) {
					t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
				}
				before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				next, _ := r.Update(tc.decline)
				r = next.(Root)
				after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				if before == after {
					t.Fatalf("the declining key redrew a byte-identical pane at 80x%d:\n%s", h, after)
				}
				poAssertFits(t, "a declining key inside a search box", screen)
			})
		}
	}
}

// TestPOReview_TheCartTotalOutlivesTheOptionalRows.
//
// A RANK DOES NOT REMOVE THE SIGNIFICANCE OF ORDER WITHIN A RANK. jdeFitHeader
// gives ground from the END within each rank, so two rows sharing one are still
// separated by POSITION — and merging the attribution block and the cart-total
// block into a single jdeHeadContext block, to save a separator row, made
// position the tiebreak again. Written total-last, an 80x20 review frame under
// a dozen staged lines dropped `Total: … (12 line items)` while
// `Agreement ..... (none)` and `Committee ..... (none)` were still drawn: the
// money floor going off the surface an order is COMMITTED from, so two empty
// optionals could stay.
//
// The assertion is the rank rule read off the PANE rather than off the function
// that implements it: within the context rank the layer gives ground from the
// end, so a context row that IS drawn implies every context row emitted before
// it is drawn too. That catches the append order for the whole block, not just
// the one row the report named.
func TestPOReview_TheCartTotalOutlivesTheOptionalRows(t *testing.T) {
	// The band where the header is really trimmed is narrow and nothing says
	// where it is, so the sweep walks every drawable height and counts the ones
	// that exercised it. Zero would mean the check asserted nothing.
	trimmed := 0

	for _, h := range poDrawableHeights() {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{reorder: 12, catalog: 2, assets: 1,
				agreements: 1, workOrders: 2, committees: 1}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, poPhaseKeyMsg("r"))
			r = key(t, r, poPhaseKeyMsg("a"))
			_ = r
			if screen.phase != poPhaseReview || len(screen.lines) != 12 {
				t.Fatalf("setup landed on phase %v with %d line(s)", screen.phase, len(screen.lines))
			}
			if poFrameRefused(t, screen, h) {
				return
			}
			shown := strings.Join(poPaneLinesAt(t, screen, h), "\n")

			drawn := func(text string) bool {
				return strings.Contains(shown,
					truncateVisible(strings.TrimRight(text, " "), screenBodyWidth(80)))
			}
			lost := ""
			for _, row := range screen.headerLines() {
				if row.Rank != jdeHeadContext || strings.TrimSpace(row.Text) == "" {
					continue
				}
				if !drawn(row.Text) {
					if lost == "" {
						lost = strings.TrimSpace(row.Text)
					}
					continue
				}
				if lost != "" {
					t.Errorf("the pane at 80x%d keeps %q while it has dropped %q, which the "+
						"header emits EARLIER at the same rank — ground is given from the END, "+
						"so this is the block ordered so the wrong row survives:\n%s",
						h, strings.TrimSpace(row.Text), lost, shown)
				}
			}

			// The half the report was filed about, stated in its own terms so a
			// future reader sees the money fact named rather than inferred.
			total, optional := drawn("Total:"), drawn(poRowAgreement+jdeLeader)
			if !optional && total {
				trimmed++
			}
			if optional && !total {
				t.Errorf("the pane at 80x%d draws an optional attribution row and NOT what the "+
					"cart comes to — the money floor is what a confirm surface may not lose:\n%s",
					h, shown)
			}
		})
	}
	if trimmed == 0 {
		t.Errorf("no drawable height trimmed an optional row while keeping the total, so this " +
			"sweep asserted nothing about the order of sacrifice")
	}
}

// TestPOLineForm_TheBarDoesNotOfferPagingItCannotDo.
//
// A FIELD form has nothing to page: its cursor WRAPS, so UP/DN reaches every
// one of its three or four rows in at most three presses. Paging was left on
// the shared clamped path, so wherever the fields outran the pane the bar named
// PgUp/PgDn and jdePageCursor clamped — on the first row PgUp blurred and
// re-focused the SAME field, which is a named key with no visible effect.
//
// That state does not exist at 24 or 30 rows, where the line body fits and the
// keys are not named at all, so this sweep walks every drawable height and
// counts the ones where the body really does outrun the pane.
func TestPOLineForm_TheBarDoesNotOfferPagingItCannotDo(t *testing.T) {
	overflowed := 0

	for _, h := range poDrawableHeights() {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poPickerAtSize(t, &poPickFake{catalog: 2, suppliers: 1}, 80, h)
			r = key(t, r, poPhaseKeyMsg("i"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			if screen.phase != poPhaseLine || len(screen.lineFields()) != 4 {
				t.Fatalf("setup landed on phase %v with %d field(s)", screen.phase, len(screen.lineFields()))
			}
			if poFrameRefused(t, screen, h) {
				return
			}
			// Asked of the LAYER directly rather than through bodyPagesFor,
			// which is the predicate under test: this is "does the body outrun
			// the pane", the state in which the shared path used to name the
			// keys.
			body, _ := screen.body()
			if screen.bodyScrollsForBar(body, len(screen.headerLines()), screen.barItems(true)) {
				overflowed++
			}

			if bar := poBarText(screen.bar()); strings.Contains(bar, "PgUp") {
				t.Errorf("the line form's bar at 80x%d names PgUp/PgDn, which clamps on a "+
					"wrapping field form and cannot move the cursor: %s", h, bar)
			}
			for _, k := range []string{"pgup", "pgdown"} {
				before, was := strings.Join(poPaneLinesAt(t, screen, h), "\n"), screen.lineFocused
				r = key(t, r, poPhaseKeyMsg(k))
				if screen.lineFocused != was {
					t.Errorf("%q moved the line form's focus from field %d to %d while the bar "+
						"does not name it", k, was, screen.lineFocused)
				}
				if after := strings.Join(poPaneLinesAt(t, screen, h), "\n"); after != before {
					t.Errorf("%q changed the pane at 80x%d while the bar does not name it:\n%s",
						k, h, after)
				}
			}
		})
	}
	if overflowed == 0 {
		t.Errorf("no drawable height made the line body outrun the pane, so this sweep never " +
			"reached the state the paging keys used to be named in")
	}
}

// poSameValueNoLessShown reports whether `after` is the same value as `before`,
// shown at no less length — which is exactly what dropping the key column does
// to an attribution row: the two freed cells go to the operator's data, so a
// clipped name comes back two characters longer.
//
// Written as "the shorter stem prefixes the longer" rather than as equality,
// because equality is a claim the code does not make and only held while the
// fixture stayed under the bound. It is still strong enough for what this test
// is for: any OTHER change to the value — a different name, an emptied row, a
// truncation going the wrong way — fails it.
func poSameValueNoLessShown(before, after string) bool {
	stem := func(v string) string { return strings.TrimSuffix(v, "…") }
	b, a := stem(before), stem(after)
	return len(a) >= len(b) && strings.HasPrefix(a, b)
}

// poBarSpellableTokens is every token the ACTION BAR can spell, lower-cased,
// derived from poBarKeyNames — the same table both purchasing bar sweeps read.
//
// Derived rather than listed, because the roster it replaces could only ever
// find duplication somebody had already imagined: six phrases ("esc closes",
// "r reloads", "/=Search"…) chosen by hand, inside the check built to enforce
// the one-surface rule. Two notes naming keys sat under it for the whole run.
var poBarSpellableTokens = func() map[string]bool {
	out := map[string]bool{}
	for token := range poBarKeyNames {
		out[strings.ToLower(token)] = true
	}
	return out
}()

// poBarKeysEnglishAlso are the single-letter keys that are also ordinary
// English words, matched in upper case only. See poBodyKeyClaims.
var poBarKeysEnglishAlso = map[string]bool{"a": true, "i": true}

// poBodyKeyClaims returns the body lines that NAME a key the bar can spell.
//
// SINGLE-LETTER AMBIGUITY, and which way it errs. A bare `a` is also the
// English article, a bare `/` is also the divider in "current 0 / min 0", and a
// bare `[` is also half a checkbox — so a naive token scan matches every row on
// the screen. It is resolved the way the sibling parser poBarNamedKeys resolved
// the same problem: a key is read only at the START of a segment, segments
// being the ` · ` joints every note on this screen is written and folded at.
// A key claim is "<key> <what it does>"; prose that merely CONTAINS a letter
// carries it mid-sentence.
//
// Two further rules, each stated once rather than curated per sentence:
//
//   - a bracketed span is a MARK, not a key: `[ ]` and `[x]` on a reorder row,
//     `[Inventory item]` on a cart row, `[reorder pending]` on a queue row. The
//     asset pager's `[` and `]` name themselves UNPAIRED, so removing paired
//     spans leaves a real claim standing.
//   - a line carrying jdeLeader is a LABEL/VALUE row, whose key column (the `g`
//     on `g  Agreement ..... `) is a re-reading of what the bar says on the same
//     frame, governed by TestPOSubmit_TheFrozenChooserHeaderDropsTheKeyColumn.
//     What this scan is for is a key named in PROSE, the second surface.
//
// The match is CASE-INSENSITIVE — "R, I, A or F" names r/i/a/f whatever case it
// is written in — with ONE stated exception, poBarKeysEnglishAlso: `a` and `i`
// are also the English article and pronoun, and a note may legitimately open a
// segment with either ("a lookup is still out"). Those two are matched in UPPER
// case only.
//
// That exception is a NAMED RESIDUAL GAP rather than a tidy-up, and it has a
// sibling: a note writing a LOWER-case "a …" or "i …" as a key claim slips
// through, and so does one whose FIRST segment is a bare claim rather than a
// lead. Both are bounded and written down here, which is the whole difference
// from the roster of six hand-picked phrases this replaces — that one's gaps
// were everything nobody had thought of, and two notes naming keys sat in them
// for the length of the run.
func poBodyKeyClaims(t *testing.T, s *PurchaseOrderCreateScreen, h int) []string {
	t.Helper()
	marks := regexp.MustCompile(`\[[^\[\]]*\]`)
	var out []string
	for _, line := range poPaneLinesAt(t, s, h) {
		if strings.HasPrefix(strings.TrimSpace(line), "----") {
			break
		}
		if strings.Contains(line, jdeLeader) {
			continue
		}
		for i, segment := range strings.Split(marks.ReplaceAllString(line, " "), "·") {
			if i == 0 {
				// The LEAD, which decision 4 allows to name a key and
				// AGENTS.md requires to: "ctrl+x removes nothing" is a
				// statement about the press just made, and naming the key is
				// what stops two keys answering with one sentence and redrawing
				// an identical pane. What the rule forbids is a CLAIM ABOUT
				// WHAT WORKS, and those ride in a later segment — both of the
				// notes this scan was written for did.
				continue
			}
			if poSegmentNamesAKey(segment) {
				out = append(out, line)
				break
			}
		}
	}
	return out
}

// poSegmentNamesAKey reports whether one ` · ` segment OPENS with a key the bar
// can spell. Shared by both scans so the derivation from poBarKeyNames — and
// the two exceptions above — are stated once and cannot drift apart.
func poSegmentNamesAKey(segment string) bool {
	fields := strings.Fields(segment)
	if len(fields) == 0 {
		return false
	}
	head := strings.Trim(fields[0], ",.;:!?()")
	if head == "" || poBarKeysEnglishAlso[head] {
		return false
	}
	return poBarSpellableTokens[strings.ToLower(head)]
}

// poNoteKeyClaims is the same rule read off the note's OWN TEXT, before the
// fold, and it exists because the pane scan cannot see past one.
//
// pickerWrap DROPS the ` · ` at a fold (its own doc comment says so), so a note
// wider than the fold width renders its later segments on continuation lines
// carrying no separator at all. The pane scan splits each drawn LINE on `·`, so
// such a line arrives as a single segment at index 0 and is skipped wholesale
// as a lead — a blind spot with no bound on it, since any note long enough to
// fold falls in. Reading the unfolded text removes the fold from the question
// entirely rather than teaching the scan to recognise its debris.
//
// Both are kept, and they cover different things: this one sees every segment
// of the phase's note however it folds, and the PANE scan sees everything else
// the frame draws — picker rows, cart rows, the working line — which no note
// text can reach.
func poNoteKeyClaims(t *testing.T, s *PurchaseOrderCreateScreen) []string {
	t.Helper()
	text := s.phaseNote().text
	if text == "" {
		text = s.standingNote()
	}
	var out []string
	for i, segment := range strings.Split(text, poLeadJoint) {
		if i == 0 {
			continue // the LEAD, for the reason the pane scan skips it
		}
		if poSegmentNamesAKey(segment) {
			out = append(out, segment)
		}
	}
	return out
}

// poAssertBodyNamesNoKey is the one-surface rule, read off the rendered pane AND
// off the phase's unfolded note — the second because the first cannot see a
// segment the fold has separated from its ` · `.
func poAssertBodyNamesNoKey(t *testing.T, what string, s *PurchaseOrderCreateScreen, h int) {
	t.Helper()
	for _, line := range poBodyKeyClaims(t, s, h) {
		t.Errorf("%s: a body line names a key the bar can spell — the bar is the one "+
			"surface that names a key:\n\t%q\nwhole pane:\n%s",
			what, line, strings.Join(poPaneLinesAt(t, s, h), "\n"))
	}
	for _, segment := range poNoteKeyClaims(t, s) {
		t.Errorf("%s: the phase's note names a key the bar can spell in %q — the bar is "+
			"the one surface that names a key (note: %q)",
			what, strings.TrimSpace(segment), s.phaseNote().text)
	}
}

// TestPOOneSurface_TheScanSeesPastAFold is the non-vacuity proof for the note
// scan, and the reason it exists at all.
//
// pickerWrap drops the ` · ` at a fold, so a note wider than the fold width
// renders its later segments on continuation lines carrying no separator. The
// PANE scan splits each drawn line on `·`, so such a line arrives as one
// segment at index 0 and is skipped as a lead — every note long enough to fold
// fell in that hole. The note scan reads the text BEFORE the fold, so the fold
// cannot hide anything from it.
//
// The note here is constructed rather than taken from the screen, because the
// two notes this guard was written for were stripped in the round that added
// it: nothing on the screen is long enough to fold any more, which is precisely
// why the gap could sit unnoticed. The claim under test is the GUARD's, so the
// guard is what is driven.
func TestPOOneSurface_TheScanSeesPastAFold(t *testing.T) {
	// Past the 49-cell fold width at 80 columns, with the key claim in the
	// segment the fold pushes onto a continuation line.
	const folded = "the reorder queue is empty for this supplier · r reloads it"

	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poPickerAtSize(t, &poPickFake{catalog: 4, suppliers: 1}, 80, h)
			_ = r
			if screen.phase != poPhaseSource {
				t.Fatalf("setup landed on phase %v, want the source chooser", screen.phase)
			}
			screen.sourceNote.say(folded, StatusWarn)

			drawn := poPaneLinesAt(t, screen, h)
			if !poPaneHasLine(t, screen, "r reloads it") {
				t.Fatalf("the constructed note is not on the pane, so nothing here is "+
					"under test:\n%s", strings.Join(drawn, "\n"))
			}
			// The fold really did separate the claim from its joint — without
			// that, this test would be asserting about a note the pane scan
			// could have seen all along.
			for _, line := range drawn {
				if strings.Contains(line, "r reloads it") && strings.Contains(line, "·") {
					t.Fatalf("the note did not fold at the joint, so the blind spot this "+
						"test is about is not reached:\n\t%q", line)
				}
			}

			if got := poBodyKeyClaims(t, screen, h); len(got) != 0 {
				t.Errorf("the PANE scan saw the folded claim after all (%q) — if it can, "+
					"this test no longer proves what the note scan is for", got)
			}
			if got := poNoteKeyClaims(t, screen); len(got) == 0 {
				t.Errorf("the NOTE scan missed a key claim on a folded continuation line, "+
					"which is the whole reason it reads the unfolded text:\n\t%q", folded)
			}
		})
	}
}

// TestPOSubmit_ADeclineDoesNotPushTheSubmitOffTheStatusRow.
//
// The status row cannot fold, and the lead was joined in front of the working
// sentence unbounded. With the longest lead in this file — "enter commits
// nothing · the committed row goes back", 51 cells, exactly the row's width at
// 80 columns — the whole "a purchase order is being created" statement was
// pushed off. pendingDecline's flash expires after four seconds and the frozen
// bar names only Enter/UP-DN/Esc, so from then on nothing on the pane said a
// submit was out.
//
// The lead is bounded against what the subject needs now. Both halves are
// asserted: the subject because losing it is the defect, and the lead's KEY
// NAME because a lead that no longer says which press it answers is the
// identical-pane defect one row over.
//
// AN ASSERTION CHOSEN BECAUSE IT PASSES IS NOT EVIDENCE. This test asserted
// "Creating the purchase" — 21 cells, which SURVIVED the 25-cell clip the
// defect left — so it went green over a row reading "Creating the purchase
// or…" with the supplier gone: the fixed words cut mid-word and the identifier
// lost, rule 6 inverted, certified by a passing check. The substring to assert
// is the one the RULE requires (the fixed words in full), not one the
// truncation happens to leave. It is the third of this shape in this run,
// beside the two fixtures too short to reach the bound they asserted about
// (AGENTS.md carries the general form).
func TestPOSubmit_ADeclineDoesNotPushTheSubmitOffTheStatusRow(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			// Two suppliers, so `enter` on a DIFFERENT one is reachable — that
			// is the arm carrying the longest lead on the screen.
			fake := &poPickFake{catalog: 4, suppliers: 3}
			r, screen := poPickerAtSize(t, fake, 80, h)
			for _, k := range []string{"i", "enter", "enter", "d"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			next, _ := r.Update(poPhaseKeyMsg("enter")) // POST out, un-pumped
			r = next.(Root)
			if !screen.pending {
				t.Fatalf("setup did not leave a submit in flight")
			}
			for _, k := range []string{"esc", "esc", "down"} {
				r = key(t, r, poPhaseKeyMsg(k))
			}
			if screen.phase != poPhaseSupplier {
				t.Fatalf("setup landed on phase %v, want the supplier picker", screen.phase)
			}
			r = key(t, r, poPhaseKeyMsg("enter"))
			if screen.pendingLead == "" {
				t.Fatalf("enter on another supplier left no lead to crowd the row")
			}

			// Both halves are read off the ONE row they now share, because
			// the supplier this order is for is drawn in the pinned header
			// and in the body's own list as well: a pane-wide substring
			// would find "Acme Supply" there and prove nothing about the
			// status row.
			var status string
			for _, line := range poPaneLines(t, screen) {
				if strings.Contains(line, "enter commits nothing") {
					status = strings.TrimRight(line, " ")
				}
			}
			if status == "" {
				t.Fatalf("no pane line carries the lead's key name:\n%s",
					strings.Join(poPaneLines(t, screen), "\n"))
			}
			// The FACT survives whole — not a prefix of it that the clip
			// happened to spare.
			if !strings.Contains(status, poSubmitWords) {
				t.Fatalf("the status row %q does not carry %q whole", status, poSubmitWords)
			}
			// And the IDENTIFIER is what gave, visibly marked. The fixture
			// reaches that bound: "Acme Supply" is 11 cells into the 6 the
			// row has left for it under this lead.
			if strings.Contains(status, "Acme Supply") {
				t.Fatalf("the status row %q kept the whole supplier name, so the "+
					"bound under test was never reached", status)
			}
			if !strings.HasSuffix(status, "…") {
				t.Fatalf("the status row %q dropped the supplier without saying so", status)
			}
			poAssertFits(t, "a declined key while the submit is out", screen)
		})
	}
}
