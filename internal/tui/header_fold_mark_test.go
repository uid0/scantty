package tui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/uid0/scantty/internal/omsapi"
)

// A value folded across pinned-header rows is drawn WHOLE, or not at all, or
// with its cut marked — never as a fragment reading as the whole.
//
// jdeFitHeader gives ground from the END within a rank, so a sentence folded
// across four context rows lost its tail rows first and what was left ended on
// a whole word, which is what a finished sentence looks like. On the add-line
// screen's failure frame that was the answer to the keypress: at 80x11 the pane
// said "✗ could not tell whether Acme Fasteners &" and nothing else. The void
// prompts' caveats lost the same way — the order void's "This cannot be undone"
// is its last clause. Those blocks are fitted now (jdeHeader.addFitted), and a
// short pane re-draws them into the rows it has with foldKeepRows' ellipsis.
//
// The VALUES are read off each screen's own content functions rather than
// written here, so a reworded note is measured as it is drawn. The frames are
// every columnar case jdePaneCases derives, plus the add-line failure frame,
// which needs a failed lookup no case builds; each is swept at every honest
// width and every drawable height. headerFoldValues names what each screen TYPE
// folds into its header — the builders that fold are the ones that call
// addFitted, which is where to look when a new one is added.
func TestHeaderFold_AFoldedValueIsWholeAbsentOrMarked(t *testing.T) {
	type frame struct {
		name string
		mk   func() Screen
	}
	frames := []frame{{"PurchaseOrderAddLineScreen/lookup failed", func() Screen {
		return headerFoldAddLineFailure(t)
	}}}
	for _, c := range jdePaneCases() {
		// A screen type that folds nothing into its header has nothing here to
		// measure, and walking it at every pane is time the package's 600s
		// budget does not have (AGENTS.md).
		if len(headerFoldValues(c.mk())) > 0 {
			frames = append(frames, frame{c.name, c.mk})
		}
	}
	cut := map[string]int{}
	for _, f := range frames {
		var bad []string
		found := 0
		s := f.mk()
		for _, w := range receiveHonestWidths() {
			for _, h := range jdePaneHeights() {
				s = jdeAtPane(s, w, h)
				lines := strings.Split(stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))), "\n")
				for _, v := range headerFoldValues(s) {
					on, whole, marked := foldedValueOnPane(lines, v)
					if !on {
						continue
					}
					found++
					if !whole {
						cut[strings.SplitN(f.name, "/", 2)[0]]++
					}
					if !whole && !marked {
						bad = append(bad, fmt.Sprintf("%dx%d draws a fragment of %q", w, h, v))
					}
				}
			}
		}
		if len(bad) > 0 {
			t.Errorf("%s: %d pane(s) draw part of a folded value with nothing saying it is "+
				"part, e.g. %s", f.name, len(bad), bad[0])
		}
		_ = found
	}
	// The sweep must reach the state it is about on the screens it was written
	// for, or it passes by never cutting anything.
	for _, screen := range []string{"PurchaseOrderAddLineScreen", "PurchaseOrderDetailScreen",
		"PurchaseOrderEditScreen", "PurchaseOrderCreateScreen", "InventoryItemFormScreen",
		"StorageSlotGenerateScreen", "AssetDocumentsScreen", "AssetMetersScreen",
		"AssetMeterReadingsScreen", "WorkOrderScanReviewScreen"} {
		if cut[screen] == 0 {
			t.Errorf("%s: no pane drew a folded header value short of whole, so the "+
				"trim was never exercised there", screen)
		}
	}
}

// headerFoldAddLineFailure is the add-line screen standing on a failed lookup,
// its note folded across several rows at every width.
func headerFoldAddLineFailure(t *testing.T) Screen {
	t.Helper()
	fake := &poAddFake{rows: poAddRows(), fail: true}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	po := &omsapi.PurchaseOrder{ID: 1, Number: "PO-2026-0042", Status: "draft",
		SupplierDetails: "Acme Fasteners & Industrial Supply Co."}
	s := NewPurchaseOrderAddLineScreen(deps, po)
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	r = next.(Root)
	r = key(t, r, poRuneKey("AF-77"))
	_ = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.note.text == "" || s.failDetail == "" {
		t.Fatalf("the lookup did not fail into a note: %q", s.note.text)
	}
	return s
}

// headerFoldValues is every value the screen folds into its pinned header, as
// plain text, asked of the screen's own content functions.
//
// THE FOUR ASSET/SCAN SCREENS JOINED IT AFTER THE FACT, and how they were
// missed is the lesson: each folded its caveat correctly with jdeCaveatLines and
// then handed the result to addBlock, which adds INDEPENDENT rows — so the fold
// was right and the row-by-row trim under it was the very defect addFitted
// exists for. Folding a value is not the same as telling the layer it IS one
// value. Measured before the fix, at 80 columns and drawable heights: 69 panes
// on the supersede confirm, 54 on the meter adjust, 51 on the new meter, 47 on
// the scan review and 2 on the reading grid, each drawing a fragment that ended
// on a whole word.
func headerFoldValues(s Screen) []string {
	switch v := s.(type) {
	case *PurchaseOrderAddLineScreen:
		return []string{v.note.text}
	case *PurchaseOrderCreateScreen:
		if len(v.answerRows()) > 0 {
			return []string{v.answerNote().text}
		}
		if len(v.standingRows()) > 0 {
			return []string{v.standingNote()}
		}
	case *PurchaseOrderDetailScreen:
		return []string{poVoidOrderCaveat}
	case *PurchaseOrderEditScreen:
		return append(v.voidCaveats(v.bodyWidth()), voidStandingNote)
	case *InventoryItemFormScreen:
		unit := v.baseUnitValue()
		return []string{chainGuidance(unit), chainEmptyDetail(unit), kitListGuidance}
	case *StorageSlotGenerateScreen:
		return []string{levelListDetail}
	case *AssetDocumentsScreen:
		return []string{supersedeCaveat}
	case *AssetMetersScreen:
		return []string{adjustCaveat, newMeterCaveat, meterDropNote}
	case *AssetMeterReadingsScreen:
		return []string{readingDropNote}
	case *WorkOrderScanReviewScreen:
		return []string{woScanImageCaveat}
	}
	return nil
}

// foldedValueOnPane finds value's first row on the pane and walks the rows
// under it for as long as they go on spelling it. It reports whether the value
// is drawn at all, whether all of it is, and — where it stops short — whether
// the last row it reached carries the ellipsis that says so.
//
// Compared WORD BY WORD, with the level mark and the " · " joints set aside:
// pickerWrap drops a joint at a fold, and the mark rides only the first row.
// A value is "drawn" once its first two words open a row, which is the test a
// reader applies too.
func foldedValueOnPane(lines []string, value string) (on, whole, marked bool) {
	want := headerFoldWords(value)
	if len(want) < 2 {
		return false, false, false
	}
	for i, line := range lines {
		got := headerFoldWords(line)
		if len(got) < 2 || got[0] != want[0] || got[1] != want[1] {
			continue
		}
		at := 0
		for _, row := range lines[i:] {
			toks := headerFoldWords(row)
			tr := strings.TrimSpace(stripANSI(row))
			if strings.HasSuffix(tr, "…") && len(toks) > 0 {
				last := strings.TrimSuffix(toks[len(toks)-1], "…")
				toks = toks[:len(toks)-1]
				if (len(toks) == 0 || headerFoldSpells(want[at:], toks)) && at+len(toks) < len(want) &&
					strings.HasPrefix(want[at+len(toks)], last) {
					return true, false, true
				}
				return true, false, false
			}
			if !headerFoldSpells(want[at:], toks) {
				return true, false, false
			}
			at += len(toks)
			if at == len(want) {
				return true, true, false
			}
		}
		return true, false, false
	}
	return false, false, false
}

func headerFoldSpells(want, toks []string) bool {
	if len(toks) == 0 || len(toks) > len(want) {
		return false
	}
	for i, tok := range toks {
		if want[i] != tok {
			return false
		}
	}
	return true
}

func headerFoldWords(s string) []string {
	var out []string
	for _, f := range strings.Fields(stripANSI(s)) {
		if f == "·" || f == "✗" || f == "!" || f == "✓" {
			continue
		}
		out = append(out, f)
	}
	return out
}

// jdeFitHeader asks a fitted block for EXACTLY the rows it kept and draws the
// answer in their place: whole when nothing was taken, absent when all of it
// was, re-drawn in between — and always exactly the rows the budget left, or
// the body windowed against the other half of that split would be drawn one row
// off.
func TestHeaderFold_TheTrimAsksAFittedBlockForTheRowsItKept(t *testing.T) {
	var asked []int
	refit := func(rows int) []string {
		asked = append(asked, rows)
		out := make([]string, rows)
		for i := range out {
			out[i] = fmt.Sprintf("refit %d/%d", i+1, rows)
		}
		return out
	}
	header := jdeHeader(nil).
		add(jdeHeadEssential, "essential").
		addFitted(jdeHeadContext, jdeHeadContext, []string{"fold 1", "fold 2", "fold 3"}, refit).
		add(jdeHeadContext, "after")
	const budget = 10
	for _, c := range []struct {
		keep  int
		want  []string
		asked []int
	}{
		// Nothing has to give.
		{5, []string{"essential", "fold 1", "fold 2", "fold 3", "after"}, nil},
		// "after" goes first (the last context row), and the block is untouched.
		{4, []string{"essential", "fold 1", "fold 2", "fold 3"}, nil},
		// Now the block's own tail: re-drawn at two rows, not cut.
		{3, []string{"essential", "refit 1/2", "refit 2/2"}, []int{2}},
		{2, []string{"essential", "refit 1/1"}, []int{1}},
		// All of it: absent, not a fragment, and refit is not asked for zero.
		{1, []string{"essential"}, nil},
	} {
		asked = nil
		got := jdeFitHeader(header, budget, budget-c.keep)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("keeping %d: got %q, want %q", c.keep, got, c.want)
		}
		if fmt.Sprint(asked) != fmt.Sprint(c.asked) {
			t.Errorf("keeping %d: refit was asked for %v, want %v", c.keep, asked, c.asked)
		}
	}

	// A refit that answers the wrong number of rows is held to the count.
	for _, n := range []int{0, 1, 5} {
		h := jdeHeader(nil).addFitted(jdeHeadContext, jdeHeadContext,
			[]string{"a", "b", "c"}, func(int) []string { return make([]string, n) })
		if got := jdeFitHeader(h, 10, 8); len(got) != 2 {
			t.Errorf("a refit answering %d row(s) for 2 left the header %d row(s) tall", n, len(got))
		}
	}
}

// A fitted block's FIRST row may outrank the rest — a note whose first line is
// the essential answer — and it is still the block's head that survives.
func TestHeaderFold_AFittedBlockKeepsItsLeadRow(t *testing.T) {
	h := jdeHeader(nil).addFitted(jdeHeadEssential, jdeHeadContext,
		[]string{"answer head", "answer tail 1", "answer tail 2"},
		func(rows int) []string {
			return foldKeepRows([]string{"answer head", "answer tail 1", "answer tail 2"}, rows, 40)
		})
	got := jdeFitHeader(h, 5, 4)
	if len(got) != 1 || got[0] != "answer head…" {
		t.Errorf("kept %q, want the head alone and marked", got)
	}
}

// foldKeepRows keeps what it can and marks the last kept row whenever it drops
// one, within the width the rows were folded to.
func TestHeaderFold_FoldKeepRowsMarksWhatItDrops(t *testing.T) {
	lines := []string{"twelve cells", "then more", "and the end"}
	if got := foldKeepRows(lines, 3, 12); strings.Join(got, "|") != strings.Join(lines, "|") {
		t.Errorf("a fold that fits was changed: %q", got)
	}
	got := foldKeepRows(lines, 1, 12)
	if len(got) != 1 || got[0] != "twelve cell…" {
		t.Errorf("got %q, want the first row cut to give a cell to the ellipsis", got)
	}
	if lipgloss.Width(got[0]) > 12 {
		t.Errorf("the marked row is %d cells against 12", lipgloss.Width(got[0]))
	}
	if got := foldKeepRows(lines, 2, 12); got[1] != "then more…" {
		t.Errorf("got %q, want the last kept row marked where it has room", got)
	}
	if got := foldKeepRows(lines, 0, 12); len(got) != 0 {
		t.Errorf("zero rows kept %q", got)
	}
	if lines[0] != "twelve cells" {
		t.Errorf("foldKeepRows wrote through to its caller's slice: %q", lines)
	}
}
