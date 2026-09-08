package omsapi

import (
	"context"
	"testing"
)

// TestReorderSupplierPerformance_Decode locks the decode-drift contract for the
// supplier_performance report: total_order_value is a Decimal-as-string, the
// rate fields are float percentages, and days_since_last_order is a nullable
// int. Path must be trailing-slashed.
func TestReorderSupplierPerformance_Decode(t *testing.T) {
	c, path := reportSrv(t, `[
		{"supplier_id":2,"supplier_name":"Acme","total_orders":9,"completed_orders":7,
		 "active_orders":2,"average_lead_time_days":4.5,"on_time_delivery_rate":88.9,
		 "early_delivery_rate":11.1,"late_delivery_rate":11.1,"total_order_value":"4200.75",
		 "damage_rate":2.5,"last_order_date":"2026-06-01T12:00:00Z","days_since_last_order":36},
		{"supplier_id":5,"supplier_name":"NoHistory","total_orders":1,"completed_orders":0,
		 "active_orders":1,"average_lead_time_days":0.0,"on_time_delivery_rate":0.0,
		 "early_delivery_rate":0.0,"late_delivery_rate":0.0,"total_order_value":"0.00",
		 "damage_rate":0.0,"last_order_date":null,"days_since_last_order":null}
	]`)
	rows, err := c.ReorderSupplierPerformance(context.Background())
	if err != nil {
		t.Fatalf("ReorderSupplierPerformance: %v", err)
	}
	if *path != "/api/reorders/analytics/supplier_performance/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	r0 := rows[0]
	if r0.SupplierName != "Acme" || r0.TotalOrders != 9 || r0.CompletedOrders != 7 || r0.ActiveOrders != 2 {
		t.Errorf("row0 order metrics wrong: %+v", r0)
	}
	if r0.AverageLeadTimeDays != 4.5 || r0.OnTimeDeliveryRate != 88.9 || r0.DamageRate != 2.5 {
		t.Errorf("row0 float metrics wrong: %+v", r0)
	}
	// Decimal-as-string, not a float.
	if r0.TotalOrderValue != "4200.75" {
		t.Errorf("total_order_value = %q, want \"4200.75\"", r0.TotalOrderValue)
	}
	if r0.LastOrderDate != "2026-06-01T12:00:00Z" {
		t.Errorf("last_order_date = %q", r0.LastOrderDate)
	}
	if r0.DaysSinceLastOrder == nil || *r0.DaysSinceLastOrder != 36 {
		t.Errorf("days_since_last_order = %v, want 36", r0.DaysSinceLastOrder)
	}
	// null datetime → "", null int → nil pointer.
	if rows[1].LastOrderDate != "" {
		t.Errorf("row1 last_order_date should be empty, got %q", rows[1].LastOrderDate)
	}
	if rows[1].DaysSinceLastOrder != nil {
		t.Errorf("row1 days_since_last_order should be nil, got %v", *rows[1].DaysSinceLastOrder)
	}
}

// TestReorderLeadTimeTrends_Decode covers the monthly lead-time series: all
// numeric fields are JSON numbers, on_time_delivery_rate is a percentage.
func TestReorderLeadTimeTrends_Decode(t *testing.T) {
	c, path := reportSrv(t, `[
		{"month":"2026-05","average_lead_time_days":5.2,"average_variance_days":-0.3,
		 "total_deliveries":12,"on_time_delivery_rate":91.7},
		{"month":"2026-06","average_lead_time_days":6.0,"average_variance_days":1.1,
		 "total_deliveries":8,"on_time_delivery_rate":75.0}
	]`)
	rows, err := c.ReorderLeadTimeTrends(context.Background())
	if err != nil {
		t.Fatalf("ReorderLeadTimeTrends: %v", err)
	}
	if *path != "/api/reorders/analytics/lead_time_trends/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0].Month != "2026-05" || rows[0].AverageLeadTimeDays != 5.2 ||
		rows[0].AverageVarianceDays != -0.3 || rows[0].TotalDeliveries != 12 ||
		rows[0].OnTimeDeliveryRate != 91.7 {
		t.Errorf("row0 decode wrong: %+v", rows[0])
	}
}

// TestReorderTransparency_Decode covers the object envelope: summary + orders +
// purchase_orders, with nullable float costs (null stays distinct from 0).
func TestReorderTransparency_Decode(t *testing.T) {
	c, path := reportSrv(t, `{
		"summary":{"total_orders_with_financial_data":2,"total_amount_spent":150.25,
		 "total_purchase_orders":1,"total_po_amount_spent":300.00,
		 "last_updated":"2026-07-01T00:00:00Z","transparency_note":"open books"},
		"orders":[
		 {"id":11,"item_id":"i-1","item_name":"PLA","item_category":"Filament",
		  "quantity_ordered":3,"status":"ordered","requested_at":"2026-06-01T00:00:00Z",
		  "ordered_at":"2026-06-02T00:00:00Z","delivered_at":null,"estimated_cost":50.0,
		  "actual_cost":null,"cost_per_unit":16.67,"cost_variance":null,"order_number":"ON-1",
		  "invoice_number":"","invoice_url":"","purchase_order_url":"","delivery_tracking_url":"",
		  "supplier_url":"","public_notes":"","supplier_name":"Acme"}
		],
		"ledger":[{"id":11,"item_name":"PLA"}],
		"purchase_orders":[
		 {"id":"1","po_number":"PO-1","supplier_name":"Acme","status":"received",
		  "status_label":"Received","order_date":"2026-05-01T00:00:00Z",
		  "expected_delivery_date":null,"estimated_total":300.0,"actual_total":null,
		  "total_items":2,"total_quantity":5,"is_fully_received":true}
		]
	}`)
	tr, err := c.ReorderTransparency(context.Background())
	if err != nil {
		t.Fatalf("ReorderTransparency: %v", err)
	}
	if *path != "/api/reorders/analytics/transparency/" {
		t.Fatalf("path = %q", *path)
	}
	if tr.Summary.TotalOrdersWithFinancialData != 2 || tr.Summary.TotalAmountSpent != 150.25 ||
		tr.Summary.TotalPurchaseOrders != 1 || tr.Summary.TotalPOAmountSpent != 300.00 {
		t.Errorf("summary decode wrong: %+v", tr.Summary)
	}
	if len(tr.Orders) != 1 {
		t.Fatalf("want 1 order, got %d", len(tr.Orders))
	}
	o := tr.Orders[0]
	if o.ItemName != "PLA" || o.ItemCategory != "Filament" || o.QuantityOrdered != 3 || o.SupplierName != "Acme" {
		t.Errorf("order decode wrong: %+v", o)
	}
	if o.EstimatedCost == nil || *o.EstimatedCost != 50.0 {
		t.Errorf("estimated_cost = %v, want 50.0", o.EstimatedCost)
	}
	// null cost must stay nil, not decode to 0.0.
	if o.ActualCost != nil {
		t.Errorf("actual_cost should be nil, got %v", *o.ActualCost)
	}
	if len(tr.PurchaseOrders) != 1 {
		t.Fatalf("want 1 PO, got %d", len(tr.PurchaseOrders))
	}
	po := tr.PurchaseOrders[0]
	if po.PONumber != "PO-1" || po.StatusLabel != "Received" || !po.IsFullyReceived ||
		po.TotalItems != 2 || po.TotalQuantity != 5 {
		t.Errorf("PO decode wrong: %+v", po)
	}
	if po.EstimatedTotal == nil || *po.EstimatedTotal != 300.0 {
		t.Errorf("estimated_total = %v, want 300.0", po.EstimatedTotal)
	}
	if po.ActualTotal != nil {
		t.Errorf("actual_total should be nil, got %v", *po.ActualTotal)
	}
}

// TestReorderLogisticsDashboard_Decode covers the KPI envelope + QR-scan series.
func TestReorderLogisticsDashboard_Decode(t *testing.T) {
	c, path := reportSrv(t, `{
		"open_item_requests":4,"open_locations_with_problems":2,"urgent_location_problems":1,
		"alert_active":true,"assets_overdue_maintenance":3,"pm_overdue":5,"pm_due_this_week":2,
		"qr_scans_total":42,"qr_scans_by_day":[
		 {"date":"2026-07-01","count":6},{"date":"2026-07-02","count":8}
		],"last_updated":"2026-07-07T00:00:00Z"}`)
	d, err := c.ReorderLogisticsDashboard(context.Background())
	if err != nil {
		t.Fatalf("ReorderLogisticsDashboard: %v", err)
	}
	if *path != "/api/reorders/analytics/logistics_dashboard/" {
		t.Fatalf("path = %q", *path)
	}
	if d.OpenItemRequests != 4 || d.OpenLocationsWithProblems != 2 || d.UrgentLocationProblems != 1 ||
		!d.AlertActive || d.AssetsOverdueMaintenance != 3 || d.PMOverdue != 5 || d.PMDueThisWeek != 2 ||
		d.QRScansTotal != 42 {
		t.Errorf("KPI decode wrong: %+v", d)
	}
	if len(d.QRScansByDay) != 2 || d.QRScansByDay[1].Date != "2026-07-02" || d.QRScansByDay[1].Count != 8 {
		t.Errorf("qr_scans_by_day decode wrong: %+v", d.QRScansByDay)
	}
}

// TestReorderReports_EmptyAndEnvelope confirms bare-empty-array and paginated
// envelope shapes both decode to an empty slice (MaybeList tolerance) without
// error, so the report tables render "no rows" rather than crashing.
func TestReorderReports_EmptyAndEnvelope(t *testing.T) {
	c, _ := reportSrv(t, `[]`)
	sp, err := c.ReorderSupplierPerformance(context.Background())
	if err != nil || len(sp) != 0 {
		t.Fatalf("empty supplier_performance: rows=%d err=%v", len(sp), err)
	}

	c2, _ := reportSrv(t, `{"count":0,"next":null,"previous":null,"results":[]}`)
	lt, err := c2.ReorderLeadTimeTrends(context.Background())
	if err != nil || len(lt) != 0 {
		t.Fatalf("envelope lead_time_trends: rows=%d err=%v", len(lt), err)
	}
}
