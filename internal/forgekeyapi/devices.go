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
	DeviceType      string    `json:"device_type"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	Location        string    `json:"location,omitempty"`
	FirmwareVersion string    `json:"firmware_version,omitempty"`
	IsOnline        bool      `json:"is_online"`
	IsActive        bool      `json:"is_active"`
	Capabilities    []string  `json:"capabilities,omitempty"`
	IPAddress       string    `json:"ip_address,omitempty"`
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

type DeviceCommand struct {
	ID         any       `json:"id"`
	Command    string    `json:"command"`
	Status     string    `json:"status"`
	IssuedBy   string    `json:"issued_by,omitempty"`
	IssuedAt   time.Time `json:"issued_at,omitempty"`
	AckedAt    *time.Time `json:"acked_at,omitempty"`
	Response   string    `json:"response,omitempty"`
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

type OccupancyEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Delta     int       `json:"delta"`
	Total     int       `json:"total"`
	Source    string    `json:"source,omitempty"`
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
