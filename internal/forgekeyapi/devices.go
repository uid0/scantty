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

	// Live device sub-state cached from the firmware status message (op-2cr),
	// exposed by the __all__ serializer. Both are empty until a device reports.
	RelayChannels  []RelayChannelState `json:"relay_channels,omitempty"`
	IndicatorState IndicatorState      `json:"indicator_state,omitempty"`
}

// RelayChannelState is the last-reported on/off state of one power-relay channel
// (op-2cr live sub-state, parsed from power_relay.channels). The list is empty
// until the firmware reports it.
type RelayChannelState struct {
	Channel int  `json:"channel"`
	On      bool `json:"on"`
}

// IndicatorState is the last-reported indicator/status-LED sub-state (op-2cr),
// normalized to {color, pattern}. Both are empty until the firmware reports a
// state; color/pattern may arrive as JSON null, which decodes to "".
type IndicatorState struct {
	Color   string `json:"color,omitempty"`
	Pattern string `json:"pattern,omitempty"`
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

// IndicatorTestRequest is the body for POST devices/{id}/indicator/test/ — an
// explicit color/brightness/pattern preview pushed straight to an indicator
// device (bypasses status derivation, for a live hardware check). The backend
// requires at least one of color/brightness/pattern; it validates color as a
// name/hex/[r,g,b], brightness as "low"/"high" or 0-255, pattern against a
// fixed set, and period_ms/duration_s as bounded ints. Every field carries
// omitempty so the client sends exactly what the web card sends (color is
// dropped for an "off" pattern; period_ms only rides along for blink patterns).
type IndicatorTestRequest struct {
	Color      string `json:"color,omitempty"`
	Brightness string `json:"brightness,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	PeriodMS   int    `json:"period_ms,omitempty"`
	DurationS  int    `json:"duration_s,omitempty"`
}

// IndicatorTestResponse mirrors the endpoint's JSON: the sent presentation
// payload plus the created command id.
type IndicatorTestResponse struct {
	Status    string         `json:"status"`
	Device    string         `json:"device"`
	CommandID string         `json:"command_id"`
	Payload   map[string]any `json:"payload"`
}

// IndicatorTest sends an explicit indicator preview to a device. Staff-gated
// (IsAdminUser) server-side — a non-staff caller gets a 403.
func (c *Client) IndicatorTest(ctx context.Context, id string, req IndicatorTestRequest) (*IndicatorTestResponse, error) {
	var out IndicatorTestResponse
	if err := c.Post(ctx, fmt.Sprintf("/api/forgekey/devices/%s/indicator/test/", id), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
