package tui

import (
	"strings"
	"testing"
)

// TestInventoryItemForm_RenderSmoke exercises the render paths (form + picker)
// to guard against index panics in the windowed field/option renderers.
func TestInventoryItemForm_RenderSmoke(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 30

	if out := s.View(); !strings.Contains(out, "Name") {
		t.Errorf("form view missing Name field: %q", out)
	}

	// Enabling the conditional sections must not break rendering.
	s.setCursorToField(fIsSerialized)
	s.flipToggle(fIsSerialized)
	s.setCursorToField(fIsHazardous)
	s.flipToggle(fIsHazardous)
	s.setCursorToField(fUseCaseBasedReorder)
	s.flipToggle(fUseCaseBasedReorder)
	if out := s.View(); out == "" {
		t.Errorf("form view empty after enabling conditional sections")
	}

	// The category picker sub-phase renders the synthetic (none) row even with
	// no reference data loaded.
	s.openPicker(fCategory)
	if out := s.View(); !strings.Contains(out, "(none)") {
		t.Errorf("category picker view missing (none) row: %q", out)
	}
}

func fieldsContain(fields []int, id int) bool {
	for _, f := range fields {
		if f == id {
			return true
		}
	}
	return false
}

// TestInventoryItemForm_BuildPayload walks the happy path: a serialized item
// with category + location picks produces an ItemWrite with the location as a
// string pk, category as an int, the serialized flag + mode set, and is_active
// defaulting to true.
func TestInventoryItemForm_BuildPayload(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.inputs[fName].SetValue("Widget")
	s.inputs[fCurrentStock].SetValue("10")
	s.inputs[fMinimumStock].SetValue("2")
	s.inputs[fReorderQuantity].SetValue("3")
	s.isSerialized = true
	s.serialMode = 1 // reusable
	cat := 4
	loc := 7
	s.categoryID = &cat
	s.locationID = &loc

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Widget" {
		t.Errorf("name = %q", w.Name)
	}
	if w.CurrentStock != 10 || w.MinimumStock != 2 || w.ReorderQuantity != 3 {
		t.Errorf("stock fields = %d/%d/%d", w.CurrentStock, w.MinimumStock, w.ReorderQuantity)
	}
	if w.Category == nil || *w.Category != 4 {
		t.Errorf("category = %v", w.Category)
	}
	if w.Location == nil || *w.Location != "7" {
		t.Errorf("location = %v (want string \"7\")", w.Location)
	}
	if !w.IsSerialized || w.SerialTrackingMode == nil || *w.SerialTrackingMode != "reusable" {
		t.Errorf("serialized fields = %v / %v", w.IsSerialized, w.SerialTrackingMode)
	}
	if !w.IsActive {
		t.Errorf("is_active default should be true")
	}
	// serial mode set but not case-based / hazardous -> those stay unset.
	if w.MinimumCases != nil || w.MSDSURL != nil {
		t.Errorf("unexpected optional fields set: %v %v", w.MinimumCases, w.MSDSURL)
	}
}

// TestInventoryItemForm_ModeOmittedWhenNotSerialized confirms the mode pointer
// is nil (→ omitted on the wire) when the item isn't serialized.
func TestInventoryItemForm_ModeOmittedWhenNotSerialized(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")
	s.inputs[fName].SetValue("Plain")
	s.serialMode = 1 // even with a mode selected, an unserialized item omits it
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.IsSerialized {
		t.Errorf("is_serialized should be false")
	}
	if w.SerialTrackingMode != nil {
		t.Errorf("serial_tracking_mode should be nil when not serialized, got %v", *w.SerialTrackingMode)
	}
}

// TestInventoryItemForm_Validation covers the two web-mirrored refinements:
// name is required, and hazardous items require an MSDS URL.
func TestInventoryItemForm_Validation(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")

	s.inputs[fName].SetValue("")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}

	s.inputs[fName].SetValue("Acid")
	s.isHazardous = true
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: hazardous requires MSDS URL")
	}
	s.inputs[fMSDSURL].SetValue("https://x/sds.pdf")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("hazardous with MSDS should validate: %v", err)
	}

	// Case-based requires both case numbers.
	s.isHazardous = false
	s.useCaseBased = true
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: case-based requires minimum/reorder cases")
	}
}

// TestInventoryItemForm_ConditionalFields confirms toggling is_serialized adds
// the tracking-mode field and keeps the cursor anchored on the toggle.
func TestInventoryItemForm_ConditionalFields(t *testing.T) {
	s := NewInventoryItemFormScreen(Deps{}, "")

	if fieldsContain(s.fields, fSerialTrackingMode) {
		t.Fatalf("tracking mode should be hidden before serialization is enabled")
	}
	s.setCursorToField(fIsSerialized)
	s.flipToggle(fIsSerialized)

	if !fieldsContain(s.fields, fSerialTrackingMode) {
		t.Errorf("tracking mode should appear after enabling serialization")
	}
	if id, ok := s.currentFieldID(); !ok || id != fIsSerialized {
		t.Errorf("cursor should stay on the serialized toggle, got id=%d ok=%v", id, ok)
	}

	// Hazmat block toggles in the same way.
	if fieldsContain(s.fields, fMSDSURL) {
		t.Fatalf("MSDS field should be hidden before hazardous is enabled")
	}
	s.setCursorToField(fIsHazardous)
	s.flipToggle(fIsHazardous)
	for _, id := range []int{fMSDSURL, fNFPAHealth, fNFPAFire, fNFPAInstability, fNFPASpecial} {
		if !fieldsContain(s.fields, id) {
			t.Errorf("hazmat field %d should appear after enabling hazardous", id)
		}
	}
}
