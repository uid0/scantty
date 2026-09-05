package omsapi

import "context"

// This file mirrors the OMS reorder_queue AnalyticsViewSet — the four
// reorders/purchasing analytics endpoints the web exposes but that #84's
// Reports work deferred: supplier_performance, lead_time_trends, transparency,
// and logistics_dashboard. They live on a ViewSet registered at
// `router.register(r"analytics", AnalyticsViewSet)` under the reorders app, so
// the DefaultRouter routes each @action at
// /api/reorders/analytics/<method_name>/ (underscores preserved, trailing
// slash — the same slash discipline as the other @action clients).
//
// Decode-drift discipline — confirmed against reorder_queue/views.py +
// serializers.py:
//   - supplier_performance goes through SupplierPerformanceSerializer, whose
//     total_order_value is a DRF DecimalField → Decimal-as-STRING ("0.00"),
//     while the four rate fields + average_lead_time_days are FloatField → JSON
//     number. The rates are ALREADY percentages (the view computes
//     count/total*100), not fractions — do NOT ×100 again (the #84 100× lesson).
//   - lead_time_trends builds plain dicts: every numeric field is a JSON number
//     (rounded floats / ints); on_time_delivery_rate is again already a percent.
//   - transparency + logistics_dashboard build plain dicts where all money is
//     float(...) → JSON float, so nullable money is a *float64 (null stays
//     distinct from 0.0). Nullable datetimes serialize as JSON string|null and
//     decode into "" when absent.
//
// supplier_performance + lead_time_trends require IsAuthenticated; transparency
// + logistics_dashboard are AllowAny (public). Neither of the latter two has a
// dedicated web report page — the web renders them on the standalone
// TransparencyPage / LogisticsDashboard surfaces — so scantty is the first
// place their data appears as report tables.

const reorderAnalyticsBase = "/api/reorders/analytics/"

// ---------------------------------------------------------------------------
// supplier_performance — per-supplier order / delivery / quality metrics
// ---------------------------------------------------------------------------

// ReorderSupplierPerformance is one row of supplier_performance. Rates are
// percentages (0..100). TotalOrderValue is a Decimal-as-string; the rate + lead
// fields are floats. LastOrderDate is an ISO datetime or "" (null);
// DaysSinceLastOrder is a nullable int.
//
// OnTimeDeliveryRate / EarlyDeliveryRate / LateDeliveryRate are all counted off
// variance_days, so all three are measured against the standing quote rather
// than against the dates confirmed on the orders — see LeadTimeYardstick.
// AverageLeadTimeDays is not: it is what the deliveries actually took, with no
// promise in it.
type ReorderSupplierPerformance struct {
	SupplierID          int     `json:"supplier_id"`
	SupplierName        string  `json:"supplier_name"`
	TotalOrders         int     `json:"total_orders"`
	CompletedOrders     int     `json:"completed_orders"`
	ActiveOrders        int     `json:"active_orders"`
	AverageLeadTimeDays float64 `json:"average_lead_time_days"`
	OnTimeDeliveryRate  float64 `json:"on_time_delivery_rate"`
	EarlyDeliveryRate   float64 `json:"early_delivery_rate"`
	LateDeliveryRate    float64 `json:"late_delivery_rate"`
	TotalOrderValue     string  `json:"total_order_value"`
	DamageRate          float64 `json:"damage_rate"`
	LastOrderDate       string  `json:"last_order_date"`
	DaysSinceLastOrder  *int    `json:"days_since_last_order"`
	LeadTimeYardstick
}

// ReorderSupplierPerformance fetches the supplier_performance report (bare
// array, sorted by total order value desc).
func (c *Client) ReorderSupplierPerformance(ctx context.Context) ([]ReorderSupplierPerformance, error) {
	var out MaybeList[ReorderSupplierPerformance]
	if err := c.Get(ctx, reorderAnalyticsBase+"supplier_performance/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ---------------------------------------------------------------------------
// lead_time_trends — monthly lead-time series over the trailing 6 months
// ---------------------------------------------------------------------------

// ReorderLeadTimeTrend is one row of lead_time_trends. Month is "YYYY-MM"; lead
// / variance are rounded-to-0.1 floats; OnTimeDeliveryRate is a percentage.
//
// AverageVarianceDays and OnTimeDeliveryRate are both measured against the
// standing quote — see LeadTimeYardstick. AverageLeadTimeDays is not.
type ReorderLeadTimeTrend struct {
	Month               string  `json:"month"`
	AverageLeadTimeDays float64 `json:"average_lead_time_days"`
	AverageVarianceDays float64 `json:"average_variance_days"`
	TotalDeliveries     int     `json:"total_deliveries"`
	OnTimeDeliveryRate  float64 `json:"on_time_delivery_rate"`
	LeadTimeYardstick
}

// ReorderLeadTimeTrends fetches the lead_time_trends report (bare array,
// chronological).
func (c *Client) ReorderLeadTimeTrends(ctx context.Context) ([]ReorderLeadTimeTrend, error) {
	var out MaybeList[ReorderLeadTimeTrend]
	if err := c.Get(ctx, reorderAnalyticsBase+"lead_time_trends/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ---------------------------------------------------------------------------
// transparency — public financial ledger: summary + orders + purchase orders
// ---------------------------------------------------------------------------

// ReorderTransparency is the transparency envelope. The Ledger sub-array the
// backend also returns is a strict subset of Orders, so it is intentionally not
// modelled here — Orders carries everything the ledger does and more.
type ReorderTransparency struct {
	Summary        ReorderTransparencySummary `json:"summary"`
	Orders         []ReorderTransparencyOrder `json:"orders"`
	PurchaseOrders []ReorderTransparencyPO    `json:"purchase_orders"`
}

// ReorderTransparencySummary is the transparency roll-up. Money is JSON float.
type ReorderTransparencySummary struct {
	TotalOrdersWithFinancialData int     `json:"total_orders_with_financial_data"`
	TotalAmountSpent             float64 `json:"total_amount_spent"`
	TotalPurchaseOrders          int     `json:"total_purchase_orders"`
	TotalPOAmountSpent           float64 `json:"total_po_amount_spent"`
	LastUpdated                  string  `json:"last_updated"`
	TransparencyNote             string  `json:"transparency_note"`
}

// ReorderTransparencyOrder is one reorder request in the public ledger (last
// 100 with financial data). All cost fields are nullable floats.
type ReorderTransparencyOrder struct {
	ID                  int      `json:"id"`
	ItemID              string   `json:"item_id"`
	ItemName            string   `json:"item_name"`
	ItemCategory        string   `json:"item_category"`
	QuantityOrdered     int      `json:"quantity_ordered"`
	Status              string   `json:"status"`
	RequestedAt         string   `json:"requested_at"`
	OrderedAt           string   `json:"ordered_at"`
	DeliveredAt         string   `json:"delivered_at"`
	EstimatedCost       *float64 `json:"estimated_cost"`
	ActualCost          *float64 `json:"actual_cost"`
	CostPerUnit         *float64 `json:"cost_per_unit"`
	CostVariance        *float64 `json:"cost_variance"`
	OrderNumber         string   `json:"order_number"`
	InvoiceNumber       string   `json:"invoice_number"`
	InvoiceURL          string   `json:"invoice_url"`
	PurchaseOrderURL    string   `json:"purchase_order_url"`
	DeliveryTrackingURL string   `json:"delivery_tracking_url"`
	SupplierURL         string   `json:"supplier_url"`
	PublicNotes         string   `json:"public_notes"`
	SupplierName        string   `json:"supplier_name"`
}

// ReorderTransparencyPO is one purchase order in the public ledger (last 50 in
// a sent/received-ish status). Totals are nullable floats.
type ReorderTransparencyPO struct {
	ID                   string   `json:"id"`
	PONumber             string   `json:"po_number"`
	SupplierName         string   `json:"supplier_name"`
	Status               string   `json:"status"`
	StatusLabel          string   `json:"status_label"`
	OrderDate            string   `json:"order_date"`
	ExpectedDeliveryDate string   `json:"expected_delivery_date"`
	EstimatedTotal       *float64 `json:"estimated_total"`
	ActualTotal          *float64 `json:"actual_total"`
	TotalItems           int      `json:"total_items"`
	TotalQuantity        int      `json:"total_quantity"`
	IsFullyReceived      bool     `json:"is_fully_received"`
}

// ReorderTransparency fetches the public transparency envelope.
func (c *Client) ReorderTransparency(ctx context.Context) (*ReorderTransparency, error) {
	var out ReorderTransparency
	if err := c.Get(ctx, reorderAnalyticsBase+"transparency/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------------------
// logistics_dashboard — public TV-dashboard KPI counters + 7-day QR sparkline
// ---------------------------------------------------------------------------

// ReorderLogisticsDashboard is the logistics_dashboard envelope: scalar KPI
// counters plus a 7-day QR-scan series. Every count is a plain int.
type ReorderLogisticsDashboard struct {
	OpenItemRequests          int                      `json:"open_item_requests"`
	OpenLocationsWithProblems int                      `json:"open_locations_with_problems"`
	UrgentLocationProblems    int                      `json:"urgent_location_problems"`
	AlertActive               bool                     `json:"alert_active"`
	AssetsOverdueMaintenance  int                      `json:"assets_overdue_maintenance"`
	PMOverdue                 int                      `json:"pm_overdue"`
	PMDueThisWeek             int                      `json:"pm_due_this_week"`
	QRScansTotal              int                      `json:"qr_scans_total"`
	QRScansByDay              []ReorderLogisticsQRScan `json:"qr_scans_by_day"`
	LastUpdated               string                   `json:"last_updated"`
}

// ReorderLogisticsQRScan is one day of the QR-scan sparkline (unique
// assets+items scanned that day).
type ReorderLogisticsQRScan struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// ReorderLogisticsDashboard fetches the public logistics dashboard envelope.
func (c *Client) ReorderLogisticsDashboard(ctx context.Context) (*ReorderLogisticsDashboard, error) {
	var out ReorderLogisticsDashboard
	if err := c.Get(ctx, reorderAnalyticsBase+"logistics_dashboard/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
