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

// ThermostatWrite is the writable field set for create/update. It mirrors the
// backend ThermostatSerializer minus the read-only fields (needs_review,
// created_at, updated_at, the *_name derivations) — i.e. exactly the web
// ThermostatWritable in api.ts. The web form POSTs/PATCHes the FULL payload
// every time, and this struct does the same:
//
//   - Label / Manufacturer / Model / Notes ride as plain strings (no omitempty)
//     so a PATCH can clear a previously-set text field back to "".
//   - Location is the required mount FK (Location pk is int); it is always
//     present on the wire (a create without it 400s, matching the serializer).
//   - ControlsLocation is the SET_NULL "conditions room" FK. A *int with NO
//     omitempty so nil serialises as JSON null: the backend Thermostat.save()
//     then auto-defaults it to Location, mirroring the web's clearable Select.
//   - ControlledAsset is the SET_NULL HVAC/RTU asset FK. Asset pk is a UUID, so
//     it is a *string, again with NO omitempty so nil → null clears it.
//
// [[scantty-api-field-drift]] Location/ControlsLocation are ints; ControlledAsset
// is a UUID string.
type ThermostatWrite struct {
	Label            string  `json:"label"`
	Location         int     `json:"location"`
	ControlsLocation *int    `json:"controls_location"`
	ControlledAsset  *string `json:"controlled_asset"`
	Manufacturer     string  `json:"manufacturer"`
	Model            string  `json:"model"`
	Notes            string  `json:"notes"`
}

// CreateThermostat registers a new thermostat (POST to the collection). The
// ThermostatViewSet is a plain ModelViewSet gated only by IsAuthenticated, so
// any signed-in operator may create one (no staff gate).
func (c *Client) CreateThermostat(ctx context.Context, body ThermostatWrite) (*Thermostat, error) {
	var out Thermostat
	if err := c.Post(ctx, "/api/climate/thermostats/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateThermostat edits an existing thermostat. PATCH to the detail URL,
// matching the web's climateAPI.updateThermostat (api.patch).
func (c *Client) UpdateThermostat(ctx context.Context, id int, body ThermostatWrite) (*Thermostat, error) {
	var out Thermostat
	if err := c.Patch(ctx, fmt.Sprintf("/api/climate/thermostats/%d/", id), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteThermostat removes a thermostat (DELETE to the detail URL). Nothing
// PROTECTs a Thermostat row (its reverse FKs are all outbound), so a delete
// never 409s — it simply drops the record and any safety-sign kill-breaker
// chain it fed.
func (c *Client) DeleteThermostat(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/climate/thermostats/%d/", id))
}
