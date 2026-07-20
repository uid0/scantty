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
// envelope) carrying one fully-populated restock-interval row and one
// insufficient-history row with every nullable field null — the shape the v2
// report actions actually emit, retired v1 quantity columns and all.
const demandForecastJSON = `[
  {
    "id": 42,
    "item": "11111111-2222-3333-4444-555555555555",
    "item_name": "PLA filament",
    "sku": "PLA-175",
    "category_name": "Printing",
    "generated_at": "2026-07-18T03:00:00Z",
    "avg_interval_days": 47.5,
    "interval_samples": 5,
    "last_restock_date": "2026-06-25",
    "predicted_next_reorder_date": "2026-08-12",
    "days_until_due": -3.0,
    "horizon_days": 0,
    "predicted_daily_demand": 0.0,
    "horizon_demand": 0.0,
    "horizon_demand_upper": 0.0,
    "days_until_stockout": null,
    "projected_stockout_date": null,
    "predictive_reorder_point": 0,
    "safety_stock": 0,
    "available_at_generation": 4,
    "needs_reorder": true,
    "lead_time_days": 7,
    "method": "restock_interval",
    "model_version": "interval-1"
  },
  {
    "id": 43,
    "item": "66666666-7777-8888-9999-000000000000",
    "item_name": "Blue tape",
    "sku": "TAPE-B",
    "category_name": null,
    "generated_at": "2026-07-18T03:00:00Z",
    "avg_interval_days": null,
    "interval_samples": 0,
    "last_restock_date": null,
    "predicted_next_reorder_date": null,
    "days_until_due": null,
    "horizon_days": 0,
    "predicted_daily_demand": 0.0,
    "horizon_demand": 0.0,
    "horizon_demand_upper": 0.0,
    "days_until_stockout": null,
    "projected_stockout_date": null,
    "predictive_reorder_point": 0,
    "safety_stock": 0,
    "available_at_generation": 30,
    "needs_reorder": false,
    "lead_time_days": null,
    "method": "insufficient_history",
    "model_version": ""
  }
]`

// TestDemandForecast_Contract pins the endpoint path, the low_stock_only query
// key, and the decode of both a fully-populated interval row and an
// insufficient-history one: nullable numbers stay nil rather than collapsing to
// 0, and a null date/category decodes to nil/"".
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
	// The interval signal: cadence, the history behind it, and the due date.
	if full.AvgIntervalDays == nil || *full.AvgIntervalDays != 47.5 {
		t.Errorf("avg_interval_days = %v", full.AvgIntervalDays)
	}
	if full.IntervalSamples != 5 {
		t.Errorf("interval_samples = %d, want 5", full.IntervalSamples)
	}
	if full.LastRestockDate == nil || *full.LastRestockDate != "2026-06-25" {
		t.Errorf("last_restock_date = %v", full.LastRestockDate)
	}
	if full.PredictedNextReorderDate == nil || *full.PredictedNextReorderDate != "2026-08-12" {
		t.Errorf("predicted_next_reorder_date = %v", full.PredictedNextReorderDate)
	}
	if full.DaysUntilDue == nil || *full.DaysUntilDue != -3 {
		t.Errorf("days_until_due = %v, want -3 (overdue stays negative)", full.DaysUntilDue)
	}
	if full.LeadTimeDays == nil || *full.LeadTimeDays != 7 {
		t.Errorf("lead_time_days = %v", full.LeadTimeDays)
	}
	if full.AvailableAtGeneration != 4 || !full.NeedsReorder {
		t.Errorf("reorder fields = %+v", full)
	}
	if full.Method != ForecastMethodRestockInterval || full.ModelVersion != "interval-1" {
		t.Errorf("model fields = %q / %q", full.Method, full.ModelVersion)
	}
	if full.GeneratedAt.IsZero() || full.GeneratedAt.UTC().Format("2006-01-02 15:04") != "2026-07-18 03:00" {
		t.Errorf("generated_at = %v", full.GeneratedAt)
	}

	// Retired v1 columns: a zero the backend really sent must stay a zero, and
	// a null must stay nil — a v2 row sends both, so the pointer types are what
	// keeps "0/day" from being invented out of a null.
	if full.PredictedDailyDemand == nil || *full.PredictedDailyDemand != 0 {
		t.Errorf("a sent 0 predicted_daily_demand must decode to a non-nil 0, got %v",
			full.PredictedDailyDemand)
	}
	if full.HorizonDays == nil || *full.HorizonDays != 0 {
		t.Errorf("a sent 0 horizon_days must decode to a non-nil 0, got %v", full.HorizonDays)
	}
	if full.DaysUntilStockout != nil {
		t.Errorf("null days_until_stockout should decode to nil, got %v", *full.DaysUntilStockout)
	}
	if full.ProjectedStockoutDate != nil {
		t.Errorf("null projected_stockout_date should decode to nil, got %q", *full.ProjectedStockoutDate)
	}

	// The insufficient-history row: every interval nullable is null and must
	// stay distinguishable from zero, or the screen would print a cadence of 0
	// days and a due date of today.
	nulls := rows[1]
	if nulls.Method != ForecastMethodInsufficientHistory {
		t.Errorf("method = %q, want %q", nulls.Method, ForecastMethodInsufficientHistory)
	}
	if nulls.AvgIntervalDays != nil {
		t.Errorf("null avg_interval_days should decode to nil, got %v", *nulls.AvgIntervalDays)
	}
	if nulls.DaysUntilDue != nil {
		t.Errorf("null days_until_due should decode to nil, got %v", *nulls.DaysUntilDue)
	}
	if nulls.LastRestockDate != nil || nulls.PredictedNextReorderDate != nil {
		t.Errorf("null dates should decode to nil, got %v / %v",
			nulls.LastRestockDate, nulls.PredictedNextReorderDate)
	}
	if nulls.IntervalSamples != 0 {
		t.Errorf("interval_samples = %d, want 0", nulls.IntervalSamples)
	}
	if nulls.LeadTimeDays != nil {
		t.Errorf("null lead_time_days should decode to nil, got %v", *nulls.LeadTimeDays)
	}
	if nulls.CategoryName != "" {
		t.Errorf("null category should decode to empty, got %q", nulls.CategoryName)
	}
}

// TestDemandForecast_LegacyRowDecodes: a row written by the retired v1 engine
// is still readable — its quantity projection carries real numbers where a v2
// row carries 0/null, which is what the detail view keys off to show it.
func TestDemandForecast_LegacyRowDecodes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"id": 7, "item": "itm-1", "item_name": "Blue tape",
			"avg_interval_days": null, "interval_samples": 0,
			"last_restock_date": null, "predicted_next_reorder_date": null,
			"days_until_due": null,
			"horizon_days": 21, "predicted_daily_demand": 1.2857,
			"horizon_demand": 27.0, "horizon_demand_upper": 34.5,
			"days_until_stockout": 3.25, "projected_stockout_date": "2026-07-22",
			"predictive_reorder_point": 12, "safety_stock": 6,
			"available_at_generation": 4, "needs_reorder": true, "lead_time_days": 7,
			"method": "holtwinters", "model_version": "hw-1"
		}]`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).DemandForecast(context.Background(), nil)
	if err != nil {
		t.Fatalf("DemandForecast: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Method != ForecastMethodHoltWinters {
		t.Errorf("method = %q", r.Method)
	}
	if r.HorizonDays == nil || *r.HorizonDays != 21 {
		t.Errorf("horizon_days = %v", r.HorizonDays)
	}
	if r.PredictedDailyDemand == nil || *r.PredictedDailyDemand != 1.2857 {
		t.Errorf("predicted_daily_demand = %v", r.PredictedDailyDemand)
	}
	if r.HorizonDemand == nil || *r.HorizonDemand != 27 ||
		r.HorizonDemandUpper == nil || *r.HorizonDemandUpper != 34.5 {
		t.Errorf("horizon demand = %v / %v", r.HorizonDemand, r.HorizonDemandUpper)
	}
	if r.DaysUntilStockout == nil || *r.DaysUntilStockout != 3.25 {
		t.Errorf("days_until_stockout = %v", r.DaysUntilStockout)
	}
	if r.ProjectedStockoutDate == nil || *r.ProjectedStockoutDate != "2026-07-22" {
		t.Errorf("projected_stockout_date = %v", r.ProjectedStockoutDate)
	}
	if r.PredictiveReorderPoint == nil || *r.PredictiveReorderPoint != 12 ||
		r.SafetyStock == nil || *r.SafetyStock != 6 {
		t.Errorf("reorder point / safety stock = %v / %v", r.PredictiveReorderPoint, r.SafetyStock)
	}
	// A v1 row has no interval signal at all.
	if r.AvgIntervalDays != nil || r.DaysUntilDue != nil || r.LastRestockDate != nil {
		t.Errorf("a v1 row must carry no interval signal, got %+v", r)
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
