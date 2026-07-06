package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetThermostat_Hydrates confirms the registry read decodes the full
// serializer shape — the two Location FKs (int), the UUID controlled_asset,
// the derived *_name labels, and the read-only needs_review flag.
func TestGetThermostat_Hydrates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/climate/thermostats/12/" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":12,"label":"Wood shop north","location":3,"location_name":"Hall",
			"controls_location":4,"controls_location_name":"Wood shop",
			"controlled_asset":"a1b2c3d4-0000-4000-8000-000000000000","controlled_asset_name":"RTU-1",
			"manufacturer":"Honeywell","model":"T6 Pro","notes":"n","needs_review":true,
			"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	th, err := c.GetThermostat(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetThermostat: %v", err)
	}
	if th.ID != 12 || th.Label != "Wood shop north" {
		t.Fatalf("unexpected thermostat: %+v", th)
	}
	if th.Location == nil || *th.Location != 3 || th.LocationName != "Hall" {
		t.Errorf("location = %v / %q", th.Location, th.LocationName)
	}
	if th.ControlsLocation == nil || *th.ControlsLocation != 4 {
		t.Errorf("controls_location = %v", th.ControlsLocation)
	}
	if th.ControlledAsset == nil || *th.ControlledAsset != "a1b2c3d4-0000-4000-8000-000000000000" {
		t.Errorf("controlled_asset = %v", th.ControlledAsset)
	}
	if th.ControlledAssetName == nil || *th.ControlledAssetName != "RTU-1" {
		t.Errorf("controlled_asset_name = %v", th.ControlledAssetName)
	}
	if th.Manufacturer != "Honeywell" || th.Model != "T6 Pro" || th.Notes != "n" {
		t.Errorf("mfr/model/notes = %q/%q/%q", th.Manufacturer, th.Model, th.Notes)
	}
	if !th.NeedsReview {
		t.Errorf("needs_review should be true")
	}
}

// TestCreateThermostat_Contract pins the create body: POST to the collection
// with the FULL writable field set, location as a JSON number, and the
// nullable FKs carried through when set.
func TestCreateThermostat_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":9,"label":"Front office","location":3}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	th, err := c.CreateThermostat(context.Background(), ThermostatWrite{
		Label:            "Front office",
		Location:         3,
		ControlsLocation: intptr(4),
		ControlledAsset:  strptr("a1b2c3d4-0000-4000-8000-000000000000"),
		Manufacturer:     "Honeywell",
		Model:            "T6 Pro",
		Notes:            "hallway unit",
	})
	if err != nil {
		t.Fatalf("CreateThermostat: %v", err)
	}
	if th == nil || th.ID != 9 {
		t.Fatalf("unexpected thermostat: %+v", th)
	}
	if captured.method != http.MethodPost || captured.path != "/api/climate/thermostats/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["label"] != "Front office" {
		t.Errorf("label = %v", captured.body["label"])
	}
	if captured.body["location"].(float64) != 3 {
		t.Errorf("location = %v (%T)", captured.body["location"], captured.body["location"])
	}
	if captured.body["controls_location"].(float64) != 4 {
		t.Errorf("controls_location = %v", captured.body["controls_location"])
	}
	if captured.body["controlled_asset"] != "a1b2c3d4-0000-4000-8000-000000000000" {
		t.Errorf("controlled_asset = %v", captured.body["controlled_asset"])
	}
	if captured.body["manufacturer"] != "Honeywell" {
		t.Errorf("manufacturer = %v", captured.body["manufacturer"])
	}
	if captured.body["model"] != "T6 Pro" {
		t.Errorf("model = %v", captured.body["model"])
	}
	if captured.body["notes"] != "hallway unit" {
		t.Errorf("notes = %v", captured.body["notes"])
	}
}

// TestCreateThermostat_NullFKs confirms an unset controls_location /
// controlled_asset ride as explicit JSON null (no omitempty) so the backend
// save() can auto-default the conditioned room and a PATCH can clear the asset.
// The text fields still ride as empty strings.
func TestCreateThermostat_NullFKs(t *testing.T) {
	var body map[string]any
	var rawBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		_ = json.Unmarshal(rawBody, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":10,"label":"Shop","location":3}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateThermostat(context.Background(), ThermostatWrite{Label: "Shop", Location: 3}); err != nil {
		t.Fatalf("CreateThermostat: %v", err)
	}
	if v, present := body["controls_location"]; !present || v != nil {
		t.Errorf("controls_location = %v (present=%v), want null present; body=%s", v, present, rawBody)
	}
	if v, present := body["controlled_asset"]; !present || v != nil {
		t.Errorf("controlled_asset = %v (present=%v), want null present; body=%s", v, present, rawBody)
	}
	if body["manufacturer"] != "" || body["model"] != "" || body["notes"] != "" {
		t.Errorf("text fields should be empty strings, got mfr=%v model=%v notes=%v",
			body["manufacturer"], body["model"], body["notes"])
	}
}

// TestUpdateThermostat_Contract confirms edits go out as PATCH to the detail
// URL keyed by the integer pk.
func TestUpdateThermostat_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":9,"label":"Front office","location":3}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateThermostat(context.Background(), 9, ThermostatWrite{Label: "Front office", Location: 3}); err != nil {
		t.Fatalf("UpdateThermostat: %v", err)
	}
	if method != http.MethodPatch || path != "/api/climate/thermostats/9/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}

// TestDeleteThermostat_Contract confirms DELETE to the detail URL with a 204.
func TestDeleteThermostat_Contract(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteThermostat(context.Background(), 9); err != nil {
		t.Fatalf("DeleteThermostat: %v", err)
	}
	if method != http.MethodDelete || path != "/api/climate/thermostats/9/" {
		t.Fatalf("method/path = %q %q", method, path)
	}
}
