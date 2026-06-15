// PurchaseOrderCreateScreen — multi-phase create-PO form for scantty.
//
// scantty's primary UX is scanner-driven, but the gap "I'm at the
// workstation and want to open a PO without context-switching to the
// web UI" came up often enough to warrant a focused form. The flow is
// now a small state machine:
//
//   poPhaseSupplier   — j/k pick a supplier from the loaded list
//                       (PR #38 picker; enter commits and advances)
//   poPhaseSource     — choose where the next line comes from:
//                         r → items the supplier has on the reorder queue
//                         i → other inventory items associated with the supplier
//                         a → assets purchased from the supplier
//                         f → a freeform line (no item/asset reference)
//   poPhaseReorderPick / ItemPick / AssetPick — list pickers backed by
//                       the corresponding omsapi endpoints; enter
//                       prefills the line buffer and jumps to poPhaseLine.
//   poPhaseLine       — description/qty/cost/notes inputs (pre-filled
//                       when the line came from a picker), enter
//                       submits via PurchaseOrderCreate.
//
// One line per PO in this iteration; multi-line is a follow-on bead.
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
)

// Field indexes inside the line-entry form (Phase 4).
const (
	poLineFieldDesc = iota
	poLineFieldQty
	poLineFieldCost
	poLineFieldNotes
	poLineFieldCount
)

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

	// Phase 3a: reorder-queue items for this supplier.
	reorderItems   []omsapi.ReorderDataItem
	reorderLoading bool
	reorderLoadErr string
	reorderCursor  int

	// Phase 3b: inventory items for this supplier.
	itemSuppliers      []omsapi.ItemSupplier
	itemSuppliersAll   []omsapi.ItemSupplier // unfiltered page so '/' search is client-side
	itemSuppliersLoad  bool
	itemSuppliersErr   string
	itemSuppliersCur   int
	itemSuppliersSearch textinput.Model
	itemSuppliersTyping bool

	// Phase 3c: assets-from-supplier picker (server-side search).
	assets         []omsapi.Asset
	assetsLoading  bool
	assetsErr      string
	assetsCursor   int
	assetsSearch   textinput.Model
	assetsTyping   bool
	assetsPage     int
	assetsHasNext  bool

	// Phase 4: line-entry form. The pointer fields drive which
	// PurchaseOrderCreateItem shape we build at submit time —
	// item_supplier_id / asset_id / freeform description.
	lineInputs    []textinput.Model
	lineFocused   int
	pickedItemSup *int    // set when the line came from Phase 3a/3b
	pickedAssetID *string // set when the line came from Phase 3c
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
	cost.Placeholder = "unit cost (optional, e.g. 12.50)"
	cost.CharLimit = 20
	s.lineInputs[poLineFieldCost] = cost

	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "notes (optional)"
	notes.CharLimit = 500
	s.lineInputs[poLineFieldNotes] = notes

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
		}
	}

	// Forward unhandled msgs to whichever textinput owns input now.
	switch s.phase {
	case poPhaseLine:
		var cmd tea.Cmd
		s.lineInputs[s.lineFocused], cmd = s.lineInputs[s.lineFocused].Update(msg)
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
		// pre-filled. Existing single-line MVP behavior.
		s.enterLinePhase(nil, nil, "", 0, 0)
		return s, textinput.Blink
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Phase: Line entry + submit
// ---------------------------------------------------------------------------

// enterLinePhase moves the screen into Phase 4 and pre-fills the inputs.
// Either both pointers are nil (freeform), or exactly one is set.
func (s *PurchaseOrderCreateScreen) enterLinePhase(
	itemSupplierID *int, assetID *string, desc string, qty int, unitCost float64,
) {
	s.phase = poPhaseLine
	s.lineFocused = poLineFieldDesc
	s.errMsg = ""
	s.pickedItemSup = itemSupplierID
	s.pickedAssetID = assetID

	for i := range s.lineInputs {
		s.lineInputs[i].SetValue("")
		s.lineInputs[i].Blur()
	}
	s.lineInputs[poLineFieldDesc].SetValue(desc)
	if qty > 0 {
		s.lineInputs[poLineFieldQty].SetValue(strconv.Itoa(qty))
	}
	if unitCost > 0 {
		s.lineInputs[poLineFieldCost].SetValue(strconv.FormatFloat(unitCost, 'f', -1, 64))
	}
	s.lineInputs[s.lineFocused].Focus()
}

func (s *PurchaseOrderCreateScreen) updateLinePhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		// Esc on the line goes back to the source chooser so the
		// operator can pick a different line source without losing
		// the supplier.
		s.phase = poPhaseSource
		s.pickedItemSup = nil
		s.pickedAssetID = nil
		return s, nil
	case "tab", "down":
		s.focusNextLine(+1)
		return s, nil
	case "shift+tab", "up":
		s.focusNextLine(-1)
		return s, nil
	case "enter":
		if s.pending {
			return s, nil
		}
		return s, s.submit()
	}
	var cmd tea.Cmd
	s.lineInputs[s.lineFocused], cmd = s.lineInputs[s.lineFocused].Update(m)
	return s, cmd
}

func (s *PurchaseOrderCreateScreen) focusNextLine(delta int) {
	s.lineInputs[s.lineFocused].Blur()
	s.lineFocused = (s.lineFocused + delta + poLineFieldCount) % poLineFieldCount
	s.lineInputs[s.lineFocused].Focus()
}

func (s *PurchaseOrderCreateScreen) submit() tea.Cmd {
	if s.supplierID <= 0 {
		s.errMsg = "supplier is required (return to supplier phase)"
		return Status(s.errMsg, StatusError)
	}
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
	costRaw := strings.TrimSpace(s.lineInputs[poLineFieldCost].Value())
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
		Notes:    strings.TrimSpace(s.lineInputs[poLineFieldNotes].Value()),
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
		return "Add a line: r reorder queue · i inventory items · a assets · f freeform · b back · esc cancel."
	case poPhaseReorderPick:
		return "Reorder-queue suggestions for this supplier (j/k move, enter pick, b back, esc cancel)."
	case poPhaseItemPick:
		return "Inventory items for this supplier (j/k move, / filter, enter pick, b back, esc cancel)."
	case poPhaseAssetPick:
		return "Assets purchased from this supplier (j/k move, / search, ] next page, [ prev page, enter pick, b back, esc cancel)."
	case poPhaseLine:
		return "Line entry (tab/shift-tab cycle fields, enter to submit, esc to pick a different source)."
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
	b.WriteString(StyleMuted.Render("Line source: "+source) + "\n\n")
	labels := []string{"Description", "Quantity", "Unit cost", "Notes"}
	for i := 0; i < poLineFieldCount; i++ {
		marker := "  "
		if i == s.lineFocused {
			marker = "▸ "
		}
		b.WriteString(marker)
		b.WriteString(StyleTitle.Render(labels[i] + ": "))
		b.WriteString(s.lineInputs[i].View())
		b.WriteString("\n")
	}
	return b.String()
}
