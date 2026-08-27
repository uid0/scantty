package tui

import (
	"fmt"
	"strconv"
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

// ---------------------------------------------------------------------------
// An order-level error is the fact; it shares the row with nothing
// ---------------------------------------------------------------------------

// poOrderErrorFixtures are the order-level errors the sweep below drives, and
// there are two of them for the reason this project keeps relearning: A FIXTURE
// THAT CANNOT REACH THE BOUND UNDER TEST MAKES THE ASSERTION VACUOUS.
//
// poSubmitFailWords is 22 cells and the reservation it had to survive leaves
// exactly 22 at 80 columns (room 49, the lead's clause capped at 49/2 = 24, the
// joint 3), so a sweep driving only the real wording went green with the lead
// still composed — certifying the rule on the one string the truncation
// happened to spare. The second fixture reaches PAST that, once, so the check
// fails the moment an order-level error is led again.
//
// Both stay inside what the row can hold at 80 columns (51 less the mark's two
// cells), because what is under test is the LEAD, not fitStatus.
var poOrderErrorFixtures = []string{
	poSubmitFailWords,
	"creating the PO failed: upstream refused",
}

// TestPOStatus_AnOrderLevelErrorIsNeverLedOffTheStatusRow is rule 6 applied to
// the one message on this screen that must never abbreviate.
//
// IT IS A MECHANISM GUARD, NOT A REGRESSION TEST, and that has to be said out
// loud: it writes setErr DIRECTLY onto a box-pinning phase because NO KEY
// SEQUENCE reaches that pair. errMsg is retired at every phase change (Update's
// key dispatch), no setErr writer fires on a picker phase, the pending freeze
// stops a picker being entered with a POST out, and on review phaseNote is
// empty while poCreatedMsg clears pendingLead before it calls setErr. What is
// held here is statusPlan's composition rule, so that the state stays harmless
// if a later change makes it reachable again. Do not read it as evidence of a
// defect an operator can produce.
//
// The status row is shared: a working sentence, an order-level error and the
// screen's answer to the last keypress can all want it, and where a typed box
// has taken the pinned header's one essential row the answer LEADS whatever is
// there (statusPlan). Applied to the error as well, that reserved up to half
// the row for a picker hint and cut the failure to what was left:
// `✗ type to narrow the catalo… · creating the PO f…` on the surface an order
// is committed from, which leaves the operator unable to tell what failed while
// telling them something they can rediscover by pressing the key again.
//
// So the error takes the row alone. The LEAD is what gives, entirely — not its
// tail — and the answer's own folded copy is still in the pinned header
// (answerRows).
//
// Driven with a lead PAST poLeadOnto's cap, because every longer lead reserves
// the identical cells: that makes it the provable worst case rather than a
// sample, the same way TestPOStatus_AnAnswerNeverDisplacesTheWorkInFlight
// reaches past the bound instead of hoping the longest sentence in the file
// does. The phases are DISCOVERED by poBoxPhases — a lead is only ever composed
// where a box is pinned — so a phase that grows a search box later is swept the
// moment it does.
func TestPOStatus_AnOrderLevelErrorIsNeverLedOffTheStatusRow(t *testing.T) {
	longLead := strings.Repeat("z", 200)

	drove := 0
	t.Cleanup(func() {
		if drove == 0 {
			t.Error("every box phase was skipped, so this sweep asserted nothing " +
				"about the error branch it exists for")
		}
	})

	for _, b := range poBoxPhases(t) {
		spec, ok := poBoxInFlight[b.c.phase]
		if !ok {
			// poBoxInFlight is checked for completeness over poBoxPhases by
			// TestPOStatus_AnAnswerNeverDisplacesTheWorkInFlight; this sweep
			// only borrows its writers.
			continue
		}
		for _, h := range poDrawableHeights() {
			t.Run(fmt.Sprintf("%s/80x%d", b.c.name, h), func(t *testing.T) {
				_, screen := b.reach(t, h)
				if screen.workingSubject() != "" {
					// The working sentence outranks the error and takes the row
					// itself, which is a different branch — and the one
					// TestPOStatus_AnAnswerNeverDisplacesTheWorkInFlight drives.
					// poBoxPhases includes the review cart WITH a submit out, so
					// this is a real case rather than a setup slip.
					t.Skip("a request is out, so the row draws the working branch")
				}
				if poFrameRefused(t, screen, h) {
					t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
				}
				spec.setLead(screen, longLead)
				for _, errMsg := range poOrderErrorFixtures {
					screen.setErr(errMsg, "")
					row, plan := screen.statusPlan()
					if !strings.Contains(row, errMsg) {
						t.Errorf("%s at 80x%d: a %d-cell answer pushed the order-level "+
							"error off the status row.\n\terror: %q\n\trow: %q",
							b.c.name, h, lipgloss.Width(longLead), errMsg, row)
					}
					if !plan.drawsHead {
						t.Errorf("%s at 80x%d: the row is the failure headline but the "+
							"plan says it is not, so failLines will draw a second copy",
							b.c.name, h)
					}
					drove++
					// And on the pane the terminal really shows, not just in
					// the string the composer returned.
					poWantPaneLine(t, screen, errMsg)
					poAssertFits(t, b.c.name+" under an order-level error", screen)
				}
			})
		}
	}
}

// TestPOCreate_AnOrderLevelFailureDoesNotOutliveThePhaseItHappenedOn is what
// makes the rule above SOUND rather than a trade in a new place.
//
// While an errMsg stands the status row is the error alone, so a picker's
// answer falls back to answerRows — a CONTEXT row of the pinned header, and the
// first rank jdeFitHeader gives ground in. At 80x11 through 80x13 that puts it
// off the pane, which is exactly the byte-identical-frame state this branch
// exists to remove.
//
// That state was PERMANENT: setErr's only other writers are enterLinePhase,
// removeLineAt and addReorderLines, so one failed submit carried the error onto
// the source chooser, both pickers and the line form for the rest of the
// session. The clear at the phase-change site is what makes it UNREACHABLE —
// not merely brief — because no setErr writer fires on a picker phase and the
// pending freeze stops one being entered with a POST out. This test is the
// guard on that clear: remove it and the state comes back, which is why the
// composition rule in statusPlan is kept and guarded separately.
//
// Driven through the real submit and the real keys, and asserted on the clipped
// pane at the three heights the report was filed about.
func TestPOCreate_AnOrderLevelFailureDoesNotOutliveThePhaseItHappenedOn(t *testing.T) {
	for _, h := range []int{11, 12, 13} {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{catalog: 2, assets: 1, failCreate: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = poStageCostlessLine(t, r, screen)
			r = key(t, r, poPhaseKeyMsg("d"))
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit → 502
			if screen.errMsg == "" {
				t.Fatalf("setup: the submit came back without recording a failure")
			}
			if screen.phase != poPhaseReview {
				t.Fatalf("setup landed on phase %v, want review", screen.phase)
			}

			// esc to the chooser, then into the item picker and open its filter
			// — the box that takes the essential header row, so the answer has
			// nowhere but the status row to go.
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			if screen.errMsg != "" || screen.errDetail != "" {
				// Errorf, not Fatalf: the pane assertions below are the ones
				// that show what the operator loses, and they must still run.
				t.Errorf("the failure outlived the phase it happened on: %q / %q",
					screen.errMsg, screen.errDetail)
			}
			r = key(t, r, poPhaseKeyMsg("i"))
			r = key(t, r, poPhaseKeyMsg("/"))
			if screen.essentialBoxRow() == "" {
				t.Fatalf("the item filter did not open at 80x%d", h)
			}
			_ = r
			if poFrameRefused(t, screen, h) {
				t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
			}

			pane := strings.Join(poPaneLinesAt(t, screen, h), "\n")
			if strings.Contains(pane, poSubmitFailWords) {
				t.Errorf("the item picker at 80x%d still reports the submit that failed "+
					"two phases ago:\n%s", h, pane)
			}
			// The answer to the key that opened the box is on the pane, which
			// is the property the error was displacing.
			answer, _, _ := strings.Cut(poNoteText(screen), poLeadJoint)
			if answer == "" {
				t.Fatalf("`/` was answered with nothing at 80x%d", h)
			}
			if !strings.Contains(pane, answer) {
				t.Errorf("the item filter at 80x%d does not carry its answer %q:\n%s",
					h, answer, pane)
			}
			poAssertFits(t, fmt.Sprintf("item filter after a failed submit at 80x%d", h), screen)
		})
	}
}

// TestPOFailure_TheHeaderNeitherRepeatsNorSilentlyCutsTheHeadline covers the two
// halves of failLines' lead, on the narrowest terminal Root will draw.
//
// The header carries the failure HEADLINE only when the status row is drawing
// something else — the work in flight, or an answer. Asked as a substring of
// the assembled row, that question also answered "no" when the row WAS drawing
// the headline and merely shortened it, so a narrow pane got a second,
// identically shortened copy of one sentence and spent a body row on it.
//
// And where the header's own copy is cut, the cut is MARKED. Every other bound
// on this screen carries its ellipsis; a headline cut clean reads as a finished
// sentence, and at 45 columns — where contentWidth is exactly the 20 Root
// refuses below — "looking up this supplier's items failed" ends mid-word
// looking complete.
func TestPOFailure_TheHeaderNeitherRepeatsNorSilentlyCutsTheHeadline(t *testing.T) {
	// 60 columns leaves the pane 31, which is where the reported cut was
	// measured. 80 is the width that must HOLD and every headline fits it; a
	// terminal the operator has dragged narrow is where both halves bite.
	const width = 60

	reach := func(t *testing.T) (Root, *PurchaseOrderCreateScreen) {
		t.Helper()
		r, screen := poPickerAtSize(t, &poPickFake{catalog: 2}, width, 24)
		r = key(t, r, poPhaseKeyMsg("i"))
		if screen.phase != poPhaseItemPick {
			t.Fatalf("setup landed on phase %v, want the item picker", screen.phase)
		}
		screen.itemSuppliersErr = "oms: http 502: upstream is not answering"
		// The picker's own note would outrank the headline on the status row
		// and put this on a third branch; what is under test is the headline.
		screen.itemSuppliersNote.clear()
		return r, screen
	}

	head, _ := func() (string, string) {
		_, s := reach(t)
		return s.failure()
	}()
	if head == "" {
		t.Fatal("setup recorded no failure headline, so neither half is asserted")
	}
	// A probe short enough to survive the narrowest clip on either surface.
	probe := cellPrefix(head, 20)

	t.Run("the row is drawing it, so the header does not", func(t *testing.T) {
		_, screen := reach(t)
		if screen.workingSubject() != "" || poNoteText(screen) != "" {
			t.Fatalf("something outranks the headline, so the row is not drawing it")
		}
		lines := poPaneLinesAt(t, screen, 24)
		seen := 0
		for _, line := range lines {
			if strings.Contains(line, probe) {
				seen++
			}
		}
		if seen != 1 {
			t.Errorf("the %d-column pane says %q on %d lines, want exactly 1 — "+
				"the status row is drawing it and the header repeated it:\n%s",
				width, probe, seen, strings.Join(lines, "\n"))
		}
		poAssertFits(t, "item picker failing at a narrow terminal", screen)
	})

	t.Run("the row is drawing something else, so the header marks its cut", func(t *testing.T) {
		_, screen := reach(t)
		// A request in flight outranks the headline, so the header picks it up.
		screen.itemSuppliersLoad = true
		if screen.workingSubject() == "" {
			t.Fatal("nothing is in flight, so the row would draw the headline itself")
		}
		lines := poPaneLinesAt(t, screen, 24)
		carried := ""
		for _, line := range lines {
			if strings.Contains(line, probe) {
				carried = line
			}
		}
		if carried == "" {
			t.Fatalf("the header dropped the headline the status row is not drawing:\n%s",
				strings.Join(lines, "\n"))
		}
		if strings.Contains(carried, head) {
			t.Skipf("the headline fits the %d-column pane whole (%q), so there is no "+
				"cut to mark", width, carried)
		}
		if !strings.Contains(carried, "…") {
			t.Errorf("the header cut the headline at %d columns without marking it, so "+
				"it reads as a finished sentence: %q", width, carried)
		}
		poAssertFits(t, "item picker failing mid-reload at a narrow terminal", screen)
	})
}

// poAssetQueryHeadCells is how much of an operator's search term the status
// row has to keep for the row to be worth reading at all.
//
// TWELVE, and the number is argued rather than measured off the code. Ordinary
// MRO search terms share their leading word — "hydraulic pump" and "hydraulic
// hose" first differ at cell 11 — so a head that stops before there names
// neither, and an operator watching a slow search cannot tell which of two
// things they asked for is out. Twelve clears that with a cell to spare.
//
// It is NOT poLeadOnto's arithmetic restated. A test that recomputes the bound
// it is checking is a second implementation of it, and the two drift; this is a
// floor the ROW must clear, so shortening the fixed words passes it and
// lengthening them fails it, whatever the reservation happens to be that day.
const poAssetQueryHeadCells = 12

// TestPOAssetSearch_TheQuerySurvivesTheLeadOnTheStatusRow drives the sequence
// the asset picker's working sentence is cut on, from the seat.
//
// `a`, `/`, type a query, Enter, `/` again — and that last press is ordinary,
// not contrived: the bar names `/` on the working frame, so re-opening the box
// while the server-side search is still out is what an operator does when they
// realise they mistyped. openAssetSearch then writes a 26-cell opening clause,
// which is over poLeadOnto's half-row cap, so the subject is bounded to the
// 23-cell floor at 80 columns.
//
// The subject used to be `Searching ` + supplier + `'s assets for "zzz"…`, and
// at that floor the row drew `Searching Acme Supply'…`: the SUPPLIER kept — the
// same on every phase of this screen, and pinned on a header row of its own —
// and the QUERY, the only thing saying what this lookup is, gone. Rule 6
// inverted inside one sentence. The fixed words lead now, the query comes next
// and the supplier is the tail.
//
// WATCHED TO FAIL TWICE, and the second time is the point. Against the ORIGINAL
// supplier-first subject it failed on the query being absent outright. Against
// the FIRST reorder — fixed words `Searching assets `, 17 of the floor's 23 —
// it failed on the head: five cells were left for the quote and the query, so
// the row drew `Searching assets "hydr…` and a reorder that had moved the query
// to the front still could not say which pump was being looked for. A fixture
// sitting ON the limit hid that for a round: `zzz` is exactly the longest query
// those 17 cells preserved whole, so the check passed for a reason unrelated to
// the property it names. The fixture below is an ordinary MRO term PAST the
// limit, which is this project's vacuous-fixture rule pointed the right way.
//
// Only the STATUS ROW is asserted. The header pins a `Showing ..... "…"` row
// carrying the same query, so a bare "is the query on the pane" check is
// answered by a different row entirely and stays green with the status row
// empty; every assertion here is anchored on the fixed words, which only this
// row draws.
func TestPOAssetSearch_TheQuerySurvivesTheLeadOnTheStatusRow(t *testing.T) {
	// Past what the row can hold, so the bound under test really bites.
	const query = "hydraulic pump seal"

	for _, h := range poDrawableHeights() {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			r, screen := poPickerAtSize(t, &poPickFake{catalog: 2, assets: 3}, 80, h)
			r = key(t, r, poPhaseKeyMsg("a"))
			if screen.phase != poPhaseAssetPick {
				t.Fatalf("a landed on phase %v, want the asset picker", screen.phase)
			}
			r = key(t, r, poPhaseKeyMsg("/"))
			r = poType(t, r, query)

			// Raw Update, not key(): the search must still be OUT when the row
			// is read, and key() pumps the request to its reply.
			next, _ := r.Update(tea.KeyMsg{Type: tea.KeyEnter})
			r = next.(Root)
			if !screen.assetsLoading || strings.TrimSpace(screen.assetsQuery) != query {
				t.Fatalf("enter left loading=%v query=%q, want a search out on %q",
					screen.assetsLoading, screen.assetsQuery, query)
			}

			// Re-open the box over the lookup: this is what puts a lead on the
			// row beside the working sentence.
			next, _ = r.Update(poPickerKeyMsg("/"))
			r = next.(Root)
			_ = r
			if screen.essentialBoxRow() == "" {
				t.Fatalf("/ did not re-open the search box over the lookup")
			}
			if poNoteText(screen) == "" {
				t.Fatalf("/ over a lookup answered with nothing, so no lead is composed")
			}

			if poFrameRefused(t, screen, h) {
				t.Skip("the layer refuses this frame, which is jdeTooShort's rule")
			}

			// The FIXED WORDS survive whole — they are what the operator reads
			// to know a search is what is out — and the head of the query comes
			// with them. Both anchored on the same substring, so neither can be
			// satisfied by the header's Showing row.
			head := poAssetSearchWords + strconv.Quote(query)
			head = cellPrefix(head, lipgloss.Width(poAssetSearchWords)+1+poAssetQueryHeadCells)
			row, _ := screen.statusPlan()
			if !strings.Contains(row, head) {
				t.Errorf("the status row at 80x%d does not name the search and the term "+
					"it is running on.\n\twant it to carry: %q\n\tsubject: %q\n\trow: %q",
					h, head, screen.workingSubject(), row)
			}
			// And on the pane the terminal really draws, not just the string the
			// composer returned.
			poWantPaneLine(t, screen, head)
			poAssertFits(t, fmt.Sprintf("asset search mid-lookup at 80x%d", h), screen)
		})
	}
}
