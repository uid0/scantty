package tui

import (
	"fmt"
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
//     vocabulary from, so the answer is not to curate one.
//
// The LIST screens get the same treatment from Workspaces() in
// list_bar_honesty_test.go, and the screen's own state fields from
// reflect.TypeOf in po_create_picker_status_test.go.

// poKeySpace is every keystroke the sweep presses: printable ASCII, then the
// named keys a terminal sends that are not runes. Nothing is curated — a key
// bound tomorrow is pressed by this list today.
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
	)
}

func poPhaseKeyMsg(k string) tea.KeyMsg {
	switch k {
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	}
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

// poBarNamedKeys reads a frame's action bar into the set of keys it CLAIMS.
//
// Segment-start tokens are looked up strictly (poPickerBarKeys), because a
// single-letter key is only unambiguous when it leads its own claim: scanning
// prose for a bare "a" would find the article. Segments whose first token is
// not a key are then scanned for the MULTI-character key names, which cannot
// collide with English. A single-letter key buried inside prose is therefore
// NOT counted as named, which is the safe direction: the sweep reports it as
// acting-while-unnamed rather than passing over it — and that is exactly how
// `b` was caught. It is bound on all three association pickers precisely as
// `esc` is, and none of their bars named it; the bars are `key claim · key
// claim` now, which is what a bar has to be for this rule to be checkable at
// all. The buried scan stays for the bars that keep a prose lead-in.
func poBarNamedKeys(t *testing.T, bar string) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	add := func(keys []string) {
		for _, k := range keys {
			named[k] = true
		}
	}
	buried := []struct {
		token string
		keys  []string
	}{
		{"j/k", []string{"j", "k", "down", "up"}},
		{"tab/shift+tab", []string{"tab", "shift+tab", "down", "up"}},
		{"tab/shift-tab", []string{"tab", "shift+tab", "down", "up"}},
		{"↑↓", []string{"up", "down", "ctrl+p", "ctrl+n"}},
		{"shift-tab", []string{"shift+tab"}},
		{"shift+tab", []string{"shift+tab"}},
		{"tab", []string{"tab"}},
		{"enter", []string{"enter"}},
		{"esc", []string{"esc"}},
		{"ctrl+t", []string{"ctrl+t"}},
		{"ctrl+e", []string{"ctrl+e"}},
		{"ctrl+x", []string{"ctrl+x"}},
	}
	for _, seg := range strings.Split(bar, " · ") {
		fields := strings.Fields(strings.TrimSpace(seg))
		if len(fields) == 0 {
			continue
		}
		if keys, ok := poPickerBarKeys[fields[0]]; ok {
			add(keys)
			continue
		}
		lower := strings.ToLower(seg)
		for _, b := range buried {
			for _, word := range strings.FieldsFunc(lower, func(r rune) bool {
				return r == ' ' || r == ',' || r == '(' || r == ')' || r == '.'
			}) {
				if word == b.token {
					add(b.keys)
				}
			}
		}
	}
	return named
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
		r = key(t, r, poPhaseKeyMsg("j"))
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
		{poPhaseSupplier, "supplier picker", plain, press("b"), false},
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
				r = key(t, r, poPhaseKeyMsg("b"))
				r = key(t, r, poPhaseKeyMsg("j"))
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
				bar := screen.helpText()
				named := poBarNamedKeys(t, bar)
				poAssertFits(t, c.name, screen)

				for _, k := range space {
					if c.typing && (poIsPrintable(k) || poFieldKeys[k]) && !named[k] {
						continue
					}
					acted := false
					// Probed from more than one position: j does nothing at the
					// bottom of a list and k nothing at the top, so a key is
					// dead only if it does nothing from ANY of them. `up` is in
					// the set for the review cart, where j types into the notes
					// field and the cursor lands on the LAST line, so nothing
					// else would let `down` move.
					for _, probe := range [][]string{nil, {"j"}, {"up"}} {
						pr, ps := fresh(probe)
						before := poPickerState(ps)
						_, cmd := pr.Update(poPhaseKeyMsg(k))
						if poPickerState(ps) != before || poCmdActs(cmd) {
							acted = true
						}
					}
					switch {
					case named[k] && !acted:
						t.Errorf("%s names %q but pressing it changes nothing (bar: %q)", c.name, k, bar)
					case !named[k] && acted:
						t.Errorf("%s does not name %q, but pressing it acts (bar: %q)", c.name, k, bar)
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
			r = key(t, r, poPhaseKeyMsg("b"))
			r = key(t, r, poPhaseKeyMsg("j"))
			r = key(t, r, poPhaseKeyMsg("enter"))
			if screen.phase != poPhaseSupplierSwitch {
				t.Fatalf("setup landed on phase %v, want the switch confirm", screen.phase)
			}

			before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			// In sequence, no reset between presses: two keys that answered
			// with one sentence would redraw the pane the first one left.
			for _, k := range []string{"enter", "j", "enter"} {
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
				// The decline points at the keys that DO answer, and it reads
				// them off the same sentence the frame prints above it.
				poWantPaneLine(t, screen, "ctrl+x drops")
				poWantPaneLine(t, screen, "esc keeps the cart")
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
			poWantPaneLine(t, screen, "enter submit")

			// r.Update without pump: the POST is genuinely still out.
			next, _ := r.Update(poPhaseKeyMsg("enter"))
			r = next.(Root)
			if !screen.pending {
				t.Fatalf("the submit did not go out")
			}
			poRejectPaneLine(t, screen, "enter submit")
			poWantPaneLine(t, screen, "Submitting…")

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
			poWantPaneLine(t, screen, "PO notes:")
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
	fake := &poPickFake{catalog: 4, suppliers: 3,
		agreements: 1, workOrders: 2, committees: 1}
	r, screen := poPickerAtSize(t, fake, 80, h)
	for _, k := range []string{"i", "enter", "enter", "i", "j", "enter", "enter", "d"} {
		r = key(t, r, poPhaseKeyMsg(k))
	}
	if screen.phase != poPhaseReview || len(screen.lines) != 2 {
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
		{"supplier picker", poPhaseSupplier, []string{"esc", "b"}},
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
				bar := screen.helpText()
				named := poBarNamedKeys(t, bar)
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
					if screen.phase != st.phase && !frozen[screen.phase] {
						t.Fatalf("%q reached phase %v while the submit was out, and it is not "+
							"swept as frozen", k, screen.phase)
					}
					if !named[k] && acted {
						t.Errorf("%s with a submit in flight does not name %q, but pressing it acts (bar: %q)",
							st.name, k, bar)
					}
					if named[k] && !acted {
						for _, probe := range []string{"up", "j"} {
							pr, ps := build(probe)
							pb := poPickerState(ps)
							_, pc := pr.Update(poPhaseKeyMsg(k))
							if poPickerState(ps) != pb || poCmdActs(pc) {
								acted = true
								break
							}
						}
						if !acted {
							t.Errorf("%s with a submit in flight names %q but pressing it changes nothing (bar: %q)",
								st.name, k, bar)
						}
					}
					if acted {
						// The frame has moved on, so the next key would be
						// judged against a state this one left rather than the
						// one the bar was read from.
						r, screen = build()
					}
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
			step("x", "x removes nothing")
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
			r = key(t, r, poPhaseKeyMsg("b"))
			if screen.phase != poPhaseSupplier {
				t.Fatalf("b landed on phase %v, want the supplier picker", screen.phase)
			}
			r = key(t, r, poPhaseKeyMsg("j")) // highlight a DIFFERENT supplier
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
			for _, k := range []string{"i", "enter", "enter", "i", "j", "enter", "enter", "d"} {
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
			poWantPaneLine(t, screen, "enter submit")
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
