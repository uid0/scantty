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
	OMS      *omsapi.Client
	ForgeKey *forgekeyapi.Client
	Cache    *cache.Cache
	Ctx      context.Context
	// Health is the shared service-status snapshot: which external
	// dependencies the backend's circuit breakers say are working right now.
	// Root polls it and is the only writer; screens read it to gate a control
	// on the capability it actually needs. NewRoot installs one, so main does
	// not have to. A NIL Health is legal everywhere and reports everything
	// healthy — a screen built with a bare Deps{} gates nothing.
	Health              *ServiceHealth
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
//   - a screen that answers the question itself (a BackStackScreen), which is
//     the only reader whose transience and whose keyboard ownership are allowed
//     to be different states;
//   - forms, pickers and confirm prompts (a RawInputScreen that currently
//     WantsRawInput), which cancel themselves via their own esc; and
//   - any screen that claims esc via HandlesKey (the Reports tables/pulse,
//     which return to the Reports hub on their own).
//
// Those screens are also intercepted before the global esc handler ever runs,
// so their local esc-cancel keeps working unchanged.
//
// THE RAW-INPUT TEST IS A FALLBACK, NOT THE QUESTION. It was the question once,
// and a screen that narrowed WantsRawInput for reasons of its own moved this
// behaviour with it — see BackStackScreen for what that cost. A screen that
// implements BackStackScreen is asked THAT and nothing else.
func (r *Root) recordHistory(screen Screen, ws Workspace) {
	if screen == nil {
		return
	}
	if bs, ok := screen.(BackStackScreen); ok {
		if bs.SkipsBackStack() {
			return
		}
	} else if rs, ok := screen.(RawInputScreen); ok && rs.WantsRawInput() {
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
	// One shared holder for the whole run: every screen Root builds gets this
	// same pointer through its copy of Deps, so the poll below is the only
	// request any of them costs.
	if deps.Health == nil {
		deps.Health = NewServiceHealth()
	}
	r := Root{
		deps:     deps,
		nav:      NewNav(),
		status:   NewStatusBar(),
		navWidth: navColumnWidth,
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
		Status("welcome — tab opens the menu · ctrl+k searches · esc goes back", StatusInfo),
	}
	if poll := PollNotifications(r.deps, 60*time.Second); poll != nil {
		cmds = append(cmds, poll)
	}
	// Ask for the service status straight away rather than waiting out the
	// first interval: a console brought up during an outage should say so on
	// its first frame. The reply re-arms the recurring poll (see dispatch), so
	// this is the loop's only ignition point.
	if fetch := FetchServiceStatus(r.deps); fetch != nil {
		cmds = append(cmds, fetch)
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

// updateNav is the whole key model of the sidebar menu while it holds focus:
// up/down through the tree, left/right between workspaces (the coarse axis —
// ForgeKey alone is seven surfaces deep), home/end to the edges, enter to open,
// tab or esc to hand the keyboard back. The only letters here are j/k/g/G,
// which are not accelerators: they are the app-wide SCROLL vocabulary the
// redesign keeps (scroll.go, list.go), and a menu you move through with arrows
// is a thing you scroll. Nothing here addresses a destination by letter, which
// is the point — the tree is read, not memorised.
func (r Root) updateNav(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.String() {
	case "up", "k":
		r.nav.Move(-1)
	case "down", "j":
		r.nav.Move(+1)
	case "left":
		r.nav.MoveWorkspace(-1)
	case "right":
		r.nav.MoveWorkspace(+1)
	case "home", "g":
		r.nav.MoveToEdge(-1)
	case "end", "G":
		r.nav.MoveToEdge(+1)
	case "enter":
		return r.openNavSelection()
	case "ctrl+k":
		// Search stays reachable from the menu too, rather than making the
		// operator tab out first to press the one key that goes anywhere.
		r.nav.Blur()
		if _, ok := r.screen.(*SearchPalette); !ok {
			r.screen = NewSearchPalette(r.deps)
			return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
		}
	case "esc", "tab":
		r.nav.Blur()
	}
	return r, nil
}

// openNavSelection opens whatever the sidebar cursor is on — a workspace's
// default screen, or one of the surfaces listed beneath it — and hands the
// keyboard back to that screen. A workspace with no default screen (none today,
// but newScreenFor may return nil) leaves the menu where it is rather than
// blanking the pane.
func (r Root) openNavSelection() (tea.Model, tea.Cmd) {
	ws, build := r.nav.Selected()
	var next Screen
	if build != nil {
		next = build(r.deps)
	} else {
		next = newScreenFor(ws, r.deps)
	}
	if next == nil {
		return r, nil
	}
	r.nav.Blur()
	r.screen = next
	r.nav.SetActive(ws)
	return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
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
		// The sidebar menu owns the keyboard while it holds focus — it is a
		// menu, not a decoration, and the screen behind it is not being typed
		// into. Ahead of the raw-input check because focus can only have got
		// here from a screen that was NOT raw (see the `tab` arm below), and
		// opening a form from the menu blurs the sidebar on the way in.
		if r.nav.Focused() {
			return r.updateNav(m)
		}
		// Screens with active textinputs (login, forms, search palette) take
		// every key, so a modal owns its own esc/tab rather than having the
		// root steal them mid-entry.
		if rs, ok := r.screen.(RawInputScreen); ok && rs.WantsRawInput() {
			next, cmd := r.screen.Update(msg)
			r.screen = next
			return r, cmd
		}
		// Give the ACTIVE SCREEN first crack at the key. Since phase 3 the
		// root holds no letter, so a claim no longer rescues a screen-local
		// letter from a colliding global — what it still decides is `esc`
		// (a screen that owns its own back step) and the two keys below.
		// Screens that don't implement LocalKeyScreen, or don't claim this
		// key, fall through to the root's own arms exactly as before.
		if lk, ok := r.screen.(LocalKeyScreen); ok && lk.HandlesKey(m.String()) {
			next, cmd := r.screen.Update(msg)
			r.screen = next
			return r, cmd
		}
		switch m.String() {
		case "ctrl+k":
			// The universal search palette, and the last non-navigational key the
			// root owns. Bare `/` used to open it too; that arm is gone with the
			// letters, because `/` is a character a screen may want and ctrl+k is
			// the modifier-based binding the retained key model is built from.
			if _, ok := r.screen.(*SearchPalette); !ok {
				r.screen = NewSearchPalette(r.deps)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "tab":
			// Move the keyboard into the sidebar menu. This is the ONE key that
			// replaced the ~25 global letter accelerators and the 0-9/s workspace
			// digits: every surface they opened is a row of the tree. The hint
			// rides along because the 24-column pane has no room for a help line
			// and a key nothing on screen names is a key nobody finds.
			r.nav.Focus()
			return r, Status(navFocusHint, StatusInfo)
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

	case ServiceStatusMsg:
		// A failed fetch is NEVER surfaced and never gates: it stores UNKNOWN,
		// which shows nothing and takes no control away. A skipped fetch (no
		// token yet) leaves whatever we had alone rather than clearing it.
		// Either way the loop re-arms — the poll surviving matters more than
		// any single request.
		if !m.Skipped {
			r.deps.Health.Set(m.Status)
			r.status.SetDegradedServices(r.deps.Health.Degraded())
		}
		if m.Manual {
			// The operator's own refresh. It feeds the snapshot but must not
			// re-arm — the recurring poll is one loop, and forking a second
			// one per press would quietly multiply the request rate.
			return r, nil
		}
		return r, PollServiceStatus(r.deps, serviceStatusPollInterval)
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
	// rows than fit (the non-scroller screens — op modes, maker
	// boxes, …) makes the body extend past the box and
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
