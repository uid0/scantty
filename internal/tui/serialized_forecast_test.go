package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// sampleForecastRow is a fully-populated ComponentForecastRow (both nullable
// pointers set) used to exercise the detail render.
func sampleForecastRow() omsapi.ComponentForecastRow {
	days := 12.5
	lead := 7.0
	return omsapi.ComponentForecastRow{
		ItemID:                "it-1",
		ItemName:              "Nitrogen cylinder",
		SKU:                   "N2-CYL",
		CategoryName:          "Gases",
		SerialTrackingMode:    "consumable",
		AvailableStock:        4,
		CurrentStock:          6,
		WindowDays:            90,
		UnitsDepletedInWindow: 8,
		AvgDailyUse:           0.4286,
		DaysUntilStockout:     &days,
		ProjectedStockoutDate: "2026-07-20",
		LeadTimeDays:          &lead,
		SafetyStock:           2,
		ReorderPoint:          5,
		NeedsReorder:          true,
	}
}

// TestSerializedForecast_DetailRendersFields: the detail body labels and shows
// every field the row carries (identity, stock, consumption/forecast).
func TestSerializedForecast_DetailRendersFields(t *testing.T) {
	s := NewSerializedForecastScreen(Deps{})
	s.detailRow = sampleForecastRow()

	body := s.renderDetail()
	for _, want := range []string{
		"Nitrogen cylinder", "N2-CYL", "it-1", "Gases", "consumable",
		"needs reorder",
		"Available (on-hand)", "4",
		"Current stock", "6",
		"Safety stock", "Reorder point", "5",
		"Needs reorder", "yes",
		"Window", "90 days",
		"Units depleted in window", "8",
		"Avg daily use", "0.43/day",
		"Days until stockout", "12.5 d",
		"Projected stockout date", "2026-07-20",
		"Lead time", "7 d",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q:\n%s", want, body)
		}
	}
	// Raw is off by default: no raw JSON, but a hint that r reveals it.
	if strings.Contains(body, "\"item_id\"") {
		t.Errorf("raw JSON should be hidden until toggled:\n%s", body)
	}
	if !strings.Contains(body, "press r to show the raw JSON") {
		t.Errorf("detail should hint at the raw toggle when off:\n%s", body)
	}
}

// TestSerializedForecast_DetailNullableFields: when the nullable pointers are
// nil the detail renders explanatory placeholders, not a crash or "0".
func TestSerializedForecast_DetailNullableFields(t *testing.T) {
	row := sampleForecastRow()
	row.DaysUntilStockout = nil
	row.LeadTimeDays = nil
	row.ProjectedStockoutDate = ""
	s := NewSerializedForecastScreen(Deps{})
	s.detailRow = row

	body := s.renderDetail()
	for _, want := range []string{
		"no depletion in window", // days-until-stockout nil
		"unknown",                // lead-time nil
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing nil-placeholder %q:\n%s", want, body)
		}
	}
}

// TestSerializedForecast_RawToggle: r reveals the pretty-printed JSON record
// (with its wire field names) and hides it again.
func TestSerializedForecast_RawToggle(t *testing.T) {
	s := NewSerializedForecastScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.ComponentForecastRow{sampleForecastRow()}
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
		"\"item_id\"", "\"needs_reorder\"", "\"days_until_stockout\"",
		"\"projected_stockout_date\"", "\"avg_daily_use\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("raw body missing %q:\n%s", want, body)
		}
	}
	s.Update(mtKey("r")) // toggle raw off
	if s.showRaw {
		t.Fatalf("r should turn raw off")
	}
	if strings.Contains(s.renderDetail(), "\"item_id\"") {
		t.Errorf("raw JSON should be hidden after toggling off")
	}
}

// TestSerializedForecast_EnterOpensDetail_EscReturns: Enter on the highlighted
// row opens its detail; esc returns to the list with the cursor preserved.
func TestSerializedForecast_EnterOpensDetail_EscReturns(t *testing.T) {
	rows := []omsapi.ComponentForecastRow{
		{ItemID: "a", ItemName: "Alpha"},
		{ItemID: "b", ItemName: "Bravo"},
		{ItemID: "c", ItemName: "Charlie"},
	}
	s := NewSerializedForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = rows
	s.cursor = 2

	s.Update(mtKey("enter"))
	if s.mode != forecastModeDetail {
		t.Fatalf("enter should open detail mode, got %d", s.mode)
	}
	if s.detailRow.ItemID != "c" {
		t.Fatalf("detail should snapshot the highlighted row, got %q", s.detailRow.ItemID)
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

// TestSerializedForecast_RawInputGating: the list stays non-raw (so global
// hotkeys keep working); the detail view claims raw input so esc lands here.
func TestSerializedForecast_RawInputGating(t *testing.T) {
	s := NewSerializedForecastScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.ComponentForecastRow{sampleForecastRow()}
	if s.WantsRawInput() {
		t.Errorf("list mode should not claim raw input")
	}
	s.openDetail()
	if !s.WantsRawInput() {
		t.Errorf("detail mode should claim raw input")
	}
}

// TestSerializedForecast_ListRendersCursorAndRows: the list highlights the
// cursor row and shows each item's forecast line.
func TestSerializedForecast_ListRendersCursorAndRows(t *testing.T) {
	s := NewSerializedForecastScreen(Deps{})
	s.loading = false
	s.terminalHeight = 40
	s.rows = []omsapi.ComponentForecastRow{sampleForecastRow()}
	s.windowSize = s.computeWindowSize()

	out := s.View()
	for _, want := range []string{
		"Consumption forecast", "Nitrogen cylinder", "N2-CYL", "LOW",
		"1 need reorder", "enter detail", "▸ ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list view missing %q:\n%s", want, out)
		}
	}
}
