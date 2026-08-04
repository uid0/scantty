// The service-status feature: the shared snapshot, the poll loop, the compact
// indicator, the status screen, and the inline gates.
//
// The property that matters more than any single assertion here: NOTHING is
// gated on ignorance. An unknown status, a failed fetch, a state string this
// build does not recognise — all of them leave every control exactly as
// available as it was, because a monitoring outage must never become an outage.
// Several of the tests below exist only to hold that line.
package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// ssSnapshot builds a snapshot from key→state pairs, with the labels the
// backend registry actually publishes.
func ssSnapshot(states map[string]string) *omsapi.ResilienceStatus {
	labels := map[string][2]string{
		omsapi.ServiceKeyDeviceControl: {"Device control", "Turning equipment on and off remotely"},
		omsapi.ServiceKeyWebhooks:      {"Webhook delivery", "Notifying connected external systems"},
		omsapi.ServiceKeyWHMCS:         {"Maker Box billing", "Maker Box subscription and billing lookups"},
		omsapi.ServiceKeyCommonAPI:     {"Member directory", "Member lookups from the shared directory"},
		omsapi.ServiceKeyEmail:         {"Email delivery", "Sending notifications, reorder alerts, and receipts"},
	}
	// Registry order, so the rendered list is deterministic.
	order := []string{
		omsapi.ServiceKeyDeviceControl,
		omsapi.ServiceKeyWebhooks,
		omsapi.ServiceKeyWHMCS,
		omsapi.ServiceKeyCommonAPI,
		omsapi.ServiceKeyEmail,
	}
	snap := &omsapi.ResilienceStatus{CheckedAt: time.Date(2026, 8, 4, 21, 30, 0, 0, time.UTC)}
	for _, key := range order {
		state, ok := states[key]
		if !ok {
			state = omsapi.ServiceStateClosed
		}
		meta := labels[key]
		row := omsapi.ServiceStatus{
			Key:         key,
			Label:       meta[0],
			Description: meta[1],
			State:       state,
			Healthy:     state == omsapi.ServiceStateClosed,
			TotalCount:  1,
		}
		if !row.Healthy {
			row.DegradedCount = 1
			snap.Degraded = true
		}
		snap.Services = append(snap.Services, row)
	}
	return snap
}

// ssHealth is a holder already carrying those states.
func ssHealth(states map[string]string) *ServiceHealth {
	h := NewServiceHealth()
	h.Set(ssSnapshot(states))
	return h
}

func ssDegradedDeps(states map[string]string) Deps { return Deps{Health: ssHealth(states)} }

// ---------------------------------------------------------------------------
// The holder
// ---------------------------------------------------------------------------

// A nil holder is what every screen built with a bare Deps{} carries, and what
// the app runs with before the first poll lands. It must answer "healthy" to
// everything rather than panicking or gating.
func TestServiceHealth_NilAndEmptyGateNothing(t *testing.T) {
	var nilHealth *ServiceHealth
	empty := NewServiceHealth()

	for name, h := range map[string]*ServiceHealth{"nil": nilHealth, "empty": empty} {
		if h.Snapshot() != nil {
			t.Errorf("%s: Snapshot() should be nil", name)
		}
		if h.Service(omsapi.ServiceKeyEmail) != nil {
			t.Errorf("%s: Service() should be nil", name)
		}
		if len(h.Degraded()) != 0 {
			t.Errorf("%s: Degraded() should be empty", name)
		}
		for _, key := range []string{
			omsapi.ServiceKeyDeviceControl, omsapi.ServiceKeyWebhooks,
			omsapi.ServiceKeyWHMCS, omsapi.ServiceKeyCommonAPI, omsapi.ServiceKeyEmail,
		} {
			if h.IsDegraded(key) {
				t.Errorf("%s: IsDegraded(%s) = true on an unknown status — that gates on ignorance", name, key)
			}
		}
		if got := serviceUnavailableNotice(h, omsapi.ServiceKeyEmail, "boom"); got != "" {
			t.Errorf("%s: notice on unknown status = %q, want nothing", name, got)
		}
	}
	// A nil holder must also survive a write (Root does this before any screen
	// exists in some orderings).
	nilHealth.Set(ssSnapshot(nil))
}

func TestServiceHealth_GatesOnlyOnOpenOrHalfOpen(t *testing.T) {
	cases := map[string]bool{
		omsapi.ServiceStateClosed:   false,
		omsapi.ServiceStateOpen:     true,
		omsapi.ServiceStateHalfOpen: true,
		"who_knows":                 false,
	}
	for state, want := range cases {
		h := ssHealth(map[string]string{omsapi.ServiceKeyDeviceControl: state})
		if got := h.IsDegraded(omsapi.ServiceKeyDeviceControl); got != want {
			t.Errorf("state %q: IsDegraded = %v, want %v", state, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// The poll loop, driven through Root
// ---------------------------------------------------------------------------

func ssRootWithClient(t *testing.T) Root {
	t.Helper()
	r := newTestRoot(NewWelcomeScreen())
	// A client is needed only so the poll cmds are non-nil; nothing executes
	// them in these tests.
	r.deps.OMS = omsapi.New("http://status.invalid")
	r.deps.Health = NewServiceHealth()
	return r
}

func ssApply(t *testing.T, r Root, msg ServiceStatusMsg) (Root, tea.Cmd) {
	t.Helper()
	next, cmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after, cmd
}

// A failed status fetch is not an outage: it clears the snapshot back to
// unknown (so nothing keeps being gated on evidence we no longer have), shows
// no error anywhere, and re-arms the loop.
func TestRootServiceStatus_FailedFetchGoesUnknownAndNeverErrors(t *testing.T) {
	r := ssRootWithClient(t)

	r, _ = ssApply(t, r, ServiceStatusMsg{Status: ssSnapshot(map[string]string{
		omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateOpen,
	})})
	if !r.deps.Health.IsDegraded(omsapi.ServiceKeyDeviceControl) {
		t.Fatal("a degraded snapshot did not reach the holder")
	}

	r, cmd := ssApply(t, r, ServiceStatusMsg{Err: ssFetchError{}})
	if r.deps.Health.IsDegraded(omsapi.ServiceKeyDeviceControl) {
		t.Error("a failed fetch left the old degradation gating controls")
	}
	if r.deps.Health.Snapshot() != nil {
		t.Error("a failed fetch must store UNKNOWN, not a snapshot")
	}
	if cmd == nil {
		t.Error("the poll loop must re-arm after a failure — one bad request must not stop reporting")
	}
	// And nothing user-visible is left flying: the chip goes with the evidence.
	r.status.SetWidth(120)
	if strings.Contains(r.status.View(), "!") {
		t.Errorf("status bar still flags a problem after the snapshot went unknown:\n%s", r.status.View())
	}
}

// A skipped fetch (signed out, no client) is not evidence either way, so it must
// not wipe a snapshot — and the loop still has to keep turning, or an operator
// who signs in on a kiosk that started signed-out never gets a status again.
func TestRootServiceStatus_SkippedKeepsSnapshotAndKeepsPolling(t *testing.T) {
	r := ssRootWithClient(t)
	r, _ = ssApply(t, r, ServiceStatusMsg{Status: ssSnapshot(map[string]string{
		omsapi.ServiceKeyEmail: omsapi.ServiceStateOpen,
	})})

	r, cmd := ssApply(t, r, ServiceStatusMsg{Skipped: true})
	if !r.deps.Health.IsDegraded(omsapi.ServiceKeyEmail) {
		t.Error("a skipped fetch cleared the snapshot — it is not evidence of recovery")
	}
	if cmd == nil {
		t.Error("a skipped fetch must still re-arm the loop")
	}
}

// The operator's own refresh feeds the snapshot but must NOT re-arm: every
// press would otherwise fork another 60s loop and quietly multiply the poll
// rate for the rest of the session.
func TestRootServiceStatus_ManualRefreshDoesNotForkThePollLoop(t *testing.T) {
	r := ssRootWithClient(t)
	r, cmd := ssApply(t, r, ServiceStatusMsg{
		Status: ssSnapshot(map[string]string{omsapi.ServiceKeyWHMCS: omsapi.ServiceStateOpen}),
		Manual: true,
	})
	if !r.deps.Health.IsDegraded(omsapi.ServiceKeyWHMCS) {
		t.Error("a manual refresh must still update the snapshot")
	}
	if cmd != nil {
		t.Error("a manual refresh re-armed the poll — that forks a second loop per press")
	}
}

// ssFetchError is a stand-in transport error; the poll path only ever checks
// for non-nil, so standing up a dead socket to get a real one would be theatre.
type ssFetchError struct{}

func (ssFetchError) Error() string { return "dial tcp: i/o timeout" }

// ---------------------------------------------------------------------------
// The compact indicator
// ---------------------------------------------------------------------------

func TestServiceStatusSummary_SaysWhatIsLost(t *testing.T) {
	email := ssSnapshot(map[string]string{omsapi.ServiceKeyEmail: omsapi.ServiceStateOpen}).DegradedServices()
	if got, want := serviceStatusSummary(email), "! Email delivery unavailable"; got != want {
		t.Errorf("one service: summary = %q, want %q", got, want)
	}

	// A FAMILY reports counts instead of claiming the whole capability is down:
	// 3 bad endpoints out of 12 is not "webhook delivery is unavailable".
	family := []omsapi.ServiceStatus{{
		Key: omsapi.ServiceKeyWebhooks, Label: "Webhook delivery",
		State: omsapi.ServiceStateOpen, DegradedCount: 3, TotalCount: 12,
	}}
	if got, want := serviceStatusSummary(family), "! Webhook delivery degraded (3/12)"; got != want {
		t.Errorf("family: summary = %q, want %q", got, want)
	}

	two := ssSnapshot(map[string]string{
		omsapi.ServiceKeyEmail:         omsapi.ServiceStateOpen,
		omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateHalfOpen,
	}).DegradedServices()
	if got, want := serviceStatusSummary(two), "! 2 services degraded"; got != want {
		t.Errorf("two services: summary = %q, want %q", got, want)
	}

	if got := serviceStatusSummary(nil); got != "" {
		t.Errorf("healthy: summary = %q, want nothing at all", got)
	}
}

// The chip rides the one line that also carries the connection state and the
// key hints. If it pushes that line past the terminal width lipgloss WRAPS it,
// the bar becomes two rows, and the whole frame shifts — so a narrow terminal
// gets the short form instead.
func TestStatusBar_DegradedChipNeverWrapsTheBar(t *testing.T) {
	degraded := ssSnapshot(map[string]string{omsapi.ServiceKeyEmail: omsapi.ServiceStateOpen}).DegradedServices()

	wide := NewStatusBar()
	wide.SetWidth(120)
	wide.SetDegradedServices(degraded)
	out := wide.View()
	if !strings.Contains(out, "Email delivery unavailable") {
		t.Errorf("wide bar dropped the named chip:\n%s", out)
	}

	narrow := NewStatusBar()
	narrow.SetWidth(56)
	narrow.SetDegradedServices(degraded)
	out = narrow.View()
	if strings.Contains(out, "Email delivery unavailable") {
		t.Errorf("narrow bar kept the long chip — it will wrap:\n%s", out)
	}
	if !strings.Contains(out, "1 service degraded") {
		t.Errorf("narrow bar dropped the fact along with the label:\n%s", out)
	}
	for _, line := range strings.Split(narrow.View(), "\n") {
		if lenVis(line) > 56 {
			t.Errorf("bar line is %d cols wide, terminal is 56 — it will wrap:\n%q", lenVis(line), line)
		}
	}

	healthy := NewStatusBar()
	healthy.SetWidth(120)
	healthy.SetDegradedServices(nil)
	if strings.Contains(healthy.View(), "!") {
		t.Errorf("healthy bar is flying a warning:\n%s", healthy.View())
	}
}

// The short chip is NOT always shorter: with two or more services the two forms
// are the same string, so the label-dropping fallback saves nothing and the bar
// has to give up the static hints instead. A hand-run at 56 columns is what
// caught this — the single-service case above passes either way.
func TestStatusBar_StaysOneLineWhenTheChipCannotShrink(t *testing.T) {
	two := ssSnapshot(map[string]string{
		omsapi.ServiceKeyEmail:         omsapi.ServiceStateOpen,
		omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateOpen,
	}).DegradedServices()

	for _, width := range []int{80, 64, 56, 40, 24} {
		bar := NewStatusBar()
		bar.SetWidth(width)
		bar.SetUnread(3)
		bar.SetDegradedServices(two)
		out := bar.View()
		for _, line := range strings.Split(out, "\n") {
			if lenVis(line) > width {
				t.Errorf("at %d cols a bar line is %d wide — the frame grows a row:\n%q",
					width, lenVis(line), line)
			}
		}
		// Whatever else goes, the operator must still be told.
		if width >= 40 && !strings.Contains(out, "2 services degraded") {
			t.Errorf("at %d cols the degradation was dropped entirely:\n%s", width, out)
		}
	}
}

// ---------------------------------------------------------------------------
// The status screen
// ---------------------------------------------------------------------------

const ssScreenHeight = 34

func ssScreen(t *testing.T, deps Deps) *ServiceStatusScreen {
	t.Helper()
	s := NewServiceStatusScreen(deps)
	s.now = func() time.Time { return time.Date(2026, 8, 4, 22, 0, 0, 0, time.UTC) }
	s.Update(tea.WindowSizeMsg{Width: 110, Height: ssScreenHeight})
	return s
}

func TestServiceStatusScreen_ListsEveryServiceWithStateAndDuration(t *testing.T) {
	snap := ssSnapshot(map[string]string{omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateOpen})
	since := time.Date(2026, 8, 4, 20, 30, 0, 0, time.UTC)
	snap.Services[0].Since = &since
	h := NewServiceHealth()
	h.Set(snap)

	s := ssScreen(t, Deps{Health: h})
	out := s.View()

	for _, want := range []string{
		"Device control", "Webhook delivery", "Maker Box billing", "Member directory", "Email delivery",
		"Unavailable", "Working",
		"1h 30m", // how long it has been degraded — the bead's third column
		"Turning equipment on and off remotely",
		"5 services", "1 degraded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status screen missing %q:\n%s", want, out)
		}
	}
	// Healthy services carry no duration row — there is nothing to time.
	if strings.Count(out, "Degraded for") != 1 {
		t.Errorf("expected exactly one 'Degraded for' row (only the open service):\n%s", out)
	}
}

// Columnar contract, same as the sweep tests: every field row hangs off ONE
// leader column, and the bar is pinned to a fixed two rows of the pane.
func TestServiceStatusScreen_IsColumnarWithAPersistentBar(t *testing.T) {
	s := ssScreen(t, ssDegradedDeps(map[string]string{omsapi.ServiceKeyEmail: omsapi.ServiceStateOpen}))
	out := s.View()

	col := -1
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, jdeLeader)
		if idx < 0 {
			continue
		}
		rows++
		if col == -1 {
			col = idx
			continue
		}
		if idx != col {
			t.Errorf("leader column moved: %d vs %d in %q", idx, col, line)
		}
	}
	if rows < 5 {
		t.Errorf("expected several columnar rows, found %d:\n%s", rows, out)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != ssScreenHeight-statusBarRows-contentVerticalPadding-screenHeaderRows {
		t.Errorf("frame is %d rows, want the pane budget %d — the bar only stays put if the body is padded",
			len(lines), ssScreenHeight-statusBarRows-contentVerticalPadding-screenHeaderRows)
	}
	if !strings.Contains(lines[len(lines)-1], "Enter") {
		t.Errorf("last row is not the action bar:\n%s", out)
	}
}

// The bar is the only place a key can be learned, so it must name exactly the
// keys that work here — and no key that works may be missing from it.
func TestServiceStatusScreen_BarNamesOnlyTheKeysThatApply(t *testing.T) {
	s := ssScreen(t, ssDegradedDeps(nil))
	bar := ssBarLine(t, s.View())
	for _, want := range []string{"Enter=Refresh", "Esc=Back", "UP/DN=Move"} {
		if !strings.Contains(bar, want) {
			t.Errorf("bar missing %q:\n%s", want, bar)
		}
	}
	// Nothing is off screen at this height, so paging must not be advertised.
	if strings.Contains(bar, "PgUp") {
		t.Errorf("bar offers paging with nothing to page to:\n%s", bar)
	}

	// A single service has nowhere to move to; the bar must drop UP/DN rather
	// than name a key that does nothing.
	one := NewServiceHealth()
	one.Set(&omsapi.ResilienceStatus{Services: []omsapi.ServiceStatus{{
		Key: omsapi.ServiceKeyEmail, Label: "Email delivery", State: omsapi.ServiceStateClosed,
	}}})
	solo := ssScreen(t, Deps{Health: one})
	if strings.Contains(ssBarLine(t, solo.View()), "UP/DN") {
		t.Errorf("bar offers Move with one service:\n%s", solo.View())
	}
}

// No letter accelerators: the redesign retired them, so a stray letter on this
// screen must do NOTHING rather than fire something the bar never mentioned.
func TestServiceStatusScreen_NoLetterAccelerators(t *testing.T) {
	s := ssScreen(t, ssDegradedDeps(nil))
	before := s.View()
	for _, key := range []string{"r", "j", "k", "g", "G", "x", "v", "n"} {
		_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil {
			t.Errorf("%q fired a command on a screen whose bar never mentions it", key)
		}
	}
	if s.View() != before {
		t.Error("a letter key moved the screen")
	}
	// Enter is on the bar, so it must actually do something.
	if _, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Error("Enter produced a cmd with no client — expected nil here")
	}
	withClient := ssScreen(t, Deps{Health: NewServiceHealth(), OMS: omsapi.New("http://status.invalid")})
	if _, cmd := withClient.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("Enter=Refresh is on the bar but does nothing")
	}
}

func TestServiceStatusScreen_ArrowsMoveAndClamp(t *testing.T) {
	s := ssScreen(t, ssDegradedDeps(nil))
	if s.cursor != 0 {
		t.Fatalf("cursor starts at %d", s.cursor)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyUp})
	if s.cursor != 0 {
		t.Errorf("up at the top wrapped to %d — paging and moving both clamp", s.cursor)
	}
	for i := 0; i < 10; i++ {
		s.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if s.cursor != len(s.services())-1 {
		t.Errorf("cursor = %d after running off the end, want %d", s.cursor, len(s.services())-1)
	}
	s.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if s.cursor < 0 {
		t.Errorf("pgup left the cursor at %d", s.cursor)
	}
}

// Unknown must read as unknown — not as an outage, and not as a false all-clear
// that hides the fact we have not heard anything.
func TestServiceStatusScreen_UnknownIsNotAnOutage(t *testing.T) {
	s := ssScreen(t, Deps{Health: NewServiceHealth()})
	out := s.View()
	if !strings.Contains(out, "Service status unavailable") {
		t.Errorf("unknown status does not say so:\n%s", out)
	}
	if !strings.Contains(out, "Nothing is being gated") {
		t.Errorf("unknown status does not say controls are unaffected:\n%s", out)
	}
	if strings.Contains(out, "degraded") && !strings.Contains(out, "Nothing is being gated") {
		t.Errorf("unknown status reads as degradation:\n%s", out)
	}
	if !strings.Contains(ssBarLine(t, out), "Enter=Refresh") {
		t.Errorf("no way to retry from the unknown state:\n%s", out)
	}
}

// last_error can name internal infrastructure (a broker host, a provider's
// reply). The web keeps it out of member-facing surfaces; ScanTTY's screen is
// open to any operator, so the line is staff-only.
func TestServiceStatusScreen_LastErrorIsStaffOnly(t *testing.T) {
	snap := ssSnapshot(map[string]string{omsapi.ServiceKeyDeviceControl: omsapi.ServiceStateOpen})
	snap.Services[0].LastError = "broker.internal:1883 connection refused"
	h := NewServiceHealth()
	h.Set(snap)

	member := ssScreen(t, Deps{Health: h})
	if strings.Contains(member.View(), "broker.internal") {
		t.Errorf("a non-staff operator can read the transition detail:\n%s", member.View())
	}

	staff := ssScreen(t, Deps{Health: h, InitialStaff: true})
	if !strings.Contains(staff.View(), "broker.internal") {
		t.Errorf("staff cannot see the detail that explains the outage:\n%s", staff.View())
	}
}

func TestServiceDegradedFor_ReadsAsADuration(t *testing.T) {
	now := time.Date(2026, 8, 4, 22, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }

	cases := []struct {
		since *time.Time
		want  string
	}{
		{at(30 * time.Second), "30s"},
		{at(45 * time.Minute), "45m"},
		{at(90 * time.Minute), "1h 30m"},
		{at(50 * time.Hour), "2d 2h"},
		// A service degraded now but with no recorded transition is not "0s" —
		// that would claim a precision the payload does not carry.
		{nil, "unknown (no recorded transition)"},
	}
	for _, tc := range cases {
		got := serviceDegradedFor(tc.since, now)
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("serviceDegradedFor(%v) = %q, want it to start with %q", tc.since, got, tc.want)
		}
	}
	// A clock skew that puts the transition in the future must not render a
	// negative age.
	future := now.Add(time.Hour)
	if got := serviceDegradedFor(&future, now); !strings.HasPrefix(got, "0s") {
		t.Errorf("future since = %q, want it clamped to 0s", got)
	}
}

// ssBarLine returns the action bar's key line (the last row of the frame).
func ssBarLine(t *testing.T, view string) string {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		t.Fatal("empty view")
	}
	return lines[len(lines)-1]
}

// ---------------------------------------------------------------------------
// Reachability
// ---------------------------------------------------------------------------

// The screen is reached from Settings — a surface the sidebar already opens —
// rather than by taking another global letter, which the redesign is retiring.
// Pressed through the ROOT, not the screen: that is the only way to catch the
// key being eaten by a global before Settings ever sees it (the sc-5dqy
// reachability class of bug).
func TestSettings_SOpensServiceStatus(t *testing.T) {
	r := newTestRoot(NewSettingsScreen(Deps{}))
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	if _, stillSettings := after.screen.(*SettingsScreen); !stillSettings {
		t.Fatalf("S left Settings for %T before the switch resolved — a global ate it", after.screen)
	}
	if cmd == nil {
		t.Fatal("S on Settings produced no command — the key never reached the screen")
	}
	sw, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("S produced %T, want a SwitchScreenMsg", cmd())
	}
	if _, ok := sw.Screen.(*ServiceStatusScreen); !ok {
		t.Fatalf("S switched to %T, want *ServiceStatusScreen", sw.Screen)
	}
	if sw.Workspace != WSSettings {
		t.Errorf("S landed in workspace %q, want settings", sw.Workspace)
	}
}

// The Settings screen advertises the key, and names what is wrong when
// something is — an operator who saw the chip should not have to guess.
func TestSettings_ShowsTheServiceStatusEntryAndAnyDegradation(t *testing.T) {
	healthy := NewSettingsScreen(Deps{Health: NewServiceHealth()})
	out := healthy.View()
	if !strings.Contains(out, "S service status") {
		t.Errorf("Settings does not advertise the status screen:\n%s", out)
	}
	if strings.Contains(out, "⚠") {
		t.Errorf("Settings warns with nothing degraded:\n%s", out)
	}

	degraded := NewSettingsScreen(ssDegradedDeps(map[string]string{
		omsapi.ServiceKeyEmail: omsapi.ServiceStateOpen,
	}))
	if !strings.Contains(degraded.View(), "Email delivery unavailable") {
		t.Errorf("Settings does not name the degraded service:\n%s", degraded.View())
	}
}
