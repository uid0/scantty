// The columnar layer's one geometric promise, held over every screen that
// rides on it: WHAT THE LAYER ASSEMBLES FITS THE PANE.
//
// It matters because of what sits at the bottom of the frame. clampToBox drops
// lines from the BOTTOM, and the bottom of a columnar frame is the action bar —
// the only place an operator learns which keys work. A frame one row too tall
// loses the last key line; four rows too tall loses the bar, rule and all. The
// keys go on working the whole time, so nothing about the screen says the
// legend it is showing is a fragment of the real one.
//
// That is not hypothetical. jdeScreen.bodyRowsForBar used to FLOOR its budget
// at three rows, which does not create rows — it only makes the frame claim
// rows the pane does not have. Whenever screenBodyRows(H) < barRows+4 the frame
// ran over, and on the purchase-order detail at 80 columns (a four-key-line
// bar) that is every height from 14 down: one key line gone at 80x14, three at
// 80x12, the whole bar at 80x10 and below.
//
// Two things had to be true for a sweep to catch it, and this file is built
// around both:
//
//   - it has to walk the REAL screens, through Root.View(), because the clip is
//     Root's and a screen's own View() is byte-identical either side of the
//     defect. Every assertion here is made on the clipped render.
//   - it has to walk ALL of them. The set of columnar screens is DERIVED from
//     the package's own source — every type that embeds jdeScreen — and a type
//     with no entry in jdeScreenFixtures fails the sweep. A hand-kept roster is
//     the omission this project keeps paying for (AGENTS.md records three), and
//     the layer is the one place where an omission is thirty screens wide.
package tui

import (
	"fmt"
	"go/ast"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/uid0/scantty/internal/omsapi"
)

// jdePaneWidths are the widths the columnar screens are checked at. 80 is the
// one that bites — it is the width the interface is modelled on, and the width
// at which a twelve-key bar folds onto four lines — and the other two are here
// so a fix cannot be tuned to 80.
var jdePaneWidths = []int{80, 100, 120}

// jdePaneHeights is every height Root will draw a screen at.
//
// The floor is Root.View's own gate: it refuses below a content height of 5,
// which is a terminal height of 7, so 7 is the shortest terminal this project
// supports and the shortest one the layer has to have an answer for. Deriving
// it rather than writing 7 is the point — if that gate moves, this sweep moves
// with it instead of leaving the new heights untested.
func jdePaneHeights() []int {
	var out []int
	for h := 1; h <= 40; h++ {
		if !jdeRootDraws(h) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// jdeRootDraws reports whether Root.View() renders a screen at all at this
// height, rather than its own "terminal too short" line. Asked of Root instead
// of restated here.
func jdeRootDraws(height int) bool {
	r := newTestRoot(NewServiceStatusScreen(Deps{}))
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: height})
	return !strings.Contains(next.(Root).View(), "terminal too short")
}

// ---------------------------------------------------------------------------
// The set of screens, derived
// ---------------------------------------------------------------------------

// jdeEmbedders returns every type in the package's non-test source that embeds
// jdeScreen — which is exactly the set of screens the layer draws, because
// embedding it is how a screen reaches the frames at all.
func jdeEmbedders(t *testing.T) map[string]bool {
	t.Helper()
	_, files := jdeParsePackage(t)
	out := map[string]bool{}
	for path, f := range files {
		if jdeIsLayer(path) {
			continue // jdeScreen's own file declares it; it does not embed it
		}
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok || st.Fields == nil {
					continue
				}
				for _, field := range st.Fields.List {
					if len(field.Names) > 0 {
						continue // named field, not an embedding
					}
					if id, ok := field.Type.(*ast.Ident); ok && id.Name == "jdeScreen" {
						out[ts.Name.Name] = true
					}
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no type in this package embeds jdeScreen, so every sweep in this " +
			"file would pass vacuously. If embedding stopped being how a screen " +
			"reaches the columnar frames, this derivation needs rewriting rather " +
			"than deleting")
	}
	return out
}

// jdeScreenFixtures builds one of every columnar screen, in a state that
// actually reaches a frame.
//
// The MAP is written by hand and that is fine; what may not be written by hand
// is the SET OF KEYS, which jdeEmbedders derives and
// TestJDEForm_EveryColumnarScreenIsSwept compares against. Adding a screen and
// forgetting this map fails the build; deleting or renaming one and leaving a
// stale entry fails it too.
//
// Every fixture is put PAST its loading state, because a screen still fetching
// draws "Loading…" and no frame at all — an entry that renders no bar is an
// entry that proves nothing, which is what TestJDEForm_EveryColumnarScreenIsSwept's
// second half exists to catch.
func jdeScreenFixtures() map[string]func() Screen {
	return map[string]func() Screen{
		"AssetFormScreen":                func() Screen { s := NewAssetFormScreen(Deps{}, ""); s.loading = false; return s },
		"AssetPartFormScreen":            func() Screen { s := NewAssetPartFormScreen(Deps{}, "a1", "Asset", ""); s.loading = false; return s },
		"AuthorizationGrantScreen":       func() Screen { s := NewAuthorizationGrantScreen(Deps{}); s.loading = false; return s },
		"CategoryFormScreen":             func() Screen { s := NewCategoryFormScreen(Deps{}, ""); s.loading = false; return s },
		"DeviceTypeFormScreen":           func() Screen { return NewDeviceTypeFormScreen(Deps{}, 0) },
		"DisconnectFormScreen":           func() Screen { s := NewDisconnectFormScreen(Deps{}, 0, 0, 0); s.loading = false; return s },
		"InventoryItemFormScreen":        func() Screen { s := NewInventoryItemFormScreen(Deps{}, ""); s.loading = false; return s },
		"ItemSupplierFormScreen":         func() Screen { s := NewItemSupplierFormScreen(Deps{}, "i1", "Item", nil); s.loading = false; return s },
		"LocationFormScreen":             func() Screen { s := NewLocationFormScreen(Deps{}, ""); s.loading = false; return s },
		"LocationProblemFormScreen":      func() Screen { return NewLocationProblemFormScreen(Deps{}, 1, "Loc") },
		"MaintenanceItemFormScreen":      func() Screen { s := NewMaintenanceItemFormScreen(Deps{}, ""); s.loading = false; return s },
		"MakerBoxFormScreen":             func() Screen { return NewMakerBoxFormScreen(Deps{}, 0) },
		"PowerBreakerFormScreen":         func() Screen { s := NewPowerBreakerFormScreen(Deps{}, 0, 0); s.loading = false; return s },
		"PowerCircuitFormScreen":         func() Screen { s := NewPowerCircuitFormScreen(Deps{}, 0, 0, 0); s.loading = false; return s },
		"PowerOutletFormScreen":          func() Screen { s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0); s.loading = false; return s },
		"PowerPanelFormScreen":           func() Screen { s := NewPowerPanelFormScreen(Deps{}, 0); s.loading = false; return s },
		"ProjectStorageFormScreen":       func() Screen { return NewProjectStorageFormScreen(Deps{}) },
		"PurchaseOrderAddLineScreen":     func() Screen { return NewPurchaseOrderAddLineScreen(Deps{}, poViewPO()) },
		"PurchaseOrderAttachmentsScreen": func() Screen { return NewPurchaseOrderAttachmentsScreen(Deps{}, poViewPO()) },
		"PurchaseOrderDetailScreen": func() Screen {
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			return s
		},
		"PurchaseOrderEditScreen":   func() Screen { return NewPurchaseOrderEditScreen(Deps{}, poViewPO()) },
		"ReceiveFormScreen":         func() Screen { return NewReceiveFormScreen(Deps{}, poViewPO()) },
		"SIGFormScreen":             func() Screen { return NewSIGFormScreen(Deps{}, "") },
		"ServiceStatusScreen":       func() Screen { return NewServiceStatusScreen(Deps{}) },
		"SiteSettingsFormScreen":    func() Screen { s := NewSiteSettingsFormScreen(Deps{}); s.loading = false; return s },
		"StorageAssignFormScreen":   func() Screen { return NewStorageAssignFormScreen(Deps{}, "R1-S1", nil) },
		"StorageSlotFormScreen":     func() Screen { return NewStorageSlotFormScreen(Deps{}, "") },
		"StorageSlotGenerateScreen": func() Screen { return NewStorageSlotGenerateScreen(Deps{}, 0) },
		"SupplierFormScreen":        func() Screen { return NewSupplierFormScreen(Deps{}, "") },
		"ThermostatFormScreen":      func() Screen { s := NewThermostatFormScreen(Deps{}, ""); s.loading = false; return s },
		"WebhookFormScreen":         func() Screen { return NewWebhookFormScreen(Deps{}, 0) },
	}
}

// jdeScreenStates are EXTRA states of screens jdeScreenFixtures already builds,
// and they are the second axis of this file: DERIVING the set of screens makes
// a screen impossible to forget and says nothing whatever about the states
// inside one.
//
// The layer's geometry only bites where a screen has a BODY long enough to
// overflow its window and a bar that changes shape around it, and a freshly
// built form has neither — its body fits, so jdeLines.Scrolls is false at every
// height and its bar never names a scroll key. So the fixtures above sweep
// thirty screens over the one state where the interesting arithmetic is inert.
// That is not hypothetical either: the refusal notice named a height at which
// the screen was still refused, it was measured on the purchase-order detail's
// ORDER PAD, and the sweep written to catch it passed — no fixture reached that
// state.
//
// The KEYS are `<fixture name>/<state>` and the prefix is checked against
// jdeScreenFixtures by TestJDEForm_EveryColumnarScreenIsSwept, so a state
// naming a screen that has been renamed away fails rather than quietly
// sweeping nothing.
//
// The PICK-LIST states are not optional and are not a hand-picked selection:
// every `<Type>/<method>` that builds a jdePickList in the package's own source
// must appear here, and TestJDEForm_EveryPickListSiteIsSwept derives that
// roster and fails on an omission. One picker in the sweep would have caught
// the filter-box defect and nineteen prove it is closed everywhere, which is
// the difference between fixing a rule at the site that was reported and
// applying it.
func jdeScreenStates() map[string]func() Screen {
	return map[string]func() Screen{
		// A long export over a three-line pinned header: the state the notice
		// defect was measured in. The bar names PgUp/PgDn exactly while the pad
		// overflows, which is what makes its height vary with the pane.
		"PurchaseOrderDetailScreen/order pad": func() Screen {
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			var rows []string
			for i := 0; i < 40; i++ {
				rows = append(rows, fmt.Sprintf("PART-%04d\t%d", i, i+1))
			}
			s.orderPad = true
			s.orderPadExport = &omsapi.OrderPadExport{
				Text: strings.Join(rows, "\n"), Supplier: "Acme Fasteners & Industrial Supply",
				Filename: "PO-2026-0042-order.csv", LineCount: len(rows),
				MissingSku: []string{"Widget clamp", "Gear housing", "Bearing race"},
			}
			return s
		},

		"AssetFormScreen/pickView": func() Screen {
			s := NewAssetFormScreen(Deps{}, "")
			s.loading = false
			s.categories = []omsapi.Category{{ID: 1, Name: "Bolts"}, {ID: 2, Name: "Bolt washers"}}
			s.openPicker(afCategory)
			return s
		},
		"AssetPartFormScreen/pickView": func() Screen {
			s := NewAssetPartFormScreen(Deps{}, "a1", "Asset", "")
			s.loading = false
			s.items = []omsapi.Item{{ID: "item-1", Name: "Drive belt", SKU: "B-1"}, {ID: "item-2", Name: "Air filter"}}
			s.openPicker()
			return s
		},
		"AuthorizationGrantScreen/pickView": func() Screen {
			s := NewAuthorizationGrantScreen(Deps{})
			s.loading = false
			s.assets = []omsapi.Asset{{ID: "a-1", Name: "Laser cutter"}, {ID: "a-2", Name: "Lathe"}}
			s.openPicker(agAsset)
			return s
		},
		"CategoryFormScreen/pickView": func() Screen {
			s := NewCategoryFormScreen(Deps{}, "")
			s.loading = false
			s.categories = []omsapi.Category{{ID: 1, Name: "Hardware"}, {ID: 2, Name: "Consumables"}}
			s.openParentPicker()
			return s
		},
		"DisconnectFormScreen/pickView": func() Screen {
			s := NewDisconnectFormScreen(Deps{}, 0, 0, 0)
			s.loading = false
			s.lotoDevices = []omsapi.LOTODevice{
				{ID: 7, DeviceType: "breaker_lock", DeviceTypeDisplay: "Breaker lock", Label: "BL-1", Status: "available"},
				{ID: 9, DeviceType: "padlock", DeviceTypeDisplay: "Padlock", Label: "PAD-2", Status: "available"},
			}
			s.openPicker(dcLOTODevices)
			return s
		},
		"InventoryItemFormScreen/pickView": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.categories = []omsapi.Category{{ID: 1, Name: "Bolts"}, {ID: 2, Name: "Bolt washers"}}
			s.openPicker(fCategory)
			return s
		},
		"InventoryItemFormScreen/kitPickView": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.kitItems = []omsapi.Item{{ID: "i-1", Name: "Drive belt", SKU: "B-1"}, {ID: "i-2", Name: "Air filter"}}
			s.openKitPick()
			return s
		},
		"ItemSupplierFormScreen/pickView": func() Screen {
			s := NewItemSupplierFormScreen(Deps{}, "i1", "Item", nil)
			s.loading = false
			s.suppliers = []omsapi.Supplier{{ID: 4, Name: "Acme"}, {ID: 5, Name: "Beta"}}
			s.openPicker()
			return s
		},
		"LocationFormScreen/pickView": func() Screen {
			s := NewLocationFormScreen(Deps{}, "")
			s.loading = false
			s.locations = []omsapi.Location{{ID: 1, Name: "Hall"}, {ID: 2, Name: "Shop"}}
			s.openParentPicker()
			return s
		},
		"MaintenanceItemFormScreen/pickView": func() Screen {
			s := NewMaintenanceItemFormScreen(Deps{}, "")
			s.loading = false
			s.assets = []omsapi.Asset{{ID: "a1", Name: "Lathe", AssetTag: "LT-1"}, {ID: "a2", Name: "Mill"}}
			s.openAssetPick()
			return s
		},
		"PowerBreakerFormScreen/pickView": func() Screen {
			s := NewPowerBreakerFormScreen(Deps{}, 0, 0)
			s.loading = false
			s.panels = []omsapi.PowerPanel{{ID: 1, Name: "P1", LocationName: "Shop", PhaseConfiguration: "split"}}
			s.openPicker()
			return s
		},
		"PowerCircuitFormScreen/pickView": func() Screen {
			s := NewPowerCircuitFormScreen(Deps{}, 0, 0, 0)
			s.loading = false
			s.breakers = []omsapi.PowerBreakerDetail{
				{ID: 3, Position: "12", Amperage: 20, PoleCount: 1, Label: "north wall"},
				{ID: 4, Position: "14", Amperage: 30, PoleCount: 2},
			}
			s.openPicker()
			return s
		},
		"PowerOutletFormScreen/pickView": func() Screen {
			s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0)
			s.loading = false
			s.disconnects = []omsapi.DisconnectDetail{{ID: 8, Label: "d", DisconnectType: "fused"}}
			s.openPicker(poDisconnect)
			return s
		},
		"PowerPanelFormScreen/pickView": func() Screen {
			s := NewPowerPanelFormScreen(Deps{}, 0)
			s.loading = false
			s.locations = []omsapi.Location{{ID: 1, Name: "Hall"}, {ID: 2, Name: "Shop"}}
			s.openPicker(ppLocation)
			return s
		},
		"ProjectStorageFormScreen/pickView": func() Screen {
			s := NewProjectStorageFormScreen(Deps{})
			next, _ := s.Update(projectStorageSlotsLoadedMsg{slots: freeSlots()})
			ps := next.(*ProjectStorageFormScreen)
			ps.openSlotPick()
			return ps
		},
		"StorageAssignFormScreen/pickView": func() Screen {
			s := NewStorageAssignFormScreen(Deps{}, "R1-S1", nil)
			s.sigs = []omsapi.SIG{{ID: 3, Name: "Woodshop"}, {ID: 4, Name: "Metal"}}
			s.openPicker()
			return s
		},
		"StorageSlotFormScreen/pickView": func() Screen {
			s := NewStorageSlotFormScreen(Deps{}, "")
			next, _ := s.Update(storageSlotFormLoadedMsg{sigs: []omsapi.SIG{{ID: 3, Name: "Woodshop"}, {ID: 4, Name: "Metal"}}})
			sf := next.(*StorageSlotFormScreen)
			sf.openPicker()
			return sf
		},
		"StorageSlotGenerateScreen/pickView": func() Screen {
			s := NewStorageSlotGenerateScreen(Deps{}, 0)
			s.sigs = []omsapi.SIG{{ID: 3, Name: "Woodshop"}, {ID: 4, Name: "Metal"}}
			s.openPicker()
			return s
		},
		"ThermostatFormScreen/pickView": func() Screen {
			s := NewThermostatFormScreen(Deps{}, "")
			s.loading = false
			s.locations = []omsapi.Location{{ID: 1, Name: "Hall"}, {ID: 2, Name: "Shop"}}
			s.openPicker(tfLocation)
			return s
		},
	}
}

// jdePaneCases is every (screen, state) pair the sweeps in this file walk:
// jdeScreenFixtures plus jdeScreenStates. Sorted, so a failure names the same
// case run to run.
func jdePaneCases() []struct {
	name string
	mk   func() Screen
} {
	var out []struct {
		name string
		mk   func() Screen
	}
	add := func(m map[string]func() Screen) {
		for name, mk := range m {
			out = append(out, struct {
				name string
				mk   func() Screen
			}{name, mk})
		}
	}
	add(jdeScreenFixtures())
	add(jdeScreenStates())
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// TestJDEForm_EveryColumnarScreenIsSwept: the fixtures and the screens agree,
// and every fixture really draws a frame.
//
// Both halves earn their keep. Without the first, a screen added to the app
// tomorrow is simply absent from every sweep in this file and passes by not
// being looked at — which is verbatim how `N` on the purchasing list survived
// poAllBarKeys. Without the second, a fixture left in its loading state renders
// one line of "Loading…", fits every pane trivially, and reports coverage of a
// frame it never built.
func TestJDEForm_EveryColumnarScreenIsSwept(t *testing.T) {
	want := jdeEmbedders(t)
	got := jdeScreenFixtures()
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("%s embeds jdeScreen and has no fixture, so no sweep in this file "+
				"ever draws it. Add one to jdeScreenFixtures in the state the operator "+
				"reaches it in — the layer's geometry is only checked over the screens "+
				"that are built", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("jdeScreenFixtures builds %s, which no longer embeds jdeScreen. "+
				"A stale fixture is a sweep spending its time on a screen the layer "+
				"does not draw, while the one that replaced it goes unchecked", name)
		}
	}
	for name := range jdeScreenStates() {
		screen, _, ok := strings.Cut(name, "/")
		if !ok {
			t.Errorf("the extra state %q is not named <fixture>/<state>, so nothing "+
				"checks that it still belongs to a screen the layer draws", name)
			continue
		}
		if _, ok := got[screen]; !ok {
			t.Errorf("jdeScreenStates builds the state %q of %s, which jdeScreenFixtures "+
				"no longer builds. A state whose screen has been renamed away is a sweep "+
				"spending its time on nothing", name, screen)
		}
	}
	for _, c := range jdePaneCases() {
		s := c.mk()
		jdeRootAt(t, s, 80, 40)
		if jdeBarOf(s.View()) == nil {
			t.Errorf("the %s fixture draws no action bar at 80x40, so every assertion "+
				"this file makes about it is vacuous:\n%s", c.name, s.View())
		}
	}
}

// ---------------------------------------------------------------------------
// The promise
// ---------------------------------------------------------------------------

// jdeRootAt puts a screen in a Root of this size and returns it. The Root is
// what makes a render CLIPPED, which is the only render worth asserting on.
func jdeRootAt(t *testing.T, s Screen, w, h int) Root {
	t.Helper()
	r := newTestRoot(s)
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after
}

// jdeBarOf finds the action bar in a rendered frame: the last rule line and
// every key line under it. Nil when the frame draws no bar at all.
//
// The rule is the anchor because it is the one line of the bar with a shape
// nothing else has — renderActionBar and renderActionBarWrapped both open with
// a run of hyphens the width of the pane.
func jdeBarOf(view string) []string {
	lines := strings.Split(view, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimRight(lines[i], " ")
		if len(trimmed) >= 8 && strings.Trim(trimmed, "-") == "" {
			return lines[i:]
		}
	}
	return nil
}

// TestJDEForm_NoColumnarScreenOverflowsThePane: what the layer assembles is
// never taller than the pane it will be clipped into.
//
// This is the structural half, and it is the one that would have caught the
// floor on the day it was written. A frame of the right height cannot lose its
// bar, whatever the bar happens to be carrying; a frame one row too tall loses
// it silently and looks perfectly ordinary in a diff.
func TestJDEForm_NoColumnarScreenOverflowsThePane(t *testing.T) {
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				lines := strings.Split(s.View(), "\n")
				if pane := screenBodyRows(h); len(lines) > pane {
					t.Errorf("%s at %dx%d renders %d rows into a pane of %d — clampToBox "+
						"drops the last %d from the BOTTOM, which is where the action bar "+
						"is:\n%s", name, w, h, len(lines), pane, len(lines)-pane, s.View())
				}
			}
		}
	}
}

// TestJDEForm_TheActionBarSurvivesEveryHeight: whenever a columnar screen draws
// a bar, every ROW of it is still on the pane after Root has clipped the frame.
//
// The complement of the structural check, and the one stated the way an
// operator would state it. It is asserted on Root.View() and not on the
// screen's own output for the reason po_view_jde_test.go's whole header
// explains: the screen's string is identical either side of this defect.
//
// It is about the VERTICAL clip only, and says so by comparing each bar line
// against the same horizontal truncation the pane applies. That is not a
// loophole, it is the honest boundary of this change: renderActionBar — the
// ONE-line bar the non-wrapping frames draw — tightens its gutter and then
// lets the line run past the pane, and eleven form screens name enough keys at
// 80 columns to reach that (MaintenanceItemFormScreen's bar is 63 cells against
// a pane of 51). That is a WIDTH defect, it predates this change and this
// change reduces rather than causes it — 56 (screen, height) pairs before, 44
// after, the same eleven screens — and its fix is to give those frames the
// WRAPPING bar, which means giving bodyAvail and bodyScrolls the items they
// currently do not take, on some thirty sheets. It is recorded and routed, not
// smuggled in here.
func TestJDEForm_TheActionBarSurvivesEveryHeight(t *testing.T) {
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				r := jdeRootAt(t, s, w, h)
				bar := jdeBarOf(s.View())
				if bar == nil {
					continue // a frame the layer refused to draw; the sweep below owns it
				}
				shown := r.View()
				for _, line := range bar {
					line = truncateVisible(strings.TrimRight(line, " "), screenBodyWidth(w))
					if line == "" {
						continue
					}
					if !strings.Contains(shown, line) {
						t.Errorf("%s at %dx%d: the bar row %q is cut off the BOTTOM of the "+
							"pane, so the keys on it are unnamed while they go on "+
							"working:\n%s", name, w, h, line, shown)
					}
				}
			}
		}
	}
}

// TestJDEForm_AScreenThatCannotDrawItsBarNamesNoKeys: below the height where
// the bar fits, the layer draws its notice and nothing else.
//
// This is the other half of the acceptance the whole change is for: at every
// supported height EITHER the bar is fully readable OR the screen names no keys
// at all. A frame that quietly kept drawing a body while losing the bar would
// satisfy the first sweep in this file (it is not too tall) and the second (it
// draws no bar, so there is nothing to find cut) and still be the defect. This
// is what closes that gap: where no bar is drawn, the notice must be.
func TestJDEForm_AScreenThatCannotDrawItsBarNamesNoKeys(t *testing.T) {
	refused := 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				view := s.View()
				if jdeBarOf(view) != nil {
					continue
				}
				refused++
				if !strings.Contains(view, "Too short") {
					t.Errorf("%s at %dx%d draws no action bar and does not say why. A pane "+
						"with no legend on it and no explanation is a screen the operator "+
						"cannot tell from a wedged one:\n%s", name, w, h, view)
				}
			}
		}
	}
	if refused == 0 {
		t.Error("no screen at any supported size refused to draw its bar, so this sweep " +
			"asserted nothing. The shortest supported terminal is 7 rows, which leaves " +
			"the pane one; if the layer now fits a bar into that, this test needs " +
			"rewriting rather than deleting")
	}
}

// TestJDEForm_AFrameThatIsDrawnShowsSomething: wherever the layer does draw a
// frame, that frame carries at least one row of the screen's own content.
//
// The bar being intact is not the whole of the promise. A frame of blank rows
// under an honest legend is still a pane where no keypress changes anything —
// the cursor moves through rows that are not on it, typing goes into a box that
// is not on it, and every redraw is byte-identical. That is this project's
// oldest report arriving by geometry, and it is what the pinned header caused
// the moment the budget stopped being flooded: the receiving form's header is a
// note row plus a separator, so at 80x10 and 80x11 it took the whole budget and
// the frame was two blank rows over a status row and a bar.
//
// So the layer gives the BODY its row and trims the HEADER to pay for it
// (jdeBodyAvail / jdeFitHeader), and refuses the frame outright when it cannot
// pay for both. This is that promise asserted over every screen rather than
// over the one the report came from — applying it only where it was reported is
// how the receiving conversion fixed one of its three bodies and left the other
// two stranded for a round.
//
// "Content" is measured as a non-blank row that is neither the action bar nor
// the status row directly above it, which is exactly what the frame is built
// from: header, body, status, bar.
func TestJDEForm_AFrameThatIsDrawnShowsSomething(t *testing.T) {
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				view := s.View()
				bar := jdeBarOf(view)
				if bar == nil {
					continue // refused; the sweep above owns it
				}
				lines := strings.Split(view, "\n")
				content := lines[:len(lines)-len(bar)]
				if len(content) > 0 {
					content = content[:len(content)-1] // the status row
				}
				found := false
				for _, line := range content {
					if strings.TrimSpace(line) != "" {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("%s at %dx%d draws its bar over %d blank rows — every key on that "+
						"bar acts on something the operator cannot see, and every redraw is "+
						"byte-identical:\n%s", name, w, h, len(content), view)
				}
			}
		}
	}
}

// TestJDEForm_TheNoticeFitsThePaneItReplaces: the too-short notice is itself
// bounded, in both axes, by the layer.
//
// A notice cut by clampToBox would be the defect it exists to report, and the
// first line is the one carrying the fact — so a notice that overflows loses
// the sentence saying the keys still work and keeps the one saying nothing.
func TestJDEForm_TheNoticeFitsThePaneItReplaces(t *testing.T) {
	for _, w := range jdePaneWidths {
		for _, h := range jdePaneHeights() {
			for _, barRows := range []int{2, 3, 4, 5, 6} {
				g := jdeScreen{terminalWidth: w, terminalHeight: h}
				for _, headerRows := range []int{0, 1, 3} {
					if !g.tooShort(barRows, headerRows) {
						continue
					}
					notice := g.tooShortNotice(barRows, headerRows)
					lines := strings.Split(notice, "\n")
					if len(lines) > screenBodyRows(h) {
						t.Errorf("at %dx%d the notice for a %d-row bar is %d lines in a pane "+
							"of %d:\n%s", w, h, barRows, len(lines), screenBodyRows(h), notice)
					}
					for _, line := range lines {
						if got := lipgloss.Width(line); got > screenBodyWidth(w) {
							t.Errorf("at %dx%d the notice line %q is %d cells wide in a pane "+
								"of %d", w, h, line, got, screenBodyWidth(w))
						}
					}
					if !strings.Contains(lines[0], fmt.Sprintf("has %d", h)) {
						t.Errorf("at %dx%d the notice's first line does not say what height the "+
							"terminal has, and the first line is all a one-row pane keeps: %q",
							w, h, lines[0])
					}
				}
			}
		}
	}
}

// jdeNoticeNeeds reads the required TERMINAL height back out of a rendered
// too-short notice.
//
// It is read off the PANE rather than recomputed from the layer on purpose: the
// number the operator can act on is the number that was drawn, and a check that
// asked jdeTooShortRows for it would agree with the layer by construction —
// including when the layer is wrong. The notice is this screen's one
// operator-facing contract at a refused height, so its text is the interface
// being read.
//
// The view is flattened first because the sentence goes through jdeWrapNote,
// which may fold it at a narrow pane.
func jdeNoticeNeeds(view string) (int, bool) {
	m := jdeNoticeNeedsRe.FindStringSubmatch(strings.Join(strings.Fields(view), " "))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

var jdeNoticeNeedsRe = regexp.MustCompile(`needs (\d+) rows`)

// TestJDEForm_TheHeightTheNoticeNamesActuallyWorks: resize to the height the
// refusal names and the screen draws its bar.
//
// A notice exists to be ACTED ON, so the one actionable fact it carries has to
// be a height that WORKS. This is the empirical half of the argument written
// out in jdeTooShortRows' doc comment — that argument says the number cannot be
// an under-estimate, and this walks every screen at every supported size to see
// whether it is.
//
// It caught a real one. While a REFUSED pane answered the body an avail of 0,
// jdeLines.Scrolls went false at exactly the refused heights, the sheet dropped
// the scroll keys, the bar it handed the layer lost a row, and the number came
// out one row short: the operator resized to precisely what the screen asked
// for and was refused again, with a number one larger. A screen with genuinely
// no fixed point would fail here too, which is the other thing this is for.
func TestJDEForm_TheHeightTheNoticeNamesActuallyWorks(t *testing.T) {
	checked := 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				view := s.View()
				if jdeBarOf(view) != nil {
					continue // drawn; there is no advice to act on
				}
				need, ok := jdeNoticeNeeds(view)
				if !ok {
					t.Errorf("%s at %dx%d draws no bar and no height the operator could "+
						"resize to — being stuck is all they learn:\n%s", name, w, h, view)
					continue
				}
				checked++
				grown := mk()
				jdeRootAt(t, grown, w, need)
				if jdeBarOf(grown.View()) == nil {
					t.Errorf("%s at %dx%d tells the operator to resize to %d rows, and at "+
						"%dx%d it is REFUSED again — they did exactly what the screen "+
						"asked and got the same blank pane:\n%s",
						name, w, h, need, w, need, grown.View())
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no screen at any supported size drew a refusal notice, so this sweep " +
			"asserted nothing. If the layer now fits every bar into every supported " +
			"pane, this test needs rewriting rather than deleting")
	}
}

// ---------------------------------------------------------------------------
// The picker's filter box
// ---------------------------------------------------------------------------

// jdePickSites is every place in the package's own non-test source that builds
// a jdePickList — keyed `<receiver type>/<method>`, which is exactly the key
// shape jdeScreenStates uses.
//
// It is DERIVED for the reason every roster in this project is: a picker added
// tomorrow and left out of a hand-kept list is a picker no sweep ever draws,
// and it fails silently. There are nineteen of them across seventeen files, so
// a list would have been wrong within a round.
func jdePickSites(t *testing.T) map[string]bool {
	t.Helper()
	_, files := jdeParsePackage(t)
	out := map[string]bool{}
	for path, f := range files {
		if jdeIsLayer(path) {
			continue // the layer DECLARES jdePickList; it builds none
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Body == nil {
				continue
			}
			recv := ""
			switch e := fn.Recv.List[0].Type.(type) {
			case *ast.StarExpr:
				if id, ok := e.X.(*ast.Ident); ok {
					recv = id.Name
				}
			case *ast.Ident:
				recv = e.Name
			}
			if recv == "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				if id, ok := lit.Type.(*ast.Ident); ok && id.Name == "jdePickList" {
					out[recv+"/"+fn.Name.Name] = true
				}
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no function in this package builds a jdePickList, so the filter-box " +
			"sweep below would pass vacuously. If pickers stopped going through " +
			"jdePickList this derivation needs rewriting rather than deleting")
	}
	return out
}

// TestJDEForm_EveryPickListSiteIsSwept: every picker in the app is one of the
// states the sweeps here walk.
//
// The same rule as TestJDEForm_EveryColumnarScreenIsSwept, one axis in: that
// one makes a SCREEN impossible to forget and says nothing about the states
// inside one, and a picker is a state. The filter-box defect lived in the one
// shared jdePickList.render, so it was on all nineteen pickers at once and
// visible on none of them — jdeScreenFixtures builds every screen in its base
// form and never opens a picker at all.
func TestJDEForm_EveryPickListSiteIsSwept(t *testing.T) {
	states := jdeScreenStates()
	for site := range jdePickSites(t) {
		if _, ok := states[site]; !ok {
			t.Errorf("%s builds a jdePickList and has no state in jdeScreenStates, so no "+
				"sweep in this file ever draws that picker. Add one in the state the "+
				"operator reaches it in — a rule applied to the pickers somebody "+
				"thought of is the omission this project keeps paying for", site)
		}
	}
}

// jdePickFilterLabel is the label renderJDEField draws on the picker's filter
// row. It leads the row, so it survives any horizontal clip the pane applies
// and finding it is finding the box.
const jdePickFilterLabel = "Filter"

// jdePickFilterRow returns the frame's filter row, or "" when the frame does
// not carry one at all.
func jdePickFilterRow(view string) string {
	for _, line := range strings.Split(view, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), jdePickFilterLabel) {
			return line
		}
	}
	return ""
}

// TestJDEForm_ThePickFilterBoxIsOnEveryPane: the box the operator types into is
// drawn at every supported size.
//
// A picker's filter is ALWAYS LIVE — it holds the caret for as long as the
// picker is open, and typing is the only thing that narrows the list — so a
// frame that does not draw it is a frame where every keystroke goes somewhere
// invisible. The list under it is windowed on the CURSOR, which typing does not
// move, so the pane comes back byte for byte: rule 1 by geometry, which is
// this project's oldest report.
//
// It reached the pane through the HEADER. jdeFitHeader trims a pinned header
// from the END, and the header used to open with a decorative title, so at the
// minimum drawable budget the one row that survived was the title. Measured on
// InventoryItemFormScreen's category picker at 80x11: pane 5, a two-row bar,
// budget 2, the body floor takes one, keep is 1 — "Category" on the pane and
// the box nowhere. jdePickList.render draws the filter FIRST now, which is
// receive_form.go's serialBody rule for a block no key can move.
//
// Asserted on the CLIPPED Root.View(), and in two steps because the row can be
// lost two different ways: the layer can trim it out of the frame, or the pane
// can cut it off the bottom.
func TestJDEForm_ThePickFilterBoxIsOnEveryPane(t *testing.T) {
	sites := jdePickSites(t)
	checked := 0
	for _, c := range jdePaneCases() {
		if !sites[c.name] {
			continue
		}
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := c.mk()
				r := jdeRootAt(t, s, w, h)
				view := s.View()
				if jdeBarOf(view) == nil {
					continue // a frame the layer refused; it draws no rows at all
				}
				checked++
				row := jdePickFilterRow(view)
				if row == "" {
					t.Errorf("%s at %dx%d draws a frame with no filter row in it — the box "+
						"the operator types into was trimmed off the pinned header, so "+
						"every keystroke goes somewhere they cannot see:\n%s",
						c.name, w, h, r.View())
					continue
				}
				clipped := truncateVisible(strings.TrimRight(row, " "), screenBodyWidth(w))
				if clipped != "" && !strings.Contains(r.View(), clipped) {
					t.Errorf("%s at %dx%d: the filter row %q is cut off the pane, so the box "+
						"the operator types into is not on it:\n%s",
						c.name, w, h, row, r.View())
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no picker drew a frame at any supported size, so this sweep asserted " +
			"nothing. Either jdeScreenStates has no pick-list state left or every one " +
			"of them is being refused; both need this test rewritten rather than deleted")
	}
}
