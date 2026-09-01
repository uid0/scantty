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
	StaffOnly bool
}

// navSurface is one openable entry BENEATH a workspace in the sidebar menu
// tree. Phase 3 of the JD Edwards redesign retired the global letter
// accelerators that used to be the only door to these screens (m/a/l/n/o/u/f/e
// and C/B/K/P/Q/N/I/A/V/M/D/F/G/L/U/W/T); this tree is where they live now, so
// each is still one arrow-and-enter away with nothing memorised.
//
// A workspace's own row opens its default screen (see newScreenFor), so the
// surfaces listed here are the EXTRAS only — "Inventory" is still the item
// list, and "New item" is a child beside it.
type navSurface struct {
	label string
	build func(Deps) Screen
}

// workspaceSurfaces returns the child entries the sidebar shows under a
// workspace. A workspace whose landing screen is already a cursor menu of its
// own surfaces (Facilities, Reports) returns none — duplicating that menu in
// the sidebar would give every one of those screens two doors and two places to
// keep correct.
func workspaceSurfaces(ws Workspace) []navSurface {
	switch ws {
	case WSInventory:
		return []navSurface{
			{"New item", func(d Deps) Screen { return NewInventoryItemFormScreen(d, "") }},
			{"Categories", func(d Deps) Screen { return NewCategoryListScreen(d) }},
			{"Locations", func(d Deps) Screen { return NewLocationListScreen(d) }},
			{"Suppliers", func(d Deps) Screen { return NewSupplierListScreen(d) }},
		}
	case WSPurchasing:
		return []navSurface{
			{"New order", func(d Deps) Screen { return NewPurchaseOrderCreateScreen(d) }},
			{"Reorder queue", func(d Deps) Screen { return NewReorderQueueScreen(d) }},
		}
	case WSAssets:
		return []navSurface{
			{"New asset", func(d Deps) Screen { return NewAssetFormScreen(d, "") }},
		}
	case WSMaintenance:
		return []navSurface{
			{"PM board", func(d Deps) Screen { return NewPMBoardScreen(d) }},
			{"PM items", func(d Deps) Screen { return NewMaintenanceItemsScreen(d) }},
			{"Vendors", func(d Deps) Screen { return NewVendorsScreen(d) }},
		}
	case WSForgeKey:
		return []navSurface{
			{"Device types", func(d Deps) Screen { return NewDeviceTypeListScreen(d) }},
			{"Firmware", func(d Deps) Screen { return NewFirmwareScreen(d) }},
			{"e-Paper panels", func(d Deps) Screen { return NewEPaperPanelsScreen(d) }},
			{"Authorizations", func(d Deps) Screen { return NewAuthorizationsScreen(d) }},
			{"Lockouts", func(d Deps) Screen { return NewLockoutsScreen(d) }},
			{"Operational modes", func(d Deps) Screen { return NewOperationalModesScreen(d) }},
			{"Usage sessions", func(d Deps) Screen { return NewUsageScreen(d) }},
		}
	case WSSettings:
		// Donations rides here rather than under a workspace of its own: the
		// global D set no workspace at all, and the tax-receipt lookup already
		// on the Settings screen is the same backend app (donations/), so the
		// pair stays together.
		return []navSurface{
			{"Profile", func(d Deps) Screen { return NewProfileScreen(d) }},
			{"Notifications", func(d Deps) Screen { return NewNotificationsScreen(d) }},
			{"Donations", func(d Deps) Screen { return NewDonationsScreen(d) }},
			{"Webhooks", func(d Deps) Screen { return NewWebhookListScreen(d) }},
		}
	}
	return nil
}

const (
	WSLogin          Workspace = "login"
	WSProfile        Workspace = "profile"
	WSAuthorizations Workspace = "authorizations"
	WSLockouts       Workspace = "lockouts"
)

// Workspaces is the sidebar's top level, in display order. The Hotkey field
// these entries used to carry ('0'-'9' and 's') is gone: phase 3 of the JD
// Edwards redesign reserves system keys for scroll / exit / submit / edit, and
// a digit that jumps workspaces is outside that set exactly as a letter is.
// Arrow-navigating the sidebar (see Nav) replaces them.
func Workspaces() []WorkspaceMeta {
	return []WorkspaceMeta{
		{WSScan, "Scan", false},
		{WSDashboard, "Dashboard", false},
		{WSInventory, "Inventory", false},
		{WSPurchasing, "Purchasing", false},
		{WSAssets, "Assets", false},
		{WSFacilities, "Facilities", true},
		{WSMaintenance, "Maintenance", false},
		{WSSIGs, "SIGs", false},
		{WSReports, "Reports", false},
		{WSForgeKey, "ForgeKey", true},
		{WSSettings, "Settings", false},
	}
}

type Screen interface {
	Init() tea.Cmd
	Update(msg tea.Msg) (Screen, tea.Cmd)
	View() string
	Title() string
}

// RawInputScreen lets a screen opt into receiving every keypress before the
// root applies its global keys. Forms, login, and the search palette implement
// this so nothing the root owns — `tab` into the sidebar, `esc` back, `ctrl+k`
// search — is taken away from a screen that is mid-entry. Since phase 3 the
// root holds no letter at all, so this is no longer about protecting a
// textinput from workspace shortcuts; it is about a modal owning its own exit.
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
//
// Since phase 3 stripped the root's letter accelerators, a claim is no longer
// needed to WIN a letter — no letter collides any more. Existing claims are
// kept because they cost nothing and a screen that names a key it handles is
// documenting itself; what a claim still genuinely decides is `esc` (a screen
// that claims it owns its own back step) and the two keys the root still holds.
type LocalKeyScreen interface {
	HandlesKey(key string) bool
}

// BackStackScreen answers the BACK-STACK question directly: is this screen in a
// transient state that must not be recorded, so `esc` never navigates back INTO
// something the operator already dismissed?
//
// IT EXISTS BECAUSE recordHistory USED TO ASK WantsRawInput INSTEAD, and that is
// a different question — "does this screen take every keystroke". The two gave
// one answer for as long as every transient state also owned the keyboard, and
// they came apart the moment ListScreen narrowed its key-routing half to
// exclude a refused pane: the history half moved with it, silently, and a
// searching list started being pushed onto the stack. Restored later, it drew
// its query and match count over an unfiltered reload.
//
// So a screen that answers here owns the answer, and WantsRawInput is left
// answering only about keys. Screens that do not implement it keep the
// raw-input fallback, which is still the right proxy for a form or a confirm
// whose transience and whose keyboard ownership really are the same state.
type BackStackScreen interface {
	SkipsBackStack() bool
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
