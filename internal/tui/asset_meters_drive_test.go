package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// End-to-end drives of the bench gesture this work exists for: an operator
// standing at a machine reads a number off it and records it from the terminal.
//
// Pumped through Root.Update against a stateful httptest fake, since no shared
// OMS is reachable from a task worktree — the shape wo_materials_drive_test.go
// established and which every drive in this package reuses. ROOT level rather
// than screen level on purpose: these screens are reached by letters (M and D on
// the asset sheet, n on the meter grid) and only WantsRawInput keeps the ones
// inside a form, so a screen-level drive would pass while the real app walked
// somewhere else.
//
// WHAT THEY ASSERT IS THE REQUEST, not a field on the screen. A meter reading
// that reaches the wrong endpoint, or arrives with is_absolute silently dropped,
// is a wrong number stored with nothing on screen to say so — so the fake keeps
// every method, path and body and the assertions read those.

// meterFake is an asset with meters and a document library, and it remembers
// what was asked of it.
type meterFake struct {
	mu       sync.Mutex
	requests []meterRequest
	meters   []map[string]any
	readings []map[string]any
	docs     []map[string]any
	// refuse, when set, answers the next write with this status and body — the
	// server's own hand-written {"detail": …} shape.
	refuseStatus    int
	refuseBody      string
	refuseMeterList bool
}

type meterRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

func (f *meterFake) seen() []meterRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]meterRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// writes returns the requests that CHANGE something, which is what a drive is
// about: a list reloaded three times says nothing about what was stored.
func (f *meterFake) writes() []meterRequest {
	var out []meterRequest
	for _, r := range f.seen() {
		if r.Method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

func (f *meterFake) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		body := map[string]any{}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		f.requests = append(f.requests, meterRequest{r.Method, r.URL.Path, body})
		if f.refuseMeterList && r.Method == http.MethodGet &&
			strings.Contains(r.URL.Path, "/asset-meters/") {
			f.refuseMeterList = false
			http.Error(w, "meter list unavailable", http.StatusServiceUnavailable)
			return
		}

		page := func(rows []map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"count": len(rows), "next": nil, "previous": nil, "results": rows,
			})
		}
		if r.Method != http.MethodGet && f.refuseStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(f.refuseStatus)
			_, _ = io.WriteString(w, f.refuseBody)
			return
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/record-reading/"),
			strings.HasSuffix(r.URL.Path, "/adjust/"):
			// The server applies the reading and answers with BOTH the meter as
			// it now stands and the ledger row that moved it — the envelope both
			// actions really return.
			adjust := strings.HasSuffix(r.URL.Path, "/adjust/")
			meter := f.meters[0]
			value := fmt.Sprint(body["value"])
			if adjust {
				value = fmt.Sprint(body["target"])
			}
			if !adjust {
				if abs, ok := body["is_absolute"].(bool); ok && !abs {
					// A DELTA: the meter advances by the typed amount.
					var now, d float64
					fmt.Sscanf(fmt.Sprint(meter["current_value"]), "%g", &now)
					fmt.Sscanf(value, "%g", &d)
					value = fmt.Sprintf("%.4f", now+d)
				}
			}
			meter["current_value"] = value
			source := omsapi.MeterSourceManual
			if adjust {
				source = omsapi.MeterReadingSourceManualAdjust
			}
			reading := map[string]any{
				"id": fmt.Sprintf("r-%d", len(f.readings)+1), "meter": meter["id"],
				"source": source, "delta": value, "value_after": value,
				"is_estimated": body["is_estimated"] == true,
				"observed_at":  "2026-09-11T14:05:00Z", "recorded_at": "2026-09-11T14:05:00Z",
				"notes": body["reason"],
			}
			f.readings = append([]map[string]any{reading}, f.readings...)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"meter": meter, "reading": reading})

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/asset-meters/"):
			meter := map[string]any{
				"id": fmt.Sprintf("m-%d", len(f.meters)+1), "asset": body["asset"],
				"name": body["name"], "meter_type": body["meter_type"],
				"meter_type_display": "Cycles", "unit": body["unit"],
				"source": body["source"], "source_display": "Manual entry",
				"current_value": "0.0000", "is_active": true,
			}
			f.meters = append(f.meters, meter)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(meter)

		case strings.Contains(r.URL.Path, "/asset-meter-readings/"):
			page(f.readings)
		case strings.Contains(r.URL.Path, "/asset-meters/"):
			page(f.meters)
		case strings.Contains(r.URL.Path, "/asset-documents/"):
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			page(f.docs)
		case strings.Contains(r.URL.Path, "/assets/"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "a1", "name": assetMeterFixtureAsset, "asset_tag": "ASSET-0042",
			})
		default:
			page(nil)
		}
	}
}

func meterFakeWithSpindle() *meterFake {
	return &meterFake{meters: []map[string]any{{
		"id": "m-1", "asset": "a1", "name": "Spindle runtime",
		"meter_type": omsapi.MeterTypeRuntimeHours, "meter_type_display": "Runtime hours",
		"unit": "hours", "source": omsapi.MeterSourceManual, "source_display": "Manual entry",
		"current_value": "1250.5000", "is_active": true,
	}}}
}

// meterDrive stands the operator on the meter grid of an asset, through a real
// Root at a real terminal size.
func meterDrive(t *testing.T, fake *meterFake) (Root, *AssetMetersScreen, func()) {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewAssetMetersScreen(deps, "a1", assetMeterFixtureAsset)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	return r, screen, srv.Close
}

func meterType(t *testing.T, r Root, text string) Root {
	t.Helper()
	for _, ch := range text {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	return r
}

func TestAssetMeters_AReadingAfterFailedRefreshIsConfirmedThenWritten(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	fake.mu.Lock()
	fake.refuseMeterList = true
	fake.mu.Unlock()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if screen.loadErr == "" {
		t.Fatal("the failed refresh did not leave the meter list marked stale")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "1298.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != meterPhaseConfirm {
		t.Fatalf("an unchecked ordinary reading left phase %v, want confirm", screen.phase)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Fatalf("the unchecked reading was written before Ctrl-X: %+v", writes)
	}
	pane := assetFlatPane(screen, 80, 30)
	if !strings.Contains(pane, "could not be checked against the server") {
		t.Fatalf("the confirm does not name the unchecked comparison:\n%s", stripANSI(pane))
	}
	if !strings.Contains(screen.confirmHeadline(), "1250.5 hours?") {
		t.Errorf("the stale figure is not marked unconfirmed in %q", screen.confirmHeadline())
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	writes := fake.writes()
	if len(writes) != 1 || writes[0].Body["value"] != "1298.75" {
		t.Fatalf("Ctrl-X writes = %+v, want exactly the typed reading", writes)
	}
}

func TestAssetMeters_AnAdjustmentAfterFailedRefreshIsConfirmedThenWritten(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	fake.mu.Lock()
	fake.refuseMeterList = true
	fake.mu.Unlock()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlA})
	r = meterType(t, r, "1289.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = meterType(t, r, "recount against the control")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != meterPhaseConfirm {
		t.Fatalf("an unchecked ordinary adjustment left phase %v, want confirm", screen.phase)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Fatalf("the unchecked adjustment was written before Ctrl-X: %+v", writes)
	}
	if pane := assetFlatPane(screen, 80, 30); !strings.Contains(pane, "could not be checked against the server") {
		t.Fatalf("the adjustment confirm does not name the unchecked comparison:\n%s", stripANSI(pane))
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	writes := fake.writes()
	if len(writes) != 1 || writes[0].Body["target"] != "1289.75" {
		t.Fatalf("Ctrl-X writes = %+v, want exactly the typed adjustment", writes)
	}
}

func TestAssetMeters_AReadingDuringRefreshIsConfirmedThenWritten(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	next, refreshCmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	r = next.(Root)
	if refreshCmd == nil || screen.meterValuesConfirmed() {
		t.Fatal("dispatching refresh did not immediately make cached meter values unconfirmed")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "1298.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != meterPhaseConfirm {
		t.Fatalf("an ordinary reading during refresh left phase %v, want confirm", screen.phase)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Fatalf("the unchecked reading was written before Ctrl-X: %+v", writes)
	}
	if pane := assetFlatPane(screen, 80, 30); !strings.Contains(pane, "could not be confirmed against the server") {
		t.Fatalf("the in-flight confirm misstates why the comparison is unchecked:\n%s", stripANSI(pane))
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	writes := fake.writes()
	if len(writes) != 1 || writes[0].Body["value"] != "1298.75" {
		t.Fatalf("Ctrl-X writes = %+v, want exactly the typed reading", writes)
	}
}

func TestAssetMeters_AnAdjustmentDuringRefreshIsConfirmedThenWritten(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	next, refreshCmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	r = next.(Root)
	if refreshCmd == nil || screen.meterValuesConfirmed() {
		t.Fatal("dispatching refresh did not immediately make cached meter values unconfirmed")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlA})
	r = meterType(t, r, "1289.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = meterType(t, r, "recount against the control")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.phase != meterPhaseConfirm {
		t.Fatalf("an ordinary adjustment during refresh left phase %v, want confirm", screen.phase)
	}
	if writes := fake.writes(); len(writes) != 0 {
		t.Fatalf("the unchecked adjustment was written before Ctrl-X: %+v", writes)
	}
	if pane := assetFlatPane(screen, 80, 30); !strings.Contains(pane, "could not be confirmed against the server") {
		t.Fatalf("the in-flight adjustment confirm misstates why it is unchecked:\n%s", stripANSI(pane))
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	writes := fake.writes()
	if len(writes) != 1 || writes[0].Body["target"] != "1289.75" {
		t.Fatalf("Ctrl-X writes = %+v, want exactly the typed adjustment", writes)
	}
}

// THE BENCH GESTURE. Enter on a meter, type the number off the machine, Enter.
// The assertion is the REQUEST: the right endpoint, the operator's own digits,
// and is_absolute explicitly true.
func TestAssetMeters_RecordingAReadingPostsWhatWasTyped(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	if len(screen.meters) != 1 {
		t.Fatalf("the grid loaded %d meters, want 1", len(screen.meters))
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if screen.phase != meterPhaseRecord {
		t.Fatalf("Enter on the grid left the phase at %v, want the record form", screen.phase)
	}
	r = meterType(t, r, "1298.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("the drive made %d writes, want 1: %+v", len(writes), writes)
	}
	got := writes[0]
	if got.Method != http.MethodPost ||
		got.Path != "/api/inventory/asset-meters/m-1/record-reading/" {
		t.Fatalf("recorded to %s %s, want POST .../m-1/record-reading/", got.Method, got.Path)
	}
	if got.Body["value"] != "1298.75" {
		t.Errorf("value = %v, want the operator's own digits %q", got.Body["value"], "1298.75")
	}
	// The BASIS must be on the wire. The server defaults it to true, so a `false`
	// dropped by omitempty would store a delta as an absolute — accepted with a
	// 201, and the number is gone.
	if basis, present := got.Body["is_absolute"]; !present || basis != true {
		t.Errorf("is_absolute = %v (present=%v), want an explicit true", basis, present)
	}
	if got.Body["is_estimated"] != false {
		t.Errorf("is_estimated = %v, want false — nothing said it was eyeballed", got.Body["is_estimated"])
	}
	if screen.phase != meterPhaseList {
		t.Errorf("phase = %v after the write landed, want back on the grid", screen.phase)
	}
	// The screen reports what the SERVER stored, with the unit.
	if !strings.Contains(screen.note, "1298.75") || !strings.Contains(screen.note, "hours") {
		t.Errorf("the note after recording reads %q; it must name the stored value AND its unit",
			screen.note)
	}
}

// A DELTA READING SENDS is_absolute FALSE. This is the one confusion on the
// endpoint that destroys a number in silence rather than refusing.
func TestAssetMeters_ADeltaReadingSaysSoOnTheWire(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "8.25")
	// Down onto the basis row, then ← to flip it to "add this to the counter".
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if screen.recordFocus != meterRecordBasis {
		t.Fatalf("Down left the caret on row %d, want the basis row", screen.recordFocus)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyLeft})
	if screen.recordAbsolute {
		t.Fatal("← on the basis row did not flip it off absolute")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("the drive made %d writes, want 1: %+v", len(writes), writes)
	}
	if basis, present := writes[0].Body["is_absolute"]; !present || basis != false {
		t.Errorf("is_absolute = %v (present=%v), want an explicit false — the server "+
			"defaults it to TRUE, so an omitted one stores 'add 8.25' as 'the meter reads 8.25'",
			basis, present)
	}
	if writes[0].Body["value"] != "8.25" {
		t.Errorf("value = %v, want %q", writes[0].Body["value"], "8.25")
	}
}

// AN ADJUSTMENT IS A DIFFERENT REQUEST TO A DIFFERENT ENDPOINT, and it carries
// the reason the server requires and stores beside the correction.
func TestAssetMeters_AdjustingPostsTheCorrectionAndItsReason(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlA})
	if screen.phase != meterPhaseAdjust {
		t.Fatalf("Ctrl-A on the grid left the phase at %v, want the adjust form", screen.phase)
	}
	r = meterType(t, r, "1289.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = meterType(t, r, "recount against the control")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("the drive made %d writes, want 1: %+v", len(writes), writes)
	}
	got := writes[0]
	if got.Path != "/api/inventory/asset-meters/m-1/adjust/" {
		t.Fatalf("adjusted at %s, want .../m-1/adjust/ — a correction is NOT a reading", got.Path)
	}
	if got.Body["target"] != "1289.75" {
		t.Errorf("target = %v, want %q", got.Body["target"], "1289.75")
	}
	if got.Body["reason"] != "recount against the control" {
		t.Errorf("reason = %v, want the operator's own words", got.Body["reason"])
	}
	// It must NOT carry a reading's keys: those belong to the other endpoint and
	// the two are being kept apart in the record, not merged behind a flag.
	for _, key := range []string{"value", "is_absolute"} {
		if _, present := got.Body[key]; present {
			t.Errorf("the adjustment carried %q — that is the record-reading contract, and "+
				"the two actions are different operations", key)
		}
	}
}

// AN ADJUSTMENT WITHOUT A REASON IS REFUSED HERE AND NOTHING IS SENT. The server
// refuses it too; this is caught first because the operator is standing on the
// frame with the empty box in front of them.
func TestAssetMeters_AnAdjustmentWithoutAReasonWritesNothing(t *testing.T) {
	fake := meterFakeWithSpindle()
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlA})
	r = meterType(t, r, "1289.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if w := fake.writes(); len(w) != 0 {
		t.Fatalf("a reasonless adjustment reached the server: %+v", w)
	}
	if screen.phase != meterPhaseAdjust {
		t.Errorf("phase = %v, want to stay on the form the operator can fix it from", screen.phase)
	}
	if !strings.Contains(screen.errMsg, "reason") {
		t.Errorf("the refusal reads %q; it must say what is missing", screen.errMsg)
	}
	// The refusal LEADS with the load-bearing clause, because fitStatus gives an
	// error one row and lets the circumstance be what a narrow pane takes.
	if !strings.HasPrefix(screen.errMsg, "nothing adjusted") {
		t.Errorf("the refusal reads %q; it must lead with what was NOT done", screen.errMsg)
	}
}

// THE SERVER'S OWN SENTENCE REACHES THE OPERATOR. Both actions write
// `{"detail": …}` by hand in the view body, so they never reach DRF's exception
// handler and parseError hands the whole raw JSON over unless it is recovered.
func TestAssetMeters_AServerRefusalArrivesAsItsOwnSentence(t *testing.T) {
	fake := meterFakeWithSpindle()
	fake.refuseStatus = http.StatusBadRequest
	fake.refuseBody = `{"detail": "value must be a number"}`
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = meterType(t, r, "1298.75")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if screen.errMsg != "value must be a number" {
		t.Errorf("the refusal reads %q, want the server's own sentence — without "+
			"AsDetailRefusal the operator reads the raw JSON envelope", screen.errMsg)
	}
	if strings.Contains(screen.errMsg, "{") {
		t.Errorf("the refusal still carries JSON: %q", screen.errMsg)
	}
	// And the operator is left where they can fix it, with their number intact.
	if screen.phase != meterPhaseRecord {
		t.Errorf("phase = %v after a refusal, want the form the entry was typed on", screen.phase)
	}
	if screen.valueInput.Value() != "1298.75" {
		t.Errorf("the typed value is %q after a refusal; it must survive so it can be corrected",
			screen.valueInput.Value())
	}
}

// DEFINING A METER POSTS A MANUAL ONE, stated rather than defaulted.
func TestAssetMeters_CreatingAMeterPostsAManualSource(t *testing.T) {
	fake := &meterFake{}
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if screen.phase != meterPhaseNew {
		t.Fatalf("n on the grid left the phase at %v, want the new-meter form", screen.phase)
	}
	r = meterType(t, r, "Press cycles")
	// Down onto the type row, then → twice: runtime hours → gallons → cycles.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRight})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRight})
	if got := meterTypeOptions[screen.newTypeIdx].Value; got != omsapi.MeterTypeCycles {
		t.Fatalf("the type row landed on %q, want cycles", got)
	}
	// The UNIT followed the type while nobody had touched it.
	if got := screen.unitInput.Value(); got != "cycles" {
		t.Errorf("unit = %q, want it to follow the type until the operator edits it", got)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("the drive made %d writes, want 1: %+v", len(writes), writes)
	}
	got := writes[0]
	if got.Path != "/api/inventory/asset-meters/" {
		t.Fatalf("created at %s, want the collection route", got.Path)
	}
	for key, want := range map[string]any{
		"asset": "a1", "name": "Press cycles",
		"meter_type": omsapi.MeterTypeCycles, "unit": "cycles",
		"source": omsapi.MeterSourceManual,
	} {
		if got.Body[key] != want {
			t.Errorf("%s = %v, want %v", key, got.Body[key], want)
		}
	}
}

// A UNIT THE OPERATOR TYPED IS NOT OVERWRITTEN by a later change of type.
// Discarding somebody's input in answer to a keypress on another row is never
// this program's answer.
func TestAssetMeters_AnEditedUnitStopsFollowingTheType(t *testing.T) {
	fake := &meterFake{}
	r, screen, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	r = meterType(t, r, "Coolant")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown}) // onto the unit row
	r = meterType(t, r, "L")
	if !strings.HasSuffix(screen.unitInput.Value(), "L") {
		t.Fatalf("typing into the unit row produced %q", screen.unitInput.Value())
	}
	typed := screen.unitInput.Value()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyUp}) // back onto the type row
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRight})
	if screen.unitInput.Value() != typed {
		t.Errorf("changing the type overwrote the operator's unit %q with %q",
			typed, screen.unitInput.Value())
	}
}

// THE LEDGER IS REACHED FROM THE ROW, and it asks for THAT meter's readings.
func TestAssetMeters_CtrlEOpensTheLedgerForTheHighlightedMeter(t *testing.T) {
	fake := meterFakeWithSpindle()
	fake.readings = []map[string]any{{
		"id": "r-1", "meter": "m-1", "source": omsapi.MeterReadingSourceManualAdjust,
		"source_display": "Manual correction", "delta": "-9.0000", "value_after": "1250.5000",
		"observed_at": "2026-09-11T14:05:00Z", "recorded_at": "2026-09-11T14:05:00Z",
		"notes": "recount",
	}}
	r, _, done := meterDrive(t, fake)
	defer done()

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	ledger, ok := r.screen.(*AssetMeterReadingsScreen)
	if !ok {
		t.Fatalf("Ctrl-E landed on %T, want the readings ledger", r.screen)
	}
	if ledger.meter.ID != "m-1" {
		t.Errorf("the ledger opened on meter %q, want the highlighted one", ledger.meter.ID)
	}
	var asked string
	for _, req := range fake.seen() {
		if strings.Contains(req.Path, "/asset-meter-readings/") {
			asked = req.Path
		}
	}
	if asked == "" {
		t.Fatal("the ledger never asked the readings endpoint for anything")
	}
	if len(ledger.readings) != 1 {
		t.Fatalf("the ledger loaded %d readings, want 1", len(ledger.readings))
	}
	// A CORRECTION reads as one on the pane, with its sign and its reason.
	pane := assetFlatPane(ledger, 80, 30)
	for _, want := range []string{"-9", "Manual correction", "recount"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the ledger pane does not show %q:\n%s", want, stripANSI(pane))
		}
	}
}
