package tui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The two screens that read a line refusal, driven against OMS's STANDARDIZED
// error envelope — `{"error": {"code", "message"}}` — which is what the add and
// delete doors answer with once backend config/api_errors.py owns them.
//
// Both screens' symptoms were the SAME mis-read of a value that arrived intact:
// parseError understands the envelope, so the code and the sentence were both
// on the *APIError already. What was wrong was omsapi.AsLineEntryError
// declining to call it a refusal, and the two screens' fallbacks.
//
// The old-shape renderings are pinned beside these in po_add_line_test.go and
// po_line_remove_test.go and stay green: both shapes must read correctly at
// once, or there is a window in which one of these screens misreports.

// The add screen's generic branch says "the add did not answer — the line may
// or may not be on the order". That branch is RIGHT when the server genuinely
// did not answer, and it stays. What is false is reaching it here: the server
// answered, and refused, so the line is definitively NOT on the order and the
// operator can act — an outcome reported as unknown when it is known is the one
// they cannot act on, because they do not know whether to retry.
func TestPOAddLine_AnEnvelopedRefusalIsAnAnswerAndNotAnUnknown(t *testing.T) {
	const sentence = "Acme Fasteners no longer supplies Widget bracket (marked discontinued)."
	fake := &poAddFake{rows: poAddRows(),
		refuse: fmt.Sprintf(`{"error": {"code": "discontinued", "message": %q}}`, sentence)}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-77"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != poAddPhasePrice {
		t.Fatalf("a refusal left the flow on phase %v, want the price prompt it came from", s.phase)
	}
	// The refusal's own head, and NOT the unknown-outcome one.
	poAddWantPane(t, r, "refused this line")
	for _, unwanted := range []string{"did not answer", "may or may not"} {
		poAddRejectPane(t, r, unwanted)
	}
	// The server's own sentence, in the server's own words. It is longer than
	// the 49 cells an 80-column status row gives, so what is asserted is its
	// HEAD plus the mark that says the rest was cut — a sentence cut clean
	// reads as a finished one.
	poAddWantPane(t, r, "Acme Fasteners no longer")
	if !strings.Contains(poAddPane(r), "…") {
		t.Errorf("the sentence was cut with no mark, so a fragment reads as complete:\n%s", poAddPane(r))
	}
	// And never the transport wrapping or the raw body.
	for _, unwanted := range []string{`"code"`, "oms: discontinued", "oms: http 400"} {
		poAddRejectPane(t, r, unwanted)
	}
}

// The 409's choice set rides in error.details.candidates under the envelope, so
// an ambiguous identifier is still a CHOICE and not a dead end.
func TestPOAddLine_AnEnvelopedAmbiguityStillCarriesItsChoiceSet(t *testing.T) {
	fake := &poAddFake{rows: poAddRows(), refuseStatus: http.StatusConflict,
		refuse: `{"error": {"code": "ambiguous",
			"message": "\"AF-77\" matches 2 items Acme supplies. Choose which one to add.",
			"details": {"candidates": [
				{"item_supplier": 4, "item": {"id": "a1", "name": "Bolt A"}},
				{"item_supplier": 5, "item": {"id": "a2", "name": "Bolt B"}}]}}}`}
	r, s := poAddAt(t, fake, 80, 24)
	r = key(t, r, poRuneKey("AF-77"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != poAddPhasePrice {
		t.Fatalf("phase = %v, want the price prompt the refusal came from", s.phase)
	}
	poAddWantPane(t, r, "refused this line")
	poAddWantPane(t, r, "matches 2 items")
	for _, unwanted := range []string{"did not answer", "may or may not"} {
		poAddRejectPane(t, r, unwanted)
	}
}

// The delete screen prints the error's own Error(), so a coded *APIError
// reached the operator as `oms: line_received: This line records…` — the
// machine code standing in front of the sentence on the one row they read to
// find out what to do.
func TestPOLineRemove_AnEnvelopedRefusalCarriesNoTransportPrefix(t *testing.T) {
	const reason = "This line records 4 received, so it cannot be deleted. " +
		"Correct the received quantity to 0 first, then delete the line."
	fake := poRemoveOrder("draft", boolPtr(true))
	fake.refuseStatus = http.StatusBadRequest
	fake.refuse = fmt.Sprintf(`{"error": {"code": "line_received", "message": %q}}`, reason)

	r, _ := poRemoveRoot(t, fake, 80)
	r, s := poRemoveOnStatusRow(t, r, 0)
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlE})
	r = key(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	if s.errMsg != reason {
		t.Fatalf("the screen recorded %q, want the server's own sentence alone %q", s.errMsg, reason)
	}
	for _, unwanted := range []string{"oms:", "line_received", "{", "http 400"} {
		if strings.Contains(s.errMsg, unwanted) {
			t.Fatalf("the recorded refusal still carries %q: %q", unwanted, s.errMsg)
		}
	}
	pane := poRemovePane(s, 80)
	if !strings.Contains(pane, "This line records 4 received") {
		t.Fatalf("the refusal is not on the pane:\n%s", pane)
	}
	if strings.Contains(pane, "oms:") {
		t.Fatalf("the transport prefix reached the pane:\n%s", pane)
	}
	if fake.line("line-bolts") == nil {
		t.Fatal("the refused line was removed from the order anyway")
	}
}
