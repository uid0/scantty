// PurchaseOrderEditScreen — edit an existing purchase order.
//
// This is the TUI counterpart to the web PurchaseOrderPage's edit affordances
// (frontend/src/pages/PurchaseOrderPage.tsx): the "Edit details" metadata modal
// plus the per-line edit-cost / edit-ship-date / remove-line controls — where
// removing is DELETE on an order the supplier has not seen and VOID once it
// has, the server saying which (see poRemovalFor). It mirrors the FULL set so
// an operator at the workstation can amend a PO without the browser
// ([[ship-complete-features]]).
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
//	                         line editor, the removal the server allows, the
//	                         last price on file)
//	←/→ or space             change a "< value >" choice row
//	Esc                      back / cancel  (Ctrl-C always quits, app-wide)
//
// One key sits outside that scheme, on one phase: Ctrl-X confirms the DELETE on
// poEditPhaseDeleteLine, because Ctrl-E opened that frame and enter is the key
// a hand reaches for next (see updateDeleteLine). Nothing else is bound: the
// w / c / v accelerators that used to hide on the line rows are now rows of the
// line editor, reached with Ctrl-E, and the bar at the bottom names exactly the
// keys that apply where the cursor is standing.
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
//	                       the last three rows opens the picker or the removal
//	                       that owns it, and on the cost row takes the
//	                       last price on file for a line that carries none. What a
//	                       cost field may and may not send is po_line_price.go.
//	poEditPhaseVoidLine  — reason input; enter voids the line via
//	                       VoidPurchaseOrderLineItem.
//	poEditPhaseDeleteLine— the irreversible counterpart, on an order the
//	                       supplier has not seen: a confirmation naming the line
//	                       and NO reason input, Ctrl-X destroys it via
//	                       DeletePurchaseOrderLineItem. Which of the two the
//	                       status row's Ctrl-E opens is the SERVER's answer
//	                       (can_delete_items), read in one place — poRemovalFor.
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
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type poEditPhase int

const (
	poEditPhaseForm poEditPhase = iota
	poEditPhaseLine
	poEditPhaseVoidLine
	poEditPhaseDeleteLine
	poEditPhaseAssoc
	// poEditPhaseCount is the sentinel the phase sweep walks to. A phase added
	// above it is swept without anybody remembering to add it to a roster —
	// which is the only kind of roster this package trusts (AGENTS.md).
	poEditPhaseCount
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

	// The delete confirm's answer to the last keypress (poEditPhaseDeleteLine).
	// That frame binds two keys and has no input, so without this every other
	// key redraws a pane that is a pure function of unchanged state — the
	// byte-identical redraw this package reports as a hang. Cleared whenever
	// the frame is opened or the write goes out.
	deleteNote string

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
	// done is the whole SUCCESS sentence where the verb alone does not say
	// enough. A delete names the line it destroyed, because by the time this is
	// read the row that named it is gone and this flash is the only account of
	// it left on screen. Empty falls back to action.
	done string
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

// poLineID is a PO line's own identity as a string. The wire types the pk `any`
// — OMS serves an int and the fakes a slug — so every writer on this screen
// already spells it this way before it goes into a URL, and one helper keeps
// them spelling it the same. A line with NO id has no identity: blank is
// returned for it rather than a "<nil>" two such lines would compare equal on,
// which is the confusion an identity exists to prevent.
func poLineID(li omsapi.PurchaseOrderItem) string {
	if li.ID == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", li.ID))
}

// lineIndexOf finds the line carrying id, or -1. A blank id never matches: see
// poLineID.
func (s *PurchaseOrderEditScreen) lineIndexOf(id string) int {
	if id == "" || s.po == nil {
		return -1
	}
	for i, li := range s.po.Items {
		if poLineID(li) == id {
			return i
		}
	}
	return -1
}

// addressedLine is the line editLineIdx names, or false when it names none.
//
// EVERY read of po.Items[editLineIdx] goes through it. The index is POSITIONAL
// and a reload can invalidate it — a line voided from the web, an order
// amended, or this screen's own delete, which takes the row away where voiding
// only struck it through — so an unguarded index is a panic on a state this
// change newly makes reachable. reseatLineIndexes is what keeps that state from
// arising at all; this is what keeps it from crashing the terminal if it ever
// does.
func (s *PurchaseOrderEditScreen) addressedLine() (omsapi.PurchaseOrderItem, bool) {
	if s.po == nil || s.editLineIdx < 0 || s.editLineIdx >= s.lineCount() {
		return omsapi.PurchaseOrderItem{}, false
	}
	return s.po.Items[s.editLineIdx], true
}

// closeLineSubPhase drops whichever per-line sub-phase is open back onto the
// form, unfocused and with nothing half-typed left behind. It is the answer
// when the line a sub-phase stands on has gone off the order: the form is the
// one frame that can show what IS on the order now. returnFromSub is the
// ordinary way back (it returns to the line editor); this is the way back when
// there is no line to return to.
func (s *PurchaseOrderEditScreen) closeLineSubPhase() {
	s.voidReason.Blur()
	s.voidReason.SetValue("")
	s.deleteNote = ""
	s.phase = poEditPhaseForm
}

// reseatLineIndexes puts the per-line indexes — and any sub-phase standing on
// one — back on a footing the freshly loaded order actually supports. It is
// handed the ids those indexes named BEFORE the new order was stored.
//
// The clamp this replaces re-pointed a stale index at whatever now occupied
// that position, which is the dangerous answer twice over. With two lines cut
// to one, `editLineIdx = 0` left an OPEN delete confirm naming the line the
// operator had read and confirmed while Ctrl-X would have destroyed the other
// one — a destroy nobody agreed to, and the quieter half of the defect. And an
// EMPTIED Items list is newly reachable, because deleting takes the row away
// where voiding left it struck through, so the same clamp handed every reader
// index 0 into nothing.
//
// So IDENTITY is carried across and never position: an index follows its own
// line wherever the reload put it, and where that line is gone the index is
// dropped rather than aimed somewhere else. A sub-phase standing on a dropped
// index closes, because re-targeting an irreversible confirm is precisely what
// must not happen; the operator is told, on the status row, rather than finding
// the frame swapped under them.
// It hands its sentence BACK rather than only writing it to errMsg, because the
// status row is one surface and this one needs two: the row cannot fold, so
// what runs past 49 cells at 80 columns is gone, and the toast behind it is
// where the tail survives. The caller is what has a command to return.
func (s *PurchaseOrderEditScreen) reseatLineIndexes(editID, assocID string) string {
	s.editLineIdx = s.lineIndexOf(editID)
	if s.assocLineIdx != poAssocLineOrder {
		if idx := s.lineIndexOf(assocID); idx >= 0 {
			s.assocLineIdx = idx
		} else if s.phase == poEditPhaseAssoc {
			s.closeLineSubPhase()
			s.errMsg = poEditLineGoneNote
			return s.errMsg
		}
	}
	if s.editLineIdx < 0 {
		switch s.phase {
		case poEditPhaseLine, poEditPhaseVoidLine, poEditPhaseDeleteLine:
			s.closeLineSubPhase()
			s.errMsg = poEditLineGoneNote
			return s.errMsg
		}
		return ""
	}
	// The line survived; the ORDER is the other half and it is READ AGAIN here
	// rather than carried over from the moment the sub-phase opened. See
	// removalPhaseHolds — this is the refresh the flag must never be cached
	// across.
	if note := s.removalFlipNote(); note != "" {
		s.returnFromSub()
		s.errMsg = note
		return note
	}
	return ""
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
		// The ids the per-line indexes named a moment ago, read off the OLD
		// order while it is still the stored one — reseatLineIndexes carries
		// them across by identity rather than letting a position stand.
		editID, assocID := "", ""
		if li, ok := s.addressedLine(); ok {
			editID = poLineID(li)
		}
		if s.po != nil && s.assocLineIdx >= 0 && s.assocLineIdx < s.lineCount() {
			assocID = poLineID(s.po.Items[s.assocLineIdx])
		}
		s.po = m.po
		if s.cursor >= s.rowCount() && s.rowCount() > 0 {
			s.cursor = s.rowCount() - 1
		}
		note := s.reseatLineIndexes(editID, assocID)
		s.syncFocus()
		if note != "" {
			return s, Status(note, StatusWarn)
		}
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
		done := m.done
		if done == "" {
			done = m.action
		}
		return s, tea.Batch(Status(done, StatusOK), s.load())

	case tea.KeyMsg:
		switch s.phase {
		case poEditPhaseLine:
			return s.updateLineEdit(m)
		case poEditPhaseVoidLine:
			return s.updateVoidLine(m)
		case poEditPhaseDeleteLine:
			return s.updateDeleteLine(m)
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
	case poEditPhaseDeleteLine:
		// Nothing on the confirm is typed into — deleting asks for no reason —
		// so there is no caret for a blink to move, and the metadata inputs
		// below must not be fed one while this frame is up.
		return s, nil
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
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, s.rowCount(), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// pageCursor moves by one screenful of rows. It CLAMPS where moveCursor wraps:
// paging is a way of covering ground in a body too tall for the pane, and a
// page that jumped from the last line back to the first would lose the
// operator's place rather than save them keystrokes.
func (s *PurchaseOrderEditScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, s.rowCount(), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
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
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		next, ok := s.moveRow(s.lineFocus, poLineEditCount, delta, 0, s.lineBar())
		if !ok {
			return s, nil
		}
		s.lineFocus = next
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
	if _, ok := s.addressedLine(); !ok {
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
		// The bar's own predicate, so a key it did not name does nothing. It
		// used to answer a voided line with a Status warning while the bar named
		// no key at all — an unnamed key that acts, which is the honesty rule
		// broken in the direction that is hardest to notice.
		if _, ok := s.removalOffered(); !ok {
			return nil
		}
		if s.lineRemoval() == poRemovalDelete {
			s.openDeleteLine(s.editLineIdx)
			return nil
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
	li, addressed := s.addressedLine()
	if !addressed {
		return nil, "", ""
	}
	if s.lineCostShown != "" {
		return nil, "", ""
	}
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
	li, ok := s.addressedLine()
	if !ok {
		return nil
	}
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
	li, ok := s.addressedLine()
	if !ok {
		// The line went off the order under the form (reseatLineIndexes closes
		// this phase when that happens, so this is belt and braces): a PATCH
		// aimed at a position would write this form's cost and dates onto
		// whatever now sits there.
		s.closeLineSubPhase()
		s.errMsg = poEditLineSaveGoneNote
		s.syncFocus()
		return Status(s.errMsg, StatusWarn)
	}
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
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		// A LIST cursor: it clamps rather than wrapping (running off the bottom
		// must not reappear on row 1, which DETACHES the association), and it
		// declines outright on a pane the picker is not drawn into.
		next, ok := s.pickRow(s.assocCursor, len(s.assocRows), delta, 0, poEditAssocBar)
		if !ok {
			return s, nil
		}
		s.assocCursor = next
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
	// A LINE's picker addresses po.Items positionally, so it is the same
	// reload hazard the removal confirms carry (reseatLineIndexes): the write
	// is refused rather than aimed at whatever now sits at that position.
	if lineIdx != poAssocLineOrder && (s.po == nil || lineIdx < 0 || lineIdx >= s.lineCount()) {
		s.closeLineSubPhase()
		s.errMsg = poEditLineGoneNote
		s.syncFocus()
		return Status(s.errMsg, StatusWarn)
	}

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
// Which removal this order offers — the server's answer, read and not derived
// ---------------------------------------------------------------------------

// poLineRemoval is which of the two ways a line comes OFF an order applies
// here. Exactly one is ever offered, and the operator is never asked to know
// which: the answer is the server's.
//
// The boundary is not a status name — it is whether the supplier has seen the
// document. OMS keeps that as PurchaseOrder.PRE_SUPPLIER_STATUSES and serves
// the answer as `can_delete_items`, whose serializer docstring says in as many
// words that a client must never keep its own copy of which statuses those
// are: guess wrong and you offer an irreversible destroy on an order the
// supplier already holds, or hide it on one where voiding leaves a meaningless
// ghost. So nothing here reads po.Status, and adding a second pre-send state to
// OMS costs this screen nothing.
type poLineRemoval int

const (
	// poRemovalUnknown is the server not having ANSWERED — an OMS too old to
	// serve the key, or a payload that lost it. It is a state of its own and
	// not a no: reading absent as false would say "the supplier holds this
	// order" about a draft the operator is still editing.
	poRemovalUnknown poLineRemoval = iota
	poRemovalDelete
	poRemovalVoid
)

// poRemovalFor reads the flag. The ONE place it is read on this screen; the
// bar, the key arm and the body note all come through here so they cannot come
// to disagree about which action is on offer.
func poRemovalFor(po *omsapi.PurchaseOrder) poLineRemoval {
	if po == nil || po.CanDeleteItems == nil {
		return poRemovalUnknown
	}
	if *po.CanDeleteItems {
		return poRemovalDelete
	}
	return poRemovalVoid
}

// lineRemoval is poRemovalFor for the order this screen is editing.
func (s *PurchaseOrderEditScreen) lineRemoval() poLineRemoval {
	return poRemovalFor(s.po)
}

// removalOffered says whether the status row's Ctrl-E has anything to open for
// the line being edited, and what the bar should call it. It is the single
// predicate behind BOTH — the bar naming a key the arm declines is the defect
// this screen's whole key scheme exists to prevent.
//
// A line that is already voided has nothing left to void; it can still be
// DESTROYED while the order is pre-send, because a typo and the ghost of a typo
// are both things a private document is better without, and OMS's _destroy_item
// carries no is_voided guard. Where neither applies the key is not named and
// does nothing — the bar has already said so, and a note answering a key the
// bar declined to offer would be the second surface this package keeps deleting.
func (s *PurchaseOrderEditScreen) removalOffered() (label string, ok bool) {
	li, addressed := s.addressedLine()
	if !addressed {
		return "", false
	}
	// A write is already out against this line. Opening a removal on top of it
	// gives the operator a confirm whose status row reads "Deleting…" for a
	// delete nobody asked for, whose own Ctrl-X is dropped for as long as the
	// other write is in flight, and which then vanishes on its own when that
	// write answers — a frame that reports the wrong work and cannot be acted
	// on. The gate lives HERE because this is the one predicate the bar and the
	// arm both read, so the legend loses Ctrl-E in the same breath the key
	// stops acting.
	if s.saving {
		return "", false
	}
	if s.lineRemoval() == poRemovalDelete {
		return "Delete line", true
	}
	// Void is what the other two answers land on. On poRemovalUnknown that is
	// deliberate and is NOT a guess at the flag: void is the answer that cannot
	// destroy anything, it is what this screen has always offered, and the row
	// says the server did not answer rather than presenting it as the rule
	// (removalNote). The irreversible action is never offered on a silence.
	if li.IsVoided {
		return "", false
	}
	return "Void line", true
}

// removalNote is the standing sentence under the status row: what Ctrl-E will
// open, and — when the server did not answer — that it did not. It is drawn
// only while the cursor is ON that row, the same test the bar makes, so the
// body never goes on describing a key the bar has dropped.
func (s *PurchaseOrderEditScreen) removalNote() string {
	li, ok := s.addressedLine()
	if !ok {
		return ""
	}
	switch s.lineRemoval() {
	case poRemovalDelete:
		note := "This order has not gone to the supplier, so a line put on it by mistake can be DELETED outright — no reason is asked for, and nothing is left behind."
		// The KEY is named here only while the bar names it, off the one
		// predicate both read. Gating the whole sentence would be the wrong
		// fix — the other branches carry a standing FACT about the row (a line
		// already voided, a server that did not answer) which is exactly what
		// this note exists to hold, and which is true whether or not a key is
		// on offer. Only the clause naming Ctrl-E is a claim about a key, so
		// only that clause follows the legend.
		if _, offered := s.removalOffered(); offered {
			note += " Ctrl-E asks you to confirm first; it cannot be undone."
		}
		return note
	case poRemovalVoid:
		if li.IsVoided {
			return "This line is already voided. The supplier holds this order, so its lines stay on the record — a voided line is struck off rather than removed."
		}
		return "The supplier already has this order, so a line can only be VOIDED: it is struck off and stays on the record, with the reason you give. Deleting it outright would be a lie about what was ordered."
	default:
		// poRemovalUnknown. Named rather than papered over: this is OMS not
		// having answered, which is a different fact from "you may not delete",
		// and an operator who cannot see the difference cannot report it.
		if li.IsVoided {
			return "This line is already voided. This server did not report whether this order's lines may be deleted outright, so nothing destructive is offered on it."
		}
		return "This server did not report whether this order's lines may be deleted outright (can_delete_items), so only voiding is offered — the safe half. Voiding on a draft leaves a struck-off line where deleting would have left nothing."
	}
}

// removalPhaseHolds reports that the removal sub-phase the screen is standing
// on is still the one the ORDER's flag calls for. It is the single predicate
// the confirm's bar, its destructive arm and its frame all read, so none of the
// three can go on offering an action the last refresh has superseded.
//
// It exists because a reload landing under an OPEN removal sub-phase is a
// supported, surviving state (reseatLineIndexes): the sub-phase follows its own
// LINE across the refresh, and the line surviving says nothing whatever about
// the ORDER. Open the delete confirm on a draft, have the order sent to the
// supplier from the web, and the screen's own reload lands with
// can_delete_items false — the line is still there, so the confirm stayed up,
// the bar went on reading `Ctrl-X=Delete line` and the body went on saying
// there is no undo, over an order the server now says the supplier holds. That
// is the flag read once and CACHED ACROSS A REFRESH, which is the one thing its
// serializer docstring forbids.
//
// The mirror is held too rather than only the direction that was reported: a
// void prompt open when the order becomes the shop's own again is offering the
// instrument that leaves a ghost where the server now allows the typo to be
// erased.
func (s *PurchaseOrderEditScreen) removalPhaseHolds() bool {
	switch s.phase {
	case poEditPhaseDeleteLine:
		return s.lineRemoval() == poRemovalDelete
	case poEditPhaseVoidLine:
		// Void is what BOTH of the other answers land on, silence included —
		// the same reading removalOffered makes, so the prompt does not close
		// on an OMS that merely stopped answering.
		return s.lineRemoval() != poRemovalDelete
	}
	return true
}

// removalFlipNote is what the operator is told when it does not hold, and ""
// when it does. Nothing is destroyed and nothing is silently swapped to the
// other instrument: the frame closes back to the line editor, whose status row
// now offers whichever removal the refreshed flag calls for, and the sentence
// names what CHANGED. Bounded to the status row's 49 cells at 80 columns with
// the load-bearing clause first, because that row cannot fold.
func (s *PurchaseOrderEditScreen) removalFlipNote() string {
	if s.removalPhaseHolds() {
		return ""
	}
	if s.phase == poEditPhaseDeleteLine {
		return poEditDeleteFlippedNote
	}
	return poEditVoidFlippedNote
}

// The screen's refusals, worded for the surface that cannot fold and cannot
// scroll. fitStatus gives an error message bodyWidth-2 cells — 49 at the 80
// columns this interface is modelled on — so the clause each of these exists to
// carry leads and the circumstance follows: cut at 49 the operator still reads
// that NOTHING WAS WRITTEN, which is the fact, and loses only the part of the
// reason the frame around them already shows.
const (
	poEditLineGoneNote      = "nothing written: that line has left this order"
	poEditLineSaveGoneNote  = "nothing saved: that line has left this order"
	poEditLineVoidGoneNote  = "nothing voided: that line has left this order"
	poEditLineDelGoneNote   = "nothing deleted: that line has left this order"
	poEditDeleteFlippedNote = "nothing deleted: the supplier holds this order"
	poEditVoidFlippedNote   = "nothing voided: this order is the shop's own"
)

// poLastActiveLine reports that idx names the only line on the order that is
// not voided — so taking it off, either way, leaves the order with no ACTIVE
// line. It answers about the state the removal LEAVES and says nothing about
// which instrument produced it, which is why one predicate serves both.
//
// It matters because OMS's purchase-order LIST hides an order when ALL THREE of
// these hold, and only then (PurchaseOrderViewSet.get_queryset, oms-a8o): the
// order HAS line items, none of them survives unvoided, and it is OUTSIDE
// PurchaseOrder.PRE_SUPPLIER_STATUSES. ScanTTY's list is a straight
// pass-through of that endpoint (list.go's purchaseOrderRows), so it cannot
// lift the filter and answers by SAYING SO on the frame the key is pressed
// from — but only where the third conjunct can actually be true.
//
// THAT IS THE VOID PROMPT AND NOT THE DELETE CONFIRM, and the reason is the
// frozenset rather than a judgement: `can_delete_items` is served from
// PRE_SUPPLIER_STATUSES itself (PurchaseOrderSerializer.get_can_delete_items),
// so an order this screen will DELETE from is inside the set by construction
// and the third conjunct is false whatever the delete leaves behind — no line
// at all, or voided ghosts. A delete this client can perform cannot hide an
// order, and a confirm saying it will would be describing a loss that cannot
// happen. VOID is offered on the other two answers, and on the one that is not
// a silence the supplier demonstrably holds the order, which is outside the
// set: there the arithmetic below IS the hide. See voidCaveats for what the
// prompt says about it, and why the way back it names is the only one there is.
func poLastActiveLine(po *omsapi.PurchaseOrder, idx int) bool {
	if po == nil || idx < 0 || idx >= len(po.Items) {
		return false
	}
	if po.Items[idx].IsVoided {
		return false
	}
	for i, li := range po.Items {
		if i != idx && !li.IsVoided {
			return false
		}
	}
	return true
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

// returnFromSub closes a picker, the void prompt or the delete confirm back
// onto whichever form opened it, re-focusing that form's input.
func (s *PurchaseOrderEditScreen) returnFromSub() {
	s.voidReason.Blur()
	s.deleteNote = ""
	if _, addressed := s.addressedLine(); s.subReturn == poEditPhaseLine && addressed {
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
		if note := s.removalFlipNote(); note != "" {
			s.returnFromSub()
			s.errMsg = note
			return s, Status(note, StatusWarn)
		}
		reason := strings.TrimSpace(s.voidReason.Value())
		if reason == "" {
			s.errMsg = "a reason is required to void a line"
			return s, Status(s.errMsg, StatusError)
		}
		li, ok := s.addressedLine()
		if !ok {
			// Same hazard as the delete confirm's: a void aimed at a position
			// would strike off whatever now sits there.
			s.closeLineSubPhase()
			s.errMsg = poEditLineVoidGoneNote
			s.syncFocus()
			return s, Status(s.errMsg, StatusWarn)
		}
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
// Delete-line sub-phase
// ---------------------------------------------------------------------------

// openDeleteLine opens the confirmation. Deleting takes NO reason — demanding
// one is precisely the friction that made voiding the wrong instrument for a
// typo — so this phase has no input and nothing to type into. What it owes the
// operator instead is a frame that NAMES what is about to be destroyed, because
// after the write the row it was on is gone.
func (s *PurchaseOrderEditScreen) openDeleteLine(idx int) {
	s.subReturn = s.phase
	s.phase = poEditPhaseDeleteLine
	s.editLineIdx = idx
	s.errMsg = ""
	s.deleteNote = ""
}

// updateDeleteLine is the confirm's key handler.
//
// Ctrl-X and not Enter, deliberately: Ctrl-E OPENS this frame and enter is the
// key an operator's hand reaches for next, so binding the irreversible write to
// it would make a reflex enough to destroy a line. It is the same choice the
// New PO supplier-switch confirm makes for the same reason.
//
// Every other key ANSWERS. A frame that binds two keys and returns nil for the
// rest redraws a pane that is a pure function of unchanged state — byte for
// byte identical, which reads as a wedged program and is the reported hang this
// package keeps finding. The note says what the KEY DID and names no key: the
// bar makes that claim, on every frame, where no budget can trim it.
func (s *PurchaseOrderEditScreen) updateDeleteLine(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.returnFromSub()
		return s, nil
	case "ctrl+x":
		if s.saving {
			// A delete is already out. The status row is drawing "Deleting…"
			// and the bar has dropped the key, so the frame has answered
			// already and a second press has nothing to add.
			return s, nil
		}
		if note := s.removalFlipNote(); note != "" {
			s.returnFromSub()
			s.errMsg = note
			return s, Status(note, StatusWarn)
		}
		li, ok := s.addressedLine()
		if !ok {
			// The line went off the order while this frame was up. Nothing is
			// sent at a position: destroying whatever now sits where the
			// confirmed line was is the one outcome an irreversible key must
			// never have.
			s.closeLineSubPhase()
			s.errMsg = poEditLineDelGoneNote
			s.syncFocus()
			return s, Status(s.errMsg, StatusWarn)
		}
		itemID := fmt.Sprintf("%v", li.ID)
		label := li.DisplayLabel()
		s.saving = true
		s.errMsg = ""
		s.deleteNote = ""
		deps := s.deps
		ctx := s.ctx()
		id := s.poID
		return s, func() tea.Msg {
			out, err := deps.OMS.DeletePurchaseOrderLineItem(ctx, id, itemID)
			done := "line deleted: " + label
			if err == nil && out != nil && strings.TrimSpace(out.Deleted.Label) != "" {
				// The server's own account of what it destroyed, which is what
				// the audit trail now carries. Preferred over the label this
				// screen was showing so the flash and the trail say one thing.
				done = "line deleted: " + strings.TrimSpace(out.Deleted.Label)
			}
			return poLineActionMsg{err: err, action: "line delete", done: done}
		}
	}
	s.deleteNote = m.String() + " does nothing here — this frame only confirms or cancels."
	return s, nil
}

// deleteBar names Ctrl-X only while it will act. While the write is out the
// frame's status row is the answer and the key is not offered — see
// updateDeleteLine.
func (s *PurchaseOrderEditScreen) deleteBar() []actionBarItem {
	if s.saving || !s.removalPhaseHolds() {
		return []actionBarItem{{"Esc", "Back"}}
	}
	return []actionBarItem{{"Ctrl-X", "Delete line"}, {"Esc", "Cancel"}}
}

// deleteHeadline is the ONE thing the confirm may not be drawn without: what is
// about to be destroyed, on a single bounded row.
//
// It is a PINNED, ESSENTIAL header row rather than a body line, and that is the
// whole reason this frame has a header at all. jdeLines gives a body ground
// first — down to nothing — so at 80x10 through 80x16 the body was two markers
// and a title, and the frame read `Delete line item`, `↓ 13 more below`, and a
// bar saying `Ctrl-X=Delete line`: an irreversible destroy confirmed on a frame
// that named nothing it would destroy, with no key on it able to fetch the rest.
// jdeFitHeader gives ground BY RANK and keeps the essential row last, so
// wherever the frame is drawn at all, this row is on the pane.
//
// A row that cannot fold is a row where the FACT survives whole and the
// IDENTIFIER abbreviates: the lead, the ordered quantity and the money are
// fixed-width and never give, and the line's own name — OMS-supplied and
// unbounded — is clipped to what they leave, with the ellipsis that says so.
// The MONEY is deliberately not on it. Everything added to this row is taken
// from the name, and the name is the answer to "what am I destroying" that
// nothing else on the frame carries: ` · 5 ordered · $50.00` is 21 cells of a
// 51-column pane and left the name eleven, drawn as `M3 hex bol…`. The ordered
// quantity stays because it is what tells two otherwise similar lines apart;
// the line total rides a CONTEXT row, where it survives every height that has a
// second header row to give.
//
// "Never gives" is true of every pane this interface is modelled on and NOT of
// the extreme: past the point where keeping the quantity would push the name
// below its floor, the quantity is what gives and says so — removalHeadline
// carries that decision and the reason for it.
func (s *PurchaseOrderEditScreen) deleteHeadline(li omsapi.PurchaseOrderItem, width int) string {
	return removalHeadline("Delete: ", StyleStatusError, li, width)
}

// removalHeadline is that row, said ONCE for both removals. The void prompt
// pins the same shape for the same reason — a frame whose whole job is to take
// a line off the order may not be drawn without naming which line — and voiding
// has no undo either (OMS writes is_voided true and has no endpoint that clears
// it), so getting the identity wrong is exactly as unrecoverable there.
//
// THE ASSEMBLED ROW IS BOUNDED, NOT THE NAME INSIDE IT. It used to clip the
// name to `width - indent - lead - facts` FLOORED AT 1 and then append the
// facts anyway, which is this project's own width rule broken verbatim: a floor
// applied to one PART of a row that is afterwards added to is not a bound. With
// " · 250 ordered" (14 cells) against the 20-cell pane screenBodyWidth floors
// at, the delete row assembled to 2+8+1+14 = 25 cells and clampToBox cut it
// from the right with no ellipsis — the pane drew ` Delete: M · 250 orde`, the
// line's identity reduced to one character and the fact cut mid-word, on the
// frame whose whole job is to name what it is about to destroy. Every terminal
// width from 45 to 53 was in that state, and the guard that should have caught
// it walked three hand-picked widths.
//
// THE GIVE-ORDER IS A DECISION AND NOT THE ARITHMETIC'S LEFTOVER. Where the
// pane cannot hold both, the FACTS give and the NAME keeps the room. That
// reverses what this row's doc above used to say flatly — that the ordered
// quantity never gives, because it is what tells two otherwise similar lines
// apart — and the qualification is the point: the quantity DISAMBIGUATES a
// name, so it presupposes one. Beside a name cut to a character it separates
// nothing, while the name alone still answers "what am I destroying". So the
// quantity holds at every width where the name can keep poHeaderValueFloor
// cells beside it, and gives only past that, which is the extreme this bound
// exists for and not the pane the interface is modelled on. What it leaves is
// poRowDropMark, because a row that gave something up may not read as a whole
// one — the same convention poFitRow keeps on the picker rows.
func removalHeadline(lead string, style lipgloss.Style, li omsapi.PurchaseOrderItem, width int) string {
	name := li.DisplayLabel()
	facts := fmt.Sprintf(" · %d ordered", li.QuantityOrdered)
	if width <= 0 {
		// UNSIZED, which is the layer's standing "draw whole and let clampToBox
		// decide" — there is no pane to measure against yet.
		return jdeIndent + style.Render(lead) + name + StyleMuted.Render(facts)
	}
	room := width - len(jdeIndent) - lipgloss.Width(lead)
	if room < 0 {
		room = 0
	}
	tail := facts
	if room-lipgloss.Width(facts) < poHeaderValueFloor {
		tail = poRowDropMark
	}
	space := room - lipgloss.Width(tail)
	if space <= 0 {
		// Narrower than the mark itself. Unreachable while screenBodyWidth
		// floors at 20 and the longest lead is eight cells, and here so that
		// the row is bounded by CONSTRUCTION rather than by that coincidence.
		return jdeIndent + style.Render(lead) + pickerClip(name, room)
	}
	return jdeIndent + style.Render(lead) + pickerClip(name, space) + StyleMuted.Render(tail)
}

// deleteCaveats is the standing sentence of the confirm, folded by the layer
// rather than hand-counted against 51 columns.
//
// It is CONTEXT and the headline above is essential, which is the sacrifice
// order stated: on a pane too short for both, the operator keeps the identity of
// what they are about to destroy and loses the prose about it — the bar still
// reads `Ctrl-X=Delete line`, so the ACT is named even where its consequences
// are not, and the identity is the half nothing else on the frame carries.
//
// IT USED TO BE TWO, AND THE SECOND ONE WAS RETIRED RATHER THAN SHORTENED. A
// conditional caveat led this list, warning that deleting the last active line
// would drop the order out of every purchase-order list and naming ctrl+k as
// the way back "until another line is added". Both halves of that were true of
// the filter as it stood; oms-a8o narrowed it to orders OUTSIDE
// PRE_SUPPLIER_STATUSES, which is the very set `can_delete_items` is served
// from, so a delete this screen can reach cannot hide anything — see
// poLastActiveLine. A warning describing a loss that cannot happen is as wrong
// as silence about one that can, so it is gone from here and the void prompt
// carries it instead (voidCaveats), where the loss is real, permanent, and
// has a different way back. Do not reinstate it: reaching this frame at all
// means the server has put the order inside the pre-supplier set.
//
// The remaining sentence returns as a slice because the frame folds and spaces
// each caveat as a unit, and because a conditional one belongs beside it the
// moment a delete acquires a consequence the frame does not otherwise name.
func (s *PurchaseOrderEditScreen) deleteCaveats() []string {
	return []string{
		"Deleting takes the line off the order for good. It is not a void: nothing is struck off, no reason is recorded, and there is no undo.",
	}
}

// deleteHeader is the confirm's pinned block: the essential headline above,
// then what the line costs the order and — on a line already struck off — what
// deleting it additionally removes. Said ONCE, because the frame draws it and
// the pane sweeps measure it, and a second literal beside this one would be a
// header measured that is not the header drawn.
func (s *PurchaseOrderEditScreen) deleteHeader(li omsapi.PurchaseOrderItem, width int) jdeHeader {
	h := jdeHeader(nil).add(jdeHeadEssential, s.deleteHeadline(li, width))
	if total := formatMoney(li.EstimatedCost); total != "" {
		h = h.add(jdeHeadContext, jdeIndent+StyleMuted.Render("Line total on the order: "+total))
	}
	if li.IsVoided {
		// Worth knowing before the press and not worth the essential row: a
		// voided line is already struck off, so this destroys the ghost too.
		h = h.add(jdeHeadContext, jdeIndent+StyleMuted.Render(
			"This line is already voided; deleting removes it and its void from the order."))
	}
	return h.add(jdeHeadDecorative, "")
}

// viewDeleteLine draws the confirmation. A frame whose whole job is to name
// what will be destroyed cannot be drawn without a line to name, so where
// editLineIdx addresses none it draws the FORM — which is the phase
// reseatLineIndexes has already put the screen back on when that can happen.
func (s *PurchaseOrderEditScreen) viewDeleteLine() string {
	li, ok := s.addressedLine()
	if !ok {
		return s.viewForm()
	}
	if !s.removalPhaseHolds() {
		return s.viewLineEdit()
	}
	width := s.bodyWidth()

	body := &jdeLines{}
	for _, caveat := range s.deleteCaveats() {
		for _, line := range jdeCaveatLines(caveat, width) {
			body.Add(line)
		}
		body.Add("")
	}
	if s.deleteNote != "" {
		for _, line := range jdeCaveatLines(s.deleteNote, width) {
			body.Add(line)
		}
	}
	return s.jdeScreen.frameWithHeader(
		s.deleteHeader(li, width), body, 0,
		s.statusRow(s.saving, "Deleting…", s.errMsg), s.deleteBar())
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
	case poEditPhaseDeleteLine:
		return s.viewDeleteLine()
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

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *PurchaseOrderEditScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, s.rowCount(), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those:
// a bar that advertised Ctrl-E on a row with nothing to open would be teaching
// the operator a key that does nothing.
func (s *PurchaseOrderEditScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Exit"}, {"UP/DN", "Fields"}}
	switch {
	case s.isSelectRow(s.cursor):
		items = append(items, actionBarItem{"←→", "Change"})
	case s.cursorOnAssoc():
		items = append(items, actionBarItem{"Ctrl-E", "Pick"})
	case s.cursorOnLine():
		items = append(items, actionBarItem{"Ctrl-E", "Edit line"})
	}
	if paging {
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
func (s *PurchaseOrderEditScreen) lineFields(li omsapi.PurchaseOrderItem) []jdeField {
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
		// Which removal — if either — this ORDER offers. One predicate for the
		// bar and for the arm behind it (removalOffered), so the legend and the
		// key cannot come to disagree about what Ctrl-E opens.
		if label, ok := s.removalOffered(); ok {
			items = append(items, actionBarItem{"Ctrl-E", label})
		}
	}
	return items
}

func (s *PurchaseOrderEditScreen) viewLineEdit() string {
	li, ok := s.addressedLine()
	if !ok {
		return s.viewForm()
	}

	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Edit line: ") + li.DisplayLabel())
	body.Add(jdeIndent + StyleMuted.Render(poLineEditSubtitle(li)))
	body.Add("")
	// One block, so it finds its own label column — unlike the header form,
	// whose two bands share one.
	for i, line := range renderJDEFields(s.lineFields(li), s.bodyWidth()) {
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
	// The status row's own standing note, drawn on the same test the bar makes:
	// what Ctrl-E opens there is decided by the ORDER, not by the line, so a row
	// reading "open" says nothing about whether removal is delete or void.
	if note := s.removalNote(); note != "" && s.lineFocus == poLineRowStatus {
		for _, line := range jdeCaveatLines(note, s.bodyWidth()) {
			body.Add(line)
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
	return s.frame(body, cursorLine, "Saving…", poEditAssocBar)
}

// poEditAssocBar is the association picker's bar, said ONCE: the movement arm
// needs it to ask the layer whether the frame is drawn before it moves the
// highlight, and a second literal beside the view's would be a bar measured
// that is not the bar drawn.
var poEditAssocBar = []actionBarItem{{"Enter", "Select"}, {"Esc", "Cancel"}, {"UP/DN", "Move"}}

// ---------------------------------------------------------------------------
// Void prompt
// ---------------------------------------------------------------------------

// voidSearchSentence names the key, and it names WHERE the key works.
//
// The qualifier is load-bearing rather than throat-clearing: this screen
// returns true from WantsRawInput, so the root never sees a keystroke and
// `ctrl+k` does NOT open the search palette here — it reaches the focused
// Reason box, where bubbles binds it to "delete to end of line". A sentence
// reading "ctrl+k finds it" on this frame would name a key that, pressed where
// it is named, silently eats what the operator has typed. That is why the
// one-row headline names the REMEDY and not the key at all, and why the key is
// named only in the prose that has room to say when it applies. The retired
// delete caveat got this right ("once you leave this screen …"); the first
// rewrite of it did not.
//
// THE QUALIFIER PRECEDES THE KEY, AND THAT ORDER IS THE GUARANTEE. This sits at
// the tail of a caveat the header trims from the end, so it can be cut — and
// with the key ahead of the qualifier a cut would leave "…ctrl+k" standing
// alone, which is the bare invitation this sentence exists to avoid. Written
// this way round the cut can only ever take the key, never the condition on it.
// TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning reported the
// wrong order at 80x18 the moment it was written.
//
// IT NAMES THE ORDER, BECAUSE "BY NUMBER" IS USELESS WITHOUT THE NUMBER. Every
// persisted order has one — PurchaseOrder.save() auto-assigns it — but nothing
// on THIS frame showed it: the essential row names the LINE, and the order's
// number is two screens back on the detail sheet. A remedy that tells the
// operator to search for a token the frame withholds is a remedy they can only
// use if they wrote it down first, which is the dead end one step removed. A
// payload that carried no number falls back to the unqualified wording rather
// than drawing an empty quote.
//
// THE NUMBER PRECEDES "BY NUMBER", AND THAT IS THE SAME GUARANTEE ONE CLAUSE
// LATER. It was appended at the TAIL — "…reaches it by number: PO-2026-0042." —
// which put the one word the sentence exists to deliver in the position the
// trim takes first. jdeWrapNote broke the poRemovalUnknown wording so the
// number landed alone on the last prose row, jdeFitHeader dropped exactly that
// row at 80x18, and the pane went on telling the operator to search by a number
// it had stopped showing: the dead end this whole caveat was rewritten to
// remove, reintroduced by word order. Written this way round the bound holds BY
// CONSTRUCTION rather than by luck — the header trims from the END, so any trim
// that keeps "by number" necessarily keeps everything ahead of it, the number
// included, and no re-wrapping at any width can separate them.
//
// The poRemovalVoid wording never drew that dead end, and it is worth saying
// why it did not: its own words happened to break so that "by number" and the
// number shared a row at 80, 100 and 120. A check green for a reason unrelated
// to the property it names is the failure this project keeps closing, and the
// sweep that certified it was single-branch — see
// TestPOLineRemove_AShortVoidPaneKeepsTheVanishingOrderWarning, which walks
// both answers now.
func voidSearchSentence(po *omsapi.PurchaseOrder) string {
	if po == nil || po.Number == "" {
		return "Once you leave this screen, ctrl+k search still reaches it."
	}
	return "Once you leave this screen, ctrl+k search reaches " + po.Number + " by number."
}

// voidStandingNote is what voiding does on every order, true whatever else the
// frame says. It is a CONTEXT row, and it is emitted after the vanishing
// warning on purpose — see voidHeader.
const voidStandingNote = "This marks the line voided and the supplier link discontinued."

// voidCaveats are the prompt's conditional sentences — what voiding THIS line
// costs the operator's ability to find the order again — and nil where voiding
// it costs them nothing.
//
// THIS IS THE WARNING THE DELETE CONFIRM USED TO CARRY, and it is here because
// this is where the loss survived oms-a8o. The list hides an order only when it
// has lines, none of them active, AND it is outside PRE_SUPPLIER_STATUSES; the
// delete confirm is inside that set by construction (poLastActiveLine), and
// this prompt on the poRemovalVoid answer is demonstrably outside it, because
// `can_delete_items` came back FALSE off exactly that frozenset.
//
// THE WAY BACK IS DIFFERENT HERE, AND SAYING SO IS THE POINT. The old sentence
// offered ctrl+k "until another line is added", which is a true remedy on a
// draft and a false one here: past the pre-supplier boundary OMS refuses to add
// a line (services.line_entry.assert_addable) and has no unvoid endpoint at all
// — is_voided is only ever written true — so nothing on either side of the wire
// undoes this. Search is not one way back among several, it is the only one,
// and a warning naming a remedy the operator cannot reach would be worse than
// the silence it replaced.
//
// IT IS A ONE-ROW HEADLINE AND A DETAIL BEHIND IT, BECAUSE A WARNING CUT BEFORE
// ITS WAY BACK IS A DEAD END AND ONLY A ONE-ROW CLAIM CANNOT BE CUT.
// jdeFitHeader drops header rows from the END of a rank, so a caveat spanning
// several rows loses its tail first — and the tail is where a remedy naturally
// falls. Both longer wordings tried here shipped that dead end and were caught
// by the height sweep, not by reading: one caveat read "…takes the order off
// every purchase-order list, and nothing puts it back:" and stopped at 80x14,
// and the two-row rewrite of it read "…takes the order off every purchase-order
// list;" and stopped at 80x13. Shortening is not a fix, it only moves the
// height, because the budget goes to zero one row at a time.
//
// So the FIRST caveat is a single row wherever it is drawn, states the loss AND
// the way back, and is emitted first so it is the last thing dropped: wherever
// this frame warns at all, it warns completely. Everything that EXPLAINS the
// warning — which line, why the order is past the boundary, what "nothing puts
// it back" rests on — is the second caveat, where the pane takes it first and
// takes it as prose the headline has already summarised. The same
// headline-then-detail split setErr makes on the status row, for the same
// reason: one surface can be trimmed and the other cannot.
//
// Do not merge them back to save a row. Redraw the frame at 80x12, 80x13 and
// 80x14 first; that is where every version of this sentence has failed.
//
// THE RULE THIS FRAME KEEPS: IT MUST NEVER STATE A LOSS WITHOUT ITS REMEDY.
// A warning is only legitimate where the operator can act on it, so a pane
// carrying "the order goes off every list" and not carrying "search still finds
// it" is not a shortened warning, it is a dead end — the very thing this work
// was opened to remove.
//
// AND THE MECHANISM THAT KEEPS IT IS ONE SENTENCE: jdeFitHeader and
// jdeCaveatLines BOTH give ground from the TAIL, therefore WHATEVER MUST
// SURVIVE MUST LEAD. That is one structural rule with three instances on this
// one frame, and it is written here once rather than three times as three
// tricks:
//
//   - the qualifier leads `ctrl+k`, so a cut takes the key and never the
//     condition on it (voidSearchSentence);
//   - the order NUMBER leads "by number", so a cut takes the phrase and never
//     the token it tells the operator to search for (voidSearchSentence);
//   - the REMEDY leads the LOSS, here, so every prefix of the folded headline
//     either makes no loss claim at all or carries the remedy with it.
//
// A property that holds BY CONSTRUCTION at every width beats one that holds
// above a threshold nothing enforces. The loss-first wording held only at >= 80
// columns and nothing said so: Root draws from a terminal width of 45 up
// (app.go's contentWidth gate), and at 60 columns screenBodyWidth is 31, which
// leaves jdeCaveatLines 29 cells; "Voiding hides the order; only search finds
// it." breaks at exactly 29 into "Voiding hides the order; only" and "search
// finds it.", and a trim keeping the first row alone drew the loss with the
// remedy gone.
//
// WHERE EVEN THAT IS NOT ENOUGH, NOTHING IS DRAWN — the headline AND the prose,
// as a unit. Below roughly 74 columns no wording carrying both facts folds to
// one row (the budget is 18 cells at the narrowest drawable pane), so the
// caveats are gated on the width the terminal REALLY gave: refuse rather than
// mutilate, which is the stance jdeTooShort already takes one level up. This is
// NOT the silence rule 1 forbids — that rule is about a keypress changing
// nothing visible, and nothing here is an answer to a key; it is the choice
// between half a warning that strands the operator and none. The gate takes
// BOTH caveats because the prose states the loss too, so dropping the headline
// alone would reintroduce the dead end through the other half and break the
// "prose never survives without the headline" property beside it.
//
// The gate reads s.bodyWidth() and no named width, deliberately: AGENTS.md says
// 80 columns is the width that must HOLD while app.go draws down to 45, and
// that is a question for somebody else. A gate computed from the real pane
// needs no answer to it.
//
// THE UNKNOWN ANSWER CONCLUDES NOTHING. poRemovalUnknown lands on this prompt
// too, and there the client does not know which side of the boundary the order
// is on, so it does not know whether the void hides it. Found-nothing and
// could-not-tell are different facts: the leading sentence hedges the LOSS
// rather than asserting either outcome, and the sentence that names the
// withholding is the expendable half — an operator who loses it has read a
// "may", not a claim.
//
// WHICH IS ALSO WHY ONLY THE VOID WORDING SAYS "ONLY". On the void answer the
// order is demonstrably past the boundary, so search really is the single way
// back. On the unknown answer it may not be hidden at all, and there the lists
// would still find it — so "only search finds the order" is a claim that
// branch cannot make. The hedge belongs on the loss, and the remedy that leads
// is the half that is true either way.
func (s *PurchaseOrderEditScreen) voidCaveats(width int) []string {
	if !poLastActiveLine(s.po, s.editLineIdx) {
		return nil
	}
	headline, ok := voidHeadlines[s.lineRemoval()]
	if !ok {
		// poRemovalDelete, which removalPhaseHolds has already closed this
		// prompt on: the flag flipped under an open frame and the screen is on
		// its way back to the line editor.
		return nil
	}
	if !voidCaveatsFit(width) {
		return nil
	}
	switch s.lineRemoval() {
	case poRemovalVoid:
		return []string{
			headline,
			"This is the order's only unvoided line, and the supplier holds this order, so voiding it leaves the order off every purchase-order list. Nothing puts it back: OMS will not add a line to an order past that point and has nothing that lifts a void. " + voidSearchSentence(s.po),
		}
	default:
		return []string{
			headline,
			"This is the order's only unvoided line. This server did not say whether the supplier already has the order (can_delete_items); if it does, voiding leaves it off every purchase-order list with nothing to put it back. " + voidSearchSentence(s.po),
		}
	}
}

// voidHeadlines is every leading caveat voidCaveats can draw, one per answer
// that reaches this prompt. It is a map rather than two literals inside the
// switch because voidCaveatsFit has to measure the WHOLE SET, and a set that
// lived in the switch could only be measured one branch at a time.
var voidHeadlines = map[poLineRemoval]string{
	poRemovalVoid:    "Only search finds the order; voiding hides it.",
	poRemovalUnknown: "Search finds the order; voiding may hide it.",
}

// voidCaveatsFit is the width gate, and it answers for EVERY headline rather
// than for the one about to be drawn.
//
// THE SET, NOT THE BRANCH, BECAUSE OTHERWISE THE THRESHOLD IS PER-WORDING AND
// THE SEVERITIES INVERT. Asked of the branch alone, the gate opened at whatever
// width that branch's sentence happened to fit: the CERTAIN-loss wording is two
// cells longer than the hedged one, so across a band of widths the prompt went
// silent about a loss the server had CONFIRMED while still warning about one it
// had only left possible. A warning about what will happen must survive at
// least as far as a warning about what might. Taking the maximum over the set
// makes the threshold single BY CONSTRUCTION, so a later reword of one branch
// moves both together instead of quietly reopening the inversion.
//
// The property is "both answers are withheld together or drawn together"; the
// width it currently evaluates to is 77 columns and up, measured with the
// layer's own functions (screenBodyWidth(77) = 48, which is jdeCaveatLines'
// 46-cell budget for the longer headline plus the two-cell indent). That number
// is an OUTPUT of the wordings and not a rule — reword them and it moves, and
// the check that holds the property is written so that it moves with them.
func voidCaveatsFit(width int) bool {
	for _, headline := range voidHeadlines {
		if len(jdeCaveatLines(headline, width)) != 1 {
			return false
		}
	}
	return true
}

// voidHeader is the prompt's pinned block.
//
// THE CAVEATS ARE PINNED AND THE FIELD IS THE BODY, and that split is forced
// rather than chosen. jdeLines anchors its window on the cursor's block and
// keeps the block's START, so a caveat written into the body ahead of the
// Reason row pushes the ROW off a short pane — every rune then typed into a box
// the operator cannot see redraws a byte-identical frame — while one written
// after it is simply the tail a short window drops. A body with one navigable
// row cannot hold both; the header can, because jdeFitHeader gives ground BY
// RANK instead of by position.
//
// THE ORDER WITHIN THE CONTEXT RANK IS A DECISION, NOT A LAYOUT ACCIDENT.
// jdeFitHeader drops rows of a rank from the END, so the last context row
// emitted is the first a short pane loses, and these are emitted in rising
// order of expendability: the one-row warning, then the prose explaining it
// (voidCaveats), then the standing note. The vanishing warning leads
// because it is the one fact on this frame nothing else carries — that voiding
// is a striking-off is restated by the bar's `Enter=Void line`, by the
// essential row's own `Void:` lead and by the reason the prompt asks for at
// all, while "the order leaves every list and search is the only way back" is
// said here or nowhere. Same reasoning as deleteCaveats made one surface over,
// and as jdeHeadRank makes one level up.
func (s *PurchaseOrderEditScreen) voidHeader(li omsapi.PurchaseOrderItem, width int) jdeHeader {
	h := jdeHeader(nil).add(jdeHeadEssential, removalHeadline("Void: ", StyleStatusWarn, li, width))
	for _, caveat := range s.voidCaveats(width) {
		h = h.addBlock(jdeHeadContext, jdeCaveatLines(caveat, width))
	}
	h = h.addBlock(jdeHeadContext, jdeCaveatLines(voidStandingNote, width))
	return h.add(jdeHeadDecorative, "")
}

func (s *PurchaseOrderEditScreen) viewVoidLine() string {
	li, ok := s.addressedLine()
	if !ok {
		return s.viewForm()
	}
	if !s.removalPhaseHolds() {
		return s.viewLineEdit()
	}
	width := s.bodyWidth()
	field := jdeField{
		Label:   "Reason",
		Kind:    jdeText,
		Input:   &s.voidReason,
		Width:   40,
		Hint:    "required",
		Focused: true,
	}

	body := &jdeLines{}
	body.AddRow(0, renderJDEFields([]jdeField{field}, width)[0])
	return s.jdeScreen.frameWithHeader(
		s.voidHeader(li, width), body, 0,
		s.statusRow(s.saving, "Voiding…", s.errMsg), s.voidBar())
}

// voidBar is the void prompt's legend, said ONCE — the same reason deleteBar
// and poEditAssocBar are methods rather than literals. The phase sweep reads
// the bar a phase DRAWS and presses the whole key space against it; a literal
// restated in the sweep would be a bar measured that is not the bar drawn, so a
// key added here would be judged against a stale copy and pass silently, which
// is exactly the hand-kept-roster failure that sweep exists to prevent.
func (s *PurchaseOrderEditScreen) voidBar() []actionBarItem {
	if s.saving || !s.removalPhaseHolds() {
		return []actionBarItem{{"Esc", "Back"}}
	}
	return []actionBarItem{{"Enter", "Void line"}, {"Esc", "Cancel"}}
}

// stringPtr returns a pointer to s. Used to send a metadata/line field even
// when empty (an empty expected-delivery/ship-date is the clear signal).
func stringPtr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Shared windowed-list renderer
// ---------------------------------------------------------------------------

// windowedListDefaultRows is the block height a caller that has not measured
// its pane gets. It is the old fixed ten-row window plus its two markers, so an
// unbudgeted caller draws exactly what it always did.
const windowedListDefaultRows = 12

// renderWindowedList draws `total` items via the supplied formatter, keeping
// `cursor` on screen inside a block of `rows` terminal lines — MARKERS
// INCLUDED. Same pattern as the supplier picker so all four pickers look
// consistent, and a free function rather than a method on the create screen,
// because the PO edit screen's association pickers draw their lists the same
// way.
//
// rows is a budget, not a preference: `clampToBox` drops whatever runs past the
// bottom of the pane, and a row it drops out of a PICKER is a row the cursor
// can still be moved onto and enter can still stage. An item going onto a
// purchase order that the operator cannot see is a wrong purchase order, so a
// list that does not fit says how many rows it hid rather than losing them
// silently — and the markers that say it are counted inside the budget, not
// added on top of it. rows <= 0 keeps the historic ten.
//
// The formatter is handed the CELLS its row may draw into as well as the index,
// because the horizontal cut is the same defect as the vertical one: a row
// clampToBox trims loses its right-hand end — the SKU and the price an item is
// picked on — with no mark to say it happened. The room is computed once here
// (windowedListRoom) rather than by each formatter, so no picker can be the one
// that forgets the caret or the highlight, and it comes from the pane the
// caller is really drawing into rather than from the 51-column floor.
func renderWindowedList(total, cursor, rows, width int, formatRow func(i, room int) string) string {
	if rows <= 0 {
		rows = windowedListDefaultRows
	}
	if rows < 3 {
		rows = 3
	}
	// Fit the item rows and their markers together. Reserving a marker shrinks
	// the window, which can move it to an edge and remove the need for that
	// marker, so this settles rather than assuming: at most two passes change
	// anything, and a spare row left over beats a clipped one.
	visible, start, end := rows, 0, 0
	for i := 0; i < 3; i++ {
		start, end = windowedListSpan(total, cursor, visible)
		markers := 0
		if start > 0 {
			markers++
		}
		if end < total {
			markers++
		}
		if visible+markers <= rows {
			break
		}
		if visible = rows - markers; visible < 1 {
			visible = 1
		}
	}

	var b strings.Builder
	if start > 0 {
		// The newline stays OUTSIDE Render: lipgloss treats a styled string
		// containing one as a two-line block and pads the short line, which
		// leaked twenty columns of padding onto the row underneath the marker
		// and pushed that row past the 51-column cut.
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	room := windowedListRoom(width)
	for i := start; i < end; i++ {
		caret := "    "
		if i == cursor {
			caret = "  ▸ "
		}
		line := caret + formatRow(i, room)
		if i == cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < total {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", total-end)) + "\n")
	}
	return b.String()
}

// windowedListSpan centres a window of `size` item rows on cursor.
func windowedListSpan(total, cursor, size int) (start, end int) {
	if size >= total {
		return 0, total
	}
	start = cursor - size/2
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > total {
		end = total
		start = end - size
		if start < 0 {
			start = 0
		}
	}
	return start, end
}
