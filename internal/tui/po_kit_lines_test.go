// Kit lines on a purchase order, and what receiving one really does (op-8n0).
//
// This is the acceptance criterion the operator can actually get hurt by. A kit
// line names the KIT — "Eufy Ink Kit, ordered 2" — and receiving it credits five
// other items and leaves the kit's own stock at zero. A receiving screen that
// says only what the line says is telling the operator the wrong thing twice:
// about what they are creating stock of, and about what the number they type
// means.
//
// The contract these hold it to:
//
//	a kit line says so       — on the label, where the name is read
//	the breakdown is there   — before the quantity is typed (the ratio) and
//	                           again as it is typed (the credit)
//	the arithmetic is right  — a PARTIAL receipt credits per-kit × what is being
//	                           received, never the pre-multiplied ordered figure
//	an ordinary line is      — no tag, no block, no warning
//	untouched
//	it survives the clip     — measured on the CLIPPED render at 80, 100 and 120
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// poKitFixtureLine is the widest realistic kit line: five components, one with a
// name far longer than any column the pane can afford.
func poKitFixtureLine() omsapi.PurchaseOrderItem {
	return omsapi.PurchaseOrderItem{
		ID: 11, Description: "Eufy printer maintenance kit (CMYK + cleaning)",
		QuantityOrdered: 2, QuantityPending: 2, IsKitLine: true,
		KitComponents: []omsapi.POKitComponent{
			{Component: "itm-c", ComponentName: "Cyan ink cartridge, high yield, wide-format",
				ComponentSKU: "CI-100-XL", QuantityPerKit: 1, Quantity: 2},
			{Component: "itm-m", ComponentName: "Magenta ink", ComponentSKU: "MI-100", QuantityPerKit: 1, Quantity: 2},
			{Component: "itm-y", ComponentName: "Yellow ink", ComponentSKU: "YI-100", QuantityPerKit: 1, Quantity: 2},
			{Component: "itm-k", ComponentName: "Black ink", ComponentSKU: "KI-100", QuantityPerKit: 3, Quantity: 6},
			{Component: "itm-x", ComponentName: "Cleaning kit", ComponentSKU: "CK-1", QuantityPerKit: 1, Quantity: 2},
		},
	}
}

func poPlainLine() omsapi.PurchaseOrderItem {
	return omsapi.PurchaseOrderItem{
		ID: 12, Description: "Box of M3 bolts", QuantityOrdered: 4, QuantityPending: 4,
	}
}

func poKitReceiveForm(t *testing.T, lines []omsapi.PurchaseOrderItem, width int) *ReceiveFormScreen {
	t.Helper()
	po := &omsapi.PurchaseOrder{ID: 5, Number: "PO-1001", Items: lines}
	s := NewReceiveFormScreen(Deps{}, po)
	s.Update(tea.WindowSizeMsg{Width: width, Height: jdeSweepHeight})
	return s
}

// TestReceiveKit_TheLineSaysItIsAKitAndWhatItCredits. Before a quantity is
// typed, the useful reading is the RATIO — "one kit is these five things" — so
// that is what the block shows rather than a row of zeroes.
func TestReceiveKit_TheLineSaysItIsAKitAndWhatItCredits(t *testing.T) {
	out := poKitReceiveForm(t, []omsapi.PurchaseOrderItem{poKitFixtureLine()}, 120).View()

	if !strings.Contains(out, poKitTag) {
		t.Errorf("the kit line is not marked as one:\n%s", out)
	}
	if !strings.Contains(out, "ordered 2 kits") {
		t.Errorf("the form does not say what the quantity column counts:\n%s", out)
	}
	if !strings.Contains(out, "per kit:") {
		t.Errorf("no per-kit breakdown before a quantity is typed:\n%s", out)
	}
	for _, want := range []string{"CI-100-XL", "MI-100", "YI-100", "KI-100", "CK-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("component %s is missing from the breakdown:\n%s", want, out)
		}
	}
	// The 3-per-kit component has to read as 3, not as 1 — the whole reason the
	// per-kit figure is carried separately from the multiplied one.
	if !strings.Contains(out, "3 × Black ink") {
		t.Errorf("a component with a per-kit quantity above 1 lost it:\n%s", out)
	}
	// And the standing warning, which is what stops "received 2" being read as
	// two of the thing named on the line.
	if !strings.Contains(out, "COMPONENT items") {
		t.Errorf("no standing warning about what a kit receipt credits:\n%s", out)
	}
}

// TestReceiveKit_TheBreakdownFollowsWhatIsTyped is the arithmetic that matters:
// a PARTIAL receipt credits per-kit × what is being received. Reusing the
// serializer's pre-multiplied figure — which is per-kit × quantity ORDERED —
// would over-state every component on the screen.
func TestReceiveKit_TheBreakdownFollowsWhatIsTyped(t *testing.T) {
	s := poKitReceiveForm(t, []omsapi.PurchaseOrderItem{poKitFixtureLine()}, 120)
	s.qty[0].SetValue("1") // one of the two ordered kits arrived

	out := s.View()
	if !strings.Contains(out, "receiving 1 kit credits") {
		t.Errorf("the breakdown did not follow the typed quantity:\n%s", out)
	}
	// One kit credits one cyan and THREE black — not the two and six the ordered
	// quantity would.
	if !strings.Contains(out, "1 × Cyan ink") || !strings.Contains(out, "3 × Black ink") {
		t.Errorf("a partial receipt was priced off the ordered quantity:\n%s", out)
	}
	if strings.Contains(out, "6 × Black ink") {
		t.Errorf("the ordered-quantity figure leaked into a partial receipt:\n%s", out)
	}

	s.qty[0].SetValue("2")
	if out := s.View(); !strings.Contains(out, "6 × Black ink") || !strings.Contains(out, "receiving 2 kits credits") {
		t.Errorf("a full receipt did not multiply out:\n%s", out)
	}
}

// TestReceiveKit_AnOrdinaryLineIsUntouched is acceptance criterion 4 on the
// receiving flow.
func TestReceiveKit_AnOrdinaryLineIsUntouched(t *testing.T) {
	out := poKitReceiveForm(t, []omsapi.PurchaseOrderItem{poPlainLine()}, 120).View()
	for _, forbidden := range []string{poKitTag, "per kit", "COMPONENT items"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("an ordinary receive form shows %q:\n%s", forbidden, out)
		}
	}
}

// TestReceiveKit_AKitLineWithNoComponentsSaysSo. Such a line credits nothing at
// all on receipt, which the operator has to learn BEFORE typing a quantity into
// it rather than by noticing no stock moved.
func TestReceiveKit_AKitLineWithNoComponentsSaysSo(t *testing.T) {
	line := poKitFixtureLine()
	line.KitComponents = nil
	out := poKitReceiveForm(t, []omsapi.PurchaseOrderItem{line}, 120).View()
	if !strings.Contains(out, "would credit nothing") {
		t.Errorf("an empty kit line did not warn:\n%s", out)
	}
}

// TestReceiveKit_TheClippedRenderLosesNothing is acceptance criterion 3 on the
// receiving flow, asserted on the CLIPPED render: Root truncates an over-wide
// row with nothing to show it did, and the last component in a breakdown is
// exactly as important as the first.
func TestReceiveKit_TheClippedRenderLosesNothing(t *testing.T) {
	empty := poKitFixtureLine()
	empty.KitComponents = nil
	states := []struct {
		name  string
		lines []omsapi.PurchaseOrderItem
	}{
		{"a kit line", []omsapi.PurchaseOrderItem{poKitFixtureLine(), poPlainLine()}},
		{"an empty kit line", []omsapi.PurchaseOrderItem{empty}},
	}
	for _, st := range states {
		for _, width := range kitTestWidths {
			budget := screenBodyWidth(width)
			s := poKitReceiveForm(t, st.lines, width)
			s.qty[0].SetValue("2")
			out := s.View()
			if clampToBox(out, budget, len(strings.Split(out, "\n"))) == out {
				continue
			}
			for _, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("%s at %d columns: a line is %d wide but the pane is %d — it is clipped: %q",
						st.name, width, w, budget, line)
				}
			}
		}
	}
}

// poKitDetailRows renders one PO-detail line block the way the sheet does — the
// same grid fit off the same pane width — so a kit assertion reads the rows an
// operator actually sees (po_detail.go's addLineItems).
func poKitDetailRows(li omsapi.PurchaseOrderItem, bodyWidth int) []string {
	fit := poFitLineGrid(bodyWidth, []omsapi.PurchaseOrderItem{li})
	return poLineBlock(1, li, "Acme", fit, bodyWidth)
}

// TestPODetailKit_TheLineShowsWhatItWillCredit. The purchase-order detail is
// where a line is read before anyone goes near the receiving screen, so it
// carries the same two facts: that the line is a kit, and what the ordered
// quantity will put on the shelf.
func TestPODetailKit_TheLineShowsWhatItWillCredit(t *testing.T) {
	out := strings.Join(poKitDetailRows(poKitFixtureLine(), screenBodyWidth(120)), "\n")

	if !strings.Contains(out, poKitTag) {
		t.Errorf("the PO line is not marked as a kit:\n%s", out)
	}
	if !strings.Contains(out, "receiving all 2 kits credits") {
		t.Errorf("the PO line does not say what it credits:\n%s", out)
	}
	// Two ordered kits × three black per kit = six.
	if !strings.Contains(out, "6 × Black ink") {
		t.Errorf("the ordered quantity was not multiplied out:\n%s", out)
	}

	plain := strings.Join(poKitDetailRows(poPlainLine(), screenBodyWidth(120)), "\n")
	if strings.Contains(plain, poKitTag) || strings.Contains(plain, "credits") {
		t.Errorf("an ordinary PO line grew a kit block:\n%s", plain)
	}
}

// TestPODetailKit_TheClippedRenderLosesNothing — the same measurement on the
// screen that draws the line first, narrowed to the BLOCK this bead adds.
//
// That narrowing is deliberate, not an oversight: the PO detail body already
// loses content at 80 columns that predates kits entirely — its aligned cost row
// (PART/UNIT/QTY/TOTAL) is 81 columns wide against the 51 an 80-column terminal
// has, and a long line description overruns on its own. Those are sc-xxpa. What
// a kit line CONTRIBUTES has to fit the floor regardless, and the tag it adds is
// drawn ahead of the description precisely so the clip cannot eat it.
func TestPODetailKit_TheClippedRenderLosesNothing(t *testing.T) {
	empty := poKitFixtureLine()
	empty.KitComponents = nil
	for _, line := range []omsapi.PurchaseOrderItem{poKitFixtureLine(), empty} {
		for _, width := range kitTestWidths {
			budget := screenBodyWidth(width)
			block := poKitCreditBlock(line.KitComponents, "receiving all 2 kits credits",
				"    ", budget, line.QuantityOrdered)
			for _, l := range block {
				if w := lipgloss.Width(l); w > budget {
					t.Errorf("at %d columns: a kit block line is %d wide but the pane is %d: %q",
						width, w, budget, l)
				}
			}
			// And the tag survives the clip of an over-long description, which is
			// the whole reason it leads the row.
			head := poKitDetailRows(line, budget)[0]
			if !strings.Contains(clampToBox(head, budget, 1), poKitTag) {
				t.Errorf("at %d columns the kit tag was clipped off the line: %q", width, head)
			}
		}
	}
}
