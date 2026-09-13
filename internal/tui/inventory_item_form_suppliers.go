// The item form's SUPPLIERS band — who sells this inventory item, for how much,
// and how long they take to deliver it (sc-7wag).
//
// Ian's ask: "when I edit the inventory items, my expectation is to see the
// suppliers and their associated information of the inventory items on the edit
// screen as well." Before this the sourcing context was only reachable by
// leaving the form (the item detail's `s` → Suppliers screen), so an operator
// amending an item — changing a reorder point, say — could not see what the item
// costs or how long it takes to arrive while making that decision.
//
// It needs NO new request and NO backend change: InventoryItemSerializer nests
// the through-rows as `suppliers` (backend/inventory/serializers.py:544, from
// the `item_suppliers__supplier` prefetch on InventoryItemViewSet, and the
// retrieve-only InventoryItemDetailSerializer inherits the field), the read
// struct already decodes them as `Item.Suppliers`, and the form's edit-mode
// hydrate already fetches the item detail. The rows arrive in the model's own
// order — `["-is_primary", "unit_cost"]` — so the preferred supplier leads and
// the cheapest follows, which is the order the band wants anyway.
//
// The FIELD SELECTION mirrors the web's SupplierRelationshipForm (supplier,
// supplier SKU, URL, unit cost, package cost, quantity per package, lead time,
// primary), which InventoryItemFormPage has carried all along — ScanTTY is the
// surface that diverged. Deliberately NOT mirrored: the web sub-form is
// EDITABLE. Here the band is READ-ONLY and Ctrl-E opens ItemSuppliersScreen,
// which already has the full add / edit / remove / set-primary surface; that
// reaches the same place without this sheet growing a second resource's writes.
//
// Shape: a JD Edwards DETAIL GRID, the same fold po_edit.go uses for a purchase
// order's lines and asset_form_supplies.go for an asset's parts — a muted column
// header, one dense row per link, and the readings that did not earn a column
// indented underneath it. The rows are NAVIGABLE (they extend the form's cursor
// past its fields) because the body window keeps the CURSOR's block on screen:
// lines hanging below the last navigable row of a sheet this long could never be
// scrolled to (sc-hf1z).
//
// Create mode draws no band at all: an item that does not exist yet has nothing
// linked to it, and a permanently empty section is noise on every registration.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Detail-grid columns. The supplier column takes whatever the pane has left; the
// rest are fixed so the costs and lead times line up down the sheet — comparing
// them across suppliers is the whole reason to list more than one.
//
// The SKU rides INSIDE the supplier cell rather than taking a column of its own:
// the pane is 81 columns at a 110-column terminal and 51 at an 80, so a fixed
// SKU column is affordable on the wide one and ruinous on the narrow one, while
// a cell that carries "name (sku)" simply gets shorter.
const (
	itemSupplierNumW  = 2
	itemSupplierCostW = 9
	itemSupplierLeadW = 5
	// The CEILINGS on the measured widths of those three columns. A column is
	// reserved at what this item's own links will really put in it
	// (jdeGridFactW), because padCell never truncates and a value wider than its
	// budget widened the whole ROW: a six-figure pack cost is "$123456.78", ten
	// cells in a nine-cell column, and the row ran a cell past the pane at every
	// drawable width up to 94 columns with the LEAD TIME as the thing clampToBox
	// took. The ceilings are set past any figure OMS can legitimately serve — a
	// hundred links, a seven-figure cost, a lead time in years — so an ordinary
	// row is never shortened, and they exist only so that a garbage value cannot
	// eat the supplier column and push the row off the pane anyway.
	itemSupplierNumMaxW  = 4
	itemSupplierCostMaxW = 13
	itemSupplierLeadMaxW = 8
	// itemSupplierStar marks the preferred link, in the supplier cell as plain
	// text (a focused row reverse-videos whole, and an inner style would end the
	// highlight partway through it) and again as a styled reading underneath.
	itemSupplierStar = "★"
)

// supplierContIndent puts a continuation line under the supplier column, so it
// reads as part of the row above rather than as a row of its own.
//
// It takes the MEASURED index width rather than the constant, because the index
// column grows with the number of links (supplierFit) and an indent built from
// the constant would leave every reading two cells left of the column it hangs
// under on an item with a hundred of them.
func (s *InventoryItemFormScreen) supplierContIndent() string {
	return strings.Repeat(" ", len(jdeIndent)+s.supplierFit(s.supplierRows()).numW+2)
}

// supplierRows are the links nested on the loaded item. Nil in create mode and
// while the fetch is still out, which is what keeps the band off both.
func (s *InventoryItemFormScreen) supplierRows() []omsapi.ItemSupplier {
	if !s.edit || s.item == nil {
		return nil
	}
	return s.item.Suppliers
}

// supplierBandRows is how many NAVIGABLE rows the band contributes. An item with
// no links still gets one: it is the door to the screen that adds the first one,
// and a band you cannot put the cursor on is a band with no door.
func (s *InventoryItemFormScreen) supplierBandRows() int {
	if !s.edit {
		return 0
	}
	if n := len(s.supplierRows()); n > 0 {
		return n
	}
	return 1
}

// rowCount is the sheet's navigable rows: its visible fields, then the band.
// Cursor movement, paging and the rebuild clamp all measure against this rather
// than len(s.fields).
func (s *InventoryItemFormScreen) rowCount() int {
	return len(s.fields) + s.supplierBandRows()
}

// onSupplierRow reports which band row the cursor is standing on, if any. When
// the item has no links at all, row 0 is the empty-state placeholder. The field
// helpers already answer "not a field" for these rows (currentFieldID is bounded
// by len(s.fields)), which is what stops every field gesture from firing here.
func (s *InventoryItemFormScreen) onSupplierRow() (int, bool) {
	idx := s.cursor - len(s.fields)
	if idx < 0 || idx >= s.supplierBandRows() {
		return 0, false
	}
	return idx, true
}

// openSuppliersCmd is the Ctrl-E door: the item's supplier links, where they can
// actually be changed. It is the same gesture the rest of this sheet uses —
// "Ctrl-E opens whatever the highlighted row IS" — rather than a new accelerator
// (sc-rdrk retired those).
func (s *InventoryItemFormScreen) openSuppliersCmd() tea.Cmd {
	if !s.edit || s.itemID == "" {
		return nil
	}
	name := ""
	if s.item != nil {
		// The SAVED name, not the one in the Name field: an unsaved rename is not
		// what the supplier screen is about to show links for.
		name = s.item.Name
	}
	return SwitchTo(WSInventory, NewItemSuppliersScreen(s.deps, s.itemID, name))
}

// supplierBand appends the band to the form body. The heading says what the band
// is and that it is read-only; it names no key, because the bar is where keys
// are learned.
func (s *InventoryItemFormScreen) supplierBand(l *jdeLines) {
	if !s.edit {
		return
	}
	rows := s.supplierRows()
	base := len(s.fields)
	focused, onBand := s.onSupplierRow()

	l.Add("")
	l.Add(s.supplierHeading(len(rows)))

	if len(rows) == 0 {
		empty := "(no suppliers linked to this item)"
		if onBand {
			l.AddRow(base, jdeIndent+StyleJDEFieldFocused.Render(empty))
		} else {
			l.AddRow(base, jdeIndent+StyleMuted.Render(empty))
		}
		for _, line := range s.supplierWarnLines() {
			l.AddRow(base, line)
		}
		return
	}

	fit := s.supplierFit(rows)
	l.Add(StyleMuted.Render(itemSupplierGridRow("#", "Supplier", s.supplierCostHeader(), "Lead", "", fit)))
	for i, sup := range rows {
		row := itemSupplierGridRow(
			strconv.Itoa(i+1),
			itemSupplierCell(sup, fit.nameW),
			s.supplierCostCell(sup),
			itemSupplierLeadCell(sup),
			itemSupplierLeadMark(sup),
			fit,
		)
		switch s.supplierRowKind(i, sup) {
		case supplierRowFocused:
			row = StyleJDEFieldFocused.Render(row)
		case supplierRowDim:
			row = StyleMuted.Render(row)
		}
		// Every line of a link shares its row, so the window can never separate a
		// cost or a URL from the supplier it belongs to.
		l.AddRow(base+i, row)
		if onBand && focused == i {
			for _, line := range s.supplierWarnLines() {
				l.AddRow(base+i, line)
			}
		}
		for _, meta := range s.supplierMetaLines(sup) {
			l.AddRow(base+i, meta)
		}
		if url := s.supplierURLLine(sup); url != "" {
			l.AddRow(base+i, url)
		}
	}
}

// supplierRowKind is how a link's grid row is drawn, as an enum rather than as
// the style itself, so that the treatment survives as a DECISION something can
// assert (sc-lmsi/sc-rdrk).
//
// It was once the ONLY way to assert it, because lipgloss renders flat in a test
// binary. It is not any more: withColorProfile forces the sequences back on and
// jdeCells (jde_cells_test.go) decodes a frame into cells carrying their
// attributes. The enum stays because naming the four readings is clearer than
// four inline styles — not because the render is unassertable.
type supplierRowKind int

const (
	supplierRowPlain supplierRowKind = iota
	// supplierRowFocused is picked out end to end, as a focused field row is —
	// the whole row IS the value here, so there is no input area to reverse on
	// its own.
	supplierRowFocused
	// supplierRowDim is a link nobody can buy through today. It is still worth
	// SEEING: dimmed rather than hidden, so an operator wondering where an old
	// supplier went can see that it has not gone anywhere.
	supplierRowDim
)

func (s *InventoryItemFormScreen) supplierRowKind(i int, sup omsapi.ItemSupplier) supplierRowKind {
	if focused, onBand := s.onSupplierRow(); onBand && focused == i {
		return supplierRowFocused
	}
	if sup.IsDiscontinued || !sup.IsActive {
		return supplierRowDim
	}
	return supplierRowPlain
}

// supplierHeading names the band and says it is read-only. The aside is dropped
// to a shorter reading — and then dropped entirely — rather than being allowed to
// overrun: clampToBox truncates a long row with nothing to show it did (sc-ye0i),
// and this line is 60 columns wide against the 51 an 80-column terminal has. It
// names no key either way; the bar is where keys are learned.
func (s *InventoryItemFormScreen) supplierHeading(n int) string {
	title := fmt.Sprintf("Suppliers (%d)", n)
	head := StyleJDEHeading.Render(title)
	budget := s.bodyWidth()
	for _, note := range []string{"(read-only — managed on the Suppliers screen)", "(read-only)"} {
		if budget <= 0 || lipgloss.Width(title)+2+lipgloss.Width(note) <= budget {
			return head + "  " + StyleMuted.Render(note)
		}
	}
	return head
}

// itemSupplierFit is the band's column widths at this pane, for this item's own
// links: the three FACT columns reserved at what their values really need, and
// the supplier column given whatever that leaves.
//
// It is poFitLineGrid's shape one grid over, and it replaced three fixed
// constants for the reason recorded at jdeGridFactW: padCell pads and never
// truncates, so a cost or a lead time wider than its constant widened the whole
// row rather than losing its own tail, and what went off the pane was the
// column drawn last.
type itemSupplierFit struct {
	nameW int
	numW  int
	costW int
	leadW int
}

// supplierFit measures the band. The supplier column keeps a FLOOR so a narrow
// terminal shortens the name rather than collapsing the column, and a CEILING so
// a wide one does not strand the costs out at the far right of an otherwise empty
// row. The fallback width is what a 100-column terminal has.
func (s *InventoryItemFormScreen) supplierFit(rows []omsapi.ItemSupplier) itemSupplierFit {
	const minNameW, maxNameW = 12, 40
	width := 76
	if s.terminalWidth > 0 {
		width = screenBodyWidth(s.terminalWidth)
	}
	fit := itemSupplierFit{
		numW:  itemSupplierNumW,
		costW: itemSupplierCostW,
		leadW: itemSupplierLeadW,
	}
	for i, sup := range rows {
		fit.numW = jdeGridFactW(fit.numW, itemSupplierNumMaxW, strconv.Itoa(i+1))
		fit.costW = jdeGridFactW(fit.costW, itemSupplierCostMaxW, s.supplierCostCell(sup))
		// A marked cell reserves its MARK on top of the number's own ceiling, so
		// the ceiling still bounds only the number (leadTimeFactCell).
		leadCeil := itemSupplierLeadMaxW
		if mark := itemSupplierLeadMark(sup); mark != "" {
			leadCeil += 1 + lipgloss.Width(mark)
		}
		fit.leadW = jdeGridFactW(fit.leadW, leadCeil, itemSupplierLeadText(sup))
	}
	// The HEADER is a row of this grid too, and "Pack cost" is nine cells.
	fit.costW = jdeGridFactW(fit.costW, itemSupplierCostMaxW, s.supplierCostHeader())
	fixed := len(jdeIndent) + fit.numW + 2 + 2 + fit.costW + 2 + fit.leadW
	// THE TRADE, where the pane forces one: the supplier column is at its floor
	// and the row still does not fit. A marked lead column is the one fact that
	// can give without lying, because its NUMBER gives and its provenance mark
	// stays (leadTimeFactCell) — so the column narrows toward leadTimeMarkFloor
	// before the row is allowed to run past the pane. Only a garbage figure at
	// every ceiling at once reaches this; an ordinary row never does.
	if short := minNameW - (width - fixed); short > 0 && s.supplierLeadMarked(rows) {
		give := fit.leadW - leadTimeMarkFloor()
		if give > short {
			give = short
		}
		if give > 0 {
			fit.leadW -= give
			fixed -= give
		}
	}
	switch w := width - fixed; {
	case w < minNameW:
		fit.nameW = minNameW
	case w > maxNameW:
		fit.nameW = maxNameW
	default:
		fit.nameW = w
	}
	return fit
}

// itemSupplierGridRow lays one detail row out in its columns. The number, the
// cost and the lead time right-align under their headers the way a printed
// quotation sheet does, each through jdeGridFactCell so no cell can widen the
// row and a figure past its ceiling is MARKED rather than drawn as a smaller
// real number.
func itemSupplierGridRow(num, supplier, cost, lead, leadMark string, fit itemSupplierFit) string {
	cells := []string{
		jdeGridFactCell(num, fit.numW, alignRight),
		padCell(supplier, fit.nameW, alignLeft),
		jdeGridFactCell(cost, fit.costW, alignRight),
		leadTimeFactCell(lead, leadMark, fit.leadW, alignRight),
	}
	return jdeIndent + strings.TrimRight(strings.Join(cells, "  "), " ")
}

// itemSupplierCell identifies the supplier in the width the pane allows:
// "name (sku)", starred when it is the preferred one, with the NAME giving way
// when they do not all fit. That is the right sacrifice — a shortened name still
// reads, and the SKU is what the operator types into that supplier's order pad.
func itemSupplierCell(sup omsapi.ItemSupplier, w int) string {
	name := itemSupplierName(sup)
	if sup.IsPreferred {
		name = itemSupplierStar + " " + name
	}
	sku := strings.TrimSpace(sup.SupplierSKU)
	if sku == "" {
		return fitCell(name, w)
	}
	tail := " (" + sku + ")"
	if room := w - lipgloss.Width(tail); room > 0 {
		return fitCell(name, room) + tail
	}
	return fitCell(name+tail, w)
}

// supplierCostIsPack reports whether the grid's cost column is the PACKAGE cost.
// This is the item's UoM/packaging mode deciding what a cost means here: an item
// counted in base units is bought and reasoned about per unit, while one counted
// in whole packs (the two pack-counting modes of the packaging matrix, sc-sfbg)
// is reasoned about per pack. It reads the LIVE count mode rather than the saved
// one, so the column re-labels itself the moment the operator changes it.
func (s *InventoryItemFormScreen) supplierCostIsPack() bool {
	return s.countMode() != omsapi.CountModeEach
}

// supplierCostHeader names the cost column, which is the only place the choice
// above is stated — a bare "Cost" over two different meanings would need one of
// them to be guessed.
func (s *InventoryItemFormScreen) supplierCostHeader() string {
	if s.supplierCostIsPack() {
		return "Pack cost"
	}
	return "Unit cost"
}

// supplierCostCell is the cost the column promises, or an em dash when this link
// does not carry it. The other cost is never quietly substituted: a package cost
// sitting unlabelled under "Unit cost" is a wrong number, not a fallback — it is
// listed as a reading underneath instead (see supplierMetaTokens).
func (s *InventoryItemFormScreen) supplierCostCell(sup omsapi.ItemSupplier) string {
	cost := sup.UnitCost
	if s.supplierCostIsPack() {
		cost = sup.PackageCost
	}
	if money := itemSupplierMoney(cost); money != "" {
		return money
	}
	return "—"
}

// itemSupplierLeadCell is how long this supplier takes, in days. Zero is the
// field being absent rather than a real same-day promise, so it reads as unknown
// instead of as instant.
func itemSupplierLeadCell(sup omsapi.ItemSupplier) string {
	if sup.LeadTimeDays <= 0 {
		return "—"
	}
	return leadTimeDays(sup.LeadTimeDays)
}

// itemSupplierLeadMark is the provenance mark the lead cell carries — only
// beside a number it drew, since an em dash has no provenance to state.
func itemSupplierLeadMark(sup omsapi.ItemSupplier) string {
	if sup.LeadTimeDays <= 0 {
		return ""
	}
	return leadTimeMark(sup.LeadTimeSource)
}

// itemSupplierLeadText is the whole lead cell as it draws when nothing gives,
// which is what the column is measured against.
func itemSupplierLeadText(sup omsapi.ItemSupplier) string {
	if mark := itemSupplierLeadMark(sup); mark != "" {
		return itemSupplierLeadCell(sup) + " " + mark
	}
	return itemSupplierLeadCell(sup)
}

// supplierLeadMarked reports whether any lead cell in the band carries a mark,
// which is what makes the lead column able to give without dropping one.
func (s *InventoryItemFormScreen) supplierLeadMarked(rows []omsapi.ItemSupplier) bool {
	for _, sup := range rows {
		if itemSupplierLeadMark(sup) != "" {
			return true
		}
	}
	return false
}

// itemSupplierMoney renders a link's cost. Two decimals are the money convention
// everywhere else in the app (formatMoney), and that is what this shows — EXCEPT
// for the fractional cents of a small unit cost, which are the whole reason the
// column exists. A ream at $12.00 for 250 sheets is $0.048 each and the next
// supplier's is $0.0512: rounded to cents both read "$0.05", and a column that
// cannot separate the suppliers it is there to compare is worse than none.
//
// So: up to four decimals under ten dollars, trailing zeros trimmed back to the
// usual two, and plain cents above it — past ten dollars the fourth decimal
// carries nothing and would only cost the column its width.
func itemSupplierMoney(d omsapi.DecimalString) string {
	if d.Empty() {
		return ""
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(d)), 64)
	if err != nil {
		return ""
	}
	if f >= 10 || f <= -10 {
		return formatMoney(d)
	}
	out := strconv.FormatFloat(f, 'f', 4, 64)
	dot := strings.IndexByte(out, '.')
	end := len(out)
	for end > dot+3 && out[end-1] == '0' {
		end--
	}
	return "$" + out[:end]
}

// supplierMetaLines is the readings under a supplier row, WRAPPED rather than
// trimmed: they run past the pane on a narrow terminal and the piece an ellipsis
// would eat is the last one — which is where "[discontinued]" sits.
func (s *InventoryItemFormScreen) supplierMetaLines(sup omsapi.ItemSupplier) []string {
	return jdeWrapTokens(s.supplierMetaTokens(sup), s.supplierContIndent(), s.bodyWidth())
}

// supplierMetaTokens is everything about a link that did not earn a column: what
// it IS (preferred, or no longer buyable), the cost the column did not show, and
// the pack size that cost is quoted for.
func (s *InventoryItemFormScreen) supplierMetaTokens(sup omsapi.ItemSupplier) []jdeToken {
	var meta []jdeToken
	if sup.IsPreferred {
		// Spelled out as well as starred: the star is what finds the row at a
		// glance, the word is what says what the star means.
		meta = append(meta, jdeToken{text: itemSupplierStar + " primary", style: StyleStatusOK})
	}
	if sup.IsDiscontinued {
		meta = append(meta, jdeToken{text: "[discontinued]", style: StyleMuted})
	} else if !sup.IsActive {
		meta = append(meta, jdeToken{text: "[inactive]", style: StyleMuted})
	}
	other, per := sup.PackageCost, "/pkg"
	if s.supplierCostIsPack() {
		other, per = sup.UnitCost, "/unit"
	}
	if money := itemSupplierMoney(other); money != "" {
		meta = append(meta, jdeToken{text: money + per, style: StyleMuted})
	}
	// The pack size is what the package cost is quoted for; a pack of one is the
	// model default and says nothing.
	if sup.PackQuantity > 1 {
		meta = append(meta, jdeToken{text: fmt.Sprintf("pack of %d", sup.PackQuantity), style: StyleMuted})
	}
	return meta
}

// supplierURLLine is where to go buy it — the one reading long enough to want a
// line of its own. Ellipsised rather than wrapped: a URL split over two lines
// cannot be copied off the screen as one either way, and clampToBox would cut it
// at the pane edge with nothing to say it had (sc-ye0i).
func (s *InventoryItemFormScreen) supplierURLLine(sup omsapi.ItemSupplier) string {
	url := strings.Join(strings.Fields(sup.URL), " ")
	if url == "" {
		return ""
	}
	indent := s.supplierContIndent()
	if budget := s.bodyWidth(); budget > 0 {
		url = fitCell(url, budget-lipgloss.Width(indent))
	}
	if url == "" {
		return ""
	}
	return indent + StyleMuted.Render(url)
}

// ---------------------------------------------------------------------------
// The door, and what it costs to walk through it
// ---------------------------------------------------------------------------

// supplierWarnLines is the confirm the door raises when the sheet has unsaved
// edits. Ctrl-E LEAVES this screen — the item form is a raw-input screen, so the
// root never records it on the back-stack, and the supplier screen's own esc
// goes to the item detail — which means an unannounced hop would silently throw
// away everything typed since the item loaded. Esc already means "discard" here
// and says so on the bar; Ctrl-E means "open", which is not a gesture anyone
// reads as "and lose my typing", so it asks first.
func (s *InventoryItemFormScreen) supplierWarnLines() []string {
	if !s.supplierWarn {
		return nil
	}
	const note = "Unsaved edits on this sheet will be discarded. Ctrl-E again to open the supplier screen, Esc to stay here."
	width := 0
	if budget := s.bodyWidth(); budget > 0 {
		width = budget - len(jdeIndent) - 2
	}
	wrapped := jdeWrapNote(note, width)
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		lead := "⚠ "
		if i > 0 {
			lead = "  "
		}
		out = append(out, jdeIndent+StyleStatusWarn.Render(lead+line))
	}
	return out
}

// updateSupplierWarn is the confirm's own key handling, and it is MODAL: while
// the warning is up the only two keys that mean anything are the two the bar
// names. Enter would otherwise save the sheet from under a question about
// discarding it, and esc would cancel the whole form — which is the very loss
// being warned about.
func (s *InventoryItemFormScreen) updateSupplierWarn(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "ctrl+e":
		s.supplierWarn = false
		return s, s.openSuppliersCmd()
	case "esc":
		s.supplierWarn = false
		return s, nil
	}
	return s, nil
}

// openSupplierRow is what Ctrl-E does on a band row: open the supplier screen
// outright when there is nothing to lose, and ask first when there is.
func (s *InventoryItemFormScreen) openSupplierRow() tea.Cmd {
	if s.dirty() {
		s.supplierWarn = true
		return nil
	}
	return s.openSuppliersCmd()
}

// snapshotBaseline records the sheet as loaded, so dirty() can tell an operator
// who typed something from one who only walked the cursor down the form.
func (s *InventoryItemFormScreen) snapshotBaseline() { s.baseline = s.formSignature() }

// dirty reports whether anything on the sheet differs from what was loaded.
func (s *InventoryItemFormScreen) dirty() bool { return s.formSignature() != s.baseline }

// formSignature fingerprints every piece of state the operator can change. It is
// built by walking the inputs rather than listing field ids so a field added to
// the sheet later is covered by construction — the failure mode that matters is
// a change this MISSES, which would let the door discard it without asking.
//
// The packaging chain contributes its content signature and the counting level's
// POSITION rather than its client-minted row key: the key is an identity the
// editor mints, and a fingerprint should change when the chain does, not when
// the bookkeeping does.
func (s *InventoryItemFormScreen) formSignature() string {
	ptr := func(p *int) string {
		if p == nil {
			return "-"
		}
		return strconv.Itoa(*p)
	}
	var b strings.Builder
	for id := 0; id < len(s.inputs); id++ {
		if !isTextKind(id) {
			continue
		}
		b.WriteString(s.inputs[id].Value())
		b.WriteByte('\x1f')
	}
	fmt.Fprintf(&b, "%t|%t|%t|%t|%t|%t|%d|%d|%s|%s|%d|%d|%s|%s",
		s.useCaseBased, s.reorderAlerts, s.isHazardous, s.isSerialized, s.isActive, s.isRetired,
		s.shelfPos, s.serialMode, ptr(s.categoryID), ptr(s.locationID),
		s.countModeIx, packagingRowIndex(s.packRows, s.countLevelKey), chainSignature(s.packRows),
		kitSignature(s.kitRows))
	return b.String()
}
