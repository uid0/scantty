package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestLoadInventoryItems_MetricsLine drives the inventory list loader against a
// stub OMS: it must request ?with_metrics=1, render a bold Q's & Costs metrics
// line (with the SKU cell) for an item that carries metrics, and fall back to
// the plain SKU/stock subtitle for one that doesn't.
func TestLoadInventoryItems_MetricsLine(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[
			{"id":"a1","name":"Widget","sku":"W-1","current_stock":5,
			 "metrics":{"current_stock":5,"quantity_on_order":0,"quantity_available":5.0,
			   "quantity_committed":0.0,"quantity_in_transit":0,"reorder_point":3,
			   "lead_time_days":7,"unit_cost":"11.22","cost_trend":"up"}},
			{"id":"a2","name":"Gadget","sku":"G-9","current_stock":2}
		]}`))
	}))
	defer srv.Close()

	page, err := inventoryItemPage(context.Background(), Deps{OMS: omsapi.New(srv.URL)}, nil)
	if err != nil {
		t.Fatalf("inventoryItemPage: %v", err)
	}
	rows := page.rows
	if !strings.Contains(query, "with_metrics=1") {
		t.Fatalf("list request should carry with_metrics=1, got %q", query)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}

	// Item with metrics: a MetricsLine with the SKU cell and bold labels, and no
	// plain subtitle (the metrics line replaces it).
	withM := rows[0]
	if withM.MetricsLine == "" {
		t.Fatalf("item with metrics should have a MetricsLine")
	}
	if withM.Subtitle != "" {
		t.Errorf("metrics row should replace the subtitle, got %q", withM.Subtitle)
	}
	if !strings.Contains(withM.MetricsLine, StyleMetricLabel.Render("QOH")) {
		t.Errorf("MetricsLine should bold the QOH label: %q", withM.MetricsLine)
	}
	if !strings.Contains(withM.MetricsLine, StyleMetricLabel.Render("SKU")) || !strings.Contains(withM.MetricsLine, "W-1") {
		t.Errorf("list MetricsLine should keep the SKU cell: %q", withM.MetricsLine)
	}

	// Item without metrics: no MetricsLine, degrade to the SKU/stock subtitle.
	noM := rows[1]
	if noM.MetricsLine != "" {
		t.Errorf("item without metrics should have no MetricsLine: %q", noM.MetricsLine)
	}
	if noM.Subtitle != "SKU G-9 · stock 2" {
		t.Errorf("fallback subtitle = %q, want %q", noM.Subtitle, "SKU G-9 · stock 2")
	}
}

// TestInventoryDetail_FrozenHeader confirms the detail's View pins the three
// header lines (name, full SKU/ID, metrics row) ABOVE the scrolling body, that
// the body scrolls independently beneath them, and that the metrics row in the
// header carries no redundant SKU cell (de-dup with the identity line).
func TestInventoryDetail_FrozenHeader(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "abc")
	s.loading = false
	s.terminalHeight = 30
	s.item = &omsapi.Item{
		ID:   "abc",
		Name: "Frozen Widget",
		SKU:  "FW-100",
		// A long description so the scrolling body overflows the viewport and the
		// "↑ more above" indicator appears once scrolled — proving the body moves
		// independently of the pinned header.
		Description: strings.TrimRight(strings.Repeat("detail body line\n", 40), "\n"),
	}
	s.metrics = &omsapi.ItemMetrics{
		CurrentStock: ccPtr(5),
		ReorderPoint: ccPtr(3),
		UnitCost:     omsapi.DecimalString("11.22"),
		CostTrend:    "up",
	}
	s.scroller.Set(s.renderBody())

	// The header has exactly three lines: name / SKU+ID / metrics.
	header := s.renderHeader()
	if n := strings.Count(header, "\n") + 1; n != 3 {
		t.Fatalf("frozen header should be 3 lines, got %d:\n%q", n, header)
	}
	if !strings.Contains(header, "Frozen Widget") {
		t.Errorf("header line 1 should be the name: %q", header)
	}
	if !strings.Contains(header, "SKU FW-100 · ID abc") {
		t.Errorf("header line 2 should be the full SKU/ID: %q", header)
	}
	// De-dup: the metrics row in the header must not repeat a "SKU:" cell.
	if strings.Contains(header, "SKU:") {
		t.Errorf("detail metrics row should not repeat a SKU cell: %q", header)
	}
	if !strings.Contains(header, StyleMetricLabel.Render("QOH")) {
		t.Errorf("header metrics row should bold the QOH label: %q", header)
	}

	// The View pins the header at the top, then the body below it. The header's
	// first line is the very first thing rendered and stays put when we scroll.
	view := s.View()
	if !strings.HasPrefix(view, s.renderHeader()) {
		t.Errorf("View should start with the frozen header:\n%q", view)
	}

	// Scroll the body down; the header (name/SKU) must still lead the view.
	s.scroller.ScrollDown(3)
	scrolled := s.View()
	if !strings.HasPrefix(scrolled, s.renderHeader()) {
		t.Errorf("frozen header should stay pinned after scrolling:\n%q", scrolled)
	}
	// The body region really did move (overflow indicator appears once scrolled).
	if !strings.Contains(scrolled, "↑ more above") {
		t.Errorf("body should scroll independently beneath the header:\n%q", scrolled)
	}
	// The whole rendered screen must stay within the body budget so the frozen
	// header + scroller + footer don't push the frame past the terminal.
	if got := lipgloss.Height(scrolled); got > screenBodyHeight(s.terminalHeight) {
		t.Errorf("rendered %d rows, exceeds body budget %d", got, screenBodyHeight(s.terminalHeight))
	}
}
