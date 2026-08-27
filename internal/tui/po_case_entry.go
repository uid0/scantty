// Ordering by the case: the packaging level a purchase-order line is ENTERED
// at, and the conversion every entry point runs to reach the wire.
//
// The captain's report, which is what this file exists for: "I'm putting in the
// case pricing. It's saving the unit cost, even though we're ordering case
// quantities." A case of 24 at £48 typed into a row meaning per-unit, against a
// quantity of 24 base units, records a line worth £1,152 instead of £48 — and
// every figure built on it (line and order totals, purchase history, the price
// suggested next time) inherits the error.
//
// # The contract on the other side
//
// OMS stores a purchase-order line in BASE units at a per-BASE-unit price:
//
//	quantity_ordered   — always base units, whichever pack size shaped it
//	unit_cost_ordered  — cost per base unit
//	order_in_packages  — derived server-side by order_packages_for_line()
//
// (backend/reorder_queue/services/purchase_orders.py). Nothing here changes
// that and nothing here sends a new field: the conversion is derive-then-send.
// order_in_packages is deliberately NOT posted even though create_purchase_order
// accepts it, because ceil(cases × qpp / qpp) is exactly `cases` — sending it
// would be a second client-side copy of a number the server already derives
// correctly, free to drift. omsapi.reorders_test's
// TestCreatePurchaseOrder_InventoryLineCaseDerivedCost pins that absence.
//
// # Which case size, and why the supplier's
//
// ItemSupplier.quantity_per_package is the SUPPLIER's case — what that vendor
// actually ships — and it is what the operator is holding. It is not the item's
// own packaging chain (internal/tui/packaging.go, countLevelBaseUnits), which
// is how the shop COUNTS the thing on the shelf; the two are different numbers
// and OMS reconciles them itself in order_package_size(). A client that reached
// for the item's rung would offer "cases" the vendor does not sell.
//
// So case entry is offered exactly when the candidate row the operator picked
// declares quantity_per_package > 1, which is the same gate the web PO form
// uses (frontend/src/pages/PurchaseOrderFormPage.tsx). A column that DEFAULTS
// to 1 cannot tell "this vendor sells singles" from "nobody filled the case
// size in", so 1 means singles and singles are left exactly as they were.
//
// # One basis for the whole line
//
// The row that took a case price while meaning a unit price is the reported
// defect, and RELABELLING it is not the fix: the operator's intent is to type
// what the vendor charges for what the vendor ships, so the QUANTITY has to
// move with the price or the pane still asks for two different denominators
// side by side. Hence one basis per line, flipped with Ctrl-T, governing both
// rows and both labels together.
//
// Base-unit entry stays reachable on purpose. quantity_ordered is any positive
// integer, so a broken case or an odd top-up is a legitimate order, and a form
// that could only express whole cases would be a refusal the operator cannot
// satisfy. Ctrl-T is that escape hatch, and it is why the case basis is a
// DEFAULT rather than a constraint.
//
// # Where the basis cannot be assumed
//
// A prefilled quantity arrives in base units and is not always a whole number
// of the supplier's cases: OMS rounds a suggestion up to a whole supplier
// package only for items counted in base units (views.reorder_data,
// line_entry.default_quantity both gate on counts_in_packs), so a pack-counted
// item can suggest 30 against a case of 12. Opening that line at case basis
// would have to round — 2 cases is 48 units, an order 60% larger than the one
// suggested — which is the "never silently overwrite" rule with money on it.
// poOpensAtCaseBasis answers that question in one place: whole cases, or no
// prefill at all, opens at case basis; anything else opens at unit basis and
// the screen says why.
//
// The same rule governs the Ctrl-T flip in the units→cases direction, and there
// it is a BAR GATE rather than a fallback: poFlipsToCases decides whether the
// key is offered at all, so it is never named over a state where it could only
// decline, and poUnwholeCaseNote says on the pane why it is absent and what to
// type to get it back.
package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// poPackSize is base units in ONE purchase package: the supplier's declared
// case size, or 1. Every conversion below divides or multiplies by this, so a
// caller may run them unconditionally and a singles line comes out unchanged.
func poPackSize(qpp int) int {
	if qpp > 1 {
		return qpp
	}
	return 1
}

// poCasePacked reports whether this line is bought by the case — the ONE
// predicate the labels, the Ctrl-T offer and the conversions all branch on, so
// a screen offering the toggle and a payload applying the conversion cannot
// disagree about which lines are case-packed.
func poCasePacked(qpp int) bool { return qpp > 1 }

// poBaseQuantity is what goes on the wire for a quantity typed at `caseBasis`.
// Cases multiply up; base units pass through.
func poBaseQuantity(entered, qpp int, caseBasis bool) int {
	if !caseBasis || !poCasePacked(qpp) {
		return entered
	}
	return entered * poPackSize(qpp)
}

// poWholeCases turns base units into whole cases, reporting whether the
// division came out exact. It never rounds for its caller: `exact` false means
// "this quantity is not a whole number of cases", which is a fact the caller
// has to act on rather than paper over.
func poWholeCases(base, qpp int) (cases int, exact bool) {
	size := poPackSize(qpp)
	return base / size, base%size == 0
}

// poUnitFromCase and poCaseFromUnit convert a price between the two bases at
// FULL precision. No cent rounding: a case size that does not divide evenly
// would otherwise drift a fraction of a penny per unit every time the basis
// flipped, and the value that reaches the payload is the unrounded one.
func poUnitFromCase(caseCost float64, qpp int) float64 {
	return caseCost / float64(poPackSize(qpp))
}

func poCaseFromUnit(unitCost float64, qpp int) float64 {
	return unitCost * float64(poPackSize(qpp))
}

// poOpensAtCaseBasis decides which basis a line form OPENS at, given the
// quantity it is being prefilled with in BASE units (0 for "no prefill").
//
// It is a function and not an inline `qpp > 1` because the answer has two
// halves and the second one is the one that gets forgotten: a case-packed line
// whose prefill is not a whole number of cases must open at UNIT basis, or the
// form would have to round a suggestion the server computed deliberately. See
// the file comment for how a pack-counted item reaches that state.
func poOpensAtCaseBasis(baseQty, qpp int) bool {
	if !poCasePacked(qpp) {
		return false
	}
	if baseQty <= 0 {
		return true
	}
	_, exact := poWholeCases(baseQty, qpp)
	return exact
}

// ---------------------------------------------------------------------------
// Wording
// ---------------------------------------------------------------------------

// poQtyRowLabel and poCostRowLabel are the two typed rows' labels. They are
// here rather than on each screen because the defect being fixed is a label
// disagreeing with what the row means, and two screens spelling the pair
// differently is that defect waiting to come back on whichever one was not
// edited.
func poQtyRowLabel(caseBasis bool) string {
	if caseBasis {
		return "Cases"
	}
	return "Quantity"
}

func poCostRowLabel(caseBasis bool) string {
	if caseBasis {
		return "Case cost"
	}
	return "Unit cost"
}

// poCaseCount and poUnitCount are the counted nouns, pluralised. A bare number
// beside a label is what the reported defect looked like, so every derived
// figure this file renders carries its unit inline.
func poCaseCount(n int) string {
	if n == 1 {
		return "1 case"
	}
	return strconv.Itoa(n) + " cases"
}

func poUnitCount(n int) string {
	if n == 1 {
		return "1 unit"
	}
	return strconv.Itoa(n) + " units"
}

// poQtyFact renders a BASE-unit quantity in the unit the line is bought in, for
// a compact list row where there is no label column to carry the noun.
//
// A singles line renders the bare number it always did — the whole point of the
// case work is that a vendor selling singles sees nothing new. A case-packed
// line gets a suffix, and WHICH suffix says which denominator it is in: "2 cs"
// for two of the vendor's cases, "30 u" for a quantity that is not a whole
// number of them. A bare number on a row that also says "case ×24" is the
// ambiguity this whole file exists to remove, one surface earlier.
func poQtyFact(base, qpp int) string {
	if !poCasePacked(qpp) {
		return strconv.Itoa(base)
	}
	if cases, exact := poWholeCases(base, qpp); exact {
		return strconv.Itoa(cases) + " cs"
	}
	return strconv.Itoa(base) + " u"
}

// poPackFact is what ONE case contains — the fact the acceptance criteria call
// for and the one an operator needs to catch a mis-keyed entry before it is
// committed. Empty for a line that is not case-packed, which contains nothing
// worth saying.
func poPackFact(qpp int) string {
	if !poCasePacked(qpp) {
		return ""
	}
	return fmt.Sprintf("1 case = %s", poUnitCount(qpp))
}

// poEntryDerivation is the line spelled out in BOTH bases plus what it comes
// to: the row under the two typed fields, and the whole of "a wrong entry
// should be visible on the pane before it is committed".
//
// entered is the quantity as typed (cases or base units per caseBasis), cost
// the price as typed with hasCost false for a blank row, and qpp the supplier's
// case size. It renders what it HAS: a quantity with no price still says how
// many units the line is for, and a price with no quantity still says what one
// unit costs.
//
// withTotal says whether to end with what the line comes to. It is a parameter
// because one of the two callers already draws a "Line total" row of its own
// (po_add_line.go), and a sentence repeating it would be the same figure twice
// on one pane — the shape this project keeps having to delete, where two copies
// of one claim eventually disagree.
func poEntryDerivation(entered int, cost float64, hasCost bool, qpp int, caseBasis, withTotal bool) string {
	base := poBaseQuantity(entered, qpp, caseBasis)
	var parts []string

	if poCasePacked(qpp) && entered > 0 {
		if caseBasis {
			parts = append(parts, fmt.Sprintf("%s × %d = %s",
				poCaseCount(entered), qpp, poUnitCount(base)))
		} else {
			cases, exact := poWholeCases(base, qpp)
			if exact {
				parts = append(parts, fmt.Sprintf("%s = %s of %d",
					poUnitCount(base), poCaseCount(cases), qpp))
			} else if note := poUnwholeCaseNote(base, qpp); note != "" {
				// Deliberately NOT rounded to a case count: the point of the
				// row is that this quantity is not a whole number of cases,
				// which is also why the bar is not offering Ctrl-T.
				parts = append(parts, note)
			}
		}
	}

	if hasCost && poCasePacked(qpp) {
		unit, cas := cost, poCaseFromUnit(cost, qpp)
		if caseBasis {
			unit, cas = poUnitFromCase(cost, qpp), cost
		}
		parts = append(parts, fmt.Sprintf("$%s/case ÷ %d = $%s/unit",
			poDisplayMoney(cas), qpp, poDisplayMoney(unit)))
	}

	if withTotal && hasCost && base > 0 {
		unit := cost
		if caseBasis {
			unit = poUnitFromCase(cost, qpp)
		}
		parts = append(parts, fmt.Sprintf("line $%s", poDisplayMoney(unit*float64(base))))
	}

	return strings.Join(parts, " · ")
}

// poFlipsToCases reports whether a UNIT-basis quantity box can move onto the
// case basis: it holds a whole number of cases, or it holds nothing at all.
//
// This is a BAR GATE and not a refusal, and the difference is the rule this
// project keeps: a bar may only name a key that will act, and a key that can
// only decline must not be named — so Ctrl-T disappears while the quantity has
// no case count and comes back the moment it has one. The pane is not silent
// while it is gone: poEntryDerivation states, standing, that the quantity is
// not a whole number of cases, which is the same reason a list edge may answer
// a key with nothing (the answer is already drawn).
//
// An unparseable box flips freely. The characters are left alone by the flip
// and the submit's own reader refuses them in its own words; deciding here that
// "12x" is a conversion problem would be a second judge of one row.
func poFlipsToCases(raw string, qpp int) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || !poCasePacked(qpp) {
		return true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return true
	}
	_, exact := poWholeCases(n, qpp)
	return exact
}

// poUnwholeCaseNote is why a case-packed line is being entered in UNITS: the
// quantity in the box is not a whole number of the vendor's cases.
//
// It names the two quantities that ARE, both typeable into the row the cursor
// can already reach, so the state is one the operator can leave. It names no
// KEY: the action bar is the only surface that names keys on these screens, and
// it starts naming Ctrl-T again by itself the moment the box holds one of the
// two figures below (poFlipsToCases is the same arithmetic, asked as a gate).
//
// It is deliberately SHORT. It rides inside poEntryDerivation, which is folded
// onto a 51-column pane under a field on a form of three or four rows, and a
// sentence that spent four lines saying this would push the derivation it is
// part of off the block.
func poUnwholeCaseNote(base, qpp int) string {
	if !poCasePacked(qpp) || base <= 0 {
		return ""
	}
	low, exact := poWholeCases(base, qpp)
	if exact {
		return ""
	}
	// Below one case there is no lower whole-case quantity to name: zero is not
	// an order, and offering it as one of two figures to type would be the note
	// proposing a line nobody can place.
	if low < 1 {
		return fmt.Sprintf("%s — not a whole number of %d-unit cases (%d is)",
			poUnitCount(base), qpp, qpp)
	}
	return fmt.Sprintf("%s — not a whole number of %d-unit cases (%d or %d is)",
		poUnitCount(base), qpp, low*qpp, (low+1)*qpp)
}
