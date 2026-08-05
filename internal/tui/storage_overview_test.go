package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

func overviewKey(t *testing.T, s *StorageOverviewScreen, key string) *StorageOverviewScreen {
	t.Helper()
	next, _ := s.Update(namedKey(key))
	out, ok := next.(*StorageOverviewScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *StorageOverviewScreen", next)
	}
	return out
}

func cell(code string, position int, letter, status, color, occupant string, active bool) *omsapi.StorageOverviewCell {
	return &omsapi.StorageOverviewCell{
		Code: code, SlotID: position, Position: position, Type: letter,
		Status: status, Color: color, Occupant: occupant, IsActive: active,
	}
}

// sampleOverview is Ian's example rack in miniature: a hole in the racking, a
// committee holding, a logistics holding, an expiring project and an expired
// one — plus a second rack to page to.
func sampleOverview() *omsapi.StorageOverview {
	return &omsapi.StorageOverview{
		Racks: []omsapi.StorageOverviewRack{
			{
				Rack: 1, Levels: []string{"C", "A"}, MaxPosition: 4,
				Rows: []omsapi.StorageOverviewRow{
					{Level: "C", Cells: []*omsapi.StorageOverviewCell{
						cell("1C1", 1, "C", "occupied", "", "Welding SIG", true),
						nil, // a hole in the racking
						cell("1C3", 3, "L", "occupied", "", "Dock crew", true),
						cell("1C4", 4, "", "empty", "", "", false), // retired
					}},
					{Level: "A", Cells: []*omsapi.StorageOverviewCell{
						cell("1A1", 1, "P", "expiring_soon", "yellow", "Ada Byron", true),
						cell("1A2", 2, "P", "expired", "red", "Grace Hopper", true),
						cell("1A3", 3, "P", "active", "", "Alan Turing", true),
						cell("1A4", 4, "", "empty", "", "", true),
					}},
				},
			},
			{
				Rack: 2, Levels: []string{"A"}, MaxPosition: 1,
				Rows: []omsapi.StorageOverviewRow{
					{Level: "A", Cells: []*omsapi.StorageOverviewCell{
						cell("2A1", 1, "E", "occupied", "", "Ana's CNC class", true),
					}},
				},
			},
		},
	}
}

func loadedOverview(t *testing.T) *StorageOverviewScreen {
	t.Helper()
	s := NewStorageOverviewScreen(Deps{})
	next, _ := s.Update(storageOverviewLoadedMsg{overview: sampleOverview()})
	out, ok := next.(*StorageOverviewScreen)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	// A realistic terminal, so the windowing code runs the same way it will in
	// front of an operator rather than falling back to its floors.
	next, _ = out.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return next.(*StorageOverviewScreen)
}

// gridLine pulls one rendered level row out of the view, with the styling
// stripped, so the assertions are about the PICTURE — which is the whole
// deliverable.
func gridLine(t *testing.T, view, level string) string {
	t.Helper()
	for _, line := range strings.Split(view, "\n") {
		plain := stripANSI(line)
		if strings.HasPrefix(plain, level) && len(plain) > 1 {
			return plain
		}
	}
	t.Fatalf("no grid row for level %q in:\n%s", level, view)
	return ""
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // the 'm'
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// TestStorageOverview_GridIsTheRack is the bead's core deliverable: levels high
// to low with the letter in the leading column, a type letter per slot, "." for
// an empty slot AND for a hole, and the position ruler above.
func TestStorageOverview_GridIsTheRack(t *testing.T) {
	view := loadedOverview(t).View()

	if got := gridLine(t, view, "C"); got != "CC.L." {
		t.Errorf("level C row = %q, want %q (committee, hole, logistics, retired-empty)", got, "CC.L.")
	}
	if got := gridLine(t, view, "A"); got != "APPP." {
		t.Errorf("level A row = %q, want %q", got, "APPP.")
	}

	plain := stripANSI(view)
	if !strings.Contains(plain, "\n 1234\n") {
		t.Errorf("the position ruler should sit above the grid, offset by the level column:\n%s", plain)
	}
	// High levels first — the payload is ordered that way and the render must
	// not re-sort it.
	if strings.Index(plain, "\nCC.L.") > strings.Index(plain, "\nAPPP.") {
		t.Errorf("level A rendered above level C — the rack reads top-down:\n%s", plain)
	}
	for _, want := range []string{"P=Project", "C=Committee", "L=Logistics", "E=Class"} {
		if !strings.Contains(plain, want) {
			t.Errorf("legend is missing %q:\n%s", want, plain)
		}
	}
}

// TestStorageOverview_ColorsComeFromTheBackend pins the one rule the screen must
// not reinvent: yellow/red are painted where the PAYLOAD says so, and nowhere
// else — a long-standing committee holding stays uncoloured on purpose.
func TestStorageOverview_ColorsComeFromTheBackend(t *testing.T) {
	yellow := storageGridStyles[omsapi.StorageOverviewColorYellow].Render("P")
	red := storageGridStyles[omsapi.StorageOverviewColorRed].Render("P")

	view := loadedOverview(t).View()
	if !strings.Contains(view, yellow) {
		t.Errorf("the expiring-soon project should be painted yellow")
	}
	if !strings.Contains(view, red) {
		t.Errorf("the expired project should be painted red")
	}

	for _, tc := range []struct {
		name string
		cell *omsapi.StorageOverviewCell
		want string
	}{
		{"expiring", cell("1A1", 1, "P", "expiring_soon", "yellow", "", true), "yellow"},
		{"expired", cell("1A2", 2, "P", "expired", "red", "", true), "red"},
		{"active project", cell("1A3", 3, "P", "active", "", "", true), "held"},
		{"committee", cell("1C1", 1, "C", "occupied", "", "", true), "held"},
		{"logistics", cell("1C3", 3, "L", "occupied", "", "", true), "held"},
		{"class", cell("2A1", 1, "E", "occupied", "", "", true), "held"},
		{"empty", cell("1A4", 4, "", "empty", "", "", true), "empty"},
		{"retired", cell("1C4", 4, "", "empty", "", "", false), "retired"},
		{"hole", nil, "hole"},
	} {
		if got := storageGridCellClass(tc.cell); got != tc.want {
			t.Errorf("%s cell class = %q, want %q", tc.name, got, tc.want)
		}
	}

	// A colour the backend grows later must still stand out rather than
	// silently rendering as an ordinary cell.
	unknown := cell("1A1", 1, "P", "on_fire", "orange", "", true)
	if got := storageGridCellClass(unknown); got == "held" || got == "empty" {
		t.Errorf("an unrecognized colour rendered as an ordinary cell (%q)", got)
	}
}

// A retired slot is empty but NOT available. It keeps the "." the spec asks for,
// so the fact has to survive somewhere the warden will actually read it.
func TestStorageOverview_RetiredSlotIsNamed(t *testing.T) {
	s := loadedOverview(t)
	s.level, s.position = "C", 4
	if got := stripANSI(s.cursorLine()); !strings.Contains(got, "out of service") {
		t.Errorf("cursor line for a retired slot = %q, want it to say out of service", got)
	}
	if class := storageGridCellClass(s.cell()); class != "retired" {
		t.Errorf("retired cell class = %q", class)
	}
}

func TestStorageOverview_PositionRulers(t *testing.T) {
	if got := storageGridPositionRuler(0, 12); got != " 123456789012" {
		t.Errorf("ruler = %q", got)
	}
	// Windowed: the digits keep meaning the real position, not the column.
	if got := storageGridPositionRuler(8, 4); got != " 9012" {
		t.Errorf("windowed ruler = %q, want the real positions 9-12", got)
	}
	// No multiple of ten on screen → no second ruler line at all.
	if got := storageGridTensRuler(0, 9); got != "" {
		t.Errorf("tens ruler for a 9-wide rack = %q, want none", got)
	}
	// The label ENDS on its own column: "1" under position 10, "2" under 20.
	tens := storageGridTensRuler(0, 21)
	if len(tens) < 21 || tens[10] != '1' || tens[20] != '2' {
		t.Errorf("tens ruler = %q, want 1 at position 10 and 2 at position 20", tens)
	}
	// A two-digit label still ends on its column.
	if tens := storageGridTensRuler(95, 6); !strings.HasSuffix(strings.TrimRight(tens, " "), "10") {
		t.Errorf("tens ruler near 100 = %q, want it to end with 10", tens)
	}
}

// TestStorageOverview_CursorAndPaging covers moving around the grid and paging
// racks — h/l along a level, j/k between levels (down the printed picture),
// pgup/pgdn between racks (tab belongs to the sidebar menu since phase 3).
func TestStorageOverview_CursorAndPaging(t *testing.T) {
	s := loadedOverview(t)
	if s.level != "C" || s.position != 1 {
		t.Fatalf("the cursor should start top-left, got %s%d", s.level, s.position)
	}

	s = overviewKey(t, s, "l")
	if s.position != 2 {
		t.Errorf("l should move right, got position %d", s.position)
	}
	s = overviewKey(t, s, "h")
	if s.position != 1 {
		t.Errorf("h should move back left, got position %d", s.position)
	}
	// j moves DOWN the picture, which is toward the ground.
	s = overviewKey(t, s, "j")
	if s.level != "A" {
		t.Errorf("j should move to the level below, got %q", s.level)
	}
	s = overviewKey(t, s, "k")
	if s.level != "C" {
		t.Errorf("k should move back up, got %q", s.level)
	}

	// The edges hold rather than wrapping onto another level's slot.
	s = overviewKey(t, s, "k")
	if s.level != "C" {
		t.Errorf("k at the top level should hold, got %q", s.level)
	}
	s = overviewKey(t, s, "h")
	if s.position != 1 {
		t.Errorf("h at position 1 should hold, got %d", s.position)
	}
	s = overviewKey(t, s, "end")
	if s.position != 4 {
		t.Errorf("end should go to the last position, got %d", s.position)
	}
	s = overviewKey(t, s, "l")
	if s.position != 4 {
		t.Errorf("l past the last position should hold, got %d", s.position)
	}

	// Paging racks lands top-left of the new rack: a level letter carried over
	// from another rack means nothing there.
	s = overviewKey(t, s, "pgdown")
	if s.rackNumber != 2 || s.level != "A" || s.position != 1 {
		t.Errorf("pgdown landed at rack %d %s%d, want rack 2 A1", s.rackNumber, s.level, s.position)
	}
	s = overviewKey(t, s, "pgdown")
	if s.rackNumber != 1 {
		t.Errorf("pgdown should wrap back to rack 1, got %d", s.rackNumber)
	}
	s = overviewKey(t, s, "pgup")
	if s.rackNumber != 2 {
		t.Errorf("pgup should page backwards, got rack %d", s.rackNumber)
	}
}

// TestStorageOverview_CursorSurvivesAReload is the stale-index guard: the cursor
// is an ADDRESS, so a refetch that drops a level (or narrows a row) has to move
// it somewhere real rather than leaving it pointing at nothing.
func TestStorageOverview_CursorSurvivesAReload(t *testing.T) {
	s := loadedOverview(t)
	s.level, s.position = "A", 4

	next, _ := s.Update(storageOverviewLoadedMsg{overview: sampleOverview()})
	s = next.(*StorageOverviewScreen)
	if s.level != "A" || s.position != 4 {
		t.Errorf("an unchanged rack moved the cursor to %s%d", s.level, s.position)
	}

	// Level A retired away and the rack got narrower.
	shrunk := &omsapi.StorageOverview{Racks: []omsapi.StorageOverviewRack{{
		Rack: 1, Levels: []string{"C"}, MaxPosition: 2,
		Rows: []omsapi.StorageOverviewRow{{Level: "C", Cells: []*omsapi.StorageOverviewCell{
			cell("1C1", 1, "C", "occupied", "", "Welding SIG", true), nil,
		}}},
	}}}
	next, _ = s.Update(storageOverviewLoadedMsg{overview: shrunk})
	s = next.(*StorageOverviewScreen)
	if s.level != "C" || s.position != 2 {
		t.Errorf("cursor = %s%d, want it clamped onto the rack that came back", s.level, s.position)
	}
	if s.cell() != nil {
		t.Errorf("position 2 is a hole; the cursor may sit on it but there is no cell")
	}
}

// TestStorageOverview_AssignRefusesWhatTheBackendWould keeps the warden from
// spending a round trip on a guaranteed refusal, and names the remedy each time.
func TestStorageOverview_AssignRefusesWhatTheBackendWould(t *testing.T) {
	for _, tc := range []struct {
		name       string
		level      string
		position   int
		wantOpens  bool
		wantStatus string
	}{
		{name: "free slot", level: "A", position: 4, wantOpens: true},
		{name: "live stint", level: "A", position: 1, wantStatus: "stint"},
		{name: "already assigned", level: "C", position: 1, wantStatus: "already assigned"},
		{name: "retired", level: "C", position: 4, wantStatus: "out of service"},
		{name: "hole", level: "C", position: 2, wantStatus: "no slot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := loadedOverview(t)
			s.level, s.position = tc.level, tc.position
			_, cmd := s.Update(namedKey("a"))
			if cmd == nil {
				t.Fatal("a produced no command at all")
			}
			switch msg := cmd().(type) {
			case SwitchScreenMsg:
				if !tc.wantOpens {
					t.Fatalf("a opened %T on a slot that can't be assigned", msg.Screen)
				}
				form, ok := msg.Screen.(*StorageAssignFormScreen)
				if !ok {
					t.Fatalf("a opened %T, want the assign form", msg.Screen)
				}
				if form.code != "1A4" {
					t.Errorf("the form is aimed at %q, want 1A4", form.code)
				}
			case StatusMsg:
				if tc.wantOpens {
					t.Fatalf("a refused a free slot: %q", msg.Text)
				}
				if !strings.Contains(msg.Text, tc.wantStatus) {
					t.Errorf("message %q should mention %q", msg.Text, tc.wantStatus)
				}
			default:
				t.Fatalf("a produced %T", msg)
			}
		})
	}
}

// The assign form comes back to the cell the warden was standing on, not to the
// top of rack one.
func TestStorageOverview_AssignReturnsToTheSameCell(t *testing.T) {
	s := loadedOverview(t)
	s.rackNumber, s.level, s.position = 1, "A", 4

	back := s.backHere()(Deps{})
	grid, ok := back.(*StorageOverviewScreen)
	if !ok {
		t.Fatalf("back opened %T, want the overview", back)
	}
	if grid.rackNumber != 1 || grid.level != "A" || grid.position != 4 {
		t.Errorf("back landed on rack %d %s%d, want 1 A4", grid.rackNumber, grid.level, grid.position)
	}
}

// TestStorageOverview_ReleaseOnlyForAssignments: a project stint is the member's
// own lifecycle and is NOT ended from the grid.
func TestStorageOverview_ReleaseOnlyForAssignments(t *testing.T) {
	s := loadedOverview(t)
	s.level, s.position = "C", 1
	s = overviewKey(t, s, "x")
	if !s.confirmingRelease {
		t.Fatal("x on a committee cell should open the release confirm")
	}
	if !s.WantsRawInput() {
		t.Error("the confirm must claim raw input so n=no doesn't open notifications")
	}
	if got := stripANSI(s.releaseConfirmText()); !strings.Contains(got, "Welding SIG") {
		t.Errorf("confirm = %q, want it to name the holder", got)
	}
	s = overviewKey(t, s, "n")
	if s.confirmingRelease {
		t.Error("n should cancel the confirm")
	}

	s.level, s.position = "A", 1
	_, cmd := s.Update(namedKey("x"))
	if cmd == nil {
		t.Fatal("x on a project cell produced no message")
	}
	if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "stint") {
		t.Errorf("x on a project cell should point at the stint, got %#v", cmd())
	}
	if s.confirmingRelease {
		t.Error("x on a project cell must not open the release confirm")
	}

	s.level, s.position = "A", 4 // free
	_, cmd = s.Update(namedKey("x"))
	if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "not assigned") {
		t.Errorf("x on a free cell should say so, got %#v", cmd())
	}
}

// A grid that says C but an assignment that is already gone must not read as a
// success — it reloads and says why.
func TestStorageOverview_ReleaseHandlesAVanishedHolding(t *testing.T) {
	s := loadedOverview(t)
	s.level, s.position = "C", 1
	s.confirmingRelease, s.releasing = true, true

	next, cmd := s.Update(storageOverviewReleasedMsg{code: "1C1", notHeld: true})
	s = next.(*StorageOverviewScreen)
	if s.confirmingRelease || s.releasing {
		t.Error("the confirm should close whatever the outcome")
	}
	if !s.loading {
		t.Error("a vanished holding should trigger a reload")
	}
	if cmd == nil {
		t.Fatal("no command")
	}
	if got := statusTextFrom(t, cmd); !strings.Contains(got, "no live holding") {
		t.Errorf("status = %q", got)
	}
}

// statusTextFrom runs a (possibly batched) command and returns the first status
// message's text.
func statusTextFrom(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if sub == nil {
				continue
			}
			if status, ok := sub().(StatusMsg); ok {
				return status.Text
			}
		}
		return ""
	}
	if status, ok := msg.(StatusMsg); ok {
		return status.Text
	}
	return ""
}

// TestStorageOverview_EnterOpensTheSlot — the grid's one character can't carry
// everything, so enter walks to the slot (and from there to the stint).
func TestStorageOverview_EnterOpensTheSlot(t *testing.T) {
	s := loadedOverview(t)
	s.level, s.position = "A", 2
	_, cmd := s.Update(namedKey("enter"))
	if cmd == nil {
		t.Fatal("enter produced no command")
	}
	msg, ok := cmd().(SwitchScreenMsg)
	if !ok {
		t.Fatalf("enter produced %T, want a screen switch", cmd())
	}
	detail, ok := msg.Screen.(*StorageSlotDetailScreen)
	if !ok {
		t.Fatalf("enter opened %T, want the slot detail", msg.Screen)
	}
	if detail.code != "1A2" {
		t.Errorf("opened slot %q, want 1A2", detail.code)
	}

	// A hole has nothing to open.
	s.level, s.position = "C", 2
	_, cmd = s.Update(namedKey("enter"))
	if msg, ok := cmd().(StatusMsg); !ok || !strings.Contains(msg.Text, "no slot") {
		t.Errorf("enter on a hole should say there is no slot, got %#v", cmd())
	}
}

// TestStorageOverview_HandlesKey pins the claim. `a` and `l` were claimed
// because they collided with globals; those globals are gone, but the claim
// stays as the screen naming the keys it owns — and `tab` must NOT be claimed,
// or the sidebar menu would be unreachable from this screen.
func TestStorageOverview_HandlesKey(t *testing.T) {
	s := loadedOverview(t)
	for _, k := range []string{"a", "l"} {
		if !s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = false, want true (the screen owns this key)", k)
		}
	}
	for _, k := range []string{"h", "j", "k", "x", "r", "enter"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true — it is globally free and should reach us by fall-through", k)
		}
	}
	if s.HandlesKey("tab") {
		t.Error("HandlesKey(\"tab\") = true — tab opens the sidebar menu and no screen may take it")
	}
	// While the confirm is up, raw input already routes every key here.
	s.confirmingRelease = true
	if s.HandlesKey("a") {
		t.Error("HandlesKey should defer to WantsRawInput while the confirm is open")
	}
}

// TestStorageOverview_AssignReachesTheGrid drives `a` through the ROOT, where
// the real hazard lives: the global switch would otherwise open Authorizations
// and the assign key would be permanently dead.
func TestStorageOverview_AssignReachesTheGrid(t *testing.T) {
	s := loadedOverview(t)
	s.level, s.position = "A", 4 // a free slot, so `a` opens the form
	r := press(t, newTestRoot(s), "a")
	if r.screen != Screen(s) {
		t.Fatalf("global a=authorizations shadowed the assign key: screen is now %T", r.screen)
	}
}

// TestStorageOverview_EmptyRacking distinguishes "no racking here" from a
// failure, and points at where a rack is generated.
func TestStorageOverview_EmptyRacking(t *testing.T) {
	s := NewStorageOverviewScreen(Deps{})
	next, _ := s.Update(storageOverviewLoadedMsg{overview: &omsapi.StorageOverview{}})
	out := stripANSI(next.(*StorageOverviewScreen).View())
	if !strings.Contains(out, "No racking yet") {
		t.Errorf("view = %q", out)
	}
}

// TestStorageOverview_WideRackWindows keeps a rack wider than the pane usable:
// the columns follow the cursor and the header says which slice is showing,
// rather than the row being silently truncated at the box edge.
func TestStorageOverview_WideRackWindows(t *testing.T) {
	cells := make([]*omsapi.StorageOverviewCell, 60)
	for i := range cells {
		cells[i] = cell("1A"+itoa(i+1), i+1, "", "empty", "", "", true)
	}
	wide := &omsapi.StorageOverview{Racks: []omsapi.StorageOverviewRack{{
		Rack: 1, Levels: []string{"A"}, MaxPosition: 60,
		Rows: []omsapi.StorageOverviewRow{{Level: "A", Cells: cells}},
	}}}

	s := NewStorageOverviewScreen(Deps{})
	next, _ := s.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	next, _ = next.(*StorageOverviewScreen).Update(storageOverviewLoadedMsg{overview: wide})
	s = next.(*StorageOverviewScreen)

	cols := s.gridColumns()
	if cols >= 60 {
		t.Skipf("pane is wide enough for the whole rack (%d columns); nothing to window", cols)
	}
	row := gridLine(t, s.View(), "A")
	if len(row) != cols+1 {
		t.Errorf("row is %d chars wide, want %d (the level letter + the window)", len(row), cols+1)
	}
	if !strings.Contains(stripANSI(s.View()), "of 60") {
		t.Errorf("the header should say which positions are showing:\n%s", stripANSI(s.View()))
	}

	// Walking right past the window's edge scrolls it.
	for i := 0; i < cols+2; i++ {
		s = overviewKey(t, s, "l")
	}
	if s.colStart == 0 {
		t.Errorf("the window never followed the cursor (position %d, colStart %d)", s.position, s.colStart)
	}
	if s.position-1 < s.colStart || s.position-1 >= s.colStart+cols {
		t.Errorf("the cursor (position %d) fell outside the window [%d,%d)", s.position, s.colStart+1, s.colStart+cols+1)
	}
}
