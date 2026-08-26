package tui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// The receiving flow end to end, driven through Root.Update the way an operator
// drives it: pick the order, scan or pick the line, scan the tracking barcode,
// say how much arrived, capture serials, finish the order off.
//
// Everything here reads the CLIPPED pane — clampToBox(screen.View(), pane) —
// and not the screen's own View(), for the reason the rest of this package
// does: clampToBox truncates in Root, so a test that reads View() passes over a
// line the terminal cuts in half.

// ---------------------------------------------------------------------------
// The worksheet is the input, and its two absences are different facts
// ---------------------------------------------------------------------------

// TestReceiveFlow_AnUnreadableWorksheetIsNotAnEmptyOne.
//
// "The worksheet could not be read" and "there is nothing to receive" are
// different facts and an operator acts differently on each — one means try
// again or fetch somebody, the other means the box in their hands is a
// surprise. There is deliberately NO fall-back to the order's own items:
// receiving off those would mean guessing at which codes resolve to which line
// and which identity a serial belongs to, which are exactly the two things this
// screen must not guess at.
func TestReceiveFlow_AnUnreadableWorksheetIsNotAnEmptyOne(t *testing.T) {
	fake := &receiveFake{sheet: receiveWorksheet(receiveOrder()...),
		sheetFail: http.StatusBadGateway, sheetBody: nginx502}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)

	if s.phase != phaseBlocked {
		t.Fatalf("a failed worksheet left the screen on phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 24)
	if !strings.Contains(text, "could not be read") {
		t.Errorf("the pane does not say the worksheet could not be read:\n%s", text)
	}
	if !strings.Contains(text, "not the same as an order with nothing left to receive") {
		t.Errorf("the pane does not keep could-not-tell apart from found-nothing:\n%s", text)
	}
	if !strings.Contains(text, "502 Bad Gateway") {
		t.Errorf("the reason the fetch failed is not on the pane:\n%s", text)
	}
	if len(s.qty) != 0 {
		t.Errorf("a screen that could not read the worksheet built %d quantity boxes anyway",
			len(s.qty))
	}
	if !barHas(s.bar(), "R", "Re-read") {
		t.Errorf("the frame offers no way to try again: %+v", s.bar())
	}

	// And `r` really does fetch again — the gateway coming back is one of the
	// two things that can change under this frame.
	fake.mu.Lock()
	fake.sheetFail = 0
	fake.mu.Unlock()
	r = receiveKey(t, r, poPhaseKeyMsg("r"))
	if s.phase != phaseQty {
		t.Fatalf("re-reading a worksheet that now answers left the screen on phase %v", s.phase)
	}
	if len(s.qty) != 3 {
		t.Errorf("the reload built %d quantity boxes, want one per receivable line", len(s.qty))
	}
}

// TestReceiveFlow_AnOrderThatCannotBeReceivedSaysWhy.
//
// can_receive and unavailable_reason are a pair and a client must not collapse
// them into a hidden button: the server's own sentence is what tells the
// operator whether to go and send the order or to go and ask about the box.
func TestReceiveFlow_AnOrderThatCannotBeReceivedSaysWhy(t *testing.T) {
	sheet := receiveWorksheet(receiveOrder()...)
	sheet.CanReceive, sheet.Status, sheet.StatusLabel = false, "draft", "Draft"
	sheet.UnavailableReason = "This order is still a draft. Send it to the supplier " +
		"before receiving against it."
	fake := &receiveFake{sheet: sheet}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)

	if s.phase != phaseBlocked {
		t.Fatalf("a refused order left the screen on phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 24)
	if !strings.Contains(text, "still a draft") {
		t.Errorf("the server's own reason is not on the pane:\n%s", text)
	}
	if !strings.Contains(text, "Draft") {
		t.Errorf("the pane does not say what state the order is in:\n%s", text)
	}
	// The lines are still listed and WALKABLE: "which lines am I waiting on?"
	// is a question an operator asks about an order they cannot receive against
	// too. The frame windows, so the second line is reached with the key the
	// bar names for it rather than assumed to be on the pane already.
	if !barHas(s.bar(), "UP/DN", "Lines") {
		t.Fatalf("the refused frame lists lines and names no key that walks them: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	walked := receivePaneText(s, 80, 24)
	if !strings.Contains(walked, "Reel of 24AWG wire") {
		t.Errorf("walking the refused frame does not reach the order's lines:\n%s", walked)
	}
	if !strings.Contains(walked, "Partially received") {
		t.Errorf("the refused frame does not say where receiving got to:\n%s", walked)
	}
}

// ---------------------------------------------------------------------------
// Scanning a line
// ---------------------------------------------------------------------------

// TestReceiveFlow_AScannedCodeFindsItsLine is step 2 of the flow.
//
// A barcode scanner is a keyboard that fires a burst and then an Enter, so the
// burst has to land somewhere it means something: the scan row leads the form
// for that reason, and Enter there FINDS rather than receives. Enter receiving
// while an unresolved code sat in the box would book a delivery on the press
// that was meant to choose a line.
func TestReceiveFlow_AScannedCodeFindsItsLine(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)
	if s.focused != receiveRowScan {
		t.Fatalf("a fresh form opens on row %d, not the scan row", s.focused)
	}
	// With a quantity typed and the scan box EMPTY there is nothing to find, so
	// Enter means what it means on every other row of the form.
	s.qty[0].SetValue("1")
	if !barHas(s.bar(), "Enter", "Receive") {
		t.Fatalf("an empty scan box does not offer Enter as the receive: %+v", s.bar())
	}
	s.qty[0].SetValue("")

	r = receiveTypeInto(t, r, "SKU-13") // the third line's own SKU
	if !barHas(s.bar(), "Enter", "Find line") {
		t.Errorf("a code in the scan box does not change what Enter does: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if want := receiveRowFirstLine + 2; s.focused != want {
		t.Fatalf("the scan landed the cursor on row %d, want %d", s.focused, want)
	}
	text := receivePaneText(s, 80, 24)
	if !strings.Contains(text, "is line 3") {
		t.Errorf("the scan does not say which line it found:\n%s", text)
	}
	if !strings.Contains(text, "our SKU") {
		t.Errorf("the scan does not say what kind of code matched:\n%s", text)
	}
	if len(fake.sent()) != 0 {
		t.Errorf("finding a line posted a receipt: %+v", fake.sent())
	}
}

// TestReceiveFlow_ACodeThatMatchesNothingSaysSo, and says WHICH kind of nothing.
//
// "No line carries that code" sends the operator back to the label; "no line on
// this order carries any code at all" tells them this order cannot be scanned
// to and they must pick by hand. An asset or freeform line contributes no codes,
// so an order made of those is the second case and is not rare.
func TestReceiveFlow_ACodeThatMatchesNothingSaysSo(t *testing.T) {
	t.Run("a coded order with no match", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)
		r = receiveTypeInto(t, r, "NOT-A-CODE")
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		text := receivePaneText(s, 80, 24)
		if !strings.Contains(text, "matches no line on this order") {
			t.Errorf("a scan that found nothing did not say so:\n%s", text)
		}
		if s.focused != receiveRowScan {
			t.Errorf("a scan that found nothing moved the cursor to row %d", s.focused)
		}
		_ = r
	})

	t.Run("an order nothing can be scanned to", func(t *testing.T) {
		line := receiveWSLine(21, "Custom fabricated bracket", 1, 0)
		line.ScanCodes = nil
		line.Item, line.ItemType = "", "freeform"
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, []omsapi.ReceivingLine{line}, 80, 24)
		if !strings.Contains(receivePaneText(s, 80, 24), "carries a scannable code") {
			t.Errorf("the scan row promises a match on an order that has no codes:\n%s",
				receivePaneText(s, 80, 24))
		}
		r = receiveTypeInto(t, r, "ANYTHING")
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if text := receivePaneText(s, 80, 24); !strings.Contains(text, "carries a scannable code") {
			t.Errorf("the refusal does not say the order cannot be scanned to:\n%s", text)
		}
		_ = r
	})

	t.Run("a code on a line that cannot take a receipt", func(t *testing.T) {
		closed := receiveWSLine(15, "Backordered gasket", 6, 2)
		closed.IsClosedShort, closed.IsSettled = true, true
		closed.ReceiptState, closed.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
		closed.QuantityPending = 0
		lines := []omsapi.ReceivingLine{receiveWSLine(11, "Box of M3 bolts", 4, 0), closed}
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, lines, 80, 24)
		r = receiveTypeInto(t, r, "SKU-15")
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		text := receivePaneText(s, 80, 24)
		if !strings.Contains(text, "closed short") {
			t.Errorf("a scan onto a settled line did not say why it cannot take a receipt:\n%s",
				text)
		}
		if strings.Contains(text, "matches no line") {
			t.Errorf("a settled line was reported as no match at all:\n%s", text)
		}
		_ = r
	})
}

// ---------------------------------------------------------------------------
// A mismatch is recorded and flagged, never rounded
// ---------------------------------------------------------------------------

// TestReceiveFlow_AnOverReceiptIsFlaggedAndSentAsTyped.
//
// The captain's decision, and the API's: record what actually arrived and flag
// the difference. The figure typed is the figure sent — a client that clamped
// it to the ordered quantity would destroy the only record of the discrepancy —
// and it is raised before the post, because a typo is cheaper to fix than a
// vendor query.
func TestReceiveFlow_AnOverReceiptIsFlaggedAndSentAsTyped(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)
	r = receiveGoToLine(t, r, s, 0) // ordered 4, received 0
	r = receiveTypeInto(t, r, "6")

	// On the form, under the box the number was typed into.
	if text := receivePaneText(s, 80, 24); !strings.Contains(text, "2 OVER") {
		t.Errorf("the form does not flag the over-receipt under the box:\n%s", text)
	}

	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review
	if s.phase != phaseReview {
		t.Fatalf("enter did not reach the review: phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 24)
	if !strings.Contains(text, "over the order") {
		t.Errorf("the review does not raise the over-receipt before it goes:\n%s", text)
	}
	if !strings.Contains(text, "never rounded") {
		t.Errorf("the review does not say the figure goes as typed:\n%s", text)
	}

	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	sent := fake.sent()
	if len(sent) != 1 || len(sent[0].Items) != 1 {
		t.Fatalf("want one receipt naming one line, got %+v", sent)
	}
	if got := sent[0].Items[0].QuantityReceived; got != 6 {
		t.Errorf("quantity_received = %d — the over-receipt was rounded to the order", got)
	}
	_ = r
}

// TestReceiveFlow_AShortReceiptIsOutstandingAndNotAMismatch.
//
// Short is NOT the same fact as over and must not be flagged as one: receiving
// 8 of 10 leaves 2 still expected, which may simply be on a backorder. Only an
// explicit close-short says the balance is not coming, and flagging every
// partial receipt as short would raise a vendor query on every backorder.
func TestReceiveFlow_AShortReceiptIsOutstandingAndNotAMismatch(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)
	r = receiveGoToLine(t, r, s, 0) // ordered 4
	r = receiveTypeInto(t, r, "3")

	text := receivePaneText(s, 80, 24)
	if !strings.Contains(text, "1 still outstanding") {
		t.Errorf("the form does not say what is still expected:\n%s", text)
	}
	if strings.Contains(text, "OVER") || strings.Contains(text, "SHORT") {
		t.Errorf("a partial receipt was flagged as a mismatch:\n%s", text)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// A partly received order says which lines are outstanding
// ---------------------------------------------------------------------------

// TestReceiveFlow_ThePartlyReceivedOrderIsUnambiguous.
//
// Every line is drawn with the state the SERVER says it is in, never one
// re-derived from the quantities: asking "is received < ordered?" calls a line
// closed short a partial one, and closed short is the one state that means
// somebody decided.
func TestReceiveFlow_ThePartlyReceivedOrderIsUnambiguous(t *testing.T) {
	closed := receiveWSLine(15, "Backordered gasket", 6, 2)
	closed.IsClosedShort, closed.IsSettled = true, true
	closed.ReceiptState, closed.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
	closed.ClosedShortReason, closed.QuantityPending = "backorder cancelled", 0
	done := receiveWSLine(16, "Reel of solder", 2, 2)
	lines := append(receiveOrder(), done, closed)

	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, lines, 80, 30)

	// The line a receipt may NOT name has no box and is listed as such: a line
	// that simply vanished would read as a line nobody ordered.
	if len(s.qty) != 4 {
		t.Errorf("want a box for every line a receipt may name, got %d", len(s.qty))
	}
	if len(s.closed) != 1 {
		t.Fatalf("want the closed-short line held back, got %d", len(s.closed))
	}
	r = receiveGoToLine(t, r, s, 1) // the part-received line
	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "Partially received") {
		t.Errorf("the part-received line does not carry the server's own state:\n%s", text)
	}
	if !strings.Contains(text, "pending 7") {
		t.Errorf("the part-received line does not say what is still expected:\n%s", text)
	}

	// And the settled tail is reachable from the notes row, where it hangs.
	for s.focused != s.notesRow() {
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	tail := receivePaneText(s, 80, 30)
	if !strings.Contains(tail, "cannot take a receipt") {
		t.Errorf("the lines a receipt may not name are not listed:\n%s", tail)
	}
	if !strings.Contains(tail, "Closed short") {
		t.Errorf("the closed-short line does not say what it is:\n%s", tail)
	}
}

// TestReceiveFlow_OutstandingSerialsAreOnTheLineTheyBelongTo.
//
// serials_outstanding is what replaced the old ban on serialized kit
// components: units already on the shelf carrying no serial. It is not an error
// and it blocks nothing, but it is invisible unless a client draws it — so it
// is drawn on the line it belongs to and broken down per identity.
func TestReceiveFlow_OutstandingSerialsAreOnTheLineTheyBelongTo(t *testing.T) {
	line := receiveWSSerialized(13, "Serialized controller board", 4, 2)
	line.SerialsOutstanding = 2
	line.SerialGap = []omsapi.SerialGapRow{
		{Item: line.Item, ItemName: line.Label, Expected: 2, Recorded: 0, Outstanding: 2},
	}
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, []omsapi.ReceivingLine{line}, 80, 30)
	r = receiveGoToLine(t, r, s, 0)

	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "2 units already in stock with no serial recorded") {
		t.Errorf("the outstanding serials are not on the line:\n%s", text)
	}
	if !strings.Contains(text, "0 of 2 captured") {
		t.Errorf("the gap is not broken down per identity:\n%s", text)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// The tracking barcode
// ---------------------------------------------------------------------------

// TestReceiveFlow_TheTrackingBarcodeReachesTheWireVerbatim is step 3.
//
// It is RECORDED and never interpreted: no transit duration is computed here or
// anywhere in the API, because goods are not always booked in on the day they
// arrive. The DELIVERED date is the operator's own statement of that day, which
// is the same fact read from the other side.
func TestReceiveFlow_TheTrackingBarcodeReachesTheWireVerbatim(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)

	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown}) // onto Tracking
	if s.focused != receiveRowTracking {
		t.Fatalf("down from the scan row landed on %d, not the tracking row", s.focused)
	}
	r = receiveTypeInto(t, r, "1Z999AA10123456784")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = receiveTypeInto(t, r, "UPS")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = receiveTypeInto(t, r, "2026-08-24") // arrived yesterday, booked in today
	r = receiveGoToLine(t, r, s, 0)
	r = receiveTypeInto(t, r, "2")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review

	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "1Z999AA10123456784") {
		t.Errorf("the review does not show what will be recorded:\n%s", text)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.sent()
	if len(sent) != 1 {
		t.Fatalf("want one receipt, got %+v", sent)
	}
	if sent[0].TrackingNumber != "1Z999AA10123456784" {
		t.Errorf("tracking_number = %q", sent[0].TrackingNumber)
	}
	if sent[0].Carrier != "UPS" {
		t.Errorf("carrier = %q", sent[0].Carrier)
	}
	if sent[0].DeliveryDate != "2026-08-24" {
		t.Errorf("delivery_date = %q — the date the operator STATED is what is recorded",
			sent[0].DeliveryDate)
	}
	// Nothing on the screen derives a duration from any of it.
	for _, forbidden := range []string{"transit", "days in transit", "took "} {
		if strings.Contains(strings.ToLower(receivePaneText(s, 80, 30)), forbidden) {
			t.Errorf("the screen computes something from the tracking data: %q", forbidden)
		}
	}
}

// TestReceiveFlow_ADeliveredDateThatIsNotADateIsRefused. A scanner burst that
// lands in the date box must not be sent as a date, and the refusal has to name
// the shape rather than rolling the whole receipt back on a 400.
func TestReceiveFlow_ADeliveredDateThatIsNotADateIsRefused(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
	r = receiveGoToLine(t, r, s, 0)
	r = receiveTypeInto(t, r, "2")
	for s.focused != receiveRowDelivered {
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyUp})
	}
	r = receiveTypeInto(t, r, "24081Z999")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // refused before the post

	if len(fake.sent()) != 0 {
		t.Errorf("a receipt went out with a delivered date that is not one: %+v", fake.sent())
	}
	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "YYYY-MM-DD") {
		t.Errorf("the refusal does not say what shape the date takes:\n%s", text)
	}
	if got := s.delivered.Value(); got != "24081Z999" {
		t.Errorf("the refusal discarded what was typed: %q", got)
	}
}

// ---------------------------------------------------------------------------
// Writing a balance off
// ---------------------------------------------------------------------------

// TestReceiveFlow_ClosingALineShortIsConfirmedAndReachesItsEndpoint.
//
// This is how a short receipt ENDS, and it is destructive: the balance is
// recorded as never arriving. Ctrl+X and not Enter commits it, for the reason
// the New PO screen's supplier switch uses the same key — a reflexive
// double-tap of the key that opened the confirm must not be what writes a
// balance off.
func TestReceiveFlow_ClosingALineShortIsConfirmedAndReachesItsEndpoint(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)

	// Ctrl+K is named only where it can act, and the scan row is not a line.
	if barHas(s.bar(), "Ctrl+K", "Close short") {
		t.Errorf("the bar names Ctrl+K with the cursor off a line: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlK})
	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "cursor is not on one") {
		t.Errorf("ctrl+k off a line did not say why it declined:\n%s", text)
	}
	if s.phase != phaseQty {
		t.Fatalf("ctrl+k off a line opened the confirm anyway: phase %v", s.phase)
	}

	r = receiveGoToLine(t, r, s, 1) // ordered 10, received 3
	if !barHas(s.bar(), "Ctrl+K", "Close short") {
		t.Fatalf("the bar does not name Ctrl+K on a line with an outstanding balance: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlK})
	if s.phase != phaseWriteOff {
		t.Fatalf("ctrl+k did not open the confirm: phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "7 units still outstanding are recorded as never arriving") {
		t.Errorf("the confirm does not say what it will do:\n%s", text)
	}
	if !barHas(s.bar(), "Ctrl+X", "Close line short") {
		t.Errorf("the confirm does not name the key that commits: %+v", s.bar())
	}

	// Enter is deliberately NOT the commit, and declining to bind it is not
	// licence to leave the press silent.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if len(fake.closedShort()) != 0 {
		t.Fatalf("enter wrote the balance off: %+v", fake.closedShort())
	}
	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "ctrl+x is") {
		t.Errorf("enter on the confirm did not name the key that acts:\n%s", text)
	}

	r = receiveTypeInto(t, r, "backorder cancelled")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	closed := fake.closedShort()
	if len(closed) != 1 || len(closed[0].Items) != 1 {
		t.Fatalf("want one close-short naming one line, got %+v", closed)
	}
	if got := fmt.Sprint(closed[0].Items[0].PurchaseOrderItem); got != "12" {
		t.Errorf("the wrong line was closed short: %q", got)
	}
	if closed[0].Items[0].Reason != "backorder cancelled" {
		t.Errorf("the reason was dropped: %q", closed[0].Items[0].Reason)
	}
	if len(fake.sent()) != 0 {
		t.Errorf("closing a line short posted a receipt too: %+v", fake.sent())
	}
	if s.phase != phaseDone {
		t.Errorf("the write-off left the screen on phase %v", s.phase)
	}
}

// TestReceiveFlow_EscOnTheConfirmWritesNothingOff. The safe answer has to be
// reachable and has to say that nothing happened.
func TestReceiveFlow_EscOnTheConfirmWritesNothingOff(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
	r = receiveGoToLine(t, r, s, 1)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlK})
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})

	if s.phase != phaseQty {
		t.Fatalf("esc on the confirm left the screen on phase %v", s.phase)
	}
	if len(fake.closedShort()) != 0 {
		t.Errorf("esc wrote a balance off: %+v", fake.closedShort())
	}
	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "nothing was closed short") {
		t.Errorf("esc did not say that nothing happened:\n%s", text)
	}
}

// TestReceiveFlow_MarkingReceivedFinishesTheOrderOff is step 6.
//
// It closes every still-outstanding line SHORT and advances the order. It is
// deliberately not mark-delivered, which asserts the opposite — that the
// outstanding quantity did arrive — and stocks it. The difference is exactly
// the difference between an honest record and a tidy one.
func TestReceiveFlow_MarkingReceivedFinishesTheOrderOff(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)

	if !barHas(s.bar(), "Ctrl+R", "Mark received") {
		t.Fatalf("the bar does not name Ctrl+R on an order with outstanding lines: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	if s.phase != phaseWriteOff {
		t.Fatalf("ctrl+r did not open the confirm: phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "closed SHORT") {
		t.Errorf("the confirm does not say the outstanding lines are written off:\n%s", text)
	}
	if !strings.Contains(text, "nothing is stocked by it") {
		t.Errorf("the confirm does not keep this apart from mark-delivered:\n%s", text)
	}

	r = receiveTypeInto(t, r, "vendor closed the order")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	marked := fake.marked()
	if len(marked) != 1 || marked[0] != "vendor closed the order" {
		t.Fatalf("mark-received was called %d times with %v", len(marked), marked)
	}
	if len(fake.sent()) != 0 {
		t.Errorf("marking received posted a receipt too: %+v", fake.sent())
	}
	if s.phase != phaseDone {
		t.Errorf("the write-off left the screen on phase %v", s.phase)
	}
}

// TestReceiveFlow_TheSummaryReportsWhatTheOrderBecameRatherThanAssumingIt.
//
// Closing the last outstanding balance does NOT on its own make the order
// `received`: an order nothing was ever received against does not advance, and
// that rule is the server's — it has already changed once since this client was
// written. So nothing on this screen may predict it. The confirm says what the
// write DOES, and the summary reports the status that came back, whatever it is.
//
// Driven with a reply that came back NOT advanced, which is the case an
// assumption gets wrong and the ordinary one hides.
func TestReceiveFlow_TheSummaryReportsWhatTheOrderBecameRatherThanAssumingIt(t *testing.T) {
	fake := &receiveFake{replyWith: map[string]any{
		"id": 5, "po_number": "PO-1001",
		// Every line closed short, nothing ever received against the order — so
		// it stays where it was rather than advancing.
		"status": "sent", "status_label": "Sent",
		"total_received_quantity": 0, "total_quantity": 9,
		"outstanding_line_count": 0,
	}}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)

	// The confirm must not promise the transition either: the operator reads it
	// before pressing the key, and a promise the server will not keep is the
	// same defect one frame earlier.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	confirm := receivePaneText(s, 80, 30)
	if strings.Contains(confirm, "order goes to received") ||
		strings.Contains(confirm, "advances the order") {
		t.Errorf("the confirm predicts what the order will become:\n%s", confirm)
	}

	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	if len(fake.marked()) != 1 {
		t.Fatalf("mark-received was called %d times", len(fake.marked()))
	}
	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "PO-1001 · Sent") {
		t.Errorf("the summary does not report the status that came back:\n%s", text)
	}
	if strings.Contains(text, "Received") {
		t.Errorf("the summary reports a transition the server did not make:\n%s", text)
	}
	_ = r
}

// TestReceiveFlow_AnExpiredSessionIsASentenceAndNotABody.
//
// The worksheet is a GET and was served under IsAuthenticatedOrReadOnly, which
// lets a read through unauthenticated; gating it to IsAuthenticated turns a
// fetch that always answered into one that can 401. DRF renders that as
// `{"detail": ...}` — neither the receiving endpoints' hand-built refusal
// envelope nor a coded one — so without a name for it the operator reads the
// JSON on the frame whose whole job is explaining why nothing can be received.
//
// The client refreshes its token once before this is reached, so arriving here
// means the refresh failed too and signing in again is the actual next move.
func TestReceiveFlow_AnExpiredSessionIsASentenceAndNotABody(t *testing.T) {
	fake := &receiveFake{sheet: receiveWorksheet(receiveOrder()...),
		sheetFail: http.StatusUnauthorized,
		sheetBody: `{"detail":"Authentication credentials were not provided."}`}
	_, s := receiveDrive(t, fake, receiveOrder(), 80, 24)

	if s.phase != phaseBlocked {
		t.Fatalf("a 401 on the worksheet left the screen on phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 24)
	if !strings.Contains(text, "no longer signed in") {
		t.Errorf("an expired session is not named:\n%s", text)
	}
	if strings.Contains(text, `{"detail"`) || strings.Contains(text, "http 401") {
		t.Errorf("the operator is reading a raw body:\n%s", text)
	}
	// And it still names the key that gets them back once they have signed in.
	if !barHas(s.bar(), "R", "Re-read") {
		t.Errorf("the frame offers no way to try again: %+v", s.bar())
	}
}

// TestReceiveFlow_MarkReceivedIsNotNamedOnASettledOrder. A key the server would
// refuse must not be advertised, and the press has to say why rather than
// opening a confirm for something that cannot happen.
func TestReceiveFlow_MarkReceivedIsNotNamedOnASettledOrder(t *testing.T) {
	done := receiveWSLine(16, "Reel of solder", 2, 2)
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, []omsapi.ReceivingLine{done}, 80, 30)

	if barHas(s.bar(), "Ctrl+R", "Mark received") {
		t.Errorf("the bar names Ctrl+R on an order with nothing outstanding: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	if s.phase != phaseQty {
		t.Fatalf("ctrl+r opened a confirm for a write-off that cannot happen: phase %v", s.phase)
	}
	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "already settled") {
		t.Errorf("ctrl+r did not say why it declined:\n%s", text)
	}
}

// TestReceiveFlow_AnOrderWithNothingLeftToReceiveNamesTheWayOut.
//
// A state the merged contract added and calls out to read twice:
// `can_receive: true` with `outstanding_line_count: 0`. It is what an order
// lands in when every line was closed short or struck off without a single
// delivery — it never reached `received`, because that status means goods
// arrived, so it is still a receivable status with nothing left to receive.
//
// The contract instructs a client here in as many words: "say so, and point at
// voiding or cancelling the order." Saying only the first half leaves the
// operator on a form with no lines, a key that refuses, and nowhere to go.
func TestReceiveFlow_AnOrderWithNothingLeftToReceiveNamesTheWayOut(t *testing.T) {
	closed := receiveWSLine(15, "Backordered gasket", 6, 0)
	closed.IsClosedShort, closed.IsSettled = true, true
	closed.ReceiptState, closed.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
	closed.QuantityPending, closed.QuantityVariance = 0, -6

	sheet := receiveWorksheet(closed)
	// Still receivable, and nothing to receive: the order never advanced,
	// because nothing was ever received against it.
	sheet.CanReceive, sheet.Status, sheet.StatusLabel = true, "sent", "Sent"
	sheet.OutstandingLineCount = 0
	fake := &receiveFake{sheet: sheet}
	r, s := receiveDrive(t, fake, []omsapi.ReceivingLine{closed}, 80, 30)

	if s.phase != phaseQty {
		t.Fatalf("a receivable order left the screen on phase %v", s.phase)
	}
	if len(s.qty) != 0 {
		t.Fatalf("want no receivable lines, got %d", len(s.qty))
	}
	for s.focused != s.notesRow() {
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "nothing to book against this order") {
		t.Errorf("the form does not say there is nothing to receive:\n%s", text)
	}
	if !strings.Contains(text, "Void or cancel the ORDER") {
		t.Errorf("the form leaves the operator with no way to finish with the order:\n%s", text)
	}

	// And the key that looks like the way out says the same thing rather than
	// refusing into a dead end — the server would, and this saves the trip.
	if barHas(s.bar(), "Ctrl+R", "Mark received") {
		t.Errorf("the bar names Ctrl+R on an order with nothing outstanding: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	if decline := receivePaneText(s, 80, 30); !strings.Contains(decline, "void or cancel the order") {
		t.Errorf("ctrl+r declines without naming the way out:\n%s", decline)
	}
	if len(fake.marked()) != 0 {
		t.Errorf("the decline still called mark-received: %v", fake.marked())
	}
}

// TestReceiveFlow_AReopenedLineSaysItWasClosedShortOnce.
//
// `reopen-short/` takes back a close-short recorded in error, and it is a
// CORRECTION rather than an undo: the write-off keeps its stamps and the reopen
// is recorded beside it. The line comes back outstanding, so it is receivable
// again — and an operator receiving against it is receiving against one
// somebody has already got wrong once, which the row says.
//
// ScanTTY does not drive reopen-short; what it must not do is show the
// corrected line as though nothing had happened to it.
func TestReceiveFlow_AReopenedLineSaysItWasClosedShortOnce(t *testing.T) {
	line := receiveWSLine(12, "Reel of 24AWG wire", 10, 3)
	line.WasReopened, line.ReopenedReason = true, "closed the wrong line"
	line.ClosedShortReason = "backorder cancelled"

	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, []omsapi.ReceivingLine{line}, 80, 30)

	// It is receivable again: is_closed_short is derived from both stamps, so a
	// reopened line is simply not closed short any more.
	if len(s.qty) != 1 {
		t.Fatalf("a reopened line is not receivable again: %d boxes, %d held back",
			len(s.qty), len(s.closed))
	}
	r = receiveGoToLine(t, r, s, 0)
	if text := receivePaneText(s, 80, 30); !strings.Contains(text, "reopened") {
		t.Errorf("the line does not say it was closed short once:\n%s", text)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// A refusal is a sentence, never a raw dump
// ---------------------------------------------------------------------------

// TestReceiveFlow_AServerRefusalIsShownAsItsOwnSentence.
//
// The receiving endpoints write `{"error": "<prose>"}` by hand, so parseError
// hands the whole raw body over. Without omsapi.AsReceivingRefusal the operator
// reads the JSON — on the step where losing the reason costs the delivery.
func TestReceiveFlow_AServerRefusalIsShownAsItsOwnSentence(t *testing.T) {
	const refusal = "Line item 12: this line credits several serialized items, so each " +
		"serial must say which one it belongs to (one of: Black ink, Cyan ink)"
	fake := &receiveFake{failWith: http.StatusBadRequest,
		failBody: `{"error": "` + refusal + `"}`}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
	r = receiveGoToLine(t, r, s, 0)
	r = receiveTypeInto(t, r, "2")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // refused

	text := receivePaneText(s, 80, 30)
	if !strings.Contains(text, "which one it belongs to") {
		t.Errorf("the server's own sentence is not on the pane:\n%s", text)
	}
	if strings.Contains(text, `{"error"`) || strings.Contains(text, "http 400") {
		t.Errorf("the operator is reading a raw body:\n%s", text)
	}
	// The headline names the order the way every other line on the screen does.
	if !strings.Contains(text, "Receiving PO-1001 failed") {
		t.Errorf("the failure headline does not name the order:\n%s", text)
	}
	// And nothing is lost: Esc goes back to a form that still holds the counts.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != phaseQty || s.qty[0].Value() != "2" {
		t.Errorf("a refused receipt cost the operator their entry: phase %v, qty %q",
			s.phase, s.qty[0].Value())
	}
}
