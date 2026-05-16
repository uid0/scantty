package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type PurchaseOrderDetailScreen struct {
	deps    Deps
	poID    string
	po      *omsapi.PurchaseOrder
	loading bool
	loadErr string
}

type poDetailLoadedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

func NewPurchaseOrderDetailScreen(deps Deps, id string) *PurchaseOrderDetailScreen {
	return &PurchaseOrderDetailScreen{deps: deps, poID: id, loading: true}
}

func (s *PurchaseOrderDetailScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("PO %s", s.po.Number)
	}
	return fmt.Sprintf("PO #%s", s.poID)
}

func (s *PurchaseOrderDetailScreen) Init() tea.Cmd {
	deps := s.deps
	id := s.poID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		po, err := deps.OMS.GetPurchaseOrder(ctx, id)
		return poDetailLoadedMsg{po: po, err: err}
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
		return s, nil
	case tea.KeyMsg:
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "R", "enter":
			if s.po != nil {
				return s, SwitchTo(WSPurchasing, NewReceiveFormScreen(s.deps, s.po))
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
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.po == nil {
		return StyleMuted.Render("Purchase order not found.")
	}
	po := s.po
	var b strings.Builder
	header := po.Number
	if header == "" {
		header = fmt.Sprintf("PO #%v", po.ID)
	}
	b.WriteString(StyleTitle.Render(header) + "\n")
	supplier := po.SupplierName
	if supplier == "" {
		supplier = fmt.Sprintf("supplier %v", po.Supplier)
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%s · Status: %s", supplier, po.Status)) + "\n")
	if po.Total > 0 {
		curr := po.Currency
		if curr == "" {
			curr = "USD"
		}
		b.WriteString(fmt.Sprintf("Total: $%.2f %s\n", po.Total, curr))
	}
	if !po.OrderDate.IsZero() {
		b.WriteString(StyleMuted.Render("Ordered: "+po.OrderDate.Format("2006-01-02")) + "\n")
	}
	if po.ExpectedDeliveryDate != "" {
		b.WriteString(StyleMuted.Render("Expected: "+po.ExpectedDeliveryDate) + "\n")
	}
	if po.Notes != "" {
		b.WriteString("\n" + po.Notes + "\n")
	}
	b.WriteString("\n")

	if len(po.Items) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Line items (%d)", len(po.Items))) + "\n")
		for _, li := range po.Items {
			label := li.DisplayLabel()
			line := fmt.Sprintf("  · %s", label)
			if li.QuantityOrdered > 0 {
				line += fmt.Sprintf(" — ordered %d", li.QuantityOrdered)
				if li.QuantityReceived > 0 {
					line += fmt.Sprintf(", received %d", li.QuantityReceived)
				}
				if li.QuantityPending > 0 && li.QuantityPending != li.QuantityOrdered {
					line += " " + StyleStatusWarn.Render(fmt.Sprintf("(%d pending)", li.QuantityPending))
				}
				if li.IsFullyReceived {
					line += " " + StyleStatusOK.Render("✓")
				}
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if li.UnitCostOrdered != "" {
				meta = append(meta, "@ $"+li.UnitCostOrdered)
			}
			if li.ActualCost != "" {
				meta = append(meta, "actual $"+li.ActualCost)
			} else if li.EstimatedCost != "" {
				meta = append(meta, "est $"+li.EstimatedCost)
			}
			if li.SupplierDetails != "" && li.SupplierDetails != supplier {
				meta = append(meta, li.SupplierDetails)
			}
			if li.IsVoided {
				meta = append(meta, "voided")
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
			if li.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(li.Notes) + "\n")
			}
		}
		b.WriteString("\n")
	} else {
		b.WriteString(StyleMuted.Render("No line items on this PO.") + "\n\n")
	}

	b.WriteString(StyleMuted.Render("R/enter receive items · r refresh · esc back"))
	return b.String()
}
