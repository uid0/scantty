package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// MakerBox mirrors backend MakerBoxSerializer — one per-member bin
// assignment. The MakerBox primary key is an int (Django default
// autoid in the maker_boxes app, unlike inventory's UUIDs), so the id
// field stays an int per [[scantty-api-field-drift]].
//
// status values: "valid" | "grace" | "expired" | "unknown" |
// "pre_conversion" — pre_conversion rows have BinID="" (the JSON null
// the backend returns for nullable bin_id; decoded as empty string
// here). The DisplayName field is what scantty should render in the
// queue UI.
type MakerBox struct {
	ID                    int        `json:"id"`
	BinID                 string     `json:"bin_id"`
	AssignedUsername      string     `json:"assigned_username"`
	FirstName             string     `json:"first_name,omitempty"`
	LastName              string     `json:"last_name,omitempty"`
	Email                 string     `json:"email,omitempty"`
	DisplayName           string     `json:"display_name,omitempty"`
	AssignedAt            *time.Time `json:"assigned_at"`
	ExpiresAt             *time.Time `json:"expires_at"`
	LastVerifiedAt        *time.Time `json:"last_verified_at"`
	Status                string     `json:"status"`
	IdentitySource        string     `json:"identity_source,omitempty"`
	ConversionCompletedAt *time.Time `json:"conversion_completed_at"`
	PaidAt                *time.Time `json:"paid_at"`
	Notes                 string     `json:"notes,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

// MakerBoxScanResult is what the POST /scan/ action returns — a slim
// envelope describing whether the (bin, user) pairing is current,
// without exposing the full MakerBox row to anonymous scanners.
type MakerBoxScanResult struct {
	Status        string     `json:"status"`
	BinID         string     `json:"bin_id"`
	Username      string     `json:"username"`
	FirstName     string     `json:"first_name,omitempty"`
	LastName      string     `json:"last_name,omitempty"`
	Email         string     `json:"email,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at"`
	DaysRemaining *int       `json:"days_remaining"`
}

// ListMakerBoxes paginates the full directory. Useful filters:
// `?assigned_username=<u>` for member lookup, `?status=expired` for
// the renewal-overdue list.
func (c *Client) ListMakerBoxes(ctx context.Context, q url.Values) (*Page[MakerBox], error) {
	return GetPage[MakerBox](ctx, c, "/api/maker-boxes/", q)
}

// GetMakerBox fetches a single box by its integer id.
func (c *Client) GetMakerBox(ctx context.Context, id int) (*MakerBox, error) {
	var out MakerBox
	if err := c.Get(ctx, fmt.Sprintf("/api/maker-boxes/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ScanMakerBox posts a bin-id + username pair to the /scan/ action and
// returns the (valid/grace/expired/unknown) verdict. Used for the
// scanner-driven "is this person allowed to have this bin" check.
func (c *Client) ScanMakerBox(ctx context.Context, binID, username string) (*MakerBoxScanResult, error) {
	body := map[string]string{"bin_id": binID, "username": username}
	var out MakerBoxScanResult
	if err := c.Post(ctx, "/api/maker-boxes/scan/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MakerBoxLookupResult is what POST /lookup/ returns — an identity
// preview from the WHMCS + Common API cascade. Found=false means the
// query didn't match either backend; the other fields are empty in
// that case so the renderer can render one shape.
type MakerBoxLookupResult struct {
	Found            bool       `json:"found"`
	IdentitySource   string     `json:"identity_source"`
	Username         string     `json:"username"`
	FirstName        string     `json:"first_name"`
	LastName         string     `json:"last_name"`
	Email            string     `json:"email"`
	MembershipStatus string     `json:"membership_status"`
	ExpiresAt        *time.Time `json:"expires_at"`
	DaysRemaining    *int       `json:"days_remaining"`
}

// LookupMakerBoxIdentity is the dry-run identity probe behind the
// pre-conversion UI's live preview. No DB write.
func (c *Client) LookupMakerBoxIdentity(ctx context.Context, query string) (*MakerBoxLookupResult, error) {
	body := map[string]string{"query": query}
	var out MakerBoxLookupResult
	if err := c.Post(ctx, "/api/maker-boxes/lookup/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PreConvertMakerBox resolves identity and queues the user for bin
// allocation. Idempotent on resolved username: re-scanning the same
// member updates the existing pre-conversion row instead of creating
// duplicates. Returns 409 conflict if the user already has a bin.
func (c *Client) PreConvertMakerBox(ctx context.Context, query, notes string) (*MakerBox, error) {
	body := map[string]string{"query": query}
	if notes != "" {
		body["notes"] = notes
	}
	var out MakerBox
	if err := c.Post(ctx, "/api/maker-boxes/pre-convert/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListPreConversionQueue returns the rows waiting for bin allocation,
// newest first. Unlike ListMakerBoxes this hits the dedicated queue
// action so we don't have to filter on status client-side.
func (c *Client) ListPreConversionQueue(ctx context.Context) ([]MakerBox, error) {
	var out []MakerBox
	if err := c.Get(ctx, "/api/maker-boxes/pre-conversion-queue/", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ConvertMakerBox finalizes a pre-conversion row: backend allocates
// MBX-NNN, stamps conversion_completed_at, promotes status from
// pre_conversion to valid/expired based on the cached lookup.
// Returns 404 if the row is missing, 409 if it's already converted.
func (c *Client) ConvertMakerBox(ctx context.Context, id int) (*MakerBox, error) {
	body := map[string]int{"id": id}
	var out MakerBox
	if err := c.Post(ctx, "/api/maker-boxes/convert/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// MakerBoxWrite is the create/edit payload for a maker box, mirroring the
// writable fields of MakerBoxSerializer. The React web app has NO row-CRUD form
// — a bin's lifecycle there is scan → pre-convert → convert — so this mirrors
// the Django admin (the only human CRUD surface) and covers the FULL writable
// serializer set so an operator can create or correct any field
// ([[scantty-makerbox-backend]]). MakerBoxViewSet is a ModelViewSet, so
// create/update/delete all exist; every verb is staff-gated (Logistics), a
// non-staff user 4xxs on save/delete.
//
// Every field is sent on every request (no omitempty) so a PATCH can clear a
// value — matching the CategoryWrite / LocationWrite convention. Field notes:
//   - BinID is a *string: nil → JSON null. The bin_id partial-unique index is
//     `WHERE bin_id IS NOT NULL`, so a blank bin MUST serialize as null, never
//     "" (two blank ""s would violate the constraint); an unallocated /
//     pre-conversion row carries null.
//   - Status is required (CharField with choices, NOT nullable); the model
//     default is "unassigned".
//   - IdentitySource is blank-allowed ("" clears it — model blank=True
//     default="").
//   - The four *string datetimes are ISO-8601 / RFC3339 (DRF DateTimeField
//     rejects date-only); nil → JSON null clears the field.
type MakerBoxWrite struct {
	BinID                 *string `json:"bin_id"`
	AssignedUsername      string  `json:"assigned_username"`
	FirstName             string  `json:"first_name"`
	LastName              string  `json:"last_name"`
	Email                 string  `json:"email"`
	Status                string  `json:"status"`
	IdentitySource        string  `json:"identity_source"`
	AssignedAt            *string `json:"assigned_at"`
	ExpiresAt             *string `json:"expires_at"`
	ConversionCompletedAt *string `json:"conversion_completed_at"`
	PaidAt                *string `json:"paid_at"`
	Notes                 string  `json:"notes"`
}

// CreateMakerBox POSTs a new bin row to the maker-boxes collection.
func (c *Client) CreateMakerBox(ctx context.Context, body MakerBoxWrite) (*MakerBox, error) {
	var out MakerBox
	if err := c.Post(ctx, "/api/maker-boxes/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateMakerBox PATCHes an existing bin row by its integer id.
func (c *Client) UpdateMakerBox(ctx context.Context, id int, body MakerBoxWrite) (*MakerBox, error) {
	var out MakerBox
	if err := c.Patch(ctx, fmt.Sprintf("/api/maker-boxes/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteMakerBox removes a bin row. The backend releases the row's WHERE
// fiducial (AprilTag) before deleting; no client concern. Returns 204 on
// success, 4xx if the caller lacks Logistics/staff access.
func (c *Client) DeleteMakerBox(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/maker-boxes/%d/", id))
}
