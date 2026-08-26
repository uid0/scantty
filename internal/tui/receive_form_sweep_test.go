package tui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	bubblekey "github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// The rules the receiving flow is held to, asserted as RULES rather than one
// entry at a time — and every set they need is DERIVED from its authority, so
// an omission fails the build instead of failing silently.
//
// This is po_add_line_sweep_test.go's architecture applied to the screen the
// columnar conversion just reached, and for the same reason: three keys reached
// an operator's terminal in this package doing nothing, and every time, the
// sweep that existed to stop it was reading a hand-kept roster the key was not
// in (AGENTS.md: `N` on the purchasing list, `tab` on the supplier picker,
// `enter` on the supplier-switch confirm). So here:
//
//	PHASES come from the receivePhase iota, walked to receivePhaseCount.
//	KEYS   come from poKeySpace() — every printable ASCII rune plus the named
//	       specials — because no authority for "the keys a terminal can send"
//	       can be derived, so the answer is to press the whole space.
//	FIELDS come from reflect over the screen struct: every one is either in the
//	       fingerprint or declared as something a key may move WITHOUT acting.
//	FOCUS  comes from reflect for textinput fields, because a caret lives INSIDE
//	       the value rather than beside it and the field-name check cannot see it.

// ---------------------------------------------------------------------------
// The order each state is reached through
// ---------------------------------------------------------------------------

// receiveSweepGateway is what a failing request answers with: a gateway page
// rather than a DRF envelope, because omsapi.parseError puts the ENTIRE raw
// body into APIError.Message when the envelope carries no code — so this is
// what really reaches the failure detail, and the detail's height is the only
// thing left that varies the pinned header.
const receiveSweepGateway = "<html><head><title>502 Bad Gateway</title></head>" +
	"<body><center><h1>502 Bad Gateway</h1></center><hr><center>nginx</center></body></html>"

// receiveSweepLines is the order every state is reached against: one plain
// line, one SERIALIZED line (so a receipt opens capture), one KIT with
// serialized components (so its caveat, its credit block and its
// component-identity capture are all on the pane while the keys are pressed),
// and one line already CLOSED SHORT (so the read-only tail exists). The kit is
// also what makes the body tall enough to page at 24 rows, which is the only
// state where PgUp/PgDn are named.
func receiveSweepLines() []omsapi.ReceivingLine {
	closed := receiveWSLine(15, "Backordered gasket", 6, 2)
	closed.IsClosedShort, closed.IsSettled = true, true
	closed.ReceiptState, closed.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
	closed.ClosedShortReason = "backorder cancelled"
	closed.QuantityPending = 0
	serialized := receiveWSSerialized(14, "Serialized controller board", 2, 0)
	// One unit is already on the shelf with no serial against it, so
	// serials_outstanding is non-zero somewhere in every swept state — the
	// figure the lifted kit ban was replaced by, and one no case reached before.
	serialized.SerialsOutstanding = 1
	serialized.SerialGap = []omsapi.SerialGapRow{
		{Item: serialized.Item, ItemName: serialized.Label, Expected: 1, Recorded: 0, Outstanding: 1},
	}
	return []omsapi.ReceivingLine{
		receiveWSKit(11, "Eufy printer maintenance kit (CMYK + cleaning)", 2, 0),
		receiveWSLine(12, "Box of M3 bolts", 4, 0),
		serialized,
		closed,
	}
}

// receiveSettle runs a command chain the way the bubbletea runtime would, and
// is this sweep's `key` — the shared pump (wo_materials_drive_test.go) with ONE
// difference, which is worth its own function because of what it costs.
//
// The shared pump abandons the textinput cursor-blink tick by WAITING IT OUT:
// it starts the command, gives it 200ms, and gives up. That is correct and it
// is what its own comment says it does. It is also 200ms of dead wall-clock per
// drive, and this screen focuses an input after every async reply — so the
// sweep paid it on every one of its several thousand rebuilds, and one
// non-typing state alone took seventy seconds.
//
// The blink is abandoned one step EARLIER here: `textinput.Blink` returns its
// message immediately and cheaply, and it is only FEEDING that message back to
// Update that makes bubbles start the 530ms tick nobody waits for. So the chain
// stops at the blink instead of at a stopwatch. Nothing else changes: every
// other message is fed back exactly as the runtime would feed it, so the states
// these cases reach are the states an operator reaches.
//
// The shared pump is deliberately left alone. Shortening ITS budget would speed
// this sweep up by making every other drive in the package racier on a loaded
// machine, which is a bad trade for a test that is not about timing.
func receiveSettle(t *testing.T, r Root, cmd tea.Cmd, depth int) Root {
	t.Helper()
	if cmd == nil || depth > 24 {
		return r
	}
	msg, ok := receiveRun(cmd)
	if !ok {
		// A command that did not answer promptly is one that only marks time —
		// bubbles' cursor tick is 530ms. The bound is a backstop rather than the
		// mechanism: the blink is normally recognised and dropped below, before
		// anything can start ticking. Every command these drives really wait on
		// is an in-process httptest round trip.
		return r
	}
	if msg == nil || receiveIsBlink(msg) {
		return r
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			r = receiveSettle(t, r, c, depth+1)
		}
		return r
	}
	next, nextCmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return receiveSettle(t, after, nextCmd, depth+1)
}

// receiveRun runs one command, refusing to wait on a timer. ok is false when it
// did not answer.
func receiveRun(cmd tea.Cmd) (tea.Msg, bool) {
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		return msg, true
	case <-time.After(200 * time.Millisecond):
		return nil, false
	}
}

// receiveType sends one keystroke and does NOT settle what it returns.
//
// Typing is synchronous: the screen updates its own textinput inside Update, so
// the value and the frame are already correct when this returns. The only
// command a typed rune produces is the cursor's own blink tick, and RUNNING
// that is a flat 530ms — which is why a 150-keystroke typing test that settled
// every press took four minutes and this one takes no measurable time. Nothing
// is skipped that a drive needs; a key that starts real work goes through
// receiveKey.
func receiveType(t *testing.T, r Root, msg tea.KeyMsg) Root {
	t.Helper()
	next, _ := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after
}

// receiveIsBlink reports a cursor-blink message. bubbles keeps those types
// unexported, so this matches on the type NAME — which is checked rather than
// trusted: TestReceive_TheBlinkIsWhatTheDriveSkips fails if the message
// textinput.Blink produces stops matching, so a bubbles upgrade that renamed it
// could not silently turn this into a drive that skips nothing.
func receiveIsBlink(msg tea.Msg) bool {
	return strings.Contains(strings.ToLower(fmt.Sprintf("%T", msg)), "blink")
}

func TestReceive_TheBlinkIsWhatTheDriveSkips(t *testing.T) {
	if !receiveIsBlink(textinput.Blink()) {
		t.Fatalf("textinput.Blink now produces %T, which receiveIsBlink does not match — "+
			"the sweep would be waiting out a tick again, or worse, skipping a real message",
			textinput.Blink())
	}
	if receiveIsBlink(StatusMsg{}) || receiveIsBlink(receiveSubmittedMsg{}) {
		t.Error("receiveIsBlink matches a message the drive must not skip")
	}
}

// receiveKey sends one key through Root.Update and settles whatever it kicked
// off — this sweep's equivalent of the shared `key` helper.
func receiveKey(t *testing.T, r Root, msg tea.KeyMsg) Root {
	t.Helper()
	next, cmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return receiveSettle(t, after, cmd, 0)
}

// receivePhaseCase is one STATE of the flow, driven into being through
// Root.Update against the fake. Several cases may share a phase — a phase and
// the same phase with a request in flight are different states of the keyboard
// — and TestReceive_EveryPhaseIsSwept only requires that no phase has none.
type receivePhaseCase struct {
	phase receivePhase
	name  string
	// typing marks a state where a text input owns the keyboard. On those the
	// rule is about COMMAND keys only: a printable rune typed into a field acts
	// by editing the value, which is what the field is FOR, so the reverse
	// direction ("a key the bar does not name must not act") cannot apply to it.
	typing bool
	// lines is the order this state is reached against.
	lines []omsapi.ReceivingLine
	// fake is what OMS answers with while this state is reached and pressed.
	// It is per-case because a FAILED reply is a state of its own: since the
	// note block became a constant height, the failure detail is the only
	// input left that varies the pinned header, and headerRows is what the
	// paging claim is measured against.
	fake func() *receiveFake
	// heights are the pane heights this state is pressed at, nil meaning
	// receivePaneSizes. A state whose bar changes shape only on a particular
	// pane has to be swept on THAT pane, or the case reaches the state without
	// ever reaching the bar it was added for — coverage in name only.
	heights []int
	// reach drives a freshly opened form into this state.
	reach func(t *testing.T, r Root, s *ReceiveFormScreen) Root
}

// receivePhasesWithoutKeys records a phase where NO key acts, so "no entry" and
// "nothing to check" cannot look the same. It is empty today, and a phase added
// to it needs a reason.
var receivePhasesWithoutKeys = map[receivePhase]string{}

func receivePhaseCases() []receivePhaseCase {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	down := tea.KeyMsg{Type: tea.KeyDown}
	all := receiveSweepLines()

	okFake := func() *receiveFake { return &receiveFake{sheet: receiveWorksheet(all...)} }
	writeFails := func() *receiveFake {
		return &receiveFake{sheet: receiveWorksheet(all...), failWith: 502, failBody: receiveSweepGateway}
	}
	sheetFails := func() *receiveFake {
		return &receiveFake{sheet: receiveWorksheet(all...), sheetFail: 502, sheetBody: receiveSweepGateway}
	}
	unavailable := func() *receiveFake {
		w := receiveWorksheet(all...)
		w.CanReceive, w.Status, w.StatusLabel = false, "draft", "Draft"
		w.UnavailableReason = "This order is still a draft. Send it to the supplier before " +
			"receiving against it."
		return &receiveFake{sheet: w}
	}

	// pressed drives with the pump, so every request is resolved.
	pressed := func(keys ...tea.KeyMsg) func(*testing.T, Root, *ReceiveFormScreen) Root {
		return func(t *testing.T, r Root, _ *ReceiveFormScreen) Root {
			for _, k := range keys {
				r = receiveKey(t, r, k)
			}
			return r
		}
	}
	// inFlight drives with the pump up to the last key and fires THAT one with a
	// bare Update, so its request is genuinely still out.
	inFlight := func(keys ...tea.KeyMsg) func(*testing.T, Root, *ReceiveFormScreen) Root {
		return func(t *testing.T, r Root, _ *ReceiveFormScreen) Root {
			for _, k := range keys[:len(keys)-1] {
				r = receiveKey(t, r, k)
			}
			next, _ := r.Update(keys[len(keys)-1])
			return next.(Root)
		}
	}
	// typedQty fills the quantity boxes WITHOUT keystrokes, then drives. Not a
	// shortcut around the flow: what is under test is the key pressed AFTER the
	// reach, and every reach still goes through the real endpoints. Typing it a
	// rune at a time costs a cursor-blink timer per key, which this sweep pays
	// on every rebuild — and it rebuilds for every key it presses.
	typedQty := func(qty map[int]string, drive func(*testing.T, Root, *ReceiveFormScreen) Root) func(*testing.T, Root, *ReceiveFormScreen) Root {
		return func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			for i, v := range qty {
				s.qty[i].SetValue(v)
			}
			return drive(t, r, s)
		}
	}
	// onLine walks the cursor onto a line row before the rest of the drive,
	// which is what makes Ctrl+K reachable — the bar names it only where it can
	// act, so a case that never leaves the scan row never reaches that bar.
	onLine := func(i int, drive func(*testing.T, Root, *ReceiveFormScreen) Root) func(*testing.T, Root, *ReceiveFormScreen) Root {
		return func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			for s.focused != receiveRowFirstLine+i {
				r = receiveKey(t, r, down)
			}
			return drive(t, r, s)
		}
	}
	// receiveOne books one unit of the SERIALIZED line, which is what opens
	// capture. That is index 2 of receiveSweepLines.
	receiveOne := map[int]string{2: "1"}
	// receiveKit books a KIT whose components are serialized: capture against a
	// component identity, which is the path the lifted ban made live.
	receiveKit := map[int]string{0: "1"}

	return []receivePhaseCase{
		// The worksheet fetch, genuinely still out. loadSheet's command is
		// DROPPED rather than run, which is what leaves the request out: the
		// harness has already let the first fetch land, so this puts the screen
		// back into the state it opens in and keeps it there while the keys are
		// pressed.
		{phaseLoading, "loading", false, all, okFake, nil,
			func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				_ = s.loadSheet()
				return r
			}},
		// The fetch FAILED: could not tell. `r` acts, the rest declines.
		{phaseBlocked, "worksheet unreadable", false, all, sheetFails, nil, pressed()},
		// The fetch SUCCEEDED and said no. A different fact, a different frame.
		{phaseBlocked, "cannot receive", false, all, unavailable, nil, pressed()},

		{phaseQty, "qty", true, all, okFake, nil, pressed()},
		// The same phase with something typed: the Esc label changes to
		// "Discard & back", and the paging keys are named because the kit block
		// has grown a credit breakdown.
		{phaseQty, "qty entered", true, all, okFake, nil, typedQty(map[int]string{0: "2"}, pressed())},
		// The cursor on a LINE with an outstanding balance, which is the only
		// state Ctrl+K is named in.
		{phaseQty, "qty on a line", true, all, okFake, nil, onLine(1, pressed())},
		// A box holding a ZERO. A different STATE of the quantity phase, not a
		// different phase, which is exactly why it had to be added by hand:
		// walking the receivePhase iota makes a phase impossible to forget and
		// says nothing about the states inside one.
		{phaseQty, "qty all zero", true, all, okFake, nil, typedQty(map[int]string{1: "0"}, pressed())},
		// A code in the SCAN box: Enter means FIND here and RECEIVE everywhere
		// else, so this is a state the bar changes shape in.
		{phaseQty, "qty scanning", true, all, okFake, nil,
			func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				s.scan.SetValue("SKU-12")
				return r
			}},

		{phaseSerial, "serial", true, all, okFake, nil, typedQty(receiveOne, pressed(enter))},
		// Capture against a kit's COMPONENT identity.
		{phaseSerial, "serial on a kit", true, all, okFake, nil, typedQty(receiveKit, pressed(enter))},
		// Past the last unit: no box on the pane, and a different bar. Reached
		// the way an operator reaches it — capture every unit, which lands on
		// the review, walk back to the quantities and press Enter again, so
		// firstUncaptured has nothing to open on.
		{phaseSerial, "serial answered", false, all, okFake, nil,
			typedQty(receiveOne, func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				r = receiveKey(t, r, enter) // -> capture
				s.serialInput.SetValue("SN-1")
				r = receiveKey(t, r, enter) // last unit -> review
				r = receiveKey(t, r, esc)   // -> quantities
				return receiveKey(t, r, enter)
			})},

		{phaseReview, "review", false, all, okFake, nil,
			typedQty(map[int]string{1: "2"}, pressed(enter))},
		// FROZEN: the receipt is out and the payload has already gone. It is a
		// state of the REVIEW and of no other phase, because the review is the
		// only frame the receipt leaves from.
		{phaseReview, "review sending", false, all, okFake, nil,
			typedQty(map[int]string{1: "2"}, inFlight(enter, enter))},
		// A review reached with serials captured, so Ctrl+E is named.
		{phaseReview, "review with serials", false, all, okFake, nil,
			typedQty(receiveOne, pressed(enter, esc))},

		{phaseWriteOff, "close line short", true, all, okFake, nil,
			onLine(1, pressed(tea.KeyMsg{Type: tea.KeyCtrlK}))},
		{phaseWriteOff, "mark received", true, all, okFake, nil,
			pressed(tea.KeyMsg{Type: tea.KeyCtrlR})},
		// The write-off genuinely in flight: the confirm is frozen too, and it
		// is frozen the same way — an allow-list of esc, so a key an arm adds
		// tomorrow is frozen until somebody says otherwise.
		{phaseWriteOff, "writing off", false, all, okFake, nil,
			inFlight(tea.KeyMsg{Type: tea.KeyCtrlR}, tea.KeyMsg{Type: tea.KeyCtrlX})},

		{phaseDone, "done", false, all, okFake, nil,
			typedQty(map[int]string{1: "2"}, pressed(enter, enter))},

		// A FAILED write, standing. This is the state the header allocator's
		// failure arms were written for and the one no sweep had ever reached:
		// the failure detail is pinned in the same header as the note, so it is
		// the only input left that moves headerRows — and headerRows is what
		// qtyPagesFor measures the paging claim against, so the bar can
		// genuinely be a different bar here.
		{phaseReview, "review after a failed receipt", false, all, writeFails, receiveFailureHeights(),
			typedQty(map[int]string{1: "2"}, func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				r = receiveKey(t, r, enter) // qty -> review
				r = receiveKey(t, r, enter) // the receipt comes back failed
				if s.failDetail == "" {
					t.Fatalf("the receipt did not fail, so this case is not the state it names")
				}
				return r
			})},
	}
}

// TestReceive_EveryPhaseIsSwept walks the receivePhase iota to its sentinel and
// fails on any phase the sweeps below have no case for. This is what makes
// their coverage a property of the code rather than of who last remembered to
// extend a table.
func TestReceive_EveryPhaseIsSwept(t *testing.T) {
	covered := map[receivePhase]bool{}
	for _, c := range receivePhaseCases() {
		covered[c.phase] = true
	}
	for p := receivePhase(0); p < receivePhaseCount; p++ {
		if covered[p] {
			if why, ok := receivePhasesWithoutKeys[p]; ok {
				t.Errorf("phase %v is both swept and recorded as keyless (%q)", p, why)
			}
			continue
		}
		if _, ok := receivePhasesWithoutKeys[p]; !ok {
			t.Errorf("phase %v has no receivePhaseCases entry and is not recorded in "+
				"receivePhasesWithoutKeys — a key on it would be judged by nothing", p)
		}
	}
}

// ---------------------------------------------------------------------------
// The state a key may move
// ---------------------------------------------------------------------------

// receiveState is the observable state of the flow. Text inputs contribute
// their VALUE and their FOCUS, never their View: a blinking caret moving is not
// a key acting, and a fingerprint a caret can move is one any keypress passes.
func receiveState(s *ReceiveFormScreen) string {
	var b strings.Builder
	fmt.Fprint(&b, s.phase, "|", s.loading, s.pending, s.focused, s.rowCursor,
		"|", s.scan.Value(), s.scan.Focused(),
		"|", s.tracking.Value(), s.tracking.Focused(),
		"|", s.carrier.Value(), s.carrier.Focused(),
		"|", s.delivered.Value(), s.delivered.Focused(),
		"|", s.notes.Value(), s.notes.Focused(),
		"|", s.serialInput.Value(), s.serialInput.Focused(),
		"|", s.lotInput.Value(), s.lotInput.Focused(),
		"|", s.expiryInput.Value(), s.expiryInput.Focused(),
		"|", s.reason.Value(), s.reason.Focused(),
		"|", s.parked.Value(), s.parked.Focused(),
		"|", s.serialCursor, s.serialField, len(s.serialUnits), s.captures, s.dropped,
		"|", s.scope, s.scopeLine,
		"|", s.receipt, s.result != nil,
		"|", s.terminalWidth, s.terminalHeight, len(s.lines), len(s.closed), s.sheet != nil)
	for i := range s.qty {
		fmt.Fprint(&b, "|q", i, ":", s.qty[i].Value(), s.qty[i].Focused())
	}
	if s.po != nil {
		fmt.Fprint(&b, "|po:", len(s.po.Items))
	}
	return b.String()
}

// receiveStateFingerprinted / receiveStateDeclined classify EVERY field of the
// screen. The struct is enumerated by reflection, so a field added tomorrow
// fails by name until somebody decides which half it belongs in — the omission
// cannot be made quietly, which is the whole point.
var receiveStateFingerprinted = map[string]bool{
	"jdeScreen": true, "phase": true, "loading": true, "pending": true,
	"focused": true, "rowCursor": true,
	"scan": true, "tracking": true, "carrier": true, "delivered": true,
	"qty": true, "notes": true,
	"serialInput": true, "lotInput": true, "expiryInput": true, "parked": true,
	"serialUnits": true, "captures": true, "serialCursor": true, "serialField": true,
	"dropped": true, "scope": true, "scopeLine": true, "reason": true,
	"receipt": true, "result": true, "lines": true, "closed": true,
	"po": true, "sheet": true,
}

// receiveStateDeclined is every field a key may move WITHOUT having acted, with
// the reason. All of them are the screen ANSWERING a key: counting them would
// call "x does nothing here" an action and invert the rule the sweep enforces.
var receiveStateDeclined = map[string]string{
	"deps": "injected dependencies; no keystroke reaches them",
	"note": "the screen's answer to the last keypress — a decline writes here",
	"failHead": "the failure line's headline: written by a reply off the wire, and " +
		"RETIRED by the submit that supersedes it (clearFail), which a keypress reaches",
	"failDetail": "the failure line's unbounded half, written and retired with failHead",
}

func TestReceiveFormScreen_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(ReceiveFormScreen{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		seen[name] = true
		_, in := receiveStateFingerprinted[name]
		_, out := receiveStateDeclined[name]
		switch {
		case in && out:
			t.Errorf("field %q is in BOTH receiveStateFingerprinted and receiveStateDeclined", name)
		case !in && !out:
			t.Errorf("field %q is in neither receiveStateFingerprinted nor receiveStateDeclined — "+
				"a key that moves it would be judged by nothing", name)
		}
	}
	for name := range receiveStateFingerprinted {
		if !seen[name] {
			t.Errorf("receiveStateFingerprinted names %q, which the screen no longer has", name)
		}
	}
	for name := range receiveStateDeclined {
		if !seen[name] {
			t.Errorf("receiveStateDeclined names %q, which the screen no longer has", name)
		}
	}
}

// receiveFocusFingerprinted is every input whose FOCUS receiveState carries,
// keyed by the field that holds it. Derived against the struct below, because a
// caret lives inside a textinput rather than beside it: the field-name check
// above cannot see focus at all, and a key that only moves the caret INTO a
// field was invisible to every fingerprint in this package until somebody
// noticed. The derivation looks THROUGH a slice, because this screen's
// quantity boxes are one per receivable line.
var receiveFocusFingerprinted = map[string]bool{
	"scan": true, "tracking": true, "carrier": true, "delivered": true,
	"qty": true, "notes": true,
	"serialInput": true, "lotInput": true, "expiryInput": true,
	"reason": true, "parked": true,
}

func TestReceiveFormScreen_EveryInputFocusIsFingerprinted(t *testing.T) {
	typ := reflect.TypeOf(ReceiveFormScreen{})
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
		if !receiveFocusFingerprinted[f.Name] {
			t.Errorf("field %q holds a textinput whose focus receiveState does not carry — "+
				"a key that only moves the caret into it would be judged by nothing", f.Name)
		}
	}
	if len(found) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}
	for name := range receiveFocusFingerprinted {
		if !found[name] {
			t.Errorf("receiveFocusFingerprinted names %q, which is no longer a textinput", name)
		}
	}
}

// receiveEveryBox walks a LIVE screen for every textinput it holds, by name and
// by address.
//
// The same reflection as the focus fingerprint above, over a built screen
// instead of the type, because the question here is about the boxes that exist
// at run time: the quantity boxes are one per receivable line, so the slice has
// to be walked THROUGH rather than counted as one field. Addresses, because
// what allBoxes hands back is pointers and the only honest way to ask "is this
// box in that list" is to compare what they point AT.
// The screen's fields are unexported, so reflect refuses to hand one back as an
// interface — and it does NOT refuse an address. Taking the address and naming
// its type is what turns the enumeration into usable boxes; the alternative is
// a hand-written list of the fields, which is the thing that broke.
func receiveEveryBox(t *testing.T, s *ReceiveFormScreen) map[*textinput.Model]string {
	t.Helper()
	v := reflect.ValueOf(s).Elem()
	input := reflect.TypeOf(textinput.Model{})
	out := map[*textinput.Model]string{}
	at := func(f reflect.Value) *textinput.Model {
		return (*textinput.Model)(f.Addr().UnsafePointer())
	}
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		switch f := v.Field(i); {
		case f.Type() == input:
			out[at(f)] = name
		case f.Kind() == reflect.Slice && f.Type().Elem() == input:
			for j := 0; j < f.Len(); j++ {
				out[at(f.Index(j))] = fmt.Sprintf("%s[%d]", name, j)
			}
		}
	}
	return out
}

// TestReceiveFormScreen_EveryBoxIsInAllBoxes.
//
// allBoxes is the one list blurAll and resetEntry both walk, and it claimed in
// its own doc comment to be every textinput on the screen while omitting
// `delivered` — a real, drawn, typed-into box that inputAt returns and the
// quantity form renders. Two defects came out of the one omission, in opposite
// directions: a caret left armed in a field the frame was no longer about, and
// a stale delivery date riding onto a later receipt. Both are driven below;
// this is the guard that stops the NEXT box being added without the list
// following it, which is how the first one happened.
//
// Derived from the struct rather than from a roster, and against a screen with
// quantity boxes REALLY built, so the slice walk is exercised rather than
// merely written.
func TestReceiveFormScreen_EveryBoxIsInAllBoxes(t *testing.T) {
	lines := receiveSweepLines()
	s := NewReceiveFormScreen(Deps{}, receivePO(lines...))
	s.Update(receiveSheetMsg{sheet: receiveWorksheet(lines...)})
	if len(s.qty) == 0 {
		t.Fatal("the fixture built no quantity boxes, so the slice half of the " +
			"derivation is checking nothing")
	}

	want := receiveEveryBox(t, s)
	if len(want) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}
	got := map[*textinput.Model]bool{}
	for _, box := range s.allBoxes() {
		got[box] = true
	}
	for box, name := range want {
		if !got[box] {
			t.Errorf("field %q holds a textinput that allBoxes() does not reach — blurAll "+
				"will leave its caret armed and resetEntry will leave its value standing", name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("allBoxes() hands back %d distinct boxes and the screen holds %d — it "+
			"repeats one, or reaches something the struct does not own", len(got), len(want))
	}
}

// TestReceiveFormScreen_EveryBoxIsClassifiedForTheWriteOffGate.
//
// The write-off gate asks what a form-ending write would DESTROY, and the answer
// has to cover every box on the screen. Its first version asked only about the
// focused line's quantity box, so a `4` typed on line 1 was thrown away by a
// close-short against line 2 — an unrecoverable write with nothing on the pane
// saying so. Widening it to the whole form is only half a fix: a hand-written
// list of fields is exactly what was too narrow, and the next box added is the
// next silent discard.
//
// So every box in allBoxes() must be either entry the gate protects or recorded
// as excluded WITH A REASON, and this walks allBoxes — the roster the reflection
// guard above already proves complete — to make the omission impossible to make
// quietly. Absent and deliberate are different states.
func TestReceiveFormScreen_EveryBoxIsClassifiedForTheWriteOffGate(t *testing.T) {
	lines := receiveSweepLines()
	s := NewReceiveFormScreen(Deps{}, receivePO(lines...))
	s.Update(receiveSheetMsg{sheet: receiveWorksheet(lines...)})
	if len(s.qty) == 0 {
		t.Fatal("the fixture built no quantity boxes, so the slice half of the walk " +
			"is checking nothing")
	}

	protected := map[*textinput.Model]bool{}
	for _, f := range s.entryBoxes() {
		if f.name == "" {
			t.Errorf("an entry box has no name, so a refusal could not say what is holding it")
		}
		if protected[f.box] {
			t.Errorf("entryBoxes lists %q twice", f.name)
		}
		protected[f.box] = true
	}
	excluded := s.entryBoxesExcluded()
	names := receiveEveryBox(t, s)
	if len(names) == 0 {
		t.Fatal("the derivation found no textinput fields at all — it is broken, not the screen")
	}

	// The QUANTITY boxes are the one group counted rather than named, so they
	// are classified by the count that covers them rather than by either map.
	quantity := map[*textinput.Model]bool{}
	for i := range s.qty {
		quantity[&s.qty[i]] = true
	}

	for _, box := range s.allBoxes() {
		name := names[box]
		switch {
		case quantity[box]:
			// linesTyped walks these.
		case protected[box]:
		case excluded[box] != "":
		case excluded[box] == "":
			if _, recorded := excluded[box]; recorded {
				t.Errorf("box %q is recorded as excluded from the write-off gate with no "+
					"reason — absent and deliberate must be different states", name)
				continue
			}
			t.Errorf("box %q is in neither entryBoxes nor entryBoxesExcluded — a write-off "+
				"would destroy it and the gate would not know", name)
		}
	}
	for box, why := range excluded {
		if _, ok := names[box]; !ok {
			t.Errorf("entryBoxesExcluded records a box the screen no longer has (%q)", why)
		}
		if protected[box] {
			t.Errorf("a box is both protected and excluded (%q)", why)
		}
	}
}

// ---------------------------------------------------------------------------
// No sentence points at an empty line picker
// ---------------------------------------------------------------------------

// TestReceive_NoSentencePointsAtAnEmptyLinePicker.
//
// A frame may only name a key that acts in the state it is drawing, and "pick
// the line with up/dn" is a promise about an OUTCOME rather than about a key:
// up/dn does still move, it just reaches no line, because applyWorksheet puts
// every voided and closed-short line in s.closed and an order all of whose
// lines are settled leaves s.lines empty. qtyBody draws the scan row before its
// own empty branch, so that is the state the operator lands in.
//
// It is swept rather than asserted at the two sites that were reported, because
// the reported-site fix has now been made twice: scanHint was corrected in one
// round and noScanMatchNote, two functions away, carried the identical tail in
// the identical state into the next. A third sentence added later would be a
// fourth round.
//
// The forbidden PHRASE is read off the code — it is what linePickWayOut answers
// on an order that has lines — so rewording the promise does not quietly retire
// this check, and the whole key space is pressed so that a sentence reachable
// only through some particular key is still reached. Both nothing-fixtures are
// swept because the notes branch on them: an order carrying no code at all is a
// different fact from one whose codes are all on settled lines.
func TestReceive_NoSentencePointsAtAnEmptyLinePicker(t *testing.T) {
	_, with := receiveDrive(t, &receiveFake{}, receiveOrder(), 80, 30)
	if len(with.lines) == 0 {
		t.Fatal("the reference order draws no line, so the phrase derived from it is the " +
			"empty-order wording and this check would be vacuous")
	}
	promise := with.linePickWayOut()
	if promise == "" {
		t.Fatal("linePickWayOut answers nothing on an order with lines")
	}

	space := poKeySpace()
	for _, coded := range []bool{true, false} {
		lines := receiveAllSettled(coded)
		for _, seed := range []struct{ name, typed string }{
			// The bare seed reaches the scan HINT, which is drawn on every
			// frame of this phase; the typed one reaches findLine, whose
			// no-match note is the sibling that was missed.
			{"nothing typed", ""},
			// A code no line on the order carries, so findLine cannot resolve
			// it and has to say which kind of nothing this is.
			{"an unmatched code in the scan box", "SKU-77"},
		} {
			for _, k := range space {
				t.Run(fmt.Sprintf("coded=%v %s then %q", coded, seed.name, k), func(t *testing.T) {
					r, s := receiveDrive(t, &receiveFake{}, lines, 80, 30)
					if len(s.lines) != 0 {
						t.Fatalf("the fixture left %d receivable lines, so this is not the "+
							"state the check is about", len(s.lines))
					}
					if seed.typed != "" {
						r = receiveTypeInto(t, r, seed.typed)
					}
					next, _ := r.Update(poPhaseKeyMsg(k))
					r = next.(Root)

					// The NOTE is read raw beside the clipped pane: the pane is
					// what the operator sees, but a promise cut off by the
					// reservation would pass a pane-only check while still
					// being what the screen meant to say.
					said := s.note.text + " " + receiveClippedPane(s, 80, 30)
					if strings.Contains(said, promise) {
						t.Errorf("after %q the screen promises %q on an order with no line "+
							"to pick:\n%s", k, promise, said)
					}
					_ = r
				})
			}
		}
	}
}

// receiveFocusedBoxes names every box on a live screen whose caret is armed.
//
// It walks the STRUCT and not allBoxes, which is the whole point: a box missing
// from allBoxes is exactly the box blurAll cannot reach, so asking allBoxes
// which carets are armed would be asking the broken list about its own defect.
func receiveFocusedBoxes(t *testing.T, s *ReceiveFormScreen) []string {
	t.Helper()
	var out []string
	for box, name := range receiveEveryBox(t, s) {
		if box.Focused() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// TestReceive_NoStateArmsTwoCarets is the blurAll half of the omission above,
// swept over every state rather than over the one it was reported on.
//
// bubbles draws a focused box's cursor in reverse video, which is the layer's
// strongest "you may type here" signal, so two focused boxes is two answers to
// "who owns the keyboard" on one pane. It happened because blurAll walks
// allBoxes and allBoxes did not know about `delivered`: leaving the quantity
// form from the Delivered row left that caret armed through the review, the
// submit and the summary, while the frame drew and focused something else.
//
// The states come from receivePhaseCases and the boxes from reflection, so
// neither half is a list somebody has to remember to extend.
func TestReceive_NoStateArmsTwoCarets(t *testing.T) {
	for _, c := range receivePhaseCases() {
		t.Run(c.name, func(t *testing.T) {
			build := receiveHarness(t, c.fake, c.lines, 80, 24)
			r, s := build(t)
			// Into the Delivered box FIRST, which is the row the defect was
			// reached through: a caret armed there and never blurred is the
			// one the phase change carries with it.
			for s.phase == phaseQty && s.focused != receiveRowDelivered {
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
			}
			r = c.reach(t, r, s)
			if s.phase != c.phase {
				t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
			}
			if armed := receiveFocusedBoxes(t, s); len(armed) > 1 {
				t.Errorf("%s draws %d armed carets at once (%v) — the pane says "+
					"\"type here\" in two places", c.name, len(armed), armed)
			}
			_ = r
		})
	}
}

// ---------------------------------------------------------------------------
// The bar names exactly the keys that work
// ---------------------------------------------------------------------------

// receiveBarKeyNames maps a bar entry's Key to the keystrokes it SPELLS, and to
// no synonyms. Credit for a synonym would be the sweep making a claim on the
// bar's behalf — the defect it exists to report, sitting inside the check.
//
// It held `"R": {"r"}` and that is exactly the shape it warns about. Three bars
// spelled the re-read as "R" while both handlers bind "r", so the pane drew
// "R=Re-read" on the frame whose only recovery key that is and answered Shift+R
// with "R does nothing here" — and this table hid it from both directions at
// once. Forward, the sweep pressed "r", saw it act and passed; reverse, it
// pressed "R", saw it decline and passed because no bar entry named "R". The
// key was swept in a direction that could never fail.
//
// TestReceive_TheBarTokenTableTranscribes is what stops the next entry
// interpreting instead of transcribing, and receiveBarTokenAbbreviations is
// where the two entries that genuinely abbreviate are recorded WITH a reason,
// so "absent" and "allowed" are different states.
var receiveBarKeyNames = map[string][]string{
	"Enter":     {"enter"},
	"Esc":       {"esc"},
	"Enter/Esc": {"enter", "esc"},
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
	"PgUp":      {"pgup"},
	"r":         {"r"},
	"Ctrl+K":    {"ctrl+k"},
	"Ctrl+R":    {"ctrl+r"},
	"Ctrl+E":    {"ctrl+e"},
	"Ctrl+X":    {"ctrl+x"},
}

// receiveBarTokenAbbreviations records the tokens whose segments do not spell
// their keystroke letter for letter, and why each is allowed to.
//
// Both are the columnar layer's own shorthand for keys a terminal names longer
// than a 49-cell bar can afford, and both are the spelling roughly twenty other
// forms in this program use. Respelling them here would make the receiving
// screen disagree with every one of them, which is the reasoning poFormNavAliases
// records for Tab/Shift-Tab — the exception is the consistency, not a
// convenience.
//
// An entry here still has to be an abbreviation OF ITS OWN KEY: the guard below
// requires every letter of the token to appear in the keystroke, in order, so
// this map cannot be used to credit a token with a key it has nothing to do
// with. That is the whole difference between recording an exception and
// creating a loophole.
var receiveBarTokenAbbreviations = map[string]string{
	"UP/DN": "DN is the layer's shorthand for down; UP/DN=Fields is how every " +
		"columnar form in this program spells the pair",
	"PgUp/PgDn": "PgDn is the layer's shorthand for pgdown, spelled the same way " +
		"on every form that pages",
}

// TestReceive_TheBarTokenTableTranscribes.
//
// A bar-token table is half of a key space: the sweeps ask it what a bar CLAIMS,
// so an entry that interprets rather than transcribes puts the defect inside the
// check. `"R": {"r"}` did exactly that for three bars at once.
//
// The rule is applied by CASE of what the keystroke is, and that distinction is
// derived rather than listed: receiveNamedKeyMsg answers whether a terminal
// sends the key as a named type or as a RUNE. For a named key ("enter", "pgup",
// "ctrl+k") the bar's capitalisation is display convention — a terminal has no
// other "Enter" to send — so the token need only match case-insensitively. For a
// RUNE the case IS the keystroke: "R" and "r" are two keys this program already
// treats as different (list.go's listShortcuts gives lowercase to the screen and
// uppercase to a sibling surface), so the token must match exactly.
func TestReceive_TheBarTokenTableTranscribes(t *testing.T) {
	if len(receiveBarKeyNames) == 0 {
		t.Fatal("the token table is empty, so this guard is checking nothing")
	}
	for token, keys := range receiveBarKeyNames {
		segments := strings.Split(token, "/")
		if len(segments) != len(keys) {
			t.Errorf("token %q has %d segment(s) but is credited with %d key(s) %v — a "+
				"segment and a key are what the operator reads and what the terminal "+
				"sends, and they must pair up", token, len(segments), len(keys), keys)
			continue
		}
		for i, seg := range segments {
			key := keys[i]
			msg, ok := receiveNamedKeyMsg(key)
			if !ok {
				t.Errorf("token %q is credited with %q, which is no keystroke at all", token, key)
				continue
			}
			if msg.Type == tea.KeyRunes {
				// A rune: the case is the key, so the spelling must be exact.
				if seg != key {
					t.Errorf("token %q spells %q where the key is %q — a terminal sends "+
						"those as different keystrokes, so the bar is naming one key and "+
						"the handler binding another", token, seg, key)
				}
				continue
			}
			if strings.EqualFold(seg, key) {
				continue
			}
			why, recorded := receiveBarTokenAbbreviations[token]
			if !recorded {
				t.Errorf("token %q spells %q where the key is %q, and is not recorded in "+
					"receiveBarTokenAbbreviations — either spell what the bar binds or "+
					"say why it may not", token, seg, key)
				continue
			}
			if why == "" {
				t.Errorf("token %q is recorded as an abbreviation with no reason", token)
			}
			if !receiveIsAbbreviationOf(seg, key) {
				t.Errorf("token %q is recorded as an abbreviation but %q is not one of "+
					"%q — the exception is being used to credit an unrelated key", token, seg, key)
			}
		}
	}
	for token := range receiveBarTokenAbbreviations {
		if _, ok := receiveBarKeyNames[token]; !ok {
			t.Errorf("receiveBarTokenAbbreviations records %q, which no bar entry spells "+
				"any more — a standing exception nothing needs", token)
		}
	}
}

// receiveIsAbbreviationOf reports whether every letter of the token segment
// appears in the keystroke, in order. It is what keeps a recorded abbreviation
// tied to its own key rather than becoming a licence to name anything.
func receiveIsAbbreviationOf(seg, key string) bool {
	rest := strings.ToLower(key)
	for _, r := range strings.ToLower(seg) {
		i := strings.IndexRune(rest, r)
		if i < 0 {
			return false
		}
		rest = rest[i+len(string(r)):]
	}
	return true
}

func receiveNamedKeys(t *testing.T, bar []actionBarItem) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	for _, it := range bar {
		keys, ok := receiveBarKeyNames[it.Key]
		if !ok {
			t.Fatalf("bar entry %q is not in receiveBarKeyNames — add it so the rule covers it", it.Key)
		}
		for _, k := range keys {
			named[k] = true
		}
	}
	return named
}

// receiveHarness serves one fake for a whole subtest and hands back a builder
// that opens a fresh form against it, so a probe keystroke cannot leak into the
// next assertion and the sweep does not stand up a server per keypress.
func receiveHarness(t *testing.T, fake func() *receiveFake, lines []omsapi.ReceivingLine, width, height int) func(*testing.T) (Root, *ReceiveFormScreen) {
	t.Helper()
	f := fake()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	po := receivePO(lines...)
	return func(t *testing.T) (Root, *ReceiveFormScreen) {
		t.Helper()
		s := NewReceiveFormScreen(deps, po)
		r := newTestRoot(s)
		r.deps = deps
		next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		r = next.(Root)
		// The worksheet is fetched the way the screen fetches it. A drive that
		// wrote the rows in would be pressing keys against a state the endpoint
		// cannot produce — and the loading frame is itself one of the states
		// these rules cover.
		return receiveSettle(t, r, s.Init(), 0), s
	}
}

// receiveClippedPane is what the operator can actually READ: the screen's frame
// clipped on both axes exactly as Root clips it, joined back into one string.
//
// These sweeps used to compare `Root.View()` — the whole terminal, nav column
// and border included, unclipped. That is the pattern AGENTS.md names as the
// trap that let eight advertised keys ship past the 51-column cut, and it was
// safe here only by accident: every declining lead happens to name its key at
// position 0 of the note, so nothing distinguishing sat past the cut. The claim
// these sweeps make is about what the operator sees, so they measure the pane
// the operator sees, and a lead that grows a prefix cannot quietly move the
// distinguishing part off the edge.
//
// The WIDTH is a parameter for the same reason it is not 80 everywhere else on
// these screens: it hard-coded 80 while a caller looped over 80/100/120, so at
// two of the three widths the comparison and the failure dumps were measured
// against a pane the screen had never been sized for. That fails in the safe
// direction, but a helper documented as "what the operator can READ" measuring
// a pane the terminal never gave is the same claim-the-code-does-not-honour
// this file keeps closing.
func receiveClippedPane(s *ReceiveFormScreen, width, height int) string {
	return strings.Join(receivePaneLines(s, width, height), "\n")
}

// receivePaneSizes are the pane heights every state is checked at. 24 is the
// terminal this interface is modelled on and the one that clips; 30 is there so
// a frame cannot be tuned for the short pane.
var receivePaneSizes = []int{24, 30}

// receiveFailureHeights are the panes the failed-write case is swept at.
//
// The extra one is the point of the case. A standing failure detail is the only
// input left that moves the pinned header, so it can only change the BAR at a
// height where the rows it takes are the rows that decide whether the body
// overflows — and at the usual pair the body overflows either way, so a case
// swept only there reaches the state and never reaches the bar it exists for.
//
// That height is SEARCHED FOR rather than written down. It was written down
// once (38), and 38 stopped being that height the moment the flow gained a
// review phase — so the case went on reading as coverage of the failure frame's
// bar while covering nothing, which is the exact failure mode every roster in
// this file is derived to avoid. Anything that re-words the review moves the
// flip point; nothing should be holding a copy of it.
func receiveFailureHeights() []int {
	out := []int{24, 30}
	if h, ok := receiveFailureFlipHeight(); ok {
		out = append(out, h)
	}
	return out
}

// receiveFailureFlipHeight is the shortest pane at which a standing failure
// detail changes whether the review's bar names PgUp/PgDn.
//
// It builds the screens directly rather than driving them, and that is
// deliberate: the question is pure GEOMETRY — does the header this detail costs
// push the body over the window — so a round trip per height would measure
// nothing extra. What it compares is the same expression the bar and the key
// arm both read.
func receiveFailureFlipHeight() (int, bool) {
	lines := receiveSweepLines()
	build := func(height int, failed bool) *ReceiveFormScreen {
		s := NewReceiveFormScreen(Deps{}, receivePO(lines...))
		s.Update(tea.WindowSizeMsg{Width: 80, Height: height})
		s.Update(receiveSheetMsg{sheet: receiveWorksheet(lines...)})
		s.qty[1].SetValue("2")
		s.phase = phaseReview
		if failed {
			s.setFail("Receiving PO-1001 failed", receiveSweepGateway)
		}
		return s
	}
	pages := func(s *ReceiveFormScreen) bool {
		return s.rowsPageFor(s.reviewBody(), s.reviewRows(), len(s.headerLines()))
	}
	for h := 10; h <= 60; h++ {
		if pages(build(h, false)) != pages(build(h, true)) {
			return h, true
		}
	}
	return 0, false
}

// paneSizes is the heights this case is pressed at.
func (c receivePhaseCase) paneSizes() []int {
	if len(c.heights) > 0 {
		return c.heights
	}
	return receivePaneSizes
}

// receiveProbes is where a named key is pressed FROM.
//
// A key is dead only if it does nothing from ANY position, because a cursor on
// the first row cannot move up and one on the last cannot move down — so a
// named key gets probed from several. Which positions exist is DERIVED from the
// bar rather than fixed: a state whose bar names no movement key has no cursor
// to move, so pressing `down` three times there reaches exactly the position it
// started from and the two extra probes only rebuild the state to no purpose.
//
// Derived rather than a per-state exception, for this file's standing reason: a
// hand-kept list of "the cheap states" would be one more roster to forget to
// extend, and forgetting would cost coverage rather than time.
func receiveProbes(named map[string]bool) [][]string {
	if !named["down"] && !named["pgdown"] {
		return [][]string{nil}
	}
	probes := [][]string{nil, {"down"}, {"down", "down", "down"}}
	if named["pgdown"] {
		// PgUp/PgDn do not always move what Down moves. On serial capture Down
		// walks the three FIELDS of one unit and PgDn walks the units, so a
		// probe set built out of Down presses never leaves unit 1 — where PgUp
		// is at an edge and can only decline, which reads as a named key that
		// does nothing. The pair is only honestly judged from somewhere it can
		// move in both directions.
		probes = append(probes, []string{"pgdown"}, []string{"pgdown", "pgdown"})
	}
	return probes
}

// TestReceive_EveryStateNamesExactlyTheKeysThatWork presses the whole key space
// at every state, in both directions: a key the bar names must change
// something, and a key it does not name must not.
//
// A named key is probed from several positions, because a cursor on the first
// row cannot move up and one on the last cannot move down: a key is dead only
// if it does nothing from ANY of them.
//
// The two directions are judged DIFFERENTLY, and the difference is the whole
// correctness of this sweep on a bar whose shape follows the cursor:
//
//   - FORWARD ("a key the bar names must act") is asked across the probes. A
//     key named where the cursor rests may only be able to act from somewhere
//     else, and that is not a dead key.
//   - REVERSE ("a key the bar does not name must not act") is asked AT EACH
//     PROBE, against the bar read AT THAT PROBE. It used to compare the bar at
//     REST with the action at any probe — the bar in one place against the
//     press in another — and Ctrl+K is exactly the key that shape gets wrong:
//     qtyBarItems names it only with the cursor on a line with an outstanding
//     balance, which is also the only place it acts, and the old comparison
//     read the bar on the scan row and the press three rows down and reported
//     an honest key as a violation. A conditionally-named key is the ordinary
//     case on this screen, not the exception.
//
// The reverse direction asks for no STATE change rather than no COMMAND,
// because a declining key is meant to answer with a sentence and change nothing
// — that answer is the rule this screen exists to keep, not a key that escaped
// the audit.
func TestReceive_EveryStateNamesExactlyTheKeysThatWork(t *testing.T) {
	space := poKeySpace()
	for _, c := range receivePhaseCases() {
		for _, height := range c.paneSizes() {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := receiveHarness(t, c.fake, c.lines, 80, height)
				fresh := func(t *testing.T, probe []string) (Root, *ReceiveFormScreen) {
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
				named := receiveNamedKeys(t, bar)
				probes := receiveProbes(named)

				for _, k := range space {
					if c.typing && (poIsPrintable(k) || poFieldKeys[k] || poFormNavAliases[k]) && !named[k] {
						continue
					}
					acted, namedSomewhere := false, named[k]
					for _, probe := range probes {
						pr, ps := fresh(t, probe)
						here := receiveNamedKeys(t, ps.bar())
						namedSomewhere = namedSomewhere || here[k]
						before := receiveState(ps)
						_, cmd := pr.Update(poPhaseKeyMsg(k))
						if receiveState(ps) == before && !poCmdActs(cmd) {
							continue
						}
						acted = true
						if !here[k] {
							t.Errorf("%s does not name %q where the cursor is after %v, but "+
								"pressing it there acts (bar: %+v)", c.name, k, probe, ps.bar())
						}
					}
					if namedSomewhere && !acted {
						t.Errorf("%s names %q but pressing it changes nothing (bar: %+v)", c.name, k, bar)
					}
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// No two declining keys redraw the same pane
// ---------------------------------------------------------------------------

// TestReceive_NoTwoDecliningKeysRedrawTheSamePane is the other half of "a
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
// It runs on the NON-TYPING states. On a state with a focused field the caret
// is on the pane and the field owns the keys the sweep would otherwise be
// pressing, so the property is neither true nor meaningful there.
func TestReceive_NoTwoDecliningKeysRedrawTheSamePane(t *testing.T) {
	space := poKeySpace()
	for _, c := range receivePhaseCases() {
		if c.typing {
			continue
		}
		for _, height := range c.paneSizes() {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := receiveHarness(t, c.fake, c.lines, 80, height)
				fresh := func(t *testing.T) (Root, *ReceiveFormScreen) {
					t.Helper()
					r, s := build(t)
					r = c.reach(t, r, s)
					if s.phase != c.phase {
						t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
					}
					return r, s
				}
				_, screen := fresh(t)
				named := receiveNamedKeys(t, screen.bar())
				resting := receiveClippedPane(screen, 80, height)

				panes := map[string]string{}
				for _, k := range space {
					r, s := fresh(t)
					before := receiveState(s)
					next, cmd := r.Update(poPhaseKeyMsg(k))
					r = next.(Root)
					// "Acted" includes leaving the screen: esc hands back a
					// SwitchTo command, which changes no field here and would
					// otherwise read as a named key that answers with nothing.
					if receiveState(s) != before || poCmdActs(cmd) {
						continue // it acted; this sweep is about the ones that do not
					}
					if after := receiveClippedPane(s, 80, height); after == resting {
						t.Errorf("%s: %q answers with a byte-for-byte identical pane — "+
							"that reads as a wedged program (named by the bar: %v)", c.name, k, named[k])
					} else if other, clash := panes[after]; clash {
						t.Errorf("%s: %q and %q redraw the same pane — the second press would "+
							"leave the first one's answer standing", c.name, other, k)
					} else {
						panes[after] = k
					}
				}
				if len(panes) == 0 {
					t.Errorf("%s: no key declined at all — the sweep is looking at nothing", c.name)
				}
			})
		}
	}
}

// receiveFieldOwns reports whether a focused textinput would answer this key
// ITSELF, and it asks BUBBLES rather than a roster.
//
// The typing sweep below needs to know which keys belong to the field and which
// belong to the frame, and a hand-kept list of the first is this file's
// standing failure mode: bubbles binds fifteen actions across some
// twenty-five keystrokes, several of them chords nobody would think to write
// down, and a version bump moves the set under the list. So the question is put
// to a real textinput — value present, caret in the MIDDLE, so every movement
// and deletion binding has somewhere to go — and answered by whether the widget
// changes what it HOLDS or hands back WORK: its value, its caret or its command,
// which is the same triple typeInto asks about, so the sweep and the code cannot
// come apart over who owns a key. Not what it DRAWS — lipgloss strips the
// caret's reverse video when stdout is not a TTY, so a rendered comparison
// classifies every movement key as the frame's inside a test binary and as the
// field's in production.
//
// The COMMAND is in the triple because bubbles has bindings whose whole effect
// is asynchronous: Paste comes back as `return m, Paste`, model untouched. Left
// out, a paste is indistinguishable from a key the widget ignored — which is
// how ctrl+v came to be answered with "ctrl+v does nothing here" while its
// command was discarded.
//
// Asked with the caret in the middle on purpose: at an edge, Left or Home
// answers with no change and would be classified as the frame's, which is a
// different question (that one is "does this key do anything from HERE", and
// typeInto answers it at run time).
func receiveFieldOwns(k string) bool {
	msg, ok := receiveNamedKeyMsg(k)
	if !ok {
		return false
	}
	return receiveBoxTakes(msg)
}

// receiveBoxTakes is the question typeInto asks, put to a bare box.
func receiveBoxTakes(msg tea.KeyMsg) bool {
	box := textinput.New()
	box.Focus()
	box.SetValue("abc")
	box.SetCursor(1)
	before, at := box.Value(), box.Position()
	box, cmd := box.Update(msg)
	return cmd != nil || box.Value() != before || box.Position() != at
}

// receiveNamedKeyMsg turns a key NAME into the KeyMsg a terminal delivers for
// it, by asking bubbletea what each of its own key types is CALLED.
//
// Derived rather than tabulated because the names this file needs come out of
// bubbles' KeyMap — chords like "alt+backspace" and "ctrl+right" that no
// hand-written switch in this package has ever had a case for, and that a
// bubbles version bump can add. poPhaseKeyMsg falls back to KeyRunes for
// anything it does not recognise, which would turn "ctrl+v" into the six
// literal characters and quietly test nothing.
func receiveNamedKeyMsg(name string) (tea.KeyMsg, bool) {
	base, alt := name, false
	if rest, cut := strings.CutPrefix(name, "alt+"); cut {
		base, alt = rest, true
	}
	for t := -40; t <= 127; t++ {
		typ := tea.KeyType(t)
		if typ == tea.KeyRunes {
			continue
		}
		if (tea.Key{Type: typ}).String() == base {
			return tea.KeyMsg{Type: typ, Alt: alt}, true
		}
	}
	if r := []rune(base); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r, Alt: alt}, true
	}
	return tea.KeyMsg{}, false
}

// TestReceive_TheKeyNameLookupIsNotAFallback keeps receiveNamedKeyMsg from
// silently degrading. It resolves the chords the KeyMap really carries — if the
// scan ever stopped finding them it would hand back KeyRunes, and every guard
// built on it would go on passing while pressing the wrong thing entirely.
func TestReceive_TheKeyNameLookupIsNotAFallback(t *testing.T) {
	for _, name := range []string{"ctrl+v", "ctrl+right", "alt+backspace", "pgup", "enter"} {
		msg, ok := receiveNamedKeyMsg(name)
		if !ok {
			t.Errorf("no key type is named %q", name)
			continue
		}
		if msg.Type == tea.KeyRunes {
			t.Errorf("%q resolved to runes %q rather than to its own key type", name, msg.Runes)
		}
		if got := msg.String(); got != name {
			t.Errorf("%q resolved to a key that calls itself %q", name, got)
		}
	}
}

// TestReceive_TheFieldOwnershipProbeIsNotVacuous keeps the derivation above
// from being the thing under test. A probe that answered "the field owns it"
// for everything would empty the sweep below and read as coverage; one that
// answered "nothing" would fail on every key of every typing state and be
// reverted rather than believed.
func TestReceive_TheFieldOwnershipProbeIsNotVacuous(t *testing.T) {
	// A rune and a movement chord are the field's; Enter and Esc are the
	// frame's on every screen in this program.
	for _, k := range []string{"a", "left", "backspace", "ctrl+e"} {
		if !receiveFieldOwns(k) {
			t.Errorf("the probe says a focused field does not own %q", k)
		}
	}
	for _, k := range []string{"enter", "esc", "pgup", "pgdown"} {
		if receiveFieldOwns(k) {
			t.Errorf("the probe says a focused field owns %q, so the sweep would never "+
				"press it on a typing state", k)
		}
	}
	// typeInto reads a nil command as "the box ignored it", and that reading is
	// only safe while bubbles really answers a key it ignores with nothing. A
	// version that started handing back a blink on every keystroke would make
	// typeInto stop declining anything at all, on every typing phase at once,
	// and no other check in this file would notice.
	box := textinput.New()
	box.Focus()
	box.SetValue("abc")
	box.SetCursor(1)
	ignored, ok := receiveNamedKeyMsg("pgdown")
	if !ok {
		t.Fatal("pgdown has no key type, so this check is looking at nothing")
	}
	if _, cmd := box.Update(ignored); cmd != nil {
		t.Error("a focused textinput hands back a command for a key it ignores, so " +
			"typeInto's command half now reads every key as taken and declines nothing")
	}
	// And the half that broke: a binding whose whole effect is in the command.
	paste, ok := receiveNamedKeyMsg("ctrl+v")
	if !ok {
		t.Fatal("ctrl+v has no key type")
	}
	if _, cmd := box.Update(paste); cmd == nil {
		t.Error("a focused textinput hands back no command for its Paste binding, so " +
			"the command half of the probe is guarding nothing")
	}
}

// receiveDeferredFieldKeys is every key the box's own KeyMap claims whose whole
// effect is in the returned COMMAND — it changes neither the value nor the
// caret, so a frame that judged ownership by mutation alone would decline it
// and throw the command away.
//
// Derived twice over, because both halves are things that move under us. The
// key NAMES come from reflection over textinput.KeyMap, so a binding added by a
// version bump is covered without anyone extending a list; which of them are
// DEFERRED is answered by pressing each at a bare widget, so a binding that
// changes shape from synchronous to asynchronous moves into this set on its
// own. Today it is exactly Paste, and Paste is the one poKeySpace has never
// contained — which is why the sweep that presses the whole space never saw
// ctrl+v fall silent.
func receiveDeferredFieldKeys(t *testing.T) []tea.KeyMsg {
	t.Helper()
	km := reflect.ValueOf(textinput.New().KeyMap)
	binding := reflect.TypeOf(bubblekey.Binding{})
	var out []tea.KeyMsg
	seen := map[string]bool{}
	for i := 0; i < km.NumField(); i++ {
		if km.Field(i).Type() != binding {
			continue
		}
		b := km.Field(i).Interface().(bubblekey.Binding)
		for _, name := range b.Keys() {
			if seen[name] {
				continue
			}
			seen[name] = true
			msg, ok := receiveNamedKeyMsg(name)
			if !ok {
				continue
			}
			box := textinput.New()
			box.Focus()
			before, at := box.Value(), box.Position()
			next, cmd := box.Update(msg)
			if cmd != nil && next.Value() == before && next.Position() == at {
				out = append(out, msg)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no binding in the box's KeyMap defers its work to a command, so the " +
			"guard below is looking at nothing — either bubbles changed shape or the " +
			"reflection stopped finding its bindings")
	}
	return out
}

// TestReceive_ADeferredFieldKeyIsNotDeclined.
//
// typeInto decides whether the focused box took a key. Written to compare the
// value and the caret alone, it read a PASTE as a key the box ignored: bubbles
// answers its Paste binding with `return m, Paste`, so the model comes back
// unchanged and all of the work is in the command. The frame then printed
// "ctrl+v does nothing here" and dropped the command — a silent discard of the
// operator's action and a false claim about it in the same press, on all three
// typing phases at once.
//
// Held over the whole class rather than over ctrl+v, and over every typing
// phase rather than the one it was noticed on, because a hand-kept exception
// for the binding somebody happened to report is the shape this file exists to
// stop.
func TestReceive_ADeferredFieldKeyIsNotDeclined(t *testing.T) {
	deferred := receiveDeferredFieldKeys(t)
	for _, c := range receivePhaseCases() {
		if !c.typing {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			build := receiveHarness(t, c.fake, c.lines, 80, 24)
			for _, msg := range deferred {
				r, s := build(t)
				r = c.reach(t, r, s)
				if s.phase != c.phase {
					t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
				}
				k := msg.String()
				next, cmd := r.Update(msg)
				r = next.(Root)
				if pane := receivePaneText(s, 80, 24); strings.Contains(pane, k+" does nothing here") {
					t.Errorf("%s: %q is the box's own binding and the frame declined it:\n%s",
						c.name, k, pane)
				}
				// And the work really went out: the command the frame handed
				// back is the SAME FUNCTION the widget hands back for this key.
				// The frame's answer to a declined key is a status write, so
				// the two are different functions and the comparison separates
				// them.
				want := receiveCmdIdentity(receiveBareBoxCmd(msg))
				if got := receiveCmdIdentity(cmd); got != want {
					t.Errorf("%s: %q answered with %s, want the box's own %s — the "+
						"command the widget handed back was dropped", c.name, k, got, want)
				}
			}
		})
	}
}

// receiveBareBoxCmd is the widget's own answer to this key, taken from a bare
// focused box so the expectation is bubbles' rather than a name written here.
func receiveBareBoxCmd(msg tea.KeyMsg) tea.Cmd {
	box := textinput.New()
	box.Focus()
	_, cmd := box.Update(msg)
	return cmd
}

// receiveCmdIdentity names the FUNCTION a command is, WITHOUT running it.
//
// Not running it is the point. The one deferred binding bubbles has today is
// Paste, and textinput.Paste calls clipboard.ReadAll — pbpaste on darwin,
// xclip or xsel on Linux. The first shape of this check compared the MESSAGE
// each command yielded, which meant executing both sides on every typing phase:
// a unit sweep that spawns half a dozen external processes and reads whatever
// the developer happens to have copied. It was not flaky, but a test that
// reaches outside the process for its evidence is unsound whether or not it
// happens to agree today, and the clipboard is state no test owns.
//
// The identity of the function answers the same question. Root.Update hands the
// screen's command straight back (app.go's dispatch returns it unwrapped), so a
// frame that routed the key to the box returns the box's own function pointer,
// and a frame that DECLINED returns the closure s.say built — a different
// function, reported by name, which is what makes the failure readable.
func receiveCmdIdentity(cmd tea.Cmd) string {
	if cmd == nil {
		return "<nil>"
	}
	pc := reflect.ValueOf(cmd).Pointer()
	if fn := runtime.FuncForPC(pc); fn != nil {
		return fn.Name()
	}
	return fmt.Sprintf("func@%#x", pc)
}

// TestReceive_NoKeyFallsSilentIntoAField is the typing half of "a keypress that
// changes nothing visible IS the reported bug".
//
// The sweep above runs on the NON-typing states, because with a field focused
// the printable keys act by editing the value and the property is neither true
// nor meaningful for them. That exemption was being taken by the whole STATE,
// though, and the keys the field has no opinion about were riding out on it:
// every typing phase ended its switch by handing the key to the box, and the
// box silently dropped ctrl+t, ctrl+p, ctrl+n, ctrl+x and ctrl+r — plus Up and
// Down on the write-off confirm, the one frame of this screen where the next
// key writes a balance off, reached from four frames whose bars all name UP/DN.
//
// So the exemption is taken per KEY instead, and which keys get it is derived
// from bubbles (receiveFieldOwns) rather than listed. What is left is the set
// the FRAME is answerable for, and it is held to both halves of the rule: no
// key may redraw the resting pane, and no two may redraw each other's.
func TestReceive_NoKeyFallsSilentIntoAField(t *testing.T) {
	space := poKeySpace()
	for _, c := range receivePhaseCases() {
		if !c.typing {
			continue
		}
		for _, height := range c.paneSizes() {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				build := receiveHarness(t, c.fake, c.lines, 80, height)
				fresh := func(t *testing.T) (Root, *ReceiveFormScreen) {
					t.Helper()
					r, s := build(t)
					r = c.reach(t, r, s)
					if s.phase != c.phase {
						t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
					}
					return r, s
				}
				_, screen := fresh(t)
				resting := receiveClippedPane(screen, 80, height)

				pressed, panes := 0, map[string]string{}
				for _, k := range space {
					if receiveFieldOwns(k) {
						continue // the field answers it, which is what the field is FOR
					}
					if poFormNavAliases[k] {
						// Tab and Shift-Tab, recorded rather than omitted: they
						// ride alongside Up/Down on a sheet WITH FIELDS and
						// roughly twenty columnar forms name the pair as
						// UP/DN=Fields, so this screen cannot answer for them
						// on its own (poFormNavAliases).
						continue
					}
					pressed++
					r, s := fresh(t)
					before := receiveState(s)
					next, cmd := r.Update(poPhaseKeyMsg(k))
					r = next.(Root)
					if receiveState(s) != before || poCmdActs(cmd) {
						continue // it acted
					}
					after := receiveClippedPane(s, 80, height)
					if after == resting {
						t.Errorf("%s: %q falls into the focused field and answers with a "+
							"byte-for-byte identical pane — that reads as a wedged program", c.name, k)
					} else if other, clash := panes[after]; clash {
						t.Errorf("%s: %q and %q redraw the same pane — the second press would "+
							"leave the first one's answer standing", c.name, other, k)
					} else {
						panes[after] = k
					}
				}
				if pressed == 0 {
					t.Errorf("%s: every key in the space was classified as the field's, so "+
						"this sweep pressed nothing at all", c.name)
				}
			})
		}
	}
}

// TestReceive_EveryDeclineNamesTheKeyItAnswers is the reason the sweep above
// can pass: a lead that names the key cannot be shared by two keys. Checking
// the property AND its mechanism is deliberate — the mechanism is what a future
// change would break first, and it breaks quietly.
func TestReceive_EveryDeclineNamesTheKeyItAnswers(t *testing.T) {
	lines := receiveSweepLines()
	build := receiveHarness(t, func() *receiveFake {
		return &receiveFake{sheet: receiveWorksheet(lines...)}
	}, lines, 80, 24)
	r, s := build(t)
	s.qty[1].SetValue("1")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // review -> the receipt lands
	if s.phase != phaseDone {
		t.Fatalf("setup landed on phase %v, want the summary", s.phase)
	}
	// In SEQUENCE, with no reset between presses: two keys answering with one
	// sentence would redraw the pane the first one left.
	before := receiveClippedPane(s, 80, 24)
	for _, k := range []string{"x", "z", "ctrl+t"} {
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
		// The sentence FOLDS on a 51-column pane, so it is read out of the
		// flattened pane rather than the raw lines: a substring check against
		// the lines fails on the line break rather than on the defect.
		if pane := receivePaneText(s, 80, 24); !strings.Contains(pane, k+" does nothing here") {
			t.Errorf("the decline for %q does not name it:\n%s", k, pane)
		}
		if now := receiveClippedPane(s, 80, 24); now == before {
			t.Errorf("%q redrew a byte-for-byte identical pane", k)
		} else {
			before = now
		}
	}
}

// TestReceive_TheFreezeIsAnAllowList: while the receipt is out, the payload has
// already gone, so nothing on the screen may change under it. Written as an
// allow-list rather than a list of frozen keys, because the other way round
// froze the keys somebody thought of and left every arm added later free by
// default — which is exactly how `d` got past the New PO screen's freeze.
//
// The defect this closes is not theoretical: Enter used to post the SAME
// receipt a second time while the first was in flight, and typing changed
// quantities the request no longer reflected.
func TestReceive_TheFreezeIsAnAllowList(t *testing.T) {
	lines := receiveSweepLines()
	build := receiveHarness(t, func() *receiveFake {
		return &receiveFake{sheet: receiveWorksheet(lines...)}
	}, lines, 80, 24)
	r, s := build(t)
	s.qty[1].SetValue("2")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
	// Fire the receipt without the pump: it is genuinely still out.
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !s.pending {
		t.Fatalf("the receipt did not go out")
	}
	if s.qty[1].Focused() || s.notes.Focused() {
		t.Error("a caret is still blinking in a field whose contents have already gone")
	}

	before := receiveState(s)
	for _, k := range poKeySpace() {
		if k == "esc" {
			continue // the one key the bar names, and the only way out
		}
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
		if got := receiveState(s); got != before {
			t.Fatalf("%q changed the form while the receipt was in flight:\n%s\n%s", k, before, got)
		}
	}
	// And a frozen key said WHY rather than "does nothing": it works one second
	// from now, and an operator watching a slow gateway will press it again.
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
	r = next.(Root)
	if !strings.Contains(r.View(), "frozen until the receipt answers") {
		t.Errorf("a frozen key did not say so:\n%s", r.View())
	}
	if cmd == nil {
		t.Error("a frozen key answered with nothing at all")
	}
}

// TestReceive_TheFailedReceiptIsSweptWhereItsBarDiffers keeps the failed-write
// case honest about why it exists.
//
// The rule this file's own commit put in AGENTS.md is that a phase's cases must
// span every state its BAR changes shape in, because that is exactly where the
// honesty rule can break. A standing failure DETAIL is the only input left that
// moves the pinned header — the note block has been a constant height since the
// reservation became unconditional — and headerRows is what the paging claim is
// measured against. But a taller header only CHANGES the answer on a pane where
// the rows it costs are the rows that decide whether the body overflows: at the
// usual pair the body overflows either way, so a case swept only there reaches
// the state and never reaches the bar.
//
// So the swept set is required to contain such a height, and the height is
// derived rather than named — see receiveFailureHeights for what naming it cost.
func TestReceive_TheFailedReceiptIsSweptWhereItsBarDiffers(t *testing.T) {
	var failure *receivePhaseCase
	for _, c := range receivePhaseCases() {
		if c.fake().failWith != 0 {
			c := c
			failure = &c
		}
	}
	if failure == nil {
		t.Fatal("no case reaches a frame with a failed write standing, so every arm of " +
			"the header allocator that exists because a detail is standing is swept by nothing")
	}

	flip, ok := receiveFailureFlipHeight()
	if !ok {
		t.Fatal("no pane between 10 and 60 rows is one where a standing failure detail " +
			"changes whether the bar names PgUp/PgDn — so the failed-write case is " +
			"ordinary coverage and the arm it exists for is swept by nothing. Either the " +
			"header allocator stopped costing the body rows, or the review's body stopped " +
			"being able to overflow")
	}
	swept := false
	for _, h := range failure.paneSizes() {
		if h == flip {
			swept = true
		}
	}
	if !swept {
		t.Errorf("the failed-write case is swept at %v and the bar differs at 80x%d, which "+
			"is not among them — the case reaches the state without reaching the bar it "+
			"was added for", failure.paneSizes(), flip)
	}
	// And the case really does leave a failure standing at the height it is
	// about: a search that found a flip point proves nothing if the reach never
	// puts a detail on the pane.
	r, s := receiveHarness(t, failure.fake, failure.lines, 80, flip)(t)
	r = failure.reach(t, r, s)
	if s.failDetail == "" {
		t.Errorf("at 80x%d the case did not leave a failure standing", flip)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// Every body line belongs to a navigable row
// ---------------------------------------------------------------------------

// TestReceive_EveryBodyLineBelongsToANavigableRow is the rule of this screen's
// bodies, checked over EVERY phase rather than over the one it was written for.
//
// jdeLines.Window anchors the window on the CURSOR's block. A line tagged
// jdeNoRow ahead of the first block is therefore a line no key can bring onto
// the pane — while the layer goes on drawing "↑ N more above" and counting it,
// so the frame tells the operator there is content up there and then refuses
// every key they reach for.
//
// It was closed on the quantity form and left open one phase over, which is the
// omission this sweep exists to make impossible rather than to remember:
// serialBody led with a heading, a counter and a blank, all jdeNoRow, and the
// Serial field was the only line belonging to row 0 — so at 80x17 the item
// label the serial is being scanned AGAINST sat behind a marker no key on that
// phase can act on, because keySerial binds Enter and Esc and nothing else.
//
// The roster is DERIVED twice over: the phases come from receivePhaseCases,
// which TestReceive_EveryPhaseIsSwept walks against the receivePhase iota, and
// the body comes from the screen's own body() — the same expression View draws
// through. A phase added tomorrow is swept because the iota says so, and its
// body is whatever View would really build.
func TestReceive_EveryBodyLineBelongsToANavigableRow(t *testing.T) {
	seen := map[receivePhase]bool{}
	for _, c := range receivePhaseCases() {
		for _, height := range c.paneSizes() {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				r, s := receiveHarness(t, c.fake, c.lines, 80, height)(t)
				r = c.reach(t, r, s)
				if s.phase != c.phase {
					t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
				}
				seen[s.phase] = true
				body, cursor := s.body()
				if body.Len() == 0 {
					t.Fatalf("%s draws no body at all, so this case judges nothing", c.name)
				}
				for i, row := range body.row {
					if row == jdeNoRow {
						t.Errorf("body line %d (%q) belongs to no navigable row, so no key "+
							"can bring it onto the pane once the body overflows",
							i, strings.Join(strings.Fields(body.text[i]), " "))
					}
				}
				// The cursor's own block has to exist, because a body whose
				// every line is tagged to a row NOBODY is standing on is the
				// same defect wearing the tag: block() answers (0,0) for a row
				// that owns nothing, and the window is then positioned against
				// a row that is not there.
				if first, last := body.block(cursor); first == last && body.row[first] != cursor {
					t.Errorf("no line belongs to the cursor's row %d, so the window is "+
						"anchored on a row that owns nothing", cursor)
				}
			})
		}
	}
	// The sweep has to have reached every phase it claims to cover; a reach
	// that quietly landed elsewhere would leave a body judged by nothing.
	for p := receivePhase(0); p < receivePhaseCount; p++ {
		if !seen[p] && receivePhasesWithoutKeys[p] == "" {
			t.Errorf("phase %v was never reached, so its body was never checked", p)
		}
	}
}

// TestReceive_ABodyWithOneRowNeverHidesLinesAboveTheWindow is the behavioural
// half of the rule above, on the states where getting it wrong is permanent.
//
// jdeLines.Window draws "↑ N more above" whenever it starts past the body's
// first line, and that marker is a claim: there is content up there, fetch it.
// On a body with exactly ONE navigable row there is nowhere for a cursor to go
// — not one key on the screen can change which lines the window holds — so a
// marker drawn there can never be acted on, whatever the bar names. That is the
// state serial capture is always in (keySerial binds Enter and Esc, body()
// anchors on row 0), the state the summary is always in, and the state an order
// with nothing receivable is in.
//
// It is the ABOVE marker this holds, and the narrowing is deliberate rather
// than an oversight to widen later. Window draws BOTH, and on a one-row body
// neither can be acted on — but only one of them is this sheet's to prevent.
// The above-marker appears when the window starts past line 0, which is a
// consequence of where the sheet puts its lines, and pinning every line to the
// one block is what makes it impossible. The below-marker appears when the
// block outruns the pane, which no arrangement of one block can avoid: measured
// across the swept phases it is drawn from 80x14 to 80x18 on serial capture,
// the summary and the nothing-receivable order. Suppressing it would mean
// changing what jdeLines.Window emits, which is the shared layer forty screens
// read, and the block is ordered precisely so that what a short pane keeps is
// what the operator cannot do without — the box a scanner fires into, the
// sentence explaining an order with nothing on it. So the TAIL is the accepted
// loss, and the residual — that the frame counts lines it cannot fetch — is a
// property of the layer's marker rather than of this body, recorded rather than
// asserted away.
//
// The set is derived from the body rather than listed: "how many rows does this
// body have?" is a question the built body answers, and a roster of phases
// would be one more list to forget to extend — which is how the serial and
// summary bodies kept their unreachable leads for a round after the quantity
// form lost its own.
func TestReceive_ABodyWithOneRowNeverHidesLinesAboveTheWindow(t *testing.T) {
	judged := 0
	for _, c := range receivePhaseCases() {
		for height := 10; height <= 30; height++ {
			t.Run(fmt.Sprintf("%s at 80x%d", c.name, height), func(t *testing.T) {
				r, s := receiveHarness(t, c.fake, c.lines, 80, height)(t)
				r = c.reach(t, r, s)
				if s.phase != c.phase {
					t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
				}
				body, _ := s.body()
				if rows := body.rowsIn(0, body.Len()); rows != 1 {
					return // the cursor has somewhere to go; a marker there is actionable
				}
				judged++
				if pane := receiveClippedPane(s, 80, height); strings.Contains(pane, "more above") {
					t.Errorf("%s has one navigable row and hides lines above the window, "+
						"behind a marker no key on this screen can act on:\n%s", c.name, pane)
				}
			})
		}
	}
	if judged == 0 {
		t.Error("no swept state had a single navigable row, so this property judged nothing")
	}
}
