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

// ===========================================================================
// PowerOutlet CRUD (Bead B)
// ===========================================================================

// TestCreatePowerOutlet_Contract pins the outlet create body: POST to the
// outlets-crud collection, circuit + location as int pks, the nullable disconnect
// serialized as explicit JSON null when unset, and needs_review always present.
func TestCreatePowerOutlet_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":11,"circuit":5,"location":3,"outlet_type":"5-15R","status":"active"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	out, err := c.CreatePowerOutlet(context.Background(), PowerOutletWrite{
		Circuit:    5,
		Location:   3,
		OutletType: "5-15R",
		Status:     "active",
		Label:      "NW-bench-1",
	})
	if err != nil {
		t.Fatalf("CreatePowerOutlet: %v", err)
	}
	if out == nil || out.ID != 11 {
		t.Fatalf("unexpected outlet: %+v", out)
	}
	if captured.method != http.MethodPost || captured.path != "/api/electrical/outlets-crud/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["circuit"].(float64) != 5 || captured.body["location"].(float64) != 3 {
		t.Errorf("circuit/location = %v/%v", captured.body["circuit"], captured.body["location"])
	}
	if captured.body["outlet_type"] != "5-15R" {
		t.Errorf("outlet_type = %v", captured.body["outlet_type"])
	}
	// nullable disconnect present as explicit null so an edit can clear it.
	if v, present := captured.body["disconnect"]; !present || v != nil {
		t.Errorf("disconnect = %v (present=%v), want explicit null", v, present)
	}
	if v, ok := captured.body["needs_review"]; !ok || v != false {
		t.Errorf("needs_review = %v (present=%v), want false present", v, ok)
	}
}

// TestCreatePowerOutlet_DisconnectSet confirms the disconnect fk rides as an int
// when the operator picks one.
func TestCreatePowerOutlet_DisconnectSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":12}`))
	}))
	defer srv.Close()

	disc := 8
	c := New(srv.URL)
	if _, err := c.CreatePowerOutlet(context.Background(), PowerOutletWrite{
		Circuit:    5,
		Location:   3,
		Disconnect: &disc,
		OutletType: "L6-30R",
		Status:     "active",
	}); err != nil {
		t.Fatalf("CreatePowerOutlet: %v", err)
	}
	if body["disconnect"].(float64) != 8 {
		t.Errorf("disconnect = %v, want 8", body["disconnect"])
	}
}

// TestUpdatePowerOutlet_Contract pins the edit path + PATCH method.
func TestUpdatePowerOutlet_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":11}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdatePowerOutlet(context.Background(), 11, PowerOutletWrite{Circuit: 5, Location: 3, OutletType: "5-15R", Status: "capped"}); err != nil {
		t.Fatalf("UpdatePowerOutlet: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/electrical/outlets-crud/11/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestDeletePowerOutlet_Contract pins the delete path + method.
func TestDeletePowerOutlet_Contract(t *testing.T) {
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
	if err := c.DeletePowerOutlet(context.Background(), 11); err != nil {
		t.Fatalf("DeletePowerOutlet: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/electrical/outlets-crud/11/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestListPowerOutlets_Filter confirms the ?circuit= filter is sent and the DRF
// page envelope is unwrapped.
func TestListPowerOutlets_Filter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("circuit")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":11,"circuit":5,"circuit_label":"north wall","location":3,"location_name":"Shop","disconnect":null,"outlet_type":"5-15R","label":"NW-1","status":"active"}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	outlets, err := c.ListPowerOutlets(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListPowerOutlets: %v", err)
	}
	if gotQuery != "5" {
		t.Errorf("circuit query = %q, want 5", gotQuery)
	}
	if len(outlets) != 1 || outlets[0].ID != 11 || outlets[0].Disconnect != nil {
		t.Fatalf("outlets = %+v", outlets)
	}
	if outlets[0].CircuitLabel != "north wall" || outlets[0].LocationName != "Shop" {
		t.Errorf("label decode: %+v", outlets[0])
	}
}

// TestPowerOutletDetail_Decode covers the read struct round-trip incl. the
// nullable disconnect pointer resolving to a real id.
func TestPowerOutletDetail_Decode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":11,"circuit":5,"circuit_label":"north wall","location":3,"location_name":"Shop",
			"disconnect":8,"disconnect_label":"welder disc","outlet_type":"L6-30R","label":"weld-1",
			"location_description":"east wall","status":"active","notes":"n","needs_review":true,
			"created_at":"2026-07-06T09:00:00Z","updated_at":"2026-07-06T09:00:00Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	o, err := c.GetPowerOutlet(context.Background(), 11)
	if err != nil {
		t.Fatalf("GetPowerOutlet: %v", err)
	}
	if o.Disconnect == nil || *o.Disconnect != 8 || o.DisconnectLabel != "welder disc" {
		t.Errorf("disconnect decode: %+v", o)
	}
	if o.OutletType != "L6-30R" || o.LocationDescription != "east wall" || !o.NeedsReview {
		t.Errorf("field decode: %+v", o)
	}
}

// ===========================================================================
// Disconnect CRUD (Bead B) — incl. the required_loto_device_ids M2M write field
// ===========================================================================

// TestCreateDisconnect_Contract pins the disconnect create body: POST to the
// legacy /electrical-circuits/disconnects/ collection, circuit as an int pk, the
// required_loto_device_ids list serialized as a JSON array, and the nullable
// location + amperage as explicit null when unset.
func TestCreateDisconnect_Contract(t *testing.T) {
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
		_, _ = w.Write([]byte(`{"id":21,"circuit":5,"label":"dust collector disc","disconnect_type":"fused"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	d, err := c.CreateDisconnect(context.Background(), DisconnectWrite{
		Circuit:               5,
		Label:                 "dust collector disc",
		DisconnectType:        "fused",
		FuseSize:              "30A class J",
		IsLockable:            true,
		RequiredLOTODeviceIDs: []int{7, 9},
	})
	if err != nil {
		t.Fatalf("CreateDisconnect: %v", err)
	}
	if d == nil || d.ID != 21 {
		t.Fatalf("unexpected disconnect: %+v", d)
	}
	if captured.method != http.MethodPost || captured.path != "/api/electrical-circuits/disconnects/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
	if captured.body["circuit"].(float64) != 5 {
		t.Errorf("circuit = %v", captured.body["circuit"])
	}
	if captured.body["disconnect_type"] != "fused" || captured.body["label"] != "dust collector disc" {
		t.Errorf("type/label = %v/%v", captured.body["disconnect_type"], captured.body["label"])
	}
	ids, ok := captured.body["required_loto_device_ids"].([]any)
	if !ok || len(ids) != 2 || ids[0].(float64) != 7 || ids[1].(float64) != 9 {
		t.Errorf("required_loto_device_ids = %v (want [7,9] array)", captured.body["required_loto_device_ids"])
	}
	// nullable location + amperage present as explicit null.
	for _, k := range []string{"location", "amperage"} {
		v, present := captured.body[k]
		if !present || v != nil {
			t.Errorf("%s = %v (present=%v), want explicit null", k, v, present)
		}
	}
	if captured.body["is_lockable"] != true {
		t.Errorf("is_lockable = %v, want true", captured.body["is_lockable"])
	}
}

// TestCreateDisconnect_NullablesSet confirms location + amperage ride as real
// values when set.
func TestCreateDisconnect_NullablesSet(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":22}`))
	}))
	defer srv.Close()

	loc := 4
	amp := 60
	c := New(srv.URL)
	if _, err := c.CreateDisconnect(context.Background(), DisconnectWrite{
		Circuit:        5,
		Location:       &loc,
		Amperage:       &amp,
		Label:          "RTU disc",
		DisconnectType: "unfused",
	}); err != nil {
		t.Fatalf("CreateDisconnect: %v", err)
	}
	if body["location"].(float64) != 4 || body["amperage"].(float64) != 60 {
		t.Errorf("location/amperage = %v/%v", body["location"], body["amperage"])
	}
}

// TestDisconnectWrite_NilLOTONormalized confirms a nil id slice is normalized to
// an empty JSON array (never null) so the many=True serializer accepts it.
func TestDisconnectWrite_NilLOTONormalized(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":23}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	// No RequiredLOTODeviceIDs set → nil slice.
	if _, err := c.CreateDisconnect(context.Background(), DisconnectWrite{Circuit: 5, Label: "d", DisconnectType: "none"}); err != nil {
		t.Fatalf("CreateDisconnect: %v", err)
	}
	ids, ok := body["required_loto_device_ids"].([]any)
	if !ok || len(ids) != 0 {
		t.Errorf("required_loto_device_ids = %v (present type %T), want empty [] array", body["required_loto_device_ids"], body["required_loto_device_ids"])
	}
}

// TestUpdateDisconnect_Contract pins the edit path + PATCH method.
func TestUpdateDisconnect_Contract(t *testing.T) {
	var captured struct {
		method string
		path   string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":21}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.UpdateDisconnect(context.Background(), 21, DisconnectWrite{Circuit: 5, Label: "d", DisconnectType: "toggle", RequiredLOTODeviceIDs: []int{}}); err != nil {
		t.Fatalf("UpdateDisconnect: %v", err)
	}
	if captured.method != http.MethodPatch || captured.path != "/api/electrical-circuits/disconnects/21/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestDeleteDisconnect_Contract pins the delete path + method.
func TestDeleteDisconnect_Contract(t *testing.T) {
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
	if err := c.DeleteDisconnect(context.Background(), 21); err != nil {
		t.Fatalf("DeleteDisconnect: %v", err)
	}
	if captured.method != http.MethodDelete || captured.path != "/api/electrical-circuits/disconnects/21/" {
		t.Fatalf("method/path = %q %q", captured.method, captured.path)
	}
}

// TestListDisconnects_Filter confirms the ?circuit= filter and the page unwrap.
func TestListDisconnects_Filter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("circuit")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":21,"circuit":5,"circuit_label":"north wall","panel_name":"Main","breaker_position":"12","location":null,"label":"dc disc","disconnect_type":"fused","amperage":null,"is_lockable":true,"required_loto_devices":[],"needs_review":false}]}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	discs, err := c.ListDisconnects(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListDisconnects: %v", err)
	}
	if gotQuery != "5" {
		t.Errorf("circuit query = %q, want 5", gotQuery)
	}
	if len(discs) != 1 || discs[0].ID != 21 || discs[0].Location != nil || discs[0].Amperage != nil {
		t.Fatalf("discs = %+v", discs)
	}
	if discs[0].PanelName != "Main" || discs[0].BreakerPosition != "12" {
		t.Errorf("denorm decode: %+v", discs[0])
	}
}

// TestDisconnectDetail_Decode covers the read round-trip incl. the nullable
// location/amperage pointers and the embedded required_loto_devices objects.
func TestDisconnectDetail_Decode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":21,"circuit":5,"circuit_label":"north wall","panel_name":"Main","breaker_position":"12",
			"location":4,"location_name":"Shop","label":"dc disc","disconnect_type":"fused","amperage":60,
			"fuse_size":"30A","is_lockable":true,"notes":"n",
			"required_loto_devices":[{"id":7,"device_type":"breaker_lock","device_type_display":"Breaker lock","label":"BL-1","status":"available"}],
			"needs_review":false,"created_at":"2026-07-06T09:00:00Z","updated_at":"2026-07-06T09:00:00Z"}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	d, err := c.GetDisconnect(context.Background(), 21)
	if err != nil {
		t.Fatalf("GetDisconnect: %v", err)
	}
	if d.Location == nil || *d.Location != 4 || d.Amperage == nil || *d.Amperage != 60 {
		t.Errorf("nullable decode: %+v", d)
	}
	if len(d.RequiredLOTODevices) != 1 || d.RequiredLOTODevices[0].Label != "BL-1" {
		t.Errorf("required_loto_devices decode: %+v", d.RequiredLOTODevices)
	}
	if id, ok := d.RequiredLOTODevices[0].IntID(); !ok || id != 7 {
		t.Errorf("loto device id coercion = %v/%v, want 7", id, ok)
	}
}
