package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type capture struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// captureServer records the method/path/body of the single request it serves
// and replies with the supplied status + JSON body. Mirrors the httptest
// pattern used by inventory_test.go.
func captureServer(t *testing.T, status int, resp string, into *capture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		into.method = r.Method
		into.path = r.URL.Path
		into.query = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &into.body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
}

// TestCreateMaintenanceItem_Contract pins the create payload: POST to the
// items collection, estimated_cost carried as a string, is_active always
// present, and the nullable interval_days / estimated_time_minutes emitted as
// explicit JSON null when unset (so a PATCH can clear them).
func TestCreateMaintenanceItem_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated,
		`{"id":"mi-1","asset":"asset-9","title":"Replace filter","is_active":true}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.CreateMaintenanceItem(context.Background(), MaintenanceItemWrite{
		Asset:         "asset-9",
		Title:         "Replace filter",
		Description:   "why",
		Instructions:  "steps",
		EstimatedCost: "12.34",
		IsActive:      true,
		// EstimatedTimeMin + IntervalDays intentionally nil → JSON null.
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceItem: %v", err)
	}
	if item == nil || item.ID != "mi-1" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-items/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body["asset"] != "asset-9" {
		t.Errorf("asset = %v", cap.body["asset"])
	}
	if cap.body["title"] != "Replace filter" {
		t.Errorf("title = %v", cap.body["title"])
	}
	// estimated_cost is a JSON string, not a number.
	if v, ok := cap.body["estimated_cost"].(string); !ok || v != "12.34" {
		t.Errorf("estimated_cost = %v (%T), want string \"12.34\"", cap.body["estimated_cost"], cap.body["estimated_cost"])
	}
	if cap.body["is_active"] != true {
		t.Errorf("is_active = %v", cap.body["is_active"])
	}
	// Nullable fields must be present-and-null, not absent.
	if v, ok := cap.body["interval_days"]; !ok || v != nil {
		t.Errorf("interval_days should be present null, got %v (present=%v)", v, ok)
	}
	if v, ok := cap.body["estimated_time_minutes"]; !ok || v != nil {
		t.Errorf("estimated_time_minutes should be present null, got %v (present=%v)", v, ok)
	}
}

// TestCreateMaintenanceItem_IntervalSet confirms a set interval marshals as a
// number.
func TestCreateMaintenanceItem_IntervalSet(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"mi-2"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CreateMaintenanceItem(context.Background(), MaintenanceItemWrite{
		Asset:            "a1",
		Title:            "Lube",
		EstimatedCost:    "0",
		IntervalDays:     intptr(30),
		EstimatedTimeMin: intptr(45),
		IsActive:         true,
	}); err != nil {
		t.Fatalf("CreateMaintenanceItem: %v", err)
	}
	if v, ok := cap.body["interval_days"].(float64); !ok || v != 30 {
		t.Errorf("interval_days = %v (%T)", cap.body["interval_days"], cap.body["interval_days"])
	}
	if v, ok := cap.body["estimated_time_minutes"].(float64); !ok || v != 45 {
		t.Errorf("estimated_time_minutes = %v", cap.body["estimated_time_minutes"])
	}
}

// TestUpdateMaintenanceItem_Contract pins PATCH + path.
func TestUpdateMaintenanceItem_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":"mi-1","title":"Edited"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.UpdateMaintenanceItem(context.Background(), "mi-1", MaintenanceItemWrite{
		Asset: "a1", Title: "Edited", EstimatedCost: "0", IsActive: true,
	})
	if err != nil {
		t.Fatalf("UpdateMaintenanceItem: %v", err)
	}
	if item.Title != "Edited" {
		t.Errorf("title = %q", item.Title)
	}
	if cap.method != http.MethodPatch || cap.path != "/api/inventory/maintenance-items/mi-1/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
}

// TestDeleteMaintenanceItem_Contract pins DELETE + path and tolerates 204.
func TestDeleteMaintenanceItem_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusNoContent, ``, &cap)
	defer srv.Close()

	c := New(srv.URL)
	if err := c.DeleteMaintenanceItem(context.Background(), "mi-1"); err != nil {
		t.Fatalf("DeleteMaintenanceItem: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/inventory/maintenance-items/mi-1/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
}

// TestGetMaintenanceItem_ParsesNested confirms nested materials + tasks and the
// decimal + computed fields deserialize.
func TestGetMaintenanceItem_ParsesNested(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"mi-1","asset":"a1","asset_name":"Lathe","title":"PM",
		"estimated_cost":"5.00","interval_days":30,"is_active":true,
		"is_overdue":true,"days_overdue":3,
		"materials":[{"id":"mat-1","name":"Oil","quantity":"2.00","unit":"qt","estimated_cost_per_unit":"4.50","total_estimated_cost":"9.00"}],
		"tasks":[{"id":"t-1","order":0,"title":"Drain","is_required":true},{"id":"t-2","order":1,"title":"Refill","is_required":false}]
	}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	item, err := c.GetMaintenanceItem(context.Background(), "mi-1")
	if err != nil {
		t.Fatalf("GetMaintenanceItem: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/inventory/maintenance-items/mi-1/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if item.AssetName != "Lathe" || !item.IsOverdue || item.DaysOverdue == nil || *item.DaysOverdue != 3 {
		t.Errorf("computed fields = %+v", item)
	}
	if item.EstimatedCost != "5.00" {
		t.Errorf("estimated_cost = %q", item.EstimatedCost)
	}
	if len(item.Materials) != 1 || item.Materials[0].Name != "Oil" || item.Materials[0].TotalEstimatedCost != "9.00" {
		t.Errorf("materials = %+v", item.Materials)
	}
	if len(item.Tasks) != 2 || item.Tasks[1].Title != "Refill" || item.Tasks[1].IsRequired {
		t.Errorf("tasks = %+v", item.Tasks)
	}
}

// TestCompleteMaintenanceItem_Contract pins the complete action: POST to
// .../complete/, optional fields present when set, and returns the log.
func TestCompleteMaintenanceItem_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated,
		`{"id":"log-1","maintenance_item":"mi-1","time_spent_minutes":20,"cost_incurred":"3.00","completed_by":7}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	log, err := c.CompleteMaintenanceItem(context.Background(), "mi-1", MaintenanceCompleteRequest{
		TimeSpentMinutes: intptr(20),
		CostIncurred:     "3.00",
		Notes:            "done",
	})
	if err != nil {
		t.Fatalf("CompleteMaintenanceItem: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-items/mi-1/complete/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if v, ok := cap.body["time_spent_minutes"].(float64); !ok || v != 20 {
		t.Errorf("time_spent_minutes = %v", cap.body["time_spent_minutes"])
	}
	if cap.body["cost_incurred"] != "3.00" || cap.body["notes"] != "done" {
		t.Errorf("body = %v", cap.body)
	}
	if log.ID != "log-1" || log.TimeSpentMinutes == nil || *log.TimeSpentMinutes != 20 || log.CompletedBy == nil || *log.CompletedBy != 7 {
		t.Errorf("log = %+v", log)
	}
}

// TestCompleteMaintenanceItem_Empty confirms an empty completion sends a bare
// object (all optional fields omitted).
func TestCompleteMaintenanceItem_Empty(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"log-2","maintenance_item":"mi-1"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.CompleteMaintenanceItem(context.Background(), "mi-1", MaintenanceCompleteRequest{}); err != nil {
		t.Fatalf("CompleteMaintenanceItem: %v", err)
	}
	if _, ok := cap.body["time_spent_minutes"]; ok {
		t.Errorf("time_spent_minutes should be omitted, got %v", cap.body["time_spent_minutes"])
	}
	if _, ok := cap.body["cost_incurred"]; ok {
		t.Errorf("cost_incurred should be omitted")
	}
	if _, ok := cap.body["notes"]; ok {
		t.Errorf("notes should be omitted")
	}
}

// TestCloneMaintenanceItem_Contract pins the clone action body + path.
func TestCloneMaintenanceItem_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"mi-clone","asset":"asset-2","title":"PM"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	cloned, err := c.CloneMaintenanceItem(context.Background(), "mi-1", "asset-2")
	if err != nil {
		t.Fatalf("CloneMaintenanceItem: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-items/mi-1/clone/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body["target_asset_id"] != "asset-2" {
		t.Errorf("target_asset_id = %v", cap.body["target_asset_id"])
	}
	if cloned.ID != "mi-clone" || cloned.Asset != "asset-2" {
		t.Errorf("cloned = %+v", cloned)
	}
}

// TestGenerateMaintenanceWorkOrder_Contract pins the generate action + that it
// returns a WorkOrder.
func TestGenerateMaintenanceWorkOrder_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"wo-1","title":"PM","status":"open"}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	wo, err := c.GenerateMaintenanceWorkOrder(context.Background(), "mi-1", MaintenanceWorkOrderRequest{
		DueDate: "2026-08-01",
		Notes:   "seasonal",
	})
	if err != nil {
		t.Fatalf("GenerateMaintenanceWorkOrder: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-items/mi-1/generate_work_order/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if cap.body["due_date"] != "2026-08-01" || cap.body["notes"] != "seasonal" {
		t.Errorf("body = %v", cap.body)
	}
	if wo == nil || wo.Status != "open" {
		t.Errorf("wo = %+v", wo)
	}
}

// TestCheckMaintenanceMaterialStock_Contract pins the GET action + alert parse.
func TestCheckMaintenanceMaterialStock_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"low_stock_alerts":[{"material_id":"mat-1","item_id":"it-1","name":"Oil","current":1,"minimum":5,"reorder_qty":10}]}`,
		&cap)
	defer srv.Close()

	c := New(srv.URL)
	resp, err := c.CheckMaintenanceMaterialStock(context.Background(), "mi-1")
	if err != nil {
		t.Fatalf("CheckMaintenanceMaterialStock: %v", err)
	}
	if cap.method != http.MethodGet || cap.path != "/api/inventory/maintenance-items/mi-1/check_material_stock/" {
		t.Fatalf("method/path = %q %q", cap.method, cap.path)
	}
	if len(resp.LowStockAlerts) != 1 {
		t.Fatalf("alerts = %+v", resp.LowStockAlerts)
	}
	a := resp.LowStockAlerts[0]
	if a.Name != "Oil" || a.Current != 1 || a.Minimum != 5 || a.ReorderQty != 10 {
		t.Errorf("alert = %+v", a)
	}
}

// TestMaintenanceTask_CRUDContract pins the task create/update/delete + list.
func TestMaintenanceTask_CRUDContract(t *testing.T) {
	// create
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"t-1","title":"Drain","order":0,"is_required":true}`, &cap)
	c := New(srv.URL)
	task, err := c.CreateMaintenanceTask(context.Background(), MaintenanceTaskWrite{
		MaintenanceItem: "mi-1", Order: 0, Title: "Drain", Description: "d", IsRequired: true,
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceTask: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-tasks/" {
		t.Fatalf("create method/path = %q %q", cap.method, cap.path)
	}
	if cap.body["maintenance_item"] != "mi-1" || cap.body["title"] != "Drain" || cap.body["is_required"] != true {
		t.Errorf("create body = %v", cap.body)
	}
	if task.ID != "t-1" {
		t.Errorf("task = %+v", task)
	}
	srv.Close()

	// update — a fresh server means a fresh client (baseURL is fixed at New).
	cap = capture{}
	srv = captureServer(t, http.StatusOK, `{"id":"t-1","title":"Drain oil"}`, &cap)
	c = New(srv.URL)
	if _, err := c.UpdateMaintenanceTask(context.Background(), "t-1", MaintenanceTaskWrite{
		MaintenanceItem: "mi-1", Order: 0, Title: "Drain oil", IsRequired: true,
	}); err != nil {
		t.Fatalf("UpdateMaintenanceTask: %v", err)
	}
	if cap.method != http.MethodPatch || cap.path != "/api/inventory/maintenance-tasks/t-1/" {
		t.Fatalf("update method/path = %q %q", cap.method, cap.path)
	}
	srv.Close()

	// delete
	cap = capture{}
	srv = captureServer(t, http.StatusNoContent, ``, &cap)
	c = New(srv.URL)
	defer srv.Close()
	if err := c.DeleteMaintenanceTask(context.Background(), "t-1"); err != nil {
		t.Fatalf("DeleteMaintenanceTask: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/inventory/maintenance-tasks/t-1/" {
		t.Fatalf("delete method/path = %q %q", cap.method, cap.path)
	}
}

// TestMaintenanceMaterial_CRUDContract pins the material create/update/delete +
// list, including the decimal fields going out as strings.
func TestMaintenanceMaterial_CRUDContract(t *testing.T) {
	// create
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"mat-1","name":"Oil"}`, &cap)
	c := New(srv.URL)
	mat, err := c.CreateMaintenanceMaterial(context.Background(), MaintenanceMaterialWrite{
		MaintenanceItem: "mi-1", Name: "Oil", Quantity: "2", Unit: "qt", EstimatedCostPerUnit: "4.50", Notes: "n",
	})
	if err != nil {
		t.Fatalf("CreateMaintenanceMaterial: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/maintenance-materials/" {
		t.Fatalf("create method/path = %q %q", cap.method, cap.path)
	}
	if v, ok := cap.body["quantity"].(string); !ok || v != "2" {
		t.Errorf("quantity should be string, got %v (%T)", cap.body["quantity"], cap.body["quantity"])
	}
	if v, ok := cap.body["estimated_cost_per_unit"].(string); !ok || v != "4.50" {
		t.Errorf("estimated_cost_per_unit should be string, got %v", cap.body["estimated_cost_per_unit"])
	}
	if mat.ID != "mat-1" {
		t.Errorf("material = %+v", mat)
	}
	srv.Close()

	// update — a fresh server means a fresh client (baseURL is fixed at New).
	cap = capture{}
	srv = captureServer(t, http.StatusOK, `{"id":"mat-1","name":"Oil 5W"}`, &cap)
	c = New(srv.URL)
	if _, err := c.UpdateMaintenanceMaterial(context.Background(), "mat-1", MaintenanceMaterialWrite{
		MaintenanceItem: "mi-1", Name: "Oil 5W", Quantity: "3", Unit: "qt", EstimatedCostPerUnit: "5.00",
	}); err != nil {
		t.Fatalf("UpdateMaintenanceMaterial: %v", err)
	}
	if cap.method != http.MethodPatch || cap.path != "/api/inventory/maintenance-materials/mat-1/" {
		t.Fatalf("update method/path = %q %q", cap.method, cap.path)
	}
	srv.Close()

	// delete
	cap = capture{}
	srv = captureServer(t, http.StatusNoContent, ``, &cap)
	c = New(srv.URL)
	defer srv.Close()
	if err := c.DeleteMaintenanceMaterial(context.Background(), "mat-1"); err != nil {
		t.Fatalf("DeleteMaintenanceMaterial: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/inventory/maintenance-materials/mat-1/" {
		t.Fatalf("delete method/path = %q %q", cap.method, cap.path)
	}
}

// TestListMaintenance_QueryParam confirms the list helpers scope by
// maintenance_item.
func TestListMaintenance_QueryParam(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"count":0,"results":[]}`, &cap)
	defer srv.Close()

	c := New(srv.URL)
	if _, err := c.ListMaintenanceTasks(context.Background(), "mi-1"); err != nil {
		t.Fatalf("ListMaintenanceTasks: %v", err)
	}
	if cap.path != "/api/inventory/maintenance-tasks/" || cap.query != "maintenance_item=mi-1" {
		t.Errorf("tasks path/query = %q %q", cap.path, cap.query)
	}

	if _, err := c.ListMaintenanceMaterials(context.Background(), "mi-1"); err != nil {
		t.Fatalf("ListMaintenanceMaterials: %v", err)
	}
	if cap.path != "/api/inventory/maintenance-materials/" || cap.query != "maintenance_item=mi-1" {
		t.Errorf("materials path/query = %q %q", cap.path, cap.query)
	}
}
