// The purchasing VIEWING screens on the columnar "JD Edwards" layer (sc-h412,
// purchasing view slice): the PO detail sheet and its four sub-surfaces
// (mark-shipped, void, mark-delivered, order pad), and the attachments screen
// with its grid, upload sheet and delete confirm.
//
// These hold all of them to the same contract the pilot's own tests hold PO
// edit to, plus one this slice adds:
//
//	the sheet is columnar   — every reading hangs off ONE leader column
//	the bar is persistent   — pinned to the bottom of the pane on every frame
//	the bar is honest       — a key on it works here, and a key that works
//	                          here is on it
//	IT FITS AT 80           — the canonical JD Edwards World width, checked on
//	                          the CLIPPED Root.View() render and not on the
//	                          screen's own unclipped output. That distinction is
//	                          the whole reason this file exists: a caveat has
//	                          already shipped in this project that was cut at 80
//	                          columns while its test passed, because the test
//	                          read the string the screen produced rather than
//	                          the one Root.View() puts on the terminal.
package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// poViewWidths are the widths every purchasing viewing screen must be legible
// at. 80 is the one that matters — it is the width the interface is modelled on
// and the one that clips — and the other two are there so a layout cannot be
// tuned for 80 by hard-coding it.
var poViewWidths = []int{80, 100, 120}

const poViewHeight = 34

// barHas reports whether the bar names this key with this label.
func barHas(items []actionBarItem, key, label string) bool {
	for _, it := range items {
		if it.Key == key && it.Label == label {
			return true
		}
	}
	return false
}

// poViewPO is a purchase order that exercises every band of the detail sheet:
// both levels of association, an agreement, terms with a schedule, notes, a
// received line, a voided line and an attachment.
//
// The received line and the voided line are SEPARATE lines on purpose. They
// were once the same line, and because poLineFlag prefers "[voided]" — which is
// exactly the 8 columns the old grid budgeted — the received flag "✓ received"
// (10 columns) was never rendered at any width, so the row it overran the pane
// by two columns on went unnoticed.
func poViewPO() *omsapi.PurchaseOrder {
	sent := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	days := 12
	group := 3
	return &omsapi.PurchaseOrder{
		ID:                    "po-1",
		Number:                "PO-2026-0042",
		Status:                "confirmed",
		StatusLabel:           "Confirmed",
		SupplierDetails:       "Acme Fasteners & Industrial Supply Co.",
		SupplierOrderNumber:   "SUP-88213",
		SalesOrderNumber:      "SO-4417",
		OrderDate:             time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		ExpectedDeliveryDate:  "2026-08-20",
		CreatedAt:             time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		UpdatedAt:             time.Date(2026, 8, 2, 9, 15, 0, 0, time.UTC),
		DaysSinceOrdered:      &days,
		CreatedByUsername:     "alice",
		SentByUsername:        "bob",
		SentAt:                &sent,
		Priority:              "urgent",
		PaymentTerms:          "net_30",
		FreightTerms:          "fob_destination",
		PaymentSchedule:       &omsapi.POPaymentSchedule{Amount: omsapi.DecimalString("1234.56"), DueDate: "2026-08-31", Basis: "Net 30 from order date"},
		EstimatedTotal:        omsapi.DecimalString("1234.56"),
		ActualTotal:           omsapi.DecimalString("240.00"),
		TotalItems:            3,
		TotalQuantity:         11,
		TotalReceivedQuantity: 6,
		Currency:              "USD",
		Notes:                 "Deliver to the loading dock; the front desk cannot sign for pallets.",
		SupplierAgreementRef:  &omsapi.SupplierAgreementRef{ID: 4, Name: "2026 nonprofit pricing"},
		WorkOrderRef:          &omsapi.WorkOrderRef{ShortID: "WO-1A2B", DisplayTitle: "Replace drive belt"},
		OwningGroup:           &group,
		OwningGroupRef:        &omsapi.OwningGroupRef{ID: 3, Name: "Woodshop"},
		Items: []omsapi.PurchaseOrderItem{
			{
				ID: "line-1", Description: "M3 hex bolt, stainless",
				ItemType: "item_supplier", ItemDetails: map[string]any{"sku": "M3-HEX-BOLT-SS", "name": "M3 hex bolt"},
				QuantityOrdered: 5, QuantityPending: 5,
				UnitCostOrdered:      omsapi.DecimalString("10.0000"),
				EstimatedCost:        omsapi.DecimalString("50.00"),
				ExpectedShipmentDate: "2026-08-10",
				WorkOrderRef:         &omsapi.WorkOrderRef{ShortID: "WO-9Z8Y", DisplayTitle: "Lathe PM"},
				Notes:                "substitute A2 only with approval",
			},
			{
				ID: "line-2", Description: "Gadget",
				QuantityOrdered: 2, QuantityReceived: 2, IsFullyReceived: true,
				UnitCostOrdered:    omsapi.DecimalString("10.0000"),
				UnitCostActual:     omsapi.DecimalString("12.0000"),
				EstimatedCost:      omsapi.DecimalString("20.00"),
				ActualCost:         omsapi.DecimalString("24.00"),
				ActualShipmentDate: "2026-08-05",
				IsVoided:           true, VoidReason: "supplier discontinued the part",
			},
			{
				ID: "line-3", Description: "Bracket",
				QuantityOrdered: 4, QuantityReceived: 4, IsFullyReceived: true,
				UnitCostOrdered:      omsapi.DecimalString("2.5000"),
				EstimatedCost:        omsapi.DecimalString("10.00"),
				ExpectedShipmentDate: "2026-08-06",
				ActualShipmentDate:   "2026-08-06",
			},
		},
		Attachments: []omsapi.PurchaseOrderAttachment{{
			ID: 1, FileName: "sales-order-confirmation.pdf",
			Description:    "supplier's countersigned confirmation",
			UploadedAt:     time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC),
			UploadedByName: "alice",
			FileURL:        "https://oms.example.org/media/po/sales-order-confirmation.pdf",
		}},
	}
}

// poViewRoot puts a screen in a Root sized to `width`, which is what makes the
// render CLIPPED: Root.View() runs clampToBox over the joined content.
func poViewRoot(t *testing.T, screen Screen, width int) Root {
	t.Helper()
	r := newTestRoot(screen)
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: poViewHeight})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after
}

// poDetailAt builds a loaded detail screen sized for `width`.
func poDetailAt(t *testing.T, width int) (*PurchaseOrderDetailScreen, Root) {
	t.Helper()
	s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	s.loading = false
	s.po = poViewPO()
	return s, poViewRoot(t, s, width)
}

// poAttachAt builds a loaded attachments screen sized for `width`.
func poAttachAt(t *testing.T, width int) (*PurchaseOrderAttachmentsScreen, Root) {
	t.Helper()
	s := NewPurchaseOrderAttachmentsScreen(Deps{}, poViewPO())
	return s, poViewRoot(t, s, width)
}

// ---------------------------------------------------------------------------
// It fits — asserted on the clipped render
// ---------------------------------------------------------------------------

// poSeenWhileScrolling pages the sheet from the top to the bottom and reports
// whether `want` ever lands in the CLIPPED render — the string the terminal
// actually shows, not the one the screen produced.
func poSeenWhileScrolling(s *PurchaseOrderDetailScreen, r Root, want string) bool {
	s.scroll = 0
	for i := 0; i <= s.sheetLines().Len(); i++ {
		if strings.Contains(r.View(), want) {
			return true
		}
		before := s.scroll
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if s.View(); s.scroll == before {
			break // rested at the end
		}
	}
	return strings.Contains(r.View(), want)
}

// TestPOView_ReadingsSurviveTheClip: every reading the sheet draws is still
// legible after Root.View() has clamped it to the content box. A row that
// overran the pane would lose its tail here and nowhere else — which is exactly
// how a caveat shipped truncated once already.
func TestPOView_ReadingsSurviveTheClip(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			for _, want := range []string{
				// Every band, and the readings from it that a narrow pane puts
				// at risk — the long ones.
				"Order", "Confirmed", "Acme Fasteners & Industrial Supply Co.",
				"2026 nonprofit pricing", "WO-1A2B — Replace drive belt", "Woodshop",
				"bob on 2026-08-02",
				"Identifiers", "SUP-88213", "SO-4417",
				"Dates", "2026-08-01", "2026-08-20", "12 days since ordered",
				"Totals", "$1234.56 USD", "received 6",
				"Terms", "Net 30", "FOB Destination",
				"Notes", "Deliver to the loading dock;", "pallets.",
				"Line items (3)", "PART M3-HEX-BOLT-SS", "type Inventory item",
				"ordered for: WO-9Z8Y", "[voided]", "supplier discontinued the part",
				// The received-but-not-voided flag: 10 columns, and the cell the
				// grid used to budget 8 for. A row that overruns loses its tail
				// here, so "received" landing whole is what says the budget is
				// right.
				"✓ received",
				"Attachments (1)", "sales-order-confirmation.pdf",
			} {
				if !poSeenWhileScrolling(s, r, want) {
					t.Errorf("%q never survives the clip at %d columns; last frame:\n%s", want, width, r.View())
				}
			}
		})
	}
}

// TestPOView_BarSurvivesTheClip: every key the bar names is still readable at
// every width. The bar is the only place an operator learns what works, so a
// key whose label lost its tail to the pane edge is a key they cannot use — and
// the whole reason the bar wraps rather than being cut.
func TestPOView_BarSurvivesTheClip(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			out := r.View()
			for _, it := range s.sheetBar() {
				if !strings.Contains(out, it.Key+"="+it.Label) {
					t.Errorf("bar entry %s=%s was clipped out of the %d-column render:\n%s",
						it.Key, it.Label, width, out)
				}
			}
		})
	}
}

// TestPOView_OrderPadSurvivesTheClip: the pad's chrome says what was built and
// what was left out, and the omitted-line warning is the piece that runs long.
func TestPOView_OrderPadSurvivesTheClip(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			s.orderPad = true
			s.orderPadExport = &omsapi.OrderPadExport{
				Text:       "M3-HEX-BOLT-SS\t5\nGADGET-LONG-PART-NUMBER-0001\t2",
				Supplier:   "Acme Fasteners & Industrial Supply Co.",
				Filename:   "PO-2026-0042-order.csv",
				LineCount:  2,
				MissingSku: []string{"Gadget", "Bracket"},
			}
			out := r.View()
			for _, want := range []string{"Order pad", "2 lines", "2 lines no supplier part #", "M3-HEX-BOLT-SS", "Enter=Copy", "Esc=Close"} {
				if !strings.Contains(out, want) {
					t.Errorf("%q was clipped out of the %d-column pad:\n%s", want, width, out)
				}
			}
		})
	}
}

// TestPOView_AttachmentGridSurvivesTheClip: the grid's file names, dates and
// readings, on the clipped render.
func TestPOView_AttachmentGridSurvivesTheClip(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			_, r := poAttachAt(t, width)
			out := r.View()
			for _, want := range []string{
				"Attachments (1)", "sales-order-confirmation.pdf", "2026-08-02",
				"supplier's countersigned confirmation", "by alice",
				"Enter=Upload", "Ctrl-X=Delete", "r=Refresh",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("%q was clipped out of the %d-column grid:\n%s", want, width, out)
				}
			}
		})
	}
}

// TestPOView_ModalsSurviveTheClip: the four sub-surfaces the sheet opens — and
// the attachment screen's upload sheet and delete confirm — carry prompts,
// caveats and hints that are longer than an 80-column pane. Every one of them
// still has to reach the terminal, which is what jdeFitRow's fold is for.
func TestPOView_ModalsSurviveTheClip(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			ship, r := poDetailAt(t, width)
			ship.openShipForm()
			assertClipped(t, r.View(), width, "mark shipped",
				"Mark item shipped", "Line #", "Shipment date",
				"YYYY-MM-DD", "blank is today", "Enter=Mark shipped", "Esc=Cancel", "UP/DN=Fields")

			void, r := poDetailAt(t, width)
			void.openVoidForm()
			assertClipped(t, r.View(), width, "void order",
				"Void purchase order", "Reason", "cascades to every line", "undone.",
				"Enter=Void order", "Esc=Cancel")

			deliver, r := poDetailAt(t, width)
			deliver.openDeliverForm()
			assertClipped(t, r.View(), width, "mark delivered",
				"Mark delivered", "Delivery date", "Tracking #", "Carrier", "Receipt notes",
				"Receives every pending quantity", "Enter=Mark delivered", "UP/DN=Fields")

			upload, r := poAttachAt(t, width)
			upload.openUpload()
			assertClipped(t, r.View(), width, "upload sheet",
				"Upload attachment", "File path", "Description",
				"a path on this machine", "optional", "Enter=Upload", "Esc=Cancel")

			confirm, r := poAttachAt(t, width)
			confirm.confirmingDelete = true
			assertClipped(t, r.View(), width, "delete confirm",
				"Delete attachment", "sales-order-confirmation.pdf", "Removes the file", "undone,",
				"Enter=Delete", "Esc=Cancel")
		})
	}
}

// assertClipped checks every phrase is still in the CLIPPED render.
func assertClipped(t *testing.T, out string, width int, name string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s: %q was clipped out of the %d-column render:\n%s", name, w, width, out)
		}
	}
}

// poWideCellPO is poViewPO with a single line whose QUANTITY and COST are both
// wider than the columns the poGrid* constants budget for them — 10000 is five
// columns against four, "$12345.6700" is eleven against ten. OMS money arrives
// with either two or four decimals, so the wide cost is an ordinary order and
// not a contrived one.
func poWideCellPO() *omsapi.PurchaseOrder {
	po := poViewPO()
	po.Items = []omsapi.PurchaseOrderItem{{
		ID: "line-wide", Description: "M3 hex bolt, stainless",
		ItemDetails:      map[string]any{"sku": "M3-HEX-BOLT-SS"},
		QuantityOrdered:  10000,
		QuantityReceived: 10000,
		IsFullyReceived:  true,
		UnitCostOrdered:  omsapi.DecimalString("1.2345"),
		EstimatedCost:    omsapi.DecimalString("12345.6700"),
		ActualCost:       omsapi.DecimalString("12345.6700"),
		// Comfortably in the future on purpose: poShipTokens repeats an overdue
		// or imminent ship-by as a reading under the row, and that second copy
		// would hide a date the GRID had lost its tail off.
		ExpectedShipmentDate: "2027-03-15",
	}}
	return po
}

// poScrollToLineGrid pages the sheet until the line-item grid is in frame, so a
// caller that renders ONE frame renders the one with the grid on it.
func poScrollToLineGrid(s *PurchaseOrderDetailScreen, want string) {
	for i := 0; i <= s.sheetLines().Len(); i++ {
		if strings.Contains(s.View(), want) {
			return
		}
		before := s.scroll
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if s.View(); s.scroll == before {
			return
		}
	}
}

// TestPOView_WideGridCellDoesNotCutTheCellsBesideIt: a cell wider than its
// budgeted column must widen nothing.
//
// poLineGridRow pads each fixed cell to its poGrid* constant and padCell never
// truncates, so an over-wide value used to push the whole ROW past the pane and
// clampToBox ate whatever was on the right of it — at 80 columns the ship date
// reached the terminal as "2026-08-1", a date silently missing a digit, and at
// 100 and 120 it was the flag that lost its tail. Every existing fixture used
// two- and three-digit quantities, which is why this was invisible.
//
// The assertions are on the CLIPPED render: the wide values themselves and the
// cells to their right all have to arrive whole.
func TestPOView_WideGridCellDoesNotCutTheCellsBesideIt(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			s.po = poWideCellPO()
			for _, want := range []string{
				"10000",       // the over-wide quantity, whole
				"$12345.6700", // the over-wide cost, whole
				"2027-03-15",  // the ship date, which sits to their right
				"✓ received",  // and the flag, which sits right of that
			} {
				if !poSeenWhileScrolling(s, r, want) {
					t.Errorf("%q never survives the clip at %d columns; last frame:\n%s", want, width, r.View())
				}
			}
		})
	}
}

// TestPOView_TypedValueLongerThanItsFieldStaysInThePane: an operator typing a
// path longer than the field must still see what they are typing.
//
// jdeFitRow sizes the row's FILL, not the box behind it, and a bubbles box left
// at Width 0 has no scrolling viewport — it renders the whole value. A 60-column
// path at 80 columns therefore drew an ~80-column row into a 51-column pane,
// clampToBox cut the tail, and the caret — which sits at the tail — went off the
// screen. Every existing test used values short enough to fit, which is why it
// was invisible.
//
// The assertion is made on the CLIPPED render, and it is that ONE line of it
// carries both the label and the end of the value: a row that overran would
// still show the label (the pane cuts the tail) but would have lost the tail
// the caret is standing on.
func TestPOView_TypedValueLongerThanItsFieldStaysInThePane(t *testing.T) {
	// Wider than the 34-column field at every width under test, so the box has
	// to scroll rather than merely fit.
	const path = "/home/operator/scans/2026-08/incoming/purchase-order-0042.pdf"
	const head, tail = "/home/operator", "order-0042.pdf"
	if len(path) <= 34 {
		t.Fatalf("fixture path is %d columns, which fits the field — it proves nothing", len(path))
	}

	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poAttachAt(t, width)
			s.openUpload()
			// One burst of runes and no Enter: the same shape a barcode scanner
			// delivers, and what a paste of a path looks like.
			s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(path)})

			out := r.View()
			var row string
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, "File path") {
					row = line
					break
				}
			}
			if row == "" {
				t.Fatalf("no File path row in the %d-column render:\n%s", width, out)
			}
			if !strings.Contains(row, tail) {
				t.Errorf("the caret end %q of the typed path was clipped off the %d-column row: %q",
					tail, width, row)
			}
			if strings.Contains(row, head) {
				t.Errorf("the %d-column row still shows the start %q of a %d-column path, so the box never scrolled: %q",
					width, head, len(path), row)
			}
		})
	}
}

// TestPOView_NoRowOverrunsThePane: the positive form of the same rule. Every
// line every purchasing viewing surface draws must fit the body width, at every
// width — because clampToBox cuts rather than wraps, and a cut line says
// nothing about having been cut.
func TestPOView_NoRowOverrunsThePane(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			budget := screenBodyWidth(width)
			for _, tc := range poViewSurfaces(t, width) {
				for _, line := range strings.Split(tc.view(), "\n") {
					if w := lipgloss.Width(line); w > budget {
						t.Errorf("%s: a line is %d wide but the pane is %d — it will be clipped: %q",
							tc.name, w, budget, line)
					}
				}
			}
		})
	}
}

// TestPOView_BarIsPinnedToTheBottom: "persistent" means the bar is on the same
// rows of the pane on every frame. A body allowed to set its own height would
// walk the bar up and down as the order gained a band or the pad gained a
// warning — and on a viewing screen it would also walk with the scroll.
func TestPOView_BarIsPinnedToTheBottom(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			for _, tc := range poViewSurfaces(t, width) {
				lines := strings.Split(tc.view(), "\n")
				if want := screenBodyHeight(poViewHeight); len(lines) != want {
					t.Errorf("%s: view is %d rows, want the pane's budget of %d", tc.name, len(lines), want)
					continue
				}
				rule := lines[len(lines)-1-tc.barKeyRows()]
				if rule == "" || strings.Trim(rule, "-") != "" {
					t.Errorf("%s: the row above the keys should be the bar's rule, got %q", tc.name, rule)
				}
			}
		})
	}
}

// TestPOView_ScrollDoesNotMoveTheBar drives the sheet down a line at a time and
// checks the frame keeps its shape all the way to the end.
func TestPOView_ScrollDoesNotMoveTheBar(t *testing.T) {
	s, _ := poDetailAt(t, 80)
	want := screenBodyHeight(poViewHeight)
	for i := 0; i < s.sheetLines().Len()+4; i++ {
		if got := len(strings.Split(s.View(), "\n")); got != want {
			t.Fatalf("after %d line scrolls the view is %d rows, want %d", i, got, want)
		}
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
}

// ---------------------------------------------------------------------------
// The surfaces
// ---------------------------------------------------------------------------

type poViewSurface struct {
	name string
	view func() string
	bar  []actionBarItem
	// header is the rows pinned above the scrollable body, which the bar's
	// height is measured against.
	width int
}

func (c poViewSurface) barKeyRows() int {
	return actionBarRowsFor(screenBodyWidth(c.width), c.bar) - 1
}

// poViewSurfaces is every frame the two screens can be in, already sized.
func poViewSurfaces(t *testing.T, width int) []poViewSurface {
	t.Helper()
	out := []poViewSurface{}

	sheet, _ := poDetailAt(t, width)
	out = append(out, poViewSurface{"detail sheet", sheet.View, sheet.sheetBar(), width})

	// The same sheet carrying a line whose quantity and cost are wider than the
	// columns budgeted for them: a grid row's width has to come from the values
	// on it, not from the constants.
	wide, _ := poDetailAt(t, width)
	wide.po = poWideCellPO()
	poScrollToLineGrid(wide, "10000")
	out = append(out, poViewSurface{"detail sheet, wide grid cells", wide.View, wide.sheetBar(), width})

	ship, _ := poDetailAt(t, width)
	ship.openShipForm()
	out = append(out, poViewSurface{"mark shipped", ship.View,
		[]actionBarItem{{"Enter", "Mark shipped"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}, width})

	void, _ := poDetailAt(t, width)
	void.openVoidForm()
	out = append(out, poViewSurface{"void order", void.View,
		[]actionBarItem{{"Enter", "Void order"}, {"Esc", "Cancel"}}, width})

	deliver, _ := poDetailAt(t, width)
	deliver.openDeliverForm()
	out = append(out, poViewSurface{"mark delivered", deliver.View,
		[]actionBarItem{{"Enter", "Mark delivered"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}, width})

	pad, _ := poDetailAt(t, width)
	pad.orderPad = true
	pad.orderPadExport = &omsapi.OrderPadExport{
		Text:       "M3-HEX-BOLT-SS\t5\nGADGET-LONG-PART-NUMBER-0001\t2",
		Supplier:   "Acme Fasteners & Industrial Supply Co.",
		Filename:   "PO-2026-0042-order.csv",
		LineCount:  2,
		MissingSku: []string{"Gadget", "Bracket"},
	}
	out = append(out, poViewSurface{"order pad", pad.View, pad.orderPadBar(), width})

	list, _ := poAttachAt(t, width)
	out = append(out, poViewSurface{"attachment grid", list.View, list.listBar(), width})

	confirm, _ := poAttachAt(t, width)
	confirm.confirmingDelete = true
	out = append(out, poViewSurface{"delete confirm", confirm.View,
		[]actionBarItem{{"Enter", "Delete"}, {"Esc", "Cancel"}}, width})

	upload, _ := poAttachAt(t, width)
	upload.openUpload()
	out = append(out, poViewSurface{"upload sheet", upload.View,
		[]actionBarItem{{"Enter", "Upload"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}, width})

	// The same sheet with a value LONGER than its field, because an empty box
	// and a box holding a 61-column path are different rows and only the second
	// one can overrun.
	typed, _ := poAttachAt(t, width)
	typed.openUpload()
	typed.Update(tea.KeyMsg{Type: tea.KeyRunes,
		Runes: []rune("/home/operator/scans/2026-08/incoming/purchase-order-0042.pdf")})
	out = append(out, poViewSurface{"upload sheet, long path", typed.View,
		[]actionBarItem{{"Enter", "Upload"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}, width})

	return out
}

func widthName(w int) string {
	switch w {
	case 80:
		return "80col"
	case 100:
		return "100col"
	}
	return "120col"
}

// ---------------------------------------------------------------------------
// The bar is honest
// ---------------------------------------------------------------------------

// TestPOView_SheetBarNamesEveryKeyThatWorks: the contract that replaced the
// letter accelerators. Every command key the sheet acts on is on the bar, and
// every key on the bar does something.
func TestPOView_SheetBarNamesEveryKeyThatWorks(t *testing.T) {
	s, _ := poDetailAt(t, 100)
	s.po.Status = "draft" // the one status that offers Send
	bar := s.sheetBar()
	for _, want := range [][2]string{
		{"Enter", "Receive"}, {"Esc", "Back"}, {"UP/DN", "Scroll"},
		{"PgUp/PgDn", "Page"}, {"Home/End", "Top/End"},
		{"E", "Edit"}, {"A", "Files"}, {"s", "Send"}, {"S", "Ship line"},
		{"x", "Order pad"}, {"v", "Void"}, {"r", "Refresh"},
	} {
		if !barHas(bar, want[0], want[1]) {
			t.Errorf("the sheet's bar should name %q=%q: %+v", want[0], want[1], bar)
		}
	}
	// A draft PO cannot be confirmed or delivered, so those keys are not named.
	for _, deny := range []string{"c", "d"} {
		for _, it := range bar {
			if it.Key == deny {
				t.Errorf("a draft PO should not offer %q: %+v", deny, bar)
			}
		}
	}
}

// TestPOView_RetiredKeysDoNothing: the aliases the TextScroller era carried —
// j/k, g/G, ctrl+d/ctrl+u — and the letters the reduced scheme retired are not
// on the bar, so they must not act. A key that works while nothing names it is
// the failure the bar exists to prevent, and on a scanner-driven terminal it is
// also a burst of barcode characters firing commands.
func TestPOView_RetiredKeysDoNothing(t *testing.T) {
	for _, k := range []string{"j", "k", "g", "G", "R", "u", "y", "n"} {
		s, _ := poDetailAt(t, 100)
		before := s.View()
		next, cmd := s.Update(poRuneKey(k))
		if next != Screen(s) {
			t.Errorf("%q navigated away from the detail sheet", k)
		}
		if cmd != nil {
			t.Errorf("%q fired a command while the bar names no such key", k)
		}
		if s.View() != before {
			t.Errorf("%q changed the sheet while the bar names no such key", k)
		}
	}
}

// TestPOView_AttachmentKeysMovedTogether: the attachment screen's own reduced
// scheme. Enter uploads, Ctrl-X deletes, arrows move — and the u / x / j / k
// letters it used to carry do nothing now that nothing names them.
func TestPOView_AttachmentKeysMovedTogether(t *testing.T) {
	s, _ := poAttachAt(t, 100)
	for _, k := range []string{"u", "x", "j", "k"} {
		s.phase = poAttachPhaseList
		s.confirmingDelete = false
		s.cursor = 0
		s.Update(poRuneKey(k))
		if s.phase != poAttachPhaseList || s.confirmingDelete || s.cursor != 0 {
			t.Errorf("%q still acts on the attachment grid (phase=%v confirm=%v cursor=%d)",
				k, s.phase, s.confirmingDelete, s.cursor)
		}
	}

	// Enter opens the upload sheet.
	s.phase = poAttachPhaseList
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != poAttachPhaseUpload {
		t.Errorf("Enter should open the upload sheet, phase = %v", s.phase)
	}

	// Ctrl-X arms the delete confirm, and Enter there is the delete.
	s.phase = poAttachPhaseList
	s.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
	if !s.confirmingDelete {
		t.Fatal("Ctrl-X should arm the delete confirm")
	}
	if !s.WantsRawInput() {
		t.Error("the confirm should claim raw input so its Enter is not the global one")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.confirmingDelete {
		t.Error("Esc should back out of the confirm")
	}
}

// TestPOView_OrderPadEnterCopies: the overlay's own action is putting the pad on
// the clipboard, so it is Enter and not the 'c' it used to be — 'c' is Confirm
// order one Esc away, and the same letter meaning two things two keystrokes
// apart is what the bar cannot explain.
func TestPOView_OrderPadEnterCopies(t *testing.T) {
	s, _ := poDetailAt(t, 100)
	s.orderPad = true
	s.orderPadExport = &omsapi.OrderPadExport{Text: "ABC-1\t5", LineCount: 1}

	if _, cmd := s.Update(poRuneKey("c")); cmd != nil {
		t.Error("'c' should do nothing in the overlay — the bar does not name it")
	}
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("Enter should copy the pad")
	}
	if !barHas(s.orderPadBar(), "Enter", "Copy") {
		t.Errorf("the overlay's bar should name Enter=Copy: %+v", s.orderPadBar())
	}
	// 'q' closed the overlay before the conversion; Esc is the only way out now.
	if _, _ = s.Update(poRuneKey("q")); !s.orderPad {
		t.Error("'q' should not close the overlay — the bar names Esc")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.orderPad {
		t.Error("Esc should close the overlay")
	}
}

// ---------------------------------------------------------------------------
// The sheet is columnar
// ---------------------------------------------------------------------------

// TestPOView_OneLeaderColumn: every reading on the sheet hangs off the SAME
// label column, whichever band it is in — that is what a columnar sheet is, and
// a band that found its own column would read as a different form.
func TestPOView_OneLeaderColumn(t *testing.T) {
	s, _ := poDetailAt(t, 100)
	body := s.renderBody()
	want := -1
	for _, line := range strings.Split(body, "\n") {
		i := strings.Index(line, jdeLeader)
		if i < 0 {
			continue
		}
		if want < 0 {
			want = i
			continue
		}
		if i != want {
			t.Errorf("leader at column %d, want %d — the bands are not sharing one column: %q", i, want, line)
		}
	}
	if want < 0 {
		t.Fatalf("the sheet drew no columnar rows at all:\n%s", body)
	}
}

// TestPOView_LabelColumnIsStableAcrossOrders: the column is computed from every
// label the sheet CAN draw, not from the ones this order happens to carry, so an
// order that grows a Voided row on the next reload does not shift every other
// value one column right under the operator's eye.
func TestPOView_LabelColumnIsStableAcrossOrders(t *testing.T) {
	bare := NewPurchaseOrderDetailScreen(Deps{}, "po-2")
	bare.loading = false
	bare.po = &omsapi.PurchaseOrder{ID: "po-2", Number: "PO-2", Status: "draft"}

	leaderOf := func(body string) int {
		for _, line := range strings.Split(body, "\n") {
			if i := strings.Index(line, jdeLeader); i >= 0 {
				return i
			}
		}
		return -1
	}
	full, _ := poDetailAt(t, 100)
	if a, b := leaderOf(full.renderBody()), leaderOf(bare.renderBody()); a != b {
		t.Errorf("leader column moved between orders: %d vs %d", a, b)
	}
}

// TestPOView_LongValuesFoldRatherThanClip: a value too wide for the pane is
// folded onto continuation lines indented under the input area. clampToBox
// would cut it instead, with nothing on screen to say it had.
func TestPOView_LongValuesFoldRatherThanClip(t *testing.T) {
	s, r := poDetailAt(t, 80)
	// The payment schedule is the widest reading on the sheet and does not fit
	// 80 columns on one row; every word of it still has to reach the screen.
	for _, want := range []string{"$1234.56", "due 2026-08-31", "from order date"} {
		if !poSeenWhileScrolling(s, r, want) {
			t.Errorf("%q was lost from the 80-column render:\n%s", want, r.View())
		}
	}
	// And the fold is indented to the input area, not back to the margin.
	body := strings.Split(s.renderBody(), "\n")
	indent := jdeStripIndent(poDetailLabelWidth())
	found := false
	for _, line := range body {
		if strings.HasPrefix(line, indent) && strings.Contains(line, "from order date") {
			found = true
		}
	}
	if !found {
		t.Errorf("the folded remainder should line up under the input area:\n%s", s.renderBody())
	}
}

// ---------------------------------------------------------------------------
// Behaviour the conversion had to preserve
// ---------------------------------------------------------------------------

// TestPOView_ScrollingReachesTheEnd: Up/Down/PgUp/PgDn/Home/End cover the whole
// sheet and stop at its ends rather than wrapping — a page that jumped from the
// last row back to the first would lose the operator's place.
func TestPOView_ScrollingReachesTheEnd(t *testing.T) {
	s, _ := poDetailAt(t, 80)
	if !strings.Contains(s.View(), "PO-2026-0042") {
		t.Fatal("the sheet should open at the top")
	}
	for i := 0; i < 40; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	}
	if !strings.Contains(s.View(), "Attachments (1)") {
		t.Errorf("paging down should reach the last band:\n%s", s.View())
	}
	if strings.Contains(s.View(), "more below") {
		t.Errorf("paging past the end should rest at the end:\n%s", s.View())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyHome})
	if !strings.Contains(s.View(), "PO-2026-0042") {
		t.Errorf("Home should return to the top:\n%s", s.View())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if !strings.Contains(s.View(), "Attachments (1)") {
		t.Errorf("End should jump to the bottom:\n%s", s.View())
	}
}

// TestPOView_UploadSheetStillValidates: the presentation moved, the upload did
// not. A blank path is refused before anything is opened, and so is a path that
// is not a file.
func TestPOView_UploadSheetStillValidates(t *testing.T) {
	s, _ := poAttachAt(t, 80)
	s.openUpload()
	s.uploadInputs[poAttachFieldPath].SetValue("")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.errMsg == "" || s.uploading {
		t.Errorf("a blank path should error without starting an upload; err=%q uploading=%v", s.errMsg, s.uploading)
	}
	if !strings.Contains(s.View(), "✗") {
		t.Errorf("the error belongs on the status row above the bar:\n%s", s.View())
	}
	s.uploadInputs[poAttachFieldPath].SetValue(t.TempDir())
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(s.errMsg, "directory") {
		t.Errorf("a directory should be refused, err = %q", s.errMsg)
	}
}

// TestPOView_ShipFormStillValidates: same for the mark-shipped prompt.
func TestPOView_ShipFormStillValidates(t *testing.T) {
	s, _ := poDetailAt(t, 80)
	s.openShipForm()
	s.shipIdxIn.SetValue("9")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.shipErr == "" {
		t.Error("a line number past the end should be refused")
	}
	if !strings.Contains(s.View(), "✗") {
		t.Errorf("the error belongs on the status row above the bar:\n%s", s.View())
	}
	// Up/Down move between the two fields, the way they do on every other
	// columnar sheet — tab still does too, as it does on the pilot.
	s.Update(tea.KeyMsg{Type: tea.KeyDown})
	if s.shipFocus != 1 {
		t.Errorf("Down should move to the date field, focus = %d", s.shipFocus)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyUp})
	if s.shipFocus != 0 {
		t.Errorf("Up should move back to the line field, focus = %d", s.shipFocus)
	}
}
