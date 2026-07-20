package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// DemandForecastScreen renders the ML demand forecast for non-serialized items
// (GET reports/inventory/demand_forecast/) and its notify-set sibling
// (…/reorder_alerts/) as one scrollable, row-selectable report — the
// non-serialized twin of SerializedForecastScreen, whose layout and key map it
// mirrors deliberately so the two forecasts read the same.
//
// Two views, one screen (`a` swaps between them, and each has its own Reports
// menu entry):
//
//   - forecast — every item with a stored forecast, most-urgent-first. `w`
//     narrows to the items the model flags for reorder.
//   - alerts   — the notify set: opted-in items (the item form's "ML reorder
//     alerts" toggle) that are due to reorder. Server-filtered, so `w` is a
//     no-op here.
//
// Enter opens a per-row detail with every field the row carries plus a raw-JSON
// dump (r), derived from the already-fetched row — no extra endpoint. esc
// returns to the list with the cursor preserved.
//
// Both endpoints read STORED rows written by the nightly forecasting task, so
// an empty list means "not forecast yet", not "no demand" — the empty states
// say so.
type DemandForecastScreen struct {
	deps Deps

	// alerts picks the endpoint: the notify set rather than the full forecast.
	alerts         bool
	rows           []omsapi.DemandForecastRow
	lowOnly        bool
	loading        bool
	loadErr        string
	terminalHeight int

	// List-mode selection state. Rows are 2–3 lines each, so the visible
	// window is sized against the actual per-row line cost, as in
	// SerializedForecastScreen.
	cursor      int
	windowStart int
	windowSize  int

	// Detail-mode state. detailRow is a copy of the selected row so it stays
	// stable if the list reloads; showRaw gates the raw-JSON section.
	mode      forecastMode
	detailRow omsapi.DemandForecastRow
	showRaw   bool
	detail    *TextScroller
}

type demandForecastLoadedMsg struct {
	rows []omsapi.DemandForecastRow
	err  error
}

// NewDemandForecastScreen opens the full demand forecast.
func NewDemandForecastScreen(deps Deps) *DemandForecastScreen {
	return &DemandForecastScreen{
		deps:       deps,
		loading:    true,
		windowSize: listWindowSize,
		detail:     NewTextScroller(defaultDetailHeight),
	}
}

// NewReorderAlertsScreen opens the same screen on the notify set — the
// opted-in items the forecast says are due to reorder.
func NewReorderAlertsScreen(deps Deps) *DemandForecastScreen {
	s := NewDemandForecastScreen(deps)
	s.alerts = true
	return s
}

func (s *DemandForecastScreen) Title() string {
	if s.mode == forecastModeDetail {
		name := s.detailRow.ItemName
		if name == "" {
			name = s.detailRow.SKU
		}
		if name != "" {
			return "Forecast: " + name
		}
	}
	if s.alerts {
		return "Reorder alerts"
	}
	return "Demand forecast"
}

// WantsRawInput claims every keypress while the per-row detail view is open, so
// esc/r/j/k land here instead of the root's global hotkeys. The list stays
// non-raw so workspace switching keeps working.
func (s *DemandForecastScreen) WantsRawInput() bool { return s.mode == forecastModeDetail }

// HandlesKey claims the forecast/alerts swap key, which would otherwise fire
// the global "a" (authorizations) before reaching this screen.
func (s *DemandForecastScreen) HandlesKey(key string) bool { return key == "a" }

func (s *DemandForecastScreen) Init() tea.Cmd { return s.load() }

func (s *DemandForecastScreen) load() tea.Cmd {
	deps := s.deps
	alerts := s.alerts
	lowOnly := s.lowOnly
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if alerts {
			rows, err := deps.OMS.ReorderAlerts(ctx)
			return demandForecastLoadedMsg{rows: rows, err: err}
		}
		var q url.Values
		if lowOnly {
			q = url.Values{"low_stock_only": []string{"true"}}
		}
		rows, err := deps.OMS.DemandForecast(ctx, q)
		return demandForecastLoadedMsg{rows: rows, err: err}
	}
}

// reload restarts the fetch, clearing the previous error and parking the cursor
// at the top — the row set changes wholesale when the view or filter flips.
func (s *DemandForecastScreen) reload() tea.Cmd {
	s.loading = true
	s.loadErr = ""
	return s.load()
}

func (s *DemandForecastScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case demandForecastLoadedMsg:
		s.loading = false
		s.rows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case tea.KeyMsg:
		if s.mode == forecastModeDetail {
			return s.updateDetail(m)
		}
		return s.updateList(m)
	}
	return s, nil
}

func (s *DemandForecastScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "j", "down":
		if s.cursor < len(s.rows)-1 {
			s.cursor++
			s.scrollIntoView()
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
			s.scrollIntoView()
		}
	case "ctrl+d", "pgdown":
		s.cursor += s.windowSize
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "ctrl+u", "pgup":
		s.cursor -= s.windowSize
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "g", "home":
		s.cursor = 0
		s.scrollIntoView()
	case "G", "end":
		s.cursor = len(s.rows) - 1
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "enter":
		if s.cursor >= 0 && s.cursor < len(s.rows) {
			s.openDetail()
		}
	case "w":
		// The alerts view is already server-filtered to flagged-and-due rows,
		// so the low-stock filter only applies to the full forecast.
		if s.alerts {
			return s, nil
		}
		s.lowOnly = !s.lowOnly
		s.cursor, s.windowStart = 0, 0
		return s, s.reload()
	case "a":
		s.alerts = !s.alerts
		s.cursor, s.windowStart = 0, 0
		return s, s.reload()
	case "r":
		return s, s.reload()
	}
	return s, nil
}

// openDetail snapshots the highlighted row into detail mode. Raw starts hidden;
// the cursor is left untouched so esc returns to exactly where we were.
func (s *DemandForecastScreen) openDetail() {
	s.detailRow = s.rows[s.cursor]
	s.showRaw = false
	s.mode = forecastModeDetail
	s.detail.Set(s.renderDetail())
	s.detail.Top()
}

func (s *DemandForecastScreen) updateDetail(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.detail.Handle(m) {
		return s, nil
	}
	switch m.String() {
	case "r":
		s.showRaw = !s.showRaw
		s.detail.Set(s.renderDetail())
		return s, nil
	case "esc", "backspace":
		s.mode = forecastModeList
		return s, nil
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// List-mode windowing (variable-height rows, mirrors SerializedForecastScreen)
// ---------------------------------------------------------------------------

// rowLineCost is how many rendered lines a row occupies: a title line + a meta
// line, plus a third line when a projected-stockout date is present.
func (s *DemandForecastScreen) rowLineCost(i int) int {
	if s.rows[i].ProjectedStockoutDate != "" {
		return 3
	}
	return 2
}

// computeWindowSize returns how many rows the current terminal can show,
// packing rows by their actual line cost from windowStart forward. Chrome
// reserved: a 3-row header (title + summary + blank), a 2-row footer (blank +
// hint) and 2 rows for the ↑/↓ overflow indicators.
func (s *DemandForecastScreen) computeWindowSize() int {
	const headerRows = 3
	const footerRows = 2
	const indicatorRows = 2

	avail := screenBodyHeight(s.terminalHeight) - headerRows - footerRows - indicatorRows
	if avail < 2 {
		avail = 2
	}
	if len(s.rows) == 0 {
		return avail
	}
	start := s.windowStart
	if start < 0 {
		start = 0
	}
	used, count := 0, 0
	for i := start; i < len(s.rows); i++ {
		cost := s.rowLineCost(i)
		if used+cost > avail {
			break
		}
		used += cost
		count++
	}
	if count < 1 {
		count = 1
	}
	return count
}

func (s *DemandForecastScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = listWindowSize
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if maxStart := len(s.rows) - s.windowSize; maxStart > 0 && s.windowStart > maxStart {
		s.windowStart = maxStart
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

func (s *DemandForecastScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading forecast…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.mode == forecastModeDetail {
		return s.viewDetail()
	}
	return s.viewList()
}

func (s *DemandForecastScreen) viewList() string {
	var b strings.Builder

	flagged := 0
	for _, r := range s.rows {
		if r.NeedsReorder {
			flagged++
		}
	}

	title, scope := "Demand forecast", "all forecast items"
	switch {
	case s.alerts:
		title, scope = "Reorder alerts", "notify set — opted-in & due"
	case s.lowOnly:
		scope = "flagged for reorder only"
	}
	b.WriteString(StyleTitle.Render(title))
	b.WriteString("  " + StyleMuted.Render(fmt.Sprintf("(%s)", scope)) + "\n")
	summary := fmt.Sprintf("%d item(s)", len(s.rows))
	if flagged > 0 {
		summary += " · " + StyleStatusWarn.Render(fmt.Sprintf("%d need reorder", flagged))
	}
	b.WriteString(StyleMuted.Render(summary) + "\n\n")

	if len(s.rows) == 0 {
		b.WriteString(StyleMuted.Render(s.emptyMessage()))
		b.WriteString("\n\n" + StyleMuted.Render(s.hint()))
		return b.String()
	}

	if s.windowSize <= 0 {
		s.windowSize = s.computeWindowSize()
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i, i == s.cursor))
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}

	b.WriteString("\n" + StyleMuted.Render(s.hint()))
	return b.String()
}

// emptyMessage explains WHY the list is empty — both endpoints read stored rows
// only, so "nothing here" usually means the nightly run hasn't produced any yet
// rather than that everything is well stocked.
func (s *DemandForecastScreen) emptyMessage() string {
	if s.alerts {
		return "No items are due to reorder. 🎉\n" +
			"The notify set only lists items with \"ML reorder alerts\" switched on in the item form."
	}
	if s.lowOnly {
		return "Nothing is flagged for reorder. 🎉"
	}
	return "No stored forecasts yet.\n" +
		"Rows appear once the nightly forecasting run has projected demand for an item."
}

func (s *DemandForecastScreen) hint() string {
	keys := []string{"j/k move", "pgup/pgdn page", "enter detail"}
	if len(s.rows) == 0 {
		keys = nil
	}
	if s.alerts {
		keys = append(keys, "a full forecast")
	} else {
		if s.lowOnly {
			keys = append(keys, "w show all")
		} else {
			keys = append(keys, "w flagged only")
		}
		keys = append(keys, "a alerts")
	}
	keys = append(keys, "r refresh", "esc back")
	return strings.Join(keys, " · ")
}

func (s *DemandForecastScreen) renderRow(i int, selected bool) string {
	r := s.rows[i]
	var b strings.Builder

	prefix := "  "
	if selected {
		prefix = "▸ "
	}
	dot := "· "
	if r.NeedsReorder {
		dot = StyleStatusWarn.Render("● ")
	}
	title := prefix + dot + fcOrDash(r.ItemName)
	if r.SKU != "" {
		title += " " + StyleMuted.Render("("+r.SKU+")")
	}
	if r.NeedsReorder {
		title += " " + StyleStatusWarn.Render("REORDER")
	}
	if selected {
		title = StyleSidebarItemActive.Render(title)
	}
	b.WriteString(title + "\n")

	meta := []string{
		fmt.Sprintf("avail %d", r.AvailableAtGeneration),
		"~" + trimFloat(r.PredictedDailyDemand) + "/day",
	}
	if r.DaysUntilStockout != nil {
		meta = append(meta, trimFloat(*r.DaysUntilStockout)+"d to stockout")
	} else {
		meta = append(meta, "no projected stockout")
	}
	meta = append(meta, fmt.Sprintf("reorder@%d", r.PredictiveReorderPoint))
	meta = append(meta, forecastMethodLabel(r.Method))
	b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")

	if r.ProjectedStockoutDate != "" {
		extra := "projected stockout " + r.ProjectedStockoutDate
		if r.LeadTimeDays != nil {
			extra += fmt.Sprintf(" · lead %dd", *r.LeadTimeDays)
		}
		extra += fmt.Sprintf(" · horizon %dd", r.HorizonDays)
		b.WriteString("    " + StyleMuted.Render(extra) + "\n")
	}
	return b.String()
}

func (s *DemandForecastScreen) viewDetail() string {
	s.detail.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	raw := "r show raw"
	if s.showRaw {
		raw = "r hide raw"
	}
	hint := "j/k scroll · pgup/pgdn page · " + raw + " · esc back"
	return s.detail.View() + "\n\n" + StyleMuted.Render(hint)
}

// renderDetail draws every field the row carries, grouped into Item / Demand
// projection / Reorder decision / Model sections, and — when showRaw is on —
// the record as pretty-printed JSON so a warden can inspect exactly what the
// model produced.
func (s *DemandForecastScreen) renderDetail() string {
	r := s.detailRow
	var b strings.Builder

	b.WriteString(StyleTitle.Render(fcOrDash(r.ItemName)))
	if r.NeedsReorder {
		b.WriteString("  " + StyleStatusWarn.Render("REORDER · projected to run out"))
	} else {
		b.WriteString("  " + StyleStatusOK.Render("stock OK"))
	}
	b.WriteString("\n")
	head := []string{}
	if r.SKU != "" {
		head = append(head, "SKU "+r.SKU)
	}
	if r.Item != "" {
		head = append(head, "ID "+r.Item)
	}
	if r.CategoryName != "" {
		head = append(head, r.CategoryName)
	}
	if len(head) > 0 {
		b.WriteString(StyleMuted.Render(strings.Join(head, " · ")) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Item") + "\n")
	b.WriteString(fcField("Item ID", fcOrDash(r.Item)))
	b.WriteString(fcField("Name", fcOrDash(r.ItemName)))
	b.WriteString(fcField("SKU", fcOrDash(r.SKU)))
	b.WriteString(fcField("Category", fcOrDash(r.CategoryName)))
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Demand projection") + "\n")
	b.WriteString(fcField("Horizon", fmt.Sprintf("%d days", r.HorizonDays)))
	b.WriteString(fcField("Predicted daily demand", trimFloat(r.PredictedDailyDemand)+"/day"))
	b.WriteString(fcField("Horizon demand", trimFloat(r.HorizonDemand)))
	b.WriteString(fcField("Horizon demand (upper band)", trimFloat(r.HorizonDemandUpper)))
	if r.DaysUntilStockout != nil {
		b.WriteString(fcField("Days until stockout", trimFloat(*r.DaysUntilStockout)+" d"))
	} else {
		b.WriteString(fcField("Days until stockout", "— (no depletion projected)"))
	}
	b.WriteString(fcField("Projected stockout date", fcOrDash(r.ProjectedStockoutDate)))
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Reorder decision") + "\n")
	b.WriteString(fcField("Available at generation", fmt.Sprintf("%d", r.AvailableAtGeneration)))
	b.WriteString(fcField("Predictive reorder point", fmt.Sprintf("%d", r.PredictiveReorderPoint)))
	b.WriteString(fcField("Safety stock", fmt.Sprintf("%d", r.SafetyStock)))
	if r.LeadTimeDays != nil {
		b.WriteString(fcField("Lead time", fmt.Sprintf("%d d", *r.LeadTimeDays)))
	} else {
		b.WriteString(fcField("Lead time", "— (unknown)"))
	}
	b.WriteString(fcField("Needs reorder", fcYesNo(r.NeedsReorder)))
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Model") + "\n")
	b.WriteString(fcField("Method", forecastMethodLabel(r.Method)))
	b.WriteString(fcField("Model version", fcOrDash(r.ModelVersion)))
	generated := "—"
	if !r.GeneratedAt.IsZero() {
		generated = r.GeneratedAt.Local().Format("2006-01-02 15:04")
	}
	b.WriteString(fcField("Generated at", generated))

	if s.showRaw {
		b.WriteString("\n")
		b.WriteString(StyleTitle.Render("Raw") + "\n")
		if raw, err := json.MarshalIndent(r, "", "  "); err != nil {
			b.WriteString(StyleStatusError.Render("marshal error: "+err.Error()) + "\n")
		} else {
			b.WriteString(StyleMuted.Render(string(raw)) + "\n")
		}
	} else {
		b.WriteString("\n" + StyleMuted.Render("press r to show the raw JSON record") + "\n")
	}

	return b.String()
}

// forecastMethodLabel renders the engine that produced a row. Unknown values
// (a newer backend engine) pass through as-is rather than showing a dash.
func forecastMethodLabel(method string) string {
	switch method {
	case omsapi.ForecastMethodProphet:
		return "Prophet"
	case omsapi.ForecastMethodHoltWinters:
		return "Holt-Winters seasonal"
	case omsapi.ForecastMethodFallback:
		return "statistical fallback"
	}
	return fcOrDash(method)
}
