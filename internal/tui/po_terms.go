// Purchase-order header terms (op-bwo9): priority, payment terms and freight
// terms.
//
// The three choice sets live here, in ONE table each, because two screens read
// them — po_edit.go picks a value and po_detail.go names the stored one — and a
// second copy of "net_30 means Net 30" is a copy that can drift. The values are
// the backend's TextChoices tokens verbatim (reorder_queue.models.PurchaseOrder):
// the serializer validates them, so a typo here is a 400, not a display bug.
//
// The DISPLAY side of this file is columnar (sc-h412): poTermsFields hands the
// detail sheet a block of jdeFields to hang off the shared leader column, the
// way po_edit.go's metaFields does for the same four readings. It used to write
// "Payment terms: Net 30" straight into a strings.Builder, which is the
// hand-rolled style the redesign replaces — and which could not line its labels
// up with the Identifiers and Dates rows drawn either side of it.
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

// poTermsValue renders one stored term as a detail row's value, with the flag
// that says it should read as an absence. A term nobody has agreed yet is
// dimmed rather than pre-styled here, because the columnar renderer owns the
// styling: a value that arrived carrying its own colour sequence would end the
// row's highlight partway across the field (the same rule assocRowValue keeps).
func poTermsValue(opts []selectOption, value string) (string, bool) {
	if value == "" {
		return poTermsLabel(opts, ""), true
	}
	return poTermsLabel(opts, value), false
}

// poTermsFields is the header terms and the payment they imply (op-bwo9) as
// columnar rows for the PO detail sheet. nil when the order carries none of
// them, which is the caller's signal to draw no section at all.
//
// The whole block rides the PO payload, so it has no loading or failed state of
// its own to tell apart — either the order carries the fields or it came from a
// backend that predates them, and that second case gets nothing rather than
// three invented rows reading "not agreed". Everything here is descriptive: no
// stock moves and no money posts off any of it.
func poTermsFields(po *omsapi.PurchaseOrder) []jdeField {
	if po.Priority == "" && po.PaymentTerms == "" && po.FreightTerms == "" && po.PaymentSchedule == nil {
		return nil
	}
	priority, _ := poTermsValue(poPriorityOptions, firstNonEmpty(po.Priority, poDefaultPriority))
	payment, payDim := poTermsValue(poPaymentTermsOptions, po.PaymentTerms)
	freight, freightDim := poTermsValue(poFreightTermsOptions, po.FreightTerms)
	out := []jdeField{
		{Label: "Priority", Kind: jdeValue, Value: priority},
		{Label: "Payment terms", Kind: jdeValue, Value: payment, Dim: payDim},
		{Label: "Freight terms", Kind: jdeValue, Value: freight, Dim: freightDim},
	}
	// The schedule is the backend's arithmetic, rendered and not repeated: the
	// due date it derived, the amount it derived it over, and the rule it used —
	// which is also what says why a payment has no date yet.
	if sched := po.PaymentSchedule; sched != nil {
		out = append(out, jdeField{Label: "Payment", Kind: jdeValue, Value: poPaymentScheduleValue(sched)})
	}
	return out
}

// poPaymentScheduleValue renders the derived payment. The amount is spelled the
// way the Totals block above it spells the estimated total this amount IS — a
// comma-grouped copy of the same number two rows apart reads as a discrepancy.
func poPaymentScheduleValue(sched *omsapi.POPaymentSchedule) string {
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
	return value
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
