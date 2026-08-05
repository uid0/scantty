// Slot-aware project storage (op-hfw5): the intake form's slot claim and the
// stint detail's location read-out.
package tui

import (
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

func psFormKey(t *testing.T, s *ProjectStorageFormScreen, key string) *ProjectStorageFormScreen {
	t.Helper()
	next, _ := s.Update(namedKey(key))
	out, ok := next.(*ProjectStorageFormScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *ProjectStorageFormScreen", next)
	}
	return out
}

func psFormWithSlots(slots []omsapi.StorageSlot) *ProjectStorageFormScreen {
	s := NewProjectStorageFormScreen(Deps{})
	s.terminalHeight = 40
	next, _ := s.Update(projectStorageSlotsLoadedMsg{slots: slots})
	return next.(*ProjectStorageFormScreen)
}

func freeSlots() []omsapi.StorageSlot {
	return []omsapi.StorageSlot{
		{ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true},
		{ID: 2, Code: "1A2", Rack: 1, Level: "A", Position: 2, IsActive: true, RequiresPalletJack: true},
		{ID: 3, Code: "2B1", Rack: 2, Level: "B", Position: 1, IsActive: true, OwningGroupName: "Woodshop"},
	}
}

// TestProjectStorageForm_SlotPickerClaimsCode — picking a free slot puts its
// CODE on the wire (the spelling the serializer can't confuse with a pk).
func TestProjectStorageForm_SlotPickerClaimsCode(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.cursor = indexOfField(s.fields, psfSlot)
	s = psFormKey(t, s, "ctrl+e")
	if s.phase != psFormPhaseSlotPick {
		t.Fatalf("ctrl+e on the slot row should open the picker")
	}
	if !s.pickRows[0].clear {
		t.Errorf("row 0 must be the explicit no-slot row — ad-hoc storage is first-class")
	}
	s.pickCursor = 2 // 1A2
	s = psFormKey(t, s, "enter")
	if s.slotCode != "1A2" {
		t.Errorf("slotCode = %q, want 1A2", s.slotCode)
	}

	s.inputs[psfUsername].SetValue("alice")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.SlotCode != "1A2" {
		t.Errorf("payload slot_code = %q, want 1A2", w.SlotCode)
	}
}

// TestProjectStorageForm_SlotOptional — a stint still starts with no slot at
// all (ad-hoc storage), so the claim must never become required.
func TestProjectStorageForm_SlotOptional(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.inputs[psfUsername].SetValue("alice")
	s.inputs[psfStorageLocation].SetValue("floor by the CNC")
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.SlotCode != "" {
		t.Errorf("slot_code should stay empty (and be omitted on the wire), got %q", w.SlotCode)
	}
	if w.StorageLocationName != "floor by the CNC" {
		t.Errorf("the ad-hoc location must still work: %+v", w)
	}
}

// TestProjectStorageForm_SlotPickerDetaches — row 0 clears a mis-pick.
func TestProjectStorageForm_SlotPickerDetaches(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.slotCode, s.slotLabel = "1A1", "1A1"
	s.openSlotPick()
	if s.pickRows[s.pickCursor].key != "1A1" {
		t.Errorf("re-opening should park on the current claim, got %+v", s.pickRows[s.pickCursor])
	}
	s.pickCursor = 0
	s = psFormKey(t, s, "enter")
	if s.slotCode != "" {
		t.Errorf("the clear row should detach the slot, got %q", s.slotCode)
	}
}

// TestProjectStorageForm_SlotPickerEscKeeps — esc means "done looking".
func TestProjectStorageForm_SlotPickerEscKeeps(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.slotCode = "1A1"
	s.openSlotPick()
	s.pickCursor = 0
	s = psFormKey(t, s, "esc")
	if s.slotCode != "1A1" {
		t.Errorf("esc must keep the current claim, got %q", s.slotCode)
	}
	if s.phase != psFormPhaseForm {
		t.Errorf("esc should return to the form")
	}
}

// TestProjectStorageForm_TypedCodeEscapeHatch — the slot list is a staff /
// Storage Admin surface while the claim itself is not, and the free filter
// hides slots an operator may still be standing at. A typed, VALID code is
// therefore offered as its own row; the backend re-checks it either way.
func TestProjectStorageForm_TypedCodeEscapeHatch(t *testing.T) {
	s := psFormWithSlots(nil)
	s.slotsErr = "oms: http 403: forbidden"
	s.openSlotPick()
	if len(s.pickRows) != 1 {
		t.Fatalf("an empty list should offer only the no-slot row, got %+v", s.pickRows)
	}

	s.pickSearch.SetValue("3c7")
	s.applySlotFilter()
	var typed *assetPickRow
	for i := range s.pickRows {
		if s.pickRows[i].key == "3C7" {
			typed = &s.pickRows[i]
		}
	}
	if typed == nil {
		t.Fatalf("a valid typed code should be offered as a row: %+v", s.pickRows)
	}
	if !strings.Contains(typed.label, "typed") {
		t.Errorf("the synthetic row should say it was typed, got %q", typed.label)
	}

	// Junk is NOT offered — only something the code grammar accepts.
	s.pickSearch.SetValue("zzz")
	s.applySlotFilter()
	if len(s.pickRows) != 1 {
		t.Errorf("a non-code must not become a claimable row: %+v", s.pickRows)
	}
}

// TestProjectStorageForm_TypedCodeNotDuplicated — when the filter already
// matched the exact slot, the synthetic row would be a confusing duplicate.
func TestProjectStorageForm_TypedCodeNotDuplicated(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.openSlotPick()
	s.pickSearch.SetValue("1A1")
	s.applySlotFilter()
	count := 0
	for _, r := range s.pickRows {
		if r.key == "1A1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("1A1 should appear once, got %d rows: %+v", count, s.pickRows)
	}
}

// TestProjectStorageForm_MalformedSlotRejectedLocally — a claim that could
// never resolve is a message, not a round trip.
func TestProjectStorageForm_MalformedSlotRejectedLocally(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.inputs[psfUsername].SetValue("alice")
	s.slotCode = "not-a-code"
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("a malformed slot code should be rejected before the request")
	}
	s.slotCode = " 1a1 "
	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.SlotCode != "1A1" {
		t.Errorf("the code should be normalized before it is sent, got %q", w.SlotCode)
	}
}

// TestProjectStorageForm_SlotRowStates keeps the three "no slot" readings
// distinct — nothing claimed, a FAILED list, and a genuinely empty rack.
// Silence on a failure would read as "the racking is full".
func TestProjectStorageForm_SlotRowStates(t *testing.T) {
	loading := NewProjectStorageFormScreen(Deps{})
	if got, _ := loading.slotRowValue(); !strings.Contains(got, "loading") {
		t.Errorf("pre-load should say so, got %q", got)
	}

	failed := psFormWithSlots(nil)
	failed.slotsReady = false
	failed.slotsErr = "403"
	if got, _ := failed.slotRowValue(); !strings.Contains(got, "unavailable") {
		t.Errorf("a failed load must say unavailable, got %q", got)
	}

	empty := psFormWithSlots(nil)
	if got, _ := empty.slotRowValue(); !strings.Contains(got, "no free slots") {
		t.Errorf("an empty rack should say so, got %q", got)
	}

	full := psFormWithSlots(freeSlots())
	if got, _ := full.slotRowValue(); !strings.Contains(got, "3 free") {
		t.Errorf("a loaded list should offer its size, got %q", got)
	}

	claimed := psFormWithSlots(freeSlots())
	claimed.slotCode = "1A1"
	if got, _ := claimed.slotRowValue(); !strings.Contains(got, "1A1") {
		t.Errorf("a claim should show the code, got %q", got)
	}
}

// TestProjectStorageForm_SlotRowIsNotATextInput — the picker row holds a code,
// not free text, so stray letters must not silently become input.
func TestProjectStorageForm_SlotRowIsNotATextInput(t *testing.T) {
	s := psFormWithSlots(freeSlots())
	s.cursor = indexOfField(s.fields, psfSlot)
	s = psFormKey(t, s, "z")
	if s.inputs[psfSlot].Value() != "" {
		t.Errorf("the picker row must not accept typed text, got %q", s.inputs[psfSlot].Value())
	}
	if s.phase != psFormPhaseForm {
		t.Errorf("a stray key should not open the picker")
	}
}

// TestProjectStorageDetail_ShowsSlot — the slot is authoritative over the
// free-text location, so it leads the Location section, and the label preview
// mirrors the printed ticket (which puts "Slot <code>" under the stint id).
func TestProjectStorageDetail_ShowsSlot(t *testing.T) {
	s := NewProjectStorageDetailScreen(Deps{}, "PS-AB23CDFG")
	next, _ := s.Update(projectStorageDetailLoadedMsg{stint: &omsapi.ProjectStorageStint{
		StintID: "PS-AB23CDFG", Username: "alice", DisplayName: "Alice Smith",
		SlotCode: "1A1", LocationDisplay: "1A1", Status: "active",
	}})
	s = next.(*ProjectStorageDetailScreen)
	body := s.renderBody()
	if !strings.Contains(body, "Slot: 1A1") {
		t.Errorf("the Location section must show the slot code:\n%s", body)
	}
	if !strings.Contains(body, "SLOT     1A1") {
		t.Errorf("the label preview must carry the slot line the ticket prints:\n%s", body)
	}
	// A slot-claiming stint has no free-text location; an empty "Storage: —"
	// row would imply something is missing.
	if strings.Contains(body, "Storage: —") {
		t.Errorf("a slot-claiming stint should not show an empty storage row:\n%s", body)
	}
}

// TestProjectStorageDetail_AdHocStintUnchanged — a stint with no slot renders
// exactly as it did before op-hfw5, matching the printed ticket's three lines.
func TestProjectStorageDetail_AdHocStintUnchanged(t *testing.T) {
	s := NewProjectStorageDetailScreen(Deps{}, "PS-AB23CDFG")
	next, _ := s.Update(projectStorageDetailLoadedMsg{stint: &omsapi.ProjectStorageStint{
		StintID: "PS-AB23CDFG", Username: "bob", DisplayName: "Bob Jones",
		StorageLocationName: "floor by the CNC", Status: "active",
	}})
	s = next.(*ProjectStorageDetailScreen)
	body := s.renderBody()
	if strings.Contains(body, "Slot:") || strings.Contains(body, "SLOT ") {
		t.Errorf("an ad-hoc stint must not grow an empty slot line:\n%s", body)
	}
	if !strings.Contains(body, "Storage: floor by the CNC") {
		t.Errorf("the ad-hoc location must still render:\n%s", body)
	}
	if !strings.Contains(body, "STORAGE  floor by the CNC") {
		t.Errorf("the label preview must still carry the ad-hoc location:\n%s", body)
	}
}

// TestProjectStorageDetail_NoLocationAtAll — neither a slot nor a name still
// renders a Storage row, so the section is never empty.
func TestProjectStorageDetail_NoLocationAtAll(t *testing.T) {
	s := NewProjectStorageDetailScreen(Deps{}, "PS-AB23CDFG")
	next, _ := s.Update(projectStorageDetailLoadedMsg{stint: &omsapi.ProjectStorageStint{
		StintID: "PS-AB23CDFG", Username: "bob", Status: "active",
	}})
	s = next.(*ProjectStorageDetailScreen)
	if !strings.Contains(s.renderBody(), "Storage: —") {
		t.Errorf("a locationless stint should still show a dash:\n%s", s.renderBody())
	}
}
