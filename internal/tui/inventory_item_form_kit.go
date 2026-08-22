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
// What the sheet does NOT do, and why:
//
//	create a kit    — a kit create needs at least one component AND its purchase
//	                  terms in the same request (KitSerializer.create), and it
//	                  posts to /kits/, not /items/. ScanTTY's item form creates
//	                  ITEMS; a kit is created on the web's own kit form. The
//	                  band is therefore edit-mode only, like the suppliers band.
//	nest a kit      — kits cannot contain kits upstream, so the component picker
//	                  never offers one. /items/ already excludes kits by default,
//	                  which is what makes that free.
//	add a serialized— receiving a kit credits stock without recording serials, so
//	   component      the serializer refuses it. The picker dims those rows and
//	                  says why rather than letting the save fail at the end.
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

// isKit reports whether the sheet is editing a kit. Only a successful /kits/
// fetch says yes — the item serializer carries no `is_kit`, so an ordinary item
// and an unanswered question are indistinguishable from the item payload alone,
// and neither may grow a components row.
func (s *InventoryItemFormScreen) isKit() bool { return s.kit != nil }

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
		if s.kitCursor < s.kitAddRow() {
			s.kitCursor++
		}
	case "up", "shift+tab":
		if s.kitCursor > 0 {
			s.kitCursor--
		}
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

// pageKitCursor moves a pane's worth of rows, clamping rather than wrapping —
// the same move jdePageCursor makes on every other columnar list.
//
// It exists because kitListBar NAMES PgUp/PgDn once the list outgrows the pane,
// and a key the bar names must do something. The step is measured off the lines
// View actually draws, so a page covers exactly what the operator can see: a
// component with a wrapped note costs more than one row, and a guessed constant
// would skip over it. The count includes the trailing add row, which is the row
// after the last component.
func (s *InventoryItemFormScreen) pageKitCursor(dir int) {
	body := s.kitListLines()
	s.kitCursor = jdePageCursor(s.kitCursor, s.kitAddRow()+1, s.windowRows(body, s.kitCursor, 0), dir)
}

// viewKitList renders the bill of materials as a columnar detail grid, with the
// standing rules under it — the same guidance the serializer enforces, said
// before the save rather than after it.
func (s *InventoryItemFormScreen) viewKitList() string {
	l := s.kitListLines()
	return s.frame(l, s.kitCursor, "", s.kitListBar(l))
}

// kitListLines builds the list's body. Split out of viewKitList so paging can
// measure a page against the SAME lines View draws — the pattern the packaging
// and slot-generate lists already follow.
func (s *InventoryItemFormScreen) kitListLines() *jdeLines {
	width := s.bodyWidth()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Kit components"))
	for _, line := range jdeWrapNote(
		"What one kit contains. Receiving a kit credits these items — the kit itself never carries stock.",
		kitNoteWidth(width),
	) {
		l.Add(jdeIndent + StyleMuted.Render(line))
	}
	l.Add("")

	if len(s.kitRows) == 0 {
		l.Add(jdeIndent + StyleMuted.Render("No components yet."))
	} else {
		nameW := kitNameWidth(width, kitNoLastCol)
		l.Add(StyleMuted.Render(kitGridRow("#", "Component", "Per kit", "", nameW, kitNoLastCol)))
	}
	for i, row := range s.kitRows {
		// This grid ENDS at the per-kit quantity: there is no "On hand" column
		// to size for, and reserving one gave the name cell 27 columns at the
		// 80-column floor instead of the 34 the pane actually has spare.
		nameW := kitNameWidth(width, kitNoLastCol)
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

	if len(s.kitRows) == 0 {
		l.Add("")
		l.Add(jdeIndent + StyleStatusWarn.Render("! A kit needs at least one component."))
	}
	if s.kitRowErr != "" {
		l.Add("")
		l.Add(jdeIndent + StyleStatusError.Render("✗ "+s.kitRowErr))
	}
	return l
}

// kitListBar names the keys that apply where the cursor is standing. Enter and
// Esc both mean done: the list is saved nested with the KIT, so leaving it
// writes nothing either way and there is nothing to cancel.
func (s *InventoryItemFormScreen) kitListBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Done"}, {"Esc", "Done"}, {"UP/DN", "Components"}}
	if s.onKitAddRow() {
		items = append(items, actionBarItem{"Ctrl-E", "Add component"})
	} else {
		items = append(items, actionBarItem{"Ctrl-E", "Edit component"})
	}
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail {
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
	n := kitRowFieldCount
	s.kitRowFocus = (s.kitRowFocus + delta + n) % n
	s.syncKitRowFocus()
}

// commitKitRow validates and applies the edited component, then returns to the
// list. The quantity floor is the serializer's own — a component quantity of
// zero would credit nothing on receipt — checked here so the message lands
// beside the row rather than at the end of a save.
func (s *InventoryItemFormScreen) commitKitRow() {
	qty, err := strconv.Atoi(strings.TrimSpace(s.kitRowQty.Value()))
	if err != nil || qty < 1 {
		s.kitRowErr = "quantity per kit must be a whole number of at least 1"
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
			Value:   jdeInputValue(s.kitRowQty, s.kitRowFocus == kitRowFieldQty),
			Width:   6,
			Hint:    "at least 1",
			Focused: s.kitRowFocus == kitRowFieldQty,
		},
		{
			Label:   "Notes",
			Kind:    jdeText,
			Value:   jdeInputValue(s.kitRowNotes, s.kitRowFocus == kitRowFieldNotes),
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
	l.Add(renderJDEField(fields[0], labelWidth))
	l.AddFields(fields[1:], labelWidth, kitRowFieldQty)

	items := []actionBarItem{{"Enter", "Save component"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if s.kitRowFocus == kitRowFieldRemove {
		items = append(items, actionBarItem{"Ctrl-E", "Remove"})
	}
	return s.frame(l, s.kitRowFocus, jdeStatusLine(false, "", s.kitRowErr), items)
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

// kitPickOption is one row of the component picker: an inventory item, and — when
// it cannot legally be a component — why not.
type kitPickOption struct {
	item omsapi.Item
	why  string // non-empty means "cannot be added", and says why
}

// applyKitPickFilter rebuilds the visible option list from the filter box.
//
// The three exclusions are the serializer's own rules, applied here so an
// impossible pick is visible as impossible rather than surfacing as a 400 after
// the whole kit save. A kit's own row and an already-listed component are
// dropped outright — neither is a choice — while a serialized item is SHOWN and
// dimmed, because "why can't I add this?" is a question an operator will
// otherwise ask the screen and get no answer to.
func (s *InventoryItemFormScreen) applyKitPickFilter() {
	// The refusal on screen names ONE option ("Serialized widget cannot be a kit
	// component…"), so it dies with the option list it was about: reopening the
	// picker or typing a filter rebuilds that list, and a message about a row
	// nobody can see any more reads as a refusal of whatever is now under the
	// cursor. Every path that rebuilds the options comes through here.
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
		opt := kitPickOption{item: it}
		if it.IsSerialized {
			opt.why = "serialized — receiving a kit records no serials"
		}
		opts = append(opts, opt)
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
		header, body := s.kitPickView()
		s.moveKitPick(delta * s.windowRows(body, s.kitPickCursor, len(header)))
	default:
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyKitPickFilter()
		return s, cmd
	}
	return s, nil
}

func (s *InventoryItemFormScreen) moveKitPick(delta int) {
	s.kitPickCursor = jdeClampPick(s.kitPickCursor+delta, len(s.kitPickOptions))
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
// An option that cannot legally be a component does NOT commit: the picker stays
// open with its reason on screen, rather than accepting a row the save would
// reject.
func (s *InventoryItemFormScreen) commitKitPick() {
	if s.kitPickCursor < 0 || s.kitPickCursor >= len(s.kitPickOptions) {
		return
	}
	opt := s.kitPickOptions[s.kitPickCursor]
	if opt.why != "" {
		s.kitPickErr = opt.item.Name + " cannot be a kit component: " + opt.why
		return
	}
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

func (s *InventoryItemFormScreen) kitPickView() ([]string, *jdeLines) {
	note := "Kits cannot contain kits, so kits are not listed."
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
		Dim:    func(i int) bool { return s.kitPickOptions[i].why != "" },
		Cursor: s.kitPickCursor,
		Empty:  empty,
	}.render()
}

// kitPickLabel is one picker row: what the item is, its stock, and — for a row
// that cannot be picked — why not, which is the reading worth the width.
//
// width is what the row has (0 = unknown, do not truncate). The label is fitted
// rather than left to run: a picker list is drawn straight into the pane, and a
// row cut by clampToBox loses its tail — which for an unpickable row is the
// REASON it cannot be picked, the only part of that row worth reading.
func kitPickLabel(opt kitPickOption, width int) string {
	label := opt.item.Name
	if sku := strings.TrimSpace(opt.item.SKU); sku != "" {
		label += " (" + sku + ")"
	}
	if opt.why != "" {
		label += " — " + opt.why
	} else {
		label = fmt.Sprintf("%s · %d on hand", label, opt.item.Stock)
	}
	if width > 0 {
		label = fitCell(label, width)
	}
	return label
}

func (s *InventoryItemFormScreen) viewKitPick() string {
	header, body := s.kitPickView()
	paging := false
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail-len(header) {
		paging = true
	}
	return s.frameWithHeader(header, body, s.kitPickCursor,
		jdeStatusLine(s.kitItemsLoading, "Loading items…", s.kitPickErr),
		jdePickBar("Add", paging))
}
