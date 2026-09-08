package tui

import (
	"context"
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
