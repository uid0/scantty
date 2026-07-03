package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateAsset asserts the POST path, method, and the load-bearing wire
// shape: location rides as an int pk, inventory_item as a UUID string,
// required_certifications as a JSON array, amount_paid as a decimal string,
// booleans are always present, and nil optional scalars are omitted.
func TestCreateAsset(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"asset-uuid","name":"Metal lathe","asset_tag":"DMS-ABCD1234"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	desc := "10x22 bench lathe"
	item := "item-uuid-1"
	loc := 7
	cat := 3
	a, err := c.CreateAsset(context.Background(), AssetWrite{
		Name:                   "Metal lathe",
		Description:            &desc,
		InventoryItem:          &item,
		Category:               &cat,
		Location:               &loc,
		AmountPaid:             "0",
		Status:                 "active",
		OwnershipType:          "space",
		IsActive:               true,
		RequiredCertifications: []int{5, 3},
	})
	if err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	if a == nil || a.Name != "Metal lathe" {
		t.Fatalf("unexpected asset returned: %+v", a)
	}

	if captured.method != "POST" {
		t.Fatalf("method = %q, want POST", captured.method)
	}
	if captured.path != "/api/inventory/assets/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["name"] != "Metal lathe" {
		t.Fatalf("name = %v", captured.body["name"])
	}
	if captured.body["description"] != "10x22 bench lathe" {
		t.Fatalf("description = %v", captured.body["description"])
	}
	// inventory_item is a UUID string, NOT a number.
	if captured.body["inventory_item"] != "item-uuid-1" {
		t.Fatalf("inventory_item = %v (want string uuid)", captured.body["inventory_item"])
	}
	// location + category ride as int pks (JSON numbers).
	if captured.body["location"].(float64) != 7 {
		t.Fatalf("location = %v (want 7)", captured.body["location"])
	}
	if captured.body["category"].(float64) != 3 {
		t.Fatalf("category = %v (want 3)", captured.body["category"])
	}
	// amount_paid is a decimal STRING.
	if captured.body["amount_paid"] != "0" {
		t.Fatalf("amount_paid = %v (want string \"0\")", captured.body["amount_paid"])
	}
	if captured.body["ownership_type"] != "space" {
		t.Fatalf("ownership_type = %v", captured.body["ownership_type"])
	}
	// Booleans carry no omitempty, so is_active=true is on the wire.
	if captured.body["is_active"] != true {
		t.Fatalf("is_active = %v (want true)", captured.body["is_active"])
	}
	// A false boolean must also be present (not dropped).
	if _, ok := captured.body["is_donation"]; !ok {
		t.Fatalf("is_donation missing from body; false booleans must be sent")
	}
	certs, ok := captured.body["required_certifications"].([]any)
	if !ok || len(certs) != 2 {
		t.Fatalf("required_certifications = %v (want 2-element array)", captured.body["required_certifications"])
	}
	// Nil optional scalars are omitted.
	if _, ok := captured.body["serial_number"]; ok {
		t.Fatalf("serial_number should be omitted when nil, got %v", captured.body["serial_number"])
	}
	if _, ok := captured.body["owning_group"]; ok {
		t.Fatalf("owning_group should be omitted when nil, got %v", captured.body["owning_group"])
	}
}

// TestUpdateAsset asserts PATCH (not PUT), the id-scoped path, and that a
// boolean turned off (is_active=false) reaches the wire.
func TestUpdateAsset(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":"asset-9","name":"Renamed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	a, err := c.UpdateAsset(context.Background(), "asset-9", AssetWrite{
		Name:          "Renamed",
		Status:        "maintenance",
		AmountPaid:    "12.50",
		OwnershipType: "space",
		IsActive:      false,
	})
	if err != nil {
		t.Fatalf("UpdateAsset: %v", err)
	}
	if a == nil || a.Name != "Renamed" {
		t.Fatalf("unexpected asset returned: %+v", a)
	}
	if captured.method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", captured.method)
	}
	if captured.path != "/api/inventory/assets/asset-9/" {
		t.Fatalf("path = %q", captured.path)
	}
	if captured.body["status"] != "maintenance" {
		t.Fatalf("status = %v", captured.body["status"])
	}
	if captured.body["amount_paid"] != "12.50" {
		t.Fatalf("amount_paid = %v", captured.body["amount_paid"])
	}
	if captured.body["is_active"] != false {
		t.Fatalf("is_active = %v (want false on the wire)", captured.body["is_active"])
	}
}

// TestDeleteAsset asserts the DELETE method + path and that a 204 is a
// success (nil error).
func TestDeleteAsset(t *testing.T) {
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
	if err := c.DeleteAsset(context.Background(), "asset-9"); err != nil {
		t.Fatalf("DeleteAsset: %v", err)
	}
	if captured.method != "DELETE" {
		t.Fatalf("method = %q, want DELETE", captured.method)
	}
	if captured.path != "/api/inventory/assets/asset-9/" {
		t.Fatalf("path = %q", captured.path)
	}
}

// TestListAvailableCertifications asserts the endpoint and that the bare
// (non-paginated) JSON array decodes into CertificationOptions.
func TestListAvailableCertifications(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":1,"name":"Laser"},{"id":2,"name":"CNC Mill"}]`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	certs, err := c.ListAvailableCertifications(context.Background())
	if err != nil {
		t.Fatalf("ListAvailableCertifications: %v", err)
	}
	if path != "/api/lockers/available-certifications/" {
		t.Fatalf("path = %q", path)
	}
	if len(certs) != 2 || certs[0].Name != "Laser" || certs[1].ID != 2 {
		t.Fatalf("unexpected certs: %+v", certs)
	}
}

// TestAssetInventoryItemID confirms the polymorphic InventoryItem read field
// coerces a UUID string (and a defensive numeric shape) back to a pk string
// for edit-mode hydration.
func TestAssetInventoryItemID(t *testing.T) {
	a := &Asset{InventoryItem: "item-uuid-7"}
	if id, ok := a.InventoryItemID(); !ok || id != "item-uuid-7" {
		t.Fatalf("string pk: got (%q, %v)", id, ok)
	}
	a = &Asset{InventoryItem: float64(42)}
	if id, ok := a.InventoryItemID(); !ok || id != "42" {
		t.Fatalf("numeric pk: got (%q, %v)", id, ok)
	}
	a = &Asset{InventoryItem: nil}
	if _, ok := a.InventoryItemID(); ok {
		t.Fatalf("nil inventory_item should report ok=false")
	}
}
