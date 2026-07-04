package omsapi

import (
	"context"
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
	AverageLeadTime      int           `json:"average_lead_time,omitempty"`
	TotalValue           DecimalString `json:"total_value,omitempty"`
	ThumbnailURL         string        `json:"thumbnail,omitempty"`
	QRCodeURL            string        `json:"qr_code_url,omitempty"`
	UseCaseBasedReorder  bool          `json:"use_case_based_reorder,omitempty"`
	MinimumCases         *float64      `json:"minimum_cases,omitempty"`
	ReorderCases         *float64      `json:"reorder_cases,omitempty"`
	CurrentCases         *float64      `json:"current_cases,omitempty"`
	ReorderInstruction   string        `json:"reorder_instruction,omitempty"`
	// IsSerialized marks an item whose stock is tracked as individual
	// serial-numbered units (SerializedComponent). SerialTrackingMode is
	// "consumable" or "reusable" and drives which lifecycle transitions are
	// legal on those units.
	IsSerialized       bool   `json:"is_serialized,omitempty"`
	SerialTrackingMode string `json:"serial_tracking_mode,omitempty"`

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
	Notes         string `json:"notes,omitempty"`
	Image         string `json:"image,omitempty"`

	Suppliers []ItemSupplier `json:"suppliers,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
	UpdatedAt time.Time      `json:"updated_at,omitempty"`
}

func (c *Client) ListItems(ctx context.Context, q url.Values) (*Page[Item], error) {
	return GetPage[Item](ctx, c, "/api/inventory/items/", q)
}

func (c *Client) GetItem(ctx context.Context, id string) (*Item, error) {
	var out Item
	if err := c.Get(ctx, fmt.Sprintf("/api/inventory/items/%s/", id), nil, &out); err != nil {
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

	IsActive bool    `json:"is_active"`
	Notes    *string `json:"notes,omitempty"`
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
func (c *Client) DeleteInventoryItem(ctx context.Context, id string) error {
	return c.Delete(ctx, fmt.Sprintf("/api/inventory/items/%s/", id))
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
	Circuit            string `json:"circuit,omitempty"`
	MACAddress         string `json:"mac_address,omitempty"`
	NeedsCompressedAir bool   `json:"needs_compressed_air,omitempty"`
	NeedsVentilation   bool   `json:"needs_ventilation,omitempty"`
	IsChargeable       bool   `json:"is_chargeable,omitempty"`

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

type AssetPart struct {
	ID                      any            `json:"id"`
	Asset                   string         `json:"asset"`
	AssetName               string         `json:"asset_name,omitempty"`
	AssetTag                string         `json:"asset_tag,omitempty"`
	Part                    string         `json:"part"`
	PartName                string         `json:"part_name,omitempty"`
	PartSKU                 string         `json:"part_sku,omitempty"`
	QuantityNeeded          int            `json:"quantity_needed,omitempty"`
	IsRequired              bool           `json:"is_required,omitempty"`
	MaintenanceIntervalDays *int           `json:"maintenance_interval_days,omitempty"`
	LastReplacedAt          *time.Time     `json:"last_replaced_at,omitempty"`
	DaysSinceReplacement    *int           `json:"days_since_replacement,omitempty"`
	NeedsReplacement        bool           `json:"needs_replacement,omitempty"`
	Notes                   string         `json:"notes,omitempty"`
	PartDetails             map[string]any `json:"part_details,omitempty"`
	CreatedAt               time.Time      `json:"created_at,omitempty"`
	UpdatedAt               time.Time      `json:"updated_at,omitempty"`
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
// item's pk string for edit-mode form hydration. InventoryItem's primary key
// is a UUID (models.UUIDField), so the value comes over as a string; the
// float64 branch is defensive for any numeric-pk serializer shape. ok is false
// when the asset has no linked inventory-item type.
func (a *Asset) InventoryItemID() (string, bool) {
	switch v := a.InventoryItem.(type) {
	case string:
		if s := strings.TrimSpace(v); s != "" {
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

	NeedsCompressedAir     bool  `json:"needs_compressed_air"`
	NeedsVentilation       bool  `json:"needs_ventilation"`
	IsChargeable           bool  `json:"is_chargeable"`
	TrainingRequired       bool  `json:"training_required"`
	RequiredCertifications []int `json:"required_certifications,omitempty"`
	ReportOnly             bool  `json:"report_only"`

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

type ItemSupplier struct {
	ID                int           `json:"id"`
	Item              string        `json:"item"`
	ItemName          string        `json:"item_name,omitempty"`
	Supplier          int           `json:"supplier"`
	SupplierName      string        `json:"supplier_name,omitempty"`
	SupplierSKU       string        `json:"supplier_sku,omitempty"`
	URL               string        `json:"supplier_url,omitempty"`
	PackageUPC        string        `json:"package_upc,omitempty"`
	UnitUPC           string        `json:"unit_upc,omitempty"`
	PackQuantity      int           `json:"quantity_per_package,omitempty"`
	UnitCost          DecimalString `json:"unit_cost,omitempty"`
	PackageCost       DecimalString `json:"package_cost,omitempty"`
	LeadTimeDays      int           `json:"average_lead_time,omitempty"`
	IsPreferred       bool          `json:"is_primary,omitempty"`
	IsActive          bool          `json:"is_active,omitempty"`
	IsDiscontinued    bool          `json:"is_discontinued,omitempty"`
	PackageDimensions string        `json:"package_dimensions_display,omitempty"`
	Notes             string        `json:"notes,omitempty"`
	CreatedAt         time.Time     `json:"created_at,omitempty"`
	UpdatedAt         time.Time     `json:"updated_at,omitempty"`
}

func (c *Client) ListItemSuppliers(ctx context.Context, q url.Values) (*Page[ItemSupplier], error) {
	return GetPage[ItemSupplier](ctx, c, "/api/inventory/item-suppliers/", q)
}

// ListItemSuppliersForSupplier is a convenience wrapper for the
// supplier-scoped item picker in the New PO flow. It only loads
// active rows so the warden doesn't see discontinued lines.
//
// ItemSupplierViewSet does not accept ?search= on the backend today,
// so the picker filters client-side after fetch. Typical supplier
// catalogs are small enough that one or two pages cover everything.
func (c *Client) ListItemSuppliersForSupplier(ctx context.Context, supplierID int, page int) (*Page[ItemSupplier], error) {
	q := url.Values{}
	q.Set("supplier_id", strconv.Itoa(supplierID))
	q.Set("active_only", "true")
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	return c.ListItemSuppliers(ctx, q)
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

type Fixture struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Location *int   `json:"location,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (c *Client) ScanFixture(ctx context.Context, id int) (*Fixture, error) {
	var out Fixture
	if err := c.Post(ctx, fmt.Sprintf("/api/inventory/fixtures/%d/scan/", id), nil, &out); err != nil {
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
