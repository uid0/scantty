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
	OMS          *omsapi.Client
	ForgeKey     *forgekeyapi.Client
	Cache        *cache.Cache
	Ctx          context.Context
	InitialStaff bool
}

type Root struct {
	deps     Deps
	nav      Nav
	status   StatusBar
	screen   Screen
	width    int
	height   int
	navWidth int
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

func (r Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case "s":
			// Screens that advertise lowercase `s` in their on-screen
			// prompt (maker-boxes bin+user scan, list-screen sort,
			// PO-detail send-to-supplier) claim the key here so the
			// ItemForHotkey('s') fallthrough below doesn't whisk the
			// operator off to WSSettings. Settings is still reachable
			// from every other screen.
			switch r.screen.(type) {
			case *MakerBoxesScreen, *ListScreen, *PurchaseOrderDetailScreen:
				next, cmd := r.screen.Update(msg)
				r.screen = next
				return r, cmd
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
		case "V":
			if _, ok := r.screen.(*VendorsScreen); !ok {
				r.screen = NewVendorsScreen(r.deps)
				r.nav.SetActive(WSMaintenance)
				return r, r.screen.Init()
			}
		case "D":
			if _, ok := r.screen.(*DonationsScreen); !ok {
				r.screen = NewDonationsScreen(r.deps)
				return r, tea.Batch(r.screen.Init(), r.windowResizeCmd())
			}
		case "q":
			if _, ok := r.screen.(*WelcomeScreen); ok {
				return r, tea.Quit
			}
		case "esc":
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
		r.screen = m.Screen
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
			kind:   "purchase_orders",
			loader: loadPurchaseOrders,
			detail: func(id string, d Deps) Screen { return NewPurchaseOrderDetailScreen(d, id) },
		})
	case WSAssets:
		return NewListScreen(deps, "Assets", listScreenSpec{
			kind:   "assets",
			loader: loadAssets,
			detail: func(id string, d Deps) Screen { return NewAssetDetailScreen(d, id) },
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
		return NewListScreen(deps, "SIGs", listScreenSpec{
			kind:   "sigs",
			loader: loadSIGs,
			detail: func(id string, d Deps) Screen { return NewSIGDetailScreen(d, id) },
		})
	case WSReports:
		// Reports currently surfaces the serialized-component consumption
		// forecast (days-until-stockout / reorder point / low-stock). It's
		// the first populated report; others can join via a report picker
		// later.
		return NewSerializedForecastScreen(deps)
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
