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

type ReceiveFormScreen struct {
	deps    Deps
	po      *omsapi.PurchaseOrder
	lines   []omsapi.ItemSupplier
	qty     []textinput.Model
	notes   textinput.Model
	focused int
	pending bool
	result  string
	level   StatusLevel
}

type receiveSubmittedMsg struct {
	receipt *omsapi.Receipt
	err     error
}

func NewReceiveFormScreen(deps Deps, po *omsapi.PurchaseOrder, lines []omsapi.ItemSupplier) *ReceiveFormScreen {
	s := &ReceiveFormScreen{deps: deps, po: po, lines: lines}
	for range lines {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 8
		ti.Placeholder = "0"
		s.qty = append(s.qty, ti)
	}
	s.notes = textinput.New()
	s.notes.Prompt = ""
	s.notes.CharLimit = 200
	s.notes.Placeholder = "optional notes"
	if len(s.qty) > 0 {
		s.qty[0].Focus()
	} else {
		s.notes.Focus()
	}
	return s
}

func (s *ReceiveFormScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("Receive %s", s.po.Number)
	}
	return "Receive Items"
}

func (s *ReceiveFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ReceiveFormScreen) totalInputs() int { return len(s.qty) + 1 }

func (s *ReceiveFormScreen) currentInput() *textinput.Model {
	if s.focused < len(s.qty) {
		return &s.qty[s.focused]
	}
	return &s.notes
}

func (s *ReceiveFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case receiveSubmittedMsg:
		s.pending = false
		if m.err != nil {
			s.result = "submit failed: " + m.err.Error()
			s.level = StatusError
			return s, Status(s.result, StatusError)
		}
		s.result = fmt.Sprintf("receipt #%d created", m.receipt.ID)
		s.level = StatusOK
		return s, Status(s.result, StatusOK)
	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyTab, tea.KeyDown, tea.KeyShiftTab, tea.KeyUp:
			s.focusNext(m.Type == tea.KeyShiftTab || m.Type == tea.KeyUp)
			return s, nil
		case tea.KeyEnter:
			if s.focused < s.totalInputs()-1 {
				s.focusNext(false)
				return s, nil
			}
			return s.submit()
		case tea.KeyEsc:
			return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, strconv.Itoa(s.po.ID)))
		}
	}
	var cmd tea.Cmd
	if s.focused < len(s.qty) {
		s.qty[s.focused], cmd = s.qty[s.focused].Update(msg)
	} else {
		s.notes, cmd = s.notes.Update(msg)
	}
	return s, cmd
}

func (s *ReceiveFormScreen) focusNext(reverse bool) {
	s.currentInput().Blur()
	if reverse {
		s.focused--
		if s.focused < 0 {
			s.focused = s.totalInputs() - 1
		}
	} else {
		s.focused = (s.focused + 1) % s.totalInputs()
	}
	s.currentInput().Focus()
}

func (s *ReceiveFormScreen) submit() (Screen, tea.Cmd) {
	var items []omsapi.ReceiptLine
	for i, ti := range s.qty {
		raw := strings.TrimSpace(ti.Value())
		if raw == "" {
			continue
		}
		qty, err := strconv.Atoi(raw)
		if err != nil || qty < 0 {
			s.result = fmt.Sprintf("line %d: quantity must be a non-negative integer", i+1)
			s.level = StatusError
			return s, nil
		}
		if qty == 0 {
			continue
		}
		items = append(items, omsapi.ReceiptLine{ItemSupplier: s.lines[i].ID, QtyReceived: qty})
	}
	if len(items) == 0 {
		s.result = "no quantities entered"
		s.level = StatusWarn
		return s, nil
	}
	req := omsapi.ReceiptCreate{
		PurchaseOrder: s.po.ID,
		Items:         items,
		Notes:         strings.TrimSpace(s.notes.Value()),
	}
	s.pending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.CreateReceipt(ctx, req)
		return receiveSubmittedMsg{receipt: out, err: err}
	}
}

func (s *ReceiveFormScreen) View() string {
	var b strings.Builder
	header := s.po.Number
	if header == "" {
		header = fmt.Sprintf("PO #%d", s.po.ID)
	}
	b.WriteString(StyleTitle.Render("Receive items into "+header) + "\n\n")
	if len(s.qty) == 0 {
		b.WriteString(StyleMuted.Render("No supplier lines visible for this PO. Enter free-text receipt notes only.") + "\n\n")
	} else {
		for i, ti := range s.qty {
			caret := "  "
			if i == s.focused {
				caret = "▸ "
			}
			line := s.lines[i]
			b.WriteString(fmt.Sprintf("%sitem %d · sku %s · pack %d\n", caret, line.Item, line.SupplierSKU, line.PackQuantity))
			b.WriteString("    qty received: " + ti.View() + "\n\n")
		}
	}
	caret := "  "
	if s.focused == len(s.qty) {
		caret = "▸ "
	}
	b.WriteString(caret + StyleMuted.Render("Notes") + "\n")
	b.WriteString("    " + s.notes.View() + "\n\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…") + "\n")
	} else if s.result != "" {
		b.WriteString(RenderStatus(s.result, s.level) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab move · enter submit · esc back"))
	return b.String()
}
