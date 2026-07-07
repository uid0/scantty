package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetItemMetrics_Contract pins the metrics GET path (trailing slash) and
// that the pointer/decimal fields decode — including a null (case_size) → nil.
func TestGetItemMetrics_Contract(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"current_stock":5,"quantity_on_order":0,"quantity_available":5.0,
			"quantity_committed":0.0,"quantity_in_transit":0,"reorder_point":3,
			"lead_time_days":7,"unit_cost":"11.22","cost_trend":"up",
			"last_po_unit_cost":"10.00","is_case_based":false,"case_size":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	m, err := c.GetItemMetrics(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetItemMetrics: %v", err)
	}
	if path != "/api/inventory/items/abc/metrics/" {
		t.Fatalf("path = %q, want trailing-slashed metrics path", path)
	}
	if m.CurrentStock == nil || *m.CurrentStock != 5 {
		t.Errorf("current_stock = %v", m.CurrentStock)
	}
	// QA/QC are DRF FloatField on the backend → JSON 5.0/0.0. These MUST decode
	// into *float64 (a *int would fail the whole response decode).
	if m.QuantityAvailable == nil || *m.QuantityAvailable != 5 {
		t.Errorf("quantity_available = %v (float 5.0 must decode)", m.QuantityAvailable)
	}
	if m.QuantityCommitted == nil || *m.QuantityCommitted != 0 {
		t.Errorf("quantity_committed = %v", m.QuantityCommitted)
	}
	if m.ReorderPoint == nil || *m.ReorderPoint != 3 {
		t.Errorf("reorder_point = %v", m.ReorderPoint)
	}
	if m.LeadTimeDays == nil || *m.LeadTimeDays != 7 {
		t.Errorf("lead_time_days = %v", m.LeadTimeDays)
	}
	if m.UnitCost != "11.22" {
		t.Errorf("unit_cost = %q", m.UnitCost)
	}
	if m.CostTrend != "up" {
		t.Errorf("cost_trend = %q", m.CostTrend)
	}
	if m.CaseSize != nil {
		t.Errorf("case_size should decode to nil, got %v", *m.CaseSize)
	}
}

// TestGetItemMetrics_NullCounts confirms null count fields decode to nil (so the
// row can render "-") rather than failing the whole decode.
func TestGetItemMetrics_NullCounts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"current_stock":null,"reorder_point":null,
			"lead_time_days":null,"unit_cost":null,"cost_trend":"no_history"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	m, err := c.GetItemMetrics(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetItemMetrics: %v", err)
	}
	if m.CurrentStock != nil || m.ReorderPoint != nil || m.LeadTimeDays != nil {
		t.Errorf("null counts should be nil: %+v", m)
	}
	if !m.UnitCost.Empty() {
		t.Errorf("null unit_cost should be empty, got %q", m.UnitCost)
	}
}

// TestCycleCountItem_Contract pins the POST path (trailing slash — avoids the
// 301 method downgrade), the request body field names/values, and that the
// re-serialized item (with the new count fields) decodes back.
func TestCycleCountItem_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Widget","current_stock":8,
			"last_counted_at":"2026-07-07T00:00:00Z","days_since_last_count":0}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.CycleCountItem(context.Background(), "abc", 8, "miscounted", false, "recount after audit")
	if err != nil {
		t.Fatalf("CycleCountItem: %v", err)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/items/abc/cycle-count/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["counted_qty"].(float64) != 8 {
		t.Errorf("counted_qty = %v", captured.body["counted_qty"])
	}
	if captured.body["reason"] != "miscounted" {
		t.Errorf("reason = %v", captured.body["reason"])
	}
	if captured.body["skip_reorder"] != false {
		t.Errorf("skip_reorder = %v", captured.body["skip_reorder"])
	}
	if captured.body["notes"] != "recount after audit" {
		t.Errorf("notes = %v", captured.body["notes"])
	}
	if item.Stock != 8 {
		t.Errorf("current_stock = %d", item.Stock)
	}
	if item.DaysSinceLastCount == nil || *item.DaysSinceLastCount != 0 {
		t.Errorf("days_since_last_count = %v", item.DaysSinceLastCount)
	}
	if item.LastCountedAt == nil || item.LastCountedAt.IsZero() {
		t.Errorf("last_counted_at not decoded: %v", item.LastCountedAt)
	}
}

// TestCycleCountItem_OmitsEmptyNotes confirms an empty note is omitted from the
// body (json omitempty) and that skip_reorder / a zero count still ride along.
func TestCycleCountItem_OmitsEmptyNotes(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Widget","current_stock":0}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CycleCountItem(context.Background(), "abc", 0, "lost", true, ""); err != nil {
		t.Fatalf("CycleCountItem: %v", err)
	}
	if _, present := body["notes"]; present {
		t.Errorf("empty notes should be omitted, got %v", body["notes"])
	}
	if body["skip_reorder"] != true {
		t.Errorf("skip_reorder = %v, want true", body["skip_reorder"])
	}
	if body["counted_qty"].(float64) != 0 {
		t.Errorf("counted_qty = %v, want 0", body["counted_qty"])
	}
}
