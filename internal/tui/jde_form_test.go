package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

// Note on colour: lipgloss renders plain in a test binary (no TTY → the ASCII
// profile), so nothing here can assert on an escape sequence. The focus
// treatment is therefore checked STRUCTURALLY — a focused text field is filled
// with a reverse-video run where an unfocused one is filled with underscores,
// which is exactly the difference an operator sees on a real terminal too.

func TestJDEField_ColumnarLayout(t *testing.T) {
	fields := []jdeField{
		{Label: "Supplier", Kind: jdeText, Value: "Acme Bolt Co.", Width: 20},
		{Label: "Date ordered", Kind: jdeText, Value: "2026-08-02", Width: 12, Hint: "YYYY-MM-DD"},
		{Label: "Priority", Kind: jdeChoice, Value: "Normal"},
	}
	w := jdeLabelWidth(fields)
	if w != len("Date ordered") {
		t.Fatalf("label column = %d, want the widest label (%d)", w, len("Date ordered"))
	}

	lines := renderJDEFields(fields, 0)
	// Every leader starts at the same column: that is what "right-aligned into
	// a common column" has to mean for the block to read as one sheet.
	col := -1
	for i, line := range lines {
		at := strings.Index(line, jdeLeader)
		if at < 0 {
			t.Fatalf("row %d has no dotted leader: %q", i, line)
		}
		if col < 0 {
			col = at
		} else if at != col {
			t.Errorf("row %d puts the leader at column %d, want %d:\n%s", i, at, col, strings.Join(lines, "\n"))
		}
	}
	if !strings.HasPrefix(lines[0], jdeIndent+strings.Repeat(" ", w-len("Supplier"))+"Supplier") {
		t.Errorf("the short label should be right-aligned into the column: %q", lines[0])
	}
	// A text field fills the rest of its width with the underscored run of a
	// green-screen form.
	if !strings.Contains(lines[0], "Acme Bolt Co."+strings.Repeat("_", 20-len("Acme Bolt Co."))) {
		t.Errorf("text field should be underscored out to its width: %q", lines[0])
	}
	// A hint rides after the field, never inside the label column.
	if !strings.HasSuffix(lines[1], "YYYY-MM-DD") || strings.Contains(lines[1][:strings.Index(lines[1], jdeLeader)], "YYYY") {
		t.Errorf("the hint belongs after the input area: %q", lines[1])
	}
	if !strings.Contains(lines[2], "< Normal >") {
		t.Errorf("a choice row should read as < value >: %q", lines[2])
	}
}

// TestJDEField_FocusIsVisibleWithoutMovingAnything is the load-bearing property
// of a form navigated by arrow keys alone: the row the operator is standing in
// has to look different, and the columns must not shift when it does — a form
// whose leaders moved as the cursor walked it would be unreadable.
func TestJDEField_FocusIsVisibleWithoutMovingAnything(t *testing.T) {
	blurred := renderJDEField(jdeField{Label: "Supplier", Kind: jdeText, Value: "Acme", Width: 12}, 12, 0)
	focused := renderJDEField(jdeField{Label: "Supplier", Kind: jdeText, Value: "Acme", Width: 12, Focused: true}, 12, 0)

	if blurred == focused {
		t.Fatal("a focused row must render differently from a blurred one")
	}
	if !strings.Contains(blurred, "_") {
		t.Errorf("a blurred text field should be underscored: %q", blurred)
	}
	if strings.Contains(focused, "_") {
		t.Errorf("a focused text field is a reverse-video run, not underscores: %q", focused)
	}
	if lipgloss.Width(blurred) != lipgloss.Width(focused) {
		t.Errorf("focus must not change the row's width: %d vs %d", lipgloss.Width(blurred), lipgloss.Width(focused))
	}
	if strings.Index(blurred, jdeLeader) != strings.Index(focused, jdeLeader) {
		t.Error("focus must not move the leader column")
	}
}

func TestJDEField_LongLabelCannotWidenTheColumnForever(t *testing.T) {
	long := strings.Repeat("x", jdeLabelMaxWidth+10)
	if w := jdeLabelWidth([]jdeField{{Label: long}}); w != jdeLabelMaxWidth {
		t.Errorf("label column = %d, want it capped at %d", w, jdeLabelMaxWidth)
	}
	line := renderJDEField(jdeField{Label: long, Kind: jdeValue, Value: "v"}, jdeLabelMaxWidth, 0)
	if at := strings.Index(line, jdeLeader); at != len(jdeIndent)+jdeLabelMaxWidth {
		t.Errorf("an over-long label should be truncated into the column, leader at %d: %q", at, line)
	}
}

func TestJDEInputValue_BlurredFieldDropsTheCursorCell(t *testing.T) {
	ti := textinput.New()
	ti.Prompt = ""
	ti.SetValue("SUP-1")

	// A blurred bubbles textinput still draws a cursor cell; in a columnar form
	// that reads as a gap between the value and the underscores after it.
	if got := jdeInputValue(ti, false); got != "SUP-1" {
		t.Errorf("blurred value = %q, want the bare value", got)
	}
	if got := jdeInputValue(ti, true); !strings.HasPrefix(got, "SUP-1") || got == "SUP-1" {
		t.Errorf("a focused field should render its cursor: %q", got)
	}
	// A placeholder is only ever drawn by View(), so an empty field keeps it.
	empty := textinput.New()
	empty.Prompt = ""
	empty.Placeholder = "e.g. 125.00"
	if got := jdeInputValue(empty, false); !strings.Contains(got, "125.00") {
		t.Errorf("an empty field should still show its placeholder: %q", got)
	}
}

// ---------------------------------------------------------------------------
// The windowed body
// ---------------------------------------------------------------------------

// jdeTestLines builds rows lines tall, `owns` lines per navigable row.
func jdeTestLines(rows, owns int) *jdeLines {
	l := &jdeLines{}
	l.Add("heading")
	for r := 0; r < rows; r++ {
		for i := 0; i < owns; i++ {
			l.AddRow(r, "row")
		}
	}
	return l
}

func TestJDELines_WindowIsExactlyTheHeightItWasGiven(t *testing.T) {
	l := jdeTestLines(40, 1)
	for _, cursor := range []int{0, 7, 20, 39} {
		for _, avail := range []int{5, 12, 21} {
			got, _ := l.Window(cursor, avail)
			if len(got) != avail {
				t.Errorf("cursor %d, avail %d: got %d lines, want exactly %d", cursor, avail, len(got), avail)
			}
		}
	}
	// Everything fits: no padding, no indicators, just the lines.
	got, _ := l.Window(0, 500)
	if len(got) != l.Len() {
		t.Errorf("a body that fits should render whole: %d of %d", len(got), l.Len())
	}
}

// TestJDELines_WindowKeepsTheWHOLECursorBlock: a row that owns several lines (a
// choice row and its option strip, a detail line and what it was ordered for)
// must not be sliced in half by the window — the operator would be editing
// something they cannot see.
func TestJDELines_WindowKeepsTheWholeCursorBlock(t *testing.T) {
	l := &jdeLines{}
	l.Add("heading")
	for r := 0; r < 30; r++ {
		l.AddRow(r, "row-"+string(rune('a'+r%26)))
		l.AddRow(r, "strip-"+string(rune('a'+r%26)))
	}
	for _, cursor := range []int{0, 1, 14, 28, 29} {
		got, rows := l.Window(cursor, 10)
		joined := strings.Join(got, "\n")
		want := []string{
			"row-" + string(rune('a'+cursor%26)),
			"strip-" + string(rune('a'+cursor%26)),
		}
		for _, w := range want {
			if !strings.Contains(joined, w) {
				t.Errorf("cursor %d: window lost %q:\n%s", cursor, w, joined)
			}
		}
		if rows < 1 {
			t.Errorf("cursor %d: window reported %d navigable rows", cursor, rows)
		}
	}
}

// TestJDELines_WindowKeepsATallBlockAnchoredOnItsField: centring on a block's
// FIRST line is not enough — a row tall enough to reach past the window (a
// choice row with a long option strip, a detail line with a continuation) would
// have its tail cut off. And when the block cannot fit at all, what has to
// survive is its TOP, which is where the focused field itself is drawn.
func TestJDELines_WindowKeepsATallBlockAnchoredOnItsField(t *testing.T) {
	tall := func(owns int) *jdeLines {
		l := &jdeLines{}
		for r := 0; r < 12; r++ {
			for i := 0; i < owns; i++ {
				l.AddRow(r, fmt.Sprintf("r%d/%d", r, i))
			}
		}
		return l
	}

	// A block that fits the window exactly must be shown whole, tail included.
	got, _ := tall(6).Window(4, 8)
	joined := strings.Join(got, "\n")
	for i := 0; i < 6; i++ {
		if !strings.Contains(joined, fmt.Sprintf("r4/%d", i)) {
			t.Errorf("a block that fits lost line %d of 6:\n%s", i, joined)
		}
	}

	// A block taller than the window keeps its first lines — the field — rather
	// than scrolling to a tail the operator cannot act on.
	got, _ = tall(10).Window(4, 8)
	joined = strings.Join(got, "\n")
	if !strings.Contains(joined, "r4/0") {
		t.Errorf("an over-tall block should stay anchored on its first line:\n%s", joined)
	}
}

func TestJDELines_WindowSaysWhatIsOffScreen(t *testing.T) {
	l := jdeTestLines(40, 1)
	top, _ := l.Window(0, 10)
	if strings.TrimSpace(top[0]) != "" {
		t.Errorf("nothing is above the first row, so no indicator: %q", top[0])
	}
	if !strings.Contains(top[len(top)-1], "more below") {
		t.Errorf("the window should say what is below it: %q", top[len(top)-1])
	}
	bottom, _ := l.Window(39, 10)
	if !strings.Contains(bottom[0], "more above") {
		t.Errorf("the window should say what is above it: %q", bottom[0])
	}
	if strings.TrimSpace(bottom[len(bottom)-1]) != "" {
		t.Errorf("nothing is below the last row: %q", bottom[len(bottom)-1])
	}
}

// ---------------------------------------------------------------------------
// The action bar
// ---------------------------------------------------------------------------

// A bar whose keys fit on one line is still actionBarRows tall — the rule plus
// the key line — which is what layout.go's constant budgets for. The frames all
// draw renderActionBarWrapped now, and this is the case where wrapping changes
// nothing: it wraps only when the keys will not fit.
func TestActionBar_IsTwoRowsAndNamesEveryKey(t *testing.T) {
	out := renderActionBarWrapped(40, []actionBarItem{{"Enter", "Save"}, {"Esc", "Exit"}, {"Ctrl-E", "Edit line"}})
	lines := strings.Split(out, "\n")
	if len(lines) != actionBarRows {
		t.Fatalf("the bar is %d rows, want %d (layout.go budgets for it):\n%s", len(lines), actionBarRows, out)
	}
	if strings.Trim(lines[0], "-") != "" || lipgloss.Width(lines[0]) != 40 {
		t.Errorf("the first row is the rule, drawn to the width given: %q", lines[0])
	}
	for _, want := range []string{"Enter=Save", "Esc=Exit", "Ctrl-E=Edit line"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("bar missing %q:\n%s", want, out)
		}
	}
}

// TestActionBar_TightensRatherThanDroppingAKey: the bar is the only place the
// keys are discoverable, so a narrow terminal squeezes the gutters and then
// takes another ROW rather than quietly losing an entry.
//
// The FIRST key line is what tightens; the keys that will not fit on it go onto
// the next one. Reading line 1 alone was enough while the non-wrapping bar was
// the one the forms drew — it had only one key line, and its answer to a bar
// that would not fit was to run past the pane and let clampToBox cut it, which
// is the defect the wrap removed.
func TestActionBar_TightensRatherThanDroppingAKey(t *testing.T) {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Exit"}, {"UP/DN", "Fields"}, {"Ctrl-E", "Edit line"}, {"PgUp/PgDn", "Page"}}
	wide := actionBarKeyLines(200, items)
	narrow := actionBarKeyLines(60, items)

	if len(wide) != 1 {
		t.Fatalf("200 columns should hold these five keys on one line, got %d:\n%s",
			len(wide), strings.Join(wide, "\n"))
	}
	if lipgloss.Width(narrow[0]) >= lipgloss.Width(wide[0]) {
		t.Errorf("a narrow bar should tighten its gutters:\n%q\n%q", wide[0], narrow[0])
	}
	joined := strings.Join(narrow, "\n")
	for _, it := range items {
		if !strings.Contains(joined, it.Key+"="+it.Label) {
			t.Errorf("the narrow bar dropped %q:\n%s", it.Key, joined)
		}
	}
	for _, line := range narrow {
		if got := lipgloss.Width(line); got > 60 {
			t.Errorf("the narrow bar row %q is %d cells wide in 60 — it ran past the pane "+
				"instead of taking another row", line, got)
		}
	}
}
