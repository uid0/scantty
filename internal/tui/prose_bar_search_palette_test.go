package tui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_search_palette_test.go — the fixtures and the frame checks for the
// TENTH conversion taken out of proseBarUnconverted, which is one screen because
// nothing else has its shape: the universal search palette (search.go).
//
// THE SHAPE. A flat result list drawn under a LIVE QUERY BOX. Three things about
// it none of the earlier recipes answered, each decided in search.go and held
// here:
//
//   - THE BOX OWNS THE LETTERS, so the cursor moves on the arrows alone and every
//     fixture is a typing fixture (proseBarFixture.typing). That includes the
//     load states, which is new: every other load frame draws no box, so the
//     load-state sweeps learned the same exemption the loaded biconditional
//     takes.
//   - ITS LOAD KEEPS ITS ROWS DRAWN. A refined query is searched over the
//     results it is narrowing, so a search out is a state with rows on the pane
//     and keys that visibly act on them (proseLoadScreen.rowsStay). And its load
//     is started by typing rather than by Init (proseLoadScreen.loadCmd).
//   - THE FRAME HAS TWO ENDS THAT NEVER GIVE — the box at the top and the bar at
//     the bottom — so the body is budgeted between them rather than above a bar
//     alone, and the frame's status (`searching…`, `N match(es)`) rides the box
//     row where no budget reaches it.
//
// THE 80x7 FLOOR IS DELIBERATELY NOT DECIDED HERE. One row cannot hold both the
// box and the bar, and which of them it should keep is an open design question.
// The palette keeps the BOX there, which is what it drew before this conversion,
// and TestSearchPalette_TheBoxLeadsAndTheBarClosesAtEveryDrawablePane pins that
// as today's behaviour rather than as an answer.

// proseBarPaletteLoadQuery is the query the palette's load states search. The
// LOADED fixture searched one character more, so the reload key — backspace —
// lands a refresh on exactly this query (see the palette's proseLoadScreens
// entry for why that matters).
const proseBarPaletteLoadQuery = "hex bol"

// proseBarSearchPaletteFixtures is the palette in every state its bar changes
// shape in, past a load. Its load states are proseLoadScreens'.
func proseBarSearchPaletteFixtures() []proseBarFixture {
	const box = proseBarFormBox
	return append([]proseBarFixture{
		{
			name: "search palette/empty box", recv: "SearchPalette", typing: box,
			build: func() proseBarScreen { return NewSearchPalette(Deps{}) },
			immobile: "nothing has been searched, so there are no results to move through — the " +
				"point of this fixture: `↑↓` and `enter` must be absent and `esc` named",
		},
		{
			name: "search palette/results", recv: "SearchPalette", typing: box,
			build: func() proseBarScreen {
				return proseBarWalk(proseBarPaletteAnswered(proseBarPaletteLoadQuery+"t", proseBarPaletteResults(20)), "down")
			},
		},
		{
			name: "search palette/one result", recv: "SearchPalette", typing: box,
			build: func() proseBarScreen {
				return proseBarPaletteAnswered(proseBarPaletteLoadQuery+"t", proseBarPaletteResults(1))
			},
			immobile: "one result, so there is nowhere for the cursor to go — `enter` is named and " +
				"`↑↓` must not be",
		},
		{
			name: "search palette/no matches", recv: "SearchPalette", typing: box,
			build: func() proseBarScreen { return proseBarPaletteAnswered("zzzz", nil) },
			immobile: "a query that matched nothing: no results, so neither `↑↓` nor `enter` " +
				"has anything to act on",
		},
	}, proseBarOversizedPalette()...)
}

// proseBarOversizedPalette is the palette's window with its cursor on a result
// taller than the whole pane: alone, and between two ordinary results so both
// arrows move onto and off it.
func proseBarOversizedPalette() []proseBarFixture {
	const box = proseBarFormBox
	return []proseBarFixture{
		{
			name: "search palette/oversized row", recv: "SearchPalette", typing: box,
			build: func() proseBarScreen {
				results := proseBarPaletteResults(1)
				results[0].Title = proseBarTallName()
				return proseBarPaletteAnswered(proseBarPaletteLoadQuery+"t", results)
			},
			immobile: "one result, so there is nowhere for the cursor to go",
		},
		{
			name: "search palette/oversized row between two", recv: "SearchPalette", typing: box,
			build: func() proseBarScreen {
				results := proseBarPaletteResults(3)
				results[1].Title = proseBarTallName()
				return proseBarPress(proseBarPaletteAnswered(proseBarPaletteLoadQuery+"t", results), "down")
			},
		},
	}
}

// TestSearchPalette_AnOversizedRowFitsAndMarksItsCut holds the palette's window
// to the bar every converted window is held to: a result taller than the pane is
// clipped to what the box and the bar leave, the cut is MARKED, and the bar is
// still the last thing on the pane.
//
// WATCHED FAILING against HEAD before the conversion: the one-result fixture
// assembled a 50-row frame against an 18-row pane at 80x24, and `esc close` was
// nowhere on the clipped pane.
func TestSearchPalette_AnOversizedRowFitsAndMarksItsCut(t *testing.T) {
	const width, height = 80, 24
	for _, f := range proseBarOversizedPalette() {
		t.Run(f.name, func(t *testing.T) {
			s := proseBarSize(f.build(), width, height)
			if !proseBarFrameFits(s, height) {
				t.Fatalf("an oversized result pushed the frame past %dx%d, taking the bar with it:\n%s",
					width, height, stripANSI(s.View()))
			}
			got := stripANSI(s.View())
			if !strings.Contains(got, " more lines") {
				t.Fatalf("an oversized result was clipped with no mark saying so:\n%s", got)
			}
			if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
				t.Fatalf("the bar is not the last thing on the pane:\n%s", got)
			}
		})
	}
}

// TestSearchPalette_TheBoxLeadsAndTheBarClosesAtEveryDrawablePane: at every
// pane Root draws, in every state the palette is swept in, the query box is the
// first row; wherever the pane has room for the box and the bar the frame FITS
// and carries every segment whole; and at the one pane that has room for only
// one row the box is what it keeps.
//
// SCOPED BY A DERIVED BOUNDARY AND BOTH SIDES COUNTED: `1 + bar.rows` is the
// least the frame can be — the box, the blank, the folded bar — so it is where
// the claim starts, not a height somebody chose.
//
// WATCHED FAILING: with View's body budget replaced by an unbounded body, every
// state that draws one reports a frame past the pane from 80x9 — the first pane
// the claim is made at — and the two oversized fixtures at every height; with
// the body written ABOVE the box, every pane where a state has a body reports
// the box off the first row.
func TestSearchPalette_TheBoxLeadsAndTheBarClosesAtEveryDrawablePane(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, f := range proseBarFixtures() {
		if f.recv != "SearchPalette" {
			continue
		}
		t.Run(f.name, func(t *testing.T) {
			held, short := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					s := proseBarSize(f.build(), w, h)
					bar := s.proseBar()
					pane := strings.Split(stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))), "\n")
					if !strings.HasPrefix(pane[0], "▸ ") {
						t.Errorf("at %dx%d the first row of the pane is not the query box: %q", w, h, pane[0])
					}
					for i, line := range strings.Split(s.View(), "\n") {
						if lipgloss.Width(line) > screenBodyCells(w) {
							t.Errorf("at %dx%d row %d is %d cells against a %d-cell pane, so clampToBox "+
								"cuts it with no mark: %q", w, h, i, lipgloss.Width(line), screenBodyCells(w),
								stripANSI(line))
						}
					}
					if screenBodyRows(h) < 1+bar.rows(proseBarCells(w)) {
						short++
						if screenBodyRows(h) == 1 && len(pane) != 1 {
							t.Errorf("at %dx%d the one-row pane drew %d rows", w, h, len(pane))
						}
						continue
					}
					held++
					if !proseBarFrameFits(s, h) {
						t.Errorf("at %dx%d the frame needs %d rows and the pane has %d, so what clampToBox "+
							"takes is the bar:\n%s", w, h, lipgloss.Height(s.View()), screenBodyRows(h),
							stripANSI(s.View()))
						continue
					}
					joined := strings.Join(pane, "\n")
					for _, seg := range bar {
						if !strings.Contains(joined, seg.Hint) {
							t.Errorf("at %dx%d the pane does not carry %q whole:\n%s", w, h, seg.Hint, joined)
						}
					}
				}
			}
			if held == 0 || short == 0 {
				t.Errorf("%s reached %d panes with room for the box and the bar and %d without, so "+
					"one side of the boundary was never tested", f.name, held, short)
			}
		})
	}
}

// TestSearchPalette_ACutResultListIsMarked: wherever the window leaves results
// off the pane and has a row to say so, it says so — `↑ more above` or
// `↓ N more below` — and the box row's `N match(es)` counts the whole list at
// every height, including the ones where the body has no row at all.
//
// WATCHED FAILING with the markers taken out of the window: every pane from
// 80x12 up reports twenty results with some missing and nothing saying so.
func TestSearchPalette_ACutResultListIsMarked(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	build := func() proseBarScreen {
		return proseBarPaletteAnswered(proseBarPaletteLoadQuery+"t", proseBarPaletteResults(20))
	}
	cut := 0
	for _, w := range widths {
		for _, h := range heights {
			s := proseBarSize(proseBarPress(build(), "down", "down", "down"), w, h).(*SearchPalette)
			pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
			if screenBodyRows(h) >= 1 && !strings.Contains(pane, "20 match(es)") {
				t.Errorf("at %dx%d the box row does not count the results:\n%s", w, h, pane)
			}
			missing := false
			for _, r := range s.results {
				if !strings.Contains(pane, r.Title[:3]) {
					missing = true
				}
			}
			// The body's lines: the pane less the box, the blank under it and the bar.
			body := screenBodyRows(h) - 2 - s.proseBar().rows(proseBarCells(w))
			if !missing || body < 2 {
				continue
			}
			cut++
			if !strings.Contains(pane, "more below") && !strings.Contains(pane, "more above") {
				t.Errorf("at %dx%d results are off the pane and nothing says so:\n%s", w, h, pane)
			}
		}
	}
	if cut == 0 {
		t.Fatal("no pane cut the result list, so this check asserted nothing")
	}
}

// TestSearchPalette_TheBarNamesWhatEachStateAnswers states the bar's shape per
// state directly, so a reader does not have to reconstruct it from the sweeps.
//
// The sweeps are what hold it, and both directions were watched: restoring the
// literal's constant claim (`↑↓ move · enter open · esc close` in every state)
// has TestProseBar_TheFooterNamesExactlyTheKeysThatWork report the arrows and
// `enter` as named and dead over the empty box, a query that matched nothing
// and both load frames without rows; and dropping them from the bar while a
// search is out has it, the load-state biconditional and the hidden-state sweep
// report them acting unnamed over the results a refined query keeps drawn.
func TestSearchPalette_TheBarNamesWhatEachStateAnswers(t *testing.T) {
	failed := proseLoadDeliver(proseBarPaletteTyped(proseLoadDeps(http.StatusBadGateway), proseBarPaletteLoadQuery),
		proseLoadFailure(proseLoadScreen{
			name: "search palette/bar shape", fresh: func(d Deps) proseBarScreen {
				return proseBarPaletteTyped(d, proseBarPaletteLoadQuery)
			},
			loadCmd: func(s proseBarScreen) tea.Cmd {
				_, cmd := s.Update(searchTickMsg{query: proseBarPaletteLoadQuery})
				return cmd
			},
		}, http.StatusBadGateway))
	cases := []struct {
		name string
		s    proseBarScreen
		want string
	}{
		{"empty box", NewSearchPalette(Deps{}), "esc close"},
		{"searching, nothing yet", proseBarPaletteTyped(Deps{}, "hex"), "esc close"},
		{"no matches", proseBarPaletteAnswered("zzzz", nil), "esc close"},
		{"failed", failed, "esc close"},
		{"one result", proseBarPaletteAnswered("hex", proseBarPaletteResults(1)), "enter open · esc close"},
		{"results", proseBarPaletteAnswered("hex", proseBarPaletteResults(3)), "↑↓ move · enter open · esc close"},
		{"searching over results", proseBarPress(proseBarPaletteAnswered("hex", proseBarPaletteResults(3)), "b"),
			"↑↓ move · enter open · esc close"},
	}
	for _, c := range cases {
		if got := c.s.proseBar().hint(); got != c.want {
			t.Errorf("%s: the bar is %q, want %q", c.name, got, c.want)
		}
	}
	if p := failed.(*SearchPalette); p.loadErr == "" {
		t.Fatal("the failed case never failed, so its row asserted another state")
	}
}

// TestSearchPalette_NoMatchesIsAnAnswerNotAGuess: "No matches." and the count
// beside the box are claims about a query that was SEARCHED.
//
// Before the conversion the palette drew "No matches." the instant a query was
// typed — before the debounce had sent it — and compared the search's TRIMMED
// query against the box's raw value, so a query with a trailing space dropped
// its own debounce and was never searched at all while the pane said it had
// matched nothing.
func TestSearchPalette_NoMatchesIsAnAnswerNotAGuess(t *testing.T) {
	typed := proseBarSize(proseBarPaletteTyped(Deps{}, "zzzz"), 80, 24)
	if got := stripANSI(typed.View()); strings.Contains(got, "No matches") || !strings.Contains(got, "searching…") {
		t.Errorf("a query nobody has searched yet must say it is being searched, not that it "+
			"matched nothing:\n%s", got)
	}
	answered := proseBarSize(proseBarPaletteAnswered("zzzz", nil), 80, 24)
	if got := stripANSI(answered.View()); !strings.Contains(got, "No matches.") || !strings.Contains(got, "0 match(es)") {
		t.Errorf("a query answered with nothing must say so:\n%s", got)
	}

	spaced := proseBarPaletteTyped(proseLoadDeps(http.StatusBadGateway), "hex ")
	if _, cmd := spaced.Update(searchTickMsg{query: "hex"}); cmd == nil {
		t.Error("the debounce for a query with a trailing space sent no search")
	}
	next, _ := spaced.Update(searchResponseMsg{query: "hex", results: proseBarPaletteResults(2)})
	if p := next.(*SearchPalette); len(p.results) != 2 || p.loading {
		t.Errorf("the answer to a query with a trailing space was dropped: %d results, loading %t",
			len(p.results), p.loading)
	}
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// proseBarPaletteTyped is a palette with `q` TYPED into it, key by key, the way
// an operator reaches a search — so its debounce and loading flag are the ones
// the keystrokes really set.
func proseBarPaletteTyped(d Deps, q string) proseBarScreen {
	var s proseBarScreen = NewSearchPalette(d)
	for _, r := range q {
		s = proseBarPress(s, string(r))
	}
	return s
}

// proseBarPaletteAnswered is a palette whose typed query has been answered.
func proseBarPaletteAnswered(q string, results []omsapi.SearchResult) proseBarScreen {
	s := proseBarPaletteTyped(Deps{}, q)
	next, _ := s.Update(searchResponseMsg{query: q, results: results})
	return next.(proseBarScreen)
}

// proseBarPaletteResults is `n` results at the length OMS really serves — past
// the 51 cells an 80-column pane gives, so the clip is exercised — differing at
// the FRONT, since a check telling rows apart cannot see a difference past the
// clip. Every other one carries a subtitle, so rows are one and two lines.
func proseBarPaletteResults(n int) []omsapi.SearchResult {
	types := []string{"inventory", "asset", "purchase_order", "supplier", "location"}
	out := make([]omsapi.SearchResult, 0, n)
	for i := 0; i < n; i++ {
		r := omsapi.SearchResult{
			Type:  types[i%len(types)],
			ID:    fmt.Sprint(i + 1),
			Title: fmt.Sprintf("R%02d · Hex bolt M8x40 zinc plated, box of one hundred, grade 8.8", i+1),
		}
		if i%2 == 0 {
			r.Subtitle = "Fastener aisle, bin 12 · Grainger Industrial Supply, Hillsboro branch"
		}
		out = append(out, r)
	}
	return out
}

// Compile-time: the palette states the record contract.
var _ proseBarScreen = (*SearchPalette)(nil)
