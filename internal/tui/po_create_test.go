package tui

import (
	"math"
	"strings"
	"testing"

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

	// Single-pack inventory: no cost field (unchanged, sc-5yr).
	s.enterLinePhase(&id, nil, "Bolt", 1, 0, 0, 1)
	if hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("single-pack inventory line should omit the cost field")
	}

	// Freeform: cost field present (unchanged).
	s.enterLinePhase(nil, nil, "", 0, 0, 0, 0)
	if !hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("freeform line should include the cost field")
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

func TestPOAddLine_SinglePackInventoryOmitsCost(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	id := 42
	s.enterLinePhase(&id, nil, "Bolt", 100, 0.10, 0, 1)
	// Even if a stale cost value were present, a single-pack line ignores it.
	s.lineInputs[poLineFieldCost].SetValue("9.99")
	s.addLine()

	line := lastLine(t, s)
	if line.UnitCost != nil {
		t.Errorf("unit_cost = %v, want nil for a single-pack inventory line", *line.UnitCost)
	}
	if line.ItemSupplierID == nil || *line.ItemSupplierID != 42 {
		t.Errorf("item_supplier_id = %v, want 42", line.ItemSupplierID)
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
	// The single-pack catalog-cost note must NOT appear on a case-packed line.
	if strings.Contains(out, "taken from the supplier catalog") {
		t.Errorf("case-packed line should not show the catalog-cost note:\n%s", out)
	}

	s.toggleCostBasis()
	if out := s.renderLinePhase(); !strings.Contains(out, "Unit cost:") {
		t.Errorf("after toggle, line should render 'Unit cost:' label:\n%s", out)
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
