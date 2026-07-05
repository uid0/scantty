package forgekeyapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListFirmwareVersionsNumericFKs feeds the REAL FirmwareVersionSerializer
// shape: device_type and created_by are integer foreign keys (JSON NUMBERS),
// with the human labels in the separate device_type_name / created_by_username
// fields. The old `string` typing crashed with
// `cannot unmarshal number into ... device_type of type string`.
func TestListFirmwareVersionsNumericFKs(t *testing.T) {
	body := `[{
		"id":"7a7a7a7a-1111-2222-3333-444444444444","version":"1.4.2",
		"device_type":3,"device_type_name":"Indicator","device_type_code":"indicator",
		"is_active":true,"created_by":5,"created_by_username":"ada",
		"created_at":"2026-01-02T03:04:05Z"
	}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vers, err := c.ListFirmwareVersions(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListFirmwareVersions: %v", err)
	}
	if len(vers) != 1 {
		t.Fatalf("versions = %d, want 1", len(vers))
	}
	v := vers[0]
	if v.Version != "1.4.2" || v.DeviceTypeName != "Indicator" || v.CreatedByUsername != "ada" {
		t.Fatalf("unexpected version: %+v", v)
	}
}

// TestListFirmwareUpdatesNumericRequestedBy feeds the REAL
// DeviceFirmwareUpdateSerializer shape: requested_by is an integer user FK, and
// the display fields are device_mac_address / firmware_version_string /
// requested_by_username. The old `RequestedBy string` typing crashed on any row
// with a requester.
func TestListFirmwareUpdatesNumericRequestedBy(t *testing.T) {
	body := `{"count":1,"results":[{
		"id":"8b8b8b8b-1111-2222-3333-444444444444",
		"device":"9c9c9c9c-1111-2222-3333-444444444444","device_mac_address":"AA:BB:CC:DD:EE:FF",
		"firmware_version":"7a7a7a7a-1111-2222-3333-444444444444","firmware_version_string":"1.4.2",
		"requested_by":5,"requested_by_username":"ada","status":"pending",
		"requested_at":"2026-01-02T03:04:05Z"
	}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ups, err := c.ListFirmwareUpdates(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListFirmwareUpdates: %v", err)
	}
	if len(ups) != 1 {
		t.Fatalf("updates = %d, want 1", len(ups))
	}
	u := ups[0]
	if u.DeviceMACAddress != "AA:BB:CC:DD:EE:FF" || u.FirmwareVersionStr != "1.4.2" || u.RequestedByUsername != "ada" {
		t.Fatalf("unexpected update: %+v", u)
	}
}
