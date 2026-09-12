package tui

import (
	"fmt"
	"math/big"

	"github.com/uid0/scantty/internal/omsapi"
)

// THE SERVER ACCEPTS A TYPO, SO THE TERMINAL SAYS SO BEFORE SENDING IT.
//
// Measured against a real OMS (2026-09-11, remote `main` tree 2d9c8f9c — the
// recordings and the method are in internal/omsapi/testdata/README.md), none of
// these is refused:
//
//	absolute 120       against a meter reading 1250.5   -> 201, delta -1130.5
//	absolute 12505000  against the same meter           -> 201
//	absolute -5        against the same meter           -> 201
//
// `apply_reading` computes `delta = value_after - current_value` and stores what
// it is handed. There is no monotonicity rule, no magnitude bound, and no
// refusal to relay — so "surface the server's refusal legibly" has nothing to
// surface here, and the choice is between sending a probable typo in silence and
// naming it first.
//
// WHAT DECIDES IT IS THAT THE DAMAGE IS ONE-WAY. For a `runtime_hours` meter
// `apply_reading` adds a positive advance to `Asset.hours_used` through an
// `F()` update and deliberately does NOT decrement on the way back down ("it is
// a monotonic cumulative counter"), and `Asset.hours_used` is what the existing
// maintenance forecast reads. Measured: 1250.5 -> 120 -> 12505000 -> -5 left
// `hours_used` at 12506130, and no endpoint in this client can bring it back.
// So an extra digit typed into a runtime meter permanently inflates the forecast
// input, and the `adjust` action — which fixes the METER — does not touch it.
// That is a loss the operator cannot undo from anywhere in this program, which
// is exactly the shape the rest of this codebase confirms before committing.
//
// IT NAMES, IT DOES NOT REFUSE. Nothing here alters a value, and nothing here
// declines to send one: a meter really can be replaced, a counter really can be
// reset to zero, and an operator who means it presses one more key. Silently
// correcting or rejecting a value the server would accept would be the worse
// defect, and a refusal the operator cannot satisfy from the frame it is drawn
// on is a dead end (receive_form.go's rule, applied here).

// meterEntryVerdict is what a proposed write looks like against the meter as it
// stands: the total it would leave, and what is odd about it.
//
// Suspicious is the one predicate the confirm phase and the submit arm both
// read, so the frame that is drawn and the key that is gated cannot come apart.
type meterEntryVerdict struct {
	// Result is the total the meter would read afterwards, "" when the entry
	// could not be evaluated (an unparseable box, or a meter whose current
	// value the server never sent).
	Result omsapi.DecimalString
	// Backwards is true when the entry would move a cumulative counter DOWN.
	Backwards bool
	// Magnitude is true when the entry would multiply the meter by ten or more
	// — the shape of one extra digit.
	Magnitude bool
	// Negative is true when the meter would end up below zero, which no
	// cumulative counter reaches by counting.
	Negative bool
	// Unchecked is true when the screen could not confirm that current is still
	// the value held by the server.
	Unchecked bool
}

// Suspicious reports whether this entry is worth a word before it is sent.
func (v meterEntryVerdict) Suspicious() bool {
	return v.Backwards || v.Magnitude || v.Negative || v.Unchecked
}

// meterMagnitudeFactor is how far up an entry has to go before it reads as a
// slipped digit rather than a long shift.
//
// TEN, because that is what one extra digit multiplies by. A threshold below ten
// would fire on an honest first reading after a long gap — a meter left at 12
// hours over a quiet month and read back at 200 is ordinary — and the cost of a
// confirm nobody needed is a keystroke, while the cost of one nobody got is a
// forecast input that cannot be corrected.
var meterMagnitudeFactor = big.NewRat(10, 1)

// meterEntryCheck evaluates a proposed record-reading against the meter.
//
// current is what the server last served for this meter; typed is the operator's
// box; absolute says which of the two things the box means. A meter whose
// current value did not decode, or a box that is not a number, yields a verdict
// with no Result and nothing suspicious: there is nothing to compare, and a
// warning built on a number nobody has is worse than none. The box being
// unparseable is the SUBMIT's refusal to make, not this one's.
//
// THE MAGNITUDE TEST IS SKIPPED AT A CURRENT VALUE OF ZERO, and that is a
// decision rather than an oversight: every entry against a fresh meter is
// infinitely many times its current value, so the test would fire on the FIRST
// reading of every meter anybody ever created — a confirm that is always shown
// is a confirm nobody reads, which would cost exactly the attention the real one
// needs.
func meterEntryCheck(current omsapi.DecimalString, typed string, absolute bool) meterEntryVerdict {
	now, haveNow := meterRat(current.String())
	entry, ok := meterRat(typed)
	if !ok {
		return meterEntryVerdict{}
	}
	if !haveNow {
		// No current value to compare against — a total can still be reported
		// for an absolute entry, and nothing can be called odd.
		if absolute {
			return meterEntryVerdict{Result: omsapi.DecimalString(meterRatText(entry))}
		}
		return meterEntryVerdict{}
	}

	result := new(big.Rat).Set(entry)
	if !absolute {
		result = new(big.Rat).Add(now, entry)
	}

	v := meterEntryVerdict{Result: omsapi.DecimalString(meterRatText(result))}
	v.Negative = result.Sign() < 0
	v.Backwards = result.Cmp(now) < 0
	if now.Sign() > 0 {
		v.Magnitude = result.Cmp(new(big.Rat).Mul(now, meterMagnitudeFactor)) >= 0
	}
	return v
}

// meterAdjustCheck evaluates a proposed ADJUSTMENT.
//
// It is a DIFFERENT check from the one above and deliberately narrower: going
// backwards is what an adjustment is FOR — a recount that came in lower is the
// textbook case, and `adjust` exists precisely so the correction is recorded as
// one — so a "this goes backwards" confirm there would fire on the correct use
// of the instrument and teach the operator to dismiss it. Going UP by an order
// of magnitude is a slipped digit whichever action typed it, and a negative
// total is not a state a cumulative counter reaches, so both of those still
// speak.
func meterAdjustCheck(current omsapi.DecimalString, typed string) meterEntryVerdict {
	v := meterEntryCheck(current, typed, true)
	v.Backwards = false
	return v
}

// meterVerdictReason is the sentence that says what is odd, in the operator's
// terms and with both figures in it.
//
// It is one line and it NAMES BOTH NUMBERS with their unit, because the whole
// question the operator is being asked is "did you mean to go from A to B" and a
// warning that states only the conclusion cannot be checked against the machine
// in front of them. Where more than one thing is odd the most alarming is the
// one said — a negative total is a worse fact than a big jump, and both being
// listed would spend the row on a conjunction.
func meterVerdictReason(m omsapi.AssetMeter, v meterEntryVerdict) string {
	from := meterFigure(m.CurrentValue, m.Unit, m.MeterTypeDisplay)
	to := meterFigure(v.Result, m.Unit, m.MeterTypeDisplay)
	var reason string
	switch {
	case v.Negative:
		reason = fmt.Sprintf("that leaves the meter at %s — below zero, which counting never reaches.", to)
	case v.Backwards:
		reason = fmt.Sprintf("that moves the meter BACKWARDS, from %s to %s.", from, to)
	case v.Magnitude:
		reason = fmt.Sprintf("that multiplies the meter by ten or more, from %s to %s — the shape of one extra digit.", from, to)
	}
	if v.Unchecked {
		unchecked := "This meter's current value could not be confirmed against the server right now, so this entry could not be checked against the server."
		if reason == "" {
			return unchecked
		}
		return reason + " " + unchecked
	}
	return reason
}

// meterRuntimeCaveat is the part that cannot be taken back, said only where it
// is TRUE.
//
// It is gated on the meter TYPE because the dual-write is: `apply_reading`
// touches `Asset.hours_used` only for a `runtime_hours` meter. On a gallons or
// cycles meter an over-reading is fully correctable with `adjust`, and saying
// otherwise there would be a warning the code cannot honour — which this
// codebase treats as the same defect as silence about a real loss.
func meterRuntimeCaveat(m omsapi.AssetMeter, v meterEntryVerdict) string {
	if m.MeterType != omsapi.MeterTypeRuntimeHours {
		return ""
	}
	if v.Backwards || v.Negative {
		// Going DOWN never reaches the dual-write at all: the server adds only
		// a positive advance. What is already in hours_used stays, but this
		// entry adds nothing to it.
		return "The asset's logged hours do not follow a meter downwards, so this leaves them where they are."
	}
	if !v.Magnitude {
		return ""
	}
	return "This is a runtime meter, so the advance is ALSO added to the asset's logged hours, " +
		"which the maintenance forecast reads. An adjustment corrects the meter and never " +
		"takes those hours back — nothing in this program does."
}
