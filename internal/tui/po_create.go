// PurchaseOrderCreateScreen — minimal create-PO form for scantty.
//
// scantty's primary UX is scanner-driven, but the gap "I'm at the workstation
// and want to open a PO without context-switching to the web UI" came up
// often enough to warrant a focused form. The MVP captures one freeform
// line item (description / qty / unit_cost). Supplier is picked from a
// loaded list — j/k to navigate, enter to commit, tab to next field —
// instead of asking the operator to memorise numeric IDs from the web
// UI (uid0 asked for the picker on 2026-06-14).
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
	deps      Deps
	inputs    []textinput.Model
	focused   int
	pending   bool
	errMsg    string
	suppliers []omsapi.Supplier
	supplierLoading bool
	supplierLoadErr string
	// supplierCursor is the highlighted row in the picker; -1 means
	// nothing selected yet.
	supplierCursor int
	// supplierID holds the committed pick. 0 means none.
	supplierID int
}

type poCreatedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

type poCreateSuppliersLoadedMsg struct {
	suppliers []omsapi.Supplier
	err       error
}

func NewPurchaseOrderCreateScreen(deps Deps) *PurchaseOrderCreateScreen {
	s := &PurchaseOrderCreateScreen{deps: deps, supplierLoading: true, supplierCursor: -1}
	s.inputs = make([]textinput.Model, poCreateFieldCount)

	// Supplier slot is a no-op textinput — we never write to it. The
	// picker (j/k over loaded suppliers) drives s.supplierID instead.
	s.inputs[poCreateSupplierID] = textinput.New()

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

func (s *PurchaseOrderCreateScreen) Init() tea.Cmd {
	return tea.Batch(s.loadSuppliers(), textinput.Blink)
}

func (s *PurchaseOrderCreateScreen) loadSuppliers() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		// Pull the lot — there's rarely more than a few dozen suppliers
		// in an OMS install, and a non-paginated picker beats paging
		// inside the form. If a deploy ever has hundreds, slice 2
		// can add a type-to-filter input.
		page, err := deps.OMS.ListSuppliers(ctx, nil)
		if err != nil {
			return poCreateSuppliersLoadedMsg{err: err}
		}
		return poCreateSuppliersLoadedMsg{suppliers: page.Results}
	}
}

func (s *PurchaseOrderCreateScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case poCreateSuppliersLoadedMsg:
		s.supplierLoading = false
		if m.err != nil {
			s.supplierLoadErr = m.err.Error()
			return s, Status("load suppliers failed: "+m.err.Error(), StatusError)
		}
		s.suppliers = m.suppliers
		if len(s.suppliers) > 0 {
			s.supplierCursor = 0
		}
		return s, nil

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
		// Supplier field gets list-picker behaviour: j/k move the
		// highlight, enter commits the pick AND advances to the next
		// field. Tab / shift-tab cycle fields without picking.
		if s.focused == poCreateSupplierID {
			switch m.String() {
			case "esc":
				return s, SwitchTo(WSPurchasing, nil)
			case "j", "down":
				if s.supplierCursor < len(s.suppliers)-1 {
					s.supplierCursor++
				}
				return s, nil
			case "k", "up":
				if s.supplierCursor > 0 {
					s.supplierCursor--
				}
				return s, nil
			case "tab":
				s.commitSupplier()
				s.focusNext(+1)
				return s, nil
			case "shift+tab":
				s.commitSupplier()
				s.focusNext(-1)
				return s, nil
			case "enter":
				if s.pending {
					return s, nil
				}
				// On the supplier field, enter commits the pick and
				// advances; it doesn't submit the form. The operator
				// has to tab through and hit enter on the last field
				// for that.
				s.commitSupplier()
				s.focusNext(+1)
				return s, nil
			}
			return s, nil
		}

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

	// Forward to the focused input (no-op when focused == supplier
	// since that slot's textinput is never read).
	var cmd tea.Cmd
	s.inputs[s.focused], cmd = s.inputs[s.focused].Update(msg)
	return s, cmd
}

func (s *PurchaseOrderCreateScreen) commitSupplier() {
	if s.supplierCursor < 0 || s.supplierCursor >= len(s.suppliers) {
		return
	}
	s.supplierID = s.suppliers[s.supplierCursor].ID
}

func (s *PurchaseOrderCreateScreen) focusNext(delta int) {
	if s.focused != poCreateSupplierID {
		s.inputs[s.focused].Blur()
	}
	s.focused = (s.focused + delta + poCreateFieldCount) % poCreateFieldCount
	if s.focused != poCreateSupplierID {
		s.inputs[s.focused].Focus()
	}
}

func (s *PurchaseOrderCreateScreen) submit() tea.Cmd {
	if s.supplierID <= 0 {
		s.errMsg = "supplier is required (pick from the list at the top of the form)"
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
		Supplier: s.supplierID,
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
	b.WriteString(StyleMuted.Render(
		"Create a purchase order (1 freeform line). j/k to pick a supplier, " +
			"tab/shift-tab to move between fields, enter to submit, esc to cancel.",
	))
	b.WriteString("\n\n")

	// Supplier picker (or its status while loading).
	supplierLabel := "Supplier"
	if s.focused == poCreateSupplierID {
		supplierLabel = "▸ " + supplierLabel
	} else {
		supplierLabel = "  " + supplierLabel
	}
	b.WriteString(StyleTitle.Render(supplierLabel + ":") + " ")
	switch {
	case s.supplierLoading:
		b.WriteString(StyleMuted.Render("loading suppliers…"))
	case s.supplierLoadErr != "":
		b.WriteString(StyleStatusError.Render("✗ " + s.supplierLoadErr))
	case len(s.suppliers) == 0:
		b.WriteString(StyleMuted.Render("(no suppliers configured)"))
	case s.supplierID > 0:
		// Show the committed pick on a single summary line.
		name := ""
		for _, sup := range s.suppliers {
			if sup.ID == s.supplierID {
				name = sup.Name
				break
			}
		}
		b.WriteString(StyleStatusOK.Render(fmt.Sprintf("%s (#%d)", name, s.supplierID)))
	default:
		b.WriteString(StyleMuted.Render("(none picked yet)"))
	}
	b.WriteString("\n")

	// Render the supplier picker list when the supplier field is
	// focused; otherwise keep the form compact so the operator can see
	// the committed pick + the rest of the fields in one frame.
	if s.focused == poCreateSupplierID && !s.supplierLoading && len(s.suppliers) > 0 {
		// Window the list to ~10 entries around the cursor so the
		// form doesn't push the rest of the fields off the screen on
		// a tall supplier directory.
		const window = 10
		start := s.supplierCursor - window/2
		if start < 0 {
			start = 0
		}
		end := start + window
		if end > len(s.suppliers) {
			end = len(s.suppliers)
			start = end - window
			if start < 0 {
				start = 0
			}
		}
		if start > 0 {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("    ↑ %d more above\n", start)))
		}
		for i := start; i < end; i++ {
			sup := s.suppliers[i]
			caret := "    "
			if i == s.supplierCursor {
				caret = "  ▸ "
			}
			line := fmt.Sprintf("%s%s  (#%d)", caret, sup.Name, sup.ID)
			if sup.ID == s.supplierID {
				line += "  " + StyleStatusOK.Render("✓ picked")
			}
			if i == s.supplierCursor {
				line = StyleSidebarItemActive.Render(line)
			}
			b.WriteString(line + "\n")
		}
		if end < len(s.suppliers) {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("    ↓ %d more below\n", len(s.suppliers)-end)))
		}
	}
	b.WriteString("\n")

	// Remaining text fields.
	labels := []string{"Supplier", "Item description", "Quantity", "Unit cost", "Notes"}
	for i := poCreateItemDesc; i < poCreateFieldCount; i++ {
		marker := "  "
		if i == s.focused {
			marker = "▸ "
		}
		b.WriteString(marker)
		b.WriteString(StyleTitle.Render(labels[i] + ": "))
		b.WriteString(s.inputs[i].View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	} else {
		b.WriteString(StyleMuted.Render("Press enter on a non-supplier field to submit."))
	}
	return b.String()
}
