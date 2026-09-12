package tui

import (
	"fmt"
	"time"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_flat_lists_test.go — the fixtures for the FLAT cursor lists, the
// second recipe taken out of proseBarUnconverted as a group rather than a screen
// at a time.
//
// THE RECIPE, and what makes these screens one group. Each is a single cursor
// list with no second surface drawn in its place; each binds j/k and the arrows
// in its own switch and NO other movement keystroke; each wrote its footer as a
// literal naming `j/k move` alone, so the arrows moved the cursor unnamed; and
// each drew EVERY row and then that footer, with no window at all — so a list
// longer than the pane took the footer off the bottom whatever it said. Their
// rows are also not one line each (a meta line, a notes line, a contact line,
// present or absent per row), which is why a row-counting window would have been
// the AssetPartsScreen defect under a new name. proseNavStep and
// proseFlatListFrame (prose_bar.go) are the shared answer.
//
// A FLAT LIST NOT HERE is named in proseBarUnconverted with what stopped it: a
// second cursor surface drawn in place of the list, a footer that changes with a
// form's state, or a movement vocabulary this recipe's step pair does not
// describe.
//
// THREE STATES PER SCREEN, each for a condition the others cannot reach. The
// LONG list is the one that outruns the pane, so it is the only one where the
// window and both markers are drawn and where the movement keys must be named.
// Its cursor is walked a few rows down through the keys, because these screens
// bind no `end`: from the top `k` clamps, and the movement sweep's `end` probe
// cannot move it anywhere either, so a list standing at row 0 would report `k`
// as named and dead. The ONE-ROW list is where the movement segment must come
// OFF. The EMPTY list is where every row action must come off too, and where the
// way out must still be named — several of these drew the fact alone there,
// with `r` and `esc` working under no word.
//
// THE ROW BUILDERS CARRY REAL LENGTHS and a second line on most rows, for the
// vacuous-fixture rule (AGENTS.md): a fixture of one-line rows cannot show a
// window packed by lines.

// proseBarFlatListFixtures is every screen on that recipe, in all three states.
func proseBarFlatListFixtures() []proseBarFixture {
	var out []proseBarFixture
	for _, l := range proseBarFlatLists() {
		out = append(out, proseBarFlatTriple(l)...)
	}
	return out
}

// proseBarFlatList is one screen of the recipe.
type proseBarFlatList struct {
	name string
	recv string
	// build loads the screen holding `n` rows.
	build func(rows int) proseBarScreen
	// noun is what one row is, for the immobile reasons.
	noun string
}

// proseBarFlatLongRows is how many rows the LONG fixture holds: more than the
// tallest pane Root draws (jdePaneHeights stops at a 40-row terminal) can hold
// even at one line a row. Thirty was enough for every screen whose rows carry a
// second line and NOT for the operational-mode list, whose rows are one line
// each — so at the tallest panes it fitted whole, and a frame drawing every row
// with no window at all passed there. Watched: removing the window reported the
// multi-line lists and not that one.
const proseBarFlatLongRows = 45

// proseBarFlatSteps is how far down the long fixture's cursor is walked before
// the sweep presses anything. Far enough that `k` moves, near enough that the
// cursor is still on the first screenful at 80x24.
const proseBarFlatSteps = 3

func proseBarFlatTriple(l proseBarFlatList) []proseBarFixture {
	return []proseBarFixture{
		{
			name: l.name, recv: l.recv,
			build: func() proseBarScreen {
				s := proseBarSize(l.build(proseBarFlatLongRows), 80, 24)
				for i := 0; i < proseBarFlatSteps; i++ {
					next, _ := s.Update(listRuneKey("j"))
					s = next.(proseBarScreen)
				}
				return s
			},
		},
		{
			name: l.name + "/one row", recv: l.recv,
			build:    func() proseBarScreen { return l.build(1) },
			immobile: "one " + l.noun + ", so there is nowhere for the cursor to go",
		},
		{
			name: l.name + "/empty", recv: l.recv,
			build: func() proseBarScreen { return l.build(0) },
			immobile: "no " + l.noun + " at all — which is the point of this fixture: the " +
				"movement segment and every row action must be absent, and the way out named",
		},
	}
}

func proseBarFlatLists() []proseBarFlatList {
	return []proseBarFlatList{
		{
			name: "donations", recv: "DonationsScreen", noun: "donation",
			build: func(rows int) proseBarScreen {
				s := NewDonationsScreen(Deps{})
				next, _ := s.Update(donationsLoadedMsg{rows: proseBarDonations(rows)})
				return next.(*DonationsScreen)
			},
		},
		{
			name: "operational modes", recv: "OperationalModesScreen", noun: "operational mode",
			build: func(rows int) proseBarScreen {
				s := NewOperationalModesScreen(Deps{})
				next, _ := s.Update(opModesLoadedMsg{rows: proseBarOpModes(rows)})
				return next.(*OperationalModesScreen)
			},
		},
		{
			name: "usage sessions", recv: "UsageScreen", noun: "usage session",
			build: func(rows int) proseBarScreen {
				s := NewUsageScreen(Deps{})
				next, _ := s.Update(usageLoadedMsg{rows: proseBarUsage(rows)})
				return next.(*UsageScreen)
			},
		},
		{
			name: "vendors", recv: "VendorsScreen", noun: "vendor",
			build: func(rows int) proseBarScreen {
				s := NewVendorsScreen(Deps{})
				next, _ := s.Update(vendorsLoadedMsg{rows: proseBarVendors(rows)})
				return next.(*VendorsScreen)
			},
		},
		{
			name: "authorizations", recv: "AuthorizationsScreen", noun: "authorization",
			build: func(rows int) proseBarScreen {
				s := NewAuthorizationsScreen(Deps{})
				next, _ := s.Update(fkListLoadedMsg{auths: proseBarAuthorizations(rows)})
				return next.(*AuthorizationsScreen)
			},
		},
		{
			name: "lockouts", recv: "LockoutsScreen", noun: "lockout",
			build: func(rows int) proseBarScreen {
				s := NewLockoutsScreen(Deps{})
				next, _ := s.Update(fkListLoadedMsg{lockouts: proseBarLockouts(rows)})
				return next.(*LockoutsScreen)
			},
		},
		{
			name: "pm board", recv: "PMBoardScreen", noun: "PM schedule",
			build: func(rows int) proseBarScreen {
				s := NewPMBoardScreen(Deps{})
				next, _ := s.Update(pmBoardLoadedMsg{board: proseBarPMBoard(rows)})
				return next.(*PMBoardScreen)
			},
		},
	}
}

func proseBarDonations(n int) []omsapi.Donation {
	out := make([]omsapi.Donation, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Donation{
			ID:               fmt.Sprint(i + 1),
			DonorName:        fmt.Sprintf("Central Texas Woodworkers Guild, chapter %d", i+1),
			DonationNumber:   fmt.Sprintf("DON-2026-%04d", i+1),
			Status:           "received",
			DateReceived:     omsapi.DateOnly{Time: time.Date(2026, 3, 1+i%28, 0, 0, 0, 0, time.UTC)},
			TaxReceiptNumber: fmt.Sprintf("TR-%05d", 880+i),
			EstimatedValue:   omsapi.DecimalString("1250.00"),
		})
	}
	return out
}

// proseBarFlatAt is a fixed instant for the timestamps these rows carry, so a
// frame is the same frame on every run.
var proseBarFlatAt = time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)

func proseBarOpModes(n int) []forgekeyapi.OperationalMode {
	modes := []string{"AVAILABLE", "LOCKED_OUT", "CLASSROOM"}
	out := make([]forgekeyapi.OperationalMode, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, forgekeyapi.OperationalMode{
			ID: i + 1, Asset: 100 + i,
			AssetName:            fmt.Sprintf("SawStop PCS 3HP cabinet saw, station %d", i+1),
			AssetLocationName:    "Wood shop, south wall",
			Mode:                 modes[i%len(modes)],
			ClassroomModeEnabled: i%len(modes) == 2,
		})
	}
	return out
}

// proseBarUsage leaves every third session ENDED, so the active-count line the
// list opens with is drawn and the rows carry both shapes of the second line.
func proseBarUsage(n int) []forgekeyapi.Usage {
	out := make([]forgekeyapi.Usage, 0, n)
	for i := 0; i < n; i++ {
		u := forgekeyapi.Usage{
			ID: i + 1, Asset: 100 + i, User: 200 + i,
			UserName:  fmt.Sprintf("member%02d", i+1),
			AssetName: fmt.Sprintf("Epilog Fusion Pro 48 laser, bay %d", i+1),
			StartedAt: proseBarFlatAt,
		}
		if i%3 == 2 {
			ended := proseBarFlatAt.Add(90 * time.Minute)
			u.EndedAt = &ended
		}
		out = append(out, u)
	}
	return out
}

func proseBarVendors(n int) []omsapi.Vendor {
	out := make([]omsapi.Vendor, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Vendor{
			ID: fmt.Sprint(i + 1), IsActive: i%5 != 4,
			Name:                 fmt.Sprintf("Lone Star Electrical Contractors, crew %d", i+1),
			VendorKind:           "electrical",
			VendorKindDisplay:    "Electrician",
			ContactName:          "Maria Delgado",
			Phone:                "512-555-0142",
			Email:                "dispatch@lonestar-electric.example",
			TDLRLicenseNumber:    fmt.Sprintf("ECL-%05d", 31000+i),
			TDLRLicenseExpiresAt: omsapi.DateOnly{Time: proseBarFlatAt.AddDate(1, 0, 0)},
			COIProvider:          "Texas Mutual",
			COIExpiresAt:         omsapi.DateOnly{Time: proseBarFlatAt.AddDate(0, 6, 0)},
		})
	}
	return out
}

// proseBarAuthorizations leaves every fourth grant REVOKED, but not the row the
// long fixture's cursor stands on, so `x` there opens the confirm.
func proseBarAuthorizations(n int) []forgekeyapi.Authorization {
	out := make([]forgekeyapi.Authorization, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, forgekeyapi.Authorization{
			ID: i + 1, Asset: 100 + i, User: 200 + i,
			UserName:  fmt.Sprintf("member%02d", i+1),
			AssetName: fmt.Sprintf("Haas TM-1P toolroom mill, cell %d", i+1),
			IsActive:  i%4 != 1,
			Notes:     "Completed the mill safety class and a supervised first job",
		})
	}
	return out
}

func proseBarLockouts(n int) []forgekeyapi.Lockout {
	out := make([]forgekeyapi.Lockout, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, forgekeyapi.Lockout{
			ID: i + 1, Asset: 100 + i, LockedBy: 7,
			AssetName:    fmt.Sprintf("Jet 16-inch bandsaw, wood shop station %d", i+1),
			LockoutLevel: "asset",
			IsActive:     i%4 != 1,
			Reason:       "Blade guard cracked; do not run until the guard is replaced",
			LockedAt:     proseBarFlatAt,
		})
	}
	return out
}

func proseBarPMBoard(n int) *omsapi.PMBoard {
	statuses := []string{"overdue", "warning", "ok", "never"}
	board := &omsapi.PMBoard{Count: n}
	for i := 0; i < n; i++ {
		due := 14 - 3*i
		location := "Machine shop, north bay"
		board.Schedules = append(board.Schedules, omsapi.PMBoardEntry{
			ID: fmt.Sprintf("pm-%d", i+1), AssetID: fmt.Sprint(100 + i),
			AssetName:    fmt.Sprintf("Haas VF-2SS machining centre %d", i+1),
			TaskName:     "Way lube top-up and chip auger check",
			LocationName: &location,
			IntervalDays: 30,
			DaysUntilDue: &due,
			Status:       statuses[i%len(statuses)],
		})
	}
	return board
}
