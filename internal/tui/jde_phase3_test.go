package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Phase 3 of the JD Edwards redesign: the root's ~25 global letter accelerators
// and its 0-9/s workspace digits are gone, so the only system keys are scroll /
// exit / submit / edit plus the two the root still owns (ctrl+k search, tab into
// the sidebar menu). These tests pin both halves of that: NO letter navigates,
// and EVERY surface a retired key used to open is still reachable.

// ---------------------------------------------------------------------------
// The reachability enumeration, executable.
//
// One row per retired global. This is the table the bead asks for in the PR
// description, kept as a test so a surface can never be orphaned by a later
// edit: if a row's door is removed from the sidebar, this fails.
// ---------------------------------------------------------------------------

type reachRow struct {
	retired string // the global key phase 3 removed
	ws      Workspace
	surface string // "" = the workspace's own row (its default screen)
	want    string // %T of the screen that must open
}

func phase3Reachability() []reachRow {
	return []reachRow{
		// Workspace rows — these replace the 0-9 / s digits.
		{"0", WSScan, "", "*tui.ScanScreen"},
		{"1", WSDashboard, "", "*tui.DashboardScreen"},
		{"2", WSInventory, "", "*tui.ListScreen"},
		{"3", WSPurchasing, "", "*tui.ListScreen"},
		{"4", WSAssets, "", "*tui.ListScreen"},
		{"5", WSFacilities, "", "*tui.FacilitiesScreen"},
		{"6", WSMaintenance, "", "*tui.ListScreen"},
		{"7", WSSIGs, "", "*tui.SIGListScreen"},
		{"8", WSReports, "", "*tui.ReportsScreen"},
		{"9", WSForgeKey, "", "*tui.ListScreen"},
		{"s", WSSettings, "", "*tui.SettingsScreen"},

		// Surfaces that had a global letter of their own.
		{"I", WSInventory, "New item", "*tui.InventoryItemFormScreen"},
		{"G", WSInventory, "Categories", "*tui.CategoryListScreen"},
		{"L", WSInventory, "Locations", "*tui.LocationListScreen"},
		{"U", WSInventory, "Suppliers", "*tui.SupplierListScreen"},
		{"N", WSPurchasing, "New order", "*tui.PurchaseOrderCreateScreen"},
		{"Q", WSPurchasing, "Reorder queue", "*tui.ReorderQueueScreen"},
		{"A", WSAssets, "New asset", "*tui.AssetFormScreen"},
		{"P", WSMaintenance, "PM board", "*tui.PMBoardScreen"},
		{"M", WSMaintenance, "PM items", "*tui.MaintenanceItemsScreen"},
		{"V", WSMaintenance, "Vendors", "*tui.VendorsScreen"},
		{"F", WSForgeKey, "Device types", "*tui.DeviceTypeListScreen"},
		{"f", WSForgeKey, "Firmware", "*tui.FirmwareScreen"},
		{"e", WSForgeKey, "e-Paper panels", "*tui.EPaperPanelsScreen"},
		{"a", WSForgeKey, "Authorizations", "*tui.AuthorizationsScreen"},
		{"l", WSForgeKey, "Lockouts", "*tui.LockoutsScreen"},
		{"o", WSForgeKey, "Operational modes", "*tui.OperationalModesScreen"},
		{"u", WSForgeKey, "Usage sessions", "*tui.UsageScreen"},
		{"m", WSSettings, "Profile", "*tui.ProfileScreen"},
		{"n", WSSettings, "Notifications", "*tui.NotificationsScreen"},
		{"D", WSSettings, "Donations", "*tui.DonationsScreen"},
		{"W", WSSettings, "Webhooks", "*tui.WebhookListScreen"},
	}
}

// TestPhase3_EveryRetiredGlobalStillHasADoor walks the sidebar to each surface
// with real key presses and checks the screen that opens. NO SURFACE MAY BECOME
// UNREACHABLE is the bead's one hard rule; this is that rule as code.
func TestPhase3_EveryRetiredGlobalStillHasADoor(t *testing.T) {
	for _, row := range phase3Reachability() {
		name := string(row.ws)
		if row.surface != "" {
			name += "/" + row.surface
		}
		t.Run(name, func(t *testing.T) {
			r := openFromMenu(t, newTestRoot(NewWelcomeScreen()), row.ws, row.surface)
			if got := fmt.Sprintf("%T", r.screen); got != row.want {
				t.Fatalf("retired %q -> menu %s: opened %s, want %s", row.retired, name, got, row.want)
			}
			if r.nav.Active() != row.ws {
				t.Errorf("opening %s left the sidebar on workspace %q, want %q", name, r.nav.Active(), row.ws)
			}
		})
	}
}

// TestPhase3_ThreeFacilitiesSurfacesKeepTheirMenuEntry covers the four retired
// globals whose door is the Facilities cursor menu rather than a sidebar child
// (C check-ins, B maker boxes, K checklists, T thermostats). The menu predates
// this bead — the point here is that nothing about the strip broke it.
func TestPhase3_FacilitiesSurfacesKeepTheirMenuEntry(t *testing.T) {
	cases := []struct {
		retired string
		hotkey  string
		want    string
	}{
		{"C", "c", "*tui.LocationCheckinsScreen"},
		{"B", "b", "*tui.MakerBoxesScreen"},
		{"K", "K", "*tui.ChecklistsScreen"},
		{"T", "t", "*tui.ThermostatListScreen"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			r := openFromMenu(t, newTestRoot(NewWelcomeScreen()), WSFacilities, "")
			if _, ok := r.screen.(*FacilitiesScreen); !ok {
				t.Fatalf("the Facilities row opened %T, want the facilities menu", r.screen)
			}
			// The menu is a screen, so its own entry key reaches it by
			// fall-through and the SwitchScreenMsg it emits does the opening.
			r = press(t, r, tc.hotkey)
			next, _ := r.Update(r.screen.(*FacilitiesScreen).items[facilitiesIndexOf(t, r.screen.(*FacilitiesScreen), tc.hotkey)].switchMsg(r.deps))
			after := next.(Root)
			if got := fmt.Sprintf("%T", after.screen); got != tc.want {
				t.Fatalf("retired %q -> Facilities menu %q: opened %s, want %s", tc.retired, tc.hotkey, got, tc.want)
			}
		})
	}
}

// facilitiesIndexOf finds a menu entry by its letter, failing loudly if the
// entry was renamed or re-lettered out from under the enumeration above.
func facilitiesIndexOf(t *testing.T, s *FacilitiesScreen, hotkey string) int {
	t.Helper()
	for i, it := range s.items {
		if string(it.hotkey) == hotkey {
			return i
		}
	}
	t.Fatalf("the Facilities menu has no entry %q", hotkey)
	return -1
}

// switchMsg rebuilds the SwitchScreenMsg an entry emits, so the test can resolve
// it without a live tea program to run the returned Cmd.
func (it facilitiesItem) switchMsg(deps Deps) SwitchScreenMsg {
	screen, ws := it.build(deps)
	return SwitchScreenMsg{Workspace: ws, Screen: screen}
}

// ---------------------------------------------------------------------------
// No letter navigates.
// ---------------------------------------------------------------------------

// TestPhase3_NoLetterOrDigitNavigatesFromWelcome presses every letter, every
// digit, and the two punctuation keys the root used to own, and demands the
// screen never changes. The welcome screen claims no key and takes no raw input,
// so anything that moves came from the root — which is exactly what phase 3
// removed.
func TestPhase3_NoLetterOrDigitNavigatesFromWelcome(t *testing.T) {
	keys := []string{}
	for c := 'a'; c <= 'z'; c++ {
		keys = append(keys, string(c))
	}
	for c := 'A'; c <= 'Z'; c++ {
		keys = append(keys, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		keys = append(keys, string(c))
	}
	keys = append(keys, "/", "?", "[", "]", " ")

	for _, key := range keys {
		home := NewWelcomeScreen()
		r := newTestRoot(home)
		after := press(t, r, key)
		if after.screen != Screen(home) {
			t.Errorf("%q navigated to %T — no bare letter, digit or punctuation may be a global accelerator", key, after.screen)
		}
		if after.nav.Focused() {
			t.Errorf("%q moved the keyboard into the sidebar; only tab does that", key)
		}
	}
}

// TestPhase3_QuitIsOnlyCtrlCAndCtrlQ: `q` used to quit from the welcome screen.
// It is a letter, so it went; ctrl+c and ctrl+q are the survivors and they work
// from anywhere, not just home.
func TestPhase3_QuitIsOnlyCtrlCAndCtrlQ(t *testing.T) {
	home := NewWelcomeScreen()
	r := newTestRoot(home)
	if _, cmd := r.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd != nil {
		t.Error("bare 'q' still produced a command on the welcome screen; it must be inert")
	}
	for _, key := range []tea.KeyType{tea.KeyCtrlC, tea.KeyCtrlQ} {
		if _, cmd := newTestRoot(home).Update(tea.KeyMsg{Type: key}); cmd == nil {
			t.Errorf("%v produced no command; it must still quit", key)
		}
	}
}

// TestPhase3_TabIsTheOnlyDoorIntoTheSidebar guards the one key the whole model
// now rests on, including that a screen mid-entry keeps it: a form owns tab for
// its own field movement, so the root must not take it away.
func TestPhase3_TabIsTheOnlyDoorIntoTheSidebar(t *testing.T) {
	r := press(t, newTestRoot(NewWelcomeScreen()), "tab")
	if !r.nav.Focused() {
		t.Fatal("tab did not focus the sidebar")
	}
	r = press(t, r, "tab")
	if r.nav.Focused() {
		t.Error("tab from inside the menu did not hand the keyboard back")
	}
	r = press(t, press(t, r, "tab"), "esc")
	if r.nav.Focused() {
		t.Error("esc from inside the menu did not hand the keyboard back")
	}

	// A raw-input screen keeps tab. The PO-create form uses it to move between
	// line fields; losing it there would be a regression the sidebar caused.
	form := NewPurchaseOrderCreateScreen(Deps{})
	rf := press(t, newTestRoot(form), "tab")
	if rf.nav.Focused() {
		t.Error("tab focused the sidebar from inside a form; a raw-input screen owns its own tab")
	}
}

// TestPhase3_SearchStaysOnCtrlK: ctrl+k survives (it is modifier-based, not a
// letter), bare `/` does not — `/` is a character a screen may want, and the
// list screens' own filter uses it.
func TestPhase3_SearchStaysOnCtrlK(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	next, _ := r.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if _, ok := next.(Root).screen.(*SearchPalette); !ok {
		t.Fatalf("ctrl+k opened %T, want the search palette", next.(Root).screen)
	}
	// And from inside the menu, without making the operator tab out first.
	r = press(t, newTestRoot(NewWelcomeScreen()), "tab")
	next, _ = r.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	after := next.(Root)
	if _, ok := after.screen.(*SearchPalette); !ok {
		t.Fatalf("ctrl+k from the menu opened %T, want the search palette", after.screen)
	}
	if after.nav.Focused() {
		t.Error("opening search from the menu left the keyboard in the sidebar")
	}
}

// ---------------------------------------------------------------------------
// The sidebar menu itself.
// ---------------------------------------------------------------------------

// TestNav_ArrowsWalkTheTree: down steps into the surfaces of the workspace the
// cursor is on, left/right skip whole workspaces, home/end reach the edges.
func TestNav_ArrowsWalkTheTree(t *testing.T) {
	r := press(t, newTestRoot(NewWelcomeScreen()), "tab")
	if r.nav.cursorWS != WSScan || r.nav.cursorSurface != -1 {
		t.Fatalf("focus started at %q/%d, want the active workspace's own row", r.nav.cursorWS, r.nav.cursorSurface)
	}

	// Right walks workspace to workspace without visiting surfaces.
	r = press(t, r, "right")
	r = press(t, r, "right")
	if r.nav.cursorWS != WSInventory || r.nav.cursorSurface != -1 {
		t.Fatalf("two rights landed on %q/%d, want the Inventory row", r.nav.cursorWS, r.nav.cursorSurface)
	}

	// Down steps into that workspace's surfaces, in order.
	for i, sf := range workspaceSurfaces(WSInventory) {
		r = press(t, r, "down")
		if r.nav.cursorSurface != i {
			t.Fatalf("down %d landed on surface %d, want %d (%q)", i+1, r.nav.cursorSurface, i, sf.label)
		}
	}
	// One more down leaves the workspace entirely, onto the next one's row.
	r = press(t, r, "down")
	if r.nav.cursorWS != WSPurchasing || r.nav.cursorSurface != -1 {
		t.Fatalf("down past the last surface landed on %q/%d, want the Purchasing row", r.nav.cursorWS, r.nav.cursorSurface)
	}

	// Left from a surface row goes back to its own workspace row first.
	r = press(t, r, "down")
	if r.nav.cursorSurface != 0 {
		t.Fatalf("down landed on surface %d, want the first Purchasing surface", r.nav.cursorSurface)
	}
	r = press(t, r, "left")
	if r.nav.cursorWS != WSPurchasing || r.nav.cursorSurface != -1 {
		t.Fatalf("left from a surface landed on %q/%d, want its own workspace row", r.nav.cursorWS, r.nav.cursorSurface)
	}

	// The edges clamp; they never wrap.
	r = press(t, r, "home")
	if r.nav.cursorWS != WSScan {
		t.Fatalf("home landed on %q, want the first workspace", r.nav.cursorWS)
	}
	r = press(t, r, "up")
	if r.nav.cursorWS != WSScan {
		t.Errorf("up at the top wrapped to %q; the menu must clamp", r.nav.cursorWS)
	}
	r = press(t, r, "end")
	last := Workspaces()[len(Workspaces())-1].Key
	wantSurface := len(workspaceSurfaces(last)) - 1
	if r.nav.cursorWS != last || r.nav.cursorSurface != wantSurface {
		t.Fatalf("end landed on %q/%d, want %q/%d (the last surface of the last workspace)",
			r.nav.cursorWS, r.nav.cursorSurface, last, wantSurface)
	}
	r = press(t, r, "down")
	if r.nav.cursorWS != last || r.nav.cursorSurface != wantSurface {
		t.Errorf("down at the bottom wrapped to %q/%d; the menu must clamp", r.nav.cursorWS, r.nav.cursorSurface)
	}
}

// TestNav_SurfacesShowOnlyUnderTheOneWorkspaceInPlay: the tree shows one
// workspace's surfaces at a time — the cursor's while the sidebar has focus, the
// ACTIVE one when it does not, so a blurred sidebar still says where you are.
func TestNav_SurfacesShowOnlyUnderTheOneWorkspaceInPlay(t *testing.T) {
	n := NewNav()
	n.SetActive(WSForgeKey)

	labels := func() []string {
		out := []string{}
		for _, row := range n.rows() {
			if row.child {
				out = append(out, row.label)
			}
		}
		return out
	}

	got := labels()
	if len(got) != len(workspaceSurfaces(WSForgeKey)) {
		t.Fatalf("blurred sidebar showed %d surfaces %v, want ForgeKey's %d",
			len(got), got, len(workspaceSurfaces(WSForgeKey)))
	}

	// Focus, then move to a different workspace: the shown surfaces follow the
	// cursor, and only ever one workspace's worth.
	n.Focus()
	n.cursorWS, n.cursorSurface = WSInventory, -1
	got = labels()
	if len(got) != len(workspaceSurfaces(WSInventory)) {
		t.Fatalf("focused sidebar showed %v, want Inventory's %d surfaces", got, len(workspaceSurfaces(WSInventory)))
	}
	for _, l := range got {
		for _, other := range workspaceSurfaces(WSForgeKey) {
			if l == other.label {
				t.Errorf("surface %q from another workspace is still on the tree", l)
			}
		}
	}
}

// TestNav_CursorRowIsMarkedOnlyWhileFocused pins the focus TREATMENT. lipgloss
// renders plain in a test binary, so this asserts the decision (rowKind) rather
// than the rendered bytes — the same move sweep E made for the grant screen's
// dimmed picker row.
func TestNav_CursorRowIsMarkedOnlyWhileFocused(t *testing.T) {
	n := NewNav()
	n.SetActive(WSInventory)
	rows := n.rows()
	cursor := n.cursorIndex(rows)

	if k := rowKind(rows[cursor], true, false, n.active); k == navRowCursor {
		t.Error("a blurred sidebar marked a cursor row; the reverse-video treatment must mean the keys go HERE")
	} else if k != navRowActive {
		t.Errorf("the active workspace row reads as %v while blurred, want navRowActive", k)
	}

	n.Focus()
	rows = n.rows()
	cursor = n.cursorIndex(rows)
	if k := rowKind(rows[cursor], true, true, n.active); k != navRowCursor {
		t.Errorf("the focused cursor row reads as %v, want navRowCursor", k)
	}
	// And it out-ranks the active highlight, which is on the same row here.
	if navRowStyle(navRowCursor).GetReverse() != true {
		t.Error("the cursor style is not reverse-video; it must match the columnar sheet's focused field")
	}
	if navRowStyle(navRowActive).GetReverse() {
		t.Error("the active-workspace style is reverse-video too; the two readings must stay distinct")
	}
}

// TestNav_StaffOnlyRowsStayDimButOpenable locks today's behavior rather than
// inventing a new denial: the sidebar has always dimmed Facilities/ForgeKey for
// a non-staff session while still opening them (the backend is the real gate).
func TestNav_StaffOnlyRowsStayDimButOpenable(t *testing.T) {
	n := NewNav()
	for _, row := range n.rows() {
		staffOnly := row.ws == WSFacilities || row.ws == WSForgeKey
		if row.disabled != staffOnly {
			t.Errorf("row %q disabled=%v, want %v for a non-staff session", row.label, row.disabled, staffOnly)
		}
	}
	r := openFromMenu(t, newTestRoot(NewWelcomeScreen()), WSFacilities, "")
	if _, ok := r.screen.(*FacilitiesScreen); !ok {
		t.Fatalf("a dimmed staff row opened %T; dimming is a heads-up, not a lock", r.screen)
	}
}

// TestNav_WindowsToThePaneAndKeepsTheCursorVisible: the tree is taller than a
// short terminal's sidebar, so it windows — and the row the keys are on has to
// be inside the window, or the operator is steering something they can't see.
func TestNav_WindowsToThePaneAndKeepsTheCursorVisible(t *testing.T) {
	n := NewNav()
	n.SetWidth(navColumnWidth)
	n.Focus()
	// Park the cursor on ForgeKey's last surface — the deepest row in the tree.
	n.cursorWS = WSForgeKey
	n.cursorSurface = len(workspaceSurfaces(WSForgeKey)) - 1
	label := workspaceSurfaces(WSForgeKey)[n.cursorSurface].label

	for _, height := range []int{8, 12, 18, 30} {
		view := n.View(height)
		lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
		if len(lines) > height {
			t.Errorf("height %d: sidebar rendered %d lines; a taller pane than we were given pushes the frame off screen", height, len(lines))
		}
		if !strings.Contains(view, label) {
			t.Errorf("height %d: the cursor row %q is outside the window:\n%s", height, label, view)
		}
		if width := lipgloss.Width(view); width > navColumnWidth+1 {
			t.Errorf("height %d: sidebar rendered %d columns, want at most %d (+1 border)", height, width, navColumnWidth)
		}
	}
}

// TestNav_EveryRowFitsTheColumn: a label wider than the pane wraps, which adds a
// row and pushes the frame down. The tree's labels are fixed, so this is a
// cheap guard on adding a long one later.
func TestNav_EveryRowFitsTheColumn(t *testing.T) {
	// Room for the text inside the pane: the sidebar's own Padding(0,1) and the
	// item style's Padding(0,1), off a navColumnWidth content box.
	avail := navColumnWidth - 2 - 2
	for _, item := range Workspaces() {
		if w := lipgloss.Width(item.Label); w > avail {
			t.Errorf("workspace label %q is %d columns, over the %d the pane has", item.Label, w, avail)
		}
		for _, sf := range workspaceSurfaces(item.Key) {
			if w := lipgloss.Width(navChildIndent + sf.label); w > avail {
				t.Errorf("surface label %q indents to %d columns, over the %d the pane has", sf.label, w, avail)
			}
		}
	}
}

// TestNav_WindowRangeNeverOverflowsTheBudget exercises navWindowRange directly
// across every cursor position and pane height: the drawn lines (rows plus a
// "⋯" for each clipped end) must fit, and the cursor must be among them.
func TestNav_WindowRangeNeverOverflowsTheBudget(t *testing.T) {
	for _, total := range []int{1, 5, 11, 18, 40} {
		for avail := 1; avail <= total+3; avail++ {
			for cursor := 0; cursor < total; cursor++ {
				start, end, markers := navWindowRange(total, cursor, avail)
				drawn := end - start
				if markers && start > 0 {
					drawn++
				}
				if markers && end < total {
					drawn++
				}
				if drawn > avail && total > avail {
					t.Fatalf("total=%d avail=%d cursor=%d: drew %d lines", total, avail, cursor, drawn)
				}
				if cursor < start || cursor >= end {
					t.Fatalf("total=%d avail=%d cursor=%d: window [%d,%d) excludes the cursor", total, avail, cursor, start, end)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The discovery surfaces have to tell the truth.
// ---------------------------------------------------------------------------

// TestPhase3_WelcomeNamesOnlyKeysThatWork: the welcome screen is the app's
// standing statement of the key model, and phase 3 makes an out-of-date one a
// real bug — it is the only place the retained keys are written down.
func TestPhase3_WelcomeNamesOnlyKeysThatWork(t *testing.T) {
	view := (&WelcomeScreen{}).View()
	for _, want := range []string{"tab", "enter", "esc", "ctrl+e", "ctrl+k", "ctrl+c"} {
		if !strings.Contains(view, want) {
			t.Errorf("the welcome screen does not name %q, which is part of the key model", want)
		}
	}
	// Nothing that reads as "press this letter to go somewhere" may survive.
	for _, gone := range []string{
		"[0] Scan", "[1] Dashboard", "[9] ForgeKey", "[s] Settings",
		"— your member profile", "— notifications", "— ForgeKey authorizations",
		"Press `q`", "Ctrl+K or /",
	} {
		if strings.Contains(view, gone) {
			t.Errorf("the welcome screen still advertises %q, which no longer works", gone)
		}
	}
}

// TestPhase3_WelcomeFitsAnEightyByTwentyFourPane: Root.View clamps with
// clampToBox, which TRUNCATES in BOTH directions rather than wrapping or
// paging, and the welcome screen does not scroll. So a line written at 110
// columns loses its right-hand half, and a 35-line screen loses its bottom
// third — which is exactly where the old one kept "ctrl+c quits". Sweep C's
// width guard (TestJDESweepC_RowsFitTheBody) with the height half added,
// pointed at the one screen that states the key model.
func TestPhase3_WelcomeFitsAnEightyByTwentyFourPane(t *testing.T) {
	availW, availH := screenBodyWidth(80), screenBodyHeight(24)
	lines := strings.Split((&WelcomeScreen{}).View(), "\n")
	if len(lines) > availH {
		t.Errorf("the welcome screen is %d lines, over the %d an 80x24 pane has — the tail is cut with nothing to say so",
			len(lines), availH)
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w > availW {
			t.Errorf("welcome line %d is %d columns, over the %d an 80-column pane has — it will be cut: %q",
				i+1, w, availW, line)
		}
	}
}

// TestPhase3_StatusHintsNameTheLiveKeys: the bar's right-hand hint is the other
// standing surface. It used to say "q quit · tab nav · / search" — of which
// only tab still worked, and only because this bead made it so.
func TestPhase3_StatusHintsNameTheLiveKeys(t *testing.T) {
	sb := NewStatusBar()
	sb.SetWidth(120)
	visible := stripStatusANSI(sb.View())
	for _, want := range []string{"tab", "ctrl+k", "esc"} {
		if !strings.Contains(visible, want) {
			t.Errorf("the status bar hints do not name %q; got %q", want, visible)
		}
	}
	for _, gone := range []string{"q quit", "/ search"} {
		if strings.Contains(visible, gone) {
			t.Errorf("the status bar still advertises %q; got %q", gone, visible)
		}
	}
}

// TestPhase3_FocusHintArrivesWithTheFocus: the sidebar is 24 columns wide with
// no room for a help line, so the keys it takes are flashed the moment it takes
// them. A key nothing on screen names is a key nobody finds.
func TestPhase3_FocusHintArrivesWithTheFocus(t *testing.T) {
	r := newTestRoot(NewWelcomeScreen())
	next, cmd := r.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !next.(Root).nav.Focused() {
		t.Fatal("tab did not focus the sidebar")
	}
	if cmd == nil {
		t.Fatal("tab produced no command; the focus hint must ride along with the focus")
	}
	msg, ok := cmd().(StatusMsg)
	if !ok {
		t.Fatalf("tab's command produced %T, want a StatusMsg carrying the hint", cmd())
	}
	for _, want := range []string{"↑↓", "←→", "enter", "esc"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("the focus hint %q does not name %q", msg.Text, want)
		}
	}
}
