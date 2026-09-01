// MaintenanceItemsScreen — the preventive-maintenance ITEM list.
//
// ScanTTY's Maintenance workspace lands on WORK ORDERS; this screen is the
// missing PM-item management surface (the web asset page's "Maintenance" tab).
// It lists every maintenance item across assets and is the entry point for
// create (c), open-detail (enter), edit, delete, and the per-item actions
// (complete / clone / generate-work-order), which live on the detail screen.
//
// Reached with the global `M` hotkey (app.go). It is a plain (non-raw) screen
// so workspace switching keeps working; `c` and `enter` are not global hotkeys
// so they reach us via the root's fall-through.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type MaintenanceItemsScreen struct {
	deps           Deps
	items          []omsapi.MaintenanceItem
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int
}

type maintenanceItemsLoadedMsg struct {
	items []omsapi.MaintenanceItem
	err   error
}

func NewMaintenanceItemsScreen(deps Deps) *MaintenanceItemsScreen {
	return &MaintenanceItemsScreen{deps: deps, loading: true, windowSize: 20}
}

func (s *MaintenanceItemsScreen) Title() string { return "PM Items" }

func (s *MaintenanceItemsScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListMaintenanceItems(ctx, nil)
		if err != nil {
			return maintenanceItemsLoadedMsg{err: err}
		}
		return maintenanceItemsLoadedMsg{items: page.Results}
	}
}

func (s *MaintenanceItemsScreen) computeWindowSize() int {
	const chrome = 4 // header + blank + hint + one indicator slack
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *MaintenanceItemsScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case maintenanceItemsLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.items = m.items
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.items)-1 {
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
			if s.cursor >= len(s.items) {
				s.cursor = len(s.items) - 1
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
			s.cursor = len(s.items) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "c":
			return s, SwitchTo(WSMaintenance, NewMaintenanceItemFormScreen(s.deps, ""))
		case "enter":
			if s.cursor >= 0 && s.cursor < len(s.items) {
				return s, SwitchTo(WSMaintenance, NewMaintenanceItemDetailScreen(s.deps, s.items[s.cursor].ID))
			}
		}
	}
	return s, nil
}

func (s *MaintenanceItemsScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 20
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
	if len(s.items) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *MaintenanceItemsScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading PM items…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · c new · esc back")
	}
	if len(s.items) == 0 {
		return StyleMuted.Render("No PM items.") + "\n\n" + StyleMuted.Render("c new PM item · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d PM items", len(s.items))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}

	end := s.windowStart + s.windowSize
	if end > len(s.items) {
		end = len(s.items)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.items) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.items)-end)) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · g/G top/bottom · enter open · c new · r refresh · esc back"))
	return b.String()
}

func (s *MaintenanceItemsScreen) renderRow(i int) string {
	it := s.items[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	title := it.Title
	meta := []string{}
	if it.AssetName != "" {
		meta = append(meta, it.AssetName)
	}
	if it.IntervalDays != nil {
		meta = append(meta, fmt.Sprintf("every %dd", *it.IntervalDays))
	}
	if !it.IsActive {
		meta = append(meta, "inactive")
	}
	line := marker + title
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	if it.IsOverdue {
		badge := "OVERDUE"
		if it.DaysOverdue != nil {
			badge = fmt.Sprintf("OVERDUE %dd", *it.DaysOverdue)
		}
		line += " " + StyleStatusError.Render(badge)
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}
