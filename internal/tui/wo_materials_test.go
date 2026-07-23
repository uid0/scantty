package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TUI half of op-768w — actual materials & cost on a work order (sc-ggdx).
//
// Three things the screen owes the operator that the client can't: an EMPTY
// material list has to be reachable (a corrective work order has no template, so
// it starts with nothing and adding the first line is the whole point), the two
// backend refusals have to be stated before a round trip rather than after one,
// and an unpriced line must read as unpriced rather than as free.

func woKey(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// woMatReq is one request the fake OMS saw.
type woMatReq struct {
	method string
	path   string
	body   map[string]any
	fields map[string][]string
}

// materialServer records every request and answers all of them with the same
// work-order payload — which is all the screen needs, since every material write
// is followed by a re-fetch.
func materialServer(t *testing.T, wo string, log *[]woMatReq) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := woMatReq{method: r.Method, path: r.URL.Path}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
			} else {
				req.fields = r.MultipartForm.Value
			}
		} else if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			_ = json.Unmarshal(raw, &req.body)
		}
		*log = append(*log, req)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wo))
	}))
}

// writes filters the request log down to the calls that changed something — the
// screen re-fetches after each one, and those GETs are noise here.
func writes(log []woMatReq) []woMatReq {
	var out []woMatReq
	for _, r := range log {
		if r.method != http.MethodGet {
			out = append(out, r)
		}
	}
	return out
}

func costWO() *omsapi.WorkOrder {
	applied := 2
	item := "it-9"
	return &omsapi.WorkOrder{
		ID: "wo1", Title: "Spindle service", Status: "in_progress",
		MaintenanceItem: "mi-1", ActualMaterialCost: "38.37",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{
			ID: "mu-1", Material: "mm-1", MaterialName: "Way oil",
			InventoryItemName: "Vactra No. 2",
			QuantityPlanned:   "2.00", QuantityUsed: "2.00", Unit: "L",
			UnitCost: "12.79", ActualCost: "25.58", WasUsed: true,
			AppliedQuantity: &applied, StockApplied: true,
		}, {
			ID: "mu-2", MaterialName: "Shop rags", IsAdHoc: true,
			InventoryItem: &item, InventoryItemName: "Shop rags",
			QuantityPlanned: "1.00", QuantityUsed: "1.00", Unit: "pack",
			UnitCost: "12.79", ActualCost: "12.79", WasUsed: true,
			ReceiptURL: "https://oms.example/media/receipts/r.jpg",
		}},
	}
}

// TestWODetailMaterialsOpenWhenEmpty: the corrective case. Before op-768w M was
// refused with "no materials to toggle", which locked the operator out of the
// one screen that could record what the job consumed.
func TestWODetailMaterialsOpenWhenEmpty(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Title: "Fix the lathe", Status: "open"})

	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeMaterials {
		t.Fatalf("mode = %v, want the material list to open on an empty work order", s.mode)
	}
	out := s.View()
	if !strings.Contains(out, "Press a to add") {
		t.Errorf("empty state must point at the add key: %q", out)
	}
	if !strings.Contains(s.footerHint(), "M materials") {
		t.Errorf("footer must offer M with no materials yet: %q", s.footerHint())
	}
}

// TestWODetailMaterialRowRendersCost: what each line cost, where it draws stock
// from, and — the point of the ad-hoc tag — which lines were added on the job
// rather than copied from the template.
func TestWODetailMaterialRowRendersCost(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, costWO())
	out := s.renderBody()

	if !strings.Contains(out, "cost $25.58 ($12.79/unit)") {
		t.Errorf("line cost missing: %q", out)
	}
	if !strings.Contains(out, "(added)") {
		t.Errorf("ad-hoc line must be tagged as added: %q", out)
	}
	if !strings.Contains(out, "stock: Vactra No. 2") {
		t.Errorf("template line must name the stock it draws from: %q", out)
	}
	if !strings.Contains(out, "−2 from stock") {
		t.Errorf("live decrement missing: %q", out)
	}
	if !strings.Contains(out, "planned 2.00 L") || !strings.Contains(out, "used 2.00 L") {
		t.Errorf("planned/used quantities missing: %q", out)
	}
	if !strings.Contains(out, "receipt https://oms.example/media/receipts/r.jpg") {
		t.Errorf("receipt link missing: %q", out)
	}
}

// TestWODetailUnpricedMaterialReadsUnpriced: a line nobody priced must not
// render as $0.00 — free and unrecorded are different answers.
func TestWODetailUnpricedMaterialReadsUnpriced(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{ID: "mu-1", MaterialName: "Grease", WasUsed: true}}})
	out := s.renderBody()

	if !strings.Contains(out, "no cost recorded") {
		t.Errorf("unpriced line must say so: %q", out)
	}
	if strings.Contains(out, "cost $0.00") {
		t.Errorf("unpriced must not render as free: %q", out)
	}
}

// TestWODetailMaterialTotalsActualVsEstimate: the pair the materials view exists
// to produce. The estimate is the TEMPLATE's per-unit price applied to the
// quantities this work order planned — 2.00 × $15.00 — so the added line, which
// has no template row, is in the actual but never in the estimate.
func TestWODetailMaterialTotalsActualVsEstimate(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, costWO())
	next, _ := s.Update(woEstimatesLoadedMsg{key: "mi-1", costs: map[string]omsapi.DecimalString{"mm-1": "15.00"}})
	s = next.(*WorkOrderDetailScreen)

	out := s.renderBody()
	if !strings.Contains(out, "Actual material cost: $38.37") {
		t.Errorf("actual total missing: %q", out)
	}
	if !strings.Contains(out, "estimated $30.00") {
		t.Errorf("estimate must come from the template's per-unit price: %q", out)
	}
	if !strings.Contains(out, "$8.37 over") {
		t.Errorf("over/under comparison missing: %q", out)
	}
}

// TestWODetailMaterialTotalsWithoutEstimate: a corrective work order has an
// actual and nothing to measure it against, exactly like the stopwatch when its
// template carries no estimated time. It says so rather than implying $0.00.
func TestWODetailMaterialTotalsWithoutEstimate(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open", ActualMaterialCost: "18.42",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{ID: "mu-1", MaterialName: "Bolts", IsAdHoc: true,
			QuantityUsed: "1.00", UnitCost: "18.42", ActualCost: "18.42", WasUsed: true}}})

	out := s.renderBody()
	if !strings.Contains(out, "Actual material cost: $18.42") {
		t.Errorf("actual total missing: %q", out)
	}
	if !strings.Contains(out, "no estimate for this job") {
		t.Errorf("missing estimate must be stated: %q", out)
	}
}

// TestWODetailMaterialTotalsFallBackToLines: a backend older than op-768w sends
// no work-order total, and the lines still add up to one. Only lines actually
// marked used count — a planned-but-unused material cost nothing.
func TestWODetailMaterialTotalsFallBackToLines(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{
			{ID: "mu-1", MaterialName: "Used", ActualCost: "10.00", WasUsed: true},
			{ID: "mu-2", MaterialName: "Not used", ActualCost: "99.00"},
		}})

	if out := s.renderBody(); !strings.Contains(out, "Actual material cost: $10.00") {
		t.Errorf("unused line must not count toward the total: %q", out)
	}
}

// TestWODetailNoEstimateFetchWithoutTemplate: corrective work has no PM item, so
// there is nothing to fetch and the screen must not go looking.
func TestWODetailNoEstimateFetchWithoutTemplate(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	if s.estimatesFor != "" {
		t.Errorf("estimatesFor = %q, want nothing to fetch", s.estimatesFor)
	}
	if s.loadEstimates() != nil {
		t.Errorf("a work order with no maintenance item must not fetch estimates")
	}
}

// TestWODetailEstimatesFetchedOncePerTemplate: the estimate rides a second call,
// so a reload must not re-issue it.
func TestWODetailEstimatesFetchedOncePerTemplate(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, costWO())
	if s.estimatesFor != "mi-1" {
		t.Fatalf("estimatesFor = %q, want the template the first load asked for", s.estimatesFor)
	}
	if s.loadEstimates() != nil {
		t.Errorf("second load must reuse the fetched estimates")
	}
}

// TestWODetailRemoveRefusesTemplateLine: a template row is the frozen copy of
// what the job was supposed to be and prints on the sign-off sheet. The backend
// 400s on it; the screen says so without spending the round trip.
func TestWODetailRemoveRefusesTemplateLine(t *testing.T) {
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	s := loadWO(t, deps, costWO())
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	s.materialCursor = 0 // the template-derived line
	next, cmd := s.Update(woRuneKey("d"))
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		cmd()
	}
	if got := writes(log); len(got) != 0 {
		t.Errorf("removing a template line must not reach the API, got %+v", got)
	}
	if s.actionPending {
		t.Errorf("refusal must not leave the picker gated on an in-flight write")
	}
}

// TestWODetailRemoveRefusesAppliedStock: deleting a line still holding a
// decrement would strand the units taken out of inventory. Un-toggling restores
// them, so that is what the refusal has to say.
func TestWODetailRemoveRefusesAppliedStock(t *testing.T) {
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	applied := 1
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{ID: "mu-2", MaterialName: "Shop rags",
			IsAdHoc: true, WasUsed: true, AppliedQuantity: &applied, StockApplied: true}}})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(woRuneKey("d"))
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		cmd()
	}
	if got := writes(log); len(got) != 0 {
		t.Errorf("a line with stock applied must not be deleted, got %+v", got)
	}
}

// TestWODetailRemovesAdHocLine: the case that IS allowed.
func TestWODetailRemovesAdHocLine(t *testing.T) {
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{ID: "mu-2", MaterialName: "Shop rags", IsAdHoc: true}}})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(woRuneKey("d"))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("removing an unused ad-hoc line must fire a request")
	}
	msg := cmd().(woMaterialRemovedMsg)
	if msg.err != nil {
		t.Fatalf("remove: %v", msg.err)
	}
	if msg.name != "Shop rags" {
		t.Errorf("removal must carry the name for the status line, got %q", msg.name)
	}
	got := writes(log)
	if len(got) != 1 || got[0].method != http.MethodDelete {
		t.Fatalf("requests = %+v", got)
	}
	if got[0].path != "/api/inventory/work-orders/wo1/materials/mu-2/" {
		t.Errorf("path = %q", got[0].path)
	}
}

// TestWODetailCostEditRefusesFrozenLine: the price freezes with the decrement so
// the recorded spend can't drift from the stock movement backing it.
func TestWODetailCostEditRefusesFrozenLine(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, costWO())
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	s.materialCursor = 0 // stock_applied
	next, _ = s.Update(woRuneKey("c"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeMaterials {
		t.Errorf("mode = %v, want the editor refused on a frozen line", s.mode)
	}
}

// TestWODetailCostEditSaves: the toggle is the only endpoint that writes a
// price, so the save has to carry the line's CURRENT was_used unchanged —
// pricing an already-marked out-of-pocket buy must not un-mark it.
func TestWODetailCostEditSaves(t *testing.T) {
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{ID: "mu-2", MaterialName: "Bolts", IsAdHoc: true, WasUsed: true}}})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("c"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeMaterialCost {
		t.Fatalf("mode = %v, want the cost editor", s.mode)
	}
	next, _ = s.Update(woRuneKey("18.42"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("enter must save")
	}
	if msg := cmd().(woMaterialCostSavedMsg); msg.err != nil {
		t.Fatalf("save cost: %v", msg.err)
	}
	got := writes(log)
	if len(got) != 1 || got[0].method != http.MethodPatch {
		t.Fatalf("requests = %+v", got)
	}
	if got[0].path != "/api/inventory/work-orders/wo1/materials/mu-2/toggle/" {
		t.Errorf("path = %q", got[0].path)
	}
	if got[0].body["unit_cost"] != "18.42" {
		t.Errorf("unit_cost = %v", got[0].body["unit_cost"])
	}
	if got[0].body["was_used"] != true {
		t.Errorf("was_used = %v, want the line's current value unchanged", got[0].body["was_used"])
	}
}

// TestWODetailCostEditRejectsNonNumber: caught on the form, so a typo never
// costs a round trip.
func TestWODetailCostEditRejectsNonNumber(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open",
		MaterialUsage: []omsapi.WorkOrderMaterialUsage{{ID: "mu-2", MaterialName: "Bolts", IsAdHoc: true}}})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("c"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("lots"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		t.Fatal("a non-numeric price must not be sent")
	}
	if s.costErr == "" || s.mode != woModeMaterialCost {
		t.Errorf("expected an in-form error, got %q mode=%v", s.costErr, s.mode)
	}
}

// TestWODetailAddMaterialPosts drives the add form the way an operator does:
// open it from the (empty) material list, type the name, tab to the price, and
// submit. No receipt was picked, so it stays plain JSON.
func TestWODetailAddMaterialPosts(t *testing.T) {
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Title: "Fix the lathe", Status: "open"})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("a"))
	s = next.(*WorkOrderDetailScreen)
	if s.mode != woModeAddMaterial {
		t.Fatalf("mode = %v, want the add form", s.mode)
	}
	next, _ = s.Update(woRuneKey("Drive belt"))
	s = next.(*WorkOrderDetailScreen)
	// name → quantity → unit → unit cost
	for i := 0; i < 3; i++ {
		next, _ = s.Update(woKey(tea.KeyTab))
		s = next.(*WorkOrderDetailScreen)
	}
	next, _ = s.Update(woRuneKey("42.10"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("enter must submit the add form")
	}
	msg := cmd().(woMaterialAddedMsg)
	if msg.err != nil {
		t.Fatalf("add material: %v", msg.err)
	}

	got := writes(log)
	if len(got) != 1 || got[0].method != http.MethodPost {
		t.Fatalf("requests = %+v", got)
	}
	if got[0].path != "/api/inventory/work-orders/wo1/materials/" {
		t.Errorf("path = %q", got[0].path)
	}
	if got[0].body["material_name"] != "Drive belt" || got[0].body["unit_cost"] != "42.10" {
		t.Errorf("body = %v", got[0].body)
	}
	// The quantity field is pre-filled with 1 — the common case is one of the
	// thing you just bought, and it also seeds quantity_planned server-side.
	if got[0].body["quantity_used"] != "1" {
		t.Errorf("quantity_used = %v, want the form default", got[0].body["quantity_used"])
	}
	if _, ok := got[0].body["inventory_item"]; ok {
		t.Errorf("no stock picked — the link must be omitted, got %v", got[0].body)
	}
}

// TestWODetailAddMaterialRequiresName: the one required field, caught in-form.
func TestWODetailAddMaterialRequiresName(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("a"))
	s = next.(*WorkOrderDetailScreen)
	next, cmd := s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		t.Fatal("a nameless material must not be sent")
	}
	if s.amErr == "" {
		t.Errorf("expected an in-form error")
	}
}

// TestWODetailAddMaterialLinksStock: picking a stock item is what makes marking
// the line used decrement inventory, and it seeds the name and the price from
// the item — a default, not a lock.
func TestWODetailAddMaterialLinksStock(t *testing.T) {
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("a"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woItemsLoadedMsg{items: []omsapi.Item{{ID: "it-9", Name: "Shop rags", SKU: "RAG-1", UnitCost: "12.79"}}})
	s = next.(*WorkOrderDetailScreen)

	s.amCursor = woMatItem
	s.syncAddMaterialFocus()
	next, _ = s.Update(woRuneKey(" "))
	s = next.(*WorkOrderDetailScreen)
	if !s.amPicking {
		t.Fatal("space on the stock field must open the picker")
	}
	// Row 0 clears the link; row 1 is the item.
	next, _ = s.Update(woRuneKey("j"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if s.amItemID != "it-9" {
		t.Fatalf("amItemID = %q, want the picked item", s.amItemID)
	}
	if got := s.amInputs[woMatUnitCost].Value(); got != "12.79" {
		t.Errorf("unit cost = %q, want it seeded from the item", got)
	}
	if got := s.amInputs[woMatName].Value(); got != "Shop rags" {
		t.Errorf("name = %q, want it seeded from the item", got)
	}

	next, cmd := s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("enter must submit")
	}
	if msg := cmd().(woMaterialAddedMsg); msg.err != nil {
		t.Fatalf("add material: %v", msg.err)
	}
	got := writes(log)
	if len(got) != 1 || got[0].body["inventory_item"] != "it-9" {
		t.Fatalf("requests = %+v", got)
	}
}

// TestWODetailAddMaterialSendsReceiptAsMultipart: a picked receipt turns the add
// into a multipart upload, and every scalar still rides with it.
func TestWODetailAddMaterialSendsReceiptAsMultipart(t *testing.T) {
	receipt := t.TempDir() + "/receipt.jpg"
	if err := os.WriteFile(receipt, []byte("JPEGDATA"), 0o600); err != nil {
		t.Fatalf("write receipt fixture: %v", err)
	}
	var log []woMatReq
	srv := materialServer(t, `{"id":"wo1"}`, &log)
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}

	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("a"))
	s = next.(*WorkOrderDetailScreen)
	s.amInputs[woMatName].SetValue("Misc supplies")
	s.amInputs[woMatUnitCost].SetValue("18.42")
	s.amInputs[woMatReceipt].SetValue(receipt)

	next, cmd := s.Update(woKey(tea.KeyEnter))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("enter must submit")
	}
	if msg := cmd().(woMaterialAddedMsg); msg.err != nil {
		t.Fatalf("add material: %v", msg.err)
	}
	got := writes(log)
	if len(got) != 1 {
		t.Fatalf("requests = %+v", got)
	}
	if f := got[0].fields["material_name"]; len(f) != 1 || f[0] != "Misc supplies" {
		t.Errorf("multipart scalars lost: %+v", got[0].fields)
	}
	if f := got[0].fields["unit_cost"]; len(f) != 1 || f[0] != "18.42" {
		t.Errorf("unit_cost = %v", got[0].fields["unit_cost"])
	}
}

// TestWODetailAddedMaterialLandsBackOnTheList: adding records the plan; TOGGLING
// is what moves the stock, so the flow has to end where that key lives.
func TestWODetailAddedMaterialLandsBackOnTheList(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://unused"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	next, _ := s.Update(woRuneKey("M"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("a"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woMaterialAddedMsg{name: "Drive belt"})
	s = next.(*WorkOrderDetailScreen)

	if s.mode != woModeMaterials {
		t.Errorf("mode = %v, want the material list", s.mode)
	}
	if !strings.Contains(s.actionMsg, "mark it used") {
		t.Errorf("status must point at the toggle: %q", s.actionMsg)
	}
}
