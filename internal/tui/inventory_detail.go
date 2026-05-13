package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type InventoryDetailScreen struct {
	deps     Deps
	itemID   int
	item     *omsapi.Item
	loadErr  string
	loading  bool
	suppliers []omsapi.ItemSupplier
}

type inventoryDetailLoadedMsg struct {
	item      *omsapi.Item
	suppliers []omsapi.ItemSupplier
	err       error
}

func NewInventoryDetailScreen(deps Deps, id string) *InventoryDetailScreen {
	itemID, _ := strconv.Atoi(id)
	return &InventoryDetailScreen{deps: deps, itemID: itemID, loading: true}
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
		if err != nil {
			return inventoryDetailLoadedMsg{err: err}
		}
		var suppliers []omsapi.ItemSupplier
		if len(item.Suppliers) > 0 {
			page, serr := deps.OMS.ListItemSuppliers(ctx, nil)
			if serr == nil {
				for _, is := range page.Results {
					if is.Item == item.ID {
						suppliers = append(suppliers, is)
					}
				}
			}
		}
		return inventoryDetailLoadedMsg{item: item, suppliers: suppliers}
	}
}

func (s *InventoryDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case inventoryDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.item = m.item
		s.suppliers = m.suppliers
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "o", "enter":
			if s.item != nil {
				return s, SwitchTo(WSInventory, NewReorderFormScreen(s.deps, s.item, s.suppliers))
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

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s\n", StyleTitle.Render(s.item.Name)))
	b.WriteString(StyleMuted.Render(fmt.Sprintf("SKU %s · ID %d", s.item.SKU, s.item.ID)))
	b.WriteString("\n\n")
	if s.item.Description != "" {
		b.WriteString(s.item.Description + "\n\n")
	}
	b.WriteString(fmt.Sprintf("Stock: %d", s.item.Stock))
	if s.item.ReorderLevel > 0 {
		b.WriteString(fmt.Sprintf("  ·  Reorder at: %d", s.item.ReorderLevel))
	}
	if s.item.NeedsReorder {
		b.WriteString("  " + StyleStatusWarn.Render("(needs reorder)"))
	}
	b.WriteString("\n")
	if s.item.ReorderStatus != "" {
		b.WriteString(StyleMuted.Render("Status: ") + s.item.ReorderStatus + "\n")
	}
	b.WriteString("\n")

	if len(s.suppliers) > 0 {
		b.WriteString(StyleTitle.Render("Suppliers") + "\n")
		for _, sup := range s.suppliers {
			line := fmt.Sprintf("  · supplier %d", sup.Supplier)
			if sup.SupplierSKU != "" {
				line += fmt.Sprintf(" · sku %s", sup.SupplierSKU)
			}
			if sup.PackQuantity > 0 {
				line += fmt.Sprintf(" · pack %d", sup.PackQuantity)
			}
			if sup.UnitCost > 0 {
				line += fmt.Sprintf(" · $%.2f", sup.UnitCost)
			}
			if sup.LeadTimeDays > 0 {
				line += fmt.Sprintf(" · lead %dd", sup.LeadTimeDays)
			}
			if sup.IsPreferred {
				line += " " + StyleStatusOK.Render("★")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(StyleMuted.Render("o/enter request reorder · r refresh · esc back"))
	return b.String()
}
