// Kits — purchasable SKUs that decompose into component stock on receipt
// (OMS op-8n0).
//
// A kit IS an InventoryItem carrying `is_kit=True`; it has no table of its own.
// That single fact decides the whole shape of this file, and of every ScanTTY
// screen that reads it, so it is worth stating what the API actually does before
// anything here is changed:
//
//	GET  /api/inventory/items/            EXCLUDES kits by default. `?is_kit=true`
//	                                      returns only kits, `?include_kits=true`
//	                                      returns both. The filter runs in
//	                                      get_queryset, so it applies to the
//	                                      DETAIL route too: GET (and PATCH) of a
//	                                      kit's id under /items/ is a 404 without
//	                                      one of those params. GetItem therefore
//	                                      sends include_kits=true — see
//	                                      inventory.go.
//	GET  /api/inventory/items/{id}/       InventoryItemSerializer /
//	                                      InventoryItemDetailSerializer. NEITHER
//	                                      exposes `is_kit`, `components` or
//	                                      `component_count`: the kit fields live
//	                                      ONLY on KitSerializer. So an item
//	                                      payload cannot say whether it is a kit,
//	                                      and the only way to ask is to fetch the
//	                                      id from /kits/ — which is exactly what
//	                                      GetKit is for, and why a 404 from it is
//	                                      the ANSWER ("not a kit") rather than an
//	                                      error (see IsNotKit).
//	GET  /api/inventory/kits/{id}/        KitSerializer: the whole item field set
//	                                      PLUS is_kit / components /
//	                                      component_count. 404 for a non-kit id.
//	PATCH /api/inventory/kits/{id}/       Nested-WRITABLE `components`: the bill
//	                                      of materials is replaced wholesale in
//	                                      the same request that saves the item
//	                                      fields, upserted on `component`. There
//	                                      is deliberately no /kit-components/
//	                                      endpoint — this is the only write path
//	                                      a component has.
//	GET  /api/inventory/items/{id}/kits/  KitSummarySerializer: "which kits
//	                                      supply this component, and how many of
//	                                      it does one kit hold". Empty for a kit
//	                                      (kits cannot contain kits).
//
// Source of truth: backend/inventory/serializers.py (KitComponentSerializer,
// KitSerializer, KitSummarySerializer), backend/inventory/views.py
// (KitViewSet, InventoryItemViewSet.get_queryset / .kits) and
// backend/inventory/urls.py.
package omsapi

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// KitComponent is one line of a kit's bill of materials: which item one kit
// contains and how many of it.
//
// Quantity is PER KIT, never multiplied out — receiving N kits credits
// N × Quantity of Component. The `component_*` fields are read-only accessors
// the serializer nests off the component item so a component row renders
// without a second fetch; Component itself is the InventoryItem UUID and is the
// only one of them that is written back.
//
// ComponentStock is a pointer because 0 is a real reading ("none on the shelf")
// and has to stay distinguishable from a backend that did not send the field at
// all — the same rule the metrics row follows.
type KitComponent struct {
	ID                    int    `json:"id,omitempty"`
	Component             string `json:"component"`
	ComponentName         string `json:"component_name,omitempty"`
	ComponentSKU          string `json:"component_sku,omitempty"`
	ComponentStock        *int   `json:"component_current_stock,omitempty"`
	ComponentNeedsReorder bool   `json:"component_needs_reorder,omitempty"`
	Quantity              int    `json:"quantity"`
	Notes                 string `json:"notes,omitempty"`
}

// Kit is a kit SKU and its bill of materials.
//
// It EMBEDS Item rather than redeclaring the catalog fields, mirroring
// KitSerializer subclassing InventoryItemSerializer: a kit is an inventory item
// with `is_kit` set, and every name / SKU / category / supplier field on it
// means exactly what it means on any other item. Embedding also means a screen
// that already renders an Item renders a kit's header unchanged — the kit-only
// fields are the three below.
//
// IsKit is always true on anything this endpoint returns (the queryset filters
// on it), so it is not what a caller tests; it is carried because the API sends
// it and a payload that ever stopped saying so is worth being able to see.
type Kit struct {
	Item

	IsKit          bool           `json:"is_kit"`
	Components     []KitComponent `json:"components,omitempty"`
	ComponentCount int            `json:"component_count,omitempty"`
}

// KitComponentWrite is one row of the nested `components` payload.
//
// The row pk is deliberately absent: the serializer upserts on
// (kit, component), so a component that survives an edit keeps its pk without
// the client having to track one — the same reasoning as PackagingLevelWrite's
// missing pk. Quantity must be at least 1 (the serializer rejects 0, which
// would credit nothing on receipt).
type KitComponentWrite struct {
	Component string `json:"component"`
	Quantity  int    `json:"quantity"`
	Notes     string `json:"notes,omitempty"`
}

// KitWrite is the body of a kit save: every ordinary item field, plus the bill
// of materials.
//
// Components REPLACES the whole bill of materials. nil omits the key and leaves
// the stored components alone — which is what a save that only touched the item
// fields must do, because sending an empty list is a validation error ("A kit
// must contain at least one component"), not a no-op.
type KitWrite struct {
	ItemWrite

	Components *[]KitComponentWrite `json:"components,omitempty"`
}

// KitSummary is the compact "this component comes in these kits" row behind
// GET /api/inventory/items/{id}/kits/.
//
// QuantityInKit is how many of the ASKED-ABOUT component one of this kit holds;
// it is a pointer because the serializer returns null when the view did not say
// which component was being asked about, and a null must not read as zero.
type KitSummary struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	SKU            string        `json:"sku,omitempty"`
	IsActive       bool          `json:"is_active"`
	QuantityInKit  *int          `json:"quantity_in_kit"`
	SupplierName   string        `json:"supplier_name,omitempty"`
	SupplierSKU    string        `json:"supplier_sku,omitempty"`
	UnitCost       DecimalString `json:"unit_cost,omitempty"`
	ComponentCount int           `json:"component_count,omitempty"`
}

// GetKit fetches one kit with its bill of materials.
//
// A 404 here is the API's way of saying "that id is not a kit" — the queryset
// is filtered to is_kit=True — so callers asking "is this a kit?" pair this
// with IsNotKit rather than treating the error as a failure.
func (c *Client) GetKit(ctx context.Context, id string) (*Kit, error) {
	var out Kit
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/kits/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListKits pages the kit catalog (GET /api/inventory/kits/). Supported filters
// mirror the viewset: search, is_active, supplier, component, ordering.
func (c *Client) ListKits(ctx context.Context, q url.Values) (*Page[Kit], error) {
	return GetPage[Kit](ctx, c, "/api/inventory/kits/", q)
}

// UpdateKit PATCHes a kit — its item fields and, when body.Components is set,
// its whole bill of materials in the same request.
//
// PATCH (not PUT) and PATCH to /kits/ (not /items/) are both load-bearing:
// /items/ excludes kits from its queryset, so the ordinary item update is a 404
// for a kit id, and `components` is not a field on the item serializer at all.
func (c *Client) UpdateKit(ctx context.Context, id string, body KitWrite) (*Kit, error) {
	var out Kit
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/kits/%s/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListItemKits lists the kits that CONTAIN this item, with how many of it each
// one holds (GET /api/inventory/items/{id}/kits/).
//
// The action returns a bare array rather than a paginated envelope, so this
// decodes into a slice directly. It returns [] for an item nothing contains and
// for a kit itself — nested kits are out of scope upstream, so a kit is never a
// component.
func (c *Client) ListItemKits(ctx context.Context, itemID string) ([]KitSummary, error) {
	var out []KitSummary
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%s/kits/", itemID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// IsNotKit reports whether err is the 404 that GetKit returns for an ordinary
// item — i.e. whether the answer to "is this a kit?" is a plain no.
//
// This exists because the item serializer does not carry `is_kit` (see the file
// comment): the ONLY way a client can ask is to fetch the id from /kits/ and
// read the status. Anything else — a network failure, a 500, an auth error — is
// a real error, and a screen must not silently render "not a kit" for it, which
// is why this narrows to NotFound rather than treating every error as "no".
func IsNotKit(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.IsNotFound()
}
