package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Vendor mirrors backend VendorSerializer — a third-party service
// provider that staff add to maintenance orders. Per
// [[scantty-api-field-drift]]: vendor PK is UUID (string) in
// backend/vendors/models.py — `Vendor.id = UUIDField`.
//
// TDLR (Texas Department of Licensing and Regulation) license and COI
// (Certificate of Insurance) tracking are first-class fields so the
// dashboard can flag an electrician whose license is days from
// expiring before they get re-assigned to a circuit job.
type Vendor struct {
	ID                   string    `json:"id"`
	Name                 string    `json:"name"`
	VendorKind           string    `json:"vendor_kind"`
	VendorKindDisplay    string    `json:"vendor_kind_display,omitempty"`
	ContactName          string    `json:"contact_name,omitempty"`
	Phone                string    `json:"phone,omitempty"`
	Email                string    `json:"email,omitempty"`
	Website              string    `json:"website,omitempty"`
	Address              string    `json:"address,omitempty"`
	TDLRLicenseNumber    string    `json:"tdlr_license_number,omitempty"`
	TDLRLicenseExpiresAt DateOnly  `json:"tdlr_license_expires_at"`
	TDLRIsExpired        bool      `json:"tdlr_is_expired"`
	COIProvider          string    `json:"coi_provider,omitempty"`
	COIPolicyNumber      string    `json:"coi_policy_number,omitempty"`
	COIExpiresAt         DateOnly  `json:"coi_expires_at"`
	COIIsExpired         bool      `json:"coi_is_expired"`
	Notes                string    `json:"notes,omitempty"`
	IsActive             bool      `json:"is_active"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// ListVendors returns the paginated vendor list. Useful filters:
// `?vendor_kind=electrical`, `?is_active=true`, `?search=acme`.
func (c *Client) ListVendors(ctx context.Context, q url.Values) (*Page[Vendor], error) {
	return GetPage[Vendor](ctx, c, "/api/vendors/vendors/", q)
}

func (c *Client) ListAllVendors(ctx context.Context, q url.Values) ([]Vendor, error) {
	var all []Vendor
	if err := IterPages[Vendor](ctx, c, "/api/vendors/vendors/", q, func(batch []Vendor) error {
		all = append(all, batch...)
		return nil
	}); err != nil {
		return nil, err
	}
	return all, nil
}

// GetVendor fetches a single vendor by UUID.
func (c *Client) GetVendor(ctx context.Context, id string) (*Vendor, error) {
	var out Vendor
	if err := c.Get(ctx, fmt.Sprintf("/api/vendors/vendors/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
