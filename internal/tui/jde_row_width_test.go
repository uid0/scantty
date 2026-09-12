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
//
// WHY NOTHING BELOW 80 COLUMNS IS MEASURED, AND WHY THAT IS NOT A JUDGEMENT
// CALL. Root.View() returns terminalTooSmall's notice INSTEAD of a frame
// whenever the terminal is under minTerminalWidth (80) — the refusal REPLACES
// the frame rather than being drawn beside a mutilated one, which
// TestRoot_TheRefusalIsTheWholeFrame holds — so at 49 to 79 columns no columnar
// row is drawn at all and none can be cut. receiveHonestWidths reads that gate
// rather than naming a width, so this sweep's band follows the contract if the
// floor ever moves.
//
// That matters to anyone re-reading the measurements this sweep was written
// from, which list rows overrunning from 49 columns up — the purchase-order
// review and source-chooser cart rows, the add-line confirm rows, and the detail
// grid drawing $36.00 as `$3` at 62 columns. Every one of those is MOOT rather
// than unfixed: the 80-column floor landed after they were measured, and at 62
// columns Root now draws `scantty needs 80 columns; this terminal has 62` and
// nothing else. Lower minTerminalWidth and they come back, which is what this
// sweep will then say in its "not recorded" direction.
//
// WHAT WAS DELIBERATELY LEFT OUT OF THE GRID FIT, recorded here because no code
// shows a decision not to act and this is where the set is derived. The set of
// grids bounded by jdeGridFactW / jdeGridFactCell was taken by grepping every
// padCell call in non-test code and asking what bounds each cell, rather than by
// fixing the two that were reported — so the exclusions are the residue of a
// complete pass and not an arbitrary list. Three sites are outside it on purpose:
//
//   - poGridCell (po_detail.go) and inventory_detail_kit.go fit their number
//     cells with fitCell rather than fitFactCell, so a figure past its column is
//     ellipsised instead of replaced by the cut mark. NEITHER CAN OVERRUN the
//     pane, so no row there is cut without a mark and this sweep is satisfied;
//     which mark they draw is a separate decision about the purchase-order line
//     grid's own give-order, and changing it would change what those screens
//     show.
//   - report_table.go is not this layer at all. It has its own fit
//     (fitReportTable) with its own stated give-order and its own sweep, and its
//     pane accessors are deliberately separate for that reason.
//   - po_edit.go at 80 columns and up was filed separately and is untouched
//     here.
//
// Do not read any of those as unfinished: each is a judgement that the row is
// already non-silent, and an unstated judgement is one the next reader pays to
// re-derive.
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

// jdeMoveVocabulary is every keystroke the movement tokens spell, which is the
// whole set of keys that can put a DIFFERENT row of a body on the pane.
//
// It is read off jdeMoveTokens rather than off each screen's BAR, which
// jdeMoveKeysFor does, and the difference matters for a WIDTH sweep. The bar
// sweeps ask a biconditional — a key that moves must be named and a named key
// must move — so reading the bar there is the point. This sweep asks something
// else entirely: which rows does this sheet ever DRAW? A key bound but unnamed
// still draws a row the operator can reach, so measuring only the named keys
// would make this check's reach depend on another check's subject. Pressing a
// key a screen does not bind costs one render and moves nothing.
func jdeMoveVocabulary() []string {
	seen := map[string]bool{}
	for _, keys := range jdeMoveTokens {
		for _, k := range keys {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// walked draws the case at one terminal size, at the opening position and then
// at every further position its own movement keys can reach.
//
// The keys are pressed IN SEQUENCE on one screen with no rebuild between them,
// which is jdeMoveTrace's rule for the reason AGENTS.md records: a walk that
// rebuilt the state before each key would test each key against a state no
// operator is ever in. Each key is pressed until the PLACE fingerprint repeats
// — a clamping list stops at its edge, a wrapping field cursor stops when it
// comes back round — so the walk visits every position the cursor or the offset
// can hold and then stops, rather than running to a count somebody chose.
//
// The place is read with jdePlaceOf, the same instrument the refused-pane sweep
// uses, and the bound is a backstop against a screen whose place is invisible to
// it: such a screen's fingerprint never changes, so the walk stops on the first
// press rather than spinning.
func (c jdeRowWidthCase) walked(w, h int) []string {
	s := jdeAtPane(c.mk(), w, h)
	if c.after != nil {
		c.after(s)
	}
	out := []string{s.View()}
	seen := map[string]bool{fmt.Sprint(jdePlaceOf(s)): true}
	for _, k := range jdeMoveVocabulary() {
		for i := 0; i < jdeRowWalkBound; i++ {
			next, _ := s.Update(poPickerKeyMsg(k))
			if next != nil {
				s = next
			}
			out = append(out, s.View())
			fp := fmt.Sprint(jdePlaceOf(s))
			if seen[fp] {
				break
			}
			seen[fp] = true
		}
	}
	return out
}

// jdeRowWalkBound is how many presses of ONE key the walk above will make before
// giving up on its place ever repeating. Every list and body in this package is
// far shorter than this; it exists so that a screen whose place jdePlaceOf
// cannot see, or one whose cursor is driven by something other than an int
// field, cannot turn this sweep into the hang AGENTS.md records reach loops
// becoming ("a reach loop in a test must be BOUNDED").
const jdeRowWalkBound = 400

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

// jdeWalkGainsRows reports whether walking this case's positions draws a row its
// OPENING position does not — which is the whole claim the position axis makes.
//
// Rows are compared as the text an operator reads, with styling gone and
// whitespace collapsed, so a row that merely moved up the pane or gained a
// highlight does not read as a new one. A marker row is discounted for the same
// reason: "↓ 4 more below" changing to "↓ 3 more below" is the window moving,
// not a row of the body arriving.
func jdeWalkGainsRows(c jdeRowWidthCase, w, h int) bool {
	opening := map[string]bool{}
	views := c.walked(w, h)
	for _, r := range jdeRowTexts(views[0]) {
		opening[r] = true
	}
	for _, view := range views[1:] {
		for _, r := range jdeRowTexts(view) {
			if !opening[r] {
				return true
			}
		}
	}
	return false
}

// jdeRowTexts is every row of a frame above the bar's rule as the operator reads
// it, minus the window's own more-above / more-below markers.
func jdeRowTexts(view string) []string {
	lines := strings.Split(stripANSI(view), "\n")
	end := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if plain := strings.TrimSpace(lines[i]); plain != "" && strings.Trim(plain, "-") == "" {
			end = i
			break
		}
	}
	var out []string
	for _, line := range lines[:end] {
		text := strings.Join(strings.Fields(line), " ")
		if text == "" || jdeDrawsMoreMarker(line) {
			continue
		}
		out = append(out, text)
	}
	return out
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
//
// IT HAS TWO AXES, AND THE SECOND ONE IS WHY: A ROW BELOW THE FOLD IS STILL A
// ROW THE OPERATOR READS. Until it had one this sweep rendered each case in the
// position its fixture OPENS in and nothing else, and jdeLines.Window anchors the
// body on the CURSOR's block — so every row of a body longer than the pane, and
// every row whose content the cursor decides, was outside what the sweep looked
// at. It was not wrong about what it measured; it never measured them.
//
// That is the MAJORITY of cases rather than a corner, and the sweep reports the
// live count rather than this comment carrying one that drifts: when the axis was
// added it came to 113 of 139. The purchase-order DETAIL sheet's whole line grid
// was among them — `#  Item  Qty  Cost`, and `2  Gadget  2  $24.00  [voided]`
// with the flag that says the line is struck off. That grid is the one this sweep
// reported a 51-column ceiling for while it overran to 65, which is exactly the
// shape of being blind rather than wrong: a ceiling measured off the rows it
// could see.
//
//   - THE HEIGHT AXIS draws the OPENING position at every drawable height, which
//     is what reaches every state of the PINNED HEADER: jdeFitHeader gives ground
//     by rank as the pane shortens, and a refit is the one thing on a columnar
//     frame whose text really does depend on how tall the terminal is.
//   - THE POSITION AXIS draws every position the movement keys reach, at the
//     TALLEST drawable pane. The tallest is not a preference and not a sample: a
//     body's ROWS are built from the screen's state and the pane's WIDTH, never
//     from its height — height decides only which of them the window shows — so
//     every width needs the position walk exactly once, and the tallest pane is
//     the one whose window covers the most rows per press, which is the same
//     coverage for the least work. Walking positions at every height as well was
//     measured at 500 seconds against this pair's 35 and reached no row the pair
//     does not; this package has twice hit `go test`'s 600-second per-package
//     timeout, and a sweep nobody can afford to run is a sweep that gets
//     deleted.
//
// WHAT THE PAIR DOES NOT PROVE, said plainly because a claim no check delivers is
// worse than no claim: a row whose text depends on the cursor AND on the height
// at once is measured at the tallest pane only. Nothing in the layer builds one —
// a body row's text never reads the pane's height — so the gap is in what the
// sweep ASSERTS rather than in what it found, and it is recorded here rather than
// papered over with a sentence claiming the product of the two axes.
func TestJDEForm_NoRowRunsPastThePane(t *testing.T) {
	widths, heights := receiveHonestWidths(), jdePaneHeights()
	if len(heights) == 0 {
		t.Fatal("no drawable height, so the sweep has no pane to measure")
	}
	tallest := heights[len(heights)-1]
	extent, first := map[string]int{}, map[string]string{}
	screens := map[string]bool{}
	cases := jdeRowWidthCases()
	gained := 0
	for _, c := range cases {
		screen := jdeRowCaseScreen(c.name)
		screens[screen] = true
		if jdeWalkGainsRows(c, jdeRowsMustHoldFrom, tallest) {
			gained++
		}
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
			for _, view := range c.walked(w, tallest) {
				if over := jdeRowOverrun(view, w); over != "" {
					extent[screen] = w
					first[screen] = fmt.Sprintf("%s at %dx%d, after moving: %s",
						c.name, w, tallest, over)
					break walk
				}
			}
		}
	}
	// THE POSITION AXIS IS ONLY WORTH ITS SECONDS IF IT REACHES ROWS THE OTHER
	// ONE CANNOT, and that has to be measured rather than assumed: the walk
	// presses keys through Update, so a screen that stopped binding them, a
	// jdePlaceOf that stopped seeing a renamed field, or a movement vocabulary
	// that drifted would each leave the axis pressing keys that move nothing —
	// drawing the opening pane over and over while reporting a coverage it no
	// longer has. That is the vacuity this project keeps finding inside its own
	// checks, so the axis carries its own guard. The live figure is LOGGED rather
	// than written down here, which is the same reason no count in AGENTS.md is:
	// a number in a comment drifts on the first commit that adds a case.
	if gained == 0 {
		t.Fatal("the position axis drew no row the opening position does not, at any case: " +
			"either nothing moves any more or the walk has stopped reaching it, and the axis " +
			"is measuring the panes the height axis already measured")
	}
	t.Logf("the position axis reaches rows the opening position does not on %d of %d cases",
		gained, len(cases))
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

// TestJDEForm_AGridFactColumnGrowsToItsValuesAndNoFurther pins the two layer
// primitives a detail grid's fact columns are built out of.
//
// The sweep above proves the PROPERTY — no row past the pane — over the screens.
// This pins the CONTRACT, because both functions have an edge that reads as
// redundant and is not, and a later tidy-up would take it out: jdeGridFactW
// never returns less than the budget it was handed (a ceiling set below the
// budget clamps nothing, it would otherwise SHRINK a column that was already
// agreed), and jdeGridFactCell answers a digit-bearing value past its column
// with the cut MARK rather than a truncation, because `$3` where $36.00 was
// meant reads as a real, smaller figure.
func TestJDEForm_AGridFactColumnGrowsToItsValuesAndNoFurther(t *testing.T) {
	for _, c := range []struct {
		budget, ceiling int
		values          []string
		want            int
		why             string
	}{
		{9, 13, []string{"$1.00"}, 9, "a value inside the budget leaves the column alone"},
		{9, 13, []string{"$123456.78"}, 10, "a wider value grows the column to itself"},
		{9, 13, []string{"$1.00", "$123456.78", "—"}, 10, "the WIDEST value decides"},
		{9, 13, []string{strings.Repeat("9", 40)}, 13, "past the ceiling the column stops"},
		{9, 5, []string{"$123456.78"}, 9, "a ceiling under the budget never shrinks the column"},
		{9, 13, nil, 9, "no rows leaves the budget"},
	} {
		if got := jdeGridFactW(c.budget, c.ceiling, c.values...); got != c.want {
			t.Errorf("jdeGridFactW(%d, %d, %q) = %d, want %d — %s",
				c.budget, c.ceiling, c.values, got, c.want, c.why)
		}
	}

	if got := stripANSI(jdeGridFactCell("$123456.78", 9, alignRight)); !strings.Contains(got, paneCutMark) {
		t.Errorf("a figure past its column came back as %q — a cut number reads as a "+
			"different number, so the cell must carry %q instead", got, paneCutMark)
	}
	if got := jdeGridFactCell("12345", 0, alignRight); got != "" {
		t.Errorf("a DROPPED column drew %q — fitFactCell answers a digit-bearing value "+
			"with the mark at any width and padCell cannot pad one cell back down, so a "+
			"grid that could not afford a figure would widen every row to say so", got)
	}
	for _, w := range []int{5, 9, 14} {
		if got := jdeGridFactCell("$1.00", w, alignRight); lipgloss.Width(got) != w {
			t.Errorf("a cell in a %d-cell column came to %d cells (%q): padCell pads and "+
				"never truncates, so a cell off its column width widens the whole row",
				w, lipgloss.Width(got), got)
		}
	}
}
