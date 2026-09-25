// No row a LIST screen draws is wider than the pane it is drawn into, and
// whatever a row gives up it says so.
//
// THE DEFECT. list.go's bodyView wrote row.Title, row.Subtitle and
// row.MetricsLine straight into the pane with NO BOUND AT ALL. clampToBox then
// cut them at the pane's right edge, from the right, with no mark — so a row
// that had lost its supplier and its price read as a complete one. Measured at
// 80 columns, where screenBodyCells gives 51, with the content OMS really
// serves:
//
//	60 cells  "▸ Hex bolt M8x40 zinc-plated DIN 933 grade 8.8 full thread"
//	67 cells  "    Acme Fasteners & Industrial Supply Company Limited · $12,345.67"
//
// It fits from 100 columns up, so it was an 80-column defect specifically — and
// 80 is the width the size contract guarantees.
//
// WHY NOTHING REPORTED IT. The list sweeps' own fixture (listFixtureRows) draws
// "Row 1" and "Acme Supply Company · $0,234.56": every legibility assertion in
// list_bar_honesty_test.go passed because no fixture in the package had ever
// rendered a list row at the length OMS actually carries. That is the
// vacuous-fixture rule, and it is why this file carries its own fixture rather
// than reusing that one — listFixtureRows is shaped for the LINE-COUNT
// arithmetic (which rows cost two lines, which cost three) and is deliberately
// left short there, because widening it would change the row heights every
// other sweep in that file is calibrated against.
//
// THE CHROME IS NOT THIS. The header and the footer fold against
// listPaneCells() already (ListScreen.footerRows, headerLine) and were measured
// clean; this is the ROWS between them, which nothing bounded.
package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// listWideFixtures is one row per SHAPE bodyView draws, carrying the content
// OMS really serves rather than the short placeholders the other sweeps use.
//
// The three shapes are the three lines a row can render: the title line (always
// drawn, and the one the highlight pads), a Subtitle, and a MetricsLine — which
// is PRE-STYLED (bold labels) and is therefore the one a naive clip mangles
// twice over, once by cutting a number in half and once by dropping the closing
// SGR reset into everything drawn after it.
//
// Every value here is longer than the 51 cells an 80-column pane gives, because
// a fixture that cannot reach the bound under test makes the assertion vacuous
// however precisely it is worded.
func listWideFixtures() []listRow {
	i := func(n int) *int { return &n }
	f := func(n float64) *float64 { return &n }
	metrics := &omsapi.ItemMetrics{
		CurrentStock:      i(1284),
		QuantityOnOrder:   i(96),
		QuantityAvailable: f(1188),
		QuantityCommitted: f(96),
		QuantityInTransit: i(24),
		ReorderPoint:      i(250),
		LeadTimeDays:      f(14),
		UnitCost:          omsapi.DecimalString("12.34"),
		CostTrend:         "up",
	}
	return []listRow{
		{
			ID:       "1",
			Title:    "Hex bolt M8x40 zinc-plated DIN 933 grade 8.8 full thread",
			Tag:      "needs-reorder",
			Subtitle: "Acme Fasteners & Industrial Supply Company Limited · $12,345.67",
		},
		{
			ID:          "2",
			Title:       "Hydraulic pump seal kit, Bridgeport series 2 knee mill",
			Tag:         "retired",
			MetricsLine: formatItemMetricsRow(metrics, "AF-99887766-XZ", metricsRowOpts{withSKU: true, boldLabels: true}),
		},
		{
			ID:       "3",
			Title:    "Vendor work order: replace spindle bearings and re-shim head",
			Tag:      "in_progress",
			Subtitle: "WO-2026-0412 · emergency · Precision Machine Rebuilders Inc · Bridgeport Series 2",
		},
		{
			// A row with BOTH continuation lines: the loaders do not build one
			// today, but listRow permits it and rowLineCost counts it, so the
			// bound has to hold for the shape rather than for the callers.
			ID:          "4",
			Title:       "Sodium hydroxide pellets, ACS reagent grade, 2.5 kg bottle",
			Subtitle:    "Chemical Distribution Partners of the Upper Midwest · $1,099.00 · 4 items",
			MetricsLine: formatItemMetricsRow(metrics, "CHEM-000442199", metricsRowOpts{withSKU: true, boldLabels: true}),
		},
	}
}

// listWideScreen is a surface loaded with the wide fixture, sized to the pane
// under test.
func listWideScreen(t *testing.T, build func() *ListScreen, w, h int) *ListScreen {
	t.Helper()
	s := build()
	s.loading = false
	s.rows = listWideFixtures()
	s.windowSize = len(s.rows)
	sized, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	out, ok := sized.(*ListScreen)
	if !ok {
		t.Fatalf("ListScreen.Update returned %T, want *ListScreen", sized)
	}
	return out
}

// listRowLines is what the SCREEN hands over, before clampToBox has had its
// say. That is the only render an overrun is visible in: after the clamp no
// frame can be too wide, because the truncation has already happened.
func listRowLines(s *ListScreen) []string {
	return strings.Split(s.View(), "\n")
}

// listClippedRowLines is the pane an OPERATOR reads: the screen's own render
// with clampToBox applied exactly as Root.View applies it. A partial number is
// only ever visible here — the screen hands over the whole figure and the clamp
// is what cuts it in half, which is the entire shape of the reported defect.
func listClippedRowLines(s *ListScreen) []string {
	return strings.Split(clampToBox(s.View(), screenBodyCells(s.terminalWidth), s.listPaneRows()), "\n")
}

// TestList_NoRowRunsPastThePane is the width bound outright.
func TestList_NoRowRunsPastThePane(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	if len(widths) == 0 || len(heights) == 0 {
		t.Fatal("no drawable panes — the derivation is broken, not the app")
	}
	drawn := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, w := range widths {
				for _, h := range heights {
					for cursor := range listWideFixtures() {
						s := listWideScreen(t, surface.build, w, h)
						s.cursor = cursor
						s.scrollIntoView()
						if !s.paneDrawn() {
							continue
						}
						drawn++
						for n, line := range listRowLines(s) {
							if got := lipgloss.Width(line); got > screenBodyCells(w) {
								t.Errorf("%dx%d cursor=%d line %d is %d cells, pane is %d:\n%q",
									w, h, cursor, n, got, screenBodyCells(w), line)
							}
						}
					}
				}
			}
		})
	}
	if drawn == 0 {
		t.Fatal("no pane was drawn — the sweep measured nothing")
	}
}

// listNumberRun finds a run of digits together with the currency, grouping and
// decimal characters around it: "$12,345.67" and "1284" are each one run.
var listNumberRun = regexp.MustCompile(`\$?\d[\d,.]*`)

// TestList_ARowNeverDrawsAPartialNumber holds the half of the rule a width
// bound alone does not reach: a number is shown WHOLE or dropped, never clipped
// into a partial. "$12,34…" still reads as money and is worse than an absent
// price, which is the reason fitFactCell exists on the columnar layer.
//
// It is asserted by TOKEN rather than by eye: every numeric run drawn on a row
// line must appear, in full, in the row's own unclipped source. A run that is a
// PREFIX of a source run and not equal to it is a number cut in half.
//
// IT IS ASKED OF THE CONTINUATION LINES AND NOT OF THE TITLE, and the scope is
// a decision rather than an oversight. A list row is the same two kinds of part
// poFitRow already names and there is no third: the TITLE is a BOUNDED
// IDENTIFIER, which abbreviates and marks, and the Subtitle and MetricsLine are
// FACT LINES, where a figure is whole or it is not drawn. A title's digits are
// incidental to prose — a DIN number, a bolt grade, a mill series — and
// fitFactCell's whole-or-drop rule applied to prose blanks the row's IDENTITY,
// which is the one thing an operator scans a list for; a title that is a single
// unbroken part number would draw as "…" and nothing else. So a title
// abbreviates exactly as every other identifier in this package does (a picker
// row's name, a cart row's label), with the mark IMMEDIATELY beside the cut, and
// the figures a list draws all ride the lines below it.
//
// The corollary worth keeping: if a figure is ever moved onto a title line, this
// scope stops being safe and the title needs the fact rule instead.
func TestList_ARowNeverDrawsAPartialNumber(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	fixtures := listWideFixtures()
	sources := make([]string, 0, len(fixtures)*3)
	for _, r := range fixtures {
		sources = append(sources, r.Title, r.Subtitle, stripANSI(r.MetricsLine))
	}
	whole := map[string]bool{}
	for _, src := range sources {
		for _, run := range listNumberRun.FindAllString(src, -1) {
			whole[run] = true
		}
	}
	if len(whole) == 0 {
		t.Fatal("the fixture carries no numbers — the check cannot fail")
	}
	checked := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, w := range widths {
				for _, h := range heights {
					for cursor := range fixtures {
						s := listWideScreen(t, surface.build, w, h)
						s.cursor = cursor
						s.scrollIntoView()
						if !s.paneDrawn() {
							continue
						}
						for _, line := range listClippedRowLines(s) {
							plain := stripANSI(line)
							// A CONTINUATION line is the only one this is about,
							// and it is the only line on the pane that opens on
							// the row indent: a title line opens on its 2-cell
							// marker and then the date or the title itself, the
							// header and the marker rows on two spaces, and the
							// footer on none.
							if !strings.HasPrefix(plain, listRowIndent) {
								continue
							}
							checked++
							for _, run := range listNumberRun.FindAllString(plain, -1) {
								if whole[run] {
									continue
								}
								for src := range whole {
									if src != run && strings.HasPrefix(src, run) {
										t.Errorf("%dx%d cursor=%d drew %q, a partial of %q:\n%q",
											w, h, cursor, run, src, plain)
										break
									}
								}
							}
						}
					}
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no continuation line was read — the sweep measured nothing, which is " +
			"how a check about a cut passes over every cut there is")
	}
}

// TestList_ACutRowSaysSo is the mark: a row that gave something up carries
// paneCutMark, so it cannot be read as a whole one. Asserted at the width the
// contract guarantees, where every fixture row is genuinely cut.
//
// IT IS ASKED OF THE SEARCH OVERLAY TOO, on the surfaces that have one. The
// overlay draws its own head and then the SAME bodyView, so the rows under a
// query are the rows this bound is about — and a search is exactly where an
// OMS-length title arrives, since the asset list and searchKits forward the query
// to the server and hand back whatever it names. The rows are bounded there by
// construction rather than by a second code path, which is the point: this
// presses the state rather than reasoning about it.
func TestList_ACutRowSaysSo(t *testing.T) {
	const w, h = 80, 30
	overlays := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for cursor := range listWideFixtures() {
				for _, searching := range []bool{false, true} {
					if listAssertCutRowSaysSo(t, surface.build, w, h, cursor, searching) && searching {
						overlays++
					}
				}
			}
		})
	}
	if overlays == 0 {
		t.Error("no search overlay was reached — every surface declined `/`, so the " +
			"overlay half of this check asserted nothing")
	}
}

// listAssertCutRowSaysSo reports whether it reached the state it was asked for:
// a surface with no server search has no overlay to press, and `false` there is
// what keeps the overlay axis from asserting nothing everywhere at once.
func listAssertCutRowSaysSo(t *testing.T, build func() *ListScreen, w, h, cursor int, searching bool) bool {
	t.Helper()
	s := listWideScreen(t, build, w, h)
	if searching {
		next, _ := s.Update(listRuneKey("/"))
		s = next.(*ListScreen)
		if !s.searching {
			return false // this surface has no server search, so it has no overlay
		}
		s.rows = listWideFixtures()
		s.windowSize = len(s.rows)
	}
	s.cursor = cursor
	s.scrollIntoView()
	if !s.paneDrawn() {
		t.Fatalf("%dx%d refuses the pane — the fixture cannot be measured", w, h)
	}
	lines := listRowLines(s)
	for i, row := range listWideFixtures() {
		for _, want := range []string{row.Title, row.Subtitle, stripANSI(row.MetricsLine)} {
			if want == "" {
				continue
			}
			if lipgloss.Width(want) <= screenBodyCells(w) {
				t.Fatalf("row %d value %q is %d cells and the pane is %d — the fixture does "+
					"not reach the bound, so the check is vacuous",
					i, want, lipgloss.Width(want), screenBodyCells(w))
			}
			line := listLineCarrying(lines, want)
			if line == "" {
				continue // windowed out at this cursor
			}
			if strings.Contains(stripANSI(line), want) {
				t.Errorf("searching=%v row %d at %dx%d drew %q whole into a %d-cell pane — "+
					"nothing bounded it:\n%q", searching, i, w, h, want, screenBodyCells(w), line)
				continue
			}
			if !strings.Contains(line, paneCutMark) {
				t.Errorf("searching=%v row %d at %dx%d cursor=%d drew a cut value with no "+
					"mark:\n%q\nwanted a prefix of %q", searching, i, w, h, cursor, line, want)
			}
		}
	}
	return true
}

// listLineCarrying finds the drawn line a source value was cut down onto, by
// its leading words — enough of the value to identify the row and short enough
// to survive the cut.
func listLineCarrying(lines []string, want string) string {
	head := want
	if r := []rune(head); len(r) > 10 {
		head = string(r[:10])
	}
	for _, line := range lines {
		if strings.Contains(stripANSI(line), head) {
			return line
		}
	}
	return ""
}

// TestList_FittingARowChangesNoLineCount is the containment claim, VERIFIED
// rather than taken on trust — it is the one that decides whether bounding
// these three values is a contained change or a window rewrite.
//
// THE CLAIM. A list window is measured in LINES, not rows: rowLineCost counts
// one line for the title plus one for each of a Subtitle and a MetricsLine, and
// rowsFittingFrom packs rows into listBodyLines() by that cost. Sizing a window
// over the cheap rows and keeping it through a scroll is what once put twenty
// lines into an eighteen-line pane. So if CLIPPING could change what a row
// renders to, every one of those sums would be wrong and the fix would reach
// far past bodyView.
//
// It cannot, and the mechanism is worth stating: the fitters CLIP, they never
// FOLD — nothing they return carries a newline — and each of the three values
// is written followed by exactly one "\n" whatever its length. Clipping also
// cannot empty a value that was set, which is the other half of rowLineCost's
// arithmetic: the fit runs only where room is positive, and every branch of it
// returns at least the mark.
//
// HOW IT IS ASSERTED. Not by reading the fitters — by rendering the SAME row
// SHAPES twice, once with content OMS really serves and once with content short
// enough that nothing is cut, and requiring the two panes to have the same
// number of lines at every pane Root draws. That measures the property the
// window arithmetic actually depends on, and it would fail just as loudly if a
// later change folded a subtitle onto two rows.
func TestList_FittingARowChangesNoLineCount(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	short := make([]listRow, 0, len(listWideFixtures()))
	for i, r := range listWideFixtures() {
		s := listRow{ID: r.ID, Title: fmt.Sprintf("t%d", i), Tag: r.Tag}
		if r.Subtitle != "" {
			s.Subtitle = "s"
		}
		if r.MetricsLine != "" {
			s.MetricsLine = "m"
		}
		short = append(short, s)
	}
	compared := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, w := range widths {
				for _, h := range heights {
					for cursor := range listWideFixtures() {
						wide := listWideScreen(t, surface.build, w, h)
						wide.cursor = cursor
						wide.scrollIntoView()
						lean := listWideScreen(t, surface.build, w, h)
						lean.rows = short
						lean.cursor = cursor
						lean.scrollIntoView()
						compared++
						if got, want := len(listRowLines(wide)), len(listRowLines(lean)); got != want {
							t.Errorf("%dx%d cursor=%d: the OMS-length rows render %d lines and "+
								"the same row shapes short render %d — clipping moved the window "+
								"arithmetic", w, h, cursor, got, want)
						}
					}
				}
			}
		})
	}
	if compared == 0 {
		t.Fatal("nothing was compared — the sweep measured nothing")
	}
}

// TestList_TheFittersNeverFold is the mechanism behind the claim above, asked
// directly and at every room a drawable pane can hand them, INCLUDING the
// degenerate ones a pane cannot reach — a bound that only holds at the widths
// in use is a bound waiting for the size contract to move.
func TestList_TheFittersNeverFold(t *testing.T) {
	for _, row := range listWideFixtures() {
		for _, v := range []string{row.Title, row.Subtitle, row.MetricsLine} {
			if v == "" {
				continue
			}
			for room := 1; room <= 140; room++ {
				for _, mark := range []string{paneCutMark, StyleMuted.Render(paneCutMark)} {
					got := listFitFacts(v, room, mark)
					if strings.Contains(got, "\n") {
						t.Fatalf("listFitFacts(%q, %d) folded: %q", v, room, got)
					}
					if got == "" {
						t.Fatalf("listFitFacts(%q, %d) emptied a value that was set", v, room)
					}
					if w := lipgloss.Width(got); w > room {
						t.Fatalf("listFitFacts(%q, %d) is %d cells", v, room, w)
					}
				}
				line := poFitRow(room, row.Title, "", " "+StyleMuted.Render("("+row.Tag+")"))
				if strings.Contains(line, "\n") {
					t.Fatalf("poFitRow(%d, %q) folded: %q", room, row.Title, line)
				}
			}
		}
	}
}
