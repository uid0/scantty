// The item form's KIT COMPONENTS editor (op-8n0) — the bill of materials of a
// kit, edited on the sheet that edits the kit itself.
//
// Why it lives HERE rather than on a screen of its own, which is the question
// the suppliers band (inventory_item_form_suppliers.go) answers the other way:
// the API decides it. A kit's components are NESTED-WRITABLE on the kit
// serializer and there is deliberately no `/kit-components/` endpoint — the
// upstream serializer says so in as many words — so the only write a component
// has is the one that saves the kit. That is exactly the packaging chain's
// situation (packaging_levels nested on the item), so this follows the packaging
// chain's shape rather than the suppliers band's:
//
//	fKitComponents  — one summary row on the sheet, opened with Ctrl-E
//	itemFormPhaseKit     — the component list, plus a trailing "(add …)" row
//	itemFormPhaseKitRow  — one component's quantity + notes, and its remove row
//	itemFormPhaseKitPick — which inventory item to add, filtered
//
// Nothing here talks to the API: the list lives in memory until the KIT is
// saved, and then rides the same PATCH as every other field (see submit()).
//
// A kit is also CREATED here now, through NewKitFormScreen below: the same sheet
// with `kit` set before it is drawn, so the band is live from the first frame
// and the save posts to /kits/ instead of /items/. This comment used to say the
// band was edit-only and that a kit was created on the web's own form, which was
// the parity gap rather than a design.
//
// What the sheet does NOT do, and why:
//
//	send supplier terms — KitSerializer.create takes an optional `supplier_terms`
//	                  block (supplier, part number, unit cost, lead time) and the
//	                  web form offers it. This sheet does not, because its
//	                  equivalent is the suppliers band, which is edit-only for a
//	                  reason of its own: an ItemSupplier is written against an
//	                  item that already exists. A kit gets its terms from its
//	                  detail screen, one step later. See NewKitFormScreen.
//	nest a kit      — kits cannot contain kits upstream, so the component picker
//	                  never offers one. /items/ already excludes kits by default,
//	                  which is what makes that free.
//
// What it USED to refuse and no longer does: a SERIALIZED component.
//
// OpenMakerSuite forbade one outright, on the grounds that receiving a kit would
// credit stock without recording serial numbers. That ban was lifted deliberately
// — KitComponent.clean() on the OMS default branch carries the argument — because
// the hazard was never unique to kits (mark-delivered has always done it to an
// ordinary serialized line) and a prohibition covering one path is worse than
// visibility covering all of them. What replaced it is `serials_outstanding`,
// which every receive path reports and which internal/tui/receive_form.go draws
// on the line it belongs to and on the summary. Receiving a kit WITH serial
// capture is a live path, and the serials go to the COMPONENTS.
//
// This editor enforced the retired rule for a release after the receiving side
// stopped believing it, which is a documented claim the code did not honour in
// the more expensive direction: an operator was refused a configuration the
// server accepts, with a reason the server had abandoned.
//
// THE LINE THAT DID NOT MOVE. A serial belongs to the COMPONENT identity that
// goes on the shelf and never to the kit's own id — a kit is bought as one SKU
// and stocked as its parts, so its own stock is permanently zero and a serial
// written against it names a unit nothing can ever draw down. This sheet's share
// of that guard is unchanged and is two things: fieldReadOnly freezes a kit's
// "Track serial numbers" row, and buildPayload ASSERTS is_serialized:false on
// every kit save (asserting, not omitting, because KitSerializer.validate falls
// back to the STORED value for an absent key). Receiving's share is that
// `serial_targets` is the only thing it consults. Neither is loosened by
// anything below, and
// TestItemFormKit_ASerializedComponentNeverSerializesTheKit is the guard.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// kitComponentRow is one editable row of the bill of materials.
//
// key is a CLIENT-side identity, minted here and never sent: the server upserts
// on `component`, so a row's identity to the API is the item it points at, but
// the editor needs something stable to address a row by while the operator is
// still choosing what that item will be.
type kitComponentRow struct {
	key       int
	component string // InventoryItem UUID
	name      string
	sku       string
	quantity  int
	notes     string
}

// Rows of the per-component editor. Quantity and notes are what Enter saves; the
// third is where "remove this component" lives, sitting with the component it
// removes — the same fold the packaging-rung editor uses now that the reduced
// key scheme has no letter accelerators left to hide a delete behind.
const (
	kitRowFieldQty = iota
	kitRowFieldNotes
	kitRowFieldRemove
	kitRowFieldCount
)

// isKit reports whether the sheet is editing or creating a kit. In EDIT mode
// only a successful /kits/ fetch says yes — the item serializer carries no
// `is_kit`, so an ordinary item and an unanswered question are indistinguishable
// from the item payload alone, and neither may grow a components row. In CREATE
// mode there is no record to ask about, so NewKitFormScreen sets the field
// itself and the answer is the operator's own choice of door.
func (s *InventoryItemFormScreen) isKit() bool { return s.kit != nil }

// NewKitFormScreen opens the item sheet in KIT-CREATE mode — the terminal half
// of the web's /inventory/kits/new.
//
// It is the SAME SCREEN as NewInventoryItemFormScreen("") with one field
// pre-decided, and that is the whole design rather than an economy. A kit is an
// InventoryItem carrying is_kit=True; every name, SKU, category, hazmat and
// packaging row on this sheet means exactly what it means for any other item,
// the bill-of-materials band already exists here (this file), and the two rows
// a kit may not carry are already frozen and already asserted at save time
// (fieldReadOnly, buildPayload). A separate kit form would be a second copy of
// forty rows in order to differ in one.
//
// `kit` is set to an EMPTY Kit rather than left nil because isKit() is what puts
// the components row on the sheet, freezes the stock and serial rows, and
// routes the save. Nothing reads a field off it in create mode: hydrateKit only
// runs for an edit, and both "this save is about to clear a stored figure"
// warnings ask s.item, which is nil until something is created.
//
// WHAT THE SHEET DOES NOT DO, and why, since the file comment above used to say
// a kit create was impossible here: it still sends no `supplier_terms`. The web
// form offers a supplier + part number + unit cost block that rides the create,
// and the terminal's equivalent is the suppliers band — which is edit-only,
// because an item-supplier link is written through its own endpoint against an
// item that must already exist (inventory_item_form_suppliers.go). So a kit is
// created here and given its purchase terms from its detail screen afterwards,
// which is one extra step and no lost capability: a kit with no supplier link
// cannot go on a purchase order, and the band is where that link is made for
// every other item in the catalogue.
func NewKitFormScreen(deps Deps) *InventoryItemFormScreen {
	s := NewInventoryItemFormScreen(deps, "")
	s.kit = &omsapi.Kit{}
	// The sheet is built before this runs, and the components row is conditional
	// on isKit(), so the field list has to be recomputed or the row the whole
	// screen exists for is absent until some other toggle rebuilds it.
	s.rebuildFields()
	return s
}

// fieldReadOnly reports whether a row is SHOWN but not the operator's to change.
//
// Two rows are, and only for a kit:
//
//	Current stock         a kit holds no stock of its own — receiving one credits
//	                      its component items and leaves the kit at zero forever
//	Track serial numbers  a kit's COMPONENTS are the units that get serials; the
//	                      kit itself is bought as one SKU and decomposes
//
// Both are the same shape of wrong. The server REFUSES either state on a kit —
// KitSerializer.validate rejects a truthy is_serialized ("A kit cannot be
// serialized; its components are stocked, not it") exactly as it rejects stock,
// and InventoryItem._clean_kit carries the same two rules at the model layer —
// so an operator CANNOT reach a serialized kit through this sheet. That is
// precisely the defect: leaving the toggle live offers an edit whose save is
// refused every single time, which is a broken screen rather than a dangerous
// one.
//
// The second, quieter leg is why the save then ASSERTS the cleared value rather
// than omitting the key (see buildPayload): validate reads
// attrs.get("is_serialized", instance.is_serialized), so a kit that already
// carries a stray true — reachable because InventoryItem.save() never calls
// full_clean() — would be unsaveable from ScanTTY for good, not just for that
// one field. Same dead end the stock row's current_stock: 0 exists to avoid.
//
// It is a screen-level question rather than part of fieldKind, which is a pure
// function of the field id: the same rows are perfectly editable on an ordinary
// item's sheet, and criterion 4 of this whole change is that an ordinary item is
// untouched.
func (s *InventoryItemFormScreen) fieldReadOnly(id int) bool {
	return (id == fCurrentStock || id == fIsSerialized) && s.isKit()
}

// readOnlyValue is what a frozen row SHOWS, read from wherever that row's state
// actually lives: a stock row keeps its reading in a textinput, a toggle in a
// bool. It exists because a frozen row of either kind renders as the same dimmed
// value row, so the render must not have to know which one it is looking at.
func (s *InventoryItemFormScreen) readOnlyValue(id int) string {
	if fieldKind(id) == kindToggle {
		return jdeYesNo(s.toggleState(id))
	}
	return s.inputs[id].Value()
}

// kitStockWarnLines is the note under a kit's Current stock row when that stock
// is NOT already zero, and it exists because the save WRITES OVER a number that
// is really there in shared data.
//
// Zeroing a figure a kit was never allowed to carry is defensible. Doing it
// invisibly, as a side effect of an operator renaming the kit, is a silent
// overwrite — OpenMakerSuite is a shared frontend, and the figure came from
// somewhere. So the sheet shows both halves before anything is pressed: what is
// recorded now, and what saving does to it.
//
// Drawn under the row rather than as a Hint because a Hint is CLIPPED: at 80
// columns the pane is 51 and the Current stock row already spends most of it, so
// clampToBox would cut the sentence mid-word (sc-ye0i). These lines WRAP, and
// they line up under the input area the way jdeNoteLines does, with the warning
// lead supplierWarnLines uses.
func (s *InventoryItemFormScreen) kitStockWarnLines(id, labelWidth int) []string {
	if id != fCurrentStock || !s.kitStockWillBeCleared() {
		return nil
	}
	return kitClearWarnLines(fmt.Sprintf(
		"Recorded as %d on hand. A kit holds no stock of its own, so saving this sheet clears that to 0.",
		s.item.Stock,
	), labelWidth, s.bodyWidth())
}

// kitRowWarnLines is the note a row grows when the save is about to CLEAR a
// stored figure that is really there — the one thing that stops the clearing
// happening invisibly. Exactly one row at a time has one, and most kits have
// none at all: both notes are silent for the ordinary values (0 on hand, not
// serialized), because there is then nothing to overwrite and nothing to say.
func (s *InventoryItemFormScreen) kitRowWarnLines(id, labelWidth int) []string {
	if lines := s.kitStockWarnLines(id, labelWidth); lines != nil {
		return lines
	}
	return s.kitSerialWarnLines(id, labelWidth)
}

// kitSerialWarnLines is the serialized row's half of that, and it exists for the
// same reason and under the same condition as the stock one: the save asserts
// is_serialized:false unconditionally for a kit, so a kit that somehow carries
// true has a stored fact written over by an operator who came here to rename it.
//
// The stray true is not hypothetical for the same reason stray stock is not:
// InventoryItem.save() never calls full_clean(), so the model's own refusal of a
// serialized kit never runs on a direct write, and a row written that way sits
// there until something saves it through the serializer.
func (s *InventoryItemFormScreen) kitSerialWarnLines(id, labelWidth int) []string {
	if id != fIsSerialized || !s.kitSerialWillBeCleared() {
		return nil
	}
	return kitClearWarnLines(
		"Recorded as serialized. A kit's components carry the serials, not the kit, "+
			"so saving this sheet clears that to No.",
		labelWidth, s.bodyWidth())
}

// kitClearWarnLines draws one of those notes: wrapped under the row, lined up
// with the input area, led by the "! " of supplierWarnLines.
//
// Wrapped rather than carried as a Hint because a Hint is CLIPPED: at 80 columns
// the pane is 51 and these rows already spend most of it, so clampToBox would
// cut the sentence mid-word with nothing to show it had (sc-ye0i).
func kitClearWarnLines(note string, labelWidth, bodyWidth int) []string {
	width := jdeStripWidth(bodyWidth, labelWidth)
	if width > 2 {
		width -= 2 // the "! " lead
	}
	wrapped := jdeWrapNote(note, width)
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		lead := "! "
		if i > 0 {
			lead = "  "
		}
		out = append(out, jdeStripIndent(labelWidth)+StyleStatusWarn.Render(lead+line))
	}
	return out
}

// kitStockWillBeCleared reports whether this sheet's save is about to write a
// stored figure down to zero — a kit whose current stock is not already 0.
//
// It reads s.item, which is what hydrate put IN the row, so the warning quotes
// the figure the operator can see rather than a second reading of the same
// record from the other endpoint. It is one predicate rather than two because
// the warning and the row's own hint have to agree about which of them is
// speaking: exactly one of them explains the frozen row at a time.
func (s *InventoryItemFormScreen) kitStockWillBeCleared() bool {
	return s.fieldReadOnly(fCurrentStock) && s.item != nil && s.item.Stock != 0
}

// kitSerialWillBeCleared is the same predicate for the serialized row: a kit
// whose STORED flag is not already false. It reads s.item rather than
// s.isSerialized for the reason above — the warning quotes what hydrate put in
// the row, so the note and the row can never disagree.
func (s *InventoryItemFormScreen) kitSerialWillBeCleared() bool {
	return s.fieldReadOnly(fIsSerialized) && s.item != nil && s.item.IsSerialized
}

// kitStockHint is the terse "why can I not type here?" for a kit's read-only
// Current stock row, and it is deliberately SHORT rather than explanatory.
//
// The row spends its width before the hint gets any: at an 80-column terminal
// the pane is 51, and jdeIndent + the 21-column label column + the leader + a
// "0" + the gutter costs 33 of it. That leaves 18, which the sentence this used
// to carry ("a kit carries no stock of its own", 33) overran by fifteen —
// clampToBox then cut the one line on the sheet that explains why the row is
// frozen, at the one width the whole screen is measured against.
//
// It goes quiet when the row is about to be CLEARED, because kitStockWarnLines
// is then drawn underneath saying the same thing at length plus what saving
// does to the figure. Two sentences about a kit holding no stock, one of them
// clipped, would bury the half that matters.
func (s *InventoryItemFormScreen) kitStockHint() string {
	if s.kitStockWillBeCleared() {
		return ""
	}
	return "kits hold no stock"
}

// kitReadOnlyHint is the terse "why can I not change this?" for whichever frozen
// row the cursor is on, and each one is measured against the width its own row
// leaves rather than written to taste — see kitStockHint for what the floor
// costs, and for why each goes quiet once the fuller warning is drawn beneath.
func (s *InventoryItemFormScreen) kitReadOnlyHint(id int) string {
	if id == fIsSerialized {
		if s.kitSerialWillBeCleared() {
			return ""
		}
		return "its components do"
	}
	return s.kitStockHint()
}

// kitErrLines is what the sheet says when "is this a kit?" could not be
// answered — a 500, a timeout, an auth failure, anything that is NOT the 404
// meaning "ordinary item".
//
// Same wording and same trigger as the item detail's renderKitErrLine, on
// purpose: two screens answering the same question differently is its own
// defect. It leads the body rather than riding a field, because it is about the
// whole sheet — every field below it is readable, and none of it is saveable
// (see submit).
func (s *InventoryItemFormScreen) kitErrLines() []string {
	if s.kitErr == "" {
		return nil
	}
	note := "Kit status unavailable: " + s.kitErr + " — this sheet cannot be saved until it is known."
	wrapped := jdeWrapNote(note, kitNoteWidth(s.bodyWidth()))
	out := make([]string, 0, len(wrapped)+1)
	for i, line := range wrapped {
		lead := "! "
		if i > 0 {
			lead = "  "
		}
		out = append(out, StyleStatusWarn.Render(lead+line))
	}
	return append(out, "")
}

// hydrateKit fills the editor from the fetched kit and snapshots what the server
// already has, so a save that never opened the editor sends no `components` key
// at all — which matters more here than it does for the packaging chain: sending
// an empty list is a validation ERROR upstream ("A kit must contain at least one
// component"), not a no-op.
func (s *InventoryItemFormScreen) hydrateKit() {
	s.kitRows = nil
	if s.kit == nil {
		s.savedKitSig = kitSignature(nil)
		return
	}
	for _, comp := range s.kit.Components {
		s.kitNextKey++
		s.kitRows = append(s.kitRows, kitComponentRow{
			key:       s.kitNextKey,
			component: comp.Component,
			name:      comp.ComponentName,
			sku:       comp.ComponentSKU,
			quantity:  comp.Quantity,
			notes:     comp.Notes,
		})
	}
	s.savedKitSig = kitSignature(s.kitRows)
}

// kitSignature fingerprints the bill of materials, so submit() can tell a sheet
// that touched it from one that only walked past the row. ORDER is part of the
// signature even though the API has no ordering of its own: reordering is not a
// gesture this editor offers, so any order change is a real edit.
func kitSignature(rows []kitComponentRow) string {
	var b strings.Builder
	for _, row := range rows {
		fmt.Fprintf(&b, "%s\x1f%d\x1f%s\x1e", row.component, row.quantity, row.notes)
	}
	return b.String()
}

// kitComponentsPayload is what the save sends, or nil to omit the key entirely.
//
// nil for an unchanged bill of materials AND for a non-kit sheet. The empty case
// is deliberately still sent when the operator emptied it: the backend's
// rejection is the correct answer to "save a kit with no components", and
// silently omitting the key would instead save every OTHER field and leave the
// components the operator just deleted in place, with nothing on screen to say
// so.
func (s *InventoryItemFormScreen) kitComponentsPayload() *[]omsapi.KitComponentWrite {
	if !s.isKit() || kitSignature(s.kitRows) == s.savedKitSig {
		return nil
	}
	out := make([]omsapi.KitComponentWrite, 0, len(s.kitRows))
	for _, row := range s.kitRows {
		out = append(out, omsapi.KitComponentWrite{
			Component: row.component,
			Quantity:  row.quantity,
			Notes:     row.notes,
		})
	}
	return &out
}

// kitFieldValue is the one-line value the fKitComponents row shows: how many
// components, then as many of them as the row has room for — as PLAIN text plus
// whether it is an empty state, so a focused row can reverse-video the whole
// field. Styled text inside would end the highlight partway through it.
//
// The COUNT leads and the names follow, which is the opposite of how the
// packaging-chain row reads, and deliberately so: this value is truncated to the
// row's width (component names are long, and clampToBox cuts a row with nothing
// to show it did — sc-ye0i), so the piece that must survive the truncation has
// to be at the front.
func (s *InventoryItemFormScreen) kitFieldValue() (string, bool) {
	if len(s.kitRows) == 0 {
		return "(none — a kit needs at least one)", true
	}
	names := make([]string, 0, len(s.kitRows))
	for _, row := range s.kitRows {
		name := strings.TrimSpace(row.name)
		if name == "" {
			name = "item " + shortKitID(row.component)
		}
		names = append(names, fmt.Sprintf("%d× %s", row.quantity, name))
	}
	return fmt.Sprintf("%d %s: %s",
		len(names), plural("component", len(names)), strings.Join(names, ", ")), false
}

// kitRowField finishes the fKitComponents row: it fits the summary to what the
// row actually has, and keeps the Ctrl-E hint only when both still fit.
//
// The HINT is what gives way, not the value, and only when it has to. A hint
// duplicates what the action bar already says — the bar is where keys are
// learned, which is the whole point of the persistent bar — while the value is
// the only place the bill of materials appears without opening anything. At an
// 80-column terminal the pane is 51 and the two together want 80, so on that
// terminal the hint goes.
//
// The label column is measured from the LABELS alone rather than from
// formFields(), which would recurse straight back into this function. That is
// exact rather than approximate: formFields builds each row's Label from
// itemFieldLabel, so both measurements are of the same strings.
func (s *InventoryItemFormScreen) kitRowField(f jdeField) jdeField {
	// The narrowest summary worth keeping — "N components: …" and a first name.
	const minValue = 20
	avail := jdeStripWidth(s.bodyWidth(), s.labelColumnWidth())
	if avail <= 0 {
		return f
	}
	if f.Hint != "" {
		if room := avail - lipgloss.Width(f.Hint) - 2; room >= minValue {
			f.Value = fitCell(f.Value, room)
			return f
		}
		f.Hint = ""
	}
	f.Value = fitCell(f.Value, avail)
	return f
}

// labelColumnWidth is the leader column the sheet's visible fields share.
func (s *InventoryItemFormScreen) labelColumnWidth() int {
	fields := make([]jdeField, 0, len(s.fields))
	for _, id := range s.fields {
		fields = append(fields, jdeField{Label: itemFieldLabel[id]})
	}
	return jdeLabelWidth(fields)
}

// shortKitID is the tail of a UUID, for a row whose item name never arrived.
// Enough to tell two rows apart without spending the whole field width on an
// identifier nobody reads.
func shortKitID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return "…" + id[len(id)-8:]
}

// ---------------------------------------------------------------------------
// The component list
// ---------------------------------------------------------------------------

// kitAddRow is the cursor position of the trailing "(add a component)" row: one
// past the last component, always present, so adding is a row you navigate to
// rather than a letter you have to know.
func (s *InventoryItemFormScreen) kitAddRow() int { return len(s.kitRows) }

func (s *InventoryItemFormScreen) onKitAddRow() bool { return s.kitCursor >= len(s.kitRows) }

func (s *InventoryItemFormScreen) openKitList() {
	s.phase = itemFormPhaseKit
	s.kitRowErr = ""
	if s.kitCursor > s.kitAddRow() {
		s.kitCursor = s.kitAddRow()
	}
	if s.kitCursor < 0 {
		s.kitCursor = 0
	}
}

func (s *InventoryItemFormScreen) closeKitList() {
	s.phase = itemFormPhaseForm
	s.kitRowErr = ""
	s.rebuildFields()
	s.syncFocus()
}

// updateKitPhase drives the component list on the reduced key scheme: Up/Down
// move, Ctrl-E opens whatever the row IS (a component's editor, or the picker
// that adds one), and Enter or Esc are both done — the list lives in memory
// until the KIT is saved, so neither writes anything and there is nothing to
// cancel.
func (s *InventoryItemFormScreen) updateKitPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter":
		s.closeKitList()
		return s, nil
	case "down", "tab":
		s.moveKitCursor(+1)
	case "up", "shift+tab":
		s.moveKitCursor(-1)
	case "pgdown":
		s.pageKitCursor(+1)
	case "pgup":
		s.pageKitCursor(-1)
	case "ctrl+e":
		if s.onKitAddRow() {
			return s, s.openKitPick()
		}
		s.openKitRow(s.kitCursor)
		return s, textinput.Blink
	}
	return s, nil
}

// moveKitCursor walks the component list, clamping at both ends and DECLINING
// on a pane the frame is not drawn into — see moveChainCursor.
func (s *InventoryItemFormScreen) moveKitCursor(delta int) {
	body := s.kitListLines()
	next, ok := s.pickRow(s.kitCursor, s.kitAddRow()+1, delta, len(s.kitListHeader()), s.kitListBar(body))
	if !ok {
		return
	}
	s.kitCursor = next
}

// pageKitCursor moves a pane's worth of rows, clamping rather than wrapping —
// the same move jdePageCursor makes on every other columnar list, and the same
// one pageChainCursor makes on the sibling list one file over.
//
// It exists because kitListBar NAMES PgUp/PgDn once the list outgrows the pane,
// and a key the bar names must do something. The step is measured off the lines
// View actually draws, so a page covers exactly what the operator can see: a
// component with a wrapped note costs more than one row, and a guessed constant
// would skip over it. The count includes the trailing add row, which is the row
// after the last component.
//
// Both gates are the LAYER's: pageRow asks the bar really DRAWN whether the
// frame is on the pane and the CEILING bar whether the list overflows it, and
// declines in silence either way.
func (s *InventoryItemFormScreen) pageKitCursor(dir int) {
	body := s.kitListLines()
	next, ok := s.pageRow(body, s.kitCursor, s.kitAddRow()+1, dir, len(s.kitListHeader()),
		s.kitListBar(body), s.kitListBarItems(jdeCeilingRows, true))
	if !ok {
		return
	}
	s.kitCursor = next
}

// viewKitList renders the bill of materials as a columnar detail grid, with the
// standing rules under it — the same guidance the serializer enforces, said
// before the save rather than after it.
func (s *InventoryItemFormScreen) viewKitList() string {
	l := s.kitListLines()
	return s.frameWithHeader(s.kitListHeader(), l, s.kitCursor,
		s.statusRow(false, "", s.kitRowErr), s.kitListBar(l))
}

// kitListHeader is the list's chrome, PINNED above the rows: the heading, what a
// kit IS, the grid's column header (or the empty state), and the standing
// warning that a kit with nothing in it cannot be saved.
//
// They used to lead the BODY, and a body line that belongs to no navigable row
// is a line no key can reach: jdeLines.Window anchors on the cursor's block and
// a columnar cursor cannot go above its first row. On a NEW item — the state
// this list opens in, where the only navigable row is the trailing "(add a
// component)" and the bar therefore names no movement key at all — the pane at
// 80x12 read `↑ 5 more above`, the add row, `↓ 2 more below`, with the heading
// and the guidance out of reach above and the "! A kit needs at least one
// component." warning out of reach below. Pinned, they are trimmed by
// jdeFitHeader, which gives ground BY RANK and claims nothing about what it
// dropped.
//
// The COLUMN HEADER takes the one essential row a header may have (jdeMinBudget)
// because an operator reading a columnar grid needs to know which column is
// which; with nothing listed there is no grid, and the row goes to the warning
// that says why the save will be refused. The heading and the guidance are
// context: an operator who has opened the kit list already knows they are in it.
//
// The per-row ERROR left the body entirely and rides the layer's STATUS ROW,
// which no budget can trim — the answer-surface rule. It was the last two lines
// of the body, so it was the first thing a short pane lost, and it is the
// screen's answer to a keypress.
// kitListGuidance is the kit list's standing guidance.
const kitListGuidance = "What one kit contains. Receiving a kit credits these items — the kit itself never carries stock."

func (s *InventoryItemFormScreen) kitListHeader() jdeHeader {
	width := s.bodyWidth()
	h := jdeHeader(nil).add(jdeHeadContext, StyleJDEHeading.Render("Kit components"))
	// FITTED, so a short pane re-draws the guidance with its cut marked rather
	// than dropping its tail — which is "the kit itself never carries stock",
	// the half of the sentence that matters (jdeHeader.addFitted).
	guide := func(rows int) []string {
		w := kitNoteWidth(width)
		lines := jdeWrapNote(kitListGuidance, w)
		if rows > 0 {
			lines = foldKeepRows(lines, rows, w)
		}
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			out = append(out, jdeIndent+StyleMuted.Render(line))
		}
		return out
	}
	h = h.addFitted(jdeHeadContext, jdeHeadContext, guide(0), guide)
	if len(s.kitRows) == 0 {
		return h.add(jdeHeadContext, "", jdeIndent+StyleMuted.Render("No components yet.")).
			add(jdeHeadEssential, jdeIndent+StyleStatusWarn.Render(
				"! A kit needs at least one component.")).
			add(jdeHeadDecorative, "")
	}
	return h.add(jdeHeadDecorative, "").
		add(jdeHeadEssential, StyleMuted.Render(kitGridRow(
			"#", "Component", "Per kit", "", kitNameWidth(width, kitNoLastCol), kitNoLastCol))).
		add(jdeHeadDecorative, "")
}

// kitListLines builds the list's body. Split out of viewKitList so paging can
// measure a page against the SAME lines View draws — the pattern the packaging
// and slot-generate lists already follow.
func (s *InventoryItemFormScreen) kitListLines() *jdeLines {
	width := s.bodyWidth()
	l := &jdeLines{}

	// ONE binding for the header and every row beneath it, so the two cannot be
	// sized differently — a ragged grid is the one thing a columnar list must
	// not be, and an invariant made structural beats a comment asking the next
	// reader to keep two copies in step.
	//
	// This grid ENDS at the per-kit quantity: there is no "On hand" column to
	// size for, and reserving one gave the name cell 27 columns at the
	// 80-column floor instead of the 34 the pane actually has spare.
	nameW := kitNameWidth(width, kitNoLastCol)

	for i, row := range s.kitRows {
		line := kitGridRow(
			strconv.Itoa(i+1),
			kitNameCell(row.name, row.sku, nameW),
			strconv.Itoa(row.quantity),
			"",
			nameW, kitNoLastCol,
		)
		if i == s.kitCursor {
			l.AddRow(i, StyleJDEFieldFocused.Render(line))
		} else {
			l.AddRow(i, line)
		}
		if note := strings.Join(strings.Fields(row.notes), " "); note != "" {
			for _, cont := range jdeWrapTokens(
				[]jdeToken{{text: note, style: StyleMuted}}, kitContIndent, width,
			) {
				l.AddRow(i, cont)
			}
		}
	}

	add := "(add a component)"
	if s.onKitAddRow() {
		l.AddRow(s.kitAddRow(), StyleSidebarItemActive.Render("  ▸ "+add))
	} else {
		l.AddRow(s.kitAddRow(), "    "+StyleMuted.Render(add))
	}

	return l
}

// kitListBar names the keys that apply where the cursor is standing. Enter and
// Esc both mean done: the list is saved nested with the KIT, so leaving it
// writes nothing either way and there is nothing to cancel.
func (s *InventoryItemFormScreen) kitListBar(body *jdeLines) []actionBarItem {
	n := s.kitAddRow() + 1
	return s.kitListBarItems(n, s.bodyPagesForBar(body, n, len(s.kitListHeader()),
		s.kitListBarItems(jdeCeilingRows, true)))
}

// kitListBarItems is kitListBar for a given paging state, so the bar that is
// MEASURED against the pane is the bar that is drawn on it.
func (s *InventoryItemFormScreen) kitListBarItems(count int, paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Done"}, {"Esc", "Done"}}
	items = append(items, jdeMoveItem("Components", count)...)
	if s.onKitAddRow() {
		items = append(items, actionBarItem{"Ctrl-E", "Add component"})
	} else {
		items = append(items, actionBarItem{"Ctrl-E", "Edit component"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// ---------------------------------------------------------------------------
// One component
// ---------------------------------------------------------------------------

// openKitRow enters the per-component editor for an existing row.
func (s *InventoryItemFormScreen) openKitRow(index int) {
	if index < 0 || index >= len(s.kitRows) {
		return
	}
	s.phase = itemFormPhaseKitRow
	s.kitRowEditing = index
	s.kitRowErr = ""
	s.kitRowFocus = kitRowFieldQty
	row := s.kitRows[index]
	s.kitRowQty.SetValue(strconv.Itoa(row.quantity))
	s.kitRowNotes.SetValue(row.notes)
	s.syncKitRowFocus()
}

func (s *InventoryItemFormScreen) syncKitRowFocus() {
	s.kitRowQty.Blur()
	s.kitRowNotes.Blur()
	switch s.kitRowFocus {
	case kitRowFieldQty:
		s.kitRowQty.Focus()
	case kitRowFieldNotes:
		s.kitRowNotes.Focus()
	}
}

// updateKitRowPhase drives the per-component editor: Up/Down move, Enter saves
// the row, Ctrl-E on the remove row drops it, Esc abandons the edit.
func (s *InventoryItemFormScreen) updateKitRowPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = itemFormPhaseKit
		s.kitRowQty.Blur()
		s.kitRowNotes.Blur()
		s.kitRowErr = ""
		return s, nil
	case "tab", "down":
		s.moveKitRowFocus(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveKitRowFocus(-1)
		return s, textinput.Blink
	case "enter":
		s.commitKitRow()
		return s, nil
	case "ctrl+e":
		if s.kitRowFocus == kitRowFieldRemove {
			// Removing is immediate: nothing is written until the kit is saved, so
			// there is nothing for a confirm to protect — and the component is on
			// screen while it is being dropped.
			index := s.kitRowEditing
			s.phase = itemFormPhaseKit
			s.kitRowQty.Blur()
			s.kitRowNotes.Blur()
			s.removeKitRow(index)
		}
		return s, nil
	}

	var cmd tea.Cmd
	switch s.kitRowFocus {
	case kitRowFieldQty:
		// Gate to digits so the quantity only ever holds a whole number; editing
		// keys (backspace/arrows) are not KeyRunes, so they pass through.
		if m.Type == tea.KeyRunes {
			for _, r := range m.Runes {
				if r < '0' || r > '9' {
					return s, nil
				}
			}
		}
		s.kitRowQty, cmd = s.kitRowQty.Update(m)
	case kitRowFieldNotes:
		s.kitRowNotes, cmd = s.kitRowNotes.Update(m)
	}
	return s, cmd
}

func (s *InventoryItemFormScreen) moveKitRowFocus(delta int) {
	_, items := s.kitRowFrame()
	next, ok := s.moveRow(s.kitRowFocus, kitRowFieldCount, delta, 0, items)
	if !ok {
		return
	}
	s.kitRowFocus = next
	s.syncKitRowFocus()
}

// commitKitRow validates and applies the edited component, then returns to the
// list. The quantity floor is the serializer's own — a component quantity of
// zero would credit nothing on receipt — checked here so the message lands
// beside the row rather than at the end of a save.
//
// The wording is held to 49 columns, which is what an error has on the status
// line at the 80-column floor: jdeScreen.statusRow draws one UNWRAPPED row, so
// anything longer is shortened by its bound (fitStatus) — and the tail here is
// the floor value, the only actionable part of the refusal. Before that bound
// moved into the layer (sc-jde-lift) this row was cut by clampToBox instead,
// losing its tail with nothing to show it had. The
// field already gates typing to digits, so "whole number" was saying something
// the operator cannot make untrue anyway.
func (s *InventoryItemFormScreen) commitKitRow() {
	qty, err := strconv.Atoi(strings.TrimSpace(s.kitRowQty.Value()))
	if err != nil || qty < 1 {
		s.kitRowErr = "quantity per kit must be at least 1"
		return
	}
	if s.kitRowEditing < 0 || s.kitRowEditing >= len(s.kitRows) {
		s.kitRowErr = "that component is no longer on the list"
		return
	}
	s.kitRows[s.kitRowEditing].quantity = qty
	s.kitRows[s.kitRowEditing].notes = strings.TrimSpace(s.kitRowNotes.Value())
	s.kitCursor = s.kitRowEditing

	s.phase = itemFormPhaseKit
	s.kitRowEditing = -1
	s.kitRowErr = ""
	s.kitRowFocus = kitRowFieldQty
	s.kitRowQty.Blur()
	s.kitRowNotes.Blur()
}

func (s *InventoryItemFormScreen) removeKitRow(index int) {
	if index < 0 || index >= len(s.kitRows) {
		return
	}
	s.kitRows = append(s.kitRows[:index:index], s.kitRows[index+1:]...)
	if s.kitCursor >= len(s.kitRows) {
		s.kitCursor = len(s.kitRows)
	}
	if s.kitCursor < 0 {
		s.kitCursor = 0
	}
	s.kitRowErr = ""
}

// kitNotesWidth sizes the notes box from the pane: the widest box that still
// leaves room for its "optional" hint, floored so a very narrow terminal gets a
// usable field rather than none, and capped so a wide one does not draw a
// hundred underscores.
func kitNotesWidth(bodyWidth int) int {
	const minW, maxW, labelBlock, hint = 10, 30, len(jdeIndent) + 9 + len(jdeLeader), 2 + 8
	if bodyWidth <= 0 {
		return maxW
	}
	switch w := bodyWidth - labelBlock - hint; {
	case w < minW:
		return minW
	case w > maxW:
		return maxW
	default:
		return w
	}
}

// viewKitRow renders the per-component editor as a columnar block. The component
// itself is a VALUE row rather than a picker: which item a row points at is its
// identity to the server (the upsert key), so changing it would be removing one
// component and adding another — which is what the list's two rows already are.
func (s *InventoryItemFormScreen) viewKitRow() string {
	l, items := s.kitRowFrame()
	return s.frame(l, s.kitRowFocus, s.statusRow(false, "", s.kitRowErr), items)
}

// kitRowFrame is the editor's body and bar, split out of viewKitRow so the
// movement arm can ask the layer whether that frame is DRAWN before it moves the
// caret — one builder, so the bar that is measured is the bar drawn.
func (s *InventoryItemFormScreen) kitRowFrame() (*jdeLines, []actionBarItem) {
	name := "(unknown)"
	sku := ""
	if s.kitRowEditing >= 0 && s.kitRowEditing < len(s.kitRows) {
		row := s.kitRows[s.kitRowEditing]
		if n := strings.TrimSpace(row.name); n != "" {
			name = n
		}
		sku = strings.TrimSpace(row.sku)
	}
	if sku != "" {
		name += " (" + sku + ")"
	}

	// The labels are terse — "Per kit", not "Quantity per kit" — because the
	// leader column is shared by all four rows and every column it takes is one
	// the input areas and hints lose. At an 80-column terminal the pane is 51,
	// which is what makes that the difference between a hint that renders and a
	// hint clampToBox cuts (sc-ye0i). The heading above says what sheet this is.
	fields := []jdeField{
		{Label: "Component", Kind: jdeValue, Value: name},
		{
			Label:   "Per kit",
			Kind:    jdeText,
			Input:   &s.kitRowQty,
			Width:   6,
			Hint:    "at least 1",
			Focused: s.kitRowFocus == kitRowFieldQty,
		},
		{
			Label:   "Notes",
			Kind:    jdeText,
			Input:   &s.kitRowNotes,
			Width:   kitNotesWidth(s.bodyWidth()),
			Hint:    "optional",
			Focused: s.kitRowFocus == kitRowFieldNotes,
		},
		{
			Label:   "Remove",
			Kind:    jdeValue,
			Value:   "drops it from the kit",
			Dim:     true,
			Focused: s.kitRowFocus == kitRowFieldRemove,
		},
	}
	// The component's own name is the one value here with no bound of its own,
	// so it is fitted to what the row has left rather than allowed to run off it.
	if width := jdeStripWidth(s.bodyWidth(), jdeLabelWidth(fields)); width > 0 {
		fields[0].Value = fitCell(fields[0].Value, width)
	}

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Edit kit component"))
	for _, line := range jdeWrapNote(
		"How many of this item one kit holds. Receiving one kit credits this many.",
		kitNoteWidth(s.bodyWidth()),
	) {
		l.Add(jdeIndent + StyleMuted.Render(line))
	}
	l.Add("")
	// The component row is not navigable, so the field cursor is offset by one:
	// row 0 IS the quantity, which is what kitRowFieldQty names.
	labelWidth := jdeLabelWidth(fields)
	l.AddFittedField(jdeNoRow, fields[0], labelWidth, s.bodyWidth())
	l.AddFittedFields(fields[1:], labelWidth, s.bodyWidth(), kitRowFieldQty)

	items := []actionBarItem{{"Enter", "Save component"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if s.kitRowFocus == kitRowFieldRemove {
		items = append(items, actionBarItem{"Ctrl-E", "Remove"})
	}
	return l, items
}

// ---------------------------------------------------------------------------
// Picking the component to add
// ---------------------------------------------------------------------------

// itemFormKitItemsMsg carries the item catalogue the component picker chooses
// from, fetched the first time the picker is opened.
type itemFormKitItemsMsg struct {
	items []omsapi.Item
	err   error
}

// openKitPick enters the component picker, fetching the catalogue the first time
// it is needed. Loading it lazily keeps the cost off every item edit — the vast
// majority of which are not kits and never open this at all.
func (s *InventoryItemFormScreen) openKitPick() tea.Cmd {
	s.phase = itemFormPhaseKitPick
	s.pickSearch.SetValue("")
	s.pickSearch.Focus()
	s.kitPickCursor = 0
	s.applyKitPickFilter()
	if s.kitItems != nil || s.kitItemsLoading {
		return textinput.Blink
	}
	s.kitItemsLoading = true
	deps, ctx := s.deps, s.ctx()
	return tea.Batch(textinput.Blink, func() tea.Msg {
		items, err := deps.OMS.ListAllItems(ctx)
		return itemFormKitItemsMsg{items: items, err: err}
	})
}

// kitPickOption is one row of the component picker.
//
// It is a struct of one field rather than a bare omsapi.Item because it used to
// carry a second — the reason a row could not be picked — and every option this
// picker offers is now pickable, so there is no such reason left to carry. The
// shape is kept because the LIST is what the filter, the label and the commit
// all address, and a named row type is where a per-row fact would go if the
// server ever grows one again.
type kitPickOption struct {
	item omsapi.Item
}

// applyKitPickFilter rebuilds the visible option list from the filter box.
//
// The exclusions are the serializer's own rules, applied here so an impossible
// pick is visible as impossible rather than surfacing as a 400 after the whole
// kit save. They are DERIVED from what OMS still refuses rather than kept as a
// list somebody remembers to prune, and today that is three rules covered by two
// drops and one absence:
//
//	the kit itself      — "A kit cannot contain itself" (KitSerializer.validate,
//	                      and a DB CheckConstraint under it). Dropped by id.
//	an item already on  — "'X' is listed more than once — combine them into a
//	the list              single row with a higher quantity". Dropped, because
//	                      the row that already lists it is where its quantity is
//	                      edited.
//	another kit         — "kits cannot contain kits". Never listed at all:
//	                      /api/inventory/items/ excludes kits from its queryset
//	                      and ListAllItems does not send include_kits, so the
//	                      absence costs this filter nothing.
//
// Both survivors are DROPS, and there is no longer any dimmed row: a SERIALIZED
// item used to be shown-and-refused here and is now an ordinary option (see the
// file comment). Nothing is left that the picker can offer and the save would
// reject, which is why kitPickOption no longer carries a reason and the list no
// longer dims anything.
func (s *InventoryItemFormScreen) applyKitPickFilter() {
	// The answer on screen is about the LIST — why there was nothing to add —
	// so it dies with the list it was about: reopening the picker, typing a
	// filter, or the catalogue landing rebuilds the options, and a "no match"
	// standing over a list that now has matches answers a keypress nobody made.
	// Every path that rebuilds the options comes through here.
	s.kitPickErr = ""
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	listed := map[string]bool{}
	for _, row := range s.kitRows {
		listed[row.component] = true
	}

	opts := make([]kitPickOption, 0, len(s.kitItems))
	for _, it := range s.kitItems {
		if it.ID == s.itemID || listed[it.ID] {
			continue
		}
		label := it.Name
		if it.SKU != "" {
			label += " " + it.SKU
		}
		if q != "" && !strings.Contains(strings.ToLower(label), q) {
			continue
		}
		opts = append(opts, kitPickOption{item: it})
	}
	s.kitPickOptions = opts
	if s.kitPickCursor >= len(s.kitPickOptions) {
		s.kitPickCursor = 0
	}
}

func (s *InventoryItemFormScreen) updateKitPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closeKitPick()
	case jdePickCommit:
		s.commitKitPick()
	case jdePickMove:
		s.moveKitPick(delta)
	case jdePickPage:
		s.pageKitPick(delta)
	default:
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyKitPickFilter()
		return s, cmd
	}
	return s, nil
}

// moveKitPick steps the highlight one row, clamping at both ends.
func (s *InventoryItemFormScreen) moveKitPick(delta int) {
	header, body := s.kitPickView()
	next, ok := s.pickRow(s.kitPickCursor, len(s.kitPickOptions), delta, len(header), s.kitPickBar(header, body))
	if !ok {
		return
	}
	s.toKitPick(next)
}

// pageKitPick steps it a pane's worth, through the layer's gate: PgUp/PgDn move
// exactly when kitPickBar names them and the frame is on the pane.
func (s *InventoryItemFormScreen) pageKitPick(dir int) {
	header, body := s.kitPickView()
	next, ok := s.pageRow(body, s.kitPickCursor, len(s.kitPickOptions), dir, len(header),
		s.kitPickBar(header, body), jdePickBarCeiling("Add", "Cancel"))
	if !ok {
		return
	}
	s.toKitPick(next)
}

// toKitPick is the ONE place the kit picker's highlight lands, and the answer on
// screen dies with it.
//
// The clear lives HERE, where the cursor arrives, so every movement path —
// down/tab, up/shift+tab, pgup/pgdown, and any added later — is covered by
// construction rather than by remembering to add each one. It has already been
// worth exactly that: routing the PAGE through the layer's pageRow took it
// around a clear that used to live inside moveKitPick, and a stale message
// survived pgup on the one screen in the program that carries this rule.
//
// It no longer FIRES, and that is a fact about the answer rather than about the
// rule. While the picker could refuse one option the message was about "that
// item", which silently re-points at whatever the cursor moved to, so a refusal
// left standing described — and defamed — a perfectly pickable row. The only
// message left is about the LIST being empty (kitPickEmptyNote), and pickRow
// declines with a count of zero, so no cursor can move out from under one. The
// clear stays because this is the single landing site: a per-row answer added
// later inherits the rule by arriving here instead of by being remembered.
func (s *InventoryItemFormScreen) toKitPick(next int) {
	s.kitPickErr = ""
	s.kitPickCursor = next
}

func (s *InventoryItemFormScreen) closeKitPick() {
	s.phase = itemFormPhaseKit
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
}

// commitKitPick appends the highlighted item as a new component, quantity 1, and
// opens its editor — because "1" is a guess and the quantity is the whole point
// of the row, so the operator lands on the field that needs their answer.
//
// EVERY option commits. It used to refuse a serialized one, which the server has
// not refused since the ban was lifted (see the file comment); what is left is
// the one refusal a picker cannot avoid, which is having nothing to pick, and it
// ANSWERS rather than returning in silence. Silence there was the reported-hang
// shape: with the catalogue still in flight, or a filter that matched nothing,
// Enter redrew a byte-for-byte identical pane — and this phase is reached with a
// scanner in hand, which fires a burst and an Enter at whatever is on screen.
//
// The out-of-range half of the guard cannot be reached — every path that moves
// the cursor clamps (pickRow, pageRow) and every path that rebuilds the options
// resets it (applyKitPickFilter) — and shares the answer because the answer is
// about the LIST, which makes it true there too.
func (s *InventoryItemFormScreen) commitKitPick() {
	if s.kitPickCursor < 0 || s.kitPickCursor >= len(s.kitPickOptions) {
		s.kitPickErr = s.kitPickEmptyNote()
		return
	}
	opt := s.kitPickOptions[s.kitPickCursor]
	s.kitNextKey++
	s.kitRows = append(s.kitRows, kitComponentRow{
		key:       s.kitNextKey,
		component: opt.item.ID,
		name:      opt.item.Name,
		sku:       opt.item.SKU,
		quantity:  1,
	})
	s.kitPickErr = ""
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.kitCursor = len(s.kitRows) - 1
	s.openKitRow(s.kitCursor)
}

func (s *InventoryItemFormScreen) kitPickView() (jdeHeader, *jdeLines) {
	note := s.kitPickNote()
	empty := "(no matching items)"
	switch {
	case s.kitItemsLoading:
		empty = "(loading items…)"
	case s.kitItemsErr != "":
		empty = "(items unavailable: " + s.kitItemsErr + ")"
	}
	// "for <kit>" is dropped rather than allowed to overrun: the title alone
	// says what is being picked, and the kit's name is on the sheet underneath.
	const title = "Add kit component"
	forKit := strings.TrimSpace(s.inputs[fName].Value())
	if width := s.bodyWidth(); width > 0 {
		if room := width - lipgloss.Width(title) - len("  for "); room > 0 {
			forKit = fitCell(forKit, room)
		} else {
			forKit = ""
		}
	}
	// Option rows are indented four columns by the picker, and the SELECTED one is
	// drawn through a style that pads one column either side — so six is what a
	// row has to give back before being fitted to the pane. Sizing to the widest
	// case keeps the list from reflowing as the cursor moves down it.
	labelWidth := 0
	if width := s.bodyWidth(); width > 0 {
		labelWidth = width - 6
	}
	return jdePickList{
		Title:  title,
		For:    forKit,
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.kitPickOptions),
		Label:  func(i int) string { return kitPickLabel(s.kitPickOptions[i], labelWidth) },
		// Nothing is dimmed: jdePickList.Dim marks a row that is an empty state
		// rather than a value, and every row here is a value now that no option
		// is shown-and-refused.
		Cursor: s.kitPickCursor,
		Empty:  empty,
	}.render(s.bodyWidth())
}

// kitPickSerialFlag / kitPickNoFlag are the picker's FLAG COLUMN: two cells in
// front of every row, carrying an "S" on a serialized item and blank on anything
// else.
//
// WHY THE READING SURVIVED THE REFUSAL. Which items are serialized is something
// the operator could see before the ban was lifted, because it was the reason the
// row could not be picked. Lifting the ban must not take the FACT away with the
// prohibition — a serialized component is the one that opens a serial-capture
// step when the kit is received, and an operator choosing between two equivalent
// parts is entitled to know which of them signs them up for that.
//
// WHY TWO CELLS AND NOT A PHRASE. At the 80-column floor a picker row has 45
// cells and a realistic MRO name spends all of them; the reason this used to
// carry ran to 47 on its own. Anything that competes with the item's IDENTITY at
// that width is the wrong trade, so this is the narrowest thing that can carry
// the fact: one glyph and the space that separates it.
//
// WHY IT LEADS. fitCell clips a row from the RIGHT, so a marker written after the
// name is the first thing a long name eats — exactly the width at which the fact
// matters most — while one written in front of it survives by construction. Same
// rule as the void prompt's headline: whatever must survive must lead. The blank
// gutter is the same two cells, so the names still line up down the list, which
// is what makes a flag column readable at a glance rather than something the eye
// has to hunt for.
//
// It is UNSTYLED on purpose: the cursor row is rendered through
// StyleSidebarItemActive as one run, so a styled flag would be overridden on the
// one row the operator is looking hardest at, and a mark that changes colour when
// selected reads as a different mark.
const (
	kitPickSerialFlag = "S "
	kitPickNoFlag     = "  "
)

// kitPickLabel is one picker row: the serialized flag, what the item is, and how
// much of it is on the shelf. ONE shape for every row, because every row is now
// pickable — the second shape it used to have carried the reason a serialized
// item could not be added, and there is no such reason left to draw.
//
// width is what the row has (0 = unknown, do not truncate). The label is fitted
// rather than left to run: a picker list is drawn straight into the pane, and a
// row cut by clampToBox loses its tail with nothing to say it had.
func kitPickLabel(opt kitPickOption, width int) string {
	flag := kitPickNoFlag
	if opt.item.IsSerialized {
		flag = kitPickSerialFlag
	}
	label := opt.item.Name
	if sku := strings.TrimSpace(opt.item.SKU); sku != "" {
		label += " (" + sku + ")"
	}
	label = fmt.Sprintf("%s%s · %d on hand", flag, label, opt.item.Stock)
	if width > 0 {
		label = fitCell(label, width)
	}
	return label
}

// kitPickNote is the muted line under the picker's title, and it says one thing
// or the other because it cannot say both.
//
// jdePickList draws Note as ONE unwrapped header row, and at the 80-column floor
// the pane is 51 with jdeIndent taking two of it — which the standing wording,
// at 49 cells, spends to the last column. So a legend for the serialized flag is
// not free: it is paid for out of the kits sentence, and the clause that gives is
// the CONSEQUENCE ("so kits are not listed"), because the rule above it implies
// the absence while the absence does not imply the rule.
//
// It is worth paying only where there is a flag on screen to explain, so the
// question is asked of the DRAWN options rather than of the catalogue: filtering
// every serialized row away takes the legend with it, and a list with nothing
// flagged reads exactly as it did before the flag existed. A legend for a mark
// that is not on the pane explains nothing and spends a header row saying so.
func (s *InventoryItemFormScreen) kitPickNote() string {
	for _, opt := range s.kitPickOptions {
		if opt.item.IsSerialized {
			return kitPickNoteFlagged
		}
	}
	return kitPickNoteBare
}

const (
	kitPickNoteBare    = "Kits cannot contain kits, so kits are not listed."
	kitPickNoteFlagged = "S = serialized. Kits cannot contain kits."
)

// kitPickBar is the component picker's bar, with PgUp/PgDn on it exactly when
// the list moves under the bar about to be drawn — measured against the bar
// WITH the pair on it, because the tallest bar is the fixed point.
func (s *InventoryItemFormScreen) kitPickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Add", len(s.kitPickOptions), s.bodyPagesForBar(body, len(s.kitPickOptions), len(header), jdePickBarCeiling("Add", "Cancel")))
}

func (s *InventoryItemFormScreen) viewKitPick() string {
	header, body := s.kitPickView()
	return s.frameWithHeader(header, body, s.kitPickCursor,
		s.kitPickStatus(), s.kitPickBar(header, body))
}

// kitPickStatus is the picker's status row: what is in flight, AND what the last
// keypress did.
//
// Both, on the one row, because they cannot be separated here. The layer's
// status row is the only surface a short pane cannot trim, and this picker pins
// its FILTER BOX on the header's single essential row (jdePickList marks it
// jdeHeadEssential, after a box written into a trimmable row took every rune the
// operator typed off the pane) — so an answer put in the header is gone at
// exactly the heights it is most needed. That is AGENTS.md's answer-surface
// rule, and the picker is the shape it was written for.
//
// The row used to be `statusRow(loading, verb, err)`, whose `saving` branch wins
// outright: Enter over a catalogue still in flight set the answer, the working
// line drew instead of it, and the pane came back byte for byte — the reported
// hang, inside the row that exists to prevent it.
//
// So the ANSWER LEADS and the WORK KEEPS THE ROOM, through poLeadOnto, which is
// this package's ONE implementation of that reservation: the lead's opening
// clause is capped at half the row, so the subject cannot be crowded out however
// the answer is later reworded. Nothing is lost by leading here — the loading
// answer is the bare "nothing added", because the subject beside it is already
// the reason.
func (s *InventoryItemFormScreen) kitPickStatus() string {
	if !s.kitItemsLoading {
		return s.statusRow(false, "", s.kitPickErr)
	}
	verb := kitPickLoadingVerb
	switch room := s.bodyWidth(); {
	case s.kitPickErr == "":
	case room > 0:
		verb = poLeadOnto(s.kitPickErr, verb, room)
	default:
		// An unsized pane means "do not truncate" everywhere in this layer, and
		// fitStatus leaves the row alone there too — but poLeadOnto would read a
		// room of 0 as no room at all and drop the subject entirely.
		verb = s.kitPickErr + poLeadJoint + verb
	}
	return s.statusRow(true, verb, "")
}

// kitPickEmptyNote is what Enter answers when there is nothing under the cursor
// to add. It is the ONLY refusal this picker has left, and until it existed the
// key returned a byte-for-byte identical pane — the reported-hang shape rule 1
// forbids, on the phase an operator reaches with a scanner in their hand.
//
// FOUR answers, because they are four different facts and the remedy differs:
// the catalogue has not arrived yet, it did not arrive, the filter excluded
// everything, or there is genuinely nothing left this kit can add. The first two
// are COULD NOT TELL and the last two are FOUND NOTHING, which is the one
// distinction a picker must never collapse (AGENTS.md).
//
// The remedies: wait, retry by leaving and reopening (openKitPick refetches
// whenever the catalogue is nil, which a failed load leaves it), clear the
// filter, or Esc — which the bar names on every frame.
//
// Each names what to do about it, because a refusal the operator cannot act on
// is a dead end. The loading one is the exception that proves it: its remedy is
// to wait, and the subject it shares the row with says what for, so it is the
// bare lead rather than a sentence restating the working line beside it.
//
// The wordings lead with the load-bearing clause and let the circumstance be
// what a cut takes, the shape every local refusal on the converted sheets uses —
// the status row cannot fold, so at the 80-column floor an error has
// screenBodyWidth(80) less its mark. That bound is checked by DRIVING the four
// states through the real screen at every width
// (TestItemFormKit_AnEmptyPickerAnswersEnter reads the drawn row and fails a
// clipped one) rather than by measuring a roster of the constants: a roster is a
// list somebody has to keep in step, and a fifth answer would be added to the
// switch below and not to it.
const (
	// kitPickNothingAdded is the lead every one of them opens with: what the key
	// DID. It is the whole answer while a load is in flight, because the working
	// line beside it already says why — see kitPickStatus.
	kitPickNothingAdded  = "nothing added"
	kitPickLoadingNote   = kitPickNothingAdded
	kitPickUnloadedNote  = kitPickNothingAdded + ": Esc then Ctrl-E retries the load"
	kitPickNoMatchNote   = kitPickNothingAdded + ": no match — clear the filter"
	kitPickNoOptionsNote = kitPickNothingAdded + ": nothing here is left to add"
)

// kitPickLoadingVerb is what the picker's status row says while the catalogue is
// being fetched. It names the work and its subject rather than saying "Loading…",
// and it is SHORT because it may have to share the row with the answer above.
const kitPickLoadingVerb = "Loading items…"

// kitPickEmptyNote picks the one that describes why there is nothing to add.
//
// The order is the order the facts REFUTE each other in: a list that is still
// loading says nothing about a filter, and a filter says nothing about a load
// that failed. It answers for the LIST rather than for a row, which is what lets
// commitKitPick use it for its out-of-range guard too — a state no path can
// reach, since every path that moves the cursor clamps and every path that
// rebuilds the options resets it, and one the wording is true of anyway.
func (s *InventoryItemFormScreen) kitPickEmptyNote() string {
	switch {
	case s.kitItemsLoading:
		return kitPickLoadingNote
	case s.kitItemsErr != "":
		return kitPickUnloadedNote
	case strings.TrimSpace(s.pickSearch.Value()) != "":
		return kitPickNoMatchNote
	default:
		return kitPickNoOptionsNote
	}
}
