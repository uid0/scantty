package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// AnalyticsPulseScreen renders the OMS "Analytics Pulse" page as tabular data.
// The web page draws a budget gauge, a WO-volume line chart, and a category-
// spend bar chart — but every one is backed by plain rows/scalars, so a
// terminal can surface the underlying numbers as tables. It's permission-gated
// (IsAnalyticsViewer: staff / SIG-admin); a 403 renders a clean notice instead
// of an error. Read-only; everything is one cached /pulse/ fetch.
type AnalyticsPulseScreen struct {
	deps           Deps
	pulse          *omsapi.AnalyticsPulse
	loading        bool
	loadErr        string
	forbidden      bool
	scroller       *TextScroller
	terminalHeight int
}

type analyticsPulseLoadedMsg struct {
	pulse *omsapi.AnalyticsPulse
	err   error
}

func NewAnalyticsPulseScreen(deps Deps) *AnalyticsPulseScreen {
	return &AnalyticsPulseScreen{deps: deps, loading: true, scroller: NewTextScroller(defaultDetailHeight)}
}

func (s *AnalyticsPulseScreen) Title() string { return "Analytics Pulse" }

// HandlesKey claims G (global = categories) so scroll-to-bottom works, and esc
// so the screen returns to the Reports hub.
func (s *AnalyticsPulseScreen) HandlesKey(key string) bool { return key == "G" || key == "esc" }

func (s *AnalyticsPulseScreen) Init() tea.Cmd { return s.load() }

func (s *AnalyticsPulseScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		p, err := deps.OMS.GetAnalyticsPulse(ctx, "", "", "")
		return analyticsPulseLoadedMsg{pulse: p, err: err}
	}
}

func (s *AnalyticsPulseScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
		return s, nil
	case analyticsPulseLoadedMsg:
		s.loading = false
		s.forbidden = false
		s.loadErr = ""
		if m.err != nil {
			var apiErr *omsapi.APIError
			if errors.As(m.err, &apiErr) && apiErr.IsForbidden() {
				s.forbidden = true
			} else {
				s.loadErr = m.err.Error()
			}
			return s, nil
		}
		s.pulse = m.pulse
		s.scroller.Set(s.renderPulse())
		s.scroller.Top()
		return s, nil
	case tea.KeyMsg:
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "esc", "backspace":
			return s, SwitchTo(WSReports, NewReportsScreen(s.deps))
		case "r":
			s.loading = true
			s.loadErr = ""
			s.forbidden = false
			return s, s.load()
		}
	}
	return s, nil
}

func (s *AnalyticsPulseScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading analytics pulse…")
	}
	if s.forbidden {
		return StyleStatusWarn.Render("Analytics is staff-only.") + "\n\n" +
			StyleMuted.Render("The pulse report requires the analytics-viewer permission\n(staff or a SIG admin). Ask an administrator for access.") +
			"\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	return s.scroller.View() + "\n\n" + StyleMuted.Render("j/k scroll · pgup/pgdn page · r refresh · esc back")
}

// renderPulse builds the full scrollable body: a scalar value-summary block,
// the monthly-budget line, then one table per aggregation.
func (s *AnalyticsPulseScreen) renderPulse() string {
	p := s.pulse
	var b strings.Builder

	period := orDash(p.Summary.PeriodStart) + " → " + orDash(p.Summary.PeriodEnd)
	b.WriteString(StyleTitle.Render("Analytics Pulse") + "  " + StyleMuted.Render(period) + "\n\n")

	// Value summary — scalar KPI block as a 2-column table.
	sumRows := [][]string{
		{"Total value to makerspace", fmtMoneyStr(p.Summary.TotalValueToMakerspace)},
		{"Internal net value", fmtMoneyStr(p.Summary.InternalNetValue)},
		{"Internal WOs completed", itoa(p.Summary.InternalCompletedCount)},
		{"Internal est. external cost", fmtMoneyStr(p.Summary.InternalEstimatedExternalCost)},
		{"Internal est. internal cost", fmtMoneyStr(p.Summary.InternalEstimatedInternalCost)},
		{"External WOs closed", itoa(p.Summary.ExternalClosedCount)},
		{"External actual cost", fmtMoneyStr(p.Summary.ExternalActualCost)},
		{"External estimated cost", fmtMoneyStr(p.Summary.ExternalEstimatedCost)},
	}
	b.WriteString(s.section("Value summary", []reportColumn{{"Metric", alignLeft}, {"Value", alignRight}}, sumRows))

	// Monthly budget line (the web budget gauge is scalar: spent / budget / %).
	b.WriteString(StyleTitle.Render("Monthly budget") + "\n")
	if strings.TrimSpace(p.MonthlyBudget) == "" {
		b.WriteString(StyleMuted.Render("  Not set (settings.MONTHLY_BUDGET_TOTAL unset).") + "\n")
	} else {
		line := "  Spent " + fmtMoneyStr(p.Summary.ExternalActualCost) + " / " + fmtMoneyStr(p.MonthlyBudget)
		if spent, ok1 := parseMoney(p.Summary.ExternalActualCost); ok1 {
			if budget, ok2 := parseMoney(p.MonthlyBudget); ok2 && budget != 0 {
				line += fmt.Sprintf(" (%s%%)", trimFloat(spent/budget*100))
			}
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")

	// WO volume trend (trailing 12 months) — 2-col time-series table.
	volRows := make([][]string, len(p.WOVolumeTrend))
	for i, r := range p.WOVolumeTrend {
		volRows[i] = []string{dateOnly(r.BucketStart), itoa(r.WOCount)}
	}
	b.WriteString(s.section("WO volume trend (trailing 12 months)",
		[]reportColumn{{"Period", alignLeft}, {"WOs", alignRight}}, volRows))

	// Top contributors.
	topRows := make([][]string, len(p.TopUsers))
	for i, u := range p.TopUsers {
		name := u.FullName
		if name == "" {
			name = u.Username
		}
		topRows[i] = []string{orDash(name), itoa(u.WOCount)}
	}
	b.WriteString(s.section("Top contributors",
		[]reportColumn{{"Member", alignLeft}, {"Completed WOs", alignRight}}, topRows))

	// Equipment utilization.
	utilRows := make([][]string, len(p.Utilization))
	for i, u := range p.Utilization {
		utilRows[i] = []string{orDash(u.AssetName), orDash(u.Category), orDash(u.Status), itoa(u.HoursUsed), itoa(u.CompletedWOCount)}
	}
	b.WriteString(s.section("Equipment utilization",
		[]reportColumn{{"Asset", alignLeft}, {"Category", alignLeft}, {"Status", alignLeft}, {"Hours", alignRight}, {"WOs", alignRight}}, utilRows))

	// Category spend (money as Decimal-strings).
	spendRows := make([][]string, len(p.CategorySpend))
	for i, c := range p.CategorySpend {
		spendRows[i] = []string{orDash(c.CategoryName), fmtMoneyStr(c.InternalEstimated), fmtMoneyStr(c.ExternalEstimated), fmtMoneyStr(c.ExternalActual), itoa(c.InternalWOCount), itoa(c.ExternalWOCount)}
	}
	b.WriteString(s.section("Category spend",
		[]reportColumn{{"Category", alignLeft}, {"Int est", alignRight}, {"Ext est", alignRight}, {"Ext actual", alignRight}, {"Int WOs", alignRight}, {"Ext WOs", alignRight}}, spendRows))

	// Maintenance forecast.
	fcRows := make([][]string, len(p.MaintenanceForecast))
	for i, f := range p.MaintenanceForecast {
		fcRows[i] = []string{orDash(f.AssetName), orDash(f.Category), itoa(f.HoursUsed), intOrDash(f.IntervalHours), intOrDash(f.IntervalDays), intOrDash(f.DaysSinceLastWO), orDash(f.DueReason)}
	}
	b.WriteString(s.section("Maintenance forecast",
		[]reportColumn{{"Asset", alignLeft}, {"Category", alignLeft}, {"Hours", alignRight}, {"Int h", alignRight}, {"Int d", alignRight}, {"Since d", alignRight}, {"Due", alignLeft}}, fcRows))

	return strings.TrimRight(b.String(), "\n")
}

// section renders a titled table; when there are no rows it prints a muted
// placeholder instead so an empty aggregation never looks broken.
func (s *AnalyticsPulseScreen) section(title string, cols []reportColumn, rows [][]string) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render(title) + "\n")
	if len(rows) == 0 {
		b.WriteString(StyleMuted.Render("  (none)") + "\n\n")
		return b.String()
	}
	header, body := reportTableLines(cols, rows)
	b.WriteString("  " + header + "\n")
	for _, line := range body {
		b.WriteString("  " + line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// parseMoney parses a Decimal-as-string ("1250.50") to a float for the budget
// percentage; ok is false on blank/garbage so the caller can skip the ratio.
func parseMoney(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
