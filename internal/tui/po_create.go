// PurchaseOrderCreateScreen — multi-phase create-PO form for scantty.
//
// scantty's primary UX is scanner-driven, but the gap "I'm at the
// workstation and want to open a PO without context-switching to the
// web UI" came up often enough to warrant a focused form. The flow is
// now a small state machine:
//
//	poPhaseSupplier   — j/k pick a supplier from the loaded list
//	                    (PR #38 picker; enter commits and advances)
//	poPhaseAgreement  — optional: which purchase/pricing agreement the order is
//	                    placed under (op-yoos). Committing a supplier loads its
//	                    ACTIVE agreements in the background and the source
//	                    chooser grows a g row when there are any — the picker is
//	                    never forced on the many orders that cite none, matching
//	                    the web form, which renders its select only when the
//	                    supplier has agreements on file.
//	poPhaseWorkOrder  — optional: which work order this order is being bought
//	poPhaseCommittee    for, and which committee (SIG) it is placed on behalf of
//	                    (op-shb9). Same shape as the agreement picker and for the
//	                    same reason — both are attribution only, so neither may
//	                    cost the common path a phase to dismiss. The options load
//	                    in the background when the screen opens (they are not
//	                    supplier-scoped) and the source chooser grows a w / c row
//	                    once there is something to pick. Per-LINE associations are
//	                    set from the PO detail screen once the order exists, which
//	                    is where the web puts them too — which job a part turned
//	                    out to be for is usually settled after it arrives.
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
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Phase enum. The screen tracks which surface owns input right now
// so the same .View()/.Update() can render either the supplier
// picker, the source-chooser menu, one of the three pickers, or the
// final line-entry form.
type poPhase int

const (
	poPhaseSupplier poPhase = iota
	poPhaseAgreement
	poPhaseWorkOrder
	poPhaseCommittee
	poPhaseSource
	poPhaseReorderPick
	poPhaseItemPick
	poPhaseAssetPick
	poPhaseLine
	poPhaseReview
	// poPhaseSupplierSwitch is the confirm the supplier picker raises when
	// committing a DIFFERENT supplier would invalidate lines already in the
	// cart. Appended rather than slotted in beside poPhaseSupplier so the
	// existing constants keep their values.
	poPhaseSupplierSwitch

	// poPhaseCount is the sentinel the sweeps walk to. It exists so a phase
	// added above it is covered by construction rather than by somebody
	// remembering to extend a table: the key sweep iterates 0..poPhaseCount-1
	// and fails on any phase it has no entry for. Three rounds of this project
	// lost a key to a hand-maintained member list — poAllBarKeys had none of
	// the list letters, poPickerVocabulary had no tab — and a list that must be
	// edited in step with an enum is the same shape of omission waiting again.
	poPhaseCount
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

	// The failure line View draws last: errMsg is the sentence the operator
	// reads first, errDetail the unbounded half underneath it. They are two
	// fields because omsapi.parseError puts the ENTIRE raw response body in
	// APIError.Message whenever the JSON envelope carries no code, so a 502
	// from a gateway or a Django debug page arrives here multi-KB — and the
	// one line that used to carry it was neither folded nor budgeted, so at
	// submit the operator read "✗ oms: http 502: <!DOCTYPE html><htm" and
	// nothing else. Split, the headline is what pickerFail keeps and the body
	// is what it sacrifices. Always set through setErr, never separately.
	errMsg    string
	errDetail string

	// Phase 1: supplier picker (unchanged from PR #38).
	suppliers       []omsapi.Supplier
	supplierLoading bool
	supplierLoadErr string
	supplierCursor  int
	supplierID      int
	supplierNote    pickerNote

	// Phase 1b: the supplier's ACTIVE purchase/pricing agreements (op-yoos).
	// Loaded in the background the moment a supplier is committed, so the
	// source chooser is reachable without waiting on a request that most
	// orders don't need. agreementID is the operator's optional pick — nil
	// means "no agreement", which is what the vast majority of POs cite.
	// A failed load never blocks the order: agreementLoadErr is surfaced as a
	// muted note (so a missing list can't be misread as "this supplier has
	// none") and g retries it.
	agreements       []omsapi.SupplierAgreement
	agreementLoading bool
	agreementLoadErr string
	agreementCursor  int
	agreementID      *int

	// Phase 1c: the order-level work-order / committee associations (op-shb9).
	// Neither list is supplier-scoped, so they load once when the screen opens
	// rather than on every supplier change, and neither is ever required — the
	// two ids below stay empty on the many orders that are simply stock.
	// workOrderID is a WorkOrder UUID; committeeID is an auth.Group pk as text,
	// so both pickers share one implementation (see po_associations.go).
	assoc           poAssocOptions
	workOrderCursor int
	workOrderID     string
	committeeCursor int
	committeeID     string

	// Phase 3a: reorder-queue items for this supplier. reorderSelected marks
	// rows toggled with space for a bulk add (keyed by index into
	// reorderItems); it is cleared whenever the list reloads so a stale index
	// can never select the wrong row.
	reorderItems    []omsapi.ReorderDataItem
	reorderLoading  bool
	reorderLoadErr  string
	reorderCursor   int
	reorderSelected map[int]bool
	reorderNote     pickerNote

	// Phase 3b: inventory items for this supplier.
	itemSuppliers    []omsapi.ItemSupplier
	itemSuppliersAll []omsapi.ItemSupplier // whole catalog, so '/' search is client-side
	// itemSuppliersFor is the supplier itemSuppliersAll was loaded for, or 0
	// when nothing usable is held. The catalog is now paged in whole — one
	// request per page — and the picker is re-entered once per line, so
	// re-walking it on every entry is dozens of round trips for an answer that
	// cannot have changed. It is keyed rather than just cached because a
	// catalog that outlived its supplier is the wrong catalog, not a stale one.
	itemSuppliersFor    int
	itemSuppliersLoad   bool
	itemSuppliersErr    string
	itemSuppliersCur    int
	itemSuppliersSearch textinput.Model
	itemSuppliersTyping bool
	// itemSuppliersNote is the picker's OWN answer to the last keypress: what
	// the search matched, or why nothing was picked. It lives on the screen
	// rather than only in a Status flash because a flash expires after four
	// seconds and the operator who pressed enter and saw nothing is precisely
	// the operator who is still staring at the picker. See pickerNote.
	itemSuppliersNote pickerNote

	// Phase 3c: assets-from-supplier picker (server-side search).
	assets        []omsapi.Asset
	assetsLoading bool
	// assetsSeq is the generation of the asset lookup that is allowed to land.
	// assetsLoading alone cannot carry that: it is the flag every arm tests to
	// refuse a SECOND lookup, and resetSupplierScopedPickers clears it while a
	// request is still out, so an A -> B -> A round trip through the supplier
	// picker put two lookups for the SAME supplier on the wire again. The reply
	// is identified by supplierID, both matched, and whichever landed last won
	// — a filtered subset painted as the supplier's whole asset list, with a
	// green tick and an empty search box. Only the asset reply varies by query
	// and page, which is why it is the one that needs a generation and the
	// other two pickers do not. Same shape as ListScreen.searchSeq (list.go).
	assetsSeq     int
	assetsErr     string
	assetsCursor  int
	assetsSearch  textinput.Model
	assetsTyping  bool
	assetsPage    int
	assetsHasNext bool
	assetsNote    pickerNote
	// assetsQuery is the search the last asset load actually CARRIED, which is
	// not the same thing as what the search box holds. This search is
	// SERVER-side and runs only on enter, so a box the operator typed into and
	// escaped without committing left the rows answering the PREVIOUS query
	// while every note read the live textinput — "no asset matches
	// \"hovercraft\"" against a supplier that simply has no assets, which is
	// found-nothing stated where could-not-tell is the fact. The pager read it
	// too, so ']' after an uncommitted esc quietly applied a query nobody
	// submitted to page 2.
	assetsQuery string

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
	// cartLead is what the last declined key DID on a source chooser whose cart
	// is collapsed. Only the lead is kept, never the finished sentence: the
	// count and the total are re-derived on every render, so a line added from
	// the line form cannot leave a stale figure sitting on the pane. A decline
	// that only flashes on the status bar is gone in four seconds with the pane
	// unchanged, which is the silence this screen exists to remove.
	cartLead string
	// attrLead is the same thing for the optional g / w / c rows: what the last
	// declined one of those keys DID, on a pane too short to draw the rows they
	// name. Only the lead is kept — the sentence is rebuilt on every render.
	attrLead string
	// switchLead is the same shape for the supplier-switch confirm, which binds
	// exactly two keys and had NOTHING to say to any other press. enter is the
	// one that matters: it is the key that opened this frame, so a reflexive
	// double-tap landed on a pure function of unchanged state and redrew a
	// byte-for-byte identical pane — the reported hang, on a destructive
	// confirm. The answer is a note, not a binding: see updateSupplierSwitchPhase.
	switchLead string
	// pendingLead is what a key that the submit has made inert just did. The
	// review bar drops `enter submit` while the POST is out, and the press
	// itself answers on the "Submitting…" line rather than into a flash the
	// operator watching a slow request will have missed.
	pendingLead string

	// editIndex is the s.lines index being edited in place (ctrl+e re-opens the
	// Phase-4 form pre-filled), or -1 when the line form is adding a new line.
	// enterLinePhase resets it to -1 on every entry and the edit path re-sets it
	// afterward, so a stale index can never turn a later add into an in-place
	// replace. editReturn is the phase the edit was launched from (review cart or
	// source chooser) — saving or cancelling goes back there rather than always
	// dumping the operator in review.
	editIndex  int
	editReturn poPhase

	// terminalHeight is the last size Root forwarded, and is what the cart
	// windows against. Zero means "not sized yet" — a screen driven straight
	// in a unit test — and windows nothing, because guessing a pane height
	// would hide rows nobody asked to hide.
	terminalHeight int
}

type poCreatedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

type poCreateSuppliersLoadedMsg struct {
	suppliers []omsapi.Supplier
	err       error
}

// poAgreementsLoadedMsg carries one supplier's active purchase/pricing
// agreements. supplierID is echoed back so a slow response for a supplier the
// operator has since moved off of is dropped instead of offering agreements the
// backend would reject against the current supplier.
type poAgreementsLoadedMsg struct {
	supplierID int
	agreements []omsapi.SupplierAgreement
	err        error
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
	// The association options ride along with the supplier load: they belong to
	// no supplier, so there is nothing to wait for and nothing to refetch later.
	return tea.Batch(s.loadSuppliers(), s.assoc.load(s.deps), textinput.Blink)
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

// loadAgreements fetches the committed supplier's ACTIVE agreements. Only the
// active set is offered: retired paperwork stays on file but must not be
// citable on a new order (the backend's is_active flag exists for exactly
// that), matching the web create form.
func (s *PurchaseOrderCreateScreen) loadAgreements() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		rows, err := deps.OMS.ListSupplierAgreements(ctx, supplierID)
		return poAgreementsLoadedMsg{supplierID: supplierID, agreements: rows, err: err}
	}
}

func (s *PurchaseOrderCreateScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case poCreateSuppliersLoadedMsg:
		s.supplierLoading = false
		s.supplierNote.clear()
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
			// Headline + detail, because the detail is a whole OMS response
			// body: pickerFail folds it and trims it to the rows the pane has
			// left, so what the operator loses on a short terminal is the tail
			// of the gateway's HTML and never the sentence naming what failed.
			s.setErr("submitting this purchase order failed", m.err.Error())
			return s, Status("create PO failed: "+m.err.Error(), StatusError)
		}
		s.setErr("", "")
		poNum := ""
		if m.po != nil {
			poNum = m.po.Number
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("created %s", poNum), StatusOK),
			SwitchTo(WSPurchasing, nil),
		)

	case poAgreementsLoadedMsg:
		if m.supplierID != s.supplierID {
			// The operator moved to another supplier while this was in
			// flight — its agreements belong to a supplier that is no longer
			// the order's, and the backend would reject any of them.
			return s, nil
		}
		s.agreementLoading = false
		if m.err != nil {
			// Never blocks the order (same call the web form makes
			// non-fatal): the note is muted and g retries.
			s.agreementLoadErr = m.err.Error()
			s.agreements = nil
			return s, nil
		}
		s.agreementLoadErr = ""
		s.agreements = m.agreements
		s.agreementCursor = 0
		return s, nil

	// Picker messages live in po_create_pickers.go.
	case poReorderItemsLoadedMsg, poItemSuppliersLoadedMsg, poAssetsLoadedMsg:
		return s, s.handlePickerLoaded(msg)

	case poWorkOrdersLoadedMsg, poCommitteesLoadedMsg:
		// Association options (op-shb9). Nothing to do beyond storing them —
		// the affordances appear on the source chooser once there is something
		// to pick, and a failure only annotates the row.
		s.assoc.handle(msg)
		return s, nil

	case tea.KeyMsg:
		switch s.phase {
		case poPhaseSupplier:
			return s.updateSupplierPhase(m)
		case poPhaseSupplierSwitch:
			return s.updateSupplierSwitchPhase(m)
		case poPhaseAgreement:
			return s.updateAgreementPhase(m)
		case poPhaseWorkOrder:
			return s.updateWorkOrderPhase(m)
		case poPhaseCommittee:
			return s.updateCommitteePhase(m)
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
	if !s.supplierListOnScreen() {
		switch m.String() {
		case "j", "down", "k", "up":
			// Named, like every other cursor decline on this screen: j and k
			// share this arm, and this frame has no rows and no highlight, so
			// one sentence for both would make the second press redraw the
			// pane the first one left.
			return s, s.supplierVerdictNote(m.String() + " moves nothing")
		case "enter":
			return s, s.supplierVerdictNote("nothing to commit")
		}
	}
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
	case "enter":
		// Changing supplier under a cart that already names the old supplier's
		// catalog rows destroys work, so it asks first. Only a CHANGE, and only
		// when there is something to lose: re-committing the same supplier, or
		// committing one with an empty (or wholly supplier-agnostic) cart, is
		// the same single keypress it has always been.
		if s.supplierCursor >= 0 && s.supplierCursor < len(s.suppliers) &&
			s.suppliers[s.supplierCursor].ID != s.supplierID &&
			s.supplierScopedLineCount() > 0 {
			s.phase = poPhaseSupplierSwitch
			s.switchLead = ""
			return s, Status(fmt.Sprintf(
				"%d staged line(s) belong to %s — ctrl+x drops them and switches, esc keeps them",
				s.supplierScopedLineCount(), s.supplierLabel()), StatusWarn)
		}
		cmd := s.commitSupplier()
		if s.supplierID > 0 {
			s.phase = poPhaseSource
		}
		return s, cmd
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Phase: "changing supplier will drop part of the cart"
// ---------------------------------------------------------------------------

// supplierScopedLineCount is how many staged lines only the CURRENT supplier
// can fill — the ones whose payload names an item_supplier_id.
//
// An ItemSupplier row IS the (item, supplier) pair, so its id is meaningless
// against any other supplier, and the backend accepts it without cross-checking
// it against the order's supplier: the POST succeeds and the purchase order
// quietly names another supplier's item. That is the failure
// resetSupplierScopedPickers closes inside the pickers, one step further along.
//
// Freeform lines carry only a description and a cost, so they are valid against
// anybody. ASSET lines are kept for the same reason, and that is a reading of
// the wire contract rather than a guess: PurchaseOrderCreateItem documents
// asset_id as one of three line SHAPES with no supplier coupling, the same file
// calls out supplier validation explicitly where it does exist (the backend
// validates supplier_agreement against the order's supplier), and the asset
// picker scopes its list with ?manufacturer= — who BUILT the equipment, a
// property of the asset, not a sales relationship with whoever is being
// ordered from. Buying a part for a mill from a different vendor is ordinary.
// (No OpenMakerSuite checkout is reachable from a task worktree, so this is the
// contract as this repo models it; if the backend does reject such a line the
// failure is a visible 400 at submit, where dropping the lines outright would
// have destroyed valid staged work to prevent a rejection that never comes.)
func (s *PurchaseOrderCreateScreen) supplierScopedLineCount() int {
	n := 0
	for _, l := range s.lines {
		if l.item.ItemSupplierID != nil {
			n++
		}
	}
	return n
}

// dropSupplierScopedLines removes exactly those lines and returns how many went.
func (s *PurchaseOrderCreateScreen) dropSupplierScopedLines() int {
	kept := make([]poCartLine, 0, len(s.lines))
	dropped := 0
	for _, l := range s.lines {
		if l.item.ItemSupplierID != nil {
			dropped++
			continue
		}
		kept = append(kept, l)
	}
	s.lines = kept
	s.clampReviewCursor()
	return dropped
}

// updateSupplierSwitchPhase drives the confirm: ctrl+x drops and switches, esc
// keeps the cart AND the current supplier. Two keys, both named, nothing else.
//
// The affirmative is ctrl+x rather than enter for two reasons. It is the chord
// this screen already spends on "remove a staged line" (the review cart and the
// source chooser both bind it), so it means the same thing here; and enter is
// the key that OPENED this frame, so binding it to the destructive answer would
// turn one reflexive double-tap on the supplier list into a silently emptied
// cart. Letters stay out of it for the reason the attachment delete records: a
// scanner burst is a run of letters, and one landing on a confirm is the
// accident the reduced key scheme exists to rule out.
func (s *PurchaseOrderCreateScreen) updateSupplierSwitchPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "ctrl+x":
		from := s.supplierLabel()
		s.switchLead = ""
		dropped := s.dropSupplierScopedLines()
		cmd := s.commitSupplier()
		s.phase = poPhaseSupplier
		if s.supplierID > 0 {
			s.phase = poPhaseSource
		}
		return s, tea.Batch(Status(fmt.Sprintf(
			"dropped %d line(s) only %s carried · %d left in the cart",
			dropped, from, len(s.lines)), StatusWarn), cmd)
	case "esc":
		s.phase = poPhaseSupplier
		s.switchLead = ""
		return s, Status("kept the cart · still ordering from "+s.supplierLabel(), StatusInfo)
	}
	// Everything else declines and SAYS SO. enter is why this arm exists: it is
	// the key that opened the confirm, this frame has no cursor, no highlight
	// and no focused textinput, and the renderer is a pure function of state —
	// so returning nil redrew the pane byte for byte, which is the reported
	// hang landing on a destructive confirm. The lead names the key so two
	// different presses cannot answer with the same sentence.
	s.switchLead = m.String() + " does neither"
	n := s.supplierSwitchNote()
	return s, Status(n.flash(), n.level)
}

// supplierSwitchNote answers a key this frame does not bind. It reads the two
// live keys off supplierSwitchBar — the same sentence the frame draws above it —
// so a decline can never name a key the confirm does not honour.
func (s *PurchaseOrderCreateScreen) supplierSwitchNote() pickerNote {
	return pickerNote{text: s.switchLead + "\n" + s.supplierSwitchBar(), level: StatusWarn}
}

func (s *PurchaseOrderCreateScreen) renderSupplierSwitchPhase() string {
	scoped := s.supplierScopedLineCount()
	to := "the highlighted supplier"
	if s.supplierCursor >= 0 && s.supplierCursor < len(s.suppliers) {
		if name := s.suppliers[s.supplierCursor].Name; name != "" {
			to = pickerClip(name, 20)
		}
	}
	var b strings.Builder
	// Through the folder like every other note on these screens, even though
	// this one is fixed and fits: a styled literal written straight to the pane
	// is how each of the others started, and the wording is the kind that grows.
	b.WriteString(pickerNote{text: "Changing supplier drops part of the cart", level: StatusWarn}.render() + "\n\n")
	// The two KEY CLAIMS go above the prose, and carry no supplier name. This
	// is a destructive confirm and the decline is the safe answer, so the
	// decline is the one line that must never be what the pane cuts — and a
	// 20-cell supplier name folded onto the end of it was exactly what pushed
	// it off a 24-row terminal. Prose is what gets sacrificed when the rows
	// run out; the keys are not.
	b.WriteString(pickerHint(s.supplierSwitchBar()) + "\n\n")
	if s.switchLead != "" {
		// Under the keys and ABOVE the prose, so the prose is what the row
		// budget below sacrifices when the pane runs out — the same order this
		// frame already keeps between its keys and its sentence.
		b.WriteString(s.supplierSwitchNote().render() + "\n\n")
	}

	prose := fmt.Sprintf("%d of %d staged line(s) name items only %s sells, so %s cannot fill them.",
		scoped, len(s.lines), s.supplierLabel(), to)
	if kept := len(s.lines) - scoped; kept > 0 {
		prose += fmt.Sprintf(" The other %d line(s) stay.", kept)
	}
	lines := pickerWrap(prose, pickerPaneWidth)
	if budget := s.bodyRowBudget(poRenderedRows(b.String())); budget > 0 && len(lines) > budget {
		lines = lines[:budget]
	}
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(StyleMuted.Render(line))
	}
	return b.String()
}

// commitSupplier locks in the highlighted supplier and, when that changed the
// order's supplier, refreshes the agreement list behind it. Any previously
// picked agreement is dropped in the same breath: an agreement belongs to
// exactly one supplier, so carrying it across would build a payload the
// backend answers with a 400. Re-committing the SAME supplier keeps the pick
// (and skips the refetch) — the operator only stepped back to look.
//
// The load runs in the background rather than gating a phase of its own:
// most suppliers have no agreements, and making every PO wait on a request
// that usually returns nothing would tax the common path to serve the rare one.
func (s *PurchaseOrderCreateScreen) commitSupplier() tea.Cmd {
	if s.supplierCursor < 0 || s.supplierCursor >= len(s.suppliers) {
		return nil
	}
	picked := s.suppliers[s.supplierCursor].ID
	if picked == s.supplierID {
		return nil
	}
	s.supplierID = picked
	s.agreements = nil
	s.agreementID = nil
	s.agreementCursor = 0
	s.agreementLoadErr = ""
	s.agreementLoading = true
	s.resetSupplierScopedPickers()
	return s.loadAgreements()
}

// resetSupplierScopedPickers drops everything the three line-source pickers
// hold, because every row in them belongs to the supplier that was current when
// it loaded. The agreement reset above has always been done for this reason —
// an agreement belongs to one supplier and the backend rejects a foreign one —
// and the pickers are the same fact with a worse failure mode: the backend
// accepts an item_supplier id happily, so a stale catalog row staged after a
// supplier change is a purchase order that quietly names another supplier's
// item. Nothing on the screen would have flagged it.
//
// Note it also drops the in-flight flags, so a reply still out cannot strand
// the picker on a "looking up…" frame for an answer nobody is going to use.
// Clearing them is what reopened the asset race: the supplierID echo drops a
// reply only while the order has MOVED OFF that supplier, and coming back to
// the first one made it current again with its lookup still in flight and the
// guards reset. The asset generation counter is bumped here for that reason —
// anything asked for before this reset is answered too late to be believed.
func (s *PurchaseOrderCreateScreen) resetSupplierScopedPickers() {
	s.reorderItems = nil
	s.reorderLoading = false
	s.reorderLoadErr = ""
	s.reorderCursor = 0
	s.reorderNote.clear()
	s.reorderSelected = map[int]bool{}

	s.itemSuppliers = nil
	s.itemSuppliersAll = nil
	s.itemSuppliersFor = 0
	s.itemSuppliersLoad = false
	s.itemSuppliersErr = ""
	s.itemSuppliersCur = 0
	s.itemSuppliersTyping = false
	s.itemSuppliersSearch.SetValue("")
	s.itemSuppliersSearch.Blur()
	s.itemSuppliersNote.clear()

	s.assets = nil
	s.assetsLoading = false
	s.assetsSeq++
	s.assetsErr = ""
	s.assetsCursor = 0
	s.assetsTyping = false
	s.assetsSearch.SetValue("")
	s.assetsSearch.Blur()
	s.assetsQuery = ""
	s.assetsPage = 1
	s.assetsHasNext = false
	s.assetsNote.clear()
}

// ---------------------------------------------------------------------------
// Phase: Purchase / pricing agreement picker (optional, op-yoos)
// ---------------------------------------------------------------------------

// agreementRows is the picker's row count: the supplier's agreements plus a
// leading "no agreement" row. Skipping is a first-class choice here, not just
// an esc — most orders cite nothing, and an operator clearing a pick they made
// by mistake needs a way to say so.
func (s *PurchaseOrderCreateScreen) agreementRows() int { return len(s.agreements) + 1 }

// agreementOffered reports whether the source chooser shows the agreement row
// and honours g. A supplier with none on file gets neither — nothing to pick,
// so the affordance would only be a dead end, and the row appearing on every
// order would be noise on the many that cite no agreement.
//
// A FAILED load still counts: silence there would read as "this supplier has
// no agreements", which may be false. The row says so plainly and g retries.
// A load in flight offers nothing yet — the source chooser is fully usable
// while it lands, and the row appears when there is something to say.
func (s *PurchaseOrderCreateScreen) agreementOffered() bool {
	return len(s.agreements) > 0 || s.agreementLoadErr != ""
}

// pickedAgreementName is the committed agreement's display name, or "" when
// none is picked (or the pick somehow outlived its list).
func (s *PurchaseOrderCreateScreen) pickedAgreementName() string {
	if s.agreementID == nil {
		return ""
	}
	for _, a := range s.agreements {
		if a.ID == *s.agreementID {
			return a.Name
		}
	}
	return ""
}

func (s *PurchaseOrderCreateScreen) updateAgreementPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "b":
		// Leave the pick as it was — esc is "I'm done looking", not "clear it".
		// Row 0 is the explicit way to clear.
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.agreementCursor < s.agreementRows()-1 {
			s.agreementCursor++
		}
	case "k", "up":
		if s.agreementCursor > 0 {
			s.agreementCursor--
		}
	case "enter":
		s.commitAgreement()
		s.phase = poPhaseSource
		return s, nil
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) commitAgreement() {
	if s.agreementCursor <= 0 {
		s.agreementID = nil
		return
	}
	idx := s.agreementCursor - 1 // row 0 is "no agreement"
	if idx >= len(s.agreements) {
		return
	}
	id := s.agreements[idx].ID
	s.agreementID = &id
}

// enterAgreementPhase opens the picker with the cursor on the current pick, so
// enter is a no-op confirm and the operator can see what the order carries
// today. Retrying a failed load happens here too — the source chooser's g is
// the only affordance either way, so it shouldn't matter to the operator
// whether the list is empty because it failed or because they haven't looked.
func (s *PurchaseOrderCreateScreen) enterAgreementPhase() tea.Cmd {
	if s.agreementLoadErr != "" && !s.agreementLoading {
		s.agreementLoadErr = ""
		s.agreementLoading = true
		return s.loadAgreements()
	}
	s.agreementCursor = 0
	if s.agreementID != nil {
		for i, a := range s.agreements {
			if a.ID == *s.agreementID {
				s.agreementCursor = i + 1
				break
			}
		}
	}
	s.phase = poPhaseAgreement
	return nil
}

// ---------------------------------------------------------------------------
// Phase: Order-level work order / committee (optional, op-shb9)
// ---------------------------------------------------------------------------

// workOrderRows / committeeRows are the two pickers' row sets, built fresh each
// time so the current pick is always represented. Nothing is grafted here: a
// brand-new order starts with no association, so the only values reachable are
// the ones the pickers just offered (the graft guard is for the detail screen,
// which edits orders that may cite a job since finished).
func (s *PurchaseOrderCreateScreen) workOrderRows() []poAssocOption {
	return s.assoc.workOrderRows(s.workOrderID, "")
}

func (s *PurchaseOrderCreateScreen) committeeRows() []poAssocOption {
	return s.assoc.committeeRows(s.committeeID, "")
}

// pickedWorkOrderLabel / pickedCommitteeLabel name the committed association, or
// "" when none is picked (or a pick somehow outlived its list).
func (s *PurchaseOrderCreateScreen) pickedWorkOrderLabel() string {
	return poAssocLabelFor(s.workOrderRows(), s.workOrderID)
}

func (s *PurchaseOrderCreateScreen) pickedCommitteeLabel() string {
	return poAssocLabelFor(s.committeeRows(), s.committeeID)
}

// enterWorkOrderPhase / enterCommitteePhase open a picker with the cursor on the
// current pick, so enter is a no-op confirm. A failed load is retried here
// instead — w / c is the only affordance either way, so it shouldn't matter to
// the operator whether the list is empty because it failed or because they
// haven't looked yet (same contract as the agreement picker's g).
func (s *PurchaseOrderCreateScreen) enterWorkOrderPhase() tea.Cmd {
	if s.assoc.workOrderErr != "" && !s.assoc.workOrderLoad {
		s.assoc.workOrderErr = ""
		s.assoc.workOrderLoad = true
		return loadWorkOrderOptionsCmd(s.deps)
	}
	s.workOrderCursor = poAssocCursorFor(s.workOrderRows(), s.workOrderID)
	s.phase = poPhaseWorkOrder
	return nil
}

func (s *PurchaseOrderCreateScreen) enterCommitteePhase() tea.Cmd {
	if s.assoc.committeeErr != "" && !s.assoc.committeeLoad {
		s.assoc.committeeErr = ""
		s.assoc.committeeLoad = true
		return loadCommitteeOptionsCmd(s.deps)
	}
	s.committeeCursor = poAssocCursorFor(s.committeeRows(), s.committeeID)
	s.phase = poPhaseCommittee
	return nil
}

func (s *PurchaseOrderCreateScreen) updateWorkOrderPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	rows := s.workOrderRows()
	switch m.String() {
	case "esc", "b":
		// Leave the pick as it was — esc is "I'm done looking", not "clear it".
		// Row 0 is the explicit way to detach.
		s.phase = poPhaseSource
	case "j", "down":
		if s.workOrderCursor < len(rows)-1 {
			s.workOrderCursor++
		}
	case "k", "up":
		if s.workOrderCursor > 0 {
			s.workOrderCursor--
		}
	case "enter":
		if s.workOrderCursor >= 0 && s.workOrderCursor < len(rows) {
			s.workOrderID = rows[s.workOrderCursor].value
		}
		s.phase = poPhaseSource
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) updateCommitteePhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	rows := s.committeeRows()
	switch m.String() {
	case "esc", "b":
		s.phase = poPhaseSource
	case "j", "down":
		if s.committeeCursor < len(rows)-1 {
			s.committeeCursor++
		}
	case "k", "up":
		if s.committeeCursor > 0 {
			s.committeeCursor--
		}
	case "enter":
		if s.committeeCursor >= 0 && s.committeeCursor < len(rows) {
			s.committeeID = rows[s.committeeCursor].value
		}
		s.phase = poPhaseSource
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Phase: Source chooser (r/i/a/f)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateSourcePhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "j", "down", "k", "up", "x", "ctrl+e":
		// The collapsed-cart sentence answers exactly these four keys, and the
		// hidden-rows sentence answers g / w / c. Any other key has moved on
		// from the question it was asked, so the sentence goes back to its
		// un-led wording and the next decline is visibly a new one.
		s.attrLead = ""
	case "g", "w", "c":
		s.cartLead = ""
	default:
		s.cartLead, s.attrLead = "", ""
	}
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
		if s.reorderLoading {
			// Same guard as 'i' below. 'b' out of a picker is not gated on the
			// working frame — the bar names it and it has to keep working — so
			// leaving mid-lookup and pressing the source key again is an
			// ordinary sequence, and it used to put a second request on the
			// wire for a reply the first one is already going to deliver.
			return s, s.reorderVerdictNote("")
		}
		s.reorderLoading = true
		s.reorderLoadErr = ""
		s.reorderNote.clear()
		return s, s.loadReorderItemsForSupplier()
	case "i":
		s.phase = poPhaseItemPick
		if s.itemSuppliersLoad {
			// A walk is already out for this supplier — say so rather than
			// firing a second one or posting an entry note that concludes
			// something about rows still in transit.
			//
			// AHEAD of the reset below, like the 'a' arm: this arm used to
			// clear the search box first, so 'i' → '/' → type → 'b' → 'i'
			// mid-walk threw away the query the operator had typed with
			// nothing on the pane saying it had gone.
			return s, s.catalogVerdictNote("")
		}
		s.itemSuppliersSearch.SetValue("")
		s.itemSuppliersSearch.Blur()
		s.itemSuppliersTyping = false
		s.itemSuppliersCur = 0
		if s.catalogAnswered() {
			// Already held for THIS supplier. Showing the "Looking up the items
			// … sells…" frame here would be the same rule broken from the other
			// side: that frame is a claim that work is happening, and on a
			// ten-line order it would be claimed ten times over one catalog
			// that is fetched once. Open on the rows; r goes and asks again.
			s.applyItemSupplierFilter()
			return s, s.itemPickEntryNote()
		}
		s.itemSuppliersLoad = true
		s.itemSuppliersErr = ""
		s.itemSuppliersNote.clear() // the generic working line speaks for this one
		return s, s.loadItemSuppliersForSupplier()
	case "a":
		s.phase = poPhaseAssetPick
		if s.assetsLoading {
			// Two lookups for the SAME supplier are still wrong even though
			// the reply now carries a generation and the stale one is dropped:
			// the operator would be watching a "looking up…" frame whose answer
			// is thrown away, and the second request is work nobody asked for.
			// Gating the search box (updateAssetPickPhase) closed that race
			// only from inside the picker: 'a' -> '/' + search -> 'b' -> 'a'
			// left the search request in flight and fired an unfiltered page 1
			// over it, and if the search reply landed last the pane showed a
			// FILTERED SUBSET as the supplier's whole asset list — empty search
			// box, no "search:" row, "N asset(s)" with a green tick.
			//
			// Nothing below this line runs either: resetting the query and the
			// page while the request that owns them is still out is what made
			// the mismatch legible as a success.
			return s, s.assetVerdictNote("")
		}
		s.assetsLoading = true
		s.assetsErr = ""
		s.assetsSearch.SetValue("")
		s.assetsTyping = false
		s.assetsPage = 1
		s.assetsQuery = ""
		s.assetsNote.clear()
		return s, s.loadAssetsForSupplier("")
	case "f":
		// Freeform: go straight to the line form with nothing
		// pre-filled.
		s.enterLinePhase(nil, nil, "", 0, 0, 0, 0)
		return s, textinput.Blink
	case "g":
		// Optional purchase/pricing agreement. Silent when this supplier has
		// none on file — the key is only advertised alongside a rendered row,
		// and opening an empty picker would be a dead end (the web form
		// likewise renders the select only when agreements exist).
		if !s.agreementOffered() {
			return s, nil
		}
		if !s.sourceAttributionShown() {
			return s, s.attributionHiddenNote("g")
		}
		return s, s.enterAgreementPhase()
	case "w":
		// Optional work-order association (op-shb9). Silent when there are no
		// unfinished jobs, for the same reason g is silent without agreements.
		if !s.assoc.workOrdersOffered() {
			return s, nil
		}
		if !s.sourceAttributionShown() {
			return s, s.attributionHiddenNote("w")
		}
		return s, s.enterWorkOrderPhase()
	case "c":
		// Optional committee association. 'c' is free here — the source chooser
		// spends r/i/a/f on line sources and g/d/x on the rest.
		if !s.assoc.committeesOffered() {
			return s, nil
		}
		if !s.sourceAttributionShown() {
			return s, s.attributionHiddenNote("c")
		}
		return s, s.enterCommitteePhase()
	case "d":
		// Done adding lines → review + submit. Only meaningful once the
		// cart has at least one line (the backend rejects an empty PO).
		if len(s.lines) == 0 {
			s.setErr("add at least one line before submitting", "")
			return s, Status(s.errMsg, StatusError)
		}
		s.phase = poPhaseReview
		s.clampReviewCursor()
		s.poNotes.Focus()
		return s, textinput.Blink
	case "j", "down":
		// Move the cart highlight — the same cursor the review phase uses, so
		// x / ctrl+e below act on ANY line, not just the last one added.
		//
		// All four of these decline when the cart is collapsed. A highlight
		// that is not on the pane is one the operator cannot check before
		// pressing x, and removing or editing a line nobody can see is the
		// wrong-purchase-order failure this screen keeps being measured
		// against. The bar stops naming them for exactly as long as the gate
		// holds (sourceHelpText).
		if !s.cartListedOnScreen() {
			return s, s.cartHiddenNote(m.String() + " moves nothing")
		}
		if s.reviewCursor < len(s.lines)-1 {
			s.reviewCursor++
		}
		return s, nil
	case "k", "up":
		if !s.cartListedOnScreen() {
			return s, s.cartHiddenNote(m.String() + " moves nothing")
		}
		if s.reviewCursor > 0 {
			s.reviewCursor--
		}
		return s, nil
	case "x":
		// Remove the highlighted line. The cursor follows each add (addLine
		// parks it on the new line), so with an untouched highlight this is
		// still the "undo the line I just added" it always was — but j/k can
		// now aim it at any line in the cart.
		if !s.cartListedOnScreen() {
			return s, s.cartHiddenNote("x removes nothing")
		}
		s.removeLineAt(s.reviewCursor)
		return s, nil
	case "ctrl+e":
		// Edit the highlighted line in place without a detour through review.
		// Same handler the review cart uses; saving/cancelling comes back here
		// (editReturn) so the operator keeps adding lines where they left off.
		// ctrl+e (not a bare letter) matches the review-cart chord.
		if !s.cartListedOnScreen() {
			return s, s.cartHiddenNote("ctrl+e edits nothing")
		}
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
	s.setErr("", "")
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
	s.setErr("", "")
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
		s.setErr("item description is required", "")
		return Status(s.errMsg, StatusError)
	}
	qty, err := strconv.Atoi(strings.TrimSpace(s.lineInputs[poLineFieldQty].Value()))
	if err != nil || qty <= 0 {
		s.setErr("quantity must be a positive integer", "")
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
		s.setErr(label+" must be a non-negative number", "")
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
				s.setErr("expected date must be YYYY-MM-DD (or blank)", "")
				return Status(s.errMsg, StatusError)
			}
			line.ExpectedShipmentDate = raw
		}
	}

	cartLine := poCartLine{item: line, label: s.lineLabel(desc), qpp: s.pickedQPP}
	s.setErr("", "")

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
		s.setErr("supplier is required (return to supplier phase)", "")
		return Status(s.errMsg, StatusError)
	}
	if len(s.lines) == 0 {
		s.setErr("add at least one line before submitting", "")
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
	// Only a committed agreement rides along; leaving the field off is how the
	// payload says "none", which is what nearly every order means (op-yoos).
	if s.agreementID != nil {
		id := *s.agreementID
		req.SupplierAgreementID = &id
	}
	// The order-level associations follow the same omit-when-unset rule
	// (op-shb9): each resolves to a row the backend rejects when it can't find
	// it, so an unpicked association has to be an ABSENT key. Both are set from
	// the picker values, whose zero value is exactly "not picked".
	req.WorkOrder = s.workOrderID
	req.OwningGroup = poCommitteeID(s.committeeID)

	s.pending = true
	s.pendingLead = ""
	s.setErr("", "")
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
			// The bar has already stopped naming enter here, and the press
			// answers on the "Submitting…" line rather than into a flash: an
			// operator watching a slow POST and pressing enter again is exactly
			// the operator who will have missed a four-second status.
			s.pendingLead = "enter is already in"
			return s, Status("the submit is already out", StatusInfo)
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
	// Folded, not truncated. This line is the screen's action bar — it is the
	// only place several of these phases name enter, b and esc at all — and at
	// 80 columns the pane cuts it around "…(j/k move, / sear", which loses
	// every key it exists to advertise. Folding is not the columnar conversion
	// that is queued for this screen; it is the same "a hint the operator
	// cannot finish reading is worse than none" rule the pickers now follow.
	b.WriteString(pickerHint(s.helpText()))
	b.WriteString("\n\n")

	// Always show the committed supplier (if any) as a header so the
	// operator never loses context on which supplier the line will be
	// billed to.
	b.WriteString(s.renderSupplierHeader())
	b.WriteString("\n")

	switch s.phase {
	case poPhaseSupplier:
		b.WriteString(s.renderSupplierPhase())
	case poPhaseSupplierSwitch:
		b.WriteString(s.renderSupplierSwitchPhase())
	case poPhaseAgreement:
		b.WriteString(s.renderAgreementPhase())
	case poPhaseWorkOrder:
		b.WriteString(s.renderAssocPickPhase("Work order (optional)", s.workOrderRows(), s.workOrderCursor))
	case poPhaseCommittee:
		b.WriteString(s.renderAssocPickPhase("Committee (optional)", s.committeeRows(), s.committeeCursor))
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
	b.WriteString(s.renderFailLine(s.helpText()))
	return b.String()
}

// setErr records the screen's failure line: what went wrong, and the unbounded
// half underneath it. Both fields, here and only here, so a detail can never
// outlive the headline it belonged to — a 502's HTML body left standing under
// "quantity must be a positive integer" would read as the gateway explaining
// the validation, which is a worse lie than either sentence alone.
func (s *PurchaseOrderCreateScreen) setErr(what, detail string) {
	s.errMsg, s.errDetail = what, detail
}

// renderFailLine is the last thing View draws: the submit's working line, or
// the failure that came back from it.
//
// Through pickerFail like the three picker failure frames, for the same two
// reasons. It is FOLDED because the detail is an OMS response body and the pane
// is 51 columns that clampToBox truncates — at submit, which is the one place
// where losing the reason costs the whole order, the operator used to read
// "✗ oms: http 502: <!DOCTYPE html><htm" and nothing more. And it is TRIMMED to
// a row budget because that body renders as however many rows it needs: an
// unbudgeted block here is a block that pushes itself, and whatever the phase
// draws last, off the bottom.
//
// help is the action bar the caller is measuring against, so the budget and
// the frame accounting that reserves for it are computed from one wording.
func (s *PurchaseOrderCreateScreen) renderFailLine(help string) string {
	switch {
	case s.pending:
		// The lead is what a key the submit has made inert just did, so the
		// press moves the BODY rather than only the four-second flash. Folded
		// like every other line on these screens: the lead grows it past 51.
		if s.pendingLead != "" {
			return pickerHint(s.pendingLead + " · Submitting…")
		}
		return pickerHint("Submitting…")
	case s.errMsg == "":
		return ""
	}
	return pickerFail(s.errMsg, s.errDetail, s.failRowBudget(help))
}

// failRowBudget is how many rendered rows the failure line may spend. Derived,
// not constant: the pane, less the chrome around the body, less the smallest
// body this phase can honestly draw. What is left is the failure's, and the
// DETAIL is what pickerFail sacrifices when it does not fit — the headline is
// the row that survives.
func (s *PurchaseOrderCreateScreen) failRowBudget(help string) int {
	if s.terminalHeight <= 0 {
		// Unsized screens get no budget at all, here as everywhere else on this
		// screen: guessing a pane height would hide rows nobody asked to hide.
		return 0
	}
	rows := screenBodyHeight(s.terminalHeight) - s.frameChromeRows(help) - s.failBodyFloor()
	if rows < 1 {
		// One row is the floor rather than zero: a failure with no line at all
		// is the silence this whole file exists to remove, and pickerFail's
		// first row is the sentence naming what failed.
		rows = 1
	}
	return rows
}

// failBodyFloor is the rows the phase body keeps even under a long failure.
//
// It is measured from the phase's SMALLEST honest layout rather than the one
// on screen, because the layout decisions that shrink a phase
// (sourceTitleShown, sourceAttributionShown) measure a frame that includes this
// very line — reading the live layout here would send them round in a circle.
// The source chooser is the one phase whose body cannot scroll: its four line
// sources and the cart's collapsed sentence are a fixed height, so a failure
// line budgeted against bodyRowBudget's three-row floor would have pushed the
// cart's existence off the pane instead of trimming its own detail.
func (s *PurchaseOrderCreateScreen) failBodyFloor() int {
	switch s.phase {
	case poPhaseSource:
		return poRenderedRows(s.sourceChrome(false, false)) + s.sourceCartMinRows()
	case poPhaseReview:
		// The cart's own chrome and the tail at its shortest — which still
		// carries the focused PO-notes input. This is the phase the failure
		// actually arrives on, and an operator typing into a field that is off
		// the pane is the worst form of this defect, so the notes row is
		// reserved before the gateway's HTML gets a single line.
		//
		// The cart's one LISTED line is deliberately not reserved here:
		// renderReviewPhase clamps its budget up to one row whatever this
		// answer is, so counting it again would spend a row twice and leave the
		// failure a single line — the headline with nothing under it saying
		// there is more, which reads as the whole story.
		return s.cartChromeRows() + poRenderedRows(s.reviewTail(false))
	}
	return poFailBodyFloor
}

// poFailBodyFloor mirrors bodyRowBudget's floor: every scrolling block on this
// screen is guaranteed three rows, so the failure line may not take the pane
// below them.
const poFailBodyFloor = 3

// sourceHelpText is the source chooser's action bar. cartListed says whether
// the cart's ROWS are on the pane, because the four keys that act on a row —
// j, k, ctrl+e and x — may only be named while there is a row to act on. On an
// 18-row pane a long cart is collapsed to a summary and those keys decline, so
// naming them there would be the "a key the bar names must act" half of the
// rule broken by the bar itself.
//
// Taken as an argument rather than read from the screen because the collapse
// decision is measured against this very sentence: frameRows() folds it to
// count the rows it costs, so cartListedOnScreen() would recurse through
// helpText() if this arm asked it. The decision is made against the LONGER
// (cart-listed) wording, which is the conservative direction — the collapsed
// bar folds to no more rows, so it can never turn the answer back round.
func (s *PurchaseOrderCreateScreen) sourceHelpText(cartListed, attribution bool) string {
	base := "Add a line · r reorder queue · i inventory items · a assets · f freeform · b back · esc cancel"
	switch {
	case len(s.lines) > 0 && cartListed:
		base = fmt.Sprintf(
			"Add a line (r/i/a/f) · j/k highlight · ctrl+e edit · x remove · d done → review %d line(s) · b back · esc cancel",
			len(s.lines),
		)
	case len(s.lines) > 0:
		base = fmt.Sprintf(
			"Add a line (r/i/a/f) · d review & edit %d line(s) · b back · esc cancel",
			len(s.lines),
		)
	}
	// g / w / c only appear once there is something behind them AND their rows
	// are on the pane. Advertising a key that does nothing is worse than not
	// offering it, and a key naming a row the frame has dropped for want of
	// space is the same defect one step along: the rows go first when the pane
	// runs out (sourceAttributionShown), so the bar has to go with them.
	//
	// Joined at the same " · " the rest of the bar uses. Appended with a plain
	// space they merged into one 51-cell chunk that pickerWrap could only
	// word-fold, and the bar spread over FOUR rows — rows taken straight out of
	// the bottom of the pane, which is where the cart is.
	if attribution {
		if s.agreementOffered() {
			base += " · g agreement"
		}
		if s.assoc.workOrdersOffered() {
			base += " · w work order"
		}
		if s.assoc.committeesOffered() {
			base += " · c committee"
		}
	}
	return base + "."
}

func (s *PurchaseOrderCreateScreen) helpText() string {
	switch s.phase {
	case poPhaseSupplier:
		return "Pick a supplier · " + s.supplierPickBar()
	case poPhaseSupplierSwitch:
		// Exactly the two keys the frame binds, from the same sentence the
		// frame prints. j/k, enter and the rest are deliberately absent: they
		// do nothing here.
		return "Changing supplier · " + s.supplierSwitchBar()
	case poPhaseAgreement:
		// One claim per segment, and `b` among them: it is bound here exactly as
		// esc is (both return to the source chooser) and no bar named it, which
		// is the "a key the bar does not name must do nothing" half of the rule
		// the derived phase sweep now presses for.
		return "Purchase / pricing agreement · j/k move · enter commits · b back · esc keeps the current one · row 1 is none"
	case poPhaseWorkOrder:
		return "Work order for this purchase · j/k move · enter commits · b back · esc keeps the current one · row 1 is none"
	case poPhaseCommittee:
		return "Committee this purchase is on behalf of · j/k move · enter commits · b back · esc keeps the current one · row 1 is none"
	case poPhaseSource:
		return s.sourceHelpText(s.cartListedOnScreen(), s.sourceAttributionShown())
	case poPhaseReorderPick:
		// The three picker bars come from the pickers themselves
		// (po_create_pickers.go), which is also where each frame's own way-out
		// line comes from. One sentence, both surfaces: a bar and a body line
		// that state the same fact by hand are a bar and a body line that end
		// up contradicting each other.
		return "Reorder queue · " + s.reorderPickBar()
	case poPhaseItemPick:
		return "Items · " + s.itemPickBar()
	case poPhaseAssetPick:
		return "Assets · " + s.assetPickBar()
	case poPhaseLine:
		if s.editIndex >= 0 {
			// Editing an existing cart line (opened with ctrl+e from review).
			if s.pickedItemSup != nil && s.pickedQPP > 1 {
				return "Editing line · tab/shift+tab cycle fields · ctrl+t unit/case cost basis · enter saves the changes · esc cancels the edit"
			}
			return "Editing line · tab/shift+tab cycle fields · enter saves the changes · esc cancels the edit"
		}
		if s.pickedItemSup != nil && s.pickedQPP > 1 {
			return "Line entry · tab/shift+tab cycle fields · ctrl+t unit/case cost basis · enter adds to the cart · esc picks a different source"
		}
		return "Line entry · tab/shift+tab cycle fields · enter adds to the cart · esc picks a different source"
	case poPhaseReview:
		bar := "Review cart — type PO notes · ↑↓ highlight a line · ctrl+e edit it · ctrl+x remove it"
		if s.pending {
			// The POST is out and the enter arm declines, so the bar stops
			// naming it — the same drop itemPickBar and assetPickBar make for a
			// gated key. esc still works: leaving review does not cancel the
			// request, and stranding the operator on a frame with no way out
			// while a slow gateway thinks about it would be the worse defect.
			return bar + " · esc back · submitting…"
		}
		return bar + " · enter submit · esc back."
	}
	return ""
}

// supplierLabel names the committed supplier for the pickers' "working" lines.
// A status line that names the supplier is the difference between "something is
// happening" and "we are asking OMS what Acme sells", which is what the report
// asked for. Falls back to the id, and then to a generic noun, so the sentence
// is never left with a hole in it.
func (s *PurchaseOrderCreateScreen) supplierLabel() string {
	for _, sup := range s.suppliers {
		if sup.ID == s.supplierID {
			if sup.Name != "" {
				// Bounded: the label goes inside a one-line status sentence in a
				// 51-column pane that TRUNCATES, so a long supplier name would
				// push the verb off the edge and leave "Looking up the items Ac".
				return pickerClip(sup.Name, 20)
			}
		}
	}
	if s.supplierID > 0 {
		return fmt.Sprintf("supplier #%d", s.supplierID)
	}
	return "this supplier"
}

// poHeaderValueFloor is the cells a clipped header value keeps, the same floor
// renderAssocValue uses: enough that the operator can see a value is there and
// that it was shortened, rather than reading a label followed by nothing.
const poHeaderValueFloor = 6

// headerFailRows caps the rows the supplier header may spend on a failed
// supplier load. A fraction of the pane rather than a measurement of what is
// left, deliberately: this row is chrome that rides on EVERY phase, so the
// honest measurement would be "the pane less the body" — and the body's own
// height is decided by sourceTitleShown / sourceAttributionShown, which measure
// a frame that includes this header. A third is what the header may take; the
// rest of the pane belongs to whatever the operator came to do.
func (s *PurchaseOrderCreateScreen) headerFailRows() int {
	if s.terminalHeight <= 0 {
		return 0
	}
	return screenBodyHeight(s.terminalHeight) / 3
}

func (s *PurchaseOrderCreateScreen) renderSupplierHeader() string {
	switch {
	case s.supplierLoading:
		return StyleTitle.Render("Supplier:") + " " + StyleMuted.Render("loading suppliers…")
	case s.supplierLoadErr != "":
		// Folded and trimmed like every other failure on these screens: the
		// string is an OMS response body, so written straight to the pane it
		// was cut at 51 columns AND — with the newlines an HTML error page
		// always carries — rendered as a lipgloss BLOCK that padded every short
		// line to the widest, leaking columns onto the row beneath it.
		return pickerFail("loading the suppliers failed", s.supplierLoadErr, s.headerFailRows())
	case s.supplierID > 0:
		name := ""
		for _, sup := range s.suppliers {
			if sup.ID == s.supplierID {
				name = sup.Name
				break
			}
		}
		// Both values here are OMS-supplied and were unbounded, on the one row
		// that is drawn on every phase — including review, where it is the last
		// thing seen before submit. At 51 columns "Supplier: Acme Supply (#1) ·
		// agreement: Annual 2026 Steel Contract" is 67 cells, so the pane cut
		// the agreement mid-name and a longer supplier name removed it, and the
		// `(#id)`, altogether: the confirm-before-submit surface losing which
		// agreement the order is placed under.
		//
		// Bounded the way renderAssocValue bounds an association's value —
		// pickerClip to the room the labels leave, one row, ellipsis included —
		// rather than folded, because this row is drawn on every phase and a
		// second row would come out of every one of their budgets.
		const label, agreeLabel = "Supplier: ", "  · agreement: "
		id := fmt.Sprintf(" (#%d)", s.supplierID)
		agreement := s.pickedAgreementName()
		room := pickerPaneWidth - lipgloss.Width(label) - lipgloss.Width(id)
		if agreement != "" {
			// The supplier is the row's subject and takes what is left, but the
			// agreement's label and floor are reserved FIRST: that a pricing
			// agreement is attached at all is the fact a long supplier name
			// used to take off the pane.
			room -= lipgloss.Width(agreeLabel) + poHeaderValueFloor
		}
		if room < poHeaderValueFloor {
			room = poHeaderValueFloor
		}
		name = pickerClip(name, room)
		line := StyleTitle.Render("Supplier:") + " " + StyleStatusOK.Render(name+id)
		// A committed agreement is header-level context, not a line item, so it
		// rides with the supplier and stays on screen through every phase.
		if agreement != "" {
			left := pickerPaneWidth - lipgloss.Width(label+name+id+agreeLabel)
			if left < poHeaderValueFloor {
				left = poHeaderValueFloor
			}
			line += "  " + StyleMuted.Render("· agreement:") + " " + pickerClip(agreement, left)
		}
		return line
	default:
		return StyleTitle.Render("Supplier:") + " " + StyleMuted.Render("(none picked)")
	}
}

// renderAgreementPhase draws the optional agreement picker: a leading
// "no agreement" row followed by the supplier's active agreements. The picked
// agreement's notes render underneath (the terms are the reason to cite one),
// mirroring the web form's notes paragraph under its select.
func (s *PurchaseOrderCreateScreen) renderAgreementPhase() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Purchase / pricing agreement (optional)") + "\n")
	tail := ""
	if s.agreementCursor > 0 && s.agreementCursor-1 < len(s.agreements) {
		if notes := strings.TrimSpace(s.agreements[s.agreementCursor-1].Notes); notes != "" {
			tail = "\n" + pickerHint(notes) + "\n"
		}
	}
	b.WriteString(renderWindowedList(
		s.agreementRows(), s.agreementCursor,
		s.bodyRowBudget(poRenderedRows(b.String())+poRenderedRows(tail)),
		func(i int) string {
			if i == 0 {
				return "— no agreement —"
			}
			return s.agreements[i-1].Name
		},
	))
	b.WriteString(tail)
	return b.String()
}

// renderAgreementRow is the source chooser's / review phase's one-line summary
// of the agreement state. Returns "" when this supplier offers none, so the
// surfaces that call it render nothing at all.
func (s *PurchaseOrderCreateScreen) renderAgreementRow(withKey bool) string {
	if !s.agreementOffered() {
		return ""
	}
	// With the key it's an affordance in a menu of them, so it names the field
	// and says the field is optional; without, it's a line on a confirmation
	// screen, where the short label the PO detail screen uses reads better.
	label := "  " + StyleTitle.Render("Agreement")
	if withKey {
		// "Purchase / pricing agreement (optional)" put this row at 52 cells
		// with an EMPTY value, so the 51-column pane cut the value off every
		// time and any real agreement name with it. The short label the PO
		// detail screen uses is the one the non-key form already preferred.
		label = "  " + StyleStatusOK.Render("g") + "  " +
			StyleTitle.Render("Agreement (optional)")
	}
	// Say the list is MISSING rather than absent — "no agreements" and
	// "couldn't ask" are different facts, and only one of them is safe to let
	// the operator assume. Bounded like every other OMS-supplied value on these
	// rows; an APIError message is the whole raw response body.
	return renderAssocValue(label, s.pickedAgreementName(), s.agreementLoadErr) + "\n"
}

// renderPendingLookups names the optional header lookups that are still in
// flight — the supplier's agreements, and the work-order / committee option
// lists that ride along with the screen opening.
//
// None of them gate anything, which is why they load in the background and why
// their affordances (g / w / c) only appear once there is something to pick.
// But "no row yet" and "this supplier has no agreements" render identically,
// and the second is a conclusion the operator may act on. So the wait says it
// is a wait. Deliberately WITHOUT a key: naming g here would advertise a key
// that opens an empty picker, and the bar may only name keys that work.
func (s *PurchaseOrderCreateScreen) renderPendingLookups() string {
	var pending []string
	if s.agreementLoading && !s.agreementOffered() {
		pending = append(pending, "purchase / pricing agreements")
	}
	if s.assoc.workOrderLoad && !s.assoc.workOrdersOffered() {
		pending = append(pending, "work orders")
	}
	if s.assoc.committeeLoad && !s.assoc.committeesOffered() {
		pending = append(pending, "committees")
	}
	if len(pending) == 0 {
		return ""
	}
	// Three pending lookups name 74 columns' worth of subject, so this folds
	// like every other line on these screens: a wait that says which lookups
	// are outstanding is only useful if the operator can read which.
	var b strings.Builder
	for _, line := range pickerWrap("still looking up "+strings.Join(pending, " · ")+"…", pickerPaneWidth-2) {
		b.WriteString("  " + StyleMuted.Render(line) + "\n")
	}
	return b.String()
}

// renderAssocPickPhase draws a work-order / committee picker: row 0 is the
// explicit "none", the rest are whatever loaded. One renderer for both, so the
// two associations can never diverge in how they're picked.
func (s *PurchaseOrderCreateScreen) renderAssocPickPhase(title string, rows []poAssocOption, cursor int) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render(title) + "\n")
	tail := "\n" + pickerHint(
		"Records who this order is for. It does not change stock, pricing, or what a committee is billed.") + "\n"
	b.WriteString(renderWindowedList(len(rows), cursor,
		s.bodyRowBudget(poRenderedRows(b.String())+poRenderedRows(tail)),
		func(i int) string { return rows[i].label }))
	b.WriteString(tail)
	return b.String()
}

// renderAssocRows is the source chooser's / review phase's summary of the two
// order-level associations (op-shb9). withKey renders them as affordances in a
// menu of them; without, as lines on the confirm-before-submit surface. Returns
// "" when neither is offered, so a shop that runs no work orders and no
// committees sees nothing about either.
func (s *PurchaseOrderCreateScreen) renderAssocRows(withKey bool) string {
	var b strings.Builder
	if s.assoc.workOrdersOffered() {
		label := "  " + StyleTitle.Render("Work order")
		if withKey {
			label = "  " + StyleStatusOK.Render("w") + "  " + StyleTitle.Render("Work order (optional)")
		}
		b.WriteString(renderAssocValue(label, s.pickedWorkOrderLabel(), s.assoc.workOrderErr) + "\n")
	}
	if s.assoc.committeesOffered() {
		label := "  " + StyleTitle.Render("Committee")
		if withKey {
			label = "  " + StyleStatusOK.Render("c") + "  " + StyleTitle.Render("Committee (optional)")
		}
		b.WriteString(renderAssocValue(label, s.pickedCommitteeLabel(), s.assoc.committeeErr) + "\n")
	}
	return b.String()
}

func (s *PurchaseOrderCreateScreen) renderSupplierPhase() string {
	if s.supplierLoading || s.supplierLoadErr != "" {
		return s.supplierNote.render()
	}
	if len(s.suppliers) == 0 {
		if note := s.supplierNote.render(); note != "" {
			return note
		}
		return pickerHint("(no suppliers configured)")
	}
	// Through the shared windower like every other scrolling block on this
	// screen. Its own copy of the logic had both of the failures that one was
	// fixed for: a fixed ten-row window that no row budget could shrink, and
	// scroll markers whose newline sat INSIDE Render — which makes lipgloss
	// treat the marker as a two-line block, pad the short line, and leak twenty
	// columns onto the supplier row underneath it, pushing that row's "(#id)"
	// past the 51-column cut.
	tail := ""
	if note := s.supplierNote.render(); note != "" {
		tail = "\n" + note
	}
	return renderWindowedList(len(s.suppliers), s.supplierCursor,
		s.bodyRowBudget(poRenderedRows(tail)),
		func(i int) string {
			sup := s.suppliers[i]
			return fmt.Sprintf("%s  (#%d)", sup.Name, sup.ID)
		}) + tail
}

// sourceCartChrome is everything the source chooser draws ABOVE the cart. It is
// one function because the collapse decision has to measure exactly what the
// renderer will write, and a second hand-kept copy of these rows would be the
// stale constant this screen has already been bitten by.
// sourceAttributionRows is the optional header block: the agreement row, the
// work-order and committee rows, and the "still looking up…" line for whichever
// of those is in flight. Header-level, not line sources — so they sit below the
// r/i/a/f block with a blank line between, and only when there is something
// behind each key.
//
// They are also the LOWEST-value rows on this frame, which is what decides the
// order of sacrifice when the pane runs out. See sourceAttributionShown.
func (s *PurchaseOrderCreateScreen) sourceAttributionRows() string {
	return s.renderAgreementRow(true) + s.renderAssocRows(true) + s.renderPendingLookups()
}

// sourceAttributionShown reports whether that block is drawn.
//
// The source chooser gives ground in a fixed order when the pane is too short,
// and this is the first thing to go. Last to go, in order: the cart's EXISTENCE
// — the count, the `d` that opens it, and its total — then the r/i/a/f rows the
// screen is for, then the TITLE (sourceTitleShown, the step after this one),
// then these. Three header rows plus the bar
// they lengthen were enough on their own to push the whole collapsed-cart
// sentence off an 18-row pane, so the frame drew no cart at all and the four
// gated keys answered into a four-second flash: the operator could not see that
// a cart existed, let alone what it came to.
//
// It asks TWO questions, and both have to say yes before a row is dropped: does
// hiding actually free rows, and does the frame overflow with them shown.
//
// Asking only the second is what the first version did, and dropping a row that
// costs nothing to keep is worse than the overflow it was avoiding. A supplier
// offering exactly ONE optional row spends the same rows either way — the
// substitute notice is one row in the same blank-plus-row slot the row occupied,
// and the bar folds to the same height with or without that one clause — so at
// 80x24 the frame replaced a real committee row with "optional rows need more
// height", which was FALSE, stopped naming `c`, and made `c` decline. A false
// sentence plus a disabled working key is the bar-honesty rule inside out.
//
// Both layouts are measured whole (chrome plus the frame the bar folds to),
// because that is what "does hiding help" means: with three rows the hidden
// layout is three rows shorter and the trade is real, with one it is level and
// there is nothing to trade. The cart block is the same in both, so it only
// enters the second question.
func (s *PurchaseOrderCreateScreen) sourceAttributionShown() bool {
	if s.terminalHeight <= 0 || s.sourceAttributionRows() == "" {
		// Unsized screens get no budget at all, here as everywhere else on this
		// screen, and there is nothing to hide when the block is empty.
		return true
	}
	// Measured WITH the title in both worlds: the title is the next thing to
	// give after these rows, not before them, so a decision about them may not
	// help itself to rows the title is still holding.
	shown := poRenderedRows(s.sourceChrome(true, true)) + s.frameRowsWith(s.sourceHelpText(false, true))
	hidden := poRenderedRows(s.sourceChrome(false, true)) + s.frameRowsWith(s.sourceHelpText(false, false))
	if hidden >= shown {
		return true
	}
	return shown+s.sourceCartMinRows() <= screenBodyHeight(s.terminalHeight)
}

// sourceCartMinRows is what the cart's EXISTENCE costs the frame: its blank
// separator, the collapsed sentence folded, and one row of slack.
//
// The slack is what a declining key's lead costs when it folds the sentence
// onto another row, and it is reserved up front so that neither this frame's
// layout decisions can change under the operator's hands because they pressed
// j. Over-reserving one row beats clipping one, as everywhere else on this
// screen. One function because sourceAttributionShown and sourceTitleShown are
// consecutive steps of one sacrifice order and must reserve the same cart.
func (s *PurchaseOrderCreateScreen) sourceCartMinRows() int {
	if len(s.lines) == 0 {
		return 0
	}
	return 2 + poRenderedRows(pickerNote{text: s.cartHiddenSentence("")}.render())
}

// sourceTitleShown is the NEXT step of that order, and the reason the cart's
// total is no longer what a short pane takes.
//
// "Where should this line come from?" and its blank line are two rows that name
// no key and carry no value: the four r/i/a/f rows immediately under them say
// what the screen is, so dropping the title costs the operator nothing and no
// substitute notice is owed for it — unlike the optional rows, whose keys stop
// working and which therefore have to say they are gone.
//
// It is measured against the same conservative reservation the attribution
// decision uses — the chrome, the folded bar and the cart's minimum — rather
// than against cartListedOnScreen, which asks this very question through
// sourceCartChrome. That reservation is a row or two more than the frame spends
// today, so the title goes while the pane still has a row spare, and that is
// deliberate: the spare row is what a declining key's lead spends when it folds
// the cart sentence onto another line, and the title is the row this screen can
// most afford to lose. Dropping it costs nothing; letting a keypress push the
// cart's total off the bottom is the defect this step exists to close.
func (s *PurchaseOrderCreateScreen) sourceTitleShown() bool {
	if s.terminalHeight <= 0 {
		return true
	}
	attribution := s.sourceAttributionShown()
	need := poRenderedRows(s.sourceChrome(attribution, true)) +
		s.frameRowsWith(s.sourceHelpText(false, attribution)) +
		s.sourceCartMinRows()
	return need <= screenBodyHeight(s.terminalHeight)
}

// sourceAttributionNote is what the frame says INSTEAD of those rows. Dropping
// them silently would leave the operator reading a screen whose g / w / c keys
// have quietly stopped working with nothing to say why, which is the same
// silence as the rest of this file. It carries a lead when one of those keys
// has just been declined, so the press moves the body.
// Both strings are FIXED, which is what lets this be one rendered row led or
// not: "g is off here · optional rows need more height" is 46 of the 49 a
// warned note gets. That is a budget, not a preference — the frame draws this
// where the rows would have been, nothing downstream reserves for it, and a
// second row appearing on a keypress would push the cart summary off the
// bottom, which is the defect this whole block exists to close.
func (s *PurchaseOrderCreateScreen) sourceAttributionNote() pickerNote {
	text := "optional rows need more height"
	level := StatusInfo
	if s.attrLead != "" {
		text, level = s.attrLead+" · "+text, StatusWarn
	}
	return pickerNote{text: text, level: level}
}

// attributionHiddenNote answers g / w / c while their rows are off the pane.
// The bar has stopped naming them (sourceHelpText), so they must not act.
func (s *PurchaseOrderCreateScreen) attributionHiddenNote(key string) tea.Cmd {
	s.attrLead = key + " is off here"
	n := s.sourceAttributionNote()
	return Status(n.flash(), n.level)
}

func (s *PurchaseOrderCreateScreen) sourceCartChrome() string {
	return s.sourceChrome(s.sourceAttributionShown(), s.sourceTitleShown())
}

func (s *PurchaseOrderCreateScreen) sourceChrome(attribution, title bool) string {
	var b strings.Builder
	if title {
		b.WriteString(StyleTitle.Render("Where should this line come from?") + "\n\n")
	}
	b.WriteString("  " + StyleStatusOK.Render("r") + "  Reorder queue (items flagged for reorder)\n")
	b.WriteString("  " + StyleStatusOK.Render("i") + "  Inventory items associated with this supplier\n")
	b.WriteString("  " + StyleStatusOK.Render("a") + "  Assets purchased from this supplier\n")
	b.WriteString("  " + StyleStatusOK.Render("f") + "  Freeform line (no item / asset reference)\n")
	if header := s.sourceAttributionRows(); header != "" {
		if attribution {
			b.WriteString("\n" + header)
		} else {
			b.WriteString("\n" + s.sourceAttributionNote().render() + "\n")
		}
	}
	if len(s.lines) > 0 {
		// Keys ABOVE the block that grows, as on the supplier-switch confirm.
		// This row used to sit UNDER the cart, and the cart cannot shrink past
		// its own header and total — so on an 18-row pane with a one-line cart
		// the fixed chrome alone overflowed and clampToBox ate exactly the row
		// naming d. The cart is the part that can give.
		b.WriteString("\n  " + StyleStatusOK.Render("d") + "  Done — review & submit\n")
	}
	return b.String()
}

// sourceCartKeys is the hint naming the four keys that act on a cart ROW. It is
// drawn only while those rows are on the pane, for the same reason
// sourceHelpText stops naming them: the keys decline when the cart is
// collapsed, and a hint that outlives the thing it names is the defect.
func sourceCartKeys() string {
	return pickerHint("j/k highlight a line · ctrl+e edit it · x remove it") + "\n\n"
}

// cartListedOnScreen reports whether the source chooser is drawing the cart's
// ROWS — the same question itemListOnScreen / assetListOnScreen answer for the
// pickers, and it gates the same three things: the keys the bar names, the keys
// the arms honour, and whether a highlight exists to act on.
//
// It is measured, not assumed. At 80x24 the chooser's fixed chrome leaves the
// cart one or two rows, bodyRowBudget floors at three and hands out rows that
// do not exist, and clampToBox then dropped the HIGHLIGHTED line, the "N more
// below" marker that would have said rows were hidden, and the total — while x
// still removed and ctrl+e still edited the line the operator could not see.
//
// The frame rows are counted against the cart-LISTED bar (see sourceHelpText):
// that bar is the longer of the two, so the answer cannot flip back when the
// collapsed wording is folded instead.
func (s *PurchaseOrderCreateScreen) cartListedOnScreen() bool {
	if len(s.lines) == 0 || s.terminalHeight <= 0 {
		// Unsized screens get no budget at all, here as everywhere else on this
		// screen: guessing a pane height would hide rows nobody asked to hide.
		return true
	}
	left, budget := s.sourceCartSpace()
	return poRenderedRows(s.renderCart(s.reviewCursor, budget)) <= left
}

// sourceCartSpace is the rows the source chooser has left for the cart block
// and the row budget renderCart gets out of them. ONE derivation, because the
// decision to collapse and the render that follows it have to agree: measuring
// with one budget and drawing with another is how a block passes its own fit
// check and then overflows.
//
// It does not go through cartRowBudget, for two reasons. bodyRowBudget floors
// at three rows it may not have — that floor is the arithmetic that handed the
// cart room it did not own and let clampToBox eat the highlighted line — and it
// reads frameRows(), which folds helpText(), which asks this very question.
func (s *PurchaseOrderCreateScreen) sourceCartSpace() (left, budget int) {
	if s.terminalHeight <= 0 {
		return 0, 0
	}
	other := poRenderedRows(s.sourceCartChrome() + sourceCartKeys())
	left = screenBodyHeight(s.terminalHeight) - s.frameRowsWith(s.sourceHelpText(true, s.sourceAttributionShown())) - other
	budget = left - s.cartChromeRows()
	if budget < 1 {
		budget = 1
	}
	return left, budget
}

// cartHiddenSentence is what the source chooser says INSTEAD of the cart when
// the cart's rows will not fit. One sentence, because the operator must not
// have to add up a count that is drawn in one place and a caveat drawn in
// another: it names the key that opens the lines, how many are staged, what
// they come to, and that they are not listed here. The review phase really does
// list and highlight them at this pane height, so d is a route and not a hope.
//
// The KEY goes first for the reason the supplier-switch confirm puts its keys
// above its prose: this block is the LAST thing the source chooser draws, and
// on a supplier carrying agreements and associations the chooser's fixed rows
// can fill an 18-row pane on their own, so whatever is at the end of this
// sentence is what clampToBox eats. The order inside the sentence is the order
// of sacrifice: the total goes first, then the count, and the way out never.
// The frame gives up its TITLE before any of them (sourceTitleShown), so on the
// pane sizes this project checks the total is not what a short terminal takes.
// The leads are kept short for the same reason — they fold onto the
// first line ahead of the key rather than pushing it onto a second one.
//
// lead is what a declining key just did, so pressing j over a cart that is not
// on screen moves the body rather than redrawing the sentence already there.
// Every one of the four NAMES its key, because two of them used to share the
// wording: j and k both said "no line shown to move", and this frame has no
// cursor, no highlighted row and no focused textinput, so j-then-k redrew a
// byte-for-byte identical pane — the reported hang's shape, in the code written
// against it.
func (s *PurchaseOrderCreateScreen) cartHiddenSentence(lead string) string {
	total, noCost := poCartTotal(s.lines)
	money := fmtMoney(total)
	if noCost > 0 {
		// The sum is a floor: a line with no cost is priced from the supplier
		// catalog at save time, so reporting it flat would read as free.
		money = "at least " + money
	}
	// Ordered by what may be sacrificed, not shaved to fit: the key, the count
	// and "not listed" are the sentence's whole job and they lead it, so the
	// TOTAL is what falls onto a second row. It really does fall — "at least"
	// (any line priced from the catalog at save time) or a five-figure order
	// takes this past 49 cells — and the row budget counts the fold rather than
	// assuming one row, which is what the previous wording claimed and was not.
	out := fmt.Sprintf("d lists the %d line(s) · not listed here · %s", len(s.lines), money)
	if lead != "" {
		out = lead + " · " + out
	}
	return out
}

// cartHiddenNote answers j / k / x / ctrl+e on a collapsed cart: no state
// change, and the same sentence the frame already carries, now led by what the
// key did. The lead is also what turns the muted summary into a warned one, so
// the press changes the body twice over.
func (s *PurchaseOrderCreateScreen) cartHiddenNote(lead string) tea.Cmd {
	s.cartLead = lead
	n := s.cartHiddenNoteText()
	return Status(n.flash(), n.level)
}

func (s *PurchaseOrderCreateScreen) cartHiddenNoteText() pickerNote {
	level := StatusInfo
	if s.cartLead != "" {
		level = StatusWarn
	}
	return pickerNote{text: s.cartHiddenSentence(s.cartLead), level: level}
}

func (s *PurchaseOrderCreateScreen) renderSourcePhase() string {
	body := s.sourceCartChrome()
	if len(s.lines) == 0 {
		return body
	}
	if !s.cartListedOnScreen() {
		// One block: the count, the total, the "not listed" and the way out are
		// one sentence, so nothing here can be cut without the rest going with
		// it — and the reply to a declined key is a lead on that same sentence
		// rather than a second line under it.
		return body + "\n" + s.cartHiddenNoteText().render() + "\n"
	}
	_, budget := s.sourceCartSpace()
	return body + sourceCartKeys() + s.renderCart(s.reviewCursor, budget)
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

// poCartWindow picks which cart rows to draw so the frame's LAST row — on the
// review phase, the focused PO-notes input the operator is typing into — is
// still on the pane. clampToBox drops from the bottom, so an unbounded cart
// does not merely scroll off: it takes the field being typed into with it, and
// a field that is not on screen is the "the screen is not telling me what is
// happening" defect at its worst, because data is going into it.
//
// budget <= 0 means unbounded, which is what an unsized screen gets.
func poCartWindow(total, highlight, budget int) (start, end int) {
	if budget <= 0 || total <= budget {
		return 0, total
	}
	if highlight < 0 || highlight >= total {
		highlight = 0
	}
	start = highlight - budget/2
	if start < 0 {
		start = 0
	}
	end = start + budget
	if end > total {
		end = total
		start = end - budget
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

// poRenderedRows counts the terminal rows a rendered chunk occupies.
func poRenderedRows(chunk string) int {
	if chunk == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSuffix(chunk, "\n"), "\n"))
}

// frameRows is what View() draws around every phase body: the folded help line,
// its blank separator, the supplier header and its own separator, plus the
// trailing newline and the error/pending line when one is showing.
//
// It is computed from the folded help rather than assumed to be one row. The
// help line is the screen's action bar and now wraps to two or three rows on
// several phases, and every row it gained came straight out of the bottom of
// the pane.
func (s *PurchaseOrderCreateScreen) frameRows() int {
	return s.frameRowsWith(s.helpText())
}

// frameRowsWith is frameRows against a given action bar. The source chooser
// needs it: deciding whether its cart fits means measuring the chrome, and the
// chrome includes the bar whose wording that same decision changes.
func (s *PurchaseOrderCreateScreen) frameRowsWith(help string) int {
	// The failure line is MEASURED, not counted as one. It used to be reserved
	// as a single row while an OMS error body carrying newlines rendered as a
	// block of them, so every budget on this screen was computed against a line
	// count that was wrong — the same stale-constant shape as poCartChromeRows.
	return s.frameChromeRows(help) + poRenderedRows(s.renderFailLine(help))
}

// frameChromeRows is everything View draws around the body EXCEPT that trailing
// failure line: the folded help, its blank separator, the supplier header, and
// the newline after the phase body.
func (s *PurchaseOrderCreateScreen) frameChromeRows(help string) int {
	n := poRenderedRows(pickerHint(help)) + 1
	n += poRenderedRows(s.renderSupplierHeader()) + 1
	n++ // the newline View writes after the phase body
	return n
}

// bodyRowBudget is the row budget for the scrolling blocks on this screen —
// the review cart, the three pickers, the supplier list and the association
// pickers all take theirs from here. otherRows is whatever the phase draws
// around the block, measured rather than assumed.
//
// One block does its own sum and says why: the SOURCE chooser's cart, through
// sourceCartSpace. The floor below hands out three rows the pane may not have,
// which is right for a block that can be a little cramped and wrong for one
// whose highlighted row is what x and ctrl+e act on.
//
// One derivation on purpose. The first pass at this gave the list footer, the
// review cart and the picker bodies each their own hand-rolled answer, and the
// two that were not written that round went on clipping: `clampToBox` drops
// from the bottom, so every row the folded action bar gained at the TOP came
// out of whatever the frame drew LAST. A per-block constant is stale the next
// time a line is added anywhere above it.
//
// Zero means unbounded, which is what an unsized screen gets: guessing a pane
// height would hide rows nobody asked to hide.
func (s *PurchaseOrderCreateScreen) bodyRowBudget(otherRows int) int {
	if s.terminalHeight <= 0 {
		return 0
	}
	avail := screenBodyHeight(s.terminalHeight) - s.frameRows() - otherRows
	if avail < 3 {
		avail = 3
	}
	return avail
}

// cartRowBudget is bodyRowBudget with the cart's own chrome taken off —
// over-reserving one row beats clipping one, the same trade computeWindowSize
// makes on the list screens.
func (s *PurchaseOrderCreateScreen) cartRowBudget(bodyRows int) int {
	return s.bodyRowBudget(bodyRows + s.cartChromeRows())
}

// cartChromeRows is what renderCart spends on everything that is not a line:
// its header, both scroll markers, its total, and the catalog-pricing caveat as
// that caveat will ACTUALLY fold.
//
// It was the constant 5 — one row per item — which stopped being true the
// moment the caveat went through pickerHint: at 63 cells it folds to TWO rows
// whenever a line carries no cost, so the reservation was a row short. That is
// the horizontal-cut-traded-for-a-vertical-one this screen has been bitten by
// three times, and it is why nothing here is counted by hand any more.
func (s *PurchaseOrderCreateScreen) cartChromeRows() int {
	n := 4 // header, the two scroll markers, the total
	if _, noCost := poCartTotal(s.lines); noCost > 0 {
		n += poRenderedRows(pickerHint(poCartCaveat(noCost)))
	}
	return n
}

// poCartCaveat is the ONE wording of the catalog-priced caveat. The renderer
// and the row reservation both read it, so they cannot disagree about how many
// rows it takes.
func poCartCaveat(noCost int) string {
	subject := fmt.Sprintf("%d lines are", noCost)
	if noCost == 1 {
		subject = "1 line is"
	}
	return subject + " priced from the supplier catalog — not included above"
}

// renderCart lists the staged lines and the running total. When highlight >= 0
// the matching row is marked (used by the review phase and the source chooser's
// cart list); pass -1 for a plain list. The total lives here rather than in
// renderReviewPhase so both surfaces that show the cart also show what it costs.
func (s *PurchaseOrderCreateScreen) renderCart(highlight, rowBudget int) string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Cart (%d line(s))", len(s.lines))) + "\n")
	start, end := poCartWindow(len(s.lines), highlight, rowBudget)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("    ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		l := s.lines[i]
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
	if end < len(s.lines) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("    ↓ %d more below", len(s.lines)-end)) + "\n")
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
			b.WriteString(pickerHint(poCartCaveat(noCost)) + "\n")
		}
	}
	return b.String()
}

// reviewTail is everything the review phase draws UNDER the cart, ending in the
// focused PO-notes input. Built before the cart so the cart can be windowed
// against what is left after it: that last row is the one an unbounded cart
// used to push off the bottom of the pane, and an operator typing into a field
// that is not on screen is the worst form of this screen's defect.
//
// attribution repeats the agreement and the order-level associations beside the
// cart they apply to. Review is the confirm-before-submit surface and a long
// cart can push the header line well off the top, so "(none)" is worth showing
// as readily as a name — but they are a REPEAT, which makes them the rows that
// yield when the pane cannot hold the cart's lines and the notes field too.
func (s *PurchaseOrderCreateScreen) reviewTail(attribution bool) string {
	var tail strings.Builder
	rows := s.renderAgreementRow(false) + s.renderAssocRows(false)
	switch {
	case rows == "":
	case attribution:
		tail.WriteString(rows)
	default:
		// One row, worded like the source chooser's: at 45 cells it cannot fold
		// and take back the row it was dropped to free.
		tail.WriteString(pickerHint("agreement / association rows need more height") + "\n")
	}
	tail.WriteString("\n")
	tail.WriteString("▸ " + StyleTitle.Render("PO notes: ") + s.poNotes.View() + "\n")
	return tail.String()
}

// reviewCartSpace is the rows left for the cart's LINES once the frame and the
// tail are drawn. Computed here rather than through cartRowBudget for the
// reason sourceCartSpace gives: bodyRowBudget floors at three rows it may not
// have, and on this phase the row that floor spends is the focused PO-notes
// input. Zero or less means the cart cannot draw a line at this size.
func (s *PurchaseOrderCreateScreen) reviewCartSpace(tail string) int {
	return screenBodyHeight(s.terminalHeight) - s.frameRows() - poRenderedRows(tail) - s.cartChromeRows()
}

// reviewAttributionShown reports whether the review tail repeats the agreement
// and association rows in full. Three repeated rows and a caveat folded onto
// two were enough to leave the cart no line at all at 80x24, and what fell off
// the bottom of the pane was the notes field being typed into — so the REPEAT
// is what gives, the same order of sacrifice the source chooser keeps.
func (s *PurchaseOrderCreateScreen) reviewAttributionShown() bool {
	if s.terminalHeight <= 0 {
		return true
	}
	return s.reviewCartSpace(s.reviewTail(true)) >= 1
}

func (s *PurchaseOrderCreateScreen) renderReviewPhase() string {
	tail := s.reviewTail(s.reviewAttributionShown())
	if s.terminalHeight <= 0 {
		// Unsized screens get no budget at all, here as everywhere else.
		return s.renderCart(s.reviewCursor, 0) + tail
	}
	budget := s.reviewCartSpace(tail)
	if budget < 1 {
		budget = 1
	}
	return s.renderCart(s.reviewCursor, budget) + tail
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
	b.WriteString(pickerHint("Line source: "+source) + "\n\n")
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
				b.WriteString(pickerHint(hint) + "\n")
			}
		}
	}
	// Catalog lines: the cost is an optional override, so say what a blank field
	// does. (Case-packed lines also show the unit/case derivation hint above.)
	if s.pickedItemSup != nil {
		b.WriteString(pickerHint("Cost is optional — blank uses the supplier catalog price.") + "\n")
	}
	// Asset / freeform lines carry no date field — say why, so its absence
	// doesn't read as an oversight.
	if !s.lineTakesDate() {
		b.WriteString(pickerHint("Expected dates are stored on inventory lines only; set this line's dates at send/receive.") + "\n")
	}
	return b.String()
}
