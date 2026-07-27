package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListSupplierAgreements_DecodesActiveSetForOneSupplier pins the request the
// PO-create picker makes: one supplier's ACTIVE agreements, read out of the
// paginated results envelope. Asking for the active set server-side is the
// point — retired paperwork must never reach the picker, and filtering it
// client-side would depend on every caller remembering to.
func TestListSupplierAgreements_DecodesActiveSetForOneSupplier(t *testing.T) {
	var gotPath, gotSupplier, gotActive string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSupplier = r.URL.Query().Get("supplier")
		gotActive = r.URL.Query().Get("is_active")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"count": 2,
			"next": null,
			"previous": null,
			"results": [
				{"id": 4, "supplier": 7, "supplier_name": "Acme Supply",
				 "name": "2026 nonprofit pricing", "notes": "15% off list, net 30",
				 "document": "/media/supplier_agreements/acme.pdf",
				 "is_active": true, "created_at": "2026-01-04T10:00:00Z",
				 "updated_at": "2026-01-04T10:00:00Z"},
				{"id": 9, "supplier": 7, "supplier_name": "Acme Supply",
				 "name": "Standing quote Q3", "notes": "", "document": null,
				 "is_active": true, "created_at": "2026-07-01T10:00:00Z",
				 "updated_at": "2026-07-01T10:00:00Z"}
			]
		}`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ListSupplierAgreements(context.Background(), 7)
	if err != nil {
		t.Fatalf("ListSupplierAgreements: %v", err)
	}

	if gotPath != "/api/inventory/supplier-agreements/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotSupplier != "7" {
		t.Errorf("?supplier = %q, want 7", gotSupplier)
	}
	if gotActive != "true" {
		t.Errorf("?is_active = %q, want true (retired agreements must not be offered)", gotActive)
	}

	if len(rows) != 2 {
		t.Fatalf("got %d agreement(s), want 2", len(rows))
	}
	first := rows[0]
	if first.ID != 4 || first.Supplier != 7 {
		t.Errorf("ids = %d/%d, want 4/7", first.ID, first.Supplier)
	}
	if first.Name != "2026 nonprofit pricing" {
		t.Errorf("name = %q", first.Name)
	}
	if first.Notes != "15% off list, net 30" {
		t.Errorf("notes = %q — the terms are why an operator cites an agreement", first.Notes)
	}
	if first.SupplierName != "Acme Supply" {
		t.Errorf("supplier_name = %q", first.SupplierName)
	}
	if !first.IsActive {
		t.Errorf("is_active decoded false on an active agreement")
	}
	// A null `document` and a blank `notes` are ordinary on this endpoint —
	// neither may take the whole decode down with it.
	if rows[1].Notes != "" || rows[1].Name != "Standing quote Q3" {
		t.Errorf("second row decoded wrong: %+v", rows[1])
	}
}

// TestListSupplierAgreements_EmptyIsNotAnError: a supplier with no agreements is
// the common case, and the create form must read it as "offer no picker", not
// as a failure.
func TestListSupplierAgreements_EmptyIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"next":null,"previous":null,"results":[]}`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ListSupplierAgreements(context.Background(), 12)
	if err != nil {
		t.Fatalf("ListSupplierAgreements: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d row(s), want 0", len(rows))
	}
}

// TestCreatePurchaseOrder_SupplierAgreementOmittedWhenUnset is the contract that
// keeps "no agreement" from turning into a 400: the backend validates any
// supplier_agreement it is handed against the order's supplier, so the key has
// to be ABSENT — not null, not 0 — when the operator picked none.
func TestCreatePurchaseOrder_SupplierAgreementOmittedWhenUnset(t *testing.T) {
	srv, body := captureCreatePO(t)
	c := New(srv.URL)

	if _, err := c.CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Supplier: 7,
		Items:    []PurchaseOrderCreateItem{{Description: "Bolts", Quantity: 2}},
	}); err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	if v, present := (*body)["supplier_agreement"]; present {
		t.Errorf("supplier_agreement should be omitted when unset, got %v", v)
	}
}

// TestCreatePurchaseOrder_SupplierAgreementSentWhenPicked is the other half: a
// chosen agreement rides as a bare id, the shape the create serializer's
// PrimaryKeyRelatedField expects.
func TestCreatePurchaseOrder_SupplierAgreementSentWhenPicked(t *testing.T) {
	srv, body := captureCreatePO(t)
	c := New(srv.URL)

	agreement := 4
	if _, err := c.CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Supplier:            7,
		SupplierAgreementID: &agreement,
		Items:               []PurchaseOrderCreateItem{{Description: "Bolts", Quantity: 2}},
	}); err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	got, present := (*body)["supplier_agreement"]
	if !present {
		t.Fatalf("supplier_agreement missing from payload: %v", *body)
	}
	if n, ok := got.(float64); !ok || n != 4 {
		t.Errorf("supplier_agreement = %v (%T), want the bare id 4", got, got)
	}
}

// TestPurchaseOrder_DecodesAgreementDetails: the read serializer nests
// {id, name} so a detail screen can name the agreement without a second
// request, and returns null when the order cites none.
func TestPurchaseOrder_DecodesAgreementDetails(t *testing.T) {
	var withAgreement PurchaseOrder
	if err := json.Unmarshal([]byte(`{
		"id": "po-1", "po_number": "PO-2026-0042", "status": "draft",
		"supplier_agreement": 4,
		"supplier_agreement_details": {"id": 4, "name": "2026 nonprofit pricing"}
	}`), &withAgreement); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if withAgreement.SupplierAgreement == nil || *withAgreement.SupplierAgreement != 4 {
		t.Errorf("supplier_agreement = %v, want 4", withAgreement.SupplierAgreement)
	}
	if withAgreement.SupplierAgreementRef == nil ||
		withAgreement.SupplierAgreementRef.Name != "2026 nonprofit pricing" {
		t.Errorf("supplier_agreement_details = %+v", withAgreement.SupplierAgreementRef)
	}

	var without PurchaseOrder
	if err := json.Unmarshal([]byte(`{
		"id": "po-2", "po_number": "PO-2026-0043", "status": "draft",
		"supplier_agreement": null, "supplier_agreement_details": null
	}`), &without); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if without.SupplierAgreement != nil || without.SupplierAgreementRef != nil {
		t.Errorf("an order with no agreement decoded non-nil: %v / %+v",
			without.SupplierAgreement, without.SupplierAgreementRef)
	}
}

// captureCreatePO serves the PO create endpoint and hands back the POSTed body.
func captureCreatePO(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"po-uuid","po_number":"PO-2026-0042"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}
