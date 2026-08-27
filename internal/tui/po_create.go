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
//	                    A case-packed inventory line (qpp > 1) is ENTERED in
//	                    CASES at a CASE COST — one basis for both typed rows,
//	                    ctrl+t flips it — and the payload carries the base
//	                    quantity and per-base-unit cost derived from them
//	                    (po_case_entry.go carries the whole note — op-7j8v).
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
	// nothing else. Split, the headline goes on the layer's status row and
	// never gives; the body is what a short pane sacrifices (failLines).
	// Always set through setErr, never separately.
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
	itemSuppliersCursor int
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

	// The ENTRY BASIS for the Phase-4 form. When a picked inventory line is
	// case-packed (pickedQPP > 1) the operator enters CASES and a CASE COST,
	// and the payload carries the derived base quantity (cases × qpp) and
	// per-base-unit cost (case ÷ qpp). caseBasis selects which basis both typed
	// rows are on — one basis for the line, never one row per denominator, or
	// the pane is asking for two different things side by side (po_case_entry.go
	// carries the whole note). pickedQPP is 0/1 for every non-case line (asset,
	// freeform, single-pack inventory), which leaves those exactly as they were.
	pickedQPP int
	caseBasis bool

	// Multi-line cart. Each entered line is staged here; the whole cart is
	// POSTed once from the review phase. reviewCursor highlights a line for
	// edit/remove and is shared by the review cart AND the source chooser's
	// cart list, so a line highlighted in one is still highlighted in the
	// other; poNotes is the PO-level notes field.
	lines        []poCartLine
	reviewCursor int
	poNotes      textinput.Model
	// sourceNote is the source chooser's own answer to the last keypress, in
	// the screen BODY rather than only in a four-second flash. It replaces the
	// two leads this phase used to keep (cartLead / attrLead) and the two
	// sentences rebuilt around them: those existed because the cart's rows and
	// the optional attribution rows could be OFF the pane while their keys were
	// still bound, and neither state survives the conversion — the window
	// anchors on the cursor's block, so the highlighted line is always drawn,
	// and G/W/C name a phase rather than a row.
	sourceNote pickerNote
	// switchScroll is where the supplier-switch confirm's read-only prose is
	// scrolled to. frameScrolled hands back the offset it actually drew and
	// this stores it, so a terminal dragged short and grown again comes back
	// where the operator left it.
	switchScroll int
	// switchLead is the same shape for the supplier-switch confirm, which binds
	// exactly two keys and had NOTHING to say to any other press. enter is the
	// one that matters: it is the key that opened this frame, so a reflexive
	// double-tap landed on a pure function of unchanged state and redrew a
	// byte-for-byte identical pane — the reported hang, on a destructive
	// confirm. The answer is a note, not a binding: see updateSupplierSwitchPhase.
	switchLead string
	// pendingLead is what a key that the submit has made inert just did. The
	// review bar drops `enter submit` while the POST is out, and the press
	// itself answers where the operator watching a slow request can still see
	// it, rather than into a flash they will have missed.
	//
	// It is one of the two writers of the screen's ANSWER (answerNote), and it
	// can be the FIRST of them because it is non-empty only while the POST is
	// out: pendingDecline writes it, and finalize, poCreatedMsg and every phase
	// change clear it. That is the one window in which the phase's own note is
	// guaranteed stale, since every arm that could refresh it is frozen.
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

	// jdeScreen is the pane geometry and the framing every columnar screen
	// shares: how many rows the body gets under the bar that is about to be
	// drawn, whether that body MOVES, the bounded status row, and the frames
	// themselves. It replaces the hand-rolled row budgets this screen used to
	// carry — frameRows / frameChromeRows / bodyRowBudget / cartRowBudget /
	// sourceCartSpace / reviewCartSpace, six answers to one question — and the
	// sacrifice order that rode on them (sc-jde-poc). What a short pane gives
	// up is now said with jdeHeadRank, beside the row, once.
	jdeScreen
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
	desc.CharLimit = 200
	desc.Width = poFieldWidth
	s.lineInputs[poLineFieldDesc] = desc

	qty := textinput.New()
	qty.Prompt = ""
	qty.CharLimit = 10
	qty.Width = poFieldWidth
	s.lineInputs[poLineFieldQty] = qty

	cost := textinput.New()
	cost.Prompt = ""
	cost.CharLimit = 20
	cost.Width = poFieldWidth
	s.lineInputs[poLineFieldCost] = cost

	date := textinput.New()
	date.Prompt = ""
	date.CharLimit = 12
	date.Width = poFieldWidth
	s.lineInputs[poLineFieldDate] = date

	// PO-level notes, captured once in the review phase.
	poNotes := textinput.New()
	poNotes.Prompt = ""
	poNotes.CharLimit = 500
	poNotes.Width = poFieldWidth
	s.poNotes = poNotes

	// Picker search inputs (Phase 3b/3c).
	is := textinput.New()
	is.Prompt = ""
	is.CharLimit = 60
	is.Width = poFieldWidth
	s.itemSuppliersSearch = is

	as := textinput.New()
	as.Prompt = ""
	as.CharLimit = 60
	as.Width = poFieldWidth
	s.assetsSearch = as

	return s
}

// poNotesLabel is the PO-level notes row's label. It is a jdeField Label now,
// so the layer right-aligns it into the shared column and sizes the box that
// hangs off it — the local poInputWidth / poLineRowPrefix pair that used to
// measure a hand-drawn prefix against a fixed 51 columns is gone with the rest
// of the pane-local layer (sc-jde-poc).
const poNotesLabel = "PO notes"

// poItemFilterLabel / poAssetSearchLabel are the two picker search rows'
// labels, on the same terms.
const (
	poItemFilterLabel  = "Filter"
	poAssetSearchLabel = "Search"
)

// poFieldWidth is the input area every typed row on this screen asks for. The
// layer shrinks it to what the pane really has (jdeFitRow) and bounds the value
// inside it (jdeFitInputValue), so this is a preference rather than a promise —
// which is the whole difference from the fixed width poInputWidth used to
// compute against a pane it had guessed at.
const poFieldWidth = 30

// paneWidth is the cells this screen's body actually has on THIS terminal, and
// is what every value the screen CLIPS is measured against.
//
// The distinction it draws is between the two things a bound can do. FOLDING a
// hint at 51 columns on a wider terminal costs an extra line and loses nothing.
// CLIPPING a value at 51 on a 91-cell pane DESTROYS the tail of a catalog name
// the terminal had room to draw, on the rows an operator picks from. 80 columns
// is the width that must hold; it is not the width to render as though we had.
//
// Unsized (a screen driven straight in a unit test, or before Root's first
// WindowSizeMsg) falls back to the 80-column pane, the narrowest supported —
// the layer's own bodyWidth answers 0 there, which every bound in jde_form.go
// reads as "do not truncate", and a row this screen CLIPS has to be clipped
// against something. Same shape as po_add_line.go's paneWidth.
func (s *PurchaseOrderCreateScreen) paneWidth() int {
	if w := s.bodyWidth(); w > 0 {
		return w
	}
	return pickerPaneWidth
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
		s.setSize(m)
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
		// The lead is what a key THE SUBMIT MADE INERT just did, so it dies
		// with the submit. Nothing draws it after this today — the phases that
		// set one have no working sentence of their own — but that is geometry
		// rather than a guarantee, and a lead outliving its submit would name a
		// key against a sentence it was never pressed on.
		s.pendingLead = ""
		if m.err != nil {
			// Headline + detail, because the detail is a whole OMS response
			// body: failLines cuts it to poFailDetailRows before folding it, so
			// what the operator loses on a short terminal is the tail of the
			// gateway's HTML and never the sentence naming what failed — that
			// one rides on the status row, which never gives.
			s.setErr(poSubmitFailWords, m.err.Error())
			if s.phase == poPhaseReview {
				// The freeze is over, so the field takes input again. Only on
				// review: the operator may have escaped to the source chooser
				// while the POST was out, and focusing a field that phase does
				// not draw would send its keystrokes nowhere.
				s.poNotes.Focus()
				return s, tea.Batch(
					Status("create PO failed: "+m.err.Error(), StatusError),
					textinput.Blink,
				)
			}
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
		// The phase the key was pressed ON, captured before any arm can change
		// it, so the block below can tell whether this press navigated. What it
		// does with that answer, and why it is done there, is on that block.
		before := s.phase
		// The pinned header is measured ONCE, on the frame the key was pressed
		// against, and handed down. An arm can retire a note or a failure
		// detail mid-dispatch, and the header's height is what decides how many
		// rows the body gets and therefore whether PgUp/PgDn are named at all —
		// so a press judged against a header re-derived after the arm ran would
		// be judged against a frame the operator never saw. Same shape, same
		// reason, as receive_form's barFor / handleKey pair.
		headerRows := len(s.headerLines())
		var next Screen
		var cmd tea.Cmd
		handled := true
		switch s.phase {
		case poPhaseSupplier:
			next, cmd = s.updateSupplierPhase(m, headerRows)
		case poPhaseSupplierSwitch:
			next, cmd = s.updateSupplierSwitchPhase(m, headerRows)
		case poPhaseAgreement:
			next, cmd = s.updateAgreementPhase(m, headerRows)
		case poPhaseWorkOrder:
			next, cmd = s.updateWorkOrderPhase(m, headerRows)
		case poPhaseCommittee:
			next, cmd = s.updateCommitteePhase(m, headerRows)
		case poPhaseSource:
			next, cmd = s.updateSourcePhase(m, headerRows)
		case poPhaseReorderPick:
			next, cmd = s.updateReorderPickPhase(m, headerRows)
		case poPhaseItemPick:
			next, cmd = s.updateItemPickPhase(m, headerRows)
		case poPhaseAssetPick:
			next, cmd = s.updateAssetPickPhase(m, headerRows)
		case poPhaseLine:
			next, cmd = s.updateLinePhase(m, headerRows)
		case poPhaseReview:
			next, cmd = s.updateReviewPhase(m, headerRows)
		default:
			handled = false
		}
		if handled {
			if s.phase != before {
				// A lead NAMES a key, and the key it names belongs to the frame
				// it was pressed on — "ctrl+x removes nothing" on the source
				// chooser advertises a key the supplier picker does not bind.
				// Cleared HERE, at the one place every phase change passes
				// through, rather than in the arms that navigate: a fourth
				// added later would have to remember, and enumerating the sites
				// is the mistake this screen has made everywhere else it
				// appeared.
				s.pendingLead = ""
				s.sourceNote.clear()
				// The ORDER-LEVEL failure goes with it, and that is not
				// tidiness: it is what keeps statusPlan's residual momentary.
				// While an errMsg stands the status row is the error alone, so
				// a picker's ANSWER falls back to a trimmable header row — off
				// the pane at 80x11 through 80x13, which is the byte-identical
				// frame this screen was reported for. Left standing it never
				// cleared: setErr's only other writers are enterLinePhase,
				// removeLineAt and addReorderLines, so one failed submit put
				// every later phase — the chooser, both pickers, the line
				// form — permanently back into that state. The failure belongs
				// to the frame it happened on; the operator who walks away has
				// read it or has not.
				s.setErr("", "")
			}
			return next, cmd
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

func (s *PurchaseOrderCreateScreen) updateSupplierPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if !s.supplierListOnScreen() {
		// No rows on the pane. The bar names esc and nothing else here, so
		// every other key DECLINES and says why — each naming itself, because
		// this frame has no highlight and no focused box, so two keys sharing
		// one sentence would redraw the pane the first press left.
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSPurchasing, nil)
		case "up", "down", "pgup", "pgdown":
			return s, s.supplierVerdictNote(m.String() + " moves nothing")
		case "enter":
			return s, s.supplierVerdictNote("nothing to commit")
		}
		return s, nil
	}
	if s.pending {
		// Same default-deny as the source chooser, one phase further out:
		// review → esc → sources → esc reaches this picker with the POST still
		// in flight, and committing a DIFFERENT supplier would re-target a
		// request that already names the old one.
		//
		// What the freeze may not do is take the way back. This phase binds no
		// `b`, and its esc leaves the SCREEN — so freezing enter outright left
		// the operator who wandered here with nothing but the wait or throwing
		// the outcome away. Enter on the row the order ALREADY carries commits
		// nothing (commitSupplier returns early on the same id) and only sets
		// the phase back to the source chooser, so it is navigation and stays
		// live; the bar names it for precisely that row.
		switch m.String() {
		case "esc", "up", "down", "pgup", "pgdown":
		case "enter":
			if !s.supplierHighlightIsCommitted() {
				return s, s.pendingDecline("enter commits nothing · the committed row goes back")
			}
		default:
			return s, s.pendingDecline(m.String() + " waits for the submit")
		}
	}
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
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
			s.switchScroll = 0
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

// supplierHighlightIsCommitted reports whether the highlighted row is the
// supplier the order already carries, so enter would commit nothing and merely
// return to the source chooser. ONE predicate, read by the arm that decides
// whether the freeze applies and by the bar that says so, because two answers
// to the same question are how a bar comes to name a key that declines.
func (s *PurchaseOrderCreateScreen) supplierHighlightIsCommitted() bool {
	return s.supplierCursor >= 0 && s.supplierCursor < len(s.suppliers) &&
		s.suppliers[s.supplierCursor].ID == s.supplierID
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
func (s *PurchaseOrderCreateScreen) updateSupplierSwitchPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
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
	case "up", "down":
		// The prose is a READ-ONLY body, so these scroll it rather than moving
		// a cursor — and they are named on the bar for exactly as long as the
		// layer says the body moves. Without that gate they would be a key the
		// bar names doing nothing on every frame the confirm fits on.
		if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
			// Refused pane: no prose is drawn, so there is nothing to scroll
			// and nothing to say about not scrolling it — see moveCursor.
			return s, nil
		}
		if s.bodyPagesFor(headerRows) {
			delta := 1
			if m.String() == "up" {
				delta = -1
			}
			s.switchScroll += delta
			if s.switchScroll < 0 {
				s.switchScroll = 0
			}
			return s, nil
		}
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

// supplierSwitchNote answers a key this frame does not bind: what the key DID,
// and nothing else.
//
// It used to repeat the confirm's two live keys, read off a supplierSwitchBar
// the frame also printed, so that a decline could not name a key the confirm
// does not honour. The action bar makes that claim now — Ctrl-X and Esc, drawn
// on every frame at the bottom of the pane, where they cannot be trimmed — so
// repeating them here would be two statements of one claim, which on this
// screen is how a bar and a body line came to contradict each other about
// `esc` twice.
func (s *PurchaseOrderCreateScreen) supplierSwitchNote() pickerNote {
	if s.switchLead == "" {
		return pickerNote{}
	}
	return pickerNote{text: s.switchLead, level: StatusWarn}
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
	s.itemSuppliersCursor = 0
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

func (s *PurchaseOrderCreateScreen) updateAgreementPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	switch m.String() {
	case "esc":
		// Leave the pick as it was — esc is "I'm done looking", not "clear it".
		// Row 1 is the explicit way to clear, which the header says.
		s.phase = poPhaseSource
		return s, nil
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

func (s *PurchaseOrderCreateScreen) updateWorkOrderPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	rows := s.workOrderRows()
	switch m.String() {
	case "esc":
		s.phase = poPhaseSource
	case "enter":
		if s.workOrderCursor >= 0 && s.workOrderCursor < len(rows) {
			s.workOrderID = rows[s.workOrderCursor].value
		}
		s.phase = poPhaseSource
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) updateCommitteePhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	rows := s.committeeRows()
	switch m.String() {
	case "esc":
		s.phase = poPhaseSource
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

func (s *PurchaseOrderCreateScreen) updateSourcePhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if s.pending {
		// DEFAULT-DENY while the create POST is out. The submit is reachable
		// from here — esc off the review phase lands on this chooser with the
		// request still out — and every key that stages, removes, edits or
		// re-attributes a line is changing a cart finalize() has already copied
		// into the payload; the 201 navigates away and takes the change with
		// it.
		//
		// The ALLOW-LIST is the gate, not a list of frozen keys. Written the
		// other way round it froze the nine keys somebody thought of and let
		// `d` through, which then re-focused the notes field the submit had
		// just blurred — the arm added after the freeze was free by default.
		// Anything not named here declines, including a key this phase does not
		// bind at all: the operator learns the submit owns the screen, which is
		// true of every key on it.
		switch m.String() {
		case "esc", "d", "up", "down", "pgup", "pgdown":
			// Leave, review the cart, or move a highlight through it. None of
			// them touches the payload or focuses an input.
		case "r", "i", "a", "f":
			return s, s.pendingDecline(m.String() + " adds nothing")
		case "ctrl+x":
			return s, s.pendingDecline("ctrl+x removes nothing")
		case "ctrl+e":
			return s, s.pendingDecline("ctrl+e edits nothing")
		case "g", "w", "c":
			return s, s.pendingDecline(m.String() + " changes nothing")
		default:
			return s, s.pendingDecline(m.String() + " waits for the submit")
		}
	}
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	switch m.String() {
	case "esc":
		// Back to the supplier picker — the operator can change their mind
		// before committing to any source. The pick stays committed so they do
		// not have to re-pick if they only want a different supplier.
		s.phase = poPhaseSupplier
		return s, nil
	case "r":
		s.phase = poPhaseReorderPick
		if s.reorderLoading {
			// Same guard as 'i' below. B out of a picker is not gated on the
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
			// clear the search box first, so i → / → type → b → i mid-walk
			// threw away the query the operator had typed with nothing on the
			// pane saying it had gone.
			return s, s.catalogVerdictNote("")
		}
		s.itemSuppliersSearch.SetValue("")
		s.itemSuppliersSearch.Blur()
		s.itemSuppliersTyping = false
		s.itemSuppliersCursor = 0
		if s.catalogAnswered() {
			// Already held for THIS supplier. Showing a working line here would
			// be the same rule broken from the other side: that line is a claim
			// that work is happening, and on a ten-line order it would be
			// claimed ten times over one catalog fetched once. Open on the
			// rows; R goes and asks again.
			s.applyItemSupplierFilter()
			return s, s.itemPickEntryNote()
		}
		s.itemSuppliersLoad = true
		s.itemSuppliersErr = ""
		s.itemSuppliersNote.clear() // the working line speaks for this one
		return s, s.loadItemSuppliersForSupplier()
	case "a":
		s.phase = poPhaseAssetPick
		if s.assetsLoading {
			// Two lookups for the SAME supplier are still wrong even though the
			// reply carries a generation and the stale one is dropped: the
			// operator would be watching a working frame whose answer is thrown
			// away, and the second request is work nobody asked for. Gating the
			// search box closed that race only from inside the picker.
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
		// Freeform: straight to the line form with nothing pre-filled.
		s.enterLinePhase(nil, nil, "", 0, 0, 0, 0)
		return s, textinput.Blink
	case "g":
		// Optional purchase/pricing agreement. The bar names G only once there
		// is something behind it, so this arm is only ever reached by a key the
		// bar named — and it no longer has to ask whether the ROW is on the
		// pane, because the row is a label and the key is named on the bar.
		if !s.agreementOffered() {
			return s, nil
		}
		return s, s.enterAgreementPhase()
	case "w":
		if !s.assoc.workOrdersOffered() {
			return s, nil
		}
		return s, s.enterWorkOrderPhase()
	case "c":
		if !s.assoc.committeesOffered() {
			return s, nil
		}
		return s, s.enterCommitteePhase()
	case "d":
		// Done adding lines → review + submit. Only meaningful once the cart
		// has at least one line (the backend rejects an empty PO), and the bar
		// names D for exactly that long.
		if len(s.lines) == 0 {
			return s, s.sourceNote.say("d reviews nothing · the cart is empty", StatusWarn)
		}
		s.phase = poPhaseReview
		s.clampReviewCursor()
		if s.pending {
			// D is on the frozen allow-list because reviewing the cart reads
			// it; focusing the notes would be the screen inviting input the
			// pending arm is going to refuse, which is the caret finalize()
			// blurred for exactly that reason.
			s.poNotes.Blur()
			return s, nil
		}
		s.poNotes.Focus()
		return s, textinput.Blink
	case "ctrl+x":
		// Remove the highlighted line. The cursor follows each add (addLine
		// parks it on the new line), so with an untouched highlight this is
		// still the "undo the line I just added" it always was — but UP/DN can
		// aim it at any line in the cart, and the window keeps whatever it is
		// aimed at ON the pane, which is what retired the four gates this arm
		// and its three neighbours used to carry.
		//
		// Ctrl-X rather than the bare `x` this phase used to bind: the review
		// cart already spends that chord on the same act, and one screen
		// binding two different keys to "remove the highlighted line" is a
		// difference the operator has to learn for nothing.
		if len(s.lines) == 0 {
			return s, s.sourceNote.say("ctrl+x removes nothing · the cart is empty", StatusWarn)
		}
		s.removeLineAt(s.reviewCursor)
		return s, nil
	case "ctrl+e":
		// Edit the highlighted line in place without a detour through review.
		// Same handler the review cart uses; saving or cancelling comes back
		// here (editReturn) so the operator keeps adding lines where they left
		// off.
		if len(s.lines) == 0 {
			return s, s.sourceNote.say("ctrl+e edits nothing · the cart is empty", StatusWarn)
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
	// The staged line holds BASE units and a per-BASE-unit cost, which is what
	// the payload carries; enterLinePhase converts both back to the basis the
	// form opens at. packageCost = 0 is deliberate: the case cost is re-derived
	// as unit_cost × qpp, which round-trips exactly back through
	// poDeriveUnitCost on save, whereas the catalog's own package_cost could
	// differ from the price the operator actually staged. The line's source
	// (item-supplier / asset / freeform) is preserved — editing never re-targets
	// a line.
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
//
// Either both pointers are nil (freeform), or exactly one is set. `qty` is
// always in BASE units — it is what the wire carries and what every caller
// holds — and this function converts it to the basis the form opens at. unitCost
// and packageCost prefill the cost field (0 = leave blank); qpp is the picked
// inventory line's quantity_per_package (>1 ⇒ case-packed).
//
// It resets editIndex to -1 (add mode) on every entry — the single choke point
// every add path funnels through — so a stale edit target can never redirect a
// later add into an in-place replace; the review-cart edit path re-sets
// editIndex to the target line after calling this.
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
	// Only an inventory line has a supplier case at all: an asset and a freeform
	// line name no catalogue row, so there is nothing declaring what a package
	// of them is.
	s.caseBasis = itemSupplierID != nil && poOpensAtCaseBasis(qty, qpp)

	for i := range s.lineInputs {
		s.lineInputs[i].SetValue("")
		s.lineInputs[i].Blur()
	}
	s.lineInputs[poLineFieldDesc].SetValue(desc)
	if qty > 0 {
		shown := qty
		if s.caseBasis {
			shown, _ = poWholeCases(qty, qpp)
		}
		s.lineInputs[poLineFieldQty].SetValue(strconv.Itoa(shown))
	}

	switch {
	case s.caseBasis:
		// Case-packed inventory line: the operator enters what the vendor sells
		// them, so the cost row is prefilled per CASE — the saved package_cost,
		// or unit_cost × qpp when package_cost is blank. Left blank when the
		// catalog carries neither, in which case no unit_cost is submitted and
		// the backend derives the line cost from the stored unit_cost (sc-5yr).
		//
		// The fallback multiplies through poCaseCostFrom rather than in
		// float64: 0.05 × 12 is 0.6000000000000001 in binary, which the box
		// then shows at full precision, and this is the figure the operator is
		// asked to confirm as what the vendor charges.
		switch {
		case packageCost > 0:
			s.lineInputs[poLineFieldCost].SetValue(poFormatCost(packageCost))
		case unitCost > 0:
			s.lineInputs[poLineFieldCost].SetValue(
				poCaseCostFrom(poFormatCost(unitCost), unitCost, qpp))
		}
	default:
		// Per-unit cost prefill. Asset / freeform lines start blank (nothing
		// knows their cost); a single-pack catalog line is seeded with the price
		// the picker showed — the item-supplier's unit_cost, or the override
		// already staged on the line when re-opened with ctrl+e — so the
		// operator can keep it, change it, or clear it back to catalog pricing
		// (sc-gnzw).
		//
		// package_cost wins here too, divided back down THROUGH THE EXACT
		// HELPER. It is the figure the vendor charges and the one OMS derives
		// unit_cost FROM, rounding to two decimals as it goes
		// (backend/inventory/models/core.py), so sourcing the per-unit price
		// from unit_cost feeds that rounding back in: a 10.00 case of 3 comes
		// back as 3.33, and three of them are 9.99. Doing it only on the case
		// branch would leave the same queue row priced two ways depending on
		// whether its suggestion happened to be a whole number of cases.
		//
		// The poCasePacked gate is a DEFENSIVE GUARD, not a fix for anything
		// observed: it fixes the denominator this division needs to a size the
		// row actually STATED, so a package_cost arriving beside no case size
		// could never be prefilled into a row labelled Unit cost — which is the
		// captain's original defect. No wire shape reaches it today
		// (ItemSupplier.quantity_per_package is a non-null PositiveIntegerField
		// defaulting to 1 with MinValueValidator(1), and reorder_data always
		// emits the key), and at qpp 1 the two figures are equal by
		// construction, so the guard costs nothing and claims nothing.
		switch {
		case packageCost > 0 && poCasePacked(qpp):
			s.lineInputs[poLineFieldCost].SetValue(
				poUnitCostFrom(poFormatCost(packageCost), packageCost, qpp))
		case unitCost > 0:
			s.lineInputs[poLineFieldCost].SetValue(poFormatCost(unitCost))
		}
	}
	s.lineInputs[s.lineFocused].Focus()
}

func (s *PurchaseOrderCreateScreen) updateLinePhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch m.String() {
	case "tab", "down":
		// Tab / Shift-Tab ride alongside UP/DN on a sheet with fields and are
		// deliberately NOT named: roughly twenty converted forms name that pair
		// as UP/DN=Fields alone, and naming the alias here would make this
		// sheet disagree with every other one (poFormNavAliases).
		//
		// All four WRAP, which is why they are answered here rather than left
		// to moveCursor: the shared cursor clamps (jdeClampPick), and clamping
		// a four-field form means Down on the last row blurs and re-focuses the
		// same field — no state change, no note, and the bar naming UP/DN at
		// that moment. A list is the other case and keeps the clamp: running
		// off the bottom of one must not reappear at the row that CLEARS a
		// field. po_add_line's price rows wrap for the same reason.
		s.focusNextLine(1, headerRows)
		return s, nil
	case "shift+tab", "up":
		s.focusNextLine(-1, headerRows)
		return s, nil
	}
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
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
		s.caseBasis = false
		s.editIndex = -1
		if editing {
			return s, s.returnFromLineForm()
		}
		s.phase = poPhaseSource
		return s, nil
	case "ctrl+t":
		// Move BOTH typed rows between cases and base units on a case-packed
		// inventory line. A chord, not a bare letter, so the key never collides
		// with typing into a focused field — and gated on the same predicate
		// the bar reads, so it cannot act where the bar left it unnamed nor be
		// named where it would only decline.
		if !s.caseFlipOffered() {
			return s, nil
		}
		return s, s.toggleEntryBasis()
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

// poLineFieldLabel maps a field index to its static form label. The two TYPED
// rows do not have one: their label is the basis they are being entered at, and
// s.lineFieldLabel answers for both (po_case_entry.go). The unit-basis spellings
// are kept here so a caller with no screen — and the label-column measurement —
// still has the wider of the two to size against.
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

// lineFieldLabel is the label the Phase-4 form DRAWS for field i, which for the
// quantity and cost rows is decided by the line's entry basis.
//
// One function for both rows rather than one per row, because the two labels
// have to move together: a form reading "Cases" beside "Unit cost" is asking
// for two different denominators at once, which is the reported defect with the
// halves swapped rather than the defect fixed.
func (s *PurchaseOrderCreateScreen) lineFieldLabel(i int) string {
	switch i {
	case poLineFieldQty:
		return poQtyRowLabel(s.caseBasis)
	case poLineFieldCost:
		return poCostRowLabel(s.caseBasis)
	}
	return poLineFieldLabel(i)
}

// costFieldLabel is the cost row's label alone, kept as the name the bar and
// the error wording read.
func (s *PurchaseOrderCreateScreen) costFieldLabel() string {
	return poCostRowLabel(s.caseBasis)
}

// focusNextLine moves the line form's focus by delta, WRAPPING at either end,
// and it BLURS on the way past: a caret left in a field the cursor has moved
// off is the screen taking input into a row it is not drawing as active. It is
// the only thing that moves this form's focus — the shared setCursorRow carries
// no line-form branch, because neither of the paths that used to reach it does
// so any more.
func (s *PurchaseOrderCreateScreen) focusNextLine(delta, headerRows int) {
	fields := s.lineFields()
	cur := 0
	for i, f := range fields {
		if f == s.lineFocused {
			cur = i
			break
		}
	}
	next, ok := s.moveRow(cur, len(fields), delta, headerRows, s.barFor(headerRows))
	if !ok {
		return
	}
	s.lineInputs[s.lineFocused].Blur()
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
		s.setErr("description is required", "")
		return Status(s.errMsg, StatusError)
	}
	// The quantity row is read at the basis it was TYPED at and converted to
	// the base units the wire carries (po_case_entry.go). On a case-packed line
	// at case basis "2" is two of the vendor's cases, not two loose units, and
	// the payload has to say so or the order is short by the case size.
	entered, err := strconv.Atoi(strings.TrimSpace(s.lineInputs[poLineFieldQty].Value()))
	if err != nil || entered <= 0 {
		s.setErr(strings.ToLower(s.lineFieldLabel(poLineFieldQty))+" must be a positive integer", "")
		return Status(s.errMsg, StatusError)
	}
	qty := poBaseQuantity(entered, s.pickedQPP, s.caseBasis)

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
		s.lineInputs[poLineFieldCost].Value(), s.caseBasis, s.pickedQPP,
	)
	if err != nil {
		s.setErr(strings.ToLower(s.costFieldLabel())+" must be a non-negative number", "")
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
				s.setErr("date must be YYYY-MM-DD or blank", "")
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
		s.caseBasis = false
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
	s.caseBasis = false
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
// cost and is divided by qpp; otherwise it is already a per-unit cost. provided
// is false (with a nil error) when the field is blank, signalling the caller to
// omit unit_cost so the backend derives it from the stored catalog cost. A
// non-numeric or negative entry returns errInvalidCost.
//
// The division goes through poUnitCostValue, so it is the SAME arithmetic the
// box the operator is looking at was filled by: exact in decimal, quantised at
// the four-place column OMS stores. Divided in float64 the payload and the row
// could disagree — 10.00 over 3 posts 3.3333333333333335 while the box, after
// a Ctrl-T, showed something else — and a screen that says one price while the
// order records another is the defect this whole file exists to remove.
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
		return poUnitCostValue(raw, v, qpp), true, nil
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

// caseFlipOffered is the ONE predicate the Ctrl-T arm and the bar both read, so
// the key cannot be named over a state it would decline in nor act in a state
// the bar left it out of.
func (s *PurchaseOrderCreateScreen) caseFlipOffered() bool {
	if s.pickedItemSup == nil || !poCasePacked(s.pickedQPP) {
		return false
	}
	if s.caseBasis {
		return true // cases → units always converts exactly
	}
	return poFlipsToCases(s.lineInputs[poLineFieldQty].Value(), s.pickedQPP)
}

// toggleEntryBasis flips the Phase-4 form between per-case and per-unit entry
// for a case-packed inventory line (a no-op for any other line), moving BOTH
// typed rows and both labels together.
//
// Both values are converted so the order is preserved: quantity by the case
// size, price in exact DECIMAL arithmetic (po_case_entry.go's poUnitCostFrom /
// poCaseCostFrom) quantised at the four-place column unit_cost_ordered stores,
// so an odd case size settles on the figure the order will really carry rather
// than drifting into binary noise the box then truncates. Nothing on this screen carries a
// placeholder — a placeholder fills the columnar input area and hides the
// underscored run that says the field is EMPTY — so what each row is asking for
// is carried by its basis-aware LABEL (lineFieldLabel) and by the derivation
// under the cost row, both of which follow the flip.
//
// Units → cases is not always available: a quantity that is not a whole number
// of cases has no case count, and inventing one by rounding would enlarge an
// order behind the operator. That is a BAR GATE (caseFlipOffered) rather than a
// refusal — the key is not named while it could only decline — and the
// derivation under the cost row says, standing, that the quantity is not a
// whole number of cases and which two figures are.
func (s *PurchaseOrderCreateScreen) toggleEntryBasis() tea.Cmd {
	if !s.caseFlipOffered() {
		return nil
	}
	qtyRaw := strings.TrimSpace(s.lineInputs[poLineFieldQty].Value())
	if n, err := strconv.Atoi(qtyRaw); err == nil && n > 0 {
		if s.caseBasis {
			n = poBaseQuantity(n, s.pickedQPP, true) // cases → units
		} else {
			n, _ = poWholeCases(n, s.pickedQPP) // units → cases
		}
		s.lineInputs[poLineFieldQty].SetValue(strconv.Itoa(n))
	}
	costRaw := strings.TrimSpace(s.lineInputs[poLineFieldCost].Value())
	if v, err := strconv.ParseFloat(costRaw, 64); err == nil && costRaw != "" {
		if s.caseBasis {
			s.lineInputs[poLineFieldCost].SetValue(poUnitCostFrom(costRaw, v, s.pickedQPP))
		} else {
			s.lineInputs[poLineFieldCost].SetValue(poCaseCostFrom(costRaw, v, s.pickedQPP))
		}
	}
	s.caseBasis = !s.caseBasis
	unit := "units"
	if s.caseBasis {
		unit = "cases"
	}
	// A flash only: the two LABELS change on the pane, which is a keypress
	// answering itself where the operator is already looking, and the standing
	// note row belongs to the fact about the line rather than to the last key.
	return Status("both rows are now in "+unit+" · "+poPackFact(s.pickedQPP), StatusOK)
}

// entryDerivation is the muted line drawn under the cost field: the line
// spelled out in BOTH bases and what it comes to, so a mis-keyed case count or
// a case price typed into a unit row is visible on the pane BEFORE the line is
// staged rather than on the invoice.
//
// It names no key. It used to end "· ctrl+t: toggle unit/case basis", which the
// action bar says — with the label the row's own state decides — on every frame
// of this phase; two statements of one claim on one pane is how this screen
// came to advertise and refuse the same keys.
//
// The empty-row wordings are what the rows are ASKING for, because with no
// number typed there is nothing to derive and a blank line under a blank field
// would leave the basis stated only by the label.
func (s *PurchaseOrderCreateScreen) entryDerivation() string {
	// An asset or freeform line names no catalogue row, so it has no supplier
	// case and nothing to convert — only the total, which is not a case-only
	// question and is drawn for every line kind.
	if s.pickedItemSup == nil || !poCasePacked(s.pickedQPP) {
		return s.plainLineTotal()
	}
	entered, _ := strconv.Atoi(strings.TrimSpace(s.lineInputs[poLineFieldQty].Value()))
	raw := strings.TrimSpace(s.lineInputs[poLineFieldCost].Value())
	cost, err := strconv.ParseFloat(raw, 64)
	// A NEGATIVE cost is not a cost, and the guard is the same one
	// plainLineTotal below already applies. Without it, typing "-5" into the
	// cost row of a case-packed line drew "$-5.00/case ÷ 12 = $-0.4167/unit ·
	// line $-10.00" — a derivation over an entry enter is about to REFUSE
	// (poDeriveUnitCost returns errInvalidCost for v < 0), which is the one
	// thing a check-before-you-commit row may not do. The row falls back to
	// saying what it is ASKING for, exactly as it does over a blank box,
	// because that is what an unusable value leaves it with.
	hasCost := raw != "" && err == nil && cost >= 0
	// withTotal TRUE: this form has no Line total row of its own, so the
	// derivation is where "what the line comes to" is said.
	if derivation := poEntryDerivation(entered, cost, hasCost, s.pickedQPP, s.caseBasis, true); derivation != "" {
		return derivation
	}
	// The KEY PHRASE leads. This note is folded onto a ~31-cell strip under the
	// field, so a sentence that opened with scene-setting put the words that
	// say what the row is asking for onto its second line — and the whole-line
	// legibility check (poWantPaneLine) reads one line at a time, for the same
	// reason an operator's eye does.
	if s.caseBasis {
		return fmt.Sprintf("The price of ONE case; the line records %s per case and the case cost ÷ %d.",
			poUnitCount(s.pickedQPP), s.pickedQPP)
	}
	return fmt.Sprintf("The price of ONE unit; %s is one case here.",
		poUnitCount(s.pickedQPP))
}

// plainLineTotal is what the line comes to for a line with no case size: the
// same "before it is committed" fact the case derivation ends with, which is
// not a case-only question. Empty when either row is blank, because a total
// invented over a row the operator has not filled is a number nobody typed.
func (s *PurchaseOrderCreateScreen) plainLineTotal() string {
	qty, qerr := strconv.Atoi(strings.TrimSpace(s.lineInputs[poLineFieldQty].Value()))
	raw := strings.TrimSpace(s.lineInputs[poLineFieldCost].Value())
	cost, cerr := strconv.ParseFloat(raw, 64)
	if qerr != nil || qty <= 0 || raw == "" || cerr != nil || cost < 0 {
		return ""
	}
	return fmt.Sprintf("%s × $%s = line $%s",
		poUnitCount(qty), poDisplayMoney(cost), poDisplayMoney(cost*float64(qty)))
}

// finalize POSTs the whole cart as one PurchaseOrderCreate. Called from the
// review phase; the PO-level notes come from the review notes input.
func (s *PurchaseOrderCreateScreen) finalize() tea.Cmd {
	if s.supplierID <= 0 {
		s.setErr("pick a supplier before submitting", "")
		return Status(s.errMsg, StatusError)
	}
	if len(s.lines) == 0 {
		s.setErr("add a line before submitting", "")
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
	// Blurred for as long as the request is out. The cart and these notes are
	// in the payload now, so a caret still blinking in the field would be the
	// screen inviting input it is going to throw away — the same lie as a bar
	// naming a key that no longer acts. poCreatedMsg focuses it again if the
	// submit comes back failed and there is something to edit.
	s.poNotes.Blur()
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

func (s *PurchaseOrderCreateScreen) updateReviewPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch m.String() {
	case "up", "down", "pgup", "pgdown":
		// Intercepted AHEAD of the notes box, which is focused on this phase
		// and would otherwise swallow them. The box is pinned in the header —
		// it never scrolls away and it takes every rune — so these four are
		// free to move the cart highlight, which is what they did before the
		// conversion too.
		//
		// They stay live while the POST is out: reading the cart is not
		// changing it, and the bar names them for exactly that reason.
		if moved, cmd := s.moveCursor(m, headerRows); moved {
			return s, cmd
		}
		return s, nil
	case "esc":
		// Back to the source chooser to add or remove lines. Keep the cart and
		// any notes already typed. Only esc goes back, so every character still
		// reaches the notes field.
		s.phase = poPhaseSource
		s.poNotes.Blur()
		return s, nil
	case "enter":
		if s.pending {
			// The bar has already stopped naming enter here, and the press
			// answers on the working line rather than into a flash: an operator
			// watching a slow POST and pressing enter again is exactly the
			// operator who will have missed a four-second status.
			return s, s.pendingDecline("enter is already in")
		}
		return s, s.finalize()
	case "ctrl+x":
		// Remove the highlighted line. A chord, not a bare letter, so it does
		// not collide with typing into the notes field.
		if s.pending {
			// finalize() copied the cart and the notes into the request, so
			// nothing pressed after it can reach the order being created: the
			// removal would be discarded by the navigation the 201 triggers,
			// and until then the pane would be describing a cart that is not
			// the one going in. Worse on the LAST line, where removeLineAt
			// drops the phase back to the source chooser — the operator told to
			// add a line while the order they already have is being created.
			return s, s.pendingDecline("ctrl+x removes nothing")
		}
		s.removeLineAt(s.reviewCursor)
		return s, nil
	case "ctrl+e":
		if s.pending {
			return s, s.pendingDecline("ctrl+e edits nothing")
		}
		return s, s.openLineEditor(s.reviewCursor, poPhaseReview)
	}
	if s.pending {
		// The notes went into the request with the cart, so a rune typed now is
		// work the 201 throws away. The field is BLURRED while the POST is out
		// (finalize), which is what stops the caret inviting the typing in the
		// first place; this answers the press that comes anyway, and names the
		// key so two of them cannot redraw one pane.
		return s, s.pendingDecline(m.String() + " is not in the notes")
	}
	var cmd tea.Cmd
	s.poNotes, cmd = s.poNotes.Update(m)
	return s, cmd
}

// ---------------------------------------------------------------------------
// The columnar frame
// ---------------------------------------------------------------------------
//
// This screen used to assemble its own pane: a prose action bar folded at the
// TOP, a supplier header under it, a phase body, and a failure line last —
// with six hand-rolled answers to "how many rows does that leave?"
// (frameRows / frameRowsWith / frameChromeRows / bodyRowBudget / cartRowBudget
// / sourceCartSpace / reviewCartSpace), its own windower (poCartWindow,
// renderWindowedList), its own status bound (renderFailLine + failRowBudget +
// failBodyFloor) and its own SACRIFICE ORDER for a short pane
// (sourceAttributionShown / sourceTitleShown / cartListedOnScreen /
// reviewAttributionShown). That was a second design system standing beside
// jde_form.go rather than a screen that had not been converted yet, and every
// one of those pieces had already been the site of the same defect the layer
// exists to prevent: a claim measured against one budget and drawn against
// another.
//
// All of it is the layer's now (sc-jde-poc):
//
//	the pane geometry     — jdeScreen, embedded.
//	the row budget        — bodyAvailForBar, one answer, read by the frame and
//	                        by every question asked ABOUT the frame.
//	the window            — jdeLines.Window, anchored on the cursor's block, so
//	                        the highlighted row is on the pane BY CONSTRUCTION.
//	                        That is what retired cartListedOnScreen and the four
//	                        keys it used to gate: a cart line the operator
//	                        cannot see is no longer a state this screen has.
//	the status row        — jdeScreen.statusRow, which flattens a multi-line OMS
//	                        body and bounds it in one forward pass.
//	the sacrifice order   — jdeHeadRank, declared beside each pinned row.
//	the action bar        — []actionBarItem through renderActionBarWrapped.
//
// WHAT MOVED, and why it is the same screen. The bar is at the BOTTOM and names
// keys rather than spelling sentences; the supplier line, the optional g/w/c
// values, what the cart comes to and the review phase's PO-notes box are PINNED
// above the body; and the body is the thing the operator moves a cursor
// through — the cart on the source chooser and on review, the rows on each
// picker, the fields on the line form. The keys that act on a row (Ctrl-E,
// Ctrl-X) can no longer be pressed against a row that is not drawn, and the
// keys that open a phase (R/I/A/F/G/W/C/D) no longer stop working when the
// pane is too short to draw their row, because the row was only ever their
// LABEL and the bar is where they are named.

func (s *PurchaseOrderCreateScreen) View() string {
	header := s.headerLines()
	items := s.barFor(len(header))
	status := s.statusLine()
	if s.phase == poPhaseSupplierSwitch {
		// A read-only body: what has to stay on the pane is wherever the
		// operator scrolled to rather than a row they are standing on.
		frame, offset := s.frameScrolled(header, s.switchBody(), s.switchScroll, status, items)
		s.switchScroll = offset
		return frame
	}
	body, cursor := s.body()
	return s.frameWrapped(header, body, cursor, status, items)
}

// statusLine is the row above the bar: what is in flight, or what came back
// failed. Both halves go through the LAYER's status row, which flattens a
// multi-line message and bounds it to the pane in one forward pass.
//
// That is not a nicety. omsapi.parseError puts the ENTIRE raw response body
// into APIError.Message whenever the JSON envelope carries no code, so nginx's
// stock 502 page — seven lines — reaches this row verbatim; unflattened it
// pushed the frame six rows over and clampToBox, which drops from the BOTTOM,
// took the whole action bar with it. The screen's own renderFailLine used to
// fold and budget it instead, which traded the height overflow for a block of
// rows taken out of whatever the phase drew last.
//
// Only the HEADLINE is here. The unbounded half rides in the pinned header
// (failLines), where it is cut to a fixed row count before it is folded.
//
// It is also where the screen's ANSWER TO THE LAST KEYPRESS goes, on every
// phase — the surface jdeFitHeader cannot reach, which is why a box being typed
// into can have the one essential header row. What competes for the row and in
// what order is statusPlan's, and it is answered there because the same
// decision says what the header must still carry.
func (s *PurchaseOrderCreateScreen) statusLine() string {
	row, _ := s.statusPlan()
	return row
}

// poStatusPlan is the assembled status row AND what it leaves for the pinned
// header to carry, answered together because they are one decision.
//
// Answered apart, they drift the way every duplicated bound in this file has
// drifted: the header would repeat what the row already says, or — the worse
// direction, and the one measured — drop the only copy of it. Both happened
// inside one round. The failure HEADLINE was displaced off the row by an answer
// that did not name it ("type a name, tag or serial" over a failed asset
// lookup) and nothing in the header had been told to pick it up; and the whole
// ANSWER was folded into the header on frames where the row had already drawn
// every word of it.
//
// The ROW is returned beside it rather than inside it, and that is not a style
// choice: TestJDEForm_EveryStatusRowComesFromTheLayer follows what a status
// argument is built from, and it can follow a call and a local but not a field
// selected off a struct a call returned. A row it cannot trace reads to that
// sweep exactly like an unbounded one — and because `statusLine` is a method
// name four other screens also declare, one untraceable return failed all of
// them at once.
type poStatusPlan struct {
	// holdsAnswer is true when the row carries the WHOLE answer, so the header
	// need not draw it at all.
	holdsAnswer bool
	// drawsHead is true when the row is DRAWING the failure headline, so
	// failLines need not lead its detail with it.
	//
	// This one asks WHICH content the row chose rather than how much of it
	// survived, and the difference is deliberate. Read as a substring the way
	// holdsAnswer is, it also went true→false when the row was drawing the
	// headline and merely SHORTENED it, so a narrow pane got a second,
	// identically shortened copy of the same sentence four rows up — a body row
	// spent on a near-duplicate that tells the operator nothing the row above
	// it did not. Which branch built the row is a fact about the assembly, not
	// a prediction of a bound, so nothing can drift here the way a
	// re-implemented bound would.
	drawsHead bool
}

// statusPlan decides what the one row above the bar draws.
//
// The order is the order of what an operator cannot reconstruct from anything
// else on the pane. WORKING wins: a request being out is a fact nothing else
// states, and the operator watching a slow gateway is exactly the one whose
// four-second flash has already expired. The ORDER-level error is next — a
// submit that came back failed, a validation refusal — because it belongs to
// the whole order rather than to the list being drawn. Then the ANSWER, and
// last the phase's failure HEADLINE.
//
// The answer LEADS the WORKING sentence, and only when a typed box is pinned.
// That condition is the whole architecture of this change in one line: the
// answer's home is the pinned header's essential row; a box being typed into
// takes that row when there is one, and then — and only then — the answer has
// nowhere left that jdeFitHeader cannot reach, so it rides here. Leading
// unconditionally was tried and cost the working sentence its tail on frames
// that had a perfectly good header row for the answer: `nothing to pick ·
// Looking up the items Acme Supply…`, which spends fifteen cells restating what
// the header already says in full and cuts the one sentence naming the work.
//
// AN ORDER-LEVEL ERROR IS NEVER LED. It shares this row with nothing, and that
// is rule 6 decided in the one place it bites hardest: on a row that cannot
// fold the FACT survives whole and the wording gives, and here the error IS the
// fact. A picker's hint is the lesser thing beside a submit that came back
// refused — `✗ type to narrow the catalo… · creating the PO f…` spends
// half the row on an instruction the operator can rediscover by pressing the
// key again, and leaves them unable to tell what failed on the surface an order
// is committed from. The wordings are cut short as well (setErr's headlines),
// but that only buys room in the common case and is NOT the guarantee: this
// branch is.
//
// WHAT THAT WOULD COST, AND WHY NO KEY CAN SPEND IT. Taking the row alone means
// that where a box is ALSO pinned the answer falls back to answerRows — a
// CONTEXT row of the pinned header, which jdeFitHeader trims first, so at 80x11
// through 80x13 it would be off the pane: the very state this branch exists to
// remove. That state is NOT REACHABLE, and the argument is written down because
// it is what a later change would break rather than notice. It needs a
// box-pinning phase (essentialBoxRow) holding BOTH a standing errMsg and a
// non-empty answer, and errMsg is retired at every phase change (Update's key
// dispatch). No setErr writer fires on a picker phase; the pending freeze on the
// source chooser's r/i/a/f stops a picker being ENTERED with a POST out; and on
// review phaseNote is empty while poCreatedMsg clears pendingLead before it
// calls setErr, so the answer there is always "".
//
// The MECHANISM is still here, and it reopens the moment a setErr writer becomes
// reachable from a box-pinning phase or the phase-change clear is removed —
// which is why that clear is a rule rather than a tidy-up, and why
// TestPOStatus_AnOrderLevelErrorIsNeverLedOffTheStatusRow reaches the state by
// writing setErr directly and says in as many words that no key sequence does.
func (s *PurchaseOrderCreateScreen) statusPlan() (string, poStatusPlan) {
	answer := s.answerNote()
	lead := ""
	if s.essentialBoxRow() != "" {
		lead = answer.text
	}
	head, _ := s.failure()
	var row string
	drawsHead := false
	switch {
	case s.workingSubject() != "":
		row = s.statusRow(true, s.workingLine(lead), "")
	case s.errMsg != "":
		// failure() answers errMsg first, so this row IS the headline.
		row = s.statusAnswer(StatusError, s.errMsg)
		drawsHead = true
	case answer.text != "":
		row = s.statusAnswer(answer.level, answer.text)
	default:
		row = s.statusRow(false, "", head)
		drawsHead = head != ""
	}
	// holdsAnswer is read off the row that was just ASSEMBLED rather than
	// predicted from the branch that assembled it. A prediction is a second
	// implementation of the bound it predicts, and this file has already
	// shipped that pair disagreeing; asking the drawn row cannot. It is also
	// what stops the header repeating a lead the row printed in full — the
	// review cart's frozen keys draw `ctrl+x removes nothing` on the row and
	// used to draw it again four lines up, adding nothing and spending a row of
	// a body that is showing the operator their cart.
	//
	// Everything a screen puts on this row is styled as ONE run, so the text is
	// contiguous inside it; a message fitStatus shortened carries the ellipsis
	// and no longer matches, which is exactly when the header has to pick it up.
	//
	// drawsHead is the branch's own answer for the reason on the field.
	return row, poStatusPlan{
		holdsAnswer: answer.text != "" && strings.Contains(row, answer.text),
		drawsHead:   drawsHead,
	}
}

// poLeadClause is an answer reduced to its OPENING CLAUSE — the part that names
// the key and what it did.
//
// It is what the answer contributes when the row already has a subject AND
// cannot hold both whole. The whole answer used to go in unreduced, and the
// reservation arithmetic then cut it to fit: "down moves nothing · still
// looking up the suppliers…" beside "Looking up the suppliers…" came back as
// `down moves nothing · s… · Looking up the suppliers…`, which spends nine
// cells restating the subject badly and drops the ellipsis in the middle of a
// word. The rest of the answer is in the pinned header on exactly those frames
// (answerRows), so nothing is lost by leaving it there — and the clause that
// tells two presses apart still rides the row that cannot be trimmed.
//
// The CONDITION is the correction: this is a measurement, and it was made at 80
// columns and then applied at every width. At 120 the pane is 91, the subject
// wants 37 and the whole answer 50, so the row had 25 cells free and spent a
// pinned-header row redrawing a 23-cell tail it could have carried itself —
// "51 is the width that must HOLD, not the width to render as though we had"
// pointed backwards. poLeadOnto asks the row first now, so the reduction fires
// exactly when the row cannot hold both, which at 80 columns is still always.
func poLeadClause(text string) string {
	clause, _, _ := strings.Cut(text, poLeadJoint)
	return clause
}

// answerNote is the screen's answer to the last keypress, as ONE line, from
// whichever of its two writers owns the press.
//
// pendingLead first, and it can only be first: it is written by pendingDecline
// and cleared by finalize, by poCreatedMsg and by every phase change, so it is
// non-empty only while the create POST is out — the one window in which the
// phase's own note is guaranteed stale, because every arm that could refresh it
// is frozen.
//
// The note is flattened at its JOINTS rather than truncated at its first line
// (pickerNote.statusText), because a forced break in a note separates two whole
// claims: assetsSearchClosedNote's "what you typed was never run" and "what the
// rows do answer" are two facts, and rule 3 says they are not interchangeable.
// flash drops the second — the toast has no room and the body line behind it
// carried the rest — and jdeStatusOneLine would run them together with a space.
func (s *PurchaseOrderCreateScreen) answerNote() pickerNote {
	if s.pendingLead != "" {
		return pickerNote{text: s.pendingLead}
	}
	n := s.phaseNote()
	n.text = n.statusText()
	return n
}

// answerRows is the answer's home in the PINNED HEADER, drawn exactly when the
// status row did not print the whole of it — because it is sharing the row with
// a working sentence or an order-level error, or because the answer is wider
// than one row of the pane.
//
// The two surfaces divide the work by what each can do, and NEITHER is
// sufficient on its own. The status row is the only one jdeFitHeader cannot
// reach, so the clause naming the key has to be reachable there at every
// height; the header is the only one that FOLDS, so the clauses saying WHY have
// to be there whenever the row could not hold them. Both halves have been
// shipped alone and both were defects: the header alone is how a declining key
// answered into a row a short pane trimmed (byte-identical panes at 80x11 and
// 80x12), and the row alone is how `searched again · no match for "zzz" (12 in
// catal…` lost the count that is the whole difference between "no such item"
// and "I mistyped".
//
// Where BOTH would say the same thing the header stays out of it: holdsAnswer
// is read off the row that was actually assembled, so a lead the row printed in
// full is not repeated four lines up.
//
// Which RANK it takes is headerLines': the essential row where no box has
// claimed it, context where one has.
//
// The CONTEXT case has one shape no key can reach, and it is recorded here so
// nobody re-derives it as a live defect: with a box pinned AND an order-level
// error standing, the status row is the error alone (statusPlan refuses to lead
// one), so the WHOLE answer would arrive here as a context row and be trimmed
// at 80x11 through 80x13. An order-level error does not outlive the phase it
// happened on — every phase change retires it — and no setErr writer fires on a
// box-pinning phase, so the pair cannot stand together. statusPlan carries the
// closing argument and the note on what would reopen it.
func (s *PurchaseOrderCreateScreen) answerRows() []string {
	n := s.answerNote()
	if n.text == "" {
		return nil
	}
	if _, plan := s.statusPlan(); plan.holdsAnswer {
		return nil
	}
	lines := n.renderLines(s.paneWidth() - len(jdeIndent))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, jdeIndent+line)
	}
	return out
}

// failure is the headline and the unbounded detail of whatever has gone wrong
// on the phase being drawn.
//
// ONE function, because the two halves are drawn in two places — the headline
// on the status row, the detail in the pinned header — and a headline that
// could outlive its detail is how "quantity must be a positive integer" ended
// up with a gateway's HTML folded underneath it, reading as the gateway
// explaining the validation.
//
// It answers for the PHASE, not for the screen: a failed agreement load is not
// a fact about the item picker, and reporting it there would put a sentence
// nobody can act on over the one they came for. The screen-level errMsg — a
// validation refusal, or the submit coming back failed — is checked first,
// because it belongs to the whole order rather than to the list being drawn.
func (s *PurchaseOrderCreateScreen) failure() (head, detail string) {
	if s.errMsg != "" {
		return s.errMsg, s.errDetail
	}
	switch s.phase {
	case poPhaseSupplier, poPhaseSupplierSwitch:
		if s.supplierLoadErr != "" {
			return "loading the suppliers failed", s.supplierLoadErr
		}
	case poPhaseAgreement:
		if s.agreementLoadErr != "" {
			return "loading this supplier's agreements failed", s.agreementLoadErr
		}
	case poPhaseWorkOrder:
		if s.assoc.workOrderErr != "" {
			return "loading the work orders failed", s.assoc.workOrderErr
		}
	case poPhaseCommittee:
		if s.assoc.committeeErr != "" {
			return "loading the committees failed", s.assoc.committeeErr
		}
	case poPhaseReorderPick:
		if s.reorderLoadErr != "" {
			return "reading the reorder queue failed", s.reorderLoadErr
		}
	case poPhaseItemPick:
		if s.itemSuppliersErr != "" {
			return "looking up this supplier's items failed", s.itemSuppliersErr
		}
	case poPhaseAssetPick:
		if s.assetsErr != "" {
			return "looking up this supplier's assets failed", s.assetsErr
		}
	}
	return "", ""
}

// workingLine names the WORK and the SUBJECT of whatever request is out on the
// phase being drawn: "Loading…" tells an operator that a rectangle is busy,
// "Looking up the items Acme Supply sells…" tells them which request is out
// and against whom.
//
// It answers for the phase on screen and not for the screen as a whole, because
// several of these loads run in the background against phases that are not
// drawing them — the association options ride along with the screen opening,
// and the agreements ride along with a supplier commit. A row that named those
// would be reporting work the operator did not ask for over the work they did.
// The submit is the one exception and comes first: it owns the whole screen for
// as long as it is out, on every phase esc can reach.
//
// pendingLead is what a key the submit has made inert just did, and it leads
// this row rather than answering into a four-second flash — the operator
// watching a slow gateway is exactly the operator who will have missed one.
// It is BOUNDED against what the subject needs (workingLine below), never the
// reverse.
func (s *PurchaseOrderCreateScreen) workingSubject() string {
	if s.pending {
		return poSubmitWords + s.supplierLabel() + "…"
	}
	switch s.phase {
	case poPhaseSupplier, poPhaseSupplierSwitch:
		if s.supplierLoading {
			return "Looking up the suppliers…"
		}
	case poPhaseAgreement:
		if s.agreementLoading {
			return "Looking up what " + s.supplierLabel() + " has on agreement…"
		}
	case poPhaseWorkOrder:
		if s.assoc.workOrderLoad {
			return "Looking up the open work orders…"
		}
	case poPhaseCommittee:
		if s.assoc.committeeLoad {
			return "Looking up the committees…"
		}
	case poPhaseReorderPick:
		if s.reorderLoading {
			return "Looking up what " + s.supplierLabel() + " has flagged for reorder…"
		}
	case poPhaseItemPick:
		if s.itemSuppliersLoad {
			// "Reloading" is not decoration: the catalog is held per supplier
			// and r goes and asks again, so the operator has to be able to tell
			// a first look from a refresh they asked for.
			verb := "Looking up"
			if s.itemSuppliersFor == s.supplierID && s.supplierID > 0 {
				verb = "Reloading"
			}
			return verb + " the items " + s.supplierLabel() + " sells…"
		}
	case poPhaseAssetPick:
		if s.assetsLoading {
			// FIXED WORDS, then the QUERY, then the supplier — and that order is
			// the whole of rule 6 read INSIDE one sentence rather than between
			// two. This used to read `Searching ` + supplier + `'s assets for
			// "zzz"…`, which put the one part that is NOT the identity of the
			// work first: the supplier is the same on every phase of this screen
			// and is pinned on a header row of its own, while the query is the
			// only thing that says what this particular lookup is. Under a lead
			// the subject is bounded to 23 cells at 80 columns (poSubmitWords
			// carries the arithmetic), and the old order spent all of them on
			// `Searching Acme Supply'…` — the query gone, the fact restated.
			//
			// WHAT IS CLAIMED IS THE ORDER IT DEGRADES IN, NOT THAT IT FITS.
			// The query is operator-supplied and a bound expressed in an
			// unbounded value is not a bound, so this says only what is true at
			// every width: the FIXED WORDS survive whole, then as much of the
			// QUERY as the row has left, then the query's tail gives, then the
			// supplier and the noun after it. Nothing here promises a whole
			// query — an MRO part description runs to forty cells and the floor
			// is 23. `Searching assets ` was 17 of those 23 and left four
			// characters of query, which is the claim-that-fits written as
			// though it were the order-it-degrades-in; poAssetSearchWords is 8
			// and leaves an ordinary term recognisable, which is as far as fixed
			// words can get anyone.
			//
			// It is PROGRESSIVE, like every other subject on this screen, and
			// that is not a style note: this row is muted and this screen's
			// other muted rows are instructions, so an imperative here
			// ("Search …") sat beside a picker hint as `type a name, tag or
			// seri… · Search "hydraulic pump…` — two hints joined by a ` · `,
			// with nothing on the row saying a request was out.
			//
			// No ` · ` inside either sentence: that joint is the clause
			// separator poLeadClause and pickerWrap read, and one here would
			// make half a subject look like a second claim.
			if q := strings.TrimSpace(s.assetsQuery); q != "" {
				return poAssetSearchWords + poQuotedClip(q, 20) +
					" in " + s.supplierLabel() + "'s assets…"
			}
			return "Loading assets from " + s.supplierLabel() + "…"
		}
	}
	return ""
}

// workingLine is that sentence bounded to the row the layer will draw it on.
//
// The status row CANNOT FOLD — fitStatus flattens and clips it in one forward
// pass — so both halves of this row have to be measured against it here. Two
// things were wrong before, and the doc comment above denied the second:
//
//   - the SUBJECT could overflow on its own. supplierLabel clips at 20, so
//     "Looking up what … has flagged for reorder…" reaches 58 into the 51 a
//     body has at 80 columns. Bounded HERE, at the one exit every one of these
//     sentences leaves through, rather than by shortening them one at a time.
//     (The example used to be the asset picker's "Looking up the assets bought
//     from …"; that sentence is "Loading assets from " now and comes to 41, so
//     a reader checking the bound against it would have found it unnecessary.
//     The reorder subject is the one that still overruns.)
//   - the LEAD was joined in front of it unbounded, so a decline pushed the
//     subject off the row and, with the longest lead in this file (51 cells),
//     off it entirely: nothing left saying a purchase order was being created,
//     once pendingDecline's four-second flash expired.
//
// So both are bounded against the row, and what gives is chosen by rule 6: the
// FACTS survive and the IDENTIFIERS abbreviate. The facts here are the fixed
// words of the subject (poSubmitWords, and see there for why they are short
// enough to be kept) and the KEY NAME the lead opens with; what gives is the
// supplier name at the subject's tail and the lead's own tail.
//
// The subject therefore RESERVES the lead's own fact before clipping, rather
// than taking the row and leaving the remainder. Subject-takes-all was tried
// and left the lead four cells at 80 columns — "en…", which names no key and
// so tells two presses apart by nothing. A lead is written to the same ` · `
// joints as everything else on this screen, and its FIRST CLAUSE is the part
// that answers the press ("ctrl+x removes nothing"); the rest elaborates. So
// the first clause is what the row keeps for it, capped at half the row so a
// long one cannot crowd the subject out in turn.
//
// The lead also keeps its place at the FRONT when it is cut, because the key it
// answers for is its first token; putting it after the subject would cut away
// the one token that distinguishes the presses.
//
// At 80 columns the two sentences want about 54 cells of a 51-cell row, so the
// SUPPLIER at the subject's tail is what goes — the identifier abbreviating
// while the facts stay, which is rule 6. At 100 and 120 neither gives.
func (s *PurchaseOrderCreateScreen) workingLine(lead string) string {
	subject := s.workingSubject()
	if subject == "" {
		return ""
	}
	return poLeadOnto(lead, subject, s.paneWidth())
}

// poLeadOnto is that arithmetic, split out from workingLine so the reservation
// can be read on its own — the two bounds it applies are the whole of this
// row's rule 6 and they are easier to check apart from the sentence-building.
//
// ONE caller: workingLine. It used to have two, and the second was the
// order-level error, which is now drawn alone (statusPlan) — an error sharing
// the row with a picker hint is rule 6 inverted, because there the error IS the
// fact. So `room` is simply the pane: the muted working line is drawn behind no
// mark at all, and there is no longer a caller passing a mark-adjusted
// remainder. A second caller that DOES sit behind a mark must subtract it
// before calling, because nothing here can see what it will be drawn behind.
//
// The SUBJECT is never empty: workingLine returns early on an empty
// workingSubject, which is the only thing that reaches this.
//
// It takes the WHOLE answer and reduces it to its opening clause itself, rather
// than being handed one already reduced. The reduction is a consequence of the
// bound, so it belongs where the bound is measured: asked one caller earlier it
// could only be unconditional, and a wide terminal then split an answer across
// two surfaces that one row had room for. See poLeadClause.
func poLeadOnto(lead, subject string, room int) string {
	if lead == "" {
		return pickerClip(subject, room)
	}
	joint := lipgloss.Width(poLeadJoint)
	if lipgloss.Width(lead)+joint+lipgloss.Width(subject) <= room {
		return lead + poLeadJoint + subject
	}
	lead = poLeadClause(lead)
	answer, rest, more := strings.Cut(lead, poLeadJoint)
	floor := lipgloss.Width(answer)
	if more && rest != "" {
		// One cell for the ellipsis pickerClip adds, or the clause it is
		// reserving room for comes back a character short of itself.
		floor++
	}
	if half := room / 2; floor > half {
		floor = half
	}
	subject = pickerClip(subject, room-floor-joint)
	lead = pickerClip(lead, room-lipgloss.Width(subject)-joint)
	if lead == "" {
		return subject
	}
	return lead + poLeadJoint + subject
}

// poLeadJoint is what separates a lead from the sentence it leads, and it is
// the same ` · ` joint every note on this screen is written at.
//
// It is measured with lipgloss.Width and never len: U+00B7 is two BYTES and one
// CELL, so len says 4 where the terminal draws 3. The error was conservative
// while the joint was this one — a cell wasted on a row this file spends pages
// defending — but a bound in bytes is only ever accidentally right, and the
// next joint could be wide enough to make it too small instead.
const poLeadJoint = " · "

// poSubmitWords is the FACT the submit's working row carries, and it is short
// because that row cannot fold and may have to share 51 cells with a lead.
//
// The arithmetic, at 80 columns where room is 51: a lead reserves its first
// clause, capped at room/2 = 25, and the joint costs 3, so the subject is
// bounded to 23 in the WORST case (the file's longest first clauses —
// "shift+tab waits for the submit", "shift+tab is not in the notes" — are over
// the cap; "enter commits nothing" reserves 22 and leaves 26). These 20 cells
// fit that with room over for the supplier, which is the part that abbreviates.
//
// "Creating the purchase order for " was 32 and could not: under the longest
// lead the row drew "Creating the purchase or…" with the supplier gone
// altogether — the sentence cut mid-word AND the identifier lost, which is
// rule 6 exactly inverted on the surface an order is committed from.
const poSubmitWords = "Creating the PO for "

// poAssetSearchWords is the FACT the asset picker's working row leads with, and
// it is eight cells for the same reason poSubmitWords is twenty: it shares a row
// that cannot fold with a lead, and every cell it spends is a cell of the
// operator's search term that the clip takes instead.
//
// The arithmetic, at 80 columns: a lead reserves half the 51-cell row and the
// joint costs 3, so the subject gets 23 and its head is 22. These 8 leave 14 for
// the opening quote and the term — enough that two ordinary MRO searches that
// share a leading word ("hydraulic pump", "hydraulic hose") are still told apart,
// which is the floor po_create_answer_surface_test.go's poAssetQueryHeadCells
// pins, with a cell over. "Searching assets " was 17 and left FOUR characters of
// the term: the query had been moved to the front of the sentence and was still,
// in effect, gone.
//
// PROGRESSIVE, and short enough to stay so. "Search " bought three cells by
// going imperative and cost more than it bought: it was the only working
// sentence on this screen not in the progressive, so on a muted row whose
// neighbours are instructions it read as one more hint rather than as work in
// flight, and it collided with poAssetSearchLabel — one word meaning a field
// label on the header row and a request in flight on the row below it.
// "Searching " reads best of the three and is 10, which leaves exactly the 11
// cells that separate "hydraulic pump" from "hydraulic hose": right on the
// floor, with nothing spare. Eight has the margin.
//
// It buys an ORDER OF DEGRADATION and not a fit — the term is operator-supplied
// and no fixed words make forty cells fit 23. See workingSubject.
const poAssetSearchWords = "Finding "

// poQuotedClip bounds an operator-supplied string that is about to be QUOTED
// into the middle of a sentence, and keeps its closing quote when the bound
// bites.
//
// Two rules meet here and each is right on its own. The QUOTED string is what
// must be bounded, never a bounded string that is then quoted (assetScopeRows
// carries the reason at length: strconv.Quote ESCAPES, so a backslash comes
// back two cells and a control rune up to six, and clipping first budgets for
// the quote marks and then pays the escaping on top). But whether the CLOSING
// QUOTE is worth a cell is POSITIONAL: at the end of a row the ellipsis is
// already the boundary and the cell is better spent on one more character of
// the term, while mid-sentence a bare `"hydraulic pump sea…` runs straight on
// into the fixed words after it and the operator cannot see where what they
// typed ends.
//
// So: clip the QUOTED string, keeping the escape bound, and re-append the
// closing quote out of the room the clip was given rather than past it.
//
// WHICH SITES ARE MID-SENTENCE IS ASKED OF THE ROW, NOT OF THE FILE, and this
// doc got that wrong once: it said assetScopeRows' value ENDS its row, so the
// question could not arise there. It appends a page suffix AFTER the value
// (`Value: shown + page`), so whenever the operator is on page 2 or there is a
// next page their term is mid-sentence and the row drew
// `Showing ..... "hydraulic pump seal k… · page 1`. That site now routes here
// for exactly the paged case and keeps pickerClip for the unpaged one — which
// is rule 10 with the general rule in hand: derive the set of sites from what
// the rule is ABOUT, and ask each ROW rather than assuming a whole function
// answers one way.
func poQuotedClip(q string, room int) string {
	quoted := strconv.Quote(q)
	if room < 3 || lipgloss.Width(quoted) <= room {
		return pickerClip(quoted, room)
	}
	return pickerClip(quoted, room-1) + `"`
}

// poSubmitFailWords is what the same submit says when it comes back refused,
// and it is cut for the same reason poSubmitWords is: it is the whole of the
// status row on the frame it is drawn on, and it shares that row with nothing
// (statusPlan refuses to lead an order-level error), so what a narrow pane
// takes off it is the sentence naming what failed.
//
// "submitting this purchase order failed" was 37 cells and lost its tail on a
// 60-column terminal, where the pane is 31; these 22 fit that with the mark's
// two cells to spare. It echoes the working sentence deliberately — the
// operator watched "Creating the PO for Acme…" and reads the same three words
// back.
const poSubmitFailWords = "creating the PO failed"

// ---------------------------------------------------------------------------
// The pinned header, and what a short pane gives up
// ---------------------------------------------------------------------------

// headerLines is everything drawn ABOVE the scrollable body on every frame,
// each row carrying the RANK that decides when it is given up (jdeHeadRank).
//
// This is where this screen's sacrifice order lives now. It used to be four
// predicates and two substitute notices — sourceAttributionShown,
// sourceTitleShown, cartListedOnScreen, reviewAttributionShown,
// sourceAttributionNote, attributionHiddenNote — each measuring a frame that
// included the very bar its own answer changed, and each of them had to make
// the keys it hid stop working so the bar could stop naming them. None of that
// is needed once the bar is the place a key is named: a row here is a LABEL and
// a VALUE, never an affordance, so dropping one costs the operator a fact and
// never a key.
//
// The order the rows give ground in is therefore the order they are worth
// least in, and it is stated once, per row, in the rank:
//
//	decorative — the blank separators. Nothing names them and nothing reads
//	             them, so they go first.
//	context    — the supplier line, the failure DETAIL, what the cart COMES TO,
//	             then the optional agreement / work-order / committee values
//	             and the "still looking up…" line under them.
//	essential  — exactly one row per phase, and it is the row the operator
//	             would ACT DIFFERENTLY without. There is no contention for it:
//	             a phase that pins a typed BOX gives it the slot, always
//	             (essentialBoxRow — the item filter, the asset search, the
//	             PO-notes box on review), and a phase that pins none gives it to
//	             the screen's ANSWER to the last keypress where there is one and
//	             to the phase's standing FACT otherwise. A box being typed into
//	             is the layer's own first example of the rank, and it is why
//	             review pins its notes rather than hanging them off the bottom
//	             of the cart: an operator typing into a field that is not on the
//	             pane is the worst form of this screen's oldest defect.
//	             The answer used to compete for this slot and the box is what
//	             lost; the answer's home is the layer's STATUS ROW now
//	             (statusLine), which no rank can reach.
//
// A RANK DOES NOT REMOVE THE SIGNIFICANCE OF ORDER WITHIN A RANK, and that is
// why the cart total is emitted BEFORE the optional values rather than after
// them. jdeFitHeader gives ground from the END within each rank, so two rows
// sharing a rank are still separated by POSITION — merging the attribution
// block and the total block into one to save a separator row quietly made
// position the tiebreak again, which is the coupling jdeHeadRank exists to
// break. Written the other way round, an 80x20 review frame under a dozen
// staged lines dropped "at least $84.00" — the money floor, on the surface the
// operator commits an order from — while "Agreement ..... (none)" and
// "Committee ..... (none)" were still drawn. Do not "tidy" the append back.
//
// Only one row may be essential, because the smallest pane a frame is drawn
// into keeps exactly one header row (jdeMinBudget). Two would be a claim the
// geometry cannot honour, which is the same false claim in a new place —
// TestJDEForm_EveryEssentialHeaderRowIsOnThePane is where it fails.
func (s *PurchaseOrderCreateScreen) headerLines() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadContext, s.supplierHeaderRows()...)
	h = h.add(jdeHeadContext, s.failLines()...)

	switch s.phase {
	case poPhaseSource:
		// ONE block, one separator: the attribution values and what the cart
		// comes to are both facts about the order, and a blank row between them
		// is a row of the body's.
		//
		// The TOTAL is emitted FIRST and that order is load-bearing, not
		// cosmetic — see the rank ledger above. Merging the two blocks to save
		// a separator row put them at one rank, and within a rank jdeFitHeader
		// gives ground from the END, so whichever is written last is the one a
		// short pane drops. Written the other way round the confirm surface
		// lost "at least $84.00" while "Committee ..... (none)" stayed.
		//
		// The key column goes with the BAR's claim about g / w / c, because it
		// is the same claim: while the create POST is out barItems drops those
		// three and updateSourcePhase answers them with pendingDecline, so a
		// highlighted letter beside the value would be this pane advertising
		// and refusing one key at once — the defect the line-source rows were
		// deleted to remove. The VALUES stay: they are part of the order being
		// created, and only the affordance is false.
		h = h.addBlock(jdeHeadContext, append(s.cartTotalRows(), s.attributionRows(!s.pending)...))
	case poPhaseReview:
		h = h.addBlock(jdeHeadContext, append(s.cartTotalRows(), s.attributionRows(false)...))
	case poPhaseItemPick:
		// With the box SHUT and a filter still applied, the rows on the pane
		// are a subset and nothing else says so. The box itself is the
		// essential row while it is open (essentialBoxRow), so it is not
		// repeated here.
		if !s.itemSuppliersTyping {
			if q := strings.TrimSpace(s.itemSuppliersSearch.Value()); q != "" {
				lw, pane := poHeaderLabelWidth(), s.paneWidth()
				h = h.addBlock(jdeHeadContext, []string{renderJDEField(jdeField{
					Label: poItemFilterLabel, Kind: jdeValue,
					Value: pickerClip(q, poFieldValueRoom(pane, lw, false))}, lw, pane)})
			}
		}
	case poPhaseAssetPick:
		h = h.addBlock(jdeHeadContext, s.assetScopeRows())
	}

	// The essential row, last, so it sits directly above the body it is about.
	//
	// It is the BOX the operator is typing into wherever a phase pins one, and
	// the phase's standing FACT everywhere else. Neither is an ANSWER: the
	// answer to the last keypress rides the status row now (statusLine), which
	// the frames append unconditionally and jdeFitHeader cannot reach.
	//
	// That split is the whole point, and it is worth the paragraph because the
	// obvious alternative has already been shipped and reverted. The note used
	// to take this slot whenever it had something to say, so that a declining
	// key had somewhere to answer that a short pane could not trim — and only
	// one row may be essential, so what gave instead was the SEARCH BOX. It was
	// gone at 80x11, 80x12 and 80x13, and the asset picker's typing branch
	// changes nothing else on the pane (its search is server-side and runs on
	// enter, so assetScopeRows still reads the COMMITTED query and the note is
	// written once by openAssetSearch), which made every typed rune there redraw
	// a byte-identical frame. Trading which row disappears cannot fix that in
	// either direction; both rows have to be drawable at once, so the answer
	// moved off this budget entirely.
	//
	// A phase that pins a box therefore draws no STANDING fact: the box is the
	// essential row, and a fact repeated under a box the operator is typing
	// into would spend a row of a body that is showing them the rows they are
	// picking from. The ANSWER can still appear above it as CONTEXT, but only
	// on the frames where the status row could not print the whole of it
	// (answerRows) — which is the only case where the header adds anything.
	answer := s.answerRows()
	if box := s.essentialBoxRow(); box != "" {
		return h.addBlock(jdeHeadContext, answer).add(jdeHeadDecorative, "").add(jdeHeadEssential, box)
	}
	// With no box to pin, the answer takes the slot when it has one to take —
	// its head is on the status row either way, but a header that marked
	// nothing essential is a header the layer may trim to whichever row
	// happened to be first. The standing FACT fills it the rest of the time, so
	// "nothing to say" and "the row scrolled away" stay different states.
	rows := answer
	if len(rows) == 0 {
		rows = s.standingRows()
	}
	if len(rows) == 0 {
		return h
	}
	return h.add(jdeHeadDecorative, "").
		add(jdeHeadEssential, rows[0]).
		add(jdeHeadContext, rows[1:]...)
}

// supplierHeaderRows is the committed supplier, drawn as a columnar value row.
//
// Both values on it are OMS-supplied and are bounded in PRIORITY order, because
// this row is drawn on EVERY phase — review included, where it is the last
// thing seen before submit. At 51 columns "Acme Supply (#1)  · agreement:
// Annual 2026 Steel Contract" is over the pane on its own, and an unbounded
// supplier name used to take the agreement, and the (#id) with it, off the
// row: the confirm-before-submit surface losing which agreement the order is
// placed under.
func (s *PurchaseOrderCreateScreen) supplierHeaderRows() []string {
	labels := []jdeField{{Label: poRowSupplier}, {Label: poRowAgreement},
		{Label: poRowWorkOrder}, {Label: poRowCommittee}}
	lw := jdeLabelWidth(labels)
	pane := s.paneWidth()
	value, dim := "(none picked)", true
	switch {
	case s.supplierLoading:
		value, dim = "looking them up…", true
	case s.supplierLoadErr != "":
		// The reason is on the status row and its body in failLines; this row
		// says only that there is no supplier to show, so a failed load can
		// never read as "this order has no supplier yet".
		value, dim = "unavailable", true
	case s.supplierID > 0:
		name := ""
		for _, sup := range s.suppliers {
			if sup.ID == s.supplierID {
				name = sup.Name
				break
			}
		}
		id := fmt.Sprintf(" (#%d)", s.supplierID)
		// The room the VALUE has, measured off the row the layer is about to
		// draw rather than off a constant: label column, leader and indent.
		room := pane - len(jdeIndent) - lw - len(jdeLeader) - lipgloss.Width(id)
		if room < poHeaderValueFloor {
			room = poHeaderValueFloor
		}
		value, dim = pickerClip(name, room)+id, false
	}
	return []string{renderJDEField(jdeField{
		Label: poRowSupplier, Kind: jdeValue, Value: value, Dim: dim}, lw, pane)}
}

// poHeaderValueFloor is the cells a clipped value keeps: enough that the
// operator can see a value is there and that it was shortened, rather than
// reading a label followed by nothing. Shared with po_add_line.go and the
// picker rows, which is why it is a package constant rather than a local one.
const poHeaderValueFloor = 6

// The four columnar labels this screen's header rows share. Stated once so
// jdeLabelWidth measures the same set every builder draws from, which is what
// keeps the leader column in one place as the frame changes phase.
const (
	poRowSupplier  = "Supplier"
	poRowAgreement = "Agreement"
	poRowWorkOrder = "Work order"
	poRowCommittee = "Committee"
)

// poHeaderLabelWidth is that shared column.
func poHeaderLabelWidth() int {
	return jdeLabelWidth([]jdeField{{Label: poRowSupplier}, {Label: poRowAgreement},
		{Label: poRowWorkOrder}, {Label: poRowCommittee}})
}

// attributionRows is the optional agreement / work-order / committee block:
// what this order is being placed under and who for.
//
// withKey draws them as the source chooser's affordances, keyed with the letter
// the bar names; without, as plain value rows — on the review surface, and on
// the chooser itself while the submit is out, where those three letters decline.
// The key column is a READING of what the bar already says, never a second
// place a key is named, which is why the "still looking up…" line under them can
// name no key at all and lose nothing.
func (s *PurchaseOrderCreateScreen) attributionRows(withKey bool) []string {
	lw, pane := poHeaderLabelWidth(), s.paneWidth()
	var out []string
	row := func(key, label, value, loadErr string) {
		f := jdeField{Label: label, Kind: jdeValue}
		switch {
		case loadErr != "":
			// Say the list is MISSING rather than absent: "no agreements" and
			// "couldn't ask" are different facts and only one is safe to act
			// on. Bounded where it is drawn, because an APIError message is the
			// whole raw response body.
			f.Value = "unavailable — " + loadErr
		case value != "":
			f.Value = value
		default:
			f.Value, f.Dim = "(none)", true
		}
		f.Value = pickerClip(f.Value, poFieldValueRoom(pane, lw, withKey))
		line := renderJDEField(f, lw, pane)
		if withKey {
			line = StyleActionBarKey.Render(key) + " " + line
		}
		out = append(out, line)
	}
	// The bare predicate, never `offered() || err != ""`: each of the three
	// already ANSWERS for a failed load (agreementOffered is
	// `len(...) > 0 || loadErr != ""`), and repeating the error half reads as
	// though a failure were a separate case these rows handle — which is the
	// one thing those predicates' own doc comments exist to deny.
	if s.agreementOffered() {
		row("g", poRowAgreement, s.pickedAgreementName(), s.agreementLoadErr)
	}
	if s.assoc.workOrdersOffered() {
		row("w", poRowWorkOrder, s.pickedWorkOrderLabel(), s.assoc.workOrderErr)
	}
	if s.assoc.committeesOffered() {
		row("c", poRowCommittee, s.pickedCommitteeLabel(), s.assoc.committeeErr)
	}
	return append(out, s.pendingLookupRows()...)
}

// poFieldValueRoom is the cells a columnar value row leaves its VALUE, given
// the pane and the shared label column. keyed rows carry a two-cell key column
// in front of the indent, so they are measured with it rather than against a
// row that does not have one.
func poFieldValueRoom(pane, labelWidth int, keyed bool) int {
	room := pane - len(jdeIndent) - labelWidth - len(jdeLeader)
	if keyed {
		room -= 2
	}
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	return room
}

// pendingLookupRows names the optional lookups still in flight.
//
// None of them gates anything, which is why they load in the background — but
// "no row yet" and "this supplier has no agreements" render identically, and
// the second is a conclusion an operator may act on. So the wait says it is a
// wait. Folded, because three pending subjects come to 74 columns and a wait
// that says which lookups are out is only useful if it can be read.
func (s *PurchaseOrderCreateScreen) pendingLookupRows() []string {
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
		return nil
	}
	return jdeCaveatLines("still looking up "+strings.Join(pending, " · ")+"…", s.paneWidth())
}

// essentialBoxRow is the row a phase pins because the operator is TYPING into
// it, or "" on a phase that pins none.
//
// Where it answers, it TAKES the header's one essential slot, on every phase
// and unconditionally — nothing competes with it any more. It used to share the
// slot with the screen's answer to the last keypress, whichever of them had
// something to say, and that trade is the defect this arrangement replaced:
// with the answer winning, the box was off the pane at 80x11 through 80x13 and
// every rune typed into the asset search redrew a byte-identical frame. The
// answer rides the layer's status row now (statusPlan), which is outside the
// header budget, so there is one row for each of them at every height.
//
// A phase that pins one therefore draws no standing fact (standingRows) and no
// answer in the header except the folded TAIL the status row could not print
// (answerRows).
func (s *PurchaseOrderCreateScreen) essentialBoxRow() string {
	lw, pane := poHeaderLabelWidth(), s.paneWidth()
	switch {
	case s.phase == poPhaseReview:
		box := s.poNotes
		return renderJDEField(jdeField{
			Label: poNotesLabel, Kind: jdeText, Input: &box, Width: poFieldWidth,
			Focused: box.Focused()}, lw, pane)
	case s.phase == poPhaseItemPick && s.itemSuppliersTyping:
		box := s.itemSuppliersSearch
		return renderJDEField(jdeField{
			Label: poItemFilterLabel, Kind: jdeText, Input: &box, Width: poFieldWidth,
			Focused: true}, lw, pane)
	case s.phase == poPhaseAssetPick && s.assetsTyping:
		box := s.assetsSearch
		return renderJDEField(jdeField{
			Label: poAssetSearchLabel, Kind: jdeText, Input: &box, Width: poFieldWidth,
			Focused: true}, lw, pane)
	}
	return ""
}

// standingRows is the pinned header's own row: the phase's standing FACT,
// folded to the pane the terminal really gave.
//
// It used to be the screen's ANSWER to the last keypress, with the standing
// fact as a fallback for a phase that had nothing to answer. The answer has its
// own surface now — the status row, which the frames reserve on every pane and
// jdeFitHeader cannot reach (statusLine) — so what is left here is the row that
// is true whatever was last pressed, and the essential slot it fills is stable
// rather than contended.
//
// A phase that pins a typed BOX draws none of it: the box is that phase's
// essential row and nothing is gained by repeating a standing fact above it.
//
// Folded, never hand-counted. Root.View TRUNCATES rather than wrapping, and the
// tail of one of these sentences is the half that says what the state IS.
func (s *PurchaseOrderCreateScreen) standingRows() []string {
	if s.essentialBoxRow() != "" {
		return nil
	}
	// A FACT about the phase rather than a list of keys. The bar names the
	// keys, on every frame, at the bottom of the pane where nothing can trim
	// it; a standing note that repeated them would be the second statement of
	// one claim that this screen's own history says will eventually contradict
	// the first.
	lines := pickerNote{text: s.standingNote()}.renderLines(s.paneWidth() - len(jdeIndent))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, jdeIndent+line)
	}
	return out
}

// phaseNote is the pickerNote this phase ANSWERS a keypress into.
// One switch, so a phase cannot answer a key into a note no frame draws — which
// is what the four picker notes each had to be wired up for separately.
func (s *PurchaseOrderCreateScreen) phaseNote() pickerNote {
	switch s.phase {
	case poPhaseSupplier:
		return s.supplierNote
	case poPhaseSupplierSwitch:
		return s.supplierSwitchNote()
	case poPhaseSource:
		return s.sourceNote
	case poPhaseReorderPick:
		return s.reorderNote
	case poPhaseItemPick:
		return s.itemSuppliersNote
	case poPhaseAssetPick:
		return s.assetsNote
	}
	return pickerNote{}
}

// standingNote is what a phase with nothing to answer says instead. It is a
// FACT about the phase rather than an instruction — the keys are the bar's job
// — and it is what fills the essential slot on the phases whose notes are
// empty, so every frame has a row it can be judged by.
func (s *PurchaseOrderCreateScreen) standingNote() string {
	switch s.phase {
	case poPhaseSupplier:
		return "Every picker after this is scoped to the supplier you commit."
	case poPhaseSupplierSwitch:
		return "Dropped lines cannot be recovered."
	case poPhaseAgreement:
		return "Row 1 places the order under no agreement."
	case poPhaseWorkOrder:
		return "Row 1 attaches the order to no work order."
	case poPhaseCommittee:
		return "Row 1 places the order on behalf of nobody."
	case poPhaseSource:
		return "Staged lines are not saved until the order is submitted."
	case poPhaseReorderPick:
		return "Quantities come from each item's suggested reorder quantity."
	case poPhaseItemPick:
		return "The filter runs over the catalog already loaded, not on the server."
	case poPhaseAssetPick:
		return "The search runs on the server, over name, tag and serial."
	case poPhaseLine:
		// WHAT is being ordered, which is the fact an operator would act
		// differently without: a freeform line and a catalog line take the same
		// keystrokes and produce different purchase orders. Why a case-packed
		// line is in UNITS is NOT here: it belongs to the quantity in the box,
		// which changes as it is typed, so it is derived under the cost row by
		// entryDerivation and stated once.
		return "Line source: " + s.lineSourceName()
	case poPhaseReview:
		// Never reached — review pins its notes box as the essential row, and
		// standingRows draws nothing on a phase that pins one — but answered
		// rather than left empty, because a phase that stopped pinning a box
		// would otherwise fall through to "" and mark no essential row at all.
		return "The whole cart is submitted in one request."
	}
	return ""
}

// lineSourceName names where the line being entered came from.
func (s *PurchaseOrderCreateScreen) lineSourceName() string {
	switch {
	case s.pickedItemSup != nil:
		return fmt.Sprintf("item-supplier #%d", *s.pickedItemSup)
	case s.pickedAssetID != nil:
		return "asset " + pickerClip(*s.pickedAssetID, 24)
	}
	return "freeform"
}

// failLines draws the failure's unbounded half under the headline the status
// row carries. The detail is an OMS response body, so it is CUT to what this
// block can hold before it is folded — folding a multi-KB gateway page is work
// whose result is thrown away, on a header rebuilt on every keystroke.
//
// Whatever the two cuts drop is MARKED, and the mark is spent on the LAST of
// the block's OWN rows rather than on a fourth one. This is a pinned header
// block: its height feeds bodyAvailForBar, so a block that grew a row when a
// reply landed could change whether the bar names PgUp/PgDn — the circularity
// receive_form.go's receiveNoteRows warns about. An error cut off mid-token
// with nothing saying more exists is the defect this conversion has spent its
// whole length removing, and this is the one step where losing the reason
// costs the operator the order.
//
// TWO wordings, because the two cuts know different things. The FOLD knows
// exactly how many lines it left behind, so it names the number. The
// cellPrefix bound does not: past it the fold only ever saw a PREFIX, so a
// count would be true of the prefix and not of the error — and naming a number
// that is only true of a fraction is the same false claim as marking nothing.
func (s *PurchaseOrderCreateScreen) failLines() []string {
	head, detail := s.failure()
	width := s.paneWidth() - len(jdeIndent)
	if width < 12 {
		width = 12
	}
	var lead []string
	if _, plan := s.statusPlan(); head != "" && !plan.drawsHead {
		// The status row is drawing something ELSE — the work in flight, or the
		// ANSWER to a keypress — so the headline comes here rather than nowhere.
		// It was nowhere for a round: `/` over a failed asset lookup answers
		// "type a name, tag or serial", which took the row and does not name the
		// failure, and the pane then said what had gone wrong only in the body's
		// own empty line.
		//
		// The question asked is which content the row CHOSE, not how much of it
		// fit. A row that is drawing this headline and merely shortened it is
		// answered by the shortening, and a second identically shortened copy
		// here would spend a body row saying nothing new (poStatusPlan.drawsHead).
		//
		// Bounded like everything else in this block, and the bound CARRIES ITS
		// ELLIPSIS: every other cut on this screen marks itself, and a headline
		// cut clean reads as a whole sentence — at 45 columns the pane is 20 and
		// "looking up this supplier's items failed" would end mid-word looking
		// finished.
		lead = []string{jdeIndent + StyleStatusError.Render(
			pickerClip(jdeStatusErrMark+jdeStatusOneLine(head), width))}
	}
	if detail == "" {
		return lead
	}
	trimmed := cellPrefix(detail, poFailDetailRows*width)
	bounded := trimmed != detail
	folded := pickerWrap(trimmed, width)

	keep, mark := folded, ""
	if bounded || len(folded) > poFailDetailRows {
		if len(keep) > poFailDetailRows-1 {
			keep = keep[:poFailDetailRows-1]
		}
		mark = "… more of the error than this pane can hold"
		if !bounded {
			mark = fmt.Sprintf("… %d more line(s) of the error", len(folded)-len(keep))
		}
	}
	out := make([]string, 0, poFailDetailRows+len(lead))
	out = append(out, lead...)
	for _, line := range keep {
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	if mark != "" {
		// Bounded like every other line here: the row that says something was
		// cut may not be the row that runs off the pane.
		out = append(out, jdeIndent+StyleMuted.Render(cellPrefix(mark, width)))
	}
	return out
}

// poFailDetailRows caps that detail. The sentence naming WHAT failed is on the
// status row and never gives; what a short terminal loses is the tail of the
// gateway's HTML, and the last of these rows says so. Same bound, same reason,
// as po_add_line's — whose own failLines does not yet carry the mark.
const poFailDetailRows = 3

// ---------------------------------------------------------------------------
// The action bar
// ---------------------------------------------------------------------------

// bar is the bar the frame is about to draw.
func (s *PurchaseOrderCreateScreen) bar() []actionBarItem {
	return s.barFor(len(s.headerLines()))
}

// barFor is bar for a frame with a KNOWN pinned header, and it is the one the
// key arms read.
//
// The two exist for the reason receive_form's pair does: the bar an operator
// obeys and the bar the screen is about to draw are not always the same bar.
// The header costs the body rows, the body's height is what decides whether
// PgUp/PgDn are named at all, and an arm can retire a note or a failure detail
// mid-dispatch — so a press has to be judged against the frame it was made ON.
// Update measures the header before the arms run and hands it down.
func (s *PurchaseOrderCreateScreen) barFor(headerRows int) []actionBarItem {
	return s.barItems(s.bodyPagesFor(headerRows))
}

// barItems is the bar for a given paging state, so the bar that is MEASURED is
// the bar that is DRAWN. Measuring against one wording and drawing another is
// how a block passes its own fit check and then overflows — this screen did it
// three times with the sentence version of this function.
//
// Every arm below is an ALLOW-LIST of what acts in the state being drawn. That
// is the direction the freeze had to be rewritten in once already: written as a
// list of what is FROZEN it froze the nine keys somebody thought of, and `d`,
// added in the same round, was free by default and re-focused the notes field
// the submit had just blurred.
func (s *PurchaseOrderCreateScreen) barItems(paging bool) []actionBarItem {
	var items []actionBarItem
	add := func(key, label string) { items = append(items, actionBarItem{key, label}) }
	move := func() {
		if s.rowCount() > 1 {
			add("UP/DN", "Move")
		}
		if paging {
			add("PgUp/PgDn", "Page")
		}
	}
	switch s.phase {
	case poPhaseSupplier:
		if !s.supplierListOnScreen() {
			// No rows: nothing to move onto and nothing to commit. Esc is the
			// one key that acts, so it is the one key named.
			add("Esc", "Cancel order")
			return items
		}
		switch {
		case s.pending && s.supplierHighlightIsCommitted():
			// The highlighted row is the supplier the order already carries, so
			// enter commits nothing and only goes back to the source chooser —
			// navigation, not a change, and the one way back into the order
			// from this frame while the POST is out.
			add("Enter", "Back to sources")
		case s.pending:
			// A DIFFERENT supplier would re-target a request that already names
			// the old one, so it is frozen with the rest of the payload.
		default:
			add("Enter", "Commit supplier")
		}
		move()
		add("Esc", "Cancel order")
	case poPhaseSupplierSwitch:
		add("Ctrl-X", "Drop & switch")
		add("Esc", "Keep cart")
		if paging {
			add("UP/DN", "Scroll")
		}
	case poPhaseAgreement, poPhaseWorkOrder, poPhaseCommittee:
		add("Enter", "Commit")
		move()
		add("Esc", "Keep current")
	case poPhaseSource:
		if !s.pending {
			// Everything that stages, removes, edits or re-attributes a line is
			// changing a cart finalize() has already copied into the payload;
			// the 201 navigates away and takes the change with it.
			add("r", "Reorder")
			add("i", "Inventory")
			add("a", "Assets")
			add("f", "Freeform")
			if s.agreementOffered() {
				add("g", "Agreement")
			}
			if s.assoc.workOrdersOffered() {
				add("w", "Work order")
			}
			if s.assoc.committeesOffered() {
				add("c", "Committee")
			}
		}
		if len(s.lines) > 0 {
			add("d", "Review")
			move()
			if !s.pending {
				add("Ctrl-E", "Edit line")
				add("Ctrl-X", "Remove line")
			}
		}
		add("Esc", "Supplier")
	case poPhaseReorderPick:
		if s.reorderListOnScreen() && len(s.reorderItems) > 0 {
			commit := "Add line"
			if len(s.reorderSelected) > 0 {
				// With rows marked, enter stages exactly those — a different
				// act, so a different word. The bar naming "Add line" over a
				// key that adds nine is the claim this pair exists to keep
				// honest.
				commit = fmt.Sprintf("Add %d marked", len(s.reorderSelected))
			}
			add("Enter", commit)
			add("Space", "Mark")
			add("a", "Add all")
			move()
		}
		add("b", "Line sources")
		add("Esc", "Cancel order")
	case poPhaseItemPick:
		if s.itemSuppliersTyping {
			// Enter's outcome depends on the MATCH COUNT, so the bar says which
			// of the two it is about to do — and names nothing at all over a
			// query that matched nothing, where enter can only decline.
			//
			// A single label for all three was the state this bar was in before
			// the conversion, promising "picks the match" over a search with
			// eleven matches and over one with none. Both halves matter: a key
			// named where it declines teaches a key that does not work, and a
			// key whose label describes a different act is the same lie with
			// more words.
			if s.itemListOnScreen() && len(s.itemSuppliers) > 0 {
				if len(s.itemSuppliers) == 1 {
					add("Enter", "Pick it")
				} else {
					add("Enter", "Close & choose")
				}
			}
			add("Esc", "Close search")
			return items
		}
		// The list on the pane decides what Enter and the movement keys can do;
		// '/' and 'r' are decided by their OWN predicates, because a filter
		// that matched nothing is a state where the list is empty and the box
		// still opens — the previous shape read the row count for all four and
		// dropped '/' from a frame where it was the operator's only way to fix
		// the query.
		if len(s.itemSuppliers) > 0 && s.itemListOnScreen() {
			add("Enter", "Pick item")
		}
		if s.itemSearchOpens() {
			add("/", "Search")
		}
		if !s.itemSuppliersLoad {
			// A walk is already out, so R would fire a second one for a reply
			// the first is going to deliver.
			retry := "Reload"
			if s.itemSuppliersErr != "" {
				retry = "Retry"
			}
			add("r", retry)
		}
		move()
		add("b", "Line sources")
		add("Esc", "Cancel order")
	case poPhaseAssetPick:
		if s.assetsTyping {
			if !s.assetsLoading {
				// Gated while a search is in flight: a second one would leave
				// the operator watching a lookup whose result is discarded.
				add("Enter", "Run search")
			}
			add("Esc", "Close search")
			return items
		}
		switch {
		case s.assetsLoading:
			add("/", "Search")
		case s.assetsErr != "":
			add("/", "Retry with a search")
		case len(s.assets) == 0:
			add("/", "Search")
		default:
			add("Enter", "Pick asset")
			add("/", "Search")
			move()
			if s.assetsHasNext {
				add("]", "Next page")
			}
			if s.assetsPage > 1 {
				add("[", "Prev page")
			}
		}
		add("b", "Line sources")
		add("Esc", "Cancel order")
	case poPhaseLine:
		commit := "Add to cart"
		if s.editIndex >= 0 {
			commit = "Save changes"
		}
		add("Enter", commit)
		move()
		if s.caseFlipOffered() {
			// The key moves BOTH typed rows, so the label names the entry basis
			// rather than the cost alone — a bar naming only the cost while the
			// quantity row also flipped would be understating what the key does.
			add("Ctrl-T", "Cases/units")
		}
		if s.editIndex >= 0 {
			add("Esc", "Cancel edit")
		} else {
			add("Esc", "Pick another source")
		}
	case poPhaseReview:
		if !s.pending {
			add("Enter", "Submit order")
		}
		move()
		if !s.pending {
			add("Ctrl-E", "Edit line")
			add("Ctrl-X", "Remove line")
		}
		add("Esc", "Back to sources")
	}
	return items
}

// bodyPagesFor reports whether PgUp/PgDn do anything on a frame whose pinned
// header is headerRows tall.
//
// TWO conditions, because the keys make two claims and both have to hold. The
// body must MOVE — bodyScrollsForBar's question, asked of the LAYER so a bar's
// claim and the window that decides it cannot part company. And a page moves
// the CURSOR (jdePageCursor) rather than the body, so there has to be another
// row to land on. They agree in almost every state and come apart in the ones
// this screen has by design: a picker drawing a working or failure frame has a
// body that can overflow and no rows at all.
//
// The bar passed to the layer is the bar WITH the paging keys on it, because
// the tallest bar is the fixed point: a body that overflows the smallest budget
// also overflows the larger one left when the keys are dropped, so the answer
// cannot oscillate between frames.
func (s *PurchaseOrderCreateScreen) bodyPagesFor(headerRows int) bool {
	if s.phase == poPhaseLine {
		// A FIELD form has nothing to page. Its cursor WRAPS (focusNextLine),
		// so UP/DN reaches every one of its three or four rows in at most three
		// presses and the window follows the cursor — nothing is out of reach.
		// jdePageCursor clamps on purpose, so at a height where the fields
		// outrun the pane PgUp on the first row blurred and re-focused the same
		// field while the bar named the key: a named key with no visible
		// effect. Wrapping the page instead would contradict the layer, which
		// clamps so a page cannot lose the operator's place, so the key is not
		// offered at all. ONE predicate, read by barItems through barFor and by
		// moveCursor's paging arm, so the bar and the arm cannot disagree.
		return false
	}
	if s.phase == poPhaseSupplierSwitch {
		// The confirm has no cursor: its body is read-only and UP/DN scroll it,
		// so the second condition — "is there another row to land on" — is not
		// one it has. What is left is the layer's own question.
		return s.bodyScrollsForBar(s.switchBody(), headerRows, s.barItems(true))
	}
	if s.rowCount() <= 1 {
		return false
	}
	body, _ := s.body()
	return s.bodyScrollsForBar(body, headerRows, s.barItems(true))
}

// pageStep is how many rows one page covers on the frame being drawn: measured
// off the same window that frame draws, so a page moves by exactly what the
// operator could see. The arithmetic is the LAYER's, not a local copy.
func (s *PurchaseOrderCreateScreen) pageStep(headerRows int) int {
	body, cursor := s.body()
	return s.windowRowsForBar(body, cursor, headerRows, s.barItems(true))
}

// ---------------------------------------------------------------------------
// The cursor, said once
// ---------------------------------------------------------------------------
//
// Every phase's movement keys read these three rather than the eight cursor
// fields behind them. It is what lets UP/DN, PgUp/PgDn and the bar's claim
// about them be written ONCE — the eight copies they replace had already
// drifted into eight slightly different edge cases, and the bar could only
// name the pair by asking each phase separately.

// moveCursor honours UP/DN and PgUp/PgDn on any phase that has a cursor, and
// is the ONE place the bar's claim about those keys is kept.
//
// It returns whether the key BELONGED to movement, not whether the cursor
// actually moved: a highlight resting against an edge it cannot pass has
// already answered the press — the row is drawn at the end of the list and the
// "more above / more below" marker is absent — so a sentence there would be
// noise on a frame that is already correct (poAddSilentKeys records the same
// judgement one screen over).
//
// Paging is gated on the LAYER's answer, not on a second opinion about it:
// bodyPagesFor asks bodyScrollsForBar, which is the same expression the window
// itself short-circuits on, so a bar naming PgUp/PgDn and a body that moves
// cannot part company. Without the gate the keys would still page the cursor
// on a body that fits, which is a key acting where the bar does not name it.
func (s *PurchaseOrderCreateScreen) moveCursor(m tea.KeyMsg, headerRows int) (bool, tea.Cmd) {
	if s.rowCount() <= 1 {
		// Nothing to move between. The bar names neither pair here, so both
		// have to be inert — an arm that moved a cursor with one row to move it
		// through would be a key acting unnamed.
		return false, nil
	}
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		// The pane is too short for the layer to draw this frame at all, so
		// there is no highlight on screen for a movement key to move. It still
		// BELONGS to movement — which is what the true says — so the key is
		// swallowed here rather than falling through to an arm that would read
		// it as something else.
		//
		// It answers with NOTHING, deliberately: the refusal notice is the
		// whole pane and is the standing answer to every key on it. A note set
		// here would not be drawn now and WOULD be drawn when the terminal
		// grows back, which is a stale reply to a press the operator made
		// before the resize.
		switch m.String() {
		case "up", "down", "pgup", "pgdown":
			return true, nil
		}
		return false, nil
	}
	switch m.String() {
	case "up":
		s.setCursorRow(s.cursorRow() - 1)
		return true, nil
	case "down":
		s.setCursorRow(s.cursorRow() + 1)
		return true, nil
	case "pgup", "pgdown":
		if !s.bodyPagesFor(headerRows) {
			return false, nil
		}
		dir := 1
		if m.String() == "pgup" {
			dir = -1
		}
		s.setCursorRow(jdePageCursor(s.cursorRow(), s.rowCount(), s.pageStep(headerRows), dir))
		return true, nil
	}
	return false, nil
}

// rowCount is how many NAVIGABLE rows the phase being drawn has. Zero on a
// frame whose list is not on the pane, which is what stops the bar naming a
// movement key over a working or failed picker.
func (s *PurchaseOrderCreateScreen) rowCount() int {
	switch s.phase {
	case poPhaseSupplier:
		if !s.supplierListOnScreen() {
			return 0
		}
		return len(s.suppliers)
	case poPhaseAgreement:
		return s.agreementRows()
	case poPhaseWorkOrder:
		return len(s.workOrderRows())
	case poPhaseCommittee:
		return len(s.committeeRows())
	case poPhaseSource, poPhaseReview:
		return len(s.lines)
	case poPhaseReorderPick:
		if !s.reorderListOnScreen() {
			return 0
		}
		return len(s.reorderItems)
	case poPhaseItemPick:
		if !s.itemListOnScreen() {
			return 0
		}
		return len(s.itemSuppliers)
	case poPhaseAssetPick:
		if !s.assetListOnScreen() {
			return 0
		}
		return len(s.assets)
	case poPhaseLine:
		return len(s.lineFields())
	}
	return 0
}

// cursorRow is the navigable row the window is anchored on. Clamped rather
// than trusted: supplierCursor starts at -1 (nothing picked yet) and a list
// that shrank under a reply can leave any of these past its end, and a cursor
// outside the body makes jdeLines.block answer the TOP of the body — which
// would silently unpin the window from the row the operator is standing on.
func (s *PurchaseOrderCreateScreen) cursorRow() int {
	var cur int
	switch s.phase {
	case poPhaseSupplier:
		cur = s.supplierCursor
	case poPhaseAgreement:
		cur = s.agreementCursor
	case poPhaseWorkOrder:
		cur = s.workOrderCursor
	case poPhaseCommittee:
		cur = s.committeeCursor
	case poPhaseSource, poPhaseReview:
		cur = s.reviewCursor
	case poPhaseReorderPick:
		cur = s.reorderCursor
	case poPhaseItemPick:
		cur = s.itemSuppliersCursor
	case poPhaseAssetPick:
		cur = s.assetsCursor
	case poPhaseLine:
		for i, f := range s.lineFields() {
			if f == s.lineFocused {
				cur = i
				break
			}
		}
	}
	return jdeClampPick(cur, s.rowCount())
}

// setCursorRow moves the phase's cursor to row n, clamped into the body.
//
// The LINE form is not here: it moves focus rather than a highlight, and both
// ways in were closed — updateLinePhase answers up / down / tab / shift+tab
// ahead of moveCursor (focusNextLine, which wraps) and bodyPagesFor gates
// pgup / pgdown off that phase — so moveCursor cannot reach this function while
// the line form is drawn.
func (s *PurchaseOrderCreateScreen) setCursorRow(n int) {
	n = jdeClampPick(n, s.rowCount())
	switch s.phase {
	case poPhaseSupplier:
		s.supplierCursor = n
	case poPhaseAgreement:
		s.agreementCursor = n
	case poPhaseWorkOrder:
		s.workOrderCursor = n
	case poPhaseCommittee:
		s.committeeCursor = n
	case poPhaseSource, poPhaseReview:
		s.reviewCursor = n
	case poPhaseReorderPick:
		s.reorderCursor = n
	case poPhaseItemPick:
		s.itemSuppliersCursor = n
	case poPhaseAssetPick:
		s.assetsCursor = n
	}
}

// ---------------------------------------------------------------------------
// The bodies
// ---------------------------------------------------------------------------

// body is the phase's scrollable body and the row the window is anchored on,
// answered in ONE place so a sweep asking what the frame draws cannot end up
// asking a different builder from the one View picks. A phase added to the
// iota arrives here or it draws nothing at all.
func (s *PurchaseOrderCreateScreen) body() (*jdeLines, int) {
	switch s.phase {
	case poPhaseSupplier:
		return s.supplierBody(), s.cursorRow()
	case poPhaseSupplierSwitch:
		return s.switchBody(), 0
	case poPhaseAgreement:
		return s.agreementBody(), s.cursorRow()
	case poPhaseWorkOrder:
		return s.assocBody(s.workOrderRows(), s.cursorRow()), s.cursorRow()
	case poPhaseCommittee:
		return s.assocBody(s.committeeRows(), s.cursorRow()), s.cursorRow()
	case poPhaseReorderPick:
		return s.reorderBody(), s.cursorRow()
	case poPhaseItemPick:
		return s.itemBody(), s.cursorRow()
	case poPhaseAssetPick:
		return s.assetBody(), s.cursorRow()
	case poPhaseLine:
		return s.lineBody(), s.cursorRow()
	case poPhaseSource, poPhaseReview:
		return s.cartBody(), s.cursorRow()
	}
	// Every phase of the iota is named above, so this is unreachable today —
	// and it draws NOTHING rather than falling through to the cart, because a
	// phase added later would otherwise draw a cart while rowCount(), which has
	// no default, answered 0 for it: the bar and the body disagreeing about
	// whether there are rows at all.
	return poEmptyBody(s.paneWidth(), "This screen has nothing to draw here."), 0
}

// poPickRow draws one option row the way jdePickList draws its own: a caret and
// the reverse-video highlight on the cursor, four cells of gutter otherwise.
// One function, so no list on this screen can be the one that forgets the
// caret — which is what windowedListRoom is reserving on EVERY row.
func poPickRow(i, cursor int, text string) string {
	if i == cursor {
		return StyleSidebarItemActive.Render("  ▸ " + text)
	}
	return "    " + text
}

// poRowRoom is the cells one of those rows may draw into on this pane.
func (s *PurchaseOrderCreateScreen) poRowRoom() int {
	return windowedListRoom(s.paneWidth())
}

// emptyBody is the body a phase draws when it has no rows: ONE muted line
// saying what the list is, so "the list is empty" and "the window scrolled off
// the rows" can never look the same. It is jdeNoRow, which is safe for exactly
// the reason a lead-in line is not — there is no navigable row below it for the
// window to anchor on and strand it behind.
func poEmptyBody(pane int, what string) *jdeLines {
	lines := jdeCaveatLines(what, pane)
	if len(lines) == 0 {
		// A body with no lines at all is not the same thing as an empty state:
		// the frame would pad the pane out and the operator would be looking at
		// a blank rectangle with no way to tell it from a load that had not
		// started. One blank line is what keeps the block a block.
		lines = []string{""}
	}
	l := &jdeLines{}
	for _, line := range lines {
		l.Add(line)
	}
	return l
}

func (s *PurchaseOrderCreateScreen) supplierBody() *jdeLines {
	switch {
	case s.supplierLoading:
		return poEmptyBody(s.paneWidth(), "The supplier list is on its way.")
	case s.supplierLoadErr != "":
		return poEmptyBody(s.paneWidth(), "No supplier list — the lookup failed.")
	case len(s.suppliers) == 0:
		return poEmptyBody(s.paneWidth(), "No suppliers are configured in OpenMakerSuite.")
	}
	l := &jdeLines{}
	cur, room := s.cursorRow(), s.poRowRoom()
	for i, sup := range s.suppliers {
		// The id is what an operator reads back to confirm they are on the
		// right supplier, so the NAME is what gives.
		l.AddRow(i, poPickRow(i, cur, poFitRow(room, sup.Name, fmt.Sprintf("  (#%d)", sup.ID))))
	}
	return l
}

// switchBody is the destructive confirm's prose. It carries no keys: those are
// on the bar, which never gives, and putting them here as well is how this
// screen used to end up with two statements of one claim drifting apart.
func (s *PurchaseOrderCreateScreen) switchBody() *jdeLines {
	scoped := s.supplierScopedLineCount()
	to := "the highlighted supplier"
	if s.supplierCursor >= 0 && s.supplierCursor < len(s.suppliers) {
		if name := s.suppliers[s.supplierCursor].Name; name != "" {
			to = pickerClip(name, 20)
		}
	}
	prose := fmt.Sprintf("%d of %d staged line(s) name items only %s sells, so %s cannot fill them.",
		scoped, len(s.lines), s.supplierLabel(), to)
	if kept := len(s.lines) - scoped; kept > 0 {
		prose += fmt.Sprintf(" The other %d line(s) stay.", kept)
	}
	return poEmptyBody(s.paneWidth(), prose)
}

// agreementBody is the optional agreement picker: a leading "no agreement" row
// followed by the supplier's active agreements.
//
// The picked agreement's NOTES — the terms, which are the reason to cite one —
// are tagged onto the cursor's own row rather than hung off the bottom of the
// list. That is the receiving form's rule: what a row needs is drawn ON that
// row and AFTER its field, so the window keeps the row and its explanation
// together instead of stranding one of them.
func (s *PurchaseOrderCreateScreen) agreementBody() *jdeLines {
	l := &jdeLines{}
	cur, room, pane := s.cursorRow(), s.poRowRoom(), s.paneWidth()
	for i := 0; i < s.agreementRows(); i++ {
		if i == 0 {
			l.AddRow(0, poPickRow(0, cur, "— no agreement —"))
			continue
		}
		a := s.agreements[i-1]
		// An OMS-supplied name with nothing beside it: the whole row is the
		// identifier, so it abbreviates rather than being cut.
		l.AddRow(i, poPickRow(i, cur, pickerClip(a.Name, room)))
		if i != cur {
			continue
		}
		for _, line := range jdeCaveatLines(strings.TrimSpace(a.Notes), pane) {
			l.AddRow(i, line)
		}
	}
	return l
}

// assocBody draws a work-order / committee picker. One renderer for both, so
// the two associations can never diverge in how they are picked.
func (s *PurchaseOrderCreateScreen) assocBody(rows []poAssocOption, cur int) *jdeLines {
	if len(rows) == 0 {
		return poEmptyBody(s.paneWidth(), "Nothing to attach this order to.")
	}
	l := &jdeLines{}
	room := s.poRowRoom()
	for i, r := range rows {
		l.AddRow(i, poPickRow(i, cur, pickerClip(r.label, room)))
	}
	// The caveat travels with the LAST row, so a body that overflows loses it
	// from the tail rather than stranding the first row behind it.
	for _, line := range jdeCaveatLines(
		"Records who this order is for. It does not change stock, pricing, or what a committee is billed.",
		s.paneWidth()) {
		l.AddRow(len(rows)-1, line)
	}
	return l
}

// lineBody is the line-entry form: one navigable row per active field, sized
// and bounded by the LAYER (AddFittedFields → jdeFitRow → jdeFitInputValue).
//
// That is the whole of what replaced this screen's own field layout. The rows
// used to be "▸ Label: " plus a textinput the screen had sized against a fixed
// 51-column pane, which is a bound computed against a width the terminal may
// not have — too narrow at 120 columns and, before poInputWidth existed at all,
// unbounded. The layer sizes the row against the pane it is really drawing
// into, folds a hint that will not fit onto a line of its own, and keeps the
// caret inside the field on every width.
func (s *PurchaseOrderCreateScreen) lineBody() *jdeLines {
	fields := s.lineFields()
	pane := s.paneWidth()
	rows := make([]jdeField, 0, len(fields))
	for _, i := range fields {
		label := s.lineFieldLabel(i)
		box := s.lineInputs[i]
		rows = append(rows, jdeField{
			Label: label, Kind: jdeText, Input: &box, Width: poFieldWidth,
			Hint: s.lineFieldHint(i), Focused: i == s.lineFocused,
		})
	}
	lw := jdeLabelWidth(rows)
	l := &jdeLines{}
	for pos, f := range rows {
		// ONE row at a time, so a caveat lands directly UNDER the field it is
		// about. AddFittedFields over the whole block first and the caveats
		// after it put every note at the foot of the form, tagged to rows they
		// were nowhere near — which is the shape that strands a note behind a
		// window, and it is also simply the wrong thing to read.
		l.AddFittedFields([]jdeField{f}, lw, pane, pos)
		for _, line := range jdeNoteLines(s.lineFieldCaveat(fields[pos]), lw, pane) {
			l.AddRow(pos, line)
		}
	}
	return l
}

// lineFieldHint is the short note that rides on a field row: the format it
// takes, or what leaving it blank means.
func (s *PurchaseOrderCreateScreen) lineFieldHint(i int) string {
	switch i {
	case poLineFieldQty:
		// What ONE of the thing in the box contains. On a case-packed line at
		// case basis this is the fact that makes "2" checkable before it is
		// committed; at unit basis it still says what the vendor's case is, so
		// the operator can see the row is in the smaller unit.
		if s.pickedItemSup != nil && poCasePacked(s.pickedQPP) {
			return poPackFact(s.pickedQPP)
		}
		return ""
	case poLineFieldCost:
		// An item-supplier line's cost is an OVERRIDE the operator may leave
		// empty; an asset or freeform line's is required, because the backend
		// rejects those two branches without one. Same predicate the payload is
		// built from (addLine), so the row cannot promise what the wire refuses.
		if s.pickedItemSup != nil {
			return "optional"
		}
		return "required"
	case poLineFieldDate:
		return "YYYY-MM-DD"
	}
	return ""
}

// lineFieldCaveat is the longer note under a field: what a blank cost actually
// does, and the case/unit derivation a case-packed line is being priced by.
//
// Both were sheet-level hints printed under the whole form, which is where a
// note nobody ties to a field goes to be ignored — and, once the body could
// scroll, where it goes to be stranded. They belong to their rows now.
func (s *PurchaseOrderCreateScreen) lineFieldCaveat(i int) string {
	switch i {
	case poLineFieldCost:
		hint := s.entryDerivation()
		if s.pickedItemSup != nil {
			if hint != "" {
				return hint + " · blank prices this line from the supplier catalog"
			}
			return "Blank prices this line from the supplier catalog at save time."
		}
		// Asset / freeform lines carry no date field — say why, so its absence
		// does not read as an oversight. This is the LAST row the form draws for
		// those two shapes (lineFields), and the row the missing date field
		// would have followed, so a caveat about an absent row can sit here
		// without pretending to belong to one that is present. It cannot
		// collide with the note above it, which is an item-supplier note and
		// this branch is only reached with pickedItemSup nil.
		//
		// The line total is APPENDED to it and never drawn INSTEAD of it. This
		// used to return the derivation first, so the moment the operator
		// filled BOTH rows — which is exactly the state they are in when they
		// press enter — plainLineTotal answered non-empty and the standing
		// sentence vanished from the last row of the form. Two true statements
		// about one row are not a choice between them: both fold onto the
		// row's own note block (jdeNoteLines → AddRow), so neither is stranded
		// off a navigable row, and the caveat leads because it is the fact the
		// operator cannot derive for themselves.
		caveat := "Expected dates are stored on inventory lines only; set this line's dates at send/receive."
		if hint != "" {
			return caveat + " · " + hint
		}
		return caveat
	case poLineFieldDate:
		return ""
	}
	return ""
}

// ---------------------------------------------------------------------------
// The cart
// ---------------------------------------------------------------------------

// cartBody is the staged cart, one navigable row per line and nothing else.
// What the cart COMES TO is not here: it is pinned above the block by
// cartTotalRows, for the reason recorded there.
//
// It is the same block on the source chooser and on review, and it is where the
// biggest piece of this screen's own machinery went. The cart used to be
// windowed by poCartWindow against a budget computed twice — once to decide
// whether to draw the rows at all (cartListedOnScreen / sourceCartSpace) and
// once to draw them (reviewCartSpace) — with a whole substitute sentence
// (cartHiddenSentence) and four gated keys (j/k/x/ctrl+e, through
// cartHiddenNote) for the case where the rows did not fit. jdeLines.Window
// anchors on the CURSOR's block, so the highlighted row is on the pane by
// construction and there is no such case: Ctrl-E and Ctrl-X can no longer be
// pressed against a line the operator cannot see, which is the wrong-purchase-
// order failure the whole apparatus existed to prevent.
func (s *PurchaseOrderCreateScreen) cartBody() *jdeLines {
	if len(s.lines) == 0 {
		return poEmptyBody(s.paneWidth(), "Nothing staged yet.")
	}
	l := &jdeLines{}
	cur, pane := s.cursorRow(), s.paneWidth()
	for i := range s.lines {
		l.AddRow(i, s.cartRow(i, cur, pane))
	}
	return l
}

// cartTotalRows are what the cart COMES TO, pinned above it rather than drawn
// under its last line.
//
// They were tagged onto the last row at first, on the reasoning that anything
// left over hangs off the last row and End brings it back. That is the right
// rule for a decoration and the wrong one for these two: the total is the
// figure the review phase EXISTS to confirm, and the caveat is what makes it a
// floor rather than the order's value — "1 line is priced from the supplier
// catalog" is the difference between a $14 order and one nobody has priced.
// Hanging off row N-1 means the block is the row plus three folded lines, which
// jdeLines.Window keeps the START of when it cannot fit them all, so at 80x24
// under a five-line cart with three attribution rows the caveat was two lines
// past the bottom with `↓ 2 more below` where it had been. A fact an operator
// has to press a key to see is a fact they will confirm the order without.
//
// Context rank, not essential: the PO-notes box has that slot on review, and
// what a pane too short for both loses is a figure the operator can still reach
// by leaving review — where a trimmed notes box would be a field taking input
// nobody can see.
func (s *PurchaseOrderCreateScreen) cartTotalRows() []string {
	if len(s.lines) == 0 {
		return nil
	}
	pane := s.paneWidth()
	total, noCost := poCartTotal(s.lines)
	money := fmtMoney(total)
	if noCost > 0 {
		// The sum is a FLOOR: a line with no cost is priced from the supplier
		// catalog at save time, so reporting it flat would read as free.
		money = "at least " + money
	}
	noun := "line items"
	if len(s.lines) == 1 {
		noun = "line item"
	}
	count := fmt.Sprintf("  (%d %s)", len(s.lines), noun)
	room := pane - len(jdeIndent) - lipgloss.Width(count)
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	out := []string{jdeIndent + StyleTitle.Render(fitCell("Total: "+money, room)) +
		StyleMuted.Render(count)}
	if noCost > 0 {
		out = append(out, jdeCaveatLines(poCartCaveat(noCost), pane)...)
	}
	return out
}

// cartRow draws one staged line.
//
// This row is the review phase's whole job, so it gives ground in a STATED
// order rather than letting clampToBox choose: the LABEL first (an OMS-supplied
// identifier the operator still recognises shortened), then the expected DATE
// (optional per line, and the line form still holds it in full), and only then
// may the BADGE abbreviate. The index, the quantity and the price NEVER give —
// they are the facts review exists to confirm — and whatever is shortened
// carries the ellipsis that says so, because a silently cut row reads as a
// whole one and "@ 3." reads as a price.
//
// Clipping the label alone was not enough: an item-supplier line is the only
// shape that CAN carry an expected date (lineTakesDate) and it is also the
// shape carrying the 14-cell "Inventory item" badge, so the fixed parts alone
// came to 52 cells and the pane took the badge on an ordinary line.
func (s *PurchaseOrderCreateScreen) cartRow(i, cursor, pane int) string {
	l := s.lines[i]
	prefix := fmt.Sprintf("%d) ", i+1)
	facts := poCartFacts(l)
	date := ""
	if l.item.ExpectedShipmentDate != "" {
		date = "  exp " + l.item.ExpectedShipmentDate
	}
	badgeName := poLineTypeLabel(poCartLineType(l))
	badge := "  [" + badgeName + "]"
	// The caret gutter and the highlight's padding are reserved on EVERY row,
	// not only the highlighted one: a row that fits until it is selected is a
	// row the pane cuts on exactly the press that stages it.
	room := windowedListRoom(pane) - lipgloss.Width(prefix)
	// What the label costs once it has given everything it can: its own width,
	// or the floor, whichever is smaller. Asking for the floor outright would
	// make a THREE-cell name demand six and abbreviate a badge that fitted.
	need := lipgloss.Width(l.label)
	if need > poHeaderValueFloor {
		need = poHeaderValueFloor
	}
	spent := func() int {
		return need + lipgloss.Width(facts) + lipgloss.Width(date) + lipgloss.Width(badge)
	}
	if spent() > room && date != "" {
		date = poCartDateMark
	}
	if spent() > room {
		badge = poCartBadge(badgeName, room-need-lipgloss.Width(facts)-lipgloss.Width(date))
	}
	label := room - lipgloss.Width(facts) - lipgloss.Width(date) - lipgloss.Width(badge)
	if label < need {
		label = need
	}
	return poPickRow(i, cursor, prefix+pickerClip(l.label, label)+facts+date+badge)
}

// poCartFacts is the quantity-and-price half of a cart row, stated in the unit
// the line is BOUGHT in.
//
// A case-packed line reads "×2 cs @ $30.00": two of the vendor's cases at the
// price of one case, which is what the operator typed and what they will be
// invoiced for. Rendering it as the base "×24 @ $1.25" is not wrong, but it is
// the one arithmetic the reported defect turns on, and an operator checking a
// cart against a quote has nothing to compare it with. The `cs` IS the label —
// a bare number beside a price is exactly the row that took a case cost while
// meaning a unit cost.
//
// The base-unit form is kept for every other line, and also for a case-packed
// line whose staged quantity is not a whole number of cases: that line has no
// case count, and rounding one for the display would put a figure on the review
// pane that the payload does not carry.
func poCartFacts(l poCartLine) string {
	qty := poQtyFact(l.item.Quantity, l.qpp)
	facts := "  ×" + qty
	if l.item.UnitCost != nil {
		// The price follows the QUANTITY's denominator, so the row's own
		// arithmetic holds: "×2 cs @ $48" multiplies out to the $96 the cart
		// total reports. Pairing a case count with a per-unit price would be
		// two denominators on one row, which is the reported defect with an
		// extra step.
		shown := *l.item.UnitCost
		if strings.HasSuffix(qty, " cs") {
			shown = poCaseFromUnit(shown, l.qpp)
		}
		// Through poDisplayMoney, never FormatFloat at -1 precision. The case
		// price is a float64 multiply of a value produced by a float64 divide,
		// and that round trip is not exact for ordinary money: a case of 24
		// entered at 12.01 stores 0.5004166666666667 per unit and came back
		// here as "$12.009999999999998" — twenty cells of binary noise on the
		// one surface whose purpose is checking the order against the vendor's
		// quote, and on the part of the row that NEVER gives.
		facts += fmt.Sprintf(" @ $%s", poDisplayMoney(shown))
	}
	return facts
}

// poCartDateMark is what an expected shipment date shrinks to when the row
// cannot hold it: the fact that the line HAS one, plus the ellipsis that says
// the value was taken. The full date is one Ctrl-E away on the line form.
const poCartDateMark = "  exp…"

// poCartBadge renders the line-type badge inside `room` cells, abbreviating the
// type name rather than letting the pane cut the bracket off. It is the LAST
// thing on the row to give, after the label and the date.
func poCartBadge(name string, room int) string {
	const brackets = 4 // "  [" + "]"
	if room >= lipgloss.Width(name)+brackets {
		return "  [" + name + "]"
	}
	inner := room - brackets
	if inner < 2 {
		inner = 2
	}
	return "  [" + pickerClip(name, inner) + "]"
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
// line — and reports how many lines carry NO cost at all.
//
// The second return is not a detail: a catalog line with a blank cost field is
// priced by the backend from the item-supplier's stored unit_cost at save time,
// so the sum here is a FLOOR rather than the order's value. Reporting it flat
// would read as "these lines are free", which is the one thing a
// confirm-before-submit surface may not say.
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

// poCartCaveat is the ONE wording of the catalog-priced caveat.
//
// It is kept to ONE folded row on a 51-column pane, and that is a budget rather
// than a preference: it rides in the pinned header directly under the total,
// on the two phases whose header is already the tallest in the app, and every
// row it takes comes out of a body that is showing the operator their cart. The
// previous wording ("… — not included above") was 61 cells and folded to two,
// which at 80x24 under three attribution rows left the body ONE row — and a
// one-row window is the one case jdeLines.Window draws no "N more below"
// marker in, so a 15-line cart showed a single line and said nothing about the
// rest. What the tail said is already on the total beside it: "at least".
func poCartCaveat(noCost int) string {
	if noCost == 1 {
		return "1 of them is priced from the catalog"
	}
	return fmt.Sprintf("%d of them are priced from the catalog", noCost)
}

// ---------------------------------------------------------------------------
// Failures and declines
// ---------------------------------------------------------------------------

// setErr records the screen's failure: what went wrong, and the unbounded half
// underneath it. Both fields, here and only here, so a detail can never outlive
// the headline it belonged to — a 502's HTML body left standing under "quantity
// must be a positive integer" would read as the gateway explaining the
// validation, which is a worse lie than either sentence alone.
//
// Cleared by every phase change (Update's key dispatch) as well as by the three
// arms that stage or retire a line, because an order-level error left standing
// takes statusPlan's whole row on every later phase.
//
// The HEADLINES are written short, the way poSubmitWords was cut from 32 cells
// to 20: they share a 51-cell row that cannot fold, and the filler goes first
// ("submitting this purchase order failed" was 37 and is 22; "add at least one
// line before submitting" was 38 and is 28). The quantity refusal is left as it
// stands — "must be a positive integer" carries no filler to cut, and trading
// "integer" for a shorter word that admits 1.5 would buy cells with meaning.
// The COST refusal is the same rule learned the hard way: cut to "must be 0 or
// more" it stopped naming the fault it fires on most, because poDeriveUnitCost
// raises one error for a value it could not parse AND for one below zero, so
// `abc` came back as a number out of range. It says "must be a non-negative
// number" again, which is what errInvalidCost and po_line_price.go's
// poCostRejectedReason say, so one validation has one wording. FILLER is what
// may be cut; a word carrying one of the faults is not filler.
//
// SHORTENING IS NOT THE GUARANTEE, and recording that here is the point of this
// paragraph. The detail beside these headlines is an OMS response body of any
// length, and a bound expressed in terms of an unbounded value is not a bound;
// what makes the row safe is statusPlan refusing to lead an order-level error
// at all. These cuts only buy room for the headline and its neighbours in the
// common case.
func (s *PurchaseOrderCreateScreen) setErr(what, detail string) {
	s.errMsg, s.errDetail = what, detail
}

// pendingDecline is how every frozen arm answers while the create POST is out:
// it records what the key did on the WORKING line and flashes the same
// sentence. One writer, so the three frozen phases cannot drift into three ways
// of saying the submit owns the cart, and so the lead — which NAMES the key,
// because two keys sharing one sentence would redraw the pane the first press
// left — is always set beside the status it goes with.
//
// WHERE the lead is drawn depends on the phase, and the two surfaces have
// DIFFERENT survival arguments, so it is worth saying which is which. On REVIEW
// — the one frozen phase that pins a typed box (the PO notes) — it leads the
// working sentence on the layer's STATUS ROW, which is outside the header
// budget and never gives. On the supplier picker and the source chooser nothing
// is pinned, so the lead takes the pinned header's ESSENTIAL row instead
// (headerLines), which survives because jdeFitHeader gives ground by rank and
// keeps the essential one down to the smallest budget a frame is drawn at. Both
// hold at every drawable height; neither argument covers the other's phases.
func (s *PurchaseOrderCreateScreen) pendingDecline(lead string) tea.Cmd {
	s.pendingLead = lead
	return Status("the submit is already out", StatusInfo)
}

// supplierLabel names the committed supplier for the working lines. A status
// line that names the supplier is the difference between "something is
// happening" and "we are asking OMS what Acme sells". Falls back to the id, and
// then to a generic noun, so the sentence is never left with a hole in it.
func (s *PurchaseOrderCreateScreen) supplierLabel() string {
	for _, sup := range s.suppliers {
		if sup.ID == s.supplierID && sup.Name != "" {
			// Bounded: the label goes inside a one-line status sentence that the
			// layer then bounds again, so a long supplier name would spend the
			// whole row and leave "Looking up the items Ac…".
			return pickerClip(sup.Name, 20)
		}
	}
	if s.supplierID > 0 {
		return fmt.Sprintf("supplier #%d", s.supplierID)
	}
	return "this supplier"
}
