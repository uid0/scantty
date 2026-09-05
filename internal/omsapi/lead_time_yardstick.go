package omsapi

// The one place this client spells what a lateness or variance figure is
// measured against.
//
// A LeadTimeLog row on the OMS side holds TWO promises and only one of them is
// scored. `estimated_lead_time_days` is the supplier link's STANDING QUOTE
// (`ItemSupplier.average_lead_time`, read at receipt time), and `variance_days`
// — with every rate derived from it — is measured against that and nothing
// else. `expected_delivery_date` is the date the operator confirmed on the
// purchase order, and no column scores it. So a vendor that quotes three days,
// is confirmed for day ten and delivers on day ten carries
// `expected_delivery_date == actual_delivery_date` ALONGSIDE
// `variance_days: 7`: it met the date it agreed while missing the lead time it
// advertises.
//
// That is deliberate and is not ours to reopen — `inventory.services.
// supplier_selection` scores the standing quote and discounts it by how often
// the vendor broke it, and scoring the discount against a per-order date would
// let a vendor quote three, confirm ten, deliver ten and win on both axes.
// Because the number is right and only the RENDERING can lie, OMS's
// `LeadTimeLog` docstring makes it a rule that no screen, payload or export may
// show one of these figures without naming the yardstick, and serves the
// yardstick alongside every one of them as `variance_measured_against`
// (OMS PR #1046, `LeadTimeLog.VARIANCE_YARDSTICK`).
//
// THE KEY IS READ, NEVER ASSUMED. An OMS too old to serve it has not said the
// figures are scored against the quote — it has said nothing — and a client
// that fills the silence in with the current answer is asserting a promise
// nobody made. LeadTimeYardstick therefore decodes to "" in that case and the
// surfaces say the server did not state it: "could not tell" and "found
// nothing" are different facts.
//
// Which surfaces name it, and how, is `internal/tui/report_table.go`'s
// (`reportYardstickLegend`); this file is only the wire.

// VarianceYardstickQuotedLeadTime is the one value OMS serves today — the
// machine name of `LeadTimeLog.VARIANCE_YARDSTICK`. A payload naming anything
// else is a yardstick this client has no prose for, which is a different fact
// again from an absent key, so callers match on this constant rather than
// treating every non-empty answer as the quote.
const VarianceYardstickQuotedLeadTime = "quoted_lead_time"

// LeadTimeYardstick is the `variance_measured_against` key OMS serves beside
// every lateness or variance figure. It is embedded rather than repeated so the
// three report payloads carrying it cannot drift into decoding three different
// keys, and so the reason above lives in one place.
//
// "" means the payload did not say. It is never a shorthand for the quote.
type LeadTimeYardstick struct {
	VarianceMeasuredAgainst string `json:"variance_measured_against"`
}
