package tui

import (
	"context"
	"fmt"
	"net/url"
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

// consumeStep tracks where the operator is in the in-screen use/consume prompt
// (accounting Phase 2). consumeStepNone is the zero value: no modal open.
type consumeStep int

const (
	consumeStepNone consumeStep = iota
	consumeStepQty
	consumeStepSIG
	consumeStepNotes
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

	// "Assets that use this item" (op-qdfr): the assets that list this item as
	// a part/consumable via the AssetPart through-model. Loaded from its own
	// filtered asset query alongside the item, so usedByLoading distinguishes
	// "still fetching" from a genuinely empty result.
	usedBy        []omsapi.Asset
	usedByErr     string
	usedByLoading bool

	// Purchase / receipt provenance (op-96uo): what each order was placed at per
	// unit, and what actually shipped when. Its own (auth-required) endpoint,
	// loaded alongside the item, so purchasesLoading tells "still fetching" apart
	// from a genuinely never-ordered item.
	purchases        *omsapi.ItemPurchaseHistory
	purchasesErr     string
	purchasesLoading bool

	// Cycle-count modal (issue-7). Active while ccStep != ccStepNone, during
	// which WantsRawInput routes every key here. The two textinputs are
	// (re)initialised each time the modal opens.
	ccStep     cycleCountStep
	ccQty      textinput.Model
	ccNotes    textinput.Model
	ccReasonIx int
	ccErr      string
	ccPending  bool

	// Use / consume modal (accounting Phase 2). Active while cnStep !=
	// consumeStepNone, during which WantsRawInput routes every key here. The
	// committee (SIG) pick-list loads async when the modal opens: index 0 is the
	// "— none (no charge) —" row, indices 1..len(cnSIGs) map to cnSIGs[ix-1].
	cnStep        consumeStep
	cnQty         textinput.Model
	cnNotes       textinput.Model
	cnSIGs        []omsapi.SIG
	cnSIGIx       int
	cnLoadingSIGs bool
	cnSIGErr      string
	cnErr         string
	cnPending     bool
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

// inventoryUsedByLoadedMsg carries the assets that consume this item as a part
// (op-qdfr), fetched in parallel with the item. A non-nil err is non-fatal —
// the section shows the reason instead of the list.
type inventoryUsedByLoadedMsg struct {
	assets []omsapi.Asset
	err    error
}

// inventoryPurchaseHistoryLoadedMsg carries the item's order + receipt
// provenance (op-96uo), fetched in parallel with the item. A non-nil err is
// non-fatal — the section shows the reason instead of the lists.
type inventoryPurchaseHistoryLoadedMsg struct {
	history *omsapi.ItemPurchaseHistory
	err     error
}

// cycleCountDoneMsg is the result of a cycle-count submission (issue-7). On
// success item is the re-serialized item with the updated stock + count fields.
type cycleCountDoneMsg struct {
	item *omsapi.Item
	err  error
}

// consumeSIGsLoadedMsg carries the committee pick-list for the consume modal,
// fetched async when the modal opens (accounting Phase 2). A non-nil err is
// non-fatal — the operator can still record usage with no charge ("— none —").
type consumeSIGsLoadedMsg struct {
	sigs []omsapi.SIG
	err  error
}

// consumeDoneMsg is the result of a log-usage submission (accounting Phase 2).
// qty / itemName / chargedName are captured at submit time so the status line
// reads correctly regardless of what the (possibly partial) response echoes.
type consumeDoneMsg struct {
	res         *omsapi.LogUsageResult
	qty         int
	itemName    string
	chargedName string
	err         error
}

type inventoryDeletedMsg struct {
	err error
}

// inventoryRetireDoneMsg is the result of the T retire/un-retire action
// (op-jv7r). retired records the direction requested so the status line and any
// error message read correctly regardless of the item's prior state.
type inventoryRetireDoneMsg struct {
	retired bool
	err     error
}

func NewInventoryDetailScreen(deps Deps, id string) *InventoryDetailScreen {
	return &InventoryDetailScreen{
		deps:             deps,
		itemID:           id,
		loading:          true,
		usedByLoading:    true,
		purchasesLoading: true,
		scroller:         NewTextScroller(defaultDetailHeight),
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
	return s.confirmingDelete || s.ccStep != ccStepNone || s.cnStep != consumeStepNone
}

// HandlesKey claims lowercase 's' (manage suppliers) so it beats the global
// Settings nav hotkey, uppercase 'T' (retire/un-retire) so it beats the global
// Thermostat-list hotkey, and lowercase 'u' (use/consume) so it beats the global
// ForgeKey-Usage hotkey — the sc-k7p LocalKeyScreen pattern. Only consulted in
// the normal view (any open modal flips WantsRawInput true, routing every key
// here first).
func (s *InventoryDetailScreen) HandlesKey(key string) bool {
	return key == "s" || key == "T" || key == "u"
}

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

// loadUsedByCmd fetches the assets that use this item as a part/consumable
// (op-qdfr) — the AssetPart through-model, which is what people mean by "what
// uses this?". The backend exposes it as an asset-list filter rather than an
// item sub-resource, so this is a plain filtered ListAssets. Loads in parallel
// with the item; a failure only blanks the section (see Update).
func (s *InventoryDetailScreen) loadUsedByCmd() tea.Cmd {
	deps, id, ctx := s.deps, s.itemID, s.ctx()
	return func() tea.Msg {
		page, err := deps.OMS.ListAssets(ctx, url.Values{"consumable_for_item": {id}})
		if err != nil {
			return inventoryUsedByLoadedMsg{err: err}
		}
		return inventoryUsedByLoadedMsg{assets: page.Results}
	}
}

// loadPurchaseHistoryCmd fetches the item's order + receipt provenance
// (op-96uo) — the per-order unit costs and the deliveries behind the current
// stock. Batched with the item like the other detail loads; a failure only
// marks the section unavailable (see Update).
func (s *InventoryDetailScreen) loadPurchaseHistoryCmd() tea.Cmd {
	deps, id, ctx := s.deps, s.itemID, s.ctx()
	return func() tea.Msg {
		history, err := deps.OMS.GetPurchaseHistory(ctx, id)
		return inventoryPurchaseHistoryLoadedMsg{history: history, err: err}
	}
}

func (s *InventoryDetailScreen) Init() tea.Cmd {
	s.usedByLoading = true
	s.purchasesLoading = true
	return tea.Batch(s.loadItemCmd(), s.loadMetricsCmd(), s.loadUsedByCmd(), s.loadPurchaseHistoryCmd())
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
	case inventoryUsedByLoadedMsg:
		// Non-fatal like metrics: on error keep the section header and show the
		// reason rather than silently claiming nothing uses the item.
		s.usedByLoading = false
		if m.err != nil {
			s.usedByErr = m.err.Error()
		} else {
			s.usedByErr = ""
			s.usedBy = m.assets
		}
		if s.item != nil {
			s.scroller.Set(s.renderBody())
		}
		return s, nil
	case inventoryPurchaseHistoryLoadedMsg:
		// Non-fatal like metrics and used-by: an item that was genuinely never
		// ordered has empty lists, so a failed fetch must say so rather than
		// render as "never ordered".
		s.purchasesLoading = false
		if m.err != nil {
			s.purchasesErr = m.err.Error()
		} else {
			s.purchasesErr = ""
			s.purchases = m.history
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
	case consumeSIGsLoadedMsg:
		// Late arrival after an esc-cancel is harmless: the modal is closed, so
		// nothing reads cnSIGs until it reopens (which reloads). Only apply while
		// a consume modal is up.
		if s.cnStep != consumeStepNone {
			s.cnLoadingSIGs = false
			if m.err != nil {
				s.cnSIGErr = m.err.Error()
			} else {
				s.cnSIGErr = ""
				s.cnSIGs = m.sigs
			}
		}
		return s, nil
	case consumeDoneMsg:
		s.cnPending = false
		if m.err != nil {
			// Keep the modal open on the notes step so the operator can retry or
			// esc out; surface the reason inline and in the status bar.
			s.cnStep = consumeStepNotes
			s.cnErr = "log usage failed: " + m.err.Error()
			return s, Status(s.cnErr, StatusError)
		}
		s.closeConsume()
		// The log_usage response is the UsageLog, not the item — re-fetch the full
		// item AND metrics for the drawn-down stock level.
		s.scroller.Set(s.renderBody())
		text, level := consumeStatusLine(m)
		return s, tea.Batch(Status(text, level), s.loadItemCmd(), s.loadMetricsCmd())
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
	case inventoryRetireDoneMsg:
		if m.err != nil {
			verb := "retire"
			if !m.retired {
				verb = "un-retire"
			}
			return s, Status(verb+" failed: "+m.err.Error(), StatusError)
		}
		status := "item retired"
		if !m.retired {
			status = "item un-retired"
		}
		// Re-fetch the full item so the header's [retired] marker and the
		// reorder flags re-render; the action's own response serializer shape
		// isn't guaranteed to match the detail. Stock is unchanged by a
		// retire, so the metrics row need not reload.
		return s, tea.Batch(Status(status, StatusOK), s.loadItemCmd())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.ccStep != ccStepNone {
			return s.updateCycleCount(m)
		}
		if s.cnStep != consumeStepNone {
			return s.updateConsume(m)
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
		case "u":
			// Use / consume (accounting Phase 2): record consumption of N units,
			// optionally charging the value to a committee (SIG). Lowercase u is
			// the global ForgeKey-Usage hotkey, so it's claimed via HandlesKey to
			// reach the screen here instead.
			if s.item != nil {
				return s.openConsume()
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
		case "T":
			// Retire / un-retire (op-jv7r): a retired item is suppressed from
			// reorder and auto-hidden from the default list once its stock hits
			// 0. Reversible, so no confirm prompt (unlike x delete). Uppercase T
			// (claimed via HandlesKey) because lowercase t is the serialized-unit
			// retire elsewhere and bare T is the global Thermostat-list hotkey.
			if s.item != nil {
				deps := s.deps
				ctx := deps.Ctx
				if ctx == nil {
					ctx = context.Background()
				}
				id := s.item.ID
				retire := !s.item.IsRetired
				return s, func() tea.Msg {
					_, err := deps.OMS.SetItemRetired(ctx, id, retire)
					return inventoryRetireDoneMsg{retired: retire, err: err}
				}
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
	if s.cnStep != consumeStepNone {
		return header + "\n\n" + body + "\n\n" + s.consumePrompt()
	}
	// The retire hint flips to "un-retire" once the item is retired, so the key
	// reads correctly whichever direction T will toggle.
	retireHint := "T retire"
	if s.item.IsRetired {
		retireHint = "T un-retire"
	}
	hint := "j/k scroll · o/enter reorder · c count · u use · s suppliers · E edit · " + retireHint + " · x delete · r refresh · esc back"
	if s.item.IsSerialized {
		hint = "j/k scroll · o/enter reorder · c count · u use · s suppliers · i instances · b batch-scan · E edit · " + retireHint + " · x delete · r refresh · esc back"
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

	// Line 1: item name (+ reorder / retired flags).
	b.WriteString(StyleTitle.Render(it.Name))
	if it.IsRetired {
		b.WriteString("  " + StyleMuted.Render("[retired]"))
	}
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

	// First in the body, so it sits directly beneath the pinned metrics row whose
	// QC cell it explains (nothing at all when there's nothing committed).
	b.WriteString(s.renderCommittedBreakdown())

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

	b.WriteString(s.renderPurchaseSection())

	b.WriteString(s.renderUsedBySection())

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

// --- Assets that use this item (op-qdfr) -------------------------------------

// renderUsedBySection renders the assets that list this item as a part or
// consumable (the AssetPart through-model — NOT assets that ARE an instance of
// this item type, which is Asset.inventory_item; the web keeps the two apart
// too). The header is drawn unconditionally so the section doesn't pop into
// existence mid-body and shift the scroll once the query lands; the body below
// it reads loading / error / empty / rows. Returns text with a trailing blank
// line, matching its sibling sections.
func (s *InventoryDetailScreen) renderUsedBySection() string {
	var b strings.Builder
	title := "Assets that use this item"
	if !s.usedByLoading && s.usedByErr == "" && len(s.usedBy) > 0 {
		title = fmt.Sprintf("%s (%d)", title, len(s.usedBy))
	}
	b.WriteString(StyleTitle.Render(title) + "\n")

	switch {
	case s.usedByLoading:
		b.WriteString(StyleMuted.Render("loading…") + "\n\n")
		return b.String()
	case s.usedByErr != "":
		b.WriteString(StyleStatusWarn.Render("unavailable: "+s.usedByErr) + "\n\n")
		return b.String()
	case len(s.usedBy) == 0:
		b.WriteString(StyleMuted.Render("No assets use this item as a part.") + "\n\n")
		return b.String()
	}

	for _, a := range s.usedBy {
		id := fmt.Sprint(a.ID)
		name := a.Name
		if name == "" {
			name = "asset " + id
		}
		b.WriteString("  · " + name + "\n")
		if meta := usedByMetaLine(a, s.itemID); meta != "" {
			b.WriteString("    " + StyleMuted.Render(meta) + "\n")
		}
		b.WriteString("    " + StyleMuted.Render("ID "+id) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// usedByMetaLine summarises one consuming asset: its tag, the through-model
// detail for THIS item (quantity needed + required/optional), status and
// location. The asset payload already nests its AssetPart rows, so the
// through-model fields cost no extra round-trip — but an asset whose parts
// aren't serialized in the list response simply omits those cells rather than
// showing a wrong quantity.
func usedByMetaLine(a omsapi.Asset, itemID string) string {
	meta := []string{}
	if a.AssetTag != "" {
		meta = append(meta, "tag "+a.AssetTag)
	}
	for _, p := range a.Parts {
		if p.Part != itemID {
			continue
		}
		meta = append(meta, fmt.Sprintf("qty %d", p.QuantityNeeded))
		if p.IsRequired {
			meta = append(meta, "required")
		} else {
			meta = append(meta, "optional")
		}
		break
	}
	if a.Status != "" {
		meta = append(meta, a.Status)
	}
	if a.LocationName != "" {
		meta = append(meta, a.LocationName)
	}
	return strings.Join(meta, " · ")
}

// --- Committed-to breakdown (op-l4i0) ----------------------------------------

// renderCommittedBreakdown attributes the QC metric to the work orders holding
// it — which job, and so which machine, the reserved stock is going to. It opens
// the body, directly beneath the pinned metrics row whose QC cell it explains.
//
// Unlike its sibling sections this one draws nothing when there is nothing to
// attribute, because it has no load of its own: the entries ride the metrics
// payload, so "still loading" and "fetch failed" are already spoken for by the
// metrics row itself being absent. An empty breakdown means QC is 0 (or a
// backend that predates the field) — neither is worth a header.
func (s *InventoryDetailScreen) renderCommittedBreakdown() string {
	if s.metrics == nil || len(s.metrics.CommittedBreakdown) == 0 {
		return ""
	}
	title := "Committed to"
	if s.metrics.QuantityCommitted != nil {
		title += fmt.Sprintf(" (QC %s)", metricFloatQtyString(s.metrics.QuantityCommitted))
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(title) + "\n")
	for _, e := range s.metrics.CommittedBreakdown {
		b.WriteString("  · " + committedWorkOrderLabel(e) + "  " + StyleMuted.Render(committedEntryMeta(e)) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

// committedWorkOrderLabel names the work order holding the stock: its short id
// ("WO-1A2B3C4D"), falling back to the raw id on a payload without one.
func committedWorkOrderLabel(e omsapi.CommittedBreakdownEntry) string {
	if e.WorkOrderShortID != "" {
		return e.WorkOrderShortID
	}
	return "work order " + e.WorkOrderID
}

// committedEntryMeta is the rest of one entry: the asset the job is on and the
// quantity it holds. A work order with no asset says so rather than leaving a
// blank cell — an asset-less work order is legitimate, not missing data.
func committedEntryMeta(e omsapi.CommittedBreakdownEntry) string {
	asset := e.AssetName
	if asset == "" {
		asset = "no asset"
	}
	return asset + " · qty " + formatQty(e.Quantity)
}

// --- Purchase / receipts (op-96uo) -------------------------------------------

// renderPurchaseSection renders the item's order + receipt provenance: the unit
// cost each order was placed at (the full history behind the metrics row's
// single last-PO cost), then the deliveries grouped by order so every tracking
// number of a partially-shipped order is visible under it.
//
// Header drawn unconditionally, body branching loading / unavailable / empty /
// rows — the section must not materialise mid-scroll, and a failed fetch must
// not read as "never ordered", which is real and different information.
func (s *InventoryDetailScreen) renderPurchaseSection() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Purchase / Receipts") + "\n")

	switch {
	case s.purchasesLoading:
		b.WriteString(StyleMuted.Render("loading…") + "\n\n")
		return b.String()
	case s.purchasesErr != "":
		b.WriteString(StyleStatusWarn.Render("unavailable: "+s.purchasesErr) + "\n\n")
		return b.String()
	case s.purchases == nil || (len(s.purchases.OrderCosts) == 0 && len(s.purchases.Deliveries) == 0):
		b.WriteString(StyleMuted.Render("Never ordered — no purchase-order lines or deliveries for this item.") + "\n\n")
		return b.String()
	}

	if orders := s.purchases.OrderCosts; len(orders) > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Orders (%d)", len(orders))) + "\n")
		for _, o := range orders {
			b.WriteString("  · " + poDisplayLabel(o.PONumber, o.PurchaseOrder) + "  " + StyleMuted.Render(orderCostMeta(o)) + "\n")
		}
	} else {
		b.WriteString(StyleMuted.Render("Orders: none") + "\n")
	}

	if len(s.purchases.Deliveries) == 0 {
		// Ordered but nothing received: the open order above is the whole story.
		b.WriteString("\n" + StyleMuted.Render("Deliveries: none received yet") + "\n\n")
		return b.String()
	}
	b.WriteString("\n" + StyleMuted.Render(fmt.Sprintf("Deliveries (%d)", len(s.purchases.Deliveries))) + "\n")
	for _, g := range groupDeliveriesByPO(s.purchases.Deliveries) {
		b.WriteString("  " + g.label + "\n")
		for _, d := range g.rows {
			b.WriteString("    · " + StyleMuted.Render(deliveryLine(d)) + "\n")
			if note := strings.Join(strings.Fields(d.ReceiptNotes), " "); note != "" {
				b.WriteString("      " + StyleMuted.Render("note: "+note) + "\n")
			}
		}
	}
	b.WriteString("\n")
	return b.String()
}

// poDisplayLabel names an order: its PO number, or the pk when no number has
// been assigned yet (po_number is nullable — the reason the payload carries the
// pk at all).
func poDisplayLabel(number string, pk int) string {
	if n := strings.TrimSpace(number); n != "" {
		return n
	}
	return fmt.Sprintf("order %d (no PO number)", pk)
}

// orderCostMeta summarises one purchase-order line: when it was placed, the
// order's status, how many units, and what it cost per unit.
func orderCostMeta(o omsapi.ItemOrderCost) string {
	meta := []string{}
	if !o.OrderDate.IsZero() {
		meta = append(meta, o.OrderDate.Format("2006-01-02"))
	}
	if o.Status != "" {
		meta = append(meta, o.Status)
	}
	meta = append(meta, fmt.Sprintf("qty %d", o.QuantityOrdered))
	meta = append(meta, orderUnitCost(o))
	return strings.Join(meta, " · ")
}

// orderUnitCost renders what the order paid per unit: the price it was PLACED
// at, plus the actual once a receipt has priced it. Both are shown when they
// differ — a supplier re-pricing between order and delivery is exactly what
// keeping the two columns is for.
func orderUnitCost(o omsapi.ItemOrderCost) string {
	ordered, actual := formatMoney(o.UnitCostOrdered), formatMoney(o.UnitCostActual)
	switch {
	case ordered == "" && actual == "":
		return "no unit cost"
	case actual == "" || actual == ordered:
		return ordered + "/unit"
	case ordered == "":
		return actual + "/unit actual"
	default:
		return ordered + "/unit → " + actual + " actual"
	}
}

// poDeliveryGroup is one order's deliveries, oldest first.
type poDeliveryGroup struct {
	label string
	rows  []omsapi.ItemDelivery
}

// groupDeliveriesByPO buckets the flat delivery list by ORDER, keyed on the PO
// pk and not po_number: the number is nullable, so two numberless orders would
// collapse into one group. Groups appear in first-seen (oldest-delivery) order,
// as do the rows inside them.
func groupDeliveriesByPO(rows []omsapi.ItemDelivery) []poDeliveryGroup {
	var groups []poDeliveryGroup
	at := map[int]int{}
	for _, d := range rows {
		ix, seen := at[d.PurchaseOrder]
		if !seen {
			ix = len(groups)
			at[d.PurchaseOrder] = ix
			groups = append(groups, poDeliveryGroup{label: poDisplayLabel(d.PONumber, d.PurchaseOrder)})
		}
		groups[ix].rows = append(groups[ix].rows, d)
	}
	return groups
}

// deliveryLine summarises one receipt: when it landed, how many units, its
// shipment identity, and whether the receiving side has processed it.
func deliveryLine(d omsapi.ItemDelivery) string {
	meta := []string{}
	if !d.DeliveryDate.IsZero() {
		meta = append(meta, d.DeliveryDate.Format("2006-01-02"))
	}
	meta = append(meta, fmt.Sprintf("qty %d", d.QuantityReceived))
	meta = append(meta, deliveryTracking(d))
	if d.IsComplete {
		meta = append(meta, "processed")
	} else {
		meta = append(meta, "not yet processed")
	}
	return strings.Join(meta, " · ")
}

// deliveryTracking is the shipment identity — "UPS 1Z999…", either half on its
// own, or a stated absence: a receipt logged with no tracking number is normal
// (someone carried it in) and shouldn't read as a dropped field.
func deliveryTracking(d omsapi.ItemDelivery) string {
	carrier, tracking := strings.TrimSpace(d.Carrier), strings.TrimSpace(d.TrackingNumber)
	switch {
	case carrier != "" && tracking != "":
		return carrier + " " + tracking
	case tracking != "":
		return tracking
	case carrier != "":
		return carrier
	}
	return "no tracking number"
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
// compactly, with nil → "-". Right-aligned in the metrics row like the int
// quantities.
func metricFloatQtyString(p *float64) string {
	if p == nil {
		return "-"
	}
	return formatQty(*p)
}

// formatQty renders a float quantity for display: whole values drop the trailing
// ".0" and genuine fractions are kept ("2", "1.5").
func formatQty(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
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

// --- Use / consume (accounting Phase 2) --------------------------------------

// openConsume enters the use/consume prompt at the quantity step and kicks off
// the committee (SIG) pick-list load in the background, so the list is ready by
// the time the operator reaches the committee step (the qty step covers the
// round-trip). Modeled on openCycleCount.
func (s *InventoryDetailScreen) openConsume() (Screen, tea.Cmd) {
	qty := textinput.New()
	qty.Prompt = ""
	qty.Placeholder = "quantity used"
	qty.CharLimit = 9
	qty.Focus()
	s.cnQty = qty

	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "optional"
	notes.CharLimit = 200
	s.cnNotes = notes

	s.cnSIGs = nil
	s.cnSIGIx = 0
	s.cnLoadingSIGs = true
	s.cnSIGErr = ""
	s.cnErr = ""
	s.cnPending = false
	s.cnStep = consumeStepQty
	return s, tea.Batch(textinput.Blink, s.loadSIGsCmd())
}

// loadSIGsCmd fetches the committee pick-list for the charge step.
func (s *InventoryDetailScreen) loadSIGsCmd() tea.Cmd {
	deps, ctx := s.deps, s.ctx()
	return func() tea.Msg {
		page, err := deps.OMS.ListSIGs(ctx, nil)
		if err != nil {
			return consumeSIGsLoadedMsg{err: err}
		}
		return consumeSIGsLoadedMsg{sigs: page.Results}
	}
}

// closeConsume tears the modal down and returns to the normal detail view.
func (s *InventoryDetailScreen) closeConsume() {
	s.cnStep = consumeStepNone
	s.cnErr = ""
	s.cnSIGErr = ""
	s.cnLoadingSIGs = false
	s.cnPending = false
	s.cnSIGs = nil
	s.cnSIGIx = 0
	s.cnQty.Blur()
	s.cnNotes.Blur()
}

// updateConsume drives the three-step prompt: quantity → committee → notes. esc
// cancels at any step (screen-local); the screen is in raw-input mode throughout
// (WantsRawInput), so these keys reach us before the global hotkeys.
func (s *InventoryDetailScreen) updateConsume(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.cnPending {
		return s, nil // submission in flight
	}
	if m.String() == "esc" {
		s.closeConsume()
		return s, nil
	}
	switch s.cnStep {
	case consumeStepQty:
		if m.Type == tea.KeyEnter {
			if n, err := strconv.Atoi(strings.TrimSpace(s.cnQty.Value())); err != nil || n <= 0 {
				s.cnErr = "quantity used must be a positive whole number"
				return s, nil
			}
			s.cnErr = ""
			s.cnStep = consumeStepSIG
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
		s.cnQty, cmd = s.cnQty.Update(m)
		return s, cmd
	case consumeStepSIG:
		switch m.String() {
		case "up", "k":
			if s.cnSIGIx > 0 {
				s.cnSIGIx--
			}
		case "down", "j":
			// Index 0 is the "— none —" row; 1..len(cnSIGs) are the committees.
			if s.cnSIGIx < len(s.cnSIGs) {
				s.cnSIGIx++
			}
		case "enter":
			s.cnStep = consumeStepNotes
			s.cnNotes.Focus()
			return s, textinput.Blink
		}
		return s, nil
	case consumeStepNotes:
		if m.Type == tea.KeyEnter {
			return s.submitConsume()
		}
		var cmd tea.Cmd
		s.cnNotes, cmd = s.cnNotes.Update(m)
		return s, cmd
	}
	return s, nil
}

// selectedConsumeSIG returns the picked committee and true, or nil/false for the
// "— none (no charge) —" row (index 0, or a stale index past the loaded list).
func (s *InventoryDetailScreen) selectedConsumeSIG() (*omsapi.SIG, bool) {
	if s.cnSIGIx <= 0 || s.cnSIGIx > len(s.cnSIGs) {
		return nil, false
	}
	return &s.cnSIGs[s.cnSIGIx-1], true
}

// projectedCharge is the item's unit cost × qty as "$X.XX", or "" when the item
// has no unit cost (then the backend records the usage but posts no charge). The
// unit cost is the same value the "Costing" section renders.
func (s *InventoryDetailScreen) projectedCharge(qty int) string {
	if s.item == nil || s.item.UnitCost.Empty() || qty <= 0 {
		return ""
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(s.item.UnitCost)), 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("$%.2f", f*float64(qty))
}

// submitConsume validates and fires the log-usage request.
func (s *InventoryDetailScreen) submitConsume() (Screen, tea.Cmd) {
	if s.item == nil {
		s.closeConsume()
		return s, nil
	}
	qty, err := strconv.Atoi(strings.TrimSpace(s.cnQty.Value()))
	if err != nil || qty <= 0 {
		s.cnStep = consumeStepQty
		s.cnErr = "quantity used must be a positive whole number"
		return s, nil
	}
	body := omsapi.LogUsageBody{Quantity: qty, Notes: strings.TrimSpace(s.cnNotes.Value())}
	chargedName := ""
	if sig, ok := s.selectedConsumeSIG(); ok {
		id := sig.ID
		body.ChargedGroup = &id
		chargedName = sig.Name
	}
	itemName := s.item.Name
	s.cnPending = true
	s.cnErr = ""
	deps, ctx, id := s.deps, s.ctx(), s.item.ID
	return s, func() tea.Msg {
		res, err := deps.OMS.LogUsage(ctx, id, body)
		return consumeDoneMsg{res: res, qty: qty, itemName: itemName, chargedName: chargedName, err: err}
	}
}

// consumeStatusLine builds the status-bar line + severity for a completed
// log-usage. A backend warning (no unit cost → nothing posted) still recorded
// the usage, so it reads as a warning rather than an error.
func consumeStatusLine(m consumeDoneMsg) (string, StatusLevel) {
	base := fmt.Sprintf("used %d × %s", m.qty, m.itemName)
	if m.res != nil && m.res.Warning != "" {
		return base + " — " + m.res.Warning, StatusWarn
	}
	if m.chargedName == "" {
		return base + " — no charge", StatusOK
	}
	if amt := formatMoney(consumeTotalCost(m.res)); amt != "" {
		return fmt.Sprintf("%s — charged %s to %s", base, amt, m.chargedName), StatusOK
	}
	return fmt.Sprintf("%s — charged to %s", base, m.chargedName), StatusOK
}

// consumeTotalCost pulls the posted total off the response, tolerating a nil
// response (a partial/absent body still leaves the captured status readable).
func consumeTotalCost(res *omsapi.LogUsageResult) omsapi.DecimalString {
	if res == nil {
		return ""
	}
	return res.TotalCost
}

// formatMoney renders a decimal money string as "$X.XX", or "" when empty or
// unparseable (unlike metricCostString, which yields the "-" table sentinel).
func formatMoney(d omsapi.DecimalString) string {
	if d.Empty() {
		return ""
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(d)), 64)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("$%.2f", f)
}

// consumePrompt renders the use/consume modal for the active step (the
// cycleCountPrompt sibling for the accounting log-usage flow).
func (s *InventoryDetailScreen) consumePrompt() string {
	var b strings.Builder
	name := ""
	if s.item != nil {
		name = s.item.Name
	}
	b.WriteString(StyleStatusWarn.Render("Use / consume") + "  " + StyleMuted.Render(name) + "\n\n")

	if s.cnPending {
		b.WriteString(StyleMuted.Render("Recording usage…"))
		return b.String()
	}

	qty := strings.TrimSpace(s.cnQty.Value())
	qtyN, _ := strconv.Atoi(qty)
	switch s.cnStep {
	case consumeStepQty:
		b.WriteString("Quantity used:\n  " + s.cnQty.View() + "\n")
		if s.cnErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.cnErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("enter next · esc cancel"))
	case consumeStepSIG:
		b.WriteString(StyleMuted.Render("Quantity used: "+qty) + "\n")
		if amt := s.projectedCharge(qtyN); amt != "" {
			b.WriteString(StyleMuted.Render("Value if charged: "+amt) + "\n")
		} else {
			b.WriteString(StyleMuted.Render("No unit cost — nothing will be charged") + "\n")
		}
		b.WriteString("\nCharge to committee:\n")
		none := "— none (no charge) —"
		if s.cnSIGIx == 0 {
			b.WriteString("  " + StyleStatusOK.Render("▸ "+none) + "\n")
		} else {
			b.WriteString("    " + StyleMuted.Render(none) + "\n")
		}
		for i, sig := range s.cnSIGs {
			if s.cnSIGIx == i+1 {
				b.WriteString("  " + StyleStatusOK.Render("▸ "+sig.Name) + "\n")
			} else {
				b.WriteString("    " + StyleMuted.Render(sig.Name) + "\n")
			}
		}
		if s.cnLoadingSIGs {
			b.WriteString("    " + StyleMuted.Render("loading committees…") + "\n")
		}
		if s.cnSIGErr != "" {
			b.WriteString("\n" + StyleStatusWarn.Render("committees unavailable: "+s.cnSIGErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("j/k move · enter next · esc cancel"))
	case consumeStepNotes:
		b.WriteString(StyleMuted.Render("Quantity used: "+qty) + "\n")
		b.WriteString(StyleMuted.Render("Charge: "+s.consumeChargeSummary(qtyN)) + "\n\n")
		b.WriteString("Note (optional):\n  " + s.cnNotes.View() + "\n")
		if s.cnErr != "" {
			b.WriteString("\n" + StyleStatusError.Render(s.cnErr) + "\n")
		}
		b.WriteString("\n" + StyleMuted.Render("enter submit · esc cancel"))
	}
	return b.String()
}

// consumeChargeSummary describes the pending charge for the notes-step review:
// the committee it will post to and the projected amount, or "none (no charge)".
func (s *InventoryDetailScreen) consumeChargeSummary(qty int) string {
	sig, ok := s.selectedConsumeSIG()
	if !ok {
		return "none (no charge)"
	}
	amt := s.projectedCharge(qty)
	if amt == "" {
		return sig.Name + " (no unit cost — nothing will post)"
	}
	return amt + " to " + sig.Name
}
