package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// The scanned-work-order review, driven through the REAL key handlers against a
// fake that records what was actually sent.
//
// Both halves of the gap are held here. The one that matters first is
// VISIBILITY: before this, a sheet could be uploaded from the terminal and
// nothing anywhere said one was waiting, so the review only ever happened in a
// browser. The second is the review itself, and what these tests assert about
// it is the REQUEST — which endpoint, which submission, which target_ids,
// whether confirm_complete rode along — because a screen that draws a
// convincing confirm and posts the wrong scope is the same dead end with a nicer
// pane.

// ---------------------------------------------------------------------------
// A fake that remembers what it was asked to do
// ---------------------------------------------------------------------------

type woReviewCall struct {
	method  string
	path    string
	body    map[string]any
	rawBody string
}

// woReviewFake serves one work order with two parked sheets and records every
// write. It answers the writes the way the real backend does — the reply shapes
// are the recorded ones in internal/omsapi/testdata — but it deliberately does
// NOT re-derive the queue: what these tests are about is the request.
type woReviewFake struct {
	mu       sync.Mutex
	calls    []woReviewCall
	wo       *omsapi.WorkOrder
	applyErr int // when non-zero, the status apply-pending answers with
}

func (f *woReviewFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		raw, _ := io.ReadAll(r.Body)
		call := woReviewCall{method: r.Method, path: r.URL.Path, rawBody: string(raw)}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &call.body)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasSuffix(r.URL.Path, "/apply-pending/"):
			f.calls = append(f.calls, call)
			if f.applyErr != 0 {
				w.WriteHeader(f.applyErr)
				_, _ = w.Write([]byte(`{"detail": "Submission not found for this work order."}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"detail": "Applied 1 pending change(s).", "submission_status": "pending_review",
				"applied_count": 1, "work_order_completed": call.body["confirm_complete"] == true,
				"work_order_status": "in_progress",
			})
		case strings.HasSuffix(r.URL.Path, "/discard-pending/"):
			f.calls = append(f.calls, call)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"detail":            "Discarded 1 pending change(s).",
				"submission_status": "pending_review", "dropped_count": 1,
			})
		default:
			_ = json.NewEncoder(w).Encode(f.wo)
		}
	}
}

func (f *woReviewFake) writes() []woReviewCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]woReviewCall(nil), f.calls...)
}

// woReviewDriven builds the review screen behind a real Root against the fake,
// sized to the width the interface is modelled on.
func woReviewDriven(t *testing.T) (*woReviewFake, Root, *WorkOrderScanReviewScreen) {
	t.Helper()
	fake := &woReviewFake{wo: woReviewWorkOrder()}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewWorkOrderScanReviewScreen(deps, woReviewFixtureWO, woReviewWorkOrder())
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)
	r = pump(t, r, screen.Init(), 0)
	return fake, r, screen
}

// woReviewWalkTo moves the cursor onto the row whose target is id, BOUNDED: an
// unbounded `for cursor != want { down }` turns a declined key into a hang, and
// the package then fails by timing out with whichever test happened to be
// running named in the panic (AGENTS.md).
func woReviewWalkTo(t *testing.T, r Root, s *WorkOrderScanReviewScreen, target string) Root {
	t.Helper()
	for i := 0; i <= len(s.rows); i++ {
		if seat, ok := s.seatOf(s.cursor); ok && seat.target == target &&
			seat.kind == woReviewChangeRow {
			return r
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	t.Fatalf("no row carries target %q; rows = %d", target, len(s.rows))
	return r
}

// ---------------------------------------------------------------------------
// Seeing that a sheet is waiting
// ---------------------------------------------------------------------------

// THE FIRST HALF OF THE GAP, and the one an operator hits before any of the
// rest: a scan waiting on a work order has to be visible from the LIST, not
// only from a detail screen somebody already opened. Both fields ride
// WorkOrderListSerializer, so this costs no extra request.
func TestWorkOrderList_ARowSaysAScanIsWaiting(t *testing.T) {
	waiting := omsapi.WorkOrder{
		ID: "wo-1", DisplayTitle: "Quarterly lube", AssetName: "Haas VF-2",
		HasPendingReview: true, PendingReviewCount: 2,
	}
	got := workOrderListSubtitle(waiting)
	if !strings.Contains(got, "2 scanned sheets to review") {
		t.Errorf("subtitle = %q, want it to name the two sheets waiting", got)
	}
	if !strings.HasPrefix(stripANSI(got), "⚠") {
		t.Errorf("subtitle = %q, want the badge to LEAD — the asset is what an operator "+
			"scanning the list is not looking for", got)
	}
	if !strings.Contains(got, "Haas VF-2") {
		t.Errorf("subtitle = %q dropped the asset", got)
	}

	one := waiting
	one.PendingReviewCount = 1
	if got := workOrderListSubtitle(one); !strings.Contains(got, "1 scanned sheet to review") {
		t.Errorf("one sheet reads %q", got)
	}

	// And a work order with nothing waiting says nothing: a badge on every row
	// is a badge nobody notices.
	quiet := waiting
	quiet.HasPendingReview, quiet.PendingReviewCount = false, 0
	if got := workOrderListSubtitle(quiet); got != "Haas VF-2" {
		t.Errorf("a work order with no scan waiting reads %q, want just the asset", got)
	}
}

// A row an operator cannot NAME is not a row they can act on, and until now
// every work-order row was blank: neither serializer carries a `title`.
func TestWorkOrderList_ARowHasAName(t *testing.T) {
	wo := omsapi.WorkOrder{ID: "wo-1", ShortID: "WO-4CB3", DisplayTitle: "Quarterly lube"}
	if got := workOrderName(wo); got != "Quarterly lube" {
		t.Errorf("name = %q, want the display title the serializer really sends", got)
	}
	nameless := omsapi.WorkOrder{ID: "wo-1", ShortID: "WO-4CB3"}
	if got := workOrderName(nameless); got != "WO-4CB3" {
		t.Errorf("a job with no display title reads %q, want its short id rather than nothing", got)
	}
}

// The DETAIL screen says so too, in the body and on the bar, and the bar names
// the key only where it acts.
func TestWorkOrderDetail_SaysAScanIsWaitingAndNamesTheKey(t *testing.T) {
	s := NewWorkOrderDetailScreen(Deps{}, woReviewFixtureWO)
	s.loading = false
	s.wo = woReviewWorkOrder()
	s.refreshBody()

	body := stripANSI(s.renderBody())
	if !strings.Contains(body, "Scanned sheet awaiting review") {
		t.Errorf("the work-order body does not say a scan is waiting:\n%s", body)
	}
	if !strings.Contains(body, "R opens the review") {
		t.Error("the section does not name the key that reviews it")
	}
	if !strings.Contains(body, "nothing readable") {
		t.Error("the degraded sheet is not reported; a sheet with an empty queue still needs a human")
	}
	if !strings.Contains(stripANSI(s.footerHint()), "R review scan") {
		t.Errorf("the footer does not name R: %s", s.footerHint())
	}

	// And it does NOT, on a work order with no sheet parked — a key the bar
	// names must do something.
	s.wo.Submissions = nil
	s.refreshBody()
	if strings.Contains(stripANSI(s.renderBody()), "Scanned sheet awaiting review") {
		t.Error("the section is drawn on a work order with nothing waiting")
	}
	if strings.Contains(stripANSI(s.footerHint()), "R review scan") {
		t.Error("the footer names R where no sheet is waiting")
	}
}

// R really opens the review, through the root that dispatches it.
func TestWorkOrderDetail_ROpensTheReview(t *testing.T) {
	fake := &woReviewFake{wo: woReviewWorkOrder()}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()

	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	screen := NewWorkOrderDetailScreen(deps, woReviewFixtureWO)
	r := newTestRoot(screen)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = pump(t, next.(Root), screen.Init(), 0)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	if _, ok := r.screen.(*WorkOrderScanReviewScreen); !ok {
		t.Fatalf("R left the operator on %T, not the review screen", r.screen)
	}
}

// ---------------------------------------------------------------------------
// The request the confirm actually sends
// ---------------------------------------------------------------------------

// A SELECTION APPLIES ONLY WHAT WAS MARKED, and the assertion is the POST body:
// a screen that draws the right confirm and posts the whole queue would be
// applying readings the operator rejected.
func TestWOScanReview_ASelectiveApplyPostsOnlyTheMarkedTargets(t *testing.T) {
	fake, r, s := woReviewDriven(t)

	r = woReviewWalkTo(t, r, s, woReviewFixtureTask2)
	r = key(t, r, tea.KeyMsg{Type: tea.KeySpace})
	if len(s.selectionIn(woReviewQueuedSubmission())) != 1 {
		t.Fatalf("space marked %d readings, want 1", len(s.selectionIn(woReviewQueuedSubmission())))
	}
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != woReviewPhaseConfirm {
		t.Fatal("enter did not open the confirm")
	}
	// WHAT IS BEING APPLIED IS ON THE PANE BEFORE IT IS APPLIED. An apply an
	// operator cannot read first is the dead end this screen removes, moved one
	// keypress later.
	pane := stripANSI(clampToBox(s.View(), screenBodyWidth(80), 30))
	if !strings.Contains(pane, "Check backlash") {
		t.Errorf("the confirm does not name the reading it is about to apply:\n%s", pane)
	}
	if strings.Contains(pane, "Grease the ways") {
		t.Errorf("the confirm lists a reading that is NOT in the selection:\n%s", pane)
	}

	key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("sent %d writes, want 1: %+v", len(writes), writes)
	}
	got := writes[0]
	wantPath := fmt.Sprintf("/api/inventory/work-orders/%s/submissions/%s/apply-pending/",
		woReviewFixtureWO, woReviewFixtureSub)
	if got.method != http.MethodPost || got.path != wantPath {
		t.Errorf("posted %s %s, want POST %s", got.method, got.path, wantPath)
	}
	ids, _ := got.body["target_ids"].([]any)
	if len(ids) != 1 || ids[0] != woReviewFixtureTask2 {
		t.Errorf("target_ids = %#v, want just the marked reading", got.body["target_ids"])
	}
	if _, named := got.body["confirm_complete"]; named {
		t.Error("an apply sent confirm_complete; that key closes the work order")
	}
}

// WITH NOTHING MARKED THE WRITE IS THE WHOLE SHEET, and target_ids must be
// ABSENT rather than empty: the server applies everything only when it cannot
// see the key, and `[]` is "nothing" to it (measured on a live backend).
func TestWOScanReview_AnUnmarkedApplyPostsTheWholeSheet(t *testing.T) {
	fake, r, _ := woReviewDriven(t)

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("sent %d writes, want 1", len(writes))
	}
	if _, named := writes[0].body["target_ids"]; named {
		t.Errorf("a whole-sheet apply sent target_ids = %#v; the key must be absent",
			writes[0].body["target_ids"])
	}
	if strings.Contains(writes[0].rawBody, "target_ids") {
		t.Errorf("request body still spells target_ids: %s", writes[0].rawBody)
	}
}

// Discard posts to the OTHER endpoint with the same scope rules.
func TestWOScanReview_DiscardPostsTheSelectionToDiscardPending(t *testing.T) {
	fake, r, s := woReviewDriven(t)

	r = woReviewWalkTo(t, r, s, woReviewFixtureTask1)
	r = key(t, r, tea.KeyMsg{Type: tea.KeySpace})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	if s.phase != woReviewPhaseConfirm || s.confirmAction != woReviewDiscard {
		t.Fatalf("ctrl+x did not open the discard confirm (phase %v, action %v)",
			s.phase, s.confirmAction)
	}
	key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("sent %d writes, want 1", len(writes))
	}
	if !strings.HasSuffix(writes[0].path, "/discard-pending/") {
		t.Errorf("posted to %s, want the discard endpoint", writes[0].path)
	}
	ids, _ := writes[0].body["target_ids"].([]any)
	if len(ids) != 1 || ids[0] != woReviewFixtureTask1 {
		t.Errorf("target_ids = %#v", writes[0].body["target_ids"])
	}
}

// THE HUMAN GATE. Ctrl-F is the only key that asks the server to close the job,
// and it applies the WHOLE sheet — a selection would be a different write
// wearing the same key. Nothing predicts the outcome: the server answers
// whether it closed, and the screen reports that.
func TestWOScanReview_CompletePostsConfirmCompleteForTheWholeSheet(t *testing.T) {
	fake, r, s := woReviewDriven(t)

	// Marked FIRST, so the test shows the selection is deliberately ignored here
	// rather than that there happened not to be one.
	r = woReviewWalkTo(t, r, s, woReviewFixtureTask2)
	r = key(t, r, tea.KeyMsg{Type: tea.KeySpace})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlF})
	if s.phase != woReviewPhaseConfirm || s.confirmAction != woReviewComplete {
		t.Fatalf("ctrl+f did not open the completion confirm")
	}
	pane := stripANSI(clampToBox(s.View(), screenBodyWidth(80), 30))
	if !strings.Contains(pane, "close the work order") {
		t.Errorf("the completion confirm does not say it closes the job:\n%s", pane)
	}
	key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	writes := fake.writes()
	if len(writes) != 1 {
		t.Fatalf("sent %d writes, want 1", len(writes))
	}
	if writes[0].body["confirm_complete"] != true {
		t.Errorf("confirm_complete = %#v, want true", writes[0].body["confirm_complete"])
	}
	if _, named := writes[0].body["target_ids"]; named {
		t.Errorf("the completion narrowed itself to %#v; it applies the whole sheet",
			writes[0].body["target_ids"])
	}
}

// The completion is CONDITIONAL server-side and is never predicted here: it
// closes only when every required task is done, and answers 200 either way.
func TestWOScanReview_TheCompletionReportsWhatTheServerDid(t *testing.T) {
	closed := woReviewSummary(woReviewWroteMsg{complete: true, applied: &omsapi.WorkOrderApplyResult{
		AppliedCount: 4, WorkOrderCompleted: true, WorkOrderStatus: "completed"}})
	if !strings.Contains(closed, "is completed") {
		t.Errorf("a closed job reads %q", closed)
	}
	refused := woReviewSummary(woReviewWroteMsg{complete: true, applied: &omsapi.WorkOrderApplyResult{
		AppliedCount: 4, WorkOrderCompleted: false, WorkOrderStatus: "in_progress"}})
	if !strings.Contains(refused, "still in progress") {
		t.Errorf("a refused completion reads %q, want the status the server came back with", refused)
	}
	if !strings.Contains(refused, "every required task") {
		t.Errorf("a refused completion does not say WHY: %q", refused)
	}
}

// ---------------------------------------------------------------------------
// What the operator is told
// ---------------------------------------------------------------------------

// A READING THE SERVER GIVES NO ID CANNOT BE SELECTED, and the row says so
// rather than being silently dropped from a selective write. A refusal that
// cannot be satisfied from the frame it is drawn on is a dead end, so it names
// the way that DOES reach the reading.
func TestWOScanReview_AnUnselectableReadingSaysWhyAndNamesTheWayRound(t *testing.T) {
	_, r, s := woReviewDriven(t)

	// Walk onto the signature, which the recorded payload sends with a null
	// target_id.
	moved := false
	for i := 0; i <= len(s.rows); i++ {
		if c, ok := s.currentChange(); ok && c.Kind == omsapi.WorkOrderChangeSignature {
			moved = true
			break
		}
		r = key(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	if !moved {
		t.Fatal("no signature row on the fixture")
	}
	key(t, r, tea.KeyMsg{Type: tea.KeySpace})
	if !strings.Contains(s.note, "whole sheet") || !strings.Contains(s.note, "Enter") {
		t.Errorf("space on an unselectable reading answered %q; it must say why AND name "+
			"the way that reaches it", s.note)
	}
	if len(s.marked) != 0 {
		t.Error("an unselectable reading was marked anyway")
	}
}

// A REFUSAL REACHES THE OPERATOR AS A SENTENCE, not as JSON. Both endpoints
// answer a missing submission with DRF's bare {"detail": …}, which carries no
// `code`, so parseError hands the whole raw body over.
func TestWOScanReview_ARefusalIsOneLegibleLine(t *testing.T) {
	fake, r, _ := woReviewDriven(t)
	fake.applyErr = http.StatusNotFound

	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	s := r.screen.(*WorkOrderScanReviewScreen)
	if s.writeErr == "" {
		t.Fatal("the refusal was swallowed")
	}
	if strings.Contains(s.writeErr, "{") {
		t.Errorf("the operator reads raw JSON: %q", s.writeErr)
	}
	if !strings.Contains(s.writeErr, "Submission not found") {
		t.Errorf("the server's own sentence is gone: %q", s.writeErr)
	}
	// One line, at the bottom of the pane, left-justified — the layer's status
	// row, which is flattened and bounded by fitStatus.
	row := stripANSI(s.listStatus())
	if strings.Contains(row, "\n") {
		t.Errorf("the refusal is drawn on more than one line:\n%q", row)
	}
	if lipgloss.Width(row) > screenBodyWidth(80) {
		t.Errorf("the refusal row is %d cells against a pane of %d: %q",
			lipgloss.Width(row), screenBodyWidth(80), row)
	}
}

// AN IDENTIFIER IS NEVER DRAWN CUT. A UUID does not fit beside a label at 80
// columns, so the LAYOUT gives — the id runs onto its own line, whole — and it
// is the submission id every write here posts to.
func TestWOScanReview_TheSubmissionIDIsDrawnWhole(t *testing.T) {
	for _, w := range jdeDrawableWidths() {
		s := woReviewFixture()
		s.setSize(tea.WindowSizeMsg{Width: w, Height: 40})
		pane := stripANSI(clampToBox(s.View(), screenBodyWidth(w), 40))
		// Wrapped is still whole: strip the pane's line breaks and indentation
		// before looking for it, which is exactly what a reader does.
		flat := strings.Join(strings.Fields(pane), "")
		if !strings.Contains(flat, strings.ReplaceAll(woReviewFixtureSub, " ", "")) {
			t.Errorf("at %d columns the submission id is not on the pane whole:\n%s", w, pane)
		}
		if strings.Contains(pane, woReviewFixtureSub[:8]+"…") {
			t.Errorf("at %d columns the id is drawn abbreviated", w)
		}
	}
}

// WHAT APPLYING DOES IS NOT WHAT THE READER SAW, and both facts are on the
// pane. `omr_apply_mark(..., marked=True)` runs for every selected mark
// whatever its value, so a row reading `blank` that is applied ends up
// COMPLETE — measured against a live backend.
func TestWOScanReview_TheConfirmSaysApplyingMarksItDone(t *testing.T) {
	s := woReviewConfirmFixture(woReviewApply)
	s.setSize(tea.WindowSizeMsg{Width: 80, Height: 40})
	pane := stripANSI(clampToBox(s.View(), screenBodyWidth(80), 40))

	if !strings.Contains(pane, "reader: blank") {
		t.Errorf("the confirm does not say what the reader SAW:\n%s", pane)
	}
	if !strings.Contains(pane, "whatever the reader saw") {
		t.Errorf("the confirm does not say that applying marks it DONE regardless:\n%s", pane)
	}
	if !strings.Contains(pane, "moves stock") {
		t.Errorf("the confirm does not warn that a material mark moves stock:\n%s", pane)
	}
	// And it says what it does NOT do, because Ctrl-F is a different key.
	if !strings.Contains(pane, "does NOT close the work order") {
		t.Errorf("the apply confirm does not disclaim the completion:\n%s", pane)
	}
}

// A DEGRADED SHEET IS PARKED FOR A HUMAN WITH NOTHING TO APPLY, and the reason
// is what the frame has to carry — a screen gating on len(pending_changes)
// draws an empty pane where the server sent the one useful sentence.
func TestWOScanReview_ADegradedSheetShowsItsReason(t *testing.T) {
	wo := woReviewWorkOrder()
	wo.Submissions = []omsapi.WorkOrderSubmission{woReviewDegradedSubmission()}
	s := NewWorkOrderScanReviewScreen(Deps{}, woReviewFixtureWO, wo)
	s.loading = false
	s.setSize(tea.WindowSizeMsg{Width: 80, Height: 30})

	pane := stripANSI(clampToBox(s.View(), screenBodyWidth(80), 30))
	if !strings.Contains(pane, "Reprint the OMR form") {
		t.Errorf("the sheet's reason is not on the pane:\n%s", pane)
	}
	// Enter declines rather than posting a write the server would answer
	// "No pending changes to apply." to.
	s.openConfirm(woReviewApply)
	if s.phase == woReviewPhaseConfirm {
		t.Error("enter opened a confirm on a sheet with nothing queued")
	}
	if !strings.Contains(s.note, "nothing is queued") {
		t.Errorf("the decline said %q", s.note)
	}
}

// ---------------------------------------------------------------------------
// The bar is honest
// ---------------------------------------------------------------------------

// Pressed against the whole key SPACE, not against the bar's own vocabulary: a
// key bound in a handler and absent from a curated roster is pressed in NEITHER
// direction, which is verbatim how three dead keys reached an operator
// (AGENTS.md).
//
// "ACTS" EXCLUDES THE DECLINE NOTE, and that distinction is the whole reason
// this screen answers every inert key at all: a key that declines and says why
// has not acted, it has ANSWERED — the pane changes precisely so a keypress is
// never silent, and counting that as acting would make the rule forbid the
// answer. So each press is measured on the pane rendered with the screen's
// answer held at what it was before the press.
//
// FORWARD is asked per TOKEN and REVERSE per KEY, which is the layer's own
// asymmetry (jde_refused_pane_test.go): a token names a PAIR, so the claim is
// that SOME key it spells moves — `up` at the top of a scroll rightly moves
// nothing — while "a key that acts must be named" can only be asked of one key.
func TestWOScanReview_TheBarNamesExactlyTheKeysThatWork(t *testing.T) {
	cases := map[string]func() Screen{
		"list":             func() Screen { return woReviewFixture() },
		"apply confirm":    func() Screen { return woReviewConfirmFixture(woReviewApply) },
		"discard confirm":  func() Screen { return woReviewConfirmFixture(woReviewDiscard) },
		"complete confirm": func() Screen { return woReviewConfirmFixture(woReviewComplete) },
		"nothing waiting": func() Screen {
			wo := woReviewWorkOrder()
			wo.Submissions = nil
			s := NewWorkOrderScanReviewScreen(Deps{}, woReviewFixtureWO, wo)
			s.loading = false
			return s
		},
	}
	// Esc on the LIST is the ROOT's back step: it is named because it really
	// leaves, and it changes nothing this screen draws.
	// TestWOScanReview_EscReallyLeaves presses it through a real Root, which is
	// the only place that claim can be made.
	rootOwned := map[string]bool{"esc": true}

	// COLOUR IS FORCED, because the highlight IS the movement. lipgloss strips
	// every sequence when stdout is not a TTY, which is always in a test binary,
	// so a cursor that moved and a cursor that did not are byte-identical and
	// this sweep would report every honest movement key as dead (AGENTS.md).
	withColorProfile(t, termenv.TrueColor)

	named, acted, unnamed := 0, 0, 0
	for name, mk := range cases {
		// REVERSE: every key in the space, from the rest state.
		//
		// The bar is read BEFORE the press. Read after, a key that changes the
		// PHASE is judged against the bar of the frame it arrived on — Enter
		// opens the confirm, whose bar quite rightly does not name Enter — so
		// every correct key would be reported as unnamed.
		for _, k := range poKeySpace() {
			s := woReviewAt(mk())
			barNames := woReviewBarNames(t, s, k)
			if woReviewActs(t, s, k) {
				acted++
				if !barNames {
					t.Errorf("%s: %q changes the pane and the bar does not name it", name, k)
				}
			}
		}
		// FORWARD: every token the bar spells, pressing each key it names.
		for _, it := range woReviewBarOf(woReviewAt(mk())) {
			keys, ok := poBarKeyNames[it.Key]
			if !ok {
				t.Fatalf("%s: the bar spells %q, which poBarKeyNames does not know", name, it.Key)
			}
			named++
			moved := false
			for _, k := range keys {
				if rootOwned[k] {
					moved = true
					continue
				}
				if woReviewActs(t, woReviewAt(mk()), k) {
					moved = true
				}
			}
			if !moved {
				t.Errorf("%s: the bar names %q (%v) and no key it spells changes anything",
					name, it.Key, keys)
			}
		}
		// The vacuity guard: a state where the bar names nothing this space
		// reaches would pass the reverse half by never firing it.
		for _, k := range poKeySpace() {
			if !woReviewBarNames(t, woReviewAt(mk()), k) {
				unnamed++
			}
		}
	}
	if named == 0 || acted == 0 || unnamed == 0 {
		t.Fatalf("the sweep found %d named tokens, %d acting keys and %d unnamed keys; "+
			"a zero in any of them means one of the two implications was never fired",
			named, acted, unnamed)
	}
}

// woReviewAt sizes a fixture at the width the interface is modelled on.
func woReviewAt(s Screen) *WorkOrderScanReviewScreen {
	v := s.(*WorkOrderScanReviewScreen)
	v.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return v
}

// woReviewActs presses one key and reports whether the pane changed for a
// reason OTHER than the screen answering the press.
func woReviewActs(t *testing.T, s *WorkOrderScanReviewScreen, key string) bool {
	t.Helper()
	before, wasNote := stripANSI(s.View()), s.note
	next, _ := s.Update(poPickerKeyMsg(key))
	if v, ok := next.(*WorkOrderScanReviewScreen); ok {
		s = v
	}
	// Held at what it was, so the ANSWER to this press is not counted as the
	// press having acted.
	gotNote := s.note
	s.note = wasNote
	after := stripANSI(s.View())
	s.note = gotNote
	return after != before
}

func woReviewBarOf(s *WorkOrderScanReviewScreen) []actionBarItem {
	if s.phase == woReviewPhaseConfirm {
		return s.confirmBar()
	}
	return s.listBar()
}

// woReviewBarNames reports whether the bar the screen is ABOUT to draw spells
// this keystroke, read through the shared bar table so a token this file has
// never heard of fails rather than being skipped.
func woReviewBarNames(t *testing.T, s *WorkOrderScanReviewScreen, key string) bool {
	t.Helper()
	for _, it := range woReviewBarOf(s) {
		keys, ok := poBarKeyNames[it.Key]
		if !ok {
			t.Fatalf("the bar spells %q, which poBarKeyNames does not know — an unknown "+
				"token would silently credit the bar with naming nothing", it.Key)
		}
		for _, k := range keys {
			if k == key {
				return true
			}
		}
	}
	return false
}

// Esc is the one key this frame names that it does not bind, so it is PRESSED
// through a real Root rather than read off the switch.
func TestWOScanReview_EscReallyLeaves(t *testing.T) {
	detail := NewWorkOrderDetailScreen(Deps{}, woReviewFixtureWO)
	r := newTestRoot(detail)
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)
	// Reached the way an operator reaches it, so the back stack holds what it
	// really holds.
	r.screen = detail
	nextScreen, _ := r.Update(SwitchScreenMsg{
		Workspace: WSMaintenance,
		Screen:    NewWorkOrderScanReviewScreen(Deps{}, woReviewFixtureWO, woReviewWorkOrder()),
	})
	r = nextScreen.(Root)
	if _, ok := r.screen.(*WorkOrderScanReviewScreen); !ok {
		t.Fatalf("did not reach the review screen, got %T", r.screen)
	}

	after, _ := r.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, still := after.(Root).screen.(*WorkOrderScanReviewScreen); still {
		t.Error("Esc is on the bar and does not leave the review screen")
	}
}

// ---------------------------------------------------------------------------
// A reload lands under the operator
// ---------------------------------------------------------------------------

// A WRITE CHANGES THE QUEUE — that is what it is for — so a reload landing
// under the cursor is ordinary. Re-seating by POSITION would leave the cursor
// on whatever moved up into its index, and on this screen the next keypress
// applies a reading: the operator would confirm one and write another.
func TestWOScanReview_AReloadKeepsTheCursorOnItsOwnReading(t *testing.T) {
	s := woReviewFixture()
	s.setSize(tea.WindowSizeMsg{Width: 80, Height: 40})

	var want string
	for i := range s.rows {
		if seat, ok := s.seatOf(i); ok && seat.target == woReviewFixtureTask2 {
			s.cursor = i
			want = seat.target
			break
		}
	}
	if want == "" {
		t.Fatal("fixture has no second mark")
	}

	// The first reading is applied and drops out of the queue, exactly as the
	// server would leave it after a selective apply.
	reloaded := woReviewWorkOrder()
	sub := reloaded.Submissions[0]
	sub.PendingChanges = sub.PendingChanges[1:]
	reloaded.Submissions[0] = sub
	s.adopt(reloaded)

	seat, ok := s.seatOf(s.cursor)
	if !ok || seat.target != want {
		t.Errorf("after the reload the cursor sits on %+v, want the reading the operator "+
			"left it on (%s)", seat, want)
	}
}

// A mark whose reading is gone is DROPPED rather than carried into the next
// write, which would name a target the sheet no longer has.
func TestWOScanReview_AReloadDropsAMarkWhoseReadingIsGone(t *testing.T) {
	s := woReviewFixture()
	s.marked[woReviewMark{woReviewFixtureSub, woReviewFixtureTask1}] = true
	s.marked[woReviewMark{woReviewFixtureSub, woReviewFixtureTask2}] = true

	reloaded := woReviewWorkOrder()
	sub := reloaded.Submissions[0]
	sub.PendingChanges = sub.PendingChanges[1:]
	reloaded.Submissions[0] = sub
	s.adopt(reloaded)

	if s.marked[woReviewMark{woReviewFixtureSub, woReviewFixtureTask1}] {
		t.Error("a mark survived the reading it was on")
	}
	if !s.marked[woReviewMark{woReviewFixtureSub, woReviewFixtureTask2}] {
		t.Error("a mark on a reading that is still there was dropped")
	}
}
