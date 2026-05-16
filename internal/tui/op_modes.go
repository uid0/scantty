package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type OperationalModesScreen struct {
	deps    Deps
	rows    []forgekeyapi.OperationalMode
	cursor  int
	loading bool
	loadErr string
}

type opModesLoadedMsg struct {
	rows []forgekeyapi.OperationalMode
	err  error
}

func NewOperationalModesScreen(deps Deps) *OperationalModesScreen {
	return &OperationalModesScreen{deps: deps, loading: true}
}

func (s *OperationalModesScreen) Title() string { return "Operational Modes" }

func (s *OperationalModesScreen) Init() tea.Cmd { return s.load() }

func (s *OperationalModesScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListOperationalModes(ctx, nil)
		return opModesLoadedMsg{rows: rows, err: err}
	}
}

func (s *OperationalModesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case opModesLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		return s, nil
	case fkActionResultMsg:
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		return s, tea.Batch(Status(m.action, StatusOK), s.load())
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
		case "c":
			if s.cursor >= len(s.rows) {
				return s, nil
			}
			row := s.rows[s.cursor]
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			id := fmt.Sprint(row.ID)
			if row.ClassroomModeEnabled {
				return s, func() tea.Msg {
					err := deps.ForgeKey.DisableClassroomMode(ctx, id)
					return fkActionResultMsg{action: fmt.Sprintf("classroom mode OFF for asset %v", row.Asset), err: err}
				}
			}
			return s, func() tea.Msg {
				err := deps.ForgeKey.EnableClassroomMode(ctx, id)
				return fkActionResultMsg{action: fmt.Sprintf("classroom mode ON for asset %v", row.Asset), err: err}
			}
		}
	}
	return s, nil
}

func (s *OperationalModesScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading operational modes…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No operational modes.")
	}
	var b strings.Builder
	for i, r := range s.rows {
		caret := "  "
		if i == s.cursor {
			caret = "▸ "
		}
		mode := r.Mode
		switch r.Mode {
		case "AVAILABLE":
			mode = StyleStatusOK.Render(r.Mode)
		case "LOCKED_OUT":
			mode = StyleStatusError.Render(r.Mode)
		case "CLASSROOM":
			mode = StyleStatusWarn.Render(r.Mode)
		}
		title := fmt.Sprintf("%sasset %v  [%s]", caret, r.Asset, mode)
		if r.ClassroomModeEnabled {
			title += "  " + StyleStatusWarn.Render("📚 classroom")
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · c toggle classroom mode · r refresh · esc back"))
	return b.String()
}
