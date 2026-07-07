package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// TestForgeKeyDeviceForm_Hydrate: the edit form seeds locationID from the device
// it is handed, and does not alias the source device's Location pointer.
func TestForgeKeyDeviceForm_Hydrate(t *testing.T) {
	loc := 3
	dev := &forgekeyapi.Device{ID: "dev-1", Name: "Relay A", Location: &loc}
	s := NewForgeKeyDeviceFormScreen(Deps{}, dev)
	if s.locationID == nil || *s.locationID != 3 {
		t.Fatalf("locationID = %v, want 3", s.locationID)
	}
	*s.locationID = 99
	if *dev.Location != 3 {
		t.Errorf("hydrate aliased the source device Location pointer")
	}
}

// TestForgeKeyDeviceForm_HydrateUnassigned: a device with no location leaves the
// form unassigned (nil), not zero.
func TestForgeKeyDeviceForm_HydrateUnassigned(t *testing.T) {
	s := NewForgeKeyDeviceFormScreen(Deps{}, &forgekeyapi.Device{ID: "dev-1", Name: "Relay A"})
	if s.locationID != nil {
		t.Errorf("locationID = %v, want nil (unassigned)", s.locationID)
	}
}

// TestForgeKeyDeviceForm_LocsMsgEntersEdit: the async location load flips the
// loading phase into the edit view whether or not it errored (the list is a
// convenience, not a gate on saving the unchanged value).
func TestForgeKeyDeviceForm_LocsMsgEntersEdit(t *testing.T) {
	s := NewForgeKeyDeviceFormScreen(Deps{}, &forgekeyapi.Device{ID: "dev-1"})
	if s.phase != fkDeviceFormLoading {
		t.Fatalf("phase = %v, want loading", s.phase)
	}
	next, _ := s.Update(fkDeviceFormLocsMsg{locations: []omsapi.Location{{ID: 1, Name: "Hall"}}})
	s = next.(*ForgeKeyDeviceFormScreen)
	if s.phase != fkDeviceFormEdit {
		t.Errorf("phase = %v, want edit after locations arrive", s.phase)
	}
	if len(s.locations) != 1 {
		t.Errorf("locations = %d, want 1", len(s.locations))
	}
}

// TestForgeKeyDeviceForm_PickAndClear drives the location picker: it leads with a
// clear row (location is nullable), selecting a row sets the id, and the clear
// row nils it back to unassigned.
func TestForgeKeyDeviceForm_PickAndClear(t *testing.T) {
	s := NewForgeKeyDeviceFormScreen(Deps{}, &forgekeyapi.Device{ID: "dev-1"})
	s.locations = []omsapi.Location{{ID: 1, Name: "Hall"}, {ID: 2, Name: "Shop", Code: "SH"}}

	s.openPicker()
	if len(s.pickOptions) == 0 || !s.pickOptions[0].clear {
		t.Fatalf("location picker should lead with a clear row")
	}
	if len(s.pickOptions) != 3 { // clear + 2 locations
		t.Fatalf("pickOptions = %d, want 3", len(s.pickOptions))
	}

	// Select "Shop" (index 2) and commit.
	s.pickCursor = 2
	s.commitPick()
	if s.locationID == nil || *s.locationID != 2 {
		t.Fatalf("locationID = %v, want 2", s.locationID)
	}
	if s.phase != fkDeviceFormEdit {
		t.Errorf("commit should return to the edit phase")
	}

	// Re-open and pick the clear row → unassigned.
	s.openPicker()
	s.pickCursor = 0
	s.commitPick()
	if s.locationID != nil {
		t.Errorf("clear row should nil the location, got %v", s.locationID)
	}
}

// TestForgeKeyDeviceForm_SubmitPatchesLocation drives an edit → save and asserts
// the PATCH carries the picked location to the device-update endpoint.
func TestForgeKeyDeviceForm_SubmitPatchesLocation(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1","name":"Relay A","location":2}`))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	s := NewForgeKeyDeviceFormScreen(deps, &forgekeyapi.Device{ID: "dev-1", Name: "Relay A"})
	s.phase = fkDeviceFormEdit
	loc := 2
	s.locationID = &loc

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*ForgeKeyDeviceFormScreen)
	if cmd == nil {
		t.Fatalf("enter should return a save cmd")
	}
	if _, ok := cmd().(fkDeviceFormSavedMsg); !ok {
		t.Fatalf("save cmd should produce fkDeviceFormSavedMsg")
	}
	if gotMethod != http.MethodPatch || gotPath != "/api/forgekey/devices/dev-1/" {
		t.Fatalf("%s %s, want PATCH /api/forgekey/devices/dev-1/", gotMethod, gotPath)
	}
	if gotBody["location"] != float64(2) {
		t.Errorf("location = %v, want 2", gotBody["location"])
	}
}

// TestForgeKeyDeviceForm_SubmitClearsLocation: saving an unassigned form sends an
// explicit null so the backend clears the FK.
func TestForgeKeyDeviceForm_SubmitClearsLocation(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"dev-1","name":"Relay A"}`))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	s := NewForgeKeyDeviceFormScreen(deps, &forgekeyapi.Device{ID: "dev-1", Name: "Relay A"})
	s.phase = fkDeviceFormEdit
	s.locationID = nil // unassigned

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter should return a save cmd")
	}
	cmd()
	if _, present := gotBody["location"]; !present {
		t.Errorf("location key must be present as explicit null, body = %v", gotBody)
	}
	if gotBody["location"] != nil {
		t.Errorf("location = %v, want null", gotBody["location"])
	}
}

// TestForgeKeyDeviceForm_SubmitError surfaces a backend 4xx as an on-screen error
// rather than crashing or navigating away.
func TestForgeKeyDeviceForm_SubmitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"location":["Invalid pk."]}`))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	s := NewForgeKeyDeviceFormScreen(deps, &forgekeyapi.Device{ID: "dev-1", Name: "Relay A"})
	s.phase = fkDeviceFormEdit
	loc := 999
	s.locationID = &loc

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter should return a save cmd")
	}
	msg := cmd()
	saved, ok := msg.(fkDeviceFormSavedMsg)
	if !ok || saved.err == nil {
		t.Fatalf("expected a saved msg carrying an error, got %#v", msg)
	}
	next, _ := s.Update(saved)
	s = next.(*ForgeKeyDeviceFormScreen)
	if s.errMsg == "" {
		t.Errorf("form should retain an error message after a failed save")
	}
	if s.saving {
		t.Errorf("saving flag should clear after a failed save")
	}
}

// TestForgeKeyDeviceForm_RenderSmoke guards the edit + picker render paths.
func TestForgeKeyDeviceForm_RenderSmoke(t *testing.T) {
	s := NewForgeKeyDeviceFormScreen(Deps{}, &forgekeyapi.Device{ID: "dev-1", Name: "Relay A"})
	s.phase = fkDeviceFormEdit
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Location") {
		t.Errorf("edit view missing Location field: %q", out)
	}
	s.locations = []omsapi.Location{{ID: 1, Name: "Hall"}}
	s.openPicker()
	if out := s.View(); !strings.Contains(out, "unassigned") {
		t.Errorf("picker missing the (none — unassigned) clear row: %q", out)
	}
}
