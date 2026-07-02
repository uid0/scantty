package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

type PurchaseOrderDetailScreen struct {
	deps           Deps
	poID           string
	po             *omsapi.PurchaseOrder
	loading        bool
	loadErr        string
	scroller       *TextScroller
	terminalHeight int

	// "Mark shipped" form state. When shipping == true, the screen
	// renders a two-field prompt over the body: line index + date
	// (YYYY-MM-DD, default today). Enter submits; esc cancels.
	shipping    bool
	shipIdxIn   textinput.Model
	shipDateIn  textinput.Model
	shipFocus   int // 0 = index, 1 = date
	shipErr     string
	shipPending bool

	// transitioning gates the async Send-to-Supplier / Confirm actions so
	// a second keypress can't fire a duplicate request while one is in
	// flight — the same guard the OMS frontend applies to its Send/Confirm
	// buttons.
	transitioning bool
}

type poItemShippedMsg struct {
	item *omsapi.PurchaseOrderItem
	err  error
}

// poTransitionedMsg reports the result of a Send-to-Supplier or Confirm
// action. action is "sent" or "confirmed" and drives the toast wording.
type poTransitionedMsg struct {
	action string
	err    error
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

// WantsRawInput routes every key to the screen while the "Mark shipped"
// form is open so the textinputs receive characters without the app
// dispatcher claiming letters like 'r' / 'R' / 'S'.
func (s *PurchaseOrderDetailScreen) WantsRawInput() bool { return s.shipping }

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
		s.terminalHeight = m.Height
		return s, nil
	case poDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.po = m.po
		s.scroller.Set(s.renderBody())
		return s, nil
	case poItemShippedMsg:
		s.shipPending = false
		if m.err != nil {
			s.shipErr = m.err.Error()
			return s, Status("mark shipped failed: "+m.err.Error(), StatusError)
		}
		s.shipping = false
		s.shipErr = ""
		// Refresh the PO so the dates render with the new value.
		s.loading = true
		return s, tea.Batch(Status("marked shipped", StatusOK), s.load())
	case poTransitionedMsg:
		s.transitioning = false
		if m.err != nil {
			verb := "send to supplier"
			if m.action == "confirmed" {
				verb = "confirm order"
			}
			return s, Status(verb+" failed: "+m.err.Error(), StatusError)
		}
		// Reload so the new status (and any resulting date/label changes)
		// render, mirroring the frontend's reload-after-transition.
		s.loading = true
		s.loadErr = ""
		toast := "sent to supplier"
		if m.action == "confirmed" {
			toast = "order confirmed"
		}
		return s, tea.Batch(Status(toast, StatusOK), s.load())

	case tea.KeyMsg:
		if s.shipping {
			return s.handleShipKey(m)
		}
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
		case "S":
			if s.po == nil || len(s.po.Items) == 0 {
				return s, Status("no items to mark shipped", StatusWarn)
			}
			s.openShipForm()
			return s, textinput.Blink
		case "s":
			// Send to Supplier: draft -> sent. Gated the same way the
			// frontend gates its Send button (status == draft).
			if s.po == nil || s.po.Status != "draft" {
				return s, Status("send to supplier is only available on draft POs", StatusWarn)
			}
			if s.transitioning {
				return s, nil
			}
			return s, s.transitionPO("send")
		case "c":
			// Confirm: sent -> confirmed. Gated on status == sent.
			if s.po == nil || s.po.Status != "sent" {
				return s, Status("confirm is only available on sent POs", StatusWarn)
			}
			if s.transitioning {
				return s, nil
			}
			return s, s.transitionPO("confirm")
		}
	}
	return s, nil
}

// transitionPO fires the async Send-to-Supplier ("send") or Confirm
// ("confirm") action against the current PO and reports the result as a
// poTransitionedMsg. The caller has already verified the PO is in the
// right state; this only marshals the request off the UI goroutine.
func (s *PurchaseOrderDetailScreen) transitionPO(kind string) tea.Cmd {
	poID := fmt.Sprintf("%v", s.po.ID)
	s.transitioning = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if kind == "confirm" {
			// No expected_delivery_date — mirrors the OMS frontend's
			// default confirm, which posts no body.
			err := deps.OMS.ConfirmOrder(ctx, poID, "")
			return poTransitionedMsg{action: "confirmed", err: err}
		}
		err := deps.OMS.SendToSupplier(ctx, poID)
		return poTransitionedMsg{action: "sent", err: err}
	}
}

func (s *PurchaseOrderDetailScreen) openShipForm() {
	idx := textinput.New()
	idx.Prompt = ""
	idx.Placeholder = fmt.Sprintf("line # (1-%d)", len(s.po.Items))
	idx.CharLimit = 4
	idx.Focus()
	date := textinput.New()
	date.Prompt = ""
	date.Placeholder = "YYYY-MM-DD (blank = today, '-' = clear)"
	date.CharLimit = 12
	date.SetValue(time.Now().Format("2006-01-02"))
	s.shipIdxIn = idx
	s.shipDateIn = date
	s.shipFocus = 0
	s.shipErr = ""
	s.shipping = true
}

func (s *PurchaseOrderDetailScreen) handleShipKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.shipping = false
		s.shipErr = ""
		return s, nil
	case tea.KeyTab, tea.KeyShiftTab:
		if s.shipFocus == 0 {
			s.shipIdxIn.Blur()
			s.shipDateIn.Focus()
			s.shipFocus = 1
		} else {
			s.shipDateIn.Blur()
			s.shipIdxIn.Focus()
			s.shipFocus = 0
		}
		return s, nil
	case tea.KeyEnter:
		if s.shipPending {
			return s, nil
		}
		return s.submitShip()
	}
	var cmd tea.Cmd
	if s.shipFocus == 0 {
		s.shipIdxIn, cmd = s.shipIdxIn.Update(m)
	} else {
		s.shipDateIn, cmd = s.shipDateIn.Update(m)
	}
	return s, cmd
}

func (s *PurchaseOrderDetailScreen) submitShip() (Screen, tea.Cmd) {
	idxRaw := strings.TrimSpace(s.shipIdxIn.Value())
	var idx int
	if _, err := fmt.Sscanf(idxRaw, "%d", &idx); err != nil || idx < 1 || idx > len(s.po.Items) {
		s.shipErr = fmt.Sprintf("line number must be between 1 and %d", len(s.po.Items))
		return s, nil
	}
	dateRaw := strings.TrimSpace(s.shipDateIn.Value())
	switch dateRaw {
	case "":
		dateRaw = time.Now().Format("2006-01-02")
	case "-":
		dateRaw = "" // clear actual_shipment_date
	default:
		if _, err := time.Parse("2006-01-02", dateRaw); err != nil {
			s.shipErr = "date must be YYYY-MM-DD (or '-' to clear, blank for today)"
			return s, nil
		}
	}

	line := s.po.Items[idx-1]
	itemID := fmt.Sprintf("%v", line.ID)
	poID := fmt.Sprintf("%v", s.po.ID)
	s.shipPending = true
	s.shipErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		item, err := deps.OMS.MarkPurchaseOrderItemShipped(ctx, poID, itemID, dateRaw)
		return poItemShippedMsg{item: item, err: err}
	}
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
	if s.shipping {
		var b strings.Builder
		b.WriteString(StyleTitle.Render("Mark item shipped") + "\n\n")
		b.WriteString(StyleMuted.Render("Line #:        ") + s.shipIdxIn.View() + "\n")
		b.WriteString(StyleMuted.Render("Shipment date: ") + s.shipDateIn.View() + "\n")
		if s.shipErr != "" {
			b.WriteString("\n" + StyleStatusError.Render("✗ "+s.shipErr) + "\n")
		}
		if s.shipPending {
			b.WriteString("\n" + StyleMuted.Render("Submitting…"))
		} else {
			b.WriteString("\n" + StyleMuted.Render("tab next field · enter submit · esc cancel · '-' in date field clears"))
		}
		return b.String()
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, detailFooterRows))
	return s.scroller.View() + "\n\n" + StyleMuted.Render(s.footerHint())
}

// footerHint builds the one-line key legend. The Send (s) and Confirm (c)
// hints are status-gated so they only appear when the action is actually
// available — draft POs can be sent, sent POs can be confirmed — matching
// how the frontend shows those affordances only in the right state.
func (s *PurchaseOrderDetailScreen) footerHint() string {
	parts := []string{"j/k scroll", "pgup/pgdn page"}
	if s.po != nil {
		switch s.po.Status {
		case "draft":
			parts = append(parts, "s send to supplier")
		case "sent":
			parts = append(parts, "c confirm")
		}
	}
	parts = append(parts, "R receive items", "S mark item shipped", "r refresh", "esc back")
	return strings.Join(parts, " · ")
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
		for i, li := range po.Items {
			renderPOLineItem(&b, i+1, li, supplier)
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

func renderPOLineItem(b *strings.Builder, lineNum int, li omsapi.PurchaseOrderItem, poSupplier string) {
	label := li.DisplayLabel()
	line := fmt.Sprintf("  %d) %s", lineNum, label)
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

	// Second line: JD-Edwards-style aligned cost row — Part Number,
	// Unit Cost, Quantity, Total Cost. Fixed-width fields so decimal
	// points line up between rows when the operator scans the PO.
	b.WriteString("    " + StyleMuted.Render(jdeCostLine(li)) + "\n")

	// Third line: ship-by + ship-date in different colors. ship-by
	// gets a urgency hue (red overdue, yellow ≤7d, plain otherwise);
	// the actual ship date is always green to make it pop. Whole
	// line is suppressed when neither date is set so a freeform line
	// without dates doesn't waste a row.
	if li.ExpectedShipmentDate != "" || li.ActualShipmentDate != "" {
		b.WriteString("    " + renderShipDates(li) + "\n")
	}

	// Fourth line: anything else worth surfacing that isn't already
	// on the JDE / ship-date rows — item type, supplier override,
	// voided flag, actual-cost-override-when-different.
	meta := []string{}
	if li.ItemType != "" {
		meta = append(meta, "type "+li.ItemType)
	}
	if !li.UnitCostActual.Empty() && li.UnitCostActual != li.UnitCostOrdered {
		meta = append(meta, "actual @ $"+string(li.UnitCostActual))
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

// jdeCostLine renders the per-item cost summary as a JD-Edwards-style
// columnar row. Decimal points align between items when the columns
// don't overflow — for inventory items SKUs are usually 8-16 chars,
// asset tags rarely run past 12, so the 20-char part-number column
// holds the typical case without truncation. Long part numbers
// expand the field and the columns to their right shift past the
// usual alignment; that's a knowing trade-off so we never truncate
// an identifier the operator needs to read.
func jdeCostLine(li omsapi.PurchaseOrderItem) string {
	part := "—"
	if sku, ok := li.ItemDetails["sku"].(string); ok && sku != "" {
		part = sku
	} else if tag, ok := li.AssetDetails["asset_tag"].(string); ok && tag != "" {
		part = tag
	}

	unit := "—"
	if !li.UnitCostOrdered.Empty() {
		unit = string(li.UnitCostOrdered)
	}

	total := "—"
	if !li.ActualCost.Empty() {
		total = string(li.ActualCost)
	} else if !li.EstimatedCost.Empty() {
		total = string(li.EstimatedCost)
	}

	// %-20s left-aligns the part number in a fixed 20-char column so
	// the cost / qty / total fields all start at the same offset
	// across rows. %10s right-aligns the decimal strings so their
	// decimal points fall in the same column.
	return fmt.Sprintf(
		"PART  %-20s   UNIT $%10s   QTY %5d   TOTAL $%10s",
		part, unit, li.QuantityOrdered, total,
	)
}

// renderShipDates draws the ship-by + ship-date row with separate
// colors so the operator can see urgency vs. fulfillment at a glance.
//
//   - expected_shipment_date colored by urgency:
//       red    overdue (today is past the date and we have no actual ship)
//       yellow ≤ 7 days out (today is within a week of the date)
//       plain  further out, OR an actual ship has already been recorded
//   - actual_shipment_date always green so it stands out as the "done"
//     signal regardless of where the ship-by sits.
func renderShipDates(li omsapi.PurchaseOrderItem) string {
	var parts []string
	if li.ExpectedShipmentDate != "" {
		label := "SHIP BY " + li.ExpectedShipmentDate
		urgency := shipByUrgency(li)
		switch urgency {
		case "overdue":
			parts = append(parts, StyleStatusError.Render(label))
		case "soon":
			parts = append(parts, StyleStatusWarn.Render(label))
		default:
			parts = append(parts, StyleMuted.Render(label))
		}
	}
	if li.ActualShipmentDate != "" {
		parts = append(parts, StyleStatusOK.Render("SHIPPED "+li.ActualShipmentDate))
	}
	return strings.Join(parts, "   ")
}

// shipByUrgency classifies the expected_shipment_date for coloring.
// Returns "shipped" when an actual_shipment_date is already set (no
// urgency — the work is done), "overdue" when the ship-by has passed
// without a ship, "soon" when within 7 days, or "" otherwise.
func shipByUrgency(li omsapi.PurchaseOrderItem) string {
	if li.ExpectedShipmentDate == "" {
		return ""
	}
	if li.ActualShipmentDate != "" {
		return "shipped"
	}
	expected, err := time.Parse("2006-01-02", li.ExpectedShipmentDate)
	if err != nil {
		return ""
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	expected = expected.UTC().Truncate(24 * time.Hour)
	if expected.Before(today) {
		return "overdue"
	}
	if expected.Sub(today) <= 7*24*time.Hour {
		return "soon"
	}
	return ""
}
