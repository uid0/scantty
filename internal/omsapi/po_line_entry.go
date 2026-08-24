// Adding a line to a draft purchase order by typing or scanning an identifier
// (OMS oms-po-add-item, backend PR #1020).
//
// Two endpoints, and the split between them is the whole design:
//
//	GET  /api/reorders/purchase-orders/{id}/item-lookup/?q=…
//	     A PURE READ. It resolves one identifier IN THE CONTEXT OF THAT ORDER'S
//	     SUPPLIER and commits nothing. Everything a confirm screen needs is in
//	     the reply: which item, why it matched, who supplies it, what a fresh
//	     line would land on, and whether the order already carries that item.
//
//	POST /api/reorders/purchase-orders/{id}/items/
//	     The write. Names what to add exactly ONE way — `identifier`,
//	     `item_supplier`, `asset` or `description`.
//
// ScanTTY drives the same two endpoints the web app drives, and it does NOT
// re-implement any part of them. In particular:
//
//   - The MATCH LADDER is the server's. An identifier is classified into a tier
//     (unit barcode, package barcode, supplier SKU, item SKU, item name, then
//     the three "partial" tiers, then another vendor's listing), and the tiers
//     are what decide whether one answer is obvious. `Resolves` is the server
//     saying "the strongest tier that matched holds exactly one candidate, so
//     you may add straight from this without asking the operator anything".
//     A client that recomputed that from `len(candidates)` would disagree with
//     the server the moment an exact hit came back alongside a partial-name
//     match — which is the ordinary case, not a corner.
//
//   - AMBIGUITY is the server's. When the strongest tier holds more than one,
//     the operator picks; `Candidates` is the choice set, already ordered
//     strongest-tier-first.
//
//   - REFUSALS are the server's. The order must be a draft, the supplier must
//     actually carry the item, and a discontinued relationship is refused with
//     its own reason. None of those is checked here, because a check duplicated
//     client-side is a check that can disagree.
//
// # A rival vendor's code still finds the item
//
// `ItemSupplier.unit_upc` is blank on most rows, and a shop buys the same part
// from several vendors, so the barcode on the box in front of the operator
// routinely belongs to a DIFFERENT vendor's listing. The server resolves the
// identifier to the ITEM first and then offers THIS supplier's own row for it,
// so nothing is ever substituted — and it says so, in `MatchKind`
// (POLineMatchOtherSupplier) and in `MatchLabel`, which names the vendor whose
// listing matched. A screen that dropped that label would be silently
// presenting one vendor's code as another's, so it is carried through to the
// confirm frame (internal/tui/po_add_line.go).
//
// # The refusal body is NOT the standard envelope
//
// `add_item` returns its refusals as a hand-built `{"error": "<prose>",
// "code": "<code>"}` (plus `"candidates"` on a 409), which does NOT go through
// OMS's DRF exception handler and therefore does NOT arrive in the
// `{"error": {"code", "message"}}` shape parseError understands. parseError
// falls through and puts the ENTIRE raw body into APIError.Message, so without
// help here the operator reads
// `oms: http 400: {"error": "Acme no longer supplies…", "code": "discontinued"}`
// — a raw dump on the one step where losing the reason costs the whole line.
// AsLineEntryError recovers the sentence and the code from it; everything else
// (a DRF validation error, a gateway page, a network failure) is left exactly
// as it arrived.
//
// Source of truth: backend/reorder_queue/services/line_entry.py and
// backend/reorder_queue/views.py (PurchaseOrderViewSet.item_lookup / .add_item).
package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Match tiers, strongest first — MATCH_TIERS in line_entry.py. They are here so
// a screen can say WHY something matched and can tell an exact hit from a
// typed-a-few-letters one without parsing MatchLabel, which is prose and is
// overridden per candidate for the cross-vendor case.
const (
	POLineMatchUnitBarcode    = "unit_barcode"
	POLineMatchPackageBarcode = "package_barcode"
	POLineMatchVendorSKU      = "vendor_sku"
	POLineMatchItemSKU        = "item_sku"
	POLineMatchItemName       = "item_name"
	POLineMatchPartialVendor  = "partial_vendor_sku"
	POLineMatchPartialItemSKU = "partial_item_sku"
	POLineMatchPartialName    = "partial_item_name"
	// POLineMatchOtherSupplier is the weakest tier: the identifier is not one of
	// THIS supplier's, but it names an item they do carry. The candidate offered
	// is still this supplier's own row — see the file comment.
	POLineMatchOtherSupplier = "other_supplier_listing"
)

// Refusal codes the add endpoint returns in `code`. A client branches on these
// rather than on the prose; the prose is what the operator reads.
const (
	POLineErrNotDraft         = "not_draft"
	POLineErrAmbiguous        = "ambiguous"
	POLineErrNoMatch          = "no_match"
	POLineErrNotSupplied      = "not_supplied"
	POLineErrDiscontinued     = "discontinued"
	POLineErrSupplierMismatch = "supplier_mismatch"
	POLineErrLineVoided       = "line_voided"
)

// POLineItemRef is the item a candidate names. IsKit matters to a purchasing
// screen: a kit is ordered as one SKU and credits its COMPONENTS on receipt,
// never its own stock (AGENTS.md, internal/omsapi/kits.go).
type POLineItemRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	SKU   string `json:"sku"`
	IsKit bool   `json:"is_kit"`
}

// POLineExisting is what a REPEAT add would do to a line the order already
// carries — the numbers a confirm screen shows.
//
// RepeatIncrement is the quantity an add carrying NO explicit quantity adds
// (one supplier package, not the whole reorder suggestion), and
// QuantityOrderedAfter is where that leaves the line. Both are null — 0 here,
// with IsVoided true — for a voided line, because that add is refused outright
// rather than resurrecting it, and quoting an outcome for it would be a lie.
type POLineExisting struct {
	LineItem             string `json:"line_item"`
	QuantityOrdered      int    `json:"quantity_ordered"`
	IsVoided             bool   `json:"is_voided"`
	RepeatIncrement      *int   `json:"repeat_increment"`
	QuantityOrderedAfter *int   `json:"quantity_ordered_after"`
}

// POLineCandidate is one orderable supplier-catalogue row the identifier named,
// and why it matched.
//
// SuggestedQuantity / SuggestedUnitCost are what a FRESH line would land on —
// the item's own reorder maths rounded to a whole supplier package, and the
// price from the supplier relationship falling back to what this item last
// actually cost from this supplier. They are the defaults a prompt prefills.
//
// They are NOT what a repeat add lands on: when AlreadyOnOrder is non-nil the
// add GROWS that line instead, by RepeatIncrement, and leaves its price alone
// unless one is sent. A screen that prefilled SuggestedUnitCost on that path
// and posted it would silently reprice a line the operator only meant to add
// one more box to.
//
// SuggestedUnitCost of "0.00" is a real answer and not an absence: it means the
// supplier relationship carries no price AND this item has never been bought
// from this supplier, so there is nothing on file. It is still the default the
// server would apply, so it is still what a prompt shows — with the fact said
// out loud beside it, because a zero accepted by reflex is a zero-priced line.
type POLineCandidate struct {
	ItemSupplier       int             `json:"item_supplier"`
	MatchKind          string          `json:"match_kind"`
	MatchLabel         string          `json:"match_label"`
	MatchedValue       string          `json:"matched_value"`
	IsExact            bool            `json:"is_exact"`
	Item               POLineItemRef   `json:"item"`
	SupplierSKU        string          `json:"supplier_sku"`
	PackageUPC         string          `json:"package_upc"`
	UnitUPC            string          `json:"unit_upc"`
	QuantityPerPackage int             `json:"quantity_per_package"`
	SuggestedQuantity  int             `json:"suggested_quantity"`
	SuggestedUnitCost  DecimalString   `json:"suggested_unit_cost"`
	AlreadyOnOrder     *POLineExisting `json:"already_on_order"`
}

// FromAnotherVendor reports that the identifier matched some OTHER vendor's
// listing for this item. The candidate is still this supplier's own row, so
// nothing is being substituted — but the operator is owed the provenance,
// because the code they scanned is not the code this order will carry.
func (c POLineCandidate) FromAnotherVendor() bool {
	return c.MatchKind == POLineMatchOtherSupplier
}

// POLineUnavailable is an item the identifier really does name that this order
// still cannot carry, with the server's own explanation.
//
// It is the difference between "found nothing" and "found it, and here is why
// it cannot go on THIS order" — two facts an operator acts on differently, and
// the server keeps them apart precisely so a client does not have to guess.
type POLineUnavailable struct {
	Item    POLineItemRef `json:"item"`
	Reason  string        `json:"reason"`
	Message string        `json:"message"`
}

// POLineLookupOrder is the order the lookup was run against. CanAddItems is the
// server's own draft check, reported rather than re-derived: a client that
// compared Status to "draft" itself would be a second copy of a rule that lives
// in assert_addable.
type POLineLookupOrder struct {
	ID          string `json:"id"`
	Number      string `json:"po_number"`
	Status      string `json:"status"`
	CanAddItems bool   `json:"can_add_items"`
}

// POLineSupplierRef is the supplier the lookup was scoped to.
type POLineSupplierRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// POLineLookup is the whole answer to one identifier.
//
// Resolves is the SERVER saying a client may add straight from this without
// asking the operator anything — exactly the rule resolve_identifier applies.
// Do not re-derive it: it is "the strongest tier that matched holds exactly
// one candidate", which is not len(Candidates) == 1 and is not
// Candidates[0].IsExact either.
//
// TotalCandidates / BestMatchTotal / Truncated (and the matching trio for
// Unavailable) are the PRE-CAP counts: both lists are capped server-side at 20,
// so a client rendering either has to be able to say that more matched. Showing
// the cap as if it were the total sends an operator hunting for an item that
// was never in the list.
type POLineLookup struct {
	Query         string            `json:"query"`
	Supplier      POLineSupplierRef `json:"supplier"`
	PurchaseOrder POLineLookupOrder `json:"purchase_order"`
	BestMatchKind string            `json:"best_match_kind"`
	Resolves      bool              `json:"resolves"`

	Candidates      []POLineCandidate `json:"candidates"`
	TotalCandidates int               `json:"total_candidates"`
	BestMatchTotal  int               `json:"best_match_total"`
	Truncated       bool              `json:"truncated"`

	Unavailable          []POLineUnavailable `json:"unavailable"`
	TotalUnavailable     int                 `json:"total_unavailable"`
	UnavailableTruncated bool                `json:"unavailable_truncated"`
}

// POLineAdd is the add request. Name what is being bought EXACTLY ONE way; the
// server refuses anything else with a validation error rather than guessing.
//
// ScanTTY's flow always sends ItemSupplier — the exact catalogue row the
// operator looked at and confirmed — never Identifier. Both are legal, but
// re-posting the identifier would RE-RESOLVE it, and the catalogue can change
// between the lookup and the confirm: the operator would then have approved one
// item and added another. Sending the row id makes the add unambiguous by
// construction, and every guard (supplier, discontinued, draft) still runs
// server-side against it.
//
// Quantity and UnitCost are omitted when zero-valued so the server applies its
// own defaults; the flow fills them in from the candidate's suggestions, which
// are those same defaults, so an operator who accepts the prompt gets exactly
// what a bare scan would have produced.
type POLineAdd struct {
	Identifier   string `json:"identifier,omitempty"`
	ItemSupplier int    `json:"item_supplier,omitempty"`
	Asset        string `json:"asset,omitempty"`
	Description  string `json:"description,omitempty"`
	Quantity     int    `json:"quantity,omitempty"`
	// UnitCost is a string so "0.00" can be sent deliberately and an empty
	// value can mean "no override" — a float zero cannot express both.
	UnitCost    string `json:"unit_cost,omitempty"`
	Notes       string `json:"notes,omitempty"`
	WorkOrder   string `json:"work_order,omitempty"`
	OwningGroup int    `json:"owning_group,omitempty"`
}

// POLineAdded is the add response. Created is false when an existing line was
// GROWN rather than a new one inserted, which is a different sentence to show
// the operator. PurchaseOrder is the full refreshed order, so a caller can
// patch its view in place without a second fetch.
type POLineAdded struct {
	Created       bool              `json:"created"`
	LineItem      PurchaseOrderItem `json:"line_item"`
	Match         *POLineCandidate  `json:"match"`
	PurchaseOrder *PurchaseOrder    `json:"purchase_order"`
}

// POLineEntryError is a refusal from the add endpoint, recovered from the
// hand-built `{"error", "code", "candidates"}` body it returns (see the file
// comment). Message is the server's own operator-facing sentence and is what a
// screen shows; Code is what it branches on.
type POLineEntryError struct {
	Status     int               `json:"-"`
	Message    string            `json:"error"`
	Code       string            `json:"code"`
	Candidates []POLineCandidate `json:"candidates,omitempty"`
}

func (e *POLineEntryError) Error() string { return e.Message }

// Ambiguous reports the 409 that carries a choice set.
func (e *POLineEntryError) Ambiguous() bool { return e.Code == POLineErrAmbiguous }

// AsLineEntryError recovers the add endpoint's refusal from whatever parseError
// made of it, and reports false for anything that is not one.
//
// It is deliberately narrow. A body that does not start with `{`, does not
// parse, or parses without BOTH a message and a code is left alone — a gateway's
// HTML page and a DRF validation envelope are not this shape and must keep
// arriving as the APIError they already are, rather than being coerced into a
// refusal the server never made.
func AsLineEntryError(err error) (*POLineEntryError, bool) {
	var entry *POLineEntryError
	if errors.As(err, &entry) {
		return entry, true
	}
	var api *APIError
	if !errors.As(err, &api) {
		return nil, false
	}
	body := strings.TrimSpace(api.Message)
	if !strings.HasPrefix(body, "{") {
		return nil, false
	}
	var out POLineEntryError
	if json.Unmarshal([]byte(body), &out) != nil {
		return nil, false
	}
	if out.Message == "" || out.Code == "" {
		return nil, false
	}
	out.Status = api.Status
	return &out, true
}

// LookupPurchaseOrderLine resolves one typed or scanned identifier against the
// order's supplier WITHOUT adding anything.
//
// A blank query is not sent: the server answers it with an empty result, and a
// request whose answer is known is a round trip an operator waits through for
// nothing. The empty result is returned verbatim so callers see the same shape
// either way.
func (c *Client) LookupPurchaseOrderLine(ctx context.Context, poID, query string) (*POLineLookup, error) {
	var out POLineLookup
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/item-lookup/", poID)
	if err := c.Get(ctx, path, url.Values{"q": {query}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddPurchaseOrderLine adds (or grows) one line on a DRAFT purchase order.
//
// A refusal comes back as a *POLineEntryError carrying the server's own
// sentence; everything else is passed through untouched.
func (c *Client) AddPurchaseOrderLine(ctx context.Context, poID string, req POLineAdd) (*POLineAdded, error) {
	var out POLineAdded
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/items/", poID)
	if err := c.Post(ctx, path, req, &out); err != nil {
		if entry, ok := AsLineEntryError(err); ok {
			return nil, entry
		}
		return nil, err
	}
	return &out, nil
}
