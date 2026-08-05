// The asset form's SUPPLIES band — the inventory items attached to this asset
// as consumable parts (sc-hf1z).
//
// Ian's ask: "when I edit the Asset page, my expectation is to see the inventory
// items associated with that Asset on the edit screen as well." Before this the
// association was only reachable by leaving the form (the asset detail's Parts
// screen), so an operator amending an asset could not see what it consumes.
//
// It needs NO new request and NO backend change: `AssetSerializer` nests the
// through-rows as `parts` (backend/inventory/serializers.py, prefetched by
// AssetViewSet), the read struct already decodes them as `Asset.Parts`, and the
// form's edit-mode hydrate already fetches the asset detail. The band is simply
// drawn from what came back with it.
//
// Shape: a JD Edwards DETAIL GRID, the same fold po_edit.go uses for a purchase
// order's line items — a muted column header, then one dense row per part with
// its replacement clock and note indented underneath it. The rows are NAVIGABLE
// (they extend the form's cursor past its fields) for two reasons: it is what
// lets the operator scroll to the tail of a long list — the body window keeps
// the CURSOR's block on screen, so lines hanging below the last navigable row
// are unreachable — and it is what makes the band page with PgUp/PgDn like the
// rest of the sheet.
//
// READ-ONLY here, deliberately. An AssetPart is a separate API resource, not an
// Asset field: writing one from this form would either fire the moment a row was
// touched (breaking "enter saves the sheet, esc discards it") or need the web's
// staged pending-rows-chained-after-save machinery. ScanTTY already has the full
// create/edit/delete + mark-replaced surface on AssetPartsScreen, so this bead
// ships the listing and leaves add/remove-in-place to its own follow-up. Nothing
// on the bar names a key that does not work here: standing on a supply row, the
// bar offers exactly Enter/Esc/UP-DN (and paging) — there is no Ctrl-E, because
// there is nothing here to open.
//
// Create mode draws no band at all: an asset that does not exist yet cannot have
// parts attached, and a permanently empty section is noise on every registration.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Detail-grid columns. The item column takes whatever the pane has left; the
// rest are fixed so the quantities line up down the sheet.
//
// The SKU rides INSIDE the item cell rather than taking a column of its own: the
// pane is 81 columns at a 110-column terminal and 51 at an 80, so a fixed SKU
// column is affordable on the wide one and ruinous on the narrow one, while a
// cell that carries "name (sku)" simply gets shorter.
const (
	assetSupplyNumW  = 2
	assetSupplyQtyW  = 3
	assetSupplyRoleW = 8
)

// assetSupplyContIndent puts a continuation line under the item column, so it
// reads as part of the row above rather than as a row of its own.
var assetSupplyContIndent = strings.Repeat(" ", len(jdeIndent)+assetSupplyNumW+2)

// supplyRows are the parts nested on the loaded asset. Nil in create mode and
// while the fetch is still out, which is what keeps the band off both.
func (s *AssetFormScreen) supplyRows() []omsapi.AssetPart {
	if !s.edit || s.asset == nil {
		return nil
	}
	return s.asset.Parts
}

// rowCount is the sheet's navigable rows: its visible fields, then one per
// attached part. Cursor movement, paging and the rebuild clamp all measure
// against this rather than len(s.fields).
func (s *AssetFormScreen) rowCount() int {
	return len(s.fields) + len(s.supplyRows())
}

// onSupplyRow reports which part the cursor is standing on, if any. The field
// helpers already answer "not a field" for these rows (currentFieldID is bounded
// by len(s.fields)), which is what stops every field gesture from firing here.
func (s *AssetFormScreen) onSupplyRow() (int, bool) {
	idx := s.cursor - len(s.fields)
	if idx < 0 || idx >= len(s.supplyRows()) {
		return 0, false
	}
	return idx, true
}

// supplyBand appends the band to the form body. The heading says what the band
// is and that it is read-only; it names no key, because the bar is where keys
// are learned and a key named here would be another screen's.
func (s *AssetFormScreen) supplyBand(l *jdeLines) {
	if !s.edit {
		return
	}
	rows := s.supplyRows()
	l.Add("")
	l.Add(StyleJDEHeading.Render(fmt.Sprintf("Supplies & parts (%d)", len(rows))) + "  " +
		StyleMuted.Render("(read-only — managed on the Parts screen)"))
	if len(rows) == 0 {
		l.Add(jdeIndent + StyleMuted.Render("(no inventory items attached to this asset)"))
		return
	}

	itemW := s.supplyItemWidth()
	l.Add(StyleMuted.Render(assetSupplyGridRow("#", "Item", "Qty", "Role", itemW)))
	focused, onSupply := s.onSupplyRow()
	for i, p := range rows {
		row := assetSupplyGridRow(
			strconv.Itoa(i+1),
			supplyItemCell(p, itemW),
			supplyQtyCell(p),
			supplyRoleCell(p),
			itemW,
		)
		if onSupply && focused == i {
			// Picked out end to end, as a focused field row is — the whole row
			// IS the value here, so there is no input area to reverse on its own.
			row = StyleJDEFieldFocused.Render(row)
		}
		// Every line of a part shares its row, so the window can never separate
		// a clock or a note from the item it belongs to.
		l.AddRow(len(s.fields)+i, row)
		for _, meta := range s.supplyMetaLines(p) {
			l.AddRow(len(s.fields)+i, meta)
		}
		if note := s.supplyNoteLine(p); note != "" {
			l.AddRow(len(s.fields)+i, note)
		}
	}
}

// supplyItemWidth sizes the item column from the pane: a floor so a narrow
// terminal shortens the name rather than collapsing the column, and a ceiling so
// a wide one doesn't strand the quantities out at the far right of an otherwise
// empty row. The fallback is what a 100-column terminal has.
func (s *AssetFormScreen) supplyItemWidth() int {
	const minItemW, maxItemW = 12, 40
	width := 76
	if s.terminalWidth > 0 {
		width = screenBodyWidth(s.terminalWidth)
	}
	fixed := len(jdeIndent) + assetSupplyNumW + 2 + 2 + assetSupplyQtyW + 2 + assetSupplyRoleW
	switch w := width - fixed; {
	case w < minItemW:
		return minItemW
	case w > maxItemW:
		return maxItemW
	default:
		return w
	}
}

// assetSupplyGridRow lays one detail row out in its columns. The number and the
// quantity right-align under their headers the way a printed parts list does.
func assetSupplyGridRow(num, item, qty, role string, itemW int) string {
	cells := []string{
		padCell(num, assetSupplyNumW, alignRight),
		padCell(item, itemW, alignLeft),
		padCell(qty, assetSupplyQtyW, alignRight),
		role,
	}
	return jdeIndent + strings.TrimRight(strings.Join(cells, "  "), " ")
}

// supplyItemCell identifies the inventory item in the width the pane allows:
// "name (sku)", with the NAME giving way when the two do not both fit. That is
// the right sacrifice — a shortened name still reads, and the SKU is what the
// operator types into a supplier's order pad.
func supplyItemCell(p omsapi.AssetPart, w int) string {
	name := supplyItemLabel(p)
	sku := strings.TrimSpace(p.PartSKU)
	if sku == "" {
		return fitCell(name, w)
	}
	tail := " (" + sku + ")"
	if room := w - lipgloss.Width(tail); room > 0 {
		return fitCell(name, room) + tail
	}
	return fitCell(name+tail, w)
}

// supplyItemLabel identifies the inventory item: its name, falling back to the
// raw pk and then to the through-row's own id, so a row is never nameless.
func supplyItemLabel(p omsapi.AssetPart) string {
	if name := strings.TrimSpace(p.PartName); name != "" {
		return name
	}
	if p.Part != "" {
		return p.Part
	}
	return "part #" + p.IDString()
}

// supplyQtyCell is how many of the item one service takes. Zero is the field
// being absent rather than a real zero (the model floors it at 1), so it reads
// as unknown instead of as none.
func supplyQtyCell(p omsapi.AssetPart) string {
	if p.QuantityNeeded <= 0 {
		return "—"
	}
	return strconv.Itoa(p.QuantityNeeded)
}

// supplyRoleCell spells is_required out rather than drawing a flag: "required"
// and "optional" are what the web form's switch means, and a bare mark in a
// column would need a legend.
func supplyRoleCell(p omsapi.AssetPart) string {
	if p.IsRequired {
		return "required"
	}
	return "optional"
}

// supplyMetaLines is the replacement clock under a supply row, WRAPPED rather
// than trimmed: the readings run to about 75 columns with a serial recorded, the
// pane is 51 at an 80-column terminal, and the piece an ellipsis would eat is
// the last one — which is exactly where "NEEDS REPLACEMENT" sits. jdeWrapTokens
// is the shared layer's fold for that (sc-7wag lifted it out of here when the
// item sheet's supplier band needed the same thing).
func (s *AssetFormScreen) supplyMetaLines(p omsapi.AssetPart) []string {
	return jdeWrapTokens(supplyMetaTokens(p), assetSupplyContIndent, s.bodyWidth())
}

// supplyMetaTokens is the clock in words. A part with no interval is not on a
// schedule at all, so it says nothing about never having been replaced — that
// reading is only meaningful once a clock exists to have not started.
func supplyMetaTokens(p omsapi.AssetPart) []jdeToken {
	var meta []jdeToken
	if p.MaintenanceIntervalDays != nil {
		meta = append(meta, jdeToken{text: fmt.Sprintf("replace every %dd", *p.MaintenanceIntervalDays), style: StyleMuted})
	}
	switch {
	case p.LastReplacedAt != nil:
		entry := "last replaced " + p.LastReplacedAt.Format("2006-01-02")
		if p.DaysSinceReplacement != nil {
			entry += fmt.Sprintf(" (%dd ago)", *p.DaysSinceReplacement)
		}
		meta = append(meta, jdeToken{text: entry, style: StyleMuted})
	case p.MaintenanceIntervalDays != nil:
		meta = append(meta, jdeToken{text: "never replaced", style: StyleMuted})
	}
	if p.ReplacementSerialNumber != "" {
		meta = append(meta, jdeToken{text: "s/n " + p.ReplacementSerialNumber, style: StyleMuted})
	}
	if p.NeedsReplacement {
		// The one reading that is not reference material but a call to act on.
		meta = append(meta, jdeToken{text: "NEEDS REPLACEMENT", style: StyleStatusWarn})
	}
	return meta
}

// supplyNoteLine is the part's note, collapsed to ONE line — it is free text in
// the database, and a newline inside a body line would break the row accounting
// the window does. Unlike the clock above it this one is ellipsised rather than
// wrapped: prose has no natural end, and a sheet listing five parts must not
// turn into a page of it. The ellipsis says the rest is there; the Parts screen
// is where the whole note lives.
func (s *AssetFormScreen) supplyNoteLine(p omsapi.AssetPart) string {
	note := strings.Join(strings.Fields(p.Notes), " ")
	if note == "" {
		return ""
	}
	if budget := s.bodyWidth(); budget > 0 {
		note = fitCell(note, budget-len(assetSupplyContIndent))
	}
	if note == "" {
		return ""
	}
	return assetSupplyContIndent + StyleMuted.Render(note)
}
