package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_row_packed_lists_test.go — the fixtures for the SIXTH recipe taken out
// of proseBarUnconverted as a group rather than a screen at a time.
//
// THE RECIPE, and what makes these three one group. Each is a cursor list whose
// rows are SEVERAL LINES — a forecast row is a title, a meta line and sometimes a
// history line; a supplier link is a name, its folded facts and a URL — and each
// had already taught its window to pack rows by how many lines they cost, which
// is exactly what AssetPartsScreen is recorded as missing. What none of them had
// was a bar that could be asked what it claims, or a budget that knew the bar
// folds:
//
//   - the two FORECASTS priced a row from a cost table (two lines, three with a
//     date) under a flat two-row footer, named four of the ten movement
//     keystrokes their switch binds, and wrote a detail literal of `j/k scroll ·
//     pgup/pgdn page` over a TextScroller that binds all ten and `backspace`
//     beside `esc`. The serialized forecast's empty unfiltered list left `w` off
//     its bar while `w` swapped the view.
//   - the SUPPLIER LIST already derived its budget from the folded footer and
//     marked a row cut by the pane; its literal named `j/k move` alone while the
//     arrows, the pager, g/G/home/end and `enter` all acted, named `p primary` on
//     the row that already is, and drew the bar under a working line whose write
//     held every key it named.
//
// The forecasts now pack by the RENDERED rows through proseFlatListFrame, whose
// budget the pager reads too (proseFlatListBudget), so a stored newline in an
// item name is a row the window can measure and the oversized fixture below can
// cut. The supplier list keeps its own line arithmetic, which the
// proseBarUnconverted entry said was the right one, taught to budget against the
// bar's CEILING.
//
// THE WINDOW HALF IS HELD BY proseBarAssertMultiLineWindows, the same check the
// windowed lists and the second-surface lists are held to: an oversized row
// taller than any pane, standing on it, must fit the pane and say it was cut.
// The supplier list's cut mark is its own sentence (suppliersRowCutMark), which
// is why the check takes the mark from the list rather than assuming one.

// proseBarRowPackedListFixtures is every screen on that recipe, in every state it
// draws a bar in.
func proseBarRowPackedListFixtures() []proseBarFixture {
	var out []proseBarFixture
	for _, l := range proseBarRowPackedLists() {
		out = append(out, proseBarListPair(l)...)
	}
	return append(out,
		// --- serialized forecast -----------------------------------------------
		proseBarFixture{
			name: "serialized forecast/empty", recv: "SerializedForecastScreen",
			build: func() proseBarScreen { return proseBarSerializedForecast(0, proseBarSameName) },
			immobile: "no rows — the state whose literal left `w` off while `w` swapped the " +
				"view; the movement segments and `enter` must be absent",
		},
		proseBarFixture{
			name: "serialized forecast/empty, low-stock only", recv: "SerializedForecastScreen",
			build: func() proseBarScreen {
				s := proseBarSerializedForecast(0, proseBarSameName)
				s.lowOnly = true
				return s
			},
			immobile: "no rows under the low-stock filter, where `w` reads `show all`",
		},
		proseBarFixture{
			name: "serialized forecast/detail", recv: "SerializedForecastScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarSerializedForecast(3, proseBarSameName), "enter")
			},
		},
		proseBarFixture{
			name: "serialized forecast/detail, raw", recv: "SerializedForecastScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarSerializedForecast(3, proseBarSameName), "enter", "r")
			},
		},

		// --- demand forecast ---------------------------------------------------
		proseBarFixture{
			name: "demand forecast/empty", recv: "DemandForecastScreen",
			build:    func() proseBarScreen { return proseBarDemandForecast(false, 0, proseBarSameName) },
			immobile: "no stored forecasts, so the movement segments and `enter` must be absent",
		},
		// THE ALERTS VIEW, which is the one state where `w` does nothing — the
		// notify set is server-filtered — and so the one state that can show the
		// key coming off.
		proseBarFixture{
			name: "demand forecast/alerts", recv: "DemandForecastScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarSize(proseBarDemandForecast(true, 30, proseBarSameName), 80, 24), "pgdown")
			},
		},
		proseBarFixture{
			name: "demand forecast/detail", recv: "DemandForecastScreen",
			build: func() proseBarScreen {
				return proseBarPress(proseBarDemandForecast(false, 3, proseBarSameName), "enter")
			},
		},

		// --- item suppliers ----------------------------------------------------
		proseBarFixture{
			name: "item suppliers/empty", recv: "ItemSuppliersScreen",
			build: func() proseBarScreen { return proseBarItemSuppliers(0, proseBarSameName) },
			immobile: "no supplier links, so the movement segments and every row action must " +
				"be absent while `c add` stays named",
		},
		// THE PRIMARY ROW, where `p` only warns that it already is: the one state
		// that can show the key coming off, which a list whose cursor stands on a
		// plain link never reaches.
		proseBarFixture{
			name: "item suppliers/on the primary link", recv: "ItemSuppliersScreen",
			build: func() proseBarScreen {
				s := proseBarItemSuppliers(1, proseBarSameName)
				s.rows[0].IsPreferred = true
				return s
			},
			immobile: "one link, so there is nowhere for the cursor to go",
		},
		// AFTER A STALE REFUSAL, where the count line gives its slot to the
		// standing note that the list is out of date. The bar is the same bar —
		// the note names `r`, which it already carries — so the state is swept
		// for that claim holding on a frame drawing one more error row's worth
		// of prose rather than for a new key.
		proseBarFixture{
			name: "item suppliers/after a stale refusal", recv: "ItemSuppliersScreen",
			build: func() proseBarScreen {
				s := proseBarItemSuppliers(3, proseBarSameName)
				next, _ := s.Update(itemSupplierPrimaryMsg{err: supLinkStaleErr(supLinkStalePrimary)})
				return next.(*ItemSuppliersScreen)
			},
		},
	)
}

func proseBarRowPackedLists() []proseBarWindowedList {
	return []proseBarWindowedList{
		{
			name: "serialized forecast", recv: "SerializedForecastScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				return proseBarSerializedForecast(rows, name)
			},
			reference: proseBarTwoLineName,
			immobile:  "one forecast row, so there is nowhere for the cursor to go",
		},
		{
			name: "demand forecast", recv: "DemandForecastScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				return proseBarDemandForecast(false, rows, name)
			},
			reference: proseBarTwoLineName,
			immobile:  "one forecast row, so there is nowhere for the cursor to go",
		},
		{
			name: "item suppliers", recv: "ItemSuppliersScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				return proseBarItemSuppliers(rows, name)
			},
			immobile: "one supplier link, so there is nowhere for the cursor to go",
		},
	}
}

// proseBarTwoLineName is the fit BOUNDARY's reference names for a list whose row
// already draws a meta line under the name: see proseBarWindowedList.reference.
// Measured against one-line names, a forecast row of two lines spends less than
// proseListWindowFloor's three and fits by coincidence in exactly the band where
// the floor makes every frame overrun, so the check would report the floor.
func proseBarTwoLineName(_ int, base string) string {
	return base + "\nsecond line of the stored name"
}

// TestProseBarRowPackedList_MultiLineNamesFitThePaneAndMarkTheirCut holds the
// forecasts' window with the check the other windowed recipes use.
//
// THE SUPPLIER LIST IS NOT HANDED TO IT, and the reason is a fact about that
// row rather than a gap in the check: its name is CLIPPED to the room its badges
// leave (renderRow, pickerClip), and a newline costs that clip nothing, so a
// forty-line name draws as two lines ending in an ellipsis and no pane that fits
// ever holds a row tall enough to cut. The check requires a cut at every fitting
// pane and would fail on a row that was never oversized. The supplier row's real
// oversized case is a link carrying every fact on a short pane, which
// TestProseBarRowPackedList_AnOversizedSupplierRowKeepsTheBarAndMarksItsCut
// drives instead.
func TestProseBarRowPackedList_MultiLineNamesFitThePaneAndMarkTheirCut(t *testing.T) {
	var lists []proseBarWindowedList
	for _, l := range proseBarRowPackedLists() {
		if l.recv != "ItemSuppliersScreen" {
			lists = append(lists, l)
		}
	}
	proseBarAssertMultiLineWindows(t, lists)
}

// TestProseBarRowPackedList_AnOversizedSupplierRowKeepsTheBarAndMarksItsCut is
// the supplier list's oversized-row check: a list whose LAST link carries every
// fact the row can draw — both barcodes, the pack, both prices, the lead time
// and a URL — with the cursor standing on it, at every pane Root draws.
//
// Where the frame is drawn at all it must fit the pane, so the bar is on it; and
// wherever that link is taller than the body the plan gives, the pane must carry
// suppliersRowCutMark, because a link cut short with no mark reads as a link
// that records fewer facts. The band where the frame cannot fit is the one
// bodyPlan documents — a pane with fewer rows than the folded bar plus one line
// of body — and a frame overrunning OUTSIDE that band fails. All three sides are
// counted, so no scoping here can become a way of asserting nothing.
func TestProseBarRowPackedList_AnOversizedSupplierRowKeepsTheBarAndMarksItsCut(t *testing.T) {
	var cut, whole, tooShort int
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := proseBarItemSuppliers(30, proseBarSameName)
			s.rows[29].URL = "https://www.mcmaster.com/products/screws/socket-head-screws/alloy-steel-socket-head-screws/91290A115"
			s = proseBarPress(proseBarSize(s, w, h), "end").(*ItemSuppliersScreen)
			if screenBodyRows(h) < len(s.suppliersFooterLines())+2 {
				tooShort++
				continue
			}
			view := s.View()
			if !proseBarFrameFits(s, h) {
				t.Fatalf("at %dx%d the supplier list standing on its fullest link hands over %d "+
					"rows for a %d-row pane — the bar is what clampToBox takes:\n%s",
					w, h, lipgloss.Height(view), screenBodyRows(h), stripANSI(view))
			}
			pane := stripANSI(clampToBox(view, screenBodyCells(w), screenBodyRows(h)))
			for _, seg := range s.proseBar() {
				if !strings.Contains(pane, seg.Hint) {
					t.Fatalf("at %dx%d the pane loses bar segment %q:\n%s", w, h, seg.Hint, pane)
				}
			}
			if strings.Count(s.renderRow(s.cursor), "\n")+1 <= s.bodyPlan().body {
				whole++
				continue
			}
			cut++
			if !strings.Contains(pane, strings.TrimSpace(suppliersRowCutMark)) {
				t.Fatalf("at %dx%d the fullest link is taller than the body and the pane does not "+
					"say it was cut:\n%s", w, h, pane)
			}
		}
	}
	if cut == 0 || whole == 0 || tooShort == 0 {
		t.Errorf("the link was cut on %d panes, drawn whole on %d and refused room on %d; a "+
			"side never reached is a check that asserted nothing on it", cut, whole, tooShort)
	}
}

func proseBarSerializedForecast(n int, name proseBarRowName) *SerializedForecastScreen {
	rows := make([]omsapi.ComponentForecastRow, n)
	for i := range rows {
		days, lead := float64(3+i), 7.0
		rows[i] = omsapi.ComponentForecastRow{
			ItemID:             fmt.Sprintf("it-%d", i+1),
			ItemName:           name(i, fmt.Sprintf("%02d Nitrogen cylinder, 300 cu ft, CGA-580 valve", i+1)),
			SKU:                fmt.Sprintf("N2-CYL-%03d", i+1),
			SerialTrackingMode: "consumable",
			AvailableStock:     4, Installed: 1, CurrentStock: 6,
			WindowDays: 90, UnitsDepletedInWindow: 8, AvgDailyUse: 0.4286,
			DaysUntilStockout: &days, LeadTimeDays: &lead,
			ReorderPoint: 5, NeedsReorder: i%2 == 0,
		}
		// Every third row carries the projected-stockout line, so the list mixes
		// two- and three-line rows the way a real forecast does.
		if i%3 == 0 {
			rows[i].ProjectedStockoutDate = "2026-10-20"
		}
	}
	s := NewSerializedForecastScreen(Deps{})
	next, _ := s.Update(serializedForecastLoadedMsg{rows: rows})
	return next.(*SerializedForecastScreen)
}

func proseBarDemandForecast(alerts bool, n int, name proseBarRowName) *DemandForecastScreen {
	rows := make([]omsapi.DemandForecastRow, n)
	for i := range rows {
		cadence, due, lead := 47.5, float64(i-3), 7
		next, last := "2026-10-12", "2026-08-25"
		rows[i] = omsapi.DemandForecastRow{
			ID:              int64(i + 1),
			Item:            fmt.Sprintf("itm-%d", i+1),
			ItemName:        name(i, fmt.Sprintf("%02d PLA filament, 1.75 mm, 1 kg spool, matte black", i+1)),
			SKU:             fmt.Sprintf("PLA-175-%03d", i+1),
			AvgIntervalDays: &cadence, IntervalSamples: 5,
			PredictedNextReorderDate: &next, DaysUntilDue: &due,
			LeadTimeDays: &lead, NeedsReorder: i%2 == 0,
			Method: omsapi.ForecastMethodRestockInterval,
		}
		if i%3 == 0 {
			rows[i].LastRestockDate = &last
		}
	}
	s := NewDemandForecastScreen(Deps{})
	s.alerts = alerts
	loaded, _ := s.Update(demandForecastLoadedMsg{rows: rows})
	return loaded.(*DemandForecastScreen)
}

func proseBarItemSuppliers(n int, name proseBarRowName) *ItemSuppliersScreen {
	rows := make([]omsapi.ItemSupplier, n)
	for i := range rows {
		rows[i] = supUPCRow(i + 1)
		rows[i].SupplierName = name(i, fmt.Sprintf("%02d McMaster-Carr Supply Company", i+1))
	}
	s := NewItemSuppliersScreen(Deps{}, "itm-1", "Hex bolt M8x40")
	next, _ := s.Update(itemSuppliersLoadedMsg{rows: rows})
	return next.(*ItemSuppliersScreen)
}
