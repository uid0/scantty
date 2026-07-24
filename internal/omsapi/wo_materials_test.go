package omsapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

// Actual materials + cost capture on a work order (op-768w / B3, ScanTTY half
// sc-ggdx). Two things this file pins that nothing else can:
//
//   - the money fields are DECIMAL STRINGS ("12.50"), while applied_quantity is
//     a plain integer — the one place the material row mixes the two forms; and
//   - the ad-hoc write goes out as JSON when no receipt was picked and as
//     multipart only when one was, so an added line never sends an empty file
//     part and a receipted one never loses the scalars.

// TestGetWorkOrder_ParsesMaterialCostFields pins the full material row: the
// template-derived line beside an ad-hoc one, the money on each, and the
// work-order total that B5/B6 consume.
func TestGetWorkOrder_ParsesMaterialCostFields(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":7,"title":"Spindle service","status":"in_progress",
		"actual_material_cost":"38.37",
		"material_usage":[
			{"id":"mu-1","material":"mm-1","material_name":"Way oil","is_ad_hoc":false,
			 "inventory_item":null,"inventory_item_name":"Vactra No. 2",
			 "quantity_planned":"2.00","quantity_used":"2.00","unit":"L",
			 "unit_cost":"12.79","actual_cost":"25.58","was_used":true,
			 "applied_quantity":2,"stock_applied":true,
			 "receipt_image":null,"receipt_url":null},
			{"id":"mu-2","material":null,"material_name":"Shop rags","is_ad_hoc":true,
			 "inventory_item":"it-9","inventory_item_name":"Shop rags",
			 "quantity_planned":"1.00","quantity_used":"1.00","unit":"pack",
			 "unit_cost":"12.79","actual_cost":"12.79","was_used":true,
			 "applied_quantity":null,"stock_applied":false,
			 "receipt_url":"https://oms.example/media/receipts/r.jpg"}
		]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if wo.ActualMaterialCost != "38.37" {
		t.Errorf("actual_material_cost = %q", wo.ActualMaterialCost)
	}
	if len(wo.MaterialUsage) != 2 {
		t.Fatalf("material_usage = %+v", wo.MaterialUsage)
	}

	tmpl := wo.MaterialUsage[0]
	if tmpl.IsAdHoc {
		t.Errorf("template-derived line must not be ad-hoc: %+v", tmpl)
	}
	if fmt.Sprint(tmpl.Material) != "mm-1" {
		t.Errorf("material = %v, want the source MaintenanceMaterial id", tmpl.Material)
	}
	if tmpl.UnitCost != "12.79" || tmpl.ActualCost != "25.58" || tmpl.QuantityUsed != "2.00" {
		t.Errorf("money/qty = %+v", tmpl)
	}
	// The decrement pair: a live applied_quantity is what freezes the line's
	// price and blocks removal, so both halves have to survive the decode.
	if !tmpl.StockApplied || tmpl.AppliedQuantity == nil || *tmpl.AppliedQuantity != 2 {
		t.Errorf("stock decrement = %+v", tmpl)
	}
	if tmpl.InventoryItem != nil {
		t.Errorf("a template line carries no direct item link, got %v", *tmpl.InventoryItem)
	}
	if tmpl.InventoryItemName != "Vactra No. 2" {
		t.Errorf("inventory_item_name = %q — it resolves the SPEC's link for a template row", tmpl.InventoryItemName)
	}
	if tmpl.ReceiptURL != "" {
		t.Errorf("null receipt_url must decode to empty, got %q", tmpl.ReceiptURL)
	}

	adhoc := wo.MaterialUsage[1]
	if !adhoc.IsAdHoc {
		t.Errorf("added line must be ad-hoc: %+v", adhoc)
	}
	if adhoc.Material != nil {
		t.Errorf("an ad-hoc line has no template row, got material = %v", adhoc.Material)
	}
	if adhoc.InventoryItem == nil || *adhoc.InventoryItem != "it-9" {
		t.Errorf("ad-hoc line's own stock link = %+v", adhoc.InventoryItem)
	}
	if adhoc.StockApplied || adhoc.AppliedQuantity != nil {
		t.Errorf("un-toggled line must hold no decrement: %+v", adhoc)
	}
	if adhoc.ReceiptURL == "" {
		t.Errorf("receipt_url dropped: %+v", adhoc)
	}
}

// TestGetWorkOrder_UnpricedMaterialDecodes: cost is optional throughout — plenty
// of lines are shop stock nobody prices at the point of use — so a null price
// decodes to empty rather than failing the whole work order.
func TestGetWorkOrder_UnpricedMaterialDecodes(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":7,"title":"PM","status":"open","actual_material_cost":"0.00",
		"material_usage":[{"id":"mu-1","material_name":"Grease","unit_cost":null,"actual_cost":null,"was_used":true}]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	mu := wo.MaterialUsage[0]
	if !mu.UnitCost.Empty() || !mu.ActualCost.Empty() {
		t.Errorf("null money must decode empty, got %+v", mu)
	}
}

// TestAddWorkOrderMaterial_JSON pins the plain (receipt-less) add: POST to the
// materials collection with only the fields that were actually filled in, so
// the backend's own defaults — quantity 1, and a unit cost seeded from the
// linked item — still apply.
func TestAddWorkOrderMaterial_JSON(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"mu-2","material_name":"Shop rags","is_ad_hoc":true}`, &cap)
	defer srv.Close()

	mu, err := New(srv.URL).AddWorkOrderMaterial(context.Background(), "wo1", WorkOrderAdHocMaterial{
		MaterialName:  "Shop rags",
		QuantityUsed:  "2",
		UnitCost:      "12.50",
		InventoryItem: "it-9",
	})
	if err != nil {
		t.Fatalf("AddWorkOrderMaterial: %v", err)
	}
	if mu == nil || !mu.IsAdHoc {
		t.Fatalf("unexpected material: %+v", mu)
	}
	if cap.method != http.MethodPost {
		t.Errorf("method = %q, want POST", cap.method)
	}
	if cap.path != "/api/inventory/work-orders/wo1/materials/" {
		t.Errorf("path = %q", cap.path)
	}
	for k, want := range map[string]string{
		"material_name":  "Shop rags",
		"quantity_used":  "2",
		"unit_cost":      "12.50",
		"inventory_item": "it-9",
	} {
		if got := fmt.Sprint(cap.body[k]); got != want {
			t.Errorf("body[%s] = %q, want %q", k, got, want)
		}
	}
	// An empty unit must not ride as "": the field would then overwrite the
	// serializer's default instead of being left to it.
	if _, ok := cap.body["unit"]; ok {
		t.Errorf("empty fields must be omitted, got %v", cap.body)
	}
}

// TestAddWorkOrderMaterial_MultipartWithReceipt: picking a receipt switches the
// request to multipart/form-data under the serializer's receipt_image field —
// and every scalar still rides, as the form strings DRF parses out of it.
func TestAddWorkOrderMaterial_MultipartWithReceipt(t *testing.T) {
	var cap multipartCapture
	srv := multipartServer(t, http.StatusCreated,
		`{"id":"mu-3","material_name":"Misc supplies","is_ad_hoc":true}`, "receipt_image", &cap)
	defer srv.Close()

	if _, err := New(srv.URL).AddWorkOrderMaterial(context.Background(), "wo1", WorkOrderAdHocMaterial{
		MaterialName:    "Misc supplies",
		QuantityUsed:    "1",
		UnitCost:        "18.42",
		ReceiptFilename: "receipt.jpg",
		Receipt:         []byte("JPEGDATA"),
	}); err != nil {
		t.Fatalf("AddWorkOrderMaterial: %v", err)
	}
	if cap.path != "/api/inventory/work-orders/wo1/materials/" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.fileName != "receipt.jpg" || cap.fileBody != "JPEGDATA" {
		t.Errorf("receipt part = %q / %q", cap.fileName, cap.fileBody)
	}
	for k, want := range map[string]string{
		"material_name": "Misc supplies",
		"quantity_used": "1",
		"unit_cost":     "18.42",
	} {
		if got := cap.fields[k]; len(got) != 1 || got[0] != want {
			t.Errorf("field %s = %v, want %q", k, got, want)
		}
	}
	// No stock link: an out-of-pocket buy records the spend and moves nothing.
	if _, ok := cap.fields["inventory_item"]; ok {
		t.Errorf("unset inventory_item must be omitted, got %v", cap.fields)
	}
}

// TestRemoveWorkOrderMaterial pins the DELETE — including the trailing slash,
// without which DRF answers a redirect rather than doing the work.
func TestRemoveWorkOrderMaterial(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusNoContent, ``, &cap)
	defer srv.Close()

	if err := New(srv.URL).RemoveWorkOrderMaterial(context.Background(), "wo1", "mu-2"); err != nil {
		t.Fatalf("RemoveWorkOrderMaterial: %v", err)
	}
	if cap.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", cap.method)
	}
	if cap.path != "/api/inventory/work-orders/wo1/materials/mu-2/" {
		t.Errorf("path = %q", cap.path)
	}
}

// TestToggleWorkOrderMaterial_CarriesEdits: the toggle is the ONLY endpoint that
// writes a line's price, so it has to carry one without flipping was_used —
// that is how an already-marked out-of-pocket buy gets priced at all.
func TestToggleWorkOrderMaterial_CarriesEdits(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":"mu-1","unit_cost":"12.50","was_used":true}`, &cap)
	defer srv.Close()

	cost, qty := "12.50", "3"
	if _, err := New(srv.URL).ToggleWorkOrderMaterial(context.Background(), "wo1", "mu-1", true,
		WorkOrderMaterialEdit{UnitCost: &cost, QuantityUsed: &qty}); err != nil {
		t.Fatalf("ToggleWorkOrderMaterial: %v", err)
	}
	if cap.body["was_used"] != true {
		t.Errorf("was_used = %v", cap.body["was_used"])
	}
	if fmt.Sprint(cap.body["unit_cost"]) != "12.50" || fmt.Sprint(cap.body["quantity_used"]) != "3" {
		t.Errorf("edits = %v", cap.body)
	}
}

// TestToggleWorkOrderMaterial_ClearsUnitCost: a blank price is a real answer —
// "nobody knows what this cost" — and the backend only reads it as one when it
// arrives as an explicit JSON null, so an empty string must not ride as "".
func TestToggleWorkOrderMaterial_ClearsUnitCost(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":"mu-1","unit_cost":null,"was_used":false}`, &cap)
	defer srv.Close()

	blank := ""
	if _, err := New(srv.URL).ToggleWorkOrderMaterial(context.Background(), "wo1", "mu-1", false,
		WorkOrderMaterialEdit{UnitCost: &blank}); err != nil {
		t.Fatalf("ToggleWorkOrderMaterial: %v", err)
	}
	raw, ok := cap.body["unit_cost"]
	if !ok {
		t.Fatalf("unit_cost must be present to clear the price, got %v", cap.body)
	}
	if raw != nil {
		t.Errorf("unit_cost = %v, want JSON null", raw)
	}
}

// TestToggleWorkOrderMaterial_OmitsUntouchedEdits: a plain used/un-used toggle
// sends neither amount, so it can never silently rewrite a price or a quantity
// somebody else recorded.
func TestToggleWorkOrderMaterial_OmitsUntouchedEdits(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":"mu-1","was_used":true}`, &cap)
	defer srv.Close()

	if _, err := New(srv.URL).ToggleWorkOrderMaterial(context.Background(), "wo1", "mu-1", true,
		WorkOrderMaterialEdit{}); err != nil {
		t.Fatalf("ToggleWorkOrderMaterial: %v", err)
	}
	if _, ok := cap.body["unit_cost"]; ok {
		t.Errorf("untouched unit_cost must be absent, got %v", cap.body)
	}
	if _, ok := cap.body["quantity_used"]; ok {
		t.Errorf("untouched quantity_used must be absent, got %v", cap.body)
	}
}
