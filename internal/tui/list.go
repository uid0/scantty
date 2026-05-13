package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type listScreenSpec struct {
	kind   string
	loader func(ctx context.Context, deps Deps) ([]listRow, error)
	detail func(id string, deps Deps) Screen
}

type listRow struct {
	ID       string
	Title    string
	Subtitle string
	Tag      string
}

type listLoadedMsg struct {
	rows []listRow
	err  error
}

type ListScreen struct {
	deps     Deps
	title    string
	spec     listScreenSpec
	rows     []listRow
	cursor   int
	loading  bool
	loadErr  string
}

func NewListScreen(deps Deps, title string, spec listScreenSpec) *ListScreen {
	return &ListScreen{deps: deps, title: title, spec: spec, loading: true}
}

func (s *ListScreen) Title() string { return s.title }

func (s *ListScreen) Init() tea.Cmd {
	if s.spec.loader == nil {
		s.loading = false
		return nil
	}
	loader := s.spec.loader
	ctx := s.deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	deps := s.deps
	return func() tea.Msg {
		rows, err := loader(ctx, deps)
		return listLoadedMsg{rows: rows, err: err}
	}
}

func (s *ListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case listLoadedMsg:
		s.loading = false
		s.rows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "g":
			s.cursor = 0
		case "G":
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
		case "r":
			s.loading = true
			return s, s.Init()
		case "enter":
			if s.spec.detail == nil || s.cursor >= len(s.rows) {
				return s, nil
			}
			next := s.spec.detail(s.rows[s.cursor].ID, s.deps)
			if next == nil {
				return s, nil
			}
			return s, SwitchTo(workspaceForKind(s.spec.kind), next)
		}
	}
	return s, nil
}

func workspaceForKind(kind string) Workspace {
	switch kind {
	case "inventory_items":
		return WSInventory
	case "assets":
		return WSAssets
	case "purchase_orders":
		return WSPurchasing
	case "work_orders":
		return WSMaintenance
	case "sigs":
		return WSSIGs
	case "fk_devices":
		return WSForgeKey
	}
	return WSDashboard
}

func (s *ListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry")
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No rows.")
	}
	var b strings.Builder
	for i, row := range s.rows {
		marker := "  "
		if i == s.cursor {
			marker = "▸ "
		}
		title := row.Title
		if row.Tag != "" {
			title = title + " " + StyleMuted.Render("("+row.Tag+")")
		}
		line := marker + title
		if i == s.cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
		if row.Subtitle != "" {
			b.WriteString("    ")
			b.WriteString(StyleMuted.Render(row.Subtitle))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	hint := "j/k move · g/G top/bottom · r refresh"
	if s.spec.detail != nil {
		hint += " · enter open"
	}
	b.WriteString(StyleMuted.Render(hint))
	return b.String()
}

func loadInventoryItems(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListItems(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, it := range page.Results {
		subtitle := fmt.Sprintf("SKU %s · stock %d", it.SKU, it.Stock)
		tag := ""
		if it.NeedsReorder {
			tag = "needs-reorder"
		}
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", it.ID),
			Title:    it.Name,
			Subtitle: subtitle,
			Tag:      tag,
		})
	}
	return rows, nil
}

func loadAssets(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListAssets(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, a := range page.Results {
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", a.ID),
			Title:    a.Name,
			Subtitle: a.Description,
			Tag:      a.Status,
		})
	}
	return rows, nil
}

func loadPurchaseOrders(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListPurchaseOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, po := range page.Results {
		title := po.Number
		if title == "" {
			title = fmt.Sprintf("PO #%d", po.ID)
		}
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", po.ID),
			Title:    title,
			Subtitle: fmt.Sprintf("%.2f %s", po.Total, po.Currency),
			Tag:      po.Status,
		})
	}
	return rows, nil
}

func loadWorkOrders(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListWorkOrders(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, wo := range page.Results {
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", wo.ID),
			Title:    wo.Title,
			Subtitle: wo.AssetName,
			Tag:      wo.Status,
		})
	}
	return rows, nil
}

func loadSIGs(ctx context.Context, deps Deps) ([]listRow, error) {
	page, err := deps.OMS.ListSIGs(ctx, nil)
	if err != nil {
		return nil, err
	}
	rows := make([]listRow, 0, len(page.Results))
	for _, sig := range page.Results {
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", sig.ID),
			Title:    sig.Name,
			Subtitle: sig.Description,
			Tag:      sig.Slug,
		})
	}
	return rows, nil
}

func loadForgeKeyDevices(ctx context.Context, deps Deps) ([]listRow, error) {
	devices, err := deps.ForgeKey.ListDevices(ctx, nil)
	if err != nil {
		return nil, err
	}
	return forgekeyDeviceRows(devices), nil
}

func forgekeyDeviceRows(devices []forgekeyapi.Device) []listRow {
	rows := make([]listRow, 0, len(devices))
	for _, d := range devices {
		tag := "offline"
		if d.IsOnline {
			tag = "online"
		}
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", d.ID),
			Title:    d.Name,
			Subtitle: fmt.Sprintf("%s · %s · %s", d.DeviceType, d.MACAddress, d.Location),
			Tag:      tag,
		})
	}
	return rows
}

