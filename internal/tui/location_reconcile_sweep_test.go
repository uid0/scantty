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

// The rules the location count is held to, asserted as RULES and over sets
// DERIVED from their authority — the shape po_create_phase_sweep_test.go,
// po_add_line_sweep_test.go and receive_form_sweep_test.go already use, because
// three keys reached an operator's terminal in this package doing nothing while
// a bar named them, and every time the sweep that existed to stop it was reading
// a hand-kept roster the key was not in.
//
//	PHASES come from the reconPhase iota, walked to reconPhaseCount.
//	KEYS   come from poKeySpace() — every printable ASCII rune plus the named
//	       specials — because no authority for "the keys a terminal can send"
//	       can be derived, so the answer is to press the whole space.
//	FIELDS come from reflect over the screen struct: every one is either in the
//	       fingerprint or declared as something a key may move WITHOUT acting.
//	FOCUS  comes from reflect for textinput fields, because a caret lives INSIDE
//	       the value rather than beside it.

// ---------------------------------------------------------------------------
// Reaching each phase
// ---------------------------------------------------------------------------

type reconPhaseCase struct {
	phase reconPhase
	name  string
	// typing marks a phase where a text input owns the keyboard. There the rule
	// is about COMMAND keys only: a printable rune typed into a field acts by
	// editing the value, which is what the field is FOR.
	typing bool
	reach  func(t *testing.T, r Root, s *LocationReconcileScreen) Root
}

// reconPhasesWithoutKeys records a phase where NO key acts, so "no entry" and
// "nothing to check" cannot look the same. It is empty today, and a phase added
// to it needs a reason.
var reconPhasesWithoutKeys = map[reconPhase]string{}

func reconPhaseCases() []reconPhaseCase {
	down := tea.KeyMsg{Type: tea.KeyDown}
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	ctrlE := tea.KeyMsg{Type: tea.KeyCtrlE}
	ctrlR := tea.KeyMsg{Type: tea.KeyCtrlR}

	// loaded pumps the grid fetch and then drives. Every case but LOADING uses
	// it, because LOADING is the state before that reply lands: pumping Init
	// there would resolve the very request the phase is defined by.
	loaded := func(keys ...tea.KeyMsg) func(*testing.T, Root, *LocationReconcileScreen) Root {
		return func(t *testing.T, r Root, s *LocationReconcileScreen) Root {
			r = pump(t, r, s.Init(), 0)
			for _, k := range keys {
				r = key(t, r, k)
			}
			return r
		}
	}
	// counted puts numbers in the boxes WITHOUT keystrokes and then drives.
	//
	// Not a shortcut around the flow: what is under test is the key pressed
	// AFTER the reach, and every reach still goes through the real arms. Typing
	// a rune at a time costs a cursor-blink timer per key, which this sweep pays
	// on every rebuild — and it rebuilds once per key per probe per pane.
	counted := func(keys ...tea.KeyMsg) func(*testing.T, Root, *LocationReconcileScreen) Root {
		return func(t *testing.T, r Root, s *LocationReconcileScreen) Root {
			r = pump(t, r, s.Init(), 0)
			s.counts[0].SetValue("9")
			s.counts[1].SetValue("240")
			for _, k := range keys {
				r = key(t, r, k)
			}
			return r
		}
	}

	return []reconPhaseCase{
		// LOADING is the CONSTRUCTED state: the screen is built with the grid
		// fetch in flight, so the reach does nothing at all — pumping Init here
		// would resolve the very request the phase is defined by.
		{reconLoading, "loading", false, func(t *testing.T, r Root, s *LocationReconcileScreen) Root {
			return r
		}},
		{reconBlocked, "blocked (empty room)", false, loaded()},
		{reconCount, "count (on the scan row)", true, loaded()},
		// The cursor ON AN ITEM is a different state of the same phase and the
		// bar changes shape there — Enter means "next scan" rather than "find",
		// and Ctrl+E is named at all. A phase's cases must span every state its
		// bar changes shape in; a derived phase roster says nothing about that.
		{reconCount, "count (on an item, nothing typed)", true, loaded(down)},
		{reconCount, "count (on an item, counted)", true, counted(down)},
		{reconRow, "row detail (reason)", false, counted(down, ctrlE)},
		// The NOTES field owns the keyboard on the same phase, and ←→ stops
		// being the bar's key there — the second state this bar changes in.
		{reconRow, "row detail (notes)", true, counted(down, ctrlE, down, down)},
		{reconReview, "review", false, counted(down, ctrlR)},
		{reconDone, "done", false, counted(down, ctrlR, enter)},
	}
}

// TestReconcile_EveryPhaseIsSwept walks the reconPhase iota to its sentinel and
// fails on any phase the sweeps below have no entry for. This is what makes
// their coverage a property of the code rather than of who last remembered to
// extend a table.
func TestReconcile_EveryPhaseIsSwept(t *testing.T) {
	covered := map[reconPhase]bool{}
	for _, c := range reconPhaseCases() {
		covered[c.phase] = true
	}
	for p := reconPhase(0); p < reconPhaseCount; p++ {
		if covered[p] {
			if why, ok := reconPhasesWithoutKeys[p]; ok {
				t.Errorf("phase %v is both swept and recorded as keyless (%q)", p, why)
			}
			continue
		}
		if _, ok := reconPhasesWithoutKeys[p]; !ok {
			t.Errorf("phase %v has no reconPhaseCases entry and is not recorded in "+
				"reconPhasesWithoutKeys — a key on it would be judged by nothing", p)
		}
	}
}

// ---------------------------------------------------------------------------
// The state a key may move
// ---------------------------------------------------------------------------

// reconState is the observable state of the screen. Text inputs contribute
// their VALUE and their FOCUS, never their View: a blinking caret moving is not
// a key acting, and a fingerprint a caret can move is one any keypress passes.
//
// It must NOT range a map: Go's randomised iteration would make every
// fingerprint compare unequal to itself and the sweep would go red for a reason
// unrelated to the property it names.
func reconState(s *LocationReconcileScreen) string {
	var b strings.Builder
	fmt.Fprint(&b, s.phase, "|", s.loading, s.pending,
		"|", s.focused, s.rowIdx, s.fieldCursor, s.reviewCursor, s.sheetOffset,
		"|", s.scan.Value(), s.scan.Focused(),
		"|", s.notes.Value(), s.notes.Focused(),
		"|", s.open.Value(), s.open.Focused(),
		"|", s.parked.Value(), s.parked.Focused(),
		"|", s.terminalWidth, s.terminalHeight)
	for i := range s.counts {
		fmt.Fprint(&b, "|c", i, ":", s.counts[i].Value(), s.counts[i].Focused())
	}
	for i, row := range s.rows {
		fmt.Fprint(&b, "|r", i, ":", row.reasonIx, row.skipReorder, row.notes, row.openCount)
	}
	fmt.Fprint(&b, "|items:", len(s.items))
	if s.result != nil {
		fmt.Fprint(&b, "|result:", s.result.Reconciled, s.result.ReordersCreated)
	}
	if s.grid != nil {
		fmt.Fprint(&b, "|grid:", s.grid.LocationID, len(s.grid.Items))
	}
	return b.String()
}

// reconStateFingerprinted / reconStateDeclined classify EVERY field of the
// screen, enumerated by reflection — so a field added tomorrow fails by name
// until somebody decides which half it belongs in.
var reconStateFingerprinted = map[string]bool{
	"jdeScreen": true, "grid": true, "items": true, "rows": true,
	"phase": true, "loading": true, "pending": true,
	"scan": true, "counts": true, "focused": true,
	"rowIdx": true, "fieldCursor": true, "notes": true, "open": true,
	"reviewCursor": true, "sheetOffset": true, "result": true, "parked": true,
}

// reconStateDeclined is every field a key may move WITHOUT having acted, with
// the reason. All of them are the screen ANSWERING a key or holding something
// fixed at construction: counting them would call "j does nothing here" an
// action and invert the rule this sweep enforces.
var reconStateDeclined = map[string]string{
	"deps":       "injected dependencies; no keystroke reaches them",
	"locID":      "the location being counted, fixed at construction",
	"locName":    "the room's name as the caller knew it, fixed at construction",
	"note":       "the screen's answer to the last keypress — a decline writes here",
	"failHead":   "the failure line's headline, written only by a reply off the wire",
	"failDetail": "the failure line's unbounded half, written with failHead",
}

func TestReconcileScreen_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(LocationReconcileScreen{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		_, in := reconStateFingerprinted[name]
		_, out := reconStateDeclined[name]
		switch {
		case in && out:
			t.Errorf("field %q is in BOTH reconStateFingerprinted and reconStateDeclined", name)
		case !in && !out:
			t.Errorf("field %q is in neither reconStateFingerprinted nor reconStateDeclined — "+
				"a key that moves it would be judged by nothing", name)
		}
	}
	for name := range reconStateFingerprinted {
		if !seen[name] {
			t.Errorf("reconStateFingerprinted names %q, which the screen no longer has", name)
		}
	}
	for name := range reconStateDeclined {
		if !seen[name] {
			t.Errorf("reconStateDeclined names %q, which the screen no longer has", name)
		}
	}
}

// reconFocusFingerprinted is every input whose FOCUS reconState carries.
// Derived against the struct, because a caret lives inside a textinput rather
// than beside it: the field-name check above cannot see focus at all.
var reconFocusFingerprinted = map[string]bool{
	"scan": true, "counts": true, "notes": true, "open": true, "parked": true,
}

func TestReconcileScreen_EveryInputFocusIsFingerprinted(t *testing.T) {
	typ := reflect.TypeOf(LocationReconcileScreen{})
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
		if !reconFocusFingerprinted[f.Name] {
			t.Errorf("field %q holds a textinput whose focus reconState does not carry — "+
				"a key that only moves the caret into it would be judged by nothing", f.Name)
		}
	}
	if len(found) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}
	for name := range reconFocusFingerprinted {
		if !found[name] {
			t.Errorf("reconFocusFingerprinted names %q, which is no longer a textinput", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The bar names exactly the keys that work
// ---------------------------------------------------------------------------

// reconBarKeyNames maps a bar entry's Key to the keystrokes it SPELLS, and to
// no synonyms. Credit for a synonym would be the sweep making a claim on the
// bar's behalf — the defect it exists to report, sitting inside the check.
//
// Every token here is read LITERALLY, which is also what keeps the convention
// true: a bar drawing "R" for a key that is really "r" would be spelling a
// keystroke nobody presses.
var reconBarKeyNames = map[string][]string{
	"Enter":     {"enter"},
	"Esc":       {"esc"},
	"Enter/Esc": {"enter", "esc"},
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
	"Home/End":  {"home", "end"},
	"←→":        {"left", "right"},
	"Ctrl+E":    {"ctrl+e"},
	"Ctrl+R":    {"ctrl+r"},
	"r":         {"r"},
}

func reconNamedKeys(t *testing.T, bar []actionBarItem) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	for _, it := range bar {
		keys, ok := reconBarKeyNames[it.Key]
		if !ok {
			t.Fatalf("bar entry %q is not in reconBarKeyNames — add it so the rule covers it", it.Key)
		}
		for _, k := range keys {
			named[k] = true
		}
	}
	return named
}

// reconHarness serves one fake for a whole subtest and hands back a builder that
// opens a fresh screen against it, so a probe keystroke cannot leak into the
// next assertion and the sweep does not stand up a server per keypress.
func reconHarness(t *testing.T, grid *omsapi.LocationReconcileGrid, width, height int) func(*testing.T) (Root, *LocationReconcileScreen) {
	t.Helper()
	fake := &reconFake{grid: grid}
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	return func(t *testing.T) (Root, *LocationReconcileScreen) {
		t.Helper()
		s := NewLocationReconcileScreen(deps, "7", "Machine shop mezzanine")
		r := newTestRoot(s)
		r.deps = deps
		next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		return next.(Root), s
	}
}

// reconGridFor is the room a phase case is reached through. The BLOCKED case
// needs an empty one; every other case needs items to count.
func reconGridFor(c reconPhaseCase) *omsapi.LocationReconcileGrid {
	if c.phase == reconBlocked {
		return &omsapi.LocationReconcileGrid{LocationID: "7", LocationName: "Machine shop mezzanine"}
	}
	return reconGridFixture()
}

// reconPaneSizes are the pane heights every state is checked at. 24 is the
// terminal this interface is modelled on and the one that clips; 30 is there so
// a frame cannot be tuned for the short pane.
var reconPaneSizes = []int{24, 30}

// TestReconcile_EveryPhaseNamesExactlyTheKeysThatWork presses the whole key
// space at every phase, in both directions: a bar token must name something
// that works, and a key the bar does not name must not act.
//
// THE TWO DIRECTIONS ARE ASKED AT DIFFERENT GRANULARITIES, and getting that
// wrong reports screens that are honest. FORWARD is asked per TOKEN, because a
// token names a PAIR — "PgUp/PgDn" claims that paging works here, not that
// either key works from wherever the cursor happens to rest, and at the top of a
// list PgUp correctly clamps. REVERSE is asked per KEY from the resting
// position, because "a key that acts must be named" cannot be answered about a
// pair.
//
// Tab and Shift-Tab are the recorded exception (poFormNavAliases): they ride
// alongside Up/Down on a sheet WITH FIELDS, and roughly twenty columnar forms in
// this program name that pair as UP/DN. Naming the alias here alone would make
// this screen disagree with every other form. The exemption is gated on the bar
// really naming UP/DN, so a frame with no field cursor is not excused.
func TestReconcile_EveryPhaseNamesExactlyTheKeysThatWork(t *testing.T) {
	space := poKeySpace()
	for _, c := range reconPhaseCases() {
		for _, height := range reconPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := reconHarness(t, reconGridFor(c), 80, height)
				fresh := func(t *testing.T) (Root, *LocationReconcileScreen) {
					t.Helper()
					r, s := build(t)
					r = c.reach(t, r, s)
					if s.phase != c.phase {
						t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
					}
					return r, s
				}
				// acts presses ONE key against a freshly reached state and says
				// whether anything the operator could act on moved.
				acts := func(k string) bool {
					pr, ps := fresh(t)
					before := reconState(ps)
					_, cmd := pr.Update(poPhaseKeyMsg(k))
					return reconState(ps) != before || poCmdActs(cmd)
				}

				_, screen := fresh(t)
				bar := screen.bar()
				named := reconNamedKeys(t, bar)

				// FORWARD, per token.
				for _, it := range bar {
					moved := false
					for _, k := range reconBarKeyNames[it.Key] {
						if acts(k) {
							moved = true
						}
					}
					if !moved {
						t.Errorf("%s names %q (%s) and none of the keys it spells changes "+
							"anything (bar: %+v)", c.name, it.Key, it.Label, bar)
					}
				}

				// REVERSE, per key.
				for _, k := range space {
					if named[k] {
						continue
					}
					if c.typing && (poIsPrintable(k) || poFieldKeys[k]) {
						continue
					}
					if poFormNavAliases[k] && named["up"] {
						continue
					}
					if acts(k) {
						t.Errorf("%s does not name %q, but pressing it acts (bar: %+v)",
							c.name, k, bar)
					}
				}
			})
		}
	}
}

// reconSilentKeys records, per phase case, the keys that answer with NOTHING
// from that case's resting position — and why that is honest rather than the
// reported hang.
//
// Every entry must be a NAMED key resting against an edge it cannot move past,
// where the highlight (or the absent marker) is already on the pane saying so.
// A key that is NOT named and is silent is the defect, and the sweep below fails
// on it — as does an entry here that is no longer silent, because an exception
// that outlives its behaviour excuses whatever falls into its place next.
//
// IT IS EMPTY, and that is a fact about this screen rather than an oversight.
// Every arm that declines answers: a movement key stopped at an edge says which
// edge, a page with nothing to page says every row is already on the pane, and
// an unbound key is declined by name. The only silence on this screen is a
// REFUSED PANE, where the arm's whole product is a position nothing draws — and
// a refused pane is not a state this sweep's heights reach.
var reconSilentKeys = map[string]map[string]string{}

// TestReconcile_NoTwoDecliningKeysRedrawTheSamePane is the other half of "a
// keypress that changes nothing visible IS the reported bug".
//
// A decline is a PURE note write — the fingerprint sweep above proves it moves
// nothing else — so pressing key A then key B leaves the pane B alone would have
// produced. Two declining keys that share a sentence therefore redraw each
// other's pane byte for byte, which from the operator's seat is a program that
// has stopped responding. Requiring the key→pane map to be INJECTIVE is that
// property checked over the whole key space at once.
//
// It runs on the NON-TYPING cases. On a phase with a focused field the caret is
// on the pane and the field owns the keys the sweep would otherwise press, so
// the property is neither true nor meaningful there.
func TestReconcile_NoTwoDecliningKeysRedrawTheSamePane(t *testing.T) {
	space := poKeySpace()
	for _, c := range reconPhaseCases() {
		if c.typing {
			continue
		}
		for _, height := range reconPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := reconHarness(t, reconGridFor(c), 80, height)
				fresh := func(t *testing.T) (Root, *LocationReconcileScreen) {
					t.Helper()
					r, s := build(t)
					r = c.reach(t, r, s)
					if s.phase != c.phase {
						t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
					}
					return r, s
				}
				base, screen := fresh(t)
				named := reconNamedKeys(t, screen.bar())
				resting := base.View()

				panes := map[string]string{}
				// A recorded exception that is no longer silent is as much a
				// defect as an unrecorded one: it excuses a key that has since
				// been given an answer, and the next silent key in its place
				// would inherit the excuse. Tracked and checked below.
				usedExcuse := map[string]bool{}
				for _, k := range space {
					r, s := fresh(t)
					before := reconState(s)
					next, cmd := r.Update(poPhaseKeyMsg(k))
					r = next.(Root)
					if reconState(s) != before || poCmdActs(cmd) {
						continue // it acted; this sweep is about the ones that do not
					}
					after := r.View()
					if after == resting {
						why, ok := reconSilentKeys[c.name][k]
						switch {
						case !named[k]:
							t.Errorf("%s: %q is not named by the bar and answers with nothing — "+
								"the pane comes back byte for byte, which reads as a wedged program", c.name, k)
						case !ok:
							t.Errorf("%s: %q is named by the bar and answers with nothing, and is "+
								"not recorded in reconSilentKeys", c.name, k)
						default:
							usedExcuse[k] = true
							_ = why
						}
						continue
					}
					if prev, clash := panes[after]; clash {
						t.Errorf("%s: %q and %q both decline with the SAME pane, so pressing "+
							"one after the other redraws it byte for byte", c.name, prev, k)
					}
					panes[after] = k
				}
				for k := range reconSilentKeys[c.name] {
					if !usedExcuse[k] {
						t.Errorf("%s: %q is recorded in reconSilentKeys but it is not silent "+
							"here any more — the exception outlived the behaviour it excused",
							c.name, k)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Every body line belongs to a navigable row, and a row's lines are ONE run
// ---------------------------------------------------------------------------

// reconBodyStates builds every phase's body directly, without a server: this is
// a structural property of what the builders emit, and standing a fake up per
// case would buy nothing.
//
// The set is checked against the reconPhase iota below, so a phase added to the
// iota fails here until its body is judged too.
func reconBodyStates() map[reconPhase]func() *LocationReconcileScreen {
	return map[reconPhase]func() *LocationReconcileScreen{
		reconLoading: func() *LocationReconcileScreen {
			return NewLocationReconcileScreen(Deps{}, "7", "Machine shop mezzanine")
		},
		reconBlocked: func() *LocationReconcileScreen {
			s := NewLocationReconcileScreen(Deps{}, "7", "Machine shop mezzanine")
			s.Update(reconGridMsg{grid: &omsapi.LocationReconcileGrid{
				LocationID: "7", LocationName: "Machine shop mezzanine",
			}})
			return s
		},
		reconCount: func() *LocationReconcileScreen { return reconCountedFixture() },
		reconRow: func() *LocationReconcileScreen {
			s := reconCountedFixture()
			// The OPEN/CLOSED row, because its sheet is the only one with the
			// fourth field: a fixture on any other row judges the builder in the
			// shape where its own conditional is inert.
			s.focused = reconRowFirstItem + 2
			s.openRowDetail(0)
			return s
		},
		reconReview: func() *LocationReconcileScreen {
			s := reconCountedFixture()
			s.openReview(0)
			return s
		},
		reconDone: func() *LocationReconcileScreen { return reconDoneFixture() },
	}
}

// TestReconcile_EveryBodysRowsAreOneContiguousRun holds the property
// jdeLines.block() depends on and that no rendering test can see.
//
// block() spans a row's FIRST line to its LAST, so a row whose lines are
// INTERLEAVED with another row's swallows everything between them: the window
// anchored on that row draws its neighbours as part of it, and the sacrifice
// order the builder wrote is not the one the pane applies. The row sheet had
// exactly that — every field emitted first and the caveats hung off the end — and
// it also DREW the sentence explaining the Reorder row underneath Notes, which
// is the half a reader notices.
func TestReconcile_EveryBodysRowsAreOneContiguousRun(t *testing.T) {
	for phase, mk := range reconBodyStates() {
		t.Run(phase.String(), func(t *testing.T) {
			s := mk()
			s.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
			body, _ := s.body()
			if body.Len() == 0 {
				t.Fatalf("%v draws an empty body, so this check asserts nothing", phase)
			}
			seen := map[int]bool{}
			done := map[int]bool{}
			prev := -2
			for i := 0; i < body.Len(); i++ {
				row := body.row[i]
				if row != prev {
					if done[row] {
						t.Fatalf("%v: row %d's lines are interleaved with another row's — "+
							"jdeLines.block would span both, so the window anchored on it "+
							"draws its neighbour as part of it\nbody:\n%s",
							phase, row, strings.Join(body.text, "\n"))
					}
					if prev != -2 {
						done[prev] = true
					}
					prev = row
				}
				seen[row] = true
			}
			// And no line belongs to NO row on a cursor-anchored body: a
			// jdeNoRow line ahead of the first block is stranded the moment the
			// body overflows, with the frame drawing "↑ N more above" and every
			// key the bar names refusing to fetch it.
			if !s.scrolledPhase() && seen[jdeNoRow] {
				t.Errorf("%v tags a line to no navigable row on a cursor-anchored body:\n%s",
					phase, strings.Join(body.text, "\n"))
			}
		})
	}
}

// TestReconcile_EveryPhasesBodyIsJudged derives the roster above from the iota,
// so a phase added tomorrow brings its body with it rather than waiting for
// somebody to remember a table.
func TestReconcile_EveryPhasesBodyIsJudged(t *testing.T) {
	states := reconBodyStates()
	for p := reconPhase(0); p < reconPhaseCount; p++ {
		if _, ok := states[p]; !ok {
			t.Errorf("phase %v has no reconBodyStates entry, so nothing checks what its "+
				"body emits", p)
		}
	}
	for p := range states {
		if p >= reconPhaseCount {
			t.Errorf("reconBodyStates names %v, which is not a phase any more", p)
		}
	}
}

// TestReconcile_TheReviewsFirstRowSurvivesEveryPane is "whatever must survive
// must lead", on the frame an operator commits a room's stock from.
//
// A ONE-ROW review is effectively PINNED: jdeRowMoves is false, so the bar names
// no movement key and nothing moves the window — the block's tail is an accepted
// loss and its START is all a short pane keeps. What that start has to carry is
// HOW MANY of WHAT, and WHICH item; the reason and the delta below them are
// recoverable by going back to the grid, and a lost quantity is not.
//
// No lead is DECLARED for it (jdeLines.DeclareLead is receive_form's mechanism
// and its sweep is scoped to that screen's own phase cases). The property is
// held here instead, and structurally: reviewHeading is the first line the block
// emits, so a short pane keeps it by construction rather than by arrangement.
//
// THE TWO HALVES OF THE ROW ARE HELD TO DIFFERENT RULES, because they give
// differently and that is the design:
//
//	the QUANTITY and its UNIT never give, at any pane. A count with no unit,
//	or a unit with no count, is not a fact anybody can confirm — and a cut
//	number does not read as a shortened fact, it reads as a different one.
//	the ITEM's name ABBREVIATES, and where it does it is MARKED. At width 45
//	the pane is sixteen cells and "Nitrile gloves, powder-free, blue, medium"
//	cannot be on it; what must never happen is the name going SILENTLY.
//
// Both sides of that boundary are COUNTED, so the scoping cannot become a way of
// asserting nothing: the sweep fails if no pane ever drew the name whole and if
// no pane ever had to mark it.
//
// The panes are DERIVED from Root's own gate, because three hand-picked widths
// and two hand-picked heights is exactly how this package has missed every
// geometry defect it has shipped. Both sets are asked ONCE, outside the loops:
// each answer costs a Root render per candidate size.
func TestReconcile_TheReviewsFirstRowSurvivesEveryPane(t *testing.T) {
	const nameHead = "Nitrile"
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	whole, marked, judged := 0, 0, 0
	for _, w := range widths {
		for _, h := range heights {
			s := reconFixture(nil)
			s.Update(tea.WindowSizeMsg{Width: w, Height: h})
			// ONE counted row, which is the state with nothing to move.
			s.counts[0].SetValue("9")
			s.openReview(0)
			if rows := len(s.reviewRows()); rows != 1 {
				t.Fatalf("the fixture has %d review rows, not the pinned one", rows)
			}
			if jdeRowMoves(len(s.reviewRows())) {
				t.Fatalf("at %dx%d the one-row review still moves, so this check is not "+
					"about the state it names", w, h)
			}
			pane := clampToBox(s.View(), screenBodyWidth(w), screenBodyRows(h))
			if strings.Contains(pane, "Too short") {
				continue // the layer refused the frame; there is no body to judge
			}
			judged++
			flat := strings.Join(strings.Fields(pane), " ")
			if !strings.Contains(flat, "9 boxes") {
				t.Errorf("at %dx%d the review's first row lost the counted quantity:\n%s", w, h, pane)
			}
			switch {
			case strings.Contains(flat, nameHead):
				whole++
			case strings.Contains(flat, "…"):
				marked++
			default:
				t.Errorf("at %dx%d the review's first row dropped the item with no mark, so "+
					"the operator is confirming a quantity against nothing:\n%s", w, h, pane)
			}
		}
	}
	if judged == 0 {
		t.Fatal("every pane refused the frame, so this sweep asserted nothing at all")
	}
	if whole == 0 {
		t.Errorf("no pane of %d drew the item's name at all — the fixture cannot reach the "+
			"unclipped case, so the marked branch is passing for the wrong reason", judged)
	}
	if marked == 0 {
		t.Errorf("no pane of %d had to abbreviate the item's name, so the mark half of this "+
			"check was never exercised — the fixture's name is too short to reach the bound",
			judged)
	}
	t.Logf("%d panes judged: %d drew the name whole, %d marked it as cut", judged, whole, marked)
}
