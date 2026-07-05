package forgekeyapi

import (
	"context"
	"net/url"
	"time"
)

// FirmwareVersion mirrors forgekey FirmwareVersionSerializer. `device_type`
// and `created_by` are both integer foreign keys — the backend sends them as
// NUMBERS, so the old `string` typing crashed the firmware screen on decode
// ("cannot unmarshal number into ... device_type of type string"). They are
// `any` here (matching Device.DeviceType) so a raw id or a future nested object
// both decode; the human labels come from the serializer's separate
// device_type_name / device_type_code / created_by_username fields.
type FirmwareVersion struct {
	ID                any       `json:"id"`
	Version           string    `json:"version"`
	DeviceType        any       `json:"device_type,omitempty"`
	DeviceTypeName    string    `json:"device_type_name,omitempty"`
	DeviceTypeCode    string    `json:"device_type_code,omitempty"`
	IsActive          bool      `json:"is_active"`
	CreatedBy         any       `json:"created_by,omitempty"`
	CreatedByUsername string    `json:"created_by_username,omitempty"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
}

func (c *Client) ListFirmwareVersions(ctx context.Context, q url.Values) ([]FirmwareVersion, error) {
	var out MaybeList[FirmwareVersion]
	if err := c.Get(ctx, "/api/forgekey/firmware-versions/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

type FirmwareUpdate struct {
	ID                 any    `json:"id"`
	Device             any    `json:"device"`
	DeviceMACAddress   string `json:"device_mac_address,omitempty"`
	FirmwareVersion    any    `json:"firmware_version"`
	FirmwareVersionStr string `json:"firmware_version_string,omitempty"`
	// requested_by is an integer user FK (nullable) — `string` crashed the
	// decode whenever an update had a requester; the username is the separate
	// requested_by_username field.
	RequestedBy         any       `json:"requested_by,omitempty"`
	RequestedByUsername string    `json:"requested_by_username,omitempty"`
	RequestedAt         time.Time `json:"requested_at,omitempty"`
	Status              string    `json:"status,omitempty"`
}

func (c *Client) ListFirmwareUpdates(ctx context.Context, q url.Values) ([]FirmwareUpdate, error) {
	var out MaybeList[FirmwareUpdate]
	if err := c.Get(ctx, "/api/forgekey/firmware-updates/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
