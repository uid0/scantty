package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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
		AssetTag:               "DMS-ABCD1234",
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
	if captured.body["asset_tag"] != "DMS-ABCD1234" {
		t.Fatalf("asset_tag = %v", captured.body["asset_tag"])
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

// TestCreateAssetWithManualPDFUsesMultipart asserts that supplying a local
// manual path switches the asset create call to multipart/form-data, preserving
// scalar fields and uploading the file as manual_pdf.
func TestCreateAssetWithManualPDFUsesMultipart(t *testing.T) {
	manual := t.TempDir() + "/manual.pdf"
	if err := os.WriteFile(manual, []byte("%PDF manual"), 0o600); err != nil {
		t.Fatalf("write manual fixture: %v", err)
	}

	var captured struct {
		method      string
		path        string
		contentType string
		fields      map[string][]string
		fileName    string
		fileBody    string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		captured.contentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		captured.fields = r.MultipartForm.Value
		file, header, err := r.FormFile("manual_pdf")
		if err != nil {
			t.Fatalf("manual_pdf file: %v", err)
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		captured.fileName = header.Filename
		captured.fileBody = string(raw)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"asset-uuid","name":"Manual asset"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	user := 42
	a, err := c.CreateAsset(context.Background(), AssetWrite{
		Name:          "Manual asset",
		AssetTag:      "MAN-1",
		AmountPaid:    "0",
		Status:        "active",
		OwnershipType: "user",
		OwningUser:    &user,
		IsActive:      true,
		ManualPDFPath: manual,
	})
	if err != nil {
		t.Fatalf("CreateAsset multipart: %v", err)
	}
	if a == nil || a.Name != "Manual asset" {
		t.Fatalf("unexpected asset returned: %+v", a)
	}
	if captured.method != http.MethodPost || captured.path != "/api/inventory/assets/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if !strings.HasPrefix(captured.contentType, "multipart/form-data;") {
		t.Fatalf("Content-Type = %q", captured.contentType)
	}
	if captured.fields["asset_tag"][0] != "MAN-1" {
		t.Fatalf("asset_tag = %v", captured.fields["asset_tag"])
	}
	if captured.fields["owning_user"][0] != "42" {
		t.Fatalf("owning_user = %v", captured.fields["owning_user"])
	}
	if captured.fields["is_donation"][0] != "false" {
		t.Fatalf("is_donation = %v", captured.fields["is_donation"])
	}
	if captured.fileName != "manual.pdf" || captured.fileBody != "%PDF manual" {
		t.Fatalf("manual file = %q %q", captured.fileName, captured.fileBody)
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

// TestGetAssetDecodesSiteRequirements asserts the four flattened
// site-requirement fields (generates_heat_or_flame, needs_chilling,
// special_requirements, work_safety_notes) decode off the asset detail
// payload — the read side of the #880 parity add. work_safety_notes carries a
// newline to confirm multiline free text survives the round-trip.
func TestGetAssetDecodesSiteRequirements(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"asset-1","name":"Kiln",
			"generates_heat_or_flame":true,
			"needs_chilling":false,
			"special_requirements":"Bolt to floor; 3-phase only",
			"work_safety_notes":"Wear heat gloves.\nNo loose sleeves."
		}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	a, err := c.GetAsset(context.Background(), "asset-1")
	if err != nil {
		t.Fatalf("GetAsset: %v", err)
	}
	if !a.GeneratesHeatOrFlame {
		t.Errorf("GeneratesHeatOrFlame = false, want true")
	}
	if a.NeedsChilling {
		t.Errorf("NeedsChilling = true, want false")
	}
	if a.SpecialRequirements != "Bolt to floor; 3-phase only" {
		t.Errorf("SpecialRequirements = %q", a.SpecialRequirements)
	}
	if a.WorkSafetyNotes != "Wear heat gloves.\nNo loose sleeves." {
		t.Errorf("WorkSafetyNotes = %q", a.WorkSafetyNotes)
	}
}

// TestCreateAssetSendsSiteRequirements asserts the four new site-requirement
// fields ride the JSON create body: the booleans are always present (false is
// not dropped), and the free-text fields serialize when set but are omitted
// when nil (mirroring notes/condition_notes).
func TestCreateAssetSendsSiteRequirements(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"asset-uuid","name":"Kiln"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	special := "Bolt to floor"
	if _, err := c.CreateAsset(context.Background(), AssetWrite{
		Name:                 "Kiln",
		AmountPaid:           "0",
		Status:               "active",
		OwnershipType:        "space",
		IsActive:             true,
		GeneratesHeatOrFlame: true,
		NeedsChilling:        false,
		SpecialRequirements:  &special,
		// WorkSafetyNotes left nil → must be omitted.
	}); err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}

	if body["generates_heat_or_flame"] != true {
		t.Errorf("generates_heat_or_flame = %v, want true", body["generates_heat_or_flame"])
	}
	// A false boolean must still be present on the wire (no omitempty).
	if v, ok := body["needs_chilling"]; !ok || v != false {
		t.Errorf("needs_chilling = %v (present=%v), want false present", v, ok)
	}
	if body["special_requirements"] != "Bolt to floor" {
		t.Errorf("special_requirements = %v", body["special_requirements"])
	}
	// Nil free-text is omitted (mirrors notes/condition_notes).
	if _, ok := body["work_safety_notes"]; ok {
		t.Errorf("work_safety_notes should be omitted when nil, got %v", body["work_safety_notes"])
	}
}

// TestCreateAssetMultipartSendsSiteRequirements asserts the site-requirement
// fields also ride the multipart path (taken when a manual PDF is attached):
// booleans always present, free-text only when set.
func TestCreateAssetMultipartSendsSiteRequirements(t *testing.T) {
	manual := t.TempDir() + "/m.pdf"
	if err := os.WriteFile(manual, []byte("%PDF"), 0o600); err != nil {
		t.Fatalf("write manual: %v", err)
	}
	var fields map[string][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		fields = r.MultipartForm.Value
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","name":"Kiln"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	notes := "No loose sleeves"
	if _, err := c.CreateAsset(context.Background(), AssetWrite{
		Name:                 "Kiln",
		AmountPaid:           "0",
		Status:               "active",
		OwnershipType:        "space",
		IsActive:             true,
		GeneratesHeatOrFlame: true,
		WorkSafetyNotes:      &notes,
		ManualPDFPath:        manual,
	}); err != nil {
		t.Fatalf("CreateAsset multipart: %v", err)
	}

	if got := fields["generates_heat_or_flame"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("generates_heat_or_flame = %v, want [true]", got)
	}
	if got := fields["needs_chilling"]; len(got) != 1 || got[0] != "false" {
		t.Errorf("needs_chilling = %v, want [false]", got)
	}
	if got := fields["work_safety_notes"]; len(got) != 1 || got[0] != "No loose sleeves" {
		t.Errorf("work_safety_notes = %v", got)
	}
	// special_requirements left nil → omitted from multipart too.
	if _, ok := fields["special_requirements"]; ok {
		t.Errorf("special_requirements should be omitted when nil, got %v", fields["special_requirements"])
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
