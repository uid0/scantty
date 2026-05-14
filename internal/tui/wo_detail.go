package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type WorkOrderDetailScreen struct {
	deps    Deps
	woID    int
	wo      *omsapi.WorkOrder
	loading bool
	loadErr string
}

type woDetailLoadedMsg struct {
	wo  *omsapi.WorkOrder
	err error
}

func NewWorkOrderDetailScreen(deps Deps, id string) *WorkOrderDetailScreen {
	woID, _ := strconv.Atoi(id)
	return &WorkOrderDetailScreen{deps: deps, woID: woID, loading: true}
}

func (s *WorkOrderDetailScreen) Title() string {
	if s.wo != nil && s.wo.Title != "" {
		return fmt.Sprintf("WO: %s", s.wo.Title)
	}
	return fmt.Sprintf("WO #%d", s.woID)
}

func (s *WorkOrderDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.woID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		wo, err := deps.OMS.GetWorkOrder(ctx, id)
		return woDetailLoadedMsg{wo: wo, err: err}
	}
}

func (s *WorkOrderDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case woDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.wo = m.wo
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		}
	}
	return s, nil
}

func (s *WorkOrderDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading work order…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.wo == nil {
		return StyleMuted.Render("Work order not found.")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(s.wo.Title) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d · Status: %s · Priority: %s", s.wo.ID, s.wo.Status, s.wo.Priority)) + "\n\n")
	if s.wo.AssetName != "" {
		b.WriteString(StyleMuted.Render("Asset: ") + s.wo.AssetName + "\n")
	}
	if s.wo.Description != "" {
		b.WriteString("\n" + s.wo.Description + "\n\n")
	}
	if !s.wo.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Created %s", s.wo.CreatedAt.Format("2006-01-02"))) + "\n")
	}
	if s.wo.ClosedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Closed %s", s.wo.ClosedAt.Format("2006-01-02"))) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("(transitions + notifications coming next slice)") + "\n\n")
	b.WriteString(StyleMuted.Render("r refresh · esc back"))
	return b.String()
}