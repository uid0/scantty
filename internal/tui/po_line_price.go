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
// operator never touched").
//
// When the line carries no price at all — an asset or freeform line entered
// without one, or a line whose price was already lost — the form OFFERS the
// last price the shop actually paid for that item, read from the same
// purchase-history endpoint the inventory detail's Purchase / Receipts section
// uses (omsapi.GetPurchaseHistory, inventory_detail.go). It is offered, never
// applied: Ctrl-E on the cost row pulls it into the field, and until the
// operator does that the save carries no price. The figure is labelled as
// historical wherever it is shown, because it is what a DIFFERENT order paid,
// not what this one was quoted.
package tui

import (
	"context"
	"fmt"
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

// poMoney renders a total the way the cost field takes it: bare digits, two
// places, no currency sign — what the operator would have typed.
func poMoney(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

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
		return poMoney(unit * float64(li.QuantityOrdered))
	}
	if est, ok := decimalAmount(li.EstimatedCost); ok && est > 0 {
		return poMoney(est)
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
// usable per-unit price: what the shop last actually paid, and which order says
// so. Confirmed marks a price that was settled after delivery
// (unit_cost_actual) rather than the price the order was placed at.
type poLastPaid struct {
	unit      float64
	order     string
	date      string
	confirmed bool
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
// supplier-scoped answer is not available from it. The order it came from is
// carried on the offer and shown to the operator, which is what lets them judge
// whether the price belongs to the supplier in front of them.
func poLastPaidFrom(h *omsapi.ItemPurchaseHistory, excludePO string) *poLastPaid {
	if h == nil {
		return nil
	}
	for i := len(h.OrderCosts) - 1; i >= 0; i-- {
		o := h.OrderCosts[i]
		if excludePO != "" && strconv.Itoa(o.PurchaseOrder) == excludePO {
			continue
		}
		row := &poLastPaid{order: poDisplayLabel(o.PONumber, o.PurchaseOrder)}
		if !o.OrderDate.IsZero() {
			row.date = o.OrderDate.Format("2006-01-02")
		}
		if unit, ok := decimalAmount(o.UnitCostActual); ok && unit > 0 {
			row.unit, row.confirmed = unit, true
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
	return poMoney(p.unit * float64(quantityOrdered))
}

// describe names the offer the way it has to read on a green screen: the price,
// per unit, on which order, and — always — that it is history rather than a
// figure anybody has agreed to for THIS order.
func (p *poLastPaid) describe() string {
	if p == nil {
		return ""
	}
	basis := "ordered at"
	if p.confirmed {
		basis = "paid"
	}
	out := fmt.Sprintf("Last %s $%s/unit on %s", basis, poUnitMoney(p.unit), p.order)
	if p.date != "" {
		out += " (" + p.date + ")"
	}
	return out + " — historical, not confirmed for this order."
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
		return nil, "Looking up the last price paid…"
	case c.errs[itemID] != "":
		return nil, "Last price unavailable: " + c.errs[itemID]
	case c.rows[itemID] == nil:
		return nil, "Never ordered before — type the price."
	}
	return c.rows[itemID], ""
}
