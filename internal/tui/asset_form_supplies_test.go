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
//	the bar stays honest   — a part row opens nothing, so the bar names no
//	                         key that would do nothing here
//	nothing is silently    — every line fits the pane, and the reading an
//	cut                      ellipsis would eat (NEEDS REPLACEMENT) wraps
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
	s.Update(tea.WindowSizeMsg{Width: assetSupplyWidth, Height: jdeSweepHeight})
	return s
}

// assetTestParts is the fixture, and it is deliberately the WIDEST realistic
// one: a part with every clock reading filled in AND a serial, a bare part, and
// a name far longer than the item column. A width test is only as honest as its
// widest fixture (sc-lmsi).
func assetTestParts() []omsapi.AssetPart {
	interval := 90
	days := 96
	replaced := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return []omsapi.AssetPart{
		{
			ID: float64(11), Part: "item-uuid-1", PartName: "Magenta ink cartridge", PartSKU: "INK-MAG-01",
			QuantityNeeded: 1, IsRequired: true,
			MaintenanceIntervalDays: &interval, LastReplacedAt: &replaced, DaysSinceReplacement: &days,
			NeedsReplacement: true, ReplacementSerialNumber: "SN-7781",
			Notes: "Genuine cartridges only — the clones jam the print head.",
		},
		{
			ID: float64(12), Part: "item-uuid-2", PartName: "0.4mm brass nozzle", PartSKU: "NOZ-04-BRASS",
			QuantityNeeded: 2, IsRequired: false,
		},
		{
			ID: float64(13), Part: "item-uuid-3",
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

// TestAssetSupplies_AnAssetWithNoPartsSaysSo — an absent band and an empty one
// are different statements, and only one of them is true here.
func TestAssetSupplies_AnAssetWithNoPartsSaysSo(t *testing.T) {
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
}

// TestAssetSupplies_TheLastPartIsReachable is the reason the rows are navigable
// rather than trailing decoration: the body window keeps the CURSOR's block on
// screen, so lines hanging below the last navigable row of a sheet this long
// could never be scrolled to.
func TestAssetSupplies_TheLastPartIsReachable(t *testing.T) {
	many := make([]omsapi.AssetPart, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, omsapi.AssetPart{
			ID: float64(i), Part: "u", PartName: fmt.Sprintf("Filter cartridge %d", i+1),
			PartSKU: fmt.Sprintf("FLT-%03d", i+1), QuantityNeeded: 1, IsRequired: true,
		})
	}
	s := assetSheetWithParts(t, many)
	s.cursor = s.rowCount() - 1
	s.syncFocus()

	if out := s.View(); !strings.Contains(out, "Filter cartridge 20 (FLT-020)") {
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

// TestAssetSupplies_BarNamesNothingToOpenOnAPartRow. The band is read-only, so
// the bar offers the system keys and nothing else — naming Ctrl-E here would
// teach a key that does nothing.
func TestAssetSupplies_BarNamesNothingToOpenOnAPartRow(t *testing.T) {
	s := assetSheetWithParts(t, assetTestParts())
	s.cursor = len(s.fields)
	s.syncFocus()
	bar := jdeBarLine(s.View())

	for _, want := range []string{"Enter=Save", "Esc=Cancel", "UP/DN=Fields"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar should always offer %q: %q", want, bar)
		}
	}
	for _, deny := range []string{"Ctrl-E", "←→"} {
		if strings.Contains(bar, deny) {
			t.Errorf("a read-only part row must not offer %q: %q", deny, bar)
		}
	}
}

// TestAssetSupplies_NoKeyIsBoundOnAPartRow is the other half of the bar
// contract: a stray letter does NOTHING rather than firing something invisible.
func TestAssetSupplies_NoKeyIsBoundOnAPartRow(t *testing.T) {
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
	for _, key := range []string{"left", "right", " ", "ctrl+e"} {
		s.Update(namedKey(key))
		if got := s.View(); got != before {
			t.Fatalf("%q changed the screen — a part row has nothing to cycle or open:\n%s", key, got)
		}
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

	if !strings.Contains(out, "(PLATE-GLASS-330X240)") {
		t.Errorf("the SKU should survive whole:\n%s", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("the over-long name should be visibly shortened:\n%s", out)
	}
	if strings.Contains(out, "very long descriptive name") {
		t.Errorf("the name should have given way to the SKU:\n%s", out)
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
