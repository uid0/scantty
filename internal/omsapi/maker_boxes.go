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
// status values: "valid" | "grace" | "expired" | "unknown" — the same
// taxonomy the scan action returns, so we reuse it.
type MakerBox struct {
	ID               int        `json:"id"`
	BinID            string     `json:"bin_id"`
	AssignedUsername string     `json:"assigned_username"`
	FirstName        string     `json:"first_name,omitempty"`
	LastName         string     `json:"last_name,omitempty"`
	Email            string     `json:"email,omitempty"`
	DisplayName      string     `json:"display_name,omitempty"`
	AssignedAt       *time.Time `json:"assigned_at"`
	ExpiresAt        *time.Time `json:"expires_at"`
	LastVerifiedAt   *time.Time `json:"last_verified_at"`
	Status           string     `json:"status"`
	PaidAt           *time.Time `json:"paid_at"`
	Notes            string     `json:"notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
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
