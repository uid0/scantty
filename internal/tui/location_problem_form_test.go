package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestLocationProblemForm_DefaultSeverityMedium pins the web modal's default
// severity (medium) on a fresh report form.
func TestLocationProblemForm_DefaultSeverityMedium(t *testing.T) {
	s := NewLocationProblemFormScreen(Deps{}, 42, "Bay 3")
	if got := locationProblemSeverityOptions[s.severityIdx].value; got != omsapi.LocationProblemSeverityMedium {
		t.Errorf("default severity = %q, want medium", got)
	}
}

// TestLocationProblemForm_CycleSeverity drives the severity select key.
func TestLocationProblemForm_CycleSeverity(t *testing.T) {
	s := NewLocationProblemFormScreen(Deps{}, 42, "Bay 3")
	s.cursor = indexOfField(s.fields, lpfSeverity)
	before := s.severityIdx
	s.Update(tea.KeyMsg{Type: tea.KeySpace})
	if s.severityIdx == before {
		t.Errorf("space should cycle severity from %d", before)
	}
	// left wraps back.
	s.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if s.severityIdx != before {
		t.Errorf("left should return to %d, got %d", before, s.severityIdx)
	}
}

// TestLocationProblemForm_RequireDescription rejects an empty description before
// any network round-trip.
func TestLocationProblemForm_RequireDescription(t *testing.T) {
	s := NewLocationProblemFormScreen(Deps{}, 42, "Bay 3")
	if _, err := s.buildReport(); err == nil {
		t.Errorf("blank description should be rejected")
	}
	// submit must not flip to saving on a validation failure.
	s.submit()
	if s.saving {
		t.Errorf("submit should not start saving with a blank description")
	}
	if s.errMsg == "" {
		t.Errorf("submit should set an error message")
	}
}

// TestLocationProblemForm_BuildReport assembles the full payload incl. the chosen
// severity + optional photo path, trimming whitespace.
func TestLocationProblemForm_BuildReport(t *testing.T) {
	s := NewLocationProblemFormScreen(Deps{}, 42, "Bay 3")
	s.inputs[lpfDescription].SetValue("  Ceiling leak over the CNC  ")
	s.inputs[lpfPhoto].SetValue("  /tmp/leak.jpg  ")
	s.severityIdx = indexOfSeverity(omsapi.LocationProblemSeverityUrgent)

	body, err := s.buildReport()
	if err != nil {
		t.Fatalf("buildReport: %v", err)
	}
	if body.Description != "Ceiling leak over the CNC" {
		t.Errorf("description = %q (should be trimmed)", body.Description)
	}
	if body.Severity != omsapi.LocationProblemSeverityUrgent {
		t.Errorf("severity = %q", body.Severity)
	}
	if body.PhotoPath != "/tmp/leak.jpg" {
		t.Errorf("photo path = %q (should be trimmed)", body.PhotoPath)
	}
}

// TestLocationProblemForm_RenderSmoke guards the render path.
func TestLocationProblemForm_RenderSmoke(t *testing.T) {
	s := NewLocationProblemFormScreen(Deps{}, 42, "Bay 3")
	out := s.View()
	for _, want := range []string{"Description", "Severity", "Photo path"} {
		if !strings.Contains(out, want) {
			t.Errorf("form view missing %q: %q", want, out)
		}
	}
}

func indexOfSeverity(code string) int {
	for i, o := range locationProblemSeverityOptions {
		if o.value == code {
			return i
		}
	}
	return 0
}
