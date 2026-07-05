package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// TestForgeKeyDeviceDetail_RelayLiveState renders per-channel on/off from the
// cached live sub-state (op-2cr): ch1 on, ch2 off, no "not reported" note.
func TestForgeKeyDeviceDetail_RelayLiveState(t *testing.T) {
	s := NewForgeKeyDeviceDetailScreen(Deps{}, "dev-1")
	s.loading = false
	s.device = &forgekeyapi.Device{
		Name:         "Relay A",
		Capabilities: []string{"power_relay"},
		RelayChannels: []forgekeyapi.RelayChannelState{
			{Channel: 1, On: true},
			{Channel: 2, On: false},
		},
	}

	out := s.View()
	for _, want := range []string{"Power relay", "Channel 1:", "● on", "Channel 2:", "○ off"} {
		if !strings.Contains(out, want) {
			t.Errorf("relay view missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not reported yet") {
		t.Errorf("live state present but 'not reported yet' shown:\n%s", out)
	}
}

// TestForgeKeyDeviceDetail_RelayNoLiveState: a power_relay device with no
// reported channels still shows both channels as — plus the not-reported note.
func TestForgeKeyDeviceDetail_RelayNoLiveState(t *testing.T) {
	s := NewForgeKeyDeviceDetailScreen(Deps{}, "dev-1")
	s.loading = false
	s.device = &forgekeyapi.Device{
		Name:         "Relay A",
		Capabilities: []string{"power_relay"},
	}

	out := s.View()
	for _, want := range []string{"Power relay", "Channel 1:", "Channel 2:", "not reported yet"} {
		if !strings.Contains(out, want) {
			t.Errorf("relay view missing %q:\n%s", want, out)
		}
	}
}

// TestForgeKeyDeviceDetail_IndicatorState: a status_led device renders the
// colour name and appends a non-solid pattern.
func TestForgeKeyDeviceDetail_IndicatorState(t *testing.T) {
	s := NewForgeKeyDeviceDetailScreen(Deps{}, "dev-1")
	s.loading = false
	s.device = &forgekeyapi.Device{
		Name:           "LED A",
		Capabilities:   []string{"status_led"},
		IndicatorState: forgekeyapi.IndicatorState{Color: "green", Pattern: "slow_blink"},
	}

	out := s.View()
	for _, want := range []string{"Status LED", "green", "slow_blink"} {
		if !strings.Contains(out, want) {
			t.Errorf("indicator view missing %q:\n%s", want, out)
		}
	}
}

// TestForgeKeyDeviceDetail_IndicatorSolidPatternHidden: a solid pattern is the
// default and must not be appended after the colour name.
func TestForgeKeyDeviceDetail_IndicatorSolidPatternHidden(t *testing.T) {
	s := NewForgeKeyDeviceDetailScreen(Deps{}, "dev-1")
	s.loading = false
	s.device = &forgekeyapi.Device{
		Name:           "LED A",
		Capabilities:   []string{"status_led"},
		IndicatorState: forgekeyapi.IndicatorState{Color: "red", Pattern: "solid"},
	}

	out := s.View()
	if !strings.Contains(out, "red") {
		t.Errorf("indicator view missing colour:\n%s", out)
	}
	if strings.Contains(out, "· solid") {
		t.Errorf("solid pattern should be hidden:\n%s", out)
	}
}

// TestForgeKeyDeviceDetail_IndicatorEmpty: a status_led device with no reported
// state falls back to a "State: —" placeholder rather than an empty section.
func TestForgeKeyDeviceDetail_IndicatorEmpty(t *testing.T) {
	s := NewForgeKeyDeviceDetailScreen(Deps{}, "dev-1")
	s.loading = false
	s.device = &forgekeyapi.Device{
		Name:         "LED A",
		Capabilities: []string{"status_led"},
	}

	out := s.View()
	if !strings.Contains(out, "Status LED") || !strings.Contains(out, "State:") {
		t.Errorf("expected placeholder indicator state:\n%s", out)
	}
}

// TestForgeKeyDeviceDetail_NoLiveSectionsWithoutCapability: a device announcing
// neither capability shows no relay/LED sub-state sections at all.
func TestForgeKeyDeviceDetail_NoLiveSectionsWithoutCapability(t *testing.T) {
	s := NewForgeKeyDeviceDetailScreen(Deps{}, "dev-1")
	s.loading = false
	s.device = &forgekeyapi.Device{
		Name:         "Plain",
		Capabilities: []string{"people_counter"},
	}

	out := s.View()
	if strings.Contains(out, "Power relay") || strings.Contains(out, "Status LED") {
		t.Errorf("unexpected live sub-state section for non-relay device:\n%s", out)
	}
}

func loadFKDevice(t *testing.T, deps Deps, dev *forgekeyapi.Device, isIndicator bool) *ForgeKeyDeviceDetailScreen {
	t.Helper()
	s := NewForgeKeyDeviceDetailScreen(deps, "dev-1")
	next, _ := s.Update(fkDeviceLoadedMsg{device: dev, isIndicator: isIndicator})
	return next.(*ForgeKeyDeviceDetailScreen)
}

// TestDeviceIsIndicator covers the web-parity detection rule: type-name match,
// device-type-code match, and neither.
func TestDeviceIsIndicator(t *testing.T) {
	types := []forgekeyapi.DeviceType{{ID: float64(3), Name: "Indicator/Status Light", Code: "indicator"}}
	byName := &forgekeyapi.Device{DeviceType: float64(9), DeviceTypeName: "Indicator/Status Light"}
	byCode := &forgekeyapi.Device{DeviceType: float64(3), DeviceTypeName: "Something Else"}
	neither := &forgekeyapi.Device{DeviceType: float64(7), DeviceTypeName: "Relay"}

	if !deviceIsIndicator(byName, nil) {
		t.Errorf("expected name match to be an indicator")
	}
	if !deviceIsIndicator(byCode, types) {
		t.Errorf("expected code match to be an indicator")
	}
	if deviceIsIndicator(neither, types) {
		t.Errorf("expected non-indicator device to be excluded")
	}
}

// TestIndicatorTestGatedToIndicatorDevices verifies 't' opens the form only for
// indicator devices — a non-indicator device keeps the read-only view.
func TestIndicatorTestGatedToIndicatorDevices(t *testing.T) {
	s := loadFKDevice(t, Deps{Ctx: context.Background()},
		&forgekeyapi.Device{ID: "dev-1", DeviceTypeName: "Relay"}, false)
	next, _ := s.Update(woRuneKey("t"))
	s = next.(*ForgeKeyDeviceDetailScreen)
	if s.mode != fkDetailView {
		t.Fatalf("t must not open the indicator-test form on a non-indicator device")
	}
}

// TestIndicatorTestBlinkFlow drives the form for a blink pattern and verifies
// the POST carries color+brightness+pattern+period_ms (the period field only
// appears once the pattern becomes a blink pattern).
func TestIndicatorTestBlinkFlow(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"command_id":"cmd-1","payload":{"pattern":"blink"}}`))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	s := loadFKDevice(t, deps, &forgekeyapi.Device{ID: "dev-1", DeviceTypeName: "Indicator/Status Light"}, true)

	next, _ := s.Update(woRuneKey("t"))
	s = next.(*ForgeKeyDeviceDetailScreen)
	if s.mode != fkDetailIndicatorTest || !s.WantsRawInput() {
		t.Fatalf("expected indicator-test mode with raw input, mode=%v raw=%v", s.mode, s.WantsRawInput())
	}
	// Defaults: green / high / solid → no period field yet.
	if s.itFieldCount() != 3 {
		t.Fatalf("field count = %d, want 3 for a steady pattern", s.itFieldCount())
	}

	// Focus the pattern field (0→1→2) and cycle solid→blink.
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s = next.(*ForgeKeyDeviceDetailScreen)
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s = next.(*ForgeKeyDeviceDetailScreen)
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyRight})
	s = next.(*ForgeKeyDeviceDetailScreen)
	if indicatorPatterns[s.itPatternIdx] != "blink" {
		t.Fatalf("pattern = %q, want blink", indicatorPatterns[s.itPatternIdx])
	}
	if s.itFieldCount() != 4 {
		t.Fatalf("field count = %d, want 4 once a blink pattern is chosen", s.itFieldCount())
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*ForgeKeyDeviceDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a send cmd")
	}
	if _, ok := cmd().(fkIndicatorTestMsg); !ok {
		t.Fatalf("wrong send msg type")
	}
	if gotMethod != http.MethodPost || gotPath != "/api/forgekey/devices/dev-1/indicator/test/" {
		t.Fatalf("%s %s, want POST /api/forgekey/devices/dev-1/indicator/test/", gotMethod, gotPath)
	}
	if gotBody["color"] != "green" || gotBody["brightness"] != "high" || gotBody["pattern"] != "blink" {
		t.Errorf("body = %v", gotBody)
	}
	if gotBody["period_ms"] != float64(1500) {
		t.Errorf("period_ms = %v, want 1500", gotBody["period_ms"])
	}
}

// TestIndicatorTestOffOmitsColor verifies the "off" pattern drops color at the
// TUI shaping layer (mirrors the web card's body construction).
func TestIndicatorTestOffOmitsColor(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"command_id":"cmd-2","payload":{}}`))
	}))
	defer srv.Close()

	deps := Deps{ForgeKey: fkTestClient(t, srv.URL), Ctx: context.Background()}
	s := loadFKDevice(t, deps, &forgekeyapi.Device{ID: "dev-1", DeviceTypeName: "Indicator/Status Light"}, true)
	next, _ := s.Update(woRuneKey("t"))
	s = next.(*ForgeKeyDeviceDetailScreen)

	// Focus pattern (0→1→2) and cycle left solid→off (wraps to the last option).
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s = next.(*ForgeKeyDeviceDetailScreen)
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyDown})
	s = next.(*ForgeKeyDeviceDetailScreen)
	next, _ = s.Update(tea.KeyMsg{Type: tea.KeyLeft})
	s = next.(*ForgeKeyDeviceDetailScreen)
	if indicatorPatterns[s.itPatternIdx] != "off" {
		t.Fatalf("pattern = %q, want off", indicatorPatterns[s.itPatternIdx])
	}

	next, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	s = next.(*ForgeKeyDeviceDetailScreen)
	if cmd == nil {
		t.Fatalf("expected a send cmd")
	}
	cmd()
	if _, present := gotBody["color"]; present {
		t.Errorf("color must be omitted for an off pattern, got %v", gotBody["color"])
	}
	if gotBody["pattern"] != "off" || gotBody["brightness"] != "high" {
		t.Errorf("body = %v", gotBody)
	}
}
