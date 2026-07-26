package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestInventoryDetail_PurchaseHistory_QueryAndRender drives the real Init
// fan-out against a fake OMS and asserts the provenance endpoint is fetched
// alongside the item, that each order shows what it was placed at per unit, and
// that the deliveries are GROUPED by order so a partially-shipped order lists
// both of its tracking numbers under one heading.
func TestInventoryDetail_PurchaseHistory_QueryAndRender(t *testing.T) {
	var historyHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/purchase_history/"):
			historyHits++
			_, _ = w.Write([]byte(`{
				"order_costs":[
					{"purchase_order":7,"po_number":"PO-2026-0007","order_date":"2026-05-01T10:00:00Z",
					 "status":"received","quantity_ordered":24,
					 "unit_cost_ordered":"1.1000","unit_cost_actual":"1.0500"},
					{"purchase_order":11,"po_number":null,"order_date":"2026-06-14T09:30:00Z",
					 "status":"draft","quantity_ordered":12,
					 "unit_cost_ordered":"1.2000","unit_cost_actual":null}
				],
				"deliveries":[
					{"purchase_order":7,"po_number":"PO-2026-0007","delivery_date":"2026-05-09T12:00:00Z",
					 "tracking_number":"1Z999AA1","carrier":"UPS","quantity_received":12,
					 "receipt_notes":"box crushed\non one corner","is_complete":false},
					{"purchase_order":7,"po_number":"PO-2026-0007","delivery_date":"2026-05-12T12:00:00Z",
					 "tracking_number":"1Z999AA2","carrier":"UPS","quantity_received":12,
					 "receipt_notes":"","is_complete":true},
					{"purchase_order":11,"po_number":null,"delivery_date":"2026-06-20T12:00:00Z",
					 "tracking_number":"","carrier":"","quantity_received":4,
					 "receipt_notes":"","is_complete":false}
				]}`))
		case strings.HasPrefix(r.URL.Path, "/api/inventory/assets/"):
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		case strings.HasSuffix(r.URL.Path, "/metrics/"):
			_, _ = w.Write([]byte(`{"current_stock":4}`))
		default:
			_, _ = w.Write([]byte(`{"id":"item-1","name":"Ink Cartridge","sku":"INK-1","current_stock":4}`))
		}
	}))
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL)}, "item-1")
	for _, msg := range ccDrainCmd(s.Init()) {
		s.Update(msg)
	}

	if historyHits != 1 {
		t.Fatalf("purchase_history fetched %d times, want exactly 1 (batched in Init)", historyHits)
	}
	if s.purchasesLoading {
		t.Errorf("load should clear the loading flag")
	}
	if s.purchasesErr != "" {
		t.Fatalf("unexpected load error: %s", s.purchasesErr)
	}

	body := s.renderBody()
	for _, want := range []string{
		"Purchase / Receipts",
		"Orders (2)",
		// Placed-at vs actual: both are shown when the supplier re-priced.
		"PO-2026-0007", "2026-05-01", "received", "qty 24", "$1.10/unit → $1.05 actual",
		// Not yet delivered → no actual cost to show.
		"2026-06-14", "draft", "qty 12", "$1.20/unit",
		// A PO with no number yet is still identifiable by its pk.
		"order 11 (no PO number)",
		"Deliveries (3)",
		"2026-05-09", "UPS 1Z999AA1", "not yet processed",
		"2026-05-12", "UPS 1Z999AA2", "processed",
		// A multi-line receipt note is flattened so it can't break the layout.
		"note: box crushed on one corner",
		// A hand-carried receipt has no tracking; say so rather than leave a gap.
		"no tracking number",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}

	// Grouping check: PO-2026-0007 has two deliveries but appears exactly twice —
	// once as its order line, once as the single delivery heading over both rows.
	if got := strings.Count(body, "PO-2026-0007"); got != 2 {
		t.Errorf("PO-2026-0007 appears %d times, want 2 (deliveries grouped under one heading):\n%s", got, body)
	}
}

// TestInventoryDetail_PurchaseHistory_States covers the three non-row states.
// Never-ordered is real information, so it gets a message rather than a
// vanished section, and a failed fetch must not read as never-ordered.
func TestInventoryDetail_PurchaseHistory_States(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "item-1")
	s.item = &omsapi.Item{ID: "item-1", Name: "Widget", SKU: "W-1"}
	s.loading = false

	// While loading the header is already drawn, so the section can't pop into
	// existence later and shift the scroll out from under the operator.
	loading := s.renderPurchaseSection()
	if !strings.Contains(loading, "Purchase / Receipts") || !strings.Contains(loading, "loading…") {
		t.Errorf("loading state should draw the header and say so: %q", loading)
	}

	s.Update(inventoryPurchaseHistoryLoadedMsg{history: &omsapi.ItemPurchaseHistory{}})
	empty := s.renderPurchaseSection()
	if !strings.Contains(empty, "Never ordered") {
		t.Errorf("empty state missing its message: %q", empty)
	}

	s.Update(inventoryPurchaseHistoryLoadedMsg{err: errFake("boom")})
	failed := s.renderPurchaseSection()
	if !strings.Contains(failed, "unavailable: boom") {
		t.Errorf("error state should surface the reason: %q", failed)
	}
	if strings.Contains(failed, "Never ordered") {
		t.Errorf("a failed fetch must not read as 'never ordered': %q", failed)
	}
}

// TestInventoryDetail_PurchaseHistory_OrderedNotReceived covers the in-between
// state: an open order with nothing received yet still shows its unit cost, and
// says plainly that no delivery has landed.
func TestInventoryDetail_PurchaseHistory_OrderedNotReceived(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "item-1")
	s.item = &omsapi.Item{ID: "item-1", Name: "Widget", SKU: "W-1"}
	s.loading = false
	s.Update(inventoryPurchaseHistoryLoadedMsg{history: &omsapi.ItemPurchaseHistory{
		OrderCosts: []omsapi.ItemOrderCost{{
			PurchaseOrder: 3, PONumber: "PO-2026-0003", Status: "sent",
			QuantityOrdered: 6, UnitCostOrdered: "2.5000",
		}},
	}})

	got := s.renderPurchaseSection()
	for _, want := range []string{"Orders (1)", "PO-2026-0003", "sent", "qty 6", "$2.50/unit", "Deliveries: none received yet"} {
		if !strings.Contains(got, want) {
			t.Errorf("section missing %q:\n%s", want, got)
		}
	}
}

// TestInventoryDetail_CommittedBreakdown_Render asserts the QC attribution
// renders under the metrics row: one line per work order holding stock, naming
// the asset the job is on, and nothing at all when there's nothing committed.
func TestInventoryDetail_CommittedBreakdown_Render(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "item-1")
	s.item = &omsapi.Item{ID: "item-1", Name: "Widget", SKU: "W-1"}
	s.loading = false

	// No metrics yet, and metrics without a breakdown (QC 0 or an older
	// backend): the block is absent rather than an empty header.
	if got := s.renderCommittedBreakdown(); got != "" {
		t.Errorf("no metrics should render nothing, got %q", got)
	}
	s.Update(inventoryMetricsLoadedMsg{metrics: &omsapi.ItemMetrics{QuantityCommitted: ccPtr(0.0)}})
	if got := s.renderCommittedBreakdown(); got != "" {
		t.Errorf("empty breakdown should render nothing, got %q", got)
	}

	s.Update(inventoryMetricsLoadedMsg{metrics: &omsapi.ItemMetrics{
		QuantityCommitted: ccPtr(3.5),
		CommittedBreakdown: []omsapi.CommittedBreakdownEntry{
			{WorkOrderShortID: "WO-1A2B3C4D", AssetName: "Laser Cutter", Quantity: 2},
			{WorkOrderShortID: "WO-5E6F7A8B", Quantity: 1.5},
		},
	}})
	got := s.renderCommittedBreakdown()
	for _, want := range []string{
		"Committed to (QC 3.5)",
		"WO-1A2B3C4D", "Laser Cutter", "qty 2",
		// An asset-less work order says so rather than showing a blank cell.
		"WO-5E6F7A8B", "no asset", "qty 1.5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("committed block missing %q:\n%s", want, got)
		}
	}
	// It opens the body, directly beneath the pinned metrics row it explains.
	if body := s.renderBody(); !strings.HasPrefix(body, got) {
		t.Errorf("committed block should lead the body:\n%s", body)
	}
}
