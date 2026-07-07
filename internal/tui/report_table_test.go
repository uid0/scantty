package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func TestReportFormatters(t *testing.T) {
	n7 := 7
	cases := []struct {
		got, want, label string
	}{
		{fmtMoney(1234.5), "$1,234.50", "fmtMoney thousands"},
		{fmtMoney(0), "$0.00", "fmtMoney zero"},
		{fmtMoney(1000000), "$1,000,000.00", "fmtMoney millions"},
		{fmtMoneyStr("175.00"), "$175.00", "fmtMoneyStr"},
		{fmtMoneyStr("1234567.89"), "$1,234,567.89", "fmtMoneyStr big"},
		{fmtMoneyStr(""), "—", "fmtMoneyStr blank"},
		{commaGroup("999"), "999", "commaGroup small"},
		{commaGroup("1234"), "1,234", "commaGroup 4"},
		{commaGroup("-1234.5"), "-1,234.5", "commaGroup negative"},
		{dateOnly("2026-04-01T10:00:00Z"), "2026-04-01", "dateOnly datetime"},
		{dateOnly("2026-04-01"), "2026-04-01", "dateOnly bare"},
		{orDash(""), "—", "orDash blank"},
		{orDash("x"), "x", "orDash value"},
		{intOrDash(nil), "—", "intOrDash nil"},
		{intOrDash(&n7), "7", "intOrDash value"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: got %q want %q", c.label, c.got, c.want)
		}
	}
}

func TestReportTableLines_Alignment(t *testing.T) {
	cols := []reportColumn{{"Name", alignLeft}, {"Qty", alignRight}}
	rows := [][]string{{"A", "5"}, {"Bravo", "100"}}
	header, body := reportTableLines(cols, rows)
	if !strings.Contains(header, "Name") || !strings.Contains(header, "Qty") {
		t.Errorf("header missing column names: %q", header)
	}
	// Right-aligned numeric column pads on the left: "5" → "  5" in a width-3 col.
	if !strings.HasSuffix(body[0], "  5") {
		t.Errorf("row0 should right-align 5 in its column: %q", body[0])
	}
	if !strings.HasSuffix(body[1], "100") {
		t.Errorf("row1 should end with 100: %q", body[1])
	}
	// Left column padded to the widest cell ("Bravo" = 5) so both rows align.
	if !strings.HasPrefix(body[0], "A    ") {
		t.Errorf("row0 left column should pad A to width 5: %q", body[0])
	}
}

// cannedTab builds a tab whose loader returns fixed rows (no network).
func cannedTab(label string, rows [][]string) reportTab {
	return reportTab{
		label:   label,
		columns: []reportColumn{{"Col", alignLeft}, {"N", alignRight}},
		loader: func(ctx context.Context, deps Deps) ([][]string, error) {
			return rows, nil
		},
	}
}

func TestReportTableScreen_RendersRowsAndCursor(t *testing.T) {
	s := NewReportTableScreen(Deps{}, "Test", []reportTab{
		cannedTab("Alpha", [][]string{{"a", "1"}, {"b", "2"}}),
		cannedTab("Beta", [][]string{{"x", "9"}}),
	})
	s.terminalHeight = 40
	cmd := s.Init()
	if cmd == nil {
		t.Fatal("Init should load the active tab")
	}
	s.Update(cmd())
	out := s.View()
	for _, want := range []string{"Alpha", "Beta", "Col", "a", "b", "▸ "} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestReportTableScreen_TabSwitchLazyLoads(t *testing.T) {
	s := NewReportTableScreen(Deps{}, "Test", []reportTab{
		cannedTab("Alpha", [][]string{{"a", "1"}}),
		cannedTab("Beta", [][]string{{"x", "9"}}),
	})
	s.terminalHeight = 40
	s.Update(s.Init()()) // load Alpha

	next, cmd := s.Update(mtKey("right"))
	s = next.(*ReportTableScreen)
	if s.active != 1 {
		t.Fatalf("right should switch to tab 1, got %d", s.active)
	}
	if cmd == nil {
		t.Fatal("switching to an unloaded tab should trigger a load")
	}
	s.Update(cmd())
	out := s.View()
	if !strings.Contains(out, "x") || !strings.Contains(out, "9") {
		t.Errorf("beta rows should render after switch:\n%s", out)
	}

	// Switching back should NOT reload (already cached): no cmd.
	_, cmd2 := s.Update(mtKey("left"))
	if cmd2 != nil {
		t.Errorf("switching back to a loaded tab should not reload")
	}
}

func TestReportTableScreen_EmptyRows(t *testing.T) {
	s := NewReportTableScreen(Deps{}, "Test", []reportTab{cannedTab("Empty", [][]string{})})
	s.terminalHeight = 40
	s.Update(s.Init()())
	out := s.View()
	if !strings.Contains(out, "No rows") {
		t.Errorf("empty tab should say No rows:\n%s", out)
	}
}

func TestReportTableScreen_EscReturnsToHub(t *testing.T) {
	s := NewReportTableScreen(Deps{}, "Test", []reportTab{cannedTab("Alpha", [][]string{{"a", "1"}})})
	_, cmd := s.Update(mtKey("esc"))
	if cmd == nil {
		t.Fatal("esc should fire a cmd")
	}
	sm, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("esc msg = %T, want SwitchScreenMsg", cmd())
	}
	if sm.Workspace != WSReports {
		t.Errorf("esc workspace = %q, want reports", sm.Workspace)
	}
	if _, ok := sm.Screen.(*ReportsScreen); !ok {
		t.Errorf("esc should return to the Reports hub, got %T", sm.Screen)
	}
}

func TestReportTableScreen_HandlesKey(t *testing.T) {
	s := NewReportTableScreen(Deps{}, "Test", nil)
	if !s.HandlesKey("G") || !s.HandlesKey("esc") {
		t.Errorf("screen should claim G and esc")
	}
	if s.HandlesKey("j") || s.HandlesKey("r") {
		t.Errorf("screen should not claim j/r (they reach it via fallthrough)")
	}
}

func TestReportTableScreen_GJumpsToEnd(t *testing.T) {
	rows := make([][]string, 50)
	for i := range rows {
		rows[i] = []string{fmt.Sprintf("row%d", i), fmt.Sprintf("%d", i)}
	}
	s := NewReportTableScreen(Deps{}, "Test", []reportTab{cannedTab("Big", rows)})
	s.terminalHeight = 24
	s.Update(s.Init()())
	s.Update(mtKey("G"))
	if s.states[0].cursor != 49 {
		t.Errorf("G should jump to last row (49), got %d", s.states[0].cursor)
	}
	// The window should have scrolled so the last row is visible.
	if s.states[0].windowStart == 0 {
		t.Errorf("windowStart should advance when jumping to the end")
	}
}

// fixedBodyClient returns a client whose every GET returns the given JSON body,
// plus the captured request path.
func fixedBodyClient(t *testing.T, body string) (*omsapi.Client, *string) {
	t.Helper()
	got := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return omsapi.New(srv.URL), got
}

// TestInventoryReportScreen_LoaderFormatsMoney drives tab 0's loader through a
// real client + server and checks the float money renders as "$1,234.50".
func TestInventoryReportScreen_LoaderFormatsMoney(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"category_id":1,"category_name":"Filament","total_items":12,
		"total_stock":340,"total_value":1234.5,"low_stock_count":2}]`)
	s := NewInventoryReportScreen(Deps{OMS: c})
	if len(s.tabs) != 3 {
		t.Fatalf("inventory report should have 3 tabs, got %d", len(s.tabs))
	}
	rows, err := s.tabs[0].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/inventory/reports/inventory/stock_by_category/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 1 || rows[0][0] != "Filament" || rows[0][3] != "$1,234.50" {
		t.Errorf("stock_by_category cells wrong: %v", rows)
	}
}

// TestAssetReportScreen_TCOFormatsDecimalString verifies the TCO tab renders
// the Decimal-as-string money fields with a "$".
func TestAssetReportScreen_TCOFormatsDecimalString(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"asset_id":"a-1","asset_name":"Lathe","asset_tag":"DMS-1",
		"maintenance_days_last_90":6,"scheduled_maintenance_cost":"100.00",
		"unscheduled_maintenance_cost":"50.00","repair_cost":"25.00","tco":"175.00",
		"preventive_maintenance_cost":"100.00","vendor_maintenance_cost":"0.00",
		"total_maintenance_cost_90d":"175.00"}]`)
	s := NewAssetReportScreen(Deps{OMS: c})
	if len(s.tabs) != 4 {
		t.Fatalf("asset report should have 4 tabs, got %d", len(s.tabs))
	}
	// TCO is the last tab.
	rows, err := s.tabs[3].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("tco loader: %v", err)
	}
	if *path != "/api/inventory/reports/assets/tco/" {
		t.Fatalf("path = %q", *path)
	}
	if len(rows) != 1 {
		t.Fatalf("tco rows = %d", len(rows))
	}
	last := rows[0][len(rows[0])-1]
	if last != "$175.00" {
		t.Errorf("tco total cell = %q, want $175.00", last)
	}
}

// TestPurchasingLeadTime_OnTimeRateNotDoubled locks the fix: the backend's
// on_time_rate is already a 0..100 percentage, so the cell must be "75%", not
// "7500%" (a ×100 double-scale would be a real bug).
func TestPurchasingLeadTime_OnTimeRateNotDoubled(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt",
		"total_orders":4,"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,
		"avg_variance":1.5,"on_time_rate":75.0}]`)
	s := NewPurchasingReportScreen(Deps{OMS: c})
	// Lead time is the 3rd tab (index 2).
	rows, err := s.tabs[2].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("lead_time loader: %v", err)
	}
	if *path != "/api/reorders/reports/purchasing/lead_time_analysis/" {
		t.Fatalf("path = %q", *path)
	}
	last := rows[0][len(rows[0])-1]
	if last != "75%" {
		t.Errorf("on_time_rate cell = %q, want 75%% (must not be re-multiplied by 100)", last)
	}
}

// TestAssetMaintenanceDue_IncludesSKUAndLastReplaced ensures the maintenance-due
// tab surfaces every column the web report shows, incl. part_sku + last_replaced_at.
func TestAssetMaintenanceDue_IncludesSKUAndLastReplaced(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"asset_id":"a-1","asset_name":"Lathe","asset_tag":"DMS-1",
		"part_id":"p-1","part_name":"Belt","part_sku":"BELT-9","maintenance_interval_days":90,
		"days_since_replacement":120,"days_overdue":30,"last_replaced_at":"2026-03-01T00:00:00Z"}]`)
	s := NewAssetReportScreen(Deps{OMS: c})
	// Maintenance due is tab index 1.
	rows, err := s.tabs[1].loader(context.Background(), Deps{OMS: c})
	if err != nil {
		t.Fatalf("maintenance_due loader: %v", err)
	}
	if *path != "/api/inventory/reports/assets/maintenance_due/" {
		t.Fatalf("path = %q", *path)
	}
	joined := strings.Join(rows[0], "|")
	for _, want := range []string{"BELT-9", "2026-03-01"} {
		if !strings.Contains(joined, want) {
			t.Errorf("maintenance_due row missing %q: %q", want, joined)
		}
	}
	// The column header set must include SKU + Last replaced.
	var headers []string
	for _, col := range s.tabs[1].columns {
		headers = append(headers, col.header)
	}
	hj := strings.Join(headers, "|")
	if !strings.Contains(hj, "SKU") || !strings.Contains(hj, "Last replaced") {
		t.Errorf("maintenance_due columns missing SKU/Last replaced: %q", hj)
	}
}

func TestPurchasingReportScreen_HasFourTabs(t *testing.T) {
	s := NewPurchasingReportScreen(Deps{})
	if len(s.tabs) != 4 {
		t.Fatalf("purchasing report should have 4 tabs, got %d", len(s.tabs))
	}
	wantLabels := []string{"Spend by supplier", "Spend by category", "Lead time", "Price trends"}
	for i, w := range wantLabels {
		if s.tabs[i].label != w {
			t.Errorf("tab %d = %q, want %q", i, s.tabs[i].label, w)
		}
	}
}
