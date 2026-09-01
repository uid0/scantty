package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// list_nav_surfaces_test.go — WHICH surfaces the movement rule is PROVEN on, and
// which are excused, derived rather than listed.
//
// The rule is one sentence: on every list surface, the bar names exactly the
// keys that will act on that surface in its current state. Two behavioural
// sweeps hold it, and each can only read the kind of bar its half of the app
// draws:
//
//   - TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves walks every type
//     embedding jdeScreen, at every width and every drawable height, and reads
//     the columnar []actionBarItem off the rendered pane.
//   - TestList_FooterNamesExactlyTheKeysThatWork walks every *ListScreen the nav
//     tree reaches, at every row count in listRowCases, and parses the footer
//     STRING through a transcription table.
//
// A third kind of surface is outside both, and this file is where that is said
// out loud rather than left to be discovered: some thirty screens write their
// bar as a muted literal straight into a strings.Builder inside View. There is
// no record to read, so there is nothing for a sweep to press keys against —
// adding them to a roster would not help, because the roster is not what is
// missing.
//
// WHAT THIS FILE GUARANTEES INSTEAD, which is the honest narrowing: the SET of
// such surfaces cannot grow in silence. Every method in the package that binds a
// navigation keystroke is classified by its receiver — swept as columnar, swept
// as a ListScreen, or recorded here WITH A REASON — and a new one that is none
// of those fails the build. So the exclusion is a derived, reviewed list rather
// than an unexamined remainder, and the next reader meets it where the code is
// rather than in a pull request nobody re-reads.
//
// WHAT IT DOES NOT GUARANTEE, stated because a claim no check delivers is worse
// than no claim: it does not say those thirty bars are honest. They are not. A
// typical one reads "j/k move · n new · E/enter edit · x delete · r refresh ·
// esc back" while binding the arrows, g/G, home/end and pgup/pgdn as well — and
// at 80 columns the pane gives a list body 51 cells, which that footer is
// already past before a single key is added to it. Naming the rest would make
// them longer and therefore LESS readable, and "a bar the operator cannot read
// is not honest, it is absent" (AGENTS.md). Closing it properly means giving
// each of those screens the folded footer and the row budget ListScreen already
// has (footerRows), which is a conversion of the same shape as sc-jde-lift and
// is why it is written down here rather than half-done in passing.
//
// THE ONE HALF THAT IS CLOSED FOR THEM is the vocabulary: they no longer bind
// anything no bar in the program spells (list_nav.go, and
// TestListNav_NoSurfaceBindsARetiredChord).

// listNavUnsweptReceivers are the receivers that bind a keystroke the navigation
// vocabulary spells and that neither behavioural sweep can read a bar for.
//
// MOSTLY, that is because the bar is prose written straight into View. Two
// entries are not, and they are here rather than filtered out because the
// derivation is a set of KEY NAMES and cannot tell a movement `g` from a `g`
// that means generate — a filter clever enough to drop one would eventually drop
// a real one, and this map is where that limit is visible instead of invisible.
//
// One entry per receiver, each saying what the surface is — not "excluded",
// which is the fact the map already carries, but what a reader would need to
// know to convert it. A stale entry fails as loudly as a missing one, so a
// screen that joins a swept class must be taken OUT of here.
var listNavUnsweptReceivers = map[string]string{
	"AssetPartsScreen":            "the parts list on an asset; footer written in View, already past 51 cells",
	"AssetProblemsScreen":         "the problem list on an asset, plus its vendor picker",
	"AuthorizationsScreen":        "the ForgeKey authorization grid",
	"LockoutsScreen":              "the ForgeKey lockout list",
	"BadgeEnrollmentScreen":       "the ForgeKey badge enrolment list",
	"CategoryListScreen":          "the category list beside CategoryFormScreen, which IS columnar and IS swept",
	"ForgeKeyCertificatesScreen":  "the ForgeKey certificate list",
	"ChecklistRunScreen":          "the step list of a checklist run",
	"ChecklistsScreen":            "the checklist browse list",
	"ThermostatListScreen":        "the thermostat list beside ClimateFormScreen, which is columnar and swept",
	"DemandForecastScreen":        "the demand-forecast table",
	"DeviceTypeListScreen":        "the device-type list beside DeviceTypeFormScreen",
	"DonationsScreen":             "the donation list",
	"EPaperPanelsScreen":          "the e-paper panel list",
	"ElectricalPanelsScreen":      "the electrical panel list",
	"PanelBreakersScreen":         "the electrical panel management list",
	"BreakerCircuitsScreen":       "the electrical circuit management list",
	"CircuitOutletsScreen":        "the electrical outlet management list",
	"CircuitDisconnectsScreen":    "the electrical disconnect management list",
	"FacilitiesScreen":            "the facilities hub, a cursor menu of surfaces",
	"FirmwareScreen":              "the firmware rollout list",
	"ForgeKeyDeviceFormScreen":    "its location picker; the form itself is columnar",
	"InventoryDetailScreen":       "the item detail sheet and its three pick modals",
	"ItemSuppliersScreen":         "the supplier list on an item beside ItemSupplierFormScreen",
	"LocationCheckinsScreen":      "the check-in list for a location",
	"LocationListScreen":          "the location list beside LocationFormScreen",
	"LocationProblemsScreen":      "the problem list for a location",
	"MaintenanceItemDetailScreen": "the PM item detail sheet",
	"MaintenanceItemsScreen":      "the PM item list beside MaintenanceItemFormScreen",
	"MakerBoxesScreen":            "the maker-box list beside MakerBoxFormScreen",
	"OperationalModesScreen":      "the ForgeKey operational-mode list",
	"PMBoardScreen":               "the preventive-maintenance board",
	"ReorderQueueScreen":          "the reorder queue",
	"ReportTableScreen":           "the shared scrollable report table (three report pages ride it)",
	"ReportsScreen":               "the reports hub, a cursor menu of surfaces",
	"SIGListScreen":               "the SIG list beside SIGFormScreen",
	"SIGMembersScreen":            "the member list of a SIG, plus its person picker",
	"SerializedComponentsScreen":  "the serialized-component list",
	"SerializedForecastScreen":    "the serialized-component consumption forecast table",
	"StorageOverviewScreen":       "the storage overview",
	"StorageSlotsScreen":          "the storage slot list beside StorageSlotFormScreen",
	"SupplierListScreen":          "the supplier list beside SupplierFormScreen",
	"TextScroller": "not a list at all: a read-only text body with a scroll offset and " +
		"no cursor, shared by fifteen detail sheets. It is here because Handle binds the " +
		"same movement keys the vocabulary spells, and its callers' footers disagree about " +
		"which of them to name — six say 'j/k scroll · pgup/pgdn page', the rest name " +
		"'j/k scroll' alone while pgup/pgdn, the arrows, g/G and home/end all work. That is " +
		"the prose-footer gap in its purest form: one handler, fifteen bars, no record to " +
		"read",
	"UsageScreen":                "the ForgeKey usage-session list",
	"VendorsScreen":              "the maintenance vendor list",
	"WebhookListScreen":          "the webhook list beside WebhookFormScreen",
	"WorkOrderAttachmentsScreen": "the attachment list on a work order",
	"WorkOrderDetailScreen":      "the work-order detail sheet and its material pickers",
	"AssetDetailScreen":          "the asset detail sheet and its certification picker",
	"LocationDetailScreen": "NOT a navigation binding: its `g` generates the location's QR " +
		"code. It is here because the vocabulary is a set of KEY NAMES and cannot tell a " +
		"movement `g` from a `g` that means generate — which is a limit of the derivation " +
		"and is recorded rather than silently filtered, since filtering it would need a " +
		"rule that also hid a real one",
	"slotCardPrompt": "a two-row modal prompt inside the storage-slot list, not a list of " +
		"rows: up/down move between a text field and a toggle and its cursor WRAPS, so the " +
		"field-form exemption applies (AGENTS.md)",
	"Root": "not a screen: app.go's root, which moves the NAV TREE cursor. The sidebar is its own surface with its own legend and is not a list of rows",
}

// listNavBindingSurfaces parses the package and returns, for every method that
// binds a navigation keystroke in a `case` clause, the receiver type it belongs
// to and where.
//
// AST rather than a grep, because the answer has to be per RECEIVER and a file
// routinely holds several: category_form.go declares CategoryFormScreen, which
// is columnar and swept, beside CategoryListScreen, which is not. A file-level
// classification would excuse the second on the strength of the first.
func listNavBindingSurfaces(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	pkg, ok := pkgs["tui"]
	if !ok {
		t.Fatal("the tui package did not parse — the derivation is broken, not the app")
	}

	out := map[string][]string{}
	for name, file := range pkg.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
				return true
			}
			recv := listNavReceiverName(fn.Recv.List[0].Type)
			if recv == "" {
				return true
			}
			ast.Inspect(fn.Body, func(inner ast.Node) bool {
				cl, ok := inner.(*ast.CaseClause)
				if !ok {
					return true
				}
				for _, expr := range cl.List {
					lit, ok := expr.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					key, err := strconv.Unquote(lit.Value)
					if err != nil || !listNavBinds(key) {
						continue
					}
					pos := fset.Position(lit.Pos())
					out[recv] = append(out[recv],
						name+":"+strconv.Itoa(pos.Line)+" "+strconv.Quote(key))
				}
				return true
			})
			return false
		})
	}
	if len(out) < 20 {
		t.Fatalf("the AST scan found only %d receivers binding a navigation key, which "+
			"is far fewer than this package has — the derivation is broken", len(out))
	}
	return out
}

func listNavReceiverName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.StarExpr:
		return listNavReceiverName(v.X)
	case *ast.Ident:
		return v.Name
	}
	return ""
}

// listNavColumnarReceivers is every type in the package that embeds jdeScreen,
// read out of the source — the same derivation jdeEmbedders makes for the pane
// sweeps, repeated here because this file must not depend on a test helper's
// fixture map to answer a question about the SOURCE.
func listNavColumnarReceivers(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	out := map[string]bool{}
	for _, file := range pkgs["tui"].Files {
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			for _, f := range st.Fields.List {
				if len(f.Names) != 0 {
					continue // named field, not an embed
				}
				if id, ok := f.Type.(*ast.Ident); ok && id.Name == "jdeScreen" {
					out[ts.Name.Name] = true
				}
			}
			return true
		})
	}
	if len(out) < 20 {
		t.Fatalf("only %d types embed jdeScreen by this scan, which contradicts the "+
			"pane sweeps — the derivation is broken", len(out))
	}
	return out
}

// TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused: every receiver in
// the package that binds a movement key is either covered by one of the two
// behavioural sweeps or recorded, with a reason, in
// listNavUnsweptReceivers.
//
// THIS IS THE COMPLETENESS HALF, and it is deliberately weaker than the rule it
// serves: it does not say the excused bars are honest, it says nothing can be
// excused by nobody having looked. A screen added tomorrow that draws a prose
// footer and binds j/k fails here until somebody writes down what it is — which
// is the difference between a known gap and an unexamined remainder, and the
// difference this area's three previous defects all turned on.
//
// ALL THREE DIRECTIONS FAIL, for the reason jdeUnsizedDeclineCases gives: a
// roster in a test is only worth keeping if being wrong about it is loud. A
// receiver that binds navigation and is neither swept nor listed fails; a listed
// receiver that IS swept fails, because it is excusing something that no longer
// needs excusing; and a listed receiver that binds no navigation key at all
// fails, because it has outlived the screen it was written about.
func TestListNav_EverySurfaceThatBindsNavigationIsSweptOrExcused(t *testing.T) {
	binding := listNavBindingSurfaces(t)
	columnar := listNavColumnarReceivers(t)

	var unclassified []string
	for recv, sites := range binding {
		switch {
		case columnar[recv]:
			if why, listed := listNavUnsweptReceivers[recv]; listed {
				t.Errorf("%s embeds jdeScreen, so the columnar movement sweep already reads "+
					"its bar — but it is recorded as a prose-footer exclusion (%q). A stale "+
					"exception excuses a surface from the sweep it passes", recv, why)
			}
		case recv == "ListScreen":
			if why, listed := listNavUnsweptReceivers[recv]; listed {
				t.Errorf("ListScreen's footer IS read structurally by "+
					"TestList_FooterNamesExactlyTheKeysThatWork, so the exclusion %q is "+
					"stale", why)
			}
		default:
			if _, listed := listNavUnsweptReceivers[recv]; !listed {
				sort.Strings(sites)
				unclassified = append(unclassified,
					recv+" — binds navigation at "+strings.Join(sites, ", "))
			}
		}
	}
	sort.Strings(unclassified)
	for _, u := range unclassified {
		t.Errorf("%s\n"+
			"This receiver binds a movement key and neither behavioural sweep can read "+
			"its bar: it does not embed jdeScreen (so the columnar sweep skips it) and "+
			"it is not a *ListScreen (so the footer sweep does not reach it). Either put "+
			"it on one of those two surfaces — which is what makes the rule PROVABLE for "+
			"it — or record it in listNavUnsweptReceivers saying what it is.", u)
	}

	for recv, why := range listNavUnsweptReceivers {
		if _, binds := binding[recv]; !binds {
			t.Errorf("listNavUnsweptReceivers excuses %s (%q) but nothing on it binds a "+
				"navigation key any more — the entry has outlived the screen it was "+
				"written about", recv, why)
		}
	}
}
