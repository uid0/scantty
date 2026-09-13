package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// A supplier lead time is drawn WITH where it came from, and this file is the
// one place that spells it.
//
// The defect is a sentence about the operator rather than about the wire: OMS
// stores a planning default of 7 whenever nobody recorded a lead time, so a bare
// "lead 7d" reads exactly like a supplier who genuinely promised a week, and an
// operator choosing who to buy from cannot tell the two apart. OMS now serves
// `average_lead_time_source` beside the number (omsapi.LeadTimeSource carries
// the wire note); every surface that draws a SUPPLIER LINK's lead time appends
// leadTimeMark to it.
//
// THE DERIVED SET is every render of `ItemSupplier.average_lead_time` or the
// item serializer's flat mirror of it, found by asking what can put that number
// in front of a person rather than by grepping one spelling:
//
//   - the item detail's `Avg lead time` line (flat, primary link) and its
//     `All suppliers` meta rows (inventory_detail.go);
//   - the supplier detail's item rows (simple_details.go);
//   - the item's Suppliers screen rows (item_suppliers.go), and the edit form's
//     lead-time box, whose hint names the source of the number it was opened on;
//   - the item form's read-only supplier grid (inventory_item_form_suppliers.go);
//   - the New PO item picker's rows (po_create_pickers.go).
//
// DELIBERATELY OUTSIDE IT, because OMS serves no source for them — each is a
// number DERIVED from link lead times rather than a link's own, and OMS's
// `lead_time_source` docstring names the class as not carrying it: the item
// metrics `Lead` column, both demand forecasts' `lead_time_days`, and the
// lead-time analytics reports (whose yardstick is a different fact again, see
// omsapi.LeadTimeYardstick). Marking those would assert a provenance nobody
// served.
//
// THREE STATES OF SILENCE ARE KEPT APART. An absent key (an OMS predating the
// marker) draws the number exactly as before — no mark, because the server said
// nothing and the terminal must not invent an answer. `unknown` is the server
// SAYING it cannot tell, and draws as such. A value this client has no words for
// is provenance that could not be established here, so it draws as unknown too
// rather than as whichever known source it happens to resemble.

// The marks, one per provenance. `recorded` is drawn "quoted": it is the number
// a person or client SUPPLIED — an operator typing what the vendor said — which
// is what the operator weighing a purchase needs to hear set against "default".
const (
	leadMarkQuoted   = "(quoted)"
	leadMarkDefault  = "(default)"
	leadMarkMeasured = "(measured)"
	leadMarkUnknown  = "(unknown)"
)

// leadTimeMark is the provenance mark for a served source, or "" when the
// payload did not carry one.
func leadTimeMark(src omsapi.LeadTimeSource) string {
	switch src {
	case "":
		return ""
	case omsapi.LeadTimeSourceRecorded:
		return leadMarkQuoted
	case omsapi.LeadTimeSourceDefault:
		return leadMarkDefault
	case omsapi.LeadTimeSourceMeasured:
		return leadMarkMeasured
	default:
		return leadMarkUnknown
	}
}

// leadTimeDays keeps the number in the `%gd` spelling every surface already
// used, including fractional averages such as "14.5d".
func leadTimeDays(days float64) string {
	return fmt.Sprintf("%gd", days)
}

// leadTimeText is the number followed by its mark ("7d (default)"), or the bare
// number when no source was served. Callers that fold or drop a reading WHOLE
// use this, so the number and its mark are never separated by a fold.
func leadTimeText(days float64, src omsapi.LeadTimeSource) string {
	if mark := leadTimeMark(src); mark != "" {
		return leadTimeDays(days) + " " + mark
	}
	return leadTimeDays(days)
}

// leadTimeFactCell draws value-and-mark into a grid column reserved at w, and
// it is where the one TRADE this rendering can face is decided: when the column
// cannot hold both, the MARK survives and the NUMBER gives, through the same
// fitFactCell every fact cell uses — so a cut number is replaced by the cut mark
// rather than drawn as a smaller real one. Kept the other way round, a clipped
// cell would show a bare "7d", which is precisely the reading this exists to
// stop. Only a column narrower than the mark itself clips the mark, and then
// with fitCell's ellipsis.
func leadTimeFactCell(value, mark string, w int, align colAlign) string {
	if mark == "" || w <= 0 {
		return jdeGridFactCell(value, w, align)
	}
	full := value + " " + mark
	if lipgloss.Width(full) <= w {
		return padCell(full, w, align)
	}
	if room := w - lipgloss.Width(mark) - 1; room >= 1 {
		return padCell(fitFactCell(value, room)+" "+mark, w, align)
	}
	return padCell(fitCell(mark, w), w, align)
}

// leadTimeMarkFloor is the narrowest a marked lead column may be squeezed to:
// the widest mark, a space and one cell for the cut mark standing in for the
// number.
func leadTimeMarkFloor() int {
	floor := 0
	for _, m := range []string{leadMarkQuoted, leadMarkDefault, leadMarkMeasured, leadMarkUnknown} {
		if w := lipgloss.Width(m); w > floor {
			floor = w
		}
	}
	return floor + 1 + lipgloss.Width(paneCutMark)
}
