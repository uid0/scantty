package tui

import (
	"strings"
	"testing"
)

// countLines mirrors how a bubbletea View() is measured by a terminal: split
// on "\n" and count segments. A trailing "\n" produces a final empty segment
// which counts.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func makeContent(n int) string {
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = "line " + string(rune('a'+i%26))
	}
	return strings.Join(out, "\n")
}

func TestScrollerViewNeverExceedsViewHeight_short(t *testing.T) {
	s := NewTextScroller(10)
	s.Set(makeContent(5)) // 5 lines, view fits everything
	if got := countLines(s.View()); got > 10 {
		t.Fatalf("view exceeded budget: got %d, max 10", got)
	}
	if s.HasOverflow() {
		t.Fatalf("HasOverflow=true on fitting content")
	}
}

func TestScrollerViewNeverExceedsViewHeight_topOfOverflow(t *testing.T) {
	s := NewTextScroller(10)
	s.Set(makeContent(50)) // 5x overflow
	// At offset=0 the "more above" indicator is hidden; only the
	// bottom indicator is needed. Either way the total row count
	// stays inside the budget.
	if got := countLines(s.View()); got > 10 {
		t.Fatalf("view exceeded budget at top of overflow: got %d, max 10", got)
	}
}

func TestScrollerViewNeverExceedsViewHeight_middleOfOverflow(t *testing.T) {
	s := NewTextScroller(10)
	s.Set(makeContent(50))
	s.ScrollDown(15)
	if got := countLines(s.View()); got > 10 {
		t.Fatalf("view exceeded budget in middle of overflow: got %d, max 10", got)
	}
}

func TestScrollerViewNeverExceedsViewHeight_bottomOfOverflow(t *testing.T) {
	s := NewTextScroller(10)
	s.Set(makeContent(50))
	s.Bottom()
	if got := countLines(s.View()); got > 10 {
		t.Fatalf("view exceeded budget at bottom: got %d, max 10", got)
	}
}

func TestScrollerViewNeverExceedsViewHeight_acrossEntireScroll(t *testing.T) {
	// Walk the full range of offsets and assert the budget is never
	// busted, including for short viewports where indicators are
	// likely to dominate.
	for _, viewHeight := range []int{3, 5, 8, 12, 20} {
		for _, lines := range []int{1, 5, viewHeight, viewHeight + 1, 100} {
			s := NewTextScroller(viewHeight)
			s.Set(makeContent(lines))
			for off := 0; off < lines+5; off++ {
				s.offset = off
				s.clamp()
				rows := countLines(s.View())
				if rows > viewHeight {
					t.Fatalf("view=%d lines=%d offset=%d: emitted %d rows (over)", viewHeight, lines, off, rows)
				}
			}
		}
	}
}

func TestScrollerSetViewHeightFallback(t *testing.T) {
	s := NewTextScroller(0)
	if s.viewHeight != defaultDetailHeight {
		t.Fatalf("zero-height ctor should fall back to default")
	}
	s.SetViewHeight(-1)
	if s.viewHeight != defaultDetailHeight {
		t.Fatalf("negative SetViewHeight should fall back to default")
	}
	s.SetViewHeight(7)
	if s.viewHeight != 7 {
		t.Fatalf("positive SetViewHeight should set")
	}
}

func TestScrollerPageStep(t *testing.T) {
	s := NewTextScroller(10)
	if got := s.pageStep(); got != 9 {
		t.Fatalf("pageStep should overlap by 1: got %d, want 9", got)
	}
	s.SetViewHeight(1)
	if got := s.pageStep(); got != 1 {
		t.Fatalf("pageStep should floor at 1 for tiny viewports: got %d", got)
	}
}

func TestScrollerScrollsClampToBounds(t *testing.T) {
	s := NewTextScroller(5)
	s.Set(makeContent(10))
	s.Bottom()
	// Should not be able to scroll past the last visible row.
	maxOffset := s.offset
	s.ScrollDown(100)
	if s.offset != maxOffset {
		t.Fatalf("scrolling past end leaked: offset=%d max=%d", s.offset, maxOffset)
	}
	s.Top()
	s.ScrollUp(100)
	if s.offset != 0 {
		t.Fatalf("scrolling past start leaked: offset=%d", s.offset)
	}
}

func TestScreenBodyHeightFloor(t *testing.T) {
	if got := screenBodyHeight(2); got < 4 {
		t.Fatalf("screenBodyHeight floored too low: got %d", got)
	}
	if got := screenBodyHeight(30); got != 30-statusBarRows-contentVerticalPadding-screenHeaderRows {
		t.Fatalf("screenBodyHeight wrong: got %d", got)
	}
}

func TestScrollerViewHeightAccountsForFooter(t *testing.T) {
	// 30 row terminal → 24 body rows → 22 scroller after the default
	// 2-row detail footer.
	if got := scrollerViewHeight(30, detailFooterRows); got != screenBodyHeight(30)-detailFooterRows {
		t.Fatalf("unexpected scroller view height: %d", got)
	}
	// Action banner adds one more row of overhead.
	plain := scrollerViewHeight(30, detailFooterRows)
	withAction := scrollerViewHeight(30, detailFooterRowsWithAction)
	if withAction != plain-1 {
		t.Fatalf("action banner should cost 1 scroller row: plain=%d action=%d", plain, withAction)
	}
}
