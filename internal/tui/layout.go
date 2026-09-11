package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Chrome accounting for the layout in app.go::Root.View():
//
//     ┌──────────────────────────────────────┐
//     │ nav │   content area (StyleContent)  │  ← height = termHeight - statusBarRows
//     │     │ ┌────────────────────────────┐ │
//     │     │ │ title                      │ │  ← screen.titleRows
//     │     │ │                            │ │  ← screen.blankAfterTitle
//     │     │ │ screen.View()              │ │
//     │     │ └────────────────────────────┘ │
//     └──────────────────────────────────────┘
//     │   status bar (StyleStatusBar)        │  ← statusBarRows
//     └──────────────────────────────────────┘
//
// Counts have to add up exactly to the terminal height or the bottom row of
// the screen body clips off the visible region. We carry the numbers in named
// constants instead of duplicating "5 here, 8 there" math across every detail
// screen.

const (
	// statusBarRows: the status bar renders Padding(0,1) on a 1-line body
	// plus a 1-row top border = 2 rows total. Root.View subtracts this
	// from the terminal height to size the content pane.
	statusBarRows = 2

	// contentVerticalPadding: StyleContent has Padding(1, 2). The vertical
	// padding eats one row at top and one at bottom of every screen.
	contentVerticalPadding = 2

	// screenHeaderRows: every screen renders a title line + a blank
	// separator above its body. Root.View injects both via JoinVertical.
	screenHeaderRows = 2

	// navColumnWidth: the left nav column Root reserves (Root.navWidth is
	// initialised from this), plus the 1-column border Root.View subtracts
	// beside it.
	navColumnWidth  = 24
	navBorderColumn = 1

	// contentHorizontalPadding: StyleContent has Padding(1, 2) — two columns
	// eaten at each side of every screen.
	contentHorizontalPadding = 4
)

// screenBodyWidth returns the columns a screen's View() can render inside the
// content pane without being truncated by clampToBox. The mirror of
// screenBodyHeight: callers pass the raw terminal width and this peels off the
// nav column, its border and the content padding. Falls back to a floor so a
// screen that sizes a grid off it never computes a zero or negative width.
func screenBodyWidth(terminalWidth int) int {
	w := terminalWidth - navColumnWidth - navBorderColumn - contentHorizontalPadding
	if w < 20 {
		return 20
	}
	return w
}

// screenBodyCells is how many COLUMNS a screen's View() really gets inside the
// content pane: the terminal width less the nav column, its border and the
// content padding, and NOTHING ELSE. Zero when the terminal is too narrow to
// give it any.
//
// It is to screenBodyWidth what screenBodyRows is to screenBodyHeight, and it
// exists for the same reason: the floor of 20 below is a LIE at small widths,
// and a caller whose whole job is to keep a value ON the pane cannot budget
// against a lie. Root.View clips to r.width - navColumnWidth - navBorderColumn
// - contentHorizontalPadding, so at the narrowest terminal it draws (45, which
// is where its own contentWidth < 20 gate bites) the pane is 16 cells while
// screenBodyWidth answers 20 — four cells a caller would spend and clampToBox
// would take back, off the RIGHT edge, where a bounded notice keeps the number
// it is asking the operator to act on.
//
// Callers that only need "a sane number to lay a grid out with" should keep
// using screenBodyWidth; callers that must not overrun the pane by a cell use
// this one.
func screenBodyCells(terminalWidth int) int {
	w := terminalWidth - navColumnWidth - navBorderColumn - contentHorizontalPadding
	if w < 0 {
		return 0
	}
	return w
}

// screenBodyRows is how many rows a screen's View() really gets inside the
// content pane: the terminal height less the status bar, the content padding
// and the title+blank header, and NOTHING ELSE. Zero when the terminal is too
// short to give it any.
//
// It is the truthful half of screenBodyHeight, split out because the floor
// below is a LIE at small heights and a caller that has to fit a fixed piece of
// chrome — the columnar layer's action bar — cannot budget against a lie. At a
// terminal height of 9 this answers 3 and screenBodyHeight answers 4, so a
// frame built to the latter runs a row over and clampToBox takes the bar's last
// key line off the bottom: keys that work, unnamed, which is the one thing the
// bar exists to make impossible.
//
// Callers that only need "a sane number to size a viewport with" should keep
// using screenBodyHeight; callers that must EXACTLY FILL the pane use this.
func screenBodyRows(terminalHeight int) int {
	h := terminalHeight - screenChromeRows
	if h < 0 {
		return 0
	}
	return h
}

// screenChromeRows is everything Root.View spends around a screen's body: the
// status bar, the content pane's vertical padding and the title+blank header.
// It is the inverse of screenBodyRows, and it exists so that a screen that has
// to say how tall a terminal it NEEDS can convert its own row count back
// without restating the sum (jdeTooShort).
const screenChromeRows = statusBarRows + contentVerticalPadding + screenHeaderRows

// screenBodyHeight returns the number of rows a screen's View() can render
// inside the content pane without clipping at the bottom. Callers pass the
// raw terminal height; this peels off the status bar, content padding, and
// title+blank header. Falls back to a sane minimum so the layout never
// degenerates to 0 or negative — see screenBodyRows for the unfloored answer
// and for why a screen pinning chrome to the bottom of the pane must use that
// one instead.
func screenBodyHeight(terminalHeight int) int {
	if h := screenBodyRows(terminalHeight); h >= 4 {
		return h
	}
	return 4
}

// scrollerViewHeight returns the rows available to a TextScroller embedded
// in a detail screen. Detail screens render a footer line (the hotkey hint)
// plus an optional action-message banner above it. The scroller's own
// "more above / more below" indicators are accounted for INSIDE the
// scroller — see TextScroller.View — so callers should not subtract for
// them here.
//
//	terminalHeight: full bubbletea WindowSizeMsg.Height
//	footerRows: rows below the scroller (hint + optional action banner +
//	            blank separator).
func scrollerViewHeight(terminalHeight, footerRows int) int {
	h := screenBodyHeight(terminalHeight) - footerRows
	if h < 4 {
		return 4
	}
	return h
}

// actionBarRows is the height of the persistent action bar the columnar
// (JD Edwards) screens pin to the bottom of the content pane: a rule plus ONE
// key line, which is the floor rather than the answer — a bar with more keys
// than a line holds wraps onto further lines, and actionBarRowsFor
// (jde_form.go) is what a screen budgets against. A screen that draws one has this many fewer
// rows for its own body, and has to PAD its body out to that budget, or the
// bar walks up and down the pane as the content changes length.
const actionBarRows = 2

// detailFooterRows is the standard footer height for a detail screen with
// no action message displayed: one blank line + one hint line.
const detailFooterRows = 2

// detailFooterRowsWithAction reflects the footer when an action result
// banner is showing: action banner (1 row) + blank (1 row) + hint (1 row).
const detailFooterRowsWithAction = 3

// clampToBox truncates “content“ so it fits inside “height“ rows and
// “width“ columns of visible output.
//
// Why this exists: screens that don't use TextScroller can render more
// rows than the content box budget. Without clamping, the resulting
// string passed to lipgloss extends past the box and bubbletea draws
// past the terminal bottom, which causes the terminal to scroll the
// frame's top off-screen — that's why the left-side nav "hides" as
// you scroll on a long screen. Clamping at the Root.View boundary
// keeps the frame the size we said it'd be.
//
// Lines are split on "\n"; the first “height“ are kept and the rest
// are dropped (the screen's own j/k or scroller is responsible for
// keeping the operator's focus inside the budget). For each kept line
// we use lipgloss.Width to measure visible width (ANSI-aware) and
// drop any tail bytes that exceed “width“ — naive byte slicing
// would chop an ANSI sequence in half and bleed escape codes into the
// next column.
//
// A TAB is expanded before anything is measured, to exactly what lipgloss
// will draw it as. lipgloss.Width counts a tab as NO cells while the Render
// the pane goes through next draws it as four spaces, so a line with tabs in
// it passed this bound as fitting and was then drawn wider than the pane,
// where lipgloss WRAPS it — the frame grew a row per wrap and the top of it
// went off the terminal, which is the one thing this function exists to stop
// (TestRoot_TheFrameIsNeverTallerThanTheTerminal). Tabs only arrive in data —
// an OMS note or error body — but data is most of what these panes draw.
// A vertical tab or form feed is likewise changed to one space before it is
// measured: a terminal moves its cursor DOWN for both, adding rows that a count
// over "\n" cannot see. They are normalised here, at the one bound every pane
// goes through, with the same mapping jdeStatusOneLine uses.
func clampToBox(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.ReplaceAll(line, "\t", paneTab)
		line = strings.Map(func(r rune) rune {
			if strings.ContainsRune(paneVerticalBreaks, r) {
				return ' '
			}
			return r
		}, line)
		if lipgloss.Width(line) <= width {
			out = append(out, line)
			continue
		}
		out = append(out, truncateVisible(line, width))
	}
	return strings.Join(out, "\n")
}

// paneTab is what a tab is drawn as by a style that sets no tab width, which
// is every style Root renders the pane through. Asked of lipgloss rather than
// written down as four spaces, so a lipgloss that changes its default moves
// this with it.
var paneTab = lipgloss.NewStyle().Render("\t")

const paneVerticalBreaks = "\v\f"

// truncateVisible drops runes from the end until the visible width
// (ANSI-aware, via lipgloss.Width) fits within “width“. Naive byte
// slicing would split an escape sequence and bleed color codes into
// the next column.
func truncateVisible(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes)
}
