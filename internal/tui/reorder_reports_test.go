package tui

import (
	"context"
	"strings"
	"testing"
)

// TestReorderAnalyticsReport_Tabs locks the tab set + labels so the six
// reorders/purchasing surfaces stay wired in order.
func TestReorderAnalyticsReport_Tabs(t *testing.T) {
	s := NewReorderAnalyticsReportScreen(Deps{})
	if s.Title() != "Reorders analytics" {
		t.Errorf("title = %q", s.Title())
	}
	want := []string{"Supplier perf", "Lead-time trends", "Transparency", "Trans. orders", "Trans. POs", "Logistics"}
	if len(s.tabs) != len(want) {
		t.Fatalf("want %d tabs, got %d", len(want), len(s.tabs))
	}
	for i, w := range want {
		if s.tabs[i].label != w {
			t.Errorf("tab %d = %q, want %q", i, s.tabs[i].label, w)
		}
	}
}

// TestReorderSupplierPerf_Loader drives tab 0 through a real client + server:
// the Decimal-as-string order value must render "$", and the already-percent
// rate must be "88.9%" (NOT re-multiplied to 8890%).
func TestReorderSupplierPerf_Loader(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"supplier_id":2,"supplier_name":"Acme","total_orders":9,
		"completed_orders":7,"active_orders":2,"average_lead_time_days":4.5,
		"on_time_delivery_rate":88.9,"early_delivery_rate":11.1,"late_delivery_rate":11.1,
		"total_order_value":"4200.75","damage_rate":2.5,"last_order_date":"2026-06-01T12:00:00Z",
		"days_since_last_order":36}]`)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	rows, err := s.tabs[0].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/reorders/analytics/supplier_performance/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	joined := strings.Join(rows[0], "|")
	// Supplier | Orders | Done | Avg lead d | On-time % | Late % | Damage % | Order value
	if rows[0][0] != "Acme" || rows[0][2] != "7" || rows[0][3] != "4.5" {
		t.Errorf("supplier perf cells wrong: %q", joined)
	}
	if rows[0][4] != "88.9%" {
		t.Errorf("on-time rate cell = %q, want 88.9%% (must not be re-scaled)", rows[0][4])
	}
	last := rows[0][len(rows[0])-1]
	if last != "$4,200.75" {
		t.Errorf("order value cell = %q, want $4,200.75 (Decimal-string money)", last)
	}
}

// TestReorderLeadTimeTrends_Loader covers tab 1: month string, trimmed floats,
// percentage rate.
func TestReorderLeadTimeTrends_Loader(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"month":"2026-05","average_lead_time_days":5.2,
		"average_variance_days":-0.3,"total_deliveries":12,"on_time_delivery_rate":91.7}]`)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	rows, err := s.tabs[1].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/reorders/analytics/lead_time_trends/" {
		t.Fatalf("path = %q", *path)
	}
	if rows[0][0] != "2026-05" || rows[0][1] != "5.2" || rows[0][2] != "-0.3" ||
		rows[0][3] != "12" || rows[0][4] != "91.7%" {
		t.Errorf("lead-time trend cells wrong: %q", strings.Join(rows[0], "|"))
	}
}

// transparencyBody is the shared transparency envelope for the summary / orders
// / POs tab tests (they all hit the same endpoint).
const transparencyBody = `{
	"summary":{"total_orders_with_financial_data":2,"total_amount_spent":150.25,
	 "total_purchase_orders":1,"total_po_amount_spent":300.00,
	 "last_updated":"2026-07-01T00:00:00Z","transparency_note":"open books"},
	"orders":[{"id":11,"item_id":"i-1","item_name":"PLA","item_category":"Filament",
	 "quantity_ordered":3,"status":"ordered","requested_at":"2026-06-01T00:00:00Z",
	 "ordered_at":"2026-06-02T00:00:00Z","delivered_at":null,"estimated_cost":50.0,
	 "actual_cost":null,"cost_per_unit":16.67,"cost_variance":null,"order_number":"ON-1",
	 "invoice_number":"","invoice_url":"","purchase_order_url":"","delivery_tracking_url":"",
	 "supplier_url":"","public_notes":"","supplier_name":"Acme"}],
	"ledger":[{"id":11,"item_name":"PLA"}],
	"purchase_orders":[{"id":"po-1","po_number":"PO-1","supplier_name":"Acme","status":"received",
	 "status_label":"Received","order_date":"2026-05-01T00:00:00Z","expected_delivery_date":null,
	 "estimated_total":300.0,"actual_total":null,"total_items":2,"total_quantity":5,
	 "is_fully_received":true}]
}`

// TestReorderTransparencySummary_Loader covers tab 2: object → Metric/Value rows.
func TestReorderTransparencySummary_Loader(t *testing.T) {
	c, path := fixedBodyClient(t, transparencyBody)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	rows, err := s.tabs[2].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/reorders/analytics/transparency/" {
		t.Fatalf("path = %q", *path)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[0]] = r[1]
	}
	if got["Total amount spent"] != "$150.25" {
		t.Errorf("total amount spent = %q, want $150.25", got["Total amount spent"])
	}
	if got["Total PO amount spent"] != "$300.00" {
		t.Errorf("total PO amount spent = %q, want $300.00", got["Total PO amount spent"])
	}
	if got["Orders with financial data"] != "2" || got["Purchase orders"] != "1" {
		t.Errorf("summary counts wrong: %+v", got)
	}
	if got["Last updated"] != "2026-07-01" {
		t.Errorf("last updated = %q, want date-only", got["Last updated"])
	}
}

// TestReorderTransparencyOrders_Loader covers tab 3: null actual_cost → "—",
// non-null estimated_cost → "$".
func TestReorderTransparencyOrders_Loader(t *testing.T) {
	c, _ := fixedBodyClient(t, transparencyBody)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	rows, err := s.tabs[3].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	// Item | Category | Qty | Status | Ordered | Est | Actual | Supplier
	r := rows[0]
	if r[0] != "PLA" || r[1] != "Filament" || r[2] != "3" || r[4] != "2026-06-02" || r[7] != "Acme" {
		t.Errorf("order cells wrong: %q", strings.Join(r, "|"))
	}
	if r[5] != "$50.00" {
		t.Errorf("est cost cell = %q, want $50.00", r[5])
	}
	if r[6] != "—" {
		t.Errorf("null actual cost cell = %q, want — (em dash)", r[6])
	}
}

// TestReorderTransparencyPOs_Loader covers tab 4: yes/no receipt + null totals.
func TestReorderTransparencyPOs_Loader(t *testing.T) {
	c, _ := fixedBodyClient(t, transparencyBody)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	rows, err := s.tabs[4].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	// PO # | Supplier | Status | Ordered | Expected | Est total | Actual total | Recv
	r := rows[0]
	if r[0] != "PO-1" || r[2] != "Received" || r[3] != "2026-05-01" {
		t.Errorf("PO cells wrong: %q", strings.Join(r, "|"))
	}
	if r[4] != "—" {
		t.Errorf("null expected date cell = %q, want —", r[4])
	}
	if r[5] != "$300.00" || r[6] != "—" {
		t.Errorf("PO totals wrong: est=%q actual=%q", r[5], r[6])
	}
	if r[7] != "yes" {
		t.Errorf("fully received cell = %q, want yes", r[7])
	}
}

// TestReorderLogistics_Loader covers tab 5: KPI rows + per-day QR rows.
func TestReorderLogistics_Loader(t *testing.T) {
	c, path := fixedBodyClient(t, `{"open_item_requests":4,"open_locations_with_problems":2,
		"urgent_location_problems":1,"alert_active":true,"assets_overdue_maintenance":3,
		"pm_overdue":5,"pm_due_this_week":2,"qr_scans_total":42,
		"qr_scans_by_day":[{"date":"2026-07-01","count":6},{"date":"2026-07-02","count":8}],
		"last_updated":"2026-07-07T00:00:00Z"}`)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	rows, err := s.tabs[5].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/reorders/analytics/logistics_dashboard/" {
		t.Fatalf("path = %q", *path)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[0]] = r[1]
	}
	if got["Open item requests"] != "4" || got["Alert active"] != "yes" ||
		got["QR scans (7-day total)"] != "42" || got["PM overdue"] != "5" {
		t.Errorf("logistics KPI rows wrong: %+v", got)
	}
	if got["QR scans 2026-07-02"] != "8" {
		t.Errorf("QR day row missing/wrong: %+v", got)
	}
}

// TestReorderAnalyticsReport_RenderSmokeAndEmpty drives the screen end-to-end
// through Init/Update/View, then confirms an empty array renders "No rows"
// rather than crashing.
func TestReorderAnalyticsReport_RenderSmokeAndEmpty(t *testing.T) {
	c, _ := fixedBodyClient(t, `[{"supplier_id":1,"supplier_name":"Acme","total_orders":3,
		"completed_orders":3,"active_orders":0,"average_lead_time_days":2.0,
		"on_time_delivery_rate":100.0,"early_delivery_rate":0.0,"late_delivery_rate":0.0,
		"total_order_value":"99.00","damage_rate":0.0,"last_order_date":null,
		"days_since_last_order":null}]`)
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	s.terminalHeight = 40
	s.Update(s.Init()()) // load tab 0
	out := s.View()
	for _, want := range []string{"Supplier perf", "Acme", "$99.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}

	empty, _ := fixedBodyClient(t, `[]`)
	se := NewReorderAnalyticsReportScreen(Deps{OMS: empty})
	se.terminalHeight = 40
	se.Update(se.Init()())
	if outE := se.View(); !strings.Contains(outE, "No rows") {
		t.Errorf("empty supplier_performance should say No rows:\n%s", outE)
	}
}

// TestReportsHub_ROpensReorderAnalytics locks the new hub wiring: 'r' opens the
// Reorders analytics report.
func TestReportsHub_ROpensReorderAnalytics(t *testing.T) {
	s := NewReportsScreen(Deps{})
	if !s.HandlesKey("r") {
		t.Fatal("hub should claim the 'r' hotkey")
	}
	_, cmd := s.Update(mtKey("r"))
	if cmd == nil {
		t.Fatal("'r' should open a report")
	}
	sm := cmd().(SwitchScreenMsg)
	rt, ok := sm.Screen.(*ReportTableScreen)
	if !ok || rt.Title() != "Reorders analytics" {
		t.Fatalf("'r' should open Reorders analytics, got %T", sm.Screen)
	}
}
