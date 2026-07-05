package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListDonationsDecodesStringID feeds the REAL DonationListSerializer shape:
// a UUID string id (not the old int), a date-only date_received, and
// DecimalField money strings. The previous `ID int` typing crashed with
// `cannot unmarshal string into ... id of type int`.
func TestListDonationsDecodesStringID(t *testing.T) {
	body := `{"count":1,"next":null,"previous":null,"results":[{
		"id":"a1b2c3d4-0000-4444-8888-abcdef012345",
		"donation_number":"D-0001","donor_name":"Ada Lovelace","date_received":"2026-06-01",
		"status":"received","estimated_number_of_items":3,
		"estimated_value":"100.00","associated_costs":"5.00","net_value":"95.00",
		"total_items":3,"total_quantity":10,"created_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListDonations(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListDonations: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(page.Results))
	}
	d := page.Results[0]
	if d.ID != "a1b2c3d4-0000-4444-8888-abcdef012345" {
		t.Errorf("id = %q, want the UUID string", d.ID)
	}
	if d.DateReceived.IsZero() || d.DateReceived.Format("2006-01-02") != "2026-06-01" {
		t.Errorf("date_received = %v, want 2026-06-01", d.DateReceived)
	}
	if d.NetValue.String() != "95.00" || d.EstimatedValue.String() != "100.00" {
		t.Errorf("decimals = net %q est %q", d.NetValue, d.EstimatedValue)
	}
}

// TestListTaxReceiptsDecodesUUIDFKs feeds the REAL TaxReceiptSerializer shape:
// UUID id + UUID serial_number + a `donation` FK that is a UUID string (FK to a
// UUID-PK Donation) + a date-only issued_date. The old int id / int donation
// typing crashed the decode.
func TestListTaxReceiptsDecodesUUIDFKs(t *testing.T) {
	body := `{"count":1,"results":[{
		"id":"11111111-2222-3333-4444-555555555555",
		"serial_number":"66666666-7777-8888-9999-000000000000",
		"donation":"a1b2c3d4-0000-4444-8888-abcdef012345",
		"donation_number":"D-0001","donor_name":"Ada Lovelace",
		"issued_date":"2026-06-02","issued_by":7,"issued_by_username":"clerk",
		"pdf_file":"/media/receipts/1.pdf","is_copy":false,
		"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := New(srv.URL)
	page, err := c.ListTaxReceipts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTaxReceipts: %v", err)
	}
	if len(page.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(page.Results))
	}
	r := page.Results[0]
	if r.Donation != "a1b2c3d4-0000-4444-8888-abcdef012345" {
		t.Errorf("donation FK = %q, want the UUID string", r.Donation)
	}
	if r.IssuedDate.IsZero() || r.IssuedDate.Format("2006-01-02") != "2026-06-02" {
		t.Errorf("issued_date = %v, want 2026-06-02", r.IssuedDate)
	}
}
