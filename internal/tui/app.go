package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/cache"
	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

type Deps struct {
	OMS                 *omsapi.Client
	ForgeKey            *forgekeyapi.Client
	Cache               *cache.Cache
	Ctx                 context.Context
	InitialStaff        bool
	SaveThemePreference func(name string) error
}

type Root struct {
	deps     Deps
	nav      Nav
	status   StatusBar
	screen   Screen
	history  []navEntry
	backNav  bool // set for the turn in which `esc` navigated back; see Update
	width    int
	height   int
	navWidth int
}

// navEntry is one frame of the back-stack: the screen that was active and the
// nav workspace highlighted at the moment it was pushed. `esc` pops the top
// frame to restore both.
type navEntry struct {
	screen Screen
	ws     Workspace
}

// Update wraps the real key/message dispatch (see dispatch) with the back-stack
// bookkeeping: whenever a turn navigates FORWARD — swapping in a genuinely
// different screen instance — the outgoing screen is recorded so a later `esc`
// can return to it (a "back button"). Because every Screen uses a pointer
// receiver, its identity is stable across an in-place Update, so `screen`
// changing to a different value is exactly the "a real navigation happened"
// signal; ordinary key handling that leaves the same screen active records
// nothing. A turn that was itself an `esc` (backNav) records nothing either —
// a back step must not re-push the screen it just left.
func (r Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	prev := r.screen
	prevWS := r.nav.Active()
	r.backNav = false
	model, cmd := r.dispatch(msg)
	next, ok := model.(Root)
	if !ok {
		return model, cmd
	}
	if !next.backNav && next.screen != prev {
		next.recordHistory(prev, prevWS)
	}
	return next, cmd
}

// recordHistory pushes an outgoing screen onto the back-stack. Transient
// screens that own their own esc handling are deliberately NOT recorded — `esc`
// must never navigate the user back INTO a view they already dismissed:
//   - forms, pickers and confirm prompts (a RawInputScreen that currently
//     WantsRawInput), which cancel themselves via their own esc; and
//   - any screen that claims esc via HandlesKey (the Reports tables/pulse,
//     which return to the Reports hub on their own).
//
// Those screens are also intercepted before the global esc handler ever runs,
// so their local esc-cancel keeps working unchanged.
func (r *Root) recordHistory(screen Screen, ws Workspace) {
	if screen == nil {
		return
	}
	if rs, ok := screen.(RawInputScreen); ok && rs.WantsRawInput() {
		return
	}
	if lk, ok := screen.(LocalKeyScreen); ok && lk.HandlesKey("esc") {
		return
	}
	r.history = append(r.history, navEntry{screen: screen, ws: ws})
}

// popHistory restores the previous screen and its workspace from the
// back-stack, returning false when the stack is empty. The restored screen is
// re-Init'd so its data refreshes (mirroring the fresh-screen reload the
// existing SwitchTo back-navigations already perform) while its retained
// context — a detail's record id, a sub-list's parent id — is preserved.
func (r *Root) popHistory() (tea.Cmd, bool) {
	if len(r.history) == 0 {
		return nil, false
	}
	entry := r.history[len(r.history)-1]
	r.history = r.history[:len(r.history)-1]
	r.screen = entry.screen
	r.nav.SetActive(entry.ws)
	return tea.Batch(r.screen.Init(), r.windowResizeCmd()), true
}

func NewRoot(deps Deps) Root {
	r := Root{
		deps:     deps,
		nav:      NewNav(),
		status:   NewStatusBar(),
		navWidth: 24,
	}
	if deps.OMS != nil && deps.OMS.AccessToken() != "" {
		r.nav.SetStaff(deps.InitialStaff)
		r.screen = NewWelcomeScreen()
	} else {
		r.screen = NewLoginScreen(deps)
	}
	return r
}

func (r Root) Init() tea.Cmd {
	cmds := []tea.Cmd{
		r.screen.Init(),
		Status("welcome — press / to scan, 1-9 to switch", StatusInfo),
	}
	if poll := PollNotifications(r.deps, 60*time.Second); poll != nil {
		cmds = append(cmds, poll)
	}
	return tea.Batch(cmds...)
}

// windowResizeCmd returns a cmd that re-emits a tea.WindowSizeMsg with the
// current terminal dimensions. Used to push the current size into a freshly-
// swapped screen so its scrollers/lists fit the actual pane rather than the
// default placeholder size. Returns nil when the size isn't known yet (initial
// startup); Bubbletea's own first WindowSizeMsg will cover that case.
func (r Root) windowResizeCmd() tea.Cmd {
	if r.width <= 0 || r.height <= 0 {
		return nil
	}
	w, h := r.width, r.height
	return func() tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }
}

func (r Root) dispatch(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = m.Width, m.Height
		r.nav.SetWidth(r.navWidth)
		r.status.SetWidth(r.width)
		// Forward the resize to the active screen so scrollers and
		// lists can re-fit their viewports.
		if r.screen != nil {
			next, cmd := r.screen.Update(msg)
			r.screen = next
			return r, cmd
		}
		return r, nil

	case tea.KeyMsg:
		// Quit is always available, even inside textinput forms.
		if s := m.String(); s == "ctrl+c" || s == "ctrl+q" {
			return r, tea.Quit
		}
		// Screens with active textinputs (login, forms, search palette)
		// bypass global hotkey handling so letters reach the input.
		if rs, ok := r.screen.(RawInputScreen); ok && rs.WantsRawInput() {
			next, cmd := r.screen.Update(msg)
			r.screen = next
			return r, cmd
		}
		// Give the ACTIVE SCREEN first crack at the key. If it claims this
		// key as a screen-local binding (e.g. location check-in 'n',
		// checklist finalize 'f'), route the KeyMsg to it and stop before
		// the global nav switch below — global nav keys are the fallback,
		// not an override. Screens that don't implement LocalKeyScreen (or
		// don't claim this key) fall through to the global dispatch exactly
		// as before.
		if lk, ok := r.screen.(LocalKeyScreen); ok && lk.HandlesKey(m.String()) {
			next, cmd := r.screen.Update(msg)
			r.screen = next
			return r, cmd
		}
		switch m.String() {
		case "ctrl+k", "/":
			if _, ok := r.screen.(*SearchPalette); !ok {
				r.screen = NewSearchPalette(r.deps)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "m":
			if _, ok := r.screen.(*ProfileScreen); !ok {
				r.screen = NewProfileScreen(r.deps)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "a":
			if _, ok := r.screen.(*AuthorizationsScreen); !ok {
				r.screen = NewAuthorizationsScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "l":
			if _, ok := r.screen.(*LockoutsScreen); !ok {
				r.screen = NewLockoutsScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "n":
			if _, ok := r.screen.(*NotificationsScreen); !ok {
				r.screen = NewNotificationsScreen(r.deps)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "o":
			if _, ok := r.screen.(*OperationalModesScreen); !ok {
				r.screen = NewOperationalModesScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "u":
			if _, ok := r.screen.(*UsageScreen); !ok {
				r.screen = NewUsageScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "f":
			if _, ok := r.screen.(*FirmwareScreen); !ok {
				r.screen = NewFirmwareScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "e":
			if _, ok := r.screen.(*EPaperPanelsScreen); !ok {
				r.screen = NewEPaperPanelsScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, r.screen.Init()
			}
		case "C":
			if _, ok := r.screen.(*LocationCheckinsScreen); !ok {
				r.screen = NewLocationCheckinsScreen(r.deps)
				r.nav.SetActive(WSFacilities)
				return r, r.screen.Init()
			}
		case "B":
			if _, ok := r.screen.(*MakerBoxesScreen); !ok {
				r.screen = NewMakerBoxesScreen(r.deps)
				r.nav.SetActive(WSFacilities)
				return r, r.screen.Init()
			}
		case "K":
			if _, ok := r.screen.(*ChecklistsScreen); !ok {
				r.screen = NewChecklistsScreen(r.deps)
				r.nav.SetActive(WSFacilities)
				return r, r.screen.Init()
			}
		case "P":
			if _, ok := r.screen.(*PMBoardScreen); !ok {
				r.screen = NewPMBoardScreen(r.deps)
				r.nav.SetActive(WSMaintenance)
				return r, r.screen.Init()
			}
		case "Q":
			if _, ok := r.screen.(*ReorderQueueScreen); !ok {
				r.screen = NewReorderQueueScreen(r.deps)
				r.nav.SetActive(WSPurchasing)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "N":
			// Shift+n opens the create-PO form. Lowercase n is taken
			// by the Notifications shortcut a few branches up.
			if _, ok := r.screen.(*PurchaseOrderCreateScreen); !ok {
				r.screen = NewPurchaseOrderCreateScreen(r.deps)
				r.nav.SetActive(WSPurchasing)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "I":
			// Shift+i opens the create-inventory-item form. Lowercase i is
			// used on the item detail screen (serialized instances), so the
			// new-item global takes the shifted key — same convention as N
			// (new PO). Editing an existing item is reached with E from its
			// detail screen.
			if _, ok := r.screen.(*InventoryItemFormScreen); !ok {
				r.screen = NewInventoryItemFormScreen(r.deps, "")
				r.nav.SetActive(WSInventory)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "A":
			// Shift+a opens the create-asset form. Lowercase a is the global
			// Authorizations shortcut, so the new-asset global takes the
			// shifted key — same convention as N (new PO) and I (new item).
			// Editing an existing asset is reached with E from its detail
			// screen.
			if _, ok := r.screen.(*AssetFormScreen); !ok {
				r.screen = NewAssetFormScreen(r.deps, "")
				r.nav.SetActive(WSAssets)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "V":
			if _, ok := r.screen.(*VendorsScreen); !ok {
				r.screen = NewVendorsScreen(r.deps)
				r.nav.SetActive(WSMaintenance)
				return r, r.screen.Init()
			}
		case "M":
			// Shift+m opens the preventive-maintenance ITEM list (create/edit
			// PM items + per-item actions). Lowercase m is the Profile
			// shortcut a few branches up, so PM items take the shifted key —
			// same convention as N (new PO) / I (new item).
			if _, ok := r.screen.(*MaintenanceItemsScreen); !ok {
				r.screen = NewMaintenanceItemsScreen(r.deps)
				r.nav.SetActive(WSMaintenance)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "D":
			if _, ok := r.screen.(*DonationsScreen); !ok {
				r.screen = NewDonationsScreen(r.deps)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "F":
			// Shift+f opens the ForgeKey device-type management list (create /
			// edit / delete). Uppercase because it rides the "uppercase letter =
			// open a management surface" convention (G / L / U / V); it sits in
			// the ForgeKey workspace alongside firmware (lowercase f) and e-paper
			// (e), reusing the same upper/lower-of-a-letter pairing as N/n, I/i,
			// A/a — here F = device types, f = firmware, both ForgeKey.
			if _, ok := r.screen.(*DeviceTypeListScreen); !ok {
				r.screen = NewDeviceTypeListScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "G":
			// Shift+g opens the inventory Category management list (create /
			// edit / delete). Uppercase because the taxonomy lists ride the
			// same "uppercase letter = open a surface" convention as V / M / Q,
			// and lowercase g is used for top-of-list nav inside screens.
			if _, ok := r.screen.(*CategoryListScreen); !ok {
				r.screen = NewCategoryListScreen(r.deps)
				r.nav.SetActive(WSInventory)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "L":
			// Shift+l opens the Location management list. Lowercase l is the
			// ForgeKey lockouts global, so locations take the shifted key.
			if _, ok := r.screen.(*LocationListScreen); !ok {
				r.screen = NewLocationListScreen(r.deps)
				r.nav.SetActive(WSInventory)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "U":
			// Shift+u opens the Supplier management list. Lowercase u is the
			// usage-sessions global, so suppliers take the shifted key.
			if _, ok := r.screen.(*SupplierListScreen); !ok {
				r.screen = NewSupplierListScreen(r.deps)
				r.nav.SetActive(WSInventory)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "W":
			// Shift+w opens the Webhook management list (create / edit / delete
			// + test-delivery). Webhooks live under Settings in the web app, so
			// the surface sets the Settings workspace active. Uppercase rides the
			// same "uppercase letter = open a surface" convention as G / V / B.
			if _, ok := r.screen.(*WebhookListScreen); !ok {
				r.screen = NewWebhookListScreen(r.deps)
				r.nav.SetActive(WSSettings)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "T":
			// Shift+t opens the climate Thermostat management list (create /
			// edit / delete). Uppercase like the other management surfaces; both
			// T and lowercase t were free in the global set.
			if _, ok := r.screen.(*ThermostatListScreen); !ok {
				r.screen = NewThermostatListScreen(r.deps)
				r.nav.SetActive(WSFacilities)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "q":
			if _, ok := r.screen.(*WelcomeScreen); ok {
				return r, tea.Quit
			}
		case "esc":
			// Global fallback (a "back button"): pop the back-stack one level,
			// restoring the previous screen and workspace, instead of jumping
			// all the way home. Screens that own esc — forms/pickers via
			// RawInput, or any HandlesKey("esc") — are intercepted above and
			// never reach here, so their local esc-cancel is preserved.
			//
			// backNav tells Update this turn was a back step, so it does not
			// record the screen we are leaving (that would trap esc in a loop).
			r.backNav = true
			if cmd, ok := r.popHistory(); ok {
				return r, cmd
			}
			// Bottom of the stack: from the home screen esc is a no-op (don't
			// trap the user or needlessly rebuild); from any other top-level
			// screen with an empty stack, fall back home.
			if _, ok := r.screen.(*WelcomeScreen); ok {
				return r, nil
			}
			r.screen = NewWelcomeScreen()
			r.nav.SetActive(WSScan)
			return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
		}
		if len(m.String()) == 1 {
			if item, ok := r.nav.ItemForHotkey(rune(m.String()[0])); ok {
				next := newScreenFor(item.Key, r.deps)
				if next != nil {
					r.screen = next
					r.nav.SetActive(item.Key)
					return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
				}
			}
		}

	case SwitchScreenMsg:
		next := m.Screen
		if next == nil {
			// A nil target means "the workspace's default screen" — several
			// create-flows (e.g. PO create, po_create_pickers) use
			// SwitchTo(ws, nil) to leave the form. Resolve it so a nil Screen
			// never nil-derefs Init() and panics the whole program.
			next = newScreenFor(m.Workspace, r.deps)
		}
		if next == nil {
			return r, nil // no default screen for this workspace — stay put, don't crash
		}
		r.screen = next
		r.nav.SetActive(m.Workspace)
		return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())

	case StatusMsg:
		r.status.Flash(m.Text, m.Level, 0)
		return r, nil

	case NotificationPollMsg:
		if m.err == nil {
			r.status.SetUnread(len(m.rows))
		}
		return r, PollNotifications(r.deps, 60*time.Second)
	}

	next, cmd := r.screen.Update(msg)
	r.screen = next
	return r, cmd
}

func (r Root) View() string {
	contentWidth := r.width - r.navWidth - 1
	contentHeight := r.height - 2

	if contentWidth < 20 {
		return "scantty: terminal too narrow"
	}
	if contentHeight < 5 {
		return "scantty: terminal too short"
	}

	navView := r.nav.View(contentHeight)
	titleLine := StyleTitle.Render(r.screen.Title())
	screenView := r.screen.View()

	content := lipgloss.JoinVertical(lipgloss.Left, titleLine, "", screenView)
	// Clamp the joined content to the body budget BEFORE handing it
	// to lipgloss.Render. Without this, a screen that returns more
	// rows than fit (the non-scroller screens — reorder queue, op
	// modes, maker boxes, …) makes the body extend past the box and
	// pushes the whole frame down, which causes the terminal to
	// scroll the nav off the top. StyleContent applies Padding(1,2),
	// so subtract that here so the visible content area is what
	// actually fits.
	innerWidth := contentWidth - 2*2 // horizontal Padding(_, 2)
	innerHeight := contentHeight - 2 // vertical Padding(1, _)
	content = clampToBox(content, innerWidth, innerHeight)
	body := StyleContent.Width(contentWidth).Height(contentHeight).Render(content)

	row := lipgloss.JoinHorizontal(lipgloss.Top, navView, body)
	return lipgloss.JoinVertical(lipgloss.Left, row, r.status.View())
}

func newScreenFor(ws Workspace, deps Deps) Screen {
	switch ws {
	case WSScan:
		return NewScanScreen(deps)
	case WSDashboard:
		return NewDashboardScreen(deps)
	case WSInventory:
		return NewListScreen(deps, "Inventory", listScreenSpec{
			kind:   "inventory_items",
			loader: loadInventoryItems,
			detail: func(id string, d Deps) Screen { return NewInventoryDetailScreen(d, id) },
		})
	case WSPurchasing:
		return NewListScreen(deps, "Purchasing", listScreenSpec{
			kind: "purchase_orders",
			// Filter-driven (f cycles all/draft/sent/…) so a saved DRAFT order
			// is findable and resumable — filters[0] is unfiltered, so the
			// landing list is what it always was. No plain loader: a
			// filter-driven list expresses its unfiltered view as filters[0].
			filters:      purchaseOrderFilters,
			filterLoader: purchaseOrderRows,
			detail:       func(id string, d Deps) Screen { return NewPurchaseOrderDetailScreen(d, id) },
		})
	case WSAssets:
		return NewListScreen(deps, "Assets", listScreenSpec{
			kind:         "assets",
			loader:       loadAssets,
			searchLoader: searchAssets,
			detail:       func(id string, d Deps) Screen { return NewAssetDetailScreen(d, id) },
		})
	case WSFacilities:
		return NewFacilitiesScreen(deps)
	case WSMaintenance:
		return NewListScreen(deps, "Maintenance", listScreenSpec{
			kind:   "work_orders",
			loader: loadWorkOrders,
			detail: func(id string, d Deps) Screen { return NewWorkOrderDetailScreen(d, id) },
		})
	case WSSIGs:
		// The SIGs nav workspace ('7') is now the create/edit/delete surface
		// (SIGListScreen): n new, E edit, x delete, enter drills into member
		// management, v opens the read-only detail. The generic browse list it
		// replaced only supported enter→detail.
		return NewSIGListScreen(deps)
	case WSReports:
		// Reports is a hub menu mirroring the web /reports section: the
		// staff-gated Analytics Pulse, the three report pages (Inventory,
		// Purchasing, Asset) as tabbed tables, and the serialized-component
		// consumption forecast. Each entry opens a scrollable table view.
		return NewReportsScreen(deps)
	case WSForgeKey:
		return NewListScreen(deps, "ForgeKey Devices", listScreenSpec{
			kind:   "fk_devices",
			loader: loadForgeKeyDevices,
			detail: func(id string, d Deps) Screen { return NewForgeKeyDeviceDetailScreen(d, id) },
		})
	case WSSettings:
		return NewSettingsScreen(deps)
	}
	return nil
}
