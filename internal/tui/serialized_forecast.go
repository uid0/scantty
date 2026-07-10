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

// SerializedForecastScreen renders the serialized-component consumption
// forecast (GET reports/inventory/serialized_forecast/) as a scrollable,
// row-selectable report: one entry per active serialized item with its
// depletion rate, days-until-stockout and reorder point, most-urgent-first.
// `w` toggles the low-stock-only view (items at/below their reorder point).
//
// Enter on the highlighted row opens a DETAIL view showing every field the
// ComponentForecastRow carries, plus a raw-JSON dump (r toggles it) so a warden
// can inspect exactly what drove the forecast. esc returns to the list with the
// cursor preserved. The detail is derived from the already-fetched row — no
// extra endpoint. Mirrors the OMS web SerializedForecastPanel's per-row drill-in.
type SerializedForecastScreen struct {
	deps           Deps
	rows           []omsapi.ComponentForecastRow
	lowOnly        bool
	loading        bool
	loadErr        string
	terminalHeight int

	// List-mode selection state. The forecast rows are 2–3 lines each, so the
	// visible window is sized against the actual per-row line cost (see
	// computeWindowSize), the same variable-height windowing ListScreen uses.
	cursor      int
	windowStart int
	windowSize  int

	// Detail-mode state. detailRow is a copy of the selected row so it stays
	// stable if the list reloads; showRaw gates the raw-JSON section.
	mode      forecastMode
	detailRow omsapi.ComponentForecastRow
	showRaw   bool
	detail    *TextScroller
}

type forecastMode int

const (
	forecastModeList forecastMode = iota
	forecastModeDetail
)

type serializedForecastLoadedMsg struct {
	rows []omsapi.ComponentForecastRow
	err  error
}

func NewSerializedForecastScreen(deps Deps) *SerializedForecastScreen {
	return &SerializedForecastScreen{
		deps:       deps,
		loading:    true,
		windowSize: listWindowSize,
		detail:     NewTextScroller(defaultDetailHeight),
	}
}

func (s *SerializedForecastScreen) Title() string {
	if s.mode == forecastModeDetail {
		name := s.detailRow.ItemName
		if name == "" {
			name = s.detailRow.SKU
		}
		if name != "" {
			return "Forecast: " + name
		}
	}
	return "Serialized forecast"
}

// WantsRawInput claims every keypress while the per-row detail view is open, so
// esc/r/j/k land here (esc returns to the list) instead of the root's global
// hotkeys. The list stays non-raw so workspace switching keeps working.
func (s *SerializedForecastScreen) WantsRawInput() bool { return s.mode == forecastModeDetail }

func (s *SerializedForecastScreen) Init() tea.Cmd { return s.load() }

func (s *SerializedForecastScreen) load() tea.Cmd {
	deps := s.deps
	lowOnly := s.lowOnly
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		var q url.Values
		if lowOnly {
			q = url.Values{"low_stock_only": []string{"true"}}
		}
		rows, err := deps.OMS.SerializedForecast(ctx, q)
		return serializedForecastLoadedMsg{rows: rows, err: err}
	}
}

func (s *SerializedForecastScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case serializedForecastLoadedMsg:
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

func (s *SerializedForecastScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		s.lowOnly = !s.lowOnly
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	}
	return s, nil
}

// openDetail snapshots the highlighted row into detail mode. Raw starts hidden;
// the cursor is left untouched so esc returns to exactly where we were.
func (s *SerializedForecastScreen) openDetail() {
	s.detailRow = s.rows[s.cursor]
	s.showRaw = false
	s.mode = forecastModeDetail
	s.detail.Set(s.renderDetail())
	s.detail.Top()
}

func (s *SerializedForecastScreen) updateDetail(m tea.KeyMsg) (Screen, tea.Cmd) {
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
// List-mode windowing (variable-height rows, mirrors ListScreen)
// ---------------------------------------------------------------------------

// rowLineCost is how many rendered lines a row occupies: a title line + a meta
// line, plus a third line when a projected-stockout date is present.
func (s *SerializedForecastScreen) rowLineCost(i int) int {
	if s.rows[i].ProjectedStockoutDate != "" {
		return 3
	}
	return 2
}

// computeWindowSize returns how many rows the current terminal can show,
// packing rows by their actual line cost from windowStart forward. Chrome
// reserved: a 3-row header (title + summary + blank), a 2-row footer (blank +
// hint) and 2 rows for the ↑/↓ overflow indicators.
func (s *SerializedForecastScreen) computeWindowSize() int {
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

func (s *SerializedForecastScreen) scrollIntoView() {
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

func (s *SerializedForecastScreen) View() string {
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

func (s *SerializedForecastScreen) viewList() string {
	var b strings.Builder

	lowCount := 0
	for _, r := range s.rows {
		if r.NeedsReorder {
			lowCount++
		}
	}

	scope := "all serialized items"
	if s.lowOnly {
		scope = "low-stock only"
	}
	b.WriteString(StyleTitle.Render("Consumption forecast"))
	b.WriteString("  " + StyleMuted.Render(fmt.Sprintf("(%s)", scope)) + "\n")
	summary := fmt.Sprintf("%d item(s)", len(s.rows))
	if lowCount > 0 {
		summary += " · " + StyleStatusWarn.Render(fmt.Sprintf("%d need reorder", lowCount))
	}
	b.WriteString(StyleMuted.Render(summary) + "\n\n")

	if len(s.rows) == 0 {
		if s.lowOnly {
			b.WriteString(StyleMuted.Render("Nothing at or below its reorder point. 🎉"))
			b.WriteString("\n\n" + StyleMuted.Render("w show all · r refresh · esc back"))
		} else {
			b.WriteString(StyleMuted.Render("No active serialized items to forecast."))
			b.WriteString("\n\n" + StyleMuted.Render("r refresh · esc back"))
		}
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

	toggle := "w low-stock only"
	if s.lowOnly {
		toggle = "w show all"
	}
	hint := "j/k move · pgup/pgdn page · enter detail · " + toggle + " · r refresh · esc back"
	b.WriteString("\n" + StyleMuted.Render(hint))
	return b.String()
}

func (s *SerializedForecastScreen) renderRow(i int, selected bool) string {
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
	title := prefix + dot + r.ItemName
	if r.SKU != "" {
		title += " " + StyleMuted.Render("("+r.SKU+")")
	}
	if r.NeedsReorder {
		title += " " + StyleStatusWarn.Render("LOW")
	}
	if selected {
		title = StyleSidebarItemActive.Render(title)
	}
	b.WriteString(title + "\n")

	meta := []string{fmt.Sprintf("on-hand %d", r.AvailableStock)}
	// When some units are installed in assets, show the on-shelf vs installed
	// split (op-0cd2). on_hand == available_stock, so on-shelf = on_hand −
	// installed stays correct even on an older backend (installed decodes 0).
	if r.Installed > 0 {
		meta = append(meta, fmt.Sprintf("%d on shelf · %d installed", r.AvailableStock-r.Installed, r.Installed))
	}
	meta = append(meta, fmt.Sprintf("~%s/day", trimFloat(r.AvgDailyUse)))
	if r.DaysUntilStockout != nil {
		meta = append(meta, trimFloat(*r.DaysUntilStockout)+"d to stockout")
	} else {
		meta = append(meta, "no depletion in window")
	}
	meta = append(meta, fmt.Sprintf("reorder@%d", r.ReorderPoint))
	if r.SerialTrackingMode != "" {
		meta = append(meta, r.SerialTrackingMode)
	}
	b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")

	if r.ProjectedStockoutDate != "" {
		extra := "projected stockout " + r.ProjectedStockoutDate
		if r.LeadTimeDays != nil {
			extra += fmt.Sprintf(" · lead %sd", trimFloat(*r.LeadTimeDays))
		}
		b.WriteString("    " + StyleMuted.Render(extra) + "\n")
	}
	return b.String()
}

func (s *SerializedForecastScreen) viewDetail() string {
	s.detail.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	raw := "r show raw"
	if s.showRaw {
		raw = "r hide raw"
	}
	hint := "j/k scroll · pgup/pgdn page · " + raw + " · esc back"
	return s.detail.View() + "\n\n" + StyleMuted.Render(hint)
}

// renderDetail draws every field the ComponentForecastRow carries, grouped into
// Item / Stock / Consumption sections, and — when showRaw is on — the record as
// pretty-printed JSON so a warden can inspect exactly what drove the forecast.
func (s *SerializedForecastScreen) renderDetail() string {
	r := s.detailRow
	var b strings.Builder

	b.WriteString(StyleTitle.Render(fcOrDash(r.ItemName)))
	if r.NeedsReorder {
		b.WriteString("  " + StyleStatusWarn.Render("LOW · needs reorder"))
	} else {
		b.WriteString("  " + StyleStatusOK.Render("stock OK"))
	}
	b.WriteString("\n")
	head := []string{}
	if r.SKU != "" {
		head = append(head, "SKU "+r.SKU)
	}
	if r.ItemID != "" {
		head = append(head, "ID "+r.ItemID)
	}
	if r.CategoryName != "" {
		head = append(head, r.CategoryName)
	}
	if len(head) > 0 {
		b.WriteString(StyleMuted.Render(strings.Join(head, " · ")) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Item") + "\n")
	b.WriteString(fcField("Item ID", fcOrDash(r.ItemID)))
	b.WriteString(fcField("Name", fcOrDash(r.ItemName)))
	b.WriteString(fcField("SKU", fcOrDash(r.SKU)))
	b.WriteString(fcField("Category", fcOrDash(r.CategoryName)))
	b.WriteString(fcField("Tracking mode", fcOrDash(r.SerialTrackingMode)))
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Stock") + "\n")
	b.WriteString(fcField("On-hand", fmt.Sprintf("%d", r.AvailableStock)))
	// Available (on shelf) = on_hand − installed. Computed from available_stock
	// (== on_hand) so it's correct even before op-0cd2 added the split fields
	// (installed decodes 0 → on-shelf == on-hand).
	b.WriteString(fcField("Available (on shelf)", fmt.Sprintf("%d", r.AvailableStock-r.Installed)))
	b.WriteString(fcField("Installed", fmt.Sprintf("%d", r.Installed)))
	b.WriteString(fcField("Current stock", fmt.Sprintf("%d", r.CurrentStock)))
	b.WriteString(fcField("Safety stock", fmt.Sprintf("%d", r.SafetyStock)))
	b.WriteString(fcField("Reorder point", fmt.Sprintf("%d", r.ReorderPoint)))
	b.WriteString(fcField("Needs reorder", fcYesNo(r.NeedsReorder)))
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Consumption & forecast") + "\n")
	b.WriteString(fcField("Window", fmt.Sprintf("%d days", r.WindowDays)))
	b.WriteString(fcField("Units depleted in window", fmt.Sprintf("%d", r.UnitsDepletedInWindow)))
	b.WriteString(fcField("Avg daily use", trimFloat(r.AvgDailyUse)+"/day"))
	if r.DaysUntilStockout != nil {
		b.WriteString(fcField("Days until stockout", trimFloat(*r.DaysUntilStockout)+" d"))
	} else {
		b.WriteString(fcField("Days until stockout", "— (no depletion in window)"))
	}
	b.WriteString(fcField("Projected stockout date", fcOrDash(r.ProjectedStockoutDate)))
	if r.LeadTimeDays != nil {
		b.WriteString(fcField("Lead time", trimFloat(*r.LeadTimeDays)+" d"))
	} else {
		b.WriteString(fcField("Lead time", "— (unknown)"))
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

// fcField renders a "Label: value" detail line with a muted label.
func fcField(label, value string) string {
	return StyleMuted.Render(label+": ") + value + "\n"
}

func fcOrDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func fcYesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// trimFloat renders a float without trailing zeros: 7.5 → "7.5", 3.0 → "3",
// 0.4286 → "0.43". Keeps the forecast rows compact on a shop-floor terminal.
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-0" {
		s = "0"
	}
	return s
}
