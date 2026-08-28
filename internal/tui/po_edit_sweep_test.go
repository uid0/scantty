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

// The bar-honesty rule over the purchase-order EDIT screen, asserted as a rule
// rather than one phase at a time.
//
// This screen had no sweep of its own until a fifth phase was added to it
// (poEditPhaseDeleteLine). Every set the sweep needs is DERIVED from its
// authority, because three separate defects reached an operator's terminal in
// this package through a sweep reading a hand-kept roster the key was not in
// (AGENTS.md: `N` on the purchasing list, `tab` on the supplier picker, `enter`
// on the supplier-switch confirm):
//
//	PHASES come from the poEditPhase iota, walked to poEditPhaseCount, so a
//	       phase added tomorrow fails the build until somebody says how it is
//	       reached.
//	KEYS   come from poKeySpace() — the whole printable ASCII range plus the
//	       named specials — because no authority for "the keys a terminal can
//	       send" can be derived, so the answer is to press all of them.
//	FIELDS come from reflect over the screen struct: every one is either in the
//	       fingerprint or declared as something a key may move WITHOUT acting.
//	FOCUS  comes from reflect for textinput fields, because a caret lives INSIDE
//	       the value and the field-name check cannot see it.
//
// A DERIVED ROSTER IS ONE AXIS AND A SWEEP HAS TWO: walking the iota says
// nothing about the STATES inside a phase, and it is the states a bar changes
// shape in that carry the defect. Those are still a judgement, and the bar is
// what says which are needed — see poEditPhaseCases.

// ---------------------------------------------------------------------------
// Reaching each phase
// ---------------------------------------------------------------------------

type poEditPhaseCase struct {
	phase poEditPhase
	name  string
	// typing marks a phase where a text input owns the keyboard. There the rule
	// is about COMMAND keys only: a printable rune acts by editing the value,
	// which is what the field is FOR.
	typing bool
	// canDelete is the order's can_delete_items, which is what decides which
	// removal the status row offers — and therefore which of the two removal
	// phases is reachable at all.
	canDelete *bool
	// reach drives a freshly opened edit screen into this phase.
	reach func(t *testing.T, r Root, s *PurchaseOrderEditScreen) Root
	// bar is the legend that phase draws, read from the screen the same way its
	// own View does.
	bar func(s *PurchaseOrderEditScreen) []actionBarItem
	// probes are the extra positions a named key is tried from, on top of the
	// resting one. A key is dead only if it does nothing from ANY of them.
	//
	// They are PER CASE and not one shared list, because on a FIELD form a
	// probe changes which row the cursor is on — and this screen's rows do not
	// all obey the same rules. Probing "down" from the line editor's status row
	// wraps onto the COST row, where a printable rune goes into a focused box:
	// every unnamed letter then reads as acting, and the sweep reports 96
	// violations of a rule it never actually tested. Where the cursor WRAPS
	// (the two field forms) the resting position already reaches every named
	// key; where it CLAMPS (the association picker's list, and the form's
	// paging pair) a second position is what proves the key is not dead.
	probes [][]string
}

// poEditPhasesWithoutKeys records a phase where NO key acts, so "no entry" and
// "nothing to check" cannot look the same. Empty today; an entry needs a reason.
var poEditPhasesWithoutKeys = map[poEditPhase]string{}

// poEditDown presses Down n times.
func poEditDown(n int) func(*testing.T, Root, *PurchaseOrderEditScreen) Root {
	return func(t *testing.T, root Root, _ *PurchaseOrderEditScreen) Root {
		t.Helper()
		for i := 0; i < n; i++ {
			root = key(t, root, tea.KeyMsg{Type: tea.KeyDown})
		}
		return root
	}
}

// poEditToAssocRow walks to one of the two order-level association rows. The
// option lists those rows pick from are seeded by the harness — see poEditHarness.
func poEditToAssocRow(row int) func(*testing.T, Root, *PurchaseOrderEditScreen) Root {
	return func(t *testing.T, r Root, s *PurchaseOrderEditScreen) Root {
		t.Helper()
		return poEditDown(poEditMetaCount+row)(t, r, s)
	}
}

// poEditToLineRow walks to the first line row and opens its editor, then walks
// the line editor's cursor to `row`. Both walks are BOUNDED (AGENTS.md): a loop
// that pressed until the focus arrived would turn a declined key into a hang,
// and the package would then fail by timing out with whichever test happened to
// be running named in the panic.
func poEditToLineRow(row int) func(*testing.T, Root, *PurchaseOrderEditScreen) Root {
	return func(t *testing.T, r Root, s *PurchaseOrderEditScreen) Root {
		t.Helper()
		for i := 0; i < poEditLineBase; i++ {
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
		if s.phase != poEditPhaseLine {
			t.Fatalf("Ctrl-E on a line row opened phase %v, want the line editor", s.phase)
		}
		for i := 0; i < poLineEditCount && s.lineFocus != row; i++ {
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
		}
		if s.lineFocus != row {
			t.Fatalf("the line editor's cursor did not reach row %d (lineFocus %d)", row, s.lineFocus)
		}
		return r
	}
}

// then chains a reach onto another one.
func poEditThen(
	first func(*testing.T, Root, *PurchaseOrderEditScreen) Root,
	rest ...tea.KeyMsg,
) func(*testing.T, Root, *PurchaseOrderEditScreen) Root {
	return func(t *testing.T, r Root, s *PurchaseOrderEditScreen) Root {
		t.Helper()
		r = first(t, r, s)
		for _, k := range rest {
			r = key(t, r, k)
		}
		return r
	}
}

// poEditPhaseCases is every phase, in the states its bar changes shape in.
//
// The form's bar has four shapes (a plain text row, a choice row, an
// association row, a line row) and the line editor's has four more, because
// Ctrl-E means something different on each of its rows — and on the STATUS row
// it means one of three things, or nothing at all, depending on an answer that
// comes off the wire. Those three are the states this whole change is about.
func poEditPhaseCases() []poEditPhaseCase {
	form := func(s *PurchaseOrderEditScreen) []actionBarItem { return s.formBar(s.formLines()) }
	line := func(s *PurchaseOrderEditScreen) []actionBarItem { return s.lineBar() }
	del := func(s *PurchaseOrderEditScreen) []actionBarItem { return s.deleteBar() }
	void := func(s *PurchaseOrderEditScreen) []actionBarItem {
		return []actionBarItem{{"Enter", "Void line"}, {"Esc", "Cancel"}}
	}
	assoc := func(s *PurchaseOrderEditScreen) []actionBarItem { return poEditAssocBar }
	ctrlE := tea.KeyMsg{Type: tea.KeyCtrlE}
	yes, no := boolPtr(true), boolPtr(false)

	return []poEditPhaseCase{
		// The form's cursor wraps, so every named key but the PAGING pair acts
		// from the resting row; PgUp clamps at the top, so the text-row case
		// carries one probe that steps off it — onto another text row, so the
		// case's own state is unchanged.
		{poEditPhaseForm, "form (text row)", true, no,
			func(t *testing.T, r Root, _ *PurchaseOrderEditScreen) Root { return r },
			form, [][]string{{"down"}}},
		{poEditPhaseForm, "form (choice row)", false, no,
			poEditDown(poMetaPriority), form, nil},
		{poEditPhaseForm, "form (association row)", false, no,
			poEditToAssocRow(poAssocRowWorkOrder), form, nil},
		{poEditPhaseForm, "form (line row)", false, no,
			poEditDown(poEditLineBase), form, nil},

		{poEditPhaseLine, "line editor (cost row)", true, no,
			poEditToLineRow(poLineEditCost), line, nil},
		{poEditPhaseLine, "line editor (notes row)", true, no,
			poEditToLineRow(poLineEditNotes), line, nil},
		{poEditPhaseLine, "line editor (work-order row)", false, no,
			poEditToLineRow(poLineRowWorkOrder), line, nil},
		// The three states of the STATUS row — the one row on this screen whose
		// key is chosen by the server rather than by where the cursor is, and
		// the reason this sweep exists. A phase's cases must span every state
		// its bar changes shape in; here the bar changes shape three ways off
		// one boolean that is not even a boolean.
		{poEditPhaseLine, "line editor (status row, deletable)", false, yes,
			poEditToLineRow(poLineRowStatus), line, nil},
		{poEditPhaseLine, "line editor (status row, voidable)", false, no,
			poEditToLineRow(poLineRowStatus), line, nil},
		{poEditPhaseLine, "line editor (status row, flag absent)", false, nil,
			poEditToLineRow(poLineRowStatus), line, nil},

		{poEditPhaseVoidLine, "void prompt", true, no,
			poEditThen(poEditToLineRow(poLineRowStatus), ctrlE), void, nil},
		{poEditPhaseDeleteLine, "delete confirm", false, yes,
			poEditThen(poEditToLineRow(poLineRowStatus), ctrlE), del, nil},
		// A CLAMPING list: the cursor cannot move up from the top, so the
		// resting position alone would report Up as dead.
		{poEditPhaseAssoc, "association picker", false, no,
			poEditThen(poEditToAssocRow(poAssocRowWorkOrder), ctrlE), assoc,
			[][]string{{"down"}, {"end"}}},
	}
}

// TestPOEdit_EveryPhaseIsSwept walks the poEditPhase iota to its sentinel and
// fails on any phase with no case. This is what makes the coverage below a
// property of the code rather than of who last remembered to extend a table.
func TestPOEdit_EveryPhaseIsSwept(t *testing.T) {
	covered := map[poEditPhase]bool{}
	for _, c := range poEditPhaseCases() {
		covered[c.phase] = true
	}
	for p := poEditPhase(0); p < poEditPhaseCount; p++ {
		if covered[p] {
			if why, ok := poEditPhasesWithoutKeys[p]; ok {
				t.Errorf("phase %v is both swept and recorded as keyless (%q)", p, why)
			}
			continue
		}
		if _, ok := poEditPhasesWithoutKeys[p]; !ok {
			t.Errorf("phase %v has no poEditPhaseCases entry and is not recorded in "+
				"poEditPhasesWithoutKeys — a key on it would be judged by nothing", p)
		}
	}
}

// ---------------------------------------------------------------------------
// The state a key may move
// ---------------------------------------------------------------------------

// poEditState is the observable state of the screen. Text inputs contribute
// their VALUE and their FOCUS, never their View: a blinking caret moving is not
// a key acting, and a fingerprint a caret can move is one any keypress passes.
func poEditState(s *PurchaseOrderEditScreen) string {
	var b strings.Builder
	fmt.Fprint(&b, s.phase, "|", s.cursor, s.lineFocus, s.editLineIdx, s.subReturn,
		"|", s.saving, s.loading, s.errMsg, s.loadErr,
		"|", s.lineCostShown, s.lineCostConfirmed, s.origOrderDate,
		"|", s.assocField, s.assocLineIdx, s.assocCursor, len(s.assocRows),
		"|", s.voidReason.Value(), s.voidReason.Focused(),
		"|", s.terminalWidth, s.terminalHeight)
	for i := range s.meta {
		fmt.Fprint(&b, "|m", i, ":", s.meta[i].Value(), s.meta[i].Focused())
	}
	for i := range s.lineInputs {
		fmt.Fprint(&b, "|l", i, ":", s.lineInputs[i].Value(), s.lineInputs[i].Focused())
	}
	// In ROW order. Ranging the map would make the fingerprint depend on Go's
	// randomised iteration, so every key would compare unequal to itself and
	// the whole sweep would report every unnamed key as acting — a check that
	// fails for a reason unrelated to the property it names.
	for row := 0; row < poEditMetaCount; row++ {
		if sel, ok := s.selects[row]; ok {
			fmt.Fprint(&b, "|s", row, ":", sel.idx, sel.original)
		}
	}
	if s.po != nil {
		fmt.Fprint(&b, "|po:", s.po.Status, len(s.po.Items))
	}
	return b.String()
}

// poEditStateFingerprinted / poEditStateDeclined classify EVERY field of the
// screen. The struct is enumerated by reflection, so a field added tomorrow
// fails by name until somebody decides which half it belongs in — the omission
// cannot be made quietly, which is the whole point.
var poEditStateFingerprinted = map[string]bool{
	"jdeScreen": true, "phase": true, "subReturn": true, "cursor": true,
	"meta": true, "selects": true, "origOrderDate": true,
	"lineInputs": true, "lineFocus": true, "editLineIdx": true,
	"lineCostShown": true, "lineCostConfirmed": true,
	"voidReason": true,
	"assocField": true, "assocLineIdx": true, "assocRows": true, "assocCursor": true,
	"po": true, "loading": true, "loadErr": true, "saving": true, "errMsg": true,
}

// poEditStateDeclined is every field a key may move WITHOUT having acted, with
// the reason.
var poEditStateDeclined = map[string]string{
	"deps":     "injected dependencies; no keystroke reaches them",
	"poID":     "the order being edited, fixed at construction",
	"lastPaid": "a per-item price cache filled by replies off the wire, not by keys",
	"assoc":    "the association OPTION lists, loaded in the background and never keyed",
	"deleteNote": "the delete confirm's answer to the last keypress — a decline writes here, " +
		"and counting it would call \"j does nothing here\" an action and invert the rule this " +
		"sweep enforces. That the answers are non-silent and DISTINGUISHABLE is held one file " +
		"over, in sequence, by TestPOLineRemove_NoTwoDeclinedKeysRedrawOneFrame.",
}

func TestPOEditScreen_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(PurchaseOrderEditScreen{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		_, in := poEditStateFingerprinted[name]
		_, out := poEditStateDeclined[name]
		switch {
		case in && out:
			t.Errorf("field %q is in BOTH poEditStateFingerprinted and poEditStateDeclined", name)
		case !in && !out:
			t.Errorf("field %q is in neither poEditStateFingerprinted nor poEditStateDeclined — "+
				"a key that moves it would be judged by nothing", name)
		}
	}
	for name := range poEditStateFingerprinted {
		if !seen[name] {
			t.Errorf("poEditStateFingerprinted names %q, which the screen no longer has", name)
		}
	}
	for name := range poEditStateDeclined {
		if !seen[name] {
			t.Errorf("poEditStateDeclined names %q, which the screen no longer has", name)
		}
	}
}

// poEditFocusFingerprinted is every input whose FOCUS poEditState carries.
// Derived against the struct, because a caret lives inside a textinput rather
// than beside it: the field-name check above cannot see focus at all, and a key
// that only moves the caret INTO a field was invisible to every fingerprint in
// this package until somebody noticed.
var poEditFocusFingerprinted = map[string]bool{
	"meta": true, "lineInputs": true, "voidReason": true,
}

func TestPOEditScreen_EveryInputFocusIsFingerprinted(t *testing.T) {
	typ := reflect.TypeOf(PurchaseOrderEditScreen{})
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
		if !poEditFocusFingerprinted[f.Name] {
			t.Errorf("field %q holds a textinput whose focus poEditState does not carry — "+
				"a key that only moves the caret into it would be judged by nothing", f.Name)
		}
	}
	if len(found) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}
	for name := range poEditFocusFingerprinted {
		if !found[name] {
			t.Errorf("poEditFocusFingerprinted names %q, which is no longer a textinput", name)
		}
	}
}

// ---------------------------------------------------------------------------
// The bar names exactly the keys that work
// ---------------------------------------------------------------------------

// poEditHarness serves one fake for a whole subtest and hands back a builder
// that opens a fresh edit screen against it, so a probe keystroke cannot leak
// into the next assertion and the sweep does not stand up a server per press.
func poEditHarness(t *testing.T, canDelete *bool, width, height int) func(*testing.T) (Root, *PurchaseOrderEditScreen) {
	t.Helper()
	fake := poRemoveOrder("draft", canDelete)
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	return func(t *testing.T) (Root, *PurchaseOrderEditScreen) {
		t.Helper()
		po, err := deps.OMS.GetPurchaseOrder(context.Background(), "po-1")
		if err != nil {
			t.Fatalf("fixture load: %v", err)
		}
		s := NewPurchaseOrderEditScreen(deps, po)
		// The association OPTION lists, which belong to no supplier and no
		// order and load in the background from Init. The fake serves no
		// endpoint for them, so they are seeded here: without them Ctrl-E on an
		// association row can only answer "no work orders available to pick",
		// and every claim the bar makes about that key would be a claim about
		// an empty list rather than about the row.
		//
		// The UN-SEEDED state — the bar going on naming Ctrl-E while the list
		// is empty or its load failed — is a PRE-EXISTING gap on this screen,
		// older than this sweep and unrelated to line removal. Recorded here
		// rather than fixed: whether such a row should drop the key or open a
		// picker that says why it is empty is a decision, not a patch.
		s.assoc.workOrders = []omsapi.WorkOrder{
			{ID: "wo-1", DisplayTitle: "Lathe teardown and spindle rebuild"},
			{ID: "wo-2", DisplayTitle: "Mill way-cover replacement"},
			{ID: "wo-3", DisplayTitle: "Compressor annual service"},
		}
		s.assoc.committees = []omsapi.SIG{{ID: 1, Name: "Metal shop"}, {ID: 2, Name: "Wood shop"}}
		r := newTestRoot(s)
		r.deps = deps
		next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		return next.(Root), s
	}
}

// poEditNamedKeys reads a bar into the set of keystrokes it CLAIMS, through the
// ONE table every purchasing sweep shares. A Key it does not know FAILS rather
// than being skipped: credit for a synonym would be the sweep making a claim on
// the bar's behalf, which is the defect it exists to report.
func poEditNamedKeys(t *testing.T, bar []actionBarItem) map[string]bool {
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

// poEditPaneSizes are the pane heights every state is checked at. 24 is the
// terminal this interface is modelled on and the one that clips; 30 is there so
// a frame cannot be tuned for the short pane.
var poEditPaneSizes = []int{24, 30}

// TestPOEdit_EveryPhaseNamesExactlyTheKeysThatWork presses the whole key space
// at every phase, in both directions: a key the bar names must change
// something, and a key it does not name must not.
//
// A named key is probed from several positions, because a cursor at the top of
// a body cannot move up: a key is dead only if it does nothing from ANY of them.
//
// The reverse direction asks for no STATE change rather than no COMMAND,
// because a declining key is meant to answer with a sentence and change nothing
// — that answer is the rule this screen keeps, not a key that escaped the audit.
func TestPOEdit_EveryPhaseNamesExactlyTheKeysThatWork(t *testing.T) {
	space := poKeySpace()
	for _, c := range poEditPhaseCases() {
		probes := append([][]string{nil}, c.probes...)
		for _, height := range poEditPaneSizes {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := poEditHarness(t, c.canDelete, 80, height)
				fresh := func(t *testing.T, probe []string) (Root, *PurchaseOrderEditScreen) {
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
				bar := c.bar(screen)
				named := poEditNamedKeys(t, bar)

				for _, k := range space {
					if c.typing && (poIsPrintable(k) || poFieldKeys[k]) && !named[k] {
						continue
					}
					if c.typing && poFormNavAliases[k] {
						continue
					}
					// Tab/Shift-Tab ride alongside Up/Down on every columnar
					// form in the program and no bar names them (poFormNavAliases).
					if poFormNavAliases[k] && !named[k] {
						continue
					}
					if poEditSelectAliases[k] && !named[k] && s0IsSelectRow(screen) {
						continue
					}
					acted := false
					for _, probe := range probes {
						pr, ps := fresh(t, probe)
						before := poEditState(ps)
						_, cmd := pr.Update(poPhaseKeyMsg(k))
						if poEditState(ps) != before || poCmdActs(cmd) {
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

// poEditSelectAliases is SPACE on a "< value >" choice row, where it cycles the
// value alongside ←→ and no bar in the program spells it.
//
// Recorded rather than omitted, which is the whole difference: an omission is
// silent and this is a statement with a reason. The alias is app-wide — the
// electrical, webhook and site-settings sheets all bind it on their own choice
// rows and none of them names it — so spelling it on the purchase-order header
// terms alone would make this sheet disagree with every other choice row in the
// program, and spelling it everywhere is a change to all of them that nobody
// has asked for. It is the same deviation poFormNavAliases records for Tab.
var poEditSelectAliases = map[string]bool{" ": true}

// s0IsSelectRow reports that the screen is resting on a choice row, which is
// the only place the alias above applies.
func s0IsSelectRow(s *PurchaseOrderEditScreen) bool {
	return s.phase == poEditPhaseForm && s.isSelectRow(s.cursor)
}
