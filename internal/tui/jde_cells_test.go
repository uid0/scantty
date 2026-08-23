// A terminal frame, decoded into CELLS — the assertion this suite did not have.
//
// Every visual contract on the columnar layer is carried by an escape sequence:
// the focused row is a reverse-video run, the focused label is bold, the fill is
// underscores in the border colour. None of that survives into a test binary,
// because lipgloss detects that stdout is not a TTY and renders FLAT — so a
// frame with the highlight and a frame without it are byte-identical strings of
// identical width. Three separate regressions on this layer shipped through a
// green suite for exactly that reason (see jdeFitInputValue): a bounded box that
// swallowed the fill, and a TextStyle repair that then reversed the typed value.
// Review was the only thing that caught either.
//
// It is closable, and this file closes it. lipgloss's profile is a package-level
// setting, not a property of the terminal — color_swatch_test.go's
// withColorProfile has been forcing it for the swatch tests all along. Forced to
// TrueColor, Render emits the real sequences, and a frame can be decoded into
// what an operator would actually SEE:
//
//	\x1b[7;38;5;213m   \x1b[0m   ->   three cells, reverse, accent
//
// jdeCells is that decoder, and jdeFieldRow reads one columnar row out of a
// CLIPPED Root.View() frame. What they make assertable is the whole class:
// which columns are reversed, which are not, and whether the caret is on screen
// at all. TestJDECells_SeesWhatAFlatRenderCannot is the canary that keeps the
// decoder honest — an assertion that can only see plain text passes vacuously,
// which is the failure mode that got us here.
package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// jdeCell is one column of a rendered frame: the rune drawn in it and the
// attributes it is drawn with. Only the two attributes this layer signals with
// are tracked — reverse video says WHERE THE CURSOR IS, bold says which label
// belongs to it — because a decoder that tracked everything would fail on a
// palette change that means nothing.
type jdeCell struct {
	r       rune
	reverse bool
	bold    bool
}

// jdeCells decodes one rendered line into its cells.
//
// It walks CSI sequences rather than stripping them, because the question a
// visual test asks is not "what text is here" but "what is this column drawn
// like" — and that is only answerable by carrying the SGR state forward from
// cell to cell the way a terminal does.
//
// Colour parameters are consumed WHOLE (38;5;n and 38;2;r;g;b). A naive scan for
// the parameter "7" would read the 256-colour index 7 as "reverse video on" and
// report a highlight on every row painted in colour 7 — a decoder that invents
// the thing under test is worse than no decoder.
//
// One column per rune: nothing on a columnar row is double-width today, and the
// row indices this file compares against (jdeLeader's offset) count runes too,
// so the two agree. A wide rune would put them one apart.
func jdeCells(line string) []jdeCell {
	var (
		out           []jdeCell
		bold, reverse bool
		runes         = []rune(line)
		i             int
	)
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			j := i + 2
			for j < len(runes) && !(runes[j] >= '@' && runes[j] <= '~') {
				j++
			}
			if j >= len(runes) {
				// An unterminated sequence: the line was cut through an escape,
				// which is itself a defect, but there is nothing left to decode.
				break
			}
			if runes[j] == 'm' {
				params := strings.Split(string(runes[i+2:j]), ";")
				for k := 0; k < len(params); k++ {
					switch params[k] {
					case "", "0":
						bold, reverse = false, false
					case "1":
						bold = true
					case "22":
						bold = false
					case "7":
						reverse = true
					case "27":
						reverse = false
					case "38", "48":
						// Skip the colour this introduces so none of its
						// parameters can be read as an attribute.
						if k+1 < len(params) && params[k+1] == "5" {
							k += 2
						} else if k+1 < len(params) && params[k+1] == "2" {
							k += 4
						}
					}
				}
			}
			i = j + 1
			continue
		}
		out = append(out, jdeCell{r: runes[i], reverse: reverse, bold: bold})
		i++
	}
	return out
}

// jdePlain is the text of a decoded line, with every sequence gone.
func jdePlain(cells []jdeCell) string {
	var b strings.Builder
	for _, c := range cells {
		b.WriteRune(c.r)
	}
	return b.String()
}

// jdeFieldRow finds one columnar row in a frame and returns its cells together
// with the column its INPUT AREA starts at — the cell just past the leader,
// which is where the value is drawn and where the fill begins.
//
// The frame it is given must be Root.View()'s, not the screen's: clampToBox
// truncates in Root, so a row that overruns the pane is whole in the screen's
// own output and cut in the one the terminal shows. Reading the wrong one is
// what let an over-long value pass as fitting.
func jdeFieldRow(t *testing.T, frame, label string) ([]jdeCell, int) {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		cells := jdeCells(line)
		plain := jdePlain(cells)
		at := jdeColumnOf(plain, label+" ")
		lead := jdeColumnOf(plain, jdeLeader)
		if at < 0 || lead < 0 || lead < at {
			continue
		}
		return cells, lead + len([]rune(jdeLeader))
	}
	t.Fatalf("no %q row in the frame:\n%s", label, frame)
	return nil, 0
}

// jdeColumnOf is where `want` starts in a decoded line, counted in CELLS.
//
// strings.Index counts BYTES, and every frame in this app carries the nav
// column's "│" — three bytes, one column — before anything a field test wants to
// look at. Mixing the two puts every column index two to the left of the cell it
// names, which reads exactly like a highlight that has crept into the leader.
func jdeColumnOf(plain, want string) int {
	at := strings.Index(plain, want)
	if at < 0 {
		return -1
	}
	return utf8.RuneCountInString(plain[:at])
}

// jdeFocusedFieldRow is the row the cursor is standing on, found the way an
// operator finds it: the only columnar row whose LABEL is bold. Tests use it
// rather than a label so that one sweep can hold every converted sheet to the
// same contract without knowing what any of their fields are called.
func jdeFocusedFieldRow(t *testing.T, frame string) ([]jdeCell, int, bool) {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		cells := jdeCells(line)
		lead := jdeColumnOf(jdePlain(cells), jdeLeader)
		if lead < 0 {
			continue
		}
		bold := false
		for _, c := range cells[:lead] {
			if c.bold {
				bold = true
				break
			}
		}
		if bold {
			return cells, lead + len([]rune(jdeLeader)), true
		}
	}
	return nil, 0, false
}

// jdeReverseRuns are the contiguous reverse-video runs of a row, as
// [start, end) cell ranges. The SHAPE of the highlight is what the contracts in
// this package are about: one run that ends the input area is a focused text
// row; a run one cell long is a caret with no field behind it; a run holding
// anything but spaces is a value that has been styled when it should not be.
func jdeReverseRuns(cells []jdeCell) [][2]int {
	var runs [][2]int
	start := -1
	for i, c := range cells {
		if c.reverse && start < 0 {
			start = i
		} else if !c.reverse && start >= 0 {
			runs = append(runs, [2]int{start, i})
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, [2]int{start, len(cells)})
	}
	return runs
}

// TestJDECells_SeesWhatAFlatRenderCannot is the canary for every other test in
// this file. All of them assert things about reverse video, and every one of
// them would pass VACUOUSLY against a frame that carries no sequences at all —
// which is precisely the frame a Go test binary produces by default. So this
// pins both halves: flat by default, and decodable once the profile is forced.
//
// If this ever fails, the visual assertions elsewhere in the package have gone
// blind rather than green.
func TestJDECells_SeesWhatAFlatRenderCannot(t *testing.T) {
	field := jdeField{Label: "Supplier", Kind: jdeValue, Value: "Acme", Focused: true}

	withColorProfile(t, termenv.Ascii)
	flat := jdeCells(renderJDEField(field, 8, 0))
	if runs := jdeReverseRuns(flat); len(runs) != 0 {
		t.Errorf("a flat render should carry no attributes at all, got runs %v — the "+
			"decoder is inventing them", runs)
	}
	if !strings.Contains(jdePlain(flat), "Acme") {
		t.Errorf("the flat render lost its text: %q", jdePlain(flat))
	}

	withColorProfile(t, termenv.TrueColor)
	lit := jdeCells(renderJDEField(field, 8, 0))
	runs := jdeReverseRuns(lit)
	if len(runs) != 1 {
		t.Fatalf("a focused value row should be one reverse run, got %v in %q", runs, jdePlain(lit))
	}
	if start, end := runs[0][0], runs[0][1]; jdePlain(lit[start:end]) != "Acme" {
		t.Errorf("the highlight covers %q, want the value %q", jdePlain(lit[start:end]), "Acme")
	}
	if jdePlain(flat) != jdePlain(lit) {
		t.Errorf("the two renders differ in TEXT (%q vs %q) — then a width test could have "+
			"caught this and the blind spot would not exist", jdePlain(flat), jdePlain(lit))
	}
	if len(flat) != len(lit) {
		t.Errorf("the two renders are %d and %d cells wide — the blind spot this file exists "+
			"for is that they are IDENTICAL", len(flat), len(lit))
	}
}

// jdeTextRowCase is one converted sheet, sized and standing on a text row.
//
// It exists to flatten the five sweep families and the purchasing sheets into
// ONE list, because the rule being checked is the layer's and not any screen's:
// a fix that reached only the screens someone listed is what produced the two
// workarounds this bead deleted.
type jdeTextRowCase struct {
	name string
	// build returns the sheet and the Root that CLIPS it, already sized, with
	// the cursor on a text row. Built per width and per test so a burst typed by
	// one assertion cannot reach another.
	build func(t *testing.T, width int) (Screen, Root)
	// typeInto is what to send to put a value in the focused box. Some fields
	// take digits only and some cap their length, so the assertions never
	// depend on what actually landed — only that whatever did is drawn inside
	// the field.
	typeInto string
}

const jdeCellHeight = 34

// jdeSized drives a screen through a sized Root, which is the only render an
// operator ever sees.
func jdeSized(t *testing.T, s Screen, width int) (Screen, Root) {
	t.Helper()
	r := newTestRoot(s)
	next, _ := r.Update(tea.WindowSizeMsg{Width: width, Height: jdeCellHeight})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return s, after
}

// jdeTextRowCases is every sheet in the package that draws a typed row and can
// be reached from a test: the twenty-five forms of sweeps A–E, the pilot, the
// four purchasing sheets, and the filter box every picker opens.
//
// The set is not a judgement call. A sheet is affected by the sizing rule if and
// only if it builds a jdeField with an Input, and
// TestJDEForm_EveryTextRowIsSizedByTheLayer holds the package to there being no
// other way to build a text row — it reads the whole package's syntax tree and
// fails any row that draws its own string instead of handing over its box — so
// the files carrying an Input ARE the blast radius, and these cases cover every
// one of them.
func jdeTextRowCases(t *testing.T) []jdeTextRowCase {
	t.Helper()
	var out []jdeTextRowCase

	// Sweeps A–E. Each family builds its own cases at a fixed width, so they are
	// rebuilt here per test and re-sized through Root.
	out = append(out, jdeSweepTextRows(t, "sweep A", jdeSweepARows)...)
	out = append(out, jdeSweepTextRows(t, "sweep B", jdeSweepBRows)...)
	out = append(out, jdeSweepTextRows(t, "sweep C", jdeSweepCRows)...)
	out = append(out, jdeSweepTextRows(t, "sweep D", jdeSweepDRows)...)
	out = append(out, jdeSweepTextRows(t, "sweep E", jdeSweepERows)...)

	// The pilot. po_edit is the reference every other sheet is held to, and it
	// is a text row on the first field.
	out = append(out, jdeTextRowCase{
		name:     "po edit (pilot)",
		typeInto: "ACME INDUSTRIAL SUPPLY COMPANY OF GREATER TOLEDO LLC",
		build: func(t *testing.T, width int) (Screen, Root) {
			s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
			s.cursor = 0
			return jdeSized(t, s, width)
		},
	})

	// The four purchasing sheets whose local workaround this bead deleted. They
	// are here to prove the deletion restored parity rather than to test the
	// purchasing screens: they now go through the same line of jdeFieldArea as
	// the pilot above, and these cases are what says so.
	detail := func(name, typing string, open func(*PurchaseOrderDetailScreen)) jdeTextRowCase {
		return jdeTextRowCase{
			name:     name,
			typeInto: typing,
			build: func(t *testing.T, width int) (Screen, Root) {
				s, r := poDetailAt(t, width)
				open(s)
				return s, r
			},
		}
	}
	out = append(out,
		detail("po mark shipped", "20260820", func(s *PurchaseOrderDetailScreen) { s.openShipForm() }),
		detail("po void order", "damaged in transit and returned to the supplier unopened",
			func(s *PurchaseOrderDetailScreen) { s.openVoidForm() }),
		detail("po mark delivered", "20260820", func(s *PurchaseOrderDetailScreen) { s.openDeliverForm() }),
		jdeTextRowCase{
			name:     "po attachments upload",
			typeInto: "/home/operator/scans/2026-08/incoming/purchase-order-0042.pdf",
			build: func(t *testing.T, width int) (Screen, Root) {
				s, r := poAttachAt(t, width)
				s.openUpload()
				return s, r
			},
		},
	)

	// The picker's filter box lives in jde_form.go itself and is the one text
	// row no sheet owns — every converted form opens the same one. It is reached
	// the way an operator reaches it, with Ctrl-E on a picker row.
	for _, family := range []struct {
		name string
		rows func(t *testing.T) []jdeSweepRow
	}{
		{"sweep A", jdeSweepARows}, {"sweep B", jdeSweepBRows}, {"sweep C", jdeSweepCRows},
		{"sweep D", jdeSweepDRows}, {"sweep E", jdeSweepERows},
	} {
		rows, family := family.rows, family.name
		picked := false
		for _, r := range rows(t) {
			if !r.picker || picked {
				continue
			}
			picked = true
			name := r.name
			out = append(out, jdeTextRowCase{
				name:     family + "/" + name + " picker filter",
				typeInto: jdeSweepBurst,
				build: func(t *testing.T, width int) (Screen, Root) {
					t.Helper()
					for _, fresh := range rows(t) {
						if fresh.name != name {
							continue
						}
						s, root := jdeSized(t, fresh.screen, width)
						fresh.setCursor(fresh.pickerRow)
						s.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
						return s, root
					}
					t.Fatalf("%s: case %q vanished between builds", family, name)
					return nil, Root{}
				},
			})
		}
	}

	// The pilot's own sub-sheet. It was still rendering textinput.View() by hand
	// when this bead started — a 40-column field on the reference screen, which
	// is as central as an unnamed case gets.
	out = append(out, jdeTextRowCase{
		name:     "po edit void line",
		typeInto: "damaged in transit and returned to the supplier unopened",
		build: func(t *testing.T, width int) (Screen, Root) {
			s := NewPurchaseOrderEditScreen(Deps{}, samplePO())
			_, r := jdeSized(t, s, width)
			s.openVoidLine(0)
			return s, r
		},
	})

	return out
}

// jdeCellWidths are the widths every columnar sheet must hold at. 80 is the one
// that matters — the JD Edwards World width, and the one clampToBox actually
// cuts at — and the other two are there so nothing can be tuned for 80 alone.
var jdeCellWidths = []int{80, 100, 120}

func widthLabel(w int) string { return strconv.Itoa(w) + " columns" }

// TestJDEField_FocusedTextRowHighlightsItsFill is the pilot's contract, held
// against every converted sheet at once: on the row the cursor is standing on,
// the input area ends in a reverse-video run and NOTHING ELSE on the row is
// reversed.
//
// Both halves have been broken in production on this layer, one after the other:
//
//	the run disappeared    — a locally bounded box padded its own value out to
//	                         Width, so the fill computed to 0 and the operator
//	                         lost the only cue saying which row they were on.
//	the run swallowed the  — the repair for that handed the box the highlight as
//	value                    its TextStyle, and bubbles applies TextStyle to the
//	                         VALUE RUNES too, so the typed text came out reversed
//	                         where the pilot draws it plain.
//
// Neither changed the width of anything, which is why the suite saw neither.
func TestJDEField_FocusedTextRowHighlightsItsFill(t *testing.T) {
	for _, tc := range jdeTextRowCases(t) {
		for _, width := range jdeCellWidths {
			t.Run(tc.name+"/"+widthLabel(width), func(t *testing.T) {
				withColorProfile(t, termenv.TrueColor)
				_, r := tc.build(t, width)

				cells, area, ok := jdeFocusedFieldRow(t, r.View())
				if !ok {
					t.Fatalf("no row is drawn as focused — the sheet has no cursor on it:\n%s", r.View())
				}
				runs := jdeReverseRuns(cells)
				if len(runs) == 0 {
					t.Fatalf("the focused row carries no highlight at all: %q", jdePlain(cells))
				}
				if len(runs) > 1 {
					t.Errorf("the focused row has %d separate reverse runs (%v) — the field is one "+
						"block, so this is a value that has been styled: %q",
						len(runs), runs, jdePlain(cells))
				}

				run := runs[len(runs)-1]
				if run[0] < area {
					t.Errorf("the highlight starts at column %d, before the input area at %d — it is "+
						"covering the label or the leader: %q", run[0], area, jdePlain(cells))
				}
				if n := run[1] - run[0]; n < 2 {
					t.Errorf("the highlight is %d cell(s) wide: that is the caret with no field behind "+
						"it, which is what a box that pads its own value leaves: %q", n, jdePlain(cells))
				}
				for i := run[0]; i < run[1]; i++ {
					if cells[i].r != ' ' {
						t.Errorf("column %d of the highlight holds %q — the fill is reverse video, the "+
							"VALUE is plain (the pilot's rule): %q", i, cells[i].r, jdePlain(cells))
						break
					}
				}
			})
		}
	}
}

// TestJDEField_TypedValueStaysInsideItsField is the sizing rule itself, at every
// width, on every sheet: type more than the field can hold and the row is still
// a row — the value stops at the field, the caret is still on screen, and the
// value is still plain.
//
// The caret is the assertion that matters. clampToBox truncates, so an over-long
// row is not VISIBLY over-long in the frame it produces: it is a row whose tail
// has been cut off, and the first thing to go is the cursor. An operator typing
// against that is typing blind, which is exactly what the upload sheet did with
// a 60-column path in a 34-column field.
func TestJDEField_TypedValueStaysInsideItsField(t *testing.T) {
	for _, tc := range jdeTextRowCases(t) {
		if tc.typeInto == "" {
			continue
		}
		for _, width := range jdeCellWidths {
			t.Run(tc.name+"/"+widthLabel(width), func(t *testing.T) {
				withColorProfile(t, termenv.TrueColor)
				s, r := tc.build(t, width)

				// A scanner delivers its burst as one runes message, which is
				// also the fastest way to overfill a field.
				s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.typeInto)})

				frame := r.View()
				cells, area, ok := jdeFocusedFieldRow(t, frame)
				if !ok {
					t.Fatalf("no row is drawn as focused after typing:\n%s", frame)
				}
				runs := jdeReverseRuns(cells)
				if len(runs) == 0 {
					t.Fatalf("after typing, the focused row has no caret and no fill — the value ran "+
						"past the pane and the clip took the cursor with it: %q", jdePlain(cells))
				}
				for _, run := range runs {
					for i := run[0]; i < run[1]; i++ {
						if cells[i].r != ' ' {
							t.Fatalf("column %d of the highlight holds %q: the typed value is being "+
								"drawn in reverse video, where the pilot draws it plain: %q",
								i, cells[i].r, jdePlain(cells))
						}
					}
				}
				if runs[len(runs)-1][0] < area {
					t.Errorf("the caret sits at column %d, before the input area at %d: %q",
						runs[len(runs)-1][0], area, jdePlain(cells))
				}

				// And the frame as a whole still fits: no line of it was cut,
				// which for a clipped render means no line reached the width.
				for i, line := range strings.Split(frame, "\n") {
					if w := lipgloss.Width(line); w > width {
						t.Errorf("line %d is %d columns against a %d-column terminal: %q", i, w, width, line)
					}
				}
			})
		}
	}
}

// TestJDEField_BlurredOverLongValueSaysItWasCut: the two halves of a text row
// are bounded differently and only one may be cut.
//
// A FOCUSED box scrolls — it shows the window the caret is in, so what it draws
// is complete as far as it goes and carries no ellipsis. A BLURRED box does not
// scroll at all: jdeInputValue reads the raw value straight out of it, so that a
// masked field cannot give up its mask, and a raw value has had no window
// applied to it. The row therefore has to shorten it, and the cut must ANNOUNCE
// itself — an operator proof-reading a path before pressing Enter must not be
// shown a shortened one that reads whole.
//
// This is the third of the three faults, and the one with no workaround at all
// before now: the purchasing sheets handled it locally and every other sheet in
// the package simply drew the whole value.
func TestJDEField_BlurredOverLongValueSaysItWasCut(t *testing.T) {
	const path = "/home/operator/scans/2026-08/incoming/purchase-order-0042.pdf"

	for _, width := range jdeCellWidths {
		t.Run(widthLabel(width), func(t *testing.T) {
			withColorProfile(t, termenv.TrueColor)
			s, r := poAttachAt(t, width)
			s.openUpload()
			s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(path)})

			focused, _ := jdeFieldRow(t, r.View(), "File path")
			if strings.Contains(jdePlain(focused), "…") {
				t.Errorf("the FOCUSED row was cut and marked, but a scrolling box shows a whole "+
					"window: %q", jdePlain(focused))
			}
			if !strings.Contains(jdePlain(focused), "order-0042.pdf") {
				t.Errorf("the focused row lost the caret end of the value: %q", jdePlain(focused))
			}

			s.Update(tea.KeyMsg{Type: tea.KeyDown})
			blurred, _ := jdeFieldRow(t, r.View(), "File path")
			if !strings.Contains(jdePlain(blurred), "…") {
				t.Errorf("the BLURRED row shortened a %d-column path with nothing to say so: %q",
					lipgloss.Width(path), jdePlain(blurred))
			}
			if !strings.Contains(jdePlain(blurred), "/home/operator") {
				t.Errorf("a blurred box does not scroll, so the row reads from the START of the "+
					"value: %q", jdePlain(blurred))
			}
			if runs := jdeReverseRuns(blurred); len(runs) != 0 {
				t.Errorf("the blurred row carries a highlight at %v — the reverse-video block is "+
					"the one cue saying where the cursor IS: %q", runs, jdePlain(blurred))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The sweep families, flattened
// ---------------------------------------------------------------------------

// jdeSweepRow is one sweep case reduced to what a rendering test needs: a sized
// screen and the row to stand on. The five families each declare their own case
// struct with the same shape, so each is normalised into this one rather than
// generalised in place — the sweeps pin the KEY SCHEME and are not this file's
// to reshape.
type jdeSweepRow struct {
	name      string
	screen    Screen
	setCursor func(row int)
	// text is the index of a row that is typed into. A family whose sheet has
	// none is skipped: the sizing rule has nothing to say about a sheet with no
	// box on it.
	text int
	ok   bool
	// picker marks a sheet with a foreign-key row, and pickerRow is that row.
	// The picker's filter box is a text row of the SHARED layer rather than of
	// any sheet, so one per family is enough to hold it to the same contract.
	picker    bool
	pickerRow int
}

// jdeSweepBurst is what gets typed into a sheet whose fields are unknown to this
// file. Digits, because a numeric field rejects letters and would leave nothing
// on the row at all; long, because the point is to overfill; and delivered as
// ONE runes message, which is how a barcode scanner delivers a burst and the
// fastest way to overrun a field.
//
// Nothing asserted afterwards depends on what actually landed — a CharLimit or a
// Validate may keep most of it out. The rule is about what is DRAWN, whatever
// that turns out to be.
const jdeSweepBurst = "01234567890123456789012345678901234567890123456789012345678901234567890123456789"

func jdeSweepARows(t *testing.T) []jdeSweepRow {
	t.Helper()
	out := make([]jdeSweepRow, 0, 8)
	for _, c := range jdeSweepCases(t) {
		row, ok := c.kinds[jdeRowText]
		pick, hasPick := c.kinds[jdeRowPicker]
		out = append(out, jdeSweepRow{c.name, c.screen, c.setCursor, row, ok, hasPick, pick})
	}
	return out
}

func jdeSweepBRows(t *testing.T) []jdeSweepRow {
	t.Helper()
	out := make([]jdeSweepRow, 0, 8)
	for _, c := range jdeSweepBCases(t) {
		row, ok := c.kinds[jdeRowText]
		pick, hasPick := c.kinds[jdeRowPicker]
		out = append(out, jdeSweepRow{c.name, c.screen, c.setCursor, row, ok, hasPick, pick})
	}
	return out
}

func jdeSweepCRows(t *testing.T) []jdeSweepRow {
	t.Helper()
	out := make([]jdeSweepRow, 0, 8)
	for _, c := range jdeSweepCCases(t) {
		row, ok := c.kinds[jdeRowText]
		pick, hasPick := c.kinds[jdeRowPicker]
		out = append(out, jdeSweepRow{c.name, c.screen, c.setCursor, row, ok, hasPick, pick})
	}
	return out
}

func jdeSweepDRows(t *testing.T) []jdeSweepRow {
	t.Helper()
	out := make([]jdeSweepRow, 0, 8)
	for _, c := range jdeSweepDCases(t) {
		row, ok := c.kinds[jdeRowText]
		pick, hasPick := c.kinds[jdeRowPicker]
		out = append(out, jdeSweepRow{c.name, c.screen, c.setCursor, row, ok, hasPick, pick})
	}
	return out
}

func jdeSweepERows(t *testing.T) []jdeSweepRow {
	t.Helper()
	out := make([]jdeSweepRow, 0, 8)
	for _, c := range jdeSweepECases(t) {
		row, ok := c.kinds[jdeRowText]
		pick, hasPick := c.kinds[jdeRowPicker]
		out = append(out, jdeSweepRow{c.name, c.screen, c.setCursor, row, ok, hasPick, pick})
	}
	return out
}

// jdeSweepTextRows turns one sweep family into cases this file can drive.
//
// The build closure REBUILDS the family from scratch every time it is called,
// and finds its case by name rather than closing over the screen. A sweep case
// is a live screen: one shared between two widths would carry the first width's
// typed burst into the second, and a form that had already been overfilled is
// not the form the next assertion means to be looking at.
func jdeSweepTextRows(t *testing.T, family string, rows func(t *testing.T) []jdeSweepRow) []jdeTextRowCase {
	t.Helper()
	var out []jdeTextRowCase
	for _, r := range rows(t) {
		if !r.ok {
			continue
		}
		name := r.name
		out = append(out, jdeTextRowCase{
			name:     family + "/" + name,
			typeInto: jdeSweepBurst,
			build: func(t *testing.T, width int) (Screen, Root) {
				t.Helper()
				for _, fresh := range rows(t) {
					if fresh.name != name {
						continue
					}
					s, root := jdeSized(t, fresh.screen, width)
					fresh.setCursor(fresh.text)
					return s, root
				}
				t.Fatalf("%s: case %q vanished between builds", family, name)
				return nil, Root{}
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// The sizing rule itself
// ---------------------------------------------------------------------------

// TestJDEFitInputValue_MaskedBlurredValueStaysMasked: the shortening applied to
// a blurred row must go through jdeEchoValue's mask, not around it (sc-lmsi).
// It moved here from the purchasing tests when the bounding did: the rule was
// never a purchasing one, and the webhook secret is the field it was written
// for.
func TestJDEFitInputValue_MaskedBlurredValueStaysMasked(t *testing.T) {
	const secret = "correct-horse-battery-staple-and-then-some-more"
	ti := textinput.New()
	ti.Prompt = ""
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.SetValue(secret)

	fitted, _ := jdeFitRow(jdeField{Label: "Secret", Kind: jdeText, Width: 34}, 11, screenBodyWidth(80))
	got := jdeFitInputValue(ti, false, fitted.Width)

	for _, leak := range []string{"correct", "horse", "staple"} {
		if strings.Contains(got, leak) {
			t.Errorf("the masked value leaked %q: %q", leak, got)
		}
	}
	if !strings.Contains(got, "…") {
		t.Errorf("a masked value too long for its row should still say it was cut: %q", got)
	}
	if w, budget := lipgloss.Width(got), screenBodyWidth(80); w > budget {
		t.Errorf("the masked value is %d wide against a %d-column pane: %q", w, budget, got)
	}
}

// TestJDEFitInputValue_NeverExceedsItsArea is the rule stated as arithmetic,
// over the whole space of things that change what a box renders: how long the
// value is, where the caret is inside it, whether the row has focus, whether a
// placeholder is standing in for an empty value, and whether the box draws a
// prompt.
//
// The table is here because the sheet-level tests can only check the cases their
// sheets happen to produce, and three of the columns below are cases no sheet
// produces TODAY — a placeholder longer than its field, a prompt on a columnar
// box, a caret parked in the middle of an over-long value. Each is one edit away
// from existing, and each is a way the value could walk out of the pane again.
func TestJDEFitInputValue_NeverExceedsItsArea(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)

	values := []string{
		"",
		"a",
		"2026-08-20",
		"/home/operator/scans/2026-08/incoming/purchase-order-0042.pdf",
		strings.Repeat("W", 200),
	}
	for _, area := range []int{4, 6, 10, 12, 22, 34, jdeFieldWidth, screenBodyWidth(80)} {
		for _, value := range values {
			for _, caret := range []string{"start", "middle", "end"} {
				for _, focused := range []bool{true, false} {
					for _, extra := range []string{"none", "placeholder", "prompt"} {
						ti := textinput.New()
						ti.Prompt = ""
						ti.SetValue(value)
						switch caret {
						case "start":
							ti.SetCursor(0)
						case "middle":
							ti.SetCursor(len([]rune(value)) / 2)
						default:
							ti.CursorEnd()
						}
						switch extra {
						case "placeholder":
							ti.Placeholder = "type the whole path here, all of it"
						case "prompt":
							ti.Prompt = "> "
						}
						if focused {
							ti.Focus()
						}

						got := jdeFitInputValue(ti, focused, area)
						if w := lipgloss.Width(got); w > area {
							t.Errorf("area %d, %q value, caret %s, focused=%v, %s: rendered %d "+
								"columns — %q", area, valueName(value), caret, focused, extra, w, got)
						}
						// And the box itself is untouched: this layer sizes a
						// COPY, so two panes of different widths cannot leave
						// the operator's box in one another's state.
						if ti.Width != 0 {
							t.Errorf("area %d: the caller's box came back with Width %d — the "+
								"sizing has leaked out of the render", area, ti.Width)
						}
					}
				}
			}
		}
	}
}

// valueName keeps the table's failure messages readable when the value under
// test is 200 columns of W.
func valueName(v string) string {
	if len(v) > 24 {
		return strconv.Itoa(len(v)) + " chars"
	}
	return strconv.Quote(v)
}

// TestJDEFitInputValue_FocusedBoxKeepsTheCaretInView is the fault that made an
// operator type blind: bubbles gives a box NO scrolling window at Width 0, so
// View() draws the whole value and the caret walks off the end of the pane with
// it. Bounding the box gives the window back — and the window is only worth
// having if it is the one the caret is in.
func TestJDEFitInputValue_FocusedBoxKeepsTheCaretInView(t *testing.T) {
	withColorProfile(t, termenv.TrueColor)
	const area = 20
	value := "BEGIN-" + strings.Repeat("x", 60) + "-END"

	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue(value)
	ti.Focus()

	ti.CursorEnd()
	end := jdePlain(jdeCells(jdeFitInputValue(ti, true, area)))
	if !strings.Contains(end, "-END") {
		t.Errorf("with the caret at the end the row shows %q — not the end of the value", end)
	}
	if lipgloss.Width(end) > area {
		t.Errorf("the row is %d columns against a %d-column field: %q", lipgloss.Width(end), area, end)
	}

	ti.SetCursor(0)
	start := jdePlain(jdeCells(jdeFitInputValue(ti, true, area)))
	if !strings.Contains(start, "BEGIN-") {
		t.Errorf("with the caret at the start the row shows %q — not the start of the value", start)
	}
	if lipgloss.Width(start) > area {
		t.Errorf("the row is %d columns against a %d-column field: %q", lipgloss.Width(start), area, start)
	}
	if start == end {
		t.Errorf("the two caret positions render identically (%q) — the box has no window at all, "+
			"which is what Width 0 means in bubbles", start)
	}
}

// ---------------------------------------------------------------------------
// The rule, enforced on the source rather than on a list of screens
// ---------------------------------------------------------------------------

// TestJDEForm_EveryTextRowIsSizedByTheLayer reads the package's own source and
// fails if any sheet draws a typed row itself.
//
// READ THIS BEFORE DELETING IT. This is a LINT, not a behavioural test. It
// executes no screen, renders no frame and cannot fail for a wrong pixel: it
// parses the package into a syntax tree and judges how a row is CONSTRUCTED.
// The project's test-quality rule names exactly that — assertions over source
// text and AST shapes — as an anti-pattern, and the rule is right in general,
// because source inspection is a poor substitute for behaviour: matching syntax
// can be dead, and a behaviour-preserving refactor of how a row is built can
// break this while nothing an operator sees has changed. This test is a
// DELIBERATE, STATED EXCEPTION to that rule, and this comment is the record of
// the exception so the next reader does not remove it as a violation.
//
// It earns the exception because it reaches a property behaviour cannot. Every
// defect on this branch of work has recurred by a fix going exactly as far as
// the list of screens it was handed. Thirty-three files render through this
// layer; a rendering test can only reach the sheets a test can BUILD, and the
// sheets it cannot build are precisely the ones a sweep forgets. "No sheet
// constructs a text row outside the layer" is a claim about all of them at
// once, whether or not anything can drive them, and syntax is the only place
// that claim can be checked.
//
// So it is COMPLEMENTARY to the sheet sweep in jdeTextRowCases, not a duplicate
// of it, and neither half is sufficient alone: that sweep is the BEHAVIOURAL
// half — thirty-six sheets rendered at 80/100/120 through a clipped Root.View()
// with the colour profile forced, asserting what the operator actually sees —
// and this is the REACHABILITY half, which says the sweep's set is the whole
// set. Delete this and a new sheet can quietly build an unbounded row in a file
// no test constructs; delete that and nothing checks a pixel.
//
// It is also how the blast radius of the sizing rule was established rather
// than assumed: the affected set is the set of jdeText rows, this test
// enumerates that set from the syntax tree, and the sweep then drives every
// screen that owns one.
//
// The three ways a row can escape the layer, all of which have existed:
//
//	Value on a jdeText row   — a string the sheet rendered itself, which the
//	                           layer can then only measure, not bound. po_edit's
//	                           void-line prompt was still doing this, straight
//	                           off textinput.View(), on the pilot screen.
//	no Kind at all           — jdeText is the ZERO VALUE of jdeFieldKind, so
//	                           jdeField{Label: "x", Value: "y"} is a text row
//	                           drawing an unbounded string just as surely as one
//	                           that spells Kind out, and an earlier draft of
//	                           this test looked only at a Kind key and missed
//	                           it. A Kind-less literal is therefore judged as
//	                           jdeText — but only when it actually carries a
//	                           Value, because a literal with neither a Value nor
//	                           an Input draws nothing: jdeLabelFields,
//	                           poDetailLabelWidth and the item form's
//	                           labelColumnWidth all build jdeField{Label: l}
//	                           purely so jdeLabelWidth can MEASURE the label,
//	                           and those are not rows. A spelled-out
//	                           Kind: jdeText with neither is still flagged,
//	                           because that one is rendered. The other exemption
//	                           is a row kinded IMPERATIVELY: po_edit's
//	                           lineFields builds three Kind-less literals in a
//	                           []jdeField and sets f.Kind = jdeValue as it
//	                           ranges over them, so the literal's zero value is
//	                           never what gets drawn. A function that assigns a
//	                           non-text Kind is therefore out of this check's
//	                           syntactic reach for its Kind-less literals — the
//	                           narrowest exclusion that admits it, and one that
//	                           costs coverage only inside such a function.
//	jdeInputValue outside    — the unbounded half of the render. It is exported
//	  jde_form.go              to the package only because jdeFitInputValue is
//	                           built on it.
//
// A jdeField literal is found both where it names its type and where the type
// is ELIDED inside a []jdeField or a map of them — []jdeField{{Label: …}} is
// how several sheets build their first row, and a check that only looked at
// named types would have missed the very shape the Kind-less case is about.
func TestJDEForm_EveryTextRowIsSizedByTheLayer(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	// checkFieldLit judges one jdeField literal, however its type was spelled.
	// kinded says the enclosing function assigns a non-text Kind imperatively,
	// which is the one way a Kind-less literal is not the text row its zero
	// value makes it.
	checkFieldLit := func(lit *ast.CompositeLit, kinded bool) {
		kindKey, kind := false, ""
		value, input := false, false
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, _ := kv.Key.(*ast.Ident)
			if key == nil {
				continue
			}
			switch key.Name {
			case "Kind":
				kindKey = true
				if v, ok := kv.Value.(*ast.Ident); ok {
					kind = v.Name
				}
			case "Value":
				value = true
			case "Input":
				input = true
			}
		}
		if kindKey {
			// A Kind that is not the jdeText ident (jdeChoice, jdeValue, or a
			// variable this test cannot resolve) is not this rule's business.
			if kind != "jdeText" || (input && !value) {
				return
			}
		} else if !value || kinded {
			// Zero value, so this IS a text row — unless it draws nothing (a
			// measurement of its own label, not a row) or the function kinds it
			// afterwards.
			return
		}
		t.Errorf("%s: a jdeText row built with %s. A text row hands the layer its "+
			"BOX (Input), so that jdeFitInputValue can bound it to the pane; a row "+
			"that renders its own string is one the layer can only measure",
			fset.Position(lit.Pos()), jdeHowBuilt(value, input))
	}
	// elided reports the jdeField literals inside a []jdeField / map[…]jdeField
	// composite, which carry no type of their own.
	elided := func(lit *ast.CompositeLit, isField, kinded bool) {
		if !isField {
			return
		}
		for _, elt := range lit.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}
			if inner, ok := elt.(*ast.CompositeLit); ok && inner.Type == nil {
				checkFieldLit(inner, kinded)
			}
		}
	}
	isFieldIdent := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == "jdeField"
	}
	// assignsNonTextKind is the imperative-kind exemption above: `f.Kind = X`
	// for any X that is not jdeText means this function decides its rows' kinds
	// after building them, so their zero values say nothing.
	assignsNonTextKind := func(fn *ast.FuncDecl) bool {
		found := false
		ast.Inspect(fn, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range assign.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Kind" || i >= len(assign.Rhs) {
					continue
				}
				if id, ok := assign.Rhs[i].(*ast.Ident); ok && id.Name != "jdeText" {
					found = true
				}
			}
			return true
		})
		return found
	}
	files := 0
	for _, pkg := range pkgs {
		for path, file := range pkg.Files {
			files++
			base := filepath.Base(path)
			// Declaration by declaration, because the imperative-kind exemption
			// is a property of the enclosing function.
			for _, decl := range file.Decls {
				kinded := false
				if fn, ok := decl.(*ast.FuncDecl); ok {
					kinded = assignsNonTextKind(fn)
				}
				ast.Inspect(decl, func(n ast.Node) bool {
					switch node := n.(type) {
					case *ast.CompositeLit:
						switch t := node.Type.(type) {
						case *ast.Ident:
							if t.Name == "jdeField" {
								checkFieldLit(node, false)
							}
						case *ast.ArrayType:
							elided(node, isFieldIdent(t.Elt), kinded)
						case *ast.MapType:
							elided(node, isFieldIdent(t.Value), kinded)
						}
					case *ast.AssignStmt:
						// The other spelling: f.Kind, f.Value = jdeText, …
						text := false
						for _, rhs := range node.Rhs {
							if id, ok := rhs.(*ast.Ident); ok && id.Name == "jdeText" {
								text = true
							}
						}
						if !text {
							return true
						}
						for _, lhs := range node.Lhs {
							if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Value" {
								t.Errorf("%s: a jdeText row assigned a Value. Assign Input instead — the "+
									"layer bounds the box, and cannot bound a string", fset.Position(lhs.Pos()))
							}
						}
					case *ast.CallExpr:
						id, ok := node.Fun.(*ast.Ident)
						if !ok || id.Name != "jdeInputValue" || base == "jde_form.go" {
							return true
						}
						t.Errorf("%s: jdeInputValue is the UNBOUNDED half of the render and belongs to "+
							"jde_form.go. Give the row an Input and let jdeFitInputValue size it",
							fset.Position(node.Pos()))
					}
					return true
				})
			}
		}
	}
	// A parse that found nothing would pass this test silently, which is the
	// failure mode the whole file is written against.
	if files < 30 {
		t.Fatalf("only %d files parsed — this test is meant to read the whole package", files)
	}
}

// jdeHowBuilt names what a mis-built text row did, for the failure message.
func jdeHowBuilt(value, input bool) string {
	if value && input {
		return "both a Value and an Input"
	}
	if value {
		return "a Value"
	}
	return "no Input at all"
}
