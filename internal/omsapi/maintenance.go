package omsapi

import (
	"context"
	"net/url"
	"time"
)

// MaintenanceItem mirrors MaintenanceItemSerializer — a recurring
// preventive-maintenance (PM) task tied to an asset. The computed fields
// (is_overdue, days_overdue, next_due_at) are SerializerMethodField /
// ReadOnlyField projections, and materials + tasks arrive nested so a single
// GET hydrates the whole edit form. Decimal money fields use DecimalString
// because the serializer emits them as JSON strings.
type MaintenanceItem struct {
	ID               string                `json:"id"`
	Asset            string                `json:"asset"`
	AssetName        string                `json:"asset_name,omitempty"`
	AssetTag         string                `json:"asset_tag,omitempty"`
	Title            string                `json:"title"`
	Description      string                `json:"description,omitempty"`
	Instructions     string                `json:"instructions,omitempty"`
	EstimatedTimeMin *int                  `json:"estimated_time_minutes,omitempty"`
	EstimatedCost    DecimalString         `json:"estimated_cost,omitempty"`
	IntervalDays     *int                  `json:"interval_days,omitempty"`
	IsActive         bool                  `json:"is_active"`
	IsOverdue        bool                  `json:"is_overdue"`
	DaysOverdue      *int                  `json:"days_overdue,omitempty"`
	NextDueAt        *time.Time            `json:"next_due_at,omitempty"`
	LastCompletedAt  *time.Time            `json:"last_completed_at,omitempty"`
	Materials        []MaintenanceMaterial `json:"materials,omitempty"`
	Tools            []MaintenanceTool     `json:"tools,omitempty"`
	Tasks            []MaintenanceTask     `json:"tasks,omitempty"`
	CreatedAt        time.Time             `json:"created_at,omitempty"`
	UpdatedAt        time.Time             `json:"updated_at,omitempty"`
}

// MaintenanceItemWrite is the create/update payload. estimated_time_minutes and
// interval_days are pointers WITHOUT omitempty so a nil marshals to an explicit
// JSON null — that's how the operator clears a previously-set interval on a
// PATCH (matching the web form, whose zod schema types both as nullable).
// estimated_cost is a string ("0" / "12.34") like the web's String(cost).
type MaintenanceItemWrite struct {
	Asset            string `json:"asset"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	Instructions     string `json:"instructions"`
	EstimatedTimeMin *int   `json:"estimated_time_minutes"`
	EstimatedCost    string `json:"estimated_cost"`
	IntervalDays     *int   `json:"interval_days"`
	IsActive         bool   `json:"is_active"`
}

// MaintenanceMaterialInventoryDetail is the optional inventory-item projection
// nested on a material when it's linked to a tracked InventoryItem.
type MaintenanceMaterialInventoryDetail struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	CurrentStock int    `json:"current_stock"`
	MinimumStock int    `json:"minimum_stock"`
	ReorderQty   int    `json:"reorder_quantity"`
}

// MaintenanceMaterial mirrors MaintenanceMaterialSerializer — one consumable
// needed to complete a MaintenanceItem.
type MaintenanceMaterial struct {
	ID                   string                              `json:"id,omitempty"`
	MaintenanceItem      string                              `json:"maintenance_item,omitempty"`
	InventoryItem        *string                             `json:"inventory_item,omitempty"`
	InventoryItemDetail  *MaintenanceMaterialInventoryDetail `json:"inventory_item_detail,omitempty"`
	Name                 string                              `json:"name"`
	Quantity             DecimalString                       `json:"quantity,omitempty"`
	Unit                 string                              `json:"unit,omitempty"`
	EstimatedCostPerUnit DecimalString                       `json:"estimated_cost_per_unit,omitempty"`
	TotalEstimatedCost   DecimalString                       `json:"total_estimated_cost,omitempty"`
	Notes                string                              `json:"notes,omitempty"`
	CreatedAt            time.Time                           `json:"created_at,omitempty"`
}

// MaintenanceMaterialWrite is the create/update payload for a material.
// Quantity and estimated_cost_per_unit are strings because the backend field
// is a DecimalField; the web likewise sends String(quantity).
type MaintenanceMaterialWrite struct {
	MaintenanceItem      string `json:"maintenance_item"`
	Name                 string `json:"name"`
	Quantity             string `json:"quantity"`
	Unit                 string `json:"unit"`
	EstimatedCostPerUnit string `json:"estimated_cost_per_unit"`
	Notes                string `json:"notes"`
}

// MaintenanceTool mirrors MaintenanceToolSerializer — one tool needed to
// perform a MaintenanceItem. It is the direct analog of MaintenanceMaterial,
// but for gear that gets gathered, used, and returned rather than consumed: so
// a tool carries a location_hint ("Tool crib, drawer 3") for where to find it
// instead of a material's unit + per-unit cost.
//
// Quantity is a plain int — the backend field is a PositiveIntegerField, NOT
// the DecimalField a material quantity uses, so it arrives as a JSON number
// and must not be decoded as a DecimalString.
type MaintenanceTool struct {
	ID              string  `json:"id,omitempty"`
	MaintenanceItem string  `json:"maintenance_item,omitempty"`
	InventoryItem   *string `json:"inventory_item,omitempty"`
	// Same five keys the material serializer projects, so the type is shared.
	InventoryItemDetail *MaintenanceMaterialInventoryDetail `json:"inventory_item_detail,omitempty"`
	Name                string                              `json:"name"`
	Quantity            int                                 `json:"quantity,omitempty"`
	LocationHint        string                              `json:"location_hint,omitempty"`
	IsRequired          bool                                `json:"is_required"`
	Notes               string                              `json:"notes,omitempty"`
	CreatedAt           time.Time                           `json:"created_at,omitempty"`
}

// MaintenanceToolWrite is the create/update payload for a tool. Unlike
// MaintenanceMaterialWrite — whose decimal fields ride as strings — quantity is
// a JSON number here, matching the model's PositiveIntegerField.
type MaintenanceToolWrite struct {
	MaintenanceItem string `json:"maintenance_item"`
	Name            string `json:"name"`
	Quantity        int    `json:"quantity"`
	LocationHint    string `json:"location_hint"`
	IsRequired      bool   `json:"is_required"`
	Notes           string `json:"notes"`
}

// MaintenanceTask mirrors MaintenanceTaskSerializer — one ordered sub-step
// within a MaintenanceItem (checked off on the generated work order).
type MaintenanceTask struct {
	ID              string    `json:"id,omitempty"`
	MaintenanceItem string    `json:"maintenance_item,omitempty"`
	Order           int       `json:"order"`
	Title           string    `json:"title"`
	Description     string    `json:"description,omitempty"`
	IsRequired      bool      `json:"is_required"`
	CreatedAt       time.Time `json:"created_at,omitempty"`
}

// MaintenanceTaskWrite is the create/update payload for a task step.
type MaintenanceTaskWrite struct {
	MaintenanceItem string `json:"maintenance_item"`
	Order           int    `json:"order"`
	Title           string `json:"title"`
	Description     string `json:"description"`
	IsRequired      bool   `json:"is_required"`
}

// MaintenanceLog mirrors MaintenanceLogSerializer — the completion record the
// complete action returns and stamps last_completed_at from.
type MaintenanceLog struct {
	ID                   string        `json:"id"`
	MaintenanceItem      string        `json:"maintenance_item"`
	MaintenanceItemTitle string        `json:"maintenance_item_title,omitempty"`
	AssetName            string        `json:"asset_name,omitempty"`
	CompletedBy          *int          `json:"completed_by,omitempty"`
	CompletedByName      *string       `json:"completed_by_name,omitempty"`
	CompletedAt          time.Time     `json:"completed_at,omitempty"`
	TimeSpentMinutes     *int          `json:"time_spent_minutes,omitempty"`
	CostIncurred         DecimalString `json:"cost_incurred,omitempty"`
	Notes                string        `json:"notes,omitempty"`
	CreatedAt            time.Time     `json:"created_at,omitempty"`
}

// MaintenanceCompleteRequest is the body for the complete action. Every field
// is optional (the backend injects maintenance_item + completed_by), so the
// pointers/omitempty keep an empty completion a bare {}.
type MaintenanceCompleteRequest struct {
	TimeSpentMinutes *int   `json:"time_spent_minutes,omitempty"`
	CostIncurred     string `json:"cost_incurred,omitempty"`
	Notes            string `json:"notes,omitempty"`
}

// MaintenanceWorkOrderRequest is the optional body for generate_work_order.
// An empty request lets the backend default due_date from next_due_at.
type MaintenanceWorkOrderRequest struct {
	DueDate string `json:"due_date,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// MaterialStockAlert is one low-stock warning from check_material_stock.
type MaterialStockAlert struct {
	MaterialID string `json:"material_id"`
	ItemID     string `json:"item_id"`
	Name       string `json:"name"`
	Current    int    `json:"current"`
	Minimum    int    `json:"minimum"`
	ReorderQty int    `json:"reorder_qty"`
}

// MaterialStockResponse wraps the check_material_stock action's alert list.
type MaterialStockResponse struct {
	LowStockAlerts []MaterialStockAlert `json:"low_stock_alerts"`
}

const maintenanceItemsPath = "/api/inventory/maintenance-items/"

// ListMaintenanceItems returns the paginated PM-item list. Accepts the same
// query filters as the backend viewset: ?asset=<uuid> and ?is_active=true.
func (c *Client) ListMaintenanceItems(ctx context.Context, q url.Values) (*Page[MaintenanceItem], error) {
	return GetPage[MaintenanceItem](ctx, c, maintenanceItemsPath, q)
}

// GetMaintenanceItem fetches one PM item with its nested materials + tasks.
func (c *Client) GetMaintenanceItem(ctx context.Context, id string) (*MaintenanceItem, error) {
	var out MaintenanceItem
	if err := c.Get(ctx, maintenanceItemsPath+id+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateMaintenanceItem POSTs a new PM item. Materials and tasks are created
// separately (they're read-only on the item serializer).
func (c *Client) CreateMaintenanceItem(ctx context.Context, body MaintenanceItemWrite) (*MaintenanceItem, error) {
	var out MaintenanceItem
	if err := c.Post(ctx, maintenanceItemsPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMaintenanceItem PATCHes an existing PM item (partial update, mirroring
// the web form so omitted keys are untouched).
func (c *Client) UpdateMaintenanceItem(ctx context.Context, id string, body MaintenanceItemWrite) (*MaintenanceItem, error) {
	var out MaintenanceItem
	if err := c.Patch(ctx, maintenanceItemsPath+id+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMaintenanceItem removes a PM item (cascades to its materials/tasks).
func (c *Client) DeleteMaintenanceItem(ctx context.Context, id string) error {
	return c.Delete(ctx, maintenanceItemsPath+id+"/")
}

// CompleteMaintenanceItem logs a completion (POST .../complete/) and returns
// the created MaintenanceLog. The backend stamps completed_by + updates the
// item's last_completed_at.
func (c *Client) CompleteMaintenanceItem(ctx context.Context, id string, req MaintenanceCompleteRequest) (*MaintenanceLog, error) {
	var out MaintenanceLog
	if err := c.Post(ctx, maintenanceItemsPath+id+"/complete/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CloneMaintenanceItem copies this PM item (with its tasks + materials) onto
// another asset (POST .../clone/) and returns the new item.
func (c *Client) CloneMaintenanceItem(ctx context.Context, id, targetAssetID string) (*MaintenanceItem, error) {
	body := map[string]string{"target_asset_id": targetAssetID}
	var out MaintenanceItem
	if err := c.Post(ctx, maintenanceItemsPath+id+"/clone/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GenerateMaintenanceWorkOrder creates a work order from this PM item,
// pre-populated with its tasks + materials (POST .../generate_work_order/).
func (c *Client) GenerateMaintenanceWorkOrder(ctx context.Context, id string, req MaintenanceWorkOrderRequest) (*WorkOrder, error) {
	var out WorkOrder
	if err := c.Post(ctx, maintenanceItemsPath+id+"/generate_work_order/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CheckMaintenanceMaterialStock returns low-stock alerts for any inventory-
// linked materials on this PM item (GET .../check_material_stock/).
func (c *Client) CheckMaintenanceMaterialStock(ctx context.Context, id string) (*MaterialStockResponse, error) {
	var out MaterialStockResponse
	if err := c.Get(ctx, maintenanceItemsPath+id+"/check_material_stock/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

const maintenanceTasksPath = "/api/inventory/maintenance-tasks/"

// ListMaintenanceTasks returns the task steps for one PM item.
func (c *Client) ListMaintenanceTasks(ctx context.Context, maintenanceItemID string) (*Page[MaintenanceTask], error) {
	q := url.Values{}
	q.Set("maintenance_item", maintenanceItemID)
	return GetPage[MaintenanceTask](ctx, c, maintenanceTasksPath, q)
}

// CreateMaintenanceTask adds a task step to a PM item.
func (c *Client) CreateMaintenanceTask(ctx context.Context, body MaintenanceTaskWrite) (*MaintenanceTask, error) {
	var out MaintenanceTask
	if err := c.Post(ctx, maintenanceTasksPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMaintenanceTask PATCHes a task step.
func (c *Client) UpdateMaintenanceTask(ctx context.Context, id string, body MaintenanceTaskWrite) (*MaintenanceTask, error) {
	var out MaintenanceTask
	if err := c.Patch(ctx, maintenanceTasksPath+id+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMaintenanceTask removes a task step.
func (c *Client) DeleteMaintenanceTask(ctx context.Context, id string) error {
	return c.Delete(ctx, maintenanceTasksPath+id+"/")
}

const maintenanceMaterialsPath = "/api/inventory/maintenance-materials/"

// ListMaintenanceMaterials returns the materials for one PM item.
func (c *Client) ListMaintenanceMaterials(ctx context.Context, maintenanceItemID string) (*Page[MaintenanceMaterial], error) {
	q := url.Values{}
	q.Set("maintenance_item", maintenanceItemID)
	return GetPage[MaintenanceMaterial](ctx, c, maintenanceMaterialsPath, q)
}

// CreateMaintenanceMaterial adds a material to a PM item.
func (c *Client) CreateMaintenanceMaterial(ctx context.Context, body MaintenanceMaterialWrite) (*MaintenanceMaterial, error) {
	var out MaintenanceMaterial
	if err := c.Post(ctx, maintenanceMaterialsPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMaintenanceMaterial PATCHes a material. The web only ever creates or
// deletes materials, but the backend viewset is a full ModelViewSet so an
// in-place edit is supported — the TUI uses it so editing a material row keeps
// its id instead of churning a delete+create.
func (c *Client) UpdateMaintenanceMaterial(ctx context.Context, id string, body MaintenanceMaterialWrite) (*MaintenanceMaterial, error) {
	var out MaintenanceMaterial
	if err := c.Patch(ctx, maintenanceMaterialsPath+id+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMaintenanceMaterial removes a material.
func (c *Client) DeleteMaintenanceMaterial(ctx context.Context, id string) error {
	return c.Delete(ctx, maintenanceMaterialsPath+id+"/")
}

const maintenanceToolsPath = "/api/inventory/maintenance-tools/"

// ListMaintenanceTools returns the tools for one PM item. The viewset is
// registered next to maintenance-materials and takes the same
// ?maintenance_item= filter.
func (c *Client) ListMaintenanceTools(ctx context.Context, maintenanceItemID string) (*Page[MaintenanceTool], error) {
	q := url.Values{}
	q.Set("maintenance_item", maintenanceItemID)
	return GetPage[MaintenanceTool](ctx, c, maintenanceToolsPath, q)
}

// CreateMaintenanceTool adds a required tool to a PM item.
func (c *Client) CreateMaintenanceTool(ctx context.Context, body MaintenanceToolWrite) (*MaintenanceTool, error) {
	var out MaintenanceTool
	if err := c.Post(ctx, maintenanceToolsPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMaintenanceTool PATCHes a tool in place, so editing a row keeps its id
// instead of churning a delete+create (same reasoning as the material path).
func (c *Client) UpdateMaintenanceTool(ctx context.Context, id string, body MaintenanceToolWrite) (*MaintenanceTool, error) {
	var out MaintenanceTool
	if err := c.Patch(ctx, maintenanceToolsPath+id+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMaintenanceTool removes a tool.
func (c *Client) DeleteMaintenanceTool(ctx context.Context, id string) error {
	return c.Delete(ctx, maintenanceToolsPath+id+"/")
}

// ---------------------------------------------------------------------------
// Asset problems (reported issues, distinct from scheduled PM items)
// ---------------------------------------------------------------------------

type AssetProblem struct {
	ID            string              `json:"id"`
	Asset         string              `json:"asset"`
	AssetName     string              `json:"asset_name,omitempty"`
	AssetTag      string              `json:"asset_tag,omitempty"`
	ReportedBy    string              `json:"reported_by,omitempty"`
	Description   string              `json:"description"`
	Status        string              `json:"status"`
	AffectedParts []AffectedAssetPart `json:"affected_parts,omitempty"`
	CreatedAt     time.Time           `json:"created_at,omitempty"`
	UpdatedAt     time.Time           `json:"updated_at,omitempty"`
}

// AffectedAssetPart is the compact read-only projection of an AssetPart the
// reporter flagged as needing replace/fix, mirroring the backend's
// AffectedAssetPartSerializer. ID is `any` because an AssetPart uses an integer
// primary key (arriving as a JSON number) — the same shape AssetPart.ID carries.
type AffectedAssetPart struct {
	ID             any    `json:"id"`
	PartName       string `json:"part_name,omitempty"`
	PartSKU        string `json:"part_sku,omitempty"`
	QuantityNeeded int    `json:"quantity_needed,omitempty"`
	IsRequired     bool   `json:"is_required,omitempty"`
}

// AssetProblemCreate is the report-a-problem payload. Description is required;
// PartIds optionally flags which of the asset's AssetParts need attention
// (stringified AssetPart ids — the backend coerces them to ints and validates
// each belongs to the asset). Asset rides in the URL, not the body, and the
// backend stamps reported_by from the authenticated user, so neither is sent.
type AssetProblemCreate struct {
	Asset       string   `json:"-"`
	Description string   `json:"description"`
	PartIds     []string `json:"part_ids,omitempty"`
}

func (c *Client) ListAssetProblems(ctx context.Context, q url.Values) (*Page[AssetProblem], error) {
	return GetPage[AssetProblem](ctx, c, "/api/inventory/asset-problems/", q)
}

// CreateAssetProblem reports a problem against an asset through the asset's
// report_problem action (POST /assets/{id}/report_problem/). The asset-problems
// collection is a read-only viewset; report_problem is the write path the web
// uses too, and the only one that accepts the optional affected part_ids. The
// action takes the asset from the URL and stamps reported_by from the
// authenticated user, so the body is just description + part_ids.
func (c *Client) CreateAssetProblem(ctx context.Context, req AssetProblemCreate) (*AssetProblem, error) {
	var out AssetProblem
	path := "/api/inventory/assets/" + req.Asset + "/report_problem/"
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListAssetParts returns the AssetParts linked to one asset — its consumable /
// replaceable components, each carrying its part's InventoryItem name + SKU.
// Mirrors the web's assetPartsAPI.getByAsset (GET /asset-parts/?asset=<id>).
// The asset-detail report-problem checklist reuses the parts already nested on
// the Asset payload; this stand-alone lister serves callers holding only an id.
func (c *Client) ListAssetParts(ctx context.Context, assetID string) (*Page[AssetPart], error) {
	q := url.Values{}
	q.Set("asset", assetID)
	return GetPage[AssetPart](ctx, c, "/api/inventory/asset-parts/", q)
}
