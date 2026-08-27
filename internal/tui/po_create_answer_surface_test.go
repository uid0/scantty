package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

// ---------------------------------------------------------------------------
// The answer shares the status row; the work in flight does not give
// ---------------------------------------------------------------------------

// poBoxInFlight drives a box-pinning phase into a state with a REQUEST OUT, and
// sets the screen's answer from that phase's own writer.
//
// A map keyed by phase rather than a slice, so poBoxPhases' discovery and this
// table are checked against each other: a phase that grows a search box later
// is discovered there and FAILS here until somebody says what "a request is
// out" means on it. Absent and empty are different states, which is the shape
// poPhasesWithoutKeys uses for the same reason.
//
// The LEAD is set directly rather than pressed, because what is under test is
// the composer's bound and the longest lead any arm happens to carry today is
// not the bound. Each entry writes the field that phase's own arms write.
var poBoxInFlight = map[poPhase]struct {
	// inFlight leaves a request genuinely out with the box open.
	inFlight func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root
	// setLead writes the phase's answer, the way that phase's arms write it.
	setLead func(s *PurchaseOrderCreateScreen, lead string)
}{
	poPhaseItemPick: {
		inFlight: func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
			t.Helper()
			s.itemSuppliersLoad, s.itemSuppliersAll, s.itemSuppliers = true, nil, nil
			return r
		},
		setLead: func(s *PurchaseOrderCreateScreen, lead string) {
			s.itemSuppliersNote = pickerNote{text: lead, level: StatusWarn}
		},
	},
	poPhaseAssetPick: {
		inFlight: func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
			t.Helper()
			s.assetsLoading, s.assetsQuery = true, "zzz"
			return r
		},
		setLead: func(s *PurchaseOrderCreateScreen, lead string) {
			s.assetsNote = pickerNote{text: lead, level: StatusWarn}
		},
	},
	poPhaseReview: {
		inFlight: func(t *testing.T, r Root, s *PurchaseOrderCreateScreen) Root {
			t.Helper()
			s.pending = true
			return r
		},
		setLead: func(s *PurchaseOrderCreateScreen, lead string) { s.pendingLead = lead },
	},
}

// TestPOStatus_AnAnswerNeverDisplacesTheWorkInFlight is the guard PR #152 put on
// this row, re-proved now that a SECOND writer can reach it.
//
// That defect: the lead was joined in front of the working sentence unbounded,
// so with the longest lead on the screen "a purchase order is being created"
// was pushed off a row that cannot fold — and the frozen bar names almost
// nothing, so from then on the pane did not say a submit was out. It was fixed
// by RESERVING the subject before the lead is allowed to expand, and by cutting
// poSubmitWords from 32 cells to 20 so it fits what that reservation leaves.
//
// Until this change the only writer of that lead was pendingDecline, which
// fires only while the submit is out. Now every phase's ANSWER can reach the
// row, so the question has to be asked again of a lead of ANY length and of
// every subject that can share the row with one.
//
// The answer is that they cannot collide, and the reason is the CAP rather than
// the wording: poLeadOnto reserves the lead's opening clause at no more than
// half the row, so at 80 columns the subject is bounded to at least
// 51 - 25 - 3 = 23 cells whatever the lead says. An over-long lead is therefore
// the provable worst case — every lead past the cap produces the identical
// reservation — which is why the fixture uses one instead of the longest
// sentence that happens to be in the file today. That is this project's
// "a fixture that cannot reach the bound makes the assertion vacuous" rule
// pointed the other way: reach PAST it, once, rather than hope.
//
// Two things are asserted of every subject that can share the row, both derived
// from the composer's own arithmetic rather than transcribed:
//
//   - the subject keeps its guaranteed PREFIX, which is the FACT — the words
//     that say what work is out — because that is what leads every one of these
//     sentences. What gives is the tail, where the identifier is.
//   - poSubmitWords survives WHOLE, which is the sentence PR #152 was about,
//     and it is checked against the floor as well as against the drawn row, so
//     lengthening it or narrowing the pane fails here rather than in the field.
func TestPOStatus_AnAnswerNeverDisplacesTheWorkInFlight(t *testing.T) {
	box := poBoxPhases(t)
	for _, b := range box {
		if _, ok := poBoxInFlight[b.c.phase]; !ok {
			t.Errorf("phase %v pins a typed box, so a lead can reach its status row, "+
				"but poBoxInFlight has no entry saying what a request in flight looks "+
				"like there — the bound would be unchecked on it", b.c.phase)
		}
	}

	// A lead past the cap. Every longer one reserves exactly the same cells, so
	// this IS the worst case rather than a sample of it.
	longLead := strings.Repeat("z", 200)

	for _, b := range box {
		spec, ok := poBoxInFlight[b.c.phase]
		if !ok {
			continue
		}
		for _, h := range poDrawableHeights() {
			t.Run(fmt.Sprintf("%s/80x%d", b.c.name, h), func(t *testing.T) {
				r, screen := b.reach(t, h)
				r = spec.inFlight(t, r, screen)
				spec.setLead(screen, longLead)
				if poFrameRefused(t, screen, h) {
					t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
				}
				subject := screen.workingSubject()
				if subject == "" {
					t.Fatalf("%s: the setup left no request in flight, so this case "+
						"asserts nothing about a row it never shares", b.c.name)
				}
				row, _ := screen.statusPlan()

				// The floor the composer guarantees, derived from it rather
				// than written down: the lead's clause is capped at half the
				// row and the joint costs its own width.
				pane := screen.paneWidth()
				floor := pane - pane/2 - lipgloss.Width(poLeadJoint)
				if floor < 1 {
					t.Fatalf("the pane leaves the subject %d cells, which is not a floor", floor)
				}
				// pickerClip spends one cell of that on the ellipsis.
				kept := cellPrefix(subject, floor-1)
				if !strings.Contains(row, kept) {
					t.Errorf("%s at 80x%d: a %d-cell lead pushed the work in flight off "+
						"the status row.\n\tsubject: %q\n\tguaranteed prefix: %q\n\trow: %q",
						b.c.name, h, lipgloss.Width(longLead), subject, kept, row)
				}
				// And the sentence PR #152 was about survives WHOLE, not as a
				// prefix the clip happened to spare.
				if strings.HasPrefix(subject, poSubmitWords) {
					if w := lipgloss.Width(poSubmitWords); w > floor {
						t.Errorf("poSubmitWords is %d cells and the subject's floor at "+
							"80x%d is %d — the fixed words no longer fit what the "+
							"reservation leaves", w, h, floor)
					}
					if !strings.Contains(row, poSubmitWords) {
						t.Errorf("%s at 80x%d: the status row %q does not carry %q whole",
							b.c.name, h, row, poSubmitWords)
					}
				}
				poAssertFits(t, b.c.name+" under a long lead", screen)
			})
		}
	}
}
