package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type ReorderQueueScreen struct {
	deps    Deps
	rows    []omsapi.ReorderRequest
	cursor  int
	loading bool
	loadErr string
}

type reorderQueueLoadedMsg struct {
	rows []omsapi.ReorderRequest
	err  error
}

func NewReorderQueueScreen(deps Deps) *ReorderQueueScreen {
	return &ReorderQueueScreen{deps: deps, loading: true}
}

func (s *ReorderQueueScreen) Title() string { return "Reorder Queue (pending)" }

func (s *ReorderQueueScreen) Init() tea.Cmd { return s.load() }

func (s *ReorderQueueScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.OMS.ListPendingReorders(ctx, nil)
		if err != nil {
			return reorderQueueLoadedMsg{err: err}
		}
		return reorderQueueLoadedMsg{rows: rows}
	}
}

func (s *ReorderQueueScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case reorderQueueLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			return s, s.load()
		case "enter":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, row.Item))
		}
	}
	return s, nil
}

func (s *ReorderQueueScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading reorder queue…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No pending reorders.") + "\n\n" + StyleMuted.Render("r refresh · esc back")
	}
	var b strings.Builder
	b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("%d pending", len(s.rows))) + "\n\n")
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		title := fmt.Sprintf("%sitem %s × %d", caret, r.Item, r.Quantity)
		if r.Priority != "" && r.Priority != "normal" {
			title += " " + StyleStatusWarn.Render("("+r.Priority+")")
		}
		if r.RequestedBy != "" {
			title += " " + StyleMuted.Render("by "+r.RequestedBy)
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")
		if r.RequestNotes != "" {
			b.WriteString("    " + StyleMuted.Render(r.RequestNotes) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · enter open item · r refresh · esc back"))
	return b.String()
}
