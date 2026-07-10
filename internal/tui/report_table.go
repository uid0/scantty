package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Shared report-table rendering + number formatting
//
// The OMS web Reports section (Inventory / Purchasing / Asset pages) is a set
// of tabbed, sortable TABLES. A terminal can't render the analytics charts, so
// scantty mirrors the underlying report DATA as aligned text tables. These
// helpers are shared by the generic tabbed ReportTableScreen (below) and the
// AnalyticsPulseScreen (analytics_pulse.go).
// ---------------------------------------------------------------------------

// colAlign controls per-column text alignment.
type colAlign int

const (
	alignLeft colAlign = iota
	alignRight
)

// reportColumn is one table column: a header and its alignment (numbers right,
// text left).
type reportColumn struct {
	header string
	align  colAlign
}

// reportColWidths returns the display width of each column: the max ANSI-aware
// width across the header and every row cell, so columns stay aligned as the
// table scrolls.
func reportColWidths(cols []reportColumn, rows [][]string) []int {
	w := make([]int, len(cols))
	for i, c := range cols {
		w[i] = lipgloss.Width(c.header)
	}
	for _, row := range rows {
		for i := 0; i < len(cols) && i < len(row); i++ {
			if cw := lipgloss.Width(row[i]); cw > w[i] {
				w[i] = cw
			}
		}
	}
	return w
}

// padCell aligns a cell to width w within its column.
func padCell(s string, w int, align colAlign) string {
	n := w - lipgloss.Width(s)
	if n < 0 {
		n = 0
	}
	if align == alignRight {
		return strings.Repeat(" ", n) + s
	}
	return s + strings.Repeat(" ", n)
}

// joinReportRow renders one row of cells padded to the column widths, joined by
// a two-space gutter.
func joinReportRow(cells []string, widths []int, cols []reportColumn) string {
	parts := make([]string, len(widths))
	for i := range widths {
		cell := ""
		if i < len(cells) {
			cell = cells[i]
		}
		align := alignLeft
		if i < len(cols) {
			align = cols[i].align
		}
		parts[i] = padCell(cell, widths[i], align)
	}
	return strings.Join(parts, "  ")
}

// reportTableLines builds the styled header line and the plain (unstyled) body
// lines for a table. Callers overlay cursor highlighting / windowing as needed.
func reportTableLines(cols []reportColumn, rows [][]string) (header string, body []string) {
	widths := reportColWidths(cols, rows)
	hcells := make([]string, len(cols))
	for i, c := range cols {
		hcells[i] = c.header
	}
	header = StyleMuted.Render(joinReportRow(hcells, widths, cols))
	body = make([]string, len(rows))
	for i, r := range rows {
		body[i] = joinReportRow(r, widths, cols)
	}
	return header, body
}

// --- number / value formatting -------------------------------------------

// commaGroup inserts thousands separators into a plain decimal string like
// "1234.50" → "1,234.50" (tolerates a leading '-' and an optional fraction).
func commaGroup(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, frac = s[:dot], s[dot:]
	}
	n := len(intPart)
	if n > 3 {
		var b strings.Builder
		pre := n % 3
		if pre > 0 {
			b.WriteString(intPart[:pre])
			b.WriteByte(',')
		}
		for i := pre; i < n; i += 3 {
			b.WriteString(intPart[i : i+3])
			if i+3 < n {
				b.WriteByte(',')
			}
		}
		intPart = b.String()
	}
	out := intPart + frac
	if neg {
		out = "-" + out
	}
	return out
}

// fmtMoney formats a float money value (inventory / purchasing reports serialize
// money as JSON floats) as "$1,234.50".
func fmtMoney(f float64) string {
	return "$" + commaGroup(fmt.Sprintf("%.2f", f))
}

// fmtMoneyStr formats a Decimal-as-string money value (asset TCO + analytics
// category_spend serialize money as strings) as "$175.00"; blank → "—".
func fmtMoneyStr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return "$" + commaGroup(s)
}

// orDash returns "—" for an empty/whitespace string, else the string.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// intOrDash renders a *int, using "—" for nil.
func intOrDash(p *int) string {
	if p == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *p)
}

// dateOnly trims an RFC3339 datetime string to its date ("2026-04-01T…" →
// "2026-04-01"); passes through bare dates and "" unchanged.
func dateOnly(s string) string {
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return s
}

// ---------------------------------------------------------------------------
// ReportTableScreen — a generic, tabbed, scrollable set of report tables.
//
// Each tab lazy-loads its rows on first view (loaders return pre-formatted
// string cells so this screen is type-agnostic). ←/→ or tab/shift+tab switch
// tabs; j/k/pgup/pgdn/g/G scroll rows; r refreshes the active tab; esc returns
// to the Reports hub. Read-only — mirrors the web report DATA, not its chart
// widgets (there are none on these pages).
// ---------------------------------------------------------------------------

// reportTab is one tab: a label, its columns, and a row loader.
type reportTab struct {
	label   string
	columns []reportColumn
	loader  func(ctx context.Context, deps Deps) ([][]string, error)
	// note is an optional caption under the table (e.g. the default window).
	note string
}

type reportTabState struct {
	loaded      bool
	loading     bool
	err         string
	rows        [][]string
	cursor      int
	windowStart int
}

type ReportTableScreen struct {
	deps           Deps
	title          string
	tabs           []reportTab
	states         []reportTabState
	active         int
	terminalHeight int
}

type reportTabLoadedMsg struct {
	tab  int
	rows [][]string
	err  error
}

func NewReportTableScreen(deps Deps, title string, tabs []reportTab) *ReportTableScreen {
	return &ReportTableScreen{
		deps:   deps,
		title:  title,
		tabs:   tabs,
		states: make([]reportTabState, len(tabs)),
	}
}

func (s *ReportTableScreen) Title() string { return s.title }

// HandlesKey claims the two keys the global nav layer would otherwise eat: G
// (global = inventory categories) so end-of-table works, and esc so the screen
// returns to the Reports hub rather than jumping to the home screen.
func (s *ReportTableScreen) HandlesKey(key string) bool {
	return key == "G" || key == "esc"
}

func (s *ReportTableScreen) Init() tea.Cmd {
	if len(s.tabs) == 0 {
		return nil
	}
	s.states[s.active].loading = true
	return s.loadTab(s.active)
}

func (s *ReportTableScreen) loadTab(i int) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	loader := s.tabs[i].loader
	return func() tea.Msg {
		rows, err := loader(ctx, deps)
		return reportTabLoadedMsg{tab: i, rows: rows, err: err}
	}
}

func (s *ReportTableScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.scrollIntoView()
		return s, nil
	case reportTabLoadedMsg:
		if m.tab >= 0 && m.tab < len(s.states) {
			st := &s.states[m.tab]
			st.loaded = true
			st.loading = false
			st.rows = m.rows
			if m.err != nil {
				st.err = m.err.Error()
			} else {
				st.err = ""
			}
			if st.cursor >= len(st.rows) {
				st.cursor = len(st.rows) - 1
			}
			if st.cursor < 0 {
				st.cursor = 0
			}
		}
		s.scrollIntoView()
		return s, nil
	case tea.KeyMsg:
		return s.updateKey(m)
	}
	return s, nil
}

func (s *ReportTableScreen) updateKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	if len(s.tabs) == 0 {
		if m.String() == "esc" {
			return s, SwitchTo(WSReports, NewReportsScreen(s.deps))
		}
		return s, nil
	}
	st := &s.states[s.active]
	switch m.String() {
	case "esc", "backspace":
		return s, SwitchTo(WSReports, NewReportsScreen(s.deps))
	case "right", "tab", "]":
		return s.switchTab((s.active + 1) % len(s.tabs))
	case "left", "shift+tab", "[":
		return s.switchTab((s.active - 1 + len(s.tabs)) % len(s.tabs))
	case "j", "down":
		if st.cursor < len(st.rows)-1 {
			st.cursor++
			s.scrollIntoView()
		}
	case "k", "up":
		if st.cursor > 0 {
			st.cursor--
			s.scrollIntoView()
		}
	case "ctrl+d", "pgdown":
		st.cursor += s.windowSize()
		if st.cursor >= len(st.rows) {
			st.cursor = len(st.rows) - 1
		}
		if st.cursor < 0 {
			st.cursor = 0
		}
		s.scrollIntoView()
	case "ctrl+u", "pgup":
		st.cursor -= s.windowSize()
		if st.cursor < 0 {
			st.cursor = 0
		}
		s.scrollIntoView()
	case "g", "home":
		st.cursor = 0
		s.scrollIntoView()
	case "G", "end":
		st.cursor = len(st.rows) - 1
		if st.cursor < 0 {
			st.cursor = 0
		}
		s.scrollIntoView()
	case "r":
		st.loaded = false
		st.loading = true
		st.err = ""
		return s, s.loadTab(s.active)
	}
	return s, nil
}

// switchTab moves to tab i, triggering a lazy load if its rows aren't in hand.
func (s *ReportTableScreen) switchTab(i int) (Screen, tea.Cmd) {
	s.active = i
	s.scrollIntoView()
	st := &s.states[i]
	if !st.loaded && !st.loading {
		st.loading = true
		return s, s.loadTab(i)
	}
	return s, nil
}

// windowSize is how many single-line rows fit under the tab bar + column header.
func (s *ReportTableScreen) windowSize() int {
	if s.terminalHeight <= 0 {
		return listWindowSize
	}
	// chrome: tab bar (1) + blank (1) + column header (1) + note (1) +
	// footer blank+hint (2) + ↑/↓ indicators (2).
	const chrome = 8
	n := screenBodyHeight(s.terminalHeight) - chrome
	if n < 3 {
		n = 3
	}
	return n
}

func (s *ReportTableScreen) scrollIntoView() {
	if len(s.states) == 0 {
		return
	}
	st := &s.states[s.active]
	win := s.windowSize()
	if st.cursor < st.windowStart {
		st.windowStart = st.cursor
	}
	if st.cursor >= st.windowStart+win {
		st.windowStart = st.cursor - win + 1
	}
	if st.windowStart < 0 {
		st.windowStart = 0
	}
	if maxStart := len(st.rows) - win; maxStart > 0 && st.windowStart > maxStart {
		st.windowStart = maxStart
	}
	if len(st.rows) <= win {
		st.windowStart = 0
	}
}

func (s *ReportTableScreen) View() string {
	var b strings.Builder
	b.WriteString(s.renderTabBar() + "\n\n")

	if len(s.tabs) == 0 {
		b.WriteString(StyleMuted.Render("No reports.") + "\n")
		b.WriteString("\n" + StyleMuted.Render("esc back"))
		return b.String()
	}

	tab := s.tabs[s.active]
	st := &s.states[s.active]

	if st.loading || !st.loaded {
		b.WriteString(StyleMuted.Render("Loading " + tab.label + "…"))
		return b.String()
	}
	if st.err != "" {
		b.WriteString(StyleStatusError.Render("Error: ") + st.err + "\n")
		b.WriteString("\n" + StyleMuted.Render("r retry · ←/→ switch report · esc back"))
		return b.String()
	}
	if len(st.rows) == 0 {
		b.WriteString(StyleMuted.Render("No rows for " + tab.label + "."))
		if tab.note != "" {
			b.WriteString("\n" + StyleMuted.Render(tab.note))
		}
		b.WriteString("\n\n" + StyleMuted.Render("←/→ switch report · r refresh · esc back"))
		return b.String()
	}

	header, body := reportTableLines(tab.columns, st.rows)
	b.WriteString(header + "\n")

	win := s.windowSize()
	end := st.windowStart + win
	if end > len(body) {
		end = len(body)
	}
	if st.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	for i := st.windowStart; i < end; i++ {
		line := "  " + body[i]
		if i == st.cursor {
			line = StyleSidebarItemActive.Render("▸ " + body[i])
		}
		b.WriteString(line + "\n")
	}
	if end < len(body) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(body)-end)) + "\n")
	}

	if tab.note != "" {
		b.WriteString(StyleMuted.Render(tab.note) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render(fmt.Sprintf("%d row(s) · j/k move · ←/→ switch report · r refresh · esc back", len(st.rows))))
	return b.String()
}

func (s *ReportTableScreen) renderTabBar() string {
	parts := make([]string, len(s.tabs))
	for i, t := range s.tabs {
		label := fmt.Sprintf(" %s ", t.label)
		if i == s.active {
			parts[i] = StyleSidebarItemActive.Render(label)
		} else {
			parts[i] = StyleMuted.Render(label)
		}
	}
	return strings.Join(parts, StyleMuted.Render("│"))
}

// ---------------------------------------------------------------------------
// Concrete report screens (the three web report pages)
// ---------------------------------------------------------------------------

// NewInventoryReportScreen mirrors the web /reports/inventory page: 3 tabs.
func NewInventoryReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Inventory report", []reportTab{
		{
			label:   "Stock by category",
			columns: []reportColumn{{"Category", alignLeft}, {"Items", alignRight}, {"Stock", alignRight}, {"Value", alignRight}, {"Low", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.InventoryStockByCategory(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.CategoryName), itoa(r.TotalItems), itoa(r.TotalStock), fmtMoney(r.TotalValue), itoa(r.LowStockCount)}
				}
				return out, nil
			},
		},
		{
			label:   "Reorder frequency",
			columns: []reportColumn{{"Item", alignLeft}, {"SKU", alignLeft}, {"Category", alignLeft}, {"Reorders", alignRight}},
			note:    "Default window: trailing 12 months (the web default).",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.InventoryReorderFrequency(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.ItemName), orDash(r.ItemSKU), orDash(r.CategoryName), itoa(r.ReorderCount)}
				}
				return out, nil
			},
		},
		{
			label:   "Value by location",
			columns: []reportColumn{{"Location", alignLeft}, {"Items", alignRight}, {"Stock", alignRight}, {"Value", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.InventoryValueByLocation(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.LocationName), itoa(r.TotalItems), itoa(r.TotalStock), fmtMoney(r.TotalValue)}
				}
				return out, nil
			},
		},
	})
}

// NewPurchasingReportScreen mirrors the web /reports/purchasing page: 4 tabs.
func NewPurchasingReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Purchasing report", []reportTab{
		{
			label:   "Spend by supplier",
			columns: []reportColumn{{"Supplier", alignLeft}, {"Orders", alignRight}, {"Spend", alignRight}, {"Avg order", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.PurchasingSpendBySupplier(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.SupplierName), itoa(r.TotalOrders), fmtMoney(r.TotalSpend), fmtMoney(r.AvgOrderValue)}
				}
				return out, nil
			},
		},
		{
			label:   "Spend by category",
			columns: []reportColumn{{"Category", alignLeft}, {"Items", alignRight}, {"Qty", alignRight}, {"Spend", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.PurchasingSpendByCategory(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.CategoryName), itoa(r.TotalItems), itoa(r.TotalQuantity), fmtMoney(r.TotalSpend)}
				}
				return out, nil
			},
		},
		{
			label:   "Lead time",
			columns: []reportColumn{{"Supplier", alignLeft}, {"Item", alignLeft}, {"Orders", alignRight}, {"Est", alignRight}, {"Actual", alignRight}, {"Var", alignRight}, {"On-time", alignRight}},
			note:    "Lead times in days. Default window: trailing 6 months.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.PurchasingLeadTimeAnalysis(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					// on_time_rate is ALREADY a percentage (backend computes
					// on_time_count/total*100), so append "%" — do NOT ×100 again.
					out[i] = []string{orDash(r.SupplierName), orDash(r.ItemName), itoa(r.TotalOrders), trimFloat(r.AvgEstimatedLeadTime), trimFloat(r.AvgActualLeadTime), trimFloat(r.AvgVariance), trimFloat(r.OnTimeRate) + "%"}
				}
				return out, nil
			},
		},
		{
			label:   "Price trends",
			columns: []reportColumn{{"Item", alignLeft}, {"Supplier", alignLeft}, {"Changes", alignRight}, {"Min", alignRight}, {"Max", alignRight}, {"Latest", alignRight}, {"Change", alignRight}},
			note:    "Default window: trailing 12 months.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.PurchasingPriceTrends(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					change := "—"
					if r.PriceChangePercentage != nil {
						change = trimFloat(*r.PriceChangePercentage) + "%"
					}
					out[i] = []string{orDash(r.ItemName), orDash(r.SupplierName), itoa(r.PriceChanges), fmtMoney(r.MinUnitCost), fmtMoney(r.MaxUnitCost), fmtMoney(r.LatestUnitCost), change}
				}
				return out, nil
			},
		},
	})
}

// NewAssetReportScreen mirrors the web /reports/assets page: 5 tabs.
func NewAssetReportScreen(deps Deps) *ReportTableScreen {
	return NewReportTableScreen(deps, "Asset report", []reportTab{
		{
			label:   "By status",
			columns: []reportColumn{{"Status", alignLeft}, {"Count", alignRight}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.AssetsByStatus(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					label := r.StatusDisplay
					if label == "" {
						label = r.Status
					}
					out[i] = []string{orDash(label), itoa(r.Count)}
				}
				return out, nil
			},
		},
		{
			label:   "Maintenance due",
			columns: []reportColumn{{"Asset", alignLeft}, {"Tag", alignLeft}, {"Part", alignLeft}, {"SKU", alignLeft}, {"Interval d", alignRight}, {"Since d", alignRight}, {"Overdue d", alignRight}, {"Last replaced", alignLeft}},
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.AssetMaintenanceDue(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					part := r.PartName
					if r.Status == "in_maintenance" {
						part = "(in maintenance)"
					}
					out[i] = []string{orDash(r.AssetName), orDash(r.AssetTag), orDash(part), orDash(r.PartSKU), intOrDash(r.MaintenanceIntervalDays), intOrDash(r.DaysSinceReplacement), intOrDash(r.DaysOverdue), orDash(dateOnly(r.LastReplacedAt))}
				}
				return out, nil
			},
		},
		{
			label:   "Utilization",
			columns: []reportColumn{{"Asset", alignLeft}, {"Tag", alignLeft}, {"Sessions", alignRight}, {"Hours", alignRight}, {"Avg/session", alignRight}},
			note:    "ForgeKey session hours. Default window: trailing 30 days.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.AssetUtilization(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.AssetName), orDash(r.AssetTag), itoa(r.TotalSessions), trimFloat(r.TotalHours), trimFloat(r.AvgHoursPerSession)}
				}
				return out, nil
			},
		},
		{
			label:   "Total cost of ownership",
			columns: []reportColumn{{"Asset", alignLeft}, {"Tag", alignLeft}, {"Maint d/90", alignRight}, {"Scheduled", alignRight}, {"Unscheduled", alignRight}, {"Preventive", alignRight}, {"Vendor", alignRight}, {"Total 90d", alignRight}},
			note:    "Costs over the trailing 90 days.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				rows, err := deps.OMS.AssetTCO(ctx)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					out[i] = []string{orDash(r.AssetName), orDash(r.AssetTag), itoa(r.MaintenanceDaysLast90), fmtMoneyStr(r.ScheduledMaintenanceCost), fmtMoneyStr(r.UnscheduledMaintenanceCost), fmtMoneyStr(r.PreventiveMaintenanceCost), fmtMoneyStr(r.VendorMaintenanceCost), fmtMoneyStr(r.TotalMaintenanceCost90d)}
				}
				return out, nil
			},
		},
		{
			label:   "Supplies used",
			columns: []reportColumn{{"Asset", alignLeft}, {"Source", alignLeft}, {"Item", alignLeft}, {"Serial/Qty", alignLeft}, {"Action", alignLeft}, {"Used at", alignLeft}, {"Cost", alignRight}},
			note:    "Serialized installs/consumes + PM-work-order consumables. Default window: trailing 30 days.",
			loader: func(ctx context.Context, deps Deps) ([][]string, error) {
				// nil query → server default window (trailing 30 days), matching
				// how the other windowed tabs mirror the web's default view.
				rows, err := deps.OMS.AssetSuppliesUsed(ctx, nil)
				if err != nil {
					return nil, err
				}
				out := make([][]string, len(rows))
				for i, r := range rows {
					// Serial/Qty column folds the two source shapes: a serial
					// number for serialized rows, quantity+unit for consumables.
					serialQty := r.SerialNumber
					if r.Source == "consumable" {
						serialQty = strings.TrimSpace(r.Quantity + " " + r.Unit)
					}
					action := r.ActionDisplay
					if action == "" {
						action = r.Action // consumable rows have no action verb → "—"
					}
					out[i] = []string{orDash(r.AssetName), orDash(r.Source), orDash(r.ItemName), orDash(serialQty), orDash(action), orDash(dateOnly(r.UsedAt)), fmtMoneyStr(r.EstimatedCost)}
				}
				return out, nil
			},
		},
	})
}

// itoa renders an int as a decimal string, used across the report loaders.
func itoa(n int) string { return fmt.Sprintf("%d", n) }
