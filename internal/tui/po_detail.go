package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type PurchaseOrderDetailScreen struct {
	deps    Deps
	poID    int
	po      *omsapi.PurchaseOrder
	lines   []omsapi.ItemSupplier
	loading bool
	loadErr string
}

type poDetailLoadedMsg struct {
	po    *omsapi.PurchaseOrder
	lines []omsapi.ItemSupplier
	err   error
}

func NewPurchaseOrderDetailScreen(deps Deps, id string) *PurchaseOrderDetailScreen {
	poID, _ := strconv.Atoi(id)
	return &PurchaseOrderDetailScreen{deps: deps, poID: poID, loading: true}
}

func (s *PurchaseOrderDetailScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("PO %s", s.po.Number)
	}
	return fmt.Sprintf("PO #%d", s.poID)
}

func (s *PurchaseOrderDetailScreen) Init() tea.Cmd {
	deps := s.deps
	poID := s.poID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		po, err := deps.OMS.GetPurchaseOrder(ctx, poID)
		if err != nil {
			return poDetailLoadedMsg{err: err}
		}
		page, _ := deps.OMS.ListItemSuppliers(ctx, nil)
		var lines []omsapi.ItemSupplier
		if page != nil {
			for _, is := range page.Results {
				if is.Supplier == po.Supplier {
					lines = append(lines, is)
				}
			}
		}
		return poDetailLoadedMsg{po: po, lines: lines}
	}
}

func (s *PurchaseOrderDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case poDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.po = m.po
		s.lines = m.lines
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "R", "enter":
			if s.po != nil {
				return s, SwitchTo(WSPurchasing, NewReceiveFormScreen(s.deps, s.po, s.lines))
			}
		}
	}
	return s, nil
}

func (s *PurchaseOrderDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading purchase order…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry")
	}
	if s.po == nil {
		return StyleMuted.Render("Purchase order not found.")
	}
	var b strings.Builder
	header := s.po.Number
	if header == "" {
		header = fmt.Sprintf("PO #%d", s.po.ID)
	}
	b.WriteString(StyleTitle.Render(header) + "\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("Supplier %d · Status: %s", s.po.Supplier, s.po.Status)) + "\n")
	if s.po.Total > 0 {
		b.WriteString(fmt.Sprintf("Total: %.2f %s\n", s.po.Total, s.po.Currency))
	}
	if !s.po.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Created %s", s.po.CreatedAt.Format("2006-01-02"))) + "\n")
	}
	b.WriteString("\n")
	if len(s.lines) > 0 {
		b.WriteString(StyleTitle.Render("Line items (supplier offerings)") + "\n")
		for _, l := range s.lines {
			line := fmt.Sprintf("  · item %d sku %s", l.Item, l.SupplierSKU)
			if l.PackQuantity > 0 {
				line += fmt.Sprintf(" pack %d", l.PackQuantity)
			}
			if l.UnitCost > 0 {
				line += fmt.Sprintf(" $%.2f", l.UnitCost)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(StyleMuted.Render("R/enter receive items · r refresh · esc back"))
	return b.String()
}
