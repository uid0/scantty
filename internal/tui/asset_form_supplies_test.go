// The asset form's supplies band (sc-hf1z): the inventory items attached to an
// asset as consumable parts, listed on the edit sheet.
//
// The contract these hold it to:
//
//	it is THERE            — editing an asset shows what that asset consumes,
//	                         which is the whole ask
//	it is a grid, not a    — the sheet's leader column is computed from its
//	fields                   FIELDS alone, so a long part name cannot move it
//	the rows are navigable — which is what makes the tail of a long list
//	                         reachable and the band page with the sheet
//	the bar stays honest   — the bar names Ctrl-E where it works and nowhere
//	                         else, and the key it names actually opens
//	nothing is silently    — every line fits the pane, and the reading an
//	cut                      ellipsis would eat (NEEDS REPLACEMENT) wraps
//
// sc-lvp7 added the last two: every row identifies its item by NAME and by the
// last four of its uuid — the disambiguator two same-named parts need — and
// Ctrl-E on a row opens the Parts screen, asking first when that would discard
// unsaved edits.
package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// assetSupplyWidth is the terminal the columnar screens are measured at, as in
// every sweep's width test.
const assetSupplyWidth = 110

// assetSheetWithParts is a loaded EDIT-mode asset form carrying `parts` — the
// state the form reaches after GetAsset returns, since the parts ride nested on
// the asset payload rather than arriving in a request of their own.
func assetSheetWithParts(t *testing.T, parts []omsapi.AssetPart) *AssetFormScreen {
	t.Helper()
	s := NewAssetFormScreen(Deps{}, "a-1")
	s.loading = false
	s.categories = []omsapi.Category{{ID: 1, Name: "Machines"}}
	s.locations = []omsapi.Location{{ID: 2, Name: "Wood shop"}}
	s.asset = &omsapi.Asset{ID: "a-1", Name: "Ultimaker S5 3D printer", Parts: parts}
	s.hydrate()
	s.rebuildFields()
	s.syncFocus()
	// The same order maybeFinalizeLoad takes, baseline included: a sheet that
	// never recorded one would read as dirty from the moment it loaded, and the
	// door would ask about discarding edits nobody made.
	s.snapshotBaseline()
	s.Update(tea.WindowSizeMsg{Width: assetSupplyWidth, Height: jdeSweepHeight})
	return s
}

// assetTestParts is the fixture, and it is deliberately the WIDEST realistic
// one: a part with every clock reading filled in AND a serial, a bare part, and
// a name far longer than the item column. A width test is only as honest as its
// widest fixture (sc-lmsi). The pks are real-shaped uuids, because the last four
// characters of one are now what the item cell ends with (sc-lvp7).
func assetTestParts() []omsapi.AssetPart {
	interval := 90
	days := 96
	replaced := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return []omsapi.AssetPart{
		{
			ID: float64(11), Part: "8f14e45f-ceea-467a-9ba5-5e9f3c2a1b2c",
			PartName: "Magenta ink cartridge", PartSKU: "INK-MAG-01",
			QuantityNeeded: 1, IsRequired: true,
			MaintenanceIntervalDays: &interval, LastReplacedAt: &replaced, DaysSinceReplacement: &days,
			NeedsReplacement: true, ReplacementSerialNumber: "SN-7781",
			Notes: "Genuine cartridges only — the clones jam the print head.",
		},
		{
			ID: float64(12), Part: "c9f0f895-fb98-4b17-a2d9-1e7c04d0e7f3",
			PartName: "0.4mm brass nozzle", PartSKU: "NOZ-04-BRASS",
			QuantityNeeded: 2, IsRequired: false,
		},
		{
			ID: float64(13), Part: "45c48cce-2e2d-4fb1-9b1e-8f7a6c5d4b3a",
			PartName: "Borosilicate glass build plate with a very long descriptive name",
			PartSKU:  "PLATE-GLASS-330X240", QuantityNeeded: 12, IsRequired: true,
			MaintenanceIntervalDays: &interval,
		},
	}
}

// TestAssetSupplies_TheSheetListsTheAssetsInventoryItems is the bead itself: the
// operator editing an asset can see what it consumes without leaving the screen.
func TestAssetSupplies_TheSheetListsTheAssetsInventoryItems(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields) // stand in the band so it is on screen
	s.syncFocus()
	out := s.View()

	for _, want := range []string{
		"Supplies & parts (3)", // the heading counts them
		"Magenta ink cartridge",
		"INK-MAG-01", // the SKU identifies it to a supplier
		"1b2c",       // …and the last four of the uuid identify it to the operator
		"required",   // is_required, spelled out
		"optional",   // …and its other reading, from the second part
		"replace every 90d",
		"last replaced 2026-05-01 (96d ago)",
		"NEEDS REPLACEMENT",
		"s/n SN-7781",
		"Genuine cartridges only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the supplies band should show %q:\n%s", want, out)
		}
	}
	// The quantity is a column of its own, not prose.
	if !strings.Contains(out, "  Qty  Role") {
		t.Errorf("the band should have Qty and Role columns:\n%s", out)
	}
}

// TestAssetSupplies_CreateModeDrawsNoBand: an asset that does not exist yet
// cannot have parts attached, and a permanently empty section is noise on every
// registration.
func TestAssetSupplies_CreateModeDrawsNoBand(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")
	s.loading = false
	s.Update(tea.WindowSizeMsg{Width: assetSupplyWidth, Height: jdeSweepHeight})
	s.cursor = len(s.fields) - 1
	s.syncFocus()

	if got := s.rowCount(); got != len(s.fields) {
		t.Errorf("create mode has no supply rows: rowCount %d, fields %d", got, len(s.fields))
	}
	if out := s.View(); strings.Contains(out, "Supplies & parts") {
		t.Errorf("create mode should draw no supplies band:\n%s", out)
	}
}

// TestAssetSupplies_AnAssetWithNoPartsSaysSoAndKeepsTheDoor — an absent band and
// an empty one are different statements, and only one of them is true here. The
// empty one still gets a NAVIGABLE row (sc-lvp7): that row is the door to the
// screen that attaches the first part, and a band the cursor cannot reach is a
// band with no door.
func TestAssetSupplies_AnAssetWithNoPartsSaysSoAndKeepsTheDoor(t *testing.T) {
	s := assetSheetWithParts(t, nil)
	s.cursor = len(s.fields) - 1
	s.syncFocus()
	out := s.View()

	if !strings.Contains(out, "Supplies & parts (0)") {
		t.Errorf("an asset with no parts still gets the band:\n%s", out)
	}
	if !strings.Contains(out, "(no inventory items attached to this asset)") {
		t.Errorf("the empty band should say what is empty:\n%s", out)
	}
	if strings.Contains(out, "  Qty  Role") {
		t.Errorf("a column header with no rows under it is furniture:\n%s", out)
	}

	if got, want := s.rowCount(), len(s.fields)+1; got != want {
		t.Fatalf("the empty band keeps one navigable row: rowCount %d, want %d", got, want)
	}
	s.Update(namedKey("down"))
	if _, onBand := s.onSupplyRow(); !onBand {
		t.Fatalf("the cursor should reach the empty band's row, sits at %d of %d", s.cursor, s.rowCount())
	}
	if !strings.Contains(jdeBarLine(s.View()), "Ctrl-E=Parts") {
		t.Errorf("the empty row is the door and the bar should say so: %q", jdeBarLine(s.View()))
	}
	_, cmd := s.Update(namedKey("ctrl+e"))
	if _, ok := resolveSwitch(t, cmd).Screen.(*AssetPartsScreen); !ok {
		t.Errorf("Ctrl-E on the empty band should open the parts screen")
	}
}

// TestAssetSupplies_PartRowsExtendTheCursorPastTheFields. The rows are part of
// the sheet's cursor space, which is what makes them scrollable and pageable —
// see _TheLastPartIsReachable for why that matters.
func TestAssetSupplies_PartRowsExtendTheCursorPastTheFields(t *testing.T) {
	parts := assetTestParts()
	s := assetSheetWithParts(t, parts)

	if want := len(s.fields) + len(parts); s.rowCount() != want {
		t.Fatalf("rowCount = %d, want fields+parts = %d", s.rowCount(), want)
	}

	// Down from the last field lands on the first part…
	s.cursor = len(s.fields) - 1
	s.Update(namedKey("down"))
	idx, ok := s.onSupplyRow()
	if !ok || idx != 0 {
		t.Errorf("down from the last field should land on part 1, got idx=%d ok=%v (cursor %d)", idx, ok, s.cursor)
	}
	// …and no field is focused there, so a keystroke cannot land in one.
	if id, ok := s.currentFieldID(); ok {
		t.Errorf("a supply row is not a field, but currentFieldID answered %d", id)
	}

	// Down from the last part wraps to the top of the sheet, as it does from
	// the last field when there are no parts.
	s.cursor = s.rowCount() - 1
	s.Update(namedKey("down"))
	if s.cursor != 0 {
		t.Errorf("cursor = %d after wrapping off the last part, want 0", s.cursor)
	}

	// A rebuild — what a conditional field appearing or disappearing triggers —
	// must not knock the cursor off the band; its clamp measures the whole
	// sheet, not just the fields. No gesture reaches rebuildFields from a part
	// row today, which is precisely why the invariant is pinned here rather
	// than assumed.
	s.cursor = s.rowCount() - 1
	s.rebuildFields()
	if want := s.rowCount() - 1; s.cursor != want {
		t.Errorf("a rebuild moved the cursor off the last part: %d, want %d", s.cursor, want)
	}
}

// TestAssetSupplies_TheLastPartIsReachable is the reason the rows are navigable
// rather than trailing decoration: the body window keeps the CURSOR's block on
// screen, so lines hanging below the last navigable row of a sheet this long
// could never be scrolled to.
func TestAssetSupplies_TheLastPartIsReachable(t *testing.T) {
	many := make([]omsapi.AssetPart, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, omsapi.AssetPart{
			ID: float64(i), Part: fmt.Sprintf("00000000-0000-4000-8000-0000000%04d", i+1),
			PartName: fmt.Sprintf("Filter cartridge %d", i+1),
			PartSKU:  fmt.Sprintf("FLT-%03d", i+1), QuantityNeeded: 1, IsRequired: true,
		})
	}
	s := assetSheetWithParts(t, many)
	s.cursor = s.rowCount() - 1
	s.syncFocus()

	if out := s.View(); !strings.Contains(out, "Filter cartridge 20 (FLT-020 · 0020)") {
		t.Errorf("standing on the last of 20 parts should show it:\n%s", out)
	}
	// And paging gets there without twenty keystrokes.
	s.cursor = 0
	for i := 0; i < 12 && s.cursor < s.rowCount()-1; i++ {
		s.Update(namedKey("pgdown"))
	}
	if s.cursor != s.rowCount()-1 {
		t.Errorf("paging stopped at row %d of %d", s.cursor, s.rowCount()-1)
	}
}

// TestAssetSupplies_BarNamesTheDoorAndTheDoorOpens. The bar is the only
// discovery mechanism left after sc-rdrk retired the letter accelerators, so it
// has to name Ctrl-E where Ctrl-E works — and naming a key is not the same claim
// as the key working, which is why this presses it too (sc-lmsi).
func TestAssetSupplies_BarNamesTheDoorAndTheDoorOpens(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields)
	s.syncFocus()
	bar := jdeBarLine(s.View())

	for _, want := range []string{"Enter=Save", "Esc=Cancel", "UP/DN=Fields", "Ctrl-E=Parts"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar on a part row should offer %q: %q", want, bar)
		}
	}
	// A read-only row has nothing to cycle.
	if strings.Contains(bar, "←→") {
		t.Errorf("a part row has no choice to change: %q", bar)
	}

	// And the key the bar names opens the screen that can actually change these.
	_, cmd := s.Update(namedKey("ctrl+e"))
	sw := resolveSwitch(t, cmd)
	parts, ok := sw.Screen.(*AssetPartsScreen)
	if !ok {
		t.Fatalf("Ctrl-E should open the asset parts screen, got %T", sw.Screen)
	}
	if parts.assetID != "a-1" {
		t.Errorf("the parts screen should be scoped to this asset, got %q", parts.assetID)
	}
	if parts.assetName != "Ultimaker S5 3D printer" {
		t.Errorf("the parts screen should carry the asset's saved name, got %q", parts.assetName)
	}
	if sw.Workspace != WSAssets {
		t.Errorf("workspace = %v, want WSAssets", sw.Workspace)
	}
}

// TestAssetSupplies_TheBarDropsTheDoorOnAFieldRow is the other half of the bar
// contract. Ctrl-E on a plain text field opens nothing, so the bar must not
// teach it there — an inaccurate bar is a real bug now that it is the only place
// keys are learned.
func TestAssetSupplies_TheBarDropsTheDoorOnAFieldRow(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.setCursorToField(afName)
	s.syncFocus()

	if bar := jdeBarLine(s.View()); strings.Contains(bar, "Ctrl-E") {
		t.Errorf("a text field opens nothing, so the bar must not name Ctrl-E: %q", bar)
	}
	if _, cmd := s.Update(namedKey("ctrl+e")); cmd != nil {
		t.Errorf("Ctrl-E on a text field should do nothing, got a command")
	}
	if s.supplyWarn {
		t.Errorf("Ctrl-E on a text field should not raise the band's confirm")
	}
}

// TestAssetSupplies_NoFieldGestureFiresOnAPartRow is the rest of the key
// contract: a stray letter does NOTHING rather than firing something invisible.
func TestAssetSupplies_NoFieldGestureFiresOnAPartRow(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields)
	s.syncFocus()
	before := s.View()

	for _, letter := range []rune{'a', 'c', 'd', 'e', 'n', 'r', 'v', 'x', 'E', 'R', 'S'} {
		s.Update(runeKey(letter))
		if got := s.View(); got != before {
			t.Fatalf("%q changed the screen — no letter is bound on a part row:\n%s", letter, got)
		}
	}
	for _, key := range []string{"left", "right", " "} {
		s.Update(namedKey(key))
		if got := s.View(); got != before {
			t.Fatalf("%q changed the screen — a part row has nothing to cycle:\n%s", key, got)
		}
	}
}

// TestAssetSupplies_APartRowRoutesToNoField is the assertion _NoKeyIsBoundOnA
// PartRow cannot make. A BLURRED bubbles textinput swallows a keystroke all by
// itself (sc-ye0i), so a screen that looks unchanged proves only that nothing
// was visible — not that the key went nowhere. Focusing a field by hand takes
// the blur out of the picture and leaves the routing decision on its own: the
// zero value of a field id is afName, so a dropped guard does not fail loudly,
// it quietly types into the first field on the sheet.
func TestAssetSupplies_APartRowRoutesToNoField(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields)
	s.syncFocus()

	for id := range s.inputs {
		if assetIsTextKind(id) && s.inputs[id].Focused() {
			t.Errorf("%q keeps focus while the cursor is on a part row", assetFieldLabel[id])
		}
	}

	s.inputs[afName].Focus()
	before := s.inputs[afName].Value()
	s.Update(runeKey('z'))
	if got := s.inputs[afName].Value(); got != before {
		t.Errorf("a keystroke on a part row reached the %q field: %q → %q",
			assetFieldLabel[afName], before, got)
	}
}

// TestAssetSupplies_RowsFitTheBody is sweep C's test, which the asset sheet
// never had: clampToBox TRUNCATES an over-wide line — no ellipsis, no wrap — so
// anything past the pane is silently lost. It sweeps every cursor row because a
// focused row draws differently from a blurred one.
func TestAssetSupplies_RowsFitTheBody(t *testing.T) {
	budget := screenBodyWidth(assetSupplyWidth)
	s := assetSheetWithParts(t, assetTestParts())
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

// TestAssetSupplies_TheGridColumnsLineUp is what makes it a grid rather than
// three ragged lines: an item cell that overflowed its width would shove the
// quantity and role right on that row alone, which is the defect a printed
// parts list never has.
func TestAssetSupplies_TheGridColumnsLineUp(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields)
	s.syncFocus()

	header, rows := "", []string(nil)
	for _, line := range strings.Split(s.View(), "\n") {
		if header == "" {
			// Only lines BELOW the column header are grid rows — "required" is
			// also the Name field's hint, higher up the same sheet.
			if strings.Contains(line, "Qty") && strings.Contains(line, "Role") {
				header = line
			}
			continue
		}
		if strings.Contains(line, "required") || strings.Contains(line, "optional") {
			rows = append(rows, line)
		}
	}
	if header == "" || len(rows) != 3 {
		t.Fatalf("expected a column header and 3 grid rows, got header=%q and %d rows:\n%s",
			header, len(rows), s.View())
	}

	// In DISPLAY columns, not bytes: an ellipsised name carries a 3-byte "…"
	// that occupies one column, so byte offsets would report a ragged grid that
	// is in fact perfectly aligned (the same trap sc-k5kc hit measuring bold).
	column := func(line, sub string) int {
		at := strings.Index(line, sub)
		if at < 0 {
			return -1
		}
		return lipgloss.Width(line[:at])
	}

	want := column(header, "Role")
	for _, line := range rows {
		at := column(line, "required")
		if at < 0 {
			at = column(line, "optional")
		}
		if at != want {
			t.Errorf("the Role column is at %d here but %d in the header — the grid is ragged: %q",
				at, want, line)
		}
	}
}

// TestAssetSupplies_TheLeaderColumnIsTheFieldsAlone. The band is a detail grid,
// not more fields: it hangs off no leader, and a part name — which can be far
// longer than any label — must not shove every input area on the sheet right.
func TestAssetSupplies_TheLeaderColumnIsTheFieldsAlone(t *testing.T) {
	column := func(s *AssetFormScreen) int {
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

	bare := column(assetSheetWithParts(t, nil))
	loaded := assetSheetWithParts(t, assetTestParts())
	if got := column(loaded); got != bare {
		t.Errorf("the leader column moved from %d to %d because parts were attached", bare, got)
	}

	// And no grid line pretends to be a field row.
	loaded.cursor = len(loaded.fields)
	loaded.syncFocus()
	for _, line := range strings.Split(loaded.View(), "\n") {
		if strings.Contains(line, "Magenta ink cartridge") && strings.Contains(line, jdeLeader) {
			t.Errorf("a part is a grid row, not a field row: %q", line)
		}
	}
}

// TestAssetSupplies_TheClockWrapsRatherThanLosingTheFlag. The readings run past
// the pane once a serial is recorded, and the piece an ellipsis eats is the LAST
// one — which is where "NEEDS REPLACEMENT" sits. Wrapping is what keeps it.
func TestAssetSupplies_TheClockWrapsRatherThanLosingTheFlag(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	lines := s.supplyMetaLines(assetTestParts()[0])
	if len(lines) < 2 {
		t.Fatalf("the full clock should wrap onto more than one line, got %d: %q", len(lines), lines)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"replace every 90d", "last replaced 2026-05-01 (96d ago)", "s/n SN-7781", "NEEDS REPLACEMENT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the clock dropped %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "…") {
		t.Errorf("a wrapped clock has nothing to ellipsise:\n%s", joined)
	}
	budget := screenBodyWidth(assetSupplyWidth)
	for _, line := range lines {
		if w := lipgloss.Width(line); w > budget {
			t.Errorf("a wrapped clock line is %d wide, pane is %d: %q", w, budget, line)
		}
	}
}

// TestAssetSupplies_TheSKUSurvivesALongItemName: when the name and the SKU
// cannot both fit, the NAME gives way — a shortened name still reads, and the
// SKU is what gets typed into a supplier's order pad.
func TestAssetSupplies_TheSKUSurvivesALongItemName(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields) + 2
	s.syncFocus()
	out := s.View()

	if !strings.Contains(out, "(PLATE-GLASS-330X240 · 4b3a)") {
		t.Errorf("the SKU and the short id should both survive whole:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the over-long name should be visibly shortened:\n%s", out)
	}
	if strings.Contains(out, "very long descriptive name") {
		t.Errorf("the name should have given way to the SKU:\n%s", out)
	}
}

// TestAssetSupplies_TheLastFourOfTheUUIDTellsTwoPartsApart is the bead itself
// (sc-lvp7): Ian asked for the last four of the item's uuid beside its name, and
// the case it exists for is two parts an operator otherwise cannot separate —
// the same name, and no SKU on either to tell them apart.
func TestAssetSupplies_TheLastFourOfTheUUIDTellsTwoPartsApart(t *testing.T) {
	twins := []omsapi.AssetPart{
		{ID: float64(31), Part: "d3d94468-02a2-4c1e-9a11-000000009c41", PartName: "Drive belt", QuantityNeeded: 1, IsRequired: true},
		{ID: float64(32), Part: "6f4922f4-5568-4423-8f27-0000000071b7", PartName: "Drive belt", QuantityNeeded: 1, IsRequired: true},
	}
	s := assetSheetWithParts(t, twins)
	s.cursor = len(s.fields)
	s.syncFocus()

	// Read the cells one at a time rather than searching the whole frame: "the
	// view contains 9c41" passes just as well when both ids landed on one row
	// (sc-7wag).
	itemW := s.supplyItemWidth()
	first, second := supplyItemCell(twins[0], itemW), supplyItemCell(twins[1], itemW)
	if !strings.Contains(first, "9c41") || !strings.Contains(second, "71b7") {
		t.Fatalf("each row carries its OWN last four: %q and %q", first, second)
	}
	if first == second {
		t.Errorf("two same-named parts still read identically: %q", first)
	}
	for _, cell := range []string{first, second} {
		if !strings.Contains(cell, "Drive belt") {
			t.Errorf("the name is still what the row leads with: %q", cell)
		}
		if strings.Contains(cell, "()") {
			t.Errorf("a part with no SKU should not draw an empty pair of brackets: %q", cell)
		}
	}
	if out := s.View(); !strings.Contains(out, "9c41") || !strings.Contains(out, "71b7") {
		t.Errorf("both short ids should be on the sheet:\n%s", out)
	}
}

// TestAssetSupplies_TheShortIDOutlivesTheSKU. The two rides in the item cell are
// not equal: the SKU is the band's own earlier choice about the space (sc-hf1z),
// the short id is what this bead was asked for. So when the pane is too narrow
// for both, the SKU is what gives way — and the id is never the thing an
// ellipsis eats, because a truncated disambiguator disambiguates nothing.
func TestAssetSupplies_TheShortIDOutlivesTheSKU(t *testing.T) {
	p := omsapi.AssetPart{
		ID: float64(41), Part: "1679091c-5a88-4faf-b3b1-000000008d3f",
		PartName: "Magenta ink cartridge", PartSKU: "INK-MAG-01-LONG-CODE",
		QuantityNeeded: 1, IsRequired: true,
	}
	// Wide enough for everything, then squeezed a column at a time down to the
	// band's own floor. The id has to be there at every single width.
	sawFullTail, sawSKUDropped := false, false
	for w := 60; w >= 12; w-- {
		cell := supplyItemCell(p, w)
		if got := lipgloss.Width(cell); got > w {
			t.Fatalf("width %d: the cell is %d wide and would shove the Qty column right: %q", w, got, cell)
		}
		if !strings.Contains(cell, "8d3f") {
			t.Fatalf("width %d: the short id was lost: %q", w, cell)
		}
		// The name is what the row is READ by, so it is never spent down to a
		// bare ellipsis to keep an optional tail whole: past its floor the SKU
		// goes instead. A cell leading with "…" is that trade made backwards.
		if strings.HasPrefix(cell, "…") {
			t.Errorf("width %d: the name was thrown away to keep the tail: %q", w, cell)
		}
		switch {
		case strings.Contains(cell, "INK-MAG-01-LONG-CODE"):
			sawFullTail = true
		case strings.Contains(cell, "INK-MAG"):
			t.Errorf("width %d: a half-printed SKU is a wrong SKU — it should have gone whole: %q", w, cell)
		default:
			sawSKUDropped = true
		}
	}
	if !sawFullTail {
		t.Errorf("a wide pane should show the SKU as well as the id")
	}
	if !sawSKUDropped {
		t.Errorf("a narrow pane should drop the SKU rather than the id")
	}

	// Past the floor of name-plus-id the name is what gets cut, not the id. The
	// band's own column never asks for this (it floors at 12), so pin it here
	// rather than trusting a bound nothing reaches (sc-7wag).
	if cell := supplyItemCell(p, 10); !strings.Contains(cell, "8d3f") || lipgloss.Width(cell) > 10 {
		t.Errorf("even at 10 columns the id survives and the cell fits: %q", cell)
	}
}

// TestAssetSupplies_AnOddPartIDIsHarmless. `part` is a uuid in practice, but the
// cell must not slice off the end of something shorter than four characters or
// absent altogether — and a part with no pk at all still has to identify itself
// somehow.
func TestAssetSupplies_AnOddPartIDIsHarmless(t *testing.T) {
	cases := []struct {
		what, id, want string
	}{
		{"an empty pk", "", ""},
		{"a one-character pk", "7", "7"},
		{"a pk of exactly four", "ab12", "ab12"},
		{"a pk of five", "9ab12", "ab12"},
		{"a padded pk", "  8f14e45f-1b2c  ", "1b2c"},
		{"a multi-byte pk", "naïve-café", "café"},
	}
	for _, tc := range cases {
		p := omsapi.AssetPart{ID: float64(51), Part: tc.id, PartName: "Belt", QuantityNeeded: 1}
		if got := assetPartShortID(p); got != tc.want {
			t.Errorf("%s: short id = %q, want %q", tc.what, got, tc.want)
		}
		cell := supplyItemCell(p, 40)
		if !strings.Contains(cell, "Belt") {
			t.Errorf("%s: the cell lost the name: %q", tc.what, cell)
		}
		if strings.Contains(cell, "()") {
			t.Errorf("%s: an absent id should draw no brackets: %q", tc.what, cell)
		}
	}

	// A part the serializer sent no name for reads as unnamed rather than as its
	// own uuid printed twice — the cell already ends with the last four of it.
	bare := omsapi.AssetPart{ID: float64(52), Part: "8f14e45f-ceea-467a-9ba5-5e9f3c2a1b2c", QuantityNeeded: 1}
	cell := supplyItemCell(bare, 40)
	if strings.Contains(cell, "8f14e45f") {
		t.Errorf("the whole uuid should not be the name as well as the tail: %q", cell)
	}
	if !strings.Contains(cell, "1b2c") {
		t.Errorf("a nameless part is identified by its short id: %q", cell)
	}
}

// TestAssetSupplies_TheDoorAsksBeforeDiscardingEdits. Ctrl-E LEAVES this screen,
// and the form is a raw-input screen the root never records on the back-stack —
// so an unannounced hop would throw away everything typed since the asset loaded.
// Esc already means discard and says so on the bar; Ctrl-E does not, so it asks.
func TestAssetSupplies_TheDoorAsksBeforeDiscardingEdits(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields)
	s.syncFocus()

	// Clean sheet: nothing to lose, so the door simply opens — and until the
	// question is asked it is nowhere on the sheet. (Asserting only that the
	// warning APPEARS would pass just as well for a warning that never left.)
	if s.dirty() {
		t.Fatalf("a freshly loaded sheet is not dirty")
	}
	if strings.Contains(s.View(), "Unsaved edits") {
		t.Errorf("the sheet warns about discarding nothing:\n%s", s.View())
	}
	_, cmd := s.Update(namedKey("ctrl+e"))
	if resolveSwitch(t, cmd).Screen == nil {
		t.Fatalf("Ctrl-E on a clean sheet should open the parts screen")
	}
	if s.supplyWarn {
		t.Errorf("a clean sheet should not be warned about")
	}

	// Now type something and try again.
	s.inputs[afName].SetValue("Ultimaker S5 3D printer (bench 2)")
	if !s.dirty() {
		t.Fatalf("editing a field should make the sheet dirty")
	}
	_, cmd = s.Update(namedKey("ctrl+e"))
	if cmd != nil {
		t.Fatalf("a dirty sheet should ask before leaving, not leave")
	}
	if !s.supplyWarn {
		t.Fatalf("the door should have raised its warning")
	}
	out := s.View()
	if !strings.Contains(out, "Unsaved edits") {
		t.Errorf("the warning should say what is at stake:\n%s", out)
	}
	// Once, under the row the question was asked from — not once per part. It is
	// a question about the SHEET, and a band that repeated it down every row
	// would read as a defect in the list rather than as a prompt.
	if n := strings.Count(out, "Unsaved edits"); n != 1 {
		t.Errorf("the confirm should be asked once, appears %d times:\n%s", n, out)
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
	if !s.supplyWarn {
		t.Errorf("an unrelated key should leave the question standing")
	}

	// Esc stays…
	if _, cmd := s.Update(namedKey("esc")); cmd != nil {
		t.Errorf("esc under the confirm means STAY, not cancel the form")
	}
	if s.supplyWarn {
		t.Errorf("esc should take the question down")
	}
	if got := s.inputs[afName].Value(); got != "Ultimaker S5 3D printer (bench 2)" {
		t.Errorf("staying should keep the edit, got %q", got)
	}
	// …and the next esc is the form's own cancel, as always.
	if _, cmd := s.Update(namedKey("esc")); cmd == nil {
		t.Errorf("the second esc should cancel the form")
	}

	// Confirming goes.
	s.supplyWarn = true
	_, cmd = s.Update(namedKey("ctrl+e"))
	if _, ok := resolveSwitch(t, cmd).Screen.(*AssetPartsScreen); !ok {
		t.Errorf("confirming should open the parts screen")
	}
	if s.supplyWarn {
		t.Errorf("the question should be gone once it is answered")
	}
}

// TestAssetSupplies_EveryEditableThingCountsAsDirty. The failure that matters is
// a change the fingerprint MISSES — that is what would let the door discard it
// without asking — so every piece of state the operator can reach is exercised
// here rather than trusted to the walk over s.inputs.
func TestAssetSupplies_EveryEditableThingCountsAsDirty(t *testing.T) {
	cases := []struct {
		what   string
		change func(s *AssetFormScreen)
	}{
		{"a text field", func(s *AssetFormScreen) { s.inputs[afNotes].SetValue("bench 2, by the window") }},
		{"a number field", func(s *AssetFormScreen) { s.inputs[afAmountPaid].SetValue("5995.00") }},
		{"a date field", func(s *AssetFormScreen) { s.inputs[afDateReceived].SetValue("2026-01-09") }},
		{"a toggle", func(s *AssetFormScreen) { s.flipToggle(afNeedsVentilation) }},
		{"the active flag", func(s *AssetFormScreen) { s.flipToggle(afIsActive) }},
		{"the donation flag", func(s *AssetFormScreen) { s.flipToggle(afIsDonation) }},
		{"the status select", func(s *AssetFormScreen) { s.cycleSelect(afStatus, +1) }},
		{"the ownership select", func(s *AssetFormScreen) { s.cycleSelect(afOwnership, +1) }},
		{"the inventory item", func(s *AssetFormScreen) { id := "itm-9"; s.inventoryItemID = &id }},
		{"the category", func(s *AssetFormScreen) { id := 1; s.categoryID = &id }},
		{"the location", func(s *AssetFormScreen) { id := 2; s.locationID = &id }},
		{"the owning group", func(s *AssetFormScreen) { id := 3; s.owningGroupID = &id }},
		{"the owning user", func(s *AssetFormScreen) { id := 4; s.owningUserID = &id }},
		{"the required certifications", func(s *AssetFormScreen) { s.certIDs = append(s.certIDs, 7) }},
	}
	for _, tc := range cases {
		s := assetSheetWithParts(t, assetTestParts())
		if s.dirty() {
			t.Fatalf("%s: the sheet was dirty before anything changed", tc.what)
		}
		tc.change(s)
		if !s.dirty() {
			t.Errorf("changing %s should make the sheet dirty — the door would discard it silently", tc.what)
		}
	}

	// And walking the sheet does not: a cursor that moved is not an edit.
	s := assetSheetWithParts(t, assetTestParts())
	for i := 0; i < s.rowCount(); i++ {
		s.Update(namedKey("down"))
	}
	if s.dirty() {
		t.Errorf("walking the cursor down the sheet is not an edit")
	}
}

// TestAssetSupplies_TheBandFitsAnEightyColumnTerminal. sc-7wag found that 80
// columns is where a columnar sheet's rows start being clipped, and that several
// of this sheet's FIELD rows and the action bar itself already overrun there
// (filed as sc-xxpa). Those are not this bead's to fix, so this holds the BAND
// alone to the narrow pane — including the wider item cell it just grew.
func TestAssetSupplies_TheBandFitsAnEightyColumnTerminal(t *testing.T) {
	const narrow = 80
	budget := screenBodyWidth(narrow)
	s := assetSheetWithParts(t, assetTestParts())
	s.Update(tea.WindowSizeMsg{Width: narrow, Height: jdeSweepHeight})

	for row := len(s.fields); row < s.rowCount(); row++ {
		s.cursor = row
		s.syncFocus()
		body := s.formLines()
		for i, line := range body.text {
			if i < len(body.row) && body.row[i] < len(s.fields) {
				continue // a field row, not the band's
			}
			if w := lipgloss.Width(line); w > budget {
				t.Errorf("row %d: a band line is %d wide but an 80-column pane is %d: %q",
					row, w, budget, line)
			}
		}
	}
}

// TestAssetSupplies_APartsLinesShareItsRow. A part's clock and note belong to
// the part: tagged with its row, the window can never scroll the item away from
// the readings underneath it.
func TestAssetSupplies_APartsLinesShareItsRow(t *testing.T) {
	parts := assetTestParts()
	s := assetSheetWithParts(t, parts)
	body := s.formLines()

	for i := range parts {
		row := len(s.fields) + i
		first, last := body.block(row)
		if first == 0 && last == 0 {
			t.Fatalf("part %d has no lines of its own", i+1)
		}
		for j := first; j <= last; j++ {
			if body.row[j] != row {
				t.Errorf("part %d's block holds a line belonging to row %d: %q", i+1, body.row[j], body.text[j])
			}
		}
		if i == 0 && last-first < 3 {
			// the grid row, two wrapped clock lines and the note
			t.Errorf("part 1 should own its clock and note lines, block is %d..%d", first, last)
		}
	}
}

// TestAssetSupplies_AMultilineNoteStaysOneLine — a note is free text in the
// database, and a raw newline inside a body line would desynchronise the row
// accounting the window does.
func TestAssetSupplies_AMultilineNoteStaysOneLine(t *testing.T) {
	parts := []omsapi.AssetPart{{
		ID: float64(21), Part: "u", PartName: "Belt", PartSKU: "BLT-1", QuantityNeeded: 1,
		Notes: "check tension\nthen re-seat the idler",
	}}
	s := assetSheetWithParts(t, parts)
	if got := s.supplyNoteLine(parts[0]); !strings.Contains(got, "check tension then re-seat the idler") {
		t.Errorf("the note should collapse onto one line, got %q", got)
	}
	body := s.formLines()
	for i, line := range body.text {
		if strings.Contains(line, "\n") {
			t.Errorf("body line %d carries a newline: %q", i, line)
		}
	}
}
