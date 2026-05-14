package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type WorkOrder struct {
	ID          any        `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority,omitempty"`
	Asset       any        `json:"asset,omitempty"`
	AssetName   string     `json:"asset_name,omitempty"`
	AssignedTo  any        `json:"assigned_to,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
}

func (c *Client) ListWorkOrders(ctx context.Context, q url.Values) (*Page[WorkOrder], error) {
	return GetPage[WorkOrder](ctx, c, "/api/inventory/work-orders/", q)
}

func (c *Client) GetWorkOrder(ctx context.Context, id string) (*WorkOrder, error) {
	var out WorkOrder
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateWorkOrder(ctx context.Context, wo WorkOrder) (*WorkOrder, error) {
	var out WorkOrder
	if err := c.Post(ctx, "/api/inventory/work-orders/", wo, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateWorkOrder(ctx context.Context, id string, patch map[string]any) (*WorkOrder, error) {
	var out WorkOrder
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/", id), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type WorkOrderTransition struct {
	Action string `json:"action"`
	Notes  string `json:"notes,omitempty"`
}

func (c *Client) TransitionWorkOrder(ctx context.Context, id, action, notes string) (*WorkOrder, error) {
	patch := map[string]any{"status": action}
	if notes != "" {
		patch["notes"] = notes
	}
	return c.UpdateWorkOrder(ctx, id, patch)
}

type MaintenanceOrder struct {
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	Vendor      *int       `json:"vendor,omitempty"`
	VendorName  string     `json:"vendor_name,omitempty"`
	Asset       *int       `json:"asset,omitempty"`
	Status      string     `json:"status"`
	Cost        float64    `json:"cost,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

func (c *Client) ListMaintenanceOrders(ctx context.Context, q url.Values) (*Page[MaintenanceOrder], error) {
	return GetPage[MaintenanceOrder](ctx, c, "/api/maintenance-orders/work-orders/", q)
}
