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

	"github.com/charmbracelet/lipgloss"
)

// jdeRowsMustHoldFrom is the terminal width from which the PO edit screen's
// state sweep requires every row to fit. It is also one of the two widths used
// to deduplicate the wider columnar sweep's rendered cases.
const jdeRowsMustHoldFrom = 80

// jdeRowsPastThePane is the residue the sweep found, per SCREEN: the widest
// terminal at which some row of some state of it is still drawn past the pane.
// Every width above it is clean, in every state the sweep builds, at every
// drawable height.
//
// It is the known, unfixed half of the rule, and it fails in BOTH directions — a
// screen overrunning above its recorded width is a new defect, and a screen whose
// recorded width no longer overruns is a stale entry that must come down with the
// fix that moved it — so the numbers are what the sweep MEASURES, not what anyone
// remembered.
//
// WHAT IT IS NOT: a licence below the recorded width. A ceiling says nothing
// about a new row that overruns only at widths narrower than one already does;
// that finer claim would need the whole set of cutting panes per state, which
// moves with every wording on every frame. The per-row detail — which row, what
// the operator misreads, and the layer mechanism behind each class (a hint
// appended after jdePaneFieldWidth has capped its field; a label column and a
// value floor wider than a narrow pane; prose and grids drawn unbounded) — is
// the companion item filed with this sweep, scantty-columnar-rows-past-the-pane.
var jdeRowsPastThePane = map[string]int{
	// Both asset-meter entries are BELOW jdeRowsMustHoldFrom, which is the
	// standard width this program is held to: every row of both screens fits at
	// 80 columns and above. Each is one cell, and each is the SAME layer
	// mechanism — a jdeChoice row. jdeFitRow trades a field against its hint and
	// folds what is left, but only for a jdeText row: a choice row has no input
	// area to give, so its rendered "< value >" plus any hint has to fit as
	// written. On the documents form the value is the SERVER's own category
	// label ("Manual / Documentation"), which is kept verbatim so the row reads
	// the way the web does rather than in a second vocabulary.
	"AssetDocumentsScreen":      74,
	"AssetMetersScreen":         77,
	"AssetFormScreen":           107,
	"AssetPartFormScreen":       98,
	"AuthorizationGrantScreen":  98,
	"CategoryFormScreen":        98,
	"DeviceTypeFormScreen":      89,
	"DisconnectFormScreen":      108,
	"InventoryItemFormScreen":   113,
	"ItemSupplierFormScreen":    108,
	"LocationFormScreen":        102,
	"LocationProblemFormScreen": 115,
	// The NARROWEST honest width, and one cell over it. A choice row cannot be
	// drawn in less than indent + label column + leader + jdeFieldArea's own
	// "< " / " >" + one cell of value, which is 21 against the 20 that width 49
	// gives — so the residue here is the last cell of the value on the row
	// detail, marked with the ellipsis reconChoiceValue puts there. Every other
	// row of every phase fits from 49 up.
	"LocationReconcileScreen":        49,
	"MaintenanceItemFormScreen":      120,
	"MakerBoxFormScreen":             108,
	"PowerBreakerFormScreen":         103,
	"PowerCircuitFormScreen":         109,
	"PowerOutletFormScreen":          98,
	"PowerPanelFormScreen":           101,
	"ProjectStorageFormScreen":       109,
	"PurchaseOrderAddLineScreen":     66,
	"PurchaseOrderAttachmentsScreen": 59,
	"PurchaseOrderCreateScreen":      67,
	"PurchaseOrderDetailScreen":      51,
	"PurchaseOrderEditScreen":        65,
	"SIGFormScreen":                  104,
	"ServiceStatusScreen":            95,
	"SiteSettingsFormScreen":         102,
	"StorageAssignFormScreen":        105,
	"StorageSlotFormScreen":          98,
	"StorageSlotGenerateScreen":      107,
	"SupplierFormScreen":             112,
	"ThermostatFormScreen":           103,
	"WebhookFormScreen":              103,
	// The grid's LABEL column floors at woReviewLabelFloor, so below a pane of
	// 23 cells the row is wider than the pane it was sized from — the "value
	// floor wider than a narrow pane" class, and the same one and only mechanism
	// PurchaseOrderDetailScreen's 51 is. Everything else on this screen is
	// bounded against the live pane: the empty sentence and the load error
	// through fitCellIf, the caveats through jdeCaveatLines, the readings
	// through jdeWrapTokens, and an identifier is WRAPPED rather than clipped
	// (woReviewIDLines) because a cut UUID reads as a different record.
	"WorkOrderScanReviewScreen": 51,
}

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
