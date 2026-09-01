package tui

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// list_bar_honesty_test.go — the bar-honesty rule for the LIST screens.
//
// The rule this project already holds itself to (AGENTS.md) is:
//
//	a key the bar NAMES must do something, and
//	a key the bar does NOT name must do nothing.
//
// TestPOView_BarNamesExactlyTheKeysThatWork sweeps it over the purchase-order
// DETAIL and ATTACHMENTS screens. It did not catch `N` on the purchase-order
// LIST doing nothing, and it never could have, for three independent reasons —
// each of which is a hole this file closes:
//
//  1. The list screens are not in its phase set. poBarPhases() enumerates the
//     detail sheet, its modals, the order pad and the attachments grid. The
//     Purchasing LIST — the screen an operator lands on first, and the one the
//     report came from — was simply never swept.
//
//  2. It reads a []actionBarItem. The list screens are not on the columnar
//     layer; their bar is a hand-built STRING (ListScreen.footerHint). A sweep
//     keyed on the structured bar cannot see a claim made in prose, so adding
//     the list to its phase set would not have helped either. This file parses
//     the string instead.
//
//  3. Its vocabulary is a hardcoded list of the detail screen's keys
//     (poAllBarKeys). N, Q, I, C, L, U, M and A are not in it, and a key that
//     is not in the vocabulary is never pressed in either direction.
//
// So the answer to "was the key outside the swept set, or claimed while its
// action silently no-ops?" is BOTH, and neither alone would have been enough.
// The key was claimed by a literal appended to a hint string, pointing at a
// global letter accelerator in app.go that phase 3 of the redesign had already
// deleted; the handler never knew the claim existed. Eight keys were in that
// state across four lists, not one. listShortcuts (list.go) now makes the claim
// and the handler one record, and this file walks that record.

// listBarKeyNames maps a footer token to the keystrokes it claims, and every
// entry is a LITERAL transcription of the token: it names the keys it spells
// and no synonyms.
//
// It used to credit "j/k" with the arrows, "pgup/pgdn" with ctrl+u/ctrl+d and
// "g/G" with home/end, on the reasoning that a synonym costs cells in a
// 51-column footer. That is the sweep handing the bar a claim the bar never
// made — the very thing it reports — and it hides the next key bound behind
// one of those arms. The footer names the arrows and home/end itself now
// ("j/k ↑↓ move", "g/G home/end top/bottom"), and the two paging chords went
// the way the supplier picker's `tab` alias did: unbound, because a chord in a
// bar this narrow costs more than it is worth.
var listBarKeyNames = map[string][]string{
	"j/k":       {"j", "k"},
	"↑↓":        {"up", "down"},
	"pgup/pgdn": {"pgup", "pgdown"},
	"g/G":       {"g", "G"},
	"home/end":  {"home", "end"},
	"s":         {"s"},
	"f":         {"f"},
	"r":         {"r"},
	"enter":     {"enter"},
	"/":         {"/"},
	"n":         {"n"},
	"N":         {"N"},
	"Q":         {"Q"},
	"I":         {"I"},
	"C":         {"C"},
	"L":         {"L"},
	"U":         {"U"},
	"M":         {"M"},
	"A":         {"A"},
}

// listBarAliasKeys is the tokens a footer segment carries AFTER its head to
// name a second key that acts exactly as the head's does — "j/k ↑↓ move". Only
// tokens that SPELL the keys they map to belong here: the whole point is that
// the bar says the key, so the sweep may credit it.
//
// It lived in po_create_picker_status_test.go as poBarAliasKeys until the New
// PO conversion retired that file's prose-bar parser — the columnar bar is
// []actionBarItem, so there is no prose left to alias — leaving this sweep, the
// last one over a footer STRING, as its only user.
var listBarAliasKeys = map[string][]string{
	"↑↓":       {"up", "down"},
	"home/end": {"home", "end"},
}

// listKeySpace is every keystroke the sweep presses: printable ASCII, then the
// named keys a terminal sends that are not runes. It is the KEY SPACE, not a
// vocabulary, and that is the whole point — the roster it replaced was a
// hand-written token list (the same shape as poAllBarKeys and
// poPickerVocabulary), safe only in the FORWARD direction, because
// listNamedKeys fails on a footer token it does not know. In REVERSE it was
// blind: a key bound in ListScreen.Update and absent from the roster was
// pressed in neither direction, which is verbatim how `N` survived a sweep
// written to catch exactly it.
//
// Nothing here is curated, so a key bound tomorrow is pressed by this list
// today. Mirrors poKeySpace (po_create_phase_sweep_test.go); the two are
// separate because the halves of the app they walk read their bars differently
// — a []actionBarItem, a picker bar, and this one a footer STRING.
func listKeySpace() []string {
	var out []string
	for c := byte(0x20); c <= 0x7e; c++ {
		out = append(out, string(rune(c)))
	}
	return append(out,
		"enter", "esc", "tab", "shift+tab",
		"up", "down", "left", "right", "home", "end", "pgup", "pgdown",
		"backspace", "delete",
		"ctrl+u", "ctrl+d", "ctrl+e", "ctrl+x", "ctrl+p", "ctrl+n",
	)
}

// listNamedKeys parses a footer hint into the set of keystrokes it claims.
// Every token must be known: an unrecognised one is a bar entry the sweep would
// otherwise skip in silence, which is precisely how `N` survived.
func listNamedKeys(t *testing.T, hint string) map[string]bool {
	t.Helper()
	named := map[string]bool{}
	for _, part := range strings.Split(hint, " · ") {
		token := strings.Fields(strings.TrimSpace(part))
		if len(token) == 0 {
			continue
		}
		keys, ok := listBarKeyNames[token[0]]
		if !ok {
			t.Fatalf("footer token %q is not in listBarKeyNames — add it so the rule covers it (hint: %q)", token[0], hint)
		}
		for _, k := range keys {
			named[k] = true
		}
		// A segment can name a second key beside its head ("j/k ↑↓ move"), and
		// only a token that SPELLS its keys may be read that way.
		for _, f := range token[1:] {
			for _, k := range listBarAliasKeys[f] {
				named[k] = true
			}
		}
	}
	return named
}

// listBarSurface is one list screen the app can build.
type listBarSurface struct {
	name  string
	build func() *ListScreen
}

// listBarSurfaces is EVERY ListScreen in the app, DERIVED rather than listed.
// It walks Workspaces() — the nav tree's own authority, which route.go and the
// sidebar already read — and takes every workspace whose newScreenFor yields a
// *ListScreen. A list added to a workspace joins this sweep by existing.
//
// The Purchasing list was outside the previous sweep, which is the whole reason
// eight advertised keys reached an operator's terminal doing nothing; a
// hand-kept roster of constructors would have been the same omission one
// workspace over. Screens reachable OUTSIDE the nav tree cannot be derived from
// it, so they are named in listBarSurfacesOffTree with the reason — absent and
// deliberately-not-a-workspace are then different states, not the same silence.
func listBarSurfaces() []listBarSurface {
	var out []listBarSurface
	for _, ws := range Workspaces() {
		screen := newScreenFor(ws.Key, Deps{})
		if _, ok := screen.(*ListScreen); !ok {
			continue
		}
		id, label := ws.Key, ws.Label
		out = append(out, listBarSurface{
			name:  strings.ToLower(label),
			build: func() *ListScreen { return newScreenFor(id, Deps{}).(*ListScreen) },
		})
	}
	return append(out, listBarSurfacesOffTree()...)
}

// listBarSurfacesOffTree is every ListScreen the nav tree cannot reach, so
// Workspaces() cannot name it. Each line states why it is here rather than
// derived; anything that can be reached from a workspace must NOT be listed
// here, because that would take it back out of the derivation.
func listBarSurfacesOffTree() []listBarSurface {
	return []listBarSurface{
		// Reached from a project's detail screen, not from the sidebar.
		{"project storage", func() *ListScreen { return NewProjectStorageListScreen(Deps{}) }},
	}
}

// TestList_EveryWorkspaceListIsSwept is the completeness half: the derivation
// above only helps if it actually reaches every workspace list, so this fails
// when one is missing rather than letting the sweep quietly cover five of six.
func TestList_EveryWorkspaceListIsSwept(t *testing.T) {
	swept := map[string]bool{}
	for _, s := range listBarSurfaces() {
		swept[s.name] = true
	}
	for _, ws := range Workspaces() {
		if _, ok := newScreenFor(ws.Key, Deps{}).(*ListScreen); !ok {
			continue
		}
		if !swept[strings.ToLower(ws.Label)] {
			t.Errorf("workspace %q builds a *ListScreen but the bar-honesty sweep does not cover it",
				ws.Label)
		}
	}
	if len(swept) < 2 {
		t.Fatalf("the sweep found %d list screen(s) — the derivation is broken, not the app", len(swept))
	}
}

// listFixtureRows builds rows the shape the real loaders build them, which is
// the shape the pane has to survive: a row renders its title, plus a line for
// a Subtitle and another for a MetricsLine. The loaders do not agree on which
// — purchaseOrderRows sets a Subtitle for any PO carrying a supplier name or a
// total and leaves it empty otherwise, loadInventoryItems sets a MetricsLine
// when the item has metrics and a Subtitle when it does not — so a real list
// is MIXED, and the cheap rows come wherever the data puts them.
//
// The order here is deliberate: plain rows FIRST, taller rows after. A window
// sized once over the cheap rows and kept through the scroll is what put more
// lines into the pane than it has, and the fixture these sweeps used to carry
// (listRow{ID, Title} and nothing else) rendered one line per row, so the
// arithmetic closed on a body half the height of the real one and every
// legibility assertion below passed over it.
func listFixtureRows(n int) []listRow {
	rows := make([]listRow, 0, n)
	for i := 0; i < n; i++ {
		row := listRow{ID: fmt.Sprint(i + 1), Title: fmt.Sprintf("Row %d", i+1)}
		switch {
		case i < n/3:
			// A PO with neither supplier name nor total: title only.
		case i%2 == 0:
			row.Subtitle = fmt.Sprintf("Acme Supply Company · $%d,234.56", i)
		default:
			row.MetricsLine = StyleTitle.Render("On hand: ") + "12  " +
				StyleTitle.Render("Reorder: ") + "4"
		}
		rows = append(rows, row)
	}
	return rows
}

// listLoaded is the state every named key is meaningful in: enough rows that
// the cursor can move in both directions.
func listLoaded(build func() *ListScreen) *ListScreen {
	return listWithRows(build, 8)
}

// listWithRows is listLoaded at a chosen row count, which is the axis the sweep
// was blind on.
//
// AN EMPTY LIST IS A STATE, NOT AN EXCEPTION, and it is the state a list spends
// much of its life in — a fresh install, a filter that matched nothing, a load
// that has not answered. Every fixture in this file used to carry eight rows, so
// the footer's movement claims were only ever pressed where they were true:
// "j/k ↑↓ move · pgup/pgdn page · g/G home/end top/bottom" was an unconditional
// literal, and on an empty list all three segments named keys that clamped onto
// the row the cursor was already on. Worse for `pgdown`, which ran
// `s.cursor = len(s.rows) - 1` and left the cursor at -1.
//
// ONE row as well as none, because the two are different arithmetic and only one
// of them was obviously wrong: with a single row `j` fails its `cursor < len-1`
// guard while `G` assigns the index it is already on, so the second is a write
// that changes nothing rather than a branch not taken. A fixture with none would
// pass over a bar that had been fixed for the empty case alone.
func listWithRows(build func() *ListScreen, rows int) *ListScreen {
	s := build()
	s.loading = false
	s.windowSize = 3
	s.rows = listFixtureRows(rows)
	return s
}

// listRowCases are the row counts every list surface is swept at, with what each
// one is FOR — so a count is not quietly dropped as redundant.
var listRowCases = map[string]int{
	"empty":     0,
	"one row":   1,
	"many rows": 8,
}

// listKeyEffect presses one key from a fresh screen (optionally after a probe
// run) and reports whether it changed anything or issued a command.
func listKeyEffect(build func() *ListScreen, probe []string, key string) (changed, issued bool) {
	return listKeyEffectAt(build, 8, probe, key)
}

// listKeyEffectAt is listKeyEffect at a chosen row count.
//
// `changed` is measured on the CLIPPED PANE and not on a state fingerprint, and
// that is the difference between standing rule 1 and a weaker cousin of it: the
// rule is that a keypress produces a distinguishable operator-VISIBLE change,
// and a fingerprint over the screen's fields reports a key that moved a number
// nothing draws as working. It was not hypothetical — with the footer restored
// on an empty list, `s sort` was named while sorting nothing redrew a
// byte-identical pane, and the fingerprint (which carries s.sort) passed it.
// The screen is SIZED first so the pane is the one a terminal really gives.
func listKeyEffectAt(build func() *ListScreen, rows int, probe []string, key string) (changed, issued bool) {
	s := listWithRows(build, rows)
	if next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: 24}); next != nil {
		s = next.(*ListScreen)
	}
	press := func(k string) tea.Cmd {
		next, cmd := s.Update(listRuneKey(k))
		s = next.(*ListScreen)
		return cmd
	}
	for _, p := range probe {
		press(p)
	}
	pane := func() string {
		return clampToBox(s.View(), screenBodyWidth(80), screenBodyHeight(24))
	}
	before := pane()
	cmd := press(key)
	return pane() != before, cmd != nil
}

// listBarState is the fingerprint the rule reads. It deliberately spans every
// field a list key can move — a fingerprint that missed one would report a
// working key as dead.
func listBarState(s *ListScreen) string {
	return fmt.Sprint(s.cursor, s.windowStart, s.sort, s.filter, s.searching, s.loading, s.searchQuery)
}

func listRuneKey(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "ctrl+p":
		return tea.KeyMsg{Type: tea.KeyCtrlP}
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// listPaneLines is the list body as the CONTENT PANE actually shows it: the
// screen's own View clipped to screenBodyWidth(80), which is what Root.View()
// does to it before painting (app.go clamps the joined content, and clampToBox
// truncates rather than wraps).
//
// Reading the clipped pane rather than footerHint() is the point. The claim and
// the handler were made one record last round, which fixed the keys — but a
// claim the operator cannot see is not a claim, and asserting the method's
// return value is exactly the blindness that let it ship: every list's footer
// was cut at 51 columns with all eight restored keys past the cut.
func listPaneLines(t *testing.T, s *ListScreen, termHeight int) []string {
	t.Helper()
	return strings.Split(
		clampToBox(s.View(), screenBodyWidth(80), screenBodyHeight(termHeight)), "\n")
}

// listFooterLegible reports whether one footer segment ("N new PO") survives
// the clip whole, on a single visible line of a REAL pane.
//
// The height matters as much as the width and for the same reason. The first
// pass at this helper clamped at 400 rows, which no terminal has, so folding
// the footer onto three lines looked fine here while clampToBox was dropping
// the third one — the same claim, cut off a different edge.
func listFooterLegible(t *testing.T, s *ListScreen, termHeight int, segment string) bool {
	t.Helper()
	for _, line := range listPaneLines(t, s, termHeight) {
		if strings.Contains(line, segment) {
			return true
		}
	}
	return false
}

// listSized builds a list the way the runtime does: sized through a
// WindowSizeMsg so scrollIntoView re-derives the window (rowsFittingFrom packs
// by rendered LINES), and filled with enough rows to overflow it.
func listSized(t *testing.T, build func() *ListScreen, termHeight int) *ListScreen {
	t.Helper()
	s := build()
	s.loading = false
	s.rows = listFixtureRows(60)
	next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: termHeight})
	return next.(*ListScreen)
}

// TestList_FooterNamesExactlyTheKeysThatWork is the rule, as a rule, over every
// list screen in the app, pressed over the whole key space.
//
// The key space rather than a roster is the point: this is the sweep that
// catches `N` (revert the listShortcuts handler and it reports all eight dead
// claims across four lists), and until now it could only catch a key that was
// already in listAllBarKeys. That roster was safe forwards — listNamedKeys
// still fails on a footer token it does not know, so a NAMED key cannot be
// skipped — and blind backwards: a key bound in ListScreen.Update and absent
// from it was pressed in neither direction, which is exactly how `N` survived
// the sweep written to catch it.
//
// Each key is probed from several positions — as opened, after G, after
// paging down — because `g` does nothing at the top and `G` nothing at the
// bottom; a key is dead only if it does nothing from any of them.
//
// It also requires each claim to be READABLE at 80 columns. A key named on a
// line the pane cuts off is dead to the operator whatever the handler does, so
// it fails this sweep rather than passing it.
func TestList_FooterNamesExactlyTheKeysThatWork(t *testing.T) {
	probes := [][]string{nil, {"G"}, {"pgdown"}}
	heights := jdePaneHeights()

	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			// Legibility on a REAL pane, at EVERY height Root will draw one at
			// and in both scroll positions. A footer claim that survives the
			// 51-column cut only to be dropped off the bottom by clampToBox is
			// just as unread.
			//
			// jdePaneHeights and not the pair {24, 30} this loop used to walk.
			// Two hand-picked heights is the same mistake on the vertical axis
			// that three hand-picked widths was on the horizontal one, and it
			// cost the same thing: every height at which the folded footer ran
			// past the bottom of the pane was below both of them, so nothing
			// reported a bar that vanished on exactly the panes an operator
			// running a split terminal has. The set is derived from Root's own
			// drawable gate, so it moves when that gate moves.
			//
			// A pane the screen REFUSES is not a claim about the footer and is
			// checked by TestList_AShortPaneRefusesRatherThanCuttingTheFooter,
			// which is where the refusal's own honesty is asserted.
			for _, termHeight := range heights {
				for _, scrolled := range []bool{false, true} {
					sized := listSized(t, surface.build, termHeight)
					if scrolled {
						next, _ := sized.Update(listRuneKey("G"))
						sized = next.(*ListScreen)
					}
					if !sized.paneDrawn() {
						continue
					}
					for _, segment := range strings.Split(sized.footerHint(), " · ") {
						if !listFooterLegible(t, sized, termHeight, segment) {
							t.Errorf("the %s footer claims %q on a line the pane cuts off (height %d, scrolled %v):\n%s",
								surface.name, segment, termHeight, scrolled,
								strings.Join(listPaneLines(t, sized, termHeight), "\n"))
						}
					}
				}
			}

			// And at every ROW COUNT, because the footer's shape depends on one
			// now: the movement segments come off it below two rows and `enter
			// open` below one, so the string that has to survive the clip is a
			// different string in each state. Proven rather than reasoned about —
			// "these bars only got shorter" is exactly the kind of claim that is
			// true until the next segment is added behind the condition.
			for state, rows := range listRowCases {
				for _, termHeight := range heights {
					sized := listWithRows(surface.build, rows)
					next, _ := sized.Update(tea.WindowSizeMsg{Width: 80, Height: termHeight})
					sized = next.(*ListScreen)
					if !sized.paneDrawn() {
						continue
					}
					for _, segment := range strings.Split(sized.footerHint(), " · ") {
						if !listFooterLegible(t, sized, termHeight, segment) {
							t.Errorf("the %s footer claims %q on a line the pane cuts off "+
								"(%s, height %d):\n%s", surface.name, segment, state, termHeight,
								strings.Join(listPaneLines(t, sized, termHeight), "\n"))
						}
					}
				}
			}

			// EVERY ROW COUNT, not just the loaded one. The footer's movement
			// segments are conditional on there being a second row to move to
			// (listNavHint), so the state a claim can be false in is exactly the
			// state no fixture here used to reach.
			for state, rows := range listRowCases {
				sized := listWithRows(surface.build, rows)
				namedAt := listNamedKeys(t, sized.footerHint())
				for _, key := range listKeySpace() {
					var changed, issued bool
					for _, probe := range probes {
						c, i := listKeyEffectAt(surface.build, rows, probe, key)
						changed = changed || c
						issued = issued || i
					}
					switch {
					case namedAt[key] && !changed && !issued:
						t.Errorf("the %s footer names %q with %s but pressing it does nothing:\n%s",
							surface.name, key, state, sized.footerHint())
					case !namedAt[key] && (changed || issued):
						t.Errorf("the %s footer does not name %q with %s, but pressing it acts:\n%s",
							surface.name, key, state, sized.footerHint())
					}
				}
			}
		})
	}
}

// TestList_AShortPaneRefusesRatherThanCuttingTheFooter is the OTHER half of the
// sweep above: what a list does at the heights where its folded footer will not
// fit at all.
//
// THE DEFECT. The footer is pinned to the bottom of the pane and clampToBox
// drops from the bottom, so a pane that cannot hold the whole assembly loses
// the bar — some keys named, the rest hidden, and nothing on the pane to say a
// fragment is what the operator is reading. Both branches did it, for the same
// reason: they budgeted against screenBodyHeight, which floors at four and is
// therefore a lie below a terminal height of ten, and listBodyLines then floored
// its own answer at two rows the pane did not have. On the purchase-order list
// at 80 columns the second folded footer row ("· N new PO · Q pending
// reorders") went at height 11 empty and 14 loaded, and by height 10 the whole
// bar was gone — the bar-less pane the empty-list work exists to remove,
// restored by geometry.
//
// THE PROPERTY, at every height Root will draw a list at and every row count:
// either the footer is on the pane WHOLE, or the screen has refused the pane and
// says so — bounded in both axes, naming a height in TERMINAL rows, and naming a
// height that ACTUALLY DRAWS when the operator resizes to it. A refusal the
// operator cannot act on is its own defect (standing rule 11), and a notice that
// names a height still too short is exactly that.
//
// The count of refusals is asserted rather than assumed: if no case in the whole
// sweep reaches the refusal, this test is only checking legibility that the
// sweep above already checks, and the notice path is untested while looking
// covered.
func TestList_AShortPaneRefusesRatherThanCuttingTheFooter(t *testing.T) {
	heights := jdePaneHeights()
	if len(heights) == 0 {
		t.Fatal("no drawable heights — the derivation is broken, not the app")
	}
	refusals := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for state, rows := range listRowCases {
				for _, termHeight := range heights {
					s := listWithRows(surface.build, rows)
					next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: termHeight})
					s = next.(*ListScreen)
					pane := listPaneLines(t, s, termHeight)

					if s.paneDrawn() {
						for _, segment := range strings.Split(s.footerHint(), " · ") {
							if !listFooterLegible(t, s, termHeight, segment) {
								t.Errorf("the %s pane is drawn at height %d (%s) but its footer "+
									"claims %q on a line the pane cuts off:\n%s",
									surface.name, termHeight, state, segment, strings.Join(pane, "\n"))
							}
						}
						continue
					}
					refusals++

					// The notice, and NOTHING of the bar: a refused pane that still
					// drew a footer fragment would be the defect wearing the fix.
					need := s.needRows() + screenChromeRows
					want := fmt.Sprintf("needs %d rows", need)
					if !listFooterLegible(t, s, termHeight, want) {
						t.Errorf("the %s pane at height %d (%s) draws no footer and does not say "+
							"why — the operator is left on a bar-less pane:\n%s",
							surface.name, termHeight, state, strings.Join(pane, "\n"))
					}

					// Bounded in BOTH axes by the screen, not by clampToBox: a notice
					// that was itself cut would be the defect it exists to report.
					raw := strings.Split(s.View(), "\n")
					if len(raw) > s.listPaneRows() {
						t.Errorf("the %s refusal at height %d (%s) is %d rows into a pane of %d",
							surface.name, termHeight, state, len(raw), s.listPaneRows())
					}
					for _, line := range raw {
						if lipgloss.Width(line) > screenBodyCells(80) {
							t.Errorf("the %s refusal at height %d (%s) draws %d cells into a pane of %d: %q",
								surface.name, termHeight, state, lipgloss.Width(line),
								screenBodyCells(80), line)
						}
					}

					// THE HEIGHT IT NAMES HAS TO WORK. Resized to it, the same list
					// draws its frame and its whole footer — otherwise the notice is a
					// refusal the operator cannot satisfy.
					at := listWithRows(surface.build, rows)
					n2, _ := at.Update(tea.WindowSizeMsg{Width: 80, Height: need})
					at = n2.(*ListScreen)
					if !at.paneDrawn() {
						t.Errorf("the %s refusal at height %d (%s) names %d rows, but the pane is "+
							"still refused there", surface.name, termHeight, state, need)
						continue
					}
					for _, segment := range strings.Split(at.footerHint(), " · ") {
						if !listFooterLegible(t, at, need, segment) {
							t.Errorf("the %s refusal at height %d (%s) names %d rows, but at that "+
								"height the footer still claims %q on a line the pane cuts off:\n%s",
								surface.name, termHeight, state, need, segment,
								strings.Join(listPaneLines(t, at, need), "\n"))
						}
					}
				}
			}
		})
	}
	if refusals == 0 {
		t.Fatal("no list refused a pane at any drawable height, so the refusal half of " +
			"this check asserted nothing — either the fixtures no longer reach a short " +
			"pane or the gate has stopped answering")
	}
}

// TestList_ARefusedPaneKeepsTheOperatorsPlace: while the pane is too short to
// draw the footer, the movement keys are HELD.
//
// The notice promises it in as many words, and a notice claiming a hold that is
// not applied is a documented claim the code does not honour — the same defect
// in the other direction as a bar naming a dead key. It is also the loss itself:
// `end` on a refused pane would walk the cursor to the bottom of a list nobody
// can see, and the operator who drags the terminal back finds somewhere they
// never went. paneDrawn is the one predicate the notice and the gate both read.
//
// POSITIVELY CONTROLLED, because "pressing G changed nothing" proves nothing on
// its own: the same fixture is driven at a height where the pane IS drawn and
// the key must MOVE there, so a fixture that could not move for unrelated
// reasons fails instead of passing.
func TestList_ARefusedPaneKeepsTheOperatorsPlace(t *testing.T) {
	heights := jdePaneHeights()
	place := func(s *ListScreen) string { return fmt.Sprint(s.cursor, "+", s.windowStart) }
	moved, held := 0, 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, key := range []string{"j", "down", "pgdown", "G", "end"} {
				var tall, short int
				for _, termHeight := range heights {
					s := listSized(t, surface.build, termHeight)
					before := place(s)
					next, _ := s.Update(listRuneKey(key))
					s = next.(*ListScreen)
					if s.paneDrawn() {
						if place(s) != before {
							tall++
						}
						continue
					}
					short++
					if place(s) != before {
						t.Errorf("the %s list is refused at height %d and drawing the "+
							"too-short notice, but %q moved the operator from %s to %s — the "+
							"notice promises the moving keys are held",
							surface.name, termHeight, key, before, place(s))
					}
				}
				if short == 0 {
					t.Errorf("%q was never pressed on a refused %s pane, so the hold was not "+
						"tested for it", key, surface.name)
				}
				if tall == 0 {
					t.Errorf("%q never moved the %s cursor at ANY drawable height, so the "+
						"check above passes for a reason unrelated to the refusal",
						key, surface.name)
				}
				moved += tall
				held += short
			}
		})
	}
	if moved == 0 || held == 0 {
		t.Fatalf("the sweep saw %d moves and %d holds — one of the two states was never "+
			"reached, so the control is missing", moved, held)
	}
}

// listRootLines renders a list inside a real Root of this size and returns the
// CLIPPED pane, which is the only render worth asserting a bound on: Root.View
// clamps the joined content to the width the terminal really gives, and a
// screen measured on its own cannot see what that takes.
func listRootLines(t *testing.T, s *ListScreen, w, h int) []string {
	t.Helper()
	sized, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	r := newTestRoot(sized)
	next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
	after, ok := next.(Root)
	if !ok {
		t.Fatalf("Root.Update returned %T, want Root", next)
	}
	return strings.Split(after.View(), "\n")
}

// TestList_ARefusedPaneNamesAHeightTheTerminalCannotClip: at EVERY width Root
// will draw a list at, the too-short notice's leading figure — the height the
// operator must resize to — reaches the pane whole.
//
// THE DEFECT. The notice folded and marked itself against a hard-coded 51 cells
// (screenBodyWidth(80)) on a screen that recorded only the terminal HEIGHT, so
// below 80 columns Root clipped the sentence the screen had just refused the
// pane in order to show. At width 60 the pane is 31 cells and
// "Too short: needs 16 rows, has 12." is 33, so it drew as
// "Too short: needs 16 rows, has 1" — the operator asked to act on a number
// that is not the one the code computed, with nothing marking the cut, and
// StyleMuted's closing reset dropped off the end into whatever came after. A
// refusal naming a WRONG height is worse than the clipped footer it replaced.
//
// THE WIDTHS ARE DERIVED from Root's own gate (jdeDrawableWidths), not picked.
// This property held at 80, 100 and 120 and failed at every width from 45 to
// 79; three hand-picked widths is exactly how the 60-column hole survived an
// earlier round of this work.
//
// WHAT IS ASSERTED IS THE LEAD AND NOT THE WHOLE NOTICE, because no wording
// carrying the way out and both numbers fits the 16 cells the narrowest drawable
// pane gives. That is the "whatever must survive must lead" rule, and on a
// refusal the load-bearing clause is the WAY OUT: listTooShortWayOut leads in
// its own fold segment, then the height NEEDED, then the height the operator
// already HAS, then the prose — so a trim on either axis takes the tail and can
// never leave a wrong number standing.
//
// THE LEAD IS READ OFF THE RECORD, so the reordering that put the RULE in front
// of the exception ("No keys but Esc" rather than "Esc leaves", the order
// jdeTooShort states) had to keep this test passing rather than be traded
// against it: both facts ride ONE clause, so the clause that must survive is
// still the clause that leads and no prefix of the notice denies the key
// without naming it.
//
// SO THE TWO CLAIMS ARE ASKED AT DIFFERENT SCOPES, and the difference is the
// one pane where they genuinely cannot both be met: the way out must be on
// EVERY drawable pane, and the height figure on every pane of more than one
// row. One row is terminal height 7 alone (screenBodyRows is height − 6), and
// at 16 cells no line carries both — there the height gives, which is the
// deliberate choice recorded at listTooShort. The row threshold is asked of
// the screen's own listPaneRows rather than written as a number.
func TestList_ARefusedPaneNamesAHeightTheTerminalCannotClip(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	if len(widths) == 0 {
		t.Fatal("no drawable widths — the derivation is broken, not the app")
	}
	checked := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for state, rows := range listRowCases {
				for _, w := range widths {
					for _, h := range heights {
						probe := listWithRows(surface.build, rows)
						sized, _ := probe.Update(tea.WindowSizeMsg{Width: w, Height: h})
						probe = sized.(*ListScreen)
						if probe.paneDrawn() {
							continue
						}
						checked++
						lines := listRootLines(t, listWithRows(surface.build, rows), w, h)
						on := func(want string) bool {
							for _, line := range lines {
								if strings.Contains(line, want) {
									return true
								}
							}
							return false
						}
						// THE WAY OUT, on every drawable pane. Esc is the one key this
						// frame names, and a refusal whose way out the terminal cut is
						// the refusal an operator cannot act on.
						if !on(listTooShortWayOut) {
							t.Errorf("the %s refusal at %dx%d (%s) does not put %q on the pane "+
								"whole — the operator is left on a frame that names no way out:"+
								"\n%s", surface.name, w, h, state, listTooShortWayOut,
								strings.Join(lines, "\n"))
						}
						if want := fmt.Sprintf("needs %d rows", probe.needRows()+screenChromeRows); probe.listPaneRows() > 1 && !on(want) {
							t.Errorf("the %s refusal at %dx%d (%s) has %d pane rows and does not "+
								"put %q on the pane whole — the operator is asked to resize to a "+
								"height the terminal cut:\n%s", surface.name, w, h, state,
								probe.listPaneRows(), want, strings.Join(lines, "\n"))
						}
						// AND NOTHING OF IT OVERRUNS, which is the other half and the
						// one the leading figure cannot speak for: a line wider than
						// the pane is a line clampToBox truncates, and truncating a
						// styled line drops StyleMuted's closing reset off the end and
						// colours everything drawn after it. Measured on the SCREEN's
						// own output against the pane it was given, because by the time
						// Root has clipped it the overrun has already happened.
						for _, line := range strings.Split(probe.View(), "\n") {
							if lipgloss.Width(line) > probe.listPaneCells() {
								t.Errorf("the %s refusal at %dx%d (%s) draws %d cells into a pane "+
									"of %d, so clampToBox cuts it: %q", surface.name, w, h, state,
									lipgloss.Width(line), probe.listPaneCells(), line)
							}
						}
					}
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no list refused a pane at any drawable size, so this check asserted " +
			"nothing about the notice it exists to bound")
	}
}

// listWayOutKey is one keystroke a refusal notice can name: the WORD the notice
// spells it with, and the message pressing it really sends.
type listWayOutKey struct {
	name string
	msg  tea.KeyMsg
}

// listWayOutKeyNames transcribes the words a refusal notice may use for a key
// into the keystroke each one spells, and nothing else.
//
// It is a TRANSCRIPTION and never a fallback, which is the whole reason it is a
// table rather than a call into listRuneKey: that helper resolves a name it has
// not been taught to KeyRunes, so a reworded notice would silently be "pressed"
// as literal text and the sweep below would certify a way out nobody can use.
// poPickerKeyMsg shipped exactly that hole once (AGENTS.md), which is why an
// unknown word here is a FATAL rather than a guess.
var listWayOutKeyNames = map[string]listWayOutKey{
	"Esc": {"esc", tea.KeyMsg{Type: tea.KeyEsc}},
}

// listWayOutKeys is every keystroke the notice's own record NAMES, read out of
// that record so the press and the wording cannot come apart.
//
// listTooShortWayOut exists so the notice and its sweeps read ONE record; a
// sweep that hard-codes the keystroke and quotes the constant only in its
// failure message leaves the drift open in the one direction that matters —
// reword the notice and the test goes on pressing the old key, goes on passing,
// and certifies a way out the pane no longer names.
//
// IT READS THE RECORD'S STRUCTURE rather than scanning it for words it happens
// to know, and that is what lets an unknown KEY be told apart from an ordinary
// one. The notice is built of " · " clauses and each is worded rule-then-
// exception, so a clause ENDS on its key and everything before it is prose:
// "No keys but Esc" names Esc. Scanning every word instead, an unrecognised key
// was indistinguishable from the word "keys" and could only be skipped — so a
// notice reworded to "No keys but Esc · nor Ctrl-C" would have pressed esc,
// passed over Ctrl-C in silence and certified half the record. Now every clause
// must yield a key the table knows, or the sweep fatals.
func listWayOutKeys(t *testing.T) []listWayOutKey {
	t.Helper()
	var out []listWayOutKey
	for _, clause := range strings.Split(listTooShortWayOut, " · ") {
		words := strings.Fields(clause)
		if len(words) == 0 {
			continue
		}
		token := strings.Trim(words[len(words)-1], ".,;:")
		key, ok := listWayOutKeyNames[token]
		if !ok {
			t.Fatalf("the refusal notice reads %q, whose clause %q names the key %q — "+
				"and listWayOutKeyNames does not know it, so this sweep would skip that "+
				"clause and certify only the rest of the record. Teach the table the key "+
				"the notice now names: a way out nobody presses is a way out nobody proved",
				listTooShortWayOut, clause, token)
		}
		out = append(out, key)
	}
	if len(out) == 0 {
		t.Fatalf("the refusal notice reads %q and names no key at all, so this sweep "+
			"would press nothing and pass", listTooShortWayOut)
	}
	return out
}

// TestList_ARefusedPaneNamesAKeyThatReallyLeaves: the one key the too-short
// notice names actually gets the operator off the refused pane.
//
// NAMING A KEY THAT DOES NOT ACT ON THE FRAME IT IS NAMED ON is the defect this
// whole branch exists to close, so the way out cannot be added on the strength
// of reading Root's switch — it is pressed, through a real Root, at every
// drawable pane the refusal is reachable at, and the screen has to change.
//
// It is a claim about LEAVING and not about the list: esc does not act on the
// list (the gate holds movement and sort), it pops the back-stack, or falls
// home from the bottom of it. Both count as leaving and both are exercised —
// the stack is empty in one case and carries a screen in the other, because
// "esc worked" for the wrong one of those two reasons is how a way out comes to
// be named on a frame it does not really work on.
//
// THE KEY IT PRESSES IS READ OFF THE NOTICE (listWayOutKeys), not written here,
// so rewording the notice changes what this sweep presses. Every key the record
// names has to leave; a word the transcription does not know is a fatal rather
// than a silent skip.
func TestList_ARefusedPaneNamesAKeyThatReallyLeaves(t *testing.T) {
	wayOut := listWayOutKeys(t)
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	checked := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for state, rows := range listRowCases {
				for _, w := range widths {
					for _, h := range heights {
						probe := listWithRows(surface.build, rows)
						sized, _ := probe.Update(tea.WindowSizeMsg{Width: w, Height: h})
						if sized.(*ListScreen).paneDrawn() {
							continue
						}
						for _, key := range wayOut {
							for _, withHistory := range []bool{false, true} {
								list := listWithRows(surface.build, rows)
								r := newTestRoot(list)
								if withHistory {
									r.history = []navEntry{{screen: NewWelcomeScreen(), ws: WSScan}}
								}
								next, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
								r = next.(Root)
								after, _ := r.Update(key.msg)
								checked++
								if after.(Root).screen == Screen(list) {
									t.Errorf("the %s refusal at %dx%d (%s, history %v) reads %q "+
										"and pressing %q left the operator on the same screen",
										surface.name, w, h, state, withHistory,
										listTooShortWayOut, key.name)
								}
							}
						}
					}
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no list was refused at any drawable size, so the way out this notice " +
			"names was never pressed")
	}
}

// TestList_ARefusedPaneHoldsTheKeysThatCouldNotBeSeenToAct: on a pane too short
// to draw the frame, `s` declines alongside the movement vocabulary.
//
// `s` re-orders locally and its ONLY visible product is headerLine, which the
// refusal branch does not draw; needRows is invariant under re-ordering
// (minBodyLines is a MAX over the rows and footerHint does not read the sort),
// so pressing it returned the pane byte for byte — standing rule 1, introduced
// by the refusal itself and closed with it.
//
// POSITIVELY CONTROLLED, because "pressing s changed nothing" is equally true of
// a fixture that could not sort: the same list is driven at a height where the
// pane IS drawn and `s` must change the rendered pane there, so a fixture that
// was inert for unrelated reasons fails instead of passing. The sort MODE is
// asserted too, since holding the key means the screen's own record of it must
// not move either — that is what makes the operator "come back where they were"
// when the terminal grows.
func TestList_ARefusedPaneHoldsTheKeysThatCouldNotBeSeenToAct(t *testing.T) {
	heights := jdePaneHeights()
	held, moved := 0, 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for state, rows := range listRowCases {
				for _, h := range heights {
					s := listWithRows(surface.build, rows)
					sized, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: h})
					s = sized.(*ListScreen)
					before, beforeSort := clampToBox(s.View(), screenBodyCells(80), s.listPaneRows()), s.sort
					next, _ := s.Update(listRuneKey("s"))
					s = next.(*ListScreen)
					after := clampToBox(s.View(), screenBodyCells(80), s.listPaneRows())

					if !s.paneDrawn() {
						held++
						if s.sort != beforeSort {
							t.Errorf("the %s list is refused at height %d (%s) and drawing the "+
								"too-short notice, but `s` moved the sort from %v to %v — a "+
								"re-order nothing on the pane can show",
								surface.name, h, state, beforeSort, s.sort)
						}
						if after != before {
							t.Errorf("the %s refusal at height %d (%s) changed under `s`",
								surface.name, h, state)
						}
						continue
					}
					if after != before {
						moved++
					}
				}
			}
		})
	}
	if held == 0 {
		t.Fatal("`s` was never pressed on a refused list pane, so the hold was not tested")
	}
	if moved == 0 {
		t.Fatal("`s` changed no drawn list pane at any height, so the control is missing " +
			"and every hold above passed for a reason unrelated to the refusal")
	}
}

// listSearchOverlayKeys are the keystrokes the search overlay's bar can be held
// to, which is NOT the whole key space.
//
// A search box is a focused textinput and every printable rune belongs to it by
// design: typing is what the surface is FOR, so "a key the bar does not name
// must do nothing" cannot apply to a rune there (the same exemption
// poFieldKeys records for the columnar forms, and the same reason
// jde_form.go's movement gate deliberately leaves typing alone — declining a
// rune would DISCARD input, including a scanner burst). What is left is the
// keys the OVERLAY owns, and those are exactly what its bar claims.
func listSearchOverlayKeys() []string {
	return []string{
		"up", "down", "enter", "esc", "home", "end", "pgup", "pgdown",
		"tab", "shift+tab", "left", "right",
	}
}

// TestList_TheSearchOverlayNamesExactlyTheKeysThatWork is the rule over the
// SEARCH state of every list that has one, at every row count.
//
// A SEARCH THAT MATCHED NOTHING IS THE STATE THIS BAR SPENDS ITS LIFE IN: it is
// what the operator sees for every prefix of every query while the answer is
// still coming, and the overlay's bar was a CONSTANT — "↑/↓ move · enter open ·
// esc cancel" — named over no rows at all. Both arms decline in silence there,
// so the pane came back byte for byte under a bar promising three keys and
// answering one.
//
// It is a separate test from the browse sweep rather than another loop inside
// it because the two states answer to different bars, different handlers
// (updateSearch switches on m.Type before Update's own switch is reached) and a
// different key set — and folding them together is how one of them ends up
// pressed against the other's claim.
func TestList_TheSearchOverlayNamesExactlyTheKeysThatWork(t *testing.T) {
	searched := 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			if listWithRows(surface.build, 8).spec.searchLoader == nil {
				return // this list names no search key; nothing to open
			}
			searched++
			for state, rows := range listRowCases {
				open := func() *ListScreen {
					s := listWithRows(surface.build, rows)
					next, _ := s.Update(listRuneKey("/"))
					return next.(*ListScreen)
				}
				bar := open()
				if !bar.searching {
					t.Fatalf("%s has a search loader but `/` did not open the overlay", surface.name)
				}
				// LEGIBILITY on a real pane, the same half the browse footer
				// carries: a claim the operator cannot read is not a claim, and
				// asserting a method's return value is exactly the blindness that
				// let every list ship with its footer cut at 51 columns.
				for _, termHeight := range []int{24, 30} {
					sized := listWithRows(surface.build, rows)
					next, _ := sized.Update(tea.WindowSizeMsg{Width: 80, Height: termHeight})
					sized = next.(*ListScreen)
					next, _ = sized.Update(listRuneKey("/"))
					sized = next.(*ListScreen)
					for _, segment := range strings.Split(sized.searchBarHint(), " · ") {
						if !listFooterLegible(t, sized, termHeight, segment) {
							t.Errorf("the %s search bar claims %q on a line the pane cuts off "+
								"(%s, height %d):\n%s", surface.name, segment, state, termHeight,
								strings.Join(listPaneLines(t, sized, termHeight), "\n"))
						}
					}
				}

				tokens := listSearchBarTokens(t, bar.searchBarHint())
				named := map[string]bool{}
				for _, keys := range tokens {
					for _, k := range keys {
						named[k] = true
					}
				}

				pane := func(x *ListScreen) string {
					return clampToBox(x.View(), screenBodyWidth(80), screenBodyHeight(24))
				}

				// FORWARD, per TOKEN. "↑/↓" is one token for two opposed keys and
				// the claim it makes is that SOME key it spells moves — which is
				// the granularity every bar in this program spells a pair at, and
				// why a list EDGE stays silent rather than declining out loud
				// (AGENTS.md): the highlight is visibly at the end, so the press
				// has answered itself. Pressed in SEQUENCE with no reset, since
				// `down` is the one with room from a cursor resting at the top.
				for token, keys := range tokens {
					s := open()
					acted := false
					for _, k := range keys {
						before := pane(s)
						next, cmd := s.Update(listRuneKey(k))
						s = next.(*ListScreen)
						if pane(s) != before || listSearchActed(cmd) {
							acted = true
						}
					}
					if !acted {
						t.Errorf("the %s search bar names %q with %s and none of %v does "+
							"anything:\n%s", surface.name, token, state, keys, bar.searchBarHint())
					}
				}

				// REVERSE, per KEY: a key that acts must be spelled by some token
				// the bar drew.
				for _, key := range listSearchOverlayKeys() {
					s := open()
					before := pane(s)
					next, cmd := s.Update(listRuneKey(key))
					s = next.(*ListScreen)
					if (pane(s) != before || listSearchActed(cmd)) && !named[key] {
						t.Errorf("the %s search bar does not name %q with %s, but pressing it acts:\n%s",
							surface.name, key, state, bar.searchBarHint())
					}
				}
			}
		})
	}
	if searched == 0 {
		t.Fatal("no list surface has a search loader, so this sweep asserted nothing")
	}
}

// TestList_TheSearchBarCeilingIsTheTallestBarItDraws: listSearchBarHint, which
// listBodyLines reserves rows against, really is every key searchBarHint can
// draw.
//
// A budget measured against a bar the frame can EXCEED is a bar that gets cut —
// the failure listSearchBarHint's own doc was written for, when the renderer and
// the reservation read different literals. Splitting the constant into a ceiling
// and a live builder reopened that door from the other side: the ceiling is now
// a second spelling of the same three segments, and nothing but this check
// stops a segment being added to one and not the other.
func TestList_TheSearchBarCeilingIsTheTallestBarItDraws(t *testing.T) {
	s := listLoaded(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) })
	if got := s.searchBarHint(); got != listSearchBarHint {
		t.Errorf("with results and a detail screen the overlay draws %q, but "+
			"listBodyLines budgets against %q — the ceiling is not the tallest bar "+
			"this overlay can draw", got, listSearchBarHint)
	}
	for _, rows := range []int{0, 1, 8} {
		x := listWithRows(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) }, rows)
		for _, segment := range strings.Split(x.searchBarHint(), " · ") {
			if !strings.Contains(listSearchBarHint, segment) {
				t.Errorf("at %d rows the overlay draws the segment %q, which the ceiling "+
					"%q does not carry", rows, segment, listSearchBarHint)
			}
		}
	}
}

// listSearchActed reports whether a command the overlay returned is work rather
// than a caret tick.
//
// The blink is not an act: bubbles falls through to Cursor.Update for any key
// its own switch does not handle and that returns a tick unconditionally, so an
// unfiltered `cmd != nil` would read every key pressed inside a focused box as
// working — the filter poCmdActs already applies on the purchasing side.
func listSearchActed(cmd tea.Cmd) bool {
	return cmd != nil && !poIsBlink(cmd())
}

// listSearchBarTokens parses the overlay's bar into the tokens it draws and the
// keystrokes each one SPELLS.
//
// Its movement token is spelled "↑/↓" rather than the browse footer's "↑↓", so
// it needs its own transcription — and an unknown token FAILS here exactly as it
// does in listNamedKeys, because a token the sweep skips is a claim nobody
// presses.
func listSearchBarTokens(t *testing.T, hint string) map[string][]string {
	t.Helper()
	spelling := map[string][]string{
		"↑/↓":   {"up", "down"},
		"enter": {"enter"},
		"esc":   {"esc"},
	}
	out := map[string][]string{}
	for _, part := range strings.Split(hint, " · ") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		keys, ok := spelling[fields[0]]
		if !ok {
			t.Fatalf("search-bar token %q is not transcribed — add it so the rule covers it (hint: %q)",
				fields[0], hint)
		}
		out[fields[0]] = keys
	}
	return out
}

// TestList_TheFooterSurvivesEveryScrollPosition walks the cursor through a
// whole list and requires, at every position, that the footer is still
// readable and the highlighted row is still on the pane.
//
// The sweep above checks two positions (as opened, and after G). Two is not
// enough, because how many LINES the window spends depends on which rows are
// in it: the window is sized by packing rows into the body's line budget, and
// a size taken at one start is wrong at another. Before the window was
// re-derived on every move, a list whose first rows are title-only and whose
// later rows carry a subtitle or a metrics line — the ordinary shape of the
// purchasing and inventory lists — rendered twenty lines into an eighteen-line
// pane the moment the cursor reached the taller rows, and what clampToBox
// dropped off the bottom was the folded footer with "N new PO" in it.
//
// The cursor half matters for the same reason it does on the pickers: a
// highlighted row the operator cannot see is a row they act on blind.
func TestList_TheFooterSurvivesEveryScrollPosition(t *testing.T) {
	heights := jdePaneHeights()
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, termHeight := range heights {
				for _, key := range []string{"j", "pgdown"} {
					s := listSized(t, surface.build, termHeight)
					if !s.paneDrawn() {
						continue
					}
					for step := 0; step < len(s.rows); step++ {
						pane := listPaneLines(t, s, termHeight)
						for _, segment := range strings.Split(s.footerHint(), " · ") {
							if !listFooterLegible(t, s, termHeight, segment) {
								t.Fatalf("the %s footer claims %q on a line the pane cuts off "+
									"(height %d, %s x%d, cursor %d, window %d+%d):\n%s",
									surface.name, segment, termHeight, key, step,
									s.cursor, s.windowStart, s.windowSize, strings.Join(pane, "\n"))
							}
						}
						if !listCursorRowOnPane(t, s, termHeight) {
							t.Fatalf("the %s highlight is on a row the pane cuts off "+
								"(height %d, %s x%d, cursor %d, window %d+%d):\n%s",
								surface.name, termHeight, key, step,
								s.cursor, s.windowStart, s.windowSize, strings.Join(pane, "\n"))
						}
						next, _ := s.Update(listRuneKey(key))
						s = next.(*ListScreen)
					}
				}
			}
		})
	}
}

// listCursorRowOnPane reports whether the row the cursor is on survives the
// clip. bodyView marks it with "▸ ", and it is the only row so marked.
func listCursorRowOnPane(t *testing.T, s *ListScreen, termHeight int) bool {
	t.Helper()
	if len(s.rows) == 0 {
		return true
	}
	title := s.rows[s.cursor].Title
	for _, line := range listPaneLines(t, s, termHeight) {
		if strings.Contains(line, "▸ ") && strings.Contains(line, title) {
			return true
		}
	}
	return false
}

// TestList_SearchBoxCaretStaysOnThePane is the typing half of the same rule
// the sweeps above hold for the footer: a keystroke the operator cannot see is
// a keystroke that did nothing, and a search box whose value has outgrown its
// row cuts the caret off the right edge on every rune after it.
//
// Distinct runes, because a scrolling viewport full of one repeated character
// looks the same however far it has scrolled.
func TestList_SearchBoxCaretStaysOnThePane(t *testing.T) {
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, termHeight := range []int{24, 30} {
				s := listSized(t, surface.build, termHeight)
				next, _ := s.Update(listRuneKey("/"))
				s = next.(*ListScreen)
				if !s.searching {
					return // this list names no search key
				}
				row := func() string {
					for _, line := range listPaneLines(t, s, termHeight) {
						if strings.Contains(line, listSearchPrompt) {
							return line
						}
					}
					return "<not on the pane>"
				}
				before := row()
				if before == "<not on the pane>" {
					t.Fatalf("the search box is not on the 80x%d pane at all", termHeight)
				}
				for i := 0; i < 100; i++ {
					next, _ := s.Update(listRuneKey(string(rune('a' + i%26))))
					s = next.(*ListScreen)
					after := row()
					if after == before {
						t.Fatalf("keystroke %d into the %s search box redrew the row byte for byte "+
							"at 80x%d — the query has outgrown it and the pane is cutting the caret off:\n\t%q",
							i+1, surface.name, termHeight, after)
					}
					before = after
				}
			}
		})
	}
}

// TestList_SiblingSurfaceKeysOpenTheSurfaceTheyName closes the other half of the
// hole. The sweep above proves a named key ACTS; it does not prove it acts on
// the thing the words promise. `N new PO` opening the reorder queue would pass
// it, and the report was about a key whose words were the whole expectation.
func TestList_SiblingSurfaceKeysOpenTheSurfaceTheyName(t *testing.T) {
	want := map[string]string{
		"N new PO":           "*tui.PurchaseOrderCreateScreen",
		"Q pending reorders": "*tui.ReorderQueueScreen",
		"I new item":         "*tui.InventoryItemFormScreen",
		"C categories":       "*tui.CategoryListScreen",
		"L locations":        "*tui.LocationListScreen",
		"U suppliers":        "*tui.SupplierListScreen",
		"M PM items":         "*tui.MaintenanceItemsScreen",
		"A new asset":        "*tui.AssetFormScreen",
	}
	seen := map[string]bool{}

	for _, surface := range listBarSurfaces() {
		s := listLoaded(surface.build)
		for _, sc := range listShortcuts(s.spec.kind) {
			entry := sc.key + " " + sc.label
			wantType, ok := want[entry]
			if !ok {
				t.Fatalf("%s advertises %q — add it to this table so its destination is pinned", surface.name, entry)
			}
			seen[entry] = true

			// Through Root, because that is where a key is actually resolved:
			// a screen-level assertion would pass while the global layer ate
			// the key on the way in.
			r := newTestRoot(s)
			next, cmd := r.Update(listRuneKey(sc.key))
			after := next.(Root)
			if cmd == nil {
				t.Fatalf("%s: %q issued no command", surface.name, entry)
			}
			msg := cmd()
			switch m := msg.(type) {
			case SwitchScreenMsg:
				after.screen = m.Screen
			default:
				t.Fatalf("%s: %q produced %T, want a screen switch", surface.name, entry, msg)
			}
			if got := fmt.Sprintf("%T", after.screen); got != wantType {
				t.Errorf("%s: %q opened %s, want %s", surface.name, entry, got, wantType)
			}
		}
	}
	for entry := range want {
		if !seen[entry] {
			t.Errorf("no list advertises %q any more — drop it from this table", entry)
		}
	}
}

// TestList_NOnThePurchaseOrderListOpensTheNewOrderScreen is the report itself,
// end to end at 80 columns: press N on the Purchasing list and be looking at
// the New purchase order screen.
//
// Asserted against the CLIPPED Root.View() (AGENTS.md) rather than the screen's
// own View, because the pane is 51 columns wide there and this test's whole job
// is to say what the operator is actually looking at.
func TestList_NOnThePurchaseOrderListOpensTheNewOrderScreen(t *testing.T) {
	s := listLoaded(func() *ListScreen { return newScreenFor(WSPurchasing, Deps{}).(*ListScreen) })
	r := newTestRoot(s)
	next, _ := r.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	r = next.(Root)

	// The CLAIM has to be on the operator's screen, not merely in the string
	// the screen would like to show. This assertion used to read footerHint()
	// and so passed while "N new PO" sat 20 columns past the right edge.
	if !listFooterLegible(t, s, 30, "N new PO") {
		t.Fatalf("the Purchasing footer does not offer a legible N at 80x30:\n%s",
			strings.Join(listPaneLines(t, s, 30), "\n"))
	}
	if out := r.View(); !strings.Contains(out, "N new PO") {
		t.Fatalf("the 80-column render does not carry the N claim:\n%s", out)
	}

	next, cmd := r.Update(listRuneKey("N"))
	r = next.(Root)
	if cmd == nil {
		t.Fatal("N on the purchase-order list did nothing — the footer names it")
	}
	next, _ = r.Update(cmd())
	r = next.(Root)

	if _, ok := r.screen.(*PurchaseOrderCreateScreen); !ok {
		t.Fatalf("N landed on %T, want the New purchase order screen", r.screen)
	}
	out := r.View()
	if !strings.Contains(out, "New purchase order") {
		t.Errorf("the 80-column render does not show the new-order screen:\n%s", out)
	}
	// The first phase is the supplier picker, and what says so is its own
	// standing note plus the bar naming the one key that acts while the list is
	// still on its way. "Pick a supplier" was the prose action bar's lead-in;
	// the columnar bar names keys, not phases.
	if !strings.Contains(out, "Every picker after this is scoped") {
		t.Errorf("the new-order screen did not open on its first phase:\n%s", out)
	}
}

// TestList_SearchOverlayBarNamesExactlyTheKeysThatWork: the searching state has
// a bar of its own, and letters there are TEXT. A sweep that only knew the
// browse footer would miss a claim made on the overlay.
func TestList_SearchOverlayBarNamesExactlyTheKeysThatWork(t *testing.T) {
	s := listLoaded(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) })
	next, _ := s.Update(listRuneKey("/"))
	s = next.(*ListScreen)
	if !s.searching {
		t.Fatal("/ did not open the search overlay on a list that names it")
	}
	out := s.View()
	// The LIVE bar. With eight results this fixture draws every key the
	// listSearchBarHint ceiling spells, so the two coincide here — but the
	// constant is the BUDGET's fixed point, not a claim the frame always makes,
	// and asserting it would go stale the first time this fixture lost a row.
	if !strings.Contains(out, s.searchBarHint()) {
		t.Fatalf("the search overlay does not render its bar:\n%s", out)
	}

	// And it is the ONLY bar. updateSearch swallows every key that is not an
	// arrow, enter or esc into the query, so the browse footer's claims are all
	// false here — pressing N types "N". Folding that footer is what made all
	// eight of its sibling-surface claims legible in exactly this state, so the
	// sweep has to say they are gone, not merely that the overlay's own four
	// keys work.
	for _, height := range []int{24, 30} {
		sized := listSized(t, func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) }, height)
		next, _ := sized.Update(listRuneKey("/"))
		sized = next.(*ListScreen)
		if !sized.searching {
			t.Fatal("/ did not open the search overlay")
		}
		pane := strings.Join(listPaneLines(t, sized, height), "\n")
		// The LIVE bar, not the listSearchBarHint ceiling: the constant is what
		// listBodyLines budgets against, and reading it here would assert a claim
		// the frame does not necessarily make (it drops `↑/↓ move` below two
		// results and `enter open` below one).
		drawn := sized.searchBarHint()
		if !strings.Contains(pane, drawn) {
			t.Errorf("the overlay bar is not on the 80x%d pane:\n%s", height, pane)
		}
		for _, segment := range strings.Split(sized.footerHint(), " · ") {
			if strings.Contains(pane, segment) && !strings.Contains(drawn, segment) {
				t.Errorf("the browse footer still claims %q while the search box owns the keyboard (80x%d):\n%s",
					segment, height, pane)
			}
		}
	}

	// Every browse-footer key is inert here, which is why naming them would be
	// a lie: they go into the query instead.
	typed := listLoaded(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) })
	n, _ := typed.Update(listRuneKey("/"))
	typed = n.(*ListScreen)
	for _, k := range []string{"N", "Q", "n", "s", "f", "r", "g", "G"} {
		n, _ = typed.Update(listRuneKey(k))
		typed = n.(*ListScreen)
	}
	if got := typed.searchInput.Value(); got != "NQnsfrgG" {
		t.Errorf("the browse-footer keys produced query %q — they are not inert under the overlay", got)
	}
	// The whole key space against the overlay, in both directions. Printable
	// runes are exempt and by design: with the box open they act by going into
	// the query, which is what the box is FOR, so the reverse half of the rule
	// cannot apply to them (the same exemption poFieldKeys makes on the New PO
	// screen's typing phases). Everything else is judged — a key bound in
	// updateSearch that this bar does not name would be `N` all over again, on
	// the one list state the browse footer has nothing to say about.
	// DERIVED from the bar the overlay actually draws, not restated. It used to
	// be a literal {up, down, enter, esc} — the old constant's claim, copied —
	// which was safe only while that bar was a constant: now that it drops
	// `↑/↓ move` below two results and `enter open` below one, a restated roster
	// would be this check making a claim on the bar's behalf, which is the defect
	// the transcription rule exists to report. (It also used to credit
	// ctrl+p/ctrl+n, on the reasoning that "↑/↓" named the emacs pair as readily
	// as it named the arrows — a synonym this bar never says. updateSearch binds
	// the arrows alone now.)
	overlayNamed := map[string]bool{}
	opened := listLoaded(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) })
	if next, _ := opened.Update(listRuneKey("/")); next != nil {
		opened = next.(*ListScreen)
	}
	for _, keys := range listSearchBarTokens(t, opened.searchBarHint()) {
		for _, k := range keys {
			overlayNamed[k] = true
		}
	}
	for _, k := range listKeySpace() {
		if r := []rune(k); len(r) == 1 && r[0] >= 0x20 && r[0] <= 0x7e {
			continue
		}
		acted := false
		for _, probe := range [][]string{nil, {"down"}} {
			fresh := listLoaded(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) })
			n, _ := fresh.Update(listRuneKey("/"))
			fresh = n.(*ListScreen)
			for _, p := range probe {
				n, _ = fresh.Update(listRuneKey(p))
				fresh = n.(*ListScreen)
			}
			before := listBarState(fresh)
			n, cmd := fresh.Update(listRuneKey(k))
			fresh = n.(*ListScreen)
			acted = acted || listBarState(fresh) != before || cmd != nil
		}
		switch {
		case overlayNamed[k] && !acted:
			t.Errorf("the search overlay names %q but pressing it does nothing", k)
		case !overlayNamed[k] && acted:
			t.Errorf("the search overlay does not name %q, but pressing it acts", k)
		}
	}

	// Every key the overlay bar names must act there — probed from both ends,
	// since ↑ does nothing at the top and ↓ nothing at the bottom.
	named := make([]string, 0, len(overlayNamed))
	for k := range overlayNamed {
		named = append(named, k)
	}
	sort.Strings(named)
	for _, key := range named {
		acted := false
		for _, probe := range [][]string{nil, {"down"}, {"down", "down"}} {
			fresh := listLoaded(func() *ListScreen { return newScreenFor(WSAssets, Deps{}).(*ListScreen) })
			n, _ := fresh.Update(listRuneKey("/"))
			fresh = n.(*ListScreen)
			for _, p := range probe {
				n, _ = fresh.Update(listRuneKey(p))
				fresh = n.(*ListScreen)
			}
			before := listBarState(fresh)
			n, cmd := fresh.Update(listRuneKey(key))
			fresh = n.(*ListScreen)
			acted = acted || listBarState(fresh) != before || cmd != nil
		}
		if !acted {
			t.Errorf("the search overlay names %q but pressing it does nothing", key)
		}
	}
}

// listSearchSurfaces is every list that has a search overlay, DISCOVERED by
// asking the spec rather than named here — the roster rule this package keeps
// re-learning. It fatals on an empty answer, because a sweep over no surfaces
// passes without pressing anything.
func listSearchSurfaces(t *testing.T) []listBarSurface {
	t.Helper()
	var out []listBarSurface
	for _, surface := range listBarSurfaces() {
		if listWithRows(surface.build, 8).spec.searchLoader != nil {
			out = append(out, surface)
		}
	}
	if len(out) == 0 {
		t.Fatal("no list in the app has a searchLoader, so the overlay sweeps below " +
			"press nothing — either the feature is gone or the discovery is broken")
	}
	return out
}

// listSearchOpen builds a sized list with the overlay open, in that order: the
// size arrives first so scrollIntoView has a real pane to window against, and
// '/' is PRESSED rather than the flag set, so the sweep drives the overlay the
// way an operator reaches it.
func listSearchOpen(t *testing.T, build func() *ListScreen, rows, w, h int) *ListScreen {
	t.Helper()
	s := listWithRows(build, rows)
	sized, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: h})
	next, _ := sized.(*ListScreen).Update(listRuneKey("/"))
	return next.(*ListScreen)
}

// TestList_TheSearchOverlayRefusesRatherThanCuttingItsBar: the overlay is
// inside the refusal like every other state that draws a bar.
//
// IT USED TO BE EXEMPT, ON A PREMISE ABOUT A DIRECTION clampToBox DOES NOT CUT
// IN. paneDrawn excused it because the overlay "pins its bar to the TOP of the
// pane, where clampToBox cannot reach it" — but clampToBox drops from the
// BOTTOM, and the overlay's bar is on the SECOND row of an assembly that then
// draws a header, the markers and the rows beneath it. At 80x7 the pane got the
// input line and nothing else: no bar, no rows, no notice, every key the
// overlay names still live and nothing on the screen saying so. An exemption
// has to hold at every drawable pane or it is not an exemption, so this walks
// Root's own drawable widths and heights rather than a size somebody picked.
//
// Both halves are asserted, because either alone is satisfied by a screen that
// is always refused or never refused: where the pane IS drawn every segment of
// the overlay's own bar survives the clip whole, and where it is NOT the notice
// is there instead, with no fragment of the bar left beside it.
func TestList_TheSearchOverlayRefusesRatherThanCuttingItsBar(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	drawn, refused := 0, 0
	for _, surface := range listSearchSurfaces(t) {
		t.Run(surface.name, func(t *testing.T) {
			for state, rows := range listRowCases {
				for _, w := range widths {
					for _, h := range heights {
						s := listSearchOpen(t, surface.build, rows, w, h)
						if !s.searching {
							t.Fatalf("%s: '/' did not open the overlay at %dx%d (%s)",
								surface.name, w, h, state)
						}
						pane := listRootLines(t, s, w, h)
						joined := strings.Join(pane, "\n")

						if s.paneDrawn() {
							drawn++
							for _, segment := range strings.Split(s.searchBarHint(), " · ") {
								if !listLineHolds(pane, segment) {
									t.Errorf("the %s search overlay at %dx%d (%s) claims %q on a "+
										"line the pane cuts off:\n%s",
										surface.name, w, h, state, segment, joined)
								}
							}
							continue
						}
						refused++
						// The WAY OUT at every refused pane and the HEIGHT wherever the
						// pane has more than one row, which is the scope the browse
						// refusal already states: a one-row pane keeps only the first
						// folded line and at the narrowest widths that line has room for
						// the clause and not the figure. The height is what gives; the
						// way out is not.
						if !listLineHolds(pane, listTooShortWayOut) {
							t.Errorf("the %s search overlay at %dx%d (%s) cannot draw its bar and "+
								"does not name the way off it — the operator is left on a bar-less "+
								"pane:\n%s", surface.name, w, h, state, joined)
						}
						want := fmt.Sprintf("needs %d rows", s.needRows()+screenChromeRows)
						if s.listPaneRows() > 1 && !listLineHolds(pane, want) {
							t.Errorf("the %s search overlay at %dx%d (%s) is refused and does not "+
								"say how tall a terminal it needs:\n%s",
								surface.name, w, h, state, joined)
						}
						for _, segment := range strings.Split(s.searchBarHint(), " · ") {
							if listLineHolds(pane, segment) {
								t.Errorf("the %s search overlay at %dx%d (%s) is refused but still "+
									"draws %q beside the notice:\n%s",
									surface.name, w, h, state, segment, joined)
							}
						}
					}
				}
			}
		})
	}
	if drawn == 0 || refused == 0 {
		t.Fatalf("the overlay was drawn at %d panes and refused at %d — both halves of "+
			"this check need a case, or one implication was never exercised", drawn, refused)
	}
}

// listLineHolds reports whether one whole line of a clipped pane carries the
// segment, which is the only way a claim is legible to an operator.
func listLineHolds(pane []string, segment string) bool {
	for _, line := range pane {
		if strings.Contains(line, segment) {
			return true
		}
	}
	return false
}

// TestList_ARefusedSearchOverlayNamesAKeyThatReallyLeaves is the way-out claim
// in the state the refusal was just extended to.
//
// It is a SEPARATE press from the browse sweep because the key takes a
// different road: while the overlay is open the screen used to claim raw input,
// so `esc` reached updateSearch and merely CLOSED the overlay — and since the
// browse footer folds to more lines than the overlay's bar, every height that
// refuses the overlay refuses the browse pane too. The notice named a key that
// redrew the notice. WantsRawInput now releases the keyboard on a refused pane,
// and this presses the record's own keys through a real Root to prove it, with
// the back-stack empty and loaded for the reason the browse sweep gives.
func TestList_ARefusedSearchOverlayNamesAKeyThatReallyLeaves(t *testing.T) {
	wayOut := listWayOutKeys(t)
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	checked := 0
	for _, surface := range listSearchSurfaces(t) {
		t.Run(surface.name, func(t *testing.T) {
			for state, rows := range listRowCases {
				for _, w := range widths {
					for _, h := range heights {
						if listSearchOpen(t, surface.build, rows, w, h).paneDrawn() {
							continue
						}
						for _, key := range wayOut {
							for _, withHistory := range []bool{false, true} {
								list := listSearchOpen(t, surface.build, rows, w, h)
								r := newTestRoot(list)
								if withHistory {
									r.history = []navEntry{{screen: NewWelcomeScreen(), ws: WSScan}}
								}
								sized, _ := r.Update(tea.WindowSizeMsg{Width: w, Height: h})
								after, _ := sized.(Root).Update(key.msg)
								checked++
								if after.(Root).screen == Screen(list) {
									t.Errorf("the %s search refusal at %dx%d (%s, history %v) reads "+
										"%q and pressing %q left the operator on the same screen",
										surface.name, w, h, state, withHistory,
										listTooShortWayOut, key.name)
								}
							}
						}
					}
				}
			}
		})
	}
	if checked == 0 {
		t.Fatal("no search overlay was refused at any drawable size, so the way out " +
			"this notice names was never pressed there")
	}
}

// listMarkerCount reads the figure a drawn marker row states, and reports
// whether it stated one at all. It takes the digits that FOLLOW the ↓, which is
// exactly what an operator reads off the row — so a row cut mid-number hands
// back the truncated figure rather than the real one, which is the whole point.
func listMarkerCount(line string) (int, bool) {
	i := strings.Index(line, "↓")
	if i < 0 {
		return 0, false
	}
	digits := ""
	for _, r := range strings.TrimSpace(line[i+len("↓"):]) {
		if r < '0' || r > '9' {
			break
		}
		digits += string(r)
	}
	if digits == "" {
		return 0, false
	}
	n := 0
	for _, r := range digits {
		n = n*10 + int(r-'0')
	}
	return n, true
}

// listMarkerLineCases are the marker states the row can be asked to draw, with
// counts chosen so the figure is one, two and three digits wide — a one-digit
// count cannot be truncated mid-number, so a sweep carrying only "1" would pass
// over the defect this exists to report.
var listMarkerLineCases = []struct {
	name  string
	above bool
	below int
}{
	{"both", true, 12},
	{"both, wide count", true, 999},
	{"both, one digit", true, 1},
	{"above only", true, 0},
	{"below only", false, 12},
	{"below only, wide count", false, 999},
}

// TestList_TheMarkerRowTellsBothFactsOrMarksTheCut: the ↑/↓ row states the
// facts it promises, whole, or says it gave one up — and NEVER states a figure
// the list does not have.
//
// The shared row was assembled at full length and clipped to the pane, so below
// the width it fits the operator read "  ↑ more above · ↓ 1" at 45 columns and
// "  ↑ more above · ↓ 12 more b" at 60: a row promising two facts, delivering
// one and a half, with the truncation unmarked — and at 45 A COUNT CUT
// MID-NUMBER, which does not read as a shortened fact but as a different one.
// Twelve rows below reported as one is the price column drawing @ 3.50 as @ 3.,
// on the row whose whole job is to say how much of the list is out of sight.
//
// Swept over Root's own drawable widths rather than a width somebody picked,
// because 45 and 60 are both below the 80 every legibility loop in this file
// used to walk, which is precisely why nothing reported it.
func TestList_TheMarkerRowTellsBothFactsOrMarksTheCut(t *testing.T) {
	widths := jdeDrawableWidths()
	if len(widths) == 0 {
		t.Fatal("no drawable widths — the derivation is broken, not the app")
	}
	degraded := 0
	for _, tc := range listMarkerLineCases {
		for _, w := range widths {
			cells := screenBodyCells(w)
			line := listMarkerLine(tc.above, tc.below, cells)
			if line == "" {
				t.Errorf("%s at width %d (%d cells): the row says nothing at all, so the "+
					"operator is not told the list continues", tc.name, w, cells)
				continue
			}
			if got := lipgloss.Width(line); got > cells {
				t.Errorf("%s at width %d: the row draws %d cells into a pane of %d: %q",
					tc.name, w, got, cells, line)
			}
			// The ladder was exercised at this width, which is what stops the
			// sweep certifying a bound it never reached (the vacuous-fixture rule).
			if lipgloss.Width(line) < lipgloss.Width(listMarkerLine(tc.above, tc.below, 999)) {
				degraded++
			}

			marked := strings.Contains(line, "…")
			if n, stated := listMarkerCount(line); stated && n != tc.below {
				t.Errorf("%s at width %d states %d rows below, and the list has %d — a cut "+
					"number reads as a number: %q", tc.name, w, n, tc.below, line)
			} else if !stated && tc.below > 0 && !marked {
				t.Errorf("%s at width %d drops the count of %d and does not mark the row, so "+
					"the operator is not told a fact was given up: %q",
					tc.name, w, tc.below, line)
			}

			// BOTH FACTS OR NEITHER: a row that tells one of two directions
			// without marking is the mutilation the refusal exists to avoid,
			// one row further in.
			if tc.above && tc.below > 0 {
				if !strings.Contains(line, "↑") || !strings.Contains(line, "↓") {
					t.Errorf("%s at width %d names one direction of two and does not say it "+
						"gave the other up: %q", tc.name, w, line)
				}
			}
		}
	}
	if degraded == 0 {
		t.Fatal("no width in Root's drawable range made the marker row give any ground, " +
			"so this sweep only ever measured the full wording — either the widths are " +
			"no longer derived or the row has stopped being bounded at all")
	}
}

// TestList_EveryMarkerRowOnThePaneFitsIt is the behavioural half: the rows the
// list really draws go through that bounded builder, at every drawable pane.
//
// Asserted on the CLIPPED pane, because the bound that matters is the one the
// operator meets — a marker row measured off the screen's own View cannot fail,
// since clampToBox has not run yet. It walks the cursor into a SCROLLED state
// first, since a list resting at the top has no ↑ fact to lose.
func TestList_EveryMarkerRowOnThePaneFitsIt(t *testing.T) {
	widths, heights := jdeDrawableWidths(), jdePaneHeights()
	scrolled, degraded := 0, 0
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, w := range widths {
				for _, h := range heights {
					// A THREE-DIGIT REMAINDER, because the row's full wording fits the
					// narrowest pane at a one-digit count: swept at eight rows this
					// check could not fail, and passed with both per-marker sites
					// writing their own unbounded literals. A fixture that cannot
					// reach the bound makes the assertion vacuous however precisely
					// it is worded.
					s := listWithRows(surface.build, listMarkerFixtureRows)
					sized, _ := s.Update(tea.WindowSizeMsg{Width: w, Height: h})
					s = sized.(*ListScreen)
					// BOUNDED, and generously: a page-worth of presses scrolls any pane
					// Root will draw. An unbounded reach loop turns a declined key into
					// a hang that fails the whole package by timing out.
					for i := 0; i < listMarkerScrollPresses && s.windowStart == 0; i++ {
						next, _ := s.Update(listRuneKey("j"))
						s = next.(*ListScreen)
					}
					if s.windowStart > 0 {
						scrolled++
					}
					below := len(s.rows) - (s.windowStart + s.windowSize)
					if below < 0 {
						below = 0
					}
					// EVERY marker row the pane draws is one the bounded builder could
					// have produced at that pane. Equality rather than a fits-the-pane
					// check, because the two per-marker sites drew their own literals
					// and cellPrefix would have made either of them "fit" while still
					// saying something the builder had already given up — which is how
					// a bound applied to the shared row left the pair beside it
					// unbounded.
					cells := screenBodyCells(w)
					want := map[string]bool{
						listMarkerLine(true, 0, cells):      true,
						listMarkerLine(false, below, cells): true,
						listMarkerLine(true, below, cells):  true,
					}
					for form := range want {
						if form != "" && lipgloss.Width(form) <
							lipgloss.Width(listMarkerLine(true, below, 999)) {
							degraded++
						}
					}
					clipped := strings.Split(
						clampToBox(s.View(), screenBodyCells(w), screenBodyRows(h)), "\n")
					for _, line := range clipped {
						// A MARKER ROW OPENS ON ITS ARROW, which is what tells it apart
						// from the footer — whose movement segment spells "j/k ↑↓ move"
						// and would otherwise be swept as a marker row that no builder
						// produces.
						head := strings.TrimSpace(line)
						if !strings.HasPrefix(head, "↑") && !strings.HasPrefix(head, "↓") {
							continue
						}
						drawn := strings.TrimRight(line, " ")
						if !want[drawn] {
							t.Errorf("the %s marker row at %dx%d draws %q, which the bounded "+
								"builder does not produce at that pane (%d cells, %d below) — the "+
								"site is writing its own row",
								surface.name, w, h, drawn, cells, below)
						}
						if n, stated := listMarkerCount(drawn); stated && n != below {
							t.Errorf("the %s marker row at %dx%d states %d rows below, and %d are "+
								"out of sight: %q", surface.name, w, h, n, below, drawn)
						}
					}
				}
			}
		})
	}
	if scrolled == 0 {
		t.Fatal("no list ever scrolled at any drawable pane, so no ↑ marker was drawn and " +
			"the shared row this sweep is about was never reached")
	}
	if degraded == 0 {
		t.Fatal("no pane in Root's drawable range asked the marker row for less than its " +
			"full wording, so a draw site writing that wording itself would pass — the " +
			"fixture no longer reaches the bound")
	}
}

// listMarkerFixtureRows is the row count the marker sweep builds, chosen so the
// remainder below the window runs to three digits: the row's full wording fits
// the narrowest drawable pane at a one-digit count, so a smaller fixture
// measures a bound it never reaches.
const listMarkerFixtureRows = 200

// listMarkerScrollPresses bounds the walk that puts the cursor past the window,
// which is all this sweep needs the cursor for. The tallest drawable pane holds
// far fewer rows than this.
const listMarkerScrollPresses = 40
