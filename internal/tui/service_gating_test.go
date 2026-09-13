// The inline gates: a control whose service is genuinely down is taken away and
// says why, in place. Everything else keeps working.
//
// The gating choices mirror OMS web PR op-lpes (#1001) surface for surface, and
// the two directions are asserted separately on purpose:
//
//	DEGRADED  → the control is gone AND the reason is on screen
//	UNKNOWN   → the control is exactly as available as before
//
// The second half is the one that matters most. A status endpoint we cannot
// reach must never take a working control away from an operator standing at a
// machine.
package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// ---------------------------------------------------------------------------
// device_control — the ForgeKey device commands
// ---------------------------------------------------------------------------

// sgDeviceScreen builds a loaded device-detail screen whose commands hit a test
// server, so "did the command actually go out?" is a fact rather than an
// inference about a returned closure.
func sgDeviceScreen(t *testing.T, health *ServiceHealth) (*ForgeKeyDeviceDetailScreen, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	fk, err := forgekeyapi.New(forgekeyapi.Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("forgekeyapi.New: %v", err)
	}
	s := NewForgeKeyDeviceDetailScreen(Deps{ForgeKey: fk, Health: health}, "dev-1")
	s.loading = false
	s.isIndicator = true
	s.device = &forgekeyapi.Device{
		ID:           1,
		Name:         "Relay A",
		Capabilities: []string{"power_relay", "status_led"},
	}
	return s, &calls
}

// sgPress sends a key and RUNS whatever command comes back, so a command that
// was actually dispatched reaches the test server.
func sgPress(t *testing.T, s Screen, key string) tea.Msg {
	t.Helper()
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	_, cmd := s.Update(msg)
	if cmd == nil {
		return nil
	}
	return cmd()
}

// Every key on this screen that ends in an MQTT publish is refused while the
// broker's breaker is open, and says so instead of promising an ack that cannot
// come. The database actions (edit, delete, refresh) are untouched.
func TestDeviceDetail_CommandsGatedWhenDeviceControlIsOpen(t *testing.T) {
	s, calls := sgDeviceScreen(t, ssHealth(map[string]string{
		omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateOpen,
	}))

	for _, key := range []string{"e", "d", "s", "i", "p", "b", "R", "1", "2", "!", "@", "t"} {
		msg := sgPress(t, s, key)
		status, ok := msg.(StatusMsg)
		if !ok {
			t.Errorf("%q while device control is open produced %T, want a refusal", key, msg)
			continue
		}
		if !strings.Contains(status.Text, "Device control unavailable") {
			t.Errorf("%q refused with %q — it must name the reason", key, status.Text)
		}
		if status.Level != StatusWarn {
			t.Errorf("%q refused at level %v, want a warning", key, status.Level)
		}
	}
	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("%d commands still went to the broker while it was unreachable", n)
	}
	// 't' must not even open the indicator form: a form whose only button
	// cannot fire is worse than not opening it.
	if s.mode == fkDetailIndicatorTest {
		t.Error("the indicator-test form opened while device control was open")
	}

	out := s.View()
	if !strings.Contains(out, "Device control unavailable (MQTT broker unreachable)") {
		t.Errorf("no inline reason where the keys used to be:\n%s", out)
	}
	for _, gone := range []string{"e enable", "d disable", "b blink", "1/2 relay", "t indicator-test"} {
		if strings.Contains(out, gone) {
			t.Errorf("hint line still advertises %q, which cannot work:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"E edit", "x delete", "r refresh"} {
		if !strings.Contains(out, kept) {
			t.Errorf("hint line dropped %q, which is a database action and still works:\n%s", kept, out)
		}
	}
}

// Half-open is degraded too: the dependency is on trial and calls may still
// fail, so the gate holds (matching the backend's DEGRADED_STATES).
func TestDeviceDetail_HalfOpenAlsoGates(t *testing.T) {
	s, calls := sgDeviceScreen(t, ssHealth(map[string]string{
		omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateHalfOpen,
	}))
	if _, ok := sgPress(t, s, "e").(StatusMsg); !ok {
		t.Error("half-open did not gate the enable command")
	}
	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("%d commands went out while the breaker was half-open", n)
	}
}

// The important direction: an unknown status (no poll yet, a failed fetch, a
// status endpoint that is itself down) must leave every command working.
func TestDeviceDetail_UnknownStatusGatesNothing(t *testing.T) {
	for name, health := range map[string]*ServiceHealth{
		"never polled": NewServiceHealth(),
		"nil holder":   nil,
		"healthy":      ssHealth(nil),
	} {
		s, calls := sgDeviceScreen(t, health)
		msg := sgPress(t, s, "e")
		if status, ok := msg.(StatusMsg); ok && strings.Contains(status.Text, "unavailable") {
			t.Errorf("%s: enable was refused on an unknown status", name)
		}
		if _, ok := msg.(fkCommandResultMsg); !ok {
			t.Errorf("%s: enable produced %T, want the command to have run", name, msg)
		}
		if n := atomic.LoadInt32(calls); n != 1 {
			t.Errorf("%s: %d commands reached the device, want 1", name, n)
		}
		if strings.Contains(s.View(), "Device control unavailable") {
			t.Errorf("%s: a notice is shown with nothing wrong:\n%s", name, s.View())
		}
	}
}

// The breaker can trip while the indicator form is already open — the submit is
// re-checked rather than trusting the gate at the door.
func TestDeviceDetail_IndicatorTestRecheckedAtSubmit(t *testing.T) {
	s, calls := sgDeviceScreen(t, NewServiceHealth())
	if status, ok := sgPress(t, s, "t").(StatusMsg); ok {
		t.Fatalf("opening the indicator form was refused: %q", status.Text)
	}
	if s.mode != fkDetailIndicatorTest {
		t.Fatal("indicator form did not open on a healthy system")
	}

	// The broker goes down while the operator is choosing a colour.
	s.deps.Health.Set(ssSnapshot(map[string]string{
		omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateOpen,
	}))
	s.submitIndicatorTest()
	if n := atomic.LoadInt32(calls); n != 0 {
		t.Errorf("%d indicator tests published after the breaker opened", n)
	}
	if !strings.Contains(s.itErr, "Device control unavailable") {
		t.Errorf("submit refused silently: itErr = %q", s.itErr)
	}
	if !strings.Contains(s.renderIndicatorTest(), "MQTT broker unreachable") {
		t.Errorf("the open form does not say why enter will not work:\n%s", s.renderIndicatorTest())
	}
}

// ---------------------------------------------------------------------------
// whmcs / common_api — the Maker Box lookups
// ---------------------------------------------------------------------------

func sgMakerBoxes(health *ServiceHealth) *MakerBoxesScreen {
	s := NewMakerBoxesScreen(Deps{Health: health})
	s.loading = false
	return s
}

// A scan and a pre-conversion are both membership lookups, so with billing down
// they cannot resolve anybody: the forms do not open, and the keys come off the
// hint line. Convert is deliberately left alone — it allocates from the expiry
// the pre-conversion already stored and calls no lookup.
func TestMakerBoxes_LookupsGatedWhenBillingIsOpen(t *testing.T) {
	s := sgMakerBoxes(ssHealth(map[string]string{omsapi.ServiceKeyWHMCS: omsapi.ServiceStateOpen}))
	// A QUEUED row under the cursor, because that is the only row `c` converts
	// and so the only one its bar names it on.
	s.rows = []omsapi.MakerBox{{ID: 1, AssignedUsername: "ada", Status: "pre_conversion"}}

	for _, key := range []string{"s", "p"} {
		msg := sgPress(t, s, key)
		status, ok := msg.(StatusMsg)
		if !ok {
			t.Errorf("%q while billing is open produced %T, want a refusal", key, msg)
			continue
		}
		if !strings.Contains(status.Text, "Membership lookups are unavailable") {
			t.Errorf("%q refused with %q", key, status.Text)
		}
	}
	if s.scanning || s.preConverting {
		t.Error("a lookup form opened while billing was unreachable")
	}

	out := s.View()
	if !strings.Contains(out, "Membership lookups are unavailable") {
		t.Errorf("no inline reason on the list:\n%s", out)
	}
	if strings.Contains(out, "s scan") || strings.Contains(out, "p pre-convert") {
		t.Errorf("hint line still advertises the lookups:\n%s", out)
	}
	if !strings.Contains(out, "c convert") {
		t.Errorf("convert was gated too — it needs no lookup:\n%s", out)
	}
}

// A degraded member directory only breaks BADGE resolution; a typed username
// still resolves. So it warns and takes nothing away — the web makes the same
// distinction, and a gate here would strand a staffer who could still type.
func TestMakerBoxes_CommonAPIWarnsButDoesNotGate(t *testing.T) {
	s := sgMakerBoxes(ssHealth(map[string]string{omsapi.ServiceKeyCommonAPI: omsapi.ServiceStateOpen}))

	// (Opening a form answers with textinput.Blink, so only a StatusMsg is a
	// refusal here.)
	if status, ok := sgPress(t, s, "p").(StatusMsg); ok {
		t.Errorf("pre-convert was refused for a badge-directory outage: %q", status.Text)
	}
	if !s.preConverting {
		t.Fatal("the pre-conversion form did not open")
	}
	// Folded to the pane, so asked of the words rather than of one line.
	if !strings.Contains(strings.Join(strings.Fields(s.View()), " "), "type the member's username instead") {
		t.Errorf("the form does not say what still works:\n%s", s.View())
	}
}

func TestMakerBoxes_UnknownStatusGatesNothing(t *testing.T) {
	s := sgMakerBoxes(NewServiceHealth())
	if status, ok := sgPress(t, s, "s").(StatusMsg); ok {
		t.Errorf("scan was refused on an unknown status: %q", status.Text)
	}
	if !s.scanning {
		t.Error("the scan form did not open on an unknown status")
	}
	if strings.Contains(s.View(), "unavailable") {
		t.Errorf("a notice is shown with nothing known to be wrong:\n%s", s.View())
	}
}

// The gate is re-checked at submit, because the breaker can trip while the
// operator is typing a bin number.
func TestMakerBoxes_ScanRecheckedAtSubmit(t *testing.T) {
	s := sgMakerBoxes(NewServiceHealth())
	sgPress(t, s, "s")
	if !s.scanning {
		t.Fatal("the scan form did not open")
	}
	s.binInput.SetValue("MBX-001")
	s.userInput.SetValue("ada")
	s.deps.Health.Set(ssSnapshot(map[string]string{omsapi.ServiceKeyWHMCS: omsapi.ServiceStateOpen}))

	_, cmd := s.runScan()
	if cmd != nil {
		t.Error("the scan request was sent after billing went down")
	}
	if !strings.Contains(s.scanErr, "Membership lookups are unavailable") {
		t.Errorf("the refusal is silent: scanErr = %q", s.scanErr)
	}
}

// ---------------------------------------------------------------------------
// webhooks — warned, deliberately not gated
// ---------------------------------------------------------------------------

// The status endpoint aggregates the whole webhook family, so it cannot say
// whether THIS endpoint's breaker is the open one — and re-testing an endpoint
// is exactly what a staffer does during a delivery outage. So the test key
// keeps working and the screen warns instead. This test is the guard on that
// decision: someone "fixing" the inconsistency by disabling it fails here.
func TestWebhooks_DeliveryDegradedWarnsWithoutBlockingTheTest(t *testing.T) {
	s := NewWebhookListScreen(Deps{Health: ssHealth(map[string]string{
		omsapi.ServiceKeyWebhooks: omsapi.ServiceStateOpen,
	})})
	s.loading = false
	s.rows = []omsapi.Webhook{{ID: 1, Name: "Slack", URL: "https://example.test/hook", IsActive: true}}

	out := s.View()
	if !strings.Contains(out, "Webhook delivery is degraded") {
		t.Errorf("the list does not warn about degraded delivery:\n%s", out)
	}
	if !strings.Contains(out, "t test") {
		t.Errorf("the test key was taken off the hint line — it must stay available:\n%s", out)
	}

	if status, ok := sgPress(t, s, "t").(StatusMsg); ok {
		t.Errorf("t was refused: %q", status.Text)
	}
	if !s.confirmingTest {
		t.Fatal("t did not open the confirm — the test must stay available during a delivery outage")
	}
	if !strings.Contains(s.View(), "may fail or be retried") {
		t.Errorf("the confirm does not set expectations:\n%s", s.View())
	}
}

func TestWebhooks_HealthyShowsNoNotice(t *testing.T) {
	s := NewWebhookListScreen(Deps{Health: ssHealth(nil)})
	s.loading = false
	s.rows = []omsapi.Webhook{{ID: 1, Name: "Slack", URL: "https://example.test/hook"}}
	if strings.Contains(s.View(), "degraded") {
		t.Errorf("a healthy system warns anyway:\n%s", s.View())
	}
}
