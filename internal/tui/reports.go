package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ReportsScreen is the landing page for the Reports workspace (nav 8). It
// mirrors the OMS web /reports section — a set of report surfaces — as a menu:
// the staff-gated Analytics Pulse plus the three report pages (Inventory,
// Purchasing, Asset), and the serialized-component consumption forecast that
// scantty already shipped. Each entry opens a scrollable table view.
//
// j/k or arrows move; enter or the entry's hotkey opens it. Selection lives on
// the screen so leaving the workspace resets it. Reachable again any time via
// the global "8".
type ReportsScreen struct {
	deps   Deps
	cursor int
	items  []reportsItem
}

type reportsItem struct {
	hotkey   rune
	label    string
	subtitle string
	build    func(deps Deps) Screen
}

func NewReportsScreen(deps Deps) *ReportsScreen {
	return &ReportsScreen{
		deps: deps,
		items: []reportsItem{
			{
				hotkey:   'p',
				label:    "Analytics Pulse",
				subtitle: "value summary, WO trend, top users, utilization, category spend, forecast (staff)",
				build:    func(d Deps) Screen { return NewAnalyticsPulseScreen(d) },
			},
			{
				hotkey:   'i',
				label:    "Inventory report",
				subtitle: "stock by category · reorder frequency · value by location",
				build:    func(d Deps) Screen { return NewInventoryReportScreen(d) },
			},
			{
				hotkey:   'c',
				label:    "Purchasing report",
				subtitle: "spend by supplier / category · lead time · price trends",
				build:    func(d Deps) Screen { return NewPurchasingReportScreen(d) },
			},
			{
				hotkey:   'r',
				label:    "Reorders analytics",
				subtitle: "supplier performance · lead-time trends · public transparency · logistics",
				build:    func(d Deps) Screen { return NewReorderAnalyticsReportScreen(d) },
			},
			{
				hotkey:   'a',
				label:    "Asset report",
				subtitle: "by status · maintenance due · utilization · total cost of ownership",
				build:    func(d Deps) Screen { return NewAssetReportScreen(d) },
			},
			{
				hotkey:   'd',
				label:    "ForgeKey fleet",
				subtitle: "device health · online/offline · by type/capability/firmware · attention · recent activity",
				build:    func(d Deps) Screen { return NewForgeKeyFleetReportScreen(d) },
			},
			{
				hotkey:   'f',
				label:    "Serialized forecast",
				subtitle: "consumption forecast · days-to-stockout · reorder point",
				build:    func(d Deps) Screen { return NewSerializedForecastScreen(d) },
			},
			{
				hotkey:   'm',
				label:    "Demand forecast",
				subtitle: "ML predicted demand · days-to-stockout · predictive reorder point",
				build:    func(d Deps) Screen { return NewDemandForecastScreen(d) },
			},
			{
				hotkey:   'n',
				label:    "Reorder alerts",
				subtitle: "the notify set — opted-in items the forecast says are due to reorder",
				build:    func(d Deps) Screen { return NewReorderAlertsScreen(d) },
			},
		},
	}
}

func (s *ReportsScreen) Title() string { return "Reports" }

func (s *ReportsScreen) Init() tea.Cmd { return nil }

// HandlesKey claims the entry hotkeys so an accelerator that collides with a
// global nav key (a = authorizations, f = firmware, m = profile,
// n = notifications) still opens the report while this menu is active, rather
// than firing the global.
func (s *ReportsScreen) HandlesKey(key string) bool {
	if len(key) != 1 {
		return false
	}
	r := rune(key[0])
	for _, it := range s.items {
		if it.hotkey == r {
			return true
		}
	}
	return false
}

func (s *ReportsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	keymsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch keymsg.String() {
	case "j", "down":
		if s.cursor < len(s.items)-1 {
			s.cursor++
		}
		return s, nil
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
		return s, nil
	case "g", "home":
		s.cursor = 0
		return s, nil
	case "enter":
		return s.openSelected()
	}
	if r := keymsg.String(); len(r) == 1 {
		for i, it := range s.items {
			if string(it.hotkey) == r {
				s.cursor = i
				return s.openSelected()
			}
		}
	}
	return s, nil
}

func (s *ReportsScreen) openSelected() (Screen, tea.Cmd) {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return s, nil
	}
	it := s.items[s.cursor]
	return s, SwitchTo(WSReports, it.build(s.deps))
}

func (s *ReportsScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Select a report — j/k move · enter or hotkey opens · esc back") + "\n\n")
	for i, it := range s.items {
		marker := "  "
		label := "[" + string(it.hotkey) + "]  " + it.label
		if i == s.cursor {
			marker = "▸ "
			label = StyleSidebarItemActive.Render(label)
		}
		b.WriteString(marker + label + "\n")
		if it.subtitle != "" {
			b.WriteString("      " + StyleMuted.Render(it.subtitle) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("A terminal can't draw the web's charts, so each report surfaces the underlying numbers as tables."))
	return b.String()
}
