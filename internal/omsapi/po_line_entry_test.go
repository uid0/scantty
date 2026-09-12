package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The HAND-BUILT refusal shape — `{"error": "<prose>", "code": "<code>"}`
// written by the view itself, which never reaches OMS's DRF exception handler,
// so parseError falls through and puts the WHOLE raw body in APIError.Message.
// Without AsLineEntryError the operator reads that body verbatim, which is the
// "never a raw dump" rule broken on the one step where losing the reason costs
// the whole line.
//
// This shape and the standardized envelope must BOTH read correctly at once:
// these two tests are the old half and po_line_envelope_test.go is the new one.
// Either alone would leave a window in which a screen misreports — see
// AsLineEntryError's doc.
func TestAddPurchaseOrderLine_RefusalKeepsTheServersSentence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": "Acme Fasteners no longer supplies Widget bracket (marked discontinued).", "code": "discontinued"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{ItemSupplier: 7})
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the refusal did not survive as a POLineEntryError: %v", err)
	}
	if entry.Code != POLineErrDiscontinued {
		t.Errorf("code = %q, want %q", entry.Code, POLineErrDiscontinued)
	}
	if !strings.HasPrefix(entry.Message, "Acme Fasteners no longer supplies") {
		t.Errorf("message = %q — the server's own sentence is what the operator reads", entry.Message)
	}
	// And the raw JSON must not be what a caller printing the error shows.
	if strings.Contains(entry.Error(), `"code"`) {
		t.Errorf("Error() is still a raw body: %q", entry.Error())
	}
}

// The 409 carries the choice set. A client that dropped it would have to send
// the operator back to the lookup for something the server already handed over.
func TestAddPurchaseOrderLine_AmbiguityCarriesItsCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error": "\"bolt\" matches 2 items Acme supplies. Choose which one to add.",
			"code": "ambiguous",
			"candidates": [{"item_supplier": 4, "item": {"id": "a1c5d7ad-4115-463d-bce2-47db6fd9eab5", "name": "Bolt A"}},
			               {"item_supplier": 5, "item": {"id": "65b5df3c-d0de-4eb0-ad98-df48cf37d3fd", "name": "Bolt B"}}]}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{Identifier: "bolt"})
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the 409 did not survive as a POLineEntryError: %v", err)
	}
	if !entry.Ambiguous() {
		t.Errorf("code = %q, want ambiguous", entry.Code)
	}
	if len(entry.Candidates) != 2 || entry.Candidates[1].Item.Name != "Bolt B" {
		t.Errorf("candidates = %+v, want the two the server sent", entry.Candidates)
	}
}

// The narrowness this file used to assert here — that anything which is NOT a
// refusal keeps the shape it arrived in — now lives in
// TestAsLineEntryError_StillLeavesTheUnansweredAlone (po_line_envelope_test.go),
// over a strictly wider table, and is NOT restated here: two tables of the same
// property are two tables that drift apart.
//
// One of its rows had to change sides, which is the part worth recording. It
// listed the standardized envelope `{"error": {"code", "message"}}` among the
// bodies that must be left alone, on the reasoning that coercing one would
// invent "a refusal the server never made". That reasoning was right about the
// bodies it was written for and is now FALSE of this one: OMS composes these
// endpoints' refusals in that envelope on purpose (config/api_errors.py), so a
// coded, worded body IS the server's own answer, and calling it unknown is what
// the operator cannot act on. The rule did not move; that body changed what it
// is.

// The lookup's whole payload has to survive decoding: a client that lost
// `resolves`, the pre-cap counts or `already_on_order` would have to re-derive
// facts the server had already established, and would get them wrong.
func TestLookupPurchaseOrderLine_DecodesTheWholeAnswer(t *testing.T) {
	var gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("q")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "AF-99",
			// The ids are the SERVER's own types: Supplier and PurchaseOrder
			// declare no primary key, so both are integers, while the item ids
			// below are str()-wrapped UUIDs. testdata/po_item_lookup.json is a
			// recorded reply and is the authority; this map is the one that
			// used to say `"id": "po-1"` and so agreed with a struct the server
			// could not feed.
			"supplier":        map[string]any{"id": 3, "name": "Acme Fasteners"},
			"purchase_order":  map[string]any{"id": 2, "po_number": "PO-2026-0042", "status": "draft", "can_add_items": true},
			"best_match_kind": "vendor_sku",
			"resolves":        true,
			"candidates": []any{map[string]any{
				"item_supplier": 12, "match_kind": "vendor_sku", "match_label": "supplier SKU",
				"matched_value": "AF-99", "is_exact": true,
				"item": map[string]any{"id": "a1c5d7ad-4115-463d-bce2-47db6fd9eab5",
					"name": "Widget bracket", "sku": "WB-1200", "is_kit": false},
				"supplier_sku":         "AF-99",
				"quantity_per_package": 25,
				"suggested_quantity":   50,
				"suggested_unit_cost":  "4.5000",
				"already_on_order": map[string]any{
					"line_item": "7", "quantity_ordered": 5, "is_voided": false,
					"repeat_increment": 25, "quantity_ordered_after": 30,
				},
			}},
			"total_candidates":      7,
			"best_match_total":      1,
			"truncated":             false,
			"unavailable":           []any{},
			"total_unavailable":     0,
			"unavailable_truncated": false,
		})
	}))
	defer srv.Close()

	res, err := New(srv.URL).LookupPurchaseOrderLine(context.Background(), "2", "AF-99")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if gotPath != "/api/reorders/purchase-orders/2/item-lookup/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "AF-99" {
		t.Errorf("q = %q", gotQuery)
	}
	if !res.Resolves || res.BestMatchTotal != 1 || res.TotalCandidates != 7 {
		t.Errorf("resolves=%v best=%d total=%d — the server's own accounting must survive",
			res.Resolves, res.BestMatchTotal, res.TotalCandidates)
	}
	c := res.Candidates[0]
	if c.SuggestedQuantity != 50 || c.SuggestedUnitCost.String() != "4.5000" {
		t.Errorf("suggestions = %d / %q, want the prefill defaults", c.SuggestedQuantity, c.SuggestedUnitCost)
	}
	if c.AlreadyOnOrder == nil || c.AlreadyOnOrder.RepeatIncrement == nil || *c.AlreadyOnOrder.RepeatIncrement != 25 {
		t.Fatalf("already_on_order = %+v — a repeat add's increment is what the confirm frame shows", c.AlreadyOnOrder)
	}
	if got := *c.AlreadyOnOrder.QuantityOrderedAfter; got != 30 {
		t.Errorf("quantity_ordered_after = %d, want 30", got)
	}
}

// A voided existing line reports NO outcome — the add is refused rather than
// resurrecting it, so a null increment must decode as absent and not as zero.
func TestLookupPurchaseOrderLine_VoidedLineQuotesNoOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"item_supplier": 12,
				"already_on_order": map[string]any{
					"line_item": "7", "quantity_ordered": 5, "is_voided": true,
					"repeat_increment": nil, "quantity_ordered_after": nil,
				},
			}},
		})
	}))
	defer srv.Close()

	res, err := New(srv.URL).LookupPurchaseOrderLine(context.Background(), "2", "x")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	r := res.Candidates[0].AlreadyOnOrder
	if r.RepeatIncrement != nil || r.QuantityOrderedAfter != nil {
		t.Errorf("a voided line quoted an outcome: %+v", r)
	}
	if !r.IsVoided {
		t.Error("is_voided did not survive")
	}
}

// The add sends exactly one shape and omits what it wants defaulted: an empty
// quantity or cost must not go over the wire as a zero, which the server would
// take literally.
func TestAddPurchaseOrderLine_OmitsWhatItWantsDefaulted(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"created":   true,
			"line_item": map[string]any{"id": 2, "quantity_ordered": 50, "unit_cost_ordered": "4.5000"},
			"purchase_order": map[string]any{
				"id": 2, "po_number": "PO-2026-0042", "status": "draft",
			},
		})
	}))
	defer srv.Close()

	res, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{ItemSupplier: 12})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, present := body["quantity"]; present {
		t.Errorf("an unset quantity was sent anyway: %v", body)
	}
	if _, present := body["unit_cost"]; present {
		t.Errorf("an unset unit cost was sent anyway: %v", body)
	}
	if body["item_supplier"] != float64(12) {
		t.Errorf("item_supplier = %v", body["item_supplier"])
	}
	if !res.Created || res.PurchaseOrder == nil || res.PurchaseOrder.Number != "PO-2026-0042" {
		t.Errorf("the refreshed order did not come back: %+v", res)
	}
	if res.LineItem.QuantityOrdered != 50 {
		t.Errorf("line quantity = %d, want 50", res.LineItem.QuantityOrdered)
	}
}

// A deliberate zero price IS sendable — "0.00" is a value, not an absence — so
// it must go over the wire when the operator typed it.
func TestAddPurchaseOrderLine_SendsADeliberateZeroPrice(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"created": true})
	}))
	defer srv.Close()

	if _, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2",
		POLineAdd{ItemSupplier: 12, Quantity: 3, UnitCost: "0.00"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if body["unit_cost"] != "0.00" {
		t.Errorf("unit_cost = %v, want the 0.00 the operator typed", body["unit_cost"])
	}
	if body["quantity"] != float64(3) {
		t.Errorf("quantity = %v", body["quantity"])
	}
}
