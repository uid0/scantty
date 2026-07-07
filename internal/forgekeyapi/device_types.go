package forgekeyapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// DeviceType mirrors the ForgeKey DeviceType model — the metadata lookup table
// that names a kind of device (its code drives device-enrollment matching and
// firmware targeting). The backend serializer is `fields = "__all__"` over a
// flat 4-field model, so the whole read shape is id/name/code/description/
// is_active with no FKs, JSON blobs or timestamps.
//
// ID is `any` because the JSON pk decodes as a float64; use IntID for the
// integer value the detail/update/delete URLs need.
type DeviceType struct {
	ID          any    `json:"id"`
	Name        string `json:"name"`
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
	IsActive    bool   `json:"is_active"`
}

// IntID coerces the `any`-typed pk (float64 from JSON, or int/int64/string/
// json.Number depending on the decoder) to the integer the API paths use. It
// returns 0 when the value can't be read as an integer.
func (d DeviceType) IntID() int {
	switch v := d.ID.(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

// DeviceTypeWrite is the create/update payload. It mirrors the writable
// serializer set exactly: name, code, description, is_active.
//
//   - Code carries omitempty so an edit (which leaves it blank) omits it from
//     the PATCH — the code is immutable after creation, matching the web form
//     (ForgeKeyDeviceTypesPage disables the code input on edit and drops it from
//     the update body). On create the form always sets a real choice value, so
//     omitempty never strips a required field.
//   - Description has NO omitempty so a blanked description clears the stored
//     value on PATCH (TextField(blank=True) accepts "").
//   - IsActive has NO omitempty so the boolean is always sent explicitly.
type DeviceTypeWrite struct {
	Name        string `json:"name"`
	Code        string `json:"code,omitempty"`
	Description string `json:"description"`
	IsActive    bool   `json:"is_active"`
}

// ListDeviceTypes returns every device type. The endpoint may answer with a
// bare array or a paginated envelope; MaybeList tolerates both.
func (c *Client) ListDeviceTypes(ctx context.Context) ([]DeviceType, error) {
	var out MaybeList[DeviceType]
	if err := c.Get(ctx, "/api/forgekey/device-types/", nil, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// GetDeviceType fetches a single device type by its integer pk.
func (c *Client) GetDeviceType(ctx context.Context, id int) (*DeviceType, error) {
	var out DeviceType
	if err := c.Get(ctx, fmt.Sprintf("/api/forgekey/device-types/%d/", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateDeviceType POSTs a new device type. Staff-gated server-side
// (IsAdminUser) — a non-staff caller gets a 403.
func (c *Client) CreateDeviceType(ctx context.Context, w DeviceTypeWrite) (*DeviceType, error) {
	var out DeviceType
	if err := c.Post(ctx, "/api/forgekey/device-types/", w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDeviceType PATCHes an existing device type (partial update). Staff-gated
// server-side.
func (c *Client) UpdateDeviceType(ctx context.Context, id int, w DeviceTypeWrite) (*DeviceType, error) {
	var out DeviceType
	if err := c.Patch(ctx, fmt.Sprintf("/api/forgekey/device-types/%d/", id), w, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDeviceType removes a device type. Staff-gated server-side. Note the
// backend protects the row: ESP32Device.device_type is on_delete=PROTECT, so a
// type still referenced by any device returns an error rather than deleting
// (firmware versions/builds cascade). Callers should surface that error.
func (c *Client) DeleteDeviceType(ctx context.Context, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/api/forgekey/device-types/%d/", id))
}
