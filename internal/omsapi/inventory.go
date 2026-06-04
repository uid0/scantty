package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type LookupResult struct {
	Type     string `json:"type"`
	ID       any    `json:"id"`
	Name     string `json:"name"`
	SKU      string `json:"sku,omitempty"`
	Location string `json:"location,omitempty"`
	Code     string `json:"code,omitempty"`
}

func (c *Client) LookupCode(ctx context.Context, code string) (*LookupResult, error) {
	if code == "" {
		return nil, &APIError{Code: "invalid_code", Message: "code is empty"}
	}
	var out LookupResult
	q := url.Values{"code": []string{strings.ToUpper(code)}}
	if err := c.Get(ctx, "/api/inventory/lookup-code/", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type Item struct {
	ID                   string         `json:"id"`
	Name                 string         `json:"name"`
	SKU                  string         `json:"sku"`
	Description          string         `json:"description,omitempty"`
	Category             *int           `json:"category,omitempty"`
	CategoryName         string         `json:"category_name,omitempty"`
	Location             string         `json:"location,omitempty"`
	Stock                int            `json:"current_stock"`
	MinimumStock         int            `json:"minimum_stock,omitempty"`
	ReorderQuantity      int            `json:"reorder_quantity,omitempty"`
	NeedsReorder         bool           `json:"needs_reorder,omitempty"`
	ReorderStatus        string         `json:"reorder_status,omitempty"`
	HasPendingReorder    bool           `json:"has_pending_reorder,omitempty"`
	ExpectedDeliveryDate string         `json:"expected_delivery_date,omitempty"`
	SupplierName         string         `json:"supplier_name,omitempty"`
	SupplierSKU          string         `json:"supplier_sku,omitempty"`
	SupplierURL          string         `json:"supplier_url,omitempty"`
	UnitCost             DecimalString  `json:"unit_cost,omitempty"`
	PackageCost          DecimalString  `json:"package_cost,omitempty"`
	QuantityPerPackage   int            `json:"quantity_per_package,omitempty"`
	AverageLeadTime      int            `json:"average_lead_time,omitempty"`
	TotalValue           DecimalString  `json:"total_value,omitempty"`
	ThumbnailURL         string         `json:"thumbnail,omitempty"`
	QRCodeURL            string         `json:"qr_code_url,omitempty"`
	UseCaseBasedReorder  bool           `json:"use_case_based_reorder,omitempty"`
	MinimumCases         *float64       `json:"minimum_cases,omitempty"`
	ReorderCases         *float64       `json:"reorder_cases,omitempty"`
	CurrentCases         *float64       `json:"current_cases,omitempty"`
	ReorderInstruction   string         `json:"reorder_instruction,omitempty"`
	Suppliers            []ItemSupplier `json:"suppliers,omitempty"`
	Tags                 []string       `json:"tags,omitempty"`
	CreatedAt            time.Time      `json:"created_at,omitempty"`
	UpdatedAt            time.Time      `json:"updated_at,omitempty"`
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
	PowerDrawWatts       *int   `json:"power_draw_watts,omitempty"`
	WiringType           string `json:"wiring_type,omitempty"`
	Suite                string `json:"suite,omitempty"`
	ElectricalBox        string `json:"electrical_box,omitempty"`
	BreakerLocation      string `json:"breaker_location,omitempty"`
	HasInterlock         bool   `json:"has_interlock,omitempty"`
	InterlockType        string `json:"interlock_type,omitempty"`
	InterlockResponsible string `json:"interlock_responsible,omitempty"`
	LockoutType          string `json:"lockout_type,omitempty"`
	LockoutInstructions  string `json:"lockout_instructions,omitempty"`
	LockoutResponsible   string `json:"lockout_responsible,omitempty"`
	HasNetworkDrop       bool   `json:"has_network_drop,omitempty"`
	NetworkDropLocation  string `json:"network_drop_location,omitempty"`
	IsForgeKeyManaged    bool   `json:"is_forgekey_managed,omitempty"`

	// Scanning
	LastScannedAt *time.Time `json:"last_scanned_at,omitempty"`

	// Ownership
	OwningGroup     *int   `json:"owning_group,omitempty"`
	OwningGroupName string `json:"owning_group_name,omitempty"`
	OwningUserName  string `json:"owning_user_name,omitempty"`
	GroupsCanEnable []int  `json:"groups_can_enable,omitempty"`

	// ForgeKey runtime
	OperationalMode map[string]any `json:"operational_mode,omitempty"`
	IsLocked        bool           `json:"is_locked,omitempty"`
	LockoutInfo     *AssetLockout  `json:"lockout_info,omitempty"`
	CanEnable       bool           `json:"can_enable,omitempty"`
	CanUnlock       bool           `json:"can_unlock,omitempty"`

	// Metadata
	IsActive   bool      `json:"is_active,omitempty"`
	ReportOnly bool      `json:"report_only,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
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

type Location struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Parent   *int   `json:"parent,omitempty"`
	Code     string `json:"code,omitempty"`
	Capacity int    `json:"capacity,omitempty"`
}

func (c *Client) ListLocations(ctx context.Context, q url.Values) (*Page[Location], error) {
	return GetPage[Location](ctx, c, "/api/inventory/locations/", q)
}

type Category struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Parent *int   `json:"parent,omitempty"`
}

func (c *Client) ListCategories(ctx context.Context, q url.Values) (*Page[Category], error) {
	return GetPage[Category](ctx, c, "/api/inventory/categories/", q)
}

type Supplier struct {
	ID                    int           `json:"id"`
	Name                  string        `json:"name"`
	SupplierType          string        `json:"supplier_type,omitempty"`
	Website               string        `json:"website,omitempty"`
	AccountNumber         string        `json:"account_number,omitempty"`
	TaxFreePaperworkFiled bool          `json:"tax_free_paperwork_filed,omitempty"`
	Notes                 string        `json:"notes,omitempty"`
	ItemCount             int           `json:"item_count,omitempty"`
	PurchaseOrderCount    int           `json:"purchase_order_count,omitempty"`
	TotalSpent            DecimalString `json:"total_spent,omitempty"`
	CreatedAt             time.Time     `json:"created_at,omitempty"`
	UpdatedAt             time.Time     `json:"updated_at,omitempty"`
}

func (c *Client) ListSuppliers(ctx context.Context, q url.Values) (*Page[Supplier], error) {
	return GetPage[Supplier](ctx, c, "/api/inventory/suppliers/", q)
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
