package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestCategoryForm_BuildPayload walks the happy path: name/description/color and
// the parent pick map onto a CategoryWrite.
func TestCategoryForm_BuildPayload(t *testing.T) {
	s := NewCategoryFormScreen(Deps{}, "")
	s.inputs[cfName].SetValue("Resistors")
	s.inputs[cfDescription].SetValue("through-hole")
	s.inputs[cfColor].SetValue("#FF5733")
	p := 3
	s.parentID = &p

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Resistors" {
		t.Errorf("name = %q", w.Name)
	}
	if w.Description != "through-hole" {
		t.Errorf("description = %q", w.Description)
	}
	if w.Color != "#FF5733" {
		t.Errorf("color = %q", w.Color)
	}
	if w.Parent == nil || *w.Parent != 3 {
		t.Errorf("parent = %v", w.Parent)
	}
}

// TestCategoryForm_Validation covers name-required and hex-color validation.
func TestCategoryForm_Validation(t *testing.T) {
	s := NewCategoryFormScreen(Deps{}, "")

	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[cfName].SetValue("Caps")

	// empty color is fine
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("empty color should validate: %v", err)
	}
	// bad color rejected
	for _, bad := range []string{"red", "#12", "#GGGGGG", "FF5733"} {
		s.inputs[cfColor].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("expected error for bad color %q", bad)
		}
	}
	// good colors accepted
	for _, good := range []string{"#fff", "#FF5733", "#abc123"} {
		s.inputs[cfColor].SetValue(good)
		if _, err := s.buildPayload(); err != nil {
			t.Errorf("color %q should validate: %v", good, err)
		}
	}
}

// TestCategoryForm_Hydrate fills text + parent from a fetched category.
func TestCategoryForm_Hydrate(t *testing.T) {
	s := NewCategoryFormScreen(Deps{}, "7")
	p := 2
	s.cat = &omsapi.Category{ID: 7, Name: "Resistors", Description: "d", Color: "#010203", Parent: &p, Slug: "resistors"}
	s.hydrate()
	if s.inputs[cfName].Value() != "Resistors" {
		t.Errorf("name = %q", s.inputs[cfName].Value())
	}
	if s.inputs[cfColor].Value() != "#010203" {
		t.Errorf("color = %q", s.inputs[cfColor].Value())
	}
	if s.parentID == nil || *s.parentID != 2 {
		t.Errorf("parentID = %v", s.parentID)
	}
}

// TestCategoryForm_ParentExcludesSelf confirms the parent picker never offers
// the category being edited as its own parent.
func TestCategoryForm_ParentExcludesSelf(t *testing.T) {
	s := NewCategoryFormScreen(Deps{}, "7")
	s.categories = []omsapi.Category{{ID: 7, Name: "Self"}, {ID: 8, Name: "Other"}}
	s.applyParentFilter()
	for _, o := range s.pickOptions {
		if !o.clear && o.id == 7 {
			t.Errorf("parent picker must exclude the category itself (id 7)")
		}
	}
}

// TestCategoryForm_RenderSmoke guards the form + parent-pick render paths.
func TestCategoryForm_RenderSmoke(t *testing.T) {
	s := NewCategoryFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Name") {
		t.Errorf("form view missing Name: %q", out)
	}
	s.openParentPicker()
	if out := s.View(); !strings.Contains(out, "none") {
		t.Errorf("parent picker missing (none) row: %q", out)
	}
}

// TestCategoryList_DeleteConfirm confirms x arms the confirmation (raw input on)
// and n cancels it (raw input off).
func TestCategoryList_DeleteConfirm(t *testing.T) {
	s := NewCategoryListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Category{{ID: 1, Name: "A"}}
	if s.WantsRawInput() {
		t.Fatalf("should not want raw input before confirming")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Errorf("x should arm delete confirmation + raw input")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingDelete || s.WantsRawInput() {
		t.Errorf("n should cancel the confirmation")
	}
}
