package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_second_surface_test.go — the fixtures for the THIRD recipe taken out
// of proseBarUnconverted as a group rather than a screen at a time.
//
// THE RECIPE, and what makes these screens one group. Each is a list — windowed
// (the problem lists, the SIG members, the badge directory) or flat (the e-paper
// fleet, the check-ins), or in one case a one-field form — that draws a SECOND
// SURFACE in its place: a picker with its own cursor and often its own search
// box, a read-only detail, a prompt with a text box. Each surface wrote its own
// literal, so converting the list alone would have left the others behind a
// receiver the navigation classifier then counts as swept — which is what every
// one of these entries in proseBarUnconverted said stopped it. The answer is the
// same on all seven: proseBar answers for WHICHEVER surface is up, and the
// sweep is handed a fixture per surface.
//
// WHAT IS STILL nil IS WHAT WAS nil BEFORE: a one-line y/n confirm that names
// its own two keys, a write while it is out (a working line with every key
// held), and the badge enrolment panel, whose every key is "any key". Those are
// the states the earlier recipes left as literals, for the same reasons. A load
// in flight or failed used to be on this list and has a bar of its own now —
// prose_bar_load_states_test.go sweeps it.
//
// TWO THINGS THIS RECIPE NEEDED THAT THE EARLIER TWO DID NOT, both recorded
// where they are decided. A surface with a focused TEXT BOX takes the printable
// keys, which no record can spell, so a fixture says so (proseBarFixture.typing)
// and the reverse half stands down for exactly those keys. And a search picker
// whose box owns the letters moves on the ARROWS alone (proseNavArrows), where
// the step pair would name two letters the box eats.
//
// EVERY PICKER IS WINDOWED NOW, and that is the body half of the bar: each drew
// either a flat twelve rows under a four-line head, or every row, so a short
// pane took the bar the conversion moved under the rows. proseFlatListFrame is
// the window, and each new one has an OVERSIZED-ROW fixture below — one row
// taller than the whole pane — because a footer check whose fixtures cannot push
// the footer off is not checking the footer.

// proseBarSecondSurfaceFixtures is every screen on that recipe, in every surface
// it draws a bar on.
func proseBarSecondSurfaceFixtures() []proseBarFixture {
	var out []proseBarFixture
	for _, l := range proseBarSecondSurfaceWindowedLists() {
		out = append(out, proseBarListPair(l)...)
	}
	out = append(out, proseBarFlatTriple(proseBarFlatList{
		name: "e-paper panels", recv: "EPaperPanelsScreen", noun: "e-paper panel",
		build: func(rows int) proseBarScreen { return proseBarEPaper(rows) },
	})...)
	out = append(out, proseBarFlatTriple(proseBarFlatList{
		name: "location check-ins", recv: "LocationCheckinsScreen", noun: "check-in",
		build: func(rows int) proseBarScreen { return proseBarCheckins(rows) },
	})...)
	out = append(out, proseBarSecondSurfaceStates()...)
	return append(out, proseBarOversizedSurfaces()...)
}

// proseBarSecondSurfaceWindowedLists are the four screens whose LIST is on the
// windowed recipe, swept the way proseBarListPair sweeps that recipe.
func proseBarSecondSurfaceWindowedLists() []proseBarWindowedList {
	return []proseBarWindowedList{
		{
			name: "location problems", recv: "LocationProblemsScreen",
			build: func(rows int, _ proseBarRowName) proseBarScreen {
				return proseBarLocationProblems(rows)
			},
			immobile: "one open problem, so there is nowhere for the cursor to go",
		},
		{
			name: "asset problems", recv: "AssetProblemsScreen",
			build: func(rows int, _ proseBarRowName) proseBarScreen {
				return proseBarAssetProblems(proseBarAssetProblemRows(rows))
			},
			immobile: "one open problem, so there is nowhere for the cursor to go",
		},
		{
			name: "sig members", recv: "SIGMembersScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				return proseBarSIGMemberList(rows, name)
			},
			immobile: "one member, so there is nowhere for the cursor to go",
		},
		{
			name: "badge enrollment", recv: "BadgeEnrollmentScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				return proseBarBadges(rows, name)
			},
			immobile: "one member, so there is nowhere for the cursor to go",
		},
	}
}

// proseBarSecondSurfaceStates are the surfaces drawn IN PLACE of those lists,
// and the empty lists the windowed pair cannot reach.
func proseBarSecondSurfaceStates() []proseBarFixture {
	const box = "a focused text box takes every printable key"
	const report = "a read-only report, which binds no movement key"
	return []proseBarFixture{
		// --- location problems -------------------------------------------------
		{
			name: "location problems/empty", recv: "LocationProblemsScreen",
			build: func() proseBarScreen { return proseBarLocationProblems(0) },
			immobile: "no open problems — which is the point of this fixture: the movement " +
				"segments and the row actions must be absent, and `n`, `f`, `r` and `esc` named",
		},
		{
			name: "location problems/detail", recv: "LocationProblemsScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarLocationProblems(3), "enter") },
			immobile: report,
		},
		{
			name: "location problems/resolve", recv: "LocationProblemsScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarLocationProblems(3), "R") },
			immobile: "the resolve prompt, which binds no movement key", typing: box,
		},

		// --- asset problems ----------------------------------------------------
		{
			name: "asset problems/empty", recv: "AssetProblemsScreen",
			build: func() proseBarScreen { return proseBarAssetProblems(nil) },
			immobile: "no open problems, so the movement segments and every row action " +
				"must be absent",
		},
		{
			name: "asset problems/detail", recv: "AssetProblemsScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarAssetProblems(proseBarAssetProblemRows(3)), "enter")
			},
			immobile: report,
		},
		// A report that already went to BOTH kinds of work order: `w` and `t` come
		// off the bar and still close the report with a toast saying why, which is
		// a decline rather than an act.
		{
			name: "asset problems/detail, promoted", recv: "AssetProblemsScreen",
			build: func() proseBarScreen {
				rows := proseBarAssetProblemRows(1)
				wo, tp := "wo-1", "tp-1"
				rows[0].Status = omsapi.AssetProblemInProgress
				rows[0].WorkOrder, rows[0].WorkOrderShortID = &wo, "WO-0042"
				rows[0].ThirdPartyWorkOrder, rows[0].ThirdPartyWorkOrderShortID = &tp, "TP-0007"
				return proseBarPress(proseBarAssetProblems(rows), "enter")
			},
			immobile: report,
			declines: map[string]string{
				"w": "an in-house work order already exists; the arm closes the report and says so",
				"t": "a vendor work order already exists; the arm closes the report and says so",
			},
		},
		{
			name: "asset problems/resolve", recv: "AssetProblemsScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarAssetProblems(proseBarAssetProblemRows(3)), "R")
			},
			immobile: "the resolve prompt, which binds no movement key", typing: box,
		},
		{
			name: "asset problems/vendor picker", recv: "AssetProblemsScreen",
			build: func() proseBarScreen { return proseBarVendorPicker(proseBarFlatLongRows) },
		},
		{
			name: "asset problems/vendor picker, one vendor", recv: "AssetProblemsScreen",
			build:    func() proseBarScreen { return proseBarVendorPicker(1) },
			immobile: "one vendor to pick, so there is nowhere for the cursor to go",
		},
		{
			name: "asset problems/vendor title", recv: "AssetProblemsScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarVendorPicker(3), "enter") },
			immobile: "the scope-line prompt, which binds no movement key", typing: box,
		},
		// The work-type cycler: j, k and the vertical arrows turn it as space and
		// the side arrows do, so this fixture MOVES and names all six.
		{
			name: "asset problems/vendor work type", recv: "AssetProblemsScreen",
			build: func() proseBarScreen { return proseBarPress(proseBarVendorPicker(3), "enter", "enter") },
		},

		// --- SIG members -------------------------------------------------------
		{
			name: "sig members/empty", recv: "SIGMembersScreen",
			build: func() proseBarScreen { return proseBarSIGMemberList(0) },
			immobile: "no members, so the movement segments and `x` must be absent, and " +
				"`n`, `r` and `esc` named",
		},
		{
			name: "sig members/add picker", recv: "SIGMembersScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarSIGPicker(proseBarUsers(proseBarFlatLongRows)), "j")
			},
		},
		{
			name: "sig members/add picker, filtering", recv: "SIGMembersScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarSIGPicker(proseBarUsers(3)), "/") },
			immobile: "the filter box has the focus, so the arrows go into it", typing: box,
		},

		// --- badge enrollment --------------------------------------------------
		{
			name: "badge enrollment/empty", recv: "BadgeEnrollmentScreen",
			build: func() proseBarScreen { return proseBarBadges(0) },
			immobile: "no members, so the movement segments and the three row actions " +
				"must be absent",
		},
		{
			name: "badge enrollment/search", recv: "BadgeEnrollmentScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarBadges(3), "/") },
			immobile: "the search box has the focus, so the list's movement keys go into it",
			typing:   box,
		},
		{
			name: "badge enrollment/set badge", recv: "BadgeEnrollmentScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarBadges(3), "s") },
			immobile: "the manual badge prompt, which binds no movement key", typing: box,
		},

		// --- e-paper panels ----------------------------------------------------
		{
			name: "e-paper panels/bind picker", recv: "EPaperPanelsScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarEPaperBind(proseBarAssets(30)), "down")
			},
			typing: box,
		},
		{
			name: "e-paper panels/bind picker, no results", recv: "EPaperPanelsScreen",
			build: func() proseBarScreen { return proseBarEPaperBind(nil) },
			immobile: "nothing matched, so the arrows and `enter` must be absent and `esc` " +
				"still named",
			typing: box,
		},

		// --- location check-ins ------------------------------------------------
		{
			name: "location check-ins/entry", recv: "LocationCheckinsScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarCheckins(3), "n") },
			immobile: "the id-entry prompt, which binds no movement key", typing: box,
		},
		// With the NOTES box focused the lookup keys are letters in the note, so
		// `l/L/?` must come off the bar — the one state that shows it does.
		{
			name: "location check-ins/entry, notes", recv: "LocationCheckinsScreen",
			build:    func() proseBarScreen { return proseBarPress(proseBarCheckins(3), "n", "tab") },
			immobile: "the id-entry prompt, which binds no movement key", typing: box,
		},
		{
			name: "location check-ins/lookup", recv: "LocationCheckinsScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarCheckinLookup(proseBarLocationsNamed(proseBarFlatLongRows, "")), "down")
			},
			typing: box,
		},
		{
			name: "location check-ins/confirm", recv: "LocationCheckinsScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarCheckinLookup(proseBarLocationsNamed(3, "")), "enter")
			},
			immobile: "the confirm step, which binds no movement key",
		},

		// --- ForgeKey device form ----------------------------------------------
		{
			name: "forgekey device form", recv: "ForgeKeyDeviceFormScreen",
			build:    func() proseBarScreen { return proseBarDeviceForm(proseBarLocationsNamed(3, ""), nil) },
			immobile: "a one-field form, which binds no movement key",
		},
		{
			name: "forgekey device form/location picker", recv: "ForgeKeyDeviceFormScreen",
			build: func() proseBarScreen {
				return proseBarWalk(proseBarPress(proseBarDeviceForm(proseBarLocationsNamed(proseBarFlatLongRows, ""), nil), " "), "j")
			},
		},
		{
			name: "forgekey device form/location picker, filtering", recv: "ForgeKeyDeviceFormScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarDeviceForm(proseBarLocationsNamed(3, ""), nil), " ", "/")
			},
			immobile: "the filter box has the focus, so the arrows go into it", typing: box,
		},
	}
}

// ---------------------------------------------------------------------------
// The oversized rows
// ---------------------------------------------------------------------------

// proseBarOversizedLines is how many lines the oversized row carries: past the
// tallest pane Root draws, so no budget can hold it whole.
const proseBarOversizedLines = 45

// proseBarTallName is an OMS-supplied name with newlines in it — API prose can
// carry them — tall enough to outrun any pane.
func proseBarTallName() string {
	lines := make([]string, proseBarOversizedLines)
	for i := range lines {
		lines[i] = fmt.Sprintf("Mezzanine rack %02d", i+1)
	}
	return strings.Join(lines, "\n")
}

// proseBarOversizedSurfaces is every window proseFlatListFrame draws for this
// recipe, each with its cursor on a row taller than the pane.
func proseBarOversizedSurfaces() []proseBarFixture {
	one := func(noun string) string { return "one " + noun + ", so there is nowhere for the cursor to go" }
	const box = "a focused text box takes every printable key"
	return []proseBarFixture{
		{
			name: "e-paper panels/oversized row", recv: "EPaperPanelsScreen",
			build: func() proseBarScreen {
				s := NewEPaperPanelsScreen(Deps{})
				rows := proseBarEPaperRows(1)
				tall := proseBarTallName()
				rows[0].AssetName = &tall
				next, _ := s.Update(epaperPanelsLoadedMsg{rows: rows})
				return next.(*EPaperPanelsScreen)
			},
			immobile: one("e-paper panel"),
		},
		{
			name: "e-paper panels/bind picker, oversized row", recv: "EPaperPanelsScreen",
			build: func() proseBarScreen {
				assets := proseBarAssets(1)
				assets[0].Name = proseBarTallName()
				return proseBarEPaperBind(assets)
			},
			immobile: one("matching asset"), typing: box,
		},
		{
			name: "location check-ins/oversized row", recv: "LocationCheckinsScreen",
			build: func() proseBarScreen {
				s := NewLocationCheckinsScreen(Deps{})
				rows := proseBarCheckinRows(1)
				rows[0].LocationName = proseBarTallName()
				next, _ := s.Update(locationCheckinsLoadedMsg{rows: rows})
				return next.(*LocationCheckinsScreen)
			},
			immobile: one("check-in"),
		},
		{
			name: "location check-ins/lookup, oversized row", recv: "LocationCheckinsScreen",
			build: func() proseBarScreen {
				return proseBarCheckinLookup(proseBarLocationsNamed(1, proseBarTallName()))
			},
			immobile: one("matching location"), typing: box,
		},
		{
			name: "sig members/add picker, oversized row", recv: "SIGMembersScreen",
			build: func() proseBarScreen {
				users := proseBarUsers(1)
				users[0].DisplayName = proseBarTallName()
				return proseBarSIGPicker(users)
			},
			immobile: one("user to add"),
		},
		// The device picker always offers its "(none)" row, so the tall location
		// sits BETWEEN it and an ordinary one with the cursor on it: both steps
		// then move, which a cursor at either end could not show.
		{
			name: "forgekey device form/location picker, oversized row", recv: "ForgeKeyDeviceFormScreen",
			build: func() proseBarScreen {
				locs := proseBarLocationsNamed(2, "")
				locs[0].Name = proseBarTallName()
				id := locs[0].ID
				return proseBarPress(proseBarDeviceForm(locs, &id), " ")
			},
		},
		{
			name: "asset problems/vendor picker, oversized row", recv: "AssetProblemsScreen",
			build: func() proseBarScreen {
				s := proseBarPress(proseBarAssetProblems(proseBarAssetProblemRows(1)), "t")
				vendors := proseBarVendors(1)
				vendors[0].Name = proseBarTallName()
				next, _ := s.Update(assetProblemVendorsLoadedMsg{vendors: vendors})
				return next.(proseBarScreen)
			},
			immobile: one("vendor"),
		},
	}
}

// TestProseBarSecondSurface_AnOversizedRowFitsAndMarksItsCut holds every window
// this recipe introduced to the bar PR 199 set for the flat lists: a row taller
// than the pane is clipped to the body budget, the cut is MARKED, and the frame
// still fits — so the bar under it survives.
//
// WATCHED FAILING: with proseFlatListFrame's clip taken out, every one of these
// reports a frame past 80x24, because the one row outruns the pane on its own.
func TestProseBarSecondSurface_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	for _, f := range proseBarOversizedSurfaces() {
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

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// proseBarPress presses keys at a screen, in order, the way an operator reaches
// a surface — so a fixture cannot describe a state the keys cannot reach.
func proseBarPress(s proseBarScreen, keys ...string) proseBarScreen {
	for _, k := range keys {
		next, _ := s.Update(listRuneKey(k))
		s = next.(proseBarScreen)
	}
	return s
}

// proseBarWalk sizes a long surface and walks its cursor a few rows down, for
// the reason proseBarFlatSteps gives: these pickers bind no `end`, so a cursor
// left at the top would report the upward step as named and dead.
func proseBarWalk(s proseBarScreen, key string) proseBarScreen {
	s = proseBarSize(s, 80, 24)
	for i := 0; i < proseBarFlatSteps; i++ {
		s = proseBarPress(s, key)
	}
	return s
}

var proseBarSurfaceAt = time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

func proseBarLocationProblems(n int) *LocationProblemsScreen {
	rows := make([]omsapi.LocationProblem, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, omsapi.LocationProblem{
			ID: fmt.Sprintf("lp-%d", i+1), Location: 7,
			LocationName: "Wood shop, south wall",
			Description:  fmt.Sprintf("Dust collector gate %d sticks half open and starves the planer", i+1),
			Status:       omsapi.LocationProblemReported, StatusDisplay: "Reported",
			Severity: omsapi.LocationProblemSeverityHigh, SeverityDisplay: "High",
			ReportedBy: "member07", ReportedAt: proseBarSurfaceAt,
		})
	}
	s := NewLocationProblemsScreen(Deps{}, 7, "Wood shop, south wall")
	next, _ := s.Update(locationProblemsLoadedMsg{rows: rows})
	return next.(*LocationProblemsScreen)
}

func proseBarAssetProblemRows(n int) []omsapi.AssetProblem {
	rows := make([]omsapi.AssetProblem, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, omsapi.AssetProblem{
			ID: fmt.Sprintf("ap-%d", i+1), Asset: "a-1",
			AssetName:   "Haas VF-2SS vertical machining centre",
			Description: fmt.Sprintf("Spindle chiller alarm %d trips within ten minutes of warm-up", i+1),
			Status:      omsapi.AssetProblemReported,
			ReportedBy:  "member07", CreatedAt: proseBarSurfaceAt,
		})
	}
	return rows
}

func proseBarAssetProblems(rows []omsapi.AssetProblem) *AssetProblemsScreen {
	s := NewAssetProblemsScreen(Deps{}, "a-1", "Haas VF-2SS vertical machining centre")
	next, _ := s.Update(assetProblemsLoadedMsg{rows: rows})
	return next.(*AssetProblemsScreen)
}

// proseBarVendorPicker opens the send-to-vendor prompt on the first report and
// lands the vendor directory, which is the order an operator meets them in.
func proseBarVendorPicker(vendors int) proseBarScreen {
	s := proseBarPress(proseBarAssetProblems(proseBarAssetProblemRows(1)), "t")
	next, _ := s.Update(assetProblemVendorsLoadedMsg{vendors: proseBarVendors(vendors)})
	s = next.(proseBarScreen)
	if vendors > 1 {
		s = proseBarWalk(s, "j")
	}
	return s
}

func proseBarSIGMemberList(n int, names ...proseBarRowName) *SIGMembersScreen {
	members := proseBarSIGMembers(n)
	if len(names) > 0 {
		for i := range members {
			members[i].Username = names[0](i, members[i].Username)
		}
	}
	s := NewSIGMembersScreen(Deps{}, 3, "Metal Fabrication SIG")
	next, _ := s.Update(sigMembersLoadedMsg{members: members})
	return next.(*SIGMembersScreen)
}

// proseBarUsers are directory users none of whom is already a member of the
// fixture SIG, whose members are ids 1..n — so the picker offers every one.
func proseBarUsers(n int) []omsapi.User {
	out := make([]omsapi.User, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.User{
			ID: 1000 + i, Username: fmt.Sprintf("welder%02d", i+1),
			DisplayName: fmt.Sprintf("Alex Hernandez-Whitfield %d", i+1),
			Email:       fmt.Sprintf("welder%02d@example.org", i+1),
		})
	}
	return out
}

func proseBarSIGPicker(users []omsapi.User) proseBarScreen {
	s := proseBarPress(proseBarSIGMemberList(2), "n")
	next, _ := s.Update(sigUsersLoadedMsg{users: users})
	return next.(proseBarScreen)
}

// proseBarBadges leaves every other member with a badge, so `x` both clears and
// declines on the rows a walk crosses.
func proseBarBadges(n int, names ...proseBarRowName) *BadgeEnrollmentScreen {
	users := proseBarUsers(n)
	for i := range users {
		if len(names) > 0 {
			users[i].DisplayName = names[0](i, users[i].DisplayName)
		}
		if i%2 == 0 {
			badge := fmt.Sprintf("04A2%06X", i+1)
			users[i].BadgeNumber = &badge
		}
	}
	s := NewBadgeEnrollmentScreen(Deps{})
	next, _ := s.Update(badgeUsersLoadedMsg{users: users})
	return next.(*BadgeEnrollmentScreen)
}

// proseBarEPaperRows carry no timestamps: renderRelative measures against the
// wall clock, and a pane that changes between two reads of itself would report
// every key as acting.
func proseBarEPaperRows(n int) []forgekeyapi.EPaperDisplay {
	out := make([]forgekeyapi.EPaperDisplay, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("SawStop PCS 3HP cabinet saw, station %d", i+1)
		tag := fmt.Sprintf("WOOD-%04d", i+1)
		pct := 40 + i%60
		out = append(out, forgekeyapi.EPaperDisplay{
			ID:        fmt.Sprintf("5f0c2a7e-%04d-4c1e-9b1a-0d2f6c8e9a%02d", i, i%100),
			AssetName: &name, AssetTag: &tag,
			BatteryPercent:  &pct,
			FirmwareVersion: "1.4.2",
			IsActive:        i%5 != 4,
		})
	}
	return out
}

func proseBarEPaper(n int) *EPaperPanelsScreen {
	s := NewEPaperPanelsScreen(Deps{})
	next, _ := s.Update(epaperPanelsLoadedMsg{rows: proseBarEPaperRows(n)})
	return next.(*EPaperPanelsScreen)
}

func proseBarAssets(n int) []omsapi.Asset {
	out := make([]omsapi.Asset, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Asset{
			ID: fmt.Sprint(100 + i), Name: fmt.Sprintf("Powermatic 209HH planer %d", i+1),
			AssetTag: fmt.Sprintf("WOOD-%04d", 100+i), LocationName: "Wood shop, south wall",
		})
	}
	return out
}

// proseBarEPaperBind opens the bind picker on the first panel and lands the
// search it fires, stamped with the sequence the screen is waiting for.
func proseBarEPaperBind(results []omsapi.Asset) proseBarScreen {
	s := proseBarPress(proseBarEPaper(3), "b").(*EPaperPanelsScreen)
	next, _ := s.Update(epaperBindResultsMsg{seq: s.bindSeq, results: results})
	return next.(proseBarScreen)
}

func proseBarCheckinRows(n int) []omsapi.LocationCheckIn {
	out := make([]omsapi.LocationCheckIn, 0, n)
	kinds := []string{"member", "volunteer", "contractor"}
	for i := 0; i < n; i++ {
		who := fmt.Sprintf("member%02d", i+1)
		row := omsapi.LocationCheckIn{
			ID: fmt.Sprint(i + 1), Location: 7 + i,
			LocationName: fmt.Sprintf("Wood shop, south wall bay %d", i+1),
			CheckinType:  kinds[i%len(kinds)], UserUsername: &who,
			CheckedInAt: proseBarSurfaceAt.Add(time.Duration(i) * time.Minute),
		}
		if i%2 == 0 {
			row.Notes = "Swapped the planer knives and emptied the collector bin"
		}
		out = append(out, row)
	}
	return out
}

func proseBarCheckins(n int) *LocationCheckinsScreen {
	s := NewLocationCheckinsScreen(Deps{})
	next, _ := s.Update(locationCheckinsLoadedMsg{rows: proseBarCheckinRows(n)})
	return next.(*LocationCheckinsScreen)
}

// proseBarLocationsNamed builds n locations, the first called `first` when it is
// not empty.
func proseBarLocationsNamed(n int, first string) []omsapi.Location {
	out := proseBarLocations(n)
	for i := range out {
		out[i].Code = fmt.Sprintf("WS-%02d", i+1)
	}
	if first != "" && len(out) > 0 {
		out[0].Name = first
	}
	return out
}

// proseBarCheckinLookup opens the location lookup from the id entry and lands
// the search it fires, stamped with the sequence the screen is waiting for.
func proseBarCheckinLookup(locs []omsapi.Location) proseBarScreen {
	s := proseBarPress(proseBarCheckins(3), "n", "l").(*LocationCheckinsScreen)
	next, _ := s.Update(locationLookupMsg{seq: s.pickSeq, rows: locs})
	return next.(proseBarScreen)
}

func proseBarDeviceForm(locs []omsapi.Location, location *int) proseBarScreen {
	s := NewForgeKeyDeviceFormScreen(Deps{}, &forgekeyapi.Device{
		ID: 12, Name: "Wood shop south door reader", Location: location,
	})
	next, _ := s.Update(fkDeviceFormLocsMsg{locations: locs})
	return next.(proseBarScreen)
}

// Compile-time: every screen this recipe converts states the record contract.
var (
	_ proseBarScreen = (*LocationProblemsScreen)(nil)
	_ proseBarScreen = (*AssetProblemsScreen)(nil)
	_ proseBarScreen = (*SIGMembersScreen)(nil)
	_ proseBarScreen = (*BadgeEnrollmentScreen)(nil)
	_ proseBarScreen = (*EPaperPanelsScreen)(nil)
	_ proseBarScreen = (*LocationCheckinsScreen)(nil)
	_ proseBarScreen = (*ForgeKeyDeviceFormScreen)(nil)
)
