// MaintenanceRollupScreen — the maintenance ROLLUP: everything open, what it has
// cost, and every scheduled PM item, on one read-only sheet.
//
// TUI counterpart to the web's Maintenance page (MaintenanceDashboardPage.tsx),
// which reads `maintenance/dashboard/` and `maintenance/active/` together and
// offers no actions — every row links out, and nothing on the page writes. The
// sheet keeps the web's five sections in the web's order:
//
//	Active maintenance       work orders, and asset / location problems nobody
//	                         has promoted yet — with the web's kind filter (f)
//	Maintenance cost         today · this week · this month · this year · all time
//	Scheduled PM             every active item with an interval, soonest first
//	Unscheduled / open       work orders with no PM schedule behind them
//	Cost by asset            the trailing 90 days
//
// THE COST PERIODS ARE CALENDAR PERIODS AND THE DUE LISTS ARE NOT. `this week`
// here runs from Monday (UTC) and counts only COMPLETED work orders, while the
// PM due screen's "due this week" is a rolling seven days that includes the
// past. The two sheets use the same words for different windows because the
// server does; the cost block's heading says which one it is so nobody reads a
// cost against the due list.
//
// It is a scrolled sheet rather than a list because nothing on it acts on a row:
// the web's links open detail pages this terminal reaches from the work-order
// list and the asset screens, and a cursor that could only be moved would be a
// key that changes nothing but a highlight.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// rollupFilters are the web's Active Maintenance chips, in its order. "" is All.
var rollupFilters = []struct{ kind, label string }{
	{"", "All"},
	{omsapi.ActiveKindWorkOrder, "Work orders"},
	{omsapi.ActiveKindAssetProblem, "Asset problems"},
	{omsapi.ActiveKindLocationProblem, "Location problems"},
}

type MaintenanceRollupScreen struct {
	deps      Deps
	dashboard *omsapi.MaintenanceDashboard
	active    *omsapi.ActiveMaintenance
	filter    int
	loading   bool
	loadErr   string
	scroller  *TextScroller

	terminalWidth  int
	terminalHeight int
}

type maintenanceRollupLoadedMsg struct {
	dashboard *omsapi.MaintenanceDashboard
	active    *omsapi.ActiveMaintenance
	err       error
}

func NewMaintenanceRollupScreen(deps Deps) *MaintenanceRollupScreen {
	return &MaintenanceRollupScreen{deps: deps, loading: true, scroller: NewTextScroller(defaultDetailHeight)}
}

func (s *MaintenanceRollupScreen) Title() string { return "Maintenance rollup" }

func (s *MaintenanceRollupScreen) Init() tea.Cmd { return s.load() }

// load reads both halves, and either failing fails the sheet — the web's page
// shows one error for the pair, and a rollup missing its open work would read
// as a shop with nothing open.
func (s *MaintenanceRollupScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		dash, err := deps.OMS.GetMaintenanceDashboard(ctx)
		if err != nil {
			return maintenanceRollupLoadedMsg{err: err}
		}
		active, err := deps.OMS.ListActiveMaintenance(ctx)
		if err != nil {
			return maintenanceRollupLoadedMsg{err: err}
		}
		return maintenanceRollupLoadedMsg{dashboard: dash, active: active}
	}
}

func (s *MaintenanceRollupScreen) cells() int { return proseBarCells(s.terminalWidth) }

// sync writes the body for the pane the terminal really gave. Every line is
// clipped rather than folded, so the line COUNT does not depend on the width and
// the scroll offset means the same thing across a resize.
func (s *MaintenanceRollupScreen) sync() {
	if s.dashboard != nil && s.active != nil {
		s.scroller.Set(s.renderBody(s.cells()))
	}
}

func (s *MaintenanceRollupScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		s.sync()
		proseSizeScroller(s.scroller, s.terminalHeight, s.cells(), s.bar)
		return s, nil
	case maintenanceRollupLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.dashboard, s.active = m.dashboard, m.active
		s.sync()
		return s, nil
	case tea.KeyMsg:
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "f":
			s.filter = (s.filter + 1) % len(rollupFilters)
			s.sync()
			s.scroller.Top()
		}
	}
	return s, nil
}

// bar names every key that acts on the sheet: the scroll vocabulary where the
// body overflows, the filter, the refresh and the way back.
func (s *MaintenanceRollupScreen) bar(scrolls bool) proseBar {
	next := rollupFilters[(s.filter+1)%len(rollupFilters)].label
	return append(proseNavScroll(scrolls),
		proseBarItem{Keys: []string{"f"}, Hint: "f show " + strings.ToLower(next)},
		proseBarRefresh, proseBarEsc)
}

// proseBar is the bar this sheet is DRAWING, in every state.
func (s *MaintenanceRollupScreen) proseBar() proseBar {
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	s.sync()
	return proseScrollBar(s.scroller, s.terminalHeight, s.cells(), s.bar)
}

// loadBar is the bar while a load is out or has failed: the reload and the way
// back. The filter is not named and is ignored there — it would change a section
// the frame does not draw.
func (s *MaintenanceRollupScreen) loadBar() proseBar {
	return proseBar{proseBarReloadFor(s.loadErr != ""), proseBarEsc}
}

func (s *MaintenanceRollupScreen) View() string {
	if s.loading {
		return proseLoadingFrame("Reading the maintenance rollup…", s.cells(), s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, s.cells(), s.proseBar())
	}
	s.sync()
	return proseScrollFrame(s.scroller, s.terminalHeight, s.cells(), s.bar)
}

// rollupLine is one row of the sheet: an IDENTIFIER that abbreviates with the
// cut marked, and a FACT that never gives, reserved (in cells) before the
// identifier is clipped. The identifier is FLATTENED first: every line here is
// one row of the scroller, so a stored newline would be a row the offset
// arithmetic never counted. Where the pane cannot hold the fact beside even one
// cell of identifier, the fact is drawn alone and clipped with its cut marked.
func rollupLine(indent, ident, fact string, cells int) string {
	ident = jdeStatusOneLine(ident)
	if fact == "" {
		return indent + pickerClip(ident, cells-len(indent))
	}
	room := cells - len(indent) - 2 - lipgloss.Width(fact)
	if room < 1 {
		return indent + pickerClip(fact, cells-len(indent))
	}
	return indent + pickerClip(ident, room) + "  " + fact
}

func (s *MaintenanceRollupScreen) activeRows() []omsapi.ActiveMaintenanceRow {
	if s.active == nil {
		return nil
	}
	kind := rollupFilters[s.filter].kind
	if kind == "" {
		return s.active.Results
	}
	var out []omsapi.ActiveMaintenanceRow
	for _, r := range s.active.Results {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

var rollupKindLabel = map[string]string{
	omsapi.ActiveKindWorkOrder:       "Work order",
	omsapi.ActiveKindAssetProblem:    "Asset problem",
	omsapi.ActiveKindLocationProblem: "Location problem",
}

func (s *MaintenanceRollupScreen) renderBody(cells int) string {
	var lines []string
	add := func(l string) { lines = append(lines, l) }
	heading := func(text string) { add(StyleTitle.Render(pickerClip(text, cells))) }

	rows := s.activeRows()
	heading(fmt.Sprintf("Active maintenance · %s (%d)", rollupFilters[s.filter].label, len(rows)))
	if len(rows) == 0 {
		add(StyleMuted.Render(pickerClip("  No active maintenance items.", cells)))
	}
	for _, r := range rows {
		label := rollupKindLabel[r.Kind]
		if label == "" {
			label = r.Kind
		}
		status := r.StatusDisplay
		if r.Severity != nil && *r.Severity != "" {
			status += " · " + *r.Severity
		}
		add(rollupLine("  ", r.ShortID+" "+label, status, cells))
		add(StyleMuted.Render(rollupLine("    ", r.Title, "", cells)))
		where := "—"
		switch {
		case r.AssetName != nil:
			where = *r.AssetName
		case r.LocationName != nil:
			where = *r.LocationName
		}
		fact := "opened " + r.OpenedAt.Local().Format("2006-01-02")
		if !r.DueDate.IsZero() {
			fact += " · due " + r.DueDate.String()
		}
		add(StyleMuted.Render(rollupLine("    ", where, fact, cells)))
	}

	add("")
	heading("Maintenance cost (completed work orders, calendar periods)")
	p := s.dashboard.Costs.PerPeriod
	for _, c := range []struct {
		label string
		value omsapi.DecimalString
	}{
		{"Today", p.Today}, {"This week", p.ThisWeek}, {"This month", p.ThisMonth},
		{"This year", p.ThisYear}, {"All time", p.AllTime},
	} {
		add(rollupLine("  ", c.label, rollupMoney(c.value), cells))
	}

	add("")
	pm := s.dashboard.ScheduledPM
	heading(fmt.Sprintf("Scheduled PM (%d)", len(pm)))
	if len(pm) == 0 {
		add(StyleMuted.Render(pickerClip("  No scheduled maintenance.", cells)))
	}
	for _, r := range pm {
		badge := "—"
		switch {
		case r.IsOverdue && r.DaysUntil != nil:
			badge = StyleStatusError.Render(fmt.Sprintf("%dd overdue", -*r.DaysUntil))
		case r.IsOverdue:
			badge = StyleStatusError.Render("Overdue")
		case r.DaysUntil != nil:
			badge = fmt.Sprintf("%dd", *r.DaysUntil)
		}
		add(rollupLine("  ", r.Title, badge, cells))
		next, last := "never done", "last —"
		if r.NextDue != nil {
			next = "next " + r.NextDue.Local().Format("2006-01-02")
		}
		if r.LastCompletedAt != nil {
			last = "last " + r.LastCompletedAt.Local().Format("2006-01-02")
		}
		add(StyleMuted.Render(rollupLine("    ", r.AssetName,
			fmt.Sprintf("every %dd · %s · %s", r.IntervalDays, next, last), cells)))
	}

	add("")
	un := s.dashboard.Unscheduled
	heading(fmt.Sprintf("Unscheduled / open problems (%d)", len(un)))
	if len(un) == 0 {
		add(StyleMuted.Render(pickerClip("  No open problems.", cells)))
	}
	for _, r := range un {
		add(rollupLine("  ", r.ShortID, r.Status, cells))
		add(StyleMuted.Render(rollupLine("    ", r.Problem, "", cells)))
		add(StyleMuted.Render(rollupLine("    ", r.AssetName, "opened "+r.OpenedAt.Local().Format("2006-01-02"), cells)))
	}

	add("")
	by := s.dashboard.Costs.ByAsset
	heading("Cost by asset (last 90 days)")
	if len(by) == 0 {
		add(StyleMuted.Render(pickerClip("  No maintenance cost recorded in the last 90 days.", cells)))
	}
	for _, r := range by {
		add(rollupLine("  ", r.AssetName,
			fmt.Sprintf("%s · %dd down", rollupMoney(r.TotalCost), r.DaysInMaintenance90Days), cells))
	}
	return strings.Join(lines, "\n")
}

// rollupMoney is a dashboard figure as dollars, and — rather than an empty cell
// that reads as zero — a dash where the server sent nothing parseable.
func rollupMoney(d omsapi.DecimalString) string {
	if m := formatMoney(d); m != "" {
		return m
	}
	return "—"
}
