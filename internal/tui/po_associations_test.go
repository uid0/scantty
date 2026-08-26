package tui

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Work-order / committee associations on a purchase order (op-shb9).
//
// Two levels and two surfaces: the create flow tags the whole order, and the
// edit screen re-tags the order or any single line. The invariants that matter
// are the same at every one of those points — an unpicked association must not
// reach the wire at all on create, a detach must reach it as an explicit null,
// and an edit of one field must never disturb the other.

const (
	poWorkOrderUUID = "3f1c0e58-0000-4000-8000-000000000001"
	poActiveWOs     = `{"id":"` + poWorkOrderUUID + `","short_id":"WO-1A2B",
		"display_title":"Replace drive belt","status":"open"}`
	poSIGs = `{"id":3,"name":"Woodshop"},{"id":5,"name":"Metal Shop"}`
)

// poAssocSrv serves the two option lists plus the PO create / PATCH endpoints,
// capturing whatever body was last sent so a test can assert what went out.
// woResults / sigResults are the raw `results` rows for each list ("" = empty).
func poAssocSrv(t *testing.T, woResults, sigResults string) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "work-orders"):
			// Only the open set carries rows: the picker must offer unfinished
			// jobs from both requests, and one empty page is ordinary.
			rows := ""
			if r.URL.Query().Get("status") == "open" {
				rows = woResults
			}
			_, _ = w.Write([]byte(`{"count":0,"results":[` + rows + `]}`))
		case strings.Contains(r.URL.Path, "sigs"):
			_, _ = w.Write([]byte(`{"count":0,"results":[` + sigResults + `]}`))
		case strings.Contains(r.URL.Path, "supplier-agreements"):
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		default:
			raw, _ := io.ReadAll(r.Body)
			for k := range body {
				delete(body, k)
			}
			_ = json.Unmarshal(raw, &body)
			_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0009"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

// poAssocCreateScreen builds a create screen sitting on the source chooser with
// both option lists settled — the state an operator reaches after picking a
// supplier.
func poAssocCreateScreen(t *testing.T, srv *httptest.Server) *PurchaseOrderCreateScreen {
	t.Helper()
	s := NewPurchaseOrderCreateScreen(Deps{OMS: omsapi.New(srv.URL)})
	s.supplierLoading = false
	s.suppliers = []omsapi.Supplier{{ID: 7, Name: "Acme Supply"}}
	s.supplierCursor = 0
	s.Update(loadWorkOrderOptionsCmd(s.deps)())
	s.Update(loadCommitteeOptionsCmd(s.deps)())
	s.updateSupplierPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	return s
}

// TestPOAssoc_OptionsLoadInBackgroundAndOfferKeys: an association is optional on
// every order, so the option lists must never gate a phase — they arrive behind
// the source chooser and only then grow the w / c affordances.
func TestPOAssoc_OptionsLoadInBackgroundAndOfferKeys(t *testing.T) {
	srv, _ := poAssocSrv(t, poActiveWOs, poSIGs)
	s := poAssocCreateScreen(t, srv)

	if s.phase != poPhaseSource {
		t.Fatalf("phase = %v, want poPhaseSource — the option loads must not block the flow", s.phase)
	}
	if len(s.assoc.workOrders) != 1 || len(s.assoc.committees) != 2 {
		t.Fatalf("loaded %d work order(s) / %d committee(s), want 1 / 2",
			len(s.assoc.workOrders), len(s.assoc.committees))
	}
	out := s.View()
	if !strings.Contains(out, "Work order") || !strings.Contains(out, "Committee") {
		t.Errorf("source chooser should advertise both associations:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("an untagged order should read (none):\n%s", out)
	}
	help := poBarText(s.bar())
	if !strings.Contains(help, "w=Work order") || !strings.Contains(help, "c=Committee") {
		t.Errorf("the bar should name both keys: %q", help)
	}
}

// TestPOAssoc_NothingToPickOffersNothing: a shop with no unfinished jobs and no
// committees gets no rows, no help entries and inert keys — an empty picker
// would be a dead end, and the web renders its selects on the same condition.
func TestPOAssoc_NothingToPickOffersNothing(t *testing.T) {
	srv, _ := poAssocSrv(t, "", "")
	s := poAssocCreateScreen(t, srv)

	if s.assoc.workOrdersOffered() || s.assoc.committeesOffered() {
		t.Error("nothing to pick must not offer either picker")
	}
	if out := s.View(); strings.Contains(out, "Work order") || strings.Contains(out, "Committee") {
		t.Errorf("source chooser should stay silent:\n%s", out)
	}
	if help := poBarText(s.bar()); strings.Contains(help, "Work order") || strings.Contains(help, "Committee") {
		t.Errorf("the bar should not advertise dead keys: %q", help)
	}
	s.updateSourcePhase(runeKey('w'), 0)
	s.updateSourcePhase(runeKey('c'), 0)
	if s.phase != poPhaseSource {
		t.Errorf("w / c opened a picker with nothing in it; phase = %v", s.phase)
	}
}

// TestPOAssoc_PickRoundTripsToSubmit walks the whole create path: open each
// picker, choose a job and a committee, see both before submitting, and find
// them on the wire in the shapes the create serializer expects — a bare UUID
// string and a bare group pk.
func TestPOAssoc_PickRoundTripsToSubmit(t *testing.T) {
	srv, body := poAssocSrv(t, poActiveWOs, poSIGs)
	s := poAssocCreateScreen(t, srv)

	s.updateSourcePhase(runeKey('w'), 0)
	if s.phase != poPhaseWorkOrder {
		t.Fatalf("w should open the work-order picker; phase = %v", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "— no work order —") {
		t.Errorf("picker must offer an explicit skip row:\n%s", out)
	}
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyDown}, 0) // row 0 is "none"
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.phase != poPhaseSource {
		t.Errorf("committing should return to the source chooser; phase = %v", s.phase)
	}
	if s.workOrderID != poWorkOrderUUID {
		t.Fatalf("workOrderID = %q, want the picked job's uuid", s.workOrderID)
	}

	s.updateSourcePhase(runeKey('c'), 0)
	s.updateCommitteePhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	s.updateCommitteePhase(tea.KeyMsg{Type: tea.KeyDown}, 0) // Metal Shop
	s.updateCommitteePhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.committeeID != "5" {
		t.Fatalf("committeeID = %q, want 5 (Metal Shop)", s.committeeID)
	}

	// Both are visible before the order goes out.
	stageOneLine(s)
	review := s.View()
	if !strings.Contains(review, "WO-1A2B — Replace drive belt") {
		t.Errorf("review should name the job:\n%s", review)
	}
	if !strings.Contains(review, "Metal Shop") {
		t.Errorf("review should name the committee:\n%s", review)
	}

	if msg := s.finalize()(); msg.(poCreatedMsg).err != nil {
		t.Fatalf("create failed: %v", msg.(poCreatedMsg).err)
	}
	if got := (*body)["work_order"]; got != poWorkOrderUUID {
		t.Errorf("work_order = %v, want the job uuid", got)
	}
	if n, ok := (*body)["owning_group"].(float64); !ok || n != 5 {
		t.Errorf("owning_group = %v, want the bare pk 5", (*body)["owning_group"])
	}
}

// TestPOAssoc_SkippedOrderOmitsBothFields: declining an association on a shop
// that HAS jobs and committees must leave both keys off the payload entirely.
// The backend resolves whatever it is handed and rejects an id it can't find,
// so a null or a 0 would be a value to reject rather than the "none" meant.
func TestPOAssoc_SkippedOrderOmitsBothFields(t *testing.T) {
	srv, body := poAssocSrv(t, poActiveWOs, poSIGs)
	s := poAssocCreateScreen(t, srv)

	s.updateSourcePhase(runeKey('w'), 0)
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0) // row 0 = none
	s.updateSourcePhase(runeKey('c'), 0)
	s.updateCommitteePhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)

	stageOneLine(s)
	if msg := s.finalize()(); msg.(poCreatedMsg).err != nil {
		t.Fatalf("create failed: %v", msg.(poCreatedMsg).err)
	}
	for _, key := range []string{"work_order", "owning_group"} {
		if v, present := (*body)[key]; present {
			t.Errorf("%s should be omitted when skipped, got %v", key, v)
		}
	}
}

// TestPOAssoc_RowZeroClearsAPickAndEscKeepsIt separates the two ways out of a
// picker: esc means "done looking", row 0 means "detach". An operator who
// opened the picker to check what the order carries must not lose the pick.
func TestPOAssoc_RowZeroClearsAPickAndEscKeepsIt(t *testing.T) {
	srv, _ := poAssocSrv(t, poActiveWOs, poSIGs)
	s := poAssocCreateScreen(t, srv)

	s.updateSourcePhase(runeKey('w'), 0)
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyDown}, 0)
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.workOrderID == "" {
		t.Fatal("setup: a job should be committed")
	}

	// Re-opening parks the cursor on the current pick, so enter is a no-op
	// confirm rather than a silent reset to "none".
	s.updateSourcePhase(runeKey('w'), 0)
	if s.workOrderCursor != 1 {
		t.Errorf("re-opened cursor = %d, want 1 (the committed job)", s.workOrderCursor)
	}
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyEsc}, 0)
	if s.workOrderID != poWorkOrderUUID {
		t.Errorf("esc cleared the pick; workOrderID = %q", s.workOrderID)
	}

	s.updateSourcePhase(runeKey('w'), 0)
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyUp}, 0)
	s.updateWorkOrderPhase(tea.KeyMsg{Type: tea.KeyEnter}, 0)
	if s.workOrderID != "" {
		t.Errorf("row 0 should detach; got %q", s.workOrderID)
	}
}

// TestPOAssoc_FailedLoadSaysUnavailableAndStillSubmits: a lookup that could not
// be made is reported as MISSING (silence would read as "there are no jobs",
// which may be false) and never stops the purchase order from going out.
func TestPOAssoc_FailedLoadSaysUnavailableAndStillSubmits(t *testing.T) {
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "work-orders") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "sigs") || strings.Contains(r.URL.Path, "supplier-agreements") {
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		_, _ = w.Write([]byte(`{"id":"po-1","po_number":"PO-2026-0009"}`))
	}))
	defer srv.Close()

	s := poAssocCreateScreen(t, srv)
	if s.assoc.workOrderErr == "" {
		t.Fatal("a failed work-order load should be recorded")
	}
	if s.phase != poPhaseSource {
		t.Errorf("a failed load must not strand the flow; phase = %v", s.phase)
	}
	if out := s.View(); !strings.Contains(out, "unavailable") {
		t.Errorf("source chooser should say the list is unavailable, not imply there are none:\n%s", out)
	}
	// w retries rather than opening a picker over an empty list.
	s.updateSourcePhase(runeKey('w'), 0)
	if s.phase == poPhaseWorkOrder {
		t.Error("w should retry a failed load, not open an empty picker")
	}

	stageOneLine(s)
	if msg := s.finalize()(); msg.(poCreatedMsg).err != nil {
		t.Fatalf("a failed option lookup blocked the order: %v", msg.(poCreatedMsg).err)
	}
	if v, present := body["work_order"]; present {
		t.Errorf("work_order should be omitted, got %v", v)
	}
}

// TestPOWithCurrentOption_GraftsAnUnlistedAttachment is the guard that keeps an
// unrelated edit from silently detaching a job: the pickers offer only
// unfinished work and the viewer's own committees, so an order tagged with a
// since-completed job has to be grafted in — right under the "none" row, where
// it reads as the current value — or it would vanish from its own picker.
func TestPOWithCurrentOption_GraftsAnUnlistedAttachment(t *testing.T) {
	rows := []poAssocOption{
		{value: "", label: "— no work order —"},
		{value: "wo-open", label: "WO-1 — Open job"},
	}

	same := poWithCurrentOption(rows, "wo-open", "WO-1 — Open job")
	if len(same) != 2 {
		t.Errorf("an attachment already in the list must not be duplicated: %+v", same)
	}
	if unattached := poWithCurrentOption(rows, "", ""); len(unattached) != 2 {
		t.Errorf("nothing attached means nothing to graft: %+v", unattached)
	}

	got := poWithCurrentOption(rows, "wo-done", "WO-9 — Finished job")
	if len(got) != 3 {
		t.Fatalf("an unlisted attachment should be grafted: %+v", got)
	}
	if got[0].value != "" {
		t.Errorf("the none row must stay first, got %+v", got[0])
	}
	if got[1].value != "wo-done" || got[1].label != "WO-9 — Finished job" {
		t.Errorf("graft = %+v, want the current attachment directly under none", got[1])
	}
	if poAssocCursorFor(got, "wo-done") != 1 {
		t.Error("opening the picker should land on the grafted current value")
	}

	// A grafted attachment with no label still has to be selectable — its id is
	// better than a blank row the operator cannot tell apart from "none".
	bare := poWithCurrentOption(rows, "wo-done", "")
	if bare[1].label != "wo-done" {
		t.Errorf("a labelless graft should fall back to its id, got %q", bare[1].label)
	}
}

// TestPOWorkOrderLabel_FallsBackThroughTheNames: display_title is the only name
// a corrective work order has, but a payload from an older backend still has to
// read as something an operator can pick.
func TestPOWorkOrderLabel_FallsBackThroughTheNames(t *testing.T) {
	cases := []struct {
		name string
		wo   omsapi.WorkOrder
		want string
	}{
		{"display title", omsapi.WorkOrder{ID: "u", ShortID: "WO-1", DisplayTitle: "Belt"}, "WO-1 — Belt"},
		{"template title", omsapi.WorkOrder{ID: "u", ShortID: "WO-1", MaintenanceItemTitle: "Quarterly PM"}, "WO-1 — Quarterly PM"},
		{"asset name", omsapi.WorkOrder{ID: "u", ShortID: "WO-1", AssetName: "Lathe"}, "WO-1 — Lathe"},
		{"short id only", omsapi.WorkOrder{ID: "u", ShortID: "WO-1"}, "WO-1"},
		{"nothing but an id", omsapi.WorkOrder{ID: "u"}, "u"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := poWorkOrderLabel(tc.wo); got != tc.want {
				t.Errorf("poWorkOrderLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PO detail + edit screens
// ---------------------------------------------------------------------------

// poAssociatedPO is a saved order tagged at BOTH levels: the order is for one
// job and committee, and line 1 was bought for a different one — the mixed
// order that is the whole reason lines carry associations of their own.
func poAssociatedPO() *omsapi.PurchaseOrder {
	po := samplePO()
	group := 3
	po.WorkOrder = poWorkOrderUUID
	po.WorkOrderRef = &omsapi.WorkOrderRef{
		ID: poWorkOrderUUID, ShortID: "WO-1A2B", DisplayTitle: "Replace drive belt", Status: "open",
	}
	po.OwningGroup = &group
	po.OwningGroupRef = &omsapi.OwningGroupRef{ID: 3, Name: "Woodshop"}

	lineGroup := 5
	po.Items[0].WorkOrder = "wo-line"
	po.Items[0].WorkOrderRef = &omsapi.WorkOrderRef{ID: "wo-line", ShortID: "WO-9Z8Y", DisplayTitle: "Lathe PM"}
	po.Items[0].OwningGroup = &lineGroup
	po.Items[0].OwningGroupRef = &omsapi.OwningGroupRef{ID: 5, Name: "Metal Shop"}
	return po
}

// TestPODetail_RendersBothLevelsOfAssociation: the detail body names the job and
// committee the order was placed for, and a line that was bought for a
// different one says so on its own row.
func TestPODetail_RendersBothLevelsOfAssociation(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	s.po = poAssociatedPO()
	s.loading = false
	out := s.renderBody()

	for _, want := range [][2]string{
		{"Work order", "WO-1A2B — Replace drive belt"},
		{"Committee", "Woodshop"},
	} {
		if row := poDetailRow(t, out, want[0]); !strings.Contains(row, want[1]) {
			t.Errorf("the %s row should name %q, got %q", want[0], want[1], row)
		}
	}
	if !strings.Contains(out, "ordered for: WO-9Z8Y — Lathe PM · Metal Shop") {
		t.Errorf("detail body missing the line's own association:\n%s", out)
	}

	// Line 2 carries neither, and must not grow an empty row for them.
	if strings.Count(out, "ordered for:") != 1 {
		t.Errorf("only the associated line should show an 'ordered for' row:\n%s", out)
	}
}

// TestPODetail_UnassociatedOrderShowsNoAssociationRows: the associations are
// optional on every order, so an untagged one draws nothing about them rather
// than a pair of empty rows on every PO in the shop.
func TestPODetail_UnassociatedOrderShowsNoAssociationRows(t *testing.T) {
	s := NewPurchaseOrderDetailScreen(Deps{}, "po-1")
	s.po = samplePO()
	s.loading = false
	out := s.renderBody()
	for _, unwanted := range []string{"Work order" + jdeLeader, "Committee" + jdeLeader, "ordered for:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("an untagged order should not render %q:\n%s", unwanted, out)
		}
	}
}

// poAssocEditScreen builds an edit screen over the mixed order with both option
// lists settled.
func poAssocEditScreen(t *testing.T, srv *httptest.Server) *PurchaseOrderEditScreen {
	t.Helper()
	s := NewPurchaseOrderEditScreen(Deps{OMS: omsapi.New(srv.URL)}, poAssociatedPO())
	s.Update(loadWorkOrderOptionsCmd(s.deps)())
	s.Update(loadCommitteeOptionsCmd(s.deps)())
	return s
}

// TestPOEditAssoc_OrderLevelRowsAreAlwaysNavigable: the two order-level rows sit
// between the metadata fields and the lines and are drawn whatever the option
// lists are doing — a row that appeared with an async response would shift every
// line beneath it under the operator's cursor.
func TestPOEditAssoc_OrderLevelRowsAreAlwaysNavigable(t *testing.T) {
	srv, _ := poAssocSrv(t, "", "")
	s := poAssocEditScreen(t, srv)

	s.cursor = poEditMetaCount + poAssocRowWorkOrder
	if row, ok := s.onAssocRow(); !ok || row != poAssocRowWorkOrder {
		t.Fatalf("cursor %d should be the work-order row", s.cursor)
	}
	if _, ok := s.onLineRow(); ok {
		t.Error("an association row must not also read as a line row")
	}
	out := s.viewForm()
	// Columnar rows (sc-h412): a right-aligned label, then the dotted leader.
	if !strings.Contains(out, "Work order"+jdeLeader) || !strings.Contains(out, "Committee"+jdeLeader) {
		t.Errorf("form should render both association rows:\n%s", out)
	}
	// Even with no pickable options, what IS attached comes from the PO itself
	// and renders immediately.
	if !strings.Contains(out, "WO-1A2B — Replace drive belt") || !strings.Contains(out, "Woodshop") {
		t.Errorf("form should show the attached job and committee:\n%s", out)
	}
	// Nothing to pick and nothing worth opening: say so instead of showing an
	// empty list. The attachment is grafted, so the list is 2 rows — openable.
	s.cursor = poEditMetaCount + poAssocRowCommittee
	if _, ok := s.onAssocRow(); !ok {
		t.Fatal("cursor should be the committee row")
	}
}

// TestPOEditAssoc_OrderLevelPickWritesOnlyThatField: re-tagging the order must
// PATCH the association alone — an order-details save that also rewrote the
// supplier order number or the notes would clobber whatever someone else set.
func TestPOEditAssoc_OrderLevelPickWritesOnlyThatField(t *testing.T) {
	srv, body := poAssocSrv(t, poActiveWOs, poSIGs)
	s := poAssocEditScreen(t, srv)

	// Ctrl-E opens what the highlighted row IS (sc-h412): enter is the SAVE key
	// on every row of this form, so a picker cannot also ride it.
	s.cursor = poEditMetaCount + poAssocRowCommittee
	s.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseAssoc {
		t.Fatalf("ctrl+e on the committee row should open its picker; phase = %v", s.phase)
	}
	// Parked on the attached committee (Woodshop, id 3 — first in the list).
	if s.assocRows[s.assocCursor].value != "3" {
		t.Errorf("picker should open on the attached committee, got %+v", s.assocRows[s.assocCursor])
	}
	s.updateAssocPick(tea.KeyMsg{Type: tea.KeyDown}) // Metal Shop
	msg := s.saveAssoc()()
	if action, ok := msg.(poLineActionMsg); !ok || action.err != nil {
		t.Fatalf("saving the association failed: %#v", msg)
	}

	if n, ok := (*body)["owning_group"].(float64); !ok || n != 5 {
		t.Errorf("owning_group = %v, want 5", (*body)["owning_group"])
	}
	for _, untouched := range []string{"work_order", "supplier_order_number", "notes", "sales_order_number"} {
		if v, present := (*body)[untouched]; present {
			t.Errorf("an association edit must not touch %s (got %v): %v", untouched, v, *body)
		}
	}
}

// TestPOEditAssoc_LineLevelPickWritesThroughUpdateItem: a line's own work
// order / committee re-tag that line alone, through update_item — which is
// where the answer usually lands, since which job a part was for is often
// settled once it lands. Since sc-h412 the route there is Ctrl-E into the line
// editor, then Ctrl-E on the row itself, instead of the bare w / c keys.
func TestPOEditAssoc_LineLevelPickWritesThroughUpdateItem(t *testing.T) {
	var patched string
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "work-orders"):
			rows := ""
			if r.URL.Query().Get("status") == "open" {
				rows = poActiveWOs
			}
			_, _ = w.Write([]byte(`{"count":0,"results":[` + rows + `]}`))
		case strings.Contains(r.URL.Path, "sigs"):
			_, _ = w.Write([]byte(`{"count":0,"results":[` + poSIGs + `]}`))
		default:
			patched = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			_, _ = w.Write([]byte(`{"id":"line-1"}`))
		}
	}))
	defer srv.Close()

	s := poAssocEditScreen(t, srv)
	s.cursor = poEditLineBase // line 1
	s.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseLine || s.editLineIdx != 0 {
		t.Fatalf("ctrl+e on a line row should open THAT line's editor; phase=%v line=%d", s.phase, s.editLineIdx)
	}
	s.lineFocus = poLineRowWorkOrder
	s.updateLineEdit(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseAssoc || s.assocLineIdx != 0 {
		t.Fatalf("ctrl+e on the line's work-order row should open its picker; phase=%v line=%d", s.phase, s.assocLineIdx)
	}
	// The line's own job (WO-9Z8Y) isn't in the offered set — it is grafted in
	// so this edit can't drop it, and the picker opens on it.
	if s.assocRows[s.assocCursor].value != "wo-line" {
		t.Fatalf("picker should open on the line's attached job, got %+v", s.assocRows[s.assocCursor])
	}
	if !strings.Contains(s.viewAssocPick(), "line 1") {
		t.Errorf("the picker should name the line it is tagging:\n%s", s.viewAssocPick())
	}
	s.updateAssocPick(tea.KeyMsg{Type: tea.KeyUp}) // row 0 = none → detach
	if msg := s.saveAssoc()(); msg.(poLineActionMsg).err != nil {
		t.Fatalf("saving the line association failed: %v", msg.(poLineActionMsg).err)
	}

	if !strings.HasSuffix(patched, "/items/line-1/") {
		t.Errorf("line associations must go through update_item, patched %q", patched)
	}
	v, present := body["work_order"]
	if !present || v != nil {
		t.Errorf("work_order = %v (present=%v), want null — the backend's detach signal", v, present)
	}
	if _, present := body["owning_group"]; present {
		t.Errorf("re-tagging the job must not touch the line's committee: %v", body)
	}
}

// TestPOEditAssoc_LineAffordancesAreDiscoverable: with the w / c accelerators
// gone (sc-h412), a line's associations are reachable only through the action
// bar and the line editor's own rows — so both have to name them. This is the
// test that fails if the redesign hid an affordance instead of moving it.
func TestPOEditAssoc_LineAffordancesAreDiscoverable(t *testing.T) {
	srv, _ := poAssocSrv(t, poActiveWOs, poSIGs)
	s := poAssocEditScreen(t, srv)

	s.cursor = poEditLineBase
	out := s.viewForm()
	if !strings.Contains(out, "Ctrl-E=Edit line") {
		t.Errorf("the bar should offer the line editor on a line row:\n%s", out)
	}
	// A line's own associations read on its row, so the operator can see which
	// lines are already accounted for without opening anything.
	if !strings.Contains(out, "ordered for: WO-9Z8Y — Lathe PM · Metal Shop") {
		t.Errorf("line row should show what it was ordered for:\n%s", out)
	}

	// Inside the line editor both associations are rows of their own, showing
	// what is attached today, and the bar names the key that opens each.
	s.openLineEditor(0)
	out = s.viewLineEdit()
	if !strings.Contains(out, "Work order"+jdeLeader) || !strings.Contains(out, "Committee"+jdeLeader) {
		t.Errorf("the line editor should carry both association rows:\n%s", out)
	}
	if !strings.Contains(out, "WO-9Z8Y — Lathe PM") || !strings.Contains(out, "Metal Shop") {
		t.Errorf("the rows should show what the line is tagged with:\n%s", out)
	}
	for _, focus := range []int{poLineRowWorkOrder, poLineRowCommittee} {
		s.lineFocus = focus
		if out := s.viewLineEdit(); !strings.Contains(out, "Ctrl-E=Pick") {
			t.Errorf("row %d should advertise the picker:\n%s", focus, out)
		}
	}
	s.lineFocus = poLineRowStatus
	if out := s.viewLineEdit(); !strings.Contains(out, "Ctrl-E=Void line") {
		t.Errorf("the status row should advertise the void prompt:\n%s", out)
	}
}

// TestPOEditAssoc_NothingToPickIsRefusedNotOpened: with no options and nothing
// attached the picker would be a single "none" row, so the key reports why
// instead of opening an empty list an operator has to escape from.
func TestPOEditAssoc_NothingToPickIsRefusedNotOpened(t *testing.T) {
	srv, _ := poAssocSrv(t, "", "")
	s := NewPurchaseOrderEditScreen(Deps{OMS: omsapi.New(srv.URL)}, samplePO())
	s.Update(loadWorkOrderOptionsCmd(s.deps)())
	s.Update(loadCommitteeOptionsCmd(s.deps)())

	s.cursor = poEditMetaCount + poAssocRowWorkOrder
	_, cmd := s.updateForm(tea.KeyMsg{Type: tea.KeyCtrlE})
	if s.phase != poEditPhaseForm {
		t.Errorf("an empty picker should not open; phase = %v", s.phase)
	}
	if cmd == nil {
		t.Fatal("the operator should be told why nothing opened")
	}
}

// TestPOAssocValueField_ThreeStates: the columnar row the detail sheet draws
// keeps the three states an association row has always had. The middle one is
// the point — a picker that could not be loaded must never render as an order with
// nothing attached, because "we couldn't ask" and "there is none" lead an
// operator to opposite conclusions.
func TestPOAssocValueField_ThreeStates(t *testing.T) {
	attached := poAssocValueField("Work order", "WO-1A2B — Replace drive belt", "")
	if attached.Value != "WO-1A2B — Replace drive belt" || attached.Dim {
		t.Errorf("an attached job should read as a value, got %+v", attached)
	}

	failed := poAssocValueField("Work order", "", "connection refused")
	if !strings.Contains(failed.Value, "unavailable") || !strings.Contains(failed.Value, "connection refused") {
		t.Errorf("a failed load should say so and why, got %+v", failed)
	}
	if !failed.Dim {
		t.Errorf("a failed load is not a value; it should render dimmed: %+v", failed)
	}

	none := poAssocValueField("Committee", "", "")
	if none.Value != "(none)" || !none.Dim {
		t.Errorf("nothing attached should read as a dimmed absence, got %+v", none)
	}

	// The value is handed over PLAIN: the columnar renderer owns the styling, and
	// a value carrying its own colour sequence would end a focused row's
	// highlight partway across the field.
	for _, f := range []jdeField{attached, failed, none} {
		if strings.Contains(f.Value, "\x1b") {
			t.Errorf("value should be plain text, got %q", f.Value)
		}
		if f.Kind != jdeValue {
			t.Errorf("an association row is a value row, got kind %v", f.Kind)
		}
	}
}
