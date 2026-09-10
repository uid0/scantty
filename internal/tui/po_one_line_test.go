// The WIDE form of the purchase-order line grid: one row per line, carrying the
// supplier's part number, the part's whole internal UUID and what the supplier
// is charging for the line (po_detail.go, poBuildOneLineGrid).
//
// EVERY CLAIM HERE IS MADE ON Root.View() — the string the terminal really
// shows, after clampToBox has cut it on both axes — and never on the string the
// screen produced. That distinction is the reason po_view_jde_test.go exists at
// all, and this file inherits it: a row measured before the clip cannot report
// the one defect a grid this wide can have.
//
// THE WIDTH AXIS IS THIS FILE'S OWN, AND THAT IS A FINDING RATHER THAN A
// PREFERENCE. Every derived width set in the package stops at 120 —
// jdeDrawableWidths caps there because "a wider pane only folds less", and
// jdePaneWidths and poViewWidths are {80, 100, 120} — which was true of every
// layout in the program until this one. A full UUID is 36 cells, so the wide
// form's threshold is well past 120 and NO existing sweep can reach the state
// this file is about: they all measure the block form, correctly, and are
// silent about the other one. poOneLineWidths walks up to poOneLineMaxWidth
// instead, and every sweep below counts the panes it found on BOTH sides of the
// boundary and fails if it never reached one of them — because a sweep that
// only ever saw the block form would pass with this whole feature deleted.
package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// poOneLineMaxWidth is where the width sweep stops. It is past every threshold
// the fixtures in this file reach — each sweep asserts it found collapsed panes,
// so a fixture that grew past this ceiling fails here rather than going quietly
// untested — and it is a test-only ceiling: nothing in the program reads it.
const poOneLineMaxWidth = 420

// poOneLineHeight is tall enough that the detail sheet does not scroll for any
// fixture in this file. Every row-counting claim below needs the WHOLE line
// band on the pane at once, so the sweeps assert the sheet is not scrolling
// rather than assuming it.
const poOneLineHeight = 60

// poOneLineWidths is every terminal width from Root's own "too narrow" gate up
// to poOneLineMaxWidth at which Root draws a screen at all.
//
// Derived by asking Root, not by naming widths, for the reason
// jdeDrawableWidths records at length — and memoised for the reason it records
// even more emphatically: this package has already gone past go test's 600s
// per-package timeout once by re-deriving a pane set inside a loop.
func poOneLineWidths() []int {
	return append([]int(nil), poOneLineWidthsOnce()...)
}

var poOneLineWidthsOnce = sync.OnceValue(func() []int {
	var out []int
	for w := 1; w <= poOneLineMaxWidth; w++ {
		if jdeRootDrawsAtWidth(w) {
			out = append(out, w)
		}
	}
	return out
})

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// The three UUIDs the fixtures carry. They are real 36-cell canonical UUIDs
// because the whole point of the column is that a part id is 36 cells and is
// never abbreviated; a short stand-in would make every width claim in this file
// a claim about a value OMS cannot send.
// poOneLineShipDate is the fixtures' expected ship date, and it is far in the
// future ON PURPOSE. shipByUrgency reads the CLOCK: a date within a week grows
// the row a coloured "SHIP BY" reading that a date further out does not, so a
// fixture dated a few weeks ahead would change what these rows measure on a
// calendar date — the threshold moving under a sweep nobody had touched. Far
// out, the answer is the same every day this test is ever run.
const poOneLineShipDate = "2099-10-01"

const (
	poOneLineUUID1 = "3f2a9c11-7b4e-4d2a-9f10-5c8e1a6b0d33"
	poOneLineUUID2 = "b81d0e47-2c93-4a55-8e61-70f4d9a2c118"
	poOneLineUUID3 = "5a6c2f80-91be-4d17-a3c2-0e8b47f5d926"
)

// poOneLinePO is an order whose three lines are the three shapes a PO line
// comes in, each distinguishable AT THE FRONT of its row: an inventory line, a
// received line whose price was re-agreed afterwards, and a freeform line that
// names no part at all.
//
// The freeform line is not decoration. It is the only line for which the two
// new columns have nothing to show, and "this line names no part" and "we could
// not find out what part this line names" are different facts — so it is what
// makes the em-dash a tested answer rather than an accident.
func poOneLinePO() *omsapi.PurchaseOrder {
	return &omsapi.PurchaseOrder{
		ID: 1, Number: "PO-2026-0042", Status: "confirmed", StatusLabel: "Confirmed",
		SupplierDetails: "Acme Fasteners",
		Items: []omsapi.PurchaseOrderItem{
			{
				ID: 1, ItemType: "item_supplier",
				ItemDetails: map[string]any{
					"id": poOneLineUUID1, "name": "Hex bolt M8x40 zinc", "sku": "INT-4411",
					// The item's PRIMARY supplier's SKU — a flat legacy accessor
					// on InventoryItemSerializer, and the wrong vendor's part
					// number on an order placed with anybody else. It is here so
					// the SKU column can be caught reading it.
					"supplier_sku": "PRIMARY-0001",
				},
				ItemSupplierDetails:  &omsapi.POLineItemSupplier{SupplierSKU: "AF-99123"},
				QuantityOrdered:      250,
				QuantityPending:      250,
				UnitCostOrdered:      omsapi.DecimalString("3.5000"),
				EstimatedCost:        omsapi.DecimalString("875.00"),
				ExpectedShipmentDate: poOneLineShipDate,
			},
			{
				ID: 2, ItemType: "item_supplier",
				ItemDetails:         map[string]any{"id": poOneLineUUID2, "name": "Bearing 6203-2RS", "sku": "INT-7788"},
				ItemSupplierDetails: &omsapi.POLineItemSupplier{SupplierSKU: "BR-6203"},
				QuantityOrdered:     10, QuantityReceived: 10, IsFullyReceived: true,
				UnitCostOrdered: omsapi.DecimalString("4.0000"),
				UnitCostActual:  omsapi.DecimalString("4.5000"),
				EstimatedCost:   omsapi.DecimalString("40.00"),
				ActualCost:      omsapi.DecimalString("45.00"),
			},
			{
				ID: 3, Description: "Zip ties, assorted", ItemType: "freeform",
				QuantityOrdered: 4,
				UnitCostOrdered: omsapi.DecimalString("2.0000"),
				EstimatedCost:   omsapi.DecimalString("8.00"),
			},
		},
	}
}

// poOneLineRichPO is ONE line carrying every optional row the block form can
// draw: an association, a void with its reason, notes, an inventory note whose
// item name differs from the label, and a re-agreed price. It exists so the
// "nothing is lost" sweep is asked about a line that has something to lose.
//
// Its strings are short ON PURPOSE. The claim being tested is that each fact
// reaches the collapsed row, and a fixture whose threshold is past
// poOneLineMaxWidth would never collapse and would prove nothing — the sweep
// would pass having only ever seen the block form.
func poOneLineRichPO() *omsapi.PurchaseOrder {
	group := 3
	return &omsapi.PurchaseOrder{
		ID: 2, Number: "PO-2026-0043", Status: "confirmed", StatusLabel: "Confirmed",
		SupplierDetails: "Acme",
		Items: []omsapi.PurchaseOrderItem{{
			ID: 9, Description: "Belt", ItemType: "item_supplier",
			ItemDetails:         map[string]any{"id": poOneLineUUID3, "name": "V-belt A42", "sku": "INT-1"},
			ItemSupplierDetails: &omsapi.POLineItemSupplier{SupplierSKU: "VB-42"},
			QuantityOrdered:     6, QuantityReceived: 2,
			QuantityPending: 4,
			UnitCostOrdered: omsapi.DecimalString("5.0000"),
			UnitCostActual:  omsapi.DecimalString("6.0000"),
			EstimatedCost:   omsapi.DecimalString("30.00"),
			ActualCost:      omsapi.DecimalString("12.00"),
			IsVoided:        true, VoidReason: "wrong size",
			Notes:                "call first",
			ExpectedShipmentDate: poOneLineShipDate,
			WorkOrderRef:         &omsapi.WorkOrderRef{ShortID: "WO-1", DisplayTitle: "PM"},
			OwningGroup:          &group,
			OwningGroupRef:       &omsapi.OwningGroupRef{ID: 3, Name: "Shop"},
		}},
	}
}

// poOneLineKitPO is an order with a kit line on it. A kit's credit block names
// several OTHER items and how many of each one kit puts on the shelf; there is
// no row for that to ride, so the order keeps the block form at every width.
func poOneLineKitPO() *omsapi.PurchaseOrder {
	po := poOneLinePO()
	po.Items = append(po.Items, omsapi.PurchaseOrderItem{
		ID: 4, ItemType: "item_supplier", IsKitLine: true,
		ItemDetails:         map[string]any{"id": poOneLineUUID3, "name": "Lathe spares kit", "sku": "KIT-1"},
		ItemSupplierDetails: &omsapi.POLineItemSupplier{SupplierSKU: "LK-1"},
		QuantityOrdered:     2,
		UnitCostOrdered:     omsapi.DecimalString("10.0000"),
		EstimatedCost:       omsapi.DecimalString("20.00"),
		KitComponents: []omsapi.POKitComponent{
			{Component: "c1", ComponentName: "Shear pin", ComponentSKU: "SP-1", QuantityPerKit: 3, Quantity: 6},
			{Component: "c2", ComponentName: "Drive belt", ComponentSKU: "DB-1", QuantityPerKit: 1, Quantity: 2},
		},
	})
	return po
}

// ---------------------------------------------------------------------------
// Reading the CLIPPED pane
// ---------------------------------------------------------------------------

// poOneLinePane renders an order through a real Root at this width and returns
// the clipped pane's content rows, plain — the navigation column and the frame
// rule cut away, every escape sequence stripped.
//
// It fails rather than returning a short answer when the sheet is SCROLLING:
// every row-counting claim below is about the whole line band, and a band with
// its tail below the fold would make a "one row per line" assertion pass for a
// reason that has nothing to do with the layout.
func poOneLinePane(t *testing.T, po *omsapi.PurchaseOrder, width int) []string {
	t.Helper()
	s := NewPurchaseOrderDetailScreen(Deps{}, "1")
	s.loading = false
	s.po = po
	r := newTestRoot(s)
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: poOneLineHeight})
	root, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	view := root.View()
	if s.sheetScrolls() {
		t.Fatalf("the sheet scrolls at %dx%d, so the line band is not whole on the pane; "+
			"raise poOneLineHeight or shorten the fixture", width, poOneLineHeight)
	}
	return poPaneContentRows(view)
}

// poPaneContentRows strips a Root render down to the body pane's own rows.
// Root draws the nav column and a vertical rule to the left of every body row,
// so a row without the rule is chrome and a row with it carries the body after
// the LAST one.
func poPaneContentRows(view string) []string {
	var out []string
	for _, row := range strings.Split(stripANSI(view), "\n") {
		i := strings.LastIndex(row, "│")
		if i < 0 {
			continue
		}
		out = append(out, strings.TrimRight(row[i+len("│"):], " "))
	}
	return out
}

// poOneLineBand is the rows of the "Line items" band: everything from the
// heading to the blank row that closes it, heading excluded.
func poOneLineBand(t *testing.T, rows []string, wantLines int) []string {
	t.Helper()
	head := fmt.Sprintf("Line items (%d)", wantLines)
	start := -1
	for i, row := range rows {
		if strings.Contains(row, head) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		t.Fatalf("no %q heading on the pane:\n%s", head, strings.Join(rows, "\n"))
	}
	var band []string
	for _, row := range rows[start:] {
		if strings.TrimSpace(row) == "" {
			break
		}
		band = append(band, row)
	}
	return band
}

// poOneLineHeadOrder is the wide grid's headings left to right. The cell reader
// needs the NEXT heading to know where a column stops, and reading that off the
// rendered heading row rather than off the fit is what keeps this a check on
// what was DRAWN.
var poOneLineHeadOrder = []string{
	poOneLineNumHead, poOneLineItemHead, poOneLineQtyHead, poOneLineSKUHead,
	poOneLineUUIDHead, poOneLineCostHead, poOneLineShipHead,
}

// poOneLineCell reads the cell a row holds UNDER a named heading.
//
// It reads the column's whole SPAN — from this heading's start to the next
// heading's — and not a window the width of the heading, which is the first way
// this was written and which quietly cut every value longer than its own
// heading: "(not sent)" is ten cells under a nine-cell "Part UUID", so the
// reader chopped a character off and the assertion failed on a pane that was
// perfectly correct. A test helper that shortens what it reads reports a defect
// in itself as a defect in the code.
//
// Offsets are DISPLAY CELLS at both ends. The heading row is ASCII, so a byte
// index into it is a cell offset; a data row is not — it carries em dashes for
// the columns a line has nothing for, and a tick in its flag — so cellPrefix
// walks the row forward to the same cell offset instead of slicing it by byte.
//
// The cell to the LEFT is asserted not to have run into this one, because a
// right-aligned value wider than its own heading would start before the heading
// does and this reader would return a piece of it. That cannot happen while a
// heading is the widest thing in its column, and it is exactly the kind of thing
// that stops being true when somebody adds a value.
func poOneLineCell(t *testing.T, head, row, heading string) string {
	t.Helper()
	at := strings.Index(head, heading)
	if at < 0 {
		t.Fatalf("the heading row has no %q:\n%s", heading, head)
	}
	end := len(head)
	for i, h := range poOneLineHeadOrder {
		if h != heading || i+1 >= len(poOneLineHeadOrder) {
			continue
		}
		if next := strings.Index(head, poOneLineHeadOrder[i+1]); next > at {
			end = next
		}
	}
	start := len(cellPrefix(row, at))
	if start > len(row) {
		return ""
	}
	if at > 0 && strings.TrimSpace(row[:start]) != "" &&
		!strings.HasSuffix(row[:start], " ") {
		t.Fatalf("the cell left of %q runs into it, so this read would return a piece of the "+
			"wrong column:\n%s\n%s", heading, head, row)
	}
	rest := row[start:]
	return strings.TrimSpace(cellPrefix(rest, end-at))
}

// poOneLineCollapsed reports whether this pane is drawing the wide form. It
// keys on the Part UUID heading, which the block form has no column for and
// cannot produce at any width.
func poOneLineCollapsed(rows []string) bool {
	for _, row := range rows {
		if strings.Contains(row, poOneLineUUIDHead) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The boundary
// ---------------------------------------------------------------------------

// TestPOOneLine_TheWideFormIsDrawnOnOneSideOfOneBoundary: sweeping every pane
// Root draws, the wide form appears at every width from some threshold upward
// and at none below it, 80 is below it, and BOTH sides were really reached.
//
// The single boundary is the property, not the number. poBuildOneLineGrid takes
// the MAXIMUM over the order's lines precisely so the threshold is single by
// construction; asked per line, an order would draw some lines as rows and
// others as five-row blocks, in a columnar grid whose columns are budgeted from
// the set — and the two would not even line up. The width it comes to is
// REPORTED rather than asserted, because it is an output of the fixture's own
// values and moves with any of them.
func TestPOOneLine_TheWideFormIsDrawnOnOneSideOfOneBoundary(t *testing.T) {
	widths := poOneLineWidths()
	threshold, block, collapsed := 0, 0, 0
	for _, w := range widths {
		rows := poOneLinePane(t, poOneLinePO(), w)
		if !poOneLineCollapsed(rows) {
			block++
			if threshold != 0 {
				t.Errorf("width %d draws the block form after the wide form appeared at %d: "+
					"the boundary is not single", w, threshold)
			}
			continue
		}
		collapsed++
		if threshold == 0 {
			threshold = w
		}
	}
	switch {
	case collapsed == 0:
		t.Fatalf("no pane up to %d columns drew the wide form; the sweep never reached the "+
			"state it exists to check", poOneLineMaxWidth)
	case block == 0:
		t.Fatalf("every pane drew the wide form; the sweep never saw the block form, so it " +
			"cannot report a fall-back that stopped happening")
	}
	if rows := poOneLinePane(t, poOneLinePO(), 80); poOneLineCollapsed(rows) {
		t.Errorf("80 columns drew the wide form; 80 is the width this interface is modelled "+
			"on and it must keep the block form:\n%s", strings.Join(rows, "\n"))
	}
	t.Logf("the wide form is drawn from %d columns up (%d panes), the block form below it (%d panes)",
		threshold, collapsed, block)
}

// TestPOOneLine_ACollapsedOrderDrawsOneRowPerLine is the captain's ask, stated
// as an arithmetic the pane can be asked: at every width that collapses, the
// line band is a heading row plus exactly one row per line and nothing else.
//
// Counting is what makes this fail on a half-collapse. Asserting the three
// VALUES are on the pane would pass just as happily on a row that had spilled
// its readings onto a continuation underneath, which is the outcome this whole
// design refuses.
func TestPOOneLine_ACollapsedOrderDrawsOneRowPerLine(t *testing.T) {
	po := poOneLinePO()
	seen := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if !poOneLineCollapsed(rows) {
			continue
		}
		seen++
		band := poOneLineBand(t, rows, len(po.Items))
		if want := len(po.Items) + 1; len(band) != want {
			t.Fatalf("at %d columns the line band is %d rows, want %d (one heading + one per line):\n%s",
				w, len(band), want, strings.Join(band, "\n"))
		}
	}
	if seen == 0 {
		t.Fatalf("no width collapsed, so this asserted nothing")
	}
	t.Logf("checked %d collapsed panes", seen)
}

// TestPOOneLine_ACollapsedRowCarriesTheSKUTheUUIDAndTheSupplierCost: the three
// facts the captain named are on the SAME row as the line they belong to.
//
// Asked per ROW rather than per pane on purpose. "All three strings are
// somewhere on the pane" is satisfied by three different rows, which is the one
// answer this feature exists to stop.
func TestPOOneLine_ACollapsedRowCarriesTheSKUTheUUIDAndTheSupplierCost(t *testing.T) {
	po := poOneLinePO()
	want := []struct{ name, sku, uuid, cost string }{
		{"Hex bolt M8x40 zinc", "AF-99123", poOneLineUUID1, "$875.00"},
		{"Bearing 6203-2RS", "BR-6203", poOneLineUUID2, "$40.00"},
		// The freeform line names no part, so both new columns say so.
		{"Zip ties, assorted", "—", "—", "$8.00"},
	}
	seen := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if !poOneLineCollapsed(rows) {
			continue
		}
		seen++
		band := poOneLineBand(t, rows, len(po.Items))
		for _, c := range want {
			row := ""
			for _, r := range band {
				if strings.Contains(r, c.name) {
					row = r
					break
				}
			}
			if row == "" {
				t.Fatalf("at %d columns no row names %q:\n%s", w, c.name, strings.Join(band, "\n"))
			}
			for _, fact := range []string{c.sku, c.uuid, c.cost} {
				if !strings.Contains(row, fact) {
					t.Errorf("at %d columns the row for %q does not carry %q:\n%s",
						w, c.name, fact, row)
				}
			}
		}
	}
	if seen == 0 {
		t.Fatalf("no width collapsed, so this asserted nothing")
	}
}

// TestPOOneLine_TheSKUColumnIsTheSuppliersAndNotTheItems: a PO line carries
// THREE part-number-shaped strings and only one of them is what a buyer quotes
// at the vendor.
//
// item_details.sku is the makerspace's internal number; item_details.
// supplier_sku is a flat accessor for the item's PRIMARY supplier, which is the
// wrong vendor entirely on an order placed with anyone else; the right one is
// item_supplier_details.supplier_sku, which is what OMS builds every row of its
// own order-pad export from. The fixture carries all three as distinct values,
// so a column reading the wrong one fails here rather than shipping a part
// number that orders the wrong thing.
func TestPOOneLine_TheSKUColumnIsTheSuppliersAndNotTheItems(t *testing.T) {
	po := poOneLinePO()
	seen := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if !poOneLineCollapsed(rows) {
			continue
		}
		seen++
		band := poOneLineBand(t, rows, len(po.Items))
		head, row := band[0], ""
		for _, r := range band {
			if strings.Contains(r, "Hex bolt M8x40 zinc") {
				row = r
				break
			}
		}
		if row == "" {
			t.Fatalf("at %d columns no row for the inventory line:\n%s", w, strings.Join(band, "\n"))
		}
		// Read the column by its heading's position, so this cannot pass on a
		// value that merely appears somewhere else on the row.
		cell := poOneLineCell(t, head, row, poOneLineSKUHead)
		if cell != "AF-99123" {
			t.Errorf("at %d columns the %s column holds %q, want the item_supplier SKU %q "+
				"(the internal SKU is INT-4411 and the primary supplier's is PRIMARY-0001):\n%s",
				w, poOneLineSKUHead, cell, "AF-99123", row)
		}
		if strings.Contains(row, "PRIMARY-0001") {
			t.Errorf("at %d columns the row carries the PRIMARY supplier's SKU:\n%s", w, row)
		}
	}
	if seen == 0 {
		t.Fatalf("no width collapsed, so this asserted nothing")
	}
}

// TestPOOneLine_TheCostColumnIsTheLinesSupplierCost: the column carries OMS's
// estimated_cost — quantity_ORDERED × unit_cost_ordered, the price the supplier
// is charging for this line — and not the per-unit figure beside it, and not
// the money that has since been spent.
//
// The fixture's second line makes all four numbers distinct: unit 4.0000,
// actual unit 4.5000, line 40.00, spent 45.00. Any two of them being equal
// would let a wrong read pass.
func TestPOOneLine_TheCostColumnIsTheLinesSupplierCost(t *testing.T) {
	po := poOneLinePO()
	seen := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if !poOneLineCollapsed(rows) {
			continue
		}
		seen++
		band := poOneLineBand(t, rows, len(po.Items))
		head, row := band[0], ""
		for _, r := range band {
			if strings.Contains(r, "Bearing 6203-2RS") {
				row = r
				break
			}
		}
		if row == "" {
			t.Fatalf("at %d columns no row for the received line:\n%s", w, strings.Join(band, "\n"))
		}
		cell := poOneLineCell(t, head, row, poOneLineCostHead)
		if cell != "$40.00" {
			t.Errorf("at %d columns the %s column holds %q, want the line's ordered cost $40.00 "+
				"(the unit cost is $4.0000 and the money spent is $45.00):\n%s",
				w, poOneLineCostHead, cell, row)
		}
		// And the money actually spent is not lost by the column carrying the
		// other figure — it rides the row as a reading.
		if !strings.Contains(row, "actual $45.00") {
			t.Errorf("at %d columns the row drops what was actually spent:\n%s", w, row)
		}
	}
	if seen == 0 {
		t.Fatalf("no width collapsed, so this asserted nothing")
	}
}

// TestPOOneLine_TheUUIDIsNeverAbbreviated: at every pane Root draws, a part's
// id is on the screen WHOLE or it is not on the screen at all.
//
// This is the captain's instruction stated as an invariant over the whole width
// axis rather than as a property of the widths that happen to collapse: a
// shortened UUID names a different part, and the ellipsis that says it was cut
// does not make it name the right one. It sweeps the block form's widths too,
// where the id is not drawn at all, so a future reading of it on a narrow pane
// fails here as well.
func TestPOOneLine_TheUUIDIsNeverAbbreviated(t *testing.T) {
	po := poOneLinePO()
	whole, seen := 0, 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		for _, uuid := range []string{poOneLineUUID1, poOneLineUUID2} {
			for _, row := range rows {
				// The head of a UUID with anything after it that is not the
				// rest of the UUID is a UUID that was cut.
				for _, n := range []int{8, 18, 30, 35} {
					head := uuid[:n]
					if !strings.Contains(row, head) {
						continue
					}
					seen++
					if !strings.Contains(row, uuid) {
						t.Errorf("at %d columns a part id is on the pane cut to %d cells or fewer:\n%s",
							w, n, row)
						continue
					}
					whole++
				}
			}
		}
	}
	if seen == 0 {
		t.Fatalf("no pane up to %d columns drew a part id at all, so this asserted nothing",
			poOneLineMaxWidth)
	}
	t.Logf("%d of %d part-id sightings were whole", whole, seen)
}

// TestPOOneLine_NothingTheBlockFormSaysIsLost: every fact the block form draws
// for a line is still on the pane once that line collapses to a row.
//
// The fixture is one line carrying every optional row the block form has —
// association, void and its reason, notes, an inventory note whose item name
// differs from the label, a re-agreed price, a part-received balance. The
// wanted strings are read from what the block form ITSELF draws at a narrow
// pane rather than written out here, so a row this test forgot to name is a row
// the sweep still checks.
func TestPOOneLine_NothingTheBlockFormSaysIsLost(t *testing.T) {
	po := poOneLineRichPO()
	// What the BLOCK form draws for this line. Each of these is confirmed
	// against the 80-column pane below before it is required of the wide one,
	// so a string this test invented rather than observed fails here instead of
	// quietly making the sweep stricter than the claim it stands for.
	carried := []string{
		"Belt", poOneLineShipDate, "$12.00",
		"PART INT-1", "UNIT $5.0000", "received 2", "4 pending",
		"type Inventory item", "actual @ $6.0000",
		"ordered for: WO-1", "Shop", "inv item: V-belt A42", "SKU INT-1",
		"void reason: wrong size", "call first", "[voided]",
	}
	// What only the WIDE form has: the supplier's part number, the part's id,
	// and the line's ordered cost — which for a part-received line is not the
	// figure the block form's Cost column carries at all, since that one prefers
	// the money already spent. These are asserted ABSENT at 80, so a block form
	// that quietly grew them would fail here rather than making the sweep below
	// vacuous.
	gained := []string{"VB-42", poOneLineUUID3, "$30.00"}

	blockRows := poOneLinePane(t, po, 80)
	if poOneLineCollapsed(blockRows) {
		t.Fatalf("80 columns collapsed; this sweep needs the block form as its reference")
	}
	blockPane := strings.Join(blockRows, "\n")
	for _, want := range carried {
		if !strings.Contains(blockPane, want) {
			t.Fatalf("the block form does not draw %q at 80 columns, so it is not a fact the "+
				"wide form owes anybody:\n%s", want, blockPane)
		}
	}
	for _, gain := range gained {
		if strings.Contains(blockPane, gain) {
			t.Fatalf("the block form already draws %q at 80 columns, so it is not something "+
				"the wide form adds:\n%s", gain, blockPane)
		}
	}
	facts := append(append([]string(nil), carried...), gained...)

	seen := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if !poOneLineCollapsed(rows) {
			continue
		}
		seen++
		band := poOneLineBand(t, rows, len(po.Items))
		if len(band) != 2 {
			t.Fatalf("at %d columns the band is %d rows, want a heading and one row:\n%s",
				w, len(band), strings.Join(band, "\n"))
		}
		for _, want := range facts {
			if !strings.Contains(band[1], want) {
				t.Errorf("at %d columns the collapsed row drops %q:\n%s", w, want, band[1])
			}
		}
	}
	if seen == 0 {
		t.Fatalf("the rich fixture never collapsed up to %d columns, so this asserted nothing",
			poOneLineMaxWidth)
	}
	t.Logf("checked %d collapsed panes against %d facts", seen, len(facts))
}

// TestPOOneLine_AKitLineKeepsTheWholeOrderOnTheBlockForm: a kit line's credit
// block names several OTHER items and how many of each one kit puts on the
// shelf. There is no row for that to ride, and the grid answers for the ORDER,
// so one kit line keeps every line on the block form — at every width, however
// wide.
//
// IT IS POSITIVELY CONTROLLED, and without that it would be a check that cannot
// fail: "this order never collapsed" is equally true of an order that could not
// have collapsed for some other reason, and it passes with the whole wide form
// deleted. So the SAME order minus its kit line is swept alongside, and the
// sweep fails if that one never collapses either — which is what makes the kit
// line the thing being measured rather than the width.
func TestPOOneLine_AKitLineKeepsTheWholeOrderOnTheBlockForm(t *testing.T) {
	po := poOneLineKitPO()
	control := poOneLineKitPO()
	control.Items = control.Items[:len(control.Items)-1]
	controlCollapsed := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if poOneLineCollapsed(rows) {
			t.Fatalf("at %d columns an order with a kit line collapsed:\n%s",
				w, strings.Join(rows, "\n"))
		}
		if poOneLineCollapsed(poOneLinePane(t, control, w)) {
			controlCollapsed++
		}
	}
	if controlCollapsed == 0 {
		t.Fatalf("the same order without its kit line never collapsed either, so the kit line " +
			"is not what this sweep measured")
	}
	t.Logf("the kit line held the block form at every width; without it, %d panes collapsed",
		controlCollapsed)
	// And the block it kept the form for is really drawn.
	pane := strings.Join(poOneLinePane(t, po, poOneLineMaxWidth), "\n")
	for _, want := range []string{"component breakdown for all 2 kits", "Shear pin", "Drive belt"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the kit credit block does not draw %q:\n%s", want, pane)
		}
	}
}

// TestPOOneLine_TheBarNamesTheScrollKeysExactlyWhereTheCollapsedBodyMoves: the
// wide form makes the body SHORTER, which is exactly the kind of change that
// leaves a bar naming keys that no longer do anything.
//
// It is checked here because it cannot be checked anywhere else: every bar
// sweep in the package walks a width axis that stops at 120, and the collapsed
// body does not exist below that. Both directions are asserted — a named key
// that does not move the pane, and a moving key the bar does not name — and the
// claim is made on the CLIPPED pane, because an offset that moved while the
// terminal showed the same thing is standing rule 1 broken invisibly.
//
// A REFUSED PANE IS SKIPPED, and that is the layer's rule rather than a hole in
// this one: below the height jdeTooShort draws at, the movement keys are HELD
// and the bar goes on naming them on purpose — it is not drawn there, and its
// only remaining job is to be MEASURED, so shrinking it would make the refusal
// notice name a height that does not work. frameDrawn is asked rather than a
// height written down, and the skipped panes are counted so a sweep that
// skipped everything fails instead of passing.
func TestPOOneLine_TheBarNamesTheScrollKeysExactlyWhereTheCollapsedBodyMoves(t *testing.T) {
	po := poOneLinePO()
	collapsed, drawn, refused := 0, 0, 0
	for _, w := range poOneLineWidths() {
		if !poOneLineCollapsed(poOneLinePane(t, po, w)) {
			continue
		}
		collapsed++
		for _, h := range jdePaneHeights() {
			s := NewPurchaseOrderDetailScreen(Deps{}, "1")
			s.loading = false
			s.po = po
			r := newTestRoot(s)
			next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
			root := next.(Root)
			if !s.frameDrawn(0, s.sheetBar()) {
				refused++
				continue
			}
			drawn++
			named := barHas(s.sheetBar(), "UP/DN", "Scroll")
			before := root.View()
			s.Update(tea.KeyMsg{Type: tea.KeyDown})
			moved := root.View() != before
			if named != moved {
				t.Errorf("at %dx%d the bar names UP/DN=%v but the pane moves=%v", w, h, named, moved)
			}
		}
	}
	switch {
	case collapsed == 0:
		t.Fatalf("no width collapsed, so this asserted nothing")
	case drawn == 0:
		t.Fatalf("every collapsed pane refused to draw, so this asserted nothing")
	}
	t.Logf("checked %d drawn panes over %d collapsed widths (%d refused panes skipped)",
		drawn, collapsed, refused)
}

// TestPOOneLine_ALineThatNamesNoPartReadsDifferentlyFromAReplyThatDidNotSayWhich:
// the two new columns tell an ABSENCE from a SILENCE.
//
// A freeform line has no vendor part number and no part id, and never will —
// that is the order as somebody wrote it. A catalogue line whose reply arrived
// without its item_supplier_details block has both and we were not told them —
// that is a lookup to go and do. An em dash covering the pair would report the
// second as the first, on the column an operator quotes at a vendor.
//
// The silent line is built the way an older OMS answers: the relationship id is
// on the line and the nested block is not. Both rows are asserted on the SAME
// pane, so this cannot pass by the two cases never being compared.
func TestPOOneLine_ALineThatNamesNoPartReadsDifferentlyFromAReplyThatDidNotSayWhich(t *testing.T) {
	po := poOneLinePO()
	// An OMS that carries the relationship and not the block: item_supplier is
	// the pk, item_supplier_details is absent, and item_details with it.
	po.Items = append(po.Items, omsapi.PurchaseOrderItem{
		ID: 5, ItemType: "item_supplier", ItemSupplier: 7,
		Description:     "Coupling half",
		QuantityOrdered: 1,
		UnitCostOrdered: omsapi.DecimalString("9.0000"),
		EstimatedCost:   omsapi.DecimalString("9.00"),
	})
	seen := 0
	for _, w := range poOneLineWidths() {
		rows := poOneLinePane(t, po, w)
		if !poOneLineCollapsed(rows) {
			continue
		}
		seen++
		band := poOneLineBand(t, rows, len(po.Items))
		head := band[0]
		find := func(name string) string {
			t.Helper()
			for _, r := range band {
				if strings.Contains(r, name) {
					return r
				}
			}
			t.Fatalf("at %d columns no row names %q:\n%s", w, name, strings.Join(band, "\n"))
			return ""
		}
		silent, absent := find("Coupling half"), find("Zip ties, assorted")
		for _, col := range []string{poOneLineSKUHead, poOneLineUUIDHead} {
			gotSilent := poOneLineCell(t, head, silent, col)
			gotAbsent := poOneLineCell(t, head, absent, col)
			if gotSilent != poOneLineUnknown {
				t.Errorf("at %d columns the %s column reads %q for a line whose block the reply "+
					"did not carry, want %q:\n%s", w, col, gotSilent, poOneLineUnknown, silent)
			}
			if gotAbsent != poOneLineNone {
				t.Errorf("at %d columns the %s column reads %q for a line that names no part, "+
					"want %q:\n%s", w, col, gotAbsent, poOneLineNone, absent)
			}
			if gotSilent == gotAbsent {
				t.Errorf("at %d columns the %s column gives a silence and an absence the same "+
					"answer %q", w, col, gotSilent)
			}
		}
	}
	if seen == 0 {
		t.Fatalf("no width collapsed, so this asserted nothing")
	}
}
