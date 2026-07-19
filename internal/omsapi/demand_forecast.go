package omsapi

import (
	"context"
	"net/url"
	"time"
)

// ML demand forecast (op-1) — the non-serialized sibling of the serialized
// component forecast in serialized.go. Two read-only report actions on the
// inventory report viewset return the SAME row payload (a stored
// inventory.DemandForecast plus the item's name/sku/category_name):
//
//   - demand_forecast/ — the latest stored row per active, non-retired,
//     non-serialized item that has one, most-urgent-first (reorder-flagged
//     first, then soonest stockout, nulls last). ?low_stock_only=1 narrows it
//     to the needs_reorder set.
//   - reorder_alerts/  — the notify set: the same rows filtered to items whose
//     owner opted in (reorder_alerts_enabled) AND that are due to reorder.
//     Takes no query params.
//
// Both read STORED rows only: they return an empty list until the nightly
// forecasting task has populated the table. That empty state is part of the
// contract, not an error — the screens say so rather than showing "no data".

// DemandForecastRow is one row of either report. Nullable backend fields are
// pointers so "unknown" stays distinct from zero: DaysUntilStockout is null
// when demand is projected at zero, LeadTimeDays when the item has no measured
// lead time. ProjectedStockoutDate is a bare YYYY-MM-DD string, "" when null
// (JSON null decodes into a string as a no-op).
type DemandForecastRow struct {
	// ID is the stored forecast row's pk (BigAutoField → number); Item is the
	// forecast item's UUID, matching Item.ID.
	ID           int64  `json:"id"`
	Item         string `json:"item"`
	ItemName     string `json:"item_name"`
	SKU          string `json:"sku,omitempty"`
	CategoryName string `json:"category_name,omitempty"`

	// GeneratedAt stamps the forecast run this row came from; HorizonDays is
	// the lead-time-plus-buffer window the projection covers.
	GeneratedAt time.Time `json:"generated_at"`
	HorizonDays int       `json:"horizon_days"`

	// PredictedDailyDemand is the mean per-day projection (yhat).
	// HorizonDemand sums yhat over the horizon; HorizonDemandUpper sums the
	// upper prediction band, which is what drives safety stock.
	PredictedDailyDemand float64 `json:"predicted_daily_demand"`
	HorizonDemand        float64 `json:"horizon_demand"`
	HorizonDemandUpper   float64 `json:"horizon_demand_upper"`

	// AvailableAtGeneration snapshots the stock the projection was run
	// against, so a later stock movement never rewrites a historical row —
	// NeedsReorder is that snapshot vs PredictiveReorderPoint at run time.
	AvailableAtGeneration  int      `json:"available_at_generation"`
	DaysUntilStockout      *float64 `json:"days_until_stockout"`
	ProjectedStockoutDate  string   `json:"projected_stockout_date,omitempty"`
	PredictiveReorderPoint int      `json:"predictive_reorder_point"`
	NeedsReorder           bool     `json:"needs_reorder"`
	LeadTimeDays           *int     `json:"lead_time_days"`
	SafetyStock            int      `json:"safety_stock"`

	// Method records which engine produced the projection; ModelVersion is its
	// identifier (e.g. "prophet-1"), "" when the engine doesn't set one.
	Method       string `json:"method"`
	ModelVersion string `json:"model_version,omitempty"`
}

// Forecast method values, mirroring inventory.DemandForecast.Method.
const (
	ForecastMethodProphet     = "prophet"
	ForecastMethodHoltWinters = "holtwinters"
	ForecastMethodFallback    = "fallback"
)

const (
	demandForecastPath = inventoryReportBase + "demand_forecast/"
	reorderAlertsPath  = inventoryReportBase + "reorder_alerts/"
)

// DemandForecast returns the stored ML demand forecast, most-urgent-first.
// Query keys: low_stock_only (truthy → only rows flagged needs_reorder). Both
// report actions return a bare array, so decode through MaybeList.
func (c *Client) DemandForecast(ctx context.Context, q url.Values) ([]DemandForecastRow, error) {
	var out MaybeList[DemandForecastRow]
	if err := c.Get(ctx, demandForecastPath, q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ReorderAlerts returns the predictive reorder-alert notify set: the latest
// forecast row for every opted-in item (reorder_alerts_enabled) that is due to
// reorder. No query params — the filter is entirely server-side.
func (c *Client) ReorderAlerts(ctx context.Context) ([]DemandForecastRow, error) {
	var out MaybeList[DemandForecastRow]
	if err := c.Get(ctx, reorderAlertsPath, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
