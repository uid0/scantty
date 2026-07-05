package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListVendorsDecodesDateOnly feeds the REAL VendorSerializer shape: a UUID
// string id and Django DateField values ("2026-09-09", plus null) for the
// tdlr/coi expiry fields. The previous *time.Time typing crashed here with
// `parsing time "2026-09-09" ... cannot parse "" as "T"`.
func TestListVendorsDecodesDateOnly(t *testing.T) {
	body := `{"count":1,"next":null,"previous":null,"results":[{
		"id":"3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f",
		"name":"Acme Electric","vendor_kind":"electrical","vendor_kind_display":"Electrical",
		"tdlr_license_number":"TX-12345","tdlr_license_expires_at":"2026-09-09","tdlr_is_expired":false,
		"coi_provider":"Travelers","coi_policy_number":"POL-9","coi_expires_at":null,"coi_is_expired":false,
		"is_active":true,"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListVendors(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListVendors: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(page.Results))
	}
	v := page.Results[0]
	if v.ID != "3f4c8b2a-1e2d-4a5b-8c9d-0a1b2c3d4e5f" {
		t.Errorf("id = %q, want the UUID string", v.ID)
	}
	if v.TDLRLicenseExpiresAt.IsZero() || v.TDLRLicenseExpiresAt.Format("2006-01-02") != "2026-09-09" {
		t.Errorf("tdlr expiry = %v, want 2026-09-09", v.TDLRLicenseExpiresAt)
	}
	if !v.COIExpiresAt.IsZero() {
		t.Errorf("coi expiry = %v, want zero (null)", v.COIExpiresAt)
	}
}
