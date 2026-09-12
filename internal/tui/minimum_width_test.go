// The size contract: which terminals ScanTTY draws in, and what it says in the
// ones it will not.
//
// WHY THERE IS A FLOOR AT ALL. Every budget in this package is written against
// the pane 80 columns gives — screenBodyWidth(80) is 51 and the action bar gets
// 49 of them — and Root used to accept any terminal whose CONTENT pane came to
// 20 cells, which is a terminal width of 45. Between 45 and 80 the guarantees
// written against 51 stopped holding one at a time, each at its own width, and
// none of them said so. Measured on the code as it stood:
//
//   - 45–48: screenBodyWidth's floor of 20 is WIDER than the pane really is
//     (16 to 19 cells), so every columnar row is budgeted against cells the
//     terminal does not have. TestJDEForm_NoRowRunsPastThePane excluded the
//     band outright for that reason (receiveHonestWidths).
//   - up to 56: the columnar ACTION BAR is drawn past the pane — all 35
//     columnar screens by width 48, and LocationReconcileScreen as high as 56 —
//     in their opening states alone, so clampToBox takes the tail of the one
//     surface the key-honesty rule rests on.
//   - up to 53: every one of the eight list footers loses
//     `g/G home/end top/bottom` off the clipped pane, which is the same rule
//     broken on the other half of the app.
//   - up to 76: the purchase-order void prompt withholds BOTH of its caveats
//     (voidCaveatsFit), because no wording of the permanent-loss warning folds
//     to one row in the cells a narrower pane leaves — so an irreversible
//     action is confirmed with no warning at all.
//
// 48, 53, 56 and 76 do not agree, so there is no coherent floor among them: any
// one of them leaves the others broken. 80 is the width the interface is
// designed to and the one the captain keeps as the standard, so it is the one
// the program insists on, and every width guarantee in the package is a claim
// about the panes that leaves reachable.
//
// WHAT MAKES THE FLOOR LOAD-BEARING rather than declarative is that two sweeps
// walk jdeDrawableWidths() — which is derived from Root's own gate, so it moves
// with the floor — and are green only because of it:
// TestJDEForm_TheActionBarSurvivesEveryHeight and
// TestList_TheFooterIsLegibleAtEveryDrawableWidth. Both were watched failing
// with minTerminalWidth temporarily set back to 45.
package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// contractPreviousFloor is the narrowest terminal Root drew a frame in before
// the size contract was settled: contentWidth was r.width - 24 - 1 and the gate
// refused below 20, so 45 was the first width that passed it.
//
// It is here to give the sweeps below a band to make a NON-VACUOUS claim about.
// "the notice names both numbers wherever it fits" is true of an empty set of
// widths as readily as of a real one; the widths from here to the floor are the
// ones an operator can actually arrive at by dragging a pane narrower, so they
// are the ones the notice has to be legible at.
const contractPreviousFloor = 45

// TestRoot_TheFloorIsTheSizeContract: Root draws a frame at exactly the sizes
// the contract admits and refuses at exactly the ones it does not.
//
// Asked of Root.View directly rather than of terminalTooSmall, because the gate
// being wired into the frame is the whole point — a contract the renderer does
// not consult is a comment. It walks past the floor in both axes so the
// boundary is pinned from both sides rather than sampled on one.
func TestRoot_TheFloorIsTheSizeContract(t *testing.T) {
	for w := 1; w <= minTerminalWidth+20; w++ {
		r := newTestRoot(NewServiceStatusScreen(Deps{}))
		next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: 40})
		drew := strings.Contains(next.(Root).View(), "\n")
		if want := w >= minTerminalWidth; drew != want {
			t.Errorf("at width %d Root drew a frame = %v, want %v (the contract's floor "+
				"is %d columns)", w, drew, want, minTerminalWidth)
		}
	}
	for h := 1; h <= minTerminalHeight+20; h++ {
		r := newTestRoot(NewServiceStatusScreen(Deps{}))
		next, _ := r.Update(tea.WindowSizeMsg{Width: minTerminalWidth, Height: h})
		drew := strings.Contains(next.(Root).View(), "\n")
		if want := h >= minTerminalHeight; drew != want {
			t.Errorf("at height %d Root drew a frame = %v, want %v (the contract's floor "+
				"is %d rows)", h, drew, want, minTerminalHeight)
		}
	}
}

// TestRoot_TheRefusalIsTheWholeFrame: at a size the contract refuses, what Root
// renders is the notice and nothing else.
//
// It matters that the notice REPLACES the frame rather than being drawn above a
// clipped one: a nav column and half a screen beside a line saying the terminal
// is too small is a pane whose guarantees have stopped applying, drawn anyway,
// which is the state the contract exists to remove.
func TestRoot_TheRefusalIsTheWholeFrame(t *testing.T) {
	for _, size := range [][2]int{
		{contractPreviousFloor, 40}, {minTerminalWidth - 1, 40}, {20, 40},
		{minTerminalWidth, minTerminalHeight - 1}, {40, 5}, {1, 1},
	} {
		w, h := size[0], size[1]
		r := newTestRoot(NewServiceStatusScreen(Deps{}))
		next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
		got := next.(Root).View()
		if want := terminalTooSmall(w, h); got != want {
			t.Errorf("at %dx%d Root rendered %q, want the refusal %q", w, h, got, want)
		}
		if strings.Contains(got, "scantty\n") || strings.Contains(got, "Dashboard") {
			t.Errorf("at %dx%d Root drew part of the frame beside the refusal:\n%s", w, h, got)
		}
	}
}

// TestRoot_TheRefusalFitsTheTerminalItIsShownIn: at every size the contract
// refuses, the notice is one non-empty line that fits the terminal's width.
//
// The refusal is the WHOLE user experience at that size, so a clipped or blank
// one is the dead end the contract was supposed to close, not a cosmetic
// shortfall. One LINE because the shortest terminal this can be read in is one
// row tall, and clampToBox is not reached here — Root returns the notice before
// it builds a frame — so nothing downstream will fold or trim it.
func TestRoot_TheRefusalFitsTheTerminalItIsShownIn(t *testing.T) {
	for w := 1; w <= minTerminalWidth+2; w++ {
		for h := 1; h <= minTerminalHeight+2; h++ {
			notice := terminalTooSmall(w, h)
			if w >= minTerminalWidth && h >= minTerminalHeight {
				if notice != "" {
					t.Errorf("at %dx%d the contract admits the terminal but a notice was "+
						"drawn: %q", w, h, notice)
				}
				continue
			}
			switch {
			case notice == "":
				t.Errorf("at %dx%d the contract refuses the terminal and the notice is "+
					"empty — a blank screen is the dead end this exists to close", w, h)
			case strings.Contains(notice, "\n"):
				t.Errorf("at %dx%d the notice is %d lines; a one-row terminal reads the "+
					"first and nothing fetches the rest:\n%s", w, h,
					strings.Count(notice, "\n")+1, notice)
			case lipgloss.Width(notice) > w:
				t.Errorf("at %dx%d the notice %q is %d cells against a terminal of %d, so "+
					"the tail an operator has to act on is off the screen", w, h, notice,
					lipgloss.Width(notice), w)
			}
		}
	}
}

// TestRoot_TheRefusalNamesTheSizeNeededAndTheSizeThereIs: over the band an
// operator actually arrives at — every width from the floor Root used to draw
// at up to the one it draws at now — the notice names both figures in words.
//
// The band is what keeps the claim honest. Below contractPreviousFloor the
// terminal was already refused before this work and the notice has to shorten;
// the sweep after this one is what covers those widths, and it claims only what
// a handful of cells can deliver.
func TestRoot_TheRefusalNamesTheSizeNeededAndTheSizeThereIs(t *testing.T) {
	if contractPreviousFloor >= minTerminalWidth {
		t.Fatalf("the band this sweep measures is empty (previous floor %d, contract floor "+
			"%d), so it asserts nothing", contractPreviousFloor, minTerminalWidth)
	}
	for w := contractPreviousFloor; w < minTerminalWidth; w++ {
		notice := terminalTooSmall(w, 40)
		need := fmt.Sprintf("needs %d columns", minTerminalWidth)
		has := fmt.Sprintf("has %d", w)
		if !strings.Contains(notice, need) {
			t.Errorf("at width %d the notice %q does not say what the program needs (%q), "+
				"so the operator is refused with no number to resize to", w, notice, need)
		}
		if !strings.Contains(notice, has) {
			t.Errorf("at width %d the notice %q does not say what the terminal has (%q), "+
				"so nothing confirms the program and the operator see the same size",
				w, notice, has)
		}
	}
}

// TestRoot_TheRefusalLeadsWithTheSizeItNeeds: at every refused size, the first
// figure in the notice is the one the operator has to resize TO, and whatever
// the width took is either a whole figure or MARKED as cut.
//
// This is the package's standing rule about what survives a trim, applied to
// the one surface that has nothing else on it. The notice shortens as the
// terminal narrows, so the ordering is the only bound that holds by
// CONSTRUCTION: every rung leads with the required size, so every prefix of one
// either carries it whole or is a cut of it — and a cut is marked, because a
// lone digit left standing reads as a required width that is not the one the
// program means.
func TestRoot_TheRefusalLeadsWithTheSizeItNeeds(t *testing.T) {
	marked, whole := 0, 0
	for w := 1; w < minTerminalWidth; w++ {
		for _, h := range []int{1, minTerminalHeight - 1, 40} {
			notice := terminalTooSmall(w, h)
			if notice == "" {
				t.Fatalf("at %dx%d the contract refuses the terminal but drew nothing", w, h)
			}
			want := fmt.Sprintf("%d", minTerminalWidth)
			if h < minTerminalHeight && w >= minTerminalWidth {
				want = fmt.Sprintf("%d", minTerminalHeight)
			}
			switch {
			case strings.Contains(notice, want):
				whole++
				if got := contractFirstNumber(notice); got != want {
					t.Errorf("at %dx%d the notice %q leads with %q rather than the size it "+
						"needs (%q) — a trim takes the tail, so the required figure has to "+
						"be what a trim keeps", w, h, notice, got, want)
				}
			case strings.HasSuffix(notice, paneCutMark):
				marked++
			default:
				t.Errorf("at %dx%d the notice %q neither carries the required size whole "+
					"nor marks that it was cut, so a fragment of a number reads as the "+
					"whole one", w, h, notice)
			}
		}
	}
	if whole == 0 || marked == 0 {
		t.Errorf("the sweep saw %d whole figures and %d marked cuts; it has to reach BOTH "+
			"or one of its two arms is asserting nothing", whole, marked)
	}
}

// contractFirstNumber is the first run of digits in a line, or "" when it holds
// none.
func contractFirstNumber(s string) string {
	start := -1
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			if start < 0 {
				start = i
			}
		case start >= 0:
			return s[start:i]
		}
	}
	if start >= 0 {
		return s[start:]
	}
	return ""
}

// TestLayout_TheWidthFloorIsNeverReached: at every width Root draws at,
// screenBodyWidth's floor of 20 is not in play, so it and screenBodyCells
// answer the same number.
//
// The two disagreeing is what made widths 45–48 unmeasurable before the
// contract: a caller budgeting against screenBodyWidth spent up to four cells
// the pane did not have, clampToBox took them off the right edge with no mark,
// and the sweep that checks rows against the pane skipped the band rather than
// reporting a defect per screen for one layer arithmetic error
// (receiveHonestWidths). With the floor at minTerminalWidth the narrowest pane
// is 51 cells and the question cannot arise — and if the contract is ever
// reopened downwards this is what says so first.
func TestLayout_TheWidthFloorIsNeverReached(t *testing.T) {
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("Root draws at no width at all, so this sweep measured nothing")
	}
	for _, w := range widths {
		if floored, real := screenBodyWidth(w), screenBodyCells(w); floored != real {
			t.Errorf("at width %d screenBodyWidth answers %d and the pane is really %d — a "+
				"caller budgeting against the floor spends %d cells clampToBox takes back "+
				"off the right edge, unmarked", w, floored, real, floored-real)
		}
	}
	if got := screenBodyWidth(widths[0]); got != screenBodyCells(widths[0]) {
		t.Fatalf("the narrowest drawable width %d is already floored", widths[0])
	}
}
