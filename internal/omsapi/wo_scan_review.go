package omsapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// A SCANNED WORK ORDER IS PARKED BEHIND A HUMAN, AND THIS IS THAT GATE'S WIRE.
//
// `POST work-orders/upload-pdf/` (UploadWorkOrderPdf, workorders.go) does NOT
// close a work order. The backend reads the marks off the sheet and parks every
// one it is not certain of on a WorkOrderSubmission as `pending_changes`, with
// the submission left `pending_review`; `inventory/services/work_order_ingest`
// says so in as many words ("The work order is NEVER auto-advanced to COMPLETED
// here — that transition is gated behind an explicit human confirm"). The gate
// is deliberate and this file does not weaken it: it gives the terminal the
// same two answers the web review surface has, apply and discard, plus the same
// explicit completion confirm.
//
// THE THREE FACTS A CLIENT NEEDS, and where each is decided:
//
//   - `has_pending_review` / `pending_review_count` ride BOTH the work-order
//     LIST and DETAIL serializers (`inventory/serializers.py`,
//     `_pending_review_count`), so an operator can be told a scan is waiting
//     without opening the job. They count SUBMISSIONS whose status is
//     `pending_review`, NOT changes: a submission the reader could not align at
//     all is parked with an empty queue and a `parse_error`, and it counts.
//   - `submissions[]` carries each parked submission and its queue.
//   - the two writes below.
//
// WIRE TYPES ARE THE BUILDER'S DECISION (AGENTS.md), so every one here was READ
// off a live OpenMakerSuite rather than reasoned out; the recordings are
// `testdata/wo_detail_pending_review.json` and its siblings, with provenance in
// `testdata/README.md`.

// Submission statuses (`WorkOrderSubmission.Status`). PENDING_REVIEW is the one
// that matters to a client: it is what `has_pending_review` counts and the only
// state the two writes below are offered on.
const (
	WorkOrderSubmissionReceived      = "received"
	WorkOrderSubmissionApplied       = "applied"
	WorkOrderSubmissionFailed        = "failed"
	WorkOrderSubmissionPendingReview = "pending_review"
)

// Change kinds (`work_order_cv.Detection.kind` and the OMR reader's).
// Checkbox/ink are MARKS against a task or material; signature and handwritten
// are free-form readings that go into the work order's notes.
const (
	WorkOrderChangeCheckbox    = "checkbox"
	WorkOrderChangeInk         = "ink"
	WorkOrderChangeSignature   = "signature"
	WorkOrderChangeHandwritten = "handwritten"
)

// WorkOrderPendingChange is one reading the scanner took that the server would
// not act on by itself.
//
// TargetID names what the reading is ABOUT — `task_<uuid>`, `material_<uuid>`,
// `loto_<uuid>`, or one of the fixed marks (`work_complete`, `result_pass`, …).
// It is EMPTY on a signature or a handwritten note, which are readings about
// the sheet rather than about a row, and that emptiness is load-bearing: the
// server matches `target_ids` against this value, so a change with none can
// only ever be reached by a WHOLE-QUEUE apply or discard. Selectable() is the
// one predicate for that and every caller reads it rather than testing the
// field twice.
//
// Value is `any` because the server's is: a bool for a mark (what the reader
// saw in the box), a string for a handwritten note (the OCR'd text — which is
// the whole of what a reviewer judges it on). A number is impossible on today's
// builders and would arrive as a json.Number rather than a float64 anyway,
// because responses decode through jsonDecoder (client.go).
//
// AutoApplied is the mark the reader was confident enough to PRE-CHECK on the
// work order already. It still sits in the queue, and discarding it UNDOES the
// pre-check — so on such a row "discard" is not a no-op, it is a correction.
type WorkOrderPendingChange struct {
	Kind        string  `json:"kind"`
	TargetID    string  `json:"target_id"`
	Value       any     `json:"value"`
	Confidence  float64 `json:"confidence"`
	Label       string  `json:"label"`
	CropURL     string  `json:"crop_url,omitempty"`
	AutoApplied bool    `json:"auto_applied,omitempty"`
}

// Selectable reports whether this change can be named in a `target_ids` list.
//
// The server's filter is `change.get("target_id") in target_ids`, so a change
// carrying none can never match one — and there is no way to spell it. A client
// that offers per-change selection must therefore say so on the row rather than
// silently dropping it from a selective write, which would apply neither the
// change nor an explanation.
func (c WorkOrderPendingChange) Selectable() bool { return c.TargetID != "" }

// IsMark reports a reading about a task/material box rather than about the
// sheet. Only a mark moves work-order state on apply; a signature or a
// handwritten note is appended to the work order's NOTES.
func (c WorkOrderPendingChange) IsMark() bool {
	return c.Kind == WorkOrderChangeCheckbox || c.Kind == WorkOrderChangeInk
}

// MarkedInScan reports what the READER saw in the box, and is meaningful only
// on a mark.
//
// It is deliberately NOT what apply does. `apply_pending_changes` calls
// `omr_apply_mark(..., marked=True)` for every selected mark whatever this
// says — "accepting a scanned mark = confirm the task/material is done" — so a
// row reading `blank` that is applied ends up COMPLETE. Measured on a live
// backend: applying the `value: false` row left its task `is_completed: true`.
// A review surface has to show both facts or the operator is deciding on the
// wrong one.
func (c WorkOrderPendingChange) MarkedInScan() bool {
	b, ok := c.Value.(bool)
	return ok && b
}

// Text is the handwritten reading, empty on anything else. A note is judged on
// its words, so this is the one Value a reviewer reads directly.
func (c WorkOrderPendingChange) Text() string {
	s, ok := c.Value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// WorkOrderSubmission is one inbound sheet — emailed or uploaded from this
// terminal — and the queue of readings parked on it.
//
// ParseError is a RECOVERABLE problem the reader hit (`_omr_review`: no OMR
// template on file, the checklist changed since the sheet printed, the scan
// would not align). Such a submission is parked `pending_review` with an EMPTY
// queue, so "nothing to apply" and "nothing to say" are different states and a
// review surface must draw the sentence rather than an empty list.
//
// ID is a string: `WorkOrderSubmission.id` is an explicit UUIDField, so it is a
// string on the wire and there is no number for an `any` to mangle. SubmittedBy
// is the uploader's integer user id, null for an emailed submission.
type WorkOrderSubmission struct {
	ID              string                   `json:"id"`
	PDFURL          string                   `json:"pdf_url,omitempty"`
	ReceivedAt      time.Time                `json:"received_at,omitempty"`
	Status          string                   `json:"status"`
	Source          string                   `json:"source,omitempty"`
	FromEmail       string                   `json:"from_email,omitempty"`
	Subject         string                   `json:"subject,omitempty"`
	SubmittedBy     *int                     `json:"submitted_by,omitempty"`
	SubmittedByName string                   `json:"submitted_by_name,omitempty"`
	ParseError      string                   `json:"parse_error,omitempty"`
	PendingChanges  []WorkOrderPendingChange `json:"pending_changes,omitempty"`
}

// AwaitsReview reports the state `has_pending_review` counts. Read the STATUS,
// never `len(PendingChanges)`: the degraded read above is parked for a human
// with an empty queue, and treating it as settled hides the one sentence that
// says why the sheet could not be read.
func (s WorkOrderSubmission) AwaitsReview() bool {
	return s.Status == WorkOrderSubmissionPendingReview
}

// WorkOrderApplyResult is what `apply-pending/` answers.
//
// WorkOrderCompleted and WorkOrderStatus are the SERVER's, and nothing on this
// side predicts them: `omr_confirm_completion` closes the job only when every
// REQUIRED task is complete and refuses silently otherwise, returning
// `work_order_completed: false` with a 200. Measured both ways on a live
// backend. Report what came back.
//
// Detail is the server's own sentence and is the only field populated on the
// nothing-to-do reply (`{"detail": "No pending changes to apply.",
// "submission_status": "applied"}` — no counts at all), so a caller must read
// Detail rather than inferring failure from a zero AppliedCount.
type WorkOrderApplyResult struct {
	Detail             string `json:"detail"`
	SubmissionStatus   string `json:"submission_status"`
	AppliedCount       int    `json:"applied_count"`
	WorkOrderCompleted bool   `json:"work_order_completed"`
	WorkOrderStatus    string `json:"work_order_status"`
}

// WorkOrderDiscardResult is what `discard-pending/` answers. A separate type
// from the apply result on purpose: the two payloads share no count key, and
// one struct covering both would hand every caller a field the endpoint it
// called never sends.
type WorkOrderDiscardResult struct {
	Detail           string `json:"detail"`
	SubmissionStatus string `json:"submission_status"`
	DroppedCount     int    `json:"dropped_count"`
}

// ErrEmptyTargetIDs refuses a selective write that names nothing.
//
// THE SERVER READS AN EMPTY LIST AS "NOTHING", AND A READER READS IT AS "ALL".
// `target_ids` absent means the whole queue; `target_ids: []` builds `set()`,
// which no change is in, so the write applies or drops NOTHING and still
// answers 200 (measured: `{"detail": "Applied 0 pending change(s)."}`). A
// caller that let an empty selection through would therefore get "everything"
// or "nothing" depending on whether its slice happened to be nil, on a write
// that marks tasks complete and moves stock. That is not a distinction to leave
// to a nil check at a call site, so the client refuses it here and the two
// scopes are spelled apart: nil for the whole queue, a non-empty slice for
// those changes.
var ErrEmptyTargetIDs = errors.New(
	"omsapi: target_ids names no change; pass nil for the whole queue")

// ApplyWorkOrderPendingChanges accepts parked readings on a submission
// (`POST work-orders/{woID}/submissions/{submissionID}/apply-pending/`).
//
// targetIDs nil applies the WHOLE queue — including signature and handwritten
// readings, which carry no target and can be reached no other way. A non-empty
// slice applies only the named changes and leaves the rest queued, which is
// what keeps a wrongly-read mark from having to be accepted alongside a right
// one.
//
// confirmComplete is the HUMAN GATE, and it is the only route by which a scan
// may close a work order. It is sent only when true, so an ordinary apply
// carries no key that could be read as asking for one.
//
// APPLYING A MARK RECORDS IT AS DONE WHATEVER THE READER SAW — see
// WorkOrderPendingChange.MarkedInScan — and applying a material mark moves
// stock through `apply_material_usage`. Both are why this is a confirmed action
// on the screens that drive it.
func (c *Client) ApplyWorkOrderPendingChanges(
	ctx context.Context, woID, submissionID string, targetIDs []string, confirmComplete bool,
) (*WorkOrderApplyResult, error) {
	body, err := pendingChangeBody(targetIDs)
	if err != nil {
		return nil, err
	}
	if confirmComplete {
		body["confirm_complete"] = true
	}
	var out WorkOrderApplyResult
	if err := c.Post(ctx, woSubmissionPath(woID, submissionID, "apply-pending"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DiscardWorkOrderPendingChanges rejects parked readings
// (`POST .../discard-pending/`), with the same two scopes ApplyWorkOrderPendingChanges
// has and the same refusal of an empty selection.
//
// It is not merely "forget these": a reading the server had already PRE-CHECKED
// on the work order (`auto_applied`) is UNDONE here, so discarding is how a
// wrong pre-check is taken back off the job. There is no way to put a discarded
// reading back — the sheet would have to be scanned again.
func (c *Client) DiscardWorkOrderPendingChanges(
	ctx context.Context, woID, submissionID string, targetIDs []string,
) (*WorkOrderDiscardResult, error) {
	body, err := pendingChangeBody(targetIDs)
	if err != nil {
		return nil, err
	}
	var out WorkOrderDiscardResult
	if err := c.Post(ctx, woSubmissionPath(woID, submissionID, "discard-pending"), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// pendingChangeBody is the one place the two scopes become a payload, so the
// empty-slice refusal cannot be forgotten on one of the two endpoints.
func pendingChangeBody(targetIDs []string) (map[string]any, error) {
	body := map[string]any{}
	if targetIDs == nil {
		return body, nil
	}
	if len(targetIDs) == 0 {
		return nil, ErrEmptyTargetIDs
	}
	body["target_ids"] = targetIDs
	return body, nil
}

func woSubmissionPath(woID, submissionID, action string) string {
	return fmt.Sprintf("/api/inventory/work-orders/%s/submissions/%s/%s/",
		woID, submissionID, action)
}

// AsSubmissionRefusal recovers the sentence out of these endpoints' refusals.
//
// Both actions answer a missing submission with DRF's own
// `{"detail": "Submission not found for this work order."}`, which carries no
// `code` — so parseError's `{"error": {...}}` envelope does not match and it
// puts the ENTIRE RAW BODY into APIError.Message. Without this the operator
// reads JSON on the status row of a screen about to change a work order.
//
// It is narrow in the way its two siblings are (AsLineEntryError,
// AsReceivingRefusal): an object whose `detail` is a NON-BLANK JSON STRING and
// nothing else, so a gateway page, a field-validation body and DRF's
// list-valued `detail` all keep the shape they arrived in and reach the
// operator as the APIError they are.
func AsSubmissionRefusal(err error) (string, bool) {
	var api *APIError
	if !errors.As(err, &api) {
		return "", false
	}
	body := strings.TrimSpace(api.Message)
	if !strings.HasPrefix(body, "{") {
		return "", false
	}
	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal([]byte(body), &envelope) != nil || len(envelope.Detail) == 0 {
		return "", false
	}
	var prose string
	if json.Unmarshal(envelope.Detail, &prose) != nil {
		return "", false
	}
	if strings.TrimSpace(prose) == "" {
		return "", false
	}
	return prose, true
}
