package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreatePowerPanel_Contract pins the panel create wire contract: POST to the
// panels-crud collection, location as an int pk, needs_review always present,
// and the nullable main_breaker_amperage / install_date / fed_by serialized as
// JSON null (not omitted) when unset so an edit can clear them.
func TestCreatePowerPanel_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":42,"name":"Main","location":3,"location_name":"Shop","phase_configuration":"split","voltage":240,"breaker_count":0}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	panel, err := c.CreatePowerPanel(context.Background(), PowerPanelWrite{
		Location:           3,
		Name:               "Main",
		PhaseConfiguration: "split",
		Voltage:            240,
		BreakerType:        "SQUARE_D_QO",
		NumberingDirection: "top_down",
		NeedsReview:        false,
	})
	if err != nil {
		t.Fatalf("CreatePowerPanel: %v", err)
	}
	if panel == nil || panel.ID != 42 {
		t.Fatalf("unexpected panel: %+v", panel)
	}
	if captured.method != http.MethodPost || captured.path != "/api/electrical/panels-crud/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["location"].(float64) != 3 {
		t.Errorf("location = %v (%T), want int 3", captured.body["location"], captured.body["location"])
	}
	if captured.body["name"] != "Main" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["phase_configuration"] != "split" {
		t.Errorf("phase_configuration = %v", captured.body["phase_configuration"])
	}
	if captured.body["breaker_type"] != "SQUARE_D_QO" {
		t.Errorf("breaker_type = %v", captured.body["breaker_type"])
	}
	// needs_review bool always present even when false.
	if v, ok := captured.body["needs_review"]; !ok || v != false {
		t.Errorf("needs_review = %v (present=%v), want false present", v, ok)
	}
	// Nullable fields present as JSON null (not omitted) so an edit can clear them.
	for _, k := range []string{"main_breaker_amperage", "install_date", "fed_by"} {
		v, present := captured.body[k]
		if !present || v != nil {
			t.Errorf("%s = %v (present=%v), want explicit null", k, v, present)
		}
	}
}

// TestCreatePowerPanel_NullablesSet confirms the nullable fields ride along with
// real values when set.
func TestCreatePowerPanel_NullablesSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"name":"Sub"}`))
	}))
	defer srv.Close()

	amp := 100
	fed := 55
	date := "2026-07-06"
	c := New(srv.URL)
	if _, err := c.CreatePowerPanel(context.Background(), PowerPanelWrite{
		Location:            3,
		Name:                "Sub",
		PhaseConfiguration:  "three",
		Voltage:             208,
		MainBreakerAmperage: &amp,
		InstallDate:         &date,
		FedBy:               &fed,
	}); err != nil {
		t.Fatalf("CreatePowerPanel: %v", err)
	}
	if body["main_breaker_amperage"].(float64) != 100 {
		t.Errorf("main_breaker_amperage = %v", body["main_breaker_amperage"])
	}
	if body["install_date"] != "2026-07-06" {
		t.Errorf("install_date = %v", body["install_date"])
	}
	if body["fed_by"].(float64) != 55 {
		t.Errorf("fed_by = %v", body["fed_by"])
	}
}

// TestUpdatePowerPanel_Contract pins the edit path + method (PATCH to the panel
// detail URL).
func TestUpdatePowerPanel_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":42,"name":"Renamed"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdatePowerPanel(context.Background(), 42, PowerPanelWrite{
		Location:           3,
		Name:               "Renamed",
		PhaseConfiguration: "split",
		Voltage:            240,
		NumberingDirection: "bottom_up",
		NeedsReview:        true,
	}); err != nil {
		t.Fatalf("UpdatePowerPanel: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/electrical/panels-crud/42/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["name"] != "Renamed" {
		t.Errorf("name = %v", captured.body["name"])
	}
	if captured.body["numbering_direction"] != "bottom_up" {
		t.Errorf("numbering_direction = %v", captured.body["numbering_direction"])
	}
	if captured.body["needs_review"] != true {
		t.Errorf("needs_review = %v, want true", captured.body["needs_review"])
	}
}

// TestDeletePowerPanel_Contract pins the delete path + method and that a 204 with
// no body is treated as success.
func TestDeletePowerPanel_Contract(t *testing.T) {
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
	if err := c.DeletePowerPanel(context.Background(), 42); err != nil {
		t.Fatalf("DeletePowerPanel: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/electrical/panels-crud/42/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestCreatePowerBreaker_Contract pins the breaker create body: pole_count as an
// int (not string), phase sent verbatim (server never derives it), amperage
// present, and the critical pairing carried together.
func TestCreatePowerBreaker_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":9,"panel":42,"position":"12","amperage":20,"phase":"AB","pole_count":2}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	br, err := c.CreatePowerBreaker(context.Background(), PowerBreakerWrite{
		Panel:            42,
		Position:         "12",
		PoleCount:        2,
		Amperage:         20,
		Phase:            "AB",
		Status:           "active",
		ReviewStatus:     "ok",
		NeedsReview:      false,
		IsCritical:       true,
		CriticalCategory: "fire_alarm",
		CriticalNote:     "panel FA",
	})
	if err != nil {
		t.Fatalf("CreatePowerBreaker: %v", err)
	}
	if br == nil || br.ID != 9 {
		t.Fatalf("unexpected breaker: %+v", br)
	}
	if captured.method != http.MethodPost || captured.path != "/api/electrical/breakers-crud/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["panel"].(float64) != 42 {
		t.Errorf("panel = %v", captured.body["panel"])
	}
	if captured.body["position"] != "12" {
		t.Errorf("position = %v (want string \"12\")", captured.body["position"])
	}
	if captured.body["pole_count"].(float64) != 2 {
		t.Errorf("pole_count = %v (%T), want int 2", captured.body["pole_count"], captured.body["pole_count"])
	}
	if captured.body["amperage"].(float64) != 20 {
		t.Errorf("amperage = %v", captured.body["amperage"])
	}
	if captured.body["phase"] != "AB" {
		t.Errorf("phase = %v", captured.body["phase"])
	}
	if captured.body["is_critical"] != true {
		t.Errorf("is_critical = %v", captured.body["is_critical"])
	}
	if captured.body["critical_category"] != "fire_alarm" {
		t.Errorf("critical_category = %v", captured.body["critical_category"])
	}
}

// TestListPowerBreakers_Filter confirms the ?panel= filter is sent and the DRF
// page envelope is unwrapped into the typed slice.
func TestListPowerBreakers_Filter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("panel")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":9,"panel":42,"panel_name":"Main","position":"12","amperage":20,"phase":"A","pole_count":1,"circuit_count":3}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	breakers, err := c.ListPowerBreakers(context.Background(), 42)
	if err != nil {
		t.Fatalf("ListPowerBreakers: %v", err)
	}
	if gotQuery != "42" {
		t.Errorf("panel query = %q, want 42", gotQuery)
	}
	if len(breakers) != 1 || breakers[0].ID != 9 {
		t.Fatalf("breakers = %+v", breakers)
	}
	if breakers[0].Panel != 42 || breakers[0].PanelName != "Main" {
		t.Errorf("panel decode: %+v", breakers[0])
	}
	if breakers[0].CircuitCount != 3 {
		t.Errorf("circuit_count = %d", breakers[0].CircuitCount)
	}
}

// TestCreatePowerCircuit_OmitsMaxLoad confirms max_load_amps is omitted (so the
// backend applies its 80% derate) while the nullable conductor_length_ft rides
// as an explicit value, and the breaker fk is an int.
func TestCreatePowerCircuit_OmitsMaxLoad(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":5,"breaker":9,"max_load_amps":16}`))
	}))
	defer srv.Close()

	length := 40
	c := New(srv.URL)
	ck, err := c.CreatePowerCircuit(context.Background(), PowerCircuitWrite{
		Breaker:           9,
		Label:             "north wall",
		ConductorSize:     "12 AWG",
		ConductorLengthFt: &length,
		NeedsReview:       false,
	})
	if err != nil {
		t.Fatalf("CreatePowerCircuit: %v", err)
	}
	if ck == nil || ck.MaxLoadAmps == nil || *ck.MaxLoadAmps != 16 {
		t.Fatalf("unexpected circuit (max_load derate should echo back): %+v", ck)
	}
	if captured.method != http.MethodPost || captured.path != "/api/electrical/circuits-crud/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["breaker"].(float64) != 9 {
		t.Errorf("breaker = %v", captured.body["breaker"])
	}
	if captured.body["conductor_length_ft"].(float64) != 40 {
		t.Errorf("conductor_length_ft = %v", captured.body["conductor_length_ft"])
	}
	// max_load_amps must NOT be present — the form never prompts it.
	if _, present := captured.body["max_load_amps"]; present {
		t.Errorf("max_load_amps should be omitted, got %v", captured.body["max_load_amps"])
	}
}

// TestPowerCircuitDetail_Decode covers the read struct round-trip, especially the
// nullable pointers and the computed panel_id / breaker_label convenience fields.
func TestPowerCircuitDetail_Decode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":5,"breaker":9,"breaker_label":"Main / pos 12","panel_id":42,"panel_name":"Main",
			"label":"north wall","conductor_size":"12 AWG","conductor_length_ft":null,
			"max_load_amps":16,"needs_review":true,"outlet_count":4,
			"created_at":"2026-07-06T09:00:00Z","updated_at":"2026-07-06T09:00:00Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	ck, err := c.GetPowerCircuit(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetPowerCircuit: %v", err)
	}
	if ck.Breaker != 9 || ck.PanelID != 42 || ck.PanelName != "Main" {
		t.Errorf("fk/convenience decode: %+v", ck)
	}
	if ck.BreakerLabel != "Main / pos 12" {
		t.Errorf("breaker_label = %q", ck.BreakerLabel)
	}
	if ck.ConductorLengthFt != nil {
		t.Errorf("conductor_length_ft should be nil for null, got %v", *ck.ConductorLengthFt)
	}
	if ck.MaxLoadAmps == nil || *ck.MaxLoadAmps != 16 {
		t.Errorf("max_load_amps = %v", ck.MaxLoadAmps)
	}
	if !ck.NeedsReview || ck.OutletCount != 4 {
		t.Errorf("needs_review/outlet_count decode: %+v", ck)
	}
}
