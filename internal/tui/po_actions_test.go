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
		ID:                   "po-1",
		Number:               "PO-2026-0001",
		Status:               "confirmed",
		SupplierOrderNumber:  "SUP-1",
		SalesOrderNumber:     "SO-1",
		ExpectedDeliveryDate: "2026-08-01",
		Notes:                "handle with care",
		Items: []omsapi.PurchaseOrderItem{
			{ID: "line-1", Description: "Widget", QuantityOrdered: 5, EstimatedCost: omsapi.DecimalString("50.00")},
			{ID: "line-2", Description: "Gadget", QuantityOrdered: 2, ActualCost: omsapi.DecimalString("20.00"), ExpectedShipmentDate: "2026-07-10", IsVoided: true},
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

	// Add a freeform line.
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

	// Add a second line with an expected ship date.
	s.lineInputs[poLineFieldDesc].SetValue("Panel")
	s.lineInputs[poLineFieldQty].SetValue("1")
	s.lineInputs[poLineFieldCost].SetValue("")
	s.lineInputs[poLineFieldShipDate].SetValue("2026-09-01")
	s.addLine()
	if len(s.lines) != 2 {
		t.Fatalf("after second addLine, cart len = %d, want 2", len(s.lines))
	}
	if s.lines[1].item.ExpectedShipmentDate != "2026-09-01" {
		t.Errorf("line 1 ship date = %q", s.lines[1].item.ExpectedShipmentDate)
	}

	// 'd' from the source chooser enters review.
	s.updateSourcePhase(runeKey('d'))
	if s.phase != poPhaseReview {
		t.Fatalf("d should enter review phase, got %v", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "Cart (2 line(s))") {
		t.Errorf("review view missing cart summary: %q", out)
	}

	// ctrl+x in review removes the highlighted line.
	s.reviewCursor = 0
	s.updateReviewPhase(tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(s.lines) != 1 {
		t.Errorf("after ctrl+x remove, cart len = %d, want 1", len(s.lines))
	}
	if s.lines[0].label != "Panel" {
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

	// Bad ship date is rejected.
	s.lineInputs[poLineFieldQty].SetValue("2")
	s.lineInputs[poLineFieldShipDate].SetValue("07/20/2026")
	s.addLine()
	if len(s.lines) != 0 || !strings.Contains(s.errMsg, "ship date") {
		t.Errorf("bad ship date should be rejected; lines=%d err=%q", len(s.lines), s.errMsg)
	}
}

func TestPOCreate_DoneRequiresLine(t *testing.T) {
	s := NewPurchaseOrderCreateScreen(Deps{})
	s.supplierID = 7
	s.phase = poPhaseSource
	s.updateSourcePhase(runeKey('d'))
	if s.phase == poPhaseReview {
		t.Errorf("d with an empty cart should not enter review")
	}
	if s.errMsg == "" {
		t.Errorf("expected an error when submitting an empty cart")
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
	if s.rowCount() != poEditMetaCount+2 {
		t.Errorf("rowCount = %d, want %d", s.rowCount(), poEditMetaCount+2)
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

	// Move the cursor onto the first line row and open the editor.
	s.cursor = poEditMetaCount // first line
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

	// Line 1 prefers actual cost + carries a ship date.
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
	if out := s.View(); !strings.Contains(out, "Void line item") {
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
	out := s.viewList()
	if !strings.Contains(out, "sales-order.pdf") || !strings.Contains(out, "SO confirmation") {
		t.Errorf("attachment list view = %q", out)
	}

	// Delete-confirm prompt renders.
	s.confirmingDelete = true
	if out := s.viewList(); !strings.Contains(out, "Delete") {
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

func TestPODetail_FooterHintStatusGating(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	s.po = &omsapi.PurchaseOrder{ID: "po-1", Status: "confirmed"}
	hint := s.footerHint()
	for _, want := range []string{"E edit", "A attachments", "d mark delivered", "v void"} {
		if !strings.Contains(hint, want) {
			t.Errorf("confirmed-PO footer missing %q: %s", want, hint)
		}
	}

	// A received PO can't be voided or delivered.
	s.po = &omsapi.PurchaseOrder{ID: "po-1", Status: "received", IsFullyReceived: true}
	hint = s.footerHint()
	if strings.Contains(hint, "v void") {
		t.Errorf("received PO should not offer void: %s", hint)
	}
	if strings.Contains(hint, "d mark delivered") {
		t.Errorf("received PO should not offer mark-delivered: %s", hint)
	}
}

func TestPODetail_VoidAndDeliverModalsRender(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	s.loading = false
	s.po = &omsapi.PurchaseOrder{ID: "po-1", Number: "PO-2026-0001", Status: "confirmed"}

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
