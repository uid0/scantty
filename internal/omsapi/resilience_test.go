package omsapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newResilienceTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL)
}

// The whole contract in one payload: a healthy single-breaker service, an open
// one carrying a transition + error detail, a half-open one (degraded, on
// trial), and the webhook FAMILY with counts and a null since/last_error.
const resilienceFixture = `{
  "degraded": true,
  "checked_at": "2026-08-04T21:30:00Z",
  "services": [
    {"key": "device_control", "label": "Device control",
     "description": "Turning equipment on and off remotely",
     "state": "open", "healthy": false, "since": "2026-08-04T20:00:00Z",
     "last_error": "broker.internal:1883 connection refused",
     "degraded_count": 1, "total_count": 1},
    {"key": "webhooks", "label": "Webhook delivery",
     "description": "Notifying connected external systems",
     "state": "half_open", "healthy": false, "since": null,
     "last_error": null, "degraded_count": 3, "total_count": 12},
    {"key": "email", "label": "Email delivery",
     "description": "Sending notifications, reorder alerts, and receipts",
     "state": "closed", "healthy": true, "since": null,
     "last_error": null, "degraded_count": 0, "total_count": 1}
  ]
}`

func TestGetResilienceStatus_DecodesTheSnapshot(t *testing.T) {
	var gotPath string
	c := newResilienceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resilienceFixture))
	}))

	got, err := c.GetResilienceStatus(context.Background())
	if err != nil {
		t.Fatalf("GetResilienceStatus: %v", err)
	}
	// The trailing slash is the DRF route; without it the GET redirects.
	if gotPath != "/api/resilience/status/" {
		t.Errorf("path = %q, want /api/resilience/status/", gotPath)
	}
	if !got.Degraded {
		t.Error("Degraded = false, want true")
	}
	if got.CheckedAt.IsZero() {
		t.Error("CheckedAt did not decode")
	}
	if len(got.Services) != 3 {
		t.Fatalf("len(Services) = %d, want 3", len(got.Services))
	}

	dc := got.Service(ServiceKeyDeviceControl)
	if dc == nil {
		t.Fatal("Service(device_control) = nil")
	}
	if dc.Label != "Device control" || dc.State != ServiceStateOpen || dc.Healthy {
		t.Errorf("device_control decoded as %+v", *dc)
	}
	if dc.Since == nil {
		t.Fatal("device_control Since = nil, want the recorded transition")
	}
	if dc.Since.UTC().Hour() != 20 {
		t.Errorf("device_control Since = %v, want 20:00Z", dc.Since.UTC())
	}
	if dc.LastError == "" {
		t.Error("device_control LastError lost")
	}

	// A null since must arrive as nil (never transitioned), not as the zero
	// time — the status screen renders "how long" only when there is a since.
	wh := got.Service(ServiceKeyWebhooks)
	if wh == nil {
		t.Fatal("Service(webhooks) = nil")
	}
	if wh.Since != nil {
		t.Errorf("webhooks Since = %v, want nil for a null", wh.Since)
	}
	if wh.LastError != "" {
		t.Errorf("webhooks LastError = %q, want empty for a null", wh.LastError)
	}
	if wh.DegradedCount != 3 || wh.TotalCount != 12 {
		t.Errorf("webhooks counts = %d/%d, want 3/12", wh.DegradedCount, wh.TotalCount)
	}

	if got.Service("nope") != nil {
		t.Error("Service(unknown key) should be nil")
	}
}

func TestServiceStatus_IsDegradedOnlyOnAKnownBadState(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{ServiceStateClosed, false},
		{ServiceStateOpen, true},
		// Half-open is on trial: calls through it may still fail, so it counts
		// as degraded (matches the backend's DEGRADED_STATES).
		{ServiceStateHalfOpen, true},
		// An unknown state — a future backend value, a truncated payload — must
		// NOT read as an outage. Gating on ignorance is the one direction this
		// feature may never fail in.
		{"", false},
		{"tripping", false},
	}
	for _, tc := range cases {
		got := ServiceStatus{State: tc.state}.IsDegraded()
		if got != tc.want {
			t.Errorf("state %q: IsDegraded() = %v, want %v", tc.state, got, tc.want)
		}
	}
}

// Healthy and State can disagree only if the backend changes; IsDegraded must
// follow State, because that is the field the gate is specified on.
func TestServiceStatus_IsDegradedIgnoresHealthyFlag(t *testing.T) {
	if (ServiceStatus{State: ServiceStateClosed, Healthy: false}).IsDegraded() {
		t.Error("a closed service must not gate, whatever healthy says")
	}
	if !(ServiceStatus{State: ServiceStateOpen, Healthy: true}).IsDegraded() {
		t.Error("an open service must gate, whatever healthy says")
	}
}

func TestDegradedServices_KeepsRegistryOrder(t *testing.T) {
	c := newResilienceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resilienceFixture))
	}))
	got, err := c.GetResilienceStatus(context.Background())
	if err != nil {
		t.Fatalf("GetResilienceStatus: %v", err)
	}
	degraded := got.DegradedServices()
	if len(degraded) != 2 {
		t.Fatalf("len(DegradedServices()) = %d, want 2", len(degraded))
	}
	if degraded[0].Key != ServiceKeyDeviceControl || degraded[1].Key != ServiceKeyWebhooks {
		t.Errorf("DegradedServices() = %q/%q, want device_control/webhooks in registry order",
			degraded[0].Key, degraded[1].Key)
	}

	var nilStatus *ResilienceStatus
	if nilStatus.DegradedServices() != nil || nilStatus.Service("x") != nil {
		t.Error("a nil snapshot must answer empty, not panic")
	}
}

// A transport failure is "we could not ask", and the caller has to be able to
// tell that apart from a snapshot — it must never arrive as a zero-value
// snapshot that reads as "nothing degraded" (or worse, as everything degraded).
func TestGetResilienceStatus_ErrorReturnsNoSnapshot(t *testing.T) {
	c := newResilienceTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"nope"}`, http.StatusInternalServerError)
	}))
	got, err := c.GetResilienceStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error from a 500")
	}
	if got != nil {
		t.Errorf("snapshot = %+v, want nil on error", got)
	}
}
