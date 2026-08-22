// The item detail's KIT sections (op-8n0) — what a kit is made of, and which
// kits an ordinary item arrives in.
//
// A kit is an inventory item that is BOUGHT as one supplier SKU and DECOMPOSES
// on receipt: receiving one credits its component items and leaves the kit's own
// stock at zero forever. That is the single fact an operator standing at this
// screen has to be told, because every stock reading on the page is otherwise a
// lie by omission — a kit reads "Current stock: 0" whether five of them are on
// the shelf or none ever were.
//
// Two sections, in opposite directions, and an item is only ever in one of them:
//
//	Kit contents      — this item IS a kit: the bill of materials, one row per
//	                    component, with the per-kit quantity that is what
//	                    receiving multiplies.
//	Supplied by kits  — this item is a COMPONENT of one or more kits: a way to
//	                    buy it, which is reorder-triage context the web shows on
//	                    the same screen ("show, don't act" — nothing here decides
//	                    to order anything).
//
// Both are drawn as JD Edwards DETAIL GRIDS — the fold po_edit.go uses for a
// purchase order's lines and inventory_item_form_suppliers.go for an item's
// suppliers: a muted column header, one dense row per record, and the readings
// that did not earn a column indented underneath. The columns are sized from the
// pane, because this body is CLIPPED and not wrapped (layout.go's clampToBox
// cuts an over-wide line with nothing to show it did — sc-ye0i), and 80 columns
// leaves the pane 51 of them.
//
// Where the data comes from is not obvious and is worth stating here: the item
// serializer does NOT carry `is_kit`, so "is this a kit?" is answered by
// fetching the id from /api/inventory/kits/ and reading the status — a 404 IS
// the answer "no". See internal/omsapi/kits.go, which owns that contract.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Detail-grid columns. The name column takes whatever the pane has left; the
// quantity columns are fixed so the per-kit figures line up down the sheet —
// reading them against each other is the whole reason to list a bill of
// materials rather than a sentence.
//
// The SKU rides INSIDE the name cell rather than taking a column of its own, for
// the reason itemSupplierCell gives: a fixed SKU column is affordable at 120
// columns and ruinous at 80, while a cell that carries "name (sku)" simply gets
// shorter.
const (
	kitNumW    = 2
	kitQtyW    = 7 // fits the "Per kit" header
	kitStockW  = 7 // fits the "On hand" header
	kitMinName = 12
	kitMaxName = 44
)

// kitContIndent puts a continuation line under the name column, so it reads as
// part of the row above rather than as a row of its own.
var kitContIndent = strings.Repeat(" ", len(jdeIndent)+kitNumW+2)

// kitNameWidth sizes the name column from the pane: a floor so a narrow terminal
// shortens the name rather than collapsing the column, and a ceiling so a wide
// one doesn't strand the quantities out at the far right of an otherwise empty
// row. The fallback is what a 100-column terminal has.
func kitNameWidth(bodyWidth int) int {
	if bodyWidth <= 0 {
		bodyWidth = 71
	}
	fixed := len(jdeIndent) + kitNumW + 2 + 2 + kitQtyW + 2 + kitStockW
	switch w := bodyWidth - fixed; {
	case w < kitMinName:
		return kitMinName
	case w > kitMaxName:
		return kitMaxName
	default:
		return w
	}
}

// kitGridRow lays one detail row out in its columns. The number and both
// quantities right-align under their headers the way a printed parts list does.
//
// Every cell is FITTED to its column before it is padded, because padCell pads
// and never truncates: a value wider than its column would otherwise push the
// row past the pane and be eaten whole by clampToBox, which cuts with nothing
// to show it did (sc-ye0i). A four-figure price is the case that bites — "$1000.00"
// is 8 columns against a 7-column cell, and a silently halved price reads as a
// plausible one. Elided, it reads as cut.
func kitGridRow(num, name, qty, stock string, nameW int) string {
	cells := []string{
		padCell(fitCell(num, kitNumW), kitNumW, alignRight),
		padCell(fitCell(name, nameW), nameW, alignLeft),
		padCell(fitCell(qty, kitQtyW), kitQtyW, alignRight),
		padCell(fitCell(stock, kitStockW), kitStockW, alignRight),
	}
	return jdeIndent + strings.TrimRight(strings.Join(cells, "  "), " ")
}

// kitNameCell identifies a component in the width the pane allows: "name (sku)",
// with the NAME giving way when they do not both fit. That is the right
// sacrifice — a shortened name still reads, and the SKU is what the operator
// finds the part by on the shelf.
func kitNameCell(name, sku string, w int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "(unnamed)"
	}
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return fitCell(name, w)
	}
	tail := " (" + sku + ")"
	if room := w - lipgloss.Width(tail); room > 0 {
		return fitCell(name, room) + tail
	}
	return fitCell(name+tail, w)
}

// kitHeading names a section and, when the pane can afford it, says what the
// section is FOR. The aside is dropped to a shorter reading — and then dropped
// entirely — rather than being allowed to overrun: this body is clipped, not
// wrapped, so an over-wide heading is silently cut (sc-ye0i / sc-xxpa), and at
// 80 columns the pane is 51.
func kitHeading(title string, asides []string, bodyWidth int) string {
	head := StyleTitle.Render(title)
	for _, note := range asides {
		if bodyWidth <= 0 || lipgloss.Width(title)+2+lipgloss.Width(note) <= bodyWidth {
			return head + "  " + StyleMuted.Render(note)
		}
	}
	return head
}

// bodyWidth is the columns this screen's body has, or 0 before the first
// WindowSizeMsg — which every width-aware helper in the JDE layer reads as "do
// not truncate", since there is no budget to truncate against yet.
func (s *InventoryDetailScreen) bodyWidth() int {
	if s.terminalWidth <= 0 {
		return 0
	}
	return screenBodyWidth(s.terminalWidth)
}

// isKit reports whether the loaded item is a kit. Only a SUCCESSFUL /kits/ fetch
// says yes: a 404 means "ordinary item" and anything else means the question was
// never answered, and neither may put a kit affordance on the screen.
func (s *InventoryDetailScreen) isKit() bool { return s.kit != nil }

// renderKitSection is the bill of materials — what one kit holds, and therefore
// what receiving one credits. Drawn only for a kit, so an ordinary item's detail
// is byte-identical to what it was before kits existed.
//
// Returned with a trailing blank line, matching its sibling sections.
func (s *InventoryDetailScreen) renderKitSection() string {
	if !s.isKit() {
		return ""
	}
	kit := s.kit
	width := s.bodyWidth()
	rows := kit.Components

	var b strings.Builder
	b.WriteString(kitHeading(
		fmt.Sprintf("Kit contents (%d)", kitComponentCount(kit)),
		[]string{"(quantities are per kit)", "(per kit)"},
		width,
	) + "\n")

	// The standing note, wrapped rather than clipped: it is the one thing on this
	// screen that explains why every stock reading above it reads zero, so losing
	// its tail to the pane edge would lose the point of it.
	for _, line := range jdeWrapNote(
		"A kit is bought as one SKU and holds no stock of its own — receiving one credits the component items below.",
		kitNoteWidth(width),
	) {
		b.WriteString(jdeIndent + StyleMuted.Render(line) + "\n")
	}

	if len(rows) == 0 {
		// The API refuses to save a kit with no components, so an empty bill of
		// materials means a legacy row or a mid-edit state — worth saying plainly
		// rather than drawing an empty grid the operator has to interpret.
		// WRAPPED, not clipped: at 80 columns the pane is 51 and this sentence is
		// 69, and the half that would be cut is the half that says what it costs.
		for i, line := range jdeWrapNote(
			"This kit lists no components — receiving it would credit nothing.",
			kitNoteWidth(width)-2,
		) {
			lead := "! "
			if i > 0 {
				lead = "  "
			}
			b.WriteString(jdeIndent + StyleStatusWarn.Render(lead+line) + "\n")
		}
		b.WriteString("\n")
		return b.String()
	}

	nameW := kitNameWidth(width)
	b.WriteString(StyleMuted.Render(kitGridRow("#", "Component", "Per kit", "On hand", nameW)) + "\n")
	for i, comp := range rows {
		b.WriteString(kitGridRow(
			strconv.Itoa(i+1),
			kitNameCell(comp.ComponentName, comp.ComponentSKU, nameW),
			strconv.Itoa(comp.Quantity),
			kitStockCell(comp),
			nameW,
		) + "\n")
		for _, line := range jdeWrapTokens(kitComponentTokens(comp), kitContIndent, width) {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// kitNoteWidth is what a note indented under the section heading has left.
func kitNoteWidth(bodyWidth int) int {
	if bodyWidth <= 0 {
		return 0
	}
	if w := bodyWidth - len(jdeIndent); w > 0 {
		return w
	}
	return 0
}

// kitComponentCount prefers the server's own count over len(Components): they
// agree today, but the count is the field the serializer computes and the list
// is the one a future partial payload could truncate.
func kitComponentCount(kit *omsapi.Kit) int {
	if kit.ComponentCount > 0 {
		return kit.ComponentCount
	}
	return len(kit.Components)
}

// kitStockCell is the component's own on-hand figure. A missing field reads as
// unknown rather than as an empty shelf — 0 is a real reading here, which is why
// the struct carries a pointer at all.
func kitStockCell(comp omsapi.KitComponent) string {
	if comp.ComponentStock == nil {
		return "—"
	}
	return strconv.Itoa(*comp.ComponentStock)
}

// kitComponentTokens is everything about a component row that did not earn a
// column: whether the component itself is below its reorder point (which is what
// makes buying the kit interesting), and any note the bill of materials carries.
func kitComponentTokens(comp omsapi.KitComponent) []jdeToken {
	var out []jdeToken
	if comp.ComponentNeedsReorder {
		out = append(out, jdeToken{text: "needs reorder", style: StyleStatusWarn})
	}
	if note := strings.Join(strings.Fields(comp.Notes), " "); note != "" {
		out = append(out, jdeToken{text: note, style: StyleMuted})
	}
	return out
}

// renderSuppliedByKitsSection is the other direction: the kits that CONTAIN this
// item, and how many of it each one holds.
//
// Drawn only when there is at least one, which is what keeps an ordinary item in
// an install with no kits exactly as it was. Unlike its sibling sections it does
// NOT draw a header while loading or on error: this is context, not an answer
// the operator asked for, and a permanently-empty "Supplied by kits" heading on
// every item in the catalogue would be noise on every screen.
func (s *InventoryDetailScreen) renderSuppliedByKitsSection() string {
	if len(s.suppliedByKits) == 0 {
		return ""
	}
	width := s.bodyWidth()
	rows := s.suppliedByKits

	var b strings.Builder
	b.WriteString(kitHeading(
		fmt.Sprintf("Supplied by kits (%d)", len(rows)),
		[]string{"(ways to buy this item in a bundle)", "(bundles containing it)"},
		width,
	) + "\n")

	nameW := kitNameWidth(width)
	b.WriteString(StyleMuted.Render(kitGridRow("#", "Kit", "Per kit", "Cost", nameW)) + "\n")
	for i, kit := range rows {
		b.WriteString(kitGridRow(
			strconv.Itoa(i+1),
			kitNameCell(kit.Name, kit.SKU, nameW),
			kitPerKitCell(kit),
			kitCostCell(kit),
			nameW,
		) + "\n")
		for _, line := range jdeWrapTokens(kitSummaryTokens(kit), kitContIndent, width) {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}

// kitPerKitCell is how many of THIS item one of that kit holds. Null is the
// backend saying it was not asked, which must not render as "none".
func kitPerKitCell(kit omsapi.KitSummary) string {
	if kit.QuantityInKit == nil {
		return "—"
	}
	return strconv.Itoa(*kit.QuantityInKit)
}

// kitCostCell is what the kit costs from its primary supplier, or an em dash
// when no price is recorded. Empty means null/unset, NOT zero — the distinction
// DecimalString exists to preserve.
func kitCostCell(kit omsapi.KitSummary) string {
	if kit.UnitCost.Empty() {
		return "—"
	}
	return formatMoney(kit.UnitCost)
}

// kitSummaryTokens is what a kit row carries beyond its columns: who sells it
// and under what part number (which is what the operator types into that
// supplier's order pad), and whether the kit has been retired from new orders.
func kitSummaryTokens(kit omsapi.KitSummary) []jdeToken {
	var out []jdeToken
	if name := strings.TrimSpace(kit.SupplierName); name != "" {
		if sku := strings.TrimSpace(kit.SupplierSKU); sku != "" {
			name += " " + sku
		}
		out = append(out, jdeToken{text: name, style: StyleMuted})
	}
	if kit.ComponentCount > 0 {
		out = append(out, jdeToken{
			text:  fmt.Sprintf("%d %s", kit.ComponentCount, plural("component", kit.ComponentCount)),
			style: StyleMuted,
		})
	}
	if !kit.IsActive {
		// Still worth SEEING: an inactive kit is hidden from new purchase-order
		// pickers upstream, so saying so here is what stops an operator hunting
		// for a kit that will not appear.
		out = append(out, jdeToken{text: "[inactive]", style: StyleMuted})
	}
	return out
}

// kitStockNote is what the Stock section grows for a kit: why the number above
// it is zero and always will be. Empty for every other item, which is what leaves
// an ordinary item's Stock block untouched.
//
// Wrapped rather than clipped: it is 57 columns and the pane is 51 at an
// 80-column terminal, so clampToBox would cut it mid-sentence — and this is a
// sentence whose only job is to stop a zero being read as a count.
func (s *InventoryDetailScreen) kitStockNote() string {
	if !s.isKit() {
		return ""
	}
	var b strings.Builder
	for _, line := range jdeWrapNote(
		"Kits hold no stock of their own — see Kit contents below.", s.bodyWidth(),
	) {
		b.WriteString(StyleMuted.Render(line) + "\n")
	}
	return b.String()
}

// kitNameLine is the item-detail header's name line for a KIT: the name, then
// the tag that says the record is not the thing it is named after.
//
// The tag is what must survive a narrow pane, so the NAME is what gives way —
// a shortened name still reads, while a header clipped at the pane edge loses
// the tag entirely and the screen goes back to looking like an ordinary item
// with no stock. Returns "" for a non-kit, leaving that header untouched.
//
// The budget it fits against is [kit] PLUS every tag renderHeader appends after
// it, not [kit] alone: this line is only the first half of a line that continues
// with [retired] / needs reorder / [reorder pending], so reserving room for the
// kit tag alone just moves the clip one tag along and loses THOSE instead.
func (s *InventoryDetailScreen) kitNameLine(name string) string {
	if !s.isKit() {
		return ""
	}
	const tag = "  [kit]"
	if width := s.bodyWidth(); width > 0 {
		room := width - lipgloss.Width(tag) - s.headerTagsWidth()
		// A floor rather than a hard fit: with every tag showing at once there is
		// no width left at 80 columns for a name at all, and a header whose name
		// vanished is worse than one whose last tag is clipped.
		if room < kitMinName {
			room = kitMinName
		}
		name = fitCell(name, room)
	}
	return StyleTitle.Render(name) + "  " + StyleStatusOK.Render("[kit]")
}

// headerTagsWidth is what renderHeader will append to the name line AFTER
// kitNameLine has had its say — the tags, each with its two-space gutter. It is
// the reservation kitNameLine fits the name against, and it has to be read from
// the same item flags renderHeader tests or the two disagree.
func (s *InventoryDetailScreen) headerTagsWidth() int {
	it := s.item
	if it == nil {
		return 0
	}
	w := 0
	if it.IsRetired {
		w += 2 + lipgloss.Width("[retired]")
	}
	if it.NeedsReorder {
		w += 2 + lipgloss.Width("needs reorder")
	}
	if it.HasPendingReorder {
		w += 2 + lipgloss.Width("[reorder pending]")
	}
	return w
}

// renderKitErrLine is what the screen says when the "is this a kit?" question
// could not be answered — a network failure, a 500, an auth error, anything that
// is NOT the 404 meaning "ordinary item".
//
// It is a single warned line rather than a section header because that is
// exactly what it is worth: the item's own detail loaded fine and is entirely
// usable, but one fact about it is missing, and an operator reading "Current
// stock: 0" deserves to know that the screen cannot currently rule out the
// reading being structural.
func (s *InventoryDetailScreen) renderKitErrLine() string {
	if s.kitErr == "" {
		return ""
	}
	line := "! Kit status unavailable: " + s.kitErr
	if width := s.bodyWidth(); width > 0 {
		line = fitCell(line, width)
	}
	return StyleStatusWarn.Render(line) + "\n\n"
}
