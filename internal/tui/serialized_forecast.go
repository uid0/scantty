package tui

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// SerializedForecastScreen renders the serialized-component consumption
// forecast (GET reports/inventory/serialized_forecast/) as a read-only,
// scrollable report: one entry per active serialized item with its depletion
// rate, days-until-stockout and reorder point, most-urgent-first. `w` toggles
// the low-stock-only view (items at/below their reorder point).
type SerializedForecastScreen struct {
	deps           Deps
	rows           []omsapi.ComponentForecastRow
	lowOnly        bool
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int
}

type serializedForecastLoadedMsg struct {
	rows []omsapi.ComponentForecastRow
	err  error
}

func NewSerializedForecastScreen(deps Deps) *SerializedForecastScreen {
	return &SerializedForecastScreen{
		deps:     deps,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *SerializedForecastScreen) Title() string { return "Serialized forecast" }

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
		return s, nil
	case serializedForecastLoadedMsg:
		s.loading = false
		s.rows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		s.scroller.Set(s.renderBody())
		s.scroller.Top()
		return s, nil
	case tea.KeyMsg:
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "w":
			s.lowOnly = !s.lowOnly
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		}
	}
	return s, nil
}

func (s *SerializedForecastScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading forecast…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	toggle := "w show low-stock only"
	if s.lowOnly {
		toggle = "w show all"
	}
	hint := "j/k scroll · pgup/pgdn page · " + toggle + " · r refresh · esc back"
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
}

func (s *SerializedForecastScreen) renderBody() string {
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
		} else {
			b.WriteString(StyleMuted.Render("No active serialized items to forecast."))
		}
		return b.String()
	}

	for _, r := range s.rows {
		marker := "  · "
		if r.NeedsReorder {
			marker = StyleStatusWarn.Render("  ● ")
		}
		title := marker + r.ItemName
		if r.SKU != "" {
			title += " " + StyleMuted.Render("("+r.SKU+")")
		}
		if r.NeedsReorder {
			title += " " + StyleStatusWarn.Render("LOW")
		}
		b.WriteString(title + "\n")

		meta := []string{
			fmt.Sprintf("stock %d", r.AvailableStock),
			fmt.Sprintf("~%s/day", trimFloat(r.AvgDailyUse)),
		}
		if r.DaysUntilStockout != nil {
			meta = append(meta, fmt.Sprintf("%s to stockout", trimFloat(*r.DaysUntilStockout)+"d"))
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
	}

	return b.String()
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
