package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The menu-wide invariants — unique letters, letters that dodge the cursor keys,
// and every letter actually opening its item through the root — live in
// menu_hotkeys_test.go, which holds the Reports hub to the same contract. What
// stays here is per-item: the letters whose choice took an argument.

// TestFacilities_OpensStorageSlots drives the hotkey through the ROOT, not the
// screen, because that is where the real hazard lives: FacilitiesScreen is not
// a LocalKeyScreen, so its item letters reach it only via app.go's
// fall-through. A letter that is also a GLOBAL hotkey (l = lockouts, e =
// e-paper, s = nav scan) opens the global surface instead and leaves the menu
// item permanently unreachable — which a screen-level Update test would not
// catch.
func TestFacilities_OpensStorageSlots(t *testing.T) {
	screen := pressFacilitiesHotkey(t, 'R')
	if _, ok := screen.(*StorageSlotsScreen); !ok {
		t.Fatalf("R from the facilities menu opened %T, want *StorageSlotsScreen", screen)
	}
}

// pressFacilitiesHotkey sends one key through the ROOT with a fresh facilities
// menu active and returns whatever screen ends up showing.
func pressFacilitiesHotkey(t *testing.T, key rune) Screen {
	t.Helper()
	return pressMenuHotkey(t, NewFacilitiesScreen(Deps{}), key)
}

// TestFacilities_ChecklistsHotkey is the sc-5dqy regression. The item sat on
// lowercase 'k', which this menu — like every cursor list in scantty — spends on
// cursor-up, so it was matched long before the item loop and the entry could
// only ever be opened with enter. It now rides uppercase K, the app-wide
// checklists global (app.go, advertised on the welcome screen), which opens the
// same screen in the same workspace. The second half of the test is the other
// half of the fix: lowercase k must still move the cursor, because promoting
// item hotkeys over cursor movement would have bought this one entry at the cost
// of the up-arrow every other menu keeps.
func TestFacilities_ChecklistsHotkey(t *testing.T) {
	screen := pressFacilitiesHotkey(t, 'K')
	if _, ok := screen.(*ChecklistsScreen); !ok {
		t.Fatalf("K from the facilities menu opened %T, want *ChecklistsScreen", screen)
	}

	menu := NewFacilitiesScreen(Deps{})
	menu.cursor = 1
	next, cmd := menu.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if cmd != nil {
		t.Errorf("lowercase k should move the cursor, not open a screen")
	}
	if got := next.(*FacilitiesScreen).cursor; got != 0 {
		t.Errorf("lowercase k left the cursor at %d, want 0 (cursor-up)", got)
	}

	if out := menu.View(); !strings.Contains(out, "[K]  Checklists") {
		t.Errorf("the menu should advertise the checklists hotkey:\n%s", out)
	}
}

// TestFacilities_StorageSlotsListedInView keeps the item discoverable — the
// menu is the only way in.
func TestFacilities_StorageSlotsListedInView(t *testing.T) {
	out := NewFacilitiesScreen(Deps{}).View()
	for _, want := range []string{"Storage slots", "[R]", "Storage overview", "[O]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the facilities menu should list the storage entries (%q):\n%s", want, out)
		}
	}
}

// TestFacilities_OpensStorageOverview: `o` is the global op-modes hotkey, so the
// overview claims uppercase O — and the only way to prove it lands is through
// the root, because the menu is not a LocalKeyScreen.
func TestFacilities_OpensStorageOverview(t *testing.T) {
	screen := pressFacilitiesHotkey(t, 'O')
	if _, ok := screen.(*StorageOverviewScreen); !ok {
		t.Fatalf("O from the facilities menu opened %T, want *StorageOverviewScreen", screen)
	}
}
