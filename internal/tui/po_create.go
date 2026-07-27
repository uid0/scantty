// PurchaseOrderCreateScreen — multi-phase create-PO form for scantty.
//
// scantty's primary UX is scanner-driven, but the gap "I'm at the
// workstation and want to open a PO without context-switching to the
// web UI" came up often enough to warrant a focused form. The flow is
// now a small state machine:
//
//	poPhaseSupplier   — j/k pick a supplier from the loaded list
//	                    (PR #38 picker; enter commits and advances)
//	poPhaseSource     — choose where the next line comes from:
//	                      r → items the supplier has on the reorder queue
//	                      i → other inventory items associated with the supplier
//	                      a → assets purchased from the supplier
//	                      f → a freeform line (no item/asset reference)
//	                    The staged cart is listed here too, and j/k highlight a
//	                    line so ctrl+e edits it / x removes it without first
//	                    going to review — the operator is never limited to
//	                    popping the last line (sc-ytr5).
//	poPhaseReorderPick / ItemPick / AssetPick — list pickers backed by
//	                    the corresponding omsapi endpoints; enter
//	                    prefills the line buffer and jumps to poPhaseLine.
//	                    The reorder picker also does bulk adds: space marks
//	                    rows and a adds the supplier's WHOLE reorder queue,
//	                    each seeded from its suggested_quantity, landing
//	                    straight in the review cart (sc-ytr5).
//	poPhaseLine       — description/qty/cost (+ an optional expected-date
//	                    field for inventory lines) inputs, pre-filled when the
//	                    line came from a picker; enter ADDS the line to the
//	                    cart and returns to poPhaseSource so more lines can be
//	                    added (the web create form is multi-line —
//	                    [[ship-complete-features]]).
//	                    Cost is REQUIRED on asset/freeform lines and an
//	                    optional OVERRIDE on item-supplier-backed (catalog)
//	                    lines: it is prefilled from the catalog price and a
//	                    blank field omits unit_cost so the backend prices the
//	                    line from the item-supplier's stored unit_cost, which
//	                    is what the web create form does too (sc-gnzw).
//	                    Case-packed inventory lines (qpp > 1) enter that cost
//	                    per case or per unit (ctrl+t toggles the basis,
//	                    deriving unit_cost = case_cost / qpp — op-7j8v).
//	                    Only item-supplier-backed lines prompt for a per-line
//	                    expected shipment date: the backend's create path reads
//	                    expected_shipment_date on the item_supplier branch only
//	                    and drops it for asset/freeform lines (sc-ytr5).
//	poPhaseReview     — the accumulated line cart + a PO-level notes
//	                    input; enter submits every line at once via
//	                    PurchaseOrderCreate.
//
// A PO can hold as many lines as the operator adds: each line is staged in
// s.lines and the whole cart is POSTed once from the review phase.
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

// Phase enum. The screen tracks which surface owns input right now
// so the same .View()/.Update() can render either the supplier
// picker, the source-chooser menu, one of the three pickers, or the
// final line-entry form.
type poPhase int

const (
	poPhaseSupplier poPhase = iota
	poPhaseSource
	poPhaseReorderPick
	poPhaseItemPick
	poPhaseAssetPick
	poPhaseLine
	poPhaseReview
)

// Field indexes inside the line-entry form (Phase 4). Every line collects a
// unit cost: required on asset and freeform lines, an optional override on
// item-supplier-backed lines where a blank field means "price it from the
// catalog" (see lineFields). The expected shipment date is not symmetric: only
// item-supplier-backed lines collect it, because create_purchase_order reads
// expected_shipment_date on the item_supplier branch alone. PO-level notes live
// in the review phase.
const (
	poLineFieldDesc = iota
	poLineFieldQty
	poLineFieldCost
	poLineFieldDate
	poLineFieldCount
)

// poCartLine is one staged line in the multi-line create cart: the wire payload
// plus a human label rendered in the source/review lists, and the picked line's
// quantity_per_package so an in-place edit (ctrl+e) can restore a case-packed
// line's unit/case cost basis. qpp is 0/1 for non-case lines (asset, freeform,
// single-pack inventory).
type poCartLine struct {
	item  omsapi.PurchaseOrderCreateItem
	label string
	qpp   int
}

type PurchaseOrderCreateScreen struct {
	deps    Deps
	phase   poPhase
	pending bool
	errMsg  string

	// Phase 1: supplier picker (unchanged from PR #38).
	suppliers       []omsapi.Supplier
	supplierLoading bool
	supplierLoadErr string
	supplierCursor  int
	supplierID      int

	// Phase 3a: reorder-queue items for this supplier. reorderSelected marks
	// rows toggled with space for a bulk add (keyed by index into
	// reorderItems); it is cleared whenever the list reloads so a stale index
	// can never select the wrong row.
	reorderItems    []omsapi.ReorderDataItem
	reorderLoading  bool
	reorderLoadErr  string
	reorderCursor   int
	reorderSelected map[int]bool

	// Phase 3b: inventory items for this supplier.
	itemSuppliers       []omsapi.ItemSupplier
	itemSuppliersAll    []omsapi.ItemSupplier // unfiltered page so '/' search is client-side
	itemSuppliersLoad   bool
	itemSuppliersErr    string
	itemSuppliersCur    int
	itemSuppliersSearch textinput.Model
	itemSuppliersTyping bool

	// Phase 3c: assets-from-supplier picker (server-side search).
	assets        []omsapi.Asset
	assetsLoading bool
	assetsErr     string
	assetsCursor  int
	assetsSearch  textinput.Model
	assetsTyping  bool
	assetsPage    int
	assetsHasNext bool

	// Phase 4: line-entry form. The pointer fields drive which
	// PurchaseOrderCreateItem shape we build at add time —
	// item_supplier_id / asset_id / freeform description.
	lineInputs    []textinput.Model
	lineFocused   int
	pickedItemSup *int    // set when the line came from Phase 3a/3b
	pickedAssetID *string // set when the line came from Phase 3c

	// Case-cost basis for the Phase-4 cost field. When a picked inventory line
	// is case-packed (pickedQPP > 1) the cost field can hold either a per-unit
	// or a per-case cost; costBasisCase selects which, and the payload always
	// carries the derived per-item unit_cost (unit = case / qpp). pickedQPP is
	// 0/1 for non-case lines (asset, freeform, reorder, single-pack inventory),
	// which keeps their single unit-cost entry unchanged. (op-7j8v parity.)
	pickedQPP     int
	costBasisCase bool

	// Multi-line cart. Each entered line is staged here; the whole cart is
	// POSTed once from the review phase. reviewCursor highlights a line for
	// edit/remove and is shared by the review cart AND the source chooser's
	// cart list, so a line highlighted in one is still highlighted in the
	// other; poNotes is the PO-level notes field.
	lines        []poCartLine
	reviewCursor int
	poNotes      textinput.Model

	// editIndex is the s.lines index being edited in place (ctrl+e re-opens the
	// Phase-4 form pre-filled), or -1 when the line form is adding a new line.
	// enterLinePhase resets it to -1 on every entry and the edit path re-sets it
	// afterward, so a stale index can never turn a later add into an in-place
	// replace. editReturn is the phase the edit was launched from (review cart or
	// source chooser) — saving or cancelling goes back there rather than always
	// dumping the operator in review.
	editIndex  int
	editReturn poPhase
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
	s := &PurchaseOrderCreateScreen{
		deps:            deps,
		phase:           poPhaseSupplier,
		supplierLoading: true,
		supplierCursor:  -1,
		editIndex:       -1,
		editReturn:      poPhaseReview,
		reorderSelected: map[int]bool{},
	}

	// Line-entry inputs (Phase 4).
	s.lineInputs = make([]textinput.Model, poLineFieldCount)
	desc := textinput.New()
	desc.Prompt = ""
	desc.Placeholder = "item description (freeform line)"
	desc.CharLimit = 200
	s.lineInputs[poLineFieldDesc] = desc

	qty := textinput.New()
	qty.Prompt = ""
	qty.Placeholder = "quantity"
	qty.CharLimit = 10
	s.lineInputs[poLineFieldQty] = qty

	cost := textinput.New()
	cost.Prompt = ""
	cost.Placeholder = "unit cost (e.g. 12.50)"
	cost.CharLimit = 20
	s.lineInputs[poLineFieldCost] = cost

	date := textinput.New()
	date.Prompt = ""
	date.Placeholder = "YYYY-MM-DD (optional)"
	date.CharLimit = 12
	s.lineInputs[poLineFieldDate] = date

	// PO-level notes, captured once in the review phase.
	poNotes := textinput.New()
	poNotes.Prompt = ""
	poNotes.Placeholder = "notes for the whole PO (optional)"
	poNotes.CharLimit = 500
	s.poNotes = poNotes

	// Picker search inputs (Phase 3b/3c).
	is := textinput.New()
	is.Prompt = ""
	is.Placeholder = "filter by name / SKU"
	is.CharLimit = 60
	s.itemSuppliersSearch = is

	as := textinput.New()
	as.Prompt = ""
	as.Placeholder = "search (name / tag / serial)"
	as.CharLimit = 60
	s.assetsSearch = as

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
		return s, tea.Batch(
			Status(fmt.Sprintf("created %s", poNum), StatusOK),
			SwitchTo(WSPurchasing, nil),
		)

	// Picker messages live in po_create_pickers.go.
	case poReorderItemsLoadedMsg, poItemSuppliersLoadedMsg, poAssetsLoadedMsg:
		return s, s.handlePickerLoaded(msg)

	case tea.KeyMsg:
		switch s.phase {
		case poPhaseSupplier:
			return s.updateSupplierPhase(m)
		case poPhaseSource:
			return s.updateSourcePhase(m)
		case poPhaseReorderPick:
			return s.updateReorderPickPhase(m)
		case poPhaseItemPick:
			return s.updateItemPickPhase(m)
		case poPhaseAssetPick:
			return s.updateAssetPickPhase(m)
		case poPhaseLine:
			return s.updateLinePhase(m)
		case poPhaseReview:
			return s.updateReviewPhase(m)
		}
	}

	// Forward unhandled msgs to whichever textinput owns input now.
	switch s.phase {
	case poPhaseLine:
		var cmd tea.Cmd
		s.lineInputs[s.lineFocused], cmd = s.lineInputs[s.lineFocused].Update(msg)
		return s, cmd
	case poPhaseReview:
		var cmd tea.Cmd
		s.poNotes, cmd = s.poNotes.Update(msg)
		return s, cmd
	case poPhaseItemPick:
		if s.itemSuppliersTyping {
			var cmd tea.Cmd
			s.itemSuppliersSearch, cmd = s.itemSuppliersSearch.Update(msg)
			return s, cmd
		}
	case poPhaseAssetPick:
		if s.assetsTyping {
			var cmd tea.Cmd
			s.assetsSearch, cmd = s.assetsSearch.Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Phase: Supplier picker
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateSupplierPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "j", "down":
		if s.supplierCursor < len(s.suppliers)-1 {
			s.supplierCursor++
		}
	case "k", "up":
		if s.supplierCursor > 0 {
			s.supplierCursor--
		}
	case "enter", "tab":
		s.commitSupplier()
		if s.supplierID > 0 {
			s.phase = poPhaseSource
		}
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) commitSupplier() {
	if s.supplierCursor < 0 || s.supplierCursor >= len(s.suppliers) {
		return
	}
	s.supplierID = s.suppliers[s.supplierCursor].ID
}

// ---------------------------------------------------------------------------
// Phase: Source chooser (r/i/a/f)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateSourcePhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "b":
		// Back to supplier picker — operator can change their mind
		// before committing to any source. Pick stays committed so
		// they don't have to re-pick if they only want a different
		// supplier source.
		s.phase = poPhaseSupplier
		return s, nil
	case "r":
		s.phase = poPhaseReorderPick
		s.reorderLoading = true
		s.reorderLoadErr = ""
		return s, s.loadReorderItemsForSupplier()
	case "i":
		s.phase = poPhaseItemPick
		s.itemSuppliersLoad = true
		s.itemSuppliersErr = ""
		s.itemSuppliersSearch.SetValue("")
		s.itemSuppliersTyping = false
		return s, s.loadItemSuppliersForSupplier()
	case "a":
		s.phase = poPhaseAssetPick
		s.assetsLoading = true
		s.assetsErr = ""
		s.assetsSearch.SetValue("")
		s.assetsTyping = false
		s.assetsPage = 1
		return s, s.loadAssetsForSupplier("")
	case "f":
		// Freeform: go straight to the line form with nothing
		// pre-filled.
		s.enterLinePhase(nil, nil, "", 0, 0, 0, 0)
		return s, textinput.Blink
	case "d":
		// Done adding lines → review + submit. Only meaningful once the
		// cart has at least one line (the backend rejects an empty PO).
		if len(s.lines) == 0 {
			s.errMsg = "add at least one line before submitting"
			return s, Status(s.errMsg, StatusError)
		}
		s.phase = poPhaseReview
		s.clampReviewCursor()
		s.poNotes.Focus()
		return s, textinput.Blink
	case "j", "down":
		// Move the cart highlight — the same cursor the review phase uses, so
		// x / ctrl+e below act on ANY line, not just the last one added.
		if s.reviewCursor < len(s.lines)-1 {
			s.reviewCursor++
		}
		return s, nil
	case "k", "up":
		if s.reviewCursor > 0 {
			s.reviewCursor--
		}
		return s, nil
	case "x":
		// Remove the highlighted line. The cursor follows each add (addLine
		// parks it on the new line), so with an untouched highlight this is
		// still the "undo the line I just added" it always was — but j/k can
		// now aim it at any line in the cart.
		s.removeLineAt(s.reviewCursor)
		return s, nil
	case "ctrl+e":
		// Edit the highlighted line in place without a detour through review.
		// Same handler the review cart uses; saving/cancelling comes back here
		// (editReturn) so the operator keeps adding lines where they left off.
		// ctrl+e (not a bare letter) matches the review-cart chord.
		return s, s.openLineEditor(s.reviewCursor, poPhaseSource)
	}
	return s, nil
}

// clampReviewCursor keeps the shared cart cursor inside the cart after lines
// are added or removed.
func (s *PurchaseOrderCreateScreen) clampReviewCursor() {
	if s.reviewCursor >= len(s.lines) {
		s.reviewCursor = len(s.lines) - 1
	}
	if s.reviewCursor < 0 {
		s.reviewCursor = 0
	}
}

// removeLineAt drops one staged cart line and keeps the shared cursor in range.
// Shared by the source chooser (x) and the review cart (ctrl+x) so per-line
// removal behaves identically from both. Emptying the cart from review returns
// to the source chooser — there is nothing left to review.
func (s *PurchaseOrderCreateScreen) removeLineAt(idx int) {
	if idx < 0 || idx >= len(s.lines) {
		return
	}
	s.lines = append(s.lines[:idx], s.lines[idx+1:]...)
	s.clampReviewCursor()
	s.errMsg = ""
	if len(s.lines) == 0 && s.phase == poPhaseReview {
		s.phase = poPhaseSource
		s.poNotes.Blur()
	}
}

// openLineEditor re-opens the Phase-4 form pre-filled from cart line idx for an
// in-place edit, remembering the phase to return to on save or cancel. Shared by
// the review cart (ctrl+e) and the source chooser (ctrl+e) so editing any line
// works the same wherever it was launched from.
func (s *PurchaseOrderCreateScreen) openLineEditor(idx int, back poPhase) tea.Cmd {
	if idx < 0 || idx >= len(s.lines) {
		return nil
	}
	line := s.lines[idx]
	item := line.item
	var unitCost float64
	if item.UnitCost != nil {
		unitCost = *item.UnitCost
	}
	s.poNotes.Blur()
	// packageCost = 0 with qpp > 1 makes enterLinePhase default to case basis
	// and prefill case cost = unit_cost × qpp, which round-trips exactly back
	// through poDeriveUnitCost on save. The line's source (item-supplier /
	// asset / freeform) is preserved — editing never re-targets a line.
	s.enterLinePhase(item.ItemSupplierID, item.AssetID, item.Description, item.Quantity, unitCost, 0, line.qpp)
	// enterLinePhase blanks every input for a fresh add, so the staged date is
	// restored afterward — same idiom as the editIndex assignment below.
	s.lineInputs[poLineFieldDate].SetValue(item.ExpectedShipmentDate)
	s.editIndex = idx
	s.editReturn = back
	return textinput.Blink
}

// returnFromLineForm leaves the line form for the phase the edit was opened
// from, focusing the notes input only when that phase is the review cart.
func (s *PurchaseOrderCreateScreen) returnFromLineForm() tea.Cmd {
	s.phase = s.editReturn
	if s.phase == poPhaseReview {
		s.poNotes.Focus()
		return textinput.Blink
	}
	s.poNotes.Blur()
	return nil
}

// ---------------------------------------------------------------------------
// Phase: Line entry + submit
// ---------------------------------------------------------------------------

// enterLinePhase moves the screen into Phase 4 and pre-fills the inputs.
// Either both pointers are nil (freeform), or exactly one is set. unitCost and
// packageCost prefill the cost field (0 = leave blank); qpp is the picked
// inventory line's quantity_per_package (>1 ⇒ case-packed, enabling the
// unit/case cost-basis toggle). It resets editIndex to -1 (add mode) on every
// entry — the single choke point every add path funnels through — so a stale
// edit target can never redirect a later add into an in-place replace; the
// review-cart edit path re-sets editIndex to the target line after calling this.
func (s *PurchaseOrderCreateScreen) enterLinePhase(
	itemSupplierID *int, assetID *string, desc string, qty int, unitCost, packageCost float64, qpp int,
) {
	s.phase = poPhaseLine
	s.editIndex = -1
	s.editReturn = poPhaseReview
	s.lineFocused = poLineFieldDesc
	s.errMsg = ""
	s.pickedItemSup = itemSupplierID
	s.pickedAssetID = assetID
	s.pickedQPP = qpp
	s.costBasisCase = false

	for i := range s.lineInputs {
		s.lineInputs[i].SetValue("")
		s.lineInputs[i].Blur()
	}
	s.lineInputs[poLineFieldDesc].SetValue(desc)
	if qty > 0 {
		s.lineInputs[poLineFieldQty].SetValue(strconv.Itoa(qty))
	}

	switch {
	case itemSupplierID != nil && qpp > 1:
		// Case-packed inventory line: default to per-case entry (the op-7j8v
		// headline) and prefill the case cost — the saved package_cost, or
		// unit_cost × qpp when package_cost is blank. Left blank when the
		// catalog carries neither, in which case no unit_cost is submitted and
		// the backend derives the line cost from the stored unit_cost (sc-5yr).
		s.costBasisCase = true
		caseCost := packageCost
		if caseCost <= 0 && unitCost > 0 {
			caseCost = unitCost * float64(qpp)
		}
		if caseCost > 0 {
			s.lineInputs[poLineFieldCost].SetValue(poFormatCost(caseCost))
		}
	default:
		// Per-unit cost prefill. Asset / freeform lines start blank (nothing
		// knows their cost); a single-pack catalog line is seeded with the price
		// the picker showed — the item-supplier's unit_cost, or the override
		// already staged on the line when re-opened with ctrl+e — so the
		// operator can keep it, change it, or clear it back to catalog pricing
		// (sc-gnzw).
		if unitCost > 0 {
			s.lineInputs[poLineFieldCost].SetValue(poFormatCost(unitCost))
		}
	}
	s.applyCostPlaceholder()
	s.lineInputs[s.lineFocused].Focus()
}

func (s *PurchaseOrderCreateScreen) updateLinePhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		// Esc cancels the line form. During an in-place edit it returns to
		// whichever phase opened the edit (review cart or source chooser) with
		// the line unchanged; on a fresh add it goes back to the source chooser
		// so the operator can pick a different line source without losing the
		// supplier.
		editing := s.editIndex >= 0
		s.pickedItemSup = nil
		s.pickedAssetID = nil
		s.pickedQPP = 0
		s.costBasisCase = false
		s.editIndex = -1
		if editing {
			return s, s.returnFromLineForm()
		}
		s.phase = poPhaseSource
		return s, nil
	case "tab", "down":
		s.focusNextLine(+1)
		return s, nil
	case "shift+tab", "up":
		s.focusNextLine(-1)
		return s, nil
	case "ctrl+t":
		// Toggle the cost field between per-unit and per-case entry for a
		// case-packed inventory line (a no-op otherwise). ctrl+t (not a bare
		// letter) so the key never collides with typing into the field.
		s.toggleCostBasis()
		return s, nil
	case "enter":
		return s, s.addLine()
	}
	var cmd tea.Cmd
	s.lineInputs[s.lineFocused], cmd = s.lineInputs[s.lineFocused].Update(m)
	return s, cmd
}

// lineFields returns the active field indexes for the current line source, in
// tab order. Every line carries a cost field, but it means different things:
// asset and freeform lines REQUIRE a unit cost (the backend rejects those
// branches without one), while item-supplier-backed lines take an OPTIONAL
// per-unit override — blank leaves unit_cost out of the payload and the backend
// prices the line from the item-supplier's stored cost (sc-5yr's default, kept
// as the default here). sc-5yr hid the field entirely on single-pack catalog
// lines, which left an operator staring at a $0 line they could not correct
// when the catalog cost was unset; the web create form has always allowed the
// override, so offering it is parity, not a new power (sc-gnzw). Case-packed
// inventory lines (qpp > 1) enter that cost per case or per unit (op-7j8v). The
// expected date field is not symmetric: inventory lines only (lineTakesDate).
func (s *PurchaseOrderCreateScreen) lineFields() []int {
	fields := []int{poLineFieldDesc, poLineFieldQty, poLineFieldCost}
	if s.lineTakesDate() {
		fields = append(fields, poLineFieldDate)
	}
	return fields
}

// lineTakesDate reports whether the current line source accepts a per-line
// expected shipment date. Only item-supplier-backed lines do: the backend's
// create_purchase_order reads expected_shipment_date on the item_supplier
// branch and never looks at it on the asset or freeform branches, so offering
// the field there would collect a value the PO silently drops. The one
// predicate feeds both lineFields (what renders) and addLine (what is sent), so
// the form and the payload can't drift.
func (s *PurchaseOrderCreateScreen) lineTakesDate() bool {
	return s.pickedItemSup != nil
}

// poLineFieldLabel maps a field index to its static form label. The cost row
// uses the basis-aware s.costFieldLabel instead (unit vs case).
func poLineFieldLabel(i int) string {
	switch i {
	case poLineFieldDesc:
		return "Description"
	case poLineFieldQty:
		return "Quantity"
	case poLineFieldCost:
		return "Unit cost"
	case poLineFieldDate:
		return "Expected date"
	default:
		return ""
	}
}

// costFieldLabel is the label for the Phase-4 cost row, reflecting the active
// cost basis: "Case cost" when entering a per-case cost for a case-packed
// inventory line, "Unit cost" otherwise.
func (s *PurchaseOrderCreateScreen) costFieldLabel() string {
	if s.costBasisCase {
		return "Case cost"
	}
	return "Unit cost"
}

func (s *PurchaseOrderCreateScreen) focusNextLine(delta int) {
	fields := s.lineFields()
	cur := 0
	for i, f := range fields {
		if f == s.lineFocused {
			cur = i
			break
		}
	}
	s.lineInputs[s.lineFocused].Blur()
	next := (cur + delta + len(fields)) % len(fields)
	s.lineFocused = fields[next]
	s.lineInputs[s.lineFocused].Focus()
}

// addLine validates the line-entry inputs, stages the line in the cart, and
// returns to the source chooser so another line can be added. It does NOT POST
// — the whole cart is submitted from the review phase via finalize.
func (s *PurchaseOrderCreateScreen) addLine() tea.Cmd {
	desc := strings.TrimSpace(s.lineInputs[poLineFieldDesc].Value())
	// Item/asset-backed lines don't strictly need a description
	// (backend will fall back to item/asset name), but when the line
	// came from freeform we require one.
	if desc == "" && s.pickedItemSup == nil && s.pickedAssetID == nil {
		s.errMsg = "item description is required"
		return Status(s.errMsg, StatusError)
	}
	qty, err := strconv.Atoi(strings.TrimSpace(s.lineInputs[poLineFieldQty].Value()))
	if err != nil || qty <= 0 {
		s.errMsg = "quantity must be a positive integer"
		return Status(s.errMsg, StatusError)
	}

	line := omsapi.PurchaseOrderCreateItem{
		Description:    desc,
		Quantity:       qty,
		ItemSupplierID: s.pickedItemSup,
		AssetID:        s.pickedAssetID,
	}
	// Cost entry, read on every line kind. On a case-packed inventory line the
	// field holds a per-case OR per-unit cost per the ctrl+t basis toggle, and
	// poDeriveUnitCost divides a case cost by qpp at full precision (no cent
	// rounding, so odd case sizes don't drift). A blank field omits unit_cost
	// altogether: for a catalog line that hands pricing back to the stored
	// item-supplier cost (sc-5yr's default), and for an asset / freeform line
	// the backend rejects the missing cost, which is the pre-existing contract.
	unit, provided, err := poDeriveUnitCost(
		s.lineInputs[poLineFieldCost].Value(), s.costBasisCase, s.pickedQPP,
	)
	if err != nil {
		label := "unit cost"
		if s.costBasisCase {
			label = "case cost"
		}
		s.errMsg = label + " must be a non-negative number"
		return Status(s.errMsg, StatusError)
	}
	if provided {
		line.UnitCost = &unit
	}
	// Per-line expected shipment date. Blank is allowed (omitempty drops it) —
	// the date is optional on the backend. Only read when the field is active,
	// so a value typed on an inventory line can't leak onto an asset/freeform
	// line whose create branch would ignore it anyway.
	if s.lineTakesDate() {
		raw := strings.TrimSpace(s.lineInputs[poLineFieldDate].Value())
		if raw != "" {
			if _, err := time.Parse("2006-01-02", raw); err != nil {
				s.errMsg = "expected date must be YYYY-MM-DD (or blank)"
				return Status(s.errMsg, StatusError)
			}
			line.ExpectedShipmentDate = raw
		}
	}

	cartLine := poCartLine{item: line, label: s.lineLabel(desc), qpp: s.pickedQPP}
	s.errMsg = ""

	if s.editIndex >= 0 && s.editIndex < len(s.lines) {
		// In-place edit: overwrite the line (not append) and return to whichever
		// phase opened the edit, with the same line still highlighted.
		idx := s.editIndex
		s.lines[idx] = cartLine
		s.reviewCursor = idx
		s.pickedItemSup = nil
		s.pickedAssetID = nil
		s.pickedQPP = 0
		s.costBasisCase = false
		s.editIndex = -1
		return tea.Batch(Status("line updated", StatusOK), s.returnFromLineForm())
	}

	s.lines = append(s.lines, cartLine)
	// Park the shared cart cursor on the line just added, so the source
	// chooser's x still removes it and d opens review on it.
	s.reviewCursor = len(s.lines) - 1
	// Back to the source chooser to add another line (or press d to submit).
	s.phase = poPhaseSource
	s.pickedItemSup = nil
	s.pickedAssetID = nil
	s.pickedQPP = 0
	s.costBasisCase = false
	s.editIndex = -1
	return Status(fmt.Sprintf("line added (%d in cart)", len(s.lines)), StatusOK)
}

// lineLabel builds the cart display label from the entered description and the
// line source, so a freeform line and a picked item read sensibly in the list.
func (s *PurchaseOrderCreateScreen) lineLabel(desc string) string {
	switch {
	case desc != "":
		return desc
	case s.pickedItemSup != nil:
		return fmt.Sprintf("item-supplier #%d", *s.pickedItemSup)
	case s.pickedAssetID != nil:
		return fmt.Sprintf("asset %s", *s.pickedAssetID)
	default:
		return "line"
	}
}

// ---------------------------------------------------------------------------
// Phase 4 cost helpers (per-unit / per-case entry — op-7j8v parity)
// ---------------------------------------------------------------------------

// errInvalidCost is returned by poDeriveUnitCost for a non-numeric or negative
// cost entry.
var errInvalidCost = errors.New("cost must be a non-negative number")

// poDeriveUnitCost converts a raw cost-field entry into the per-item unit_cost
// carried by the payload. When basisCase is true the raw value is a per-CASE
// cost and is divided by qpp (full precision — no cent rounding, so odd case
// sizes such as $10 / 3 don't drift); otherwise it is already a per-unit cost.
// provided is false (with a nil error) when the field is blank, signalling the
// caller to omit unit_cost so the backend derives it from the stored catalog
// cost. A non-numeric or negative entry returns errInvalidCost.
func poDeriveUnitCost(raw string, basisCase bool, qpp int) (unit float64, provided bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	v, perr := strconv.ParseFloat(raw, 64)
	if perr != nil || v < 0 {
		return 0, false, errInvalidCost
	}
	if basisCase {
		if qpp < 1 {
			qpp = 1
		}
		return v / float64(qpp), true, nil
	}
	return v, true, nil
}

// poFormatCost renders a cost at full precision with no trailing-zero noise
// (e.g. 12.5, 3, 4.285714285714286) — the value round-trips into the payload
// unrounded. Matches the -1 precision idiom used for cart display.
func poFormatCost(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// poDisplayMoney formats a cost for human-facing hints only (2–4 decimals,
// trailing zeros trimmed but at least two kept): 3.00, 1.25, 4.2857. Display
// rounding never touches the full-precision value submitted to the backend.
func poDisplayMoney(v float64) string {
	str := strconv.FormatFloat(v, 'f', 4, 64)
	if strings.Contains(str, ".") {
		str = strings.TrimRight(str, "0")
		dot := strings.IndexByte(str, '.')
		for len(str)-dot-1 < 2 {
			str += "0"
		}
	}
	return str
}

// toggleCostBasis flips the Phase-4 cost field between per-unit and per-case
// entry for a case-packed inventory line (a no-op for any other line). The
// current value is converted so the economics are preserved (case = unit ×
// qpp) at full precision, and the placeholder is refreshed to match.
func (s *PurchaseOrderCreateScreen) toggleCostBasis() {
	if s.pickedItemSup == nil || s.pickedQPP <= 1 {
		return
	}
	raw := strings.TrimSpace(s.lineInputs[poLineFieldCost].Value())
	if v, err := strconv.ParseFloat(raw, 64); err == nil && raw != "" {
		if s.costBasisCase {
			v /= float64(s.pickedQPP) // case → unit
		} else {
			v *= float64(s.pickedQPP) // unit → case
		}
		s.lineInputs[poLineFieldCost].SetValue(poFormatCost(v))
	}
	s.costBasisCase = !s.costBasisCase
	s.applyCostPlaceholder()
}

// applyCostPlaceholder keeps the cost input's placeholder in step with the
// active basis (case vs unit) and with what the field means for this line kind:
// a catalog line's cost is an override the operator may leave empty, so the
// empty field reads "(optional)" instead of prompting for a value it doesn't
// need. What blank actually does is spelled out by the note renderLinePhase
// prints under the form.
func (s *PurchaseOrderCreateScreen) applyCostPlaceholder() {
	basis, hint := "unit cost", "(e.g. 12.50)"
	if s.costBasisCase {
		basis, hint = "case cost", "(e.g. 30.00)"
	}
	if s.pickedItemSup != nil {
		hint = "(optional)"
	}
	s.lineInputs[poLineFieldCost].Placeholder = basis + " " + hint
}

// costDerivationHint is the muted line shown under a case-packed inventory
// line's cost field: it echoes the entered cost in both bases (case ÷ qpp =
// unit) so the operator always sees the derived counterpart, plus the toggle
// key. Returns "" for any non-case line.
func (s *PurchaseOrderCreateScreen) costDerivationHint() string {
	if s.pickedItemSup == nil || s.pickedQPP <= 1 {
		return ""
	}
	qpp := s.pickedQPP
	raw := strings.TrimSpace(s.lineInputs[poLineFieldCost].Value())
	v, err := strconv.ParseFloat(raw, 64)
	if raw == "" || err != nil {
		if s.costBasisCase {
			return fmt.Sprintf("Enter the CASE cost — divided by %d units/case for the unit cost. ctrl+t: switch to unit-cost entry.", qpp)
		}
		return fmt.Sprintf("Enter the UNIT cost — multiplied by %d for the case cost. ctrl+t: switch to case-cost entry.", qpp)
	}
	unit, cas := v, v*float64(qpp)
	if s.costBasisCase {
		unit, cas = v/float64(qpp), v
	}
	return fmt.Sprintf("$%s/case ÷ %d = $%s/unit  ·  ctrl+t: toggle unit/case basis",
		poDisplayMoney(cas), qpp, poDisplayMoney(unit))
}

// finalize POSTs the whole cart as one PurchaseOrderCreate. Called from the
// review phase; the PO-level notes come from the review notes input.
func (s *PurchaseOrderCreateScreen) finalize() tea.Cmd {
	if s.supplierID <= 0 {
		s.errMsg = "supplier is required (return to supplier phase)"
		return Status(s.errMsg, StatusError)
	}
	if len(s.lines) == 0 {
		s.errMsg = "add at least one line before submitting"
		return Status(s.errMsg, StatusError)
	}

	items := make([]omsapi.PurchaseOrderCreateItem, len(s.lines))
	for i, l := range s.lines {
		items[i] = l.item
	}
	req := omsapi.PurchaseOrderCreate{
		Supplier: s.supplierID,
		Notes:    strings.TrimSpace(s.poNotes.Value()),
		Items:    items,
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

// ---------------------------------------------------------------------------
// Phase: Review cart + submit
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateReviewPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		// Back to the source chooser to add or remove lines. Keep the cart
		// and any notes already typed. Only esc (not a letter) goes back so
		// every character still reaches the focused notes field.
		s.phase = poPhaseSource
		s.poNotes.Blur()
		return s, nil
	case "enter":
		if s.pending {
			return s, nil
		}
		return s, s.finalize()
	case "up", "ctrl+p":
		if s.reviewCursor > 0 {
			s.reviewCursor--
		}
		return s, nil
	case "down", "ctrl+n":
		if s.reviewCursor < len(s.lines)-1 {
			s.reviewCursor++
		}
		return s, nil
	case "ctrl+x":
		// Remove the highlighted line. ctrl+x (not plain x) so the key
		// doesn't collide with typing 'x' into the notes field.
		s.removeLineAt(s.reviewCursor)
		return s, nil
	case "ctrl+e":
		// Edit the highlighted line in place. ctrl+e (not a bare letter, which
		// types into the focused notes field, nor enter, which submits) so the
		// chord never collides. openLineEditor re-opens the Phase-4 form
		// pre-filled and flags editIndex so addLine writes the change back to
		// s.lines[reviewCursor] instead of appending.
		return s, s.openLineEditor(s.reviewCursor, poPhaseReview)
	}
	var cmd tea.Cmd
	s.poNotes, cmd = s.poNotes.Update(m)
	return s, cmd
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()))
	b.WriteString("\n\n")

	// Always show the committed supplier (if any) as a header so the
	// operator never loses context on which supplier the line will be
	// billed to.
	b.WriteString(s.renderSupplierHeader())
	b.WriteString("\n")

	switch s.phase {
	case poPhaseSupplier:
		b.WriteString(s.renderSupplierPhase())
	case poPhaseSource:
		b.WriteString(s.renderSourcePhase())
	case poPhaseReorderPick:
		b.WriteString(s.renderReorderPick())
	case poPhaseItemPick:
		b.WriteString(s.renderItemPick())
	case poPhaseAssetPick:
		b.WriteString(s.renderAssetPick())
	case poPhaseLine:
		b.WriteString(s.renderLinePhase())
	case poPhaseReview:
		b.WriteString(s.renderReviewPhase())
	}

	b.WriteString("\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *PurchaseOrderCreateScreen) helpText() string {
	switch s.phase {
	case poPhaseSupplier:
		return "Pick a supplier (j/k move, enter to commit, esc to cancel)."
	case poPhaseSource:
		base := "Add a line: r reorder queue · i inventory items · a assets · f freeform · b back · esc cancel."
		if len(s.lines) > 0 {
			base = fmt.Sprintf(
				"Add a line (r/i/a/f) · j/k highlight · ctrl+e edit · x remove · d done → review %d line(s) · b back · esc cancel.",
				len(s.lines),
			)
		}
		return base
	case poPhaseReorderPick:
		return "Reorder-queue suggestions (j/k move · space mark · a add ALL · enter add marked/highlighted · b back · esc cancel)."
	case poPhaseItemPick:
		return "Inventory items for this supplier (j/k move, / filter, enter pick, b back, esc cancel)."
	case poPhaseAssetPick:
		return "Assets purchased from this supplier (j/k move, / search, ] next page, [ prev page, enter pick, b back, esc cancel)."
	case poPhaseLine:
		if s.editIndex >= 0 {
			// Editing an existing cart line (opened with ctrl+e from review).
			if s.pickedItemSup != nil && s.pickedQPP > 1 {
				return "Editing line (tab/shift-tab cycle fields · ctrl+t unit/case cost basis · enter save changes · esc cancel edit)."
			}
			return "Editing line (tab/shift-tab cycle fields, enter to save changes, esc to cancel edit)."
		}
		if s.pickedItemSup != nil && s.pickedQPP > 1 {
			return "Line entry (tab/shift-tab cycle fields · ctrl+t unit/case cost basis · enter add · esc different source)."
		}
		return "Line entry (tab/shift-tab cycle fields, enter to add to cart, esc to pick a different source)."
	case poPhaseReview:
		return "Review cart — type PO notes · ↑↓ highlight a line · ctrl+e edit it · ctrl+x remove it · enter submit · esc back."
	}
	return ""
}

func (s *PurchaseOrderCreateScreen) renderSupplierHeader() string {
	switch {
	case s.supplierLoading:
		return StyleTitle.Render("Supplier:") + " " + StyleMuted.Render("loading suppliers…")
	case s.supplierLoadErr != "":
		return StyleTitle.Render("Supplier:") + " " + StyleStatusError.Render("✗ "+s.supplierLoadErr)
	case s.supplierID > 0:
		name := ""
		for _, sup := range s.suppliers {
			if sup.ID == s.supplierID {
				name = sup.Name
				break
			}
		}
		return StyleTitle.Render("Supplier:") + " " + StyleStatusOK.Render(fmt.Sprintf("%s (#%d)", name, s.supplierID))
	default:
		return StyleTitle.Render("Supplier:") + " " + StyleMuted.Render("(none picked)")
	}
}

func (s *PurchaseOrderCreateScreen) renderSupplierPhase() string {
	if s.supplierLoading || s.supplierLoadErr != "" {
		return ""
	}
	if len(s.suppliers) == 0 {
		return StyleMuted.Render("(no suppliers configured)")
	}
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
	var b strings.Builder
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above\n", start)))
	}
	for i := start; i < end; i++ {
		sup := s.suppliers[i]
		caret := "    "
		if i == s.supplierCursor {
			caret = "  ▸ "
		}
		line := fmt.Sprintf("%s%s  (#%d)", caret, sup.Name, sup.ID)
		if i == s.supplierCursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.suppliers) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below\n", len(s.suppliers)-end)))
	}
	return b.String()
}

func (s *PurchaseOrderCreateScreen) renderSourcePhase() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Where should this line come from?") + "\n\n")
	b.WriteString("  " + StyleStatusOK.Render("r") + "  Reorder queue (items flagged for reorder)\n")
	b.WriteString("  " + StyleStatusOK.Render("i") + "  Inventory items associated with this supplier\n")
	b.WriteString("  " + StyleStatusOK.Render("a") + "  Assets purchased from this supplier\n")
	b.WriteString("  " + StyleStatusOK.Render("f") + "  Freeform line (no item / asset reference)\n")
	if len(s.lines) > 0 {
		b.WriteString("\n")
		// Highlight the same line the review cart would: j/k aim it, and
		// ctrl+e / x act on it.
		b.WriteString(s.renderCart(s.reviewCursor))
		b.WriteString("\n  " + StyleStatusOK.Render("d") + "  Done — review & submit    " +
			StyleMuted.Render("(j/k highlight a line · ctrl+e edit it · x remove it)") + "\n")
	}
	return b.String()
}

// poCartLineType derives the wire item_type token a staged line will carry once
// the backend saves it. The create payload names its target with a field rather
// than a type token, so this mirrors the same field dispatch
// create_purchase_order uses server-side — and feeds poLineTypeLabel, so the
// cart and the saved PO's detail screen label a line identically.
func poCartLineType(l poCartLine) string {
	switch {
	case l.item.ItemSupplierID != nil:
		return "item_supplier"
	case l.item.AssetID != nil:
		return "asset"
	}
	return "freeform"
}

// poCartTotal sums the staged cart's extended cost — quantity × unit_cost per
// line. Quantity is always in UNITS and unit_cost is always per-unit (a
// case-packed line's case cost is divided down at add time), so the case
// quantity-per-package never enters the sum.
//
// A line with no cost contributes 0 rather than dropping the whole total: a
// blank cost on a catalog line means "price it from item_supplier.unit_cost at
// save time", so the sum is a floor, not the PO's final value. noCost counts
// those lines so the caller can say the total is incomplete instead of letting
// them read as free. A cost that is explicitly 0 is an explicit $0 — same
// nil-versus-typed-zero distinction the backend makes — and is not counted.
func poCartTotal(lines []poCartLine) (total float64, noCost int) {
	for _, l := range lines {
		if l.item.UnitCost == nil {
			noCost++
			continue
		}
		total += float64(l.item.Quantity) * *l.item.UnitCost
	}
	return total, noCost
}

// renderCart lists the staged lines and the running total. When highlight >= 0
// the matching row is marked (used by the review phase and the source chooser's
// cart list); pass -1 for a plain list. The total lives here rather than in
// renderReviewPhase so both surfaces that show the cart also show what it costs.
func (s *PurchaseOrderCreateScreen) renderCart(highlight int) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Cart (%d line(s))", len(s.lines))) + "\n")
	for i, l := range s.lines {
		caret := "    "
		if i == highlight {
			caret = "  ▸ "
		}
		row := fmt.Sprintf("%s%d) %s  ×%d", caret, i+1, l.label, l.item.Quantity)
		if l.item.UnitCost != nil {
			row += fmt.Sprintf(" @ $%s", strconv.FormatFloat(*l.item.UnitCost, 'f', -1, 64))
		}
		if l.item.ExpectedShipmentDate != "" {
			row += "  exp " + l.item.ExpectedShipmentDate
		}
		// Target type as a trailing badge: plain text inside the row so the
		// whole-row highlight style still applies cleanly.
		row += "  [" + poLineTypeLabel(poCartLineType(l)) + "]"
		if i == highlight {
			row = StyleSidebarItemActive.Render(row)
		}
		b.WriteString(row + "\n")
	}
	if len(s.lines) > 0 {
		total, noCost := poCartTotal(s.lines)
		noun := "line items"
		if len(s.lines) == 1 {
			noun = "line item"
		}
		b.WriteString(StyleTitle.Render("  Total: "+fmtMoney(total)) +
			StyleMuted.Render(fmt.Sprintf("  (%d %s)", len(s.lines), noun)) + "\n")
		if noCost > 0 {
			subject := fmt.Sprintf("%d lines are", noCost)
			if noCost == 1 {
				subject = "1 line is"
			}
			b.WriteString(StyleMuted.Render(
				"    "+subject+" priced from the supplier catalog — not included above") + "\n")
		}
	}
	return b.String()
}

func (s *PurchaseOrderCreateScreen) renderReviewPhase() string {
	var b strings.Builder
	b.WriteString(s.renderCart(s.reviewCursor))
	b.WriteString("\n")
	b.WriteString("▸ " + StyleTitle.Render("PO notes: ") + s.poNotes.View() + "\n")
	return b.String()
}

func (s *PurchaseOrderCreateScreen) renderLinePhase() string {
	var b strings.Builder
	source := "freeform"
	if s.pickedItemSup != nil {
		source = fmt.Sprintf("item-supplier #%d", *s.pickedItemSup)
	}
	if s.pickedAssetID != nil {
		source = fmt.Sprintf("asset %s", *s.pickedAssetID)
	}
	if s.editIndex >= 0 {
		// Editing an existing cart line — make it unmistakable this modifies the
		// highlighted line rather than adding a new one.
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Editing line %d of %d", s.editIndex+1, len(s.lines))) + "\n")
	}
	b.WriteString(StyleMuted.Render("Line source: "+source) + "\n\n")
	for _, i := range s.lineFields() {
		marker := "  "
		if i == s.lineFocused {
			marker = "▸ "
		}
		label := poLineFieldLabel(i)
		if i == poLineFieldCost {
			label = s.costFieldLabel()
		}
		b.WriteString(marker)
		b.WriteString(StyleTitle.Render(label + ": "))
		b.WriteString(s.lineInputs[i].View())
		b.WriteString("\n")
		// Case-packed inventory line: echo the derived counterpart (case ÷ qpp
		// = unit) and the basis-toggle key directly under the cost row.
		if i == poLineFieldCost {
			if hint := s.costDerivationHint(); hint != "" {
				b.WriteString(StyleMuted.Render("    "+hint) + "\n")
			}
		}
	}
	// Catalog lines: the cost is an optional override, so say what a blank field
	// does. (Case-packed lines also show the unit/case derivation hint above.)
	if s.pickedItemSup != nil {
		b.WriteString(StyleMuted.Render("  Cost is optional — blank uses the supplier catalog price.") + "\n")
	}
	// Asset / freeform lines carry no date field — say why, so its absence
	// doesn't read as an oversight.
	if !s.lineTakesDate() {
		b.WriteString(StyleMuted.Render("  Expected dates are stored on inventory lines only; set this line's dates at send/receive.") + "\n")
	}
	return b.String()
}
