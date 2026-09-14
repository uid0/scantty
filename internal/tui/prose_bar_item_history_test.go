package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_item_history_test.go — the bar-honesty fixtures for ItemHistoryScreen
// (item_history.go), which was written on the prose-bar record rather than
// converted to it.
//
// ONE STATE PER SHAPE ITS BAR TAKES. The bar changes with the VIEW (the switch is
// worded for the other one), with whether the view has a second row (the
// movement segments), and with whether the view's own reading failed (`r retry`
// against `r refresh`). The long fixtures are the only ones where the window and
// its markers are drawn and the movement keys must be named; the one-row and
// empty ones are where they must come off; the partial failure is the one state
// where a view draws a failure while the switch still reaches a view that
// loaded. The load states are prose_bar_load_states_test.go's.
//
// THE ROWS CARRY REAL LENGTHS and differ AT THE FRONT (a fixture whose rows
// differ only past the clip cannot report movement, AGENTS.md): every stock row
// leads with its own date, and every usage row with its own timestamp.

// proseBarHistoryItem is the item every fixture is the history of: a named base
// unit, so the unit text is drawn at a realistic length.
func proseBarHistoryItem() *omsapi.Item {
	return &omsapi.Item{ID: "itm-gloves", Name: "Blue nitrile exam gloves, powder-free, medium", BaseUnit: "glove"}
}

// proseBarStockHistory is `weeks` weekly snapshots with a count and a reorder
// every fourth week, so all three row kinds are drawn.
func proseBarStockHistory(weeks int) *omsapi.StockHistory {
	h := &omsapi.StockHistory{CurrentStock: 360, Thresholds: omsapi.StockHistoryThresholds{ReorderPoint: 100, Desired: 400}}
	start := time.Date(2025, 11, 3, 0, 0, 0, 0, time.UTC)
	for i := 0; i < weeks; i++ {
		d := omsapi.DateOnly{Time: start.AddDate(0, 0, 7*i)}
		h.Series = append(h.Series, omsapi.StockHistoryPoint{Date: d, Count: 900 - 13*i})
		if i%4 == 3 {
			h.CycleCounts = append(h.CycleCounts, omsapi.StockHistoryPoint{
				Date: omsapi.DateOnly{Time: d.AddDate(0, 0, 2)}, Count: 890 - 13*i})
			h.ReorderEvents = append(h.ReorderEvents, omsapi.StockHistoryEvent{Date: omsapi.DateOnly{Time: d.AddDate(0, 0, 3)}})
		}
	}
	return h
}

// proseBarUsageLogs is `n` logs, newest first, every other one with a note and
// every third one with no recorder.
func proseBarUsageLogs(n int) []omsapi.UsageLog {
	out := make([]omsapi.UsageLog, 0, n)
	at := time.Date(2026, 9, 12, 16, 45, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		l := omsapi.UsageLog{ID: 1000000 + n - i, QuantityUsed: 1 + i%12, UsageDate: at.Add(-time.Duration(i) * 7 * time.Hour)}
		if i%3 != 2 {
			by := 200 + i
			l.ChargedBy = &by
		}
		if i%2 == 0 {
			l.Notes = fmt.Sprintf("Laser cutter orientation class, section %d — two boxes opened at the bench", i+1)
		}
		out = append(out, l)
	}
	return out
}

// proseBarItemHistory is the screen past its load, on `view`.
func proseBarItemHistory(h *omsapi.StockHistory, logs []omsapi.UsageLog, histErr, usageErr error, view itemHistoryView) *ItemHistoryScreen {
	s := NewItemHistoryScreen(Deps{}, proseBarHistoryItem())
	next, _ := s.Update(itemHistoryStockMsg{history: h, err: histErr})
	next, _ = next.Update(itemHistoryUsageMsg{logs: logs, err: usageErr})
	s = next.(*ItemHistoryScreen)
	s.view = view
	return s
}

func proseBarItemHistoryFixtures() []proseBarFixture {
	walked := func(s *ItemHistoryScreen) proseBarScreen {
		return proseBarPressAll(proseBarSize(s, 80, 24), "j", "j", "j")
	}
	return []proseBarFixture{
		{
			name: "item history/stock", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return walked(proseBarItemHistory(proseBarStockHistory(40), proseBarUsageLogs(40), nil, nil, itemHistoryStock))
			},
		},
		{
			name: "item history/usage", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return walked(proseBarItemHistory(proseBarStockHistory(40), proseBarUsageLogs(40), nil, nil, itemHistoryUsage))
			},
		},
		{
			name: "item history/stock one row", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return proseBarItemHistory(proseBarStockHistory(1), proseBarUsageLogs(1), nil, nil, itemHistoryStock)
			},
			immobile: "one snapshot, so there is nowhere for the cursor to go — the movement segments must be absent",
		},
		{
			name: "item history/usage one row", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return proseBarItemHistory(proseBarStockHistory(1), proseBarUsageLogs(1), nil, nil, itemHistoryUsage)
			},
			immobile: "one usage log, so there is nowhere for the cursor to go",
		},
		{
			name: "item history/stock empty", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return proseBarItemHistory(&omsapi.StockHistory{CurrentStock: 6}, nil, nil, nil, itemHistoryStock)
			},
			immobile: "no stock history at all — which is the point of this fixture: the movement " +
				"segments must be absent and the switch, the reload and the way out named",
		},
		{
			name: "item history/usage empty", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return proseBarItemHistory(&omsapi.StockHistory{CurrentStock: 6}, nil, nil, nil, itemHistoryUsage)
			},
			immobile: "no usage logged at all",
		},
		{
			name: "item history/usage failed", recv: "ItemHistoryScreen",
			build: func() proseBarScreen {
				return proseBarItemHistory(proseBarStockHistory(40), nil, nil,
					errors.New("oms: http 502: "+proseLoadGatewayPage), itemHistoryUsage)
			},
			immobile: "the usage reading failed, so the view draws a failure and no rows — `r retry` " +
				"and the switch to the stock view that DID load are what it names",
		},
		proseBarItemHistoryOversizedFixture(),
	}
}

// proseBarItemHistoryOversizedFixture is ONE usage log whose note is taller than
// the tallest pane Root draws: a note is free text, any number of lines.
func proseBarItemHistoryOversizedFixture() proseBarFixture {
	return proseBarFixture{
		name: "item history/oversized note", recv: "ItemHistoryScreen",
		build: func() proseBarScreen {
			logs := proseBarUsageLogs(1)
			lines := make([]string, 60)
			for i := range lines {
				lines[i] = fmt.Sprintf("Glove box %02d opened for the resin pour", i+1)
			}
			logs[0].Notes = strings.Join(lines, "\n")
			return proseBarItemHistory(proseBarStockHistory(1), logs, nil, nil, itemHistoryUsage)
		},
		immobile: "one usage log, so there is nowhere for the cursor to go",
	}
}

// TestItemHistory_AnOversizedNoteFitsAndMarksItsCut: at every pane Root draws
// where the frame has room for a line of body, the oversized note's frame fits
// the pane — so the bar is on it — and the cut is named on the pane.
//
// WATCHED FAILING: with the usage view writing its rows under the head with no
// window (every row, then the bar), it reported the frame overrunning the pane at
// 80x17, the shortest pane the check reaches.
func TestItemHistory_AnOversizedNoteFitsAndMarksItsCut(t *testing.T) {
	f := proseBarItemHistoryOversizedFixture()
	fits, short := 0, 0
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := proseBarSize(f.build(), w, h).(*ItemHistoryScreen)
			head := strings.Count(s.usageHead(), "\n")
			if screenBodyRows(h) < head+2+proseListWindowFloor+s.bar(proseFlatCeilingRows).rows(proseBarCells(w)) {
				short++
				continue
			}
			fits++
			if !proseBarFrameFits(s, h) {
				t.Fatalf("at %dx%d the oversized note pushed the frame past the pane — the bar is "+
					"what clampToBox takes:\n%s", w, h, stripANSI(s.View()))
			}
			pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
			if !strings.Contains(pane, " more lines") {
				t.Fatalf("at %dx%d the note was cut and the pane does not say so:\n%s", w, h, pane)
			}
			for _, seg := range s.proseBar() {
				if !strings.Contains(pane, seg.Hint) {
					t.Fatalf("at %dx%d the pane loses bar segment %q:\n%s", w, h, seg.Hint, pane)
				}
			}
		}
	}
	if fits == 0 || short == 0 {
		t.Errorf("the note was checked on %d panes and skipped on %d as too short for a window; "+
			"a side never reached is a check that asserted nothing on it", fits, short)
	}
}
