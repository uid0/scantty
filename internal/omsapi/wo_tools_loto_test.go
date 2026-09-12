package omsapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The two halves of one work-order screen: the per-job TOOL rows (op-0v4) and
// the structured LOTO record. Both are pinned against the OMS source that builds
// them — inventory/serializers.py's WorkOrderToolSerializer /
// WorkOrderAdHocToolSerializer / WorkOrderToolLocationSerializer /
// WorkOrderLotoCompletionSerializer and inventory/views.py's add_tool /
// tool_detail / complete_loto — rather than against anything derived from these
// structs, which is the fixture rule AGENTS.md records ("a fixture written from
// the struct cannot contradict the struct").

// TestGetWorkOrder_ParsesToolRowsAndLotoCompletions pins the two DETAIL-only
// blocks this work adds to the decode, and the one thing about them that is easy
// to get wrong: `tools` and `tool_rows` are DIFFERENT keys carrying different
// facts, so the lean display projection must not be mistaken for the editable
// rows. The fixture serves a work order whose displayed tools come from its PM
// TEMPLATE — the legacy shape, where tool_rows is absent entirely — beside one
// whose rows are its own.
func TestGetWorkOrder_ParsesToolRowsAndLotoCompletions(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"wo-7","display_title":"Quarterly PM","status":"open",
		"tools":[
			{"id":"wt-1","name":"Torque wrench","quantity":2,"location_hint":"Bench 2","is_required":true,"notes":""}
		],
		"tool_rows":[
			{"id":"wt-1","work_order":"wo-7","tool":41,"inventory_item":"inv-9",
			 "inventory_item_name":"Torque wrench 1/2\"","is_ad_hoc":false,
			 "name":"Torque wrench","quantity":2,"location_hint":"",
			 "resolved_location":"Tool crib, drawer 3","is_required":true,"notes":"calibrated",
			 "created_at":"2026-09-01T10:00:00Z"},
			{"id":"wt-2","work_order":"wo-7","tool":null,"inventory_item":null,
			 "inventory_item_name":null,"is_ad_hoc":true,
			 "name":"Scissor lift key","quantity":1,"location_hint":"Bench 2",
			 "resolved_location":"Bench 2","is_required":false,"notes":"",
			 "created_at":"2026-09-02T11:30:00Z"}
		],
		"loto_completions":[
			{"id":"lc-1","work_order":"wo-7","energy_source":18,"source_type":"electrical",
			 "source_label":"Electrical (240V)","isolation_point":"Panel B, breaker 14",
			 "required_devices":"Red padlock #12, breaker lockout clamp",
			 "is_completed":true,"completed_by":4,"completed_by_name":"Dana Reyes",
			 "completed_at":"2026-09-11T08:15:00Z","notes":"verified de-energized",
			 "created_at":"2026-09-01T10:00:00Z"},
			{"id":"lc-2","work_order":"wo-7","energy_source":null,"source_type":"pneumatic",
			 "source_label":"Pneumatic (90 psi)","isolation_point":"","required_devices":"",
			 "is_completed":false,"completed_by":null,"completed_by_name":null,
			 "completed_at":null,"notes":"","created_at":"2026-09-01T10:00:00Z"}
		]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "wo-7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}

	if len(wo.ToolRows) != 2 {
		t.Fatalf("tool_rows = %+v", wo.ToolRows)
	}
	// The template-derived row: not removable, and its LOCATION is the resolved
	// value — the hint is blank because the linked item's location stands in, so
	// a surface reading the hint would show nothing for a tool that has a place.
	tmpl := wo.ToolRows[0]
	if tmpl.IsAdHoc {
		t.Errorf("tool_rows[0] is template-derived on the wire, decoded is_ad_hoc=true: %+v", tmpl)
	}
	if tmpl.LocationHint != "" || tmpl.ResolvedLocation != "Tool crib, drawer 3" {
		t.Errorf("tool_rows[0] hint/resolved = %q/%q", tmpl.LocationHint, tmpl.ResolvedLocation)
	}
	if tmpl.InventoryItem == nil || *tmpl.InventoryItem != "inv-9" || tmpl.InventoryItemName != `Torque wrench 1/2"` {
		t.Errorf("tool_rows[0] stock link = %+v", tmpl)
	}
	if tmpl.IDString() != "wt-1" {
		t.Errorf("tool_rows[0] id = %q", tmpl.IDString())
	}
	adhoc := wo.ToolRows[1]
	if !adhoc.IsAdHoc || adhoc.InventoryItem != nil || adhoc.Quantity != 1 {
		t.Errorf("tool_rows[1] = %+v", adhoc)
	}
	// `tools` is still the lean six-key projection and is NOT the editable rows.
	if len(wo.Tools) != 1 || wo.Tools[0].Name != "Torque wrench" {
		t.Errorf("tools = %+v", wo.Tools)
	}

	if len(wo.LotoCompletions) != 2 {
		t.Fatalf("loto_completions = %+v", wo.LotoCompletions)
	}
	done := wo.LotoCompletions[0]
	if !done.IsCompleted || done.CompletedByName != "Dana Reyes" || done.CompletedAt == nil {
		t.Errorf("loto[0] = %+v", done)
	}
	if done.IsolationPoint != "Panel B, breaker 14" ||
		done.RequiredDevices != "Red padlock #12, breaker lockout clamp" {
		t.Errorf("loto[0] isolation/devices = %+v", done)
	}
	if done.IDString() != "lc-1" {
		t.Errorf("loto[0] id = %q", done.IDString())
	}
	// A DELETED energy source leaves the denormalized copy standing: the record
	// still says what had to be isolated. Decoding must not fail over the null.
	open := wo.LotoCompletions[1]
	if open.IsCompleted || open.CompletedAt != nil || open.CompletedByName != "" {
		t.Errorf("loto[1] = %+v", open)
	}
	if open.SourceLabel != "Pneumatic (90 psi)" {
		t.Errorf("loto[1] label = %q", open.SourceLabel)
	}
}

// TestGetWorkOrder_LegacyWorkOrderServesToolsWithoutRows: the shape a work order
// generated before per-job tools has. `tools` is the PM TEMPLATE's list and
// `tool_rows` is absent — which is the state a client has to tell apart from
// "this job has no tools at all", because the first has nothing to edit and the
// second is where the first ad-hoc row goes.
func TestGetWorkOrder_LegacyWorkOrderServesToolsWithoutRows(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"wo-8","status":"open",
		"tools":[{"id":"mt-3","name":"Multimeter","quantity":1,"location_hint":"","is_required":true,"notes":""}]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "wo-8")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if len(wo.Tools) != 1 {
		t.Fatalf("tools = %+v", wo.Tools)
	}
	if len(wo.ToolRows) != 0 {
		t.Errorf("tool_rows = %+v, want empty on a work order that owns no rows", wo.ToolRows)
	}
}

// TestAddWorkOrderTool_Contract pins the add: the nested collection URL, the
// name, and the two tri-state fields. A quantity the operator did not supply is
// ABSENT so the serializer's own default of 1 applies; is_required is likewise
// absent rather than false, because it defaults to TRUE and an absent key and a
// false one mean opposite things.
func TestAddWorkOrderTool_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"wt-9","name":"Scissor lift key","quantity":1,"is_ad_hoc":true,"resolved_location":""}`, &cap)
	defer srv.Close()

	row, err := New(srv.URL).AddWorkOrderTool(context.Background(), "wo-7",
		WorkOrderAdHocTool{Name: "Scissor lift key"})
	if err != nil {
		t.Fatalf("AddWorkOrderTool: %v", err)
	}
	if cap.method != http.MethodPost || cap.path != "/api/inventory/work-orders/wo-7/tools/" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if cap.body["name"] != "Scissor lift key" {
		t.Errorf("name = %v", cap.body["name"])
	}
	for _, key := range []string{"quantity", "is_required", "location_hint", "notes", "inventory_item"} {
		if _, present := cap.body[key]; present {
			t.Errorf("%q must be ABSENT when nothing supplied it, so the server's default applies; body = %v", key, cap.body)
		}
	}
	if row.IDString() != "wt-9" || !row.IsAdHoc {
		t.Errorf("row = %+v", row)
	}
}

// TestAddWorkOrderTool_CarriesEverySuppliedField: a typed ZERO quantity and an
// explicit is_required=false both have to reach the server. Zero is not a real
// quantity and the serializer refuses it (min_value 1) — which is the server's
// judgement to make, so coercing it to 1 here would silently record a tool
// nobody asked for.
func TestAddWorkOrderTool_CarriesEverySuppliedField(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusCreated, `{"id":"wt-9","name":"Shim stock","quantity":3}`, &cap)
	defer srv.Close()

	zero, no := 0, false
	if _, err := New(srv.URL).AddWorkOrderTool(context.Background(), "wo-7", WorkOrderAdHocTool{
		Name:          "Shim stock",
		Quantity:      &zero,
		IsRequired:    &no,
		LocationHint:  "Bench 2",
		Notes:         "0.005in",
		InventoryItem: "inv-4",
	}); err != nil {
		t.Fatalf("AddWorkOrderTool: %v", err)
	}
	if fmt.Sprint(cap.body["quantity"]) != "0" {
		t.Errorf("a typed zero must reach the server to be refused there; quantity = %v", cap.body["quantity"])
	}
	if cap.body["is_required"] != false {
		t.Errorf("is_required = %v, want an explicit false (the default is true)", cap.body["is_required"])
	}
	if cap.body["location_hint"] != "Bench 2" || cap.body["notes"] != "0.005in" ||
		cap.body["inventory_item"] != "inv-4" {
		t.Errorf("body = %v", cap.body)
	}
}

// TestUpdateWorkOrderToolLocation_SendsABlankHint: blank is MEANINGFUL — it
// clears the per-job hint and lets the linked item's location stand in again — so
// the key must ride even when the value is empty. Omitting it would also fail
// the serializer, which declares location_hint required.
func TestUpdateWorkOrderToolLocation_SendsABlankHint(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{"id":"wt-1","name":"Torque wrench","location_hint":"","resolved_location":"Tool crib, drawer 3"}`, &cap)
	defer srv.Close()

	row, err := New(srv.URL).UpdateWorkOrderToolLocation(context.Background(), "wo-7", "wt-1", "")
	if err != nil {
		t.Fatalf("UpdateWorkOrderToolLocation: %v", err)
	}
	if cap.method != http.MethodPatch || cap.path != "/api/inventory/work-orders/wo-7/tools/wt-1/" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	hint, present := cap.body["location_hint"]
	if !present || hint != "" {
		t.Errorf("location_hint = %v (present=%v), want an explicit empty string", hint, present)
	}
	// The reply is what a surface reads afterwards, and the resolved value is
	// what it displays: clearing the hint did not leave the tool placeless.
	if row.ResolvedLocation != "Tool crib, drawer 3" {
		t.Errorf("resolved_location = %q", row.ResolvedLocation)
	}
}

func TestRemoveWorkOrderTool_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusNoContent, ``, &cap)
	defer srv.Close()

	if err := New(srv.URL).RemoveWorkOrderTool(context.Background(), "wo-7", "wt-2"); err != nil {
		t.Fatalf("RemoveWorkOrderTool: %v", err)
	}
	if cap.method != http.MethodDelete || cap.path != "/api/inventory/work-orders/wo-7/tools/wt-2/" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
}

// TestRemoveWorkOrderTool_RelaysTheTemplateRefusal: the backend's 400 on a
// template-derived row arrives as DRF's {"detail": …}, which parseError puts in
// APIError.Message. The sentence has to survive — it is the only thing that says
// why the row cannot go.
func TestRemoveWorkOrderTool_RelaysTheTemplateRefusal(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusBadRequest,
		`{"detail":"Only ad-hoc tools can be removed."}`, &cap)
	defer srv.Close()

	err := New(srv.URL).RemoveWorkOrderTool(context.Background(), "wo-7", "wt-1")
	if err == nil {
		t.Fatal("a 400 must be an error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if got := apiErr.Error(); !strings.Contains(got, "Only ad-hoc tools can be removed") {
		t.Errorf("the server's reason must reach the operator: %q", got)
	}
}

// TestCompleteWorkOrderLoto_Contract pins the safety write: the nested
// per-record URL, and is_completed present on every call because the backend
// 400s without it.
func TestCompleteWorkOrderLoto_Contract(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"lc-1","source_label":"Electrical (240V)","is_completed":true,
		"completed_by_name":"Dana Reyes","completed_at":"2026-09-11T08:15:00Z"
	}`, &cap)
	defer srv.Close()

	rec, err := New(srv.URL).CompleteWorkOrderLoto(context.Background(), "wo-7", "lc-1", true, "")
	if err != nil {
		t.Fatalf("CompleteWorkOrderLoto: %v", err)
	}
	if cap.method != http.MethodPatch ||
		cap.path != "/api/inventory/work-orders/wo-7/loto/lc-1/complete/" {
		t.Fatalf("%s %s", cap.method, cap.path)
	}
	if cap.body["is_completed"] != true {
		t.Errorf("is_completed = %v", cap.body["is_completed"])
	}
	// An empty note must be ABSENT: the backend writes `notes` only when the key
	// is present, so a helpful "" would erase a note recorded elsewhere.
	if _, present := cap.body["notes"]; present {
		t.Errorf("notes must be absent when empty; body = %v", cap.body)
	}
	if !rec.IsCompleted || rec.CompletedByName != "Dana Reyes" {
		t.Errorf("record = %+v", rec)
	}
}

// TestCompleteWorkOrderLoto_TakesARecordBack: the endpoint is a toggle, and the
// backend clears the actor and the timestamp with the flag — so an un-recorded
// row keeps no trace of having been marked, and the decode must reflect that
// rather than holding the stale name.
func TestCompleteWorkOrderLoto_TakesARecordBack(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":"lc-1","source_label":"Electrical (240V)","is_completed":false,
		"completed_by":null,"completed_by_name":null,"completed_at":null
	}`, &cap)
	defer srv.Close()

	rec, err := New(srv.URL).CompleteWorkOrderLoto(context.Background(), "wo-7", "lc-1", false, "mis-ticked")
	if err != nil {
		t.Fatalf("CompleteWorkOrderLoto: %v", err)
	}
	if cap.body["is_completed"] != false {
		t.Errorf("is_completed = %v", cap.body["is_completed"])
	}
	if cap.body["notes"] != "mis-ticked" {
		t.Errorf("a supplied note must ride: body = %v", cap.body)
	}
	if rec.IsCompleted || rec.CompletedAt != nil || rec.CompletedByName != "" {
		t.Errorf("record = %+v", rec)
	}
}

// TestCompleteWorkOrderLoto_RelaysTheServersRefusal: the two refusals this
// endpoint can answer with — a record that is not on this work order (404) and a
// missing is_completed (400) — are DRF {"detail": …} bodies written by hand in
// the view. Nothing on this side pre-empts either, so the sentence is all the
// operator gets and it must arrive intact.
func TestCompleteWorkOrderLoto_RelaysTheServersRefusal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not on this work order", http.StatusNotFound,
			`{"detail":"LOTO completion record not found."}`, "LOTO completion record not found"},
		{"missing flag", http.StatusBadRequest,
			`{"detail":"is_completed is required."}`, "is_completed is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cap capture
			srv := captureServer(t, tc.status, tc.body, &cap)
			defer srv.Close()

			_, err := New(srv.URL).CompleteWorkOrderLoto(context.Background(), "wo-7", "lc-9", true, "")
			if err == nil {
				t.Fatal("want an error")
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Status != tc.status {
				t.Fatalf("err = %v (%T)", err, err)
			}
			if !strings.Contains(apiErr.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to carry %q", apiErr.Error(), tc.want)
			}
		})
	}
}

// TestCompleteWorkOrderLoto_NothingRefusesAnOutOfOrderStep is the NEGATIVE half
// of the safety contract, and it is here because it is the rule an
// implementation is most tempted to invent: complete_loto has no ordering gate
// and no already-complete gate, so a client that refused either would be
// enforcing a rule the server does not have. Every one of these writes is
// accepted, in this order, and the client sends all of them.
func TestCompleteWorkOrderLoto_NothingRefusesAnOutOfOrderStep(t *testing.T) {
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = append(sent, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","is_completed":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL)
	// The LAST source first, then the first, then the last AGAIN.
	for _, id := range []string{"lc-3", "lc-1", "lc-3"} {
		if _, err := c.CompleteWorkOrderLoto(context.Background(), "wo-7", id, true, ""); err != nil {
			t.Fatalf("CompleteWorkOrderLoto(%s): %v", id, err)
		}
	}
	want := []string{
		"/api/inventory/work-orders/wo-7/loto/lc-3/complete/",
		"/api/inventory/work-orders/wo-7/loto/lc-1/complete/",
		"/api/inventory/work-orders/wo-7/loto/lc-3/complete/",
	}
	if fmt.Sprint(sent) != fmt.Sprint(want) {
		t.Errorf("sent = %v, want %v", sent, want)
	}
}
