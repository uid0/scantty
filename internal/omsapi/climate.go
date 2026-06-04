package omsapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Thermostat mirrors backend ThermostatSerializer — the per-location
// climate-control registry. Per [[scantty-api-field-drift]] Thermostat
// id is int (Django autoid in the climate app), Location FK is int,
// Asset FK (controlled_asset) is UUID string nullable.
//
// Note: this is the *registry* of installed thermostats, not the
// time-series of readings the bead description originally implied.
// Time-series climate data (if/when it exists) lands on a separate
// endpoint that scantty would add alongside.
type Thermostat struct {
	ID                   int       `json:"id"`
	Label                string    `json:"label"`
	Location             *int      `json:"location"`
	LocationName         string    `json:"location_name,omitempty"`
	ControlsLocation     *int      `json:"controls_location"`
	ControlsLocationName *string   `json:"controls_location_name"`
	ControlledAsset      *string   `json:"controlled_asset"`
	ControlledAssetName  *string   `json:"controlled_asset_name"`
	Manufacturer         string    `json:"manufacturer,omitempty"`
	Model                string    `json:"model,omitempty"`
	Notes                string    `json:"notes,omitempty"`
	NeedsReview          bool      `json:"needs_review"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// ListThermostats returns the paginated registry. Useful filters:
// `?location=<id>` to scope to one space, `?needs_review=true` for
// the migration-flagged stubs that still need a maintainer to fill in
// manufacturer / model.
func (c *Client) ListThermostats(ctx context.Context, q url.Values) (*Page[Thermostat], error) {
	return GetPage[Thermostat](ctx, c, "/api/climate/thermostats/", q)
}

// GetThermostat fetches one thermostat by id.
func (c *Client) GetThermostat(ctx context.Context, id int) (*Thermostat, error) {
	var out Thermostat
	if err := c.Get(ctx, fmt.Sprintf("/api/climate/thermostats/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
