// PurchaseOrderEditScreen — edit an existing purchase order.
//
// This is the TUI counterpart to the web PurchaseOrderPage's edit affordances
// (frontend/src/pages/PurchaseOrderPage.tsx): the "Edit details" metadata modal
// plus the per-line edit-cost / edit-ship-date / void-line controls. It mirrors
// the FULL set so an operator at the workstation can amend a PO without the
// browser ([[ship-complete-features]]).
//
// It is also the PILOT of the columnar "JD Edwards" redesign (sc-h412): it
// renders through jde_form.go's shared layer rather than hand-rolling a
// "caret + label: + value" line, and it is navigated with four reliable keys
// and a persistent action bar instead of letter accelerators. The whole key
// scheme, on every phase of this screen:
//
//	Up/Down, Tab/Shift-Tab   move between fields
//	PgUp/PgDn                page, when the body is taller than the pane
//	Enter                    SAVE the record this phase is editing
//	Ctrl-E                   OPEN what the highlighted row is (a picker, the
//	                         line editor, the void prompt, the last price on file)
//	←/→ or space             change a "< value >" choice row
//	Esc                      back / cancel  (Ctrl-C always quits, app-wide)
//
// Nothing else is bound: the w / c / v accelerators that used to hide on the
// line rows are now rows of the line editor, reached with Ctrl-E, and the bar
// at the bottom names exactly the keys that apply where the cursor is standing.
//
// Layout (one cursor over three bands):
//
//	header fields    — Supplier order #, Sales order #, Date ordered, Expected
//	                   delivery, Priority, Payment terms, Freight terms, Notes.
//	                   Text inputs except the three terms (op-bwo9), which are
//	                   choice rows. Enter on ANY of them saves the whole header
//	                   in one UpdatePurchaseOrder (PATCH), the way the web modal
//	                   saves it.
//	association rows — Work order, Committee (op-shb9): who the whole order was
//	                   placed for. Pickers rather than text, so Ctrl-E opens a
//	                   list; they sit between the metadata and the lines because
//	                   that is what they are — order-level fields.
//	line rows        — a JDE-style detail grid, one row per PO line. Ctrl-E
//	                   opens that line's editor.
//
// Sub-phases:
//
//	poEditPhaseLine      — the selected line's own columnar form: cost / ship-by
//	                       / notes inputs, then its work order, its committee
//	                       and its status. Enter saves ship/notes — and the cost
//	                       ONLY if the operator changed it — via
//	                       UpdatePurchaseOrderLineItem (PATCH); Ctrl-E on one of
//	                       the last three rows opens the picker or the void
//	                       prompt that owns it, and on the cost row takes the
//	                       last price on file for a line that carries none. What a
//	                       cost field may and may not send is po_line_price.go.
//	poEditPhaseVoidLine  — reason input; enter voids the line via
//	                       VoidPurchaseOrderLineItem.
//	poEditPhaseAssoc     — one work-order / committee picker, serving BOTH the
//	                       order-level rows and the line editor's; enter writes
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

// Line-editor text inputs. These three are what one enter saves through
// update_item, so they are the ones with a textinput behind them.
const (
	poLineEditCost = iota
	poLineEditShipDate
	poLineEditNotes
	poLineEditInputCount
)

// The rest of the line editor's rows. They are the affordances that used to be
// bare letters on a line row (w / c / v): each one now has a row of its own
// that shows what is set today and is opened with Ctrl-E. Each writes on its
// own — a re-tag must not carry along a cost the operator never touched — so
// they sit below the inputs rather than joining the line's enter-save.
const (
	poLineRowWorkOrder = poLineEditInputCount + iota
	poLineRowCommittee
	poLineRowStatus
	poLineEditCount
)

var poMetaLabels = map[int]string{
	poMetaSupplierOrder:    "Supplier order #",
	poMetaSalesOrder:       "Sales order #",
	poMetaOrderDate:        "Date ordered",
	poMetaExpectedDelivery: "Expected delivery",
	poMetaPriority:         "Priority",
	poMetaPaymentTerms:     "Payment terms",
	poMetaFreightTerms:     "Freight terms",
	poMetaNotes:            "Notes",
}

// poMetaHints carry the format notes that used to live inside the labels. In a
// columnar form the label column is shared by every field, so a parenthetical
// on one label would push every input area right; a hint rides after the input
// instead, where it costs nobody else anything.
var poMetaHints = map[int]string{
	poMetaOrderDate:        "YYYY-MM-DD · when the order was actually placed",
	poMetaExpectedDelivery: "YYYY-MM-DD · blank clears",
}

// poMetaWidths size the input areas that are not the default: a date is twelve
// columns wide however much room the form has, and the notes field is the one
// place an operator writes a sentence.
var poMetaWidths = map[int]int{
	poMetaOrderDate:        12,
	poMetaExpectedDelivery: 12,
	poMetaNotes:            40,
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

// label names the option the row currently shows — what goes between the
// columnar row's angle brackets. An unrecognized token labels itself (see
// poTermsOptions), so a row can never read as a value nobody chose.
func (h *poHeaderSelect) label() string {
	if h.idx < 0 || h.idx >= len(h.opts) {
		return poTermsLabel(h.base, h.original)
	}
	return h.opts[h.idx].label
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
	deps    Deps
	poID    string
	po      *omsapi.PurchaseOrder
	loading bool
	loadErr string
	saving  bool
	errMsg  string
	// jdeScreen carries the pane geometry and the frame (jde_form.go). Embedded
	// rather than copied, so terminalHeight/terminalWidth and frame() read here
	// exactly as they did when this screen owned them.
	jdeScreen

	phase poEditPhase
	// subReturn is the phase esc goes back to from a picker or the void
	// prompt. Both are opened from two places now — the order-level rows and
	// the line editor — and cancelling has to land where the operator was.
	subReturn poEditPhase
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

	// What openLineEditor put in the cost field. The field is prefilled so the
	// operator can SEE the line's price, and a shown price must not thereby be
	// re-submitted: saveLine compares against this and sends line_cost only
	// when the two differ as AMOUNTS (see po_line_price.go for why that
	// matters, and why comparing the text was not enough).
	lineCostShown string

	// Set by Ctrl-E on the cost row: the operator asked for the price the row
	// is showing to be written as it stands. Without it there is no keystroke
	// that means "save this figure" for a line whose field already holds the
	// figure — which is precisely the line pinned at $0.00, whose prefill
	// recovered its estimate and which therefore has nothing different to type.
	// Cleared by openLineEditor, so arming one line never arms the next.
	lineCostConfirmed bool

	// Last recorded prices for lines that carry none, from the item purchase
	// history (po_line_price.go). Keyed by inventory item, so an order with the
	// same item on two lines asks once.
	lastPaid poLastPaidCache

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

	// Only the first three line-editor rows are typed into; the rest show what
	// another gesture sets. Their format notes ride as hints beside the field
	// (see poLineEditHints) rather than as placeholders inside it, which in a
	// fixed-width columnar area would overrun the column.
	s.lineInputs = make([]textinput.Model, poLineEditInputCount)
	for i := range s.lineInputs {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
		s.lineInputs[i] = ti
	}

	s.voidReason = textinput.New()
	s.voidReason.Prompt = ""
	s.voidReason.CharLimit = 300

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
		s.setSize(m)
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
		// A reload can return fewer lines than the last one did (a line voided
		// elsewhere, an order amended). editLineIdx addresses po.Items directly
		// in the line editor and the void prompt, so it has to come back inside
		// the slice with the cursor rather than panic the next time one opens.
		if s.editLineIdx >= s.lineCount() {
			s.editLineIdx = 0
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
	if s.lastPaid.handle(msg) {
		return s, nil
	}

	// Cursor blink → the focused input.
	switch s.phase {
	case poEditPhaseLine:
		row, ok := s.lineInputRow()
		if !ok {
			return s, nil
		}
		var cmd tea.Cmd
		s.lineInputs[row], cmd = s.lineInputs[row].Update(msg)
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
	// The system keys, first and everywhere: they mean the same thing on every
	// row of the form, which is the whole point of the reduced scheme.
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID))
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "pgdown":
		s.pageCursor(+1)
		return s, textinput.Blink
	case "pgup":
		s.pageCursor(-1)
		return s, textinput.Blink
	case "enter":
		// SUBMIT saves the record — the order header — from any row, so an
		// operator never has to navigate back to a text field to save. The
		// association rows and the lines write through their own endpoints and
		// deliberately stay out of it (see saveAssoc).
		if s.saving {
			return s, nil
		}
		return s, s.saveMetadata()
	case "ctrl+e":
		return s, s.openFocusedRow()
	}

	if _, ok := s.onAssocRow(); ok {
		// Nothing to type on a picker row, and no accelerators left to press.
		return s, nil
	}
	if _, ok := s.onLineRow(); ok {
		return s, nil
	}

	if sel, ok := s.selects[s.cursor]; ok {
		// A choice row: there is nothing to type into it, so space/←→ cycle the
		// value in place. It stages like the text fields around it — the enter
		// above saves the header as one PATCH, which is the whole point of
		// keeping the terms in this band rather than giving them the save-alone
		// treatment the associations get.
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

// openFocusedRow is what Ctrl-E does on the header form: it opens whatever the
// highlighted row IS. A picker row opens its picker, a line row opens that
// line's editor, and a row you simply type into opens nothing — which is why
// the action bar drops the Ctrl-E entry there rather than offering a key that
// would do nothing.
func (s *PurchaseOrderEditScreen) openFocusedRow() tea.Cmd {
	if row, ok := s.onAssocRow(); ok {
		field := poAssocFieldWorkOrder
		if row == poAssocRowCommittee {
			field = poAssocFieldCommittee
		}
		return s.openAssocPick(field, poAssocLineOrder)
	}
	if idx, ok := s.onLineRow(); ok {
		return tea.Batch(s.openLineEditor(idx), textinput.Blink)
	}
	return nil
}

func (s *PurchaseOrderEditScreen) moveCursor(delta int) {
	n := s.rowCount()
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

// pageCursor moves by one screenful of rows. It CLAMPS where moveCursor wraps:
// paging is a way of covering ground in a body too tall for the pane, and a
// page that jumped from the last line back to the first would lose the
// operator's place rather than save them keystrokes.
func (s *PurchaseOrderEditScreen) pageCursor(dir int) {
	if s.rowCount() == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, s.rowCount(), s.pageStep(), dir)
	s.syncFocus()
}

// pageStep is how many navigable rows the pane is currently showing — computed
// from the same lines View draws, so a page moves by exactly what the operator
// can see rather than by a guessed constant.
func (s *PurchaseOrderEditScreen) pageStep() int {
	return s.windowRows(s.formLines(), s.cursor, 0)
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

// openLineEditor opens one line's form. It returns the command that looks up
// the last price recorded for the item, which only runs for a line that has no
// price of its own to show.
func (s *PurchaseOrderEditScreen) openLineEditor(idx int) tea.Cmd {
	s.phase = poEditPhaseLine
	s.editLineIdx = idx
	s.lineFocus = poLineEditCost
	s.errMsg = ""
	li := s.po.Items[idx]

	for i := range s.lineInputs {
		s.lineInputs[i].SetValue("")
		s.lineInputs[i].Blur()
	}
	// Prefill the cost with the price the line already carries, on the basis
	// update_item reads it back on — the total for the quantity ORDERED, never
	// actual_cost's received-so-far subtotal (po_line_price.go). Remember it, so
	// the save can tell a price the operator typed from one it merely showed.
	s.lineCostShown = poLineCarriedCost(li)
	s.lineCostConfirmed = false
	s.lineInputs[poLineEditCost].SetValue(s.lineCostShown)
	s.lineInputs[poLineEditShipDate].SetValue(li.ExpectedShipmentDate)
	s.lineInputs[poLineEditNotes].SetValue(li.Notes)
	s.syncLineFocus()

	if s.lineCostShown != "" {
		return nil
	}
	return s.lastPaid.load(s.deps, poLineItemID(li), s.poID)
}

// lineInputRow reports whether the line editor's cursor is on one of the three
// rows that has a textinput behind it.
func (s *PurchaseOrderEditScreen) lineInputRow() (int, bool) {
	if s.lineFocus >= 0 && s.lineFocus < poLineEditInputCount {
		return s.lineFocus, true
	}
	return 0, false
}

// syncLineFocus focuses the line editor's input for the cursor's row, and none
// when the cursor is on one of the rows Ctrl-E opens instead.
func (s *PurchaseOrderEditScreen) syncLineFocus() {
	for i := range s.lineInputs {
		s.lineInputs[i].Blur()
	}
	if row, ok := s.lineInputRow(); ok {
		s.lineInputs[row].Focus()
	}
}

func (s *PurchaseOrderEditScreen) updateLineEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poEditPhaseForm
		s.syncFocus()
		return s, nil
	case "tab", "down":
		s.lineFocus = (s.lineFocus + 1) % poLineEditCount
		s.syncLineFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.lineFocus = (s.lineFocus - 1 + poLineEditCount) % poLineEditCount
		s.syncLineFocus()
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s, s.saveLine()
	case "ctrl+e":
		return s, s.openLineRow()
	}
	row, ok := s.lineInputRow()
	if !ok {
		return s, nil
	}
	var cmd tea.Cmd
	s.lineInputs[row], cmd = s.lineInputs[row].Update(m)
	return s, cmd
}

// openLineRow is Ctrl-E inside the line editor: the three rows below the inputs
// are the per-line affordances that used to be the bare w / c / v keys, and
// each opens the surface that owns it. They are separate from the line's
// enter-save on purpose — re-tagging which job a line was bought for must not
// carry along a cost or a ship date someone else set while this form was open.
func (s *PurchaseOrderEditScreen) openLineRow() tea.Cmd {
	if s.po == nil || s.editLineIdx < 0 || s.editLineIdx >= s.lineCount() {
		return nil
	}
	switch s.lineFocus {
	case poLineEditCost:
		return s.costRowAction()
	case poLineRowWorkOrder:
		return s.openAssocPick(poAssocFieldWorkOrder, s.editLineIdx)
	case poLineRowCommittee:
		return s.openAssocPick(poAssocFieldCommittee, s.editLineIdx)
	case poLineRowStatus:
		if s.po.Items[s.editLineIdx].IsVoided {
			return Status("line is already voided", StatusWarn)
		}
		s.openVoidLine(s.editLineIdx)
		return textinput.Blink
	}
	return nil
}

// lineOffer is the historical offer standing for the line being edited: the
// last price recorded for its item, the total that comes to in the cost field's
// own terms, and — when there is no offer — the note that says why. Only a line
// with NO price of its own has one: a line that carries a price shows that, and
// the history behind it is not this screen's business.
//
// It resolves the total HERE, once, and reports no offer when the total cannot
// be expressed, because three callers used to decide separately and could
// disagree. A line with quantity_ordered 0 was the state where they did: the
// row existed, so the action bar named Ctrl-E and the body announced "Ctrl-E
// offers $ for the 0 ordered" — an empty amount after a dollar sign — while
// takeLastPaid quietly returned nil because a per-unit price over no quantity
// is not a total. A key the bar names has to do something; one boundary
// deciding is what keeps that true no matter which of them is read.
func (s *PurchaseOrderEditScreen) lineOffer() (row *poLastPaid, total, note string) {
	if s.po == nil || s.editLineIdx < 0 || s.editLineIdx >= s.lineCount() {
		return nil, "", ""
	}
	if s.lineCostShown != "" {
		return nil, "", ""
	}
	li := s.po.Items[s.editLineIdx]
	row, note = s.lastPaid.offer(poLineItemID(li))
	if row == nil {
		return nil, "", note
	}
	if total = row.total(li.QuantityOrdered); total == "" {
		return nil, "", "Nothing is ordered on this line, so a per-unit price is no total to offer — type the price."
	}
	return row, total, ""
}

// costRowAction is Ctrl-E on the cost row. The row has two states and the key
// serves whichever one it is in: a line with NO price of its own takes the
// historical offer, and a line that already shows a price commits that price as
// it stands. They cannot both apply — lineOffer only answers for a line
// whose cost field opened empty — so the key never has to choose, and the
// action bar names exactly the one that is live (lineBar).
//
// Both halves return nil when there is nothing behind them, which keeps the
// screen's rule: a key the bar does not name does nothing.
func (s *PurchaseOrderEditScreen) costRowAction() tea.Cmd {
	if row, _, _ := s.lineOffer(); row != nil {
		return s.takeLastPaid()
	}
	return s.confirmShownCost()
}

// confirmShownCost is Ctrl-E on the cost row of a line that already carries a
// price: it arms the save to write that price back exactly as shown.
//
// Nothing else on the form can say this. saveLine sends a cost only when the
// operator changed it, and "changed" is an amount comparison, so a line pinned
// at $0.00 — showing the estimate the prefill recovered — has no figure the
// operator could type that would differ from the one in front of them. Before
// this, whether such a line recovered depended on typing "50" rather than the
// "50.00" it was showing, which is not a rule anybody could be taught. Arming
// is deliberately a separate keystroke from Enter: the operator says "write
// this price" and then says "save the line", the same two-step the offer
// already uses.
func (s *PurchaseOrderEditScreen) confirmShownCost() tea.Cmd {
	if s.lineCostShown == "" {
		return nil
	}
	// A cleared field gets the shown price put back: "commit what this row is
	// showing" has to mean something when the row is showing nothing because
	// the operator emptied it, and restoring is the only reading that does not
	// invent a figure.
	if strings.TrimSpace(s.lineInputs[poLineEditCost].Value()) == "" {
		s.lineInputs[poLineEditCost].SetValue(s.lineCostShown)
		s.lineInputs[poLineEditCost].CursorEnd()
	}
	s.lineCostConfirmed = true
	li := s.po.Items[s.editLineIdx]
	// Arming is unconditional — the operator has said "write what this row
	// shows", and they may well fix the figure afterwards — but what the arm
	// SAYS is not. The field takes any characters at all (no validator, only a
	// CharLimit), and saveLine refuses anything that is not a non-negative
	// number, so an arm on "-5" that answered "enter will write $-5" promised a
	// write enter was going to reject. Say the rejection instead.
	cur := strings.TrimSpace(s.lineInputs[poLineEditCost].Value())
	if poCostRejected(cur) {
		return Status(fmt.Sprintf("enter will reject %q: %s", cur, poCostRejectedReason), StatusError)
	}
	return Status(fmt.Sprintf("enter will write $%s as the total for the %d ordered",
		cur, li.QuantityOrdered), StatusWarn)
}

// takeLastPaid is Ctrl-E on the cost row of a line with no price of its own: it
// puts the offered historical price into the field, where it becomes an
// ordinary typed value the operator can correct and the save will carry.
// Nothing accepts it on the operator's behalf — an offer that applied itself
// would be the very thing this screen is being fixed for.
func (s *PurchaseOrderEditScreen) takeLastPaid() tea.Cmd {
	// Nothing to offer opens nothing, the same way a plain text row does — the
	// bar has already dropped the Ctrl-E entry, and why there is no offer is
	// said in the body rather than in a message the operator has to provoke.
	// lineOffer has already resolved the total, so an offer that reaches here
	// is one that can be expressed.
	row, total, _ := s.lineOffer()
	if row == nil {
		return nil
	}
	s.lineInputs[poLineEditCost].SetValue(total)
	s.lineInputs[poLineEditCost].CursorEnd()
	return Status(fmt.Sprintf("offered $%s — a historical price; enter saves it", total), StatusWarn)
}

func (s *PurchaseOrderEditScreen) saveLine() tea.Cmd {
	li := s.po.Items[s.editLineIdx]
	itemID := fmt.Sprintf("%v", li.ID)

	req := omsapi.LineItemUpdate{}

	// The cost rides the save ONLY when the operator changed it. The field is
	// prefilled so the price is visible, and one enter on this form saves the
	// ship date and the notes with it — so a cost that went out untouched would
	// rewrite unit_cost_actual on every ship-date edit anybody ever makes. It
	// did, and that is what walked partially received lines down to $0.00; see
	// po_line_price.go for the arithmetic.
	//
	// "Changed" is an AMOUNT comparison, not a text one: the field is prefilled
	// with two decimal places, so retyping the 50.00 on screen and typing 50
	// are the same intent and a text guard made them opposite outcomes. Blank
	// still means "leave the price alone" — update_item has no defined
	// semantics for clearing a line_cost — and an unparseable or negative entry
	// is still rejected rather than being waved through as unchanged, which is
	// why the parse happens before the comparison rather than inside it.
	// Ctrl-E on the cost row (confirmShownCost) is how the operator says "write
	// the shown price back anyway"; see po_line_price.go.
	costRaw := strings.TrimSpace(s.lineInputs[poLineEditCost].Value())
	if costRaw != "" {
		cost, err := strconv.ParseFloat(costRaw, 64)
		if err != nil || cost < 0 {
			s.errMsg = poCostRejectedReason
			return Status(s.errMsg, StatusError)
		}
		if s.lineCostConfirmed || !poSameAmount(costRaw, s.lineCostShown) {
			req.LineCost = &cost
		}
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
	s.subReturn = s.phase
	s.phase = poEditPhaseAssoc
	return nil
}

func (s *PurchaseOrderEditScreen) updateAssocPick(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		// Nothing is written until enter, so leaving changes nothing — but it
		// has to land back where the picker was opened from.
		s.returnFromSub()
		return s, nil
	case "tab", "down":
		if s.assocCursor < len(s.assocRows)-1 {
			s.assocCursor++
		}
		return s, nil
	case "shift+tab", "up":
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

// The value is returned PLAIN, with a flag for whether it should read as an
// absence: the columnar renderer owns the styling, and a value that arrived
// pre-styled could not be picked out when the row takes focus (its own reset
// would end the highlight partway through the field).
func (s *PurchaseOrderEditScreen) assocRowValue(row int) (string, bool) {
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
		return label, false
	case loadErr != "":
		return "(none) · options unavailable — " + loadErr, true
	case loading:
		return "(none) · loading options…", true
	}
	return "(none)", true
}

// ---------------------------------------------------------------------------
// Void-line sub-phase
// ---------------------------------------------------------------------------

func (s *PurchaseOrderEditScreen) openVoidLine(idx int) {
	s.subReturn = s.phase
	s.phase = poEditPhaseVoidLine
	s.editLineIdx = idx
	s.errMsg = ""
	s.voidReason.SetValue("")
	s.voidReason.Focus()
}

// returnFromSub closes a picker or the void prompt back onto whichever form
// opened it, re-focusing that form's input.
func (s *PurchaseOrderEditScreen) returnFromSub() {
	s.voidReason.Blur()
	if s.subReturn == poEditPhaseLine && s.po != nil && s.editLineIdx >= 0 && s.editLineIdx < s.lineCount() {
		s.phase = poEditPhaseLine
		s.syncLineFocus()
		return
	}
	s.phase = poEditPhaseForm
	s.syncFocus()
}

func (s *PurchaseOrderEditScreen) updateVoidLine(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.returnFromSub()
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

// ---------------------------------------------------------------------------
// The frame every phase renders into
// ---------------------------------------------------------------------------

// frame is jdeScreen.frame with this screen's in-flight verb and error folded
// into the status line — the pane geometry itself lives in jde_form.go, shared
// with every other columnar screen.
func (s *PurchaseOrderEditScreen) frame(body *jdeLines, cursorRow int, verb string, items []actionBarItem) string {
	return s.jdeScreen.frame(body, cursorRow, s.statusRow(s.saving, verb, s.errMsg), items)
}

// ---------------------------------------------------------------------------
// Header form
// ---------------------------------------------------------------------------

// metaFields describes the header band as columnar rows. The three terms rows
// are choices; everything else is typed into.
func (s *PurchaseOrderEditScreen) metaFields() []jdeField {
	out := make([]jdeField, poEditMetaCount)
	for i := 0; i < poEditMetaCount; i++ {
		f := jdeField{
			Label:   poMetaLabels[i],
			Hint:    poMetaHints[i],
			Width:   poMetaWidths[i],
			Focused: s.cursor == i,
		}
		if sel, ok := s.selects[i]; ok {
			f.Kind, f.Value = jdeChoice, sel.label()
		} else {
			f.Kind, f.Input = jdeText, &s.meta[i]
		}
		out[i] = f
	}
	return out
}

// assocFields describes the two order-level association rows (op-shb9).
func (s *PurchaseOrderEditScreen) assocFields() []jdeField {
	row, onAssoc := s.onAssocRow()
	out := make([]jdeField, poEditAssocCount)
	for i := 0; i < poEditAssocCount; i++ {
		value, dim := s.assocRowValue(i)
		f := jdeField{
			Label:   s.assocRowLabel(i),
			Kind:    jdeValue,
			Value:   value,
			Dim:     dim,
			Focused: onAssoc && row == i,
		}
		if f.Focused {
			// Said on the row rather than only on the bar because this one
			// writes ALONE — it is not staged into the enter-save above it.
			f.Hint = "Ctrl-E picks · saves on its own"
		}
		out[i] = f
	}
	return out
}

// formLines builds the header form body, tagging each line with the navigable
// row it belongs to so the window can keep a whole row on screen — a focused
// choice row owns its option strip, a line owns what it was ordered for.
func (s *PurchaseOrderEditScreen) formLines() *jdeLines {
	l := &jdeLines{}
	if s.po == nil {
		return l
	}
	meta, assoc := s.metaFields(), s.assocFields()
	labelWidth := jdeLabelWidth(meta, assoc)
	strip := strings.Repeat(" ", len(jdeIndent)+labelWidth+len(jdeLeader))

	l.Add(StyleJDEHeading.Render("Order details"))
	for i := 0; i < poEditMetaCount; i++ {
		l.AddRow(i, renderJDEField(meta[i], labelWidth, s.bodyWidth()))
		if sel, ok := s.selects[i]; ok && s.cursor == i {
			// The whole set under the focused row: these are short fixed lists,
			// so showing every label beats cycling blind through six terms to
			// find out what is even on offer.
			l.AddRow(i, strip+StyleMuted.Render(poSelectStrip(sel)))
		}
	}

	l.Add("")
	l.Add(StyleJDEHeading.Render("Ordered for") + "  " +
		StyleMuted.Render("(attribution only — moves no stock, bills nobody)"))
	for i := 0; i < poEditAssocCount; i++ {
		l.AddRow(poEditMetaCount+i, renderJDEField(assoc[i], labelWidth, s.bodyWidth()))
	}

	l.Add("")
	l.Add(StyleJDEHeading.Render(fmt.Sprintf("Line items (%d)", s.lineCount())))
	s.lineGrid(l)
	return l
}

// lineGrid draws the lines as a JD Edwards detail grid: a column header, then
// one dense row per line with the focused row picked out end to end.
func (s *PurchaseOrderEditScreen) lineGrid(l *jdeLines) {
	if s.lineCount() == 0 {
		l.Add(jdeIndent + StyleMuted.Render("(no lines)"))
		return
	}
	itemW := s.lineItemWidth()
	l.Add(StyleMuted.Render(poLineGridRow("#", "Item", "Qty", "Cost", "Ship date", "", itemW)))
	focused, onLine := s.onLineRow()
	for i, li := range s.po.Items {
		cost := ""
		if !li.ActualCost.Empty() {
			cost = "$" + string(li.ActualCost)
		} else if !li.EstimatedCost.Empty() {
			cost = "$" + string(li.EstimatedCost)
		}
		flag := ""
		if li.IsVoided {
			flag = "[voided]"
		}
		row := poLineGridRow(
			strconv.Itoa(i+1),
			truncateOneLine(li.DisplayLabel(), itemW),
			strconv.Itoa(li.QuantityOrdered),
			cost,
			firstNonEmpty(li.ExpectedShipmentDate, "—"),
			flag,
			itemW,
		)
		if onLine && focused == i {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(poEditLineBase+i, row)
		// The line's own "ordered for", indented under it — a mixed order is
		// the whole reason lines carry associations of their own.
		if orderedFor := poLineOrderedFor(li); orderedFor != "" {
			// Indented to the item column, so it reads as a continuation of the
			// row above rather than as a row of its own.
			l.AddRow(poEditLineBase+i, poLineGridItemIndent+StyleMuted.Render("ordered for: "+orderedFor))
		}
	}
}

// Detail-grid column widths. The item column takes whatever the pane has left.
const (
	poGridNumW  = 3
	poGridQtyW  = 4
	poGridCostW = 10
	poGridShipW = 10
	poGridFlagW = 8
)

// poLineGridItemIndent puts a continuation line under the item column.
var poLineGridItemIndent = strings.Repeat(" ", len(jdeIndent)+poGridNumW+2)

// lineItemWidth sizes the item column from the pane: a floor so a narrow
// terminal shortens the description rather than collapsing the column, and a
// ceiling so a wide one doesn't strand the quantities and costs out at the far
// right of an otherwise empty row.
func (s *PurchaseOrderEditScreen) lineItemWidth() int {
	const minItemW, maxItemW = 12, 44
	width := 76
	if s.terminalWidth > 0 {
		width = screenBodyWidth(s.terminalWidth)
	}
	fixed := len(jdeIndent) + poGridNumW + 2 + 2 + poGridQtyW + 2 + poGridCostW + 2 + poGridShipW + 2 + poGridFlagW
	switch w := width - fixed; {
	case w < minItemW:
		return minItemW
	case w > maxItemW:
		return maxItemW
	default:
		return w
	}
}

// poLineGridRow lays one detail row out in its columns. Numbers right-align
// under their headers the way a printed order pad does.
func poLineGridRow(num, item, qty, cost, ship, flag string, itemW int) string {
	cells := []string{
		padCell(num, poGridNumW, alignRight),
		padCell(item, itemW, alignLeft),
		padCell(qty, poGridQtyW, alignRight),
		padCell(cost, poGridCostW, alignRight),
		padCell(ship, poGridShipW, alignLeft),
		flag,
	}
	return jdeIndent + strings.TrimRight(strings.Join(cells, "  "), " ")
}

// formBar names the keys that apply where the cursor is standing — and only
// those: a bar that advertised Ctrl-E on a row with nothing to open would be
// teaching the operator a key that does nothing.
func (s *PurchaseOrderEditScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Exit"}, {"UP/DN", "Fields"}}
	switch {
	case s.isSelectRow(s.cursor):
		items = append(items, actionBarItem{"←→", "Change"})
	case s.cursorOnAssoc():
		items = append(items, actionBarItem{"Ctrl-E", "Pick"})
	case s.cursorOnLine():
		items = append(items, actionBarItem{"Ctrl-E", "Edit line"})
	}
	if s.bodyScrolls(body, 0) {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

func (s *PurchaseOrderEditScreen) cursorOnAssoc() bool {
	_, ok := s.onAssocRow()
	return ok
}

func (s *PurchaseOrderEditScreen) cursorOnLine() bool {
	_, ok := s.onLineRow()
	return ok
}

func (s *PurchaseOrderEditScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, "Saving…", s.formBar(body))
}

// ---------------------------------------------------------------------------
// Line editor
// ---------------------------------------------------------------------------

var poLineEditLabels = map[int]string{
	poLineEditCost:     "Total line cost",
	poLineEditShipDate: "Expected ship date",
	poLineEditNotes:    "Notes",
	poLineRowWorkOrder: "Work order",
	poLineRowCommittee: "Committee",
	poLineRowStatus:    "Line status",
}

var poLineEditHints = map[int]string{
	poLineEditShipDate: "YYYY-MM-DD · '-' or blank clears",
}

// poLineCostHint says which quantity the cost field is a total FOR. The endpoint
// divides it by the ORDERED quantity, and on a partly delivered line that is not
// the quantity the grid's Cost column is counting — so the row names it rather
// than leaving the operator to infer it.
func poLineCostHint(li omsapi.PurchaseOrderItem) string {
	if li.QuantityOrdered <= 0 {
		return "$ total for the line, e.g. 125.00"
	}
	return fmt.Sprintf("$ total for all %d ordered, e.g. 125.00", li.QuantityOrdered)
}

// lineFields describes the line editor: three typed rows, then the three the
// action bar's Ctrl-E opens.
func (s *PurchaseOrderEditScreen) lineFields() []jdeField {
	li := s.po.Items[s.editLineIdx]
	out := make([]jdeField, 0, poLineEditCount)
	for i := 0; i < poLineEditInputCount; i++ {
		width := 0
		if i == poLineEditNotes {
			width = 40
		}
		hint := poLineEditHints[i]
		if i == poLineEditCost {
			hint = poLineCostHint(li)
		}
		focused := s.lineFocus == i
		out = append(out, jdeField{
			Label:   poLineEditLabels[i],
			Kind:    jdeText,
			Input:   &s.lineInputs[i],
			Hint:    hint,
			Width:   width,
			Focused: focused,
		})
	}

	workOrder, dimWO := poAssocCell(li.WorkOrderRef.Label())
	committee, dimCommittee := poAssocCell(poCommitteeRefLabel(li.OwningGroupRef))
	status, dimStatus := "open", true
	if li.IsVoided {
		status, dimStatus = "voided", false
	}
	for _, f := range []jdeField{
		{Label: poLineEditLabels[poLineRowWorkOrder], Value: workOrder, Dim: dimWO, Focused: s.lineFocus == poLineRowWorkOrder},
		{Label: poLineEditLabels[poLineRowCommittee], Value: committee, Dim: dimCommittee, Focused: s.lineFocus == poLineRowCommittee},
		{Label: poLineEditLabels[poLineRowStatus], Value: status, Dim: dimStatus, Focused: s.lineFocus == poLineRowStatus},
	} {
		f.Kind = jdeValue
		out = append(out, f)
	}
	return out
}

// poLineEditSubtitle says what the line is, in the terms its two money figures
// are in. The grid's Cost column shows actual_cost — the money SPENT so far,
// counted over what has arrived — while the field below takes a total for the
// whole ordered quantity, so on a partly delivered line the two differ by
// design. Naming both here is what keeps that from reading as a discrepancy.
func poLineEditSubtitle(li omsapi.PurchaseOrderItem) string {
	out := fmt.Sprintf("ordered %d · received %d", li.QuantityOrdered, li.QuantityReceived)
	if spent := formatMoney(li.ActualCost); spent != "" {
		out += " · " + spent + " spent so far"
	}
	return out
}

// poAssocCell renders an association's label for a columnar row: what is
// attached, or a muted "(none)" so an empty row reads as an absence rather than
// as a blank someone has to guess at.
func poAssocCell(label string) (string, bool) {
	if label == "" {
		return "(none)", true
	}
	return label, false
}

func (s *PurchaseOrderEditScreen) lineBar() []actionBarItem {
	items := []actionBarItem{{"Enter", "Save line"}, {"Esc", "Back"}, {"UP/DN", "Fields"}}
	switch s.lineFocus {
	case poLineEditCost:
		// Whichever of the row's two states is live, and nothing when neither
		// is: a key the bar names has to do something, and one it does not name
		// has to do nothing. costRowAction is the other side of this.
		//
		// One word each, because the bar is clipped and not wrapped. At 80
		// columns — the width this interface is modelled on — the pane leaves
		// this bar 49 columns and the three standing entries spend 38 of them,
		// so a label longer than four characters loses its tail. "Use last
		// price" came out as "Ctrl-E=Use" and "Confirm price" as "Ctrl-E=Conf":
		// the first is worse than the second, because it still reads as a whole
		// instruction while no longer saying WHICH price. A short label that is
		// true at every width beats a long one that is true at 140. What the key
		// does in full is on the body line above it, which has room for it.
		if row, _, _ := s.lineOffer(); row != nil {
			items = append(items, actionBarItem{"Ctrl-E", "Take"})
		} else if s.lineCostShown != "" {
			items = append(items, actionBarItem{"Ctrl-E", "Send"})
		}
	case poLineRowWorkOrder, poLineRowCommittee:
		items = append(items, actionBarItem{"Ctrl-E", "Pick"})
	case poLineRowStatus:
		if !s.po.Items[s.editLineIdx].IsVoided {
			items = append(items, actionBarItem{"Ctrl-E", "Void line"})
		}
	}
	return items
}

func (s *PurchaseOrderEditScreen) viewLineEdit() string {
	li := s.po.Items[s.editLineIdx]

	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Edit line: ") + li.DisplayLabel())
	body.Add(jdeIndent + StyleMuted.Render(poLineEditSubtitle(li)))
	body.Add("")
	// One block, so it finds its own label column — unlike the header form,
	// whose two bands share one.
	for i, line := range renderJDEFields(s.lineFields(), s.bodyWidth()) {
		body.AddRow(i, line)
	}
	body.Add("")
	// Everything about the cost row's Ctrl-E is drawn only while the cursor is
	// ON the cost row, the same test lineBar makes. The operator learns the keys
	// from a bar naming exactly what works where they are standing; a body that
	// went on naming Ctrl-E from the notes row — where the bar has dropped it
	// and the key does nothing — teaches the opposite of that rule.
	if s.lineFocus == poLineEditCost {
		if row, total, note := s.lineOffer(); row != nil {
			body.Add(jdeIndent + StyleStatusWarn.Render(row.describe()))
			body.Add(jdeIndent + StyleMuted.Render(fmt.Sprintf(
				"Ctrl-E offers $%s for the %d ordered; leave the field blank to save no price.",
				total, li.QuantityOrdered)))
		} else if note != "" {
			body.Add(jdeIndent + StyleMuted.Render(note))
		}
	}
	body.Add(jdeIndent + StyleMuted.Render("Enter saves cost, ship date and notes together; the three rows under them write on their own."))
	body.Add(jdeIndent + StyleMuted.Render("A cost you do not change is not re-sent — the price stays as it is."))
	// …which is why the row needs a way to say "send it anyway", and why what
	// that key will do is drawn rather than left to a status line the operator
	// may already have scrolled past. A line reading $0.00 in the grid is
	// recovered from right here.
	if note, pending := s.costPendingNote(li.QuantityOrdered); note != "" && s.lineFocus == poLineEditCost {
		style := StyleMuted
		if pending {
			// The next enter's outcome is already decided — a write armed, or
			// an entry that will be refused — which is not a hint about a key:
			// it is the same class of thing as the historical offer above, and
			// reads in the same colour.
			style = StyleStatusWarn
		}
		body.Add(jdeIndent + style.Render(note))
	}
	return s.frame(body, s.lineFocus, "Saving…", s.lineBar())
}

// costPendingNote is the line under the form that says what enter will do with
// the COST as the field stands right now, and the bool is whether that is
// something already decided about the next enter — a write armed, or an entry
// enter will refuse — rather than a key still on offer. The first two are drawn
// as pending action, the last as a hint.
//
// It is one function and not two branches inline because the drawn state and
// saveLine have to agree in EVERY field state, not just the common one, and
// they did not. saveLine sends nothing at all when the field is blank — blank
// means "leave the price alone", deliberately, since update_item has no defined
// semantics for clearing a line_cost — and it reads lineCostConfirmed only
// after that test. So an armed line whose field the operator then emptied
// writes no price, while a note rendered from the arm alone announced "enter
// writes $ as the total for the 10 ordered": an empty amount, promising a money
// write that never happened, with no error to contradict it.
//
// Every reading is built from the trimmed FIELD and never from lineCostShown,
// for the matching reason: Ctrl-E arms whatever the field holds and only puts
// lineCostShown back when the field is empty, so a note quoting the price the
// row opened with went stale the moment the operator typed over it — naming
// $50.00 "unchanged" while the save was about to write $62.50.
func (s *PurchaseOrderEditScreen) costPendingNote(quantityOrdered int) (string, bool) {
	cur := strings.TrimSpace(s.lineInputs[poLineEditCost].Value())
	switch {
	case s.lineCostShown == "":
		// A line with no price of its own: the offer block above already speaks
		// for this row, and its Ctrl-E takes that offer rather than confirming
		// anything. Two notes about one key would be one too many.
		return "", false
	case cur == "" && s.lineCostConfirmed:
		return fmt.Sprintf(
			"The cost field is empty, so enter writes no price and the line keeps the one it has — Ctrl-E puts the $%s back.",
			s.lineCostShown), true
	case cur == "":
		return fmt.Sprintf(
			"Ctrl-E puts the $%s back and writes it; a blank field leaves the price alone.",
			s.lineCostShown), false
	case poCostRejected(cur):
		// Nothing about a price, armed or offered, until the field holds one
		// saveLine would accept. The textinput takes any characters at all —
		// "-5", "50,00", "$50" — and both branches below read "the field is not
		// empty" as "the field holds a figure", so they announced "enter writes
		// $-5 as the total for the 10 ordered" over an entry enter was about to
		// refuse outright. The refusal is drawn in saveLine's own words.
		//
		// The REASON leads and the entry follows, for the same reason the
		// offer's caveat leads (poLastPaid.describe): the pane clips this line
		// at 80 columns, the entry is still legible in the field two rows above,
		// and the rule it broke is not recoverable from anywhere else on the
		// screen. Capitalised only because it opens a sentence here while
		// saveLine's status line puts it mid-line.
		reason := strings.ToUpper(poCostRejectedReason[:1]) + poCostRejectedReason[1:]
		return fmt.Sprintf("%s, so enter will not save %q.", reason, cur), true
	case s.lineCostConfirmed:
		return fmt.Sprintf("Price confirmed: enter writes $%s as the total for the %d ordered.",
			cur, quantityOrdered), true
	default:
		return fmt.Sprintf("Ctrl-E writes $%s as the total for the %d ordered, even where it matches the price the line already carries.",
			cur, quantityOrdered), false
	}
}

// ---------------------------------------------------------------------------
// Association picker
// ---------------------------------------------------------------------------

// viewAssocPick draws the open association picker, naming what it is tagging
// (the order, or one line) so an operator who opened it from a line can't
// mistake it for the order-level field two bands up.
func (s *PurchaseOrderEditScreen) viewAssocPick() string {
	field := "Work order"
	if s.assocField == poAssocFieldCommittee {
		field = "Committee"
	}
	target := "this purchase order"
	if s.assocLineIdx >= 0 && s.assocLineIdx < s.lineCount() {
		target = fmt.Sprintf("line %d: %s", s.assocLineIdx+1, s.po.Items[s.assocLineIdx].DisplayLabel())
	}

	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render(field+" for ") + target)
	body.Add(jdeIndent + StyleMuted.Render("Attribution only — it moves no stock and bills no committee."))
	body.Add("")

	// The list windows around its OWN cursor before the frame ever sees it, so
	// the line to keep on screen is wherever that render put the marker — not
	// assocCursor, which indexes the options rather than the drawn lines.
	rows := strings.Split(strings.TrimRight(renderWindowedList(len(s.assocRows), s.assocCursor, 0, pickerPaneWidth,
		// The room the pane-local renderer offers is ignored here: these rows go
		// on to jdeLines, which fits them against the columnar layer's own
		// width rather than the picker pane's.
		func(i int, _ int) string { return s.assocRows[i].label }), "\n"), "\n")
	cursorLine := 0
	for i, line := range rows {
		if strings.Contains(line, "▸ ") {
			cursorLine = i
		}
		body.AddRow(i, line)
	}
	body.Add("")
	body.Add(jdeIndent + StyleMuted.Render("Row 1 is none — it detaches what is attached today."))
	return s.frame(body, cursorLine, "Saving…", []actionBarItem{
		{"Enter", "Select"}, {"Esc", "Cancel"}, {"UP/DN", "Move"},
	})
}

// ---------------------------------------------------------------------------
// Void prompt
// ---------------------------------------------------------------------------

func (s *PurchaseOrderEditScreen) viewVoidLine() string {
	li := s.po.Items[s.editLineIdx]
	field := jdeField{
		Label:   "Reason",
		Kind:    jdeText,
		Input:   &s.voidReason,
		Width:   40,
		Hint:    "required",
		Focused: true,
	}

	body := &jdeLines{}
	body.Add(StyleStatusWarn.Render("Void line item"))
	body.Add("")
	body.Add(jdeIndent + StyleMuted.Render("Line: ") + li.DisplayLabel())
	body.Add(jdeIndent + StyleMuted.Render("This marks the line voided and the supplier link discontinued."))
	body.Add("")
	body.AddRow(0, renderJDEFields([]jdeField{field}, s.bodyWidth())[0])
	return s.frame(body, 0, "Voiding…", []actionBarItem{
		{"Enter", "Void line"}, {"Esc", "Cancel"},
	})
}

// stringPtr returns a pointer to s. Used to send a metadata/line field even
// when empty (an empty expected-delivery/ship-date is the clear signal).
func stringPtr(s string) *string { return &s }
