package forgekeyapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetDeviceTypeDecodesFullShape confirms the read struct decodes every field
// the `fields = "__all__"` serializer emits: id, name, code, description,
// is_active — and that IntID coerces the numeric pk to an int.
//
// It used to say "the float64 pk", which stopped being what this test exercises
// the moment client.go's jsonDecoder set UseNumber: the number now arrives as a
// json.Number, so the assertion silently moved off the float64 arm it named onto
// the json.Number one. The wording is about the SERVER's shape now (a number) so
// it cannot go stale again the next time the decoder's representation changes,
// which is the whole reason a comment naming a Go type here was a liability —
// what the wire carries and what a decoder makes of it are different facts.
func TestGetDeviceTypeDecodesFullShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want := "/api/forgekey/device-types/3/"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":3,"name":"Badge Reader","code":"badge_reader","description":"door badge","is_active":true}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dt, err := c.GetDeviceType(context.Background(), 3)
	if err != nil {
		t.Fatalf("GetDeviceType: %v", err)
	}
	if dt.IntID() != 3 {
		t.Errorf("IntID = %d, want 3", dt.IntID())
	}
	if dt.Name != "Badge Reader" || dt.Code != "badge_reader" {
		t.Errorf("name/code = %q/%q", dt.Name, dt.Code)
	}
	if dt.Description != "door badge" {
		t.Errorf("description = %q, want %q", dt.Description, "door badge")
	}
	if !dt.IsActive {
		t.Errorf("is_active = false, want true")
	}
}

// TestCreateDeviceTypePostsFullSet verifies create POSTs the full writable set
// (name, code, description, is_active) to the collection endpoint.
func TestCreateDeviceTypePostsFullSet(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":9,"name":"Env Sensor","code":"env_sensor","description":"","is_active":true}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	dt, err := c.CreateDeviceType(context.Background(), DeviceTypeWrite{
		Name: "Env Sensor", Code: "env_sensor", Description: "", IsActive: true,
	})
	if err != nil {
		t.Fatalf("CreateDeviceType: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if want := "/api/forgekey/device-types/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	// code must be present on create.
	if gotBody["code"] != "env_sensor" {
		t.Errorf("body code = %v, want env_sensor", gotBody["code"])
	}
	if gotBody["name"] != "Env Sensor" {
		t.Errorf("body name = %v", gotBody["name"])
	}
	// description + is_active are sent even when default-ish (no omitempty).
	if _, ok := gotBody["description"]; !ok {
		t.Errorf("description missing from create body: %v", gotBody)
	}
	if gotBody["is_active"] != true {
		t.Errorf("is_active = %v, want true", gotBody["is_active"])
	}
	if dt.IntID() != 9 {
		t.Errorf("returned id = %d, want 9", dt.IntID())
	}
}

// TestUpdateDeviceTypeOmitsCode verifies an edit PATCHes to the detail URL and
// omits `code` (immutable after creation, mirroring the web) while still sending
// name/description/is_active.
func TestUpdateDeviceTypeOmitsCode(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":3,"name":"Badge Reader v2","code":"badge_reader","description":"","is_active":false}`))
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	// Note: Code left blank — the form never sets it on edit.
	_, err := c.UpdateDeviceType(context.Background(), 3, DeviceTypeWrite{
		Name: "Badge Reader v2", Description: "", IsActive: false,
	})
	if err != nil {
		t.Fatalf("UpdateDeviceType: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if want := "/api/forgekey/device-types/3/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if _, present := gotBody["code"]; present {
		t.Errorf("code must be omitted on edit, got body %v", gotBody)
	}
	if gotBody["name"] != "Badge Reader v2" {
		t.Errorf("name = %v", gotBody["name"])
	}
	// description "" and is_active false must still be sent so a PATCH can clear
	// them (no omitempty).
	if v, ok := gotBody["description"]; !ok || v != "" {
		t.Errorf("description = %v (present=%v), want \"\"", v, ok)
	}
	if v, ok := gotBody["is_active"]; !ok || v != false {
		t.Errorf("is_active = %v (present=%v), want false", v, ok)
	}
}

// TestDeleteDeviceType verifies delete issues DELETE to the detail URL.
func TestDeleteDeviceType(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, _ := New(Options{BaseURL: srv.URL})
	if err := c.DeleteDeviceType(context.Background(), 7); err != nil {
		t.Fatalf("DeleteDeviceType: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if want := "/api/forgekey/device-types/7/"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// TestListDeviceTypesToleratesBothShapes confirms the list decodes both a bare
// array and a paginated envelope (MaybeList).
func TestListDeviceTypesToleratesBothShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"bare array", `[{"id":1,"name":"Indicator","code":"indicator","is_active":true}]`},
		{"envelope", `{"count":1,"results":[{"id":1,"name":"Indicator","code":"indicator","is_active":true}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, _ := New(Options{BaseURL: srv.URL})
			types, err := c.ListDeviceTypes(context.Background())
			if err != nil {
				t.Fatalf("ListDeviceTypes: %v", err)
			}
			if len(types) != 1 || types[0].Code != "indicator" {
				t.Fatalf("types = %+v", types)
			}
			if types[0].IntID() != 1 {
				t.Errorf("IntID = %d, want 1", types[0].IntID())
			}
		})
	}
}
