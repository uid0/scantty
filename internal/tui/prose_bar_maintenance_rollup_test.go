package tui

import (
	"errors"
	"fmt"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_maintenance_rollup_test.go — the proseBar fixtures for the two
// maintenance-rollup surfaces: the PM due list (a flat list whose confirm is the
// foot under its rows) and the rollup sheet (a scroller).
//
// Every state the bars change shape in is here: `W` comes and goes with the week
// list and with a write in flight, the confirm has a bar of its own, and the
// rollup's `f` names the filter it would switch TO.

var pmDueFixtureNow = time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)

// pmDueItems is `n` items at the lengths OMS really serves: a real PM task
// title, a real machine name and tag, and materials on every other row. The
// titles differ at the FRONT so a clipped row still tells rows apart.
func pmDueItems(prefix string, n int, overdueEvery int, dueIn time.Duration) []omsapi.MaintenanceItem {
	out := make([]omsapi.MaintenanceItem, 0, n)
	for i := 0; i < n; i++ {
		next := pmDueFixtureNow.Add(dueIn)
		it := omsapi.MaintenanceItem{
			ID:        fmt.Sprintf("%s-%d", prefix, i+1),
			Asset:     fmt.Sprintf("asset-%d", i%3+1),
			AssetName: "SawStop PCS 3HP Professional Cabinet Table Saw",
			AssetTag:  fmt.Sprintf("WS-%04d", 40+i),
			Title:     fmt.Sprintf("%02d Replace the blade brake cartridge and test the flesh sensor", i+1),
			NextDueAt: &next,
		}
		if overdueEvery > 0 && i%overdueEvery == 0 {
			days := 7 + i
			late := pmDueFixtureNow.AddDate(0, 0, -days)
			it.IsOverdue, it.DaysOverdue, it.NextDueAt = true, &days, &late
			if i%(overdueEvery*2) == 0 {
				// Never completed: overdue with no date to be late against.
				it.DaysOverdue, it.NextDueAt = nil, nil
			}
		}
		if i%2 == 0 {
			it.Materials = []omsapi.MaintenanceMaterial{{Name: "Brake cartridge"}}
		}
		out = append(out, it)
	}
	return out
}

// pmDueFixture is a loaded PM due list with `week` items due this week (every
// third overdue) and `monthOnly` more due later in the month.
func pmDueFixture(week, monthOnly int) *PMDueScreen {
	w := pmDueItems("wk", week, 3, 48*time.Hour)
	m := append(append([]omsapi.MaintenanceItem{}, w...), pmDueItems("mo", monthOnly, 0, 20*24*time.Hour)...)
	s := NewPMDueScreen(Deps{})
	s.now = func() time.Time { return pmDueFixtureNow }
	next, _ := s.Update(pmDueLoadedMsg{week: w, month: m})
	return next.(*PMDueScreen)
}

func pmDueWalked(week, monthOnly int) proseBarScreen {
	return proseBarWalk(pmDueFixture(week, monthOnly), "j")
}

// maintenanceRollupFixture is a loaded rollup long enough to scroll at every
// pane the sweep draws.
func maintenanceRollupFixture(rows int) *MaintenanceRollupScreen {
	s := NewMaintenanceRollupScreen(Deps{})
	dash := &omsapi.MaintenanceDashboard{Costs: omsapi.MaintenanceCosts{PerPeriod: omsapi.MaintenanceCostPeriods{
		Today: "89.50", ThisWeek: "89.50", ThisMonth: "412.00", ThisYear: "1052.00", AllTime: "12345.67",
	}}}
	active := &omsapi.ActiveMaintenance{}
	kinds := []string{omsapi.ActiveKindWorkOrder, omsapi.ActiveKindAssetProblem, omsapi.ActiveKindLocationProblem}
	for i := 0; i < rows; i++ {
		days := i - 3
		next := pmDueFixtureNow.AddDate(0, 0, days)
		name := "Epilog Fusion Pro 48 CO2 Laser Engraver"
		dash.ScheduledPM = append(dash.ScheduledPM, omsapi.MaintenanceScheduledPM{
			AssetID: "a1", AssetName: name, MaintenanceItemID: fmt.Sprintf("pm-%d", i),
			Title: fmt.Sprintf("%02d Clean the optics, mirrors and focus lens", i), IntervalDays: 14,
			NextDue: &next, DaysUntil: &days, LastCompletedAt: &pmDueFixtureNow, IsOverdue: days < 0,
		})
		dash.Unscheduled = append(dash.Unscheduled, omsapi.MaintenanceUnscheduledWorkOrder{
			WorkOrderID: fmt.Sprintf("wo-%d", i), ShortID: fmt.Sprintf("WO-%08X", i), AssetName: name,
			Problem: "Red dot pointer flickers when the head moves", OpenedAt: pmDueFixtureNow, Status: "open",
		})
		dash.Costs.ByAsset = append(dash.Costs.ByAsset, omsapi.MaintenanceAssetCost{
			AssetID: "a1", AssetName: name, TotalCost: "640.00", DaysInMaintenance90Days: 3,
		})
		row := omsapi.ActiveMaintenanceRow{
			Kind: kinds[i%3], ID: fmt.Sprintf("act-%d", i), ShortID: fmt.Sprintf("%08X", i),
			Title: "Dust collector blast gate sticks half open", Status: "reported",
			StatusDisplay: "Reported", OpenedAt: pmDueFixtureNow,
		}
		if row.Kind == omsapi.ActiveKindLocationProblem {
			loc, sev := "Wood shop", "high"
			row.LocationName, row.Severity = &loc, &sev
		} else {
			row.AssetName = &name
		}
		active.Results = append(active.Results, row)
	}
	active.Count = len(active.Results)
	next, _ := s.Update(maintenanceRollupLoadedMsg{dashboard: dash, active: active})
	return next.(*MaintenanceRollupScreen)
}

func proseBarMaintenanceRollupFixtures() []proseBarFixture {
	return []proseBarFixture{
		// --- PM due -----------------------------------------------------------
		// The long list with its cursor walked down: movement, both row keys and
		// `W` all named.
		{name: "pm due", recv: "PMDueScreen",
			build: func() proseBarScreen { return pmDueWalked(proseBarFlatLongRows, 5) }},
		{name: "pm due/one row", recv: "PMDueScreen",
			build:    func() proseBarScreen { return pmDueFixture(1, 0) },
			immobile: "one due item, so there is nowhere for the cursor to go"},
		// Nothing due this week, something later in the month: the rows are
		// there and `W` is NOT — the server would create nothing.
		{name: "pm due/month only", recv: "PMDueScreen",
			build: func() proseBarScreen { return pmDueWalked(0, proseBarFlatLongRows) }},
		{name: "pm due/empty", recv: "PMDueScreen",
			build: func() proseBarScreen { return pmDueFixture(0, 0) },
			immobile: "nothing due at all — the point of this fixture: the movement segment, the row " +
				"keys and `W` must be absent, and `r` and `esc` named"},
		// The confirm: its own bar under the rows, and nothing else acts.
		{name: "pm due/confirm", recv: "PMDueScreen",
			build: func() proseBarScreen {
				return proseBarPress(pmDueWalked(proseBarFlatLongRows, 5), "W")
			},
			immobile: "the confirm holds the keyboard: only y, n and esc answer it"},
		// A generation out: `W` comes off and the working line leads the list.
		{name: "pm due/generating", recv: "PMDueScreen",
			build: func() proseBarScreen {
				return proseBarPress(pmDueWalked(proseBarFlatLongRows, 5), "W", "y")
			}},
		// The server's answer, fewer than were due, on the pane after the reload.
		{name: "pm due/after a generation", recv: "PMDueScreen",
			build: func() proseBarScreen {
				s := proseBarPress(pmDueWalked(proseBarFlatLongRows, 5), "W", "y")
				next, _ := s.Update(pmDueGeneratedMsg{result: &omsapi.MaintenanceBulkGeneration{Created: 3}, due: proseBarFlatLongRows})
				s = next.(proseBarScreen)
				next, _ = s.Update(pmDueLoadedMsg{week: pmDueItems("wk", proseBarFlatLongRows, 3, 48*time.Hour),
					month: pmDueItems("wk", proseBarFlatLongRows, 3, 48*time.Hour)})
				return next.(proseBarScreen)
			}},
		{name: "pm due/generation refused", recv: "PMDueScreen",
			build: func() proseBarScreen {
				s := proseBarPress(pmDueWalked(proseBarFlatLongRows, 5), "W", "y")
				next, _ := s.Update(pmDueGeneratedMsg{err: errors.New(proseLoadGatewayPage), due: proseBarFlatLongRows})
				return next.(proseBarScreen)
			}},
		{name: "pm due/oversized row", recv: "PMDueScreen",
			build: func() proseBarScreen {
				s := pmDueFixture(1, 0)
				s.rows[0].item.Title = proseBarTallName()
				return s
			},
			immobile: "one due item, so there is nowhere for the cursor to go"},

		// --- maintenance rollup -----------------------------------------------
		{name: "maintenance rollup", recv: "MaintenanceRollupScreen",
			build: func() proseBarScreen { return proseBarSize(maintenanceRollupFixture(12), 80, 24) }},
		{name: "maintenance rollup/filtered", recv: "MaintenanceRollupScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarSize(maintenanceRollupFixture(12), 80, 24), "f", "f")
			}},
		{name: "maintenance rollup/nothing open", recv: "MaintenanceRollupScreen",
			build: func() proseBarScreen { return proseBarSize(maintenanceRollupFixture(0), 80, 40) }},
	}
}

var (
	_ proseBarScreen = (*PMDueScreen)(nil)
	_ proseBarScreen = (*MaintenanceRollupScreen)(nil)
)
