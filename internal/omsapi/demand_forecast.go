package omsapi

import (
	"context"
	"net/url"
	"time"
)

// Demand forecast (op-1 API) — the non-serialized sibling of the serialized
// component forecast in serialized.go. Two read-only report actions on the
// inventory report viewset return the SAME row payload (a stored
// inventory.DemandForecast plus the item's name/sku/category_name):
//
//   - demand_forecast/ — the latest stored row per active, non-retired,
//     non-serialized item that has one, most-urgent-first (due-to-reorder
//     first, then soonest due, nulls last). ?low_stock_only=1 narrows it to
//     the needs_reorder set.
//   - reorder_alerts/  — the notify set: the same rows filtered to items whose
//     owner opted in (reorder_alerts_enabled) AND that are due to reorder.
//     Takes no query params.
//
// Both read STORED rows only: they return an empty list until the nightly
// forecasting task has populated the table. That empty state is part of the
// contract, not an error — the screens say so rather than showing "no data".
//
// v2 of the engine swapped the model, not the endpoints: it measures the
// restock INTERVAL (how often an item actually gets bought) instead of a usage
// RATE, because on real data almost nothing has usage logged while every
// restock goes through a purchase order. So the live signal is a cadence and a
// due date. The v1 quantity fields are still emitted — 0/null on v2 rows, real
// only on pre-v2 history — so they stay on the struct.

// DemandForecastRow is one row of either report. Every nullable backend field
// is a pointer so "unknown" stays distinct from zero: that is the difference
// between "no cadence could be measured" and "a cadence of 0 days", and
// between "a v1 row that really projected 0/day" and "a v2 row that doesn't
// project quantities at all". Dates arrive as bare YYYY-MM-DD strings.
type DemandForecastRow struct {
	// ID is the stored forecast row's pk (BigAutoField → number); Item is the
	// forecast item's UUID, matching Item.ID.
	ID           int64  `json:"id"`
	Item         string `json:"item"`
	ItemName     string `json:"item_name"`
	SKU          string `json:"sku,omitempty"`
	CategoryName string `json:"category_name,omitempty"`

	// GeneratedAt stamps the forecast run this row came from.
	GeneratedAt time.Time `json:"generated_at"`

	// Restock-interval signal (v2). AvgIntervalDays is the mean gap between
	// purchase events — the cadence — and IntervalSamples is how many gaps it
	// was averaged over (purchase events minus one), so 0 means no cadence
	// could be derived. PredictedNextReorderDate is LastRestockDate plus that
	// cadence, and DaysUntilDue counts from generation to it: negative when
	// the item is already overdue. All of them except IntervalSamples are null
	// on an insufficient-history row.
	AvgIntervalDays          *float64 `json:"avg_interval_days"`
	IntervalSamples          int      `json:"interval_samples"`
	LastRestockDate          *string  `json:"last_restock_date"`
	PredictedNextReorderDate *string  `json:"predicted_next_reorder_date"`
	DaysUntilDue             *float64 `json:"days_until_due"`

	// Retired v1 usage-rate projection, kept so pre-v2 history stays readable.
	// A v2 row carries 0/null across all of these — only a row written by a v1
	// engine (see ForecastMethodProphet and friends) holds real numbers.
	HorizonDays            *int     `json:"horizon_days"`
	PredictedDailyDemand   *float64 `json:"predicted_daily_demand"`
	HorizonDemand          *float64 `json:"horizon_demand"`
	HorizonDemandUpper     *float64 `json:"horizon_demand_upper"`
	DaysUntilStockout      *float64 `json:"days_until_stockout"`
	ProjectedStockoutDate  *string  `json:"projected_stockout_date"`
	PredictiveReorderPoint *int     `json:"predictive_reorder_point"`
	SafetyStock            *int     `json:"safety_stock"`

	// Reorder decision. AvailableAtGeneration snapshots the stock the run saw,
	// which under the interval model is informational only: NeedsReorder is
	// set when DaysUntilDue falls inside LeadTimeDays — order now and it lands
	// about when it is due. LeadTimeDays is null when the item has no measured
	// or estimated lead time.
	AvailableAtGeneration int  `json:"available_at_generation"`
	NeedsReorder          bool `json:"needs_reorder"`
	LeadTimeDays          *int `json:"lead_time_days"`

	// Method records which engine produced the projection; ModelVersion is its
	// identifier (e.g. "interval-1"), "" when the engine doesn't set one.
	Method       string `json:"method"`
	ModelVersion string `json:"model_version,omitempty"`
}

// Forecast method values, mirroring inventory.DemandForecast.Method.
const (
	// v2 (restock interval): a cadence was averaged from purchase history, or
	// there were fewer than two purchases to average a gap between — in which
	// case the row flags nothing, because a false alert costs more than a
	// missed one.
	ForecastMethodRestockInterval     = "restock_interval"
	ForecastMethodInsufficientHistory = "insufficient_history"

	// Retired v1 (usage rate) engines — historical rows only.
	ForecastMethodProphet     = "prophet"
	ForecastMethodHoltWinters = "holtwinters"
	ForecastMethodFallback    = "fallback"
)

const (
	demandForecastPath = inventoryReportBase + "demand_forecast/"
	reorderAlertsPath  = inventoryReportBase + "reorder_alerts/"
)

// DemandForecast returns the stored demand forecast, most-urgent-first.
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
