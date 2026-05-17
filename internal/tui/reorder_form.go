package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type ReorderFormScreen struct {
	deps      Deps
	item      *omsapi.Item
	suppliers []omsapi.ItemSupplier

	inputs    []textinput.Model
	labels    []string
	focused   int
	pending   bool
	resultMsg string
	resultLvl StatusLevel
}

type reorderSubmittedMsg struct {
	result *omsapi.ReorderRequest
	err    error
}

const (
	reorderFieldQuantity = iota
	reorderFieldRequester
	reorderFieldPriority
	reorderFieldNotes
)

func NewReorderFormScreen(deps Deps, item *omsapi.Item, suppliers []omsapi.ItemSupplier) *ReorderFormScreen {
	s := &ReorderFormScreen{
		deps:      deps,
		item:      item,
		suppliers: suppliers,
		labels:    []string{"Quantity", "Requested by", "Priority", "Notes"},
	}
	for i, label := range s.labels {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
		ti.Placeholder = placeholderFor(label)
		if i == 0 {
			ti.Focus()
		}
		s.inputs = append(s.inputs, ti)
	}
	if def := defaultPackQty(suppliers); def > 0 {
		s.inputs[reorderFieldQuantity].SetValue(strconv.Itoa(def))
	}
	s.inputs[reorderFieldPriority].SetValue("normal")
	return s
}

func placeholderFor(label string) string {
	switch label {
	case "Quantity":
		return "integer"
	case "Priority":
		return "low | normal | high | urgent"
	case "Requested by":
		return "your name"
	case "Notes":
		return "optional"
	}
	return ""
}

func defaultPackQty(suppliers []omsapi.ItemSupplier) int {
	for _, s := range suppliers {
		if s.IsPreferred && s.PackQuantity > 0 {
			return s.PackQuantity
		}
	}
	for _, s := range suppliers {
		if s.PackQuantity > 0 {
			return s.PackQuantity
		}
	}
	return 0
}

func (s *ReorderFormScreen) Title() string { return "Request Reorder" }

func (s *ReorderFormScreen) WantsRawInput() bool { return true }

func (s *ReorderFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ReorderFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case reorderSubmittedMsg:
		s.pending = false
		if m.err != nil {
			s.resultMsg = "submit failed: " + m.err.Error()
			s.resultLvl = StatusError
			return s, Status(s.resultMsg, StatusError)
		}
		s.resultMsg = fmt.Sprintf("reorder #%d created", m.result.ID)
		s.resultLvl = StatusOK
		return s, Status(s.resultMsg, StatusOK)
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyTab, tea.KeyShiftTab, tea.KeyDown, tea.KeyUp:
			s.focusNext(m.Type == tea.KeyShiftTab || m.Type == tea.KeyUp)
			return s, nil
		case tea.KeyEnter:
			if s.focused < len(s.inputs)-1 {
				s.focusNext(false)
				return s, nil
			}
			return s.submit()
		case tea.KeyEsc:
			return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.item.ID))
		}
	}
	var cmd tea.Cmd
	s.inputs[s.focused], cmd = s.inputs[s.focused].Update(msg)
	return s, cmd
}

func (s *ReorderFormScreen) focusNext(reverse bool) {
	s.inputs[s.focused].Blur()
	if reverse {
		s.focused--
		if s.focused < 0 {
			s.focused = len(s.inputs) - 1
		}
	} else {
		s.focused = (s.focused + 1) % len(s.inputs)
	}
	s.inputs[s.focused].Focus()
}

func (s *ReorderFormScreen) submit() (Screen, tea.Cmd) {
	qty, err := strconv.Atoi(strings.TrimSpace(s.inputs[reorderFieldQuantity].Value()))
	if err != nil || qty <= 0 {
		s.resultMsg = "quantity must be a positive integer"
		s.resultLvl = StatusError
		return s, nil
	}
	requester := strings.TrimSpace(s.inputs[reorderFieldRequester].Value())
	priority := strings.TrimSpace(s.inputs[reorderFieldPriority].Value())
	notes := strings.TrimSpace(s.inputs[reorderFieldNotes].Value())

	req := omsapi.ReorderRequestCreate{
		Item:         s.item.ID,
		Quantity:     qty,
		RequestedBy:  requester,
		Priority:     priority,
		RequestNotes: notes,
	}
	s.pending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.CreateReorderRequest(ctx, req)
		return reorderSubmittedMsg{result: out, err: err}
	}
}

func (s *ReorderFormScreen) View() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Reorder %s (SKU %s, stock %d)\n\n",
		StyleTitle.Render(s.item.Name), s.item.SKU, s.item.Stock))
	if len(s.suppliers) > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("%d supplier(s) available", len(s.suppliers))))
		b.WriteString("\n\n")
	}
	for i, ti := range s.inputs {
		caret := "  "
		if i == s.focused {
			caret = "▸ "
		}
		b.WriteString(caret + StyleMuted.Render(s.labels[i]) + "\n")
		b.WriteString("    " + ti.View() + "\n\n")
	}
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…") + "\n")
	} else if s.resultMsg != "" {
		b.WriteString(RenderStatus(s.resultMsg, s.resultLvl) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab move · enter submit · esc back"))
	return b.String()
}
