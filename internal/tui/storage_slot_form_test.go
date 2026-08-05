package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func slotFormKey(t *testing.T, s *StorageSlotFormScreen, key string) *StorageSlotFormScreen {
	t.Helper()
	next, _ := s.Update(namedKey(key))
	out, ok := next.(*StorageSlotFormScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *StorageSlotFormScreen", next)
	}
	return out
}

func readySlotForm() *StorageSlotFormScreen {
	s := NewStorageSlotFormScreen(Deps{}, "")
	s.terminalHeight = 40
	next, _ := s.Update(storageSlotFormLoadedMsg{sigs: []omsapi.SIG{{ID: 3, Name: "Woodshop"}, {ID: 4, Name: "Metal"}}})
	return next.(*StorageSlotFormScreen)
}

// TestStorageSlotForm_BuildPayload pins the writable set and the local
// validation that keeps a typo from becoming a 400.
func TestStorageSlotForm_BuildPayload(t *testing.T) {
	s := readySlotForm()
	s.inputs[ssfRack].SetValue(" 1 ")
	s.inputs[ssfLevel].SetValue("a")
	s.inputs[ssfPosition].SetValue("12")
	s.inputs[ssfNotes].SetValue("  by the door  ")
	s.palletJack = true

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Rack != 1 || w.Position != 12 {
		t.Errorf("components = %d/%d, want 1/12", w.Rack, w.Position)
	}
	if w.Level != "A" {
		t.Errorf("level must be upper-cased before the unique-together check, got %q", w.Level)
	}
	if w.Notes != "by the door" {
		t.Errorf("notes = %q, want trimmed", w.Notes)
	}
	if !w.RequiresPalletJack || !w.IsActive {
		t.Errorf("flags wrong: %+v", w)
	}
	if w.OwningGroup != nil {
		t.Errorf("an unset SIG must stay nil so it serializes as null")
	}
}

// TestStorageSlotForm_Validation covers each backend constraint restated
// locally: rack/position are required positive integers and the level is one
// letter.
func TestStorageSlotForm_Validation(t *testing.T) {
	s := readySlotForm()
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected an error with no components set")
	}

	s.inputs[ssfRack].SetValue("1")
	s.inputs[ssfPosition].SetValue("1")
	// The level field mirrors the model's CharField(max_length=1), so a second
	// character can't even be typed; the validator covers the rest.
	if s.inputs[ssfLevel].CharLimit != 1 {
		t.Errorf("level CharLimit = %d, want 1 (model max_length)", s.inputs[ssfLevel].CharLimit)
	}
	for _, bad := range []string{"", "1", "-"} {
		s.inputs[ssfLevel].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("level %q should be rejected", bad)
		}
	}
	s.inputs[ssfLevel].SetValue("A")

	for _, bad := range []string{"0", "-1", "x", ""} {
		s.inputs[ssfRack].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("rack %q should be rejected", bad)
		}
	}
	s.inputs[ssfRack].SetValue("1")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("a valid form should pass: %v", err)
	}
}

// TestStorageSlotForm_CodePreview — the components ARE the identity and the
// code follows, so the operator sees what they are about to create (or rename)
// before saving.
func TestStorageSlotForm_CodePreview(t *testing.T) {
	s := readySlotForm()
	if got := s.currentCodePreview(); got != "—" {
		t.Errorf("an empty form has no code yet, got %q", got)
	}
	s.inputs[ssfRack].SetValue("1")
	s.inputs[ssfLevel].SetValue("b")
	s.inputs[ssfPosition].SetValue("3")
	if got := s.currentCodePreview(); got != "1B3" {
		t.Errorf("code preview = %q, want 1B3", got)
	}
}

// TestStorageSlotForm_OwningGroupGraft is the sc-1or/sc-gfg2 picker rule: the
// SIG list is one page and can fail outright, so a slot whose owner isn't in it
// must still be shown — otherwise an edit aimed at another field would rest on
// "(none)" and silently un-reserve the slot.
func TestStorageSlotForm_OwningGroupGraft(t *testing.T) {
	sigs := []omsapi.SIG{{ID: 3, Name: "Woodshop"}}
	missing := 9
	rows := storageSlotGroupRows(sigs, &missing, "Ceramics")
	if !rows[0].clear {
		t.Errorf("row 0 must be the explicit clear row so detaching is first-class")
	}
	var found bool
	for _, r := range rows {
		if r.key == "9" && strings.Contains(r.label, "Ceramics") {
			found = true
		}
	}
	if !found {
		t.Errorf("the current owner must be grafted in when absent from the list: %+v", rows)
	}

	// Present in the list → no duplicate row.
	present := 3
	rows = storageSlotGroupRows(sigs, &present, "Woodshop")
	count := 0
	for _, r := range rows {
		if r.key == "3" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("a listed owner must not be duplicated, got %d rows", count)
	}
}

// TestStorageSlotForm_GroupNameFromList — the display name is resolved from the
// loaded SIGs, not by trimming the grafted row's " (current)" suffix, which
// would mangle a SIG genuinely named that way.
func TestStorageSlotForm_GroupNameFromList(t *testing.T) {
	sigs := []omsapi.SIG{{ID: 3, Name: "Woodshop (current)"}}
	three := 3
	if got := storageSlotGroupName(sigs, &three, nil, ""); got != "Woodshop (current)" {
		t.Errorf("name = %q, want the SIG's real name", got)
	}
	nine := 9
	if got := storageSlotGroupName(sigs, &nine, &nine, "Ceramics"); got != "Ceramics" {
		t.Errorf("a grafted pick should keep the name the slot reported, got %q", got)
	}
	if got := storageSlotGroupName(sigs, nil, &nine, "Ceramics"); got != "" {
		t.Errorf("clearing should clear the name, got %q", got)
	}
}

// TestStorageSlotForm_CreateDoesNotWaitOnSigs — a slot saves fine without an
// owner, so a slow (or forbidden) SIG list must not hold up typing.
func TestStorageSlotForm_CreateDoesNotWaitOnSigs(t *testing.T) {
	s := NewStorageSlotFormScreen(Deps{}, "")
	s.terminalHeight = 40
	if s.loading {
		t.Errorf("create mode has nothing to fetch and must render immediately")
	}
	if !strings.Contains(s.View(), "Rack") {
		t.Errorf("the form should render before the SIG list arrives:\n%s", s.View())
	}
	if !NewStorageSlotFormScreen(Deps{}, "1A1").loading {
		t.Errorf("edit mode does have to wait for the slot")
	}
}

// TestStorageSlotForm_PickerParksOnCurrent — re-opening the picker and pressing
// enter must be a no-op confirm, not a silent reset to "not reserved".
func TestStorageSlotForm_PickerParksOnCurrent(t *testing.T) {
	s := readySlotForm()
	id := 4
	s.owningGroupID = &id
	s.owningGroupName = "Metal"
	s.openPicker()
	if s.pickRows[s.pickCursor].key != "4" {
		t.Fatalf("cursor parked on %+v, want the current pick", s.pickRows[s.pickCursor])
	}
	s = slotFormKey(t, s, "enter")
	if s.owningGroupID == nil || *s.owningGroupID != 4 {
		t.Errorf("enter on the current pick should keep it, got %v", s.owningGroupID)
	}

	// Row 0 detaches.
	s.openPicker()
	s.pickCursor = 0
	s = slotFormKey(t, s, "enter")
	if s.owningGroupID != nil || s.owningGroupName != "" {
		t.Errorf("the clear row should detach the SIG, got %v/%q", s.owningGroupID, s.owningGroupName)
	}
}

// TestStorageSlotForm_PickerEscKeeps — esc means "done looking", not "clear".
func TestStorageSlotForm_PickerEscKeeps(t *testing.T) {
	s := readySlotForm()
	id := 3
	s.owningGroupID = &id
	s.openPicker()
	s.pickCursor = 0 // sitting on the clear row
	s = slotFormKey(t, s, "esc")
	if s.owningGroupID == nil || *s.owningGroupID != 3 {
		t.Errorf("esc must keep the current pick, got %v", s.owningGroupID)
	}
	if s.phase != slotPhaseForm {
		t.Errorf("esc should return to the form")
	}
}

// TestStorageSlotForm_HydrateEdit round-trips a fetched slot into the fields.
func TestStorageSlotForm_HydrateEdit(t *testing.T) {
	s := NewStorageSlotFormScreen(Deps{}, "1A1")
	group := 3
	next, _ := s.Update(storageSlotFormLoadedMsg{
		sigs: []omsapi.SIG{{ID: 3, Name: "Woodshop"}},
		slot: &omsapi.StorageSlot{
			ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1,
			RequiresPalletJack: true, IsActive: false,
			OwningGroup: &group, OwningGroupName: "Woodshop", Notes: "by the door",
		},
	})
	s = next.(*StorageSlotFormScreen)
	if s.inputs[ssfRack].Value() != "1" || s.inputs[ssfLevel].Value() != "A" || s.inputs[ssfPosition].Value() != "1" {
		t.Errorf("components not hydrated: %q/%q/%q",
			s.inputs[ssfRack].Value(), s.inputs[ssfLevel].Value(), s.inputs[ssfPosition].Value())
	}
	if !s.palletJack || s.isActive {
		t.Errorf("flags not hydrated: jack=%v active=%v", s.palletJack, s.isActive)
	}
	if s.owningGroupID == nil || *s.owningGroupID != 3 || s.owningGroupName != "Woodshop" {
		t.Errorf("owner not hydrated: %v/%q", s.owningGroupID, s.owningGroupName)
	}
	if s.inputs[ssfNotes].Value() != "by the door" {
		t.Errorf("notes not hydrated: %q", s.inputs[ssfNotes].Value())
	}

	// A round-trip with nothing touched must re-send the same values.
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Rack != 1 || w.Level != "A" || w.Position != 1 || !w.RequiresPalletJack || w.IsActive {
		t.Errorf("untouched round-trip changed the payload: %+v", w)
	}
}

// TestStorageSlotForm_SavedCodeFollowsServer — a rack/level/position edit
// renames the slot, so navigating to the OLD code afterwards would 404 on a
// save that worked.
func TestStorageSlotForm_SavedCodeFollowsServer(t *testing.T) {
	if got := storageSlotSavedCode(&omsapi.StorageSlot{Code: "2B3"}, "1A1"); got != "2B3" {
		t.Errorf("got %q, want the server's recomputed code", got)
	}
	if got := storageSlotSavedCode(nil, "1A1"); got != "1A1" {
		t.Errorf("got %q, want the fallback when nothing came back", got)
	}
}

// TestStorageSlotForm_CreateReportsMarker — creating allocates a permanent
// AprilTag, which the operator now has to print, so the confirmation names it
// (and says so when the family was exhausted).
func TestStorageSlotForm_CreateReportsMarker(t *testing.T) {
	tag := 41
	text := storageSlotSavedText(&omsapi.StorageSlot{Code: "1A1", AprilTagID: &tag}, true)
	if !strings.Contains(text, "41") || !strings.Contains(text, "1A1") {
		t.Errorf("create confirmation should name the slot and its marker, got %q", text)
	}
	text = storageSlotSavedText(&omsapi.StorageSlot{Code: "1A1"}, true)
	if !strings.Contains(text, "no AprilTag") {
		t.Errorf("a tagless create must say so, got %q", text)
	}
	text = storageSlotSavedText(&omsapi.StorageSlot{Code: "1A1", AprilTagID: &tag}, false)
	if strings.Contains(text, "41") {
		t.Errorf("an update does not allocate a marker and should not imply it did, got %q", text)
	}
}

// TestStorageSlotForm_RenderSmoke walks the two render paths.
func TestStorageSlotForm_RenderSmoke(t *testing.T) {
	s := readySlotForm()
	out := s.View()
	for _, want := range []string{"Rack", "Level", "Position", "Needs pallet jack", "Reserved for"} {
		if !strings.Contains(out, want) {
			t.Errorf("form view missing %q:\n%s", want, out)
		}
	}
	s.cursor = len(s.fields) - 2 // the picker row
	s = slotFormKey(t, s, "ctrl+e")
	if s.phase != slotPhasePick {
		t.Fatalf("ctrl+e on the picker row should open the SIG list")
	}
	if out := s.View(); !strings.Contains(out, "Woodshop") {
		t.Errorf("picker view missing the SIG list:\n%s", out)
	}
}

// TestStorageSlotForm_SigFailureIsNotFatal — the SIG list is a separate
// permission surface; losing it must not block editing the slot itself, and it
// must be reported rather than looking like "there are no SIGs".
func TestStorageSlotForm_SigFailureIsNotFatal(t *testing.T) {
	s := NewStorageSlotFormScreen(Deps{}, "")
	s.terminalHeight = 40
	next, _ := s.Update(storageSlotFormLoadedMsg{sigsErr: errStub("403 forbidden")})
	s = next.(*StorageSlotFormScreen)
	if s.loadErr != "" {
		t.Errorf("a SIG failure must not become a form load error")
	}
	if !strings.Contains(s.View(), "SIG list unavailable") {
		t.Errorf("the failure should be reported beside the form:\n%s", s.View())
	}
	s.inputs[ssfRack].SetValue("1")
	s.inputs[ssfLevel].SetValue("A")
	s.inputs[ssfPosition].SetValue("1")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("the slot should still be savable without the SIG list: %v", err)
	}
}
