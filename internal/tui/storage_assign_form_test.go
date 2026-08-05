package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func assignKey(t *testing.T, s *StorageAssignFormScreen, key string) *StorageAssignFormScreen {
	t.Helper()
	next, _ := s.Update(namedKey(key))
	out, ok := next.(*StorageAssignFormScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *StorageAssignFormScreen", next)
	}
	return out
}

func loadedAssignForm(t *testing.T) *StorageAssignFormScreen {
	t.Helper()
	s := NewStorageAssignFormScreen(Deps{}, "1a1", nil)
	next, _ := s.Update(storageAssignSIGsLoadedMsg{sigs: []omsapi.SIG{
		{ID: 3, Name: "Welding SIG"},
		{ID: 4, Name: "Woodshop"},
	}})
	return next.(*StorageAssignFormScreen)
}

// The form is aimed at a canonical code, so a code typed at the rack in lower
// case addresses the same slot the printed card does.
func TestStorageAssignForm_NormalizesTheCode(t *testing.T) {
	if got := NewStorageAssignFormScreen(Deps{}, " 1a1 ", nil).code; got != "1A1" {
		t.Errorf("code = %q, want 1A1", got)
	}
}

// TestStorageAssignForm_TypeDrivesTheFields: only a committee has a group to
// point at. Offering the SIG picker for logistics/class would invite an
// occupant recorded in the wrong half of the model.
func TestStorageAssignForm_TypeDrivesTheFields(t *testing.T) {
	s := loadedAssignForm(t)
	if !s.isCommittee() {
		t.Fatalf("the form should start on committee, got %q", s.storageType())
	}
	if indexOfField(s.fields, safGroup) < 0 {
		t.Error("a committee assignment must offer the SIG picker")
	}

	s = assignKey(t, s, " ") // cycle to logistics
	if s.storageType() != omsapi.StorageAssignmentTypeLogistics {
		t.Fatalf("space should cycle the type, got %q", s.storageType())
	}
	if indexOfField(s.fields, safGroup) >= 0 {
		t.Error("logistics has no committee to point at — the picker should be hidden")
	}

	s = assignKey(t, s, " ")
	if s.storageType() != omsapi.StorageAssignmentTypeClass {
		t.Fatalf("type = %q, want class", s.storageType())
	}
	s = assignKey(t, s, " ")
	if !s.isCommittee() {
		t.Fatalf("the cycle should wrap back to committee, got %q", s.storageType())
	}
	// left cycles the other way.
	s = assignKey(t, s, "left")
	if s.storageType() != omsapi.StorageAssignmentTypeClass {
		t.Errorf("left should cycle backwards, got %q", s.storageType())
	}

	// Every option carries its grid letter, so the operator can see what the
	// rack will read afterwards — and the option STRIP under the focused row
	// puts all three on screen at once, so the set is never cycled blind.
	for s.fields[s.cursor] != safType {
		s = assignKey(t, s, "tab")
	}
	view := s.View()
	for _, want := range []string{"(C)", "(L)", "(E)"} {
		if !strings.Contains(view, want) {
			t.Errorf("the type options should show the grid letter %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, "Storage type") {
		t.Errorf("view is missing the type row:\n%s", view)
	}
	// The value between the brackets is the LABEL alone — elecSelectLabel wraps
	// its value in "‹ ›", which inside a jdeChoice would render "< ‹ … › >".
	if !strings.Contains(view, "< Class (E) >") {
		t.Errorf("the choice row should carry the bare label between its brackets:\n%s", view)
	}
}

// The cursor stays on the same FIELD when the type cycles a row in or out —
// otherwise changing the type teleports it.
func TestStorageAssignForm_CursorKeepsItsFieldAcrossARebuild(t *testing.T) {
	s := loadedAssignForm(t)
	// Move to Occupant, which exists in both shapes.
	for s.fields[s.cursor] != safLabel {
		s = assignKey(t, s, "tab")
	}
	s = assignKey(t, s, " ") // committee → logistics drops the SIG row
	if s.fields[s.cursor] != safLabel {
		t.Errorf("cursor moved to field %d, want it to stay on Occupant", s.fields[s.cursor])
	}
}

// TestStorageAssignForm_CommitteeNeedsAnIdentity mirrors the serializer: a
// committee assignment that names neither a SIG nor a label is just a blocked
// slot, so it is refused locally instead of costing a 400.
func TestStorageAssignForm_CommitteeNeedsAnIdentity(t *testing.T) {
	s := loadedAssignForm(t)
	if _, err := s.buildPayload(); err == nil {
		t.Fatal("a committee assignment with no SIG and no label should be refused")
	}

	// A free-text description is enough (the serializer accepts either).
	s.inputs[safLabel].SetValue("Welding, pending the SIG being created")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("a described committee should be accepted: %v", err)
	}
	if w.OwningGroup != nil {
		t.Errorf("owning_group = %v, want nil", w.OwningGroup)
	}

	// Logistics carries its occupant as free text and may legitimately be
	// blank — a logistics slot is self-describing.
	s.inputs[safLabel].SetValue("")
	s = assignKey(t, s, " ")
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("a bare logistics assignment should be accepted: %v", err)
	}
}

// A logistics/class assignment must never smuggle a committee group along, even
// if one was picked before the type was cycled.
func TestStorageAssignForm_NonCommitteeDropsTheGroup(t *testing.T) {
	s := loadedAssignForm(t)
	id := 3
	s.owningGroupID, s.owningGroupNm = &id, "Welding SIG"

	w, err := s.buildPayload()
	if err != nil || w.OwningGroup == nil || *w.OwningGroup != 3 {
		t.Fatalf("committee payload = %+v (err %v), want owning_group 3", w, err)
	}

	s = assignKey(t, s, " ") // → logistics
	w, err = s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.OwningGroup != nil {
		t.Errorf("owning_group = %v, want it dropped for a logistics assignment", *w.OwningGroup)
	}
	if w.StorageType != omsapi.StorageAssignmentTypeLogistics {
		t.Errorf("storage_type = %q", w.StorageType)
	}
	if w.SlotCode != "1A1" {
		t.Errorf("slot_code = %q, want the canonical code", w.SlotCode)
	}
}

// TestStorageAssignForm_Picker covers the SIG sub-phase: Ctrl-E opens it, the
// arrows move, enter picks, and the clear row puts it back to none. The old
// space/j/k are gone with the rest of the accelerators (sc-6qsk) — the filter
// is always live, so a letter is filter text now.
func TestStorageAssignForm_Picker(t *testing.T) {
	s := loadedAssignForm(t)
	for s.fields[s.cursor] != safGroup {
		s = assignKey(t, s, "tab")
	}
	s = assignKey(t, s, "ctrl+e")
	if s.phase != assignPhasePick {
		t.Fatal("ctrl+e on the SIG row should open the picker")
	}
	// Row 0 is the clear row — a create has nothing to graft, so every other
	// row is a SIG.
	if len(s.pickRows) != 3 || !s.pickRows[0].clear {
		t.Fatalf("picker rows = %+v", s.pickRows)
	}
	s = assignKey(t, s, "down")
	s = assignKey(t, s, "enter")
	if s.phase != assignPhaseForm {
		t.Error("enter should close the picker")
	}
	if s.owningGroupID == nil || *s.owningGroupID != 3 || s.owningGroupNm != "Welding SIG" {
		t.Errorf("picked %v / %q, want SIG 3 Welding SIG", s.owningGroupID, s.owningGroupNm)
	}

	// The clear row puts it back to none.
	s = assignKey(t, s, "ctrl+e")
	s = assignKey(t, s, "up")
	s = assignKey(t, s, "enter")
	if s.owningGroupID != nil {
		t.Errorf("the clear row should unset the group, got %v", *s.owningGroupID)
	}
}

// A SIG list that fails (it is a separate permission surface) must not block a
// logistics assignment that never needed it — it is reported beside the field.
func TestStorageAssignForm_SIGFailureIsNotFatal(t *testing.T) {
	s := NewStorageAssignFormScreen(Deps{}, "1A1", nil)
	next, _ := s.Update(storageAssignSIGsLoadedMsg{
		err: &omsapi.APIError{Status: 403, Code: "permission_denied", Message: "staff only"},
	})
	s = next.(*StorageAssignFormScreen)
	if !strings.Contains(s.View(), "SIG list unavailable") {
		t.Errorf("a failed SIG list should be reported on the form:\n%s", s.View())
	}
	s = assignKey(t, s, " ") // → logistics
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("a logistics assignment should still save: %v", err)
	}
}

// esc and a successful save both go where the caller said, so the warden lands
// back on the cell they were standing on.
func TestStorageAssignForm_ReturnsToTheCaller(t *testing.T) {
	marker := NewStorageSlotsScreen(Deps{})
	s := NewStorageAssignFormScreen(Deps{}, "1A1", func(Deps) Screen { return marker })
	_, cmd := s.Update(namedKey("esc"))
	if cmd == nil {
		t.Fatal("esc produced no command")
	}
	msg, ok := cmd().(SwitchScreenMsg)
	if !ok || msg.Screen != Screen(marker) {
		t.Fatalf("esc went to %#v, want the caller's screen", cmd())
	}

	// With no caller it falls back to the slot's own detail screen.
	fallback := NewStorageAssignFormScreen(Deps{}, "1A1", nil)
	detail, ok := fallback.backScreen().(*StorageSlotDetailScreen)
	if !ok || detail.code != "1A1" {
		t.Errorf("fallback = %#v, want the 1A1 detail screen", fallback.backScreen())
	}
}

func TestStorageAssignForm_SaveReportsWhatTheGridWillPaint(t *testing.T) {
	got := storageAssignedText(&omsapi.StorageAssignment{
		SlotCode: "1C1", OccupantDisplay: "Welding SIG", TypeLetter: "C",
	}, "1C1")
	for _, want := range []string{"1C1", "Welding SIG", "C"} {
		if !strings.Contains(got, want) {
			t.Errorf("save message %q is missing %q", got, want)
		}
	}
}

// The form owns every key: field letters, tab, enter and esc must not reach the
// root's globals.
func TestStorageAssignForm_WantsRawInput(t *testing.T) {
	if !NewStorageAssignFormScreen(Deps{}, "1A1", nil).WantsRawInput() {
		t.Error("the assign form should claim raw input")
	}
}
