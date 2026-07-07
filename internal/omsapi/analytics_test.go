package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetAnalyticsPulse_DecodesAllAggregations round-trips a realistic /pulse/
// payload and asserts every aggregation decodes with the correct wire types:
// hours_used as int, category_spend money as Decimal-strings, nullable
// category_id / interval fields as pointers, and null monthly_budget → "".
func TestGetAnalyticsPulse_DecodesAllAggregations(t *testing.T) {
	var gotPath, gotBucket string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBucket = r.URL.Query().Get("bucket")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"summary": {
				"period_start": "2026-06-01",
				"period_end": "2026-06-30",
				"internal_completed_count": 12,
				"internal_estimated_external_cost": "3400.00",
				"internal_estimated_internal_cost": "800.00",
				"internal_net_value": "2600.00",
				"external_closed_count": 3,
				"external_actual_cost": "1250.50",
				"external_estimated_cost": "1400.00",
				"total_value_to_makerspace": "3850.50"
			},
			"wo_volume_trend": [
				{"period": "2026-05-01", "count": 8},
				{"period": "2026-06-01", "count": 12}
			],
			"top_users": [
				{"user_id": 7, "username": "welder", "display_name": "Wanda Welder", "completed_wo_count": 5}
			],
			"utilization": [
				{"asset_id": "a-1", "asset_name": "Lathe", "category": "Machining",
				 "hours_used": 40, "completed_wo_count": 2, "status": "operational"},
				{"asset_id": "a-2", "asset_name": "Orphan", "category": null,
				 "hours_used": 0, "completed_wo_count": 0, "status": "operational"}
			],
			"category_spend": [
				{"category_id": 4, "category_name": "Gases", "internal_estimated": "120.00",
				 "external_estimated": "300.00", "external_actual": "275.25",
				 "internal_wo_count": 6, "external_wo_count": 1},
				{"category_id": null, "category_name": null, "internal_estimated": "0.00",
				 "external_estimated": "0.00", "external_actual": "0.00",
				 "internal_wo_count": 0, "external_wo_count": 0}
			],
			"maintenance_forecast": [
				{"asset_id": "a-1", "asset_name": "Lathe", "category": "Machining",
				 "status": "operational", "hours_used": 40,
				 "last_completed_wo_at": "2026-04-01T10:00:00+00:00",
				 "interval_hours": 100, "interval_days": 90, "days_since_last_wo": 97,
				 "due_reason": "both"},
				{"asset_id": "a-3", "asset_name": "New rig", "category": null,
				 "status": "operational", "hours_used": 2,
				 "last_completed_wo_at": null, "interval_hours": null,
				 "interval_days": null, "days_since_last_wo": null, "due_reason": "days"}
			],
			"monthly_budget": "5000.00"
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	p, err := c.GetAnalyticsPulse(context.Background(), "", "", "quarter")
	if err != nil {
		t.Fatalf("GetAnalyticsPulse: %v", err)
	}
	if gotPath != "/api/analytics/pulse/" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBucket != "quarter" {
		t.Errorf("bucket = %q, want quarter", gotBucket)
	}

	// Summary (money-as-string, counts int).
	if p.Summary.TotalValueToMakerspace != "3850.50" || p.Summary.InternalCompletedCount != 12 {
		t.Errorf("summary decode wrong: %+v", p.Summary)
	}
	if p.MonthlyBudget != "5000.00" {
		t.Errorf("monthly_budget = %q", p.MonthlyBudget)
	}

	// Trend.
	if len(p.WOVolumeTrend) != 2 || p.WOVolumeTrend[1].BucketStart != "2026-06-01" || p.WOVolumeTrend[1].WOCount != 12 {
		t.Errorf("wo_volume_trend decode wrong: %+v", p.WOVolumeTrend)
	}

	// Top users.
	if len(p.TopUsers) != 1 || p.TopUsers[0].FullName != "Wanda Welder" || p.TopUsers[0].WOCount != 5 {
		t.Errorf("top_users decode wrong: %+v", p.TopUsers)
	}

	// Utilization — hours_used is int; null category → "".
	if len(p.Utilization) != 2 {
		t.Fatalf("utilization len = %d", len(p.Utilization))
	}
	if p.Utilization[0].HoursUsed != 40 || p.Utilization[0].Category != "Machining" {
		t.Errorf("utilization[0] wrong: %+v", p.Utilization[0])
	}
	if p.Utilization[1].Category != "" {
		t.Errorf("utilization[1] null category should decode to \"\", got %q", p.Utilization[1].Category)
	}

	// Category spend — Decimal strings + null category_id.
	if len(p.CategorySpend) != 2 {
		t.Fatalf("category_spend len = %d", len(p.CategorySpend))
	}
	if p.CategorySpend[0].CategoryID == nil || *p.CategorySpend[0].CategoryID != 4 {
		t.Errorf("category_spend[0] category_id wrong: %v", p.CategorySpend[0].CategoryID)
	}
	if p.CategorySpend[0].ExternalActual != "275.25" {
		t.Errorf("category_spend[0] external_actual = %q", p.CategorySpend[0].ExternalActual)
	}
	if p.CategorySpend[1].CategoryID != nil {
		t.Errorf("category_spend[1] category_id should be nil, got %v", *p.CategorySpend[1].CategoryID)
	}

	// Maintenance forecast — int hours, pointer intervals, dt-str.
	if len(p.MaintenanceForecast) != 2 {
		t.Fatalf("maintenance_forecast len = %d", len(p.MaintenanceForecast))
	}
	f0 := p.MaintenanceForecast[0]
	if f0.HoursUsed != 40 || f0.DueReason != "both" || f0.LastCompletedWOAt != "2026-04-01T10:00:00+00:00" {
		t.Errorf("forecast[0] wrong: %+v", f0)
	}
	if f0.IntervalHours == nil || *f0.IntervalHours != 100 || f0.DaysSinceLastWO == nil || *f0.DaysSinceLastWO != 97 {
		t.Errorf("forecast[0] pointers wrong: %+v", f0)
	}
	f1 := p.MaintenanceForecast[1]
	if f1.IntervalHours != nil || f1.IntervalDays != nil || f1.DaysSinceLastWO != nil {
		t.Errorf("forecast[1] null intervals should be nil pointers: %+v", f1)
	}
	if f1.LastCompletedWOAt != "" {
		t.Errorf("forecast[1] null last_completed_wo_at should be \"\", got %q", f1.LastCompletedWOAt)
	}
}

// TestGetAnalyticsPulse_NullBudget: a null monthly_budget decodes to "" without
// error (the budget is unset when settings.MONTHLY_BUDGET_TOTAL is absent).
func TestGetAnalyticsPulse_NullBudget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"summary":{"period_start":"2026-06-01","period_end":"2026-06-30"},
			"wo_volume_trend":[],"top_users":[],"utilization":[],"category_spend":[],
			"maintenance_forecast":[],"monthly_budget":null}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	p, err := c.GetAnalyticsPulse(context.Background(), "2026-06-01", "2026-06-30", "")
	if err != nil {
		t.Fatalf("GetAnalyticsPulse: %v", err)
	}
	if p.MonthlyBudget != "" {
		t.Errorf("null monthly_budget should decode to \"\", got %q", p.MonthlyBudget)
	}
	if len(p.Utilization) != 0 || len(p.CategorySpend) != 0 || len(p.MaintenanceForecast) != 0 {
		t.Errorf("empty aggregations should decode to empty slices")
	}
}
