package forgekeyapi

import (
	"context"
	"net/url"
	"time"
)

type FirmwareVersion struct {
	ID         int       `json:"id"`
	Version    string    `json:"version"`
	DeviceType string    `json:"device_type,omitempty"`
	IsActive   bool      `json:"is_active"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

func (c *Client) ListFirmwareVersions(ctx context.Context, q url.Values) ([]FirmwareVersion, error) {
	var out MaybeList[FirmwareVersion]
	if err := c.Get(ctx, "/api/forgekey/firmware-versions/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

type FirmwareUpdate struct {
	ID                int       `json:"id"`
	Device            int       `json:"device"`
	DeviceName        string    `json:"device_name,omitempty"`
	FirmwareVersion   int       `json:"firmware_version"`
	FirmwareVersionStr string   `json:"firmware_version_str,omitempty"`
	RequestedBy       string    `json:"requested_by,omitempty"`
	RequestedAt       time.Time `json:"requested_at,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
	Status            string    `json:"status,omitempty"`
}

func (c *Client) ListFirmwareUpdates(ctx context.Context, q url.Values) ([]FirmwareUpdate, error) {
	var out MaybeList[FirmwareUpdate]
	if err := c.Get(ctx, "/api/forgekey/firmware-updates/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
