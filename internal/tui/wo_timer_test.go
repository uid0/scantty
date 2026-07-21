package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// TUI half of op-m3so — the work-order stopwatch. The server owns every total
// (elapsed_seconds already includes the segment in flight); the screen displays
// it, ticks a local copy forward between fetches, and re-anchors on each load.

func intPtr(v int) *int { return &v }

// tickOnce delivers one tick from the screen's LIVE chain (ticks carry the
// generation that emitted them) and reports whether the chain re-armed.
func tickOnce(t *testing.T, s *WorkOrderDetailScreen) (*WorkOrderDetailScreen, bool) {
	t.Helper()
	next, cmd := s.Update(woTickMsg{gen: s.tickGen})
	return next.(*WorkOrderDetailScreen), cmd != nil
}

// timerServer records the last request and replies with a fixed body. The screen
// re-fetches after any timer action, so it serves more than one request.
func timerServer(t *testing.T, resp string, method, path *string, body *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			*method = r.Method
			*path = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, body)
		}
		_, _ = w.Write([]byte(resp))
	}))
}

// TestWODetailRendersElapsedTimer: the clock sits in the header block (above the
// description, so a running job is readable without scrolling) and carries the
// actual-vs-estimate comparison the timer exists to produce.
func TestWODetailRendersElapsedTimer(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Title: "Quarterly PM", Description: "spindle service",
		Status: "in_progress", ElapsedSeconds: 1125, IsTiming: true,
		EstimatedTimeMin: intPtr(30),
	})
	out := s.renderBody()

	if !strings.Contains(out, "Elapsed: 18:45") {
		t.Errorf("missing MM:SS clock: %q", out)
	}
	if !strings.Contains(out, "running") {
		t.Errorf("missing running marker: %q", out)
	}
	if !strings.Contains(out, "19m / est 30m") {
		t.Errorf("missing actual-vs-estimate summary: %q", out)
	}
	elapsed, desc := strings.Index(out, "Elapsed:"), strings.Index(out, "spindle service")
	if elapsed < 0 || (desc >= 0 && elapsed > desc) {
		t.Errorf("timer must render in the header block (elapsed=%d desc=%d)", elapsed, desc)
	}
}

// TestWODetailRendersIdleTimer: a clock nobody started still renders — 00:00 is
// what tells the operator the key exists — and reports no running marker.
func TestWODetailRendersIdleTimer(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{ID: "wo1", Title: "Ad-hoc", Status: "open"})
	out := s.renderBody()
	if !strings.Contains(out, "Elapsed: 00:00") || !strings.Contains(out, "0m on job") {
		t.Errorf("idle timer line wrong: %q", out)
	}
	if strings.Contains(out, "running") {
		t.Errorf("idle clock must not claim to be running: %q", out)
	}
}

// TestWODetailRendersStartedAt: started_at is when work FIRST started and is
// never moved by a later resume, so it belongs with the dates, not the clock.
func TestWODetailRendersStartedAt(t *testing.T) {
	started := mustTime(t, "2026-07-20T14:05:00Z")
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Title: "Quarterly PM", Status: "in_progress", StartedAt: &started,
	})
	out := s.renderBody()
	if !strings.Contains(out, "Started: 2026-07-20") {
		t.Errorf("missing Started date: %q", out)
	}
	dates, started2 := strings.Index(out, "Dates"), strings.Index(out, "Started:")
	if dates < 0 || started2 < dates {
		t.Errorf("Started belongs in the Dates section (dates=%d started=%d)", dates, started2)
	}
}

// TestWODetailTimerKeyStarts: 's' on an idle clock posts action=start.
func TestWODetailTimerKeyStarts(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := timerServer(t, `{"id":"wo1","status":"in_progress","elapsed_seconds":1,"is_timing":true}`, &method, &path, &body)
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Title: "Lube pump", Status: "open"})

	next, cmd := s.Update(woRuneKey("s"))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("expected a timer cmd")
	}
	if !s.timerPending {
		t.Error("timerPending should gate a second press while one is in flight")
	}
	msg, ok := cmd().(woTimerToggledMsg)
	if !ok {
		t.Fatalf("msg = %T, want woTimerToggledMsg", cmd())
	}
	if msg.err != nil {
		t.Fatalf("timer err: %v", msg.err)
	}
	if method != "POST" || path != "/api/inventory/work-orders/wo1/timer/" {
		t.Fatalf("request = %s %s", method, path)
	}
	if body["action"] != "start" {
		t.Errorf("action = %v, want start", body["action"])
	}
}

// TestWODetailTimerKeyPauses: the key is a toggle — a running clock pauses.
func TestWODetailTimerKeyPauses(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := timerServer(t, `{"id":"wo1","status":"in_progress","elapsed_seconds":900,"is_timing":false}`, &method, &path, &body)
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{
		ID: "wo1", Title: "Lube pump", Status: "in_progress", ElapsedSeconds: 880, IsTiming: true,
	})

	_, cmd := s.Update(woRuneKey("s"))
	if cmd == nil {
		t.Fatal("expected a timer cmd")
	}
	cmd()
	if body["action"] != "pause" {
		t.Errorf("action = %v, want pause", body["action"])
	}
}

// TestWODetailTimerRefetchesAfterAction: the echoed object can't be trusted to
// describe the whole work order (starting a step pauses another and can flip the
// status), so a completed toggle reloads.
func TestWODetailTimerRefetchesAfterAction(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://127.0.0.1:0"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	s.timerPending = true

	next, cmd := s.Update(woTimerToggledMsg{action: "start", label: "timer"})
	s = next.(*WorkOrderDetailScreen)
	if s.timerPending {
		t.Error("timerPending should clear when the action lands")
	}
	if cmd == nil {
		t.Fatal("expected a status + reload batch")
	}
}

// TestWODetailTimerBlockedOnCompleted: completing a work order finalizes its
// clocks server-side and stamps the total on the maintenance log, so the screen
// refuses to restart one — no request is made and the hint disappears.
func TestWODetailTimerBlockedOnCompleted(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"id":"wo1"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{
		ID: "wo1", Title: "Done", Status: "completed", ElapsedSeconds: 2400,
	})

	next, cmd := s.Update(woRuneKey("s"))
	s = next.(*WorkOrderDetailScreen)
	if s.timerPending {
		t.Error("a blocked toggle must not mark the timer pending")
	}
	if cmd != nil {
		cmd() // a Status cmd, never an HTTP call
	}
	if hits != 0 {
		t.Errorf("hit the API %d time(s) on a completed WO, want 0", hits)
	}
	if hint := s.footerHint(); strings.Contains(hint, "s start") || strings.Contains(hint, "s pause") {
		t.Errorf("completed WO should not offer the timer key: %q", hint)
	}
	// The recorded total still shows — that is the number the job produced.
	if !strings.Contains(s.renderBody(), "Elapsed: 40:00") {
		t.Errorf("completed WO should still report its total: %q", s.renderBody())
	}
}

// TestWODetailFooterHintTracksTimerState: the hint names the action the key will
// perform, so an operator can tell a running clock from a stopped one.
func TestWODetailFooterHintTracksTimerState(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{ID: "wo1", Status: "in_progress"})
	if hint := s.footerHint(); !strings.Contains(hint, "s start") {
		t.Errorf("idle hint = %q, want 's start'", hint)
	}
	s.wo.IsTiming = true
	if hint := s.footerHint(); !strings.Contains(hint, "s pause") {
		t.Errorf("running hint = %q, want 's pause'", hint)
	}
}

// TestWODetailTaskPickerStepTimer: 's' inside the picker clocks the highlighted
// step, posting to the step-scoped endpoint.
func TestWODetailTaskPickerStepTimer(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := timerServer(t, `{"id":"tc2","task_title":"Replace filter","is_timing":true,"elapsed_seconds":1}`, &method, &path, &body)
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{
		ID: "wo1", Title: "Lube pump", Status: "in_progress",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{
			{ID: "tc1", TaskTitle: "Drain sump"},
			{ID: "tc2", TaskTitle: "Replace filter"},
		},
	})

	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	next, _ = s.Update(woRuneKey("j")) // highlight the second step
	s = next.(*WorkOrderDetailScreen)

	next, cmd := s.Update(woRuneKey("s"))
	s = next.(*WorkOrderDetailScreen)
	if cmd == nil {
		t.Fatal("expected a step-timer cmd")
	}
	msg, ok := cmd().(woTimerToggledMsg)
	if !ok {
		t.Fatalf("msg = %T, want woTimerToggledMsg", cmd())
	}
	if msg.label != "Replace filter" {
		t.Errorf("label = %q, want the step title", msg.label)
	}
	if path != "/api/inventory/work-orders/wo1/tasks/tc2/timer/" {
		t.Fatalf("path = %q", path)
	}
	if body["action"] != "start" {
		t.Errorf("action = %v, want start", body["action"])
	}
}

// TestWODetailTaskPickerStepTimerPauses: a running step pauses instead.
func TestWODetailTaskPickerStepTimerPauses(t *testing.T) {
	var method, path string
	var body map[string]any
	srv := timerServer(t, `{"id":"tc1","is_timing":false,"elapsed_seconds":300}`, &method, &path, &body)
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{
		ID: "wo1", Status: "in_progress",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{
			{ID: "tc1", TaskTitle: "Drain sump", ElapsedSeconds: 290, IsTiming: true},
		},
	})

	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	_, cmd := s.Update(woRuneKey("s"))
	if cmd == nil {
		t.Fatal("expected a step-timer cmd")
	}
	cmd()
	if body["action"] != "pause" {
		t.Errorf("action = %v, want pause", body["action"])
	}
	if path != "/api/inventory/work-orders/wo1/tasks/tc1/timer/" {
		t.Errorf("path = %q", path)
	}
}

// TestWODetailStepClocksRender: per-step time shows in both the read-only body
// and the picker, with a marker on the one step that is running. Steps nobody
// timed stay silent rather than printing a column of 00:00.
func TestWODetailStepClocksRender(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Title: "Lube pump", Status: "in_progress",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{
			{ID: "tc1", TaskTitle: "Drain sump", IsCompleted: true, ElapsedSeconds: 600},
			{ID: "tc2", TaskTitle: "Replace filter", ElapsedSeconds: 65, IsTiming: true},
			{ID: "tc3", TaskTitle: "Untouched step"},
		},
	})

	body := s.renderBody()
	if !strings.Contains(body, "10:00") || !strings.Contains(body, "01:05") {
		t.Errorf("body missing per-step clocks: %q", body)
	}
	if !strings.Contains(body, "01:05 ● running") {
		t.Errorf("body missing the running step marker: %q", body)
	}

	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	picker := s.renderTaskPicker()
	if !strings.Contains(picker, "time: 10:00") || !strings.Contains(picker, "time: 01:05") {
		t.Errorf("picker missing per-step clocks: %q", picker)
	}
	if !strings.Contains(picker, "s timer") {
		t.Errorf("picker footer missing the timer key: %q", picker)
	}
	// The untimed step contributes no clock line at all.
	if strings.Count(picker, "time: ") != 2 {
		t.Errorf("untimed step should print no clock: %q", picker)
	}
}

// TestWODetailTickAdvancesRunningClock: between fetches a local tick advances
// the display, and only for clocks that are actually running. The offset is
// display-only — it is never sent anywhere.
func TestWODetailTickAdvancesRunningClock(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Status: "in_progress", ElapsedSeconds: 58, IsTiming: true,
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{
			{ID: "tc1", TaskTitle: "Paused step", ElapsedSeconds: 120},
		},
	})
	if !s.ticking {
		t.Fatal("a running clock should start the tick chain")
	}

	for i := 0; i < 3; i++ {
		var armed bool
		if s, armed = tickOnce(t, s); !armed {
			t.Fatalf("tick %d did not re-arm the chain", i)
		}
	}
	out := s.renderBody()
	if !strings.Contains(out, "Elapsed: 01:01") {
		t.Errorf("running clock did not advance to 01:01: %q", out)
	}
	// A paused step must not drift with the work-order clock.
	if !strings.Contains(out, "02:00") {
		t.Errorf("paused step clock should stay put: %q", out)
	}
	// The wire value is untouched — the server owns the total.
	if s.wo.ElapsedSeconds != 58 {
		t.Errorf("tick mutated the server value: %d", s.wo.ElapsedSeconds)
	}
}

// TestWODetailTickReanchorsOnFetch: a fresh server total already includes the
// running segment, so the local offset restarts at zero instead of stacking on
// top of it (which would double-count every refresh).
func TestWODetailTickReanchorsOnFetch(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{ID: "wo1", Status: "in_progress", ElapsedSeconds: 10, IsTiming: true})
	for i := 0; i < 5; i++ {
		s, _ = tickOnce(t, s)
	}
	if s.tickOffset != 5 {
		t.Fatalf("tickOffset = %d, want 5", s.tickOffset)
	}

	next, _ := s.Update(woDetailLoadedMsg{wo: &omsapi.WorkOrder{
		ID: "wo1", Status: "in_progress", ElapsedSeconds: 15, IsTiming: true,
	}})
	s = next.(*WorkOrderDetailScreen)
	if s.tickOffset != 0 {
		t.Errorf("tickOffset = %d after a fetch, want 0", s.tickOffset)
	}
	if !strings.Contains(s.renderBody(), "Elapsed: 00:15") {
		t.Errorf("display should re-anchor on the server total: %q", s.renderBody())
	}
}

// TestWODetailTickStopsWhenPaused: once nothing is running the chain ends rather
// than burning a wakeup every second, and a stale tick from the old chain dies
// instead of re-arming it.
func TestWODetailTickStopsWhenPaused(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{ID: "wo1", Status: "in_progress", ElapsedSeconds: 10, IsTiming: true})

	next, _ := s.Update(woDetailLoadedMsg{wo: &omsapi.WorkOrder{
		ID: "wo1", Status: "in_progress", ElapsedSeconds: 42, IsTiming: false,
	}})
	s = next.(*WorkOrderDetailScreen)
	if s.ticking {
		t.Error("tick chain should stop when the clock is paused")
	}
	s, armed := tickOnce(t, s)
	if armed {
		t.Error("a stale tick must not re-arm the chain")
	}
	if s.tickOffset != 0 {
		t.Errorf("stale tick advanced the display: %d", s.tickOffset)
	}
}

// TestWODetailTickSurvivesNavigation: the back stack hands this same screen
// instance back, so Init has to abandon the chain it left behind and arm a
// fresh one — otherwise the clock freezes on a return visit. The tick still in
// flight from the abandoned chain must not arm a SECOND one, which would count
// seconds twice as fast.
func TestWODetailTickSurvivesNavigation(t *testing.T) {
	running := &omsapi.WorkOrder{ID: "wo1", Status: "in_progress", ElapsedSeconds: 10, IsTiming: true}
	s := loadWO(t, Deps{OMS: omsapi.New("http://127.0.0.1:0"), Ctx: context.Background()}, running)
	staleGen := s.tickGen

	// Navigate away and back: Init reloads, and the reload re-arms the chain.
	s.Init()
	if s.ticking {
		t.Error("Init should abandon the chain the screen left behind")
	}
	next, _ := s.Update(woDetailLoadedMsg{wo: running})
	s = next.(*WorkOrderDetailScreen)
	if !s.ticking {
		t.Fatal("a return visit to a running clock should re-arm the chain")
	}

	// The abandoned chain's last tick lands now. It must be ignored.
	next, cmd := s.Update(woTickMsg{gen: staleGen})
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		t.Error("a tick from the abandoned chain must not arm a second one")
	}
	if s.tickOffset != 0 {
		t.Errorf("stale tick advanced the display: %d", s.tickOffset)
	}
	// The live chain still works.
	if s, armed := tickOnce(t, s); !armed || s.tickOffset != 1 {
		t.Errorf("live chain broken: armed=%v offset=%d", armed, s.tickOffset)
	}
}

// TestWODetailTickChainNotDuplicated: repeated fetches while the clock runs must
// not leave two chains alive — two would count seconds twice as fast.
func TestWODetailTickChainNotDuplicated(t *testing.T) {
	running := &omsapi.WorkOrder{ID: "wo1", Status: "in_progress", ElapsedSeconds: 10, IsTiming: true}
	s := loadWO(t, Deps{}, running)
	if !s.ticking {
		t.Fatal("expected a live chain")
	}
	next, cmd := s.Update(woDetailLoadedMsg{wo: running})
	s = next.(*WorkOrderDetailScreen)
	if cmd != nil {
		t.Error("a second fetch while ticking must not start another chain")
	}
}

// TestWODetailStepTimerBlockedOnCompleted: the picker's key obeys the same gate
// as the body's — a completed WO's clocks are final.
func TestWODetailStepTimerBlockedOnCompleted(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"id":"tc1"}`))
	}))
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{
		ID: "wo1", Status: "completed",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{{ID: "tc1", TaskTitle: "Drain sump", ElapsedSeconds: 60}},
	})
	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	_, cmd := s.Update(woRuneKey("s"))
	if cmd != nil {
		cmd()
	}
	if hits != 0 {
		t.Errorf("hit the API %d time(s) on a completed WO, want 0", hits)
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		secs int
		want string
	}{
		{0, "00:00"},
		{5, "00:05"},
		{65, "01:05"},
		{599, "09:59"},
		{3600, "1:00:00"},
		{3725, "1:02:05"},
		{-4, "00:00"}, // never render a negative clock
	}
	for _, c := range cases {
		if got := formatElapsed(c.secs); got != c.want {
			t.Errorf("formatElapsed(%d) = %q, want %q", c.secs, got, c.want)
		}
	}
}

func TestElapsedSummary(t *testing.T) {
	if got := elapsedSummary(1125, intPtr(30)); got != "19m / est 30m" {
		t.Errorf("with estimate = %q", got)
	}
	if got := elapsedSummary(1125, nil); got != "19m on job" {
		t.Errorf("without estimate = %q", got)
	}
	// A template estimate of 0 is no estimate at all — don't print "est 0m".
	if got := elapsedSummary(60, intPtr(0)); got != "1m on job" {
		t.Errorf("zero estimate = %q", got)
	}
	// Rounds rather than truncates: 90s is closer to 2m than to 1m.
	if got := elapsedSummary(90, nil); got != "2m on job" {
		t.Errorf("rounding = %q", got)
	}
}

// mustTime parses an RFC3339 timestamp for a fixture.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return ts
}

// TestTimerPastTense: the status line reports what happened, and "start" + "d"
// is not the past tense of start.
func TestTimerPastTense(t *testing.T) {
	if got := timerPastTense(omsapi.TimerStart); got != "started" {
		t.Errorf("start → %q, want started", got)
	}
	if got := timerPastTense(omsapi.TimerPause); got != "paused" {
		t.Errorf("pause → %q, want paused", got)
	}
}

func TestWODetailTimerErrorSurfaces(t *testing.T) {
	deps := Deps{OMS: omsapi.New("http://127.0.0.1:0"), Ctx: context.Background()}
	s := loadWO(t, deps, &omsapi.WorkOrder{ID: "wo1", Status: "open"})
	s.timerPending = true

	next, cmd := s.Update(woTimerToggledMsg{action: "start", label: "timer", err: context.DeadlineExceeded})
	s = next.(*WorkOrderDetailScreen)
	if s.timerPending {
		t.Error("timerPending should clear on failure so the key works again")
	}
	if cmd == nil {
		t.Fatal("expected a status cmd reporting the failure")
	}
}

// Guard: the timer keys must not collide with the global hotkey layer. Lowercase
// 's' is free there (unlike M/U, which screens have to claim), and the picker
// takes raw input, so nothing upstream can steal it.
func TestWODetailPickerTakesRawInput(t *testing.T) {
	s := loadWO(t, Deps{}, &omsapi.WorkOrder{
		ID: "wo1", Status: "open",
		TaskCompletions: []omsapi.WorkOrderTaskCompletion{{ID: "tc1", TaskTitle: "Step"}},
	})
	next, _ := s.Update(woRuneKey("t"))
	s = next.(*WorkOrderDetailScreen)
	if !s.WantsRawInput() {
		t.Error("the picker must claim raw input for 's' to reach it")
	}
}
