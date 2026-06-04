package omsapi

import (
	"context"
	"net/url"
)

// AnalyticsValueSummary mirrors the value_summary aggregation — internal
// vs external work-order spend over the chosen window. Money fields are
// strings ("0.00") on the wire because Django serializes Decimal that
// way; consumers parse with strconv.ParseFloat / shopspring decimal as
// needed.
type AnalyticsValueSummary struct {
	PeriodStart                   string `json:"period_start"`
	PeriodEnd                     string `json:"period_end"`
	InternalCompletedCount        int    `json:"internal_completed_count"`
	InternalEstimatedExternalCost string `json:"internal_estimated_external_cost"`
	InternalEstimatedInternalCost string `json:"internal_estimated_internal_cost"`
	InternalNetValue              string `json:"internal_net_value"`
	ExternalClosedCount           int    `json:"external_closed_count"`
	ExternalActualCost            string `json:"external_actual_cost"`
	ExternalEstimatedCost         string `json:"external_estimated_cost"`
	TotalValueToMakerspace        string `json:"total_value_to_makerspace"`
}

// AnalyticsTopUser is one row in the top_users projection.
type AnalyticsTopUser struct {
	UserID      int    `json:"user_id"`
	Username    string `json:"username"`
	FullName    string `json:"full_name,omitempty"`
	WOCount     int    `json:"wo_count"`
	HoursLogged string `json:"hours_logged,omitempty"`
}

// AnalyticsBucket is one entry in the wo_volume time-series.
type AnalyticsBucket struct {
	BucketStart string `json:"bucket_start"`
	BucketEnd   string `json:"bucket_end"`
	WOCount     int    `json:"wo_count"`
}

// AnalyticsPulse is the full /pulse/ envelope. Most slice fields are
// untyped any/map for v1 — the rich aggregations (utilization,
// category_spend, maintenance_forecast) are charting-heavy and only
// useful when a UI exists to render them. As features land, swap the
// `any` slices for typed structs.
type AnalyticsPulse struct {
	Summary             AnalyticsValueSummary `json:"summary"`
	WOVolumeTrend       []AnalyticsBucket     `json:"wo_volume_trend"`
	TopUsers            []AnalyticsTopUser    `json:"top_users"`
	Utilization         []map[string]any      `json:"utilization"`
	CategorySpend       []map[string]any      `json:"category_spend"`
	MaintenanceForecast []map[string]any      `json:"maintenance_forecast"`
	MonthlyBudget       string                `json:"monthly_budget"`
}

// GetAnalyticsPulse fetches the pulse aggregate. Optional `start` +
// `end` (YYYY-MM-DD) override the default last-full-month window; both
// must be supplied together or the backend 400s. `bucket` is "month"
// (default) or "quarter" and controls the wo_volume_trend granularity.
//
// Requires the IsAnalyticsViewer permission — non-admin users get 403,
// which surfaces through the standard APIError envelope.
func (c *Client) GetAnalyticsPulse(ctx context.Context, start, end, bucket string) (*AnalyticsPulse, error) {
	q := url.Values{}
	if start != "" {
		q.Set("start", start)
	}
	if end != "" {
		q.Set("end", end)
	}
	if bucket != "" {
		q.Set("bucket", bucket)
	}
	var out AnalyticsPulse
	if err := c.Get(ctx, "/api/analytics/pulse/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
