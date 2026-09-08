package tui

import (
	"context"
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
		for _, w := range []int{12, 20, 40, 51} {
			got := failDetailLines(strings.Repeat("y", 4000), w, rows)
			for i, line := range got {
				if c := lipgloss.Width(line); c > w {
					t.Errorf("at width %d line %d is %d cells: %q — the row saying "+
						"something was cut may not be the row that runs off the pane",
						w, i, c, line)
				}
			}
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
	pane := receivePaneText(s, 80, 30)
	if !strings.Contains(pane, "more of the error") && !strings.Contains(pane, "…") {
		t.Errorf("the cut failure body carries no mark, so it reads as a finished "+
			"sentence:\n%s", pane)
	}
}
