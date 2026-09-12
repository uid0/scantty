package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Location-wide stock reconciliation: counting a whole ROOM in one visit
// (OMS oms-90k, extended by op-ev14). Two endpoints, and nothing here
// re-implements either of them:
//
//	GET  /api/inventory/locations/{id}/reconcile/   the GRID — one row per
//	                                                active item stored there
//	POST /api/inventory/reconciliations/batch/      the whole count, at once
//
// The per-ITEM cycle count (CycleCountItem, inventory.go) stays exactly as it
// is. It is the same write applied to one item and it reaches a different
// endpoint; this pair is the room-scale twin, and the two are deliberately not
// folded together — a location count is atomic ACROSS items and a cycle count
// is not, which is the whole difference an operator feels when one row is
// refused.
//
// # WHICH UNIT A NUMBER IS, IS THE THING THIS FILE IS ABOUT
//
// Stock is COUNTED in individual base units by default and a purchase order is
// PLACED in cases; both are correct and neither is converted into the other.
// OMS's own seam for that is `inventory.services.packaging.resolve_base_quantity`,
// and its rule is one sentence: `actual_count` is BASE UNITS unless the row
// says `at_level`, in which case it is a count of whole packs of the item's
// `count_level` ("I counted 3 cases"). An `at_level` sent for an item that is
// NOT counted in packs is a 400 rather than a silent base-unit reading, which
// is the behaviour that makes a wrong flag loud instead of a wrong stock level.
//
// The GRID answers which unit each row should be counted in — that is what
// CountUnit and ProjectedAtUnit are for, and the serializer's own docstring
// says so in as many words. So a client that draws the grid has been TOLD the
// unit per row and has no reason to guess at one.
//
// MinimumStock is in that same COUNT unit and not in base units. That is not a
// quirk of this payload: `InventoryItem.minimum_stock` is re-read as a
// threshold in the item's counting rung for every pack-counting mode (op-es7c),
// which is exactly why the reorder trigger below compares a COUNT against it
// rather than a base-unit figure.
//
// # THE REORDER SIDE EFFECT IS PART OF THE CONTRACT
//
// A submitted row whose counted quantity lands at or below the item's minimum
// AUTO-CREATES a ReorderRequest, unless the row says SkipReorder. OMS's
// condition, verbatim from `_apply_reconciliation_row`:
//
//	not skip_reorder && not item.is_retired && count_at_level(item) <= item.minimum_stock
//
// with `count_at_level` read AFTER the count is applied — so for a row counted
// in its own count unit it is simply the number typed.
// `ReconciliationRow.FilesAReorder` answers the two conjuncts a client can see;
// `is_retired` is NOT on this payload and no honest client can predict it,
// which is why that method's doc says what it cannot decide rather than leaving
// a caller to assume it decided everything.

// LocationReconcileItem is one row of the reconcile grid: an item stored at the
// location, the stock OMS currently believes it holds, and the unit a counter
// should write that count in.
//
// Projected and ProjectedAtUnit are the SAME stock in two units and must not be
// read interchangeably. Projected is canonical base units — what
// `current_stock` holds — and ProjectedAtUnit is that figure expressed in
// CountUnit, so for a case-counted item they differ by the pack size and for an
// each-counted item they are equal. A screen that draws one while labelling it
// with the other is the silently-wrong-unit defect this whole file is written
// against.
type LocationReconcileItem struct {
	// ItemID is the InventoryItem UUID, and is what a batch row names.
	ItemID string `json:"item_id"`
	Name   string `json:"name"`
	SKU    string `json:"sku"`

	// Projected is current_stock in BASE units, always.
	Projected int `json:"projected"`
	// MinimumStock is the reorder threshold in the COUNT unit (see the file
	// note): base units for an each-counted item, whole packs otherwise.
	MinimumStock int `json:"minimum_stock"`
	// ReorderQuantity is how much an auto-created request asks for, in the
	// same count unit. Carried so the grid can say what a reorder would ask
	// for rather than only that one would be filed.
	ReorderQuantity int    `json:"reorder_quantity"`
	OwningGroupName string `json:"owning_group_name"`

	// CountMode is "each", "by_level" or "open_closed" (the CountMode*
	// constants in inventory.go). It is what decides AtLevel on the row this
	// item is submitted as — see CountsInPacks.
	CountMode string `json:"count_mode"`
	// CountUnit is the noun a count for this item is written in: the counting
	// rung's name for a pack-counted item, the item's base unit otherwise.
	// Server-derived, so a client never has to resolve a packaging chain to
	// label a box.
	CountUnit string `json:"count_unit"`
	// ProjectedAtUnit is Projected expressed in CountUnit.
	ProjectedAtUnit int `json:"projected_at_unit"`
	// OpenContainerCount is how many packs are currently OPEN, and is
	// meaningful only for CountModeOpenClosed. Under that mode ProjectedAtUnit
	// is the SEALED count — a partial pack is not a countable pack — so the
	// pair is what fully describes the shelf.
	OpenContainerCount int `json:"open_container_count"`
}

// CountsInPacks reports whether a count for this item is entered in whole packs
// rather than base units, and is therefore the value AtLevel must take on a row
// submitting it.
//
// Read off CountMode, which is the only thing on this payload that answers it.
// The tempting alternative — comparing Projected against ProjectedAtUnit — is
// WRONG in the state a room count is most often in: an item with nothing on the
// shelf has 0 in both units whatever its pack size, so a case-counted item at
// zero would be taken for an each-counted one and "5" would be stored as five
// gloves instead of five boxes.
//
// A half-configured item (a pack-counting mode whose count_level is missing)
// reads as packs here and is REFUSED by the server, whole batch rolled back,
// with the item named. That is the intended failure: OMS's own
// `resolve_base_quantity` is written to raise rather than fall back to base
// units precisely so a unit that cannot be resolved is loud. `InventoryItem`'s
// own clean() requires a count_level for these modes, so the state is not
// reachable through any ordinary write.
func (it LocationReconcileItem) CountsInPacks() bool {
	return it.CountMode != "" && it.CountMode != CountModeEach
}

// Unit is the noun this item's count is written in, falling back to the
// backend's own default base unit for an item that predates the packaging
// matrix and carries no count_unit at all.
func (it LocationReconcileItem) Unit() string {
	if u := strings.TrimSpace(it.CountUnit); u != "" {
		return u
	}
	return "unit"
}

// LocationReconcileGrid is the whole GET payload: the location, and every
// active item stored in it, ordered by name (the server's order, kept).
//
// LocationID is a STRING because the server sends `str(location.pk)` — an
// integer primary key rendered as text. It is carried as sent rather than
// re-parsed: nothing on this side does arithmetic with it.
type LocationReconcileGrid struct {
	LocationID   string                  `json:"location_id"`
	LocationName string                  `json:"location_name"`
	Items        []LocationReconcileItem `json:"items"`
}

// GetLocationReconcileGrid fetches the reconcile grid for one location.
//
// The trailing slash is load-bearing, as everywhere else in this package: the
// unslashed path 301-redirects and the canonical form avoids the hop. A missing
// location answers 404 with `{"detail": "Location not found."}`, which is the
// hand-written shape AsDetailRefusal recovers.
func (c *Client) GetLocationReconcileGrid(ctx context.Context, locationID string) (*LocationReconcileGrid, error) {
	id := strings.TrimSpace(locationID)
	if id == "" {
		return nil, &APIError{Code: "invalid_location", Message: "location id is empty"}
	}
	var out LocationReconcileGrid
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/locations/%s/reconcile/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReconciliationRow is one counted item in a batch submission.
//
// ActualCount is the number the operator counted, in the unit AtLevel names —
// and AtLevel carries NO omitempty on purpose. The server defaults it to false,
// so omitting it would be correct on the wire and unreadable in a log: a
// recorded request would say what was counted without ever saying what it was
// counted in, on the one payload in this program where that is the difference
// between a right stock level and a silently wrong one. Every row states its
// unit.
//
// SkipReorder and Notes keep omitempty because neither is a unit and neither is
// ambiguous when absent: no notes is no notes, and an unsuppressed reorder is
// the documented default the whole workflow is built around.
type ReconciliationRow struct {
	// ItemID is the InventoryItem UUID from the grid row.
	ItemID string `json:"item_id"`
	// ActualCount is the counted quantity, in base units when AtLevel is
	// false and in whole count_level packs when it is true.
	ActualCount int `json:"actual_count"`
	// Reason is one of StockReconciliation.ReasonCode — the same set the
	// per-item cycle count posts.
	Reason      string `json:"reason"`
	Notes       string `json:"notes,omitempty"`
	SkipReorder bool   `json:"skip_reorder,omitempty"`
	// AtLevel states the unit of ActualCount, always. See the type doc.
	AtLevel bool `json:"at_level"`
	// OpenCount sets an open_closed item's open-container tally in the same
	// write — the sealed count above plus this is the pair that describes the
	// shelf. nil omits the key and leaves the stored tally alone; the server
	// refuses it outright for an item that is not counted open/closed, so it
	// is never sent for one.
	OpenCount *int `json:"open_count,omitempty"`
}

// FilesAReorder reports whether submitting this row will auto-create a
// ReorderRequest, as far as the GRID can tell.
//
// It is the client-side half of OMS's own condition, and it is deliberately
// NOT the whole of it. The server also refuses to file one for a RETIRED item,
// and `is_retired` is not on the reconcile grid payload — so this answers true
// for a retired item that the server will then quietly not file for. A caller
// reporting a count of these to an operator must say the number is a CEILING
// rather than a prediction; inventing the missing fact would be worse than
// naming it.
//
// The comparison is in the row's COUNT unit on both sides, which is what makes
// it exact for everything it can see: ActualCount is in that unit by
// construction, and `minimum_stock` is re-read in that unit by OMS for every
// pack-counting mode.
func (r ReconciliationRow) FilesAReorder(minimumStock int) bool {
	return !r.SkipReorder && r.ActualCount <= minimumStock
}

// ReconciliationBatch is the POST body: a list of rows, and nothing else.
type ReconciliationBatch struct {
	Rows []ReconciliationRow `json:"rows"`
}

// StockReconciliation is one audit row the batch wrote back. Counts here are
// BASE-unit canonical whatever unit the entry arrived in — the server converts
// before it stores — so ActualCount on this READ struct is not necessarily the
// number the operator typed, and a caller echoing it must say which unit it is.
type StockReconciliation struct {
	ID                 int    `json:"id"`
	Item               string `json:"item"`
	ItemName           string `json:"item_name"`
	ItemSKU            string `json:"item_sku"`
	ProjectedCount     int    `json:"projected_count"`
	ActualCount        int    `json:"actual_count"`
	Delta              int    `json:"delta"`
	Reason             string `json:"reason"`
	Notes              string `json:"notes"`
	ReconciledByName   string `json:"reconciled_by_name"`
	ReconciledAt       string `json:"reconciled_at"`
	TriggeredReorderID *int   `json:"triggered_reorder_id"`
}

// ReconciliationBatchResult is what a landed batch answers with: how many rows
// were written, how many reorder requests that filed, and the audit rows.
type ReconciliationBatchResult struct {
	Reconciled      int                   `json:"reconciled"`
	ReordersCreated int                   `json:"reorders_created"`
	Reconciliations []StockReconciliation `json:"reconciliations"`
}

// SubmitReconciliationBatch posts a whole location's count.
//
// IT IS ALL OR NOTHING, and every caller must be built on that. OMS validates
// the entire payload, resolves every item and checks permission on every one of
// them BEFORE it opens a transaction, then applies the rows inside
// `transaction.atomic` — so a refusal at any point leaves NOTHING written and
// nothing partially applied. There is no per-row result to reconcile and no
// "which ones landed" to report: either `Reconciled` equals the number of rows
// sent, or the write did not happen.
//
// (The CSV upload endpoint beside this one does offer a partial mode. It is a
// different endpoint with a different contract, and this one has no such
// switch — do not carry that expectation across.)
//
// The consequence for a client is the useful half: a failed submit must KEEP
// every typed count, because the server is guaranteed not to have consumed any
// of them.
func (c *Client) SubmitReconciliationBatch(ctx context.Context, rows []ReconciliationRow) (*ReconciliationBatchResult, error) {
	if len(rows) == 0 {
		return nil, &APIError{Code: "empty_batch", Message: "no rows to submit"}
	}
	var out ReconciliationBatchResult
	if err := c.Post(ctx, "/api/inventory/reconciliations/batch/", ReconciliationBatch{Rows: rows}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AsDetailRefusal recovers the prose from a `{"detail": "<sentence>"}` body.
//
// The reconciliation endpoints write their own refusals by hand — an unknown
// item, a permission denial, a location that does not exist — so those bodies
// never reach OMS's DRF exception handler and never carry the `{"error": {...}}`
// envelope parseError understands. What the operator would otherwise read is
// the raw JSON, because parseError puts the ENTIRE body into APIError.Message
// whenever the envelope has no code.
//
// Narrow on purpose, exactly as AsReceivingRefusal is: it accepts only an
// OBJECT whose `detail` is a non-blank JSON STRING. A gateway's HTML page, a
// DRF field-validation body (`{"rows": [...]}`) and anything else keep the
// shape they arrived in rather than being mangled into a sentence. DRF's own
// handler happens to use this shape too — an expired session answers
// `{"detail": "Authentication credentials were not provided."}` — and
// recovering that sentence is right for the same reason.
func AsDetailRefusal(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return "", false
	}
	body := strings.TrimSpace(api.Message)
	if !strings.HasPrefix(body, "{") {
		return "", false
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Detail) == 0 {
		return "", false
	}
	var prose string
	if json.Unmarshal(envelope.Detail, &prose) != nil {
		return "", false
	}
	if strings.TrimSpace(prose) == "" {
		return "", false
	}
	return prose, true
}
