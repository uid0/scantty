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

// listAllBarKeys is every keystroke the list vocabulary can name, in a stable
// order. A list that does NOT name one is checked to be silent on it — which is
// how a shortcut wired onto the wrong kind would be caught.
func listAllBarKeys() []string {
	seen := map[string]bool{}
	var out []string
	for _, token := range []string{"j/k", "pgup/pgdn", "g/G", "s", "f", "r", "enter", "/", "n",
		"N", "Q", "I", "C", "L", "U", "M", "A"} {
		for _, k := range listBarKeyNames[token] {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
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

// listBarSurfaces is EVERY ListScreen in the app, not the ones this report
// happened to name. The Purchasing list was outside the old sweep; enumerating
// the constructors rather than the reported instances is what stops the next
// list from being outside this one.
func listBarSurfaces() []listBarSurface {
	fromWorkspace := func(ws Workspace) func() *ListScreen {
		return func() *ListScreen { return newScreenFor(ws, Deps{}).(*ListScreen) }
	}
	return []listBarSurface{
		{"purchasing", fromWorkspace(WSPurchasing)},
		{"inventory", fromWorkspace(WSInventory)},
		{"assets", fromWorkspace(WSAssets)},
		{"maintenance", fromWorkspace(WSMaintenance)},
		{"forgekey devices", fromWorkspace(WSForgeKey)},
		{"project storage", func() *ListScreen { return NewProjectStorageListScreen(Deps{}) }},
	}
}

// listLoaded is the state every named key is meaningful in: enough rows that
// the cursor can move in both directions.
func listLoaded(build func() *ListScreen) *ListScreen {
	s := build()
	s.loading = false
	s.windowSize = 3
	for i := 0; i < 8; i++ {
		s.rows = append(s.rows, listRow{ID: fmt.Sprint(i + 1), Title: fmt.Sprintf("Row %d", i+1)})
	}
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
func listPaneLines(t *testing.T, s *ListScreen) []string {
	t.Helper()
	return strings.Split(clampToBox(s.View(), screenBodyWidth(80), 400), "\n")
}

// listFooterLegible reports whether one footer segment ("N new PO") survives
// the clip whole, on a single visible line.
func listFooterLegible(t *testing.T, s *ListScreen, segment string) bool {
	t.Helper()
	for _, line := range listPaneLines(t, s) {
		if strings.Contains(line, segment) {
			return true
		}
	}
	return false
}

// TestList_FooterNamesExactlyTheKeysThatWork is the rule, as a rule, over every
// list screen in the app.
//
// Each named key is probed from several positions — as opened, after G, after
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

			for _, segment := range strings.Split(loaded.footerHint(), " · ") {
				if !listFooterLegible(t, loaded, segment) {
					t.Errorf("the %s footer claims %q on a line the 51-column pane cuts off:\n%s",
						surface.name, segment, strings.Join(listPaneLines(t, loaded), "\n"))
				}
			}

			for _, key := range listAllBarKeys() {
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
	if !listFooterLegible(t, s, "N new PO") {
		t.Fatalf("the Purchasing footer does not offer a legible N at 80 columns:\n%s",
			strings.Join(listPaneLines(t, s), "\n"))
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
	const bar = "↑/↓ move · enter open · esc cancel"
	if !strings.Contains(out, bar) {
		t.Fatalf("the search overlay does not render its bar:\n%s", out)
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
