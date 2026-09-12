// No row a columnar sheet draws is wider than the pane it is drawn into.
//
// clampToBox cuts a line at the pane's right edge and marks nothing, so a row
// drawn past the pane loses its TAIL silently — and on these screens the tail is
// where a hint says what a value means ("total for all 5 ordered", "blank
// clears"), where a flag says a line is voided, and where a sentence says what a
// destructive key also destroys. Until this sweep nothing checked the rows
// themselves against the pane.
// TestJDEForm_TheActionBarSurvivesEveryHeight measures the BAR's width and
// TestJDEForm_EveryEssentialHeaderRowIsOnThePane measures the ESSENTIAL header
// rows, and every other row fell between them: fourteen on the purchase-order
// edit screen alone at 80 columns and up, and several hundred across the layer
// below it.
//
// It is TestReceive_NothingOverflowsThePane's shape, lifted to every columnar
// case: the cases are DERIVED (jdePaneCases, plus every state jdeHeaderCases
// builds to reach its header's bounds), the panes are every drawable height at
// every width where the layer's floored width tells the truth
// (receiveHonestWidths — below 49 columns screenBodyWidth floors at 20 against a
// smaller pane and every columnar row overruns, which is one layer defect rather
// than a per-screen one), and a line is judged against screenBodyCells, the
// unfloored width Root really clips to.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// jdeRowsMustHoldFrom is the terminal width from which the PO edit screen's
// state sweep requires every row to fit. It is also one of the two widths used
// to deduplicate the wider columnar sweep's rendered cases.
const jdeRowsMustHoldFrom = 80

// jdeRowsPastThePane is the residue this sweep found, per SCREEN: the widest
// terminal at which some row of some state of it is still drawn past the pane.
//
// IT IS EMPTY, AND IT IS KEPT SO THE RULE CAN BE STATED WITHOUT ONE. Every
// columnar screen now fits every row it draws, in every state this sweep builds,
// at every drawable height and every honest width — so the check below is the
// rule outright rather than a rule with a schedule of exceptions. The map stays
// because the sweep fails in BOTH directions off it: an unrecorded overrun is a
// new defect, and a recorded one that no longer overruns is a stale exception,
// so an entry added here later says exactly what was given up and how far.
//
// HOW IT EMPTIED, in two rounds, and the mechanisms are worth knowing because
// each was the layer's rather than a screen's.
//
// Nine screens came off it when the SIZE CONTRACT was settled and not one of
// them was edited to do it: LocationReconcileScreen (49),
// PurchaseOrderDetailScreen (51), WorkOrderScanReviewScreen (51),
// PurchaseOrderAttachmentsScreen (59), PurchaseOrderEditScreen (65),
// PurchaseOrderAddLineScreen (66), PurchaseOrderCreateScreen (67),
// AssetDocumentsScreen (74) and AssetMetersScreen (77). Each cut a row only at
// widths Root no longer draws in. Lower minTerminalWidth and they come back;
// that is what this sweep will say, in the "not recorded" direction.
//
// The remaining twenty-six were LIVE at 80 columns — the width the whole
// interface is designed to — and as high as 120, so the floor had nothing to do
// with them. Three mechanisms, all closed in the layer:
//
//   - A HINT DRAWN PAST A CAPPED FILL. jdePaneFieldWidth caps a text row's fill
//     to the pane and renderJDEField then appends "  " + Hint after it, so the
//     hint was always past the edge. jdeFitRow trades the two and folds the
//     hint underneath, and it was a LAYOUT DECISION a sheet opted into —
//     AddFittedFields — while fifteen sheets called the unfitted AddFields or
//     renderJDEField directly. AddFields is gone and AddFittedField is the
//     per-row form those loops needed; there is nothing left to opt out of.
//     `Manual PDF path ..... ____  absolute local path` was 72 cells against 51.
//   - A VALUE OR CHOICE ROW BOUNDED NOWHERE. jdePaneFieldWidth caps only a text
//     row's fill and said a value row's width was "a content decision each sheet
//     already makes for itself"; no sheet made it. jdeFitValueRow is that half:
//     the hint folds first because folding loses nothing, then the value is
//     clipped with fitCell's mark. `Affects ..... Sending notifications,
//     reorder alerts, and receipts` was 67 cells.
//   - PROSE WRITTEN STRAIGHT OUT. A picker's note and empty state, a form's
//     standing caveat, a chain-validation message, a maintenance sub-list row —
//     each hand-counted against a width its author had in mind. They fold
//     against the LIVE pane now, and the one that was a header's ESSENTIAL row
//     (chainHeader's 85-cell validation message, which jdeOverWideEssentialRows
//     recorded as the layer's to fix) folds through addFitted, so a trim marks
//     its own cut.
//
// WHAT AN ENTRY WOULD NOT BE: a licence below its recorded width. A ceiling says
// nothing about a new row that overruns only at widths narrower than one already
// does; that finer claim would need the whole set of cutting panes per state,
// which moves with every wording on every frame.
var jdeRowsPastThePane = map[string]int{}

// jdeRowWidthCase is one (screen, state) the sweep draws. after, when set, runs
// once the screen is sized — jdeHeaderCase's rule, for a state a resize destroys.
type jdeRowWidthCase struct {
	name  string
	mk    func() Screen
	after func(Screen)
}

// at draws the case at one terminal size.
func (c jdeRowWidthCase) at(w, h int) string {
	s := jdeAtPane(c.mk(), w, h)
	if c.after != nil {
		c.after(s)
	}
	return s.View()
}

// jdeRowWidthCases is every columnar case in the state its fixture opens in,
// plus every state jdeHeaderCases builds — those exist precisely to REACH a
// header's bounds (a voided line's delete confirm, a name at the headline's
// limit), which is the state a width sweep needs most. A header case's `after`
// keypress is applied after sizing, so the state measured is the one that
// sweep measures.
func jdeRowWidthCases() []jdeRowWidthCase {
	var out []jdeRowWidthCase
	for _, c := range jdePaneCases() {
		out = append(out, jdeRowWidthCase{name: c.name, mk: c.mk})
	}
	for site, hc := range jdeHeaderCases() {
		for name, mk := range hc.states(site) {
			out = append(out, jdeRowWidthCase{name: "header " + name, mk: mk, after: hc.after})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return jdeRowWidthDistinct(out)
}

// jdeRowWidthDistinct drops a case that draws EXACTLY what an earlier one draws
// at the width that must hold and at the widest this sweep reaches. Nineteen of
// jdeHeaderCases' states are jdeScreenStates' own picker builders reused, so
// without it the sweep walks each of those twice at every pane.
func jdeRowWidthDistinct(cases []jdeRowWidthCase) []jdeRowWidthCase {
	seen := map[string]bool{}
	var out []jdeRowWidthCase
	for _, c := range cases {
		key := c.at(jdeRowsMustHoldFrom, 40) + "\x00" + c.at(120, 40)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

// jdeRowOverrun is the first line of a frame drawn past the pane, or "" when
// every line above the bar's rule fits. The bar's own lines are the layer's and
// TestJDEForm_TheActionBarSurvivesEveryHeight holds them; a refused pane draws no
// rule, and every line of its notice is judged.
func jdeRowOverrun(view string, width int) string {
	lines := strings.Split(view, "\n")
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if plain := stripANSI(lines[i]); plain != "" && strings.Trim(plain, "-") == "" {
			end = i
			break
		}
	}
	for _, line := range lines[:end] {
		if lipgloss.Width(line) > screenBodyCells(width) {
			return fmt.Sprintf("%q is %d cells against %d", stripANSI(line), lipgloss.Width(line),
				screenBodyCells(width))
		}
	}
	return ""
}

// jdeRowCaseScreen is the screen a case draws: the part of its name before the
// first "/", with the header prefix taken off.
func jdeRowCaseScreen(name string) string {
	name = strings.TrimPrefix(name, "header ")
	screen, _, _ := strings.Cut(name, "/")
	screen, _, _ = strings.Cut(screen, " ")
	return screen
}

// TestJDEForm_NoRowRunsPastThePane: at every honest width and every drawable
// height, every row of every columnar case fits the pane — except the residue
// jdeRowsPastThePane records, which it holds to its measured extent.
//
// Each case is walked from the widest terminal down and stops at the first width
// that cuts a row, or at the widest width its screen is already known to cut at:
// only the screen's widest cut is recorded, and everything above it has been
// checked at every height. A screen with no cut is walked all the way down in
// every state.
func TestJDEForm_NoRowRunsPastThePane(t *testing.T) {
	widths, heights := receiveHonestWidths(), jdePaneHeights()
	extent, first := map[string]int{}, map[string]string{}
	screens := map[string]bool{}
	for _, c := range jdeRowWidthCases() {
		screen := jdeRowCaseScreen(c.name)
		screens[screen] = true
	walk:
		for i := len(widths) - 1; i >= 0 && widths[i] > extent[screen]; i-- {
			w := widths[i]
			for _, h := range heights {
				if over := jdeRowOverrun(c.at(w, h), w); over != "" {
					extent[screen] = w
					first[screen] = fmt.Sprintf("%s at %dx%d: %s", c.name, w, h, over)
					break walk
				}
			}
		}
	}
	if len(screens) == 0 {
		t.Fatal("the sweep built no case at all, so it measured nothing")
	}

	var names []string
	for name := range screens {
		names = append(names, name)
	}
	for name := range jdeRowsPastThePane {
		if !screens[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		got, recorded := extent[name], jdeRowsPastThePane[name]
		switch {
		case !screens[name]:
			t.Errorf("jdeRowsPastThePane records %s, which no case draws any more", name)
		case got == recorded:
		case recorded == 0:
			t.Errorf("%s draws a row past the pane up to %d columns and is not recorded — "+
				"clampToBox takes the tail with no mark; %s", name, got, first[name])
		case got > recorded:
			t.Errorf("%s now draws a row past the pane up to %d columns, wider than the %d "+
				"recorded for it — a new overrun; %s", name, got, recorded, first[name])
		case got == 0:
			t.Errorf("%s is recorded as overrunning up to %d columns and no row of it "+
				"overruns at any honest width now. Take the entry out: a stale exception is "+
				"a screen excused from the check it passes", name, recorded)
		default:
			t.Errorf("%s is recorded as overrunning up to %d columns and now stops at %d. "+
				"Bring the entry down with the fix that moved it", name, recorded, got)
		}
	}
	if t.Failed() {
		var b strings.Builder
		for _, name := range names {
			if extent[name] > 0 {
				fmt.Fprintf(&b, "\t%q: %d,\n", name, extent[name])
			}
		}
		t.Logf("measured extents:\n%s", b.String())
	}
}

// TestJDEForm_AColourRowKeepsItsSampleOnThePane: a text row carrying a hex
// SAMPLE fits the pane, at every width Root draws.
//
// The sweep above cannot make this claim and passed over the defect: a swatch
// only exists once the row's value parses as a colour, and no fixture
// jdePaneCases builds carries one — which is the vacuous-fixture rule with the
// value, rather than the assertion, as the thing that could not reach the bound.
//
// THE MECHANISM IS THE HINT PAYING FOR THE SAMPLE BY ACCIDENT. renderJDEField
// draws the swatch LAST, after the fill and after any hint, and jdeColorRow
// drops the HINT whenever a sample is present — so the fill was sized against a
// budget that still had the hint's cells in it and the sample landed just past
// the pane. Measured with a 40-column field at 80 columns, where the pane is 51:
// `  Colour ..... #ff8800_____________________________  ●` is 54 cells, and
// clampToBox took the sample off the row whose whole job is to show it.
// jdeSwatchCost reserves it in jdePaneFieldWidth (the floor) and in jdeFitRow
// (the fit), so both halves of the bound agree.
func TestJDEForm_AColourRowKeepsItsSampleOnThePane(t *testing.T) {
	box := textinput.New()
	box.SetValue("#ff8800")
	f := jdeColorRow(jdeField{Label: "Colour", Kind: jdeText, Input: &box, Width: 40}, "#ff8800")
	if f.Swatch == "" {
		t.Fatal("the fixture produced no sample, so this test asserts nothing about one")
	}
	for _, w := range jdeDrawableWidths() {
		pane := screenBodyCells(w)
		fitted, _ := jdeFitRow(f, jdeLabelWidth([]jdeField{f}), pane)
		row := renderJDEField(fitted, jdeLabelWidth([]jdeField{f}), pane)
		if got := lipgloss.Width(row); got > pane {
			t.Errorf("at a terminal width of %d the colour row is %d cells against the %d the "+
				"pane gives, so clampToBox takes the sample off it: %q",
				w, got, pane, stripANSI(row))
		}
	}
}
