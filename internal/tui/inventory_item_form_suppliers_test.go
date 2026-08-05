// The item form's suppliers band (sc-7wag): who sells this item, for how much,
// and how long they take, listed on the edit sheet.
//
// The contract these hold it to:
//
//	it is THERE            — editing an item shows its sourcing context, which
//	                         is the whole ask
//	the cost means what     — the column follows the item's counting mode, and
//	the column says           never shows one cost under the other's heading
//	it is a grid, not a    — the sheet's leader column is computed from its
//	fields                   FIELDS alone, so a long supplier name cannot move it
//	the rows are navigable — which is what makes the tail of a long list
//	                         reachable and the band page with the sheet
//	the bar stays honest   — Ctrl-E is named on a band row and WORKS there
//	nothing is discarded   — the door asks before it walks off with unsaved edits
//	silently
package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// itemSupplierBandWidth is the terminal the columnar screens are measured at, as
// in every sweep's width test.
const itemSupplierBandWidth = 110

// itemSheetWithSuppliers is a loaded EDIT-mode item form carrying `suppliers` —
// the state the form reaches after GetItem returns, since the links ride nested
// on the item payload rather than arriving in a request of their own.
func itemSheetWithSuppliers(t *testing.T, sups []omsapi.ItemSupplier) *InventoryItemFormScreen {
	t.Helper()
	s := NewInventoryItemFormScreen(Deps{}, "itm-1")
	s.loading = false
	s.categories = []omsapi.Category{{ID: 1, Name: "Paper"}}
	s.locations = []omsapi.Location{{ID: 2, Name: "Bay 4"}}
	s.item = &omsapi.Item{ID: "itm-1", Name: "Copy paper 8.5x11", SKU: "CP-1", Suppliers: sups}
	s.hydrate()
	s.rebuildFields()
	s.syncFocus()
	s.snapshotBaseline()
	s.Update(tea.WindowSizeMsg{Width: itemSupplierBandWidth, Height: jdeSweepHeight})
	return s
}

// itemTestSuppliers is the fixture, and it is deliberately the WIDEST realistic
// one: a preferred link with every field filled in and a long URL, a supplier
// whose name is far longer than the column, and a discontinued link carrying
// only one of the two costs. A width test is only as honest as its widest
// fixture (sc-lmsi).
func itemTestSuppliers() []omsapi.ItemSupplier {
	return []omsapi.ItemSupplier{
		{
			ID: 1, Supplier: 4, SupplierName: "Acme Office Supply", SupplierSKU: "ACM-88421",
			UnitCost: "0.0480", PackageCost: "12.0000", PackQuantity: 250, LeadTimeDays: 7,
			IsPreferred: true, IsActive: true,
			URL: "https://acme.example.com/products/copy-paper-8-5x11-white-20lb",
		},
		{
			ID: 2, Supplier: 5, SupplierName: "Bolt Depot & Paper Warehouse of Greater Portland",
			SupplierSKU: "BD-1104-XL", UnitCost: "0.0512", PackageCost: "12.80",
			PackQuantity: 250, LeadTimeDays: 14.5, IsActive: true,
		},
		{
			ID: 3, Supplier: 6, SupplierName: "Old Mill Paper", SupplierSKU: "OMP-7",
			UnitCost: "0.0395", LeadTimeDays: 30, IsDiscontinued: true,
		},
	}
}

// itemBandRow puts the cursor on band row i and returns the rendered sheet.
func itemBandRow(t *testing.T, s *InventoryItemFormScreen, i int) string {
	t.Helper()
	s.cursor = len(s.fields) + i
	s.syncFocus()
	return s.View()
}

// TestItemSuppliers_TheSheetListsWhoSellsTheItem is the bead itself: the
// operator amending an item can see its sourcing context without leaving the
// screen.
func TestItemSuppliers_TheSheetListsWhoSellsTheItem(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	out := itemBandRow(t, s, 0)

	for _, want := range []string{
		"Suppliers (3)",      // the heading counts them
		"Acme Office Supply", // …and every one of them is listed
		"ACM-88421",          // the SKU is what gets typed into their order pad
		"Old Mill Paper",     // including the one nobody buys from any more
		"★ primary",          // which one is preferred, in words as well as a mark
		"[discontinued]",     // and which one is no longer buyable
		"$12.00/pkg",         // the cost the column did not show
		"pack of 250",        // …and what that cost is quoted for
		"acme.example.com",   // where to go buy it
		"Unit cost",          // the column says which cost it holds
		"Lead",               //
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the suppliers band should show %q:\n%s", want, out)
		}
	}
}

// TestItemSuppliers_FractionalCentsSurvive. Rounded to cents the three suppliers
// all read "$0.05", and a column that cannot separate the suppliers it exists to
// compare is worse than no column at all.
func TestItemSuppliers_FractionalCentsSurvive(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	out := itemBandRow(t, s, 0)

	for _, want := range []string{"$0.048", "$0.0512", "$0.0395"} {
		if !strings.Contains(out, want) {
			t.Errorf("a unit cost lost its fractional cents (%s missing):\n%s", want, out)
		}
	}
	// But a round figure is still plain money — trailing zeros are noise.
	if !strings.Contains(out, "$12.00/pkg") || strings.Contains(out, "$12.0000") {
		t.Errorf("a whole-cent cost should read as money:\n%s", out)
	}
	if got := itemSupplierMoney("1234.5000"); got != "$1234.50" {
		t.Errorf("past ten dollars the fourth decimal carries nothing: %q", got)
	}
}

// TestItemSuppliers_TheCostColumnFollowsTheCountingMode is the bead's "cost
// respecting the item's UoM/packaging mode": an item counted in base units is
// reasoned about per unit, one counted in whole packs per pack. Whichever cost
// the column does not hold is listed underneath, so nothing is lost either way.
func TestItemSuppliers_TheCostColumnFollowsTheCountingMode(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	each := itemBandRow(t, s, 0)
	if !strings.Contains(each, "Unit cost") || strings.Contains(each, "Pack cost") {
		t.Errorf("an each-counted item's column is the UNIT cost:\n%s", each)
	}
	if !strings.Contains(each, "$12.00/pkg") {
		t.Errorf("the package cost should still be readable underneath:\n%s", each)
	}

	// Counting whole packs re-labels the column, live — this is the same count
	// mode the packaging matrix section three bands up is setting.
	s.packRows = []packagingRow{{key: 1, id: 1, name: "case", baseUnits: 5000}}
	s.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	s.countLevelKey = 1
	s.rebuildFields()
	pack := itemBandRow(t, s, 0)
	if !strings.Contains(pack, "Pack cost") || strings.Contains(pack, "Unit cost") {
		t.Errorf("a pack-counted item's column is the PACKAGE cost:\n%s", pack)
	}
	if !strings.Contains(pack, "$12.00") || !strings.Contains(pack, "$0.048/unit") {
		t.Errorf("the two costs should swap places, not disappear:\n%s", pack)
	}
}

// TestItemSuppliers_AMissingCostIsNotSubstituted. The other cost sitting
// unlabelled under the column's heading is a WRONG NUMBER, not a fallback: the
// third fixture link carries no package cost, so in pack mode its column reads
// as absent and the unit cost it does have is listed as a reading instead.
func TestItemSuppliers_AMissingCostIsNotSubstituted(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	s.packRows = []packagingRow{{key: 1, id: 1, name: "case", baseUnits: 5000}}
	s.countModeIx = countModeIndex(omsapi.CountModeByLevel)
	s.countLevelKey = 1
	s.rebuildFields()

	sup := itemTestSuppliers()[2]
	if got := s.supplierCostCell(sup); got != "—" {
		t.Errorf("a link with no package cost should read as absent, got %q", got)
	}
	joined := strings.Join(s.supplierMetaLines(sup), "\n")
	if !strings.Contains(joined, "$0.0395/unit") {
		t.Errorf("the cost it DOES carry should still be shown:\n%s", joined)
	}
}

// TestItemSuppliers_CreateModeDrawsNoBand: an item that does not exist yet has
// nothing linked to it, and a permanently empty section is noise on every
// registration.
func TestItemSuppliers_CreateModeDrawsNoBand(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: itemSupplierBandWidth, Height: jdeSweepHeight})

	if got := s.rowCount(); got != len(s.fields) {
		t.Errorf("create mode has no supplier rows: rowCount %d, fields %d", got, len(s.fields))
	}
	s.cursor = len(s.fields) - 1
	s.syncFocus()
	if out := s.View(); strings.Contains(out, "Suppliers (") {
		t.Errorf("create mode should draw no suppliers band:\n%s", out)
	}
}

// TestItemSuppliers_AnItemWithNoLinksSaysSoAndKeepsTheDoor — an absent band and
// an empty one are different statements, and only one of them is true here. The
// empty state stays a NAVIGABLE row because it is the door to the screen that
// adds the first link: a band you cannot put the cursor on is a band with no
// door.
func TestItemSuppliers_AnItemWithNoLinksSaysSoAndKeepsTheDoor(t *testing.T) {
	s := itemSheetWithSuppliers(t, nil)
	if want := len(s.fields) + 1; s.rowCount() != want {
		t.Fatalf("rowCount = %d, want fields+1 = %d", s.rowCount(), want)
	}
	out := itemBandRow(t, s, 0)

	if !strings.Contains(out, "Suppliers (0)") {
		t.Errorf("an item with no links still gets the band:\n%s", out)
	}
	if !strings.Contains(out, "(no suppliers linked to this item)") {
		t.Errorf("the empty band should say what is empty:\n%s", out)
	}
	if strings.Contains(out, "Unit cost") {
		t.Errorf("a column header with no rows under it is furniture:\n%s", out)
	}
	if !strings.Contains(jdeBarLine(out), "Ctrl-E=Suppliers") {
		t.Errorf("the empty band is still the door: %q", jdeBarLine(out))
	}
	_, cmd := s.Update(namedKey("ctrl+e"))
	if sw := resolveSwitch(t, cmd); sw.Screen == nil {
		t.Errorf("Ctrl-E on the empty band should open the suppliers screen")
	}
}

// TestItemSuppliers_RowsExtendTheCursorPastTheFields. The rows are part of the
// sheet's cursor space, which is what makes them scrollable and pageable — see
// _TheLastLinkIsReachable for why that matters.
func TestItemSuppliers_RowsExtendTheCursorPastTheFields(t *testing.T) {
	sups := itemTestSuppliers()
	s := itemSheetWithSuppliers(t, sups)

	if want := len(s.fields) + len(sups); s.rowCount() != want {
		t.Fatalf("rowCount = %d, want fields+links = %d", s.rowCount(), want)
	}

	// Down from the last field lands on the first link…
	s.cursor = len(s.fields) - 1
	s.Update(namedKey("down"))
	idx, ok := s.onSupplierRow()
	if !ok || idx != 0 {
		t.Errorf("down from the last field should land on link 1, got idx=%d ok=%v (cursor %d)", idx, ok, s.cursor)
	}
	// …and no field is focused there, so a keystroke cannot land in one.
	if id, ok := s.currentFieldID(); ok {
		t.Errorf("a supplier row is not a field, but currentFieldID answered %d", id)
	}

	// Down from the last link wraps to the top of the sheet, as it does from the
	// last field when there are none.
	s.cursor = s.rowCount() - 1
	s.Update(namedKey("down"))
	if s.cursor != 0 {
		t.Errorf("cursor = %d after wrapping off the last link, want 0", s.cursor)
	}

	// A rebuild — what a conditional field appearing or disappearing triggers —
	// must not knock the cursor off the band; its clamp measures the whole sheet,
	// not just the fields. No gesture reaches rebuildFields from a band row
	// today, which is precisely why the invariant is pinned here rather than
	// assumed.
	s.cursor = s.rowCount() - 1
	s.rebuildFields()
	if want := s.rowCount() - 1; s.cursor != want {
		t.Errorf("a rebuild moved the cursor off the last link: %d, want %d", s.cursor, want)
	}
}

// TestItemSuppliers_TheLastLinkIsReachable is the reason the rows are navigable
// rather than trailing decoration: the body window keeps the CURSOR's block on
// screen, so lines hanging below the last navigable row of a sheet this long
// could never be scrolled to.
func TestItemSuppliers_TheLastLinkIsReachable(t *testing.T) {
	many := make([]omsapi.ItemSupplier, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, omsapi.ItemSupplier{
			ID: i + 1, Supplier: i + 1, SupplierName: fmt.Sprintf("Paper wholesaler %d", i+1),
			SupplierSKU: fmt.Sprintf("PW-%03d", i+1), UnitCost: "0.05", IsActive: true,
		})
	}
	s := itemSheetWithSuppliers(t, many)

	if out := itemBandRow(t, s, 19); !strings.Contains(out, "Paper wholesaler 20 (PW-020)") {
		t.Errorf("standing on the last of 20 links should show it:\n%s", out)
	}
	// And paging gets there without twenty keystrokes.
	s.cursor = 0
	for i := 0; i < 14 && s.cursor < s.rowCount()-1; i++ {
		s.Update(namedKey("pgdown"))
	}
	if s.cursor != s.rowCount()-1 {
		t.Errorf("paging stopped at row %d of %d", s.cursor, s.rowCount()-1)
	}
}

// TestItemSuppliers_ADiscontinuedLinkIsDimmedNotHidden. A link nobody can buy
// through today is still worth seeing: an operator wondering where an old
// supplier went can see that it is still there and why it is not usable.
func TestItemSuppliers_ADiscontinuedLinkIsDimmedNotHidden(t *testing.T) {
	sups := itemTestSuppliers()
	s := itemSheetWithSuppliers(t, sups)
	// Stand somewhere else: a FOCUSED row draws focused, not dim.
	s.cursor = 0
	s.syncFocus()

	// The BODY, not the windowed view: with the cursor up in the fields the band
	// is simply scrolled off, which says nothing about whether it was drawn.
	body := strings.Join(s.formLines().text, "\n")
	if !strings.Contains(body, "Old Mill Paper") {
		t.Fatalf("the discontinued link is not on the sheet at all:\n%s", body)
	}
	// The treatment is asserted on the DECISION, not the rendered string: lipgloss
	// is flat in a test binary, so a styled row and a plain one are byte-identical
	// there (sc-lmsi).
	for i, want := range []supplierRowKind{supplierRowPlain, supplierRowPlain, supplierRowDim} {
		if got := s.supplierRowKind(i, sups[i]); got != want {
			t.Errorf("link %d draws as kind %d, want %d", i+1, got, want)
		}
	}
	// …and the cursor's own row wins over dimming, or standing on a discontinued
	// link would leave nothing marking where the cursor is.
	s.cursor = len(s.fields) + 2
	if got := s.supplierRowKind(2, sups[2]); got != supplierRowFocused {
		t.Errorf("the focused row draws focused even when discontinued, got kind %d", got)
	}
	if StyleJDEFieldFocused.GetReverse() == StyleMuted.GetReverse() {
		t.Errorf("the focused and dim treatments must be distinguishable")
	}
	// An inactive link reads differently from a discontinued one, and both from a
	// live one.
	inactive := omsapi.ItemSupplier{ID: 9, SupplierName: "Dormant Co", SupplierSKU: "D-1"}
	joined := strings.Join(s.supplierMetaLines(inactive), " ")
	if !strings.Contains(joined, "[inactive]") || strings.Contains(joined, "[discontinued]") {
		t.Errorf("an inactive link reads [inactive]: %q", joined)
	}
}

// TestItemSuppliers_BarNamesTheDoorAndTheDoorOpens is both halves of the bar
// contract: a key the bar names has to WORK where it is named (sc-lmsi), and the
// keys it does not name must do nothing.
func TestItemSuppliers_BarNamesTheDoorAndTheDoorOpens(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	bar := jdeBarLine(itemBandRow(t, s, 1))

	for _, want := range []string{"Enter=Save", "Esc=Cancel", "UP/DN=Fields", "Ctrl-E=Suppliers"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar on a supplier row should offer %q: %q", want, bar)
		}
	}
	// A read-only row has nothing to cycle.
	if strings.Contains(bar, "←→") {
		t.Errorf("a supplier row has no choice to change: %q", bar)
	}

	// And the key the bar names opens the screen that can actually change these.
	_, cmd := s.Update(namedKey("ctrl+e"))
	sw := resolveSwitch(t, cmd)
	sup, ok := sw.Screen.(*ItemSuppliersScreen)
	if !ok {
		t.Fatalf("Ctrl-E should open the item suppliers screen, got %T", sw.Screen)
	}
	if sup.itemID != "itm-1" {
		t.Errorf("the suppliers screen should be scoped to this item, got %q", sup.itemID)
	}
	if sw.Workspace != WSInventory {
		t.Errorf("workspace = %v, want WSInventory", sw.Workspace)
	}
}

// TestItemSuppliers_NoFieldGestureFiresOnABandRow. A stray key does NOTHING
// rather than firing something invisible — and the routing half of that cannot
// be seen in a screen diff, because a BLURRED bubbles textinput swallows a
// keystroke all by itself (sc-ye0i/sc-hf1z). The zero value of a field id is
// fName, so a dropped guard would not fail loudly: it would quietly type into
// the first field on the sheet.
func TestItemSuppliers_NoFieldGestureFiresOnABandRow(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	before := itemBandRow(t, s, 0)

	for _, key := range []string{"a", "e", "x", "E", "S", "left", "right", " "} {
		s.Update(namedKey(key))
		if got := s.View(); got != before {
			t.Fatalf("%q changed the screen — a supplier row has nothing to type or cycle:\n%s", key, got)
		}
	}

	for id := range s.inputs {
		if isTextKind(id) && s.inputs[id].Focused() {
			t.Errorf("%q keeps focus while the cursor is on a supplier row", itemFieldLabel[id])
		}
	}
	s.inputs[fName].Focus()
	was := s.inputs[fName].Value()
	s.Update(runeKey('z'))
	if got := s.inputs[fName].Value(); got != was {
		t.Errorf("a keystroke on a supplier row reached the Name field: %q → %q", was, got)
	}
}

// TestItemSuppliers_TheDoorAsksBeforeDiscardingEdits. Ctrl-E LEAVES this screen,
// and the form is a raw-input screen the root never records on the back-stack —
// so an unannounced hop would throw away everything typed since the item loaded.
// Esc already means discard and says so on the bar; Ctrl-E does not, so it asks.
func TestItemSuppliers_TheDoorAsksBeforeDiscardingEdits(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	s.cursor = len(s.fields)
	s.syncFocus()

	// Clean sheet: nothing to lose, so the door simply opens.
	if s.dirty() {
		t.Fatalf("a freshly loaded sheet is not dirty")
	}
	_, cmd := s.Update(namedKey("ctrl+e"))
	if resolveSwitch(t, cmd).Screen == nil {
		t.Fatalf("Ctrl-E on a clean sheet should open the suppliers screen")
	}
	if s.supplierWarn {
		t.Errorf("a clean sheet should not be warned about")
	}

	// Now type something and try again.
	s.inputs[fName].SetValue("Copy paper 8.5x11 (recycled)")
	if !s.dirty() {
		t.Fatalf("editing a field should make the sheet dirty")
	}
	_, cmd = s.Update(namedKey("ctrl+e"))
	if cmd != nil {
		t.Fatalf("a dirty sheet should ask before leaving, not leave")
	}
	if !s.supplierWarn {
		t.Fatalf("the door should have raised its warning")
	}
	out := s.View()
	if !strings.Contains(out, "Unsaved edits") {
		t.Errorf("the warning should say what is at stake:\n%s", out)
	}
	bar := jdeBarLine(out)
	for _, want := range []string{"Ctrl-E=Discard & open", "Esc=Stay here"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the confirm owns the bar, expected %q: %q", want, bar)
		}
	}
	// Enter no longer means save while the question is up — the bar says so, and
	// a bar that names two keys must not have a third one working behind it.
	if _, cmd := s.Update(namedKey("enter")); cmd != nil || s.saving {
		t.Errorf("enter should not save from under the confirm (saving=%v)", s.saving)
	}
	if !s.supplierWarn {
		t.Errorf("an unrelated key should leave the question standing")
	}

	// Esc stays…
	if _, cmd := s.Update(namedKey("esc")); cmd != nil {
		t.Errorf("esc under the confirm means STAY, not cancel the form")
	}
	if s.supplierWarn {
		t.Errorf("esc should take the question down")
	}
	if got := s.inputs[fName].Value(); got != "Copy paper 8.5x11 (recycled)" {
		t.Errorf("staying should keep the edit, got %q", got)
	}
	// …and the next esc is the form's own cancel, as always.
	if _, cmd := s.Update(namedKey("esc")); cmd == nil {
		t.Errorf("the second esc should cancel the form")
	}

	// Confirming goes.
	s.supplierWarn = true
	_, cmd = s.Update(namedKey("ctrl+e"))
	if _, ok := resolveSwitch(t, cmd).Screen.(*ItemSuppliersScreen); !ok {
		t.Errorf("confirming should open the suppliers screen")
	}
	if s.supplierWarn {
		t.Errorf("the question should be gone once it is answered")
	}
}

// TestItemSuppliers_EveryEditableThingCountsAsDirty. The failure that matters is
// a change the fingerprint MISSES — that is what would let the door discard it
// without asking — so every piece of state the operator can reach is exercised
// here rather than trusted to the walk over s.inputs.
func TestItemSuppliers_EveryEditableThingCountsAsDirty(t *testing.T) {
	cases := []struct {
		what   string
		change func(s *InventoryItemFormScreen)
	}{
		{"a text field", func(s *InventoryItemFormScreen) { s.inputs[fNotes].SetValue("re-order in bulk") }},
		{"a number field", func(s *InventoryItemFormScreen) { s.inputs[fMinimumStock].SetValue("40") }},
		{"the base unit", func(s *InventoryItemFormScreen) { s.inputs[fBaseUnit].SetValue("sheet") }},
		{"a toggle", func(s *InventoryItemFormScreen) { s.flipToggle(fIsHazardous) }},
		{"the retired flag", func(s *InventoryItemFormScreen) { s.flipToggle(fIsRetired) }},
		{"a select", func(s *InventoryItemFormScreen) { s.cycleSelect(fShelfPosition, +1) }},
		{"the category", func(s *InventoryItemFormScreen) { id := 1; s.categoryID = &id }},
		{"the location", func(s *InventoryItemFormScreen) { id := 2; s.locationID = &id }},
		{"the counting mode", func(s *InventoryItemFormScreen) { s.countModeIx = countModeIndex(omsapi.CountModeByLevel) }},
		{"the packaging chain", func(s *InventoryItemFormScreen) {
			s.packRows = append(s.packRows, packagingRow{key: 99, name: "case", baseUnits: 500})
		}},
		{"the counting level", func(s *InventoryItemFormScreen) {
			s.packRows = []packagingRow{{key: 7, name: "case", baseUnits: 500}, {key: 8, name: "ream", baseUnits: 100}}
			s.snapshotBaseline()
			s.countLevelKey = 8
		}},
	}
	for _, tc := range cases {
		s := itemSheetWithSuppliers(t, itemTestSuppliers())
		if s.dirty() {
			t.Fatalf("%s: the sheet was dirty before anything changed", tc.what)
		}
		tc.change(s)
		if !s.dirty() {
			t.Errorf("changing %s should make the sheet dirty — the door would discard it silently", tc.what)
		}
	}

	// And walking the sheet does not: a cursor that moved is not an edit.
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	for i := 0; i < s.rowCount(); i++ {
		s.Update(namedKey("down"))
	}
	if s.dirty() {
		t.Errorf("walking the cursor down the sheet is not an edit")
	}
}

// TestItemSuppliers_RowsFitTheBody is sweep C's test: clampToBox TRUNCATES an
// over-wide line — no ellipsis, no wrap — so anything past the pane is silently
// lost. It sweeps every cursor row because a focused row draws differently from
// a blurred one.
func TestItemSuppliers_RowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(itemSupplierBandWidth)
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	for row := 0; row < s.rowCount(); row++ {
		s.cursor = row
		s.syncFocus()
		for _, line := range strings.Split(s.View(), "\n") {
			if w := lipgloss.Width(line); w > budget {
				t.Errorf("row %d: a line is %d wide but the pane is %d — it will be clipped: %q",
					row, w, budget, line)
			}
		}
	}
}

// TestItemSuppliers_TheBandFitsAnEightyColumnTerminal is the same test at the
// floor, measured on the BAND ALONE — which is a deliberate narrowing, not an
// oversight. At 80 columns the pane is 51 and the item sheet already loses
// content that predates this bead entirely: four of its field rows overrun (the
// Base unit box at 52, Packaging chain at 55, Count mode at 57, Notes at 70) and
// so does the action bar on every columnar screen in the app. Those are filed as
// sc-xxpa. What this band contributes has to fit the floor regardless, and the
// heading is the line that made the point — 60 columns until it learned to drop
// its aside.
func TestItemSuppliers_TheBandFitsAnEightyColumnTerminal(t *testing.T) {
	const narrow = 80
	budget := screenBodyWidth(narrow)
	for _, sups := range [][]omsapi.ItemSupplier{itemTestSuppliers(), nil} {
		s := itemSheetWithSuppliers(t, sups)
		s.Update(tea.WindowSizeMsg{Width: narrow, Height: jdeSweepHeight})
		for row := 0; row < s.supplierBandRows(); row++ {
			s.cursor = len(s.fields) + row
			s.syncFocus()
			s.supplierWarn = row == 0 // the confirm is part of the band's height too
			band := &jdeLines{}
			s.supplierBand(band)
			for _, line := range band.text {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("%d links, row %d: a band line is %d wide but the pane is %d: %q",
						len(sups), row, w, budget, line)
				}
			}
		}
	}
}

// TestItemSuppliers_TheGridColumnsLineUp is what makes it a grid rather than
// three ragged lines: a supplier cell that overflowed its width would shove the
// cost and lead time right on that row alone, which is the defect a printed
// quotation sheet never has.
func TestItemSuppliers_TheGridColumnsLineUp(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	out := itemBandRow(t, s, 0)

	header, rows := "", []string(nil)
	for _, line := range strings.Split(out, "\n") {
		if header == "" {
			if strings.Contains(line, "Unit cost") && strings.Contains(line, "Lead") {
				header = line
			}
			continue
		}
		if strings.Contains(line, "-88421") || strings.Contains(line, "BD-1104") || strings.Contains(line, "OMP-7") {
			rows = append(rows, line)
		}
	}
	if header == "" || len(rows) != 3 {
		t.Fatalf("expected a column header and 3 grid rows, got header=%q and %d rows:\n%s", header, len(rows), out)
	}

	// In DISPLAY columns, not bytes: an ellipsised name carries a 3-byte "…" that
	// occupies one column, and the star is 3 bytes too, so byte offsets would
	// report a ragged grid that is in fact perfectly aligned (sc-hf1z).
	column := func(line, sub string) int {
		at := strings.Index(line, sub)
		if at < 0 {
			return -1
		}
		return lipgloss.Width(line[:at])
	}
	// The lead times are the LAST cell, so their right edge is the row's end.
	want := lipgloss.Width(header)
	for _, line := range rows {
		if got := lipgloss.Width(line); got != want {
			t.Errorf("row ends at column %d but the header at %d — the grid is ragged: %q", got, want, line)
		}
	}
	// And every cost sits inside the cost column rather than running into it.
	costAt := column(header, "Unit cost")
	for _, line := range rows {
		at := column(line, "$")
		if at < costAt {
			t.Errorf("a cost starts at %d, left of the %q column at %d: %q", at, "Unit cost", costAt, line)
		}
	}
}

// TestItemSuppliers_TheLeaderColumnIsTheFieldsAlone. The band is a detail grid,
// not more fields: it hangs off no leader, and a supplier name — which can be far
// longer than any label — must not shove every input area on the sheet right.
func TestItemSuppliers_TheLeaderColumnIsTheFieldsAlone(t *testing.T) {
	column := func(s *InventoryItemFormScreen) int {
		s.cursor = 0
		s.syncFocus()
		for _, line := range strings.Split(s.View(), "\n") {
			if at := strings.Index(line, jdeLeader); at >= 0 {
				return at
			}
		}
		t.Fatalf("no columnar row rendered:\n%s", s.View())
		return -1
	}

	bare := column(itemSheetWithSuppliers(t, nil))
	loaded := itemSheetWithSuppliers(t, itemTestSuppliers())
	if got := column(loaded); got != bare {
		t.Errorf("the leader column moved from %d to %d because links were attached", bare, got)
	}

	// And no grid line pretends to be a field row.
	itemBandRow(t, loaded, 0)
	for _, line := range strings.Split(loaded.View(), "\n") {
		if strings.Contains(line, "Acme Office Supply") && strings.Contains(line, jdeLeader) {
			t.Errorf("a supplier is a grid row, not a field row: %q", line)
		}
	}
}

// TestItemSuppliers_ALinksLinesShareItsRow. A link's readings and URL belong to
// the link: tagged with its row, the window can never scroll the supplier away
// from the price underneath it.
func TestItemSuppliers_ALinksLinesShareItsRow(t *testing.T) {
	sups := itemTestSuppliers()
	s := itemSheetWithSuppliers(t, sups)
	body := s.formLines()

	for i := range sups {
		row := len(s.fields) + i
		first, last := body.block(row)
		if first == 0 && last == 0 {
			t.Fatalf("link %d has no lines of its own", i+1)
		}
		for j := first; j <= last; j++ {
			if body.row[j] != row {
				t.Errorf("link %d's block holds a line belonging to row %d: %q", i+1, body.row[j], body.text[j])
			}
		}
		if i == 0 && last-first < 2 {
			// the grid row, the readings and the URL
			t.Errorf("link 1 should own its readings and URL, block is %d..%d", first, last)
		}
	}
	// A URL is free text on the wire; a raw newline inside a body line would
	// desynchronise the row accounting the window does.
	for i, line := range body.text {
		if strings.Contains(line, "\n") {
			t.Errorf("body line %d carries a newline: %q", i, line)
		}
	}
}

// TestItemSuppliers_TheSKUSurvivesALongSupplierName: when the name and the SKU
// cannot both fit, the NAME gives way — a shortened name still reads, and the
// SKU is what gets typed into that supplier's order pad.
func TestItemSuppliers_TheSKUSurvivesALongSupplierName(t *testing.T) {
	s := itemSheetWithSuppliers(t, itemTestSuppliers())
	out := itemBandRow(t, s, 1)

	if !strings.Contains(out, "(BD-1104-XL)") {
		t.Errorf("the SKU should survive whole:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the over-long name should be visibly shortened:\n%s", out)
	}
	if strings.Contains(out, "Greater Portland") {
		t.Errorf("the name should have given way to the SKU:\n%s", out)
	}
}
