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

func (s *WelcomeScreen) View() string {
	lines := []string{
		"Scantty — scanner-driven console for OMS + ForgeKey.",
		"",
		StyleMuted.Render("Press a workspace hotkey to begin:"),
		"",
		"  [0] Scan         — barcode + badge entry",
		"  [1] Dashboard    — operations overview",
		"  [2] Inventory    — items, suppliers, locations",
		"  [3] Purchasing   — purchase orders + reorders",
		"  [4] Assets       — equipment register",
		"  [5] Facilities   — TV, kiosk, electrical",
		"  [6] Maintenance  — work orders, PM dashboard",
		"  [7] SIGs         — special interest groups",
		"  [8] Reports      — analytics + exports",
		"  [9] ForgeKey     — devices, authorizations",
		"  [s] Settings",
		"",
		"  Ctrl+K or /     — global search palette",
		"  m               — your member profile",
		"  n               — notifications (poll runs every 60s)",
		"  a               — ForgeKey authorizations (revoke)",
		"  l               — ForgeKey lockouts (hierarchical unlock)",
		"  o               — operational modes (toggle classroom mode)",
		"  u               — usage sessions (end active)",
		"  f               — firmware versions + recent updates",
		"  B               — maker boxes (per-member bin assignments + scan)",
		"  Q               — pending reorder request queue",
		"  D               — donations log",
		"",
		StyleMuted.Render("Press `q` on this screen, or Ctrl+C anywhere, to quit."),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}
