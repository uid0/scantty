package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetPurchaseHistory_Contract pins the endpoint path (underscored, trailing
// slash — DRF derives the action path from the method name) and that both lists
// decode, including the nullable fields: a PO with no number yet (po_number
// null → "", which is why the pk rides along as the grouping key) and a line
// whose actual unit cost isn't known until a delivery prices it.
func TestGetPurchaseHistory_Contract(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
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
				 "receipt_notes":"box crushed","is_complete":false},
				{"purchase_order":7,"po_number":"PO-2026-0007","delivery_date":"2026-05-12T12:00:00Z",
				 "tracking_number":"1Z999AA2","carrier":"UPS","quantity_received":12,
				 "receipt_notes":"","is_complete":true}
			]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	ph, err := c.GetPurchaseHistory(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetPurchaseHistory: %v", err)
	}
	if path != "/api/inventory/items/abc/purchase_history/" {
		t.Fatalf("path = %q, want the underscored trailing-slashed action path", path)
	}
	if len(ph.OrderCosts) != 2 {
		t.Fatalf("order_costs = %d, want 2", len(ph.OrderCosts))
	}
	first := ph.OrderCosts[0]
	if first.PurchaseOrder != 7 || first.PONumber != "PO-2026-0007" {
		t.Errorf("first order po = %d/%q", first.PurchaseOrder, first.PONumber)
	}
	if got := first.OrderDate.Format("2006-01-02"); got != "2026-05-01" {
		t.Errorf("order_date = %q (DRF ISO-8601 must decode into time.Time)", got)
	}
	if first.Status != "received" || first.QuantityOrdered != 24 {
		t.Errorf("first order status/qty = %q/%d", first.Status, first.QuantityOrdered)
	}
	if first.UnitCostOrdered != "1.1000" || first.UnitCostActual != "1.0500" {
		t.Errorf("first order costs = %q/%q", first.UnitCostOrdered, first.UnitCostActual)
	}
	// A numberless PO must still be identifiable — the pk is the grouping key.
	second := ph.OrderCosts[1]
	if second.PONumber != "" {
		t.Errorf("null po_number should decode to empty, got %q", second.PONumber)
	}
	if second.PurchaseOrder != 11 {
		t.Errorf("numberless order must still carry its pk, got %d", second.PurchaseOrder)
	}
	if !second.UnitCostActual.Empty() {
		t.Errorf("null unit_cost_actual should be empty, got %q", second.UnitCostActual)
	}

	// One partially-shipped order → one row per delivery, each with its own
	// tracking number. That is the whole point of the flat list.
	if len(ph.Deliveries) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(ph.Deliveries))
	}
	if ph.Deliveries[0].TrackingNumber != "1Z999AA1" || ph.Deliveries[1].TrackingNumber != "1Z999AA2" {
		t.Errorf("tracking numbers = %q/%q", ph.Deliveries[0].TrackingNumber, ph.Deliveries[1].TrackingNumber)
	}
	if ph.Deliveries[0].PurchaseOrder != ph.Deliveries[1].PurchaseOrder {
		t.Errorf("both deliveries belong to the same PO: %d/%d",
			ph.Deliveries[0].PurchaseOrder, ph.Deliveries[1].PurchaseOrder)
	}
	d0 := ph.Deliveries[0]
	if got := d0.DeliveryDate.Format("2006-01-02"); got != "2026-05-09" {
		t.Errorf("delivery_date = %q", got)
	}
	if d0.Carrier != "UPS" || d0.QuantityReceived != 12 || d0.ReceiptNotes != "box crushed" {
		t.Errorf("first delivery = %+v", d0)
	}
	if d0.IsComplete || !ph.Deliveries[1].IsComplete {
		t.Errorf("is_complete = %v/%v, want false/true", d0.IsComplete, ph.Deliveries[1].IsComplete)
	}
}

// TestGetPurchaseHistory_NeverOrdered confirms the never-ordered case is an
// empty 200 rather than an error, so the caller can say "never ordered" instead
// of "unavailable".
func TestGetPurchaseHistory_NeverOrdered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"order_costs":[],"deliveries":[]}`))
	}))
	defer srv.Close()

	ph, err := New(srv.URL).GetPurchaseHistory(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetPurchaseHistory: %v", err)
	}
	if len(ph.OrderCosts) != 0 || len(ph.Deliveries) != 0 {
		t.Errorf("expected both lists empty, got %+v", ph)
	}
}
