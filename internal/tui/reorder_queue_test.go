package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func TestReorderQueueLineUsesItemDetailsForSKU(t *testing.T) {
	r := omsapi.ReorderRequest{
		Item:     "uuid-1234",
		Quantity: 10,
		ItemDetails: &omsapi.ReorderItemDetails{
			SKU:          "M3-HEX-BOLT",
			CurrentStock: 2,
			MinimumStock: 5,
		},
		EstimatedCost: omsapi.DecimalString("12.50"),
		DaysPending:   1,
	}
	out := reorderQueueLine(r)
	if !strings.Contains(out, "M3-HEX-BOLT") {
		t.Errorf("expected SKU in row, got %q", out)
	}
	if !strings.Contains(out, "2/5") {
		t.Errorf("expected stock/min ratio in row, got %q", out)
	}
	if !strings.Contains(out, "12.50") {
		t.Errorf("expected estimated cost in row, got %q", out)
	}
	if !strings.Contains(out, "1d pending") {
		t.Errorf("expected days-pending label, got %q", out)
	}
}

func TestReorderQueueLineEmDashWhenItemDetailsMissing(t *testing.T) {
	// Anonymous-create endpoint can echo a row with no item_details
	// expansion. The renderer should fall back to em-dashes rather
	// than printing literal "0" for everything (which would imply
	// "stock is fine" — the opposite of the truth).
	r := omsapi.ReorderRequest{
		Item:        "uuid-only",
		Quantity:    1,
		DaysPending: 0,
	}
	out := reorderQueueLine(r)
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash for missing fields, got %q", out)
	}
}

func TestReorderQueueLineColumnsAlignBetweenRows(t *testing.T) {
	// Decimal point alignment + SKU column alignment between rows
	// so the operator can scan the queue at a glance.
	a := omsapi.ReorderRequest{
		ItemDetails: &omsapi.ReorderItemDetails{
			SKU: "A", CurrentStock: 0, MinimumStock: 1,
		},
		EstimatedCost: omsapi.DecimalString("1.00"),
	}
	b := omsapi.ReorderRequest{
		ItemDetails: &omsapi.ReorderItemDetails{
			SKU: "VERY-LONG-LIKELY", CurrentStock: 99, MinimumStock: 100,
		},
		EstimatedCost: omsapi.DecimalString("999.99"),
	}
	la := reorderQueueLine(a)
	lb := reorderQueueLine(b)

	// STOCK label should appear at the same offset in both rows
	// because the SKU column is fixed-width 16.
	if strings.Index(la, "STOCK") != strings.Index(lb, "STOCK") {
		t.Errorf("STOCK label moved between rows:\n  a=%q\n  b=%q", la, lb)
	}
	// Decimal point of the EST column should align across rows.
	estA := strings.Index(la, "EST")
	estB := strings.Index(lb, "EST")
	dotA := strings.Index(la[estA:], ".")
	dotB := strings.Index(lb[estB:], ".")
	if dotA != dotB {
		t.Errorf("estimated-cost decimals didn't align: %d vs %d\n  a=%q\n  b=%q", dotA, dotB, la, lb)
	}
}
