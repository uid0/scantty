package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type PurchaseOrderDetailScreen struct {
	deps     Deps
	poID     string
	po       *omsapi.PurchaseOrder
	loading  bool
	loadErr  string
	scroller *TextScroller
}

type poDetailLoadedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

func NewPurchaseOrderDetailScreen(deps Deps, id string) *PurchaseOrderDetailScreen {
	return &PurchaseOrderDetailScreen{
		deps:     deps,
		poID:     id,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *PurchaseOrderDetailScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("PO %s", s.po.Number)
	}
	return fmt.Sprintf("PO #%s", s.poID)
}

func (s *PurchaseOrderDetailScreen) Init() tea.Cmd {
	return s.load()
}

func (s *PurchaseOrderDetailScreen) load() tea.Cmd {
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
	case tea.WindowSizeMsg:
		s.scroller.SetViewHeight(m.Height - detailChromeRows)
		return s, nil
	case poDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.po = m.po
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
			return s, s.load()
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
	hint := "j/k scroll · pgup/pgdn page · R receive items · r refresh · esc back"
	return s.scroller.View() + "\n\n" + StyleMuted.Render(hint)
}

func (s *PurchaseOrderDetailScreen) renderBody() string {
	po := s.po
	var b strings.Builder

	header := po.Number
	if header == "" {
		header = fmt.Sprintf("PO #%v", po.ID)
	}
	b.WriteString(StyleTitle.Render(header))
	if po.Status == "voided" {
		b.WriteString("  " + StyleStatusError.Render("VOIDED"))
	} else if po.IsFullyReceived {
		b.WriteString("  " + StyleStatusOK.Render("RECEIVED"))
	}
	b.WriteString("\n")

	supplier := po.SupplierDetails
	if supplier == "" {
		supplier = po.SupplierName
	}
	if supplier == "" && po.Supplier != nil {
		supplier = fmt.Sprintf("supplier %v", po.Supplier)
	}
	status := po.StatusLabel
	if status == "" {
		status = po.Status
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%s · %s", supplier, status)) + "\n")

	if po.CreatedByUsername != "" {
		b.WriteString(StyleMuted.Render("Created by: ") + po.CreatedByUsername + "\n")
	}
	if po.SentByUsername != "" {
		line := "Sent by: " + po.SentByUsername
		if po.SentAt != nil {
			line += " on " + po.SentAt.Format("2006-01-02")
		}
		b.WriteString(StyleMuted.Render(line) + "\n")
	}
	if po.VoidedAt != nil {
		b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("Voided %s", po.VoidedAt.Format("2006-01-02"))))
		if po.VoidedByUsername != "" {
			b.WriteString(" by " + po.VoidedByUsername)
		}
		b.WriteString("\n")
		if po.VoidReason != "" {
			b.WriteString(StyleMuted.Render("Reason: ") + po.VoidReason + "\n")
		}
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Identifiers") + "\n")
	b.WriteString(StyleMuted.Render("PO ID: ") + fmt.Sprintf("%v", po.ID) + "\n")
	if po.SupplierOrderNumber != "" {
		b.WriteString(StyleMuted.Render("Supplier order #: ") + po.SupplierOrderNumber + "\n")
	}
	if po.SalesOrderNumber != "" {
		b.WriteString(StyleMuted.Render("Sales order #: ") + po.SalesOrderNumber + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Dates") + "\n")
	if !po.OrderDate.IsZero() {
		b.WriteString(StyleMuted.Render("Ordered: ") + po.OrderDate.Format("2006-01-02") + "\n")
	}
	if po.ExpectedDeliveryDate != "" {
		b.WriteString(StyleMuted.Render("Expected delivery: ") + po.ExpectedDeliveryDate + "\n")
	}
	if !po.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Created: ") + po.CreatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if !po.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render("Updated: ") + po.UpdatedAt.Format("2006-01-02 15:04") + "\n")
	}
	if po.DaysSinceOrdered != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Days since ordered: %d", *po.DaysSinceOrdered)) + "\n")
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Totals") + "\n")
	currency := po.Currency
	if currency == "" {
		currency = "USD"
	}
	if !po.EstimatedTotal.Empty() {
		b.WriteString(StyleMuted.Render("Estimated: ") + fmt.Sprintf("$%s %s", po.EstimatedTotal, currency) + "\n")
	}
	if !po.ActualTotal.Empty() {
		b.WriteString(StyleMuted.Render("Actual: ") + fmt.Sprintf("$%s %s", po.ActualTotal, currency) + "\n")
	}
	if po.Total > 0 && po.EstimatedTotal.Empty() && po.ActualTotal.Empty() {
		b.WriteString(StyleMuted.Render("Total: ") + fmt.Sprintf("$%.2f %s", po.Total, currency) + "\n")
	}
	if po.TotalItems > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Line items: %d", po.TotalItems)) + "\n")
	}
	if po.TotalQuantity > 0 {
		line := fmt.Sprintf("Quantity: %d", po.TotalQuantity)
		if po.TotalReceivedQuantity > 0 {
			line += fmt.Sprintf(" · received %d", po.TotalReceivedQuantity)
		}
		b.WriteString(StyleMuted.Render(line) + "\n")
	}
	b.WriteString("\n")

	if po.Notes != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(po.Notes + "\n\n")
	}

	if len(po.Items) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Line items (%d)", len(po.Items))) + "\n")
		for _, li := range po.Items {
			renderPOLineItem(&b, li, supplier)
		}
		b.WriteString("\n")
	} else {
		b.WriteString(StyleMuted.Render("No line items on this PO.") + "\n\n")
	}

	if len(po.Attachments) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Attachments (%d)", len(po.Attachments))) + "\n")
		for _, att := range po.Attachments {
			name := att.FileName
			if name == "" {
				name = att.File
			}
			line := "  · " + name
			if att.Description != "" {
				line += " — " + att.Description
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if !att.UploadedAt.IsZero() {
				meta = append(meta, att.UploadedAt.Format("2006-01-02"))
			}
			if att.UploadedByName != "" {
				meta = append(meta, "by "+att.UploadedByName)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
			if att.FileURL != "" {
				b.WriteString("    " + StyleMuted.Render(att.FileURL) + "\n")
			}
		}
	}

	return b.String()
}

func renderPOLineItem(b *strings.Builder, li omsapi.PurchaseOrderItem, poSupplier string) {
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
	if li.ItemType != "" {
		meta = append(meta, "type "+li.ItemType)
	}
	if !li.UnitCostOrdered.Empty() {
		meta = append(meta, "@ $"+string(li.UnitCostOrdered))
	}
	if !li.UnitCostActual.Empty() && li.UnitCostActual != li.UnitCostOrdered {
		meta = append(meta, "actual @ $"+string(li.UnitCostActual))
	}
	if !li.ActualCost.Empty() {
		meta = append(meta, "actual $"+string(li.ActualCost))
	} else if !li.EstimatedCost.Empty() {
		meta = append(meta, "est $"+string(li.EstimatedCost))
	}
	if li.ExpectedShipmentDate != "" {
		meta = append(meta, "ship by "+li.ExpectedShipmentDate)
	}
	if li.SupplierDetails != "" && li.SupplierDetails != poSupplier {
		meta = append(meta, li.SupplierDetails)
	}
	if li.IsVoided {
		meta = append(meta, "voided")
	}
	if len(meta) > 0 {
		b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
	}

	if sku, ok := li.ItemDetails["sku"].(string); ok && sku != "" {
		if name, ok2 := li.ItemDetails["name"].(string); ok2 && name != "" && name != label {
			b.WriteString("    " + StyleMuted.Render(fmt.Sprintf("inv item: %s · SKU %s", name, sku)) + "\n")
		} else {
			b.WriteString("    " + StyleMuted.Render("SKU "+sku) + "\n")
		}
	}
	if tag, ok := li.AssetDetails["asset_tag"].(string); ok && tag != "" {
		assetLine := "asset: " + tag
		if loc, ok2 := li.AssetDetails["location_name"].(string); ok2 && loc != "" {
			assetLine += " · " + loc
		}
		b.WriteString("    " + StyleMuted.Render(assetLine) + "\n")
	}
	if li.IsVoided && li.VoidReason != "" {
		b.WriteString("    " + StyleStatusWarn.Render("void reason: "+li.VoidReason) + "\n")
	}
	if li.Notes != "" {
		b.WriteString("    " + StyleMuted.Render(li.Notes) + "\n")
	}
}
