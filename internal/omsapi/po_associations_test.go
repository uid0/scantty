package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Work-order / committee associations on a purchase order and its lines
// (op-shb9). Two levels, three writers: the create payload tags the whole
// order, and update_item / the PO PATCH re-tag a line or the order afterward.
// The recurring trap is "none": every one of these fields resolves to a row the
// backend rejects when it can't find it, so unset has to be an ABSENT key on
// create and an explicit null on an edit — never a zero value on the wire.

// TestPurchaseOrder_DecodesOrderLevelAssociations pins the read contract at
// order level: the bare ids plus the identity blocks that let a detail screen
// name the job and the committee without a second request.
func TestPurchaseOrder_DecodesOrderLevelAssociations(t *testing.T) {
	var po PurchaseOrder
	if err := json.Unmarshal([]byte(`{
		"id": 1, "po_number": "PO-2026-0042", "status": "draft",
		"work_order": "3f1c0e58-0000-4000-8000-000000000001",
		"work_order_details": {
			"id": "3f1c0e58-0000-4000-8000-000000000001", "short_id": "WO-1A2B",
			"display_title": "Replace drive belt", "status": "in_progress"
		},
		"owning_group": 3,
		"owning_group_details": {"id": 3, "name": "Woodshop"}
	}`), &po); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if po.WorkOrder != "3f1c0e58-0000-4000-8000-000000000001" {
		t.Errorf("work_order = %q", po.WorkOrder)
	}
	if po.WorkOrderRef == nil || po.WorkOrderRef.ShortID != "WO-1A2B" ||
		po.WorkOrderRef.DisplayTitle != "Replace drive belt" ||
		po.WorkOrderRef.Status != "in_progress" {
		t.Fatalf("work_order_details = %+v", po.WorkOrderRef)
	}
	if got := po.WorkOrderRef.Label(); got != "WO-1A2B — Replace drive belt" {
		t.Errorf("Label() = %q", got)
	}
	if po.OwningGroup == nil || *po.OwningGroup != 3 {
		t.Errorf("owning_group = %v, want 3", po.OwningGroup)
	}
	if po.OwningGroupRef == nil || po.OwningGroupRef.Name != "Woodshop" {
		t.Errorf("owning_group_details = %+v", po.OwningGroupRef)
	}
}

// TestPurchaseOrder_UnassociatedOrderDecodesEmpty: no association is the common
// case and must decode as "nothing attached", not as a phantom job or a
// committee 0 that a later edit would try to write back.
func TestPurchaseOrder_UnassociatedOrderDecodesEmpty(t *testing.T) {
	var po PurchaseOrder
	if err := json.Unmarshal([]byte(`{
		"id": 2, "status": "draft",
		"work_order": null, "work_order_details": null,
		"owning_group": null, "owning_group_details": null,
		"items": [{"id": "li-1", "work_order": null, "work_order_details": null,
		           "owning_group": null, "owning_group_details": null}]
	}`), &po); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if po.WorkOrder != "" || po.WorkOrderRef != nil || po.OwningGroup != nil || po.OwningGroupRef != nil {
		t.Errorf("order decoded an association it does not have: %q / %+v / %v / %+v",
			po.WorkOrder, po.WorkOrderRef, po.OwningGroup, po.OwningGroupRef)
	}
	li := po.Items[0]
	if li.WorkOrder != "" || li.WorkOrderRef != nil || li.OwningGroup != nil || li.OwningGroupRef != nil {
		t.Errorf("line decoded an association it does not have: %+v", li)
	}
	// Nothing attached has nothing to say, at any level.
	if got := po.WorkOrderRef.Label(); got != "" {
		t.Errorf("Label() on a nil ref = %q, want empty", got)
	}
}

// TestPurchaseOrderItem_DecodesLineLevelAssociations is the line-level twin:
// the same four fields on a PO line, since a mixed order buys parts for more
// than one job.
func TestPurchaseOrderItem_DecodesLineLevelAssociations(t *testing.T) {
	var li PurchaseOrderItem
	if err := json.Unmarshal([]byte(`{
		"id": "li-9", "description": "V-belt",
		"work_order": "3f1c0e58-0000-4000-8000-000000000002",
		"work_order_details": {
			"id": "3f1c0e58-0000-4000-8000-000000000002", "short_id": "WO-9Z8Y",
			"display_title": "Lathe PM", "status": "open"
		},
		"owning_group": 5,
		"owning_group_details": {"id": 5, "name": "Metal Shop"}
	}`), &li); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if li.WorkOrderRef.Label() != "WO-9Z8Y — Lathe PM" {
		t.Errorf("line work order = %q", li.WorkOrderRef.Label())
	}
	if li.OwningGroup == nil || *li.OwningGroup != 5 || li.OwningGroupRef.Name != "Metal Shop" {
		t.Errorf("line committee = %v / %+v", li.OwningGroup, li.OwningGroupRef)
	}
}

// TestWorkOrderRefLabel_FallsBackWhenPartiallyPopulated: the label is used in
// pickers and detail rows, so a ref missing one half still has to read as
// something an operator can act on rather than collapsing to blank.
func TestWorkOrderRefLabel_FallsBackWhenPartiallyPopulated(t *testing.T) {
	cases := []struct {
		name string
		ref  WorkOrderRef
		want string
	}{
		{"short id only", WorkOrderRef{ID: "u", ShortID: "WO-1"}, "WO-1"},
		{"title only", WorkOrderRef{ID: "u", DisplayTitle: "Lathe PM"}, "Lathe PM"},
		{"neither", WorkOrderRef{ID: "u"}, "u"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ref.Label(); got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCreatePurchaseOrder_AssociationsOmittedWhenUnset is the "none" contract at
// create: both keys must be ABSENT, because the backend resolves whatever it is
// handed and 400s on an id it can't find — an empty string or a 0 would be a
// value to reject rather than the "no association" the operator meant.
func TestCreatePurchaseOrder_AssociationsOmittedWhenUnset(t *testing.T) {
	srv, body := captureCreatePO(t)

	if _, err := New(srv.URL).CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Supplier: 7,
		Items:    []PurchaseOrderCreateItem{{Description: "Bolts", Quantity: 2}},
	}); err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	for _, key := range []string{"work_order", "owning_group"} {
		if v, present := (*body)[key]; present {
			t.Errorf("%s should be omitted when unset, got %v", key, v)
		}
	}
}

// TestCreatePurchaseOrder_AssociationsSentWhenPicked: the work order rides as a
// bare UUID string and the committee as a bare int pk — the shapes the create
// serializer's PrimaryKeyRelatedFields expect.
func TestCreatePurchaseOrder_AssociationsSentWhenPicked(t *testing.T) {
	srv, body := captureCreatePO(t)

	committee := 3
	if _, err := New(srv.URL).CreatePurchaseOrder(context.Background(), PurchaseOrderCreate{
		Supplier:    7,
		WorkOrder:   "3f1c0e58-0000-4000-8000-000000000001",
		OwningGroup: &committee,
		Items:       []PurchaseOrderCreateItem{{Description: "Bolts", Quantity: 2}},
	}); err != nil {
		t.Fatalf("CreatePurchaseOrder: %v", err)
	}
	if got := (*body)["work_order"]; got != "3f1c0e58-0000-4000-8000-000000000001" {
		t.Errorf("work_order = %v (%T)", got, got)
	}
	if n, ok := (*body)["owning_group"].(float64); !ok || n != 3 {
		t.Errorf("owning_group = %v, want the bare pk 3", (*body)["owning_group"])
	}
}

// TestUpdateLineItem_AssociationsClearVsUntouched is the edit-side contract, and
// the one most likely to lose data if it slips: an edit that only re-prices a
// line must not silently detach the job it was bought for, while an operator
// who chose "none" must actually see it detached. nil omits, zero sends null.
func TestUpdateLineItem_AssociationsClearVsUntouched(t *testing.T) {
	srv, body := capturePatch(t, `{"id":"li-1"}`)
	c := New(srv.URL)

	cost := 12.5
	if _, err := c.UpdatePurchaseOrderLineItem(context.Background(), "1", "li-1", LineItemUpdate{
		LineCost: &cost,
	}); err != nil {
		t.Fatalf("UpdatePurchaseOrderLineItem: %v", err)
	}
	for _, key := range []string{"work_order", "owning_group"} {
		if _, present := (*body)[key]; present {
			t.Errorf("a cost-only edit must not touch %s: %v", key, *body)
		}
	}

	wo, group := "", 0
	if _, err := c.UpdatePurchaseOrderLineItem(context.Background(), "1", "li-1", LineItemUpdate{
		WorkOrder: &wo, OwningGroup: &group,
	}); err != nil {
		t.Fatalf("UpdatePurchaseOrderLineItem: %v", err)
	}
	for _, key := range []string{"work_order", "owning_group"} {
		v, present := (*body)[key]
		if !present {
			t.Fatalf("detaching must SEND %s: %v", key, *body)
		}
		if v != nil {
			t.Errorf("%s = %v, want null (the backend's clear signal)", key, v)
		}
	}

	wo, group = "3f1c0e58-0000-4000-8000-000000000001", 3
	if _, err := c.UpdatePurchaseOrderLineItem(context.Background(), "1", "li-1", LineItemUpdate{
		WorkOrder: &wo, OwningGroup: &group,
	}); err != nil {
		t.Fatalf("UpdatePurchaseOrderLineItem: %v", err)
	}
	if (*body)["work_order"] != wo {
		t.Errorf("work_order = %v, want %q", (*body)["work_order"], wo)
	}
	if n, ok := (*body)["owning_group"].(float64); !ok || n != 3 {
		t.Errorf("owning_group = %v, want 3", (*body)["owning_group"])
	}
}

// TestUpdatePurchaseOrder_AssociationsUseTheSameRule: the order-level PATCH is
// the same three-way switch, so re-tagging an order can't behave differently
// from re-tagging one of its lines.
func TestUpdatePurchaseOrder_AssociationsUseTheSameRule(t *testing.T) {
	srv, body := capturePatch(t, `{"id":1}`)
	c := New(srv.URL)

	notes := "reprint"
	if _, err := c.UpdatePurchaseOrder(context.Background(), "1", PurchaseOrderUpdate{
		Notes: &notes,
	}); err != nil {
		t.Fatalf("UpdatePurchaseOrder: %v", err)
	}
	for _, key := range []string{"work_order", "owning_group"} {
		if _, present := (*body)[key]; present {
			t.Errorf("a notes-only edit must not touch %s: %v", key, *body)
		}
	}

	wo, group := "", 0
	if _, err := c.UpdatePurchaseOrder(context.Background(), "1", PurchaseOrderUpdate{
		WorkOrder: &wo, OwningGroup: &group,
	}); err != nil {
		t.Fatalf("UpdatePurchaseOrder: %v", err)
	}
	if v, present := (*body)["work_order"]; !present || v != nil {
		t.Errorf("work_order = %v (present=%v), want null", v, present)
	}
	if v, present := (*body)["owning_group"]; !present || v != nil {
		t.Errorf("owning_group = %v (present=%v), want null", v, present)
	}
}

// TestListActiveWorkOrders_AsksForOpenAndInProgress pins the pair of requests
// the pickers rely on. ?status= matches one value, so both unfinished states
// take a call of their own; a completed job must never reach the picker.
func TestListActiveWorkOrders_AsksForOpenAndInProgress(t *testing.T) {
	var statuses []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status := r.URL.Query().Get("status")
		statuses = append(statuses, r.URL.Path+"?status="+status)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[
			{"id":"wo-` + status + `","short_id":"WO-` + status + `",
			 "display_title":"Job ` + status + `","status":"` + status + `"}]}`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ListActiveWorkOrders(context.Background())
	if err != nil {
		t.Fatalf("ListActiveWorkOrders: %v", err)
	}
	want := []string{
		"/api/inventory/work-orders/?status=open",
		"/api/inventory/work-orders/?status=in_progress",
	}
	for i, w := range want {
		if i >= len(statuses) || statuses[i] != w {
			t.Fatalf("requests = %v, want %v", statuses, want)
		}
	}
	if len(rows) != 2 || rows[0].Status != "open" || rows[1].Status != "in_progress" {
		t.Fatalf("rows = %+v, want the open set then the in-progress set", rows)
	}
	if rows[0].DisplayTitle != "Job open" {
		t.Errorf("display_title = %q — the only name a corrective WO has", rows[0].DisplayTitle)
	}
}

// TestListActiveWorkOrders_PartialFailureStillReturnsWhatLoaded: one status
// failing must not throw away the other. The pickers are optional decoration on
// a purchase order — half a list of jobs beats none, and the error still comes
// back so the caller can say the list is incomplete.
func TestListActiveWorkOrders_PartialFailureStillReturnsWhatLoaded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") == "in_progress" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"wo-1","short_id":"WO-1","status":"open"}]}`))
	}))
	defer srv.Close()

	rows, err := New(srv.URL).ListActiveWorkOrders(context.Background())
	if err == nil {
		t.Error("the failed half should still be reported")
	}
	if len(rows) != 1 || rows[0].ShortID != "WO-1" {
		t.Fatalf("rows = %+v, want the open set that did load", rows)
	}
}

// capturePatch serves a PATCH endpoint and hands back the body it was sent, so
// a test can assert exactly which keys an edit put on the wire. The map is
// replaced per request, so successive calls each see only their own body.
func capturePatch(t *testing.T, response string) (*httptest.Server, *map[string]any) {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		for k := range body {
			delete(body, k)
		}
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}
