package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_field_forms_test.go — the fixtures for the FIFTH recipe taken out of
// proseBarUnconverted as a group rather than a screen at a time.
//
// THE RECIPE, and what makes these screens one group. Each is a FIELD FORM: a
// short column of text boxes whose up/down move a FOCUS rather than a cursor
// through rows, with no window, and a muted literal under it. They were recorded
// as "the field-form exemption", and that was an exemption from the navigation
// CLASSIFIER — which cannot tell a focus pair from a list cursor — rather than
// from the bar rule, which was broken on every one of them in the same way:
//
//   - THE FOCUS KEYS WERE NAMED IN PART. `tab move` on the sign-in and reorder
//     forms, while shift+tab and both arrows walk the fields too. The batch-scan
//     setup named `tab/↑↓ move` from both fields while its focus CLAMPS, so from
//     each field half of what it named did nothing and shift+tab was never named.
//   - ENTER WAS NAMED FOR THE WRONG FIELD. It moves to the next field everywhere
//     but the last, and the literals called it a submit throughout; on the scan
//     step it was named over an empty buffer, where it is ignored outright.
//   - A KEY WAS NAMED FOR A STATE IT DOES NOT ACT IN: `ctrl+z undo last` with
//     nothing to undo, and `esc back` over a half-typed serial, where esc clears.
//
// WHAT HAD TO COME WITH THE BAR IS THE FRAME'S HEIGHT. A form with no window is
// as tall as its lines, and every line but one kind is fixed: the ones carrying
// a value the screen does not control — an OMS error body above all, which
// arrives as a whole gateway page. Those are bounded to one marked row
// (proseFormLine), and the batch scan's log, a flat eight rows, is a count the
// pane answers now (BatchScanSerialsScreen.logWindow). Each has an OVERSIZED
// fixture below — a value that alone outruns the pane — because a footer check
// whose fixtures cannot push the footer off is not checking the footer.

const proseBarFormBox = "a focused text box takes every printable key"

// proseBarFieldFormFixtures is every screen on that recipe, in every state its
// bar changes shape in.
func proseBarFieldFormFixtures() []proseBarFixture {
	const scanImmobile = "the scan step is one text box; up/down go to it and nothing on " +
		"the pane moves"
	undoDecline := map[string]string{
		"ctrl+z": "undo with nothing newly received says `nothing to undo` on the result row",
	}
	out := []proseBarFixture{
		// --- sign in -----------------------------------------------------------
		{
			name: "login/username", recv: "LoginScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarLogin() },
		},
		{
			name: "login/password", recv: "LoginScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarPress(proseBarLogin(), "tab") },
		},

		// --- reorder request ---------------------------------------------------
		{
			name: "reorder form/first field", recv: "ReorderFormScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarReorderForm() },
		},
		// Reached by shift+tab from the first field, which is the wrap the bar
		// claims: the last field is where enter becomes the submit.
		{
			name: "reorder form/last field", recv: "ReorderFormScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarPress(proseBarReorderForm(), "shift+tab") },
		},

		// --- batch scan --------------------------------------------------------
		{
			name: "batch scan/setup lot", recv: "BatchScanSerialsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarBatchScan() },
		},
		{
			name: "batch scan/setup expiration", recv: "BatchScanSerialsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen { return proseBarPress(proseBarBatchScan(), "tab") },
		},
		{
			name: "batch scan/scan empty", recv: "BatchScanSerialsScreen", typing: proseBarFormBox,
			build:    func() proseBarScreen { return proseBarPress(proseBarBatchScan(), "enter") },
			immobile: scanImmobile, declines: undoDecline,
		},
		{
			name: "batch scan/scan typed", recv: "BatchScanSerialsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				return proseBarPress(proseBarBatchScanLogged(3, ""), "S", "N", "-", "9")
			},
			immobile: scanImmobile,
		},
		// A scan OUT: enter and ctrl+z are both held while it is, so both come off
		// the bar, and typing the next serial still works.
		{
			name: "batch scan/scan pending", recv: "BatchScanSerialsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				return proseBarPress(proseBarBatchScanLogged(3, ""), "S", "N", "-", "9", "enter")
			},
			immobile: scanImmobile,
		},
	}
	return append(out, proseBarOversizedFieldForms()...)
}

// proseBarOversizedFieldForms are the states in which a value the form does not
// control is, on its own, taller than an 80x24 pane — so without its bound the
// bar is what clampToBox takes.
func proseBarOversizedFieldForms() []proseBarFixture {
	return []proseBarFixture{
		{
			name: "login/refused with a gateway page", recv: "LoginScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := proseBarLogin()
				next, _ := s.Update(loginDoneMsg{err: errors.New(proseBarGatewayPage())})
				return next.(proseBarScreen)
			},
		},
		{
			name: "reorder form/refused with a gateway page", recv: "ReorderFormScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := proseBarReorderForm()
				next, _ := s.Update(reorderSubmittedMsg{err: errors.New(proseBarGatewayPage())})
				return next.(proseBarScreen)
			},
		},
		{
			name: "batch scan/a long log under a gateway page", recv: "BatchScanSerialsScreen",
			typing: proseBarFormBox, immobile: "the scan step is one text box; nothing on the pane moves",
			build: func() proseBarScreen { return proseBarBatchScanLogged(30, proseBarGatewayPage()) },
		},
	}
}

// TestProseBarFieldForms_AnOversizedValueFitsAndMarksItsCut holds every bound
// this recipe introduced to the bar PR 199 set for the flat lists: a value
// taller than the pane is cut to the form's own rows, the cut is MARKED, and the
// frame still fits — so the bar under it survives.
//
// EVERY HEIGHT THE FORM'S FIXED ROWS FIT AT, not the first few until one does
// not. The batch-scan log is the one part of these frames the pane decides, so
// "fits at 80x24" proves it at one value — and at 80x24 a flat log of eight
// happens to fit, which is how the first version of this check passed with the
// window taken out. proseBarFormFixedRows derives what the form needs without
// its log, and every height that leaves room for that must hold the frame.
//
// WATCHED FAILING: with proseFormLine returning its input every fixture reports
// a form taller than an 80x24 terminal leaves; with logWindow drawing a flat
// eight, the batch scan reports its frame past the pane at 80x16, where the
// form's fixed rows fit and eight scans do not.
func TestProseBarFieldForms_AnOversizedValueFitsAndMarksItsCut(t *testing.T) {
	const width = 80
	heights := jdePaneHeights()
	for _, f := range proseBarOversizedFieldForms() {
		t.Run(f.name, func(t *testing.T) {
			fixed := proseBarFormFixedRows(f, width)
			if fixed > screenBodyRows(24) {
				t.Fatalf("the form needs %d rows without its log, more than the %d an %dx24 "+
					"terminal leaves it — the bar is cut at the canonical size", fixed,
					screenBodyRows(24), width)
			}
			checked := 0
			for _, height := range heights {
				if screenBodyRows(height) < fixed {
					continue
				}
				s := proseBarSize(f.build(), width, height)
				if !proseBarFrameFits(s, height) {
					t.Fatalf("at %dx%d an oversized value pushed the frame past the pane, taking "+
						"the bar with it:\n%s", width, height, s.View())
				}
				checked++
				got := stripANSI(s.View())
				if !strings.Contains(got, "…") {
					t.Fatalf("at %dx%d an oversized value was cut with no mark saying so:\n%s",
						width, height, got)
				}
				if want := "\n\n" + stripANSI(s.proseBar().render(proseBarCells(width))); !strings.HasSuffix(got, want) {
					t.Fatalf("at %dx%d the bar is not the last thing on the pane:\n%s", width, height, got)
				}
			}
			if checked == 0 {
				t.Fatalf("no drawable height leaves the form its %d fixed rows, so this checked nothing", fixed)
			}
		})
	}
}

// TestProseBarFieldForms_TheScanLogSaysWhatItLeftOut: every scan the log does
// not draw is counted by its `… N more` row, at every height the form's fixed
// rows fit at — a log cut to fit is only honest while the rows it drops are
// accounted for. Both sides of the cap are required: a tall pane draws all
// eight, and a short one fewer.
func TestProseBarFieldForms_TheScanLogSaysWhatItLeftOut(t *testing.T) {
	const width, scans = 80, 30
	f := proseBarFixture{build: func() proseBarScreen { return proseBarBatchScanLogged(scans, "") }}
	fixed := proseBarFormFixedRows(f, width)
	full, shrank := false, false
	for _, height := range jdePaneHeights() {
		if screenBodyRows(height) < fixed {
			continue
		}
		s := proseBarSize(f.build(), width, height)
		got := stripANSI(s.View())
		if !proseBarFrameFits(s, height) {
			t.Fatalf("at %dx%d the log pushed the frame past the pane:\n%s", width, height, got)
		}
		drawn := strings.Count(got, "✓ SN-")
		full = full || drawn == batchScanLogRows
		shrank = shrank || drawn < batchScanLogRows
		if want := fmt.Sprintf("… %d more", scans-drawn); !strings.Contains(got, want) {
			t.Fatalf("at %dx%d the log drew %d of %d scans and does not say %q:\n%s",
				width, height, drawn, scans, want, got)
		}
	}
	if !full || !shrank {
		t.Fatalf("the log drew all %d rows at some height: %v, and fewer at some height: %v — "+
			"both sides of the pane-derived count must be reached", batchScanLogRows, full, shrank)
	}
}

// proseBarFormFixedRows is how many rows a fixture's frame needs WITHOUT its scan
// log: its height on a tall pane, less the scans that pane drew. Measured rather
// than written down, so a line added to a form moves the threshold with it.
func proseBarFormFixedRows(f proseBarFixture, width int) int {
	s := proseBarSize(f.build(), width, 200)
	view := s.View()
	return lipgloss.Height(view) - strings.Count(stripANSI(view), "✓ SN-")
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

func proseBarLogin() *LoginScreen {
	return NewLoginScreen(Deps{OMS: omsapi.New("https://oms.example.org")})
}

func proseBarReorderForm() *ReorderFormScreen {
	item := &omsapi.Item{
		ID: "item-1", Name: "Hex bolt M8x40 zinc-plated, grade 8.8, box of 100",
		SKU: "HB-M8-40-ZN", Stock: 3,
	}
	return NewReorderFormScreen(Deps{}, item, []omsapi.ItemSupplier{{IsPreferred: true, PackQuantity: 100}})
}

func proseBarBatchScan() *BatchScanSerialsScreen {
	return NewBatchScanSerialsScreen(Deps{}, "item-1", "Safety relay, dual channel, 24V DC")
}

// proseBarBatchScanLogged is the scan step with n units newly received, and the
// result row carrying `result` where it is not empty.
func proseBarBatchScanLogged(n int, result string) *BatchScanSerialsScreen {
	s := proseBarPress(proseBarBatchScan(), "enter").(*BatchScanSerialsScreen)
	for i := 0; i < n; i++ {
		serial := fmt.Sprintf("SN-%06d", i+1)
		next, _ := s.Update(batchScanDoneMsg{serial: serial, res: &omsapi.ScanReceiveResult{
			SerializedComponent: omsapi.SerializedComponent{ID: fmt.Sprintf("unit-%d", i+1)},
			Created:             true,
		}})
		s = next.(*BatchScanSerialsScreen)
	}
	if result != "" {
		s.result, s.level = result, StatusError
	}
	return s
}

// proseBarGatewayPage is an error body the way omsapi.parseError hands one over
// when the envelope carries no code: a whole HTML page, many lines tall.
func proseBarGatewayPage() string {
	return "oms: http 502: " + strings.Repeat("<html><body><h1>502 Bad Gateway</h1></body></html>\n", 40)
}
