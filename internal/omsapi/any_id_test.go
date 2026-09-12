package omsapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An `any`-typed id must survive as the DIGITS the server sent.
//
// A purchase order's id is a number on the wire (BigAutoField), and
// PurchaseOrder.ID / PurchaseOrderItem.ID are declared `any` so that a client
// carries whatever the serializer echoed. encoding/json puts a JSON number into
// an `any` as a float64, and the callers turn that back into a path segment
// with fmt — at which point %v uses %g and a seven-digit id becomes "1e+06".
//
// That id is spent on the wire: internal/tui/po_add_line.go builds
// `/api/reorders/purchase-orders/<poID>/item-lookup/` from it, and po_edit.go
// builds the line paths from PurchaseOrderItem.ID the same way. Against a real
// OMS, `.../1000000/item-lookup/` answers 200 and `.../1e+06/item-lookup/`
// answers 404 — the add-line flow aimed at an order that does not exist.
//
// Every id in this package's fixtures used to be a string like "po-1", so %v
// returned it unchanged and no test could see this.
func TestAnyID_ANumericIDKeepsItsDigits(t *testing.T) {
	// Seven digits is where %g switches to exponent form; the boundary is
	// derived by asserting the property at each size rather than named.
	for _, id := range []int64{1, 42, 999999, 1000000, 1234567, 21000000, 900719925474099} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id": %d, "po_number": "PO-2026-0042", "status": "draft",
					"items": [{"id": %d, "quantity_ordered": 5}]}`, id, id)
			}))
			defer srv.Close()

			po, err := New(srv.URL).GetPurchaseOrder(context.Background(), fmt.Sprint(id))
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			want := fmt.Sprint(id)
			if got := fmt.Sprintf("%v", po.ID); got != want {
				t.Errorf("order id rendered %q, want %q — this is the path segment the "+
					"item-lookup and line URLs are built from", got, want)
			}
			if len(po.Items) != 1 {
				t.Fatalf("items = %d", len(po.Items))
			}
			if got := fmt.Sprintf("%v", po.Items[0].ID); got != want {
				t.Errorf("line id rendered %q, want %q — po_edit builds the line "+
					"delete/void/patch paths from this", got, want)
			}
		})
	}
}

// The same property THROUGH A CUSTOM UNMARSHALER, which is where the decoder's
// option stopped.
//
// encoding/json hands a json.Unmarshaler the raw bytes and steps out of the way,
// so nothing the outer decoder was configured with reaches inside one:
// MaybeList[T].UnmarshalJSON decoded both of its branches with json.Unmarshal,
// which cannot be configured at all. ListPendingReorders decodes
// MaybeList[ReorderRequest], whose ID is `any`, and internal/tui's reorder
// queue spends it as the path segment of
// `/api/reorders/requests/<id>/approve/` — so before this option was set, a
// seven-digit request pk approved "1e+06", a request that does not exist, on a
// queue whose whole purpose is approving and cancelling.
//
// BOTH SHAPES ARE DRIVEN because MaybeList exists to accept either, and a
// fallback chain is exactly where a decoding decision gets quietly re-made: the
// /pending/ action returns a bare array today and DRF pagination on the same
// route would return the envelope tomorrow.
func TestAnyID_ANumericIDKeepsItsDigitsThroughAMaybeList(t *testing.T) {
	shapes := map[string]string{
		"bare array": `[{"id": %d, "item": "itm-1", "quantity": 3}]`,
		"envelope": `{"count": 1, "next": null, "previous": null,
			"results": [{"id": %d, "item": "itm-1", "quantity": 3}]}`,
	}
	for shape, body := range shapes {
		t.Run(shape, func(t *testing.T) {
			for _, id := range []int64{1, 42, 999999, 1000000, 1234567, 21000000, 900719925474099} {
				t.Run(fmt.Sprint(id), func(t *testing.T) {
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprintf(w, body, id)
					}))
					defer srv.Close()

					rows, err := New(srv.URL).ListPendingReorders(context.Background(), nil)
					if err != nil {
						t.Fatalf("list: %v", err)
					}
					if len(rows) != 1 {
						t.Fatalf("rows = %d — the %s branch did not decode", len(rows), shape)
					}
					want := fmt.Sprint(id)
					if got := fmt.Sprintf("%v", rows[0].ID); got != want {
						t.Errorf("reorder request id rendered %q, want %q — this is the "+
							"path segment reorder_queue.go builds the approve and cancel "+
							"URLs from", got, want)
					}
				})
			}
		})
	}
}

// An asset's polymorphic inventory-item pk keeps its digits too.
//
// Asset.InventoryItemID is one of the three `any` coercers in this package and
// it was the one not brought along when UseNumber went in: its switch offered
// `string` and `float64`, and under UseNumber a numeric pk is a json.Number, so
// it matched neither and returned ("", false) — which every caller reads as
// "this asset has no linked inventory item", silently, while hydrating an edit
// form the operator is about to save.
//
// InventoryItem's pk is a models.UUIDField today, so the string arm is the live
// one and is asserted here as the case that must not regress; the numeric case
// is the defensive branch its comment promises, tested so the promise is one the
// code honours.
func TestAnyID_AnAssetsInventoryItemIDSurvivesEveryPKShape(t *testing.T) {
	cases := []struct {
		name, field, want string
		ok                bool
	}{
		{"uuid string", `"3f2b1c60-0000-4000-8000-000000000001"`,
			"3f2b1c60-0000-4000-8000-000000000001", true},
		{"numeric pk", `1234567`, "1234567", true},
		{"large numeric pk", `900719925474099`, "900719925474099", true},
		{"absent", `null`, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id": "a-1", "name": "Lathe", "inventory_item": %s}`, c.field)
			}))
			defer srv.Close()

			asset, err := New(srv.URL).GetAsset(context.Background(), "a-1")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			got, ok := asset.InventoryItemID()
			if ok != c.ok || got != c.want {
				t.Errorf("InventoryItemID() = (%q, %v), want (%q, %v) — a false here is "+
					"read as \"no linked inventory item\" by the edit form",
					got, ok, c.want, c.ok)
			}
		})
	}
}
