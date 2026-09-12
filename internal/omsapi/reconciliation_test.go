package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetLocationReconcileGrid_PathAndDecode pins the grid's wire contract: the
// canonical slashed path, and a full decode of the count-unit fields that
// decide which unit a row is counted in.
//
// The fixture carries BOTH shapes on purpose. An each-counted row where
// projected and projected_at_unit are equal is the common case and proves
// nothing about the conversion; the case-counted row beside it (1400 gloves =
// 14 boxes) is the one where reading the wrong field is a factor of a hundred.
func TestGetLocationReconcileGrid_PathAndDecode(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"location_id":"7","location_name":"Machine shop",
			"items":[
			 {"item_id":"11111111-1111-1111-1111-111111111111","name":"Blue nitrile gloves",
			  "sku":"NIT-BLU-M","projected":1400,"minimum_stock":12,"reorder_quantity":6,
			  "owning_group_name":"Metal shop","count_mode":"by_level","count_unit":"box",
			  "projected_at_unit":14,"open_container_count":0},
			 {"item_id":"22222222-2222-2222-2222-222222222222","name":"M8 hex bolt",
			  "sku":"BOLT-M8","projected":250,"minimum_stock":50,"reorder_quantity":100,
			  "owning_group_name":"","count_mode":"each","count_unit":"bolt",
			  "projected_at_unit":250,"open_container_count":0}
			]}`))
	}))
	defer srv.Close()

	grid, err := New(srv.URL).GetLocationReconcileGrid(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetLocationReconcileGrid: %v", err)
	}
	if gotPath != "/api/inventory/locations/7/reconcile/" {
		t.Fatalf("path = %q", gotPath)
	}
	if grid.LocationName != "Machine shop" || grid.LocationID != "7" {
		t.Fatalf("location = %q/%q", grid.LocationID, grid.LocationName)
	}
	if len(grid.Items) != 2 {
		t.Fatalf("items = %d", len(grid.Items))
	}

	gloves := grid.Items[0]
	if gloves.Projected != 1400 || gloves.ProjectedAtUnit != 14 {
		t.Fatalf("gloves projected = %d base / %d at unit", gloves.Projected, gloves.ProjectedAtUnit)
	}
	if gloves.Unit() != "box" {
		t.Fatalf("gloves unit = %q", gloves.Unit())
	}
	if !gloves.CountsInPacks() {
		t.Fatal("a by_level row must count in packs: its number is boxes, not gloves")
	}
	if gloves.MinimumStock != 12 {
		t.Fatalf("gloves minimum = %d (boxes)", gloves.MinimumStock)
	}

	bolts := grid.Items[1]
	if bolts.CountsInPacks() {
		t.Fatal("an each row must not count in packs")
	}
	if bolts.Projected != bolts.ProjectedAtUnit {
		t.Fatal("an each row's two projections are the same number")
	}
}

// TestReconcileGrid_AnEmptyShelfStillKnowsItsUnit is the state the pack/base
// question is most often asked in and the one a value-comparison gets wrong: an
// item with nothing on the shelf reads 0 in every unit, so CountsInPacks has to
// come off count_mode rather than off projected vs projected_at_unit. Read the
// other way, "5" against this row would store five gloves instead of five boxes.
func TestReconcileGrid_AnEmptyShelfStillKnowsItsUnit(t *testing.T) {
	empty := LocationReconcileItem{
		CountMode: CountModeByLevel, CountUnit: "box",
		Projected: 0, ProjectedAtUnit: 0,
	}
	if empty.Projected != empty.ProjectedAtUnit {
		t.Fatal("fixture is not the ambiguous state it exists to be")
	}
	if !empty.CountsInPacks() {
		t.Fatal("an empty case-counted shelf still counts in boxes")
	}
}

// TestSubmitReconciliationBatch_EveryRowStatesItsUnit is the payload half of the
// unit rule: at_level is present on EVERY row, including the base-unit ones, so
// a recorded request can never say what was counted without saying what it was
// counted in.
func TestSubmitReconciliationBatch_EveryRowStatesItsUnit(t *testing.T) {
	var gotPath string
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"reconciled":2,"reorders_created":1,"reconciliations":[
			{"id":9,"item":"11111111-1111-1111-1111-111111111111","item_name":"Blue nitrile gloves",
			 "item_sku":"NIT-BLU-M","projected_count":1400,"actual_count":1200,"delta":-200,
			 "reason":"miscounted","notes":"","reconciled_by_name":"alice",
			 "reconciled_at":"2026-09-11T10:00:00Z","triggered_reorder_id":4}]}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL).SubmitReconciliationBatch(context.Background(), []ReconciliationRow{
		{ItemID: "11111111-1111-1111-1111-111111111111", ActualCount: 12, Reason: "miscounted", AtLevel: true},
		{ItemID: "22222222-2222-2222-2222-222222222222", ActualCount: 40, Reason: "miscounted", AtLevel: false},
	})
	if err != nil {
		t.Fatalf("SubmitReconciliationBatch: %v", err)
	}
	if gotPath != "/api/inventory/reconciliations/batch/" {
		t.Fatalf("path = %q", gotPath)
	}
	rows, _ := raw["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	for i, r := range rows {
		row, _ := r.(map[string]any)
		if _, ok := row["at_level"]; !ok {
			t.Fatalf("row %d omitted at_level: the payload does not say what unit %v is", i, row["actual_count"])
		}
	}
	if first, _ := rows[0].(map[string]any); first["at_level"] != true {
		t.Fatalf("row 0 at_level = %v, want true (12 boxes)", first["at_level"])
	}
	if second, _ := rows[1].(map[string]any); second["at_level"] != false {
		t.Fatalf("row 1 at_level = %v, want false (40 bolts)", second["at_level"])
	}
	if res.Reconciled != 2 || res.ReordersCreated != 1 {
		t.Fatalf("result = %d reconciled / %d reorders", res.Reconciled, res.ReordersCreated)
	}
	if len(res.Reconciliations) != 1 || res.Reconciliations[0].Delta != -200 {
		t.Fatalf("audit rows = %+v", res.Reconciliations)
	}
	if res.Reconciliations[0].TriggeredReorderID == nil {
		t.Fatal("the audit row's triggered reorder id decoded as nil")
	}
}

// TestSubmitReconciliationBatch_OpenCountOnlyWhenSet holds the one optional
// quantity that must be ABSENT rather than zero: the server refuses open_count
// outright for an item not counted open/closed, so sending 0 for every row
// would refuse the whole batch.
func TestSubmitReconciliationBatch_OpenCountOnlyWhenSet(t *testing.T) {
	var raw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"reconciled":2,"reorders_created":0,"reconciliations":[]}`))
	}))
	defer srv.Close()

	open := 3
	if _, err := New(srv.URL).SubmitReconciliationBatch(context.Background(), []ReconciliationRow{
		{ItemID: "a", ActualCount: 1, Reason: "miscounted", AtLevel: true, OpenCount: &open},
		{ItemID: "b", ActualCount: 2, Reason: "miscounted", AtLevel: false},
	}); err != nil {
		t.Fatalf("SubmitReconciliationBatch: %v", err)
	}
	rows, _ := raw["rows"].([]any)
	first, _ := rows[0].(map[string]any)
	if first["open_count"] != float64(3) {
		t.Fatalf("row 0 open_count = %v", first["open_count"])
	}
	second, _ := rows[1].(map[string]any)
	if _, ok := second["open_count"]; ok {
		t.Fatalf("row 1 sent open_count = %v; an item that is not open/closed must not carry the key", second["open_count"])
	}
}

// TestSubmitReconciliationBatch_ARefusalReachesTheOperatorAsProse pins the
// recovery of the hand-written {"detail": ...} refusal. Without it the operator
// reads raw JSON: parseError puts the whole body into APIError.Message whenever
// the envelope carries no code, and these views never reach DRF's handler.
func TestSubmitReconciliationBatch_ARefusalReachesTheOperatorAsProse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail": "You do not have permission to reconcile item Blue nitrile gloves."}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL).SubmitReconciliationBatch(context.Background(), []ReconciliationRow{
		{ItemID: "a", ActualCount: 1, Reason: "miscounted"},
	})
	if err == nil {
		t.Fatal("a 403 answered as success")
	}
	prose, ok := AsDetailRefusal(err)
	if !ok {
		t.Fatalf("AsDetailRefusal declined %q", err.Error())
	}
	if prose != "You do not have permission to reconcile item Blue nitrile gloves." {
		t.Fatalf("prose = %q", prose)
	}
}

// TestAsDetailRefusal_LeavesEveryOtherShapeAlone is the narrowness half. Each
// case is a body this recogniser must NOT claim, because mangling it into a
// sentence would replace a fact with a guess.
func TestAsDetailRefusal_LeavesEveryOtherShapeAlone(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a gateway page", "<!DOCTYPE html><html><body>502 Bad Gateway</body></html>"},
		{"a field validation body", `{"rows": [{"actual_count": ["Ensure this value is >= 0."]}]}`},
		{"the standard envelope", `{"error": {"code": "validation_failed", "message": "nope"}}`},
		{"a blank detail", `{"detail": "   "}`},
		{"a non-string detail", `{"detail": {"nested": "thing"}}`},
	} {
		if prose, ok := AsDetailRefusal(&APIError{Status: 400, Message: tc.body}); ok {
			t.Fatalf("%s was claimed as a refusal: %q", tc.name, prose)
		}
	}
}

// TestSubmitReconciliationBatch_AnEmptyBatchNeverLeavesTheTerminal: the server
// rejects an empty rows list, so sending one spends a round trip to be told
// what this side already knows.
func TestSubmitReconciliationBatch_AnEmptyBatchNeverLeavesTheTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an empty batch reached the server at %s", r.URL.Path)
	}))
	defer srv.Close()
	if _, err := New(srv.URL).SubmitReconciliationBatch(context.Background(), nil); err == nil {
		t.Fatal("an empty batch was accepted")
	}
}

// TestReconciliationRow_FilesAReorderIsJudgedInTheRowsOwnUnit: the comparison
// is count-unit against count-unit on both sides. Twelve BOXES against a
// twelve-BOX minimum trips it; the same shelf read as 1200 gloves does not,
// which is the arithmetic a base-unit comparison would do.
func TestReconciliationRow_FilesAReorderIsJudgedInTheRowsOwnUnit(t *testing.T) {
	boxes := ReconciliationRow{ActualCount: 12, AtLevel: true}
	if !boxes.FilesAReorder(12) {
		t.Fatal("12 boxes is at the 12-box minimum and must file")
	}
	if !(ReconciliationRow{ActualCount: 11, AtLevel: true}).FilesAReorder(12) {
		t.Fatal("below the minimum must file")
	}
	if (ReconciliationRow{ActualCount: 13, AtLevel: true}).FilesAReorder(12) {
		t.Fatal("above the minimum must not file")
	}
	if (ReconciliationRow{ActualCount: 1, AtLevel: true, SkipReorder: true}).FilesAReorder(12) {
		t.Fatal("skip_reorder suppresses it")
	}
}
