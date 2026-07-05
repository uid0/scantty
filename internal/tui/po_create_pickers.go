// po_create_pickers.go — Phase 3 of the New PO state machine.
//
// Holds the three picker phases (reorder queue, inventory items,
// assets) so po_create.go stays focused on the supplier picker, the
// source-chooser menu, and the line-entry form. Each phase shares
// the same shape: load list → render windowed list → j/k navigate,
// enter to commit, b to go back, esc to cancel. The inventory and
// assets pickers add `/` for filter / search.
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

// ---------------------------------------------------------------------------
// Async loaders + msg types
// ---------------------------------------------------------------------------

type poReorderItemsLoadedMsg struct {
	items []omsapi.ReorderDataItem
	err   error
}

type poItemSuppliersLoadedMsg struct {
	rows []omsapi.ItemSupplier
	err  error
}

type poAssetsLoadedMsg struct {
	rows    []omsapi.Asset
	hasNext bool
	err     error
}

func (s *PurchaseOrderCreateScreen) loadReorderItemsForSupplier() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		data, err := deps.OMS.GetReorderData(ctx)
		if err != nil {
			return poReorderItemsLoadedMsg{err: err}
		}
		// reorder_data is grouped by supplier — pick our slice.
		for _, sup := range data.Suppliers {
			if sup.ID == supplierID {
				return poReorderItemsLoadedMsg{items: sup.Items}
			}
		}
		return poReorderItemsLoadedMsg{items: nil}
	}
}

func (s *PurchaseOrderCreateScreen) loadItemSuppliersForSupplier() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	return func() tea.Msg {
		page, err := deps.OMS.ListItemSuppliersForSupplier(ctx, supplierID, 0)
		if err != nil {
			return poItemSuppliersLoadedMsg{err: err}
		}
		return poItemSuppliersLoadedMsg{rows: page.Results}
	}
}

func (s *PurchaseOrderCreateScreen) loadAssetsForSupplier(search string) tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	supplierID := s.supplierID
	page := s.assetsPage
	if page <= 0 {
		page = 1
	}
	return func() tea.Msg {
		p, err := deps.OMS.ListAssetsForSupplier(ctx, supplierID, search, page)
		if err != nil {
			return poAssetsLoadedMsg{err: err}
		}
		hasNext := p.Next != nil && *p.Next != ""
		return poAssetsLoadedMsg{rows: p.Results, hasNext: hasNext}
	}
}

// handlePickerLoaded dispatches the three async results to the right
// state slot. Returns nil so the caller can chain into tea.Cmd.
func (s *PurchaseOrderCreateScreen) handlePickerLoaded(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case poReorderItemsLoadedMsg:
		s.reorderLoading = false
		if m.err != nil {
			s.reorderLoadErr = m.err.Error()
			return Status("load reorder items failed: "+m.err.Error(), StatusError)
		}
		s.reorderItems = m.items
		s.reorderCursor = 0
	case poItemSuppliersLoadedMsg:
		s.itemSuppliersLoad = false
		if m.err != nil {
			s.itemSuppliersErr = m.err.Error()
			return Status("load inventory items failed: "+m.err.Error(), StatusError)
		}
		s.itemSuppliersAll = m.rows
		s.applyItemSupplierFilter()
		s.itemSuppliersCur = 0
	case poAssetsLoadedMsg:
		s.assetsLoading = false
		if m.err != nil {
			s.assetsErr = m.err.Error()
			return Status("load assets failed: "+m.err.Error(), StatusError)
		}
		s.assets = m.rows
		s.assetsHasNext = m.hasNext
		s.assetsCursor = 0
	}
	return nil
}

// applyItemSupplierFilter populates itemSuppliers from itemSuppliersAll
// using the search-input value (case-insensitive substring on
// ItemName / SupplierSKU). Backend has no ?search= on this endpoint
// today, so we do it client-side over the loaded page.
func (s *PurchaseOrderCreateScreen) applyItemSupplierFilter() {
	q := strings.ToLower(strings.TrimSpace(s.itemSuppliersSearch.Value()))
	if q == "" {
		s.itemSuppliers = s.itemSuppliersAll
		return
	}
	filtered := make([]omsapi.ItemSupplier, 0, len(s.itemSuppliersAll))
	for _, r := range s.itemSuppliersAll {
		if strings.Contains(strings.ToLower(r.ItemName), q) ||
			strings.Contains(strings.ToLower(r.SupplierSKU), q) {
			filtered = append(filtered, r)
		}
	}
	s.itemSuppliers = filtered
}

// ---------------------------------------------------------------------------
// Phase 3a: Reorder-queue picker
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateReorderPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.reorderCursor < len(s.reorderItems)-1 {
			s.reorderCursor++
		}
	case "k", "up":
		if s.reorderCursor > 0 {
			s.reorderCursor--
		}
	case "enter":
		if s.reorderCursor < 0 || s.reorderCursor >= len(s.reorderItems) {
			return s, nil
		}
		it := s.reorderItems[s.reorderCursor]
		qty := it.SuggestedQuantity
		if qty <= 0 {
			qty = 1
		}
		unitCost := 0.0
		if v, err := strconv.ParseFloat(string(it.UnitCost), 64); err == nil {
			unitCost = v
		}
		desc := it.ItemName
		if desc == "" {
			desc = it.SKU
		}
		s.enterLinePhase(it.ItemSupplierID, nil, desc, qty, unitCost)
		return s, textinput.Blink
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) renderReorderPick() string {
	if s.reorderLoading {
		return StyleMuted.Render("Loading reorder-queue suggestions…")
	}
	if s.reorderLoadErr != "" {
		return StyleStatusError.Render("✗ " + s.reorderLoadErr)
	}
	if len(s.reorderItems) == 0 {
		return StyleMuted.Render("Nothing flagged for reorder under this supplier.")
	}
	return s.renderWindowedList(
		len(s.reorderItems), s.reorderCursor,
		func(i int) string {
			it := s.reorderItems[i]
			tag := ""
			if it.HasActiveReorderReq {
				tag = " " + StyleStatusOK.Render(fmt.Sprintf("[reorder %s]", it.ReorderRequestStatus))
			}
			cost := ""
			if it.UnitCost != "" {
				cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s", it.UnitCost))
			}
			return fmt.Sprintf(
				"%s  qty %d (current %d / min %d)%s%s",
				it.ItemName, it.SuggestedQuantity, it.CurrentStock, it.MinimumStock, cost, tag,
			)
		},
	)
}

// ---------------------------------------------------------------------------
// Phase 3b: Inventory items picker
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateItemPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.itemSuppliersTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.itemSuppliersTyping = false
			return s, nil
		case tea.KeyEnter:
			s.itemSuppliersTyping = false
			s.applyItemSupplierFilter()
			s.itemSuppliersCur = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.itemSuppliersSearch, cmd = s.itemSuppliersSearch.Update(m)
		s.applyItemSupplierFilter()
		if s.itemSuppliersCur >= len(s.itemSuppliers) {
			s.itemSuppliersCur = 0
		}
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.itemSuppliersCur < len(s.itemSuppliers)-1 {
			s.itemSuppliersCur++
		}
	case "k", "up":
		if s.itemSuppliersCur > 0 {
			s.itemSuppliersCur--
		}
	case "/":
		s.itemSuppliersTyping = true
		s.itemSuppliersSearch.Focus()
		return s, textinput.Blink
	case "enter":
		if s.itemSuppliersCur < 0 || s.itemSuppliersCur >= len(s.itemSuppliers) {
			return s, nil
		}
		row := s.itemSuppliers[s.itemSuppliersCur]
		id := row.ID
		unitCost := 0.0
		if v, err := strconv.ParseFloat(string(row.UnitCost), 64); err == nil {
			unitCost = v
		}
		desc := row.ItemName
		if desc == "" {
			desc = row.SupplierSKU
		}
		s.enterLinePhase(&id, nil, desc, 1, unitCost)
		return s, textinput.Blink
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) renderItemPick() string {
	var b strings.Builder
	if s.itemSuppliersSearch.Value() != "" || s.itemSuppliersTyping {
		b.WriteString(StyleMuted.Render("filter: ") + s.itemSuppliersSearch.View() + "\n\n")
	}
	if s.itemSuppliersLoad {
		b.WriteString(StyleMuted.Render("Loading inventory items…"))
		return b.String()
	}
	if s.itemSuppliersErr != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.itemSuppliersErr))
		return b.String()
	}
	if len(s.itemSuppliers) == 0 {
		b.WriteString(StyleMuted.Render("No inventory items match."))
		return b.String()
	}
	b.WriteString(s.renderWindowedList(
		len(s.itemSuppliers), s.itemSuppliersCur,
		func(i int) string {
			it := s.itemSuppliers[i]
			sku := it.SupplierSKU
			if sku == "" {
				sku = "—"
			}
			cost := ""
			if it.UnitCost != "" {
				cost = "  " + StyleMuted.Render(fmt.Sprintf("@ %s", it.UnitCost))
			}
			lead := ""
			if it.LeadTimeDays > 0 {
				lead = "  " + StyleMuted.Render(fmt.Sprintf("lead %gd", it.LeadTimeDays))
			}
			return fmt.Sprintf("%s  %s%s%s", it.ItemName, sku, cost, lead)
		},
	))
	return b.String()
}

// ---------------------------------------------------------------------------
// Phase 3c: Assets-from-supplier picker (server-side search + pagination)
// ---------------------------------------------------------------------------

func (s *PurchaseOrderCreateScreen) updateAssetPickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.assetsTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.assetsTyping = false
			return s, nil
		case tea.KeyEnter:
			s.assetsTyping = false
			s.assetsPage = 1
			s.assetsLoading = true
			s.assetsErr = ""
			return s, s.loadAssetsForSupplier(s.assetsSearch.Value())
		}
		var cmd tea.Cmd
		s.assetsSearch, cmd = s.assetsSearch.Update(m)
		return s, cmd
	}
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSPurchasing, nil)
	case "b":
		s.phase = poPhaseSource
		return s, nil
	case "j", "down":
		if s.assetsCursor < len(s.assets)-1 {
			s.assetsCursor++
		}
	case "k", "up":
		if s.assetsCursor > 0 {
			s.assetsCursor--
		}
	case "/":
		s.assetsTyping = true
		s.assetsSearch.Focus()
		return s, textinput.Blink
	case "]":
		if s.assetsHasNext {
			s.assetsPage++
			s.assetsLoading = true
			return s, s.loadAssetsForSupplier(s.assetsSearch.Value())
		}
	case "[":
		if s.assetsPage > 1 {
			s.assetsPage--
			s.assetsLoading = true
			return s, s.loadAssetsForSupplier(s.assetsSearch.Value())
		}
	case "enter":
		if s.assetsCursor < 0 || s.assetsCursor >= len(s.assets) {
			return s, nil
		}
		a := s.assets[s.assetsCursor]
		// Asset.ID is a polyglot `any` (UUID strings + int rows both
		// occur in inventory). Convert to a string for the PO create
		// payload — backend's asset_id accepts the string repr.
		idStr := fmt.Sprintf("%v", a.ID)
		desc := a.Name
		if a.AssetTag != "" {
			desc = fmt.Sprintf("%s (%s)", a.Name, a.AssetTag)
		}
		s.enterLinePhase(nil, &idStr, desc, 1, 0)
		return s, textinput.Blink
	}
	return s, nil
}

func (s *PurchaseOrderCreateScreen) renderAssetPick() string {
	var b strings.Builder
	if s.assetsSearch.Value() != "" || s.assetsTyping {
		b.WriteString(StyleMuted.Render("search: ") + s.assetsSearch.View() + "\n\n")
	}
	if s.assetsLoading {
		b.WriteString(StyleMuted.Render("Loading assets…"))
		return b.String()
	}
	if s.assetsErr != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.assetsErr))
		return b.String()
	}
	if len(s.assets) == 0 {
		b.WriteString(StyleMuted.Render("No assets match."))
		return b.String()
	}
	b.WriteString(s.renderWindowedList(
		len(s.assets), s.assetsCursor,
		func(i int) string {
			a := s.assets[i]
			tag := a.AssetTag
			if tag == "" {
				tag = "—"
			}
			serial := ""
			if a.SerialNumber != "" {
				serial = "  " + StyleMuted.Render("s/n "+a.SerialNumber)
			}
			return fmt.Sprintf("%s  %s%s", a.Name, tag, serial)
		},
	))
	pager := fmt.Sprintf("page %d", s.assetsPage)
	if s.assetsHasNext {
		pager += " · ] next"
	}
	if s.assetsPage > 1 {
		pager += " · [ prev"
	}
	b.WriteString("\n" + StyleMuted.Render(pager))
	return b.String()
}

// ---------------------------------------------------------------------------
// Shared windowed-list renderer
// ---------------------------------------------------------------------------

// renderWindowedList draws `total` items via the supplied formatter,
// keeping `cursor` on screen with ~10 lines of context. Same pattern
// as the supplier picker so all four pickers look consistent.
func (s *PurchaseOrderCreateScreen) renderWindowedList(total, cursor int, formatRow func(int) string) string {
	const window = 10
	start := cursor - window/2
	if start < 0 {
		start = 0
	}
	end := start + window
	if end > total {
		end = total
		start = end - window
		if start < 0 {
			start = 0
		}
	}
	var b strings.Builder
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above\n", start)))
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == cursor {
			caret = "  ▸ "
		}
		line := caret + formatRow(i)
		if i == cursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
	}
	if end < total {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below\n", total-end)))
	}
	return b.String()
}
