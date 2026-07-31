// Purchase-order header terms (op-bwo9): priority, payment terms and freight
// terms.
//
// The three choice sets live here, in ONE table each, because two screens read
// them — po_edit.go picks a value and po_detail.go names the stored one — and a
// second copy of "net_30 means Net 30" is a copy that can drift. The values are
// the backend's TextChoices tokens verbatim (reorder_queue.models.PurchaseOrder):
// the serializer validates them, so a typo here is a 400, not a display bug.
package tui

import (
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// poDefaultPriority is the model's own default. It stands in wherever an order
// reports no priority at all — a backend that predates op-bwo9 sends the field
// nowhere, and "Low" would be a worse guess than the value such an order will
// report the moment that backend lands.
const poDefaultPriority = "normal"

// poPriorityOptions mirrors PurchaseOrder.Priority. The model gives priority a
// default and no blank, so every order has one of these four and the picker
// offers no "unset" row — there is no such state to pick.
var poPriorityOptions = []selectOption{
	{"low", "Low"},
	{"normal", "Normal"},
	{"high", "High"},
	{"urgent", "Urgent"},
}

// poPaymentTermsOptions mirrors PurchaseOrder.PaymentTerms. Blank leads the list
// because it is a real state — "not agreed with the supplier yet" — and the one
// an order starts in, so it has to be pickable to undo a mis-set term.
var poPaymentTermsOptions = []selectOption{
	{"", "— not agreed —"},
	{"due_on_receipt", "Due on Receipt"},
	{"net_15", "Net 15"},
	{"net_30", "Net 30"},
	{"net_60", "Net 60"},
	{"cod", "Cash on Delivery"},
	{"prepaid", "Prepaid"},
}

// poFreightTermsOptions mirrors PurchaseOrder.FreightTerms — who pays to ship
// the order, and from where. Blankable for the same reason as payment terms.
var poFreightTermsOptions = []selectOption{
	{"", "— not agreed —"},
	{"fob_origin", "FOB Origin"},
	{"fob_destination", "FOB Destination"},
	{"prepaid", "Prepaid"},
	{"collect", "Collect"},
	{"third_party", "Third Party"},
}

// poTermsLabel names a stored token for display. An unrecognized token renders
// as itself rather than as the first option's label: if the backend grows a
// fifth priority, an order carrying it should say so, not quietly read as "Low".
func poTermsLabel(opts []selectOption, value string) string {
	for _, o := range opts {
		if o.value == value {
			return o.label
		}
	}
	return value
}

// poTermsOptions returns a choice set with the order's stored value grafted on
// when it isn't one of the known tokens — the same guard poWithCurrentOption
// gives the association pickers, for the same reason. selectIndexOf falls back
// to index 0 for an unknown value, so without the graft a row would both
// display and (on save) write a value nobody chose. The graft keeps the label
// honest by marking where it came from.
func poTermsOptions(opts []selectOption, current string) []selectOption {
	// Nothing to graft for a blank the set has no row for: an empty token is an
	// order that reports no value at all, which is a caller's fallback to make
	// (see poDefaultPriority), not a choice to offer as "(unrecognized)".
	if current == "" {
		return opts
	}
	for _, o := range opts {
		if o.value == current {
			return opts
		}
	}
	out := make([]selectOption, 0, len(opts)+1)
	out = append(out, opts...)
	return append(out, selectOption{current, current + " (unrecognized)"})
}

// poTermsCell renders one stored term as a detail row's value. A term nobody
// has agreed yet is muted, so the row reads as an absence rather than as a
// value someone chose.
func poTermsCell(opts []selectOption, value string) string {
	if value == "" {
		return StyleMuted.Render(poTermsLabel(opts, ""))
	}
	return poTermsLabel(opts, value)
}

// renderPOTerms draws the header terms and the payment they imply (op-bwo9) on
// the PO detail screen.
//
// The whole block rides the PO payload, so it has no loading or failed state of
// its own to tell apart — either the order carries the fields or it came from a
// backend that predates them, and that second case gets nothing at all rather
// than three invented rows reading "not agreed". Everything here is descriptive:
// no stock moves and no money posts off any of it.
func renderPOTerms(b *strings.Builder, po *omsapi.PurchaseOrder) {
	if po.Priority == "" && po.PaymentTerms == "" && po.FreightTerms == "" && po.PaymentSchedule == nil {
		return
	}
	b.WriteString(StyleTitle.Render("Terms") + "\n")
	b.WriteString(StyleMuted.Render("Priority: ") +
		poTermsLabel(poPriorityOptions, firstNonEmpty(po.Priority, poDefaultPriority)) + "\n")
	b.WriteString(StyleMuted.Render("Payment terms: ") + poTermsCell(poPaymentTermsOptions, po.PaymentTerms) + "\n")
	b.WriteString(StyleMuted.Render("Freight terms: ") + poTermsCell(poFreightTermsOptions, po.FreightTerms) + "\n")
	// The schedule is the backend's arithmetic, rendered and not repeated: the
	// due date it derived, the amount it derived it over, and the rule it used —
	// which is also what says why a payment has no date yet.
	if sched := po.PaymentSchedule; sched != nil {
		// Rendered the way the Totals block right above renders the estimated
		// total this amount IS — a comma-grouped copy of the same number two
		// lines apart reads as a discrepancy.
		value := "—"
		if !sched.Amount.Empty() {
			value = "$" + string(sched.Amount)
		}
		if sched.DueDate != "" {
			value += " due " + sched.DueDate
		}
		if sched.Basis != "" {
			value += " · " + sched.Basis
		}
		b.WriteString(StyleMuted.Render("Payment: ") + value + "\n")
	}
	b.WriteString("\n")
}

// poSelectStrip lists a choice row's whole option set with the current one
// bracketed, for the line the edit form draws under the focused row.
func poSelectStrip(sel *poHeaderSelect) string {
	parts := make([]string, 0, len(sel.opts))
	for i, o := range sel.opts {
		if i == sel.idx {
			parts = append(parts, "["+o.label+"]")
			continue
		}
		parts = append(parts, o.label)
	}
	return strings.Join(parts, " · ")
}
