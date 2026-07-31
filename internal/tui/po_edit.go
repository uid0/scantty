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
//	header fields    — Supplier order #, Sales order #, Date ordered, Expected
//	                   delivery, Priority, Payment terms, Freight terms, Notes.
//	                   Text inputs except the three terms (op-bwo9), which are
//	                   choice rows cycled with space/←→. Enter on ANY of them
//	                   saves the whole header in one UpdatePurchaseOrder (PATCH),
//	                   the way the web modal saves it.
//	association rows — Work order, Committee (op-shb9): who the whole order was
//	                   placed for. Pickers rather than text, so enter opens a
//	                   list; they sit between the metadata and the lines because
//	                   that is what they are — order-level fields.
//	line rows        — one row per PO line. Not text inputs, so command keys
//	                   land here: enter opens the line editor, w / c re-tag the
//	                   line's own work order / committee, v voids the line
//	                   (reason prompt + confirm).
//
// Sub-phases:
//
//	poEditPhaseLine      — cost / ship-by / notes inputs for the selected line;
//	                       enter saves via UpdatePurchaseOrderLineItem (PATCH).
//	poEditPhaseVoidLine  — reason input + confirm; enter voids the line via
//	                       VoidPurchaseOrderLineItem.
//	poEditPhaseAssoc     — one work-order / committee picker, serving BOTH the
//	                       order-level rows and the per-line keys; enter writes
//	                       just that association (the PO PATCH or update_item).
//
// After any line action the PO is reloaded so the rows reflect the new state.
package tui

import (
	"context"
	"errors"
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
	poEditPhaseAssoc
)

// Metadata field indexes. The two association rows follow them, then the line
// rows: cursor positions poEditMetaCount+poEditAssocCount .. +len(lines)-1.
//
// The header terms (op-bwo9) sit here rather than in a band of their own: they
// are order-level details the same PATCH carries, so one enter saves the header
// as it is shown. Ordinals are positional only — nothing persists them — so
// inserting the four new rows in reading order (identifiers, dates, terms,
// notes) costs nothing.
const (
	poMetaSupplierOrder = iota
	poMetaSalesOrder
	poMetaOrderDate
	poMetaExpectedDelivery
	poMetaPriority
	poMetaPaymentTerms
	poMetaFreightTerms
	poMetaNotes
	poEditMetaCount
)

// Association row offsets, relative to poEditMetaCount. Both rows are ALWAYS
// drawn, even before their option lists land or when a load fails: they are
// order-level fields like the ones above, and a row that appeared or vanished
// with an async response would shift every line beneath it under the operator's
// cursor. What the row can't do yet is said in the row itself.
const (
	poAssocRowWorkOrder = iota
	poAssocRowCommittee
	poEditAssocCount
)

// poAssocField names which association a picker is editing. The two are picked
// the same way and written through the same endpoints, so one phase serves
// both — at order level and at line level alike.
type poAssocField int

const (
	poAssocFieldWorkOrder poAssocField = iota
	poAssocFieldCommittee
)

// poAssocLineOrder is the "line index" that means the order itself rather than
// one of its lines.
const poAssocLineOrder = -1

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
	poMetaOrderDate:        "Date ordered (YYYY-MM-DD)",
	poMetaExpectedDelivery: "Expected delivery (YYYY-MM-DD)",
	poMetaPriority:         "Priority",
	poMetaPaymentTerms:     "Payment terms",
	poMetaFreightTerms:     "Freight terms",
	poMetaNotes:            "Notes",
}

// poHeaderSelect is one header-terms choice row (op-bwo9): the choice set it is
// built from, the options it actually offers (that set plus any unrecognized
// stored token), where the cursor sits in them, and the token the order arrived
// with — which is what tells a real change from a no-op when the header is saved.
type poHeaderSelect struct {
	base     []selectOption
	opts     []selectOption
	idx      int
	original string
}

// set points the row at a stored token and records it as what the order arrived
// with. The token is grafted into the offered options when it is not one this
// build knows, so a row can never display — or save — a value nobody chose.
func (h *poHeaderSelect) set(value string) {
	h.opts = poTermsOptions(h.base, value)
	h.idx = selectIndexOf(h.opts, value)
	h.original = value
}

// picked returns the token the row currently shows.
func (h *poHeaderSelect) picked() string {
	if h.idx < 0 || h.idx >= len(h.opts) {
		return h.original
	}
	return h.opts[h.idx].value
}

// cycle moves the row by delta, wrapping. The choice sets are short and fixed,
// so they cycle in place (the electrical/webhook select idiom) instead of
// opening a sub-phase the way the unbounded association lists have to.
func (h *poHeaderSelect) cycle(delta int) {
	if len(h.opts) == 0 {
		return
	}
	h.idx = (h.idx + delta + len(h.opts)) % len(h.opts)
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

	phase poEditPhase
	// cursor walks three bands: metadata fields, then the two association rows,
	// then one row per line (see poEditLineBase).
	cursor int

	// Metadata inputs, indexed by poMeta* . The choice rows keep an entry here
	// too — unused, but it keeps every row index meaning the same thing in one
	// slice rather than splitting the band across two parallel arrays.
	meta []textinput.Model

	// Header-terms choice rows (op-bwo9), keyed by their poMeta* row. Presence
	// in this map is what makes a row a select: it has no text to type into, so
	// space/←→ cycle it and the focus never lands on an input.
	selects map[int]*poHeaderSelect

	// What the Date-ordered field was hydrated with. The field shows the day
	// alone while the column stores a timestamp, so an untouched field must not
	// be sent back — see saveMetadata.
	origOrderDate string

	// Line editor inputs (poEditPhaseLine), indexed by poLineEdit* .
	lineInputs  []textinput.Model
	lineFocus   int
	editLineIdx int // index into po.Items being edited / voided

	// Void-line reason (poEditPhaseVoidLine).
	voidReason textinput.Model

	// Association pickers (op-shb9). The option lists load once when the screen
	// opens; assocField / assocLineIdx / assocRows / assocCursor describe the
	// picker currently open, whether it was opened from an order-level row or
	// from a line's w / c.
	assoc        poAssocOptions
	assocField   poAssocField
	assocLineIdx int
	assocRows    []poAssocOption
	assocCursor  int
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

// poLineActionMsg reports an action that edits the PO in place — a line edit or
// void, or an association re-tag at either level; success reloads the PO so the
// rows refresh. action is the whole subject-and-verb ("line edited", "order
// association updated") rather than a bare verb, because these actions no
// longer all act on a line.
type poLineActionMsg struct {
	err    error
	action string
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
	s.meta[poMetaOrderDate].Placeholder = "when the order was actually placed"

	// The choice rows exist before the PO does, like the association rows below
	// them: they are fixed rows of the header, and a row that materialised with
	// a load would renumber everything under the operator's cursor. They start
	// on the model's own defaults — normal priority, no terms agreed — and
	// hydrate moves each to the token the order actually carries.
	s.selects = map[int]*poHeaderSelect{
		poMetaPriority:     {base: poPriorityOptions},
		poMetaPaymentTerms: {base: poPaymentTermsOptions},
		poMetaFreightTerms: {base: poFreightTermsOptions},
	}
	s.selects[poMetaPriority].set(poDefaultPriority)
	s.selects[poMetaPaymentTerms].set("")
	s.selects[poMetaFreightTerms].set("")

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

func (s *PurchaseOrderEditScreen) Init() tea.Cmd {
	// The association option lists load in the background: they belong to no
	// supplier and to no line, and nothing on this screen waits on them.
	return tea.Batch(textinput.Blink, s.assoc.load(s.deps))
}

func (s *PurchaseOrderEditScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// hydrate fills the metadata inputs and the choice rows from the loaded PO.
func (s *PurchaseOrderEditScreen) hydrate() {
	s.meta[poMetaSupplierOrder].SetValue(s.po.SupplierOrderNumber)
	s.meta[poMetaSalesOrder].SetValue(s.po.SalesOrderNumber)
	s.meta[poMetaExpectedDelivery].SetValue(s.po.ExpectedDeliveryDate)
	s.meta[poMetaNotes].SetValue(s.po.Notes)

	// Header terms (op-bwo9). The date field shows the DAY the order was placed
	// — that is what the field is for, and what the detail screen shows — while
	// the column underneath stores a timestamp; origOrderDate is what keeps that
	// difference from being written back on an unrelated save.
	s.meta[poMetaOrderDate].SetValue(poFormatOrderDate(s.po.OrderDate))
	s.origOrderDate = s.meta[poMetaOrderDate].Value()
	s.selects[poMetaPriority].set(firstNonEmpty(s.po.Priority, poDefaultPriority))
	s.selects[poMetaPaymentTerms].set(s.po.PaymentTerms)
	s.selects[poMetaFreightTerms].set(s.po.FreightTerms)
}

// isSelectRow reports whether a metadata row is a choice row rather than a text
// input — the one thing every key path in this band has to branch on.
func (s *PurchaseOrderEditScreen) isSelectRow(row int) bool {
	_, ok := s.selects[row]
	return ok
}

func (s *PurchaseOrderEditScreen) lineCount() int {
	if s.po == nil {
		return 0
	}
	return len(s.po.Items)
}

// rowCount is the total navigable rows (metadata fields + association rows +
// line rows).
func (s *PurchaseOrderEditScreen) rowCount() int {
	return poEditMetaCount + poEditAssocCount + s.lineCount()
}

// poEditLineBase is the cursor position of the first line row.
const poEditLineBase = poEditMetaCount + poEditAssocCount

func (s *PurchaseOrderEditScreen) onLineRow() (int, bool) {
	if s.cursor >= poEditLineBase && s.cursor < s.rowCount() {
		return s.cursor - poEditLineBase, true
	}
	return 0, false
}

// onAssocRow reports which order-level association row the cursor is on.
func (s *PurchaseOrderEditScreen) onAssocRow() (int, bool) {
	if s.cursor >= poEditMetaCount && s.cursor < poEditLineBase {
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
			return s, Status(m.action+" failed: "+m.err.Error(), StatusError)
		}
		s.errMsg = ""
		s.phase = poEditPhaseForm
		s.loading = true
		return s, tea.Batch(Status(m.action, StatusOK), s.load())

	case tea.KeyMsg:
		switch s.phase {
		case poEditPhaseLine:
			return s.updateLineEdit(m)
		case poEditPhaseVoidLine:
			return s.updateVoidLine(m)
		case poEditPhaseAssoc:
			return s.updateAssocPick(m)
		default:
			return s.updateForm(m)
		}
	}

	if s.assoc.handle(msg) {
		return s, nil
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
		if s.cursor < poEditMetaCount && !s.isSelectRow(s.cursor) {
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

	if row, ok := s.onAssocRow(); ok {
		// Order-level association rows: enter opens that field's picker. There
		// is nothing to type here, so no other key does anything.
		if m.String() == "enter" {
			field := poAssocFieldWorkOrder
			if row == poAssocRowCommittee {
				field = poAssocFieldCommittee
			}
			return s, s.openAssocPick(field, poAssocLineOrder)
		}
		return s, nil
	}

	if idx, ok := s.onLineRow(); ok {
		// Command keys on a line row (rows aren't text inputs).
		switch m.String() {
		case "enter":
			s.openLineEditor(idx)
			return s, textinput.Blink
		case "w":
			// Re-tag which job THIS line was bought for. Per-line because a
			// single order routinely covers several — the order-level tag says
			// nothing about a line that carries its own.
			return s, s.openAssocPick(poAssocFieldWorkOrder, idx)
		case "c":
			return s, s.openAssocPick(poAssocFieldCommittee, idx)
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
	if sel, ok := s.selects[s.cursor]; ok {
		// A choice row: there is nothing to type into it, so the letter keys are
		// free and space/←→ cycle the value. It stages like the text fields do —
		// enter anywhere in this band saves the header as one PATCH, which is
		// the whole point of keeping the terms in the band rather than giving
		// them the save-alone treatment the associations get.
		switch m.String() {
		case " ", "right":
			sel.cycle(+1)
		case "left":
			sel.cycle(-1)
		}
		return s, nil
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
	if s.cursor < poEditMetaCount && !s.isSelectRow(s.cursor) {
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

	// The header terms (op-bwo9) go out only when they actually moved. The four
	// fields above round-trip losslessly — what was read is what is shown — but
	// these three do not: the date field shows a day where the column holds a
	// timestamp, and a choice row can only show a token this build knows. Sending
	// an untouched one back would rewrite a field nobody edited.
	if orderRaw := strings.TrimSpace(s.meta[poMetaOrderDate].Value()); orderRaw != s.origOrderDate {
		ts, err := poParseOrderDate(orderRaw)
		if err != nil {
			s.errMsg = err.Error()
			return Status(s.errMsg, StatusError)
		}
		req.OrderDate = &ts
	}
	req.Priority = s.changedSelect(poMetaPriority)
	req.PaymentTerms = s.changedSelect(poMetaPaymentTerms)
	req.FreightTerms = s.changedSelect(poMetaFreightTerms)

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

// changedSelect returns the token a header-terms row now shows, or nil when the
// operator left it where the order had it. See saveMetadata for why unchanged
// has to mean "absent from the request" rather than "sent back unchanged".
func (s *PurchaseOrderEditScreen) changedSelect(row int) *string {
	sel, ok := s.selects[row]
	if !ok {
		return nil
	}
	value := sel.picked()
	if value == sel.original {
		return nil
	}
	return &value
}

// poFormatOrderDate hydrates the Date-ordered field with the day the order was
// placed. UTC because the backend runs on it, so this is the same day the
// schedule counts from and the same one the detail screen prints.
func poFormatOrderDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// poParseOrderDate turns the Date-ordered field into what the API wants: an
// RFC3339 timestamp for a DRF DateTimeField, which does not accept a bare date.
// A day becomes midnight UTC — the backend stores UTC, so the day typed is the
// day payment_schedule counts from — and a full timestamp is passed through for
// an operator recording the hour an order actually went out (the maker-box
// datetime-write idiom). Blank is an error rather than a clear: order_date is
// not nullable, so "no order date" is not a state to ask for.
func poParseOrderDate(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", errors.New("date ordered is required — it cannot be cleared")
	}
	if _, err := time.Parse(time.RFC3339, v); err == nil {
		return v, nil
	}
	if _, err := time.Parse("2006-01-02", v); err == nil {
		return v + "T00:00:00Z", nil
	}
	return "", errors.New("date ordered must be YYYY-MM-DD (or an RFC3339 timestamp)")
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
		return poLineActionMsg{err: err, action: "line edited"}
	}
}

// ---------------------------------------------------------------------------
// Association pickers (work order / committee — op-shb9)
// ---------------------------------------------------------------------------

// assocCurrent returns the value and label of what is attached today, at the
// order level (lineIdx == poAssocLineOrder) or on one line.
func (s *PurchaseOrderEditScreen) assocCurrent(field poAssocField, lineIdx int) (value, label string) {
	if s.po == nil {
		// The association rows are navigable before the PO lands (they are
		// fixed rows, not a section that materialises), so this is reachable.
		return "", ""
	}
	var (
		workOrder    string
		workOrderRef *omsapi.WorkOrderRef
		committee    *int
		committeeRef *omsapi.OwningGroupRef
	)
	if lineIdx == poAssocLineOrder {
		workOrder, workOrderRef = s.po.WorkOrder, s.po.WorkOrderRef
		committee, committeeRef = s.po.OwningGroup, s.po.OwningGroupRef
	} else if lineIdx >= 0 && lineIdx < s.lineCount() {
		li := s.po.Items[lineIdx]
		workOrder, workOrderRef = li.WorkOrder, li.WorkOrderRef
		committee, committeeRef = li.OwningGroup, li.OwningGroupRef
	}
	if field == poAssocFieldWorkOrder {
		return workOrder, workOrderRef.Label()
	}
	return poCommitteeValue(committee), poCommitteeRefLabel(committeeRef)
}

// openAssocPick opens the picker for one association. The attached target is
// grafted into the row list when the fetched options don't contain it — the
// pickers offer only unfinished jobs and the viewer's own committees, so an
// order tagged with a since-completed job would otherwise be silently detached
// by an edit that meant to change the other field.
func (s *PurchaseOrderEditScreen) openAssocPick(field poAssocField, lineIdx int) tea.Cmd {
	if s.po == nil {
		return Status("purchase order is still loading", StatusWarn)
	}
	value, label := s.assocCurrent(field, lineIdx)
	rows := s.assoc.workOrderRows(value, label)
	noun, loadErr := "work orders", s.assoc.workOrderErr
	if field == poAssocFieldCommittee {
		rows = s.assoc.committeeRows(value, label)
		noun, loadErr = "committees", s.assoc.committeeErr
	}
	// Only the "none" row: there is nothing to attach and nothing attached to
	// detach, so say why rather than opening an empty list. A failed load says
	// so plainly — "couldn't ask" is a different fact from "there are none".
	if len(rows) <= 1 {
		if loadErr != "" {
			return Status("could not load "+noun+": "+loadErr, StatusError)
		}
		return Status("no "+noun+" available to pick", StatusWarn)
	}
	s.assocField = field
	s.assocLineIdx = lineIdx
	s.assocRows = rows
	s.assocCursor = poAssocCursorFor(rows, value)
	s.errMsg = ""
	s.phase = poEditPhaseAssoc
	return nil
}

func (s *PurchaseOrderEditScreen) updateAssocPick(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		// Nothing is written until enter, so leaving changes nothing.
		s.phase = poEditPhaseForm
		s.syncFocus()
		return s, nil
	case "tab", "down", "j":
		if s.assocCursor < len(s.assocRows)-1 {
			s.assocCursor++
		}
		return s, nil
	case "shift+tab", "up", "k":
		if s.assocCursor > 0 {
			s.assocCursor--
		}
		return s, nil
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.saveAssoc()
	}
	return s, nil
}

// saveAssoc writes the highlighted row through the endpoint that owns it: the
// PO PATCH for an order-level association, update_item for a line's. Only the
// one field is sent — the rest of the order (and of the line) is left alone, so
// re-tagging can't disturb a cost or a date someone else just set. Row 0 sends
// the field as null, which is how the backend detaches an association.
func (s *PurchaseOrderEditScreen) saveAssoc() tea.Cmd {
	if s.assocCursor < 0 || s.assocCursor >= len(s.assocRows) {
		return nil
	}
	value := s.assocRows[s.assocCursor].value
	lineIdx := s.assocLineIdx

	var (
		workOrder *string
		committee *int
	)
	if s.assocField == poAssocFieldWorkOrder {
		workOrder = &value
	} else {
		// "" and the "none" row both mean detach, which putAssociations spells
		// as a 0 pk; poCommitteeID returns nil for the none row, so the zero is
		// supplied here rather than lost.
		id := 0
		if picked := poCommitteeID(value); picked != nil {
			id = *picked
		}
		committee = &id
	}

	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	id := s.poID
	if lineIdx == poAssocLineOrder {
		req := omsapi.PurchaseOrderUpdate{WorkOrder: workOrder, OwningGroup: committee}
		return func() tea.Msg {
			_, err := deps.OMS.UpdatePurchaseOrder(ctx, id, req)
			return poLineActionMsg{err: err, action: "association updated"}
		}
	}
	itemID := fmt.Sprintf("%v", s.po.Items[lineIdx].ID)
	req := omsapi.LineItemUpdate{WorkOrder: workOrder, OwningGroup: committee}
	return func() tea.Msg {
		_, err := deps.OMS.UpdatePurchaseOrderLineItem(ctx, id, itemID, req)
		return poLineActionMsg{err: err, action: "association updated"}
	}
}

// assocRowLabel / assocRowValue render one order-level association row in the
// form. The value branches on load state so an empty picker can never be
// mistaken for an order with nothing attached.
func (s *PurchaseOrderEditScreen) assocRowLabel(row int) string {
	if row == poAssocRowCommittee {
		return "Committee"
	}
	return "Work order"
}

func (s *PurchaseOrderEditScreen) assocRowValue(row int) string {
	field := poAssocFieldWorkOrder
	loadErr, loading := s.assoc.workOrderErr, s.assoc.workOrderLoad
	if row == poAssocRowCommittee {
		field = poAssocFieldCommittee
		loadErr, loading = s.assoc.committeeErr, s.assoc.committeeLoad
	}
	_, label := s.assocCurrent(field, poAssocLineOrder)
	switch {
	case label != "":
		// What is attached is known from the PO itself, so it renders even
		// while the pickable options are still on their way.
		return StyleStatusOK.Render(label)
	case loadErr != "":
		return StyleStatusWarn.Render("(none) · options unavailable — " + loadErr)
	case loading:
		return StyleMuted.Render("(none) · loading options…")
	}
	return StyleMuted.Render("(none)")
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
			return poLineActionMsg{err: err, action: "line voided"}
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
	case poEditPhaseAssoc:
		return s.viewAssocPick()
	default:
		return s.viewForm()
	}
}

func (s *PurchaseOrderEditScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(
		"tab/↑↓ move · type to edit · space/←→ change a choice · enter saves the details · esc back") + "\n\n")

	b.WriteString(StyleTitle.Render("Order details") + "\n")
	for i := 0; i < poEditMetaCount; i++ {
		caret := "  "
		if s.cursor == i {
			caret = "▸ "
		}
		sel, isSelect := s.selects[i]
		if !isSelect {
			b.WriteString(caret + StyleTitle.Render(poMetaLabels[i]+": ") + s.meta[i].View() + "\n")
			continue
		}
		b.WriteString(caret + StyleTitle.Render(poMetaLabels[i]+": ") + elecSelectLabel(sel.opts, sel.idx) + "\n")
		if s.cursor == i {
			// The whole set under the focused row: these are short fixed lists,
			// so showing every label beats cycling blind through six terms to
			// find out what is even on offer.
			b.WriteString("    " + StyleMuted.Render(poSelectStrip(sel)) + "\n")
		}
	}

	// Order-level associations (op-shb9) — who the whole order was placed for.
	// Pickers, not text, so enter opens a list rather than saving the form.
	b.WriteString("\n" + StyleTitle.Render("Ordered for") + "  " +
		StyleMuted.Render("(optional — records who the order is for; changes no cost and bills nobody)") + "\n")
	for i := 0; i < poEditAssocCount; i++ {
		caret := "  "
		if row, ok := s.onAssocRow(); ok && row == i {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(s.assocRowLabel(i)+": ") + s.assocRowValue(i) + "\n")
	}
	if _, ok := s.onAssocRow(); ok {
		b.WriteString(StyleMuted.Render("  enter: pick — saves this association on its own") + "\n")
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
		// The line's own "ordered for", indented under it — a mixed order is
		// the whole reason lines carry associations of their own.
		if orderedFor := poLineOrderedFor(li); orderedFor != "" {
			b.WriteString("    " + StyleMuted.Render("ordered for: "+orderedFor) + "\n")
		}
	}
	if s.lineCount() > 0 {
		b.WriteString("\n" + StyleMuted.Render(
			"on a line: enter edit cost/ship/notes · w work order · c committee · v void line") + "\n")
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

// viewAssocPick draws the open association picker, naming what it is tagging
// (the order, or one line) so an operator who pressed w on a line can't mistake
// it for the order-level field two rows up.
func (s *PurchaseOrderEditScreen) viewAssocPick() string {
	var b strings.Builder
	field := "Work order"
	if s.assocField == poAssocFieldCommittee {
		field = "Committee"
	}
	target := "this purchase order"
	if s.assocLineIdx >= 0 && s.assocLineIdx < s.lineCount() {
		target = fmt.Sprintf("line %d: %s", s.assocLineIdx+1, s.po.Items[s.assocLineIdx].DisplayLabel())
	}
	b.WriteString(StyleTitle.Render(field+" for ") + target + "\n")
	b.WriteString(StyleMuted.Render("Attribution only — it moves no stock and bills no committee.") + "\n\n")
	b.WriteString(renderWindowedList(len(s.assocRows), s.assocCursor,
		func(i int) string { return s.assocRows[i].label }))
	b.WriteString("\n" + StyleMuted.Render("↑↓/j/k move · enter save · esc cancel · row 1 = none") + "\n")
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
