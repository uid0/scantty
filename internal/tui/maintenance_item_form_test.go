package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// mtKey builds a tea.KeyMsg for a literal key string (letters, "enter", "esc").
func mtKey(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// TestMaintenanceItemForm_BuildPayload walks the happy path: asset + title set,
// interval + cost parsed, estimated time blank → nil, is_active default true.
func TestMaintenanceItemForm_BuildPayload(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")
	s.assetID = "asset-1"
	s.inputs[mfTitle].SetValue("Filter change")
	s.inputs[mfDescription].SetValue("keeps airflow up")
	s.inputs[mfIntervalDays].SetValue("30")
	s.inputs[mfEstCost].SetValue("12.50")

	w, err := s.buildItemPayload()
	if err != nil {
		t.Fatalf("buildItemPayload: %v", err)
	}
	if w.Asset != "asset-1" || w.Title != "Filter change" {
		t.Errorf("asset/title = %q / %q", w.Asset, w.Title)
	}
	if w.IntervalDays == nil || *w.IntervalDays != 30 {
		t.Errorf("interval_days = %v", w.IntervalDays)
	}
	if w.EstimatedTimeMin != nil {
		t.Errorf("estimated_time should be nil when blank, got %v", *w.EstimatedTimeMin)
	}
	if w.EstimatedCost != "12.50" {
		t.Errorf("estimated_cost = %q", w.EstimatedCost)
	}
	if !w.IsActive {
		t.Errorf("is_active should default true")
	}
	if w.Description != "keeps airflow up" {
		t.Errorf("description = %q", w.Description)
	}
}

// TestMaintenanceItemForm_Validation covers the two required fields: asset and
// title.
func TestMaintenanceItemForm_Validation(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")

	// No asset.
	s.inputs[mfTitle].SetValue("X")
	if _, err := s.buildItemPayload(); err == nil {
		t.Errorf("expected error when asset unset")
	}
	// Asset set, no title.
	s.assetID = "a1"
	s.inputs[mfTitle].SetValue("")
	if _, err := s.buildItemPayload(); err == nil {
		t.Errorf("expected error when title empty")
	}
	// Both set — ok.
	s.inputs[mfTitle].SetValue("Grease")
	if _, err := s.buildItemPayload(); err != nil {
		t.Errorf("valid payload rejected: %v", err)
	}
	// Bad interval.
	s.inputs[mfIntervalDays].SetValue("0")
	if _, err := s.buildItemPayload(); err == nil {
		t.Errorf("expected error for interval 0")
	}
}

// TestMaintenanceItemForm_TaskEditor exercises add / validate / edit-marks-dirty
// / delete on the task sub-list.
func TestMaintenanceItemForm_TaskEditor(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")

	// Empty title is rejected.
	s.openTaskEditor(-1)
	s.teTitle.SetValue("")
	s.commitTaskEditor()
	if len(s.tasks) != 0 || s.editErr == "" {
		t.Fatalf("empty task should not commit; tasks=%d err=%q", len(s.tasks), s.editErr)
	}

	// Add a task.
	s.openTaskEditor(-1)
	s.teTitle.SetValue("Drain oil")
	s.teDesc.SetValue("into pan")
	s.teRequired = true
	s.commitTaskEditor()
	if len(s.tasks) != 1 || s.tasks[0].title != "Drain oil" || !s.tasks[0].isRequired {
		t.Fatalf("task not added: %+v", s.tasks)
	}

	// Edit it (existing row → dirty).
	s.openTaskEditor(0)
	s.teTitle.SetValue("Drain oil fully")
	s.commitTaskEditor()
	if !s.tasks[0].dirty || s.tasks[0].title != "Drain oil fully" {
		t.Fatalf("edit should mark dirty: %+v", s.tasks[0])
	}

	// Delete via the list handler.
	s.phase = mFormPhaseTaskList
	s.rowCursor = 0
	s.updateTaskList(mtKey("d"))
	if len(s.tasks) != 0 {
		t.Fatalf("task not deleted: %+v", s.tasks)
	}
}

// TestMaintenanceItemForm_MaterialEditor covers add / name-required / quantity
// default.
func TestMaintenanceItemForm_MaterialEditor(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")

	// Name required.
	s.openMaterialEditor(-1)
	s.meName.SetValue("")
	s.commitMaterialEditor()
	if len(s.materials) != 0 || s.editErr == "" {
		t.Fatalf("nameless material should not commit")
	}

	// Add with default quantity ("1") + cost ("0").
	s.openMaterialEditor(-1)
	s.meName.SetValue("Oil")
	s.meUnit.SetValue("qt")
	s.commitMaterialEditor()
	if len(s.materials) != 1 {
		t.Fatalf("material not added")
	}
	m := s.materials[0]
	if m.name != "Oil" || m.quantity != "1" || m.cost != "0" || m.unit != "qt" {
		t.Fatalf("material defaults wrong: %+v", m)
	}
}

// TestMaintenanceItemForm_Hydrate confirms edit-mode hydration fills the main
// fields and sorts task rows by their saved order.
func TestMaintenanceItemForm_Hydrate(t *testing.T) {
	interval := 14
	s := NewMaintenanceItemFormScreen(Deps{}, "mi-1")
	s.item = &omsapi.MaintenanceItem{
		ID: "mi-1", Asset: "a1", AssetName: "Lathe", Title: "PM",
		Description: "d", Instructions: "i", IntervalDays: &interval,
		EstimatedCost: "5.00", IsActive: false,
		Tasks: []omsapi.MaintenanceTask{
			{ID: "t2", Order: 1, Title: "Second"},
			{ID: "t1", Order: 0, Title: "First"},
		},
		Materials: []omsapi.MaintenanceMaterial{
			{ID: "m1", Name: "Oil", Quantity: "2", Unit: "qt", EstimatedCostPerUnit: "4"},
		},
	}
	s.hydrate()

	if s.assetID != "a1" || s.assetName != "Lathe" {
		t.Errorf("asset hydrate = %q / %q", s.assetID, s.assetName)
	}
	if s.inputs[mfTitle].Value() != "PM" || s.inputs[mfIntervalDays].Value() != "14" {
		t.Errorf("title/interval hydrate wrong")
	}
	if s.isActive {
		t.Errorf("is_active should hydrate false")
	}
	if len(s.tasks) != 2 || s.tasks[0].title != "First" || s.tasks[1].title != "Second" {
		t.Errorf("tasks should sort by order: %+v", s.tasks)
	}
	if len(s.origTaskIDs) != 2 || len(s.origMaterialIDs) != 1 {
		t.Errorf("orig ids = %v / %v", s.origTaskIDs, s.origMaterialIDs)
	}
	if len(s.materials) != 1 || s.materials[0].quantity != "2" {
		t.Errorf("materials hydrate wrong: %+v", s.materials)
	}
}

// recorder is a request-recording httptest server for reconcile tests.
type recorder struct {
	mu   sync.Mutex
	reqs []struct{ method, path string }
}

func (r *recorder) count(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, q := range r.reqs {
		if q.method == method {
			n++
		}
	}
	return n
}

func (r *recorder) hasPath(method, path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range r.reqs {
		if q.method == method && q.path == path {
			return true
		}
	}
	return false
}

func newRecorder(t *testing.T) (*recorder, *omsapi.Client) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, struct{ method, path string }{r.Method, r.URL.Path})
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	t.Cleanup(srv.Close)
	return rec, omsapi.New(srv.URL)
}

// TestReconcileTasks_DeleteCreateSkip: a removed row is DELETEd, an unchanged
// row at the same order is skipped, a new row is POSTed.
func TestReconcileTasks_DeleteCreateSkip(t *testing.T) {
	rec, c := newRecorder(t)
	rows := []taskRow{
		{id: "t-keep", title: "Keep", loadedOrder: 0}, // index 0, unchanged
		{id: "", title: "New"},                        // brand new
	}
	origIDs := []string{"t-keep", "t-del"}
	if err := reconcileTasks(context.Background(), c, "mi-1", rows, origIDs); err != nil {
		t.Fatalf("reconcileTasks: %v", err)
	}
	if !rec.hasPath(http.MethodDelete, "/api/inventory/maintenance-tasks/t-del/") {
		t.Errorf("expected DELETE of t-del")
	}
	if rec.count(http.MethodPost) != 1 {
		t.Errorf("expected 1 POST (new task), got %d", rec.count(http.MethodPost))
	}
	if rec.count(http.MethodPatch) != 0 {
		t.Errorf("unchanged task should not PATCH, got %d", rec.count(http.MethodPatch))
	}
}

// TestReconcileTasks_ReorderPatches: swapping two rows patches both (their
// position no longer matches loadedOrder), keeping run order consistent.
func TestReconcileTasks_ReorderPatches(t *testing.T) {
	rec, c := newRecorder(t)
	rows := []taskRow{
		{id: "t-b", title: "B", loadedOrder: 1}, // now index 0
		{id: "t-a", title: "A", loadedOrder: 0}, // now index 1
	}
	origIDs := []string{"t-a", "t-b"}
	if err := reconcileTasks(context.Background(), c, "mi-1", rows, origIDs); err != nil {
		t.Fatalf("reconcileTasks: %v", err)
	}
	if rec.count(http.MethodPatch) != 2 {
		t.Errorf("expected 2 PATCH for reordered rows, got %d", rec.count(http.MethodPatch))
	}
	if rec.count(http.MethodDelete) != 0 {
		t.Errorf("no deletes expected, got %d", rec.count(http.MethodDelete))
	}
}

// TestReconcileMaterials_DeleteEditCreate: removed → DELETE, dirty existing →
// PATCH, new → POST, unchanged existing → skipped.
func TestReconcileMaterials_DeleteEditCreate(t *testing.T) {
	rec, c := newRecorder(t)
	rows := []materialRow{
		{id: "m-clean", name: "Clean"},              // unchanged → skip
		{id: "m-dirty", name: "Dirty", dirty: true}, // edited → PATCH
		{id: "", name: "New"},                       // new → POST
	}
	origIDs := []string{"m-clean", "m-dirty", "m-gone"}
	if err := reconcileMaterials(context.Background(), c, "mi-1", rows, origIDs); err != nil {
		t.Fatalf("reconcileMaterials: %v", err)
	}
	if !rec.hasPath(http.MethodDelete, "/api/inventory/maintenance-materials/m-gone/") {
		t.Errorf("expected DELETE of m-gone")
	}
	if rec.count(http.MethodPatch) != 1 {
		t.Errorf("expected 1 PATCH (dirty), got %d", rec.count(http.MethodPatch))
	}
	if rec.count(http.MethodPost) != 1 {
		t.Errorf("expected 1 POST (new), got %d", rec.count(http.MethodPost))
	}
}

// TestParseHelpers pins the small nullable-int + optional-decimal parsers.
func TestParseHelpers(t *testing.T) {
	if v, err := mfOptPositiveInt("", "x"); err != nil || v != nil {
		t.Errorf("blank → nil,nil; got %v,%v", v, err)
	}
	if v, err := mfOptPositiveInt("30", "x"); err != nil || v == nil || *v != 30 {
		t.Errorf("30 → *30; got %v,%v", v, err)
	}
	if _, err := mfOptPositiveInt("0", "x"); err == nil {
		t.Errorf("0 should error")
	}
	if _, err := mfOptPositiveInt("nope", "x"); err == nil {
		t.Errorf("non-numeric should error")
	}
	if v, err := mfDecimalOrDefault("", "0", "x"); err != nil || v != "0" {
		t.Errorf("blank → default; got %q,%v", v, err)
	}
	if v, err := mfDecimalOrDefault("12.5", "0", "x"); err != nil || v != "12.5" {
		t.Errorf("12.5 verbatim; got %q,%v", v, err)
	}
	if _, err := mfDecimalOrDefault("-1", "0", "x"); err == nil {
		t.Errorf("negative should error")
	}
	if _, err := mfDecimalOrDefault("abc", "0", "x"); err == nil {
		t.Errorf("non-numeric should error")
	}
}

// TestMaintenanceItemForm_RenderSmoke exercises the render paths (main form +
// every sub-phase) to guard against index panics in the windowed renderers.
func TestMaintenanceItemForm_RenderSmoke(t *testing.T) {
	s := NewMaintenanceItemFormScreen(Deps{}, "")
	s.loading = false
	s.terminalHeight = 30
	s.assets = []omsapi.Asset{{ID: "a1", Name: "Lathe", AssetTag: "LT-1"}}

	if out := s.View(); !strings.Contains(out, "Title") {
		t.Errorf("form view missing Title field: %q", out)
	}

	s.openAssetPick()
	if out := s.View(); !strings.Contains(out, "Pick asset") {
		t.Errorf("asset pick view wrong: %q", out)
	}

	s.phase = mFormPhaseForm
	s.openTaskEditor(-1)
	if out := s.View(); !strings.Contains(out, "task step") {
		t.Errorf("task editor view wrong: %q", out)
	}
	s.commitTaskEditor() // no title → stays, but list view should still render
	s.phase = mFormPhaseTaskList
	if out := s.View(); out == "" {
		t.Errorf("task list view empty")
	}

	s.openMaterialEditor(-1)
	if out := s.View(); !strings.Contains(out, "material") {
		t.Errorf("material editor view wrong: %q", out)
	}
	s.phase = mFormPhaseMaterialList
	if out := s.View(); out == "" {
		t.Errorf("material list view empty")
	}
}
