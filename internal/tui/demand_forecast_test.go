package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// sampleDemandRow is a fully-populated v2 restock-interval row (every interval
// nullable set, the retired v1 columns zeroed the way a v2 run writes them)
// used to exercise the list and detail renders.
func sampleDemandRow() omsapi.DemandForecastRow {
	cadence := 47.5
	lastRestock := "2026-06-25"
	nextDue := "2026-08-12"
	daysUntilDue := -3.0
	lead := 7
	zeroI, zeroF := 0, 0.0
	return omsapi.DemandForecastRow{
		ID:                       42,
		Item:                     "itm-1",
		ItemName:                 "PLA filament",
		SKU:                      "PLA-175",
		CategoryName:             "Printing",
		GeneratedAt:              time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC),
		AvgIntervalDays:          &cadence,
		IntervalSamples:          5,
		LastRestockDate:          &lastRestock,
		PredictedNextReorderDate: &nextDue,
		DaysUntilDue:             &daysUntilDue,
		HorizonDays:              &zeroI,
		PredictedDailyDemand:     &zeroF,
		HorizonDemand:            &zeroF,
		HorizonDemandUpper:       &zeroF,
		PredictiveReorderPoint:   &zeroI,
		SafetyStock:              &zeroI,
		AvailableAtGeneration:    4,
		NeedsReorder:             true,
		LeadTimeDays:             &lead,
		Method:                   omsapi.ForecastMethodRestockInterval,
		ModelVersion:             "interval-1",
	}
}

// insufficientHistoryRow is what the engine stores for an item with fewer than
// two purchases: no cadence, no due date, and nothing flagged.
func insufficientHistoryRow() omsapi.DemandForecastRow {
	return omsapi.DemandForecastRow{
		ID:       43,
		Item:     "itm-2",
		ItemName: "Blue tape",
		SKU:      "TAPE-B",
		Method:   omsapi.ForecastMethodInsufficientHistory,
	}
}

// legacyDemandRow is a row a pre-v2 (usage-rate) run wrote: no interval signal,
// but a real quantity projection.
func legacyDemandRow() omsapi.DemandForecastRow {
	horizon, reorderPoint, safety, lead := 21, 12, 6, 7
	daily, demand, upper, stockout := 1.2857, 27.0, 34.5, 3.25
	stockoutDate := "2026-07-22"
	return omsapi.DemandForecastRow{
		ID:                     7,
		Item:                   "itm-3",
		ItemName:               "Nozzle 0.4",
		GeneratedAt:            time.Date(2026, 5, 1, 3, 0, 0, 0, time.UTC),
		HorizonDays:            &horizon,
		PredictedDailyDemand:   &daily,
		HorizonDemand:          &demand,
		HorizonDemandUpper:     &upper,
		DaysUntilStockout:      &stockout,
		ProjectedStockoutDate:  &stockoutDate,
		PredictiveReorderPoint: &reorderPoint,
		SafetyStock:            &safety,
		AvailableAtGeneration:  4,
		NeedsReorder:           true,
		LeadTimeDays:           &lead,
		Method:                 omsapi.ForecastMethodHoltWinters,
		ModelVersion:           "hw-1",
	}
}

// TestDemandForecast_DetailRendersFields: the detail body labels and shows the
// interval signal the row carries (identity, cadence, due date, reorder
// decision, model) — and none of the retired v1 quantity labels, which a v2
// row has no numbers for.
func TestDemandForecast_DetailRendersFields(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.detailRow = sampleDemandRow()

	body := s.renderDetail()
	for _, want := range []string{
		"PLA filament", "PLA-175", "itm-1", "Printing",
		"REORDER · overdue by 3d",
		"Restock interval",
		"Cadence", "every ~47.5 days",
		"Intervals measured", "5",
		"Last restock", "2026-06-25",
		"Predicted next reorder", "2026-08-12",
		"Days until due", "-3 d (overdue by 3d)",
		"Needs reorder", "yes",
		"Lead time", "7 d",
		"Available at generation", "4",
		"Method", "Model version", "interval-1",
		"Generated at",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q:\n%s", want, body)
		}
	}
	// The v1 projection belongs only to v1 rows — it must not resurface as a
	// column of zeroes on an interval row.
	for _, gone := range []string{
		"Predicted daily demand", "Predictive reorder point",
		"Days until stockout", "Safety stock", "Retired v1 projection",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("v2 detail should not show retired v1 field %q:\n%s", gone, body)
		}
	}
	// Raw is off by default: no raw JSON, but a hint that r reveals it.
	if strings.Contains(body, "\"avg_interval_days\"") {
		t.Errorf("raw JSON should be hidden until toggled:\n%s", body)
	}
	if !strings.Contains(body, "press r to show the raw JSON") {
		t.Errorf("detail should hint at the raw toggle when off:\n%s", body)
	}
}

// TestDemandForecast_DetailNullableFields: when the nullable pointers are nil
// the detail renders explanatory placeholders, not a crash or a misleading "0"
// cadence / "due today".
func TestDemandForecast_DetailNullableFields(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.detailRow = insufficientHistoryRow()

	body := s.renderDetail()
	for _, want := range []string{
		"not enough purchase history", // header + cadence reason
		"no due date predicted",       // days-until-due nil
		"unknown",                     // lead-time nil
		"Last restock: —",             // never bought
		"Predicted next reorder: —",
		"Intervals measured: 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing nil-placeholder %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "every ~0") || strings.Contains(body, "due today") {
		t.Errorf("a nil cadence/due date must not render as zero:\n%s", body)
	}
}

// TestDemandForecast_LegacyRowShowsRetiredProjection: a row a pre-v2 run wrote
// still shows its quantity projection (that section is the only place those
// numbers are real), while reporting that it has no cadence.
func TestDemandForecast_LegacyRowShowsRetiredProjection(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.detailRow = legacyDemandRow()

	body := s.renderDetail()
	for _, want := range []string{
		"Retired v1 projection",
		"Horizon", "21 days",
		"Predicted daily demand", "1.29/day",
		"Horizon demand (upper band)", "34.5",
		"Days until stockout", "3.25 d",
		"Projected stockout date", "2026-07-22",
		"Predictive reorder point", "12",
		"Safety stock", "6",
		"Holt-Winters seasonal",
		"no cadence recorded", // it never measured one
	} {
		if !strings.Contains(body, want) {
			t.Errorf("legacy detail missing %q:\n%s", want, body)
		}
	}
	// A v1 row is not "insufficient history" — it's a different engine.
	if strings.Contains(body, "not enough purchase history") {
		t.Errorf("a v1 row must not be described as insufficient history:\n%s", body)
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
		"\"item\"", "\"needs_reorder\"", "\"method\"",
		"\"avg_interval_days\"", "\"interval_samples\"", "\"last_restock_date\"",
		"\"predicted_next_reorder_date\"", "\"days_until_due\"",
		// The retired columns stay in the raw dump even when the rendered
		// detail hides them — raw is the wire record, not the rendering.
		"\"predicted_daily_demand\"", "\"predictive_reorder_point\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("raw body missing %q:\n%s", want, body)
		}
	}
	s.Update(mtKey("r")) // toggle raw off
	if s.showRaw {
		t.Fatalf("r should turn raw off")
	}
	if strings.Contains(s.renderDetail(), "\"avg_interval_days\"") {
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
// row and shows the columns the interval report is for — item, method, cadence,
// next due, days-until-due and the due status. The v1 rate columns are gone.
func TestDemandForecast_ListRendersCursorAndRows(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.DemandForecastRow{sampleDemandRow()}
	s.windowSize = s.computeWindowSize()

	out := s.View()
	for _, want := range []string{
		"Demand forecast", "PLA filament", "PLA-175", "REORDER",
		"1 due to reorder", "Restock interval", "every ~47.5d",
		"next 2026-08-12", "overdue by 3d",
		"last restock 2026-06-25", "5 interval(s)", "lead 7d",
		"enter detail", "▸ ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list view missing %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"/day", "reorder@", "to stockout", "horizon"} {
		if strings.Contains(out, gone) {
			t.Errorf("list view still shows retired v1 column %q:\n%s", gone, out)
		}
	}
}

// TestDemandForecast_ListInsufficientHistory: an item without enough purchase
// history says so on its row instead of showing a fabricated cadence or due
// date, and doesn't count toward the due tally.
func TestDemandForecast_ListInsufficientHistory(t *testing.T) {
	s := NewDemandForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.DemandForecastRow{insufficientHistoryRow()}
	s.windowSize = s.computeWindowSize()

	out := s.View()
	for _, want := range []string{
		"Blue tape", "not enough purchase history", "1 without a cadence",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list view missing %q:\n%s", want, out)
		}
	}
	for _, gone := range []string{"every ~", "due in", "overdue", "REORDER", "due to reorder"} {
		if strings.Contains(out, gone) {
			t.Errorf("a history-less row must not render %q:\n%s", gone, out)
		}
	}
}

// TestDemandForecast_DuePhrase mirrors the backend digest's wording so an alert
// notification and this screen describe the same row the same way.
func TestDemandForecast_DuePhrase(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		days *float64
		want string
	}{
		{nil, "no due date"},
		{f(-3), "overdue by 3d"},
		{f(-0.5), "due today"}, // truncates toward zero, as the digest's int() does
		{f(0), "due today"},
		{f(5), "due in 5d"},
		{f(5.9), "due in 5d"},
	}
	for _, tc := range cases {
		if got := forecastDuePhrase(tc.days); got != tc.want {
			t.Errorf("forecastDuePhrase(%v) = %q, want %q", tc.days, got, tc.want)
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
	for _, want := range []string{
		"Reorder alerts", "notify set", "a full forecast",
		// Due-based wording: an alert row says when it came due, not how fast
		// it is being consumed.
		"overdue by 3d", "1 due to reorder",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("alerts view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "w due only") || strings.Contains(out, "w show all") {
		t.Errorf("the due-only filter has no meaning in the alerts view:\n%s", out)
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
	if out := s.View(); !strings.Contains(out, "Nothing is due to reorder") {
		t.Errorf("empty due-only view should say nothing is due:\n%s", out)
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

// TestDemandForecast_MethodLabels maps the stored engine values — the live v2
// pair and the retired v1 ones history still carries — to readable names, and
// passes an unknown (newer-backend) engine through untouched.
func TestDemandForecast_MethodLabels(t *testing.T) {
	cases := map[string]string{
		omsapi.ForecastMethodRestockInterval:     "Restock interval",
		omsapi.ForecastMethodInsufficientHistory: "Insufficient history",
		omsapi.ForecastMethodProphet:             "Prophet",
		omsapi.ForecastMethodHoltWinters:         "Holt-Winters seasonal",
		omsapi.ForecastMethodFallback:            "statistical fallback",
		"neuralnet":                              "neuralnet",
		"":                                       "—",
	}
	for method, want := range cases {
		if got := forecastMethodLabel(method); got != want {
			t.Errorf("forecastMethodLabel(%q) = %q, want %q", method, got, want)
		}
	}
	// Only the retired engines carry a v1 quantity projection worth showing.
	for _, m := range []string{
		omsapi.ForecastMethodProphet,
		omsapi.ForecastMethodHoltWinters,
		omsapi.ForecastMethodFallback,
	} {
		if !isLegacyForecastMethod(m) {
			t.Errorf("isLegacyForecastMethod(%q) = false, want true", m)
		}
	}
	for _, m := range []string{
		omsapi.ForecastMethodRestockInterval,
		omsapi.ForecastMethodInsufficientHistory,
		"neuralnet",
		"",
	} {
		if isLegacyForecastMethod(m) {
			t.Errorf("isLegacyForecastMethod(%q) = true, want false", m)
		}
	}
}
