package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type InventoryDetailScreen struct {
	deps           Deps
	itemID         string
	item           *omsapi.Item
	loadErr        string
	loading        bool
	scroller       *TextScroller
	terminalHeight int
}

type inventoryDetailLoadedMsg struct {
	item *omsapi.Item
	err  error
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

func (s *InventoryDetailScreen) Init() tea.Cmd {
	deps := s.deps
	itemID := s.itemID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		item, err := deps.OMS.GetItem(ctx, itemID)
		return inventoryDetailLoadedMsg{item: item, err: err}
	}
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
		s.scroller.Set(s.renderBody())
		return s, nil
	case tea.KeyMsg:
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
		}
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
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	hint := "j/k scroll · pgup/pgdn page · o/enter request reorder · r refresh · esc back"
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
}

func (s *InventoryDetailScreen) renderBody() string {
	it := s.item
	var b strings.Builder

	b.WriteString(StyleTitle.Render(it.Name))
	if it.NeedsReorder {
		b.WriteString("  " + StyleStatusWarn.Render("needs reorder"))
	}
	if it.HasPendingReorder {
		b.WriteString("  " + StyleMuted.Render("[reorder pending]"))
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("SKU %s · ID %s", it.SKU, it.ID)))
	if it.CategoryName != "" {
		b.WriteString(StyleMuted.Render(" · " + it.CategoryName))
	}
	if it.Location != "" {
		b.WriteString(StyleMuted.Render(" · " + it.Location))
	}
	b.WriteString("\n\n")

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
			b.WriteString(StyleMuted.Render(fmt.Sprintf("Avg lead time: %dd", it.AverageLeadTime)) + "\n")
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
				meta = append(meta, fmt.Sprintf("lead %dd", sup.LeadTimeDays))
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
