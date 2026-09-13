package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// FacilitiesScreen is the landing page for the Facilities workspace. The
// tab used to instantiate a stub ListScreen with no loader, so it just
// rendered "No rows." now it's a menu of the facilities-related
// sub-screens scantty supports — electrical panels, e-paper, location
// check-ins, maker boxes, checklists. It is one of the two on-screen cursor
// menus (with reports.go) that the JD Edwards redesign kept as the model for
// how a workspace offers its surfaces.
//
// Items render with a cursor. j/k or arrow keys move; enter or the
// item's hotkey opens it. The selection lives on the screen rather
// than on Root because navigating away (esc to scan) should reset it.
type FacilitiesScreen struct {
	deps   Deps
	cursor int
	items  []facilitiesItem

	// windowStart is the first row drawn, refitted from View by
	// proseFlatListFrame against the pane the terminal gave.
	windowStart    int
	terminalWidth  int
	terminalHeight int
}

type facilitiesItem struct {
	hotkey      rune
	label       string
	subtitle    string
	build       func(deps Deps) (Screen, Workspace)
	implemented bool
}

func NewFacilitiesScreen(deps Deps) *FacilitiesScreen {
	return &FacilitiesScreen{
		deps: deps,
		items: []facilitiesItem{
			{
				hotkey:   'p',
				label:    "Electrical panels",
				subtitle: "panels → breakers → circuits → outlets → connected assets",
				build: func(d Deps) (Screen, Workspace) {
					return NewElectricalPanelsScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				hotkey:   'e',
				label:    "e-Paper panels",
				subtitle: "battery, firmware, retire / reactivate, bind to asset",
				build: func(d Deps) (Screen, Workspace) {
					return NewEPaperPanelsScreen(d), WSForgeKey
				},
				implemented: true,
			},
			{
				hotkey:   'c',
				label:    "Location check-ins",
				subtitle: "recent activity, log a new check-in",
				build: func(d Deps) (Screen, Workspace) {
					return NewLocationCheckinsScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				hotkey:   'b',
				label:    "Maker boxes",
				subtitle: "per-member bin assignments + scan flow",
				build: func(d Deps) (Screen, Workspace) {
					return NewMakerBoxesScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				// K (uppercase). Lowercase k is this menu's own cursor-up key —
				// matched in the switch below, long before the item loop — so a
				// 'k' item could never be opened by its own letter; it is also
				// the universal up-arrow app-wide (see checklists.go).
				hotkey:   'K',
				label:    "Checklists",
				subtitle: "active list + in-progress runs",
				build: func(d Deps) (Screen, Workspace) {
					return NewChecklistsScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				hotkey:   'S',
				label:    "Project storage",
				subtitle: "intake a stint · active list → detail, remove, label preview",
				build: func(d Deps) (Screen, Workspace) {
					return NewProjectStorageListScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				// R (Racking). 'S' above is already the stint list and 'r' below
				// is the certificate store, so racking took R. The letters on
				// this menu are not accelerators in the retired sense — they
				// address a row that is ON SCREEN with its label beside it, and
				// arrows + enter reach every one of them without knowing any
				// letter at all.
				hotkey:   'R',
				label:    "Storage slots",
				subtitle: "project-storage racking — browse by rack, generate, print cards",
				build: func(d Deps) (Screen, Workspace) {
					return NewStorageSlotsScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				// O (Overview), beside R (Racking) — the two halves of the
				// storage picture take the same case as each other.
				hotkey:   'O',
				label:    "Storage overview",
				subtitle: "ASCII rack grid — who is in every slot, what is expiring · assign C/L/E",
				build: func(d Deps) (Screen, Workspace) {
					return NewStorageOverviewScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				hotkey:   't',
				label:    "Thermostats",
				subtitle: "climate registry → create / edit / delete, kill-breaker source",
				build: func(d Deps) (Screen, Workspace) {
					return NewThermostatListScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				hotkey:   'r',
				label:    "Certificates (PKI)",
				subtitle: "internal CA + issued device certs (read-only) · rotate root CA",
				build: func(d Deps) (Screen, Workspace) {
					return NewForgeKeyCertificatesScreen(d), WSForgeKey
				},
				implemented: true,
			},
			{
				// E (Enrollment): 'b'/'e' are already the Maker-boxes / e-Paper
				// items on this menu, so enrollment took the shifted key.
				hotkey:   'E',
				label:    "Badge enrollment",
				subtitle: "assign / clear member access badges — arm a reader or enter a UID (staff)",
				build: func(d Deps) (Screen, Workspace) {
					return NewBadgeEnrollmentScreen(d), WSForgeKey
				},
				implemented: true,
			},
		},
	}
}

func (s *FacilitiesScreen) Title() string { return "Facilities" }

func (s *FacilitiesScreen) Init() tea.Cmd { return nil }

// bar names every key that acts on this menu, as a RECORD rather than a literal
// — prose_bar.go carries the conversion.
//
// It used to be a legend ABOVE the rows reading
//
//	Select a facilities surface — j/k move · enter or hotkey opens
//
// which named two of the eight movement keystrokes this switch binds — the
// arrows, g/G and home/end all moved the cursor under no word — and named the
// row letters as "hotkey", a word no keystroke spells. And the menu drew every
// row with no window at all: eleven two-line rows and a closing note are more
// than an 80x24 pane holds, so the note and the last rows were what clampToBox
// took.
//
// NO PAGER, because this switch binds none: proseNavList(moves, false) is the
// step pair and the jumps.
func (s *FacilitiesScreen) bar(rows int) proseBar {
	hotkeys := make([]rune, 0, len(s.items))
	for _, it := range s.items {
		hotkeys = append(hotkeys, it.hotkey)
	}
	return append(proseNavList(listNavMoves(rows), false),
		proseBarItem{Keys: []string{"enter"}, Hint: "enter open"},
		proseMenuHotkeys(hotkeys, "open by letter"),
		proseBarEsc,
	)
}

// proseBar is the bar this menu is drawing. A menu has no load and no second
// surface, so there is no state in which it draws something else instead.
func (s *FacilitiesScreen) proseBar() proseBar { return s.bar(len(s.items)) }

func (s *FacilitiesScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

func (s *FacilitiesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		s.terminalWidth, s.terminalHeight = size.Width, size.Height
		return s, nil
	}
	keymsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch keymsg.String() {
	case "j", "down":
		if s.cursor < len(s.items)-1 {
			s.cursor++
		}
		return s, nil
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
		}
		return s, nil
	case "g", "home":
		s.cursor = 0
		return s, nil
	case "G", "end":
		s.cursor = len(s.items) - 1
		return s, nil
	case "enter":
		return s.openSelected()
	}
	if r := keymsg.String(); len(r) == 1 {
		for i, it := range s.items {
			if string(it.hotkey) == r {
				s.cursor = i
				return s.openSelected()
			}
		}
	}
	return s, nil
}

func (s *FacilitiesScreen) openSelected() (Screen, tea.Cmd) {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return s, nil
	}
	it := s.items[s.cursor]
	if !it.implemented || it.build == nil {
		return s, Status(fmt.Sprintf("%s — not wired yet", it.label), StatusWarn)
	}
	screen, ws := it.build(s.deps)
	return s, SwitchTo(ws, screen)
}

// facilitiesNote is what the closing line under the rows used to say, without
// the key it named. It led the rows as part of the head once the bar moved under
// them, and a body sentence may name a key only where the bar does: `tab` is
// Root's, which no record on this screen can answer for.
const facilitiesNote = "Select a facilities surface. Vendors, firmware and the device fleet are rows of the sidebar menu instead."

func (s *FacilitiesScreen) View() string {
	cells := s.paneCells()
	head := pickerHintAt(facilitiesNote, cells) + "\n\n"
	rows := make([]string, len(s.items))
	for i, it := range s.items {
		marker := "  "
		label := fmt.Sprintf("[%c]  %s", it.hotkey, it.label)
		if i == s.cursor {
			marker = "▸ "
			label = StyleSidebarItemActive.Render(label)
		}
		row := marker + label
		if it.subtitle != "" {
			row += "\n" + proseMenuSubtitle(it.subtitle, cells)
		}
		rows[i] = row
	}
	return proseFlatListFrame(head, rows, s.cursor, &s.windowStart, s.terminalHeight,
		cells, s.bar(proseFlatCeilingRows), s.proseBar())
}
