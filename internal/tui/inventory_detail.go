package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// cycleCountStep tracks where the operator is in the in-screen cycle-count
// prompt (issue-7). ccStepNone is the zero value: no modal open.
type cycleCountStep int

const (
	ccStepNone cycleCountStep = iota
	ccStepQty
	ccStepReason
	ccStepNotes
)

type InventoryDetailScreen struct {
	deps             Deps
	itemID           string
	item             *omsapi.Item
	metrics          *omsapi.ItemMetrics
	loadErr          string
	loading          bool
	scroller         *TextScroller
	terminalHeight   int
	confirmingDelete bool
	deleting         bool

	// Cycle-count modal (issue-7). Active while ccStep != ccStepNone, during
	// which WantsRawInput routes every key here. The two textinputs are
	// (re)initialised each time the modal opens.
	ccStep     cycleCountStep
	ccQty      textinput.Model
	ccNotes    textinput.Model
	ccReasonIx int
	ccErr      string
	ccPending  bool
}

type inventoryDetailLoadedMsg struct {
	item *omsapi.Item
	err  error
}

// inventoryMetricsLoadedMsg carries the metrics-row snapshot, which loads from a
// separate endpoint in parallel with the item (issue-5). A non-nil err is
// non-fatal — the row is simply omitted.
type inventoryMetricsLoadedMsg struct {
	metrics *omsapi.ItemMetrics
	err     error
}

// cycleCountDoneMsg is the result of a cycle-count submission (issue-7). On
// success item is the re-serialized item with the updated stock + count fields.
type cycleCountDoneMsg struct {
	item *omsapi.Item
	err  error
}

type inventoryDeletedMsg struct {
	err error
}

func NewInventoryDetailScreen(deps Deps, id string) *InventoryDetailScreen {
	return &InventoryDetailScreen{
		deps:     deps,
		itemID:   id,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *InventoryDetailScreen) Title() string {
	if s.item != nil {
		return fmt.Sprintf("Item: %s", s.item.Name)
	}
	return "Item"
}

// WantsRawInput claims every keypress while a modal is up — the delete
// confirmation (y/n/esc) or the cycle-count prompt (digits, j/k, notes, esc) —
// so those keys land here instead of the root's global hotkeys. In the normal
// view the screen stays non-raw so workspace switching and the global shortcuts
// keep working.
func (s *InventoryDetailScreen) WantsRawInput() bool {
	return s.confirmingDelete || s.ccStep != ccStepNone
}

// HandlesKey claims lowercase 's' (manage suppliers) so it beats the global
// Settings nav hotkey — the sc-k7p LocalKeyScreen pattern. Only consulted in the
// normal view (the delete confirm flips WantsRawInput true, routing every key
// here first).
func (s *InventoryDetailScreen) HandlesKey(key string) bool { return key == "s" }

func (s *InventoryDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// loadItemCmd and loadMetricsCmd fetch the two halves of the detail
// independently: the item serializer and the metrics snapshot come from
// different endpoints, so they load in parallel and each re-renders the body as
// it arrives (see Update). A metrics failure is non-fatal.
func (s *InventoryDetailScreen) loadItemCmd() tea.Cmd {
	deps, id, ctx := s.deps, s.itemID, s.ctx()
	return func() tea.Msg {
		item, err := deps.OMS.GetItem(ctx, id)
		return inventoryDetailLoadedMsg{item: item, err: err}
	}
}

func (s *InventoryDetailScreen) loadMetricsCmd() tea.Cmd {
	deps, id, ctx := s.deps, s.itemID, s.ctx()
	return func() tea.Msg {
		mtr, err := deps.OMS.GetItemMetrics(ctx, id)
		return inventoryMetricsLoadedMsg{metrics: mtr, err: err}
	}
}

func (s *InventoryDetailScreen) Init() tea.Cmd {
	return tea.Batch(s.loadItemCmd(), s.loadMetricsCmd())
}

func (s *InventoryDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case inventoryDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.item = m.item
		if s.item != nil {
			s.scroller.Set(s.renderBody())
		}
		return s, nil
	case inventoryMetricsLoadedMsg:
		// Best-effort: keep the last good metrics on error so a transient
		// failure doesn't blank an already-shown row. Re-render only once the
		// item is present (metrics can arrive first).
		if m.err == nil {
			s.metrics = m.metrics
		}
		if s.item != nil {
			s.scroller.Set(s.renderBody())
		}
		return s, nil
	case cycleCountDoneMsg:
		s.ccPending = false
		if m.err != nil {
			// Keep the modal open on the notes step so the operator can retry
			// or esc out; surface the reason inline and in the status bar.
			s.ccStep = ccStepNotes
			s.ccErr = "cycle count failed: " + m.err.Error()
			return s, Status(s.ccErr, StatusError)
		}
		s.closeCycleCount()
		// The cycle-count response is a PARTIAL item (id/current_stock/
		// last_counted_at/days_since_last_count only) — assigning it to s.item
		// would blank name/SKU/suppliers/costs. Re-fetch the full item AND the
		// metrics instead; both changed with the new stock level.
		s.scroller.Set(s.renderBody())
		return s, tea.Batch(Status("count recorded", StatusOK), s.loadItemCmd(), s.loadMetricsCmd())
	case inventoryDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("item deleted", StatusOK),
			SwitchTo(WSInventory, newScreenFor(WSInventory, s.deps)),
		)
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.ccStep != ccStepNone {
			return s.updateCycleCount(m)
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "o", "enter":
			if s.item != nil {
				return s, SwitchTo(WSInventory, NewReorderFormScreen(s.deps, s.item, s.item.Suppliers))
			}
		case "c":
			// Cycle count (issue-7): record a physical count. Lowercase c is
			// free in the global hotkey map, so it falls through to the screen.
			if s.item != nil {
				return s.openCycleCount()
			}
		case "i":
			// Serialized items expose per-unit instance tracking; jump to
			// the instances screen. No-op for non-serialized items.
			if s.item != nil && s.item.IsSerialized {
				return s, SwitchTo(WSInventory, NewItemInstancesScreen(s.deps, s.item.ID, s.item.Name, s.item.SerializedStock))
			}
		case "b":
			// Batch-scan serials: rapid-fire scanner-gun capture that
			// creates-and-receives each unit. Serialized items only; lowercase
			// b is free in the global hotkey map, so it falls through here.
			if s.item != nil && s.item.IsSerialized {
				return s, SwitchTo(WSInventory, NewBatchScanSerialsScreen(s.deps, s.item.ID, s.item.Name))
			}
		case "E":
			// Edit opens the create/edit form in edit mode. Uppercase E
			// because lowercase e is a global ForgeKey hotkey.
			if s.item != nil {
				return s, SwitchTo(WSInventory, NewInventoryItemFormScreen(s.deps, s.item.ID))
			}
		case "s":
			// Manage the item's supplier links (add/edit/remove/set-primary).
			// 's' is claimed via HandlesKey so it beats the global Settings nav.
			if s.item != nil {
				return s, SwitchTo(WSInventory, NewItemSuppliersScreen(s.deps, s.item.ID, s.item.Name))
			}
		case "x":
			// Delete (with confirm). The web supports item delete; guard it
			// behind a y/n prompt since it's destructive.
			if s.item != nil {
				s.confirmingDelete = true
				return s, nil
			}
		}
	}
	return s, nil
}

// updateConfirmDelete handles the y/n prompt shown before deleting an item.
// The screen is in raw-input mode here (WantsRawInput), so esc/n reach us
// instead of the root's global handlers.
func (s *InventoryDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		if s.item == nil {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := s.item.ID
		return s, func() tea.Msg {
			return inventoryDeletedMsg{err: deps.OMS.DeleteInventoryItem(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *InventoryDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading item…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry")
	}
	if s.item == nil {
		return StyleMuted.Render("Item not found.")
	}

	// Frozen header: the name / full SKU+ID / metrics row stay pinned above the
	// scrolling body (Ian UX). Size the scroller so the header, the blank
	// separator beneath it, and the footer all fit without the body clipping the
	// bottom. The header height is dynamic (two lines until the metrics row
	// arrives), so measure it every render.
	header := s.renderHeader()
	headerRows := strings.Count(header, "\n") + 1
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows+headerRows+1))
	body := s.scroller.View()

	if s.confirmingDelete {
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete %q? This can't be undone.  y delete · n/esc cancel", s.item.Name))
		}
		return header + "\n\n" + body + "\n\n" + prompt
	}
	if s.ccStep != ccStepNone {
		return header + "\n\n" + body + "\n\n" + s.cycleCountPrompt()
	}
	hint := "j/k scroll · o/enter reorder · c count · s suppliers · E edit · x delete · r refresh · esc back"
	if s.item.IsSerialized {
		hint = "j/k scroll · o/enter reorder · c count · s suppliers · i instances · b batch-scan · E edit · x delete · r refresh · esc back"
	}
	return header + "\n\n" + body + "\n\n" + StyleMuted.Render(hint)
}

// renderHeader builds the frozen top-of-detail region that stays pinned while
// the body scrolls beneath it (Ian UX): three lines — the item name, the full
// SKU/ID identity line, and the aligned Q's & Costs metrics row. Returned with
// no trailing newline; View() counts its lines to size the scroller. The metrics
// line is omitted until the metrics endpoint responds (an older backend without
// it simply shows a two-line header).
func (s *InventoryDetailScreen) renderHeader() string {
	it := s.item
	if it == nil {
		return ""
	}
	var b strings.Builder

	// Line 1: item name (+ reorder flags).
	b.WriteString(StyleTitle.Render(it.Name))
	if it.NeedsReorder {
		b.WriteString("  " + StyleStatusWarn.Render("needs reorder"))
	}
	if it.HasPendingReorder {
		b.WriteString("  " + StyleMuted.Render("[reorder pending]"))
	}

	// Line 2: full SKU + ID (+ category/location). The full SKU lives here, so
	// the metrics row below drops its redundant shortened-SKU cell.
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("SKU %s · ID %s", it.SKU, it.ID)))
	if it.CategoryName != "" {
		b.WriteString(StyleMuted.Render(" · " + it.CategoryName))
	}
	if it.Location != "" {
		b.WriteString(StyleMuted.Render(" · " + it.Location))
	}

	// Line 3: aligned metrics row (issue-5) — no SKU cell (de-dup with line 2),
	// bold labels. Only once the metrics endpoint responds; a fetch failure
	// (e.g. an older backend without the endpoint) omits the whole line.
	if s.metrics != nil {
		b.WriteString("\n")
		b.WriteString(formatItemMetricsRow(s.metrics, it.SKU, metricsRowOpts{boldLabels: true}))
	}

	return b.String()
}

// renderBody builds the scrolling detail below the frozen header (see
// renderHeader). It starts at the description/stock sections — the name, SKU/ID
// and metrics row are rendered by the pinned header instead.
func (s *InventoryDetailScreen) renderBody() string {
	it := s.item
	if it == nil {
		// Metrics can arrive before the item; never dereference a nil item.
		return ""
	}
	var b strings.Builder

	if it.Description != "" {
		b.WriteString(it.Description + "\n\n")
	}

	b.WriteString(StyleTitle.Render("Stock") + "\n")
	b.WriteString(fmt.Sprintf("Current stock: %d", it.Stock))
	if it.MinimumStock > 0 {
		b.WriteString(fmt.Sprintf("  ·  Minimum: %d", it.MinimumStock))
	}
	if it.ReorderQuantity > 0 {
		b.WriteString(fmt.Sprintf("  ·  Reorder qty: %d", it.ReorderQuantity))
	}
	b.WriteString("\n")
	// Days-since-last-count (issue-7): "Counted: 12d ago" / "Counted: never".
	b.WriteString(StyleMuted.Render(metricsCountedLine(it)) + "\n")
	if it.ReorderStatus != "" {
		b.WriteString(StyleMuted.Render("Reorder status: ") + it.ReorderStatus + "\n")
	}
	if it.ExpectedDeliveryDate != "" {
		b.WriteString(StyleMuted.Render("Expected delivery: ") + it.ExpectedDeliveryDate + "\n")
	}
	if it.UseCaseBasedReorder {
		b.WriteString(StyleMuted.Render("Case-based reorder: Yes") + "\n")
		if it.CurrentCases != nil {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("Current cases: %.2f", *it.CurrentCases)) + "\n")
		}
		if it.MinimumCases != nil {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("Minimum cases: %.2f", *it.MinimumCases)) + "\n")
		}
		if it.ReorderCases != nil {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("Reorder cases: %.2f", *it.ReorderCases)) + "\n")
		}
		if it.ReorderInstruction != "" {
			b.WriteString(StyleMuted.Render("Instruction: ") + it.ReorderInstruction + "\n")
		}
	}
	b.WriteString("\n")

	if it.IsSerialized {
		b.WriteString(StyleTitle.Render("Serialized tracking") + "\n")
		mode := it.SerialTrackingMode
		if mode == "" {
			mode = "consumable"
		}
		b.WriteString(StyleMuted.Render("Mode: ") + mode + "\n")
		b.WriteString(StyleMuted.Render("Units are tracked individually by serial number. ") +
			StyleStatusOK.Render("press i") + StyleMuted.Render(" to view instances, ") +
			StyleStatusOK.Render("b") + StyleMuted.Render(" to batch-scan.") + "\n\n")
	}

	if !it.UnitCost.Empty() || !it.PackageCost.Empty() || !it.TotalValue.Empty() {
		b.WriteString(StyleTitle.Render("Costing") + "\n")
		if !it.UnitCost.Empty() {
			b.WriteString(StyleMuted.Render("Unit cost: ") + "$" + string(it.UnitCost) + "\n")
		}
		if !it.PackageCost.Empty() {
			line := "Package cost: $" + string(it.PackageCost)
			if it.QuantityPerPackage > 0 {
				line += fmt.Sprintf(" (qty %d)", it.QuantityPerPackage)
			}
			b.WriteString(StyleMuted.Render(line) + "\n")
		}
		if !it.TotalValue.Empty() {
			b.WriteString(StyleMuted.Render("Total stock value: ") + "$" + string(it.TotalValue) + "\n")
		}
		if it.AverageLeadTime > 0 {
			b.WriteString(StyleMuted.Render(fmt.Sprintf("Avg lead time: %gd", it.AverageLeadTime)) + "\n")
		}
		b.WriteString("\n")
	}

	if it.SupplierName != "" || it.SupplierSKU != "" {
		b.WriteString(StyleTitle.Render("Primary supplier") + "\n")
		if it.SupplierName != "" {
			b.WriteString(StyleMuted.Render("Name: ") + it.SupplierName + "\n")
		}
		if it.SupplierSKU != "" {
			b.WriteString(StyleMuted.Render("SKU: ") + it.SupplierSKU + "\n")
		}
		if it.SupplierURL != "" {
			b.WriteString(StyleMuted.Render("URL: ") + it.SupplierURL + "\n")
		}
		b.WriteString("\n")
	}

	if len(it.Suppliers) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("All suppliers (%d)", len(it.Suppliers))) + "\n")
		for _, sup := range it.Suppliers {
			name := sup.SupplierName
			if name == "" {
				name = fmt.Sprintf("supplier %d", sup.Supplier)
			}
			line := "  · " + name
			if sup.IsPreferred {
				line += " " + StyleStatusOK.Render("★ preferred")
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if sup.SupplierSKU != "" {
				meta = append(meta, "SKU "+sup.SupplierSKU)
			}
			if sup.PackQuantity > 0 {
				meta = append(meta, fmt.Sprintf("pack %d", sup.PackQuantity))
			}
			if !sup.UnitCost.Empty() {
				meta = append(meta, "$"+string(sup.UnitCost))
			}
			if sup.LeadTimeDays > 0 {
				meta = append(meta, fmt.Sprintf("lead %gd", sup.LeadTimeDays))
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
			if sup.URL != "" {
				b.WriteString("    " + StyleMuted.Render(sup.URL) + "\n")
			}
		}
		b.WriteString("\n")
	}

	if len(it.Tags) > 0 {
		b.WriteString(StyleMuted.Render("Tags: ") + strings.Join(it.Tags, ", ") + "\n\n")
	}

	b.WriteString(StyleTitle.Render("Metadata") + "\n")
	if !it.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Created: ") + it.CreatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if !it.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Updated: ") + it.UpdatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if it.QRCodeURL != "" {
		b.WriteString(StyleMuted.Render("QR: ") + it.QRCodeURL + "\n")
	}

	return b.String()
}

// --- Metrics row (issue-5) ---------------------------------------------------

// Fixed cell widths for the metrics row. Each numeric value is right-aligned
// within its width so the columns stay put as magnitudes change and the Cost
// decimal point lines up; the SKU tail is left-aligned text.
const (
	metricSKUTail = 6 // trailing SKU chars shown in the row
	wMetricSKU    = 7 // "…" + up to metricSKUTail chars
	wMetricQty    = 4 // QOH/QOO/QA/QC/QIT/RP — up to 9999
	wMetricLead   = 5 // "365d" / "12.5d"
	wMetricCost   = 8 // "$9999.99"
)

// metricsRowOpts configures the shared metrics-row renderer.
//
//   - withSKU prepends the "SKU: …tail" cell. The LIST wants it (the row needs
//     the SKU to identify which item the numbers belong to); the DETAIL drops it
//     because its line-2 identity row already shows the full SKU (de-dup — Ian).
//   - boldLabels renders each metric LABEL bold so the line draws the eye. Bold
//     adds no display width, so the fixed-width value columns still align.
type metricsRowOpts struct {
	withSKU    bool
	boldLabels bool
}

// formatItemMetricsRow renders the aligned metrics line (issue-5), e.g. with
// withSKU set:
//
//	SKU: WIDGET   QOH:    5   QOO:    0   QA:    5   QC:    0   QIT:    0   RP:    3   Lead:   7d   Cost:  $11.22↑
//
// Each cell is a "LABEL: value" pair with the value padded to a fixed width, so
// the columns don't shift when a single-digit count becomes multi-digit and the
// Cost decimal point stays in a fixed column. Nulls render as "-". With
// boldLabels unset the output is plain (unstyled) text so alignment is testable
// by character offset; boldLabels only wraps the label runs (zero display width),
// leaving the value columns byte-for-byte where they were.
func formatItemMetricsRow(m *omsapi.ItemMetrics, sku string, opts metricsRowOpts) string {
	if m == nil {
		return ""
	}
	label := func(s string) string {
		if opts.boldLabels {
			return StyleMetricLabel.Render(s)
		}
		return s
	}
	cell := func(lbl, value string, w int, align colAlign) string {
		return label(lbl) + ": " + padCell(value, w, align)
	}
	cells := make([]string, 0, 9)
	if opts.withSKU {
		cells = append(cells, cell("SKU", metricSKUString(sku), wMetricSKU, alignLeft))
	}
	cells = append(cells,
		cell("QOH", metricIntString(m.CurrentStock), wMetricQty, alignRight),
		cell("QOO", metricIntString(m.QuantityOnOrder), wMetricQty, alignRight),
		cell("QA", metricFloatQtyString(m.QuantityAvailable), wMetricQty, alignRight),
		cell("QC", metricFloatQtyString(m.QuantityCommitted), wMetricQty, alignRight),
		cell("QIT", metricIntString(m.QuantityInTransit), wMetricQty, alignRight),
		cell("RP", metricIntString(m.ReorderPoint), wMetricQty, alignRight),
		cell("Lead", metricLeadString(m.LeadTimeDays), wMetricLead, alignRight),
		cell("Cost", metricCostString(m.UnitCost), wMetricCost, alignRight)+costTrendArrow(m.CostTrend),
	)
	return strings.Join(cells, "   ")
}

func metricIntString(p *int) string {
	if p == nil {
		return "-"
	}
	return strconv.Itoa(*p)
}

// metricFloatQtyString renders a float quantity (backend FloatField — QA/QC)
// compactly: whole values drop the trailing ".0", genuine fractions are kept,
// and nil → "-". Right-aligned in the metrics row like the int quantities.
func metricFloatQtyString(p *float64) string {
	if p == nil {
		return "-"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func metricLeadString(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%gd", *p)
}

// metricCostString renders the unit cost as "$%.2f" so the decimal point lands
// in a fixed column when right-aligned; null/unparseable → "-".
func metricCostString(d omsapi.DecimalString) string {
	if d.Empty() {
		return "-"
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(d)), 64)
	if err != nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f", f)
}

// costTrendArrow returns the ↑/↓ marker for an up/down price trend, or "" for
// flat / no_history / unknown.
func costTrendArrow(trend string) string {
	switch trend {
	case "up":
		return "↑"
	case "down":
		return "↓"
	}
	return ""
}

// metricSKUString shows the trailing chars of the SKU (the full SKU is on the
// identity line above); an over-long SKU is prefixed with "…".
func metricSKUString(sku string) string {
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return "-"
	}
	r := []rune(sku)
	if len(r) > metricSKUTail {
		return "…" + string(r[len(r)-metricSKUTail:])
	}
	return sku
}

// metricsCountedLine renders the days-since-last-count summary (issue-7):
// "Counted: never" / "Counted: today" / "Counted: Nd ago".
func metricsCountedLine(it *omsapi.Item) string {
	if it == nil || it.DaysSinceLastCount == nil {
		return "Counted: never"
	}
	switch d := *it.DaysSinceLastCount; {
	case d <= 0:
		return "Counted: today"
	case d == 1:
		return "Counted: 1d ago"
	default:
		return fmt.Sprintf("Counted: %dd ago", d)
	}
}

// --- Cycle count (issue-7) ---------------------------------------------------

// cycleCountReasons is the reason pick-list, mirroring the backend
// StockReconciliation.REASON_CHOICES (value → display label). Value is what
// CycleCountItem posts; Label is shown in the prompt. Keep in sync with
// backend/inventory/models.py::StockReconciliation.REASON_CHOICES.
var cycleCountReasons = []struct {
	Value string
	Label string
}{
	{"lost", "Lost"},
	{"damaged", "Damaged"},
	{"miscounted", "Miscounted"},
	{"used_without_scan", "Used without scanning"},
	{"found", "Found (positive delta)"},
	{"vision_supply_check", "Vision supply check"},
	{"other", "Other"},
}

// defaultCycleCountReasonIx is the index of the pre-selected reason
// ("miscounted"), the common case for a routine recount.
func defaultCycleCountReasonIx() int {
	for i, r := range cycleCountReasons {
		if r.Value == "miscounted" {
			return i
		}
	}
	return 0
}

// openCycleCount enters the cycle-count prompt at the quantity step.
func (s *InventoryDetailScreen) openCycleCount() (Screen, tea.Cmd) {
	qty := textinput.New()
	qty.Prompt = ""
	qty.Placeholder = "counted qty"
	qty.CharLimit = 9
	qty.Focus()
	s.ccQty = qty

	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "optional"
	notes.CharLimit = 200
	s.ccNotes = notes

	s.ccReasonIx = defaultCycleCountReasonIx()
	s.ccErr = ""
	s.ccPending = false
	s.ccStep = ccStepQty
	return s, textinput.Blink
}

// closeCycleCount tears the modal down and returns to the normal detail view.
func (s *InventoryDetailScreen) closeCycleCount() {
	s.ccStep = ccStepNone
	s.ccErr = ""
	s.ccPending = false
	s.ccQty.Blur()
	s.ccNotes.Blur()
}

// updateCycleCount drives the three-step prompt: quantity → reason → notes. esc
// cancels at any step (screen-local); the screen is in raw-input mode throughout
// (WantsRawInput), so these keys reach us before the global hotkeys.
func (s *InventoryDetailScreen) updateCycleCount(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.ccPending {
		return s, nil // submission in flight
	}
	if m.String() == "esc" {
		s.closeCycleCount()
		return s, nil
	}
	switch s.ccStep {
	case ccStepQty:
		if m.Type == tea.KeyEnter {
			if _, err := strconv.Atoi(strings.TrimSpace(s.ccQty.Value())); err != nil {
				s.ccErr = "counted qty must be a whole number"
				return s, nil
			}
			s.ccErr = ""
			s.ccStep = ccStepReason
			return s, nil
		}
		// Gate to digits so the field only ever holds a valid integer; editing
		// keys (backspace/arrows) are not KeyRunes, so they pass through.
		if m.Type == tea.KeyRunes {
			for _, r := range m.Runes {
				if r < '0' || r > '9' {
					return s, nil
				}
			}
		}
		var cmd tea.Cmd
		s.ccQty, cmd = s.ccQty.Update(m)
		return s, cmd
	case ccStepReason:
		switch m.String() {
		case "up", "k":
			if s.ccReasonIx > 0 {
				s.ccReasonIx--
			}
		case "down", "j":
			if s.ccReasonIx < len(cycleCountReasons)-1 {
				s.ccReasonIx++
			}
		case "enter":
			s.ccStep = ccStepNotes
			s.ccNotes.Focus()
			return s, textinput.Blink
		}
		return s, nil
	case ccStepNotes:
		if m.Type == tea.KeyEnter {
			return s.submitCycleCount()
		}
		var cmd tea.Cmd
		s.ccNotes, cmd = s.ccNotes.Update(m)
		return s, cmd
	}
	return s, nil
}

// submitCycleCount validates and fires the cycle-count request.
func (s *InventoryDetailScreen) submitCycleCount() (Screen, tea.Cmd) {
	if s.item == nil {
		s.closeCycleCount()
		return s, nil
	}
	qty, err := strconv.Atoi(strings.TrimSpace(s.ccQty.Value()))
	if err != nil {
		s.ccStep = ccStepQty
		s.ccErr = "counted qty must be a whole number"
		return s, nil
	}
	reason := cycleCountReasons[s.ccReasonIx].Value
	notes := strings.TrimSpace(s.ccNotes.Value())
	s.ccPending = true
	s.ccErr = ""
	deps, ctx, id := s.deps, s.ctx(), s.item.ID
	return s, func() tea.Msg {
		// skip_reorder stays false: a count that drops stock below the reorder
		// point should still queue a reorder, matching the web default.
		item, err := deps.OMS.CycleCountItem(ctx, id, qty, reason, false, notes)
		return cycleCountDoneMsg{item: item, err: err}
	}
}

// cycleCountPrompt renders the modal for the active step.
func (s *InventoryDetailScreen) cycleCountPrompt() string {
	var b strings.Builder
	name := ""
	if s.item != nil {
		name = s.item.Name
	}
	b.WriteString(StyleStatusWarn.Render("Cycle count") + "  " + StyleMuted.Render(name) + "\n\n")

	if s.ccPending {
		b.WriteString(StyleMuted.Render("Recording count…"))
		return b.String()
	}

	qty := strings.TrimSpace(s.ccQty.Value())
	switch s.ccStep {
	case ccStepQty:
		b.WriteString("Counted quantity:\n  " + s.ccQty.View() + "\n")
		if s.ccErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.ccErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("enter next · esc cancel"))
	case ccStepReason:
		b.WriteString(StyleMuted.Render("Counted quantity: "+qty) + "\n\n")
		b.WriteString("Reason:\n")
		for i, r := range cycleCountReasons {
			if i == s.ccReasonIx {
				b.WriteString("  " + StyleStatusOK.Render("▸ "+r.Label) + "\n")
			} else {
				b.WriteString("    " + StyleMuted.Render(r.Label) + "\n")
			}
		}
		b.WriteString("\n" + StyleMuted.Render("j/k move · enter next · esc cancel"))
	case ccStepNotes:
		b.WriteString(StyleMuted.Render("Counted quantity: "+qty) + "\n")
		b.WriteString(StyleMuted.Render("Reason: "+cycleCountReasons[s.ccReasonIx].Label) + "\n\n")
		b.WriteString("Note (optional):\n  " + s.ccNotes.View() + "\n")
		if s.ccErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.ccErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("enter submit · esc cancel"))
	}
	return b.String()
}
