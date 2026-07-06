package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func TestAssetPartForm_BuildPayload(t *testing.T) {
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "")
	item := "item-1"
	s.partItemID = &item
	s.inputs[apfQuantity].SetValue("3")
	s.isRequired = false
	s.inputs[apfInterval].SetValue("90")
	s.inputs[apfNotes].SetValue("OEM only")

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Asset != "asset-9" || w.Part != "item-1" {
		t.Errorf("asset/part = %q/%q", w.Asset, w.Part)
	}
	if w.QuantityNeeded != 3 {
		t.Errorf("quantity_needed = %d", w.QuantityNeeded)
	}
	if w.IsRequired {
		t.Errorf("is_required should be false")
	}
	if w.MaintenanceIntervalDays == nil || *w.MaintenanceIntervalDays != 90 {
		t.Errorf("maintenance_interval_days = %v", w.MaintenanceIntervalDays)
	}
	if w.Notes != "OEM only" {
		t.Errorf("notes = %q", w.Notes)
	}
}

func TestAssetPartForm_Validation(t *testing.T) {
	// Missing part is the only hard-required field.
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "")
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when part is unset")
	}

	// Quantity below the min-1 floor.
	s = NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "")
	item := "item-1"
	s.partItemID = &item
	s.inputs[apfQuantity].SetValue("0")
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when quantity < 1")
	}

	// Non-numeric quantity.
	s.inputs[apfQuantity].SetValue("abc")
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when quantity is non-numeric")
	}

	// Non-numeric interval.
	s.inputs[apfQuantity].SetValue("1")
	s.inputs[apfInterval].SetValue("soon")
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when interval is non-numeric")
	}

	// Interval below min-1.
	s.inputs[apfInterval].SetValue("0")
	if _, err := s.buildPayload(); err == nil {
		t.Error("expected error when interval < 1")
	}
}

func TestAssetPartForm_QuantityDefaultAndIntervalBlank(t *testing.T) {
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "")
	item := "item-1"
	s.partItemID = &item
	// Constructor defaults quantity to "1"; leave interval blank.
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.QuantityNeeded != 1 {
		t.Errorf("quantity default = %d, want 1", w.QuantityNeeded)
	}
	if w.MaintenanceIntervalDays != nil {
		t.Errorf("blank interval should be nil, got %v", *w.MaintenanceIntervalDays)
	}

	// An explicitly cleared quantity also falls back to the default of 1.
	s.inputs[apfQuantity].SetValue("")
	if w, err := s.buildPayload(); err != nil || w.QuantityNeeded != 1 {
		t.Errorf("empty quantity: w=%+v err=%v", w, err)
	}
}

func TestAssetPartForm_Hydrate(t *testing.T) {
	interval := 30
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "5")
	s.part = &omsapi.AssetPart{
		ID:                      float64(5),
		Asset:                   "asset-9",
		Part:                    "item-7",
		PartName:                "Coolant filter",
		QuantityNeeded:          4,
		IsRequired:              false,
		MaintenanceIntervalDays: &interval,
		Notes:                   "swap with gloves",
	}
	s.hydrate()

	if got := s.inputs[apfQuantity].Value(); got != "4" {
		t.Errorf("quantity hydrate = %q", got)
	}
	if s.isRequired {
		t.Errorf("is_required should hydrate false")
	}
	if got := s.inputs[apfInterval].Value(); got != "30" {
		t.Errorf("interval hydrate = %q", got)
	}
	if got := s.inputs[apfNotes].Value(); got != "swap with gloves" {
		t.Errorf("notes hydrate = %q", got)
	}
	if s.partItemID == nil || *s.partItemID != "item-7" {
		t.Errorf("part hydrate = %v", s.partItemID)
	}

	// A round-trip through buildPayload keeps the edited values.
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload after hydrate: %v", err)
	}
	if w.Part != "item-7" || w.QuantityNeeded != 4 || w.MaintenanceIntervalDays == nil || *w.MaintenanceIntervalDays != 30 {
		t.Errorf("payload after hydrate = %+v", w)
	}
}

func TestAssetPartForm_Picker(t *testing.T) {
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "")
	s.items = []omsapi.Item{
		{ID: "item-1", Name: "Drive belt", SKU: "B-1"},
		{ID: "item-2", Name: "Air filter"},
	}
	s.openPicker()
	// Row 0 is the synthetic "(none)" clear row; rows 1..n are the items.
	if len(s.pickOptions) != 3 {
		t.Fatalf("pickOptions = %d, want 3", len(s.pickOptions))
	}
	s.pickCursor = 1
	s.commitPick()
	if s.partItemID == nil || *s.partItemID != "item-1" {
		t.Fatalf("commitPick selected = %v, want item-1", s.partItemID)
	}
	if s.phase != assetPhaseForm {
		t.Errorf("phase should return to form after commit")
	}

	// Selecting the "(none)" row clears the FK.
	s.openPicker()
	s.pickCursor = 0
	s.commitPick()
	if s.partItemID != nil {
		t.Errorf("selecting (none) should clear part, got %v", *s.partItemID)
	}
}

// TestAssetPartForm_EditPickerKeepsAbsentPart guards the codex-flagged bug:
// when editing a part whose InventoryItem is not in the loaded item set, opening
// the picker must land the cursor on the (injected) current part and NOT on the
// "(none)" row — otherwise pressing enter would silently clear a required FK.
func TestAssetPartForm_EditPickerKeepsAbsentPart(t *testing.T) {
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "5")
	s.part = &omsapi.AssetPart{ID: float64(5), Part: "item-gone", PartName: "Legacy belt", PartSKU: "OLD-1"}
	s.hydrate() // sets partItemID = "item-gone"
	// The loaded page does NOT contain item-gone.
	s.items = []omsapi.Item{{ID: "item-1", Name: "Belt"}, {ID: "item-2", Name: "Filter"}}

	s.openPicker()
	// The current part is injected as a selectable row and the cursor rests on it.
	opt := s.pickOptions[s.pickCursor]
	if opt.clear || opt.key != "item-gone" {
		t.Fatalf("cursor should rest on the current part, got %+v", opt)
	}
	// Pressing enter re-selects it rather than clearing the required FK.
	s.commitPick()
	if s.partItemID == nil || *s.partItemID != "item-gone" {
		t.Fatalf("enter on the current part must keep it, got %v", s.partItemID)
	}

	// And the picker label still shows the linked part's name from the fetched
	// AssetPart even though the item is absent from the loaded list.
	if got := s.partLabel(); got != "Legacy belt (OLD-1)" {
		t.Errorf("partLabel = %q, want the denormalized name", got)
	}
}

func TestAssetPartForm_RenderSmoke(t *testing.T) {
	s := NewAssetPartFormScreen(Deps{}, "asset-9", "Lathe", "")
	if strings.TrimSpace(s.View()) == "" {
		t.Error("loading view should render something")
	}
	s.loading = false
	s.items = []omsapi.Item{{ID: "item-1", Name: "Belt", SKU: "B-1"}}
	if out := s.View(); !strings.Contains(out, "Part") {
		t.Errorf("form view missing Part field: %q", out)
	}
	s.phase = assetPhasePick
	s.openPicker()
	if strings.TrimSpace(s.View()) == "" {
		t.Error("pick view should render something")
	}
}
