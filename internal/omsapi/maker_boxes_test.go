package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateMakerBox_Contract pins the maker-box create body: POST to the
// collection, every writable field present, bin_id as a JSON string when set,
// status carried, and the datetime fields emitted as ISO strings.
func TestCreateMakerBox_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":11,"bin_id":"PSB-011","assigned_username":"jdoe","status":"valid"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	box, err := c.CreateMakerBox(context.Background(), MakerBoxWrite{
		BinID:            strptr("PSB-011"),
		AssignedUsername: "jdoe",
		FirstName:        "Jane",
		LastName:         "Doe",
		Email:            "jane@example.com",
		Status:           "valid",
		IdentitySource:   "manual",
		ExpiresAt:        strptr("2026-12-31T00:00:00Z"),
		Notes:            "corner shelf",
	})
	if err != nil {
		t.Fatalf("CreateMakerBox: %v", err)
	}
	if box == nil || box.ID != 11 || box.BinID != "PSB-011" {
		t.Fatalf("unexpected box: %+v", box)
	}
	if captured.method != http.MethodPost || captured.path != "/api/maker-boxes/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["bin_id"] != "PSB-011" {
		t.Errorf("bin_id = %v", captured.body["bin_id"])
	}
	if captured.body["assigned_username"] != "jdoe" {
		t.Errorf("assigned_username = %v", captured.body["assigned_username"])
	}
	if captured.body["status"] != "valid" {
		t.Errorf("status = %v", captured.body["status"])
	}
	if captured.body["identity_source"] != "manual" {
		t.Errorf("identity_source = %v", captured.body["identity_source"])
	}
	if captured.body["email"] != "jane@example.com" {
		t.Errorf("email = %v", captured.body["email"])
	}
	if captured.body["expires_at"] != "2026-12-31T00:00:00Z" {
		t.Errorf("expires_at = %v", captured.body["expires_at"])
	}
	// Unset datetimes must be present as explicit null so a PATCH can clear them.
	for _, k := range []string{"assigned_at", "conversion_completed_at", "paid_at"} {
		v, present := captured.body[k]
		if !present {
			t.Errorf("%s key must be present (as null)", k)
		}
		if v != nil {
			t.Errorf("%s = %v, want null", k, v)
		}
	}
}

// TestCreateMakerBox_BlankBinNull confirms a blank bin_id serializes as JSON
// null (never ""), because the partial-unique index only excludes NULL — two
// empty-string bin_ids would collide.
func TestCreateMakerBox_BlankBinNull(t *testing.T) {
	var body map[string]any
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12,"bin_id":null,"status":"pre_conversion"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateMakerBox(context.Background(), MakerBoxWrite{
		AssignedUsername: "queued",
		Status:           "pre_conversion",
	}); err != nil {
		t.Fatalf("CreateMakerBox: %v", err)
	}
	v, present := body["bin_id"]
	if !present {
		t.Errorf("bin_id key must be present (as null), got %s", raw)
	}
	if v != nil {
		t.Errorf("bin_id = %v, want null", v)
	}
	// Blank string fields still ride as empty strings, not omitted.
	if body["notes"] != "" {
		t.Errorf("notes = %v, want empty string", body["notes"])
	}
	if body["identity_source"] != "" {
		t.Errorf("identity_source = %v, want empty string", body["identity_source"])
	}
}

// TestUpdateMakerBox_Contract confirms edits go out as PATCH to the detail URL
// keyed by the integer id.
func TestUpdateMakerBox_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":7,"bin_id":"PSB-007","status":"grace"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	box, err := c.UpdateMakerBox(context.Background(), 7, MakerBoxWrite{
		BinID:  strptr("PSB-007"),
		Status: "grace",
	})
	if err != nil {
		t.Fatalf("UpdateMakerBox: %v", err)
	}
	if box == nil || box.Status != "grace" {
		t.Fatalf("unexpected box: %+v", box)
	}
	if method != http.MethodPatch || path != "/api/maker-boxes/7/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestDeleteMakerBox_Contract confirms DELETE to the detail URL with a 204.
func TestDeleteMakerBox_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteMakerBox(context.Background(), 7); err != nil {
		t.Fatalf("DeleteMakerBox: %v", err)
	}
	if method != http.MethodDelete || path != "/api/maker-boxes/7/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestMakerBox_DecodeRoundTrip pins the read shape: int id, null bin_id decodes
// to "", and the datetime pointers hydrate.
func TestMakerBox_DecodeRoundTrip(t *testing.T) {
	payload := `{
		"id": 42,
		"bin_id": null,
		"assigned_username": "queued",
		"first_name": "Q",
		"last_name": "User",
		"display_name": "Q User",
		"status": "pre_conversion",
		"identity_source": "common_api",
		"expires_at": "2026-12-31T00:00:00Z",
		"paid_at": null,
		"notes": "waiting",
		"created_at": "2026-07-06T14:00:00Z",
		"updated_at": "2026-07-06T14:00:00Z"
	}`
	var box MakerBox
	if err := json.Unmarshal([]byte(payload), &box); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if box.ID != 42 {
		t.Errorf("id = %d", box.ID)
	}
	if box.BinID != "" {
		t.Errorf("bin_id = %q, want empty (null decodes to \"\")", box.BinID)
	}
	if box.Status != "pre_conversion" {
		t.Errorf("status = %q", box.Status)
	}
	if box.IdentitySource != "common_api" {
		t.Errorf("identity_source = %q", box.IdentitySource)
	}
	if box.ExpiresAt == nil || box.ExpiresAt.Year() != 2026 {
		t.Errorf("expires_at = %v", box.ExpiresAt)
	}
	if box.PaidAt != nil {
		t.Errorf("paid_at = %v, want nil", box.PaidAt)
	}
}
