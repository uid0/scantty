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
// place their data appears as report tables. "Public" is not "the same for
// everyone" on transparency: see ReorderTransparencyOrder.VendorDataWithheld.

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
//
// NOTHING HERE IS THE ORDER'S UNLESS THE ORDER RECORDS IT, and three keys this
// struct used to carry did not pass that test. A ReorderRequest has an item, a
// quantity, a timeline, actual_cost and paperwork strings — and NO supplier
// relationship and NO recorded estimate. Before OMS #1057 the feed filled that
// gap from the ITEM at request time: `supplier_name` was the item's current
// best supplier (flag a new primary today and last year's delivered order
// named a vendor it could not have bought from), `estimated_cost` was a live
// quote at that supplier's current price, and `cost_variance` was actual_cost
// minus that quote. #1057 withdrew all three and publishes the item's answers
// under item-scoped names instead — `item_supplier_choice` and
// `item_estimated_cost_today` — to a signed-in reader.
//
// NEITHER REPLACEMENT IS DECODED, on purpose. They are true about the ITEM as
// of this response, and the one place this struct is drawn is a row per ORDER,
// where a supplier name beside a paid figure reads as who was paid; the
// "Trans. orders" tab (internal/tui/reorder_reports.go) records the decision
// and the reason. The withdrawn keys are not decoded either, which is what
// keeps an OMS that has not taken #1057 — and still sends the substituted
// values — from getting them onto a screen.
//
// ActualCost has THREE states and they are different facts: a figure (a
// recorded 0.00 among them — a donation — which OMS publishes as 0.0 and not
// null since #1057), nil because OMS sent null ("no figure recorded"), and nil
// because the key was OMITTED for a reader not shown vendor data. Only
// VendorDataWithheld tells the last two apart.
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
	ActualCost          *float64 `json:"actual_cost"`
	CostPerUnit         *float64 `json:"cost_per_unit"`
	OrderNumber         string   `json:"order_number"`
	InvoiceNumber       string   `json:"invoice_number"`
	InvoiceURL          string   `json:"invoice_url"`
	PurchaseOrderURL    string   `json:"purchase_order_url"`
	DeliveryTrackingURL string   `json:"delivery_tracking_url"`
	SupplierURL         string   `json:"supplier_url"`
	PublicNotes         string   `json:"public_notes"`
	// VendorDataWithheld is OMS's `vendor_data_withheld` marker
	// (inventory.services.vendor_visibility.VENDOR_WITHHELD_KEY): true when the
	// server OMITTED this row's vendor keys — actual_cost and cost_per_unit
	// among them — because the caller may not see vendor data. The keys are
	// omitted rather than nulled precisely because null already means "no
	// figure recorded" in this payload, so this flag is the only way to tell
	// POLICY from ABSENCE. OMS decides it with `may_see_vendor_data`, which is
	// `is_authenticated` today, so a signed-in ScanTTY is not sent it by OMS
	// main — it is read because the server states it, not because this client
	// predicts when it will.
	VendorDataWithheld bool `json:"vendor_data_withheld"`
}

// ReorderTransparencyPO is one purchase order in the public ledger (last 50 in
// a sent/received-ish status). Totals are nullable floats.
//
// Unlike an order row, a PurchaseOrder HAS a supplier FK and records its own
// totals, so SupplierName and both totals are the order's own facts — which is
// why #1057 left them alone. They are the three keys withheld from a reader
// not shown vendor data (PO_VENDOR_KEYS), and VendorDataWithheld is what says
// an empty SupplierName or a nil total is withheld rather than absent.
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
	VendorDataWithheld   bool     `json:"vendor_data_withheld"`
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
