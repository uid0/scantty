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
	"reflect"
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
		"AssetFormScreen":            func() Screen { s := NewAssetFormScreen(Deps{}, ""); s.loading = false; return s },
		"AssetPartFormScreen":        func() Screen { s := NewAssetPartFormScreen(Deps{}, "a1", "Asset", ""); s.loading = false; return s },
		"AuthorizationGrantScreen":   func() Screen { s := NewAuthorizationGrantScreen(Deps{}); s.loading = false; return s },
		"CategoryFormScreen":         func() Screen { s := NewCategoryFormScreen(Deps{}, ""); s.loading = false; return s },
		"DeviceTypeFormScreen":       func() Screen { return NewDeviceTypeFormScreen(Deps{}, 0) },
		"DisconnectFormScreen":       func() Screen { s := NewDisconnectFormScreen(Deps{}, 0, 0, 0); s.loading = false; return s },
		"InventoryItemFormScreen":    func() Screen { s := NewInventoryItemFormScreen(Deps{}, ""); s.loading = false; return s },
		"ItemSupplierFormScreen":     func() Screen { s := NewItemSupplierFormScreen(Deps{}, "i1", "Item", nil); s.loading = false; return s },
		"LocationFormScreen":         func() Screen { s := NewLocationFormScreen(Deps{}, ""); s.loading = false; return s },
		"LocationProblemFormScreen":  func() Screen { return NewLocationProblemFormScreen(Deps{}, 1, "Loc") },
		"MaintenanceItemFormScreen":  func() Screen { s := NewMaintenanceItemFormScreen(Deps{}, ""); s.loading = false; return s },
		"MakerBoxFormScreen":         func() Screen { return NewMakerBoxFormScreen(Deps{}, 0) },
		"PowerBreakerFormScreen":     func() Screen { s := NewPowerBreakerFormScreen(Deps{}, 0, 0); s.loading = false; return s },
		"PowerCircuitFormScreen":     func() Screen { s := NewPowerCircuitFormScreen(Deps{}, 0, 0, 0); s.loading = false; return s },
		"PowerOutletFormScreen":      func() Screen { s := NewPowerOutletFormScreen(Deps{}, 0, 0, 0); s.loading = false; return s },
		"PowerPanelFormScreen":       func() Screen { s := NewPowerPanelFormScreen(Deps{}, 0); s.loading = false; return s },
		"ProjectStorageFormScreen":   func() Screen { return NewProjectStorageFormScreen(Deps{}) },
		"PurchaseOrderAddLineScreen": func() Screen { return NewPurchaseOrderAddLineScreen(Deps{}, poViewPO()) },
		// Past its loading state, on the supplier picker it opens on: a screen
		// still fetching draws one muted line and no rows, and a fixture that
		// renders nothing proves nothing.
		"PurchaseOrderCreateScreen": func() Screen { return poCreateFixture() },
		// With FILES on it, for the same reason as ServiceStatusScreen: poViewPO
		// carries no attachments, so the list has no rows and the cursor has
		// nowhere to go.
		"PurchaseOrderAttachmentsScreen": func() Screen {
			po := poViewPO()
			for i := 0; i < 6; i++ {
				po.Attachments = append(po.Attachments, omsapi.PurchaseOrderAttachment{
					ID: i + 1, FileName: fmt.Sprintf("quote-2026-%02d.pdf", i+1),
					Description: "Vendor quotation", UploadedByName: "shop.lead",
				})
			}
			return NewPurchaseOrderAttachmentsScreen(Deps{}, po)
		},
		"PurchaseOrderDetailScreen": func() Screen {
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			return s
		},
		"PurchaseOrderEditScreen": func() Screen { return NewPurchaseOrderEditScreen(Deps{}, poViewPO()) },
		"ReceiveFormScreen":       func() Screen { return receivePaneFixture(nil) },
		"SIGFormScreen":           func() Screen { return NewSIGFormScreen(Deps{}, "") },
		// With SERVICES on it. A bare Deps{} carries no health snapshot, so the
		// screen draws a summary over an empty list: nothing to move a cursor
		// through, and every sweep that presses a key at it proves nothing.
		"ServiceStatusScreen":       func() Screen { return NewServiceStatusScreen(ssDegradedDeps(nil)) },
		"SiteSettingsFormScreen":    func() Screen { s := NewSiteSettingsFormScreen(Deps{}); s.loading = false; return s },
		"StorageAssignFormScreen":   func() Screen { return NewStorageAssignFormScreen(Deps{}, "R1-S1", nil) },
		"StorageSlotFormScreen":     func() Screen { return NewStorageSlotFormScreen(Deps{}, "") },
		"StorageSlotGenerateScreen": func() Screen { return NewStorageSlotGenerateScreen(Deps{}, 0) },
		"SupplierFormScreen":        func() Screen { return NewSupplierFormScreen(Deps{}, "") },
		"ThermostatFormScreen":      func() Screen { s := NewThermostatFormScreen(Deps{}, ""); s.loading = false; return s },
		"WebhookFormScreen":         func() Screen { return NewWebhookFormScreen(Deps{}, 0) },
	}
}

// poCreateFixture is the New PO screen with a supplier list on it. Its EXTRA
// states — the source chooser under a long cart, the review surface, the line
// form, the four pickers — are in jdeScreenStates, because they are where this
// screen's geometry is actually interesting: the pinned header is at its
// tallest on the chooser and the body at its longest on review.
func poCreateFixture() *PurchaseOrderCreateScreen {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierLoading = false
	s.suppliers = []omsapi.Supplier{
		{ID: 1, Name: "Northern Tool & Die Supply Co"},
		{ID: 2, Name: "Acme Fasteners"},
		{ID: 3, Name: "Midwest Bearing"},
	}
	s.supplierCursor = 0
	return s
}

// poCreateStaged is that screen with a supplier committed, the optional rows
// offered and a cart long enough to overflow every pane this sweep draws.
func poCreateStaged() *PurchaseOrderCreateScreen {
	s := poCreateFixture()
	s.supplierID = 1
	s.agreements = []omsapi.SupplierAgreement{{ID: 4, Name: "2026 nonprofit pricing"}}
	s.assoc.workOrders = []omsapi.WorkOrder{{ID: "wo-1", DisplayTitle: "Lathe teardown"}}
	s.assoc.committees = []omsapi.SIG{{ID: 3, Name: "Metal shop"}}
	id, cost := 7, 3.5
	for i := 0; i < 12; i++ {
		s.lines = append(s.lines, poCartLine{
			item: omsapi.PurchaseOrderCreateItem{
				ItemSupplierID: &id, Quantity: 2, UnitCost: &cost,
				ExpectedShipmentDate: "2026-09-01",
			},
			label: fmt.Sprintf("Hex bolt M8x40 zinc plated grade 8.8 #%d", i+1),
		})
	}
	s.phase = poPhaseSource
	return s
}

// poAddLookupFixture is a lookup answer with enough candidates for the choose
// list to have somewhere to move and enough prose on the confirm frame to
// outrun a short pane.
func poAddLookupFixture() *omsapi.POLineLookup {
	l := &omsapi.POLineLookup{
		Query:         "widget",
		Supplier:      omsapi.POLineSupplierRef{ID: 1, Name: "Acme Fasteners & Industrial Supply"},
		PurchaseOrder: omsapi.POLineLookupOrder{ID: "po-1", Number: "PO-2026-0042", Status: "draft", CanAddItems: true},
		BestMatchKind: "name",
	}
	for i := 0; i < 6; i++ {
		l.Candidates = append(l.Candidates, omsapi.POLineCandidate{
			ItemSupplier: i + 1,
			MatchKind:    "name",
			MatchLabel:   "Name",
			MatchedValue: fmt.Sprintf("Hex bolt M8x40 zinc plated grade 8.8 #%d", i+1),
			Item: omsapi.POLineItemRef{
				ID: fmt.Sprintf("i-%d", i), Name: fmt.Sprintf("Hex bolt M8x40 zinc plated grade 8.8 #%d", i+1),
				SKU: fmt.Sprintf("HB-M8-40-%03d", i),
			},
			SupplierSKU:        fmt.Sprintf("AF-99-12-ZP-LH-%04d", i),
			QuantityPerPackage: 25,
			SuggestedQuantity:  50,
			SuggestedUnitCost:  omsapi.DecimalString("0.42"),
		})
	}
	l.TotalCandidates = len(l.Candidates)
	l.BestMatchTotal = len(l.Candidates)
	return l
}

// itemChainFixtureRows / itemKitFixtureRows are sub-list contents long enough
// for the cursor to have somewhere to go — a one-row list makes every sweep
// about movement vacuous.
func itemChainFixtureRows() []packagingRow {
	return []packagingRow{
		{key: 1, id: 1, name: "Pallet", baseUnits: 1000},
		{key: 2, id: 2, name: "Case", baseUnits: 100},
		{key: 3, id: 3, name: "Ream", baseUnits: 1},
	}
}

func itemKitFixtureRows() []kitComponentRow {
	return []kitComponentRow{
		{key: 1, component: "i-1", name: "Hex bolt M8x40 zinc plated grade 8.8", sku: "HB-1", quantity: 4},
		{key: 2, component: "i-2", name: "Flat washer M8 stainless", sku: "FW-8", quantity: 4},
		{key: 3, component: "i-3", name: "Nyloc nut M8", sku: "NN-8", quantity: 4, notes: "torque to 25Nm"},
	}
}

// maintenanceTaskFixtureRows / storageGenFixtureLevels are sub-list contents
// long enough for the cursor to have somewhere to go.
func maintenanceTaskFixtureRows() []taskRow {
	return []taskRow{
		{id: "t-1", title: "Drain the sump and check the filter screen", isRequired: true},
		{id: "t-2", title: "Grease the ways", description: "Way oil, not chain lube.", isRequired: true},
		{id: "t-3", title: "Check belt tension"},
	}
}

func storageGenFixtureLevels() []storageGenLevelRow {
	return []storageGenLevelRow{
		{level: "A", positions: 12},
		{level: "B", positions: 12, palletJack: true},
		{level: "C", positions: 8},
	}
}

// receivePaneFixture is the receiving screen PAST its loading frame.
//
// It has to be, and that is the rule this file already states rather than a
// special case: a screen still fetching draws "Loading…" and no frame at all,
// so an entry left there proves nothing. Everything the receiving form draws
// comes off the worksheet, so the fixture lands one — through the reply the
// fetch really produces, not by writing the fields, since the row model is
// derived in applyWorksheet and a fixture that bypassed it would be sweeping a
// layout the endpoint cannot produce.
func receivePaneFixture(tweak func(*ReceiveFormScreen)) Screen {
	po := poViewPO()
	sheet := &omsapi.ReceivingWorksheet{
		PurchaseOrder: po.ID, Number: po.Number, Supplier: "Acme Supply",
		Status: "sent", StatusLabel: "Sent", CanReceive: true,
	}
	for _, li := range po.Items {
		sheet.Lines = append(sheet.Lines, omsapi.ReceivingLine{
			PurchaseOrderItem: li.ID,
			Label:             li.DisplayLabel(),
			ItemType:          "inventory_item",
			QuantityOrdered:   li.QuantityOrdered,
			QuantityReceived:  li.QuantityReceived,
			QuantityPending:   li.QuantityPending,
			QuantityVariance:  li.QuantityReceived - li.QuantityOrdered,
			ReceiptState:      omsapi.ReceiptStateNotReceived,
			ReceiptStateLabel: "Not received",
			IsKitLine:         li.IsKitLine,
		})
		sheet.OutstandingLineCount++
	}
	s := NewReceiveFormScreen(Deps{}, po)
	s.Update(receiveSheetMsg{sheet: sheet})
	if tweak != nil {
		tweak(s)
	}
	return s
}

// receiveSerialPastEndFixture is the receiving screen on its ALL UNITS ANSWERED
// frame: the serial phase with serialCursor standing past the end of the queue.
//
// It is reached the way an operator reaches it, through the real worksheet and
// the real keys, because the state is a consequence of the flow rather than a
// field somebody sets: enter a quantity for a serialized line, capture every
// serial the line owes, land on the review, Esc back to the quantities to
// double-check a count, and press Enter again. enrol carries the captures across
// by identity, so firstUncaptured walks off the end and toSerial opens the phase
// on the frame that has no box.
//
// That frame is the reason this fixture exists. It is the only one in the
// program whose bar spells its step-back key as the bare token "PgUp", and its
// PgUp arm was the last movement handler in the package still acting on a pane
// the layer refuses — reachable, named, and swept by nothing.
func receiveSerialPastEndFixture() Screen {
	line := receiveWSSerialized(11, "Sensor module", 2, 0)
	s := NewReceiveFormScreen(Deps{}, receivePO(line))
	s.Update(receiveSheetMsg{sheet: receiveWorksheet(line)})

	// Bounded, for the reason receiveWalkTo is: a declined key would turn an
	// unbounded walk into a hung package rather than a failing fixture, and
	// receiveSerialPastEndState is what reports a walk that did not arrive.
	for n := 0; s.focused != receiveRowFirstLine && n <= 8; n++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	s.Update(woRuneKey("2"))
	s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // the quantity form -> serial capture
	for i := 0; i < 2; i++ {
		s.Update(woRuneKey(fmt.Sprintf("SN-%d", i+1)))
		s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})   // the review -> back to the quantities
	s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // and in again, past the end this time
	return s
}

// TestReceive_TheSerialPastTheEndFixtureIsOnThatFrame is the non-vacuity guard
// on the fixture above.
//
// A fixture built by DRIVING can stop arriving without anyone noticing — a
// changed row order, a refusal, one key renamed — and it would then be swept in
// whatever state it did reach, which is the vacuous-fixture rule (AGENTS.md)
// with the fixture rather than the assertion at fault. So the state it claims is
// asserted: the serial phase, a queue with units in it, and a cursor past the
// end of that queue.
func TestReceive_TheSerialPastTheEndFixtureIsOnThatFrame(t *testing.T) {
	s, ok := receiveSerialPastEndFixture().(*ReceiveFormScreen)
	if !ok {
		t.Fatalf("the fixture built a %T, want *ReceiveFormScreen", s)
	}
	if s.phase != phaseSerial {
		t.Fatalf("the fixture is on phase %v, want the serial phase", s.phase)
	}
	if len(s.serialUnits) == 0 {
		t.Fatal("the fixture reached the serial phase with an empty queue, so its bar " +
			"names no PgUp and the frame under test is not the one it claims")
	}
	if s.serialCursor < len(s.serialUnits) {
		t.Fatalf("the fixture is on unit %d of %d, want the cursor PAST the end — the "+
			"all-units-answered frame is the one this fixture exists for",
			s.serialCursor+1, len(s.serialUnits))
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

		// The receiving form's all-units-answered serial frame. Its bar is the
		// only one in the program spelling a movement key as the bare token
		// "PgUp", and the base ReceiveFormScreen fixture opens on the quantity
		// form, which never reaches it.
		"ReceiveFormScreen/serial past the end": receiveSerialPastEndFixture,

		// The add-line flow's two CURSORED / SCROLLED phases. Its fixture opens
		// on the identifier row, which is one text box with nothing to move
		// through, so without these the screen is swept in the one state where
		// every sweep about movement is vacuous — and the confirm frame is one
		// of only two read-only SCROLLED bodies in the program (the order pad is
		// the other), which is the shape the refused-pane defect was reported
		// on.
		"PurchaseOrderAddLineScreen/choose": func() Screen {
			s := NewPurchaseOrderAddLineScreen(Deps{}, poViewPO())
			s.idIn.SetValue("widget")
			s.lookup = poAddLookupFixture()
			s.phase = poAddPhaseChoose
			return s
		},
		"PurchaseOrderAddLineScreen/confirm": func() Screen {
			s := NewPurchaseOrderAddLineScreen(Deps{}, poViewPO())
			s.idIn.SetValue("AF-99-12-ZP-LH-HEAVY")
			s.lookup = poAddLookupFixture()
			c := s.lookup.Candidates[0]
			s.chosen = &c
			s.chosenFrom = poAddPhaseChoose
			s.phase = poAddPhaseConfirm
			return s
		},

		// The purchasing sub-forms and sub-lists. Each has a cursor or a focus
		// of its own, and until they were swept the movement rule was checked
		// only on the states somebody happened to list.
		"PurchaseOrderEditScreen/line editor": func() Screen {
			s := NewPurchaseOrderEditScreen(Deps{}, poViewPO())
			s.openLineEditor(0)
			return s
		},
		"PurchaseOrderDetailScreen/mark shipped": func() Screen {
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			s.openShipForm()
			return s
		},
		"PurchaseOrderDetailScreen/mark delivered": func() Screen {
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			s.openDeliverForm()
			return s
		},
		// The two removal sub-phases. The delete confirm is the DESTRUCTIVE one
		// and it draws a block a short pane has to give ground in — the line's
		// identity, its numbers, and two folded caveats — so it is swept at
		// every height rather than trusted to be short. It is reached with the
		// server's flag SET, because that flag is the only thing that opens it.
		"PurchaseOrderEditScreen/delete confirm": func() Screen {
			s := NewPurchaseOrderEditScreen(Deps{}, poDeletablePO())
			s.openLineEditor(0)
			s.lineFocus = poLineRowStatus
			s.openDeleteLine(0)
			return s
		},
		"PurchaseOrderEditScreen/void prompt": func() Screen {
			s := NewPurchaseOrderEditScreen(Deps{}, poViewPO())
			s.openLineEditor(0)
			s.lineFocus = poLineRowStatus
			s.openVoidLine(0)
			return s
		},
		"PurchaseOrderEditScreen/association picker": func() Screen {
			s := NewPurchaseOrderEditScreen(Deps{}, poViewPO())
			s.assoc.workOrders = []omsapi.WorkOrder{
				{ID: "wo-1", DisplayTitle: "Lathe teardown and spindle rebuild"},
				{ID: "wo-2", DisplayTitle: "Mill way-cover replacement"},
				{ID: "wo-3", DisplayTitle: "Compressor annual service"},
			}
			s.openAssocPick(poAssocFieldWorkOrder, -1)
			return s
		},
		"PurchaseOrderAttachmentsScreen/upload": func() Screen {
			s := NewPurchaseOrderAttachmentsScreen(Deps{}, poViewPO())
			s.openUpload()
			return s
		},
		"StorageSlotGenerateScreen/run report": func() Screen {
			s := NewStorageSlotGenerateScreen(Deps{}, 0)
			s.phase = genPhaseResult
			res := &omsapi.GenerateRackResult{Rack: 4}
			for i := 0; i < 9; i++ {
				res.Created = append(res.Created, fmt.Sprintf("R4-A%02d", i+1))
			}
			res.Skipped = []string{"R4-B01", "R4-B02"}
			res.WithoutTag = []string{"R4-C01"}
			res.CreatedCount, res.SkippedCount = len(res.Created), len(res.Skipped)
			s.result = res
			return s
		},
		"MaintenanceItemFormScreen/task list": func() Screen {
			s := NewMaintenanceItemFormScreen(Deps{}, "")
			s.loading = false
			s.tasks = maintenanceTaskFixtureRows()
			s.openSublist(mfTasks)
			return s
		},
		"MaintenanceItemFormScreen/task editor": func() Screen {
			s := NewMaintenanceItemFormScreen(Deps{}, "")
			s.loading = false
			s.tasks = maintenanceTaskFixtureRows()
			s.openSublist(mfTasks)
			s.openTaskEditor(0)
			return s
		},
		"StorageSlotGenerateScreen/level list": func() Screen {
			s := NewStorageSlotGenerateScreen(Deps{}, 0)
			s.levels = storageGenFixtureLevels()
			s.openLevels()
			return s
		},
		"StorageSlotGenerateScreen/level row": func() Screen {
			s := NewStorageSlotGenerateScreen(Deps{}, 0)
			s.levels = storageGenFixtureLevels()
			s.openLevels()
			s.openLevelRow(0)
			return s
		},

		"PurchaseOrderCreateScreen/source chooser": func() Screen { return poCreateStaged() },
		"PurchaseOrderCreateScreen/review": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseReview
			s.poNotes.Focus()
			return s
		},
		"PurchaseOrderCreateScreen/line form": func() Screen {
			s := poCreateStaged()
			id := 7
			s.enterLinePhase(&id, nil, "Hex bolt M8x40 zinc plated grade 8.8", 2, 3.5, 0, 12)
			return s
		},
		"PurchaseOrderCreateScreen/supplier switch": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseSupplierSwitch
			s.supplierCursor = 1
			return s
		},
		"PurchaseOrderCreateScreen/item picker": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseItemPick
			s.itemSuppliersFor = s.supplierID
			for i := 0; i < 12; i++ {
				s.itemSuppliersAll = append(s.itemSuppliersAll, omsapi.ItemSupplier{
					ID: i + 1, ItemName: fmt.Sprintf("Hex bolt M8x40 zinc #%d", i+1),
					SupplierSKU: fmt.Sprintf("AF-99-12-ZP-LH-%04d", i), UnitCost: "3.50",
				})
			}
			s.itemSuppliers = s.itemSuppliersAll
			return s
		},
		"PurchaseOrderCreateScreen/item search open": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseItemPick
			s.itemSuppliersFor = s.supplierID
			s.itemSuppliersAll = []omsapi.ItemSupplier{{ID: 1, ItemName: "Hex bolt", SupplierSKU: "AF-1"}}
			s.itemSuppliers = s.itemSuppliersAll
			s.itemSuppliersTyping = true
			s.itemSuppliersSearch.Focus()
			s.itemSuppliersSearch.SetValue("hex")
			return s
		},
		"PurchaseOrderCreateScreen/asset picker": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseAssetPick
			// A page number, because the phase is only ever reached through an
			// arm that sets one — a fixture that leaves it 0 draws "page 0",
			// which is a state no operator can be in.
			s.assetsPage = 1
			s.assetsHasNext = true
			for i := 0; i < 8; i++ {
				s.assets = append(s.assets, omsapi.Asset{
					ID: fmt.Sprintf("a-%d", i), Name: fmt.Sprintf("Bridgeport mill #%d", i+1),
					AssetTag: fmt.Sprintf("TAG-%04d", i), SerialNumber: "SN-12345678",
				})
			}
			return s
		},
		"PurchaseOrderCreateScreen/reorder picker": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseReorderPick
			for i := 0; i < 9; i++ {
				s.reorderItems = append(s.reorderItems, omsapi.ReorderDataItem{
					ItemName:          fmt.Sprintf("Hex bolt M8x40 zinc #%d", i+1),
					SuggestedQuantity: 25, CurrentStock: 2, MinimumStock: 10,
					UnitCost: "3.50",
				})
			}
			return s
		},
		"PurchaseOrderCreateScreen/agreement picker": func() Screen {
			s := poCreateStaged()
			s.phase = poPhaseAgreement
			s.agreementCursor = 1
			s.agreements = []omsapi.SupplierAgreement{
				{ID: 4, Name: "2026 nonprofit pricing", Notes: "15% off list, net 30, free freight over $250."},
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
		// The nested sub-lists and their per-row editors. Each has its own
		// cursor or focus, and until they were swept the movement rule was
		// being applied to the states somebody happened to think of — which is
		// the failure this project keeps paying for one level down.
		"InventoryItemFormScreen/chain list": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.packRows = itemChainFixtureRows()
			s.openChain()
			return s
		},
		"InventoryItemFormScreen/chain row": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.packRows = itemChainFixtureRows()
			s.openChain()
			s.openChainRow(0)
			return s
		},
		"InventoryItemFormScreen/kit list": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.kitRows = itemKitFixtureRows()
			s.openKitList()
			return s
		},
		"InventoryItemFormScreen/kit row": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.kitRows = itemKitFixtureRows()
			s.openKitList()
			s.openKitRow(0)
			return s
		},
		// The same four lists at their MINIMUM — the state a screen opens in
		// before anything has been added to it, and the one the fixtures above
		// put out of reach.
		//
		// They are the other axis of this file. Deriving the SCREENS makes a
		// screen impossible to forget and says nothing about the states inside
		// one, and the states above were all given several rows to stop the
		// movement sweeps being vacuous — which is right, and which also meant
		// no list here had ONE navigable row. That is the state where "the body
		// overflows" and "a page has somewhere to land" come apart: an empty kit
		// list is heading + guidance + the add row against a short pane, so the
		// body scrolls while the only row a cursor can stand on is the add row.
		// The bar named PgUp/PgDn there and a page moved nothing, on the DEFAULT
		// state of a new inventory item.
		"InventoryItemFormScreen/kit list empty": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.openKitList()
			return s
		},
		"InventoryItemFormScreen/chain list empty": func() Screen {
			s := NewInventoryItemFormScreen(Deps{}, "")
			s.loading = false
			s.openChain()
			return s
		},
		"StorageSlotGenerateScreen/level list empty": func() Screen {
			s := NewStorageSlotGenerateScreen(Deps{}, 0)
			s.openLevels()
			return s
		},
		"PurchaseOrderAttachmentsScreen/one file": func() Screen {
			// REPLACED rather than appended: poViewPO already carries one, and
			// appending to it would build the two-row list the six-file fixture
			// above already covers — which is exactly how the one-row case went
			// missing in the first place.
			po := poViewPO()
			po.Attachments = []omsapi.PurchaseOrderAttachment{{
				ID: 1, FileName: "quote-2026-01.pdf",
				Description:    "Vendor quotation for the whole order, itemised by line",
				UploadedByName: "shop.lead",
			}}
			return NewPurchaseOrderAttachmentsScreen(Deps{}, po)
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
			s.panels = []omsapi.PowerPanel{
				{ID: 1, Name: "P1", LocationName: "Shop", PhaseConfiguration: "split"},
				{ID: 2, Name: "P2", LocationName: "Mezzanine", PhaseConfiguration: "three"},
			}
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
// It checks BOTH axes now, and the width half is the newer of the two. The
// non-wrapping frames used to draw renderActionBar, which puts every key on one
// line, tightens the gutter and then lets the line run past the pane: eleven
// form screens name enough keys at 80 columns to reach that
// (MaintenanceItemFormScreen's bar is 63 cells against a pane of 51), so
// clampToBox cut the tail and the keys on it went unnamed while they went on
// working. frame and frameWithHeader are frameWrapped now, so a bar that does
// not fit gets another ROW instead of losing its tail — which is why the width
// assertion below can be made at all.
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
					line = strings.TrimRight(line, " ")
					if line == "" {
						continue
					}
					if got := lipgloss.Width(line); got > screenBodyWidth(w) {
						t.Errorf("%s at %dx%d: the bar row %q is %d cells wide in a pane of "+
							"%d, so clampToBox cuts its tail and the keys on it go unnamed "+
							"while they go on working:\n%s", name, w, h, line, got,
							screenBodyWidth(w), shown)
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
// first line is the one carrying the fact — the height to resize to — so a
// notice that overflows loses the sentence about what the keys do and keeps the
// one that can be acted on.
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

// TestJDEForm_ANoticeThatWillNotFitSaysItWasCut: where the layer drops rows off
// its own refusal notice, the last line it keeps carries the ellipsis that says
// so.
//
// The notice is bounded in BOTH axes and the HEIGHT fact leads, so a pane of
// three rows keeps the height and two lines of the sentence under it. Cut
// clean, that sentence reads as a finished one — "Moving keys are held until it
// fits, so you come" is advice an operator would act on without knowing a
// clause is missing. Every other bound on these screens marks its cut; this is
// the one the operator is being asked to act on.
//
// WATCHED TO FAIL against the unmarked trim.
func TestJDEForm_ANoticeThatWillNotFitSaysItWasCut(t *testing.T) {
	cut := 0
	for _, w := range jdePaneWidths {
		for _, h := range jdePaneHeights() {
			for _, barRows := range []int{2, 3, 4, 5, 6} {
				for _, headerRows := range []int{0, 1, 3} {
					g := jdeScreen{terminalWidth: w, terminalHeight: h}
					if !g.tooShort(barRows, headerRows) {
						continue
					}
					// The notice the layer would draw with all the room it
					// wants, against the one it really has.
					full := jdeTooShort(screenBodyWidth(w), 99, h, jdeTooShortRows(barRows, headerRows))
					drawn := g.tooShortNotice(barRows, headerRows)
					if len(strings.Split(full, "\n")) <= len(strings.Split(drawn, "\n")) {
						continue // nothing was dropped
					}
					cut++
					lines := strings.Split(drawn, "\n")
					if last := lines[len(lines)-1]; !strings.HasSuffix(last, "…") {
						t.Errorf("at %dx%d (bar %d, header %d) the notice loses %d line(s) and "+
							"its last row %q ends as a finished sentence:\n%s",
							w, h, barRows, headerRows,
							len(strings.Split(full, "\n"))-len(lines), last, drawn)
					}
					for _, line := range lines {
						if got := lipgloss.Width(line); got > screenBodyWidth(w) {
							t.Errorf("at %dx%d the marked notice line %q is %d cells in a pane of %d",
								w, h, line, got, screenBodyWidth(w))
						}
					}
				}
			}
		}
	}
	if cut == 0 {
		t.Error("no notice was ever taller than the pane it replaces, so this sweep " +
			"asserted nothing about the mark it is named for")
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
// The pinned header's essential rows
// ---------------------------------------------------------------------------

// jdeHeaderFrames is the name of every layer frame that takes a pinned header,
// and the position of that argument, read out of the layer's own declarations.
//
// Derived rather than listed for the reason TestJDEForm_EveryStatusRowComesFromTheLayer
// derives its frame set the same way: a frame variant added later is swept the
// moment it declares a `header jdeHeader` parameter, without anyone remembering
// to come back here.
func jdeHeaderFrames(t *testing.T) map[string]int {
	t.Helper()
	_, files := jdeParsePackage(t)
	frames := map[string]int{}
	for path, f := range files {
		if !jdeIsLayer(path) {
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Type.Params == nil {
				continue
			}
			i := 0
			for _, p := range fn.Type.Params.List {
				id, isIdent := p.Type.(*ast.Ident)
				for _, name := range p.Names {
					if isIdent && id.Name == "jdeHeader" && name.Name == "header" {
						frames[fn.Name.Name] = i
					}
					i++
				}
				if len(p.Names) == 0 {
					i++
				}
			}
		}
	}
	if len(frames) == 0 {
		t.Fatalf("no method in %s takes a `header jdeHeader` parameter, so the header "+
			"sweep below has nothing to derive its sites from. If the pinned header "+
			"stopped being a frame argument this derivation needs rewriting rather "+
			"than deleting", jdeLayerFile)
	}
	return frames
}

// jdeHeaderSites is every place in the package's own non-test source that hands
// a frame a header that is not nil — keyed `<receiver type>/<method>` of the
// function making the call, which is the key shape jdeScreenStates uses.
//
// This roster used to be scoped to jdePickList literals, and that is exactly
// why the SECOND instance of the header-trim defect went uncaught: the order
// pad builds its header by hand, so the picker sweep never looked at it and the
// ⚠ saying lines had been dropped from the pad went missing at 80x12 and 80x13
// with nothing to report it. A roster narrower than the rule it enforces is the
// same omission as a hand-kept one.
func jdeHeaderSites(t *testing.T) map[string]bool {
	t.Helper()
	frames := jdeHeaderFrames(t)
	_, files := jdeParsePackage(t)
	out := map[string]bool{}
	for path, f := range files {
		if jdeIsLayer(path) {
			continue // the layer DECLARES the frames; it calls them with nil
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 || fn.Body == nil {
				continue
			}
			recv := jdeRecvName(fn)
			if recv == "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pos, ok := frames[sel.Sel.Name]
				if !ok || pos >= len(call.Args) {
					return true
				}
				if id, ok := call.Args[pos].(*ast.Ident); ok && id.Name == "nil" {
					return true // a frame with no pinned header at all
				}
				out[recv+"/"+fn.Name.Name] = true
				return true
			})
		}
	}
	if len(out) == 0 {
		t.Fatal("no function in this package hands a frame a pinned header, so the " +
			"sweep below would pass vacuously. If pinned headers went away this " +
			"derivation needs rewriting rather than deleting")
	}
	return out
}

// jdeRecvName is a method's receiver type, pointer or not.
func jdeRecvName(fn *ast.FuncDecl) string {
	switch e := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// jdeHeaderCase is one header site, built in the state that reaches it, paired
// with the header its own builder produces there.
//
// `header` calls the REAL builder on the REAL state rather than re-deriving the
// rows, because the rank is a claim the builder makes and this sweep exists to
// hold it to that claim. It is written per site because the builders are
// unexported methods with different names and no interface in common; what may
// NOT be written by hand is the SET OF KEYS, which jdeHeaderSites derives and
// TestJDEForm_EveryHeaderSiteIsSwept compares against.
type jdeHeaderCase struct {
	mk func() Screen
	// after runs once the screen has been SIZED, for a state a resize destroys.
	// The receiving form clears its note on every WindowSizeMsg on purpose — a
	// note is an answer about a frame and a resize destroys the frame it was an
	// answer about — so the only way to sweep a header with a standing note in
	// it is to press the key that declines after the pane is known, which is
	// also the only way an operator ever sees one.
	after  func(Screen)
	header func(Screen) jdeHeader
}

// jdeHeaderCases builds every header site in a state that reaches its frame.
//
// The picker sites reuse jdeScreenStates' own builders, so the two rosters
// cannot describe different screens.
func jdeHeaderCases() map[string]jdeHeaderCase {
	states := jdeScreenStates()
	pick := func(state string, hdr func(Screen) jdeHeader) jdeHeaderCase {
		return jdeHeaderCase{mk: states[state], header: hdr}
	}
	return map[string]jdeHeaderCase{
		"AssetFormScreen/viewPick": pick("AssetFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*AssetFormScreen).pickView(); return h }),
		"AssetPartFormScreen/viewPick": pick("AssetPartFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*AssetPartFormScreen).pickView(); return h }),
		"AuthorizationGrantScreen/viewPick": pick("AuthorizationGrantScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*AuthorizationGrantScreen).pickView(); return h }),
		"CategoryFormScreen/viewPick": pick("CategoryFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*CategoryFormScreen).pickView(); return h }),
		"DisconnectFormScreen/viewPick": pick("DisconnectFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*DisconnectFormScreen).pickView(); return h }),
		"InventoryItemFormScreen/viewPick": pick("InventoryItemFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*InventoryItemFormScreen).pickView(); return h }),
		"InventoryItemFormScreen/viewKitPick": pick("InventoryItemFormScreen/kitPickView",
			func(s Screen) jdeHeader { h, _ := s.(*InventoryItemFormScreen).kitPickView(); return h }),
		"ItemSupplierFormScreen/viewPick": pick("ItemSupplierFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*ItemSupplierFormScreen).pickView(); return h }),
		"LocationFormScreen/viewPick": pick("LocationFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*LocationFormScreen).pickView(); return h }),
		"MaintenanceItemFormScreen/viewAssetPick": pick("MaintenanceItemFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*MaintenanceItemFormScreen).pickView(); return h }),
		"PowerBreakerFormScreen/viewPick": pick("PowerBreakerFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*PowerBreakerFormScreen).pickView(); return h }),
		"PowerCircuitFormScreen/viewPick": pick("PowerCircuitFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*PowerCircuitFormScreen).pickView(); return h }),
		"PowerOutletFormScreen/viewPick": pick("PowerOutletFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*PowerOutletFormScreen).pickView(); return h }),
		"PowerPanelFormScreen/viewPick": pick("PowerPanelFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*PowerPanelFormScreen).pickView(); return h }),
		"ProjectStorageFormScreen/viewSlotPick": pick("ProjectStorageFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*ProjectStorageFormScreen).pickView(); return h }),
		"StorageAssignFormScreen/viewPicker": pick("StorageAssignFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*StorageAssignFormScreen).pickView(); return h }),
		"StorageSlotFormScreen/viewPicker": pick("StorageSlotFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*StorageSlotFormScreen).pickView(); return h }),
		"StorageSlotGenerateScreen/viewPicker": pick("StorageSlotGenerateScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*StorageSlotGenerateScreen).pickView(); return h }),
		"ThermostatFormScreen/viewPick": pick("ThermostatFormScreen/pickView",
			func(s Screen) jdeHeader { h, _ := s.(*ThermostatFormScreen).pickView(); return h }),

		"PurchaseOrderDetailScreen/viewOrderPad": pick("PurchaseOrderDetailScreen/order pad",
			func(s Screen) jdeHeader { return s.(*PurchaseOrderDetailScreen).orderPadHeader() }),

		// The New PO screen pins the tallest header in the app: the supplier
		// row, the failure's unbounded detail, three optional attribution
		// values, and — on this phase — the screen's answer to the last
		// keypress. Built on the SOURCE CHOOSER because that is where all of
		// them are standing at once, with a 502's body under it, which is the
		// state the header floor has to hold in.
		"PurchaseOrderCreateScreen/View": {
			mk: func() Screen {
				s := poCreateStaged()
				// An EMPTY cart, so that the key pressed below really declines:
				// the essential row then carries the screen's answer to a
				// keypress rather than its standing note, which is the sentence
				// rule 1 depends on being drawn.
				s.lines = nil
				s.setErr(poSubmitFailWords, nginx502)
				return s
			},
			after: func(s Screen) {
				s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
			},
			header: func(s Screen) jdeHeader { return s.(*PurchaseOrderCreateScreen).headerLines() },
		},

		// The receiving form with a note standing AND a 502's detail under it:
		// the state its header is tallest in, and the one the header floor was
		// written for.
		"ReceiveFormScreen/View": {
			mk: func() Screen {
				return receivePaneFixture(func(s *ReceiveFormScreen) { s.failDetail = nginx502 })
			},
			after: func(s Screen) {
				s.Update(tea.KeyMsg{Type: tea.KeyEnter}) // declines: no quantity typed yet
			},
			header: func(s Screen) jdeHeader { return s.(*ReceiveFormScreen).headerLines() },
		},
		// Same shape one screen over: the add-line flow pins its answer to the
		// last keypress, with the failure detail riding under it.
		"PurchaseOrderAddLineScreen/View": {
			mk: func() Screen {
				s := NewPurchaseOrderAddLineScreen(Deps{}, poViewPO())
				s.note.text, s.note.level = "Scan or type an identifier.", StatusWarn
				s.failDetail = nginx502
				return s
			},
			header: func(s Screen) jdeHeader { return s.(*PurchaseOrderAddLineScreen).headerLines() },
		},
		// The delete confirm pins the ONE thing it may not be drawn without —
		// what is about to be destroyed — and it is the reason that frame has a
		// header at all: with the identity in the BODY, jdeLines gave it ground
		// first and from 80x10 to 80x16 the frame named nothing it would
		// destroy while the bar still read Ctrl-X=Delete line. Built on a line
		// whose name REACHES the row's bound and with a voided line's extra
		// context row standing, which is the state its header is tallest in.
		"PurchaseOrderEditScreen/viewDeleteLine": {
			mk: func() Screen {
				po := poDeletablePO()
				po.Items = append([]omsapi.PurchaseOrderItem{{
					ID:              "line-long",
					Description:     "M3×12 hex-head cap screw, A2-70 stainless, DIN 933, bright finish",
					QuantityOrdered: 250,
					EstimatedCost:   omsapi.DecimalString("31.25"),
					IsVoided:        true,
				}}, po.Items...)
				s := NewPurchaseOrderEditScreen(Deps{}, po)
				s.openLineEditor(0)
				s.lineFocus = poLineRowStatus
				s.openDeleteLine(0)
				return s
			},
			after: func(s Screen) {
				// A key the frame declines, so the note it answers with is
				// standing under the header while the sweep measures it.
				s.Update(tea.KeyMsg{Type: tea.KeyEnter})
			},
			header: func(s Screen) jdeHeader {
				v := s.(*PurchaseOrderEditScreen)
				return v.deleteHeader(v.po.Items[v.editLineIdx], v.bodyWidth())
			},
		},
		"ServiceStatusScreen/View": {
			mk:     func() Screen { return NewServiceStatusScreen(Deps{}) },
			header: func(s Screen) jdeHeader { h, _ := s.(*ServiceStatusScreen).render(); return h },
		},
	}
}

// jdeHeadersWithoutEssentials are the header sites that genuinely mark NO row
// essential, each with the reason.
//
// It is the same shape as po_create_phase_sweep_test.go's poPhasesWithoutKeys
// and exists for the same reason: absent and empty have to be different states.
// Without it a site could be made to pass by demoting the row that mattered,
// which is precisely the move jdeHeadRank exists to make visible; with it, a
// site that declares nothing essential has to be written down as such, and a
// site written down here that LATER declares one fails as a stale entry.
var jdeHeadersWithoutEssentials = map[string]string{
	"ServiceStatusScreen/View": "the header is a roll-up — service count, all-working or " +
		"degraded count, checked-at — and the body under it lists every service and its " +
		"own state, so an operator who loses the row loses a summary and no fact.",
}

// TestJDEForm_EveryHeaderSiteIsSwept: every pinned header in the app is one of
// the cases the header sweep walks, and every one of them says which of its
// rows the operator cannot do without.
func TestJDEForm_EveryHeaderSiteIsSwept(t *testing.T) {
	cases := jdeHeaderCases()
	sites := jdeHeaderSites(t)
	for site := range sites {
		if _, ok := cases[site]; !ok {
			t.Errorf("%s hands a frame a pinned header and has no case in "+
				"jdeHeaderCases, so no sweep ever asks which of its rows survive a "+
				"short pane. Add one in the state the operator reaches it in", site)
		}
	}
	for site := range cases {
		if !sites[site] {
			t.Errorf("jdeHeaderCases builds %s, which no longer hands a frame a pinned "+
				"header. A stale case is a sweep spending its time on a header nobody "+
				"draws while the one that replaced it goes unchecked", site)
		}
	}
	for site, reason := range jdeHeadersWithoutEssentials {
		if !sites[site] {
			t.Errorf("jdeHeadersWithoutEssentials excuses %s, which is not a header site. "+
				"A stale excuse silently exempts nothing and hides the next one", site)
		}
		if reason == "" {
			t.Errorf("%s is excused from having an essential row with no reason given", site)
		}
	}

	for site, c := range cases {
		if c.mk == nil {
			t.Errorf("the %s case has no builder, so every assertion about it is "+
				"vacuous — most likely it names a jdeScreenStates key that has moved", site)
			continue
		}
		s := c.mk()
		jdeRootAt(t, s, 80, 40)
		if c.after != nil {
			c.after(s)
		}
		header := c.header(s)
		if len(header) == 0 {
			t.Errorf("the %s case builds an empty header at 80x40, so the sweep would "+
				"assert nothing about it — the state it is built in does not reach the "+
				"frame that pins one", site)
			continue
		}
		essential := 0
		for _, row := range header {
			if row.Rank == jdeHeadEssential {
				essential++
			}
		}
		_, excused := jdeHeadersWithoutEssentials[site]
		if essential == 0 && !excused {
			t.Errorf("%s marks none of its %d header rows essential. Either one of them "+
				"IS the row the operator cannot act without — say so with "+
				"jdeHeadEssential — or none is, and that belongs in "+
				"jdeHeadersWithoutEssentials with the reason. Everything expendable is "+
				"how a header passes this sweep while dropping the row that mattered",
				site, len(header))
		}
		if essential > 0 && excused {
			t.Errorf("%s is listed in jdeHeadersWithoutEssentials and marks %d row(s) "+
				"essential. The excuse is stale, and while it stands the sweep's own "+
				"vacuity guard is switched off for this site", site, essential)
		}
	}
}

// TestJDEForm_EveryEssentialHeaderRowIsOnThePane: the rows a builder said the
// operator cannot act without are drawn at every size the frame is drawn at.
//
// This is the check the rank exists to make possible. Before it, the only thing
// a sweep could ask about a pinned header was "is SOMETHING left of it", which
// both instances of the defect satisfied: the picker kept its "Category" title
// while the filter box the operator was typing into went, and the order pad kept
// its "Order pad" heading while the ⚠ saying lines had been dropped from the pad
// went — at 80x12 and 80x13, on a pad whose text is already on the clipboard, so
// what the operator pastes is short and nothing on the screen says so.
//
// Inferring which row matters from POSITION is what caused that, and a per-site
// table in a test is the hand-kept roster this project keeps being bitten by. So
// the builder declares it, in production, beside the row — and this walks every
// derived site at every width and every drawable height, asserting on the
// CLIPPED Root.View() because the screen's own string is not what the operator
// reads.
func TestJDEForm_EveryEssentialHeaderRowIsOnThePane(t *testing.T) {
	asserted := 0
	for site, c := range jdeHeaderCases() {
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := c.mk()
				r := jdeRootAt(t, s, w, h)
				if c.after != nil {
					c.after(s)
				}
				if jdeBarOf(s.View()) == nil {
					continue // a frame the layer refused; it draws no header rows at all
				}
				shown := r.View()
				for _, row := range c.header(s) {
					if row.Rank != jdeHeadEssential {
						continue
					}
					want := truncateVisible(strings.TrimRight(row.Text, " "), screenBodyWidth(w))
					if want == "" {
						continue
					}
					asserted++
					if !strings.Contains(shown, want) {
						t.Errorf("%s at %dx%d drops the header row it marked essential — "+
							"%q is not on the pane, so the operator is acting on a screen "+
							"that is not telling them what it said it could not do "+
							"without:\n%s", site, w, h, row.Text, shown)
					}
				}
			}
		}
	}
	if asserted == 0 {
		t.Error("no header site drew an essential row at any supported size, so this " +
			"sweep asserted nothing. Either every builder has stopped marking rows " +
			"essential or every frame is being refused; both need this test rewritten " +
			"rather than deleted")
	}
}

// jdePagingTokens are the bar tokens that spell PAGING and nothing else,
// derived from the package's own transcription tables rather than listed.
//
// Derived because the bars do not agree on how to spell the pair: most write
// "PgUp/PgDn", and the receiving form's all-units-answered frame writes "PgUp"
// alone, because it has a unit to step BACK to and none to step forward to. A
// sweep that looked for the literal "PgUp/PgDn" would read that frame as naming
// no pager while its PgUp moves the cursor, and report a screen that is honest
// as a violation — which is how a sweep gets weakened to accommodate a site.
//
// "spells paging and nothing else" is the test: a token mapping to a keystroke
// outside the pair is some other key that happens to share a name.
func jdePagingTokens() map[string]bool {
	out := map[string]bool{}
	for token := range jdeMoveTokens {
		keys, ok := jdeResolveBarToken(token)
		if !ok || len(keys) == 0 {
			continue
		}
		paging := true
		for _, k := range keys {
			if k != "pgup" && k != "pgdown" {
				paging = false
			}
		}
		if paging {
			out[token] = true
		}
	}
	return out
}

// jdeBarOffersPaging reports whether a DRAWN bar claims a paging key.
func jdeBarOffersPaging(bar []string) bool {
	paging := jdePagingTokens()
	for _, token := range jdeBarTokens(bar) {
		if paging[token] {
			return true
		}
	}
	return false
}

// TestJDEForm_ThePagingPairIsNamedExactlyWhereAPageMoves: on every columnar
// screen, at every pane the layer draws a frame into, the bar names a paging key
// IF AND ONLY IF pressing one moves the operator's place.
//
// This is the bar-honesty rule (AGENTS.md) over the one key pair the layer can
// answer for, and it is stated as the BICONDITIONAL because the claim fails in
// two directions and a check written for one catches neither of the other:
//
//   - NAMED AND DEAD. The packaging-chain list advertised PgUp/PgDn the moment
//     its rungs outgrew the window and updateChainPhase bound neither key.
//   - BOUND AND UNNAMED. Every other columnar sheet bound the pair
//     unconditionally in its key switch while pageRow gated only on whether the
//     frame was DRAWN — so on any pane tall enough to hold the whole body the
//     bar rightly said nothing about paging and PgDn still walked the cursor.
//     Measured before the gate moved into pageRow: 3254 of 7102 drawn
//     (screen, width, height) triples, across 47 of the cases here — and not
//     one violation in the other direction, so every bar that named the pair
//     was telling the truth and every one that stayed silent was not.
//
// DERIVED, because thirty-two hand-edits do not keep a class closed and the next
// sheet added reopens it: the cases come from jdePaneCases (every type embedding
// jdeScreen, plus its extra states), the heights from jdePaneHeights, and what
// counts as a paging token from the bar tables. No screen is named.
//
// The heights the layer REFUSES are outside it, for the reason the other sweeps
// in this file give: no bar is drawn there to make a claim with, and the frame's
// answer to every key is the notice.
func TestJDEForm_ThePagingPairIsNamedExactlyWhereAPageMoves(t *testing.T) {
	drawn, named, moves := 0, 0, 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := mk()
				jdeRootAt(t, s, w, h)
				bar := jdeBarOf(s.View())
				if bar == nil {
					continue // refused: the notice replaces the bar
				}
				drawn++
				offers := jdeBarOffersPaging(bar)
				if offers {
					named++
				}

				// Both keys, in sequence and with no reset: the claim is that
				// SOME page moves, and pgdown is the one with room from a
				// cursor resting at the top.
				before := jdePlaceOf(s)
				if next, _ := s.Update(poPickerKeyMsg("pgdown")); next != nil {
					s = next
				}
				moved := !reflect.DeepEqual(before, jdePlaceOf(s))
				before = jdePlaceOf(s)
				if next, _ := s.Update(poPickerKeyMsg("pgup")); next != nil {
					s = next
				}
				moved = moved || !reflect.DeepEqual(before, jdePlaceOf(s))
				if moved {
					moves++
				}

				if offers != moved {
					t.Errorf("%s at %dx%d: the bar names a paging key = %v but a page "+
						"moved the operator's place = %v\n%s",
						name, w, h, offers, moved, strings.Join(bar, "\n"))
				}
			}
		}
	}
	if drawn == 0 {
		t.Fatal("no columnar screen drew a frame at any supported size, so this sweep " +
			"asserted nothing")
	}
	// Both halves have to be REACHED or the biconditional is one implication
	// with the other side never exercised — the vacuous-fixture rule at the
	// level of the sweep rather than of a fixture.
	if named == 0 {
		t.Fatalf("no bar named a paging key at any of the %d drawn panes, so the "+
			"named-and-acts half was never exercised", drawn)
	}
	if named == drawn {
		t.Fatalf("every one of the %d drawn panes named a paging key, so the "+
			"unnamed-and-inert half was never exercised", drawn)
	}
	if moves == 0 {
		t.Fatalf("no page moved anything at any of the %d drawn panes — poPickerKeyMsg "+
			"or Update stopped being reached, and this sweep would pass over a "+
			"screen that pages when it says it does not", drawn)
	}
}

// TestJDEForm_AnUnsizedTerminalPagesAsItAlwaysHas: a screen driven without a
// WindowSizeMsg answers PgUp/PgDn exactly as it did before paging grew a scroll
// gate.
//
// AN UNSIZED TERMINAL IS NOT A SHORT PANE, and the distinction is the whole of
// this check. bodyScrollsForBar answers FALSE when there is no pane — not
// because the body fits, but because there is no window for it to overflow — so
// a gate that took that answer at face value would make every pager in the
// program decline on an unsized screen. That would also falsify frameDrawn's
// documented property, which is that an unsized terminal answers TRUE and keeps
// such a screen behaving exactly as it always has: the layer's standing answer
// for no pane is "draw the whole thing and let clampToBox decide".
//
// Stated as an IMPLICATION over the derived cases rather than against one
// fixture: whatever pages on a real pane must still page with no pane at all.
// Removing pageRow's paneRows() short-circuit fails it on every case that pages.
//
// jdeUnsizedDeclineCases are the cases this implication does NOT hold for, with
// the reason, so "absent" and "excused" stay different states — and a stale
// entry fails as loudly as a missing one.
//
// All THREE directions fail rather than one, because a roster in a test is only
// worth keeping if being wrong about it is loud: a case that declines when
// unsized and is not listed fails; a listed case that pages when unsized fails;
// and a listed case that does not page at any height fails too, since it is
// excusing behaviour that no longer exists. Without that third one an entry
// could outlive the screen it was written about and go on passing in silence,
// which is the shape of hand-kept roster this project keeps being bitten by.
//
// They are one class rather than a list of accidents: the sheets that spell the
// scroll conjunction THEMSELVES instead of getting it from pageRow. Two of them
// scroll an OFFSET, which deliberately has no combined primitive because its two
// questions are asked of different bars; the rest feed the answer back into the
// bar's own contents and so must measure the ceiling where they build it. All of
// them asked bodyScrollsForBar directly before this work and still do, so their
// unsized answer is exactly what it has always been — which is the property this
// check is about. Nothing here is a pageRow site.
var jdeUnsizedDeclineCases = map[string]string{
	"PurchaseOrderDetailScreen":           "po_detail's sheetMoves — an OFFSET, not a cursor",
	"PurchaseOrderDetailScreen/order pad": "po_detail's padMoves — an OFFSET, not a cursor",
	"ReceiveFormScreen":                   "receive_form's qtyPagesFor",
	"PurchaseOrderAddLineScreen/choose":   "po_add_line's choosePages",
	"PurchaseOrderAddLineScreen/confirm":  "po_add_line's confirmScrolls — an OFFSET",
	"PurchaseOrderAttachmentsScreen": "po_attachments guards on listNames(\"PgUp/PgDn\"), " +
		"which reads the bar's own claim",
	"PurchaseOrderCreateScreen":                "po_create's bodyPagesFor",
	"PurchaseOrderCreateScreen/source chooser": "po_create's bodyPagesFor",
	"PurchaseOrderCreateScreen/review":         "po_create's bodyPagesFor",
	"PurchaseOrderCreateScreen/item picker":    "po_create's bodyPagesFor",
	"PurchaseOrderCreateScreen/asset picker":   "po_create's bodyPagesFor",
	"PurchaseOrderCreateScreen/reorder picker": "po_create's bodyPagesFor",
}

func TestJDEForm_AnUnsizedTerminalPagesAsItAlwaysHas(t *testing.T) {
	pagesUnsized := 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk

		// The antecedent is the UNION over every height the frame is drawn at,
		// not one height: most bodies fit a tall pane, so a single tall
		// reading would leave the implication vacuous over nearly every case.
		sizedPages, at := false, 0
		for _, h := range jdePaneHeights() {
			sized := mk()
			jdeRootAt(t, sized, 80, h)
			beforeSized := jdePlaceOf(sized)
			if next, _ := sized.Update(poPickerKeyMsg("pgdown")); next != nil {
				sized = next
			}
			if !reflect.DeepEqual(beforeSized, jdePlaceOf(sized)) {
				sizedPages, at = true, h
				break
			}
		}

		// No WindowSizeMsg at all — the state every screen is in before the
		// terminal has told the program how big it is.
		unsized := mk()
		before := jdePlaceOf(unsized)
		if next, _ := unsized.Update(poPickerKeyMsg("pgdown")); next != nil {
			unsized = next
		}
		unsizedPages := !reflect.DeepEqual(before, jdePlaceOf(unsized))
		if unsizedPages {
			pagesUnsized++
		}

		reason, excused := jdeUnsizedDeclineCases[name]
		switch {
		case excused && !sizedPages:
			t.Errorf("%s is recorded in jdeUnsizedDeclineCases (%q) but it does not page "+
				"at ANY height it draws at, so there is nothing here to excuse. An "+
				"entry describing no behaviour is a stale exception that passes in "+
				"silence", name, reason)
		case sizedPages && !unsizedPages && !excused:
			t.Errorf("%s: PgDn pages on an 80x%d pane and does nothing on an UNSIZED "+
				"terminal. There is no pane there to be too short, so the geometric "+
				"questions cannot be answered from geometry and the layer's standing "+
				"answer is to act", name, at)
		case excused && unsizedPages:
			t.Errorf("%s is recorded in jdeUnsizedDeclineCases (%q) but it DOES page on "+
				"an unsized terminal. A stale exception is a case excused from the "+
				"check it passes", name, reason)
		}
	}
	if pagesUnsized == 0 {
		t.Fatal("no columnar screen pages at all on an unsized terminal, so this check " +
			"asserted nothing about the state it is named for")
	}
}
