package forgekeyapi

import (
	"context"
	"fmt"
	"time"
)

// EPaperDisplay mirrors backend EPaperDisplaySerializer.
//
// Per [[scantty-api-field-drift]]: display, device, asset, and target
// firmware version IDs are all UUIDs over the wire — keep them as strings,
// not ints. battery_available is a tri-state nullable bool: null=never
// reported, true=has sensor + percent, false=panel reports no sensor
// (e.g. stock SKU 6416 with no battery ADC wired). See OMS PRs #682
// (firmware_version + target) and #684 (battery sensor state) for the
// field additions.
type EPaperDisplay struct {
	ID                          string     `json:"id"`
	Device                      *string    `json:"device"`
	DeviceMACAddress            *string    `json:"device_mac_address"`
	Asset                       *string    `json:"asset"`
	AssetName                   *string    `json:"asset_name"`
	AssetTag                    *string    `json:"asset_tag"`
	BatteryPercent              *int       `json:"battery_percent"`
	IsLowBattery                bool       `json:"is_low_battery"`
	LastBatteryAt               *time.Time `json:"last_battery_at"`
	BatteryAvailable            *bool      `json:"battery_available"`
	BatteryUnavailableReason    string     `json:"battery_unavailable_reason"`
	LastHealthAt                *time.Time `json:"last_health_at"`
	FirmwareVersion             string     `json:"firmware_version"`
	TargetFirmwareVersion       *string    `json:"target_firmware_version"`
	TargetFirmwareVersionString *string    `json:"target_firmware_version_string"`
	LastImageETag               string     `json:"last_image_etag"`
	LastImageAt                 *time.Time `json:"last_image_at"`
	IsActive                    bool       `json:"is_active"`
	CreatedAt                   time.Time  `json:"created_at"`
	UpdatedAt                   time.Time  `json:"updated_at"`
}

// EPaperBindResponse is the slim envelope EPaperDisplayBindView returns —
// not a full EPaperDisplay because the bind endpoint pre-dates the
// management dashboard and was shaped for the mobile bind page.
type EPaperBindResponse struct {
	DisplayID string `json:"display_id"`
	AssetID   string `json:"asset_id"`
	AssetName string `json:"asset_name"`
}

// ListEPaperDisplays returns every registered panel. Backend returns a
// bare array today; MaybeList tolerates the paginated envelope too in
// case that shape ever lands.
func (c *Client) ListEPaperDisplays(ctx context.Context) ([]EPaperDisplay, error) {
	var out MaybeList[EPaperDisplay]
	if err := c.Get(ctx, "/api/forgekey/epaper/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// BindEPaperDisplay attaches the panel identified by display_id to an
// asset. Re-binding is a plain re-call with a different asset_id. Auto-
// creates the EPaperDisplay row when the panel hits the endpoint before
// the firmware's first image.png fetch — matches the web flow.
func (c *Client) BindEPaperDisplay(ctx context.Context, displayID, assetID string) (*EPaperBindResponse, error) {
	body := map[string]string{"asset_id": assetID}
	var out EPaperBindResponse
	if err := c.Post(ctx, fmt.Sprintf("/api/forgekey/epaper/%s/bind/", displayID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetEPaperActive retires (is_active=false) or reactivates a panel. The
// firmware paints the "retired" card and stops refreshing on the next
// image fetch; reactivating brings it back into the regular rotation.
func (c *Client) SetEPaperActive(ctx context.Context, displayID string, isActive bool) (*EPaperDisplay, error) {
	body := map[string]bool{"is_active": isActive}
	var out EPaperDisplay
	if err := c.Post(ctx, fmt.Sprintf("/api/forgekey/epaper/%s/set-active/", displayID), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
