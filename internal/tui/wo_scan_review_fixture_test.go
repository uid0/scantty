package tui

import (
	"time"

	"github.com/uid0/scantty/internal/omsapi"
)

// The scan-review fixtures, in ONE place because five sweeps and a drive suite
// build the same screen and a second copy is a second thing to keep in step.
//
// THE FIXTURE IS HALF THE CHECK, and this file is built from what the SERVER
// really sends rather than from short strings that happen to fit. AGENTS.md
// records the class twice — every picker fixture said `Widget 1`, every report
// fixture said `Acme`, so no test in the package had ever rendered one of those
// rows at the length OMS carries and a whole family of clipping defects
// survived. The names here are the length a real PM checklist step runs to, and
// the queue carries every change KIND the two ingest paths produce, because a
// queue of one checkbox proves nothing about the row that has no target id.
//
// The shape is the recorded one: internal/omsapi/testdata/wo_detail_pending_review.json,
// whose provenance is in that directory's README.

const (
	woReviewFixtureWO  = "4cb33174-6c42-40b2-b1f3-7507e3ecbcea"
	woReviewFixtureSub = "5795e8d8-4a19-4a22-84d0-0a99e1836d66"
	// The DEGRADED sheet: parked for a human with an empty queue and a sentence
	// instead. It is a different state from "no sheet", and a screen that gates
	// on len(pending_changes) draws an empty pane where the server sent the one
	// thing the operator needs.
	woReviewFixtureDegraded = "4938d40c-8b49-48b0-83ef-fea3a97ca652"
	// A task id long enough to be a real one: the row draws it whole on its own
	// line rather than abbreviating it, which is only testable against a value
	// that would not fit beside a label.
	woReviewFixtureTask1 = "task_cce8e36b-fb78-4eff-af5f-489a947f7623"
	woReviewFixtureTask2 = "task_747744ff-4921-4d17-9b68-08768dfbdb7c"
)

func woReviewReceivedAt() time.Time {
	return time.Date(2026, 9, 12, 9, 14, 0, 0, time.UTC)
}

// woReviewQueuedSubmission is the ordinary parked sheet: two marks (one the
// reader pre-checked, one it was unsure of and read as BLANK), a signature and
// a handwritten note — the last two carrying no target id at all, which is the
// one shape selective apply cannot name.
func woReviewQueuedSubmission() omsapi.WorkOrderSubmission {
	return omsapi.WorkOrderSubmission{
		ID:         woReviewFixtureSub,
		Status:     omsapi.WorkOrderSubmissionPendingReview,
		Source:     "scan",
		Subject:    "Scanned form",
		ReceivedAt: woReviewReceivedAt(),
		PendingChanges: []omsapi.WorkOrderPendingChange{
			{
				Kind: omsapi.WorkOrderChangeCheckbox, TargetID: woReviewFixtureTask1,
				Value: true, Confidence: 0.9993,
				Label:       "Grease the ways and check the one-shot lube reservoir",
				AutoApplied: true,
			},
			{
				Kind: omsapi.WorkOrderChangeCheckbox, TargetID: woReviewFixtureTask2,
				Value: false, Confidence: 0.61,
				Label: "Check backlash on the X axis against the service limit",
			},
			{
				Kind: omsapi.WorkOrderChangeSignature, Value: true,
				Confidence: 0.55, Label: "Signature block",
			},
			{
				Kind: omsapi.WorkOrderChangeHandwritten, Value: "Replaced V-belt",
				Confidence: 0.6, Label: "Handwritten note",
			},
		},
	}
}

// woReviewDegradedSubmission is the read the server could not align. It counts
// towards pending_review_count and has nothing to apply.
func woReviewDegradedSubmission() omsapi.WorkOrderSubmission {
	return omsapi.WorkOrderSubmission{
		ID:         woReviewFixtureDegraded,
		Status:     omsapi.WorkOrderSubmissionPendingReview,
		Source:     "scan",
		ReceivedAt: woReviewReceivedAt().Add(-time.Hour),
		ParseError: "The work order's tasks changed after this form was printed. " +
			"Reprint the OMR form and scan again.",
	}
}

// woReviewWorkOrder is the whole payload the review screen is seeded from — a
// real OMS display title, both parked sheets, and the two flags that say so.
func woReviewWorkOrder() *omsapi.WorkOrder {
	return &omsapi.WorkOrder{
		ID:           woReviewFixtureWO,
		ShortID:      "WO-4CB33174",
		DisplayTitle: "Quarterly lube and way-cover check",
		Status:       "in_progress",
		AssetName:    "Haas VF-2 vertical machining centre",
		AssetTag:     "VMC-0041",
		Submissions: []omsapi.WorkOrderSubmission{
			woReviewQueuedSubmission(), woReviewDegradedSubmission(),
		},
		PendingReviewCount: 2,
		HasPendingReview:   true,
	}
}

// woReviewFixture is the screen as an operator reaches it: past its load, on
// the list, with the cursor where it opens.
func woReviewFixture() *WorkOrderScanReviewScreen {
	s := NewWorkOrderScanReviewScreen(Deps{}, woReviewFixtureWO, woReviewWorkOrder())
	s.loading = false
	return s
}

// woReviewConfirmFixture is the screen with a confirm OPEN, on a SELECTION —
// the state the whole feature turns on, and the one a fixture built freshly
// opened on a whole-queue write would not reach. A destructive confirm's
// mid-write half is its own case (jde_confirm_midwrite_test.go).
func woReviewConfirmFixture(action woReviewAction) *WorkOrderScanReviewScreen {
	s := woReviewFixture()
	s.marked[woReviewMark{woReviewFixtureSub, woReviewFixtureTask2}] = true
	// The cursor stands on the marked reading, which is where an operator
	// pressing the key really is.
	for i := range s.rows {
		if seat, ok := s.seatOf(i); ok && seat.target == woReviewFixtureTask2 {
			s.cursor = i
			break
		}
	}
	s.openConfirm(action)
	return s
}
