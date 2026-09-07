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
				"id": 1, "po_number": "PO-2026-0042", "status": "partially_received",
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
//
// …plus two lines that carry no price at all, for the two ways that happens.
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
			// A line the old code already drove to zero, before the guard
			// existed: unit_cost_actual pinned at 0.0000 over 10 ordered with 4
			// received, so the grid's Cost column reads $0.00 while the estimate
			// behind it still says the line was placed at $50.00. This is the
			// shape that has to be RECOVERABLE from the keyboard — the prefill
			// hands the estimate back, so there is nothing different to type.
			{
				id: "line-zeroed", description: "Idler pulley", itemID: "item-idler",
				quantityOrdered: 10, quantityReceived: 4,
				unitCostOrdered: rat("5"), unitCostActual: rat("0"),
			},
			// Priced below a cent per unit — unit_cost_actual keeps four
			// places, and a shim or a washer really does cost this. The total
			// for the 10 ordered is $0.001, which is a REAL price that two
			// decimal places would render as the same "0.00" the line above is
			// genuinely pinned at. The two must not read alike: this one has a
			// price and must not be offered a historical one, and its Ctrl-E
			// must not write a zero over it.
			{
				id: "line-subcent", description: "Shim washer", itemID: "item-shim",
				quantityOrdered: 10, quantityReceived: 4,
				unitCostOrdered: rat("1/10000"), unitCostActual: rat("1/10000"),
			},
			// Half a mil per unit: $0.005 over the 10 ordered, which two decimal
			// places round UP to "0.01" rather than down to nothing. Sending
			// that back would store 0.0010 — double the real price — so the
			// figure on screen has to carry the places that survive the trip.
			{
				id: "line-halfmil", description: "Bronze shim", itemID: "item-bronze",
				quantityOrdered: 10, quantityReceived: 4,
				unitCostOrdered: rat("1/2000"), unitCostActual: rat("1/2000"),
			},
			// Nothing ordered at all — quantity_ordered is omitempty on the
			// wire, so an absent field decodes to 0 — on a line that buys an
			// item WITH purchase history. The history is real and the item is
			// real; what does not exist is a total, because a per-unit price
			// over no quantity is not one. The bar must not name a key for it.
			{
				id: "line-nothing-ordered", description: "Sprocket (unbudgeted)",
				itemID:          "item-sprocket",
				quantityOrdered: 0, quantityReceived: 0,
				unitCostOrdered: rat("5"),
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
	detail := NewPurchaseOrderDetailScreen(deps, "1")
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

// poResize puts the terminal at a given width, which is the only way to see
// what the operator sees: Root.View clips every content line to the pane's
// inner width and never wraps, so a screen's own View() — what every other test
// here reads — shows text that never reaches the glass.
func poResize(t *testing.T, r Root, width int) Root {
	t.Helper()
	next, cmd := r.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return pump(t, after, cmd, 0)
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
		// "priced at", not "paid": the history endpoint filters neither voided
		// lines nor order status, so the order's own status rides the offer and
		// the wording claims only what the row establishes.
		"last priced at $3.75/unit on PO-2026-0007 (2026-06-02 · received)",
		"Historical price, not confirmed for this order",
		"Ctrl-E offers $30.00 for the 8 ordered",
		"Ctrl-E=Take",
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
	if strings.Contains(out, "Ctrl-E=Take") {
		t.Errorf("the bar must not offer a key with nothing behind it:\n%s", out)
	}
	// Ctrl-E opens nothing here, silently — the bar named no key, and a key the
	// bar does not name does nothing. The reason is already on screen in the
	// body above; it is not something the operator has to press a key to be
	// told, and pressing one must not put a figure in the field.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineEditCost].Value(); got != "" {
		t.Errorf("cost field = %q, want it left empty", got)
	}
}

// TestPOLastPaidDescribeClaimsOnlyWhatTheRowShows: the offer is one line of
// green screen and the operator decides on it, so what it asserts matters. A
// price read from unit_cost_actual is a price RECORDED against a line — it is
// written by the same PATCH this screen makes, on a line that may have received
// nothing and may later be voided, on an order that may be cancelled — and the
// purchase_history endpoint hands those rows back like any other. So the offer
// says "priced at", never "paid", and carries the order's status so the row can
// be judged rather than trusted.
func TestPOLastPaidDescribeClaimsOnlyWhatTheRowShows(t *testing.T) {
	voided := &omsapi.ItemPurchaseHistory{OrderCosts: []omsapi.ItemOrderCost{{
		PurchaseOrder: 7, PONumber: "PO-2026-0007",
		OrderDate: time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC),
		Status:    "cancelled", UnitCostOrdered: "3.5000", UnitCostActual: "3.7500",
	}}}
	row := poLastPaidFrom(voided, "")
	if row == nil {
		t.Fatal("a priced row should still be offered — the operator judges it")
	}
	got := row.describe()
	if strings.Contains(got, "paid $") {
		t.Errorf("a price nobody paid must not be called paid: %q", got)
	}
	for _, want := range []string{
		"last priced at $3.75/unit on PO-2026-0007",
		"2026-06-02 · cancelled",
		"Historical price, not confirmed for this order",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("offer %q missing %q", got, want)
		}
	}

	// Either half of the provenance can be missing on the wire (order_date is
	// nullable, status is free text) — a missing half drops out rather than
	// leaving an empty slot behind a separator.
	bare := &poLastPaid{unit: 2, order: "PO-9"}
	if got := bare.describe(); strings.Contains(got, "(") {
		t.Errorf("no date and no status should render no parenthetical: %q", got)
	}
}

// TestPOLineEdit_RetypingTheShownPriceIsNotAChange: the untouched-cost guard
// compares AMOUNTS. Retyping the "50.00" the field is showing and typing "50"
// are one intent expressed two ways, and a guard on raw text made them opposite
// outcomes — "50" saved, "50.00" silently dropped — which is not a rule anybody
// could be taught. Neither is sent now: both mean the price is unchanged.
func TestPOLineEdit_RetypingTheShownPriceIsNotAChange(t *testing.T) {
	for _, typed := range []string{"50.00", "50"} {
		t.Run("types "+typed, func(t *testing.T) {
			fake := poPriceOrder()
			r, _ := poPriceRoot(t, fake)
			// The grid reads $0.00 — 4 received × a unit cost pinned at zero —
			// while the estimate behind it still says $50.00.
			if got := fake.costOf(t, "line-zeroed"); got != "0.00" {
				t.Fatalf("fixture: the zeroed line should read 0.00, got %s", got)
			}

			r, s := poOpenLineEditor(t, r, 4)
			if got := s.lineInputs[poLineEditCost].Value(); got != "50.00" {
				t.Fatalf("the prefill should recover the estimate, field = %q", got)
			}
			for range s.lineInputs[poLineEditCost].Value() {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
			}
			r = poTypeRunes(t, r, typed)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

			if sent := fake.lineCostPatches(); len(sent) != 0 {
				t.Errorf("typing the shown amount is not a change; sent %v", sent)
			}
			if got := fake.line("line-zeroed").unitCostActual.FloatString(4); got != "0.0000" {
				t.Errorf("unit_cost_actual = %s, want it untouched at 0.0000", got)
			}
		})
	}
}

// TestPOLineEdit_ZeroedLineRecoversItsPriceWithCtrlE is the way back out of
// $0.00 from the keyboard. The guard above means a line whose prefill already
// shows the right figure has nothing the operator could type that would count
// as a change — so the cost row's Ctrl-E, the same key that takes the historical
// offer on a line with no price, commits the price the row is showing. The bar
// names it, because it does something.
func TestPOLineEdit_ZeroedLineRecoversItsPriceWithCtrlE(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 4)
	if !strings.Contains(s.View(), "Ctrl-E=Send") {
		t.Fatalf("the cost row of a priced line should name Ctrl-E:\n%s", s.View())
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineEditCost].Value(); got != "50.00" {
		t.Errorf("confirming must not change the figure, field = %q", got)
	}
	if !strings.Contains(s.View(), "Price confirmed") {
		t.Errorf("the armed state should be on screen, not only in a status line:\n%s", s.View())
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.lineCostPatches()
	if len(sent) != 1 {
		t.Fatalf("the confirmed price should be sent exactly once; sent %v", sent)
	}
	if got, _ := ratFromJSON(sent[0]["line_cost"]); got == nil || got.Cmp(big.NewRat(50, 1)) != 0 {
		t.Errorf("line_cost = %v, want 50", sent[0]["line_cost"])
	}
	// $50.00 over the 10 ordered is $5.00/unit again, so the 4 received are
	// worth $20.00 — the line is off zero and the grid says so.
	if got := fake.line("line-zeroed").unitCostActual.FloatString(4); got != "5.0000" {
		t.Errorf("unit_cost_actual = %s, want 5.0000", got)
	}
	if got := fake.costOf(t, "line-zeroed"); got != "20.00" {
		t.Errorf("line cost after the recovery = %s, want 20.00", got)
	}
}

// TestPOLineEdit_ConfirmingIsPerLine: the confirm is armed for the line it was
// pressed on and nothing else. It is a one-line escape hatch out of $0.00, so an
// arm that leaked into the next line opened would re-send a price on a line
// nobody touched — the exact failure the guard exists to prevent.
func TestPOLineEdit_ConfirmingIsPerLine(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 4)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	// The save drops back to the form with the cursor still on that line, so
	// the operator walks up to the partially received one and opens it — the
	// keys they would actually press, with no fresh screen in between.
	for i := 0; i < 4; i++ {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyUp})
	}
	if got, ok := s.onLineRow(); !ok || got != 0 {
		t.Fatalf("cursor %d is not the first line row (got %d, onLine=%v)", s.cursor, got, ok)
	}
	before := fake.costOf(t, "line-partial")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseLine {
		t.Fatalf("Ctrl-E on a line row should open its editor, phase = %v", s.phase)
	}
	if s.lineCostConfirmed {
		t.Fatal("opening a line must not inherit the previous line's confirm")
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = poTypeRunes(t, r, "2026-09-14")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if got := fake.costOf(t, "line-partial"); got != before {
		t.Errorf("price drifted %s -> %s", before, got)
	}
	if sent := fake.lineCostPatches(); len(sent) != 1 {
		t.Errorf("only the confirmed line should have sent a price; sent %v", sent)
	}
}

// TestPOLineEdit_ArmedThenEmptiedSaysWhatEnterActuallyDoes: the drawn state and
// saveLine have to agree in every field state. saveLine writes nothing when the
// cost field is blank — that is the deliberate "blank leaves the price alone"
// rule — and it never reaches the confirm flag, so an armed line whose field the
// operator then cleared announced a money write ("enter writes $ as the total
// for the 10 ordered", with no amount) that could not happen.
func TestPOLineEdit_ArmedThenEmptiedSaysWhatEnterActuallyDoes(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 4)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	for range s.lineInputs[poLineEditCost].Value() {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}

	out := s.View()
	if strings.Contains(out, "writes $ ") || strings.Contains(out, "enter writes $ as") {
		t.Errorf("an empty field must not be announced as a price write:\n%s", out)
	}
	if !strings.Contains(out, "enter writes no price and the line keeps the one it has") {
		t.Errorf("the screen should say what enter will really do:\n%s", out)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if sent := fake.lineCostPatches(); len(sent) != 0 {
		t.Errorf("a blank cost field must send no price; sent %v", sent)
	}
	if got := fake.line("line-zeroed").unitCostActual.FloatString(4); got != "0.0000" {
		t.Errorf("unit_cost_actual = %s, want it left alone", got)
	}
}

// TestPOLineEdit_ConfirmNoteFollowsTheFieldNotThePrefill: Ctrl-E arms whatever
// the field holds, so the note about it has to name that — it named the price
// the row OPENED with, and called it "unchanged", while the save was about to
// write the figure the operator had typed over it.
func TestPOLineEdit_ConfirmNoteFollowsTheFieldNotThePrefill(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 4)
	for range s.lineInputs[poLineEditCost].Value() {
		r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = poTypeRunes(t, r, "62.50")

	out := s.View()
	if !strings.Contains(out, "Ctrl-E writes $62.50 as the total for the 10 ordered") {
		t.Errorf("the hint should name the figure Ctrl-E would write:\n%s", out)
	}
	if strings.Contains(out, "$50.00") {
		t.Errorf("the hint still names the prefill the operator typed over:\n%s", out)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if out := s.View(); !strings.Contains(out, "Price confirmed: enter writes $62.50") {
		t.Errorf("the armed note should name the field's figure:\n%s", out)
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.lineCostPatches()
	if len(sent) != 1 {
		t.Fatalf("the typed price should be sent once; sent %v", sent)
	}
	if got, _ := ratFromJSON(sent[0]["line_cost"]); got == nil || got.Cmp(big.NewRat(125, 2)) != 0 {
		t.Errorf("line_cost = %v, want 62.50", sent[0]["line_cost"])
	}
}

// TestPOLineEdit_SubCentPriceIsNotShownAsZero: a total of $0.001 is a price, and
// two decimal places would print it as the same $0.00 a pinned line shows. That
// collapse is what this screen exists to stop: the operator would see a zero,
// the offer would stay hidden (the field is non-empty, so the line "carries a
// price"), and confirming what was on screen would PATCH a real zero over a real
// price. The figure survives to the field, and an untouched save still sends
// nothing.
func TestPOLineEdit_SubCentPriceIsNotShownAsZero(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 5)
	if got := s.lineInputs[poLineEditCost].Value(); got != "0.0010" {
		t.Errorf("the carried total is 10 × $0.0001 = $0.001, field = %q", got)
	}
	// Ship date only: the price rides nothing, and the round trip is a fixed
	// point at four places just as it is at two.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = poTypeRunes(t, r, "2026-09-14")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if sent := fake.lineCostPatches(); len(sent) != 0 {
		t.Errorf("an untouched sub-cent price must not be re-sent; sent %v", sent)
	}
	if got := fake.line("line-subcent").unitCostActual.FloatString(4); got != "0.0001" {
		t.Errorf("unit_cost_actual = %s, want 0.0001", got)
	}
}

// TestPOLineEdit_NothingOrderedOffersNoKeyAndNoEmptyAmount: the offer's figure
// is a per-unit price spread across the quantity ORDERED, so a line that orders
// nothing has no total to be offered — even though the item's history is there
// and the row was found. Three callers used to decide that separately: the bar
// named Ctrl-E, the body printed "Ctrl-E offers $ for the 0 ordered" with the
// amount missing, and the key itself did nothing. One boundary decides now, and
// this drives all three.
func TestPOLineEdit_NothingOrderedOffersNoKeyAndNoEmptyAmount(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 7)
	out := s.View()
	if strings.Contains(out, "Ctrl-E offers $ ") || strings.Contains(out, "for the 0 ordered") {
		t.Errorf("an offer with no total must not be drawn:\n%s", out)
	}
	if strings.Contains(out, "Ctrl-E=Take") {
		t.Errorf("the bar must not name a key with no offer behind it:\n%s", out)
	}
	if !strings.Contains(out, "Nothing is ordered on this line") {
		t.Errorf("the body should say why there is no offer:\n%s", out)
	}
	// And the key the bar declined to name does nothing.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineEditCost].Value(); got != "" {
		t.Errorf("cost field = %q, want it left empty", got)
	}
}

// TestPOLineEdit_HalfMilPriceSurvivesTheRoundTrip: $0.005 over 10 ordered
// rounds UP to "0.01" at two places, and sending that back would store
// 0.0010 against a real 0.0005 — the price doubled by the rendering alone.
// What is drawn is what Ctrl-E writes, so what is drawn has to survive.
func TestPOLineEdit_HalfMilPriceSurvivesTheRoundTrip(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)

	r, s := poOpenLineEditor(t, r, 6)
	if got := s.lineInputs[poLineEditCost].Value(); got != "0.0050" {
		t.Fatalf("the carried total is 10 × $0.0005 = $0.005, field = %q", got)
	}
	// Confirm exactly what is on screen, which is the path that would have
	// written the doubled price.
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if sent := fake.lineCostPatches(); len(sent) != 1 {
		t.Fatalf("the confirmed price should be sent once; sent %v", sent)
	}
	if got := fake.line("line-halfmil").unitCostActual.FloatString(4); got != "0.0005" {
		t.Errorf("unit_cost_actual = %s, want the price unchanged at 0.0005", got)
	}
}

// TestPOLineEdit_CostRowNotesStayOnTheCostRow: the screen's rule is that the
// operator learns a key from the bar naming exactly what works where the cursor
// is standing. lineBar already drops Ctrl-E off the cost row; the body kept
// naming it from the notes row, where pressing it does nothing.
func TestPOLineEdit_CostRowNotesStayOnTheCostRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		idx  int
		gone string
	}{
		{"a line that can confirm its price", 4, "Ctrl-E writes $"},
		{"a line with a historical offer", 3, "Ctrl-E offers $"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := poPriceOrder()
			r, _ := poPriceRoot(t, fake)

			r, s := poOpenLineEditor(t, r, tc.idx)
			if !strings.Contains(s.View(), tc.gone) {
				t.Fatalf("the cost row should carry %q:\n%s", tc.gone, s.View())
			}
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
			if s.lineFocus != poLineEditNotes {
				t.Fatalf("cursor is on row %d, want the notes row", s.lineFocus)
			}
			out := s.View()
			if strings.Contains(out, tc.gone) {
				t.Errorf("the body names Ctrl-E where the bar does not and the key does nothing:\n%s", out)
			}
			if strings.Contains(poJDEBarLine(out), "Ctrl-E") {
				t.Errorf("the notes row should name no Ctrl-E: %q", poJDEBarLine(out))
			}
		})
	}
}

// TestPOLastPaidNotesClaimOnlyWhatIsBeingLooked: the in-flight note is the FIRST
// thing the operator reads on that row, and it used to promise "the last price
// paid" before the answer arrived and said "priced at" instead. One row must not
// make the stronger claim and then walk it back — the history endpoint proves a
// price was recorded on some order, never that money changed hands.
func TestPOLastPaidNotesClaimOnlyWhatIsBeingLooked(t *testing.T) {
	c := &poLastPaidCache{}
	c.init()
	c.loading["item-x"] = true

	row, note := c.offer("item-x")
	if row != nil {
		t.Fatalf("a lookup in flight has no row yet, got %+v", row)
	}
	if strings.Contains(note, "paid") {
		t.Errorf("the in-flight note claims money was paid: %q", note)
	}
	if !strings.Contains(note, "recorded") {
		t.Errorf("the in-flight note should name what is being looked up: %q", note)
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
			name: "a sub-cent total keeps the digits that make it a price",
			line: omsapi.PurchaseOrderItem{
				QuantityOrdered: 10, QuantityReceived: 4,
				UnitCostOrdered: "0.0001", UnitCostActual: "0.0001",
				EstimatedCost: "0.00", ActualCost: "0.00",
			},
			want: "0.0010",
		},
		{
			name: "a total that rounds UP at two places still keeps its own price",
			line: omsapi.PurchaseOrderItem{
				QuantityOrdered: 10, QuantityReceived: 4,
				UnitCostOrdered: "0.0005", UnitCostActual: "0.0005",
				EstimatedCost: "0.01", ActualCost: "0.00",
			},
			want: "0.0050",
		},
		{
			name: "an ordinary total that survives two places stays legible at two",
			line: omsapi.PurchaseOrderItem{
				QuantityOrdered: 3, QuantityReceived: 3,
				UnitCostOrdered: "33.3333", UnitCostActual: "33.3333",
				EstimatedCost: "100.00", ActualCost: "100.00",
			},
			want: "100.00",
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
		{PurchaseOrder: 1, PONumber: "PO-1", OrderDate: day(1), Status: "received", UnitCostOrdered: "1.0000", UnitCostActual: "1.1000"},
		{PurchaseOrder: 2, PONumber: "PO-2", OrderDate: day(2), Status: "confirmed", UnitCostOrdered: "2.0000"},
		{PurchaseOrder: 3, PONumber: "", OrderDate: day(3), Status: "draft", UnitCostOrdered: "0.0000"},
		{PurchaseOrder: 4, PONumber: "PO-4", OrderDate: day(4), Status: "cancelled", UnitCostOrdered: "4.0000"},
	}}

	// The newest priced row wins, and an unnumbered / unpriced row is skipped.
	// The order's status comes with it — the endpoint filters no status and no
	// voided line, so it is the only thing that lets the operator judge the row.
	got := poLastPaidFrom(history, "")
	if got == nil || got.unit != 4 || got.order != "PO-4" || got.priced {
		t.Fatalf("offer = %+v, want PO-4 at 4.0000 ordered-at", got)
	}
	if got.status != "cancelled" {
		t.Errorf("offer status = %q, want the order's own status", got.status)
	}
	// The order being edited is not evidence about itself.
	got = poLastPaidFrom(history, "4")
	if got == nil || got.unit != 2 || got.order != "PO-2" {
		t.Fatalf("offer excluding PO 4 = %+v, want PO-2 at 2.0000", got)
	}
	// A settled price beats the price the same row was ordered at.
	got = poLastPaidFrom(&omsapi.ItemPurchaseHistory{OrderCosts: history.OrderCosts[:1]}, "")
	if got == nil || got.unit != 1.1 || !got.priced {
		t.Fatalf("offer = %+v, want the settled 1.1000", got)
	}
	if got := poLastPaidFrom(nil, ""); got != nil {
		t.Errorf("no history should offer nothing, got %+v", got)
	}
	if got := poLastPaidFrom(&omsapi.ItemPurchaseHistory{}, ""); got != nil {
		t.Errorf("an empty history should offer nothing, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// What actually reaches the glass
// ---------------------------------------------------------------------------

// TestPOLineEdit_CostRowSurvivesTheTerminalWidth is the lesson of this round,
// and it is a lesson about the tests rather than about the screen.
//
// Every other assertion here reads the SCREEN's View() — the text the screen
// composes, before Root has done anything with it. Root.View clips each content
// line to the pane's inner width and never wraps, so a line the screen composes
// is not a line the operator reads. The offer's caveat was written at the END of
// its line, so it was the first thing clipped: at 80 columns the row rendered as
// "Last priced at $3.75/unit on PO-2026-0007 (2026-0" — a bare dollar figure
// with nothing left to say it was historical, which is the exact reading the
// offer exists to prevent — and the bar's "Ctrl-E=Use last price" came out as
// "Ctrl-E=Use", a truncation still grammatical enough to be believed. Both were
// invisible to a test that never clipped.
//
// 80 columns is the canonical width of the terminal this interface is modelled
// on, so it is the case that must hold rather than the edge case; 100 and 120
// are here because the caveat was clipped at those too.
func TestPOLineEdit_CostRowSurvivesTheTerminalWidth(t *testing.T) {
	for _, tc := range []struct {
		name string
		idx  int
		want []string
	}{
		{
			// A line with no price of its own: the historical offer, and the
			// key that takes it.
			name: "a line with a historical offer",
			idx:  3,
			want: []string{"Historical price, not confirmed for this order", "Ctrl-E=Take"},
		},
		{
			// A line already showing a price: the key that re-sends it.
			name: "a line that can re-send its price",
			idx:  4,
			want: []string{"Ctrl-E=Send"},
		},
	} {
		for _, width := range []int{80, 100, 120} {
			t.Run(fmt.Sprintf("%s at %d columns", tc.name, width), func(t *testing.T) {
				fake := poPriceOrder()
				r, _ := poPriceRoot(t, fake)
				r = poResize(t, r, width)
				r, _ = poOpenLineEditor(t, r, tc.idx)

				// Root.View, not the screen's: this is the frame the terminal
				// draws, clipping and all.
				out := stripANSI(r.View())
				for _, want := range tc.want {
					if !strings.Contains(out, want) {
						t.Errorf("%q does not survive to the glass at %d columns:\n%s", want, width, out)
					}
				}
			})
		}
	}
}

// TestPOLineEdit_ClippingCostsTheProvenanceAndNotTheCaveat pins WHY the offer
// reads in the order it does. Both halves matter — the caveat is the safeguard,
// the PO number and date are what let the operator judge whose price it is — but
// only one of them fits at 80 columns, so the order decides which. It is the
// caveat, because the provenance can be recovered by opening the item and the
// warning cannot be recovered from anywhere.
func TestPOLineEdit_ClippingCostsTheProvenanceAndNotTheCaveat(t *testing.T) {
	fake := poPriceOrder()
	r, _ := poPriceRoot(t, fake)
	r = poResize(t, r, 80)
	r, s := poOpenLineEditor(t, r, 3)

	// The screen composes both halves…
	if full := s.View(); !strings.Contains(full, "last priced at $3.75/unit on PO-2026-0007 (2026-06-02 · received)") {
		t.Fatalf("the offer should still carry its provenance:\n%s", full)
	}
	// …and at 80 columns it is the provenance that falls off the end, which is
	// the trade this ordering makes on purpose.
	out := stripANSI(r.View())
	if !strings.Contains(out, "Historical price, not confirmed for this order") {
		t.Errorf("the caveat must survive 80 columns:\n%s", out)
	}
	if strings.Contains(out, "PO-2026-0007 (2026-06-02 · received)") {
		t.Errorf("80 columns cannot hold both halves; if it now does, this test is stale:\n%s", out)
	}
}

// TestPOLineEdit_RejectedCostIsNeverAnnouncedAsAWrite: the drawn state and
// saveLine have to agree in EVERY field state. The cost input has no validator
// — only a character limit — so "-5", "50,00" and "$50" all reach the field,
// and every note about the cost was built from "the field is not empty". Ctrl-E
// on "-5" therefore armed the save and said "enter writes $-5 as the total for
// the 10 ordered", while enter refused it outright: a money write promised in
// two places and made in none.
func TestPOLineEdit_RejectedCostIsNeverAnnouncedAsAWrite(t *testing.T) {
	for _, typed := range []string{"-5", "50,00"} {
		t.Run("types "+typed, func(t *testing.T) {
			fake := poPriceOrder()
			r, _ := poPriceRoot(t, fake)
			// At the width the caveat above had to survive, because the reason
			// enter refuses is the same kind of thing: the one part of the line
			// that cannot be reconstructed from what is already on screen.
			r = poResize(t, r, 80)

			r, s := poOpenLineEditor(t, r, 4)
			for range s.lineInputs[poLineEditCost].Value() {
				r = key(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
			}
			r = poTypeRunes(t, r, typed)

			// The hint before any key is pressed: no offer of a write.
			out := s.View()
			if strings.Contains(out, "$"+typed) {
				t.Errorf("an entry enter will refuse must not be quoted as money:\n%s", out)
			}
			if !strings.Contains(out, "enter will not save") ||
				!strings.Contains(out, "ine cost must be a non-negative number") {
				t.Errorf("the screen should name the refusal enter is going to make:\n%s", out)
			}
			// And it reaches the glass at 80 columns rather than being clipped
			// down to a sentence with the rule missing.
			if seen := stripANSI(r.View()); !strings.Contains(seen, "Line cost must be a non-negative number") {
				t.Errorf("the refusal is clipped off the pane at 80 columns:\n%s", seen)
			}

			// And after Ctrl-E, which arms the save: the arm is real (the
			// operator may still fix the figure) but it must not claim a write.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
			out = s.View()
			if strings.Contains(out, "Price confirmed") || strings.Contains(out, "enter writes $"+typed) {
				t.Errorf("the armed note promises a write saveLine will reject:\n%s", out)
			}
			if !strings.Contains(out, "enter will not save") {
				t.Errorf("the armed state should still name the refusal:\n%s", out)
			}
			if status := stripANSI(r.View()); !strings.Contains(status, "enter will reject") {
				t.Errorf("the Ctrl-E status line should say what enter will really do:\n%s", status)
			}

			// The refusal the notes named is the refusal that happens.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if sent := fake.lineCostPatches(); len(sent) != 0 {
				t.Errorf("a rejected cost must reach no endpoint; sent %v", sent)
			}
			if s.errMsg != "line cost must be a non-negative number" {
				t.Errorf("saveLine error = %q, want the refusal the notes predicted", s.errMsg)
			}
			if got := fake.line("line-zeroed").unitCostActual.FloatString(4); got != "0.0000" {
				t.Errorf("unit_cost_actual = %s, want it untouched", got)
			}
		})
	}
}
