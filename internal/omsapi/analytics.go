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

// AnalyticsTopUser is one row in the top_users projection. The aggregation
// (analytics/services/aggregation.py) emits `display_name` and
// `completed_wo_count`; the earlier `full_name`/`wo_count` tags never matched,
// so the dashboard's top-user rows rendered blank names and zero counts.
type AnalyticsTopUser struct {
	UserID      int    `json:"user_id"`
	Username    string `json:"username"`
	FullName    string `json:"display_name,omitempty"`
	WOCount     int    `json:"completed_wo_count"`
	HoursLogged string `json:"hours_logged,omitempty"`
}

// AnalyticsBucket is one entry in the wo_volume time-series. The aggregation
// emits `period` + `count`, not `bucket_start`/`wo_count`.
type AnalyticsBucket struct {
	BucketStart string `json:"period"`
	BucketEnd   string `json:"bucket_end"`
	WOCount     int    `json:"count"`
}

// AnalyticsUtilizationRow is one row of the pulse `utilization` projection
// (aggregation.py utilization()) — equipment "touched" in the window, sorted
// most-active first. hours_used is an INT on the wire (not a float). category
// and status are plain strings; a null category decodes to "".
type AnalyticsUtilizationRow struct {
	AssetID          string `json:"asset_id"`
	AssetName        string `json:"asset_name"`
	Category         string `json:"category,omitempty"`
	HoursUsed        int    `json:"hours_used"`
	CompletedWOCount int    `json:"completed_wo_count"`
	Status           string `json:"status,omitempty"`
}

// AnalyticsCategorySpend is one row of the pulse `category_spend` projection
// (aggregation.py category_spend()), sorted by external_actual desc. The three
// money fields are Decimal-as-string ("0.00"); category_id is null for the
// uncategorized bucket so it's a pointer.
type AnalyticsCategorySpend struct {
	CategoryID        *int   `json:"category_id"`
	CategoryName      string `json:"category_name,omitempty"`
	InternalEstimated string `json:"internal_estimated"`
	ExternalEstimated string `json:"external_estimated"`
	ExternalActual    string `json:"external_actual"`
	InternalWOCount   int    `json:"internal_wo_count"`
	ExternalWOCount   int    `json:"external_wo_count"`
}

// AnalyticsMaintenanceForecast is one row of the pulse `maintenance_forecast`
// projection (services/forecast.py). hours_used is an INT; the interval /
// days-since fields are null when the schedule is undefined (so pointers);
// last_completed_wo_at is an RFC3339 datetime string ("" when null). due_reason
// is one of "hours" | "days" | "both".
type AnalyticsMaintenanceForecast struct {
	AssetID           string `json:"asset_id"`
	AssetName         string `json:"asset_name"`
	Category          string `json:"category,omitempty"`
	Status            string `json:"status,omitempty"`
	HoursUsed         int    `json:"hours_used"`
	LastCompletedWOAt string `json:"last_completed_wo_at,omitempty"`
	IntervalHours     *int   `json:"interval_hours"`
	IntervalDays      *int   `json:"interval_days"`
	DaysSinceLastWO   *int   `json:"days_since_last_wo"`
	DueReason         string `json:"due_reason"`
}

// AnalyticsPulse is the full /pulse/ envelope. Every aggregation is now typed
// against the real aggregation.py / forecast.py response shapes so the Reports
// screen can render each as a table. monthly_budget is Decimal-as-string
// (str(Decimal)) or JSON null → "".
type AnalyticsPulse struct {
	Summary             AnalyticsValueSummary          `json:"summary"`
	WOVolumeTrend       []AnalyticsBucket              `json:"wo_volume_trend"`
	TopUsers            []AnalyticsTopUser             `json:"top_users"`
	Utilization         []AnalyticsUtilizationRow      `json:"utilization"`
	CategorySpend       []AnalyticsCategorySpend       `json:"category_spend"`
	MaintenanceForecast []AnalyticsMaintenanceForecast `json:"maintenance_forecast"`
	MonthlyBudget       string                         `json:"monthly_budget"`
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
