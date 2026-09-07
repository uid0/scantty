package omsapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Purchase-order header terms (op-bwo9): order_date, priority, payment_terms,
// freight_terms and the derived payment_schedule.
//
// Two contracts matter here. On the read side the schedule is the backend's
// arithmetic and must arrive intact — including the null due date, which is a
// real answer ("nothing to anchor to") and not a missing one. On the write side
// the blankable terms have to reach the wire as "" rather than as the null the
// association fields use to detach, because blank is a value this column stores.

const poHeaderTermsPayload = `{
	"id": 1, "po_number": "PO-2026-0042", "status": "sent",
	"priority": "urgent",
	"payment_terms": "net_30",
	"freight_terms": "fob_destination",
	"order_date": "2026-07-15T00:00:00Z",
	"expected_delivery_date": "2026-08-04",
	"estimated_total": "1234.56",
	"payment_schedule": {
		"due_date": "2026-08-14",
		"amount": "1234.56",
		"basis": "Net 30 from order date"
	}
}`

func TestPurchaseOrder_DecodesHeaderTermsAndSchedule(t *testing.T) {
	var po PurchaseOrder
	if err := json.Unmarshal([]byte(poHeaderTermsPayload), &po); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if po.Priority != "urgent" || po.PaymentTerms != "net_30" || po.FreightTerms != "fob_destination" {
		t.Errorf("terms = %q / %q / %q", po.Priority, po.PaymentTerms, po.FreightTerms)
	}
	if got := po.OrderDate.UTC().Format("2006-01-02"); got != "2026-07-15" {
		t.Errorf("order_date = %q, want 2026-07-15", got)
	}
	sched := po.PaymentSchedule
	if sched == nil {
		t.Fatal("payment_schedule did not decode")
	}
	if sched.DueDate != "2026-08-14" || string(sched.Amount) != "1234.56" ||
		sched.Basis != "Net 30 from order date" {
		t.Errorf("payment_schedule = %+v", sched)
	}
}

// TestPurchaseOrder_ScheduleWithNoDueDateDecodesAsUnscheduled: terms that anchor
// to a delivery date the order does not have yet still produce a schedule — an
// amount and the rule that produced it — with a null due date. That null has to
// read as "no date", not as a decode failure that hides the rest of the answer.
func TestPurchaseOrder_ScheduleWithNoDueDateDecodesAsUnscheduled(t *testing.T) {
	var po PurchaseOrder
	if err := json.Unmarshal([]byte(`{
		"id": 2, "status": "draft", "priority": "normal",
		"payment_terms": "cod", "freight_terms": "",
		"payment_schedule": {"due_date": null, "amount": "40.00", "basis": "On delivery"}
	}`), &po); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if po.PaymentSchedule == nil {
		t.Fatal("payment_schedule did not decode")
	}
	if po.PaymentSchedule.DueDate != "" {
		t.Errorf("null due_date = %q, want empty", po.PaymentSchedule.DueDate)
	}
	if string(po.PaymentSchedule.Amount) != "40.00" || po.PaymentSchedule.Basis != "On delivery" {
		t.Errorf("the rest of the schedule should survive a null due date: %+v", po.PaymentSchedule)
	}
	if po.FreightTerms != "" {
		t.Errorf("freight_terms = %q, want empty (nothing agreed)", po.FreightTerms)
	}
}

// TestPurchaseOrder_OrderWithoutTermsDecodesEmpty pins what an order from a
// backend older than op-bwo9 looks like: no terms and no schedule at all, which
// a reader must be able to tell apart from an order whose terms are unset.
func TestPurchaseOrder_OrderWithoutTermsDecodesEmpty(t *testing.T) {
	var po PurchaseOrder
	if err := json.Unmarshal([]byte(`{"id": 3, "status": "draft"}`), &po); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if po.Priority != "" || po.PaymentTerms != "" || po.FreightTerms != "" {
		t.Errorf("terms invented: %q / %q / %q", po.Priority, po.PaymentTerms, po.FreightTerms)
	}
	if po.PaymentSchedule != nil {
		t.Errorf("payment_schedule = %+v, want nil", po.PaymentSchedule)
	}
}

// patchPOBody runs one UpdatePurchaseOrder against a stub and returns the body
// that went out.
func patchPOBody(t *testing.T, req PurchaseOrderUpdate) map[string]any {
	t.Helper()
	body := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL).UpdatePurchaseOrder(context.Background(), "1", req); err != nil {
		t.Fatalf("UpdatePurchaseOrder: %v", err)
	}
	return body
}

// TestUpdatePurchaseOrder_OmitsUntouchedHeaderTerms: a nil field is "leave the
// column alone". The edit screen relies on this to keep an untouched order date
// (a day standing in for a timestamp) off an unrelated save.
func TestUpdatePurchaseOrder_OmitsUntouchedHeaderTerms(t *testing.T) {
	notes := "just the notes"
	body := patchPOBody(t, PurchaseOrderUpdate{Notes: &notes})

	for _, key := range []string{"order_date", "priority", "payment_terms", "freight_terms"} {
		if v, present := body[key]; present {
			t.Errorf("%s reached the wire untouched (%v): %v", key, v, body)
		}
	}
	if body["notes"] != "just the notes" {
		t.Errorf("notes = %v", body["notes"])
	}
}

func TestUpdatePurchaseOrder_SendsHeaderTerms(t *testing.T) {
	ordered, priority, payment, freight := "2026-07-15T00:00:00Z", "high", "net_60", "collect"
	body := patchPOBody(t, PurchaseOrderUpdate{
		OrderDate:    &ordered,
		Priority:     &priority,
		PaymentTerms: &payment,
		FreightTerms: &freight,
	})

	for key, want := range map[string]string{
		"order_date":    "2026-07-15T00:00:00Z",
		"priority":      "high",
		"payment_terms": "net_60",
		"freight_terms": "collect",
	} {
		if got, _ := body[key].(string); got != want {
			t.Errorf("%s = %v, want %q", key, body[key], want)
		}
	}
}

// TestUpdatePurchaseOrder_BlankTermsClearThemAsEmptyStrings: the two blankable
// terms are cleared with "" — the value the column stores for "not agreed with
// the supplier yet". Sending null here (the way work_order / owning_group
// detach) would be rejected: this is a CharField, not a nullable FK.
func TestUpdatePurchaseOrder_BlankTermsClearThemAsEmptyStrings(t *testing.T) {
	blank := ""
	body := patchPOBody(t, PurchaseOrderUpdate{PaymentTerms: &blank, FreightTerms: &blank})

	for _, key := range []string{"payment_terms", "freight_terms"} {
		v, present := body[key]
		if !present {
			t.Errorf("%s should be sent to clear it: %v", key, body)
			continue
		}
		if v != "" {
			t.Errorf("%s = %#v, want an empty string (not null)", key, v)
		}
	}
}
