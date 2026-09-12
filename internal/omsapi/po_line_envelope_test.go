package omsapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The line doors' refusals are moving from the hand-built
// `{"error": "<prose>", "code": "<code>"}` body to OMS's STANDARDIZED envelope,
// `{"error": {"code", "message", "details"}}` (backend config/api_errors.py,
// docs/API_ERROR_CONTRACT.md). Nothing is lost on the wire in that move —
// parseError understands the envelope, so the code and the sentence both arrive
// on a proper *APIError — but AsLineEntryError used to special-case the OLD
// shape and nothing else, so against the new one it answered FALSE and the two
// screens that read it misreported:
//
//   - the ADD screen fell through to its generic branch and told the operator
//     "the add did not answer — the line may or may not be on the order", which
//     is FALSE. The server answered, and refused: nothing was added. An outcome
//     reported as unknown when it is known is the one an operator cannot act
//     on, because they do not know whether to retry.
//   - the DELETE screen printed APIError.Error(), which prefixes a coded error
//     with `oms: <code>: ` — the machine code in front of the sentence, on the
//     row the operator reads to find out what to do.
//
// These tests drive the REAL client against the new shape. The old-shape ones
// live beside them in po_line_entry_test.go and stay green: a reader that
// accepts BOTH lands before, after or independently of the server change, with
// no window in which either screen misreports.

// The plain refusal: code and sentence off the envelope, and the raw body never
// reaches the operator.
func TestAsLineEntryError_TheStandardEnvelopeIsARefusalToo(t *testing.T) {
	const sentence = "Acme Fasteners no longer supplies Widget bracket (marked discontinued)."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": {"code": "discontinued", "message": `+
			`"`+sentence+`"}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{ItemSupplier: 7})
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the enveloped refusal was not recognised as one: %v — "+
			"the add screen then tells the operator the outcome is unknown when it is known", err)
	}
	if entry.Code != POLineErrDiscontinued {
		t.Errorf("code = %q, want %q", entry.Code, POLineErrDiscontinued)
	}
	if entry.Message != sentence {
		t.Errorf("message = %q, want the server's own sentence %q", entry.Message, sentence)
	}
	if entry.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", entry.Status)
	}
	// Error() is what the delete screen prints. The `oms: <code>: ` prefix
	// APIError.Error() adds is the machine code in front of the sentence.
	if got := entry.Error(); got != sentence {
		t.Errorf("Error() = %q, want the sentence alone", got)
	}
	if strings.Contains(entry.Error(), "oms:") || strings.Contains(entry.Error(), "{") {
		t.Errorf("Error() carries transport wrapping: %q", entry.Error())
	}
}

// The 409's choice set rides in error.details.candidates under the envelope
// (backend test_po_line_error_envelope.py::test_the_ambiguous_choice_set_rides_in_error_details).
// Losing it turns "pick one of these two" into a dead end.
func TestAsLineEntryError_TheEnvelopeCarriesItsCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error": {"code": "ambiguous",
			"message": "\"bolt\" matches 2 items Acme supplies. Choose which one to add.",
			"details": {"candidates": [
				{"item_supplier": 4, "item": {"id": "a1c5d7ad-4115-463d-bce2-47db6fd9eab5", "name": "Bolt A"}},
				{"item_supplier": 5, "item": {"id": "65b5df3c-d0de-4eb0-ad98-df48cf37d3fd", "name": "Bolt B"}}]}}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{Identifier: "bolt"})
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the enveloped 409 was not recognised as a refusal: %v", err)
	}
	if !entry.Ambiguous() {
		t.Errorf("code = %q, want ambiguous", entry.Code)
	}
	if len(entry.Candidates) != 2 {
		t.Fatalf("candidates = %d, want the two the server sent: %+v", len(entry.Candidates), entry.Candidates)
	}
	if entry.Candidates[0].ItemSupplier != 4 || entry.Candidates[1].Item.Name != "Bolt B" {
		t.Errorf("candidates = %+v, want the server's own choice set", entry.Candidates)
	}
}

// The DELETE door's refusals move with the add's, and it is the door where the
// stray prefix was visible: DeletePurchaseOrderLineItem hands its error
// straight to the screen, which prints Error().
func TestDeletePurchaseOrderLineItem_TheEnvelopeArrivesAsTheServersSentence(t *testing.T) {
	const sentence = "This line records 3 received, so it cannot be deleted. " +
		"Correct the received quantity to 0 first, then delete the line."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": {"code": "line_received", "message": "`+sentence+`"}}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).DeletePurchaseOrderLineItem(context.Background(), "2", "7")
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the enveloped delete refusal was not recognised as one: %v", err)
	}
	if entry.Code != "line_received" {
		t.Errorf("code = %q, want line_received", entry.Code)
	}
	if got := err.Error(); got != sentence {
		t.Errorf("Error() = %q — the operator reads this row, and `oms: <code>: ` is "+
			"the machine code in front of the sentence", got)
	}
}

// Voiding answers `{"error": "<prose>"}` with no code at all and is NOT
// converted, so the bare-shape recogniser beside this one must keep it. Held
// here so widening the coded reader cannot quietly take it over.
func TestAsLineEntryError_TheUncodedVoidRefusalIsStillTheBareShape(t *testing.T) {
	const sentence = "Cannot void line item that has already been received."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": "`+sentence+`"}`)
	}))
	defer srv.Close()

	_, err := New(srv.URL).VoidPurchaseOrderLineItem(context.Background(), "2", "7", "wrong part")
	if err == nil {
		t.Fatal("a 400 came back as success")
	}
	if got := err.Error(); got != sentence {
		t.Errorf("Error() = %q, want the server's own sentence", got)
	}
	entry, ok := AsLineEntryError(err)
	if !ok {
		t.Fatalf("the void refusal did not survive: %v", err)
	}
	if entry.Code != "" {
		t.Errorf("code = %q — this shape carries none, so inventing one would put a "+
			"fabricated code in front of the operator", entry.Code)
	}
}

// What must STILL be left alone, now that "carries a code" is what a refusal
// is. Each of these is a body the server did not compose as a line refusal at
// all: coercing one would put a sentence in the operator's hands as though the
// order had answered about their line.
func TestAsLineEntryError_StillLeavesTheUnansweredAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{"gateway html", "<!DOCTYPE html><html><body>502 Bad Gateway</body></html>", 502},
		{"drf field validation", `{"quantity": ["Enter a whole number."]}`, 400},
		{"bare detail", `{"detail": "Not found."}`, 404},
		{"envelope with no code", `{"error": {"message": "Something went wrong."}}`, 500},
		{"envelope with no message", `{"error": {"code": "server_error"}}`, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			_, err := New(srv.URL).AddPurchaseOrderLine(context.Background(), "2", POLineAdd{ItemSupplier: 1})
			if _, ok := AsLineEntryError(err); ok {
				t.Errorf("%s was coerced into a line-entry refusal: %v", tc.name, err)
			}
		})
	}
}
