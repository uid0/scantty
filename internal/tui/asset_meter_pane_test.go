package tui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// A NUMBER IS SHOWN WHOLE OR DROPPED AND MARKED — never cut on the row.
//
// A cut figure reads as a different number rather than as a shortened one, and
// on these screens the number is the entire subject. The report table states the
// same rule for a fact column; this holds it for the two meter grids, at every
// pane Root draws rather than at a hand-picked pair — the width axis is where a
// value column dies, and three hand-picked widths is how the last hole survived.

// meterDigits is every run of digits (with an optional decimal point) drawn on a
// pane, so a cut value can be told from a whole one by comparison against what
// the fixture holds.
var meterDigits = regexp.MustCompile(`-?\d[\d.]*`)

// TestAssetMeters_EveryReadingOnThePaneIsAWholeNumber walks the meters grid at
// every drawable pane and fails on a figure that appears in a form the data does
// not contain — which is what a clipped `1289.75` (drawn `1289.7`, or `1289.`)
// looks like.
func TestAssetMeters_EveryReadingOnThePaneIsAWholeNumber(t *testing.T) {
	whole := map[string]bool{}
	for _, m := range assetMeterFixtureRows() {
		whole[meterValueText(m.CurrentValue)] = true
	}
	if len(whole) < 2 {
		t.Fatal("the fixture carries fewer than two distinct readings, so nothing here " +
			"could tell a clipped figure from a whole one")
	}

	var checked int
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := assetMetersFixture()
			s.setSize(tea.WindowSizeMsg{Width: w, Height: h})
			pane := assetFlatPane(s, w, h)
			if assetPaneRefused(pane) {
				continue
			}
			for _, got := range meterDigits.FindAllString(pane, -1) {
				// Row numbers, the count in "Meters (4)" and a year are all
				// short integers the fixture's readings never look like; what
				// this is hunting is a PREFIX of a real reading.
				for want := range whole {
					if got != want && strings.HasPrefix(want, got) && len(got) >= 3 {
						t.Fatalf("at %dx%d the pane draws %q, which is a PREFIX of the "+
							"reading %q — a cut number reads as a different number, so a "+
							"value that will not fit is dropped and marked instead:\n%s",
							w, h, got, want, pane)
					}
				}
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("the sweep drew no pane at all, so it measured nothing")
	}
}

// THE VALUE COLUMN IS PRESENT AT EVERY WIDTH ROOT DRAWS. The old mark-and-move
// fallback is below the 80-column contract and therefore cannot justify an
// exception in a sweep of reachable panes.
func TestAssetMeters_TheValueColumnKeepsTheFigureAtEveryDrawableWidth(t *testing.T) {
	heights := jdePaneHeights()

	var kept int
	for _, w := range jdeDrawableWidths() {
		for _, h := range heights {
			s := assetMetersFixture()
			s.setSize(tea.WindowSizeMsg{Width: w, Height: h})
			_, _, d := s.meterGridPlan()
			pane := assetFlatPane(s, w, h)
			if assetPaneRefused(pane) {
				continue
			}
			if !d {
				kept++
				continue
			}
			t.Fatalf("at drawable size %dx%d the value column was dropped:\n%s", w, h, pane)
		}
	}
	if kept == 0 {
		t.Error("the sweep visited no drawable pane")
	}
}

// EVERY VALUE IS SHOWN WITH ITS UNIT. A runtime-hour reading and a cycle count
// are not interchangeable, and an unlabelled number invites the wrong entry.
func TestAssetMeters_EveryFigureOnThePaneCarriesItsUnit(t *testing.T) {
	s := assetMetersFixture()
	s.setSize(tea.WindowSizeMsg{Width: 100, Height: 30})
	pane := assetFlatPane(s, 100, 30)
	for _, m := range assetMeterFixtureRows() {
		fig := meterFigure(m.CurrentValue, m.Unit, m.MeterTypeDisplay)
		if !strings.Contains(pane, fig) {
			t.Errorf("the grid does not draw %q (value and unit together):\n%s", fig, pane)
		}
	}
	// And the record form's own hint names the unit, so the box says what it is
	// asking for before anything is typed into it.
	s.openEntry(meterPhaseRecord)
	form := assetFlatPane(s, 100, 30)
	if !strings.Contains(form, "in hours") {
		t.Errorf("the record form does not say what unit the box wants:\n%s", form)
	}
	if !strings.Contains(form, "1289.75 hours") {
		t.Errorf("the record form does not say what the meter reads now, with its unit:\n%s", form)
	}
}

// THE LEDGER'S TWO FIGURE COLUMNS FOLLOW THE SAME RULE.
func TestAssetMeterReadings_EveryFigureOnThePaneIsWhole(t *testing.T) {
	whole := map[string]bool{}
	for _, r := range assetMeterReadingFixtureRows() {
		whole[meterValueText(r.Delta)] = true
		whole[meterValueText(r.ValueAfter)] = true
	}
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := assetMeterReadingsFixture()
			s.setSize(tea.WindowSizeMsg{Width: w, Height: h})
			pane := assetFlatPane(s, w, h)
			if assetPaneRefused(pane) {
				continue
			}
			for _, got := range meterDigits.FindAllString(pane, -1) {
				got = strings.TrimPrefix(got, "-")
				for want := range whole {
					want = strings.TrimPrefix(want, "-")
					if got != want && strings.HasPrefix(want, got) && len(got) >= 3 {
						t.Fatalf("at %dx%d the ledger draws %q, a PREFIX of %q:\n%s",
							w, h, got, want, pane)
					}
				}
			}
		}
	}
}

// A CORRECTION IS TOLD FROM A MEASUREMENT ON THE LEDGER, which is what the whole
// record-reading / adjust distinction buys: the two land as different rows and
// the screen has to show which is which.
func TestAssetMeterReadings_ACorrectionReadsAsOne(t *testing.T) {
	s := assetMeterReadingsFixture()
	s.setSize(tea.WindowSizeMsg{Width: 100, Height: 30})
	pane := assetFlatPane(s, 100, 30)
	if !strings.Contains(pane, "Manual correction") {
		t.Errorf("the ledger does not label its correction:\n%s", pane)
	}
	if !strings.Contains(pane, "Manual entry") {
		t.Errorf("the ledger does not label its measurements:\n%s", pane)
	}
	// The REASON is what the server required before it would take the
	// correction; it is the whole reason adjust is a separate action.
	if !strings.Contains(pane, "recount against the control") {
		t.Errorf("the ledger drops the correction's reason:\n%s", pane)
	}
	// And a rollup reading stays ATTRIBUTED, though nobody recorded it.
	if !strings.Contains(pane, "device_usage") {
		t.Errorf("an automatic reading lost its provenance:\n%s", pane)
	}
}

// A ROLLUP READING HAS NO RECORDER, and that is different from one whose
// recorder could not be named.
func TestAssetMeterReadings_TheFixtureReachesAnUnrecordedRow(t *testing.T) {
	var unrecorded bool
	for _, r := range assetMeterReadingFixtureRows() {
		unrecorded = unrecorded || r.RecordedBy == nil
	}
	if !unrecorded {
		t.Fatal("every fixture reading has a recorder, so the pane test above proves " +
			"nothing about an automatic one")
	}
}

// THE ASSET SHEET NAMES THE TWO KEYS IT NOW BINDS. A key that acts and is not
// named is the bar-honesty rule broken outright; this footer is a prose literal
// no sweep can read, so it is asserted here.
func TestAssetDetail_TheFooterNamesMetersAndDocuments(t *testing.T) {
	s := NewAssetDetailScreen(Deps{}, "a1")
	s.loading = false
	s.asset = &omsapi.Asset{ID: "a1", Name: assetMeterFixtureAsset}
	s.terminalHeight = 30
	view := stripANSI(s.View())
	for _, want := range []string{"M meters", "D documents", "H history"} {
		if !strings.Contains(view, want) {
			t.Errorf("the asset footer does not name %q:\n%s", want, view)
		}
	}

	// And both keys really act, through a real Root: the letters are claimed by
	// the screen and not swallowed by the global layer.
	fake := meterFakeWithSpindle()
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	for key, want := range map[string]string{
		"M": "*tui.AssetMetersScreen", "D": "*tui.AssetDocumentsScreen",
		"H": "*tui.AssetMaintenanceHistoryScreen",
	} {
		scr := NewAssetDetailScreen(deps, "a1")
		scr.loading = false
		scr.asset = &omsapi.Asset{ID: "a1", Name: assetMeterFixtureAsset}
		r := newTestRoot(scr)
		r.deps = deps
		next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		after := next.(Root)
		after = pump(t, after, cmd, 0)
		if got := typeName(after.screen); got != want {
			t.Errorf("%s on the asset sheet landed on %s, want %s", key, got, want)
		}
	}
}

func typeName(v any) string { return fmt.Sprintf("%T", v) }

// assetPaneRefused reports the layer's too-short notice — the state where the
// frame is not drawn at all, so nothing on it is a claim about a grid.
func assetPaneRefused(pane string) bool {
	return pane == "" || strings.Contains(pane, "Too short")
}

// THE CONFIRM'S HEADLINE FITS THE PANE AS ASSEMBLED, and the FIGURES never give.
//
// It is measured off the screen's OWN View at every drawable pane, because a
// check that reads the already-clipped pane cannot fail — the truncation has
// already happened. The failure this pins: a row bounded in one part and then
// added to, which is how ` Delete: M · 250 orde` reached an operator on the
// sibling purchasing confirm.
func TestAssetMeters_TheConfirmHeadlineFitsThePaneAndKeepsItsFigures(t *testing.T) {
	var checked int
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			s := assetMetersFixture()
			s.setSize(tea.WindowSizeMsg{Width: w, Height: h})
			s.openEntry(meterPhaseRecord)
			s.valueInput.SetValue("120")
			s.submitRecord()
			if s.phase != meterPhaseConfirm {
				t.Fatalf("at %dx%d a backwards reading did not open the confirm", w, h)
			}
			line := s.confirmHeadline()
			if room := screenBodyCells(w) - len(jdeIndent); visibleCells(line) > room {
				t.Fatalf("at %dx%d the headline %q is %d cells against %d — assembled and "+
					"then added to, which clampToBox cuts with no mark",
					w, h, line, visibleCells(line), room)
			}
			pane := assetFlatPane(s, w, h)
			if assetPaneRefused(pane) {
				continue
			}
			checked++
			for _, fig := range []string{"1289.75 hours", "120 hours"} {
				if !strings.Contains(pane, fig) {
					t.Fatalf("at %dx%d the confirm has lost the figure %q:\n%s", w, h, fig, pane)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no pane at or above 80 columns drew the confirm, so the figure claim " +
			"was never measured")
	}
}
