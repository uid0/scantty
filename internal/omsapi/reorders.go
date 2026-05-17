package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type ReorderRequest struct {
	ID           any        `json:"id"`
	Item         string     `json:"item"`
	Quantity     int        `json:"quantity"`
	Priority     string     `json:"priority,omitempty"`
	RequestedBy  string     `json:"requested_by,omitempty"`
	RequestNotes string     `json:"request_notes,omitempty"`
	Status       string     `json:"status,omitempty"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
	ApprovedAt   *time.Time `json:"approved_at,omitempty"`
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
