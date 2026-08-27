package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The answer surface: where the New PO screen's reply to a keypress is drawn,
// and the two things that must be true of it at EVERY height an operator can
// reach.
//
// A pinned header row is trimmed by jdeFitHeader the moment the pane is short,
// and a header may mark exactly ONE row essential (jdeMinBudget). So a phase
// with a typed box AND something to say could keep only one of them, and both
// choices have now been shipped:
//
//   - the BOX essential — a declining key answered into a row a short pane
//     trimmed, so the press redrew a byte-identical pane at 80x11 and 80x12 on
//     the item filter and 80x11 through 80x13 on the asset search;
//   - the NOTE essential — the fix for that, which took the BOX off the pane at
//     exactly those heights, so every rune typed into the ASSET search redrew a
//     byte-identical pane. (The item filter survived on an accident of its own:
//     itemFilterOrVerdict rewrites the note on every rune, so the note it was
//     given instead of the box happened to move. The asset search is
//     server-side and runs on enter, so nothing else on its frame moves at all.)
//
// Trading which row disappears cannot fix that in either direction. The answer
// lives on the layer's STATUS ROW now, which the frames append unconditionally
// and jdeFitHeader cannot reach (po_create.go's statusPlan), and the box has
// the essential header row back.
//
// Both sweeps below DERIVE their sites: the phases come from poPhaseCases(),
// whose completeness over the poPhase iota is already enforced by
// TestPOCreate_EveryPhaseIsSwept, and which of them pin a typed box is
// DISCOVERED by asking the screen (poBoxPhases) rather than listed here. A
// phase that grows a search box later is swept the moment it does.

// poNoteText is the phase's answer to the last keypress, as the screen holds
// it, without going through the surface under test — so the check reads what
// was SAID and asserts where it is DRAWN.
func poNoteText(s *PurchaseOrderCreateScreen) string {
	if s.pendingLead != "" {
		return s.pendingLead
	}
	return s.phaseNote().text
}

// poBoxCase is one phase that pins a TYPED BOX in the pinned header, together
// with what it took to get the keyboard into it.
type poBoxCase struct {
	c    poPhaseCase
	open bool // the box needed `/` to open
}

// poBoxPhases discovers every phase of the New PO screen that pins a typed box.
//
// Discovered rather than listed, and asked of essentialBoxRow — the one
// function that decides which row a phase pins — because a roster kept beside
// the code is the omission this screen has been bitten by in every other sweep
// it owns. The probe is `/`, which is what opens a picker's search box and
// which every other phase either ignores or types.
func poBoxPhases(t *testing.T) []poBoxCase {
	t.Helper()
	var out []poBoxCase
	for _, c := range poPhaseCases() {
		r, s := poPickerAtSize(t, c.fake(), 80, poPaneSizes[0])
		r = c.reach(t, r, s)
		if s.phase != c.phase {
			t.Fatalf("reach landed on phase %v, want %v", s.phase, c.phase)
		}
		if s.essentialBoxRow() != "" {
			out = append(out, poBoxCase{c: c})
			continue
		}
		next, _ := r.Update(poPickerKeyMsg("/"))
		r = next.(Root)
		if s.essentialBoxRow() != "" {
			out = append(out, poBoxCase{c: c, open: true})
		}
	}
	if len(out) == 0 {
		t.Fatal("no phase of the New PO screen pins a typed box, so both sweeps " +
			"below assert nothing. essentialBoxRow is what they are derived from; " +
			"if it stopped being how a box is pinned, they need rewriting rather " +
			"than deleting")
	}
	return out
}

// reach drives one box case to the frame the sweep presses on.
func (b poBoxCase) reach(t *testing.T, h int) (Root, *PurchaseOrderCreateScreen) {
	t.Helper()
	r, s := poPickerAtSize(t, b.c.fake(), 80, h)
	r = b.c.reach(t, r, s)
	if b.open {
		next, _ := r.Update(poPickerKeyMsg("/"))
		r = next.(Root)
	}
	if s.essentialBoxRow() == "" {
		t.Fatalf("%s: the box did not open at 80x%d", b.c.name, h)
	}
	return r, s
}

// TestPOCreate_EveryTypedRuneMovesThePaneAtEveryDrawableHeight is the report,
// from the seat: on the asset picker at 80x11, 80x12 and 80x13 you type into a
// search box and the screen does not move.
//
// The runes are DISTINCT, because a box full of one repeated character looks
// the same however far it has scrolled — a sweep that holds one key down
// passes without the box moving at all.
//
// The HEIGHT is derived from Root's own gate rather than named: poPaneSizes is
// {24, 30} and the whole defect lives under 14, which is why every existing
// sweep of these states passed over it. Frames the layer REFUSES to draw are
// skipped, because jdeTooShort is its own rule.
func TestPOCreate_EveryTypedRuneMovesThePaneAtEveryDrawableHeight(t *testing.T) {
	for _, b := range poBoxPhases(t) {
		for _, h := range poDrawableHeights() {
			t.Run(fmt.Sprintf("%s/80x%d", b.c.name, h), func(t *testing.T) {
				r, screen := b.reach(t, h)
				if poFrameRefused(t, screen, h) {
					t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
				}
				for _, ch := range "qwe" {
					before := strings.Join(poPaneLinesAt(t, screen, h), "\n")
					next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
					r = next.(Root)
					after := strings.Join(poPaneLinesAt(t, screen, h), "\n")
					if before == after {
						t.Fatalf("typing %q into %s redrew a byte-identical pane at 80x%d:\n%s",
							string(ch), b.c.name, h, after)
					}
				}
				poAssertFits(t, b.c.name+" mid-typing", screen)
			})
		}
	}
}

// TestPOCreate_ABoxPhaseDrawsTheBoxAndItsAnswerAtOnce is the same rule read
// structurally rather than through a diff, and it is the half that says the fix
// was not another trade.
//
// A pane change is necessary and not sufficient: a frame could satisfy the
// sweep above by moving one row while the operator's box is off the pane, which
// is exactly what the previous arrangement did to the item filter. So this
// asserts that BOTH survive the clip at every drawable height — the row the
// operator is typing into, and the screen's answer to the key that opened it.
func TestPOCreate_ABoxPhaseDrawsTheBoxAndItsAnswerAtOnce(t *testing.T) {
	for _, b := range poBoxPhases(t) {
		if !b.open {
			// The review cart's notes box is pinned with nothing having been
			// pressed to open it, so there is no answer to look for beside it.
			continue
		}
		for _, h := range poDrawableHeights() {
			t.Run(fmt.Sprintf("%s/80x%d", b.c.name, h), func(t *testing.T) {
				_, screen := b.reach(t, h)
				if poFrameRefused(t, screen, h) {
					t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
				}
				pane := strings.Join(poPaneLinesAt(t, screen, h), "\n")
				// The box is found by its LABEL, which renderJDEField draws in
				// the shared label column: the value is what the operator has
				// typed and is empty on a freshly opened box.
				label := strings.TrimSpace(strings.SplitN(
					strings.TrimSpace(screen.essentialBoxRow()), " ", 2)[0])
				if label == "" {
					t.Fatalf("%s pins a box with no label to look for", b.c.name)
				}
				if !strings.Contains(pane, label) {
					t.Errorf("%s at 80x%d: the box the operator is typing into (%q) "+
						"is not on the pane:\n%s", b.c.name, h, label, pane)
				}
				// The answer's opening clause — everything before the first
				// joint, which is the part that survives every bound on this
				// screen.
				answer, _, _ := strings.Cut(poNoteText(screen), poLeadJoint)
				if answer == "" {
					t.Fatalf("%s answered `/` with nothing", b.c.name)
				}
				if !strings.Contains(pane, answer) {
					t.Errorf("%s at 80x%d: the answer to the key that opened the box "+
						"(%q) is not on the pane:\n%s", b.c.name, h, answer, pane)
				}
			})
		}
	}
}
