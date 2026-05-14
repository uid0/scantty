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
	deps    Deps
	nav     Nav
	status  StatusBar
	screen  Screen
	width   int
	height  int
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

func (r Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = m.Width, m.Height
		r.nav.SetWidth(r.navWidth)
		r.status.SetWidth(r.width)
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
				return r, r.screen.Init()
			}
		case "m":
			if _, ok := r.screen.(*ProfileScreen); !ok {
				r.screen = NewProfileScreen(r.deps)
				return r, r.screen.Init()
			}
		case "a":
			if _, ok := r.screen.(*AuthorizationsScreen); !ok {
				r.screen = NewAuthorizationsScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, r.screen.Init()
			}
		case "l":
			if _, ok := r.screen.(*LockoutsScreen); !ok {
				r.screen = NewLockoutsScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, r.screen.Init()
			}
		case "n":
			if _, ok := r.screen.(*NotificationsScreen); !ok {
				r.screen = NewNotificationsScreen(r.deps)
				return r, r.screen.Init()
			}
		case "o":
			if _, ok := r.screen.(*OperationalModesScreen); !ok {
				r.screen = NewOperationalModesScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, r.screen.Init()
			}
		case "u":
			if _, ok := r.screen.(*UsageScreen); !ok {
				r.screen = NewUsageScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, r.screen.Init()
			}
		case "f":
			if _, ok := r.screen.(*FirmwareScreen); !ok {
				r.screen = NewFirmwareScreen(r.deps)
				r.nav.SetActive(WSForgeKey)
				return r, r.screen.Init()
			}
		case "Q":
			if _, ok := r.screen.(*ReorderQueueScreen); !ok {
				r.screen = NewReorderQueueScreen(r.deps)
				r.nav.SetActive(WSPurchasing)
				return r, r.screen.Init()
			}
		case "D":
			if _, ok := r.screen.(*DonationsScreen); !ok {
				r.screen = NewDonationsScreen(r.deps)
				return r, r.screen.Init()
			}
		case "q":
			if _, ok := r.screen.(*WelcomeScreen); ok {
				return r, tea.Quit
			}
		case "esc":
			r.screen = NewWelcomeScreen()
			r.nav.SetActive(WSScan)
			return r, r.screen.Init()
		}
		if len(m.String()) == 1 {
			if item, ok := r.nav.ItemForHotkey(rune(m.String()[0])); ok {
				next := newScreenFor(item.Key, r.deps)
				if next != nil {
					r.screen = next
					r.nav.SetActive(item.Key)
					return r, r.screen.Init()
				}
			}
		}

	case SwitchScreenMsg:
		r.screen = m.Screen
		r.nav.SetActive(m.Workspace)
		return r, r.screen.Init()

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
		return NewListScreen(deps, "Facilities", listScreenSpec{kind: "facilities"})
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
		return NewListScreen(deps, "Reports", listScreenSpec{kind: "reports"})
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

