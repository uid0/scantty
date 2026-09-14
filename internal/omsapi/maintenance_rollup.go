package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// maintenance_rollup.go — the preventive-maintenance ROLLUP half of
// OpenMakerSuite's inventory app: what is due, generating work orders for all
// of it at once, the maintenance dashboard, and an asset's maintenance history
// with the backdated records that feed it.
//
// Every wire type here was read off the line of Python that BUILDS the body and
// then checked against a body recorded from a running OMS
// (testdata/maintenance_*.json, provenance in testdata/README.md). Three
// payloads are hand-built dicts drf-spectacular cannot see, and they mix
// timestamp spellings: `maintenance/dashboard/` and `maintenance/active/` write
// `.isoformat()` (`…+00:00`) while the due lists go through a serializer
// (`…Z`). Both decode as RFC 3339, so nothing here cares — but a fixture
// written by hand in one spelling would not have noticed the other.

// ListMaintenanceItemsDueThisWeek is `GET maintenance-items/due_this_week/`.
//
// THE WINDOW IS ROLLING AND IT INCLUDES THE PAST. OMS keeps an ACTIVE item
// with an interval when `next_due_at` is on or before now + 7 days — so every
// OVERDUE item is here, and so is every item never completed (its `next_due_at`
// is null and the view keeps nulls). It is not a calendar week. The body is a
// bare array, not a page; MaybeList tolerates either because the web does.
func (c *Client) ListMaintenanceItemsDueThisWeek(ctx context.Context) ([]MaintenanceItem, error) {
	return c.listMaintenanceItemsDue(ctx, "due_this_week/")
}

// ListMaintenanceItemsDueThisMonth is `GET maintenance-items/due_this_month/`:
// the same predicate at now + 30 days, so it is a SUPERSET of the week's list.
// The web draws its "Due this month" section as this list less the week's, and
// a client that drew both whole would show every weekly item twice.
func (c *Client) ListMaintenanceItemsDueThisMonth(ctx context.Context) ([]MaintenanceItem, error) {
	return c.listMaintenanceItemsDue(ctx, "due_this_month/")
}

func (c *Client) listMaintenanceItemsDue(ctx context.Context, action string) ([]MaintenanceItem, error) {
	var out MaybeList[MaintenanceItem]
	if err := c.Get(ctx, maintenanceItemsPath+action, nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// MaintenanceBulkGeneration is what `generate_work_orders_bulk/` answers.
//
// IT CARRIES NO PER-ITEM BREAKDOWN, and a client must not invent one. The server
// picks the items itself — the due-this-week predicate above — and SKIPS any
// item that already has a work order in `open` or `in_progress`; a skipped
// item is simply absent from `work_order_ids`. So `Created` below the number of
// items the operator saw due is an ordinary answer, not a partial failure: the
// whole run is one transaction, and a failure rolls every row back.
type MaintenanceBulkGeneration struct {
	Created      int      `json:"created"`
	WorkOrderIDs []string `json:"work_order_ids"`
}

// GenerateDueMaintenanceWorkOrders is `POST maintenance-items/generate_work_orders_bulk/`.
//
// THE BODY IS IGNORED — the server chooses the items — so the client sends `{}`,
// which is what the web sends. It answers 201 even when it created nothing.
// The action is `IsAuthenticated` and nothing more: any signed-in member may
// run it, which is why the web offers the button to everyone.
func (c *Client) GenerateDueMaintenanceWorkOrders(ctx context.Context) (*MaintenanceBulkGeneration, error) {
	var out MaintenanceBulkGeneration
	if err := c.Post(ctx, maintenanceItemsPath+"generate_work_orders_bulk/", struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MaintenanceDashboard is `GET maintenance/dashboard/`, hand-built in
// `MaintenanceDashboardViewSet.dashboard`.
type MaintenanceDashboard struct {
	// ScheduledPM is EVERY active item with an interval — no window — sorted by
	// days until due with the never-completed ones last.
	ScheduledPM []MaintenanceScheduledPM `json:"scheduled_pm"`
	// Unscheduled is open, in-progress and blocked work orders with no PM
	// template (or a template with no interval), oldest first.
	Unscheduled []MaintenanceUnscheduledWorkOrder `json:"unscheduled"`
	Costs       MaintenanceCosts                  `json:"costs"`
}

// MaintenanceScheduledPM is one `scheduled_pm` row.
type MaintenanceScheduledPM struct {
	AssetID           string     `json:"asset_id"`
	AssetName         string     `json:"asset_name"`
	MaintenanceItemID string     `json:"maintenance_item_id"`
	Title             string     `json:"title"`
	IntervalDays      int        `json:"interval_days"`
	NextDue           *time.Time `json:"next_due"`
	// DaysUntil is next_due's DATE less today's, both UTC — negative once
	// overdue, and null for an item never completed.
	DaysUntil       *int       `json:"days_until"`
	LastCompletedAt *time.Time `json:"last_completed_at"`
	IsOverdue       bool       `json:"is_overdue"`
}

// MaintenanceUnscheduledWorkOrder is one `unscheduled` row. AssetName is ""
// rather than null when the work order has no asset.
type MaintenanceUnscheduledWorkOrder struct {
	WorkOrderID string    `json:"workorder_id"`
	ShortID     string    `json:"short_id"`
	AssetID     *string   `json:"asset_id"`
	AssetName   string    `json:"asset_name"`
	Problem     string    `json:"problem"`
	OpenedAt    time.Time `json:"opened_at"`
	Status      string    `json:"status"`
}

// MaintenanceCosts is the dashboard's cost block. Every figure is a Decimal the
// view `str()`s, so a string on the wire.
//
// THE PERIODS ARE CALENDAR PERIODS, unlike the due lists: this_week runs from
// Monday, this_month from the 1st, all in UTC, and only COMPLETED work orders
// count. Backdated maintenance RECORDS never feed these figures.
type MaintenanceCosts struct {
	PerPeriod MaintenanceCostPeriods `json:"per_period"`
	ByAsset   []MaintenanceAssetCost `json:"by_asset"`
}

// MaintenanceCostPeriods is `costs.per_period`.
type MaintenanceCostPeriods struct {
	Today     DecimalString `json:"today"`
	ThisWeek  DecimalString `json:"this_week"`
	ThisMonth DecimalString `json:"this_month"`
	ThisYear  DecimalString `json:"this_year"`
	AllTime   DecimalString `json:"all_time"`
}

// MaintenanceAssetCost is one `costs.by_asset` row: the trailing 90 days,
// assets with neither cost nor downtime omitted, dearest first.
type MaintenanceAssetCost struct {
	AssetID                 string        `json:"asset_id"`
	AssetName               string        `json:"asset_name"`
	TotalCost               DecimalString `json:"total_cost"`
	DaysInMaintenance90Days int           `json:"days_in_maintenance_90d"`
}

// GetMaintenanceDashboard is `GET maintenance/dashboard/` (IsAuthenticated).
func (c *Client) GetMaintenanceDashboard(ctx context.Context) (*MaintenanceDashboard, error) {
	var out MaintenanceDashboard
	if err := c.Get(ctx, "/api/inventory/maintenance/dashboard/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ActiveMaintenance is `GET maintenance/active/`. It carries `count` beside
// `results` but it is NOT DRF pagination — there is no `next` and every row is
// in the one body.
type ActiveMaintenance struct {
	Results []ActiveMaintenanceRow `json:"results"`
	Count   int                    `json:"count"`
}

// Kinds an ActiveMaintenanceRow can be, spelled as the server spells them.
const (
	ActiveKindWorkOrder       = "work_order"
	ActiveKindAssetProblem    = "asset_problem"
	ActiveKindLocationProblem = "location_problem"
)

// ActiveMaintenanceRow is one open piece of maintenance work: a work order, or
// an asset or location problem nobody has promoted to one yet.
//
// LocationID IS AN INTEGER — `Location` is the one BigAutoField pk in this body,
// every other id is a UUID string — and it is never spent as a path segment
// here. AssetName is "" (not null) on a work order with no asset, and null on a
// location problem; the web draws the first as a blank cell and the second as —.
type ActiveMaintenanceRow struct {
	Kind          string    `json:"kind"`
	ID            string    `json:"id"`
	ShortID       string    `json:"short_id"`
	Title         string    `json:"title"`
	Status        string    `json:"status"`
	StatusDisplay string    `json:"status_display"`
	AssetID       *string   `json:"asset_id"`
	AssetName     *string   `json:"asset_name"`
	LocationID    *int      `json:"location_id"`
	LocationName  *string   `json:"location_name"`
	Severity      *string   `json:"severity"`
	DueDate       DateOnly  `json:"due_date"`
	OpenedAt      time.Time `json:"opened_at"`
}

// ListActiveMaintenance is `GET maintenance/active/` (IsAuthenticated).
func (c *Client) ListActiveMaintenance(ctx context.Context) (*ActiveMaintenance, error) {
	var out ActiveMaintenance
	if err := c.Get(ctx, "/api/inventory/maintenance/active/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Sources a maintenance-history row can come from, spelled as the server spells
// them — and, for the query, `all` as well.
const (
	MaintenanceSourceAll        = "all"
	MaintenanceSourceHistorical = "historical"
	MaintenanceSourceWorkOrder  = "workorder"
)

// MaintenanceHistoryQuery narrows `assets/<id>/maintenance-history/`. Since and
// Until are inclusive `YYYY-MM-DD` dates on `completed_on`; empty sends nothing.
type MaintenanceHistoryQuery struct {
	Since  string
	Until  string
	Source string
}

func (q MaintenanceHistoryQuery) values() url.Values {
	v := url.Values{}
	if q.Since != "" {
		v.Set("since", q.Since)
	}
	if q.Until != "" {
		v.Set("until", q.Until)
	}
	if q.Source != "" {
		v.Set("source", q.Source)
	}
	return v
}

// MaintenanceHistory is `GET assets/<id>/maintenance-history/`.
//
// TotalCost is `str()` of a Decimal sum, so it is "0" — not "0.00" — when no
// row in range carried a cost. Not paginated: every row in range is here.
type MaintenanceHistory struct {
	Count     int                       `json:"count"`
	TotalCost DecimalString             `json:"total_cost"`
	Results   []MaintenanceHistoryEntry `json:"results"`
}

// MaintenanceHistoryEntry is one row of an asset's maintenance history.
//
// TWO SOURCES SHARE THE SHAPE AND ONLY ONE OF THEM IS A RECORD. `historical`
// rows are MaintenanceRecords (the backdated log, editable through
// maintenance-records/); `workorder` rows are CLOSED third-party work orders,
// whose id is a work order's and must never be PATCHed as a record's.
type MaintenanceHistoryEntry struct {
	ID            string                 `json:"id"`
	Source        string                 `json:"source"`
	Title         string                 `json:"title"`
	Description   string                 `json:"description"`
	CompletedOn   DateOnly               `json:"completed_on"`
	PerformedBy   MaintenancePerformedBy `json:"performed_by"`
	Cost          DecimalString          `json:"cost"`
	InvoiceNumber string                 `json:"invoice_number"`
	Notes         string                 `json:"notes"`
	AttachmentURL *string                `json:"attachment_url"`
	DetailURL     *string                `json:"detail_url"`
}

// MaintenancePerformedBy names who did the work: a vendor, a staff member, or
// (a server-side rule on records) at least one of the two.
type MaintenancePerformedBy struct {
	Vendor       *MaintenanceVendorRef `json:"vendor"`
	InternalUser *MaintenanceUserRef   `json:"internal_user"`
}

// MaintenanceVendorRef is a vendor as the history names it (UUID pk).
type MaintenanceVendorRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// MaintenanceUserRef is a user as the history names it. The pk is an INTEGER.
type MaintenanceUserRef struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// GetAssetMaintenanceHistory is `GET assets/<id>/maintenance-history/`.
//
// Its refusals are hand-built `{"detail": …}` bodies (a bad date, a bad
// source), so AsDetailRefusal recovers the sentence. The date one is
// `str()` of a Django ValidationError and arrives WITH its list brackets —
// `['since must be a YYYY-MM-DD date']` — which is the server's own sentence
// and is relayed as it is.
func (c *Client) GetAssetMaintenanceHistory(ctx context.Context, assetID string, q MaintenanceHistoryQuery) (*MaintenanceHistory, error) {
	var out MaintenanceHistory
	path := fmt.Sprintf("/api/inventory/assets/%s/maintenance-history/", url.PathEscape(assetID))
	if err := c.Get(ctx, path, q.values(), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MaintenanceRecord mirrors MaintenanceRecordSerializer — one backdated entry
// in an asset's maintenance log.
//
// The user pks (`performed_by_internal`, `recorded_by`) are INTEGERS and every
// other id a UUID string. Cost is a DecimalField, so a string or null.
type MaintenanceRecord struct {
	ID                          string        `json:"id"`
	Asset                       string        `json:"asset"`
	AssetName                   string        `json:"asset_name"`
	Title                       string        `json:"title"`
	Description                 string        `json:"description"`
	CompletedOn                 DateOnly      `json:"completed_on"`
	Vendor                      *string       `json:"vendor"`
	VendorName                  *string       `json:"vendor_name"`
	PerformedByInternal         *int          `json:"performed_by_internal"`
	PerformedByInternalUsername *string       `json:"performed_by_internal_username"`
	Cost                        DecimalString `json:"cost"`
	InvoiceNumber               string        `json:"invoice_number"`
	AttachmentURL               *string       `json:"attachment_url"`
	Notes                       string        `json:"notes"`
	RecordedBy                  *int          `json:"recorded_by"`
	RecordedByUsername          *string       `json:"recorded_by_username"`
	RecordedAt                  time.Time     `json:"recorded_at"`
	UpdatedAt                   time.Time     `json:"updated_at"`
}

// MaintenanceRecordWrite is the CREATE body, and it is the web's body.
//
// Vendor, PerformedByInternal and Cost have NO omitempty, so an unset one goes
// as an explicit null — what the web sends, and what the serializer reads as
// "not recorded". The server requires asset, title, a non-blank description and
// completed_on; refuses a completed_on later than its own (UTC) date; and
// refuses a record with neither a vendor nor an internal performer. Those
// refusals arrive in the standard validation envelope, so AsFieldRefusal
// recovers the field's own sentence.
//
// The attachment is not here: it is a multipart file, and no terminal flow
// uploads one to a record.
type MaintenanceRecordWrite struct {
	Asset               string  `json:"asset"`
	Title               string  `json:"title"`
	Description         string  `json:"description"`
	CompletedOn         string  `json:"completed_on"`
	Vendor              *string `json:"vendor"`
	PerformedByInternal *int    `json:"performed_by_internal"`
	Cost                *string `json:"cost"`
	InvoiceNumber       string  `json:"invoice_number"`
	Notes               string  `json:"notes"`
}

const maintenanceRecordsPath = "/api/inventory/maintenance-records/"

// CreateMaintenanceRecord is `POST maintenance-records/`.
//
// Writes are staff, Logistics or SIG-admin on the server
// (IsAuthenticatedOrStaffSigAdminWrite); a plain member is answered 403. The
// only side effect is `recorded_by`: a record does NOT move a PM item's
// last-completed date, create a work order, or feed the dashboard's costs.
func (c *Client) CreateMaintenanceRecord(ctx context.Context, body MaintenanceRecordWrite) (*MaintenanceRecord, error) {
	var out MaintenanceRecord
	if err := c.Post(ctx, maintenanceRecordsPath, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMaintenanceRecordNotes is `PATCH maintenance-records/<id>/` with
// `{"notes": …}` and nothing else.
//
// THE SERVER WOULD ACCEPT MORE — every field but the ids and stamps is writable
// — and the web edits only the notes (and the attachment). A PATCH naming only
// `notes` leaves every other stored value alone, which is what keeps an edit
// made from a stale row from rewriting a date or a cost somebody else corrected.
func (c *Client) UpdateMaintenanceRecordNotes(ctx context.Context, id, notes string) (*MaintenanceRecord, error) {
	var out MaintenanceRecord
	body := struct {
		Notes string `json:"notes"`
	}{Notes: notes}
	if err := c.Patch(ctx, maintenanceRecordsPath+url.PathEscape(id)+"/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
