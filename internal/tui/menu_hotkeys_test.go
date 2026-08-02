package tui

import (
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The menu screens — Facilities and the Reports hub — share one idiom: a cursor
// list whose entries each carry a single-letter accelerator, matched AFTER the
// cursor-navigation keys in the same Update switch. That ordering is where
// accelerators go to die, and the failure is silent: the key still does
// something (the cursor moves), so nothing looks broken until someone notices
// the entry only ever opens with enter. sc-5dqy was exactly that — "Checklists"
// sat on 'k', which the menu spends on cursor-up.
//
// Three ways an entry can end up on a letter nobody can press:
//  1. two entries claiming the same letter — the first in the slice wins;
//  2. the menu's OWN cursor-nav keys (j/k/g/G), matched before the entry loop;
//  3. a GLOBAL hotkey from app.go's switch, dispatched before a menu that isn't
//     a LocalKeyScreen ever sees the key.
//
// TestMenuEntries_OpenTheirScreen is the net under all three, because it presses
// each accelerator through the ROOT and checks where it lands — a screen-level
// Update test cannot see (2) or (3) at all. The two narrower tests exist for the
// diagnosis they print: "opened *FacilitiesScreen" is a lot less useful than
// "'k' is the menu's own cursor-up key".
//
// Case (3) is a collision but not automatically a bug: the e-Paper entry ('e')
// and Checklists ('K') sit on globals that open exactly the screen the entry
// builds, in exactly the workspace it names, so the accelerator still lands
// where the label promises. Pressing through the root is what tells a benign
// overlap apart from a real shadowing.

type menuScreenCase struct {
	name string
	// build returns a fresh menu instance; every press needs its own, because a
	// press that lands mutates the root it was sent through.
	build   func() Screen
	entries []menuEntryCase
	// navKeys are the single-letter keys this menu's Update spends on cursor
	// movement before it reaches its entry loop. It is a literal — there is no
	// way to ask a switch statement what it consumes — but drift here costs
	// precision, not coverage: TestMenuEntries_OpenTheirScreen still fails on a
	// shadowed entry whatever this list says.
	navKeys []string
}

type menuEntryCase struct {
	hotkey rune
	label  string
	opens  Screen // the screen this entry builds
}

func menuScreenCases() []menuScreenCase {
	facilities := NewFacilitiesScreen(Deps{})
	facilityEntries := make([]menuEntryCase, 0, len(facilities.items))
	for _, it := range facilities.items {
		if it.build == nil {
			continue
		}
		screen, _ := it.build(Deps{})
		facilityEntries = append(facilityEntries, menuEntryCase{hotkey: it.hotkey, label: it.label, opens: screen})
	}

	reports := NewReportsScreen(Deps{})
	reportEntries := make([]menuEntryCase, 0, len(reports.items))
	for _, it := range reports.items {
		if it.build == nil {
			continue
		}
		reportEntries = append(reportEntries, menuEntryCase{hotkey: it.hotkey, label: it.label, opens: it.build(Deps{})})
	}

	return []menuScreenCase{
		{
			name:    "Facilities",
			build:   func() Screen { return NewFacilitiesScreen(Deps{}) },
			entries: facilityEntries,
			// facilities.go: j/down, k/up, g/home, G/end, enter.
			navKeys: []string{"j", "k", "g", "G", "enter"},
		},
		{
			name:    "Reports",
			build:   func() Screen { return NewReportsScreen(Deps{}) },
			entries: reportEntries,
			// reports.go: j/down, k/up, g/home, enter.
			navKeys: []string{"j", "k", "g", "enter"},
		},
	}
}

// TestMenuEntries_HotkeysAreUnique: two entries on one letter leave the second
// unreachable, and the first one in the slice wins silently.
func TestMenuEntries_HotkeysAreUnique(t *testing.T) {
	for _, mc := range menuScreenCases() {
		seen := map[rune]string{}
		for _, e := range mc.entries {
			if prev, dup := seen[e.hotkey]; dup {
				t.Errorf("%s menu: hotkey %q is claimed by both %q and %q",
					mc.name, string(e.hotkey), prev, e.label)
			}
			seen[e.hotkey] = e.label
		}
	}
}

// TestMenuEntries_DodgeCursorNav states the sc-5dqy bug directly: a menu matches
// its cursor keys before its entry loop, so an entry sitting on one is dead on
// arrival — pressing its letter moves the selection instead of opening it.
func TestMenuEntries_DodgeCursorNav(t *testing.T) {
	for _, mc := range menuScreenCases() {
		for _, e := range mc.entries {
			for _, nav := range mc.navKeys {
				if string(e.hotkey) != nav {
					continue
				}
				t.Errorf("%s menu: entry %q sits on %q, which the menu spends on cursor navigation — it can never be opened by its own hotkey",
					mc.name, e.label, nav)
			}
		}
	}
}

// TestMenuEntries_OpenTheirScreen sweeps every menu entry through the ROOT and
// requires it to land on the screen the entry builds. This is the assertion the
// sc-ofem sweep only logged.
func TestMenuEntries_OpenTheirScreen(t *testing.T) {
	for _, mc := range menuScreenCases() {
		for _, e := range mc.entries {
			got := pressMenuHotkey(t, mc.build(), e.hotkey)
			if reflect.TypeOf(got) == reflect.TypeOf(e.opens) && got.Title() == e.opens.Title() {
				continue
			}
			t.Errorf("%s menu: entry %q (hotkey %q) opened %T %q, want %T %q",
				mc.name, e.label, string(e.hotkey), got, got.Title(), e.opens, e.opens.Title())
		}
	}
}

// pressMenuHotkey sends one key through the ROOT with `menu` active and returns
// whatever screen ends up showing. A menu entry answers with a SwitchScreenMsg
// command rather than swapping the screen inline, so the command has to be run
// and fed back — a global hotkey, by contrast, replaces r.screen immediately,
// which is exactly the difference these tests turn on.
func pressMenuHotkey(t *testing.T, menu Screen, key rune) Screen {
	t.Helper()
	r := newTestRoot(menu)
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
	r = next.(Root)
	if r.screen != menu {
		// A GLOBAL hotkey swapped the screen inline — the menu never saw the
		// key. Return what it opened; its Init command is deliberately NOT run
		// (these screens fetch on Init and the test client is nil).
		return r.screen
	}
	if cmd == nil {
		return r.screen
	}
	// The menu answered with a SwitchScreenMsg command (a pure closure), so feed
	// it back to find out what it actually opens.
	msg := cmd()
	if msg == nil {
		return r.screen
	}
	next, _ = r.Update(msg)
	return next.(Root).screen
}
