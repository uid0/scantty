package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// fkTestClient builds a ForgeKey API client pointed at a test server.
func fkTestClient(t *testing.T, baseURL string) *forgekeyapi.Client {
	t.Helper()
	c, err := forgekeyapi.New(forgekeyapi.Options{BaseURL: baseURL})
	if err != nil {
		t.Fatalf("forgekeyapi.New: %v", err)
	}
	return c
}

const fwRolloutJSON = `{
	"id":"roll-1","firmware_version":"fv-uuid","firmware_version_string":"1.4.2",
	"device_type_name":"Sensor","name":"canary","status":"draft",
	"batch_size_percent":20,"interval_minutes":60,
	"progress":{"total":10,"on_target":2,"pending":1,"in_progress":0,"failed":0,"remaining":7}
}`

// loadFirmware builds a firmware screen with versions + rollouts already loaded.
func loadFirmware(t *testing.T, deps Deps, versions []forgekeyapi.FirmwareVersion, rollouts []forgekeyapi.FirmwareRollout) *FirmwareScreen {
	t.Helper()
	s := NewFirmwareScreen(deps)
	next, _ := s.Update(firmwareLoadedMsg{versions: versions, rollouts: rollouts})
	return next.(*FirmwareScreen)
}

// TestFirmwareHandlesKeyOnlyWithRollouts pins the sc-k7p claim: the 's'/'a'
// action keys are only taken from the global layer when a rollout list is
// present, so an empty firmware screen still routes 's'→Settings / 'a'→Authz.
func TestFirmwareHandlesKeyOnlyWithRollouts(t *testing.T) {
	empty := loadFirmware(t, Deps{}, nil, nil)
	if empty.HandlesKey("s") || empty.HandlesKey("a") {
		t.Fatalf("empty firmware screen must not claim s/a (they belong to the global layer)")
	}

	withRollouts := loadFirmware(t, Deps{}, nil, []forgekeyapi.FirmwareRollout{{ID: "roll-1", Status: "draft"}})
	if !withRollouts.HandlesKey("s") || !withRollouts.HandlesKey("a") {
		t.Fatalf("expected s/a to be claimed while rollouts are present")
	}
	// The non-colliding action keys reach the screen via fallthrough, so they
	// must NOT be claimed (claiming them would be redundant and could mask bugs).
	for _, k := range []string{"c", "p", "x", "j", "k", "r"} {
		if withRollouts.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false (reaches screen via fallthrough)", k)
		}
	}
}

// TestFirmwareCreateExcludesEpaperVersions verifies the New-Rollout version
// picker offers only active, non-ePaper versions — ePaper firmware takes the
// parallel HTTPS-pull rollout pipeline, so an MQTT rollout for it would target
// the wrong fleet.
func TestFirmwareCreateExcludesEpaperVersions(t *testing.T) {
	versions := []forgekeyapi.FirmwareVersion{
		{ID: "fv-1", Version: "1.4.2", IsActive: true, DeviceTypeCode: "temperature_sensor"},
		{ID: "fv-2", Version: "2.0.0", IsActive: true, DeviceTypeCode: "epaper_screen"},
		{ID: "fv-3", Version: "0.9.0", IsActive: false, DeviceTypeCode: "temperature_sensor"},
	}
	s := loadFirmware(t, Deps{}, versions, nil)
	next, _ := s.Update(woRuneKey("c"))
	s = next.(*FirmwareScreen)
	if s.mode != fwModeCreate {
		t.Fatalf("mode = %v, want create", s.mode)
	}
	if len(s.cvOptions) != 1 || s.cvOptions[0].Version != "1.4.2" {
		t.Fatalf("cvOptions = %+v, want only the active non-ePaper version", s.cvOptions)
	}
}

// TestFirmwareCreateRolloutFlow drives the whole create flow — open form, pick a
// version, submit — and verifies the POST body carries the form fields with the
// form defaults.
func TestFirmwareCreateRolloutFlow(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fwRolloutJSON))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	versions := []forgekeyapi.FirmwareVersion{{ID: "fv-uuid", Version: "1.4.2", IsActive: true, DeviceTypeCode: "temperature_sensor"}}
	s := loadFirmware(t, deps, versions, nil)

	// c → open form (raw input so the global layer stops eating keys).
	next, _ := s.Update(woRuneKey("c"))
	s = next.(*FirmwareScreen)
	if s.mode != fwModeCreate || !s.WantsRawInput() {
		t.Fatalf("expected create mode with raw input, mode=%v raw=%v", s.mode, s.WantsRawInput())
	}

	// enter on the version field → picker; enter again → select + advance to batch.
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*FirmwareScreen)
	if s.mode != fwModePickVersion {
		t.Fatalf("mode = %v, want pick-version", s.mode)
	}
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*FirmwareScreen)
	if s.mode != fwModeCreate || s.cvVersion == nil {
		t.Fatalf("expected version picked and back in create mode, mode=%v version=%v", s.mode, s.cvVersion)
	}
	if s.cvFocus != 1 {
		t.Fatalf("focus = %d, want 1 (batch) after picking a version", s.cvFocus)
	}

	// enter on the batch field → submit (defaults: batch 20, interval 60).
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*FirmwareScreen)
	if cmd == nil {
		t.Fatalf("expected a create cmd")
	}
	if _, ok := cmd().(fwRolloutCreatedMsg); !ok {
		t.Fatalf("wrong create msg type")
	}

	if gotMethod != http.MethodPost || gotPath != "/api/forgekey/firmware-rollouts/" {
		t.Fatalf("%s %s, want POST /api/forgekey/firmware-rollouts/", gotMethod, gotPath)
	}
	if gotBody["firmware_version"] != "fv-uuid" {
		t.Errorf("firmware_version = %v, want fv-uuid", gotBody["firmware_version"])
	}
	if gotBody["batch_size_percent"] != float64(20) || gotBody["interval_minutes"] != float64(60) {
		t.Errorf("batch/interval = %v/%v, want 20/60", gotBody["batch_size_percent"], gotBody["interval_minutes"])
	}
}

// TestFirmwareRolloutStartAction verifies 's' on a draft rollout POSTs the start
// transition, and that pausing a draft is refused client-side (status guard).
func TestFirmwareRolloutStartAction(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fwRolloutJSON))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	s := loadFirmware(t, deps, nil, []forgekeyapi.FirmwareRollout{
		{ID: "roll-1", Status: "draft", FirmwareVersionStr: "1.4.2"},
	})

	// Pause is invalid for a draft — the guard flashes a warning and makes no call.
	next, cmd := s.Update(woRuneKey("p"))
	s = next.(*FirmwareScreen)
	if s.actionPending {
		t.Fatalf("pause on a draft must not start an action")
	}
	if cmd != nil {
		if _, ok := cmd().(fkActionResultMsg); ok {
			t.Fatalf("pause on a draft must not fire an action result")
		}
	}
	if gotMethod != "" {
		t.Fatalf("pause on a draft must not hit the server, got %s %s", gotMethod, gotPath)
	}

	// Start is valid for a draft.
	next, cmd = s.Update(woRuneKey("s"))
	s = next.(*FirmwareScreen)
	if cmd == nil {
		t.Fatalf("expected a start cmd")
	}
	if _, ok := cmd().(fkActionResultMsg); !ok {
		t.Fatalf("wrong action msg type")
	}
	if gotMethod != http.MethodPost || gotPath != "/api/forgekey/firmware-rollouts/roll-1/start/" {
		t.Fatalf("%s %s, want POST .../roll-1/start/", gotMethod, gotPath)
	}
}
