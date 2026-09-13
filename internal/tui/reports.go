package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// ReportsScreen is the landing page for the Reports workspace (nav 8). It
// mirrors the OMS web /reports section — a set of report surfaces — as a menu:
// the staff-gated Analytics Pulse plus the three report pages (Inventory,
// Purchasing, Asset), and the serialized-component consumption forecast that
// scantty already shipped. Each entry opens a scrollable table view.
//
// j/k or arrows move; enter or the entry's hotkey opens it. Selection lives on
// the screen so leaving the workspace resets it. Reachable again any time from
// the sidebar menu (tab, then arrow to Reports).
type ReportsScreen struct {
	deps   Deps
	cursor int
	items  []reportsItem

	// windowStart is the first row drawn, refitted from View by
	// proseFlatListFrame against the pane the terminal gave.
	windowStart    int
	terminalWidth  int
	terminalHeight int
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
				subtitle: "restock cadence · next due · days-until-due",
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

// HandlesKey claims the entry hotkeys. The globals they used to collide with
// (a = authorizations, f = firmware, m = profile, n = notifications) are gone
// since phase 3, so the claim no longer rescues them from anything — it is kept
// as the screen naming the keys it owns, and it still keeps `esc` off the
// root's back-stack for a menu that is itself the back destination.
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

// bar names every key that acts on this menu, as a RECORD rather than a literal
// — prose_bar.go carries the conversion, and FacilitiesScreen.bar is the same
// shape one workspace over.
//
// It used to be a legend ABOVE the rows reading
//
//	Select a report — j/k move · enter or hotkey opens · esc back
//
// which left the arrows, g and home moving the cursor under no word, and called
// the row letters "hotkey", which no keystroke spells. Every row was drawn with
// no window, so on a short pane the last reports and the closing note were what
// clampToBox took.
//
// THE JUMP IS TO THE TOP ONLY, because that is all this switch binds: `g` and
// `home`, and neither `G` nor `end` (FacilitiesScreen binds all four). Naming the
// whole `g/G home/end` segment would claim two keys that do nothing, and binding
// them is a change to what the menu does rather than to what its bar says — so
// the segment is narrowed (proseNavTop) and the asymmetry is left visible here.
func (s *ReportsScreen) bar(rows int) proseBar {
	hotkeys := make([]rune, 0, len(s.items))
	for _, it := range s.items {
		hotkeys = append(hotkeys, it.hotkey)
	}
	moves := listNavMoves(rows)
	out := append(proseNavStep(moves), proseNavTop(moves)...)
	return append(out,
		proseBarItem{Keys: []string{"enter"}, Hint: "enter open"},
		proseMenuHotkeys(hotkeys, "open by letter"),
		proseBarEsc,
	)
}

// proseBar is the bar this menu is drawing. A menu has no load and no second
// surface, so there is no state in which it draws something else instead.
func (s *ReportsScreen) proseBar() proseBar { return s.bar(len(s.items)) }

func (s *ReportsScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *ReportsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		s.terminalWidth, s.terminalHeight = size.Width, size.Height
		return s, nil
	}
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

// reportsNote leads the rows: the instruction the old legend opened with, and
// the closing note it drew under them, with the keys moved onto the bar.
const reportsNote = "Select a report. A terminal can't draw the web's charts, so each report surfaces the underlying numbers as tables."

func (s *ReportsScreen) View() string {
	cells := s.paneCells()
	head := pickerHintAt(reportsNote, cells) + "\n\n"
	rows := make([]string, len(s.items))
	for i, it := range s.items {
		marker := "  "
		label := "[" + string(it.hotkey) + "]  " + it.label
		if i == s.cursor {
			marker = "▸ "
			label = StyleSidebarItemActive.Render(label)
		}
		row := marker + label
		if it.subtitle != "" {
			row += "\n" + proseMenuSubtitle(it.subtitle, cells)
		}
		rows[i] = row
	}
	return proseFlatListFrame(head, rows, s.cursor, &s.windowStart, s.terminalHeight,
		cells, s.bar(proseFlatCeilingRows), s.proseBar())
}
