//go:build omslab

package omsapi

import (
	"context"
	"os"
	"testing"
)

// The reported task, end to end against a REAL OMS: add an item to a purchase
// order by SKU.
//
// Build-tagged (`omslab`), so it is inert without a backend — see
// lab_sweep_test.go's header and AGENTS.md ("Verifying against a REAL OMS") for
// how to bring one up. Run it as:
//
//	OMS_LAB_URL=… OMS_LAB_TOKEN=… OMS_LAB_PO=2 OMS_LAB_SKU=SOCKET-M6-25 \
//	  go test -tags omslab -count=1 -run TestLabFinal ./internal/omsapi/ -v
//
// It exists because the checked-in tests cannot answer this one: they serve
// RECORDED bodies (testdata/, which is the right way to pin a decode), and a
// recording is still a snapshot. This walks the live ladder — resolve the SKU,
// add the line the lookup returned, then re-read the order and find it there —
// which is the sentence the operator's complaint was about.
//
// It needs the SKU to resolve to exactly ONE candidate, so give it a SKU no
// other item's name or code partially matches, and one not already on the order
// (a repeat GROWS the existing line, which is a different assertion).
func TestLabFinal_AddAnItemToAPOBySKU(t *testing.T) {
	c := New(os.Getenv("OMS_LAB_URL"), WithToken(os.Getenv("OMS_LAB_TOKEN"), ""))
	ctx := context.Background()
	po, sku := os.Getenv("OMS_LAB_PO"), os.Getenv("OMS_LAB_SKU")

	look, err := c.LookupPurchaseOrderLine(ctx, po, sku)
	if err != nil {
		t.Fatalf("lookup %q on order %s: %v", sku, po, err)
	}
	t.Logf("lookup: order=%v %q status=%q can_add=%v resolves=%v candidates=%d",
		look.PurchaseOrder.ID, look.PurchaseOrder.Number, look.PurchaseOrder.Status,
		look.PurchaseOrder.CanAddItems, look.Resolves, len(look.Candidates))
	if !look.Resolves {
		t.Fatalf("expected the SKU to resolve to exactly one candidate")
	}
	cand := look.Candidates[0]

	added, err := c.AddPurchaseOrderLine(ctx, po, POLineAdd{ItemSupplier: cand.ItemSupplier})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	t.Logf("added: created=%v line=%v qty=%v order=%v lines=%d",
		added.Created, added.LineItem.ID, added.LineItem.QuantityOrdered,
		added.PurchaseOrder.ID, len(added.PurchaseOrder.Items))

	// And the order really carries it when read back.
	back, err := c.GetPurchaseOrder(ctx, po)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	for _, li := range back.Items {
		if d := li.ItemDetails; d != nil && d["sku"] == sku {
			t.Logf("the order carries %s: line %v, %v ordered", sku, li.ID, li.QuantityOrdered)
			return
		}
	}
	t.Errorf("the order does not carry %s after the add (%d lines)", sku, len(back.Items))
}
