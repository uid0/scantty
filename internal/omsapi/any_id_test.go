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
