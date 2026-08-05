package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestProjectStorageForm_RenderSmoke exercises the render path to guard against
// index panics in the windowed renderer and confirms the required marker shows.
func TestProjectStorageForm_RenderSmoke(t *testing.T) {
	s := NewProjectStorageFormScreen(Deps{})
	s.terminalHeight = 30

	out := s.View()
	if !strings.Contains(out, "Username") {
		t.Errorf("form view missing Username field: %q", out)
	}
	// The "*" became a HINT after the input area: a columnar form marks what is
	// required rather than hanging a marker off the shared label column, which
	// would widen it for every row (sc-6qsk).
	if !strings.Contains(out, "Username ..... ") {
		t.Errorf("form view should render Username as a columnar row: %q", out)
	}
	if !strings.Contains(out, "required") {
		t.Errorf("form view should still mark username required: %q", out)
	}
	if !strings.Contains(out, "Storage location") {
		t.Errorf("form view missing Storage location field: %q", out)
	}
}

// TestProjectStorageForm_BuildPayload walks the happy path: values are trimmed,
// username is carried, and the blank optionals stay empty (omitempty drops them
// on the wire — asserted in the omsapi test).
func TestProjectStorageForm_BuildPayload(t *testing.T) {
	s := NewProjectStorageFormScreen(Deps{})
	s.inputs[psfUsername].SetValue("  alice  ")
	s.inputs[psfProjectTitle].SetValue("CNC jig")
	s.inputs[psfStorageLocation].SetValue("Rack B3")

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Username != "alice" {
		t.Errorf("username = %q (want trimmed \"alice\")", w.Username)
	}
	if w.ProjectTitle != "CNC jig" {
		t.Errorf("project_title = %q", w.ProjectTitle)
	}
	if w.StorageLocationName != "Rack B3" {
		t.Errorf("storage_location_name = %q", w.StorageLocationName)
	}
	if w.FirstName != "" || w.LastName != "" || w.Email != "" {
		t.Errorf("blank optionals should stay empty, got %+v", w)
	}
}

// TestProjectStorageForm_Validation covers username-required and email format.
func TestProjectStorageForm_Validation(t *testing.T) {
	s := NewProjectStorageFormScreen(Deps{})

	// username required
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty username")
	}
	// whitespace-only username is still empty after trim
	s.inputs[psfUsername].SetValue("   ")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for whitespace-only username")
	}
	s.inputs[psfUsername].SetValue("alice")

	// a malformed email is rejected client-side for a clean message
	s.inputs[psfEmail].SetValue("not-an-email")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for malformed email")
	}
	// a display-name form is rejected — we want a bare address
	s.inputs[psfEmail].SetValue("Alice <alice@example.com>")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for a non-bare email address")
	}
	// a valid bare address passes
	s.inputs[psfEmail].SetValue("alice@example.com")
	if w, err := s.buildPayload(); err != nil {
		t.Errorf("valid email should pass: %v", err)
	} else if w.Email != "alice@example.com" {
		t.Errorf("email = %q", w.Email)
	}
	// email is optional — clearing it is fine
	s.inputs[psfEmail].SetValue("")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("empty email should be allowed: %v", err)
	}
}

// TestProjectStorageForm_Nav confirms tab/↑↓ move the cursor and wrap, and that
// focus follows the cursor.
func TestProjectStorageForm_Nav(t *testing.T) {
	s := NewProjectStorageFormScreen(Deps{})
	if id, _ := s.currentFieldID(); id != psfUsername {
		t.Fatalf("cursor should start on username, got %d", id)
	}
	if !s.inputs[psfUsername].Focused() {
		t.Errorf("username input should be focused at start")
	}

	// shift+tab from the first field wraps to the last (storage location).
	next, _ := s.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	s = next.(*ProjectStorageFormScreen)
	if id, _ := s.currentFieldID(); id != psfStorageLocation {
		t.Errorf("shift+tab from first field should wrap to last, got %d", id)
	}
	if !s.inputs[psfStorageLocation].Focused() || s.inputs[psfUsername].Focused() {
		t.Errorf("focus should have moved to the storage-location input")
	}

	// tab moves forward, wrapping back to username.
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyTab})
	s = next.(*ProjectStorageFormScreen)
	if id, _ := s.currentFieldID(); id != psfUsername {
		t.Errorf("tab from last field should wrap to first, got %d", id)
	}
}

// TestProjectStorageForm_SubmitPostsStart drives enter → the async start call →
// the POST to /stints/start/ with the intake body, and confirms the saved msg
// navigates to the new stint's detail.
func TestProjectStorageForm_SubmitPostsStart(t *testing.T) {
	var captured struct {
		path string
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"stint_id":"PS-NEW01234","username":"alice","display_name":"Alice A","status":"active"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewProjectStorageFormScreen(deps)
	s.inputs[psfUsername].SetValue("alice")

	// enter submits; the returned cmd performs the async POST.
	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*ProjectStorageFormScreen)
	if !s.saving {
		t.Fatalf("expected saving=true after submit")
	}
	if cmd == nil {
		t.Fatalf("expected a submit cmd")
	}
	msg := cmd()
	saved, ok := msg.(projectStorageFormSavedMsg)
	if !ok {
		t.Fatalf("msg = %T, want projectStorageFormSavedMsg", msg)
	}
	if saved.err != nil {
		t.Fatalf("start failed: %v", saved.err)
	}
	if captured.path != "/api/project-storage/stints/start/" {
		t.Fatalf("path = %q, want .../stints/start/", captured.path)
	}
	if captured.body["username"] != "alice" {
		t.Fatalf("body username = %v, want alice", captured.body["username"])
	}

	// Feeding the saved msg back navigates to the new stint's detail.
	next, cmd = s.Update(saved)
	s = next.(*ProjectStorageFormScreen)
	if s.saving {
		t.Errorf("saving should clear after the saved msg")
	}
	if cmd == nil {
		t.Fatalf("expected a status+navigate batch after a successful start")
	}
}

// TestProjectStorageForm_SubmitErrorSurfaces confirms a backend 4xx (e.g. the
// member already has an active stint) surfaces as an error status without a
// crash or false success navigation.
func TestProjectStorageForm_SubmitErrorSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":"active_stint_exists"}`, http.StatusConflict)
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewProjectStorageFormScreen(deps)
	s.inputs[psfUsername].SetValue("alice")

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a submit cmd")
	}
	saved, ok := cmd().(projectStorageFormSavedMsg)
	if !ok {
		t.Fatalf("expected projectStorageFormSavedMsg")
	}
	if saved.err == nil {
		t.Fatalf("expected a conflict error from start")
	}
	next, cmd := s.Update(saved)
	s = next.(*ProjectStorageFormScreen)
	if s.saving {
		t.Errorf("saving should clear on error")
	}
	if s.errMsg == "" {
		t.Errorf("expected errMsg set on failure")
	}
	sm, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("msg = %T, want StatusMsg", cmd())
	}
	if sm.Level != StatusError || !strings.Contains(sm.Text, "intake failed") {
		t.Errorf("status = %+v, want StatusError containing 'intake failed'", sm)
	}
}
