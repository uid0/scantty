package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func tstrptr(s string) *string { return &s }

// TestThermostatForm_BuildPayload walks the happy path: the full writable set
// rides, and the two nullable FKs map to their pointer fields.
func TestThermostatForm_BuildPayload(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "")
	s.inputs[tfLabel].SetValue("Wood shop north")
	loc := 3
	s.locationID = &loc
	cond := 4
	s.controlsLocationID = &cond
	s.controlledAssetID = tstrptr("a1b2c3d4-0000-4000-8000-000000000000")
	s.inputs[tfManufacturer].SetValue("Honeywell")
	s.inputs[tfModel].SetValue("T6 Pro")
	s.inputs[tfNotes].SetValue("hallway unit")

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Label != "Wood shop north" {
		t.Errorf("label = %q", w.Label)
	}
	if w.Location != 3 {
		t.Errorf("location = %d, want 3", w.Location)
	}
	if w.ControlsLocation == nil || *w.ControlsLocation != 4 {
		t.Errorf("controls_location = %v", w.ControlsLocation)
	}
	if w.ControlledAsset == nil || *w.ControlledAsset != "a1b2c3d4-0000-4000-8000-000000000000" {
		t.Errorf("controlled_asset = %v", w.ControlledAsset)
	}
	if w.Manufacturer != "Honeywell" || w.Model != "T6 Pro" || w.Notes != "hallway unit" {
		t.Errorf("mfr/model/notes = %q/%q/%q", w.Manufacturer, w.Model, w.Notes)
	}
}

// TestThermostatForm_Validation covers the two required-field guards: label and
// the mounted location.
func TestThermostatForm_Validation(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "")
	// No label, no location.
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty label")
	}
	s.inputs[tfLabel].SetValue("Shop")
	// Label present but location still unset.
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for missing mounted location")
	}
	loc := 2
	s.locationID = &loc
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	// Unset optional FKs ride as nil (→ JSON null), text fields as empty.
	if w.ControlsLocation != nil {
		t.Errorf("controls_location should be nil when unset, got %v", w.ControlsLocation)
	}
	if w.ControlledAsset != nil {
		t.Errorf("controlled_asset should be nil when unset, got %v", w.ControlledAsset)
	}
	if w.Manufacturer != "" || w.Model != "" || w.Notes != "" {
		t.Errorf("text fields should be empty, got %q/%q/%q", w.Manufacturer, w.Model, w.Notes)
	}
}

// TestThermostatForm_Hydrate fills text + all three FK pointers from a fetched
// thermostat, copying (not aliasing) the read struct's pointers.
func TestThermostatForm_Hydrate(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "12")
	loc, cond := 3, 4
	s.therm = &omsapi.Thermostat{
		ID:               12,
		Label:            "Wood shop north",
		Location:         &loc,
		ControlsLocation: &cond,
		ControlledAsset:  tstrptr("a1b2c3d4-0000-4000-8000-000000000000"),
		Manufacturer:     "Honeywell",
		Model:            "T6 Pro",
		Notes:            "n",
	}
	s.hydrate()
	if s.inputs[tfLabel].Value() != "Wood shop north" {
		t.Errorf("label = %q", s.inputs[tfLabel].Value())
	}
	if s.locationID == nil || *s.locationID != 3 {
		t.Errorf("locationID = %v", s.locationID)
	}
	if s.controlsLocationID == nil || *s.controlsLocationID != 4 {
		t.Errorf("controlsLocationID = %v", s.controlsLocationID)
	}
	if s.controlledAssetID == nil || *s.controlledAssetID != "a1b2c3d4-0000-4000-8000-000000000000" {
		t.Errorf("controlledAssetID = %v", s.controlledAssetID)
	}
	if s.inputs[tfManufacturer].Value() != "Honeywell" || s.inputs[tfModel].Value() != "T6 Pro" {
		t.Errorf("mfr/model not hydrated")
	}
	// Mutating the hydrated pointer must not disturb the source record.
	*s.locationID = 99
	if *s.therm.Location != 3 {
		t.Errorf("hydrate aliased the source Location pointer")
	}
}

// TestThermostatForm_RequiredLocationHasNoClearRow confirms the mount-location
// picker never offers a "(none)" row (it is required), while the optional FKs
// do — a clear row on a required FK is the silent-clear footgun prior beads hit.
func TestThermostatForm_RequiredLocationHasNoClearRow(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "")
	s.locations = []omsapi.Location{{ID: 1, Name: "Hall"}, {ID: 2, Name: "Shop"}}

	s.openPicker(tfLocation)
	for _, o := range s.pickOptions {
		if o.clear {
			t.Fatalf("required mount-location picker must not offer a clear row")
		}
	}
	if len(s.pickOptions) != 2 {
		t.Errorf("expected 2 location options, got %d", len(s.pickOptions))
	}

	s.openPicker(tfControlsLocation)
	if len(s.pickOptions) == 0 || !s.pickOptions[0].clear {
		t.Errorf("conditions-room picker should lead with a clear row")
	}

	s.assets = []omsapi.Asset{{ID: "uuid-1", Name: "RTU-1"}}
	s.openPicker(tfControlledAsset)
	if len(s.pickOptions) == 0 || !s.pickOptions[0].clear {
		t.Errorf("controlled-asset picker should lead with a clear row")
	}
}

// TestThermostatForm_PickAndClearAsset drives the picker commit path for the
// UUID asset FK: select sets the id, then the clear row nils it.
func TestThermostatForm_PickAndClearAsset(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "")
	s.assets = []omsapi.Asset{{ID: "uuid-rtu", Name: "RTU-1", AssetTag: "AST-1"}}

	s.openPicker(tfControlledAsset)
	// Options: [ (none), RTU-1 ] — move to the asset and commit.
	s.pickCursor = 1
	s.commitPick()
	if s.controlledAssetID == nil || *s.controlledAssetID != "uuid-rtu" {
		t.Fatalf("controlledAssetID = %v, want uuid-rtu", s.controlledAssetID)
	}
	if s.phase != thermostatPhaseForm {
		t.Errorf("commit should return to form phase")
	}

	s.openPicker(tfControlledAsset)
	s.pickCursor = 0 // the (none) clear row
	s.commitPick()
	if s.controlledAssetID != nil {
		t.Errorf("clear row should nil the asset, got %v", s.controlledAssetID)
	}
}

// TestThermostatForm_RenderSmoke guards the form + picker render paths.
func TestThermostatForm_RenderSmoke(t *testing.T) {
	s := NewThermostatFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Mounted at") {
		t.Errorf("form view missing Mounted at field: %q", out)
	}
	s.openPicker(tfControlsLocation)
	if out := s.View(); !strings.Contains(out, "none") {
		t.Errorf("conditions-room picker missing (none) row: %q", out)
	}
}

// TestThermostatList_DeleteConfirm confirms x arms + esc cancels the confirm,
// and that WantsRawInput flips so the modal owns the n/y keystrokes.
func TestThermostatList_DeleteConfirm(t *testing.T) {
	s := NewThermostatListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Thermostat{{ID: 1, Label: "Shop"}}
	if s.WantsRawInput() {
		t.Errorf("list should not want raw input before arming")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Errorf("x should arm delete confirmation and flip raw input")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.confirmingDelete {
		t.Errorf("esc should cancel the confirmation")
	}
}

// TestThermostatList_RenderRow surfaces the conditioned-room + asset + review
// metadata the row builder derives.
func TestThermostatList_RenderRow(t *testing.T) {
	s := NewThermostatListScreen(Deps{})
	s.loading = false
	s.terminalHeight = 30
	room := "Wood shop"
	asset := "RTU-1"
	s.rows = []omsapi.Thermostat{{
		ID: 1, Label: "North", ControlsLocationName: &room, ControlledAssetName: &asset, NeedsReview: true,
	}}
	out := s.View()
	for _, want := range []string{"North", "Wood shop", "RTU-1", "needs review"} {
		if !strings.Contains(out, want) {
			t.Errorf("row view missing %q: %q", want, out)
		}
	}
}
