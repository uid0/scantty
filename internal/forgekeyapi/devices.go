package forgekeyapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

type Device struct {
	ID              any       `json:"id"`
	MACAddress      string    `json:"mac_address"`
	DeviceType      any       `json:"device_type"`
	DeviceTypeName  string    `json:"device_type_name,omitempty"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Location        *int      `json:"location,omitempty"`
	FirmwareVersion string    `json:"firmware_version,omitempty"`
	IsOnline        bool      `json:"is_online"`
	IsActive        bool      `json:"is_active"`
	Capabilities    []string  `json:"capabilities,omitempty"`
	IPAddress       string    `json:"ip,omitempty"`
	BootCount       int       `json:"boot_count,omitempty"`
	FreeHeap        int       `json:"free_heap,omitempty"`
	LastSeen        time.Time `json:"last_seen,omitempty"`
}

func (c *Client) ListDevices(ctx context.Context, q url.Values) ([]Device, error) {
	var out MaybeList[Device]
	if err := c.Get(ctx, "/api/forgekey/devices/", q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

func (c *Client) GetDevice(ctx context.Context, id string) (*Device, error) {
	var out Device
	if err := c.Get(ctx, fmt.Sprintf("/api/forgekey/devices/%s/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) EnableDevice(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/enable", id), nil, nil)
}

type DisableRequest struct {
	DelaySeconds int `json:"delay_seconds,omitempty"`
}

func (c *Client) DisableDevice(ctx context.Context, id string, req DisableRequest) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/disable", id), req, nil)
}

// RelayChannelRequest is the POST body for per-channel power-relay control
// (ga-40w): targets one channel of the 2-channel relay.
type RelayChannelRequest struct {
	Channel int  `json:"channel"`
	On      bool `json:"on"`
}

// SetRelayChannel enables/disables a single power-relay channel. The OMS endpoint
// emits a signed power_set command — the verb the firmware's power_relay
// capability handles (channel + action).
func (c *Client) SetRelayChannel(ctx context.Context, id string, req RelayChannelRequest) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/relay-channel", id), req, nil)
}

func (c *Client) RequestStatus(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/status", id), nil, nil)
}

type IdentifyRequest struct {
	DurationS int `json:"duration_s,omitempty"`
}

func (c *Client) IdentifyDevice(ctx context.Context, id string, req IdentifyRequest) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/command/identify", id), req, nil)
}

func (c *Client) RestartDevice(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/command/restart", id), nil, nil)
}

func (c *Client) PingDevice(ctx context.Context, id string) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/command/ping", id), nil, nil)
}

type BlinkRequest struct {
	Pattern   string `json:"pattern,omitempty"`
	DurationS int    `json:"duration_s,omitempty"`
}

func (c *Client) BlinkDevice(ctx context.Context, id string, req BlinkRequest) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/command/blink", id), req, nil)
}

type FirmwareUpdateRequest struct {
	FirmwareVersionID int    `json:"firmware_version_id,omitempty"`
	Version           string `json:"version,omitempty"`
	URL               string `json:"url,omitempty"`
}

func (c *Client) UpdateDeviceFirmware(ctx context.Context, id string, req FirmwareUpdateRequest) error {
	return c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/command/firmware-update", id), req, nil)
}

// DeviceCommand mirrors DeviceCommandSerializer (the device-detail
// recent-commands table). The serializer's field names are sent_by /
// sent_at / ack_status / ack_at / ack_payload — the old issued_*/status/
// response tags matched none of them, so every row rendered blank. sent_by is
// an integer user FK, so it is `any` and the display username comes from the
// separate sent_by_username field.
type DeviceCommand struct {
	ID                 any        `json:"id"`
	Command            string     `json:"command"`
	SentBy             any        `json:"sent_by,omitempty"`
	SentByUsername     string     `json:"sent_by_username,omitempty"`
	SentAt             time.Time  `json:"sent_at,omitempty"`
	AckStatus          string     `json:"ack_status,omitempty"`
	EffectiveAckStatus string     `json:"effective_ack_status,omitempty"`
	AckAt              *time.Time `json:"ack_at,omitempty"`
	AckPayload         any        `json:"ack_payload,omitempty"`
}

func (c *Client) RecentCommands(ctx context.Context, id string, limit int) ([]DeviceCommand, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	var out MaybeList[DeviceCommand]
	if err := c.Get(ctx, fmt.Sprintf("/api/forgekey/devices/%s/recent-commands", id), q, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// OccupancyEvent mirrors OccupancyEventSerializer: the timestamp key is
// event_timestamp_utc, the signed change is occupancy_delta, and the source is
// sensor_kind (there is no `total` field). The old tags never matched.
type OccupancyEvent struct {
	Timestamp time.Time `json:"event_timestamp_utc"`
	Delta     int       `json:"occupancy_delta"`
	Total     int       `json:"total"`
	Source    string    `json:"sensor_kind,omitempty"`
}

type OccupancyResponse struct {
	Events           []OccupancyEvent `json:"events"`
	CurrentOccupancy int              `json:"current_occupancy"`
}

func (c *Client) DeviceOccupancy(ctx context.Context, id string, since string) (*OccupancyResponse, error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	var out OccupancyResponse
	if err := c.Get(ctx, fmt.Sprintf("/api/forgekey/devices/%s/occupancy", id), q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type DeviceType struct {
	ID   any    `json:"id"`
	Name string `json:"name"`
	Code string `json:"code,omitempty"`
}

func (c *Client) ListDeviceTypes(ctx context.Context) ([]DeviceType, error) {
	var out MaybeList[DeviceType]
	if err := c.Get(ctx, "/api/forgekey/device-types/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}
