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
// create/edit/delete + mark-replaced surface on AssetPartsScreen, so this band
// lists and Ctrl-E opens the screen that changes (sc-lvp7) — which reaches the
// same place without this sheet growing a second resource's writes.
//
// The door is Ctrl-E and nothing else. EDIT is one of the four system keys the
// reduced scheme kept (sc-h412, sc-rdrk retired every letter accelerator), enter
// is already the sheet's save, and "Ctrl-E opens whatever the highlighted row
// IS" is what the rest of this form's pickers already mean by it. Walking
// through it LEAVES a sheet that may have unsaved edits, so it asks first — see
// supplyWarnLines below for why an unannounced hop would lose them.
//
// Create mode draws no band at all: an asset that does not exist yet cannot have
// parts attached, and a permanently empty section is noise on every registration.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// Detail-grid columns. The item column takes whatever the pane has left; the
// rest are fixed so the quantities line up down the sheet.
//
// The SKU and the short id ride INSIDE the item cell rather than taking columns
// of their own: the pane is 81 columns at a 110-column terminal and 51 at an 80,
// so two fixed columns are affordable on the wide one and ruinous on the narrow
// one, while a cell that carries "name (sku · a1b2)" simply gets shorter.
const (
	assetSupplyNumW  = 2
	assetSupplyQtyW  = 3
	assetSupplyRoleW = 8
	// assetSupplyShortIDLen is how much of the item's uuid identifies it here.
	// Four hex characters is the convention this repo already reads elsewhere
	// (and git's), and it is enough to separate two rows a shared name cannot.
	assetSupplyShortIDLen = 4
	// assetSupplyMinNameW is the narrowest the name may be squeezed before the
	// SKU is dropped instead. Below about four columns a name is an ellipsis
	// with a letter in front of it, which identifies nothing — at that point the
	// cell is better spent on the id it exists to carry.
	assetSupplyMinNameW = 4
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

// supplyBandRows is how many NAVIGABLE rows the band contributes. An asset with
// no parts still gets one: it is the door to the screen that attaches the first
// one, and a band you cannot put the cursor on is a band with no door (sc-7wag).
func (s *AssetFormScreen) supplyBandRows() int {
	if !s.edit {
		return 0
	}
	if n := len(s.supplyRows()); n > 0 {
		return n
	}
	return 1
}

// rowCount is the sheet's navigable rows: its visible fields, then the band.
// Cursor movement, paging and the rebuild clamp all measure against this rather
// than len(s.fields).
func (s *AssetFormScreen) rowCount() int {
	return len(s.fields) + s.supplyBandRows()
}

// onSupplyRow reports which band row the cursor is standing on, if any. When the
// asset has no parts at all, row 0 is the empty-state placeholder. The field
// helpers already answer "not a field" for these rows (currentFieldID is bounded
// by len(s.fields)), which is what stops every field gesture from firing here.
func (s *AssetFormScreen) onSupplyRow() (int, bool) {
	idx := s.cursor - len(s.fields)
	if idx < 0 || idx >= s.supplyBandRows() {
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
	base := len(s.fields)
	focused, onSupply := s.onSupplyRow()

	l.Add("")
	l.Add(s.supplyHeading(len(rows)))

	if len(rows) == 0 {
		empty := "(no inventory items attached to this asset)"
		if onSupply {
			l.AddRow(base, jdeIndent+StyleJDEFieldFocused.Render(empty))
		} else {
			l.AddRow(base, jdeIndent+StyleMuted.Render(empty))
		}
		for _, line := range s.supplyWarnLines() {
			l.AddRow(base, line)
		}
		return
	}

	itemW := s.supplyItemWidth()
	l.Add(StyleMuted.Render(assetSupplyGridRow("#", "Item", "Qty", "Role", itemW)))
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
		l.AddRow(base+i, row)
		if onSupply && focused == i {
			for _, line := range s.supplyWarnLines() {
				l.AddRow(base+i, line)
			}
		}
		for _, meta := range s.supplyMetaLines(p) {
			l.AddRow(base+i, meta)
		}
		if note := s.supplyNoteLine(p); note != "" {
			l.AddRow(base+i, note)
		}
	}
}

// supplyHeading names the band and says it is read-only. The aside is dropped to
// a shorter reading — and then dropped entirely — rather than being allowed to
// overrun: clampToBox truncates a long row with nothing to show it did (sc-ye0i),
// and the full line is 60 columns wide against the 51 an 80-column terminal has
// (sc-7wag). It names no key either way; the bar is where keys are learned.
func (s *AssetFormScreen) supplyHeading(n int) string {
	title := fmt.Sprintf("Supplies & parts (%d)", n)
	head := StyleJDEHeading.Render(title)
	budget := s.bodyWidth()
	for _, note := range []string{"(read-only — managed on the Parts screen)", "(read-only)"} {
		if budget <= 0 || lipgloss.Width(title)+2+lipgloss.Width(note) <= budget {
			return head + "  " + StyleMuted.Render(note)
		}
	}
	return head
}

// supplyItemWidth sizes the item column from the pane: a floor so a narrow
// terminal shortens the name rather than collapsing the column, and a ceiling so
// a wide one doesn't strand the quantities out at the far right of an otherwise
// empty row. The fallback is what a 100-column terminal has.
func (s *AssetFormScreen) supplyItemWidth() int {
	// The ceiling grew by exactly what the short id costs (" · a1b2" is seven
	// columns) when sc-lvp7 put one in every cell. The cap is an aesthetic bound
	// — nothing overflows without it, since the width is computed FROM the pane —
	// so leaving it at 40 would not have clipped anything; it would have quietly
	// charged the NAME seven columns for the id, which is not a trade this bead
	// was asked to make.
	const minItemW, maxItemW = 12, 47
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
// "name (sku · a1b2)", with the NAME giving way first and then the SKU. That
// order is the ranking of what each piece is FOR (sc-lvp7). The name is what the
// operator reads, and a shortened one still reads. The SKU is what gets typed
// into a supplier's order pad. The last four of the item's uuid is the
// DISAMBIGUATOR — the thing that separates two rows whose names are the same and
// whose SKUs are blank or duplicated, which is the case Ian asked for it to
// solve — and a disambiguator that is only sometimes there disambiguates
// nothing. So it is the last piece standing: past the point where the SKU has
// already gone and the name is down to its floor, the name is what gets cut.
func supplyItemCell(p omsapi.AssetPart, w int) string {
	name := supplyItemLabel(p)
	for _, tail := range supplyItemTails(p) {
		if room := w - lipgloss.Width(tail); room >= assetSupplyMinNameW {
			return fitCell(name, room) + tail
		}
	}
	if short := assetPartShortID(p); short != "" {
		// Narrower than the name floor plus the bare id. The band's own column
		// never goes here (it floors at minItemW), but a cell that silently
		// dropped the id at some width would be a cell that cannot be trusted to
		// carry it: the id leads, so truncation eats the name instead.
		return fitCell(short+" "+name, w)
	}
	return fitCell(name, w)
}

// supplyItemTails are the parenthesised suffixes of the item cell, FULLEST
// FIRST: what the cell shows when it has the room, then what it falls back to.
// The SKU is what gives way, because it is the band's own earlier choice about
// how to use the space and the short id is the one Ian asked for.
func supplyItemTails(p omsapi.AssetPart) []string {
	sku := strings.TrimSpace(p.PartSKU)
	short := assetPartShortID(p)
	switch {
	case sku != "" && short != "":
		return []string{" (" + sku + jdeTokenSep + short + ")", " (" + short + ")"}
	case short != "":
		return []string{" (" + short + ")"}
	case sku != "":
		return []string{" (" + sku + ")"}
	}
	return nil
}

// assetPartShortID is the last four characters of the inventory item's own uuid
// — enough to tell two rows apart, short enough to ride in the item cell beside
// the name. It counts RUNES rather than bytes: a pk is hex in practice, but a
// slice taken off the end of a byte string would cut a multi-byte character in
// half if one ever appeared, and a mangled id is worse than none.
//
// An item with no pk on the wire has no short id, and the cell says nothing
// rather than showing an empty pair of parentheses.
func assetPartShortID(p omsapi.AssetPart) string {
	id := []rune(strings.TrimSpace(p.Part))
	if len(id) <= assetSupplyShortIDLen {
		return string(id)
	}
	return string(id[len(id)-assetSupplyShortIDLen:])
}

// supplyItemLabel identifies the inventory item: its name, and — when the
// payload carried none — that it has none, rather than the raw pk it used to
// fall back to. The cell now ends with the last four of that same pk, so
// printing the whole thing here would say the same thing twice and crowd out the
// SKU beside it. Only a row with no pk at all falls back to the through-row's
// own id, so a row is still never nameless.
func supplyItemLabel(p omsapi.AssetPart) string {
	if name := strings.TrimSpace(p.PartName); name != "" {
		return name
	}
	if strings.TrimSpace(p.Part) != "" {
		return "(unnamed item)"
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

// ---------------------------------------------------------------------------
// The door, and what it costs to walk through it
// ---------------------------------------------------------------------------

// openPartsCmd is the Ctrl-E door: the asset's parts, where they can actually be
// changed — added, edited, marked replaced, detached. It is the same gesture the
// rest of this sheet uses ("Ctrl-E opens whatever the highlighted row IS") rather
// than a new accelerator, since sc-rdrk retired those.
func (s *AssetFormScreen) openPartsCmd() tea.Cmd {
	if !s.edit || s.assetID == "" {
		return nil
	}
	name := ""
	if s.asset != nil {
		// The SAVED name, not the one in the Name field: an unsaved rename is not
		// what the parts screen is about to show parts for.
		name = s.asset.Name
	}
	return SwitchTo(WSAssets, NewAssetPartsScreen(s.deps, s.assetID, name))
}

// supplyWarnLines is the confirm the door raises when the sheet has unsaved
// edits. Ctrl-E LEAVES this screen — the asset form is a raw-input screen, so the
// root never records it on the back-stack, and the parts screen's own esc goes
// back past it — which means an unannounced hop would silently throw away
// everything typed since the asset loaded. Esc already means "discard" here and
// says so on the bar; Ctrl-E means "open", which is not a gesture anyone reads as
// "and lose my typing", so it asks first (sc-7wag settled this on the item
// sheet's identical door; this is the same answer, not a second one).
func (s *AssetFormScreen) supplyWarnLines() []string {
	if !s.supplyWarn {
		return nil
	}
	const note = "Unsaved edits on this sheet will be discarded. Ctrl-E again to open the parts screen, Esc to stay here."
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

// updateSupplyWarn is the confirm's own key handling, and it is MODAL: while the
// warning is up the only two keys that mean anything are the two the bar names.
// Enter would otherwise save the sheet from under a question about discarding it,
// and esc would cancel the whole form — which is the very loss being warned about.
func (s *AssetFormScreen) updateSupplyWarn(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "ctrl+e":
		s.supplyWarn = false
		return s, s.openPartsCmd()
	case "esc":
		s.supplyWarn = false
		return s, nil
	}
	return s, nil
}

// openSupplyRow is what Ctrl-E does on a band row: open the parts screen outright
// when there is nothing to lose, and ask first when there is.
func (s *AssetFormScreen) openSupplyRow() tea.Cmd {
	if s.dirty() {
		s.supplyWarn = true
		return nil
	}
	return s.openPartsCmd()
}

// snapshotBaseline records the sheet as loaded, so dirty() can tell an operator
// who typed something from one who only walked the cursor down the form.
func (s *AssetFormScreen) snapshotBaseline() { s.baseline = s.formSignature() }

// dirty reports whether anything on the sheet differs from what was loaded.
func (s *AssetFormScreen) dirty() bool { return s.formSignature() != s.baseline }

// formSignature fingerprints every piece of state the operator can change. The
// text fields are walked rather than listed by id, so a field added to the sheet
// later is covered by construction — the failure mode that matters is a change
// this MISSES, which would let the door discard it without asking.
func (s *AssetFormScreen) formSignature() string {
	iptr := func(p *int) string {
		if p == nil {
			return "-"
		}
		return strconv.Itoa(*p)
	}
	sptr := func(p *string) string {
		if p == nil {
			return "-"
		}
		return *p
	}
	var b strings.Builder
	for id := 0; id < len(s.inputs); id++ {
		if !assetIsTextKind(id) {
			continue
		}
		b.WriteString(s.inputs[id].Value())
		b.WriteByte('\x1f')
	}
	fmt.Fprintf(&b, "%t|%t|%t|%t|%t|%t|%t|%t|%t|%d|%d|%s|%s|%s|%s|%s|%v",
		s.isDonation, s.isActive, s.needsCompressedAir, s.needsVentilation,
		s.generatesHeatOrFlame, s.needsChilling, s.isChargeable, s.trainingRequired,
		s.reportOnly, s.statusIdx, s.ownershipIdx,
		sptr(s.inventoryItemID), iptr(s.categoryID), iptr(s.locationID),
		iptr(s.owningGroupID), iptr(s.owningUserID), s.certIDs)
	return b.String()
}
