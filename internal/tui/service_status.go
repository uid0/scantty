// Service status — one shared poll of the backend's circuit breakers, the
// degraded indicator in the status bar, and the inline treatment every gated
// control shares.
//
// ScanTTY parity for OMS PR op-lpes (#1001): show the operator which external
// capability is unavailable instead of letting a keypress fail silently. The
// web mounts a ServiceStatusContext (one poll per tab) that a global banner and
// every gated control read from; ScanTTY has no context, so the same role is
// played by a POINTER hung off Deps — every screen already carries Deps by
// value, so the pointer reaches every call site with no rewiring, and Root is
// the only writer.
//
// Three rules this file exists to enforce, lifted from the web's:
//
//  1. **Poll only when it can pay off.** The endpoint is IsAuthenticated, so a
//     signed-out kiosk never asks for it; the tick still runs, so signing in
//     starts reporting within one interval without a restart.
//
//  2. **A failed status fetch is not an outage.** An unreachable status
//     endpoint tells us nothing, so the snapshot goes back to UNKNOWN — which
//     shows nothing and gates nothing. The monitoring must never become the
//     thing that breaks the app, and a stale "degraded" would keep taking
//     controls away on evidence we no longer have.
//
//  3. **Unknown is healthy.** IsDegraded is false unless the backend
//     POSITIVELY reports open/half_open. That is what makes a nil *ServiceHealth
//     safe: a screen built with a bare Deps{} (every screen-level test does
//     this) gates nothing at all.
package tui

import (
	"context"
	"fmt"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// serviceStatusPollInterval matches the web's SERVICE_STATUS_POLL_MS, and the
// notification poll ScanTTY already runs.
const serviceStatusPollInterval = 60 * time.Second

// ServiceHealth is the shared snapshot of which external dependencies are
// working. Root owns the only instance and is the only writer; screens read it
// through Deps.
//
// Every method is nil-receiver safe and answers "unknown, therefore healthy" —
// so a nil Health is a screen that gates nothing rather than a panic (the web's
// UNKNOWN default context value, in Go's idiom).
//
// The mutex is not decoration: the poll runs in a tea.Cmd goroutine and screens
// read the snapshot from their own async loaders, so reads and writes genuinely
// can land on different goroutines.
type ServiceHealth struct {
	mu       sync.RWMutex
	snapshot *omsapi.ResilienceStatus
}

func NewServiceHealth() *ServiceHealth { return &ServiceHealth{} }

// Set replaces the snapshot. A nil snapshot means UNKNOWN — that is what a
// failed fetch stores, and it must clear any previous degradation rather than
// leaving it to gate controls forever.
func (h *ServiceHealth) Set(snapshot *omsapi.ResilienceStatus) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.snapshot = snapshot
}

// Snapshot returns the last snapshot, or nil when unknown.
func (h *ServiceHealth) Snapshot() *omsapi.ResilienceStatus {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.snapshot
}

// IsDegraded reports whether the named service is POSITIVELY open/half-open.
// This is the one function a gate may call: unknown answers false.
func (h *ServiceHealth) IsDegraded(key string) bool {
	svc := h.Service(key)
	return svc != nil && svc.IsDegraded()
}

// Service returns the named service's row, or nil when unknown.
func (h *ServiceHealth) Service(key string) *omsapi.ServiceStatus {
	return h.Snapshot().Service(key)
}

// Degraded returns every service currently reported degraded, in registry order.
func (h *ServiceHealth) Degraded() []omsapi.ServiceStatus {
	return h.Snapshot().DegradedServices()
}

// ---------------------------------------------------------------------------
// The poll
// ---------------------------------------------------------------------------

// ServiceStatusMsg carries one poll result. A failed fetch is reported with a
// nil Status and is never surfaced to the operator — Root stores UNKNOWN and
// re-arms. Skipped is set when there was nothing to ask with (no client, no
// token yet), which is not a failure either.
//
// Manual marks a fetch the operator asked for (the status screen's refresh).
// Root applies it but does NOT re-arm the tick for it: the recurring poll is a
// single loop, and re-arming on a manual reply would fork a second one on every
// press until the console is polling several times a minute.
type ServiceStatusMsg struct {
	Status  *omsapi.ResilienceStatus
	Err     error
	Skipped bool
	Manual  bool
}

// fetchServiceStatus does the request. Shared by the immediate first fetch, the
// tick and the status screen's refresh, so all three treat a signed-out client
// identically.
func fetchServiceStatus(deps Deps, manual bool) tea.Msg {
	if deps.OMS == nil || deps.OMS.AccessToken() == "" {
		return ServiceStatusMsg{Skipped: true, Manual: manual}
	}
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	status, err := deps.OMS.GetResilienceStatus(ctx)
	if err != nil {
		return ServiceStatusMsg{Err: err, Manual: manual}
	}
	return ServiceStatusMsg{Status: status, Manual: manual}
}

// FetchServiceStatus asks once, right now. Root runs this at startup so a kiosk
// that comes up during an outage says so immediately rather than a minute in.
func FetchServiceStatus(deps Deps) tea.Cmd {
	if deps.OMS == nil {
		return nil
	}
	return func() tea.Msg { return fetchServiceStatus(deps, false) }
}

// RefreshServiceStatus is the operator-initiated fetch: same request, marked so
// it feeds the snapshot without touching the poll loop.
func RefreshServiceStatus(deps Deps) tea.Cmd {
	if deps.OMS == nil {
		return nil
	}
	return func() tea.Msg { return fetchServiceStatus(deps, true) }
}

// PollServiceStatus re-arms the loop: wait, then ask.
//
// Unlike PollNotifications this does NOT bail out when there is no token — the
// tick is armed, the fetch skips, and the loop keeps turning, so an operator who
// signs in on a kiosk that started signed-out is covered within one interval
// instead of never. A skipped or failed fetch re-arms exactly like a successful
// one; the loop is what has to survive, not any single request.
func PollServiceStatus(deps Deps, interval time.Duration) tea.Cmd {
	if deps.OMS == nil {
		return nil
	}
	return tea.Tick(interval, func(time.Time) tea.Msg { return fetchServiceStatus(deps, false) })
}

// ---------------------------------------------------------------------------
// The compact indicator
// ---------------------------------------------------------------------------

// serviceStatusSummary is the chip the status bar carries while something is
// degraded — short, because it rides on the one line that also holds the
// connection state and the key hints, and because the screen's own action bar is
// the only key-discovery surface the redesign leaves standing. Empty means draw
// nothing.
//
// One service names itself, so the operator knows what they have lost without
// opening anything; several collapse to a count, because a list of labels on
// that line would push the hints off it. A FAMILY (total_count > 1) reports
// counts instead of claiming the whole capability is down — 3 bad endpoints out
// of 12 is not "webhook delivery is unavailable" (the web's serviceHeadline
// makes the same distinction).
func serviceStatusSummary(degraded []omsapi.ServiceStatus) string {
	switch len(degraded) {
	case 0:
		return ""
	case 1:
		s := degraded[0]
		if s.TotalCount > 1 {
			return fmt.Sprintf("! %s degraded (%d/%d)", s.Label, s.DegradedCount, s.TotalCount)
		}
		return fmt.Sprintf("! %s unavailable", s.Label)
	default:
		return fmt.Sprintf("! %d services degraded", len(degraded))
	}
}

// serviceStatusSummaryShort is the fallback for a narrow terminal: the same fact
// with the label dropped. The status bar swaps to this rather than letting the
// chip push the line past the terminal width, where lipgloss would WRAP it and
// the whole frame would grow a row.
func serviceStatusSummaryShort(degraded []omsapi.ServiceStatus) string {
	switch len(degraded) {
	case 0:
		return ""
	case 1:
		return "! 1 service degraded"
	default:
		return fmt.Sprintf("! %d services degraded", len(degraded))
	}
}

// ---------------------------------------------------------------------------
// The inline treatment at a gated control
// ---------------------------------------------------------------------------

// The copy a gated control shows. Author-written and static: the API's
// last_error can name internal infrastructure, so it never appears in one of
// these (it is on the status screen, which is where a staffer looks).
//
// deviceControlUnavailable is shared by every ForgeKey surface, exactly as the
// web shares DEVICE_CONTROL_UNAVAILABLE, because they all end in the same MQTT
// publish and should say the same thing.
const (
	deviceControlUnavailable  = "Device control unavailable (MQTT broker unreachable)"
	makerBoxLookupUnavailable = "Membership lookups are unavailable right now — identity can't be resolved."
	badgeLookupUnavailable    = "Badge lookups are unavailable right now — type the member's username instead."
	webhookDeliveryDegraded   = "Webhook delivery is degraded right now — a test may fail or be retried."
)

// serviceUnavailableNotice is the ONE inline treatment for "this control depends
// on something that is currently down" — ScanTTY's ServiceUnavailableNotice.
// Put it next to the control you disabled, so the reason is where the keypress
// would have been rather than only in the status bar.
//
// Renders nothing unless the backend positively reports that service degraded.
// Whether to DISABLE stays with the call site: some controls (a webhook test
// against one endpoint of a partly degraded family) deserve the warning without
// losing the ability to try.
func serviceUnavailableNotice(h *ServiceHealth, key, message string) string {
	if !h.IsDegraded(key) {
		return ""
	}
	if message == "" {
		if svc := h.Service(key); svc != nil {
			message = svc.Label + " is temporarily unavailable — we'll keep retrying."
		} else {
			message = "This service is temporarily unavailable — we'll keep retrying."
		}
	}
	return StyleStatusWarn.Render("⚠ " + message)
}

// serviceUnavailableLine is serviceUnavailableNotice with the blank line a
// screen wants around it — "" when there is nothing to say, so a healthy system
// leaves no gap behind.
func serviceUnavailableLine(h *ServiceHealth, key, message string) string {
	notice := serviceUnavailableNotice(h, key, message)
	if notice == "" {
		return ""
	}
	return notice + "\n\n"
}
