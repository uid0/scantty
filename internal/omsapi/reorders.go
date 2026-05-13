package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type ReorderRequest struct {
	ID            int       `json:"id"`
	Item          int       `json:"item"`
	Quantity      int       `json:"quantity"`
	Priority      string    `json:"priority,omitempty"`
	RequestedBy   string    `json:"requested_by,omitempty"`
	RequestNotes  string    `json:"request_notes,omitempty"`
	Status        string    `json:"status,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	ApprovedAt    *time.Time `json:"approved_at,omitempty"`
}

type ReorderRequestCreate struct {
	Item         int    `json:"item"`
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

func (c *Client) ListPendingReorders(ctx context.Context, q url.Values) (*Page[ReorderRequest], error) {
	return GetPage[ReorderRequest](ctx, c, "/api/reorders/requests/pending/", q)
}

type PurchaseOrder struct {
	ID         int       `json:"id"`
	Number     string    `json:"number,omitempty"`
	Status     string    `json:"status"`
	Supplier   int       `json:"supplier"`
	Total      float64   `json:"total,omitempty"`
	Currency   string    `json:"currency,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	OrderedAt  *time.Time `json:"ordered_at,omitempty"`
	ReceivedAt *time.Time `json:"received_at,omitempty"`
}

func (c *Client) ListPurchaseOrders(ctx context.Context, q url.Values) (*Page[PurchaseOrder], error) {
	return GetPage[PurchaseOrder](ctx, c, "/api/reorders/purchase-orders/", q)
}

func (c *Client) GetPurchaseOrder(ctx context.Context, id int) (*PurchaseOrder, error) {
	var out PurchaseOrder
	if err := c.Get(ctx, fmt.Sprintf("/api/reorders/purchase-orders/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type ReceiptLine struct {
	ItemSupplier int `json:"item_supplier"`
	QtyReceived  int `json:"qty_received"`
}

type ReceiptCreate struct {
	PurchaseOrder int           `json:"purchase_order"`
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
