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

func fptr(f float64) *float64 { return &f }

// TestUpdatePurchaseOrder_Contract pins the PO-metadata PATCH to
// PATCH /api/reorders/purchase-orders/{id}/ and asserts the four editable
// fields ride in the body with the web contract's field names. The empty
// expected_delivery_date must marshal to JSON null (clear), never "".
func TestUpdatePurchaseOrder_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
		raw    []byte
		body   map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.raw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(captured.raw, &captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0050","status":"draft"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	po, err := c.UpdatePurchaseOrder(context.Background(), "po-1", PurchaseOrderUpdate{
		SupplierOrderNumber:  strptr("SUP-123"),
		SalesOrderNumber:     strptr("SO-9"),
		ExpectedDeliveryDate: strptr(""), // clear
		Notes:                strptr("rush"),
	})
	if err != nil {
		t.Fatalf("UpdatePurchaseOrder: %v", err)
	}
	if po == nil || po.Number != "PO-2026-0050" {
		t.Fatalf("unexpected po: %+v", po)
	}
	if captured.method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["supplier_order_number"] != "SUP-123" {
		t.Errorf("supplier_order_number = %v", captured.body["supplier_order_number"])
	}
	if captured.body["sales_order_number"] != "SO-9" {
		t.Errorf("sales_order_number = %v", captured.body["sales_order_number"])
	}
	if captured.body["notes"] != "rush" {
		t.Errorf("notes = %v", captured.body["notes"])
	}
	// expected_delivery_date must be present AND null (json.Unmarshal decodes
	// JSON null to a nil interface, so the key exists with a nil value).
	v, present := captured.body["expected_delivery_date"]
	if !present || v != nil {
		t.Errorf("expected_delivery_date should be JSON null; present=%v value=%v", present, v)
	}
	if !strings.Contains(string(captured.raw), `"expected_delivery_date":null`) {
		t.Errorf("raw body should carry expected_delivery_date:null, got %s", captured.raw)
	}
}

// TestUpdatePurchaseOrder_OmitsNil confirms nil fields drop off the wire so a
// PATCH touches only what the caller set, and a non-empty date is sent as-is.
func TestUpdatePurchaseOrder_OmitsNil(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0051"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.UpdatePurchaseOrder(context.Background(), "po-1", PurchaseOrderUpdate{
		ExpectedDeliveryDate: strptr("2026-09-01"),
	})
	if err != nil {
		t.Fatalf("UpdatePurchaseOrder: %v", err)
	}
	if got := body["expected_delivery_date"]; got != "2026-09-01" {
		t.Errorf("expected_delivery_date = %v, want 2026-09-01", got)
	}
	for _, k := range []string{"supplier_order_number", "sales_order_number", "notes"} {
		if _, present := body[k]; present {
			t.Errorf("%s should be omitted when nil, got %v", k, body[k])
		}
	}
}

// TestUpdatePurchaseOrderLineItem_Contract pins the per-line PATCH to
// /items/{itemID}/ with line_cost (a JSON number) and expected_shipment_date.
func TestUpdatePurchaseOrderLineItem_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":5,"quantity_ordered":10}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.UpdatePurchaseOrderLineItem(context.Background(), "po-1", "5", LineItemUpdate{
		LineCost:             fptr(125.50),
		ExpectedShipmentDate: strptr("2026-07-20"),
		Notes:                strptr("backordered"),
	})
	if err != nil {
		t.Fatalf("UpdatePurchaseOrderLineItem: %v", err)
	}
	if item == nil {
		t.Fatal("nil item returned")
	}
	if captured.method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/items/5/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["line_cost"].(float64) != 125.50 {
		t.Errorf("line_cost = %v, want 125.5", captured.body["line_cost"])
	}
	if captured.body["expected_shipment_date"] != "2026-07-20" {
		t.Errorf("expected_shipment_date = %v", captured.body["expected_shipment_date"])
	}
	if captured.body["notes"] != "backordered" {
		t.Errorf("notes = %v", captured.body["notes"])
	}
	// unit_cost_actual not set -> must be omitted (backend prefers line_cost).
	if _, present := captured.body["unit_cost_actual"]; present {
		t.Errorf("unit_cost_actual should be omitted, got %v", captured.body["unit_cost_actual"])
	}
}

// TestUpdatePurchaseOrderLineItem_ClearShipDate sends an empty shipment date,
// which the backend maps to NULL. An empty string (not omitted) is the wire
// signal to clear, so it must be present.
func TestUpdatePurchaseOrderLineItem_ClearShipDate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":5}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.UpdatePurchaseOrderLineItem(context.Background(), "po-1", "5", LineItemUpdate{
		ExpectedShipmentDate: strptr(""),
	})
	if err != nil {
		t.Fatalf("UpdatePurchaseOrderLineItem: %v", err)
	}
	v, present := body["expected_shipment_date"]
	if !present || v != "" {
		t.Errorf("expected_shipment_date should be present and empty; present=%v value=%v", present, v)
	}
}

// TestVoidPurchaseOrderLineItem pins the void-line action to
// POST /items/{itemID}/void/ with the reason in the body.
func TestVoidPurchaseOrderLineItem(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":5,"is_voided":true,"void_reason":"discontinued"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.VoidPurchaseOrderLineItem(context.Background(), "po-1", "5", "discontinued")
	if err != nil {
		t.Fatalf("VoidPurchaseOrderLineItem: %v", err)
	}
	if item == nil || !item.IsVoided {
		t.Fatalf("unexpected item: %+v", item)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/items/5/void/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["reason"] != "discontinued" {
		t.Errorf("reason = %v, want discontinued", captured.body["reason"])
	}
}

// TestVoidPurchaseOrder pins the void-order action to POST /{id}/void/ with the
// reason in the body, and returns the voided PO.
func TestVoidPurchaseOrder(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0052","status":"voided"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	po, err := c.VoidPurchaseOrder(context.Background(), "po-1", "supplier rejected")
	if err != nil {
		t.Fatalf("VoidPurchaseOrder: %v", err)
	}
	if po == nil || po.Status != "voided" {
		t.Fatalf("unexpected po: %+v", po)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/void/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["reason"] != "supplier rejected" {
		t.Errorf("reason = %v", captured.body["reason"])
	}
}

// TestVoidPurchaseOrder_BackendError surfaces the backend's 403 (non-staff) as
// an error.
func TestVoidPurchaseOrder_BackendError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"Only staff or COO group members may void purchase orders."}`, http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.VoidPurchaseOrder(context.Background(), "po-1", "x"); err == nil {
		t.Fatal("expected error on 403, got nil")
	}
}

// TestMarkPurchaseOrderDelivered_Contract pins mark-delivered to
// POST /{id}/mark-delivered/ with delivery_date required and tracking/carrier
// carried through.
func TestMarkPurchaseOrderDelivered_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0053","status":"received","is_fully_received":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	po, err := c.MarkPurchaseOrderDelivered(context.Background(), "po-1", MarkDeliveredRequest{
		DeliveryDate:   "2026-07-03",
		TrackingNumber: "1Z999",
		Carrier:        "UPS",
		ReceiptNotes:   "left at dock",
	})
	if err != nil {
		t.Fatalf("MarkPurchaseOrderDelivered: %v", err)
	}
	if po == nil || !po.IsFullyReceived {
		t.Fatalf("unexpected po: %+v", po)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/mark-delivered/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["delivery_date"] != "2026-07-03" {
		t.Errorf("delivery_date = %v", captured.body["delivery_date"])
	}
	if captured.body["tracking_number"] != "1Z999" {
		t.Errorf("tracking_number = %v", captured.body["tracking_number"])
	}
	if captured.body["carrier"] != "UPS" {
		t.Errorf("carrier = %v", captured.body["carrier"])
	}
	if captured.body["receipt_notes"] != "left at dock" {
		t.Errorf("receipt_notes = %v", captured.body["receipt_notes"])
	}
}

// TestMarkPurchaseOrderDelivered_OmitsOptional confirms the optional
// tracking/carrier/receipt_notes drop off the wire when blank, so a bare
// delivery keeps the body to just delivery_date.
func TestMarkPurchaseOrderDelivered_OmitsOptional(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0054"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.MarkPurchaseOrderDelivered(context.Background(), "po-1", MarkDeliveredRequest{
		DeliveryDate: "2026-07-03",
	})
	if err != nil {
		t.Fatalf("MarkPurchaseOrderDelivered: %v", err)
	}
	if body["delivery_date"] != "2026-07-03" {
		t.Errorf("delivery_date = %v", body["delivery_date"])
	}
	for _, k := range []string{"tracking_number", "carrier", "receipt_notes"} {
		if _, present := body[k]; present {
			t.Errorf("%s should be omitted when blank, got %v", k, body[k])
		}
	}
}

// TestUploadPurchaseOrderAttachment_Multipart parses the multipart body
// server-side and asserts the file part is named "file" (with the given
// filename + contents) and description rides as a text field, matching the web
// uploadAttachment contract.
func TestUploadPurchaseOrderAttachment_Multipart(t *testing.T) {
	var captured struct {
		method      string
		path        string
		fileName    string
		fileBody    string
		description string
		ctype       string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.ctype = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		captured.description = r.FormValue("description")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "no file part", http.StatusBadRequest)
			return
		}
		defer f.Close()
		captured.fileName = hdr.Filename
		b, _ := io.ReadAll(f)
		captured.fileBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":11,"file_name":"po.pdf","description":"sales order"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	att, err := c.UploadPurchaseOrderAttachment(
		context.Background(), "po-1", "po.pdf", strings.NewReader("%PDF-1.4 fake"), "sales order",
	)
	if err != nil {
		t.Fatalf("UploadPurchaseOrderAttachment: %v", err)
	}
	if att == nil || att.ID != 11 {
		t.Fatalf("unexpected attachment: %+v", att)
	}
	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/upload-attachment/" {
		t.Fatalf("path = %q", captured.path)
	}
	if !strings.HasPrefix(captured.ctype, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data", captured.ctype)
	}
	if captured.fileName != "po.pdf" {
		t.Errorf("file name = %q, want po.pdf", captured.fileName)
	}
	if captured.fileBody != "%PDF-1.4 fake" {
		t.Errorf("file body = %q", captured.fileBody)
	}
	if captured.description != "sales order" {
		t.Errorf("description = %q, want \"sales order\"", captured.description)
	}
}

// TestUploadPurchaseOrderAttachment_OmitsBlankDescription confirms a blank
// description is not sent as a field (matching the web, which only appends
// description when present).
func TestUploadPurchaseOrderAttachment_OmitsBlankDescription(t *testing.T) {
	sawDescription := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		if r.MultipartForm != nil {
			_, sawDescription = r.MultipartForm.Value["description"]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.UploadPurchaseOrderAttachment(
		context.Background(), "po-1", "x.pdf", strings.NewReader("data"), "",
	)
	if err != nil {
		t.Fatalf("UploadPurchaseOrderAttachment: %v", err)
	}
	if sawDescription {
		t.Error("blank description should not be sent as a multipart field")
	}
}

// TestDeletePurchaseOrderAttachment pins deletion to DELETE
// /{id}/attachments/{attachmentID}/ (204).
func TestDeletePurchaseOrderAttachment(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeletePurchaseOrderAttachment(context.Background(), "po-1", 11); err != nil {
		t.Fatalf("DeletePurchaseOrderAttachment: %v", err)
	}
	if captured.method != "DELETE" {
		t.Fatalf("method = %q, want DELETE", captured.method)
	}
	if captured.path != "/api/reorders/purchase-orders/po-1/attachments/11/" {
		t.Fatalf("path = %q", captured.path)
	}
}

// TestDeletePurchaseOrderAttachment_Forbidden surfaces the backend's staff-only
// 403 as an error.
func TestDeletePurchaseOrderAttachment_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"Only staff may delete purchase order attachments."}`, http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeletePurchaseOrderAttachment(context.Background(), "po-1", 11); err == nil {
		t.Fatal("expected error on 403, got nil")
	}
}
