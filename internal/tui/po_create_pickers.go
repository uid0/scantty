// po_create_pickers.go — Phase 3 of the New PO state machine.
//
// Holds the three picker phases (reorder queue, inventory items,
// assets) so po_create.go stays focused on the supplier picker, the
// source-chooser menu, and the line-entry form. Each phase shares
// the same shape: load list → render windowed list → j/k navigate,
// enter to commit, b to go back, esc to cancel. The inventory and
// assets pickers add `/` for filter / search.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ---------------------------------------------------------------------------
// Async loaders + msg types
// ---------------------------------------------------------------------------

// Every picker reply echoes the supplierID it was asked about, and
// handlePickerLoaded drops one whose supplier is no longer the order's —
// the same guard poAgreementsLoadedMsg already carries.
//
// It is not a nicety on these three. Every row they carry is scoped to a
// supplier, and an ItemSupplier id belongs to exactly one: a reply for supplier
// A landing after the operator has moved to supplier B would repaint B's picker
// with A's catalog and report it as a success, and the line staged from it
// names an item-supplier B does not sell. That is a wrong purchase order with
// nothing on screen to flag it. The catalog load is now N sequential page
// requests rather than one, so the window is wide enough to hit.
type poReorderItemsLoadedMsg struct {
	supplierID int
	items      []omsapi.ReorderDataItem
	err        error
}

type poItemSuppliersLoadedMsg struct {
	supplierID int
	rows       []omsapi.ItemSupplier
	err        error
}

// poAssetsLoadedMsg carries a request generation as well as the supplier,
// because the asset picker is the one picker whose reply depends on more than
// the supplier: it answers a query and a page. Two lookups for the SAME
// supplier therefore cannot be told apart by supplierID, and the in-flight flag
// that used to keep there from being two is cleared by
// resetSupplierScopedPickers, so a supplier round trip reopened the race.
type poAssetsLoadedMsg struct {
	supplierID int
	seq        int
	rows       []omsapi.Asset
	hasNext    bool
	err        error
}

func (s *PurchaseOrderCreateScreen) loadReorderItemsForSupplier() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		data, err := deps.OMS.GetReorderData(ctx)
		if err != nil {
			return poReorderItemsLoadedMsg{supplierID: supplierID, err: err}
		}
		// reorder_data is grouped by supplier — pick our slice.
		for _, sup := range data.Suppliers {
			if sup.ID == supplierID {
				return poReorderItemsLoadedMsg{supplierID: supplierID, items: sup.Items}
			}
		}
		return poReorderItemsLoadedMsg{supplierID: supplierID, items: nil}
	}
}

func (s *PurchaseOrderCreateScreen) loadItemSuppliersForSupplier() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		rows, err := deps.OMS.ListItemSuppliersForSupplier(ctx, supplierID)
		if err != nil {
			return poItemSuppliersLoadedMsg{supplierID: supplierID, err: err}
		}
		return poItemSuppliersLoadedMsg{supplierID: supplierID, rows: rows}
	}
}

func (s *PurchaseOrderCreateScreen) loadAssetsForSupplier(search string) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	page := s.assetsPage
	if page <= 0 {
		page = 1
	}
	// Stamped here rather than at the five arms that fire a load, so a sixth
	// one cannot be added without a generation: this is the only place an asset
	// request is built.
	s.assetsSeq++
	seq := s.assetsSeq
	return func() tea.Msg {
		p, err := deps.OMS.ListAssetsForSupplier(ctx, supplierID, search, page)
		if err != nil {
			return poAssetsLoadedMsg{supplierID: supplierID, seq: seq, err: err}
		}
		hasNext := p.Next != nil && *p.Next != ""
		return poAssetsLoadedMsg{supplierID: supplierID, seq: seq, rows: p.Results, hasNext: hasNext}
	}
}

// pickerReplySupplier reports which supplier a picker reply was asked about.
// One place, so a fourth picker message cannot be added without the question
// being asked of it too.
func pickerReplySupplier(msg tea.Msg) (int, bool) {
	switch m := msg.(type) {
	case poReorderItemsLoadedMsg:
		return m.supplierID, true
	case poItemSuppliersLoadedMsg:
		return m.supplierID, true
	case poAssetsLoadedMsg:
		return m.supplierID, true
	}
	return 0, false
}

// handlePickerLoaded dispatches the three async results to the right
// state slot. Returns nil so the caller can chain into tea.Cmd.
func (s *PurchaseOrderCreateScreen) handlePickerLoaded(msg tea.Msg) tea.Cmd {
	if id, ok := pickerReplySupplier(msg); ok && id != s.supplierID {
		// The operator committed another supplier while this was in flight.
		// Dropping it whole is deliberate: the loading flag belongs to the
		// request that is still out for the CURRENT supplier, and clearing it
		// here would paint that one's reply as already arrived.
		return nil
	}
	switch m := msg.(type) {
	case poReorderItemsLoadedMsg:
		s.reorderLoading = false
		s.reorderNote.clear() // the reply's own frame answers for this one
		if m.err != nil {
			s.reorderLoadErr = m.err.Error()
			return Status("load reorder items failed: "+m.err.Error(), StatusError)
		}
		s.reorderLoadErr = ""
		s.reorderItems = m.items
		s.reorderCursor = 0
		// Selections index into the list we just replaced — drop them so a
		// stale index can never mark (and bulk-add) the wrong row.
		s.reorderSelected = map[int]bool{}
	case poItemSuppliersLoadedMsg:
		s.itemSuppliersLoad = false
		if m.err != nil {
			s.itemSuppliersErr = m.err.Error()
			s.itemSuppliersNote.clear() // the error line answers for the screen
			return Status("looking up this supplier's items failed: "+m.err.Error(), StatusError)
		}
		// Clear the PREVIOUS failure. itemBody draws the error instead of the
		// list, so a stale string left here would hide a load that worked.
		s.itemSuppliersErr = ""
		s.itemSuppliersAll = m.rows
		s.itemSuppliersFor = m.supplierID
		s.applyItemSupplierFilter()
		s.itemSuppliersCur = 0
		if len(m.rows) == 0 {
			// FOUND NOTHING and COULD NOT TELL are different facts, and only
			// one of them is safe to act on. A green "✓ 0 catalog item(s)
			// loaded" says the first about a screen that may mean the second,
			// and it advertises a '/' that can only ever answer "no match".
			// Say what an empty catalog is — the same sentence the frame falls
			// back to, so the two can never diverge — and mark it as the
			// warning it is. The asset path next door already does this.
			return s.itemSuppliersNote.say(s.noCatalogSentence(), StatusWarn)
		}
		if s.itemSuppliersTyping || strings.TrimSpace(s.itemSuppliersSearch.Value()) != "" {
			// A query was typed while the walk was out. Answer THAT rather than
			// the whole catalog — and through the shared gate, so the box being
			// open decides whether '/' may be named. With the box open '/' is a
			// character going into the query, not a key that searches.
			s.itemSuppliersNote = s.itemFilterOrVerdict("")
			return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
		}
		return s.itemSuppliersNote.say(
			fmt.Sprintf("%d catalog item(s) loaded", len(m.rows)), StatusOK)
	case poAssetsLoadedMsg:
		if m.seq != s.assetsSeq {
			// An older lookup for this same supplier. Dropped whole, flag
			// included: assetsLoading belongs to the request that is still out,
			// and clearing it here would paint that one's reply as arrived.
			return nil
		}
		s.assetsLoading = false
		if m.err != nil {
			s.assetsErr = m.err.Error()
			s.assetsNote.clear()
			return Status("looking up this supplier's assets failed: "+m.err.Error(), StatusError)
		}
		s.assetsErr = ""
		s.assets = m.rows
		s.assetsHasNext = m.hasNext
		s.assetsCursor = 0
		return s.assetLoadedNote(len(m.rows))
	}
	return nil
}

// ---------------------------------------------------------------------------
// One statement of what works here
// ---------------------------------------------------------------------------

// The pickers used to state their live keys TWICE — once in a prose action bar
// at the top of the pane (helpText) and once as a way-out line in the frame
// beside the note — and keeping the two in sync by hand produced a bar
// promising "enter picks the match" four rows above a note saying enter closes
// the search, with enter doing neither. Three separate gaps between those two
// surfaces were recorded here as deferred to this conversion; all three are
// closed by it, and closed the same way rather than reconciled one at a time.
//
// There is now ONE surface that names a key: the action bar (barItems,
// po_create.go), built per frame from the phase and the state being drawn, and
// drawn at the bottom of the pane where the layer's own budget guarantees it a
// whole row. The notes name no keys at all. What a note still carries is the
// LEAD — what the key that was just pressed DID — because two keys sharing one
// sentence redraw the pane the first press left, and that is a statement about
// a press rather than a claim about what works.
//
// So the three gaps go with the sentences that had them: the empty-list arm
// that could not tell "matched nothing" from "sells nothing" was naming keys
// for two different states out of one row count, and it names none now; the two
// failure frames that printed their way-out line as the bar AND as the verdict
// note's tail print it once; and the way-out tail itself is gone from
// catalogVerdict and assetVerdictNote.
//
// "Acts" still means CHANGES something. A key that declines and says why —
// enter over an empty list, `]` at the last page — is not acting, and is
// deliberately left off the bar: naming it would advertise a dead end, and the
// rule is that such an arm must answer, not that the bar must promise it.

// supplierListOnScreen reports whether supplierBody is drawing rows.
func (s *PurchaseOrderCreateScreen) supplierListOnScreen() bool {
	return !s.supplierLoading && s.supplierLoadErr == "" && len(s.suppliers) > 0
}

// supplierVerdictNote says which of the three off-screen states a declining key
// is answering from. It is a pickerNote and not a bare Status for the reason
// the other two pickers already are: a flash expires after four seconds, and
// this note is what the pinned header draws as its ESSENTIAL row, so the answer
// is still on the pane a minute later.
func (s *PurchaseOrderCreateScreen) supplierVerdictNote(prefix string) tea.Cmd {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	switch {
	case s.supplierLoading:
		return s.supplierNote.say(lead+"still looking up the suppliers…", StatusInfo)
	case s.supplierLoadErr != "":
		return s.supplierNote.say(lead+"loading the suppliers failed", StatusError)
	}
	return s.supplierNote.say(lead+"no suppliers are configured", StatusWarn)
}

// itemSearchOpens reports whether '/' hands the keyboard to the filter box.
//
// ONE predicate, read by the arm that opens the box and by the bar that names
// the key, because two answers to the same question are how a bar comes to
// name a key that declines. Both refusals are real states: the failure frame's
// only repair is `r`, and opening the box over it would take the keyboard away
// from that; and against a supplier we KNOW sells nothing the box can only
// ever answer "no match", so it would take the keyboard away from the two keys
// that can still do something.
//
// Mid-walk is NOT a refusal, deliberately: the rows are on their way and the
// query is applied when they land.
func (s *PurchaseOrderCreateScreen) itemSearchOpens() bool {
	if s.itemSuppliersErr != "" {
		return false
	}
	return !(s.catalogAnswered() && len(s.itemSuppliersAll) == 0)
}

// itemListOnScreen / assetListOnScreen / reorderListOnScreen report whether the
// picker's body is actually DRAWING its rows. Each body answers with a single
// muted line on its working frame and on its failure frame, and none of the
// three clears the rows it was holding when it does — deliberately, because a reload that
// fails should not also destroy what the operator was looking at.
//
// The keys that act on a row have to ask. A picker that holds twenty rows
// behind a "Reloading…" line still had a cursor the operator could move with
// j/k and a row enter would stage, and neither was on the pane: an item going
// onto a purchase order that the operator cannot see is a wrong purchase order.
// The failure frame is worse again, because it names r/b/esc and nothing else,
// so enter acting there is also the bar naming one set of keys while another
// set works.
//
// itemListOnScreen answers for the SEARCH BOX too, not just the row keys: the
// item filter runs client-side over the loaded catalog, so enter inside the box
// picks out of the same invisible slice j/k would move through. The asset box
// is gated on assetsLoading instead — that search really goes off the terminal,
// and there the hazard is a second request racing the first rather than a pick
// out of nothing.
func (s *PurchaseOrderCreateScreen) itemListOnScreen() bool {
	return !s.itemSuppliersLoad && s.itemSuppliersErr == ""
}

func (s *PurchaseOrderCreateScreen) assetListOnScreen() bool {
	return !s.assetsLoading && s.assetsErr == ""
}

func (s *PurchaseOrderCreateScreen) reorderListOnScreen() bool {
	return !s.reorderLoading && s.reorderLoadErr == ""
}

// assetLoadedNote words what a finished asset search found — through the same
// typing gate the item picker's notes go through. The reply can land with the
// search box still OPEN (press 'a', then '/' while the request is out), and
// there enter runs the search again rather than picking, while '/' and 'b' are
// characters going into the query.
func (s *PurchaseOrderCreateScreen) assetLoadedNote(rows int) tea.Cmd {
	if rows == 0 {
		// A search that found nothing is a RESULT, not a blank screen: say
		// what was searched for so the operator can tell "no such asset"
		// from "I mistyped" — and read assetsQuery, the query this reply
		// actually answers, not the live box, which may already hold
		// something nobody has submitted.
		if q := strings.TrimSpace(s.assetsQuery); q != "" {
			return s.assetsNote.say(
				"no asset matches "+strconv.Quote(pickerClip(q, 16)), StatusWarn)
		}
		return s.assetsNote.say("this supplier has no assets on file", StatusWarn)
	}
	if s.assetsTyping {
		return s.assetsNote.say(fmt.Sprintf("%d asset(s) matched", rows), StatusInfo)
	}
	return s.assetsNote.say(fmt.Sprintf("%d asset(s)", rows), StatusOK)
}

// assetVerdictNote and reorderVerdictNote are the asset and reorder pickers'
// equivalents of catalogVerdictNote: they say which of the two off-screen
// states a declining key is answering from, and name the key that leaves it.
// prefix, when given, leads with what the key did — the same shape
// reportItemFilterState uses, and for the same reason: esc out of the search
// box lands here whenever the lookup is still out, and without a lead it
// re-emits the note the declining keypress before it already put on the pane,
// leaving a query in the box, the same two lines under it and only the caret
// moving.
func (s *PurchaseOrderCreateScreen) assetVerdictNote(prefix string) tea.Cmd {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	if s.assetsLoading {
		return s.assetsNote.say(
			lead+"still looking up the assets "+s.supplierLabel()+" supplied…", StatusInfo)
	}
	return s.assetsNote.say(lead+"the asset lookup failed", StatusError)
}

func (s *PurchaseOrderCreateScreen) reorderVerdictNote(prefix string) tea.Cmd {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	if s.reorderLoading {
		return s.reorderNote.say(
			lead+"still looking up what "+s.supplierLabel()+" has flagged for reorder…", StatusInfo)
	}
	return s.reorderNote.say(lead+"reading the reorder queue failed", StatusError)
}

// noCatalogSentence is the single wording of "this supplier sells nothing".
// The load that finds an empty catalog and the frame that renders one both go
// through it, so the note and the fallback line under it can never end up
// saying two different things about the same fact.
func (s *PurchaseOrderCreateScreen) noCatalogSentence() string {
	return s.supplierLabel() + " has no active catalog items on file"
}

// itemPickEntryNote is what the picker says when it opens on a catalog it
// already holds, in place of the "Looking up…" frame it would otherwise show
// over work that is not happening. Same three-way split as a fresh load, minus
// the tick: nothing was just fetched, so nothing succeeded.
func (s *PurchaseOrderCreateScreen) itemPickEntryNote() tea.Cmd {
	if !s.catalogAnswered() || len(s.itemSuppliersAll) == 0 {
		return s.catalogVerdictNote("")
	}
	return s.itemSuppliersNote.say(
		fmt.Sprintf("%d catalog item(s) held for this supplier", len(s.itemSuppliersAll)), StatusInfo)
}

// applyItemSupplierFilter populates itemSuppliers from itemSuppliersAll
// using the search-input value (case-insensitive substring on
// ItemName / SupplierSKU). Backend has no ?search= on this endpoint
// today, so we do it client-side over the whole catalog — which
// ListItemSuppliersForSupplier pages in for exactly this reason.
func (s *PurchaseOrderCreateScreen) applyItemSupplierFilter() {
	q := strings.ToLower(strings.TrimSpace(s.itemSuppliersSearch.Value()))
	if q == "" {
		s.itemSuppliers = s.itemSuppliersAll
		return
	}
	filtered := make([]omsapi.ItemSupplier, 0, len(s.itemSuppliersAll))
	for _, r := range s.itemSuppliersAll {
		if strings.Contains(strings.ToLower(r.ItemName), q) ||
			strings.Contains(strings.ToLower(r.SupplierSKU), q) {
			filtered = append(filtered, r)
		}
	}
	s.itemSuppliers = filtered
}

// ---------------------------------------------------------------------------
// Phase 3a: Reorder-queue picker
// ---------------------------------------------------------------------------

// reorderEmptyNote answers every key that acts on the reorder queue — a row
// (j / k / space / enter) or all of it (a) — while the picker is drawing a list
// with no rows in it. Say so in the BODY as well as
// the flash: the frame's fixed "Nothing flagged…" line is already on the pane,
// so a Status alone left it byte-for-byte unchanged and expired four seconds
// later with nothing recording the press. The lead is what the key DID, so two
// different keys do not answer with the same sentence.
func (s *PurchaseOrderCreateScreen) reorderEmptyNote(lead string) tea.Cmd {
	if lead != "" {
		lead += " · "
	}
	return s.reorderNote.say(lead+"nothing flagged for reorder here", StatusWarn)
}

func (s *PurchaseOrderCreateScreen) updateReorderPickPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if !s.reorderListOnScreen() {
		// The list is not DRAWN. The bar names B and Esc and nothing else, so
		// every key that would act on a row declines and says why — each
		// naming ITSELF, because this frame has no rows, no highlight and no
		// focused input, so two keys sharing one sentence would redraw the pane
		// the first press left.
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSPurchasing, nil)
		case "b":
			s.phase = poPhaseSource
			return s, nil
		case "up", "down", "pgup", "pgdown":
			return s, s.reorderVerdictNote(m.String() + " moves nothing")
		case " ":
			return s, s.reorderVerdictNote("nothing to mark")
		case "a":
			return s, s.reorderVerdictNote("nothing to add")
		case "enter":
			return s, s.reorderVerdictNote("nothing to pick")
		}
		return s, nil
	}
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "up", "down", "pgup", "pgdown":
		// Drawn and EMPTY, which the gate above does not catch: the frame is
		// showing "Nothing flagged for reorder…" and these arms answered with
		// nil, so the pane did not move and there was not even a highlight to
		// see stay put.
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote(m.String() + " moves nothing")
		}
		return s, nil
	case " ":
		// Mark/unmark this row for a bulk add. Marking several rows and
		// pressing enter is the middle ground between adding one item at a time
		// and taking the supplier's whole queue with A.
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote("nothing to mark")
		}
		if s.reorderCursor >= 0 && s.reorderCursor < len(s.reorderItems) {
			if s.reorderSelected == nil {
				s.reorderSelected = map[int]bool{}
			}
			if s.reorderSelected[s.reorderCursor] {
				delete(s.reorderSelected, s.reorderCursor)
			} else {
				s.reorderSelected[s.reorderCursor] = true
			}
		}
		return s, nil
	case "a":
		// Add ALL of this supplier's reorder items in one press — the fix for
		// "a supplier with 15 items is ~30 keystrokes". Every row is staged with
		// its suggested_quantity; the review cart is where individual lines get
		// adjusted (Ctrl-E), so land there.
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote("nothing to add")
		}
		return s, s.addReorderLines(s.reorderItems)
	case "enter":
		// With rows marked, enter stages exactly those (in list order).
		// Otherwise it keeps the original one-row behavior: open the line form
		// pre-filled so quantity/date/cost can be set before staging.
		if len(s.reorderSelected) > 0 {
			picked := make([]omsapi.ReorderDataItem, 0, len(s.reorderSelected))
			for i, it := range s.reorderItems {
				if s.reorderSelected[i] {
					picked = append(picked, it)
				}
			}
			return s, s.addReorderLines(picked)
		}
		if len(s.reorderItems) == 0 {
			return s, s.reorderEmptyNote("nothing to pick")
		}
		if s.reorderCursor < 0 || s.reorderCursor >= len(s.reorderItems) {
			s.reorderCursor = 0
		}
		it := s.reorderItems[s.reorderCursor]
		qty := it.SuggestedQuantity
		if qty <= 0 {
			qty = 1
		}
		unitCost := 0.0
		if v, err := strconv.ParseFloat(string(it.UnitCost), 64); err == nil {
			unitCost = v
		}
		pkgCost := 0.0
		if v, err := strconv.ParseFloat(string(it.PackageCost), 64); err == nil {
			pkgCost = v
		}
		desc := it.ItemName
		if desc == "" {
			desc = it.SKU
		}
		// The reorder_data row DOES carry the supplier's case size and case
		// price, so a reorder line reaches the form on the same footing as one
		// picked from the catalog. This used to pass 0 for both, under a comment
		// asserting the row carried neither — it carries both, and the result
		// was the captain's defect on the path the report did not name: a case
		// price typed into a per-unit row on every line staged from the queue.
		s.enterLinePhase(it.ItemSupplierID, nil, desc, qty, unitCost, pkgCost, it.QuantityPerPackage)
		s.reorderNote.clear()
		return s, tea.Batch(
			Status("picked "+desc+" — set quantity and cost, enter adds the line", StatusOK),
			textinput.Blink,
		)
	}
	return s, nil
}

// reorderCartLine stages one reorder-queue row as a cart line without going
// through the Phase-4 form, producing exactly what the single-row enter path
// produces once the operator accepts the prefill: suggested_quantity (floored
// at 1) and the item's name (SKU when unnamed) as the label.
//
// Cost follows the same rule the form does. The row's unit_cost is the
// item-supplier's stored cost (reorder_data reads item_supplier.unit_cost), and
// the line form now seeds its cost field with it on a catalog line, so a bulk
// add carries it too — the operator sees the same price whether the line was
// added one at a time or fifteen at once, and ctrl+e can change or clear it
// (sc-gnzw). A row whose catalog cost is unset stays blank rather than pinning
// an explicit $0, which leaves the backend pricing the line (sc-5yr). A row
// without an item_supplier_id can only be created as a freeform line, and that
// branch REQUIRES a cost, so its unit_cost is always sent (0 when the row
// carries none — visible as "@ $0" in the cart, and fixable with ctrl+e).
//
// The staged line CARRIES the supplier's case size. Nothing here converts —
// suggested_quantity is base units and unit_cost is per base unit, which is
// exactly what the payload takes — but a cart line with qpp 0 reads back as a
// singles line: the review row would state a case order in loose units, and
// Ctrl-E would re-open it in a per-unit form on an item the operator buys by
// the case. Both of those are the reported defect, one surface further on.
func reorderCartLine(it omsapi.ReorderDataItem) poCartLine {
	qty := it.SuggestedQuantity
	if qty <= 0 {
		qty = 1
	}
	desc := it.ItemName
	if desc == "" {
		desc = it.SKU
	}
	line := omsapi.PurchaseOrderCreateItem{
		Description:    desc,
		Quantity:       qty,
		ItemSupplierID: it.ItemSupplierID,
	}
	unitCost := 0.0
	if v, err := strconv.ParseFloat(string(it.UnitCost), 64); err == nil {
		unitCost = v
	}
	if it.ItemSupplierID == nil || unitCost > 0 {
		line.UnitCost = &unitCost
	}
	label := desc
	if label == "" {
		label = "line"
	}
	return poCartLine{item: line, label: label, qpp: it.QuantityPerPackage}
}

// addReorderLines stages every supplied reorder row and drops the operator in
// the review cart, where any individual line can be adjusted with ctrl+e or
// dropped with ctrl+x. Clears the marks so the picker is clean if it is
// re-entered for a second batch.
func (s *PurchaseOrderCreateScreen) addReorderLines(items []omsapi.ReorderDataItem) tea.Cmd {
	if len(items) == 0 {
		// Through the picker's own note, like the j / k / space / enter arms
		// one switch above: this was the last arm answering through the
		// SCREEN-level failure line, which is where a failed submit's detail
		// lives. Writing errMsg alone left errDetail standing, so after a 502
		// the pane drew "nothing to add" with several folded rows of the
		// gateway's HTML underneath it, presented as that sentence's reason.
		return s.reorderEmptyNote("nothing to add")
	}
	for _, it := range items {
		s.lines = append(s.lines, reorderCartLine(it))
	}
	s.reorderSelected = map[int]bool{}
	s.reorderNote.clear()
	s.setErr("", "")
	s.phase = poPhaseReview
	s.reviewCursor = len(s.lines) - len(items) // first line of this batch
	s.poNotes.Focus()
	return tea.Batch(
		Status(fmt.Sprintf("added %d line(s) (%d in cart)", len(items), len(s.lines)), StatusOK),
		textinput.Blink,
	)
}

// reorderBody is the reorder queue as navigable rows.
//
// The working and failure frames are ONE muted line rather than the block of
// prose they used to be: the subject of the request is on the status row
// (workingLine), the failure's headline is there too and its detail rides in
// the pinned header, and the keys are on the bar. What is left for the body is
// the one fact the operator cannot get anywhere else — whether there are rows.
func (s *PurchaseOrderCreateScreen) reorderBody() *jdeLines {
	switch {
	case s.reorderLoading:
		return poEmptyBody(s.paneWidth(), "The reorder queue is on its way.")
	case s.reorderLoadErr != "":
		return poEmptyBody(s.paneWidth(), "No reorder queue on the pane — the lookup failed.")
	case len(s.reorderItems) == 0:
		return poEmptyBody(s.paneWidth(), "Nothing flagged for reorder under this supplier.")
	}
	l := &jdeLines{}
	cur, room := s.cursorRow(), s.poRowRoom()
	for i, it := range s.reorderItems {
		// Checkbox for the bulk-add marks, so a marked row still reads as
		// marked once the highlight moves off it.
		mark := "[ ] "
		if s.reorderSelected[i] {
			mark = "[x] "
		}
		tag := ""
		if it.HasActiveReorderReq {
			tag = " " + StyleStatusOK.Render(fmt.Sprintf("[reorder %s]", it.ReorderRequestStatus))
		}
		cost := ""
		if it.UnitCost != "" {
			cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s", it.UnitCost))
		}
		// The mark is the row's own state and never gives. What is being
		// ORDERED — the suggested quantity — is the fact; the stock levels
		// behind it, the price and the request flag are the decorations,
		// dropped from the right so the columns that stay keep their places.
		levels := fmt.Sprintf(" (current %d / min %d)", it.CurrentStock, it.MinimumStock)
		// The suggestion is BASE units on the wire and is rendered in the unit
		// the line is bought in (poQtyFact), so it reads as the same figure the
		// line form will open on. A bare "qty 48" beside a supplier who ships
		// 24 to a case is a number whose denominator the row does not state.
		l.AddRow(i, poPickRow(i, cur, mark+poFitRow(room-lipgloss.Width(mark), it.ItemName,
			"  qty "+poQtyFact(it.SuggestedQuantity, it.QuantityPerPackage), levels, cost, tag)))
	}
	// Counts only, tagged onto the LAST row so a body that overflows loses it
	// from the tail rather than stranding the first row behind it. The keys are
	// the bar's job, and a summary that also named them was a second copy of
	// the same claim waiting to go stale.
	summary := fmt.Sprintf("%d in the queue", len(s.reorderItems))
	if n := len(s.reorderSelected); n > 0 {
		summary = fmt.Sprintf("%d marked of %d in the queue", n, len(s.reorderItems))
	}
	for _, line := range jdeCaveatLines(summary, s.paneWidth()) {
		l.AddRow(len(s.reorderItems)-1, line)
	}
	return l
}

// ---------------------------------------------------------------------------
// Phase 3b: Inventory items picker
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateItemPickPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if s.itemSuppliersTyping {
		switch m.Type {
		case tea.KeyEsc:
			// Close the box but KEEP the filter — this is the browse path, so
			// it has to say that the rows still on screen are a filtered subset.
			s.itemSuppliersTyping = false
			s.itemSuppliersSearch.Blur()
			return s, s.reportItemFilterState("search closed")
		case tea.KeyEnter:
			return s, s.commitSearchedItem()
		}
		var cmd tea.Cmd
		s.itemSuppliersSearch, cmd = s.itemSuppliersSearch.Update(m)
		s.applyItemSupplierFilter()
		if s.itemSuppliersCur >= len(s.itemSuppliers) {
			s.itemSuppliersCur = 0
		}
		// Live count as they type, so "nothing matches" is visible BEFORE the
		// enter that used to answer it with silence — but only once the walk
		// has ANSWERED. Filtering an empty slice that is empty because the
		// request has not come back yet produced `no match for "w" (0 in
		// catalog)`, which is a conclusion about a catalog nobody has seen.
		s.itemSuppliersNote = s.itemFilterOrVerdict("")
		return s, cmd
	}
	if !s.itemListOnScreen() {
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSPurchasing, nil)
		case "b":
			s.phase = poPhaseSource
			return s, nil
		case "up", "down", "pgup", "pgdown":
			return s, s.catalogVerdictNote(m.String() + " moves nothing")
		case "enter":
			return s, s.catalogVerdictNote("nothing to pick")
		case "/":
			if !s.itemSearchOpens() {
				return s, s.catalogVerdictNote("search needs the catalog")
			}
			return s, s.openItemSearch()
		case "r":
			if s.itemSuppliersLoad {
				// A walk is already out; the bar does not name R there.
				return s, s.catalogVerdictNote("already reloading")
			}
			return s, s.reloadCatalog()
		}
		return s, nil
	}
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "up", "down", "pgup", "pgdown":
		// The gate above catches a list that is not DRAWN; this catches one
		// that is drawn and EMPTY, which is the state the report is about — a
		// search that matched nothing. `return s, nil` there redrew a
		// byte-for-byte identical pane with not even a cursor to see stay put.
		//
		// Only the empty list. An EDGE is a weaker case: the highlight is on
		// screen and visibly at the end, so the press has answered itself.
		if len(s.itemSuppliers) == 0 {
			return s, s.reportItemFilterState(m.String() + " moves nothing")
		}
		return s, nil
	case "/":
		if !s.itemSearchOpens() {
			return s, s.catalogVerdictNote("search needs the catalog")
		}
		return s, s.openItemSearch()
	case "r":
		return s, s.reloadCatalog()
	case "enter":
		return s, s.commitHighlightedItem()
	}
	return s, nil
}

// openItemSearch hands the keyboard to the filter box.
//
// The note it opens with does NOT promise a pick: the box opens over a walk
// that is still out on purpose (the rows are on their way and the query lands
// with them), and there commitSearchedItem declines at its first line. The bar
// makes the same split — Enter is named inside the box only while the catalog
// is on the pane — so the two surfaces agree by construction.
func (s *PurchaseOrderCreateScreen) openItemSearch() tea.Cmd {
	s.itemSuppliersTyping = true
	s.itemSuppliersSearch.Focus()
	opened := "type to narrow the catalog"
	if !s.itemListOnScreen() {
		opened = "type to narrow the catalog · the rows are still on their way"
	}
	return tea.Batch(s.itemSuppliersNote.say(opened, StatusInfo), textinput.Blink)
}

// reloadCatalog goes and asks again.
//
// The catalog is held per supplier and re-entering the picker no longer
// re-walks it (the `i` arm in po_create.go), so there has to be a named way to
// refetch — a cache with no refresh is its own silent-wrong-answer bug.
//
// itemSuppliersFor is deliberately LEFT set: it is what tells the working line
// this is a reload rather than a first look, and the reply overwrites it either
// way. catalogAnswered() is false while itemSuppliersLoad is up, so nothing
// reads it as an answer meanwhile.
func (s *PurchaseOrderCreateScreen) reloadCatalog() tea.Cmd {
	s.itemSuppliersLoad = true
	s.itemSuppliersErr = ""
	s.itemSuppliersNote.clear() // the working line speaks for this one
	return tea.Batch(
		Status("reloading what "+s.supplierLabel()+" sells…", StatusInfo),
		s.loadItemSuppliersForSupplier(),
	)
}

// commitSearchedItem is enter inside the item picker's SEARCH box, and the
// centre of the "it just kinda hangs there" report.
//
// It used to set typing=false, re-apply the filter and return nil. Every one of
// those is invisible: the filter had already been applied on the keystroke
// before, so the redraw was byte-for-byte what was already on screen — same
// rows, same caret still blinking in the search box. The pick needed a SECOND
// enter, which nothing on the screen said. The operator's model ("enter selects
// the item") was the right one; the screen's ("enter closes the box") was never
// stated anywhere.
//
// So enter now selects, with the one exception that protects the cart:
//
//   - exactly one row matches → take it. This is also the SCANNER path, since a
//     barcode arrives as a burst of runes plus enter, and a scan that resolves
//     to one item must be one press.
//   - several match → do NOT guess which. Close the box, hand j/k back, and say
//     how many matched and what to press. The screen visibly changes and names
//     the next key, which is the whole difference from the old behaviour.
//   - none match → keep the box OPEN and focused so the query can be edited in
//     place, and say what was searched for. This is the state that used to be a
//     dead end: enter over an empty list returned nil forever, and no key on the
//     screen could reach the item.
func (s *PurchaseOrderCreateScreen) commitSearchedItem() tea.Cmd {
	if !s.itemListOnScreen() {
		return s.catalogVerdictNote("searched again")
	}
	s.applyItemSupplierFilter()
	switch {
	case len(s.itemSuppliers) == 0:
		// Through the gate, not around it: with the walk still out this says so
		// rather than concluding "no match … (N in catalog)" about a catalog
		// that has not finished arriving.
		//
		// The lead is here for the same reason it is on the multi-match arm
		// below, and this arm is where the original report survived longest:
		// the box STAYS OPEN on no-match, so enter changed the cursor position
		// not at all, the rows not at all, and the note not at all — the filter
		// had already been applied on the keystroke before, so
		// reportItemFilterState("") re-emitted the note character for character.
		// The only moving thing on the pane was the caret, which is precisely
		// what "it just kinda hangs there" describes. "searched again" is what
		// the key DID; the clause after it is the outcome.
		s.itemSuppliersCur = 0
		return s.reportItemFilterState("searched again")
	case len(s.itemSuppliers) == 1:
		s.itemSuppliersTyping = false
		s.itemSuppliersSearch.Blur()
		s.itemSuppliersCur = 0
		return s.pickItemSupplier(0)
	default:
		s.itemSuppliersTyping = false
		s.itemSuppliersSearch.Blur()
		if s.itemSuppliersCur < 0 || s.itemSuppliersCur >= len(s.itemSuppliers) {
			s.itemSuppliersCur = 0
		}
		// The lead is the whole point. Without it this arm produced the note
		// the screen was ALREADY showing — same count, same query, same keys —
		// so the only thing that changed was the caret leaving the search box,
		// and "the screen just hangs after I press enter" survived on the
		// multi-match path inside its own fix.
		return s.reportItemFilterState("too many to pick")
	}
}

// commitHighlightedItem is enter over the item list itself. The empty case is
// the other half of the dead end: with no rows there is nothing to pick, and
// answering that with nil is how the picker told the operator nothing at all.
func (s *PurchaseOrderCreateScreen) commitHighlightedItem() tea.Cmd {
	if !s.itemListOnScreen() {
		return s.catalogVerdictNote("nothing to pick")
	}
	if len(s.itemSuppliers) == 0 {
		return s.reportItemFilterState("nothing to pick")
	}
	if s.itemSuppliersCur < 0 || s.itemSuppliersCur >= len(s.itemSuppliers) {
		s.itemSuppliersCur = 0
	}
	return s.pickItemSupplier(s.itemSuppliersCur)
}

// pickItemSupplier stages row i as the line under construction and says which
// item it took. Naming the item matters more here than anywhere else on the
// screen: the operator pressed enter over a filtered list, and "which of the
// rows did it take" is the question the old silence left open.
func (s *PurchaseOrderCreateScreen) pickItemSupplier(i int) tea.Cmd {
	row := s.itemSuppliers[i]
	id := row.ID
	unitCost := 0.0
	if v, err := strconv.ParseFloat(string(row.UnitCost), 64); err == nil {
		unitCost = v
	}
	pkgCost := 0.0
	if v, err := strconv.ParseFloat(string(row.PackageCost), 64); err == nil {
		pkgCost = v
	}
	desc := row.ItemName
	if desc == "" {
		desc = row.SupplierSKU
	}
	// PackQuantity (quantity_per_package) drives the entry basis: when > 1 the
	// line form takes CASES and a CASE COST and derives the base quantity and
	// per-unit price for the wire (po_case_entry.go).
	//
	// The prefill is ONE PACKAGE in base units, not one unit. It used to be a
	// flat 1, which on a case-packed row is an order for a single loose item of
	// something the vendor only ships by the case — and, once the form takes
	// cases, it is also a quantity with no case count, so the line would open
	// in units on the very item the operator buys by the case. The picker knows
	// no better figure than "one of what they sell", which is exactly what a
	// package is.
	s.enterLinePhase(&id, nil, desc, poPackSize(row.PackQuantity), unitCost, pkgCost, row.PackQuantity)
	s.itemSuppliersNote.clear() // the picker is behind us; the line form speaks now
	label := desc
	if label == "" {
		label = fmt.Sprintf("item-supplier #%d", id)
	}
	return tea.Batch(
		Status("picked "+label+" — set quantity and cost, enter adds the line", StatusOK),
		textinput.Blink,
	)
}

// catalogAnswered reports whether the picker actually HAS an answer about this
// supplier's catalog, as opposed to merely holding an empty slice.
//
// itemSuppliersFor is the flag that means it: it is set only by a reply that
// arrived for the supplier the order is on. An empty itemSuppliersAll is
// equally true while the walk is in flight (which is now N sequential page
// requests, so the window is wide) and after it failed, and neither of those is
// the fact "this supplier sells nothing".
func (s *PurchaseOrderCreateScreen) catalogAnswered() bool {
	return s.supplierID > 0 &&
		s.itemSuppliersFor == s.supplierID &&
		!s.itemSuppliersLoad &&
		s.itemSuppliersErr == ""
}

// catalogVerdictNote answers a key that needed the catalog when there are no
// rows to give it — and says which of the four reasons that is.
//
// "Found nothing" and "could not tell" are different facts and only one of them
// is safe to act on. This is the same distinction the loaded path draws; drawn
// here too, because a key pressed mid-walk used to report the conclusion of a
// walk that had not finished.
// prefix leads with what the key DID. Without it this was the last way the
// reported hang could still be produced: the typing branch sets the note to
// catalogVerdict("") on every keystroke, so enter mid-walk answered with the
// note the rune before it had already drawn — same text, same level, same
// working line above it, and a focused textinput the enter arm never touches.
func (s *PurchaseOrderCreateScreen) catalogVerdictNote(prefix string) tea.Cmd {
	s.itemSuppliersNote = s.catalogVerdict(prefix)
	return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
}

// catalogVerdict is the same four-way answer as a pickerNote, without posting
// it. The typing path needs the wording on every keystroke but must not fire a
// status flash per rune, so the note and the flash are separated here.
func (s *PurchaseOrderCreateScreen) catalogVerdict(prefix string) pickerNote {
	// The lead travels down the verdict path too, not just the filter path.
	// itemFilterOrVerdict used to drop it here, so esc out of the search box
	// mid-walk answered with the same "still looking up…" the last keystroke
	// had already left on the pane — a query still in the box, the same line
	// under it, and only the caret leaving. Same for enter over a catalog that
	// really is empty.
	//
	// It names no key. It used to carry a way-out tail whose wording had to
	// switch on whether the search box was open (r is a letter going into the
	// query there, b is another), which is two claims about the same keys on
	// one pane — the action bar makes both of them, correctly, in every state.
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	switch {
	case s.itemSuppliersLoad:
		return pickerNote{lead + "still looking up the items " + s.supplierLabel() + " sells…", StatusInfo}
	case s.itemSuppliersErr != "":
		return pickerNote{lead + "the catalog lookup failed", StatusError}
	case !s.catalogAnswered():
		return pickerNote{lead + "this supplier's catalog has not been looked up yet", StatusWarn}
	}
	return pickerNote{lead + s.noCatalogSentence(), StatusWarn}
}

// reportItemFilterState is the note for "the filter changed and nothing was
// picked". prefix, when given, leads with what the key did.
func (s *PurchaseOrderCreateScreen) reportItemFilterState(prefix string) tea.Cmd {
	s.itemSuppliersNote = s.itemFilterOrVerdict(prefix)
	return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
}

// itemFilterOrVerdict is the ONE place a filter outcome is worded, and the one
// gate in front of it: a filter result is only a fact once the catalog walk has
// answered. Filtering a slice that is empty because the request has not come
// back yet reads out as `no match for "w" (0 in catalog)` — a conclusion about
// a catalog nobody has seen, which is the found-nothing / could-not-tell
// conflation catalogVerdict exists to close. Both the live count typed into the
// box and the note esc leaves behind come through here so neither can drift
// past the gate on its own.
func (s *PurchaseOrderCreateScreen) itemFilterOrVerdict(prefix string) pickerNote {
	// Not answered, or answered with nothing: neither is a filter outcome.
	// The emptiness test is not enough on its own — an 'r' reload leaves the
	// previous rows in place while the walk is out, so a guard written as
	// len(itemSuppliersAll) == 0 sails past a mid-reload frame and words a
	// verdict about a request that has not come back.
	if !s.catalogAnswered() || len(s.itemSuppliersAll) == 0 {
		return s.catalogVerdict(prefix)
	}
	return itemFilterNote(
		strings.TrimSpace(s.itemSuppliersSearch.Value()),
		len(s.itemSuppliers), len(s.itemSuppliersAll), prefix, s.itemSuppliersTyping)
}

// itemFilterNote words the three outcomes of a filter. A zero-match note names
// the query AND the catalog size, because those two facts together are what
// tell the operator whether to retype or to conclude this supplier does not
// sell the thing — "No inventory items match" alone said neither, and said it
// about a catalog that (before ListItemSuppliersForSupplier paged) might not
// even have been fully loaded.
//
// typing is which of two screens this note is going onto, and EVERY arm needs
// it, not just the zero-match one. These notes are rendered in both states and
// the keys differ completely between them: with the box open j and k are
// characters going into the query and enter only picks when exactly one row
// matches; with it shut j/k move the highlight, enter picks it, and esc cancels
// the whole purchase order. Gating one arm and leaving the other two is how the
// ORDINARY search path — type a few letters, see eleven matches — ended up
// naming three keys of which two did something else.
func itemFilterNote(query string, matched, total int, prefix string, typing bool) pickerNote {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	q := strconv.Quote(pickerClip(query, 16))
	switch {
	case query == "":
		if typing {
			return pickerNote{fmt.Sprintf("%s%d item(s) · type to narrow", lead, total), StatusInfo}
		}
		return pickerNote{fmt.Sprintf("%s%d item(s)", lead, total), StatusInfo}
	case matched == 0:
		// The lead is kept here too: esc having just closed the box is the
		// thing the operator most needs acknowledged on this frame, and
		// dropping it was why nothing on screen answered that press.
		return pickerNote{
			fmt.Sprintf("%sno match for %s (%d in catalog)", lead, q, total),
			StatusWarn,
		}
	case matched == 1:
		return pickerNote{fmt.Sprintf("%s1 of %d match %s", lead, total, q), StatusOK}
	}
	return pickerNote{fmt.Sprintf("%s%d of %d match %s", lead, matched, total, q), StatusOK}
}

// itemBody is the supplier's catalog as navigable rows.
func (s *PurchaseOrderCreateScreen) itemBody() *jdeLines {
	switch {
	case s.itemSuppliersLoad:
		return poEmptyBody(s.paneWidth(), "The catalog is on its way.")
	case s.itemSuppliersErr != "":
		// The rows this picker was showing are DELIBERATELY kept behind a
		// failed reload — a refresh that fails should not also destroy what the
		// operator was looking at — but they are not drawn, which is what
		// rowCount answers 0 for and what stops the bar naming a key that would
		// act on one.
		return poEmptyBody(s.paneWidth(), "No catalog on the pane — the lookup failed.")
	case len(s.itemSuppliers) == 0:
		// Never a bare "No inventory items match": that sentence is the same
		// whether the supplier sells nothing, the search missed, or the catalog
		// failed to load, and only one of those is safe to act on.
		//
		// And never the NOTE either, which is what the pinned header carries.
		// The note answers the last keypress and names the query and the
		// catalog size; this line says what the LIST is. Drawing one sentence
		// in both places put the same words on two rows of a pane whose rows
		// are the thing every other rule here is protecting.
		if strings.TrimSpace(s.itemSuppliersSearch.Value()) != "" {
			return poEmptyBody(s.paneWidth(), "No catalog item matches the filter.")
		}
		return poEmptyBody(s.paneWidth(), s.noCatalogSentence()+".")
	}
	l := &jdeLines{}
	cur, room := s.cursorRow(), s.poRowRoom()
	for i, it := range s.itemSuppliers {
		sku := it.SupplierSKU
		if sku == "" {
			sku = "—"
		}
		// The catalogue's unit_cost, and on a case-packed row it SAYS it is a
		// unit cost: the row next to it reads "case ×24", and a bare "@ 3.50"
		// beside that is exactly the two-denominators-one-row ambiguity the
		// line form was fixed for. A singles row keeps the bare price it
		// always had.
		cost := ""
		if it.UnitCost != "" {
			per := ""
			if poCasePacked(it.PackQuantity) {
				per = "/unit"
			}
			cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s%s", it.UnitCost, per))
		}
		// Flag case-packed items so the operator knows the line form will take
		// cases and a case price.
		pack := ""
		if poCasePacked(it.PackQuantity) {
			pack = "  " + StyleStatusOK.Render(fmt.Sprintf("case ×%d", it.PackQuantity))
		}
		lead := ""
		if it.LeadTimeDays > 0 {
			lead = "  " + StyleMuted.Render(fmt.Sprintf("lead %gd", it.LeadTimeDays))
		}
		// The SKU is an IDENTIFIER, not a number: OMS-supplied, unbounded, and
		// ordinary MRO part numbers run past thirty cells. It is clipped to what
		// the PRICE, the name's floor and a possible drop mark leave, so the
		// row's one fact — the price the operator is picking on — is whole by
		// construction.
		skuRoom := room - lipgloss.Width(cost) - 2 -
			poHeaderValueFloor - lipgloss.Width(poRowDropMark)
		if skuRoom < poHeaderValueFloor {
			skuRoom = poHeaderValueFloor
		}
		l.AddRow(i, poPickRow(i, cur, poFitRow(room, it.ItemName,
			"  "+pickerClip(sku, skuRoom)+cost, pack, lead)))
	}
	return l
}

// ---------------------------------------------------------------------------
// Phase 3c: Assets-from-supplier picker (server-side search + pagination)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateAssetPickPhase(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	if s.assetsTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.assetsTyping = false
			s.assetsSearch.Blur()
			return s, s.assetsSearchClosedNote()
		case tea.KeyEnter:
			if s.assetsLoading {
				// A search is already out. The reply's generation drops the
				// loser of a race outright, but firing a second search is still
				// the wrong answer to this key: it would leave the operator
				// watching a lookup whose result is discarded. The bar stops
				// naming Enter here for exactly that reason.
				return s, s.assetVerdictNote("searched again")
			}
			// Unlike the item picker this really does go off the terminal, so
			// the note says so BEFORE the request leaves: the reply repaints it
			// with the result, and a slow or failed lookup leaves the operator
			// reading "searching…" rather than a screen that has not moved.
			s.assetsTyping = false
			s.assetsSearch.Blur()
			s.assetsPage = 1
			s.assetsLoading = true
			s.assetsErr = ""
			s.assetsQuery = s.assetsSearch.Value()
			// The note is CLEARED rather than set: what is in flight, and what
			// it is searching for, is the status row's job now (workingLine),
			// and a note saying the same thing would put one sentence on two
			// rows of the pane. The reply repaints the note with the RESULT,
			// which is the half the status row cannot carry.
			s.assetsNote.clear()
			q := strings.TrimSpace(s.assetsQuery)
			what := "this supplier's assets"
			if q != "" {
				what = strconv.Quote(pickerClip(q, 24))
			}
			return s, tea.Batch(
				Status("searching "+what+"…", StatusInfo),
				s.loadAssetsForSupplier(s.assetsQuery),
			)
		}
		var cmd tea.Cmd
		s.assetsSearch, cmd = s.assetsSearch.Update(m)
		return s, cmd
	}
	if !s.assetListOnScreen() {
		switch m.String() {
		case "esc":
			return s, SwitchTo(WSPurchasing, nil)
		case "b":
			s.phase = poPhaseSource
			return s, nil
		case "up", "down", "pgup", "pgdown":
			return s, s.assetVerdictNote(m.String() + " moves nothing")
		case "enter":
			return s, s.assetVerdictNote("nothing to pick")
		case "/":
			return s, s.openAssetSearch()
		case "]":
			// Neither the working frame nor the failure frame names the pager,
			// and stepping the page over one that just FAILED means the retry
			// silently skips it. Each pager key names ITSELF: a shared sentence
			// would make the second press redraw the pane the first one left.
			return s, s.assetVerdictNote("] stays on this page")
		case "[":
			return s, s.assetVerdictNote("[ stays on this page")
		}
		return s, nil
	}
	if moved, cmd := s.moveCursor(m, headerRows); moved {
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "up", "down", "pgup", "pgdown":
		if len(s.assets) == 0 {
			return s, s.assetEmptyNote(m.String() + " moves nothing")
		}
		return s, nil
	case "/":
		return s, s.openAssetSearch()
	case "]":
		// The two paging keys are named only alongside a page that exists, so
		// the bar stays honest — but the arm still has to answer when the state
		// moved underneath the operator between the render and the press.
		if !s.assetsHasNext {
			return s, s.assetsNote.say(
				fmt.Sprintf("already on the last page (page %d)", s.assetsPage), StatusWarn)
		}
		s.assetsPage++
		s.assetsLoading = true
		s.assetsErr = ""
		s.assetsNote.clear() // the working line speaks for this one
		// assetsQuery, never the live box: it can hold text nobody submitted,
		// and paging with that would answer a key that asked for the next page
		// with a search the operator never ran.
		return s, tea.Batch(
			Status(fmt.Sprintf("loading page %d…", s.assetsPage), StatusInfo),
			s.loadAssetsForSupplier(s.assetsQuery),
		)
	case "[":
		if s.assetsPage <= 1 {
			return s, s.assetsNote.say("already on the first page", StatusWarn)
		}
		s.assetsPage--
		s.assetsLoading = true
		s.assetsErr = ""
		s.assetsNote.clear()
		return s, tea.Batch(
			Status(fmt.Sprintf("loading page %d…", s.assetsPage), StatusInfo),
			s.loadAssetsForSupplier(s.assetsQuery),
		)
	case "enter":
		if len(s.assets) == 0 {
			return s, s.assetEmptyNote("nothing to pick")
		}
		if s.assetsCursor < 0 || s.assetsCursor >= len(s.assets) {
			s.assetsCursor = 0
		}
		a := s.assets[s.assetsCursor]
		// Asset.ID is a polyglot `any` (UUID strings + int rows both occur in
		// inventory). Convert to a string for the PO create payload — the
		// backend's asset_id accepts the string repr.
		idStr := fmt.Sprintf("%v", a.ID)
		desc := a.Name
		if a.AssetTag != "" {
			desc = fmt.Sprintf("%s (%s)", a.Name, a.AssetTag)
		}
		// Assets are not case-packed (qpp 0): single per-unit cost, unchanged.
		s.enterLinePhase(nil, &idStr, desc, 1, 0, 0, 0)
		s.assetsNote.clear()
		return s, tea.Batch(
			Status("picked "+desc+" — set quantity and cost, enter adds the line", StatusOK),
			textinput.Blink,
		)
	}
	return s, nil
}

// openAssetSearch hands the keyboard to the search box.
//
// The bar names / on the working frame too, so the box can open with a lookup
// still out — and there Enter is gated. Promising the key the box cannot run
// would be the same false claim the bar carefully does not make.
func (s *PurchaseOrderCreateScreen) openAssetSearch() tea.Cmd {
	s.assetsTyping = true
	s.assetsSearch.Focus()
	opened := "type a name, tag or serial"
	if s.assetsLoading {
		opened = "type a name, tag or serial · a lookup is still out"
	}
	return tea.Batch(s.assetsNote.say(opened, StatusInfo), textinput.Blink)
}

// assetEmptyNote answers any key that acts on a row when the picker is drawing
// a list with no rows in it. Every such key gets the SAME sentence with a
// different lead: enter, j and k all used to reach separate arms, and the two
// cursor arms answered with nil — a byte-for-byte identical pane, which is the
// reported hang's exact shape one key over from where it was reported.
//
// It words the outcome from assetsQuery, never the live box: that query is what
// the rows on screen actually answer, and a box the operator typed into without
// pressing enter has run nothing.
func (s *PurchaseOrderCreateScreen) assetEmptyNote(lead string) tea.Cmd {
	if lead != "" {
		lead += " · "
	}
	if q := strings.TrimSpace(s.assetsQuery); q != "" {
		return s.assetsNote.say(lead+"no asset matches "+strconv.Quote(pickerClip(q, 16)), StatusWarn)
	}
	return s.assetsNote.say(lead+"this supplier has no assets on file", StatusWarn)
}

// assetsSearchClosedNote answers esc out of the asset search box. It has to
// branch on what is actually in the list: the unconditional "j/k move · enter
// picks" it used to post names three keys that do nothing over an empty one —
// j/k hit the no-op cursor guards and enter answers "no asset matches" — which
// is the bar-honesty rule broken by the frame's own note. The item picker's esc
// path already branches this way through reportItemFilterState.
func (s *PurchaseOrderCreateScreen) assetsSearchClosedNote() tea.Cmd {
	// The rows this note counts are only an ANSWER once the lookup has come
	// back. Esc can close the box with a search still out or after one failed —
	// the frame keeps the rows it was showing either way — and concluding "this
	// supplier has no assets on file" there is a verdict about a request nobody
	// has seen.
	if !s.assetListOnScreen() {
		return s.assetVerdictNote("search closed")
	}
	// Text in the box that was never submitted is not a result. This search is
	// SERVER-side and runs only on enter, so the rows below answer assetsQuery
	// and say nothing at all about what is typed here — concluding "no asset
	// matches X" would be found-nothing where could-not-tell is the fact.
	if q, ran := strings.TrimSpace(s.assetsSearch.Value()), strings.TrimSpace(s.assetsQuery); q != ran {
		typed := strconv.Quote(pickerClip(q, 16)) + " was never run"
		if q == "" {
			typed = "the box was emptied without running"
		}
		// …and say what the rows on the pane DO answer, which means asking
		// whether any came back. Reading assetsQuery alone put "the rows still
		// answer \"Lathe\"" on a frame with no rows at all.
		var answers string
		switch {
		case len(s.assets) == 0 && ran != "":
			answers = "no asset matches " + strconv.Quote(pickerClip(ran, 16))
		case len(s.assets) == 0:
			answers = "this supplier has no assets on file"
		case ran != "":
			answers = "the rows still answer " + strconv.Quote(pickerClip(ran, 16))
		default:
			answers = "the rows are this supplier's whole list"
		}
		return s.assetsNote.say("search closed · "+typed+"\n"+answers, StatusWarn)
	}
	if len(s.assets) > 0 {
		return s.assetsNote.say(
			fmt.Sprintf("search closed · %d asset(s)", len(s.assets)), StatusInfo)
	}
	if q := strings.TrimSpace(s.assetsQuery); q != "" {
		return s.assetsNote.say(
			"search closed · no asset matches "+strconv.Quote(pickerClip(q, 16)), StatusWarn)
	}
	return s.assetsNote.say("search closed · this supplier has no assets on file", StatusWarn)
}

// assetBody is the assets bought from this supplier, as navigable rows.
//
// What the rows ANSWER — the query the last load actually carried, and whether
// the box holds something nobody submitted — is drawn in the pinned header
// (assetScopeRows), not here: it is a label for the list and a label that can
// scroll away from the list it labels is worse than none. The pager's page
// number goes there for the same reason; the two paging KEYS are on the bar,
// named for exactly as long as there is a page on that side.
func (s *PurchaseOrderCreateScreen) assetBody() *jdeLines {
	switch {
	case s.assetsLoading:
		return poEmptyBody(s.paneWidth(), "The asset list is on its way.")
	case s.assetsErr != "":
		return poEmptyBody(s.paneWidth(), "No assets on the pane — the lookup failed.")
	case len(s.assets) == 0:
		if q := strings.TrimSpace(s.assetsQuery); q != "" {
			return poEmptyBody(s.paneWidth(),
				"No asset matches "+strconv.Quote(pickerClip(q, 24))+".")
		}
		return poEmptyBody(s.paneWidth(), s.supplierLabel()+" has no assets on file.")
	}
	l := &jdeLines{}
	cur, room := s.cursorRow(), s.poRowRoom()
	for i, a := range s.assets {
		tag := a.AssetTag
		if tag == "" {
			tag = "—"
		}
		serial := ""
		if a.SerialNumber != "" {
			serial = "  " + StyleMuted.Render("s/n "+a.SerialNumber)
		}
		// The asset TAG is what the machine is called on the shop floor, so it
		// gives last of the two identifiers and the serial is dropped whole
		// before it — but it is OMS-supplied and unbounded, so it is bounded
		// here rather than left to clampToBox.
		tagRoom := room - 2 - poHeaderValueFloor - lipgloss.Width(poRowDropMark)
		if tagRoom < poHeaderValueFloor {
			tagRoom = poHeaderValueFloor
		}
		l.AddRow(i, poPickRow(i, cur, poFitRow(room, a.Name, "  "+pickerClip(tag, tagRoom), serial)))
	}
	return l
}

// assetScopeRows say WHAT the rows on the pane answer.
//
// The first row has to name the query the rows really carry — assetsQuery —
// and never the live textinput. Reading the box let an uncommitted esc plus a
// page draw `search: hovercraft` over an unfiltered page 2 with a green tick
// and a count, and the only line that said the query was never run had by then
// been overwritten by the reply's own note. This search is SERVER-side and runs
// only on Enter, so what is typed and what is shown are different facts and the
// header states both.
func (s *PurchaseOrderCreateScreen) assetScopeRows() []string {
	lw, pane := poHeaderLabelWidth(), s.paneWidth()
	ran := strings.TrimSpace(s.assetsQuery)
	// The PAGE never gives and the QUERY abbreviates: which page these rows
	// come from is a fact the operator pages on, and a query shortened with an
	// ellipsis is still recognisable beside what they typed. Reserved BEFORE
	// the query is clipped, because a bound applied to one part and then
	// appended to is not a bound — the page suffix used to be added after the
	// clip and pushed the row six cells past a 51-column pane.
	page := ""
	if s.assetsPage > 1 || s.assetsHasNext {
		page = fmt.Sprintf(" · page %d", s.assetsPage)
	}
	room := poFieldValueRoom(pane, lw, false) - lipgloss.Width(page)
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	shown := "all of this supplier's assets"
	if ran != "" {
		// The QUOTED string is what is bounded, never a bounded string that is
		// then quoted: strconv.Quote ESCAPES, so a backslash or a double quote
		// comes back two cells and a control rune up to six. Clipping first and
		// quoting after budgets two cells for the quote marks and gets whatever
		// the escaping costs on top — a query of two dozen backslashes doubled
		// to fifty cells in a value area of twenty-six, and clampToBox took the
		// tail, which is the page suffix reserved three lines above for exactly
		// this reason.
		shown = pickerClip(strconv.Quote(ran), room)
	} else {
		shown = pickerClip(shown, room)
	}
	out := []string{renderJDEField(jdeField{
		Label: "Showing", Kind: jdeValue, Value: shown + page}, lw, pane)}
	if s.assetsTyping {
		// The box itself is the header's ESSENTIAL row while it is open
		// (essentialBoxRow), so it is not repeated here.
		return out
	}
	if draft := strings.TrimSpace(s.assetsSearch.Value()); draft != ran {
		// An EMPTIED box and an unrun query are different facts, and a value row
		// drawn blank states neither. Both are "what the box holds that the rows
		// do not answer", so both belong on this row — one as a value, one as
		// the visible absence of one.
		value, dim := pickerClip(draft, poFieldValueRoom(pane, lw, false)), false
		if draft == "" {
			value, dim = "(emptied, never run)", true
		}
		out = append(out, renderJDEField(jdeField{
			Label: "Not run", Kind: jdeValue, Dim: dim, Value: value}, lw, pane))
	}
	return out
}
