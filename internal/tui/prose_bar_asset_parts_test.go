package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// prose_bar_asset_parts_test.go — the fixtures and the checks for the parts list
// on an asset, the last screen on the windowed-cursor-list recipe and the one
// that recipe left behind.
//
// WHAT STOPPED IT, and what each half of the conversion answers. A part is drawn
// as SEVERAL lines — its name, the quantity and replacement facts, and its notes —
// so the window it budgeted in ROWS was not a budget: every part is two lines or
// more, the frame ran past the pane, and what clampToBox took was the footer. The
// window is proseFlatListFrame's now, packed by the lines each part really draws,
// and a part taller than the whole pane is clipped with the cut named
// (TestAssetParts_AnOversizedPartKeepsTheBarAndMarksItsCut).
//
// The half no shared helper answered was the PAGER. It stepped `cursor +=
// windowSize`, a count of rows, and a count of rows stops being a page once the
// window is packed by lines. A page is what the packed window shows now
// (proseLinePage): pgdn lands on the first part the window did not hold whole and
// starts the window there, pgup mirrors it, and both clamp at the ends
// (TestAssetParts_APageIsWhatThePackedWindowShows).
//
// WATCHED FAILING AT HEAD, both, before the screen changed: standing on a part
// whose notes are 45 lines, the frame overran the pane at every size from 80x12
// to 120x40 with `esc back` and the cut mark both off it; and one pgdn at 80x24
// took the cursor from the first part to the fifteenth while the pane held six
// parts, so the highlighted part was nowhere on the pane.

// proseBarAssetPartsList is the parts list as one screen of the windowed recipe,
// so it is swept in that recipe's three states and held to its multi-line check.
//
// Its REFERENCE is two-line names for the reason proseBarTwoLineName gives: a part
// always draws a facts line under its name, so measured against one-line names
// the fit boundary would report proseListWindowFloor rather than the window.
func proseBarAssetPartsList() proseBarWindowedList {
	return proseBarWindowedList{
		name: "asset parts", recv: "AssetPartsScreen",
		build: func(rows int, name proseBarRowName) proseBarScreen {
			return proseBarAssetParts(rows, name, nil)
		},
		reference: proseBarTwoLineName,
		immobile:  "one part, so there is nowhere for the cursor to go",
	}
}

// proseBarAssetPartsFixtures is the parts list in every state its bar changes
// shape in.
func proseBarAssetPartsFixtures() []proseBarFixture {
	const confirm = "a y/n confirm, which binds no movement key"
	long := func() *AssetPartsScreen {
		return proseBarSize(proseBarAssetParts(30, proseBarSameName, nil), 80, 24).(*AssetPartsScreen)
	}
	return append(proseBarListPair(proseBarAssetPartsList()),
		proseBarFixture{
			name: "asset parts/empty", recv: "AssetPartsScreen",
			build: func() proseBarScreen { return proseBarAssetParts(0, proseBarSameName, nil) },
			immobile: "no parts — the point of this fixture: the movement segments and every " +
				"row action must be absent, and `n`, `r` and `esc` named",
		},
		proseBarFixture{
			name: "asset parts/delete confirm", recv: "AssetPartsScreen",
			build:    func() proseBarScreen { return proseBarPress(long(), "x") },
			immobile: confirm,
		},
		proseBarFixture{
			name: "asset parts/mark-replaced confirm", recv: "AssetPartsScreen",
			build:    func() proseBarScreen { return proseBarPress(long(), "R") },
			immobile: confirm,
		},
		// A SERIALIZED part, where `y` opens the serial prompt rather than writing.
		proseBarFixture{
			name: "asset parts/serial prompt", recv: "AssetPartsScreen", typing: proseBarFormBox,
			build: func() proseBarScreen {
				s := long()
				s.rows[s.cursor].PartDetails.IsSerialized = true
				return proseBarPress(s, "R", "y")
			},
			immobile: "the serial box has the focus, so the movement keys go into it",
		},
	)
}

// proseBarAssetParts is an asset holding `n` parts named by `name`. Every third
// part carries a note and every fifth a two-line one, so the list mixes parts of
// two, three and four lines the way a real one does; `notes` overrides a part's
// notes by index. Names differ at the FRONT, for the reason the reorder picker's
// fixture taught: rows told apart only past the clip read as one row.
func proseBarAssetParts(n int, name proseBarRowName, notes map[int]string) *AssetPartsScreen {
	replaced := time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)
	parts := make([]omsapi.AssetPart, n)
	for i := range parts {
		interval, days := 90, 73+i
		parts[i] = omsapi.AssetPart{
			ID: float64(i + 1), Asset: "a-1", Part: fmt.Sprintf("itm-%d", i+1),
			PartName:                name(i, fmt.Sprintf("%02d Spindle drive belt, Gates 5M poly chain", i+1)),
			PartSKU:                 fmt.Sprintf("BLT-5M-%03d", i+1),
			QuantityNeeded:          1 + i%3,
			IsRequired:              i%2 == 0,
			MaintenanceIntervalDays: &interval,
			NeedsReplacement:        i%4 == 0,
		}
		if i%3 != 2 {
			parts[i].LastReplacedAt, parts[i].DaysSinceReplacement = &replaced, &days
		}
		switch {
		case i%5 == 0:
			parts[i].Notes = "Check the tension at the idler first.\nReplace the idler bearing with the belt."
		case i%3 == 0:
			parts[i].Notes = "Check the tension at the idler first."
		}
		if note, ok := notes[i]; ok {
			parts[i].Notes = note
		}
	}
	s := NewAssetPartsScreen(Deps{}, "a-1", "Haas VF-2SS vertical machining centre")
	next, _ := s.Update(assetPartsLoadedMsg{rows: parts})
	return next.(*AssetPartsScreen)
}

// TestProseBarAssetParts_MultiLineNamesFitThePaneAndMarkTheirCut holds the parts
// window with the check every windowed recipe is held to: stored newlines in the
// names, scrolled part-way down, and standing on a name taller than any pane.
func TestProseBarAssetParts_MultiLineNamesFitThePaneAndMarkTheirCut(t *testing.T) {
	proseBarAssertMultiLineWindows(t, []proseBarWindowedList{proseBarAssetPartsList()})
}

// TestAssetParts_AnOversizedPartKeepsTheBarAndMarksItsCut: a part whose NOTES are
// taller than the whole pane, with the cursor standing on it, at every pane Root
// draws.
//
// The notes are the field that makes this a real case rather than a contrived
// one: a part's name is an item name, but its notes are free text an operator
// types into a textarea on the web, and a maintenance procedure pasted there is
// ordinary. Wherever the same list with ordinary notes fits the pane, the list
// standing on the tall part must fit too, carry every segment of its bar whole,
// and say the part was cut — a part cut short with no mark reads as a part whose
// notes end there. The boundary is the ordinary list's own, because below it the
// frame overruns for a reason unrelated to notes (proseListWindowFloor), and both
// sides of it must be reached.
func TestAssetParts_AnOversizedPartKeepsTheBarAndMarksItsCut(t *testing.T) {
	tall := make([]string, proseBarOversizedLines)
	for i := range tall {
		tall[i] = fmt.Sprintf("Step %02d of the spindle belt procedure", i+1)
	}
	fits, overruns := 0, 0
	for _, w := range jdeDrawableWidths() {
		for _, h := range jdePaneHeights() {
			// The reference part carries a one-line note, so it is three lines —
			// proseListWindowFloor's three — and cannot fit by coincidence in the
			// band where that floor makes every frame overrun.
			plain := proseBarPress(proseBarSize(proseBarAssetParts(3, proseBarSameName,
				map[int]string{1: "Check the tension at the idler first."}), w, h), "j")
			if !proseBarFrameFits(plain, h) {
				overruns++
				continue
			}
			fits++
			s := proseBarPress(proseBarSize(proseBarAssetParts(3, proseBarSameName,
				map[int]string{1: strings.Join(tall, "\n")}), w, h), "j")
			if !proseBarFrameFits(s, h) {
				t.Fatalf("at %dx%d standing on a part with %d lines of notes the screen hands over %d "+
					"rows for a %d-row pane — the bar is what clampToBox takes:\n%s",
					w, h, proseBarOversizedLines, lipgloss.Height(s.View()), screenBodyRows(h), stripANSI(s.View()))
			}
			pane := stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
			for _, seg := range s.proseBar() {
				if !strings.Contains(pane, seg.Hint) {
					t.Fatalf("at %dx%d the pane loses bar segment %q:\n%s", w, h, seg.Hint, pane)
				}
			}
			if !strings.Contains(pane, "more lines") {
				t.Fatalf("at %dx%d the tall part was cut with no mark saying so:\n%s", w, h, pane)
			}
		}
	}
	if fits == 0 || overruns == 0 {
		t.Errorf("the ordinary list fitted %d panes and overran %d; a side never reached is a "+
			"check that asserted nothing on it", fits, overruns)
	}
}

// TestAssetParts_APageIsWhatThePackedWindowShows walks pgdn from the top of a
// long parts list to its end and pgup back, at every pane Root draws the frame
// whole at, and asks the PANE where each press should land.
//
// WHAT "A PAGE" IS HERE, read off what the operator sees rather than off the
// arithmetic under test: the parts drawn WHOLE on the pane are the window. pgdn
// lands on the part just below the last of them, and the window then starts on it
// — or, where the end of the list is already in view, on the last part. pgup lands
// on the part just above the first of them — or, where the top of the list is in
// view, on the first part. Either way the part the cursor lands on is on the pane,
// whole and highlighted, and nothing past it is a part the operator has not seen.
//
// The walk starts with the cursor one part down, so a page is measured from a
// cursor in the MIDDLE of its window as well as from its edge: the page follows
// the window, not the cursor.
func TestAssetParts_APageIsWhatThePackedWindowShows(t *testing.T) {
	const parts = 24
	paged, clamped, skipped := 0, 0, 0
	for _, w := range []int{80, 120} {
		for _, h := range jdePaneHeights() {
			s := proseBarPress(proseBarSize(proseBarAssetParts(parts, proseBarSameName, nil), w, h), "j").(*AssetPartsScreen)
			if !proseBarFrameFits(s, h) {
				skipped++
				continue
			}
			for _, down := range []bool{true, false} {
				key := "pgup"
				if down {
					key = "pgdown"
				}
				for step := 0; ; step++ {
					if step > parts+1 {
						t.Fatalf("at %dx%d %s never reached the end of the list", w, h, key)
					}
					whole := assetPartsWholeOnPane(s, w, h)
					if len(whole) == 0 {
						t.Fatalf("at %dx%d no part is drawn whole:\n%s", w, h, assetPartsPane(s, w, h))
					}
					first, last := whole[0], whole[len(whole)-1]
					want, end := last+1, parts-1
					if !down {
						want, end = first-1, 0
					}
					atEnd := (down && last == parts-1) || (!down && first == 0)
					if atEnd {
						want = end
					}
					before := s.cursor
					s = proseBarPress(s, key).(*AssetPartsScreen)
					if s.cursor != want {
						t.Fatalf("at %dx%d with parts %d..%d drawn whole and the cursor on %d, %s put "+
							"the cursor on %d, not %d:\n%s", w, h, first, last, before, key, s.cursor, want,
							assetPartsPane(s, w, h))
					}
					after := assetPartsWholeOnPane(s, w, h)
					if !assetPartsContains(after, want) {
						t.Fatalf("at %dx%d %s put the cursor on part %d and the pane does not draw it whole:\n%s",
							w, h, key, want, assetPartsPane(s, w, h))
					}
					// Past where it landed, a page may draw only parts the operator
					// had already seen whole: above `want` on a pgdn, below it on a
					// pgup. So a page skips nothing and repeats only what fills the
					// pane.
					for _, p := range after {
						if ((down && p < want) || (!down && p > want)) && !assetPartsContains(whole, p) {
							t.Fatalf("at %dx%d %s landed on part %d and drew part %d, which the pane "+
								"before the press did not hold whole — a page that reaches past "+
								"parts nobody has seen:\n%s", w, h, key, want, p, assetPartsPane(s, w, h))
						}
					}
					// pgdn says more than its mirror can. A window fills FORWARD from
					// its start (proseLineWindow), so it STARTS on the part pgdn lands
					// on unless the end of the list is already in view. A window ending
					// on the part pgup lands on is not promised: where the part above
					// that one does not fit beside it, the rows under it are filled
					// rather than left blank, and those are parts already seen.
					if down && after[0] != want && after[len(after)-1] != end {
						t.Fatalf("at %dx%d pgdn landed on part %d and the window neither starts there "+
							"nor reaches the end of the list:\n%s", w, h, want, assetPartsPane(s, w, h))
					}
					if !strings.Contains(assetPartsPane(s, w, h), "▸ "+fmt.Sprintf("%02d ", want+1)) {
						t.Fatalf("at %dx%d the cursor's part %d is not the highlighted one:\n%s",
							w, h, want, assetPartsPane(s, w, h))
					}
					if atEnd {
						clamped++
						if before == end {
							break
						}
						continue
					}
					paged++
				}
			}
		}
	}
	if paged == 0 || clamped == 0 || skipped == 0 {
		t.Errorf("a page past the window was measured %d times, a clamp at an end %d times and a "+
			"pane too short for the frame skipped %d times; a side never reached is a check that "+
			"asserted nothing on it", paged, clamped, skipped)
	}
}

// assetPartsPane is the clipped pane, stripped.
func assetPartsPane(s *AssetPartsScreen, w, h int) string {
	return stripANSI(clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)))
}

// assetPartsWholeOnPane is the parts drawn WHOLE on the clipped pane, in order:
// each part's title line is on it, and the line after its last is the next part's
// title, the `↓ more below` marker or the blank above the bar — so a part the pane
// cut short, or one whose continuation lines ran under something else, is not
// counted. Titles are found by the index every fixture name leads with.
func assetPartsWholeOnPane(s *AssetPartsScreen, w, h int) []int {
	lines := strings.Split(assetPartsPane(s, w, h), "\n")
	title := func(line string, i int) bool {
		return strings.HasPrefix(strings.TrimLeft(line, " ▸"), fmt.Sprintf("%02d Spindle", i+1))
	}
	var out []int
	for i := range s.rows {
		height := strings.Count(s.renderRow(i), "\n") + 1
		for at, line := range lines {
			if !title(line, i) {
				continue
			}
			if next := at + height; next < len(lines) {
				after := strings.TrimSpace(lines[next])
				if after == "" || strings.HasPrefix(after, "↓") || (i+1 < len(s.rows) && title(lines[next], i+1)) {
					out = append(out, i)
				}
			}
			break
		}
	}
	return out
}

func assetPartsContains(parts []int, want int) bool {
	for _, p := range parts {
		if p == want {
			return true
		}
	}
	return false
}
