package omsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// AssetPart CRUD + the mark-replaced lifecycle action.
//
// An AssetPart is the through-row linking an Asset to an InventoryItem it
// consumes or wears (the web "consumable supplies" / "Part Replacement
// Tracking" surface), carrying the replacement-tracking fields. The read
// struct + ListAssetParts live in inventory.go / maintenance.go; this file adds
// the write path (create/edit/delete) and MarkAssetPartReplaced, mirroring the
// web assetPartsAPI (frontend/src/services/api.ts). Backend: a plain
// ModelViewSet at /api/inventory/asset-parts/ with IsAuthenticatedOrReadOnly —
// any authenticated user may write; no staff gate.

const assetPartsPath = "/api/inventory/asset-parts/"

// AssetPartWrite is the create/edit payload for an AssetPart. It mirrors the
// writable AssetPartSerializer set exactly: asset, part, quantity_needed,
// is_required, maintenance_interval_days, notes. (last_replaced_at is also
// technically writable server-side, but the web never edits it as a form field —
// it is driven by the mark_replaced action; MarkAssetPartReplaced is the parity
// path.) The serializer's read-only projections — id, asset_name/asset_tag,
// part_name/part_sku, days_since_replacement, needs_replacement, part_details,
// created_at/updated_at — are never sent.
//
// Nullable/clearable fields carry NO omitempty so a PATCH can clear them:
// MaintenanceIntervalDays marshals to explicit null when nil ("blank = on
// demand"), and Notes sends "" to clear. QuantityNeeded/IsRequired always ride
// so a PATCH sets them deterministically rather than depending on server
// defaults. The unique_together(asset, part) constraint is enforced
// server-side; a collision surfaces as a 400 the caller renders. DRF excludes
// the current row during update, so re-sending an unchanged asset+part on PATCH
// is safe.
type AssetPartWrite struct {
	Asset                   string `json:"asset"`
	Part                    string `json:"part"`
	QuantityNeeded          int    `json:"quantity_needed"`
	IsRequired              bool   `json:"is_required"`
	MaintenanceIntervalDays *int   `json:"maintenance_interval_days"`
	Notes                   string `json:"notes"`
}

// IDString renders the AssetPart's primary key as the decimal string the
// detail/action URLs need. The pk is a Django BigAutoField (integer), which
// JSON-decodes into the any-typed ID field as a float64; going through int64
// avoids fmt's %v switching a larger pk into scientific notation (e.g. 1234567
// → "1.234567e+06"), which would build a 404 URL. Falls back gracefully for the
// string / json.Number shapes a differently-configured serializer might emit.
func (p AssetPart) IDString() string {
	switch v := p.ID.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case json.Number:
		return v.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

// GetAssetPart retrieves one AssetPart by its (stringified) integer pk. The
// edit form uses it to hydrate rather than depending on the caller threading a
// loaded row through.
func (c *Client) GetAssetPart(ctx context.Context, id string) (*AssetPart, error) {
	var out AssetPart
	if err := c.Get(ctx, assetPartsPath+id+"/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAssetPart adds a part/supply row to an asset. asset + part are required;
// the rest default server-side. Mirrors assetPartsAPI.createAssetPart.
func (c *Client) CreateAssetPart(ctx context.Context, body AssetPartWrite) (*AssetPart, error) {
	var out AssetPart
	if err := c.Post(ctx, assetPartsPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAssetPart edits an existing part row (PATCH). The web UI only edits
// quantity/interval/required/notes, but the serializer accepts the full write
// set, so a full-struct PATCH is safe and lets the TUI also re-point the part.
func (c *Client) UpdateAssetPart(ctx context.Context, id string, body AssetPartWrite) (*AssetPart, error) {
	var out AssetPart
	if err := c.Patch(ctx, assetPartsPath+id+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAssetPart removes a part row (hard delete → 204). Both reverse
// references (AssetProblem.part FK, AssetProblem.affected_parts M2M) are
// SET_NULL / join-row removals, so delete never FK-409s — no pre-check needed.
func (c *Client) DeleteAssetPart(ctx context.Context, id string) error {
	return c.Delete(ctx, assetPartsPath+id+"/")
}

// MarkAssetPartReplaced fires the mark_replaced action (POST, no body). The
// backend stamps last_replaced_at = now and returns the refreshed part
// (days_since_replacement → 0, needs_replacement → false). NOTE the URL segment
// is an underscore — mark_replaced — the DRF default url_path for the action
// method; a kebab-case mark-replaced would 404.
func (c *Client) MarkAssetPartReplaced(ctx context.Context, id string) (*AssetPart, error) {
	var out AssetPart
	if err := c.Post(ctx, assetPartsPath+id+"/mark_replaced/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
