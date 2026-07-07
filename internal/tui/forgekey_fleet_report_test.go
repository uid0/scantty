package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// fkFixedBodyClient returns a ForgeKey client whose every GET returns the given
// JSON body, plus the captured request path.
func fkFixedBodyClient(t *testing.T, body string) (*forgekeyapi.Client, *string) {
	t.Helper()
	got := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return fkTestClient(t, srv.URL), got
}

// fleetBody mirrors the real fleet_summary envelope: a never-seen offline device
// (last_seen null), a low-battery panel, a failed OTA, and recent activity rows
// with nullable version / requested_by.
const fleetBody = `{
	"generated_at":"2026-07-07T03:26:11.5+00:00",
	"devices":{
		"total":5,"active":4,"online":3,"offline":1,"never_seen":1,
		"by_type":[{"code":"epaper_screen","name":"E-Paper Screen","count":3,"online":2},
			{"code":"people_counter","name":"People Counter","count":2,"online":1}],
		"by_capability":[{"capability":"status_led","count":4},{"capability":"people_counter","count":2}],
		"by_firmware":[{"version":"1.0.0","count":3},{"version":"unknown","count":2}]
	},
	"epaper":{"total":6,"bound":4,"unbound":2,"low_battery":1},
	"firmware":{"updates_in_flight":2,"recent_failures":1},
	"attention":{
		"offline":[{"kind":"offline","device_id":"dev-1","name":"Front counter","mac_address":"AA:BB:CC:00:11:22","last_seen":null}],
		"low_battery":[{"kind":"low_battery","display_id":"disp-1","asset_name":"Laser cutter","battery_percent":5,"last_battery_at":"2026-07-07T01:00:00+00:00"}],
		"ota_failed":[{"kind":"ota_failed","device_id":"dev-3","name":"Lobby sign","version":"1.2.0","error":"timeout","requested_at":"2026-07-07T02:00:00+00:00"}]
	},
	"recent_commands":[{"id":"cmd-1","device_id":"dev-3","device_name":"Lobby sign","command":"reboot","ack_status":"acked","sent_at":"2026-07-07T02:30:00+00:00","sent_by":"admin"}],
	"recent_updates":[{"id":"upd-2","device_id":"dev-2","device_name":"Back door","version":null,"status":"pending","requested_at":"2026-07-07T02:10:00+00:00","requested_by":null}]
}`

// emptyFleetBody is a healthy/empty fleet: zero counts and empty arrays, so the
// list tabs must render "No rows" rather than crash.
const emptyFleetBody = `{
	"generated_at":"2026-07-07T03:26:11+00:00",
	"devices":{"total":0,"active":0,"online":0,"offline":0,"never_seen":0,
		"by_type":[],"by_capability":[],"by_firmware":[]},
	"epaper":{"total":0,"bound":0,"unbound":0,"low_battery":0},
	"firmware":{"updates_in_flight":0,"recent_failures":0},
	"attention":{"offline":[],"low_battery":[],"ota_failed":[]},
	"recent_commands":[],"recent_updates":[]
}`

// TestForgeKeyFleetReport_Tabs locks the tab set + labels so the seven fleet
// surfaces stay wired in order.
func TestForgeKeyFleetReport_Tabs(t *testing.T) {
	s := NewForgeKeyFleetReportScreen(Deps{})
	if s.Title() != "ForgeKey fleet" {
		t.Errorf("title = %q", s.Title())
	}
	want := []string{"Overview", "By type", "By capability", "By firmware", "Attention", "Recent cmds", "Recent updates"}
	if len(s.tabs) != len(want) {
		t.Fatalf("want %d tabs, got %d", len(want), len(s.tabs))
	}
	for i, w := range want {
		if s.tabs[i].label != w {
			t.Errorf("tab %d = %q, want %q", i, s.tabs[i].label, w)
		}
	}
}

// TestForgeKeyFleetOverview_Loader drives tab 0: the single object reshaped into
// Metric/Value rows, with the generated timestamp trimmed to the minute.
func TestForgeKeyFleetOverview_Loader(t *testing.T) {
	c, path := fkFixedBodyClient(t, fleetBody)
	s := NewForgeKeyFleetReportScreen(Deps{ForgeKey: c})
	rows, err := s.tabs[0].loader(context.Background(), Deps{ForgeKey: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if *path != "/api/forgekey/devices/fleet-summary/" {
		t.Fatalf("path = %q", *path)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[0]] = r[1]
	}
	if got["Generated"] != "2026-07-07 03:26" {
		t.Errorf("generated = %q, want minute-trimmed 2026-07-07 03:26", got["Generated"])
	}
	if got["Devices total"] != "5" || got["Online"] != "3" || got["Offline"] != "1" || got["Never seen"] != "1" {
		t.Errorf("device counters wrong: %+v", got)
	}
	if got["e-Paper low battery"] != "1" || got["Firmware updates in flight"] != "2" {
		t.Errorf("epaper/firmware counters wrong: %+v", got)
	}
}

// TestForgeKeyFleetByType_Loader covers tab 1: the by_type breakdown array.
func TestForgeKeyFleetByType_Loader(t *testing.T) {
	c, _ := fkFixedBodyClient(t, fleetBody)
	s := NewForgeKeyFleetReportScreen(Deps{ForgeKey: c})
	rows, err := s.tabs[1].loader(context.Background(), Deps{ForgeKey: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// Code | Type | Devices | Online
	if rows[0][0] != "epaper_screen" || rows[0][1] != "E-Paper Screen" || rows[0][2] != "3" || rows[0][3] != "2" {
		t.Errorf("by_type row wrong: %q", strings.Join(rows[0], "|"))
	}
}

// TestForgeKeyFleetAttention_Loader covers tab 4: the unified feed, ordered
// offline → low battery → OTA failed. The never-seen device's null last_seen
// renders "—"; the battery reads "5%".
func TestForgeKeyFleetAttention_Loader(t *testing.T) {
	c, _ := fkFixedBodyClient(t, fleetBody)
	s := NewForgeKeyFleetReportScreen(Deps{ForgeKey: c})
	rows, err := s.tabs[4].loader(context.Background(), Deps{ForgeKey: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (1 offline + 1 low battery + 1 OTA)", len(rows))
	}
	// Kind | Device / panel | Detail | When
	if rows[0][0] != "offline" || rows[0][1] != "Front counter" || rows[0][2] != "AA:BB:CC:00:11:22" {
		t.Errorf("offline row wrong: %q", strings.Join(rows[0], "|"))
	}
	if rows[0][3] != "—" {
		t.Errorf("never-seen last_seen = %q, want — (em dash)", rows[0][3])
	}
	if rows[1][0] != "low battery" || rows[1][1] != "Laser cutter" || rows[1][2] != "5%" {
		t.Errorf("low-battery row wrong: %q", strings.Join(rows[1], "|"))
	}
	if rows[2][0] != "OTA failed" || rows[2][1] != "Lobby sign" || rows[2][2] != "1.2.0" {
		t.Errorf("ota-failed row wrong: %q", strings.Join(rows[2], "|"))
	}
}

// TestForgeKeyFleetRecentUpdates_Loader covers tab 6: null version /
// requested_by → "—", timestamp trimmed to the minute.
func TestForgeKeyFleetRecentUpdates_Loader(t *testing.T) {
	c, _ := fkFixedBodyClient(t, fleetBody)
	s := NewForgeKeyFleetReportScreen(Deps{ForgeKey: c})
	rows, err := s.tabs[6].loader(context.Background(), Deps{ForgeKey: c})
	if err != nil {
		t.Fatalf("loader: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	// Device | Version | Status | Requested | By
	r := rows[0]
	if r[0] != "Back door" || r[2] != "pending" || r[3] != "2026-07-07 02:10" {
		t.Errorf("recent update row wrong: %q", strings.Join(r, "|"))
	}
	if r[1] != "—" || r[4] != "—" {
		t.Errorf("null version/requested_by should be —, got version=%q by=%q", r[1], r[4])
	}
}

// TestForgeKeyFleetReport_RenderSmokeAndEmpty drives the screen through
// Init/Update/View with data, then confirms an empty fleet renders "No rows" on
// a list tab rather than crashing.
func TestForgeKeyFleetReport_RenderSmokeAndEmpty(t *testing.T) {
	c, _ := fkFixedBodyClient(t, fleetBody)
	s := NewForgeKeyFleetReportScreen(Deps{ForgeKey: c})
	s.terminalHeight = 40
	s.Update(s.Init()()) // load tab 0 (Overview)
	out := s.View()
	for _, want := range []string{"Overview", "Devices total", "5"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}

	empty, _ := fkFixedBodyClient(t, emptyFleetBody)
	se := NewForgeKeyFleetReportScreen(Deps{ForgeKey: empty})
	se.terminalHeight = 40
	se.active = 4 // Attention — a list tab, empty in this fleet
	se.Update(se.Init()())
	if outE := se.View(); !strings.Contains(outE, "No rows") {
		t.Errorf("empty attention feed should say No rows:\n%s", outE)
	}
}

// TestReportsHub_DOpensForgeKeyFleet locks the hub wiring: 'd' opens the
// ForgeKey fleet report.
func TestReportsHub_DOpensForgeKeyFleet(t *testing.T) {
	s := NewReportsScreen(Deps{})
	if !s.HandlesKey("d") {
		t.Fatal("hub should claim the 'd' hotkey")
	}
	_, cmd := s.Update(mtKey("d"))
	if cmd == nil {
		t.Fatal("'d' should open a report")
	}
	sm := cmd().(SwitchScreenMsg)
	rt, ok := sm.Screen.(*ReportTableScreen)
	if !ok || rt.Title() != "ForgeKey fleet" {
		t.Fatalf("'d' should open ForgeKey fleet, got %T", sm.Screen)
	}
}
