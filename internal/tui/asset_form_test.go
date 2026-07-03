package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestAssetForm_RenderSmoke exercises the render paths (form + single picker +
// multi picker) to guard against index panics in the windowed renderers.
func TestAssetForm_RenderSmoke(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 30

	if out := s.View(); !strings.Contains(out, "Name") {
		t.Errorf("form view missing Name field: %q", out)
	}

	// Enabling the conditional sections must not break rendering.
	s.setCursorToField(afIsDonation)
	s.flipToggle(afIsDonation)
	s.setCursorToField(afOwnership)
	s.cycleSelect(afOwnership, +1) // → group, reveals owning-SIG picker
	if out := s.View(); out == "" {
		t.Errorf("form view empty after enabling conditional sections")
	}

	// The single-value category picker renders the synthetic (none) row even
	// with no reference data loaded.
	s.openPicker(afCategory)
	if out := s.View(); !strings.Contains(out, "(none)") {
		t.Errorf("category picker view missing (none) row: %q", out)
	}

	// The multi-select certifications picker renders checkboxes.
	s.phase = assetPhaseForm
	s.certs = []omsapi.CertificationOption{{ID: 1, Name: "Laser"}, {ID: 2, Name: "CNC"}}
	s.openPicker(afRequiredCerts)
	s.toggleCurrentCert()
	if out := s.View(); !strings.Contains(out, "[x]") {
		t.Errorf("certs picker view missing checked row: %q", out)
	}
	if !s.certSelected(1) {
		t.Errorf("expected cert 1 to be selected after toggle")
	}
}

// TestAssetForm_BuildPayload walks the happy path: category/location/inventory
// picks produce an AssetWrite with location as an int pk, inventory_item as a
// UUID string, amount as a decimal string, the status select applied, and the
// create-mode defaults (is_active true, ownership space).
func TestAssetForm_BuildPayload(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")
	s.inputs[afName].SetValue("Metal lathe")
	s.inputs[afAmountPaid].SetValue("125.50")
	cat := 3
	loc := 7
	item := "item-uuid-1"
	s.categoryID = &cat
	s.locationID = &loc
	s.inventoryItemID = &item
	s.statusIdx = assetStatusIndex("maintenance")
	s.trainingRequired = true
	s.certIDs = []int{3, 5}

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Metal lathe" {
		t.Errorf("name = %q", w.Name)
	}
	if w.AmountPaid != "125.50" {
		t.Errorf("amount_paid = %q (want \"125.50\")", w.AmountPaid)
	}
	if w.Category == nil || *w.Category != 3 {
		t.Errorf("category = %v", w.Category)
	}
	if w.Location == nil || *w.Location != 7 {
		t.Errorf("location = %v (want int pk 7)", w.Location)
	}
	if w.InventoryItem == nil || *w.InventoryItem != "item-uuid-1" {
		t.Errorf("inventory_item = %v (want uuid string)", w.InventoryItem)
	}
	if w.Status != "maintenance" {
		t.Errorf("status = %q", w.Status)
	}
	if w.OwnershipType != "space" {
		t.Errorf("ownership_type = %q (want space default)", w.OwnershipType)
	}
	if !w.IsActive {
		t.Errorf("is_active default should be true")
	}
	if !w.TrainingRequired {
		t.Errorf("training_required should be true")
	}
	if len(w.RequiredCertifications) != 2 || w.RequiredCertifications[0] != 3 {
		t.Errorf("required_certifications = %v", w.RequiredCertifications)
	}
	// space ownership → owning_group stays nil (→ omitted on the wire).
	if w.OwningGroup != nil {
		t.Errorf("owning_group should be nil for space ownership, got %v", *w.OwningGroup)
	}
}

// TestAssetForm_Validation covers name-required, the ownership refinements
// (group needs a SIG, user is a dead-end), and amount/date parsing.
func TestAssetForm_Validation(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")

	// name required
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[afName].SetValue("Bandsaw")

	// group ownership without a SIG picked → error
	s.ownershipIdx = assetOwnershipIndex("group")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: group ownership requires an owning SIG")
	}
	// ...with a SIG → ok
	g := 4
	s.owningGroupID = &g
	if w, err := s.buildPayload(); err != nil {
		t.Errorf("group ownership with SIG should validate: %v", err)
	} else if w.OwningGroup == nil || *w.OwningGroup != 4 {
		t.Errorf("owning_group = %v (want 4)", w.OwningGroup)
	}

	// user ownership is a dead-end (no owning_user picker) → error
	s.ownershipIdx = assetOwnershipIndex("user")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: user ownership can't be set from the TUI")
	}
	s.ownershipIdx = assetOwnershipIndex("space")

	// amount must parse and be non-negative
	s.inputs[afAmountPaid].SetValue("abc")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for non-numeric amount")
	}
	s.inputs[afAmountPaid].SetValue("-5")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for negative amount")
	}
	s.inputs[afAmountPaid].SetValue("10")

	// date_received, when present, must be YYYY-MM-DD
	s.inputs[afDateReceived].SetValue("nope")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for malformed date")
	}
	s.inputs[afDateReceived].SetValue("2026-07-03")
	if w, err := s.buildPayload(); err != nil {
		t.Errorf("valid date should pass: %v", err)
	} else if w.DateReceived == nil || *w.DateReceived != "2026-07-03" {
		t.Errorf("date_received = %v", w.DateReceived)
	}
}

// TestAssetForm_ConditionalFields confirms is_donation adds the donor field and
// group ownership adds the owning-SIG picker, keeping the cursor anchored on
// the control that was toggled.
func TestAssetForm_ConditionalFields(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "")

	if fieldsContain(s.fields, afDonorName) {
		t.Fatalf("donor name should be hidden before donation is enabled")
	}
	s.setCursorToField(afIsDonation)
	s.flipToggle(afIsDonation)
	if !fieldsContain(s.fields, afDonorName) {
		t.Errorf("donor name should appear after enabling donation")
	}
	if id, ok := s.currentFieldID(); !ok || id != afIsDonation {
		t.Errorf("cursor should stay on the donation toggle, got id=%d ok=%v", id, ok)
	}

	if fieldsContain(s.fields, afOwningGroup) {
		t.Fatalf("owning-SIG picker should be hidden before group ownership")
	}
	s.setCursorToField(afOwnership)
	s.cycleSelect(afOwnership, +1) // space → group
	if !fieldsContain(s.fields, afOwningGroup) {
		t.Errorf("owning-SIG picker should appear when ownership is group")
	}
	if id, ok := s.currentFieldID(); !ok || id != afOwnership {
		t.Errorf("cursor should stay on the ownership select, got id=%d ok=%v", id, ok)
	}
}

// TestAssetForm_HydrateInfersOwnership confirms edit-mode hydration fills text
// + picker state from the fetched asset and infers group ownership from a
// present owning_group (ownership_type isn't serialized).
func TestAssetForm_HydrateInfersOwnership(t *testing.T) {
	s := NewAssetFormScreen(Deps{}, "asset-9")
	group := 4
	loc := 7
	s.asset = &omsapi.Asset{
		Name:                   "CNC Router",
		SerialNumber:           "SN-123",
		Location:               &loc,
		OwningGroup:            &group,
		Status:                 "testing",
		AmountPaid:             omsapi.DecimalString("999.99"),
		TrainingRequired:       true,
		RequiredCertifications: []int{2, 1},
		IsActive:               true,
	}
	s.hydrate()

	if got := s.inputs[afName].Value(); got != "CNC Router" {
		t.Errorf("name = %q", got)
	}
	if got := s.inputs[afSerialNumber].Value(); got != "SN-123" {
		t.Errorf("serial = %q", got)
	}
	if got := s.inputs[afAmountPaid].Value(); got != "999.99" {
		t.Errorf("amount = %q", got)
	}
	if s.locationID == nil || *s.locationID != 7 {
		t.Errorf("locationID = %v", s.locationID)
	}
	if assetOwnershipOptions[s.ownershipIdx].value != "group" {
		t.Errorf("ownership should be inferred as group, got %q", assetOwnershipOptions[s.ownershipIdx].value)
	}
	if s.owningGroupID == nil || *s.owningGroupID != 4 {
		t.Errorf("owningGroupID = %v", s.owningGroupID)
	}
	if assetStatusOptions[s.statusIdx].value != "testing" {
		t.Errorf("status = %q", assetStatusOptions[s.statusIdx].value)
	}
	// certs hydrate sorted
	if len(s.certIDs) != 2 || s.certIDs[0] != 1 || s.certIDs[1] != 2 {
		t.Errorf("certIDs = %v (want sorted [1 2])", s.certIDs)
	}
}
