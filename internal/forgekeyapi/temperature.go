package forgekeyapi

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// TemperatureReading mirrors backend TemperatureReadingSerializer — one
// sample published over MQTT by a forgekey temperature-sensor device.
// `humidity_percent` is nullable on the model; sensors that don't report
// humidity (DS18B20 etc.) leave it unset.
type TemperatureReading struct {
	ID              string         `json:"id"`
	Device          string         `json:"device"`
	SensorKind      string         `json:"sensor_kind"`
	TemperatureC    float64        `json:"temperature_c"`
	HumidityPercent *float64       `json:"humidity_percent"`
	RecordedAt      time.Time      `json:"recorded_at"`
	RawPayload      map[string]any `json:"raw_payload,omitempty"`
}

// TemperatureResponse is the envelope returned by the
// /api/forgekey/devices/{id}/temperature/?since=<window> action — latest
// snapshot plus the timeseries clipped to the requested window. The
// backend caps `readings` at 1000 samples; with the firmware's ~30s
// cadence that's about 8.3h of history. Pass `since=24h` (the default)
// for a one-day window; the firmware can pull more by paging via
// shorter since values if needed.
type TemperatureResponse struct {
	Device                string               `json:"device"`
	Since                 time.Time            `json:"since"`
	LatestTemperatureC    *float64             `json:"latest_temperature_c"`
	LatestHumidityPercent *float64             `json:"latest_humidity_percent"`
	Readings              []TemperatureReading `json:"readings"`
}

// GetDeviceTemperature fetches the temperature window for a single
// device. `since` accepts the backend's compact form ("24h", "7d") or
// an ISO timestamp; empty falls through to the backend default ("24h").
//
// Returns a 4xx wrapped APIError when the device has no temperature
// capability or never reported.
func (c *Client) GetDeviceTemperature(ctx context.Context, deviceID, since string) (*TemperatureResponse, error) {
	var q url.Values
	if since != "" {
		q = url.Values{"since": []string{since}}
	}
	var out TemperatureResponse
	if err := c.Get(ctx, fmt.Sprintf("/api/forgekey/devices/%s/temperature/", deviceID), q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
