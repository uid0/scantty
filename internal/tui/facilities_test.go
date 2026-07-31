package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFacilities_HotkeysAreUnique guards the whole menu: two items sharing a
// letter would make the second unreachable, and the first item to match wins
// silently.
func TestFacilities_HotkeysAreUnique(t *testing.T) {
	s := NewFacilitiesScreen(Deps{})
	seen := map[rune]string{}
	for _, it := range s.items {
		if prev, dup := seen[it.hotkey]; dup {
			t.Errorf("hotkey %q is claimed by both %q and %q", string(it.hotkey), prev, it.label)
		}
		seen[it.hotkey] = it.label
	}
}

// TestFacilities_OpensStorageSlots pins the wiring rather than trusting that an
// edit to the menu survived: pressing the item's hotkey must land on the slots
// screen in the Facilities workspace.
func TestFacilities_OpensStorageSlots(t *testing.T) {
	s := NewFacilitiesScreen(Deps{})
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	if cmd == nil {
		t.Fatalf("the storage-slots hotkey produced no command")
	}
	msg, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("got %T, want SwitchScreenMsg", cmd())
	}
	if _, ok := msg.Screen.(*StorageSlotsScreen); !ok {
		t.Fatalf("opened %T, want *StorageSlotsScreen", msg.Screen)
	}
	if msg.Workspace != WSFacilities {
		t.Errorf("workspace = %v, want WSFacilities", msg.Workspace)
	}
}

// TestFacilities_StorageSlotsListedInView keeps the item discoverable — the
// menu is the only way in.
func TestFacilities_StorageSlotsListedInView(t *testing.T) {
	out := NewFacilitiesScreen(Deps{}).View()
	for _, want := range []string{"Storage slots", "[l]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the facilities menu should list the storage-slots entry (%q):\n%s", want, out)
		}
	}
}
