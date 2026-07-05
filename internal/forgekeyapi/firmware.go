package forgekeyapi

import (
	"context"
	"fmt"
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

// FirmwareRolloutProgress mirrors the serializer's `progress` block
// (services.firmware_rollout.rollout_progress): device counts for the
// campaign's target fleet.
type FirmwareRolloutProgress struct {
	Total      int `json:"total"`
	OnTarget   int `json:"on_target"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Failed     int `json:"failed"`
	Remaining  int `json:"remaining"`
}

// FirmwareRollout mirrors FirmwareRolloutSerializer — a staged OTA campaign
// (FirmwareRolloutViewSet). firmware_version is a UUID FK and created_by an
// integer user FK, so both are `any` and the human labels come from the
// separate firmware_version_string / device_type_name / created_by_username
// fields (same decode-safety reasoning as FirmwareVersion above).
type FirmwareRollout struct {
	ID                 any                     `json:"id"`
	FirmwareVersion    any                     `json:"firmware_version"`
	FirmwareVersionStr string                  `json:"firmware_version_string,omitempty"`
	DeviceTypeName     string                  `json:"device_type_name,omitempty"`
	Name               string                  `json:"name,omitempty"`
	Status             string                  `json:"status,omitempty"`
	BatchSizePercent   int                     `json:"batch_size_percent"`
	IntervalMinutes    int                     `json:"interval_minutes"`
	CreatedBy          any                     `json:"created_by,omitempty"`
	CreatedByUsername  string                  `json:"created_by_username,omitempty"`
	CreatedAt          time.Time               `json:"created_at,omitempty"`
	UpdatedAt          time.Time               `json:"updated_at,omitempty"`
	StartedAt          *time.Time              `json:"started_at,omitempty"`
	CompletedAt        *time.Time              `json:"completed_at,omitempty"`
	LastAdvancedAt     *time.Time              `json:"last_advanced_at,omitempty"`
	Progress           FirmwareRolloutProgress `json:"progress"`
	// Dispatched is only present on the start/advance action responses — the
	// number of devices the wave just pushed to.
	Dispatched int `json:"dispatched,omitempty"`
}

func (c *Client) ListFirmwareRollouts(ctx context.Context, q url.Values) ([]FirmwareRollout, error) {
	var out MaybeList[FirmwareRollout]
	if err := c.Get(ctx, "/api/forgekey/firmware-rollouts/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// FirmwareRolloutCreate is the create body. Mirrors the web New-Rollout form:
// firmware_version (UUID, required), batch_size_percent (1-100), interval_minutes
// (>=1), name (optional). status/created_by/timestamps are all server-set
// (serializer read-only), so they are never sent.
type FirmwareRolloutCreate struct {
	FirmwareVersion  string `json:"firmware_version"`
	BatchSizePercent int    `json:"batch_size_percent"`
	IntervalMinutes  int    `json:"interval_minutes"`
	Name             string `json:"name,omitempty"`
}

func (c *Client) CreateFirmwareRollout(ctx context.Context, req FirmwareRolloutCreate) (*FirmwareRollout, error) {
	var out FirmwareRollout
	if err := c.Post(ctx, "/api/forgekey/firmware-rollouts/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// rolloutAction POSTs a lifecycle transition (start/pause/cancel/advance) and
// decodes the refreshed rollout the viewset returns. Trailing slashes match
// the DefaultRouter contract the web client uses.
func (c *Client) rolloutAction(ctx context.Context, id, action string) (*FirmwareRollout, error) {
	var out FirmwareRollout
	path := fmt.Sprintf("/api/forgekey/firmware-rollouts/%s/%s/", id, action)
	if err := c.Post(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StartFirmwareRollout starts a draft rollout, or resumes a paused one — the
// backend `start` action accepts both (web "Start" / "Resume" both call it).
func (c *Client) StartFirmwareRollout(ctx context.Context, id string) (*FirmwareRollout, error) {
	return c.rolloutAction(ctx, id, "start")
}

func (c *Client) PauseFirmwareRollout(ctx context.Context, id string) (*FirmwareRollout, error) {
	return c.rolloutAction(ctx, id, "pause")
}

func (c *Client) CancelFirmwareRollout(ctx context.Context, id string) (*FirmwareRollout, error) {
	return c.rolloutAction(ctx, id, "cancel")
}

// AdvanceFirmwareRollout dispatches the next wave by hand (active rollouts only).
func (c *Client) AdvanceFirmwareRollout(ctx context.Context, id string) (*FirmwareRollout, error) {
	return c.rolloutAction(ctx, id, "advance")
}
