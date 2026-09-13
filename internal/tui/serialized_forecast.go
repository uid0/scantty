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
	terminalWidth  int

	// List-mode selection state. The forecast rows are 2–3 lines each, so the
	// visible window is packed by the lines each row really draws (see
	// forecastWindow), the same variable-height windowing ListScreen uses.
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
		s.terminalWidth = m.Width
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
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
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

// forecastWindow is the rows [from, to) the list window holds with the cursor on
// the pane: packed by the lines each row REALLY draws, into the budget
// proseFlatListFrame draws against.
//
// IT USED TO COUNT A COST PER ROW — two lines, three with a stockout date — under
// a flat two-row footer, and both halves were wrong about the frame. The footer
// is a folded record now, two or three rows at 80 columns, so a budget that
// assumed one row of hint assembled a frame taller than the pane and clampToBox,
// which drops from the BOTTOM, took the keys. And the item name is an OMS
// CharField that stores a newline as sent, so a row the cost table called two
// lines could draw forty. Asking the rendered rows answers both, and asking
// proseFlatListBudget with the same head the frame draws means the pager's
// windowSize and the drawn window cannot disagree about what a screenful is.
func (s *SerializedForecastScreen) forecastWindow() (from, to int) {
	budget := proseFlatListBudget(s.listHead(), s.terminalHeight, s.paneCells(), s.listBar(true))
	return proseLineWindow(proseRowHeights(s.listRows()), s.cursor, s.windowStart, budget)
}

// computeWindowSize is how many rows the window around the cursor holds — the
// step pgup/pgdn take.
func (s *SerializedForecastScreen) computeWindowSize() int {
	from, to := s.forecastWindow()
	return to - from
}

// scrollIntoView fits the window START and its SIZE together, for the reason
// ItemSuppliersScreen.scrollIntoView gives: how many rows fit depends on which
// row the window starts at.
func (s *SerializedForecastScreen) scrollIntoView() {
	from, to := s.forecastWindow()
	s.windowStart, s.windowSize = from, to-from
}

// ---------------------------------------------------------------------------
// Views
// ---------------------------------------------------------------------------

func (s *SerializedForecastScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Loading forecast…", s.paneCells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.paneCells(), s.proseBar())
	}
	if s.mode == forecastModeDetail {
		return s.viewDetail()
	}
	return s.viewList()
}

// paneCells is the width this screen folds its bar against.
func (s *SerializedForecastScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// listBar is the list's bar: the cursor vocabulary where there is a second row,
// `enter` where there is a row to open, and the filter, reload and way out.
//
// `w` IS NAMED ON THE EMPTY UNFILTERED LIST TOO, where the literal it replaced
// named only `r` and `esc`. The toggle is not a no-op there — it swaps to the
// low-stock view and reloads — so a bar leaving it off was a key acting unnamed
// on the very frame an operator most wants to change what they are looking at.
func (s *SerializedForecastScreen) listBar(moves bool) proseBar {
	out := proseNavCursor(moves)
	if len(s.rows) > 0 {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter detail"})
	}
	return append(out, s.lowStockToggle(), proseBarRefresh, proseBarEsc)
}

// lowStockToggle is `w`, which flips the low-stock filter and reloads — read by
// the list's bar and the load bar alike.
func (s *SerializedForecastScreen) lowStockToggle() proseBarItem {
	if s.lowOnly {
		return proseBarItem{Keys: []string{"w"}, Hint: "w show all"}
	}
	return proseBarItem{Keys: []string{"w"}, Hint: "w low-stock only"}
}

// detailBar is the per-row detail's bar. The detail claims raw input, so its
// own switch answers `esc` AND `backspace` — proseBarBack — and the scroller's
// Handle binds the movement vocabulary, named only where the body overflows.
func (s *SerializedForecastScreen) detailBar(scrolls bool) proseBar {
	raw := "r show raw"
	if s.showRaw {
		raw = "r hide raw"
	}
	return append(proseNavScroll(scrolls), proseBarItem{Keys: []string{"r"}, Hint: raw}, proseBarBack)
}

// proseBar is the bar this screen is DRAWING, for whichever surface is up. A
// load in flight or failed draws loadBar's.
func (s *SerializedForecastScreen) proseBar() proseBar {
	switch {
	case s.loading || s.loadErr != "":
		return s.loadBar()
	case s.mode == forecastModeDetail:
		return proseScrollBar(s.detail, s.terminalHeight, s.paneCells(), s.detailBar)
	}
	return s.listBar(listNavMoves(len(s.rows)))
}

// loadBar is the bar while a forecast load is out or has failed — what the key
// switch still answers with no rows drawn (prose_bar.go carries the defect and
// the decision). `w` still flips the filter and RELOADS from here, which from a
// failed load is a recovery under the other filter rather than a key acting on
// nothing. `enter` is not named: on a row a refresh kept it opens a detail this
// frame does not draw.
func (s *SerializedForecastScreen) loadBar() proseBar {
	return proseBar{s.lowStockToggle(), proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

// listHead is the lines the list opens with — the title, the summary and a
// blank — each ending in a newline, as proseFlatListFrame counts them.
func (s *SerializedForecastScreen) listHead() string {
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
	summary := fmt.Sprintf("%d item(s)", len(s.rows))
	if lowCount > 0 {
		summary += " · " + StyleStatusWarn.Render(fmt.Sprintf("%d need reorder", lowCount))
	}
	return StyleTitle.Render("Consumption forecast") + "  " +
		StyleMuted.Render(fmt.Sprintf("(%s)", scope)) + "\n" +
		StyleMuted.Render(summary) + "\n\n"
}

// listRows is every row as it will be drawn, without its trailing newline.
func (s *SerializedForecastScreen) listRows() []string {
	rows := make([]string, len(s.rows))
	for i := range s.rows {
		rows[i] = strings.TrimSuffix(s.renderRow(i, i == s.cursor), "\n")
	}
	return rows
}

func (s *SerializedForecastScreen) viewList() string {
	cells := s.paneCells()
	if len(s.rows) == 0 {
		empty := "No active serialized items to forecast."
		if s.lowOnly {
			empty = "Nothing at or below its reorder point. 🎉"
		}
		return s.listHead() + StyleMuted.Render(empty) + "\n\n" + s.proseBar().render(cells)
	}
	return proseFlatListFrame(s.listHead(), s.listRows(), s.cursor, &s.windowStart,
		s.terminalHeight, cells, s.listBar(true), s.proseBar())
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
	return proseScrollFrame(s.detail, s.terminalHeight, s.paneCells(), s.detailBar)
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
