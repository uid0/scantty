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

// TestReceivePOItems_Contract pins every wire field against the frozen
// backend receive contract:
//
//	POST /api/reorders/purchase-orders/{po_id}/receive/
//	{ "items": [{purchase_order_item, quantity_received}, ...],
//	  "delivery_date"?, "receipt_notes"? }
//
// The old /receipts/ create path used qty_received / item_supplier / notes
// and a top-level purchase_order — drift that 400'd in production. Guard
// against regressing to any of those names.
func TestReceivePOItems_Contract(t *testing.T) {
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
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"po-uuid","po_number":"PO-2026-0044","is_fully_received":true,"total_received_quantity":5,"total_quantity":5}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	po, err := c.ReceivePOItems(context.Background(), "po-uuid", ReceiveRequest{
		Items: []ReceiptLine{
			{PurchaseOrderItem: "item-1", QuantityReceived: 3},
			{PurchaseOrderItem: "item-2", QuantityReceived: 2},
		},
		DeliveryDate: "2026-06-16",
		ReceiptNotes: "left at dock",
	})
	if err != nil {
		t.Fatalf("ReceivePOItems: %v", err)
	}
	// Endpoint returns the updated PurchaseOrder, not a Receipt.
	if po == nil || po.Number != "PO-2026-0044" || !po.IsFullyReceived {
		t.Fatalf("unexpected po returned: %+v", po)
	}

	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-uuid/receive/" {
		t.Fatalf("path = %q", captured.path)
	}
	// po id rides in the URL, never in the body.
	if _, present := captured.body["purchase_order"]; present {
		t.Errorf("purchase_order should not be in body, got %v", captured.body["purchase_order"])
	}
	if got := captured.body["delivery_date"]; got != "2026-06-16" {
		t.Errorf("delivery_date = %v, want 2026-06-16", got)
	}
	// receipt_notes, NOT the old notes.
	if got := captured.body["receipt_notes"]; got != "left at dock" {
		t.Errorf("receipt_notes = %v, want \"left at dock\"", got)
	}
	if _, present := captured.body["notes"]; present {
		t.Errorf("body must use receipt_notes, not notes; got notes=%v", captured.body["notes"])
	}

	items, ok := captured.body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %v", captured.body["items"])
	}
	line := items[0].(map[string]any)
	if line["purchase_order_item"] != "item-1" {
		t.Errorf("purchase_order_item = %v, want item-1", line["purchase_order_item"])
	}
	// quantity_received, NOT the old qty_received.
	if line["quantity_received"].(float64) != 3 {
		t.Errorf("quantity_received = %v, want 3", line["quantity_received"])
	}
	if _, present := line["qty_received"]; present {
		t.Errorf("line must use quantity_received, not qty_received; got %v", line["qty_received"])
	}
	// item_supplier is resolved server-side and must not be sent.
	if _, present := line["item_supplier"]; present {
		t.Errorf("item_supplier should be omitted, got %v", line["item_supplier"])
	}
}

// TestReceivePOItems_OmitsOptional verifies delivery_date and receipt_notes
// drop off the wire when empty (omitempty), so a notes-less receive doesn't
// send blank strings the serializer might reject.
func TestReceivePOItems_OmitsOptional(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"po-uuid","po_number":"PO-2026-0045"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.ReceivePOItems(context.Background(), "po-uuid", ReceiveRequest{
		Items: []ReceiptLine{{PurchaseOrderItem: 7, QuantityReceived: 1}},
	})
	if err != nil {
		t.Fatalf("ReceivePOItems: %v", err)
	}
	if _, present := body["delivery_date"]; present {
		t.Errorf("delivery_date should be omitted when empty, got %v", body["delivery_date"])
	}
	if _, present := body["receipt_notes"]; present {
		t.Errorf("receipt_notes should be omitted when empty, got %v", body["receipt_notes"])
	}
}

// TestReceivePOItems_BackendError confirms the receive call surfaces a
// backend 400 (e.g. the historical received_by rejection) as an error.
func TestReceivePOItems_BackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"received_by":["This field is required."]}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.ReceivePOItems(context.Background(), "po-uuid", ReceiveRequest{
		Items: []ReceiptLine{{PurchaseOrderItem: 1, QuantityReceived: 1}},
	})
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
}

// TestSendToSupplier pins the manual send action to POST
// /api/reorders/purchase-orders/{id}/send_to_supplier/ with an empty
// body — the po id must ride in the URL, never the payload.
func TestSendToSupplier(t *testing.T) {
	var captured struct {
		method  string
		path    string
		bodyLen int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		captured.bodyLen = len(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0046","status":"sent"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.SendToSupplier(context.Background(), "po-1"); err != nil {
		t.Fatalf("SendToSupplier: %v", err)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/send_to_supplier/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.bodyLen != 0 {
		t.Errorf("send_to_supplier must post an empty body, got %d bytes", captured.bodyLen)
	}
}

// TestSendToSupplier_BackendError surfaces the backend's draft-only 400
// (e.g. sending an already-sent PO) as an error.
func TestSendToSupplier_BackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Only draft orders can be sent to suppliers"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.SendToSupplier(context.Background(), "po-1"); err == nil {
		t.Fatal("expected error on 400, got nil")
	}
}

// TestConfirmOrder_NoDate confirms with no expected_delivery_date: the
// action hits confirm_order/ with an empty body, matching the OMS
// frontend's default confirm.
func TestConfirmOrder_NoDate(t *testing.T) {
	var captured struct {
		method  string
		path    string
		bodyLen int
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		captured.bodyLen = len(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0047","status":"confirmed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.ConfirmOrder(context.Background(), "po-1", ""); err != nil {
		t.Fatalf("ConfirmOrder: %v", err)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/confirm_order/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.bodyLen != 0 {
		t.Errorf("confirm with no date must post an empty body, got %d bytes", captured.bodyLen)
	}
}

// TestConfirmOrder_WithDate forwards expected_delivery_date in the body
// when the caller supplies one.
func TestConfirmOrder_WithDate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0048","status":"confirmed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.ConfirmOrder(context.Background(), "po-1", "2026-08-01"); err != nil {
		t.Fatalf("ConfirmOrder: %v", err)
	}
	if got := body["expected_delivery_date"]; got != "2026-08-01" {
		t.Errorf("expected_delivery_date = %v, want 2026-08-01", got)
	}
}

// TestConfirmOrder_BackendError surfaces the backend's sent-only 400 as
// an error.
func TestConfirmOrder_BackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"Only sent orders can be confirmed"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.ConfirmOrder(context.Background(), "po-1", ""); err == nil {
		t.Fatal("expected error on 400, got nil")
	}
}
