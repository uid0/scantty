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
	if !strings.Contains(out, poKitDetailLead) {
		t.Errorf("the PO line does not say what it breaks down into:\n%s", out)
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
// screen that draws the line first, narrowed to the ROWS a kit line contributes.
//
// That narrowing is deliberate, not an oversight: the PO detail body already
// loses content at 80 columns that predates kits entirely — its aligned cost row
// (PART/UNIT/QTY/TOTAL) is 81 columns wide against the 51 an 80-column terminal
// has. That is sc-xxpa. What a kit line CONTRIBUTES has to fit the floor
// regardless, and the tag it adds is drawn ahead of the description, inside the
// grid's Item cell, precisely so the clip cannot eat it.
//
// Measured on the rows the sheet actually draws rather than on a
// poKitCreditBlock called with a hand-written indent: the block hangs off the
// grid's item column (poLineGridItemIndent), so an indent invented by the test
// would measure a layout no operator sees and could pass while the real one
// overran.
func TestPODetailKit_TheClippedRenderLosesNothing(t *testing.T) {
	empty := poKitFixtureLine()
	empty.KitComponents = nil
	for _, line := range []omsapi.PurchaseOrderItem{poKitFixtureLine(), empty} {
		for _, width := range kitTestWidths {
			budget := screenBodyWidth(width)
			rows := poKitDetailRows(line, budget)
			for _, l := range rows {
				if w := lipgloss.Width(l); w > budget {
					t.Errorf("at %d columns: a kit line row is %d wide but the pane is %d: %q",
						width, w, budget, l)
				}
			}
			// And the tag survives the clip of an over-long description, which is
			// the whole reason it leads the row.
			head := rows[0]
			if !strings.Contains(clampToBox(head, budget, 1), poKitTag) {
				t.Errorf("at %d columns the kit tag was clipped off the line: %q", width, head)
			}
		}
	}
}

// TestPODetailKit_AResizeRelaysTheCreditBlock. A kit line's credit block wraps
// against the pane, so a body still laid out for the OLD width after a resize
// would be cut by clampToBox with nothing to show it had: the trailing
// components simply vanish at exactly the terminal that most needs them.
//
// Driven through Update rather than through the renderer, because that is where
// the failure lives — the block laid out at 80 was always correct, the question
// is whether the screen lays it out again once the pane says 80. The
// measurement is narrowed to the block this bead adds, for the reason
// TestPODetailKit_TheClippedRenderLosesNothing gives — the PO detail's aligned
// cost row overruns 80 columns on its own and predates kits (sc-xxpa).
func TestPODetailKit_AResizeRelaysTheCreditBlock(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "5")
	s.Update(tea.WindowSizeMsg{Width: 120, Height: jdeSweepHeight})
	s.Update(poDetailLoadedMsg{po: &omsapi.PurchaseOrder{
		ID: 5, Number: "PO-1001", Items: []omsapi.PurchaseOrderItem{poKitFixtureLine()},
	}})

	// The block is on screen at the wide width, and wrapped for THAT pane —
	// otherwise the resize below would prove nothing.
	if got := len(poKitBlockLines(s.sheetLines().text)); got == 0 {
		t.Fatalf("no kit credit block at 120 columns:\n%s", strings.Join(s.sheetLines().text, "\n"))
	}
	wide := screenBodyWidth(120)
	if !poKitAnyLineWiderThan(s.sheetLines().text, screenBodyWidth(80)) {
		t.Fatalf("the fixture already fits an 80-column pane at %d, so a resize proves nothing", wide)
	}

	s.Update(tea.WindowSizeMsg{Width: 80, Height: jdeSweepHeight})

	budget := screenBodyWidth(80)
	block := poKitBlockLines(s.sheetLines().text)
	if len(block) == 0 {
		t.Fatalf("the credit block disappeared after the resize:\n%s", strings.Join(s.sheetLines().text, "\n"))
	}
	for _, l := range block {
		if w := lipgloss.Width(l); w > budget {
			t.Errorf("after resizing to 80 the body is still laid out wide: a block line is %d against a %d pane: %q",
				w, budget, l)
		}
	}
	// Re-laid out, not truncated: every component the order credits is still
	// named, which is the whole point of wrapping rather than clipping.
	joined := strings.Join(block, " ")
	for _, want := range []string{"Cyan ink", "Magenta ink", "Yellow ink", "Black ink", "Cleaning kit"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the resize lost %q from the credit block:\n%s", want, strings.Join(block, "\n"))
		}
	}
}

// poKitBlockLines picks the credit block out of a rendered body: its lead line
// says what receiving credits, and each wrapped reading carries the "N ×" of a
// component.
func poKitBlockLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.Contains(l, "credits") || strings.Contains(l, "×") {
			out = append(out, l)
		}
	}
	return out
}

func poKitAnyLineWiderThan(lines []string, budget int) bool {
	for _, l := range poKitBlockLines(lines) {
		if lipgloss.Width(l) > budget {
			return true
		}
	}
	return false
}

// poKitDetailLead is the PO detail's lead for the two-kit fixture. It is
// tense-neutral on purpose; see the tests below and poLineBlock's comment.
const poKitDetailLead = "component breakdown for all 2 kits"

// TestPODetailKit_TheCreditLeadReadsTheSameInEveryState. The lead used to be
// future-tense and computed from the ordered quantity alone, so a line the
// header had already ticked as received carried a sentence saying the receipt
// was still to come — the screen telling the operator two things at once.
//
// One phrasing now serves every state, which is the point: there is no case
// split left to fall out of date when the state model grows one.
func TestPODetailKit_TheCreditLeadReadsTheSameInEveryState(t *testing.T) {
	received := poKitFixtureLine()
	received.QuantityReceived = 2
	received.QuantityPending = 0
	received.IsFullyReceived = true

	partial := poKitFixtureLine()
	partial.QuantityReceived = 1
	partial.QuantityPending = 1

	for _, tc := range []struct {
		name string
		line omsapi.PurchaseOrderItem
	}{
		{"pending", poKitFixtureLine()},
		{"part received", partial},
		{"fully received", received},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := strings.Join(poKitDetailRows(tc.line, screenBodyWidth(120)), "\n")

			if !strings.Contains(out, poKitDetailLead) {
				t.Errorf("the %s line lost the breakdown lead:\n%s", tc.name, out)
			}
			// Nothing may assert the receipt is still to happen — that is what
			// contradicted the tick four lines above it.
			for _, tense := range []string{"receiving", "will credit", "credits on"} {
				if strings.Contains(out, tense) {
					t.Errorf("the %s line still asserts a pending action (%q):\n%s", tc.name, tense, out)
				}
			}
			// And the numbers are untouched: this screen multiplies the ORDERED
			// quantity, whatever has arrived so far.
			if !strings.Contains(out, "6 × Black ink") || !strings.Contains(out, "2 × Magenta ink") {
				t.Errorf("the %s line's component quantities changed:\n%s", tc.name, out)
			}
		})
	}
}

// TestPODetailKit_TheUnorderedFallbackLeadIsTenseNeutralToo. The other lead this
// block can produce — a kit line recorded with no ordered quantity — asserted an
// action as well ("credits on full receipt"). Leaving one of the two asserting
// would just move the defect.
func TestPODetailKit_TheUnorderedFallbackLeadIsTenseNeutralToo(t *testing.T) {
	line := poKitFixtureLine()
	line.QuantityOrdered = 0
	line.QuantityPending = 0

	for _, width := range kitTestWidths {
		budget := screenBodyWidth(width)
		out := strings.Join(poKitDetailRows(line, budget), "\n")

		if !strings.Contains(out, "component breakdown per kit") {
			t.Errorf("at %d columns the unordered kit line lost its lead:\n%s", width, out)
		}
		for _, tense := range []string{"receiving", "credits on"} {
			if strings.Contains(out, tense) {
				t.Errorf("at %d columns the unordered lead still asserts an action (%q):\n%s", width, tense, out)
			}
		}
		// With nothing ordered the readings are the per-kit ratio, which is what
		// the lead now says they are.
		if !strings.Contains(out, "3 × Black ink") {
			t.Errorf("at %d columns the per-kit readings changed:\n%s", width, out)
		}
		// Both leads are longer than the one they replaced, so the floor is
		// re-measured on the block they lead.
		for _, l := range poKitCreditBlock(line.KitComponents, "component breakdown per kit", "    ", budget, 0) {
			if w := lipgloss.Width(l); w > budget {
				t.Errorf("at %d columns a kit block line is %d wide but the pane is %d: %q",
					width, w, budget, l)
			}
		}
	}
}
