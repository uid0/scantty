package forgekeyapi

import "context"

// fleet.go mirrors the ForgeKey devices/fleet-summary/ aggregate — the single
// round-trip that backs the web ForgeKey Fleet Dashboard (ForgeKeyDashboardPage).
// It rolls up device connectivity, breakdowns by type / capability / firmware,
// an e-paper battery summary, firmware-update activity, a prioritised
// "needs attention" feed, and recent command / firmware-update activity, so the
// dashboard never has to fan out per device.
//
// Decode discipline (confirmed against forgekey/views.py fleet_summary):
//   - every tally is a plain int; there is no money anywhere on this surface.
//   - all timestamps are isoformat() strings (or JSON null → ""); the report
//     tables format them for display, so they stay strings here — like the
//     reorders report client — rather than time.Time.
//   - battery_percent is a nullable model field. The attention feed only lists
//     panels whose value is non-null, but it is modelled *int for safety so a
//     null can never masquerade as 0%.
//   - the endpoint is permission_classes=[IsAuthenticated] (NOT staff): any
//     signed-in caller can read it. The web layers a client-side staff gate on
//     top, but the API contract is plain authentication, so scantty mirrors that.

// FleetSummary is the whole devices/fleet-summary/ envelope.
type FleetSummary struct {
	GeneratedAt    string               `json:"generated_at"`
	Devices        FleetDevices         `json:"devices"`
	EPaper         FleetEPaper          `json:"epaper"`
	Firmware       FleetFirmware        `json:"firmware"`
	Attention      FleetAttention       `json:"attention"`
	RecentCommands []FleetRecentCommand `json:"recent_commands"`
	RecentUpdates  []FleetRecentUpdate  `json:"recent_updates"`
}

// FleetDevices is the device roll-up: scalar connectivity counts plus the
// type / capability / firmware breakdowns.
type FleetDevices struct {
	Total        int                   `json:"total"`
	Active       int                   `json:"active"`
	Online       int                   `json:"online"`
	Offline      int                   `json:"offline"`
	NeverSeen    int                   `json:"never_seen"`
	ByType       []FleetTypeBucket     `json:"by_type"`
	ByCapability []FleetCapBucket      `json:"by_capability"`
	ByFirmware   []FleetFirmwareBucket `json:"by_firmware"`
}

// FleetTypeBucket is one device-type row: how many devices carry that type and
// how many of them are currently online.
type FleetTypeBucket struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Online int    `json:"online"`
}

// FleetCapBucket counts devices advertising a given capability string.
type FleetCapBucket struct {
	Capability string `json:"capability"`
	Count      int    `json:"count"`
}

// FleetFirmwareBucket counts devices reporting a given firmware version
// ("unknown" for devices that never reported one).
type FleetFirmwareBucket struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
}

// FleetEPaper is the active e-paper panel summary.
type FleetEPaper struct {
	Total      int `json:"total"`
	Bound      int `json:"bound"`
	Unbound    int `json:"unbound"`
	LowBattery int `json:"low_battery"`
}

// FleetFirmware is the firmware-update activity summary.
type FleetFirmware struct {
	UpdatesInFlight int `json:"updates_in_flight"`
	RecentFailures  int `json:"recent_failures"`
}

// FleetAttention is the prioritised "needs attention" feed: offline devices,
// low-battery panels, and recently-failed OTA updates.
type FleetAttention struct {
	Offline    []FleetOfflineDevice `json:"offline"`
	LowBattery []FleetLowBattery    `json:"low_battery"`
	OTAFailed  []FleetOTAFailed     `json:"ota_failed"`
}

// FleetOfflineDevice is one active-but-offline device, never-seen first then
// longest-offline. LastSeen is null (→ "") for a device that never connected.
type FleetOfflineDevice struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	MacAddress string `json:"mac_address"`
	LastSeen   string `json:"last_seen"`
}

// FleetLowBattery is one low-battery e-paper panel. AssetName is null (→ "")
// for an unbound panel; BatteryPercent is *int (the feed lists only non-null
// values, but a null must not read as 0%).
type FleetLowBattery struct {
	DisplayID      string `json:"display_id"`
	AssetName      string `json:"asset_name"`
	BatteryPercent *int   `json:"battery_percent"`
	LastBatteryAt  string `json:"last_battery_at"`
}

// FleetOTAFailed is one recently-failed firmware update. Version is null (→ "")
// when the target version row is gone.
type FleetOTAFailed struct {
	DeviceID    string `json:"device_id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Error       string `json:"error"`
	RequestedAt string `json:"requested_at"`
}

// FleetRecentCommand is one of the last device commands. SentBy is null (→ "")
// for a system-issued command; AckStatus is the effective ack state.
type FleetRecentCommand struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Command    string `json:"command"`
	AckStatus  string `json:"ack_status"`
	SentAt     string `json:"sent_at"`
	SentBy     string `json:"sent_by"`
}

// FleetRecentUpdate is one of the last firmware updates. Version / RequestedBy
// are null (→ "") when the version row or requesting user is absent.
type FleetRecentUpdate struct {
	ID          string `json:"id"`
	DeviceID    string `json:"device_id"`
	DeviceName  string `json:"device_name"`
	Version     string `json:"version"`
	Status      string `json:"status"`
	RequestedAt string `json:"requested_at"`
	RequestedBy string `json:"requested_by"`
}

// FleetSummary fetches the devices/fleet-summary/ aggregate. It is a plain GET
// on a DefaultRouter @action, so the path carries the trailing slash — the same
// slash discipline as every other @action client here (a GET is not
// redirect-body-sensitive the way a POST is, but the slash keeps it a single
// round-trip and consistent with the rest of the surface).
func (c *Client) FleetSummary(ctx context.Context) (*FleetSummary, error) {
	var out FleetSummary
	if err := c.Get(ctx, "/api/forgekey/devices/fleet-summary/", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
