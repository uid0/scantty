// Kit lines on a purchase order (op-8n0) — what a kit line REALLY receives.
//
// A kit is bought as ONE supplier SKU and decomposes on receipt: the receipt
// credits the kit's component items and leaves the kit's own stock at zero.
// Everything an operator can see about such a line without this file is the kit
// — "Eufy Ink Kit, ordered 2" — and a screen that says only that is telling
// them the wrong thing twice over: the stock they are about to create is not the
// kit's, and the quantity they type is multiplied, not added.
//
// So both PO surfaces that show a line show its breakdown: the purchase-order
// detail (what the whole ordered quantity will credit) and the receive form
// (what the quantity being typed RIGHT NOW will credit, recomputed as it is
// typed — a preview of a different number would be worse than none).
//
// The arithmetic is deliberately done from QuantityPerKit rather than from the
// serializer's pre-multiplied Quantity. Those two answer different questions:
// Quantity is per-kit × quantity ORDERED, which is only what a receipt credits
// when the whole line arrives at once. Feeding it to a partial receipt would
// over-state every component on the screen. See omsapi.POKitComponent.
//
// Nothing here is drawn for an ordinary item, asset or freeform line — the
// backend sends `kit_components: null` for those, so the whole block is absent
// and those lines render exactly as they always have.
package tui

import (
	"fmt"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// poKitTag is the marker that says a line is not what it appears to be. It goes
// on the LABEL line — the line an operator reads to decide what they are
// holding — and it goes at the FRONT of it, ahead of the name, because a PO
// line's label is the part that overruns a narrow pane. A tag after the name is
// the first thing clampToBox cuts, which would leave a kit looking exactly like
// an ordinary line on precisely the terminal that most needs the warning.
const poKitTag = "[kit]"

// poKitCreditTokens is what receiving `kits` of this line credits: one reading
// per component, each "N × name (sku)".
//
// kits is the number of KITS, not of components. A zero (nothing typed yet)
// still produces the per-kit breakdown, so the operator can see the ratio before
// committing to a quantity — the readings then say "1 ×" per kit rather than a
// row of zeroes, which would read as "this receives nothing".
func poKitCreditTokens(comps []omsapi.POKitComponent, kits int) []jdeToken {
	if kits < 1 {
		kits = 1
	}
	out := make([]jdeToken, 0, len(comps))
	for _, comp := range comps {
		per := comp.QuantityPerKit
		if per < 1 {
			// A component the snapshot recorded with no quantity credits nothing;
			// showing "0 ×" is the honest reading and NOT a floor to 1, because
			// unlike the kit count above this is a fact about the kit rather than
			// a number the operator has not typed yet.
			per = 0
		}
		out = append(out, jdeToken{
			text:  fmt.Sprintf("%d × %s", per*kits, poKitComponentName(comp)),
			style: StyleMuted,
		})
	}
	return out
}

// poKitComponentName identifies a component the way the rest of the app does:
// the name, with the SKU in parentheses when there is one.
func poKitComponentName(comp omsapi.POKitComponent) string {
	name := strings.TrimSpace(comp.ComponentName)
	if name == "" {
		name = "(unnamed component)"
	}
	if sku := strings.TrimSpace(comp.ComponentSKU); sku != "" {
		name += " (" + sku + ")"
	}
	return name
}

// poKitCreditBlock renders a kit line's breakdown as the lines drawn UNDER it: a
// lead-in saying what is being credited, then the component readings, wrapped
// (not clipped) at `width` so the last reading — which is as important as the
// first — cannot be eaten by the pane edge.
//
// lead is the sentence the calling screen wants, and the two callers want
// different tenses for a reason: the PO detail is a RECORD of the order, where a
// line may already be received, so it leads tense-neutrally ("component
// breakdown for all 2 kits"); the receive form is ABOUT to do the thing, with no
// completed-state mark to contradict, so it leads with the action ("receiving 2
// kits credits"). indent lines the block up under the row it belongs to; width
// is the pane's body width, or 0 when it is not known yet, which — as everywhere
// in the JDE layer — means "do not truncate".
//
// A kit line with NO components gets a warning instead of an empty block: it
// would credit nothing at all on receipt, which is a thing the operator has to
// be told before they type a quantity into it, not after.
func poKitCreditBlock(comps []omsapi.POKitComponent, lead, indent string, width, kits int) []string {
	if len(comps) == 0 {
		// Wrapped, not clipped: at 80 columns the pane is 51 and this sentence is
		// 75, and the half that would be cut is the half that says what it costs.
		var out []string
		room := 0
		if width > 0 {
			if room = width - len(indent) - 2; room < 1 {
				room = 1
			}
		}
		for i, line := range jdeWrapNote(
			"this kit line lists no components — receiving it would credit nothing", room,
		) {
			lead := "! "
			if i > 0 {
				lead = "  "
			}
			out = append(out, indent+StyleStatusWarn.Render(lead+line))
		}
		return out
	}
	out := []string{indent + StyleMuted.Render(lead+":")}
	out = append(out, jdeWrapTokens(poKitCreditTokens(comps, kits), indent+"  ", width)...)
	return out
}
