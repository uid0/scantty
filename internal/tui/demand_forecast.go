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

// DemandForecastScreen renders the demand forecast for non-serialized items
// (GET reports/inventory/demand_forecast/) and its notify-set sibling
// (…/reorder_alerts/) as one scrollable, row-selectable report — the
// non-serialized twin of SerializedForecastScreen, whose layout and key map it
// mirrors deliberately so the two forecasts read the same.
//
// The forecast is a restock INTERVAL, not a usage rate: the backend measures
// how often an item is actually bought and projects when it is due again, so
// the columns are Item · Method · Cadence · Next due · Days-until-due · Status
// rather than the units-per-day and reorder-point numbers the retired v1
// engine produced.
//
// Two views, one screen (`a` swaps between them, and each has its own Reports
// menu entry):
//
//   - forecast — every item with a stored forecast, most-urgent-first. `w`
//     narrows to the items the model says are due to reorder.
//   - alerts   — the notify set: opted-in items (the item form's "ML reorder
//     alerts" toggle) that are due to reorder. Server-filtered, so `w` is a
//     no-op here.
//
// Enter opens a per-row detail with every field the row carries plus a raw-JSON
// dump (r), derived from the already-fetched row — no extra endpoint. esc
// returns to the list with the cursor preserved.
//
// Both endpoints read STORED rows written by the nightly forecasting task, so
// an empty list means "not forecast yet", not "nothing due" — the empty states
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
	terminalWidth  int

	// List-mode selection state. Rows are 2–3 lines each, so the visible
	// window is packed by the lines each row really draws, as in
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
		s.terminalWidth = m.Width
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
	case "pgdown":
		s.cursor += s.windowSize
		if s.cursor >= len(s.rows) {
			s.cursor = len(s.rows) - 1
		}
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "pgup":
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

// forecastWindow is the rows [from, to) the list window holds with the cursor on
// the pane, packed by the lines each row really draws into the budget
// proseFlatListFrame draws against. SerializedForecastScreen.forecastWindow
// carries why a per-row cost under a flat footer could not survive the folded
// bar or a stored newline in an item name.
func (s *DemandForecastScreen) forecastWindow() (from, to int) {
	budget := proseFlatListBudget(s.listHead(), s.terminalHeight, s.paneCells(), s.listBar(true))
	return proseLineWindow(proseRowHeights(s.listRows()), s.cursor, s.windowStart, budget)
}

// computeWindowSize is how many rows the window around the cursor holds — the
// step pgup/pgdn take.
func (s *DemandForecastScreen) computeWindowSize() int {
	from, to := s.forecastWindow()
	return to - from
}

// scrollIntoView fits the window start and its size together.
func (s *DemandForecastScreen) scrollIntoView() {
	from, to := s.forecastWindow()
	s.windowStart, s.windowSize = from, to-from
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

// listHead is the lines the list opens with — the title, the summary and a
// blank — each ending in a newline, as proseFlatListFrame counts them.
func (s *DemandForecastScreen) listHead() string {
	due, noCadence := 0, 0
	for _, r := range s.rows {
		if r.NeedsReorder {
			due++
		}
		if r.AvgIntervalDays == nil {
			noCadence++
		}
	}

	title, scope := "Demand forecast", "all forecast items"
	switch {
	case s.alerts:
		title, scope = "Reorder alerts", "notify set — opted-in & due"
	case s.lowOnly:
		scope = "due to reorder only"
	}
	summary := fmt.Sprintf("%d item(s)", len(s.rows))
	if due > 0 {
		summary += " · " + StyleStatusWarn.Render(fmt.Sprintf("%d due to reorder", due))
	}
	if noCadence > 0 {
		summary += fmt.Sprintf(" · %d without a cadence", noCadence)
	}
	return StyleTitle.Render(title) + "  " + StyleMuted.Render(fmt.Sprintf("(%s)", scope)) + "\n" +
		StyleMuted.Render(summary) + "\n\n"
}

// listRows is every row as it will be drawn, without its trailing newline.
func (s *DemandForecastScreen) listRows() []string {
	rows := make([]string, len(s.rows))
	for i := range s.rows {
		rows[i] = strings.TrimSuffix(s.renderRow(i, i == s.cursor), "\n")
	}
	return rows
}

func (s *DemandForecastScreen) viewList() string {
	cells := s.paneCells()
	if len(s.rows) == 0 {
		return s.listHead() + StyleMuted.Render(s.emptyMessage()) + "\n\n" + s.proseBar().render(cells)
	}
	return proseFlatListFrame(s.listHead(), s.listRows(), s.cursor, &s.windowStart,
		s.terminalHeight, cells, s.listBar(true), s.proseBar())
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
		return "Nothing is due to reorder. 🎉"
	}
	return "No stored forecasts yet.\n" +
		"Rows appear once the nightly forecasting run has measured a restock cadence for an item."
}

// paneCells is the width this screen folds its bar against.
func (s *DemandForecastScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// listBar is the list's bar: the cursor vocabulary where there is a second row,
// `enter` where there is a row to open, the view's filter and swap keys, and the
// reload and way out.
//
// `w` IS OFF THE ALERTS VIEW because it does nothing there — the notify set is
// already server-filtered, and the arm returns before touching anything — so
// naming it would be the other half of the rule.
func (s *DemandForecastScreen) listBar(moves bool) proseBar {
	out := proseNavCursor(moves)
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter detail"})
	}
	switch {
	case s.alerts:
		out = append(out, proseBarItem{Keys: []string{"a"}, Hint: "a full forecast"})
	case s.lowOnly:
		out = append(out, proseBarItem{Keys: []string{"w"}, Hint: "w show all"},
			proseBarItem{Keys: []string{"a"}, Hint: "a alerts"})
	default:
		out = append(out, proseBarItem{Keys: []string{"w"}, Hint: "w due only"},
			proseBarItem{Keys: []string{"a"}, Hint: "a alerts"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// detailBar is the per-row detail's bar; see SerializedForecastScreen.detailBar.
func (s *DemandForecastScreen) detailBar(scrolls bool) proseBar {
	raw := "r show raw"
	if s.showRaw {
		raw = "r hide raw"
	}
	return append(proseNavScroll(scrolls), proseBarItem{Keys: []string{"r"}, Hint: raw}, proseBarBack)
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up. A
// load in flight or failed is nil: those frames are literals, as on every
// earlier recipe.
func (s *DemandForecastScreen) proseBar() proseBar {
	switch {
	case s.loading || s.loadErr != "":
		return nil
	case s.mode == forecastModeDetail:
		return proseScrollBar(s.detail, s.terminalHeight, s.paneCells(), s.detailBar)
	}
	return s.listBar(listNavMoves(len(s.rows)))
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

	// Method · cadence · next due · days-until-due. A row with no measurable
	// cadence says why instead of printing a projection it doesn't have; for
	// an insufficient-history row that reason IS the method, so it isn't
	// repeated.
	var meta []string
	switch {
	case r.AvgIntervalDays != nil:
		meta = append(meta,
			forecastMethodLabel(r.Method),
			"every ~"+trimFloat(*r.AvgIntervalDays)+"d",
			"next "+fcOrDashPtr(r.PredictedNextReorderDate),
			forecastDuePhrase(r.DaysUntilDue),
		)
	case r.Method == omsapi.ForecastMethodInsufficientHistory:
		meta = append(meta, noCadenceReason(r.Method))
	default:
		meta = append(meta, forecastMethodLabel(r.Method), noCadenceReason(r.Method))
	}
	b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")

	// The history the cadence was measured from, when there is any — an item
	// bought exactly once still has a last restock, and showing it is how a
	// warden tells "never bought" from "bought once".
	if r.LastRestockDate != nil {
		extra := "last restock " + *r.LastRestockDate
		extra += fmt.Sprintf(" · %d interval(s)", r.IntervalSamples)
		if r.LeadTimeDays != nil {
			extra += fmt.Sprintf(" · lead %dd", *r.LeadTimeDays)
		}
		b.WriteString("    " + StyleMuted.Render(extra) + "\n")
	}
	return b.String()
}

func (s *DemandForecastScreen) viewDetail() string {
	return proseScrollFrame(s.detail, s.terminalHeight, s.paneCells(), s.detailBar)
}

// renderDetail draws every field the row carries, grouped into Item / Restock
// interval / Reorder decision / Model sections — plus the retired v1 quantity
// projection for the pre-v2 rows that actually hold one — and, when showRaw is
// on, the record as pretty-printed JSON so a warden can inspect exactly what
// the model produced.
func (s *DemandForecastScreen) renderDetail() string {
	r := s.detailRow
	var b strings.Builder

	b.WriteString(StyleTitle.Render(fcOrDash(r.ItemName)))
	switch {
	case r.NeedsReorder:
		b.WriteString("  " + StyleStatusWarn.Render("REORDER · "+forecastDuePhrase(r.DaysUntilDue)))
	case r.AvgIntervalDays == nil:
		b.WriteString("  " + StyleMuted.Render(noCadenceReason(r.Method)))
	default:
		b.WriteString("  " + StyleStatusOK.Render("not due yet"))
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

	b.WriteString(StyleTitle.Render("Restock interval") + "\n")
	if r.AvgIntervalDays != nil {
		b.WriteString(fcField("Cadence", "every ~"+trimFloat(*r.AvgIntervalDays)+" days"))
	} else {
		b.WriteString(fcField("Cadence", "— ("+noCadenceReason(r.Method)+")"))
	}
	b.WriteString(fcField("Intervals measured", fmt.Sprintf("%d", r.IntervalSamples)))
	b.WriteString(fcField("Last restock", fcOrDashPtr(r.LastRestockDate)))
	b.WriteString(fcField("Predicted next reorder", fcOrDashPtr(r.PredictedNextReorderDate)))
	if r.DaysUntilDue != nil {
		b.WriteString(fcField("Days until due", trimFloat(*r.DaysUntilDue)+" d ("+forecastDuePhrase(r.DaysUntilDue)+")"))
	} else {
		b.WriteString(fcField("Days until due", "— (no due date predicted)"))
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Reorder decision") + "\n")
	b.WriteString(fcField("Needs reorder", fcYesNo(r.NeedsReorder)))
	if r.LeadTimeDays != nil {
		b.WriteString(fcField("Lead time", fmt.Sprintf("%d d", *r.LeadTimeDays)))
	} else {
		b.WriteString(fcField("Lead time", "— (unknown)"))
	}
	b.WriteString(fcField("Available at generation", fmt.Sprintf("%d", r.AvailableAtGeneration)))
	b.WriteString(StyleMuted.Render(
		"Flagged once the due date falls inside the lead time — stock on hand is informational here.") + "\n")
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Model") + "\n")
	b.WriteString(fcField("Method", forecastMethodLabel(r.Method)))
	b.WriteString(fcField("Model version", fcOrDash(r.ModelVersion)))
	generated := "—"
	if !r.GeneratedAt.IsZero() {
		generated = r.GeneratedAt.Local().Format("2006-01-02 15:04")
	}
	b.WriteString(fcField("Generated at", generated))

	// Only a pre-v2 row carries a real quantity projection; on a v2 row these
	// columns are 0/null, so showing them would invent numbers the interval
	// model never produced.
	if isLegacyForecastMethod(r.Method) {
		b.WriteString("\n")
		b.WriteString(StyleTitle.Render("Retired v1 projection") + "\n")
		b.WriteString(fcField("Horizon", fcIntPtr(r.HorizonDays, " days")))
		b.WriteString(fcField("Predicted daily demand", fcFloatPtr(r.PredictedDailyDemand, "/day")))
		b.WriteString(fcField("Horizon demand", fcFloatPtr(r.HorizonDemand, "")))
		b.WriteString(fcField("Horizon demand (upper band)", fcFloatPtr(r.HorizonDemandUpper, "")))
		b.WriteString(fcField("Days until stockout", fcFloatPtr(r.DaysUntilStockout, " d")))
		b.WriteString(fcField("Projected stockout date", fcOrDashPtr(r.ProjectedStockoutDate)))
		b.WriteString(fcField("Predictive reorder point", fcIntPtr(r.PredictiveReorderPoint, "")))
		b.WriteString(fcField("Safety stock", fcIntPtr(r.SafetyStock, "")))
	}

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
	case omsapi.ForecastMethodRestockInterval:
		return "Restock interval"
	case omsapi.ForecastMethodInsufficientHistory:
		return "Insufficient history"
	case omsapi.ForecastMethodProphet:
		return "Prophet"
	case omsapi.ForecastMethodHoltWinters:
		return "Holt-Winters seasonal"
	case omsapi.ForecastMethodFallback:
		return "statistical fallback"
	}
	return fcOrDash(method)
}

// isLegacyForecastMethod reports whether a row came from the retired v1
// usage-rate engine. Those rows are the only ones whose quantity projection
// holds real numbers — v2 writes that whole group as 0/null.
func isLegacyForecastMethod(method string) bool {
	switch method {
	case omsapi.ForecastMethodProphet,
		omsapi.ForecastMethodHoltWinters,
		omsapi.ForecastMethodFallback:
		return true
	}
	return false
}

// noCadenceReason explains an absent cadence. Fewer than two purchases leaves
// no gap to average, which is exactly what the insufficient-history method
// records; a v1 row simply never measured one.
func noCadenceReason(method string) string {
	if isLegacyForecastMethod(method) {
		return "no cadence recorded"
	}
	return "not enough purchase history"
}

// forecastDuePhrase humanises days_until_due the same way the backend's
// reorder-alert digest does, so the notification a warden gets and the screen
// they open next use the same words. Truncating toward zero matches the
// digest's int() and keeps "due in 0d" reading as "due today".
func forecastDuePhrase(days *float64) string {
	if days == nil {
		return "no due date"
	}
	switch d := int(*days); {
	case d < 0:
		return fmt.Sprintf("overdue by %dd", -d)
	case d == 0:
		return "due today"
	default:
		return fmt.Sprintf("due in %dd", d)
	}
}

// Pointer-aware field renderers: every nullable forecast value is a pointer so
// null stays distinct from zero, and each renders "—" rather than a misleading
// 0 when the backend sent null. suffix is appended only to a real value.
func fcOrDashPtr(s *string) string {
	if s == nil {
		return "—"
	}
	return fcOrDash(*s)
}

func fcIntPtr(n *int, suffix string) string {
	if n == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *n) + suffix
}

func fcFloatPtr(f *float64, suffix string) string {
	if f == nil {
		return "—"
	}
	return trimFloat(*f) + suffix
}
