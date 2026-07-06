package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestComputeBreakerPhase pins the Go replica of the web computePhase helper —
// a wrong phase silently mislabels a breaker, so the algorithm is exercised
// across every panel configuration, multi-pole spans, and the bail cases.
func TestComputeBreakerPhase(t *testing.T) {
	cases := []struct {
		name   string
		pos    string
		poles  int
		config string
		want   string
		ok     bool
	}{
		{"single always A", "3", 1, "single", "A", true},
		{"single 3-pole still A", "7", 3, "single", "A", true},
		{"split slot1 row0 A", "1", 1, "split", "A", true},
		{"split slot3 row1 B", "3", 1, "split", "B", true},
		{"split 2-pole AB", "1", 2, "split", "AB", true},
		{"split 3-pole wraps to AB", "1", 3, "split", "AB", true},
		{"three slot1 A", "1", 1, "three", "A", true},
		{"three slot3 B", "3", 1, "three", "B", true},
		{"three slot5 C", "5", 1, "three", "C", true},
		{"three 3-pole ABC", "1", 3, "three", "ABC", true},
		{"three 2-pole from row1 BC", "3", 2, "three", "BC", true},
		{"three 2-pole wraps C then A canonical AC", "5", 2, "three", "AC", true},
		{"even slot shares row with odd", "2", 1, "three", "A", true},
		{"tandem bail", "14/16", 1, "three", "", false},
		{"hyphen bail", "1-3", 1, "three", "", false},
		{"blank bail", "", 1, "three", "", false},
		{"non-numeric bail", "abc", 1, "three", "", false},
		{"zero bail", "0", 1, "three", "", false},
		{"pole clamps high", "1", 9, "three", "ABC", true},
		{"pole clamps low", "1", 0, "three", "A", true},
	}
	for _, c := range cases {
		got, ok := computeBreakerPhase(c.pos, c.poles, c.config)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: computeBreakerPhase(%q,%d,%q) = (%q,%v), want (%q,%v)",
				c.name, c.pos, c.poles, c.config, got, ok, c.want, c.ok)
		}
	}
}

// --- PowerPanelFormScreen ---

func TestPanelForm_BuildPayload(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 0)
	s.inputs[ppName].SetValue("Main Distribution")
	loc := 3
	s.locationID = &loc
	s.phaseCfgIdx = selectIndexOf(panelPhaseConfigOptions, "three")
	s.inputs[ppVoltage].SetValue("208")
	s.inputs[ppMainAmp].SetValue("400")
	s.breakerTypIdx = selectIndexOf(panelBreakerTypeOptions, "SQUARE_D_QO")
	s.numberingIdx = selectIndexOf(panelNumberingOptions, "bottom_up")
	s.inputs[ppInstallDate].SetValue("2026-01-15")
	s.needsReview = true
	fed := 12
	s.fedByID = &fed

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Main Distribution" || w.Location != 3 {
		t.Errorf("name/location = %q/%d", w.Name, w.Location)
	}
	if w.PhaseConfiguration != "three" {
		t.Errorf("phase_configuration = %q", w.PhaseConfiguration)
	}
	if w.Voltage != 208 {
		t.Errorf("voltage = %d", w.Voltage)
	}
	if w.MainBreakerAmperage == nil || *w.MainBreakerAmperage != 400 {
		t.Errorf("main_breaker_amperage = %v", w.MainBreakerAmperage)
	}
	if w.BreakerType != "SQUARE_D_QO" {
		t.Errorf("breaker_type = %q", w.BreakerType)
	}
	if w.NumberingDirection != "bottom_up" {
		t.Errorf("numbering_direction = %q", w.NumberingDirection)
	}
	if w.InstallDate == nil || *w.InstallDate != "2026-01-15" {
		t.Errorf("install_date = %v", w.InstallDate)
	}
	if !w.NeedsReview {
		t.Errorf("needs_review should be true")
	}
	if w.FedBy == nil || *w.FedBy != 12 {
		t.Errorf("fed_by = %v", w.FedBy)
	}
}

func TestPanelForm_Validation(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 0)
	// name + location required
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[ppName].SetValue("P1")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for missing location")
	}
	loc := 1
	s.locationID = &loc

	// create default voltage is prefilled to 240 → valid
	if w, err := s.buildPayload(); err != nil {
		t.Errorf("default should validate: %v", err)
	} else if w.Voltage != 240 {
		t.Errorf("default voltage = %d, want 240", w.Voltage)
	}
	// clearing voltage → error
	s.inputs[ppVoltage].SetValue("")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty voltage")
	}
	s.inputs[ppVoltage].SetValue("240")

	// main amp optional → nil when blank
	if w, err := s.buildPayload(); err != nil || w.MainBreakerAmperage != nil {
		t.Errorf("blank main amp should be nil: %v %v", w.MainBreakerAmperage, err)
	}
	// bad main amp → error
	s.inputs[ppMainAmp].SetValue("abc")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for non-numeric main amp")
	}
	s.inputs[ppMainAmp].SetValue("")

	// bad install date → error
	s.inputs[ppInstallDate].SetValue("not-a-date")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for malformed install date")
	}
	s.inputs[ppInstallDate].SetValue("")
	// blank install date → nil
	if w, err := s.buildPayload(); err != nil || w.InstallDate != nil {
		t.Errorf("blank install date should be nil: %v %v", w.InstallDate, err)
	}
}

func TestPanelForm_HydrateAndFedByExclusion(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 42)
	amp := 200
	s.panel = &omsapi.PowerPanelDetail{
		ID:                  42,
		Name:                "Sub A",
		Location:            5,
		PhaseConfiguration:  "split",
		Voltage:             120,
		MainBreakerAmperage: &amp,
		BreakerType:         "EATON_BR",
		NumberingDirection:  "bottom_up",
		InstallDate:         "2025-03-03",
		NeedsReview:         true,
	}
	s.hydrate()
	if s.inputs[ppName].Value() != "Sub A" {
		t.Errorf("name = %q", s.inputs[ppName].Value())
	}
	if s.locationID == nil || *s.locationID != 5 {
		t.Errorf("locationID = %v", s.locationID)
	}
	if panelPhaseConfigOptions[s.phaseCfgIdx].value != "split" {
		t.Errorf("phase idx = %q", panelPhaseConfigOptions[s.phaseCfgIdx].value)
	}
	if s.inputs[ppVoltage].Value() != "120" || s.inputs[ppMainAmp].Value() != "200" {
		t.Errorf("voltage/main = %q/%q", s.inputs[ppVoltage].Value(), s.inputs[ppMainAmp].Value())
	}
	if !s.needsReview {
		t.Errorf("needs_review should hydrate true")
	}

	// fed_by picker must exclude this panel's own circuits (self-feed guard).
	s.circuits = []omsapi.PowerCircuitDetail{
		{ID: 100, PanelID: 42, BreakerLabel: "Sub A / pos 1"}, // own → excluded
		{ID: 200, PanelID: 9, BreakerLabel: "Main / pos 4"},   // other → offered
	}
	s.pickField = ppFedBy
	s.applyPickFilter()
	sawOwn, sawOther := false, false
	for _, o := range s.pickOptions {
		if o.id == 100 {
			sawOwn = true
		}
		if o.id == 200 {
			sawOther = true
		}
	}
	if sawOwn {
		t.Errorf("fed_by picker must exclude this panel's own circuit 100")
	}
	if !sawOther {
		t.Errorf("fed_by picker should offer other panels' circuit 200")
	}
}

func TestPanelForm_RenderSmoke(t *testing.T) {
	s := NewPowerPanelFormScreen(Deps{}, 0)
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Name") {
		t.Errorf("panel form view missing Name: %q", out)
	}
	// fed_by picker renders its (none) row; location picker (required) does not.
	s.openPicker(ppFedBy)
	if out := s.View(); !strings.Contains(out, "none") {
		t.Errorf("fed_by picker should show a (none) row: %q", out)
	}
	s.phase = elecPhaseForm
	s.openPicker(ppLocation)
	if out := s.View(); strings.Contains(out, "(none") {
		t.Errorf("required location picker should NOT show a (none) row: %q", out)
	}
}

// --- PowerBreakerFormScreen ---

func TestBreakerForm_BuildPayload(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	panel := 7
	s.panelID = &panel
	s.inputs[pbPosition].SetValue("14")
	s.poleCountIdx = selectIndexOf(breakerPoleCountOptions, "2")
	s.inputs[pbAmperage].SetValue("30")
	s.phaseIdx = selectIndexOf(breakerPhaseOptions, "AB")
	s.phaseManuallySet = true
	s.statusIdx = selectIndexOf(breakerStatusOptions, "spare")

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Panel != 7 || w.Position != "14" || w.Amperage != 30 {
		t.Errorf("panel/position/amperage = %d/%q/%d", w.Panel, w.Position, w.Amperage)
	}
	if w.PoleCount != 2 {
		t.Errorf("pole_count = %d (want int 2)", w.PoleCount)
	}
	if w.Phase != "AB" {
		t.Errorf("phase = %q", w.Phase)
	}
	if w.Status != "spare" {
		t.Errorf("status = %q", w.Status)
	}
	// not critical → category + note blank
	if w.IsCritical || w.CriticalCategory != "" || w.CriticalNote != "" {
		t.Errorf("non-critical breaker should send blank critical fields: %+v", w)
	}
}

func TestBreakerForm_Validation(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: panel required")
	}
	p := 1
	s.panelID = &p
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: position required")
	}
	s.inputs[pbPosition].SetValue("12")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: amperage required")
	}
	s.inputs[pbAmperage].SetValue("0")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: amperage must be >= 1")
	}
	s.inputs[pbAmperage].SetValue("20")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("valid breaker should pass: %v", err)
	}
}

// TestBreakerForm_CriticalPairing confirms the is_critical toggle reveals the
// category/note fields and that buildPayload never sends the backend an invalid
// is_critical XOR category combination.
func TestBreakerForm_CriticalPairing(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	p := 1
	s.panelID = &p
	s.inputs[pbPosition].SetValue("2")
	s.inputs[pbAmperage].SetValue("20")
	s.phaseManuallySet = true

	// category field hidden until is_critical is on
	if fieldsContain(s.fields, pbCriticalCategory) {
		t.Fatalf("critical category should be hidden before is_critical")
	}
	s.setCursorToField(pbIsCritical)
	s.flipToggle(pbIsCritical)
	if !fieldsContain(s.fields, pbCriticalCategory) || !fieldsContain(s.fields, pbCriticalNote) {
		t.Errorf("critical category+note should appear when is_critical is on")
	}
	s.critCatIdx = selectIndexOf(breakerCriticalCategoryOptions, "exit_sign")
	s.inputs[pbCriticalNote].SetValue("EXIT over door 3")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if !w.IsCritical || w.CriticalCategory != "exit_sign" || w.CriticalNote != "EXIT over door 3" {
		t.Errorf("critical payload = %+v", w)
	}

	// Toggle back off: category must be blanked even though critCatIdx still
	// points at a real code (else the backend 400s).
	s.flipToggle(pbIsCritical)
	w, err = s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.IsCritical || w.CriticalCategory != "" || w.CriticalNote != "" {
		t.Errorf("critical fields must blank when is_critical off: %+v", w)
	}
}

// TestBreakerForm_AutoPhase drives the create-mode auto-calc: picking a
// three-phase panel and setting position/pole derives the phase, and manually
// changing the phase pins it against further auto-calc.
func TestBreakerForm_AutoPhase(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	s.panels = []omsapi.PowerPanel{{ID: 7, Name: "3ph", PhaseConfiguration: "three"}}
	p := 7
	s.panelID = &p

	// slot 5, 1-pole, three-phase → row2 → C
	s.inputs[pbPosition].SetValue("5")
	s.maybeAutoPhase()
	if got := breakerPhaseOptions[s.phaseIdx].value; got != "C" {
		t.Errorf("auto phase for slot 5 three-phase = %q, want C", got)
	}
	// widen to 2-pole → rows 2,3 → C,A → canonical AC
	s.poleCountIdx = selectIndexOf(breakerPoleCountOptions, "2")
	s.maybeAutoPhase()
	if got := breakerPhaseOptions[s.phaseIdx].value; got != "AC" {
		t.Errorf("auto phase for slot 5 2-pole = %q, want AC", got)
	}
	// user pins phase → auto-calc no longer clobbers
	s.cycleSelect(pbPhase, +1) // sets phaseManuallySet
	pinned := breakerPhaseOptions[s.phaseIdx].value
	s.inputs[pbPosition].SetValue("1")
	s.maybeAutoPhase()
	if got := breakerPhaseOptions[s.phaseIdx].value; got != pinned {
		t.Errorf("phase should stay pinned at %q after manual set, got %q", pinned, got)
	}
}

func TestBreakerForm_EditModeLocksPhase(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 9, 0)
	if !s.phaseManuallySet {
		t.Errorf("edit mode should start with phase pinned so hydration isn't clobbered")
	}
	// create mode with a preset panel should NOT pin the phase
	c := NewPowerBreakerFormScreen(Deps{}, 0, 7)
	if c.phaseManuallySet {
		t.Errorf("create mode should leave phase auto-calc live")
	}
	if c.panelID == nil || *c.panelID != 7 {
		t.Errorf("preset panel should pre-select panelID, got %v", c.panelID)
	}
}

func TestBreakerForm_RenderSmoke(t *testing.T) {
	s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Amperage") {
		t.Errorf("breaker form view missing Amperage: %q", out)
	}
	s.setCursorToField(pbIsCritical)
	s.flipToggle(pbIsCritical)
	if out := s.View(); out == "" {
		t.Errorf("breaker form empty after enabling critical section")
	}
	s.panels = []omsapi.PowerPanel{{ID: 1, Name: "P1", LocationName: "Shop", PhaseConfiguration: "split"}}
	s.openPicker()
	if out := s.View(); !strings.Contains(out, "P1") {
		t.Errorf("panel picker should list P1: %q", out)
	}
}

// --- PowerCircuitFormScreen ---

func TestCircuitForm_BuildPayload(t *testing.T) {
	s := NewPowerCircuitFormScreen(Deps{}, 0, 0, 0)
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error: breaker required")
	}
	brk := 9
	s.breakerID = &brk
	s.inputs[pcLabel].SetValue("east bench")
	s.inputs[pcConductorSize].SetValue("10 AWG")
	s.inputs[pcConductorLength].SetValue("55")
	s.needsReview = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Breaker != 9 || w.Label != "east bench" || w.ConductorSize != "10 AWG" {
		t.Errorf("circuit payload = %+v", w)
	}
	if w.ConductorLengthFt == nil || *w.ConductorLengthFt != 55 {
		t.Errorf("conductor_length_ft = %v", w.ConductorLengthFt)
	}
	if w.MaxLoadAmps != nil {
		t.Errorf("max_load_amps must be nil (omitted → backend derate), got %v", *w.MaxLoadAmps)
	}
	if !w.NeedsReview {
		t.Errorf("needs_review should be true")
	}
	// bad length → error
	s.inputs[pcConductorLength].SetValue("-3")
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for negative conductor length")
	}
}

func TestCircuitForm_RenderSmoke(t *testing.T) {
	s := NewPowerCircuitFormScreen(Deps{}, 0, 5, 3)
	if s.breakerID == nil || *s.breakerID != 5 {
		t.Errorf("preset breaker should pre-select, got %v", s.breakerID)
	}
	s.loading = false
	s.terminalHeight = 30
	if out := s.View(); !strings.Contains(out, "Conductor size") {
		t.Errorf("circuit form view missing Conductor size: %q", out)
	}
	if out := s.View(); !strings.Contains(out, "max load auto-set") {
		t.Errorf("circuit form should note the max-load auto default: %q", out)
	}
}

// --- manage screens ---

func TestElectricalDeleteBlock(t *testing.T) {
	if got := electricalDeleteBlock("panel", 0, "breaker"); got != "" {
		t.Errorf("no children → empty, got %q", got)
	}
	if got := electricalDeleteBlock("panel", 1, "breaker"); !strings.Contains(got, "1 breaker") {
		t.Errorf("singular = %q", got)
	}
	if got := electricalDeleteBlock("breaker", 3, "circuit"); !strings.Contains(got, "3 circuits") {
		t.Errorf("plural = %q", got)
	}
}

func TestManageScreens_HandlesKey(t *testing.T) {
	pb := NewPanelBreakersScreen(Deps{}, 1, "P1")
	bc := NewBreakerCircuitsScreen(Deps{}, 2, 1, "pos 3")
	for _, k := range []string{"n", "G"} {
		if !pb.HandlesKey(k) {
			t.Errorf("PanelBreakersScreen should claim %q", k)
		}
		if !bc.HandlesKey(k) {
			t.Errorf("BreakerCircuitsScreen should claim %q", k)
		}
	}
	for _, k := range []string{"j", "x", "E", "enter"} {
		if pb.HandlesKey(k) {
			t.Errorf("PanelBreakersScreen should NOT claim non-colliding %q", k)
		}
	}
	// raw input only while confirming delete
	if pb.WantsRawInput() {
		t.Errorf("PanelBreakersScreen should not grab raw input outside confirm")
	}
	pb.confirmingDelete = true
	if !pb.WantsRawInput() {
		t.Errorf("PanelBreakersScreen should grab raw input while confirming")
	}
}

func TestManageScreens_RenderSmoke(t *testing.T) {
	pb := NewPanelBreakersScreen(Deps{}, 1, "P1")
	pb.loading = false
	pb.terminalHeight = 30
	pb.rows = []omsapi.PowerBreakerDetail{
		{ID: 1, Position: "12", Amperage: 20, Phase: "A", PoleCount: 1, CircuitCount: 2, IsCritical: true},
	}
	if out := pb.View(); !strings.Contains(out, "12") || !strings.Contains(out, "critical") {
		t.Errorf("breaker row render: %q", out)
	}
	pb.confirmingDelete = true
	if out := pb.View(); !strings.Contains(out, "blocked") {
		t.Errorf("confirm should warn about the 2 circuits blocking delete: %q", out)
	}

	bc := NewBreakerCircuitsScreen(Deps{}, 2, 1, "pos 3")
	bc.loading = false
	bc.terminalHeight = 30
	max := 16
	bc.rows = []omsapi.PowerCircuitDetail{
		{ID: 1, Label: "north wall", MaxLoadAmps: &max, OutletCount: 0, NeedsReview: true},
	}
	if out := bc.View(); !strings.Contains(out, "north wall") || !strings.Contains(out, "needs review") {
		t.Errorf("circuit row render: %q", out)
	}
}
