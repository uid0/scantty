// The C/L/E half of occupancy as it shows up on the SLOT screens.
//
// Backend PR op-wgc8 (#994) gave a slot a second kind of occupant — a
// staff-assigned committee/logistics/class holding — alongside the member's
// project stint. A screen that reads occupancy off `current_stint` alone now
// reports the welding SIG's shelf as FREE, which is exactly how the same slot
// gets handed out twice. These tests pin that it doesn't.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

func heldSlot() omsapi.StorageSlot {
	return omsapi.StorageSlot{
		ID: 11, Code: "1C1", Rack: 1, Level: "C", Position: 1, IsActive: true,
		IsOccupied: true, OccupancyType: "C",
		CurrentAssignment: &omsapi.StorageSlotAssignmentSummary{
			ID: 5, StorageType: omsapi.StorageAssignmentTypeCommittee, TypeLetter: "C",
			OccupantDisplay: "Welding SIG",
			AssignedAt:      time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		},
	}
}

// TestSlotDetail_ShowsTheHolding — the load-bearing one. A committee-held slot
// must never read as free.
func TestSlotDetail_ShowsTheHolding(t *testing.T) {
	body := stripANSI(loadedSlotDetail(heldSlot()).renderBody())

	if strings.Contains(body, "Free") || strings.Contains(body, "[free]") {
		t.Errorf("a committee-held slot rendered as free:\n%s", body)
	}
	for _, want := range []string{"[occupied]", "Welding SIG", "Committee", "2024-02-01"} {
		if !strings.Contains(body, want) {
			t.Errorf("the occupancy block should name %q:\n%s", want, body)
		}
	}
}

// is_occupied covers BOTH halves, so an occupied slot with neither one decoded
// (an older payload, a shape that drifts) still must not invite a hand-out.
func TestSlotDetail_OccupiedWithoutEitherHalf(t *testing.T) {
	slot := omsapi.StorageSlot{ID: 1, Code: "1A1", IsActive: true, IsOccupied: true}
	body := stripANSI(loadedSlotDetail(slot).renderBody())
	if strings.Contains(body, "available to reserve") {
		t.Errorf("an occupied slot offered itself for reservation:\n%s", body)
	}
}

// TestSlotDetail_AssignAndRelease covers the two actions and the guards that
// keep each key doing exactly one thing.
func TestSlotDetail_AssignAndRelease(t *testing.T) {
	held := loadedSlotDetail(heldSlot())

	// `a` on a held slot points at release rather than opening the form.
	_, cmd := held.Update(namedKey("a"))
	if cmd == nil {
		t.Fatal("a produced no command")
	}
	msg, ok := cmd().(StatusMsg)
	if !ok || !strings.Contains(msg.Text, "release") {
		t.Errorf("a on a held slot should point at release, got %#v", cmd())
	}

	// `R` opens the confirm, which owns y/n.
	held = slotDetailKey(t, held, "R")
	if !held.confirmingRelease {
		t.Fatal("R should open the release confirm")
	}
	if !held.WantsRawInput() {
		t.Error("the release confirm must claim raw input so n=no doesn't open notifications")
	}
	if got := stripANSI(held.releaseConfirmText()); !strings.Contains(got, "Welding SIG") {
		t.Errorf("confirm = %q, want it to name the holder", got)
	}
	held = slotDetailKey(t, held, "n")
	if held.confirmingRelease {
		t.Error("n should cancel the confirm")
	}

	// A free, in-service slot is the case that opens the form.
	free := loadedSlotDetail(omsapi.StorageSlot{ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true})
	_, cmd = free.Update(namedKey("a"))
	switchMsg, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("a on a free slot produced %T, want the assign form", cmd())
	}
	form, ok := switchMsg.Screen.(*StorageAssignFormScreen)
	if !ok || form.code != "1A1" {
		t.Fatalf("a opened %#v, want the 1A1 assign form", switchMsg.Screen)
	}
	// And it comes back here, not to the slot list.
	if back, ok := form.backScreen().(*StorageSlotDetailScreen); !ok || back.code != "1A1" {
		t.Errorf("the form returns to %#v, want the 1A1 detail", form.backScreen())
	}

	// R on a slot nobody holds says so instead of opening an empty confirm.
	_, cmd = free.Update(namedKey("R"))
	if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "not assigned") {
		t.Errorf("R on a free slot should say it isn't assigned, got %#v", cmd())
	}

	// A retired slot is not on offer at all.
	retired := loadedSlotDetail(omsapi.StorageSlot{ID: 2, Code: "1A2", IsActive: false})
	_, cmd = retired.Update(namedKey("a"))
	if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "out of service") {
		t.Errorf("a on a retired slot should refuse, got %#v", cmd())
	}
}

// The hint offers only the action that can actually work — listing both would
// invite the key that can't.
func TestSlotDetail_HintTracksTheOccupancy(t *testing.T) {
	held := stripANSI(loadedSlotDetail(heldSlot()).View())
	if !strings.Contains(held, "R release") || strings.Contains(held, "a assign") {
		t.Errorf("a held slot should offer release only:\n%s", held)
	}

	free := stripANSI(loadedSlotDetail(omsapi.StorageSlot{ID: 1, Code: "1A1", IsActive: true}).View())
	if !strings.Contains(free, "a assign") || strings.Contains(free, "R release") {
		t.Errorf("a free slot should offer assign only:\n%s", free)
	}
}

// `a` is the global authorizations hotkey — without the HandlesKey claim the
// detail screen would never see it.
func TestSlotDetail_ClaimsAssignKey(t *testing.T) {
	s := loadedSlotDetail(heldSlot())
	if !s.HandlesKey("a") || !s.HandlesKey("G") {
		t.Error("the detail screen must claim a (global authorizations) and G (global categories)")
	}
	if s.HandlesKey("R") || s.HandlesKey("x") {
		t.Error("R and x are globally free and should reach us by fall-through")
	}

	r := press(t, newTestRoot(s), "a")
	if r.screen != Screen(s) {
		t.Fatalf("global a=authorizations shadowed the assign key: screen is now %T", r.screen)
	}
}

// Deleting a slot a committee holds is refused server-side (the assignment FK
// is CASCADE, so the record of the holding would go with it) — say so before
// the round trip.
func TestSlotDetail_DeleteConfirmWarnsAboutTheHolding(t *testing.T) {
	s := loadedSlotDetail(heldSlot())
	s.confirmingDelete = true
	got := stripANSI(s.deleteConfirmText())
	if !strings.Contains(got, "Welding SIG") || !strings.Contains(got, "refuse") {
		t.Errorf("delete confirm = %q, want it to name the holding and the refusal", got)
	}
}

// TestSlotList_ShowsTheHolding — the same hazard on the list row: "occupied"
// alone doesn't say who, and "free" would be a lie.
func TestSlotList_ShowsTheHolding(t *testing.T) {
	got := slotOccupancyText(heldSlot())
	if !strings.Contains(got, "Welding SIG") || !strings.Contains(got, "Committee") {
		t.Errorf("row text = %q, want the committee named", got)
	}

	// A stint still wins the row when both somehow exist — the member's clock
	// is the one that has to keep being watched (the backend tie-breaks the
	// same way).
	both := heldSlot()
	both.CurrentStint = &omsapi.StorageSlotOccupant{
		StintID: "PS-AB23CDFG", Username: "ada", DisplayName: "Ada Byron",
	}
	if got := slotOccupancyText(both); !strings.Contains(got, "Ada Byron") {
		t.Errorf("row text = %q, want the stint to win", got)
	}

	free := omsapi.StorageSlot{ID: 1, Code: "1A1", IsActive: true}
	if got := slotOccupancyText(free); got != "free" {
		t.Errorf("an unheld slot = %q, want free", got)
	}
}
