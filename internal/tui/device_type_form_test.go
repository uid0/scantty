package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// TestDeviceTypeCodeOptions_MatchBackend pins the code choice list to the
// backend DeviceType.TYPE_CHOICES. A wrong or missing code 400s on save, so the
// count and a few load-bearing values (incl. the power_relay rename trap) are
// asserted, and every value must be non-empty.
func TestDeviceTypeCodeOptions_MatchBackend(t *testing.T) {
	if len(deviceTypeCodeOptions) != 19 {
		t.Errorf("code options = %d, want 19 (backend TYPE_CHOICES)", len(deviceTypeCodeOptions))
	}
	want := map[string]bool{"indicator": false, "badge_reader": false, "power_relay": false, "mortise_key": false}
	for _, o := range deviceTypeCodeOptions {
		if strings.TrimSpace(o.value) == "" || strings.TrimSpace(o.label) == "" {
			t.Errorf("code option has empty value/label: %+v", o)
		}
		if _, ok := want[o.value]; ok {
			want[o.value] = true
		}
		// the AC Relay rename: value must be power_relay, never ac_relay.
		if o.value == "ac_relay" {
			t.Errorf("code option must use value power_relay, not ac_relay")
		}
	}
	for v, seen := range want {
		if !seen {
			t.Errorf("expected code %q missing from options", v)
		}
	}
}

func TestDeviceTypeCodeLabel(t *testing.T) {
	if got := deviceTypeCodeLabel("power_relay"); got != "AC Relay" {
		t.Errorf("power_relay label = %q, want AC Relay", got)
	}
	if got := deviceTypeCodeLabel("some_future_code"); got != "some_future_code" {
		t.Errorf("unknown code should fall back to the raw value, got %q", got)
	}
	if got := deviceTypeCodeLabel(""); got != "(unset)" {
		t.Errorf("empty code label = %q, want (unset)", got)
	}
}

// TestDeviceTypeForm_BuildPayload_Create confirms create mode sends the full
// writable set (name, code, description, is_active).
func TestDeviceTypeForm_BuildPayload_Create(t *testing.T) {
	s := NewDeviceTypeFormScreen(Deps{}, 0)
	s.inputs[dtName].SetValue("  Badge Reader  ") // trimmed
	s.codeIdx = selectIndexOf(deviceTypeCodeOptions, "badge_reader")
	s.inputs[dtDescription].SetValue("front door")
	s.isActive = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Badge Reader" {
		t.Errorf("name = %q, want trimmed Badge Reader", w.Name)
	}
	if w.Code != "badge_reader" {
		t.Errorf("code = %q, want badge_reader", w.Code)
	}
	if w.Description != "front door" {
		t.Errorf("description = %q", w.Description)
	}
	if !w.IsActive {
		t.Errorf("is_active should be true")
	}
}

// TestDeviceTypeForm_BuildPayload_EditOmitsCode confirms edit mode leaves Code
// blank so the client omits it from the PATCH (code immutable after creation),
// while still sending name/description/is_active — including a cleared
// description and a flipped-off is_active.
func TestDeviceTypeForm_BuildPayload_EditOmitsCode(t *testing.T) {
	s := NewDeviceTypeFormScreen(Deps{}, 5) // edit mode
	// even if codeIdx points at a real code, edit must not emit it.
	s.codeIdx = selectIndexOf(deviceTypeCodeOptions, "indicator")
	s.inputs[dtName].SetValue("Renamed")
	s.inputs[dtDescription].SetValue("") // cleared
	s.isActive = false

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Code != "" {
		t.Errorf("edit payload must omit code, got %q", w.Code)
	}
	if w.Name != "Renamed" {
		t.Errorf("name = %q", w.Name)
	}
	if w.Description != "" {
		t.Errorf("description = %q, want cleared", w.Description)
	}
	if w.IsActive {
		t.Errorf("is_active should be false")
	}
}

func TestDeviceTypeForm_Validation(t *testing.T) {
	s := NewDeviceTypeFormScreen(Deps{}, 0)
	// name required
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[dtName].SetValue("Env")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("valid create should pass: %v", err)
	}
}

// TestDeviceTypeForm_Hydrate confirms edit hydration fills every field and that
// the code field drops out of the navigable list in edit mode (shown fixed).
func TestDeviceTypeForm_Hydrate(t *testing.T) {
	s := NewDeviceTypeFormScreen(Deps{}, 3)
	s.dt = &forgekeyapi.DeviceType{
		ID: float64(3), Name: "OLED", Code: "oled_screen", Description: "128x64", IsActive: false,
	}
	s.hydrate()
	if s.inputs[dtName].Value() != "OLED" {
		t.Errorf("name = %q", s.inputs[dtName].Value())
	}
	if s.inputs[dtDescription].Value() != "128x64" {
		t.Errorf("description = %q", s.inputs[dtDescription].Value())
	}
	if deviceTypeCodeOptions[s.codeIdx].value != "oled_screen" {
		t.Errorf("code idx = %q", deviceTypeCodeOptions[s.codeIdx].value)
	}
	if s.isActive {
		t.Errorf("is_active should hydrate false")
	}
	// edit mode: code is not a navigable field (it's fixed after creation).
	if fieldsContain(s.fields, dtCode) {
		t.Errorf("edit mode should not expose the code field for editing")
	}
	// create mode: code IS a navigable field.
	c := NewDeviceTypeFormScreen(Deps{}, 0)
	if !fieldsContain(c.fields, dtCode) {
		t.Errorf("create mode should expose the code field")
	}
}

func TestDeviceTypeForm_CodeCycle(t *testing.T) {
	s := NewDeviceTypeFormScreen(Deps{}, 0)
	s.codeIdx = 0
	s.cycleCode(-1) // wrap to last
	if s.codeIdx != len(deviceTypeCodeOptions)-1 {
		t.Errorf("cycle -1 from 0 = %d, want %d", s.codeIdx, len(deviceTypeCodeOptions)-1)
	}
	s.cycleCode(+1) // wrap back to 0
	if s.codeIdx != 0 {
		t.Errorf("cycle +1 wrap = %d, want 0", s.codeIdx)
	}
}

func TestDeviceTypeForm_RenderSmoke(t *testing.T) {
	// create: shows the editable Code select field.
	c := NewDeviceTypeFormScreen(Deps{}, 0)
	c.loading = false
	c.terminalHeight = 30
	out := c.View()
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Code") {
		t.Errorf("create form should show Name + Code: %q", out)
	}
	if strings.Contains(out, "fixed after creation") {
		t.Errorf("create form should NOT show the fixed-code line: %q", out)
	}

	// edit: shows the fixed-code header line instead of an editable select.
	e := NewDeviceTypeFormScreen(Deps{}, 7)
	e.loading = false
	e.terminalHeight = 30
	e.dt = &forgekeyapi.DeviceType{ID: float64(7), Name: "Relay", Code: "power_relay", IsActive: true}
	if out := e.View(); !strings.Contains(out, "fixed after creation") || !strings.Contains(out, "AC Relay") {
		t.Errorf("edit form should show the fixed code line with the AC Relay label: %q", out)
	}
}

// --- DeviceTypeListScreen ---

func TestDeviceTypeList_HandlesKey(t *testing.T) {
	s := NewDeviceTypeListScreen(Deps{})
	for _, k := range []string{"n", "G"} {
		if !s.HandlesKey(k) {
			t.Errorf("list should claim colliding global key %q", k)
		}
	}
	for _, k := range []string{"j", "k", "x", "E", "enter", "r"} {
		if s.HandlesKey(k) {
			t.Errorf("list should NOT claim non-colliding key %q (reaches via fall-through)", k)
		}
	}
	// raw input only while confirming a delete.
	if s.WantsRawInput() {
		t.Errorf("list should not grab raw input outside the delete confirm")
	}
	s.confirmingDelete = true
	if !s.WantsRawInput() {
		t.Errorf("list should grab raw input while confirming a delete")
	}
	// while confirming, HandlesKey must yield so WantsRawInput routes every key.
	if s.HandlesKey("n") {
		t.Errorf("during confirm, HandlesKey should yield to WantsRawInput")
	}
}

func TestDeviceTypeList_RenderSmoke(t *testing.T) {
	s := NewDeviceTypeListScreen(Deps{})
	s.loading = false
	s.terminalHeight = 30
	s.rows = []forgekeyapi.DeviceType{
		{ID: float64(1), Name: "Indicator", Code: "indicator", IsActive: true, Description: "status LED"},
		{ID: float64(2), Name: "Old Relay", Code: "power_relay", IsActive: false},
	}
	out := s.View()
	if !strings.Contains(out, "Indicator") || !strings.Contains(out, "Indicator/Status Light") {
		t.Errorf("row should show name + code label: %q", out)
	}
	if !strings.Contains(out, "inactive") {
		t.Errorf("inactive row should be flagged: %q", out)
	}

	// delete confirm surfaces the PROTECT/cascade heads-up.
	s.cursor = 1
	s.confirmingDelete = true
	if out := s.View(); !strings.Contains(out, "Blocked") || !strings.Contains(out, "Old Relay") {
		t.Errorf("delete confirm should warn about the block + name: %q", out)
	}
}
