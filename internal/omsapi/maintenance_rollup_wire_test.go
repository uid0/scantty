package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Decode, request-shape and refusal tests for the maintenance rollup, built from
// RECORDED OMS responses (testdata/README.md carries the rule and the
// provenance of every maintenance_*.json).

type rollupCall struct {
	method, path, query string
	body                map[string]any
	rawBody             string
}

// rollupServer answers every request with one recorded body at `status`, and
// records what the client really sent.
func rollupServer(t *testing.T, fixture string, status int, call *rollupCall) *Client {
	t.Helper()
	body := wireBody(t, fixture)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if call != nil {
			call.method, call.path, call.query = r.Method, r.URL.Path, r.URL.RawQuery
			raw, _ := io.ReadAll(r.Body)
			call.rawBody = string(raw)
			call.body = nil
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &call.body)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// THE DUE LISTS ARE BARE ARRAYS AND THEY CARRY THE PAST. The recorded week holds
// an item never completed (next_due_at null, is_overdue true), an overdue one
// with a date, and two due in the next few days — the three states the web
// sorts into "Overdue" and "Due this week", which is what a decode of an empty
// list could never have shown.
func TestListMaintenanceItemsDue_DecodesTheBytesOMSReallySends(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_due_week.json", http.StatusOK, &call)
	week, err := c.ListMaintenanceItemsDueThisWeek(context.Background())
	if err != nil {
		t.Fatalf("due this week: %v", err)
	}
	if call.method != http.MethodGet || call.path != "/api/inventory/maintenance-items/due_this_week/" {
		t.Errorf("went %s %s", call.method, call.path)
	}
	var sawNever, sawDatedOverdue, sawUpcoming bool
	for _, it := range week {
		if it.ID == "" || it.Asset == "" || it.AssetName == "" {
			t.Errorf("item %+v lost its identity in the decode", it)
		}
		switch {
		case it.IsOverdue && it.NextDueAt == nil:
			sawNever = true
		case it.IsOverdue:
			sawDatedOverdue = true
		default:
			sawUpcoming = it.NextDueAt != nil
		}
	}
	if !sawNever || !sawDatedOverdue || !sawUpcoming {
		t.Errorf("fixture reaches never=%v overdue=%v upcoming=%v; all three are the web's sections",
			sawNever, sawDatedOverdue, sawUpcoming)
	}

	c = rollupServer(t, "maintenance_due_month.json", http.StatusOK, &call)
	month, err := c.ListMaintenanceItemsDueThisMonth(context.Background())
	if err != nil {
		t.Fatalf("due this month: %v", err)
	}
	if call.path != "/api/inventory/maintenance-items/due_this_month/" {
		t.Errorf("month went to %s", call.path)
	}
	inWeek := map[string]bool{}
	for _, it := range week {
		inWeek[it.ID] = true
	}
	extra := 0
	for _, it := range month {
		if !inWeek[it.ID] {
			extra++
		}
	}
	if len(month) <= len(week) || extra != len(month)-len(week) {
		t.Errorf("month (%d) is not a strict superset of week (%d) in the recording", len(month), len(week))
	}
}

// The week fixture's due list is what `generate_work_orders_bulk/` acts on, and
// the recorded run created one FEWER than were due: the item that already had an
// open work order was skipped, and the reply says nothing about it. The
// second run created nothing and still answered 201.
func TestGenerateDueMaintenanceWorkOrders_PostsNothingAndReadsTheCount(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_generate_bulk.json", http.StatusCreated, &call)
	got, err := c.GenerateDueMaintenanceWorkOrders(context.Background())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if call.method != http.MethodPost || call.path != "/api/inventory/maintenance-items/generate_work_orders_bulk/" {
		t.Errorf("went %s %s", call.method, call.path)
	}
	if strings.TrimSpace(call.rawBody) != "{}" {
		t.Errorf("body = %q, want {} — the server picks the items and the web sends nothing", call.rawBody)
	}
	if got.Created != 3 || len(got.WorkOrderIDs) != 3 {
		t.Errorf("decoded %+v, want the recorded 3 created", got)
	}

	c = rollupServer(t, "maintenance_generate_bulk_none.json", http.StatusCreated, nil)
	none, err := c.GenerateDueMaintenanceWorkOrders(context.Background())
	if err != nil {
		t.Fatalf("an empty run is a 201, not an error: %v", err)
	}
	if none.Created != 0 || len(none.WorkOrderIDs) != 0 {
		t.Errorf("empty run decoded %+v", none)
	}

	c = rollupServer(t, "maintenance_generate_bulk_anonymous.json", http.StatusUnauthorized, nil)
	if _, err := c.GenerateDueMaintenanceWorkOrders(context.Background()); err == nil {
		t.Error("a 401 decoded as a success")
	}
}

func TestGetMaintenanceDashboard_DecodesTheHandBuiltBody(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_dashboard.json", http.StatusOK, &call)
	d, err := c.GetMaintenanceDashboard(context.Background())
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if call.path != "/api/inventory/maintenance/dashboard/" {
		t.Errorf("went to %s", call.path)
	}
	var sawOverdue, sawNever bool
	for _, pm := range d.ScheduledPM {
		if pm.MaintenanceItemID == "" || pm.IntervalDays == 0 {
			t.Errorf("scheduled row %+v lost a key", pm)
		}
		if pm.DaysUntil != nil && *pm.DaysUntil < 0 && pm.IsOverdue {
			sawOverdue = true
		}
		if pm.DaysUntil == nil && pm.NextDue == nil {
			sawNever = true
		}
	}
	if !sawOverdue || !sawNever {
		t.Errorf("scheduled PM reaches overdue=%v never=%v", sawOverdue, sawNever)
	}
	if len(d.Unscheduled) == 0 || d.Unscheduled[0].ShortID == "" || d.Unscheduled[0].OpenedAt.IsZero() {
		t.Errorf("unscheduled = %+v", d.Unscheduled)
	}
	if d.Costs.PerPeriod.AllTime.Empty() || d.Costs.PerPeriod.Today.Empty() {
		t.Errorf("per-period costs = %+v", d.Costs.PerPeriod)
	}
	if len(d.Costs.ByAsset) == 0 || d.Costs.ByAsset[0].TotalCost.Empty() {
		t.Errorf("by-asset costs = %+v", d.Costs.ByAsset)
	}
}

func TestListActiveMaintenance_DecodesEveryKind(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_active.json", http.StatusOK, &call)
	a, err := c.ListActiveMaintenance(context.Background())
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if call.path != "/api/inventory/maintenance/active/" {
		t.Errorf("went to %s", call.path)
	}
	if a.Count != len(a.Results) {
		t.Errorf("count %d against %d rows", a.Count, len(a.Results))
	}
	kinds := map[string]ActiveMaintenanceRow{}
	for _, r := range a.Results {
		kinds[r.Kind] = r
	}
	for _, k := range []string{ActiveKindWorkOrder, ActiveKindAssetProblem, ActiveKindLocationProblem} {
		if _, ok := kinds[k]; !ok {
			t.Errorf("recording reaches no %s row", k)
		}
	}
	if lp := kinds[ActiveKindLocationProblem]; lp.LocationID == nil || lp.Severity == nil || lp.AssetName != nil {
		t.Errorf("location problem decoded %+v", lp)
	}
}

// THE RAW TYPES, read off the recorded bytes rather than the structs: the
// location pk is the one integer id in `active`, the user pk the one integer id
// in the history, and every money figure on the dashboard is a string.
func TestMaintenanceRollupFixtures_CarryTheServersOwnTypes(t *testing.T) {
	var active struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(wireBody(t, "maintenance_active.json"), &active); err != nil {
		t.Fatal(err)
	}
	for i, r := range active.Results {
		if _, ok := r["id"].(string); !ok {
			t.Errorf("active[%d].id is %T, want a UUID string", i, r["id"])
		}
		if loc, present := r["location_id"]; present && loc != nil {
			if _, ok := loc.(float64); !ok {
				t.Errorf("active[%d].location_id is %T, want a JSON number (BigAutoField)", i, loc)
			}
		}
	}

	var dash struct {
		Costs struct {
			PerPeriod map[string]any   `json:"per_period"`
			ByAsset   []map[string]any `json:"by_asset"`
		} `json:"costs"`
	}
	if err := json.Unmarshal(wireBody(t, "maintenance_dashboard.json"), &dash); err != nil {
		t.Fatal(err)
	}
	for k, v := range dash.Costs.PerPeriod {
		if _, ok := v.(string); !ok {
			t.Errorf("per_period.%s is %T, want a string", k, v)
		}
	}
	for i, a := range dash.Costs.ByAsset {
		if _, ok := a["total_cost"].(string); !ok {
			t.Errorf("by_asset[%d].total_cost is %T, want a string", i, a["total_cost"])
		}
	}

	var hist struct {
		TotalCost any              `json:"total_cost"`
		Results   []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(wireBody(t, "maintenance_history.json"), &hist); err != nil {
		t.Fatal(err)
	}
	if _, ok := hist.TotalCost.(string); !ok {
		t.Errorf("total_cost is %T, want a string", hist.TotalCost)
	}
	var sawUser, sawNullCost bool
	for i, r := range hist.Results {
		by, _ := r["performed_by"].(map[string]any)
		if u, ok := by["internal_user"].(map[string]any); ok {
			if _, isNum := u["id"].(float64); !isNum {
				t.Errorf("results[%d].performed_by.internal_user.id is %T, want a number", i, u["id"])
			}
			sawUser = true
		}
		if r["cost"] == nil {
			sawNullCost = true
		}
	}
	if !sawUser || !sawNullCost {
		t.Errorf("history reaches an internal user=%v and a null cost=%v; both are needed", sawUser, sawNullCost)
	}
}

func TestGetAssetMaintenanceHistory_SendsTheFiltersAndDecodesBothSources(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_history.json", http.StatusOK, &call)
	h, err := c.GetAssetMaintenanceHistory(context.Background(), "asset-1",
		MaintenanceHistoryQuery{Since: "2023-09-14", Until: "2026-09-14", Source: MaintenanceSourceHistorical})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if call.path != "/api/inventory/assets/asset-1/maintenance-history/" {
		t.Errorf("went to %s", call.path)
	}
	for _, want := range []string{"since=2023-09-14", "until=2026-09-14", "source=historical"} {
		if !strings.Contains(call.query, want) {
			t.Errorf("query %q lacks %s", call.query, want)
		}
	}
	if h.Count != len(h.Results) || h.TotalCost != "1052.00" {
		t.Errorf("count %d total %q against %d rows", h.Count, h.TotalCost, len(h.Results))
	}
	sources := map[string]MaintenanceHistoryEntry{}
	for _, r := range h.Results {
		sources[r.Source] = r
		if r.CompletedOn.IsZero() {
			t.Errorf("row %q lost its completion date", r.Title)
		}
	}
	wo, rec := sources[MaintenanceSourceWorkOrder], sources[MaintenanceSourceHistorical]
	if wo.DetailURL == nil || wo.PerformedBy.Vendor == nil {
		t.Errorf("work-order row decoded %+v", wo)
	}
	if rec.ID == "" || rec.DetailURL != nil {
		t.Errorf("historical row decoded %+v", rec)
	}

	c = rollupServer(t, "maintenance_history_empty.json", http.StatusOK, nil)
	empty, err := c.GetAssetMaintenanceHistory(context.Background(), "asset-2", MaintenanceHistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if empty.TotalCost != "0" || empty.Count != 0 {
		t.Errorf("empty history decoded %+v — total_cost is str(0), not 0.00", empty)
	}
}

// The date refusal is written by the view body, so it arrives as a raw
// {"detail": …} that parseError cannot read — and its sentence carries the
// ValidationError's list brackets, which is the server's own wording.
func TestGetAssetMaintenanceHistory_ABadDateIsTheServersSentence(t *testing.T) {
	c := rollupServer(t, "maintenance_history_bad_since.json", http.StatusBadRequest, nil)
	_, err := c.GetAssetMaintenanceHistory(context.Background(), "asset-1", MaintenanceHistoryQuery{Since: "2025-13-01"})
	if err == nil {
		t.Fatal("a 400 decoded as a history")
	}
	got, ok := AsDetailRefusal(err)
	if !ok || got != "['since must be a YYYY-MM-DD date']" {
		t.Errorf("AsDetailRefusal = %q, %v", got, ok)
	}
}

// THE CREATE BODY IS THE WEB'S: an unset vendor, internal performer and cost go
// as explicit nulls, and no attachment key is sent at all.
func TestCreateMaintenanceRecord_SendsExplicitNullsAndDecodesTheRecord(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_record_create.json", http.StatusCreated, &call)
	vendor, cost := "baf2b25f-21c9-4b0a-a64a-e1f9fda5809f", "75.25"
	rec, err := c.CreateMaintenanceRecord(context.Background(), MaintenanceRecordWrite{
		Asset: "13b27d28-d92c-4ffc-ab06-b158bc4194d6", Title: "Trunnion lubrication",
		Description: "Cleaned and greased trunnions.", CompletedOn: "2023-06-02",
		Vendor: &vendor, Cost: &cost, InvoiceNumber: "HC-0931", Notes: "Backdated from paper log.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if call.method != http.MethodPost || call.path != "/api/inventory/maintenance-records/" {
		t.Errorf("went %s %s", call.method, call.path)
	}
	if v, present := call.body["performed_by_internal"]; !present || v != nil {
		t.Errorf("performed_by_internal = %v (present %v), want an explicit null", v, present)
	}
	if _, present := call.body["attachment"]; present {
		t.Error("the body carries an attachment key; no terminal flow uploads one")
	}
	if rec.CompletedOn.String() != "2023-06-02" || rec.Cost != "75.25" || rec.RecordedBy == nil || *rec.RecordedBy != 1 {
		t.Errorf("record decoded %+v", rec)
	}

	var noCost rollupCall
	c = rollupServer(t, "maintenance_record_create.json", http.StatusCreated, &noCost)
	if _, err := c.CreateMaintenanceRecord(context.Background(), MaintenanceRecordWrite{Asset: "a", Title: "t", Description: "d", CompletedOn: "2023-01-01", Vendor: &vendor}); err != nil {
		t.Fatal(err)
	}
	if v, present := noCost.body["cost"]; !present || v != nil {
		t.Errorf("an unset cost went as %v (present %v), want null", v, present)
	}
}

// Every refusal a record write can meet, as the server wrote it: the two
// validation rules come back as their field's own sentence through
// AsFieldRefusal, and a plain member's 403 is the coded envelope.
func TestCreateMaintenanceRecord_RefusalsAreTheFieldsOwnSentence(t *testing.T) {
	for fixture, want := range map[string]string{
		"maintenance_record_create_refused.json": "performed_by_internal: Either a vendor or an internal staff member must be set.",
		"maintenance_record_create_future.json":  "completed_on: completed_on cannot be in the future.",
	} {
		c := rollupServer(t, fixture, http.StatusBadRequest, nil)
		_, err := c.CreateMaintenanceRecord(context.Background(), MaintenanceRecordWrite{})
		got, ok := AsFieldRefusal(err)
		if !ok || got != want {
			t.Errorf("%s: AsFieldRefusal = %q, %v; want %q", fixture, got, ok, want)
		}
	}
	c := rollupServer(t, "maintenance_record_create_forbidden.json", http.StatusForbidden, nil)
	_, err := c.CreateMaintenanceRecord(context.Background(), MaintenanceRecordWrite{})
	var api *APIError
	if !errors.As(err, &api) || !api.IsForbidden() || api.Message != "You do not have permission to perform this action." {
		t.Errorf("403 decoded as %v", err)
	}
}

// THE EDIT NAMES ONLY THE NOTES, so a PATCH from a stale row cannot rewrite a
// date or a cost someone else corrected.
func TestUpdateMaintenanceRecordNotes_PatchesOnlyTheNotes(t *testing.T) {
	var call rollupCall
	c := rollupServer(t, "maintenance_record_patch_notes.json", http.StatusOK, &call)
	rec, err := c.UpdateMaintenanceRecordNotes(context.Background(), "0d077dec-84bc-4f41-b65c-65a89ca16df8",
		"Backdated from the 2023 paper log, page 14.")
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if call.method != http.MethodPatch || call.path != "/api/inventory/maintenance-records/0d077dec-84bc-4f41-b65c-65a89ca16df8/" {
		t.Errorf("went %s %s", call.method, call.path)
	}
	if len(call.body) != 1 || call.body["notes"] != "Backdated from the 2023 paper log, page 14." {
		t.Errorf("body = %v, want exactly {notes}", call.body)
	}
	if rec.Notes != "Backdated from the 2023 paper log, page 14." {
		t.Errorf("record decoded %+v", rec)
	}
}
