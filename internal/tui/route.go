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
	Key      Workspace
	Label    string
	Hotkey   rune
	StaffOnly bool
}

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
