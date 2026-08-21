package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Purchase-order line pricing, driven end to end from the operator's seat
// (sc-po-line-price).
//
// NO LIVE OpenMakerSuite is reachable from a worktree — SCANTTY_OMS_URL is
// unset and nothing answers on the usual port — so this drives the REAL screens
// through Root.Update against a stateful fake whose purchase-order arithmetic is
// transcribed from the OMS source rather than invented here. It models the
// contract faithfully; it is not evidence from production, and the two halves of
// that contract are exactly what the bug lives between:
//
//	models.py PurchaseOrderItem.estimated_cost   quantity_ORDERED  × unit_cost_ordered
//	models.py PurchaseOrderItem.actual_cost      quantity_RECEIVED × unit_cost_actual
//	                                             (null until a receipt AND a priced line)
//	views.py  update_item                        unit_cost_actual = line_cost / quantity_ORDERED
//
// The read side answers "what have we spent on this line so far"; the write side
// reads the same money as "the total for everything we ordered". Feed one into
// the other and the price is multiplied by quantity_received/quantity_ordered on
// every trip — which is what drove real lines to $0.00, an absorbing state once
// "0.00" starts reading as a price the line HAS.

// ---------------------------------------------------------------------------
// Decimal helpers — Django quantizes on save, and which digits survive is the
// whole story here, so the fake rounds where the columns round.
// ---------------------------------------------------------------------------

// quantizeRat rounds to places decimal digits, half away from zero — Python's
// Decimal default, and what DecimalField(max_digits, decimal_places) applies.
func quantizeRat(v *big.Rat, places int) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaled := new(big.Rat).Mul(v, new(big.Rat).SetInt(scale))
	num, den := scaled.Num(), scaled.Denom()
	q, r := new(big.Int).QuoRem(num, den, new(big.Int))
	// r/den >= 1/2  ->  2|r| >= den
	if twice := new(big.Int).Abs(new(big.Int).Lsh(r, 1)); twice.Cmp(den) >= 0 {
		if r.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(q, scale)
}

// ratString renders a value the way DRF renders a DecimalField: a fixed-point
// JSON string, never a float.
func ratString(v *big.Rat, places int) string {
	return quantizeRat(v, places).FloatString(places)
}

// ratFromJSON reads a decimal that arrived as a JSON number or a JSON string,
// which is the same tolerance omsapi.DecimalString has on the way in.
func ratFromJSON(v any) (*big.Rat, bool) {
	r := new(big.Rat)
	switch t := v.(type) {
	case float64:
		return r.SetFloat64(t), true
	case string:
		return r.SetString(strings.TrimSpace(t))
	}
	return nil, false
}

func ratFromInt(n int) *big.Rat { return new(big.Rat).SetInt64(int64(n)) }

// ---------------------------------------------------------------------------
// The fake order
// ---------------------------------------------------------------------------

// fakePOLine holds the STORED columns of a reorder_queue PurchaseOrderItem. The
// costs a client sees are derived from these on every read, never stored — which
// is why a client that writes back what it read has to write it on the same
// basis it was derived on.
type fakePOLine struct {
	id               string
	description      string
	itemID           string // inventory item pk — what purchase_history is keyed on
	quantityOrdered  int
	quantityReceived int
	unitCostOrdered  *big.Rat
	unitCostActual   *big.Rat // nil until some client prices the line
	expectedShipDate string
	notes            string
}

// estimatedCost mirrors models.py PurchaseOrderItem.estimated_cost.
func (l *fakePOLine) estimatedCost() *big.Rat {
	if l.unitCostOrdered == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Mul(ratFromInt(l.quantityOrdered), l.unitCostOrdered)
}

// actualCost mirrors models.py PurchaseOrderItem.actual_cost — nil (JSON null)
// unless the line is both priced and partly received.
func (l *fakePOLine) actualCost() *big.Rat {
	if l.unitCostActual == nil || l.quantityReceived <= 0 {
		return nil
	}
	return new(big.Rat).Mul(ratFromInt(l.quantityReceived), l.unitCostActual)
}

func (l *fakePOLine) payload() map[string]any {
	out := map[string]any{
		"id":                     l.id,
		"description":            l.description,
		"quantity_ordered":       l.quantityOrdered,
		"quantity_received":      l.quantityReceived,
		"estimated_cost":         ratString(l.estimatedCost(), 2),
		"actual_cost":            nil,
		"unit_cost_ordered":      nil,
		"unit_cost_actual":       nil,
		"expected_shipment_date": l.expectedShipDate,
		"notes":                  l.notes,
	}
	if l.unitCostOrdered != nil {
		out["unit_cost_ordered"] = ratString(l.unitCostOrdered, 4)
	}
	if l.unitCostActual != nil {
		out["unit_cost_actual"] = ratString(l.unitCostActual, 4)
	}
	if a := l.actualCost(); a != nil {
		out["actual_cost"] = ratString(a, 2)
	}
	if l.itemID != "" {
		out["item_details"] = map[string]any{"id": l.itemID, "name": l.description}
	}
	return out
}

// fakePOServer serves one purchase order plus the inventory purchase-history
// read the cost fallback uses, and records every write.
type fakePOServer struct {
	mu      sync.Mutex
	lines   []*fakePOLine
	patches []map[string]any // update_item bodies, in order
	// history is the per-item purchase_history payload, keyed by item pk.
	history map[string]omsapi.ItemPurchaseHistory
}

func (f *fakePOServer) line(id string) *fakePOLine {
	for _, l := range f.lines {
		if l.id == id {
			return l
		}
	}
	return nil
}

// costOf reports what a client would see in the Cost column for a line: the
// actual once there is one, else the estimate — the same precedence po_edit.go's
// grid and the web app's table use.
func (f *fakePOServer) costOf(t *testing.T, id string) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.line(id)
	if l == nil {
		t.Fatalf("no line %q", id)
	}
	if a := l.actualCost(); a != nil {
		return ratString(a, 2)
	}
	return ratString(l.estimatedCost(), 2)
}

func (f *fakePOServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path

		switch {
		case r.Method == http.MethodPatch && strings.Contains(path, "/items/"):
			body := map[string]any{}
			if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
				_ = json.Unmarshal(raw, &body)
			}
			f.patches = append(f.patches, body)
			id := strings.TrimSuffix(strings.Split(path, "/items/")[1], "/")
			l := f.line(id)
			if l == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if v, ok := body["expected_shipment_date"]; ok {
				l.expectedShipDate, _ = v.(string)
			}
			if v, ok := body["notes"]; ok {
				l.notes, _ = v.(string)
			}
			// views.py update_item: unit_cost_actual = line_cost / quantity_ordered.
			if v, ok := body["line_cost"]; ok && v != nil {
				cost, parsed := ratFromJSON(v)
				if !parsed || cost.Sign() < 0 || l.quantityOrdered <= 0 {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"Invalid line cost value"}`))
					return
				}
				l.unitCostActual = quantizeRat(
					new(big.Rat).Quo(cost, ratFromInt(l.quantityOrdered)), 4)
			}
			_ = json.NewEncoder(w).Encode(l.payload())

		case strings.HasSuffix(path, "/purchase_history/"):
			id := strings.TrimSuffix(strings.TrimSuffix(path, "/purchase_history/"), "/")
			id = id[strings.LastIndex(id, "/")+1:]
			hist, ok := f.history[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"detail":"Not found."}`))
				return
			}
			_ = json.NewEncoder(w).Encode(hist)

		case strings.HasPrefix(path, "/api/reorders/purchase-orders/"):
			items := make([]map[string]any, 0, len(f.lines))
			for _, l := range f.lines {
				items = append(items, l.payload())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "po-1", "po_number": "PO-2026-0042", "status": "partially_received",
				"supplier_name": "Acme Bolt Co.", "order_date": "2026-08-01T00:00:00Z",
				"items": items,
			})

		default:
			// The association pickers load in the background and treat a
			// failure as non-fatal; nothing this test drives waits on them.
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		}
	}
}

// lineCostPatches returns just the update_item bodies that carried a price.
func (f *fakePOServer) lineCostPatches() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, p := range f.patches {
		if _, ok := p["line_cost"]; ok {
			out = append(out, p)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Driving the real screens
// ---------------------------------------------------------------------------

// poPriceOrder is the order the reproduction walks: three lines that differ
// ONLY in how much of them has arrived, which is the masking condition.
//
//	partial — ordered 10, received 4, priced. actual_cost = 4 × $5 = $20.00
//	open    — ordered  6, received 0.        actual_cost null, cost = estimate
//	closed  — ordered  4, received 4, priced. actual_cost = 4 × $9 = $36.00
func poPriceOrder() *fakePOServer {
	rat := func(s string) *big.Rat {
		v, _ := new(big.Rat).SetString(s)
		return v
	}
	return &fakePOServer{
		lines: []*fakePOLine{
			{
				id: "line-partial", description: "Bearing set", itemID: "item-bearing",
				quantityOrdered: 10, quantityReceived: 4,
				unitCostOrdered: rat("5"), unitCostActual: rat("5"),
			},
			{
				id: "line-open", description: "Drive belt", itemID: "item-belt",
				quantityOrdered: 6, quantityReceived: 0,
				unitCostOrdered: rat("12"),
			},
			{
				id: "line-closed", description: "Coupler", itemID: "item-coupler",
				quantityOrdered: 4, quantityReceived: 4,
				unitCostOrdered: rat("9"), unitCostActual: rat("9"),
			},
			// Entered with no price at all — what an asset or freeform line
			// looks like, and what a line whose price was lost looks like once
			// the estimate is gone too. estimated_cost is 0.00, not null: the
			// backend has no way to say "no price", which is why $0.00 has to be
			// read as an absence rather than as a price.
			{
				id: "line-unpriced", description: "Sprocket", itemID: "item-sprocket",
				quantityOrdered: 8, quantityReceived: 0,
				unitCostOrdered: rat("0"),
			},
		},
		history: map[string]omsapi.ItemPurchaseHistory{
			// Oldest order first, as the endpoint returns them. The newest row
			// carries a price settled after delivery, which is the one worth
			// offering.
			"item-sprocket": {OrderCosts: []omsapi.ItemOrderCost{
				{
					PurchaseOrder: 5, PONumber: "PO-2026-0005",
					OrderDate: time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC),
					Status:    "received", QuantityOrdered: 4,
					UnitCostOrdered: "2.0000", UnitCostActual: "2.1000",
				},
				{
					PurchaseOrder: 7, PONumber: "PO-2026-0007",
					OrderDate: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
					Status:    "received", QuantityOrdered: 6,
					UnitCostOrdered: "3.5000", UnitCostActual: "3.7500",
				},
			}},
		},
	}
}

// poPriceRoot opens the purchase order the way the operator does and returns the
// Root plus the live detail screen.
func poPriceRoot(t *testing.T, fake *fakePOServer) (Root, *PurchaseOrderDetailScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler(t))
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	detail := NewPurchaseOrderDetailScreen(deps, "po-1")
	r := newTestRoot(detail)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	r = next.(Root)
	r = pump(t, r, detail.Init(), 0)
	return r, detail
}

// poEditScreen is the edit form Root is currently showing.
func poEditScreen(t *testing.T, r Root) *PurchaseOrderEditScreen {
	t.Helper()
	s, ok := r.screen.(*PurchaseOrderEditScreen)
	if !ok {
		t.Fatalf("active screen is %T, want the PO edit form", r.screen)
	}
	return s
}

// poOpenLineEditor walks the operator's keys: E from the detail opens the edit
// form, Down lands on the wanted line row, Ctrl-E opens that line's editor.
func poOpenLineEditor(t *testing.T, r Root, lineIdx int) (Root, *PurchaseOrderEditScreen) {
	t.Helper()
	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("E")})
	s := poEditScreen(t, r)
	r = pump(t, r, s.Init(), 0)
	for i := 0; i < poEditLineBase+lineIdx; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	if got, ok := s.onLineRow(); !ok || got != lineIdx {
		t.Fatalf("cursor %d is not line row %d (got %d, onLine=%v)", s.cursor, lineIdx, got, ok)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseLine {
		t.Fatalf("Ctrl-E on a line row should open the line editor, phase = %v", s.phase)
	}
	return r, s
}

// poTypeRunes types a string one key at a time, the way a keyboard — or a
// scanner's burst — delivers it.
func poTypeRunes(t *testing.T, r Root, text string) Root {
	t.Helper()
	for _, ch := range text {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
	}
	return r
}

// TestPOLineEdit_ShipDateEditKeepsThePrice is the reproduction, and the
// regression guard it became.
//
// Operator's keys, from the purchase order: E, Down×(header+associations) to
// land on the partially received line, Ctrl-E to open it, Down to the ship-date
// row, type a date, Enter.
//
// The price was never touched. Before the fix the save carried the cost input's
// PREFILL — actual_cost, a quantity_RECEIVED subtotal — into a field the backend
// divides by quantity_ORDERED, so $20.00 came back as $8.00 and the same keys
// walked it down to $0.00. The two sibling lines in the same order, edited with
// the same keys, kept their price: that is the masking condition, and it is
// asserted here rather than assumed.
func TestPOLineEdit_ShipDateEditKeepsThePrice(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	before := fake.costOf(t, "line-partial")
	if before != "20.00" {
		t.Fatalf("fixture: partially received line starts at %s, want 20.00", before)
	}

	r, s := poOpenLineEditor(t, r, 0)
	// One row down from the cost field is the ship date.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if s.lineFocus != poLineEditShipDate {
		t.Fatalf("cursor is on row %d, want the ship-date row", s.lineFocus)
	}
	r = poTypeRunes(t, r, "2026-09-14")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if got := fake.costOf(t, "line-partial"); got != before {
		t.Errorf("editing the ship date changed the line price: %s -> %s", before, got)
	}
	if sent := fake.lineCostPatches(); len(sent) != 0 {
		t.Errorf("a ship-date edit must not send line_cost; sent %v", sent)
	}
	if got := fake.line("line-partial").expectedShipDate; got != "2026-09-14" {
		t.Errorf("ship date = %q, want the typed date", got)
	}
}

// TestPOLineEdit_UntouchedPriceSurvivesRepeatedEdits walks the same line through
// the same keys several times over. The failure this guards was multiplicative —
// each save shrank the price by quantity_received/quantity_ordered — so one pass
// understates it and only a repeat proves the price is a fixed point.
func TestPOLineEdit_UntouchedPriceSurvivesRepeatedEdits(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)
	before := fake.costOf(t, "line-partial")

	for pass := 1; pass <= 4; pass++ {
		var s *PurchaseOrderEditScreen
		r, s = poOpenLineEditor(t, r, 0)
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
		if s.lineFocus != poLineEditNotes {
			t.Fatalf("pass %d: cursor is on row %d, want the notes row", pass, s.lineFocus)
		}
		r = poTypeRunes(t, r, fmt.Sprintf("pass %d", pass))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

		if got := fake.costOf(t, "line-partial"); got != before {
			t.Fatalf("pass %d: price drifted %s -> %s", pass, before, got)
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	}
}

// TestPOLineEdit_MaskingConditionIsTheReceivedFraction pins WHY this looked
// intermittent. The same keystrokes on a line nothing has arrived for, and on a
// line that arrived in full, are harmless — the round trip is a no-op when
// quantity_received is 0 or equals quantity_ordered. Only the partially received
// line lost money. If this ever starts failing because those lines drift too,
// the explanation in the notes is wrong, not just the fix.
func TestPOLineEdit_MaskingConditionIsTheReceivedFraction(t *testing.T) {
	for _, tc := range []struct {
		name string
		idx  int
		id   string
		want string
	}{
		{"nothing received", 1, "line-open", "72.00"},
		{"received in full", 2, "line-closed", "36.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := poPriceOrder()
			r, _ := poPriceRoot(t, fake)
			if got := fake.costOf(t, tc.id); got != tc.want {
				t.Fatalf("fixture: %s starts at %s, want %s", tc.id, got, tc.want)
			}
			r, _ = poOpenLineEditor(t, r, tc.idx)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
			r = poTypeRunes(t, r, "2026-09-14")
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if got := fake.costOf(t, tc.id); got != tc.want {
				t.Errorf("%s: price moved %s -> %s", tc.id, tc.want, got)
			}
		})
	}
}

// TestPOLineEdit_TypedPriceIsSaved is the other half of the contract: a price the
// operator DOES type must reach the order, and must mean the total for the
// quantity ordered — which is what the endpoint divides by, and what the field's
// own prompt says it is.
func TestPOLineEdit_TypedPriceIsSaved(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 0)
	if s.lineFocus != poLineEditCost {
		t.Fatalf("the line editor should open on the cost row, got %d", s.lineFocus)
	}
	// Clear the prefill the way an operator does, then type the real invoice
	// total: 10 bearings actually cost $62.50.
	for range s.lineInputs[poLineEditCost].Value() {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = poTypeRunes(t, r, "62.50")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.lineCostPatches()
	if len(sent) != 1 {
		t.Fatalf("a typed price must be sent exactly once; sent %v", sent)
	}
	if got, _ := ratFromJSON(sent[0]["line_cost"]); got == nil || got.Cmp(big.NewRat(125, 2)) != 0 {
		t.Errorf("line_cost = %v, want 62.50", sent[0]["line_cost"])
	}
	// 62.50 over the 10 ordered is $6.25/unit; 4 have arrived, so the money
	// spent so far is $25.00.
	if got := fake.costOf(t, "line-partial"); got != "25.00" {
		t.Errorf("line cost after the edit = %s, want 25.00", got)
	}
}

// TestPOLineEdit_UnpricedLineOffersTheLastPricePaid covers a line that has no
// price to carry forward at all. The form must not hand back the $0.00 the
// backend reports — it offers what the shop last actually paid for the item,
// says out loud that the figure is history, and waits: nothing is committed
// until the operator takes it with Ctrl-E.
func TestPOLineEdit_UnpricedLineOffersTheLastPricePaid(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)
	if got := fake.costOf(t, "line-unpriced"); got != "0.00" {
		t.Fatalf("fixture: the unpriced line should read 0.00, got %s", got)
	}

	r, s := poOpenLineEditor(t, r, 3)
	if got := s.lineInputs[poLineEditCost].Value(); got != "" {
		t.Errorf("a line with no price must open with an EMPTY cost field, got %q", got)
	}

	out := s.View()
	for _, want := range []string{
		"Last paid $3.75/unit on PO-2026-0007 (2026-06-02)",
		"historical, not confirmed for this order",
		"Ctrl-E offers $30.00 for the 8 ordered",
		"Ctrl-E=Use last price",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("line editor missing %q:\n%s", want, out)
		}
	}

	// Saving without taking the offer must send no price: an offer that applied
	// itself would be the same silent write this whole fix is about.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = poTypeRunes(t, r, "2026-10-01")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if sent := fake.lineCostPatches(); len(sent) != 0 {
		t.Fatalf("an untaken offer must not be saved; sent %v", sent)
	}
	if got := fake.costOf(t, "line-unpriced"); got != "0.00" {
		t.Errorf("line cost = %s, want it left alone at 0.00", got)
	}
}

// TestPOLineEdit_TakingTheOfferPricesTheLine is the other half: Ctrl-E puts the
// historical figure in the field, where it is an ordinary typed value — the
// operator can correct it, and enter saves it as the total for the quantity
// ORDERED.
func TestPOLineEdit_TakingTheOfferPricesTheLine(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 3)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineEditCost].Value(); got != "30.00" {
		t.Fatalf("Ctrl-E should offer 8 × $3.75 = 30.00, field = %q", got)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.lineCostPatches()
	if len(sent) != 1 {
		t.Fatalf("the taken offer should be saved once; sent %v", sent)
	}
	if got, _ := ratFromJSON(sent[0]["line_cost"]); got == nil || got.Cmp(big.NewRat(30, 1)) != 0 {
		t.Errorf("line_cost = %v, want 30", sent[0]["line_cost"])
	}
	// Nothing has arrived yet, so actual_cost is still null and the line reads
	// its estimate — but unit_cost_actual now holds the price that was taken.
	if got := fake.line("line-unpriced").unitCostActual.FloatString(4); got != "3.7500" {
		t.Errorf("unit_cost_actual = %s, want 3.7500", got)
	}
}

// TestPOLineEdit_NoHistorySaysSo — an offer that cannot be made has to say why,
// not leave a blank field with an unexplained Ctrl-E in the bar.
func TestPOLineEdit_NoHistorySaysSo(t *testing.T) {
	fake := poPriceOrder()
	delete(fake.history, "item-sprocket") // the endpoint 404s for this item
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 3)
	out := s.View()
	if !strings.Contains(out, "Last price unavailable") {
		t.Errorf("a failed history lookup should say so:\n%s", out)
	}
	if strings.Contains(out, "Ctrl-E=Use last price") {
		t.Errorf("the bar must not offer a key with nothing behind it:\n%s", out)
	}
	// Ctrl-E with no offer explains itself rather than doing nothing.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineEditCost].Value(); got != "" {
		t.Errorf("cost field = %q, want it left empty", got)
	}
}

// ---------------------------------------------------------------------------
// The pricing rules on their own
// ---------------------------------------------------------------------------

// TestPOLineCarriedCost pins which number the cost field carries forward, and on
// which basis. actual_cost is never it: that is a quantity_RECEIVED subtotal and
// the endpoint reads a line_cost as a quantity_ORDERED total.
func TestPOLineCarriedCost(t *testing.T) {
	for _, tc := range []struct {
		name string
		line omsapi.PurchaseOrderItem
		want string
	}{
		{
			name: "estimate when nothing is priced yet",
			line: omsapi.PurchaseOrderItem{QuantityOrdered: 5, EstimatedCost: "50.00"},
			want: "50.00",
		},
		{
			name: "actual unit price across the ORDERED quantity, not the received one",
			line: omsapi.PurchaseOrderItem{
				QuantityOrdered: 10, QuantityReceived: 4,
				UnitCostOrdered: "5.0000", UnitCostActual: "5.0000",
				EstimatedCost: "50.00", ActualCost: "20.00",
			},
			want: "50.00",
		},
		{
			name: "a line already driven to zero falls back to its estimate",
			line: omsapi.PurchaseOrderItem{
				QuantityOrdered: 10, QuantityReceived: 4,
				UnitCostOrdered: "5.0000", UnitCostActual: "0.0000",
				EstimatedCost: "50.00", ActualCost: "0.00",
			},
			want: "50.00",
		},
		{
			name: "no price at all — a zero estimate is an absence, not a price",
			line: omsapi.PurchaseOrderItem{QuantityOrdered: 8, EstimatedCost: "0.00"},
			want: "",
		},
		{
			name: "null everything",
			line: omsapi.PurchaseOrderItem{QuantityOrdered: 3},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := poLineCarriedCost(tc.line); got != tc.want {
				t.Errorf("poLineCarriedCost = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPOLastPaidFrom pins which history row becomes the offer: the newest one
// with a usable price, a settled price beating the price it was ordered at, and
// never a row belonging to the order being edited.
func TestPOLastPaidFrom(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 6, d, 0, 0, 0, 0, time.UTC) }
	history := &omsapi.ItemPurchaseHistory{OrderCosts: []omsapi.ItemOrderCost{
		{PurchaseOrder: 1, PONumber: "PO-1", OrderDate: day(1), UnitCostOrdered: "1.0000", UnitCostActual: "1.1000"},
		{PurchaseOrder: 2, PONumber: "PO-2", OrderDate: day(2), UnitCostOrdered: "2.0000"},
		{PurchaseOrder: 3, PONumber: "", OrderDate: day(3), UnitCostOrdered: "0.0000"},
		{PurchaseOrder: 4, PONumber: "PO-4", OrderDate: day(4), UnitCostOrdered: "4.0000"},
	}}

	// The newest priced row wins, and an unnumbered / unpriced row is skipped.
	got := poLastPaidFrom(history, "")
	if got == nil || got.unit != 4 || got.order != "PO-4" || got.confirmed {
		t.Fatalf("offer = %+v, want PO-4 at 4.0000 ordered-at", got)
	}
	// The order being edited is not evidence about itself.
	got = poLastPaidFrom(history, "4")
	if got == nil || got.unit != 2 || got.order != "PO-2" {
		t.Fatalf("offer excluding PO 4 = %+v, want PO-2 at 2.0000", got)
	}
	// A settled price beats the price the same row was ordered at.
	got = poLastPaidFrom(&omsapi.ItemPurchaseHistory{OrderCosts: history.OrderCosts[:1]}, "")
	if got == nil || got.unit != 1.1 || !got.confirmed {
		t.Fatalf("offer = %+v, want the settled 1.1000", got)
	}
	if got := poLastPaidFrom(nil, ""); got != nil {
		t.Errorf("no history should offer nothing, got %+v", got)
	}
	if got := poLastPaidFrom(&omsapi.ItemPurchaseHistory{}, ""); got != nil {
		t.Errorf("an empty history should offer nothing, got %+v", got)
	}
}
