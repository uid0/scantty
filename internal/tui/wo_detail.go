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
	scroller  *TextScroller
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
	return &WorkOrderDetailScreen{
		deps:     deps,
		woID:     id,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
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
	case tea.WindowSizeMsg:
		s.scroller.SetViewHeight(m.Height - detailChromeRows)
		return s, nil
	case woDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.wo = m.wo
		s.scroller.Set(s.renderBody())
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
		s.scroller.Set(s.renderBody())
		return s, Status(s.actionMsg, StatusOK)
	case tea.KeyMsg:
		if s.scroller.Handle(m) {
			return s, nil
		}
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
	body := s.scroller.View()
	footer := ""
	if s.actionMsg != "" {
		footer += RenderStatus(s.actionMsg, s.actionLvl) + "\n\n"
	}
	footer += StyleMuted.Render("j/k scroll · pgup/pgdn page · i in-progress · c complete · b block · x cancel · r refresh · esc back")
	return body + "\n\n" + footer
}

func (s *WorkOrderDetailScreen) renderBody() string {
	wo := s.wo
	var b strings.Builder

	b.WriteString(StyleTitle.Render(wo.Title))
	if wo.IsOverdue {
		b.WriteString("  " + StyleStatusError.Render("OVERDUE"))
	}
	b.WriteString("\n")

	headerParts := []string{}
	if wo.ShortID != "" {
		headerParts = append(headerParts, "#"+wo.ShortID)
	}
	headerParts = append(headerParts, fmt.Sprintf("ID %v", wo.ID))
	if wo.Status != "" {
		headerParts = append(headerParts, "status "+wo.Status)
	}
	if wo.Priority != "" {
		headerParts = append(headerParts, "priority "+wo.Priority)
	}
	b.WriteString(StyleMuted.Render(strings.Join(headerParts, " · ")) + "\n")

	if wo.AssetName != "" {
		assetLine := "Asset: " + wo.AssetName
		if wo.AssetTag != "" {
			assetLine += " (" + wo.AssetTag + ")"
		}
		b.WriteString(StyleMuted.Render(assetLine) + "\n")
	}
	if wo.MaintenanceItemTitle != "" {
		b.WriteString(StyleMuted.Render("Maintenance item: ") + wo.MaintenanceItemTitle + "\n")
	}
	if wo.AssignedToName != "" {
		b.WriteString(StyleMuted.Render("Assigned to: ") + wo.AssignedToName + "\n")
	}
	if wo.CompletedByName != "" {
		b.WriteString(StyleMuted.Render("Completed by: ") + wo.CompletedByName + "\n")
	}

	if wo.Description != "" {
		b.WriteString("\n" + wo.Description + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Dates") + "\n")
	if wo.DueDate != "" {
		b.WriteString(StyleMuted.Render("Due: ") + wo.DueDate + "\n")
	}
	if !wo.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Created: %s", wo.CreatedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if !wo.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Updated: %s", wo.UpdatedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if wo.CompletedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Completed: %s", wo.CompletedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if wo.ClosedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Closed: %s", wo.ClosedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	b.WriteString("\n")

	if wo.Notes != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(wo.Notes + "\n\n")
	}

	if len(wo.TaskCompletions) > 0 {
		done, total := 0, len(wo.TaskCompletions)
		for _, t := range wo.TaskCompletions {
			if t.IsCompleted {
				done++
			}
		}
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Tasks (%d/%d)", done, total)) + "\n")
		for _, t := range wo.TaskCompletions {
			marker := "  ○ "
			if t.IsCompleted {
				marker = StyleStatusOK.Render("  ✓ ")
			}
			title := t.TaskTitle
			if !t.IsRequired {
				title += " " + StyleMuted.Render("(optional)")
			}
			b.WriteString(marker + title + "\n")
			meta := []string{}
			if t.CompletedAt != nil {
				meta = append(meta, t.CompletedAt.Format("2006-01-02 15:04"))
			}
			if t.CompletedBy != "" {
				meta = append(meta, "by "+t.CompletedBy)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
			if t.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(t.Notes) + "\n")
			}
		}
		b.WriteString("\n")
	}

	if len(wo.MaterialUsage) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Materials (%d)", len(wo.MaterialUsage))) + "\n")
		for _, m := range wo.MaterialUsage {
			line := "  · " + m.MaterialName
			if !m.QuantityPlanned.Empty() {
				line += " — " + string(m.QuantityPlanned)
				if m.Unit != "" {
					line += " " + m.Unit
				}
			}
			if m.WasUsed {
				line += " " + StyleStatusOK.Render("✓ used")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(wo.Photos) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Photos (%d)", len(wo.Photos))) + "\n")
		for _, p := range wo.Photos {
			line := "  · "
			if p.Caption != "" {
				line += p.Caption
			} else {
				line += p.ImageURL
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if !p.UploadedAt.IsZero() {
				meta = append(meta, p.UploadedAt.Format("2006-01-02"))
			}
			if p.UploadedBy != "" {
				meta = append(meta, "by "+p.UploadedBy)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
	}

	return b.String()
}
