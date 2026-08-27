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
// The flip converts both values EXACTLY, in decimal (big.Rat), quantised at the
// four-place column unit_cost_ordered actually stores — poUnitCostFrom and
// poCaseCostFrom, never the float64 pair above. A case price that divides
// evenly round-trips byte for byte; one that does not (10.00 over 3) settles on
// the 3.3333 the order will really carry, so the box and the wire state the
// same number. Done in float64 this drifted visibly: 0.60 flipped to units gave
// 0.049999999999999996, which the cost row's CharLimit cut to
// "0.049999999999", and Enter posted that for an item priced 0.05.
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
	"math/big"
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

// poUnitFromCase and poCaseFromUnit convert a price between the two bases in
// float64. They are for DERIVED PROSE only — the "$48.00/case ÷ 24 =
// $2.00/unit" line, the cart's extended total, and the fallback for a box
// holding something no decimal reader can take.
//
// Nothing that reaches a typed BOX or the PAYLOAD may come from them, and that
// is not a style rule: money is decimal and float64 is binary, so 0.6/12 is
// 0.049999999999999996 and 0.05*12 is 0.6000000000000001. Rendered into a box
// those are a price the operator never typed — and the add-line cost row's
// CharLimit of 14 then CUT one to "0.049999999999", which Enter posted. Use
// poUnitCostFrom / poCaseCostFrom / poUnitCostValue, which convert the decimal
// exactly.
func poUnitFromCase(caseCost float64, qpp int) float64 {
	return caseCost / float64(poPackSize(qpp))
}

func poCaseFromUnit(unitCost float64, qpp int) float64 {
	return unitCost * float64(poPackSize(qpp))
}

// poUnitCostPlaces is how far a derived per-unit price is carried. OMS's
// unit_cost_ordered is a 4-place decimal, so anything past that is thrown away
// server-side; carrying fewer would be this client rounding a price it was not
// asked to round.
//
// It is the QUANTISATION POINT and there is exactly one of it, because the
// alternative is a screen showing one price and the order recording another.
// A case price that does not divide evenly has no exact per-unit form at any
// number of places — 10.00 over 3 is 3.333… forever — so the choice is not
// between rounding and not rounding, it is between rounding HERE, where the
// operator can see the figure, and rounding on the server after they have
// committed.
const poUnitCostPlaces = 4

// poUnitCostFrom is what a UNIT-cost box is filled with: the per-base-unit
// price derived from a case price the operator or the server stated as the
// decimal string `raw` (with `cost` the same figure already parsed as a float,
// for the fallback).
//
// It is poCaseCostFrom's other half and exists for the same reason: the divide
// is on MONEY, and doing it in float64 put 0.049999999999 into the box for an
// item priced 0.05 and posted it. The quotient is exact in big.Rat and is
// quantised at poUnitCostPlaces — the column the value is going to land in
// anyway — so what the box shows and what the wire records cannot disagree.
func poUnitCostFrom(raw string, cost float64, qpp int) string {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(raw))
	if poDecimalPlaces(raw) < 0 || !ok {
		return poFormatCost(poUnitFromCase(cost, qpp))
	}
	unit := new(big.Rat).Quo(r, new(big.Rat).SetInt64(int64(poPackSize(qpp))))
	return poTrimCostZeros(unit.FloatString(poUnitCostPlaces))
}

// poUnitCostValue is the same derivation for a caller that needs the NUMBER
// rather than the box string — the create payload's unit_cost is a *float64 and
// stays one, so the conversion is done exactly first and the wire's own type is
// produced from the result, never the other way round.
func poUnitCostValue(raw string, cost float64, qpp int) float64 {
	v, err := strconv.ParseFloat(poUnitCostFrom(raw, cost, qpp), 64)
	if err != nil {
		return poUnitFromCase(cost, qpp)
	}
	return v
}

// poCaseCostFrom is what a case-cost BOX is filled with: the price of one case,
// derived from a per-base-unit price the server or the operator stated as the
// decimal string `raw` (with `unit` the same figure already parsed as a float).
//
// It multiplies in big.Rat rather than float64 because the product is the one
// the operator is being asked to confirm as "what the vendor charges", and a
// binary multiply of a decimal money value is inexact for ordinary prices: a
// vendor shipping 12 to a case at a stored 0.0500 gives 0.05*12 =
// 0.6000000000000001 in float, which the box then showed at full precision and
// its CharLimit cut to "0.600000000000". Exactly the defect the review found in
// the cart row one surface over, on the row this whole file exists for.
//
// The places come from the INPUT, because multiplying a decimal by an integer
// cannot need more of them: the result is exact by construction, so nothing is
// rounded here and a case size that genuinely needs four places keeps them.
// Trailing zeros go the way poTrimCostZeros describes, which is what keeps a
// 4.5000 unit price rendering as the "112.5" box the rows already carried.
//
// A string neither a plain decimal nor readable as a rational falls back to the
// float multiply: an unreadable box is the submit's to refuse in its own words
// (readCostRow), and declining to convert it here would leave a value standing
// under a label that has already flipped.
func poCaseCostFrom(raw string, unit float64, qpp int) string {
	places := poDecimalPlaces(raw)
	r, ok := new(big.Rat).SetString(strings.TrimSpace(raw))
	if places < 0 || !ok {
		return poFormatCost(poCaseFromUnit(unit, qpp))
	}
	cas := new(big.Rat).Mul(r, new(big.Rat).SetInt64(int64(poPackSize(qpp))))
	return poTrimCostZeros(cas.FloatString(places))
}

// poDecimalPlaces counts the digits a PLAIN decimal string carries after the
// point, and answers -1 for anything that is not one — an exponent, a fraction,
// a sign, a stray letter.
//
// It is deliberately NOT the money rule readCostRow enforces on the typed row:
// that reader keeps its own scan inside itself so no second, looser judge of
// what may be SUBMITTED can grow beside it. This answers a narrower question —
// how many decimal places an exact conversion has to render — and its "no"
// costs nothing but the float fallback.
func poDecimalPlaces(raw string) int {
	raw = strings.TrimSpace(raw)
	digits, places, dot := 0, 0, false
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits++
			if dot {
				places++
			}
		case r == '.' && !dot:
			dot = true
		default:
			return -1
		}
	}
	if digits == 0 {
		return -1
	}
	return places
}

// poTrimCostZeros drops a derived price's trailing zeros (and a bare trailing
// dot), so an exact 2.00 posts as "2" rather than "2.0000". The value is
// unchanged — DRF parses both to the same Decimal — and the string is what the
// frame ECHOES back, where four dead zeros read as precision nobody typed.
func poTrimCostZeros(v string) string {
	if !strings.Contains(v, ".") {
		return v
	}
	v = strings.TrimRight(v, "0")
	return strings.TrimSuffix(v, ".")
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
