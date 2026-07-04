// PurchaseOrderEditScreen — edit an existing purchase order.
//
// This is the TUI counterpart to the web PurchaseOrderPage's edit affordances
// (frontend/src/pages/PurchaseOrderPage.tsx): the "Edit details" metadata modal
// plus the per-line edit-cost / edit-ship-date / void-line controls. It mirrors
// the FULL set so an operator at the workstation can amend a PO without the
// browser ([[ship-complete-features]]). It follows inventory_item_form.go's
// field-by-field navigation, extended with a line-item section.
//
// Layout (one cursor over two sections):
//
//	metadata fields  — Supplier order #, Sales order #, Expected delivery,
//	                   Notes. Text inputs; type to edit; enter saves the PO
//	                   metadata via UpdatePurchaseOrder (PATCH).
//	line rows        — one row per PO line. Not text inputs, so command keys
//	                   land here: enter opens the line editor, v voids the line
//	                   (reason prompt + confirm).
//
// Sub-phases:
//
//	poEditPhaseLine      — cost / ship-by / notes inputs for the selected line;
//	                       enter saves via UpdatePurchaseOrderLineItem (PATCH).
//	poEditPhaseVoidLine  — reason input + confirm; enter voids the line via
//	                       VoidPurchaseOrderLineItem.
//
// After any line action the PO is reloaded so the rows reflect the new state.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type poEditPhase int

const (
	poEditPhaseForm poEditPhase = iota
	poEditPhaseLine
	poEditPhaseVoidLine
)

// Metadata field indexes. Line rows occupy cursor positions
// poEditMetaCount .. poEditMetaCount+len(lines)-1.
const (
	poMetaSupplierOrder = iota
	poMetaSalesOrder
	poMetaExpectedDelivery
	poMetaNotes
	poEditMetaCount
)

// Line-editor field indexes.
const (
	poLineEditCost = iota
	poLineEditShipDate
	poLineEditNotes
	poLineEditCount
)

var poMetaLabels = map[int]string{
	poMetaSupplierOrder:    "Supplier order #",
	poMetaSalesOrder:       "Sales order #",
	poMetaExpectedDelivery: "Expected delivery (YYYY-MM-DD)",
	poMetaNotes:            "Notes",
}

type PurchaseOrderEditScreen struct {
	deps           Deps
	poID           string
	po             *omsapi.PurchaseOrder
	loading        bool
	loadErr        string
	saving         bool
	errMsg         string
	terminalHeight int

	phase  poEditPhase
	cursor int // 0..poEditMetaCount-1 = metadata field; >= that = line row

	// Metadata inputs, indexed by poMeta* .
	meta []textinput.Model

	// Line editor inputs (poEditPhaseLine), indexed by poLineEdit* .
	lineInputs  []textinput.Model
	lineFocus   int
	editLineIdx int // index into po.Items being edited / voided

	// Void-line reason (poEditPhaseVoidLine).
	voidReason textinput.Model
}

type poEditLoadedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

// poEditSavedMsg reports a metadata save; on success we return to the detail.
type poEditSavedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

// poLineActionMsg reports a per-line edit or void; success reloads the PO in
// place so the row list refreshes.
type poLineActionMsg struct {
	err    error
	action string // "edited" | "voided"
}

// NewPurchaseOrderEditScreen builds the edit form. The passed PO seeds instant
// rendering; the screen also reloads by id so line actions see fresh state.
func NewPurchaseOrderEditScreen(deps Deps, po *omsapi.PurchaseOrder) *PurchaseOrderEditScreen {
	s := &PurchaseOrderEditScreen{
		deps:    deps,
		po:      po,
		loading: false,
	}
	if po != nil {
		s.poID = fmt.Sprintf("%v", po.ID)
	}

	s.meta = make([]textinput.Model, poEditMetaCount)
	for i := range s.meta {
		ti := textinput.New()
		ti.Prompt = ""
		if i == poMetaNotes {
			ti.CharLimit = 1000
		} else {
			ti.CharLimit = 200
		}
		s.meta[i] = ti
	}

	s.lineInputs = make([]textinput.Model, poLineEditCount)
	for i := range s.lineInputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
		s.lineInputs[i] = ti
	}
	s.lineInputs[poLineEditCost].Placeholder = "total line cost, e.g. 125.00"
	s.lineInputs[poLineEditShipDate].Placeholder = "YYYY-MM-DD ('-' or blank clears)"
	s.lineInputs[poLineEditNotes].Placeholder = "line notes (optional)"

	s.voidReason = textinput.New()
	s.voidReason.Prompt = ""
	s.voidReason.CharLimit = 300
	s.voidReason.Placeholder = "reason (e.g. supplier discontinued item)"

	if po != nil {
		s.hydrate()
	}
	s.syncFocus()
	return s
}

func (s *PurchaseOrderEditScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("Edit PO %s", s.po.Number)
	}
	return "Edit purchase order"
}

func (s *PurchaseOrderEditScreen) WantsRawInput() bool { return true }

func (s *PurchaseOrderEditScreen) Init() tea.Cmd { return textinput.Blink }

func (s *PurchaseOrderEditScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// hydrate fills the metadata inputs from the loaded PO.
func (s *PurchaseOrderEditScreen) hydrate() {
	s.meta[poMetaSupplierOrder].SetValue(s.po.SupplierOrderNumber)
	s.meta[poMetaSalesOrder].SetValue(s.po.SalesOrderNumber)
	s.meta[poMetaExpectedDelivery].SetValue(s.po.ExpectedDeliveryDate)
	s.meta[poMetaNotes].SetValue(s.po.Notes)
}

func (s *PurchaseOrderEditScreen) lineCount() int {
	if s.po == nil {
		return 0
	}
	return len(s.po.Items)
}

// rowCount is the total navigable rows (metadata fields + line rows).
func (s *PurchaseOrderEditScreen) rowCount() int { return poEditMetaCount + s.lineCount() }

func (s *PurchaseOrderEditScreen) onLineRow() (int, bool) {
	if s.cursor >= poEditMetaCount && s.cursor < s.rowCount() {
		return s.cursor - poEditMetaCount, true
	}
	return 0, false
}

func (s *PurchaseOrderEditScreen) load() tea.Cmd {
	deps := s.deps
	id := s.poID
	ctx := s.ctx()
	return func() tea.Msg {
		po, err := deps.OMS.GetPurchaseOrder(ctx, id)
		return poEditLoadedMsg{po: po, err: err}
	}
}

func (s *PurchaseOrderEditScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case poEditLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("reload PO failed: "+m.err.Error(), StatusError)
		}
		s.po = m.po
		if s.cursor >= s.rowCount() && s.rowCount() > 0 {
			s.cursor = s.rowCount() - 1
		}
		s.syncFocus()
		return s, nil

	case poEditSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("purchase order details updated", StatusOK),
			SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID)),
		)

	case poLineActionMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("line "+m.action+" failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		s.phase = poEditPhaseForm
		s.loading = true
		return s, tea.Batch(Status("line "+m.action, StatusOK), s.load())

	case tea.KeyMsg:
		switch s.phase {
		case poEditPhaseLine:
			return s.updateLineEdit(m)
		case poEditPhaseVoidLine:
			return s.updateVoidLine(m)
		default:
			return s.updateForm(m)
		}
	}

	// Cursor blink → the focused input.
	switch s.phase {
	case poEditPhaseLine:
		var cmd tea.Cmd
		s.lineInputs[s.lineFocus], cmd = s.lineInputs[s.lineFocus].Update(msg)
		return s, cmd
	case poEditPhaseVoidLine:
		var cmd tea.Cmd
		s.voidReason, cmd = s.voidReason.Update(msg)
		return s, cmd
	default:
		if s.cursor < poEditMetaCount {
			var cmd tea.Cmd
			s.meta[s.cursor], cmd = s.meta[s.cursor].Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Form phase (metadata fields + line rows)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderEditScreen) updateForm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID))
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	}

	if idx, ok := s.onLineRow(); ok {
		// Command keys on a line row (rows aren't text inputs).
		switch m.String() {
		case "enter":
			s.openLineEditor(idx)
			return s, textinput.Blink
		case "v":
			if s.po.Items[idx].IsVoided {
				return s, Status("line is already voided", StatusWarn)
			}
			s.openVoidLine(idx)
			return s, textinput.Blink
		}
		return s, nil
	}

	// Metadata field.
	switch m.String() {
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.saveMetadata()
	}
	var cmd tea.Cmd
	s.meta[s.cursor], cmd = s.meta[s.cursor].Update(m)
	return s, cmd
}

func (s *PurchaseOrderEditScreen) moveCursor(delta int) {
	n := s.rowCount()
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *PurchaseOrderEditScreen) syncFocus() {
	for i := range s.meta {
		s.meta[i].Blur()
	}
	if s.cursor < poEditMetaCount {
		s.meta[s.cursor].Focus()
	}
}

func (s *PurchaseOrderEditScreen) saveMetadata() tea.Cmd {
	exp := strings.TrimSpace(s.meta[poMetaExpectedDelivery].Value())
	if exp != "" {
		if _, err := time.Parse("2006-01-02", exp); err != nil {
			s.errMsg = "expected delivery must be YYYY-MM-DD (or blank to clear)"
			return Status(s.errMsg, StatusError)
		}
	}
	// Send every metadata field the web edit form manages. Expected-delivery
	// empty -> cleared (JSON null) by UpdatePurchaseOrder's pointer-to-"" rule.
	req := omsapi.PurchaseOrderUpdate{
		SupplierOrderNumber:  stringPtr(strings.TrimSpace(s.meta[poMetaSupplierOrder].Value())),
		SalesOrderNumber:     stringPtr(strings.TrimSpace(s.meta[poMetaSalesOrder].Value())),
		ExpectedDeliveryDate: stringPtr(exp),
		Notes:                stringPtr(strings.TrimSpace(s.meta[poMetaNotes].Value())),
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.poID
	return func() tea.Msg {
		po, err := deps.OMS.UpdatePurchaseOrder(ctx, id, req)
		return poEditSavedMsg{po: po, err: err}
	}
}

// ---------------------------------------------------------------------------
// Line editor sub-phase
// ---------------------------------------------------------------------------

func (s *PurchaseOrderEditScreen) openLineEditor(idx int) {
	s.phase = poEditPhaseLine
	s.editLineIdx = idx
	s.lineFocus = poLineEditCost
	s.errMsg = ""
	li := s.po.Items[idx]

	for i := range s.lineInputs {
		s.lineInputs[i].SetValue("")
		s.lineInputs[i].Blur()
	}
	// Prefill line cost from the best available total: actual, else estimated.
	if !li.ActualCost.Empty() {
		s.lineInputs[poLineEditCost].SetValue(string(li.ActualCost))
	} else if !li.EstimatedCost.Empty() {
		s.lineInputs[poLineEditCost].SetValue(string(li.EstimatedCost))
	}
	s.lineInputs[poLineEditShipDate].SetValue(li.ExpectedShipmentDate)
	s.lineInputs[poLineEditNotes].SetValue(li.Notes)
	s.lineInputs[s.lineFocus].Focus()
}

func (s *PurchaseOrderEditScreen) updateLineEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poEditPhaseForm
		s.syncFocus()
		return s, nil
	case "tab", "down":
		s.lineInputs[s.lineFocus].Blur()
		s.lineFocus = (s.lineFocus + 1) % poLineEditCount
		s.lineInputs[s.lineFocus].Focus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.lineInputs[s.lineFocus].Blur()
		s.lineFocus = (s.lineFocus - 1 + poLineEditCount) % poLineEditCount
		s.lineInputs[s.lineFocus].Focus()
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.saveLine()
	}
	var cmd tea.Cmd
	s.lineInputs[s.lineFocus], cmd = s.lineInputs[s.lineFocus].Update(m)
	return s, cmd
}

func (s *PurchaseOrderEditScreen) saveLine() tea.Cmd {
	li := s.po.Items[s.editLineIdx]
	itemID := fmt.Sprintf("%v", li.ID)

	req := omsapi.LineItemUpdate{}

	costRaw := strings.TrimSpace(s.lineInputs[poLineEditCost].Value())
	if costRaw != "" {
		cost, err := strconv.ParseFloat(costRaw, 64)
		if err != nil || cost < 0 {
			s.errMsg = "line cost must be a non-negative number"
			return Status(s.errMsg, StatusError)
		}
		req.LineCost = &cost
	}

	// Ship date: blank or '-' clears (backend maps "" -> NULL); else validate.
	shipRaw := strings.TrimSpace(s.lineInputs[poLineEditShipDate].Value())
	if shipRaw == "-" {
		shipRaw = ""
	}
	if shipRaw != "" {
		if _, err := time.Parse("2006-01-02", shipRaw); err != nil {
			s.errMsg = "ship date must be YYYY-MM-DD (or '-'/blank to clear)"
			return Status(s.errMsg, StatusError)
		}
	}
	req.ExpectedShipmentDate = stringPtr(shipRaw)
	req.Notes = stringPtr(strings.TrimSpace(s.lineInputs[poLineEditNotes].Value()))

	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.poID
	return func() tea.Msg {
		_, err := deps.OMS.UpdatePurchaseOrderLineItem(ctx, id, itemID, req)
		return poLineActionMsg{err: err, action: "edited"}
	}
}

// ---------------------------------------------------------------------------
// Void-line sub-phase
// ---------------------------------------------------------------------------

func (s *PurchaseOrderEditScreen) openVoidLine(idx int) {
	s.phase = poEditPhaseVoidLine
	s.editLineIdx = idx
	s.errMsg = ""
	s.voidReason.SetValue("")
	s.voidReason.Focus()
}

func (s *PurchaseOrderEditScreen) updateVoidLine(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poEditPhaseForm
		s.voidReason.Blur()
		s.syncFocus()
		return s, nil
	case "enter":
		if s.saving {
			return s, nil
		}
		reason := strings.TrimSpace(s.voidReason.Value())
		if reason == "" {
			s.errMsg = "a reason is required to void a line"
			return s, Status(s.errMsg, StatusError)
		}
		li := s.po.Items[s.editLineIdx]
		itemID := fmt.Sprintf("%v", li.ID)
		s.saving = true
		s.errMsg = ""
		deps := s.deps
		ctx := s.ctx()
		id := s.poID
		return s, func() tea.Msg {
			_, err := deps.OMS.VoidPurchaseOrderLineItem(ctx, id, itemID, reason)
			return poLineActionMsg{err: err, action: "voided"}
		}
	}
	var cmd tea.Cmd
	s.voidReason, cmd = s.voidReason.Update(m)
	return s, cmd
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *PurchaseOrderEditScreen) View() string {
	if s.po == nil {
		if s.loadErr != "" {
			return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
		}
		return StyleMuted.Render("Loading purchase order…")
	}
	switch s.phase {
	case poEditPhaseLine:
		return s.viewLineEdit()
	case poEditPhaseVoidLine:
		return s.viewVoidLine()
	default:
		return s.viewForm()
	}
}

func (s *PurchaseOrderEditScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("tab/↑↓ move · type to edit a field · enter on a field saves details · esc back") + "\n\n")

	b.WriteString(StyleTitle.Render("Order details") + "\n")
	for i := 0; i < poEditMetaCount; i++ {
		caret := "  "
		if s.cursor == i {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(poMetaLabels[i]+": ") + s.meta[i].View() + "\n")
	}

	b.WriteString("\n" + StyleTitle.Render(fmt.Sprintf("Line items (%d)", s.lineCount())) + "\n")
	if s.lineCount() == 0 {
		b.WriteString(StyleMuted.Render("  (no lines)") + "\n")
	}
	for i, li := range s.po.Items {
		caret := "  "
		if row, ok := s.onLineRow(); ok && row == i {
			caret = "▸ "
		}
		label := li.DisplayLabel()
		line := fmt.Sprintf("%s%d) %s ×%d", caret, i+1, label, li.QuantityOrdered)
		cost := ""
		if !li.ActualCost.Empty() {
			cost = "$" + string(li.ActualCost)
		} else if !li.EstimatedCost.Empty() {
			cost = "$" + string(li.EstimatedCost)
		}
		if cost != "" {
			line += " · " + cost
		}
		if li.ExpectedShipmentDate != "" {
			line += " · ship " + li.ExpectedShipmentDate
		}
		if li.IsVoided {
			line += " " + StyleStatusWarn.Render("[voided]")
		}
		if row, ok := s.onLineRow(); ok && row == i {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if s.lineCount() > 0 {
		b.WriteString("\n" + StyleMuted.Render("on a line: enter edit cost/ship/notes · v void line") + "\n")
	}

	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *PurchaseOrderEditScreen) viewLineEdit() string {
	var b strings.Builder
	li := s.po.Items[s.editLineIdx]
	b.WriteString(StyleTitle.Render("Edit line: ") + li.DisplayLabel() + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ordered %d · received %d", li.QuantityOrdered, li.QuantityReceived)) + "\n\n")

	labels := []string{"Total line cost ($)", "Expected ship date", "Notes"}
	for i := 0; i < poLineEditCount; i++ {
		caret := "  "
		if i == s.lineFocus {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(labels[i]+": ") + s.lineInputs[i].View() + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab/↑↓ move · enter save · esc cancel") + "\n")
	if s.saving {
		b.WriteString("\n" + StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.errMsg))
	}
	return b.String()
}

func (s *PurchaseOrderEditScreen) viewVoidLine() string {
	var b strings.Builder
	li := s.po.Items[s.editLineIdx]
	b.WriteString(StyleStatusWarn.Render("Void line item") + "\n\n")
	b.WriteString(StyleMuted.Render("Line: ") + li.DisplayLabel() + "\n")
	b.WriteString(StyleMuted.Render("This marks the line voided and the supplier link discontinued.") + "\n\n")
	b.WriteString(StyleTitle.Render("Reason: ") + s.voidReason.View() + "\n")
	b.WriteString("\n" + StyleMuted.Render("enter void · esc cancel") + "\n")
	if s.saving {
		b.WriteString("\n" + StyleMuted.Render("Voiding…"))
	} else if s.errMsg != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.errMsg))
	}
	return b.String()
}

// stringPtr returns a pointer to s. Used to send a metadata/line field even
// when empty (an empty expected-delivery/ship-date is the clear signal).
func stringPtr(s string) *string { return &s }
