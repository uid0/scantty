package tui

import (
	"context"
	"fmt"
	"go/ast"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// Where does ScanTTY present a lateness or variance figure to a person?
//
// THE DERIVED SET. Three tabs, and they are derived rather than listed: a
// lateness or variance figure is one whose value is a DIFFERENCE from a promise
// or a RATE counted off one, and in this program every such figure comes off
// OMS's LeadTimeLog.variance_days — internal/omsapi's ReorderSupplierPerformance
// (on_time / early / late delivery rate), ReorderLeadTimeTrend
// (average_variance_days, on_time_delivery_rate) and PurchasingLeadTime
// (avg_variance, on_time_rate). Grep those field names and every render is the
// reorders Supplier perf tab, the reorders Lead-time trends tab and the
// purchasing Lead time tab. This file sweeps whatever is MARKED on the pane, so
// a fourth is covered the day it is marked and reported the day it is not.
//
// THE DELIBERATE EXCLUSIONS, each because it is a different fact rather than
// because it was awkward:
//
//   - EarlyDeliveryRate is decoded and never rendered. A figure that reaches
//     no person cannot mislead one. (The transparency feed's cost_variance used
//     to be the same, and is no longer decoded at all: OMS #1057 withdrew it,
//     because it was actual_cost minus a live re-quote of the ITEM and there
//     was never a budget under it — see ReorderTransparencyOrder.)
//   - Receiving's quantity variance (receive_form.go's "2 over" / "3 short",
//     the PO sheet's "Variance ..... ! N lines short or over"). A QUANTITY
//     against the quantity ordered, which is on the same row — one promise, and
//     it is shown.
//   - Every overdue reading: work orders (wo_detail), maintenance items and the
//     asset sheet's schedule block, the PM board's counts, the asset report's
//     "Overdue d" column, the logistics tab's "PM overdue". All are a date or
//     an interval the row also displays ("every 90d", "next due: …"), so their
//     yardstick is named by construction — and they are the shop's own schedule
//     rather than a vendor's promise.
//   - demand_forecast's "overdue by 3d", against a reorder due date the same
//     screen shows, and about stock running out rather than a supplier.
//   - po_detail's "SHIP BY <date>" colouring, which is a DATE and not a
//     lateness figure, and which prints the yardstick as its own value.
//   - wo_detail's "45m / est 60m", which prints the yardstick beside the value.
//
// WHAT IS ASSERTED HERE. That no MARKED figure reaches the pane without the
// legend that explains it, at every pane Root draws; that no figure is ever cut
// into a different number; that a column the pane cannot hold is NAMED rather
// than silently gone; and that the legend says what the SERVER said, including
// when the server said nothing.

// --- the swept screens ------------------------------------------------------

// reportScreenFixtures is every tabbed report screen, by constructor name.
// TestReportTable_EveryReportScreenIsSwept derives the roster from the package
// source and fails on an omission or a stale entry, so a report added later
// joins these sweeps without anyone remembering to.
var reportScreenFixtures = map[string]func() *ReportTableScreen{
	"NewInventoryReportScreen":  func() *ReportTableScreen { return NewInventoryReportScreen(Deps{}) },
	"NewPurchasingReportScreen": func() *ReportTableScreen { return NewPurchasingReportScreen(Deps{}) },
	"NewAssetReportScreen":      func() *ReportTableScreen { return NewAssetReportScreen(Deps{}) },
	"NewReorderAnalyticsReportScreen": func() *ReportTableScreen {
		return NewReorderAnalyticsReportScreen(Deps{})
	},
	"NewForgeKeyFleetReportScreen": func() *ReportTableScreen { return NewForgeKeyFleetReportScreen(Deps{}) },
}

func TestReportTable_EveryReportScreenIsSwept(t *testing.T) {
	_, files := jdeParsePackage(t)
	found := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
				continue
			}
			star, ok := fn.Type.Results.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			id, ok := star.X.(*ast.Ident)
			if !ok || id.Name != "ReportTableScreen" {
				continue
			}
			// NewReportTableScreen is the constructor the concrete screens are
			// built WITH; it defines no tabs of its own.
			if fn.Name.Name == "NewReportTableScreen" {
				continue
			}
			found[fn.Name.Name] = true
		}
	}
	if len(found) == 0 {
		t.Fatal("the source walk found no report screens, so every sweep in this file would be vacuous")
	}
	for name := range found {
		if _, ok := reportScreenFixtures[name]; !ok {
			t.Errorf("%s builds a report screen and no fixture sweeps it. Add one to "+
				"reportScreenFixtures — a report that names no yardstick is exactly "+
				"what these sweeps exist to report", name)
		}
	}
	for name := range reportScreenFixtures {
		if !found[name] {
			t.Errorf("reportScreenFixtures has %s, which no longer builds a report screen", name)
		}
	}
}

// --- fixtures that reach the bounds -----------------------------------------

// reportSweepRows builds rows shaped like the data these reports really carry:
// a long OMS-supplied name in every identifier column and a DISTINCT, wide
// figure in every fact column.
//
// Both halves are the point. The identifiers reach the clip — every report
// fixture in this package used to write "Acme" and "Bolt", so no test had ever
// rendered a report row at the length OMS actually serves, which is why a
// supplier name pushing every numeric column off the pane survived. And the
// figures are distinct per column and per row, so asserting one is on the pane
// cannot be satisfied by a different cell that happens to look the same, and a
// figure that lost its tail to a clip stops being found.
func reportSweepRows(cols []reportColumn, n int) [][]string {
	rows := make([][]string, n)
	for r := 0; r < n; r++ {
		row := make([]string, len(cols))
		for i, c := range cols {
			if c.align == alignLeft {
				row[i] = fmt.Sprintf("Acme Industrial Supply Co %d-%d", r, i)
				continue
			}
			row[i] = fmt.Sprintf("%d%d.%d%%", r+1, i+1, r+i)
		}
		rows[r] = row
	}
	return rows
}

// reportSeed puts a loaded tab in front of the sweep without a network: the
// loaders are driven by their own targeted tests, and what these sweeps are
// about is the LAYOUT of whatever rows arrive.
func reportSeed(s *ReportTableScreen, tab int, rows [][]string, yardstick string) {
	s.active = tab
	st := &s.states[tab]
	st.rows = rows
	st.yardstick = yardstick
	st.loaded = true
	st.loading = false
	st.err = ""
	st.cursor = 0
	st.windowStart = 0
	s.scrollIntoView()
}

// reportViewCapture records the exact screen value Root rendered, so the height
// sweep can assert both sides of the boundary with one render: what the screen
// assembled and what Root clipped for the terminal.
type reportViewCapture struct {
	s        *ReportTableScreen
	rendered string
}

func (c *reportViewCapture) Init() tea.Cmd { return c.s.Init() }
func (c *reportViewCapture) Update(msg tea.Msg) (Screen, tea.Cmd) {
	next, cmd := c.s.Update(msg)
	c.s = next.(*ReportTableScreen)
	return c, cmd
}
func (c *reportViewCapture) Title() string { return c.s.Title() }
func (c *reportViewCapture) View() string {
	c.rendered = c.s.View()
	return c.rendered
}

// reportRootView renders a report inside a real Root of this size and returns
// both the screen's assembled value and the CLIPPED pane. Root.View is the only
// render worth asserting the latter on: it clamps to the width the terminal
// really gives, and every defect this file is about is invisible to a screen
// measured on its own.
func reportRootView(t *testing.T, s *ReportTableScreen, w, h int) (string, []string) {
	t.Helper()
	capture := &reportViewCapture{s: s}
	r := newTestRoot(capture)
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	lines := strings.Split(after.View(), "\n")
	return capture.rendered, lines
}

func reportRootLines(t *testing.T, s *ReportTableScreen, w, h int) []string {
	t.Helper()
	_, lines := reportRootView(t, s, w, h)
	return lines
}

// reportPaneLines strips Root's left nav column off each rendered row, leaving
// the screen's own pane.
//
// Without it every prose assertion below is wrong in both directions: the nav
// column sits BETWEEN two folded halves of a sentence, so flattening the raw
// rows turns "…are measured" / "against the supplier's…" into "…are measured
// Purchasing │ against the supplier's…" and a phrase that is plainly on the
// screen reads as absent. The other direction is worse — the global status bar
// ends in "esc back", so a footer assertion for that segment passes on a screen
// whose own footer was clipped away.
//
// The nav is drawn first on every row, so the FIRST "│" is its border; rows
// with none (the rule and the status bar) are not the screen's pane at all.
func reportPaneLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		i := strings.Index(line, "│")
		if i < 0 {
			continue
		}
		out = append(out, line[i+len("│"):])
	}
	return out
}

func reportPaneText(lines []string) string {
	return strings.Join(reportPaneLines(lines), "\n")
}

// reportFlatPane is the pane with every run of whitespace — newlines included —
// collapsed to one space, for asserting PROSE.
//
// Every note on this screen folds, and a fold puts a newline and an indent in
// the middle of a phrase: at 48 columns the dropped-columns note really does
// draw "…: Spend, Avg" then "order" on the next line. A containment check on
// the raw pane reports that as the note never having been drawn, which is a
// test reporting a defect that is not there while saying nothing about the one
// it was written for.
func reportFlatPane(lines []string) string {
	return strings.Join(strings.Fields(strings.Join(reportPaneLines(lines), " ")), " ")
}

// The two ends of the dropped-columns note belowLines writes, as they survive
// flattening. The names sit between them, joined with ", ". The tail joint is
// matched WITHOUT its " · ", because pickerWrap drops the joint at a fold: the
// note reads "… 2 columns off the pane: Var*, On-time* · widen the terminal…"
// on one line and loses the "·" wherever it breaks there instead.
const (
	reportDropNoteLead = "off the pane: "
	reportDropNoteTail = "widen the terminal"
)

// reportDroppedNames reads the dropped-columns note off the rendered pane and
// returns the column headers it NAMES, whole.
//
// This is the only surface on which a column that did not fit is named — the
// header row carries reportDropMark and says how many, not which — so it is
// what "the pane accounts for this column" has to be asked of. Asking whether
// the header appears anywhere on the pane answers yes for a column that is
// simply DRAWN, which is how a clipped figure could pass for an accounted one.
func reportDroppedNames(flat string) map[string]bool {
	out := map[string]bool{}
	i := strings.Index(flat, reportDropNoteLead)
	if i < 0 {
		return out
	}
	names := flat[i+len(reportDropNoteLead):]
	if j := strings.Index(names, reportDropNoteTail); j >= 0 {
		names = names[:j]
	}
	for _, n := range strings.Split(names, ", ") {
		if n = strings.Trim(n, " ·"); n != "" {
			out[n] = true
		}
	}
	return out
}

// --- the yardstick reaches the pane with the figure -------------------------

// TestReportTable_NoMarkedFigureReachesThePaneWithoutItsLegend is the rule this
// work exists for, asked of the rendered screen at every pane Root draws.
//
// A column header carrying reportYardstickMark says "this figure is scored
// against a promise". The legend above the table says which promise. If the
// header can reach the operator and the legend cannot, the screen is asserting
// a bare lateness again — which is why the legend is drawn ABOVE the rows:
// clampToBox drops from the BOTTOM, so a short pane takes rows and never the
// legend.
//
// WHAT IS ASKED IS THE FIGURE AND NOT THE HEADER, and that is the difference
// between a check that reports this and one that cannot. A marked header's TEXT
// reaches the pane in two ways — as a column, and inside the note listing the
// columns that did NOT fit — so keyed on the header the sweep fires on a table
// that correctly dropped the column and said so. Keyed on the marked column's
// own VALUE, which is drawn only where the column is, it asks exactly the
// question the rule is about: is a figure scored against a promise in front of
// the operator with nothing saying which promise?
//
// The legend is asserted by "measured against", a phrase no other line on this
// screen carries, over the FLATTENED pane — every note here folds, and the
// legend's own words break across lines at the narrow end.
func TestReportTable_NoMarkedFigureReachesThePaneWithoutItsLegend(t *testing.T) {
	widths := jdeDrawableWidths()
	heights := jdePaneHeights()
	if len(widths) == 0 || len(heights) == 0 {
		t.Fatal("no drawable panes: the sweep would be vacuous")
	}
	marked, checked := 0, 0
	for name, build := range reportScreenFixtures {
		probe := build()
		for tab := range probe.tabs {
			cols := probe.tabs[tab].columns
			if !reportAnyMarked(cols) {
				continue
			}
			marked++
			rows := reportSweepRows(cols, 3)
			for _, w := range widths {
				for _, h := range heights {
					s := build()
					reportSeed(s, tab, rows, omsapi.VarianceYardstickQuotedLeadTime)
					lines := reportRootLines(t, s, w, h)
					flat := reportFlatPane(lines)
					for i, c := range cols {
						if !strings.HasSuffix(c.header, reportYardstickMark) {
							continue
						}
						if !strings.Contains(flat, rows[0][i]) {
							continue
						}
						checked++
						if !strings.Contains(flat, "measured against") {
							t.Fatalf("%s tab %q at %dx%d: the %q figure %q is on the pane and "+
								"no legend says what it is measured against:\n%s",
								name, probe.tabs[tab].label, w, h, c.header, rows[0][i],
								reportPaneText(lines))
						}
					}
				}
			}
		}
	}
	if marked == 0 {
		t.Fatal("no tab marks a column, so this sweep proved nothing. Every lateness " +
			"or variance column must carry reportYardstickMark")
	}
	if checked == 0 {
		t.Fatal("no pane in the whole sweep drew a marked figure, so the implication " +
			"was never tested")
	}
}

// reportAnyMarked reports whether any of these columns is scored against a
// promise, by the same mark the renderer reads.
func reportAnyMarked(cols []reportColumn) bool {
	for _, c := range cols {
		if strings.HasSuffix(c.header, reportYardstickMark) {
			return true
		}
	}
	return false
}

// --- no figure is ever cut, and a dropped column is named -------------------

// TestReportTable_EveryFigureIsWholeOrNamedAsMissing sweeps every report tab at
// every width Root draws and asserts the biconditional a fixed columnar table
// owes its reader: each fact is on the pane in FULL, or the pane says its
// column did not fit.
//
// The failure it reports is the one measured before this work: at 80 columns
// the purchasing Lead time tab drew a 75% on-time rate as "75", the reorders
// Supplier perf tab drew a 33.3% late rate as "33." and a $12,345.67 order
// value as "$12,3", and with a realistic supplier name every numeric column was
// off the pane under a header line reading "Or". A cut number reads as a number
// somebody meant, which is worse than an absent one — and an absent one that
// nothing accounts for is the silent discard.
//
// A TALL pane, because this is the WIDTH axis: the note naming the dropped
// columns is the first thing a short pane gives up (layoutRows), and asking two
// axes in one loop would report a height defect as a width one.
//
// THE TWO BRANCHES ARE MUTUALLY EXCLUSIVE, AND THE SECOND KEYS ON THE NOTE.
// It used to accept "the header is somewhere on the flattened pane" as proof
// the column had been dropped, which is true in one direction only: a column
// that IS drawn carries its header on the pane too. So a fact whose header is
// narrower than its widest cell — "Var*" is four cells over a "1234.5" value,
// "Late*" five over "33.33%" — could have its VALUE clipped to "123…" while its
// header still rendered whole, the first case would fail, the second would
// match on the still-drawn column header, and the sweep would pass over exactly
// the cut figure it exists to report. The dropped-columns note built in
// belowLines is the ONLY place a column that did not fit is NAMED, so the
// branch reads the names out of that note (reportDroppedNames) and matches them
// whole. A fact that is both drawn and named, or neither, falls through and
// FAILS.
func TestReportTable_EveryFigureIsWholeOrNamedAsMissing(t *testing.T) {
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("no drawable widths: the sweep would be vacuous")
	}
	whole, dropped := 0, 0
	for name, build := range reportScreenFixtures {
		probe := build()
		for tab := range probe.tabs {
			cols := probe.tabs[tab].columns
			rows := reportSweepRows(cols, 1)
			for _, w := range widths {
				s := build()
				reportSeed(s, tab, rows, omsapi.VarianceYardstickQuotedLeadTime)
				lines := reportRootLines(t, s, w, 40)
				pane, flat := reportPaneText(lines), reportFlatPane(lines)
				missing := reportDroppedNames(flat)
				for i, c := range cols {
					if c.align != alignRight {
						continue // an identifier abbreviates on purpose
					}
					onPane, named := strings.Contains(flat, rows[0][i]), missing[c.header]
					switch {
					case onPane && !named:
						whole++
					case named && !onPane:
						dropped++
					default:
						t.Fatalf("%s tab %q at width %d: the %q cell %q is on the pane=%v and "+
							"named in the dropped-columns note=%v — a fact is drawn WHOLE or it "+
							"is absent and named as off the pane, never neither and never both:\n%s",
							name, probe.tabs[tab].label, w, c.header, rows[0][i], onPane, named, pane)
					}
				}
			}
		}
	}
	if whole == 0 || dropped == 0 {
		t.Fatalf("the sweep saw %d whole figures and %d dropped columns — one of the two "+
			"branches was never reached, so the biconditional was only half tested",
			whole, dropped)
	}
}

// TestReportTable_TheScreenAssemblesNothingThePaneCannotHold is the complement
// of the sweep above, and the one that catches a cut nothing else can see: a
// styled line clampToBox truncates loses its closing SGR reset into everything
// drawn after it, colouring the rest of the frame.
//
// It measures the screen's OWN View against the pane budget rather than the
// clipped render, and that is the only way round that can fail: measured AFTER
// clampToBox no line can ever be too wide, because the truncation has already
// happened — the check would pass over exactly the defect it names. That is the
// same trap as measuring a clipped pane for a clipped value; here the claim is
// "nothing was cut", so what has to be measured is what was handed over.
func TestReportTable_TheScreenAssemblesNothingThePaneCannotHold(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	for name, build := range reportScreenFixtures {
		probe := build()
		for tab := range probe.tabs {
			cols := probe.tabs[tab].columns
			for _, w := range jdeDrawableWidths() {
				s := build()
				reportSeed(s, tab, reportSweepRows(cols, 3), omsapi.VarianceYardstickQuotedLeadTime)
				s.Update(tea.WindowSizeMsg{Width: w, Height: 40})
				room := screenBodyCells(w)
				for _, line := range strings.Split(s.View(), "\n") {
					if got := lipgloss.Width(line); got > room {
						t.Fatalf("%s tab %q at width %d: the screen assembled a %d-cell line "+
							"into a %d-cell pane, so clampToBox takes the tail: %q",
							name, probe.tabs[tab].label, w, got, room, line)
					}
				}
			}
		}
	}
}

// --- the legend says what the SERVER said -----------------------------------

// TestReportTable_TheLegendSaysWhatTheServerSaid holds the three answers apart.
// Naming the quote on a payload that never mentioned one would assert a promise
// nobody made, which is the same defect as naming none — "could not tell" and
// "found nothing" are different facts and an operator chasing a vendor acts
// differently on each.
func TestReportTable_TheLegendSaysWhatTheServerSaid(t *testing.T) {
	marked := []string{"On-time*", "Late*"}
	cases := []struct {
		label     string
		yardstick string
		want      []string
		notWant   []string
	}{
		{
			label:     "the server named the quote",
			yardstick: omsapi.VarianceYardstickQuotedLeadTime,
			want:      []string{"* On-time* and Late*", "standing quoted lead time", "not the delivery dates confirmed on the orders"},
		},
		{
			label:     "the server named something else",
			yardstick: "confirmed_delivery_date",
			want:      []string{"confirmed delivery date", "the server's own name"},
			notWant:   []string{"quoted lead time"},
		},
		{
			label:     "the server said nothing",
			yardstick: "",
			want:      []string{"did not say what they are measured against"},
			notWant:   []string{"quoted lead time"},
		},
	}
	for _, c := range cases {
		got := reportYardstickLegend(marked, c.yardstick)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: legend %q missing %q", c.label, got, w)
			}
		}
		for _, w := range c.notWant {
			if strings.Contains(got, w) {
				t.Errorf("%s: legend %q claims %q, which the server never said", c.label, got, w)
			}
		}
	}
	if reportYardstickLegend(nil, omsapi.VarianceYardstickQuotedLeadTime) != "" {
		t.Error("a table with no marked column must make no yardstick claim at all")
	}
}

// TestReportTable_OneRowWithoutTheKeySilencesTheWholeLegend: agreement is
// required, not a majority. A legend is a claim about every figure under it, so
// one row that did not say what it was measured against is enough to make the
// table unable to name a promise.
func TestReportTable_OneRowWithoutTheKeySilencesTheWholeLegend(t *testing.T) {
	quote := omsapi.VarianceYardstickQuotedLeadTime
	cases := []struct {
		label  string
		tokens []string
		want   string
	}{
		{"every row agrees", []string{quote, quote, quote}, quote},
		{"one row is silent", []string{quote, "", quote}, ""},
		{"the rows disagree", []string{quote, "confirmed_delivery_date"}, ""},
		{"there are no rows", nil, ""},
	}
	for _, c := range cases {
		if got := reportYardstickOf(c.tokens); got != c.want {
			t.Errorf("%s: reportYardstickOf(%v) = %q, want %q", c.label, c.tokens, got, c.want)
		}
	}
}

// --- the wire ---------------------------------------------------------------

// TestReportTable_EveryLatenessLoaderCarriesTheServersYardstick drives the three
// loaders through a real client and server. It is what makes the legend a
// report of the payload rather than a sentence this program made up: delete the
// decode and the legend goes silent instead of going wrong.
func TestReportTable_EveryLatenessLoaderCarriesTheServersYardstick(t *testing.T) {
	cases := []struct {
		label  string
		build  func(Deps) *ReportTableScreen
		tab    int
		served string
		silent string
	}{
		{
			label: "reorders supplier performance",
			build: NewReorderAnalyticsReportScreen,
			tab:   0,
			served: `[{"supplier_id":1,"supplier_name":"Acme","total_orders":3,"completed_orders":3,
				"average_lead_time_days":2.0,"on_time_delivery_rate":100.0,"late_delivery_rate":0.0,
				"damage_rate":0.0,"total_order_value":"99.00",
				"variance_measured_against":"quoted_lead_time"}]`,
			silent: `[{"supplier_id":1,"supplier_name":"Acme","total_orders":3,"completed_orders":3,
				"average_lead_time_days":2.0,"on_time_delivery_rate":100.0,"late_delivery_rate":0.0,
				"damage_rate":0.0,"total_order_value":"99.00"}]`,
		},
		{
			label: "reorders lead-time trends",
			build: NewReorderAnalyticsReportScreen,
			tab:   1,
			served: `[{"month":"2026-07","average_lead_time_days":6.4,"average_variance_days":1.4,
				"total_deliveries":11,"on_time_delivery_rate":63.6,
				"variance_measured_against":"quoted_lead_time"}]`,
			silent: `[{"month":"2026-07","average_lead_time_days":6.4,"average_variance_days":1.4,
				"total_deliveries":11,"on_time_delivery_rate":63.6}]`,
		},
		{
			label: "purchasing lead-time analysis",
			build: NewPurchasingReportScreen,
			tab:   2,
			served: `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt","total_orders":4,
				"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,"avg_variance":1.5,
				"on_time_rate":75.0,"variance_measured_against":"quoted_lead_time"}]`,
			silent: `[{"supplier_id":2,"supplier_name":"Acme","item_name":"Bolt","total_orders":4,
				"avg_estimated_lead_time":5.0,"avg_actual_lead_time":6.5,"avg_variance":1.5,
				"on_time_rate":75.0}]`,
		},
	}
	for _, c := range cases {
		served, _ := fixedBodyClient(t, c.served)
		body, err := c.build(Deps{OMS: served}).tabs[c.tab].loader(context.Background(), Deps{OMS: served})
		if err != nil {
			t.Fatalf("%s: loader: %v", c.label, err)
		}
		if body.yardstick != omsapi.VarianceYardstickQuotedLeadTime {
			t.Errorf("%s: yardstick = %q, want %q — the payload said so and the screen "+
				"must report it rather than a sentence of its own",
				c.label, body.yardstick, omsapi.VarianceYardstickQuotedLeadTime)
		}
		silent, _ := fixedBodyClient(t, c.silent)
		body, err = c.build(Deps{OMS: silent}).tabs[c.tab].loader(context.Background(), Deps{OMS: silent})
		if err != nil {
			t.Fatalf("%s (silent): loader: %v", c.label, err)
		}
		if body.yardstick != "" {
			t.Errorf("%s: an OMS that served no variance_measured_against was read as "+
				"saying %q. Silence is not the current answer", c.label, body.yardstick)
		}
	}
}

// TestReportTable_TheYardstickReachesTheScreenEndToEnd walks one tab the way an
// operator does — Init, the reply, View through a real Root — so the claim is
// about the SCREEN and not about a helper. The 80-column case is the one the
// project checks against.
func TestReportTable_TheYardstickReachesTheScreenEndToEnd(t *testing.T) {
	c, path := fixedBodyClient(t, `[{"supplier_id":2,"supplier_name":"Acme Industrial Supply Co",
		"item_name":"M8x40 hex bolt, zinc","total_orders":4,"avg_estimated_lead_time":5.0,
		"avg_actual_lead_time":6.5,"avg_variance":1.5,"on_time_rate":75.0,
		"variance_measured_against":"quoted_lead_time"}]`)
	s := NewPurchasingReportScreen(Deps{OMS: c})
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	s.active = 2
	s.states[2].loading = true
	s.Update(s.loadTab(2)())
	if *path != "/api/reorders/reports/purchasing/lead_time_analysis/" {
		t.Fatalf("path = %q", *path)
	}
	pane := reportPaneText(reportRootLines(t, s, 80, 30))
	for _, want := range []string{
		"* Var*",           // the legend, pointing at the mark
		"quoted lead time", // the promise it names
		"Var*",             // the marked header
		"On-time*",
		"75%", // the rate itself, whole: it used to be drawn as "75"
		"1.5",
	} {
		if !strings.Contains(pane, want) {
			t.Errorf("the 80-column pane is missing %q:\n%s", want, pane)
		}
	}
}

// --- the tab bar says which report you are reading --------------------------

// TestReportTable_TheTabBarAlwaysNamesTheActiveTab: drawn in full the bar is one
// long line and Root clips it from the right, so standing on the sixth tab of
// the reorders report at 80 columns showed the first three and no highlight at
// all — a bar that cannot show where you are is not a shortened bar, it is a
// wrong one.
//
// TWO ARMS, because the renderer has two and one pane genuinely cannot make both
// claims. Where the pane can hold the active label with the markers that say the
// other tabs exist, the whole label is on it; where it cannot, the label is
// ABBREVIATED and still there, which is the difference between a bar that
// answers "which report is this?" badly and one that does not answer at all.
//
// SINCE THE SIZE CONTRACT WAS SETTLED THE ABBREVIATED ARM IS UNREACHABLE, and
// that is asserted rather than counted. It was reached when Root drew from 45
// columns, which gave the bar 16 cells against the 19 " Stock by category "
// needs; the narrowest pane is 51 now and every label fits with its markers. So
// the guarantee is the strong one — the active tab is named IN FULL at every
// pane an operator can reach — and the abbreviating arm stays in the renderer
// for a label that grows. If one does, this goes red naming the pane, and the
// answer is a shorter label rather than a softer check.
//
// The boundary is asked of tabBarMarks, the renderer's own accounting, rather
// than restated: the marker cost is what the first attempt at this test left
// out, and it reported a correctly abbreviated bar as a missing one.
func TestReportTable_TheTabBarAlwaysNamesTheActiveTab(t *testing.T) {
	const prefixLen = 3
	fits, abbreviated := 0, 0
	for name, build := range reportScreenFixtures {
		probe := build()
		for tab := range probe.tabs {
			label := probe.tabs[tab].label
			for _, w := range jdeDrawableWidths() {
				s := build()
				reportSeed(s, tab, reportSweepRows(s.tabs[tab].columns, 2), "")
				room := screenBodyCells(w)
				need := lipgloss.Width(" "+label+" ") +
					StyleSidebarItemActive.GetHorizontalPadding() +
					s.tabBarMarks(tab, tab)
				flat := reportFlatPane(reportRootLines(t, s, w, 40))
				want, why := label, "name it"
				if need > room {
					abbreviated++
					want, why = label[:prefixLen], "name even the first few letters of it"
				} else {
					fits++
				}
				if !strings.Contains(flat, want) {
					t.Fatalf("%s at width %d: standing on tab %q and the bar does not %s:\n%s",
						name, w, label, why, reportPaneText(reportRootLines(t, s, w, 40)))
				}
			}
		}
	}
	if fits == 0 {
		t.Fatal("no pane could hold an active label whole, so this sweep asserted nothing " +
			"about the bar it is here for")
	}
	if abbreviated > 0 {
		t.Errorf("%d of %d panes could not hold the active tab's label whole, so an operator "+
			"reads a prefix on a pane the size contract says is big enough; shorten the label "+
			"rather than widening this check", abbreviated, fits+abbreviated)
	}
}

// --- the footer names the keys that work ------------------------------------

// TestReportTable_TheFooterReachesThePaneWhole: at 80 columns the old footer was
// 58 cells against a pane of 51 and lost "esc back" — the way out — off the
// right edge with nothing marking the cut. It folds now, and the row budget
// moves with the fold (layoutRows), so every segment reaches the operator.
func TestReportTable_TheFooterReachesThePaneWhole(t *testing.T) {
	c := NewReorderAnalyticsReportScreen(Deps{})
	cols := c.tabs[0].columns
	for _, w := range []int{80, 100, 120} {
		s := NewReorderAnalyticsReportScreen(Deps{})
		reportSeed(s, 0, reportSweepRows(cols, 12), omsapi.VarianceYardstickQuotedLeadTime)
		lines := reportRootLines(t, s, w, 40)
		flat := reportFlatPane(lines)
		for _, seg := range strings.Split(s.footerHint(12), " · ") {
			if !strings.Contains(flat, seg) {
				t.Errorf("width %d: the footer segment %q is not on the pane:\n%s",
					w, seg, reportPaneText(lines))
			}
		}
	}
}

// TestReportTable_TheFooterNamesMovementOnlyWhereItMoves. With one row j/k,
// pgup/pgdn and g/G move nothing, so naming them would be a bar promising a key
// that does not act.
func TestReportTable_TheFooterNamesMovementOnlyWhereItMoves(t *testing.T) {
	s := NewReorderAnalyticsReportScreen(Deps{})
	reportSeed(s, 0, reportSweepRows(s.tabs[0].columns, 1), "")
	if strings.Contains(s.footerHint(1), "move") {
		t.Errorf("one row: the footer names movement it cannot do: %q", s.footerHint(1))
	}
	if !strings.Contains(s.footerHint(2), "j/k ↑↓ move") {
		t.Errorf("two rows: the footer must name the movement keys: %q", s.footerHint(2))
	}
}

// TestReportTable_TheLoadingFooterNamesTheKeysThatSwitchReport is the state an
// operator LANDS in — a report screen's very first frame is !loaded — and the
// bar there named "esc back" alone while ←/→ and [/] switched report under it:
// updateKey's arms are unguarded, so switchTab moved the tab-bar highlight and
// replaced the body on a press the frame said nothing about.
//
// Asked of the RENDERED pane and of a real press through Root, in that order,
// because either half alone proves nothing: the segment on the pane says the
// bar makes the claim, the press says the claim is true. Swept over every
// report screen with more than one tab, at every width Root draws, because a
// footer that folds is a footer a narrow pane can lose the tail of.
func TestReportTable_TheLoadingFooterNamesTheKeysThatSwitchReport(t *testing.T) {
	const segment = "←/→ [/] switch report"
	swept := 0
	for name, build := range reportScreenFixtures {
		if len(build().tabs) < 2 {
			// Skipped because the PRESS half would be vacuous, not because the
			// bar says anything different: footerHint names the switch segment
			// on every branch but the no-tabs one, so a one-tab screen would
			// name it too, over a switchTab((0+1)%1) that lands where it stood.
			// No report screen has one tab today — every fixture here has at
			// least two — so this skip is reached by nothing.
			continue
		}
		for _, w := range jdeDrawableWidths() {
			s := build()
			lines := reportRootLines(t, s, w, 40)
			if flat := reportFlatPane(lines); !strings.Contains(flat, segment) {
				t.Fatalf("%s at width %d: the first frame is still loading and the bar does "+
					"not name %q, which switches report from it:\n%s",
					name, w, segment, reportPaneText(lines))
			}
			before := reportPaneText(lines)
			r := newTestRoot(s)
			next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: 40})
			after, ok := next.(Root)
			if !ok {
				t.Fatalf("Root.Update returned %T, want Root", next)
			}
			moved := reportPaneText(strings.Split(press(t, after, "right").View(), "\n"))
			if moved == before {
				t.Fatalf("%s at width %d: the bar names %q while loading and pressing → "+
					"redrew the pane byte for byte:\n%s", name, w, segment, before)
			}
			swept++
		}
	}
	if swept == 0 {
		t.Fatal("no multi-tab report screen was swept, so this proved nothing")
	}
}

// --- a cut error body says it was cut ---------------------------------------

// TestReportTable_AFailedLoadMarksWhatItCutOff. A report error is an OMS
// response body, and omsapi.parseError puts the ENTIRE raw payload into
// APIError.Message whenever the envelope carries no code — so this pane can be
// handed a 20 KB gateway page. It is bounded twice (cellPrefix before the fold,
// then the fold's own row cap) and neither cut used to be MARKED: the operator
// read six folded lines of HTML with nothing saying the sentence naming what
// actually failed was among what went.
//
// THE TWO CUTS KNOW DIFFERENT THINGS and the wordings differ accordingly, so
// both are driven, each by a fixture that can reach it:
//
//   - a MANY-LINE body inside the cellPrefix budget, where the fold knows how
//     many lines it left and names the number. It is never bounded at any
//     drawable width, so the counted wording is the only one it can produce,
//     and at the widths where it fits whole the pane must carry NO mark and the
//     body's own tail instead — the biconditional, not half of it.
//   - a multi-KB UNSPACED body, which is bounded at every drawable width. There
//     the count would be a count of the PREFIX, a figure about nothing, so the
//     row may only say that more exists than the pane can hold.
//
// Asserted on the CLIPPED Root pane, plus the assembled width of every line,
// because the row that says something was cut may itself be the row that runs
// off the pane.
func TestReportTable_AFailedLoadMarksWhatItCutOff(t *testing.T) {
	const (
		countedMark = "more line"
		boundedMark = "more of the"
		tail        = "TAILWORD"
	)
	// A body that folds past the failed frame's row budget at the pane the size
	// contract's floor gives, and inside it at the widest pane this sweep
	// reaches, so BOTH arms below are met by the same fixture.
	//
	// THE WORD LENGTH IS THE FIXTURE, not the character count. The old body was
	// eight nine-letter words, which folded to two rows and fitted everywhere
	// once Root stopped drawing below 80 columns — the counted arm was never
	// reached and this sweep's own guard said so. Nine long field names fold one
	// to a row at 51 cells and two to a row at 91, which is what puts the cut on
	// one side of the drawable range and the whole body on the other; a longer
	// string of short words cannot do it, because the fold wastes almost nothing
	// per row and the cellPrefix bound bites first, which marks the body BOUNDED
	// rather than counted and is a different claim.
	manyLines := strings.TrimSpace(strings.Repeat("unexpected-response-field-name ", 9)) +
		" " + tail
	unspaced := strings.Repeat("x", 20000)

	counted, whole, bounded := 0, 0, 0
	for _, w := range jdeDrawableWidths() {
		s := NewReorderAnalyticsReportScreen(Deps{})
		reportSeedErr(s, 0, manyLines)
		lines := reportRootLines(t, s, w, 40)
		flat := reportPaneText(lines)
		switch {
		case strings.Contains(flat, countedMark) && !strings.Contains(flat, tail):
			counted++
		case strings.Contains(flat, tail) && !strings.Contains(flat, countedMark):
			whole++
		default:
			t.Fatalf("width %d: a folded error body is either drawn whole or cut and "+
				"counted, never neither and never both:\n%s", w, flat)
		}
		if strings.Contains(flat, boundedMark) {
			t.Fatalf("width %d: the body is inside the cellPrefix budget, so the pane "+
				"must not claim more of it was thrown away than the fold left:\n%s", w, flat)
		}
		reportAssertAssembled(t, s, w)

		big := NewReorderAnalyticsReportScreen(Deps{})
		reportSeedErr(big, 0, unspaced)
		bigFlat := reportPaneText(reportRootLines(t, big, w, 40))
		if !strings.Contains(bigFlat, boundedMark) {
			t.Fatalf("width %d: a 20 KB error body reached the pane with nothing saying "+
				"a tail was dropped:\n%s", w, bigFlat)
		}
		if strings.Contains(bigFlat, countedMark) {
			t.Fatalf("width %d: the cellPrefix bound threw the rest away, so any line "+
				"count it named would be a count of the prefix:\n%s", w, bigFlat)
		}
		bounded++
		reportAssertAssembled(t, big, w)
	}
	if counted == 0 || whole == 0 || bounded == 0 {
		t.Fatalf("the sweep saw %d counted cuts, %d bodies drawn whole and %d bounded "+
			"bodies — a fixture that cannot reach the bound it names asserts nothing",
			counted, whole, bounded)
	}
}

// reportSeedErr puts a FAILED load in front of the sweep. The loaders are driven
// by their own targeted tests; what this is about is what the pane does with a
// body of any length.
func reportSeedErr(s *ReportTableScreen, tab int, err string) {
	s.active = tab
	st := &s.states[tab]
	st.rows = nil
	st.loaded = true
	st.loading = false
	st.err = err
}

// reportAssertAssembled fails on a line the screen HANDS OVER wider than the
// pane. Measured before clampToBox, which is the only way round that can fail:
// after it no line can be too wide, because the truncation has already happened.
func reportAssertAssembled(t *testing.T, s *ReportTableScreen, w int) {
	t.Helper()
	room := screenBodyCells(w)
	for _, line := range strings.Split(s.View(), "\n") {
		if got := lipgloss.Width(line); got > room {
			t.Fatalf("width %d: the screen assembled a %d-cell line into a %d-cell pane, "+
				"so clampToBox takes the tail: %q", w, got, room, line)
		}
	}
}

// TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas is the HEIGHT
// counterpart of TestReportTable_TheScreenAssemblesNothingThePaneCannotHold,
// and the axis that sweep is blind on: it measures assembled WIDTH at a fixed
// height of 40, so a frame one row too tall passed it at every width.
//
// The marker row used to be taken out of the BODY after the block below had
// been clamped, and the body's floor at one row then handed it straight back —
// so whenever the block below squeezed the body to a single row the frame came
// to avail+1 rows, the marker was drawn anyway (View decides that for itself),
// and clampToBox took the tail: the FOOTER, which layoutRows' own doc says
// never gives. Watched failing at 80 columns on the reorders analytics
// "Supplier perf" tab with 8 rows, at every terminal height from 19 to 22
// inclusive — each one row over, each dropping the line carrying
// `r refresh · esc back` and leaving no named way off the screen — and passing
// at 23, where the block below no longer squeezes the body that far.
//
// Measured on what the screen HANDS OVER, before clampToBox, which is the only
// way round that can fail — measured after, no frame can ever be too tall,
// because the truncation has already happened. That is the trap the width sweep
// records at its own site.
//
// EVERY BRANCH, because seeding only loaded 8-row tabs is how the SECOND
// instance of this got through: the failed-load frame consulted no budget at
// all and was laid out against a flat six-row constant, so at 80 columns it
// needed a sixteen-row terminal where the frame it replaced needed eleven.
// Watched failing at 80 columns in the ERROR state at every terminal height
// from 12 to 15 inclusive — a ten-row frame handed to a six-to-nine-row pane,
// dropping the footer that carries `esc back` off a frame an operator had
// reached by a load FAILING, and at 12 and 13 the row saying the error was cut
// with it. (10 and 11 are the band on both sides of the fix: the pane cannot
// hold the first error line, its mark and the footer, which is the floor the
// give-order will not go below.) A guard that
// cannot fail is not a guard, so the loading, failed and empty states are swept
// beside the loaded one and the failed fixture carries a multi-KB unspaced body
// — the shape omsapi.parseError really produces — which reaches the bound at
// every drawable pane rather than only at narrow ones.
//
// BOTH HALVES ARE ASSERTED so neither can later be traded for the other: the
// footer reaches the CLIPPED pane whole, and a cut error body still carries the
// row that says it was cut. Buying a line of error text with the mark, or with
// the way out, is the defect in each direction.
//
// SCOPED at frameFits, the screen's own answer for the branch it is drawing,
// and BOTH SIDES COUNTED, because below it the overrun is the documented band
// rather than a defect. A scoping nothing falls outside of would be a way of
// asserting nothing, so a sweep that never reached one side fails.
func TestReportTable_TheScreenAssemblesNoMoreRowsThanThePaneHas(t *testing.T) {
	var fits, tooShort atomic.Int64
	t.Run("sweep", func(t *testing.T) {
		for name, build := range reportScreenFixtures {
			probe := build()
			for tab := range probe.tabs {
				rows := reportSweepRows(probe.tabs[tab].columns, 8)
				for _, state := range reportFrameStates(rows) {
					for _, w := range jdeDrawableWidths() {
						t.Run(fmt.Sprintf("%s/%s/%s/%d", name, probe.tabs[tab].label, state.label, w), func(t *testing.T) {
							t.Parallel()
							s := build()
							state.seed(s, tab)
							for _, h := range jdePaneHeights() {
								view, lines := reportRootView(t, s, w, h)
								if !s.frameFits() {
									tooShort.Add(1)
									continue
								}
								fits.Add(1)
								room, got := s.reportPaneRows(), strings.Split(view, "\n")
								if len(got) > room {
									t.Fatalf("%s tab %q %s at %dx%d: the screen assembled %d rows into "+
										"a %d-row pane, so clampToBox takes the tail — and what is drawn "+
										"last is the footer:\n%s",
										name, probe.tabs[tab].label, state.label, w, h, len(got), room,
										view)
								}
								flat := reportFlatPane(lines)
								if !strings.Contains(flat, "esc back") {
									t.Fatalf("%s tab %q %s at %dx%d: the footer names no way off the "+
										"screen:\n%s", name, probe.tabs[tab].label, state.label, w, h,
										reportPaneText(lines))
								}
								if state.cutMark != "" && !strings.Contains(flat, state.cutMark) {
									t.Fatalf("%s tab %q %s at %dx%d: the error body was cut with nothing "+
										"saying so:\n%s", name, probe.tabs[tab].label, state.label, w, h,
										reportPaneText(lines))
								}
							}
						})
					}
				}
			}
		}
	})
	if fits.Load() == 0 || tooShort.Load() == 0 {
		t.Fatalf("the sweep saw %d panes that could hold the give-order's floor and %d "+
			"that could not — one side was never reached, so the scoping asserted nothing",
			fits.Load(), tooShort.Load())
	}
}

// reportFrameState is one of the frames View can draw, with what the pane must
// still carry once it has been drawn.
type reportFrameState struct {
	label string
	seed  func(s *ReportTableScreen, tab int)
	// cutMark is a fragment the pane must carry because this state's body is
	// always cut. Empty where nothing is cut.
	cutMark string
}

// reportFrameStates is every branch View can reach on a screen that HAS tabs.
// The tab-less branch is not among them because no report screen in the program
// is built without tabs and its frame is a single fixed line with nothing under
// it to give.
func reportFrameStates(rows [][]string) []reportFrameState {
	return []reportFrameState{
		{label: "loading", seed: func(s *ReportTableScreen, tab int) {
			s.active = tab
			st := &s.states[tab]
			st.loading, st.loaded, st.rows, st.err = true, false, nil, ""
		}},
		{label: "failed", seed: func(s *ReportTableScreen, tab int) {
			reportSeedErr(s, tab, strings.Repeat("x", 20000))
		}, cutMark: "more of the"},
		{label: "empty", seed: func(s *ReportTableScreen, tab int) {
			reportSeed(s, tab, nil, omsapi.VarianceYardstickQuotedLeadTime)
		}},
		{label: "loaded", seed: func(s *ReportTableScreen, tab int) {
			reportSeed(s, tab, rows, omsapi.VarianceYardstickQuotedLeadTime)
		}},
	}
}
