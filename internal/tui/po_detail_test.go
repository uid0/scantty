package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func poRuneKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// poDetailRow returns the body line that carries `label` as its columnar
// prompt — "  Supplier ..... Acme Supply". The leader is what makes the match
// unambiguous: it appears on field rows and nowhere else, so a value that
// merely happens to contain the word cannot be mistaken for the row.
func poDetailRow(t *testing.T, body, label string) string {
	t.Helper()
	want := label + jdeLeader
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no %q row in body:\n%s", label, body)
	return ""
}

// poLineRows renders one line item's whole block — the grid row plus the
// readings wrapped under it — as the detail sheet draws it.
func poLineRows(li omsapi.PurchaseOrderItem, supplier string) string {
	fit := poFitLineGrid(76, []omsapi.PurchaseOrderItem{li})
	return strings.Join(poLineBlock(1, li, supplier, fit, 76), "\n")
}

func TestPOLineGridCostColumnAligns(t *testing.T) {
	// Two rows with different magnitudes — the Cost column is right-aligned to
	// a fixed width, so the decimal points land in the same screen column on
	// both rows and an operator scanning a PO can compare them at a glance.
	a := omsapi.PurchaseOrderItem{
		Description:     "Bolt",
		ItemDetails:     map[string]any{"sku": "M3-HEX-BOLT"},
		UnitCostOrdered: omsapi.DecimalString("0.05"),
		QuantityOrdered: 100,
		ActualCost:      omsapi.DecimalString("5.00"),
	}
	b := omsapi.PurchaseOrderItem{
		Description:     "Widget",
		ItemDetails:     map[string]any{"sku": "WIDGET-XL-2024"},
		UnitCostOrdered: omsapi.DecimalString("12.50"),
		QuantityOrdered: 3,
		ActualCost:      omsapi.DecimalString("37.50"),
	}

	// One fit across both lines, as the sheet builds it: the columns are
	// budgeted from every line on the order, so both rows share them.
	fit := poFitLineGrid(76, []omsapi.PurchaseOrderItem{a, b})
	rowA := poLineBlock(1, a, "", fit, 76)[0]
	rowB := poLineBlock(2, b, "", fit, 76)[0]
	dotA := strings.Index(rowA, ".")
	dotB := strings.Index(rowB, ".")
	if dotA < 0 || dotA != dotB {
		t.Errorf("cost decimal points didn't align: %d vs %d\n  a=%q\n  b=%q", dotA, dotB, rowA, rowB)
	}
	// And both totals are actually on the row.
	if !strings.Contains(rowA, "5.00") || !strings.Contains(rowB, "37.50") {
		t.Errorf("a cost went missing:\n  a=%q\n  b=%q", rowA, rowB)
	}
}

func TestPOLinePartNumberPrefersSKUOverAssetTag(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		ItemDetails:  map[string]any{"sku": "SKU-123"},
		AssetDetails: map[string]any{"asset_tag": "ASSET-9"},
	}
	if got := poLinePartNumber(li); got != "SKU-123" {
		t.Errorf("part number = %q, want the SKU", got)
	}
	// And the readings under the row quote it rather than the asset tag.
	out := poLineRows(li, "")
	if !strings.Contains(out, "PART SKU-123") {
		t.Errorf("expected the SKU on the line's readings, got:\n%s", out)
	}
	if strings.Contains(out, "PART ASSET-9") {
		t.Errorf("asset tag should be suppressed when a SKU exists, got:\n%s", out)
	}
}

func TestPOLinePartNumberFallsBackToAssetTag(t *testing.T) {
	// Asset POs have no SKU but do have asset_tag — surface the tag.
	li := omsapi.PurchaseOrderItem{AssetDetails: map[string]any{"asset_tag": "TAG-42"}}
	if got := poLinePartNumber(li); got != "TAG-42" {
		t.Errorf("part number = %q, want the asset tag", got)
	}
	if out := poLineRows(li, ""); !strings.Contains(out, "PART TAG-42") {
		t.Errorf("expected the asset tag on the line's readings, got:\n%s", out)
	}
}

func TestPOLineCostCellHandlesMissingCosts(t *testing.T) {
	// No cost data: the cell renders the em-dash placeholder rather than a
	// literal $0.00, which would imply "free" instead of "unknown".
	li := omsapi.PurchaseOrderItem{QuantityOrdered: 0}
	if got := poLineCostCell(li); got != "—" {
		t.Errorf("cost cell = %q, want the em-dash placeholder", got)
	}
	if out := poLineRows(li, ""); !strings.Contains(out, "—") {
		t.Errorf("expected em-dash for missing fields, got:\n%s", out)
	}
}

func TestPOLineCostCellPrefersActualOverEstimated(t *testing.T) {
	// When both totals exist, actual is the source of truth (matches the
	// meta-line semantics the detail body has always had).
	li := omsapi.PurchaseOrderItem{
		ItemDetails:   map[string]any{"sku": "X"},
		ActualCost:    omsapi.DecimalString("100.00"),
		EstimatedCost: omsapi.DecimalString("99.99"),
	}
	out := poLineRows(li, "")
	if !strings.Contains(out, "100.00") {
		t.Errorf("expected actual cost (100.00) on the row, got:\n%s", out)
	}
	if strings.Contains(out, "99.99") {
		t.Errorf("estimated cost (99.99) leaked through when actual was set:\n%s", out)
	}
}

func TestShipByUrgencyOverdueWithoutActual(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		ExpectedShipmentDate: "2020-01-01",
	}
	if got := shipByUrgency(li); got != "overdue" {
		t.Errorf("expected overdue, got %q", got)
	}
}

func TestShipByUrgencyOverdueIgnoredWhenAlreadyShipped(t *testing.T) {
	// An actual_shipment_date means the work is done — no urgency to
	// flash, even if the expected ship-by has long since passed.
	li := omsapi.PurchaseOrderItem{
		ExpectedShipmentDate: "2020-01-01",
		ActualShipmentDate:   "2020-02-15",
	}
	if got := shipByUrgency(li); got != "shipped" {
		t.Errorf("expected shipped, got %q", got)
	}
}

func TestShipByUrgencyEmptyWhenNoExpected(t *testing.T) {
	li := omsapi.PurchaseOrderItem{}
	if got := shipByUrgency(li); got != "" {
		t.Errorf("expected empty urgency, got %q", got)
	}
}

// TestPOLineShowsBothShipDates: a line that was due on one date and shipped on
// another surfaces BOTH — the ship-by in the grid's own column, the actual as a
// reading under it, since the grid has no column for a second date.
func TestPOLineShowsBothShipDates(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		Description:          "Widget",
		ExpectedShipmentDate: "2020-01-01",
		ActualShipmentDate:   "2020-02-15",
	}
	out := poLineRows(li, "")
	if !strings.Contains(out, "2020-01-01") {
		t.Errorf("missing the ship-by date, got:\n%s", out)
	}
	if !strings.Contains(out, "SHIPPED 2020-02-15") {
		t.Errorf("missing the shipped marker, got:\n%s", out)
	}
}

// TestPOLineShipByOnly: nothing has shipped, so no SHIPPED marker — and a
// ship-by long past is called out in its own token, because the grid column
// carries the date but not the alarm.
func TestPOLineShipByOnly(t *testing.T) {
	li := omsapi.PurchaseOrderItem{Description: "Widget", ExpectedShipmentDate: "2020-01-01"}
	out := poLineRows(li, "")
	if !strings.Contains(out, "SHIP BY 2020-01-01") {
		t.Errorf("an overdue line should say so, got:\n%s", out)
	}
	if strings.Contains(out, "SHIPPED") {
		t.Errorf("unexpected shipped marker, got:\n%s", out)
	}
}

// TestPOShipTokensQuietWhenNotUrgent: a ship-by comfortably in the future is
// already legible in the grid's Ship date column, and a second plain copy of it
// under the row would be noise.
func TestPOShipTokensQuietWhenNotUrgent(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		Description:          "Widget",
		ExpectedShipmentDate: time.Now().UTC().AddDate(0, 2, 0).Format("2006-01-02"),
	}
	if got := poShipTokens(li, poFitLineGrid(76, []omsapi.PurchaseOrderItem{li})); len(got) != 0 {
		t.Errorf("a distant ship-by should add no reading, got %+v", got)
	}
	if out := poLineRows(li, ""); !strings.Contains(out, li.ExpectedShipmentDate) {
		t.Errorf("the grid column should still carry the date:\n%s", out)
	}
}

// TestPODetail_OrderPadKeyOpensAndFetches presses 'x' on a loaded PO and
// asserts the overlay opens in a loading state, hits the export-order endpoint
// (trailing slash, GET), and — once the pad arrives — fills the scroller,
// emits the copy+status batch, and stays open until esc closes it.
func TestPODetail_OrderPadKeyOpensAndFetches(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"csv":"part#,qty\nABC-1,5\n","text":"ABC-1\t5\nDEF-2\t3","filename":"PO-2026-0060-order.csv","supplier":"Grainger","line_count":2,"missing_sku":[]}`))
	}))
	defer srv.Close()

	s := NewPurchaseOrderDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "9")
	s.loading = false
	s.po = &omsapi.PurchaseOrder{ID: 9, Status: "sent"}

	_, cmd := s.Update(poRuneKey("x"))
	if !s.orderPad || !s.orderPadLoading {
		t.Fatalf("x should open the overlay loading (orderPad=%v loading=%v)", s.orderPad, s.orderPadLoading)
	}
	if cmd == nil {
		t.Fatal("x should kick off a fetch cmd")
	}
	msg := cmd() // perform the request
	pm, ok := msg.(poOrderPadMsg)
	if !ok || pm.err != nil {
		t.Fatalf("expected a successful poOrderPadMsg, got %#v", msg)
	}
	if gotMethod != "GET" || gotPath != "/api/reorders/purchase-orders/9/export-order/" {
		t.Fatalf("request = %s %s, want GET .../po-9/export-order/", gotMethod, gotPath)
	}

	_, cmd2 := s.Update(pm)
	if s.orderPadLoading {
		t.Error("loading should clear once the pad arrives")
	}
	if !s.orderPad {
		t.Error("overlay should stay open to show the pad")
	}
	if cmd2 == nil {
		t.Error("a successful load should emit the copy+status batch cmd")
	}
	if body := strings.Join(s.orderPadLines().text, "\n"); !strings.Contains(body, "ABC-1") || !strings.Contains(body, "DEF-2") {
		t.Errorf("order-pad body missing part numbers: %q", body)
	}

	// While the overlay is open every key routes here (WantsRawInput); esc closes.
	if !s.WantsRawInput() {
		t.Error("overlay should claim raw input so scroll/close keys stay local")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.orderPad {
		t.Error("esc should close the overlay")
	}
}

// TestPODetail_OrderPadBackendErrorKeepsOverlay confirms a fetch failure (e.g.
// a 404 before #855 deploys) surfaces an error in the overlay rather than
// crashing or silently closing.
func TestPODetail_OrderPadBackendErrorKeepsOverlay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"Not found."}`, http.StatusNotFound)
	}))
	defer srv.Close()

	s := NewPurchaseOrderDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "9")
	s.loading = false
	s.po = &omsapi.PurchaseOrder{ID: 9, Status: "sent"}

	_, cmd := s.Update(poRuneKey("x"))
	pm, ok := cmd().(poOrderPadMsg)
	if !ok || pm.err == nil {
		t.Fatalf("expected a poOrderPadMsg carrying an error, got %#v", pm)
	}
	s.Update(pm)
	if s.orderPadLoading {
		t.Error("loading should clear even on error")
	}
	if !s.orderPad || s.orderPadErr == "" {
		t.Errorf("overlay should stay open showing the error (orderPad=%v err=%q)", s.orderPad, s.orderPadErr)
	}
	if !strings.Contains(s.View(), "✗") {
		t.Errorf("error overlay should render the failure marker, got %q", s.View())
	}
}

// TestPODetail_OrderPadBodyEmptyState renders the empty-state note when no line
// carries a supplier part number.
func TestPODetail_OrderPadBodyEmptyState(t *testing.T) {
	s := &PurchaseOrderDetailScreen{
		orderPadExport: &omsapi.OrderPadExport{Text: "", LineCount: 0, MissingSku: []string{"Widget"}},
	}
	if body := strings.Join(s.orderPadLines().text, "\n"); !strings.Contains(body, "No lines") {
		t.Errorf("empty-state body = %q", body)
	}
}

// TestPODetail_OrderPadToastAndLevel exercises the status summary + level for a
// clean copy, a copy with omitted lines, and nothing-to-order.
func TestPODetail_OrderPadToastAndLevel(t *testing.T) {
	s := &PurchaseOrderDetailScreen{}

	s.orderPadExport = &omsapi.OrderPadExport{Text: "A\t1", LineCount: 1}
	if lvl := s.orderPadToastLevel(); lvl != StatusOK {
		t.Errorf("clean-copy level = %v, want StatusOK", lvl)
	}
	if msg := s.orderPadToast(); !strings.Contains(msg, "copied") || !strings.Contains(msg, "1 line") {
		t.Errorf("clean-copy toast = %q", msg)
	}

	s.orderPadExport = &omsapi.OrderPadExport{Text: "A\t1", LineCount: 1, MissingSku: []string{"Widget", "Gear"}}
	if lvl := s.orderPadToastLevel(); lvl != StatusWarn {
		t.Errorf("missing-sku level = %v, want StatusWarn", lvl)
	}
	if msg := s.orderPadToast(); !strings.Contains(msg, "2 lines missing part #") {
		t.Errorf("missing-sku toast = %q", msg)
	}

	s.orderPadExport = &omsapi.OrderPadExport{Text: "", LineCount: 0}
	if lvl := s.orderPadToastLevel(); lvl != StatusWarn {
		t.Errorf("nothing-orderable level = %v, want StatusWarn", lvl)
	}
	if msg := s.orderPadToast(); !strings.Contains(msg, "no lines") {
		t.Errorf("nothing-orderable toast = %q", msg)
	}
}

// TestPODetail_BarNamesOrderPad confirms the export affordance is advertised on
// the persistent action bar, which since the columnar conversion is the only
// place an operator can learn the key.
func TestPODetail_BarNamesOrderPad(t *testing.T) {
	s := &PurchaseOrderDetailScreen{po: &omsapi.PurchaseOrder{Status: "sent"}}
	if !barHas(s.sheetBar(), "x", "Order pad") {
		t.Errorf("action bar missing order-pad affordance: %+v", s.sheetBar())
	}
}

// ---------------------------------------------------------------------------
// Line target-type label (sc-be24)
// ---------------------------------------------------------------------------

func TestPOLineTypeLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		{"item_supplier", "Inventory item"},
		{"inventory_item", "Inventory item"},
		{"asset", "Asset"},
		{"freeform", "Freeform"},
		{"", "—"},
		{"something_new", "—"},
	}
	for _, c := range cases {
		if got := poLineTypeLabel(c.in); got != c.want {
			t.Errorf("poLineTypeLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPODetail_LineShowsFriendlyType checks the readings under the row carry
// the readable label rather than the raw wire token.
func TestPODetail_LineShowsFriendlyType(t *testing.T) {
	out := poLineRows(omsapi.PurchaseOrderItem{
		Description: "Bolt",
		ItemType:    "item_supplier",
	}, "Acme")
	if !strings.Contains(out, "type Inventory item") {
		t.Errorf("expected friendly type label, got:\n%s", out)
	}
	if strings.Contains(out, "item_supplier") {
		t.Errorf("raw wire token leaked into the readings:\n%s", out)
	}
}

// TestPODetail_LineOmitsTypeWhenAbsent keeps the pre-existing behaviour: an
// empty item_type renders no type chunk at all, rather than a bare "type —".
func TestPODetail_LineOmitsTypeWhenAbsent(t *testing.T) {
	if out := poLineRows(omsapi.PurchaseOrderItem{Description: "Bolt"}, "Acme"); strings.Contains(out, "type ") {
		t.Errorf("empty item_type should render no type chunk, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// Supplier purchase/pricing agreement (op-yoos)
// ---------------------------------------------------------------------------

// TestPODetail_ShowsSupplierAgreement: an order placed under contract pricing
// says so, using the {id, name} the serializer nests so no second request is
// needed.
func TestPODetail_ShowsSupplierAgreement(t *testing.T) {
	id := 4
	s := &PurchaseOrderDetailScreen{po: &omsapi.PurchaseOrder{
		Number:               "PO-2026-0042",
		Status:               "draft",
		SupplierDetails:      "Acme Supply",
		SupplierAgreement:    &id,
		SupplierAgreementRef: &omsapi.SupplierAgreementRef{ID: 4, Name: "2026 nonprofit pricing"},
	}}
	out := s.renderBody()
	if row := poDetailRow(t, out, "Agreement"); !strings.Contains(row, "2026 nonprofit pricing") {
		t.Errorf("the Agreement row should name the agreement, got %q", row)
	}
}

// TestPODetail_OmitsAgreementWhenAbsent: most orders cite none, and a row
// saying so on every PO would crowd out the ones that do.
func TestPODetail_OmitsAgreementWhenAbsent(t *testing.T) {
	s := &PurchaseOrderDetailScreen{po: &omsapi.PurchaseOrder{
		Number:          "PO-2026-0043",
		Status:          "draft",
		SupplierDetails: "Acme Supply",
	}}
	if out := s.renderBody(); strings.Contains(out, "Agreement"+jdeLeader) {
		t.Errorf("an order with no agreement should render no agreement row:\n%s", out)
	}
}
