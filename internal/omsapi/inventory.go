package omsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LookupResult is the unified shape OMS returns from the scanner dispatch
// endpoint. We map the new `target_*` field names back to Type/ID/Name so
// the TUI code that read the legacy /api/inventory/lookup-code/ response
// keeps working without per-call-site changes.
//
// Source: backend/scanner/resolvers.py::ResolvedScan.to_dict().
type LookupResult struct {
	Action     string `json:"action"`
	Type       string `json:"target_type"`
	ID         any    `json:"target_id"`
	Name       string `json:"target_name"`
	TargetURL  string `json:"target_url,omitempty"`
	Message    string `json:"message,omitempty"`
	RawPayload string `json:"raw_payload,omitempty"`
}

func (c *Client) LookupCode(ctx context.Context, code string) (*LookupResult, error) {
	if code == "" {
		return nil, &APIError{Code: "invalid_code", Message: "code is empty"}
	}
	// The old /api/inventory/lookup-code/ endpoint was removed when OMS
	// dropped the access_code feature. The replacement is the scanner
	// dispatch endpoint which takes a POST body and runs the same
	// resolver chain (items by SKU, assets by tag, locations by
	// access_code, etc).
	body := map[string]string{"payload": strings.ToUpper(code)}
	var out LookupResult
	if err := c.Post(ctx, "/api/scanner/dispatch/", body, &out); err != nil {
		return nil, err
	}
	// The dispatcher returns action="unknown" (with no target_*) when
	// nothing matched. Surface that to the caller as no-match so it
	// renders alongside the other "(no match)" rows instead of an OK
	// row with empty fields.
	if out.Action == "unknown" || out.Type == "" {
		return nil, nil
	}
	return &out, nil
}

type Item struct {
	ID                   string        `json:"id"`
	Name                 string        `json:"name"`
	SKU                  string        `json:"sku"`
	Description          string        `json:"description,omitempty"`
	Category             *int          `json:"category,omitempty"`
	CategoryName         string        `json:"category_name,omitempty"`
	Location             string        `json:"location,omitempty"`
	Stock                int           `json:"current_stock"`
	MinimumStock         int           `json:"minimum_stock,omitempty"`
	ReorderQuantity      int           `json:"reorder_quantity,omitempty"`
	NeedsReorder         bool          `json:"needs_reorder,omitempty"`
	ReorderStatus        string        `json:"reorder_status,omitempty"`
	HasPendingReorder    bool          `json:"has_pending_reorder,omitempty"`
	ExpectedDeliveryDate string        `json:"expected_delivery_date,omitempty"`
	SupplierName         string        `json:"supplier_name,omitempty"`
	SupplierSKU          string        `json:"supplier_sku,omitempty"`
	SupplierURL          string        `json:"supplier_url,omitempty"`
	UnitCost             DecimalString `json:"unit_cost,omitempty"`
	PackageCost          DecimalString `json:"package_cost,omitempty"`
	QuantityPerPackage   int           `json:"quantity_per_package,omitempty"`
	AverageLeadTime      float64       `json:"average_lead_time,omitempty"`
	// AverageLeadTimeSource is where AverageLeadTime came from, off the same
	// primary link (see LeadTimeSource). "" when unserved or null.
	AverageLeadTimeSource LeadTimeSource `json:"average_lead_time_source,omitempty"`
	TotalValue            DecimalString  `json:"total_value,omitempty"`
	ThumbnailURL          string         `json:"thumbnail,omitempty"`
	QRCodeURL             string         `json:"qr_code_url,omitempty"`
	UseCaseBasedReorder   bool           `json:"use_case_based_reorder,omitempty"`
	MinimumCases          *float64       `json:"minimum_cases,omitempty"`
	ReorderCases          *float64       `json:"reorder_cases,omitempty"`
	CurrentCases          *float64       `json:"current_cases,omitempty"`
	ReorderInstruction    string         `json:"reorder_instruction,omitempty"`

	// ReorderAlertsEnabled is the per-item opt-in for ML reorder alerts (op-1):
	// the "watch this item" toggle that puts it into the reorder_alerts notify
	// set (see DemandForecastRow). Defaults false on the model; read+write on
	// the item serializer.
	ReorderAlertsEnabled bool `json:"reorder_alerts_enabled,omitempty"`

	// Cycle-count / physical-count tracking (issue-7). The item serializer
	// exposes the last reconciliation timestamp and a precomputed age in days.
	// Both are pointers: null until the item has ever been counted, and absent
	// on a backend that predates the fields — so the detail degrades to
	// "Counted: never" instead of showing a misleading zero.
	LastCountedAt      *time.Time `json:"last_counted_at,omitempty"`
	DaysSinceLastCount *int       `json:"days_since_last_count,omitempty"`

	// Unit of measure / packaging matrix (OMS #979/#980/#981). Every field here
	// is additive and opt-in: an item with no PackagingLevels and CountMode
	// "each" — which is every item until someone opts it in — counts individual
	// base units exactly as it always has.
	//
	// CurrentStock (Stock, above) stays the canonical BASE-unit quantity that
	// every reorder / purchase / usage flow reads; these fields only say what a
	// base unit is called and at what granularity the item is counted.
	//
	// CountLevel is the pk of the PackagingLevel the item is counted in —
	// required for the two pack-counting modes, and necessarily null for "each"
	// (the backend rejects the other combinations). OpenContainerCount is the
	// number of currently-OPEN packs, meaningful only in CountModeOpenClosed.
	BaseUnit           string `json:"base_unit,omitempty"`
	CountMode          string `json:"count_mode,omitempty"`
	CountLevel         *int   `json:"count_level,omitempty"`
	OpenContainerCount int    `json:"open_container_count,omitempty"`

	// PackagingLevels is the item's pack chain, outermost rung first
	// (sort_order 0 = largest). Nested-writable on the item serializer, so the
	// item form saves the whole chain in one request — see
	// ItemWrite.PackagingLevels.
	PackagingLevels []PackagingLevel `json:"packaging_levels,omitempty"`

	// OnHandDisplay renders Stock at the item's counting granularity ("4
	// cases" / "3 sealed + 1 open") without changing it. Server-computed and
	// read-only; nil on a backend that predates the packaging matrix, in which
	// case the caller falls back to the bare base-unit number.
	OnHandDisplay *OnHandDisplay `json:"on_hand_display,omitempty"`

	// IsSerialized marks an item whose stock is tracked as individual
	// serial-numbered units (SerializedComponent). SerialTrackingMode is
	// "consumable" or "reusable" and drives which lifecycle transitions are
	// legal on those units.
	IsSerialized       bool   `json:"is_serialized,omitempty"`
	SerialTrackingMode string `json:"serial_tracking_mode,omitempty"`

	// SerializedStock is the display-only unit split for a serialized item
	// (op-0cd2): available / on-hand / installed. Present only on the item
	// DETAIL serializer (GetItem) and only for serialized items — nil for
	// non-serialized items and on a backend that predates the field, so the
	// item-instances header simply omits it.
	SerializedStock *SerializedStock `json:"serialized_stock,omitempty"`

	// Hazmat block. Mirrors the web item form's "Hazardous Materials"
	// section so the edit screen can hydrate every hazmat field. The NFPA
	// ratings are pointers because 0 is a meaningful rating distinct from
	// "unset" — the backend serializes them as null when never assigned.
	IsHazardous           bool   `json:"is_hazardous,omitempty"`
	MSDSURL               string `json:"msds_url,omitempty"`
	NFPAHealthHazard      *int   `json:"nfpa_health_hazard,omitempty"`
	NFPAFireHazard        *int   `json:"nfpa_fire_hazard,omitempty"`
	NFPAInstabilityHazard *int   `json:"nfpa_instability_hazard,omitempty"`
	NFPASpecialHazards    string `json:"nfpa_special_hazards,omitempty"`

	// Ownership + lifecycle fields the item form round-trips.
	OwnershipType string `json:"ownership_type,omitempty"`
	OwningUser    *int   `json:"owning_user,omitempty"`
	OwningGroup   *int   `json:"owning_group,omitempty"`
	IsActive      bool   `json:"is_active,omitempty"`
	// IsRetired marks a phased-out item (op-jv7r): never flagged for reorder
	// and auto-hidden from the default list once its stock hits 0 (retired
	// items with stock remaining stay listed so the stock is drawn down).
	// Include retired-and-empty items in a list with ?include_retired=true.
	IsRetired bool   `json:"is_retired,omitempty"`
	Notes     string `json:"notes,omitempty"`
	Image     string `json:"image,omitempty"`

	Suppliers []ItemSupplier `json:"suppliers,omitempty"`
	Tags      []string       `json:"tags,omitempty"`

	// Metrics is the per-item stock/cost snapshot, embedded ONLY when the list is
	// fetched with ?with_metrics=1 (ListItemsWithMetrics) — the inventory list
	// renders it as the Q's & Costs row. Nil on the plain item serializer and on
	// a backend that predates the param, so the list degrades to a SKU/stock
	// subtitle. Same shape as the standalone GetItemMetrics endpoint.
	Metrics *ItemMetrics `json:"metrics,omitempty"`

	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// Item count modes — how much of an item's packaging a physical count bothers
// with (OMS #979). Mirrors InventoryItem.CountMode on the backend.
const (
	// CountModeEach counts individual base units: today's behaviour, and the
	// default every existing item carries.
	CountModeEach = "each"
	// CountModeByLevel counts whole packs of the item's CountLevel — "4 cases",
	// "5 reams" — and never audits below that rung.
	CountModeByLevel = "by_level"
	// CountModeOpenClosed counts SEALED packs plus a tally of how many are
	// open; an open pack's remaining contents are deliberately not counted.
	CountModeOpenClosed = "open_closed"
)

// PackagingLevel is one rung of an item's packaging chain (OMS #979): how many
// BASE units one of this rung holds. SortOrder 0 is the outermost/largest rung
// and increases toward the base rung, which holds exactly 1. Expressing every
// rung in base units (rather than "per parent") is what makes all conversions
// unambiguous; PerParent is the derived "1 case = 10 reams" ratio the server
// computes by dividing adjacent rungs.
//
// PerParent is nil for the base rung, which has nothing below it, and is a
// float because a chain is only required to SHRINK, not to divide evenly.
type PackagingLevel struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	SortOrder int      `json:"sort_order"`
	BaseUnits int      `json:"base_units"`
	PerParent *float64 `json:"per_parent,omitempty"`
}

// OnHandDisplay is the server's rendering of an item's on-hand quantity at the
// granularity it is counted in (OMS #979). Mode selects which of the other
// fields are populated, so read it first:
//
//   - CountModeEach → Unit + BaseUnits ("12 sheets")
//   - CountModeByLevel → Level + LevelCount + RemainderBase ("4 case(s)"); the
//     leftover base units are reported but deliberately NOT presented as
//     countable, which is the whole point of the mode.
//   - CountModeOpenClosed → Level + Sealed + Open ("3 sealed + 1 open")
//
// Text is the ready-to-render string for all three. A pack-counting item with
// no usable CountLevel falls back to the "each" shape server-side rather than
// erroring, so a half-configured item still renders.
type OnHandDisplay struct {
	Mode string `json:"mode"`
	Text string `json:"text"`

	// each
	Unit      string `json:"unit,omitempty"`
	BaseUnits int    `json:"base_units,omitempty"`

	// by_level / open_closed
	Level string `json:"level,omitempty"`

	// by_level
	LevelCount    int `json:"level_count,omitempty"`
	RemainderBase int `json:"remainder_base,omitempty"`

	// open_closed
	Sealed int `json:"sealed,omitempty"`
	Open   int `json:"open,omitempty"`
}

// SerializedStock is the per-item serialized unit split (op-0cd2), surfaced on
// the item DETAIL serializer under "serialized_stock" for serialized items.
// OnHand is every physically-present (not-yet-depleted) unit; Installed is the
// subset currently installed in an asset; Available = OnHand − Installed (the
// count actually on the shelf). Display-only — it does not touch the aggregate
// current_stock / generic reorder path.
type SerializedStock struct {
	Available int `json:"available"`
	OnHand    int `json:"on_hand"`
	Installed int `json:"installed"`
}

func (c *Client) ListItems(ctx context.Context, q url.Values) (*Page[Item], error) {
	return GetPage[Item](ctx, c, "/api/inventory/items/", q)
}

// ListItemsWithMetrics fetches the item list with each item's metrics snapshot
// embedded (?with_metrics=1), so the inventory list can render the per-item
// Q's & Costs row without an N+1 fan-out of GetItemMetrics calls. A backend that
// predates the param simply ignores it and every Item.Metrics decodes to nil —
// the list then falls back to the plain SKU/stock subtitle.
//
// It also passes ?include_retired=true (op-jv7r) so the warden's management list
// keeps retired items visible — including retired-and-empty ones the default
// list would hide — each rendered with a (retired) tag. The backend matches the
// literal string "true" (case-insensitive); an older backend ignores it.
//
// q carries the list's own params — `page`, `search`, `low_stock`, and an
// explicit `include_retired` — and is merged over those two defaults rather
// than replacing them. include_retired is a DEFAULT and not a constant: the
// server hides retired-and-empty items for any value but "true"
// (InventoryItemViewSet.get_queryset), so a caller asking for the web's
// "Hide Retired" view sends `include_retired=false` and gets exactly the rows
// the web list opens on. with_metrics is not overridable; nothing asks for a
// list without it. q is not modified.
func (c *Client) ListItemsWithMetrics(ctx context.Context, q url.Values) (*Page[Item], error) {
	params := url.Values{}
	for k, v := range q {
		params[k] = append([]string(nil), v...)
	}
	params.Set("with_metrics", "1")
	if _, ok := params["include_retired"]; !ok {
		params.Set("include_retired", "true")
	}
	return c.ListItems(ctx, params)
}

// ListAllItems pages through every inventory item. Pickers that must be able to
// reach any item — the asset-part form's "part" FK, which is required — need the
// full set up front: truncating to page 1 (as one ListItems call does) would make an
// item beyond the first page unselectable, and on edit would drop a linked part
// that lives on a later page. Mirrors ListAllAssets; item counts are bounded per
// install, so the extra pages are cheap.
func (c *Client) ListAllItems(ctx context.Context) ([]Item, error) {
	var all []Item
	if err := IterPages[Item](ctx, c, "/api/inventory/items/", nil, func(batch []Item) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// includeKitsQuery / includeKitsValues are the one param that lets a kit's id
// resolve on an /api/inventory/items/{id}/ route at all (op-8n0).
//
// Two spellings of the same thing because the client has two shapes: Get takes
// url.Values, while Post/Patch/Delete take no query argument at all and have to
// carry it as a path suffix. The reason is identical at every call site:
// InventoryItemViewSet.get_queryset excludes kits, get_object filters through
// it, so the filter reaches EVERY detail route on the viewset — the record
// itself, its actions, and its sub-resources — and without this param a kit id
// is a flat 404 on all of them. An older backend ignores the unknown param and
// a non-kit id is unaffected, so the rule is simply: every detail route a kit
// can legitimately reach sends it.
//
// Two deliberate exceptions, and both are exceptions on purpose:
//
//   - cycle-count, log_usage and pack-container do NOT send it. All three are
//     meaningless for a kit — a kit carries no stock by construction, and packs
//     are a way of counting stock — and the first is worse than meaningless,
//     because the backend writes stock through save(update_fields=…) without
//     full_clean(), so the model's own "a kit cannot carry stock" check never
//     runs and the number would PERSIST as one nothing can ever draw down. The
//     item detail screen hides all three keys for a kit instead
//     (internal/tui/inventory_detail.go).
//   - ListItemKits ("which kits contain this item?") does not send it either,
//     for the opposite reason: a kit is never a component, so its 404 there is
//     the right answer rather than a gap. See kits.go, which owns that note.
const includeKitsQuery = "?include_kits=true"

func includeKitsValues() url.Values { return url.Values{"include_kits": []string{"true"}} }

// GetItem fetches one item's detail record.
//
// ?include_kits=true is NOT optional bookkeeping (op-8n0): InventoryItemViewSet
// filters kits out of its queryset by default, and that filter runs in
// get_queryset — so it applies to the DETAIL route as well, and a kit's id
// under /items/ is a flat 404 without it. Since a kit is reachable here (a
// scanned kit resolves to an inventory-item target like any other item), a
// detail screen that could not load one would simply report "not found" for a
// record that exists. An older backend ignores the unknown param, and for a
// non-kit id the param changes nothing at all.
//
// It does not, however, make the response say whether the item IS a kit —
// neither item serializer carries `is_kit`. That question is GetKit's; see
// kits.go.
func (c *Client) GetItem(ctx context.Context, id string) (*Item, error) {
	var out Item
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%s/", id), includeKitsValues(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ScanItem(ctx context.Context, id string) (*Item, error) {
	var out Item
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/items/%s/scan/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CommittedBreakdownEntry is one work order holding part of an item's committed
// quantity (op-l4i0) — the attribution behind ItemMetrics.QuantityCommitted:
// which job, and so which machine, the reserved stock is going to. Entries sum
// to QuantityCommitted and arrive oldest work order first.
//
// AssetID/AssetName are empty on an asset-less work order (the backend sends
// null for both). Quantity is a backend FloatField, so it can be fractional.
type CommittedBreakdownEntry struct {
	WorkOrderID      string  `json:"work_order_id"`
	WorkOrderShortID string  `json:"work_order_short_id"` // e.g. "WO-1A2B3C4D"
	AssetID          string  `json:"asset_id"`
	AssetName        string  `json:"asset_name"`
	Quantity         float64 `json:"quantity"`
}

// ItemMetrics is the aggregate stock/costing snapshot the item-detail metrics
// row renders (issue-5). It comes from a dedicated endpoint
// (GET /api/inventory/items/{id}/metrics/) rather than the item serializer, so
// the detail can surface live on-order / in-transit / committed figures
// without bloating every item-list payload with them.
//
// The count fields are pointers on purpose: a null from the backend renders as
// "-" instead of a misleading 0, and a field the endpoint omits (or an older
// backend that lacks the endpoint entirely) decodes to nil rather than
// failing the whole row. Money rides DecimalString (accepts the string- or
// number-shaped serializer output, null → empty). CostTrend is one of
// "up" | "down" | "flat" | "no_history" and drives the ↑/↓ arrow beside Cost.
type ItemMetrics struct {
	CurrentStock      *int          `json:"current_stock"`       // QOH — quantity on hand
	QuantityOnOrder   *int          `json:"quantity_on_order"`   // QOO — on open POs
	QuantityAvailable *float64      `json:"quantity_available"`  // QA  — on hand minus committed (backend FloatField)
	QuantityCommitted *float64      `json:"quantity_committed"`  // QC  — reserved (backend FloatField)
	QuantityInTransit *int          `json:"quantity_in_transit"` // QIT — shipped, not received
	ReorderPoint      *int          `json:"reorder_point"`       // RP
	LeadTimeDays      *float64      `json:"lead_time_days"`      // Lead — days (may be fractional avg)
	UnitCost          DecimalString `json:"unit_cost"`           // Cost — item or case cost
	CostTrend         string        `json:"cost_trend"`          // up | down | flat | no_history
	LastPOUnitCost    DecimalString `json:"last_po_unit_cost"`
	IsCaseBased       bool          `json:"is_case_based"`
	CaseSize          *int          `json:"case_size"`

	// CommittedBreakdown attributes QC to the work orders holding it (op-l4i0),
	// summing to QuantityCommitted. Nil on a backend that predates the field, so
	// the detail simply shows the QC number with no "Committed to" list.
	CommittedBreakdown []CommittedBreakdownEntry `json:"committed_breakdown"`
}

// GetItemMetrics fetches the item-detail metrics snapshot (issue-5). The
// trailing slash is the canonical DRF path; the item detail treats a failure
// here as non-fatal (an older backend without the endpoint simply hides the
// metrics row) — see the TUI caller.
//
// include_kits because this is a DETAIL ACTION on the item viewset, so it
// resolves through the same kit-excluding get_queryset the record itself does
// (op-8n0). A kit is reachable on the item detail screen, and the non-fatal
// handling is what would have hidden the failure: the metrics row would simply
// have gone missing for every kit, with nothing on screen to say why.
func (c *Client) GetItemMetrics(ctx context.Context, id string) (*ItemMetrics, error) {
	var out ItemMetrics
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%s/metrics/", id), includeKitsValues(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ItemOrderCost is one purchase-order line for an item (op-96uo): what a single
// order was placed at, per unit. Together the rows are the full history behind
// the metrics row's LastPOUnitCost, which keeps only the newest one. They arrive
// oldest order first and include voided lines (matching the metrics filter).
//
// PONumber is nullable on the backend (a PO whose number isn't assigned yet)
// and decodes to "", which is why PurchaseOrder — the PO pk, always present —
// is the key to group rows by order. Money rides DecimalString; UnitCostActual
// stays empty until the delivery is priced.
type ItemOrderCost struct {
	PurchaseOrder   int           `json:"purchase_order"`
	PONumber        string        `json:"po_number"`
	OrderDate       time.Time     `json:"order_date"`
	Status          string        `json:"status"`
	QuantityOrdered int           `json:"quantity_ordered"`
	UnitCostOrdered DecimalString `json:"unit_cost_ordered"`
	UnitCostActual  DecimalString `json:"unit_cost_actual"`
}

// ItemDelivery is one receipt of an item (op-96uo): one row per DeliveryItem, so
// a partially-shipped order yields several rows for the same PO — each with its
// own tracking number, which is what "one order, many tracking numbers" means.
// Oldest delivery first. Same PONumber caveat as ItemOrderCost.
type ItemDelivery struct {
	PurchaseOrder    int       `json:"purchase_order"`
	PONumber         string    `json:"po_number"`
	DeliveryDate     time.Time `json:"delivery_date"`
	TrackingNumber   string    `json:"tracking_number"`
	Carrier          string    `json:"carrier"`
	QuantityReceived int       `json:"quantity_received"`
	ReceiptNotes     string    `json:"receipt_notes"`
	IsComplete       bool      `json:"is_complete"`
}

// ItemPurchaseHistory is the order + receipt provenance behind an item's current
// stock (op-96uo): what each order paid per unit, and what actually shipped when.
type ItemPurchaseHistory struct {
	OrderCosts []ItemOrderCost `json:"order_costs"`
	Deliveries []ItemDelivery  `json:"deliveries"`
}

// GetPurchaseHistory fetches an item's order + receipt provenance
// (GET /api/inventory/items/{id}/purchase_history/, op-96uo — underscored, as
// DRF derives the action path from the method name). Unlike the public
// metrics/retrieve reads this one is auth-required, since it surfaces supplier
// pricing, so an unauthenticated session fails here even though the rest of the
// detail loads; the caller treats a failure as non-fatal (see the TUI section).
//
// include_kits for the same reason GetItemMetrics sends it — a detail action
// runs through the kit-excluding get_queryset — and it matters most here: a kit
// exists to be BOUGHT as one SKU, so its order and receipt provenance is the
// single most relevant thing on its screen, and without the param that section
// read "unavailable: not found" for a record visibly on screen (op-8n0).
func (c *Client) GetPurchaseHistory(ctx context.Context, id string) (*ItemPurchaseHistory, error) {
	var out ItemPurchaseHistory
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%s/purchase_history/", id), includeKitsValues(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CycleCountBody is the POST payload for the cycle-count action. CountedQty is
// the freshly counted physical quantity; the backend computes the signed delta
// against current_stock itself. Reason must be one of the item's REASON_CHOICES
// (lost / damaged / miscounted / used_without_scan / found / vision_supply_check
// / other — kept in sync with the TUI pick-list). SkipReorder suppresses the
// auto-reorder a negative delta would otherwise trigger. Notes is optional.
type CycleCountBody struct {
	CountedQty  int    `json:"counted_qty"`
	Reason      string `json:"reason"`
	SkipReorder bool   `json:"skip_reorder"`
	Notes       string `json:"notes,omitempty"`

	// AtLevel reads CountedQty as a count of whole CountLevel packs ("I counted
	// 3 cases") instead of base units (OMS #981). Strictly OPT-IN and carries
	// omitempty: a quantity means base units unless a caller says otherwise, so
	// every each-mode count keeps sending no flag and keeps its old meaning.
	// Sending it for an item that is not counted in packs is a 400, never a
	// silent base-unit reading.
	AtLevel bool `json:"at_level,omitempty"`

	// OpenCount sets an open_closed item's open-container tally in the same
	// reconciliation — the sealed/open pair counted together. nil omits the key
	// and leaves the stored tally alone; the backend rejects it outright for an
	// item that is not counted open/closed.
	OpenCount *int `json:"open_count,omitempty"`
}

// CycleCountItem records a manual physical count (issue-7):
// POST /api/inventory/items/{id}/cycle-count/. The trailing slash is
// load-bearing — a POST to the unslashed path 301-redirects and net/http's
// default would downgrade the replay to GET; the method-preserving redirect
// client (set in New) covers that, and the canonical slashed URL avoids the
// extra hop entirely (the #81/#46 lesson). The response re-serializes the item
// with the updated current_stock, last_counted_at and days_since_last_count so
// the caller can refresh the detail in place.
func (c *Client) CycleCountItem(ctx context.Context, id string, body CycleCountBody) (*Item, error) {
	var out Item
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/items/%s/cycle-count/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LogUsageBody is the POST payload for the consume / log-usage action
// (accounting Phase 2, backend #920). Quantity is the number of units consumed;
// the backend decrements current_stock by it. Notes is optional free text.
// ChargedGroup is an optional committee (SIG) id — when set, the backend posts a
// ledger charge for the consumed value to that committee; nil (the "— none —"
// pick-list row) records the usage but charges no one, so it carries omitempty
// to be omitted rather than sent as null.
type LogUsageBody struct {
	Quantity     int    `json:"quantity"`
	Notes        string `json:"notes,omitempty"`
	ChargedGroup *int   `json:"charged_group,omitempty"`

	// AtLevel reads Quantity as a count of whole CountLevel packs ("used 2
	// cases") instead of base units (OMS #981). Opt-in and omitempty for the
	// same reason as CycleCountBody.AtLevel — a usage quantity is often derived
	// from a base-unit-canonical source, so the flag never defaults on. The
	// stored UsageLog quantity is still base units either way; the response
	// echoes which unit was read.
	AtLevel bool `json:"at_level,omitempty"`
}

// LogUsageResult is the log_usage action's response: the recorded UsageLog plus
// the costing/charge outcome. ChargedGroup echoes the SIG the charge posted to
// (nil when none was requested). UnitCost / TotalCost are the item's unit cost
// and unit_cost × quantity as decimal strings (DecimalString tolerates the
// string- or number-shaped serializer output, null → empty). LedgerTransaction
// is the id of the posted committee-charge transaction, or nil when nothing was
// posted. Warning is set — and LedgerTransaction stays nil — when the item has
// no unit cost: the usage is still recorded, but no charge could be computed.
type LogUsageResult struct {
	ID                *int          `json:"id,omitempty"`
	Item              string        `json:"item,omitempty"`
	Quantity          int           `json:"quantity,omitempty"`
	Notes             string        `json:"notes,omitempty"`
	ChargedGroup      *int          `json:"charged_group"`
	UnitCost          DecimalString `json:"unit_cost"`
	TotalCost         DecimalString `json:"total_cost"`
	LedgerTransaction *int          `json:"ledger_transaction"`
	Warning           string        `json:"warning,omitempty"`
}

// LogUsage records consumption of an inventory item (accounting Phase 2):
// POST /api/inventory/items/{id}/log_usage/. The trailing slash is load-bearing
// — a POST to the unslashed path 301-redirects and net/http's default would
// downgrade the replay to GET (the method-preserving redirect client set in New
// covers that, and the canonical slashed URL avoids the extra hop — same reason
// as CycleCountItem). body.ChargedGroup optionally charges the consumed value to
// a committee. The response carries the UsageLog plus the costing/charge
// outcome; refresh the item afterwards for the new stock level.
func (c *Client) LogUsage(ctx context.Context, id string, body LogUsageBody) (*LogUsageResult, error) {
	var out LogUsageResult
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/items/%s/log_usage/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ItemWrite is the create/edit payload for an inventory item. It mirrors the
// writable fields of the web item form (frontend InventoryItemFormPage.tsx +
// inventoryItemSchema). A few wire-contract details are load-bearing and match
// the web form's FormData behaviour deliberately:
//
//   - Booleans carry NO omitempty so a PATCH that turns a flag off
//     (is_active / is_hazardous / is_serialized → false) actually reaches the
//     backend instead of being silently dropped.
//   - Optional scalars are pointers: nil means "omit the key" (the web skips
//     empty/null values on submit). A pointer to a zero value still serializes,
//     so NFPAHealthHazard=0 is sent as 0 — a real NFPA rating, not "unset".
//   - Location is a string. The item viewset's serializer field for location is
//     read-only (it returns the location name); the viewset resolves this raw
//     request value as a Location pk (a numeric string) or get_or_creates one
//     by name. Send the picked location's id as a string.
//   - Category rides the serializer as an ordinary FK primary key (int).
//   - SerialTrackingMode has a NOT-NULL "consumable" default on the model, so
//     it must be OMITTED (never null) when the item isn't serialized — leave it
//     nil unless IsSerialized is true, exactly as the web form does.
//
// File uploads the web form also exposes (image / msds_file) are intentionally
// out of scope for the TUI; ImageURL covers the download-by-URL path.
type ItemWrite struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	SKU         *string `json:"sku,omitempty"`
	ImageURL    *string `json:"image_url,omitempty"`

	CurrentStock    int `json:"current_stock"`
	MinimumStock    int `json:"minimum_stock"`
	ReorderQuantity int `json:"reorder_quantity"`

	UseCaseBasedReorder bool `json:"use_case_based_reorder"`
	MinimumCases        *int `json:"minimum_cases,omitempty"`
	ReorderCases        *int `json:"reorder_cases,omitempty"`

	// ReorderAlertsEnabled opts the item in or out of the ML reorder-alert
	// notify set (op-1). No omitempty — turning the watch OFF has to reach the
	// backend, same rule as the other booleans above.
	ReorderAlertsEnabled bool `json:"reorder_alerts_enabled"`

	Category      *int    `json:"category,omitempty"`
	Location      *string `json:"location,omitempty"`
	ShelfPosition *string `json:"shelf_position,omitempty"`

	IsHazardous           bool    `json:"is_hazardous"`
	MSDSURL               *string `json:"msds_url,omitempty"`
	NFPAHealthHazard      *int    `json:"nfpa_health_hazard,omitempty"`
	NFPAFireHazard        *int    `json:"nfpa_fire_hazard,omitempty"`
	NFPAInstabilityHazard *int    `json:"nfpa_instability_hazard,omitempty"`
	NFPASpecialHazards    *string `json:"nfpa_special_hazards,omitempty"`

	IsSerialized       bool    `json:"is_serialized"`
	SerialTrackingMode *string `json:"serial_tracking_mode,omitempty"`

	// BaseUnit names the smallest countable thing ("sheet"/"glove"/"bolt").
	// Blank omits the key so the model's own "unit" default stands.
	BaseUnit *string `json:"base_unit,omitempty"`

	// PackagingLevels REPLACES the item's pack chain. nil omits the key and
	// leaves the stored chain alone; a pointer to an EMPTY slice sends `[]` and
	// clears it — so callers must only send this when the chain actually
	// changed, or a form that failed to hydrate would wipe it.
	//
	// The rung pk is deliberately absent from the payload: the serializer
	// upserts on (item, sort_order), so a rung that keeps its position keeps its
	// pk — and therefore any CountLevel FK pointing at it survives the save.
	PackagingLevels *[]PackagingLevelWrite `json:"packaging_levels,omitempty"`

	// The counting granularity (count_mode + count_level) is deliberately NOT
	// here: the pair has to be written together — the backend rejects a pack
	// mode with no level and an "each" mode with one — and a pack level is a pk
	// that only exists once the chain has been saved. SetItemCountMode owns it.

	IsActive bool `json:"is_active"`
	// IsRetired is writable (op-jv7r): the item form toggles phase-out
	// directly via the serializer field. The read-only retired_at audit stamp
	// is set by the dedicated retire/unretire actions (SetItemRetired), not by
	// this PATCH.
	IsRetired bool    `json:"is_retired"`
	Notes     *string `json:"notes,omitempty"`
}

// PackagingLevelWrite is one rung of the nested packaging_levels payload.
// SortOrder is the rung's POSITION in the chain (0 = outermost/largest), which
// is the identity the serializer upserts on — so no pk is sent, and a rung that
// keeps its position keeps its pk. BaseUnits is how many base units one of this
// rung holds; the innermost rung holds exactly 1.
type PackagingLevelWrite struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	BaseUnits int    `json:"base_units"`
}

// itemCountModeBody is the count_mode + count_level pair, written on its own.
// CountLevel carries NO omitempty: switching an item back to "each" has to send
// an explicit null, and the backend rejects "each" while a level is still set.
type itemCountModeBody struct {
	CountMode  string `json:"count_mode"`
	CountLevel *int   `json:"count_level"`
}

// SetItemCountMode writes an item's counting granularity — the count_mode +
// count_level pair — as its own PATCH.
//
// It is separate from UpdateInventoryItem because the two values are only ever
// legal together, and because a pack mode's level is a PackagingLevel pk that
// does not exist until the chain has been saved. So the caller's order is:
// detach (CountModeEach, nil) if the stored level is about to be invalidated →
// write the item and its chain → attach the mode with the level pk resolved out
// of the saved chain by sort_order.
//
// level must be nil for CountModeEach and non-nil for the pack modes; the
// backend rejects the other two combinations rather than guessing.
//
// include_kits is on the route because a KIT reaches here too: a kit's own save
// goes to /kits/, but the count mode is written by this second PATCH afterwards,
// and without the param that PATCH 404s and the operator is told the item saved
// but its packaging did not, with no way to finish the change.
func (c *Client) SetItemCountMode(ctx context.Context, id, mode string, level *int) (*Item, error) {
	body := itemCountModeBody{CountMode: mode, CountLevel: level}
	var out Item
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/items/%s/%s", id, includeKitsQuery), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Pack-container transitions for an open_closed item (OMS #981).
const (
	// PackTransitionOpen breaks a sealed pack open. Under open_closed this IS
	// consumption: stock drops by the pack's base units, the open tally rises,
	// and the backend writes a usage log — because an open pack's remaining
	// contents stop being countable the moment it is opened.
	PackTransitionOpen = "open"
	// PackTransitionFinish records that the open pack is empty. Only the open
	// tally moves; stock already did, when the pack was opened.
	PackTransitionFinish = "finish"
)

// packContainerBody is the pack-container POST payload. Notes is optional and
// rides onto the usage log the "open" half writes.
type packContainerBody struct {
	Transition string `json:"transition"`
	Notes      string `json:"notes,omitempty"`
}

// PackContainerResult is the pack-container action's response. OnHandDisplay is
// the refreshed sealed/open split, which is what the caller shows; the usage_log
// the backend also returns is not decoded, since the caller re-fetches the item
// anyway.
type PackContainerResult struct {
	Transition         string         `json:"transition"`
	ID                 string         `json:"id"`
	CurrentStock       int            `json:"current_stock"`
	OpenContainerCount int            `json:"open_container_count"`
	OnHandDisplay      *OnHandDisplay `json:"on_hand_display,omitempty"`
}

// PackContainer opens a sealed pack or finishes the open one for an open_closed
// item: POST /api/inventory/items/{id}/pack-container/ (HYPHEN, and the
// trailing slash is load-bearing — same DRF-router contract the web posts to,
// and the canonical slashed URL avoids the redirect hop).
//
// 400s for an item that is not counted open/closed, for opening with no sealed
// pack left, and for finishing with no open pack — the caller surfaces the
// backend's reason rather than pre-judging it.
func (c *Client) PackContainer(ctx context.Context, id, transition, notes string) (*PackContainerResult, error) {
	body := packContainerBody{Transition: transition, Notes: notes}
	var out PackContainerResult
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/items/%s/pack-container/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateInventoryItem POSTs a new inventory item. The response echoes the
// created item (the viewset re-serializes it, so location/category names come
// back populated).
func (c *Client) CreateInventoryItem(ctx context.Context, body ItemWrite) (*Item, error) {
	var out Item
	if err := c.Post(ctx, "/api/inventory/items/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateInventoryItem PATCHes an existing item. PATCH (not PUT) mirrors the web
// form, so omitted keys are left untouched server-side.
func (c *Client) UpdateInventoryItem(ctx context.Context, id string, body ItemWrite) (*Item, error) {
	var out Item
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/items/%s/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteInventoryItem removes an item (DELETE /api/inventory/items/{id}/). The
// backend enforces manage-inventory permission and returns 204 on success.
//
// include_kits is on the route because deleting IS meaningful for a kit — a kit
// is catalogue data like any other item — and a kit id is reachable from the
// detail screen that offers this. Without the param it would be a 404 for a
// record visibly on screen.
func (c *Client) DeleteInventoryItem(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/items/%s/%s", id, includeKitsQuery))
}

// SetItemRetired retires or un-retires an item via the backend's dedicated POST
// actions (op-jv7r): …/items/{id}/retire/ or …/items/{id}/unretire/. Unlike a
// plain is_retired PATCH, these also stamp (or clear) the read-only retired_at
// audit field. Both are idempotent server-side and echo the updated item.
//
// include_kits is on both routes because retiring IS meaningful for a kit — a
// kit is catalogue data that can be phased out — and a kit id is reachable from
// the detail screen that offers this. These are detail actions, so they resolve
// through the same kit-excluding get_queryset the plain detail route does.
func (c *Client) SetItemRetired(ctx context.Context, id string, retired bool) (*Item, error) {
	action := "unretire"
	if retired {
		action = "retire"
	}
	var out Item
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/items/%s/%s/%s", id, action, includeKitsQuery), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Asset struct {
	ID                  any    `json:"id"`
	Name                string `json:"name"`
	AssetTag            string `json:"asset_tag,omitempty"`
	Description         string `json:"description,omitempty"`
	SerialNumber        string `json:"serial_number,omitempty"`
	InventoryItem       any    `json:"inventory_item,omitempty"`
	InventoryItemName   string `json:"inventory_item_name,omitempty"`
	Manufacturer        *int   `json:"manufacturer,omitempty"`
	ManufacturerName    string `json:"manufacturer_name,omitempty"`
	DisplayManufacturer string `json:"display_manufacturer,omitempty"`
	Category            *int   `json:"category,omitempty"`
	CategoryName        string `json:"category_name,omitempty"`
	Location            *int   `json:"location,omitempty"`
	LocationName        string `json:"location_name,omitempty"`
	Status              string `json:"status,omitempty"`
	IsCritical          bool   `json:"is_critical,omitempty"`

	// Acquisition / cost
	DateReceived       string        `json:"date_received,omitempty"`
	AmountPaid         DecimalString `json:"amount_paid,omitempty"`
	IsDonation         bool          `json:"is_donation,omitempty"`
	DonorName          string        `json:"donor_name,omitempty"`
	AcquisitionDisplay string        `json:"acquisition_display,omitempty"`
	AgeInDays          *int          `json:"age_in_days,omitempty"`

	// Documentation / media
	ProductURL    string `json:"product_url,omitempty"`
	WikiPageURL   string `json:"wiki_page_url,omitempty"`
	ImageURL      string `json:"image_url,omitempty"`
	ThumbnailURL  string `json:"thumbnail_url,omitempty"`
	ManualPDFURL  string `json:"manual_pdf_url,omitempty"`
	QRCodeURL     string `json:"qr_code_url,omitempty"`
	QRCodeScanURL string `json:"qr_code_scan_url,omitempty"`

	// Maintenance
	MaintenancePlan string      `json:"maintenance_plan,omitempty"`
	Parts           []AssetPart `json:"parts,omitempty"`
	ConditionNotes  string      `json:"condition_notes,omitempty"`

	// Operational requirements
	Circuit              string `json:"circuit,omitempty"`
	MACAddress           string `json:"mac_address,omitempty"`
	NeedsCompressedAir   bool   `json:"needs_compressed_air,omitempty"`
	NeedsVentilation     bool   `json:"needs_ventilation,omitempty"`
	GeneratesHeatOrFlame bool   `json:"generates_heat_or_flame,omitempty"`
	NeedsChilling        bool   `json:"needs_chilling,omitempty"`
	SpecialRequirements  string `json:"special_requirements,omitempty"`
	WorkSafetyNotes      string `json:"work_safety_notes,omitempty"`
	IsChargeable         bool   `json:"is_chargeable,omitempty"`

	// Power / electrical
	PowerDrawWatts       DecimalString `json:"power_draw_watts,omitempty"`
	WiringType           string        `json:"wiring_type,omitempty"`
	Suite                string        `json:"suite,omitempty"`
	ElectricalBox        string        `json:"electrical_box,omitempty"`
	BreakerLocation      string        `json:"breaker_location,omitempty"`
	HasInterlock         bool          `json:"has_interlock,omitempty"`
	InterlockType        string        `json:"interlock_type,omitempty"`
	InterlockResponsible string        `json:"interlock_responsible,omitempty"`
	LockoutType          string        `json:"lockout_type,omitempty"`
	LockoutInstructions  string        `json:"lockout_instructions,omitempty"`
	LockoutResponsible   string        `json:"lockout_responsible,omitempty"`
	HasNetworkDrop       bool          `json:"has_network_drop,omitempty"`
	NetworkDropLocation  string        `json:"network_drop_location,omitempty"`
	IsForgeKeyManaged    bool          `json:"is_forgekey_managed,omitempty"`

	// Scanning
	LastScannedAt *time.Time `json:"last_scanned_at,omitempty"`

	// Ownership
	OwningGroup     *int   `json:"owning_group,omitempty"`
	OwningUser      *int   `json:"owning_user,omitempty"`
	OwningGroupName string `json:"owning_group_name,omitempty"`
	OwningUserName  string `json:"owning_user_name,omitempty"`
	GroupsCanEnable []int  `json:"groups_can_enable,omitempty"`

	// ForgeKey runtime
	OperationalMode map[string]any `json:"operational_mode,omitempty"`
	IsLocked        bool           `json:"is_locked,omitempty"`
	LockoutInfo     *AssetLockout  `json:"lockout_info,omitempty"`
	CanEnable       bool           `json:"can_enable,omitempty"`
	CanUnlock       bool           `json:"can_unlock,omitempty"`

	// Training / certification gates
	TrainingRequired             bool                           `json:"training_required,omitempty"`
	RequiredCertifications       []int                          `json:"required_certifications,omitempty"`
	RequiredCertificationDetails []RequiredCertificationSummary `json:"required_certification_details,omitempty"`

	// Metadata
	IsActive   bool      `json:"is_active,omitempty"`
	ReportOnly bool      `json:"report_only,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

// RequiredCertificationSummary mirrors the AssetSerializer's
// required_certification_details payload: light cert info attached
// directly to the asset so the TUI doesn't need a second round-trip
// per cert lookup.
type RequiredCertificationSummary struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug,omitempty"`
	SIGName string `json:"sig_name,omitempty"`
}

type AssetLockout struct {
	LockedBy     string `json:"locked_by,omitempty"`
	LockedAt     string `json:"locked_at,omitempty"`
	LockoutLevel string `json:"lockout_level,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// AssetPartDetails is the read-only nested projection of the linked
// InventoryItem the AssetPartSerializer embeds under part_details. Only the
// fields the TUI consumes are decoded — extra keys are ignored. IsSerialized
// gates the replacement-serial prompt on mark-replaced (op-8nxe contract);
// absent/false → no prompt, so this stays harmless before the backend deploys.
type AssetPartDetails struct {
	IsSerialized bool `json:"is_serialized"`
}

type AssetPart struct {
	ID                      any              `json:"id"`
	Asset                   string           `json:"asset"`
	AssetName               string           `json:"asset_name,omitempty"`
	AssetTag                string           `json:"asset_tag,omitempty"`
	Part                    string           `json:"part"`
	PartName                string           `json:"part_name,omitempty"`
	PartSKU                 string           `json:"part_sku,omitempty"`
	QuantityNeeded          int              `json:"quantity_needed,omitempty"`
	IsRequired              bool             `json:"is_required,omitempty"`
	MaintenanceIntervalDays *int             `json:"maintenance_interval_days,omitempty"`
	LastReplacedAt          *time.Time       `json:"last_replaced_at,omitempty"`
	DaysSinceReplacement    *int             `json:"days_since_replacement,omitempty"`
	NeedsReplacement        bool             `json:"needs_replacement,omitempty"`
	Notes                   string           `json:"notes,omitempty"`
	ReplacementSerialNumber string           `json:"replacement_serial_number,omitempty"`
	PartDetails             AssetPartDetails `json:"part_details,omitempty"`
	CreatedAt               time.Time        `json:"created_at,omitempty"`
	UpdatedAt               time.Time        `json:"updated_at,omitempty"`
}

func (c *Client) ListAssets(ctx context.Context, q url.Values) (*Page[Asset], error) {
	return GetPage[Asset](ctx, c, "/api/inventory/assets/", q)
}

// ListAllAssets pages through every asset. The maintenance-item asset picker
// and the clone-target picker need the full set — truncating to page 1 (as the
// plain assets list does) could hide the asset the operator is looking for.
// Asset counts are bounded per install, so the extra pages are cheap.
func (c *Client) ListAllAssets(ctx context.Context) ([]Asset, error) {
	var all []Asset
	if err := IterPages[Asset](ctx, c, "/api/inventory/assets/", nil, func(batch []Asset) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

func (c *Client) GetAsset(ctx context.Context, id string) (*Asset, error) {
	var out Asset
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/assets/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ScanAsset(ctx context.Context, id string) (*Asset, error) {
	var out Asset
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/assets/%s/scan/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InventoryItemID coerces the polymorphic InventoryItem field to the linked
// item's pk string for edit-mode form hydration. ok is false when the asset has
// no linked inventory-item type.
//
// WHAT ACTUALLY ARRIVES TODAY is a string: InventoryItem's primary key is a
// models.UUIDField, so the serializer echoes a UUID and the string arm is the
// live one. The numeric arms are defensive against a future numeric-pk shape,
// and they are TWO arms rather than one because Client.decodeBody sets UseNumber
// — a JSON number in an `any` is a json.Number here, never a float64.
//
// The float64 arm alone was a documented claim the code could not honour: its
// comment promised the branch was "defensive for any numeric-pk serializer
// shape" while UseNumber had already made it unreachable, so a numeric pk would
// have matched neither case and this would have returned ("", false) — which
// every caller reads as "this asset has no linked inventory item", silently, in
// the middle of hydrating an edit form the operator is about to save. A dead
// defensive branch is worse than none, because its comment stops anybody looking.
// Both arms are kept: an `any` that some other decoder filled can still hold a
// float64, and losing the link is the same silent wrong answer either way.
func (a *Asset) InventoryItemID() (string, bool) {
	switch v := a.InventoryItem.(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
			return s, true
		}
	case json.Number:
		if s := strings.TrimSpace(v.String()); s != "" {
			return s, true
		}
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	}
	return "", false
}

// AssetWrite is the create/edit payload for a hard asset. It mirrors the
// writable fields of the web asset form (frontend AssetFormPage.tsx +
// assetFormSchema). Wire-contract details that match the web form's FormData
// behaviour deliberately:
//
//   - Booleans carry NO omitempty so a PATCH that turns a flag off
//     (is_active / is_donation / needs_* / report_only → false) actually
//     reaches the backend instead of being silently dropped.
//   - Optional scalars / FKs are pointers: nil means "omit the key". The web
//     skips empty/null values on submit, so e.g. changing ownership away from a
//     group leaves the old owning_group untouched — this mirrors that exactly.
//   - Location rides the AssetSerializer as an ordinary FK primary key (int),
//     unlike the inventory item form (whose viewset resolves a pk-or-name
//     string). Send the picked location's id.
//   - ownership_type is NOT a serializer write field (the model column can't be
//     changed through this endpoint), but the viewset's create() reads it from
//     the raw request to gate SIG-ownership permission, so it is sent to mirror
//     the web. owning_group is the field that actually persists.
//   - required_certifications is an M2M written as a JSON array of cert pks;
//     omitted when empty so a PATCH doesn't clear existing certs (the web
//     appends nothing for an empty list).
type AssetWrite struct {
	Name          string  `json:"name"`
	AssetTag      string  `json:"asset_tag,omitempty"`
	Description   *string `json:"description,omitempty"`
	SerialNumber  *string `json:"serial_number,omitempty"`
	InventoryItem *string `json:"inventory_item,omitempty"` // InventoryItem pk is a UUID
	Category      *int    `json:"category,omitempty"`
	Location      *int    `json:"location,omitempty"`

	DateReceived *string `json:"date_received,omitempty"`
	AmountPaid   string  `json:"amount_paid"`
	IsDonation   bool    `json:"is_donation"`
	DonorName    *string `json:"donor_name,omitempty"`

	WikiPageURL *string `json:"wiki_page_url,omitempty"`
	ProductURL  *string `json:"product_url,omitempty"`

	Status        string `json:"status"`
	OwnershipType string `json:"ownership_type"`
	OwningGroup   *int   `json:"owning_group,omitempty"`
	OwningUser    *int   `json:"owning_user,omitempty"`
	IsActive      bool   `json:"is_active"`

	NeedsCompressedAir     bool    `json:"needs_compressed_air"`
	NeedsVentilation       bool    `json:"needs_ventilation"`
	GeneratesHeatOrFlame   bool    `json:"generates_heat_or_flame"`
	NeedsChilling          bool    `json:"needs_chilling"`
	SpecialRequirements    *string `json:"special_requirements,omitempty"`
	WorkSafetyNotes        *string `json:"work_safety_notes,omitempty"`
	IsChargeable           bool    `json:"is_chargeable"`
	TrainingRequired       bool    `json:"training_required"`
	RequiredCertifications []int   `json:"required_certifications,omitempty"`
	ReportOnly             bool    `json:"report_only"`

	Notes          *string `json:"notes,omitempty"`
	ConditionNotes *string `json:"condition_notes,omitempty"`
	ManualPDFPath  string  `json:"-"`
}

// CreateAsset POSTs a new asset. The response echoes the created asset
// (the viewset re-serializes it, so *_name display fields come back populated).
func (c *Client) CreateAsset(ctx context.Context, body AssetWrite) (*Asset, error) {
	var out Asset
	var err error
	if body.needsMultipart() {
		files, ferr := body.multipartFiles()
		if ferr != nil {
			return nil, ferr
		}
		err = c.PostMultipart(ctx, "/api/inventory/assets/", body.multipartFields(), files, &out)
	} else {
		err = c.Post(ctx, "/api/inventory/assets/", body, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAsset PATCHes an existing asset. PATCH (not PUT) mirrors the web form:
// only the keys present in the payload change, so omitted optional fields keep
// their server-side value.
func (c *Client) UpdateAsset(ctx context.Context, id string, body AssetWrite) (*Asset, error) {
	var out Asset
	path := fmt.Sprintf("/api/inventory/assets/%s/", id)
	var err error
	if body.needsMultipart() {
		files, ferr := body.multipartFiles()
		if ferr != nil {
			return nil, ferr
		}
		err = c.PatchMultipart(ctx, path, body.multipartFields(), files, &out)
	} else {
		err = c.Patch(ctx, path, body, &out)
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (w AssetWrite) needsMultipart() bool {
	return strings.TrimSpace(w.ManualPDFPath) != ""
}

// multipartFiles reads the picked manual PDF off disk into an in-memory file
// part for PostMultipart/PatchMultipart. Returns nil when no PDF is attached.
func (w AssetWrite) multipartFiles() ([]MultipartFile, error) {
	p := strings.TrimSpace(w.ManualPDFPath)
	if p == "" {
		return nil, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("oms: read %s: %w", p, err)
	}
	return []MultipartFile{{Field: "manual_pdf", Filename: filepath.Base(p), Data: data}}, nil
}

func (w AssetWrite) multipartFields() map[string][]string {
	fields := map[string][]string{}
	add := func(k, v string) {
		fields[k] = append(fields[k], v)
	}
	addPtr := func(k string, v *string) {
		if v != nil {
			add(k, *v)
		}
	}
	addIntPtr := func(k string, v *int) {
		if v != nil {
			add(k, strconv.Itoa(*v))
		}
	}

	add("name", w.Name)
	if strings.TrimSpace(w.AssetTag) != "" {
		add("asset_tag", w.AssetTag)
	}
	addPtr("description", w.Description)
	addPtr("serial_number", w.SerialNumber)
	addPtr("inventory_item", w.InventoryItem)
	addIntPtr("category", w.Category)
	addIntPtr("location", w.Location)
	addPtr("date_received", w.DateReceived)
	add("amount_paid", w.AmountPaid)
	add("is_donation", strconv.FormatBool(w.IsDonation))
	addPtr("donor_name", w.DonorName)
	addPtr("wiki_page_url", w.WikiPageURL)
	addPtr("product_url", w.ProductURL)
	add("status", w.Status)
	add("ownership_type", w.OwnershipType)
	addIntPtr("owning_group", w.OwningGroup)
	addIntPtr("owning_user", w.OwningUser)
	add("is_active", strconv.FormatBool(w.IsActive))
	add("needs_compressed_air", strconv.FormatBool(w.NeedsCompressedAir))
	add("needs_ventilation", strconv.FormatBool(w.NeedsVentilation))
	add("generates_heat_or_flame", strconv.FormatBool(w.GeneratesHeatOrFlame))
	add("needs_chilling", strconv.FormatBool(w.NeedsChilling))
	addPtr("special_requirements", w.SpecialRequirements)
	addPtr("work_safety_notes", w.WorkSafetyNotes)
	add("is_chargeable", strconv.FormatBool(w.IsChargeable))
	add("training_required", strconv.FormatBool(w.TrainingRequired))
	for _, id := range w.RequiredCertifications {
		add("required_certifications", strconv.Itoa(id))
	}
	add("report_only", strconv.FormatBool(w.ReportOnly))
	addPtr("notes", w.Notes)
	addPtr("condition_notes", w.ConditionNotes)
	return fields
}

// DeleteAsset removes an asset (DELETE /api/inventory/assets/{id}/). The
// backend enforces manage-asset permission and returns 204 on success.
func (c *Client) DeleteAsset(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/assets/%s/", id))
}

// CertificationOption is one row of the asset form's required-certifications
// picker. GET /api/lockers/available-certifications/ returns a bare JSON array
// of these (not a paginated envelope). Named to avoid colliding with
// membership.Certification, which is a user's *granted* cert (a different
// shape).
type CertificationOption struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ListAvailableCertifications returns the active certification catalogue used
// by the asset form's required-certifications picker. Staff-only on the
// backend; a non-staff caller gets 403, which the form treats as "no certs to
// pick" rather than a fatal error — matching the web form, which wraps this
// load in a catch that falls back to an empty list.
func (c *Client) ListAvailableCertifications(ctx context.Context) ([]CertificationOption, error) {
	var out []CertificationOption
	if err := c.Get(ctx, "/api/lockers/available-certifications/", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Location mirrors the writable + read-only fields of the web Location form and
// detail pages (frontend LocationFormPage.tsx / LocationDetailPage.tsx). The
// serializer is fields="__all__", so it returns name/description/is_active plus
// the read-only parent_name, fixture_count, access_code and qr_code_url.
//
// Code/Capacity have no backing model column today (the Location model exposes
// name, description, is_active, parent, access_code, qr_code); they are retained
// only so the existing location-picker labels in the item/asset forms keep
// compiling, and stay zero-valued in practice.
type Location struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	IsActive     bool   `json:"is_active,omitempty"`
	Parent       *int   `json:"parent,omitempty"`
	ParentName   string `json:"parent_name,omitempty"`
	AccessCode   string `json:"access_code,omitempty"`
	QRCodeURL    string `json:"qr_code_url,omitempty"`
	FixtureCount int    `json:"fixture_count,omitempty"`
	Code         string `json:"code,omitempty"`
	Capacity     int    `json:"capacity,omitempty"`
}

// ListLocations fetches storage locations for the item/asset/location pickers.
//
// The backend LocationViewSet overrides list() to return Response(serializer.data)
// — a BARE JSON ARRAY, not the {count,next,previous,results} envelope the default
// PageNumberPagination emits (categories/suppliers/items all keep the envelope, so
// only this endpoint diverges). Decoding straight into Page[Location] therefore
// died with "cannot unmarshal array into omsapi.Page[Location]" the moment an
// operator opened the item/asset create/edit form or the location list. Decode
// through MaybeList so either shape parses, then repackage into *Page[Location]
// so every caller keeps reading .Results unchanged. Mirrors listUsersAt.
func (c *Client) ListLocations(ctx context.Context, q url.Values) (*Page[Location], error) {
	var out MaybeList[Location]
	if err := c.Get(ctx, "/api/inventory/locations/", q, &out); err != nil {
		return nil, err
	}
	return &Page[Location]{Count: out.Count, Results: out.Items}, nil
}

func (c *Client) GetLocation(ctx context.Context, id string) (*Location, error) {
	var out Location
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/locations/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LocationWrite is the create/edit payload for a storage location. It mirrors
// the web LocationFormPage's writable fields (name, description, parent,
// is_active). Parent carries NO omitempty so a nil pointer serializes as null
// and can clear the parent on a PATCH (the picker's "(none)" row); is_active
// likewise carries no omitempty so it can be toggled off. access_code and the
// QR image are server-managed (read-only) and never sent.
//
// NOTE: the backend LocationViewSet gates create/update/destroy behind
// IsAdminUser — a non-staff caller gets 403 on save/delete even though reads
// are public.
type LocationWrite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parent      *int   `json:"parent"`
	IsActive    bool   `json:"is_active"`
}

func (c *Client) CreateLocation(ctx context.Context, body LocationWrite) (*Location, error) {
	var out Location
	if err := c.Post(ctx, "/api/inventory/locations/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateLocation(ctx context.Context, id string, body LocationWrite) (*Location, error) {
	var out Location
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/locations/%s/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteLocation(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/locations/%s/", id))
}

// LocationQRResult is the generate_qr action's JSON body
// ({"message": ..., "qr_code_url": ...}); the action does not re-serialize the
// location, so callers that want the fresh qr_code_url read it here.
type LocationQRResult struct {
	Message   string `json:"message"`
	QRCodeURL string `json:"qr_code_url"`
	Error     string `json:"error,omitempty"`
}

// GenerateLocationQR POSTs to the generate_qr action (AllowAny on the backend)
// to create or regenerate the location's check-in QR code.
func (c *Client) GenerateLocationQR(ctx context.Context, id string) (*LocationQRResult, error) {
	var out LocationQRResult
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/locations/%s/generate_qr/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Category mirrors the CategorySerializer (fields="__all__"). Writable columns
// are name, description, color and parent; slug is auto-generated from the name
// and read-only server-side, and parent_name/children/item_count are read-only
// display helpers.
type Category struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	Slug        string     `json:"slug,omitempty"`
	Description string     `json:"description,omitempty"`
	Color       string     `json:"color,omitempty"`
	Parent      *int       `json:"parent,omitempty"`
	ParentName  string     `json:"parent_name,omitempty"`
	ItemCount   int        `json:"item_count,omitempty"`
	Children    []Category `json:"children,omitempty"`
}

func (c *Client) ListCategories(ctx context.Context, q url.Values) (*Page[Category], error) {
	return GetPage[Category](ctx, c, "/api/inventory/categories/", q)
}

func (c *Client) GetCategory(ctx context.Context, id string) (*Category, error) {
	var out Category
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/categories/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CategoryWrite is the create/edit payload for an inventory category, mirroring
// the web CategoryFormPage (name, description, color, parent). slug is omitted
// because it is auto-generated + read-only on the backend. description and color
// are always sent (matching the web form, which submits trimmed/empty strings)
// so a PATCH can clear them; parent carries no omitempty so nil clears it.
type CategoryWrite struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	Parent      *int   `json:"parent"`
}

func (c *Client) CreateCategory(ctx context.Context, body CategoryWrite) (*Category, error) {
	var out Category
	if err := c.Post(ctx, "/api/inventory/categories/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateCategory(ctx context.Context, id string, body CategoryWrite) (*Category, error) {
	var out Category
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/categories/%s/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteCategory(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/categories/%s/", id))
}

// Supplier mirrors the SupplierSerializer (list) and, on retrieve, the
// SupplierDetailSerializer — which additionally embeds the supplier's item
// catalogue under "items". Items stays empty for list responses.
type Supplier struct {
	ID                    int            `json:"id"`
	Name                  string         `json:"name"`
	SupplierType          string         `json:"supplier_type,omitempty"`
	Website               string         `json:"website,omitempty"`
	AccountNumber         string         `json:"account_number,omitempty"`
	TaxFreePaperworkFiled bool           `json:"tax_free_paperwork_filed,omitempty"`
	Notes                 string         `json:"notes,omitempty"`
	ItemCount             int            `json:"item_count,omitempty"`
	PurchaseOrderCount    int            `json:"purchase_order_count,omitempty"`
	TotalSpent            DecimalString  `json:"total_spent,omitempty"`
	Items                 []ItemSupplier `json:"items,omitempty"`
	CreatedAt             time.Time      `json:"created_at,omitempty"`
	UpdatedAt             time.Time      `json:"updated_at,omitempty"`
}

func (c *Client) ListSuppliers(ctx context.Context, q url.Values) (*Page[Supplier], error) {
	return GetPage[Supplier](ctx, c, "/api/inventory/suppliers/", q)
}

// ListAllSuppliers pages through every supplier. The item↔supplier edit form's
// supplier picker must be able to reach ANY supplier, so — like ListAllItems —
// it can't truncate to page 1: a supplier on a later page would be unpickable.
// Supplier counts are small and bounded per install, so the extra pages are
// cheap.
func (c *Client) ListAllSuppliers(ctx context.Context) ([]Supplier, error) {
	var all []Supplier
	if err := IterPages[Supplier](ctx, c, "/api/inventory/suppliers/", nil, func(batch []Supplier) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

func (c *Client) GetSupplier(ctx context.Context, id string) (*Supplier, error) {
	var out Supplier
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/suppliers/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SupplierWrite is the create/edit payload for a supplier, mirroring the web
// SupplierFormPage / supplierSchema (name, supplier_type, website,
// account_number, tax_free_paperwork_filed, notes). All string fields are sent
// as-is (empty allowed) so a PATCH can clear them, matching the web form which
// submits every field on save. supplier_type is one of local/online/national.
type SupplierWrite struct {
	Name                  string `json:"name"`
	SupplierType          string `json:"supplier_type"`
	Website               string `json:"website"`
	AccountNumber         string `json:"account_number"`
	TaxFreePaperworkFiled bool   `json:"tax_free_paperwork_filed"`
	Notes                 string `json:"notes"`
}

func (c *Client) CreateSupplier(ctx context.Context, body SupplierWrite) (*Supplier, error) {
	var out Supplier
	if err := c.Post(ctx, "/api/inventory/suppliers/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateSupplier(ctx context.Context, id string, body SupplierWrite) (*Supplier, error) {
	var out Supplier
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/suppliers/%s/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteSupplier(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/suppliers/%s/", id))
}

// SupplierAgreement is a purchase/pricing agreement held with a supplier
// (op-yoos): contract pricing, a standing quote, a nonprofit discount. A
// purchase order can cite the agreement it was placed under, and the backend
// rejects an agreement belonging to a different supplier than the order's.
//
// Retired paperwork stays on file with is_active=false; the PO-create picker
// asks for the active set only. document / created_at / updated_at are on the
// wire too but nothing in scantty renders them yet, so they're left out.
type SupplierAgreement struct {
	ID           int    `json:"id"`
	Supplier     int    `json:"supplier"`
	SupplierName string `json:"supplier_name,omitempty"`
	Name         string `json:"name"`
	Notes        string `json:"notes,omitempty"`
	IsActive     bool   `json:"is_active"`
}

// ListSupplierAgreements returns one supplier's ACTIVE agreements — the exact
// question the PO-create flow asks (mirroring the web form's
// supplierAgreementAPI.listBySupplier). Agreement counts per supplier are tiny,
// so page 1 is the whole list in practice and this returns results directly
// rather than a Page.
func (c *Client) ListSupplierAgreements(ctx context.Context, supplierID int) ([]SupplierAgreement, error) {
	q := url.Values{}
	q.Set("supplier", strconv.Itoa(supplierID))
	q.Set("is_active", "true")
	page, err := GetPage[SupplierAgreement](ctx, c, "/api/inventory/supplier-agreements/", q)
	if err != nil {
		return nil, err
	}
	return page.Results, nil
}

type ItemSupplier struct {
	ID                int            `json:"id"`
	Item              string         `json:"item"`
	ItemName          string         `json:"item_name,omitempty"`
	Supplier          int            `json:"supplier"`
	SupplierName      string         `json:"supplier_name,omitempty"`
	SupplierSKU       string         `json:"supplier_sku,omitempty"`
	URL               string         `json:"supplier_url,omitempty"`
	PackageUPC        string         `json:"package_upc,omitempty"`
	UnitUPC           string         `json:"unit_upc,omitempty"`
	PackQuantity      int            `json:"quantity_per_package,omitempty"`
	UnitCost          DecimalString  `json:"unit_cost,omitempty"`
	PackageCost       DecimalString  `json:"package_cost,omitempty"`
	LeadTimeDays      float64        `json:"average_lead_time,omitempty"`
	LeadTimeSource    LeadTimeSource `json:"average_lead_time_source,omitempty"`
	IsPreferred       bool           `json:"is_primary,omitempty"`
	IsActive          bool           `json:"is_active,omitempty"`
	IsDiscontinued    bool           `json:"is_discontinued,omitempty"`
	PackageDimensions string         `json:"package_dimensions_display,omitempty"`
	Notes             string         `json:"notes,omitempty"`
	CreatedAt         time.Time      `json:"created_at,omitempty"`
	UpdatedAt         time.Time      `json:"updated_at,omitempty"`
	// Version is the link's optimistic-concurrency token as it was LOADED, and
	// every write that edits or removes this copy hands it back
	// (item_supplier_version.go carries the contract). Zero means the server
	// served no token — an OMS before #1091 — and a zero is never sent.
	Version int `json:"version,omitempty"`
}

func (c *Client) ListItemSuppliers(ctx context.Context, q url.Values) (*Page[ItemSupplier], error) {
	return GetPage[ItemSupplier](ctx, c, "/api/inventory/item-suppliers/", q)
}

// ListItemSuppliersForSupplier loads EVERY active item this supplier sells, for
// the supplier-scoped item picker in the New PO flow. Only active rows, so the
// warden doesn't see discontinued lines.
//
// ItemSupplierViewSet does not accept ?search= on the backend today, so the
// picker filters client-side over whatever this returns. That makes the page
// count a correctness property, not a performance one: this used to fetch page
// ONE and drop the envelope's `next`, on the reasoning that "typical supplier
// catalogs are small enough that one or two pages cover everything" — and for a
// supplier whose catalog ran past a page, every item after it was invisible to
// the picker's filter. The screen then answered a search for a real item with
// "No inventory items match", which is a different and false statement, and the
// operator had no key that could reach the item at all. So it pages: an
// unreachable item is worth more than a saved round trip, and the picker says
// it is looking the catalog up while this runs.
func (c *Client) ListItemSuppliersForSupplier(ctx context.Context, supplierID int) ([]ItemSupplier, error) {
	q := url.Values{}
	q.Set("supplier_id", strconv.Itoa(supplierID))
	q.Set("active_only", "true")
	var all []ItemSupplier
	if err := IterPages[ItemSupplier](ctx, c, "/api/inventory/item-suppliers/", q, func(batch []ItemSupplier) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// ListItemSuppliersForItem loads every supplier link for one inventory item,
// primary-first (the viewset orders by -is_primary, unit_cost). It pages through
// the full set so the item-suppliers management view never drops a link that
// happens to sort onto a later page. `item_id` is the viewset's item filter.
func (c *Client) ListItemSuppliersForItem(ctx context.Context, itemID string) ([]ItemSupplier, error) {
	var all []ItemSupplier
	q := url.Values{}
	q.Set("item_id", itemID)
	if err := IterPages[ItemSupplier](ctx, c, "/api/inventory/item-suppliers/", q, func(batch []ItemSupplier) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// ItemSupplierWrite is the create/edit payload for one item↔supplier link. It
// mirrors the web SupplierRelationshipForm's field set (supplier, supplier_sku,
// supplier_url, unit_cost, package_cost, quantity_per_package, average_lead_time,
// is_primary) plus the owning item. A few wire-contract details match the
// ItemSupplierViewSet / ItemSupplier model deliberately:
//
//   - Item is the owning item's UUID and Supplier the supplier pk. Both are sent
//     on every create/edit; the serializer's unique_together (item, supplier)
//     validator needs the pair, and re-sending the same item on a PATCH is a
//     no-op. (unique_together means the same supplier can't be linked twice — the
//     backend 400s, which the form surfaces.)
//
//   - SupplierSKU has no blank=True on the model, so it is required and non-blank;
//     the form validates it before submit.
//
//   - UnitCost / PackageCost are nullable decimals sent as strings and carry NO
//     omitempty, so clearing one on edit sends an explicit null (mirroring the
//     web's `value || null`). The server compares both values with the stored
//     row, so omitting either echoed value would change the meaning of the
//     write. OpenMakerSuite's `inventory.services.suppliers.derive_costs` owns
//     the derivation rule; do not duplicate that rule here.
//
//   - QuantityPerPackage is a plain int carrying the model default (1); always
//     sent.
//
//   - AverageLeadTime is a POINTER with omitempty, and nil is a write of its own:
//     it OMITS the key, so OMS preserves it on PATCH or stores the planning
//     default labelled `default` on POST. Sending the 7 instead stores the same
//     number labelled a recorded quote (LeadTimeSource), which is why forms do
//     not restate an untouched default or an unchanged edit value.
//
//   - IsPrimary carries no omitempty so turning it off actually reaches the
//     backend instead of being dropped; the model's save() keeps a single primary
//     per item (setting one unsets the others).
//
//   - Version is the token the edited copy was LOADED at, and omitempty on
//     purpose: zero is "no copy was loaded" (a create) or "the server served no
//     token", and both must leave the key out — a create ignores it and an OMS
//     before #1091 has no token to check. Anything else is the copy's own
//     version and nothing else: never a version read back after a refusal
//     (item_supplier_version.go says why).
type ItemSupplierWrite struct {
	Item               string  `json:"item"`
	Supplier           int     `json:"supplier"`
	SupplierSKU        string  `json:"supplier_sku"`
	SupplierURL        string  `json:"supplier_url"`
	UnitCost           *string `json:"unit_cost"`
	PackageCost        *string `json:"package_cost"`
	QuantityPerPackage int     `json:"quantity_per_package"`
	AverageLeadTime    *int    `json:"average_lead_time,omitempty"`
	IsPrimary          bool    `json:"is_primary"`
	Version            int     `json:"version,omitempty"`
}

// CreateItemSupplier POSTs a new item↔supplier link. The response echoes the
// created row (with supplier_name and the derived cost populated).
func (c *Client) CreateItemSupplier(ctx context.Context, body ItemSupplierWrite) (*ItemSupplier, error) {
	var out ItemSupplier
	if err := c.Post(ctx, "/api/inventory/item-suppliers/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateItemSupplier PATCHes an existing link. PATCH (not PUT) matches the web;
// the editable field set is sent, with pointer fields omitted when unchanged,
// and body.Version states the copy the edit was made from — a link written since
// is refused rather than overwritten (AsStaleSupplierLink).
func (c *Client) UpdateItemSupplier(ctx context.Context, id int, body ItemSupplierWrite) (*ItemSupplier, error) {
	var out ItemSupplier
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/item-suppliers/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetItemSupplierPrimary flips one link to primary via a targeted partial PATCH
// (only is_primary). The model's save() unsets the previous primary for the item.
// A partial PATCH is safe: on an update DRF fills the unique_together fields from
// the instance, so item/supplier need not be re-sent.
//
// version is the token the row was loaded at, sent whenever it is positive. The
// operator chose this link as primary from what the list SHOWED them, so a
// link written since — discontinued, re-priced, or demoted by another promotion,
// which moves its version on too — is refused rather than promoted blind.
func (c *Client) SetItemSupplierPrimary(ctx context.Context, id, version int) (*ItemSupplier, error) {
	var out ItemSupplier
	body := map[string]any{"is_primary": true}
	if version > 0 {
		body["version"] = version
	}
	if err := c.Patch(ctx, fmt.Sprintf("/api/inventory/item-suppliers/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteItemSupplier removes an item↔supplier link (DELETE …/item-suppliers/{id}/).
//
// version rides as `?version=N` whenever it is positive — a DELETE carries no
// body, and the query value is where OMS reads it — so removing a link somebody
// has written since the list was loaded is refused (AsStaleSupplierLink) rather
// than destroying a quote the operator never saw.
func (c *Client) DeleteItemSupplier(ctx context.Context, id, version int) error {
	path := fmt.Sprintf("/api/inventory/item-suppliers/%d/", id)
	if version > 0 {
		path += "?version=" + strconv.Itoa(version)
	}
	return c.Delete(ctx, path)
}

// ListAssetsForSupplier is the matching wrapper for the assets-from-
// supplier picker. Backend supports both ?manufacturer= and ?search=
// (over name / description / serial_number / asset_tag /
// manufacturer_name), so the picker can issue real server-side
// searches as the warden types.
func (c *Client) ListAssetsForSupplier(ctx context.Context, supplierID int, search string, page int) (*Page[Asset], error) {
	q := url.Values{}
	q.Set("manufacturer", strconv.Itoa(supplierID))
	if search != "" {
		q.Set("search", search)
	}
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	return c.ListAssets(ctx, q)
}

// Fixture and the fixtures/{id}/scan/ response both key on a UUID (the Fixture
// PK and the FixtureRefillRequest the scan action returns are both UUIDField),
// so ID is `any`/string — the old `int` typing crashed the decode, and the
// old `id int` + `%d` path could never address a UUID-keyed fixture.
type Fixture struct {
	ID       any    `json:"id"`
	Name     string `json:"name"`
	Location *int   `json:"location,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (c *Client) ScanFixture(ctx context.Context, id string) (*Fixture, error) {
	var out Fixture
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/fixtures/%s/scan/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AssetReservation mirrors backend/inventory/serializers.AssetReservationSerializer.
type AssetReservation struct {
	ID                 string     `json:"id"`
	Asset              string     `json:"asset"`
	AssetName          string     `json:"asset_name,omitempty"`
	Title              string     `json:"title"`
	StartsAt           time.Time  `json:"starts_at"`
	EndsAt             time.Time  `json:"ends_at"`
	Notes              string     `json:"notes,omitempty"`
	ReservedBy         *int       `json:"reserved_by,omitempty"`
	ReservedByUsername string     `json:"reserved_by_username,omitempty"`
	CancelledAt        *time.Time `json:"cancelled_at,omitempty"`
	CancelledBy        *int       `json:"cancelled_by,omitempty"`
	IsCurrent          bool       `json:"is_current,omitempty"`
	CreatedAt          time.Time  `json:"created_at,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at,omitempty"`
}

// AssetOutOfService mirrors backend/inventory/serializers.AssetOutOfServiceSerializer.
type AssetOutOfService struct {
	ID               string     `json:"id"`
	Asset            string     `json:"asset"`
	AssetName        string     `json:"asset_name,omitempty"`
	Reason           string     `json:"reason"`
	PlacedOutAt      time.Time  `json:"placed_out_at"`
	PlacedBy         *int       `json:"placed_by,omitempty"`
	PlacedByUsername string     `json:"placed_by_username,omitempty"`
	ExpectedReturnAt *time.Time `json:"expected_return_at,omitempty"`
	RestoredAt       *time.Time `json:"restored_at,omitempty"`
	RestoredBy       *int       `json:"restored_by,omitempty"`
	IsOpen           bool       `json:"is_open,omitempty"`
	CreatedAt        time.Time  `json:"created_at,omitempty"`
	UpdatedAt        time.Time  `json:"updated_at,omitempty"`
}

func (c *Client) ListAssetReservations(ctx context.Context, q url.Values) (*Page[AssetReservation], error) {
	return GetPage[AssetReservation](ctx, c, "/api/inventory/asset-reservations/", q)
}

type CreateAssetReservation struct {
	Asset    string `json:"asset"`
	Title    string `json:"title"`
	StartsAt string `json:"starts_at"`
	EndsAt   string `json:"ends_at"`
	Notes    string `json:"notes,omitempty"`
}

func (c *Client) CreateAssetReservation(ctx context.Context, body CreateAssetReservation) (*AssetReservation, error) {
	var out AssetReservation
	if err := c.Post(ctx, "/api/inventory/asset-reservations/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CancelAssetReservation(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/asset-reservations/%s/", id))
}

func (c *Client) ListAssetOutOfService(ctx context.Context, q url.Values) (*Page[AssetOutOfService], error) {
	return GetPage[AssetOutOfService](ctx, c, "/api/inventory/asset-out-of-service/", q)
}

type OpenAssetOOS struct {
	Asset            string  `json:"asset"`
	Reason           string  `json:"reason"`
	ExpectedReturnAt *string `json:"expected_return_at,omitempty"`
}

func (c *Client) OpenAssetOutOfService(ctx context.Context, body OpenAssetOOS) (*AssetOutOfService, error) {
	var out AssetOutOfService
	if err := c.Post(ctx, "/api/inventory/asset-out-of-service/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) RestoreAssetOutOfService(ctx context.Context, id string) (*AssetOutOfService, error) {
	var out AssetOutOfService
	path := fmt.Sprintf("/api/inventory/asset-out-of-service/%s/restore/", id)
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
