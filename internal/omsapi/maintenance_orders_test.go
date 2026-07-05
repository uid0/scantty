package omsapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListMaintenanceOrdersDecodesUUIDs feeds the REAL
// ThirdPartyWorkOrderSerializer shape: a UUID order id plus `vendor` and
// `asset` FKs that are UUID strings (FKs to UUID-PK models). The old
// int / *int typings crashed every row of the maintenance-orders list.
func TestListMaintenanceOrdersDecodesUUIDs(t *testing.T) {
	body := `{"count":1,"results":[{
		"id":"c0ffee00-1111-2222-3333-444455556666",
		"title":"Replace compressor belt",
		"vendor":"3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f","vendor_name":"Acme Electric",
		"asset":"a1b2c3d4-0000-4444-8888-abcdef012345","status":"open",
		"created_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListMaintenanceOrders(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListMaintenanceOrders: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(page.Results))
	}
	mo := page.Results[0]
	if fmt.Sprintf("%v", mo.ID) != "c0ffee00-1111-2222-3333-444455556666" {
		t.Errorf("id = %v, want the UUID string", mo.ID)
	}
	if mo.Vendor == nil || *mo.Vendor != "3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f" {
		t.Errorf("vendor = %v, want the UUID string", mo.Vendor)
	}
	if mo.Asset == nil || *mo.Asset != "a1b2c3d4-0000-4444-8888-abcdef012345" {
		t.Errorf("asset = %v, want the UUID string", mo.Asset)
	}
	if mo.VendorName != "Acme Electric" {
		t.Errorf("vendor_name = %q", mo.VendorName)
	}
}
