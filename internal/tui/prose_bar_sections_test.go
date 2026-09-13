package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_sections_test.go — the fixtures for the SEVENTH recipe taken out of
// proseBarUnconverted as a group rather than a screen at a time.
//
// THE RECIPE, and what makes these screens one group. Each is a FLAT list that
// shares its pane with a SECOND SECTION: the checklist browser draws the
// in-progress runs above the active checklists, with a cursor in each and a key
// moving the focus between them; the firmware screen draws its rollouts above
// two read-only sections, the versions and the recent updates. Each wrote every
// row of every section and then a literal, so the pane held the footer only
// while all of the sections together were shorter than it — and neither the
// flat-list recipe's one window nor its step pair said what to do with a second
// section. The answers differ, and the difference is the point:
//
//   - WHERE A KEY MOVES THROUGH BOTH SECTIONS, they are ONE window. j/k move the
//     focused section's cursor and the window follows that row, so every marker
//     over either section is one a key answers (ChecklistsScreen.View).
//   - WHERE A SECTION IS READ-ONLY, it is NOT in the window. No key scrolls the
//     firmware versions, so a `↓ N more below` over them would be a marker no key
//     fetches; they take the lines the rollouts leave and mark their cut with the
//     mark this layer uses for lines no key brings back
//     (FirmwareScreen.renderView carries the give-order).
//
// TWO THINGS THE CONVERSION FOUND THAT WERE NOT THE BAR, both behaviour-neutral
// for the operator and both pinned below. The checklist footer named `tab` as
// the section swap, and Root takes a bare tab to the sidebar before the screen
// ever sees it: shift+tab is the key that swaps, and it was named nowhere
// (TestChecklists_TabReachesTheSidebarAndShiftTabSwaps). And a section that came
// back empty could hold the focus, leaving the other section's rows drawn with
// no key acting on them (TestChecklists_AnEmptySectionNeverHoldsTheFocus).
//
// EVERY WINDOW HAS AN OVERSIZED FIXTURE — a row, or a read-only section, taller
// than the whole pane — because a footer check whose fixtures cannot push the
// footer off is not checking the footer. Each was watched failing against HEAD
// before the conversion, and against the conversion with its cut taken out.

// proseBarSectionFixtures is every screen on that recipe, in every state its bar
// changes shape in.
func proseBarSectionFixtures() []proseBarFixture {
	const pickerOne = "one version to pick, so there is nowhere for the cursor to go"
	out := []proseBarFixture{
		// --- checklists --------------------------------------------------------
		// The focus on the ACTIVE checklists, with runs above them: the state the
		// screen opens in, and the only one naming `enter start a run` and the
		// swap to the runs together.
		{
			name: "checklists", recv: "ChecklistsScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarChecklists(proseBarFlatLongRows, 3), "j")
			},
		},
		// The focus swapped onto the runs, reached by the key that swaps.
		{
			name: "checklists/in-progress runs", recv: "ChecklistsScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarSize(proseBarChecklists(proseBarFlatLongRows, 3), 80, 24), "shift+tab", "j")
			},
		},
		// Runs and no active checklists: the focus must START on the runs, and the
		// swap must come off because there is nothing to swap to.
		{
			name: "checklists/runs only", recv: "ChecklistsScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarChecklists(0, 3), "j") },
		},
		{
			name: "checklists/one row", recv: "ChecklistsScreen",
			build:    func() proseBarScreen { return proseBarChecklists(1, 0) },
			immobile: "one checklist and no runs, so there is nowhere for the cursor to go",
		},
		{
			name: "checklists/empty", recv: "ChecklistsScreen",
			build: func() proseBarScreen { return proseBarChecklists(0, 0) },
			immobile: "no checklists and no runs — the point of this fixture: the movement, " +
				"enter and swap segments must all be absent, and the way out named",
		},

		// --- firmware: the rollout list ----------------------------------------
		// One status per fixture, because the lifecycle keys follow the SELECTED
		// rollout's status and a fixture of mixed statuses shows one of them.
		{
			name: "firmware", recv: "FirmwareScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarFirmware("active", proseBarFlatLongRows), "j") },
		},
		{
			name: "firmware/draft rollout", recv: "FirmwareScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarFirmware("draft", proseBarFlatLongRows), "j") },
		},
		{
			name: "firmware/paused rollout", recv: "FirmwareScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarFirmware("paused", proseBarFlatLongRows), "j") },
		},
		// Completed: every lifecycle key comes off, and each still answers a
		// warning when pressed — a decline with a toast, which changes no pane.
		{
			name: "firmware/completed rollout", recv: "FirmwareScreen",
			build: func() proseBarScreen { return proseBarWalk(proseBarFirmware("completed", proseBarFlatLongRows), "j") },
		},
		// A transition already out: rolloutAction declines all four until it
		// answers, so all four come off.
		{
			name: "firmware/transition out", recv: "FirmwareScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarWalk(proseBarFirmware("draft", proseBarFlatLongRows), "j"), "s")
			},
		},
		{
			name: "firmware/one rollout", recv: "FirmwareScreen",
			build:    func() proseBarScreen { return proseBarFirmware("active", 1) },
			immobile: "one rollout, so there is nowhere for the cursor to go",
		},
		{
			name: "firmware/empty", recv: "FirmwareScreen",
			build: func() proseBarScreen {
				s := NewFirmwareScreen(Deps{})
				next, _ := s.Update(firmwareLoadedMsg{})
				return next.(*FirmwareScreen)
			},
			immobile: "no rollouts, versions or updates — the movement and lifecycle " +
				"segments must be absent, and `c`, `r` and `esc` named",
		},

		// --- firmware: the New-Rollout form ------------------------------------
		{
			name: "firmware/new rollout", recv: "FirmwareScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarFirmware("active", 3), "c") },
		},
		{
			name: "firmware/new rollout, batch field", recv: "FirmwareScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarFirmwareForm() },
		},
		{
			name: "firmware/new rollout, creating", recv: "FirmwareScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarPress(proseBarFirmwareForm(), "enter") },
		},
		{
			name: "firmware/new rollout, refused with a gateway page", recv: "FirmwareScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := proseBarPress(proseBarFirmwareForm(), "enter")
				next, _ := s.Update(fwRolloutCreatedMsg{err: errors.New(proseLoadGatewayPage)})
				return next.(proseBarScreen)
			},
		},

		// --- firmware: the version picker --------------------------------------
		{
			name: "firmware/version picker", recv: "FirmwareScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarFirmwarePicker(proseBarFirmwareVersions(proseBarFlatLongRows)), "j")
			},
		},
		{
			name: "firmware/version picker, one version", recv: "FirmwareScreen",
			build:    func() proseBarScreen { return proseBarFirmwarePicker(proseBarFirmwareVersions(1)) },
			immobile: pickerOne,
		},
		{
			name: "firmware/version picker, no versions", recv: "FirmwareScreen",
			build: func() proseBarScreen { return proseBarFirmwarePicker(nil) },
			immobile: "nothing to pick — enter and space go back to the form as esc does, " +
				"and the bar must say so",
		},
	}
	return append(out, proseBarOversizedSections()...)
}

// proseBarOversizedSections is every window this recipe introduced, each with a
// row — or, on the firmware sheet, a read-only section — taller than the pane.
func proseBarOversizedSections() []proseBarFixture {
	return []proseBarFixture{
		{
			name: "checklists/oversized row", recv: "ChecklistsScreen",
			build: func() proseBarScreen {
				rows := proseBarChecklistRows(1)
				rows[0].Name = proseBarTallName()
				s := NewChecklistsScreen(Deps{})
				next, _ := s.Update(checklistsLoadedMsg{rows: rows})
				return next.(*ChecklistsScreen)
			},
			immobile: "one checklist, so there is nowhere for the cursor to go",
		},
		{
			name: "firmware/oversized rollout", recv: "FirmwareScreen",
			build: func() proseBarScreen {
				s := proseBarFirmware("active", 1)
				s.rollouts[0].FirmwareVersionStr = proseBarTallName()
				return s
			},
			immobile: "one rollout, so there is nowhere for the cursor to go",
		},
		// The read-only sections outrunning the pane on their own, under a rollout
		// list that fits: the give-order's other half.
		{
			name: "firmware/oversized versions", recv: "FirmwareScreen",
			build: func() proseBarScreen {
				s := NewFirmwareScreen(Deps{})
				next, _ := s.Update(firmwareLoadedMsg{
					versions: proseBarFirmwareVersions(proseBarOversizedLines),
					rollouts: proseBarFirmwareRollouts("active", 1),
				})
				return next.(*FirmwareScreen)
			},
			immobile: "one rollout, and no key scrolls the sections under it",
		},
		{
			name: "firmware/version picker, oversized row", recv: "FirmwareScreen",
			build: func() proseBarScreen {
				versions := proseBarFirmwareVersions(1)
				versions[0].Version = proseBarTallName()
				return proseBarFirmwarePicker(versions)
			},
			immobile: "one version to pick, so there is nowhere for the cursor to go",
		},
	}
}

// TestProseBarSections_AnOversizedRowFitsAndMarksItsCut holds every window this
// recipe introduced to the bar the flat lists were held to: whatever is taller
// than the pane is clipped to what the pane leaves, the cut is MARKED, and the
// frame still fits — so the bar under it survives.
//
// WATCHED FAILING twice. Against HEAD, before the conversion, every one of these
// frames ran past 80x24 (48 to 51 rows against 18) and the clipped pane carried
// no `esc back`. Against the conversion, with proseWriteRows' clip taken out the
// three row fixtures ran past the pane again, and with renderView's tail cut
// taken out the versions fixture did — and so did the oversized rollout, whose
// short sections no longer fitted the lines the clipped row left them.
func TestProseBarSections_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	for _, f := range proseBarOversizedSections() {
		t.Run(f.name, func(t *testing.T) {
			s := proseBarSize(f.build(), width, height)
			if !proseBarFrameFits(s, height) {
				t.Fatalf("an oversized row pushed the frame past %dx%d, taking the bar with it:\n%s",
					width, height, s.View())
			}
			got := stripANSI(s.View())
			if !strings.Contains(got, " more lines") {
				t.Fatalf("an oversized row was clipped with no mark saying so:\n%s", got)
			}
			if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
				t.Fatalf("the bar is not the last thing on the pane:\n%s", got)
			}
		})
	}
}

// TestFirmware_NoMarkerPromisesWhatNoKeyFetches: with the cursor on the LAST
// rollout, nothing on the pane says there is more below.
//
// That is the reason the read-only sections are not in the rollouts' window: in
// it, they sit below a `↓ N more below` that j at the last rollout cannot move
// past. What is cut from them is marked `… N more lines` instead, which claims a
// cut and no key. Watched failing with the sections folded into the window.
func TestFirmware_NoMarkerPromisesWhatNoKeyFetches(t *testing.T) {
	for _, h := range []int{16, 24, 30} {
		s := proseBarSize(proseBarFirmware("active", 12), 80, h)
		for i := 0; i < 12; i++ {
			s = proseBarPress(s, "j")
		}
		got := stripANSI(s.View())
		if strings.Contains(got, "more below") {
			t.Errorf("at 80x%d, on the last rollout, the pane promises more below and j "+
				"can move no further:\n%s", h, got)
		}
		if !proseBarFrameFits(s, h) {
			t.Errorf("at 80x%d the frame runs past the pane:\n%s", h, got)
		}
	}
}

// TestChecklists_TabReachesTheSidebarAndShiftTabSwaps presses both keys through
// a real Root, because Root is what decides: a bare tab moves the keyboard into
// the sidebar before this screen sees it, so the swap the old footer credited to
// `tab` never happened, and shift+tab — named nowhere — was the key that did it.
func TestChecklists_TabReachesTheSidebarAndShiftTabSwaps(t *testing.T) {
	press := func(key string) (Root, *ChecklistsScreen) {
		screen := proseBarChecklists(3, 3)
		r := newTestRoot(screen)
		next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		next, _ = next.(Root).Update(listRuneKey(key))
		return next.(Root), screen
	}
	if r, s := press("tab"); !r.nav.Focused() || s.focusCompletions {
		t.Errorf("tab on the checklists: sidebar focused = %t, focus on runs = %t; want the "+
			"sidebar to take it and the sections left alone", r.nav.Focused(), s.focusCompletions)
	}
	if r, s := press("shift+tab"); r.nav.Focused() || !s.focusCompletions {
		t.Errorf("shift+tab on the checklists: sidebar focused = %t, focus on runs = %t; want "+
			"the focus swapped onto the runs", r.nav.Focused(), s.focusCompletions)
	}
	if bar := proseBarSize(proseBarChecklists(3, 3), 80, 24).proseBar(); bar.names("tab") || !bar.names("shift+tab") {
		t.Errorf("the checklist bar must name shift+tab and not tab: %s", bar.hint())
	}
}

// TestChecklists_AnEmptySectionNeverHoldsTheFocus: a load that leaves one
// section empty puts the focus on the other, so its rows are never drawn with
// every key that acts on them dead — the state a shop with runs and no active
// checklists used to open in.
func TestChecklists_AnEmptySectionNeverHoldsTheFocus(t *testing.T) {
	s := proseBarChecklists(0, 3)
	if !s.focusCompletions {
		t.Fatal("runs and no active checklists: the focus is on the empty section")
	}
	if _, cmd := s.Update(listRuneKey("enter")); cmd == nil {
		t.Error("enter on the runs opened nothing")
	}

	s = proseBarPress(proseBarChecklists(3, 3), "shift+tab").(*ChecklistsScreen)
	next, _ := s.Update(checklistsLoadedMsg{rows: proseBarChecklistRows(3)})
	if next.(*ChecklistsScreen).focusCompletions {
		t.Error("a refresh emptied the runs the focus was on, and the focus stayed there")
	}
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

var proseBarSectionsAt = time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

// proseBarChecklistRows carry a description on every other row, so the window
// packs rows of both heights.
func proseBarChecklistRows(n int) []omsapi.ChecklistSummary {
	out := make([]omsapi.ChecklistSummary, 0, n)
	for i := 0; i < n; i++ {
		row := omsapi.ChecklistSummary{
			ID:   fmt.Sprintf("cl-%d", i+1),
			Name: fmt.Sprintf("Laser cutter end-of-day shutdown, bay %d", i+1), StepCount: 6 + i%4,
			IsActive: true, IsPublic: i%3 == 0, SIGName: "Laser SIG",
		}
		if i%2 == 0 {
			row.Description = "Empty the exhaust trap, wipe the lens and log the tube hours"
		}
		out = append(out, row)
	}
	return out
}

func proseBarChecklistRuns(n int) []omsapi.ChecklistCompletion {
	out := make([]omsapi.ChecklistCompletion, 0, n)
	for i := 0; i < n; i++ {
		started := proseBarSectionsAt.Add(time.Duration(i) * time.Hour)
		out = append(out, omsapi.ChecklistCompletion{
			ID: fmt.Sprintf("run-%d", i+1), Checklist: fmt.Sprintf("cl-%d", i+1),
			ChecklistName:       fmt.Sprintf("Bandsaw blade change, station %d", i+1),
			UserUsername:        fmt.Sprintf("member%02d", i+1),
			StartedAt:           &started,
			Status:              "in_progress",
			CompletedStepsCount: i + 1, TotalStepsCount: 8,
			RequiredStepsCompleted: i, RequiredStepsTotal: 5,
		})
	}
	return out
}

func proseBarChecklists(rows, runs int) *ChecklistsScreen {
	s := NewChecklistsScreen(Deps{})
	next, _ := s.Update(checklistsLoadedMsg{rows: proseBarChecklistRows(rows), completions: proseBarChecklistRuns(runs)})
	return next.(*ChecklistsScreen)
}

func proseBarFirmwareRollouts(status string, n int) []forgekeyapi.FirmwareRollout {
	out := make([]forgekeyapi.FirmwareRollout, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, forgekeyapi.FirmwareRollout{
			ID: fmt.Sprintf("roll-%d", i+1), FirmwareVersionStr: fmt.Sprintf("2.%d.0", i+1),
			DeviceTypeName: "Door badge reader", Name: fmt.Sprintf("Wood shop wave %d", i+1),
			Status: status, BatchSizePercent: 20, IntervalMinutes: 60,
			Progress: forgekeyapi.FirmwareRolloutProgress{Total: 40, OnTarget: 12, Pending: 3, Remaining: 25},
		})
	}
	return out
}

func proseBarFirmwareVersions(n int) []forgekeyapi.FirmwareVersion {
	out := make([]forgekeyapi.FirmwareVersion, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, forgekeyapi.FirmwareVersion{
			ID: fmt.Sprintf("fv-%d", i+1), Version: fmt.Sprintf("2.%d.0", i+1),
			DeviceTypeName: "Door badge reader", DeviceTypeCode: "badge_reader",
			IsActive: true, CreatedByUsername: "fwadmin",
		})
	}
	return out
}

// proseBarFirmware is the firmware sheet with `n` rollouts of one status, a few
// versions and a few recent updates under them.
func proseBarFirmware(status string, n int) *FirmwareScreen {
	updates := make([]forgekeyapi.FirmwareUpdate, 0, 3)
	for i := 0; i < 3; i++ {
		updates = append(updates, forgekeyapi.FirmwareUpdate{
			ID: i + 1, DeviceMACAddress: fmt.Sprintf("24:6F:28:A1:B2:%02X", i),
			FirmwareVersionStr: "2.1.0", Status: "completed", RequestedAt: proseBarSectionsAt,
		})
	}
	s := NewFirmwareScreen(Deps{})
	next, _ := s.Update(firmwareLoadedMsg{
		versions: proseBarFirmwareVersions(4), updates: updates,
		rollouts: proseBarFirmwareRollouts(status, n),
	})
	return next.(*FirmwareScreen)
}

// proseBarFirmwareForm opens the New-Rollout form and picks a version, which is
// how an operator reaches the batch field: the picker hands the focus there.
func proseBarFirmwareForm() proseBarScreen {
	return proseBarPress(proseBarFirmware("active", 3), "c", "enter", "enter")
}

func proseBarFirmwarePicker(versions []forgekeyapi.FirmwareVersion) proseBarScreen {
	s := NewFirmwareScreen(Deps{})
	next, _ := s.Update(firmwareLoadedMsg{versions: versions})
	return proseBarPress(next.(proseBarScreen), "c", "enter")
}

// Compile-time: every screen this recipe converts states the record contract.
var (
	_ proseBarScreen = (*ChecklistsScreen)(nil)
	_ proseBarScreen = (*FirmwareScreen)(nil)
)
