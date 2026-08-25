package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// receiveFake is the OMS these drives run against. It COUNTS the receipts, so a
// double-post is caught on the wire rather than inferred from the screen.
type receiveFake struct {
	mu        sync.Mutex
	receipts  int
	serials   int
	failWith  int    // HTTP status for the receive, 0 = succeed
	failBody  string // the body it fails with
	serialErr bool
}

func (f *receiveFake) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/receive/"):
			f.receipts++
			if f.failWith != 0 {
				w.WriteHeader(f.failWith)
				_, _ = w.Write([]byte(f.failBody))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 5, "po_number": "PO-1001",
				"total_received_quantity": 2, "total_quantity": 9,
			})
		case strings.HasSuffix(r.URL.Path, "/serialized-components/"):
			f.serials++
			if f.serialErr {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"detail":"serial SN-1 is already recorded against another unit"}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "sc-1", "serial_number": "SN-1"})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "in_stock"})
		}
	}
}

func (f *receiveFake) count() (receipts, serials int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.receipts, f.serials
}

// receiveDrive opens the form against `fake` at a real size and hands back the
// Root, the screen and the fake — everything a drive needs to read the pane AND
// the wire.
func receiveDrive(t *testing.T, fake *receiveFake, lines []omsapi.PurchaseOrderItem, w, h int) (Root, *ReceiveFormScreen) {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	deps := Deps{OMS: omsapi.New(srv.URL), Ctx: context.Background()}
	s := NewReceiveFormScreen(deps, &omsapi.PurchaseOrder{ID: 5, Number: "PO-1001", Items: lines})
	r := newTestRoot(s)
	r.deps = deps
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Root), s
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

// receiveManyLines is an order long enough that the form cannot fit on a pane —
// the state the conversion added windowing for. Each line's name carries its
// own number so a test can say WHICH line it is looking at.
func receiveManyLines(n int) []omsapi.PurchaseOrderItem {
	out := make([]omsapi.PurchaseOrderItem, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, omsapi.PurchaseOrderItem{
			ID:              10 + i,
			Description:     fmt.Sprintf("Line-%d hex bolt, zinc plated", i+1),
			QuantityOrdered: 4 + i, QuantityPending: 4 + i,
		})
	}
	return out
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
	if s.focused >= len(s.lines) {
		t.Fatalf("the cursor is on row %d, the notes row, which carries no line label", s.focused)
	}
	fields := strings.Fields(s.lines[s.focused].DisplayLabel())
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
	long := poKitFixtureLine()
	states := []struct {
		name  string
		lines []omsapi.PurchaseOrderItem
		drive func(*testing.T, Root, *ReceiveFormScreen) Root
	}{
		{"quantities", []omsapi.PurchaseOrderItem{long, poPlainLine()}, nil},
		{"nothing receivable", nil, nil},
		{"serial capture", receiveSweepLines(), func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			s.qty[2].SetValue("1")
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		}},
		{"summary", receiveSweepLines(), func(t *testing.T, r Root, s *ReceiveFormScreen) Root {
			s.qty[2].SetValue("1")
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			return receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
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
	line := omsapi.PurchaseOrderItem{
		ID: 21, QuantityOrdered: 2, QuantityPending: 2,
		Description: "Stainless steel socket-head cap screw, M8 x 40mm, A4-80 marine grade, box of 100",
	}
	widthOf := func(term int) int {
		fake := &receiveFake{}
		_, scr := receiveDrive(t, fake, []omsapi.PurchaseOrderItem{line}, term, 30)
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
			for s.focused != len(s.qty) {
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
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
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
				// And the form came back LIVE: a failed receipt must not hold
				// the operator's quantities hostage to a gateway.
				if !s.qty[0].Focused() {
					t.Error("the caret did not come back after a failed receipt")
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
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.phase != phaseDone {
		t.Fatalf("a receipt with nothing serialized left the flow on phase %v", s.phase)
	}
	for _, k := range []string{"enter", "enter", "enter"} {
		next, _ := r.Update(poPhaseKeyMsg(k))
		r = next.(Root)
	}
	if got, _ := fake.count(); got != 1 {
		t.Errorf("the delivery was booked %d times; pressing enter again must not repost it", got)
	}
	if !strings.Contains(r.View(), "Receive complete") {
		t.Errorf("the summary is not what the operator is left looking at:\n%s", r.View())
	}
}

// TestReceive_EnterReceivesFromAnyRow is the binding this conversion changed.
// Enter used to advance a field and submit only from the last one; every other
// columnar sheet in purchasing commits from any row, and a screen that reserves
// Enter for "next field" teaches a rule that is false one screen over.
func TestReceive_EnterReceivesFromAnyRow(t *testing.T) {
	for _, row := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("from row %d", row), func(t *testing.T) {
			fake := &receiveFake{}
			r, s := receiveDrive(t, fake, receiveManyLines(2), 80, 24)
			s.qty[0].SetValue("2")
			for i := 0; i < row; i++ {
				r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
			}
			if s.focused != row {
				t.Fatalf("the cursor is on row %d, want %d", s.focused, row)
			}
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
			if got, _ := fake.count(); got != 1 {
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
		{"not a number", "two", "line 1: quantity must be a whole number"},
		{"negative", "-3", "line 1: quantity must be a whole number"},
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
			if got, _ := fake.count(); got != 0 {
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

// TestReceive_AFailedSerialKeepsTheUnitAndWhatWasTyped.
//
// The capture used to advance past the unit whatever happened, so the error was
// drawn under the NEXT unit's prompt — describing a unit that had not been
// attempted — and the serial the operator had typed was discarded with no way
// to enter it again. On the one screen whose entire job is capturing what they
// typed.
func TestReceive_AFailedSerialKeepsTheUnitAndWhatWasTyped(t *testing.T) {
	fake := &receiveFake{serialErr: true}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("2") // two units of the serialized line
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != phaseSerial || len(s.serialUnits) != 2 {
		t.Fatalf("capture did not open: phase %v, %d units", s.phase, len(s.serialUnits))
	}

	before := s.serialCursor
	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})

	if s.serialCursor != before {
		t.Errorf("a failed capture advanced to unit %d — the error would be drawn under a "+
			"unit that was never attempted", s.serialCursor+1)
	}
	if got := s.serialInput.Value(); got != "SN-1" {
		t.Errorf("the serial the operator typed was discarded: %q", got)
	}
	pane := strings.Join(receivePaneLines(s, 80, 24), "\n")
	if !strings.Contains(pane, "Serial capture failed") {
		t.Errorf("the failure is not on the pane:\n%s", pane)
	}
	if text := receivePaneText(s, 80, 24); !strings.Contains(text, "already recorded against another unit") {
		t.Errorf("the server's own reason is not on the pane:\n%s", pane)
	}
	// And the bar offers the retry rather than claiming a save.
	if !barHas(s.bar(), "Enter", "Retry serial") {
		t.Errorf("the bar does not offer a retry after a failure: %+v", s.bar())
	}

	// Retrying against a server that now accepts it captures the unit and moves on.
	fake.mu.Lock()
	fake.serialErr = false
	fake.mu.Unlock()
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.serialCursor != before+1 {
		t.Errorf("a successful retry did not advance: cursor %d", s.serialCursor)
	}
	if s.createdCount != 1 {
		t.Errorf("the retry did not record the unit: created %d", s.createdCount)
	}
	_ = r
}

// TestReceive_TheSerialBarFollowsTheBox. With nothing typed, Enter SKIPS the
// unit; saying "Save" there would name a key that does something else. The bar
// is the only place that fact is stated, so it has to follow the box.
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
	if !barHas(s.bar(), "Enter", "Save serial") {
		t.Errorf("a filled box does not offer the save: %+v", s.bar())
	}
	// Blank + enter skips, and the summary counts it.
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // saves SN-9
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // blank: skips unit 2
	if s.phase != phaseDone {
		t.Fatalf("the flow did not finish after the last unit: phase %v", s.phase)
	}
	if s.skippedCount != 1 || s.createdCount != 1 {
		t.Errorf("want one saved and one skipped, got created=%d skipped=%d",
			s.createdCount, s.skippedCount)
	}
	pane := strings.Join(receivePaneLines(s, 80, 24), "\n")
	for _, want := range []string{"Receive complete", "1 created", "Skipped", "1 unit"} {
		if !strings.Contains(pane, want) {
			t.Errorf("the summary does not report %q:\n%s", want, pane)
		}
	}
}

// TestReceive_TheSummaryReportsUncapturedUnits. Leaving capture early is a
// legitimate answer — the serials may be recorded elsewhere — so the summary
// has to say how many units it left, derived from where the cursor stopped
// rather than counted alongside it.
func TestReceive_TheSummaryReportsUncapturedUnits(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("3")
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	r = receiveType(t, r, poRuneKey("SN-1"))
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // one captured
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})   // two left

	if s.phase != phaseDone {
		t.Fatalf("esc did not finish capture: phase %v", s.phase)
	}
	pane := strings.Join(receivePaneLines(s, 80, 24), "\n")
	if !strings.Contains(pane, "Uncaptured") || !strings.Contains(pane, "2 units") {
		t.Errorf("the summary does not report the units it left:\n%s", pane)
	}
	// A count of zero is not drawn at all: "Failed ..... 0" is a row that makes
	// an operator look for a failure there was none of.
	if strings.Contains(pane, "Failed") {
		t.Errorf("the summary reports a failure count with no failures:\n%s", pane)
	}
}

// TestReceive_AKitOrderStillSaysWhatItCredits guards the kit contract through
// the conversion: the tag, the unit of the quantity box, and the breakdown all
// have to survive the move onto the columnar layer. po_kit_lines_test.go holds
// the arithmetic; this holds that the columnar rows did not lose it.
func TestReceive_AKitOrderStillSaysWhatItCredits(t *testing.T) {
	for _, width := range receiveWidths {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			fake := &receiveFake{}
			_, s := receiveDrive(t, fake, []omsapi.PurchaseOrderItem{poKitFixtureLine()}, width, 30)
			s.qty[0].SetValue("1")
			pane := strings.Join(receivePaneLines(s, width, 30), "\n")
			for _, want := range []string{
				poKitTag,          // the line is marked
				"ordered 2 kits",  // the quantity column's unit, on the row
				"kits",            // and again beside the box the number goes in
				"COMPONENT items", // the standing caveat
				"receiving 1 kit", // what the typed quantity would credit
				"3 × Black ink",   // per-kit, not the pre-multiplied figure
			} {
				if !strings.Contains(pane, want) {
					t.Errorf("the kit contract lost %q at %d columns:\n%s", want, width, pane)
				}
			}
			if strings.Contains(pane, "6 × Black ink") {
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
	const frozen = "frozen until the receipt answers"

	t.Run("the receipt opens serial capture", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
		s.qty[2].SetValue("1") // the serialized line, so the reply moves phase
		r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if !s.pending {
			t.Fatal("the receipt did not go out")
		}
		r, _ = receiveInFlight(t, r, poPhaseKeyMsg("j"))
		if got := receivePaneText(s, 80, 24); !strings.Contains(got, frozen) {
			t.Fatalf("a key pressed under the freeze did not say so:\n%s", got)
		}

		r = receiveSettle(t, r, cmd, 0)
		if s.phase != phaseSerial {
			t.Fatalf("the receipt left the flow on phase %v, want serial", s.phase)
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
		r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		r, _ = receiveInFlight(t, r, poPhaseKeyMsg("j"))
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

	t.Run("a captured serial", func(t *testing.T) {
		fake := &receiveFake{}
		r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
		s.qty[2].SetValue("2") // two units, so the reply lands mid-capture
		r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if s.phase != phaseSerial {
			t.Fatalf("capture did not open: phase %v", s.phase)
		}
		s.serialInput.SetValue("SN-1")
		r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
		if !s.serialPending {
			t.Fatal("the capture did not go out")
		}
		r, _ = receiveInFlight(t, r, poPhaseKeyMsg("j"))
		if got := receivePaneText(s, 80, 24); !strings.Contains(got, frozen) {
			t.Fatalf("a key pressed under the capture freeze did not say so:\n%s", got)
		}

		r = receiveSettle(t, r, cmd, 0)
		if got := receivePaneText(s, 80, 24); strings.Contains(got, frozen) {
			t.Errorf("the capture answered and the unit advanced, but the pane still "+
				"claims a request is out:\n%s", got)
		}
	})
}

// TestReceive_TheFrozenFormOffersNoRowToTypeInto.
//
// jdeFieldArea draws a focused text row as a solid reverse-video field — the
// layer's strongest "you are standing here and may type" signal — and while the
// receipt is out every key but Esc declines. submit() blurs the box for exactly
// that reason; drawing the row focused anyway put the invitation back on the
// pane, which is the bar-honesty rule broken in its most visual form. serialBody
// already answered the identical question with !s.serialPending one function
// over, so the two halves of the same screen disagreed about it.
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
			r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyDown})
			s.qty[s.focused].SetValue("1")
			if receiveFocusedRow(s, width, 24) == "" {
				t.Fatalf("no row is highlighted before the receipt goes out:\n%s",
					strings.Join(receivePaneLines(s, width, 24), "\n"))
			}
			cursor := receiveCursorMarker(t, s)

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

			// And a failed receipt hands the keyboard back: the invitation
			// returns with the keys it stands for.
			r = receiveSettle(t, r, cmd, 0)
			if s.pending {
				t.Fatal("the receipt is still in flight after the fake answered")
			}
			if receiveFocusedRow(s, width, 24) == "" {
				t.Errorf("the form came back live but no row says where the caret is:\n%s",
					strings.Join(receivePaneLines(s, width, 24), "\n"))
			}
		})
	}
}

// TestReceive_LeavingCaptureMidSaveLeavesNoCaretBehind.
//
// Esc is never gated on these screens, so it is pressed while a create is out
// and finishSerial moves to the summary with the box blurred. The reply then
// lands with units still enrolled, and advanceSerial's non-terminal branch used
// to focus the box unconditionally — re-arming a caret in a field the summary
// does not draw, one line after handleSerialUnit had asked the very question
// that guard exists to answer.
func TestReceive_LeavingCaptureMidSaveLeavesNoCaretBehind(t *testing.T) {
	fake := &receiveFake{}
	r, s := receiveDrive(t, fake, receiveSweepLines(), 80, 24)
	s.qty[2].SetValue("3") // three units, so the reply leaves two unanswered
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if s.phase != phaseSerial {
		t.Fatalf("capture did not open: phase %v", s.phase)
	}

	s.serialInput.SetValue("SN-1")
	r, cmd := receiveInFlight(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if !s.serialPending {
		t.Fatal("the capture did not go out")
	}
	r = receiveKey(t, r, tea.KeyMsg{Type: tea.KeyEsc})
	if s.phase != phaseDone {
		t.Fatalf("esc under a capture left the flow on phase %v, want done", s.phase)
	}

	r = receiveSettle(t, r, cmd, 0)
	if s.phase != phaseDone {
		t.Fatalf("the capture's reply dragged the operator back to phase %v", s.phase)
	}
	if s.serialInput.Focused() {
		t.Error("the summary is on screen with a caret armed in a box it does not draw")
	}
	if got := receivePaneText(s, 80, 24); !strings.Contains(got, "Receive complete") {
		t.Errorf("the summary is not what the operator is left looking at:\n%s", got)
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
		{"a typo", map[int]string{1: "two"}, true, "line 2: quantity must be a whole number"},
		{"a negative", map[int]string{1: "-3"}, true, "line 2: quantity must be a whole number"},
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
			receipts, _ := fake.count()
			if tc.named && tc.wantSay == "" {
				if receipts != 1 {
					t.Fatalf("a named Enter posted %d receipts, want 1", receipts)
				}
				return
			}
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
		lines []omsapi.PurchaseOrderItem
		typed map[int]string
		keys  []tea.KeyMsg
		bare  bool // fire the last key without settling, so a request is out
	}
	var probes []probe
	for _, k := range long {
		probes = append(probes,
			probe{"qty page edge " + k, receiveManyLines(9), map[int]string{0: "2"},
				[]tea.KeyMsg{poPhaseKeyMsg(k)}, false},
			probe{"summary decline " + k, receiveManyLines(3), map[int]string{0: "2"},
				[]tea.KeyMsg{enter, poPhaseKeyMsg(k)}, false},
			probe{"frozen " + k, receiveManyLines(3), map[int]string{0: "2"},
				[]tea.KeyMsg{enter, poPhaseKeyMsg(k)}, true},
		)
	}
	probes = append(probes,
		probe{"enter with nothing typed", receiveManyLines(9), nil, []tea.KeyMsg{enter}, false},
		probe{"enter with every box zero", receiveManyLines(9), map[int]string{0: "0", 1: "0"},
			[]tea.KeyMsg{enter}, false},
		probe{"enter with nothing receivable", nil, nil, []tea.KeyMsg{enter}, false},
		probe{"enter on an unparseable quantity", receiveManyLines(9),
			map[int]string{8: "12345678"}, []tea.KeyMsg{enter}, false},
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
			if s.note.text == "" {
				t.Skip("this probe wrote no note")
			}
			drawn := 0
			for _, line := range s.noteLines() {
				if strings.TrimSpace(line) != "" {
					drawn++
				}
			}
			if drawn > receiveNoteRows {
				t.Fatalf("the note takes %d rows, more than the %d reserved: %q",
					drawn, receiveNoteRows, s.note.text)
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
			r, s := receiveDrive(t, fake, receiveManyLines(3), width, 40)
			if s.qtyPages() {
				t.Fatalf("the form already pages at %dx40, so shrinking proves nothing", width)
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
