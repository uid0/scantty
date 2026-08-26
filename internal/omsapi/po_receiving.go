// Recording what physically arrived against a purchase order (OMS
// oms-po-receiving, backend branch fm/oms-po-receiving-workflow —
// docs/PO_RECEIVING_API.md is the specification and is authoritative over this
// file).
//
// Four endpoints, and the split between them is the design:
//
//	GET  /api/reorders/purchase-orders/{id}/receiving/
//	     The WORKSHEET. A pure read, derived from the order on every request —
//	     there is no stored worksheet to invalidate, and a receipt another
//	     client recorded shows up on the next fetch. It answers, in one round
//	     trip, everything a receive screen has to know before it draws
//	     anything: may this order be received against and if not why; which
//	     lines are outstanding and which are settled; what a scanner will read
//	     off each line's goods; and which identities each line's serials may be
//	     written against.
//
//	POST /api/reorders/purchase-orders/{id}/receive/
//	     The write. Quantities, the tracking barcode, the serials (with their
//	     optional lot and expiry) and any close-short, in ONE transaction: a
//	     refused receipt writes nothing at all.
//
//	POST /api/reorders/purchase-orders/{id}/close-short/
//	     Write off the outstanding balance on named lines as never arriving.
//	     This is how a SHORT receipt ends.
//
//	POST /api/reorders/purchase-orders/{id}/mark-received/
//	     Finish the order off: close every still-outstanding line short.
//
// # Every one of them is an authenticated read or write
//
// Including the WORKSHEET, which is a GET. It was served under
// IsAuthenticatedOrReadOnly — a class that lets a read through with no
// credentials at all — and is being gated to IsAuthenticated, so a fetch that
// used to answer whatever the session's state was can now come back 401. The
// client already sends the bearer token on every request and refreshes once on
// a 401, so nothing here assumed an anonymous read; what the gate changes is
// that the FAILURE is reachable on the worksheet as well as on the writes, and
// DRF renders it as `{"detail": ...}` rather than as either shape below.
// internal/tui/receive_form.go's receiveReason is where that becomes a sentence
// an operator can act on.
//
// # The received transition is the server's, and it is conditional
//
// Closing the last outstanding balance does not, on its own, mean the order
// becomes `received`: an order nothing was ever received against does not
// advance. That rule lives on the server and has already changed once, so
// NOTHING here predicts it. Every one of these calls answers with the updated
// purchase order — read `Status` / `StatusLabel` off that reply and report what
// it says. A client that told the operator what the order would become would be
// keeping a copy of a rule it does not own, which is the one thing this package
// is written not to do.
//
// # What this package deliberately does NOT do
//
// Mismatch flagging, partial-receipt state, the received transition and serial
// validation are the SERVER's, and nothing here re-derives any of them. A
// second opinion computed client-side is a second opinion that can disagree,
// and on this flow a disagreement is a stock figure nobody can reconcile. So:
//
//   - `ReceiptState` arrives already decided (`not_received`,
//     `partially_received`, `received`, `over_received`, `closed_short`,
//     `voided`) together with the label to print. A client asking
//     "is quantity_received < quantity_ordered?" would get `closed_short`
//     wrong, which is the one state that means somebody has DECIDED.
//
//   - `IsSettled` is not `IsFullyReceived`. A line closed two units short is
//     settled and not fully received, and both facts stay on the record —
//     `IsSettled` is what decides whether the line still blocks the order.
//
//   - `SerialTargets` is the answer to "which identities may this line's
//     serials name". It is NEVER inferred from the line item's own
//     `is_serialized`, which on a KIT line describes the kit — a serial
//     against the kit's own id names a unit that can never be drawn down, and
//     the server refuses it by name.
//
// # An over-receipt is accepted, never rounded
//
// `QuantityReceived` may exceed the outstanding quantity. The server records
// the figure sent, flags the line `over_received` with a positive
// `QuantityVariance`, and credits the stock that actually arrived. A client
// must tell the operator before sending — a typo is cheaper to fix than a
// vendor query — and must send the real figure once they confirm it. Rounding
// down to the ordered quantity here would destroy the only record of the
// discrepancy.
//
// # Fewer serials than units is allowed, and the gap is reported
//
// Goods that physically arrived must be recordable even when not every label
// has been scanned, so a short serial list is accepted. What is NOT silent is
// the consequence: `SerialsOutstanding` counts units of a serialized identity
// sitting in stock with no serial on file, on every line, on the order, and on
// every worksheet line (broken down per identity by `SerialGap`). It is
// non-zero exactly when there is real outstanding work, and a client should
// surface it. It is not an error.
//
// Serialized items were once forbidden as kit components on the grounds that
// receiving a kit would credit stock without recording serials. That ban was
// lifted deliberately: the gap was never unique to kits (`mark-delivered` has
// always done it to an ordinary serialized line), the prohibition covered one
// path, and `SerialsOutstanding` covers all of them. Receiving a kit WITH
// serial capture is a live path — the serials go to the COMPONENTS.
//
// # The refusal body is NOT the standard envelope
//
// Every refusal on these four endpoints is a hand-built `{"error": "<prose>"}`
// written straight into a DRF `Response`, so it never reaches OMS's exception
// handler and never arrives in the `{"error": {"code", "message"}}` shape
// parseError understands. parseError falls through and puts the ENTIRE raw body
// into APIError.Message, so without help an operator reads
// `oms: http 400: {"error": "Line item 12 was closed short; reopen it…"}` — a
// raw dump on the step where losing the reason costs the delivery.
// AsReceivingRefusal recovers the sentence; see its own comment for why it is
// narrower than it looks.
package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Line receipt states, as `receipt_state` carries them. They are DERIVED
// server-side from the quantities and the close-short stamp and are never
// stored, so they cannot drift from them; they are named here so a screen can
// branch without matching on the prose label, which is display text.
const (
	ReceiptStateNotReceived  = "not_received"
	ReceiptStatePartially    = "partially_received"
	ReceiptStateReceived     = "received"
	ReceiptStateOverReceived = "over_received"
	ReceiptStateClosedShort  = "closed_short"
	ReceiptStateVoided       = "voided"
)

// Scan-code kinds, as `scan_codes[].kind` carries them: our own SKU, the
// barcode on the outer box, the barcode on a single unit, and the vendor's
// number as it appears on a vendor-applied label.
const (
	ScanCodeItemSKU     = "item_sku"
	ScanCodePackageUPC  = "package_upc"
	ScanCodeUnitUPC     = "unit_upc"
	ScanCodeSupplierSKU = "supplier_sku"
)

// ScanCode is one identifier a scanner could plausibly read off a line's goods.
//
// Blank identifiers are OMITTED by the server rather than emitted as "", which
// matters to a client matching a scan: an empty code in the list would match a
// stray empty scan against every unbarcoded line on the order. An EMPTY
// ScanCodes slice is a real answer — "this line cannot be scanned to", which is
// what an asset or freeform line always says — and is not the same fact as "no
// match found".
type ScanCode struct {
	Code string `json:"code"`
	Kind string `json:"kind"`
}

// SerialTarget is one inventory identity a receipt on a line may record serials
// against, and how many units of it the FULL ORDERED quantity implies.
//
// On an ordinary line that is the line's own item, when it is serialized. On a
// KIT line it is each serialized COMPONENT, at quantity_per_kit × ordered — the
// kit itself never appears, because a kit is bought as one SKU and stocked as
// its parts, so its own stock is permanently zero.
//
// SerialTrackingMode reflects the item AS IT IS TODAY rather than as it was
// when the order was placed: the kit snapshot freezes what a receipt CREDITS
// (which components, how many), but whether the system tracks a component
// serially is a live property, so a component that became serialized after the
// order was placed is still offered.
//
// Recorded is how many of this identity's units already carry a serial. It is
// present on the WORKSHEET's copy of this shape and absent from the one
// embedded in a purchase-order line, where it decodes as 0.
type SerialTarget struct {
	Item               string `json:"item"`
	ItemName           string `json:"item_name,omitempty"`
	ItemSKU            string `json:"item_sku,omitempty"`
	SerialTrackingMode string `json:"serial_tracking_mode,omitempty"`
	Quantity           int    `json:"quantity"`
	Recorded           int    `json:"recorded,omitempty"`
}

// Units is how many units of this identity a receipt of `received` against a
// line ordered `ordered` credits — the server's own scaling rule
// (round(target.quantity / quantity_ordered × quantity_received)), applied
// here so a capture screen offers exactly the slots the receipt will accept.
//
// This is NOT a second opinion about anything the server decides: it is the
// arithmetic the contract publishes precisely so a client can size its capture
// list without a round trip per keystroke, and the receipt still validates
// every serial it is sent. Over-supplying is a 400 naming the count, so a
// client that got this wrong would be TOLD rather than silently truncated.
//
// The zero cases are answered rather than divided by: a line with nothing
// ordered credits nothing, and a receipt of nothing credits nothing.
func (t SerialTarget) Units(ordered, received int) int {
	if ordered <= 0 || received <= 0 || t.Quantity <= 0 {
		return 0
	}
	// An OVER-receipt is the same expression and not a special case: it credits
	// the components of every kit that turned up rather than only the ordered
	// ones, so the ratio simply runs past 1.0 and the capture list grows with
	// it. A branch clamping it at the ordered quantity would offer no slot for
	// the serials on goods that are already on the shelf.
	return roundHalfUp(float64(t.Quantity) * float64(received) / float64(ordered))
}

// roundHalfUp matches Python's round-half-EVEN only where it cannot matter.
//
// The server uses `round()`, which in Python 3 is banker's rounding, and this
// is half-UP. They differ only on an exact .5, which needs
// quantity_per_kit × received / ordered to land precisely on a half — and when
// they do differ the consequence is bounded and safe in the direction that
// matters: this offers ONE MORE capture slot than the receipt would credit, so
// the operator is asked for a serial the receipt refuses with a message naming
// the count, rather than being silently prevented from recording one that was
// wanted. Matching banker's rounding exactly would trade a visible refusal for
// an invisible omission.
func roundHalfUp(f float64) int {
	n := int(f)
	if f-float64(n) >= 0.5 {
		return n + 1
	}
	return n
}

// SerialGapRow is one identity's share of a line's outstanding serials.
//
// Expected is derived from the quantity RECEIVED, not ordered, so this is real
// outstanding work rather than a restatement of the order.
type SerialGapRow struct {
	Item        string `json:"item"`
	ItemName    string `json:"item_name,omitempty"`
	Expected    int    `json:"expected"`
	Recorded    int    `json:"recorded"`
	Outstanding int    `json:"outstanding"`
}

// ReceivingLine is one line of the receiving worksheet.
//
// Every line on the order is reported, settled or not, each carrying its own
// ReceiptState — so "which lines am I still waiting on?" is answered without a
// client deciding it. Item is null on an asset or freeform line and decodes as
// "".
type ReceivingLine struct {
	PurchaseOrderItem any    `json:"purchase_order_item"`
	Label             string `json:"label"`
	Item              string `json:"item"`
	ItemType          string `json:"item_type"`
	QuantityOrdered   int    `json:"quantity_ordered"`
	QuantityReceived  int    `json:"quantity_received"`
	QuantityPending   int    `json:"quantity_pending"`
	// QuantityVariance is the SIGNED difference between what arrived and what
	// was ordered — negative short, positive over. It is the honest figure
	// QuantityPending deliberately floors away, and it is what a vendor query
	// is built on, so it is never recomputed from the other two here.
	QuantityVariance  int    `json:"quantity_variance"`
	ReceiptState      string `json:"receipt_state"`
	ReceiptStateLabel string `json:"receipt_state_label"`
	IsSettled         bool   `json:"is_settled"`
	IsVoided          bool   `json:"is_voided"`
	IsClosedShort     bool   `json:"is_closed_short"`
	ClosedShortReason string `json:"closed_short_reason"`
	IsKitLine         bool   `json:"is_kit_line"`
	// ScanCodes is every identifier a scanner could read off this line's goods,
	// so a scan resolves locally without a round trip. See ScanCode for why an
	// empty slice is an answer rather than an absence.
	ScanCodes          []ScanCode     `json:"scan_codes"`
	SerialTargets      []SerialTarget `json:"serial_targets"`
	SerialsRecorded    int            `json:"serials_recorded"`
	SerialGap          []SerialGapRow `json:"serial_gap"`
	SerialsOutstanding int            `json:"serials_outstanding"`
}

// ReceivingWorksheet is everything a receive screen needs about one order.
//
// CanReceive and UnavailableReason are a deliberate PAIR and a client must not
// collapse them: "you may not receive against this, and here is why" is a
// different fact from "there is nothing left to receive". An operator standing
// at the bench with a box acts differently on each — one means go and send the
// order, the other means the box is a surprise. UnavailableReason is null
// exactly when CanReceive is true, and decodes as "".
type ReceivingWorksheet struct {
	PurchaseOrder     any    `json:"purchase_order"`
	Number            string `json:"po_number"`
	Supplier          string `json:"supplier"`
	Status            string `json:"status"`
	StatusLabel       string `json:"status_label"`
	CanReceive        bool   `json:"can_receive"`
	UnavailableReason string `json:"unavailable_reason"`
	// IsSettled is "receiving is finished with every active line" and is what
	// decides whether a line still blocks the order. It is an INPUT to the
	// order's status and not a synonym for it — see the note above on the
	// received transition. IsFullyReceived is the stricter "everything we
	// ordered turned up" and stays false for ever once a line is closed short —
	// the honest answer to "did it all arrive?".
	IsSettled            bool `json:"is_settled"`
	IsFullyReceived      bool `json:"is_fully_received"`
	HasReceiptVariance   bool `json:"has_receipt_variance"`
	OutstandingLineCount int  `json:"outstanding_line_count"`
	VarianceLineCount    int  `json:"variance_line_count"`
	// SerialsOutstanding is the order-level roll-up of the per-line gap: units
	// on the shelf with no serial against them. Non-zero means stock was
	// credited with serials still missing, which is the hazard the old kit ban
	// existed to prevent and which is now reported instead of forbidden.
	SerialsOutstanding int             `json:"serials_outstanding"`
	Lines              []ReceivingLine `json:"lines"`
}

// ReceiptSerial is one serial-numbered unit captured during a receipt.
//
// Item names WHICH identity the serial belongs to. It is optional only when the
// line credits exactly one serialized identity; on a kit line crediting several
// serialized components it is REQUIRED, and the server refuses an unlabelled
// serial there by naming the choices rather than attaching it to whichever
// component sorted first. Naming the KIT is always refused.
//
// Lot and ExpirationDate are recorded verbatim against the unit. Neither
// affects stock and neither raises any alert: an expired unit still counts as
// on-hand. ExpirationDate is an ISO date (YYYY-MM-DD).
type ReceiptSerial struct {
	SerialNumber   string `json:"serial_number"`
	Item           string `json:"item,omitempty"`
	Lot            string `json:"lot,omitempty"`
	ExpirationDate string `json:"expiration_date,omitempty"`
}

// CloseShortLine names one line whose outstanding balance is being written off.
type CloseShortLine struct {
	PurchaseOrderItem any    `json:"purchase_order_item"`
	Reason            string `json:"reason,omitempty"`
}

// CloseShortRequest is the body for the close-short action.
type CloseShortRequest struct {
	Items []CloseShortLine `json:"items"`
}

// MarkReceivedRequest is the body for the mark-received action. The reason is
// recorded against EVERY line that still had an outstanding balance, so a
// shortfall written off in bulk is as traceable as one written off line by line.
type MarkReceivedRequest struct {
	Reason string `json:"reason,omitempty"`
}

// GetReceivingWorksheet fetches the receiving worksheet for one order.
//
//	GET /api/reorders/purchase-orders/{poID}/receiving/
func (c *Client) GetReceivingWorksheet(ctx context.Context, poID string) (*ReceivingWorksheet, error) {
	var out ReceivingWorksheet
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/receiving/", poID)
	if err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloseShortPOLines writes off the outstanding balance on named lines and
// returns the updated order.
//
//	POST /api/reorders/purchase-orders/{poID}/close-short/
//
// The line becomes `closed_short` and settled while QuantityReceived stays at
// what actually arrived and QuantityVariance stays negative — the shortfall is
// recorded, not erased. Refused when the line is already closed short (the
// first reason and actor are a record, not a draft), is voided, or has nothing
// outstanding.
//
// What the ORDER becomes is in the reply and is not predicted here.
func (c *Client) CloseShortPOLines(ctx context.Context, poID string, req CloseShortRequest) (*PurchaseOrder, error) {
	var out PurchaseOrder
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/close-short/", poID)
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkPurchaseOrderReceived finishes the order off: every still-outstanding
// line is closed short with `reason` recorded against it.
//
// It does NOT guarantee the order comes back `received` — an order nothing was
// ever received against does not advance, and that rule is the server's. Read
// the returned order's status rather than assuming one.
//
//	POST /api/reorders/purchase-orders/{poID}/mark-received/
//
// NOT the same as mark-delivered, and never a substitute for it. That endpoint
// asserts the opposite — that every outstanding quantity DID arrive — and
// receives and stocks it. This one stocks nothing and writes the shortfall off.
// The difference is exactly the difference between an honest record and a tidy
// one.
//
// Refused when the order has nothing outstanding, rather than silently doing
// nothing.
func (c *Client) MarkPurchaseOrderReceived(ctx context.Context, poID, reason string) (*PurchaseOrder, error) {
	var out PurchaseOrder
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/mark-received/", poID)
	if err := c.Post(ctx, path, MarkReceivedRequest{Reason: reason}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AsReceivingRefusal recovers the sentence from a receiving endpoint's
// hand-built refusal, and reports false for anything that is not one.
//
// It exists because those four endpoints write `{"error": "<prose>"}` straight
// into a Response, bypassing OMS's DRF exception handler — so parseError sees
// no `code`, falls through, and hands the whole raw body to the caller as
// APIError.Message. Without this the operator reads the JSON.
//
// It is deliberately NARROWER than AsLineEntryError, which requires a `code`
// this shape does not carry. A body is only a refusal here when it parses as an
// object whose `error` member is a non-blank JSON STRING. That rules out
// exactly the shapes that must keep arriving as they are:
//
//   - a gateway's HTML page, which does not start with `{`;
//   - the DRF envelope `{"error": {"code": …, "message": …}}`, whose `error` is
//     an OBJECT — and which parseError has already turned into a coded APIError
//     before this is ever reached;
//   - a DRF field-validation body such as `{"items": ["This field is required."]}`,
//     which carries no `error` member at all.
//
// Coercing any of those into a refusal would be inventing a sentence the server
// never said, on the screen whose whole job is to relay what it did say.
func AsReceivingRefusal(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return "", false
	}
	body := strings.TrimSpace(api.Message)
	if !strings.HasPrefix(body, "{") {
		return "", false
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Error) == 0 {
		return "", false
	}
	var prose string
	if json.Unmarshal(envelope.Error, &prose) != nil {
		return "", false
	}
	if strings.TrimSpace(prose) == "" {
		return "", false
	}
	return prose, true
}
