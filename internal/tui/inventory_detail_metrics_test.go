package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

func ccPtr[T any](v T) *T { return &v }

// runeKey (single-rune KeyMsg) is defined in po_actions_test.go; reuse it.
func ccEnterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func ccEscKey() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

// ccDrainCmd executes a (possibly batched) command and returns every leaf
// message it produces, so a test can feed async follow-ups (e.g. the full-item
// re-fetch a cycle count triggers) back into the model.
func ccDrainCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	switch m := cmd().(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, ccDrainCmd(c)...)
		}
		return out
	case nil:
		return nil
	default:
		return []tea.Msg{m}
	}
}

// TestFormatItemMetricsRow_Alignment renders the same layout with small and
// large magnitudes and asserts the columns stay put: every label lands at the
// same offset and the Cost decimal points line up (fixed-width, right-aligned).
func TestFormatItemMetricsRow_Alignment(t *testing.T) {
	mk := func(stock, oo int, av, com float64, transit, rp int, lead float64, cost, trend string) *omsapi.ItemMetrics {
		return &omsapi.ItemMetrics{
			CurrentStock:      ccPtr(stock),
			QuantityOnOrder:   ccPtr(oo),
			QuantityAvailable: ccPtr(av),
			QuantityCommitted: ccPtr(com),
			QuantityInTransit: ccPtr(transit),
			ReorderPoint:      ccPtr(rp),
			LeadTimeDays:      ccPtr(lead),
			UnitCost:          omsapi.DecimalString(cost),
			CostTrend:         trend,
		}
	}
	// Same SKU so the leading cell is byte-identical; only the numbers differ.
	const sku = "WIDGET-0001"
	small := formatItemMetricsRow(mk(5, 0, 5, 0, 0, 3, 7, "11.22", "up"), sku, metricsRowOpts{withSKU: true})
	big := formatItemMetricsRow(mk(1234, 56, 1178, 12, 9, 800, 14, "9876.54", "down"), sku, metricsRowOpts{withSKU: true})

	for _, label := range []string{"QOH:", "QOO:", "QA:", "QC:", "QIT:", "RP:", "Lead:", "Cost:"} {
		if i, j := strings.Index(small, label), strings.Index(big, label); i != j {
			t.Errorf("label %q misaligned: small@%d big@%d\nsmall=%q\nbig=  %q", label, i, j, small, big)
		}
	}
	// Cost is the only cell with a decimal point; both are formatted to 2 dp and
	// right-aligned, so the points share a column.
	if i, j := strings.LastIndex(small, "."), strings.LastIndex(big, "."); i != j {
		t.Errorf("cost decimal points misaligned: small@%d big@%d", i, j)
	}
	// Trend arrows.
	if !strings.HasSuffix(small, "↑") {
		t.Errorf("up trend should end with ↑: %q", small)
	}
	if !strings.HasSuffix(big, "↓") {
		t.Errorf("down trend should end with ↓: %q", big)
	}
	// Values appear.
	if !strings.Contains(small, "$11.22") || !strings.Contains(big, "$9876.54") {
		t.Errorf("cost value missing:\nsmall=%q\nbig=%q", small, big)
	}
}

// TestFormatItemMetricsRow_Nulls confirms nil metrics render as "-" with no
// dollar amount and no trend arrow (graceful degradation).
func TestFormatItemMetricsRow_Nulls(t *testing.T) {
	row := formatItemMetricsRow(&omsapi.ItemMetrics{CostTrend: "no_history"}, "", metricsRowOpts{withSKU: true})
	if strings.Contains(row, "$") {
		t.Errorf("nil unit_cost should render '-', not a dollar amount: %q", row)
	}
	if strings.ContainsAny(row, "↑↓") {
		t.Errorf("no_history trend should have no arrow: %q", row)
	}
	if !strings.Contains(row, "-") {
		t.Errorf("expected dashes for null metrics: %q", row)
	}
	// The SKU cell also dashes when empty.
	if !strings.HasPrefix(row, "SKU: -") {
		t.Errorf("empty SKU should render 'SKU: -': %q", row)
	}
}

// TestFormatItemMetricsRow_SKUTail shows the trailing SKU chars, ellipsised when
// longer than the tail width.
func TestFormatItemMetricsRow_SKUTail(t *testing.T) {
	long := formatItemMetricsRow(&omsapi.ItemMetrics{}, "SUPER-LONG-SKU-123456", metricsRowOpts{withSKU: true})
	if !strings.HasPrefix(long, "SKU: …123456") {
		t.Errorf("long SKU should show ellipsised tail: %q", long)
	}
	short := formatItemMetricsRow(&omsapi.ItemMetrics{}, "AB12", metricsRowOpts{withSKU: true})
	if !strings.HasPrefix(short, "SKU: AB12") {
		t.Errorf("short SKU should show verbatim: %q", short)
	}
}

// TestFormatItemMetricsRow_DetailNoSKU covers the DETAIL variant: no SKU cell
// (the detail's line-2 identity row already shows the full SKU), bold labels,
// and — crucially — bold must not shift the value columns. The visible width is
// compared against the plain no-SKU row: bold adds zero display width, so the
// two must match to the cell.
func TestFormatItemMetricsRow_DetailNoSKU(t *testing.T) {
	m := &omsapi.ItemMetrics{
		CurrentStock:      ccPtr(5),
		QuantityOnOrder:   ccPtr(0),
		QuantityAvailable: ccPtr(5.0),
		QuantityCommitted: ccPtr(0.0),
		QuantityInTransit: ccPtr(0),
		ReorderPoint:      ccPtr(3),
		LeadTimeDays:      ccPtr(7.0),
		UnitCost:          omsapi.DecimalString("11.22"),
		CostTrend:         "up",
	}
	row := formatItemMetricsRow(m, "WIDGET-0001", metricsRowOpts{boldLabels: true})

	// The detail row must NOT carry a SKU cell (de-dup with the identity line).
	if strings.Contains(row, "SKU") {
		t.Errorf("detail metrics row should have no SKU cell: %q", row)
	}
	// Bold labels: the bold-rendered "QOH"/"Cost" runs must be present.
	if !strings.Contains(row, StyleMetricLabel.Render("QOH")) {
		t.Errorf("expected a bold QOH label: %q", row)
	}
	if !strings.Contains(row, StyleMetricLabel.Render("Cost")) {
		t.Errorf("expected a bold Cost label: %q", row)
	}
	// The row starts at the first metric, not a SKU cell.
	if !strings.HasPrefix(row, StyleMetricLabel.Render("QOH")+": ") {
		t.Errorf("detail row should start with the bold QOH cell: %q", row)
	}
	// Bold adds no display width: the visible width equals the plain no-SKU row.
	plain := formatItemMetricsRow(m, "WIDGET-0001", metricsRowOpts{})
	if got, want := lipgloss.Width(row), lipgloss.Width(plain); got != want {
		t.Errorf("bold shifted the columns: bold width=%d plain width=%d\nbold =%q\nplain=%q", got, want, row, plain)
	}
}

// TestFormatItemMetricsRow_ListWithSKUBold covers the LIST variant: the SKU cell
// is kept (it identifies the item) and the labels are bold. Visible width must
// match the plain with-SKU row so bold hasn't disturbed the columns.
func TestFormatItemMetricsRow_ListWithSKUBold(t *testing.T) {
	m := &omsapi.ItemMetrics{
		CurrentStock: ccPtr(12),
		ReorderPoint: ccPtr(3),
		UnitCost:     omsapi.DecimalString("4.50"),
		CostTrend:    "flat",
	}
	// A short SKU (≤ the 6-char tail) renders verbatim, so it's checkable inline.
	row := formatItemMetricsRow(m, "BOLT", metricsRowOpts{withSKU: true, boldLabels: true})

	// The list keeps the SKU cell, bold.
	if !strings.HasPrefix(row, StyleMetricLabel.Render("SKU")+": ") {
		t.Errorf("list row should start with the bold SKU cell: %q", row)
	}
	if !strings.Contains(row, "BOLT") {
		t.Errorf("list row should show the SKU value: %q", row)
	}
	if !strings.Contains(row, StyleMetricLabel.Render("QOH")) {
		t.Errorf("expected a bold QOH label: %q", row)
	}
	plain := formatItemMetricsRow(m, "BOLT", metricsRowOpts{withSKU: true})
	if got, want := lipgloss.Width(row), lipgloss.Width(plain); got != want {
		t.Errorf("bold shifted the columns: bold width=%d plain width=%d", got, want)
	}
}

func TestMetricsCountedLine(t *testing.T) {
	cases := []struct {
		days *int
		want string
	}{
		{nil, "Counted: never"},
		{ccPtr(0), "Counted: today"},
		{ccPtr(1), "Counted: 1d ago"},
		{ccPtr(12), "Counted: 12d ago"},
	}
	for _, c := range cases {
		got := metricsCountedLine(&omsapi.Item{DaysSinceLastCount: c.days})
		if got != c.want {
			t.Errorf("days=%v → %q, want %q", c.days, got, c.want)
		}
	}
}

// TestCycleCountModal_Flow drives the three-step state machine with no OMS:
// open → digit gating → qty → reason move → notes → esc-cancel, plus empty-qty
// validation.
func TestCycleCountModal_Flow(t *testing.T) {
	s := NewInventoryDetailScreen(Deps{}, "abc")
	s.item = &omsapi.Item{ID: "abc", Name: "Widget", SKU: "W-1"}
	s.loading = false

	s.Update(runeKey('c'))
	if s.ccStep != ccStepQty {
		t.Fatalf("c should open the qty step, got %d", s.ccStep)
	}
	if !s.WantsRawInput() {
		t.Errorf("open modal should claim raw input")
	}
	if cycleCountReasons[s.ccReasonIx].Value != "miscounted" {
		t.Errorf("default reason = %q, want miscounted", cycleCountReasons[s.ccReasonIx].Value)
	}

	// Non-digit is ignored in the qty step; digits accumulate.
	s.Update(runeKey('a'))
	if s.ccQty.Value() != "" {
		t.Errorf("non-digit should be ignored, got %q", s.ccQty.Value())
	}
	s.Update(runeKey('1'))
	s.Update(runeKey('2'))
	if s.ccQty.Value() != "12" {
		t.Errorf("qty = %q, want 12", s.ccQty.Value())
	}

	s.Update(ccEnterKey())
	if s.ccStep != ccStepReason {
		t.Fatalf("enter should advance to reason, got %d", s.ccStep)
	}
	start := s.ccReasonIx
	s.Update(runeKey('j'))
	if s.ccReasonIx != start+1 {
		t.Errorf("j should advance reason: %d → %d", start, s.ccReasonIx)
	}
	s.Update(runeKey('k'))
	if s.ccReasonIx != start {
		t.Errorf("k should move reason back: → %d", s.ccReasonIx)
	}

	s.Update(ccEnterKey())
	if s.ccStep != ccStepNotes {
		t.Fatalf("enter should advance to notes, got %d", s.ccStep)
	}

	s.Update(ccEscKey())
	if s.ccStep != ccStepNone {
		t.Errorf("esc should cancel, step = %d", s.ccStep)
	}
	if s.WantsRawInput() {
		t.Errorf("raw input should be released after cancel")
	}

	// Empty qty does not advance.
	s.Update(runeKey('c'))
	s.Update(ccEnterKey())
	if s.ccStep != ccStepQty {
		t.Errorf("empty qty should stay on qty step, got %d", s.ccStep)
	}
	if s.ccErr == "" {
		t.Errorf("empty qty should set a validation error")
	}
}

// TestCycleCountModal_Submits drives the modal to submission against a stub
// server and verifies the request carries the collected qty/reason/notes and
// that the success message closes the modal and updates the item.
func TestCycleCountModal_Submits(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","name":"Widget","current_stock":8,
			"days_since_last_count":0}`))
	}))
	defer srv.Close()

	s := NewInventoryDetailScreen(Deps{OMS: omsapi.New(srv.URL)}, "abc")
	s.item = &omsapi.Item{ID: "abc", Name: "Widget", SKU: "W-1", Stock: 5}
	s.loading = false

	s.openCycleCount()
	s.Update(runeKey('8'))
	s.Update(ccEnterKey()) // → reason (miscounted default)
	s.Update(ccEnterKey()) // → notes
	s.Update(runeKey('o'))
	s.Update(runeKey('k'))
	_, cmd := s.Update(ccEnterKey()) // submit
	if cmd == nil {
		t.Fatal("submit should return a command")
	}
	if !s.ccPending {
		t.Errorf("submit should mark the modal pending")
	}
	msg := cmd()
	done, ok := msg.(cycleCountDoneMsg)
	if !ok {
		t.Fatalf("expected cycleCountDoneMsg, got %T", msg)
	}
	if done.err != nil {
		t.Fatalf("submit error: %v", done.err)
	}
	if body["counted_qty"].(float64) != 8 {
		t.Errorf("counted_qty = %v, want 8", body["counted_qty"])
	}
	if body["reason"] != "miscounted" {
		t.Errorf("reason = %v, want miscounted", body["reason"])
	}
	if body["notes"] != "ok" {
		t.Errorf("notes = %v, want ok", body["notes"])
	}

	// Feeding the success message back closes the modal and triggers a re-fetch
	// of the full item + metrics — the count response is a PARTIAL item, so the
	// screen re-loads rather than adopting it (which would blank name/SKU/etc.).
	// The stub answers the reload with current_stock:8.
	_, reload := s.Update(done)
	if s.ccStep != ccStepNone {
		t.Errorf("success should close the modal, step = %d", s.ccStep)
	}
	if reload == nil {
		t.Fatal("success should trigger a reload command")
	}
	for _, mm := range ccDrainCmd(reload) {
		s.Update(mm)
	}
	if s.item == nil || s.item.Stock != 8 {
		t.Errorf("item stock should refresh to 8 after re-fetch, got %v", s.item)
	}
}
