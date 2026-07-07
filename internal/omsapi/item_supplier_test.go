package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateItemSupplier_Contract pins the create body: POST to the
// item-suppliers collection with item (UUID) + supplier (pk) + supplier_sku
// always present, the qty/lead ints and is_primary present, and unit_cost/
// package_cost sent as strings when set.
func TestCreateItemSupplier_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":11,"item":"itm-1","supplier":4,"supplier_name":"Acme","is_primary":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	link, err := c.CreateItemSupplier(context.Background(), ItemSupplierWrite{
		Item:               "itm-1",
		Supplier:           4,
		SupplierSKU:        "ACME-42",
		SupplierURL:        "https://acme.test/42",
		UnitCost:           strptr("1.50"),
		PackageCost:        strptr("15.00"),
		QuantityPerPackage: 10,
		AverageLeadTime:    5,
		IsPrimary:          true,
	})
	if err != nil {
		t.Fatalf("CreateItemSupplier: %v", err)
	}
	if link == nil || link.ID != 11 || link.SupplierName != "Acme" {
		t.Fatalf("unexpected link: %+v", link)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/item-suppliers/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["item"] != "itm-1" {
		t.Errorf("item = %v", captured.body["item"])
	}
	if captured.body["supplier"].(float64) != 4 {
		t.Errorf("supplier = %v (%T)", captured.body["supplier"], captured.body["supplier"])
	}
	if captured.body["supplier_sku"] != "ACME-42" {
		t.Errorf("supplier_sku = %v", captured.body["supplier_sku"])
	}
	if captured.body["supplier_url"] != "https://acme.test/42" {
		t.Errorf("supplier_url = %v", captured.body["supplier_url"])
	}
	if captured.body["unit_cost"] != "1.50" {
		t.Errorf("unit_cost = %v", captured.body["unit_cost"])
	}
	if captured.body["package_cost"] != "15.00" {
		t.Errorf("package_cost = %v", captured.body["package_cost"])
	}
	if captured.body["quantity_per_package"].(float64) != 10 {
		t.Errorf("quantity_per_package = %v", captured.body["quantity_per_package"])
	}
	if captured.body["average_lead_time"].(float64) != 5 {
		t.Errorf("average_lead_time = %v", captured.body["average_lead_time"])
	}
	if captured.body["is_primary"] != true {
		t.Errorf("is_primary = %v", captured.body["is_primary"])
	}
}

// TestItemSupplierWrite_CostsNullWhenBlank confirms a blank cost is sent as an
// explicit JSON null (not omitted), so an edit that clears a cost actually
// reaches the backend — mirroring the web's `value || null`.
func TestItemSupplierWrite_CostsNullWhenBlank(t *testing.T) {
	raw, err := json.Marshal(ItemSupplierWrite{
		Item:        "itm-1",
		Supplier:    4,
		SupplierSKU: "SKU",
		// UnitCost / PackageCost left nil
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"unit_cost", "package_cost"} {
		v, present := m[key]
		if !present {
			t.Errorf("%s should be present (as null), got omitted", key)
			continue
		}
		if string(v) != "null" {
			t.Errorf("%s = %s, want null", key, v)
		}
	}
	// is_primary must also be present even when false (no omitempty).
	if v, present := m["is_primary"]; !present || string(v) != "false" {
		t.Errorf("is_primary = %s present=%v, want false present", v, present)
	}
}

// TestUpdateItemSupplier_Contract pins the edit: PATCH to the row's detail URL
// with the full field set.
func TestUpdateItemSupplier_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":42,"item":"itm-1","supplier":4}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.UpdateItemSupplier(context.Background(), 42, ItemSupplierWrite{
		Item:               "itm-1",
		Supplier:           4,
		SupplierSKU:        "SKU-9",
		QuantityPerPackage: 1,
		AverageLeadTime:    7,
	})
	if err != nil {
		t.Fatalf("UpdateItemSupplier: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/inventory/item-suppliers/42/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["supplier_sku"] != "SKU-9" {
		t.Errorf("supplier_sku = %v", captured.body["supplier_sku"])
	}
}

// TestSetItemSupplierPrimary_Contract confirms set-primary is a targeted partial
// PATCH: only is_primary=true, nothing else (item/supplier come from the
// instance server-side).
func TestSetItemSupplierPrimary_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":42,"is_primary":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.SetItemSupplierPrimary(context.Background(), 42)
	if err != nil {
		t.Fatalf("SetItemSupplierPrimary: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/inventory/item-suppliers/42/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["is_primary"] != true {
		t.Errorf("is_primary = %v", captured.body["is_primary"])
	}
	if len(captured.body) != 1 {
		t.Errorf("body should carry only is_primary, got %v", captured.body)
	}
}

// TestDeleteItemSupplier_Contract pins the DELETE URL.
func TestDeleteItemSupplier_Contract(t *testing.T) {
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
	if err := c.DeleteItemSupplier(context.Background(), 42); err != nil {
		t.Fatalf("DeleteItemSupplier: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/inventory/item-suppliers/42/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestListItemSuppliersForItem_FilterAndPaging confirms the item_id filter is
// sent and every page is followed (primary-first ordering is the server's job).
func TestListItemSuppliersForItem_FilterAndPaging(t *testing.T) {
	var gotItemID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotItemID = r.URL.Query().Get("item_id")
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"count":2,"next":null,"results":[{"id":2,"supplier":5,"supplier_name":"Beta"}]}`))
			return
		}
		next := srv2URL(r) + "?page=2"
		_, _ = w.Write([]byte(`{"count":2,"next":"` + next + `","results":[{"id":1,"supplier":4,"supplier_name":"Acme","is_primary":true}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	rows, err := c.ListItemSuppliersForItem(context.Background(), "itm-xyz")
	if err != nil {
		t.Fatalf("ListItemSuppliersForItem: %v", err)
	}
	if gotItemID != "itm-xyz" {
		t.Errorf("item_id filter = %q", gotItemID)
	}
	if len(rows) != 2 || rows[0].SupplierName != "Acme" || rows[1].SupplierName != "Beta" {
		t.Fatalf("rows = %+v", rows)
	}
}

// TestListAllSuppliers_Paging confirms the picker loader follows every page so a
// supplier on a later page is still selectable.
func TestListAllSuppliers_Paging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"count":2,"next":null,"results":[{"id":5,"name":"Beta"}]}`))
			return
		}
		next := srv2URL(r) + "?page=2"
		_, _ = w.Write([]byte(`{"count":2,"next":"` + next + `","results":[{"id":4,"name":"Acme"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	sups, err := c.ListAllSuppliers(context.Background())
	if err != nil {
		t.Fatalf("ListAllSuppliers: %v", err)
	}
	if len(sups) != 2 || sups[0].Name != "Acme" || sups[1].Name != "Beta" {
		t.Fatalf("suppliers = %+v", sups)
	}
}

// srv2URL rebuilds the request's own base URL (scheme+host) so a paginated
// `next` link points back at the test server.
func srv2URL(r *http.Request) string {
	return "http://" + r.Host + r.URL.Path
}
