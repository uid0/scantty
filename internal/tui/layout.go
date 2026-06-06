package tui

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
)

// screenBodyHeight returns the number of rows a screen's View() can render
// inside the content pane without clipping at the bottom. Callers pass the
// raw terminal height; this peels off the status bar, content padding, and
// title+blank header. Falls back to a sane minimum so the layout never
// degenerates to 0 or negative.
func screenBodyHeight(terminalHeight int) int {
	h := terminalHeight - statusBarRows - contentVerticalPadding - screenHeaderRows
	if h < 4 {
		return 4
	}
	return h
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

// detailFooterRows is the standard footer height for a detail screen with
// no action message displayed: one blank line + one hint line.
const detailFooterRows = 2

// detailFooterRowsWithAction reflects the footer when an action result
// banner is showing: action banner (1 row) + blank (1 row) + hint (1 row).
const detailFooterRowsWithAction = 3
