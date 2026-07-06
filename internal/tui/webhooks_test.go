package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// TestWebhookForm_BuildPayload walks the happy path: text fields, the event-type
// select and the is_active toggle map onto a WebhookWrite, and headers parse into
// a JSON object.
func TestWebhookForm_BuildPayload(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)
	s.inputs[whName].SetValue("Slack alerts")
	s.inputs[whDescription].SetValue("post to #ops")
	s.inputs[whURL].SetValue("https://hooks.example.com/abc")
	s.eventTypeIdx = selectIndexOf(webhookEventTypeOptions, "item_low_stock")
	s.isActive = true
	s.inputs[whSecret].SetValue("topsecret")
	s.inputs[whHeaders].SetValue(`{"X-Env":"prod"}`)

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Name != "Slack alerts" {
		t.Errorf("name = %q", w.Name)
	}
	if w.Description != "post to #ops" {
		t.Errorf("description = %q", w.Description)
	}
	if w.URL != "https://hooks.example.com/abc" {
		t.Errorf("url = %q", w.URL)
	}
	if w.EventType != "item_low_stock" {
		t.Errorf("event_type = %q", w.EventType)
	}
	if !w.IsActive {
		t.Errorf("is_active = %v, want true", w.IsActive)
	}
	if w.Secret != "topsecret" {
		t.Errorf("secret = %q", w.Secret)
	}
	if w.Headers["X-Env"] != "prod" {
		t.Errorf("headers = %v", w.Headers)
	}
}

// TestWebhookForm_DefaultsCompleteEventTypes confirms create mode defaults
// (is_active true, first event type) and that the full 12-choice model set is
// offered — including location_problem_reported, which the web form omits.
func TestWebhookForm_DefaultsCompleteEventTypes(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)
	if !s.isActive {
		t.Errorf("create mode should default is_active true")
	}
	if len(webhookEventTypeOptions) != 12 {
		t.Errorf("event type options = %d, want 12 (full model set)", len(webhookEventTypeOptions))
	}
	found := false
	for _, o := range webhookEventTypeOptions {
		if o.value == "location_problem_reported" {
			found = true
		}
	}
	if !found {
		t.Errorf("location_problem_reported must be offered (web omits it; serializer accepts it)")
	}
}

// TestWebhookForm_Validation covers name-required, url-required/format and the
// headers-JSON gate.
func TestWebhookForm_Validation(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)

	// empty name rejected
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty name")
	}
	s.inputs[whName].SetValue("hook")

	// empty url rejected
	if _, err := s.buildPayload(); err == nil {
		t.Errorf("expected error for empty url")
	}
	// bad url rejected
	for _, bad := range []string{"not-a-url", "ftp://x.example.com", "example.com"} {
		s.inputs[whURL].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("expected error for bad url %q", bad)
		}
	}
	s.inputs[whURL].SetValue("https://ok.example.com/hook")

	// good so far
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("valid name+url should pass: %v", err)
	}

	// bad headers JSON rejected
	for _, bad := range []string{"{not json", "[1,2]", `{"a":1}`, `"str"`} {
		s.inputs[whHeaders].SetValue(bad)
		if _, err := s.buildPayload(); err == nil {
			t.Errorf("expected error for bad headers %q", bad)
		}
	}
	// good headers accepted
	s.inputs[whHeaders].SetValue(`{"A":"b"}`)
	if _, err := s.buildPayload(); err != nil {
		t.Errorf("valid headers should pass: %v", err)
	}
}

// TestWebhookForm_EmptySecretAndHeaders confirms a blank secret is dropped from
// the payload and blank headers become an empty (non-nil) object.
func TestWebhookForm_EmptySecretAndHeaders(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)
	s.inputs[whName].SetValue("hook")
	s.inputs[whURL].SetValue("https://ok.example.com")
	// secret + headers left blank

	w, err := s.buildPayload()
	if err != nil {
		t.Fatalf("buildPayload: %v", err)
	}
	if w.Secret != "" {
		t.Errorf("blank secret should be empty (omitted on the wire), got %q", w.Secret)
	}
	if w.Headers == nil {
		t.Errorf("headers should be a non-nil empty map so it serializes as {} not null")
	}
	if len(w.Headers) != 0 {
		t.Errorf("headers = %v, want empty", w.Headers)
	}
}

// TestWebhookForm_Hydrate fills text + select + toggle from a fetched webhook and
// leaves the write-only secret blank.
func TestWebhookForm_Hydrate(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 5)
	s.webhook = &omsapi.Webhook{
		ID:        5,
		Name:      "Live",
		URL:       "https://live.example.com",
		EventType: "delivery_received",
		IsActive:  false,
		Headers:   map[string]string{"X-Env": "prod"},
	}
	s.hydrate()
	if s.inputs[whName].Value() != "Live" {
		t.Errorf("name = %q", s.inputs[whName].Value())
	}
	if s.inputs[whURL].Value() != "https://live.example.com" {
		t.Errorf("url = %q", s.inputs[whURL].Value())
	}
	if webhookEventTypeOptions[s.eventTypeIdx].value != "delivery_received" {
		t.Errorf("event_type idx = %d (%q)", s.eventTypeIdx, webhookEventTypeOptions[s.eventTypeIdx].value)
	}
	if s.isActive {
		t.Errorf("is_active should hydrate false")
	}
	if s.inputs[whSecret].Value() != "" {
		t.Errorf("secret must stay blank on hydrate (write-only), got %q", s.inputs[whSecret].Value())
	}
	if !strings.Contains(s.inputs[whHeaders].Value(), "X-Env") {
		t.Errorf("headers should hydrate to JSON, got %q", s.inputs[whHeaders].Value())
	}
}

// TestWebhookForm_CycleEventTypeAndToggle drives the select + toggle keys.
func TestWebhookForm_CycleEventTypeAndToggle(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)
	s.loading = false

	// Move cursor to the event-type field and cycle forward with space.
	s.cursor = indexOfField(s.fields, whEventType)
	before := s.eventTypeIdx
	s.Update(tea.KeyMsg{Type: tea.KeySpace})
	if s.eventTypeIdx == before {
		t.Errorf("space should cycle the event type")
	}

	// Move to the is_active toggle and flip it with space.
	s.cursor = indexOfField(s.fields, whIsActive)
	on := s.isActive
	s.Update(tea.KeyMsg{Type: tea.KeySpace})
	if s.isActive == on {
		t.Errorf("space should toggle is_active")
	}
}

// TestValidWebhookURL covers the URL pre-check directly.
func TestValidWebhookURL(t *testing.T) {
	for _, ok := range []string{"http://a.example.com", "https://a.example.com/x?y=1"} {
		if err := validWebhookURL(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "example.com", "ftp://a.example.com", "://nohost"} {
		if err := validWebhookURL(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

// TestParseWebhookHeaders covers empty→{}, valid object, and rejects.
func TestParseWebhookHeaders(t *testing.T) {
	m, err := parseWebhookHeaders("   ")
	if err != nil || m == nil || len(m) != 0 {
		t.Errorf("blank should yield empty non-nil map, got %v err=%v", m, err)
	}
	m, err = parseWebhookHeaders(`{"A":"b","C":"d"}`)
	if err != nil || m["A"] != "b" || m["C"] != "d" {
		t.Errorf("valid object parse failed: %v err=%v", m, err)
	}
	for _, bad := range []string{"[1,2]", `{"a":1}`, "not json", "42"} {
		if _, err := parseWebhookHeaders(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// TestWebhookForm_RenderSmoke guards the form render path.
func TestWebhookForm_RenderSmoke(t *testing.T) {
	s := NewWebhookFormScreen(Deps{}, 0)
	s.loading = false
	s.terminalHeight = 30
	out := s.View()
	if !strings.Contains(out, "Webhook URL") || !strings.Contains(out, "Event type") {
		t.Errorf("form view missing fields: %q", out)
	}
}

// TestWebhookList_DeleteConfirm confirms x arms the delete confirmation (raw
// input on) and n cancels it.
func TestWebhookList_DeleteConfirm(t *testing.T) {
	s := NewWebhookListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Webhook{{ID: 1, Name: "A"}}
	if s.WantsRawInput() {
		t.Fatalf("should not want raw input before confirming")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if !s.confirmingDelete || !s.WantsRawInput() {
		t.Errorf("x should arm delete confirmation + raw input")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingDelete || s.WantsRawInput() {
		t.Errorf("n should cancel the confirmation")
	}
}

// TestWebhookList_TestConfirm confirms t arms the test-delivery confirmation and
// n cancels it.
func TestWebhookList_TestConfirm(t *testing.T) {
	s := NewWebhookListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Webhook{{ID: 1, Name: "A"}}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	if !s.confirmingTest || !s.WantsRawInput() {
		t.Errorf("t should arm test confirmation + raw input")
	}
	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if s.confirmingTest {
		t.Errorf("n should cancel the test confirmation")
	}
}

// TestWebhookList_TestResultTerminal confirms an eager (terminal) test result
// shows the result panel and does not queue a poll.
func TestWebhookList_TestResultTerminal(t *testing.T) {
	s := NewWebhookListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Webhook{{ID: 1, Name: "A"}}
	ok := true
	s.Update(webhookTestStartedMsg{result: &omsapi.WebhookTestResult{TaskStatus: "SUCCESS", Success: &ok}})
	if !s.showTestResult {
		t.Errorf("terminal result should show the result panel")
	}
	if s.pollTaskID != "" {
		t.Errorf("terminal result should not queue a poll, got task %q", s.pollTaskID)
	}
	if !s.WantsRawInput() {
		t.Errorf("result panel should capture raw input")
	}
	// Any key dismisses.
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.showTestResult || s.WantsRawInput() {
		t.Errorf("a key should dismiss the result panel")
	}
}

// TestWebhookList_TestResultPending confirms an async (PENDING) result keeps the
// task id for polling.
func TestWebhookList_TestResultPending(t *testing.T) {
	s := NewWebhookListScreen(Deps{})
	s.loading = false
	s.rows = []omsapi.Webhook{{ID: 1, Name: "A"}}
	s.Update(webhookTestStartedMsg{result: &omsapi.WebhookTestResult{TaskStatus: "PENDING", TaskID: "task-9"}})
	if !s.showTestResult {
		t.Errorf("pending result should still show the panel")
	}
	if s.pollTaskID != "task-9" {
		t.Errorf("pending result should keep the task id for polling, got %q", s.pollTaskID)
	}
}

// TestWebhookTestTerminal covers the terminal-state helper.
func TestWebhookTestTerminal(t *testing.T) {
	if !webhookTestTerminal(nil) {
		t.Errorf("nil should be terminal")
	}
	if !webhookTestTerminal(&omsapi.WebhookTestResult{TaskStatus: "SUCCESS"}) {
		t.Errorf("SUCCESS should be terminal")
	}
	if !webhookTestTerminal(&omsapi.WebhookTestResult{TaskStatus: "FAILURE"}) {
		t.Errorf("FAILURE should be terminal")
	}
	if webhookTestTerminal(&omsapi.WebhookTestResult{TaskStatus: "PENDING"}) {
		t.Errorf("PENDING should not be terminal")
	}
}

// TestWebhookList_RenderSmoke guards the list + result render paths.
func TestWebhookList_RenderSmoke(t *testing.T) {
	s := NewWebhookListScreen(Deps{})
	s.loading = false
	rate := 90.0
	s.rows = []omsapi.Webhook{{ID: 1, Name: "Slack", EventTypeDisplay: "Item Low Stock", IsActive: true, SuccessRate: &rate, TotalTriggers: 10}}
	s.terminalHeight = 30
	s.windowSize = 18
	out := s.View()
	if !strings.Contains(out, "Slack") || !strings.Contains(out, "t test") {
		t.Errorf("list view missing content/footer: %q", out)
	}
	// Result panel render.
	s.showTestResult = true
	s.testName = "Slack"
	fail := false
	s.testResult = &omsapi.WebhookTestResult{TaskStatus: "FAILURE", Success: &fail, ErrorMessage: "boom"}
	if out := s.View(); !strings.Contains(out, "Test result") || !strings.Contains(out, "boom") {
		t.Errorf("result panel missing content: %q", out)
	}
}
