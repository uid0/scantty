package tui

import (
	"testing"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_report_table_test.go — the shared report table in the prose-bar
// sweeps.
//
// ONE SCREEN AND MANY REPORTS, which is why the fixtures below are drawn from
// more than one of them. ReportTableScreen is the type every tabbed report rides
// (reportScreenFixtures in report_yardstick_test.go derives that roster from the
// source), so its bar is one record — but what the record has to say depends on
// what the TAB holds: a lead-time table carrying a yardstick legend and dropping
// columns at 80, a two-column status count that drops nothing, one row, none.
// A sweep over a single report would prove the record honest on that report's
// shape and nothing else.
//
// WHAT IT WAS BEFORE: a string, footerHint, that nothing could press keys
// against, naming `esc back` and never the `backspace` updateKey binds beside it,
// and opening with the row count — a fact on a segment no key answers. The count
// rides the marker row now (ReportTableScreen.View), and the record is what every
// sweep in prose_bar_honesty_test.go and prose_bar_load_states_test.go reads.
//
// WHAT IS NOT HERE is the vertical give-order, which this screen had before it
// had a record and which the conversion did not touch: layoutRows and frameFits
// own it, and TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas holds it
// on every branch at every pane — now including the OVERSIZED ROW, a name with
// stored line breaks, which assembled 100 rows into an 18-row pane before
// reportCellOneLine and took the whole footer with it.

// proseBarReportTableFixtures builds the report table past its load, on two
// different reports and in the three row counts its bar changes shape across.
func proseBarReportTableFixtures() []proseBarFixture {
	return []proseBarFixture{
		// The REFRESH starting points for the load-state sweep, which refreshes
		// tab 0 — the tab a report opens on and the one Init loads.
		{
			name: "purchasing report", recv: "ReportTableScreen",
			build: func() proseBarScreen { return proseBarReport(NewPurchasingReportScreen, 0, 40, "") },
		},
		{
			name: "asset report", recv: "ReportTableScreen",
			build: func() proseBarScreen { return proseBarReport(NewAssetReportScreen, 0, 40, "") },
		},
		// The most crowded table in the program: seven columns, two of them
		// scored against a promise, so at 80 columns the pane draws the legend,
		// drops columns and names them under the rows — the most the layout
		// spends before the bar, which is where a bar budgeted by hand would be
		// the thing clampToBox took.
		{
			name: "purchasing report/lead time", recv: "ReportTableScreen",
			build: func() proseBarScreen {
				return proseBarReport(NewPurchasingReportScreen, 2, 40, omsapi.VarianceYardstickQuotedLeadTime)
			},
		},
		{
			name: "asset report/one row", recv: "ReportTableScreen",
			build: func() proseBarScreen { return proseBarReport(NewAssetReportScreen, 0, 1, "") },
			immobile: "one row, so there is nowhere for the cursor to go — which is the " +
				"point of this fixture: it is where the movement segments must be ABSENT, " +
				"and a table of forty can never show that",
		},
		{
			name: "inventory report/empty", recv: "ReportTableScreen",
			build: func() proseBarScreen { return proseBarReport(NewInventoryReportScreen, 0, 0, "") },
			immobile: "the tab loaded no rows, so the frame draws the fact in place of a " +
				"table and there is no cursor at all",
		},
	}
}

// proseBarReport is a report standing on `tab`, loaded with `rows` rows shaped the
// way the report sweeps shape them (reportSweepRows: full-length names, a
// distinct figure per cell), delivered through Update the way a load lands.
//
// The tab is set rather than switched to, because switching starts a load and
// what this builds is the frame after one.
func proseBarReport(build func(Deps) *ReportTableScreen, tab, rows int, yardstick string) *ReportTableScreen {
	s := build(Deps{})
	s.active = tab
	next, _ := s.Update(reportTabLoadedMsg{
		tab:       tab,
		rows:      reportSweepRows(s.tabs[tab].columns, rows),
		yardstick: yardstick,
	})
	return next.(*ReportTableScreen)
}

// revealLoad takes the ACTIVE tab out of its load, for
// TestProseBar_UnnamedLoadKeysDoNotEnterHiddenStates: that sweep clears a
// screen's own `loading` / `loadErr` fields to draw the frame a load was hiding,
// and this screen keeps its load per TAB (loadState), so the fields it would clear
// are not there. Declared here and not in the screen, because nothing but that
// sweep has any business pretending a load finished.
func (s *ReportTableScreen) revealLoad() {
	if len(s.tabs) == 0 {
		return
	}
	st := &s.states[s.active]
	st.loading, st.loaded, st.err = false, true, ""
}

// TestReportTable_BothWaysBackAreNamedWhereverTheyLeave: `esc` and `backspace`
// both return to the Reports hub from every state the report table is swept in,
// and the bar names both.
//
// A TARGETED CHECK BESIDE A GENERAL ONE, because the general one cannot see this
// key. TestProseBar_TheFooterNamesExactlyTheKeysThatWork asks the reverse half of
// the PANE only (its doc says why), and a way back changes nothing on the pane it
// was pressed on — it issues a switch — so a loaded bar that dropped `backspace`
// passed it: watched, by drawing proseBarEsc on the loaded frame. On a LOAD frame
// the key gate reads the bar, so an unnamed `backspace` stops acting there too and
// the biconditional holds by construction; only this asks that it still leaves.
// The literal this record replaced left `backspace` out on purpose, as an alias
// no other surface named, which is how a way off the screen came to be unnamed on
// every frame of it.
func TestReportTable_BothWaysBackAreNamedWhereverTheyLeave(t *testing.T) {
	swept := 0
	for _, f := range proseBarFixtures() {
		if f.recv != "ReportTableScreen" {
			continue
		}
		swept++
		for _, key := range []string{"esc", "backspace"} {
			s := proseBarSize(f.build(), 80, 24)
			if !s.proseBar().names(key) {
				t.Errorf("%s: the bar does not name %q.\nbar: %s", f.name, key, s.proseBar().hint())
			}
			_, cmd := s.Update(listRuneKey(key))
			if cmd == nil {
				t.Errorf("%s: %q issued nothing", f.name, key)
				continue
			}
			sm, ok := cmd().(SwitchScreenMsg)
			if !ok || sm.Workspace != WSReports {
				t.Errorf("%s: %q did not return to the Reports hub (got %T)", f.name, key, cmd())
			}
		}
	}
	if swept == 0 {
		t.Fatal("no report table fixture was built, so this asserted nothing")
	}
}
