package tui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// A DESTRUCTIVE CONFIRM WITH ITS WRITE IN FLIGHT IS A STATE OF ITS OWN, AND IT
// IS THE ONE NO DERIVED SWEEP IN THIS PACKAGE REACHES.
//
// jdeScreenStates builds each confirm freshly opened, so every bar-honesty sweep
// measures the frame BEFORE the operator presses the destroy key — and that is
// the half of the frame's life the bar changes shape in. Both confirms got the
// rule wrong there, in opposite directions, and neither was reported:
//
//   - THE LINE-DELETE CONFIRM acted without naming. deleteBar collapsed to
//     {Esc=Back} while the delete was out and RETURNED, so the movement tokens
//     came off the bar, while deleteScrolls — which never mirrored that branch —
//     went on answering true and the arm went on scrolling the caveat. A key
//     acting while the bar names nothing is standing rule 2 in the direction an
//     operator cannot even see to complain about. Worse at the heights where the
//     shorter drawn bar left the body room the taller MEASURED bar did not: the
//     arm bumped the offset, ClampScroll put it straight back, no note was set,
//     and the pane came back byte for byte — rule 1.
//   - THE ATTACHMENT-DELETE CONFIRM named without acting. updateConfirmDelete
//     returned early for EVERY key while deleting, so Enter, Esc and the three
//     movement tokens the bar went on spelling were all inert.
//
// So this presses the keys at the panes Root draws with the write really in
// flight — reached by pressing the destroy key, not by setting the flag — and
// asserts the biconditional the rest of the fleet is held to, plus the two
// claims that are specific to a frame mid-write: the destroy key is not named
// while it will not write, and a body that says it is hiding something still
// names a key that fetches it.
//
// It is a small, targeted walk rather than two more members of jdePaneCases on
// purpose: every sweep in this package walks that set at every width and every
// drawable height, and the package is already inside sight of go test's 600s
// per-package timeout.
func poMidWriteConfirms() []struct {
	name    string
	destroy string
	mk      func() Screen
} {
	return []struct {
		name    string
		destroy string
		mk      func() Screen
	}{
		{
			name:    "PurchaseOrderEditScreen/delete confirm mid-write",
			destroy: "Ctrl-X",
			mk: func() Screen {
				s := NewPurchaseOrderEditScreen(Deps{}, poDeletablePO())
				s.openLineEditor(0)
				s.lineFocus = poLineRowStatus
				s.openDeleteLine(0)
				// The write goes out through the key an operator presses, so a
				// change that stopped ctrl+x reaching this state fails here
				// rather than leaving the sweep measuring a flag nothing sets.
				s.Update(tea.KeyMsg{Type: tea.KeyCtrlX})
				return s
			},
		},
		{
			name:    "PurchaseOrderAttachmentsScreen/delete confirm mid-write",
			destroy: "Enter",
			mk: func() Screen {
				po := poViewPO()
				po.Attachments = []omsapi.PurchaseOrderAttachment{{
					ID: 1, FileName: "quote-2026-01.pdf",
					Description:    "Vendor quotation for the whole order, itemised by line",
					UploadedByName: "shop.lead",
				}}
				s := NewPurchaseOrderAttachmentsScreen(Deps{}, po)
				s.confirmingDelete = true
				s.Update(tea.KeyMsg{Type: tea.KeyEnter})
				return s
			},
		},
	}
}

// TestJDEConfirm_AWriteInFlightLeavesTheBarHonest holds all four claims at every
// pane Root draws.
func TestJDEConfirm_AWriteInFlightLeavesTheBarHonest(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	inFlight, named, moved := 0, 0, 0
	problems := map[string][]string{}
	note := func(key, pane string) { problems[key] = append(problems[key], pane) }

	for _, c := range poMidWriteConfirms() {
		if !poWriteIsOut(c.mk()) {
			t.Fatalf("%s does not have a write in flight after its destroy key, so every "+
				"assertion below is about the resting frame and this check is vacuous",
				c.name)
		}
		for _, w := range jdePaneWidths {
			for _, h := range jdePaneHeights() {
				s := c.mk()
				before := jdeClippedPane(s, w, h)
				bar := jdeBarOfStripped(s.View())
				if bar == nil {
					continue // refused: the notice replaces the bar
				}
				inFlight++
				on := map[string]bool{}
				for _, tok := range jdeBarTokens(bar) {
					on[tok] = true
				}
				pane := fmt.Sprintf("%dx%d", w, h)

				// (1) The destroy key is not named while it will not write.
				if on[c.destroy] {
					note(c.name+" names "+c.destroy+" while the write is out", pane)
				}
				// (2) Nothing says there is more of the body without a key to fetch it.
				if jdeDrawsMoreMarker(before) && !jdeBarNamesAMovementKey(s.View()) {
					note(c.name+" draws a more-marker over a bar naming no movement key", pane)
				}
				// (3) and (4): the biconditional, and the two halves are asked
				// with DIFFERENT instruments because they are different claims.
				//
				// FORWARD — a named token must move what the operator SEES — is
				// measured on the clipped PANE, which is the only thing that can
				// answer rule 1.
				//
				// REVERSE — a key that acts must be named — is measured on
				// jdePlaceOf, the screen's own record of where the operator is.
				// It cannot be the pane: an arm that DECLINES AND ANSWERS
				// changes the pane by design (the note saying "the whole warning
				// is on the pane" is what stops the press being silent), and a
				// key that declines and says why has not ACTED. Measured on the
				// pane this reported both confirms at every height their caveat
				// fits, which is the frame behaving exactly as the layer's
				// Movement block requires.
				place := jdePlaceOf(s)
				for token, keys := range jdeMoveTokens {
					someMoved := false
					for _, k := range keys {
						probe := c.mk()
						jdeClippedPane(probe, w, h)
						if next, _ := probe.Update(poPickerKeyMsg(k)); next != nil {
							probe = next
						}
						if jdeClippedPane(probe, w, h) != before {
							someMoved = true
						}
						if !reflect.DeepEqual(jdePlaceOf(probe), place) && !on[token] {
							note(c.name+" moves the operator's place on "+k+
								" while its bar does not name "+token, pane)
						}
					}
					if on[token] {
						named++
						if !someMoved {
							note(c.name+" names "+token+" and neither key it spells moves the pane", pane)
						}
					}
					if someMoved {
						moved++
					}
				}
			}
		}
	}

	switch {
	case inFlight == 0:
		t.Fatal("no mid-write confirm drew a bar at any pane, so this check asserted " +
			"nothing about either direction of the rule")
	case named == 0:
		t.Fatal("no mid-write confirm named a movement token at any pane, so the " +
			"named-and-dead half of this check could not fire")
	case moved == 0:
		t.Fatal("no key moved the pane of a mid-write confirm at any pane, so the " +
			"acts-while-unnamed half of this check could not fire")
	}

	var keys []string
	for k := range problems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		panes := problems[k]
		t.Errorf("%s, at %d pane(s) including %v", k, len(panes), panes[:min(4, len(panes))])
	}
}

// poWriteIsOut reads the working line off the screen's own status row rather
// than the private flag behind it: the flag is what the bar and the arms read,
// and a fixture that set it without the frame reporting anything in flight would
// be a state no operator is ever in.
func poWriteIsOut(s Screen) bool {
	if next, _ := s.Update(tea.WindowSizeMsg{Width: 120, Height: 40}); next != nil {
		s = next
	}
	return strings.Contains(stripANSI(s.View()), "Deleting…")
}

// TestJDEConfirm_ADeclineMidWriteIsNotDrawnAsAFailure: the answer to an inert
// key is a benign answer, and a destructive confirm is the last frame on which
// to dress one as a failure.
//
// po_attachments handed deleteNote to statusRow's THIRD argument, which is the
// FAILURE slot — "✗ " in StyleStatusError — so "j does nothing here" was drawn
// in the same red the server refusing the delete would be, on the frame where
// the difference decides whether the operator presses Enter.
func TestJDEConfirm_ADeclineMidWriteIsNotDrawnAsAFailure(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	po := poViewPO()
	po.Attachments = []omsapi.PurchaseOrderAttachment{{
		ID: 1, FileName: "quote-2026-01.pdf", UploadedByName: "shop.lead",
	}}
	s := NewPurchaseOrderAttachmentsScreen(Deps{}, po)
	s.confirmingDelete = true
	s.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	s.Update(poPickerKeyMsg("j"))

	view := s.View()
	if !strings.Contains(stripANSI(view), "j does nothing here") {
		t.Fatalf("the confirm did not answer an inert key, so this check asserts "+
			"nothing about how the answer is drawn:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		plain := stripANSI(line)
		if !strings.Contains(plain, "j does nothing here") {
			continue
		}
		if strings.Contains(plain, strings.TrimSpace(jdeStatusErrMark)) {
			t.Errorf("the decline is drawn behind the failure mark %q: %q",
				jdeStatusErrMark, plain)
		}
		if strings.Contains(line, poStyleOpener(StyleStatusError)) {
			t.Errorf("the decline is drawn in the failure colour: %q", line)
		}
	}
}

// poStyleOpener is the escape sequence a style opens with, taken from the style
// ITSELF rather than transcribed: a palette change would otherwise leave the
// check comparing against a colour nothing draws.
func poStyleOpener(style lipgloss.Style) string {
	rendered := style.Render("x")
	if i := strings.Index(rendered, "x"); i > 0 {
		return rendered[:i]
	}
	return ""
}
