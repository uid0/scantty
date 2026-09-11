package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
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
	body, err := s.tabs[0].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	rows := body.rows
	if *path != "/api/reorders/analytics/supplier_performance/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	joined := strings.Join(rows[0], "|")
	// Supplier | Orders | Done | Lead d | On-time* | Late* | Damage | Order value
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
	body, err := s.tabs[1].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	rows := body.rows
	if *path != "/api/reorders/analytics/lead_time_trends/" {
		t.Fatalf("path = %q", *path)
	}
	if rows[0][0] != "2026-05" || rows[0][1] != "5.2" || rows[0][2] != "-0.3" ||
		rows[0][3] != "12" || rows[0][4] != "91.7%" {
		t.Errorf("lead-time trend cells wrong: %q", strings.Join(rows[0], "|"))
	}
}

// The three transparency tabs share one endpoint, and every body they are
// driven with here is RECORDED from a real OMS (internal/omsapi/testdata, with
// provenance in its README) rather than written beside the assertions: the
// hand-written envelope these tests used to share carried `supplier_name` and
// `estimated_cost` on each order — the shape the Go struct assumed, and one OMS
// main no longer sends — so it agreed with the defect it should have caught.
// reorder_transparency_test.go drives the same bodies through the rendered pane.

// transparencyColumn is the index of the column headed `header` on tab `tab`,
// read off the tab's own declaration so a cell check follows the column
// wherever the table puts it.
func transparencyColumn(t *testing.T, s *ReportTableScreen, tab int, header string) int {
	t.Helper()
	for i, c := range s.tabs[tab].columns {
		if c.header == header {
			return i
		}
	}
	t.Fatalf("tab %q has no %q column", s.tabs[tab].label, header)
	return -1
}

// TestReorderTransparencySummary_Loader covers the summary tab: object →
// Metric/Value rows, every figure read off the recorded body.
func TestReorderTransparencySummary_Loader(t *testing.T) {
	body := transparencyWire(t, "transparency_signed_in.json")
	var raw struct {
		Summary omsapi.ReorderTransparencySummary `json:"summary"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("recorded body: %v", err)
	}
	c, path := fixedBodyClient(t, string(body))
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	got, err := s.tabs[transparencyTab(t, "Transparency")].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/reorders/analytics/transparency/" {
		t.Fatalf("path = %q", *path)
	}
	cells := map[string]string{}
	for _, r := range got.rows {
		cells[r[0]] = r[1]
	}
	want := map[string]string{
		"Orders with financial data": itoa(raw.Summary.TotalOrdersWithFinancialData),
		"Total amount spent":         fmtMoney(raw.Summary.TotalAmountSpent),
		"Purchase orders":            itoa(raw.Summary.TotalPurchaseOrders),
		"Total PO amount spent":      fmtMoney(raw.Summary.TotalPOAmountSpent),
		"Last updated":               raw.Summary.LastUpdated[:10],
	}
	for k, v := range want {
		if cells[k] != v {
			t.Errorf("%s = %q, want %q", k, cells[k], v)
		}
	}
}

// TestReorderTransparencyOrders_Loader covers the order ledger's loader: one
// cell per declared column, the recorded $0.00 kept a figure and the null kept
// "—". The withheld state and the withdrawn columns are asked of the rendered
// pane in reorder_transparency_test.go.
func TestReorderTransparencyOrders_Loader(t *testing.T) {
	body := transparencyWire(t, "transparency_signed_in.json")
	c, _ := fixedBodyClient(t, string(body))
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	tab := transparencyTab(t, "Trans. orders")
	got, err := s.tabs[tab].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	orders := rawOrders(t, body)
	if len(got.rows) != len(orders) {
		t.Fatalf("rows = %d, want one per recorded order (%d)", len(got.rows), len(orders))
	}
	item, actual := transparencyColumn(t, s, tab, "Item"), transparencyColumn(t, s, tab, "Actual")
	for i, o := range orders {
		if n := len(got.rows[i]); n != len(s.tabs[tab].columns) {
			t.Fatalf("row %d has %d cells for %d columns", i, n, len(s.tabs[tab].columns))
		}
		if got.rows[i][item] != o["item_name"] {
			t.Errorf("row %d item = %q, want %q", i, got.rows[i][item], o["item_name"])
		}
		want := "—"
		if v, ok := o["actual_cost"].(float64); ok {
			want = fmtMoney(v)
		}
		if got.rows[i][actual] != want {
			t.Errorf("row %d actual = %q, want %q", i, got.rows[i][actual], want)
		}
	}
}

// TestReorderTransparencyPOs_Loader covers the PO ledger: yes/no receipt, null
// dates and totals as "—", every value read off the recorded body.
func TestReorderTransparencyPOs_Loader(t *testing.T) {
	body := transparencyWire(t, "transparency_signed_in.json")
	var raw struct {
		POs []omsapi.ReorderTransparencyPO `json:"purchase_orders"`
	}
	if err := json.Unmarshal(body, &raw); err != nil || len(raw.POs) == 0 {
		t.Fatalf("recorded body has no purchase orders (%v)", err)
	}
	p := raw.POs[0]
	if p.ExpectedDeliveryDate != "" || p.ActualTotal != nil || p.EstimatedTotal == nil {
		t.Fatal("the recording no longer carries a null expected date, a null actual total and " +
			"an estimated total, so the \"—\" and money checks below would be vacuous")
	}
	c, _ := fixedBodyClient(t, string(body))
	s := NewReorderAnalyticsReportScreen(Deps{OMS: c})
	tab := transparencyTab(t, "Trans. POs")
	got, err := s.tabs[tab].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	r := got.rows[0]
	cell := func(h string) string { return r[transparencyColumn(t, s, tab, h)] }
	for h, want := range map[string]string{
		"PO #":         p.PONumber,
		"Supplier":     p.SupplierName,
		"Status":       p.StatusLabel,
		"Ordered":      p.OrderDate[:10],
		"Expected":     "—",
		"Est total":    fmtMoney(*p.EstimatedTotal),
		"Actual total": "—",
		"Recv":         fmtYesNo(p.IsFullyReceived),
	} {
		if cell(h) != want {
			t.Errorf("%s = %q, want %q", h, cell(h), want)
		}
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
	body, err := s.tabs[5].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	rows := body.rows
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
	// BOTH dimensions. This screen lays its table out against the width the
	// terminal really gives, so a fixture that sets only the height is laid out
	// for the narrowest supported pane (51 cells) — where an eight-column
	// supplier-performance table legitimately cannot show its order value, and
	// says so instead. 120 is a terminal that HAS the room for every column.
	s.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	s.Update(s.Init()()) // load tab 0
	out := s.View()
	for _, want := range []string{"Supplier perf", "Acme", "$99.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}

	empty, _ := fixedBodyClient(t, `[]`)
	se := NewReorderAnalyticsReportScreen(Deps{OMS: empty})
	se.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
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
