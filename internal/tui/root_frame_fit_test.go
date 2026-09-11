// Root's one promise to the terminal: THE FRAME IS NEVER TALLER THAN THE
// TERMINAL.
//
// bubbletea keeps the BOTTOM rows of a frame that is taller than the terminal,
// so every row over is a row lost off the TOP — the sidebar's title, the
// screen's title, the head of whatever the operator was reading — for as long
// as the frame stays that tall. The status bar is the surface that broke it: a
// New PO submit answered by a 502 gateway page made Root.View() 26 rows on a
// 24-row terminal (32 on 30), because nothing flattened the flashed message
// and lipgloss drew each of the page's lines as another bar row. The screens'
// own sweeps could not see it — they measure the PANE, and the pane was the
// right size; it was the frame around it that grew — and no check asserted the
// frame's height, which is why it was never reported.
//
// So this file asserts on the whole of what Root hands bubbletea, at every
// terminal size up to the widest and tallest this project sweeps (refused
// sizes included, because Root's own refusal is a frame too), with every
// surface Root composes holding the worst it can be handed at once:
//
//   - the STATUS BAR, whose public inputs are each proven to change its rendered
//     row (TestRoot_TheFrameSweepStatusInputsReachTheRenderedBar);
//   - the SCREEN, through a stand-in whose Title and View return adversarial
//     text. Root's bound on the body does not look at which screen drew it, so
//     the input space is "any string a screen can return", and the fixtures are
//     the SHAPES that defeat a height bound: more lines than the pane, lines
//     wider than it, and characters lipgloss measures as one width and draws
//     as another or that move a terminal cursor down without a newline;
//   - the SIDEBAR, at the longest tree the workspace list can expand into
//     (derived, not named) with the cursor mid-list and at the end.
package tui

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// rootFrameScreen is a Screen that returns whatever the sweep hands it.
//
// wide, when set, is a line drawn FIRST that is exactly one cell wider than
// the pane Root gives the screen at the width it was last sized to — built of
// that rune, so a double-width rune reaches past the pane by a cell too. It is
// sized to the pane rather than fixed because the bound Root applies to an
// over-wide line re-measures it rune by rune: a fixed line long enough for the
// widest pane is ninety re-measures at the narrowest, at every height, for a
// question one cell answers.
type rootFrameScreen struct {
	title, view string
	wide        rune
	width       int
}

func (rootFrameScreen) Init() tea.Cmd { return nil }
func (s rootFrameScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if m, ok := msg.(tea.WindowSizeMsg); ok {
		s.width = m.Width
	}
	return s, nil
}
func (s rootFrameScreen) Title() string { return s.title }
func (s rootFrameScreen) View() string {
	if s.wide == 0 {
		return s.view
	}
	cell := lipgloss.Width(string(s.wide))
	n := (screenBodyCells(s.width) + 1 + cell - 1) / cell
	return strings.Repeat(string(s.wide), n) + "\n" + s.view
}

// rootFrameGateway is the measured case's message: what po_create.go flashes
// when the create POST is answered by a proxy's HTML page, which
// omsapi.parseError hands over WHOLE because the body carries no JSON code.
const rootFrameGateway = "create PO failed: oms: http 502: " + poGatewayHTML

// rootFrameLookup is the second measured case: the add-line flow's
// could-not-tell sentence, one line of 152 cells whose TAIL is the way out.
const rootFrameLookup = "could not tell whether Acme Fasteners & Industrial Supply Co. " +
	"supplies that — the lookup did not answer · enter tries again · esc goes back to the order"

// rootFrameMessages are the SHAPES a flashed message can take that defeat a
// one-row bar, each named for the one it carries. A message is an OMS body as
// often as it is a sentence — the failed-write flashes append err.Error(), and
// so every shape here is one a response body can arrive in.
func rootFrameMessages() []struct{ name, text string } {
	return []struct{ name, text string }{
		{"gateway page (LF)", rootFrameGateway},
		{"gateway page (CRLF)", strings.ReplaceAll(rootFrameGateway, "\n", "\r\n")},
		{"long one line", rootFrameLookup},
		{"wide runes", "保存に失敗しました：" + strings.Repeat("在庫品目", 40)},
		// Thirty double-width runes: FEWER runes than the bar has cells at every
		// width from 45 to 61, and more CELLS. A bound counted in runes calls it
		// a fit and lipgloss wraps it.
		{"wide runes, a rune count that fits", strings.Repeat("在", 30)},
		// lipgloss measures a tab as NO cells and draws it as four, so a line a
		// measurement says fits is drawn wider than the bar and WRAPS.
		{"tabs", "delete failed:" + strings.Repeat("\tfield\terror", 12)},
		{"short", "saved"},
		{"empty after flattening", "\n"},
	}
}

// rootFrameStatus is one state the status bar can be in.
type rootFrameStatus struct {
	name string
	// message is flashed when non-empty; empty is the no-message state, where
	// the bar draws its context run and the key hints.
	message string
	level   StatusLevel
	// busy is the context run at its widest: both connections down, a scanner
	// state nobody bounded, an unread count carrying a double-width emoji, and
	// a degraded chip whose label came off the wire.
	busy bool
	// unread is the ordinary state with mail waiting and nothing else: the
	// unread chip's 📬 is one rune and two cells, and with the run measured in
	// runes that alone made the idle frame 25 rows on a 24-row terminal at
	// every width from 76 up — not for a flash, for as long as the mail sat
	// there. It is its own state so the busy one's other parts cannot mask it.
	unread bool
}

func rootFrameStatuses() []rootFrameStatus {
	var out []rootFrameStatus
	for _, busy := range []bool{false, true} {
		out = append(out, rootFrameStatus{name: fmt.Sprintf("no message, busy=%v", busy), busy: busy})
		if !busy {
			out = append(out, rootFrameStatus{name: "no message, unread mail", unread: true})
		}
		for i, m := range rootFrameMessages() {
			out = append(out, rootFrameStatus{
				name:    fmt.Sprintf("%s, busy=%v", m.name, busy),
				message: m.text,
				// Every level is reached without multiplying the sweep by it.
				level: StatusLevel(i % (int(StatusError) + 1)),
				busy:  busy,
			})
		}
	}
	return out
}

// rootFrameDegraded is a degraded chip whose LABEL is adversarial. Labels are
// OMS-supplied, and the chip rides the same one-row bar as everything else.
func rootFrameDegraded(n int) []omsapi.ServiceStatus {
	out := make([]omsapi.ServiceStatus, n)
	for i := range out {
		out[i] = omsapi.ServiceStatus{
			Key:   fmt.Sprintf("svc%d", i),
			Label: "Outbound email via the\nprimary\trelay 電子メール",
			State: omsapi.ServiceStateOpen,
		}
	}
	return out
}

// apply puts the state on a sized bar.
func (st rootFrameStatus) apply(bar *StatusBar) {
	if st.busy {
		bar.SetOMSConn(false)
		bar.SetForgeKeyConn(false)
		bar.SetScanner("reading\nbadge\t電子")
		bar.SetUnread(123456)
		bar.SetDegradedServices(rootFrameDegraded(1))
	} else {
		bar.SetOMSConn(true)
		bar.SetForgeKeyConn(true)
		if st.unread {
			bar.SetUnread(3)
		}
	}
	if st.message != "" {
		bar.Flash(st.message, st.level, time.Hour)
	}
}

// rootFrameBodies are the shapes a SCREEN's output can take against Root's
// bound on the pane, plus the ordinary case.
//
// Each shape is ONE line, drawn FIRST, over plain filler that fills the
// tallest pane swept. First, because Root keeps a pane's first rows, so a
// shape anywhere else is dropped before it is measured at a short height; over
// filler, because a line that wraps inside a pane with rows to spare grows
// nothing, so a shape alone would reach the bound only on the shortest panes.
// And one line, because the bound Root applies re-measures an over-wide line
// rune by rune: forty of them would buy nothing but minutes.
//
// attacksWidth says which half of Root's bound a shape is aimed at: the ROWS
// it keeps (more lines than the pane) or the CELLS it keeps per line (a line
// that would wrap onto another row).
func rootFrameBodies() []struct {
	name         string
	scr          rootFrameScreen
	attacksWidth bool
	attacksRows  bool
} {
	filler := strings.TrimSuffix(strings.Repeat("row\n", rootFrameMaxHeight), "\n")
	shaped := func(first string) string { return first + "\n" + filler }
	return []struct {
		name         string
		scr          rootFrameScreen
		attacksWidth bool
		attacksRows  bool
	}{
		{"ordinary", rootFrameScreen{title: "Purchasing", view: "one\ntwo\nthree"}, false, false},
		{"taller than any pane", rootFrameScreen{title: "Tall", view: filler}, false, false},
		{"a cell wider than the pane", rootFrameScreen{title: "Wide", view: filler, wide: 'x'}, true, false},
		{"double-width runes a cell wider than the pane",
			rootFrameScreen{title: "Wide runes", view: filler, wide: '界'}, true, false},
		// Measured at 20 cells and drawn at 100 — past the widest pane swept
		// (TestRoot_TheFrameSweepReachesPastEveryBound holds that).
		{"tabs", rootFrameScreen{title: "Tabs\there", view: shaped(strings.Repeat("a\tb\t", 10))}, true, false},
		{"cursor-down controls", rootFrameScreen{title: "Controls", view: shaped("a\vb\fc\vd")}, false, true},
		{"multi-line title", rootFrameScreen{title: "one\ntwo\nthree\nfour", view: filler}, false, false},
	}
}

// rootFrameNavs are the sidebar states worth sweeping: the tree expanded as
// far as the workspace list lets it, with the cursor in the middle (a marker
// at each end) and on the last row. The workspace is DERIVED — whichever has
// the most surfaces — so a workspace that grows a surface moves the sweep.
func rootFrameNavs() []struct {
	name  string
	apply func(*Nav)
} {
	longest, most := Workspaces()[0].Key, -1
	for _, ws := range Workspaces() {
		if n := len(workspaceSurfaces(ws.Key)); n > most {
			longest, most = ws.Key, n
		}
	}
	return []struct {
		name  string
		apply func(*Nav)
	}{
		{"blurred, longest tree", func(n *Nav) { n.SetActive(longest) }},
		{"focused, cursor mid-tree", func(n *Nav) {
			n.SetActive(longest)
			n.Focus()
			n.Move(most / 2)
		}},
		{"focused, cursor on the last row", func(n *Nav) {
			n.SetActive(longest)
			n.Focus()
			n.Move(1 << 10)
		}},
	}
}

// rootFrameRender draws Root at one size with one state on every surface.
func rootFrameRender(scr rootFrameScreen, nav func(*Nav), st rootFrameStatus, w, h int) string {
	r := newTestRoot(scr)
	nav(&r.nav)
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	r = next.(Root)
	st.apply(&r.status)
	return r.View()
}

// rootFrameMaxWidth / rootFrameMaxHeight bound the sweep: the widest and
// tallest terminals the rest of the package sweeps (jdeDrawableWidths,
// jdePaneHeights). Every size from 1x1 up is walked, the refused ones included.
const (
	rootFrameMaxWidth  = 120
	rootFrameMaxHeight = 40
)

// TestRoot_TheFrameIsNeverTallerThanTheTerminal is the guard: at every terminal
// size, with every surface Root composes holding the worst it can be handed,
// Root.View() has no more rows than the terminal.
//
// The surfaces are swept one at a time against the ordinary case of the
// others, so a failure names the surface that grew the frame, and then all at
// their worst together, so two surfaces that each fit cannot sum past it.
func TestRoot_TheFrameIsNeverTallerThanTheTerminal(t *testing.T) {
	bodies, navs, statuses := rootFrameBodies(), rootFrameNavs(), rootFrameStatuses()
	ordinaryBody, ordinaryNav, quiet := bodies[0], navs[0], statuses[0]

	type combo struct {
		surface, name string
		scr           rootFrameScreen
		nav           func(*Nav)
		st            rootFrameStatus
	}
	var combos []combo
	for _, st := range statuses {
		combos = append(combos, combo{"status bar", st.name, ordinaryBody.scr, ordinaryNav.apply, st})
	}
	for _, b := range bodies {
		combos = append(combos, combo{"screen", b.name, b.scr, ordinaryNav.apply, quiet})
	}
	for _, n := range navs {
		combos = append(combos, combo{"sidebar", n.name, ordinaryBody.scr, n.apply, quiet})
	}
	// All at their worst together — every screen shape joined into ONE screen,
	// under the sidebar with a marker at each end of its window, under the
	// widest message and under the busiest context run. This is not where
	// coverage comes from (each surface's every state is swept above, at every
	// size); it checks the argument that lets the sweep above be per surface:
	// Root joins the sidebar and the pane side by side and the bar beneath
	// them, so a frame whose parts each fit fits whole. The cross product of
	// every state would be the same claim at twenty times the cost, in a
	// package that has blown go test's 600s timeout twice.
	var worst rootFrameScreen
	for _, b := range bodies[1:] {
		worst.title += b.scr.title + "\n"
		worst.view += b.scr.view + "\n"
		if b.scr.wide != 0 {
			worst.wide = b.scr.wide
		}
	}
	for _, st := range statuses {
		if !st.busy || (st.message != "" && st.message != rootFrameGateway) {
			continue
		}
		combos = append(combos, combo{"all at once", navs[1].name + " / " + st.name, worst, navs[1].apply, st})
	}

	// One parallel subtest per combination: the sizes are the expensive axis
	// and every combination walks all of them, so this is where the cores go.
	for _, c := range combos {
		t.Run(c.surface+"/"+c.name, func(t *testing.T) {
			t.Parallel()
			first := ""
			over := 0
			firstControl := ""
			controls := 0
			for w := 1; w <= rootFrameMaxWidth; w++ {
				for h := 1; h <= rootFrameMaxHeight; h++ {
					view := rootFrameRender(c.scr, c.nav, c.st, w, h)
					if strings.ContainsAny(view, paneVerticalBreaks) {
						controls++
						if firstControl == "" {
							firstControl = fmt.Sprintf("%dx%d", w, h)
						}
					}
					rows := strings.Count(view, "\n") + 1
					if rows <= h {
						continue
					}
					over++
					if first == "" {
						first = fmt.Sprintf("%dx%d draws %d rows (%d lost off the top); the bottom rows are:\n%s",
							w, h, rows, rows-h, rootFrameTail(view, 4))
					}
				}
			}
			if over > 0 {
				t.Errorf("the frame is taller than the terminal at %d size(s); first at %s", over, first)
			}
			if controls > 0 {
				t.Errorf("the frame contains a vertical tab or form feed at %d size(s); first at %s", controls, firstControl)
			}
		})
	}
}

// rootFrameTail is the last n rows of a frame, ANSI stripped, for a failure
// message: the rows bubbletea keeps are the bottom ones, so those are what the
// operator was actually shown.
func rootFrameTail(view string, n int) string {
	lines := strings.Split(view, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		lines[i] = "  |" + stripStatusANSI(l) + "|"
	}
	return strings.Join(lines, "\n")
}

// TestRoot_TheFrameSweepReachesPastEveryBound keeps the guard above from being
// vacuous: every adversarial fixture must be one that WOULD defeat the bound it
// is aimed at if nothing bounded it. A fixture that fits anyway makes the
// sweep pass for a reason unrelated to the property it names — the
// vacuous-fixture rule (AGENTS.md).
//
// Each message shape, handed to the bar's style with no flattening and no
// clip, must draw more than one content row at the narrowest width Root draws
// (the "short" case is the control and is exempt).
//
// Each screen shape must overrun the pane with the OTHER half of Root's bound
// still applied — a shape aimed at the cells a line keeps is judged on the rows
// Root would keep, since every shape sits over filler that overruns the pane
// by line count alone and would otherwise pass this for the filler's sake.
func TestRoot_TheFrameSweepReachesPastEveryBound(t *testing.T) {
	narrowest := jdeDrawableWidths()[0]
	for _, m := range rootFrameMessages() {
		if m.name == "short" {
			continue
		}
		raw := StyleStatusBar.Width(narrowest).Render(m.text)
		if rows := strings.Count(raw, "\n") + 1; rows <= 2 {
			t.Errorf("message fixture %q draws %d row(s) unbounded at width %d — it cannot "+
				"reach the one-row bound it is here to test", m.name, rows, narrowest)
		}
	}
	// A shape that reaches past the pane at the WIDEST and TALLEST size swept
	// reaches past it everywhere: a narrower pane only wraps more, and a
	// shorter one only holds less. The narrowest and shortest corner is asked
	// too, so a fixture cannot be tuned to one end.
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	corners := [][2]int{{widths[0], heights[0]}, {widths[len(widths)-1], heights[len(heights)-1]}}
	for _, b := range rootFrameBodies()[1:] {
		for _, c := range corners {
			w, h := c[0], c[1]
			// Root.View's own arithmetic for the pane: the sidebar and its
			// border off the width, the bar off the height, then StyleContent's
			// padding off both.
			contentWidth, contentHeight := w-24-1, h-2
			innerHeight := contentHeight - 2
			sized, _ := b.scr.Update(tea.WindowSizeMsg{Width: w, Height: h})
			content := lipgloss.JoinVertical(lipgloss.Left, StyleTitle.Render(b.scr.title), "", sized.View())
			if b.attacksRows {
				if !strings.Contains(content, "\v") || !strings.Contains(content, "\f") {
					t.Errorf("screen fixture %q does not carry both cursor-down controls into Root's pane boundary at %dx%d", b.name, w, h)
				}
				continue
			}
			lines := strings.Split(content, "\n")
			if !b.attacksWidth {
				if len(lines) <= innerHeight {
					t.Errorf("screen fixture %q is %d line(s) at %dx%d, where the pane keeps %d — "+
						"it cannot reach the bound it is here to test", b.name, len(lines), w, h, innerHeight)
				}
				continue
			}
			if len(lines) > innerHeight {
				lines = lines[:innerHeight]
			}
			raw := StyleContent.Width(contentWidth).Render(strings.Join(lines, "\n"))
			if rows := strings.Count(raw, "\n") + 1; rows <= contentHeight {
				t.Errorf("screen fixture %q draws %d row(s) at %dx%d with only the rows bound applied, "+
					"where the pane is %d — it cannot reach the bound it is here to test",
					b.name, rows, w, h, contentHeight)
			}
		}
	}
}

func TestRoot_TheFrameSweepStatusInputsReachTheRenderedBar(t *testing.T) {
	inputs := map[string]func(*StatusBar){
		"SetOMSConn":          func(s *StatusBar) { s.SetOMSConn(false) },
		"SetForgeKeyConn":     func(s *StatusBar) { s.SetForgeKeyConn(false) },
		"SetScanner":          func(s *StatusBar) { s.SetScanner("reading\nbadge\t電子") },
		"SetUnread":           func(s *StatusBar) { s.SetUnread(123456) },
		"SetDegradedServices": func(s *StatusBar) { s.SetDegradedServices(rootFrameDegraded(1)) },
		"Flash":               func(s *StatusBar) { s.Flash(rootFrameGateway, StatusError, time.Hour) },
	}

	typ := reflect.TypeOf((*StatusBar)(nil))
	seen := map[string]bool{}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if name == "SetWidth" || (name != "Flash" && !strings.HasPrefix(name, "Set")) {
			continue
		}
		seen[name] = true
		apply, ok := inputs[name]
		if !ok {
			t.Errorf("public status-bar input %s is not driven by the frame sweep", name)
			continue
		}
		quiet := NewStatusBar()
		quiet.SetWidth(120)
		rootFrameStatus{}.apply(&quiet)
		before := stripStatusANSI(quiet.View())
		apply(&quiet)
		if after := stripStatusANSI(quiet.View()); after == before {
			t.Errorf("frame-sweep input %s did not reach the rendered status bar", name)
		}
	}
	var stale []string
	for name := range inputs {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		t.Errorf("frame-sweep input %s is not a public StatusBar input", name)
	}
}

// TestRoot_AGatewayPageOnSubmitKeepsTheFrameOnTheTerminal is the measured
// case, driven the way it happened: a New PO submit answered by a proxy's HTML
// page, through the real screen and the real Root. It drew 26 rows on a 24-row
// terminal (32 on 30); bubbletea kept the bottom 24, so the sidebar's title and
// the screen's were off the top for as long as the flash stood, and the bar's
// third row ended mid-word.
//
// What the operator must see instead: every row of the frame on the terminal —
// the TOP row is asserted, since it is the one that was lost — and the failure
// on the bottom line, left-justified, flattened, and marked as cut.
func TestRoot_AGatewayPageOnSubmitKeepsTheFrameOnTheTerminal(t *testing.T) {
	for _, h := range poPaneSizes {
		t.Run(fmt.Sprintf("80x%d", h), func(t *testing.T) {
			fake := &poPickFake{reorder: 15, catalog: 2, assets: 1, committees: 1, failCreate: true}
			r, screen := poPickerAtSize(t, fake, 80, h)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEsc})
			r = poStageCostlessLine(t, r, screen)
			r = key(t, r, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
			r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter}) // submit → 502 page
			if !strings.Contains(r.status.message, "<!DOCTYPE html>\n") {
				t.Fatalf("the submit did not flash the gateway page, so this drives nothing: %q",
					r.status.message)
			}

			lines := strings.Split(r.View(), "\n")
			if len(lines) != h {
				t.Fatalf("the frame is %d rows on a %d-row terminal:\n%s", len(lines), h, rootFrameTail(r.View(), 5))
			}
			if top := stripStatusANSI(lines[0]); !strings.Contains(top, "scantty") {
				t.Errorf("the frame's top row is not the sidebar's title — it went off the terminal:\n%q", top)
			}
			bar := strings.TrimRight(stripStatusANSI(lines[h-1]), " ")
			if !strings.HasPrefix(bar, " create PO failed: oms: http 502: <!DOCTYPE html> <html>") {
				t.Errorf("the bottom line does not lead with the failure, flattened:\n%q", bar)
			}
			if !strings.HasSuffix(bar, "…") {
				t.Errorf("the failure was cut and the bar does not say so:\n%q", bar)
			}
		})
	}
}

// TestRoot_ACutFlashLeavesTheWayOutOnThePane is the second measured case: the
// add-line lookup that could not tell flashes a 152-cell sentence whose tail —
// "enter tries again · esc goes back to the order" — is the way out, and a
// one-row bar at 80 columns cannot hold it. The bar marks the cut (the
// captain's decision); what makes that a legitimate trade rather than a dead
// end is that the keys are still named on the PANE, by the action bar the
// columnar layer draws at every height it draws a frame at all. Asserted at
// every drawable height, because the pane's own copy of the sentence is a
// header row a short pane trims and the action bar is not.
func TestRoot_ACutFlashLeavesTheWayOutOnThePane(t *testing.T) {
	heights := jdePaneHeights()
	fake := &poAddFake{rows: poAddRows(), fail: true}
	r, s := poAddAt(t, fake, 80, heights[len(heights)-1])
	r = key(t, r, poRuneKey("hovercraft"))
	r = key(t, r, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.HasSuffix(r.status.message, "esc goes back to the order") {
		t.Fatalf("the lookup did not flash the could-not-tell sentence: %q", r.status.message)
	}
	// Resized through every height rather than rebuilt at each: the flash is
	// the same one throughout, which is the sequence an operator dragging the
	// terminal goes through, and it keeps the drive to one lookup.
	drawn := 0
	for _, h := range heights {
		next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: h})
		r = next.(Root)
		if !r.status.msgExpiry.After(time.Now()) {
			t.Fatalf("80x%d: the flash expired mid-sweep, so the bar row asserts nothing", h)
		}
		view := r.View()
		lines := strings.Split(view, "\n")
		if len(lines) > h {
			t.Errorf("80x%d: the frame is %d rows", h, len(lines))
			continue
		}
		bar := strings.TrimRight(stripStatusANSI(lines[len(lines)-1]), " ")
		if !strings.HasSuffix(bar, "…") {
			t.Errorf("80x%d: the sentence was cut and the bar does not say so:\n%q", h, bar)
		}
		if !s.frameDrawn(len(s.headerLines()), s.bar()) {
			// A pane too short to draw is refused with the layer's own notice,
			// which names the way out itself.
			continue
		}
		drawn++
		pane := stripStatusANSI(view)
		for _, want := range []string{"Enter=", "Esc="} {
			if !strings.Contains(pane, want) {
				t.Errorf("80x%d: the bar row cut the way out and the pane does not name %s either:\n%s",
					h, want, pane)
			}
		}
	}
	if drawn == 0 {
		t.Fatal("no drawable height drew the add-line frame, so the way out was never looked for")
	}
}
