package tui

import tea "github.com/charmbracelet/bubbletea"

type Workspace string

const (
	WSDashboard   Workspace = "dashboard"
	WSInventory   Workspace = "inventory"
	WSPurchasing  Workspace = "purchasing"
	WSAssets      Workspace = "assets"
	WSFacilities  Workspace = "facilities"
	WSMaintenance Workspace = "maintenance"
	WSSIGs        Workspace = "sigs"
	WSReports     Workspace = "reports"
	WSSettings    Workspace = "settings"
	WSForgeKey    Workspace = "forgekey"
	WSScan        Workspace = "scan"
)

type WorkspaceMeta struct {
	Key       Workspace
	Label     string
	Hotkey    rune
	StaffOnly bool
}

const (
	WSLogin          Workspace = "login"
	WSProfile        Workspace = "profile"
	WSAuthorizations Workspace = "authorizations"
	WSLockouts       Workspace = "lockouts"
)

func Workspaces() []WorkspaceMeta {
	return []WorkspaceMeta{
		{WSScan, "Scan", '0', false},
		{WSDashboard, "Dashboard", '1', false},
		{WSInventory, "Inventory", '2', false},
		{WSPurchasing, "Purchasing", '3', false},
		{WSAssets, "Assets", '4', false},
		{WSFacilities, "Facilities", '5', true},
		{WSMaintenance, "Maintenance", '6', false},
		{WSSIGs, "SIGs", '7', false},
		{WSReports, "Reports", '8', false},
		{WSForgeKey, "ForgeKey", '9', true},
		{WSSettings, "Settings", 's', false},
	}
}

type Screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Screen, tea.Cmd)
	View() string
	Title() string
}

// RawInputScreen lets a screen opt into receiving every keypress before the
// root applies its global hotkeys. Forms, login, and the search palette
// implement this so single-letter workspace shortcuts (m, a, l, n, o, u, f,
// Q, D) don't steal characters from textinputs.
type RawInputScreen interface {
	WantsRawInput() bool
}

// LocalKeyScreen lets the active screen claim specific single-key shortcuts
// before the root applies its global navigation hotkeys. When the active
// screen reports that it handles a key, the root routes the KeyMsg straight to
// the screen's Update and stops — so a screen-local binding (location check-in
// 'n', checklist finalize 'f', list-screen sort 's') wins over the colliding
// global nav key. Screens that don't implement this keep the global keys, so
// global nav stays the fallback everywhere the active screen hasn't claimed a
// key. Unlike RawInputScreen (which swallows *every* key for a textinput), this
// is per-key: the screen names only the keys it owns and all other keys still
// reach the global nav dispatch.
type LocalKeyScreen interface {
	HandlesKey(key string) bool
}

type SwitchScreenMsg struct {
	Workspace Workspace
	Screen    Screen
}

func SwitchTo(ws Workspace, s Screen) tea.Cmd {
	return func() tea.Msg { return SwitchScreenMsg{Workspace: ws, Screen: s} }
}

type StatusMsg struct {
	Text  string
	Level StatusLevel
}

type StatusLevel int

const (
	StatusInfo StatusLevel = iota
	StatusOK
	StatusWarn
	StatusError
)

func Status(text string, level StatusLevel) tea.Cmd {
	return func() tea.Msg { return StatusMsg{Text: text, Level: level} }
}
