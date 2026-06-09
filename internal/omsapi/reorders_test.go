package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreatePurchaseOrder_FreeformLine(t *testing.T) {
	var captured struct {
		method string
		path   string
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &captured.body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"po-uuid","po_number":"PO-2026-0042"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	cost := 12.50
	po, err := c.CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Supplier: 7,
		Notes:    "Quarterly restock",
		Items: []PurchaseOrderCreateItem{{
			Description: "Custom hex bolts",
			Quantity:    24,
			UnitCost:    &cost,
		}},
	})
	if err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	if po == nil || po.Number != "PO-2026-0042" {
		t.Fatalf("unexpected po returned: %+v", po)
	}

	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["supplier"].(float64) != 7 {
		t.Fatalf("supplier in body = %v", captured.body["supplier"])
	}
	items := captured.body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items len = %d", len(items))
	}
	line := items[0].(map[string]any)
	if line["description"] != "Custom hex bolts" {
		t.Fatalf("description = %v", line["description"])
	}
	if line["quantity"].(float64) != 24 {
		t.Fatalf("quantity = %v", line["quantity"])
	}
	// unit_cost is serialized as a JSON number; verify the float lands
	// on the wire (not "12.50").
	if line["unit_cost"].(float64) != 12.50 {
		t.Fatalf("unit_cost = %v", line["unit_cost"])
	}
	// asset_id / item_supplier_id must omit when nil — sending them
	// with zero values would route to the wrong branch on the backend.
	if _, present := line["asset_id"]; present {
		t.Errorf("asset_id should be omitted, got %v", line["asset_id"])
	}
	if _, present := line["item_supplier_id"]; present {
		t.Errorf("item_supplier_id should be omitted, got %v", line["item_supplier_id"])
	}
}

func TestCreatePurchaseOrder_InventoryLine(t *testing.T) {
	var bodyJSON map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &bodyJSON)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"po-uuid","po_number":"PO-2026-0043"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	itemSup := 42
	_, err := c.CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Supplier: 7,
		Items: []PurchaseOrderCreateItem{{
			ItemSupplierID: &itemSup,
			Quantity:       10,
		}},
	})
	if err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}

	line := bodyJSON["items"].([]any)[0].(map[string]any)
	if line["item_supplier_id"].(float64) != 42 {
		t.Fatalf("item_supplier_id = %v", line["item_supplier_id"])
	}
	if _, present := line["description"]; present && line["description"] != "" {
		t.Errorf("description should be omitted/empty for inventory line, got %v", line["description"])
	}
}

func TestCreatePurchaseOrder_BackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"supplier is required"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Items: []PurchaseOrderCreateItem{{Description: "x", Quantity: 1}},
	})
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
}
