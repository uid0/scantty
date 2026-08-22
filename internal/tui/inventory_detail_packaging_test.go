package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// Packaging matrix on the item detail (OMS #979/#981, web #983).

func packDetail(item *omsapi.Item) *InventoryDetailScreen {
	s := NewInventoryDetailScreen(Deps{}, item.ID)
	s.item = item
	s.loading = false
	s.usedByLoading = false
	s.purchasesLoading = false
	// The "is this a kit?" answer has arrived and said no, which is what every
	// stock affordance below is gated on — these fixtures are the screen AFTER
	// it finished loading, not part-way through.
	s.kitAnswered = true
	return s
}

// TestDetailBody_EachModeStockUnchanged is the phase invariant: the stock section
// of an item nobody opted in must read exactly as it did before the matrix.
func TestDetailBody_EachModeStockUnchanged(t *testing.T) {
	s := packDetail(&omsapi.Item{
		ID: "abc", Name: "Widget", SKU: "W-1",
		Stock: 12, MinimumStock: 3, ReorderQuantity: 6,
	})
	body := s.renderBody()

	if !strings.Contains(body, "Current stock: 12") {
		t.Errorf("each-mode body should show the bare stock number: %q", body)
	}
	if !strings.Contains(body, "Minimum: 3") || !strings.Contains(body, "Reorder qty: 6") {
		t.Errorf("each-mode thresholds should stay bare numbers: %q", body)
	}
	// None of the new lines may appear for an opted-out item.
	for _, absent := range []string{"Base units:", "Packaging:", "Counted in"} {
		if strings.Contains(body, absent) {
			t.Errorf("each-mode body must not contain %q: %q", absent, body)
		}
	}
	// And the footer must not advertise the pack keys.
	s.terminalHeight = 40
	if view := s.View(); strings.Contains(view, "p packs") {
		t.Errorf("each-mode footer must not hint the pack keys: %q", view)
	}
}

// TestDetailBody_ByLevelShowsOnHandAtLevel pins the pack-counted rendering.
func TestDetailBody_ByLevelShowsOnHandAtLevel(t *testing.T) {
	s := packDetail(paperItem())
	body := s.renderBody()

	// On hand at the counting granularity, using the server's own text.
	if !strings.Contains(body, "Current stock: 5 ream(s)") {
		t.Errorf("body should show on-hand at the count level: %q", body)
	}
	// The canonical base-unit number is still shown, because that is what every
	// PO, usage log and reorder quantity is stored in.
	if !strings.Contains(body, "Base units: 2500 sheets") {
		t.Errorf("body should also show the base-unit stock: %q", body)
	}
	// Thresholds are read in the count unit, so they are labelled with it.
	if !strings.Contains(body, "Minimum: 2 reams") || !strings.Contains(body, "Reorder qty: 4 reams") {
		t.Errorf("thresholds should carry the count unit: %q", body)
	}
	// The chain, and what the item is counted in.
	if !strings.Contains(body, "1 case = 10 reams") || !strings.Contains(body, "1 ream = 500 sheets") {
		t.Errorf("body should describe the pack chain: %q", body)
	}
	if !strings.Contains(body, "Counted in reams") {
		t.Errorf("body should name the counting unit: %q", body)
	}
	if strings.Contains(body, "sealed + open") {
		t.Errorf("a by_level item must not mention sealed/open: %q", body)
	}
}

// TestDetailBody_OpenClosedShowsSealedSplit pins the open/closed rendering.
func TestDetailBody_OpenClosedShowsSealedSplit(t *testing.T) {
	s := packDetail(bagItem())
	body := s.renderBody()

	if !strings.Contains(body, "Current stock: 3 sealed + 1 open") {
		t.Errorf("body should show the sealed/open split: %q", body)
	}
	if !strings.Contains(body, "Counted in cases (sealed + open)") {
		t.Errorf("body should mark the mode: %q", body)
	}
	// The footer advertises the pack keys only for this mode.
	s.terminalHeight = 40
	if view := s.View(); !strings.Contains(view, "p packs") {
		t.Errorf("open_closed footer should hint the pack keys: %q", view)
	}
}

// TestDetailBody_HalfConfiguredFallsBackToBaseUnits mirrors the backend's own
// conservatism: a pack mode with no usable level keeps base-unit behaviour rather
// than erroring.
func TestDetailBody_HalfConfiguredFallsBackToBaseUnits(t *testing.T) {
	item := paperItem()
	item.CountLevel = nil
	item.OnHandDisplay = &omsapi.OnHandDisplay{Mode: omsapi.CountModeEach, Text: "2500 sheet"}
	body := packDetail(item).renderBody()

	if !strings.Contains(body, "Current stock: 2500 sheets") {
		t.Errorf("half-configured body should show base units: %q", body)
	}
	if strings.Contains(body, "Base units:") || strings.Contains(body, "Counted in") {
		t.Errorf("half-configured body must not claim pack counting: %q", body)
	}
	// The chain itself is still worth showing — it exists.
	if !strings.Contains(body, "1 case = 10 reams") {
		t.Errorf("the chain should still be described: %q", body)
	}
}

// TestCycleCount_AtLevelPayload is the count-at-level half: a pack-counted item
// posts at_level, an each-mode one must not.
func TestCycleCount_AtLevelPayload(t *testing.T) {
	var captured omsapi.CycleCountBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","current_stock":2000}`))
	}))
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL)}

	// by_level: the typed number is whole packs.
	s := packDetail(paperItem())
	s.deps = deps
	s.openCycleCount()
	s.ccQty.SetValue("4")
	_, cmd := s.submitCycleCount()
	if cmd == nil {
		t.Fatal("submitCycleCount returned no command")
	}
	cmd()
	if captured.CountedQty != 4 {
		t.Errorf("counted_qty = %d, want 4", captured.CountedQty)
	}
	if !captured.AtLevel {
		t.Error("a pack-counted item must post at_level")
	}
	if captured.OpenCount != nil {
		t.Errorf("a by_level count must not send open_count, got %v", *captured.OpenCount)
	}

	// each mode: the flag must be absent, so the backend reads base units exactly
	// as it always has.
	captured = omsapi.CycleCountBody{}
	e := packDetail(&omsapi.Item{ID: "abc", Name: "Widget", Stock: 12})
	e.deps = deps
	e.openCycleCount()
	e.ccQty.SetValue("10")
	_, cmd = e.submitCycleCount()
	cmd()
	if captured.CountedQty != 10 {
		t.Errorf("counted_qty = %d, want 10", captured.CountedQty)
	}
	if captured.AtLevel {
		t.Error("an each-mode count must NOT opt into at_level")
	}
}

// TestCycleCount_OpenClosedCollectsOpenCount pins the extra step and its payload.
func TestCycleCount_OpenClosedCollectsOpenCount(t *testing.T) {
	var captured omsapi.CycleCountBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"def","current_stock":200}`))
	}))
	defer srv.Close()

	s := packDetail(bagItem())
	s.deps = Deps{OMS: omsapi.New(srv.URL)}

	s.Update(runeKey('c'))
	if s.ccStep != ccStepQty {
		t.Fatalf("c should open the qty step, got %d", s.ccStep)
	}
	// The prompt names the unit the server will read the number in, and says the
	// count is of sealed packs only.
	prompt := s.cycleCountPrompt()
	if !strings.Contains(prompt, "Counted quantity (cases)") {
		t.Errorf("prompt should name the count unit: %q", prompt)
	}
	if !strings.Contains(prompt, "sealed only") {
		t.Errorf("prompt should say sealed only: %q", prompt)
	}
	if !strings.Contains(prompt, "system on hand: 3 sealed + 1 open") {
		t.Errorf("prompt should show what is being reconciled against: %q", prompt)
	}

	s.Update(runeKey('2'))
	s.Update(ccEnterKey())
	// An open_closed item gets the extra open-container step, seeded with the
	// current tally so a blank never silently clears it.
	if s.ccStep != ccStepOpenCount {
		t.Fatalf("open_closed should route to the open-count step, got %d", s.ccStep)
	}
	if s.ccOpenCount.Value() != "1" {
		t.Errorf("open count should seed from the current tally, got %q", s.ccOpenCount.Value())
	}
	if !strings.Contains(s.cycleCountPrompt(), "Open containers") {
		t.Errorf("open-count prompt = %q", s.cycleCountPrompt())
	}
	// Replace the seeded tally with 0 — "nothing is open now" is meaningful.
	s.ccOpenCount.SetValue("0")
	s.Update(ccEnterKey())
	if s.ccStep != ccStepReason {
		t.Fatalf("open count should advance to the reason step, got %d", s.ccStep)
	}
	s.Update(ccEnterKey()) // accept the default reason
	if s.ccStep != ccStepNotes {
		t.Fatalf("reason should advance to notes, got %d", s.ccStep)
	}
	_, cmd := s.submitCycleCount()
	if cmd == nil {
		t.Fatal("submitCycleCount returned no command")
	}
	cmd()

	if captured.CountedQty != 2 || !captured.AtLevel {
		t.Errorf("body = %+v, want 2 packs at_level", captured)
	}
	if captured.OpenCount == nil || *captured.OpenCount != 0 {
		t.Errorf("open_count = %v, want an explicit 0", captured.OpenCount)
	}
}

// TestConsume_AtLevelPayloadAndCharge pins the usage half: whole packs on the
// wire, and a charge priced through the pack (unit_cost is per BASE unit).
func TestConsume_AtLevelPayloadAndCharge(t *testing.T) {
	var captured omsapi.LogUsageBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"quantity":1000,"charged_group":null,
			"unit_cost":"0.01","total_cost":"10.00","ledger_transaction":null}`))
	}))
	defer srv.Close()

	item := paperItem()
	item.UnitCost = "0.01" // per sheet
	s := packDetail(item)
	s.deps = Deps{OMS: omsapi.New(srv.URL)}

	s.openConsume()
	prompt := s.consumePrompt()
	if !strings.Contains(prompt, "Quantity used (reams)") {
		t.Errorf("prompt should name the count unit: %q", prompt)
	}
	// 2 reams × 500 sheets × $0.01 = $10.00, not $0.02.
	if got := s.projectedCharge(2); got != "$10.00" {
		t.Errorf("projected charge = %q, want the pack-priced $10.00", got)
	}

	s.cnQty.SetValue("2")
	_, cmd := s.submitConsume()
	if cmd == nil {
		t.Fatal("submitConsume returned no command")
	}
	cmd()
	if captured.Quantity != 2 {
		t.Errorf("quantity = %d, want the pack count 2", captured.Quantity)
	}
	if !captured.AtLevel {
		t.Error("a pack-counted consume must post at_level")
	}

	// An each-mode item keeps both the plain label and the per-unit charge.
	captured = omsapi.LogUsageBody{}
	e := packDetail(&omsapi.Item{ID: "abc", Name: "Widget", Stock: 12, UnitCost: "2.50"})
	e.deps = Deps{OMS: omsapi.New(srv.URL)}
	e.openConsume()
	if strings.Contains(e.consumePrompt(), "Quantity used (") {
		t.Errorf("each-mode prompt must keep the plain label: %q", e.consumePrompt())
	}
	if got := e.projectedCharge(2); got != "$5.00" {
		t.Errorf("each-mode charge = %q, want $5.00", got)
	}
	e.cnQty.SetValue("2")
	_, cmd = e.submitConsume()
	cmd()
	if captured.AtLevel {
		t.Error("an each-mode consume must NOT opt into at_level")
	}
}

// TestPackModal_OnlyForOpenClosed keeps the transition off items that cannot make
// it. For a wrong-mode item p is a silent no-op (the i/b shape); only a
// half-configured open/closed item — a real configuration problem — gets told why.
func TestPackModal_OnlyForOpenClosed(t *testing.T) {
	// by_level has a chain but is not counted open/closed.
	s := packDetail(paperItem())
	if _, cmd := s.Update(runeKey('p')); cmd != nil {
		t.Error("p on a by_level item should be a silent no-op")
	}
	if s.pkStep != packStepNone {
		t.Error("p must not open the pack modal for a by_level item")
	}
	if s.WantsRawInput() {
		t.Error("no modal should be open, so raw input must not be claimed")
	}

	// each mode likewise — a stray p on an ordinary item must say nothing.
	e := packDetail(&omsapi.Item{ID: "abc", Name: "Widget", Stock: 12})
	if _, cmd := e.Update(runeKey('p')); cmd != nil {
		t.Error("p on an each-mode item should be a silent no-op")
	}
	if e.pkStep != packStepNone {
		t.Error("p must not open the pack modal for an each-mode item")
	}

	// open_closed but with no counting level: the mode alone is not enough, and
	// this one DOES explain itself.
	half := bagItem()
	half.CountLevel = nil
	h := packDetail(half)
	_, cmd := h.Update(runeKey('p'))
	if h.pkStep != packStepNone {
		t.Error("p must not open the pack modal for a half-configured item")
	}
	if cmd == nil {
		t.Error("a half-configured open/closed item should be told why it cannot")
	}
}

// TestPackModal_TransitionsAndAvailability drives the picker and its POST.
func TestPackModal_TransitionsAndAvailability(t *testing.T) {
	var captured struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transition":"open","id":"def","current_stock":200,
			"open_container_count":2,
			"on_hand_display":{"mode":"open_closed","level":"case","sealed":2,"open":2,
				"text":"2 sealed + 2 open"}}`))
	}))
	defer srv.Close()

	s := packDetail(bagItem())
	s.deps = Deps{OMS: omsapi.New(srv.URL)}

	s.Update(runeKey('p'))
	if s.pkStep != packStepChoose {
		t.Fatalf("p should open the pack modal, got %d", s.pkStep)
	}
	if !s.WantsRawInput() {
		t.Error("an open modal must claim raw input")
	}
	prompt := s.packPrompt()
	if !strings.Contains(prompt, "Open a case") || !strings.Contains(prompt, "Finish the open case") {
		t.Errorf("both transitions should be listed: %q", prompt)
	}
	// Opening IS consumption under this mode; the prompt has to say so.
	if !strings.Contains(prompt, "out of stock") {
		t.Errorf("prompt should warn that opening draws stock: %q", prompt)
	}

	_, cmd := s.submitPack()
	if cmd == nil {
		t.Fatal("submitPack returned no command")
	}
	msg := cmd()
	if captured.path != "/api/inventory/items/def/pack-container/" {
		t.Errorf("path = %q", captured.path)
	}
	if captured.body["transition"] != omsapi.PackTransitionOpen {
		t.Errorf("transition = %v, want open", captured.body["transition"])
	}
	done, ok := msg.(packContainerDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want packContainerDoneMsg", msg)
	}
	if got := packStatusLine(done); !strings.Contains(got, "2 sealed + 2 open") {
		t.Errorf("status line = %q, want the refreshed on-hand", got)
	}
	// The success path closes the modal and reloads.
	s.Update(done)
	if s.pkStep != packStepNone {
		t.Error("a successful transition should close the modal")
	}
}

// TestPackModal_UnavailableMoveExplainsItself: an impossible move is listed but
// says why, and pressing enter on it names the reason instead of firing.
func TestPackModal_UnavailableMoveExplainsItself(t *testing.T) {
	// No sealed packs left, one open.
	item := bagItem()
	item.Stock = 0
	item.OnHandDisplay = &omsapi.OnHandDisplay{
		Mode: omsapi.CountModeOpenClosed, Level: "case", Sealed: 0, Open: 1,
		Text: "0 sealed + 1 open",
	}
	s := packDetail(item)
	s.Update(runeKey('p'))

	// The cursor starts on the move that is actually possible.
	if s.pkOptions[s.pkCursor].transition != omsapi.PackTransitionFinish {
		t.Errorf("cursor should start on the possible move, got %q", s.pkOptions[s.pkCursor].transition)
	}
	if !strings.Contains(s.packPrompt(), "no sealed case left to open") {
		t.Errorf("prompt should explain the unavailable move: %q", s.packPrompt())
	}

	// Move onto the unavailable one and press enter: no request, just the reason.
	s.pkCursor = 0
	_, cmd := s.submitPack()
	if cmd != nil {
		t.Error("an unavailable move must not fire a request")
	}
	if s.pkErr == "" {
		t.Error("an unavailable move must report why")
	}
	if s.pkPending {
		t.Error("an unavailable move must not mark a submission in flight")
	}

	// esc closes.
	s.Update(ccEscKey())
	if s.pkStep != packStepNone || s.WantsRawInput() {
		t.Error("esc should close the pack modal")
	}
}

// TestPackModal_ErrorKeepsModalOpen so the operator can pick the other move.
func TestPackModal_ErrorKeepsModalOpen(t *testing.T) {
	s := packDetail(bagItem())
	s.Update(runeKey('p'))
	s.pkPending = true
	s.Update(packContainerDoneMsg{transition: omsapi.PackTransitionOpen, err: errAssert("no sealed pack")})

	if s.pkStep != packStepChoose {
		t.Error("a failed transition should leave the modal open")
	}
	if !strings.Contains(s.pkErr, "no sealed pack") {
		t.Errorf("pkErr = %q", s.pkErr)
	}
	if s.pkPending {
		t.Error("a failed transition must clear the in-flight flag")
	}
}
