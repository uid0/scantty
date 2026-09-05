package omsapi

import (
	"context"
	"net/url"
)

// This file mirrors the OMS web "Reports" section — the three report pages
// (Inventory, Purchasing, Asset), each a set of tabular @action endpoints on a
// ReportViewSet. Every endpoint returns a BARE JSON ARRAY (Response(list), no
// pagination envelope) so we decode through MaybeList to tolerate either shape.
//
// Decode-drift discipline — money type differs by app, confirmed against the
// backend view code:
//   - inventory reports  (inventory/views.py InventoryReportViewSet): total_value
//     is built with float(...) → JSON number → float64.
//   - purchasing reports (reorder_queue/views.py PurchasingReportViewSet): all
//     spend/cost fields are float(...) → float64.
//   - asset TCO          (inventory/serializers.py AssetTcoReportSerializer): cost
//     fields are DRF DecimalField → Decimal-as-string → string.
// Nullable ints (category_id, location_id, the maintenance-due intervals) are
// pointers so null stays distinct from 0.
//
// The date-windowed reports (reorder_frequency, lead_time_analysis, price_trends,
// asset utilization) accept optional start_date/end_date or a months/days count
// and otherwise default server-side to the SAME window the web uses by default
// (12mo / 6mo / 30d). We call them param-free so the data matches the web's
// default view; a future bead can add an interactive range picker.

const (
	inventoryReportBase  = "/api/inventory/reports/inventory/"
	assetReportBase      = "/api/inventory/reports/assets/"
	purchasingReportBase = "/api/reorders/reports/purchasing/"
)

// ---------------------------------------------------------------------------
// Inventory report (route /reports/inventory) — 3 tabs
// ---------------------------------------------------------------------------

// InventoryStockByCategory is one row of stock_by_category. total_value is a
// JSON float (float(...) in the view). category_id is null for uncategorized.
type InventoryStockByCategory struct {
	CategoryID    *int    `json:"category_id"`
	CategoryName  string  `json:"category_name"`
	TotalItems    int     `json:"total_items"`
	TotalStock    int     `json:"total_stock"`
	TotalValue    float64 `json:"total_value"`
	LowStockCount int     `json:"low_stock_count"`
}

// InventoryReorderFrequency is one row of reorder_frequency — how often an item
// has been reordered in the window (default trailing 12 months).
type InventoryReorderFrequency struct {
	ItemID       string `json:"item_id"`
	ItemName     string `json:"item_name"`
	ItemSKU      string `json:"item_sku"`
	CategoryName string `json:"category_name"`
	ReorderCount int    `json:"reorder_count"`
}

// InventoryValueByLocation is one row of value_by_location.
type InventoryValueByLocation struct {
	LocationID   *int    `json:"location_id"`
	LocationName string  `json:"location_name"`
	TotalItems   int     `json:"total_items"`
	TotalStock   int     `json:"total_stock"`
	TotalValue   float64 `json:"total_value"`
}

func (c *Client) InventoryStockByCategory(ctx context.Context) ([]InventoryStockByCategory, error) {
	var out MaybeList[InventoryStockByCategory]
	if err := c.Get(ctx, inventoryReportBase+"stock_by_category/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) InventoryReorderFrequency(ctx context.Context) ([]InventoryReorderFrequency, error) {
	var out MaybeList[InventoryReorderFrequency]
	if err := c.Get(ctx, inventoryReportBase+"reorder_frequency/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) InventoryValueByLocation(ctx context.Context) ([]InventoryValueByLocation, error) {
	var out MaybeList[InventoryValueByLocation]
	if err := c.Get(ctx, inventoryReportBase+"value_by_location/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ---------------------------------------------------------------------------
// Purchasing report (route /reports/purchasing) — 4 tabs
// ---------------------------------------------------------------------------

// PurchasingSpendBySupplier is one row of spend_by_supplier. Money is float.
type PurchasingSpendBySupplier struct {
	SupplierID    int     `json:"supplier_id"`
	SupplierName  string  `json:"supplier_name"`
	TotalOrders   int     `json:"total_orders"`
	TotalSpend    float64 `json:"total_spend"`
	AvgOrderValue float64 `json:"avg_order_value"`
}

// PurchasingSpendByCategory is one row of spend_by_category.
type PurchasingSpendByCategory struct {
	CategoryID    *int    `json:"category_id"`
	CategoryName  string  `json:"category_name"`
	TotalItems    int     `json:"total_items"`
	TotalQuantity int     `json:"total_quantity"`
	TotalSpend    float64 `json:"total_spend"`
}

// PurchasingLeadTime is one row of lead_time_analysis (default trailing 6mo).
//
// on_time_rate is a PERCENTAGE 0..100, not a fraction: the view computes
// `on_time_count / total * 100` (reorder_queue/views.py) exactly as the other
// two lead-time payloads do. It used to be documented here as "a fraction
// 0..1", which the render at internal/tui/report_table.go has never believed —
// it appends "%" without scaling — so the comment alone was false and its only
// effect was to invite a reader to "correct" a render that is right and put
// 7500% on the screen. TestPurchasingLeadTime_OnTimeRateNotDoubled is what
// holds that render; this comment now agrees with it.
//
// avg_variance and on_time_rate are both measured against the supplier link's
// standing quoted lead time — see LeadTimeYardstick, whose
// variance_measured_against says so on the wire.
type PurchasingLeadTime struct {
	SupplierID           int     `json:"supplier_id"`
	SupplierName         string  `json:"supplier_name"`
	ItemName             string  `json:"item_name"`
	TotalOrders          int     `json:"total_orders"`
	AvgEstimatedLeadTime float64 `json:"avg_estimated_lead_time"`
	AvgActualLeadTime    float64 `json:"avg_actual_lead_time"`
	AvgVariance          float64 `json:"avg_variance"`
	OnTimeRate           float64 `json:"on_time_rate"`
	LeadTimeYardstick
}

// PurchasingPriceTrend is one row of price_trends (default trailing 12mo).
// price_change_percentage is null when there's a single price point.
type PurchasingPriceTrend struct {
	ItemID                string   `json:"item_id"`
	ItemName              string   `json:"item_name"`
	SupplierName          string   `json:"supplier_name"`
	PriceChanges          int      `json:"price_changes"`
	MinUnitCost           float64  `json:"min_unit_cost"`
	MaxUnitCost           float64  `json:"max_unit_cost"`
	LatestUnitCost        float64  `json:"latest_unit_cost"`
	PriceChangePercentage *float64 `json:"price_change_percentage"`
}

func (c *Client) PurchasingSpendBySupplier(ctx context.Context) ([]PurchasingSpendBySupplier, error) {
	var out MaybeList[PurchasingSpendBySupplier]
	if err := c.Get(ctx, purchasingReportBase+"spend_by_supplier/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) PurchasingSpendByCategory(ctx context.Context) ([]PurchasingSpendByCategory, error) {
	var out MaybeList[PurchasingSpendByCategory]
	if err := c.Get(ctx, purchasingReportBase+"spend_by_category/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) PurchasingLeadTimeAnalysis(ctx context.Context) ([]PurchasingLeadTime, error) {
	var out MaybeList[PurchasingLeadTime]
	if err := c.Get(ctx, purchasingReportBase+"lead_time_analysis/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) PurchasingPriceTrends(ctx context.Context) ([]PurchasingPriceTrend, error) {
	var out MaybeList[PurchasingPriceTrend]
	if err := c.Get(ctx, purchasingReportBase+"price_trends/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ---------------------------------------------------------------------------
// Asset report (route /reports/assets) — 4 tabs
// ---------------------------------------------------------------------------

// AssetByStatus is one row of assets_by_status (a count per lifecycle status).
type AssetByStatus struct {
	Status        string `json:"status"`
	StatusDisplay string `json:"status_display"`
	Count         int    `json:"count"`
}

// AssetMaintenanceDue is one row of maintenance_due. Part rows carry the part
// identity + interval math; "in_maintenance" rows null the part/interval fields
// and set status, so the nullable ints are pointers.
type AssetMaintenanceDue struct {
	AssetID                 string `json:"asset_id"`
	AssetName               string `json:"asset_name"`
	AssetTag                string `json:"asset_tag"`
	PartID                  string `json:"part_id,omitempty"`
	PartName                string `json:"part_name,omitempty"`
	PartSKU                 string `json:"part_sku,omitempty"`
	MaintenanceIntervalDays *int   `json:"maintenance_interval_days"`
	DaysSinceReplacement    *int   `json:"days_since_replacement"`
	DaysOverdue             *int   `json:"days_overdue"`
	LastReplacedAt          string `json:"last_replaced_at,omitempty"`
	Status                  string `json:"status,omitempty"`
}

// AssetUtilizationRow is one row of the asset utilization report — ForgeKey
// DeviceUsage session hours (distinct from the analytics utilization proxy).
// Hours are floats. Default window trailing 30 days.
type AssetUtilizationRow struct {
	AssetID            string  `json:"asset_id"`
	AssetName          string  `json:"asset_name"`
	AssetTag           string  `json:"asset_tag"`
	TotalSessions      int     `json:"total_sessions"`
	TotalHours         float64 `json:"total_hours"`
	AvgHoursPerSession float64 `json:"avg_hours_per_session"`
}

// AssetTCO is one row of the tco report. Cost fields are DRF DecimalField →
// Decimal-as-string. Fixed trailing-90-day window (no params). Rows are sorted
// by total_maintenance_cost_90d desc.
type AssetTCO struct {
	AssetID                    string `json:"asset_id"`
	AssetName                  string `json:"asset_name"`
	AssetTag                   string `json:"asset_tag"`
	MaintenanceDaysLast90      int    `json:"maintenance_days_last_90"`
	ScheduledMaintenanceCost   string `json:"scheduled_maintenance_cost"`
	UnscheduledMaintenanceCost string `json:"unscheduled_maintenance_cost"`
	RepairCost                 string `json:"repair_cost"`
	TCO                        string `json:"tco"`
	PreventiveMaintenanceCost  string `json:"preventive_maintenance_cost"`
	VendorMaintenanceCost      string `json:"vendor_maintenance_cost"`
	TotalMaintenanceCost90d    string `json:"total_maintenance_cost_90d"`
}

func (c *Client) AssetsByStatus(ctx context.Context) ([]AssetByStatus, error) {
	var out MaybeList[AssetByStatus]
	if err := c.Get(ctx, assetReportBase+"assets_by_status/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) AssetMaintenanceDue(ctx context.Context) ([]AssetMaintenanceDue, error) {
	var out MaybeList[AssetMaintenanceDue]
	if err := c.Get(ctx, assetReportBase+"maintenance_due/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) AssetUtilization(ctx context.Context) ([]AssetUtilizationRow, error) {
	var out MaybeList[AssetUtilizationRow]
	if err := c.Get(ctx, assetReportBase+"utilization/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) AssetTCO(ctx context.Context) ([]AssetTCO, error) {
	var out MaybeList[AssetTCO]
	if err := c.Get(ctx, assetReportBase+"tco/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// AssetSuppliesUsedRow is one row of the supplies_used report — a flat,
// per-asset-labeled log that merges two sources over a date window (server
// default: trailing 30 days). Every row carries asset_id/asset_name/source/
// item_name/used_at; the rest are source-specific and ABSENT (→ "") on the
// other source:
//   - source == "serialized": a serial-numbered unit installed on / consumed
//     by the asset (ComponentUsageEvent). Carries serial_number, action,
//     action_display, actor.
//   - source == "consumable": a bulk material used closing a PM work order
//     (WorkOrderMaterialUsage). Carries quantity, unit, work_order_id,
//     estimated_cost.
//
// quantity and estimated_cost are DRF Decimal-as-string; estimated_cost is
// null when the material was deleted after the work order (decodes to "").
// actor is null for a system/unattributed serialized event (decodes to "").
// used_at is an ISO-8601 datetime.
type AssetSuppliesUsedRow struct {
	AssetID       string `json:"asset_id"`
	AssetName     string `json:"asset_name"`
	Source        string `json:"source"`
	ItemName      string `json:"item_name"`
	SerialNumber  string `json:"serial_number"`
	Action        string `json:"action"`
	ActionDisplay string `json:"action_display"`
	Actor         string `json:"actor"`
	Quantity      string `json:"quantity"`
	Unit          string `json:"unit"`
	WorkOrderID   string `json:"work_order_id"`
	EstimatedCost string `json:"estimated_cost"`
	UsedAt        string `json:"used_at"`
}

// AssetSuppliesUsed fetches the supplies_used report. q may carry start_date /
// end_date (YYYY-MM-DD); pass nil to take the server default window (trailing
// 30 days) — the same param-free default the other windowed report tabs use.
func (c *Client) AssetSuppliesUsed(ctx context.Context, q url.Values) ([]AssetSuppliesUsedRow, error) {
	var out MaybeList[AssetSuppliesUsedRow]
	if err := c.Get(ctx, assetReportBase+"supplies_used/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
