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
	Tools                []WorkOrderTool           `json:"tools,omitempty"`
	Photos               []WorkOrderPhoto          `json:"photos,omitempty"`
	Validation           *WorkOrderValidation      `json:"validation,omitempty"`
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

// WorkOrderTool is the lean, display-only projection of the source PM
// template's MaintenanceTool rows that rides on every work order — "what to
// grab before starting". Unlike task_completions / material_usage there is no
// completion state to toggle: it is a flat reference list, so the TUI renders
// it and offers no action. The backend sorts it required-first then by name,
// so render it in the order received. May arrive empty.
type WorkOrderTool struct {
	ID           any    `json:"id"`
	Name         string `json:"name"`
	Quantity     int    `json:"quantity"`
	LocationHint string `json:"location_hint,omitempty"`
	IsRequired   bool   `json:"is_required"`
	Notes        string `json:"notes,omitempty"`
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

// CompleteWorkOrderTask toggles completion of a single task step within a
// work order (PATCH .../tasks/{taskID}/complete/). isCompleted is required by
// the backend; notes is optional and only sent when non-empty. Mirrors the
// web workOrderAPI.completeTask.
func (c *Client) CompleteWorkOrderTask(ctx context.Context, woID, taskID string, isCompleted bool, notes string) (*WorkOrderTaskCompletion, error) {
	body := map[string]any{"is_completed": isCompleted}
	if notes != "" {
		body["notes"] = notes
	}
	var out WorkOrderTaskCompletion
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/tasks/%s/complete/", woID, taskID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ToggleWorkOrderMaterial sets whether a planned material was actually used
// (PATCH .../materials/{materialID}/toggle/). Mirrors workOrderAPI.toggleMaterial.
func (c *Client) ToggleWorkOrderMaterial(ctx context.Context, woID, materialID string, wasUsed bool) (*WorkOrderMaterialUsage, error) {
	var out WorkOrderMaterialUsage
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/materials/%s/toggle/", woID, materialID), map[string]any{"was_used": wasUsed}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AddWorkOrderPhoto uploads a photo to a work order as multipart/form-data
// (POST .../add_photo/). The web posts the file under "image" plus the work
// order id under "work_order"; caption is an optional serializer field.
func (c *Client) AddWorkOrderPhoto(ctx context.Context, woID, filename string, data []byte, caption string) (*WorkOrderPhoto, error) {
	fields := map[string][]string{"work_order": {woID}}
	if caption != "" {
		fields["caption"] = []string{caption}
	}
	files := []MultipartFile{{Field: "image", Filename: filename, Data: data}}
	var out WorkOrderPhoto
	if err := c.PostMultipart(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/add_photo/", woID), fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkOrderUploadCompletedItem is one task auto-marked complete by parsing an
// uploaded work-order PDF.
type WorkOrderUploadCompletedItem struct {
	ID        string `json:"id"`
	TaskTitle string `json:"task_title"`
}

// WorkOrderUploadResult is the ingest result returned by upload-pdf.
type WorkOrderUploadResult struct {
	SubmissionID          string                         `json:"submission_id"`
	Kind                  string                         `json:"kind,omitempty"`
	Status                string                         `json:"status"`
	WorkOrderID           *string                        `json:"work_order_id"`
	ThirdPartyWorkOrderID *string                        `json:"third_party_work_order_id,omitempty"`
	CompletedItems        []WorkOrderUploadCompletedItem `json:"completed_items,omitempty"`
	Errors                []string                       `json:"errors,omitempty"`
}

// UploadWorkOrderPdf uploads a scanned/completed work-order PDF for ingest
// (POST work-orders/upload-pdf/, staff only). The file is sent under "pdf".
// Mirrors workOrderAPI.uploadPdf; the endpoint is not scoped to one WO — the
// backend detects which work order the scan belongs to.
func (c *Client) UploadWorkOrderPdf(ctx context.Context, filename string, data []byte) (*WorkOrderUploadResult, error) {
	files := []MultipartFile{{Field: "pdf", Filename: filename, Data: data}}
	var out WorkOrderUploadResult
	if err := c.PostMultipart(ctx, "/api/inventory/work-orders/upload-pdf/", nil, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkOrderValidation is a pre-finalization acknowledgement record. A WO can
// only transition to completed (and generate its PDF) once IsComplete is true
// — all three acknowledgements were recorded together.
type WorkOrderValidation struct {
	ID                         any    `json:"id"`
	WorkOrder                  any    `json:"work_order,omitempty"`
	ValidatedBy                *int   `json:"validated_by,omitempty"`
	ValidatedByName            string `json:"validated_by_name,omitempty"`
	ValidatedAt                string `json:"validated_at,omitempty"`
	ElectricalAcknowledged     bool   `json:"electrical_acknowledged"`
	LotoAcknowledged           bool   `json:"loto_acknowledged"`
	RequiredFieldsAcknowledged bool   `json:"required_fields_acknowledged"`
	IsComplete                 bool   `json:"is_complete"`
	Notes                      string `json:"notes,omitempty"`
}

// ValidateWorkOrderChecklist records the pre-finalization checklist
// acknowledgement (POST .../validate/). The backend rejects the record with
// 400 unless all three flags are true. Mirrors workOrderAPI.validateChecklist.
func (c *Client) ValidateWorkOrderChecklist(ctx context.Context, id string, electrical, loto, requiredFields bool, notes string) (*WorkOrderValidation, error) {
	body := map[string]any{
		"electrical_acknowledged":      electrical,
		"loto_acknowledged":            loto,
		"required_fields_acknowledged": requiredFields,
	}
	if notes != "" {
		body["notes"] = notes
	}
	var out WorkOrderValidation
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/validate/", id), body, &out); err != nil {
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

// MaintenanceOrder mirrors backend ThirdPartyWorkOrderSerializer. The order
// PK is a UUID, and both `vendor` (vendors.Vendor) and `asset`
// (inventory.Asset) are FKs to UUID-PK models — the old int/*int typings
// crashed the list on every row. (`cost`/`scheduled_at` are not emitted by the
// serializer; they stay zero and are kept only for callers that set them.)
type MaintenanceOrder struct {
	ID          any        `json:"id"`
	Title       string     `json:"title"`
	Vendor      *string    `json:"vendor,omitempty"`
	VendorName  string     `json:"vendor_name,omitempty"`
	Asset       *string    `json:"asset,omitempty"`
	Status      string     `json:"status"`
	Cost        float64    `json:"cost,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

func (c *Client) ListMaintenanceOrders(ctx context.Context, q url.Values) (*Page[MaintenanceOrder], error) {
	return GetPage[MaintenanceOrder](ctx, c, "/api/maintenance-orders/work-orders/", q)
}
