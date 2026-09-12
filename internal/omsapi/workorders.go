package omsapi

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"time"
)

// WorkOrder carries the whole-job stopwatch (op-m3so) alongside its steps:
//
//   - StartedAt — when work FIRST started. A later resume never moves it.
//   - ElapsedSeconds — the server's LIVE total: the segment currently running is
//     already folded in, so a client displays it (and may tick a local copy
//     forward) but never accumulates into it and never sends it back.
//   - IsTiming — whether the clock is running right now.
//   - EstimatedTimeMin — the source PM template's guess, carried here so the
//     actual-vs-estimate comparison needs no second fetch. Nullable: a template
//     with no estimate, or an ad-hoc WO with no template at all.
//
// WO elapsed is wall-time-on-job — setup, LOTO and cleanup included — so it is
// expected to exceed the sum of the per-step clocks rather than equal it.
//
// ActualMaterialCost (op-768w) is the money half of the same idea: the server's
// sum of ActualCost over the lines actually marked used. Server-owned like the
// clock — display it, never accumulate into it. Planned-but-unused material
// costs nothing, and an unpriced used line contributes zero rather than voiding
// the total, so a partially-priced job still reports what is known.
type WorkOrder struct {
	ID      any    `json:"id"`
	ShortID string `json:"short_id,omitempty"`
	Title   string `json:"title"`
	// DisplayTitle is what the backend calls this job: the PM template's title,
	// else the reported problem, else the asset. Always set on the wire (both
	// the list and detail serializers expose it), and the only human name a
	// corrective work order has — it has no template to take a title from.
	DisplayTitle         string                    `json:"display_title,omitempty"`
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
	StartedAt            *time.Time                `json:"started_at,omitempty"`
	ElapsedSeconds       int                       `json:"elapsed_seconds,omitempty"`
	IsTiming             bool                      `json:"is_timing,omitempty"`
	EstimatedTimeMin     *int                      `json:"estimated_time_minutes,omitempty"`
	CompletedAt          *time.Time                `json:"completed_at,omitempty"`
	CreatedAt            time.Time                 `json:"created_at,omitempty"`
	UpdatedAt            time.Time                 `json:"updated_at,omitempty"`
	ClosedAt             *time.Time                `json:"closed_at,omitempty"`
	TaskCompletions      []WorkOrderTaskCompletion `json:"task_completions,omitempty"`
	MaterialUsage        []WorkOrderMaterialUsage  `json:"material_usage,omitempty"`
	ActualMaterialCost   DecimalString             `json:"actual_material_cost,omitempty"`
	Tools                []WorkOrderTool           `json:"tools,omitempty"`
	// ToolRows is the EDITABLE half of the same gear list, and it is a
	// different key from Tools rather than a richer version of it. Tools is the
	// pinned display projection and FALLS BACK to the source PM template's rows
	// when this work order owns none of its own; tool_rows is only ever the work
	// order's own WorkOrderTool records — which rows exist, which are ad-hoc (so
	// removable), and where each is staged for THIS job. Empty on a work order
	// generated before per-job tools, whose Tools is then the template's and has
	// nothing to edit. Detail-only: the list serializer omits it.
	ToolRows []WorkOrderToolRow `json:"tool_rows,omitempty"`
	// LotoCompletions is the STRUCTURED lockout/tagout checklist: one row per
	// energy source on the work order's asset, created when the WO was cut so the
	// printed sheet's loto_<id> checkbox has a record to mark against. Detail-only
	// — the list serializer omits it, so an empty slice on a list row means "not
	// served here" rather than "no lockout required". The free-text half is the
	// asset's lockout_instructions (carried on the `loto` context block) plus the
	// work order's own loto_completion_note.
	LotoCompletions    []WorkOrderLotoCompletion `json:"loto_completions,omitempty"`
	Photos             []WorkOrderPhoto          `json:"photos,omitempty"`
	Validation         *WorkOrderValidation      `json:"validation,omitempty"`
	ReferenceDocuments *ReferenceDocuments       `json:"reference_documents,omitempty"`

	// A SCANNED SHEET WAITING ON A HUMAN. These three ride BOTH serializers —
	// WorkOrderSerializer and WorkOrderListSerializer — which is what lets a
	// list row say a job needs review without anybody opening it.
	//
	// PendingReviewCount counts SUBMISSIONS parked `pending_review`, not the
	// readings on them: a sheet the reader could not align is parked with an
	// empty queue and a sentence saying why, and it counts. Submissions is
	// DETAIL-only — the list serializer omits it, so an empty slice on a list
	// row means "not served here" rather than "none". wo_scan_review.go owns
	// the whole contract and the two writes.
	Submissions        []WorkOrderSubmission `json:"submissions,omitempty"`
	PendingReviewCount int                   `json:"pending_review_count,omitempty"`
	HasPendingReview   bool                  `json:"has_pending_review,omitempty"`
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
//
// It also carries the step's own stopwatch (op-m3so). ElapsedSeconds is live
// exactly as on the work order, and only ONE step per work order can be timing
// at a time — the backend pauses whichever other step was running when a new one
// starts, so the per-step totals partition the work instead of overlapping.
// Ticking a step complete also stops its clock.
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
	ElapsedSeconds        int              `json:"elapsed_seconds,omitempty"`
	IsTiming              bool             `json:"is_timing,omitempty"`
	CreatedAt             time.Time        `json:"created_at,omitempty"`
}

// WorkOrderMaterialUsage is one material line on a work order. It is either the
// frozen copy of a PM template material made when the WO was cut, or — since
// op-768w — an AD-HOC line added during the job. The ad-hoc line is the only way
// a CORRECTIVE work order records a material at all (it has no template, so it
// starts with zero rows), and the way any work order records an out-of-pocket
// buy nobody planned for.
//
// Money: UnitCost is the real price paid per unit and is writable through the
// toggle; ActualCost is the server's QuantityUsed × UnitCost, empty when nobody
// priced the line. Cost is optional throughout — plenty of lines are shop stock
// nobody prices at the point of use — so an empty ActualCost means "unpriced",
// never "free".
//
// Stock: an ad-hoc line carries its own InventoryItem link where a template line
// inherits its spec's, and InventoryItemName resolves whichever applies — a line
// with one decrements that item when marked used, a line without simply records
// the spend. AppliedQuantity / StockApplied expose the live decrement, and a
// line holding one is frozen: neither its quantity nor its price can be edited
// and it cannot be removed until it is un-toggled, which restores the stock.
type WorkOrderMaterialUsage struct {
	ID                any           `json:"id"`
	Material          any           `json:"material,omitempty"`
	MaterialName      string        `json:"material_name,omitempty"`
	IsAdHoc           bool          `json:"is_ad_hoc,omitempty"`
	InventoryItem     *string       `json:"inventory_item,omitempty"`
	InventoryItemName string        `json:"inventory_item_name,omitempty"`
	QuantityPlanned   DecimalString `json:"quantity_planned,omitempty"`
	QuantityUsed      DecimalString `json:"quantity_used,omitempty"`
	Unit              string        `json:"unit,omitempty"`
	UnitCost          DecimalString `json:"unit_cost,omitempty"`
	ActualCost        DecimalString `json:"actual_cost,omitempty"`
	WasUsed           bool          `json:"was_used,omitempty"`
	AppliedQuantity   *int          `json:"applied_quantity,omitempty"`
	StockApplied      bool          `json:"stock_applied,omitempty"`
	ReceiptURL        string        `json:"receipt_url,omitempty"`
	CreatedAt         time.Time     `json:"created_at,omitempty"`
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

// ListActiveWorkOrders returns the UNFINISHED work orders — open first, then
// in-progress — for the "which job is this for?" pickers (op-shb9). A finished
// job is never offered: you don't raise a purchase order against work that is
// already done.
//
// Two requests because the viewset's ?status= is an exact match on one value,
// so there is no way to ask for both at once; the web association pickers make
// the same pair of calls. Neither failure is fatal on its own — the caller
// gets whatever came back plus the first error, because half a list of jobs is
// still worth offering, and an order must never be blocked by a picker for a
// field it may well leave empty.
func (c *Client) ListActiveWorkOrders(ctx context.Context) ([]WorkOrder, error) {
	var out []WorkOrder
	var firstErr error
	for _, status := range []string{"open", "in_progress"} {
		page, err := c.ListWorkOrders(ctx, url.Values{"status": {status}})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, page.Results...)
	}
	return out, firstErr
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

// Timer actions accepted by both stopwatch endpoints. Anything else is a 400
// from the backend rather than a silent no-op, so callers pass these constants.
const (
	TimerStart = "start"
	TimerPause = "pause"
)

// TimerWorkOrder starts or pauses the WHOLE work order's stopwatch
// (POST .../timer/ with {"action": "start"|"pause"}). Mirrors workOrderAPI.timer.
//
// Idempotent by contract: starting a running clock (or pausing a stopped one)
// returns 200 and changes nothing, so a double-press cannot corrupt the total.
// The response is the work order as it now stands, with a live elapsed_seconds.
func (c *Client) TimerWorkOrder(ctx context.Context, woID, action string) (*WorkOrder, error) {
	var out WorkOrder
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/timer/", woID), map[string]any{"action": action}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TimerWorkOrderTask starts or pauses ONE step's stopwatch
// (POST .../tasks/{taskID}/timer/). Mirrors workOrderAPI.taskTimer.
//
// Starting a step pauses whichever other step was running and can flip an OPEN
// work order to in_progress, so the caller re-fetches the work order afterwards
// rather than trusting the single step echoed back here.
func (c *Client) TimerWorkOrderTask(ctx context.Context, woID, taskID, action string) (*WorkOrderTaskCompletion, error) {
	var out WorkOrderTaskCompletion
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/tasks/%s/timer/", woID, taskID), map[string]any{"action": action}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkOrderMaterialEdit carries the two per-line amounts that ride a toggle
// (op-768w). A nil field is left alone; the toggle is the ONLY write endpoint
// for either, so an already-marked line — an out-of-pocket buy is marked used
// and moves no stock — is re-priced by toggling it to the value it already has.
//
// A non-nil UnitCost pointing at "" clears the price: "nobody knows what this
// cost" is a real answer, and the backend reads it as an explicit null. Both
// values freeze once a stock decrement is applied so the recorded spend cannot
// drift from the movement it backs — the backend then keeps the stored value
// silently rather than erroring, so un-toggle first to re-price.
type WorkOrderMaterialEdit struct {
	QuantityUsed *string
	UnitCost     *string
}

// ToggleWorkOrderMaterial sets whether a material was actually used
// (PATCH .../materials/{materialID}/toggle/), optionally writing the used
// quantity and the real price paid alongside. Mirrors workOrderAPI.toggleMaterial.
//
// Marking a line used is what decrements stock, for template and ad-hoc lines
// alike — AddWorkOrderMaterial deliberately creates the line un-used so every
// decrement goes through this one seam.
func (c *Client) ToggleWorkOrderMaterial(ctx context.Context, woID, materialID string, wasUsed bool, edit WorkOrderMaterialEdit) (*WorkOrderMaterialUsage, error) {
	body := map[string]any{"was_used": wasUsed}
	if edit.QuantityUsed != nil {
		body["quantity_used"] = *edit.QuantityUsed
	}
	if edit.UnitCost != nil {
		if *edit.UnitCost == "" {
			body["unit_cost"] = nil
		} else {
			body["unit_cost"] = *edit.UnitCost
		}
	}
	var out WorkOrderMaterialUsage
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/materials/%s/toggle/", woID, materialID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WorkOrderAdHocMaterial is the input for AddWorkOrderMaterial. Only
// MaterialName is required; every other field is an empty-means-omitted string
// so the backend's own defaults apply.
//
// InventoryItem links the line to tracked stock, which is what lets marking it
// used decrement that item. Leave it empty for an out-of-pocket buy: the line
// then records the spend and moves nothing. Adding a material NEVER creates an
// inventory item.
//
// UnitCost left empty defaults from the linked item's current unit cost when an
// item is given — a default, not a lock. Receipt is the optional
// proof-of-purchase photo; when it is set the request goes out as
// multipart/form-data, exactly as AddWorkOrderPhoto does.
type WorkOrderAdHocMaterial struct {
	MaterialName    string
	QuantityUsed    string
	Unit            string
	UnitCost        string
	InventoryItem   string
	ReceiptFilename string
	Receipt         []byte
}

// fields renders the input as form values, skipping the empty ones so the
// backend's defaults survive. Shared by both halves of AddWorkOrderMaterial so
// the JSON and multipart requests can never disagree about what was sent.
func (in WorkOrderAdHocMaterial) fields() map[string]string {
	out := map[string]string{"material_name": in.MaterialName}
	for k, v := range map[string]string{
		"quantity_used":  in.QuantityUsed,
		"unit":           in.Unit,
		"unit_cost":      in.UnitCost,
		"inventory_item": in.InventoryItem,
	} {
		if v != "" {
			out[k] = v
		}
	}
	return out
}

// AddWorkOrderMaterial adds an ad-hoc material line to a work order
// (POST .../materials/). Mirrors workOrderAPI.addMaterial.
//
// The line is created UN-used: the stock decrement is ToggleWorkOrderMaterial's
// job, so there is exactly one path in and out of inventory.
func (c *Client) AddWorkOrderMaterial(ctx context.Context, woID string, in WorkOrderAdHocMaterial) (*WorkOrderMaterialUsage, error) {
	path := fmt.Sprintf("/api/inventory/work-orders/%s/materials/", woID)
	var out WorkOrderMaterialUsage
	if len(in.Receipt) == 0 {
		body := make(map[string]any, 5)
		for k, v := range in.fields() {
			body[k] = v
		}
		if err := c.Post(ctx, path, body, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
	fields := map[string][]string{}
	for k, v := range in.fields() {
		fields[k] = []string{v}
	}
	files := []MultipartFile{{Field: "receipt_image", Filename: in.ReceiptFilename, Data: in.Receipt}}
	if err := c.PostMultipart(ctx, path, fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveWorkOrderMaterial deletes an ad-hoc material line
// (DELETE .../materials/{materialID}/). Mirrors workOrderAPI.removeMaterial.
//
// Two backend guards both answer 400: a TEMPLATE-derived line is never
// deletable — it is the frozen copy of what the job was supposed to be and it
// prints on the sign-off sheet — and neither is a line still holding a stock
// decrement, which would strand the units taken out of inventory. Un-toggle
// that one first to restore the stock, then remove it.
func (c *Client) RemoveWorkOrderMaterial(ctx context.Context, woID, materialID string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/materials/%s/", woID, materialID))
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

// workOrderAttachmentsPath is the top-level attachments collection (op-7pjj /
// OMS #956). Unlike PO attachments — which are nested under the PO and ride the
// PO payload — a work order's attachments live at their own inventory-app
// endpoint and are filtered to one work order with ?work_order=.
const workOrderAttachmentsPath = "/api/inventory/work-order-attachments/"

// Attachment kinds the backend accepts (WorkOrderAttachment.KIND_CHOICES). The
// upload form defaults to "document"; anything outside this set is a 400.
const (
	WorkOrderAttachmentPhoto    = "photo"
	WorkOrderAttachmentDocument = "document"
	WorkOrderAttachmentOther    = "other"
)

// WorkOrderAttachment mirrors the backend WorkOrderAttachmentSerializer. The PK
// and work_order id are `any` for the same reason the rest of the inventory app
// is: they are UUID strings on today's installs but the serializer owns the
// shape. AttachmentURL is the read-only absolute URL the backend builds from the
// stored File (the WO analogue of PurchaseOrderAttachment.FileURL). Kind is one
// of photo/document/other. UploadedBy is the read-only uploader id; there is no
// uploaded_by_name on this serializer, so the list shows the date and kind.
type WorkOrderAttachment struct {
	ID            any       `json:"id"`
	WorkOrder     any       `json:"work_order,omitempty"`
	File          string    `json:"file,omitempty"`
	AttachmentURL string    `json:"attachment_url,omitempty"`
	Description   string    `json:"description,omitempty"`
	Kind          string    `json:"kind,omitempty"`
	UploadedBy    *int      `json:"uploaded_by,omitempty"`
	UploadedAt    time.Time `json:"uploaded_at,omitempty"`
}

// ListWorkOrderAttachments returns the attachments filed against one work order
// (GET .../work-order-attachments/?work_order={woID}). Any authenticated user
// may read, so volunteer makers can view the list even though only staff /
// Logistics / SIG-admins can write. Tolerates both the paginated envelope and a
// bare array.
func (c *Client) ListWorkOrderAttachments(ctx context.Context, woID string) ([]WorkOrderAttachment, error) {
	q := url.Values{"work_order": {woID}}
	var out MaybeList[WorkOrderAttachment]
	if err := c.Get(ctx, workOrderAttachmentsPath, q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// UploadWorkOrderAttachment attaches a file to a work order via multipart POST
// to the top-level collection. The work order id rides as the "work_order" form
// field (the URL has no id — the collection is flat), the bytes as "file", and
// description/kind as text fields; a blank description is omitted, mirroring the
// PO uploader. kind must be one of photo/document/other. Create is staff /
// Logistics / SIG-admin only, so a volunteer maker gets a 403 the screen
// surfaces. fileName is the name stored server-side; file streams the bytes.
func (c *Client) UploadWorkOrderAttachment(
	ctx context.Context, woID, fileName string, file io.Reader, description, kind string,
) (*WorkOrderAttachment, error) {
	fields := map[string][]string{"work_order": {woID}}
	if description != "" {
		fields["description"] = []string{description}
	}
	if kind != "" {
		fields["kind"] = []string{kind}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("oms: read attachment %s: %w", fileName, err)
	}
	files := []MultipartFile{{Field: "file", Filename: fileName, Data: data}}
	var out WorkOrderAttachment
	if err := c.PostMultipart(ctx, workOrderAttachmentsPath, fields, files, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWorkOrderAttachment removes an attachment by its own id via DELETE
// .../work-order-attachments/{attachmentID}/ (204 on success). The endpoint is
// flat, so no work-order id is needed. Deletion is staff / Logistics /
// SIG-admin only and answers 403 otherwise; attachmentID is whatever the
// serializer echoed for the row (UUID today, so `any`).
func (c *Client) DeleteWorkOrderAttachment(ctx context.Context, attachmentID any) error {
	return c.Delete(ctx, fmt.Sprintf("%s%v/", workOrderAttachmentsPath, attachmentID))
}

// --- Per-job tools (op-0v4) -------------------------------------------------

// WorkOrderToolRow is one of the work order's OWN tool rows — the editable half
// of the gear list, decoded from `tool_rows`. It is a different shape from
// WorkOrderTool (`tools`), which is the flat display projection that falls back
// to the PM template; read the doc on WorkOrder.ToolRows for which is which.
//
// Three facts on it decide what a client may offer:
//
//   - IsAdHoc — the row was typed in DURING the job and has no template spec
//     behind it. Only an ad-hoc row is deletable: a template-derived row is the
//     frozen copy of what the job was supposed to need and it prints on the
//     sign-off sheet, so DELETE on one is a 400.
//   - ResolvedLocation — WHERE THE TOOL IS, and the only location field to
//     display. LocationHint is this job's own staging spot and is BLANK whenever
//     the linked inventory item's storage location is standing in, so a surface
//     reading the hint shows nothing for a tool that has a perfectly good
//     location. The hint is what a restage WRITES; the resolved value is what it
//     READS.
//   - InventoryItemName — the tracked stock behind the row, when there is any.
//     Tools are NOT consumed (gathered, used, returned), so the link only ever
//     supplies that location fallback: no stock moves, no cost is recorded, and
//     adding a tool never creates an inventory item.
//
// Everything but LocationHint is read-only on the wire — the display fields are
// frozen at generation, or set exactly once by the add — so a restage is the
// whole editable surface of a row.
type WorkOrderToolRow struct {
	ID                any       `json:"id"`
	WorkOrder         any       `json:"work_order,omitempty"`
	Tool              any       `json:"tool,omitempty"`
	InventoryItem     *string   `json:"inventory_item,omitempty"`
	InventoryItemName string    `json:"inventory_item_name,omitempty"`
	IsAdHoc           bool      `json:"is_ad_hoc,omitempty"`
	Name              string    `json:"name"`
	Quantity          int       `json:"quantity,omitempty"`
	LocationHint      string    `json:"location_hint,omitempty"`
	ResolvedLocation  string    `json:"resolved_location,omitempty"`
	IsRequired        bool      `json:"is_required,omitempty"`
	Notes             string    `json:"notes,omitempty"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
}

// IDString is the row's id as a path segment, through the one coercer that
// knows what the decoder made of an untyped value (client.go's anyIDString).
func (t WorkOrderToolRow) IDString() string { return anyIDString(t.ID) }

// WorkOrderAdHocTool is the input for AddWorkOrderTool. Name is the only
// required field — it is the only thing the tech reads off the printed list, and
// the serializer rejects a blank one.
//
// Quantity is a *int so "leave it to the server" and "the operator typed a
// number" are different facts: the serializer defaults it to 1 and bounds it at
// 1, and a plain int could not tell an unsupplied quantity from a typed zero —
// which must reach the server and be refused BY the server rather than silently
// become 1. IsRequired is a *bool for the same reason: it defaults to TRUE, so
// an absent key and a false one mean opposite things.
//
// LocationHint and Notes are omitted when empty; both default to "" server-side,
// so omitting and sending blank are the same write and the shorter body is the
// one the web sends.
type WorkOrderAdHocTool struct {
	Name         string
	Quantity     *int
	LocationHint string
	IsRequired   *bool
	Notes        string
	// InventoryItem links the row to tracked stock so that item's storage
	// location stands in wherever no per-job hint is set. Optional, and empty
	// means unlinked; it moves no stock either way.
	InventoryItem string
}

func (in WorkOrderAdHocTool) body() map[string]any {
	out := map[string]any{"name": in.Name}
	if in.Quantity != nil {
		out["quantity"] = *in.Quantity
	}
	if in.IsRequired != nil {
		out["is_required"] = *in.IsRequired
	}
	if in.LocationHint != "" {
		out["location_hint"] = in.LocationHint
	}
	if in.Notes != "" {
		out["notes"] = in.Notes
	}
	if in.InventoryItem != "" {
		out["inventory_item"] = in.InventoryItem
	}
	return out
}

// AddWorkOrderTool records a tool this job turned out to need
// (POST .../tools/). Mirrors workOrderAPI.addTool.
//
// It is the only way a CORRECTIVE work order lists a tool at all — it has no PM
// template to copy rows from — and the way any work order records something the
// tech found they needed mid-job. Nothing here moves stock.
//
// ONE CONSEQUENCE A CALLER MUST SURFACE: on a work order that owns no rows yet
// but whose `tools` payload is its PM TEMPLATE's list, this row becomes the
// work order's own list, and `tools` then serves that one row instead of the
// template's (inventory.services.work_order_context.build_tools_context takes
// the rows branch as soon as one exists). The template's tools are not deleted
// and the next work order off it still gets them, but they stop being displayed
// on THIS one.
func (c *Client) AddWorkOrderTool(ctx context.Context, woID string, in WorkOrderAdHocTool) (*WorkOrderToolRow, error) {
	var out WorkOrderToolRow
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/tools/", woID), in.body(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWorkOrderToolLocation restages one tool for THIS job
// (PATCH .../tools/{toolID}/ with {"location_hint": …}). Mirrors
// workOrderAPI.updateToolLocation.
//
// Allowed on EVERY row, template-derived included: per-job restaging is the
// point of the model, and the write lands only on the work order — never back on
// the MaintenanceTool the row was copied from — so the next work order off that
// template still gets the template's location.
//
// The hint is sent even when BLANK, and that is not a redundant write: blank
// CLEARS the per-job hint and lets the linked inventory item's location stand in
// again, which is a different stored state from the hint the row had before.
// (The serializer declares location_hint allow_blank and required, so there is
// no "omit it" option here in any case.)
func (c *Client) UpdateWorkOrderToolLocation(ctx context.Context, woID, toolID, locationHint string) (*WorkOrderToolRow, error) {
	body := map[string]any{"location_hint": locationHint}
	var out WorkOrderToolRow
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/tools/%s/", woID, toolID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RemoveWorkOrderTool deletes one AD-HOC tool row
// (DELETE .../tools/{toolID}/, 204 on success). Mirrors workOrderAPI.removeTool.
//
// The backend refuses a TEMPLATE-derived row with 400 — it is the frozen copy of
// what the job was supposed to need and it prints on the sign-off sheet — and
// 404s a tool id that is not on THIS work order, so a row cannot be reached
// sideways from another job.
func (c *Client) RemoveWorkOrderTool(ctx context.Context, woID, toolID string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/tools/%s/", woID, toolID))
}

// --- Structured lockout/tagout ---------------------------------------------

// WorkOrderLotoCompletion is the record that ONE energy source on this job's
// asset was isolated — the structured half of lockout/tagout, one row per
// loto.AssetEnergySource, cut when the work order was generated.
//
// THE DESCRIPTIVE FIELDS ARE DENORMALIZED COPIES, not a view of the live energy
// source: SourceType / SourceLabel / IsolationPoint / RequiredDevices were
// frozen at generation so the printed sheet and the persisted record cannot
// disagree, and they survive the energy source being edited or deleted
// afterwards (EnergySource is then null). So a client renders these, never the
// asset's current LOTO requirements, when it is describing what THIS job's
// record says.
//
// RequiredDevices is a comma-joined STRING, not a list — it is the frozen text
// of which padlocks/blocks the source needed.
//
// IsCompleted / CompletedAt / CompletedBy are the record itself. The completion
// endpoint is a TOGGLE: recording clears to false again, and the backend then
// clears the actor and the timestamp with it, so an un-recorded row keeps no
// trace of having been marked.
type WorkOrderLotoCompletion struct {
	ID              any        `json:"id"`
	WorkOrder       any        `json:"work_order,omitempty"`
	EnergySource    any        `json:"energy_source,omitempty"`
	SourceType      string     `json:"source_type,omitempty"`
	SourceLabel     string     `json:"source_label,omitempty"`
	IsolationPoint  string     `json:"isolation_point,omitempty"`
	RequiredDevices string     `json:"required_devices,omitempty"`
	IsCompleted     bool       `json:"is_completed,omitempty"`
	CompletedBy     *int       `json:"completed_by,omitempty"`
	CompletedByName string     `json:"completed_by_name,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Notes           string     `json:"notes,omitempty"`
	CreatedAt       time.Time  `json:"created_at,omitempty"`
}

// IDString is the record's id as a path segment, through client.go's one
// coercer for an untyped id.
func (l WorkOrderLotoCompletion) IDString() string { return anyIDString(l.ID) }

// CompleteWorkOrderLoto records — or takes back — the isolation of ONE energy
// source within a work order
// (PATCH .../loto/{lotoID}/complete/). Mirrors workOrderAPI.completeLoto.
//
// WHAT THE SERVER DOES AND DOES NOT CHECK, because a client must not invent the
// difference (inventory.views.WorkOrderViewSet.complete_loto is the contract):
//
//   - is_completed is REQUIRED. Omitting it is a 400 "is_completed is
//     required."; the value is read with bool(), so anything truthy records.
//   - THERE IS NO ORDERING RULE AND NO ALREADY-COMPLETE RULE. The endpoint is a
//     plain toggle over the row's own flag: any row may be marked at any time,
//     in any order, and re-marking one already marked is accepted and leaves the
//     original CompletedAt/CompletedBy in place (they are only stamped on a
//     transition INTO completed). So nothing on this side may refuse a
//     completion on those grounds — there is no server judgement to relay.
//   - A row id that is not on THIS work order is a 404 "LOTO completion record
//     not found.", so a record cannot be reached sideways from another job.
//   - Recording one can advance an OPEN work order to in_progress, and NEVER to
//     completed: closing the job stays a required-tasks gate plus a human
//     confirm. The reply is the single record, so a caller that wants the
//     work order's new status re-fetches rather than inferring it.
//
// notes is sent only when non-empty, because the backend writes the field only
// when the KEY is present: an empty string would overwrite a note somebody
// recorded elsewhere, where omitting leaves it alone.
func (c *Client) CompleteWorkOrderLoto(ctx context.Context, woID, lotoID string, isCompleted bool, notes string) (*WorkOrderLotoCompletion, error) {
	body := map[string]any{"is_completed": isCompleted}
	if notes != "" {
		body["notes"] = notes
	}
	var out WorkOrderLotoCompletion
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/work-orders/%s/loto/%s/complete/", woID, lotoID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
