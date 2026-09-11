package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/uid0/scantty/internal/omsapi"
)

// A failure body that was cut says so.
//
// The rule is report_table.go's, in its own words: "an operator reading folded
// lines of a gateway page with nothing saying a tail went cannot tell they are
// missing the sentence that says what actually failed". po_add_line.go's copy
// had lost it — it folded and broke at the row limit — so on the reported
// decode failure the add-line screen drew
//
//	oms: http 0: decode response: json: cannot unmarshal number into Go
//	  struct field POLineLookupOrder.purchase_order.id of type
//
// and stopped, one word before the word that names the type, reading as a
// finished sentence.

// The helper itself, over BOTH cuts, because they know different things: the
// fold can count what it left, the prefix bound has already thrown it away.
func TestFailDetail_ACutBodyAlwaysCarriesItsMark(t *testing.T) {
	const width, rows = 40, 3

	t.Run("fits", func(t *testing.T) {
		got := failDetailLines("short enough", width, rows)
		if len(got) != 1 || strings.Contains(got[0], "…") {
			t.Errorf("an uncut body was marked anyway: %q — a mark nobody earned "+
				"claims a cut that was never made", got)
		}
	})

	t.Run("cut by the fold", func(t *testing.T) {
		// Must fold to MORE than rows lines while staying inside rows*width
		// characters, or cellPrefix bites first and this measures the other cut.
		// Four 25-cell words: no two share a 40-cell line, so it folds to four
		// lines out of 103 characters against a 120-character bound.
		body := strings.Join([]string{
			strings.Repeat("a", 25), strings.Repeat("b", 25),
			strings.Repeat("c", 25), strings.Repeat("d", 25),
		}, " ")
		if len(body) > rows*width {
			t.Fatalf("fixture is %d chars, past the %d-char prefix bound: it would "+
				"measure the wrong cut", len(body), rows*width)
		}
		got := failDetailLines(body, width, rows)
		if len(got) != rows {
			t.Fatalf("rows = %d, want %d — the mark spends one of the block's own "+
				"rows and must not grow it", len(got), rows)
		}
		last := got[len(got)-1]
		if !strings.HasPrefix(last, "…") || !strings.Contains(last, "more line(s)") {
			t.Errorf("last line = %q, want the counted mark: the fold knows how many "+
				"lines it left", last)
		}
	})

	t.Run("cut by the prefix bound", func(t *testing.T) {
		// Unspaced, so pickerWrap cannot fold it and cellPrefix is what bites —
		// the shape a minified gateway page arrives in.
		got := failDetailLines(strings.Repeat("x", rows*width*4), width, rows)
		if len(got) != rows {
			t.Fatalf("rows = %d, want %d", len(got), rows)
		}
		last := got[len(got)-1]
		if !strings.HasPrefix(last, "…") || strings.Contains(last, "more line(s)") {
			t.Errorf("last line = %q, want the UNCOUNTED mark: the prefix bound has "+
				"thrown the rest away, so a count there would count the prefix", last)
		}
	})

	t.Run("the mark itself fits the pane", func(t *testing.T) {
		const whole = "… more of the error than this pane can hold"
		cut := 0
		for _, w := range []int{12, 20, 40, 51} {
			got := failDetailLines(strings.Repeat("y", 4000), w, rows)
			for i, line := range got {
				if c := lipgloss.Width(line); c > w {
					t.Errorf("at width %d line %d is %d cells: %q — the row saying "+
						"something was cut may not be the row that runs off the pane",
						w, i, c, line)
				}
			}
			// And where that bound bites, it says so like every other bound:
			// cellPrefix alone drew `… more of the erro` below a 43-cell block.
			last := got[len(got)-1]
			if lipgloss.Width(whole) > w {
				cut++
				if !strings.HasSuffix(last, "…") {
					t.Errorf("at width %d the mark row is %q — clipped, and not saying so", w, last)
				}
			} else if last != whole {
				t.Errorf("at width %d the mark row is %q, want it whole: %q", w, last, whole)
			}
		}
		if cut == 0 {
			t.Fatalf("no width in the table is narrower than the mark, so its clip is untested")
		}
	})
}

// And on the frame it was reported on, driven through the real screen: the
// pane an operator would be looking at must carry the mark.
func TestPOAddLine_ACutFailureBodySaysSoOnThePane(t *testing.T) {
	// A 502 whose body is far more than three folded rows. poAddFake.fail
	// already serves exactly this shape — omsapi.parseError puts the whole raw
	// payload into APIError.Message when the envelope carries no code.
	fake := &poAddFake{rows: poAddRows(), fail: true}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	po := &omsapi.PurchaseOrder{ID: 1, Number: "PO-2026-0042", Status: "draft",
		SupplierDetails: "Acme Fasteners & Industrial Supply Co."}
	s := NewPurchaseOrderAddLineScreen(deps, po)
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	r = next.(Root)

	r = key(t, r, poRuneKey("AF-77"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	// The operator is told it could not tell, and which keys act — that half
	// was already right and is asserted so a fix here cannot cost it.
	poAddWantPane(t, r, "could not tell")
	poAddWantPane(t, r, "enter tries again")
	// And the body they are shown says it is not all of it.
	poAddWantPane(t, r, "more of the error")
}

// A ONE-ROW block keeps the head of the error and marks the cut in it.
//
// The arithmetic above spends the LAST of the block's rows on the mark, and at
// rows == 1 that left `keep[:0]` — the mark ALONE, a block telling the operator
// a tail went while showing none of the head, which is the rule this function
// exists to enforce read backwards. It is not a corner: receive_form.go's
// headerSplit deliberately pays the failure detail a floor of exactly one row on
// a short pane, because the reason is what decides whether the operator retypes
// a quantity or goes and fetches somebody.
func TestFailDetail_AOneRowBlockKeepsTheHeadAndMarksTheCut(t *testing.T) {
	const width = 40
	// Both cuts, because they take different branches above: the unspaced body
	// is stopped by cellPrefix, the spaced one folds past the row limit.
	for _, c := range []struct{ name, body string }{
		{"cut by the prefix bound", strings.Repeat("x", width*8)},
		{"cut by the fold", strings.Join([]string{
			strings.Repeat("a", 25), strings.Repeat("b", 25)}, " ")},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := failDetailLines(c.body, width, 1)
			if len(got) != 1 {
				t.Fatalf("rows = %d, want 1 — the block may not grow past what it "+
					"was budgeted: %q", len(got), got)
			}
			if strings.HasPrefix(got[0], "…") {
				t.Errorf("the only row is the mark: %q — the operator is told a tail "+
					"went and shown none of the head", got[0])
			}
			if !strings.HasPrefix(got[0], c.body[:4]) {
				t.Errorf("the row does not lead with the error: %q, want the head of %q",
					got[0], c.body[:20])
			}
			if !strings.HasSuffix(got[0], "…") {
				t.Errorf("the row reads as a finished sentence: %q — a truncated value "+
					"that does not say so is the whole of rule 6", got[0])
			}
			if c := lipgloss.Width(got[0]); c > width {
				t.Errorf("the row is %d cells against a pane of %d: %q", c, width, got[0])
			}
		})
	}

	t.Run("uncut at one row", func(t *testing.T) {
		got := failDetailLines("short", width, 1)
		if len(got) != 1 || got[0] != "short" {
			t.Errorf("an uncut body was marked or dropped: %q", got)
		}
	})
}

// And on the RECEIVING frame, driven through the real screen. This was the
// fourth copy of the fold-cut-and-mark and it was still unmarked after the other
// three had been converted — while the shared helper's own comment claimed there
// were three of them.
//
// All four receiving endpoints hand-write {"error": ...} and so miss DRF's
// exception handler, which is why omsapi.parseError puts the ENTIRE raw payload
// into APIError.Message: the body below is the gateway page that arrives when
// the receipt never reaches OMS at all.
func TestReceive_ACutFailureBodySaysSoOnThePane(t *testing.T) {
	fake := &receiveFake{
		sheet:    receiveWorksheet(receiveOrder()...),
		failWith: http.StatusBadGateway,
		failBody: "<!DOCTYPE html><html><head><title>502 Bad Gateway</title></head>" +
			"<body><center><h1>502 Bad Gateway</h1></center><hr>" +
			"<center>nginx/1.24.0</center><p>The upstream server did not answer in " +
			"time. The receipt was not recorded and no stock was credited.</p></body></html>",
	}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 30)
	r = receiveGoToLine(t, r, s, 0)
	r = receiveTypeInto(t, r, "1")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // post, which 502s
	_ = r

	if s.failDetail == "" {
		t.Fatalf("no failure detail is standing, so this checks nothing")
	}
	if len(s.failDetail) <= s.failDetailRows()*(s.paneWidth()-len(jdeIndent)) {
		t.Fatalf("the fixture body is %d chars and fits the block, so no cut is made "+
			"and the assertion below is vacuous", len(s.failDetail))
	}
	// The multi-row mark, and ONLY it. An earlier version accepted an ellipsis
	// anywhere on the flattened pane as evidence, which any clipped label, a
	// fittedNote that gave up words or a shortened status row satisfies — it
	// failed correctly then only because this fixture's labels happen to fit at
	// 80x30, so a longer name would have made it pass with the mark deleted.
	// The one-row ellipsis form is the unit test's above; at this budget the
	// block gets more than one row, so the counted wording is the one under test.
	if rows := s.failDetailRows(); rows < 2 {
		t.Fatalf("the block has %d row(s) here, which is the one-row ellipsis form "+
			"rather than the mark this asserts", rows)
	}
	pane := receivePaneText(s, 80, 30)
	if !strings.Contains(pane, "more of the error") {
		t.Errorf("the cut failure body carries no mark, so it reads as a finished "+
			"sentence:\n%s", pane)
	}
}

// The helper's EMPTY contract, which the four call sites depend on.
//
// failDetailLines returns NOTHING for a detail it cannot draw a single line of,
// and every caller ranges over the result rather than indexing it. That was left
// implicit once and one of the four sites diverged: report_table.go read
// lines[0] and lines[1:], so the frame drawn when a report load has FAILED was
// the one frame in the package carrying a panic in View() — which takes the
// terminal down rather than drawing a wrong pane. It was unreachable only
// because of three floors owned by three other places, none visible from that
// call site.
//
// This pins the empty answer so the contract cannot be "fixed" into
// manufacturing a line: a mark with no content beneath it is exactly the state
// the helper exists to forbid, so inventing a row here would put it back.
func TestFailDetail_ADetailItCannotDrawReturnsNothing(t *testing.T) {
	cases := []struct {
		name        string
		detail      string
		width, rows int
	}{
		{"no detail", "", 40, 3},
		{"no rows", "something failed", 40, 0},
		{"negative rows", "something failed", 40, -1},
		{"no width", "something failed", 0, 3},
		{"negative width", "something failed", -1, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := failDetailLines(c.detail, c.width, c.rows); len(got) != 0 {
				t.Errorf("failDetailLines(%q, %d, %d) = %q, want nothing — a caller "+
					"that ranges draws nothing here, and a manufactured line would be "+
					"a mark with no content under it",
					c.detail, c.width, c.rows, got)
			}
		})
	}
}

// A short pane RE-DRAWS the failure block rather than cutting its mark off.
//
// failDetailLines spends the LAST of the block's rows on the mark, and the
// block is a context row of a pinned header — where jdeFitHeader gives ground
// from the END. So a pane too short for all three rows took the MARK first and
// kept the head of the gateway page above it: the add-line screen drew
// `oms: http 502: <!DOCTYPE` alone under the failure headline at 80x17, and the
// New PO review drew `oms: http 502: <!DOCTYPE html>` at 80x14, each reading as
// the complete reason. Both sites already called the shared helper and both
// unit tests of it passed — the helper marked the cut, and the layer then cut
// the mark. Only the rendered pane at a height below the block's full budget
// could see it, and every existing check measured 80x24 and 80x30.
//
// Measured at every drawable height and at every width the columnar layer is
// honest about (receiveHonestWidths records the 45–48 band, where the layer's
// floored bodyWidth lets clampToBox cut every row of every columnar screen, as
// the layer's own defect rather than this one's).
func TestFailDetail_AShortPaneRedrawsTheBlockRatherThanCuttingItsMark(t *testing.T) {
	t.Run("add a line", func(t *testing.T) {
		fake := &poAddFake{rows: poAddRows(), fail: true}
		srv := httptest.NewServer(fake.handler())
		defer srv.Close()
		deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
		po := &omsapi.PurchaseOrder{ID: 1, Number: "PO-2026-0042", Status: "draft",
			SupplierDetails: "Acme Fasteners & Industrial Supply Co."}
		s := NewPurchaseOrderAddLineScreen(deps, po)
		r := newTestRoot(s)
		r.deps = deps
		next, _ := r.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		r = next.(Root)
		r = key(t, r, poRuneKey("AF-77"))
		r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		_ = r
		if s.failDetail == "" {
			t.Fatalf("the lookup did not fail, so there is no block to measure")
		}
		failBlockKeepsItsMark(t, s, s.failLines)
	})

	t.Run("new purchase order", func(t *testing.T) {
		_, s := poSubmitFailure(t, poGatewayHTML, 30)
		failBlockKeepsItsMark(t, s, func() []string { return s.failDetailIn(poFailDetailRows) })
	})

	// The third caller that pins the block in a header. It never used the
	// fixed budget the other two did — headerSplit asks the pane first — so it
	// is here to hold that the other route reaches the same answer, not because
	// it was reported. Its block as BUILT is already the pane's, so the header
	// never cuts into it and the trim guard does not apply.
	t.Run("receiving", func(t *testing.T) {
		fake := &receiveFake{
			sheet:    receiveWorksheet(receiveOrder()...),
			failWith: http.StatusBadGateway,
			failBody: poGatewayHTML,
		}
		r, s := receiveDrive(t, fake, receiveOrder(), 120, 40)
		r = receiveGoToLine(t, r, s, 0)
		r = receiveTypeInto(t, r, "1")
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review
		_ = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // post, which 502s
		if s.failDetail == "" {
			t.Fatalf("the receipt did not fail, so there is no block to measure")
		}
		failBlockKeepsItsMarkOn(t, s, s.failDetailLines, false)
	})
}

// failBlockKeepsItsMark resizes s through every honest pane and fails wherever
// the head of the failure body is drawn with no mark beside it. `built` is the
// block as the screen BUILDS it at the current size, before any header trim.
//
// The fixture body must be cut at every pane or "no mark" is not a defect, so
// that is checked rather than assumed; and the sweep must reach panes where the
// header really did cut into the block, or it proved nothing about the trim —
// counted, and fatal when zero.
func failBlockKeepsItsMark(t *testing.T, s Screen, built func() []string) {
	t.Helper()
	failBlockKeepsItsMarkOn(t, s, built, true)
}

// failBlockKeepsItsMarkOn is failBlockKeepsItsMark with the trim guard optional,
// for a caller whose block as built is already sized to the pane.
func failBlockKeepsItsMarkOn(t *testing.T, s Screen, built func() []string, wantTrim bool) {
	t.Helper()
	var drawn, trimmed int
	var bad []string
	for _, w := range receiveHonestWidths() {
		for _, h := range jdePaneHeights() {
			s = jdeAtPane(s, w, h)
			whole := stripANSI(strings.Join(built(), "\n"))
			if whole == "" {
				// A caller that sizes the block to the pane can give it nothing
				// (receiving's headerSplit, on the shortest panes): nothing is
				// drawn, so there is no fragment to mark.
				continue
			}
			if !failBlockCarriesAMark(whole) {
				t.Fatalf("at %dx%d the block as built is not cut, so a pane drawing it "+
					"unmarked would be right and this sweep could not fail:\n%s", w, h, whole)
			}
			lines := strings.Split(stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))), "\n")
			head := -1
			for i, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "oms: http 502:") {
					head = i
					break
				}
			}
			if head < 0 {
				continue
			}
			drawn++
			// The block's rows on the pane: the head, then every row that is a
			// fold continuation of it (pickerWrap's two-cell indent under the
			// block's own) or its mark row. Matched EXACTLY, because a field row
			// below the block is right-aligned into the label column and so also
			// opens with spaces — counted as the block, it would hide a trim.
			rows := 1
			for _, line := range lines[head+1:] {
				cont := strings.HasPrefix(line, jdeIndent+"  ") &&
					!strings.HasPrefix(line, jdeIndent+"   ")
				if !cont && !strings.HasPrefix(line, jdeIndent+"…") {
					break
				}
				rows++
			}
			if rows < len(built()) {
				trimmed++
			}
			block := strings.Join(lines[head:head+rows], "\n")
			if !failBlockCarriesAMark(block) {
				bad = append(bad, fmt.Sprintf("%dx%d:\n%s", w, h, block))
			}
		}
	}
	if drawn == 0 {
		t.Fatalf("the failure body was never on the pane, so nothing was measured")
	}
	if wantTrim && trimmed == 0 {
		t.Fatalf("no pane drew the block at fewer rows than it was built with, so the " +
			"header never cut into it and the sweep says nothing about the trim")
	}
	t.Logf("the body was on %d pane(s), %d of them drawing the block cut by the header", drawn, trimmed)
	if len(bad) > 0 {
		t.Errorf("%d of %d pane(s) draw the head of a cut error body with nothing saying "+
			"more of it exists, e.g. %s", len(bad), drawn, bad[0])
	}
}

// failBlockCarriesAMark reports whether a failure block, as drawn, says it is
// not the whole error: the multi-row form's MARK ROW, which leads with the
// ellipsis in both of its wordings, or the one-row form's ellipsis on the head.
// Matched on the ellipsis rather than on the mark's words because at a narrow
// pane the mark row is itself clipped, and a check keyed on "more of the error"
// would report that clip as a missing mark. Nothing else in the block carries
// one: pickerWords breaks an over-long token without marking it, because the
// rest of it is the next row down.
func failBlockCarriesAMark(block string) bool {
	lines := strings.Split(block, "\n")
	for i, line := range lines {
		tr := strings.TrimSpace(line)
		if strings.HasPrefix(tr, "…") || (i == 0 && strings.HasSuffix(tr, "…")) {
			return true
		}
	}
	return false
}
