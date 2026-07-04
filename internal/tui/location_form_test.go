package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestLocationForm_BuildPayload walks the happy path and confirms the create
// default (is_active true) plus parent mapping.
func TestLocationForm_BuildPayload(t *testing.T) {
	s := NewLocationFormScreen(Deps{}, "")
	s.inputs[lfName].SetValue("Shelf 3B")
	s.inputs[lfDescription].SetValue("rack 3")
	p := 2
	s.parentID = &p

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Shelf 3B" {
		t.Errorf("name = %q", w.Name)
	}
	if w.Description != "rack 3" {
		t.Errorf("description = %q", w.Description)
	}
	if !w.IsActive {
		t.Errorf("is_active create default should be true")
	}
	if w.Parent == nil || *w.Parent != 2 {
		t.Errorf("parent = %v", w.Parent)
	}
}

// TestLocationForm_Validation covers name-required and the is_active toggle.
func TestLocationForm_Validation(t *testing.T) {
	s := NewLocationFormScreen(Deps{}, "")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[lfName].SetValue("Room")
	s.isActive = false
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.IsActive {
		t.Errorf("is_active should be false after toggle off")
	}
}

// TestLocationForm_Hydrate fills text + parent + is_active from a fetched
// location.
func TestLocationForm_Hydrate(t *testing.T) {
	s := NewLocationFormScreen(Deps{}, "4")
	p := 2
	s.loc = &omsapi.Location{ID: 4, Name: "Shelf 3B", Description: "d", Parent: &p, IsActive: false}
	s.hydrate()
	if s.inputs[lfName].Value() != "Shelf 3B" {
		t.Errorf("name = %q", s.inputs[lfName].Value())
	}
	if s.parentID == nil || *s.parentID != 2 {
		t.Errorf("parentID = %v", s.parentID)
	}
	if s.isActive {
		t.Errorf("isActive should hydrate to false")
	}
}

// TestLocationForm_RenderSmoke guards the form + parent-pick render paths.
func TestLocationForm_RenderSmoke(t *testing.T) {
	s := NewLocationFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Active") {
		t.Errorf("form view missing Active toggle: %q", out)
	}
	s.openParentPicker()
	if out := s.View(); !strings.Contains(out, "none") {
		t.Errorf("parent picker missing (none) row: %q", out)
	}
}

// TestLocationList_DeleteConfirm confirms x arms + n cancels the confirmation.
func TestLocationList_DeleteConfirm(t *testing.T) {
	s := NewLocationListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Location{{ID: 1, Name: "A"}}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Errorf("x should arm delete confirmation")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.confirmingDelete {
		t.Errorf("esc should cancel the confirmation")
	}
}
