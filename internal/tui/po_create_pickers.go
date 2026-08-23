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
// Saying something — the rule these pickers broke
// ---------------------------------------------------------------------------

// Every operator action on these screens that performs work off the terminal
// must report that it is WORKING, and must report FAILURE. A key that declines
// to act must say why. The pickers used to answer several keys with a bare
// `return s, nil`, which redraws a screen byte-for-byte identical to the one
// before the press — and from the operator's seat a keystroke that changes
// nothing and says nothing is indistinguishable from a wedged program.
//
// That is what the "the screen just hangs after I press enter" report actually
// was. Nothing was blocked and nothing was in flight: enter inside the item
// picker's search box only CLOSED the box (the pick needed a second enter), and
// enter over an empty filtered list returned nil. Both redrew the same pixels,
// so the operator concluded the program had stopped. The three states below —
// working, succeeded, failed — are now all visible, and no arm of these
// switches is allowed to be silent.

// pickerNote is a picker's own answer to the last keypress, rendered in the
// screen BODY. The status bar carries the same words, but a Flash expires after
// four seconds (status.go) and the operator who pressed enter and saw nothing is
// exactly the operator still staring at the picker a minute later — so the body
// line is the one that has to survive.
type pickerNote struct {
	text  string
	level StatusLevel
}

// render styles the note for the BODY, folded to the pane by pickerWrap. text
// may also carry explicit newlines, which stay as forced breaks.
//
// Folding is not cosmetic here. Root.View() TRUNCATES the pane rather than
// wrapping it, and the tail of one of these sentences is where the key that
// gets the operator OUT of the state is named — so a clipped hint is the
// silence this whole file exists to remove, wearing a tick mark, and it is
// worse than no hint at all because the operator believes they read it.
func (n pickerNote) render() string {
	if n.text == "" {
		return ""
	}
	mark, style := "", StyleMuted
	switch n.level {
	case StatusError:
		mark, style = "✗ ", StyleStatusError
	case StatusWarn:
		mark, style = "! ", StyleStatusWarn
	case StatusOK:
		mark, style = "✓ ", StyleStatusOK
	}
	// The mark eats two cells of the first line. Budgeting it off every line is
	// two columns conservative on the continuations and costs nothing.
	width := pickerPaneWidth
	if mark != "" {
		width -= lipgloss.Width(mark)
	}
	lines := pickerWrap(n.text, width)
	out := make([]string, 0, len(lines))
	for i, line := range lines {
		if i == 0 {
			out = append(out, style.Render(mark+line))
			continue
		}
		// Continuation lines are muted and already indented by pickerWrap:
		// they carry the way OUT of the state, not the state itself.
		out = append(out, StyleMuted.Render(line))
	}
	return strings.Join(out, "\n")
}

// flash is the note reduced to ONE line for the status bar, which has no room
// for the continuation.
func (n pickerNote) flash() string {
	if i := strings.IndexByte(n.text, '\n'); i >= 0 {
		return n.text[:i]
	}
	return n.text
}

// say records the note and flashes the same words on the status bar. Both, not
// either: the bar is where an operator's eye already goes for "did that work",
// and the body line is what is still there once the flash has gone.
func (n *pickerNote) say(text string, level StatusLevel) tea.Cmd {
	n.text, n.level = text, level
	return Status(n.flash(), level)
}

// pickerClip bounds an operator-supplied string before it goes into a note. The
// pane is 51 columns at the terminal's narrowest supported width and Root.View()
// truncates, so an unbounded search term or supplier name would push the rest of
// the sentence — the part naming the key to press — off the right edge.
func pickerClip(text string, max int) string {
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func (n *pickerNote) clear() { n.text, n.level = "", StatusInfo }

// pickerPaneWidth is the columns a picker frame actually gets at the narrowest
// terminal this project checks against: Root.View() clamps the body to
// screenBodyWidth(80) = 51 (AGENTS.md) and TRUNCATES what does not fit.
//
// Every note and every fixed hint on these screens is folded to it rather than
// hand-counted against it. Hand-counting is what produced the class of bug this
// constant exists to close — a note reads fine at the width its author had in
// mind and then grows a prefix, a supplier name or a match count and silently
// loses the key it was written to name.
var pickerPaneWidth = screenBodyWidth(80)

// pickerWrap folds text onto as many lines as it needs to fit width, and
// indents every line after the first by two so a folded sentence still reads as
// one. Explicit newlines in text are forced breaks.
//
// It folds at the " · " joints these hints are built from before it falls back
// to spaces, because those joints separate whole claims ("b picks another line
// source", "esc cancels the order") and a claim split across two lines is
// harder to read than one claim per line. The separator itself is dropped at a
// fold — the indent already says the line is a continuation.
func pickerWrap(text string, width int) []string {
	const indent = "  "
	if width < 12 {
		width = 12
	}
	var out []string
	budget := func() int {
		if len(out) == 0 {
			return width
		}
		return width - len(indent)
	}
	push := func(line string) {
		if len(out) == 0 {
			out = append(out, line)
			return
		}
		out = append(out, indent+line)
	}
	for _, para := range strings.Split(text, "\n") {
		cur := ""
		flush := func() {
			if cur != "" {
				push(cur)
				cur = ""
			}
		}
		for _, seg := range strings.Split(para, " · ") {
			if seg == "" {
				continue
			}
			if cur != "" && lipgloss.Width(cur)+3+lipgloss.Width(seg) <= budget() {
				cur += " · " + seg
				continue
			}
			flush()
			if lipgloss.Width(seg) <= budget() {
				cur = seg
				continue
			}
			// One claim too long for a line of its own — fold it on spaces
			// rather than let clampToBox take the end off it.
			for _, word := range pickerWords(seg, width-len(indent)) {
				switch {
				case cur == "":
					cur = word
				case lipgloss.Width(cur)+1+lipgloss.Width(word) <= budget():
					cur += " " + word
				default:
					flush()
					cur = word
				}
			}
		}
		flush()
	}
	return out
}

// pickerWords splits a run of text into pieces no wider than width, breaking
// mid-token when a token is wider than that on its own. The tokens that need it
// are not English: a failed lookup carries whatever OMS put in the response
// body, and a URL or an unspaced JSON blob has nowhere to fold. Better to break
// one in the middle than to hand clampToBox a 200-cell line and lose all but
// the first 51 of it.
func pickerWords(text string, width int) []string {
	if width < 4 {
		width = 4
	}
	var out []string
	for _, word := range strings.Fields(text) {
		for lipgloss.Width(word) > width {
			r := []rune(word)
			cut := width
			if cut > len(r) {
				cut = len(r)
			}
			out = append(out, string(r[:cut]))
			word = string(r[cut:])
		}
		if word != "" {
			out = append(out, word)
		}
	}
	return out
}

// pickerHint renders a fixed muted line — a way out, a working line, a summary
// — folded to the pane. Nothing on these screens writes such a line directly
// any more: routing them all through one function is what keeps the next one
// from being the one that overruns.
func pickerHint(text string) string {
	lines := pickerWrap(text, pickerPaneWidth)
	for i, line := range lines {
		lines[i] = StyleMuted.Render(line)
	}
	return strings.Join(lines, "\n")
}

// pickerFail renders a failed lookup as headline + detail. The detail goes on
// its own folded continuation because an OMS error string is arbitrarily long:
// on one line the label alone ("looking up this supplier's items failed:" is 40
// of the 51 columns) leaves the operator reading a colon and nothing after it.
func pickerFail(what, detail string) string {
	if detail == "" {
		return pickerNote{what, StatusError}.render()
	}
	return pickerNote{what + "\n" + detail, StatusError}.render()
}

// pickerWayOut is what every picker frame says when the list itself cannot help
// — both keys are live in every non-typing picker state.
const pickerWayOut = "b picks another line source · esc cancels the order"

// searchBoxWayOut is the same sentence while the SEARCH BOX owns the keyboard,
// where it would be a lie: b is a letter going into the query and esc only
// closes the box, it does not cancel the order. A frame that prints both claims
// at once is the bar-honesty defect, two frames away from the eight instances
// of it just fixed on the list screens.
const searchBoxWayOut = "esc closes the search"

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

type poAssetsLoadedMsg struct {
	supplierID int
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
	return func() tea.Msg {
		p, err := deps.OMS.ListAssetsForSupplier(ctx, supplierID, search, page)
		if err != nil {
			return poAssetsLoadedMsg{supplierID: supplierID, err: err}
		}
		hasNext := p.Next != nil && *p.Next != ""
		return poAssetsLoadedMsg{supplierID: supplierID, rows: p.Results, hasNext: hasNext}
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
		// Clear the PREVIOUS failure. renderItemPick shows the error instead of
		// the list, so a stale string left here would hide a load that worked.
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
		return s.itemSuppliersNote.say(
			fmt.Sprintf("%d catalog item(s) loaded · / searches", len(m.rows)), StatusOK)
	case poAssetsLoadedMsg:
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
		if len(m.rows) == 0 {
			// A search that found nothing is a RESULT, not a blank screen: say
			// what was searched for so the operator can tell "no such asset"
			// from "I mistyped".
			if q := strings.TrimSpace(s.assetsSearch.Value()); q != "" {
				return s.assetsNote.say("no asset matches "+strconv.Quote(pickerClip(q, 16))+"\n/ edits the search · b picks another source", StatusWarn)
			}
			return s.assetsNote.say("this supplier has no assets on file", StatusWarn)
		}
		return s.assetsNote.say(fmt.Sprintf("%d asset(s) · enter picks the highlighted row", len(m.rows)), StatusOK)
	}
	return nil
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
	if len(s.itemSuppliersAll) == 0 {
		return s.itemSuppliersNote.say(s.noCatalogSentence(), StatusWarn)
	}
	return s.itemSuppliersNote.say(
		fmt.Sprintf("%d catalog item(s) · / searches · r reloads", len(s.itemSuppliersAll)), StatusInfo)
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

func (s *PurchaseOrderCreateScreen) updateReorderPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.reorderCursor < len(s.reorderItems)-1 {
			s.reorderCursor++
		}
	case "k", "up":
		if s.reorderCursor > 0 {
			s.reorderCursor--
		}
	case " ":
		// Mark/unmark this row for a bulk add. Marking several rows and
		// pressing enter is the middle ground between adding one item at a
		// time and taking the supplier's whole queue with 'a' (sc-ytr5).
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
		// "a supplier with 15 items is ~30 keystrokes". Every row is staged
		// with its suggested_quantity; the review cart is where individual
		// lines get adjusted (ctrl+e), so land there.
		return s, s.addReorderLines(s.reorderItems)
	case "enter":
		// With rows marked, enter stages exactly those (in list order).
		// Otherwise it keeps the original one-row behavior: open the line
		// form pre-filled so quantity/date/cost can be set before staging.
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
			// Nothing to stage. Say so rather than answering the key with the
			// blank redraw that reads as a wedge.
			return s, Status("nothing flagged for reorder under this supplier — b picks another line source", StatusWarn)
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
		desc := it.ItemName
		if desc == "" {
			desc = it.SKU
		}
		// The reorder_data row carries no quantity_per_package, so a reorder
		// line stays single-basis (qpp 0) — the case-cost toggle is offered
		// from the inventory-items picker, which does expose qpp. (op-7j8v)
		s.enterLinePhase(it.ItemSupplierID, nil, desc, qty, unitCost, 0, 0)
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
	return poCartLine{item: line, label: label}
}

// addReorderLines stages every supplied reorder row and drops the operator in
// the review cart, where any individual line can be adjusted with ctrl+e or
// dropped with ctrl+x. Clears the marks so the picker is clean if it is
// re-entered for a second batch.
func (s *PurchaseOrderCreateScreen) addReorderLines(items []omsapi.ReorderDataItem) tea.Cmd {
	if len(items) == 0 {
		s.errMsg = "nothing to add — this supplier has no reorder items"
		return Status(s.errMsg, StatusError)
	}
	for _, it := range items {
		s.lines = append(s.lines, reorderCartLine(it))
	}
	s.reorderSelected = map[int]bool{}
	s.errMsg = ""
	s.phase = poPhaseReview
	s.reviewCursor = len(s.lines) - len(items) // first line of this batch
	s.poNotes.Focus()
	return tea.Batch(
		Status(fmt.Sprintf("added %d line(s) (%d in cart)", len(items), len(s.lines)), StatusOK),
		textinput.Blink,
	)
}

func (s *PurchaseOrderCreateScreen) renderReorderPick() string {
	if s.reorderLoading {
		return pickerHint("Looking up what " + s.supplierLabel() + " has flagged for reorder…")
	}
	if s.reorderLoadErr != "" {
		return pickerFail("reading the reorder queue failed", s.reorderLoadErr) + "\n" +
			pickerHint(pickerWayOut)
	}
	if len(s.reorderItems) == 0 {
		return pickerHint("Nothing flagged for reorder under this supplier.") + "\n" +
			pickerHint(pickerWayOut)
	}
	var b strings.Builder
	b.WriteString(renderWindowedList(
		len(s.reorderItems), s.reorderCursor,
		func(i int) string {
			it := s.reorderItems[i]
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
			return fmt.Sprintf(
				"%s%s  qty %d (current %d / min %d)%s%s",
				mark, it.ItemName, it.SuggestedQuantity, it.CurrentStock, it.MinimumStock, cost, tag,
			)
		},
	))
	summary := fmt.Sprintf("space marks a row · a adds all %d item(s) at their suggested quantities", len(s.reorderItems))
	if n := len(s.reorderSelected); n > 0 {
		summary = fmt.Sprintf("%d marked — enter adds them · a adds all %d", n, len(s.reorderItems))
	}
	b.WriteString("\n" + pickerHint(summary))
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase 3b: Inventory items picker
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateItemPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.itemSuppliersTyping {
		switch m.Type {
		case tea.KeyEsc:
			// Close the box but KEEP the filter — this is the browse path, so
			// it has to say that the rows still on screen are a filtered subset
			// and that j/k now move again.
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
		// enter that used to answer it with silence.
		s.itemSuppliersNote = itemFilterNote(s.itemSuppliersSearch.Value(), len(s.itemSuppliers), len(s.itemSuppliersAll), "")
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.itemSuppliersCur < len(s.itemSuppliers)-1 {
			s.itemSuppliersCur++
		}
	case "k", "up":
		if s.itemSuppliersCur > 0 {
			s.itemSuppliersCur--
		}
	case "/":
		if len(s.itemSuppliersAll) == 0 {
			// This filter is client-side over the loaded catalog, so with
			// nothing loaded the box can only ever answer "no match" — and
			// opening it would take the keyboard away from b and esc, the two
			// keys that can still do something here. Decline, and say why.
			return s, s.emptyCatalogNote()
		}
		s.itemSuppliersTyping = true
		s.itemSuppliersSearch.Focus()
		return s, tea.Batch(
			s.itemSuppliersNote.say("type to search · enter picks the match\nesc closes the search and keeps the filter", StatusInfo),
			textinput.Blink,
		)
	case "r":
		// The catalog is held per supplier and re-entering the picker no longer
		// re-walks it (see the 'i' arm in po_create.go), so there has to be a
		// named way to go and ask again — a cache with no refresh is its own
		// silent-wrong-answer bug. Named in the bar, and it does exactly what
		// it says: the frame goes back to "looking up…" because work really is
		// happening this time.
		s.itemSuppliersLoad = true
		s.itemSuppliersErr = ""
		s.itemSuppliersFor = 0
		return s, tea.Batch(
			s.itemSuppliersNote.say("reloading what "+s.supplierLabel()+" sells…", StatusInfo),
			s.loadItemSuppliersForSupplier(),
		)
	case "enter":
		return s, s.commitHighlightedItem()
	}
	return s, nil
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
	s.applyItemSupplierFilter()
	q := strings.TrimSpace(s.itemSuppliersSearch.Value())
	switch {
	case len(s.itemSuppliersAll) == 0:
		return s.emptyCatalogNote()
	case len(s.itemSuppliers) == 0:
		s.itemSuppliersCur = 0
		s.itemSuppliersNote = itemFilterNote(q, 0, len(s.itemSuppliersAll), "")
		return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
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
		return s.reportItemFilterState("")
	}
}

// commitHighlightedItem is enter over the item list itself. The empty case is
// the other half of the dead end: with no rows there is nothing to pick, and
// answering that with nil is how the picker told the operator nothing at all.
func (s *PurchaseOrderCreateScreen) commitHighlightedItem() tea.Cmd {
	if len(s.itemSuppliersAll) == 0 {
		return s.emptyCatalogNote()
	}
	if len(s.itemSuppliers) == 0 {
		q := strings.TrimSpace(s.itemSuppliersSearch.Value())
		s.itemSuppliersNote = itemFilterNote(q, 0, len(s.itemSuppliersAll), "")
		return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
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
	// PackQuantity (quantity_per_package) drives the case-cost toggle: when
	// > 1 the line form offers per-case entry prefilled from package_cost
	// (deriving unit_cost = case_cost / qpp — op-7j8v).
	s.enterLinePhase(&id, nil, desc, 1, unitCost, pkgCost, row.PackQuantity)
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

// emptyCatalogNote answers a pick attempted against a supplier with no catalog
// at all. itemFilterNote cannot speak for that state: with nothing loaded and
// no query its unfiltered wording is "0 item(s) · enter picks the highlighted
// row", which offers the key that just declined to act as the way forward and
// overwrites the warning the empty load posted with something less true.
func (s *PurchaseOrderCreateScreen) emptyCatalogNote() tea.Cmd {
	way := pickerWayOut
	if s.itemSuppliersTyping {
		way = searchBoxWayOut
	}
	return s.itemSuppliersNote.say(s.noCatalogSentence()+"\n"+way, StatusWarn)
}

// reportItemFilterState is the note for "the filter changed and nothing was
// picked". prefix, when given, leads with what the key did.
func (s *PurchaseOrderCreateScreen) reportItemFilterState(prefix string) tea.Cmd {
	q := strings.TrimSpace(s.itemSuppliersSearch.Value())
	s.itemSuppliersNote = itemFilterNote(q, len(s.itemSuppliers), len(s.itemSuppliersAll), prefix)
	return Status(s.itemSuppliersNote.flash(), s.itemSuppliersNote.level)
}

// itemFilterNote words the three outcomes of a filter. A zero-match note names
// the query AND the catalog size, because those two facts together are what
// tell the operator whether to retype or to conclude this supplier does not
// sell the thing — "No inventory items match" alone said neither, and said it
// about a catalog that (before ListItemSuppliersForSupplier paged) might not
// even have been fully loaded.
func itemFilterNote(query string, matched, total int, prefix string) pickerNote {
	lead := ""
	if prefix != "" {
		lead = prefix + " · "
	}
	q := strconv.Quote(pickerClip(query, 16))
	switch {
	case query == "":
		return pickerNote{fmt.Sprintf("%s%d item(s) · enter picks the highlighted row", lead, total), StatusInfo}
	case matched == 0:
		return pickerNote{
			fmt.Sprintf("no match for %s (%d in catalog)\nedit the search · esc then b for another source", q, total),
			StatusWarn,
		}
	case matched == 1:
		return pickerNote{fmt.Sprintf("%s1 of %d match %s · enter picks it", lead, total, q), StatusOK}
	}
	return pickerNote{
		fmt.Sprintf("%s%d of %d match %s · j/k choose · enter picks", lead, matched, total, q),
		StatusOK,
	}
}

func (s *PurchaseOrderCreateScreen) renderItemPick() string {
	var b strings.Builder
	if s.itemSuppliersSearch.Value() != "" || s.itemSuppliersTyping {
		b.WriteString(StyleMuted.Render("filter: ") + s.itemSuppliersSearch.View() + "\n\n")
	}
	if s.itemSuppliersLoad {
		// Name the WORK, not the wait. "Loading…" tells the operator a
		// rectangle is busy; this tells them which request is out and against
		// whom, which is the difference between a status line and a spinner.
		// A note set by the key that started this load (r) is more specific
		// still — it says the operator's own press is what is out.
		if note := s.itemSuppliersNote.render(); note != "" {
			b.WriteString(note)
			return b.String()
		}
		b.WriteString(pickerHint("Looking up the items " + s.supplierLabel() + " sells…"))
		return b.String()
	}
	// While the search box owns the keyboard, b is a letter going into the
	// query and esc only closes the box. Naming them as "another line source"
	// and "cancels the order" there would have the frame printing two
	// contradictory claims at once.
	wayOut := pickerWayOut
	if s.itemSuppliersTyping {
		wayOut = searchBoxWayOut
	}
	if s.itemSuppliersErr != "" {
		b.WriteString(pickerFail("looking up this supplier's items failed", s.itemSuppliersErr) + "\n")
		if s.itemSuppliersTyping {
			b.WriteString(pickerHint(wayOut)) // r is a letter in the query too
		} else {
			b.WriteString(pickerHint("r retries the lookup · " + wayOut))
		}
		return b.String()
	}
	if len(s.itemSuppliers) == 0 {
		// Never the bare "No inventory items match." this used to print: that
		// sentence is the same whether the supplier sells nothing, the search
		// missed, or the catalog failed to load, and it names no key out.
		if note := s.itemSuppliersNote.render(); note != "" {
			b.WriteString(note)
		} else {
			b.WriteString(pickerHint(s.noCatalogSentence() + "."))
		}
		b.WriteString("\n" + pickerHint(wayOut))
		return b.String()
	}
	if note := s.itemSuppliersNote.render(); note != "" {
		b.WriteString(note + "\n\n")
	}
	b.WriteString(renderWindowedList(
		len(s.itemSuppliers), s.itemSuppliersCur,
		func(i int) string {
			it := s.itemSuppliers[i]
			sku := it.SupplierSKU
			if sku == "" {
				sku = "—"
			}
			cost := ""
			if it.UnitCost != "" {
				cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s", it.UnitCost))
			}
			// Flag case-packed items so the operator knows a case-cost entry
			// will be offered on the line form (op-7j8v).
			pack := ""
			if it.PackQuantity > 1 {
				pack = "  " + StyleStatusOK.Render(fmt.Sprintf("case ×%d", it.PackQuantity))
			}
			lead := ""
			if it.LeadTimeDays > 0 {
				lead = "  " + StyleMuted.Render(fmt.Sprintf("lead %gd", it.LeadTimeDays))
			}
			return fmt.Sprintf("%s  %s%s%s%s", it.ItemName, sku, cost, pack, lead)
		},
	))
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase 3c: Assets-from-supplier picker (server-side search + pagination)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateAssetPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.assetsTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.assetsTyping = false
			s.assetsSearch.Blur()
			return s, s.assetsNote.say("search closed · j/k move · enter picks", StatusInfo)
		case tea.KeyEnter:
			// Unlike the item picker this really does go off the terminal, so
			// the note says so BEFORE the request leaves: the reply repaints it
			// with the result (handlePickerLoaded), and a slow or failed lookup
			// leaves the operator reading "searching…" rather than a screen that
			// has not moved.
			s.assetsTyping = false
			s.assetsSearch.Blur()
			s.assetsPage = 1
			s.assetsLoading = true
			s.assetsErr = ""
			q := strings.TrimSpace(s.assetsSearch.Value())
			what := "this supplier's assets"
			if q != "" {
				what = strconv.Quote(q)
			}
			return s, tea.Batch(
				s.assetsNote.say("searching "+what+"…", StatusInfo),
				s.loadAssetsForSupplier(s.assetsSearch.Value()),
			)
		}
		var cmd tea.Cmd
		s.assetsSearch, cmd = s.assetsSearch.Update(m)
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.assetsCursor < len(s.assets)-1 {
			s.assetsCursor++
		}
	case "k", "up":
		if s.assetsCursor > 0 {
			s.assetsCursor--
		}
	case "/":
		s.assetsTyping = true
		s.assetsSearch.Focus()
		return s, tea.Batch(
			s.assetsNote.say("type to search · enter runs the search", StatusInfo),
			textinput.Blink,
		)
	case "]":
		// The two paging keys are named only alongside a page that exists, so
		// the bar stays honest — but the arm still has to answer when the state
		// moved underneath the operator between the render and the press.
		if !s.assetsHasNext {
			return s, s.assetsNote.say(fmt.Sprintf("already on the last page (page %d)", s.assetsPage), StatusWarn)
		}
		s.assetsPage++
		s.assetsLoading = true
		s.assetsErr = ""
		return s, tea.Batch(
			s.assetsNote.say(fmt.Sprintf("loading page %d…", s.assetsPage), StatusInfo),
			s.loadAssetsForSupplier(s.assetsSearch.Value()),
		)
	case "[":
		if s.assetsPage <= 1 {
			return s, s.assetsNote.say("already on the first page", StatusWarn)
		}
		s.assetsPage--
		s.assetsLoading = true
		s.assetsErr = ""
		return s, tea.Batch(
			s.assetsNote.say(fmt.Sprintf("loading page %d…", s.assetsPage), StatusInfo),
			s.loadAssetsForSupplier(s.assetsSearch.Value()),
		)
	case "enter":
		if len(s.assets) == 0 {
			// Same dead end the item picker had: nothing to pick is a fact the
			// operator has to be told, not a reason to answer with nil.
			if q := strings.TrimSpace(s.assetsSearch.Value()); q != "" {
				return s, s.assetsNote.say("no asset matches "+strconv.Quote(pickerClip(q, 16))+"\n/ edits the search · b picks another source", StatusWarn)
			}
			return s, s.assetsNote.say("no assets to pick\nb picks another line source", StatusWarn)
		}
		if s.assetsCursor < 0 || s.assetsCursor >= len(s.assets) {
			s.assetsCursor = 0
		}
		a := s.assets[s.assetsCursor]
		// Asset.ID is a polyglot `any` (UUID strings + int rows both
		// occur in inventory). Convert to a string for the PO create
		// payload — backend's asset_id accepts the string repr.
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

func (s *PurchaseOrderCreateScreen) renderAssetPick() string {
	var b strings.Builder
	if s.assetsSearch.Value() != "" || s.assetsTyping {
		b.WriteString(StyleMuted.Render("search: ") + s.assetsSearch.View() + "\n\n")
	}
	if s.assetsLoading {
		b.WriteString(pickerHint("Looking up the assets " + s.supplierLabel() + " supplied…"))
		return b.String()
	}
	// Same gate as the item picker: with the search box open, '/' is a slash in
	// the query, 'b' is a letter, and esc closes the box rather than the order.
	if s.assetsErr != "" {
		b.WriteString(pickerFail("looking up this supplier's assets failed", s.assetsErr) + "\n")
		if s.assetsTyping {
			b.WriteString(pickerHint(searchBoxWayOut))
		} else {
			b.WriteString(pickerHint("/ retries with a search · " + pickerWayOut))
		}
		return b.String()
	}
	if len(s.assets) == 0 {
		if note := s.assetsNote.render(); note != "" {
			b.WriteString(note)
		} else {
			b.WriteString(pickerHint(s.supplierLabel() + " has no assets on file."))
		}
		if s.assetsTyping {
			b.WriteString("\n" + pickerHint(searchBoxWayOut))
		} else {
			b.WriteString("\n" + pickerHint("/ searches · "+pickerWayOut))
		}
		return b.String()
	}
	if note := s.assetsNote.render(); note != "" {
		b.WriteString(note + "\n\n")
	}
	b.WriteString(renderWindowedList(
		len(s.assets), s.assetsCursor,
		func(i int) string {
			a := s.assets[i]
			tag := a.AssetTag
			if tag == "" {
				tag = "—"
			}
			serial := ""
			if a.SerialNumber != "" {
				serial = "  " + StyleMuted.Render("s/n "+a.SerialNumber)
			}
			return fmt.Sprintf("%s  %s%s", a.Name, tag, serial)
		},
	))
	pager := fmt.Sprintf("page %d", s.assetsPage)
	if s.assetsHasNext {
		pager += " · ] next"
	}
	if s.assetsPage > 1 {
		pager += " · [ prev"
	}
	b.WriteString("\n" + pickerHint(pager))
	return b.String()
}

// ---------------------------------------------------------------------------
// Shared windowed-list renderer
// ---------------------------------------------------------------------------

// renderWindowedList draws `total` items via the supplied formatter,
// keeping `cursor` on screen with ~10 lines of context. Same pattern
// as the supplier picker so all four pickers look consistent — and a
// free function rather than a method on the create screen, because the
// PO edit screen's association pickers draw their lists the same way.
func renderWindowedList(total, cursor int, formatRow func(int) string) string {
	const window = 10
	start := cursor - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > total {
		end = total
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
		caret := "    "
		if i == cursor {
			caret = "  ▸ "
		}
		line := caret + formatRow(i)
		if i == cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < total {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below\n", total-end)))
	}
	return b.String()
}
