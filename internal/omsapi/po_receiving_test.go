package omsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The receiving contract, checked against the shapes OMS really sends
// (docs/PO_RECEIVING_API.md on that side is the specification).
//
// What is asserted here is the half a screen cannot check for itself: that a
// null decodes to the absence it means rather than to a value, that a refusal
// arrives as the server's own sentence rather than as a raw body, and that what
// the operator typed reaches the wire in the field the contract names.

// receivingWorksheetBody is a worksheet as the endpoint renders one.
//
// Every awkward shape the contract names is in it, because a fixture that only
// carries the easy ones is a fixture that agrees with any decoder:
//   - `unavailable_reason` is null when the order CAN be received;
//   - `item` is null on an asset or freeform line;
//   - the order and its lines are INTEGER ids while an item is a UUID string;
//   - line 301 was closed short in error and TAKEN BACK, so `is_closed_short`
//     is false again (it is derived from both stamps) while the close-short's
//     own reason stays on the record beside the correction.
const receivingWorksheetBody = `{
  "purchase_order": 412, "po_number": "PO-2026-0500", "supplier": "Grainger",
  "status": "partially_received", "status_label": "Partially Received",
  "can_receive": true, "unavailable_reason": null,
  "is_settled": false, "is_fully_received": false,
  "has_receipt_variance": true, "outstanding_line_count": 1,
  "variance_line_count": 1, "serials_outstanding": 2,
  "lines": [
    {"purchase_order_item": 301, "label": "Stocked Bolt", "item": "a71f",
     "item_type": "inventory_item",
     "quantity_ordered": 10, "quantity_received": 3, "quantity_pending": 7,
     "quantity_variance": -7, "receipt_state": "partially_received",
     "receipt_state_label": "Partially received", "is_settled": false,
     "is_voided": false,
     "is_closed_short": false, "closed_short_reason": "backorder cancelled",
     "was_reopened": true, "reopened_reason": "closed the wrong line",
     "is_kit_line": false,
     "scan_codes": [{"code": "BOLT-1", "kind": "item_sku"},
                    {"code": "0123456789012", "kind": "package_upc"}],
     "serial_targets": [], "serials_recorded": 0,
     "serial_gap": [], "serials_outstanding": 0},
    {"purchase_order_item": 302, "label": "Custom bracket", "item": null,
     "item_type": "freeform",
     "quantity_ordered": 1, "quantity_received": 0, "quantity_pending": 1,
     "quantity_variance": -1, "receipt_state": "not_received",
     "receipt_state_label": "Not received", "is_settled": false,
     "is_voided": false, "is_closed_short": false, "closed_short_reason": "",
     "is_kit_line": false, "scan_codes": [],
     "serial_targets": [], "serials_recorded": 0,
     "serial_gap": [], "serials_outstanding": 0},
    {"purchase_order_item": 303, "label": "Printer kit", "item": "kit-1",
     "item_type": "inventory_item",
     "quantity_ordered": 2, "quantity_received": 2, "quantity_pending": 0,
     "quantity_variance": 0, "receipt_state": "received",
     "receipt_state_label": "Received", "is_settled": true,
     "is_voided": false, "is_closed_short": false, "closed_short_reason": "",
     "is_kit_line": true,
     "scan_codes": [{"code": "KIT-9", "kind": "package_upc"}],
     "serial_targets": [
       {"item": "c9a2", "item_name": "Meter", "item_sku": "M-1",
        "serial_tracking_mode": "reusable", "quantity": 2, "recorded": 1}],
     "serials_recorded": 1,
     "serial_gap": [{"item": "c9a2", "item_name": "Meter",
                     "expected": 2, "recorded": 1, "outstanding": 1}],
     "serials_outstanding": 1}
  ]
}`

func receivingServer(t *testing.T, status int, body string, seen *[]*http.Request, bodies *[]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, r)
		}
		if bodies != nil {
			raw, _ := io.ReadAll(r.Body)
			*bodies = append(*bodies, string(raw))
		}
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// TestGetReceivingWorksheet_ANullIsAnAbsence.
//
// Two nulls in this payload mean two different absences and both have to decode
// as one: `unavailable_reason` is null exactly when the order CAN be received,
// and `item` is null on a line with no inventory item behind it. A client that
// decoded either into something non-empty would draw a refusal reason on an
// order that has none, or offer a scan target that is not one.
func TestGetReceivingWorksheet_ANullIsAnAbsence(t *testing.T) {
	var seen []*http.Request
	c := receivingServer(t, 0, receivingWorksheetBody, &seen, nil)

	w, err := c.GetReceivingWorksheet(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetReceivingWorksheet: %v", err)
	}
	if len(seen) != 1 || seen[0].URL.Path != "/api/reorders/purchase-orders/1/receiving/" {
		t.Fatalf("the worksheet was fetched from %v", seen)
	}
	if !w.CanReceive || w.UnavailableReason != "" {
		t.Errorf("can_receive=%v with reason %q — the pair disagrees with itself",
			w.CanReceive, w.UnavailableReason)
	}
	// The two id kinds are NOT interchangeable and the merged contract says so
	// outright: a purchase order and a purchase-order line are INTEGERS, an
	// inventory item is a UUID string. Both travel as `any` here so a client
	// cannot silently coerce one into the other, and both render through
	// fmt.Sprint at the one place a URL or a comparison needs them.
	if got := fmt.Sprint(w.PurchaseOrder); got != "412" {
		t.Errorf("purchase_order rendered as %q, want the integer 412", got)
	}
	if len(w.Lines) != 3 {
		t.Fatalf("want 3 lines, got %d", len(w.Lines))
	}
	if got := fmt.Sprint(w.Lines[0].PurchaseOrderItem); got != "301" {
		t.Errorf("purchase_order_item rendered as %q, want the integer 301", got)
	}
	if w.Lines[0].Item != "a71f" {
		t.Errorf("item = %q, want the UUID string", w.Lines[0].Item)
	}
	if got := w.Lines[1].Item; got != "" {
		t.Errorf("a freeform line's null item decoded as %q", got)
	}
	// An EMPTY scan_codes list is a real answer — "this line cannot be scanned
	// to" — and is not the same fact as a line whose codes we failed to read.
	if len(w.Lines[1].ScanCodes) != 0 {
		t.Errorf("a freeform line came back with scan codes: %+v", w.Lines[1].ScanCodes)
	}
	if len(w.Lines[0].ScanCodes) != 2 || w.Lines[0].ScanCodes[1].Kind != ScanCodePackageUPC {
		t.Errorf("the scan codes did not decode: %+v", w.Lines[0].ScanCodes)
	}
	// A close-short that was TAKEN BACK is reported beside what it corrects,
	// not in place of it: both stamps come back, and a client that decoded only
	// one of them would show a line whose history reads as a clean slate.
	l := w.Lines[0]
	if !l.WasReopened || l.ReopenedReason != "closed the wrong line" {
		t.Errorf("the reopen was dropped: was_reopened=%v reason=%q",
			l.WasReopened, l.ReopenedReason)
	}
	if l.ClosedShortReason != "backorder cancelled" {
		t.Errorf("the reopen erased the close-short it corrects: %q", l.ClosedShortReason)
	}
}

// TestGetReceivingWorksheet_TheKitLineOffersItsComponents.
//
// A kit line's `serial_targets` are the kit's serialized COMPONENTS and the
// kit's own id never appears in them — which is the whole of the identity rule,
// arriving as data. `item` on the line IS the kit, so a client that read that
// instead of the targets would write serials against a SKU that never enters
// stock.
func TestGetReceivingWorksheet_TheKitLineOffersItsComponents(t *testing.T) {
	w, err := receivingServer(t, 0, receivingWorksheetBody, nil, nil).
		GetReceivingWorksheet(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetReceivingWorksheet: %v", err)
	}
	kit := w.Lines[2]
	if !kit.IsKitLine || kit.Item != "kit-1" {
		t.Fatalf("the fixture's kit line is not one: %+v", kit)
	}
	if len(kit.SerialTargets) != 1 {
		t.Fatalf("want one serial target, got %+v", kit.SerialTargets)
	}
	if kit.SerialTargets[0].Item == kit.Item {
		t.Errorf("the kit's own id is offered as a serial target: %+v", kit.SerialTargets[0])
	}
	if kit.SerialTargets[0].Recorded != 1 || kit.SerialTargets[0].Quantity != 2 {
		t.Errorf("the target's counts did not decode: %+v", kit.SerialTargets[0])
	}
	// serials_outstanding is the figure the lifted kit ban was replaced by, and
	// it is broken down per identity so a client can say WHICH item is short.
	if kit.SerialsOutstanding != 1 || len(kit.SerialGap) != 1 ||
		kit.SerialGap[0].Outstanding != 1 {
		t.Errorf("the serial gap did not decode: outstanding=%d gap=%+v",
			kit.SerialsOutstanding, kit.SerialGap)
	}
	if w.SerialsOutstanding != 2 {
		t.Errorf("the order-level roll-up = %d, want 2", w.SerialsOutstanding)
	}
}

// TestGetReceivingWorksheet_ASettledLineIsNotAFullyReceivedOne.
//
// `is_settled` means "receiving is finished with this line" and is what decides
// whether it still blocks the order; `quantity_variance` is what survives to
// say the line did not match. They are carried separately because a line closed
// short is settled and NOT fully received, and both facts stay on the record —
// so nothing here may be re-derived from the quantities.
func TestGetReceivingWorksheet_ASettledLineIsNotAFullyReceivedOne(t *testing.T) {
	body := `{"can_receive": false,
	  "unavailable_reason": "Receiving has finished with every line on this order.",
	  "is_settled": true, "is_fully_received": false, "has_receipt_variance": true,
	  "outstanding_line_count": 0, "variance_line_count": 1,
	  "lines": [{"purchase_order_item": 9, "label": "Gasket",
	    "quantity_ordered": 10, "quantity_received": 8, "quantity_pending": 0,
	    "quantity_variance": -2, "receipt_state": "closed_short",
	    "receipt_state_label": "Closed short", "is_settled": true,
	    "is_closed_short": true, "closed_short_reason": "backorder cancelled"}]}`
	w, err := receivingServer(t, 0, body, nil, nil).
		GetReceivingWorksheet(context.Background(), "1")
	if err != nil {
		t.Fatalf("GetReceivingWorksheet: %v", err)
	}
	if w.CanReceive || w.UnavailableReason == "" {
		t.Errorf("a refused order came back with no reason: %+v", w)
	}
	if !w.IsSettled || w.IsFullyReceived {
		t.Errorf("settled=%v fully received=%v — a closed-short order is the first and "+
			"never the second", w.IsSettled, w.IsFullyReceived)
	}
	l := w.Lines[0]
	if l.ReceiptState != ReceiptStateClosedShort || !l.IsClosedShort {
		t.Errorf("the closed-short line did not decode: %+v", l)
	}
	// quantity_pending FLOORS the shortfall away; quantity_variance is what is
	// left to chase a vendor with.
	if l.QuantityPending != 0 || l.QuantityVariance != -2 {
		t.Errorf("pending=%d variance=%d — the shortfall is only in the variance",
			l.QuantityPending, l.QuantityVariance)
	}
	if l.ClosedShortReason == "" {
		t.Error("the reason recorded against the line was dropped")
	}
}

// TestSerialTargetUnits_ScalesToWhatArrived pins the one piece of arithmetic
// this package does on the server's behalf, which the contract publishes so a
// client can size a capture list without a round trip per keystroke.
func TestSerialTargetUnits_ScalesToWhatArrived(t *testing.T) {
	target := SerialTarget{Item: "itm-k", Quantity: 6} // 3 per kit × 2 ordered
	for _, tc := range []struct {
		name                    string
		ordered, received, want int
	}{
		{"the whole order", 2, 2, 6},
		{"half of it", 2, 1, 3},
		{"an over-receipt credits what turned up", 2, 3, 9},
		{"nothing received", 2, 0, 0},
		{"nothing ordered cannot be scaled", 0, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := target.Units(tc.ordered, tc.received); got != tc.want {
				t.Errorf("Units(%d, %d) = %d, want %d", tc.ordered, tc.received, got, tc.want)
			}
		})
	}
	if got := (SerialTarget{}).Units(4, 4); got != 0 {
		t.Errorf("a target with no quantity credits %d units, want 0", got)
	}
	// Rounding is half-UP where the server's round() is half-even, and the
	// direction is the safe one: one MORE slot than the receipt credits means
	// the operator is asked for a serial the receipt refuses by name, rather
	// than being silently prevented from recording one they wanted.
	if got := (SerialTarget{Quantity: 1}).Units(2, 1); got != 1 {
		t.Errorf("a half unit rounds to %d, want 1 — the direction that refuses visibly", got)
	}
}

// TestReceivePOItems_EverythingTypedReachesTheWire.
//
// The whole receipt is ONE call and one transaction, so every part of it — the
// quantity as typed, the serials with their identity, lot and expiry, and the
// tracking barcode — has to be in the body. A field silently dropped here is
// work the operator did that no record will ever show.
func TestReceivePOItems_EverythingTypedReachesTheWire(t *testing.T) {
	var bodies []string
	var seen []*http.Request
	c := receivingServer(t, 0, `{"id": 5, "po_number": "PO-1001"}`, &seen, &bodies)

	_, err := c.ReceivePOItems(context.Background(), "1", ReceiveRequest{
		Items: []ReceiptLine{{
			PurchaseOrderItem: 301,
			QuantityReceived:  12, // MORE than was ordered: sent as typed
			Serials: []ReceiptSerial{
				{SerialNumber: "SN-001", Item: "c9a2", Lot: "LOT-42", ExpirationDate: "2027-01-31"},
				{SerialNumber: "SN-002", Item: "c9a2"},
			},
		}},
		TrackingNumber: "1Z999AA10123456784",
		Carrier:        "UPS",
		DeliveryDate:   "2026-08-25",
		ReceiptNotes:   "Two boxes, one crushed corner",
	})
	if err != nil {
		t.Fatalf("ReceivePOItems: %v", err)
	}
	if len(seen) != 1 || seen[0].URL.Path != "/api/reorders/purchase-orders/1/receive/" {
		t.Fatalf("the receipt went to %v", seen)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatalf("the body is not JSON: %v", err)
	}
	for _, want := range []struct{ key, val string }{
		{"tracking_number", "1Z999AA10123456784"},
		{"carrier", "UPS"},
		{"delivery_date", "2026-08-25"},
		{"receipt_notes", "Two boxes, one crushed corner"},
	} {
		if fmt.Sprint(got[want.key]) != want.val {
			t.Errorf("%s = %v, want %q", want.key, got[want.key], want.val)
		}
	}
	items, _ := got["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", got["items"])
	}
	line, _ := items[0].(map[string]any)
	if fmt.Sprint(line["quantity_received"]) != "12" {
		t.Errorf("quantity_received = %v — an over-receipt goes as typed", line["quantity_received"])
	}
	serials, _ := line["serials"].([]any)
	if len(serials) != 2 {
		t.Fatalf("serials = %v", line["serials"])
	}
	first, _ := serials[0].(map[string]any)
	if fmt.Sprint(first["item"]) != "c9a2" {
		t.Errorf("the serial does not name its identity: %v", first)
	}
	if fmt.Sprint(first["lot"]) != "LOT-42" || fmt.Sprint(first["expiration_date"]) != "2027-01-31" {
		t.Errorf("the lot or the expiry was dropped: %v", first)
	}
	// A serial with no lot must not carry an empty one: `omitempty` is what
	// keeps "not recorded" out of a column that means "recorded as blank".
	second, _ := serials[1].(map[string]any)
	if _, ok := second["lot"]; ok {
		t.Errorf("an unset lot was sent anyway: %v", second)
	}
	// Nothing this client does not drive may appear either — an at_level of
	// false on every line would read as a deliberate "these are base units"
	// on a line whose item is not counted in packs, which the server refuses.
	if _, ok := line["at_level"]; ok {
		t.Errorf("at_level was sent on a receipt that does not use it: %v", line)
	}
}

// TestCloseShortAndMarkReceived_AddressTheirOwnEndpoints.
//
// They are different writes with different blast radii and neither is a default
// for the other: close-short names LINES, mark-received closes every
// outstanding line and advances the order. Sending one to the other's URL would
// be the difference between writing off a backorder and writing off an order.
func TestCloseShortAndMarkReceived_AddressTheirOwnEndpoints(t *testing.T) {
	var seen []*http.Request
	var bodies []string
	c := receivingServer(t, 0, `{"id": 5}`, &seen, &bodies)

	if _, err := c.CloseShortPOLines(context.Background(), "1", CloseShortRequest{
		Items: []POLineSettlement{{PurchaseOrderItem: 301, Reason: "backorder cancelled"}},
	}); err != nil {
		t.Fatalf("CloseShortPOLines: %v", err)
	}
	if _, err := c.MarkPurchaseOrderReceived(context.Background(), "1",
		"vendor closed the order"); err != nil {
		t.Fatalf("MarkPurchaseOrderReceived: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("want two writes, got %d", len(seen))
	}
	if seen[0].URL.Path != "/api/reorders/purchase-orders/1/close-short/" {
		t.Errorf("close-short went to %q", seen[0].URL.Path)
	}
	if seen[1].URL.Path != "/api/reorders/purchase-orders/1/mark-received/" {
		t.Errorf("mark-received went to %q", seen[1].URL.Path)
	}
	if !strings.Contains(bodies[0], `"backorder cancelled"`) ||
		!strings.Contains(bodies[0], `"purchase_order_item":301`) {
		t.Errorf("the close-short body lost the line or the reason: %s", bodies[0])
	}
	if !strings.Contains(bodies[1], `"vendor closed the order"`) {
		t.Errorf("the mark-received reason was dropped: %s", bodies[1])
	}
}

// TestReopenShort_AddressesItsOwnEndpointAndCarriesTheSettlementShape.
//
// The CORRECTION, and it is a third write beside the two above rather than a
// variant of either: close-short settles a line, mark-received settles an order,
// and this one takes a settlement BACK. Sending it to close-short's URL would
// write off the balance it was meant to restore.
//
// The body is the shape OMS's own LineSettlementSerializer takes — an INTEGER
// `purchase_order_item` and an optional `reason` — read off
// backend/reorder_queue/serializers.py rather than inferred from the close-short
// beside it, even though the two really are one serializer there.
func TestReopenShort_AddressesItsOwnEndpointAndCarriesTheSettlementShape(t *testing.T) {
	var seen []*http.Request
	var bodies []string
	c := receivingServer(t, 0, `{"id": 5, "status": "partially_received"}`, &seen, &bodies)

	if _, err := c.ReopenShortPOLines(context.Background(), "1", ReopenShortRequest{
		Items: []POLineSettlement{{PurchaseOrderItem: 301, Reason: "closed the wrong line"}},
	}); err != nil {
		t.Fatalf("ReopenShortPOLines: %v", err)
	}
	// The reason is OPTIONAL on the wire — `required=False, allow_blank=True` —
	// so an unset one is OMITTED rather than sent as "". A blank string is a
	// value somebody typed; an absent key lets the server apply its own default.
	if _, err := c.ReopenShortPOLines(context.Background(), "1", ReopenShortRequest{
		Items: []POLineSettlement{{PurchaseOrderItem: 302}},
	}); err != nil {
		t.Fatalf("ReopenShortPOLines with no reason: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("want two writes, got %d", len(seen))
	}
	for i, req := range seen {
		if req.URL.Path != "/api/reorders/purchase-orders/1/reopen-short/" {
			t.Errorf("reopen-short %d went to %q", i, req.URL.Path)
		}
		if req.Method != http.MethodPost {
			t.Errorf("reopen-short %d used %s", i, req.Method)
		}
	}
	if !strings.Contains(bodies[0], `"purchase_order_item":301`) ||
		!strings.Contains(bodies[0], `"closed the wrong line"`) {
		t.Errorf("the reopen body lost the line or the reason: %s", bodies[0])
	}
	if strings.Contains(bodies[1], `"reason"`) {
		t.Errorf("an unset reason was sent anyway: %s", bodies[1])
	}
}

// TestReopenShort_RecoversTheServersOwnRefusal.
//
// Its refusals are the same hand-built `{"error": "<prose>"}` the rest of this
// file's endpoints write, so they reach the caller as a raw body and
// AsReceivingRefusal is what an operator-facing screen recovers the sentence
// with. Driven here so the claim is about THIS endpoint rather than inherited.
func TestReopenShort_RecoversTheServersOwnRefusal(t *testing.T) {
	const refusal = "This line is not closed short, so there is nothing to reopen."
	c := receivingServer(t, http.StatusBadRequest, `{"error": "`+refusal+`"}`, nil, nil)

	_, err := c.ReopenShortPOLines(context.Background(), "1", ReopenShortRequest{
		Items: []POLineSettlement{{PurchaseOrderItem: 301}},
	})
	if err == nil {
		t.Fatal("a refused reopen came back as a success")
	}
	prose, ok := AsReceivingRefusal(err)
	if !ok {
		t.Fatalf("the refusal was not recognised: %v", err)
	}
	if prose != refusal {
		t.Errorf("the sentence came back as %q", prose)
	}
}

// TestAsReceivingRefusal_RecoversTheSentenceAndNothingElse.
//
// The four receiving endpoints write `{"error": "<prose>"}` by hand, so it
// never reaches OMS's DRF exception handler, parseError finds no code and hands
// the caller the WHOLE raw body. Without this the operator reads the JSON on
// the step where losing the reason costs the delivery.
//
// The negative half is the half that matters. This is narrower than
// AsLineEntryError — which requires a `code` this shape does not carry — so it
// has to refuse everything that merely looks similar, or it would be inventing
// a sentence the server never said.
func TestAsReceivingRefusal_RecoversTheSentenceAndNothingElse(t *testing.T) {
	c := receivingServer(t, http.StatusBadRequest,
		`{"error": "Line item 301 was closed short; reopen it before receiving more against it"}`,
		nil, nil)
	_, err := c.ReceivePOItems(context.Background(), "1", ReceiveRequest{})
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	prose, ok := AsReceivingRefusal(err)
	if !ok {
		t.Fatalf("the refusal did not survive: %v", err)
	}
	if !strings.HasPrefix(prose, "Line item 301 was closed short") {
		t.Errorf("prose = %q — the server's own sentence is what the operator reads", prose)
	}
	if strings.Contains(prose, `{"error"`) {
		t.Errorf("the raw body came through as the sentence: %q", prose)
	}

	for _, tc := range []struct{ name, body string }{
		{"a gateway page", "<html><head><title>502</title></head></html>"},
		{"the DRF envelope, whose error is an OBJECT",
			`{"error": {"code": "validation_error", "message": "no"}}`},
		{"a field-validation body with no error member",
			`{"items": ["This field is required."]}`},
		{"an error member that is not a string", `{"error": ["a", "b"]}`},
		{"a blank sentence", `{"error": "   "}`},
		{"a body that is not an object at all", `["nope"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := receivingServer(t, http.StatusBadGateway, tc.body, nil, nil)
			_, err := c.MarkPurchaseOrderReceived(context.Background(), "1", "")
			if err == nil {
				t.Fatal("the fake answered success")
			}
			if prose, ok := AsReceivingRefusal(err); ok {
				t.Errorf("%s was coerced into a refusal the server never made: %q",
					tc.name, prose)
			}
		})
	}
}
