package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type DashboardScreen struct {
	deps     Deps
	summary  *omsapi.InventorySummary
	reorders []omsapi.ReorderRequest
	problems []omsapi.AssetProblem
	pulse    *omsapi.AnalyticsPulse
	loading  bool
	loadErr  string
}

type dashboardLoadedMsg struct {
	summary  *omsapi.InventorySummary
	reorders []omsapi.ReorderRequest
	problems []omsapi.AssetProblem
	pulse    *omsapi.AnalyticsPulse
	err      error
}

func NewDashboardScreen(deps Deps) *DashboardScreen {
	return &DashboardScreen{deps: deps, loading: true}
}

func (s *DashboardScreen) Title() string { return "Dashboard" }

func (s *DashboardScreen) Init() tea.Cmd { return s.load() }

func (s *DashboardScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		summary, err := deps.OMS.GetInventorySummary(ctx)
		out := dashboardLoadedMsg{summary: summary, err: err}
		if rq, rerr := deps.OMS.ListPendingReorders(ctx, nil); rerr == nil {
			out.reorders = rq
		}
		if pp, perr := deps.OMS.ListAssetProblems(ctx, nil); perr == nil && pp != nil {
			out.problems = pp.Results
		}
		// Analytics pulse is permission-gated (IsAnalyticsViewer); swallow
		// the error so non-analyst users still see the rest of the
		// dashboard. Backend caches the response, so the second-fetch
		// cost is one CPU-cheap cache lookup.
		if pulse, perr := deps.OMS.GetAnalyticsPulse(ctx, "", "", ""); perr == nil {
			out.pulse = pulse
		}
		return out
	}
}

func (s *DashboardScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case dashboardLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.summary = m.summary
		s.reorders = m.reorders
		s.problems = m.problems
		s.pulse = m.pulse
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		}
	}
	return s, nil
}

func (s *DashboardScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading dashboard…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	var b strings.Builder

	b.WriteString(StyleTitle.Render("Inventory") + "\n")
	if s.summary != nil {
		rows := [][2]string{
			{"Total items", fmt.Sprintf("%d", s.summary.TotalItems)},
			{"Low stock", fmt.Sprintf("%d", s.summary.LowStockCount)},
			{"Out of stock", fmt.Sprintf("%d", s.summary.OutOfStockCount)},
			{"Pending reorders", fmt.Sprintf("%d", s.summary.PendingReorders)},
		}
		for _, r := range rows {
			val := r[1]
			if r[0] == "Out of stock" && s.summary.OutOfStockCount > 0 {
				val = StyleStatusError.Render(r[1])
			} else if r[0] == "Low stock" && s.summary.LowStockCount > 0 {
				val = StyleStatusWarn.Render(r[1])
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", StyleMuted.Render(r[0]+":"), val))
		}
	} else {
		b.WriteString(StyleMuted.Render("  (no inventory summary available)") + "\n")
	}
	b.WriteString("\n")

	if len(s.reorders) > 0 {
		b.WriteString(StyleTitle.Render("Pending reorder requests") + "\n")
		limit := len(s.reorders)
		if limit > 5 {
			limit = 5
		}
		for _, r := range s.reorders[:limit] {
			b.WriteString(fmt.Sprintf("  · item %s × %d", r.Item, r.Quantity))
			if r.RequestedBy != "" {
				b.WriteString(" " + StyleMuted.Render("by "+r.RequestedBy))
			}
			if r.Priority != "" && r.Priority != "normal" {
				b.WriteString(" " + StyleMuted.Render("("+r.Priority+")"))
			}
			b.WriteString("\n")
		}
		if len(s.reorders) > 5 {
			b.WriteString(fmt.Sprintf("  %s %d more · press 3 for full queue\n",
				StyleMuted.Render("…"), len(s.reorders)-5))
		}
		b.WriteString("\n")
	}

	if len(s.problems) > 0 {
		open := 0
		for _, p := range s.problems {
			if p.Status == "reported" || p.Status == "in_progress" {
				open++
			}
		}
		if open > 0 {
			b.WriteString(StyleTitle.Render("Open asset problems") + "\n")
			shown := 0
			for _, p := range s.problems {
				if p.Status != "reported" && p.Status != "in_progress" {
					continue
				}
				if shown >= 5 {
					break
				}
				marker := "  ● "
				if p.Status == "in_progress" {
					marker = "  ○ "
				}
				name := p.AssetName
				if name == "" {
					name = "asset"
				}
				b.WriteString(marker + name + ": " + p.Description + "\n")
				shown++
			}
			if open > 5 {
				b.WriteString(fmt.Sprintf("  %s %d more\n", StyleMuted.Render("…"), open-5))
			}
			b.WriteString("\n")
		}
	}

	// Analytics pulse — show only when the user can see it (the dashboard
	// load swallows the 403 for non-analyst viewers).
	if s.pulse != nil {
		b.WriteString(StyleTitle.Render("Pulse · "+s.pulse.Summary.PeriodStart+" → "+s.pulse.Summary.PeriodEnd) + "\n")
		rows := [][2]string{
			{"Total value to makerspace", "$" + s.pulse.Summary.TotalValueToMakerspace},
			{"Internal WOs completed", fmt.Sprintf("%d", s.pulse.Summary.InternalCompletedCount)},
			{"External WOs closed", fmt.Sprintf("%d", s.pulse.Summary.ExternalClosedCount)},
		}
		if s.pulse.MonthlyBudget != "" {
			rows = append(rows, [2]string{"Monthly budget", "$" + s.pulse.MonthlyBudget})
		}
		for _, r := range rows {
			b.WriteString(fmt.Sprintf("  %s %s\n", StyleMuted.Render(r[0]+":"), r[1]))
		}
		if len(s.pulse.TopUsers) > 0 {
			b.WriteString("  " + StyleMuted.Render("Top users:") + "\n")
			limit := len(s.pulse.TopUsers)
			if limit > 3 {
				limit = 3
			}
			for _, u := range s.pulse.TopUsers[:limit] {
				name := u.FullName
				if name == "" {
					name = u.Username
				}
				b.WriteString(fmt.Sprintf("    · %s · %d WOs\n", name, u.WOCount))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(StyleMuted.Render("r refresh · esc back"))
	return b.String()
}
