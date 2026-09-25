package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_storage_overview_test.go — the fixtures for StorageOverviewScreen,
// taken out of proseBarUnconverted on its own because it is not a list of rows
// at all: it is a rack GRID, and its cursor moves in two dimensions.
//
// THE SHAPE. A grid of one character per slot, windowed across the positions
// and down the levels, under a header, two rulers, a cursor line and a colour
// legend, with a footer literal of one row:
//
//	h/j/k/l move · enter open slot · a assign C/L/E · x release · pgup/pgdn rack · r refresh
//
// budgeted by `const chrome = 11`. The literal named four of the eight movement
// keystrokes the switch binds (the arrows and home/end moved unannounced), named
// `a` and `x` on cells where they only answer a toast, named no way off the
// screen, and at 80 columns was 88 cells against a 51-cell pane — so clampToBox
// took `pgup/pgdn rack · r refresh` off every frame. The header, the cursor line
// and the legend were cut at the same edge with no mark.
//
// WHAT CHANGED BESIDES THE RECORD. Each DIRECTION is gated on its own
// (StorageOverviewScreen.offers carries why a pair does not fit a grid), read
// off the same predicate its arm moves by; the three prose lines fold or clip
// with the cut marked; and the window's rows are counted from the folds, with a
// stated give-order (StorageOverviewScreen.layout).
//
// MEASURED AT HEAD BEFORE THE CONVERSION on the oversized rack below (three
// racks of 26 levels and 120 positions, an occupant name with a stored newline):
// at 80x24, 80x30 and 80x40 alike, four lines of the frame — the header, the
// cursor line, the legend and the bar — were past the 51st cell and cut
// unmarked, so `r refresh` and every colour claim of the legend were on none of
// those panes, and `esc back` was named nowhere.

// proseBarStorageOverviewFixtures is the grid in every state its bar changes
// shape in.
//
// THE CURSOR STANDS WHERE THE SHARED SWEEPS CAN JUDGE IT. Those sweeps read the
// bar at rest and press each key from rest AND after `end`, so a fixture whose
// `end` probe would unlock a direction the resting bar does not name would report
// that direction as acting unnamed. A list never meets that — its bar names a
// movement PAIR — and a grid gated per direction does. So every fixture here
// rests either somewhere every direction moves, or already at the last position,
// where `end` moves nothing; the per-direction biconditional at EVERY cell is
// TestProseBarStorageOverview_EveryDirectionIsNamedExactlyWhereItMoves, which
// presses each key from each cell rather than from two probes.
func proseBarStorageOverviewFixtures() []proseBarFixture {
	middle := func(position int) func() proseBarScreen {
		return func() proseBarScreen { return proseBarStorageOverview(3, 12, 60, 2, "F", position) }
	}
	return []proseBarFixture{
		// A free slot mid-rack: every direction, both jumps, the rack page, enter
		// and `a`.
		{name: "storage overview", recv: "StorageOverviewScreen", build: middle(proseBarOverviewFree)},
		// A committee holding: `x` comes on and `a` goes off.
		{name: "storage overview/held", recv: "StorageOverviewScreen", build: middle(proseBarOverviewHeld)},
		// A project stint: neither `a` nor `x`, both of which answer a toast here.
		{name: "storage overview/project", recv: "StorageOverviewScreen", build: middle(proseBarOverviewProject)},
		// A hole in the racking: no slot, so not `enter` either.
		{name: "storage overview/hole", recv: "StorageOverviewScreen", build: middle(proseBarOverviewHole)},
		// The bottom-right corner of the only rack: left, up and home only, and
		// pgup/pgdn named for the jump to the top-left they make on one rack.
		{
			name: "storage overview/bottom-right of one rack", recv: "StorageOverviewScreen",
			build: func() proseBarScreen { return proseBarStorageOverview(1, 12, 60, 1, "A", 60) },
		},
		{
			name: "storage overview/one slot", recv: "StorageOverviewScreen",
			build: func() proseBarScreen { return proseBarStorageOverview(1, 1, 1, 1, "A", 1) },
			immobile: "one slot on one rack — the point of this fixture: every direction, both " +
				"jumps and the rack page must be absent, and enter, `a`, `r` and `esc` named",
		},
		{
			name: "storage overview/no racking", recv: "StorageOverviewScreen",
			build: func() proseBarScreen {
				next, _ := NewStorageOverviewScreen(Deps{}).Update(storageOverviewLoadedMsg{overview: &omsapi.StorageOverview{}})
				return next.(proseBarScreen)
			},
			immobile: "no rack to move across — `r` and `esc` are the whole bar",
		},
		{
			name: "storage overview/oversized", recv: "StorageOverviewScreen",
			build: func() proseBarScreen { return proseBarStorageOverviewOversized() },
		},
	}
}

// The positions on every level of proseBarStorageOverview's racks that hold each
// kind of cell.
const (
	proseBarOverviewFree    = 2
	proseBarOverviewProject = 3
	proseBarOverviewHeld    = 4
	proseBarOverviewHole    = 7
)

// TestProseBarStorageOverview_EveryDirectionIsNamedExactlyWhereItMoves is the
// captain's sentence for this screen, stated at the grain it was asked at: the
// bar names exactly the directional keys the grid binds, gated on there being
// somewhere to move in EACH direction.
//
// EVERY CELL, NOT TWO PROBES. It stands the cursor on every (level, position) of
// a rack with holes, retired slots and a level at each edge, on racking of one
// rack and of three, and presses every movement keystroke the switch binds from
// a fresh screen there: the clipped pane changes if and only if the bar at that
// cell names the key. Both sides are required per key, so a direction that is
// never unnamed — the pair-grained bar this replaced — fails as surely as one
// that is never named.
//
// WATCHED FAILING with the axes gated as PAIRS (either direction naming both):
// `h`, `left`, `k` and `up` at the first position and top level, `l`, `right`,
// `j` and `down` at the last.
func TestProseBarStorageOverview_EveryDirectionIsNamedExactlyWhereItMoves(t *testing.T) {
	const w, h = 80, 24
	keys := []string{"h", "left", "l", "right", "k", "up", "j", "down", "home", "end", "pgup", "pgdown"}
	// Counted over BOTH rackings: on three racks the page is named at every cell,
	// so its unnamed side lives on the one-rack racking's top-left alone.
	named, unnamed := map[string]int{}, map[string]int{}
	for _, racks := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d rack(s)", racks), func(t *testing.T) {
			levels := proseBarOverviewLevels(4)
			for _, level := range levels {
				for position := 1; position <= 12; position++ {
					build := func() proseBarScreen {
						return proseBarStorageOverview(racks, len(levels), 12, 1, level, position)
					}
					bar := proseBarSize(build(), w, h).proseBar()
					for _, key := range keys {
						s := proseBarSize(build(), w, h)
						before := clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
						next, _ := s.Update(listRuneKey(key))
						after := clampToBox(next.(proseBarScreen).View(), screenBodyCells(w), screenBodyRows(h))
						moved := after != before
						switch {
						case moved && !bar.names(key):
							t.Errorf("at %s%d %q moves the pane and the bar does not name it.\nbar: %s",
								level, position, key, bar.hint())
						case !moved && bar.names(key):
							t.Errorf("at %s%d the bar names %q and it moves nothing.\nbar: %s",
								level, position, key, bar.hint())
						}
						if bar.names(key) {
							named[key]++
						} else {
							unnamed[key]++
						}
					}
				}
			}
		})
	}
	for _, key := range keys {
		if named[key] == 0 || unnamed[key] == 0 {
			t.Errorf("%q was named at %d cells and unnamed at %d, so one side of the "+
				"biconditional was never tested for it", key, named[key], unnamed[key])
		}
	}
}

// TestProseBarStorageOverview_AnOversizedRackFitsAndMarksItsCut: a rack too wide
// and too tall for the pane, with an occupant name longer than the cursor line
// and carrying a stored newline, draws a frame that fits with the bar last and
// every cut marked — the positions shown out of how many, the levels below the
// window, and the occupant's ellipsis.
//
// WATCHED FAILING at HEAD before the conversion: see the file header.
func TestProseBarStorageOverview_AnOversizedRackFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	s := proseBarSize(proseBarStorageOverviewOversized(), width, height)
	if !proseBarFrameFits(s, height) {
		t.Fatalf("the oversized rack pushed the frame past %dx%d, taking the bar with it:\n%s",
			width, height, stripANSI(s.View()))
	}
	got := stripANSI(s.View())
	for _, want := range []string{"of 120", "more level(s) below", "…"} {
		if !strings.Contains(got, want) {
			t.Errorf("the oversized rack's frame does not carry %q, so a cut goes unmarked:\n%s", want, got)
		}
	}
	if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
		t.Fatalf("the bar is not the last thing on the pane:\n%s", got)
	}
}

// TestProseBarStorageOverview_NoLineRunsPastThePane: at every width Root draws,
// no line the grid hands over is wider than the pane, in any state that draws
// prose the screen does not control — so clampToBox never takes a tail, and
// every cut is the screen's own, marked one.
//
// WATCHED FAILING with the legend drawn as the one line it used to be: at every
// drawable width, 80 to 120.
func TestProseBarStorageOverview_NoLineRunsPastThePane(t *testing.T) {
	states := map[string]func() proseBarScreen{
		"oversized": func() proseBarScreen { return proseBarStorageOverviewOversized() },
		"no slot":   func() proseBarScreen { return proseBarStorageOverview(3, 12, 60, 2, "F", proseBarOverviewHole) },
		"release":   func() proseBarScreen { return proseBarPress(proseBarStorageOverviewOversizedHeld(), "x") },
		"no racking": func() proseBarScreen {
			next, _ := NewStorageOverviewScreen(Deps{}).Update(storageOverviewLoadedMsg{overview: &omsapi.StorageOverview{}})
			return next.(proseBarScreen)
		},
		"loading":       func() proseBarScreen { return NewStorageOverviewScreen(Deps{}) },
		"reload failed": func() proseBarScreen { return proseBarStorageOverviewReloadFailed() },
	}
	for name, build := range states {
		for _, w := range jdeDrawableWidths() {
			s := proseBarSize(build(), w, 60)
			for i, line := range strings.Split(s.View(), "\n") {
				if got := lipgloss.Width(line); got > proseBarCells(w) {
					t.Errorf("%s at width %d: line %d is %d cells on a %d-cell pane:\n%s",
						name, w, i, got, proseBarCells(w), stripANSI(line))
				}
			}
		}
	}
}

// TestProseBarStorageOverview_TheReleaseConfirmSurvivesAtEveryDrawablePane: the
// release confirm — the question, what it does, and the two keys that answer it
// — reaches the operator whole, and the frame carrying it fits, at every pane
// Root draws that is tall enough for the confirm and the least grid the frame
// will draw.
//
// The confirm answers nil from proseBar, so the footer sweeps never read it;
// this is where it is held.
//
// THE BOUNDARY IS DERIVED, for the reason
// TestProseBarFootPrompts_TheFootSurvivesTheRowsAtEveryDrawablePane gives: from
// the same confirm opened over a rack of ONE level — no scroll markers, one row —
// plus both markers and any rows its header folds past that one's. Both sides
// must be reached.
//
// WATCHED FAILING with the confirm's rows left out of the budget (the foot
// counted at the bar's ceiling whatever is drawn): at every width, from the
// boundary up.
func TestProseBarStorageOverview_TheReleaseConfirmSurvivesAtEveryDrawablePane(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	held, short := 0, 0
	for _, w := range widths {
		for _, h := range heights {
			cells := proseBarCells(w)
			s := proseBarSize(proseBarPress(proseBarStorageOverviewOversizedHeld(), "x"), w, h).(*StorageOverviewScreen)
			least := proseBarSize(proseBarPress(proseBarStorageOverviewTallest(1), "x"), w, h).(*StorageOverviewScreen)
			headRows := len(s.headerLines(s.rack(), cells)) - len(least.headerLines(least.rack(), cells))
			need := lipgloss.Height(least.View()) + headRows + 2
			if least.layout(least.rack()).legend {
				// The least frame drew the legend because a one-level rack left it
				// room; the claim is about the grid giving rows, not about the legend.
				need -= 1 + len(storageGridLegendLines(cells))
			}
			if screenBodyRows(h) < need {
				short++
				continue
			}
			held++
			if !proseBarFrameFits(s, h) {
				t.Errorf("at %dx%d the frame needs %d rows and the pane has %d, so the confirm is "+
					"what clampToBox takes:\n%s", w, h, lipgloss.Height(s.View()), screenBodyRows(h), stripANSI(s.View()))
				continue
			}
			pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
			for _, want := range []string{"Release 2F4", "The holding is kept as history", "y release · n/esc cancel"} {
				if !strings.Contains(pane, want) {
					t.Errorf("at %dx%d the pane does not carry %q:\n%s", w, h, want, pane)
				}
			}
		}
	}
	if held == 0 || short == 0 {
		t.Errorf("the confirm reached %d panes past the boundary and %d short of it, so one side "+
			"was never tested", held, short)
	}
}

// TestProseBarStorageOverview_TheLegendGoesWholeOrNotAtAll: the colour legend is
// drawn with every claim it makes, or not drawn — never a fold off its end,
// which would say what yellow means and leave red unexplained.
func TestProseBarStorageOverview_TheLegendGoesWholeOrNotAtAll(t *testing.T) {
	drawn, gone := 0, 0
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := proseBarSize(proseBarStorageOverviewOversized(), w, h)
			if !proseBarFrameFits(s, h) {
				continue
			}
			pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
			present := 0
			for _, seg := range storageGridLegendSegments {
				if strings.Contains(pane, seg.text) {
					present++
				}
			}
			switch present {
			case 0:
				gone++
			case len(storageGridLegendSegments):
				drawn++
			default:
				t.Errorf("at %dx%d the pane carries %d of the legend's %d claims:\n%s",
					w, h, present, len(storageGridLegendSegments), pane)
			}
		}
	}
	if drawn == 0 || gone == 0 {
		t.Errorf("the legend was drawn at %d panes and given up at %d, so one side of its "+
			"give-order was never reached", drawn, gone)
	}
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// proseBarOverviewLevels is `n` level letters, highest first — the order OMS
// serves and the grid draws.
func proseBarOverviewLevels(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = string(rune('A' + n - 1 - i))
	}
	return out
}

// proseBarStorageOverview is `racks` racks of `levels` levels and `positions`
// positions, every level laid out the same way — a hole every seventh position,
// a retired slot every fifth, a coloured project every third, a committee
// holding every fourth, the rest free — with the cursor aimed at one address.
//
// The occupants are the length OMS really serves, so the cursor line is measured
// against a realistic name rather than `Ada`.
func proseBarStorageOverview(racks, levels, positions, rack int, level string, position int) *StorageOverviewScreen {
	ov := &omsapi.StorageOverview{}
	for rk := 1; rk <= racks; rk++ {
		r := omsapi.StorageOverviewRack{Rack: rk, MaxPosition: positions, Levels: proseBarOverviewLevels(levels)}
		for _, lv := range r.Levels {
			row := omsapi.StorageOverviewRow{Level: lv}
			for p := 1; p <= positions; p++ {
				row.Cells = append(row.Cells, proseBarOverviewCell(rk, lv, p))
			}
			r.Rows = append(r.Rows, row)
		}
		ov.Racks = append(ov.Racks, r)
	}
	s := NewStorageOverviewScreenAt(Deps{}, rack, level, position)
	next, _ := s.Update(storageOverviewLoadedMsg{overview: ov})
	return proseBarSize(next.(proseBarScreen), 80, 24).(*StorageOverviewScreen)
}

func proseBarOverviewCell(rack int, level string, p int) *omsapi.StorageOverviewCell {
	code := fmt.Sprintf("%d%s%d", rack, level, p)
	c := &omsapi.StorageOverviewCell{Code: code, Position: p, IsActive: true, Status: omsapi.StorageOverviewStatusEmpty}
	switch {
	case p%proseBarOverviewHole == 0:
		return nil
	case p%5 == 0:
		c.IsActive = false
	case p%proseBarOverviewProject == 0:
		c.Type, c.Occupant, c.Status, c.Color = omsapi.StorageTypeLetterProject,
			"Alex Hernandez-Whitfield", "expiring_soon", omsapi.StorageOverviewColorYellow
		if p%2 == 0 {
			c.Status, c.Color = "expired", omsapi.StorageOverviewColorRed
		}
	case p%proseBarOverviewHeld == 0:
		c.Type, c.Occupant, c.Status = omsapi.StorageTypeLetterCommittee,
			"Facilities and safety committee", omsapi.StorageOverviewStatusOccupied
	}
	return c
}

// proseBarStorageOverviewOversized is three racks of 26 levels and 120 positions
// — wider than the 91 cells the widest drawable pane gives —
// with the cursor mid-rack on a committee cell whose occupant is past the pane
// and carries a stored newline.
func proseBarStorageOverviewOversized() *StorageOverviewScreen {
	s := proseBarStorageOverview(3, 26, 120, 2, "M", 60)
	s.cell().Type, s.cell().IsActive = omsapi.StorageTypeLetterCommittee, true
	s.cell().Occupant = "Facilities, safety and\nnorth-building mezzanine logistics committee (dock crew)"
	s.cell().Status, s.cell().Color = omsapi.StorageOverviewStatusOccupied, ""
	return s
}

// proseBarStorageOverviewOversizedHeld is the oversized racking with the cursor
// on a plain committee holding, 2F4, so `x` opens the confirm.
func proseBarStorageOverviewOversizedHeld() *StorageOverviewScreen {
	return proseBarStorageOverviewTallest(26)
}

// proseBarStorageOverviewTallest is the oversized racking's width and rack count
// at `levels` levels, cursor on 2F4 — or the top level where there is no F.
func proseBarStorageOverviewTallest(levels int) *StorageOverviewScreen {
	s := proseBarStorageOverview(3, levels, 120, 2, "F", proseBarOverviewHeld)
	if s.level != "F" {
		// A rack too short to have an F: the cursor resolved onto its top level,
		// and the cell there is the same kind.
		s.level = s.rack().Levels[0]
	}
	return s
}

// proseBarStorageOverviewReloadFailed is the overview after a refresh has failed
// with a gateway page — the stale grid is kept, and the failure frame bounds the
// body.
func proseBarStorageOverviewReloadFailed() proseBarScreen {
	s := proseBarPress(proseBarStorageOverview(3, 12, 60, 2, "F", proseBarOverviewFree), "r")
	next, _ := s.Update(storageOverviewLoadedMsg{err: fmt.Errorf("oms: http 502: %s", proseLoadGatewayPage)})
	return next.(proseBarScreen)
}

// Compile-time: the grid states the record contract.
var _ proseBarScreen = (*StorageOverviewScreen)(nil)
