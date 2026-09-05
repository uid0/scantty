// What a columnar frame may PROMISE, and what it may NAME.
//
// One rule, asked of two surfaces: A PANE THAT SAYS THERE IS MORE MUST NAME A
// KEY, AND A KEY IT NAMES MUST MOVE THE PANE. Composed, those two give the
// operator a way to the next screenful of any body the frame admits it is
// hiding — which is standing rule 11 (a hint is only legitimate when the
// operator can act on it) sitting on top of standing rule 2.
//
// Both halves were broken, in different places, by the same blind spot: the
// layer's window is positioned by a CURSOR, and jdeLines.block() answers (0,0)
// for a row that owns no line, so a body nothing navigates is PINNED at line 0
// for as long as the frame is up.
//
//   - THE PROMISE. The purchase-order line-DELETE confirm takes no reason, so
//     its body is prose and nothing stands on it. Its caveat folds to three
//     lines at 80 columns and nine at 45, and a declining keypress adds two
//     more, so at 80x15, 60x15 and 45x16 the pane drew `↓ N more below` over
//     prose no key could fetch — on the frame where the next keystroke destroys
//     a line. The attachment-DELETE confirm was worse: at 80x14 the heading was
//     above the fold and the whole "cannot be undone" warning below it, with
//     `Enter=Delete` on the bar. The order-VOID prompt hid the half of its
//     sentence that says the void CASCADES TO EVERY LINE.
//   - THE NAMING. The slot-generate RUN REPORT named `UP/DN=Scroll`
//     unconditionally over a read-only body, so at every pane the report FITS —
//     61 of them at 80, 100 and 120 columns, 80x24 among them — the key moved an
//     int and redrew the pane byte for byte.
//
// The naming half is checked HERE and not by
// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves, which has the same
// forward implication, because that sweep measures jdePlaceOf — a fingerprint of
// the screen's own int fields. That is the right instrument for its other half
// (a position that drifted and happened to redraw the same is exactly what a
// fingerprint catches and a frame comparison does not), and it is the wrong one
// for this: standing rule 1 is about a change the OPERATOR can distinguish, and
// only the rendered pane can answer that. The run report moved an int on every
// press and the fingerprint called it alive.
package tui

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
)

// jdeMoreMarkerWords are the two claims jdeLines.Window and WindowFrom draw when
// a body outruns its window. Transcribed from those two functions and nowhere
// else; a wording changed there and not here would make this sweep pass in
// silence, which is why TestJDEForm_TheMoreMarkersAreTheOnesTheLayerDraws exists
// beside it.
var jdeMoreMarkerWords = []string{"more above", "more below"}

// TestJDEForm_TheMoreMarkersAreTheOnesTheLayerDraws: the words above are the
// words the layer really prints.
//
// A sweep keyed on a marker the layer stopped drawing is a sweep that reports
// nothing, and nothing about that failure is visible in a diff — the vacuous
// fixture rule (AGENTS.md) with the string in place of the fixture. So the
// markers are produced by driving a body PAST its window and read back off the
// lines, rather than trusted.
func TestJDEForm_TheMoreMarkersAreTheOnesTheLayerDraws(t *testing.T) {
	l := &jdeLines{}
	for i := 0; i < 12; i++ {
		l.AddRow(i, fmt.Sprintf("row %d", i))
	}
	// Cursor in the middle, so BOTH markers are drawn at once.
	lines, _ := l.Window(6, 5)
	got := stripANSI(strings.Join(lines, "\n"))
	for _, want := range jdeMoreMarkerWords {
		if !strings.Contains(got, want) {
			t.Errorf("jdeLines.Window no longer draws %q, so every check in this file "+
				"keyed on it passes over the state it exists for:\n%s", want, got)
		}
	}
	from := stripANSI(strings.Join(l.WindowFrom(4, 5), "\n"))
	for _, want := range jdeMoreMarkerWords {
		if !strings.Contains(from, want) {
			t.Errorf("jdeLines.WindowFrom no longer draws %q:\n%s", want, from)
		}
	}
}

// jdeClippedPane sizes a screen and returns the pane the operator really reads:
// the screen's own View, clipped exactly as Root.View clips it.
//
// It is not jdeRootAt + Root.View, and the difference is COST rather than
// meaning. Root re-renders the nav column, the title and the status bar for
// every call, and the promise sweep below walks every case at every drawable
// WIDTH — which is the axis the fifth stranded case was found on, so it is not
// an axis to give up. Building the pane directly is the same string for the
// screen's rows and leaves the package inside go test's per-package timeout,
// which internal/tui has already hit once (AGENTS.md).
//
// The two numbers are Root's own, not screenBodyWidth / screenBodyHeight: both
// of those floor, and a floor is a LIE at small panes — screenBodyWidth answers
// 20 where Root gives 16, which would leave four cells of a marker on a pane
// that never had them.
func jdeClippedPane(s Screen, w, h int) string {
	if next, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: h}); next != nil {
		s = next
	}
	return clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h))
}

// jdeDrawsMoreMarker reports whether this clipped pane admits it is hiding part
// of the body.
func jdeDrawsMoreMarker(pane string) bool {
	plain := stripANSI(pane)
	for _, w := range jdeMoreMarkerWords {
		if strings.Contains(plain, w) {
			return true
		}
	}
	return false
}

// jdeBarOfStripped is jdeBarOf with the ANSI removed first.
//
// It exists because jdeBarOf anchors on the bar's RULE — a run of hyphens the
// width of the pane — and that run is STYLED. With lipgloss stripping colour, as
// it does in a test binary that is not a TTY, the anchor matches; with a profile
// FORCED, as the naming sweep below has to force it, every line carries escape
// codes and jdeBarOf finds no bar at all and reports every screen as drawing
// none. That is not a hypothetical: the first draft of that sweep went green
// over every case in the package for exactly this reason.
func jdeBarOfStripped(view string) []string {
	lines := strings.Split(view, "\n")
	plain := make([]string, len(lines))
	for i, ln := range lines {
		plain[i] = stripANSI(ln)
	}
	return jdeBarOf(strings.Join(plain, "\n"))
}

// jdeBarNamesAMovementKey reports whether this frame's bar spells any token in
// the movement vocabulary. Read off the BAR, through the same jdeMoveTokens
// table the other movement sweeps read, so a token added there is asked about
// here without anybody remembering to.
func jdeBarNamesAMovementKey(view string) bool {
	bar := jdeBarOfStripped(view)
	if bar == nil {
		return false
	}
	for _, tok := range jdeBarTokens(bar) {
		if _, ok := jdeMoveTokens[tok]; ok {
			return true
		}
	}
	return false
}

// jdeUnfetchableMarkerCases are the (screen, state) pairs that still draw a
// marker with no movement key named, WITH THE REASON — so "absent" and "excused"
// stay different states and a stale entry fails as loudly as a missing one.
//
// After this work they are ONE MECHANISM, and it is the one no key can help
// with: a body whose SINGLE navigable row owns a block taller than the window.
// jdeLines.Window keeps a block's START and nothing scrolls inside one, so what
// a short pane loses is that block's tail — the stated sacrifice AGENTS.md
// records for addLineBlock — and a cursor cannot move within a block it is
// already standing in, so there is no key to name. The marker there is a fact
// about the terminal's HEIGHT, not a dead end wearing an affordance: what it
// says is "grow the window", and the block is ordered so that what a short pane
// keeps is what the operator cannot do without.
//
// The class this map USED to hold is gone: a body whose LEAD-IN belonged to no
// navigable row, stranded above a cursor that cannot go higher than its first
// row. Five (screen, state) pairs were in it — the add-line identify phase, the
// attachments grid with one file, and the kit, chain and level lists in the
// empty state each list opens in — and each is fixed the same way, by pinning
// the lead-in in the frame's HEADER, where jdeFitHeader trims by rank and claims
// nothing about what it dropped.
//
// WHAT IS STILL OPEN, said plainly because a claim no check delivers is worse
// than no claim: that strand also exists on some forty other (screen, state)
// pairs whose list has TWO OR MORE rows, and this sweep cannot see them, because
// their bar honestly names UP/DN and the implication it tests is satisfied. The
// operator there has a key that moves and still cannot reach the heading above
// the first row. Closing it means the same header conversion on every columnar
// screen at once — the shape of sc-jde-lift, not a patch.
var jdeUnfetchableMarkerCases = map[string]string{
	"PurchaseOrderAddLineScreen": "the identify phase is ONE navigable row — the scan box — " +
		"and its hint folds onto several lines below it at narrow widths, so from 52 to 59 " +
		"columns on a short pane the row's own block outruns a one-line window (12 panes). Nothing " +
		"leads it any more (identifyHeader pins the heading and both identity rows), and " +
		"no key can move a cursor inside the block it is already on.",
}

// TestJDEForm_NoFrameClaimsContentWithoutNamingAKeyToFetchIt: at every pane Root
// draws, a frame that says there is more names a key that moves.
//
// DERIVED on every axis: the cases from jdePaneCases (every type embedding
// jdeScreen, plus its extra states), the widths and heights from the layer's own
// drawable range, the markers from the layer's own two window functions, and the
// movement vocabulary from jdeMoveTokens.
//
// It asks about NAMING and not about reaching the very last line, and that
// boundary is deliberate. The other half of the rule —
// TestJDEForm_EveryMovementTokenIsNamedExactlyWhereItMoves — already holds that
// a named token moves something, so naming plus moving gets the operator to the
// next screenful and, by repetition, through the body. What it does NOT promise
// is a block TALLER than the window: jdeLines.Window keeps a block's START and
// nothing scrolls inside one, so a single field with four lines of context under
// it on a one-line body loses its tail whatever key is pressed. That is the
// stated sacrifice AGENTS.md records for addLineBlock, it is a fact about the
// terminal's height rather than a missing key, and a sweep demanding otherwise
// would be demanding something no arrangement of one block can give.
func TestJDEForm_NoFrameClaimsContentWithoutNamingAKeyToFetchIt(t *testing.T) {
	claimed, silent := 0, map[string][]string{}
	drew := map[string]bool{}
	// HOISTED, because each of these builds a Root per candidate size to ask
	// Root's own gate: left in the inner loop they were re-derived once per
	// (case, width) and cost more than the sweep itself.
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range widths {
			for _, h := range heights {
				s := mk()
				pane := jdeClippedPane(s, w, h)
				if !jdeDrawsMoreMarker(pane) {
					continue
				}
				drew[name] = true
				claimed++
				if jdeBarNamesAMovementKey(s.View()) {
					continue
				}
				silent[name] = append(silent[name], fmt.Sprintf("%dx%d", w, h))
			}
		}
	}
	if claimed == 0 {
		t.Fatal("no columnar frame drew a more-above / more-below marker at any pane " +
			"Root draws, so this sweep asserted nothing. Either every body now fits " +
			"every pane or the markers have been renamed; both need this rewritten " +
			"rather than deleted")
	}

	var names []string
	for name := range silent {
		names = append(names, name)
	}
	for name := range jdeUnfetchableMarkerCases {
		if _, bad := silent[name]; !bad {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		panes, bad := silent[name]
		reason, excused := jdeUnfetchableMarkerCases[name]
		switch {
		case bad && !excused:
			t.Errorf("%s says there is more of its body and its bar names no movement "+
				"key at all, at %d pane(s) including %v. The operator is looking at a "+
				"frame that admits it is hiding something and offers nothing to press: "+
				"standing rule 11, on a pane that cannot be argued with",
				name, len(panes), panes[:min(4, len(panes))])
		case excused && !bad && drew[name]:
			t.Errorf("%s is recorded in jdeUnfetchableMarkerCases (%q) but every marker it "+
				"draws now comes with a movement key named. A stale exception is a case "+
				"excused from the check it passes", name, reason)
		case excused && !bad && !drew[name]:
			t.Errorf("%s is recorded in jdeUnfetchableMarkerCases (%q) but it draws no marker "+
				"at any pane, so there is nothing here to excuse. An entry describing no "+
				"behaviour passes in silence, which is the shape of stale roster this "+
				"project keeps being bitten by", name, reason)
		}
	}
}

// TestJDEForm_AScrolledBodyReallyReachesItsLastLine: where a frame offers
// Home/End over a body it admits it is hiding, End must actually get there.
//
// This is the END-TO-END half, driven through the real screen at every pane Root
// draws: the two sweeps above hold that a marker comes with a named key and that
// a named key moves the pane, and neither of them says the operator ever ARRIVES
// — a key that moves one line at a time past a bound that clamps early would
// satisfy both while the last line of a destroy confirm's warning stayed off the
// pane for ever.
//
// Home/End is the token asked about because it is the one that makes a claim
// about the ENDS. It is DERIVED, not listed: whichever frames spell it are the
// frames swept, so the two removal confirms this change converted are covered
// alongside the order pad, the detail sheet and the add-line confirm that
// already spelled it.
func TestJDEForm_AScrolledBodyReallyReachesItsLastLine(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	reached := 0
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range widths {
			for _, h := range heights {
				s := mk()
				pane := jdeClippedPane(s, w, h)
				if !strings.Contains(stripANSI(pane), "more below") {
					continue
				}
				bar := jdeBarOfStripped(s.View())
				if bar == nil {
					continue
				}
				named := false
				for _, tok := range jdeBarTokens(bar) {
					if tok == "Home/End" {
						named = true
					}
				}
				if !named {
					continue
				}
				if next, _ := s.Update(poPickerKeyMsg("end")); next != nil {
					s = next
				}
				after := stripANSI(jdeClippedPane(s, w, h))
				reached++
				if strings.Contains(after, "more below") {
					t.Errorf("%s at %dx%d names Home/End over a body it says it is hiding, "+
						"and End does not reach the end of it — the pane still reads "+
						"\"more below\":\n%s", name, w, h, after)
				}
			}
		}
	}
	if reached == 0 {
		t.Fatal("no frame drew a more-below marker while naming Home/End at any pane, so " +
			"this check asserted nothing. Either no body scrolls any more or the token " +
			"has been reworded; both need this rewritten rather than deleted")
	}
}

// TestJDEForm_EveryMovementTokenMovesTheOperatorsPANE: a bar that spells a
// movement token must have a key that changes what the operator SEES.
//
// This is the forward half of the bar-honesty rule measured through the render
// rather than through jdePlaceOf, and it is a second walk of the cases for
// exactly one reason: the two instruments answer differently, and the
// difference is where a defect lived. The slot-generate run report is a
// read-only body drawn with no highlight, so moving its cursor changed an int
// and nothing else; the fingerprint sweep called that alive at all 61 of the
// panes this one reports, and the bar went on naming `UP/DN=Scroll` there.
//
// THE COLOUR PROFILE IS FORCED, and without it this check reports honest
// screens: lipgloss strips every escape when stdout is not a TTY, which is
// always in a test binary, so a cursor moving between two value rows renders
// byte-identically and a form that is behaving perfectly looks dead
// (AGENTS.md's colour rule). Forcing it also breaks jdeBarOf's anchor, which is
// what jdeBarOfStripped is for.
//
// THE WIDTHS ARE jdePaneWidths AND NOT EVERY DRAWABLE ONE, which is a narrowing
// and is recorded as one. At the 45-column floor a picker row has sixteen cells,
// so two different catalogue items both draw as `▸ Hex b…  AF-`: the key moves
// the cursor AND the window, and the pane is unchanged because the two rows are
// clipped to the same string. That is standing rule 5's width form — a value cut
// until it stops identifying anything — and not a bar naming a dead key, so a
// sweep about bars must not report it. The three New PO pickers are where it
// shows.
func TestJDEForm_EveryMovementTokenMovesTheOperatorsPANE(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	named, dead := 0, map[string][]string{}
	heights := jdePaneHeights()
	for _, c := range jdePaneCases() {
		name, mk := c.name, c.mk
		for _, w := range jdePaneWidths {
			for _, h := range heights {
				s := mk()
				before := jdeClippedPane(s, w, h)
				bar := jdeBarOfStripped(s.View())
				if bar == nil {
					continue // refused: the notice replaces the bar
				}
				on := map[string]bool{}
				for _, tok := range jdeBarTokens(bar) {
					on[tok] = true
				}
				for token, keys := range jdeMoveTokens {
					if !on[token] {
						continue
					}
					named++
					// The keys a token spells are pressed one at a time from the
					// rest state and the claim is that SOME of them moves — the
					// granularity the bars have always spelled these at, and why
					// a list edge stays silent rather than declining out loud.
					moved := false
					for _, k := range keys {
						probe := mk()
						jdeClippedPane(probe, w, h)
						if next, _ := probe.Update(poPickerKeyMsg(k)); next != nil {
							probe = next
						}
						if jdeClippedPane(probe, w, h) != before {
							moved = true
							break
						}
					}
					if !moved {
						dead[name+" "+token] = append(dead[name+" "+token],
							fmt.Sprintf("%dx%d", w, h))
					}
				}
			}
		}
	}
	if named == 0 {
		t.Fatal("no columnar bar spelled a movement token at any pane, so this sweep " +
			"asserted nothing")
	}
	var keys []string
	for k := range dead {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		panes := dead[k]
		t.Errorf("%s is on the bar and neither key it spells changes the pane, at %d "+
			"pane(s) including %v. A key named where it cannot be seen to act is the "+
			"bar-honesty rule broken in the direction an operator feels as a wedged "+
			"program", k, len(panes), panes[:min(4, len(panes))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
