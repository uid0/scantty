package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type WorkOrderDetailScreen struct {
	deps      Deps
	woID      string
	wo        *omsapi.WorkOrder
	loading   bool
	loadErr   string
	actionMsg string
	actionLvl StatusLevel
}

type woDetailLoadedMsg struct {
	wo  *omsapi.WorkOrder
	err error
}

type woTransitionedMsg struct {
	action string
	wo     *omsapi.WorkOrder
	err    error
}

func NewWorkOrderDetailScreen(deps Deps, id string) *WorkOrderDetailScreen {
	return &WorkOrderDetailScreen{deps: deps, woID: id, loading: true}
}

func (s *WorkOrderDetailScreen) Title() string {
	if s.wo != nil && s.wo.Title != "" {
		return fmt.Sprintf("WO: %s", s.wo.Title)
	}
	return fmt.Sprintf("WO #%s", s.woID)
}

func (s *WorkOrderDetailScreen) Init() tea.Cmd { return s.load() }

func (s *WorkOrderDetailScreen) load() tea.Cmd {
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

func (s *WorkOrderDetailScreen) transition(action string) tea.Cmd {
	deps := s.deps
	id := s.woID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		wo, err := deps.OMS.TransitionWorkOrder(ctx, id, action, "")
		return woTransitionedMsg{action: action, wo: wo, err: err}
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
	case woTransitionedMsg:
		if m.err != nil {
			s.actionMsg = fmt.Sprintf("%s failed: %s", m.action, m.err.Error())
			s.actionLvl = StatusError
			return s, Status(s.actionMsg, StatusError)
		}
		s.actionMsg = fmt.Sprintf("%s OK", m.action)
		s.actionLvl = StatusOK
		s.wo = m.wo
		return s, Status(s.actionMsg, StatusOK)
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "c":
			return s, s.transition("completed")
		case "x":
			return s, s.transition("cancelled")
		case "i":
			return s, s.transition("in_progress")
		case "b":
			return s, s.transition("blocked")
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
	wo := s.wo
	var b strings.Builder
	b.WriteString(StyleTitle.Render(wo.Title) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %v · status %s · priority %s", wo.ID, wo.Status, wo.Priority)) + "\n")
	if wo.AssetName != "" {
		b.WriteString(StyleMuted.Render("Asset: ") + wo.AssetName + "\n")
	}
	if wo.Description != "" {
		b.WriteString("\n" + wo.Description + "\n")
	}
	b.WriteString("\n")
	if !wo.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Created %s", wo.CreatedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if wo.CompletedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Completed %s", wo.CompletedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if wo.ClosedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Closed %s", wo.ClosedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	b.WriteString("\n")

	if s.actionMsg != "" {
		b.WriteString(RenderStatus(s.actionMsg, s.actionLvl) + "\n\n")
	}

	b.WriteString(StyleMuted.Render("i in-progress · c complete · b block · x cancel · r refresh · esc back"))
	return b.String()
}