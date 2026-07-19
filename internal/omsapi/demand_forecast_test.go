package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// demandForecastJSON is a bare array (Response(serializer.data), no pagination
// envelope) carrying one fully-populated row and one with every nullable field
// null — the shape the report actions actually emit.
const demandForecastJSON = `[
  {
    "id": 42,
    "item": "11111111-2222-3333-4444-555555555555",
    "item_name": "PLA filament",
    "sku": "PLA-175",
    "category_name": "Printing",
    "generated_at": "2026-07-18T03:00:00Z",
    "horizon_days": 21,
    "predicted_daily_demand": 1.2857,
    "horizon_demand": 27.0,
    "horizon_demand_upper": 34.5,
    "available_at_generation": 4,
    "days_until_stockout": 3.25,
    "projected_stockout_date": "2026-07-22",
    "predictive_reorder_point": 12,
    "needs_reorder": true,
    "lead_time_days": 7,
    "safety_stock": 6,
    "method": "holtwinters",
    "model_version": "hw-1"
  },
  {
    "id": 43,
    "item": "66666666-7777-8888-9999-000000000000",
    "item_name": "Blue tape",
    "sku": "TAPE-B",
    "category_name": null,
    "generated_at": "2026-07-18T03:00:00Z",
    "horizon_days": 14,
    "predicted_daily_demand": 0.0,
    "horizon_demand": 0.0,
    "horizon_demand_upper": 0.0,
    "available_at_generation": 30,
    "days_until_stockout": null,
    "projected_stockout_date": null,
    "predictive_reorder_point": 2,
    "needs_reorder": false,
    "lead_time_days": null,
    "safety_stock": 0,
    "method": "fallback",
    "model_version": ""
  }
]`

// TestDemandForecast_Contract pins the endpoint path, the low_stock_only query
// key, and the decode of both a fully-populated row and an all-nulls one:
// nullable numbers stay nil (not 0) and a null date/category decodes to "".
func TestDemandForecast_Contract(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(demandForecastJSON))
	}))
	defer srv.Close()

	c := New(srv.URL)
	rows, err := c.DemandForecast(context.Background(), url.Values{"low_stock_only": []string{"true"}})
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	if gotPath != "/api/inventory/reports/inventory/demand_forecast/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "low_stock_only=true" {
		t.Errorf("query = %q, want low_stock_only=true", gotQuery)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	full := rows[0]
	if full.ID != 42 || full.Item != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("identity = %d / %q", full.ID, full.Item)
	}
	if full.ItemName != "PLA filament" || full.SKU != "PLA-175" || full.CategoryName != "Printing" {
		t.Errorf("item fields = %+v", full)
	}
	if full.HorizonDays != 21 || full.PredictedDailyDemand != 1.2857 ||
		full.HorizonDemand != 27 || full.HorizonDemandUpper != 34.5 {
		t.Errorf("projection fields = %+v", full)
	}
	if full.DaysUntilStockout == nil || *full.DaysUntilStockout != 3.25 {
		t.Errorf("days_until_stockout = %v", full.DaysUntilStockout)
	}
	if full.LeadTimeDays == nil || *full.LeadTimeDays != 7 {
		t.Errorf("lead_time_days = %v", full.LeadTimeDays)
	}
	if full.ProjectedStockoutDate != "2026-07-22" || full.AvailableAtGeneration != 4 ||
		full.PredictiveReorderPoint != 12 || full.SafetyStock != 6 || !full.NeedsReorder {
		t.Errorf("reorder fields = %+v", full)
	}
	if full.Method != ForecastMethodHoltWinters || full.ModelVersion != "hw-1" {
		t.Errorf("model fields = %q / %q", full.Method, full.ModelVersion)
	}
	if full.GeneratedAt.IsZero() || full.GeneratedAt.UTC().Format("2006-01-02 15:04") != "2026-07-18 03:00" {
		t.Errorf("generated_at = %v", full.GeneratedAt)
	}

	// Nulls must stay distinguishable from zero.
	nulls := rows[1]
	if nulls.DaysUntilStockout != nil {
		t.Errorf("null days_until_stockout should decode to nil, got %v", *nulls.DaysUntilStockout)
	}
	if nulls.LeadTimeDays != nil {
		t.Errorf("null lead_time_days should decode to nil, got %v", *nulls.LeadTimeDays)
	}
	if nulls.ProjectedStockoutDate != "" || nulls.CategoryName != "" {
		t.Errorf("null date/category should decode to empty, got %q / %q",
			nulls.ProjectedStockoutDate, nulls.CategoryName)
	}
}

// TestDemandForecast_OmitsQueryWhenUnfiltered: the unfiltered call sends no
// query string at all (the backend defaults to the full set).
func TestDemandForecast_OmitsQueryWhenUnfiltered(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).DemandForecast(context.Background(), nil)
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want none", gotQuery)
	}
	// The empty list is the documented pre-forecast state, not an error.
	if len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}

// TestReorderAlerts_Contract pins the notify-set path and confirms it's called
// without query params — the flagged-and-opted-in filter is entirely
// server-side.
func TestReorderAlerts_Contract(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(demandForecastJSON))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ReorderAlerts(context.Background())
	if err != nil {
		t.Fatalf("ReorderAlerts: %v", err)
	}
	if gotPath != "/api/inventory/reports/inventory/reorder_alerts/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "" {
		t.Errorf("query = %q, want none", gotQuery)
	}
	if len(rows) != 2 || rows[0].ItemName != "PLA filament" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestReorderAlerts_PaginatedEnvelope: the report actions return a bare array
// today, but MaybeList also tolerates a paginated envelope if one is ever
// introduced.
func TestReorderAlerts_PaginatedEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[
			{"id":1,"item":"itm-1","item_name":"Widget","needs_reorder":true,"method":"prophet"}
		]}`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ReorderAlerts(context.Background())
	if err != nil {
		t.Fatalf("ReorderAlerts: %v", err)
	}
	if len(rows) != 1 || rows[0].ItemName != "Widget" || !rows[0].NeedsReorder {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestItemWrite_CarriesReorderAlertsOptIn: the ML alert opt-in must round-trip
// on the item — read off the serializer, and written on EVERY PATCH including
// when switched off (no omitempty), otherwise un-watching an item would be
// silently dropped from the payload.
func TestItemWrite_CarriesReorderAlertsOptIn(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"itm-1","name":"Filament","reorder_alerts_enabled":true}`))
	}))
	defer srv.Close()

	item, err := New(srv.URL).UpdateInventoryItem(context.Background(), "itm-1",
		ItemWrite{Name: "Filament", ReorderAlertsEnabled: false})
	if err != nil {
		t.Fatalf("UpdateInventoryItem: %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body not JSON: %v (%s)", err, body)
	}
	got, ok := sent["reorder_alerts_enabled"]
	if !ok {
		t.Fatalf("reorder_alerts_enabled missing from the PATCH body: %s", body)
	}
	if got != false {
		t.Errorf("reorder_alerts_enabled = %v, want false", got)
	}
	if !item.ReorderAlertsEnabled {
		t.Errorf("response opt-in should decode onto the item")
	}
}
