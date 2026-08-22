package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// receivePhase tracks the two stages of the receive flow: entering per-line
// quantities, then (only when serialized lines were received) scanning one
// serial number per received unit, then a final summary.
type receivePhase int

const (
	phaseQty receivePhase = iota
	phaseSerial
	phaseDone
)

// serialUnit is one serial-capture slot: a single received unit of a
// serialized line that still needs its serial number scanned in.
type serialUnit struct {
	itemID   string // InventoryItem UUID (from the PO line's item_details)
	poItemID any    // PurchaseOrderItem id, recorded as provenance
	label    string // line display label, for the prompt
	unitNo   int    // 1-based unit index within the line
	unitTot  int    // total units received on the line
}

type ReceiveFormScreen struct {
	deps    Deps
	po      *omsapi.PurchaseOrder
	lines   []omsapi.PurchaseOrderItem
	qty     []textinput.Model
	notes   textinput.Model
	focused int
	pending bool
	result  string
	level   StatusLevel

	// terminalWidth is what the kit breakdown wraps against (op-8n0). 0 until the
	// first WindowSizeMsg, which the JDE layer reads as "do not truncate".
	terminalWidth int

	// Serialized-unit capture (phase 2). After the quantity receive posts,
	// each received unit of a serialized line enrolls one capture slot so
	// the operator can scan a serial into it. Each captured serial creates a
	// SerializedComponent (provenance = the PO line) and accessions it into
	// stock.
	phase         receivePhase
	serialUnits   []serialUnit
	serialCursor  int
	serialInput   textinput.Model
	serialPending bool
	createdCount  int
	inStockCount  int
	skippedCount  int
	failedCount   int
	serialErr     string
}

type receiveSubmittedMsg struct {
	po  *omsapi.PurchaseOrder
	err error
}

// serialUnitDoneMsg reports the result of creating + accessioning one
// serialized unit during phase 2.
type serialUnitDoneMsg struct {
	created bool // the SerializedComponent was created
	inStock bool // the receive lifecycle action also succeeded
	err     error
}

func NewReceiveFormScreen(deps Deps, po *omsapi.PurchaseOrder) *ReceiveFormScreen {
	// Build the editable line list from the PO's items. Skip voided lines
	// and fully-received lines (no qty pending) so the form stays focused
	// on what's actually receivable.
	var lines []omsapi.PurchaseOrderItem
	if po != nil {
		for _, li := range po.Items {
			if li.IsVoided {
				continue
			}
			if li.IsFullyReceived && li.QuantityPending == 0 {
				continue
			}
			lines = append(lines, li)
		}
	}
	s := &ReceiveFormScreen{deps: deps, po: po, lines: lines}
	for range lines {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 8
		ti.Placeholder = "0"
		s.qty = append(s.qty, ti)
	}
	s.notes = textinput.New()
	s.notes.Prompt = ""
	s.notes.CharLimit = 200
	s.notes.Placeholder = "optional notes"
	s.serialInput = textinput.New()
	s.serialInput.Prompt = ""
	s.serialInput.CharLimit = 200
	s.serialInput.Placeholder = "scan or type serial number"
	if len(s.qty) > 0 {
		s.qty[0].Focus()
	} else {
		s.notes.Focus()
	}
	return s
}

func (s *ReceiveFormScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("Receive %s", s.po.Number)
	}
	return "Receive Items"
}

func (s *ReceiveFormScreen) WantsRawInput() bool { return true }

func (s *ReceiveFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ReceiveFormScreen) totalInputs() int { return len(s.qty) + 1 }

func (s *ReceiveFormScreen) currentInput() *textinput.Model {
	if s.focused < len(s.qty) {
		return &s.qty[s.focused]
	}
	return &s.notes
}

func (s *ReceiveFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		// The kit breakdown wraps against the pane, so the form has to know how
		// wide it is — a block laid out for an unknown width would be CLIPPED by
		// layout.go's clampToBox, losing the last component it names.
		s.terminalWidth = m.Width
		return s, nil
	case receiveSubmittedMsg:
		s.pending = false
		if m.err != nil {
			s.result = "submit failed: " + m.err.Error()
			s.level = StatusError
			return s, Status(s.result, StatusError)
		}
		label := m.po.Number
		if label == "" {
			label = fmt.Sprintf("PO #%v", m.po.ID)
		}
		if m.po.IsFullyReceived {
			s.result = fmt.Sprintf("%s fully received", label)
		} else {
			s.result = fmt.Sprintf("%s received · %d/%d units", label, m.po.TotalReceivedQuantity, m.po.TotalQuantity)
		}
		s.level = StatusOK
		// If any received line was serialized, move into per-unit serial
		// capture; otherwise the receive is complete.
		if len(s.serialUnits) > 0 {
			s.phase = phaseSerial
			s.serialCursor = 0
			s.serialInput.SetValue("")
			s.serialInput.Focus()
			return s, tea.Batch(Status(s.result, StatusOK), textinput.Blink)
		}
		return s, Status(s.result, StatusOK)

	case serialUnitDoneMsg:
		s.serialPending = false
		if m.err != nil {
			s.failedCount++
			s.serialErr = m.err.Error()
		} else {
			s.serialErr = ""
			if m.created {
				s.createdCount++
			}
			if m.inStock {
				s.inStockCount++
			}
		}
		s.advanceSerial()
		return s, nil

	case tea.KeyMsg:
		switch s.phase {
		case phaseSerial:
			return s.updateSerialKey(m)
		case phaseDone:
			// Any key returns to the PO detail.
			return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, fmt.Sprint(s.po.ID)))
		}
		switch m.Type {
		case tea.KeyTab, tea.KeyDown, tea.KeyShiftTab, tea.KeyUp:
			s.focusNext(m.Type == tea.KeyShiftTab || m.Type == tea.KeyUp)
			return s, nil
		case tea.KeyEnter:
			if s.focused < s.totalInputs()-1 {
				s.focusNext(false)
				return s, nil
			}
			return s.submit()
		case tea.KeyEsc:
			return s, SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, fmt.Sprint(s.po.ID)))
		}
	}

	var cmd tea.Cmd
	if s.phase == phaseSerial {
		s.serialInput, cmd = s.serialInput.Update(msg)
		return s, cmd
	}
	if s.focused < len(s.qty) {
		s.qty[s.focused], cmd = s.qty[s.focused].Update(msg)
	} else {
		s.notes, cmd = s.notes.Update(msg)
	}
	return s, cmd
}

// updateSerialKey handles keys during phase-2 serial capture.
func (s *ReceiveFormScreen) updateSerialKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		// Abandon any remaining captures and show the summary.
		s.phase = phaseDone
		s.serialInput.Blur()
		return s, nil
	case tea.KeyEnter:
		if s.serialPending {
			return s, nil
		}
		return s.submitSerial()
	}
	var cmd tea.Cmd
	s.serialInput, cmd = s.serialInput.Update(m)
	return s, cmd
}

// submitSerial creates a SerializedComponent for the current unit (blank =
// skip) and, on success, accessions it into stock via the receive action.
func (s *ReceiveFormScreen) submitSerial() (Screen, tea.Cmd) {
	if s.serialCursor >= len(s.serialUnits) {
		s.phase = phaseDone
		return s, nil
	}
	serial := strings.TrimSpace(s.serialInput.Value())
	if serial == "" {
		// Blank = skip this unit (serial unknown or captured elsewhere).
		s.skippedCount++
		s.advanceSerial()
		return s, nil
	}
	unit := s.serialUnits[s.serialCursor]
	s.serialPending = true
	s.serialErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		comp, err := deps.OMS.CreateSerializedComponent(ctx, omsapi.SerializedComponentCreate{
			Item:                        unit.itemID,
			SerialNumber:                serial,
			ProvenancePurchaseOrderItem: unit.poItemID,
		})
		if err != nil {
			return serialUnitDoneMsg{err: err}
		}
		// Accession received -> in_stock. A failure here still leaves a
		// valid (received) unit, so we report it created regardless.
		_, rerr := deps.OMS.SerializedComponentAction(
			ctx, comp.ID, omsapi.SerialActionReceive, omsapi.SerializedComponentAction{},
		)
		return serialUnitDoneMsg{created: true, inStock: rerr == nil}
	}
}

// advanceSerial moves to the next capture slot, finishing into the summary
// when the queue is exhausted.
func (s *ReceiveFormScreen) advanceSerial() {
	s.serialCursor++
	s.serialInput.SetValue("")
	if s.serialCursor >= len(s.serialUnits) {
		s.phase = phaseDone
		s.serialInput.Blur()
		return
	}
	s.serialInput.Focus()
}

// poLineSerialized reports whether a PO line's underlying inventory item is
// serialized, returning the item's UUID (needed to create the units). Freeform
// / asset lines have no item_details and return ok=false.
func poLineSerialized(li omsapi.PurchaseOrderItem) (itemID string, ok bool) {
	serialized, _ := li.ItemDetails["is_serialized"].(bool)
	if !serialized {
		return "", false
	}
	id, _ := li.ItemDetails["id"].(string)
	if id == "" {
		return "", false
	}
	return id, true
}

func (s *ReceiveFormScreen) focusNext(reverse bool) {
	s.currentInput().Blur()
	if reverse {
		s.focused--
		if s.focused < 0 {
			s.focused = s.totalInputs() - 1
		}
	} else {
		s.focused = (s.focused + 1) % s.totalInputs()
	}
	s.currentInput().Focus()
}

func (s *ReceiveFormScreen) submit() (Screen, tea.Cmd) {
	var items []omsapi.ReceiptLine
	// Rebuild the serial-capture queue from scratch each submit so a
	// corrected resubmit doesn't double-enroll units.
	s.serialUnits = nil
	for i, ti := range s.qty {
		raw := strings.TrimSpace(ti.Value())
		if raw == "" {
			continue
		}
		qty, err := strconv.Atoi(raw)
		if err != nil || qty < 0 {
			s.result = fmt.Sprintf("line %d: quantity must be a non-negative integer", i+1)
			s.level = StatusError
			return s, nil
		}
		if qty == 0 {
			continue
		}
		line := s.lines[i]
		items = append(items, omsapi.ReceiptLine{
			PurchaseOrderItem: line.ID,
			QuantityReceived:  qty,
		})
		// Enroll one serial-capture slot per received unit of a serialized
		// line so phase 2 can scan a serial into each.
		if itemID, ok := poLineSerialized(line); ok {
			for u := 1; u <= qty; u++ {
				s.serialUnits = append(s.serialUnits, serialUnit{
					itemID:   itemID,
					poItemID: line.ID,
					label:    line.DisplayLabel(),
					unitNo:   u,
					unitTot:  qty,
				})
			}
		}
	}
	if len(items) == 0 {
		s.result = "no quantities entered"
		s.level = StatusWarn
		return s, nil
	}
	req := omsapi.ReceiveRequest{
		Items:        items,
		ReceiptNotes: strings.TrimSpace(s.notes.Value()),
	}
	poID := fmt.Sprint(s.po.ID)
	s.pending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.ReceivePOItems(ctx, poID, req)
		return receiveSubmittedMsg{po: out, err: err}
	}
}

func (s *ReceiveFormScreen) View() string {
	switch s.phase {
	case phaseSerial:
		return s.viewSerial()
	case phaseDone:
		return s.viewDone()
	}

	var b strings.Builder
	header := s.po.Number
	if header == "" {
		header = fmt.Sprintf("PO #%v", s.po.ID)
	}
	b.WriteString(StyleTitle.Render("Receive items into "+header) + "\n\n")
	if s.hasSerializedLine() {
		b.WriteString(StyleMuted.Render("Serialized lines will prompt for a serial per unit after submit.") + "\n\n")
	}
	// The standing warning for a PO carrying a kit. It is wrapped rather than
	// clipped because its second half is the half that matters: an operator who
	// reads only "Kit lines credit" has been told nothing.
	if s.hasKitLine() {
		for _, line := range jdeWrapNote(
			"This order contains kit lines. Receiving one credits the kit's COMPONENT items, not the kit — the quantity you type is a number of kits.",
			s.bodyWidth(),
		) {
			b.WriteString(StyleStatusWarn.Render(line) + "\n")
		}
		b.WriteString("\n")
	}
	if len(s.qty) == 0 {
		b.WriteString(StyleMuted.Render("No receivable lines on this PO.") + "\n\n")
	} else {
		for i, ti := range s.qty {
			caret := "  "
			if i == s.focused {
				caret = "▸ "
			}
			line := s.lines[i]
			b.WriteString(caret + s.lineLabel(line) + "\n")
			// "ordered 2 kits" rather than a separate "quantities are kits"
			// clause: it says the same thing where the number is, and it fits an
			// 80-column pane, which the clause did not.
			unit := ""
			if line.IsKitLine {
				unit = " " + plural("kit", line.QuantityOrdered)
			}
			meta := fmt.Sprintf("    ordered %d%s · received %d",
				line.QuantityOrdered, unit, line.QuantityReceived)
			if line.QuantityPending > 0 {
				meta += fmt.Sprintf(" · pending %d", line.QuantityPending)
			}
			b.WriteString(StyleMuted.Render(meta) + "\n")
			b.WriteString("    qty received: " + ti.View() + "\n")
			// The breakdown sits directly under the box it is a preview of, and
			// recomputes from what is currently typed there.
			for _, kl := range s.kitCreditLines(line, ti.Value()) {
				b.WriteString(kl + "\n")
			}
			b.WriteString("\n")
		}
	}
	caret := "  "
	if s.focused == len(s.qty) {
		caret = "▸ "
	}
	b.WriteString(caret + StyleMuted.Render("Notes") + "\n")
	b.WriteString("    " + s.notes.View() + "\n\n")
	if s.pending {
		b.WriteString(StyleMuted.Render("Submitting…") + "\n")
	} else if s.result != "" {
		b.WriteString(RenderStatus(s.result, s.level) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("tab move · enter submit · esc back"))
	return b.String()
}

// lineLabel is a receivable line's name row: the kit tag first, then as much of
// the label as the pane has left.
//
// Both halves of that order are deliberate. The tag leads because it is what
// changes the meaning of the quantity box below it, and a tag after a long name
// is the first thing clampToBox cuts. The name is then FITTED rather than left
// to overrun, because this row is the one an operator reads to decide which line
// they are typing into, and a name silently cut at the pane edge reads as a
// different (shorter) line.
func (s *ReceiveFormScreen) lineLabel(line omsapi.PurchaseOrderItem) string {
	label := line.DisplayLabel()
	lead := ""
	if line.IsKitLine {
		lead = poKitTag + " "
	}
	if width := s.bodyWidth(); width > 0 {
		// Two columns for the caret the row is drawn with.
		if room := width - 2 - lipgloss.Width(lead); room > 0 {
			label = fitCell(label, room)
		}
	}
	if lead == "" {
		return label
	}
	return StyleStatusWarn.Render(poKitTag) + " " + label
}

// bodyWidth is the columns this screen's body has, or 0 before the first
// WindowSizeMsg — which the JDE width helpers read as "do not truncate".
func (s *ReceiveFormScreen) bodyWidth() int {
	if s.terminalWidth <= 0 {
		return 0
	}
	return screenBodyWidth(s.terminalWidth)
}

// hasKitLine reports whether any receivable line on this form is a kit, so the
// standing warning is drawn only for an order that actually contains one.
func (s *ReceiveFormScreen) hasKitLine() bool {
	for _, line := range s.lines {
		if line.IsKitLine {
			return true
		}
	}
	return false
}

// kitCreditLines is the breakdown drawn under a kit line's quantity box: what
// receiving the quantity currently TYPED there would credit.
//
// An empty or unparseable box shows the per-kit ratio instead of a row of
// zeroes — before a quantity is entered the useful reading is "one kit is these
// five things", and a breakdown that read "0 × cyan ink" would say the opposite
// of what it means. Nothing at all is drawn for a non-kit line.
func (s *ReceiveFormScreen) kitCreditLines(line omsapi.PurchaseOrderItem, typed string) []string {
	if !line.IsKitLine {
		return nil
	}
	lead := "per kit"
	kits := 0
	if qty, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && qty > 0 {
		lead = fmt.Sprintf("receiving %d %s credits", qty, plural("kit", qty))
		kits = qty
	}
	return poKitCreditBlock(line.KitComponents, lead, "    ", s.bodyWidth(), kits)
}

// hasSerializedLine reports whether any receivable line on the form is a
// serialized item, so phase 1 can warn that serials will be captured.
func (s *ReceiveFormScreen) hasSerializedLine() bool {
	for _, line := range s.lines {
		if _, ok := poLineSerialized(line); ok {
			return true
		}
	}
	return false
}

func (s *ReceiveFormScreen) viewSerial() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Capture serial numbers") + "\n")
	total := len(s.serialUnits)
	shown := s.serialCursor + 1
	if shown > total {
		shown = total
	}
	b.WriteString(StyleMuted.Render(fmt.Sprintf(
		"unit %d of %d · created %d · skipped %d", shown, total, s.createdCount, s.skippedCount,
	)) + "\n\n")

	if s.serialCursor < total {
		unit := s.serialUnits[s.serialCursor]
		b.WriteString(unit.label + "\n")
		b.WriteString(StyleMuted.Render(fmt.Sprintf("unit %d of %d on this line", unit.unitNo, unit.unitTot)) + "\n\n")
		b.WriteString("serial: " + s.serialInput.View() + "\n")
	}

	if s.serialPending {
		b.WriteString("\n" + StyleMuted.Render("Saving…"))
	} else if s.serialErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.serialErr))
	}
	b.WriteString("\n\n" + StyleMuted.Render("enter save · blank+enter skip unit · esc finish"))
	return b.String()
}

func (s *ReceiveFormScreen) viewDone() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Receive complete") + "\n\n")
	if s.result != "" {
		b.WriteString(RenderStatus(s.result, s.level) + "\n\n")
	}
	line := fmt.Sprintf("Serialized units: %d created", s.createdCount)
	if s.inStockCount > 0 {
		line += fmt.Sprintf(" (%d accessioned into stock)", s.inStockCount)
	}
	b.WriteString(line + "\n")
	if s.skippedCount > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Skipped: %d", s.skippedCount)) + "\n")
	}
	if s.failedCount > 0 {
		b.WriteString(StyleStatusError.Render(fmt.Sprintf("Failed: %d", s.failedCount)) + "\n")
	}
	if remaining := len(s.serialUnits) - s.serialCursor; remaining > 0 {
		b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("Uncaptured: %d (finished early)", remaining)) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("press any key to return to the PO"))
	return b.String()
}
