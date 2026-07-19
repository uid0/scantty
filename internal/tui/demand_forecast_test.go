package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// sampleDemandRow is a fully-populated DemandForecastRow (both nullable
// pointers set) used to exercise the detail render.
func sampleDemandRow() omsapi.DemandForecastRow {
	days := 3.25
	lead := 7
	return omsapi.DemandForecastRow{
		ID:                     42,
		Item:                   "itm-1",
		ItemName:               "PLA filament",
		SKU:                    "PLA-175",
		CategoryName:           "Printing",
		GeneratedAt:            time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC),
		HorizonDays:            21,
		PredictedDailyDemand:   1.2857,
		HorizonDemand:          27.0,
		HorizonDemandUpper:     34.5,
		AvailableAtGeneration:  4,
		DaysUntilStockout:      &days,
		ProjectedStockoutDate:  "2026-07-22",
		PredictiveReorderPoint: 12,
		NeedsReorder:           true,
		LeadTimeDays:           &lead,
		SafetyStock:            6,
		Method:                 omsapi.ForecastMethodHoltWinters,
		ModelVersion:           "hw-1",
	}
}

// TestDemandForecast_DetailRendersFields: the detail body labels and shows
// every field the row carries (identity, projection, reorder decision, model).
func TestDemandForecast_DetailRendersFields(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.detailRow = sampleDemandRow()

	body := s.renderDetail()
	for _, want := range []string{
		"PLA filament", "PLA-175", "itm-1", "Printing",
		"projected to run out",
		"Horizon", "21 days",
		"Predicted daily demand", "1.29/day",
		"Horizon demand", "27",
		"Horizon demand (upper band)", "34.5",
		"Days until stockout", "3.25 d",
		"Projected stockout date", "2026-07-22",
		"Available at generation", "4",
		"Predictive reorder point", "12",
		"Safety stock", "6",
		"Lead time", "7 d",
		"Needs reorder", "yes",
		"Method", "Holt-Winters seasonal",
		"Model version", "hw-1",
		"Generated at",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q:\n%s", want, body)
		}
	}
	// Raw is off by default: no raw JSON, but a hint that r reveals it.
	if strings.Contains(body, "\"predicted_daily_demand\"") {
		t.Errorf("raw JSON should be hidden until toggled:\n%s", body)
	}
	if !strings.Contains(body, "press r to show the raw JSON") {
		t.Errorf("detail should hint at the raw toggle when off:\n%s", body)
	}
}

// TestDemandForecast_DetailNullableFields: when the nullable pointers are nil
// the detail renders explanatory placeholders, not a crash or a misleading "0".
func TestDemandForecast_DetailNullableFields(t *testing.T) {
	row := sampleDemandRow()
	row.DaysUntilStockout = nil
	row.LeadTimeDays = nil
	row.ProjectedStockoutDate = ""
	row.ModelVersion = ""
	s := NewDemandForecastScreen(Deps{})
	s.detailRow = row

	body := s.renderDetail()
	for _, want := range []string{
		"no depletion projected", // days-until-stockout nil
		"unknown",                // lead-time nil
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing nil-placeholder %q:\n%s", want, body)
		}
	}
}

// TestDemandForecast_RawToggle: r reveals the pretty-printed JSON record (with
// its wire field names) and hides it again.
func TestDemandForecast_RawToggle(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.DemandForecastRow{sampleDemandRow()}
	s.openDetail()

	if s.showRaw {
		t.Fatalf("raw should start hidden")
	}
	s.Update(mtKey("r")) // toggle raw on
	if !s.showRaw {
		t.Fatalf("r should turn raw on")
	}
	body := s.renderDetail()
	for _, want := range []string{
		"Raw",
		"\"item\"", "\"needs_reorder\"", "\"days_until_stockout\"",
		"\"predicted_daily_demand\"", "\"predictive_reorder_point\"", "\"method\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("raw body missing %q:\n%s", want, body)
		}
	}
	s.Update(mtKey("r")) // toggle raw off
	if s.showRaw {
		t.Fatalf("r should turn raw off")
	}
	if strings.Contains(s.renderDetail(), "\"predicted_daily_demand\"") {
		t.Errorf("raw JSON should be hidden after toggling off")
	}
}

// TestDemandForecast_EnterOpensDetail_EscReturns: Enter on the highlighted row
// opens its detail; esc returns to the list with the cursor preserved.
func TestDemandForecast_EnterOpensDetail_EscReturns(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.DemandForecastRow{
		{Item: "a", ItemName: "Alpha"},
		{Item: "b", ItemName: "Bravo"},
		{Item: "c", ItemName: "Charlie"},
	}
	s.cursor = 2

	s.Update(mtKey("enter"))
	if s.mode != forecastModeDetail {
		t.Fatalf("enter should open detail mode, got %d", s.mode)
	}
	if s.detailRow.Item != "c" {
		t.Fatalf("detail should snapshot the highlighted row, got %q", s.detailRow.Item)
	}
	if out := s.View(); !strings.Contains(out, "Charlie") {
		t.Errorf("detail view should show the selected item, got:\n%s", out)
	}

	s.Update(mtKey("esc"))
	if s.mode != forecastModeList {
		t.Fatalf("esc should return to the list, got mode %d", s.mode)
	}
	if s.cursor != 2 {
		t.Errorf("esc should preserve the cursor, got %d", s.cursor)
	}
}

// TestDemandForecast_RawInputGating: the list stays non-raw (so global hotkeys
// keep working) while the detail claims raw input; `a` is claimed in both so
// the global "a" (authorizations) never steals the view swap.
func TestDemandForecast_RawInputGating(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.DemandForecastRow{sampleDemandRow()}
	if s.WantsRawInput() {
		t.Errorf("list mode should not claim raw input")
	}
	if !s.HandlesKey("a") {
		t.Errorf("the screen must claim 'a' or the global authorizations hotkey wins")
	}
	if s.HandlesKey("esc") || s.HandlesKey("r") {
		t.Errorf("only 'a' should be claimed from the globals")
	}
	s.openDetail()
	if !s.WantsRawInput() {
		t.Errorf("detail mode should claim raw input")
	}
}

// TestDemandForecast_ListRendersCursorAndRows: the list highlights the cursor
// row and shows the columns the report is for — item, method, avg/day,
// days-to-stockout, reorder point and the flagged status.
func TestDemandForecast_ListRendersCursorAndRows(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.DemandForecastRow{sampleDemandRow()}
	s.windowSize = s.computeWindowSize()

	out := s.View()
	for _, want := range []string{
		"Demand forecast", "PLA filament", "PLA-175", "REORDER",
		"1 need reorder", "~1.29/day", "3.25d to stockout", "reorder@12",
		"Holt-Winters seasonal", "projected stockout 2026-07-22", "lead 7d",
		"horizon 21d", "enter detail", "▸ ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list view missing %q:\n%s", want, out)
		}
	}
}

// TestDemandForecast_AlertsView: the notify-set view titles itself differently,
// drops the low-stock filter (the backend already filtered), and offers the way
// back to the full forecast. `a` swaps between the two.
func TestDemandForecast_AlertsView(t *testing.T) {
	s := NewReorderAlertsScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.DemandForecastRow{sampleDemandRow()}
	s.windowSize = s.computeWindowSize()

	if s.Title() != "Reorder alerts" {
		t.Errorf("alerts title = %q", s.Title())
	}
	out := s.View()
	for _, want := range []string{"Reorder alerts", "notify set", "a full forecast"} {
		if !strings.Contains(out, want) {
			t.Errorf("alerts view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "w flagged only") || strings.Contains(out, "w show all") {
		t.Errorf("the low-stock filter has no meaning in the alerts view:\n%s", out)
	}

	// w is inert here — it must not flip a filter that the alerts endpoint
	// doesn't accept.
	s.Update(mtKey("w"))
	if s.lowOnly {
		t.Errorf("w should be a no-op in the alerts view")
	}

	// a swaps back to the full forecast (and would refetch).
	s.cursor = 0
	s.Update(mtKey("a"))
	if s.alerts {
		t.Fatalf("a should swap the alerts view back to the full forecast")
	}
	if !s.loading {
		t.Errorf("swapping views should refetch")
	}
	if s.Title() != "Demand forecast" {
		t.Errorf("forecast title = %q", s.Title())
	}
}

// TestDemandForecast_LowStockToggle: w flips the flagged-only filter in the
// forecast view, resets the cursor and refetches (the row set changes).
func TestDemandForecast_LowStockToggle(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.DemandForecastRow{sampleDemandRow(), sampleDemandRow()}
	s.cursor = 1

	s.Update(mtKey("w"))
	if !s.lowOnly {
		t.Fatalf("w should turn the flagged-only filter on")
	}
	if s.cursor != 0 || s.windowStart != 0 {
		t.Errorf("filter change should reset the cursor, got %d/%d", s.cursor, s.windowStart)
	}
	if !s.loading {
		t.Errorf("filter change should refetch")
	}
	s.loading = false
	s.Update(mtKey("w"))
	if s.lowOnly {
		t.Errorf("w should turn the filter back off")
	}
}

// TestDemandForecast_EmptyStates: both endpoints read STORED rows, so an empty
// list means "not forecast yet" / "nothing due" — say which, rather than
// rendering a bare "0 items".
func TestDemandForecast_EmptyStates(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40

	if out := s.View(); !strings.Contains(out, "No stored forecasts yet") {
		t.Errorf("empty forecast should explain the nightly run:\n%s", out)
	}
	s.lowOnly = true
	if out := s.View(); !strings.Contains(out, "Nothing is flagged for reorder") {
		t.Errorf("empty flagged-only view should say nothing is flagged:\n%s", out)
	}

	a := NewReorderAlertsScreen(Deps{})
	a.loading = false
	a.terminalHeight = 40
	out := a.View()
	for _, want := range []string{"No items are due to reorder", "ML reorder alerts"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty alerts view missing %q:\n%s", want, out)
		}
	}
}

// TestDemandForecast_MethodLabels maps the stored engine values to readable
// names and passes an unknown (newer-backend) engine through untouched.
func TestDemandForecast_MethodLabels(t *testing.T) {
	cases := map[string]string{
		omsapi.ForecastMethodProphet:     "Prophet",
		omsapi.ForecastMethodHoltWinters: "Holt-Winters seasonal",
		omsapi.ForecastMethodFallback:    "statistical fallback",
		"neuralnet":                      "neuralnet",
		"":                               "—",
	}
	for method, want := range cases {
		if got := forecastMethodLabel(method); got != want {
			t.Errorf("forecastMethodLabel(%q) = %q, want %q", method, got, want)
		}
	}
}
