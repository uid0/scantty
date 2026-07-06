package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ElectricalPanelsScreen lists every PowerPanel via
// /api/electrical/panels/. Layout mirrors ListScreen but is bespoke
// because the panel rows carry their own metadata (location, voltage,
// breaker count, needs-review flag) rather than going through the
// generic listRow shape.
type ElectricalPanelsScreen struct {
	deps           Deps
	panels         []omsapi.PowerPanel
	cursor         int
	windowStart    int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type electricalPanelsLoadedMsg struct {
	panels []omsapi.PowerPanel
	err    error
}

type electricalPanelDeletedMsg struct {
	err error
}

func NewElectricalPanelsScreen(deps Deps) *ElectricalPanelsScreen {
	return &ElectricalPanelsScreen{deps: deps, loading: true}
}

func (s *ElectricalPanelsScreen) Title() string { return "Electrical panels" }

// WantsRawInput claims every key only while the delete confirm is up (y/n/esc).
func (s *ElectricalPanelsScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims the action keys that collide with global hotkeys (n new,
// G bottom) so they reach this screen instead of the global nav switch.
func (s *ElectricalPanelsScreen) HandlesKey(key string) bool {
	return key == "n" || key == "G"
}

func (s *ElectricalPanelsScreen) Init() tea.Cmd { return s.load() }

func (s *ElectricalPanelsScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		panels, err := deps.OMS.ListPowerPanels(ctx)
		return electricalPanelsLoadedMsg{panels: panels, err: err}
	}
}

func (s *ElectricalPanelsScreen) windowSize() int {
	// Each row is 2 lines (header + meta). Footer = 2 (blank + hint).
	avail := screenBodyHeight(s.terminalHeight) - 2
	rows := avail / 2
	if rows < 2 {
		rows = 2
	}
	return rows
}

func (s *ElectricalPanelsScreen) scrollIntoView() {
	w := s.windowSize()
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+w {
		s.windowStart = s.cursor - w + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
}

func (s *ElectricalPanelsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.scrollIntoView()
		return s, nil
	case electricalPanelsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.panels = m.panels
		if s.cursor >= len(s.panels) {
			s.cursor = 0
		}
		s.scrollIntoView()
		return s, nil
	case electricalPanelDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("panel deleted", StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.panels)-1 {
				s.cursor++
				s.scrollIntoView()
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
				s.scrollIntoView()
			}
		case "g", "home":
			s.cursor = 0
			s.scrollIntoView()
		case "G", "end":
			s.cursor = len(s.panels) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "r":
			s.loading = true
			return s, s.load()
		case "n":
			return s, SwitchTo(WSFacilities, NewPowerPanelFormScreen(s.deps, 0))
		case "E":
			if p, ok := s.selectedPanel(); ok {
				return s, SwitchTo(WSFacilities, NewPowerPanelFormScreen(s.deps, p.ID))
			}
		case "x":
			if _, ok := s.selectedPanel(); ok {
				s.confirmingDelete = true
			}
		case "enter":
			if p, ok := s.selectedPanel(); ok {
				return s, SwitchTo(WSFacilities, NewElectricalPanelDetailScreen(s.deps, p.ID))
			}
		}
	}
	return s, nil
}

func (s *ElectricalPanelsScreen) selectedPanel() (omsapi.PowerPanel, bool) {
	if s.cursor < 0 || s.cursor >= len(s.panels) {
		return omsapi.PowerPanel{}, false
	}
	return s.panels[s.cursor], true
}

func (s *ElectricalPanelsScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		p, ok := s.selectedPanel()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		if msg := electricalDeleteBlock("panel", p.BreakerCount, "breaker"); msg != "" {
			s.confirmingDelete = false
			return s, Status(msg, StatusError)
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := p.ID
		return s, func() tea.Msg {
			return electricalPanelDeletedMsg{err: deps.OMS.DeletePowerPanel(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *ElectricalPanelsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading panels…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		return s.viewConfirm()
	}
	if len(s.panels) == 0 {
		return StyleMuted.Render("No electrical panels defined yet.") + "\n\n" +
			StyleMuted.Render("Create one with n, or in the OMS web admin → Electrical → Power panels.") + "\n\n" +
			StyleMuted.Render("n new panel · r refresh · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d panels", len(s.panels))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize()
	if end > len(s.panels) {
		end = len(s.panels)
	}
	for i := s.windowStart; i < end; i++ {
		p := s.panels[i]
		marker := "  "
		title := p.Name
		if i == s.cursor {
			marker = "▸ "
			title = StyleSidebarItemActive.Render(title)
		}
		if p.NeedsReview {
			title += " " + StyleStatusWarn.Render("needs review")
		}
		b.WriteString(marker + title + "\n")
		meta := []string{}
		if p.LocationName != "" {
			meta = append(meta, p.LocationName)
		}
		if p.Voltage > 0 {
			meta = append(meta, fmt.Sprintf("%dV", p.Voltage))
		}
		if p.PhaseConfiguration != "" {
			meta = append(meta, p.PhaseConfiguration)
		}
		if p.MainBreakerAmperage > 0 {
			meta = append(meta, fmt.Sprintf("main %dA", p.MainBreakerAmperage))
		}
		meta = append(meta, fmt.Sprintf("%d breakers", p.BreakerCount))
		b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
	}
	if end < len(s.panels) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.panels)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · enter topology · n new · E edit · x delete · r refresh · esc back"))
	return b.String()
}

func (s *ElectricalPanelsScreen) viewConfirm() string {
	p, ok := s.selectedPanel()
	if !ok {
		return ""
	}
	if s.deleting {
		return StyleMuted.Render("Deleting…")
	}
	warn := ""
	if p.BreakerCount > 0 {
		warn = StyleStatusWarn.Render(fmt.Sprintf("  (has %d breaker(s) — delete will be blocked)", p.BreakerCount))
	}
	return StyleStatusWarn.Render(fmt.Sprintf("Delete panel %q? This can't be undone.  y delete · n/esc cancel", p.Name)) + warn
}

// ElectricalPanelDetailScreen shows the full panel → breaker → circuit
// → outlet → asset tree returned by /api/electrical/panels/{id}/topology/.
type ElectricalPanelDetailScreen struct {
	deps           Deps
	panelID        int
	topology       *omsapi.PowerPanelTopology
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type electricalPanelDetailLoadedMsg struct {
	topology *omsapi.PowerPanelTopology
	err      error
}

type electricalPanelDetailDeletedMsg struct {
	err error
}

func NewElectricalPanelDetailScreen(deps Deps, panelID int) *ElectricalPanelDetailScreen {
	return &ElectricalPanelDetailScreen{
		deps:     deps,
		panelID:  panelID,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *ElectricalPanelDetailScreen) Title() string {
	if s.topology != nil && s.topology.Name != "" {
		return "Panel: " + s.topology.Name
	}
	return fmt.Sprintf("Panel #%d", s.panelID)
}

// WantsRawInput claims every key only while the delete confirm is up (y/n/esc).
func (s *ElectricalPanelDetailScreen) WantsRawInput() bool { return s.confirmingDelete }

// HandlesKey claims G (scroll-to-bottom) so it isn't shadowed by the global
// category-list hotkey; the other action keys (b/E/x) don't collide.
func (s *ElectricalPanelDetailScreen) HandlesKey(key string) bool { return key == "G" }

func (s *ElectricalPanelDetailScreen) Init() tea.Cmd { return s.load() }

func (s *ElectricalPanelDetailScreen) load() tea.Cmd {
	deps := s.deps
	panelID := s.panelID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		topo, err := deps.OMS.GetPowerPanelTopology(ctx, panelID)
		return electricalPanelDetailLoadedMsg{topology: topo, err: err}
	}
}

func (s *ElectricalPanelDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case electricalPanelDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.topology = m.topology
		s.scroller.Set(s.renderBody())
		return s, nil
	case electricalPanelDetailDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("panel deleted", StatusOK),
			SwitchTo(WSFacilities, NewElectricalPanelsScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			return s, s.load()
		case "b":
			// Manage this panel's breakers (create/edit/delete + drill to
			// circuits). The topology tree is read-only, so row-level actions
			// live on the dedicated breaker list.
			name := ""
			if s.topology != nil {
				name = s.topology.Name
			}
			return s, SwitchTo(WSFacilities, NewPanelBreakersScreen(s.deps, s.panelID, name))
		case "E":
			return s, SwitchTo(WSFacilities, NewPowerPanelFormScreen(s.deps, s.panelID))
		case "x":
			if s.topology != nil {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *ElectricalPanelDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.topology == nil {
			s.confirmingDelete = false
			return s, nil
		}
		if msg := electricalDeleteBlock("panel", len(s.topology.Breakers), "breaker"); msg != "" {
			s.confirmingDelete = false
			return s, Status(msg, StatusError)
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := s.panelID
		return s, func() tea.Msg {
			return electricalPanelDetailDeletedMsg{err: deps.OMS.DeletePowerPanel(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *ElectricalPanelDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading topology…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.topology == nil {
		return StyleMuted.Render("Panel not found.")
	}
	if s.confirmingDelete {
		warn := ""
		if n := len(s.topology.Breakers); n > 0 {
			warn = StyleStatusWarn.Render(fmt.Sprintf("  (has %d breaker(s) — delete will be blocked)", n))
		}
		return StyleStatusWarn.Render(fmt.Sprintf("Delete panel %q? This can't be undone.  y delete · n/esc cancel", s.topology.Name)) + warn
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	hint := "j/k scroll · b breakers · E edit · x delete · r refresh · esc back"
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
}

func (s *ElectricalPanelDetailScreen) renderBody() string {
	t := s.topology
	var b strings.Builder

	b.WriteString(StyleTitle.Render(t.Name) + "\n")
	meta := []string{}
	if t.LocationName != "" {
		meta = append(meta, t.LocationName)
	}
	if t.Voltage > 0 {
		meta = append(meta, fmt.Sprintf("%dV", t.Voltage))
	}
	if t.PhaseConfiguration != "" {
		meta = append(meta, t.PhaseConfiguration)
	}
	if t.MainBreakerAmperage > 0 {
		meta = append(meta, fmt.Sprintf("main %dA", t.MainBreakerAmperage))
	}
	if t.BreakerType != "" {
		meta = append(meta, t.BreakerType)
	}
	if len(meta) > 0 {
		b.WriteString(StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
	}
	b.WriteString("\n")

	if t.FedBySummary != nil {
		fb := t.FedBySummary
		b.WriteString(StyleTitle.Render("Fed by") + "\n")
		fedLine := fmt.Sprintf("%s · breaker %s (%dA, %dp)", fb.PanelName, fb.BreakerPosition, fb.BreakerAmperage, fb.BreakerPoleCount)
		b.WriteString("  " + StyleMuted.Render(fedLine) + "\n")
		if fb.CircuitLabel != "" {
			b.WriteString("  " + StyleMuted.Render("via "+fb.CircuitLabel) + "\n")
		}
		b.WriteString("\n")
	}

	if len(t.DownstreamPanels) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Feeds %d sub-panel(s)", len(t.DownstreamPanels))) + "\n")
		for _, dp := range t.DownstreamPanels {
			b.WriteString("  · " + dp.Name + "\n")
		}
		b.WriteString("\n")
	}

	if len(t.Breakers) == 0 {
		b.WriteString(StyleMuted.Render("No breakers configured on this panel yet.") + "\n")
		return b.String()
	}

	b.WriteString(StyleTitle.Render(fmt.Sprintf("Breakers (%d)", len(t.Breakers))) + "\n")
	for _, br := range t.Breakers {
		header := fmt.Sprintf("  [%s] %dA · %s · %dp", br.Position, br.Amperage, br.Phase, br.PoleCount)
		if br.Label != "" {
			header += " — " + br.Label
		}
		if br.Status != "" && br.Status != "active" {
			header += " " + StyleStatusWarn.Render(br.Status)
		}
		if br.ReviewStatus != "" && br.ReviewStatus != "ok" {
			header += " " + StyleStatusWarn.Render(br.ReviewStatus)
		}
		b.WriteString(header + "\n")
		if br.ReviewNote != "" {
			b.WriteString("      " + StyleMuted.Render(br.ReviewNote) + "\n")
		}
		for _, ck := range br.Circuits {
			circLine := "      ↳ circuit"
			if ck.Label != "" {
				circLine += " " + ck.Label
			}
			circMeta := []string{}
			if ck.MaxLoadAmps > 0 {
				circMeta = append(circMeta, fmt.Sprintf("max %dA", ck.MaxLoadAmps))
			}
			if ck.ConductorSize != "" {
				circMeta = append(circMeta, ck.ConductorSize)
			}
			if len(circMeta) > 0 {
				circLine += " " + StyleMuted.Render("("+strings.Join(circMeta, " · ")+")")
			}
			b.WriteString(circLine + "\n")
			for _, out := range ck.Outlets {
				outLine := "          · outlet"
				if out.Label != "" {
					outLine += " " + out.Label
				}
				if out.OutletType != "" {
					outLine += " " + StyleMuted.Render("("+out.OutletType+")")
				}
				if out.LocationName != "" {
					outLine += " " + StyleMuted.Render("@ "+out.LocationName)
				}
				if out.Status != "" && out.Status != "active" {
					outLine += " " + StyleStatusWarn.Render(out.Status)
				}
				b.WriteString(outLine + "\n")
				for _, asset := range out.ConnectedAssets {
					marker := "              ↳ "
					line := marker + asset.Name
					if asset.AssetTag != "" {
						line += " " + StyleMuted.Render("("+asset.AssetTag+")")
					}
					if asset.IsCritical {
						line += " " + StyleStatusError.Render("critical")
					}
					b.WriteString(line + "\n")
				}
			}
		}
	}
	return b.String()
}
