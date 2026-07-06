package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestMakerBoxForm_BuildPayload walks the happy path: bin_id + username, the
// default status (unassigned), and a date-only expires_at that normalizes to
// midnight UTC.
func TestMakerBoxForm_BuildPayload(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.inputs[mbfBinID].SetValue("PSB-007")
	s.inputs[mbfAssignedUsername].SetValue("jdoe")
	s.inputs[mbfFirstName].SetValue("Jane")
	s.inputs[mbfEmail].SetValue("jane@example.com")
	s.inputs[mbfExpiresAt].SetValue("2026-12-31")
	s.inputs[mbfNotes].SetValue("corner shelf")

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.BinID == nil || *w.BinID != "PSB-007" {
		t.Errorf("bin_id = %v", w.BinID)
	}
	if w.AssignedUsername != "jdoe" {
		t.Errorf("assigned_username = %q", w.AssignedUsername)
	}
	if w.Status != "unassigned" {
		t.Errorf("status = %q, want unassigned default", w.Status)
	}
	if w.IdentitySource != "" {
		t.Errorf("identity_source = %q, want blank default", w.IdentitySource)
	}
	if w.ExpiresAt == nil || *w.ExpiresAt != "2026-12-31T00:00:00Z" {
		t.Errorf("expires_at = %v, want midnight-normalized", w.ExpiresAt)
	}
	// Untouched datetimes stay nil so the wire sends explicit null.
	if w.AssignedAt != nil || w.PaidAt != nil || w.ConversionCompletedAt != nil {
		t.Errorf("unset datetimes should be nil: %+v", w)
	}
}

// TestMakerBoxForm_StatusCycle confirms the status select cycles through all six
// choices and wraps.
func TestMakerBoxForm_StatusCycle(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.inputs[mbfBinID].SetValue("PSB-1")
	s.cursor = 1 // mbfStatus
	if id, _ := s.currentFieldID(); id != mbfStatus {
		t.Fatalf("cursor not on status field")
	}
	// unassigned -> valid
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace})
	if w, _ := s.buildPayload(); w.Status != "valid" {
		t.Errorf("after 1 cycle status = %q, want valid", w.Status)
	}
	// left wraps valid -> unassigned
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeyLeft})
	if w, _ := s.buildPayload(); w.Status != "unassigned" {
		t.Errorf("after left status = %q, want unassigned", w.Status)
	}
	// left again wraps unassigned -> pre_conversion (last option)
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeyLeft})
	if w, _ := s.buildPayload(); w.Status != "pre_conversion" {
		t.Errorf("after wrap status = %q, want pre_conversion", w.Status)
	}
}

// TestMakerBoxForm_IdentityCycle confirms the identity-source select starts
// blank and cycles into the real choices.
func TestMakerBoxForm_IdentityCycle(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.inputs[mbfBinID].SetValue("PSB-1")
	if makerBoxIdentityOptions[s.identityIdx].value != "" {
		t.Fatalf("identity default should be blank")
	}
	s.cursor = 6 // mbfIdentitySource
	if id, _ := s.currentFieldID(); id != mbfIdentitySource {
		t.Fatalf("cursor not on identity field, got %d", func() int { id, _ := s.currentFieldID(); return id }())
	}
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace}) // "" -> whmcs
	if w, _ := s.buildPayload(); w.IdentitySource != "whmcs" {
		t.Errorf("identity_source = %q, want whmcs", w.IdentitySource)
	}
}

// TestMakerBoxForm_RequiresIdentifier rejects a submit with neither bin_id nor
// assigned_username (an anonymous empty row).
func TestMakerBoxForm_RequiresIdentifier(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error when bin_id and username both blank")
	}
	// username alone is enough
	s.inputs[mbfAssignedUsername].SetValue("solo")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("username-only should be valid: %v", err)
	}
}

// TestMakerBoxForm_BlankBinIsNil confirms a blank bin_id becomes a nil pointer
// (→ JSON null) rather than an empty string.
func TestMakerBoxForm_BlankBinIsNil(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.inputs[mbfAssignedUsername].SetValue("queued")
	s.cursor = 1
	s.updateFormPhase(tea.KeyMsg{Type: tea.KeySpace}) // status -> valid, still no bin
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.BinID != nil {
		t.Errorf("blank bin_id should be nil, got %v", *w.BinID)
	}
}

// TestMakerBoxForm_DateTimeValidation rejects a malformed datetime.
func TestMakerBoxForm_DateTimeValidation(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.inputs[mbfBinID].SetValue("PSB-1")
	s.inputs[mbfPaidAt].SetValue("last tuesday")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for malformed paid_at")
	}
}

// TestMakerBoxForm_EmailValidation rejects an obviously bad email but accepts
// blank.
func TestMakerBoxForm_EmailValidation(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.inputs[mbfBinID].SetValue("PSB-1")
	s.inputs[mbfEmail].SetValue("not-an-email")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for email without @")
	}
	s.inputs[mbfEmail].SetValue("")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("blank email should be valid: %v", err)
	}
}

// TestMakerBoxForm_Hydrate fills every field from a fetched box, including the
// status/identity indexes and the datetime round-trip.
func TestMakerBoxForm_Hydrate(t *testing.T) {
	exp := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	s := NewMakerBoxFormScreen(Deps{}, 7)
	s.box = &omsapi.MakerBox{
		ID:               7,
		BinID:            "PSB-007",
		AssignedUsername: "jdoe",
		FirstName:        "Jane",
		LastName:         "Doe",
		Email:            "jane@example.com",
		Status:           "grace",
		IdentitySource:   "whmcs",
		ExpiresAt:        &exp,
		Notes:            "note",
	}
	s.hydrate()
	if s.inputs[mbfBinID].Value() != "PSB-007" {
		t.Errorf("bin_id = %q", s.inputs[mbfBinID].Value())
	}
	if makerBoxStatusOptions[s.statusIdx].value != "grace" {
		t.Errorf("status idx = %q, want grace", makerBoxStatusOptions[s.statusIdx].value)
	}
	if makerBoxIdentityOptions[s.identityIdx].value != "whmcs" {
		t.Errorf("identity idx = %q, want whmcs", makerBoxIdentityOptions[s.identityIdx].value)
	}
	if s.inputs[mbfExpiresAt].Value() != "2026-12-31T00:00:00Z" {
		t.Errorf("expires_at hydrated = %q", s.inputs[mbfExpiresAt].Value())
	}
	// Hydrated value must round-trip back through buildPayload unchanged.
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload after hydrate: %v", err)
	}
	if w.ExpiresAt == nil || *w.ExpiresAt != "2026-12-31T00:00:00Z" {
		t.Errorf("expires_at round-trip = %v", w.ExpiresAt)
	}
}

// TestMakerBoxParseDateTime pins the normalization rules directly.
func TestMakerBoxParseDateTime(t *testing.T) {
	if p, err := makerBoxParseDateTime(""); err != nil || p != nil {
		t.Errorf("blank => nil,nil; got %v,%v", p, err)
	}
	if p, err := makerBoxParseDateTime("2026-07-06"); err != nil || p == nil || *p != "2026-07-06T00:00:00Z" {
		t.Errorf("date-only normalize; got %v,%v", p, err)
	}
	if p, err := makerBoxParseDateTime("2026-07-06T14:30:00Z"); err != nil || p == nil || *p != "2026-07-06T14:30:00Z" {
		t.Errorf("rfc3339 passthrough; got %v,%v", p, err)
	}
	if _, err := makerBoxParseDateTime("garbage"); err == nil {
		t.Errorf("garbage should error")
	}
}

// TestMakerBoxForm_RenderSmoke guards the form render path and confirms the
// select fields draw.
func TestMakerBoxForm_RenderSmoke(t *testing.T) {
	s := NewMakerBoxFormScreen(Deps{}, 0)
	s.terminalHeight = 40
	out := s.View()
	if !strings.Contains(out, "Status") || !strings.Contains(out, "Unassigned") {
		t.Errorf("form view missing status select: %q", out)
	}
	if !strings.Contains(out, "Bin id") {
		t.Errorf("form view missing bin id field: %q", out)
	}
}
