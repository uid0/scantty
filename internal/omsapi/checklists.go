package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// ChecklistStep is one ordered step inside a Checklist definition. The
// step PK is UUID per [[scantty-api-field-drift]] note ("everything in
// inventory + checklists uses UUID"); the FKs to asset / inventory_item
// are UUIDs and the location is int.
type ChecklistStep struct {
	ID            string  `json:"id"`
	StepNumber    int     `json:"step_number"`
	Name          string  `json:"name"`
	Asset         *string `json:"asset"`
	Location      *int    `json:"location"`
	InventoryItem *string `json:"inventory_item"`
	Required      bool    `json:"required"`
	RequiresPhoto bool    `json:"requires_photo"`
	Notes         string  `json:"notes,omitempty"`
}

// ChecklistSummary mirrors ChecklistListSerializer — the lightweight
// shape returned by the list endpoint. `step_count` is a SerializerMethodField
// pre-joined for the list view.
type ChecklistSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SIG         *int   `json:"sig"`
	SIGName     string `json:"sig_name,omitempty"`
	IsActive    bool   `json:"is_active"`
	IsPublic    bool   `json:"is_public"`
	StepCount   int    `json:"step_count"`
}

// Checklist is the full detail shape — ChecklistSerializer with nested
// steps. The /detail/ action carries this; the plain GET on the list
// endpoint also returns this when fetched per-id.
type Checklist struct {
	ChecklistSummary
	CreatedBy         *int            `json:"created_by"`
	CreatedByUsername string          `json:"created_by_username,omitempty"`
	Steps             []ChecklistStep `json:"steps"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// ChecklistStepCompletion mirrors backend ChecklistStepCompletionSerializer
// — one scanned-step record inside a ChecklistCompletion. The scanned_*
// FKs match the step's prescribed target shape (asset / location / item;
// exactly one), with pre-joined display names so the dashboard renders
// without follow-up fetches.
type ChecklistStepCompletion struct {
	ID                  string    `json:"id"`
	Step                string    `json:"step"`
	StepName            string    `json:"step_name,omitempty"`
	StepNumber          int       `json:"step_number,omitempty"`
	ScannedAt           time.Time `json:"scanned_at"`
	ScannedAsset        *string   `json:"scanned_asset"`
	ScannedAssetName    string    `json:"scanned_asset_name,omitempty"`
	ScannedLocation     *int      `json:"scanned_location"`
	ScannedLocationName string    `json:"scanned_location_name,omitempty"`
	ScannedItem         *string   `json:"scanned_item"`
	ScannedItemName     string    `json:"scanned_item_name,omitempty"`
	Notes               string    `json:"notes,omitempty"`
	PhotoURL            *string   `json:"photo_url"`
	PhotoCaption        string    `json:"photo_caption,omitempty"`
}

// ChecklistCompletion is one in-progress or finished run of a
// checklist. Fields mirror ChecklistCompletionSerializer's read shape.
// step_completions is populated when the row is fetched per-id via
// GetChecklistCompletion; the list endpoint also returns it but
// callers usually don't need the full nested shape there.
type ChecklistCompletion struct {
	ID                     string                    `json:"id"`
	Checklist              string                    `json:"checklist"`
	ChecklistName          string                    `json:"checklist_name,omitempty"`
	User                   *int                      `json:"user"`
	UserUsername           string                    `json:"user_username,omitempty"`
	UserName               string                    `json:"user_name,omitempty"`
	StartedAt              *time.Time                `json:"started_at"`
	CompletedAt            *time.Time                `json:"completed_at"`
	Status                 string                    `json:"status"`
	StepCompletions        []ChecklistStepCompletion `json:"step_completions,omitempty"`
	CompletedStepsCount    int                       `json:"completed_steps_count"`
	TotalStepsCount        int                       `json:"total_steps_count"`
	RequiredStepsCompleted int                       `json:"required_steps_completed"`
	RequiredStepsTotal     int                       `json:"required_steps_total"`
	CreatedAt              time.Time                 `json:"created_at"`
	UpdatedAt              time.Time                 `json:"updated_at"`
}

// ChecklistStepScanRequest is the POST body for the /scan/ action on
// a completion. Exactly one of AssetID / LocationID / ItemID must be
// set, matching the prescribed target on the step (the backend rejects
// mismatches with a 400). Photo upload is intentionally absent here —
// the TUI can't capture images; steps with `requires_photo=true` need
// to be completed from the web instead.
type ChecklistStepScanRequest struct {
	StepID       string  `json:"step_id"`
	AssetID      *string `json:"asset_id,omitempty"`
	LocationID   *int    `json:"location_id,omitempty"`
	ItemID       *string `json:"item_id,omitempty"`
	Notes        string  `json:"notes,omitempty"`
	PhotoCaption string  `json:"photo_caption,omitempty"`
}

// ListChecklists returns the paginated checklist list. Useful filters:
// `?is_active=true`, `?sig=<id>`, `?search=<q>`.
//
// IT IS THE MANAGEMENT LIST, NOT THE LIST A MEMBER CAN RUN FROM.
// ChecklistViewSet.get_queryset hands staff, superusers and Logistics every
// checklist, a SIG admin their own SIGs' checklists, and EVERYONE ELSE
// `queryset.none()` — so an ordinary signed-in member reads an empty page here
// while public checklists exist (testdata/checklists_list_member.json, recorded
// beside checklists_available_member.json off the same database). The terminal
// used to draw its global checklist list off this endpoint and told such a member
// "No active checklists." ListAvailableChecklists is what the web runs from.
func (c *Client) ListChecklists(ctx context.Context, q url.Values) (*Page[ChecklistSummary], error) {
	return GetPage[ChecklistSummary](ctx, c, "/api/checklists/checklists/", q)
}

// ListAvailableChecklists returns every checklist the signed-in reader may RUN:
// the web's Facilities checklists page reads it (checklistsAPI.getAvailableChecklists
// with no params). ChecklistViewSet.available is AllowAny and decides the set
// itself — active only; public ones for everybody; plus every other for staff,
// superusers and Logistics; plus the reader's own SIGs' for a SIG admin — so the
// client sends no filter and keeps no copy of that rule.
//
// A PLAIN JSON ARRAY, NOT A PAGE. The action serializes the queryset directly and
// never paginates, so decoding it through GetPage would find no `results` and
// report an empty list.
func (c *Client) ListAvailableChecklists(ctx context.Context) ([]ChecklistSummary, error) {
	var out []ChecklistSummary
	if err := c.Get(ctx, "/api/checklists/checklists/available/", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListAssetChecklists, ListItemChecklists and ListLocationChecklists return the
// checklists with a STEP that scans that one record — the list the web's scan
// landing pages offer under "Are you completing a checklist?" (AssetScanPage,
// ScanPage and LocationScanPage, through assetsAPI.getAssetChecklists and
// inventoryAPI.getItemChecklists / getLocationChecklists).
//
// The three OMS actions (`checklists` on AssetViewSet, InventoryItemViewSet and
// LocationViewSet) are one body with the step FK changed, AllowAny, and they
// apply the same reader filter `available/` does: active only, public for
// everybody, the rest for staff, superusers, Logistics and the owning SIG's
// admins. A checklist the reader may not run is therefore simply ABSENT, never
// served and refused — so a list here is the set the reader can start, and the
// refusal a start can still meet (the checklist changed between the list and the
// press) comes back from StartChecklist in the server's own words.
//
// Each is a plain JSON array, like ListAvailableChecklists. `available/` also
// takes `asset_id` / `item_id` / `location_id` and filters the same way, but no
// web page sends them; these per-record routes are what the web calls, and so
// what this client calls.
func (c *Client) ListAssetChecklists(ctx context.Context, assetID string) ([]ChecklistSummary, error) {
	return c.recordChecklists(ctx, fmt.Sprintf("/api/inventory/assets/%s/checklists/", assetID), nil)
}

// ListItemChecklists sends ?include_kits=true for the reason includeKitsQuery
// gives: the action resolves the item through InventoryItemViewSet.get_object,
// whose queryset excludes kits, so without it a KIT's id is a flat 404 here
// (testdata/checklists_item_kit_without_include_kits.json) — and a checklist step
// may name a kit like any other item (checklists_item_kit.json is one).
func (c *Client) ListItemChecklists(ctx context.Context, itemID string) ([]ChecklistSummary, error) {
	return c.recordChecklists(ctx, fmt.Sprintf("/api/inventory/items/%s/checklists/", itemID), includeKitsValues())
}

// ListLocationChecklists takes the location's integer pk, the type Location.ID
// carries, so the path is formatted with %d and never through an `any`.
func (c *Client) ListLocationChecklists(ctx context.Context, locationID int) ([]ChecklistSummary, error) {
	return c.recordChecklists(ctx, fmt.Sprintf("/api/inventory/locations/%d/checklists/", locationID), nil)
}

func (c *Client) recordChecklists(ctx context.Context, path string, q url.Values) ([]ChecklistSummary, error) {
	var out []ChecklistSummary
	if err := c.Get(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetChecklist returns the full detail (with steps) for one checklist.
// Uses the /detail/ action which is AllowAny — viewing a checklist
// doesn't require auth, only modifying it does.
func (c *Client) GetChecklist(ctx context.Context, id string) (*Checklist, error) {
	var out Checklist
	if err := c.Get(ctx, fmt.Sprintf("/api/checklists/checklists/%s/detail/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartChecklist opens a new completion run for the given checklist.
// `userName` is the human-readable name to record on the run (anonymous
// flow); a signed-in user has their JWT applied server-side and can pass
// an empty string here.
func (c *Client) StartChecklist(ctx context.Context, checklistID, userName string) (*ChecklistCompletion, error) {
	body := map[string]string{}
	if userName != "" {
		body["user_name"] = userName
	}
	var out ChecklistCompletion
	if err := c.Post(ctx, fmt.Sprintf("/api/checklists/checklists/%s/start/", checklistID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListChecklistCompletions returns recent runs. Useful filters:
// `?checklist=<id>` for one definition's history, `?status=in_progress`
// for resumable runs.
func (c *Client) ListChecklistCompletions(ctx context.Context, q url.Values) (*Page[ChecklistCompletion], error) {
	return GetPage[ChecklistCompletion](ctx, c, "/api/checklists/completions/", q)
}

// GetChecklistCompletion fetches one completion with its nested
// step_completions populated. Used by the stepper to render which
// steps still need scanning.
func (c *Client) GetChecklistCompletion(ctx context.Context, id string) (*ChecklistCompletion, error) {
	var out ChecklistCompletion
	if err := c.Get(ctx, fmt.Sprintf("/api/checklists/completions/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ScanChecklistStep records a scan against one step in a completion.
// The backend cross-checks the prescribed target on the step against
// the asset_id / location_id / item_id supplied here; mismatches
// (including missing required photos) return 400 with a `detail`
// message that surfaces through the standard APIError envelope.
func (c *Client) ScanChecklistStep(ctx context.Context, completionID string, req ChecklistStepScanRequest) (*ChecklistCompletion, error) {
	var out ChecklistCompletion
	if err := c.Post(ctx, fmt.Sprintf("/api/checklists/completions/%s/scan/", completionID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CompleteChecklist finalizes a checklist run. The backend allows the
// call once all required steps have been scanned; missing required
// steps return 400. Returns the updated completion with
// completed_at + status set.
func (c *Client) CompleteChecklist(ctx context.Context, completionID string) (*ChecklistCompletion, error) {
	var out ChecklistCompletion
	if err := c.Post(ctx, fmt.Sprintf("/api/checklists/completions/%s/complete/", completionID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
