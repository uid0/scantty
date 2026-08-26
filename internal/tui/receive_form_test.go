package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/uid0/scantty/internal/omsapi"
)

// What the receiving screen has to be true of, asserted through the frame Root
// really draws.
//
// Every check here reads the CLIPPED render — clampToBox(Root.View(), pane) —
// and not the screen's own View(). That distinction is the whole reason several
// of these defects survived: clampToBox truncates in Root, not in the screen,
// so a test that reads View() passes over a line the terminal cuts in half and
// over a row the terminal drops off the bottom.
//
// The widths are the ones this project checks (80, 100, 120) and the heights
// are the pane that clips (24) and one that does not (30), because a frame
// tuned for the short pane and a frame tuned for the tall one are different
// defects.

var receiveWidths = []int{80, 100, 120}

// receiveDrive opens the form against `fake` at a real size, LETS THE WORKSHEET
// LAND, and hands back the Root and the screen — everything a drive needs to
// read the pane AND the wire.
//
// The fetch is pumped rather than skipped. Everything this screen draws comes
// off the worksheet, so a drive that wrote the fields by hand would be
// asserting about a state the endpoint cannot produce; and the loading frame is
// itself one of the states the rules apply to, so it is reached the way an
// operator reaches it.
func receiveDrive(t *testing.T, fake *receiveFake, lines []omsapi.ReceivingLine, w, h int) (Root, *ReceiveFormScreen) {
	t.Helper()
	if fake.sheet == nil {
		fake.sheet = receiveWorksheet(lines...)
	}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewReceiveFormScreen(deps, receivePO(lines...))
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	r = next.(Root)
	// receiveSettle rather than the shared pump: every drive here now fetches a
	// worksheet before it can press anything, and pump abandons the cursor
	// blink by WAITING IT OUT — 200ms of dead wall-clock per settle. Paid on
	// every one of the several thousand drives these sweeps build, that is the
	// difference between a package that finishes and one that hits the test
	// timeout (AGENTS.md's note on receive_form_sweep_test.go). Nothing else
	// changes: every message a drive really waits on is an in-process httptest
	// round trip, and only the blink is recognised and dropped.
	return receiveSettle(t, r, s.Init(), 0), s
}

// receivePaneLines is what the terminal really shows in the content pane: the
// screen's frame, clipped on BOTH axes by the same clampToBox Root applies to
// it. The screen is always driven through Root — real WindowSizeMsg, real
// commands, real replies — and only the CLIP is applied here, because Root's
// own View() is the whole terminal (nav column and border included) and
// measuring that against the pane's width would compare two different things.
//
// This is the project's standing idiom (po_create_picker_status_test.go's
// poPaneLinesAt): assert against the clipped frame, never against
// strings.Contains(Root.View(), …) — the status bar satisfies that substring
// while the body line is cut in half.
func receivePaneLines(s *ReceiveFormScreen, w, h int) []string {
	return strings.Split(clampToBox(s.View(), screenBodyWidth(w), screenBodyHeight(h)), "\n")
}

// receiveOrder is the ordinary order these drives run against: one plain line,
// one already part-received, and one serialized.
func receiveOrder() []omsapi.ReceivingLine {
	return []omsapi.ReceivingLine{
		receiveWSLine(11, "Box of M3 bolts", 4, 0),
		receiveWSLine(12, "Reel of 24AWG wire", 10, 3),
		receiveWSSerialized(13, "Serialized controller board", 2, 0),
	}
}

// receiveManyLines is an order long enough that the form cannot fit on a pane —
// the state the conversion added windowing for. Each line's name carries its
// own number so a test can say WHICH line it is looking at.
func receiveManyLines(n int) []omsapi.ReceivingLine {
	out := make([]omsapi.ReceivingLine, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, receiveWSLine(10+i, fmt.Sprintf("Line-%d hex bolt, zinc plated", i+1), 4+i, 0))
	}
	return out
}

// receiveStruckOff is an order of n VOIDED lines with the scan code on the LAST
// of them — so a scan lands on settled entry n, where the refusal's position
// field is at its widest and its reason ("struck off the order") at its
// longest.
//
// It keeps ONE receivable line, and that is not decoration: with nothing
// receivable the quantity form has no boxes, so its bar loses Enter and UP/DN
// and the way-out tail the refusal carries gets shorter — the probe would
// measure an easier sentence than the one an operator meets.
func receiveStruckOff(n int, code string) []omsapi.ReceivingLine {
	out := []omsapi.ReceivingLine{receiveWSLine(59, "Box of M3 bolts", 4, 0)}
	for i := 0; i < n; i++ {
		l := receiveWSLine(60+i, fmt.Sprintf("Cancelled bracket, crate %d", i+1), 3, 0)
		l.IsVoided, l.IsSettled = true, true
		l.QuantityPending = 0
		l.ScanCodes = nil
		if i == n-1 {
			l.ScanCodes = []omsapi.ScanCode{{Code: code, Kind: omsapi.ScanCodeItemSKU}}
		}
		out = append(out, l)
	}
	return out
}

// receiveSharedCode is an order of n lines that ALL carry one scan code — the
// same part ordered n times, which is the shape a scan resolving to several
// lines is really reached through. n is a parameter because the note that comes
// back lists the other positions up to receiveScanListMax and counts them
// after, and the two wordings are different lengths.
func receiveSharedCode(n int) []omsapi.ReceivingLine {
	out := make([]omsapi.ReceivingLine, 0, n)
	for i := 0; i < n; i++ {
		l := receiveWSLine(90+i, fmt.Sprintf("Box of M3 bolts, crate %d", i+1), 4, 0)
		l.ScanCodes = []omsapi.ScanCode{{Code: "SKU-90", Kind: omsapi.ScanCodeItemSKU}}
		out = append(out, l)
	}
	return out
}

// receiveTypeInto types a value into the box the cursor is on, a keystroke at a
// time, the way an operator or a scanner delivers it.
func receiveTypeInto(t *testing.T, r Root, value string) Root {
	t.Helper()
	for _, ch := range value {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		r = next.(Root)
	}
	return r
}

// receiveGoToLine walks the cursor onto line i's quantity box.
func receiveGoToLine(t *testing.T, r Root, s *ReceiveFormScreen, i int) Root {
	t.Helper()
	for s.focused != receiveRowFirstLine+i {
		next, _ := r.Update(tea.KeyMsg{Type: tea.KeyDown})
		r = next.(Root)
	}
	return r
}

// ---------------------------------------------------------------------------
// The form scrolls, and the row the operator is standing on is on the pane
// ---------------------------------------------------------------------------

// receiveFocusedRow returns the row drawn with the reverse-video run that says
// "the cursor is here", read out of the CLIPPED frame — or "" when no such row
// is on the pane at all.
//
// The profile has to be forced for this to mean anything: lipgloss strips every
// sequence when stdout is not a TTY, which is always in a test binary, so a
// lost highlight and a present one are byte-identical (AGENTS.md).
func receiveFocusedRow(s *ReceiveFormScreen, w, h int) string {
	for _, line := range receivePaneLines(s, w, h) {
		for _, c := range jdeCells(line) {
			if c.reverse {
				return line
			}
		}
	}
	return ""
}

// receivePaneText is the clipped pane as ONE run of text, folds undone. Prose
// on these screens is wrapped by pickerWrap, so a sentence the operator reads
// whole is several lines in the frame and a substring check against the raw
// lines fails on the line break rather than on the defect. Assertions about a
// FIXED row still read the lines; this is for the prose that folds.
func receivePaneText(s *ReceiveFormScreen, w, h int) string {
	return strings.Join(strings.Fields(strings.Join(receivePaneLines(s, w, h), " ")), " ")
}

// receiveCursorMarker is a short identifier for the row the cursor is ACTUALLY
// on, taken from that line's own label rather than written down beside the
// setup. A test that names a row by index is one cursor move away from checking
// a different row than the one it talks about, and it fails in the safe
// direction — it passes.
func receiveCursorMarker(t *testing.T, s *ReceiveFormScreen) string {
	t.Helper()
	i, ok := s.lineAt(s.focused)
	if !ok {
		t.Fatalf("the cursor is on row %d, which carries no line label", s.focused)
	}
	fields := strings.Fields(s.lines[i].sheet.Label)
	if len(fields) == 0 {
		t.Fatalf("line %d has no label to identify it by", s.focused)
	}
	return fields[0]
}

// receiveRowWith is the clipped line carrying `marker`, or "" when the pane
// does not carry it at all. It is how a row whose highlight has been eaten by
// its own value is found: once a typed value fills the input area there is no
// fill left to reverse, so the reverse-video finder above would report "no
// focused row" on exactly the state a typing test is about.
func receiveRowWith(s *ReceiveFormScreen, w, h int, marker string) string {
	for _, line := range receivePaneLines(s, w, h) {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

// TestReceive_TheFocusedRowIsAlwaysOnThePane is the defect the windowing
// closed. Before the conversion the form drew every line unconditionally and
// did not window at all: on an 80x24 terminal a fourth receivable line pushed
// the notes row and the key hint off the bottom, and a fifth pushed off
// quantity boxes the operator was typing into — clampToBox cutting silently, so
// nothing on the pane said there was more.
func TestReceive_TheFocusedRowIsAlwaysOnThePane(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	for _, width := range receiveWidths {
		for _, height := range []int{24, 30} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, receiveManyLines(9), width, height)
				// Not vacuous: nine lines genuinely outrun every pane checked
				// here, so the body has to MOVE for the loop below to keep
				// passing. A fixture that fit would satisfy every assertion
				// while windowing nothing at all.
				if !s.qtyPages() {
					t.Fatalf("nine receivable lines fit the pane at %dx%d, so this proves "+
						"nothing about windowing", width, height)
				}
				// Every row in turn — the nine quantity rows and the notes row.
				for i := 0; i < s.totalInputs(); i++ {
					if got := receiveFocusedRow(s, width, height); got == "" {
						t.Fatalf("row %d of %d: no highlighted row is on the pane at all:\n%s",
							i, s.totalInputs(), strings.Join(receivePaneLines(s, width, height), "\n"))
					}
					if s.focused != i {
						t.Fatalf("row %d: the cursor is on %d", i, s.focused)
					}
					r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
				}
			})
		}
	}
}

// TestReceive_TheActionBarSurvivesTheClip. The bar is the only place several of
// these keys are named, so a bar the terminal cut is a bar that is absent. It
// is checked on the CLIPPED render at every width and at the pane that clips.
func TestReceive_TheActionBarSurvivesTheClip(t *testing.T) {
	for _, width := range receiveWidths {
		for _, height := range []int{24, 30} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				fake := &receiveFake{}
				_, s := receiveDrive(t, fake, receiveManyLines(9), width, height)
				s.qty[0].SetValue("2") // the longest Esc label
				pane := strings.Join(receivePaneLines(s, width, height), "\n")
				for _, it := range s.bar() {
					want := it.Key + "=" + it.Label
					if !strings.Contains(pane, want) {
						t.Errorf("the bar claims %q but the clipped pane does not carry it:\n%s", want, pane)
					}
				}
			})
		}
	}
}

// TestReceive_NothingOverflowsThePane walks every state at every width and
// fails on a line the terminal would truncate. 80 columns is the width that
// must HOLD.
func TestReceive_NothingOverflowsThePane(t *testing.T) {
	long := receiveWSKit(11, "Eufy printer maintenance kit (CMYK + cleaning)", 2, 0)
	closedOnly := receiveWSLine(15, "Backordered gasket", 6, 2)
	closedOnly.IsClosedShort, closedOnly.IsSettled = true, true
	closedOnly.ReceiptState, closedOnly.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
	closedOnly.QuantityPending = 0
	states := []struct {
		name  string
		lines []omsapi.ReceivingLine
		drive func(*testing.T, Root, *ReceiveFormScreen) Root
	}{
		{"quantities", []omsapi.ReceivingLine{long, receiveWSLine(12, "Box of M3 bolts", 4, 0)}, nil},
		{"nothing receivable", []omsapi.ReceivingLine{closedOnly}, nil},
		{"serial capture", receiveSweepLines(), func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			s.qty[2].SetValue("1")
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		}},
		{"review", receiveSweepLines(), func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			s.qty[2].SetValue("1")
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
		}},
		{"summary", receiveSweepLines(), func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			s.qty[2].SetValue("1")
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> capture
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})   // -> review
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		}},
		{"write-off", receiveSweepLines(), func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR})
		}},
	}
	for _, st := range states {
		for _, width := range receiveWidths {
			for _, height := range []int{24, 30} {
				t.Run(fmt.Sprintf("%s/%dx%d", st.name, width, height), func(t *testing.T) {
					fake := &receiveFake{}
					r, s := receiveDrive(t, fake, st.lines, width, height)
					if st.drive != nil {
						r = st.drive(t, r, s)
					}
					s.qty0SetIfAny("2")
					// The SCREEN's frame, on both axes — which is what Root
					// clips. Measuring Root.View() against the pane's width
					// would be comparing the whole terminal (nav column and
					// border included) with the content pane.
					lines := strings.Split(strings.TrimSuffix(s.View(), "\n"), "\n")
					budget := screenBodyWidth(width)
					for i, line := range lines {
						if w := lipgloss.Width(line); w > budget {
							t.Errorf("line %d is %d cells and is cut at %d, losing %q",
								i+1, w, budget, string([]rune(line)[budget:]))
						}
					}
					if rows := screenBodyHeight(height); len(lines) > rows {
						t.Errorf("the frame is %d rows and the pane at %dx%d keeps %d, dropping %q",
							len(lines), width, height, rows, strings.Join(lines[rows:], " / "))
					}
				})
			}
		}
	}
}

// qty0SetIfAny types into the first quantity box when there is one, so a state
// with no receivable lines can share a table row with one that has them.
func (s *ReceiveFormScreen) qty0SetIfAny(v string) {
	if len(s.qty) > 0 {
		s.qty[0].SetValue(v)
	}
}

// TestReceive_AWideTerminalDrawsTheWholeRow. 80 columns is the width that must
// hold; it is not the width to render as though we had. A line's name is
// bounded against the pane the terminal REALLY gave, so the same order draws
// more of it at 120 than at 80.
func TestReceive_AWideTerminalDrawsTheWholeRow(t *testing.T) {
	line := receiveWSLine(21,
		"Stainless steel socket-head cap screw, M8 x 40mm, A4-80 marine grade, box of 100", 2, 0)
	widthOf := func(term int) int {
		fake := &receiveFake{}
		r, scr := receiveDrive(t, fake, []omsapi.ReceivingLine{line}, term, 30)
		// The window anchors on the CURSOR's block, and a fresh form opens on
		// the scan row — so the line has to be walked onto before its name row
		// is drawn at all.
		_ = receiveGoToLine(t, r, scr, 0)
		for _, l := range receivePaneLines(scr, term, 30) {
			if strings.Contains(l, "Stainless steel") {
				return lipgloss.Width(l)
			}
		}
		t.Fatalf("the line's name is not on the pane at %d columns", term)
		return 0
	}
	narrow, wide := widthOf(80), widthOf(120)
	if narrow > screenBodyWidth(80) {
		t.Errorf("the name row is %d wide at 80 columns, but the pane is %d", narrow, screenBodyWidth(80))
	}
	if wide <= narrow {
		t.Errorf("the name row is %d wide at 120 columns and %d at 80 — a wider terminal "+
			"is being rendered as though it were the narrowest one", wide, narrow)
	}
}

// ---------------------------------------------------------------------------
// A typed value never outgrows its row
// ---------------------------------------------------------------------------

// TestReceive_EveryKeystrokeMovesTheNotesRow. bubbles' handleOverflow returns
// early when Width is 0, so an unbounded box renders its WHOLE value: past the
// column where the row fills the pane, every further keystroke used to redraw
// it byte for byte with the caret off-screen — the reported hang, reached by
// typing. The layer bounds the box now (jdeFitInputValue), so the value scrolls
// and the caret is always the last thing on the row.
//
// DISTINCT runes, because a viewport full of one repeated character looks the
// same however far it has scrolled: a test that holds one key down passes
// without scrolling at all.
func TestReceive_EveryKeystrokeMovesTheNotesRow(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	for _, width := range receiveWidths {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(2), width, 30)
			for s.focused != s.notesRow() {
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
			}
			// The LABEL alone: with the colour profile forced the leader's
			// dots carry their own escape sequence, so "Notes ....." is not a
			// contiguous run in the rendered line even though it looks like one.
			const marker = "Notes"
			before := receiveRowWith(s, width, 30, marker)
			if before == "" {
				t.Fatalf("the notes row is not on the pane to begin with:\n%s",
					strings.Join(receivePaneLines(s, width, 30), "\n"))
			}
			// DISTINCT runes: a viewport full of one repeated character looks
			// the same however far it has scrolled, so a test that holds one key
			// down passes without the value scrolling at all. 150 is well past
			// the point where the value fills the input area at every width
			// here, and inside the box's 200-character limit — a full field
			// refusing the next rune is a different question from a row that
			// cannot show it.
			for i := 0; i < 150; i++ {
				r = receiveType(t, r, poRuneKey(string(rune('a'+i%26))))
				after := receiveRowWith(s, width, 30, marker)
				if after == before {
					t.Fatalf("keystroke %d into the notes row redrew it byte for byte — the "+
						"value has outgrown the row and the caret is off the pane:\n\t%q", i+1, after)
				}
				before = after
			}
			budget := screenBodyWidth(width)
			for _, line := range strings.Split(strings.TrimSuffix(s.View(), "\n"), "\n") {
				if w := lipgloss.Width(line); w > budget {
					t.Errorf("after typing, a line is %d wide but the pane is %d: %q", w, budget, line)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// A gateway page does not take the screen with it
// ---------------------------------------------------------------------------

// nginx502 is what omsapi.parseError hands a screen when the JSON envelope
// carries no code: the ENTIRE raw response body, newlines and all.
const nginx502 = "<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n" +
	"<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>nginx/1.24.0</center>\r\n" +
	"</body>\r\n</html>\r\n"

// TestReceive_AGatewayPageLeavesTheBarOnThePane.
//
// The failure row is ONE unwrapped line of the frame. A multi-line body does
// not overflow the WIDTH — nginx's page is seven lines of at most 42 columns —
// it overflows the HEIGHT, and clampToBox drops from the BOTTOM: the frame ran
// over by however many lines the message brought and took the whole action bar
// with it, leaving every key on the screen unnamed at once. The screen drew its
// own status line before the conversion and so had none of the layer's bound.
func TestReceive_AGatewayPageLeavesTheBarOnThePane(t *testing.T) {
	for _, width := range receiveWidths {
		for _, height := range []int{24, 30} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				fake := &receiveFake{failWith: http.StatusBadGateway, failBody: nginx502}
				r, s := receiveDrive(t, fake, receiveManyLines(3), width, height)
				s.qty[0].SetValue("1")
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // the receipt fails
				if s.pending {
					t.Fatal("the receipt is still in flight after the fake answered")
				}

				pane := receivePaneLines(s, width, height)
				joined := strings.Join(pane, "\n")
				for _, it := range s.bar() {
					if want := it.Key + "=" + it.Label; !strings.Contains(joined, want) {
						t.Errorf("the gateway page took %q off the pane:\n%s", want, joined)
					}
				}
				// The sentence naming what failed survives whole; the HTML is
				// what gives.
				if !strings.Contains(joined, "Receiving PO-1001 failed") {
					t.Errorf("the failure does not say what failed:\n%s", joined)
				}
				// And the screen came back LIVE, on the frame the operator sent
				// it from: the review names Enter again, so the receipt can be
				// retried without retyping anything, and Esc goes back to the
				// quantities — which still hold what was typed. A failed
				// receipt must not hold the operator's counts hostage to a
				// gateway.
				if !receiveBarNames(s, "Enter") {
					t.Errorf("the review did not come back live after a failed receipt: %+v", s.bar())
				}
				if got := s.qty[0].Value(); got != "1" {
					t.Errorf("the typed quantity was lost on a failed receipt: %q", got)
				}
			})
		}
	}
}

// TestReceive_AGatewayPageDoesNotBleedColour is the other half: clampToBox cuts
// a styled row by dropping runes off the END, which takes the closing SGR reset
// with them and leaves the terminal coloured for everything drawn afterwards.
// The layer's fitStatus bounds the row before that can happen.
func TestReceive_AGatewayPageDoesNotBleedColour(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	fake := &receiveFake{failWith: http.StatusBadGateway, failBody: nginx502}
	r, s := receiveDrive(t, fake, receiveManyLines(3), 80, 24)
	s.qty[0].SetValue("1")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	for i, line := range receivePaneLines(s, 80, 24) {
		if strings.Count(line, "\x1b[") > 0 && !strings.Contains(line, "\x1b[0m") &&
			!strings.HasSuffix(strings.TrimRight(line, " "), "m") {
			// A line that opens a sequence must close one. The check is
			// deliberately loose about WHICH reset, because lipgloss picks it.
			t.Errorf("line %d opens a style it never closes — the cut took the reset "+
				"with it and the terminal stays coloured: %q", i, line)
		}
	}
}

// ---------------------------------------------------------------------------
// The receipt
// ---------------------------------------------------------------------------

// TestReceive_ASuccessfulReceiptCannotBePostedTwice.
//
// The receipt used to leave the operator on the quantity form with the boxes
// still holding what they had typed and nothing on the pane saying it had
// landed except a flash that expires in four seconds — so a reflexive second
// Enter booked the whole delivery again. The evidence is on the WIRE, because a
// screen-level assertion would pass over a guard that suppressed the summary
// while still posting.
func TestReceive_ASuccessfulReceiptCannotBePostedTwice(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveManyLines(2), 80, 24)
	s.qty[0].SetValue("2")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
	if s.phase != phaseReview {
		t.Fatalf("a receipt with nothing serialized did not reach the review (phase %v)", s.phase)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // review -> the receipt goes
	if s.phase != phaseDone {
		t.Fatalf("the receipt left the flow on phase %v", s.phase)
	}
	for _, k := range []string{"enter", "enter", "enter"} {
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
	}
	if got := len(fake.sent()); got != 1 {
		t.Errorf("the delivery was booked %d times; pressing enter again must not repost it", got)
	}
	if !strings.Contains(r.View(), "Receiving complete") {
		t.Errorf("the summary is not what the operator is left looking at:\n%s", r.View())
	}
}

// TestReceive_EnterReceivesFromAnyRow is the binding this conversion changed.
// Enter used to advance a field and submit only from the last one; every other
// columnar sheet in purchasing commits from any row, and a screen that reserves
// Enter for "next field" teaches a rule that is false one screen over.
//
// The SCAN row is the one exception and it is asserted separately
// (TestReceive_EnterFindsTheLineAScanNames): with a code in the box Enter means
// FIND, because a scanner fires a burst and then an Enter, and receiving on
// that Enter would book a delivery instead of choosing a line. With the box
// empty there is nothing to find and Enter means what it means everywhere else,
// which is what this walks.
func TestReceive_EnterReceivesFromAnyRow(t *testing.T) {
	for _, row := range []int{receiveRowScan, receiveRowTracking, receiveRowFirstLine} {
		t.Run(fmt.Sprintf("from row %d", row), func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(2), 80, 24)
			s.qty[0].SetValue("2")
			for s.focused != row {
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
			}
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> review
			if s.phase != phaseReview {
				t.Fatalf("enter on row %d did not reach the review (phase %v)", row, s.phase)
			}
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if got := len(fake.sent()); got != 1 {
				t.Fatalf("enter on row %d booked %d receipts, want 1", row, got)
			}
		})
	}
	// And Up/Down still move between the rows, which is what Enter stopped
	// doing.
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveManyLines(2), 80, 24)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	if s.focused != 1 {
		t.Errorf("down left the cursor on row %d", s.focused)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyUp})
	if s.focused != 0 {
		t.Errorf("up left the cursor on row %d", s.focused)
	}
	_ = r
}

// TestReceive_RefusingAReceiptSaysWhyAndKeepsTheEntry. A local validation
// refusal has no toast behind it on some paths, so the sentence has to be in
// the BODY — and it must not cost the operator what they typed.
func TestReceive_RefusingAReceiptSaysWhyAndKeepsTheEntry(t *testing.T) {
	cases := []struct {
		name, typed, want string
	}{
		{"nothing entered", "", "nothing to receive"},
		{"not a number", "two", "line 1: a quantity is a whole number"},
		{"negative", "-3", "line 1: a quantity is a whole number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(2), 80, 24)
			if tc.typed != "" {
				s.qty[0].SetValue(tc.typed)
			}
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			pane := strings.Join(receivePaneLines(s, 80, 24), "\n")
			if !strings.Contains(pane, tc.want) {
				t.Errorf("the refusal does not say why:\nwant %q in\n%s", tc.want, pane)
			}
			if got := len(fake.sent()); got != 0 {
				t.Errorf("a refused receipt still reached the wire (%d times)", got)
			}
			if s.qty[0].Value() != tc.typed {
				t.Errorf("the refusal discarded what was typed: %q", s.qty[0].Value())
			}
			// A refusal must not leave a capture queue behind it either: the
			// units are enrolled on the way through submit(), before the
			// refusal is reached.
			if len(s.serialUnits) != 0 {
				t.Errorf("a refused receipt enrolled %d capture slots", len(s.serialUnits))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Serial capture
// ---------------------------------------------------------------------------

// TestReceive_CaptureKeepsEveryUnitTheOperatorWalksOver.
//
// Serials go INSIDE the receipt now — one transaction, so a refused receipt
// writes nothing at all — which means capture is local until Enter on the
// review. What that buys, and what this holds, is that the operator can walk
// back to a unit and fix a typo: every arm that moves the cursor stores the
// boxes first, so nothing they typed is thrown away by moving.
//
// It is the standing rule ("never silently discard what the operator typed") on
// the one screen whose entire job is capturing what they typed.
func TestReceive_CaptureKeepsEveryUnitTheOperatorWalksOver(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("2") // two units of the serialized line
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != phaseSerial || len(s.serialUnits) != 2 {
		t.Fatalf("capture did not open: phase %v, %d units", s.phase, len(s.serialUnits))
	}

	// Unit 1: a serial, a lot and an expiry.
	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = receiveType(t, r, poRuneKey("LOT-42"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
	r = receiveType(t, r, poRuneKey("2027-01-31"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> unit 2
	if s.serialCursor != 1 {
		t.Fatalf("enter did not advance to unit 2: cursor %d", s.serialCursor)
	}
	r = receiveType(t, r, poRuneKey("SN-2"))

	// Walk BACK. Unit 1 has to come back holding all three values.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyPgUp})
	if s.serialCursor != 0 {
		t.Fatalf("pgup did not walk back to unit 1: cursor %d", s.serialCursor)
	}
	if got := s.serialInput.Value(); got != "SN-1" {
		t.Errorf("walking back lost the serial: %q", got)
	}
	if got := s.lotInput.Value(); got != "LOT-42" {
		t.Errorf("walking back lost the lot: %q", got)
	}
	if got := s.expiryInput.Value(); got != "2027-01-31" {
		t.Errorf("walking back lost the expiry: %q", got)
	}
	// And forward again: unit 2's serial survived the round trip too.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyPgDown})
	if got := s.serialInput.Value(); got != "SN-2" {
		t.Errorf("walking forward lost unit 2's serial: %q", got)
	}

	// All the way through to the wire. The LAST unit's Enter lands on the
	// review, which is what its bar says it does.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != phaseReview {
		t.Fatalf("capture did not finish into the review: phase %v", s.phase)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.sent()
	if len(sent) != 1 || len(sent[0].Items) != 1 {
		t.Fatalf("want one receipt naming one line, got %+v", sent)
	}
	serials := sent[0].Items[0].Serials
	if len(serials) != 2 {
		t.Fatalf("want two serials on the wire, got %+v", serials)
	}
	if serials[0].SerialNumber != "SN-1" || serials[0].Lot != "LOT-42" ||
		serials[0].ExpirationDate != "2027-01-31" {
		t.Errorf("unit 1 did not reach the wire whole: %+v", serials[0])
	}
	if serials[1].SerialNumber != "SN-2" || serials[1].Lot != "" {
		t.Errorf("unit 2 did not reach the wire as typed: %+v", serials[1])
	}
	_ = r
}

// TestReceive_ALotWithNoSerialIsRefusedRatherThanDropped. Lot and expiry hang
// off a serial on the wire and off nothing else, so recording a slot that has
// one without the other would throw the operator's typing away in silence.
func TestReceive_ALotWithNoSerialIsRefusedRatherThanDropped(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("1")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown}) // onto Lot
	r = receiveType(t, r, poRuneKey("LOT-9"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.serialCursor != 0 {
		t.Errorf("the refusal advanced anyway: cursor %d", s.serialCursor)
	}
	if got := s.lotInput.Value(); got != "LOT-9" {
		t.Errorf("the refusal discarded the lot: %q", got)
	}
	if text := receivePaneText(s, 80, 24); !strings.Contains(text, "needs a serial before a lot") {
		t.Errorf("the refusal does not say why:\n%s", text)
	}
	_ = r
}

// TestReceive_NoWayOutOfASlotRecordsWhatEnterWouldRefuse.
//
// The two checks on a capture slot lived in the ENTER arm alone, and Enter is
// not the only way out of a slot: Esc goes forward to the review and PgUp/PgDn
// walk between units, and both of those recorded whatever was in the boxes. So
// the operator who typed a lot number and left with Esc had it recorded against
// a blank serial, where buildReceipt drops it and nothing on the review says a
// word; and the operator who typed "12/31/2026" into Expires and left with
// PgDn sent it to the wire, where a 400 rolls back the WHOLE receipt over one
// character in an optional field. Both are the exact outcomes the two checks
// exist to stop, reached through the arms added after them.
//
// So the checks moved to storeUnit — the one place a slot is recorded — and
// this walks every key that leaves a slot through both refusals. It is written
// as a matrix rather than as the reported case for the reason the defect
// happened: fixing the arm that was reported leaves the next arm free.
func TestReceive_NoWayOutOfASlotRecordsWhatEnterWouldRefuse(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	down := tea.KeyMsg{Type: tea.KeyDown}

	type slot struct {
		name string
		fill func(t *testing.T, r Root) Root
		says string
	}
	slots := []slot{
		{"a lot with no serial", func(t *testing.T, r Root) Root {
			r = receiveKey(t, r, down) // onto Lot
			return receiveType(t, r, poRuneKey("LOT-9"))
		}, "needs a serial before a lot"},
		{"an expiry that is not a date", func(t *testing.T, r Root) Root {
			r = receiveType(t, r, poRuneKey("SN-1"))
			r = receiveKey(t, r, down) // onto Lot
			r = receiveKey(t, r, down) // onto Expires
			return receiveType(t, r, poRuneKey("12/31/2026"))
		}, "needs an expiry written YYYY-MM-DD"},
	}
	exits := []struct {
		name string
		key  tea.KeyMsg
	}{
		{"enter", enter},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"pgdown", tea.KeyMsg{Type: tea.KeyPgDown}},
	}

	for _, exit := range exits {
		for _, sl := range slots {
			t.Run(exit.name+" on "+sl.name, func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
				s.qty[2].SetValue("2") // two units, so PgDn has somewhere to go
				r = receiveKey(t, r, enter)
				if s.phase != phaseSerial || len(s.serialUnits) != 2 {
					t.Fatalf("capture did not open: phase %v, %d units", s.phase, len(s.serialUnits))
				}
				r = sl.fill(t, r)
				serial, lot, expiry := s.serialInput.Value(), s.lotInput.Value(), s.expiryInput.Value()

				r = receiveKey(t, r, exit.key)

				if s.phase != phaseSerial {
					t.Errorf("%s left capture on phase %v, carrying a slot the receipt "+
						"would be refused for", exit.name, s.phase)
				}
				if s.serialCursor != 0 {
					t.Errorf("%s walked off the slot anyway: cursor %d", exit.name, s.serialCursor)
				}
				// What was typed stays in the boxes, so it can be fixed in place.
				if got := s.serialInput.Value(); got != serial {
					t.Errorf("%s discarded the serial: %q, want %q", exit.name, got, serial)
				}
				if got := s.lotInput.Value(); got != lot {
					t.Errorf("%s discarded the lot: %q, want %q", exit.name, got, lot)
				}
				if got := s.expiryInput.Value(); got != expiry {
					t.Errorf("%s discarded the expiry: %q, want %q", exit.name, got, expiry)
				}
				if got := s.captures[0]; got != (receiveCapture{}) {
					t.Errorf("%s recorded the refused slot anyway: %+v", exit.name, got)
				}
				// The refusal NAMES the key that was pressed: three keys share
				// one wording, so without the lead two of them would answer
				// with the sentence the third had already drawn.
				if text := receivePaneText(s, 80, 24); !strings.Contains(text, exit.name+" "+sl.says) {
					t.Errorf("the refusal does not name %q and say why:\n%s", exit.name, text)
				}
				if got := fake.sent(); len(got) != 0 {
					t.Errorf("a refused slot still reached the wire: %+v", got)
				}
				_ = r
			})
		}
	}
}

// TestReceive_ACorrectedSlotStillReachesTheWire is the other half: the refusal
// is a refusal to RECORD, not a dead end. Fixing the box and pressing the same
// key must go through and carry the whole slot.
func TestReceive_ACorrectedSlotStillReachesTheWire(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("1")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown}) // Lot
	r = receiveType(t, r, poRuneKey("LOT-9"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown}) // Expires
	r = receiveType(t, r, poRuneKey("12/31/2026"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != phaseSerial {
		t.Fatalf("the malformed expiry was not refused: phase %v", s.phase)
	}
	for range "12/31/2026" {
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyBackspace})
	}
	r = receiveType(t, r, poRuneKey("2026-12-31"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != phaseReview {
		t.Fatalf("the corrected slot was still refused: phase %v", s.phase)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	sent := fake.sent()
	if len(sent) != 1 || len(sent[0].Items) != 1 {
		t.Fatalf("want one receipt naming one line, got %+v", sent)
	}
	got := sent[0].Items[0].Serials
	if len(got) != 1 || got[0].SerialNumber != "SN-1" || got[0].Lot != "LOT-9" ||
		got[0].ExpirationDate != "2026-12-31" {
		t.Errorf("the corrected slot did not reach the wire whole: %+v", got)
	}
	_ = r
}

// TestReceive_TheConfirmAnswersTheKeysTheOperatorArrivesUsing.
//
// keyWriteOff bound esc, ctrl+x and enter and handed everything else to the
// reason box, and bubbles' textinput binds neither Up nor Down — so on the ONE
// frame of this screen where the next key writes a balance off, the pair
// redrew a byte-for-byte identical pane. It is the pair every route into the
// confirm has just been using: keyQty, keySerial, keyBlocked and keyReview all
// bind it and all four bars name UP/DN.
//
// They decline BY NAME here, and writeOffBar names neither, so the bar and the
// keys still agree about what acts.
func TestReceive_TheConfirmAnswersTheKeysTheOperatorArrivesUsing(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveOrder(), 80, 24)
	r = receiveGoToLine(t, r, s, 0)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlK})
	if s.phase != phaseWriteOff {
		t.Fatalf("ctrl+k did not open the confirm: phase %v", s.phase)
	}
	named := receiveNamedKeys(t, s.bar())
	for _, k := range []string{"up", "down", "pgup", "pgdown"} {
		if named[k] {
			t.Fatalf("the confirm's bar names %q, so this test is checking the wrong "+
				"direction: %+v", k, s.bar())
		}
	}
	// In SEQUENCE, with no reset between presses: a decline that did not name
	// its key would let the second press redraw the first one's pane.
	before := receivePaneText(s, 80, 24)
	for _, k := range []string{"up", "down", "pgup", "pgdown"} {
		r = receiveKey(t, r, poPhaseKeyMsg(k))
		text := receivePaneText(s, 80, 24)
		if !strings.Contains(text, k+" does nothing here") {
			t.Errorf("%q on the confirm does not say so:\n%s", k, text)
		}
		if text == before {
			t.Errorf("%q on the confirm redrew a byte-for-byte identical pane", k)
		}
		before = text
	}
	if s.phase != phaseWriteOff {
		t.Errorf("a declining key left the confirm: phase %v", s.phase)
	}
	if got := fake.closedShort(); len(got) != 0 {
		t.Errorf("a declining key wrote a balance off: %+v", got)
	}
	_ = r
}

// TestReceive_ReEnteringCaptureCountsWhatIsLeft.
//
// The note that opens capture used to name len(serialUnits) — the QUEUE LENGTH
// — which is right only until the operator walks back. enrol carries captured
// serials across by identity, so a re-entry gets a queue the same length with
// less work in it: capture one of three, Esc to the review, Esc to the
// quantities to re-check a count, Enter, and the pinned note read "3 serialized
// units to capture" over a body two rows down reading "capture 2 of 3 · 1
// serial(s) so far". Two rows of one pane, two counts of one thing, on the
// phase whose whole job is tracking exactly that.
//
// Round 5 reworded only the all-answered branch, which is why this drives the
// PARTIAL one — one captured, two left — and why it checks the fresh entry in
// the same walk: both branches read off one count now, so both are asserted
// against a figure DERIVED from the screen's own captures rather than a literal
// that could be made to agree with a wrong lead.
func TestReceive_ReEnteringCaptureCountsWhatIsLeft(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	esc := tea.KeyMsg{Type: tea.KeyEsc}

	// A serialized line ordered THREE, so the queue is long enough for
	// "captured" and "left" to be different numbers.
	lines := []omsapi.ReceivingLine{receiveWSSerialized(21, "Serialized controller board", 3, 0)}
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, lines, 80, 30)
	s.qty[0].SetValue("3")
	r = receiveKey(t, r, enter)
	if s.phase != phaseSerial || len(s.serialUnits) != 3 {
		t.Fatalf("capture did not open on three units: phase %v, %d units",
			s.phase, len(s.serialUnits))
	}

	// A FRESH enrolment: nothing captured, so the whole queue is outstanding.
	receiveAssertCaptureLead(t, s)

	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, enter) // records unit 1, moves to unit 2
	r = receiveKey(t, r, esc)   // -> review
	if s.phase != phaseReview {
		t.Fatalf("esc did not reach the review: phase %v", s.phase)
	}
	r = receiveKey(t, r, esc) // -> quantities, re-checking a count
	if s.phase != phaseQty {
		t.Fatalf("esc did not hand back the quantity form: phase %v", s.phase)
	}

	r = receiveKey(t, r, enter) // re-enter capture with one already answered
	if s.phase != phaseSerial {
		t.Fatalf("the re-entry did not reach capture: phase %v", s.phase)
	}
	if left, total := receiveCapturesLeft(s), len(s.serialUnits); left == total {
		t.Fatalf("the re-entry carried nothing across (%d of %d left), so this test is "+
			"not on the partial path it names", left, total)
	}
	receiveAssertCaptureLead(t, s)
	_ = r
}

// receiveCapturesLeft counts the capture slots still holding nothing, WITHOUT
// asking the screen's own helper: the point of the assertion is that the note
// agrees with what the operator can see, so the oracle is the same thing the
// body counts — a slot with no serial in it.
func receiveCapturesLeft(s *ReceiveFormScreen) int {
	n := 0
	for _, c := range s.captures {
		if strings.TrimSpace(c.serial) == "" {
			n++
		}
	}
	return n
}

// receiveAssertCaptureLead holds the note against the pane it is pinned over.
//
// Asserted on the CLIPPED render, and against a count derived from the screen
// rather than written down: a literal would have to be edited in step with the
// drive, and an edit that agreed with a wrong lead is how a count like this
// stays wrong.
func receiveAssertCaptureLead(t *testing.T, s *ReceiveFormScreen) {
	t.Helper()
	left, total := receiveCapturesLeft(s), len(s.serialUnits)
	pane := receivePaneText(s, 80, 30)
	if want := fmt.Sprintf("%d of %d serialized", left, total); !strings.Contains(pane, want) {
		t.Errorf("the note does not say %q — it must count what is left, not the "+
			"queue:\n%s", want, pane)
	}
	// And the body it is pinned over agrees: what is left plus what has been
	// captured is the whole queue, so the two rows cannot give different counts
	// of the same thing.
	if want := fmt.Sprintf("%d serial(s) so far", total-left); !strings.Contains(pane, want) {
		t.Errorf("the body does not say %q, so the note and the body disagree about "+
			"the same queue:\n%s", want, pane)
	}
}

// TestReceive_ReEnteringCaptureSaysWhichFrameItOpened.
//
// beginReceipt opens capture at firstUncaptured(), which answers len(captures)
// when every slot already holds something — and toSerial on that index draws
// serialBody's past-the-end branch, "Every unit has been answered." The note
// above it announced "N serialized units to capture" regardless, so the screen
// contradicted itself about the one thing the phase is for.
//
// The route is ordinary, not a corner: capture a serial, land on the review,
// press Esc back to the quantities to re-check a count, press Enter again.
// enrol carries the captures across by identity, so there is nothing left to
// type.
//
// The second half is the caret. toSerial ends in focusCurrent, and currentInput
// used to hand back s.serialInput on a frame that draws no field at all — an
// armed caret in a box nobody renders, which is exactly what focusCurrent's own
// comment says must not happen. It was harmless only because keySerial's
// past-the-end block returns before anything routes a keystroke there, and
// "harmless because of what another arm happens to do" is not a property.
func TestReceive_ReEnteringCaptureSaysWhichFrameItOpened(t *testing.T) {
	enter := tea.KeyMsg{Type: tea.KeyEnter}
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 30)
	s.qty[2].SetValue("1") // one unit of the serialized line
	r = receiveKey(t, r, enter)
	if s.phase != phaseSerial || len(s.serialUnits) != 1 {
		t.Fatalf("capture did not open: phase %v, %d units", s.phase, len(s.serialUnits))
	}
	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, enter) // the last unit lands on the review
	if s.phase != phaseReview {
		t.Fatalf("the last unit did not reach the review: phase %v", s.phase)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc}) // back to the quantities
	if s.phase != phaseQty {
		t.Fatalf("esc did not hand back the quantity form: phase %v", s.phase)
	}

	r = receiveKey(t, r, enter) // re-enter capture with nothing left to type
	if s.phase != phaseSerial {
		t.Fatalf("the re-entry did not reach capture: phase %v", s.phase)
	}
	pane := receivePaneText(s, 80, 30)
	if !strings.Contains(pane, "Every unit has been answered") {
		t.Fatalf("the re-entry did not open the answered frame, so this test is not "+
			"looking at the state it names:\n%s", pane)
	}
	if strings.Contains(pane, "to capture") {
		t.Errorf("the note announces units to capture over a frame saying every unit "+
			"is answered:\n%s", pane)
	}
	if !strings.Contains(pane, "already answered") {
		t.Errorf("the note does not say which frame it opened:\n%s", pane)
	}
	// The way out it names is the bar's, so it cannot advertise a key this
	// frame refuses.
	for _, item := range s.bar() {
		if !strings.Contains(pane, strings.ToLower(item.Key)) {
			t.Errorf("the note does not name %q, which the bar does:\n%s", item.Key, pane)
		}
	}
	if armed := receiveFocusedBoxes(t, s); len(armed) != 0 {
		t.Errorf("the answered frame draws no field and armed %v", armed)
	}
	_ = r
}

// TestReceive_TheSerialBarFollowsTheBox. With nothing typed, Enter PASSES OVER
// the unit; saying "Save" there would name a key that does something else. The
// bar is the only place that fact is stated, so it has to follow the box.
func TestReceive_TheSerialBarFollowsTheBox(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("2")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != phaseSerial {
		t.Fatalf("capture did not open: phase %v", s.phase)
	}
	if !barHas(s.bar(), "Enter", "Skip unit") {
		t.Errorf("an empty box does not offer the skip: %+v", s.bar())
	}
	r = receiveType(t, r, poRuneKey("SN-9"))
	if !barHas(s.bar(), "Enter", "Save & next") {
		t.Errorf("a filled box does not offer the save: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // saves SN-9, onto unit 2
	// The LAST unit says where enter goes, because "next" is a claim there is
	// one and there is not.
	if !barHas(s.bar(), "Enter", "Skip & review") {
		t.Errorf("the last unit does not say where enter goes: %+v", s.bar())
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // blank: passes unit 2 over

	if s.phase != phaseReview {
		t.Fatalf("the last unit did not reach the review: phase %v", s.phase)
	}
	if text := receivePaneText(s, 80, 24); !strings.Contains(text, "1 of 2 units captured") {
		t.Errorf("the review does not report the gap capture left:\n%s", text)
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // send it

	sent := fake.sent()
	if len(sent) != 1 {
		t.Fatalf("want one receipt, got %+v", sent)
	}
	if got := sent[0].Items[0].Serials; len(got) != 1 || got[0].SerialNumber != "SN-9" {
		t.Errorf("a passed-over unit did not stay off the wire: %+v", got)
	}
}

// TestReceive_TheSummaryReportsOutstandingSerials.
//
// Receiving goods without every serial is allowed on purpose — the contract
// says so, because goods that physically arrived must be recordable — and the
// gap is what replaced the old ban on serialized kit components. It is
// invisible unless a client draws it, so the summary draws the figure the
// SERVER came back with rather than one counted on this side.
func TestReceive_TheSummaryReportsOutstandingSerials(t *testing.T) {
	fake := &receiveFake{replyWith: map[string]any{
		"id": 5, "po_number": "PO-1001", "status": "partially_received",
		"status_label": "Partially Received", "total_received_quantity": 1,
		"total_quantity": 9, "outstanding_line_count": 2,
		"has_receipt_variance": true, "variance_line_count": 1,
		"serials_outstanding": 2,
	}}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("2")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // -> capture
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})   // -> review, nothing captured
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // send it

	if s.phase != phaseDone {
		t.Fatalf("the receipt did not finish: phase %v", s.phase)
	}
	text := receivePaneText(s, 80, 24)
	for _, want := range []string{
		// A value row cannot fold, so these are as long as the 51-column pane
		// lets them be — the sentence that explains the figure is the caveat
		// under the block, which does fold.
		"2 units with no serial",
		"1 line short or over",
		"2 outstanding",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the summary does not report %q:\n%s", want, text)
		}
	}
	_ = r
}

// TestReceive_AKitOrderStillSaysWhatItCredits guards the kit contract through
// the conversion: the tag, the unit of the quantity box, and the breakdown all
// have to survive the move onto the columnar layer. po_kit_lines_test.go holds
// the arithmetic; this holds that the columnar rows did not lose it.
func TestReceive_AKitOrderStillSaysWhatItCredits(t *testing.T) {
	for _, width := range receiveWidths {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake,
				[]omsapi.ReceivingLine{receiveWSKit(11,
					"Eufy printer maintenance kit (CMYK + cleaning)", 2, 0)}, width, 30)
			s.qty[0].SetValue("1")
			// The window anchors on the cursor's block, so the kit's own block
			// has to be the one the cursor is standing on for any of it to be
			// drawn at all.
			_ = receiveGoToLine(t, r, s, 0)
			pane := strings.Join(receivePaneLines(s, width, 30), "\n")
			// Read as the operator reads it, across the fold: the kit caveat
			// travels with the line now and wraps inside the pane, so at 51
			// cells "COMPONENT items" straddles two rows. A raw substring over
			// the joined lines would call that a loss when nothing was lost —
			// and would go on doing so for any phrase a re-wording moved.
			read := receivePaneText(s, width, 30)
			for _, want := range []string{
				poKitTag,          // the line is marked
				"ordered 2 kits",  // the quantity column's unit, on the row
				"kits",            // and again beside the box the number goes in
				"COMPONENT items", // the caveat, on the kit line's own row
				"receiving 1 kit", // what the typed quantity would credit
				"3 × Black ink",   // per-kit, not the pre-multiplied figure
			} {
				if !strings.Contains(read, want) {
					t.Errorf("the kit contract lost %q at %d columns:\n%s", want, width, pane)
				}
			}
			if strings.Contains(read, "6 × Black ink") {
				t.Errorf("the ordered-quantity figure leaked into a partial receipt:\n%s", pane)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// A frame stops asserting what the state has stopped being true of
// ---------------------------------------------------------------------------

// receiveInFlight fires one key with a BARE Update, leaving whatever it started
// genuinely out, and hands back the command so the caller can land the reply
// later. Asserting a mid-flight state after driving to completion is how a note
// that had already expired passed its own test (AGENTS.md).
func receiveInFlight(t *testing.T, r Root, msg tea.KeyMsg) (Root, tea.Cmd) {
	t.Helper()
	next, cmd := r.Update(msg)
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after, cmd
}

// TestReceive_TheReplyRetiresTheNoteTheFreezeWrote.
//
// The note is the screen's answer to the last keypress and headerLines PINS it
// on every phase, so a decline that outlives the state it was written in is a
// frame naming keys that state no longer has. Press anything while the receipt
// is out and the note reads "… is frozen until the receipt answers · esc back
// to order"; when the reply lands the freeze is over and — on a serialized
// order — esc no longer goes back to the order at all, so the pinned sentence
// contradicts the bar drawn four rows under it. On the failure branch the same
// stale warn line was drawn immediately above the fresh 502 detail with the bar
// fully unfrozen again.
//
// Driven in SEQUENCE with no state reset between the presses, because resetting
// between them is exactly what makes this class of defect invisible.
func TestReceive_TheReplyRetiresTheNoteTheFreezeWrote(t *testing.T) {
	// The sentence is read off the screen WHILE the request is out rather than
	// written down here, because the frozen states wait on different requests
	// and a literal would pin this test to one of them.
	frozenSays := func(s *ReceiveFormScreen) string {
		return "frozen until " + s.inFlightSubject() + " answers"
	}

	t.Run("the receipt lands", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
		s.qty[1].SetValue("1")
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
		r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if !s.pending {
			t.Fatal("the receipt did not go out")
		}
		r, _ = receiveInFlight(t, r, poPhaseKeyMsg("j"))
		frozen := frozenSays(s)
		if got := receivePaneText(s, 80, 24); !strings.Contains(got, frozen) {
			t.Fatalf("a key pressed under the freeze did not say so:\n%s", got)
		}

		r = receiveSettle(t, r, cmd, 0)
		if s.phase != phaseDone {
			t.Fatalf("the receipt left the flow on phase %v, want the summary", s.phase)
		}
		if got := receivePaneText(s, 80, 24); strings.Contains(got, frozen) {
			t.Errorf("the freeze is over and the phase has moved, but the pane still "+
				"claims the receipt is out — and still says esc goes back to the order, "+
				"which the bar under it contradicts:\n%s", got)
		}
	})

	t.Run("the receipt fails", func(t *testing.T) {
		fake := &receiveFake{failWith: http.StatusBadGateway, failBody: nginx502}
		r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
		s.qty[1].SetValue("1")
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
		r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		r, _ = receiveInFlight(t, r, poPhaseKeyMsg("j"))
		frozen := frozenSays(s)
		if got := receivePaneText(s, 80, 24); !strings.Contains(got, frozen) {
			t.Fatalf("a key pressed under the freeze did not say so:\n%s", got)
		}

		r = receiveSettle(t, r, cmd, 0)
		pane := receivePaneText(s, 80, 24)
		if strings.Contains(pane, frozen) {
			t.Errorf("the stale freeze note is drawn above the failure that ended it:\n%s", pane)
		}
		// The failure itself is a different fact and is what the operator needs
		// kept: it came off the wire rather than answering a keypress.
		if !strings.Contains(pane, "Receiving PO-1001 failed") {
			t.Errorf("clearing the note took the failure with it:\n%s", pane)
		}
		if !strings.Contains(pane, "502 Bad Gateway") {
			t.Errorf("clearing the note took the failure's reason with it:\n%s", pane)
		}
	})

	t.Run("a write-off", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyCtrlR}) // the confirm
		if s.phase != phaseWriteOff {
			t.Fatalf("ctrl+r did not open the confirm: phase %v", s.phase)
		}
		r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyCtrlX})
		if !s.pending {
			t.Fatal("the write-off did not go out")
		}
		r, _ = receiveInFlight(t, r, poPhaseKeyMsg("j"))
		pane := receivePaneText(s, 80, 24)
		if !strings.Contains(pane, frozenSays(s)) {
			t.Fatalf("a key pressed under the write-off freeze did not say so:\n%s", pane)
		}
		// And it names the WRITE-OFF, not the receipt. Naming the wrong request
		// puts the decline directly under a status row saying something else is
		// out, and tells the operator to wait on the one that is not.
		if strings.Contains(pane, "frozen until the receipt answers") {
			t.Errorf("the write-off freeze says the receipt is out:\n%s", pane)
		}
		if !strings.Contains(pane, "Closing PO-1001 out") {
			t.Fatalf("the status row does not say the write-off is out, so the two lines "+
				"cannot be compared:\n%s", pane)
		}

		r = receiveSettle(t, r, cmd, 0)
		if got := receivePaneText(s, 80, 24); strings.Contains(got, "frozen until") {
			t.Errorf("the write-off answered, but the pane still claims a request is "+
				"out:\n%s", got)
		}
		if len(fake.marked()) != 1 {
			t.Errorf("the order was marked received %d times", len(fake.marked()))
		}
	})
}

// TestReceive_TheFrozenFormOffersNoRowToTypeInto.
//
// jdeFieldArea draws a focused text row as a solid reverse-video field — the
// layer's strongest "you are standing here and may type" signal — and while the
// receipt is out every key but Esc declines. submit() blurs every box for
// exactly that reason; drawing the row focused anyway put the invitation back on
// the pane, which is the bar-honesty rule broken in its most visual form.
//
// The receipt leaves from the REVIEW, so what this walks is the round trip: a
// row highlighted on the quantity form, the highlight gone while the request is
// out, and the same row highlighted again when Esc brings the operator back.
//
// The profile has to be forced or this claim is unmeasurable: lipgloss strips
// every sequence when stdout is not a TTY, so a lost highlight and a present one
// are byte-identical.
func TestReceive_TheFrozenFormOffersNoRowToTypeInto(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	for _, width := range receiveWidths {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &receiveFake{failWith: http.StatusBadGateway, failBody: nginx502}
			r, s := receiveDrive(t, fake, receiveManyLines(3), width, 24)
			// The cursor is MOVED off row 0 on purpose, so the assertion below
			// is aimed by s.focused rather than by an index that happens to be
			// the one the screen opens on.
			r = receiveGoToLine(t, r, s, 1)
			s.qty[1].SetValue("1")
			if receiveFocusedRow(s, width, 24) == "" {
				t.Fatalf("no row is highlighted before the receipt goes out:\n%s",
					strings.Join(receivePaneLines(s, width, 24), "\n"))
			}
			cursor := receiveCursorMarker(t, s)

			// The receipt leaves from the REVIEW, so the freeze is the review's
			// — and Esc from there comes back to a quantity form that must
			// still say where the operator was standing.
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
			r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if !s.pending {
				t.Fatal("the receipt did not go out")
			}
			if got := receiveFocusedRow(s, width, 24); got != "" {
				t.Errorf("a row still invites typing while the receipt is out: %q", got)
			}
			// The operator's PLACE is not what was given up: the row the CURSOR
			// is on keeps its number, its name and its readings. The marker is
			// derived from s.focused, because an earlier version of this named
			// row 1 while the cursor sat on row 0 — so it would have passed
			// with the cursor's own row dropped entirely, which is the exact
			// regression the sentence above claims it guards.
			if row := receiveRowWith(s, width, 24, cursor); row == "" {
				t.Errorf("the frozen form lost %q, the row the cursor is on:\n%s",
					cursor, strings.Join(receivePaneLines(s, width, 24), "\n"))
			}

			// And a failed receipt hands the keyboard back: Esc returns to the
			// quantity form, and the invitation returns with the row the
			// operator was standing on.
			r = receiveSettle(t, r, cmd, 0)
			if s.pending {
				t.Fatal("the receipt is still in flight after the fake answered")
			}
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			if s.phase != phaseQty {
				t.Fatalf("esc after a failed receipt landed on phase %v", s.phase)
			}
			if receiveFocusedRow(s, width, 24) == "" {
				t.Errorf("the form came back live but no row says where the caret is:\n%s",
					strings.Join(receivePaneLines(s, width, 24), "\n"))
			}
		})
	}
}

// TestReceive_LeavingCaptureEarlyKeepsWhatWasCaptured.
//
// Esc finishes capture and goes FORWARD, to the review — not back to the
// quantities and not out of the flow — so there is no arrangement of keys that
// leaves the operator unable to reach the post. What they typed on the unit
// they were standing on goes with them, and the units they never reached are
// reported as the outstanding serials they will become.
func TestReceive_LeavingCaptureEarlyKeepsWhatWasCaptured(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("3") // three units, so esc leaves two unanswered
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != phaseSerial {
		t.Fatalf("capture did not open: phase %v", s.phase)
	}

	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != phaseReview {
		t.Fatalf("esc left capture on phase %v, want the review", s.phase)
	}
	// The box the operator was standing IN when they pressed esc is stored:
	// leaving is not the same as discarding, and this is the press the rule is
	// easiest to break on.
	if got := s.captures[0].serial; got != "SN-1" {
		t.Errorf("esc discarded the serial in the box: %q", got)
	}
	for _, box := range s.allBoxes() {
		if box.Focused() {
			t.Error("the review is on screen with a caret armed in a box it does not draw")
		}
	}
	if got := receivePaneText(s, 80, 24); !strings.Contains(got, "1 of 3 serials captured") {
		t.Errorf("the review does not report the units capture left:\n%s", got)
	}

	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	sent := fake.sent()
	if len(sent) != 1 {
		t.Fatalf("want one receipt, got %+v", sent)
	}
	if got := sent[0].Items[0].Serials; len(got) != 1 || got[0].SerialNumber != "SN-1" {
		t.Errorf("the one captured serial did not reach the wire: %+v", got)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// The pinned note does not outlive the state it describes
// ---------------------------------------------------------------------------

// receiveNoteLine is the pinned answer-to-the-last-keypress as the operator
// reads it, folds undone. "" when the pane carries no note at all.
func receiveNoteLine(s *ReceiveFormScreen, w, h int, marker string) string {
	text := receivePaneText(s, w, h)
	i := strings.Index(text, marker)
	if i < 0 {
		return ""
	}
	return text[i:]
}

// TestReceive_ANoteDoesNotOutliveTheStateItDescribes.
//
// headerLines PINS the note above the body on every frame, so a note that
// answers a keypress in one state and is still there in the next is a line
// contradicting the bar four rows under it. Two keystrokes reach it on the
// phase the operator spends most of their time on, which is why the clear is in
// the key dispatch rather than in the arms that happen to have been thought of.
//
// Driven in SEQUENCE with no reset between presses: resetting is what makes
// this class of defect invisible.
func TestReceive_ANoteDoesNotOutliveTheStateItDescribes(t *testing.T) {
	t.Run("typing a quantity retires the enter refusal", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveManyLines(3), 80, 24)
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if got := receiveNoteLine(s, 80, 24, "enter has nothing to receive"); got == "" {
			t.Fatalf("enter on an empty form did not say why:\n%s", receivePaneText(s, 80, 24))
		}

		r = receiveType(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
		// The bar now names Enter, because a quantity is something to attempt.
		named := false
		for _, it := range s.bar() {
			if it.Key == "Enter" {
				named = true
			}
		}
		if !named {
			t.Fatalf("the bar does not name Enter with a quantity typed: %v", s.bar())
		}
		if got := receiveNoteLine(s, 80, 24, "enter has nothing to receive"); got != "" {
			t.Errorf("the bar names Enter=Receive while the pinned line above it still "+
				"says Enter has nothing to receive: %q", got)
		}
	})

	t.Run("moving the cursor retires a claim about the cursor", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveManyLines(9), 80, 24)
		if !s.qtyPages() {
			t.Fatal("the body does not page at 80x24, so pgup cannot decline here")
		}
		r = receiveKey(t, r, poPhaseKeyMsg("pgup"))
		if got := receiveNoteLine(s, 80, 24, "pgup is already at"); got == "" {
			t.Fatalf("pgup at the top said nothing:\n%s", receivePaneText(s, 80, 24))
		}

		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
		if s.focused == 0 {
			t.Fatal("down did not move the cursor")
		}
		if got := receiveNoteLine(s, 80, 24, "pgup is already at"); got != "" {
			t.Errorf("the cursor has moved off the first row but the pane still says %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Enter is named for what submit will actually attempt
// ---------------------------------------------------------------------------

// receiveBarNames reports whether the bar the frame is drawing names `key`.
func receiveBarNames(s *ReceiveFormScreen, key string) bool {
	for _, it := range s.bar() {
		if it.Key == key {
			return true
		}
	}
	return false
}

// TestReceive_EnterIsNamedForWhatSubmitWillAttempt.
//
// A box holding "0" is skipped by submit, leaves nothing to post and comes
// straight back as a local refusal — so naming Enter there advertised a key
// whose entire effect was to write a note. A box holding "two" is the opposite
// case and must STAY named: it is a real attempt, and the refusal naming the
// line is the answer to it.
//
// The two refusals stay distinct, because an operator who cannot tell which one
// they hit does not know what to do next.
func TestReceive_EnterIsNamedForWhatSubmitWillAttempt(t *testing.T) {
	cases := []struct {
		name    string
		typed   map[int]string
		named   bool
		wantSay string
	}{
		{"every box empty", nil, false, "type a quantity against a line"},
		{"one box holding a zero", map[int]string{1: "0"}, false, "every quantity entered is zero"},
		{"zeroes in every box", map[int]string{0: "0", 1: "00", 2: " 0 "}, false, "every quantity entered is zero"},
		{"a typo", map[int]string{1: "two"}, true, "line 2: a quantity is a whole number"},
		{"a negative", map[int]string{1: "-3"}, true, "line 2: a quantity is a whole number"},
		{"a zero and a real quantity", map[int]string{0: "0", 1: "2"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(3), 80, 24)
			for i, v := range tc.typed {
				s.qty[i].SetValue(v)
			}
			if got := receiveBarNames(s, "Enter"); got != tc.named {
				t.Fatalf("the bar names Enter = %v, want %v: %v", got, tc.named, s.bar())
			}

			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if tc.named && tc.wantSay == "" {
				// Enter goes to the REVIEW, which is where the receipt leaves
				// from: the last thing an operator sees before stock moves is
				// what the server is about to be told.
				if s.phase != phaseReview {
					t.Fatalf("a named Enter left the flow on phase %v, want the review", s.phase)
				}
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				if receipts := len(fake.sent()); receipts != 1 {
					t.Fatalf("the review posted %d receipts, want 1", receipts)
				}
				return
			}
			receipts := len(fake.sent())
			if receipts != 0 {
				t.Errorf("a receipt went out for %q: %d posted", tc.name, receipts)
			}
			if got := receivePaneText(s, 80, 24); !strings.Contains(got, tc.wantSay) {
				t.Errorf("the refusal does not say %q:\n%s", tc.wantSay, got)
			}
		})
	}

	// And the two refusals are DIFFERENT sentences: an operator who cannot tell
	// an empty form from a pad of zeroes does not know what to do next.
	fake := &receiveFake{}
	r, empty := receiveDrive(t, fake, receiveManyLines(3), 80, 24)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	emptyPane := receivePaneText(empty, 80, 24)

	r2, zero := receiveDrive(t, fake, receiveManyLines(3), 80, 24)
	zero.qty[0].SetValue("0")
	r2 = receiveKey(t, r2, tea.KeyMsg{Type: tea.KeyEnter})
	zeroPane := receivePaneText(zero, 80, 24)
	if emptyPane == zeroPane {
		t.Errorf("an empty form and a pad of zeroes refuse with the same pane:\n%s", emptyPane)
	}
	_, _ = r, r2
}

// ---------------------------------------------------------------------------
// The paging claim and the paging guard describe the SAME frame
// ---------------------------------------------------------------------------

// TestReceive_ThePagingClaimHoldsWithANoteOnThePane.
//
// The note is part of the PINNED HEADER, the header costs the body rows, and
// the body's height is what decides whether PgUp/PgDn move anything — so the
// frame an operator is looking at and the frame left after the dispatch retires
// the note are two different frames with two different answers.
//
// Retiring the note at the top of handleKey (which is what stopped a decline
// outliving its own state) opened exactly that gap: the bar was drawn on the
// frame WITH the note and the guard in pageQty then asked about the frame
// WITHOUT it. In the two-row band where those disagree the bar said
// PgUp/PgDn=Page and the press declined — and since the decline rewrote the same
// sentence, a second press redrew a byte-for-byte identical pane.
//
// Sweeping the terminal HEIGHT one row at a time is the point: the band is two
// or three rows wide, so the two heights the other tests sample walk straight
// past it. po_view_jde_test.go's TestPOView_ScrollKeysNamedExactlyWhenTheBodyMoves
// is this project's precedent, and it is what caught the same defect on the
// order pad.
//
// Driven in SEQUENCE: the first press is what puts a note on the pane, and the
// second is the one being judged.
func TestReceive_ThePagingClaimHoldsWithANoteOnThePane(t *testing.T) {
	for _, width := range receiveWidths {
		for height := 10; height <= 40; height++ {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, receiveManyLines(3), width, height)

				// A key that declines without moving the cursor, so what
				// changes between the two presses is the NOTE and nothing else.
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				if s.focused != 0 {
					t.Fatalf("the refusal moved the cursor to row %d", s.focused)
				}
				// The NOTE, not the reservation: headerLines is a constant
				// height now, so asking it whether a note was written is a
				// branch that can never be false — a guard whose message claims
				// it prevents vacuity while checking nothing is the same defect
				// as the wrong-row assertion two rounds back.
				if s.note.text == "" {
					t.Fatalf("the refusal put no note on the pane, so this height "+
						"proves nothing:\n%s", receivePaneText(s, width, height))
				}

				// What the operator reads, on the frame they are about to press
				// against.
				named := receiveBarNames(s, "PgUp/PgDn")
				pane := receiveClippedPane(s, width, height)
				before := s.focused

				r = receiveKey(t, r, poPhaseKeyMsg("pgdown"))
				moved := s.focused != before
				if named != moved {
					t.Fatalf("the bar named PgUp/PgDn = %v but pressing it moved the body = %v"+
						"\n--- the frame the key was pressed against ---\n%s"+
						"\n--- the frame it produced ---\n%s",
						named, moved, pane, receiveClippedPane(s, width, height))
				}
				if !moved && receiveClippedPane(s, width, height) == pane {
					t.Errorf("pgdown declined with a byte-for-byte identical pane:\n%s", pane)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// The note costs the same rows whether or not it is there
// ---------------------------------------------------------------------------

// TestReceive_WritingANoteNeverChangesThePagingClaim.
//
// The note is pinned above the body, the header is subtracted from the body's
// row budget, and the body's height is what decides whether PgUp/PgDn are named
// — so while the note's rows appeared WITH the note, the sentence naming which
// keys act could itself add or remove the paging pair from the bar drawn under
// it. Two ends of one circle were reachable: a decline could print "pgdown does
// nothing here" immediately above a bar naming PgUp/PgDn (and the next press
// would then page, the key alternating between working and refusing), and a
// shorter decline replacing a taller one could leave "pgup/pgdn page" spelled
// in a sentence sitting under a bar that had dropped the pair.
//
// receiveNoteRows closed it by reserving those rows UNCONDITIONALLY, so this
// asserts exactly that: writing a note never moves the claim. Sweeping the
// terminal HEIGHT one row at a time is the point — the disagreement lived in a
// band a few rows wide, which sampled heights walk straight past.
//
// Driven in SEQUENCE, and the RESTING frame is the one judged first: the
// previous height sweep pressed Enter before judging anything, so the note was
// always already on the pane and the note-free frame — one half of the circle —
// was never the frame a judged key was pressed against.
func TestReceive_WritingANoteNeverChangesThePagingClaim(t *testing.T) {
	for _, width := range receiveWidths {
		for height := 10; height <= 40; height++ {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, receiveManyLines(3), width, height)

				resting := receiveBarNames(s, "PgUp/PgDn")
				restingRows := len(s.headerLines())

				// A key that declines without moving the cursor: what changes
				// between the frames is the NOTE and nothing else.
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				if s.focused != 0 {
					t.Fatalf("the refusal moved the cursor to row %d", s.focused)
				}
				if s.note.text == "" {
					t.Fatalf("the refusal wrote no note, so this height proves nothing")
				}
				if got := len(s.headerLines()); got != restingRows {
					t.Errorf("writing a note changed the pinned header from %d rows to %d — "+
						"the reservation is conditional again", restingRows, got)
				}
				if got := receiveBarNames(s, "PgUp/PgDn"); got != resting {
					t.Fatalf("writing a note changed whether the bar names PgUp/PgDn "+
						"(%v -> %v):\n%s", resting, got,
						receiveClippedPane(s, width, height))
				}

				// And the claim is still true of the body: named exactly when
				// pressing it moves.
				named := receiveBarNames(s, "PgUp/PgDn")
				pane := receiveClippedPane(s, width, height)
				before := s.focused
				r = receiveKey(t, r, poPhaseKeyMsg("pgdown"))
				if moved := s.focused != before; named != moved {
					t.Fatalf("the bar named PgUp/PgDn = %v but pressing it moved the body = %v"+
						"\n--- pressed against ---\n%s\n--- produced ---\n%s",
						named, moved, pane, receiveClippedPane(s, width, height))
				}
			})
		}
	}
}

// receiveNoteWords is a sentence reduced to the words it says, with the `·`
// joints pickerWrap folds at removed — so "whole" and "folded" compare equal
// and only a genuinely LOST word reads as a cut.
func receiveNoteWords(text string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(text, "·", " ")), " ")
}

// receiveNoteWordsCheck keeps the normaliser from being the thing under test:
// it must not equate a sentence with its own truncation.
func receiveNoteWordsCheck(t *testing.T) {
	t.Helper()
	whole := "pgup is already at the first row · esc back to order"
	if receiveNoteWords(whole) == receiveNoteWords("pgup is already at the first row") {
		t.Fatal("receiveNoteWords cannot tell a whole sentence from a cut one")
	}
}

// TestReceive_EveryNoteFitsItsReservation is the other half of the constant.
//
// receiveNoteRows is spent on EVERY frame, so it is deliberately the minimum
// that restores the fixed point — which means a sentence longer than it would
// be CUT, and the tail of these sentences is where the key that gets the
// operator out is named. So every note this screen can produce is driven at the
// NARROWEST pane it supports (51 cells, where the fold is worst) and has to
// arrive whole.
//
// If a new sentence fails this, shorten the SENTENCE. Raising the constant
// spends another row of body on every frame the screen ever draws.
func TestReceive_EveryNoteFitsItsReservation(t *testing.T) {
	receiveNoteWordsCheck(t)

	enter := tea.KeyMsg{Type: tea.KeyEnter}
	long := []string{"shift+tab", "backspace", "ctrl+t", "pgup", "pgdown", "j"}

	type probe struct {
		name  string
		lines []omsapi.ReceivingLine
		typed map[int]string
		keys  []tea.KeyMsg
		bare  bool // fire the last key without settling, so a request is out
	}
	// Every row here must reach a say() call site. The keys that DECLINE differ
	// by phase — on the quantity form most of `long` is typed into the focused
	// box or moves the cursor and writes nothing, which is why the edge probes
	// name their keys rather than looping the whole set.
	down := tea.KeyMsg{Type: tea.KeyDown}
	// The last row of a nine-line order: the four fixed rows (scan, tracking,
	// carrier, delivered), the nine lines, and the notes row — so thirteen
	// presses from the top. Derived from the row model rather than written as a
	// number, because a fixed count is one row-model change away from probing
	// an edge that is not one, and it fails by passing.
	toLastRow := make([]tea.KeyMsg, receiveRowFirstLine+9)
	for i := range toLastRow {
		toLastRow[i] = down
	}
	// The FIRST line row, derived the same way: the fixed rows and no further.
	toFirstLine := make([]tea.KeyMsg, receiveRowFirstLine)
	for i := range toFirstLine {
		toFirstLine[i] = down
	}
	var probes []probe
	for _, k := range long {
		probes = append(probes,
			probe{"summary decline " + k, receiveManyLines(3), map[int]string{0: "2"},
				[]tea.KeyMsg{enter, enter, poPhaseKeyMsg(k)}, false},
			probe{"frozen " + k, receiveManyLines(3), map[int]string{0: "2"},
				[]tea.KeyMsg{enter, enter, poPhaseKeyMsg(k)}, true},
		)
	}
	probes = append(probes,
		probe{"qty page edge pgup", receiveManyLines(9), map[int]string{0: "2"},
			[]tea.KeyMsg{poPhaseKeyMsg("pgup")}, false},
		probe{"qty page edge pgdown", receiveManyLines(9), map[int]string{0: "2"},
			append(append([]tea.KeyMsg{}, toLastRow...), poPhaseKeyMsg("pgdown")), false},
		// ctrl+k rather than a movement key: the form's fixed rows mean up and
		// down always have somewhere to go now, so a movement key writes no
		// note at all and the probe would prove nothing.
		probe{"ctrl+k off a line", receiveManyLines(9), nil,
			[]tea.KeyMsg{tea.KeyMsg{Type: tea.KeyCtrlK}}, false},
		probe{"ctrl+r with nothing outstanding", nil, nil,
			[]tea.KeyMsg{tea.KeyMsg{Type: tea.KeyCtrlR}}, false},
		// The two write-off refusals, at their widest: ctrl+k quotes the box's
		// whole eight characters, and ctrl+r counts every line of a long order.
		probe{"ctrl+k over a typed quantity", receiveManyLines(9),
			map[int]string{0: "88888888"},
			append(append([]tea.KeyMsg{}, toFirstLine...), tea.KeyMsg{Type: tea.KeyCtrlK}), false},
		probe{"ctrl+r over typed quantities", receiveManyLines(9),
			map[int]string{0: "1", 1: "2", 2: "3", 3: "4", 4: "5", 5: "6", 6: "7", 7: "8", 8: "9"},
			[]tea.KeyMsg{tea.KeyMsg{Type: tea.KeyCtrlR}}, false},
		probe{"enter with nothing typed", receiveManyLines(9), nil, []tea.KeyMsg{enter}, false},
		probe{"enter with every box zero", receiveManyLines(9), map[int]string{0: "0", 1: "0"},
			[]tea.KeyMsg{enter}, false},
		probe{"enter with nothing receivable", nil, nil, []tea.KeyMsg{enter}, false},
		probe{"enter on an unparseable quantity", receiveManyLines(9),
			map[int]string{8: "two"}, []tea.KeyMsg{enter}, false},
		// The capture refusals, through the LONGEST key that can reach them:
		// the key leads the sentence, so pgdown is the widest each one gets.
		probe{"pgdown on a lot with no serial", receiveSweepLines(), map[int]string{2: "2"},
			[]tea.KeyMsg{enter, down, poRuneKey("LOT-9"), poPhaseKeyMsg("pgdown")}, false},
		probe{"pgdown on an expiry that is not a date", receiveSweepLines(), map[int]string{2: "2"},
			[]tea.KeyMsg{enter, poRuneKey("SN-1"), down, down,
				poRuneKey("12/31/2026"), poPhaseKeyMsg("pgdown")}, false},
		// The scan note that names the OTHER lines a code resolved to. Listed
		// at the maximum the note spells out, and counted one past it, because
		// the two wordings are different lengths and the longer one is what a
		// 51-column pane has to hold.
		probe{"a code on receiveScanListMax+1 lines", receiveSharedCode(receiveScanListMax + 1), nil,
			[]tea.KeyMsg{poRuneKey("SKU-90"), enter}, false},
		probe{"a code on more lines than the note lists", receiveSharedCode(receiveScanListMax + 2), nil,
			[]tea.KeyMsg{poRuneKey("SKU-90"), enter}, false},
	)
	// EVERY sentence findLine can answer with, driven with a code long enough
	// to spend the whole note budget on its own. s.scan takes 120 characters
	// and a GS1 string really is that long, so the operator-supplied half of
	// these sentences is exactly the unbounded value the rule is about — and
	// the half a cut takes is the tail, where the key that gets them out is
	// named. The four rows below are findLine's four branches: nothing
	// matched, nothing on the order is scannable at all, the code names a
	// settled line, and the code names several live ones.
	longCode := poRuneKey(strings.Repeat("0195012345678", 7)[:91])
	unscannable := receiveWSLine(31, "Custom fabricated bracket", 1, 0)
	unscannable.ScanCodes = nil
	settled := receiveWSLine(32, "Backordered gasket", 6, 2)
	settled.IsClosedShort, settled.IsSettled = true, true
	settled.ReceiptState, settled.ReceiptStateLabel = omsapi.ReceiptStateClosedShort, "Closed short"
	settled.QuantityPending = 0
	settled.ScanCodes = []omsapi.ScanCode{{Code: longCode.String(), Kind: omsapi.ScanCodeItemSKU}}
	shared := receiveSharedCode(2)
	for i := range shared {
		shared[i].ScanCodes = []omsapi.ScanCode{{Code: longCode.String(), Kind: omsapi.ScanCodeItemSKU}}
	}
	probes = append(probes,
		probe{"a long code that matches nothing", receiveOrder(), nil,
			[]tea.KeyMsg{longCode, enter}, false},
		probe{"a long code on an order with no codes at all",
			[]omsapi.ReceivingLine{unscannable}, nil, []tea.KeyMsg{longCode, enter}, false},
		probe{"a long code on a settled line",
			[]omsapi.ReceivingLine{receiveWSLine(30, "Box of M3 bolts", 4, 0), settled}, nil,
			[]tea.KeyMsg{longCode, enter}, false},
		// The WORST case of that sentence and not the fixture case: the settled
		// reason it names is "struck off the order" (twenty cells against
		// "closed short"'s twelve) and the position it names is two digits, so
		// this is the longest the settled refusal can be. It is the row that
		// would fail first if the sentence grew a word.
		probe{"a long code on the tenth struck-off line", receiveStruckOff(10, longCode.String()),
			nil, []tea.KeyMsg{longCode, enter}, false},
		probe{"a long code on several live lines", shared, nil,
			[]tea.KeyMsg{longCode, enter}, false},
		// Re-entering capture with nothing left to type: the note names the
		// answered frame AND carries that frame's whole way-out tail, so it is
		// the longest sentence this transition can produce.
		probe{"re-entering an answered capture", receiveSweepLines(), map[int]string{2: "1"},
			[]tea.KeyMsg{enter, poRuneKey("SN-1"), enter,
				tea.KeyMsg{Type: tea.KeyEsc}, enter}, false},
		// The OTHER branch of that lead: a queue with work left in it, which is
		// the longer of the two wordings ("N of M serialized units to capture").
		probe{"re-entering a partly answered capture",
			[]omsapi.ReceivingLine{receiveWSSerialized(21, "Serialized controller board", 3, 0)},
			map[int]string{0: "3"},
			[]tea.KeyMsg{enter, poRuneKey("SN-1"), enter,
				tea.KeyMsg{Type: tea.KeyEsc}, tea.KeyMsg{Type: tea.KeyEsc}, enter}, false},
	)

	for _, p := range probes {
		t.Run(p.name, func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, p.lines, 80, 24)
			for i, v := range p.typed {
				s.qty[i].SetValue(v)
			}
			for i, k := range p.keys {
				if p.bare && i == len(p.keys)-1 {
					r, _ = receiveInFlight(t, r, k)
					continue
				}
				if p.bare && i == len(p.keys)-2 {
					r, _ = receiveInFlight(t, r, k)
					continue
				}
				r = receiveKey(t, r, k)
			}
			// A probe that writes no note is a table row that proves nothing
			// while reading as coverage — the skip that used to sit here hid
			// several. This is a FAILURE now, so the roster above has to earn
			// its rows: every entry must reach a say() call site.
			if s.note.text == "" {
				t.Fatalf("this probe wrote no note, so it tests nothing:\n%s",
					receivePaneText(s, 80, 24))
			}
			// The block's height cannot exceed its reservation — noteLines caps
			// and pads, so asking that directly asks a question the renderer
			// makes structurally impossible to answer wrongly, which is a guard
			// that cannot fire. What CAN still be wrong is the property the
			// reservation exists for: the block is the same height whether or
			// not a note is standing, so writing one cannot move the bar below
			// it. Measured by retiring this probe's own note and re-measuring.
			withNote := len(s.headerLines())
			held := s.note
			s.note.clear()
			bare := len(s.headerLines())
			s.note = held
			if withNote != bare {
				t.Fatalf("this note moved the pinned header from %d rows to %d — the "+
					"reservation is conditional on the note again: %q",
					bare, withNote, s.note.text)
			}
			// Cut or whole is what matters: the reservation caps the render, so
			// a sentence that overran would lose its tail silently. Compared as
			// WORDS, because pickerWrap folds at the `·` joints and eats the
			// separator it broke on — a raw substring check would report every
			// fold as a cut and prove nothing about either.
			want := receiveNoteWords(s.note.text)
			if got := receiveNoteWords(receivePaneText(s, 80, 24)); !strings.Contains(got, want) {
				t.Errorf("the note is cut by its own reservation — the tail names the way "+
					"out:\nwant: %s\npane: %s", want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// A resize retires the answer it invalidates
// ---------------------------------------------------------------------------

// receiveResize drives a real WindowSizeMsg through Root, the way a terminal
// drag arrives — not by writing the screen's fields.
func receiveResize(t *testing.T, r Root, w, h int) Root {
	t.Helper()
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return after
}

// TestReceive_AResizeRetiresTheNoteItInvalidates.
//
// The note is an answer about a FRAME, and a resize destroys the frame it was
// an answer about: WindowSizeMsg moves bodyRowsForBar, which is the one input
// the paging claim is made of. A 3-line order at 80x40 does not page, so the
// bar names no paging keys and pgdown declines with "pgdown does nothing here";
// dragged to 80x24 the same form pages, the bar gains PgUp/PgDn, and that
// pinned sentence used to sit immediately above a bar naming the key it says
// does nothing — with the very next press paging, so the key read as
// alternating between working and refusing.
//
// Driven in SEQUENCE through one live screen, which is the whole point: every
// other test here sizes once before the first keypress, so no sweep could reach
// a note that had outlived its frame.
func TestReceive_AResizeRetiresTheNoteItInvalidates(t *testing.T) {
	for _, width := range receiveWidths {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(1), width, 40)
			// The height the form STOPS paging at is derived rather than
			// written down. It was 40, and 40 stopped being that height the
			// moment the form grew its scan and delivery rows — so the test
			// went on running over a state it was no longer in, and reported
			// the fixture rather than the defect. Anything that changes the
			// body's height moves this number, which is exactly why nothing
			// should be holding a copy of it.
			tall := 40
			for ; tall <= 200 && s.qtyPages(); tall++ {
				r = receiveResize(t, r, width, tall)
			}
			if s.qtyPages() {
				t.Fatalf("the form pages at every height up to %dx200, so shrinking proves "+
					"nothing", width)
			}

			r = receiveKey(t, r, poPhaseKeyMsg("pgdown"))
			if s.note.text == "" {
				t.Fatal("pgdown on a form that does not page said nothing")
			}
			if !strings.Contains(s.note.text, "pgdown") {
				t.Fatalf("the decline does not name the key: %q", s.note.text)
			}

			// The drag. The form now pages, so the bar names the pair the note
			// says does nothing.
			r = receiveResize(t, r, width, 24)
			if !s.qtyPages() {
				t.Fatalf("the form does not page at %dx24, so this drag proves nothing", width)
			}
			if !receiveBarNames(s, "PgUp/PgDn") {
				t.Fatalf("the bar does not name PgUp/PgDn at %dx24: %v", width, s.bar())
			}
			if got := receivePaneText(s, width, 24); strings.Contains(got, "pgdown does nothing here") {
				t.Errorf("the pane pins \"pgdown does nothing here\" above a bar naming "+
					"PgUp/PgDn — the note answers a frame the resize destroyed:\n%s", got)
			}
			// The failure line is a different fact and is NOT retired with it;
			// nothing here wrote one, so the pane must simply have no note.
			if s.note.text != "" {
				t.Errorf("the note survived the resize: %q", s.note.text)
			}
			_ = r
		})
	}
}

// ---------------------------------------------------------------------------
// The note says when it gave something up
// ---------------------------------------------------------------------------

// TestReceive_ANoteTooLongToFitSaysSo.
//
// receiveNoteRows caps what the note may draw, and the cap used to break at the
// last row and draw nothing to say it had. What it drops is the TAIL, which on
// these sentences is where the key that gets the operator OUT is named — and
// noteLines' own comment says a clipped hint is worse than none because they
// believe they read it, so a silent cut made that sentence false of the code
// two lines under it.
//
// The margin at the narrowest pane is zero rather than comfortable, so this is
// reachable by one more bar item rather than only by a pathological sentence.
// It is driven here through the real note field so the mark is asserted on what
// the operator SEES, at the width the screen was sized for.
func TestReceive_ANoteTooLongToFitSaysSo(t *testing.T) {
	for _, width := range receiveWidths {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(3), width, 30)
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			headerBefore := len(s.headerLines())

			// A sentence no wording on this screen produces today, which is the
			// point: the guard must hold for the say() call site added later,
			// not only for the ones a probe list happens to name.
			//
			// The LEAD is deliberately distinct from the filler. A fixture built
			// only of repeated words could not tell dropping from the END from
			// dropping from the FRONT, which is the whole justification
			// fittedNote gives for its direction.
			const lead = "pgdown does nothing here"
			s.note.text = lead + " · " +
				strings.TrimSpace(strings.Repeat("overlong ", 60)) + " · esc back to order"
			s.note.level = StatusWarn

			pane := receivePaneText(s, width, 30)
			if !strings.Contains(pane, strings.TrimSpace(receiveNoteDropMark)) {
				t.Errorf("the note was cut with nothing on the pane saying so:\n%s", pane)
			}
			if strings.Contains(pane, "esc back to order") {
				t.Fatalf("the fixture did not overrun, so this proves nothing:\n%s", pane)
			}
			// The lead survives WHOLE, which is what "words go from the END"
			// means and what the cut is for: the lead names the key that was
			// pressed and IS the answer. A cut that took it — by dropping from
			// the front, or by falling through to a bare mark — leaves a note
			// naming no key at all, and two declining keys then redraw the same
			// pane. Compared as words because pickerWrap folds at the `·` joints
			// and eats the separator it broke on.
			if got := receiveNoteWords(pane); !strings.Contains(got, receiveNoteWords(lead)) {
				t.Errorf("the cut took the lead, so the note names no key:\nwant: %s\npane: %s",
					receiveNoteWords(lead), got)
			}

			// A note with NO word boundary is the same rule with nothing to
			// walk: the word loop stops at keep > 0, so a single token used to
			// fall through to a note whose entire content was the drop mark. A
			// note reading "…" names no key, which is the state two declining
			// keys sharing a pane is made of — and a minified JSON or HTML body
			// is exactly that shape, which is the input fittedNote's own bound
			// says is one arm away.
			s.note.text = "pgdown" + strings.Repeat("x", 4000)
			s.note.level = StatusWarn
			single := receivePaneText(s, width, 30)
			if !strings.Contains(single, "pgdown") {
				t.Errorf("a single-token note lost its lead — it now names no key:\n%s", single)
			}
			if !strings.Contains(single, strings.TrimSpace(receiveNoteDropMark)) {
				t.Errorf("a single-token note was cut with nothing saying so:\n%s", single)
			}
			// And the cut costs a word, never the header's height — the paging
			// claim is measured against that.
			if got := len(s.headerLines()); got != headerBefore {
				t.Errorf("an overlong note moved the pinned header from %d rows to %d",
					headerBefore, got)
			}
			_ = r
		})
	}
}

// TestReceive_AHugeNoteDoesNotFreezeTheFrame is the behavioural half of
// fittedNote's bound.
//
// The note block is rebuilt two or three times per frame, and the frame is
// rebuilt on every keystroke, so a bound whose cost is the LENGTH OF ITS INPUT
// rather than the size of its budget is seconds of dead terminal per press —
// the hang this project has already fixed once on the picker screens, where
// omsapi.parseError had put an entire multi-KB gateway page into a string a
// fold was handed. Every note this screen writes today is a screen-composed
// sentence, so the input is short; the bound is measured against a long one
// because the next arm that hands say() something off the wire is what this
// exists to survive.
//
// Wall-clock, because the property IS the cost. It is deliberately loose: the
// bounded shape does this in microseconds, and the shape it replaced grows with
// the input without limit.
func TestReceive_AHugeNoteDoesNotFreezeTheFrame(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveManyLines(3), 80, 24)
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	s.note.text = "pgdown does nothing here · " + strings.Repeat("overlong ", 4000)
	s.note.level = StatusWarn

	start := time.Now()
	for i := 0; i < 5; i++ {
		if got := receivePaneText(s, 80, 24); !strings.Contains(got, "pgdown does nothing here") {
			t.Fatalf("the huge note lost its lead:\n%s", got)
		}
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("five frames carrying a %d-cell note took %s — the note bound is measuring "+
			"its input rather than its budget", len(s.note.text), elapsed)
	}
	_ = r
}

// ---------------------------------------------------------------------------
// The reservation yields to the pane; the form is always drawn
// ---------------------------------------------------------------------------

// receiveFrameDrawn reports whether the columnar layer will draw this screen's
// frame at all at the size it has been given, and asserts the REFUSAL when it
// will not.
//
// The layer refuses when the pane cannot hold the action bar, the status row
// and one row each of the pinned header and the body (jdeScreen.tooShort). That
// is a state these sweeps never had to think about, because the layer used to
// FLOOR the body budget at three rows and assemble a frame that did not fit:
// clampToBox took the action bar off the bottom, and every sweep in this file
// asserts about the BODY, so 80x8 "drew the form" with no legend under it at
// all and passed. The frame now either fits whole or is replaced by a notice,
// so those heights have to be ASSERTED rather than swept past — otherwise
// raising a sweep's floor to make it pass is just the check being weakened to
// whatever is easy to assert.
//
// The bound is DERIVED: the same predicate the frames use, asked of the same
// bar and the same header they are about to be handed. A change to the geometry
// moves it instead of leaving a band of heights untested.
func receiveFrameDrawn(t *testing.T, s *ReceiveFormScreen, w, h int) bool {
	t.Helper()
	if !s.tooShort(actionBarRowsFor(s.barWidth(), s.bar()), len(s.headerLines())) {
		return true
	}
	if pane := receivePaneText(s, w, h); !strings.Contains(pane, "Too short") {
		t.Errorf("at %dx%d the layer cannot fit this frame and the pane does not say so — "+
			"a blank pane is one the operator cannot tell from a wedged program:\n%s",
			w, h, receiveClippedPane(s, w, h))
	}
	if jdeBarOf(s.View()) != nil {
		t.Errorf("at %dx%d the pane cannot carry the action bar and the frame draws one "+
			"anyway, so what the operator reads is whatever clampToBox leaves of "+
			"it:\n%s", w, h, receiveClippedPane(s, w, h))
	}
	return false
}

// receiveCursorRowIdentified reports whether the pane says WHICH row the cursor
// is on — its line name, the first line of the cursor's block.
//
// It is deliberately the weaker of the two questions, and its name now says so.
// It was called receiveCursorRowDrawn and documented as reporting whether "the
// quantity box the operator is about to type into" was on the pane, which it
// never checked: it matched the line LABEL, and because jdeLines.Window pins an
// overflowing block to its FIRST line the label is exactly what survives when
// the box does not. So the guard passed in precisely the state it was written
// to catch — and a separator that opened the block below it, pushing every box
// but row 0's one line further down, shipped straight past it.
func receiveCursorRowIdentified(t *testing.T, s *ReceiveFormScreen, w, h int) bool {
	t.Helper()
	return receiveRowWith(s, w, h, receiveCursorMarker(t, s)) != ""
}

// receiveCursorBox is the body line the cursor's own INPUT is drawn on, and
// how deep into that row's block it sits. The depth is for the failure MESSAGE
// only — see receiveCursorBoxDrawn for why it is not what decides anything.
//
// Both are taken from the body the frame is about to draw rather than written
// down beside the test: which line carries the box is decided by AddFittedFields
// and by the order this screen's own builder puts the block's lines in, and a
// marker copied into a test is one reorder away from aiming at a different line
// while still passing. The line is found by the LABEL COLUMN (receiveLabels, the
// screen's one roster of them) followed by its dot leader, which is what
// renderJDEField draws and what nothing else on these rows can produce.
func receiveCursorBox(t *testing.T, s *ReceiveFormScreen) (line string, depth int) {
	t.Helper()
	body, cursor := s.body()
	first, last := body.block(cursor)
	for i := first; i <= last && i < body.Len(); i++ {
		for _, label := range receiveLabels {
			if strings.Contains(body.text[i], label+" .") {
				return strings.Join(strings.Fields(body.text[i]), " "), i - first
			}
		}
	}
	t.Fatalf("the block for row %d carries no input row, so there is no box to look for:\n%s",
		cursor, strings.Join(body.text[first:last+1], "\n"))
	return "", 0
}

// receiveCursorBoxDrawn reports whether the operator can SEE the box they are
// typing into, and whether the pane could have paid for it.
//
// The second half is what stops the first from being a demand the geometry
// cannot meet — and it is measured against the PANE alone, never against the
// block being judged. It used to compare the window against the box's own depth
// in its own block, and that is an escape hatch wired to the thing it polices:
// the separator defect this file's sibling sweep exists for works by pushing
// the box one line further down its block, which raised the depth in lockstep,
// flipped the gate to unpayable and SKIPPED the assertion instead of failing
// it. A gate a regression can widen is not a gate.
//
// So the gate asks how deep the box is CONTRACTED to be, not how deep it turned
// out. A body that fits its window is drawn whole; one that overflows spends
// two of the window's rows on the "more above / more below" markers, leaving
// avail-2, and the box has to be inside those. Where the cursor can move, a
// receivable line's block is its name, its readings and then the box, so the
// box sits two lines in. Where NOTHING can move the window — one navigable row,
// the state serial capture and an order with nothing receivable are always in —
// the box LEADS its block, because a block whose start is all a short pane
// keeps must start with the thing the operator types into. Those two numbers
// are the builder's promise; measuring the block as built is what let a
// regression widen its own gate.
func receiveCursorBoxDrawn(t *testing.T, s *ReceiveFormScreen, w, h int) (drawn, payable bool) {
	t.Helper()
	line, _ := receiveCursorBox(t, s)
	body, _ := s.body()
	// How many lines of the cursor's block come BEFORE its box: the line's own
	// name, and nothing else — everything a row has to say about itself is
	// drawn after the field it is about (addLineBlock). On a body with one
	// navigable row the field leads outright.
	lead := 0
	if body.rowsIn(0, body.Len()) > 1 {
		lead = 1
	}
	avail := s.bodyAvailForBar(len(s.headerLines()), s.bar())
	payable = body.Len() <= avail || avail-2 > lead
	return strings.Contains(receivePaneText(s, w, h), line), payable
}

// receiveAssertBoxDrawn fails when the pane could have drawn the cursor's own
// input and did not. Split out so all three arms of the height sweep ask it,
// rather than the resting one asking and the other two taking it on trust.
func receiveAssertBoxDrawn(t *testing.T, s *ReceiveFormScreen, w, h int, what string) {
	t.Helper()
	drawn, payable := receiveCursorBoxDrawn(t, s, w, h)
	if payable && !drawn {
		line, depth := receiveCursorBox(t, s)
		t.Errorf("%s has room for the box on row %d (%d lines into its block) and does not "+
			"draw it — %q:\n%s", what, s.focused, depth, line, receiveClippedPane(s, w, h))
	}
}

// TestReceive_AShortPaneStillDrawsTheForm.
//
// The unconditional note reservation bought a fixed point — writing a note can
// never move the bar below it — and cost four pinned rows. On a short pane
// those four rows were the whole body budget: at 80x12 the pane keeps six rows,
// the bar and the status row take two, bodyAvailForBar answered 0, and
// jdeLines.Window returned nothing. The frame was four BLANK rows over a status
// row and a bar — no line names, no quantity box, nothing — and it was built
// empty, so clampToBox was nowhere near it. Every height up to 16 lost the form
// the same way.
//
// The two height sweeps beside this one ran those exact heights and passed,
// because both only compare "the bar names PgUp/PgDn" against "the cursor
// moved". A blackout satisfies that: nothing on the pane is asserted at all.
// So this asserts the two properties TOGETHER, because the fix sits between
// them — the reservation yields to the PANE (geometry, which the bar and the
// guard see identically) and never to the note's PRESENCE (which would be the
// circle reopened).
func TestReceive_AShortPaneStillDrawsTheForm(t *testing.T) {
	for _, width := range receiveWidths {
		for height := 8; height <= 30; height++ {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, receiveManyLines(3), width, height)

				if !receiveFrameDrawn(t, s, width, height) {
					return
				}
				// Onto a LINE, because that is the row this asserts about: a
				// fresh form opens on the scan row, which carries no line label
				// for the identification below to find.
				r = receiveGoToLine(t, r, s, 0)
				if !receiveFrameDrawn(t, s, width, height) {
					return
				}
				// The form is drawn at rest: the row the cursor is standing on
				// is what the operator is here to type into.
				if !receiveCursorRowIdentified(t, s, width, height) {
					t.Fatalf("the resting frame does not draw the row the cursor is on:\n%s",
						receiveClippedPane(s, width, height))
				}
				receiveAssertBoxDrawn(t, s, width, height, "the resting frame")
				restingHeader := len(s.headerLines())
				restingNames := receiveBarNames(s, "PgUp/PgDn")

				// Writing a note must not move the header, the bar's claim, or
				// the form off the pane.
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
				if s.note.text == "" {
					t.Fatalf("the refusal wrote no note, so this height proves nothing")
				}
				if got := len(s.headerLines()); got != restingHeader {
					t.Errorf("writing a note moved the pinned header from %d rows to %d — "+
						"the reservation is conditional on the note again",
						restingHeader, got)
				}
				if got := receiveBarNames(s, "PgUp/PgDn"); got != restingNames {
					t.Errorf("writing a note flipped whether the bar names PgUp/PgDn "+
						"(%v -> %v)", restingNames, got)
				}
				if !receiveFrameDrawn(t, s, width, height) {
					return
				}
				if !receiveCursorRowIdentified(t, s, width, height) {
					t.Errorf("the note pushed the cursor's own row off the pane:\n%s",
						receiveClippedPane(s, width, height))
				}
				receiveAssertBoxDrawn(t, s, width, height, "the note")
				// And the answer to that keypress is on the pane too: a note
				// squeezed to nothing is a decline the operator cannot read.
				if pane := receivePaneText(s, width, height); !strings.Contains(pane, "enter") {
					t.Errorf("the note was squeezed off the pane entirely:\n%s", pane)
				}
			})

			// The FAILURE frame at the same height. The clamp that fixed the
			// resting frame measured the note block alone and left the failure
			// detail — pinned in the SAME header — unaccounted for, so at 80x16
			// a 502's three rows of gateway HTML took the body's whole budget
			// and the form was blank again. The sweep beside this one ran that
			// height and could not see it: it never put a failure on the screen.
			t.Run(fmt.Sprintf("%dx%d with a failure", width, height), func(t *testing.T) {
				fake := &receiveFake{failWith: http.StatusBadGateway, failBody: nginx502}
				r, s := receiveDrive(t, fake, receiveManyLines(3), width, height)
				s.qty[0].SetValue("1")
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // qty -> review
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // the receipt fails
				if s.failDetail == "" {
					t.Fatalf("no failure detail is standing, so this height proves nothing")
				}
				// Back on the quantity form with the failure standing: that is
				// the frame this height sweep is about, and the one the header
				// allocator's failure arms are written for.
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
				if s.phase != phaseQty {
					t.Fatalf("esc after a failed receipt landed on phase %v", s.phase)
				}
				r = receiveGoToLine(t, r, s, 0)
				if !receiveFrameDrawn(t, s, width, height) {
					return
				}
				if !receiveCursorRowIdentified(t, s, width, height) {
					t.Fatalf("a failure blanked the row the cursor is on:\n%s",
						receiveClippedPane(s, width, height))
				}
				receiveAssertBoxDrawn(t, s, width, height, "a standing failure")
				// The failure is what gives first, but it gives its TAIL, not
				// itself. The headline is on the status row and never gives, so
				// the operator always knows something failed — but "something
				// failed" without a reason is the state that decides nothing:
				// it cannot tell them whether to retype a quantity or fetch
				// somebody. The allocator used to hand the note its whole
				// ceiling and the detail the remainder, so at every body budget
				// of 7 or less the remainder was zero and a 502 arrived with no
				// reason at all.
				pane := receivePaneText(s, width, height)
				if !strings.Contains(pane, "failed") {
					t.Errorf("the failure headline is not on the pane:\n%s", pane)
				}
				// "502" can only have come from the DETAIL: the headline the
				// status row draws is "Receiving PO-1001 failed" and carries no
				// status code.
				budget := s.bodyAvailForBar(0, s.barCeiling())
				if budget > receiveBodyFloor && !strings.Contains(pane, "502") {
					t.Errorf("the pane can pay %d body rows but carries no reason for the "+
						"failure — the detail was dropped whole rather than shortened:\n%s",
						budget, pane)
				}
				_ = r
			})
		}
	}
}

// TestReceive_ThePagingPairIsNamedWhenAPageMovesTheCursor.
//
// qtyPagesFor used to ask only bodyScrollsForBar — "is the body taller than the
// window" — but PgUp/PgDn move the CURSOR and the window follows it. The two
// questions come apart in a DESIGNED state: an order whose lines are all voided
// or already received leaves no quantity boxes, so the notes row is the only
// navigable row and jdePageCursor clamps to it. At 80x18 the body still
// overflowed, so the bar printed PgUp/PgDn=Page over a key whose whole effect
// was to write "pgdown is already at the last row".
//
// Asserted as the biconditional at every height, on BOTH orders, because a
// predicate that is merely stricter would pass a test that only checked the
// dead direction.
func TestReceive_ThePagingPairIsNamedWhenAPageMovesTheCursor(t *testing.T) {
	orders := map[string][]omsapi.ReceivingLine{
		"nothing receivable": nil,
		"nine lines":         receiveManyLines(9),
		"three lines":        receiveManyLines(3),
	}
	for name, lines := range orders {
		for height := 10; height <= 30; height++ {
			t.Run(fmt.Sprintf("%s at 80x%d", name, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, lines, 80, height)
				named := receiveBarNames(s, "PgUp/PgDn")

				// Try BOTH directions from the top: the pair is named when
				// either can move, and pgdown is the one with room from row 0.
				before := s.focused
				r = receiveKey(t, r, poPhaseKeyMsg("pgdown"))
				movedDown := s.focused != before
				before = s.focused
				r = receiveKey(t, r, poPhaseKeyMsg("pgup"))
				moved := movedDown || s.focused != before

				if named != moved {
					t.Fatalf("the bar names PgUp/PgDn = %v but a page moved the cursor = %v"+
						" (inputs %d)\n%s", named, moved, s.totalInputs(),
						receiveClippedPane(s, 80, height))
				}
			})
		}
	}
}

// receiveBodyLine is one line of a built body as the operator would read it —
// styling stripped by lipgloss (no TTY under test) and whitespace collapsed the
// way receivePaneText collapses the pane, so a line can be looked for on the
// pane without the comparison turning into a fight about indentation.
func receiveBodyLine(t *testing.T, body *jdeLines, i int) string {
	t.Helper()
	got := strings.Join(strings.Fields(body.text[i]), " ")
	if got == "" {
		t.Fatalf("body line %d is blank, so looking for it on the pane would prove nothing", i)
	}
	return got
}

// TestReceive_NoBodyLineSitsWhereNoKeyCanReach.
//
// jdeLines.Window anchors the window on the CURSOR's block, and no key on this
// screen moves a cursor above the first navigable row: up wraps round to the
// last row, which moves the window further DOWN, and jdePageCursor clamps at 0
// so pgup answers "already at the first row". A line tagged jdeNoRow ahead of
// the first block is therefore a line NO key can bring onto the pane — while
// the layer goes on drawing "↑ N more above" and counting it, so the frame
// tells the operator there is something up there and then refuses every key
// they reach for.
//
// It was not hypothetical. At the canonical 80x24 with one kit line the pane
// opened on "↑ 5 more above": the order heading and the whole kit caveat — the
// sentence that stops "received 2" being read as two of the thing named on the
// line — sat behind a marker nothing could act on. Before the conversion the
// form drew every line and clampToBox cut from the BOTTOM, so those lead lines
// were always visible; the conversion inverted which end is lost.
//
// The property asserted here is the one that makes the layer's marker honest,
// and it is asserted in both halves: STRUCTURALLY, that no line of the body
// falls outside a navigable row; and BEHAVIOURALLY, that the body's first line
// is on the pane at rest and comes back after the cursor has been walked to the
// far end and returned with the keys the bar names.
func TestReceive_NoBodyLineSitsWhereNoKeyCanReach(t *testing.T) {
	kit := receiveWSKit(11, "Eufy printer maintenance kit (CMYK + cleaning)", 2, 0)
	orders := map[string][]omsapi.ReceivingLine{
		// The kit order is the one that reported this: its credit block makes
		// its own block nine lines, which is what pushes a lead out.
		"one kit line": {kit},
		"a kit among plain lines": {kit, receiveWSLine(12, "Box of M3 bolts", 4, 0),
			receiveWSLine(13, "Reel of wire", 4, 0)},
		"four plain lines": receiveManyLines(4),
		// The order whose body is nothing BUT lead: no quantity boxes, so the
		// notes row is the only row there is and the screen's whole explanation
		// of itself used to sit above it.
		"nothing receivable": nil,
	}
	for name, lines := range orders {
		overflowed, sawMarker := false, false
		for height := 10; height <= 40; height++ {
			t.Run(fmt.Sprintf("%s at 80x%d", name, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, lines, 80, height)

				if !receiveFrameDrawn(t, s, 80, height) {
					return
				}
				body := s.qtyBody()
				for i, row := range body.row {
					if row == jdeNoRow {
						t.Fatalf("body line %d (%q) belongs to no navigable row, so no key "+
							"can bring it onto the pane once the body overflows",
							i, strings.Join(strings.Fields(body.text[i]), " "))
					}
				}
				if body.Len() > s.bodyAvailForBar(len(s.headerLines()), s.bar()) {
					overflowed = true
				}

				// At rest the cursor is on the first navigable row, and that
				// row's block starts at the body's first line — so the top of
				// the body is drawn and there is nothing above the window at
				// all.
				top := receiveBodyLine(t, body, 0)
				if pane := receivePaneText(s, 80, height); !strings.Contains(pane, top) {
					t.Fatalf("the resting frame does not draw the body's first line %q:\n%s",
						top, receiveClippedPane(s, 80, height))
				}
				if pane := receivePaneText(s, 80, height); strings.Contains(pane, "more above") {
					t.Fatalf("the resting frame hides lines above the cursor's own first row:\n%s",
						receiveClippedPane(s, 80, height))
				}

				if s.totalInputs() < 2 {
					// One row: there is nowhere to walk to, and the bar names
					// no key that moves — which the paging sweep beside this
					// one is what holds.
					return
				}
				// Walk to the far end with the key the bar names for it. If the
				// frame then says there is more above, the bar must name a key
				// that goes back — and pressing it must actually bring the top
				// line back.
				down := tea.KeyMsg{Type: tea.KeyDown}
				up := tea.KeyMsg{Type: tea.KeyUp}
				for i := 1; i < s.totalInputs(); i++ {
					r = receiveKey(t, r, down)
				}
				// The mirror at the far end, and it asks about the block's
				// FIRST line rather than the body's last.
				//
				// That is not a weakening. What must survive is the FOCUSED
				// notes field, which leads its block: a separator tagged to the
				// notes row instead of to the block above it makes the blank
				// the first line of that block and the field the second, and a
				// short window then draws the blank and leaves the operator
				// typing into a field that is not on the pane. The block's TAIL
				// is prose explaining the field, and a block that outruns the
				// window loses its tail by design — the layer keeps a block's
				// START. Asserting the last line would be asserting that this
				// screen's longest block always fits, which is a claim about
				// the pane rather than about the layout.
				tail := s.qtyBody()
				first, _ := tail.block(s.focused)
				lead := receiveBodyLine(t, tail, first)
				if pane := receivePaneText(s, 80, height); !strings.Contains(pane, lead) {
					t.Fatalf("standing on the last row does not draw its block's first line %q, "+
						"which is the focused field:\n%s", lead, receiveClippedPane(s, 80, height))
				}
				if strings.Contains(receivePaneText(s, 80, height), "more above") {
					sawMarker = true
					if !receiveBarNames(s, "UP/DN") {
						t.Fatalf("the frame says there is more above and the bar names no key "+
							"that moves the cursor:\n%s", receiveClippedPane(s, 80, height))
					}
				}
				for i := 1; i < s.totalInputs(); i++ {
					r = receiveKey(t, r, up)
				}
				if s.focused != 0 {
					t.Fatalf("walking down and back left the cursor on row %d, not row 0", s.focused)
				}
				back := receiveBodyLine(t, s.qtyBody(), 0)
				if pane := receivePaneText(s, 80, height); !strings.Contains(pane, back) {
					t.Fatalf("the keys the bar names did not bring the body's first line %q "+
						"back onto the pane:\n%s", back, receiveClippedPane(s, 80, height))
				}
			})
		}
		// Both halves of the sweep have to have had something to judge. An
		// order that fits every swept pane never overflows, and one the cursor
		// never leaves the top of never draws the marker — either way the
		// property above would be asserted over a frame it cannot break on,
		// which reads as coverage and is not.
		if !overflowed {
			t.Errorf("%s never overflowed the window at any swept height, so nothing here "+
				"was judged against a windowed body", name)
		}
		if !sawMarker && len(lines) > 0 {
			t.Errorf("%s never drew a \"more above\" marker at any swept height, so the "+
				"reachability half of this test never fired", name)
		}
	}
}

// TestReceive_AnUnsizedFrameKeepsEveryCeiling.
//
// headerSplit owns the "has the terminal told us anything?" question, and it is
// the ONLY place that question is asked: headerRoom is reached past that arm and
// so is only ever asked about a sized pane. It used to carry a branch of its own
// for a budget of zero, with a paragraph beside it explaining behaviour nothing
// performed — unreachable, because headerSplit tests the same condition three
// lines earlier and returns.
//
// What the surviving arm has to do is what this asserts: with no WindowSizeMsg
// there is no geometry to divide, so every ceiling is kept whole and the frame
// draws entire for Root's clampToBox to cut.
func TestReceive_AnUnsizedFrameKeepsEveryCeiling(t *testing.T) {
	lines := receiveManyLines(3)
	s := NewReceiveFormScreen(Deps{Ctx: context.Background()}, receivePO(lines...))
	s.Update(receiveSheetMsg{sheet: receiveWorksheet(lines...)})
	if s.terminalHeight != 0 {
		t.Fatalf("the screen came up already sized (%d rows), so this proves nothing",
			s.terminalHeight)
	}
	s.setFail("Receiving PO-1001 failed", nginx502)
	_ = s.say("enter does nothing here", StatusWarn)

	if got, want := len(s.headerLines()), receiveNoteRows+receiveFailDetailRows+1; got != want {
		t.Errorf("an unsized header is %d rows, want every ceiling (%d)", got, want)
	}
	// And the body is still there: an unsized frame that clamped against a
	// budget it does not have is the blank form rounds of this work were spent
	// closing, reached from the other end.
	view := strings.Join(strings.Fields(s.View()), " ")
	if !strings.Contains(view, "Line-1") {
		t.Errorf("an unsized frame draws no form:\n%s", s.View())
	}
}

// TestReceive_EveryRowDrawsTheBoxTheCursorIsOn walks the cursor across every
// row at every swept height, which is the axis the sweep beside this one does
// not have: it judges the resting frame, where the cursor is always row 0, and
// row 0 is the one row whose block was built correctly.
//
// The defect it exists for: the inter-line separator was tagged to the block
// BELOW it, so every block except the first opened on a blank line. Window
// keeps a block's START when the block will not fit, so that blank was the
// first line those blocks drew — at 80x17 with three lines the body window is
// one row, and pressing Down drew the blank: "↑ 4 more above", nothing, "↓ 8
// more below", not one word about the line the cursor had just moved to. The
// rule the same commit wrote into AGENTS.md — a separator travels with the
// block ABOVE it, so a block never opens on a blank — was honoured for the last
// separator and broken for every other one.
func TestReceive_EveryRowDrawsTheBoxTheCursorIsOn(t *testing.T) {
	orders := map[string][]omsapi.ReceivingLine{
		"three plain lines": receiveManyLines(3),
		"a kit among plain lines": {
			receiveWSKit(11, "Eufy printer maintenance kit (CMYK + cleaning)", 2, 0),
			receiveWSLine(12, "Box of M3 bolts", 4, 0),
			receiveWSLine(13, "Reel of wire", 4, 0),
		},
		// The order with NOTHING receivable belongs here for the same reason
		// serial capture does: its notes row is the only navigable row, so no
		// key moves the window and whatever leads that block is the whole of
		// what a short pane keeps, permanently. Drawn prose-first it kept the
		// sentence and lost the FOCUSED box — an operator typing into a field
		// off the pane, every keystroke redrawing it byte for byte — and the
		// sweep could not see it, because it only ever built orders that had
		// lines.
		"nothing receivable": nil,
	}
	for name, lines := range orders {
		payable, unpayable := 0, 0
		for height := 10; height <= 30; height++ {
			t.Run(fmt.Sprintf("%s at 80x%d", name, height), func(t *testing.T) {
				fake := &receiveFake{}
				r, s := receiveDrive(t, fake, lines, 80, height)
				// In SEQUENCE, with no rebuild between presses: a defect that
				// only shows once the cursor has left row 0 is invisible to a
				// test that resets the screen for each position.
				for row := 0; row < s.totalInputs(); row++ {
					if s.focused != row {
						t.Fatalf("walking down landed on row %d, want %d", s.focused, row)
					}
					// The notes row carries no line label to be identified by,
					// so only the receivable rows are asked that question.
					if !receiveFrameDrawn(t, s, 80, height) {
						return
					}
					if _, isLine := s.lineAt(row); isLine && !receiveCursorRowIdentified(t, s, 80, height) {
						t.Fatalf("standing on row %d, the pane says nothing about which row "+
							"that is:\n%s", row, receiveClippedPane(s, 80, height))
					}
					receiveAssertBoxDrawn(t, s, 80, height, fmt.Sprintf("row %d", row))
					if _, ok := receiveCursorBoxDrawn(t, s, 80, height); ok {
						payable++
					} else {
						unpayable++
					}
					r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
				}
			})
		}
		// Both halves have to have been exercised. Every position payable at
		// every height would mean the sweep never reached the short panes the
		// separator defect lived on; none payable would mean the box assertion
		// never fired at all.
		if payable == 0 {
			t.Errorf("%s: the box was never drawable at any swept height and row, so the "+
				"assertion above never judged anything", name)
		}
		if unpayable == 0 {
			t.Errorf("%s: every swept height could pay for the box, so the sweep never "+
				"reached the short panes this property is about", name)
		}
	}
}

// TestReceive_AnUnnumberedOrderIsStillNamed.
//
// The body's "Receive into <order>" heading was dropped when the lead lines
// were found to be unreachable, and the justification for dropping it was that
// Root pins the screen's Title above the pane on every frame. That was true
// only while the order carried a NUMBER: Title answered a generic "Receive
// Items" otherwise, so an order with no number was named nowhere at all — not
// in the title, not in the body — on the screen that moves stock. The rest of
// the screen already answers that case (orderName falls back to "PO #<id>", and
// the working line and the receipt sentence both read it), so the title was the
// one voice disagreeing.
//
// Asserted through the whole terminal frame as well as through Title, because
// the claim being made is about what Root DRAWS, and Title returning the right
// string would prove nothing if the title line were not on the frame.
func TestReceive_AnUnnumberedOrderIsStillNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		po   *omsapi.PurchaseOrder
		want string
	}{
		{"numbered", receivePO(receiveManyLines(2)...), "PO-1001"},
		{"unnumbered", &omsapi.PurchaseOrder{ID: 5, Items: receivePO(receiveManyLines(2)...).Items}, "PO #5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := Deps{Ctx: context.Background()}
			s := NewReceiveFormScreen(deps, tc.po)
			r := newTestRoot(s)
			next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			r = next.(Root)
			// The WORKSHEET is the fresher read of the order's number, so the
			// unnumbered case has to arrive unnumbered from both sources or it
			// is not the case it names.
			sheet := receiveWorksheet(receiveManyLines(2)...)
			sheet.Number = tc.po.Number
			next, _ = r.Update(receiveSheetMsg{sheet: sheet})
			r = next.(Root)

			if got := s.orderName(); got != tc.want {
				t.Fatalf("orderName() = %q, want %q — the fixture is not the case it names", got, tc.want)
			}
			if !strings.Contains(s.Title(), tc.want) {
				t.Errorf("the title %q does not name the order (%s)", s.Title(), tc.want)
			}
			if frame := strings.Join(strings.Fields(r.View()), " "); !strings.Contains(frame, tc.want) {
				t.Errorf("nothing on the terminal names the order (%s):\n%s", tc.want, r.View())
			}
		})
	}
}
