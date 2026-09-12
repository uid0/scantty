package tui

import (
	"fmt"

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
// swept twice, and TestProseBar_EveryMovementKeyIsNamedWhereItMoves requires the
// long one to MOVE and the one-row one not to: absent and empty stay different
// states, and a gate that stopped coming off would fail on the second fixture
// rather than passing quietly on the first.
//
// THE ROW BUILDERS CARRY REAL LENGTHS. Every one of these lists draws OMS-supplied
// names into a 51-cell pane, and a fixture of `Item 1` renders a row no
// operator ever sees — the vacuous-fixture rule (AGENTS.md), which has already
// cost this package a whole class of clipped rows that no test could report.

// proseBarWindowedListFixtures is every screen on that recipe, in both states.
func proseBarWindowedListFixtures() []proseBarFixture {
	var out []proseBarFixture
	for _, l := range proseBarWindowedLists() {
		out = append(out, proseBarListPair(l)...)
	}
	return out
}

// proseBarWindowedList is one screen of the recipe: how to build it holding `n`
// rows, and the words the pair of fixtures is named after.
type proseBarWindowedList struct {
	name  string
	recv  string
	build func(rows int) proseBarScreen
	// immobile is why a ONE-ROW list of this kind cannot be moved. It is worded
	// per screen because the noun differs and an operator reading the failure
	// should be told which list it is about.
	immobile string
}

// proseBarListPair is the two states each of these screens is swept in.
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
func proseBarListPair(l proseBarWindowedList) []proseBarFixture {
	return []proseBarFixture{
		{
			name: l.name,
			recv: l.recv,
			build: func() proseBarScreen {
				s := proseBarSize(l.build(30), 80, 24)
				next, _ := s.Update(listRuneKey("pgdown"))
				return next.(proseBarScreen)
			},
		},
		{
			name:     l.name + "/one row",
			recv:     l.recv,
			build:    func() proseBarScreen { return l.build(1) },
			immobile: l.immobile,
		},
	}
}

func proseBarWindowedLists() []proseBarWindowedList {
	return []proseBarWindowedList{
		{
			name: "category list", recv: "CategoryListScreen",
			build: func(rows int) proseBarScreen {
				s := NewCategoryListScreen(Deps{})
				next, _ := s.Update(categoryListLoadedMsg{rows: proseBarCategories(rows)})
				return next.(*CategoryListScreen)
			},
			immobile: "one category, so there is nowhere for the cursor to go",
		},
		{
			name: "device type list", recv: "DeviceTypeListScreen",
			build: func(rows int) proseBarScreen {
				s := NewDeviceTypeListScreen(Deps{})
				next, _ := s.Update(deviceTypeListLoadedMsg{rows: proseBarDeviceTypes(rows)})
				return next.(*DeviceTypeListScreen)
			},
			immobile: "one device type, so there is nowhere for the cursor to go",
		},
		{
			name: "thermostat list", recv: "ThermostatListScreen",
			build: func(rows int) proseBarScreen {
				s := NewThermostatListScreen(Deps{})
				next, _ := s.Update(thermostatListLoadedMsg{rows: proseBarThermostats(rows)})
				return next.(*ThermostatListScreen)
			},
			immobile: "one thermostat, so there is nowhere for the cursor to go",
		},
		{
			name: "location list", recv: "LocationListScreen",
			build: func(rows int) proseBarScreen {
				s := NewLocationListScreen(Deps{})
				next, _ := s.Update(locationListLoadedMsg{rows: proseBarLocations(rows)})
				return next.(*LocationListScreen)
			},
			immobile: "one location, so there is nowhere for the cursor to go",
		},
		{
			name: "supplier list", recv: "SupplierListScreen",
			build: func(rows int) proseBarScreen {
				s := NewSupplierListScreen(Deps{})
				next, _ := s.Update(supplierListLoadedMsg{rows: proseBarSuppliers(rows)})
				return next.(*SupplierListScreen)
			},
			immobile: "one supplier, so there is nowhere for the cursor to go",
		},
		{
			name: "sig list", recv: "SIGListScreen",
			build: func(rows int) proseBarScreen {
				s := NewSIGListScreen(Deps{})
				next, _ := s.Update(sigListLoadedMsg{rows: proseBarSIGs(rows)})
				return next.(*SIGListScreen)
			},
			immobile: "one SIG, so there is nowhere for the cursor to go",
		},
		{
			name: "webhook list", recv: "WebhookListScreen",
			build: func(rows int) proseBarScreen {
				s := NewWebhookListScreen(Deps{})
				next, _ := s.Update(webhookListLoadedMsg{rows: proseBarWebhooks(rows)})
				return next.(*WebhookListScreen)
			},
			immobile: "one webhook, so there is nowhere for the cursor to go",
		},
		{
			name: "maintenance items", recv: "MaintenanceItemsScreen",
			build: func(rows int) proseBarScreen {
				s := NewMaintenanceItemsScreen(Deps{})
				next, _ := s.Update(maintenanceItemsLoadedMsg{items: proseBarMaintenanceItems(rows)})
				return next.(*MaintenanceItemsScreen)
			},
			immobile: "one PM item, so there is nowhere for the cursor to go",
		},
		{
			name: "panel breakers", recv: "PanelBreakersScreen",
			build: func(rows int) proseBarScreen {
				s := NewPanelBreakersScreen(Deps{}, 1, "Main distribution panel MDP-1")
				next, _ := s.Update(panelBreakersLoadedMsg{rows: proseBarBreakers(rows)})
				return next.(*PanelBreakersScreen)
			},
			immobile: "one breaker on the panel, so there is nowhere for the cursor to go",
		},
		{
			name: "breaker circuits", recv: "BreakerCircuitsScreen",
			build: func(rows int) proseBarScreen {
				s := NewBreakerCircuitsScreen(Deps{}, 7, 1, "Bay 7 receptacles")
				next, _ := s.Update(breakerCircuitsLoadedMsg{rows: proseBarCircuits(rows)})
				return next.(*BreakerCircuitsScreen)
			},
			immobile: "one circuit on the breaker, so there is nowhere for the cursor to go",
		},
		{
			name: "circuit outlets", recv: "CircuitOutletsScreen",
			build: func(rows int) proseBarScreen {
				s := NewCircuitOutletsScreen(Deps{}, 3, 1, "Bay 7 receptacles, north run")
				next, _ := s.Update(circuitOutletsLoadedMsg{rows: proseBarOutlets(rows)})
				return next.(*CircuitOutletsScreen)
			},
			immobile: "one outlet on the circuit, so there is nowhere for the cursor to go",
		},
		{
			name: "circuit disconnects", recv: "CircuitDisconnectsScreen",
			build: func(rows int) proseBarScreen {
				s := NewCircuitDisconnectsScreen(Deps{}, 3, 1, "Bay 7 receptacles, north run")
				next, _ := s.Update(circuitDisconnectsLoadedMsg{rows: proseBarDisconnects(rows)})
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
