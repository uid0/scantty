package tui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/uid0/scantty/internal/omsapi"
)

// The receiving fixtures, in ONE place, because three test files drive this
// screen and a worksheet built three ways is three chances for a test to pass
// against a payload OMS never sends.
//
// Every fixture is shaped like the real reply: the worksheet carries the
// receiving state, the scan codes and the serial targets, and the purchase
// order carries the kit snapshot — which is the split the endpoint really has
// (internal/omsapi/po_receiving.go).

// receiveWSLine builds one worksheet line with the derived fields already
// filled in the way the server fills them, so a test never has to state a
// receipt_state that disagrees with the quantities beside it.
func receiveWSLine(id int, label string, ordered, received int) omsapi.ReceivingLine {
	l := omsapi.ReceivingLine{
		PurchaseOrderItem: id,
		Label:             label,
		Item:              fmt.Sprintf("itm-%d", id),
		ItemType:          "inventory_item",
		QuantityOrdered:   ordered,
		QuantityReceived:  received,
		QuantityVariance:  received - ordered,
		ScanCodes: []omsapi.ScanCode{
			{Code: fmt.Sprintf("SKU-%d", id), Kind: omsapi.ScanCodeItemSKU},
		},
	}
	if pending := ordered - received; pending > 0 {
		l.QuantityPending = pending
	}
	switch {
	case received == 0:
		l.ReceiptState, l.ReceiptStateLabel = omsapi.ReceiptStateNotReceived, "Not received"
	case received < ordered:
		l.ReceiptState, l.ReceiptStateLabel = omsapi.ReceiptStatePartially, "Partially received"
	case received == ordered:
		l.ReceiptState, l.ReceiptStateLabel, l.IsSettled = omsapi.ReceiptStateReceived, "Received", true
	default:
		l.ReceiptState, l.ReceiptStateLabel, l.IsSettled = omsapi.ReceiptStateOverReceived, "Over received", true
	}
	return l
}

// receiveWSSerialized is receiveWSLine plus one serialized identity of its own —
// an ORDINARY serialized line, where the serials go to the line's own item.
func receiveWSSerialized(id int, label string, ordered, received int) omsapi.ReceivingLine {
	l := receiveWSLine(id, label, ordered, received)
	l.SerialTargets = []omsapi.SerialTarget{{
		Item: l.Item, ItemName: label, ItemSKU: fmt.Sprintf("SKU-%d", id),
		SerialTrackingMode: "unique", Quantity: ordered,
	}}
	return l
}

// receiveWSKit is the line the kit rules are about: a KIT whose serialized
// COMPONENTS are what a serial may be written against.
//
// The kit's own id is deliberately present as `Item` and deliberately ABSENT
// from SerialTargets, which is exactly how the server renders it — so a screen
// that reached for the line's own item instead of the targets would produce a
// serial against the kit, and a test built this way can see it.
func receiveWSKit(id int, label string, ordered, received int) omsapi.ReceivingLine {
	l := receiveWSLine(id, label, ordered, received)
	l.IsKitLine = true
	l.Item = receiveKitItemID
	l.ScanCodes = []omsapi.ScanCode{
		{Code: fmt.Sprintf("KIT-%d", id), Kind: omsapi.ScanCodePackageUPC},
	}
	l.SerialTargets = []omsapi.SerialTarget{
		{Item: "itm-c", ItemName: "Cyan ink cartridge, high yield, wide-format",
			ItemSKU: "CI-100-XL", SerialTrackingMode: "unique", Quantity: ordered},
		{Item: "itm-k", ItemName: "Black ink", ItemSKU: "KI-100",
			SerialTrackingMode: "unique", Quantity: 3 * ordered},
	}
	return l
}

// receiveKitItemID is the kit's OWN inventory id — the one no serial may ever
// name. Named rather than inlined so every assertion about the corruption path
// is asserting against the same string the fixture puts on the wire.
const receiveKitItemID = "kit-1"

// receiveWorksheet wraps lines in the order-level roll-up, deriving the counts
// the way the server derives them.
func receiveWorksheet(lines ...omsapi.ReceivingLine) *omsapi.ReceivingWorksheet {
	w := &omsapi.ReceivingWorksheet{
		PurchaseOrder: 5, Number: "PO-1001", Supplier: "Acme Supply",
		Status: "sent", StatusLabel: "Sent", CanReceive: true,
		Lines: lines,
	}
	for _, l := range lines {
		if !l.IsSettled && !l.IsVoided {
			w.OutstandingLineCount++
		}
		if l.IsSettled && l.QuantityVariance != 0 && !l.IsVoided {
			w.VarianceLineCount++
			w.HasReceiptVariance = true
		}
		w.SerialsOutstanding += l.SerialsOutstanding
	}
	w.IsSettled = w.OutstandingLineCount == 0
	return w
}

// receivePO is the order behind a worksheet: the ids have to line up, and the
// KIT line carries the component snapshot the worksheet does not repeat.
func receivePO(lines ...omsapi.ReceivingLine) *omsapi.PurchaseOrder {
	po := &omsapi.PurchaseOrder{ID: 5, Number: "PO-1001"}
	for _, l := range lines {
		item := omsapi.PurchaseOrderItem{
			ID: l.PurchaseOrderItem, Description: l.Label,
			QuantityOrdered: l.QuantityOrdered, QuantityReceived: l.QuantityReceived,
			QuantityPending: l.QuantityPending, IsKitLine: l.IsKitLine,
		}
		if l.IsKitLine {
			item.KitComponents = []omsapi.POKitComponent{
				{Component: "itm-c", ComponentName: "Cyan ink cartridge, high yield, wide-format",
					ComponentSKU: "CI-100-XL", QuantityPerKit: 1, Quantity: l.QuantityOrdered},
				{Component: "itm-k", ComponentName: "Black ink", ComponentSKU: "KI-100",
					QuantityPerKit: 3, Quantity: 3 * l.QuantityOrdered},
			}
		}
		po.Items = append(po.Items, item)
	}
	return po
}

// receiveFake is the OMS these drives run against.
//
// It RECORDS what it was sent rather than only counting: a receipt's serials
// are the whole of the kit rule, so a test has to be able to read the identity
// each one named off the wire and not off the screen that built it.
type receiveFake struct {
	mu sync.Mutex

	sheet *omsapi.ReceivingWorksheet
	// sheetFail is the status the WORKSHEET fetch answers with, 0 = succeed.
	sheetFail int
	sheetBody string

	receipts []omsapi.ReceiveRequest
	closes   []omsapi.CloseShortRequest
	// reopens is every reopen-short body the fake was handed, recorded rather
	// than counted: what a test has to be able to read off the wire is WHICH
	// line was named and with what reason, not that something was posted.
	reopens   []omsapi.ReopenShortRequest
	markedAs  []string
	failWith  int    // HTTP status for the WRITE endpoints, 0 = succeed
	failBody  string // the body they fail with
	replyWith map[string]any
}

func (f *receiveFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/receiving/"):
			if f.sheetFail != 0 {
				w.WriteHeader(f.sheetFail)
				_, _ = w.Write([]byte(f.sheetBody))
				return
			}
			_ = json.NewEncoder(w).Encode(f.sheet)
		case strings.HasSuffix(r.URL.Path, "/receive/"):
			var req omsapi.ReceiveRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.receipts = append(f.receipts, req)
			f.answer(w)
		case strings.HasSuffix(r.URL.Path, "/close-short/"):
			var req omsapi.CloseShortRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.closes = append(f.closes, req)
			f.answer(w)
		case strings.HasSuffix(r.URL.Path, "/reopen-short/"):
			var req omsapi.ReopenShortRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.reopens = append(f.reopens, req)
			// The worksheet the NEXT fetch serves reflects the correction, so a
			// drive can walk the whole round trip the operator does: the line
			// leaves the settled list and comes back outstanding, with the
			// close-short's own stamps intact beside was_reopened. A fake that
			// went on serving the closed-short line would make the reload read
			// as a reopen that did nothing.
			f.applyReopen(req)
			f.answer(w)
		case strings.HasSuffix(r.URL.Path, "/mark-received/"):
			var req omsapi.MarkReceivedRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.markedAs = append(f.markedAs, req.Reason)
			f.answer(w)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error": "the fake serves no such endpoint"}`))
		}
	}
}

// applyReopen is the server's own correction, applied to the fake's worksheet:
// the close-short stamps STAY and is_closed_short goes false beside them, which
// is exactly what OMS derives from the two stamps together.
func (f *receiveFake) applyReopen(req omsapi.ReopenShortRequest) {
	if f.sheet == nil || f.failWith != 0 {
		return
	}
	for _, item := range req.Items {
		for i := range f.sheet.Lines {
			l := &f.sheet.Lines[i]
			if fmt.Sprint(l.PurchaseOrderItem) != fmt.Sprint(item.PurchaseOrderItem) {
				continue
			}
			l.IsClosedShort, l.IsSettled = false, false
			l.WasReopened, l.ReopenedReason = true, item.Reason
			l.QuantityPending = l.QuantityOrdered - l.QuantityReceived
			l.ReceiptState, l.ReceiptStateLabel = omsapi.ReceiptStateNotReceived, "Not Received"
			if l.QuantityReceived > 0 {
				l.ReceiptState, l.ReceiptStateLabel = omsapi.ReceiptStatePartially, "Partially Received"
			}
			f.sheet.OutstandingLineCount++
		}
	}
}

func (f *receiveFake) answer(w http.ResponseWriter) {
	if f.failWith != 0 {
		w.WriteHeader(f.failWith)
		_, _ = w.Write([]byte(f.failBody))
		return
	}
	body := f.replyWith
	if body == nil {
		body = map[string]any{
			"id": 5, "po_number": "PO-1001", "status": "partially_received",
			"status_label": "Partially Received", "total_received_quantity": 2,
			"total_quantity": 9, "outstanding_line_count": 1,
		}
	}
	_ = json.NewEncoder(w).Encode(body)
}

// sent is every receipt the fake was handed, copied under the lock.
func (f *receiveFake) sent() []omsapi.ReceiveRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]omsapi.ReceiveRequest(nil), f.receipts...)
}

func (f *receiveFake) closedShort() []omsapi.CloseShortRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]omsapi.CloseShortRequest(nil), f.closes...)
}

func (f *receiveFake) reopened() []omsapi.ReopenShortRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]omsapi.ReopenShortRequest(nil), f.reopens...)
}

func (f *receiveFake) marked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.markedAs...)
}
