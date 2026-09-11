package omsapi

import (
	"context"
	"encoding/json"
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

// The transparency decode tests are built from RECORDED responses
// (testdata/transparency_*.json, provenance in testdata/README.md). The
// hand-written envelope they replace carried `supplier_name`, `estimated_cost`
// and `cost_variance` on each order — the keys the struct declared, which OMS
// #1057 withdrew because they were the ITEM's, published under the order's name
// — so it could only ever confirm the struct.

// transparencyRawOrders is a recording's orders[] as the server sent them.
func transparencyRawOrders(t *testing.T, name string) []map[string]any {
	t.Helper()
	var raw struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := json.Unmarshal(wireBody(t, name), &raw); err != nil {
		t.Fatalf("%s is not JSON: %v", name, err)
	}
	if len(raw.Orders) == 0 {
		t.Fatalf("%s has no orders, so every check over it is vacuous", name)
	}
	return raw.Orders
}

// TestReorderTransparency_DecodesTheRecordedFeed: served the bytes OMS main
// really sends, each order's actual_cost keeps its three states apart — a
// figure (a recorded 0.00 among them), nil for null, and nil-with-the-marker
// for a reader the vendor block was withheld from.
func TestReorderTransparency_DecodesTheRecordedFeed(t *testing.T) {
	signed, err := serveWire(t, wireBody(t, "transparency_signed_in.json")).ReorderTransparency(context.Background())
	if err != nil {
		t.Fatalf("a recorded signed-in reply did not decode: %v", err)
	}
	raw := transparencyRawOrders(t, "transparency_signed_in.json")
	if len(signed.Orders) != len(raw) {
		t.Fatalf("orders = %d, want %d", len(signed.Orders), len(raw))
	}
	for i, o := range signed.Orders {
		if o.VendorDataWithheld {
			t.Errorf("order %d: marked withheld on the signed-in reply", i)
		}
		switch v := raw[i]["actual_cost"].(type) {
		case nil:
			if o.ActualCost != nil {
				t.Errorf("order %d: null actual_cost decoded to %v", i, *o.ActualCost)
			}
		case float64:
			if o.ActualCost == nil || *o.ActualCost != v {
				t.Errorf("order %d: actual_cost %v decoded to %v", i, v, o.ActualCost)
			}
		}
	}
	if len(signed.PurchaseOrders) == 0 || signed.PurchaseOrders[0].VendorDataWithheld ||
		signed.PurchaseOrders[0].SupplierName == "" {
		t.Errorf("signed-in PO decode wrong: %+v", signed.PurchaseOrders)
	}

	anon, err := serveWire(t, wireBody(t, "transparency_anonymous.json")).ReorderTransparency(context.Background())
	if err != nil {
		t.Fatalf("a recorded anonymous reply did not decode: %v", err)
	}
	for i, o := range anon.Orders {
		if !o.VendorDataWithheld || o.ActualCost != nil {
			t.Errorf("anonymous order %d: withheld=%v actual=%v — the marker is the only thing "+
				"telling an omitted cost from a null one", i, o.VendorDataWithheld, o.ActualCost)
		}
	}
	for i, p := range anon.PurchaseOrders {
		if !p.VendorDataWithheld || p.SupplierName != "" || p.EstimatedTotal != nil || p.ActualTotal != nil {
			t.Errorf("anonymous PO %d decode wrong: %+v", i, p)
		}
	}
}

// The recordings still carry the SERVER's shape, so the tests above and the
// screen tests in internal/tui are not passing because a fixture drifted to the
// one the code assumes. Each clause names the check that goes vacuous without
// it.
func TestTransparencyFixtures_CarryTheServersOwnShape(t *testing.T) {
	withdrawn := []string{"supplier_name", "estimated_cost", "cost_variance"}
	signed := transparencyRawOrders(t, "transparency_signed_in.json")
	zero, null := false, false
	for i, o := range signed {
		for _, k := range withdrawn {
			if _, ok := o[k]; ok {
				t.Errorf("signed-in order %d carries %q, which OMS #1057 withdrew — the fixture "+
					"was re-shaped toward the old struct", i, k)
			}
		}
		if _, ok := o["item_supplier_choice"].(map[string]any); !ok {
			t.Errorf("signed-in order %d has no item_supplier_choice object", i)
		}
		v, ok := o["actual_cost"]
		if !ok {
			t.Fatalf("signed-in order %d omits actual_cost; a signed-in reply carries it", i)
		}
		zero = zero || v == 0.0
		null = null || v == nil
	}
	if !zero || !null {
		t.Errorf("the signed-in recording must hold a recorded 0.0 and a null actual_cost "+
			"(zero=%v null=%v), or the three-state checks cannot tell them apart", zero, null)
	}
	for i, o := range transparencyRawOrders(t, "transparency_anonymous.json") {
		if o["vendor_data_withheld"] != true {
			t.Errorf("anonymous order %d is not marked vendor_data_withheld", i)
		}
		if _, ok := o["actual_cost"]; ok {
			t.Errorf("anonymous order %d carries actual_cost; OMS OMITS withheld keys", i)
		}
	}
	var anon struct {
		Summary map[string]any   `json:"summary"`
		POs     []map[string]any `json:"purchase_orders"`
	}
	if err := json.Unmarshal(wireBody(t, "transparency_anonymous.json"), &anon); err != nil {
		t.Fatalf("anonymous recording: %v", err)
	}
	if anon.Summary["vendor_data_withheld"] != true || len(anon.POs) == 0 {
		t.Error("the anonymous recording must mark its summary and carry a purchase order")
	}
	for i, p := range anon.POs {
		_, named := p["supplier_name"]
		if p["vendor_data_withheld"] != true || named {
			t.Errorf("anonymous PO %d is not a withheld row: %v", i, p)
		}
	}
	// The pre-#1057 recording is the one the substituted-supplier screen check
	// is asked of, so it must still carry what that check looks for.
	legacy := transparencyRawOrders(t, "transparency_pre1057_signed_in.json")
	carried := 0
	for _, o := range legacy {
		if n, _ := o["supplier_name"].(string); n != "" {
			if _, ok := o["estimated_cost"].(float64); ok {
				carried++
			}
		}
	}
	if carried == 0 {
		t.Error("the pre-#1057 recording has no row carrying both a supplier_name and an " +
			"estimated_cost, so the check that keeps them off the screen could not fail")
	}
	// Same rows, two servers: the donation #1057 publishes as a recorded 0.0 is
	// the one its parent published as null, which is the README's account of
	// why the recorded zero is a state of its own.
	byID := map[float64]map[string]any{}
	for _, o := range legacy {
		byID[o["id"].(float64)] = o
	}
	for _, o := range signed {
		if o["actual_cost"] == 0.0 {
			if old, ok := byID[o["id"].(float64)]; !ok || old["actual_cost"] != nil {
				t.Errorf("order %v: recorded 0.0 now, and the pre-#1057 recording has %v for it, "+
					"not the null the README describes", o["id"], old["actual_cost"])
			}
		}
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
