package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

type listScreenSpec struct {
	kind   string
	loader func(ctx context.Context, deps Deps) ([]listRow, error)
	detail func(id string, deps Deps) Screen
}

type listRow struct {
	ID        string
	Title     string
	Subtitle  string
	Tag       string
	CreatedAt time.Time
	// Optional secondary timestamp shown in the date column when CreatedAt is
	// zero (e.g. for resources that only expose updated_at or last_seen).
	FallbackDate time.Time
}

func (r listRow) sortDate() time.Time {
	if !r.CreatedAt.IsZero() {
		return r.CreatedAt
	}
	return r.FallbackDate
}

type listSortMode int

const (
	sortDefault listSortMode = iota
	sortDateDesc
	sortDateAsc
	sortTitleAsc
)

func (m listSortMode) label() string {
	switch m {
	case sortDateDesc:
		return "newest"
	case sortDateAsc:
		return "oldest"
	case sortTitleAsc:
		return "title"
	}
	return "default"
}

type listLoadedMsg struct {
	rows []listRow
	err  error
}

const listWindowSize = 20

type ListScreen struct {
	deps        Deps
	title       string
	spec        listScreenSpec
	rawRows     []listRow
	rows        []listRow
	cursor      int
	windowStart int
	windowSize  int
	sort        listSortMode
	loading     bool
	loadErr     string
}

func NewListScreen(deps Deps, title string, spec listScreenSpec) *ListScreen {
	return &ListScreen{
		deps:       deps,
		title:      title,
		spec:       spec,
		loading:    true,
		windowSize: listWindowSize,
		sort:       sortDateDesc,
	}
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

func (s *ListScreen) applySort() {
	rows := make([]listRow, len(s.rawRows))
	copy(rows, s.rawRows)
	switch s.sort {
	case sortDateDesc:
		sort.SliceStable(rows, func(i, j int) bool {
			return rows[i].sortDate().After(rows[j].sortDate())
		})
	case sortDateAsc:
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].sortDate(), rows[j].sortDate()
			if a.IsZero() {
				return false
			}
			if b.IsZero() {
				return true
			}
			return a.Before(b)
		})
	case sortTitleAsc:
		sort.SliceStable(rows, func(i, j int) bool {
			return strings.ToLower(rows[i].Title) < strings.ToLower(rows[j].Title)
		})
	}
	s.rows = rows
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.scrollIntoView()
}

func (s *ListScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = listWindowSize
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if maxStart := len(s.rows) - s.windowSize; maxStart > 0 && s.windowStart > maxStart {
		s.windowStart = maxStart
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *ListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		// Reserve rows for: page title (2), sort header (1), top scroll
		// indicator (1), per-row subtitle (~1 each, hard to estimate
		// without measuring — use half the window), and footer hint
		// (2). With subtitles ~doubling row count, halve the available
		// rows for the cursor window.
		avail := m.Height - 6
		if avail < 6 {
			avail = 6
		}
		s.windowSize = avail / 2
		if s.windowSize < 4 {
			s.windowSize = 4
		}
		s.scrollIntoView()
		return s, nil
	case listLoadedMsg:
		s.loading = false
		s.rawRows = m.rows
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
		}
		s.applySort()
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
				s.scrollIntoView()
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
				s.scrollIntoView()
			}
		case "ctrl+d", "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "ctrl+u", "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "g", "home":
			s.cursor = 0
			s.scrollIntoView()
		case "G", "end":
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "s":
			s.sort = (s.sort + 1) % 4
			s.applySort()
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
	b.WriteString(StyleMuted.Render(fmt.Sprintf("Sort: %s · %d rows", s.sort.label(), len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}

	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		row := s.rows[i]
		marker := "  "
		if i == s.cursor {
			marker = "▸ "
		}
		title := row.Title
		if row.Tag != "" {
			title = title + " " + StyleMuted.Render("("+row.Tag+")")
		}
		date := ""
		if d := row.sortDate(); !d.IsZero() {
			date = StyleMuted.Render(d.Format("2006-01-02") + "  ")
		}
		line := marker + date + title
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

	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}

	b.WriteString("\n")
	hint := "j/k move · pgup/pgdn page · g/G top/bottom · s sort · r refresh"
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
			ID:           it.ID,
			Title:        it.Name,
			Subtitle:     subtitle,
			Tag:          tag,
			CreatedAt:    it.CreatedAt,
			FallbackDate: it.UpdatedAt,
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
		subtitle := a.Description
		if a.AssetTag != "" {
			if subtitle != "" {
				subtitle = a.AssetTag + " · " + subtitle
			} else {
				subtitle = a.AssetTag
			}
		}
		fallback := a.UpdatedAt
		if a.LastScannedAt != nil && !a.LastScannedAt.IsZero() {
			fallback = *a.LastScannedAt
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(a.ID),
			Title:        a.Name,
			Subtitle:     subtitle,
			Tag:          a.Status,
			CreatedAt:    a.CreatedAt,
			FallbackDate: fallback,
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
			title = fmt.Sprintf("PO #%v", po.ID)
		}
		subtitle := poListSubtitle(po)
		created := po.CreatedAt
		if created.IsZero() {
			created = po.OrderDate
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(po.ID),
			Title:        title,
			Subtitle:     subtitle,
			Tag:          po.Status,
			CreatedAt:    created,
			FallbackDate: po.UpdatedAt,
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
			ID:           fmt.Sprint(wo.ID),
			Title:        wo.Title,
			Subtitle:     wo.AssetName,
			Tag:          wo.Status,
			CreatedAt:    wo.CreatedAt,
			FallbackDate: wo.UpdatedAt,
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
		subtitleParts := []string{}
		if sig.MemberCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d members", sig.MemberCount))
		}
		if sig.AssetCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d assets", sig.AssetCount))
		}
		if sig.InventoryCount > 0 {
			subtitleParts = append(subtitleParts, fmt.Sprintf("%d items", sig.InventoryCount))
		}
		tag := ""
		if sig.IsUserAdmin {
			tag = "admin"
		}
		rows = append(rows, listRow{
			ID:       fmt.Sprintf("%d", sig.ID),
			Title:    sig.Name,
			Subtitle: strings.Join(subtitleParts, " · "),
			Tag:      tag,
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
		typeLabel := d.DeviceTypeName
		if typeLabel == "" {
			typeLabel = fmt.Sprintf("%v", d.DeviceType)
		}
		subtitle := fmt.Sprintf("%s · %s", typeLabel, d.MACAddress)
		if d.Location != nil {
			subtitle += fmt.Sprintf(" · loc #%d", *d.Location)
		}
		rows = append(rows, listRow{
			ID:           fmt.Sprint(d.ID),
			Title:        d.Name,
			Subtitle:     subtitle,
			Tag:          tag,
			FallbackDate: d.LastSeen,
		})
	}
	return rows
}

func poListSubtitle(po omsapi.PurchaseOrder) string {
	parts := []string{}
	supplier := po.SupplierName
	if supplier == "" {
		supplier = po.SupplierDetails
	}
	if supplier != "" {
		parts = append(parts, supplier)
	}
	if !po.EstimatedTotal.Empty() {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		parts = append(parts, "$"+string(po.EstimatedTotal)+" "+curr)
	} else if po.Total > 0 {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		parts = append(parts, fmt.Sprintf("%.2f %s", po.Total, curr))
	}
	if po.TotalItems > 0 {
		parts = append(parts, fmt.Sprintf("%d items", po.TotalItems))
	}
	return strings.Join(parts, " · ")
}
