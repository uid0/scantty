package omsapi

import (
	"context"
	"net/http"
	"testing"
)

// Contract tests for op-m3so — the work-order stopwatch (whole job + per step).
//
// The pinned shape: elapsed_seconds is a plain JSON integer that the server
// computes LIVE (a running segment is already folded in), is_timing is a bool,
// started_at is a nullable timestamp, and estimated_time_minutes rides on the
// work order so actual-vs-estimate needs no second fetch. Drift in any of these
// key names is a clock stuck at 00:00, not an error — hence the pins.

// TestGetWorkOrder_ParsesTimerFields pins the read half at both levels: the work
// order's own clock and each step's.
func TestGetWorkOrder_ParsesTimerFields(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":7,"title":"Quarterly PM","status":"in_progress",
		"started_at":"2026-07-20T14:05:00Z",
		"elapsed_seconds":3725,"is_timing":true,
		"estimated_time_minutes":45,
		"task_completions":[
			{"id":"tc-1","task_title":"Drain the sump","is_completed":true,
			 "elapsed_seconds":600,"is_timing":false},
			{"id":"tc-2","task_title":"Replace the filter","is_completed":false,
			 "elapsed_seconds":90,"is_timing":true}
		]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if wo.ElapsedSeconds != 3725 {
		t.Errorf("elapsed_seconds = %d, want 3725", wo.ElapsedSeconds)
	}
	if !wo.IsTiming {
		t.Error("is_timing = false, want true")
	}
	if wo.StartedAt == nil || wo.StartedAt.UTC().Format("2006-01-02 15:04") != "2026-07-20 14:05" {
		t.Errorf("started_at = %v", wo.StartedAt)
	}
	if wo.EstimatedTimeMin == nil || *wo.EstimatedTimeMin != 45 {
		t.Errorf("estimated_time_minutes = %v", wo.EstimatedTimeMin)
	}
	if len(wo.TaskCompletions) != 2 {
		t.Fatalf("task_completions = %+v", wo.TaskCompletions)
	}
	if wo.TaskCompletions[0].ElapsedSeconds != 600 || wo.TaskCompletions[0].IsTiming {
		t.Errorf("step[0] timer = %+v", wo.TaskCompletions[0])
	}
	// Only one step runs at a time — the backend pauses the others — so a
	// running step is the exception the display has to catch.
	if wo.TaskCompletions[1].ElapsedSeconds != 90 || !wo.TaskCompletions[1].IsTiming {
		t.Errorf("step[1] timer = %+v", wo.TaskCompletions[1])
	}
}

// TestGetWorkOrder_TimerFieldsNullOrAbsent: a WO nobody has clocked reports
// started_at null and zeroes, and a backend older than op-m3so omits the keys
// entirely. Both decode to the same quiet zero state rather than failing the
// whole work-order decode over a stopwatch.
func TestGetWorkOrder_TimerFieldsNullOrAbsent(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK, `{
		"id":7,"title":"Ad-hoc fix","status":"open",
		"started_at":null,"elapsed_seconds":0,"is_timing":false,
		"estimated_time_minutes":null,
		"task_completions":[{"id":"tc-1","task_title":"Look at it"}]
	}`, &cap)
	defer srv.Close()

	wo, err := New(srv.URL).GetWorkOrder(context.Background(), "7")
	if err != nil {
		t.Fatalf("GetWorkOrder: %v", err)
	}
	if wo.StartedAt != nil {
		t.Errorf("started_at = %v, want nil", wo.StartedAt)
	}
	if wo.ElapsedSeconds != 0 || wo.IsTiming {
		t.Errorf("timer = %d/%v, want 0/false", wo.ElapsedSeconds, wo.IsTiming)
	}
	if wo.EstimatedTimeMin != nil {
		t.Errorf("estimated_time_minutes = %v, want nil", wo.EstimatedTimeMin)
	}
	// The step carried no timer keys at all.
	if wo.TaskCompletions[0].ElapsedSeconds != 0 || wo.TaskCompletions[0].IsTiming {
		t.Errorf("step timer = %+v, want zero", wo.TaskCompletions[0])
	}
}

// TestTimerWorkOrder_PostsAction pins the whole-job endpoint: POST to
// .../timer/ carrying nothing but {"action": ...}.
func TestTimerWorkOrder_PostsAction(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":7,"title":"Quarterly PM","status":"in_progress","elapsed_seconds":5,"is_timing":true,"changed":true}`,
		&cap)
	defer srv.Close()

	wo, err := New(srv.URL).TimerWorkOrder(context.Background(), "7", TimerStart)
	if err != nil {
		t.Fatalf("TimerWorkOrder: %v", err)
	}
	if cap.method != "POST" {
		t.Errorf("method = %q, want POST", cap.method)
	}
	if cap.path != "/api/inventory/work-orders/7/timer/" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.body["action"] != "start" {
		t.Errorf("action = %v, want start", cap.body["action"])
	}
	if len(cap.body) != 1 {
		t.Errorf("body = %v, want action only", cap.body)
	}
	// The action echoes the refreshed work order, so the caller can render the
	// new state without inventing it.
	if !wo.IsTiming || wo.ElapsedSeconds != 5 {
		t.Errorf("response = %+v", wo)
	}
}

// TestTimerWorkOrder_PausesWithPauseAction: the same endpoint stops the clock —
// the verb lives entirely in the body, so both directions must be pinned.
func TestTimerWorkOrder_PausesWithPauseAction(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":7,"status":"in_progress","elapsed_seconds":1800,"is_timing":false,"changed":true}`, &cap)
	defer srv.Close()

	if _, err := New(srv.URL).TimerWorkOrder(context.Background(), "7", TimerPause); err != nil {
		t.Fatalf("TimerWorkOrder: %v", err)
	}
	if cap.body["action"] != "pause" {
		t.Errorf("action = %v, want pause", cap.body["action"])
	}
	if TimerStart != "start" || TimerPause != "pause" {
		t.Errorf("timer action constants drifted: %q/%q", TimerStart, TimerPause)
	}
}

// TestTimerWorkOrderTask_PostsAction pins the per-step endpoint. Note the path
// nests the step under its work order — the same shape as the complete action —
// and the response is the single step, not the work order.
func TestTimerWorkOrderTask_PostsAction(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusOK,
		`{"id":"tc-2","task_title":"Replace the filter","elapsed_seconds":90,"is_timing":true,"changed":true}`,
		&cap)
	defer srv.Close()

	tc, err := New(srv.URL).TimerWorkOrderTask(context.Background(), "7", "tc-2", TimerStart)
	if err != nil {
		t.Fatalf("TimerWorkOrderTask: %v", err)
	}
	if cap.method != "POST" {
		t.Errorf("method = %q, want POST", cap.method)
	}
	if cap.path != "/api/inventory/work-orders/7/tasks/tc-2/timer/" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.body["action"] != "start" {
		t.Errorf("action = %v, want start", cap.body["action"])
	}
	if !tc.IsTiming || tc.ElapsedSeconds != 90 {
		t.Errorf("response = %+v", tc)
	}
}

// TestTimerWorkOrder_ErrorSurfaces: an unknown action is a 400 from the backend
// (it refuses to silently no-op a typo), and that has to reach the caller as an
// error rather than a zero-valued work order.
func TestTimerWorkOrder_ErrorSurfaces(t *testing.T) {
	var cap capture
	srv := captureServer(t, http.StatusBadRequest, `{"detail":"action must be one of start, pause"}`, &cap)
	defer srv.Close()

	if _, err := New(srv.URL).TimerWorkOrder(context.Background(), "7", "stop"); err == nil {
		t.Fatal("expected an error for a rejected action")
	}
}
