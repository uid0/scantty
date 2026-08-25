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
	"fmt"
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

// poClippedRow returns the one line of the CLIPPED render that carries `label`.
func poClippedRow(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, label) {
			return line
		}
	}
	t.Fatalf("no %q row in the render:\n%s", label, out)
	return ""
}

// TestPOView_OrderPadPartNumberUsesThePaneItHas: the part number is the one
// field an operator retypes into a vendor site, so 28 columns is the FLOOR the
// pad's part column starts at and the pane is the only thing that caps it. The
// conversion had turned 28 into a maximum, so a 45-column manufacturer number
// ellipsised at 120 with half the pane standing empty beside it.
func TestPOView_OrderPadPartNumberUsesThePaneItHas(t *testing.T) {
	// 45 columns: wider than the 28-column floor at every width, and wider than
	// an 80-column pane can hold — which is what separates "shortened because
	// the column was pinned" from "shortened because there is genuinely no room".
	const part = "MANUFACTURER-PART-NUMBER-0001-REV-C-ABCDEFGHI"
	padAt := func(t *testing.T, width int) string {
		t.Helper()
		s, r := poDetailAt(t, width)
		s.orderPad = true
		s.orderPadExport = &omsapi.OrderPadExport{
			Text: part + "\t144", Filename: "PO-2026-0042-order.csv", LineCount: 1,
		}
		return r.View()
	}

	for _, width := range []int{100, 120} {
		t.Run(widthName(width), func(t *testing.T) {
			out := padAt(t, width)
			if !strings.Contains(out, part) {
				t.Errorf("the %d-column pane has room for the whole part number and did not print it:\n%s", width, out)
			}
			if !strings.Contains(out, "144") {
				t.Errorf("the quantity went missing from the %d-column pad:\n%s", width, out)
			}
		})
	}

	t.Run(widthName(80), func(t *testing.T) {
		out := padAt(t, 80)
		if strings.Contains(out, part) {
			t.Errorf("a 51-column pane cannot hold a 45-column part number beside a quantity:\n%s", out)
		}
		if !strings.Contains(out, "…") {
			t.Errorf("the pane forced a cut, so the row has to say so:\n%s", out)
		}
		if !strings.Contains(out, "144") {
			t.Errorf("the quantity is the half of the row being checked and must survive:\n%s", out)
		}
	})
}

// poRowWhileScrolling pages the sheet and returns the first line of the CLIPPED
// render that carries `anchor`, or "" if it never appears.
func poRowWhileScrolling(s *PurchaseOrderDetailScreen, r Root, anchor string) string {
	s.scroll = 0
	for i := 0; i <= s.sheetLines().Len(); i++ {
		for _, line := range strings.Split(r.View(), "\n") {
			if strings.Contains(line, anchor) {
				return line
			}
		}
		before := s.scroll
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if s.View(); s.scroll == before {
			break
		}
	}
	return ""
}

// TestPOView_OrderPadEmptyStateSurvivesTheClip: the pad's empty state is a
// 56-column sentence, and the pane at 80 is 51. Written as one styled line it
// was cut by clampToBox, which takes the closing SGR reset with the text it
// drops — so the row lost its tail AND left the terminal dimmed. No surface in
// the width sweep rendered this branch, because every order-pad fixture carried
// a non-empty pad.
func TestPOView_OrderPadEmptyStateSurvivesTheClip(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			s.orderPad = true
			// What ExportOrderPad returns for a PO whose lines carry no supplier
			// part number: a real export, with nothing in it.
			s.orderPadExport = &omsapi.OrderPadExport{
				Supplier: "Acme Fasteners & Industrial Supply Co.", LineCount: 0,
				MissingSku: []string{"Gadget"},
			}
			out := r.View()
			// The tail is what the pane used to eat, so it is the half that
			// proves the note now folds instead of being cut.
			for _, want := range []string{"No lines have a supplier part number", "order."} {
				if !strings.Contains(out, want) {
					t.Errorf("%q was clipped out of the %d-column empty pad:\n%s", want, width, out)
				}
			}
		})
	}
}

// TestPOView_StatusRowErrorIsBounded: the status row is one row of the frame and
// cannot fold, and the errors that reach it are routinely wider than the pane —
// a missing file's os error runs to about 96 columns. Left unbounded it was cut
// by clampToBox, with the same dropped-SGR-reset hazard as the pad's empty
// state. The full text is not lost: the same message goes out as a toast.
func TestPOView_StatusRowErrorIsBounded(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poAttachAt(t, width)
			s.openUpload()
			// A path that cannot exist: submitUpload's os.Stat fails and puts
			// "cannot read file: open …: no such file or directory" on the row.
			s.Update(tea.KeyMsg{Type: tea.KeyRunes,
				Runes: []rune("/home/operator/scans/2026-08/incoming/definitely-not-here.pdf")})
			s.Update(tea.KeyMsg{Type: tea.KeyEnter})

			row := poClippedRow(t, r.View(), "cannot read file")
			if !strings.Contains(row, "…") {
				t.Errorf("the %d-column status row was cut with nothing to say so: %q", width, row)
			}
		})
	}
}

// TestPOView_AttachmentPagingActsOnlyWhenTheBarNamesIt: the rule the whole key
// scheme rests on — a key the bar does not name does nothing. The bar names
// PgUp/PgDn only when the grid is taller than the pane, but the handler paged
// the highlight regardless, so on a tall terminal PgDn jumped the cursor while
// nothing on screen said the key existed.
func TestPOView_AttachmentPagingActsOnlyWhenTheBarNamesIt(t *testing.T) {
	t.Run("grid fits the pane", func(t *testing.T) {
		s, _ := poAttachAt(t, 100)
		s.attachments = poManyAttachments(3)
		if barHas(s.listBar(), "PgUp/PgDn", "Page") {
			t.Fatalf("three attachments in a %d-row terminal should not need paging", poViewHeight)
		}
		s.cursor = 0
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if s.cursor != 0 {
			t.Errorf("PgDn moved the highlight to %d while the bar does not name the key", s.cursor)
		}
	})

	t.Run("grid overflows the pane", func(t *testing.T) {
		s, _ := poAttachAt(t, 100)
		s.attachments = poManyAttachments(40)
		if !barHas(s.listBar(), "PgUp/PgDn", "Page") {
			t.Fatalf("forty attachments overflow the pane, so the bar has to name paging")
		}
		s.cursor = 0
		s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if s.cursor == 0 {
			t.Error("PgDn should page the highlight when the bar names the key")
		}
	})
}

// poManyAttachments builds n distinguishable attachments.
func poManyAttachments(n int) []omsapi.PurchaseOrderAttachment {
	out := make([]omsapi.PurchaseOrderAttachment, n)
	for i := range out {
		out[i] = omsapi.PurchaseOrderAttachment{
			ID: i + 1, FileName: fmt.Sprintf("scan-%03d.pdf", i+1),
			UploadedAt: time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC), UploadedByName: "alice",
		}
	}
	return out
}

// TestPOView_AttachmentNameUsesEveryColumnThePaneHas: the name cell was budgeted
// with len() over a prefix containing "·" — five bytes, four columns — so a name
// that exactly filled the pane was ellipsised with a column standing empty
// beside it.
func TestPOView_AttachmentNameUsesEveryColumnThePaneHas(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			// Exactly the columns the row has left once "  · " is in front of it.
			name := strings.Repeat("a", screenBodyWidth(width)-4) + ".pdf"
			name = name[len(name)-(screenBodyWidth(width)-4):]

			s, r := poDetailAt(t, width)
			s.po.Attachments = []omsapi.PurchaseOrderAttachment{{ID: 1, FileName: name}}
			if !poSeenWhileScrolling(s, r, name) {
				t.Errorf("a name that exactly fills the %d-column pane was shortened anyway:\n%s",
					width, r.View())
			}
		})
	}
}

// TestPOView_NarrowPaneNeverShowsAFragmentOfAValue: a reading is either whole,
// or visibly cut. It is never quietly replaced by a piece of itself.
//
// jdeWrapNote only ellipsises an over-long word from two columns up; below that
// it drops the word and keeps whatever single-column tokens the value happens to
// contain. "Acme Fasteners & Industrial Supply Co." folded to one column came
// back as "&", and the sheet drew `Supplier ..... &` — a value that is not the
// value, with nothing to say so. Its sibling rows, whose values have no
// one-column word, rendered "…" at the same width, so one band showed two
// different degenerate behaviours at once.
//
// The crash sweep below could not catch this: a fragment is not a panic, and the
// frame is not empty. So this asserts CONTENT, on the clipped render.
func TestPOView_NarrowPaneNeverShowsAFragmentOfAValue(t *testing.T) {
	// From the narrowest width at which the leader column still fits the pane —
	// below it the strip has no room at all and the row draws untruncated, which
	// is the pane-is-too-small floor rather than a fold — up to the first
	// legibility width.
	for width := 52; width < 80; width++ {
		t.Run(fmt.Sprintf("%dcol", width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			// "Supplier ....." and not "Supplier": the anchor has to miss the
			// Identifiers band's "Supplier PO #" row.
			row := poRowWhileScrolling(s, r, "Supplier .....")
			if row == "" {
				t.Fatalf("the Supplier row never reaches the terminal at %d columns", width)
			}
			// Whole (the value, or a fold of it, starts at its first word) or
			// visibly cut. A fragment is neither.
			whole := strings.Contains(row, "Acme")
			cut := strings.Contains(row, "…")
			if !whole && !cut {
				t.Errorf("the %d-column Supplier row shows neither the value nor a mark that it was cut: %q",
					width, row)
			}
		})
	}
}

// TestPOView_MarginNotesUseTheWholePane: a line drawn behind jdeIndent alone has
// the pane minus two columns, not minus the leader column it does not hang off.
// Both the Notes band and the order pad's omitted-lines warning wrapped to
// jdeStripWidth, which also subtracts the seven-column leader, so both folded
// seven columns early and pushed words onto rows that had room beside them.
func TestPOView_MarginNotesUseTheWholePane(t *testing.T) {
	// Both phrases fit the corrected budget at 80 (49 columns) and not the old
	// one (42), so each proves the fold moved. A substring spanning a wrap point
	// cannot match, because the wrap puts a newline through it.
	t.Run("notes", func(t *testing.T) {
		s, r := poDetailAt(t, 80)
		const want = "Deliver to the loading dock; the front desk"
		if !poSeenWhileScrolling(s, r, want) {
			t.Errorf("the Notes band still folds before the pane needs it; %q never lands on one line:\n%s",
				want, r.View())
		}
	})

	t.Run("order pad warning", func(t *testing.T) {
		s, r := poDetailAt(t, 80)
		s.orderPad = true
		s.orderPadExport = &omsapi.OrderPadExport{
			Text:       "M3-HEX-BOLT-SS\t5",
			Supplier:   "Acme Fasteners & Industrial Supply Co.",
			Filename:   "PO-2026-0042-order.csv",
			LineCount:  1,
			MissingSku: []string{"Gadget", "Bracket"},
		}
		const want = "(omitted): Gadget,"
		if out := r.View(); !strings.Contains(out, want) {
			t.Errorf("the pad warning still folds before the pane needs it; %q never lands on one line:\n%s",
				want, out)
		}
	})
}

// TestPOView_NarrowPaneNeverCrashes: every width the layout can be ASKED for
// has to render, not just the three it has to be legible at.
//
// A terminal being dragged narrower fires a WindowSizeMsg for every column it
// passes through, and a panic in a render path takes the whole TUI down and
// leaves the terminal in raw mode — a class of failure the 80/100/120 tests
// cannot see. addValueRow indexed jdeWrapNote's first line without checking it
// returned one, and jdeWrapNote comes back EMPTY when the strip is under two
// columns and every word is wider than it; at 80/100/120 the strip is never
// that narrow, so only a drag through 52 hit it.
func TestPOView_NarrowPaneNeverCrashes(t *testing.T) {
	// From the narrowest a terminal is plausibly dragged to, up to the first
	// legibility width. Each one renders every surface of every screen: a panic
	// fails the run and names the width it happened at.
	for width := 20; width < 80; width++ {
		t.Run(fmt.Sprintf("%dcol", width), func(t *testing.T) {
			for _, tc := range poViewSurfaces(t, width) {
				if tc.view() == "" {
					t.Errorf("%s rendered nothing at %d columns", tc.name, width)
				}
			}
			// And through Root, which is what clips — the same widths again on
			// the path the terminal actually sees.
			_, detail := poDetailAt(t, width)
			_, attach := poAttachAt(t, width)
			for _, r := range []Root{detail, attach} {
				if r.View() == "" {
					t.Errorf("Root rendered nothing at %d columns", width)
				}
			}
		})
	}
}

// TestPOView_OrderPadKeepsAManyDigitQuantity: the pad's quantity column is a
// NUMBER column. "100000…" is not a shortened quantity, it is a different
// quantity that looks plausible — the same rule the line grid already follows,
// which the pad had not been given.
func TestPOView_OrderPadKeepsAManyDigitQuantity(t *testing.T) {
	const qty = "1000000"
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)
			s.orderPad = true
			s.orderPadExport = &omsapi.OrderPadExport{
				Text: "M3-HEX-BOLT-SS\t" + qty, Filename: "PO-2026-0042-order.csv", LineCount: 1,
			}
			out := r.View()
			if !strings.Contains(out, qty) {
				t.Errorf("the %d-column pad shortened a quantity into a different number:\n%s", width, out)
			}
			if !strings.Contains(out, "M3-HEX-BOLT-SS") {
				t.Errorf("the part number went missing from the %d-column pad:\n%s", width, out)
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

	// A pad whose part number is wider than the column's 28-column floor: the
	// column grows toward the pane, so the row's width has to come from what the
	// pane can give and not from the value.
	longPad, _ := poDetailAt(t, width)
	longPad.orderPad = true
	longPad.orderPadExport = &omsapi.OrderPadExport{
		Text:      "MANUFACTURER-PART-NUMBER-0001-REV-C-ABCDEFGHI\t144",
		Filename:  "PO-2026-0042-order.csv",
		LineCount: 1,
	}
	out = append(out, poViewSurface{"order pad, long part #", longPad.View, longPad.orderPadBar(), width})

	// The pad with nothing on it. Its own sentence is the widest line the
	// overlay draws, and no other pad fixture renders this branch.
	emptyPad, _ := poDetailAt(t, width)
	emptyPad.orderPad = true
	emptyPad.orderPadExport = &omsapi.OrderPadExport{
		Supplier: "Acme Fasteners & Industrial Supply Co.", LineCount: 0,
		MissingSku: []string{"Gadget", "Bracket"},
	}
	out = append(out, poViewSurface{"order pad, nothing to order", emptyPad.View, emptyPad.orderPadBar(), width})

	// The upload sheet carrying an error wider than the pane: the status row is
	// the one row of the frame that cannot fold.
	failed, _ := poAttachAt(t, width)
	failed.openUpload()
	failed.Update(tea.KeyMsg{Type: tea.KeyRunes,
		Runes: []rune("/home/operator/scans/2026-08/incoming/definitely-not-here.pdf")})
	failed.Update(tea.KeyMsg{Type: tea.KeyEnter})
	out = append(out, poViewSurface{"upload sheet, error row", failed.View,
		[]actionBarItem{{"Enter", "Upload"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}, width})

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
		{"E", "Edit"}, {"A", "Files"}, {"n", "Add line"}, {"s", "Send"},
		{"S", "Ship line"}, {"x", "Order pad"}, {"v", "Void"}, {"r", "Refresh"},
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
//
// `n` left this list when it became the add-a-line key (po_add_line.go). It is
// STATUS-GATED rather than retired, so on the wrong status it answers with an
// explaining toast exactly as s / c / d / v / S do — which is behaviour the
// bar-honesty sweep allows and this test, which rejects any command at all,
// cannot express.
func TestPOView_RetiredKeysDoNothing(t *testing.T) {
	for _, k := range []string{"j", "k", "g", "G", "R", "u", "y"} {
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

// TestPOView_OrderPadWithNothingToCopyDoesNotOfferCopy: the bar-honesty rule in
// the direction that advertises a dead key. ExportOrderPad returns a real export
// with empty Text for a PO whose lines carry no supplier part number, and the
// overlay drew "Enter=Copy" over "No lines have a supplier part number" while
// handleOrderPadKey's Enter did nothing at all.
func TestPOView_OrderPadWithNothingToCopyDoesNotOfferCopy(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			empty, r := poDetailAt(t, width)
			empty.orderPad = true
			empty.orderPadExport = &omsapi.OrderPadExport{
				Supplier: "Acme Fasteners & Industrial Supply Co.", LineCount: 0,
			}
			// Enter genuinely does nothing here — no clipboard write, no toast.
			if _, cmd := empty.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
				t.Fatal("Enter on an empty pad should have nothing to copy")
			}
			if out := r.View(); strings.Contains(out, "Enter=Copy") {
				t.Errorf("the %d-column empty pad names a key that does nothing:\n%s", width, out)
			}
			// Esc is still named, and still the way out.
			if out := r.View(); !strings.Contains(out, "Esc=Close") {
				t.Errorf("the %d-column empty pad must still name its way out:\n%s", width, out)
			}

			// And a pad that HAS lines still offers it.
			full, rf := poDetailAt(t, width)
			full.orderPad = true
			full.orderPadExport = &omsapi.OrderPadExport{
				Text: "M3-HEX-BOLT-SS\t5", Filename: "PO-2026-0042-order.csv", LineCount: 1,
			}
			if out := rf.View(); !strings.Contains(out, "Enter=Copy") {
				t.Errorf("a pad with lines must name Enter=Copy at %d columns:\n%s", width, out)
			}
		})
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

// ---------------------------------------------------------------------------
// The bar is honest — enforced as a rule, not per key
// ---------------------------------------------------------------------------

// poBarKeyNames maps a bar entry's Key to the keystrokes it names. The bar
// writes ONE entry for a pair — "UP/DN", "PgUp/PgDn", "Home/End" — so this is
// the bar's own vocabulary rather than anything read out of the handlers.
var poBarKeyNames = map[string][]string{
	"Enter":     {"enter"},
	"Esc":       {"esc"},
	"UP/DN":     {"up", "down"},
	"PgUp/PgDn": {"pgup", "pgdown"},
	"Home/End":  {"home", "end"},
	"Ctrl-X":    {"ctrl+x"},
	"r":         {"r"},
	"E":         {"E"},
	"A":         {"A"},
	"x":         {"x"},
	"s":         {"s"},
	"c":         {"c"},
	"d":         {"d"},
	"v":         {"v"},
	"S":         {"S"},
	"n":         {"n"},
}

// poKeyMsg turns one of those keystroke names into the message the terminal
// sends.
func poKeyMsg(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	}
	// Everything else — the rest of poKeySpace's named specials, and every
	// printable rune — is spelled by the phase sweep's translator, so the two
	// sweeps cannot disagree about what a key name means.
	return poPhaseKeyMsg(key)
}

// poBarPhase is one screen in one state, rebuilt from scratch on demand so a
// probe keystroke cannot leak into the next assertion. `state` is what counts as
// an observable change.
type poBarPhase struct {
	name string
	// typing marks a phase that has a FOCUSED TEXT FIELD. On those the rule is
	// about COMMAND keys only: a printable character typed into an input is
	// text, not an unnamed command, so the bar naming Enter/Esc/UP-DN and not
	// the alphabet is correct rather than a gap. Non-printable keys are still
	// held to the rule on these phases.
	typing bool
	build  func(t *testing.T, width int) (scr Screen, state func() string, bar []actionBarItem)
}

// poNavState is the CLIPPED frame plus the navigation fields the frame cannot be
// trusted to show.
//
// The frame alone is not enough for the attachment grid: it marks its highlighted
// row with StyleJDEFieldFocused, and lipgloss renders flat in a test binary, so
// moving the cursor produces a byte-identical frame. jde_cells_test.go closes
// that blind spot for the columnar rows — force the profile and decode the frame
// — but the grid's highlight is a whole rendered ROW rather than a field, so the
// cursor field is named here instead.
//
// The SCROLL OFFSETS are deliberately NOT named. A stored offset is not
// observable state: WindowFrom ignores it outright when the body fits the window,
// so padScrollBy can move s.padScroll while the frame stays byte-identical.
// Counting it as a change is what let an inert scroll key read as "the key
// works" and hid the boundary defect this file's boundary sweep now pins.
func poNavState(r Root, nav func() string) func() string {
	return func() string {
		return r.View() + "\x00" + nav()
	}
}

// poKeyEffect is what pressing `key` did: whether the observable state changed,
// and whether the screen returned a command.
func poKeyEffect(t *testing.T, p poBarPhase, width int, probe []string, key string) (changed, cmdIssued bool) {
	t.Helper()
	scr, state, _ := p.build(t, width)
	for _, pk := range probe {
		scr.Update(poKeyMsg(pk))
	}
	before := state()
	_, cmd := scr.Update(poKeyMsg(key))
	return state() != before, cmd != nil
}

// TestPOView_BarNamesExactlyTheKeysThatWork: the rule the whole key scheme rests
// on, asserted as a RULE rather than one entry at a time.
//
// It has been broken three times in a row — the attachments grid's PgUp/PgDn,
// the order pad's Enter=Copy, then the pad's and the sheet's scroll keys — each
// time by a fix that corrected one bar entry and left its neighbours alone. So
// this walks every phase in BOTH the overflowing and the fitting state and
// checks the two halves of the rule for every key the bar's vocabulary knows:
//
//	a key the bar NAMES must do something, and
//	a key the bar does NOT name must not change any state.
//
// A named key is probed from several positions — as opened, after End, and after
// paging to the bottom — because Home does nothing at the top and End nothing at
// the bottom; a key is dead only if it does nothing from any of them.
//
// The unnamed half asks for no STATE change rather than no command, because a
// status-gated letter — s/c/d/v/S when the order is in the wrong status — is
// meant to answer with an explaining toast and change nothing. That toast is the
// send/confirm/deliver/void gating the brief requires preserved exactly, so it is
// behaviour the rule has to allow rather than a key that escaped the audit.
func TestPOView_BarNamesExactlyTheKeysThatWork(t *testing.T) {
	// Enough of each to reach the far end of any of these bodies.
	toBottom := []string{"end"}
	for i := 0; i < 80; i++ {
		toBottom = append(toBottom, "down")
	}
	probes := [][]string{nil, {"end"}, toBottom}

	for _, p := range poBarPhases() {
		for _, width := range poViewWidths {
			t.Run(p.name+"/"+widthName(width), func(t *testing.T) {
				_, _, bar := p.build(t, width)

				named := map[string]bool{}
				for _, it := range bar {
					keys, ok := poBarKeyNames[it.Key]
					if !ok {
						t.Fatalf("bar entry %q is not in poBarKeyNames — add it so the rule covers it", it.Key)
					}
					for _, k := range keys {
						named[k] = true
					}
				}

				for _, key := range poKeySpace() {
					// A focused text field OWNS the printable runes and the
					// field-editing keys: they act by editing the value, which is
					// what the field is for, so the reverse direction cannot apply
					// to them there. The forward direction still does.
					if p.typing && (poIsPrintable(key) || poFieldKeys[key]) && !named[key] {
						continue
					}
					if p.typing && poFormNavAliases[key] {
						continue
					}
					var changed, issued bool
					for _, probe := range probes {
						c, i := poKeyEffect(t, p, width, probe, key)
						changed = changed || c
						issued = issued || i
					}
					switch {
					case named[key] && !changed && !issued:
						// Esc is the one exception: on the detail sheet the app-wide
						// back-step belongs to Root's dispatcher, not the screen, so
						// the screen answering nothing is correct.
						if key == "esc" {
							continue
						}
						t.Errorf("%s names %q but the key does nothing there", p.name, key)
					case !named[key] && changed:
						t.Errorf("%s does not name %q, but pressing it changes state", p.name, key)
					}
				}
			})
		}
	}
}

// poFormNavAliases are Tab and Shift-Tab on a SHEET WITH FIELDS, where they
// ride alongside Up/Down and the bar names the canonical key of the pair
// ("UP/DN=Fields") rather than every spelling of it.
//
// It is recorded here rather than left out of a roster, which is the whole
// difference: an omission is silent and this is a statement with a reason
// attached. The convention is app-wide — roughly twenty columnar forms build
// that exact bar entry (jde_form.go's callers) — so naming the alias on these
// three modals alone would make the purchasing sheets disagree with every other
// form in the program, and naming it everywhere is a change to all of them that
// nobody has asked for. po_detail.go's header comment records the same
// deviation from the screen's side.
//
// It applies only to phases with a focused field, because that is the only
// place the pair means "next field": nothing else in this package binds them.
var poFormNavAliases = map[string]bool{"tab": true, "shift+tab": true}

// The key space this sweep presses is poKeySpace() — every printable ASCII rune
// plus the named specials a terminal sends — and NOT a roster of the bar's own
// vocabulary.
//
// It used to be the latter (poAllBarKeys), and that is exactly how `N` reached
// an operator's terminal doing nothing on the purchase-order list while the
// footer named it: a key bound in a handler and absent from the roster was
// pressed in NEITHER direction, so it was untested rather than passing. A
// roster that has to be edited in step with the code is that omission waiting
// to happen again, and it fails SILENTLY. Pressing the whole space costs a
// little more wall-clock and cannot be forgotten.

// poShortPO is a freshly created draft with nothing on it — the order whose
// sheet fits the pane, so its scroll keys must not be named.
func poShortPO() *omsapi.PurchaseOrder {
	return &omsapi.PurchaseOrder{
		ID: "po-2", Number: "PO-2026-0043",
		Status: "draft", StatusLabel: "Draft",
		SupplierDetails: "Acme",
		OrderDate:       time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

// poBarPhases is every phase of the two screens, in both the state where the
// body overflows the pane and the state where it fits.
func poBarPhases() []poBarPhase {
	detail := func(po func() *omsapi.PurchaseOrder) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = po()
			r := poViewRoot(t, s, width)
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.orderPad, s.shipping, s.voiding, s.delivering)
			}), s.sheetBar()
		}
	}
	pad := func(text string) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s, r := poDetailAt(t, width)
			s.orderPad = true
			s.orderPadExport = &omsapi.OrderPadExport{
				Text: text, Supplier: "Acme", Filename: "PO-2026-0042-order.csv",
				LineCount: len(strings.Split(text, "\n")),
			}
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.orderPad)
			}), s.orderPadBar()
		}
	}
	attach := func(n int) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s, r := poAttachAt(t, width)
			s.attachments = poManyAttachments(n)
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.cursor, s.phase, s.confirmingDelete)
			}), s.listBar()
		}
	}

	// The detail screen's pre-body frames. `mut` runs after the screen is sized
	// so the state under test is the one the frame renders.
	detailIn := func(mut func(*PurchaseOrderDetailScreen)) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			r := poViewRoot(t, s, width)
			mut(s)
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.orderPad, s.shipping, s.voiding, s.delivering, s.loading, s.loadErr)
			}), s.sheetBar()
		}
	}
	// A modal sheet of the detail screen, opened after sizing.
	modal := func(open func(*PurchaseOrderDetailScreen), bar func(*PurchaseOrderDetailScreen) []actionBarItem) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s, r := poDetailAt(t, width)
			open(s)
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.shipping, s.voiding, s.delivering, s.shipFocus, s.deliverFocus)
			}), bar(s)
		}
	}
	// The pad's own loading and error frames, which pass their own bar.
	padFrame := func(mut func(*PurchaseOrderDetailScreen)) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s, r := poDetailAt(t, width)
			s.orderPad = true
			mut(s)
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.orderPad)
			}), []actionBarItem{{"Esc", "Close"}}
		}
	}
	attachIn := func(n int, mut func(*PurchaseOrderAttachmentsScreen)) func(*testing.T, int) (Screen, func() string, []actionBarItem) {
		return func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s, r := poAttachAt(t, width)
			s.attachments = poManyAttachments(n)
			mut(s)
			bar := s.listBar()
			if s.confirmingDelete {
				bar = []actionBarItem{{"Enter", "Delete"}, {"Esc", "Cancel"}}
			} else if s.phase == poAttachPhaseUpload {
				bar = []actionBarItem{{"Enter", "Upload"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
			}
			return s, poNavState(r, func() string {
				return fmt.Sprint(s.cursor, s.phase, s.confirmingDelete, s.uploadFocus)
			}), bar
		}
	}

	var longPad []string
	for i := 0; i < 60; i++ {
		longPad = append(longPad, fmt.Sprintf("PART-%04d\t%d", i, i+1))
	}

	// EVERY state of both screens. The rule is checked by enumerating the states
	// rather than the reported instances, because five consecutive review rounds
	// found this rule broken one entry at a time — each fix landing on whichever
	// side of a boundary the fixture happened to sit.
	return []poBarPhase{
		{name: "detail sheet (body overflows)", build: detail(poViewPO)},
		{name: "detail sheet (body fits)", build: detail(poShortPO)},
		{name: "detail loading (no order yet)", build: func(t *testing.T, width int) (Screen, func() string, []actionBarItem) {
			t.Helper()
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			r := poViewRoot(t, s, width)
			return s, poNavState(r, func() string { return fmt.Sprint(s.loading) }), s.sheetBar()
		}},
		{name: "detail loading (reload in flight)", build: detailIn(func(s *PurchaseOrderDetailScreen) { s.loading = true })},
		{name: "detail load error", build: detailIn(func(s *PurchaseOrderDetailScreen) {
			s.po, s.loadErr = nil, "dial tcp 10.0.0.4:8000: connect: connection refused"
		})},
		{name: "detail not found", build: detailIn(func(s *PurchaseOrderDetailScreen) { s.po = nil })},
		{name: "mark shipped", typing: true, build: modal(
			func(s *PurchaseOrderDetailScreen) { s.openShipForm() },
			func(s *PurchaseOrderDetailScreen) []actionBarItem {
				return []actionBarItem{{"Enter", "Mark shipped"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
			})},
		{name: "void order", typing: true, build: modal(
			func(s *PurchaseOrderDetailScreen) { s.openVoidForm() },
			func(s *PurchaseOrderDetailScreen) []actionBarItem {
				return []actionBarItem{{"Enter", "Void order"}, {"Esc", "Cancel"}}
			})},
		{name: "mark delivered", typing: true, build: modal(
			func(s *PurchaseOrderDetailScreen) { s.openDeliverForm() },
			func(s *PurchaseOrderDetailScreen) []actionBarItem {
				return []actionBarItem{{"Enter", "Mark delivered"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
			})},
		{name: "order pad (loading)", build: padFrame(func(s *PurchaseOrderDetailScreen) { s.orderPadLoading = true })},
		{name: "order pad (error)", build: padFrame(func(s *PurchaseOrderDetailScreen) { s.orderPadErr = "order pad failed" })},
		{name: "order pad (overflows)", build: pad(strings.Join(longPad, "\n"))},
		{name: "order pad (fits)", build: pad("M3-HEX-BOLT-SS\t5\nGADGET-0001\t2")},
		{name: "order pad (nothing to order)", build: pad("")},
		{name: "attachments grid (none)", build: attachIn(0, func(*PurchaseOrderAttachmentsScreen) {})},
		{name: "attachments grid (exactly one)", build: attachIn(1, func(*PurchaseOrderAttachmentsScreen) {})},
		{name: "attachments grid (fits)", build: attach(3)},
		{name: "attachments grid (overflows)", build: attach(60)},
		{name: "attachments grid (load error)", build: attachIn(3, func(s *PurchaseOrderAttachmentsScreen) {
			s.loadErr = "dial tcp 10.0.0.4:8000: connect: connection refused"
		})},
		{name: "attachments upload sheet", typing: true, build: attachIn(3, func(s *PurchaseOrderAttachmentsScreen) { s.openUpload() })},
		{name: "attachments delete confirm", build: attachIn(3, func(s *PurchaseOrderAttachmentsScreen) { s.confirmingDelete = true })},
	}
}

// poViewRootSized is poViewRoot for a test that needs to vary the pane HEIGHT as
// well as its width.
func poViewRootSized(t *testing.T, screen Screen, width, height int) Root {
	t.Helper()
	r := newTestRoot(screen)
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: height})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after
}

// TestPOView_ScrollKeysNamedExactlyWhenTheBodyMoves: the bar-honesty rule at the
// boundary, which is the only place it has ever actually been wrong.
//
// The scroll predicate used to ask ClampScroll, which reserves two indicator
// rows and so answered "scrollable" from two lines BEFORE WindowFrom's own
// `n <= avail` short-circuit lets the body move. In that two-row window the bar
// named UP/DN, PgUp/PgDn and Home/End — 42 of its 49 columns — over a body that
// could not move. Fixtures either side of the window cannot see it, which is why
// the generic bar-honesty sweep passed while the defect was live.
//
// So this sweeps the pane HEIGHT one row at a time. Each step changes the window
// by a row, so the sweep necessarily crosses the boundary from both sides, and at
// every height it asserts the equivalence directly against the CLIPPED render:
// the bar names the scroll keys if and only if pressing one moves the frame.
// Nothing here re-derives the threshold — it observes it.
//
// The heights the layer REFUSES the frame at are asserted rather than swept
// past. A refused pane draws the notice and NO BAR, so there is no claim on it
// to be honest or dishonest about — asking `barHas` there is asking about a
// legend nobody can read. It passed for a round by accident: while a refused
// pane answered the body an avail of 0, jdeLines.Scrolls came back false there
// and the equivalence held at both ends for the wrong reason. Skipping those
// heights silently would leave a band of the sweep untested, which is the shape
// this file exists to prevent, so the notice is checked for instead.
func TestPOView_ScrollKeysNamedExactlyWhenTheBodyMoves(t *testing.T) {
	surfaces := []struct {
		name  string
		build func(t *testing.T, width, height int) (Screen, Root, []actionBarItem)
	}{
		{name: "detail sheet", build: func(t *testing.T, width, height int) (Screen, Root, []actionBarItem) {
			t.Helper()
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			return s, poViewRootSized(t, s, width, height), s.sheetBar()
		}},
		{name: "order pad", build: func(t *testing.T, width, height int) (Screen, Root, []actionBarItem) {
			t.Helper()
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			r := poViewRootSized(t, s, width, height)
			var rows []string
			for i := 0; i < 20; i++ {
				rows = append(rows, fmt.Sprintf("PART-%04d\t%d", i, i+1))
			}
			s.orderPad = true
			s.orderPadExport = &omsapi.OrderPadExport{
				Text: strings.Join(rows, "\n"), Supplier: "Acme",
				Filename: "PO-2026-0042-order.csv", LineCount: len(rows),
			}
			return s, r, s.orderPadBar()
		}},
		{name: "attachments grid", build: func(t *testing.T, width, height int) (Screen, Root, []actionBarItem) {
			t.Helper()
			s := NewPurchaseOrderAttachmentsScreen(Deps{}, poViewPO())
			r := poViewRootSized(t, s, width, height)
			s.attachments = poManyAttachments(12)
			return s, r, s.listBar()
		}},
	}

	// Wide enough that the sweep runs from "the body dwarfs the pane" to "the
	// pane dwarfs the body", crossing every threshold in between.
	for _, sf := range surfaces {
		for _, width := range poViewWidths {
			for height := 12; height <= 60; height++ {
				name := fmt.Sprintf("%s/%dx%d", sf.name, width, height)

				// PgUp/PgDn=Page is the affordance under test on all three
				// surfaces: it means exactly "the WINDOW moves". UP/DN is not,
				// on the grid — there it moves the CURSOR, which is real but
				// invisible here because lipgloss renders the highlight flat.
				probe, _, bar := sf.build(t, width, height)
				if jdeBarOf(probe.View()) == nil {
					if !strings.Contains(probe.View(), "Too short") {
						t.Errorf("%s: the frame draws no action bar and does not say why, "+
							"so the operator reads a pane with no legend and no "+
							"explanation:\n%s", name, probe.View())
					}
					continue
				}
				named := barHas(bar, "PgUp/PgDn", "Page")

				s, r, _ := sf.build(t, width, height)
				before := r.View()
				s.Update(poKeyMsg("pgdown"))
				moved := r.View() != before

				if named != moved {
					t.Errorf("%s: bar names PgUp/PgDn = %v but paging moves the frame = %v\n%s",
						name, named, moved, r.View())
				}
			}
		}
	}
}

// TestPOView_ReloadThatLosesTheOrderUnderASheet: a reload that comes back with
// no order while one of the order-level sheets is open must not take the TUI
// with it.
//
// The sequence is ordinary operation, not a corner: press r on a loaded order,
// press S while the reload is in flight — openShipForm succeeds, because the
// order on screen is still the old one — and then let the reload fail, which on
// a shop floor means OMS is simply unreachable. poDetailLoadedMsg stores the nil
// order unconditionally, and the very next frame used to dereference it inside
// viewShip's "1-N" hint: no keystroke needed, the whole program died and left
// the terminal in raw mode.
//
// It is driven through Update because it is the STATE MACHINE that was wrong. A
// test that only rendered a nil-order screen in isolation passed throughout.
func TestPOView_ReloadThatLosesTheOrderUnderASheet(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s, r := poDetailAt(t, width)

			s.Update(poRuneKey("r"))
			s.Update(poRuneKey("S"))
			if !s.shipping {
				t.Fatalf("S should have opened the mark-shipped sheet over the reload")
			}

			s.Update(poDetailLoadedMsg{err: fmt.Errorf("OMS unreachable")})

			out := r.View()
			if !strings.Contains(out, "OMS unreachable") {
				t.Errorf("the failed reload should draw its error, got:\n%s", out)
			}
			if strings.Contains(out, "Mark item shipped") {
				t.Errorf("the mark-shipped sheet cannot be the frame without an order:\n%s", out)
			}

			// Bar honesty holds on the frame that answers for "no order": the
			// two keys that still work are named and nothing else is.
			bar := s.sheetBar()
			if !barHas(bar, "Esc", "Back") || !barHas(bar, "r", "Refresh") {
				t.Errorf("the no-order frame should name Esc and r, got %v", bar)
			}
			for _, dead := range [][2]string{{"Enter", "Receive"}, {"S", "Ship line"}, {"E", "Edit"}, {"x", "Order pad"}} {
				if barHas(bar, dead[0], dead[1]) {
					t.Errorf("the no-order frame names %s=%s, which needs an order", dead[0], dead[1])
				}
			}

			// And the keys that ACT there are that frame's: Enter is inert
			// rather than submitting a shipment against an order that is gone.
			s.Update(poKeyMsg("enter"))
			if got := r.View(); got != out {
				t.Errorf("Enter should do nothing on the no-order frame:\n%s", got)
			}
		})
	}
}

// TestPOView_RefreshKeepsTheReadingPosition: the transient loading frame is not
// allowed to throw away where the operator was reading.
//
// The sheet's scroll offset is clamped against the body being drawn and stored
// back. The loading / error / not-found bodies are one line, so clamping against
// one of them yields 0 — and every reload passes through one: r, and also send,
// confirm, void and mark-delivered, which all reload when they land. Scrolling
// into the line items and pressing r used to come back at the top.
func TestPOView_RefreshKeepsTheReadingPosition(t *testing.T) {
	for _, width := range poViewWidths {
		t.Run(widthName(width), func(t *testing.T) {
			s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
			s.loading = false
			s.po = poViewPO()
			r := poViewRootSized(t, s, width, 24)

			top := r.View()
			s.Update(poKeyMsg("pgdown"))
			reading := r.View()
			if reading == top {
				t.Fatalf("the fixture must overflow the pane for this to mean anything:\n%s", top)
			}

			s.Update(poRuneKey("r"))
			loading := r.View()
			if !strings.Contains(loading, "Loading purchase order") {
				t.Fatalf("r should draw the loading frame, got:\n%s", loading)
			}

			s.Update(poDetailLoadedMsg{po: poViewPO()})
			if got := r.View(); got != reading {
				t.Errorf("the reload should come back where the operator was reading, got:\n%s\nwant:\n%s", got, reading)
			}
		})
	}
}
