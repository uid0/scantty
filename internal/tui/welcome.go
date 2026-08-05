package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type WelcomeScreen struct{}

func NewWelcomeScreen() *WelcomeScreen { return &WelcomeScreen{} }

func (s *WelcomeScreen) Init() tea.Cmd { return nil }

func (s *WelcomeScreen) Update(msg tea.Msg) (Screen, tea.Cmd) { return s, nil }

func (s *WelcomeScreen) Title() string { return "Welcome" }

// View is the app's standing statement of the key model. It used to be a table
// of ~25 global letters, which is exactly what phase 3 of the JD Edwards
// redesign retired: system keys are now scroll / exit / submit / edit and
// nothing else, and every surface those letters opened is a row of the sidebar
// menu. So this screen names the keys that exist and points at the menu, rather
// than listing destinations by letter.
//
// It is written to fit an 80x24 terminal WHOLE — every line inside
// screenBodyWidth(80) and no more lines than screenBodyHeight(24). This screen
// does not scroll, and Root.View clamps with clampToBox, which TRUNCATES in
// both directions rather than wrapping or paging: an over-wide line loses its
// end and an over-long list loses its tail, with nothing on screen to say so.
// The old version ran 35 lines, so on a standard terminal the bottom third —
// which is where "ctrl+c quits" lived — was never visible at all.
func (s *WelcomeScreen) View() string {
	lines := []string{
		"Scantty — console for OMS + ForgeKey.",
		"",
		StyleMuted.Render("Every screen is a row of the menu: press tab,"),
		StyleMuted.Render("walk with the arrows, open with enter."),
		"",
		StyleTitle.Render("The whole key model"),
		"",
		"  tab       into the menu, and back out",
		"  ↑ ↓ j k   move · ← → jump a workspace",
		"  enter     open what is selected · submit",
		"  esc       back one step · cancel",
		"  ctrl+e    edit / open the highlighted row",
		"  ctrl+k    search — items, assets, orders",
		"  ctrl+c    quit, from anywhere (ctrl+q too)",
		"",
		StyleMuted.Render("Scrolling reads the same on every screen —"),
		StyleMuted.Render("PgUp/PgDn, g / G for top and bottom — and each"),
		StyleMuted.Render("screen names its own keys along the foot."),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
