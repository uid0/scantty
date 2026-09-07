package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// A seven-digit order or line id must reach the wire as its DIGITS.
//
// PurchaseOrder.ID and PurchaseOrderItem.ID are `any` — the client carries
// whatever the serializer echoed — and OMS sends both as numbers, because
// neither model declares a primary key so both are BigAutoField. These screens
// turn that value back into a path segment with fmt, and `%v` on a float64 uses
// `%g`: 1000000 renders "1e+06". Against a real OMS, `.../1000000/item-lookup/`
// answers 200 and `.../1e+06/item-lookup/` answers 404, so the add-line flow
// would be aimed at an order that does not exist and every line action on the
// edit screen at a line that does not.
//
// omsapi's decoder sets UseNumber so an `any` keeps the server's own digits;
// this is the check at the surface an operator actually drives. It could not
// fail before, because every fixture in this package wrote ids as strings like
// "po-1", which `%v` returns unchanged.
//
// asset_parts.go's IDString already carried this reasoning in its doc comment —
// the class was known and had been closed at exactly one of the sites.
func TestPONumericID_ASevenDigitOrderIDReachesTheWireWhole(t *testing.T) {
	for _, id := range []int64{7, 999999, 1000000, 1234567, 21000000} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			var lookupPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, "/item-lookup/") {
					fmt.Fprintf(w, `{"id": %d, "po_number": "PO-2026-0042", "status": "draft"}`, id)
					return
				}
				lookupPath = r.URL.Path
				_ = json.NewEncoder(w).Encode(map[string]any{
					"query":           "AF-99",
					"supplier":        map[string]any{"id": 3, "name": "Acme"},
					"purchase_order":  map[string]any{"id": id, "po_number": "PO-2026-0042", "status": "draft", "can_add_items": true},
					"best_match_kind": "vendor_sku", "resolves": false,
					"candidates": []any{}, "total_candidates": 0, "best_match_total": 0,
					"truncated": false, "unavailable": []any{}, "total_unavailable": 0,
					"unavailable_truncated": false,
				})
			}))
			defer srv.Close()
			deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

			// The order comes through the REAL client, because that is where
			// the id's Go type is decided. Constructing it here and assigning
			// the id by hand is how the first version of this check passed with
			// the fix reverted: it asserted a value the test itself had chosen.
			po, err := deps.OMS.GetPurchaseOrder(context.Background(), fmt.Sprint(id))
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			s := NewPurchaseOrderAddLineScreen(deps, po)
			if want := fmt.Sprint(id); s.poID != want {
				t.Fatalf("poID = %q, want %q — this is the path segment every request "+
					"from this screen is built from", s.poID, want)
			}
			if cmd := s.runLookup("AF-99", 1); cmd != nil {
				cmd()
			}
			want := fmt.Sprintf("/api/reorders/purchase-orders/%d/item-lookup/", id)
			if lookupPath != want {
				t.Errorf("the lookup went to %q, want %q", lookupPath, want)
			}
		})
	}
}

// The same for a LINE id, which po_edit spends on the update / void / delete
// paths. `%v` on a float64 line id builds a path naming no line at all.
func TestPONumericID_ASevenDigitLineIDReachesTheWireWhole(t *testing.T) {
	for _, id := range []int64{5, 1000000, 1234567} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id": 2, "po_number": "PO-2026-0042", "status": "draft",
					"items": [{"id": %d, "quantity_ordered": 5, "description": "Hex bolt"}]}`, id)
			}))
			defer srv.Close()

			po, err := omsapi.New(srv.URL).GetPurchaseOrder(context.Background(), "2")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if len(po.Items) != 1 {
				t.Fatalf("items = %d", len(po.Items))
			}
			if got, want := poLineID(po.Items[0]), fmt.Sprint(id); got != want {
				t.Errorf("poLineID = %q, want %q — the line delete/void/patch paths are "+
					"built from this, so a wrong one addresses no line", got, want)
			}
		})
	}
}
