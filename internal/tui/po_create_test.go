package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ---------------------------------------------------------------------------
// poDeriveUnitCost — the case→unit derivation at the heart of op-7j8v.
// ---------------------------------------------------------------------------

func TestPODeriveUnitCost(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		basisCase bool
		qpp       int
		wantUnit  float64
		wantProv  bool
		wantErr   bool
	}{
		{"blank omits", "", true, 12, 0, false, false},
		{"whitespace omits", "   ", true, 12, 0, false, false},
		{"case cost clean divide", "30", true, 12, 2.5, true, false},
		{"case cost with cents", "15.00", true, 10, 1.5, true, false},
		{"unit basis passes through", "1.25", false, 10, 1.25, true, false},
		{"unit basis ignores qpp", "3", false, 12, 3, true, false},
		{"zero is a valid cost", "0", true, 12, 0, true, false},
		{"case cost qpp<1 treated as 1", "9.99", true, 0, 9.99, true, false},
		{"negative rejected", "-5", true, 12, 0, false, true},
		{"garbage rejected", "abc", true, 12, 0, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			unit, prov, err := poDeriveUnitCost(tc.raw, tc.basisCase, tc.qpp)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if prov != tc.wantProv {
				t.Fatalf("provided = %v, want %v", prov, tc.wantProv)
			}
			if !tc.wantErr && math.Abs(unit-tc.wantUnit) > 1e-12 {
				t.Fatalf("unit = %v, want %v", unit, tc.wantUnit)
			}
		})
	}
}

// TestPODeriveUnitCost_FullPrecision guards the "don't round to cents" contract:
// an odd case size must derive an exact quotient, not a cent-rounded value.
func TestPODeriveUnitCost_FullPrecision(t *testing.T) {
	unit, prov, err := poDeriveUnitCost("10", true, 3)
	if err != nil || !prov {
		t.Fatalf("poDeriveUnitCost: err=%v prov=%v", err, prov)
	}
	if want := 10.0 / 3.0; math.Abs(unit-want) > 1e-12 {
		t.Fatalf("unit = %v, want %v (full precision)", unit, want)
	}
	// It must NOT be the cent-rounded 3.33.
	if math.Abs(unit-3.33) < 1e-9 {
		t.Fatalf("unit was rounded to cents (%v); derivation must keep full precision", unit)
	}
}

// ---------------------------------------------------------------------------
// Display formatting helpers.
// ---------------------------------------------------------------------------

func TestPOFormatCost(t *testing.T) {
	cases := map[float64]string{
		15:   "15",
		12.5: "12.5",
		2.5:  "2.5",
		0:    "0",
		1.25: "1.25",
	}
	for in, want := range cases {
		if got := poFormatCost(in); got != want {
			t.Errorf("poFormatCost(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestPODisplayMoney(t *testing.T) {
	cases := map[float64]string{
		3.0:        "3.00",
		2.5:        "2.50",
		1.25:       "1.25",
		0.1:        "0.10",
		10.0 / 3.0: "3.3333",
	}
	for in, want := range cases {
		if got := poDisplayMoney(in); got != want {
			t.Errorf("poDisplayMoney(%v) = %q, want %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// enterLinePhase — case-packed prefill + basis default.
// ---------------------------------------------------------------------------

func TestPOEnterLinePhase_CasePackedPrefillsPackageCost(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 3, 1.25, 15.00, 12)

	if !s.costBasisCase {
		t.Errorf("case-packed line should default to case-cost basis")
	}
	if s.pickedQPP != 12 {
		t.Errorf("pickedQPP = %d, want 12", s.pickedQPP)
	}
	// package_cost (15.00) wins the prefill over unit_cost×qpp.
	if got := s.lineInputs[poLineFieldCost].Value(); got != "15" {
		t.Errorf("case cost prefill = %q, want %q", got, "15")
	}
	if got := s.costFieldLabel(); got != "Case cost" {
		t.Errorf("cost label = %q, want %q", got, "Case cost")
	}
}

func TestPOEnterLinePhase_CasePackedFallsBackToUnitTimesQPP(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 7
	// No package_cost (0) → prefill from unit_cost × qpp = 1.25 × 12 = 15.
	s.enterLinePhase(&id, nil, "Widget", 1, 1.25, 0, 12)
	if got := s.lineInputs[poLineFieldCost].Value(); got != "15" {
		t.Errorf("case cost fallback prefill = %q, want %q", got, "15")
	}
}

func TestPOEnterLinePhase_CasePackedNoCatalogCostLeavesBlank(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 7
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	if got := s.lineInputs[poLineFieldCost].Value(); got != "" {
		t.Errorf("case cost prefill = %q, want empty when catalog has no cost", got)
	}
}

// ---------------------------------------------------------------------------
// lineFields — cost field visibility by line kind.
// ---------------------------------------------------------------------------

func TestPOLineFields_CaseVsSinglePackVsAsset(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 1

	// Case-packed inventory: cost field present.
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("case-packed inventory line should include the cost field")
	}

	// Single-pack inventory: cost field present too, as an optional override —
	// the gap that left an operator unable to correct a $0 catalog line (sc-gnzw).
	s.enterLinePhase(&id, nil, "Bolt", 1, 0, 0, 1)
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("single-pack inventory line should include the cost field")
	}

	// Freeform: cost field present (unchanged).
	s.enterLinePhase(nil, nil, "", 0, 0, 0, 0)
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("freeform line should include the cost field")
	}
}

// TestPOCatalogCost_PlaceholderAndNoteSayBlankMeansCatalog: the cost field on a
// catalog line has to READ as optional, or an operator who wants catalog pricing
// won't know clearing it is the way to ask for it.
func TestPOCatalogCost_PlaceholderAndNoteSayBlankMeansCatalog(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 1

	s.enterLinePhase(&id, nil, "Bolt", 1, 0, 0, 1)
	if got := s.lineInputs[poLineFieldCost].Placeholder; !strings.Contains(got, "optional") {
		t.Errorf("catalog-line placeholder = %q, want it to read as optional", got)
	}
	if out := s.renderLinePhase(); !strings.Contains(out, "Cost is optional") {
		t.Errorf("catalog line should explain the optional cost:\n%s", out)
	}

	// Case-packed catalog line: same promise, in the case basis.
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	if got := s.lineInputs[poLineFieldCost].Placeholder; !strings.HasPrefix(got, "case cost") ||
		!strings.Contains(got, "optional") {
		t.Errorf("case-packed catalog placeholder = %q, want an optional case-cost hint", got)
	}
	if out := s.renderLinePhase(); !strings.Contains(out, "Cost is optional") {
		t.Errorf("case-packed catalog line should explain the optional cost:\n%s", out)
	}

	// Asset / freeform lines REQUIRE a cost, so they keep the example hint and
	// must not promise catalog pricing that branch never applies.
	s.enterLinePhase(nil, nil, "Rags", 1, 0, 0, 0)
	if got := s.lineInputs[poLineFieldCost].Placeholder; !strings.Contains(got, "e.g.") {
		t.Errorf("freeform placeholder = %q, want the example hint for a required cost", got)
	}
	if out := s.renderLinePhase(); strings.Contains(out, "Cost is optional") {
		t.Errorf("freeform line should not offer catalog pricing:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// addLine — the payload the cart carries (finalize copies s.lines verbatim).
// ---------------------------------------------------------------------------

func TestPOAddLine_CaseCostDerivesUnitCost(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 5, 1.25, 15.00, 12)
	// Operator overrides the case cost to $30 → unit_cost = 30/12 = 2.5.
	s.lineInputs[poLineFieldCost].SetValue("30")
	s.addLine()

	line := lastLine(t, s)
	if line.ItemSupplierID == nil || *line.ItemSupplierID != 42 {
		t.Fatalf("item_supplier_id = %v, want 42", line.ItemSupplierID)
	}
	if line.Quantity != 5 {
		t.Errorf("quantity = %d, want 5", line.Quantity)
	}
	if line.UnitCost == nil {
		t.Fatalf("unit_cost should be sent for a case-packed line with a cost")
	}
	if math.Abs(*line.UnitCost-2.5) > 1e-12 {
		t.Errorf("unit_cost = %v, want 2.5 (30 / 12)", *line.UnitCost)
	}
}

func TestPOAddLine_CaseCostFullPrecisionOddCase(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 3)
	s.lineInputs[poLineFieldCost].SetValue("10") // $10/case ÷ 3
	s.addLine()

	line := lastLine(t, s)
	if line.UnitCost == nil {
		t.Fatalf("unit_cost missing")
	}
	if want := 10.0 / 3.0; math.Abs(*line.UnitCost-want) > 1e-12 {
		t.Errorf("unit_cost = %v, want %v (full precision, no cent-drift)", *line.UnitCost, want)
	}
}

func TestPOAddLine_UnitBasisAfterToggle(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	s.toggleCostBasis() // switch to per-unit entry
	if s.costBasisCase {
		t.Fatalf("toggle should have switched to unit basis")
	}
	s.lineInputs[poLineFieldCost].SetValue("3.00")
	s.addLine()

	line := lastLine(t, s)
	if line.UnitCost == nil || math.Abs(*line.UnitCost-3.0) > 1e-12 {
		t.Fatalf("unit_cost = %v, want 3.0 (unit basis, no division)", line.UnitCost)
	}
}

func TestPOAddLine_BlankCaseCostOmitsUnitCost(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	s.lineInputs[poLineFieldCost].SetValue("") // leave blank → backend derives
	s.addLine()

	line := lastLine(t, s)
	if line.UnitCost != nil {
		t.Errorf("unit_cost = %v, want nil (blank ⇒ omit, backend derives)", *line.UnitCost)
	}
}

// TestPOAddLine_SinglePackCatalogCostOverride is the headline of sc-gnzw: a
// typed cost on a single-pack catalog line reaches the payload as the per-unit
// override, and clearing the field hands pricing back to the catalog.
func TestPOAddLine_SinglePackCatalogCostOverride(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	// The catalog price seeds the field; the operator types over it.
	s.enterLinePhase(&id, nil, "Bolt", 100, 0.10, 0, 1)
	if got := s.lineInputs[poLineFieldCost].Value(); got != "0.1" {
		t.Errorf("cost prefill = %q, want the catalog price %q", got, "0.1")
	}
	s.lineInputs[poLineFieldCost].SetValue("9.99")
	s.addLine()

	line := lastLine(t, s)
	if line.UnitCost == nil || math.Abs(*line.UnitCost-9.99) > 1e-12 {
		t.Errorf("unit_cost = %v, want the 9.99 override", line.UnitCost)
	}
	if line.ItemSupplierID == nil || *line.ItemSupplierID != 42 {
		t.Errorf("item_supplier_id = %v, want 42", line.ItemSupplierID)
	}

	// Cleared → omitted, so the backend prices the line from the catalog.
	s2 := NewPurchaseOrderCreateScreen(Deps{})
	s2.enterLinePhase(&id, nil, "Bolt", 100, 0.10, 0, 1)
	s2.lineInputs[poLineFieldCost].SetValue("")
	s2.addLine()
	if got := lastLine(t, s2).UnitCost; got != nil {
		t.Errorf("cleared cost = %v, want nil (catalog price)", *got)
	}

	// A negative override is refused, same as on any other line.
	s3 := NewPurchaseOrderCreateScreen(Deps{})
	s3.enterLinePhase(&id, nil, "Bolt", 100, 0, 0, 1)
	s3.lineInputs[poLineFieldCost].SetValue("-1")
	s3.addLine()
	if len(s3.lines) != 0 || !strings.Contains(s3.errMsg, "unit cost") {
		t.Errorf("negative override should be rejected; lines=%d err=%q", len(s3.lines), s3.errMsg)
	}
}

// TestPOAddLine_CatalogCostZeroIsAnExplicitOverride: 0 is a real value the
// backend honours (it checks unit_cost is not None), so a typed 0 must survive
// as an explicit $0 rather than being treated as "blank".
func TestPOAddLine_CatalogCostZeroIsAnExplicitOverride(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Freebie", 1, 5.00, 0, 1)
	s.lineInputs[poLineFieldCost].SetValue("0")
	s.addLine()

	line := lastLine(t, s)
	if line.UnitCost == nil || *line.UnitCost != 0 {
		t.Errorf("unit_cost = %v, want an explicit 0", line.UnitCost)
	}
}

func TestPOAddLine_AssetAndFreeformUnchanged(t *testing.T) {
	// Asset line: single per-unit cost, unchanged.
	s := NewPurchaseOrderCreateScreen(Deps{})
	assetID := "asset-uuid"
	s.enterLinePhase(nil, &assetID, "Drill", 1, 0, 0, 0)
	s.lineInputs[poLineFieldCost].SetValue("99.99")
	s.addLine()
	line := lastLine(t, s)
	if line.AssetID == nil || *line.AssetID != "asset-uuid" {
		t.Fatalf("asset_id = %v, want asset-uuid", line.AssetID)
	}
	if line.UnitCost == nil || math.Abs(*line.UnitCost-99.99) > 1e-12 {
		t.Errorf("asset unit_cost = %v, want 99.99", line.UnitCost)
	}

	// Freeform line: description + per-unit cost, unchanged.
	s2 := NewPurchaseOrderCreateScreen(Deps{})
	s2.enterLinePhase(nil, nil, "Shop rags", 3, 0, 0, 0)
	s2.lineInputs[poLineFieldCost].SetValue("1.50")
	s2.addLine()
	line2 := lastLine(t, s2)
	if line2.Description != "Shop rags" {
		t.Errorf("description = %q, want %q", line2.Description, "Shop rags")
	}
	if line2.UnitCost == nil || math.Abs(*line2.UnitCost-1.5) > 1e-12 {
		t.Errorf("freeform unit_cost = %v, want 1.5", line2.UnitCost)
	}
}

func TestPOAddLine_RejectsNegativeCaseCost(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	s.lineInputs[poLineFieldCost].SetValue("-5")
	s.addLine()
	if len(s.lines) != 0 {
		t.Fatalf("negative case cost should not add a line")
	}
	if !strings.Contains(s.errMsg, "case cost") {
		t.Errorf("errMsg = %q, want it to mention 'case cost'", s.errMsg)
	}
}

// ---------------------------------------------------------------------------
// toggleCostBasis — value conversion + no-op guard.
// ---------------------------------------------------------------------------

func TestPOToggleCostBasis_ConvertsValueBothWays(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 15.00, 12) // case basis, "15"

	s.toggleCostBasis() // case → unit: 15 / 12 = 1.25
	if s.costBasisCase {
		t.Fatalf("expected unit basis after first toggle")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "1.25" {
		t.Errorf("unit value after toggle = %q, want %q", got, "1.25")
	}

	s.toggleCostBasis() // unit → case: 1.25 × 12 = 15
	if !s.costBasisCase {
		t.Fatalf("expected case basis after second toggle")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "15" {
		t.Errorf("case value after round-trip = %q, want %q", got, "15")
	}
}

func TestPOToggleCostBasis_NoOpForNonCaseLine(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	// Single-pack inventory: toggle must do nothing (basis stays unit).
	s.enterLinePhase(&id, nil, "Bolt", 1, 0, 0, 1)
	s.toggleCostBasis()
	if s.costBasisCase {
		t.Errorf("single-pack line should not enter case basis")
	}

	// Freeform: toggle must do nothing.
	s.enterLinePhase(nil, nil, "Rag", 1, 0, 0, 0)
	s.toggleCostBasis()
	if s.costBasisCase {
		t.Errorf("freeform line should not enter case basis")
	}
}

// TestPOCostDerivationHint confirms the operator-facing hint echoes both bases.
func TestPOCostDerivationHint(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12)
	s.lineInputs[poLineFieldCost].SetValue("30")
	hint := s.costDerivationHint()
	if !strings.Contains(hint, "30.00/case") || !strings.Contains(hint, "2.50/unit") {
		t.Errorf("hint = %q, want it to show $30.00/case and $2.50/unit", hint)
	}

	// Non-case line: no hint.
	s.enterLinePhase(nil, nil, "Rag", 1, 0, 0, 0)
	if got := s.costDerivationHint(); got != "" {
		t.Errorf("non-case hint = %q, want empty", got)
	}
}

// TestPORenderLinePhase_CasePacked exercises the render path: a case-packed
// line shows the "Case cost:" label and the derivation hint; toggling flips the
// label to "Unit cost:".
func TestPORenderLinePhase_CasePacked(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 30.00, 12)

	out := s.renderLinePhase()
	if !strings.Contains(out, "Case cost:") {
		t.Errorf("case-packed line should render 'Case cost:' label:\n%s", out)
	}
	if !strings.Contains(out, "/case") || !strings.Contains(out, "/unit") {
		t.Errorf("case-packed line should render the derivation hint:\n%s", out)
	}
	// A case-packed line is still a catalog line: clearing the cost falls back
	// to catalog pricing there too, so the note belongs on it.
	if !strings.Contains(out, "Cost is optional") {
		t.Errorf("case-packed catalog line should show the optional-cost note:\n%s", out)
	}

	s.toggleCostBasis()
	if out := s.renderLinePhase(); !strings.Contains(out, "Unit cost:") {
		t.Errorf("after toggle, line should render 'Unit cost:' label:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Editing a staged cart line in place (ctrl+e from review — sc-fo1s).
// ---------------------------------------------------------------------------

// enterReviewAt stages nothing; it drops an already-built cart into the review
// phase with the given line highlighted, mirroring the 'd' done→review path.
func enterReviewAt(s *PurchaseOrderCreateScreen, cursor int) {
	s.phase = poPhaseReview
	s.reviewCursor = cursor
	s.poNotes.Focus()
}

// TestPOEditLine_UpdatesInPlace: ctrl+e opens the highlighted line pre-filled;
// editing desc/qty/cost writes back to s.lines[i] with the cart length UNCHANGED,
// the label re-derived, review re-entered with the same line highlighted, and a
// sibling line left untouched.
func TestPOEditLine_UpdatesInPlace(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.enterLinePhase(nil, nil, "Rags", 2, 0, 0, 0)
	s.lineInputs[poLineFieldCost].SetValue("1.50")
	s.addLine()
	s.enterLinePhase(nil, nil, "Gloves", 5, 0, 0, 0)
	s.lineInputs[poLineFieldCost].SetValue("3.00")
	s.addLine()
	if len(s.lines) != 2 {
		t.Fatalf("setup: len(lines) = %d, want 2", len(s.lines))
	}

	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poPhaseLine {
		t.Fatalf("ctrl+e should open the line form; phase = %v", s.phase)
	}
	if s.editIndex != 0 {
		t.Fatalf("editIndex = %d, want 0", s.editIndex)
	}
	// Pre-filled from line 0.
	if got := s.lineInputs[poLineFieldDesc].Value(); got != "Rags" {
		t.Errorf("desc prefill = %q, want %q", got, "Rags")
	}
	if got := s.lineInputs[poLineFieldQty].Value(); got != "2" {
		t.Errorf("qty prefill = %q, want %q", got, "2")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "1.5" {
		t.Errorf("cost prefill = %q, want %q", got, "1.5")
	}

	// Change every field and save.
	s.lineInputs[poLineFieldDesc].SetValue("Shop rags")
	s.lineInputs[poLineFieldQty].SetValue("9")
	s.lineInputs[poLineFieldCost].SetValue("2.25")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})

	if len(s.lines) != 2 {
		t.Fatalf("edit must not change cart length; len = %d, want 2", len(s.lines))
	}
	if s.editIndex != -1 {
		t.Errorf("editIndex = %d, want -1 after save", s.editIndex)
	}
	if s.phase != poPhaseReview {
		t.Errorf("phase = %v, want review after save", s.phase)
	}
	if s.reviewCursor != 0 {
		t.Errorf("reviewCursor = %d, want 0 (edited line stays highlighted)", s.reviewCursor)
	}
	got := s.lines[0]
	if got.item.Description != "Shop rags" || got.item.Quantity != 9 {
		t.Errorf("line 0 = %+v, want desc 'Shop rags' qty 9", got.item)
	}
	if got.item.UnitCost == nil || math.Abs(*got.item.UnitCost-2.25) > 1e-12 {
		t.Errorf("line 0 unit_cost = %v, want 2.25", got.item.UnitCost)
	}
	if got.label != "Shop rags" {
		t.Errorf("label = %q, want re-derived 'Shop rags'", got.label)
	}
	if s.lines[1].item.Description != "Gloves" || s.lines[1].item.Quantity != 5 {
		t.Errorf("sibling line 1 changed: %+v", s.lines[1].item)
	}
}

// TestPOEditLine_EscCancelsUnchanged: esc from an edit returns to review with
// the line untouched, and editIndex is cleared so it can never leak into a later
// add (the next fresh add appends rather than replacing the edited slot).
func TestPOEditLine_EscCancelsUnchanged(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.enterLinePhase(nil, nil, "Rags", 2, 0, 0, 0)
	s.lineInputs[poLineFieldCost].SetValue("1.50")
	s.addLine()

	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	// Mutate the inputs, then cancel.
	s.lineInputs[poLineFieldDesc].SetValue("WRONG")
	s.lineInputs[poLineFieldQty].SetValue("999")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEsc})

	if s.phase != poPhaseReview {
		t.Fatalf("esc from an edit should return to review; phase = %v", s.phase)
	}
	if s.editIndex != -1 {
		t.Errorf("editIndex = %d, want -1 after esc-cancel", s.editIndex)
	}
	if len(s.lines) != 1 {
		t.Fatalf("esc must not change cart length; len = %d, want 1", len(s.lines))
	}
	if s.lines[0].item.Description != "Rags" || s.lines[0].item.Quantity != 2 {
		t.Errorf("line should be unchanged; got %+v", s.lines[0].item)
	}

	// No leak: a fresh add after the cancelled edit appends a new line.
	s.enterLinePhase(nil, nil, "New line", 1, 0, 0, 0)
	if s.editIndex != -1 {
		t.Errorf("enterLinePhase must reset editIndex to -1 for an add; got %d", s.editIndex)
	}
	s.lineInputs[poLineFieldCost].SetValue("2.00")
	s.addLine()
	if len(s.lines) != 2 {
		t.Errorf("fresh add after cancel should append; len = %d, want 2", len(s.lines))
	}
	if s.lines[0].item.Description != "Rags" {
		t.Errorf("original line 0 overwritten by leaked editIndex: %+v", s.lines[0].item)
	}
}

// TestPOEditLine_CasePackedRoundTrips: editing a case-packed line defaults back
// to case basis with the case cost pre-filled (unit × qpp), and toggling to unit
// basis still derives the identical unit_cost on save.
func TestPOEditLine_CasePackedRoundTrips(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Widget", 5, 0, 0, 12)
	s.lineInputs[poLineFieldCost].SetValue("30") // $30/case ÷ 12 = 2.5/unit
	s.addLine()
	if s.lines[0].qpp != 12 {
		t.Fatalf("staged qpp = %d, want 12 (needed to round-trip case basis)", s.lines[0].qpp)
	}
	if s.lines[0].item.UnitCost == nil {
		t.Fatalf("setup: staged line should carry a unit_cost")
	}
	unit0 := *s.lines[0].item.UnitCost

	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !s.costBasisCase {
		t.Errorf("case-packed edit should default to case basis")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "30" {
		t.Errorf("case cost prefill = %q, want %q (unit × qpp)", got, "30")
	}
	// ctrl+t → unit basis shows the derived unit cost; save still derives the same.
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyCtrlT})
	if s.costBasisCase {
		t.Errorf("ctrl+t should switch to unit basis")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "2.5" {
		t.Errorf("unit prefill after toggle = %q, want %q", got, "2.5")
	}
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter}) // save

	if len(s.lines) != 1 {
		t.Fatalf("edit changed cart length: %d", len(s.lines))
	}
	if s.lines[0].item.UnitCost == nil || math.Abs(*s.lines[0].item.UnitCost-unit0) > 1e-12 {
		t.Errorf("unit_cost after round-trip = %v, want %v", s.lines[0].item.UnitCost, unit0)
	}
	if math.Abs(unit0-2.5) > 1e-12 {
		t.Errorf("sanity: unit0 = %v, want 2.5", unit0)
	}
}

// TestPOEditLine_RestoresSourceFields: editing restores each line source so the
// right fields render — single-pack inventory shows the per-unit cost field,
// case-packed shows it in case basis, and an asset line shows the per-unit one.
func TestPOEditLine_RestoresSourceFields(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 7
	s.enterLinePhase(&id, nil, "Bolt", 3, 0, 0, 1) // single-pack inventory
	s.addLine()
	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12) // case-packed inventory
	s.lineInputs[poLineFieldCost].SetValue("24")
	s.addLine()
	assetID := "asset-uuid"
	s.enterLinePhase(nil, &assetID, "Drill", 1, 0, 0, 0) // asset
	s.lineInputs[poLineFieldCost].SetValue("99")
	s.addLine()

	s.poNotes.Focus()

	// Single-pack line: per-unit cost field, item-supplier source restored.
	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("editing a single-pack inventory line should show the cost field")
	}
	if s.costBasisCase {
		t.Errorf("a single-pack line has no case basis to enter")
	}
	if s.pickedItemSup == nil || *s.pickedItemSup != 7 {
		t.Errorf("edit should restore the item-supplier source; got %v", s.pickedItemSup)
	}
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEsc})

	// Case-packed line: cost field shown, case basis.
	enterReviewAt(s, 1)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("editing a case-packed line should show the cost field")
	}
	if !s.costBasisCase {
		t.Errorf("editing a case-packed line should default to case basis")
	}
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEsc})

	// Asset line: cost field shown, asset source restored.
	enterReviewAt(s, 2)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("editing an asset line should show the cost field")
	}
	if s.pickedAssetID == nil || *s.pickedAssetID != "asset-uuid" {
		t.Errorf("edit should restore the asset source; got %v", s.pickedAssetID)
	}
}

// TestPOEditLine_HelpAndTitleReadAsEditing: the review footer advertises ctrl+e,
// and while editing the line-phase help + title read as EDITING, not adding.
func TestPOEditLine_HelpAndTitleReadAsEditing(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.enterLinePhase(nil, nil, "Rags", 2, 0, 0, 0)
	s.lineInputs[poLineFieldCost].SetValue("1.50")
	s.addLine()

	s.phase = poPhaseReview
	if help := s.helpText(); !strings.Contains(help, "ctrl+e") {
		t.Errorf("review help should advertise ctrl+e edit; got %q", help)
	}

	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	help := s.helpText()
	if !strings.Contains(strings.ToLower(help), "edit") || strings.Contains(help, "add to cart") {
		t.Errorf("line help while editing should read as editing; got %q", help)
	}
	if out := s.renderLinePhase(); !strings.Contains(out, "Editing line 1 of 1") {
		t.Errorf("render should show 'Editing line 1 of 1':\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Bulk-adding a supplier's reorder queue (sc-ytr5 gap 2 — the ~30-keystroke fix)
// ---------------------------------------------------------------------------

// reorderItem builds one reorder_data suggestion row. supID <= 0 means the row
// carries no item_supplier_id, which forces the freeform line shape.
func reorderItem(name string, supID, qty int, cost string) omsapi.ReorderDataItem {
	it := omsapi.ReorderDataItem{
		ItemName:          name,
		SuggestedQuantity: qty,
		UnitCost:          omsapi.DecimalString(cost),
	}
	if supID > 0 {
		id := supID
		it.ItemSupplierID = &id
	}
	return it
}

// reorderScreen drops a screen straight into the reorder picker with a loaded
// list, mirroring the state after loadReorderItemsForSupplier resolves.
func reorderScreen(items ...omsapi.ReorderDataItem) *PurchaseOrderCreateScreen {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	s.phase = poPhaseReorderPick
	s.reorderItems = items
	s.reorderCursor = 0
	return s
}

// TestPOReorderAddAll_SeedsEveryItem: one 'a' turns a 15-item reorder queue into
// 15 cart lines seeded with each row's suggested_quantity, landing in the review
// cart so individual lines can be adjusted.
func TestPOReorderAddAll_SeedsEveryItem(t *testing.T) {
	items := make([]omsapi.ReorderDataItem, 15)
	for i := range items {
		items[i] = reorderItem(fmt.Sprintf("Item %d", i), 100+i, i+2, "1.25")
	}
	s := reorderScreen(items...)

	s.updateReorderPickPhase(runeKey('a'))

	if len(s.lines) != 15 {
		t.Fatalf("add-all staged %d line(s), want 15", len(s.lines))
	}
	if s.phase != poPhaseReview {
		t.Errorf("phase = %v, want review after a bulk add", s.phase)
	}
	for i, l := range s.lines {
		if want := i + 2; l.item.Quantity != want {
			t.Errorf("line %d quantity = %d, want %d (suggested_quantity)", i, l.item.Quantity, want)
		}
		if l.item.ItemSupplierID == nil || *l.item.ItemSupplierID != 100+i {
			t.Errorf("line %d item_supplier_id = %v, want %d", i, l.item.ItemSupplierID, 100+i)
		}
		if want := fmt.Sprintf("Item %d", i); l.label != want || l.item.Description != want {
			t.Errorf("line %d label/desc = %q/%q, want %q", i, l.label, l.item.Description, want)
		}
	}
	// Cursor parks on the first line of the batch so review opens at its top.
	if s.reviewCursor != 0 {
		t.Errorf("reviewCursor = %d, want 0 (top of the added batch)", s.reviewCursor)
	}
}

// TestPOReorderAddAll_QuantityFloor: a row with no suggested quantity still
// stages a orderable line (qty 1), matching the single-row prefill.
func TestPOReorderAddAll_QuantityFloor(t *testing.T) {
	s := reorderScreen(reorderItem("Zero", 11, 0, "1.00"))
	s.updateReorderPickPhase(runeKey('a'))
	if len(s.lines) != 1 || s.lines[0].item.Quantity != 1 {
		t.Fatalf("lines = %+v, want one line with quantity 1", s.lines)
	}
}

// TestPOReorderAddAll_CostPolicyMatchesTheLineForm: a bulk add must produce
// exactly what accepting the single-row form's prefill produces. The form seeds
// a catalog line's cost field from the row's unit_cost, so the bulk line carries
// it too; a row whose catalog cost is unset stays blank rather than pinning $0,
// which leaves the backend pricing it. A row with no item_supplier_id can only
// be freeform, whose backend branch REQUIRES a cost, so it always carries one.
func TestPOReorderAddAll_CostPolicyMatchesTheLineForm(t *testing.T) {
	s := reorderScreen(
		reorderItem("Catalog bolt", 11, 4, "2.50"),
		reorderItem("Orphan widget", 0, 3, "7.75"),
		reorderItem("Unpriced nut", 12, 2, "0.00"),
	)
	s.updateReorderPickPhase(runeKey('a'))
	if len(s.lines) != 3 {
		t.Fatalf("staged %d line(s), want 3", len(s.lines))
	}
	if s.lines[0].item.UnitCost == nil || math.Abs(*s.lines[0].item.UnitCost-2.50) > 1e-12 {
		t.Errorf("catalog line unit_cost = %v, want the row's 2.50", s.lines[0].item.UnitCost)
	}
	if s.lines[1].item.ItemSupplierID != nil {
		t.Errorf("row without item_supplier_id must stage as freeform, got %v", s.lines[1].item.ItemSupplierID)
	}
	if s.lines[1].item.UnitCost == nil || math.Abs(*s.lines[1].item.UnitCost-7.75) > 1e-12 {
		t.Errorf("freeform line unit_cost = %v, want 7.75 (its branch requires one)", s.lines[1].item.UnitCost)
	}
	if s.lines[2].item.UnitCost != nil {
		t.Errorf("unpriced catalog line unit_cost = %v, want nil (backend prices it)", *s.lines[2].item.UnitCost)
	}

	// Same rows through the single-row form: staging line 0 by hand lands on the
	// same payload the bulk add produced.
	s2 := reorderScreen(reorderItem("Catalog bolt", 11, 4, "2.50"))
	s2.updateReorderPickPhase(tea.KeyMsg{Type: tea.KeyEnter})
	s2.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})
	if len(s2.lines) != 1 {
		t.Fatalf("single-row path staged %d line(s), want 1 (err %q)", len(s2.lines), s2.errMsg)
	}
	if got, want := s2.lines[0].item.UnitCost, s.lines[0].item.UnitCost; got == nil || want == nil ||
		math.Abs(*got-*want) > 1e-12 {
		t.Errorf("single-row unit_cost = %v, want the bulk add's %v", got, want)
	}
}

// TestPOReorderMultiSelect_AddsOnlyMarkedRows: space marks rows, enter stages
// exactly those in list order, and the marks are cleared afterward.
func TestPOReorderMultiSelect_AddsOnlyMarkedRows(t *testing.T) {
	s := reorderScreen(
		reorderItem("A", 11, 1, "1.00"),
		reorderItem("B", 12, 2, "2.00"),
		reorderItem("C", 13, 3, "3.00"),
	)
	// Mark C (bottom-up, so the add must re-sort into list order), then A.
	s.updateReorderPickPhase(runeKey('j'))
	s.updateReorderPickPhase(runeKey('j'))
	s.updateReorderPickPhase(runeKey(' '))
	s.updateReorderPickPhase(runeKey('k'))
	s.updateReorderPickPhase(runeKey('k'))
	s.updateReorderPickPhase(runeKey(' '))
	if len(s.reorderSelected) != 2 {
		t.Fatalf("marked %d row(s), want 2", len(s.reorderSelected))
	}
	// Marks render as checkboxes so they survive the highlight moving away.
	if out := s.renderReorderPick(); !strings.Contains(out, "[x] A") || !strings.Contains(out, "[ ] B") {
		t.Errorf("marked rows should render checked:\n%s", out)
	}

	s.updateReorderPickPhase(tea.KeyMsg{Type: tea.KeyEnter})

	if len(s.lines) != 2 {
		t.Fatalf("staged %d line(s), want 2 (only the marked rows)", len(s.lines))
	}
	if s.lines[0].label != "A" || s.lines[1].label != "C" {
		t.Errorf("staged %q,%q — want A,C in list order", s.lines[0].label, s.lines[1].label)
	}
	if len(s.reorderSelected) != 0 {
		t.Errorf("marks should clear after the add; %d left", len(s.reorderSelected))
	}
	if s.phase != poPhaseReview {
		t.Errorf("phase = %v, want review after a bulk add", s.phase)
	}
}

// TestPOReorderSpaceToggle_Unmarks: space is a toggle, not a one-way set.
func TestPOReorderSpaceToggle_Unmarks(t *testing.T) {
	s := reorderScreen(reorderItem("A", 11, 1, "1.00"))
	s.updateReorderPickPhase(runeKey(' '))
	s.updateReorderPickPhase(runeKey(' '))
	if len(s.reorderSelected) != 0 {
		t.Fatalf("second space should unmark the row; %d still marked", len(s.reorderSelected))
	}
	// With nothing marked, enter falls back to the single-row line form.
	s.updateReorderPickPhase(tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poPhaseLine {
		t.Errorf("unmarked enter should open the line form; phase = %v", s.phase)
	}
	if len(s.lines) != 0 {
		t.Errorf("the single-row path stages nothing until the form is accepted; %d line(s)", len(s.lines))
	}
}

// TestPOReorderAddAll_EmptyListIsRefused: 'a' on an empty queue reports rather
// than silently jumping to an empty review cart.
func TestPOReorderAddAll_EmptyListIsRefused(t *testing.T) {
	s := reorderScreen()
	s.updateReorderPickPhase(runeKey('a'))
	if len(s.lines) != 0 {
		t.Fatalf("empty add-all staged %d line(s), want 0", len(s.lines))
	}
	if s.phase != poPhaseReorderPick {
		t.Errorf("phase = %v, want to stay on the picker", s.phase)
	}
	// Reported in the picker's own BODY note, where its j / k / space / enter
	// siblings answer, and NOT through the screen-level failure line: that line
	// carries a failed submit's headline plus the raw OMS body underneath it,
	// so a validation sentence written into it inherits whatever detail the
	// last failure left behind.
	if !strings.Contains(s.reorderNote.text, "nothing to add") {
		t.Errorf("reorder note = %q, want it to explain there is nothing to add", s.reorderNote.text)
	}
	if s.errMsg != "" {
		t.Errorf("the screen-level failure line was used for a picker decline: %q", s.errMsg)
	}
}

// TestPOReorderMarksClearOnReload: marks index into the list they were made
// against, so a reload must drop them rather than re-point them at new rows.
func TestPOReorderMarksClearOnReload(t *testing.T) {
	s := reorderScreen(reorderItem("A", 11, 1, "1.00"), reorderItem("B", 12, 1, "1.00"))
	s.updateReorderPickPhase(runeKey(' '))
	if len(s.reorderSelected) != 1 {
		t.Fatalf("setup: expected 1 mark, got %d", len(s.reorderSelected))
	}
	s.handlePickerLoaded(poReorderItemsLoadedMsg{supplierID: s.supplierID, items: []omsapi.ReorderDataItem{reorderItem("C", 13, 1, "1.00")}})
	if len(s.reorderSelected) != 0 {
		t.Errorf("reload should clear marks; %d left", len(s.reorderSelected))
	}
}

// ---------------------------------------------------------------------------
// Editing / removing ANY staged line while still building (sc-ytr5 gap 1)
// ---------------------------------------------------------------------------

// buildThreeLineCart stages three freeform lines and leaves the screen in the
// source chooser, exactly where addLine returns after each add.
func buildThreeLineCart(t *testing.T) *PurchaseOrderCreateScreen {
	t.Helper()
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	for _, name := range []string{"First", "Second", "Third"} {
		s.enterLinePhase(nil, nil, name, 1, 0, 0, 0)
		s.lineInputs[poLineFieldCost].SetValue("1.00")
		s.addLine()
	}
	if len(s.lines) != 3 || s.phase != poPhaseSource {
		t.Fatalf("setup: lines=%d phase=%v, want 3 lines in the source chooser", len(s.lines), s.phase)
	}
	return s
}

// TestPOSourcePhase_RemovesAnyLineNotJustTheLast is Ian's headline complaint:
// while building, x must be able to drop a line in the MIDDLE of the cart.
func TestPOSourcePhase_RemovesAnyLineNotJustTheLast(t *testing.T) {
	s := buildThreeLineCart(t)
	// The cursor parks on the newest line, so k aims it at the middle one.
	if s.reviewCursor != 2 {
		t.Fatalf("cursor should follow the last add; got %d", s.reviewCursor)
	}
	s.updateSourcePhase(runeKey('k'))
	if s.reviewCursor != 1 {
		t.Fatalf("k should move the cart highlight; cursor = %d", s.reviewCursor)
	}
	s.updateSourcePhase(runeKey('x'))

	if len(s.lines) != 2 {
		t.Fatalf("x should remove exactly one line; len = %d", len(s.lines))
	}
	if s.lines[0].label != "First" || s.lines[1].label != "Third" {
		t.Errorf("cart = %q,%q — want the MIDDLE line removed (First,Third)", s.lines[0].label, s.lines[1].label)
	}
	if s.phase != poPhaseSource {
		t.Errorf("phase = %v, want to stay in the source chooser", s.phase)
	}
	// Removing the last line leaves the cursor in range.
	s.updateSourcePhase(runeKey('j'))
	s.updateSourcePhase(runeKey('x'))
	if len(s.lines) != 1 || s.reviewCursor != 0 {
		t.Errorf("lines=%d cursor=%d, want 1 line with the cursor clamped to 0", len(s.lines), s.reviewCursor)
	}
	// The highlighted line is the one the cart marks.
	if out := s.renderSourcePhase(); !strings.Contains(out, "▸ 1) First") {
		t.Errorf("source cart should mark the highlighted line:\n%s", out)
	}
}

// TestPOSourcePhase_EditsAnyLineAndComesBack: ctrl+e while building edits an
// arbitrary line through the same form the review cart uses, and saving returns
// to the SOURCE chooser (where the edit started) rather than to review.
func TestPOSourcePhase_EditsAnyLineAndComesBack(t *testing.T) {
	s := buildThreeLineCart(t)
	s.updateSourcePhase(runeKey('k'))
	s.updateSourcePhase(runeKey('k')) // highlight line 0
	if s.reviewCursor != 0 {
		t.Fatalf("cursor = %d, want 0", s.reviewCursor)
	}

	s.updateSourcePhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poPhaseLine || s.editIndex != 0 {
		t.Fatalf("ctrl+e should edit line 0; phase=%v editIndex=%d", s.phase, s.editIndex)
	}
	if got := s.lineInputs[poLineFieldDesc].Value(); got != "First" {
		t.Errorf("desc prefill = %q, want %q", got, "First")
	}
	s.lineInputs[poLineFieldDesc].SetValue("First (edited)")
	s.lineInputs[poLineFieldQty].SetValue("12")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != poPhaseSource {
		t.Errorf("phase = %v, want back in the source chooser where the edit started", s.phase)
	}
	if len(s.lines) != 3 {
		t.Fatalf("edit must not change cart length; len = %d", len(s.lines))
	}
	if s.lines[0].item.Description != "First (edited)" || s.lines[0].item.Quantity != 12 {
		t.Errorf("line 0 = %+v, want the edited desc/qty", s.lines[0].item)
	}
	if s.lines[2].item.Description != "Third" {
		t.Errorf("sibling line changed: %+v", s.lines[2].item)
	}
	// Adding another line after the edit still appends (no editIndex leak).
	s.enterLinePhase(nil, nil, "Fourth", 1, 0, 0, 0)
	s.lineInputs[poLineFieldCost].SetValue("1.00")
	s.addLine()
	if len(s.lines) != 4 || s.lines[0].item.Description != "First (edited)" {
		t.Errorf("post-edit add should append; len=%d line0=%+v", len(s.lines), s.lines[0].item)
	}
}

// TestPOSourcePhase_EditEscReturnsToSource: cancelling an edit started while
// building also comes back to the source chooser, unchanged.
func TestPOSourcePhase_EditEscReturnsToSource(t *testing.T) {
	s := buildThreeLineCart(t)
	s.updateSourcePhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	s.lineInputs[poLineFieldDesc].SetValue("WRONG")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEsc})

	if s.phase != poPhaseSource {
		t.Errorf("phase = %v, want the source chooser", s.phase)
	}
	if s.editIndex != -1 {
		t.Errorf("editIndex = %d, want -1 after cancel", s.editIndex)
	}
	if s.lines[2].item.Description != "Third" {
		t.Errorf("cancelled edit changed the line: %+v", s.lines[2].item)
	}
}

// TestPOReviewPhase_EditStillReturnsToReview guards the pre-existing route: an
// edit launched from the review cart comes back to review, not to source.
func TestPOReviewPhase_EditStillReturnsToReview(t *testing.T) {
	s := buildThreeLineCart(t)
	enterReviewAt(s, 1)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	s.lineInputs[poLineFieldQty].SetValue("4")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poPhaseReview {
		t.Errorf("phase = %v, want review", s.phase)
	}
	if s.lines[1].item.Quantity != 4 {
		t.Errorf("line 1 qty = %d, want 4", s.lines[1].item.Quantity)
	}
}

// TestPOSourcePhase_EmptyCartKeysAreNoOps: with nothing staged, the cart keys
// must not panic or wander out of the source chooser.
func TestPOSourcePhase_EmptyCartKeysAreNoOps(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	s.phase = poPhaseSource
	for _, k := range []tea.KeyMsg{runeKey('j'), runeKey('k'), runeKey('x'), {Type: tea.KeyCtrlE}} {
		s.updateSourcePhase(k)
		if s.phase != poPhaseSource {
			t.Fatalf("key %q left the source chooser (phase %v) on an empty cart", k.String(), s.phase)
		}
	}
	if len(s.lines) != 0 {
		t.Errorf("empty-cart keys staged lines: %d", len(s.lines))
	}
}

// ---------------------------------------------------------------------------
// Per-line expected shipment date (sc-ytr5 gap 3)
// ---------------------------------------------------------------------------

// TestPOLineDate_OfferedOnInventoryLinesOnly: the backend's create path reads
// expected_shipment_date on the item_supplier branch alone, so the field is
// offered there (single-pack AND case-packed) and withheld from asset/freeform
// lines rather than collecting a value the PO would drop.
func TestPOLineDate_OfferedOnInventoryLinesOnly(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 11

	s.enterLinePhase(&id, nil, "Bolt", 1, 0, 0, 1) // single-pack inventory
	if !hasField(s.lineFields(), poLineFieldDate) {
		t.Errorf("single-pack inventory line should offer the expected-date field")
	}
	if out := s.renderLinePhase(); !strings.Contains(out, "Expected date:") {
		t.Errorf("inventory line should render the date input:\n%s", out)
	}

	s.enterLinePhase(&id, nil, "Widget", 1, 0, 0, 12) // case-packed inventory
	if !hasField(s.lineFields(), poLineFieldDate) {
		t.Errorf("case-packed inventory line should offer the expected-date field")
	}

	assetID := "asset-uuid"
	s.enterLinePhase(nil, &assetID, "Drill", 1, 0, 0, 0)
	if hasField(s.lineFields(), poLineFieldDate) {
		t.Errorf("asset line should not offer a date the backend ignores")
	}
	if out := s.renderLinePhase(); !strings.Contains(out, "inventory lines only") {
		t.Errorf("asset line should explain the missing date field:\n%s", out)
	}

	s.enterLinePhase(nil, nil, "Rags", 1, 0, 0, 0)
	if hasField(s.lineFields(), poLineFieldDate) {
		t.Errorf("freeform line should not offer a date the backend ignores")
	}
}

// TestPOLineDate_StagedBlankAndInvalid: a valid date reaches the payload, blank
// is allowed (omitted), and a malformed date is refused client-side.
func TestPOLineDate_StagedBlankAndInvalid(t *testing.T) {
	id := 11

	s := NewPurchaseOrderCreateScreen(Deps{})
	s.enterLinePhase(&id, nil, "Bolt", 4, 0, 0, 1)
	s.lineInputs[poLineFieldDate].SetValue("2026-08-14")
	s.addLine()
	if got := lastLine(t, s).ExpectedShipmentDate; got != "2026-08-14" {
		t.Errorf("expected_shipment_date = %q, want %q", got, "2026-08-14")
	}

	s2 := NewPurchaseOrderCreateScreen(Deps{})
	s2.enterLinePhase(&id, nil, "Bolt", 4, 0, 0, 1)
	s2.addLine() // date left blank — optional
	if got := lastLine(t, s2).ExpectedShipmentDate; got != "" {
		t.Errorf("blank date = %q, want it omitted", got)
	}

	s3 := NewPurchaseOrderCreateScreen(Deps{})
	s3.enterLinePhase(&id, nil, "Bolt", 4, 0, 0, 1)
	s3.lineInputs[poLineFieldDate].SetValue("08/14/2026")
	s3.addLine()
	if len(s3.lines) != 0 {
		t.Fatalf("a malformed date should not stage a line")
	}
	if !strings.Contains(s3.errMsg, "YYYY-MM-DD") {
		t.Errorf("errMsg = %q, want it to state the date format", s3.errMsg)
	}
}

// TestPOLineDate_NeverLeaksOntoALineThatIgnoresIt: a date typed on an inventory
// line must not ride along when the operator switches to a freeform line, whose
// backend branch would discard it.
func TestPOLineDate_NeverLeaksOntoALineThatIgnoresIt(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 11
	s.enterLinePhase(&id, nil, "Bolt", 1, 0, 0, 1)
	s.lineInputs[poLineFieldDate].SetValue("2026-08-14")

	s.enterLinePhase(nil, nil, "Rags", 2, 0, 0, 0) // switch to freeform
	s.lineInputs[poLineFieldCost].SetValue("1.50")
	// Even a value forced back into the (inactive) input must not be sent.
	s.lineInputs[poLineFieldDate].SetValue("2026-08-14")
	s.addLine()
	if got := lastLine(t, s).ExpectedShipmentDate; got != "" {
		t.Errorf("freeform line carried expected_shipment_date %q, want none", got)
	}
}

// TestPOLineDate_RoundTripsThroughSubmit drives the whole path: a date entered
// in the line form is staged, survives the review cart, and lands in the POSTed
// create payload as expected_shipment_date.
func TestPOLineDate_RoundTripsThroughSubmit(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0009"}`))
	}))
	defer srv.Close()

	s := NewPurchaseOrderCreateScreen(Deps{OMS: omsapi.New(srv.URL)})
	s.supplierID = 7
	id := 11
	s.enterLinePhase(&id, nil, "Bolt", 4, 0, 0, 1)
	s.lineInputs[poLineFieldDate].SetValue("2026-08-14")
	s.addLine()
	// A second line without a date proves the field is per-LINE, not per-PO.
	s.enterLinePhase(&id, nil, "Nut", 9, 0, 0, 1)
	s.addLine()

	msg := s.finalize()()
	created, ok := msg.(poCreatedMsg)
	if !ok {
		t.Fatalf("expected poCreatedMsg, got %T", msg)
	}
	if created.err != nil {
		t.Fatalf("create failed: %v", created.err)
	}

	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("posted %d item(s), want 2 (body: %v)", len(items), body)
	}
	first, _ := items[0].(map[string]any)
	if got := first["expected_shipment_date"]; got != "2026-08-14" {
		t.Errorf("line 1 expected_shipment_date = %v, want 2026-08-14", got)
	}
	second, _ := items[1].(map[string]any)
	if _, present := second["expected_shipment_date"]; present {
		t.Errorf("line 2 sent expected_shipment_date %v, want it omitted", second["expected_shipment_date"])
	}
}

// TestPOEditLine_EditsQtyAndDateOnABulkAddedLine ties the three gaps together:
// a line staged by the bulk add is editable exactly like a hand-entered one —
// ctrl+e changes both its quantity and its expected date.
func TestPOEditLine_EditsQtyAndDateOnABulkAddedLine(t *testing.T) {
	s := reorderScreen(
		reorderItem("Bolt", 11, 4, "2.50"),
		reorderItem("Nut", 12, 6, "0.75"),
	)
	s.updateReorderPickPhase(runeKey('a'))
	if len(s.lines) != 2 || s.phase != poPhaseReview {
		t.Fatalf("setup: lines=%d phase=%v", len(s.lines), s.phase)
	}

	s.reviewCursor = 1
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poPhaseLine {
		t.Fatalf("ctrl+e should open the line form on a bulk-added line; phase = %v", s.phase)
	}
	// A bulk-added reorder line is item-supplier-backed: editable cost seeded
	// from the reorder row, and the expected-date field offered.
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("bulk-added inventory line should offer the cost field")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "0.75" {
		t.Errorf("cost prefill = %q, want the reorder row's 0.75", got)
	}
	if !hasField(s.lineFields(), poLineFieldDate) {
		t.Errorf("bulk-added inventory line should offer the expected-date field")
	}
	if got := s.lineInputs[poLineFieldQty].Value(); got != "6" {
		t.Errorf("qty prefill = %q, want %q (the suggested quantity)", got, "6")
	}
	s.lineInputs[poLineFieldQty].SetValue("10")
	s.lineInputs[poLineFieldDate].SetValue("2026-09-01")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})

	if len(s.lines) != 2 {
		t.Fatalf("edit changed cart length: %d", len(s.lines))
	}
	edited := s.lines[1].item
	if edited.Quantity != 10 {
		t.Errorf("quantity = %d, want 10", edited.Quantity)
	}
	if edited.ExpectedShipmentDate != "2026-09-01" {
		t.Errorf("expected_shipment_date = %q, want 2026-09-01", edited.ExpectedShipmentDate)
	}
	if edited.ItemSupplierID == nil || *edited.ItemSupplierID != 12 {
		t.Errorf("edit must preserve the item-supplier source; got %v", edited.ItemSupplierID)
	}
	if s.lines[0].item.Quantity != 4 {
		t.Errorf("sibling line changed: %+v", s.lines[0].item)
	}
	// The staged date is visible in the cart, and re-opening the edit restores it.
	if out := s.renderCart(1, 0); !strings.Contains(out, "exp 2026-09-01") {
		t.Errorf("cart should show the per-line expected date:\n%s", out)
	}
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineFieldDate].Value(); got != "2026-09-01" {
		t.Errorf("re-opened edit date prefill = %q, want 2026-09-01", got)
	}
}

// ---------------------------------------------------------------------------
// Editable (optional) cost on catalog lines (sc-gnzw)
// ---------------------------------------------------------------------------

// capturePOBody serves the create endpoint and records the POSTed JSON body.
func capturePOBody(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0009"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

// submitAndReadItems finalizes the cart against the capture server and returns
// the POSTed line items.
func submitAndReadItems(t *testing.T, s *PurchaseOrderCreateScreen, body *map[string]any) []any {
	t.Helper()
	msg := s.finalize()()
	created, ok := msg.(poCreatedMsg)
	if !ok {
		t.Fatalf("expected poCreatedMsg, got %T", msg)
	}
	if created.err != nil {
		t.Fatalf("create failed: %v", created.err)
	}
	items, _ := (*body)["items"].([]any)
	return items
}

// TestPOCatalogCost_RoundTripsThroughSubmit drives the whole path: a cost typed
// on a catalog line lands in the POST as unit_cost (the backend's documented
// override), while a cleared one omits the key so the backend keeps pricing the
// line from item_supplier.unit_cost.
func TestPOCatalogCost_RoundTripsThroughSubmit(t *testing.T) {
	srv, body := capturePOBody(t)

	s := NewPurchaseOrderCreateScreen(Deps{OMS: omsapi.New(srv.URL)})
	s.supplierID = 7
	id := 11
	s.enterLinePhase(&id, nil, "Bolt", 4, 0.10, 0, 1)
	s.lineInputs[poLineFieldCost].SetValue("3.25") // override the catalog price
	s.addLine()
	s.enterLinePhase(&id, nil, "Nut", 9, 0.10, 0, 1)
	s.lineInputs[poLineFieldCost].SetValue("") // cleared → catalog price
	s.addLine()

	items := submitAndReadItems(t, s, body)
	if len(items) != 2 {
		t.Fatalf("posted %d item(s), want 2 (body: %v)", len(items), *body)
	}
	first, _ := items[0].(map[string]any)
	if got, ok := first["unit_cost"].(float64); !ok || math.Abs(got-3.25) > 1e-12 {
		t.Errorf("line 1 unit_cost = %v, want 3.25", first["unit_cost"])
	}
	second, _ := items[1].(map[string]any)
	if _, present := second["unit_cost"]; present {
		t.Errorf("line 2 sent unit_cost %v, want it omitted so the catalog prices it", second["unit_cost"])
	}
}

// TestPOEditLine_CatalogCostOverrideRoundTrips: ctrl+e re-opens a catalog line
// showing the override already staged on it — not the catalog price — so saving
// an untouched edit can't silently drop it, and clearing the field gives
// catalog pricing back.
func TestPOEditLine_CatalogCostOverrideRoundTrips(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 11
	s.enterLinePhase(&id, nil, "Bolt", 4, 0.10, 0, 1)
	s.lineInputs[poLineFieldCost].SetValue("3.25")
	s.addLine()

	// Re-open: the field shows the override, and saving keeps it.
	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if got := s.lineInputs[poLineFieldCost].Value(); got != "3.25" {
		t.Errorf("cost prefill = %q, want the staged override %q", got, "3.25")
	}
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})
	if got := s.lines[0].item.UnitCost; got == nil || math.Abs(*got-3.25) > 1e-12 {
		t.Fatalf("unit_cost after an untouched edit = %v, want 3.25", got)
	}
	if out := s.renderCart(0, 0); !strings.Contains(out, "@ $3.25") {
		t.Errorf("cart should show the overridden cost:\n%s", out)
	}

	// Re-open and clear it: back to catalog pricing.
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	s.lineInputs[poLineFieldCost].SetValue("")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})
	if got := s.lines[0].item.UnitCost; got != nil {
		t.Errorf("unit_cost after clearing = %v, want nil", *got)
	}
	if len(s.lines) != 1 {
		t.Errorf("edits changed the cart length: %d", len(s.lines))
	}
}

// TestPOBulkAddedCatalogLine_CostEditsThroughToTheSubmit is Ian's report end to
// end: bulk-add a supplier's reorder queue whose catalog cost is unset (the "15
// items all show $0" case), then correct one line's cost with ctrl+e and see it
// reach the POST.
func TestPOBulkAddedCatalogLine_CostEditsThroughToTheSubmit(t *testing.T) {
	srv, body := capturePOBody(t)

	s := reorderScreen(
		reorderItem("Bolt", 11, 4, "0.00"),
		reorderItem("Nut", 12, 6, "0.00"),
	)
	s.deps = Deps{OMS: omsapi.New(srv.URL)}
	s.updateReorderPickPhase(runeKey('a'))
	if len(s.lines) != 2 || s.phase != poPhaseReview {
		t.Fatalf("setup: lines=%d phase=%v", len(s.lines), s.phase)
	}
	// An unpriced catalog row stages no cost, so nothing is pinned before the
	// operator says otherwise.
	if s.lines[0].item.UnitCost != nil {
		t.Errorf("unpriced row staged unit_cost %v, want none", *s.lines[0].item.UnitCost)
	}

	s.reviewCursor = 0
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Fatalf("a bulk-added catalog line must expose a cost field to correct")
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "" {
		t.Errorf("cost prefill = %q, want blank when the catalog has no price", got)
	}
	s.lineInputs[poLineFieldCost].SetValue("12.50")
	s.updateLinePhase(tea.KeyMsg{Type: tea.KeyEnter})

	items := submitAndReadItems(t, s, body)
	if len(items) != 2 {
		t.Fatalf("posted %d item(s), want 2 (body: %v)", len(items), *body)
	}
	fixed, _ := items[0].(map[string]any)
	if got, ok := fixed["unit_cost"].(float64); !ok || math.Abs(got-12.50) > 1e-12 {
		t.Errorf("edited line unit_cost = %v, want 12.50", fixed["unit_cost"])
	}
	if got, ok := fixed["item_supplier_id"].(float64); !ok || got != 11 {
		t.Errorf("edited line item_supplier_id = %v, want 11", fixed["item_supplier_id"])
	}
	untouched, _ := items[1].(map[string]any)
	if _, present := untouched["unit_cost"]; present {
		t.Errorf("untouched line sent unit_cost %v, want it left to the catalog", untouched["unit_cost"])
	}
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func hasField(fields []int, want int) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}

func lastLine(t *testing.T, s *PurchaseOrderCreateScreen) omsapi.PurchaseOrderCreateItem {
	t.Helper()
	if len(s.lines) == 0 {
		t.Fatalf("no line was added (errMsg: %q)", s.errMsg)
	}
	return s.lines[len(s.lines)-1].item
}

// ---------------------------------------------------------------------------
// Running total + line target-type badge in the cart (sc-be24)
// ---------------------------------------------------------------------------

// poCartLineFor stages a cart line with the given target field set.
func poCartLineFor(item omsapi.PurchaseOrderCreateItem, label string) poCartLine {
	return poCartLine{item: item, label: label}
}

func TestPOCartTotal(t *testing.T) {
	cost := func(v float64) *float64 { return &v }
	sup := 7
	asset := "asset-1"

	lines := []poCartLine{
		poCartLineFor(omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup, Quantity: 3, UnitCost: cost(12.50)}, "Widget"),
		poCartLineFor(omsapi.PurchaseOrderCreateItem{AssetID: &asset, Quantity: 1, UnitCost: cost(1000)}, "Press"),
		poCartLineFor(omsapi.PurchaseOrderCreateItem{Description: "Freight", Quantity: 2, UnitCost: cost(0)}, "Freight"),
	}
	total, noCost := poCartTotal(lines)
	if total != 1037.50 {
		t.Errorf("total = %v, want 1037.50", total)
	}
	// An explicitly-typed 0 is a real $0, not a missing price.
	if noCost != 0 {
		t.Errorf("noCost = %d, want 0 (a typed zero cost is explicit)", noCost)
	}

	// A nil cost is "price it from the supplier catalog": it contributes 0 but
	// must be counted so the total can be flagged as a floor.
	lines = append(lines, poCartLineFor(
		omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup, Quantity: 9}, "Catalog-priced"))
	total, noCost = poCartTotal(lines)
	if total != 1037.50 {
		t.Errorf("total with an unpriced line = %v, want the other lines' 1037.50", total)
	}
	if noCost != 1 {
		t.Errorf("noCost = %d, want 1", noCost)
	}

	if total, noCost := poCartTotal(nil); total != 0 || noCost != 0 {
		t.Errorf("empty cart = (%v, %d), want (0, 0)", total, noCost)
	}
}

// TestPOCartTotal_QuantityIsAlreadyInUnits pins the case-pack invariant: qpp
// never enters the sum, because a case-packed line stores a per-UNIT cost and a
// unit quantity.
func TestPOCartTotal_QuantityIsAlreadyInUnits(t *testing.T) {
	unit := 1.25
	sup := 3
	lines := []poCartLine{{
		item: omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup, Quantity: 24, UnitCost: &unit},
		qpp:  12, // two cases of 12 — the extension is still qty × unit cost
	}}
	if total, _ := poCartTotal(lines); total != 30 {
		t.Errorf("total = %v, want 30 (24 × 1.25), qpp must not double-count", total)
	}
}

func TestPOCartLineType(t *testing.T) {
	sup := 5
	asset := "a-1"
	cases := []struct {
		item omsapi.PurchaseOrderCreateItem
		want string
	}{
		{omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup}, "item_supplier"},
		{omsapi.PurchaseOrderCreateItem{AssetID: &asset}, "asset"},
		{omsapi.PurchaseOrderCreateItem{Description: "Freight"}, "freeform"},
	}
	for _, c := range cases {
		if got := poCartLineType(poCartLineFor(c.item, "x")); got != c.want {
			t.Errorf("poCartLineType(%+v) = %q, want %q", c.item, got, c.want)
		}
	}
}

// TestPORenderCart_TotalAndTypeBadges drives the rendered cart: every staged
// line is badged with its target type and the summed total closes the list.
func TestPORenderCart_TotalAndTypeBadges(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	cost := func(v float64) *float64 { return &v }
	sup := 7
	asset := "asset-1"
	s.lines = []poCartLine{
		poCartLineFor(omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup, Quantity: 100, UnitCost: cost(12.50)}, "Widget"),
		poCartLineFor(omsapi.PurchaseOrderCreateItem{AssetID: &asset, Quantity: 1, UnitCost: cost(1.00)}, "Press"),
		poCartLineFor(omsapi.PurchaseOrderCreateItem{Description: "Freight", Quantity: 1, UnitCost: cost(0.50)}, "Freight"),
	}

	out := s.renderCart(-1, 0)
	for _, want := range []string{"[Inventory item]", "[Asset]", "[Freeform]"} {
		if !strings.Contains(out, want) {
			t.Errorf("cart missing type badge %s:\n%s", want, out)
		}
	}
	// Thousands separator + 3 line items, and no catalog-pricing caveat since
	// every line carries a cost.
	if !strings.Contains(out, "Total: $1,251.50") {
		t.Errorf("cart missing running total:\n%s", out)
	}
	if !strings.Contains(out, "(3 line items)") {
		t.Errorf("cart missing line count:\n%s", out)
	}
	if strings.Contains(out, "supplier catalog") {
		t.Errorf("fully-priced cart should not warn about catalog pricing:\n%s", out)
	}

	// The review phase shows the cart, so it inherits the same total.
	if rev := s.renderReviewPhase(); !strings.Contains(rev, "Total: $1,251.50") {
		t.Errorf("review phase missing running total:\n%s", rev)
	}
}

// TestPORenderCart_FlagsCatalogPricedLines makes sure a blank-cost line reads as
// "not yet priced" rather than silently landing as free in the total.
func TestPORenderCart_FlagsCatalogPricedLines(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	unit := 2.00
	sup := 7
	s.lines = []poCartLine{
		poCartLineFor(omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup, Quantity: 5, UnitCost: &unit}, "Priced"),
		poCartLineFor(omsapi.PurchaseOrderCreateItem{ItemSupplierID: &sup, Quantity: 5}, "Unpriced"),
	}
	out := s.renderCart(0, 0)
	if !strings.Contains(out, "Total: $10.00") {
		t.Errorf("total should sum only the priced line:\n%s", out)
	}
	if !strings.Contains(out, "1 line is priced from the supplier catalog") {
		t.Errorf("cart should flag the unpriced line:\n%s", out)
	}
}

// TestPORenderCart_EmptyDrawsNoTotal keeps a zero-line cart from advertising a
// $0.00 order.
func TestPORenderCart_EmptyDrawsNoTotal(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	if out := s.renderCart(-1, 0); strings.Contains(out, "Total:") {
		t.Errorf("empty cart should draw no total:\n%s", out)
	}
}
