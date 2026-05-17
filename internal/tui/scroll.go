package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// TextScroller windows a multi-line string so it fits inside a fixed viewport
// height. Scrolling is independent of terminal size — callers feed it raw
// content via Set() and react to scroll keys via Handle().
type TextScroller struct {
	lines      []string
	offset     int
	viewHeight int
}

const defaultDetailHeight = 24

// detailChromeRows is the number of lines a detail screen renders around
// its scroller (page title, hint footer, scroller indicators, status bar).
// Used to convert a tea.WindowSizeMsg height into a usable viewport.
const detailChromeRows = 8

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
// tea.WindowSizeMsg height (minus surrounding chrome) so the visible
// portion grows to fill the actual terminal. Falls back to the default
// when height <= 0.
func (s *TextScroller) SetViewHeight(height int) {
	if height <= 0 {
		s.viewHeight = defaultDetailHeight
	} else {
		s.viewHeight = height
	}
	s.clamp()
}

func (s *TextScroller) clamp() {
	if s.offset < 0 {
		s.offset = 0
	}
	if max := len(s.lines) - s.viewHeight; max > 0 && s.offset > max {
		s.offset = max
	}
	if len(s.lines) <= s.viewHeight {
		s.offset = 0
	}
}

func (s *TextScroller) ScrollDown(n int) {
	s.offset += n
	s.clamp()
}

func (s *TextScroller) ScrollUp(n int) {
	s.offset -= n
	s.clamp()
}

func (s *TextScroller) Top()    { s.offset = 0 }
func (s *TextScroller) Bottom() { s.offset = len(s.lines); s.clamp() }

// Handle is true when the key was recognized as a scroll command.
func (s *TextScroller) Handle(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "j", "down":
		s.ScrollDown(1)
		return true
	case "k", "up":
		s.ScrollUp(1)
		return true
	case "ctrl+d", "pgdown":
		s.ScrollDown(s.viewHeight)
		return true
	case "ctrl+u", "pgup":
		s.ScrollUp(s.viewHeight)
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

func (s *TextScroller) View() string {
	if len(s.lines) == 0 {
		return ""
	}
	end := s.offset + s.viewHeight
	if end > len(s.lines) {
		end = len(s.lines)
	}
	var b strings.Builder
	if s.offset > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	b.WriteString(strings.Join(s.lines[s.offset:end], "\n"))
	if end < len(s.lines) {
		b.WriteString("\n" + StyleMuted.Render("  ↓ more below"))
	}
	return b.String()
}

func (s *TextScroller) HasOverflow() bool {
	return len(s.lines) > s.viewHeight
}
