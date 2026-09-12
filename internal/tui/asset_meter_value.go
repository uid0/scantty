package tui

import (
	"math/big"

	"github.com/charmbracelet/lipgloss"
	"strings"

	"github.com/uid0/scantty/internal/omsapi"
)

// A METER READING IS A NUMBER, AND A NUMBER IS SHOWN WHOLE OR DROPPED AND
// MARKED — never folded, never cut to look complete.
//
// This file is the one place a meter value becomes text and the one place two
// of them are compared. Both halves are here together because they are one
// decision: what counts as "the same number" for display is what must also
// count as the same number for the backwards check, and two implementations of
// that would eventually disagree on the one screen where it decides whether an
// irreversible write is warned about.
//
// The arithmetic is big.Rat and not float64, for the reason po_case_entry.go
// gives about money: the column is `numeric(14,4)`, so a value is exact on the
// wire and a float round-trip is a way to invent digits the record does not
// have. Nothing here ever RE-ROUNDS a served value — trailing zeros come off
// (1289.7500 and 1289.75 are the same number written twice), and no other digit
// is ever removed.

// meterValueText renders a served decimal for display.
//
// It trims trailing zeros after the decimal point, and the point with them when
// nothing is left after it, because the server pads every value to four places
// and `1289.7500 hours` spends four cells of a 51-column pane on nothing. It
// removes no significant digit: 6.0833 comes through whole, and so does a
// fourteen-digit total.
//
// An EMPTY DecimalString means the server sent null or the key was absent, which
// is not zero — it is "no figure". Callers get "" and say so themselves rather
// than drawing a 0 nobody recorded.
func meterValueText(d omsapi.DecimalString) string {
	s := strings.TrimSpace(d.String())
	if s == "" {
		return ""
	}
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		// Every digit was a zero after the point (`0.0000`, `-0.0000`).
		return "0"
	}
	return s
}

// meterFigure is a value and its UNIT, which is the only form either of them
// should ever be shown in.
//
// A runtime-hour reading and a cycle count are not interchangeable and an
// unlabelled number invites the wrong entry, so nothing in the meter screens
// draws a bare figure. The unit is free text on the server and may be blank; a
// blank one falls back to the server's own label for the meter TYPE, and where
// even that is missing the number stands alone rather than being given a unit
// this side invented.
func meterFigure(value omsapi.DecimalString, unit, typeDisplay string) string {
	text := meterValueText(value)
	if text == "" {
		return ""
	}
	if u := meterUnitLabel(unit, typeDisplay); u != "" {
		return text + " " + u
	}
	return text
}

// meterUnitLabel is the words that go beside a figure: the meter's own unit
// where it has one, the server's meter-type label where it does not.
func meterUnitLabel(unit, typeDisplay string) string {
	if u := strings.TrimSpace(unit); u != "" {
		return u
	}
	return strings.TrimSpace(typeDisplay)
}

// meterSignedFigure is meterFigure for a LEDGER DELTA, which is the one place a
// leading `+` is drawn.
//
// A delta's sign is the whole of what it says — an advance or a correction
// back — and a bare `48.25` beside a `-9` reads as a different KIND of row
// rather than the other direction of the same one. The server sends no `+`, so
// it is added here and only here.
func meterSignedFigure(delta omsapi.DecimalString, unit, typeDisplay string) string {
	text := meterValueText(delta)
	if text == "" {
		return ""
	}
	if !strings.HasPrefix(text, "-") && text != "0" {
		text = "+" + text
	}
	if u := meterUnitLabel(unit, typeDisplay); u != "" {
		return text + " " + u
	}
	return text
}

// meterRat parses a served or typed decimal exactly.
//
// ok is false for anything that is not a number — including the empty string,
// which is why a caller can ask this one question of an operator's box and of a
// served value alike. big.Rat's SetString accepts forms a decimal field never
// holds (`1/3`, `1e5`), so the input is screened first: a typed `1e5` would
// otherwise silently become 100000 on a row whose label says hours.
func meterRat(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	body := strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	if body == "" {
		return nil, false
	}
	for i, r := range body {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && strings.Index(body, ".") == i && strings.Count(body, ".") == 1 {
			continue
		}
		return nil, false
	}
	if body == "." {
		return nil, false
	}
	r, ok := new(big.Rat).SetString(s)
	return r, ok
}

// meterDecimalPlaces is what the column really stores: `numeric(14,4)`.
const meterDecimalPlaces = 4

// meterRatText renders an exact rational back into the digits the column holds,
// so a value this side COMPUTED (the total a delta reading would produce) is
// shown in the same form as one the server served.
//
// It quantises at meterDecimalPlaces rather than printing whatever precision the
// arithmetic happened to produce, because a screen must not show a number the
// record cannot hold — the rule po_case_entry.go states for a price and which
// holds here for the same reason.
func meterRatText(r *big.Rat) string {
	if r == nil {
		return ""
	}
	return meterValueText(omsapi.DecimalString(r.FloatString(meterDecimalPlaces)))
}

// visibleCells is the terminal columns a string really occupies.
func visibleCells(s string) int { return lipgloss.Width(s) }

// meterFigureLine draws a figure that has been DROPPED off its row, on a line of
// its own, and it is the one place the "whole or dropped and marked" rule is
// resolved against a pane too narrow to honour every part of it.
//
// The two rules on such a line compete — a number is never cut, and a number
// carries its unit — and at the 16 cells a 45-column terminal leaves they cannot
// both be had for a nine-digit reading. They are ranked, and the ranking is the
// decision:
//
//  1. THE DIGITS ARE NEVER CUT. A cut number reads as a different number, which
//     is silently wrong; every other loss on this line is visibly a loss.
//  2. Where the figure fits whole, it is drawn whole.
//  3. Where it does not, the NUMBER is drawn whole and the UNIT gives, marked.
//     A missing unit is recoverable — the grid's own row and the ledger's header
//     both name the meter, which is what the unit belongs to — and the mark says
//     something was left off.
//  4. Where not even the digits fit, the line is the MARK alone. Nothing is
//     better than a lie, and the header's drop note has already said the reading
//     is down here.
//
// `room` is the cells the line really has; a room of zero or less is the
// columnar layer's "do not truncate" and draws the figure whole.
func meterFigureLine(figure, number string, room int) string {
	if room <= 0 || visibleCells(figure) <= room {
		return figure
	}
	if number == "" {
		return meterCutMark
	}
	// The unit gives, and says so.
	marked := number + " " + meterCutMark
	if visibleCells(marked) <= room {
		return marked
	}
	if visibleCells(number) <= room {
		return number
	}
	return meterCutMark
}

// meterCutMark says something was left off this line. It is the ellipsis every
// clip in this program marks with, used here for a DROP rather than a cut.
const meterCutMark = "…"

// assetPaneCells is the cells the pane REALLY has, unfloored.
//
// jdeScreen.bodyWidth() reads screenBodyWidth, whose floor of 20 is four cells
// more than Root draws at a terminal width of 45 — layout.go says so in as many
// words, and ListScreen keeps its own listPaneCells for exactly this reason. A
// bound that spends cells the pane does not have is not a bound, and on these
// screens what it overspends into is a NUMBER: at 45 columns the confirm's
// headline measured 18 cells against the 14 it had, and clampToBox took the tail
// with no mark.
//
// It is used for the CLIPS that carry a figure or the identity of what an
// irreversible key is about to act on. The rest of the columnar layer measures
// against bodyWidth(), which is the class jdeRowsPastThePane records; that is
// the layer's to fix and is not reopened here.
func assetPaneCells(terminalWidth int) int {
	if terminalWidth <= 0 {
		return 0
	}
	return screenBodyCells(terminalWidth)
}
