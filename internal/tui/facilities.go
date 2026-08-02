package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// FacilitiesScreen is the landing page for the Facilities workspace. The
// tab used to instantiate a stub ListScreen with no loader, so it just
// rendered "No rows." now it's a menu of the facilities-related
// sub-screens scantty supports — electrical panels, e-paper, location
// check-ins, maker boxes, checklists — with single-key hotkeys that
// mirror the global hotkey set defined in app.go.
//
// Items render with a cursor. j/k or arrow keys move; enter or the
// item's hotkey opens it. The selection lives on the screen rather
// than on Root because navigating away (esc to scan) should reset it.
type FacilitiesScreen struct {
	deps   Deps
	cursor int
	items  []facilitiesItem
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
				// the universal up-arrow app-wide (see checklists.go). Uppercase
				// K is the checklists global in app.go, which builds exactly the
				// screen this item builds, in exactly this workspace, so the
				// menu now advertises the same accelerator the welcome screen
				// does rather than inventing a third binding for one screen —
				// the same benign overlap the e-Paper item above rides on 'e'.
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
				// R (Racking). This menu is NOT a LocalKeyScreen, so its item
				// letters reach it only through app.go's fall-through — a
				// letter that is also a GLOBAL hotkey would open the global
				// surface instead and leave the item unreachable. That rules
				// out 's' (nav scan), 'l' (global lockouts) and 'e' (global
				// e-paper), and 'S' is already the stint list above; uppercase
				// R is free in the global keymap and rides the same
				// "uppercase = a management surface" convention as E/S here.
				hotkey:   'R',
				label:    "Storage slots",
				subtitle: "project-storage racking — browse by rack, generate, print cards",
				build: func(d Deps) (Screen, Workspace) {
					return NewStorageSlotsScreen(d), WSFacilities
				},
				implemented: true,
			},
			{
				// O (Overview). Same rule as R above: this menu is NOT a
				// LocalKeyScreen, so an item letter that is also a GLOBAL
				// hotkey opens the global surface and leaves the item
				// unreachable. Lowercase 'o' is the global op-modes key, but
				// uppercase O is free — and it rides the same "uppercase = a
				// management surface" convention as E/S/R here.
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
				// E (Enrollment): 'b'/'e' collide with the Maker-boxes / e-Paper
				// items; E is free here and reaches this screen via the app.go
				// fallthrough (no global E case), so it doesn't shadow detail-screen
				// E=edit elsewhere.
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

func (s *FacilitiesScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
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

func (s *FacilitiesScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Select a facilities surface — j/k move · enter or hotkey opens") + "\n\n")
	for i, it := range s.items {
		marker := "  "
		label := fmt.Sprintf("[%c]  %s", it.hotkey, it.label)
		if i == s.cursor {
			marker = "▸ "
			label = StyleSidebarItemActive.Render(label)
		}
		b.WriteString(marker + label + "\n")
		if it.subtitle != "" {
			b.WriteString("      " + StyleMuted.Render(it.subtitle) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("More facilities surfaces (LOTO, vendors) ship as separate hotkeys; see the welcome screen for the full list."))
	return b.String()
}
