package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// draftPOJSON is a one-order page whose only PO is a saved draft — the order a
// create leaves behind, which before op-nr6h parity was only findable by
// scrolling the mixed list.
const draftPOJSON = `{"count":1,"next":null,"previous":null,"results":[` +
	`{"id":11,"po_number":"PO-2026-009","supplier_details":"Acme Supplies",` +
	`"status":"draft","estimated_total":"75.00","total_items":2,"total_quantity":4}]}`

// newPurchasingListScreen builds the Purchasing list through the REAL app
// wiring (newScreenFor), so these tests break if the spec stops carrying the
// status-filter cycle rather than silently testing a copy of it.
func newPurchasingListScreen(t *testing.T, deps Deps) *ListScreen {
	t.Helper()
	scr, ok := newScreenFor(WSPurchasing, deps).(*ListScreen)
	if !ok {
		t.Fatalf("Purchasing screen = %T, want *ListScreen", newScreenFor(WSPurchasing, deps))
	}
	return scr
}

// TestPurchaseOrderRows_ForwardsStatusFilter is the load-bearing contract for
// the bead: the PO loader forwards the active filter's ?status= to the OMS
// purchase-order endpoint, which applies it server-side. The row keeps the
// status as its Tag, so a draft still reads "(draft)" in the list.
func TestPurchaseOrderRows_ForwardsStatusFilter(t *testing.T) {
	var gotPath, gotStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotStatus = r.URL.Query().Get("status")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(draftPOJSON))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	rows, err := purchaseOrderRows(context.Background(), deps, url.Values{"status": []string{"draft"}})
	if err != nil {
		t.Fatalf("purchaseOrderRows: %v", err)
	}
	if gotPath != "/api/reorders/purchase-orders/" {
		t.Fatalf("path = %q, want /api/reorders/purchase-orders/", gotPath)
	}
	if gotStatus != "draft" {
		t.Fatalf("status param = %q, want draft forwarded to the backend", gotStatus)
	}
	if len(rows) != 1 || rows[0].ID != "11" || rows[0].Title != "PO-2026-009" {
		t.Fatalf("rows = %+v, want the saved draft", rows)
	}
	// The draft indicator: the row's Tag is the status, rendered as "(draft)".
	if rows[0].Tag != "draft" {
		t.Fatalf("row tag = %q, want the draft status as the row indicator", rows[0].Tag)
	}
	if !strings.Contains(rows[0].Subtitle, "Acme Supplies") {
		t.Fatalf("subtitle = %q, want the supplier", rows[0].Subtitle)
	}
}

// TestPurchaseOrderRows_NilQueryOmitsStatus confirms the unfiltered view (the
// one the list opens on) sends no ?status= at all, so the landing list still
// shows every order rather than an empty-string status filter.
func TestPurchaseOrderRows_NilQueryOmitsStatus(t *testing.T) {
	var hadStatus bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadStatus = r.URL.Query()["status"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":0,"next":null,"previous":null,"results":[]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	if _, err := purchaseOrderRows(context.Background(), deps, nil); err != nil {
		t.Fatalf("purchaseOrderRows: %v", err)
	}
	if hadStatus {
		t.Fatalf("the unfiltered view must not send a ?status= param")
	}
}

// TestPurchaseOrderFilters_OpenUnfilteredThenDraft pins the cycle order: the
// list opens on everything (no behaviour change for the landing screen) and
// ONE press of f lands on drafts, which is the whole point of the bead.
func TestPurchaseOrderFilters_OpenUnfilteredThenDraft(t *testing.T) {
	if len(purchaseOrderFilters) < 2 {
		t.Fatalf("purchaseOrderFilters = %+v, want at least all + draft", purchaseOrderFilters)
	}
	if purchaseOrderFilters[0].query != nil {
		t.Fatalf("filters[0] = %+v, want the unfiltered view", purchaseOrderFilters[0])
	}
	if got := purchaseOrderFilters[1].query.Get("status"); got != "draft" {
		t.Fatalf("filters[1] status = %q, want draft one press away", got)
	}
	// Every filtered view must carry a status the backend actually accepts.
	valid := map[string]bool{
		"draft": true, "sent": true, "confirmed": true,
		"partially_received": true, "received": true,
	}
	for _, f := range purchaseOrderFilters[1:] {
		if !valid[f.query.Get("status")] {
			t.Fatalf("filter %q sends status=%q, not a PurchaseOrder.Status value",
				f.label, f.query.Get("status"))
		}
	}
}

// TestListScreen_POFilterCycleFindsDraft drives the keyboard flow end to end:
// the list loads unfiltered, `f` switches to the drafts view and re-fetches
// with ?status=draft, the header names the active view, and enter opens the
// draft's detail screen — where Send to Supplier resumes it.
func TestListScreen_POFilterCycleFindsDraft(t *testing.T) {
	var lastStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastStatus = r.URL.Query().Get("status")
		w.Header().Set("Content-Type", "application/json")
		if lastStatus == "draft" {
			_, _ = w.Write([]byte(draftPOJSON))
			return
		}
		_, _ = w.Write([]byte(`{"count":2,"next":null,"previous":null,"results":[` +
			`{"id":1,"po_number":"PO-2026-001","status":"sent"},` +
			`{"id":2,"po_number":"PO-2026-002","status":"received"}]}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newPurchasingListScreen(t, deps)
	next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	s = next.(*ListScreen)

	// The landing load is unfiltered: every order, no ?status=.
	next, _ = s.Update(s.Init()().(listLoadedMsg))
	s = next.(*ListScreen)
	if lastStatus != "" {
		t.Fatalf("initial load sent status=%q, want the unfiltered view", lastStatus)
	}
	if len(s.rows) != 2 {
		t.Fatalf("unfiltered rows = %d, want 2", len(s.rows))
	}
	s.cursor = 1 // cycling must not leave the cursor pointing into the old rows

	// f -> drafts. The key only reaches the screen because the list claims it.
	if !s.HandlesKey("f") {
		t.Fatalf("the PO list must claim 'f' over the global firmware hotkey")
	}
	next, cmd := s.Update(runeKey('f'))
	s = next.(*ListScreen)
	if got := s.activeFilter().label; got != "draft" {
		t.Fatalf("filter after one press = %q, want draft", got)
	}
	if s.cursor != 0 || s.windowStart != 0 {
		t.Fatalf("cycling should reset the cursor, got cursor=%d start=%d", s.cursor, s.windowStart)
	}
	if !s.loading {
		t.Fatalf("cycling should re-fetch, not filter the rows already loaded")
	}
	if cmd == nil {
		t.Fatalf("cycling should schedule a reload")
	}

	loaded, ok := cmd().(listLoadedMsg)
	if !ok {
		t.Fatalf("reload msg = %T, want listLoadedMsg", cmd())
	}
	next, _ = s.Update(loaded)
	s = next.(*ListScreen)
	if lastStatus != "draft" {
		t.Fatalf("backend received status=%q, want draft", lastStatus)
	}
	if len(s.rows) != 1 || s.rows[0].ID != "11" {
		t.Fatalf("rows = %+v, want just the saved draft", s.rows)
	}

	// The header names the active view so the operator can tell a filtered
	// list from a short one.
	view := s.View()
	if !strings.Contains(view, "Filter: draft") {
		t.Fatalf("view should name the active filter, got:\n%s", view)
	}
	if !strings.Contains(view, "f filter") {
		t.Fatalf("view should advertise the filter key, got:\n%s", view)
	}

	// Resumable: enter opens the draft's PO detail screen (s = send to supplier).
	_, ecmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if ecmd == nil {
		t.Fatalf("enter should open the selected draft")
	}
	switch m := ecmd().(type) {
	case SwitchScreenMsg:
		if m.Workspace != WSPurchasing {
			t.Fatalf("switch workspace = %v, want purchasing", m.Workspace)
		}
		if _, ok := m.Screen.(*PurchaseOrderDetailScreen); !ok {
			t.Fatalf("switch target = %T, want *PurchaseOrderDetailScreen", m.Screen)
		}
	default:
		t.Fatalf("enter msg = %T, want SwitchScreenMsg", m)
	}
}

// TestListScreen_RefreshKeepsActiveFilter pins Init() as filter-aware. Both
// 'r' and Root.popHistory re-Init the SAME screen instance, so a filter-blind
// Init would quietly drop an operator back to every order the moment they came
// back from a draft's detail — while the header still read "Filter: draft".
func TestListScreen_RefreshKeepsActiveFilter(t *testing.T) {
	var lastStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastStatus = r.URL.Query().Get("status")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(draftPOJSON))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := newPurchasingListScreen(t, deps)
	next, _ := s.Update(runeKey('f')) // -> drafts
	s = next.(*ListScreen)

	// 'r' — the explicit refresh.
	next, cmd := s.Update(runeKey('r'))
	s = next.(*ListScreen)
	if cmd == nil {
		t.Fatalf("'r' should schedule a reload")
	}
	cmd()
	if lastStatus != "draft" {
		t.Fatalf("refresh sent status=%q, want the active filter kept", lastStatus)
	}

	// Init() on its own — what Root re-runs when esc pops back to this screen.
	lastStatus = ""
	s.Init()()
	if lastStatus != "draft" {
		t.Fatalf("re-Init sent status=%q, want the active filter kept", lastStatus)
	}
}

// TestListScreen_FilterCycleWraps confirms f walks the whole cycle and comes
// back to the unfiltered view, so an operator can't get stranded in a filter.
func TestListScreen_FilterCycleWraps(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://example.invalid"), Ctx: context.Background()}
	s := newPurchasingListScreen(t, deps)
	for i := 0; i < len(purchaseOrderFilters); i++ {
		next, _ := s.Update(runeKey('f'))
		s = next.(*ListScreen)
	}
	if s.filter != 0 || s.activeFilter().query != nil {
		t.Fatalf("a full cycle should return to the unfiltered view, got %+v", s.activeFilter())
	}
}

// TestListScreen_EmptyFilteredViewNamesTheFilter confirms an empty FILTERED
// list says which view is empty ("no drafts") instead of the bare "No rows."
// that would read as "there are no purchase orders at all".
func TestListScreen_EmptyFilteredViewNamesTheFilter(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://example.invalid"), Ctx: context.Background()}
	s := newPurchasingListScreen(t, deps)
	next, _ := s.Update(runeKey('f')) // -> draft
	s = next.(*ListScreen)
	next, _ = s.Update(listLoadedMsg{rows: nil})
	s = next.(*ListScreen)

	view := s.View()
	if !strings.Contains(view, `"draft"`) {
		t.Fatalf("an empty filtered view should name the filter, got:\n%s", view)
	}
	if strings.Contains(view, "No rows.") {
		t.Fatalf("an empty filtered view must not read as an empty resource, got:\n%s", view)
	}

	// The unfiltered view keeps the plain message.
	s.filter = 0
	if !strings.Contains(s.View(), "No rows.") {
		t.Fatalf("the unfiltered empty view should keep the plain message, got:\n%s", s.View())
	}
}

// TestListScreen_FilterClaimGatedOnFilters confirms 'f' is claimed (and does
// anything) only on a list with a filter cycle; a plain list leaves 'f' to the
// global firmware hotkey and treats a direct press as a no-op.
func TestListScreen_FilterClaimGatedOnFilters(t *testing.T) {
	deps := Deps{Ctx: context.Background()}

	withFilters := newPurchasingListScreen(t, deps)
	if !withFilters.HandlesKey("f") {
		t.Fatalf("the PO list should claim 'f'")
	}
	if !withFilters.HandlesKey("s") {
		t.Fatalf("list should still claim 's' (sort)")
	}

	plain := NewListScreen(deps, "SIGs", listScreenSpec{kind: "sigs", loader: loadSIGs})
	if plain.HandlesKey("f") {
		t.Fatalf("a list without filters must not claim 'f'")
	}
	next, cmd := plain.Update(runeKey('f'))
	ps := next.(*ListScreen)
	if ps.filter != 0 {
		t.Fatalf("'f' must be a no-op without a filter cycle")
	}
	if cmd != nil {
		t.Fatalf("'f' no-op should return no cmd")
	}
	// A filter-less list also keeps its header and hint free of filter text.
	ps.rawRows = []listRow{{ID: "s-1", Title: "Woodshop"}}
	ps.applySort()
	if v := ps.View(); strings.Contains(v, "Filter:") || strings.Contains(v, "f filter") {
		t.Fatalf("a filter-less list should not advertise a filter, got:\n%s", v)
	}
}
