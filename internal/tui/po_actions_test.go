package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// runeKey builds a KeyMsg for a single-rune command key (its String() is the
// rune), so tests can drive the phase handlers the way the app dispatcher does.
func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func samplePO() *omsapi.PurchaseOrder {
	return &omsapi.PurchaseOrder{
		ID:                   1,
		Number:               "PO-2026-0001",
		Status:               "confirmed",
		SupplierOrderNumber:  "SUP-1",
		SalesOrderNumber:     "SO-1",
		ExpectedDeliveryDate: "2026-08-01",
		Notes:                "handle with care",
		// Wire-faithful lines: the backend DERIVES estimated_cost and
		// actual_cost from the stored columns (quantity_ordered ×
		// unit_cost_ordered, quantity_received × unit_cost_actual), so a
		// fixture that carries a total without the columns behind it is a shape
		// the API cannot produce.
		Items: []omsapi.PurchaseOrderItem{
			{
				ID: 1, Description: "Widget",
				QuantityOrdered: 5, UnitCostOrdered: omsapi.DecimalString("10.0000"),
				EstimatedCost: omsapi.DecimalString("50.00"),
			},
			{
				ID: 2, Description: "Gadget",
				QuantityOrdered: 2, QuantityReceived: 2,
				UnitCostOrdered:      omsapi.DecimalString("10.0000"),
				UnitCostActual:       omsapi.DecimalString("10.0000"),
				EstimatedCost:        omsapi.DecimalString("20.00"),
				ActualCost:           omsapi.DecimalString("20.00"),
				ExpectedShipmentDate: "2026-07-10", IsVoided: true,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Multi-line create cart
// ---------------------------------------------------------------------------

func TestPOCreate_MultiLineCart(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	s.phase = poPhaseSource

	// Add a freeform line — freeform keeps the (optional) unit-cost input.
	s.lineInputs[poLineFieldDesc].SetValue("Custom bracket")
	s.lineInputs[poLineFieldQty].SetValue("3")
	s.lineInputs[poLineFieldCost].SetValue("4.25")
	s.addLine()
	if len(s.lines) != 1 {
		t.Fatalf("after first addLine, cart len = %d, want 1", len(s.lines))
	}
	if s.phase != poPhaseSource {
		t.Errorf("addLine should return to source phase, got %v", s.phase)
	}
	if s.lines[0].label != "Custom bracket" || s.lines[0].item.Quantity != 3 {
		t.Errorf("line 0 = %+v", s.lines[0])
	}
	if s.lines[0].item.UnitCost == nil || *s.lines[0].item.UnitCost != 4.25 {
		t.Errorf("line 0 unit cost = %v", s.lines[0].item.UnitCost)
	}

	// Add a second line sourced from a picker (item-supplier backed, as the
	// reorder/inventory pickers do). The catalog price is prefilled into the
	// cost field, so accepting the form carries it as the line's unit_cost.
	itemSup := 42
	s.enterLinePhase(&itemSup, nil, "Reorder widget", 5, 9.99, 0, 0)
	s.addLine()
	if len(s.lines) != 2 {
		t.Fatalf("after second addLine, cart len = %d, want 2", len(s.lines))
	}
	if s.lines[1].item.ItemSupplierID == nil || *s.lines[1].item.ItemSupplierID != 42 {
		t.Errorf("line 1 item-supplier id = %v", s.lines[1].item.ItemSupplierID)
	}
	if s.lines[1].item.UnitCost == nil || *s.lines[1].item.UnitCost != 9.99 {
		t.Errorf("line 1 unit cost = %v, want the prefilled catalog price 9.99", s.lines[1].item.UnitCost)
	}
	if s.lines[1].item.ExpectedShipmentDate != "" {
		t.Errorf("no line should carry a ship date, got %q", s.lines[1].item.ExpectedShipmentDate)
	}

	// 'd' from the source chooser enters review.
	s.updateSourcePhase(runeKey('d'), 0)
	if s.phase != poPhaseReview {
		t.Fatalf("d should enter review phase, got %v", s.phase)
	}
	// The cart's own count rides on cartTotalRows, which is a PINNED HEADER
	// block rather than anything in the body: tagged onto the cart's last row
	// it was two lines past the bottom at 80x24, because jdeLines.Window keeps
	// a block's START when it cannot fit the whole of it.
	if out := s.View(); !strings.Contains(out, "(2 line items)") {
		t.Errorf("review view missing the cart total: %q", out)
	}

	// ctrl+x in review removes the highlighted line.
	s.reviewCursor = 0
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlX}, 0)
	if len(s.lines) != 1 {
		t.Errorf("after ctrl+x remove, cart len = %d, want 1", len(s.lines))
	}
	if s.lines[0].label != "Reorder widget" {
		t.Errorf("wrong line removed; remaining = %q", s.lines[0].label)
	}
}

func TestPOCreate_AddLineValidation(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	s.phase = poPhaseLine

	// Freeform with no description is rejected.
	s.lineInputs[poLineFieldDesc].SetValue("")
	s.lineInputs[poLineFieldQty].SetValue("2")
	s.addLine()
	if len(s.lines) != 0 || s.errMsg == "" {
		t.Errorf("empty freeform description should be rejected; lines=%d err=%q", len(s.lines), s.errMsg)
	}

	// Bad quantity is rejected.
	s.lineInputs[poLineFieldDesc].SetValue("Thing")
	s.lineInputs[poLineFieldQty].SetValue("0")
	s.addLine()
	if len(s.lines) != 0 {
		t.Errorf("zero quantity should be rejected, lines=%d", len(s.lines))
	}

	// A negative unit cost is rejected (freeform keeps the cost input).
	s.lineInputs[poLineFieldQty].SetValue("2")
	s.lineInputs[poLineFieldCost].SetValue("-3")
	s.addLine()
	if len(s.lines) != 0 || !strings.Contains(s.errMsg, "unit cost") {
		t.Errorf("negative unit cost should be rejected; lines=%d err=%q", len(s.lines), s.errMsg)
	}
}

// TestPOCreate_ItemSupplierLineCostIsOptional verifies the reorder/inventory
// (item-supplier-backed) line flow offers an EDITABLE cost seeded from the
// catalog price, and that clearing it hands pricing back to the backend
// (sc-5yr's default, kept as the default — sc-gnzw).
func TestPOCreate_ItemSupplierLineCostIsOptional(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7

	// Item-supplier line: the cost field is offered and prefilled from the
	// catalog price, alongside description, quantity and the expected date.
	itemSup := 11
	s.enterLinePhase(&itemSup, nil, "Bolt", 4, 2.50, 0, 0)
	if got := s.lineFields(); !hasField(got, poLineFieldCost) {
		t.Fatalf("item-supplier line fields = %v, want a cost field", got)
	}
	if got := s.lineInputs[poLineFieldCost].Value(); got != "2.5" {
		t.Errorf("cost prefill = %q, want the catalog price %q", got, "2.5")
	}
	if out := s.View(); !strings.Contains(out, "Unit cost") {
		t.Errorf("item-supplier line should render a unit-cost input:\n%s", out)
	}
	// Cleared → no unit_cost, so the backend prices it from the catalog.
	s.lineInputs[poLineFieldCost].SetValue("")
	s.addLine()
	if len(s.lines) != 1 {
		t.Fatalf("cart len = %d, want 1", len(s.lines))
	}
	if s.lines[0].item.UnitCost != nil {
		t.Errorf("a cleared cost must omit unit_cost, got %v", *s.lines[0].item.UnitCost)
	}

	// Freeform line: the cost field is active again (and carries no date field —
	// the backend's freeform branch ignores expected_shipment_date).
	s.enterLinePhase(nil, nil, "", 0, 0, 0, 0)
	if got := s.lineFields(); !hasField(got, poLineFieldCost) || hasField(got, poLineFieldDate) {
		t.Errorf("freeform line fields = %v, want [desc qty cost]", got)
	}
	if out := s.View(); !strings.Contains(out, "Unit cost") {
		t.Errorf("freeform line should render a unit-cost input:\n%s", out)
	}
}

func TestPOCreate_DoneRequiresLine(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	s.phase = poPhaseSource
	s.updateSourcePhase(runeKey('d'), 0)
	if s.phase == poPhaseReview {
		t.Errorf("d with an empty cart should not enter review")
	}
	if s.sourceNote.text == "" {
		t.Errorf("expected the chooser to say why d did nothing on an empty cart")
	}
}

// ---------------------------------------------------------------------------
// PO edit screen
// ---------------------------------------------------------------------------

func TestPOEdit_Hydrate(t *testing.T) {
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
	if got := s.meta[poMetaSupplierOrder].Value(); got != "SUP-1" {
		t.Errorf("supplier order = %q", got)
	}
	if got := s.meta[poMetaSalesOrder].Value(); got != "SO-1" {
		t.Errorf("sales order = %q", got)
	}
	if got := s.meta[poMetaExpectedDelivery].Value(); got != "2026-08-01" {
		t.Errorf("expected delivery = %q", got)
	}
	if got := s.meta[poMetaNotes].Value(); got != "handle with care" {
		t.Errorf("notes = %q", got)
	}
	// Three bands of navigable rows: the metadata fields, the two order-level
	// association rows (op-shb9), then one row per line — samplePO has two.
	if want := poEditMetaCount + poEditAssocCount + 2; s.rowCount() != want {
		t.Errorf("rowCount = %d, want %d", s.rowCount(), want)
	}
}

func TestPOEdit_MetadataDateValidation(t *testing.T) {
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
	s.meta[poMetaExpectedDelivery].SetValue("not-a-date")
	s.saveMetadata()
	if s.errMsg == "" {
		t.Errorf("expected a validation error for a bad expected-delivery date")
	}
}

func TestPOEdit_LineEditorPrefillAndNav(t *testing.T) {
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())

	// Move the cursor onto the first line row and open the editor. Line rows
	// start after the metadata fields AND the association rows.
	s.cursor = poEditLineBase
	if _, ok := s.onLineRow(); !ok {
		t.Fatalf("cursor %d should be a line row", s.cursor)
	}
	s.openLineEditor(0)
	if s.phase != poEditPhaseLine {
		t.Fatalf("openLineEditor should switch to line phase")
	}
	// Line 0 has estimated cost 50.00 (no actual) -> prefilled cost.
	if got := s.lineInputs[poLineEditCost].Value(); got != "50.00" {
		t.Errorf("cost prefill = %q, want 50.00", got)
	}

	// Line 1 prefers the actual price + carries a ship date. The prefill is
	// unit_cost_actual × quantity_ORDERED — the basis update_item reads a
	// line_cost on — not actual_cost's received-so-far subtotal.
	s.openLineEditor(1)
	if got := s.lineInputs[poLineEditCost].Value(); got != "20.00" {
		t.Errorf("line 1 cost prefill = %q, want 20.00", got)
	}
	if got := s.lineInputs[poLineEditShipDate].Value(); got != "2026-07-10" {
		t.Errorf("line 1 ship prefill = %q", got)
	}
}

func TestPOEdit_SaveLineBadCost(t *testing.T) {
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
	s.openLineEditor(0)
	s.lineInputs[poLineEditCost].SetValue("-5")
	s.saveLine()
	if s.errMsg == "" {
		t.Errorf("negative line cost should be rejected")
	}
}

func TestPOEdit_VoidLineRequiresReason(t *testing.T) {
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
	s.openVoidLine(0)
	s.voidReason.SetValue("")
	s.updateVoidLine(tea.KeyMsg{Type: tea.KeyEnter})
	if s.errMsg == "" {
		t.Errorf("empty void reason should be rejected")
	}
}

func TestPOEdit_RenderSmoke(t *testing.T) {
	s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
	out := s.View()
	for _, want := range []string{"Order details", "Supplier order #", "Line items (2)", "Widget", "[voided]"} {
		if !strings.Contains(out, want) {
			t.Errorf("edit view missing %q:\n%s", want, out)
		}
	}
	// Line-editor and void views render without panicking.
	s.openLineEditor(0)
	if out := s.View(); !strings.Contains(out, "Edit line") {
		t.Errorf("line editor view = %q", out)
	}
	s.openVoidLine(1)
	// The prompt PINS what it is about to strike off, the way the delete
	// confirm does: a heading saying only "Void line item" is a row a short
	// pane may drop, and it names no line.
	if out := s.View(); !strings.Contains(out, "Void: ") {
		t.Errorf("void line view = %q", out)
	}
}

// ---------------------------------------------------------------------------
// PO attachments screen
// ---------------------------------------------------------------------------

func TestPOAttachments_UploadValidation(t *testing.T) {
	po := samplePO()
	s := NewPurchaseOrderAttachmentsScreen(Deps{}, po)
	s.phase = poAttachPhaseUpload

	// Empty path.
	s.uploadInputs[poAttachFieldPath].SetValue("")
	s.submitUpload()
	if s.errMsg == "" || s.uploading {
		t.Errorf("empty path should error without starting an upload; err=%q uploading=%v", s.errMsg, s.uploading)
	}

	// Nonexistent path.
	s.uploadInputs[poAttachFieldPath].SetValue("/no/such/file/hopefully-1234.pdf")
	s.submitUpload()
	if s.errMsg == "" || s.uploading {
		t.Errorf("missing file should error without starting an upload; err=%q", s.errMsg)
	}
}

func TestPOAttachments_RenderSmoke(t *testing.T) {
	po := samplePO()
	po.Attachments = []omsapi.PurchaseOrderAttachment{
		{ID: 1, FileName: "sales-order.pdf", Description: "SO confirmation"},
	}
	s := NewPurchaseOrderAttachmentsScreen(Deps{}, po)
	out := s.View()
	if !strings.Contains(out, "sales-order.pdf") || !strings.Contains(out, "SO confirmation") {
		t.Errorf("attachment list view = %q", out)
	}

	// Delete-confirm prompt renders, naming the file it is about.
	s.confirmingDelete = true
	if out := s.View(); !strings.Contains(out, "Delete attachment") || !strings.Contains(out, "sales-order.pdf") {
		t.Errorf("delete-confirm view missing prompt: %q", out)
	}

	// Upload form renders both fields.
	s.confirmingDelete = false
	s.phase = poAttachPhaseUpload
	if out := s.View(); !strings.Contains(out, "File path") || !strings.Contains(out, "Description") {
		t.Errorf("upload form view = %q", out)
	}
}

// ---------------------------------------------------------------------------
// PO detail: new modals + gating
// ---------------------------------------------------------------------------

func TestPODetail_MarkDeliveredGating(t *testing.T) {
	cases := map[string]bool{
		"draft":              false,
		"sent":               true,
		"confirmed":          true,
		"partially_received": true,
		"received":           false,
		"voided":             false,
	}
	for status, want := range cases {
		if got := poCanMarkDelivered(status); got != want {
			t.Errorf("poCanMarkDelivered(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestPODetail_BarStatusGating: the action bar offers a state transition only
// where the backend would accept it — the bar is the operator's only source of
// what works here, so a key it names must do something.
func TestPODetail_BarStatusGating(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "1")
	s.po = &omsapi.PurchaseOrder{ID: 1, Status: "confirmed"}
	bar := s.sheetBar()
	for _, want := range [][2]string{{"E", "Edit"}, {"A", "Files"}, {"d", "Delivered"}, {"v", "Void"}} {
		if !barHas(bar, want[0], want[1]) {
			t.Errorf("confirmed-PO bar missing %q=%q: %+v", want[0], want[1], bar)
		}
	}

	// A received PO can't be voided or delivered.
	s.po = &omsapi.PurchaseOrder{ID: 1, Status: "received", IsFullyReceived: true}
	bar = s.sheetBar()
	if barHas(bar, "v", "Void") {
		t.Errorf("received PO should not offer void: %+v", bar)
	}
	if barHas(bar, "d", "Delivered") {
		t.Errorf("received PO should not offer mark-delivered: %+v", bar)
	}
}

func TestPODetail_VoidAndDeliverModalsRender(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "1")
	s.loading = false
	s.po = &omsapi.PurchaseOrder{ID: 1, Number: "PO-2026-0001", Status: "confirmed"}

	s.openVoidForm()
	if !s.WantsRawInput() {
		t.Errorf("void modal should claim raw input")
	}
	if out := s.View(); !strings.Contains(out, "Void purchase order") {
		t.Errorf("void modal view = %q", out)
	}
	s.voiding = false

	s.openDeliverForm()
	if !s.WantsRawInput() {
		t.Errorf("deliver modal should claim raw input")
	}
	out := s.View()
	if !strings.Contains(out, "Mark delivered") {
		t.Errorf("deliver modal view = %q", out)
	}
	// Delivery date defaults to a value; a blank date must be rejected.
	s.deliverInputs[poDeliverDate].SetValue("")
	s.submitDeliver()
	if s.deliverErr == "" {
		t.Errorf("blank delivery date should be rejected")
	}
}
