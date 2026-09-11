// What a purchase-order line's cost field offers, and where the figure comes
// from — the pricing half of the PO line editor (po_edit.go).
//
// One asymmetry in the OMS purchase-order contract is why this file exists.
// A line's money is DERIVED on every read, and the two derivations do not share
// a denominator:
//
//	estimated_cost  = quantity_ORDERED  × unit_cost_ordered   (always a number)
//	actual_cost     = quantity_RECEIVED × unit_cost_actual    (null until both exist)
//
// while the only endpoint that writes a price reads what it is given as a total
// for the ORDERED quantity:
//
//	PATCH …/items/{id}/  {"line_cost": X}  ->  unit_cost_actual = X / quantity_ordered
//
// So actual_cost — "what this line has cost us so far" — is NOT a line_cost. On
// a partially received line, feeding it back multiplies the price by
// quantity_received / quantity_ordered, and repeating that walks a real price
// down to $0.00, which then reads as a price the line HAS rather than one it
// lost. Everything here keeps the form on the ORDERED basis the endpoint reads,
// so a price that goes out is the same price that came back.
//
// The second rule lives in po_edit.go's saveLine: the cost only rides a save
// when the operator changed it. A form that shows a price so the operator can
// see it must not thereby re-submit it — the same invariant the per-line
// association rows already state ("a re-tag must not carry along a cost the
// operator never touched"). "Changed" means a different AMOUNT and not
// different characters (poSameAmount): an operator who retypes the 50.00 in
// front of them has changed nothing, and a guard comparing raw text would have
// saved "50" while silently dropping "50.00" — the same intent decided by
// trailing zeros, which is worse than either answer on its own.
//
// That leaves one thing the operator can then no longer say by typing: "write
// the price this row is SHOWING, exactly as it stands". They need to be able to
// say it, because a line pinned at $0.00 shows the estimate the prefill
// recovered for it and there is no keystroke that differs from what is already
// there. Ctrl-E on the cost row is that affordance — the screen's existing "act
// on this row" key, which the same row uses to take the historical offer when
// the line has no price of its own to show. One key, one row, two states, and
// the action bar names whichever one is live (po_edit.go's lineBar).
//
// When the line carries no price at all — an asset or freeform line entered
// without one, or a line whose price was already lost — the form OFFERS the
// last price RECORDED for that item, read from the same purchase-history
// endpoint the inventory detail's Purchase / Receipts section uses
// (omsapi.GetPurchaseHistory, inventory_detail.go). It is offered, never
// applied: Ctrl-E on the cost row pulls it into the field, and until the
// operator does that the save carries no price.
//
// What that figure is NOT is money anybody paid, and it is not labelled as if
// it were. The purchase_history action joins on item_supplier__item_id alone —
// it filters out no voided line and restricts no order status — so the newest
// usable row can belong to a draft, to a cancelled order, or to a line that
// this very screen priced and that was then voided without a single unit
// arriving. is_voided is not on the wire and cannot be, without an endpoint
// this task must not add; the order's STATUS is, so it rides the offer beside
// the PO number and the order date, and the wording states what the row
// establishes (a price recorded on that order) rather than what it does not.
package tui

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// decimalAmount parses a decimal that arrived from OMS as either a JSON string
// or a JSON number (DecimalString tolerates both), reporting whether it is a
// number at all. Blank, null and unparseable all answer false. It is the one
// place the package turns an OMS decimal into arithmetic — formatMoney renders
// through it too — so "is there a price here" cannot come out two ways.
func decimalAmount(d omsapi.DecimalString) (float64, bool) {
	if d.Empty() {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(string(d)), 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// poMoney renders a line's total the way the cost field takes it: bare digits,
// no currency sign — what the operator would have typed. quantityOrdered is the
// denominator update_item will divide the figure by, and it is what decides how
// many places the figure needs.
//
// Two places normally, because that is what money reads as. More only when two
// would not SURVIVE the round trip: the figure on screen is the figure Ctrl-E
// writes back, so a rendering the endpoint would turn into a different
// unit_cost_actual is a rendering that quietly re-prices the line. The test is
// therefore the endpoint's own arithmetic and not a general "does 2dp lose
// digits", because the two disagree in both directions:
//
//	0.0005/unit × 10 = 0.005 -> "0.01" -> 0.01/10 = 0.0010, DOUBLE the price.
//	                                       widen to "0.0050".
//	33.3333/unit × 3 = 99.9999 -> "100.00" -> 100/3 = 33.3333, already stored.
//	                                       leave it, because "99.9999" forever
//	                                       on a line entered as 100.00 is a
//	                                       readability loss and nothing else.
//
// The degenerate case of that rule is the one this file exists for: 0.001 over
// 10 renders "0.00", which is not merely imprecise but reads as a line with no
// price — a non-empty string, so lineOffer says the line CARRIES a price and
// the purchase-history offer never appears, while the cost row's Ctrl-E stands
// ready to write a genuine zero over the real one.
//
// The widening stops at the four places unit_cost_actual itself keeps, since no
// smaller figure can reach us. It is deliberately NOT strconv's shortest
// round-trip form, which poUnitMoney can afford because it renders a decimal
// straight off the wire: this value is a PRODUCT, and the shortest form of
// 6.251 × 10 is "62.510000000000005" — a number no operator should be shown and
// none would have typed.
func poMoney(v float64, quantityOrdered int) string {
	out := strconv.FormatFloat(v, 'f', 2, 64)
	if v <= 0 || poCostSurvives(out, v, quantityOrdered) {
		return out
	}
	return strconv.FormatFloat(v, 'f', 4, 64)
}

// poCostSurvives reports whether sending `shown` back as this line's line_cost
// would leave unit_cost_actual exactly where v already puts it — the one
// question that decides whether a rendering is honest, since the rendering IS
// what the cost row's Ctrl-E writes.
//
// With no quantity to divide by there is no round trip to check, and the only
// claim left worth defending is the weaker one: a real price must not read as
// nothing.
func poCostSurvives(shown string, v float64, quantityOrdered int) bool {
	amount, ok := decimalAmount(omsapi.DecimalString(shown))
	if !ok {
		return false
	}
	if quantityOrdered <= 0 {
		return amount > 0
	}
	return poUnitCostFor(amount, quantityOrdered) == poUnitCostFor(v, quantityOrdered)
}

// poUnitCostFor is update_item's own line: unit_cost_actual = line_cost /
// quantity_ordered, quantized to the column's four places, half away from zero
// as Python's Decimal quantizes.
func poUnitCostFor(total float64, quantityOrdered int) float64 {
	return math.Round(total/float64(quantityOrdered)*1e4) / 1e4
}

// poSameAmount reports whether two cost entries name the same AMOUNT rather
// than the same characters. It is what decides "the operator did not touch the
// price" in saveLine, and the distinction is not academic: the field is
// prefilled from poLineCarriedCost, which renders a trailing-zero shape the
// operator has no reason to reproduce, so retyping the 50.00 in front of them
// types the shown text while typing 50 does not. Comparing raw text made those two keystroke
// sequences mean opposite things — one dropped, one saved — for one intent.
//
// Either side being blank or unparseable answers false, which is the safe way
// round here: "not the same" sends the price, and a blank shown value means
// there was no price to be the same as. saveLine rejects an unparseable entry
// before it ever asks.
func poSameAmount(a, b string) bool {
	av, aok := decimalAmount(omsapi.DecimalString(a))
	bv, bok := decimalAmount(omsapi.DecimalString(b))
	return aok && bok && av == bv
}

// poCostRejectedReason is the refusal saveLine makes, in saveLine's own words.
// The note that PREDICTS the failure and the error that REPORTS it are the same
// string on purpose: an operator who is told one thing before pressing enter
// and another thing after has been told the screen does not know.
const poCostRejectedReason = "line cost must be a non-negative number"

// poCostRejected reports whether saveLine will REFUSE the cost entry as it
// stands — saveLine's own test, ParseFloat then non-negative, asked through the
// same decimalAmount every other figure in this file is parsed with, so the two
// cannot come out differently on the same characters.
//
// The screen has to ask it before it says anything about what enter will do.
// The textinput has no validator (only a CharLimit), so "-5", "50,00" and "$50"
// all reach the field, and a note built from "the field is not empty" announced
// "enter writes $-5 as the total for the 10 ordered" for every one of them —
// promising a money write that saveLine then rejected outright. Blank answers
// rejected too, which no caller sees: the blank field is a settled state with a
// note of its own ("leave the price alone") and is tested before this.
func poCostRejected(cur string) bool {
	v, ok := decimalAmount(omsapi.DecimalString(cur))
	return !ok || v < 0
}

// poUnitMoney renders a PER-UNIT price. The column carries four decimal places
// and cheap parts use them, so this shows two — enough to read as money — and
// more only when rounding to cents would say something untrue. Printing a real
// price as $0.00 is the exact reading this screen exists to stop.
func poUnitMoney(v float64) string {
	out := strconv.FormatFloat(v, 'f', -1, 64)
	dot := strings.IndexByte(out, '.')
	if dot < 0 {
		return out + ".00"
	}
	if places := len(out) - dot - 1; places < 2 {
		return out + strings.Repeat("0", 2-places)
	}
	return out
}

// poLineCarriedCost is the price a line already carries, expressed on the basis
// update_item reads — the total for the quantity ORDERED.
//
// Actual wins over estimated, as it does everywhere a cost is shown, but it is
// rebuilt from unit_cost_actual × quantity_ordered rather than taken from
// actual_cost, which counts only what has arrived. A zero is treated as no
// price rather than as a price of nothing: zero is what a line that lost its
// price looks like, and what an asset or freeform line entered without one
// looks like, and in both cases the right answer is to offer something rather
// than to hand the zero back.
//
// "" means the line has no price to carry forward.
func poLineCarriedCost(li omsapi.PurchaseOrderItem) string {
	if unit, ok := decimalAmount(li.UnitCostActual); ok && unit > 0 && li.QuantityOrdered > 0 {
		return poMoney(unit*float64(li.QuantityOrdered), li.QuantityOrdered)
	}
	if est, ok := decimalAmount(li.EstimatedCost); ok && est > 0 {
		return poMoney(est, li.QuantityOrdered)
	}
	return ""
}

// poLineItemID is the inventory item a line buys, when it buys one. Asset and
// freeform lines have none, and so have no purchase history to offer.
func poLineItemID(li omsapi.PurchaseOrderItem) string {
	id, _ := li.ItemDetails["id"].(string)
	return strings.TrimSpace(id)
}

// poLastPaid is the newest purchase-order line for an item that carries a
// usable per-unit price: the last price recorded for that item, and which order
// records it.
//
// priced marks a figure read from unit_cost_actual — the price written to the
// line after the order was placed — rather than from unit_cost_ordered, the
// price it was placed at. It deliberately does NOT mean the money was paid:
// unit_cost_actual is written by the same PATCH this screen makes, on a line
// that may have received nothing and may afterwards be voided, on an order that
// may still be a draft. status carries the order's own state for exactly that
// reason — it is the one thing on the wire that lets the operator tell those
// rows apart, since the payload has no is_voided.
type poLastPaid struct {
	unit   float64
	order  string
	date   string
	status string
	priced bool
}

// poLastPaidFrom picks that row out of an item's purchase history. Rows arrive
// oldest first, so the walk runs backwards and stops at the first usable price.
//
// excludePO drops this order's own lines: the reason we are looking is that
// this line has no price, and its own row would offer the nothing we already
// have.
//
// The history is scoped to the ITEM, not to the item+supplier link the line was
// bought through — the backend's purchase_history action joins on
// item_supplier__item_id and returns no supplier per row, so a strictly
// supplier-scoped answer is not available from it. Nor does it filter voided
// lines or order status. The order the row came from, when it was placed and
// what state it is in are therefore all carried on the offer and shown, which
// is what lets the operator judge whether the price belongs to the supplier in
// front of them and to an order anybody honoured.
func poLastPaidFrom(h *omsapi.ItemPurchaseHistory, excludePO string) *poLastPaid {
	if h == nil {
		return nil
	}
	for i := len(h.OrderCosts) - 1; i >= 0; i-- {
		o := h.OrderCosts[i]
		if excludePO != "" && strconv.Itoa(o.PurchaseOrder) == excludePO {
			continue
		}
		row := &poLastPaid{
			order:  poDisplayLabel(o.PONumber, o.PurchaseOrder),
			status: strings.TrimSpace(o.Status),
		}
		if !o.OrderDate.IsZero() {
			row.date = o.OrderDate.Format("2006-01-02")
		}
		if unit, ok := decimalAmount(o.UnitCostActual); ok && unit > 0 {
			row.unit, row.priced = unit, true
			return row
		}
		if unit, ok := decimalAmount(o.UnitCostOrdered); ok && unit > 0 {
			row.unit = unit
			return row
		}
	}
	return nil
}

// total is what the offer comes to for this line — the per-unit price across
// the quantity ORDERED, which is the basis the cost field takes.
func (p *poLastPaid) total(quantityOrdered int) string {
	if p == nil || quantityOrdered <= 0 {
		return ""
	}
	return poMoney(p.unit*float64(quantityOrdered), quantityOrdered)
}

// describe names the offer the way it has to read on a green screen: that it is
// history rather than a figure anybody has agreed to for THIS order, and then
// the price, per unit, on which order, in what state that order is.
//
// The caveat LEADS, and that order is the whole point. At 80 columns, the
// canonical width of the terminal this interface is modelled on, the pane gives
// a body line 49 columns and the provenance alone spends more than that. The
// sentence used to be one unfolded row, and with the caveat trailing it was the
// caveat the pane cut off, leaving "Last priced at $3.75/unit on PO-2026-0007
// (2026-0" — a bare dollar figure that reads as this order's price, which is the
// exact reading the offer exists to prevent. It is folded to the pane now
// (po_edit.go's lineRowNotes), so both halves are drawn wherever the row is; the
// order still decides what a SHORT pane keeps, because the offer is the tail of
// the cost row's block and a window too short for the whole block loses its
// tail. The warning is the part that cannot be reconstructed from anywhere else
// on the screen — the PO number and date can, by opening the item — so it is the
// part that leads.
//
// It says "priced at" and never "paid". The row it comes from proves only that
// a price was recorded against a line; the history endpoint returns voided
// lines and every order status alike, so a figure nobody ever paid — a priced
// line on a cancelled order, or one this screen priced and that was then voided
// — can perfectly well be the newest usable row. Calling that "last paid" would
// be the screen asserting something the wire does not support, on the one field
// this whole file exists to stop from lying.
func (p *poLastPaid) describe() string {
	if p == nil {
		return ""
	}
	basis := "ordered at"
	if p.priced {
		basis = "priced at"
	}
	out := fmt.Sprintf("Historical price, not confirmed for this order — last %s $%s/unit on %s",
		basis, poUnitMoney(p.unit), p.order)
	if meta := p.meta(); meta != "" {
		out += " (" + meta + ")"
	}
	return out + "."
}

// meta is the offer's provenance in the shape the inventory detail's order rows
// already use (orderCostMeta, inventory_detail.go): when the order was placed,
// then what state it is in. Same order, same separator, so an operator who has
// read one screen can read the other. Either half can be missing — order_date
// is nullable and status is free text on the wire — and a missing half drops
// out rather than rendering an empty slot.
func (p *poLastPaid) meta() string {
	parts := make([]string, 0, 2)
	if p.date != "" {
		parts = append(parts, p.date)
	}
	if p.status != "" {
		parts = append(parts, p.status)
	}
	return strings.Join(parts, " · ")
}

// ---------------------------------------------------------------------------
// Loading it
// ---------------------------------------------------------------------------

// poLastPaidLoadedMsg carries one item's history lookup back to the edit screen.
type poLastPaidLoadedMsg struct {
	itemID string
	row    *poLastPaid
	err    error
}

// poLastPaidCache remembers what has been asked for, per inventory item, so
// reopening a line editor does not re-fetch and a line with no history says so
// once instead of looking like it is still loading.
type poLastPaidCache struct {
	rows    map[string]*poLastPaid
	loading map[string]bool
	errs    map[string]string
	asked   map[string]bool
}

func (c *poLastPaidCache) init() {
	if c.rows == nil {
		c.rows = map[string]*poLastPaid{}
		c.loading = map[string]bool{}
		c.errs = map[string]string{}
		c.asked = map[string]bool{}
	}
}

// load fetches an item's purchase history unless it has already been asked for.
// A failure is NOT fatal anywhere: this is an offer, and the cost field works
// without it (the inventory detail treats the same endpoint the same way — it
// is auth-required while the rest of the screen is not).
func (c *poLastPaidCache) load(deps Deps, itemID, excludePO string) tea.Cmd {
	c.init()
	if itemID == "" || c.asked[itemID] {
		return nil
	}
	c.asked[itemID] = true
	c.loading[itemID] = true
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		history, err := deps.OMS.GetPurchaseHistory(ctx, itemID)
		if err != nil {
			return poLastPaidLoadedMsg{itemID: itemID, err: err}
		}
		return poLastPaidLoadedMsg{itemID: itemID, row: poLastPaidFrom(history, excludePO)}
	}
}

// handle absorbs a lookup result, reporting whether msg was one.
func (c *poLastPaidCache) handle(msg tea.Msg) bool {
	m, ok := msg.(poLastPaidLoadedMsg)
	if !ok {
		return false
	}
	c.init()
	c.loading[m.itemID] = false
	if m.err != nil {
		c.errs[m.itemID] = m.err.Error()
		return true
	}
	c.rows[m.itemID] = m.row
	return true
}

// offer reports what is known about an item's last price: the row when one was
// found, and a note to show while the answer is still unknown or unavailable.
func (c *poLastPaidCache) offer(itemID string) (row *poLastPaid, note string) {
	c.init()
	switch {
	case itemID == "":
		// An asset or freeform line: no inventory item, so no history exists to
		// look in. Say that rather than leaving the row silent.
		return nil, "No purchase history for this line — type the price."
	case c.loading[itemID]:
		// "recorded", not "paid", while the fetch is in flight: the row this
		// will land on proves a price was written against a prior line and
		// nothing more (see describe). The operator must not be shown the
		// stronger claim first and the true one a moment later.
		return nil, "Looking up the last price recorded for this item…"
	case c.errs[itemID] != "":
		return nil, "Last price unavailable: " + c.errs[itemID]
	case c.rows[itemID] == nil:
		// NOT "never ordered before": a nil row also covers an item whose every
		// prior line was entered at 0.0000 (unit_cost_ordered is non-null with
		// no minimum, so that is a real shape), and one whose only history is
		// THIS order, which the lookup excludes on purpose. Say what was
		// actually established rather than a stronger claim the data cannot
		// carry.
		return nil, "No usable price in this item's history — type the price."
	}
	return c.rows[itemID], ""
}
