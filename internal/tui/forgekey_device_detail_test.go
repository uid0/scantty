package tui

import (
	"strings"
	"testing"

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
