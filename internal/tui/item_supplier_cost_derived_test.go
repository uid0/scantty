package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// The unit cost on an item↔supplier link is a DERIVED figure: OpenMakerSuite
// resolves the (unit, package) pair against the stored row on every save
// (`inventory.services.suppliers.derive_costs`), so a price typed into the unit
// box can come back recomputed from the case price, and clearing the case price
// clears both. The form must say so where the operator types, or a value is
// rewritten after they entered it on a screen that gave no cue — and a save
// navigates away to ItemSuppliersScreen, so what they read next is the server's
// figure with nothing beside it saying it replaced theirs.
//
// These checks render the REAL screen through Root at the panes Root really
// draws, and measure the CLIPPED frame. Asserting against the screen's own
// View() would pass while the terminal cut the cue off the row, which is the
// class this package keeps being bitten by; and asserting against
// itemSupplierFieldLabel directly would assert the map to itself.

// itemSupplierDerivedForm builds an edit of a link that carries both costs and a
// pack size that does NOT divide evenly — 10.00 over 3 is the shape in which
// re-derivation actually moves money, and a fixture at pack 1 is the one pack
// size at which no defect on this path is reachable at all (the OMS record of
// this work says every pre-existing test of the behaviour was written there).
func itemSupplierDerivedForm(t *testing.T) *ItemSupplierFormScreen {
	t.Helper()
	s := NewItemSupplierFormScreen(Deps{}, "itm-1", "Widget", &omsapi.ItemSupplier{
		ID: 42, Supplier: 5, SupplierName: "Beta", SupplierSKU: "B-9",
		URL: "https://b", UnitCost: "3.33", PackageCost: "10.00",
		PackQuantity: 3, LeadTimeDays: 5, IsPreferred: true,
	})
	s.suppliers = []omsapi.Supplier{{ID: 5, Name: "Beta"}}
	s.hydrate()
	s.loading = false
	return s
}

// itemSupplierFieldRow is the drawn row carrying a field's label, or "" when the
// pane did not draw it. The label's leading word is the anchor and the cue is
// what is asserted about it, so the check is not circular: `Unit cost` says the
// row is on the pane, `(derived)` is the claim under test.
func itemSupplierFieldRow(view, label string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, label+" ") || strings.Contains(line, label+".") {
			return line
		}
	}
	return ""
}

// itemSupplierPaneOf is the screen's own pane out of a full Root frame: what
// sits right of the nav column's rule on each row. Root composes the nav column
// and the pane side by side, so any check that reads ACROSS rows has to drop the
// nav or it reads a menu entry as part of the sentence.
func itemSupplierPaneOf(view string) string {
	var out []string
	for _, line := range strings.Split(view, "\n") {
		if i := strings.Index(line, "│"); i >= 0 {
			out = append(out, line[i+len("│"):])
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// itemSupplierCursorOn puts the form's cursor on one field id, derived from the
// screen's own field order rather than written down as an index — the order is
// the screen's to change.
func itemSupplierCursorOn(t *testing.T, s *ItemSupplierFormScreen, id int) {
	t.Helper()
	for i, f := range s.fields {
		if f == id {
			s.cursor = i
			return
		}
	}
	t.Fatalf("field %d is not on the form", id)
}

// TestItemSupplierForm_TheUnitCostRowSaysItIsDerivedAtEveryPane: wherever the
// unit-cost row reaches the operator's terminal at all, it is labelled derived.
//
// THE CUE IS IN THE LABEL BECAUSE THE LABEL IS THE PART A SHORT PANE CANNOT
// TAKE. `jdeLines.Window` keeps a block's START, so the field row survives
// wherever the field is reachable while a hint folded under it is the tail a
// short body drops — at 80x10 the whole body is that one row. A cue that lived
// only in the hint would be absent at exactly the panes where it is hardest to
// notice a value being rewritten.
func TestItemSupplierForm_TheUnitCostRowSaysItIsDerivedAtEveryPane(t *testing.T) {
	drawn := 0
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := itemSupplierDerivedForm(t)
			itemSupplierCursorOn(t, s, isUnitCost)
			r := jdeRootAt(t, s, w, h)
			row := itemSupplierFieldRow(r.View(), "Unit cost")
			if row == "" {
				continue
			}
			drawn++
			if !strings.Contains(row, "(derived)") {
				t.Errorf("%dx%d draws the unit-cost row without saying the figure is "+
					"derived, so an operator types into it with no cue that the server "+
					"may recompute it from the case price:\n%s", w, h, row)
			}
		}
	}
	// The vacuity guard: a sweep that never found the row would pass with the
	// label deleted.
	if drawn == 0 {
		t.Fatalf("no drawable pane drew the unit-cost row at all, so this check "+
			"asserted nothing; widths=%v heights=%v", jdeDrawableWidths(), jdePaneHeights())
	}
	t.Logf("unit-cost row drawn at %d of the panes Root draws", drawn)
}

// TestItemSupplierForm_TheFocusedCostBoxesStateBothHalves: with the caret in a
// cost box, the hint says what the figure is DERIVED FROM and what CHANGING it
// does — the two halves OMS's own corrected web copy states, and the pair that
// makes the rule closed rather than half-described. Stating only the first
// leaves an operator who edits both boxes with no idea which one won.
//
// Asked at the tallest pane Root draws, because the hint folds and a short body
// keeps only its head; the pane-by-pane claim is the LABEL's, above.
func TestItemSupplierForm_TheFocusedCostBoxesStateBothHalves(t *testing.T) {
	heights := jdePaneHeights()
	tall := heights[len(heights)-1]

	for _, c := range []struct {
		field    int
		label    string
		derived  string // what the figure is derived from / governed by
		changing string // what changing this box does
	}{
		{isUnitCost, "Unit cost", "derived from package cost", "re-prices the package"},
		{isPackageCost, "Package cost", "governs when both change", "clearing it alone clears both"},
	} {
		s := itemSupplierDerivedForm(t)
		itemSupplierCursorOn(t, s, c.field)
		r := jdeRootAt(t, s, 80, tall)
		view := r.View()
		// The hint FOLDS, so a phrase crosses rows and no single row holds it.
		// Flatten — but flatten the PANE, not the frame: Root draws the nav
		// column to the left of every row, so flattening the whole view splices
		// a menu entry into the middle of each folded clause and the phrase is
		// never found however honest the hint is.
		flat := strings.Join(strings.Fields(itemSupplierPaneOf(view)), " ")
		for _, want := range []string{c.derived, c.changing} {
			if !strings.Contains(flat, want) {
				t.Errorf("with the caret in %s, 80x%d never says %q — the box has to state "+
					"both what the figure is derived from and what changing it does, or the "+
					"operator cannot tell which of the two boxes the server will honour:\n%s",
					c.label, tall, want, view)
			}
		}
	}
}

// TestItemSupplierForm_TheDerivedLabelMovesNoInput: adding the cue did not widen
// the shared label column, so no box on the form shifted right.
//
// The column is sized by the WIDEST label (`jdeLabelWidth`), and every input on
// the sheet hangs off that one number — so a cue that outgrew the current widest
// label would pay for itself by moving eight other rows and narrowing every box
// against the 51 cells an 80-column pane gives. Derived from the label set
// rather than compared against a written-down width, which would only pin the
// number as it happens to be today.
func TestItemSupplierForm_TheDerivedLabelMovesNoInput(t *testing.T) {
	s := itemSupplierDerivedForm(t)
	fields := s.formFields()
	got := jdeLabelWidth(fields)

	widest, by := 0, ""
	for id, label := range itemSupplierFieldLabel {
		if id == isUnitCost {
			continue
		}
		if n := len(label); n > widest {
			widest, by = n, label
		}
	}
	if got > widest {
		t.Errorf("the label column is %d cells, wider than the %d %q needs — the "+
			"derived cue on the unit-cost label has grown past the form's widest other "+
			"label and now shoves every input on the sheet right, against a pane that "+
			"only has %d cells at 80 columns", got, widest, by, screenBodyCells(80))
	}
}
