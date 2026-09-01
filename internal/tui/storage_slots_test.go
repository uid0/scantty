package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// namedKey builds the real tea key type for the named keys these screens
// branch on, so a test exercises the same KeyMsg the terminal produces rather
// than a rune string that happens to stringify the same way.
func namedKey(key string) tea.KeyMsg {
	switch key {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	// The columnar forms' opener and the ←/→ a choice row cycles on (sc-6qsk).
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func slotKey(t *testing.T, s *StorageSlotsScreen, key string) *StorageSlotsScreen {
	t.Helper()
	msg := namedKey(key)
	next, _ := s.Update(msg)
	out, ok := next.(*StorageSlotsScreen)
	if !ok {
		t.Fatalf("Update returned %T, want *StorageSlotsScreen", next)
	}
	return out
}

func sampleSlots() []omsapi.StorageSlot {
	tag := 41
	group := 3
	return []omsapi.StorageSlot{
		{ID: 1, Code: "1A1", Rack: 1, Level: "A", Position: 1, IsActive: true, AprilTagID: &tag},
		{ID: 2, Code: "1A2", Rack: 1, Level: "A", Position: 2, IsActive: true, IsOccupied: true,
			CurrentStint: &omsapi.StorageSlotOccupant{
				StintID: "PS-AB23CDFG", Username: "alice", DisplayName: "Alice Smith", ProjectTitle: "CNC jig",
			}},
		{ID: 3, Code: "2Y1", Rack: 2, Level: "Y", Position: 1, RequiresPalletJack: true,
			OwningGroup: &group, OwningGroupName: "Woodshop"},
	}
}

func loadedSlotsScreen() *StorageSlotsScreen {
	s := NewStorageSlotsScreen(Deps{})
	s.terminalHeight = 40
	s.rows = sampleSlots()
	s.loading = false
	s.windowSize = 20
	return s
}

// TestStorageSlots_HandlesKey pins the sc-k7p claims: only the four keys that
// collide with globals are claimed, and nothing is claimed while an overlay is
// up (raw input already routes every key there).
func TestStorageSlots_HandlesKey(t *testing.T) {
	s := loadedSlotsScreen()
	for _, k := range []string{"n", "G", "f", "/"} {
		if !s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = false, want true (collides with a global)", k)
		}
	}
	for _, k := range []string{"E", "x", "b", "c", "p", "r", "enter", " ", "j", "k"} {
		if s.HandlesKey(k) {
			t.Errorf("HandlesKey(%q) = true, want false (reaches the screen via fallthrough)", k)
		}
	}
	s.confirmingDelete = true
	if s.HandlesKey("n") {
		t.Errorf("no key should be claimed while an overlay is up — WantsRawInput owns them")
	}
	if !s.WantsRawInput() {
		t.Errorf("a delete confirm must flip WantsRawInput so n/y/esc land here")
	}
}

// TestStorageSlots_FilterCycle pins that filters[0] is the unfiltered view (the
// landing screen shows everything) and that cycling re-fetches with the new
// params rather than filtering the rows in hand.
func TestStorageSlots_FilterCycle(t *testing.T) {
	s := loadedSlotsScreen()
	if len(s.query()) != 0 {
		t.Errorf("the first filter must be unfiltered, got %v", s.query())
	}

	s = slotKey(t, s, "f")
	q := s.query()
	if q.Get("occupied") != "false" || q.Get("is_active") != "true" {
		t.Errorf("the free view must ask for unoccupied AND in-service slots, got %v", q)
	}
	if !s.loading {
		t.Errorf("cycling must re-fetch — a status absent from the loaded page is unreachable locally")
	}
	if s.cursor != 0 || s.windowStart != 0 {
		t.Errorf("cycling replaces the whole row set, so the cursor must reset")
	}

	s = slotKey(t, s, "f")
	if s.query().Get("occupied") != "true" {
		t.Errorf("third view should be occupied, got %v", s.query())
	}
	s = slotKey(t, s, "f")
	if s.query().Get("is_active") != "false" {
		t.Errorf("fourth view should be retired, got %v", s.query())
	}
	s = slotKey(t, s, "f")
	if len(s.query()) != 0 {
		t.Errorf("the cycle must wrap back to unfiltered, got %v", s.query())
	}
}

// TestParseSlotScope covers the `/` prompt's grammar. A whole slot code is
// rejected on purpose: an operator typing 1A1 meant to OPEN that slot, and
// silently dropping the position would scope them somewhere they didn't ask for.
func TestParseSlotScope(t *testing.T) {
	cases := []struct {
		in    string
		rack  int
		level string
		fail  bool
	}{
		{in: "", rack: 0, level: ""},
		{in: "  ", rack: 0, level: ""},
		{in: "1", rack: 1},
		{in: "12", rack: 12},
		{in: " 1a ", rack: 1, level: "A"},
		{in: "1A", rack: 1, level: "A"},
		{in: "1A1", fail: true},
		{in: "A", fail: true},
		{in: "1AB", fail: true},
		{in: "0", fail: true},
	}
	for _, c := range cases {
		rack, level, err := parseSlotScope(c.in)
		if c.fail {
			if err == nil {
				t.Errorf("parseSlotScope(%q) should fail", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSlotScope(%q): %v", c.in, err)
			continue
		}
		if rack != c.rack || level != c.level {
			t.Errorf("parseSlotScope(%q) = (%d,%q), want (%d,%q)", c.in, rack, level, c.rack, c.level)
		}
	}
}

// TestStorageSlots_ScopeCombinesWithFilter — the two axes are independent, so
// "free slots on rack 1 level A" has to reach the server as one query.
func TestStorageSlots_ScopeCombinesWithFilter(t *testing.T) {
	s := loadedSlotsScreen()
	s.filterIdx = 1 // free
	s.scopeRack, s.scopeLevel = 1, "A"
	q := s.query()
	if q.Get("rack") != "1" || q.Get("level") != "A" {
		t.Errorf("scope missing from the query: %v", q)
	}
	if q.Get("occupied") != "false" {
		t.Errorf("filter missing from the query: %v", q)
	}

	s.scopeRack = 0
	if s.query().Has("level") {
		t.Errorf("a level without a rack is meaningless and must not be sent: %v", s.query())
	}
}

// TestStorageSlots_SelectionKeepsToggleOrder — the cards endpoint prints
// explicit ids in the order they were given, so a reprint comes off the printer
// the way the operator listed it.
func TestStorageSlots_SelectionKeepsToggleOrder(t *testing.T) {
	s := loadedSlotsScreen()
	s.cursor = 2
	s = slotKey(t, s, " ")
	s.cursor = 0
	s = slotKey(t, s, " ")
	s.cursor = 1
	s = slotKey(t, s, " ")

	want := []int{3, 1, 2}
	if len(s.selection) != len(want) {
		t.Fatalf("selection = %v, want %v", s.selection, want)
	}
	for i, id := range want {
		if s.selection[i] != id {
			t.Fatalf("selection = %v, want %v (toggle order)", s.selection, want)
		}
	}

	// Re-toggling removes without disturbing the rest.
	s.cursor = 0
	s = slotKey(t, s, " ")
	if s.selected[1] {
		t.Errorf("re-toggling should deselect")
	}
	if len(s.selection) != 2 || s.selection[0] != 3 || s.selection[1] != 2 {
		t.Errorf("selection = %v, want [3 2]", s.selection)
	}

	// c clears everything.
	s = slotKey(t, s, "c")
	if len(s.selection) != 0 || len(s.selected) != 0 {
		t.Errorf("c should clear the selection, got %v", s.selection)
	}
}

// TestStorageSlots_SelectionSurvivesFilterChange — collecting cards across
// views is the point of a selection; a filter change must not silently drop it.
func TestStorageSlots_SelectionSurvivesFilterChange(t *testing.T) {
	s := loadedSlotsScreen()
	s.cursor = 0
	s = slotKey(t, s, " ")
	s = slotKey(t, s, "f")
	if len(s.selection) != 1 || !s.selected[1] {
		t.Errorf("selection should survive a filter cycle, got %v", s.selection)
	}
}

// TestStorageSlots_PrintModePrecedence: an explicit selection wins, else the
// scoped rack, else the rack under the cursor. The two modes are mutually
// exclusive server-side, so exactly one is ever built.
func TestStorageSlots_PrintModePrecedence(t *testing.T) {
	// 1. Selection wins.
	s := loadedSlotsScreen()
	s.scopeRack = 2
	s.cursor = 0
	s = slotKey(t, s, " ")
	s = slotKey(t, s, "p")
	if !s.card.active {
		t.Fatalf("p should open the print prompt")
	}
	req := s.card.request()
	if len(req.SlotIDs) != 1 || req.SlotIDs[0] != 1 {
		t.Errorf("a selection must print as slot_ids, got %+v", req)
	}
	if req.Rack != nil {
		t.Errorf("a selection print must not also carry a rack: %+v", req)
	}
	if s.card.hasToggle() {
		t.Errorf("include-retired is rack-only — the backend ignores it for an ids print")
	}

	// 2. No selection, but a scope.
	s = loadedSlotsScreen()
	s.scopeRack, s.scopeLevel = 1, "A"
	s.cursor = 2 // sits on rack 2 — the scope must win over the cursor
	s = slotKey(t, s, "p")
	req = s.card.request()
	if req.Rack == nil || *req.Rack != 1 || req.Level != "A" {
		t.Errorf("the scoped rack should drive the print, got %+v", req)
	}
	if !s.card.hasToggle() {
		t.Errorf("a rack print must offer the include-retired toggle")
	}

	// 3. Neither: the cursor's rack, whole rack (not its level — a card print
	// is a rack job unless the operator narrowed the view themselves).
	s = loadedSlotsScreen()
	s.cursor = 2
	s = slotKey(t, s, "p")
	req = s.card.request()
	if req.Rack == nil || *req.Rack != 2 || req.Level != "" {
		t.Errorf("the cursor's rack should drive the print, got %+v", req)
	}
}

// TestStorageSlots_PrintNothingToPrint — an empty screen with no scope has
// nothing to name, so it says so instead of firing a request that 404s.
func TestStorageSlots_PrintNothingToPrint(t *testing.T) {
	s := NewStorageSlotsScreen(Deps{})
	s.loading = false
	s = slotKey(t, s, "p")
	if s.card.active {
		t.Errorf("p must not open a prompt with nothing to print")
	}
}

// TestStorageSlots_CardToggleFoldsIntoRequest pins that flipping the
// include-retired row actually reaches the wire.
func TestStorageSlots_CardToggleFoldsIntoRequest(t *testing.T) {
	s := loadedSlotsScreen()
	s.scopeRack = 1
	s = slotKey(t, s, "p")
	if s.card.request().IncludeInactive {
		t.Fatalf("include-retired should default off — a rack print covers the slots in service")
	}
	s = slotKey(t, s, "tab") // move to the toggle row
	s = slotKey(t, s, " ")
	if !s.card.request().IncludeInactive {
		t.Errorf("space on the toggle row should set include_inactive")
	}
}

// TestStorageSlots_RowText covers the three occupancy readings a warden acts
// on, plus the marker column.
func TestStorageSlots_RowText(t *testing.T) {
	slots := sampleSlots()

	if got := slotOccupancyText(slots[0]); got != "free" {
		t.Errorf("an in-service empty slot should read free, got %q", got)
	}
	occ := slotOccupancyText(slots[1])
	if !strings.Contains(occ, "Alice Smith") || !strings.Contains(occ, "PS-AB23CDFG") {
		t.Errorf("an occupied slot must name who and which stint, got %q", occ)
	}
	if got := slotOccupancyText(slots[2]); !strings.Contains(got, "retired") {
		t.Errorf("a retired slot must say so — free alone would imply it is on offer, got %q", got)
	}

	if got := slotFlagsText(slots[0]); !strings.Contains(got, "tag 41") {
		t.Errorf("the marker id belongs on the row, got %q", got)
	}
	flags := slotFlagsText(slots[2])
	if !strings.Contains(flags, "tag —") {
		t.Errorf("a slot with no marker must say so rather than showing nothing, got %q", flags)
	}
	if !strings.Contains(flags, "jack") || !strings.Contains(flags, "Woodshop") {
		t.Errorf("pallet-jack and owner belong on the row, got %q", flags)
	}
}

// TestStorageSlots_EmptyTextDistinguishesFilter extends the empty-vs-unavailable
// rule to a whole screen: "no slots" under a filter reads as "this space has no
// racking", which sends a warden looking for a bug that isn't there.
func TestStorageSlots_EmptyTextDistinguishesFilter(t *testing.T) {
	s := NewStorageSlotsScreen(Deps{})
	s.loading = false
	if got := s.emptyText(); !strings.Contains(got, "No storage slots yet") {
		t.Errorf("unfiltered empty should offer to generate a rack, got %q", got)
	}
	s.filterIdx = 1
	got := s.emptyText()
	if !strings.Contains(got, `"free"`) || !strings.Contains(got, "cycle the filter") {
		t.Errorf("a filtered empty view must name the view, got %q", got)
	}
}

// TestStorageSlots_DeleteConfirmWarnsHard — deleting releases a permanent
// marker and is refused outright while a member's project is in the slot, so
// the confirm has to say both and point at retiring instead.
func TestStorageSlots_DeleteConfirmWarnsHard(t *testing.T) {
	s := loadedSlotsScreen()
	s.cursor = 1 // the occupied slot
	s = slotKey(t, s, "x")
	if !s.confirmingDelete {
		t.Fatalf("x should arm the delete confirm")
	}
	text := s.deleteConfirmText()
	if !strings.Contains(text, "AprilTag") {
		t.Errorf("the confirm must say the marker is released, got %q", text)
	}
	if !strings.Contains(text, "PS-AB23CDFG") {
		t.Errorf("an occupied slot's confirm must name the live stint, got %q", text)
	}
	if !strings.Contains(text, "Retiring") {
		t.Errorf("the confirm should point at the non-destructive option, got %q", text)
	}

	s = slotKey(t, s, "n")
	if s.confirmingDelete {
		t.Errorf("n should cancel the confirm")
	}
}

// TestStorageSlots_RenderSmoke walks the render paths that index into rows, to
// catch a window/cursor arithmetic panic.
func TestStorageSlots_RenderSmoke(t *testing.T) {
	s := loadedSlotsScreen()
	if out := s.View(); !strings.Contains(out, "1A1") {
		t.Errorf("view should list slot codes: %q", out)
	}
	s.cursor = 0
	s = slotKey(t, s, " ")
	if out := s.View(); !strings.Contains(out, "1 selected") {
		t.Errorf("the header should report the selection: %q", out)
	}
	s = slotKey(t, s, "/")
	if out := s.View(); !strings.Contains(out, "Scope to rack") {
		t.Errorf("the scope prompt should render: %q", out)
	}
	// A junk scope is a message, not a request.
	s.scopeInput.SetValue("1A1")
	s = slotKey(t, s, "enter")
	if s.scopeErr == "" {
		t.Errorf("a slot code in the scope box should be rejected with a message")
	}
	if !strings.Contains(s.View(), "✗") {
		t.Errorf("the scope error should render")
	}
}

// TestStorageSlots_PagedownClampsOnEmpty guards the bug class where
// `cursor = len(rows)-1` is -1 on an empty list, which no row index can be and
// which the next loaded page would inherit.
//
// IT PRESSES PGDOWN, and the key matters as much as the assertion. It used to
// press ctrl+d, which was a synonym for the same arm until sc-jde-listnav
// retired the emacs chords — after which the keystroke matched no case at all,
// the arm this test exists to enter was never entered, and `cursor >= 0` passed
// for exactly the reason it would have passed with the arm deleted. A test that
// drives a key nothing binds is the vacuous-fixture rule (AGENTS.md) with the
// fixture left alone and the KEY made inert.
//
// The assertion is the cursor's exact resting place and not merely a
// non-negative one, for the same reason: "not negative" is true of a screen on
// which nothing ran. Zero is where the clamp puts it, and scrollIntoView clamps
// only windowStart, so neutering either arm's guard fails this.
func TestStorageSlots_PagedownClampsOnEmpty(t *testing.T) {
	s := NewStorageSlotsScreen(Deps{})
	s.loading = false
	s = slotKey(t, s, "pgdown")
	if s.cursor != 0 {
		t.Errorf("cursor = %d after pgdown on an empty list, want 0", s.cursor)
	}
	s = slotKey(t, s, "G")
	if s.cursor != 0 {
		t.Errorf("cursor = %d after G on an empty list, want 0", s.cursor)
	}
}
