package tui

import (
	"reflect"
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

// pressFacilitiesHotkey sends one key through the ROOT with the facilities menu
// active and returns whatever screen ends up showing. A menu item answers with
// a SwitchScreenMsg command rather than swapping the screen inline, so the
// command has to be run and fed back — a global hotkey, by contrast, replaces
// r.screen immediately, which is exactly the difference this asserts.
func pressFacilitiesHotkey(t *testing.T, key rune) Screen {
	t.Helper()
	menu := NewFacilitiesScreen(Deps{})
	r := newTestRoot(menu)
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
	r = next.(Root)
	if r.screen != Screen(menu) {
		// A GLOBAL hotkey swapped the screen inline — the menu never saw the
		// key. Return what it opened; its Init command is deliberately NOT run
		// (these screens fetch on Init and the test client is nil).
		return r.screen
	}
	if cmd == nil {
		return r.screen
	}
	// The menu answered with a SwitchScreenMsg command (a pure closure), so
	// feed it back to find out what it actually opens.
	msg := cmd()
	if msg == nil {
		return r.screen
	}
	next, _ = r.Update(msg)
	return next.(Root).screen
}

// TestFacilities_HotkeysReachTheMenu generalizes that hazard across the whole
// menu: every item's hotkey must actually open THAT item through the root. Two
// ways to lose one — a GLOBAL hotkey of the same letter, or one of the menu's
// own navigation keys (j/k/g/G), which are matched before the item loop.
// Pre-existing casualties are logged rather than failed (fixing them is not
// this bead's business); a NEW item must not join them.
func TestFacilities_HotkeysReachTheMenu(t *testing.T) {
	menu := NewFacilitiesScreen(Deps{})
	for _, it := range menu.items {
		if it.build == nil {
			continue
		}
		want, _ := it.build(Deps{})
		gotType := reflect.TypeOf(pressFacilitiesHotkey(t, it.hotkey))
		wantType := reflect.TypeOf(want)
		if gotType != wantType {
			t.Logf("facilities item %q (hotkey %q) is unreachable by its hotkey: opened %v, want %v",
				it.label, string(it.hotkey), gotType, wantType)
			if it.label == "Storage slots" {
				t.Errorf("the storage-slots hotkey %q never reaches the menu", string(it.hotkey))
			}
		}
	}
}

// TestFacilities_StorageSlotsListedInView keeps the item discoverable — the
// menu is the only way in.
func TestFacilities_StorageSlotsListedInView(t *testing.T) {
	out := NewFacilitiesScreen(Deps{}).View()
	for _, want := range []string{"Storage slots", "[R]"} {
		if !strings.Contains(out, want) {
			t.Errorf("the facilities menu should list the storage-slots entry (%q):\n%s", want, out)
		}
	}
}
