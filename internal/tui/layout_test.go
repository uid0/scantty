package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestClampToBoxDropsExtraRows(t *testing.T) {
	in := "a\nb\nc\nd\ne"
	got := clampToBox(in, 10, 3)
	want := "a\nb\nc"
	if got != want {
		t.Errorf("expected first 3 rows, got %q", got)
	}
}

func TestClampToBoxLeavesShorterContentAlone(t *testing.T) {
	in := "a\nb"
	got := clampToBox(in, 10, 5)
	if got != in {
		t.Errorf("expected pass-through, got %q", got)
	}
}

func TestClampToBoxZeroDimensionsReturnEmpty(t *testing.T) {
	if got := clampToBox("anything", 0, 5); got != "" {
		t.Errorf("expected empty for width=0, got %q", got)
	}
	if got := clampToBox("anything", 5, 0); got != "" {
		t.Errorf("expected empty for height=0, got %q", got)
	}
}

func TestClampToBoxTruncatesLongLine(t *testing.T) {
	in := strings.Repeat("x", 20)
	got := clampToBox(in, 5, 1)
	if w := lipgloss.Width(got); w != 5 {
		t.Errorf("expected visible width 5, got %d (%q)", w, got)
	}
}

func TestClampToBoxAnsiAware(t *testing.T) {
	// A styled string has SGR escape codes that don't add visible
	// columns. The truncate must measure visible width, not byte
	// length, or we'd chop in the middle of an escape sequence and
	// bleed color codes into the next column.
	styled := lipgloss.NewStyle().Bold(true).Render("hello world")
	// visible width is 11 ("hello world"); truncate to 5 should
	// leave a visible width of 5.
	got := clampToBox(styled, 5, 1)
	if w := lipgloss.Width(got); w != 5 {
		t.Errorf("expected visible width 5 on styled string, got %d (%q)", w, got)
	}
}
