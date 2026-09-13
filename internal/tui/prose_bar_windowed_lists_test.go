package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_windowed_lists_test.go — the fixtures for ONE conversion recipe,
// applied to every screen that shares it.
//
// THE RECIPE, and what makes these screens one group rather than a dozen. Each
// draws a count line, an `↑ more above` marker, a window of rows, a
// `↓ N more below` marker, a blank and a footer; each binds the WHOLE navigation
// vocabulary in its own key switch — j/k, the arrows, pgup/pgdn and
// g/G/home/end — and each spelled TWO of those ten (the PM item list spelled
// four, which is the same defect with a different number rather than an
// exception to it); and each budgeted its window against the same
//
//	const chrome = 4
//
// whose last row is ONE footer line. So all three halves of the defect are the
// same on all of them: the keystrokes that acted with no word for them, a
// literal already past the 51 cells an 80-column pane gives, and a budget that
// cannot survive the fold which fixes the width. proseNavCursor and
// proseListWindow (prose_bar.go) are the shared answer, and this file is what
// presses it.
//
// A SCREEN ON THE RECIPE THAT IS NOT HERE is named in proseBarUnconverted with
// what stopped it, which is the point of taking a recipe rather than a list: the
// ones that do not come out mechanically are then visible AS the residue of a
// group rather than as items nobody got to. AssetPartsScreen is the one this
// round left — its rows are not one line each, so a window counted in ROWS is
// not a window at all.
//
// THE PAIR OF STATES IS THE POINT, not the convenience. proseNavCursor drops
// every movement segment where there is no second row to move to — which is
// correct, and which a fixture of thirty rows can never show. So each screen is
// swept in both, and TestProseBar_EveryMovementKeyIsNamedWhereItMoves requires the
// long one to MOVE and the one-row one not to: absent and empty stay different
// states, and a gate that stopped coming off would fail on the second fixture
// rather than passing quietly on the first.
//
// THE ROW BUILDERS CARRY REAL LENGTHS. Every one of these lists draws OMS-supplied
// names into a 51-cell pane, and a fixture of `Item 1` renders a row no
// operator ever sees — the vacuous-fixture rule (AGENTS.md), which has already
// cost this package a whole class of clipped rows that no test could report.

// proseBarWindowedListFixtures is every screen on that recipe, in all three
// states.
func proseBarWindowedListFixtures() []proseBarFixture {
	var out []proseBarFixture
	for _, l := range proseBarWindowedLists() {
		out = append(out, proseBarListPair(l)...)
	}
	return out
}

// proseBarRowName rewrites the name row i of a fixture is loaded with. The name
// is the ONE field every screen on the recipe draws first on its row, whatever
// the model calls it (Name, Label, Title).
type proseBarRowName func(i int, base string) string

// proseBarSameName loads every row with the name its builder wrote.
func proseBarSameName(_ int, base string) string { return base }

// proseBarMultiLineRows is how many rows the multi-line fixture holds: the long
// fixture's thirty, so the two differ only in the names.
const proseBarMultiLineRows = 30

// proseBarOversizedNameLines is how many lines the LAST row's name spans: more
// than the tallest pane Root draws (jdePaneHeights stops at a 40-row terminal)
// has body rows, so wherever it is drawn it cannot be drawn whole.
const proseBarOversizedNameLines = 40

// proseBarMultiLineName is a name carrying the embedded newlines OpenMakerSuite
// stores without complaint: every one of these fields is a plain model
// CharField, and DRF's CharField trims only the ENDS of a value, so a
// `{"name": "…\n…"}` POST or PATCH is saved as sent. Every row spans two lines,
// which is the undercount a window counted in ROWS cannot see; the last spans
// more lines than any pane has, which is the row that has to be CUT and say so.
//
// THIS IS THE FIXTURE THE REST OF THE FILE COULD NOT BE. Every other name here
// is one line, so a window that assumed one line a row was exactly right on
// every fixture it was ever pressed against — the vacuous-fixture rule, with the
// bound being height rather than width.
func proseBarMultiLineName(i int, base string) string {
	if i == proseBarMultiLineRows-1 {
		lines := make([]string, proseBarOversizedNameLines)
		for n := range lines {
			lines[n] = fmt.Sprintf("%s, line %02d", base, n+1)
		}
		return strings.Join(lines, "\n")
	}
	return base + "\nsecond line of the stored name"
}

// proseBarWindowedList is one screen of the recipe: how to build it holding `n`
// rows named by `name`, and the words its fixtures are named after.
type proseBarWindowedList struct {
	name  string
	recv  string
	build func(rows int, name proseBarRowName) proseBarScreen
	setup func(proseBarScreen)
	// immobile is why a ONE-ROW list of this kind cannot be moved. It is worded
	// per screen because the noun differs and an operator reading the failure
	// should be told which list it is about.
	immobile string
}

// proseBarListPair is the states each of these screens is swept in.
//
// THE LONG ONE IS SCROLLED, and that is a decision rather than tidiness: a list
// standing at the top draws ONE scroll marker, and the state these screens spend
// most of their life in — the operator part-way down — draws BOTH. That second
// marker is a row, proseListFixedRows reserves it, and a fixture at the top can
// never show whether the reservation was needed. It also puts the cursor
// somewhere every movement key moves FROM, which is what the movement sweep
// wants: `g` and `home` do nothing at the top, so at the top they are only
// reachable through that sweep's `end` probe.
//
// It scrolls with pgdn rather than by writing a cursor field, because the
// cursor, the window start and the window SIZE are three fields that have to
// agree and only the screen knows how — and because a fixture that reaches its
// state through the keys is a fixture that cannot describe a state the keys
// cannot reach.
//
// THE THIRD STATE IS THE LONG ONE WITH MULTI-LINE NAMES, scrolled the same way,
// so every sweep that presses the long list presses a list whose rows are not
// one line each. See proseBarMultiLineName.
func proseBarListPair(l proseBarWindowedList) []proseBarFixture {
	return []proseBarFixture{
		{
			name: l.name,
			recv: l.recv,
			build: func() proseBarScreen {
				s := proseBarSize(l.build(30, proseBarSameName), 80, 24)
				next, _ := s.Update(listRuneKey("pgdown"))
				return next.(proseBarScreen)
			},
		},
		{
			name:     l.name + "/one row",
			recv:     l.recv,
			build:    func() proseBarScreen { return l.build(1, proseBarSameName) },
			immobile: l.immobile,
		},
		{
			name:  l.name + "/multi-line names",
			recv:  l.recv,
			build: func() proseBarScreen { return proseBarMultiLineAt(l, 80, 24) },
		},
	}
}

// proseBarMultiLineAt is the multi-line fixture of one list, sized and scrolled
// with pgdn the way the long fixture is.
func proseBarMultiLineAt(l proseBarWindowedList, w, h int) proseBarScreen {
	s := l.build(proseBarMultiLineRows, proseBarMultiLineName)
	if l.setup != nil {
		l.setup(s)
	}
	s = proseBarSize(s, w, h)
	next, _ := s.Update(listRuneKey("pgdown"))
	return next.(proseBarScreen)
}

// TestProseBarWindowedList_MultiLineNamesFitThePaneAndMarkTheirCut is the
// height half of the window, on the lists whose window used to be counted in
// ROWS.
//
// Each of these lists budgeted its window as a number of rows and drew one
// line a row — true of every fixture it had, and false of an OpenMakerSuite name
// with a newline in it, which the API stores as sent. Two lines a row assembled
// a frame twice the height of its window, and clampToBox drops from the BOTTOM,
// so what went was the footer: every key named nowhere. And a single name taller
// than the pane, drawn whole, took the footer off at any height at all.
//
// So at every drawable pane where the SAME list with one-line names fits, the
// multi-line list must fit too — scrolled part-way down with both markers
// drawn, and standing on the oversized last row. The boundary is the one-line
// list's own rather than a height written down here, because below it the frame
// overruns for a reason that has nothing to do with names (proseListWindow's
// floor), and the check fails if that boundary was never reached on both sides.
// Standing on the oversized row the pane must say the name was CUT: a clipped
// name drawn with no mark reads as the whole name.
func TestProseBarWindowedList_MultiLineNamesFitThePaneAndMarkTheirCut(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, l := range proseBarWindowedLists() {
		t.Run(l.name, func(t *testing.T) {
			fits, overruns := 0, 0
			for _, w := range widths {
				for _, h := range heights {
					plain := proseBarSize(l.build(proseBarMultiLineRows, proseBarSameName), w, h)
					if next, _ := plain.Update(listRuneKey("pgdown")); next != nil {
						plain = next.(proseBarScreen)
					}
					if !proseBarFrameFits(plain, h) {
						overruns++
						continue
					}
					fits++
					scrolled := proseBarMultiLineAt(l, w, h)
					if !proseBarFrameFits(scrolled, h) {
						t.Fatalf("at %dx%d the %s with two-line names hands over %d rows for a "+
							"%d-row pane, where the same list with one-line names fits — the "+
							"footer is what clampToBox takes:\n%s",
							w, h, l.name, lipgloss.Height(scrolled.View()), screenBodyRows(h),
							stripANSI(scrolled.View()))
					}
					last, _ := scrolled.Update(listRuneKey("end"))
					oversized := last.(proseBarScreen)
					if !proseBarFrameFits(oversized, h) {
						t.Fatalf("at %dx%d the %s standing on a %d-line name hands over %d rows "+
							"for a %d-row pane:\n%s",
							w, h, l.name, proseBarOversizedNameLines,
							lipgloss.Height(oversized.View()), screenBodyRows(h),
							stripANSI(oversized.View()))
					}
					pane := stripANSI(clampToBox(oversized.View(), screenBodyCells(w), screenBodyRows(h)))
					if !strings.Contains(pane, "more lines") {
						t.Fatalf("at %dx%d the %s drew a %d-line name on a %d-row pane with no "+
							"mark saying it was cut:\n%s",
							w, h, l.name, proseBarOversizedNameLines, screenBodyRows(h), pane)
					}
				}
			}
			if fits == 0 || overruns == 0 {
				t.Errorf("the %s one-line boundary was reached on %d fitting panes and %d "+
					"overrunning ones; a side never reached is a check that asserted nothing "+
					"on it", l.name, fits, overruns)
			}
		})
	}
}

func proseBarWindowedLists() []proseBarWindowedList {
	return []proseBarWindowedList{
		{
			name: "category list", recv: "CategoryListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarCategories(rows)
				for i := range loaded {
					loaded[i].Name = name(i, loaded[i].Name)
				}
				s := NewCategoryListScreen(Deps{})
				next, _ := s.Update(categoryListLoadedMsg{rows: loaded})
				return next.(*CategoryListScreen)
			},
			immobile: "one category, so there is nowhere for the cursor to go",
		},
		{
			name: "device type list", recv: "DeviceTypeListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarDeviceTypes(rows)
				for i := range loaded {
					loaded[i].Name = name(i, loaded[i].Name)
				}
				s := NewDeviceTypeListScreen(Deps{})
				next, _ := s.Update(deviceTypeListLoadedMsg{rows: loaded})
				return next.(*DeviceTypeListScreen)
			},
			immobile: "one device type, so there is nowhere for the cursor to go",
		},
		{
			name: "thermostat list", recv: "ThermostatListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarThermostats(rows)
				for i := range loaded {
					loaded[i].Label = name(i, loaded[i].Label)
				}
				s := NewThermostatListScreen(Deps{})
				next, _ := s.Update(thermostatListLoadedMsg{rows: loaded})
				return next.(*ThermostatListScreen)
			},
			immobile: "one thermostat, so there is nowhere for the cursor to go",
		},
		{
			name: "location list", recv: "LocationListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarLocations(rows)
				for i := range loaded {
					loaded[i].Name = name(i, loaded[i].Name)
				}
				s := NewLocationListScreen(Deps{})
				next, _ := s.Update(locationListLoadedMsg{rows: loaded})
				return next.(*LocationListScreen)
			},
			immobile: "one location, so there is nowhere for the cursor to go",
		},
		{
			name: "supplier list", recv: "SupplierListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarSuppliers(rows)
				for i := range loaded {
					loaded[i].Name = name(i, loaded[i].Name)
				}
				s := NewSupplierListScreen(Deps{})
				next, _ := s.Update(supplierListLoadedMsg{rows: loaded})
				return next.(*SupplierListScreen)
			},
			immobile: "one supplier, so there is nowhere for the cursor to go",
		},
		{
			name: "sig list", recv: "SIGListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarSIGs(rows)
				for i := range loaded {
					loaded[i].Name = name(i, loaded[i].Name)
				}
				s := NewSIGListScreen(Deps{})
				next, _ := s.Update(sigListLoadedMsg{rows: loaded})
				return next.(*SIGListScreen)
			},
			immobile: "one SIG, so there is nowhere for the cursor to go",
		},
		{
			name: "webhook list", recv: "WebhookListScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarWebhooks(rows)
				for i := range loaded {
					loaded[i].Name = name(i, loaded[i].Name)
				}
				s := NewWebhookListScreen(Deps{})
				next, _ := s.Update(webhookListLoadedMsg{rows: loaded})
				return next.(*WebhookListScreen)
			},
			setup: func(screen proseBarScreen) {
				screen.(*WebhookListScreen).deps.Health = ssHealth(map[string]string{
					omsapi.ServiceKeyWebhooks: omsapi.ServiceStateOpen,
				})
			},
			immobile: "one webhook, so there is nowhere for the cursor to go",
		},
		{
			name: "maintenance items", recv: "MaintenanceItemsScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarMaintenanceItems(rows)
				for i := range loaded {
					loaded[i].Title = name(i, loaded[i].Title)
				}
				s := NewMaintenanceItemsScreen(Deps{})
				next, _ := s.Update(maintenanceItemsLoadedMsg{items: loaded})
				return next.(*MaintenanceItemsScreen)
			},
			immobile: "one PM item, so there is nowhere for the cursor to go",
		},
		{
			name: "panel breakers", recv: "PanelBreakersScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarBreakers(rows)
				for i := range loaded {
					loaded[i].Label = name(i, loaded[i].Label)
				}
				s := NewPanelBreakersScreen(Deps{}, 1, "Main distribution panel MDP-1")
				next, _ := s.Update(panelBreakersLoadedMsg{rows: loaded})
				return next.(*PanelBreakersScreen)
			},
			immobile: "one breaker on the panel, so there is nowhere for the cursor to go",
		},
		{
			name: "breaker circuits", recv: "BreakerCircuitsScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarCircuits(rows)
				for i := range loaded {
					loaded[i].Label = name(i, loaded[i].Label)
				}
				s := NewBreakerCircuitsScreen(Deps{}, 7, 1, "Bay 7 receptacles")
				next, _ := s.Update(breakerCircuitsLoadedMsg{rows: loaded})
				return next.(*BreakerCircuitsScreen)
			},
			immobile: "one circuit on the breaker, so there is nowhere for the cursor to go",
		},
		{
			name: "circuit outlets", recv: "CircuitOutletsScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarOutlets(rows)
				for i := range loaded {
					loaded[i].Label = name(i, loaded[i].Label)
				}
				s := NewCircuitOutletsScreen(Deps{}, 3, 1, "Bay 7 receptacles, north run")
				next, _ := s.Update(circuitOutletsLoadedMsg{rows: loaded})
				return next.(*CircuitOutletsScreen)
			},
			immobile: "one outlet on the circuit, so there is nowhere for the cursor to go",
		},
		{
			name: "circuit disconnects", recv: "CircuitDisconnectsScreen",
			build: func(rows int, name proseBarRowName) proseBarScreen {
				loaded := proseBarDisconnects(rows)
				for i := range loaded {
					loaded[i].Label = name(i, loaded[i].Label)
				}
				s := NewCircuitDisconnectsScreen(Deps{}, 3, 1, "Bay 7 receptacles, north run")
				next, _ := s.Update(circuitDisconnectsLoadedMsg{rows: loaded})
				return next.(*CircuitDisconnectsScreen)
			},
			immobile: "one disconnect on the circuit, so there is nowhere for the cursor to go",
		},
	}
}

func proseBarCategories(n int) []omsapi.Category {
	out := make([]omsapi.Category, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Category{
			ID:         i + 1,
			Name:       fmt.Sprintf("Fasteners, metric — socket head cap screws %d", i+1),
			ParentName: "Hardware",
			ItemCount:  12 + i,
			Color:      "#3366cc",
		})
	}
	return out
}

func proseBarDeviceTypes(n int) []forgekeyapi.DeviceType {
	out := make([]forgekeyapi.DeviceType, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, forgekeyapi.DeviceType{
			ID: i + 1, IsActive: true,
			Name:        fmt.Sprintf("ESP32-S3 badge reader, revision %d", i+1),
			Code:        fmt.Sprintf("READER-S3-R%d", i+1),
			Description: "Wall-mounted reader with an e-paper panel and a relay output",
		})
	}
	return out
}

func proseBarThermostats(n int) []omsapi.Thermostat {
	out := make([]omsapi.Thermostat, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Thermostat{
			ID:           i + 1,
			Label:        fmt.Sprintf("Machine shop north bay, zone %d", i+1),
			LocationName: "Machine shop, north bay",
			Manufacturer: "Honeywell",
			Model:        "T6 Pro",
			NeedsReview:  i%4 == 0,
		})
	}
	return out
}

func proseBarLocations(n int) []omsapi.Location {
	out := make([]omsapi.Location, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Location{
			ID: i + 1, IsActive: true,
			Name:         fmt.Sprintf("Machine shop, north bay — rack %d, level A", i+1),
			ParentName:   "Machine shop",
			FixtureCount: 3 + i,
		})
	}
	return out
}

func proseBarSuppliers(n int) []omsapi.Supplier {
	out := make([]omsapi.Supplier, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Supplier{
			ID:           i + 1,
			Name:         fmt.Sprintf("Grainger Industrial Supply, branch %d", i+1),
			SupplierType: "distributor",
			ItemCount:    212 + i,
		})
	}
	return out
}

func proseBarSIGs(n int) []omsapi.SIG {
	out := make([]omsapi.SIG, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.SIG{
			ID:          i + 1,
			Name:        fmt.Sprintf("Metal Fabrication SIG, shift %d", i+1),
			GroupEmail:  fmt.Sprintf("metalfab%02d@example.org", i+1),
			MemberCount: 30 + i,
			AssetCount:  12 + i,
		})
	}
	return out
}

func proseBarWebhooks(n int) []omsapi.Webhook {
	out := make([]omsapi.Webhook, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.Webhook{
			ID: i + 1, IsActive: true,
			Name:             fmt.Sprintf("Slack #shop-floor notifications %d", i+1),
			URL:              "https://hooks.example.org/services/T000/B000/XXXX",
			EventTypeDisplay: "Inventory item below minimum",
			TotalTriggers:    40 + i,
		})
	}
	return out
}

func proseBarMaintenanceItems(n int) []omsapi.MaintenanceItem {
	out := make([]omsapi.MaintenanceItem, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.MaintenanceItem{
			ID: fmt.Sprintf("pm-%d", i+1), IsActive: true,
			Title:     fmt.Sprintf("Monthly way-lube top-up and chip auger check %d", i+1),
			AssetName: "Haas VF-2SS vertical machining centre",
		})
	}
	return out
}

func proseBarBreakers(n int) []omsapi.PowerBreakerDetail {
	out := make([]omsapi.PowerBreakerDetail, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.PowerBreakerDetail{
			ID: i + 1, Panel: 1, Position: fmt.Sprint(i + 1),
			PoleCount: 2, Amperage: 20, Phase: "A", Status: "active",
			Label:        fmt.Sprintf("Machine shop north bay receptacles %d", i+1),
			CircuitCount: 1 + i%3,
		})
	}
	return out
}

func proseBarCircuits(n int) []omsapi.PowerCircuitDetail {
	out := make([]omsapi.PowerCircuitDetail, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.PowerCircuitDetail{
			ID: i + 1, Breaker: 7, PanelID: 1,
			Label:         fmt.Sprintf("Bay 7 receptacles, north run, segment %d", i+1),
			ConductorSize: "12 AWG", OutletCount: 2 + i%4,
		})
	}
	return out
}

func proseBarOutlets(n int) []omsapi.PowerOutletDetail {
	out := make([]omsapi.PowerOutletDetail, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.PowerOutletDetail{
			ID: i + 1, Circuit: 3, Location: 1,
			LocationName: "Machine shop, north bay",
			OutletType:   "NEMA 5-20R",
			Label:        fmt.Sprintf("North wall duplex, position %d", i+1),
			Status:       "active",
		})
	}
	return out
}

func proseBarDisconnects(n int) []omsapi.DisconnectDetail {
	out := make([]omsapi.DisconnectDetail, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.DisconnectDetail{
			ID: i + 1, Circuit: 3,
			LocationName:   "Machine shop, north bay",
			Label:          fmt.Sprintf("Fused disconnect beside the surface grinder %d", i+1),
			DisconnectType: "fused", FuseSize: "30A", IsLockable: true,
		})
	}
	return out
}
