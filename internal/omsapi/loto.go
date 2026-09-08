package omsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// LOTODevice mirrors backend LOTODeviceSerializer — a physical lockout
// device (padlock, scissor block, valve lock). Per
// [[scantty-api-field-drift]]: contrary to an earlier assumption, the loto
// models have NO id override, so with DEFAULT_AUTO_FIELD=BigAutoField the PK
// is an INTEGER — the old `string` id crashed every row on decode. It is
// `any` here to tolerate the id shape. location keeps the Django int autoid;
// assigned_to is a nullable user FK (int).
type LOTODevice struct {
	ID                 any       `json:"id"`
	DeviceType         string    `json:"device_type"`
	DeviceTypeDisplay  string    `json:"device_type_display,omitempty"`
	Label              string    `json:"label"`
	Location           *int      `json:"location"`
	LocationName       string    `json:"location_name,omitempty"`
	AssignedTo         *int      `json:"assigned_to"`
	AssignedToUsername string    `json:"assigned_to_username,omitempty"`
	Status             string    `json:"status"`
	StatusDisplay      string    `json:"status_display,omitempty"`
	Notes              string    `json:"notes,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// IntID coerces the device's `any`-typed PK to an int. Callers that need the
// numeric id (the disconnect required_loto_device_ids multi-picker sends []int)
// go through here rather than type-asserting at each call site, precisely so
// they do not have to know WHICH numeric representation the decoder produced —
// that is jsonDecoder's decision (client.go) and this comment deliberately does
// not restate it, because it used to say "JSON decodes an integer PK into a
// float64" and UseNumber made that false everywhere at once. Returns ok=false
// for a nil / non-numeric id.
func (d LOTODevice) IntID() (int, bool) { return anyToInt(d.ID) }

// anyToInt coerces a JSON-decoded scalar (float64 / int / int64 / json.Number /
// numeric string) to an int.
func anyToInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i, true
		}
	}
	return 0, false
}

// LOTODeviceSummary is the compact representation embedded inside
// AssetEnergySource.required_devices_detail — just enough to flag
// "this padlock is checked out and unavailable" without making the
// caller round-trip for the full device.
type LOTODeviceSummary struct {
	ID                any    `json:"id"`
	DeviceType        string `json:"device_type"`
	DeviceTypeDisplay string `json:"device_type_display,omitempty"`
	Label             string `json:"label"`
	Status            string `json:"status"`
}

// AssetEnergySource is one energy hazard that has to be isolated before
// servicing the asset. The Asset FK is a UUID (string), but the source's own
// PK is an integer BigAutoField, required_devices is an M2M of integer
// LOTODevice PKs (an array of numbers, not strings), and derived_from is a
// nullable integer FK to a PowerBreaker — the earlier string typings all
// crashed the decode.
type AssetEnergySource struct {
	ID                    any                 `json:"id"`
	Asset                 string              `json:"asset"`
	AssetName             string              `json:"asset_name,omitempty"`
	SourceType            string              `json:"source_type"`
	SourceTypeDisplay     string              `json:"source_type_display,omitempty"`
	Magnitude             string              `json:"magnitude,omitempty"`
	IsolationPoint        string              `json:"isolation_point,omitempty"`
	RequiredDevices       []any               `json:"required_devices"`
	RequiredDevicesDetail []LOTODeviceSummary `json:"required_devices_detail"`
	DerivedFrom           *int                `json:"derived_from"`
	IsStale               bool                `json:"is_stale"`
	Notes                 string              `json:"notes,omitempty"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
}

// AssetLOTORequirements is the rolled-up "what do I need to lock out
// to safely service this asset" payload — legacy Asset.lockout_*
// fields stitched together with the structured energy sources so the
// floor tech only fetches once.
type AssetLOTORequirements struct {
	AssetID             string              `json:"asset_id"`
	AssetName           string              `json:"asset_name"`
	LockoutType         string              `json:"lockout_type,omitempty"`
	LockoutTypeDisplay  string              `json:"lockout_type_display,omitempty"`
	LockoutInstructions string              `json:"lockout_instructions,omitempty"`
	LockoutResponsible  string              `json:"lockout_responsible,omitempty"`
	IsRequired          bool                `json:"is_required"`
	EnergySources       []AssetEnergySource `json:"energy_sources"`
}

// ListLOTODevices returns the paginated device list.
func (c *Client) ListLOTODevices(ctx context.Context, q url.Values) (*Page[LOTODevice], error) {
	return GetPage[LOTODevice](ctx, c, "/api/loto/devices/", q)
}

// ListAssetEnergySources returns the paginated energy-source list.
// Filter with `?asset=<uuid>` to scope to a single asset's hazards.
func (c *Client) ListAssetEnergySources(ctx context.Context, q url.Values) (*Page[AssetEnergySource], error) {
	return GetPage[AssetEnergySource](ctx, c, "/api/loto/energy-sources/", q)
}

// GetAssetLOTORequirements returns the consolidated isolation payload
// for one asset: legacy lockout fields + structured energy sources +
// the devices each source requires. Used by AssetDetailScreen to
// surface a Lockout/Tagout section next to Operational Requirements.
func (c *Client) GetAssetLOTORequirements(ctx context.Context, assetID string) (*AssetLOTORequirements, error) {
	var out AssetLOTORequirements
	if err := c.Get(ctx, fmt.Sprintf("/api/loto/assets/%s/loto-requirements/", assetID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
