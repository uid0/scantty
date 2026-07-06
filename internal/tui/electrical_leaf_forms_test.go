package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// --- choice-code completeness guards ---------------------------------------

// TestNEMAOptions_CoverSerializer pins that the outlet picker offers the full
// authoritative NEMA/IEC set from the backend NEMA_PORT_TYPE_CHOICES (a wrong or
// missing code silently narrows what an operator can record vs the web/admin).
func TestNEMAOptions_CoverSerializer(t *testing.T) {
	want := []string{
		"5-15R", "5-20R", "6-15R", "6-20R",
		"L5-15R", "L5-20R", "L5-30R", "L6-20R", "L6-30R",
		"14-30R", "14-50R",
		"C13", "C14", "C19", "C20",
		"USB", "OTHER",
	}
	for _, code := range want {
		if selectHasValue(nemaOutletTypeOptions, code) < 0 {
			t.Errorf("nemaOutletTypeOptions missing NEMA code %q", code)
		}
	}
	// The legacy lowercase alias must NOT be offered (write path uses canonical OTHER).
	if selectHasValue(nemaOutletTypeOptions, "other") >= 0 {
		t.Errorf("legacy lowercase 'other' should not be an offered option")
	}
}

// TestDisconnectTypeOptions_CoverSerializer pins the disconnect_type choice set.
func TestDisconnectTypeOptions_CoverSerializer(t *testing.T) {
	for _, code := range []string{"fused", "unfused", "toggle", "integral", "none"} {
		if selectHasValue(disconnectTypeOptions, code) < 0 {
			t.Errorf("disconnectTypeOptions missing %q", code)
		}
	}
}

func selectHasValue(opts []selectOption, value string) int {
	for i, o := range opts {
		if o.value == value {
			return i
		}
	}
	return -1
}

// --- PowerOutletFormScreen -------------------------------------------------

func TestOutletForm_BuildPayload(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0)
	cir := 5
	loc := 3
	disc := 8
	s.circuitID = &cir
	s.locationID = &loc
	s.disconnectID = &disc
	s.outletTypeIdx = selectIndexOf(nemaOutletTypeOptions, "L6-30R")
	s.statusIdx = selectIndexOf(outletStatusOptions, "capped")
	s.inputs[poLabel].SetValue("weld-1")
	s.inputs[poLocationDesc].SetValue("east wall")
	s.inputs[poNotes].SetValue("240V welder")
	s.needsReview = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Circuit != 5 || w.Location != 3 {
		t.Errorf("circuit/location = %d/%d", w.Circuit, w.Location)
	}
	if w.Disconnect == nil || *w.Disconnect != 8 {
		t.Errorf("disconnect = %v", w.Disconnect)
	}
	if w.OutletType != "L6-30R" || w.Status != "capped" {
		t.Errorf("type/status = %q/%q", w.OutletType, w.Status)
	}
	if w.Label != "weld-1" || w.LocationDescription != "east wall" {
		t.Errorf("label/desc = %q/%q", w.Label, w.LocationDescription)
	}
	if !w.NeedsReview {
		t.Errorf("needs_review should be true")
	}
}

func TestOutletForm_Validation(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0)
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: circuit required")
	}
	cir := 5
	s.circuitID = &cir
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: location required")
	}
	loc := 3
	s.locationID = &loc
	// defaults: outlet_type 5-15R, status active, disconnect nil → valid
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("default outlet should validate: %v", err)
	}
	if w.OutletType != "5-15R" || w.Status != "active" {
		t.Errorf("defaults type/status = %q/%q, want 5-15R/active", w.OutletType, w.Status)
	}
	if w.Disconnect != nil {
		t.Errorf("disconnect should default nil, got %v", *w.Disconnect)
	}
}

func TestOutletForm_PresetCircuit(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 0, 7, 2)
	if s.circuitID == nil || *s.circuitID != 7 {
		t.Errorf("preset circuit should pre-select, got %v", s.circuitID)
	}
	// edit mode ignores the preset (record hydration owns the circuit).
	e := NewPowerOutletFormScreen(Deps{}, 11, 7, 2)
	if e.circuitID != nil {
		t.Errorf("edit mode should not pre-select from preset, got %v", e.circuitID)
	}
}

func TestOutletForm_Hydrate(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 11, 0, 0)
	disc := 8
	s.outlet = &omsapi.PowerOutletDetail{
		ID:                  11,
		Circuit:             5,
		Location:            3,
		Disconnect:          &disc,
		OutletType:          "14-50R",
		Label:               "range-1",
		LocationDescription: "kitchen",
		Status:              "inactive",
		Notes:               "n",
		NeedsReview:         true,
	}
	s.hydrate()
	if s.circuitID == nil || *s.circuitID != 5 || s.locationID == nil || *s.locationID != 3 {
		t.Errorf("circuit/location hydrate: %v/%v", s.circuitID, s.locationID)
	}
	if s.disconnectID == nil || *s.disconnectID != 8 {
		t.Errorf("disconnect hydrate = %v", s.disconnectID)
	}
	if nemaOutletTypeOptions[s.outletTypeIdx].value != "14-50R" {
		t.Errorf("outlet_type idx = %q", nemaOutletTypeOptions[s.outletTypeIdx].value)
	}
	if outletStatusOptions[s.statusIdx].value != "inactive" {
		t.Errorf("status idx = %q", outletStatusOptions[s.statusIdx].value)
	}
	if s.inputs[poLabel].Value() != "range-1" || !s.needsReview {
		t.Errorf("label/needs_review hydrate: %q/%v", s.inputs[poLabel].Value(), s.needsReview)
	}

	// A null disconnect hydrates to a nil pointer (clears cleanly on edit).
	s.outlet.Disconnect = nil
	s.hydrate()
	if s.disconnectID != nil {
		t.Errorf("nil disconnect should hydrate to nil, got %v", *s.disconnectID)
	}
}

// TestOutletForm_HydrateLegacyOtherType guards the silent-corruption case codex
// flagged: an outlet stored with the backend's legacy lowercase "other" code must
// hydrate onto the canonical "OTHER" option (not the index-0 "5-15R" default) so a
// re-save doesn't rewrite its type to a 15A receptacle.
func TestOutletForm_HydrateLegacyOtherType(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 11, 0, 0)
	s.outlet = &omsapi.PowerOutletDetail{ID: 11, Circuit: 5, Location: 3, OutletType: "other", Status: "active"}
	s.hydrate()
	if got := nemaOutletTypeOptions[s.outletTypeIdx].value; got != "OTHER" {
		t.Fatalf("legacy 'other' should hydrate to canonical OTHER, got %q", got)
	}
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.OutletType != "OTHER" {
		t.Errorf("re-save of legacy-other outlet should send OTHER, got %q (silent type corruption)", w.OutletType)
	}
}

func TestOutletForm_PickerClearRows(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0)
	s.circuits = []omsapi.PowerCircuitDetail{{ID: 5, Label: "north", PanelName: "Main"}}
	s.locations = []omsapi.Location{{ID: 3, Name: "Shop"}}
	s.disconnects = []omsapi.DisconnectDetail{{ID: 8, Label: "welder disc", DisconnectType: "fused"}}

	// disconnect is optional → offers a clear row.
	s.pickField = poDisconnect
	s.applyPickFilter()
	if !pickHasClear(s.pickOptions) {
		t.Errorf("disconnect picker should offer a (none) clear row")
	}
	// circuit + location are required → no clear row.
	s.pickField = poCircuit
	s.applyPickFilter()
	if pickHasClear(s.pickOptions) {
		t.Errorf("required circuit picker must NOT offer a clear row")
	}
	s.pickField = poLocation
	s.applyPickFilter()
	if pickHasClear(s.pickOptions) {
		t.Errorf("required location picker must NOT offer a clear row")
	}
}

func TestOutletForm_RenderSmoke(t *testing.T) {
	s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0)
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Outlet type") {
		t.Errorf("outlet form view missing Outlet type: %q", out)
	}
	// disconnect picker renders its (none) row; the required circuit picker doesn't.
	s.disconnects = []omsapi.DisconnectDetail{{ID: 8, Label: "d", DisconnectType: "fused"}}
	s.openPicker(poDisconnect)
	if out := s.View(); !strings.Contains(out, "none") {
		t.Errorf("disconnect picker should show a (none) row: %q", out)
	}
	s.phase = elecPhaseForm
	s.circuits = []omsapi.PowerCircuitDetail{{ID: 5, Label: "north", PanelName: "Main"}}
	s.openPicker(poCircuit)
	if out := s.View(); strings.Contains(out, "(none") {
		t.Errorf("required circuit picker should NOT show a (none) row: %q", out)
	}
}

// --- DisconnectFormScreen --------------------------------------------------

func TestDisconnectForm_BuildPayload(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	cir := 5
	loc := 4
	s.circuitID = &cir
	s.locationID = &loc
	s.inputs[dcLabel].SetValue("dust collector disc")
	s.disconnectTypIdx = selectIndexOf(disconnectTypeOptions, "fused")
	s.inputs[dcAmperage].SetValue("60")
	s.inputs[dcFuseSize].SetValue("30A class J")
	s.isLockable = true
	s.lotoDeviceIDs = []int{9, 7}
	s.needsReview = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Circuit != 5 || w.Location == nil || *w.Location != 4 {
		t.Errorf("circuit/location = %d/%v", w.Circuit, w.Location)
	}
	if w.Label != "dust collector disc" || w.DisconnectType != "fused" {
		t.Errorf("label/type = %q/%q", w.Label, w.DisconnectType)
	}
	if w.Amperage == nil || *w.Amperage != 60 {
		t.Errorf("amperage = %v", w.Amperage)
	}
	if w.FuseSize != "30A class J" || !w.IsLockable || !w.NeedsReview {
		t.Errorf("fuse/lock/review = %q/%v/%v", w.FuseSize, w.IsLockable, w.NeedsReview)
	}
	// required_loto_device_ids ride as the selected set (order-independent).
	if len(w.RequiredLOTODeviceIDs) != 2 || !intSliceHas(w.RequiredLOTODeviceIDs, 7) || !intSliceHas(w.RequiredLOTODeviceIDs, 9) {
		t.Errorf("required_loto_device_ids = %v, want {7,9}", w.RequiredLOTODeviceIDs)
	}
}

func TestDisconnectForm_Validation(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: circuit required")
	}
	cir := 5
	s.circuitID = &cir
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: label required")
	}
	s.inputs[dcLabel].SetValue("d")
	// valid now (amperage/location optional, lockable defaults true, empty loto ok)
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("minimal disconnect should validate: %v", err)
	}
	if w.Location != nil {
		t.Errorf("blank location should be nil, got %v", *w.Location)
	}
	if w.Amperage != nil {
		t.Errorf("blank amperage should be nil, got %v", *w.Amperage)
	}
	// empty selection still marshals as a non-nil slice.
	if w.RequiredLOTODeviceIDs == nil {
		t.Errorf("required_loto_device_ids should be non-nil (empty array), got nil")
	}
	// bad amperage → error
	s.inputs[dcAmperage].SetValue("-3")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for non-positive amperage")
	}
}

func TestDisconnectForm_IsLockableDefault(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	if !s.isLockable {
		t.Errorf("create mode should default is_lockable=true (model default)")
	}
}

// TestDisconnectForm_LOTOMultiPicker drives the required_loto_devices multi-picker:
// opening it, toggling devices in/out with the cursor, and confirming the selection
// flows into the payload as required_loto_device_ids.
func TestDisconnectForm_LOTOMultiPicker(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	cir := 5
	s.circuitID = &cir
	s.inputs[dcLabel].SetValue("d")
	s.lotoDevices = []omsapi.LOTODevice{
		{ID: 7, DeviceType: "breaker_lock", DeviceTypeDisplay: "Breaker lock", Label: "BL-1", Status: "available"},
		{ID: 9, DeviceType: "padlock", DeviceTypeDisplay: "Padlock", Label: "PAD-2", Status: "available"},
	}
	s.openPicker(dcLOTODevices)
	if len(s.pickOptions) != 2 {
		t.Fatalf("multi-picker should list 2 devices, got %d", len(s.pickOptions))
	}
	// toggle the first device on
	s.pickCursor = 0
	s.toggleCurrentLOTO()
	if !s.lotoSelected(7) {
		t.Errorf("device 7 should be selected after toggle")
	}
	// toggle the second on, then the first back off
	s.pickCursor = 1
	s.toggleCurrentLOTO()
	s.pickCursor = 0
	s.toggleCurrentLOTO()
	if s.lotoSelected(7) {
		t.Errorf("device 7 should be de-selected after second toggle")
	}
	if !s.lotoSelected(9) {
		t.Errorf("device 9 should remain selected")
	}

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if len(w.RequiredLOTODeviceIDs) != 1 || w.RequiredLOTODeviceIDs[0] != 9 {
		t.Errorf("required_loto_device_ids = %v, want {9}", w.RequiredLOTODeviceIDs)
	}
}

func TestDisconnectForm_Hydrate(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 21, 0, 0)
	loc := 4
	amp := 60
	s.disconnect = &omsapi.DisconnectDetail{
		ID:             21,
		Circuit:        5,
		Location:       &loc,
		Label:          "RTU disc",
		DisconnectType: "unfused",
		Amperage:       &amp,
		FuseSize:       "",
		IsLockable:     false,
		Notes:          "n",
		NeedsReview:    true,
		RequiredLOTODevices: []omsapi.LOTODevice{
			{ID: 7, Label: "BL-1"},
			{ID: 9, Label: "PAD-2"},
		},
	}
	s.hydrate()
	if s.circuitID == nil || *s.circuitID != 5 {
		t.Errorf("circuit hydrate = %v", s.circuitID)
	}
	if s.locationID == nil || *s.locationID != 4 {
		t.Errorf("location hydrate = %v", s.locationID)
	}
	if disconnectTypeOptions[s.disconnectTypIdx].value != "unfused" {
		t.Errorf("type idx = %q", disconnectTypeOptions[s.disconnectTypIdx].value)
	}
	if s.inputs[dcAmperage].Value() != "60" {
		t.Errorf("amperage hydrate = %q", s.inputs[dcAmperage].Value())
	}
	if s.isLockable {
		t.Errorf("is_lockable should hydrate false")
	}
	if !s.needsReview {
		t.Errorf("needs_review should hydrate true")
	}
	if !s.lotoSelected(7) || !s.lotoSelected(9) || len(s.lotoDeviceIDs) != 2 {
		t.Errorf("loto ids should hydrate from embedded devices: %v", s.lotoDeviceIDs)
	}

	// A null location hydrates to nil.
	s.disconnect.Location = nil
	s.hydrate()
	if s.locationID != nil {
		t.Errorf("nil location should hydrate to nil, got %v", *s.locationID)
	}
}

func TestDisconnectForm_LocationClearRow(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	s.locations = []omsapi.Location{{ID: 4, Name: "Shop"}}
	s.circuits = []omsapi.PowerCircuitDetail{{ID: 5, Label: "north"}}
	// location optional → clear row; circuit required → none.
	s.pickField = dcLocation
	s.applyPickFilter()
	if !pickHasClear(s.pickOptions) {
		t.Errorf("optional location picker should offer a clear row")
	}
	s.pickField = dcCircuit
	s.applyPickFilter()
	if pickHasClear(s.pickOptions) {
		t.Errorf("required circuit picker must NOT offer a clear row")
	}
}

func TestDisconnectForm_RenderSmoke(t *testing.T) {
	s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Disconnect type") {
		t.Errorf("disconnect form view missing Disconnect type: %q", out)
	}
	// multi-picker view shows the checkbox prefixes.
	s.lotoDevices = []omsapi.LOTODevice{{ID: 7, DeviceTypeDisplay: "Breaker lock", Label: "BL-1", Status: "available"}}
	s.lotoDeviceIDs = []int{7}
	s.openPicker(dcLOTODevices)
	out := s.View()
	if !strings.Contains(out, "[x]") {
		t.Errorf("multi-pick view should mark the selected device with [x]: %q", out)
	}
	if !strings.Contains(out, "BL-1") {
		t.Errorf("multi-pick view should list the device: %q", out)
	}
}

// --- manage screens --------------------------------------------------------

func TestCircuitManageScreens_HandlesKey(t *testing.T) {
	co := NewCircuitOutletsScreen(Deps{}, 5, 2, "north")
	cd := NewCircuitDisconnectsScreen(Deps{}, 5, 2, "north")
	for _, k := range []string{"n", "G"} {
		if !co.HandlesKey(k) {
			t.Errorf("CircuitOutletsScreen should claim %q", k)
		}
		if !cd.HandlesKey(k) {
			t.Errorf("CircuitDisconnectsScreen should claim %q", k)
		}
	}
	for _, k := range []string{"j", "x", "E", "enter", "o", "d"} {
		if co.HandlesKey(k) {
			t.Errorf("CircuitOutletsScreen should NOT claim %q", k)
		}
	}
	// raw input only while confirming delete
	if co.WantsRawInput() {
		t.Errorf("CircuitOutletsScreen should not grab raw input outside confirm")
	}
	co.confirmingDelete = true
	if !co.WantsRawInput() {
		t.Errorf("CircuitOutletsScreen should grab raw input while confirming")
	}

	// BreakerCircuits now claims the o/d drill keys (o collides with the global
	// operational-modes hotkey, so the claim is load-bearing).
	bc := NewBreakerCircuitsScreen(Deps{}, 2, 1, "pos 3")
	for _, k := range []string{"n", "G", "o", "d"} {
		if !bc.HandlesKey(k) {
			t.Errorf("BreakerCircuitsScreen should claim %q", k)
		}
	}
}

func TestCircuitManageScreens_RenderSmoke(t *testing.T) {
	co := NewCircuitOutletsScreen(Deps{}, 5, 2, "north")
	co.loading = false
	co.terminalHeight = 30
	co.rows = []omsapi.PowerOutletDetail{
		{ID: 1, Label: "NW-1", OutletType: "5-15R", LocationName: "Shop", Status: "capped", NeedsReview: true},
	}
	if out := co.View(); !strings.Contains(out, "NW-1") || !strings.Contains(out, "capped") {
		t.Errorf("outlet row render: %q", out)
	}
	co.confirmingDelete = true
	if out := co.View(); !strings.Contains(out, "Delete outlet") {
		t.Errorf("outlet confirm render: %q", out)
	}

	cd := NewCircuitDisconnectsScreen(Deps{}, 5, 2, "north")
	cd.loading = false
	cd.terminalHeight = 30
	amp := 60
	cd.rows = []omsapi.DisconnectDetail{
		{ID: 1, Label: "dc disc", DisconnectType: "fused", Amperage: &amp, IsLockable: true,
			RequiredLOTODevices: []omsapi.LOTODevice{{ID: 7, Label: "BL-1"}}, NeedsReview: true},
	}
	if out := cd.View(); !strings.Contains(out, "dc disc") || !strings.Contains(out, "1 LOTO") {
		t.Errorf("disconnect row render: %q", out)
	}
	cd.confirmingDelete = true
	if out := cd.View(); !strings.Contains(out, "unlinked") {
		t.Errorf("disconnect confirm should warn about unlinking: %q", out)
	}
}

// --- small test helpers ----------------------------------------------------

func pickHasClear(opts []itemPickOption) bool {
	for _, o := range opts {
		if o.clear {
			return true
		}
	}
	return false
}

func intSliceHas(s []int, v int) bool {
	for _, n := range s {
		if n == v {
			return true
		}
	}
	return false
}
