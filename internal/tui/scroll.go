package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// TextScroller windows a multi-line string so it fits inside a fixed viewport
// height. The total rows emitted by View() are guaranteed not to exceed
// viewHeight — when scroll indicators are needed they're rendered IN PLACE of
// content rows, never additively. This lets callers budget the scroller
// against the layout once at WindowSizeMsg time without worrying about
// indicator-driven bottom clipping.
type TextScroller struct {
	lines      []string
	offset     int
	viewHeight int
}

const defaultDetailHeight = 24

// indicatorAbove and indicatorBelow are the literal lines we substitute in
// place of content when the scroller has overflow. Both render at 1 row
// each.
var (
	indicatorAbove = StyleMuted.Render("  ↑ more above")
	indicatorBelow = StyleMuted.Render("  ↓ more below")
)

func NewTextScroller(height int) *TextScroller {
	if height <= 0 {
		height = defaultDetailHeight
	}
	return &TextScroller{viewHeight: height}
}

func (s *TextScroller) Set(content string) {
	s.lines = strings.Split(content, "\n")
	s.clamp()
}

// SetViewHeight resizes the scroller's viewport. Callers should forward
// the screen-body height minus surrounding chrome so the visible portion
// grows to fill the actual terminal. Falls back to the default when
// height <= 0.
func (s *TextScroller) SetViewHeight(height int) {
	if height <= 0 {
		s.viewHeight = defaultDetailHeight
	} else {
		s.viewHeight = height
	}
	s.clamp()
}

func (s *TextScroller) clamp() {
	contentBudget := s.contentBudget()
	if s.offset < 0 {
		s.offset = 0
	}
	if max := len(s.lines) - contentBudget; max > 0 && s.offset > max {
		s.offset = max
	}
	if len(s.lines) <= contentBudget {
		s.offset = 0
	}
}

// contentBudget returns how many content lines the View() call will emit,
// reserving rows for indicators when overflow is present at either edge.
// Always >= 1 so a single content row stays visible.
func (s *TextScroller) contentBudget() int {
	budget := s.viewHeight
	if s.offset > 0 {
		budget--
	}
	// Conservative end-of-content check: if total lines exceed the
	// viewHeight at all, we'll eventually need the bottom indicator. The
	// per-call adjustment in View() may reclaim a row when the bottom
	// indicator isn't actually needed at the current offset.
	if len(s.lines) > s.viewHeight {
		budget--
	}
	if budget < 1 {
		budget = 1
	}
	return budget
}

func (s *TextScroller) ScrollDown(n int) {
	s.offset += n
	s.clamp()
}

func (s *TextScroller) ScrollUp(n int) {
	s.offset -= n
	s.clamp()
}

func (s *TextScroller) Top() { s.offset = 0; s.clamp() }
func (s *TextScroller) Bottom() {
	s.offset = len(s.lines)
	s.clamp()
}

// Handle is true when the key was recognized as a scroll command.
func (s *TextScroller) Handle(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "j", "down":
		s.ScrollDown(1)
		return true
	case "k", "up":
		s.ScrollUp(1)
		return true
	case "pgdown":
		s.ScrollDown(s.pageStep())
		return true
	case "pgup":
		s.ScrollUp(s.pageStep())
		return true
	case "g", "home":
		s.Top()
		return true
	case "G", "end":
		s.Bottom()
		return true
	}
	return false
}

// pageStep is one "screen of content" minus 1 line, so paging keeps one
// row of overlap for the reader's eye. Floors at 1 to avoid infinite loops
// at tiny viewports.
func (s *TextScroller) pageStep() int {
	step := s.viewHeight - 1
	if step < 1 {
		step = 1
	}
	return step
}

func (s *TextScroller) View() string {
	if len(s.lines) == 0 {
		return ""
	}
	// Compute the indicators we actually need at this offset.
	showAbove := s.offset > 0

	// Recompute the precise content slot for this render now that we
	// know exactly which indicators show.
	budget := s.viewHeight
	if showAbove {
		budget--
	}
	end := s.offset + budget
	showBelow := false
	if end < len(s.lines) {
		// Bottom indicator will be needed — take one more content row
		// back to make room for it.
		budget--
		end = s.offset + budget
		showBelow = true
	}
	if budget < 1 {
		budget = 1
		end = s.offset + 1
	}
	if end > len(s.lines) {
		end = len(s.lines)
	}

	var b strings.Builder
	if showAbove {
		b.WriteString(indicatorAbove)
		b.WriteString("\n")
	}
	b.WriteString(strings.Join(s.lines[s.offset:end], "\n"))
	if showBelow {
		b.WriteString("\n")
		b.WriteString(indicatorBelow)
	}
	return b.String()
}

func (s *TextScroller) HasOverflow() bool {
	return len(s.lines) > s.viewHeight
}
