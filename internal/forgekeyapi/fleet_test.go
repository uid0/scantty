package forgekeyapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fleetSummaryBody is a full fleet-summary envelope mirroring the real
// forgekey/views.py fleet_summary shape: scalar device/e-paper/firmware
// counters, the three breakdown arrays, a needs-attention feed exercising a
// never-seen device (last_seen null) and a low-battery panel, and recent
// command / update rows with nullable sent_by / version / requested_by.
const fleetSummaryBody = `{
	"generated_at":"2026-07-07T03:26:11.500000+00:00",
	"devices":{
		"total":5,"active":4,"online":3,"offline":1,"never_seen":1,
		"by_type":[
			{"code":"epaper_screen","name":"E-Paper Screen","count":3,"online":2},
			{"code":"people_counter","name":"People Counter","count":2,"online":1}
		],
		"by_capability":[
			{"capability":"status_led","count":4},
			{"capability":"people_counter","count":2}
		],
		"by_firmware":[
			{"version":"1.0.0","count":3},
			{"version":"unknown","count":2}
		]
	},
	"epaper":{"total":6,"bound":4,"unbound":2,"low_battery":1},
	"firmware":{"updates_in_flight":2,"recent_failures":1},
	"attention":{
		"offline":[
			{"kind":"offline","device_id":"dev-1","name":"Front counter","mac_address":"AA:BB:CC:00:11:22","last_seen":null},
			{"kind":"offline","device_id":"dev-2","name":"Back door","mac_address":"AA:BB:CC:33:44:55","last_seen":"2026-07-06T18:00:00+00:00"}
		],
		"low_battery":[
			{"kind":"low_battery","display_id":"disp-1","asset_name":"Laser cutter","battery_percent":5,"last_battery_at":"2026-07-07T01:00:00+00:00"}
		],
		"ota_failed":[
			{"kind":"ota_failed","device_id":"dev-3","name":"Lobby sign","version":"1.2.0","error":"timeout","requested_at":"2026-07-07T02:00:00+00:00"}
		]
	},
	"recent_commands":[
		{"id":"cmd-1","device_id":"dev-3","device_name":"Lobby sign","command":"reboot","ack_status":"acked","sent_at":"2026-07-07T02:30:00+00:00","sent_by":"admin"},
		{"id":"cmd-2","device_id":"dev-1","device_name":"Front counter","command":"status","ack_status":"pending","sent_at":"2026-07-07T02:45:00+00:00","sent_by":null}
	],
	"recent_updates":[
		{"id":"upd-1","device_id":"dev-3","device_name":"Lobby sign","version":"1.2.0","status":"failed","requested_at":"2026-07-07T02:00:00+00:00","requested_by":"admin"},
		{"id":"upd-2","device_id":"dev-2","device_name":"Back door","version":null,"status":"pending","requested_at":"2026-07-07T02:10:00+00:00","requested_by":null}
	]
}`

// TestFleetSummary_Decodes feeds the full envelope and confirms every section
// decodes: scalar counts, the three breakdown arrays, the attention feed with a
// null last_seen (→ "") and a non-null battery pointer, and the recent-activity
// rows with nullable string fields (→ "").
func TestFleetSummary_Decodes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fleetSummaryBody))
	}))
	defer srv.Close()

	c, err := New(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fs, err := c.FleetSummary(context.Background())
	if err != nil {
		t.Fatalf("FleetSummary: %v", err)
	}
	if gotPath != "/api/forgekey/devices/fleet-summary/" {
		t.Errorf("path = %q, want /api/forgekey/devices/fleet-summary/ (trailing slash)", gotPath)
	}

	d := fs.Devices
	if d.Total != 5 || d.Active != 4 || d.Online != 3 || d.Offline != 1 || d.NeverSeen != 1 {
		t.Errorf("device counters decoded wrong: %+v", d)
	}
	if len(d.ByType) != 2 || d.ByType[0].Code != "epaper_screen" || d.ByType[0].Online != 2 {
		t.Errorf("by_type decoded wrong: %+v", d.ByType)
	}
	if len(d.ByCapability) != 2 || d.ByCapability[0].Capability != "status_led" || d.ByCapability[0].Count != 4 {
		t.Errorf("by_capability decoded wrong: %+v", d.ByCapability)
	}
	if len(d.ByFirmware) != 2 || d.ByFirmware[1].Version != "unknown" || d.ByFirmware[1].Count != 2 {
		t.Errorf("by_firmware decoded wrong: %+v", d.ByFirmware)
	}

	if fs.EPaper.Total != 6 || fs.EPaper.Bound != 4 || fs.EPaper.Unbound != 2 || fs.EPaper.LowBattery != 1 {
		t.Errorf("epaper decoded wrong: %+v", fs.EPaper)
	}
	if fs.Firmware.UpdatesInFlight != 2 || fs.Firmware.RecentFailures != 1 {
		t.Errorf("firmware decoded wrong: %+v", fs.Firmware)
	}

	if len(fs.Attention.Offline) != 2 {
		t.Fatalf("attention.offline = %d, want 2", len(fs.Attention.Offline))
	}
	if fs.Attention.Offline[0].LastSeen != "" {
		t.Errorf("null last_seen should decode to empty, got %q", fs.Attention.Offline[0].LastSeen)
	}
	if fs.Attention.Offline[1].MacAddress != "AA:BB:CC:33:44:55" {
		t.Errorf("offline mac decoded wrong: %+v", fs.Attention.Offline[1])
	}
	if len(fs.Attention.LowBattery) != 1 {
		t.Fatalf("attention.low_battery = %d, want 1", len(fs.Attention.LowBattery))
	}
	lb := fs.Attention.LowBattery[0]
	if lb.BatteryPercent == nil || *lb.BatteryPercent != 5 || lb.AssetName != "Laser cutter" {
		t.Errorf("low-battery decoded wrong: %+v (battery=%v)", lb, lb.BatteryPercent)
	}
	if len(fs.Attention.OTAFailed) != 1 || fs.Attention.OTAFailed[0].Version != "1.2.0" {
		t.Errorf("ota_failed decoded wrong: %+v", fs.Attention.OTAFailed)
	}

	if len(fs.RecentCommands) != 2 {
		t.Fatalf("recent_commands = %d, want 2", len(fs.RecentCommands))
	}
	if fs.RecentCommands[0].SentBy != "admin" || fs.RecentCommands[1].SentBy != "" {
		t.Errorf("recent command sent_by (incl. null → \"\") decoded wrong: %+v", fs.RecentCommands)
	}
	if len(fs.RecentUpdates) != 2 {
		t.Fatalf("recent_updates = %d, want 2", len(fs.RecentUpdates))
	}
	if fs.RecentUpdates[1].Version != "" || fs.RecentUpdates[1].RequestedBy != "" {
		t.Errorf("null version/requested_by should decode to empty, got %+v", fs.RecentUpdates[1])
	}
}
