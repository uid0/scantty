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
	ReferenceDocuments   *ReferenceDocuments       `json:"reference_documents,omitempty"`
}

// ReferenceDocuments is the manual / revision history / reference links bundle
// the work order carries at sign-off. It is a read-only projection of the
// ASSET's document library — the work order stores no links of its own, so
// there is nothing to edit from the WO screen, and documents are managed on the
// asset. Detail-only: the list serializer omits it, and a backend older than
// op-pzae omits it everywhere, so a nil pointer means "empty", never an error.
type ReferenceDocuments struct {
	Documents []WorkOrderRefDoc  `json:"documents"`
	Links     []WorkOrderRefLink `json:"links"`
}

// WorkOrderRefDoc is one CURRENT document in the asset's library. Version is a
// plain integer (AssetDocument.version, bumped on each upload) — unlike the
// decimal quantities elsewhere on the work order. Revisions is the older
// versions behind it, newest-first, and is empty for a document nobody has
// replaced yet. FileURL is null when the row outlived its file, which decodes
// to "" — a document worth naming even though there is nothing to open.
type WorkOrderRefDoc struct {
	ID              any                       `json:"id"`
	Category        string                    `json:"category,omitempty"`
	CategoryDisplay string                    `json:"category_display,omitempty"`
	Title           string                    `json:"title"`
	Version         int                       `json:"version,omitempty"`
	FileURL         string                    `json:"file_url,omitempty"`
	UploadedAt      string                    `json:"uploaded_at,omitempty"`
	Revisions       []WorkOrderRefDocRevision `json:"revisions,omitempty"`
}

// WorkOrderRefDocRevision is a superseded version of a document.
//
// UploadedAt is a string, not a time.Time: the backend hand-builds these with
// isoformat() rather than letting DRF render a DateTimeField, and it emits null
// for a missing timestamp. Keeping it as text means an unexpected format
// degrades to an unformatted date instead of failing the whole work-order
// decode over a document footnote.
type WorkOrderRefDocRevision struct {
	ID         any    `json:"id"`
	Version    int    `json:"version,omitempty"`
	FileURL    string `json:"file_url,omitempty"`
	UploadedAt string `json:"uploaded_at,omitempty"`
}

// WorkOrderRefLink is one of the asset's quick links (manual PDF, product page,
// wiki). Only links that are actually set arrive, so every entry is renderable.
type WorkOrderRefLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// WorkOrderTaskCompletion is one step of a work order, and carries both halves
// of the per-step photo pair (both read-only):
//
//   - TaskReferenceImageURL — the TEMPLATE step's instructional photo, "what
//     this should look like". Null when the step has no photo, and also when
//     the template step was deleted after the WO was cut (task is nullable), so
//     never treat an empty string as an error.
//   - EvidencePhotos — the shots a tech pinned to THIS step while doing the
//     work, "here is what I did". Arrives as a trimmed projection carrying
//     exactly the five WorkOrderPhoto keys below, so the type is shared. Empty
//     is the normal case, not a failure.
//
// ScanTTY renders no images: both surface as URL / caption text.
type WorkOrderTaskCompletion struct {
	ID                    any              `json:"id"`
	Task                  *string          `json:"task,omitempty"`
	TaskTitle             string           `json:"task_title,omitempty"`
	TaskOrder             int              `json:"task_order,omitempty"`
	IsRequired            bool             `json:"is_required,omitempty"`
	IsCompleted           bool             `json:"is_completed,omitempty"`
	CompletedAt           *time.Time       `json:"completed_at,omitempty"`
	CompletedBy           string           `json:"completed_by_name,omitempty"`
	Notes                 string           `json:"notes,omitempty"`
	TaskReferenceImageURL string           `json:"task_reference_image_url,omitempty"`
	EvidencePhotos        []WorkOrderPhoto `json:"evidence_photos,omitempty"`
	CreatedAt             time.Time        `json:"created_at,omitempty"`
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

// WorkOrderPhoto is a photo on a work order. TaskCompletion pins it to a single
// step (the evidence half of the per-step photo pair) and is null for the
// work-order-level photos every pre-existing upload produced. It is `any` for
// the same reason ID is — the backend's ids are UUID strings today but the
// field is echoed straight back from the serializer.
//
// The trimmed shape nested under a step's evidence_photos carries exactly the
// id / image_url / caption / uploaded_at / uploaded_by_name subset (no
// task_completion — the parent step already is it), which decodes into this
// same struct with TaskCompletion left nil.
type WorkOrderPhoto struct {
	ID             any       `json:"id"`
	TaskCompletion any       `json:"task_completion,omitempty"`
	ImageURL       string    `json:"image_url,omitempty"`
	Caption        string    `json:"caption,omitempty"`
	UploadedAt     time.Time `json:"uploaded_at,omitempty"`
	UploadedBy     string    `json:"uploaded_by_name,omitempty"`
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
// order id under "work_order" — the serializer requires work_order even though
// the id is already in the URL, so it must keep riding; caption is an optional
// serializer field.
//
// taskCompletion optionally pins the photo to ONE step of this work order (that
// step's completion id) as evidence. It is sent only when non-empty: the action
// treats an absent or blank field as work-order-level, which is exactly what
// every caller got before per-step evidence existed. A step id belonging to
// another work order is rejected with 400.
func (c *Client) AddWorkOrderPhoto(ctx context.Context, woID, filename string, data []byte, caption, taskCompletion string) (*WorkOrderPhoto, error) {
	fields := map[string][]string{"work_order": {woID}}
	if caption != "" {
		fields["caption"] = []string{caption}
	}
	if taskCompletion != "" {
		fields["task_completion"] = []string{taskCompletion}
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
