// PurchaseOrderCreateScreen — minimal create-PO form for scantty.
//
// scantty's primary UX is scanner-driven, but the gap "I'm at the workstation
// and want to open a PO without context-switching to the web UI" came up
// often enough to warrant a focused form. The MVP captures one freeform
// line item (description / qty / unit_cost). Supplier is entered as a
// numeric ID — operators read it off the existing Purchasing list (or the
// web UI's Suppliers page) since a full picker would double the screen's
// surface area. Multi-line POs and item-supplier / asset selection are
// follow-ups; once an operator can land the FIRST line, the backend
// supports adding more via subsequent edits and the receive flow is
// already covered by ReceiveFormScreen.
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


// Field indexes. Keeps the focus / submit logic readable.
const (
	poCreateSupplierID = iota
	poCreateItemDesc
	poCreateQuantity
	poCreateUnitCost
	poCreateNotes
	poCreateFieldCount
)

type PurchaseOrderCreateScreen struct {
	deps    Deps
	inputs  []textinput.Model
	focused int
	pending bool
	errMsg  string
}

type poCreatedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

func NewPurchaseOrderCreateScreen(deps Deps) *PurchaseOrderCreateScreen {
	s := &PurchaseOrderCreateScreen{deps: deps}
	s.inputs = make([]textinput.Model, poCreateFieldCount)

	supplier := textinput.New()
	supplier.Prompt = ""
	supplier.Placeholder = "supplier id (numeric — see Purchasing list)"
	supplier.CharLimit = 10
	supplier.Focus()
	s.inputs[poCreateSupplierID] = supplier

	desc := textinput.New()
	desc.Prompt = ""
	desc.Placeholder = "item description (freeform line)"
	desc.CharLimit = 200
	s.inputs[poCreateItemDesc] = desc

	qty := textinput.New()
	qty.Prompt = ""
	qty.Placeholder = "quantity"
	qty.CharLimit = 10
	s.inputs[poCreateQuantity] = qty

	cost := textinput.New()
	cost.Prompt = ""
	cost.Placeholder = "unit cost (optional, e.g. 12.50)"
	cost.CharLimit = 20
	s.inputs[poCreateUnitCost] = cost

	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "notes (optional)"
	notes.CharLimit = 500
	s.inputs[poCreateNotes] = notes

	return s
}

func (s *PurchaseOrderCreateScreen) Title() string { return "New purchase order" }

func (s *PurchaseOrderCreateScreen) WantsRawInput() bool { return true }

func (s *PurchaseOrderCreateScreen) Init() tea.Cmd { return textinput.Blink }

func (s *PurchaseOrderCreateScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case poCreatedMsg:
		s.pending = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("create PO failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		poNum := ""
		if m.po != nil {
			poNum = m.po.Number
		}
		// Return to Purchasing list so the operator sees the new PO
		// in context (and can drill in if they want to add more items).
		return s, tea.Batch(
			Status(fmt.Sprintf("created %s", poNum), StatusOK),
			SwitchTo(WSPurchasing, nil),
		)

	case tea.KeyMsg:
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSPurchasing, nil)
		case "tab", "down":
			s.focusNext(+1)
			return s, nil
		case "shift+tab", "up":
			s.focusNext(-1)
			return s, nil
		case "enter":
			if s.pending {
				return s, nil
			}
			return s, s.submit()
		}
	}

	// Forward to the focused input.
	var cmd tea.Cmd
	s.inputs[s.focused], cmd = s.inputs[s.focused].Update(msg)
	return s, cmd
}

func (s *PurchaseOrderCreateScreen) focusNext(delta int) {
	s.inputs[s.focused].Blur()
	s.focused = (s.focused + delta + poCreateFieldCount) % poCreateFieldCount
	s.inputs[s.focused].Focus()
}

func (s *PurchaseOrderCreateScreen) submit() tea.Cmd {
	supplierID, err := strconv.Atoi(strings.TrimSpace(s.inputs[poCreateSupplierID].Value()))
	if err != nil || supplierID <= 0 {
		s.errMsg = "supplier id must be a positive integer"
		return Status(s.errMsg, StatusError)
	}
	desc := strings.TrimSpace(s.inputs[poCreateItemDesc].Value())
	if desc == "" {
		s.errMsg = "item description is required"
		return Status(s.errMsg, StatusError)
	}
	qty, err := strconv.Atoi(strings.TrimSpace(s.inputs[poCreateQuantity].Value()))
	if err != nil || qty <= 0 {
		s.errMsg = "quantity must be a positive integer"
		return Status(s.errMsg, StatusError)
	}

	line := omsapi.PurchaseOrderCreateItem{
		Description: desc,
		Quantity:    qty,
	}
	costRaw := strings.TrimSpace(s.inputs[poCreateUnitCost].Value())
	if costRaw != "" {
		cost, err := strconv.ParseFloat(costRaw, 64)
		if err != nil || cost < 0 {
			s.errMsg = "unit cost must be a non-negative number"
			return Status(s.errMsg, StatusError)
		}
		line.UnitCost = &cost
	}

	req := omsapi.PurchaseOrderCreate{
		Supplier: supplierID,
		Notes:    strings.TrimSpace(s.inputs[poCreateNotes].Value()),
		Items:    []omsapi.PurchaseOrderCreateItem{line},
	}

	s.pending = true
	s.errMsg = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		po, err := deps.OMS.CreatePurchaseOrder(ctx, req)
		return poCreatedMsg{po: po, err: err}
	}
}

func (s *PurchaseOrderCreateScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Create a purchase order (1 freeform line). Tab/shift-tab to move, enter to submit, esc to cancel."))
	b.WriteString("\n\n")

	labels := []string{"Supplier ID", "Item description", "Quantity", "Unit cost", "Notes"}
	for i, in := range s.inputs {
		marker := "  "
		if i == s.focused {
			marker = "▸ "
		}
		b.WriteString(marker)
		b.WriteString(StyleTitle.Render(labels[i] + ": "))
		b.WriteString(in.View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	} else {
		b.WriteString(StyleMuted.Render("Press enter to submit."))
	}
	return b.String()
}
