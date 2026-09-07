package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The lookup decode tests, built from RECORDED OMS responses rather than from
// hand-written maps.
//
// A hand-written fixture is written by whoever wrote the struct, so it agrees
// with the struct by construction and cannot report a disagreement with the
// server. That is not a hypothetical: `POLineLookupOrder.ID` was declared
// `string` against a payload that has always carried a number, and the three
// fixtures covering this endpoint all said `{"id": "po-1"}`, so every test
// passed while `LookupPurchaseOrderLine` could not decode one real reply and
// adding a line by SKU was impossible.
//
// testdata/README.md records where each file came from and which OMS builder
// produced it.

func wireBody(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("recorded response %s: %v", name, err)
	}
	return b
}

func serveWire(t *testing.T, body []byte) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// THE REGRESSION. Served the bytes OMS really sends, the lookup must decode.
// Declared `string`, POLineLookupOrder.ID makes this fail with the operator's
// own error: "json: cannot unmarshal number into Go struct field
// POLineLookupOrder.purchase_order.id of type string".
func TestLookupPurchaseOrderLine_DecodesTheBytesOMSReallySends(t *testing.T) {
	for _, name := range []string{"po_item_lookup.json", "po_item_lookup_resolving.json"} {
		t.Run(name, func(t *testing.T) {
			c := serveWire(t, wireBody(t, name))
			if _, err := c.LookupPurchaseOrderLine(context.Background(), "2", "Hex bolt"); err != nil {
				t.Fatalf("a recorded OMS reply did not decode: %v", err)
			}
		})
	}
}

// The recorded body really does carry a NUMBER there, so the test above is not
// passing because the fixture drifted back to a string. Without this a later
// edit "fixing" the fixture would silently restore the defect and stay green.
func TestPOItemLookupFixture_CarriesTheServersOwnTypes(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(wireBody(t, "po_item_lookup.json"), &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	order, _ := raw["purchase_order"].(map[string]any)
	if _, isNumber := order["id"].(float64); !isNumber {
		t.Fatalf("purchase_order.id in the recorded body is %T (%v), want a JSON number — "+
			"serialize_lookup writes `\"id\": purchase_order.pk` and PurchaseOrder's pk is a "+
			"BigAutoField. A string here means the fixture was hand-edited back to the shape "+
			"the Go struct assumed.", order["id"], order["id"])
	}
	supplier, _ := raw["supplier"].(map[string]any)
	if _, isNumber := supplier["id"].(float64); !isNumber {
		t.Errorf("supplier.id is %T, want a JSON number", supplier["id"])
	}
	// And the fields the builder DOES str() are strings, so the file pins the
	// distinction rather than just "ids are numbers".
	cands, _ := raw["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatal("fixture has no candidates — it would prove nothing about a candidate's types")
	}
	first, _ := cands[0].(map[string]any)
	item, _ := first["item"].(map[string]any)
	if _, isString := item["id"].(string); !isString {
		t.Errorf("candidates[0].item.id is %T, want a JSON string (serialize_candidate wraps it in str())", item["id"])
	}
	existing, ok := first["already_on_order"].(map[string]any)
	if !ok {
		t.Fatal("fixture's first candidate has no already_on_order — the repeat-add numbers are unexercised")
	}
	if _, isString := existing["line_item"].(string); !isString {
		t.Errorf("already_on_order.line_item is %T, want a JSON string — PurchaseOrderItem's pk is an "+
			"integer and _serialize_existing_line str()s it anyway, which is the whole reason the "+
			"model is not what decides the wire type", existing["line_item"])
	}
}

// The facts the screen renders survive the decode, read off the recorded body
// rather than off a map written beside the assertions.
func TestLookupPurchaseOrderLine_TheServersAccountingSurvives(t *testing.T) {
	body := wireBody(t, "po_item_lookup.json")
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	res, err := serveWire(t, body).LookupPurchaseOrderLine(context.Background(), "2", "Hex bolt")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	order, _ := raw["purchase_order"].(map[string]any)
	if want := int(order["id"].(float64)); res.PurchaseOrder.ID != want {
		t.Errorf("order id = %d, want %d", res.PurchaseOrder.ID, want)
	}
	if want, _ := order["po_number"].(string); res.PurchaseOrder.Number != want {
		t.Errorf("order number = %q, want %q", res.PurchaseOrder.Number, want)
	}
	if want, _ := order["can_add_items"].(bool); res.PurchaseOrder.CanAddItems != want {
		t.Errorf("can_add_items = %v, want %v", res.PurchaseOrder.CanAddItems, want)
	}
	supplier, _ := raw["supplier"].(map[string]any)
	if want := int(supplier["id"].(float64)); res.Supplier.ID != want {
		t.Errorf("supplier id = %d, want %d", res.Supplier.ID, want)
	}

	// `resolves` is the SERVER's answer and is never re-derived: this body has
	// two candidates in the strongest tier, so it is false.
	if want, _ := raw["resolves"].(bool); res.Resolves != want {
		t.Errorf("resolves = %v, want %v", res.Resolves, want)
	}
	if want := len(raw["candidates"].([]any)); len(res.Candidates) != want {
		t.Fatalf("candidates = %d, want %d", len(res.Candidates), want)
	}
	if want := len(raw["unavailable"].([]any)); len(res.Unavailable) != want {
		t.Fatalf("unavailable = %d, want %d", len(res.Unavailable), want)
	}
	if res.Unavailable[0].Message == "" {
		t.Error("the server's own refusal sentence was dropped — it is what the operator reads")
	}

	c := res.Candidates[0]
	if c.AlreadyOnOrder == nil {
		t.Fatal("already_on_order was dropped; the repeat-add numbers are what the confirm shows")
	}
	if c.AlreadyOnOrder.RepeatIncrement == nil || c.AlreadyOnOrder.QuantityOrderedAfter == nil {
		t.Error("repeat_increment / quantity_ordered_after came back nil on a live line")
	}
	if c.SuggestedUnitCost.Empty() {
		t.Error("suggested_unit_cost was dropped; it is the prompt's prefill")
	}
	if c.MatchKind != POLineMatchPartialName {
		t.Errorf("match_kind = %q, want %q — the tiers are the server's own constants",
			c.MatchKind, POLineMatchPartialName)
	}
}

// A refusal body is not the standard envelope; recorded shape, same as above.
func TestAddPurchaseOrderLine_ADraftGuardRefusalIsStillRecovered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": "PO-LAB-0001 is Sent.", "code": "not_draft"}`)
	}))
	defer srv.Close()
	_, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{ItemSupplier: 1})
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("refusal did not survive: %v", err)
	}
	if entry.Code != POLineErrNotDraft {
		t.Errorf("code = %q, want %q", entry.Code, POLineErrNotDraft)
	}
}
