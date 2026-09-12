package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// reorderTestRoom is the cells the data row really gets at the 80-column
// terminal this project checks against: the pane less the row's own indent.
const reorderTestRoom = 51 - reorderDataIndent

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
	out := reorderQueueLine(r, reorderTestRoom)
	if !strings.Contains(out, "M3-HEX-BOLT") {
		t.Errorf("expected SKU in row, got %q", out)
	}
	if !strings.Contains(out, "2/5") {
		t.Errorf("expected stock/min ratio in row, got %q", out)
	}
	if !strings.Contains(out, "12.50") {
		t.Errorf("expected estimated cost in row, got %q", out)
	}
	if !strings.Contains(out, "1d") {
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
	out := reorderQueueLine(r, reorderTestRoom)
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash for missing fields, got %q", out)
	}
}

func TestReorderQueueLineColumnsAlignBetweenRows(t *testing.T) {
	// The SKU column is padded to a fixed width so STOCK — and the money's
	// decimal point behind it — land in the same column on every row. That is
	// the whole readability argument for a columnar list: an operator scans
	// DOWN a column, and a column that moves per row is not one.
	a := omsapi.ReorderRequest{
		ItemDetails: &omsapi.ReorderItemDetails{
			SKU: "A", CurrentStock: 0, MinimumStock: 1,
		},
		EstimatedCost: omsapi.DecimalString("1.00"),
	}
	b := omsapi.ReorderRequest{
		ItemDetails: &omsapi.ReorderItemDetails{
			SKU: "MED-SKU-01", CurrentStock: 99, MinimumStock: 100,
		},
		EstimatedCost: omsapi.DecimalString("999.99"),
	}
	la := reorderQueueLine(a, reorderTestRoom)
	lb := reorderQueueLine(b, reorderTestRoom)

	if strings.Index(la, "STOCK") != strings.Index(lb, "STOCK") {
		t.Errorf("STOCK label moved between rows:\n  a=%q\n  b=%q", la, lb)
	}
	if strings.Index(la, "$") != strings.Index(lb, "$") {
		t.Errorf("money column moved between rows:\n  a=%q\n  b=%q", la, lb)
	}
}

// TestReorderQueueLine_AWideSKUKeepsTheFactsOnTheRow is the bound this row
// exists to hold. Before it, the data line was a fixed
// "SKU %-16s   STOCK %7s   EST $%10s   %s" that came to 68 cells plus its
// indent — 21 past the 51 an 80-column pane gives — so clampToBox cut it from
// the RIGHT with no ellipsis and took the money and the age with it.
//
// The fixture carries a 32-cell manufacturer part number ON PURPOSE: every
// reorder fixture in this package used to be six characters, so no test had
// ever rendered this row at the length real MRO data reaches, which is how the
// overflow survived.
func TestReorderQueueLine_AWideSKUKeepsTheFactsOnTheRow(t *testing.T) {
	r := omsapi.ReorderRequest{
		Quantity: 100,
		Status:   omsapi.ReorderStatusApproved,
		ItemDetails: &omsapi.ReorderItemDetails{
			SKU:          "MFR-88421-REV-C-ZINC-PLATED-HEX",
			CurrentStock: 4,
			MinimumStock: 250,
		},
		EstimatedCost: omsapi.DecimalString("1234.56"),
		DaysPending:   12,
	}
	out := reorderQueueLine(r, reorderTestRoom)
	if got := lipgloss.Width(out); got > reorderTestRoom {
		t.Fatalf("data row is %d cells against a %d-cell budget: %q", got, reorderTestRoom, out)
	}
	// The facts never give: the stock ratio, the whole price and the age are
	// what the row is read FOR, and a price cut to "$1234." reads as a price.
	for _, fact := range []string{"4/250", "1234.56", "12d"} {
		if !strings.Contains(out, fact) {
			t.Errorf("fact %q was dropped from the row: %q", fact, out)
		}
	}
	// Something was cut, so the row says so rather than reading as a whole one.
	if !strings.Contains(out, "…") {
		t.Errorf("row gave something up and did not mark it: %q", out)
	}
}
