package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// ReorderItemDetails is the slim subset of fields the reorder-queue
// screen needs from the nested item_details payload (backend
// ReorderRequestSerializer uses the full InventoryItemSerializer, but
// we only render a handful of fields).
type ReorderItemDetails struct {
	ID                 string        `json:"id,omitempty"`
	Name               string        `json:"name,omitempty"`
	SKU                string        `json:"sku,omitempty"`
	CurrentStock       int           `json:"current_stock,omitempty"`
	MinimumStock       int           `json:"minimum_stock,omitempty"`
	ReorderQuantity    int           `json:"reorder_quantity,omitempty"`
	PreferredSupplier  string        `json:"preferred_supplier_name,omitempty"`
	UnitCost           DecimalString `json:"unit_cost,omitempty"`
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

type PurchaseOrder struct {
	ID                    any                       `json:"id"`
	Number                string                    `json:"po_number,omitempty"`
	Status                string                    `json:"status"`
	StatusLabel           string                    `json:"status_label,omitempty"`
	Supplier              any                       `json:"supplier,omitempty"`
	SupplierName          string                    `json:"supplier_name,omitempty"`
	SupplierDetails       string                    `json:"supplier_details,omitempty"`
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
	ID             int        `json:"id"`
	File           string     `json:"file,omitempty"`
	FileURL        string     `json:"file_url,omitempty"`
	FileName       string     `json:"file_name,omitempty"`
	Description    string     `json:"description,omitempty"`
	UploadedBy     *int       `json:"uploaded_by,omitempty"`
	UploadedByName string     `json:"uploaded_by_name,omitempty"`
	UploadedAt     time.Time  `json:"uploaded_at,omitempty"`
}

type PurchaseOrderItem struct {
	ID                   any            `json:"id"`
	PurchaseOrder        any            `json:"purchase_order,omitempty"`
	ItemSupplier         any            `json:"item_supplier,omitempty"`
	Asset                any            `json:"asset,omitempty"`
	Description          string         `json:"description,omitempty"`
	SupplierDetails      string         `json:"supplier_details,omitempty"`
	QuantityOrdered      int            `json:"quantity_ordered,omitempty"`
	QuantityReceived     int            `json:"quantity_received,omitempty"`
	QuantityPending      int            `json:"quantity_pending,omitempty"`
	UnitCostOrdered      DecimalString  `json:"unit_cost_ordered,omitempty"`
	UnitCostActual       DecimalString  `json:"unit_cost_actual,omitempty"`
	EstimatedCost        DecimalString  `json:"estimated_cost,omitempty"`
	ActualCost           DecimalString  `json:"actual_cost,omitempty"`
	IsFullyReceived      bool           `json:"is_fully_received,omitempty"`
	IsVoided             bool           `json:"is_voided,omitempty"`
	VoidedAt             *time.Time     `json:"voided_at,omitempty"`
	VoidReason           string         `json:"void_reason,omitempty"`
	Notes                string         `json:"notes,omitempty"`
	ItemType             string         `json:"item_type,omitempty"`
	ExpectedShipmentDate string         `json:"expected_shipment_date,omitempty"`
	ActualShipmentDate   string         `json:"actual_shipment_date,omitempty"`
	CreatedAt            time.Time      `json:"created_at,omitempty"`
	UpdatedAt            time.Time      `json:"updated_at,omitempty"`
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

// PurchaseOrderCreateItem is one line in the PurchaseOrderCreate payload.
// The OMS create endpoint accepts three line shapes, picked by which fields
// are set: inventory (ItemSupplierID), asset (AssetID), or freeform
// (Description). The fields are pointers so omitempty omits them cleanly
// when nil — sending ``item_supplier_id: 0`` or ``asset_id: ""`` would
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
type PurchaseOrderCreate struct {
	Supplier              int                       `json:"supplier"`
	ExpectedDeliveryDate  string                    `json:"expected_delivery_date,omitempty"`
	Notes                 string                    `json:"notes,omitempty"`
	Items                 []PurchaseOrderCreateItem `json:"items"`
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
	AssetID    string `json:"asset_id"`
	AssetTag   string `json:"asset_tag,omitempty"`
	AssetName  string `json:"asset_name"`
	Reason     string `json:"reason,omitempty"`
}

// ReorderDataSupplier groups items + assets under one supplier in the
// reorder_data response.
type ReorderDataSupplier struct {
	ID            int                `json:"id"`
	Name          string             `json:"name"`
	SupplierType  string             `json:"supplier_type,omitempty"`
	Items         []ReorderDataItem  `json:"items,omitempty"`
	Assets        []ReorderDataAsset `json:"assets,omitempty"`
	TotalItems    int                `json:"total_items,omitempty"`
	EstimatedTotal DecimalString     `json:"estimated_total,omitempty"`
	AvgLeadTime   int                `json:"avg_lead_time,omitempty"`
}

// ReorderData is the top-level shape returned by
// GET /api/reorders/purchase-orders/reorder_data/. Suppliers is sorted
// server-side by estimated_total descending so the most expensive
// candidates surface first.
type ReorderData struct {
	Suppliers           []ReorderDataSupplier `json:"suppliers"`
	TotalSuppliers      int                   `json:"total_suppliers,omitempty"`
	TotalLowStockItems  int                   `json:"total_low_stock_items,omitempty"`
	ItemsWithRequests   int                   `json:"items_with_requests,omitempty"`
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

type ReceiptLine struct {
	ItemSupplier any `json:"item_supplier,omitempty"`
	PurchaseOrderItem any `json:"purchase_order_item,omitempty"`
	QtyReceived  int `json:"qty_received"`
}

type ReceiptCreate struct {
	PurchaseOrder any           `json:"purchase_order"`
	Items         []ReceiptLine `json:"items"`
	Notes         string        `json:"notes,omitempty"`
}

type Receipt struct {
	ID            int           `json:"id"`
	PurchaseOrder int           `json:"purchase_order"`
	Items         []ReceiptLine `json:"items"`
	ReceivedAt    time.Time     `json:"received_at,omitempty"`
	ReceivedBy    string        `json:"received_by,omitempty"`
}

func (c *Client) CreateReceipt(ctx context.Context, req ReceiptCreate) (*Receipt, error) {
	var out Receipt
	if err := c.Post(ctx, "/api/reorders/receipts/", req, &out); err != nil {
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
