package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The scan-review decode and write tests, built from RECORDED OpenMakerSuite
// responses (testdata/wo_*.json, provenance in testdata/README.md) rather than
// from maps written beside the assertions.
//
// A fixture written from the struct agrees with the struct by construction and
// cannot report a disagreement with the server — the lesson
// po_line_entry_wire_test.go records at length. These files were recorded off a
// live backend at the OMS commit named in the README, one request per branch
// the review surface has to tell apart: a queue with all four change kinds in
// it, a DEGRADED submission parked for a human with an empty queue and a
// sentence, and the four write replies.

// ---------------------------------------------------------------------------
// What the server really sends
// ---------------------------------------------------------------------------

// THE VISIBILITY HALF. Both serializers carry the two flags, and until they
// were decoded an operator could not tell a scanned work order from any other
// one. Read off the recorded LIST body, which is the surface that has to say so
// without the job being opened.
func TestWorkOrderList_CarriesThePendingReviewFlags(t *testing.T) {
	raw := wireBody(t, "wo_list_pending_review.json")

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	results, _ := envelope["results"].([]any)
	if len(results) == 0 {
		t.Fatal("recorded list has no rows — it would prove nothing about a row's flags")
	}
	first, _ := results[0].(map[string]any)
	if _, isBool := first["has_pending_review"].(bool); !isBool {
		t.Errorf("has_pending_review in the recorded body is %T (%v), want a JSON bool. "+
			"drf-spectacular types an un-annotated SerializerMethodField as `string`, "+
			"which is the artefact AGENTS.md warns about — the BUILDER "+
			"(_pending_review_count() > 0) is what decides, and it returns a bool.",
			first["has_pending_review"], first["has_pending_review"])
	}
	if _, isNumber := first["pending_review_count"].(float64); !isNumber {
		t.Errorf("pending_review_count is %T, want a JSON number", first["pending_review_count"])
	}

	page, err := serveWire(t, raw).ListWorkOrders(context.Background(), nil)
	if err != nil {
		t.Fatalf("a recorded OMS list reply did not decode: %v", err)
	}
	if len(page.Results) != len(results) {
		t.Fatalf("decoded %d rows, recorded body has %d", len(page.Results), len(results))
	}
	wo := page.Results[0]
	if !wo.HasPendingReview {
		t.Error("HasPendingReview decoded false off a row the server marked true")
	}
	if want := int(first["pending_review_count"].(float64)); wo.PendingReviewCount != want {
		t.Errorf("PendingReviewCount = %d, want %d", wo.PendingReviewCount, want)
	}
	// The list serializer carries no `title` key at all, so a row's only human
	// name is display_title. The list screen used to read Title and drew every
	// work order as a blank row.
	if wo.Title != "" {
		t.Errorf("Title = %q; the list serializer has no `title` field, so a row that "+
			"decodes one means the recording is not a list body", wo.Title)
	}
	if wo.DisplayTitle == "" {
		t.Error("DisplayTitle is empty — the one human name a list row has")
	}
}

// THE REVIEW HALF. Every change kind the two ingest paths produce, decoded off
// the detail body, with the fields a reviewer decides on.
func TestWorkOrderDetail_DecodesTheParkedSubmissions(t *testing.T) {
	raw := wireBody(t, "wo_detail_pending_review.json")

	wo, err := serveWire(t, raw).GetWorkOrder(context.Background(), "ignored")
	if err != nil {
		t.Fatalf("a recorded OMS detail reply did not decode: %v", err)
	}
	if !wo.HasPendingReview || wo.PendingReviewCount != 2 {
		t.Fatalf("flags = (%v, %d), want (true, 2) — the recording has two parked submissions",
			wo.HasPendingReview, wo.PendingReviewCount)
	}
	if len(wo.Submissions) != 2 {
		t.Fatalf("decoded %d submissions, want 2", len(wo.Submissions))
	}

	var queued, degraded *WorkOrderSubmission
	for i := range wo.Submissions {
		s := &wo.Submissions[i]
		if !s.AwaitsReview() {
			t.Errorf("submission %s decoded status %q, want pending_review", s.ID, s.Status)
		}
		if len(s.PendingChanges) > 0 {
			queued = s
		} else {
			degraded = s
		}
	}
	if queued == nil || degraded == nil {
		t.Fatal("the recording must carry BOTH a queued submission and a degraded one; " +
			"a fixture with only the first cannot show that an empty queue is still a review")
	}

	// THE DEGRADED READ. Parked for a human with nothing to apply and a reason
	// instead — so a surface that gates on len(pending_changes) shows an empty
	// pane where the server sent the one sentence that matters.
	if degraded.ParseError == "" {
		t.Error("the degraded submission decoded no parse_error; nothing on that frame would say why")
	}
	if !degraded.AwaitsReview() {
		t.Error("the degraded submission is not AwaitsReview, so it would not be counted or shown")
	}

	if queued.ID == "" || strings.Count(queued.ID, "-") != 4 {
		t.Errorf("submission id = %q, want the UUID string WorkOrderSubmission's explicit "+
			"UUIDField pk puts on the wire", queued.ID)
	}
	if queued.ReceivedAt.IsZero() {
		t.Error("received_at did not decode")
	}

	byKind := map[string]WorkOrderPendingChange{}
	for _, c := range queued.PendingChanges {
		byKind[c.Kind] = c
	}
	for _, kind := range []string{
		WorkOrderChangeCheckbox, WorkOrderChangeSignature, WorkOrderChangeHandwritten,
	} {
		if _, ok := byKind[kind]; !ok {
			t.Fatalf("the recording carries no %q change; the review surface's handling of it "+
				"would be untested rather than passing", kind)
		}
	}

	mark := byKind[WorkOrderChangeCheckbox]
	if !mark.IsMark() || !mark.Selectable() {
		t.Errorf("a checkbox change is not a selectable mark: %+v", mark)
	}
	if mark.Confidence <= 0 || mark.Confidence > 1 {
		t.Errorf("confidence = %v, want the 0..1 fraction the reader writes", mark.Confidence)
	}
	if !strings.HasPrefix(mark.TargetID, "task_") {
		t.Errorf("target_id = %q, want the `task_<uuid>` form omr_apply_mark routes on", mark.TargetID)
	}

	// A SIGNATURE CARRIES NO TARGET, WHICH IS THE WHOLE CONSTRAINT ON SELECTIVE
	// APPLY. The server matches target_ids against this field, so a change with
	// none can be reached only by a whole-queue write, and a surface offering
	// per-change selection has to say so on the row.
	sig := byKind[WorkOrderChangeSignature]
	if sig.TargetID != "" || sig.Selectable() {
		t.Errorf("signature change decoded target_id %q; the recorded body sends null, "+
			"and a fixture that gives it one hides the one case selection cannot express",
			sig.TargetID)
	}

	// A HANDWRITTEN NOTE IS JUDGED ON ITS WORDS, so the OCR'd string has to
	// survive the decode of an `any`.
	note := byKind[WorkOrderChangeHandwritten]
	if note.Text() == "" {
		t.Errorf("handwritten value = %#v, want the OCR'd text a reviewer reads", note.Value)
	}
	if note.IsMark() || note.Selectable() {
		t.Error("a handwritten note is neither a mark nor selectable")
	}
}

// The recorded body really does carry the types above, so the decode test is
// not passing because somebody edited the fixture into the shape the structs
// assume. Pinned per fact, with the reason each one is what it is.
func TestWorkOrderPendingFixture_CarriesTheServersOwnTypes(t *testing.T) {
	var raw map[string]any
	if err := json.Unmarshal(wireBody(t, "wo_detail_pending_review.json"), &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if _, isBool := raw["has_pending_review"].(bool); !isBool {
		t.Errorf("has_pending_review is %T, want a JSON bool", raw["has_pending_review"])
	}
	subs, _ := raw["submissions"].([]any)
	if len(subs) < 2 {
		t.Fatalf("recorded body has %d submissions, want the queued one and the degraded one", len(subs))
	}

	sawNullTarget, sawBoolValue, sawStringValue := false, false, false
	for _, s := range subs {
		sub, _ := s.(map[string]any)
		if _, isString := sub["id"].(string); !isString {
			t.Errorf("submission id is %T, want a JSON string (explicit UUIDField pk)", sub["id"])
		}
		changes, _ := sub["pending_changes"].([]any)
		for _, c := range changes {
			ch, _ := c.(map[string]any)
			if _, isNumber := ch["confidence"].(float64); !isNumber {
				t.Errorf("confidence is %T, want a JSON number", ch["confidence"])
			}
			switch v := ch["target_id"].(type) {
			case nil:
				sawNullTarget = true
			case string:
			default:
				t.Errorf("target_id is %T, want a JSON string or null", v)
			}
			switch ch["value"].(type) {
			case bool:
				sawBoolValue = true
			case string:
				sawStringValue = true
			}
		}
	}
	if !sawNullTarget {
		t.Error("no change in the recording carries a null target_id — the one shape " +
			"selective apply cannot name would be untested")
	}
	if !sawBoolValue || !sawStringValue {
		t.Error("the recording must carry both a bool `value` (a mark) and a string one " +
			"(a handwritten note); `value` is typed `object` server-side and one shape " +
			"proves nothing about the other")
	}
}

// ---------------------------------------------------------------------------
// The two writes
// ---------------------------------------------------------------------------

// captureWrite serves a recorded reply and hands back what the client SENT, so
// every assertion below is about the real request rather than about a stub's
// idea of one.
func captureWrite(t *testing.T, replyFixture string) (*Client, *[]byte, *string) {
	t.Helper()
	var body []byte
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(wireBody(t, replyFixture))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL), &body, &path
}

func TestApplyWorkOrderPendingChanges_SelectiveNamesOnlyTheChosenChanges(t *testing.T) {
	c, sent, path := captureWrite(t, "wo_apply_pending_selective.json")

	res, err := c.ApplyWorkOrderPendingChanges(context.Background(),
		"wo-1", "sub-1", []string{"task_a", "task_b"}, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if want := "/api/inventory/work-orders/wo-1/submissions/sub-1/apply-pending/"; *path != want {
		t.Errorf("posted to %q, want %q", *path, want)
	}

	var got map[string]any
	if err := json.Unmarshal(*sent, &got); err != nil {
		t.Fatalf("request body is not JSON: %q", *sent)
	}
	ids, _ := got["target_ids"].([]any)
	if len(ids) != 2 || ids[0] != "task_a" || ids[1] != "task_b" {
		t.Errorf("target_ids = %#v, want the two named changes", got["target_ids"])
	}
	if _, named := got["confirm_complete"]; named {
		t.Error("an ordinary apply must not send confirm_complete at all — it is the human " +
			"gate that closes the work order, and a key that could be read as asking for " +
			"one has no business on a write nobody asked to close anything with")
	}

	// The reply is the server's, and the count is not re-derived from what was
	// asked for: a target_ids list naming nothing that matches answers
	// "Applied 0" with a 200.
	if res.AppliedCount != 1 || res.SubmissionStatus != WorkOrderSubmissionPendingReview {
		t.Errorf("result = %+v, want the recorded 1 applied with the submission still parked", res)
	}
	if res.WorkOrderCompleted {
		t.Error("WorkOrderCompleted decoded true off a recording that says false")
	}
}

func TestApplyWorkOrderPendingChanges_TheWholeQueueOmitsTargetIDs(t *testing.T) {
	c, sent, _ := captureWrite(t, "wo_apply_pending_confirm_complete.json")

	if _, err := c.ApplyWorkOrderPendingChanges(context.Background(), "wo-1", "sub-1", nil, true); err != nil {
		t.Fatalf("apply: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(*sent, &got); err != nil {
		t.Fatalf("request body is not JSON: %q", *sent)
	}
	if _, named := got["target_ids"]; named {
		t.Errorf("a whole-queue apply sent target_ids = %#v. The key must be ABSENT: the "+
			"server applies everything only when it cannot see one, and every other "+
			"spelling narrows the write", got["target_ids"])
	}
	if got["confirm_complete"] != true {
		t.Errorf("confirm_complete = %#v, want true", got["confirm_complete"])
	}
}

// The completion is the SERVER's answer and is reported rather than predicted:
// omr_confirm_completion refuses silently unless every required task is done,
// and answers 200 either way. Both recordings are real.
func TestApplyWorkOrderPendingChanges_ReportsTheServersCompletion(t *testing.T) {
	for _, tc := range []struct {
		fixture   string
		completed bool
		status    string
	}{
		{"wo_apply_pending_confirm_complete.json", true, "completed"},
		{"wo_apply_pending_selective.json", false, "in_progress"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			c, _, _ := captureWrite(t, tc.fixture)
			res, err := c.ApplyWorkOrderPendingChanges(context.Background(), "wo-1", "sub-1", nil, true)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if res.WorkOrderCompleted != tc.completed || res.WorkOrderStatus != tc.status {
				t.Errorf("(completed, status) = (%v, %q), want (%v, %q)",
					res.WorkOrderCompleted, res.WorkOrderStatus, tc.completed, tc.status)
			}
		})
	}
}

// The nothing-to-do reply carries NO counts at all, so a caller that inferred
// failure from a zero AppliedCount would report a refusal the server did not
// make. Detail is what says what happened.
func TestApplyWorkOrderPendingChanges_AnEmptyQueueAnswersInProse(t *testing.T) {
	c, _, _ := captureWrite(t, "wo_apply_pending_empty_queue.json")
	res, err := c.ApplyWorkOrderPendingChanges(context.Background(), "wo-1", "sub-1", nil, false)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Detail == "" {
		t.Fatal("Detail is empty; it is the only field this reply populates")
	}
	if res.AppliedCount != 0 || res.SubmissionStatus != WorkOrderSubmissionApplied {
		t.Errorf("result = %+v", res)
	}
}

func TestDiscardWorkOrderPendingChanges_SendsTheSelectionAndReadsTheCount(t *testing.T) {
	c, sent, path := captureWrite(t, "wo_discard_pending_all.json")

	res, err := c.DiscardWorkOrderPendingChanges(context.Background(), "wo-1", "sub-1", nil)
	if err != nil {
		t.Fatalf("discard: %v", err)
	}
	if want := "/api/inventory/work-orders/wo-1/submissions/sub-1/discard-pending/"; *path != want {
		t.Errorf("posted to %q, want %q", *path, want)
	}
	if strings.Contains(string(*sent), "target_ids") {
		t.Errorf("a whole-queue discard sent target_ids: %q", *sent)
	}
	if res.DroppedCount != 3 {
		t.Errorf("DroppedCount = %d, want the recorded 3", res.DroppedCount)
	}
}

// THE EMPTY-SELECTION TRAP, held on both endpoints. `target_ids: []` is
// "nothing" to the server and reads as "everything" to a person, on writes that
// mark tasks complete and move stock — so the client refuses rather than
// letting a nil check at a call site decide which.
func TestPendingChangeWrites_RefuseASelectionThatNamesNothing(t *testing.T) {
	reached := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(srv.URL)

	if _, err := c.ApplyWorkOrderPendingChanges(
		context.Background(), "wo-1", "sub-1", []string{}, false,
	); !errors.Is(err, ErrEmptyTargetIDs) {
		t.Errorf("apply with an empty selection: err = %v, want ErrEmptyTargetIDs", err)
	}
	if _, err := c.DiscardWorkOrderPendingChanges(
		context.Background(), "wo-1", "sub-1", []string{},
	); !errors.Is(err, ErrEmptyTargetIDs) {
		t.Errorf("discard with an empty selection: err = %v, want ErrEmptyTargetIDs", err)
	}
	if reached {
		t.Error("the refused write still reached the server")
	}
}

// ---------------------------------------------------------------------------
// The refusal an operator reads
// ---------------------------------------------------------------------------

// A missing submission answers DRF's bare {"detail": ...}, which carries no
// `code`, so parseError hands the whole raw body over and the operator would
// read JSON. Driven through a real 404 rather than a synthesised APIError.
func TestAsSubmissionRefusal_RecoversTheSentenceFromARealRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(wireBody(t, "wo_apply_pending_not_found.json"))
	}))
	defer srv.Close()

	_, err := New(srv.URL).ApplyWorkOrderPendingChanges(
		context.Background(), "wo-1", "missing", nil, false)
	if err == nil {
		t.Fatal("a 404 did not surface as an error")
	}
	prose, ok := AsSubmissionRefusal(err)
	if !ok {
		t.Fatalf("AsSubmissionRefusal declined a real refusal; the operator would read %q", err)
	}
	if !strings.Contains(prose, "Submission not found") {
		t.Errorf("recovered %q, want the server's own sentence", prose)
	}
	if strings.Contains(prose, "{") {
		t.Errorf("recovered %q — that is still JSON", prose)
	}
}

// And it is NARROW: a shape it does not recognise keeps whatever it arrived as,
// so a gateway page and a field-validation body are not paraphrased into
// something they are not.
func TestAsSubmissionRefusal_DeclinesEverythingElse(t *testing.T) {
	for name, body := range map[string]string{
		"a gateway page":       "<!DOCTYPE html><html><body>502 Bad Gateway</body></html>",
		"a field envelope":     `{"quantity": ["This field is required."]}`,
		"a list-valued detail": `{"detail": ["one", "two"]}`,
		"a blank detail":       `{"detail": "   "}`,
		"the {error} shape":    `{"error": "not this one"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if prose, ok := AsSubmissionRefusal(&APIError{Status: 400, Message: body}); ok {
				t.Errorf("recognised %q out of %s", prose, name)
			}
		})
	}
	if _, ok := AsSubmissionRefusal(errors.New("plain error")); ok {
		t.Error("recognised a non-APIError")
	}
}
