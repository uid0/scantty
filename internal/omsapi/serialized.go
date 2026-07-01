package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// SerializedComponent is one serial-numbered physical unit of a serialized
// InventoryItem, mirroring backend inventory.SerializedComponent. Aggregate
// stock lives on the item; this row tracks a single unit through its
// lifecycle (received → in_stock → installed → …) branched on the owning
// item's tracking_mode.
//
// status, the lifecycle timestamps, disposal_reason and installed_in_asset
// are all read-only on the wire — they change only through the lifecycle
// action endpoints (Receive/Install/Remove/Consume/Retire/Dispose), never a
// direct PATCH. available_actions is computed server-side from the current
// status + tracking mode, so the TUI can offer exactly the legal actions
// without duplicating the transition table.
type SerializedComponent struct {
	ID               string   `json:"id"`
	Item             string   `json:"item"`
	ItemName         string   `json:"item_name,omitempty"`
	ItemSKU          string   `json:"item_sku,omitempty"`
	SerialNumber     string   `json:"serial_number"`
	Lot              string   `json:"lot,omitempty"`
	Status           string   `json:"status,omitempty"`
	StatusDisplay    string   `json:"status_display,omitempty"`
	TrackingMode     string   `json:"tracking_mode,omitempty"`
	AvailableActions []string `json:"available_actions,omitempty"`
	// InstalledInAsset is the asset UUID this unit is currently installed
	// in, or "" when not installed. InstalledInAssetName is its display name.
	InstalledInAsset            string     `json:"installed_in_asset,omitempty"`
	InstalledInAssetName        string     `json:"installed_in_asset_name,omitempty"`
	ReceivedAt                  *time.Time `json:"received_at,omitempty"`
	InstalledAt                 *time.Time `json:"installed_at,omitempty"`
	DisposedAt                  *time.Time `json:"disposed_at,omitempty"`
	ProvenanceDeliveryItem      any        `json:"provenance_delivery_item,omitempty"`
	ProvenancePurchaseOrderItem any        `json:"provenance_purchase_order_item,omitempty"`
	DisposalReason              string     `json:"disposal_reason,omitempty"`
	CreatedAt                   time.Time  `json:"created_at,omitempty"`
	UpdatedAt                   time.Time  `json:"updated_at,omitempty"`
}

// Serialized-component lifecycle status + action constants, mirroring the
// backend inventory.SerializedComponent choices. Kept here so TUI code can
// switch on them without stringly-typed literals scattered around.
const (
	SerialStatusReceived  = "received"
	SerialStatusInStock   = "in_stock"
	SerialStatusInstalled = "installed"
	SerialStatusRemoved   = "removed"
	SerialStatusConsumed  = "consumed"
	SerialStatusRetired   = "retired"
	SerialStatusDisposed  = "disposed"

	SerialActionReceive = "receive"
	SerialActionInstall = "install"
	SerialActionRemove  = "remove"
	SerialActionConsume = "consume"
	SerialActionRetire  = "retire"
	SerialActionDispose = "dispose"

	SerialTrackingConsumable = "consumable"
	SerialTrackingReusable   = "reusable"
)

// ComponentUsageEvent is one immutable audit-log entry recording a lifecycle
// action on a serialized component. Written server-side as a side effect of
// each transition; the API surface is read-only.
type ComponentUsageEvent struct {
	ID            string     `json:"id"`
	Component     string     `json:"component"`
	Asset         any        `json:"asset,omitempty"`
	AssetName     string     `json:"asset_name,omitempty"`
	Action        string     `json:"action"`
	ActionDisplay string     `json:"action_display,omitempty"`
	At            *time.Time `json:"at,omitempty"`
	Actor         any        `json:"actor,omitempty"`
	ActorUsername string     `json:"actor_username,omitempty"`
	Notes         string     `json:"notes,omitempty"`
	CreatedAt     time.Time  `json:"created_at,omitempty"`
}

// SerializedComponentCreate is the create payload. Only item + serial_number
// are required; lot and the two provenance links are optional. A freshly
// created unit starts in status "received"; the receive lifecycle action then
// accessions it into stock.
type SerializedComponentCreate struct {
	Item                        string `json:"item"`
	SerialNumber                string `json:"serial_number"`
	Lot                         string `json:"lot,omitempty"`
	ProvenanceDeliveryItem      any    `json:"provenance_delivery_item,omitempty"`
	ProvenancePurchaseOrderItem any    `json:"provenance_purchase_order_item,omitempty"`
}

// SerializedComponentAction is the body for a lifecycle action POST. Asset is
// required by install (the target asset UUID); DisposalReason is required by
// dispose; Notes is always optional and lands on the recorded usage event.
type SerializedComponentAction struct {
	Asset          string `json:"asset,omitempty"`
	DisposalReason string `json:"disposal_reason,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

// SerializedComponentActionResult is the response from a lifecycle action:
// the updated component plus the usage event the transition recorded. The
// component fields are promoted from the embedded struct so callers can read
// the new Status/InstalledInAsset directly off the result.
type SerializedComponentActionResult struct {
	SerializedComponent
	Event *ComponentUsageEvent `json:"event,omitempty"`
}

// ComponentForecastRow is one row of the serialized-component consumption
// forecast (GET reports/inventory/serialized_forecast/). DaysUntilStockout
// and LeadTimeDays are null when undefined (no depletion observed / no lead
// time known), so they're pointers. ProjectedStockoutDate is a bare
// YYYY-MM-DD string ("" when null).
type ComponentForecastRow struct {
	ItemID                string   `json:"item_id"`
	ItemName              string   `json:"item_name"`
	SKU                   string   `json:"sku,omitempty"`
	CategoryName          string   `json:"category_name,omitempty"`
	SerialTrackingMode    string   `json:"serial_tracking_mode,omitempty"`
	AvailableStock        int      `json:"available_stock"`
	CurrentStock          int      `json:"current_stock"`
	WindowDays            int      `json:"window_days"`
	UnitsDepletedInWindow int      `json:"units_depleted_in_window"`
	AvgDailyUse           float64  `json:"avg_daily_use"`
	DaysUntilStockout     *float64 `json:"days_until_stockout"`
	ProjectedStockoutDate string   `json:"projected_stockout_date,omitempty"`
	LeadTimeDays          *float64 `json:"lead_time_days"`
	SafetyStock           int      `json:"safety_stock"`
	ReorderPoint          int      `json:"reorder_point"`
	NeedsReorder          bool     `json:"needs_reorder"`
}

const (
	serializedComponentsPath = "/api/inventory/serialized-components/"
	componentUsageEventsPath = "/api/inventory/component-usage-events/"
	serializedForecastPath   = "/api/inventory/reports/inventory/serialized_forecast/"
)

// ListSerializedComponents returns the serial-numbered units matching the
// given filters. Supported query keys: item, status, installed_in_asset.
// Tolerates both the paginated envelope and a bare array.
func (c *Client) ListSerializedComponents(ctx context.Context, q url.Values) ([]SerializedComponent, error) {
	var out MaybeList[SerializedComponent]
	if err := c.Get(ctx, serializedComponentsPath, q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetSerializedComponent fetches a single unit by id.
func (c *Client) GetSerializedComponent(ctx context.Context, id string) (*SerializedComponent, error) {
	var out SerializedComponent
	if err := c.Get(ctx, serializedComponentsPath+id+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateSerializedComponent accessions a new serial-numbered unit. The unit
// starts in status "received"; callers that want it on the shelf immediately
// follow up with SerializedComponentAction(..., SerialActionReceive, ...).
func (c *Client) CreateSerializedComponent(ctx context.Context, req SerializedComponentCreate) (*SerializedComponent, error) {
	var out SerializedComponent
	if err := c.Post(ctx, serializedComponentsPath, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SerializedComponentAction applies one lifecycle transition (receive,
// install, remove, consume, retire, dispose) to a unit and returns the
// updated unit plus the recorded usage event. install requires
// req.Asset; dispose requires req.DisposalReason — the backend returns a 400
// otherwise.
func (c *Client) SerializedComponentAction(
	ctx context.Context, id, action string, req SerializedComponentAction,
) (*SerializedComponentActionResult, error) {
	var out SerializedComponentActionResult
	path := fmt.Sprintf("%s%s/%s/", serializedComponentsPath, id, action)
	if err := c.Post(ctx, path, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListComponentUsageEvents returns the audit log, most useful filtered by a
// single component (query key: component). Tolerates envelope or bare array.
func (c *Client) ListComponentUsageEvents(ctx context.Context, q url.Values) ([]ComponentUsageEvent, error) {
	var out MaybeList[ComponentUsageEvent]
	if err := c.Get(ctx, componentUsageEventsPath, q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// SerializedForecast returns the consumption forecast + low-stock report for
// serialized items. Query keys: window_days (trailing depletion window, default
// 90 server-side) and low_stock_only (truthy to return only items at/below
// their reorder point). Rows arrive pre-sorted most-urgent-first.
func (c *Client) SerializedForecast(ctx context.Context, q url.Values) ([]ComponentForecastRow, error) {
	var out MaybeList[ComponentForecastRow]
	if err := c.Get(ctx, serializedForecastPath, q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
