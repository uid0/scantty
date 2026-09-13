package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// The captain's four facts, as an operator reads them on the supplier row.
//
// Written down once and shared, because a check that names its own subset is a
// check that goes quietly green on the fact somebody forgot to list. The two
// barcodes are what this file was opened for; the SKU and the lead time are
// here because the lead time was ALREADY off the pane at 80 columns before a
// barcode was added to the row, and a test that only guards the new column
// would certify a row still losing an old one.
const (
	supUPCPackage = "00812345678905"
	supUPCUnit    = "0812345678905"
	supUPCSKU     = "91290A115"
	supUPCLead    = "lead 5d (measured)"
	supUPCPkgCost = "$14.50/pkg"
)

// supUPCRow is one ordinary MRO supplier link, at the lengths OMS really
// serves: a full-length vendor name, a real McMaster SKU and two 13/14-digit
// barcodes. Every fixture on this screen used to say "Acme", so no test had
// ever rendered a supplier row at a length that reaches the pane's edge — which
// is exactly how a cut lead time and a $14.50 drawn as $14.5 survived.
func supUPCRow(n int) omsapi.ItemSupplier {
	return omsapi.ItemSupplier{
		ID: n, Supplier: n,
		SupplierName: fmt.Sprintf("McMaster-Carr Supply Company %d", n),
		SupplierSKU:  supUPCSKU,
		PackageUPC:   supUPCPackage,
		UnitUPC:      supUPCUnit,
		PackQuantity: 100,
		UnitCost:     omsapi.DecimalString("0.1450"),
		PackageCost:  omsapi.DecimalString("14.50"),
		LeadTimeDays: 5,
		// The widest provenance mark: the lead reading is one of the facts
		// swept, and it must be swept at the length it really draws.
		LeadTimeSource: omsapi.LeadTimeSourceMeasured,
		IsActive:       true,
	}
}

// supUPCPane renders the screen through a REAL Root at this pane and returns
// the CLIPPED frame's body lines.
//
// Through Root and not screen.View(), because clampToBox truncates in
// Root.View() and not in the screen: a test reading the screen's own output
// passes while the terminal shows a cut line, which is the standing rule in
// AGENTS.md and the mechanism every defect this file guards was hiding behind.
func supUPCPane(t *testing.T, rows []omsapi.ItemSupplier, w, h int) string {
	t.Helper()
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Hex bolt M8x40")
	s.loading = false
	s.rows = rows
	r := newTestRoot(s)
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Root).View()
}

// TestItemSuppliers_TheRowCarriesBothBarcodes is the captain's ask, asserted on
// the clipped pane at the width that must hold.
//
// BOTH barcodes, because package_upc and unit_upc are codes on two different
// physical things — the box this supplier ships and an individual unit — so an
// operator holding one of them scans a number the other row would not match.
func TestItemSuppliers_TheRowCarriesBothBarcodes(t *testing.T) {
	pane := supUPCPane(t, []omsapi.ItemSupplier{supUPCRow(1)}, 80, 24)
	for _, want := range []string{
		"box barcode " + supUPCPackage,
		"unit barcode " + supUPCUnit,
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("80x24 pane does not carry %q:\n%s", want, pane)
		}
	}
}

// TestItemSuppliers_EveryFactSurvivesEveryDrawablePane is the bound that makes
// the addition safe rather than merely present.
//
// It walks every pane Root really draws — derived from Root's own gates, never
// a hand-picked pair, because two hand-picked sizes is how the previous
// overruns stayed invisible — and asserts each of the captain's facts reaches
// the CLIPPED pane WHOLE. Scoped to the panes tall enough to draw the row at
// all: a genuinely short pane windows rows away, which is the safe direction
// (fewer rows, each complete), so the claim is asked only where the row is
// drawn, and BOTH sides of that boundary are counted so the scoping cannot
// become a way of asserting nothing.
func TestItemSuppliers_EveryFactSurvivesEveryDrawablePane(t *testing.T) {
	facts := []string{
		"SKU " + supUPCSKU,
		"box barcode " + supUPCPackage,
		"unit barcode " + supUPCUnit,
		supUPCPkgCost,
		supUPCLead,
	}
	widths, heights := jdeDrawableWidths(), jdePaneHeights()

	var whole, marked int
	for _, w := range widths {
		for _, h := range heights {
			pane := supUPCPane(t, []omsapi.ItemSupplier{supUPCRow(1)}, w, h)
			// A pane that could not hold every fact must SAY SO — either the
			// vertical cut mark or the dropped-fact ellipsis is on it — and is
			// then not making a complete claim about the link. Panes that make
			// no mark are the ones the facts must reach whole.
			//
			// Keyed on the marks and not on "did the row appear", because a
			// row drawn with a fact quietly missing is exactly the defect this
			// sweep exists to report, and a boundary that read it as "windowed
			// away" would excuse it.
			if strings.Contains(pane, suppliersRowCutMark) ||
				strings.Contains(pane, strings.TrimSpace(poRowDropMark)) {
				marked++
				continue
			}
			whole++
			for _, want := range facts {
				if !strings.Contains(pane, want) {
					t.Errorf("%dx%d marks no cut but loses %q:\n%s", w, h, want, pane)
				}
			}
		}
	}
	// BOTH sides counted, or the scoping is a way of asserting nothing: if the
	// mark ever became unconditional every pane would be excused and this sweep
	// would pass having checked no fact at all.
	if whole == 0 {
		t.Fatalf("every pane claimed a cut: the sweep asserted nothing")
	}
	if marked == 0 {
		t.Fatalf("no pane marked a cut: the marks are unreachable and untested")
	}
	t.Logf("all facts whole and unmarked at %d panes, cut and marked at %d", whole, marked)
}

// TestItemSuppliers_AFullListKeepsTheActionBar holds the other edge.
//
// Folding a fact line spends ROWS, so a row that now fits horizontally can push
// the bar off the bottom instead — the same claim lost at the other edge. At
// 80x24 with eight links the bar was ALREADY gone before this change (the
// window counted rows against a flat chrome while the renderer draws lines),
// and taller rows only moved the threshold down. Every key the bar names must
// still be on the pane.
func TestItemSuppliers_AFullListKeepsTheActionBar(t *testing.T) {
	var rows []omsapi.ItemSupplier
	for i := 0; i < 8; i++ {
		rows = append(rows, supUPCRow(i))
	}
	// Scoped to the heights whose pane can hold the bar AT ALL, and counted on
	// both sides.
	//
	// The floor is DERIVED from the frame rather than written down: below it
	// the pane has fewer rows than the folded bar has lines, so no arrangement
	// keeps the bar and the claim would be false of the geometry rather than of
	// the code. Root draws down to a terminal height of 7, where the body gets
	// ONE row — this screen has never had an answer there and does not gain one
	// here; what it gains is that from the first height the bar CAN fit, every
	// key it names is on the pane, where before the bar's tail was cut at every
	// height at 80 columns.
	var held, tooShort int
	for _, h := range jdePaneHeights() {
		s := NewItemSuppliersScreen(Deps{}, "itm-1", "Hex bolt M8x40")
		s.loading = false
		s.rows = rows
		r := newTestRoot(s)
		next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: h})
		if screenBodyRows(h) < len(s.suppliersFooterLines())+1 {
			tooShort++
			continue
		}
		held++
		pane := next.(Root).View()
		// Ask the screen itself what its bar spells, so a reworded bar cannot
		// quietly narrow what this guards.
		for _, seg := range s.proseBar() {
			if !strings.Contains(pane, seg.Hint) {
				t.Errorf("80x%d loses bar segment %q:\n%s", h, seg.Hint, pane)
			}
		}
	}
	if held == 0 {
		t.Fatalf("no height held the bar: the sweep asserted nothing")
	}
	t.Logf("bar whole at %d heights, pane too short for it at %d", held, tooShort)
}

// TestItemSuppliers_TheRowAssemblesNothingThePaneCannotHold measures what the
// SCREEN hands over, before clampToBox has had a chance to hide the overrun.
//
// Measured on the clipped pane this check could not fail: the truncation has
// already happened there, so an over-wide line reads as a line that fits.
func TestItemSuppliers_TheRowAssemblesNothingThePaneCannotHold(t *testing.T) {
	for _, w := range jdeDrawableWidths() {
		s := NewItemSuppliersScreen(Deps{}, "itm-1", "Hex bolt M8x40")
		s.loading = false
		s.rows = []omsapi.ItemSupplier{supUPCRow(1)}
		r := newTestRoot(s)
		next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		_ = next
		room := screenBodyCells(w)
		for _, line := range strings.Split(s.View(), "\n") {
			if got := lipgloss.Width(line); got > room {
				t.Errorf("width %d: screen assembles a %d-cell line into a %d-cell pane: %q",
					w, got, room, line)
			}
		}
	}
}
