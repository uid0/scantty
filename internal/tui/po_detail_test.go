package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func poRuneKey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestJDECostLineDecimalsAlign(t *testing.T) {
	// Two rows with different magnitudes — decimal points should
	// land in the same screen column on both rows so an operator
	// scanning a PO can compare unit costs at a glance.
	a := omsapi.PurchaseOrderItem{
		ItemDetails:     map[string]any{"sku": "M3-HEX-BOLT"},
		UnitCostOrdered: omsapi.DecimalString("0.05"),
		QuantityOrdered: 100,
		ActualCost:      omsapi.DecimalString("5.00"),
	}
	b := omsapi.PurchaseOrderItem{
		ItemDetails:     map[string]any{"sku": "WIDGET-XL-2024"},
		UnitCostOrdered: omsapi.DecimalString("12.50"),
		QuantityOrdered: 3,
		ActualCost:      omsapi.DecimalString("37.50"),
	}

	la := jdeCostLine(a)
	lb := jdeCostLine(b)

	// Decimal point in the UNIT field.
	uA := strings.Index(la, "UNIT")
	uB := strings.Index(lb, "UNIT")
	if uA != uB {
		t.Fatalf("UNIT label moved between rows: %d vs %d", uA, uB)
	}
	dotA := strings.Index(la[uA:], ".")
	dotB := strings.Index(lb[uB:], ".")
	if dotA != dotB {
		t.Errorf("unit-cost decimal points didn't align: %d vs %d\n  a=%q\n  b=%q", dotA, dotB, la, lb)
	}

	// Decimal point in the TOTAL field.
	tA := strings.Index(la, "TOTAL")
	tB := strings.Index(lb, "TOTAL")
	if tA != tB {
		t.Fatalf("TOTAL label moved between rows: %d vs %d", tA, tB)
	}
	tdotA := strings.Index(la[tA:], ".")
	tdotB := strings.Index(lb[tB:], ".")
	if tdotA != tdotB {
		t.Errorf("total-cost decimal points didn't align: %d vs %d\n  a=%q\n  b=%q", tdotA, tdotB, la, lb)
	}
}

func TestJDECostLinePrefersSKUOverAssetTag(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		ItemDetails:  map[string]any{"sku": "SKU-123"},
		AssetDetails: map[string]any{"asset_tag": "ASSET-9"},
	}
	out := jdeCostLine(li)
	if !strings.Contains(out, "SKU-123") {
		t.Errorf("expected SKU in row, got %q", out)
	}
	if strings.Contains(out, "ASSET-9") {
		t.Errorf("asset tag should be suppressed when a SKU exists, got %q", out)
	}
}

func TestJDECostLineFallsBackToAssetTag(t *testing.T) {
	// Asset POs have no SKU but do have asset_tag — surface the tag.
	li := omsapi.PurchaseOrderItem{
		AssetDetails: map[string]any{"asset_tag": "TAG-42"},
	}
	out := jdeCostLine(li)
	if !strings.Contains(out, "TAG-42") {
		t.Errorf("expected asset tag in row, got %q", out)
	}
}

func TestJDECostLineHandlesMissingCosts(t *testing.T) {
	// No cost data: the row should render with the em-dash placeholder
	// rather than crashing or showing literal $0.00 (which would imply
	// "free" instead of "unknown").
	li := omsapi.PurchaseOrderItem{QuantityOrdered: 0}
	out := jdeCostLine(li)
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash for missing fields, got %q", out)
	}
}

func TestJDECostLinePrefersActualOverEstimated(t *testing.T) {
	// When both totals exist, actual is the source of truth (matches
	// the existing meta-line semantics in renderPOLineItem).
	li := omsapi.PurchaseOrderItem{
		ItemDetails:   map[string]any{"sku": "X"},
		ActualCost:    omsapi.DecimalString("100.00"),
		EstimatedCost: omsapi.DecimalString("99.99"),
	}
	out := jdeCostLine(li)
	if !strings.Contains(out, "100.00") {
		t.Errorf("expected actual cost (100.00) in row, got %q", out)
	}
	if strings.Contains(out, "99.99") {
		t.Errorf("estimated cost (99.99) leaked through when actual was set, got %q", out)
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

func TestRenderShipDatesIncludesBothWhenSet(t *testing.T) {
	li := omsapi.PurchaseOrderItem{
		ExpectedShipmentDate: "2020-01-01",
		ActualShipmentDate:   "2020-02-15",
	}
	out := renderShipDates(li)
	if !strings.Contains(out, "SHIP BY 2020-01-01") {
		t.Errorf("missing ship-by label, got %q", out)
	}
	if !strings.Contains(out, "SHIPPED 2020-02-15") {
		t.Errorf("missing shipped label, got %q", out)
	}
}

func TestRenderShipDatesShipByOnly(t *testing.T) {
	li := omsapi.PurchaseOrderItem{ExpectedShipmentDate: "2020-01-01"}
	out := renderShipDates(li)
	if !strings.Contains(out, "SHIP BY 2020-01-01") {
		t.Errorf("expected ship-by, got %q", out)
	}
	if strings.Contains(out, "SHIPPED") {
		t.Errorf("unexpected shipped marker, got %q", out)
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

	s := NewPurchaseOrderDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "po-9")
	s.loading = false
	s.po = &omsapi.PurchaseOrder{ID: "po-9", Status: "sent"}

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
	if gotMethod != "GET" || gotPath != "/api/reorders/purchase-orders/po-9/export-order/" {
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
	if body := s.renderOrderPadBody(); !strings.Contains(body, "ABC-1") || !strings.Contains(body, "DEF-2") {
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

	s := NewPurchaseOrderDetailScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "po-9")
	s.loading = false
	s.po = &omsapi.PurchaseOrder{ID: "po-9", Status: "sent"}

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
	if body := s.renderOrderPadBody(); !strings.Contains(body, "No lines") {
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

// TestPODetail_FooterHintIncludesOrderPad confirms the export affordance is
// advertised in the key legend.
func TestPODetail_FooterHintIncludesOrderPad(t *testing.T) {
	s := &PurchaseOrderDetailScreen{po: &omsapi.PurchaseOrder{Status: "sent"}}
	if !strings.Contains(s.footerHint(), "x order pad") {
		t.Errorf("footer hint missing order-pad affordance: %q", s.footerHint())
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

// TestPODetail_LineShowsFriendlyType checks the meta row carries the readable
// label rather than the raw wire token.
func TestPODetail_LineShowsFriendlyType(t *testing.T) {
	var b strings.Builder
	renderPOLineItem(&b, 1, omsapi.PurchaseOrderItem{
		Description: "Bolt",
		ItemType:    "item_supplier",
	}, "Acme")
	out := b.String()
	if !strings.Contains(out, "type Inventory item") {
		t.Errorf("expected friendly type label, got:\n%s", out)
	}
	if strings.Contains(out, "item_supplier") {
		t.Errorf("raw wire token leaked into the meta row:\n%s", out)
	}
}

// TestPODetail_LineOmitsTypeWhenAbsent keeps the pre-existing behaviour: an
// empty item_type renders no type chunk at all, rather than a bare "type —".
func TestPODetail_LineOmitsTypeWhenAbsent(t *testing.T) {
	var b strings.Builder
	renderPOLineItem(&b, 1, omsapi.PurchaseOrderItem{Description: "Bolt"}, "Acme")
	if out := b.String(); strings.Contains(out, "type ") {
		t.Errorf("empty item_type should render no type chunk, got:\n%s", out)
	}
}
