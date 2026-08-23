package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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

// listBarKeyNames maps a footer token to the keystrokes it claims. The
// multi-key tokens are the arrow ALIASES — a bar that says "j/k move" is also
// promising the arrow keys, and a sweep that did not know that would report
// every list as binding an unnamed `down`.
var listBarKeyNames = map[string][]string{
	"j/k":       {"j", "k", "down", "up"},
	"pgup/pgdn": {"pgup", "pgdown", "ctrl+u", "ctrl+d"},
	"g/G":       {"g", "G", "home", "end"},
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
	s := build()
	s.loading = false
	s.windowSize = 3
	s.rows = listFixtureRows(8)
	return s
}

// listKeyEffect presses one key from a fresh screen (optionally after a probe
// run) and reports whether it changed anything or issued a command.
func listKeyEffect(build func() *ListScreen, probe []string, key string) (changed, issued bool) {
	s := listLoaded(build)
	press := func(k string) tea.Cmd {
		next, cmd := s.Update(listRuneKey(k))
		s = next.(*ListScreen)
		return cmd
	}
	for _, p := range probe {
		press(p)
	}
	before := listBarState(s)
	cmd := press(key)
	return listBarState(s) != before, cmd != nil
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
// WindowSizeMsg so computeWindowSize runs, and filled with enough rows to
// overflow the window it computes.
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

	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			loaded := listLoaded(surface.build)
			named := listNamedKeys(t, loaded.footerHint())

			// Legibility on a REAL pane, at both supported heights and in both
			// scroll positions. A footer claim that survives the 51-column cut
			// only to be dropped off the bottom by clampToBox is just as unread.
			for _, termHeight := range []int{24, 30} {
				for _, scrolled := range []bool{false, true} {
					sized := listSized(t, surface.build, termHeight)
					if scrolled {
						next, _ := sized.Update(listRuneKey("G"))
						sized = next.(*ListScreen)
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

			for _, key := range listKeySpace() {
				var changed, issued bool
				for _, probe := range probes {
					c, i := listKeyEffect(surface.build, probe, key)
					changed = changed || c
					issued = issued || i
				}
				switch {
				case named[key] && !changed && !issued:
					t.Errorf("the %s footer names %q but pressing it does nothing", surface.name, key)
				case !named[key] && (changed || issued):
					t.Errorf("the %s footer does not name %q, but pressing it acts", surface.name, key)
				}
			}
		})
	}
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
	for _, surface := range listBarSurfaces() {
		t.Run(surface.name, func(t *testing.T) {
			for _, termHeight := range []int{24, 30} {
				for _, key := range []string{"j", "pgdown"} {
					s := listSized(t, surface.build, termHeight)
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
	if !strings.Contains(out, "Pick a supplier") {
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
	if !strings.Contains(out, listSearchBarHint) {
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
		if !strings.Contains(pane, listSearchBarHint) {
			t.Errorf("the overlay bar is not on the 80x%d pane:\n%s", height, pane)
		}
		for _, segment := range strings.Split(sized.footerHint(), " · ") {
			if strings.Contains(pane, segment) && !strings.Contains(listSearchBarHint, segment) {
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
	overlayNamed := map[string]bool{}
	for _, k := range []string{"up", "down", "ctrl+p", "ctrl+n", "enter", "esc"} {
		// ↑/↓ names the emacs pair the same way it names the arrows: updateSearch
		// writes `case tea.KeyUp, tea.KeyCtrlP:` as one arm.
		overlayNamed[k] = true
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
	for _, key := range []string{"up", "down", "enter", "esc"} {
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
