package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func apTestRows() []omsapi.AssetProblem {
	wo := "wo-77"
	return []omsapi.AssetProblem{
		{ID: "a", Status: omsapi.AssetProblemReported, Description: "belt frayed"},
		{ID: "b", Status: omsapi.AssetProblemResolved, Description: "old"},
		{ID: "c", Status: omsapi.AssetProblemClosed, Description: "done"},
		{ID: "d", Status: omsapi.AssetProblemInProgress, Description: "wip",
			WorkOrder: &wo, WorkOrderShortID: "WO-0042"},
	}
}

func apScreen() *AssetProblemsScreen {
	s := NewAssetProblemsScreen(Deps{}, "asset-9", "Bridgeport Mill")
	s.loading = false
	s.rows = apTestRows()
	return s
}

// TestAssetProblems_HandlesKey claims exactly the two colliding list keys — the
// action keys (R/w/t) were picked from the free letters and must NOT be claimed.
func TestAssetProblems_HandlesKey(t *testing.T) {
	s := NewAssetProblemsScreen(Deps{}, "asset-9", "Bridgeport Mill")
	for _, k := range []string{"f", "G"} {
		if !s.HandlesKey(k) {
			t.Errorf("should claim %q", k)
		}
	}
	for _, k := range []string{"j", "k", "R", "w", "t", "v", "r", "P", "enter"} {
		if s.HandlesKey(k) {
			t.Errorf("should NOT claim %q", k)
		}
	}
}

// TestAssetProblems_Filter cycles open→resolved→all and checks the visible set.
func TestAssetProblems_Filter(t *testing.T) {
	s := apScreen()

	// Default filter is open → reported + in_progress (ids a, d).
	if got := apIDs(s.visible()); got != "a,d" {
		t.Errorf("open filter visible = %q, want a,d", got)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if s.filter != apFilterResolved {
		t.Fatalf("filter = %v, want resolved", s.filter)
	}
	if got := apIDs(s.visible()); got != "b,c" {
		t.Errorf("resolved filter visible = %q, want b,c", got)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if got := apIDs(s.visible()); got != "a,b,c,d" {
		t.Errorf("all filter visible = %q, want a,b,c,d", got)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	if s.filter != apFilterOpen {
		t.Errorf("filter should wrap back to open, got %v", s.filter)
	}
	if s.openCount() != 2 {
		t.Errorf("openCount = %d, want 2", s.openCount())
	}
}

// TestAssetProblems_EmptyFilterCursor keeps the cursor at 0 when paging an empty
// filtered list, preserving the "cursor always indexes a shown row" invariant.
//
// PGDOWN and not ctrl+d, which was a synonym for this arm until sc-jde-listnav
// retired the chord: after that the keystroke matched no case, the arm was never
// entered, and the assertion passed for the reason it would have passed with the
// arm deleted. The exact resting place is asserted rather than "not negative",
// because "not negative" is equally true of a screen on which nothing ran.
func TestAssetProblems_EmptyFilterCursor(t *testing.T) {
	s := NewAssetProblemsScreen(Deps{}, "asset-9", "Bridgeport Mill")
	s.loading = false
	s.rows = []omsapi.AssetProblem{
		{ID: "b", Status: omsapi.AssetProblemResolved},
		{ID: "c", Status: omsapi.AssetProblemClosed},
	}
	if len(s.visible()) != 0 {
		t.Fatalf("expected empty visible set")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if s.cursor != 0 {
		t.Errorf("pgdown on empty list left cursor = %d, want 0", s.cursor)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if s.cursor != 0 {
		t.Errorf("end on empty list left cursor = %d, want 0", s.cursor)
	}
	if _, ok := s.selected(); ok {
		t.Errorf("no row should be selectable on an empty list")
	}
}

// TestAssetProblems_ResolveOverlay opens the overlay on an open problem, toggles
// resolved↔closed, and cancels with esc.
func TestAssetProblems_ResolveOverlay(t *testing.T) {
	s := apScreen()

	if s.WantsRawInput() {
		t.Fatalf("should not want raw input before overlay")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if !s.resolving || !s.WantsRawInput() {
		t.Fatalf("R should open the resolve overlay + raw input")
	}
	if s.resolveTarget != "a" {
		t.Errorf("resolveTarget = %q, want a", s.resolveTarget)
	}
	if s.resolveStatus() != omsapi.AssetProblemResolved {
		t.Errorf("should default to resolved, got %q", s.resolveStatus())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyTab})
	if s.resolveStatus() != omsapi.AssetProblemClosed {
		t.Errorf("tab should toggle to closed, got %q", s.resolveStatus())
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.resolving || s.WantsRawInput() {
		t.Errorf("esc should close the overlay")
	}
}

// TestAssetProblems_ResolveGatedOnResolved refuses the overlay on an already
// terminal problem (the backend would reject the second stamp anyway).
func TestAssetProblems_ResolveGatedOnResolved(t *testing.T) {
	s := apScreen()
	s.filter = apFilterResolved // cursor 0 = id "b" (resolved)

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if s.resolving {
		t.Errorf("R must not open the overlay on a resolved problem")
	}
}

// TestAssetProblems_ViewDetail toggles the read-only detail overlay and shows
// the promote hints only while each promote is still available.
func TestAssetProblems_ViewDetail(t *testing.T) {
	s := apScreen()

	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !s.viewing || !s.WantsRawInput() {
		t.Fatalf("enter should open the detail overlay + raw input")
	}
	out := s.View()
	if !strings.Contains(out, "Description") {
		t.Errorf("detail view missing Description: %q", out)
	}
	if !strings.Contains(out, "w work order") || !strings.Contains(out, "t vendor") {
		t.Errorf("un-promoted detail should offer both promotes: %q", out)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if s.viewing {
		t.Errorf("esc should close the detail overlay")
	}

	// Row "d" is already promoted to an in-house WO → that hint drops off.
	s.cursor = 1 // open filter → [a, d]
	s.viewing = true
	out = s.View()
	if strings.Contains(out, "w work order") {
		t.Errorf("promoted problem should not offer w: %q", out)
	}
	if !strings.Contains(out, "WO-0042") {
		t.Errorf("promoted detail should name the work order: %q", out)
	}
}

// TestAssetProblems_PromoteStandardGated refuses to re-promote a problem that
// already has an in-house work order, so no doomed request goes out.
func TestAssetProblems_PromoteStandardGated(t *testing.T) {
	s := apScreen()
	s.cursor = 1 // open filter → [a, d]; d already carries WO-0042

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if s.promoting {
		t.Fatalf("w must not fire a promote on an already-promoted problem")
	}
	if cmd == nil {
		t.Fatalf("expected a warning status command")
	}

	// A resolved problem has no work left to promote either.
	s.filter = apFilterResolved
	s.cursor = 0
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if s.promoting {
		t.Errorf("w must not fire a promote on a resolved problem")
	}
}

// TestAssetProblems_PromoteStandardJumpsToWO drives the whole in-house promote:
// w fires POST promote-standard/ with no maintenance_item, and the resulting
// message navigates to the new work order's detail screen.
func TestAssetProblems_PromoteStandardJumpsToWO(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","asset":"asset-9","status":"in_progress","work_order":"wo-77","work_order_short_id":"WO-0042"}`))
	}))
	defer srv.Close()

	s := NewAssetProblemsScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "asset-9", "Mill")
	s.loading = false
	s.rows = apTestRows()

	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	if !s.promoting || cmd == nil {
		t.Fatalf("w should fire the promote (promoting=%v cmd=%v)", s.promoting, cmd)
	}
	msg, ok := cmd().(assetProblemPromotedMsg)
	if !ok {
		t.Fatalf("expected assetProblemPromotedMsg, got %T", cmd())
	}
	if msg.err != nil {
		t.Fatalf("promote failed: %v", msg.err)
	}
	if gotPath != "/api/inventory/asset-problems/a/promote-standard/" {
		t.Fatalf("path = %q", gotPath)
	}
	if msg.woID != "wo-77" || msg.label != "WO-0042" {
		t.Fatalf("promote msg = %+v", msg)
	}

	_, cmd = s.Update(msg)
	if s.promoting {
		t.Errorf("promoting flag should clear on the result")
	}
	if !apBatchSwitchesToWO(t, cmd, "wo-77") {
		t.Errorf("promote should navigate to the new work order's detail screen")
	}
}

// TestAssetProblems_VendorFlow walks the three-step send-to-vendor prompt and
// pins the payload the last step submits.
func TestAssetProblems_VendorFlow(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/vendors/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"count":2,"results":[
				{"id":"v-1","name":"Acme Machine","vendor_kind":"machining","is_active":true},
				{"id":"v-2","name":"Retired Co","vendor_kind":"hvac","is_active":false}]}`))
			return
		}
		gotPath = r.URL.Path
		gotBody = apDecodeBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","status":"in_progress","third_party_work_order":"tp-3","third_party_work_order_short_id":"TP-0009"}`))
	}))
	defer srv.Close()

	s := NewAssetProblemsScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "asset-9", "Mill")
	s.loading = false
	s.rows = apTestRows()

	// t opens the vendor prompt and kicks off the pick-list load.
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if s.vendorStep != apVendorStepVendor || !s.WantsRawInput() {
		t.Fatalf("t should open the vendor prompt in raw-input mode")
	}
	// The title is seeded from the report so the operator edits rather than retypes.
	if s.vendorTitle.Value() != "belt frayed" {
		t.Errorf("title should seed from the description, got %q", s.vendorTitle.Value())
	}
	if cmd == nil {
		t.Fatalf("expected a vendor-load command")
	}
	s.Update(cmd())
	// Only the active vendor survives the pick-list filter.
	if len(s.vendors) != 1 || s.vendors[0].ID != "v-1" {
		t.Fatalf("vendors = %+v, want only the active one", s.vendors)
	}

	// enter picks the vendor → title step; a blank title is refused.
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.vendorStep != apVendorStepTitle {
		t.Fatalf("step = %v, want title", s.vendorStep)
	}
	s.vendorTitle.SetValue("   ")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.vendorStep != apVendorStepTitle || s.vendorErr == "" {
		t.Fatalf("blank title should hold the step with an error, got step=%v err=%q", s.vendorStep, s.vendorErr)
	}

	s.vendorTitle.SetValue("Replace spindle bearing")
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.vendorStep != apVendorStepWorkType {
		t.Fatalf("step = %v, want work type", s.vendorStep)
	}
	// space cycles standard → major_repair.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if apWorkTypeOptions[s.workTypeIx].value != omsapi.ThirdPartyWorkTypeMajorRepair {
		t.Fatalf("work type = %q", apWorkTypeOptions[s.workTypeIx].value)
	}

	_, cmd = s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !s.submittingTP || cmd == nil {
		t.Fatalf("enter should submit the vendor promote")
	}
	msg, ok := cmd().(assetProblemPromotedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("vendor promote msg = %+v (ok=%v)", msg, ok)
	}
	if gotPath != "/api/inventory/asset-problems/a/promote-third-party/" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["vendor"] != "v-1" || gotBody["title"] != "Replace spindle bearing" ||
		gotBody["work_type"] != "major_repair" {
		t.Fatalf("body = %v", gotBody)
	}
	if msg.woID != "" {
		t.Errorf("vendor promote must not navigate to a ScanTTY work order, woID=%q", msg.woID)
	}

	// The result tears the overlay down and reloads the list.
	s.Update(msg)
	if s.vendorStep != apVendorStepNone || s.submittingTP {
		t.Errorf("vendor overlay should close on success")
	}
}

// TestAssetProblems_VendorFailureKeepsOverlay leaves the prompt up on a backend
// rejection so the operator can fix the input instead of retyping from scratch.
func TestAssetProblems_VendorFailureKeepsOverlay(t *testing.T) {
	s := apScreen()
	s.vendorStep = apVendorStepWorkType
	s.submittingTP = true

	s.Update(assetProblemPromotedMsg{err: errString("vendor and title are required")})
	if s.vendorStep == apVendorStepNone {
		t.Errorf("a failed vendor promote should keep the overlay open")
	}
	if s.submittingTP {
		t.Errorf("submitting flag should clear on the result")
	}
	if s.vendorErr == "" {
		t.Errorf("failure should surface in the overlay")
	}
}

// TestAssetProblems_VendorGated refuses a second vendor promote on a report that
// already has a third-party order.
func TestAssetProblems_VendorGated(t *testing.T) {
	tp := "tp-3"
	s := NewAssetProblemsScreen(Deps{}, "asset-9", "Mill")
	s.loading = false
	s.rows = []omsapi.AssetProblem{{
		ID: "a", Status: omsapi.AssetProblemInProgress, Description: "belt",
		ThirdPartyWorkOrder: &tp, ThirdPartyWorkOrderShortID: "TP-0009",
	}}

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if s.vendorStep != apVendorStepNone {
		t.Errorf("t must not open the prompt on an already-dispatched problem")
	}
}

// TestAssetProblems_Load exercises Init→load→decode against a fake server
// returning the DRF envelope, and confirms the asset filter rides the query.
func TestAssetProblems_Load(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("asset"); got != "asset-9" {
			t.Errorf("asset filter = %q, want asset-9", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"next":null,"previous":null,"results":[{"id":"a","asset":"asset-9","status":"reported","description":"belt frayed"}]}`))
	}))
	defer srv.Close()

	s := NewAssetProblemsScreen(Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}, "asset-9", "Mill")
	msg := s.Init()()
	s.Update(msg)
	if s.loading {
		t.Fatalf("still loading after load msg")
	}
	if len(s.rows) != 1 || s.rows[0].ID != "a" {
		t.Fatalf("rows = %+v", s.rows)
	}
}

// TestAssetProblems_RenderSmoke guards the list render path + footer hints.
func TestAssetProblems_RenderSmoke(t *testing.T) {
	s := apScreen()
	s.terminalHeight = 30
	out := s.View()
	if !strings.Contains(out, "Bridgeport Mill") || !strings.Contains(out, "problems") {
		t.Errorf("list view missing header: %q", out)
	}
	for _, want := range []string{"R resolve", "w work order", "t vendor"} {
		if !strings.Contains(out, want) {
			t.Errorf("list footer missing %q: %q", want, out)
		}
	}
	// The status label is synthesized locally — the serializer ships no
	// status_display — so it must not render blank.
	if !strings.Contains(out, "REPORTED") {
		t.Errorf("row missing the synthesized status label: %q", out)
	}
}

// TestAssetDetail_PKeyOpensProblems guards the wiring: P on the asset detail
// opens the problems screen scoped to that asset, and the screen claims P so the
// global PM board does not swallow it.
func TestAssetDetail_PKeyOpensProblems(t *testing.T) {
	s := NewAssetDetailScreen(Deps{}, "asset-9")
	s.loading = false
	s.asset = &omsapi.Asset{ID: "asset-9", Name: "Bridgeport Mill"}

	if !s.HandlesKey("P") {
		t.Fatalf("asset detail must claim P or the global PM board shadows it")
	}
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("P")})
	if cmd == nil {
		t.Fatalf("P should return a switch command")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("expected SwitchScreenMsg, got %T", cmd())
	}
	ap, ok := sw.Screen.(*AssetProblemsScreen)
	if !ok {
		t.Fatalf("expected *AssetProblemsScreen, got %T", sw.Screen)
	}
	if ap.assetID != "asset-9" || ap.assetName != "Bridgeport Mill" {
		t.Errorf("problems screen scoped wrong: id=%q name=%q", ap.assetID, ap.assetName)
	}
	// p must still open the report form rather than the list.
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	if s.activeForm != formLogProblem {
		t.Errorf("lowercase p should still open the report form, got %v", s.activeForm)
	}
}

func apIDs(rows []omsapi.AssetProblem) string {
	ids := make([]string, 0, len(rows))
	for _, p := range rows {
		ids = append(ids, p.ID)
	}
	return strings.Join(ids, ",")
}

// apDecodeBody reads a request's JSON body into a map for payload assertions.
func apDecodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode body %q: %v", raw, err)
		}
	}
	return out
}

// apBatchSwitchesToWO reports whether a (batched) command navigates to the work
// order detail screen for woID. ccDrainCmd flattens tea.Batch for us.
func apBatchSwitchesToWO(t *testing.T, cmd tea.Cmd, woID string) bool {
	t.Helper()
	for _, msg := range ccDrainCmd(cmd) {
		sw, ok := msg.(SwitchScreenMsg)
		if !ok {
			continue
		}
		wo, ok := sw.Screen.(*WorkOrderDetailScreen)
		if ok && wo.woID == woID {
			return true
		}
	}
	return false
}

// errString is a minimal error so a failure path can be driven without a server.
type errString string

func (e errString) Error() string { return string(e) }
