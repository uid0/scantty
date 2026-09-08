package tui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// po_case_entry_test.go — the captain's report, on every screen that takes a
// purchase-order line's quantity or price.
//
// "In ScanTTY, when I enter items for a Purchase Order, I'm putting in the case
// pricing. It's saving the unit cost, even though we're ordering case
// quantities."
//
// Every check here drives the REAL screens through Root.Update and asserts
// either the clipped pane the terminal shows or the request body that reached
// the httptest fake. A test that read the screen struct would pass over the one
// thing the report is about — what the wire carried.

// poCaseFolded is the CLIPPED pane with its folds joined: the derivation under
// the fields is one sentence that jdeWrapNote breaks across lines to fit 51
// columns, so a substring spanning a fold is not on any single line even though
// the operator reads it as one.
//
// It joins folds and collapses runs of spaces. It does NOT hide a CLIP —
// poCaseNothingIsCut asserts that separately, on the unjoined lines, because a
// sentence cut at the pane edge and one folded onto the next row look the same
// once the newlines are gone and only one of them is a defect.
func poCaseFolded(t *testing.T, s Screen, width int) string {
	t.Helper()
	pane := clampToBox(s.View(), screenBodyWidth(width), 400)
	poCaseNothingIsCut(t, s, width)
	return strings.Join(strings.Fields(strings.ReplaceAll(pane, "\n", " ")), " ")
}

// poCaseNothingIsCut fails on any line of the frame wider than the pane, which
// is what clampToBox would take the tail of.
func poCaseNothingIsCut(t *testing.T, s Screen, width int) {
	t.Helper()
	budget := screenBodyWidth(width)
	for i, line := range strings.Split(strings.TrimSuffix(s.View(), "\n"), "\n") {
		if w := lipgloss.Width(line); w > budget {
			t.Errorf("line %d is %d cells against a %d-cell pane, losing %q",
				i+1, w, budget, string([]rune(line)[budget:]))
		}
	}
}

// ---------------------------------------------------------------------------
// Add-line: scanning a case onto an existing draft
// ---------------------------------------------------------------------------

// poCaseRows is the add-line catalogue with the captain's own arithmetic in it:
// a case of 24 that the vendor charges 48.00 for. The stored per-unit price is
// 2.00, so a case price typed into a per-unit row records a line worth 24 × 48
// = 1,152 instead of 48.
func poCaseRows() []poAddCatalogRow {
	return []poAddCatalogRow{
		{itemSupplier: 21, name: "Nitrile glove, blue, L", sku: "GL-L",
			supplierSKU: "AF-24-CASE", perPackage: 24, suggestQty: 24, suggestCost: "2.0000"},
		{itemSupplier: 22, name: "Widget clamp", sku: "WC-1", supplierSKU: "AF-77",
			perPackage: 1, suggestQty: 4, suggestCost: "1.2500"},
	}
}

// poCaseAdd opens the add-line flow, scans `id`, and lands on the price phase.
func poCaseAdd(t *testing.T, fake *poAddFake, width, height int, id string) (Root, *PurchaseOrderAddLineScreen) {
	t.Helper()
	r, s := poAddAt(t, fake, width, height)
	s.idIn.SetValue(id)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // look up
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // confirm the item
	if s.phase != poAddPhasePrice {
		t.Fatalf("scanning %q landed on phase %v, want the price phase", id, s.phase)
	}
	return r, s
}

// poCaseSetRows types over both price rows, leaving the caret where it started.
func poCaseSetRows(t *testing.T, r Root, s *PurchaseOrderAddLineScreen, qty, cost string) Root {
	t.Helper()
	if s.priceFocus != poAddFieldQty {
		t.Fatalf("the price phase opened on row %d, want the quantity row", s.priceFocus)
	}
	s.qtyIn.SetValue("")
	r = poType(t, r, qty)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	s.costIn.SetValue("")
	return poType(t, r, cost)
}

// TestPOAddLine_ACaseQuantityAndCasePriceReachTheWireConverted is the report,
// end to end, on the screen it was filed against: one case of 24 at 48.00 must
// reach OMS as 24 base units at 2.00 each.
//
// It asserts the POST BODY. add_line_item stores quantity_ordered in base units
// and unit_cost_ordered per base unit, so a screen that posted what was typed
// would record a £1,152 line — and every figure built on it, line and order
// totals, purchase history and the price suggested next time, inherits that.
func TestPOAddLine_ACaseQuantityAndCasePriceReachTheWireConverted(t *testing.T) {
	fake := &poAddFake{rows: poCaseRows()}
	r, s := poCaseAdd(t, fake, 80, 24, "AF-24-CASE")

	if !s.caseBasis {
		t.Fatal("a candidate whose supplier ships 24 to a case must open in cases")
	}
	r = poCaseSetRows(t, r, s, "1", "48.00")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if len(fake.adds) != 1 {
		t.Fatalf("the add posted %d time(s), want once", len(fake.adds))
	}
	body := fake.adds[0]
	if got := body["quantity"]; got != float64(24) {
		t.Errorf("quantity = %v, want 24 base units for one case of 24", got)
	}
	if got := body["unit_cost"]; got != "2" {
		t.Errorf("unit_cost = %v, want the per-unit 2 derived from a 48.00 case", got)
	}
}

// TestPOAddLine_ASinglesLineIsUnchanged is the other half of the acceptance
// criteria and the reason the conversion is gated on the vendor's declared case
// rather than applied everywhere: a supplier who sells singles must see exactly
// the rows and exactly the payload they saw before.
func TestPOAddLine_ASinglesLineIsUnchanged(t *testing.T) {
	fake := &poAddFake{rows: poCaseRows()}
	r, s := poCaseAdd(t, fake, 80, 24, "AF-77")

	if s.caseBasis {
		t.Fatal("a supplier selling singles has no case basis to enter at")
	}
	pane := poCaseFolded(t, s, 80)
	if !strings.Contains(pane, "Quantity") || !strings.Contains(pane, "Unit cost") {
		t.Errorf("a singles line must keep the Quantity / Unit cost rows:\n%s", pane)
	}
	if strings.Contains(pane, "Cases") || strings.Contains(pane, "Case cost") {
		t.Errorf("a singles line must not offer case entry:\n%s", pane)
	}
	for _, it := range s.bar() {
		if it.Key == "Ctrl-T" {
			t.Error("the bar offers Ctrl-T on a line with only one basis")
		}
	}

	r = poCaseSetRows(t, r, s, "7", "1.25")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.adds) != 1 {
		t.Fatalf("the add posted %d time(s), want once", len(fake.adds))
	}
	if got := fake.adds[0]["quantity"]; got != float64(7) {
		t.Errorf("quantity = %v, want the 7 that was typed", got)
	}
	if got := fake.adds[0]["unit_cost"]; got != "1.25" {
		t.Errorf("unit_cost = %v, want the 1.25 that was typed", got)
	}
}

// TestPOAddLine_TheRowsSayWhichUnitTheyAreIn holds the acceptance criterion the
// relabelling alone would have satisfied and the conversion alone would not:
// what one case contains, and what the line comes to, both on the pane before
// Enter is pressed.
//
// Asserted on the CLIPPED render at three widths. Root.View truncates rather
// than wrapping, so a row read off the screen's own View() passes while the
// terminal shows it cut — and 80 columns is the width that must hold.
func TestPOAddLine_TheRowsSayWhichUnitTheyAreIn(t *testing.T) {
	for _, width := range []int{80, 100, 120} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &poAddFake{rows: poCaseRows()}
			r, s := poCaseAdd(t, fake, width, 30, "AF-24-CASE")
			r = poCaseSetRows(t, r, s, "3", "48.00")

			pane := poCaseFolded(t, s, width)
			for _, want := range []string{
				"Cases",             // the quantity row says what it counts
				"Case cost",         // the price row says what it prices
				"1 case = 24 units", // what one case contains
				"3 cases × 24 = 72 units",
				"$48.00/case ÷ 24 = $2.00/unit",
				"Line total", "144.00", // what the line comes to, on its own row
			} {
				if !strings.Contains(pane, want) {
					t.Errorf("the clipped pane does not carry %q:\n%s", want, pane)
				}
			}
		})
	}
}

// TestPOAddLine_AnUnwholeSuggestionEntersInUnitsAndTheBarFollows is the state
// the conversion must NOT round its way out of.
//
// OMS rounds a suggestion up to a whole supplier package only for items counted
// in base units, so a pack-counted item can suggest 30 against a case of 12.
// Opening that at case basis would have to order 36. The rows open in units,
// the pane says why, and the bar does not name Ctrl-T — because from that state
// the key could only decline, and a bar may not name a key that will not act.
// Typing a whole case count brings the key back on the same keystroke.
func TestPOAddLine_AnUnwholeSuggestionEntersInUnitsAndTheBarFollows(t *testing.T) {
	fake := &poAddFake{rows: []poAddCatalogRow{
		{itemSupplier: 31, name: "Rag, shop, red", sku: "RAG-1", supplierSKU: "AF-ODD",
			perPackage: 12, suggestQty: 30, suggestCost: "0.7500"},
	}}
	r, s := poCaseAdd(t, fake, 80, 30, "AF-ODD")

	if s.caseBasis {
		t.Fatal("30 units is not a whole number of 12-unit cases; the rows must open in units")
	}
	pane := poCaseFolded(t, s, 80)
	if !strings.Contains(pane, "not a whole number of 12-unit cases (24 or 36 is)") {
		t.Errorf("the pane does not say why the rows are in units:\n%s", pane)
	}
	if poCaseBarNames(s.bar(), "Ctrl-T") {
		t.Error("the bar names Ctrl-T over a quantity that has no case count")
	}

	// The gate is live, not a decision taken at entry: 36 is a whole number of
	// cases, so the key comes back without leaving the row.
	s.qtyIn.SetValue("")
	r = poType(t, r, "36")
	if !poCaseBarNames(s.bar(), "Ctrl-T") {
		t.Fatal("the bar still hides Ctrl-T over 36 units, which is three whole cases")
	}
	r = key(t, r, poCtrlT())
	if !s.caseBasis {
		t.Fatal("ctrl+t did not move the rows onto the case basis")
	}
	if got := s.qtyIn.Value(); got != "3" {
		t.Errorf("quantity after the flip = %q, want 3 cases", got)
	}
	if got := s.costIn.Value(); got != "9" {
		t.Errorf("cost after the flip = %q, want the 0.75 unit price as a 9.00 case", got)
	}
	if pane := poCaseFolded(t, s, 80); !strings.Contains(pane, "Cases") {
		t.Errorf("the flipped pane does not label the row Cases:\n%s", pane)
	}
}

// TestPOAddLine_TheFlipPreservesTheOrderOnTheWire is the round trip: a line
// entered in cases and then flipped to units posts the same quantity and the
// same price it would have posted before the flip. A toggle that changed what
// was being ordered would be the "never silently overwrite" rule broken by the
// key that exists to explain the order.
func TestPOAddLine_TheFlipPreservesTheOrderOnTheWire(t *testing.T) {
	fake := &poAddFake{rows: poCaseRows()}
	r, s := poCaseAdd(t, fake, 80, 30, "AF-24-CASE")
	r = poCaseSetRows(t, r, s, "2", "48.00")

	r = key(t, r, poCtrlT())
	if s.caseBasis {
		t.Fatal("ctrl+t did not move the rows onto the unit basis")
	}
	if got := s.qtyIn.Value(); got != "48" {
		t.Errorf("quantity after the flip = %q, want the 48 units two cases of 24 come to", got)
	}
	if got := s.costIn.Value(); got != "2" {
		t.Errorf("cost after the flip = %q, want the per-unit 2", got)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.adds) != 1 {
		t.Fatalf("the add posted %d time(s), want once", len(fake.adds))
	}
	if got := fake.adds[0]["quantity"]; got != float64(48) {
		t.Errorf("quantity = %v, want 48", got)
	}
	if got := fake.adds[0]["unit_cost"]; got != "2" {
		t.Errorf("unit_cost = %v, want 2", got)
	}
}

func poCaseBarNames(bar []actionBarItem, key string) bool {
	for _, it := range bar {
		if it.Key == key {
			return true
		}
	}
	return false
}

func poCtrlT() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlT} }

// ---------------------------------------------------------------------------
// New PO: the same conversion on the create flow's line form
// ---------------------------------------------------------------------------

// poCaseLineForm drives the New PO flow from the source chooser into the line
// form via the picker `source` names, and returns with the form open.
func poCaseLineForm(t *testing.T, fake *poPickFake, width int, source string) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	r, s := poPickerAtSize(t, fake, width, 30)
	r = key(t, r, poRuneKey(source))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poPhaseLine {
		t.Fatalf("%q then enter landed on phase %v, want the line form", source, s.phase)
	}
	return r, s
}

// poCaseFillLine types over the line form's quantity and cost rows. The form
// opens on the description, so it walks down to each row rather than reaching
// into the inputs — the caret has to be where the value is going.
func poCaseFillLine(t *testing.T, r Root, s *PurchaseOrderCreateScreen, qty, cost string) Root {
	t.Helper()
	r = poReachLineField(t, r, s, poLineFieldQty)
	s.lineInputs[poLineFieldQty].SetValue("")
	r = poType(t, r, qty)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if s.lineFocused != poLineFieldCost {
		t.Fatalf("down from the quantity row landed on field %d, want the cost row", s.lineFocused)
	}
	s.lineInputs[poLineFieldCost].SetValue("")
	return poType(t, r, cost)
}

// TestPOCreate_ACaseQuantityAndCasePriceReachTheWireConverted is the report on
// the New PO flow — the sibling the captain did not name and the one the
// "derive the set, do not fix the screen that was reported" rule is about.
//
// The whole cart goes in one create request, so the assertion is that request's
// own body: two cases of 24 at 48.00 must arrive as 48 base units at 2.00.
func TestPOCreate_ACaseQuantityAndCasePriceReachTheWireConverted(t *testing.T) {
	fake := &poPickFake{catalog: 3, catalogPack: 24, catalogPackageCost: "48.00"}
	r, s := poCaseLineForm(t, fake, 80, "i")

	if !s.caseBasis {
		t.Fatal("a catalog row shipping 24 to a case must open the form in cases")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "48" {
		t.Errorf("the cost row prefilled %q, want the catalog's 48.00 case price", got)
	}

	r = poCaseFillLine(t, r, s, "2", "48.00")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // stage the line
	r = key(t, r, poRuneKey("d"))                 // review
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit

	line := fake.createdLine(t, 0)
	if got := line["quantity"]; got != float64(48) {
		t.Errorf("quantity = %v, want the 48 base units two cases of 24 come to", got)
	}
	if got := line["unit_cost"]; got != float64(2) {
		t.Errorf("unit_cost = %v, want the per-unit 2 derived from a 48.00 case", got)
	}
}

// TestPOCreate_ASinglesLineIsUnchanged is the singles half on the create flow.
func TestPOCreate_ASinglesLineIsUnchanged(t *testing.T) {
	fake := &poPickFake{catalog: 3}
	r, s := poCaseLineForm(t, fake, 80, "i")

	if s.caseBasis {
		t.Fatal("a catalog row with no declared case must stay on the unit basis")
	}
	if poCaseBarNames(s.barItems(false), "Ctrl-T") {
		t.Error("the bar offers Ctrl-T on a line with only one basis")
	}
	r = poCaseFillLine(t, r, s, "9", "3.50")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, poRuneKey("d"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	line := fake.createdLine(t, 0)
	if got := line["quantity"]; got != float64(9) {
		t.Errorf("quantity = %v, want the 9 that was typed", got)
	}
	if got := line["unit_cost"]; got != float64(3.5) {
		t.Errorf("unit_cost = %v, want the 3.50 that was typed", got)
	}
}

// TestPOCreate_TheLineFormSaysWhatACaseHoldsAndWhatTheLineComesTo is the
// create flow's half of "show enough for the operator to catch a mistake",
// asserted on the clipped pane at every width this project checks.
func TestPOCreate_TheLineFormSaysWhatACaseHoldsAndWhatTheLineComesTo(t *testing.T) {
	for _, width := range []int{80, 100, 120} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &poPickFake{catalog: 3, catalogPack: 24, catalogPackageCost: "48.00"}
			r, s := poCaseLineForm(t, fake, width, "i")
			r = poCaseFillLine(t, r, s, "2", "48.00")

			pane := poCaseFolded(t, s, width)
			for _, want := range []string{
				"Cases", "Case cost",
				"1 case = 24 units",
				"2 cases × 24 = 48 units",
				"$48.00/case ÷ 24 = $2.00/unit",
				"line $96.00",
			} {
				if !strings.Contains(pane, want) {
					t.Errorf("the clipped pane does not carry %q:\n%s", want, pane)
				}
			}
		})
	}
}

// TestPOCreate_TheCartStatesACasePackedLineInCases is the last surface before
// the order is committed. A row reading "×48 @ $2" is not false, but it is the
// arithmetic the report turns on, and an operator checking a cart against a
// vendor's quote has nothing to compare it with.
func TestPOCreate_TheCartStatesACasePackedLineInCases(t *testing.T) {
	fake := &poPickFake{catalog: 3, catalogPack: 24, catalogPackageCost: "48.00"}
	r, s := poCaseLineForm(t, fake, 80, "i")
	r = poCaseFillLine(t, r, s, "2", "48.00")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, poRuneKey("d"))

	pane := poCaseFolded(t, s, 80)
	if !strings.Contains(pane, "×2 cs @ $48") {
		t.Errorf("the review cart does not state the line in cases:\n%s", pane)
	}
}

// TestPOCreate_AReopenedCartLineComesBackInCases is the edit path: Ctrl-E
// re-opens a staged line, and the line holds BASE units and a per-base-unit
// cost. Re-opening it in units on an item bought by the case would put the
// operator back in front of the row the report is about.
func TestPOCreate_AReopenedCartLineComesBackInCases(t *testing.T) {
	fake := &poPickFake{catalog: 3, catalogPack: 24, catalogPackageCost: "48.00"}
	r, s := poCaseLineForm(t, fake, 80, "i")
	r = poCaseFillLine(t, r, s, "2", "48.00")
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, poRuneKey("d"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})

	if s.phase != poPhaseLine {
		t.Fatalf("ctrl+e landed on phase %v, want the line form", s.phase)
	}
	if !s.caseBasis {
		t.Fatal("a staged case-packed line must re-open in cases")
	}
	if got := s.lineInputs[poLineFieldQty].Value(); got != "2" {
		t.Errorf("the quantity row re-opened at %q, want 2 cases", got)
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "48" {
		t.Errorf("the cost row re-opened at %q, want the 48 case price", got)
	}
}

// ---------------------------------------------------------------------------
// The reorder queue, which is where the derived set stopped being obvious
// ---------------------------------------------------------------------------

// TestPOReorderQueue_ACasePackedRowOpensInCases is the sibling the code said
// could not exist. po_create_pickers.go carried a comment reading "the
// reorder_data row carries no quantity_per_package, so a reorder line stays
// single-basis" — reorder_data writes both quantity_per_package and
// package_cost from the item_supplier row, and the struct decoding it simply
// had no field for either. Every line staged from the queue therefore reached
// the form as a singles line, which is the captain's defect on the path the
// report did not name.
func TestPOReorderQueue_ACasePackedRowOpensInCases(t *testing.T) {
	fake := &poPickFake{reorder: 2, reorderPack: 24, reorderPackageCost: "48.00", reorderQty: 48}
	r, s := poCaseLineForm(t, fake, 80, "r")

	if s.pickedQPP != 24 {
		t.Fatalf("the staged line carries qpp %d, want the queue row's 24", s.pickedQPP)
	}
	if !s.caseBasis {
		t.Fatal("a queue row shipping 24 to a case must open the form in cases")
	}
	if got := s.lineInputs[poLineFieldQty].Value(); got != "2" {
		t.Errorf("the quantity row prefilled %q, want the 48 suggested units as 2 cases", got)
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "48" {
		t.Errorf("the cost row prefilled %q, want the queue row's 48.00 case price", got)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // stage as prefilled
	r = key(t, r, poRuneKey("d"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	line := fake.createdLine(t, 0)
	if got := line["quantity"]; got != float64(48) {
		t.Errorf("quantity = %v, want the 48 base units the queue suggested", got)
	}
	if got := line["unit_cost"]; got != float64(2) {
		t.Errorf("unit_cost = %v, want the per-unit 2 derived from the 48.00 case", got)
	}
}

// TestPOReorderQueue_ABulkAddCarriesTheCaseSize is the OTHER reorder path — the
// one that stages every marked row straight into the cart WITHOUT going through
// the line form. A cart line with no case size states a case order in loose
// units on the review pane and re-opens in a per-unit form under Ctrl-E, which
// is the same defect two surfaces on.
func TestPOReorderQueue_ABulkAddCarriesTheCaseSize(t *testing.T) {
	fake := &poPickFake{reorder: 1, reorderPack: 24, reorderPackageCost: "48.00", reorderQty: 48}
	r, s := poPickerAtSize(t, fake, 80, 30)
	r = key(t, r, poRuneKey("r"))
	r = key(t, r, poRuneKey("a")) // stage the whole queue without the line form

	if len(s.lines) != 1 {
		t.Fatalf("the bulk add staged %d line(s), want 1", len(s.lines))
	}
	if got := s.lines[0].qpp; got != 24 {
		t.Errorf("the staged line carries qpp %d, want the queue row's 24", got)
	}
	pane := poCaseFolded(t, s, 80)
	if !strings.Contains(pane, "×2 cs") {
		t.Errorf("the review cart does not state the bulk-added line in cases:\n%s", pane)
	}
}

// ---------------------------------------------------------------------------
// The wire contract the conversion is derived from
// ---------------------------------------------------------------------------

// TestReorderData_CarriesTheSuppliersCase decodes the two keys the reorder
// picker had no field for. It is here rather than in omsapi because what it is
// really pinning is that the TUI's conversion has something to read.
func TestReorderData_CarriesTheSuppliersCase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"suppliers":[{"id":1,"name":"Acme Supply","items":[`+
			`{"item_supplier_id":7,"item_name":"Nitrile glove, blue, L",`+
			`"suggested_quantity":48,"unit_cost":"2.00","package_cost":"48.00",`+
			`"quantity_per_package":24}]}]}`)
	}))
	defer srv.Close()

	data, err := omsapi.New(srv.URL).GetReorderData(context.Background())
	if err != nil {
		t.Fatalf("GetReorderData: %v", err)
	}
	if len(data.Suppliers) != 1 || len(data.Suppliers[0].Items) != 1 {
		t.Fatalf("reorder data = %+v, want one supplier with one item", data)
	}
	item := data.Suppliers[0].Items[0]
	if item.QuantityPerPackage != 24 {
		t.Errorf("QuantityPerPackage = %d, want the 24 on the wire", item.QuantityPerPackage)
	}
	if item.PackageCost.String() != "48.00" {
		t.Errorf("PackageCost = %q, want the 48.00 on the wire", item.PackageCost.String())
	}
}

// ---------------------------------------------------------------------------
// The bar and the Ctrl-T gate
// ---------------------------------------------------------------------------

// TestPOLineForm_TheBarNamesCtrlTExactlyWhenItActs presses the whole key space
// against every state the line form's bar CHANGES SHAPE in, which is the axis a
// derived phase roster is silent about.
//
// po_create_phase_sweep_test.go walks the poPhase iota and reaches the line form
// through a fixture whose catalog sells singles, so Ctrl-T is unnamed and inert
// there and the sweep passes without ever pressing it in a state where it acts.
// That is coverage along one axis reading as coverage along both — and the bar
// on this phase now has four shapes, three of which that sweep cannot reach.
//
// It builds the screen DIRECTLY rather than driving the picker: the phase sweep
// rebuilds the whole flow per key per probe and already runs for minutes, and
// the states under test here are states of the FORM, reachable by handing
// enterLinePhase the prefill each one is defined by.
func TestPOLineForm_TheBarNamesCtrlTExactlyWhenItActs(t *testing.T) {
	id := 42
	for _, tc := range []struct {
		name    string
		qty     int
		qpp     int
		offered bool
	}{
		// Two whole cases of 12 — the form opens in cases and the key flips to
		// units, which always converts exactly.
		{"case basis", 24, 12, true},
		// 30 units of a 12-case item: no case count, so the key could only
		// decline and the bar must not name it.
		{"unit basis, no case count", 30, 12, false},
		// 24 units after a flip to units: a whole number of cases again, so the
		// key comes back.
		{"unit basis, whole cases", 24, 12, true},
		// A vendor selling singles has one basis and nothing to move between.
		{"singles", 5, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *PurchaseOrderCreateScreen {
				s := NewPurchaseOrderCreateScreen(Deps{})
				s.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
				s.enterLinePhase(&id, nil, "Widget", tc.qty, 1.25, 0, tc.qpp)
				if tc.name == "unit basis, whole cases" {
					s.toggleEntryBasis()
				}
				return s
			}
			if got := poCaseBarNames(build().barItems(false), "Ctrl-T"); got != tc.offered {
				t.Fatalf("the bar names Ctrl-T = %v, want %v", got, tc.offered)
			}

			// Both directions, over the whole key space: a key the bar names
			// must change something, and a key it does not name must not.
			// Printable runes and the field keys are exempt in the reverse
			// direction only — this phase has a focused textinput, and typing
			// into it is what the field is FOR.
			named := map[string]bool{}
			for _, it := range build().barItems(false) {
				keys, ok := poBarKeyNames[it.Key]
				if !ok {
					t.Fatalf("bar entry %q is not in poBarKeyNames", it.Key)
				}
				for _, k := range keys {
					named[k] = true
				}
			}
			for _, k := range poKeySpace() {
				if !named[k] && (poIsPrintable(k) || poFieldKeys[k] || poFormNavAliases[k]) {
					continue
				}
				s := build()
				before := poPickerState(s)
				_, cmd := s.Update(poPhaseKeyMsg(k))
				acted := poPickerState(s) != before || poCmdActs(cmd)
				if named[k] && !acted {
					t.Errorf("the bar names %q and it changes nothing", k)
				}
				if !named[k] && acted {
					t.Errorf("%q acts and the bar does not name it", k)
				}
			}
		})
	}
}

// TestPOPickers_ACasePackedRowKeepsItsFactsAtEveryWidth is rule 5 over the
// widened facts.
//
// A case-packed catalogue row now carries "@ 3.50/unit" and "case ×24" where it
// used to carry "@ 3.50" alone, and a reorder row carries "qty 2 cs". Those are
// FACTS and never give, so what has to hold is that the row still fits the pane
// with a name and a part number at the length OMS really carries — the fixture
// every picker test used to use was "Widget 1", seven cells, which could not
// reach the bound at all.
func TestPOPickers_ACasePackedRowKeepsItsFactsAtEveryWidth(t *testing.T) {
	const mro = "Bracket, mounting, zinc-plated, heavy duty, 12-hole, left-hand"
	const partNumber = "AF-99-12-ZP-LH-HEAVY-ALT-0007"
	for _, width := range []int{80, 100, 120} {
		for _, tc := range []struct {
			name  string
			key   string
			fake  *poPickFake
			facts []string
			// price is the row's per-unit price WITHOUT its denominator. On the
			// catalogue row the price is a fact and never gives, so it is in
			// facts as well; on the reorder row it is a decoration poFitRow
			// drops from the right when the item name is long (it is gone at 80
			// columns with the MRO name above). So the rule asserted here is
			// the one that holds at every width: wherever the pane states the
			// price AT ALL it states which denominator it is in, because a bare
			// "@ 1.50" beside "qty 2 cs" reads as $1.50 a case — the reported
			// defect, one screen over.
			price string
		}{
			{"catalog", "i", &poPickFake{
				catalog: 3, catalogPack: 24, catalogPackageCost: "84.00",
				itemName: mro, itemSKU: partNumber,
			}, []string{"@ 3.50/unit", "case ×24"}, "@ 3.50"},
			{"reorder queue", "r", &poPickFake{
				reorder: 3, reorderPack: 24, reorderPackageCost: "84.00",
				reorderQty: 48, reorderName: mro,
			}, []string{"qty 2 cs"}, "@ 1.50"},
		} {
			t.Run(fmt.Sprintf("%s at %d columns", tc.name, width), func(t *testing.T) {
				r, s := poPickerAtSize(t, tc.fake, width, 30)
				r = key(t, r, poRuneKey(tc.key))
				pane := poCaseFolded(t, s, width)
				for _, want := range tc.facts {
					if !strings.Contains(pane, want) {
						t.Errorf("the clipped pane lost the fact %q:\n%s", want, pane)
					}
				}
				for rest := pane; ; {
					i := strings.Index(rest, tc.price)
					if i < 0 {
						break
					}
					rest = rest[i+len(tc.price):]
					if !strings.HasPrefix(rest, "/unit") {
						t.Errorf("a case-packed row states %q with no denominator:\n%s", tc.price, pane)
					}
				}
				// The widest pane draws every decoration, so the conditional
				// check above cannot go vacuous on a row that simply lost its
				// price everywhere.
				if width == 120 && !strings.Contains(pane, tc.price+"/unit") {
					t.Errorf("no denominated price on the widest pane, so nothing checked it:\n%s", pane)
				}
			})
		}
	}
}

// TestPOAddLine_TheOnOrderRowNamesItsUnit is the confirm frame's half of "a
// bare number beside a stated case size is the reported ambiguity".
//
// The Case row and the On order row are drawn one under the other, so on a
// vendor shipping 24 to a case a bare "24 ordered" sits directly beneath
// "1 case = 24 units" and reads as one case. The sibling statement of the same
// fact — quantityHint's "adds to the N on order" — already names its unit, and
// two surfaces disagreeing about whether a quantity is loose or packed is the
// defect this file exists to remove.
func TestPOAddLine_TheOnOrderRowNamesItsUnit(t *testing.T) {
	rows := poCaseRows()
	rows[0].onOrder, rows[0].onOrderLineID, rows[0].linePrice = 24, 9, "2.0000"
	fake := &poAddFake{rows: rows}
	r, s := poAddAt(t, fake, 80, 24)

	r = key(t, r, poRuneKey("AF-24-CASE"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAddPhaseConfirm {
		t.Fatalf("an exact supplier SKU landed on phase %v, want the confirm frame", s.phase)
	}

	// Both rows, on one frame: what a case holds, and what the order already
	// carries — in the same unit.
	poAddWantPane(t, r, "1 case = 24 units")
	poAddWantPane(t, r, "24 units ordered")
	// The repeat's outcome is the same quantity one step on, so it is named the
	// same way: "→ 48" alone would be the bare number again, on the figure the
	// operator is about to commit to.
	poAddWantPane(t, r, "48 units")
}

// TestPOReorderQueue_BothEntryPointsPriceTheRowTheSameWay pins the ONE queue
// row against the TWO keys that stage it: enter, through the line form, and
// `a`, straight into the cart.
//
// package_cost 10.00 over a case of 3 does not divide evenly, and OMS derives
// unit_cost from it by rounding to two decimals (3.33), so the row carries two
// figures that disagree by a third of a penny per unit. Pricing the bulk add
// from unit_cost while the form priced from package_cost made one row worth
// 9.99 or 10.00 depending on which key was pressed — and Ctrl-E on the
// bulk-staged line then showed a case cost the picker row disagreed with.
func TestPOReorderQueue_BothEntryPointsPriceTheRowTheSameWay(t *testing.T) {
	queue := func() *poPickFake {
		return &poPickFake{reorder: 1, reorderPack: 3, reorderPackageCost: "10.00",
			reorderUnitCost: "3.33", reorderQty: 3}
	}

	bulk := queue()
	r, _ := poPickerAtSize(t, bulk, 80, 30)
	r = key(t, r, poRuneKey("r"))
	r = key(t, r, poRuneKey("a"))                 // the whole queue, no line form
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit from review
	bulkLine := bulk.createdLine(t, 0)

	single := queue()
	r2, _ := poCaseLineForm(t, single, 80, "r")
	r2 = key(t, r2, tea.KeyMsg{Type: tea.KeyEnter}) // stage as prefilled
	r2 = key(t, r2, poRuneKey("d"))
	r2 = key(t, r2, tea.KeyMsg{Type: tea.KeyEnter})
	singleLine := single.createdLine(t, 0)

	if bulkLine["unit_cost"] != singleLine["unit_cost"] {
		t.Errorf("one queue row staged two prices: bulk %v, single-row enter %v",
			bulkLine["unit_cost"], singleLine["unit_cost"])
	}
	// And the price both of them carry is the case price divided, not the
	// server's rounded per-unit copy of it: three of 3.33 are 9.99.
	//
	// 3.3333 exactly, not 10.0/3.0: the division is decimal and quantised at
	// the four-place column unit_cost_ordered stores, so the figure the payload
	// carries is the one the line form's box showed. Asserting the binary
	// quotient here would pin a number no surface displays.
	if got := bulkLine["unit_cost"]; got != 3.3333 {
		t.Errorf("unit_cost = %v, want the 10.00 case over 3 rather than the rounded 3.33", got)
	}
}

// TestPOCaseCost_ADerivedCasePriceIsNotBinaryNoise is the money-precision half
// of "the operator confirms what the vendor charges".
//
// The case price is a per-unit figure multiplied by the case size, and a binary
// multiply of a decimal is inexact for ordinary prices: 0.05 × 12 is
// 0.6000000000000001 and 1.05 × 3 is 3.1500000000000004 in float64. Both were
// rendered at full precision into the entry box, where the add-line row's
// 14-character limit cut the first to "0.600000000000" — a price nobody typed,
// on the row this whole file exists for.
func TestPOCaseCost_ADerivedCasePriceIsNotBinaryNoise(t *testing.T) {
	// The add-line prefill: the server's own suggestion, multiplied up.
	fake := &poAddFake{rows: []poAddCatalogRow{
		{itemSupplier: 31, name: "Washer, nylon", sku: "W-1", supplierSKU: "AF-12-PENNY",
			perPackage: 12, suggestQty: 12, suggestCost: "0.0500"},
	}}
	r, s := poCaseAdd(t, fake, 80, 24, "AF-12-PENNY")
	if got := s.costIn.Value(); got != "0.6" {
		t.Errorf("case cost prefill = %q, want the 0.0500 unit price as a 0.60 case", got)
	}
	_ = r

	// The create screen's prefill, same multiply, one screen over.
	c := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	c.enterLinePhase(&id, nil, "Washer", 12, 0.05, 0, 12)
	if got := c.lineInputs[poLineFieldCost].Value(); got != "0.6" {
		t.Errorf("create-screen case cost prefill = %q, want %q", got, "0.6")
	}

	// And the Ctrl-T flip, which derives the same figure from what is typed.
	c2 := NewPurchaseOrderCreateScreen(Deps{})
	c2.enterLinePhase(&id, nil, "Bracket", 3, 0, 0, 3)
	c2.updateLinePhase(tea.KeyMsg{Type: tea.KeyCtrlT}, 0) // → unit basis
	if c2.caseBasis {
		t.Fatal("ctrl+t on a case-basis form should reach unit basis")
	}
	c2.lineInputs[poLineFieldCost].SetValue("1.05")
	c2.updateLinePhase(tea.KeyMsg{Type: tea.KeyCtrlT}, 0) // → back to cases
	if !c2.caseBasis {
		t.Fatal("ctrl+t on a whole case count should reach case basis")
	}
	if got := c2.lineInputs[poLineFieldCost].Value(); got != "3.15" {
		t.Errorf("case cost after the flip = %q, want the 1.05 unit price as a 3.15 case", got)
	}
}

// TestPOAddLine_TheFlipIsExactOnACaseSizeThatDoesNotDivide is the same round
// trip as the test above over a case size the arithmetic does NOT survive in
// binary — which is why that one could not see this: 48.00 over 24 is exactly
// 2, so a float64 divide and a decimal one agree.
//
// A vendor shipping 12 at a stored 0.0500 gives 0.6/12 = 0.049999999999999996
// in float64, and the cost row's CharLimit of 14 then CUT it to
// "0.049999999999" — a price the operator never typed, in the box they are
// being asked to confirm, and posted verbatim by the next Enter.
func TestPOAddLine_TheFlipIsExactOnACaseSizeThatDoesNotDivide(t *testing.T) {
	fake := &poAddFake{rows: []poAddCatalogRow{
		{itemSupplier: 31, name: "Washer, nylon", sku: "W-1", supplierSKU: "AF-12-PENNY",
			perPackage: 12, suggestQty: 12, suggestCost: "0.0500"},
	}}
	r, s := poCaseAdd(t, fake, 80, 30, "AF-12-PENNY")
	if got := s.costIn.Value(); got != "0.6" {
		t.Fatalf("case cost prefill = %q, want the 0.0500 unit price as a 0.60 case", got)
	}

	r = key(t, r, poCtrlT())
	if s.caseBasis {
		t.Fatal("ctrl+t did not move the rows onto the unit basis")
	}
	if got := s.costIn.Value(); got != "0.05" {
		t.Errorf("cost after the flip = %q, want the 0.05 the item is priced at", got)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.adds) != 1 {
		t.Fatalf("the add posted %d time(s), want once", len(fake.adds))
	}
	if got := fake.adds[0]["quantity"]; got != float64(12) {
		t.Errorf("quantity = %v, want the 12 units one case of 12 comes to", got)
	}
	if got := fake.adds[0]["unit_cost"]; got != "0.05" {
		t.Errorf("unit_cost = %v, want the 0.05 the item is priced at", got)
	}
}

// TestPOCreate_TheFlipIsExactOnACasePriceThatDoesNotDivide is the same fault on
// the create screen, where the box holds 20 characters and so showed the whole
// of it: a 10.00 case of 3 flipped to units drew "3.3333333333333335" and
// posted that.
//
// 3.3333 is the honest answer rather than a rounded one: unit_cost_ordered is a
// four-place decimal, so it is the figure the order will carry whatever this
// screen displays, and showing more digits than the column keeps is a screen
// stating a price the record cannot hold.
func TestPOCreate_TheFlipIsExactOnACasePriceThatDoesNotDivide(t *testing.T) {
	fake := &poPickFake{reorder: 1, reorderPack: 3, reorderPackageCost: "10.00",
		reorderUnitCost: "3.33", reorderQty: 3}
	r, s := poCaseLineForm(t, fake, 80, "r")
	if got := s.lineInputs[poLineFieldCost].Value(); got != "10" {
		t.Fatalf("case cost prefill = %q, want the queue row's 10.00 case price", got)
	}

	r = key(t, r, poCtrlT())
	if s.caseBasis {
		t.Fatal("ctrl+t did not move the rows onto the unit basis")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "3.3333" {
		t.Errorf("cost after the flip = %q, want the 10.00 case over 3 at the column OMS stores", got)
	}

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // stage the line
	r = key(t, r, poRuneKey("d"))                 // review
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit

	line := fake.createdLine(t, 0)
	if got := line["quantity"]; got != float64(3) {
		t.Errorf("quantity = %v, want the 3 base units one case of 3 comes to", got)
	}
	if got := line["unit_cost"]; got != 3.3333 {
		t.Errorf("unit_cost = %v, want 3.3333 — the box's own figure, not a binary quotient", got)
	}
}
