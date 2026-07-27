package omsapi

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"
)

// ReorderItemDetails is the slim subset of fields the reorder-queue
// screen needs from the nested item_details payload (backend
// ReorderRequestSerializer uses the full InventoryItemSerializer, but
// we only render a handful of fields).
type ReorderItemDetails struct {
	ID                string        `json:"id,omitempty"`
	Name              string        `json:"name,omitempty"`
	SKU               string        `json:"sku,omitempty"`
	CurrentStock      int           `json:"current_stock,omitempty"`
	MinimumStock      int           `json:"minimum_stock,omitempty"`
	ReorderQuantity   int           `json:"reorder_quantity,omitempty"`
	PreferredSupplier string        `json:"preferred_supplier_name,omitempty"`
	UnitCost          DecimalString `json:"unit_cost,omitempty"`
}

type ReorderRequest struct {
	ID            any                 `json:"id"`
	Item          string              `json:"item"`
	ItemDetails   *ReorderItemDetails `json:"item_details,omitempty"`
	Quantity      int                 `json:"quantity"`
	Priority      string              `json:"priority,omitempty"`
	RequestedBy   string              `json:"requested_by,omitempty"`
	RequestNotes  string              `json:"request_notes,omitempty"`
	Status        string              `json:"status,omitempty"`
	RequestedAt   time.Time           `json:"requested_at,omitempty"`
	DaysPending   int                 `json:"days_pending,omitempty"`
	EstimatedCost DecimalString       `json:"estimated_cost,omitempty"`
	CreatedAt     time.Time           `json:"created_at,omitempty"`
	ApprovedAt    *time.Time          `json:"approved_at,omitempty"`
}

type ReorderRequestCreate struct {
	Item         string `json:"item"`
	Quantity     int    `json:"quantity"`
	Priority     string `json:"priority,omitempty"`
	RequestedBy  string `json:"requested_by,omitempty"`
	RequestNotes string `json:"request_notes,omitempty"`
}

func (c *Client) CreateReorderRequest(ctx context.Context, req ReorderRequestCreate) (*ReorderRequest, error) {
	var out ReorderRequest
	if err := c.Post(ctx, "/api/reorders/requests/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListPendingReorders(ctx context.Context, q url.Values) ([]ReorderRequest, error) {
	var out MaybeList[ReorderRequest]
	if err := c.Get(ctx, "/api/reorders/requests/pending/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// ApproveReorderRequest flips status to "approved" on a pending
// reorder. Backend endpoint: POST /api/reorders/requests/{id}/approve/.
// adminNotes is appended to admin_notes; pass "" to skip.
func (c *Client) ApproveReorderRequest(ctx context.Context, id, adminNotes string) (*ReorderRequest, error) {
	body := map[string]string{}
	if adminNotes != "" {
		body["admin_notes"] = adminNotes
	}
	var out ReorderRequest
	path := fmt.Sprintf("/api/reorders/requests/%s/approve/", id)
	if err := c.Post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CancelReorderRequest flips status to "cancelled" on a pending /
// approved reorder. Backend endpoint:
// POST /api/reorders/requests/{id}/cancel/. adminNotes carries the
// reason; pass "" if none.
func (c *Client) CancelReorderRequest(ctx context.Context, id, adminNotes string) (*ReorderRequest, error) {
	body := map[string]string{}
	if adminNotes != "" {
		body["admin_notes"] = adminNotes
	}
	var out ReorderRequest
	path := fmt.Sprintf("/api/reorders/requests/%s/cancel/", id)
	if err := c.Post(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SupplierAgreementRef is the minimal {id, name} the PO serializer nests as
// supplier_agreement_details (op-yoos) so a detail screen can name the
// agreement an order was placed under without a second request. Nil when the
// order cites no agreement.
type SupplierAgreementRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// WorkOrderRef is the minimal work-order identity the PO serializers nest next
// to a work_order association (op-shb9) — the same four fields at order level
// and line level, so both render a job without fetching it. ID is a string
// because the backend stringifies the WorkOrder UUID (str(work_order.id)).
type WorkOrderRef struct {
	ID           string `json:"id"`
	ShortID      string `json:"short_id,omitempty"`
	DisplayTitle string `json:"display_title,omitempty"`
	Status       string `json:"status,omitempty"`
}

// Label names an attached work order for display: "WO-1234 — Replace belt".
// Falls back to the short id alone when the backend sent no display title.
func (r *WorkOrderRef) Label() string {
	if r == nil {
		return ""
	}
	switch {
	case r.ShortID != "" && r.DisplayTitle != "":
		return r.ShortID + " — " + r.DisplayTitle
	case r.ShortID != "":
		return r.ShortID
	case r.DisplayTitle != "":
		return r.DisplayTitle
	}
	return r.ID
}

// OwningGroupRef is the minimal committee (SIG) identity nested next to an
// owning_group association (op-shb9). The committee is an auth.Group, and its
// name is the whole label — nothing else is needed to render the association.
type OwningGroupRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type PurchaseOrder struct {
	ID                    any                       `json:"id"`
	Number                string                    `json:"po_number,omitempty"`
	Status                string                    `json:"status"`
	StatusLabel           string                    `json:"status_label,omitempty"`
	Supplier              any                       `json:"supplier,omitempty"`
	SupplierName          string                    `json:"supplier_name,omitempty"`
	SupplierDetails       string                    `json:"supplier_details,omitempty"`
	SupplierAgreement     *int                      `json:"supplier_agreement,omitempty"`
	SupplierAgreementRef  *SupplierAgreementRef     `json:"supplier_agreement_details,omitempty"`
	WorkOrder             string                    `json:"work_order,omitempty"`
	WorkOrderRef          *WorkOrderRef             `json:"work_order_details,omitempty"`
	OwningGroup           *int                      `json:"owning_group,omitempty"`
	OwningGroupRef        *OwningGroupRef           `json:"owning_group_details,omitempty"`
	SupplierOrderNumber   string                    `json:"supplier_order_number,omitempty"`
	SalesOrderNumber      string                    `json:"sales_order_number,omitempty"`
	Total                 float64                   `json:"total_amount,omitempty"`
	EstimatedTotal        DecimalString             `json:"estimated_total,omitempty"`
	ActualTotal           DecimalString             `json:"actual_total,omitempty"`
	Currency              string                    `json:"currency,omitempty"`
	OrderDate             time.Time                 `json:"order_date,omitempty"`
	ExpectedDeliveryDate  string                    `json:"expected_delivery_date,omitempty"`
	CreatedAt             time.Time                 `json:"created_at,omitempty"`
	CreatedBy             any                       `json:"created_by,omitempty"`
	CreatedByUsername     string                    `json:"created_by_username,omitempty"`
	SentAt                *time.Time                `json:"sent_at,omitempty"`
	SentByUsername        string                    `json:"sent_by_username,omitempty"`
	VoidedAt              *time.Time                `json:"voided_at,omitempty"`
	VoidedByUsername      string                    `json:"voided_by_username,omitempty"`
	VoidReason            string                    `json:"void_reason,omitempty"`
	UpdatedAt             time.Time                 `json:"updated_at,omitempty"`
	OrderedAt             *time.Time                `json:"ordered_at,omitempty"`
	ReceivedAt            *time.Time                `json:"received_at,omitempty"`
	Notes                 string                    `json:"notes,omitempty"`
	Items                 []PurchaseOrderItem       `json:"items,omitempty"`
	Attachments           []PurchaseOrderAttachment `json:"attachments,omitempty"`
	TotalItems            int                       `json:"total_items,omitempty"`
	TotalQuantity         int                       `json:"total_quantity,omitempty"`
	TotalReceivedQuantity int                       `json:"total_received_quantity,omitempty"`
	IsFullyReceived       bool                      `json:"is_fully_received,omitempty"`
	DaysSinceOrdered      *int                      `json:"days_since_ordered,omitempty"`
}

type PurchaseOrderAttachment struct {
	ID             int       `json:"id"`
	File           string    `json:"file,omitempty"`
	FileURL        string    `json:"file_url,omitempty"`
	FileName       string    `json:"file_name,omitempty"`
	Description    string    `json:"description,omitempty"`
	UploadedBy     *int      `json:"uploaded_by,omitempty"`
	UploadedByName string    `json:"uploaded_by_name,omitempty"`
	UploadedAt     time.Time `json:"uploaded_at,omitempty"`
}

type PurchaseOrderItem struct {
	ID                   any           `json:"id"`
	PurchaseOrder        any           `json:"purchase_order,omitempty"`
	ItemSupplier         any           `json:"item_supplier,omitempty"`
	Asset                any           `json:"asset,omitempty"`
	Description          string        `json:"description,omitempty"`
	SupplierDetails      string        `json:"supplier_details,omitempty"`
	QuantityOrdered      int           `json:"quantity_ordered,omitempty"`
	QuantityReceived     int           `json:"quantity_received,omitempty"`
	QuantityPending      int           `json:"quantity_pending,omitempty"`
	UnitCostOrdered      DecimalString `json:"unit_cost_ordered,omitempty"`
	UnitCostActual       DecimalString `json:"unit_cost_actual,omitempty"`
	EstimatedCost        DecimalString `json:"estimated_cost,omitempty"`
	ActualCost           DecimalString `json:"actual_cost,omitempty"`
	IsFullyReceived      bool          `json:"is_fully_received,omitempty"`
	IsVoided             bool          `json:"is_voided,omitempty"`
	VoidedAt             *time.Time    `json:"voided_at,omitempty"`
	VoidReason           string        `json:"void_reason,omitempty"`
	Notes                string        `json:"notes,omitempty"`
	ItemType             string        `json:"item_type,omitempty"`
	ExpectedShipmentDate string        `json:"expected_shipment_date,omitempty"`
	ActualShipmentDate   string        `json:"actual_shipment_date,omitempty"`
	CreatedAt            time.Time     `json:"created_at,omitempty"`
	UpdatedAt            time.Time     `json:"updated_at,omitempty"`
	// Who this line was bought for: the job it completes (op-bu80) and the
	// committee it was ordered on behalf of (op-shb9). Both are attribution
	// only — receiving still books stock and money the way it always did — and
	// both are settable after the fact through update_item, which is usually
	// when the answer is known.
	WorkOrder      string          `json:"work_order,omitempty"`
	WorkOrderRef   *WorkOrderRef   `json:"work_order_details,omitempty"`
	OwningGroup    *int            `json:"owning_group,omitempty"`
	OwningGroupRef *OwningGroupRef `json:"owning_group_details,omitempty"`
	// Nested details (item_details / asset_details) come back as opaque
	// objects we don't need to introspect for the receive flow.
	ItemDetails  map[string]any `json:"item_details,omitempty"`
	AssetDetails map[string]any `json:"asset_details,omitempty"`
}

// DisplayLabel returns a human-friendly label for a PO line item, falling
// back through the available identifying fields.
func (p PurchaseOrderItem) DisplayLabel() string {
	if p.Description != "" {
		return p.Description
	}
	if name, ok := p.ItemDetails["name"].(string); ok && name != "" {
		return name
	}
	if name, ok := p.AssetDetails["name"].(string); ok && name != "" {
		return name
	}
	if p.SupplierDetails != "" {
		return p.SupplierDetails
	}
	return fmt.Sprintf("line %v", p.ID)
}

func (c *Client) ListPurchaseOrders(ctx context.Context, q url.Values) (*Page[PurchaseOrder], error) {
	return GetPage[PurchaseOrder](ctx, c, "/api/reorders/purchase-orders/", q)
}

func (c *Client) GetPurchaseOrder(ctx context.Context, id string) (*PurchaseOrder, error) {
	var out PurchaseOrder
	if err := c.Get(ctx, fmt.Sprintf("/api/reorders/purchase-orders/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OrderPadExport is the vendor-agnostic order pad the backend builds from a
// PO's non-voided lines (parity with OMS web #855). Csv carries a part#,qty
// CSV with a header row; Text is the tab-separated part#\tqty copy-paste block
// (no header) suitable for pasting straight into any distributor's bulk order
// pad. MissingSku names the lines whose supplier part number is blank — those
// rows are omitted from Csv/Text rather than silently dropped, so the operator
// knows exactly which lines to fix. LineCount is the number of usable rows.
type OrderPadExport struct {
	Csv        string   `json:"csv"`
	Text       string   `json:"text"`
	Filename   string   `json:"filename"`
	Supplier   string   `json:"supplier"`
	LineCount  int      `json:"line_count"`
	MissingSku []string `json:"missing_sku"`
}

// ExportOrderPad fetches the vendor-agnostic order pad for a PO via
// GET /api/reorders/purchase-orders/{poID}/export-order/ (trailing slash — a
// DRF @action). The backend read-gates it to authenticated users, matching
// send_to_supplier. The Client's method-preserving redirect policy (see
// preserveMethodOnRedirect) keeps the GET intact across an http->https upgrade.
func (c *Client) ExportOrderPad(ctx context.Context, poID string) (*OrderPadExport, error) {
	var out OrderPadExport
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/export-order/", poID)
	if err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PurchaseOrderCreateItem is one line in the PurchaseOrderCreate payload.
// The OMS create endpoint accepts three line shapes, picked by which fields
// are set: inventory (ItemSupplierID), asset (AssetID), or freeform
// (Description). The fields are pointers so omitempty omits them cleanly
// when nil — sending “item_supplier_id: 0“ or “asset_id: ""“ would
// route to the wrong branch on the backend.
type PurchaseOrderCreateItem struct {
	ItemSupplierID       *int     `json:"item_supplier_id,omitempty"`
	AssetID              *string  `json:"asset_id,omitempty"`
	Description          string   `json:"description,omitempty"`
	Quantity             int      `json:"quantity"`
	UnitCost             *float64 `json:"unit_cost,omitempty"`
	ExpectedShipmentDate string   `json:"expected_shipment_date,omitempty"`
}

// PurchaseOrderCreate mirrors backend PurchaseOrderCreateSerializer —
// supplier + line items, with optional expected_delivery_date and notes.
// po_number is server-generated, so it's not in this payload.
//
// SupplierAgreementID is the optional purchase/pricing agreement the order is
// placed under (op-yoos). It's a pointer with omitempty because the field must
// vanish from the payload when unset — the backend validates any agreement
// present against the order's supplier, so a stray 0 (or null) would be a
// caller-invented value to validate rather than the "no agreement" the
// operator meant.
//
// WorkOrder and OwningGroup are the order-level associations (op-shb9): the job
// this order was placed for and the committee it was placed on behalf of. Both
// optional, both attribution only — neither moves stock nor posts to the
// ledger. Same omit-when-unset rule as the agreement, and for the same reason:
// each is resolved to a row the backend 400s on if it can't find it, so "none"
// has to be an ABSENT key rather than a null or a zero. A UUID string is never
// legitimately empty and no auth.Group has pk 0, so omitempty says exactly that
// on both.
type PurchaseOrderCreate struct {
	Supplier             int                       `json:"supplier"`
	SupplierAgreementID  *int                      `json:"supplier_agreement,omitempty"`
	WorkOrder            string                    `json:"work_order,omitempty"`
	OwningGroup          *int                      `json:"owning_group,omitempty"`
	ExpectedDeliveryDate string                    `json:"expected_delivery_date,omitempty"`
	Notes                string                    `json:"notes,omitempty"`
	Items                []PurchaseOrderCreateItem `json:"items"`
}

// CreatePurchaseOrder posts a new PO. The backend assigns po_number
// (PO-YYYY-NNNN), creates the line-item rows, and returns the saved PO
// with the standard PurchaseOrder shape — items + attachments populated.
func (c *Client) CreatePurchaseOrder(ctx context.Context, req PurchaseOrderCreate) (*PurchaseOrder, error) {
	var out PurchaseOrder
	if err := c.Post(ctx, "/api/reorders/purchase-orders/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReorderDataItem is one suggestion row from the reorder_data endpoint:
// an inventory item that's either low-stock or has an active reorder
// request, with everything the PO-create flow needs to prefill a line
// (item_supplier_id when available, suggested_quantity, last unit cost).
type ReorderDataItem struct {
	ItemID                 string        `json:"item_id"`
	ItemName               string        `json:"item_name"`
	SKU                    string        `json:"sku,omitempty"`
	CurrentStock           int           `json:"current_stock"`
	MinimumStock           int           `json:"minimum_stock"`
	SuggestedQuantity      int           `json:"suggested_quantity"`
	UnitCost               DecimalString `json:"unit_cost,omitempty"`
	ItemSupplierID         *int          `json:"item_supplier_id,omitempty"`
	HasActiveReorderReq    bool          `json:"has_active_reorder_request,omitempty"`
	ReorderRequestStatus   string        `json:"reorder_request_status,omitempty"`
	ReorderRequestQuantity int           `json:"reorder_request_quantity,omitempty"`
}

// ReorderDataAsset is the asset-side suggestion row: a capital asset
// associated with this supplier whose warranty or service window may
// be coming up, surfaced alongside the items in case the warden wants
// to bundle a replacement / repair into the same PO.
type ReorderDataAsset struct {
	AssetID   string `json:"asset_id"`
	AssetTag  string `json:"asset_tag,omitempty"`
	AssetName string `json:"asset_name"`
	Reason    string `json:"reason,omitempty"`
}

// ReorderDataSupplier groups items + assets under one supplier in the
// reorder_data response.
type ReorderDataSupplier struct {
	ID             int                `json:"id"`
	Name           string             `json:"name"`
	SupplierType   string             `json:"supplier_type,omitempty"`
	Items          []ReorderDataItem  `json:"items,omitempty"`
	Assets         []ReorderDataAsset `json:"assets,omitempty"`
	TotalItems     int                `json:"total_items,omitempty"`
	EstimatedTotal DecimalString      `json:"estimated_total,omitempty"`
	AvgLeadTime    float64            `json:"avg_lead_time,omitempty"`
}

// ReorderData is the top-level shape returned by
// GET /api/reorders/purchase-orders/reorder_data/. Suppliers is sorted
// server-side by estimated_total descending so the most expensive
// candidates surface first.
type ReorderData struct {
	Suppliers          []ReorderDataSupplier `json:"suppliers"`
	TotalSuppliers     int                   `json:"total_suppliers,omitempty"`
	TotalLowStockItems int                   `json:"total_low_stock_items,omitempty"`
	ItemsWithRequests  int                   `json:"items_with_requests,omitempty"`
}

// GetReorderData fetches the suggestion bundle used by the supplier-
// scoped picker in the New PO flow. AllowAny on the backend so no JWT
// round-trip is required, but the scantty session is already authed
// by the time it lands on PO-create.
func (c *Client) GetReorderData(ctx context.Context) (*ReorderData, error) {
	var out ReorderData
	if err := c.Get(ctx, "/api/reorders/purchase-orders/reorder_data/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ReceiptLine is one line in a PO receive request: which PO line item
// arrived (purchase_order_item, required) and how many units of it
// (quantity_received). The backend resolves item_supplier from the PO
// line itself, so — unlike the old /receipts/ create payload — scantty no
// longer sends it.
type ReceiptLine struct {
	PurchaseOrderItem any `json:"purchase_order_item"`
	QuantityReceived  int `json:"quantity_received"`
}

// ReceiveRequest is the body for the PO receive endpoint. The PO id
// travels in the URL, so the body carries only the received lines plus
// optional delivery metadata. received_by is stamped server-side from the
// authenticated session and must never be sent by the client.
type ReceiveRequest struct {
	Items        []ReceiptLine `json:"items"`
	DeliveryDate string        `json:"delivery_date,omitempty"`
	ReceiptNotes string        `json:"receipt_notes,omitempty"`
}

// ReceivePOItems records receipt of one or more PO line items and returns
// the updated purchase order.
//
//	POST /api/reorders/purchase-orders/{poID}/receive/
//
// This replaces the old CreateReceipt, which POSTed a receipt-with-nested-
// items payload to /api/reorders/receipts/ — a contract the backend never
// implemented. That endpoint's OrderReceiptViewSet validated against
// OrderDeliverySerializer, which required a client-supplied received_by
// (always 400) and treated items as read-only (received nothing).
func (c *Client) ReceivePOItems(ctx context.Context, poID string, req ReceiveRequest) (*PurchaseOrder, error) {
	var out PurchaseOrder
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/receive/", poID)
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkPurchaseOrderItemShipped flips the actual_shipment_date on a PO
// line item via the nested PATCH endpoint
// /api/reorders/purchase-orders/{po_id}/items/{item_id}/. Pass shipDate
// in YYYY-MM-DD; pass "" to clear (un-mark a typo).
func (c *Client) MarkPurchaseOrderItemShipped(
	ctx context.Context, poID, itemID, shipDate string,
) (*PurchaseOrderItem, error) {
	body := map[string]string{"actual_shipment_date": shipDate}
	var out PurchaseOrderItem
	path := fmt.Sprintf(
		"/api/reorders/purchase-orders/%s/items/%s/", poID, itemID,
	)
	if err := c.Patch(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SendToSupplier moves a draft PO to "sent" via the manual send action.
// The po id rides in the URL and the body is empty; the backend rejects
// (400) any PO not currently in draft. The endpoint returns the updated
// PO, but callers reload the detail view afterward so we discard it.
//
//	POST /api/reorders/purchase-orders/{poID}/send_to_supplier/
func (c *Client) SendToSupplier(ctx context.Context, poID string) error {
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/send_to_supplier/", poID)
	return c.Post(ctx, path, nil, nil)
}

// ConfirmOrder moves a sent PO to "confirmed". The backend rejects (400)
// any PO not currently in "sent". expectedDeliveryDate is optional
// (YYYY-MM-DD): when non-empty it rides in the body as
// {"expected_delivery_date": ...}; when empty the body is omitted,
// mirroring the OMS frontend's default confirm (no body). As with
// SendToSupplier the returned PO is discarded in favor of a reload.
//
//	POST /api/reorders/purchase-orders/{poID}/confirm_order/
func (c *Client) ConfirmOrder(ctx context.Context, poID, expectedDeliveryDate string) error {
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/confirm_order/", poID)
	var body any
	if expectedDeliveryDate != "" {
		body = map[string]string{"expected_delivery_date": expectedDeliveryDate}
	}
	return c.Post(ctx, path, body, nil)
}

// PurchaseOrderUpdate carries the editable PO-metadata fields for the update
// (PATCH) endpoint. Every field is a pointer so a nil leaves that column
// untouched — the PATCH only sends what the caller actually set, matching the
// web edit form (purchaseOrderAPI.updateOrder), which patches
// supplier_order_number / sales_order_number / expected_delivery_date / notes.
//
// ExpectedDeliveryDate has clear-vs-untouched semantics: nil omits the field,
// a pointer to "" sends JSON null (clears the date — the backend DateField is
// null=True), and a pointer to "YYYY-MM-DD" sets it. The plain-string fields
// send their value as-is (an empty string is a legal blank).
//
// WorkOrder and OwningGroup (op-shb9) follow the same clear-vs-untouched rule:
// nil leaves the association alone, a pointer to the empty string / to 0 sends
// JSON null to detach it, and any other value attaches that job or committee.
// The zero values are safe detach sentinels rather than values in their own
// right — a WorkOrder id is a UUID string and no auth.Group has pk 0 — and
// sending null is what the backend's serializers accept to clear an FK.
type PurchaseOrderUpdate struct {
	SupplierOrderNumber  *string
	SalesOrderNumber     *string
	ExpectedDeliveryDate *string
	Notes                *string
	WorkOrder            *string
	OwningGroup          *int
}

// UpdatePurchaseOrder patches PO metadata via PATCH
// /api/reorders/purchase-orders/{poID}/ and returns the updated PO. The backend
// uses PurchaseOrderSerializer for updates (po_number/order_date/updated_at are
// read-only). Note the OMS side effect: setting sales_order_number from empty
// to non-empty auto-transitions a DRAFT PO to SENT.
func (c *Client) UpdatePurchaseOrder(ctx context.Context, poID string, req PurchaseOrderUpdate) (*PurchaseOrder, error) {
	body := map[string]any{}
	if req.SupplierOrderNumber != nil {
		body["supplier_order_number"] = *req.SupplierOrderNumber
	}
	if req.SalesOrderNumber != nil {
		body["sales_order_number"] = *req.SalesOrderNumber
	}
	if req.Notes != nil {
		body["notes"] = *req.Notes
	}
	if req.ExpectedDeliveryDate != nil {
		if *req.ExpectedDeliveryDate == "" {
			body["expected_delivery_date"] = nil // clear (JSON null)
		} else {
			body["expected_delivery_date"] = *req.ExpectedDeliveryDate
		}
	}
	putAssociations(body, req.WorkOrder, req.OwningGroup)
	var out PurchaseOrder
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/", poID)
	if err := c.Patch(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LineItemUpdate carries the editable per-line fields for the update-item
// endpoint. Pointer fields are omitted when nil so an edit touches only the
// fields the operator changed. LineCost is the TOTAL cost for the line: the
// backend divides it by quantity_ordered to derive unit_cost_actual (mirroring
// the web edit-cost control). UnitCostActual sets the per-unit actual cost
// directly; send at most one of the two.
//
// WorkOrder (op-bu80) and OwningGroup (op-shb9) are the line's "ordered for"
// associations, and update_item is the only way to write them — the create
// payload can tag a line, but which job the parts turned out to be for is
// usually settled once they arrive. They share the clear-vs-untouched rule
// PurchaseOrderUpdate documents: nil leaves the association alone, a pointer to
// "" / 0 detaches it, anything else attaches that job or committee.
type LineItemUpdate struct {
	ExpectedShipmentDate *string
	Notes                *string
	LineCost             *float64
	UnitCostActual       *float64
	WorkOrder            *string
	OwningGroup          *int
}

// putAssociations writes the work-order / committee association keys into a
// PATCH body under the shared clear-vs-untouched rule (see PurchaseOrderUpdate).
// One helper for both endpoints so the order-level and line-level edits can't
// drift on what "detach" means — the backend reads null and "" identically at
// both levels, and this always sends null.
func putAssociations(body map[string]any, workOrder *string, owningGroup *int) {
	if workOrder != nil {
		if *workOrder == "" {
			body["work_order"] = nil // detach (JSON null)
		} else {
			body["work_order"] = *workOrder
		}
	}
	if owningGroup != nil {
		if *owningGroup == 0 {
			body["owning_group"] = nil // detach (JSON null)
		} else {
			body["owning_group"] = *owningGroup
		}
	}
}

// UpdatePurchaseOrderLineItem patches one PO line via PATCH
// /api/reorders/purchase-orders/{poID}/items/{itemID}/ and returns the updated
// line. expected_shipment_date accepts "" to clear (the backend maps empty to
// NULL). This is the general line-edit path; MarkPurchaseOrderItemShipped is
// the focused actual_shipment_date sibling on the same endpoint.
func (c *Client) UpdatePurchaseOrderLineItem(
	ctx context.Context, poID, itemID string, req LineItemUpdate,
) (*PurchaseOrderItem, error) {
	body := map[string]any{}
	if req.ExpectedShipmentDate != nil {
		body["expected_shipment_date"] = *req.ExpectedShipmentDate
	}
	if req.Notes != nil {
		body["notes"] = *req.Notes
	}
	if req.LineCost != nil {
		body["line_cost"] = *req.LineCost
	}
	if req.UnitCostActual != nil {
		body["unit_cost_actual"] = *req.UnitCostActual
	}
	putAssociations(body, req.WorkOrder, req.OwningGroup)
	var out PurchaseOrderItem
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/items/%s/", poID, itemID)
	if err := c.Patch(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VoidPurchaseOrderLineItem voids a single PO line via POST
// /api/reorders/purchase-orders/{poID}/items/{itemID}/void/ and returns the
// voided line. reason rides in the body ({"reason": …}); the backend defaults
// it to "Item discontinued by supplier" when blank, and rejects (400) a line
// that is already voided or has any received quantity. Voiding an
// item_supplier-backed line also marks that supplier link discontinued.
func (c *Client) VoidPurchaseOrderLineItem(
	ctx context.Context, poID, itemID, reason string,
) (*PurchaseOrderItem, error) {
	var out PurchaseOrderItem
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/items/%s/void/", poID, itemID)
	if err := c.Post(ctx, path, map[string]string{"reason": reason}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// VoidPurchaseOrder voids an entire PO via POST
// /api/reorders/purchase-orders/{poID}/void/ and returns the voided PO. The
// void cascades to every non-voided line. reason rides in the body. The backend
// restricts this to staff/superuser/COO and rejects (400) a PO already voided
// or already received (create a return instead).
func (c *Client) VoidPurchaseOrder(ctx context.Context, poID, reason string) (*PurchaseOrder, error) {
	var out PurchaseOrder
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/void/", poID)
	if err := c.Post(ctx, path, map[string]string{"reason": reason}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MarkDeliveredRequest is the body for the mark-delivered action. delivery_date
// is required (YYYY-MM-DD); tracking_number, carrier, and receipt_notes are
// optional and omitted when blank. This is where PO-level tracking/carrier are
// recorded — the OMS backend has no separate PO update-tracking endpoint, so
// mark-delivered is the tracking-entry path for a purchase order.
type MarkDeliveredRequest struct {
	DeliveryDate   string `json:"delivery_date"`
	TrackingNumber string `json:"tracking_number,omitempty"`
	Carrier        string `json:"carrier,omitempty"`
	ReceiptNotes   string `json:"receipt_notes,omitempty"`
}

// MarkPurchaseOrderDelivered receives every pending quantity on the PO in one
// shot via POST /api/reorders/purchase-orders/{poID}/mark-delivered/ and returns
// the updated PO. Where ReceivePOItems records an explicit per-line partial
// receipt, this marks the whole order delivered on delivery_date. The backend
// rejects (400) a PO not in sent / confirmed / partially_received, or one whose
// lines are all already received.
func (c *Client) MarkPurchaseOrderDelivered(
	ctx context.Context, poID string, req MarkDeliveredRequest,
) (*PurchaseOrder, error) {
	var out PurchaseOrder
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/mark-delivered/", poID)
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadPurchaseOrderAttachment attaches a file to a PO via multipart POST
// /api/reorders/purchase-orders/{poID}/upload-attachment/ and returns the saved
// attachment. The file part is named "file" and description (optional) rides as
// a text field — the exact contract the web uploadAttachment sends. fileName is
// the name stored server-side; file streams the bytes. Any authenticated user
// may upload (deletion is staff-only).
func (c *Client) UploadPurchaseOrderAttachment(
	ctx context.Context, poID, fileName string, file io.Reader, description string,
) (*PurchaseOrderAttachment, error) {
	fields := map[string][]string{}
	if description != "" {
		fields["description"] = []string{description}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("oms: read attachment %s: %w", fileName, err)
	}
	files := []MultipartFile{{Field: "file", Filename: fileName, Data: data}}
	var out PurchaseOrderAttachment
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/upload-attachment/", poID)
	if err := c.PostMultipart(ctx, path, fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeletePurchaseOrderAttachment removes an attachment via DELETE
// /api/reorders/purchase-orders/{poID}/attachments/{attachmentID}/. The backend
// restricts deletion to staff/superuser and returns 204 on success. attachmentID
// is the numeric attachment PK.
func (c *Client) DeletePurchaseOrderAttachment(ctx context.Context, poID string, attachmentID any) error {
	path := fmt.Sprintf("/api/reorders/purchase-orders/%s/attachments/%v/", poID, attachmentID)
	return c.Delete(ctx, path)
}
