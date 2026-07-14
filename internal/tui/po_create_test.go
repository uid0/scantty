package tui

import (
	"math"
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
// right fields render — single-pack inventory hides the cost field, case-packed
// shows it in case basis, and an asset line shows the per-unit cost field.
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

	// Single-pack line: no cost field, item-supplier source restored.
	enterReviewAt(s, 0)
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlE})
	if hasField(s.lineFields(), poLineFieldCost) {
		t.Errorf("editing a single-pack inventory line should hide the cost field")
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
