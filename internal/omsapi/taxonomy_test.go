package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateCategory_Contract pins the category create body: POST to the
// categories collection, name/description/color always present, parent sent as
// a JSON number when set, and slug never sent (it is server-generated).
func TestCreateCategory_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":7,"name":"Resistors","slug":"resistors"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	cat, err := c.CreateCategory(context.Background(), CategoryWrite{
		Name:        "Resistors",
		Description: "Through-hole + SMD",
		Color:       "#FF5733",
		Parent:      intptr(3),
	})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if cat == nil || cat.ID != 7 || cat.Slug != "resistors" {
		t.Fatalf("unexpected category: %+v", cat)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/categories/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Resistors" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["description"] != "Through-hole + SMD" {
		t.Errorf("description = %v", captured.body["description"])
	}
	if captured.body["color"] != "#FF5733" {
		t.Errorf("color = %v", captured.body["color"])
	}
	if captured.body["parent"].(float64) != 3 {
		t.Errorf("parent = %v (%T)", captured.body["parent"], captured.body["parent"])
	}
	if _, present := captured.body["slug"]; present {
		t.Errorf("slug must not be sent (server-generated), got %v", captured.body["slug"])
	}
}

// TestCreateCategory_ParentNull confirms an unset parent is transmitted as an
// explicit JSON null (no omitempty) so a PATCH can clear an existing parent.
func TestCreateCategory_ParentNull(t *testing.T) {
	var body map[string]any
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(rawBody, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":8,"name":"Top"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateCategory(context.Background(), CategoryWrite{Name: "Top"}); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	v, present := body["parent"]
	if !present {
		t.Errorf("parent key must be present (as null), got body %s", rawBody)
	}
	if v != nil {
		t.Errorf("parent = %v, want null", v)
	}
	// description + color still present as empty strings.
	if body["description"] != "" {
		t.Errorf("description = %v, want empty string", body["description"])
	}
	if body["color"] != "" {
		t.Errorf("color = %v, want empty string", body["color"])
	}
}

// TestUpdateCategory_Contract confirms edits go out as PATCH to the detail URL.
func TestUpdateCategory_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"name":"Resistors"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateCategory(context.Background(), "7", CategoryWrite{Name: "Resistors"}); err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if method != http.MethodPatch || path != "/api/inventory/categories/7/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestDeleteCategory_Contract confirms DELETE to the detail URL with a 204.
func TestDeleteCategory_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteCategory(context.Background(), "7"); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}
	if method != http.MethodDelete || path != "/api/inventory/categories/7/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestCreateLocation_Contract pins the location create body: is_active always
// present (no omitempty so it can be toggled off), parent as a JSON number when
// set, description present.
func TestCreateLocation_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":4,"name":"Shelf 3B","is_active":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	loc, err := c.CreateLocation(context.Background(), LocationWrite{
		Name:        "Shelf 3B",
		Description: "Third rack, bin B",
		Parent:      intptr(2),
		IsActive:    true,
	})
	if err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	if loc == nil || loc.ID != 4 {
		t.Fatalf("unexpected location: %+v", loc)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/locations/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Shelf 3B" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["is_active"] != true {
		t.Errorf("is_active = %v, want true", captured.body["is_active"])
	}
	if captured.body["parent"].(float64) != 2 {
		t.Errorf("parent = %v", captured.body["parent"])
	}
	if captured.body["description"] != "Third rack, bin B" {
		t.Errorf("description = %v", captured.body["description"])
	}
}

// TestCreateLocation_InactiveAndTopLevel confirms is_active=false actually rides
// the wire (not dropped) and an unset parent is sent as null.
func TestCreateLocation_InactiveAndTopLevel(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":5,"name":"Old room"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateLocation(context.Background(), LocationWrite{Name: "Old room", IsActive: false}); err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}
	if v, present := body["is_active"]; !present || v != false {
		t.Errorf("is_active = %v (present=%v), want false present", v, present)
	}
	if v, present := body["parent"]; !present || v != nil {
		t.Errorf("parent = %v (present=%v), want null present", v, present)
	}
}

// TestGetLocation_HydratesRichFields confirms the retrieve serializer's extra
// fields (description, is_active, parent_name, access_code, qr_code_url,
// fixture_count) decode onto the Location.
func TestGetLocation_HydratesRichFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/inventory/locations/4/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":4,"name":"Shelf 3B","description":"rack","is_active":true,"parent":2,"parent_name":"Rack 3","access_code":"ABC123","qr_code_url":"/media/qr.png","fixture_count":6}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	loc, err := c.GetLocation(context.Background(), "4")
	if err != nil {
		t.Fatalf("GetLocation: %v", err)
	}
	if loc.Description != "rack" || !loc.IsActive || loc.ParentName != "Rack 3" ||
		loc.AccessCode != "ABC123" || loc.QRCodeURL != "/media/qr.png" || loc.FixtureCount != 6 {
		t.Errorf("rich fields not hydrated: %+v", loc)
	}
}

// TestGenerateLocationQR_Contract confirms the POST hits the generate_qr action
// and the qr_code_url is read back off the JSON body.
func TestGenerateLocationQR_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"QR code generated successfully","qr_code_url":"/media/loc4.png"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	res, err := c.GenerateLocationQR(context.Background(), "4")
	if err != nil {
		t.Fatalf("GenerateLocationQR: %v", err)
	}
	if method != http.MethodPost || path != "/api/inventory/locations/4/generate_qr/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
	if res.QRCodeURL != "/media/loc4.png" {
		t.Errorf("qr_code_url = %q", res.QRCodeURL)
	}
}

// TestCreateSupplier_Contract pins the supplier create body: every form field
// present (name, supplier_type, website, account_number,
// tax_free_paperwork_filed, notes) so a PATCH can clear text fields.
func TestCreateSupplier_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":11,"name":"Acme","supplier_type":"online"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	sup, err := c.CreateSupplier(context.Background(), SupplierWrite{
		Name:                  "Acme",
		SupplierType:          "online",
		Website:               "https://acme.test",
		AccountNumber:         "ACC-1",
		TaxFreePaperworkFiled: true,
		Notes:                 "net-30",
	})
	if err != nil {
		t.Fatalf("CreateSupplier: %v", err)
	}
	if sup == nil || sup.ID != 11 {
		t.Fatalf("unexpected supplier: %+v", sup)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/suppliers/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Acme" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["supplier_type"] != "online" {
		t.Errorf("supplier_type = %v", captured.body["supplier_type"])
	}
	if captured.body["website"] != "https://acme.test" {
		t.Errorf("website = %v", captured.body["website"])
	}
	if captured.body["account_number"] != "ACC-1" {
		t.Errorf("account_number = %v", captured.body["account_number"])
	}
	if captured.body["tax_free_paperwork_filed"] != true {
		t.Errorf("tax_free_paperwork_filed = %v, want true", captured.body["tax_free_paperwork_filed"])
	}
	if captured.body["notes"] != "net-30" {
		t.Errorf("notes = %v", captured.body["notes"])
	}
}

// TestGetSupplier_HydratesItems confirms the retrieve serializer's embedded
// item catalogue decodes onto Supplier.Items.
func TestGetSupplier_HydratesItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/inventory/suppliers/11/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":11,"name":"Acme","supplier_type":"online","item_count":2,"items":[{"id":1,"item":"i1","item_name":"Widget","supplier_sku":"SKU1","is_primary":true},{"id":2,"item":"i2","item_name":"Gadget"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	sup, err := c.GetSupplier(context.Background(), "11")
	if err != nil {
		t.Fatalf("GetSupplier: %v", err)
	}
	if len(sup.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(sup.Items))
	}
	if sup.Items[0].ItemName != "Widget" || !sup.Items[0].IsPreferred {
		t.Errorf("item[0] = %+v", sup.Items[0])
	}
}

// TestDeleteSupplier_Contract confirms DELETE to the detail URL.
func TestDeleteSupplier_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteSupplier(context.Background(), "11"); err != nil {
		t.Fatalf("DeleteSupplier: %v", err)
	}
	if method != http.MethodDelete || path != "/api/inventory/suppliers/11/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}
