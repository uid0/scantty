package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

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

// receiveSweepFake answers the receipt so the frozen and post-receipt states
// are reached the way an operator reaches them, rather than by writing fields.
// It blocks nothing: the in-flight states are reached by firing the key with a
// bare Update instead of pumping it, which is what leaves the request genuinely
// out.
type receiveSweepFake struct{ receiveFails, serialFails bool }

// receiveSweepGateway is what a failing receipt answers with: a gateway page
// rather than a DRF envelope, because omsapi.parseError puts the ENTIRE raw
// body into APIError.Message when the envelope carries no code — so this is
// what really reaches the failure detail, and the detail's height is the only
// thing left that varies the pinned header.
const receiveSweepGateway = "<html><head><title>502 Bad Gateway</title></head>" +
	"<body><center><h1>502 Bad Gateway</h1></center><hr><center>nginx</center></body></html>"

func (f *receiveSweepFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/receive/"):
			if f.receiveFails {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(receiveSweepGateway))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 5, "po_number": "PO-1001",
				"total_received_quantity": 2, "total_quantity": 9,
			})
		case strings.HasSuffix(r.URL.Path, "/serialized-components/"):
			if f.serialFails {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"detail":"the component could not be created"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sc-1", "serial_number": "SN"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "in_stock"})
		}
	}
}

// receiveSweepLines is the order every state is reached against: one plain
// line, one SERIALIZED line (so the receipt opens capture), and one kit (so its
// caveat and its credit block are on the pane while the keys are pressed). The
// kit line is also what makes the body tall enough to page at 24 rows, which is
// the only state where PgUp/PgDn are named.
func receiveSweepLines() []omsapi.PurchaseOrderItem {
	serialized := poPlainLine()
	serialized.ID = 14
	serialized.Description = "Serialized controller board"
	serialized.ItemDetails = map[string]any{"id": "itm-b", "is_serialized": true}
	return []omsapi.PurchaseOrderItem{poKitFixtureLine(), poPlainLine(), serialized}
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
	lines []omsapi.PurchaseOrderItem
	// fake is what OMS answers with while this state is reached and pressed.
	// It is per-case because a FAILED reply is a state of its own: since the
	// note block became a constant height, the failure detail is the only
	// input left that varies the pinned header, and headerRows is what the
	// paging claim is measured against.
	fake receiveSweepFake
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
	all := receiveSweepLines()

	// pressed drives with the pump, so a receipt or a capture is resolved.
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
	// reach, and every reach still goes through the real receive endpoint.
	// Typing it a rune at a time costs a cursor-blink timer per key, which this
	// sweep pays on every rebuild — and it rebuilds for every key it presses.
	typedQty := func(qty map[int]string, drive func(*testing.T, Root, *ReceiveFormScreen) Root) func(*testing.T, Root, *ReceiveFormScreen) Root {
		return func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			for i, v := range qty {
				s.qty[i].SetValue(v)
			}
			return drive(t, r, s)
		}
	}
	// receiveOne books one unit of the SERIALIZED line, which is what opens
	// capture. Line 3 (index 2) is that line.
	receiveOne := map[int]string{2: "1"}

	ok := receiveSweepFake{}
	receiptFails := receiveSweepFake{receiveFails: true}
	captureFails := receiveSweepFake{serialFails: true}

	return []receivePhaseCase{
		{phaseQty, "qty", true, all, ok, nil, pressed()},
		// The same phase with something typed: the Esc label changes to
		// "Discard & back", and the paging keys are named because the kit block
		// has grown a credit breakdown.
		{phaseQty, "qty entered", true, all, ok, nil, typedQty(map[int]string{0: "2"}, pressed())},
		// An order with nothing receivable. Enter cannot receive, so the bar
		// must not name it — the state the honesty rule is easiest to get wrong
		// in, because the form still draws a notes row and a caret.
		{phaseQty, "qty nothing receivable", true, nil, ok, nil, pressed()},
		// A box holding a ZERO. This is a different STATE of the quantity
		// phase, not a different phase, which is exactly why it had to be added
		// by hand: walking the receivePhase iota makes a phase impossible to
		// forget and says nothing about the states inside one. Enter used to be
		// named here — a box "holds something" — while submit skipped the zero,
		// posted nothing and came straight back with a local refusal, so the
		// bar named a key whose whole effect was to write a note.
		{phaseQty, "qty all zero", true, all, ok, nil, typedQty(map[int]string{1: "0"}, pressed())},
		// Frozen: the receipt is out and the payload has already gone.
		{phaseQty, "qty receiving", false, all, ok, nil, typedQty(receiveOne, inFlight(enter))},
		{phaseSerial, "serial", true, all, ok, nil, typedQty(receiveOne, pressed(enter))},
		{phaseSerial, "serial saving", false, all, ok, nil, typedQty(receiveOne,
			func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				r = receiveKey(t, r, enter) // receive -> capture
				s.serialInput.SetValue("SN-1")
				next, _ := r.Update(enter) // the create is genuinely out
				return next.(Root)
			})},
		// The summary, reached by finishing capture with Esc.
		{phaseDone, "done", false, all, ok, nil, typedQty(receiveOne,
			pressed(enter, tea.KeyMsg{Type: tea.KeyEsc}))},
		// A FAILED receipt, standing. This is the state the last three fix
		// rounds were written for and the one no sweep had ever reached: the
		// failure detail is pinned in the same header as the note, so it is the
		// only input left that moves headerRows — and headerRows is what
		// qtyPagesFor measures the paging claim against, so the bar can
		// genuinely be a different bar here. Every arm of the header allocator
		// that exists because a detail is standing (headerFloor's extra row,
		// headerSplit's detail floor) is unreachable without it.
		{phaseQty, "qty after a failed receipt", true, all, receiptFails, receiveFailureHeights,
			typedQty(receiveOne, func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				r = receiveKey(t, r, enter)
				if s.failDetail == "" {
					t.Fatalf("the receipt did not fail, so this case is not the state it names")
				}
				return r
			})},
		// And a failed CAPTURE, which changes the serial bar's own wording:
		// serialBar reads "Save serial" with a filled box and "Retry serial"
		// once an attempt has failed, so this is a state its bar changes shape
		// in — checked rather than assumed, which is what the rule asks.
		{phaseSerial, "serial after a failed capture", true, all, captureFails, nil,
			typedQty(receiveOne, func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				r = receiveKey(t, r, enter) // receive -> capture
				s.serialInput.SetValue("SN-1")
				r = receiveKey(t, r, enter) // the create comes back failed
				if s.serialErr == "" {
					t.Fatalf("the capture did not fail, so this case is not the state it names")
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
	fmt.Fprint(&b, s.phase, "|", s.pending, s.focused,
		"|", s.notes.Value(), s.notes.Focused(),
		"|", s.serialInput.Value(), s.serialInput.Focused(),
		"|", s.serialPending, s.serialCursor, len(s.serialUnits),
		"|", s.createdCount, s.inStockCount, s.skippedCount, s.failedCount,
		"|", s.receipt, s.terminalWidth, s.terminalHeight, len(s.lines))
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
	"jdeScreen": true, "phase": true, "pending": true, "focused": true,
	"qty": true, "notes": true, "serialInput": true,
	"serialUnits": true, "serialCursor": true, "serialPending": true,
	"createdCount": true, "inStockCount": true, "skippedCount": true, "failedCount": true,
	"receipt": true, "lines": true, "po": true,
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
	"serialErr": "one capture's failure: written by a reply off the wire, and cleared by " +
		"the press that re-attempts the unit (submitSerial, on both branches)",
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
	"qty": true, "notes": true, "serialInput": true,
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

// ---------------------------------------------------------------------------
// The bar names exactly the keys that work
// ---------------------------------------------------------------------------

// receiveBarKeyNames maps a bar entry's Key to the keystrokes it SPELLS, and to
// no synonyms. Credit for a synonym would be the sweep making a claim on the
// bar's behalf — the defect it exists to report, sitting inside the check.
var receiveBarKeyNames = map[string][]string{
	"Enter":     {"enter"},
	"Esc":       {"esc"},
	"Enter/Esc": {"enter", "esc"},
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
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
func receiveHarness(t *testing.T, fake receiveSweepFake, lines []omsapi.PurchaseOrderItem, width, height int) func(*testing.T) (Root, *ReceiveFormScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	return func(t *testing.T) (Root, *ReceiveFormScreen) {
		t.Helper()
		s := NewReceiveFormScreen(deps, &omsapi.PurchaseOrder{
			ID: 5, Number: "PO-1001", Items: lines,
		})
		r := newTestRoot(s)
		r.deps = deps
		next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
		return next.(Root), s
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

// receiveFailureHeights are the panes the failed-receipt case is swept at.
//
// The extra one is the point of the case. A standing failure detail is the only
// input left that moves the pinned header, so it can only change the BAR at a
// height where the three rows it takes are the three that decide whether the
// body overflows — and for receiveSweepLines that is 38, not 24 or 30, where
// the body overflows either way. Swept only at the usual pair the case would
// reach the state and never reach the bar it exists for.
//
// 38 is a MEASURED height and not a guessed one, and it is checked rather than
// trusted: TestReceive_TheFailedReceiptIsSweptWhereItsBarDiffers fails if the
// swept set stops containing a height at which the failure flips the claim, so
// a re-wording that moves the flip point reports itself instead of quietly
// turning this case into ordinary coverage.
var receiveFailureHeights = []int{24, 30, 38}

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
	return [][]string{nil, {"down"}, {"down", "down", "down"}}
}

// TestReceive_EveryStateNamesExactlyTheKeysThatWork presses the whole key space
// at every state, in both directions: a key the bar names must change
// something, and a key it does not name must not.
//
// A named key is probed from several positions, because a cursor on the first
// row cannot move up and one on the last cannot move down: a key is dead only
// if it does nothing from ANY of them.
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
					acted := false
					for _, probe := range probes {
						pr, ps := fresh(t, probe)
						before := receiveState(ps)
						_, cmd := pr.Update(poPhaseKeyMsg(k))
						if receiveState(ps) != before || poCmdActs(cmd) {
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

// TestReceive_EveryDeclineNamesTheKeyItAnswers is the reason the sweep above
// can pass: a lead that names the key cannot be shared by two keys. Checking
// the property AND its mechanism is deliberate — the mechanism is what a future
// change would break first, and it breaks quietly.
func TestReceive_EveryDeclineNamesTheKeyItAnswers(t *testing.T) {
	build := receiveHarness(t, receiveSweepFake{}, receiveSweepLines(), 80, 24)
	r, s := build(t)
	s.qty[2].SetValue("1")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc}) // finish capture -> summary
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
	build := receiveHarness(t, receiveSweepFake{}, receiveSweepLines(), 80, 24)
	r, s := build(t)
	s.qty[1].SetValue("2")
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

// TestReceive_TheFailedReceiptIsSweptWhereItsBarDiffers keeps the failed-receipt
// case honest about why it exists.
//
// The rule this file's own commit put in AGENTS.md is that a phase's cases must
// span every state its BAR changes shape in, because that is exactly where the
// honesty rule can break. A standing failure DETAIL is the only input left that
// moves the pinned header — the note block has been a constant height since the
// reservation became unconditional — and headerRows is what qtyPagesFor
// measures the paging claim against. But a header three rows taller only
// CHANGES the answer on a pane where those three rows are the ones that decide
// whether the body overflows: at 24 and 30 this order's body overflows either
// way, so a case swept only there reaches the state and never reaches the bar.
//
// So the swept set is required to contain such a height, rather than assumed to.
// If a re-wording moves the flip point off 38 this fails and says so, instead of
// leaving a case that reads as coverage of the failure frame's bar and is not.
func TestReceive_TheFailedReceiptIsSweptWhereItsBarDiffers(t *testing.T) {
	var failure *receivePhaseCase
	for _, c := range receivePhaseCases() {
		if c.fake.receiveFails {
			c := c
			failure = &c
		}
	}
	if failure == nil {
		t.Fatal("no case reaches the quantity phase with a failed receipt, so every arm of " +
			"the header allocator that exists because a detail is standing is swept by nothing")
	}

	differs := false
	for _, height := range failure.paneSizes() {
		r, s := receiveHarness(t, failure.fake, failure.lines, 80, height)(t)
		r = failure.reach(t, r, s)
		if s.failDetail == "" {
			t.Fatalf("at 80x%d the case did not leave a failure standing", height)
		}
		// The same order and the same typed quantities with NO failure on the
		// pane. Copied off the reached screen rather than restated here: a
		// second spelling of the quantities is a second thing to keep in step.
		_, clean := receiveHarness(t, receiveSweepFake{}, failure.lines, 80, height)(t)
		for i := range clean.qty {
			clean.qty[i].SetValue(s.qty[i].Value())
		}
		if s.qtyPages() != clean.qtyPages() {
			differs = true
		}
	}
	if !differs {
		t.Errorf("the failed-receipt case is swept at %v, and at none of those heights does "+
			"the standing detail change whether the bar names PgUp/PgDn — the case reaches "+
			"the state without reaching the bar it was added for", failure.paneSizes())
	}
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
