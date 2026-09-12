package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// The guard against the operator error the SERVER does not refuse.
//
// Measured against a real OMS and recorded in internal/omsapi/testdata/README.md:
// a backwards reading, an order-of-magnitude one and a negative total are each
// accepted with a 201, and on a runtime meter the advance is dual-written into
// Asset.hours_used, which never comes back down. So there is no refusal to
// surface — the choice is between sending a probable typo in silence and naming
// it first, and these pin that it is named, that it NAMES rather than refuses,
// and that one more key really does send it.

func TestMeterValue_ShownWholeOrNotAtAll(t *testing.T) {
	for _, c := range []struct{ served, want string }{
		// Trailing zeros come off — 1289.7500 and 1289.75 are the same number
		// written twice — and no significant digit ever does.
		{"1289.7500", "1289.75"},
		{"6.0833", "6.0833"},
		{"0.0000", "0"},
		{"-9.0000", "-9"},
		{"44120.0000", "44120"},
		// Fourteen digits is what numeric(14,4) holds, and it arrives whole.
		{"9999999999.9999", "9999999999.9999"},
		// An ABSENCE is not a zero: the server sent null, so nothing is drawn
		// and the caller says so in its own words.
		{"", ""},
	} {
		if got := meterValueText(omsapi.DecimalString(c.served)); got != c.want {
			t.Errorf("meterValueText(%q) = %q, want %q", c.served, got, c.want)
		}
	}
}

// EVERY FIGURE CARRIES ITS UNIT, and where the meter has none the server's own
// label for the TYPE stands in rather than a unit this side invented.
func TestMeterFigure_AlwaysSaysWhatTheNumberIsIn(t *testing.T) {
	if got := meterFigure("1289.7500", "hours", "Runtime hours"); got != "1289.75 hours" {
		t.Errorf("figure = %q, want the value and its unit", got)
	}
	if got := meterFigure("42.0000", "", "Cycles"); got != "42 Cycles" {
		t.Errorf("a unit-less meter drew %q; the type label is what stands in", got)
	}
	if got := meterFigure("", "hours", "Runtime hours"); got != "" {
		t.Errorf("an absent value drew %q, want nothing at all", got)
	}
	// A LEDGER DELTA carries its sign, because an advance and a correction back
	// are the same row in two directions rather than two kinds of row.
	if got := meterSignedFigure("48.2500", "hours", ""); got != "+48.25 hours" {
		t.Errorf("a positive delta drew %q, want a leading +", got)
	}
	if got := meterSignedFigure("-9.0000", "hours", ""); got != "-9 hours" {
		t.Errorf("a negative delta drew %q", got)
	}
}

// meterRat screens what a decimal column can actually hold. A typed `1e5` would
// otherwise become 100000 on a row whose label says hours.
func TestMeterRat_TakesOnlyWhatTheColumnHolds(t *testing.T) {
	for _, ok := range []string{"0", "12", "-9", "1289.75", "+3.5", ".5"} {
		if _, got := meterRat(ok); !got {
			t.Errorf("meterRat(%q) refused a plain decimal", ok)
		}
	}
	for _, bad := range []string{"", "   ", "1e5", "1/3", "abc", "1.2.3", "-", ".", "12h"} {
		if _, got := meterRat(bad); got {
			t.Errorf("meterRat(%q) accepted something a decimal column cannot hold", bad)
		}
	}
}

func TestMeterEntryCheck_NamesTheThreeShapesTheServerTakes(t *testing.T) {
	const now = omsapi.DecimalString("1250.5000")
	for _, c := range []struct {
		name     string
		typed    string
		absolute bool
		want     meterEntryVerdict
	}{
		{"an ordinary advance", "1298.75", true,
			meterEntryVerdict{Result: "1298.75"}},
		{"a backwards absolute", "120", true,
			meterEntryVerdict{Result: "120", Backwards: true}},
		{"one extra digit", "12505", true,
			meterEntryVerdict{Result: "12505", Magnitude: true}},
		{"a negative total", "-5", true,
			meterEntryVerdict{Result: "-5", Backwards: true, Negative: true}},
		{"an ordinary delta", "8.25", false,
			meterEntryVerdict{Result: "1258.75"}},
		// A NEGATIVE DELTA is backwards for the same reason an absolute below
		// the current total is: the meter ends up lower than it was.
		{"a negative delta", "-100", false,
			meterEntryVerdict{Result: "1150.5", Backwards: true}},
		// An unparseable box is the SUBMIT's refusal to make, not this one's.
		{"not a number", "abc", true, meterEntryVerdict{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := meterEntryCheck(now, c.typed, c.absolute)
			if got != c.want {
				t.Errorf("meterEntryCheck(%q, %q, absolute=%v) = %+v, want %+v",
					now, c.typed, c.absolute, got, c.want)
			}
		})
	}
}

// THE MAGNITUDE TEST IS SKIPPED AT ZERO, and that is a decision: every entry
// against a fresh meter is infinitely many times its current value, so the test
// would fire on the FIRST reading of every meter anybody ever created — and a
// confirm that is always shown is a confirm nobody reads.
func TestMeterEntryCheck_AFreshMeterIsNotAMagnitudeJump(t *testing.T) {
	got := meterEntryCheck("0.0000", "1250.5", true)
	if got.Suspicious() {
		t.Errorf("the first reading on a fresh meter was called suspicious: %+v", got)
	}
	if got.Result != "1250.5" {
		t.Errorf("result = %q, want the entry itself", got.Result)
	}
}

// GOING BACKWARDS IS WHAT AN ADJUSTMENT IS FOR, so the backwards test is off
// there — a confirm that fired on the correct use of the instrument would teach
// the operator to dismiss it. A slipped digit still speaks, whichever action
// typed it.
func TestMeterAdjustCheck_ACorrectionDownIsNotSuspicious(t *testing.T) {
	down := meterAdjustCheck("1250.5000", "1200")
	if down.Suspicious() {
		t.Errorf("a correction DOWN was called suspicious: %+v — that is what adjust is for", down)
	}
	up := meterAdjustCheck("1250.5000", "12505")
	if !up.Magnitude {
		t.Errorf("an order-of-magnitude correction was not flagged: %+v", up)
	}
	neg := meterAdjustCheck("1250.5000", "-5")
	if !neg.Negative {
		t.Errorf("a negative total was not flagged on an adjustment: %+v", neg)
	}
}

// THE RUNTIME CAVEAT IS SAID ONLY WHERE IT IS TRUE. apply_reading dual-writes
// Asset.hours_used for a runtime_hours meter and for no other kind, so claiming
// an unrecoverable loss on a gallons meter would be a warning the code cannot
// honour — which this codebase treats as the same defect as silence about a real
// one.
func TestMeterRuntimeCaveat_OnlyWhereTheDualWriteReaches(t *testing.T) {
	jump := meterEntryVerdict{Result: "12505", Magnitude: true}
	runtime := omsapi.AssetMeter{MeterType: omsapi.MeterTypeRuntimeHours}
	gallons := omsapi.AssetMeter{MeterType: omsapi.MeterTypeVolumeGallons}

	if got := meterRuntimeCaveat(runtime, jump); got == "" {
		t.Error("a magnitude jump on a RUNTIME meter says nothing about the logged hours")
	} else if !strings.Contains(got, "logged hours") {
		t.Errorf("the caveat reads %q; it must name what else the advance moves", got)
	}
	if got := meterRuntimeCaveat(gallons, jump); got != "" {
		t.Errorf("a gallons meter claimed an unrecoverable loss it does not have: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Driven through the real keys
// ---------------------------------------------------------------------------

// A BACKWARDS READING IS NAMED BEFORE IT IS SENT, and Enter alone does not send
// it. Enter is the key that opened the form and the one a hand reaches for next,
// so a reflex must not be enough.
func TestAssetMeters_ABackwardsReadingAsksBeforeItIsSent(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "120")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != meterPhaseConfirm {
		t.Fatalf("phase = %v after a backwards reading, want the confirm", screen.phase)
	}
	if w := fake.writes(); len(w) != 0 {
		t.Fatalf("a backwards reading was sent before anything asked: %+v", w)
	}

	// The frame says WHICH WAY it goes and names BOTH figures with their unit,
	// because the question is "did you mean to go from A to B" and a warning
	// stating only the conclusion cannot be checked against the machine.
	pane := assetFlatPane(screen, 80, 30)
	for _, want := range []string{"BACKWARDS", "1250.5 hours", "120 hours"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the confirm does not say %q:\n%s", want, pane)
		}
	}
	// Enter is NOT the key that sends it.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if w := fake.writes(); len(w) != 0 {
		t.Fatalf("Enter on the confirm sent the reading: %+v — a reflex must not be enough", w)
	}
	if screen.phase != meterPhaseConfirm {
		t.Fatalf("Enter moved off the confirm to %v", screen.phase)
	}

	// IT NAMES, IT DOES NOT REFUSE: one more key sends exactly what was typed.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("Ctrl-X made %d writes, want 1: %+v", len(writes), writes)
	}
	if writes[0].Body["value"] != "120" {
		t.Errorf("value = %v, want the operator's own %q — nothing here corrects a number",
			writes[0].Body["value"], "120")
	}
	if writes[0].Path != "/api/inventory/asset-meters/m-1/record-reading/" {
		t.Errorf("sent to %s", writes[0].Path)
	}
}

// AN ORDER-OF-MAGNITUDE READING ON A RUNTIME METER SAYS WHAT CANNOT BE TAKEN
// BACK. This is the case the whole guard exists for: the advance is dual-written
// into Asset.hours_used, the maintenance forecast reads that, and no endpoint in
// this client can bring it down again.
func TestAssetMeters_AMagnitudeJumpNamesTheLossThatCannotBeUndone(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "12505000")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != meterPhaseConfirm {
		t.Fatalf("phase = %v after a 10,000x reading, want the confirm", screen.phase)
	}
	pane := assetFlatPane(screen, 80, 30)
	for _, want := range []string{"extra digit", "logged hours", "never"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the confirm does not say %q:\n%s", want, pane)
		}
	}
	// And it says the server would take it, so an operator who means it is not
	// left wondering whether the program is going to stop them.
	if !strings.Contains(pane, "server accepts") {
		t.Errorf("the confirm does not say the server would accept it:\n%s", pane)
	}
}

// ESC GOES BACK TO THE ENTRY, not to the grid. Throwing the operator's number
// away to make them retype it would be discarding input in answer to "are you
// sure".
func TestAssetMeters_BackingOutOfTheConfirmKeepsTheEntry(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "120")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	if screen.phase != meterPhaseRecord {
		t.Fatalf("Esc left the phase at %v, want the form the entry was typed on", screen.phase)
	}
	if screen.valueInput.Value() != "120" {
		t.Errorf("the typed value is %q after backing out, want it kept", screen.valueInput.Value())
	}
	if w := fake.writes(); len(w) != 0 {
		t.Fatalf("backing out wrote something: %+v", w)
	}
}

// AN ORDINARY READING NEVER SEES THE CONFIRM. A guard that fires on the ordinary
// case is a guard the operator learns to dismiss, which costs exactly the
// attention the real one needs.
func TestAssetMeters_AnOrdinaryReadingIsNotAskedAbout(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "1298.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase == meterPhaseConfirm {
		t.Fatal("an ordinary advance opened the confirm")
	}
	if len(fake.writes()) != 1 {
		t.Fatalf("an ordinary advance made %d writes, want 1", len(fake.writes()))
	}
}

// THE CONFIRM'S BAR AND ITS ARM READ ONE EXPRESSION, so a key the bar does not
// name cannot act — and every key the frame does not bind ANSWERS, because a
// frame holding no cursor and no caret would otherwise redraw a pane that is a
// pure function of unchanged state.
func TestAssetMeters_TheConfirmAnswersEveryKeyItDoesNotBind(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "120")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	before := assetFlatPane(screen, 80, 30)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	after := assetFlatPane(screen, 80, 30)
	if before == after {
		t.Errorf("pressing z on the confirm redrew a byte-identical pane, which reads as a "+
			"wedged program:\n%s", stripANSI(after))
	}
	if !strings.Contains(screen.note, "does nothing here") {
		t.Errorf("the answer to an inert key reads %q", screen.note)
	}
}

// While a write is OUT the bar drops the key that would not act, and Ctrl-X does
// nothing — but Esc still leaves, because a frame with no way off it while a
// slow gateway thinks is the worse defect.
func TestAssetMeters_TheConfirmBarFollowsTheWriteInFlight(t *testing.T) {
	screen := assetMetersFixture()
	screen.setSize(tea.WindowSizeMsg{Width: 80, Height: 30})
	screen.pendingMeter = screen.meters[0]
	screen.pendingVal = meterEntryCheck(screen.meters[0].CurrentValue, "120", true)
	screen.phase = meterPhaseConfirm

	named := func() map[string]bool {
		out := map[string]bool{}
		for _, it := range screen.confirmBar() {
			out[it.Key] = true
		}
		return out
	}
	if !named()["Ctrl-X"] {
		t.Fatal("the bar does not name Ctrl-X on a confirm that can write")
	}
	screen.saving = true
	if named()["Ctrl-X"] {
		t.Error("the bar still names Ctrl-X while a write is out, and the arm declines it — " +
			"a key named and inert is rule 2 broken in the direction nobody can see")
	}
	if !named()["Esc"] {
		t.Error("the bar dropped Esc while the write was out, leaving no way off the frame")
	}
}

// A CONFIRM STANDING ON A METER THAT IS GONE WRITES NOTHING. A reload can land
// under an open form, and a positional index followed blindly would name one
// meter on the prompt and write to another.
func TestAssetMeters_AConfirmOnAVanishedMeterWritesNothing(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "120")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != meterPhaseConfirm {
		t.Fatalf("phase = %v, want the confirm", screen.phase)
	}

	// The meter is deleted elsewhere and the list reloads under the open frame.
	next, _ := screen.Update(assetMetersLoadedMsg{meters: nil})
	sc := next.(*AssetMetersScreen)
	if sc.phase != meterPhaseList {
		t.Errorf("phase = %v after the meter vanished, want back on the grid", sc.phase)
	}
	if !strings.Contains(sc.note, "no longer") {
		t.Errorf("the note reads %q; it must say why the frame closed", sc.note)
	}
	// And Ctrl-X now writes nothing, because there is nothing to write to.
	sc.phase = meterPhaseConfirm
	sc.pendingMeter = omsapi.AssetMeter{}
	if _, cmd := sc.Update(tea.KeyMsg{Type: tea.KeyCtrlX}); cmd != nil {
		t.Error("Ctrl-X issued a write against a meter the screen no longer has")
	}
}
