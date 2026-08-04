// Service status — which external dependencies are working right now.
//
// The backend wraps every outbound dependency (the MQTT broker, WHMCS, the
// member directory, webhook endpoints, outbound email) in a circuit breaker,
// and `resilience/services.py` maps those breakers onto the CAPABILITY a person
// actually loses when one trips. This client reads that map so ScanTTY can say
// "device control is down" out loud instead of letting an operator press a key
// that can only time out.
//
//	GET /api/resilience/status/
//
// Contract verified against OMS backend/resilience/{serializers,services,
// views}.py (PR op-kyfi #999) and the web consumer frontend/src/types/index.ts
// + contexts/ServiceStatusContext.tsx (PR op-lpes #1001), NOT a paraphrase.
//
// Two properties of the endpoint the whole feature leans on:
//
//   - It is readable by any authenticated user (IsAuthenticated, no staff gate)
//     — the point is that ordinary members see the degradation.
//   - It ALWAYS returns 200. A degraded dependency, or a breaker store the
//     backend itself cannot reach, is reported in the body, never as an error
//     status. So a transport error here means "we could not ask", which is
//     UNKNOWN — never "everything is down" (see tui.ServiceHealth).
//
// The payload is deliberately non-sensitive: labels and states only, never
// breaker config, URLs, credentials or hostnames. The one exception is
// LastError, which carries the detail recorded on the transition and can name
// internal infrastructure — it is for a staff status view, and is never
// rendered in a member-facing banner (the web makes the same call).
package omsapi

import (
	"context"
	"time"
)

// The service keys the backend SERVICE_REGISTRY publishes. A call site gates on
// one of these; a key the backend adds later still reaches the status screen and
// the degraded indicator by its own label, it simply gates nothing until a call
// site opts in (the web types make the same promise).
const (
	ServiceKeyDeviceControl = "device_control" // the "mqtt" breaker
	ServiceKeyWebhooks      = "webhooks"       // the "webhook:<id>" family
	ServiceKeyWHMCS         = "whmcs"          // Maker Box billing lookups
	ServiceKeyCommonAPI     = "common_api"     // member directory (badge lookups)
	ServiceKeyEmail         = "email"          // outbound email
)

// Aggregate breaker states. half_open means the dependency is on trial after an
// outage — still degraded, because calls through it may still fail.
const (
	ServiceStateClosed   = "closed"
	ServiceStateHalfOpen = "half_open"
	ServiceStateOpen     = "open"
)

const resilienceStatusPath = "/api/resilience/status/"

// ServiceStatus is one user-facing capability and whether it currently works.
//
// Since is `allow_null` (a service that has never transitioned has no history),
// so it is a pointer: nil is "no recorded transition", which is NOT the same as
// the zero time. LastError is `allow_null` too but decodes into a plain string —
// encoding/json leaves a non-pointer field untouched on null, so "" IS the null
// reading, and the backend never sends an empty string for it.
//
// DegradedCount / TotalCount describe a FAMILY (the webhook breakers): TotalCount
// is 1 for every single-breaker service, so "> 1" is the test for "this is a
// family and counts are worth showing".
type ServiceStatus struct {
	Key           string     `json:"key"`
	Label         string     `json:"label"`
	Description   string     `json:"description"`
	State         string     `json:"state"`
	Healthy       bool       `json:"healthy"`
	Since         *time.Time `json:"since"`
	LastError     string     `json:"last_error"`
	DegradedCount int        `json:"degraded_count"`
	TotalCount    int        `json:"total_count"`
}

// IsDegraded reports whether this service is POSITIVELY reported as open or
// half-open.
//
// It switches on State rather than !Healthy on purpose: a state string this
// client does not know about must not be read as an outage. Gating has to be
// conservative in exactly one direction — a control is taken away only on
// evidence, never on ignorance — and this is where that is enforced. Mirrors the
// web's DEGRADED_STATES check (contexts/ServiceStatusContext.tsx).
func (s ServiceStatus) IsDegraded() bool {
	return s.State == ServiceStateOpen || s.State == ServiceStateHalfOpen
}

// ResilienceStatus is the whole snapshot. It is a plain APIView, not paginated:
// one request is every service the backend knows about.
type ResilienceStatus struct {
	Degraded  bool            `json:"degraded"`
	CheckedAt time.Time       `json:"checked_at"`
	Services  []ServiceStatus `json:"services"`
}

// Service returns the named service's row, or nil when the snapshot does not
// carry it (an unknown key, or a snapshot we never got).
func (r *ResilienceStatus) Service(key string) *ServiceStatus {
	if r == nil {
		return nil
	}
	for i := range r.Services {
		if r.Services[i].Key == key {
			return &r.Services[i]
		}
	}
	return nil
}

// DegradedServices returns every service currently open or half-open, in the
// backend's registry order (which is stable, so a list rendered from it does not
// reshuffle between polls).
func (r *ResilienceStatus) DegradedServices() []ServiceStatus {
	if r == nil {
		return nil
	}
	var out []ServiceStatus
	for _, s := range r.Services {
		if s.IsDegraded() {
			out = append(out, s)
		}
	}
	return out
}

// GetResilienceStatus fetches the current snapshot.
//
// Callers must treat an error as UNKNOWN and carry on: the endpoint reports
// degradation in the body, so an error means the status check itself failed, and
// monitoring that cannot be reached must never become the thing that breaks the
// app.
func (c *Client) GetResilienceStatus(ctx context.Context) (*ResilienceStatus, error) {
	var out ResilienceStatus
	if err := c.Get(ctx, resilienceStatusPath, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
