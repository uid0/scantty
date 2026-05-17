package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type WorkOrder struct {
	ID                   any                       `json:"id"`
	ShortID              string                    `json:"short_id,omitempty"`
	Title                string                    `json:"title"`
	Description          string                    `json:"description,omitempty"`
	Status               string                    `json:"status"`
	Priority             string                    `json:"priority,omitempty"`
	Asset                any                       `json:"asset,omitempty"`
	AssetID              any                       `json:"asset_id,omitempty"`
	AssetName            string                    `json:"asset_name,omitempty"`
	AssetTag             string                    `json:"asset_tag,omitempty"`
	MaintenanceItem      any                       `json:"maintenance_item,omitempty"`
	MaintenanceItemTitle string                    `json:"maintenance_item_title,omitempty"`
	AssignedTo           any                       `json:"assigned_to,omitempty"`
	AssignedToName       string                    `json:"assigned_to_name,omitempty"`
	CompletedByName      string                    `json:"completed_by_name,omitempty"`
	DueDate              string                    `json:"due_date,omitempty"`
	IsOverdue            bool                      `json:"is_overdue,omitempty"`
	Notes                string                    `json:"notes,omitempty"`
	CompletedAt          *time.Time                `json:"completed_at,omitempty"`
	CreatedAt            time.Time                 `json:"created_at,omitempty"`
	UpdatedAt            time.Time                 `json:"updated_at,omitempty"`
	ClosedAt             *time.Time                `json:"closed_at,omitempty"`
	TaskCompletions      []WorkOrderTaskCompletion `json:"task_completions,omitempty"`
	MaterialUsage        []WorkOrderMaterialUsage  `json:"material_usage,omitempty"`
	Photos               []WorkOrderPhoto          `json:"photos,omitempty"`
}

type WorkOrderTaskCompletion struct {
	ID          any        `json:"id"`
	Task        *string    `json:"task,omitempty"`
	TaskTitle   string     `json:"task_title,omitempty"`
	TaskOrder   int        `json:"task_order,omitempty"`
	IsRequired  bool       `json:"is_required,omitempty"`
	IsCompleted bool       `json:"is_completed,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CompletedBy string     `json:"completed_by_name,omitempty"`
	Notes       string     `json:"notes,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
}

type WorkOrderMaterialUsage struct {
	ID              any           `json:"id"`
	Material        any           `json:"material,omitempty"`
	MaterialName    string        `json:"material_name,omitempty"`
	QuantityPlanned DecimalString `json:"quantity_planned,omitempty"`
	Unit            string        `json:"unit,omitempty"`
	WasUsed         bool          `json:"was_used,omitempty"`
	CreatedAt       time.Time     `json:"created_at,omitempty"`
}

type WorkOrderPhoto struct {
	ID         any       `json:"id"`
	ImageURL   string    `json:"image_url,omitempty"`
	Caption    string    `json:"caption,omitempty"`
	UploadedAt time.Time `json:"uploaded_at,omitempty"`
	UploadedBy string    `json:"uploaded_by_name,omitempty"`
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
