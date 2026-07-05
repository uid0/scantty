package forgekeyapi

import (
	"context"
	"encoding/json"
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

const rolloutBody = `{
	"id":"11111111-1111-2222-3333-444444444444",
	"firmware_version":"7a7a7a7a-1111-2222-3333-444444444444","firmware_version_string":"1.4.2",
	"device_type_name":"Indicator","name":"canary","status":"draft",
	"batch_size_percent":25,"interval_minutes":90,
	"created_by":5,"created_by_username":"ada",
	"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z",
	"started_at":null,"completed_at":null,"last_advanced_at":null,
	"progress":{"total":10,"on_target":2,"pending":1,"in_progress":0,"failed":1,"remaining":6}
}`

// TestListFirmwareRolloutsDecodesProgress feeds the REAL FirmwareRolloutSerializer
// shape (UUID firmware_version FK, integer created_by FK, nested progress) and
// verifies the numeric-FK-safe decode plus the progress block.
func TestListFirmwareRolloutsDecodesProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[` + rolloutBody + `]}`))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rs, err := c.ListFirmwareRollouts(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListFirmwareRollouts: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("rollouts = %d, want 1", len(rs))
	}
	r := rs[0]
	if r.FirmwareVersionStr != "1.4.2" || r.Status != "draft" || r.CreatedByUsername != "ada" {
		t.Fatalf("unexpected rollout: %+v", r)
	}
	if r.BatchSizePercent != 25 || r.IntervalMinutes != 90 {
		t.Fatalf("batch/interval = %d/%d, want 25/90", r.BatchSizePercent, r.IntervalMinutes)
	}
	if r.Progress.Total != 10 || r.Progress.OnTarget != 2 || r.Progress.Remaining != 6 || r.Progress.Failed != 1 {
		t.Fatalf("unexpected progress: %+v", r.Progress)
	}
}

// TestCreateFirmwareRollout_PostsBody verifies the create POSTs the New-Rollout
// form fields (and only those) to the collection endpoint.
func TestCreateFirmwareRollout_PostsBody(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rolloutBody))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := c.CreateFirmwareRollout(context.Background(), FirmwareRolloutCreate{
		FirmwareVersion:  "7a7a7a7a-1111-2222-3333-444444444444",
		BatchSizePercent: 25,
		IntervalMinutes:  90,
		Name:             "canary",
	})
	if err != nil {
		t.Fatalf("CreateFirmwareRollout: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/forgekey/firmware-rollouts/" {
		t.Fatalf("%s %s, want POST /api/forgekey/firmware-rollouts/", gotMethod, gotPath)
	}
	if gotBody["firmware_version"] != "7a7a7a7a-1111-2222-3333-444444444444" {
		t.Errorf("firmware_version = %v", gotBody["firmware_version"])
	}
	if gotBody["batch_size_percent"] != float64(25) || gotBody["interval_minutes"] != float64(90) {
		t.Errorf("batch/interval = %v/%v", gotBody["batch_size_percent"], gotBody["interval_minutes"])
	}
	if gotBody["name"] != "canary" {
		t.Errorf("name = %v", gotBody["name"])
	}
	// status/created_by/timestamps are server-set — never sent.
	for _, k := range []string{"status", "created_by", "created_at", "id"} {
		if _, present := gotBody[k]; present {
			t.Errorf("create body leaked read-only field %q", k)
		}
	}
	if out.FirmwareVersionStr != "1.4.2" {
		t.Errorf("decoded rollout = %+v", out)
	}
}

// TestFirmwareRolloutActions_PostTrailingSlashPaths pins the lifecycle action
// URLs to the DefaultRouter trailing-slash contract the web client uses.
func TestFirmwareRolloutActions_PostTrailingSlashPaths(t *testing.T) {
	cases := []struct {
		name string
		call func(*Client) error
		path string
	}{
		{"start", func(c *Client) error { _, e := c.StartFirmwareRollout(context.Background(), "roll-1"); return e }, "/api/forgekey/firmware-rollouts/roll-1/start/"},
		{"pause", func(c *Client) error { _, e := c.PauseFirmwareRollout(context.Background(), "roll-1"); return e }, "/api/forgekey/firmware-rollouts/roll-1/pause/"},
		{"cancel", func(c *Client) error { _, e := c.CancelFirmwareRollout(context.Background(), "roll-1"); return e }, "/api/forgekey/firmware-rollouts/roll-1/cancel/"},
		{"advance", func(c *Client) error { _, e := c.AdvanceFirmwareRollout(context.Background(), "roll-1"); return e }, "/api/forgekey/firmware-rollouts/roll-1/advance/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotMethod string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotMethod = r.URL.Path, r.Method
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(rolloutBody))
			}))
			defer srv.Close()
			c, err := New(Options{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != tc.path {
				t.Errorf("path = %q, want %q", gotPath, tc.path)
			}
		})
	}
}
