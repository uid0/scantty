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
	if !barHas(s.bar(), "r", "Re-read") {
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

// TestReceiveFlow_ACodeOnTwoLinesNamesTheOthersAndTheKeyThatReachesThem.
//
// The same part ordered twice — two lines, two expected dates, one code — is an
// ordinary order, so a scan that resolves to more than one line has to leave
// the operator able to reach the rest.
//
// The note used to say "N lines carry it, enter finds the next", which was
// false in both halves. Enter cannot mean FIND from a line row (enterAction
// returns receiveEnterFind only while the cursor is in the SCAN box, and
// findLine has just moved it off), so the operator who followed the
// instruction, with a quantity typed as the screen asks, was taken to the
// REVIEW of a receipt; and the cycling loop behind the sentence looked for the
// focused row among the matches when the focused row was the scan box, so it
// never fired and no key on the screen reached the second line at all.
//
// Both halves are held here: the OTHER positions are named, the key the note
// names really walks to them, and Enter from the found line still commits.
func TestReceiveFlow_ACodeOnTwoLinesNamesTheOthersAndTheKeyThatReachesThem(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	// One code on lines 1 and 3 of a three-line order.
	shared := func() []omsapi.ReceivingLine {
		first := receiveWSLine(11, "Box of M3 bolts", 4, 0)
		second := receiveWSLine(12, "Reel of 24AWG wire", 10, 3)
		third := receiveWSLine(13, "Box of M3 bolts", 6, 0)
		third.ScanCodes = first.ScanCodes
		return []omsapi.ReceivingLine{first, second, third}
	}

	t.Run("it names the other line and the key that reaches it", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, shared(), 80, 24)
		r = receiveTypeInto(t, r, "SKU-11")
		r = receiveKey(t, r, enter)

		if want := receiveRowFirstLine; s.focused != want {
			t.Fatalf("the scan landed on row %d, want the first match at %d", s.focused, want)
		}
		text := receivePaneText(s, 80, 24)
		if !strings.Contains(text, "line 3 carries it too") {
			t.Errorf("the scan does not name the other line the code resolved to:\n%s", text)
		}
		if !strings.Contains(text, "up/dn walks there") {
			t.Errorf("the scan does not say how to reach it:\n%s", text)
		}
		if strings.Contains(text, "enter finds the next") {
			t.Errorf("the scan still names enter, which finds nothing from a line row:\n%s", text)
		}

		// The key it names really gets there: walk down until the cursor is on
		// the other match, within the rows the form has.
		reached := false
		for i := 0; i < receiveRowFirstLine+len(s.lines)+2; i++ {
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
			if s.focused == receiveRowFirstLine+2 {
				reached = true
				break
			}
		}
		if !reached {
			t.Fatalf("up/dn never reached the other line the note named")
		}
		if got := receiveCursorMarker(t, s); got != "Box" {
			t.Errorf("the cursor landed on %q, not on the other line carrying the code", got)
		}

		// And Enter from a line row still COMMITS, which is why the note cannot
		// name it as the way to the next match.
		r = receiveTypeInto(t, r, "2")
		r = receiveKey(t, r, enter)
		if s.phase != phaseReview {
			t.Fatalf("enter from a found line did not commit into the review: phase %v", s.phase)
		}
	})

	t.Run("a long run of matches is counted rather than listed", func(t *testing.T) {
		var lines []omsapi.ReceivingLine
		for i := 0; i < 6; i++ {
			l := receiveWSLine(20+i, fmt.Sprintf("Box of M3 bolts, crate %d", i+1), 4, 0)
			l.ScanCodes = []omsapi.ScanCode{{Code: "SKU-20", Kind: omsapi.ScanCodeItemSKU}}
			lines = append(lines, l)
		}
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, lines, 80, 24)
		r = receiveTypeInto(t, r, "SKU-20")
		r = receiveKey(t, r, enter)

		text := receivePaneText(s, 80, 24)
		if !strings.Contains(text, "5 more lines carry it") {
			t.Errorf("a long run of matches was not counted:\n%s", text)
		}
		if !strings.Contains(text, "up/dn walks to them") {
			t.Errorf("the counted note does not say how to reach them:\n%s", text)
		}
		_ = r
	})
}

// TestReceiveFlow_AScanNamesTheLineByTheNumberTheFormDraws.
//
// `scanMatches` counted positions over `s.sheet.Lines` — every line of the
// order — while everything the operator reads counts over `s.lines`, which
// leaves the voided and closed-short ones out (applyWorksheet splits them into
// s.closed). The two agree only on an order with nothing settled, and a settled
// line ahead of a live one is the ordinary shape of the partial-receipt flow
// this screen exists for.
//
// So on [#11 voided, #12, #13] the form drew #13 as line 2 and the scan note
// called it line 3 — a number no row on the pane carries. On the multi-match
// path the same wrong numbers were listed under "up/dn walks there", which
// walks the operator to a different line, on the screen whose whole job is
// booking stock against the right one.
//
// Held against what the FORM draws rather than against a number written here:
// the assertion reads the heading off the pane for the row the scan actually
// focused, so it cannot agree with a second index the way the note did.
func TestReceiveFlow_AScanNamesTheLineByTheNumberTheFormDraws(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	// One voided line ahead of two live ones, and the same code on both live
	// ones so the single-match lead and the multi-match list are both driven.
	order := func() []omsapi.ReceivingLine {
		voided := receiveWSLine(11, "Cancelled bracket", 3, 0)
		voided.IsVoided, voided.IsSettled = true, true
		voided.QuantityPending = 0
		second := receiveWSLine(12, "Box of M3 bolts", 4, 0)
		third := receiveWSLine(13, "Reel of 24AWG wire", 10, 0)
		return []omsapi.ReceivingLine{voided, second, third}
	}

	t.Run("a live line is named the number its own row carries", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, order(), 80, 24)
		r = receiveTypeInto(t, r, "SKU-13")
		r = receiveKey(t, r, enter)

		i, ok := s.lineAt(s.focused)
		if !ok {
			t.Fatalf("the scan left the cursor on row %d, which carries no line", s.focused)
		}
		if got := fmt.Sprint(s.lines[i].sheet.PurchaseOrderItem); got != "13" {
			t.Fatalf("the scan focused line id %s, want 13", got)
		}
		// The number the FORM draws for that row, read off the pane rather than
		// computed here: `2  Reel of 24AWG wire`.
		text := receivePaneText(s, 80, 24)
		drawn := fmt.Sprintf("%d %s", i+1, s.lines[i].sheet.Label)
		if !strings.Contains(text, drawn) {
			t.Fatalf("the form does not draw %q, so this test cannot tell what it "+
				"calls the line:\n%s", drawn, text)
		}
		if !strings.Contains(text, fmt.Sprintf("is line %d", i+1)) {
			t.Errorf("the scan note does not call the line by the number the form draws "+
				"(%d):\n%s", i+1, text)
		}
		// And it does not use the worksheet's own position, which is one more
		// because of the voided line ahead of it.
		if strings.Contains(text, fmt.Sprintf("is line %d", i+2)) {
			t.Errorf("the scan note numbers over the worksheet, not over the form:\n%s", text)
		}
	})

	t.Run("the other matches are listed by form number too", func(t *testing.T) {
		lines := order()
		lines[2].ScanCodes = lines[1].ScanCodes // one code on both live lines
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, lines, 80, 24)
		r = receiveTypeInto(t, r, "SKU-12")
		r = receiveKey(t, r, enter)

		text := receivePaneText(s, 80, 24)
		// The live lines are 1 and 2 on the form; the scan lands on 1, so the
		// note has to point at 2 and never at 3.
		if !strings.Contains(text, "line 2 carries it too") {
			t.Errorf("the other match is not listed by the number the form draws:\n%s", text)
		}
		if strings.Contains(text, "line 3 carries it too") {
			t.Errorf("the other match is listed by its worksheet position:\n%s", text)
		}
		// And the key the note names really lands on the line it pointed at.
		for i := 0; i < receiveRowFirstLine+len(s.lines)+2 && s.focused != receiveRowFirstLine+1; i++ {
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
		}
		if s.focused != receiveRowFirstLine+1 {
			t.Fatalf("up/dn never reached the row the note named")
		}
		if got := fmt.Sprint(s.lines[1].sheet.PurchaseOrderItem); got != "13" {
			t.Errorf("form line 2 is order line %s, so the note pointed somewhere else", got)
		}
		_ = r
	})

	// A settled match is named by its place in the SETTLED list, which is the
	// one identifier that both fits the refusal's budget and cannot point
	// somewhere else.
	//
	// The label cannot do it. The refusal carries the way-out tail, so its
	// values live inside receiveScanRefusalRoom, and two lines whose names
	// differ only past that bound clip to the same string — an operator holding
	// one of two boxes reads a sentence that is true of either. That is the
	// order this drives: two closed-short gaskets differing at their last four
	// characters, which is what a size or a revision looks like in a real
	// catalogue.
	//
	// A FORM number cannot do it either, and that is the constraint the whole
	// sentence is shaped around: the receivable lines are numbered 1..n of
	// their own, so "line 2" would point at a line the operator CAN receive
	// against.
	t.Run("a settled line is named by the settled list's own number", func(t *testing.T) {
		first := receiveWSLine(41, "Backordered gasket, 10mm", 6, 2)
		second := receiveWSLine(42, "Backordered gasket, 12mm", 6, 0)
		for _, l := range []*omsapi.ReceivingLine{&first, &second} {
			l.IsClosedShort, l.IsSettled = true, true
			l.ReceiptState, l.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
			l.QuantityPending = 0
		}
		lines := []omsapi.ReceivingLine{
			receiveWSLine(40, "Box of M3 bolts", 4, 0), first, second,
		}
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, lines, 80, 24)

		// The two labels really are indistinguishable once the refusal bounds
		// them, so this order is the ambiguity and not a fixture that dodges it.
		if receiveRefusalClip(first.Label) != receiveRefusalClip(second.Label) {
			t.Fatalf("the two settled labels clip apart (%q vs %q), so this order does "+
				"not reach the case the sentence was reshaped for",
				receiveRefusalClip(first.Label), receiveRefusalClip(second.Label))
		}

		r = receiveTypeInto(t, r, "SKU-42") // the SECOND settled line
		r = receiveKey(t, r, enter)

		text := receivePaneText(s, 80, 24)
		if !strings.Contains(text, "is settled line 2") {
			t.Errorf("a settled match is not named by its place in the settled list:\n%s", text)
		}
		if !strings.Contains(text, "closed short") {
			t.Errorf("a settled match does not say why it cannot take a receipt:\n%s", text)
		}
		// And never a FORM number, which would point at a receivable line.
		if strings.Contains(text, "is line ") {
			t.Errorf("a settled match was given a form line number:\n%s", text)
		}
		if s.focused != receiveRowScan {
			t.Errorf("a settled match moved the cursor to row %d", s.focused)
		}

		// The number points at a list the pane really draws, and at the entry
		// carrying the label IN FULL — which is what makes it an identifier
		// rather than a second index nobody can resolve. The list hangs off the
		// notes row, so it is reached the way an operator reaches it.
		for s.focused != s.notesRow() {
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
		}
		tail := receivePaneText(s, 80, 30)
		if !strings.Contains(tail, "2 settled lines cannot take a receipt") {
			t.Errorf("the settled list does not name itself, so \"settled line 2\" "+
				"resolves to nothing:\n%s", tail)
		}
		if !strings.Contains(tail, "2. "+second.Label) {
			t.Errorf("the settled list's entry 2 is not the line the note named:\n%s", tail)
		}
		_ = r
	})
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
		if !strings.Contains(text, "matches no line here") {
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

// TestReceiveFlow_ReceiveMoreDoesNotCarryTheLastDeliveryDate.
//
// `R` on the summary is "receive more against this order", and resetEntry is
// what makes the form it lands on a fresh one. It walks allBoxes, and allBoxes
// did not know about the Delivered box — so the operator who typed the day the
// goods actually arrived (the case the field's own caveat exists for: goods in
// Thursday, booked in Friday) got that date handed back to them on the NEXT
// delivery, under a hint still reading "blank = today", and the receipt posted
// it for goods that came in on a different day.
//
// It is the wire that is asserted, not the box: a box the operator can see is
// half the defect, and the half that matters is the date that reached OMS.
func TestReceiveFlow_ReceiveMoreDoesNotCarryTheLastDeliveryDate(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)

	// A delivery booked in on a day of its own.
	for s.focused != receiveRowDelivered {
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	}
	r = receiveTypeInto(t, r, "2026-08-20")
	r = receiveGoToLine(t, r, s, 0)
	r = receiveTypeInto(t, r, "1")
	r = receiveKey(t, r, enter) // -> review
	r = receiveKey(t, r, enter) // the receipt goes
	if s.phase != phaseDone {
		t.Fatalf("the first receipt did not land on the summary: phase %v", s.phase)
	}
	if sent := fake.sent(); len(sent) != 1 || sent[0].DeliveryDate != "2026-08-20" {
		t.Fatalf("the first receipt did not carry the date it was given: %+v", sent)
	}

	// "Receive more": a different box turns up on a different day.
	r = receiveKey(t, r, poRuneKey("r"))
	if s.phase != phaseQty {
		t.Fatalf("R did not hand back the quantity form: phase %v", s.phase)
	}
	if got := s.delivered.Value(); got != "" {
		t.Errorf("R handed back a form still holding the last delivery's date: %q", got)
	}
	r = receiveGoToLine(t, r, s, 1)
	r = receiveTypeInto(t, r, "2")
	r = receiveKey(t, r, enter) // -> review
	r = receiveKey(t, r, enter) // the second receipt goes

	sent := fake.sent()
	if len(sent) != 2 {
		t.Fatalf("want two receipts, got %d: %+v", len(sent), sent)
	}
	if sent[1].DeliveryDate != "" {
		t.Errorf("the second receipt carried the FIRST delivery's date (%q) — blank means "+
			"today, which is what the operator was shown", sent[1].DeliveryDate)
	}
	_ = r
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

// TestReceiveFlow_AWriteOffRefusesToDiscardEntryAnywhereOnTheForm.
//
// Both write-off keys END THE FORM and neither can be undone from this client —
// reopen-short is decoded and deliberately not driven — so committing one over
// the top of anything the operator typed destroys it with no way back. The only
// path that hands the boxes back is a write-off that FAILED: on success
// handleSubmitted lands on the summary, `r` there calls resetEntry before the
// reload, and enter/esc leave the screen.
//
// The gate they share was per-line for Ctrl+K to begin with, and that is what
// this drives: the entry is put somewhere the cursor is NOT, which is the case
// the focused-line predicate could not see. It is also not only the quantity
// boxes — a form-ending write throws away the tracking number, the carrier, the
// delivered date, the notes and every captured serial — so the holds below are
// a table rather than one case, and the keys are a table beside it because the
// rule is the same rule for both.
func TestReceiveFlow_AWriteOffRefusesToDiscardEntryAnywhereOnTheForm(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	down := tea.KeyMsg{Type: tea.KeyDown}

	// Ctrl+K needs the cursor on a line with an outstanding balance; Ctrl+R does
	// not care where it is. Both reaches leave the cursor OFF whatever the hold
	// put down, which is the point of the case.
	keys := []struct {
		name  string
		msg   tea.KeyMsg
		token string
		label string
		reach func(t *testing.T, r Root, s *ReceiveFormScreen) Root
	}{
		{"ctrl+k", tea.KeyMsg{Type: tea.KeyCtrlK}, "Ctrl+K", "Close short",
			func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
				return receiveGoToLine(t, r, s, 1) // ordered 10, received 3
			}},
		{"ctrl+r", tea.KeyMsg{Type: tea.KeyCtrlR}, "Ctrl+R", "Mark received",
			func(t *testing.T, r Root, s *ReceiveFormScreen) Root { return r }},
	}

	holds := []struct {
		name string
		put  func(t *testing.T, r Root, s *ReceiveFormScreen) Root
		says string
		// intact re-reads what the hold put down, so the assertion that the
		// refusal disturbed nothing is about the operator's own text.
		intact func(s *ReceiveFormScreen) string
		want   string
	}{
		{"a quantity on ANOTHER line", func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			r = receiveGoToLine(t, r, s, 0) // ordered 4, and NOT where either key acts from
			return receiveTypeInto(t, r, "4")
		}, "a quantity on 1 line",
			func(s *ReceiveFormScreen) string { return s.qty[0].Value() }, "4"},

		{"a tracking number", func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			for s.focused != receiveRowTracking {
				r = receiveKey(t, r, down)
			}
			return receiveTypeInto(t, r, "1Z999AA10123456784")
		}, "a tracking number",
			func(s *ReceiveFormScreen) string { return s.tracking.Value() }, "1Z999AA10123456784"},

		// A captured serial AND a quantity: the second half proves the sentence
		// is bounded — it names the first thing and counts the rest rather than
		// listing every line and field into the tail where the way out lives.
		{"a captured serial beside a quantity", func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			r = receiveGoToLine(t, r, s, 2) // the serialized line
			r = receiveTypeInto(t, r, "1")
			r = receiveKey(t, r, enter) // -> capture
			r = receiveType(t, r, poRuneKey("SN-1"))
			r = receiveKey(t, r, enter) // the last unit lands on the review
			return receiveKey(t, r, esc)
		}, "a quantity on 1 line and 1 more",
			func(s *ReceiveFormScreen) string { return s.captures[0].serial }, "SN-1"},
	}

	for _, k := range keys {
		for _, h := range holds {
			t.Run(k.name+" over "+h.name, func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
				r = h.put(t, r, s)
				r = k.reach(t, r, s)

				// The bar follows the gate: a key that could only refuse is not
				// named, on either key and over any hold.
				if barHas(s.bar(), k.token, k.label) {
					t.Errorf("the bar names %s over %s: %+v", k.token, h.name, s.bar())
				}
				r = receiveKey(t, r, k.msg)

				if s.phase != phaseQty {
					t.Fatalf("%s opened the confirm over %s: phase %v", k.name, h.name, s.phase)
				}
				if got := h.intact(s); got != h.want {
					t.Errorf("the refusal disturbed what was typed: %q, want %q", got, h.want)
				}
				if got := fake.closedShort(); len(got) != 0 {
					t.Errorf("a line was closed short anyway: %+v", got)
				}
				if got := fake.marked(); len(got) != 0 {
					t.Errorf("the order was marked received anyway: %+v", got)
				}
				if text := receivePaneText(s, 80, 30); !strings.Contains(text, h.says) {
					t.Errorf("the refusal does not say what is holding it (%q):\n%s", h.says, text)
				}
				_ = r
			})
		}
	}

	// A typed ZERO is still the operator's, and a write-off would take it away
	// exactly as it takes a 4 — the reasoning hasQuantityEntry already records
	// for what Esc costs. Enter cannot receive a zero, so this is the one state
	// where the gate is the only thing between a typed figure and an
	// irreversible write.
	t.Run("a typed zero counts", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
		r = receiveGoToLine(t, r, s, 0)
		r = receiveTypeInto(t, r, "0")
		r = receiveGoToLine(t, r, s, 1)
		if barHas(s.bar(), "Ctrl+K", "Close short") {
			t.Errorf("the bar names Ctrl+K over a typed zero: %+v", s.bar())
		}
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlK})
		if s.phase != phaseQty {
			t.Errorf("a typed zero was written off: phase %v", s.phase)
		}
		if got := s.qty[0].Value(); got != "0" {
			t.Errorf("the refusal disturbed the typed zero: %q", got)
		}
		_ = r
	})

	// The SCAN box is excluded from the gate ON PURPOSE — entryBoxesExcluded
	// records why: Enter consumes it to find a line and buildReceipt never
	// carries it, so a write-off destroys nothing the operator would want back.
	// Driven rather than trusted, because a recorded exception that the code
	// does not actually make is the same defect one layer down.
	t.Run("the scan box does not hold it", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
		r = receiveTypeInto(t, r, "SKU-11") // the scan row is where a fresh form opens
		r = receiveGoToLine(t, r, s, 1)
		if !barHas(s.bar(), "Ctrl+K", "Close short") {
			t.Fatalf("a half-typed lookup code held the write-off gate: %+v", s.bar())
		}
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlK})
		if s.phase != phaseWriteOff {
			t.Errorf("ctrl+k did not open the confirm with only the scan box filled: phase %v", s.phase)
		}
		_ = r
	})

	// And the gate is a gate rather than a removal: clearing what holds it hands
	// the key back, named and acting.
	t.Run("clearing what holds it hands the key back", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
		r = receiveGoToLine(t, r, s, 0)
		r = receiveTypeInto(t, r, "4")
		r = receiveGoToLine(t, r, s, 1)
		if barHas(s.bar(), "Ctrl+R", "Mark received") {
			t.Fatalf("the bar names Ctrl+R over a typed quantity: %+v", s.bar())
		}
		s.qty[0].SetValue("")
		if !barHas(s.bar(), "Ctrl+R", "Mark received") {
			t.Fatalf("clearing the box did not hand Ctrl+R back: %+v", s.bar())
		}
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
		if s.phase != phaseWriteOff {
			t.Errorf("ctrl+r did not open the confirm with the form clear: phase %v", s.phase)
		}
		_ = r
	})
}

// TestReceiveFlow_TheScanHintCountsLinesEnterCanReach.
//
// The hint counted lines carrying a code over the WORKSHEET — settled ones
// included — beside the promise "enter jumps to its line". A settled line keeps
// its scan_codes, and Enter does not jump to one: findLine's live filter leaves
// the cursor where it was and says which settled line the code hit. So an order
// of one live asset line and one closed-short coded line drew "1 of 2 lines can
// be scanned to" when no scan on it could reach a quantity box at all.
//
// The two nothings stay apart, which is why this drives both: an order carrying
// no code is a different fact, and a different next move, from an order whose
// codes are all on settled lines.
func TestReceiveFlow_TheScanHintCountsLinesEnterCanReach(t *testing.T) {
	settled := func(id int, label string) omsapi.ReceivingLine {
		l := receiveWSLine(id, label, 6, 2)
		l.IsClosedShort, l.IsSettled = true, true
		l.ReceiptState, l.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
		l.QuantityPending = 0
		return l
	}

	t.Run("codes only on settled lines", func(t *testing.T) {
		asset := receiveWSLine(31, "Bench lathe", 1, 0)
		asset.ScanCodes = nil
		fake := &receiveFake{}
		_, s := receiveDrive(t, fake, []omsapi.ReceivingLine{asset, settled(32, "Backordered gasket")}, 80, 30)

		text := receivePaneText(s, 80, 30)
		if strings.Contains(text, "enter jumps to its line") {
			t.Errorf("the hint promises a jump no scan on this order can make:\n%s", text)
		}
		if !strings.Contains(text, "Only settled lines here carry a code") {
			t.Errorf("the hint does not say which kind of nothing this is:\n%s", text)
		}
	})

	// A way out that cannot be taken is worse than none. On an order every line
	// of which is settled, applyWorksheet leaves s.lines empty, qtyBody still
	// draws the scan row, and "pick the line with up/dn" then points at a picker
	// with nothing in it: up/dn walks the fixed rows and reaches no line at all.
	// Both nothing-branches carried that tail. The way out there is the ORDER's,
	// and it is the sentence qtyBody's own empty branch already gives.
	t.Run("nothing receivable names the order's way out, not a picker", func(t *testing.T) {
		coded := settled(35, "Backordered gasket")
		bare := settled(36, "Cancelled bracket")
		bare.ScanCodes = nil
		for _, lines := range [][]omsapi.ReceivingLine{{bare, coded}, {bare}} {
			fake := &receiveFake{}
			_, s := receiveDrive(t, fake, lines, 80, 30)
			if len(s.lines) != 0 {
				t.Fatalf("the fixture left %d receivable lines, so this is not the state "+
					"it names", len(s.lines))
			}
			text := receivePaneText(s, 80, 30)
			if strings.Contains(text, "up/dn") {
				t.Errorf("the scan hint points at a picker with no line in it:\n%s", text)
			}
			if !strings.Contains(text, "void or cancel the ORDER") {
				t.Errorf("the scan hint does not name the way out that exists:\n%s", text)
			}
		}
	})

	t.Run("a coded live line is counted and a settled one is not", func(t *testing.T) {
		fake := &receiveFake{}
		lines := []omsapi.ReceivingLine{
			receiveWSLine(33, "Box of M3 bolts", 4, 0), settled(34, "Backordered gasket"),
		}
		_, s := receiveDrive(t, fake, lines, 80, 30)

		// One live line, and it is the only one the form draws a box for.
		if len(s.lines) != 1 {
			t.Fatalf("the fixture built %d receivable lines, want 1", len(s.lines))
		}
		text := receivePaneText(s, 80, 30)
		if want := fmt.Sprintf("%d of %d lines can be scanned to", 1, len(s.lines)); !strings.Contains(text, want) {
			t.Errorf("the hint does not count over the lines the form draws (%q):\n%s", want, text)
		}
		if strings.Contains(text, "of 2 lines can be scanned to") {
			t.Errorf("the hint counts the settled line Enter cannot jump to:\n%s", text)
		}
	})
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

// TestReceiveFlow_TheSummaryDoesNotCountUNITSUNDERTheWordLINES.
//
// The summary is the frame an operator reads after stock has moved, and it used
// to draw one row for two different nouns:
//
//	Lines ....... 0 outstanding · 0 received of 9
//
// on a THREE-line order. `total_received_quantity` and `total_quantity` are
// UNITS; only `outstanding_line_count` is lines — but a row of three numbers
// under one label reads as three of that label, and the operator came away
// believing the order had nine lines. Every figure was the server's and
// correct; the word over them was not.
//
// po_detail.go is the sibling screen for the same order and already says Lines
// for TotalItems and Quantity for the two totals, so the split here is taken
// from there rather than invented: the same word has to mean the same thing on
// both screens or the operator learns it twice.
//
// Asserted against the CLIPPED pane at 80 columns, because these are jdeValue
// rows and a value row cannot fold — a row that overran would be cut, and a cut
// number is worse than an absent one.
func TestReceiveFlow_TheSummaryDoesNotCountUNITSUNDERTheWordLINES(t *testing.T) {
	lines := receiveOrder() // three lines, nine units ordered across them
	fake := &receiveFake{replyWith: map[string]any{
		"id": 5, "po_number": "PO-1001", "status": "sent", "status_label": "Sent",
		"total_items": len(lines), "total_quantity": 9, "total_received_quantity": 2,
		"outstanding_line_count": 1,
	}}
	r, s := receiveDrive(t, fake, lines, 80, 30)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
	if s.phase != phaseDone {
		t.Fatalf("the write did not reach the summary: phase %v", s.phase)
	}

	// The row leaders are the columnar layer's, so the rows are read as whole
	// pane LINES rather than by substring: a Lines row that had swallowed the
	// quantities again would still contain "3".
	lineRow := receiveSummaryRow(t, s, "Lines")
	qtyRow := receiveSummaryRow(t, s, "Quantity")

	if !strings.Contains(lineRow, "3") || !strings.Contains(lineRow, "1 outstanding") {
		t.Errorf("the Lines row does not report the LINE counts: %q", lineRow)
	}
	// The two unit totals may not appear under Lines at all — that is the whole
	// defect, and "9" under a line count is what an operator misreads.
	for _, unit := range []string{"9", "received"} {
		if strings.Contains(lineRow, unit) {
			t.Errorf("the Lines row still carries a UNIT figure (%q): %q", unit, lineRow)
		}
	}
	if !strings.Contains(qtyRow, "9") || !strings.Contains(qtyRow, "received 2") {
		t.Errorf("the Quantity row does not report the UNIT counts: %q", qtyRow)
	}
	_ = r
}

// TestReceiveFlow_TheSummarySaysSoWhenTheServerSentNoLineTotal.
//
// TotalItems is `omitempty`, so "the reply carried no line total" and "the
// order has no lines" arrive identically as 0 — and this screen may not turn
// the first into the second. The receiving roll-up always sends the outstanding
// count, so that is what the row falls back to.
func TestReceiveFlow_TheSummarySaysSoWhenTheServerSentNoLineTotal(t *testing.T) {
	fake := &receiveFake{replyWith: map[string]any{
		"id": 5, "po_number": "PO-1001", "status": "sent", "status_label": "Sent",
		"total_quantity": 9, "total_received_quantity": 0,
		"outstanding_line_count": 0,
	}}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})

	row := receiveSummaryRow(t, s, "Lines")
	if !strings.Contains(row, "none outstanding") {
		t.Errorf("the Lines row does not report the count the reply did carry: %q", row)
	}
	if strings.Contains(row, "0 ·") {
		t.Errorf("an absent line total was drawn as a count of zero: %q", row)
	}
	_ = r
}

// receiveSummaryRow is the summary's row for a label, taken from the CLIPPED
// pane so a row the terminal cut cannot pass as a row that fits.
func receiveSummaryRow(t *testing.T, s *ReceiveFormScreen, label string) string {
	t.Helper()
	want := label + jdeLeader
	for _, line := range receivePaneLines(s, 80, 30) {
		if strings.Contains(line, want) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("the summary draws no %q row:\n%s", label,
		strings.Join(receivePaneLines(s, 80, 30), "\n"))
	return ""
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
	if !barHas(s.bar(), "r", "Re-read") {
		t.Errorf("the frame offers no way to try again: %+v", s.bar())
	}
}

// TestReceiveFlow_TheExpiredSessionSentenceNamesNoKeyTheFrameRefuses.
//
// The 401 sentence is the ONE failure detail this screen composes rather than
// relays, and it used to end "then r re-reads the worksheet" — reasoning about
// the blocked frame while being shared by all three failure paths. Only two of
// six phases bind `r`:
//
//   - on the REVIEW (a 401 on the receipt) `r` answers "r does nothing here",
//     so the frame's own diagnostic named a key the frame refuses; and
//   - on the WRITE-OFF CONFIRM (a 401 on the close-short or the mark-received)
//     `r` falls through to the Reason box, so following the instruction types a
//     letter into the free text that gets recorded against the line — the
//     screen altering what the operator is about to commit.
//
// The three cases below are the three call sites of receiveFailure crossed with
// the phase each leaves the screen on, which is the whole set of frames this
// sentence can be drawn on.
func TestReceiveFlow_TheExpiredSessionSentenceNamesNoKeyTheFrameRefuses(t *testing.T) {
	const denied = `{"detail":"Authentication credentials were not provided."}`
	enter := tea.KeyMsg{Type: tea.KeyEnter}

	cases := []struct {
		name  string
		phase receivePhase
		fake  func() *receiveFake
		reach func(t *testing.T, r Root, s *ReceiveFormScreen) Root
	}{
		{"the worksheet fetch", phaseBlocked, func() *receiveFake {
			return &receiveFake{sheet: receiveWorksheet(receiveOrder()...),
				sheetFail: http.StatusUnauthorized, sheetBody: denied}
		}, func(t *testing.T, r Root, s *ReceiveFormScreen) Root { return r }},
		{"the receipt", phaseReview, func() *receiveFake {
			return &receiveFake{failWith: http.StatusUnauthorized, failBody: denied}
		}, func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			r = receiveGoToLine(t, r, s, 0)
			r = receiveTypeInto(t, r, "1")
			r = receiveKey(t, r, enter) // -> review
			return receiveKey(t, r, enter)
		}},
		{"the write-off", phaseWriteOff, func() *receiveFake {
			return &receiveFake{failWith: http.StatusUnauthorized, failBody: denied}
		}, func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, s := receiveDrive(t, c.fake(), receiveOrder(), 80, 30)
			r = c.reach(t, r, s)
			if s.phase != c.phase {
				t.Fatalf("the 401 left the screen on phase %v, want %v", s.phase, c.phase)
			}
			if s.failDetail == "" {
				t.Fatalf("no failure detail is standing, so this case checks nothing")
			}
			// The fact and the next move, which are true on every frame.
			if !strings.Contains(s.failDetail, "no longer signed in") {
				t.Errorf("the 401 is relayed rather than named: %q", s.failDetail)
			}
			if pane := receivePaneText(s, 80, 30); !strings.Contains(pane, "sign in again") {
				t.Errorf("the next move does not survive the clip:\n%s", pane)
			}
			// And NO key beyond what this frame's bar names. The vocabulary is
			// receiveBarKeyNames — the same map the bar sweeps read — so a key
			// spelling added there is checked for here without anyone
			// remembering to.
			named := receiveNamedKeys(t, s.bar())
			for _, word := range strings.Fields(strings.Trim(s.failDetail, ".,")) {
				word = strings.Trim(word, ".,;:")
				if !receiveIsAKeyName(word) || named[word] {
					continue
				}
				t.Errorf("the failure detail names %q, which this frame's bar does not "+
					"(bar: %+v): %q", word, s.bar(), s.failDetail)
			}
			_ = r
		})
	}
}

// receiveIsAKeyName reports whether a word in prose is the name of a keystroke,
// against the vocabulary the bar sweeps already read. Derived rather than
// listed, so a bar entry added tomorrow makes its keystroke checkable in prose
// too.
func receiveIsAKeyName(word string) bool {
	for _, keys := range receiveBarKeyNames {
		for _, k := range keys {
			if word == k {
				return true
			}
		}
	}
	return false
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
