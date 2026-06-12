package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func TestJDECostLineDecimalsAlign(t *testing.T) {
	// Two rows with different magnitudes — decimal points should
	// land in the same screen column on both rows so an operator
	// scanning a PO can compare unit costs at a glance.
	a := omsapi.PurchaseOrderItem{
		ItemDetails:     map[string]any{"sku": "M3-HEX-BOLT"},
		UnitCostOrdered: omsapi.DecimalString("0.05"),
		QuantityOrdered: 100,
		ActualCost:      omsapi.DecimalString("5.00"),
	}
	b := omsapi.PurchaseOrderItem{
		ItemDetails:     map[string]any{"sku": "WIDGET-XL-2024"},
		UnitCostOrdered: omsapi.DecimalString("12.50"),
		QuantityOrdered: 3,
		ActualCost:      omsapi.DecimalString("37.50"),
	}

	la := jdeCostLine(a)
	lb := jdeCostLine(b)

	// Decimal point in the UNIT field.
	uA := strings.Index(la, "UNIT")
	uB := strings.Index(lb, "UNIT")
	if uA != uB {
		t.Fatalf("UNIT label moved between rows: %d vs %d", uA, uB)
	}
	dotA := strings.Index(la[uA:], ".")
	dotB := strings.Index(lb[uB:], ".")
	if dotA != dotB {
		t.Errorf("unit-cost decimal points didn't align: %d vs %d\n  a=%q\n  b=%q", dotA, dotB, la, lb)
	}

	// Decimal point in the TOTAL field.
	tA := strings.Index(la, "TOTAL")
	tB := strings.Index(lb, "TOTAL")
	if tA != tB {
		t.Fatalf("TOTAL label moved between rows: %d vs %d", tA, tB)
	}
	tdotA := strings.Index(la[tA:], ".")
	tdotB := strings.Index(lb[tB:], ".")
	if tdotA != tdotB {
		t.Errorf("total-cost decimal points didn't align: %d vs %d\n  a=%q\n  b=%q", tdotA, tdotB, la, lb)
	}
}

func TestJDECostLinePrefersSKUOverAssetTag(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		ItemDetails:  map[string]any{"sku": "SKU-123"},
		AssetDetails: map[string]any{"asset_tag": "ASSET-9"},
	}
	out := jdeCostLine(li)
	if !strings.Contains(out, "SKU-123") {
		t.Errorf("expected SKU in row, got %q", out)
	}
	if strings.Contains(out, "ASSET-9") {
		t.Errorf("asset tag should be suppressed when a SKU exists, got %q", out)
	}
}

func TestJDECostLineFallsBackToAssetTag(t *testing.T) {
	// Asset POs have no SKU but do have asset_tag — surface the tag.
	li := omsapi.PurchaseOrderItem{
		AssetDetails: map[string]any{"asset_tag": "TAG-42"},
	}
	out := jdeCostLine(li)
	if !strings.Contains(out, "TAG-42") {
		t.Errorf("expected asset tag in row, got %q", out)
	}
}

func TestJDECostLineHandlesMissingCosts(t *testing.T) {
	// No cost data: the row should render with the em-dash placeholder
	// rather than crashing or showing literal $0.00 (which would imply
	// "free" instead of "unknown").
	li := omsapi.PurchaseOrderItem{QuantityOrdered: 0}
	out := jdeCostLine(li)
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash for missing fields, got %q", out)
	}
}

func TestJDECostLinePrefersActualOverEstimated(t *testing.T) {
	// When both totals exist, actual is the source of truth (matches
	// the existing meta-line semantics in renderPOLineItem).
	li := omsapi.PurchaseOrderItem{
		ItemDetails:   map[string]any{"sku": "X"},
		ActualCost:    omsapi.DecimalString("100.00"),
		EstimatedCost: omsapi.DecimalString("99.99"),
	}
	out := jdeCostLine(li)
	if !strings.Contains(out, "100.00") {
		t.Errorf("expected actual cost (100.00) in row, got %q", out)
	}
	if strings.Contains(out, "99.99") {
		t.Errorf("estimated cost (99.99) leaked through when actual was set, got %q", out)
	}
}
