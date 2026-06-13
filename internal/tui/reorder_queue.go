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
	// busyID names the row currently waiting on an approve/cancel
	// response. Renders as "(approving…)" / "(cancelling…)" next to
	// the title and blocks repeat presses on the same row.
	busyID     string
	busyAction string
}

type reorderQueueLoadedMsg struct {
	rows []omsapi.ReorderRequest
	err  error
}

type reorderActionMsg struct {
	id     string
	action string
	err    error
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
	case reorderActionMsg:
		s.busyID = ""
		s.busyAction = ""
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
		}
		// Refresh — the row leaves the "pending" filter on success.
		s.loading = true
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
		case "a":
			return s, s.actOnCursor("approve")
		case "x":
			return s, s.actOnCursor("cancel")
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

// actOnCursor dispatches an approve / cancel call against the row
// under the cursor. Returns a Cmd or nil when the row is missing,
// already busy, or the action name is unknown.
func (s *ReorderQueueScreen) actOnCursor(action string) tea.Cmd {
	if s.cursor >= len(s.rows) {
		return nil
	}
	row := s.rows[s.cursor]
	id := fmt.Sprintf("%v", row.ID)
	if id == "" {
		return Status("row missing id; cannot "+action, StatusError)
	}
	if s.busyID == id {
		return nil
	}
	s.busyID = id
	s.busyAction = action

	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	switch action {
	case "approve":
		return func() tea.Msg {
			_, err := deps.OMS.ApproveReorderRequest(ctx, id, "")
			return reorderActionMsg{id: id, action: "approved", err: err}
		}
	case "cancel":
		return func() tea.Msg {
			_, err := deps.OMS.CancelReorderRequest(ctx, id, "")
			return reorderActionMsg{id: id, action: "cancelled", err: err}
		}
	}
	return nil
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

		// Line 1: caret + item name + quantity (+ urgent priority badge).
		// Fall back to the raw item UUID only when item_details is missing
		// so the operator can still match the row against the backend.
		name := "—"
		if r.ItemDetails != nil && r.ItemDetails.Name != "" {
			name = r.ItemDetails.Name
		} else if r.Item != "" {
			name = "item " + r.Item
		}
		title := fmt.Sprintf("%s%d) %s  × %d", caret, i+1, name, r.Quantity)
		if r.Priority != "" && r.Priority != "normal" {
			pri := "(" + r.Priority + ")"
			if r.Priority == "urgent" || r.Priority == "high" {
				title += "  " + StyleStatusError.Render(pri)
			} else {
				title += "  " + StyleStatusWarn.Render(pri)
			}
		}
		if rowID := fmt.Sprintf("%v", r.ID); rowID != "" && rowID == s.busyID {
			title += "  " + StyleStatusWarn.Render("("+s.busyAction+"ing…)")
		}
		if i == s.cursor {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")

		// Line 2: JD-Edwards-style aligned data row — SKU, stock vs min,
		// estimated cost, days pending. Days-pending colors urgency
		// (red ≥ 7d, yellow ≥ 3d, plain otherwise) so the operator can
		// spot stale requests without doing the math.
		b.WriteString("    " + StyleMuted.Render(reorderQueueLine(r)) + "\n")

		// Optional supplier / requested-by / notes rows.
		extra := []string{}
		if r.RequestedBy != "" {
			extra = append(extra, "requested by "+r.RequestedBy)
		}
		if r.ItemDetails != nil && r.ItemDetails.PreferredSupplier != "" {
			extra = append(extra, "supplier "+r.ItemDetails.PreferredSupplier)
		}
		if len(extra) > 0 {
			b.WriteString("    " + StyleMuted.Render(strings.Join(extra, " · ")) + "\n")
		}
		if r.RequestNotes != "" {
			b.WriteString("    " + StyleMuted.Render(r.RequestNotes) + "\n")
		}
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · a approve · x cancel · enter open item · r refresh · esc back"))
	return b.String()
}

// reorderQueueLine renders the per-row data summary in fixed-width
// columns so SKU / stock / cost / days-pending align between rows.
func reorderQueueLine(r omsapi.ReorderRequest) string {
	sku := "—"
	stockMin := "—"
	if r.ItemDetails != nil {
		if r.ItemDetails.SKU != "" {
			sku = r.ItemDetails.SKU
		}
		stockMin = fmt.Sprintf("%d/%d", r.ItemDetails.CurrentStock, r.ItemDetails.MinimumStock)
	}
	est := "—"
	if !r.EstimatedCost.Empty() {
		est = string(r.EstimatedCost)
	}
	daysLabel := fmt.Sprintf("%dd pending", r.DaysPending)
	switch {
	case r.DaysPending >= 7:
		daysLabel = StyleStatusError.Render(daysLabel)
	case r.DaysPending >= 3:
		daysLabel = StyleStatusWarn.Render(daysLabel)
	}
	return fmt.Sprintf(
		"SKU %-16s   STOCK %7s   EST $%10s   %s",
		sku, stockMin, est, daysLabel,
	)
}
