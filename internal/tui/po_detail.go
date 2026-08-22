// PurchaseOrderDetailScreen — the purchase order as the shop floor reads it,
// and the surface every order-level action is fired from.
//
// This is the first VIEWING screen on the columnar "JD Edwards" layer (sc-h412,
// purchasing view slice). Everything it draws goes through jde_form.go: the
// readings hang off ONE shared leader column, the lines are a detail grid with
// their extra readings wrapped underneath, and a PERSISTENT action bar sits at
// the bottom naming exactly the keys that work here. po_edit.go is the pilot
// this follows.
//
// A viewing screen is not a form, and two things follow from that:
//
//   - There is no cursor. The body is SCROLLED, not walked, so the frame is
//     positioned by an offset (jdeScreen.frameScrolled) rather than by a focused
//     row. Up/Down move a line, PgUp/PgDn a paneful, Home/End the ends.
//   - There are a dozen order-level commands live at once — receive, edit,
//     attachments, ship a line, send, confirm, deliver, void, order pad,
//     refresh — where the pilot's forms have four or five. No tightening puts
//     twelve keys on the 49 columns an 80-column terminal leaves the bar, so the
//     bar WRAPS (renderActionBarWrapped) the way a real JD Edwards World F-key
//     legend does. Nothing is dropped: a key that fell off the bar is a key the
//     operator cannot discover.
//
// The key scheme, on every phase:
//
//	Up/Down          scroll / move between fields
//	PgUp/PgDn        page
//	Home/End         top / bottom
//	Enter            the phase's own action — receive items on the sheet,
//	                 submit in a modal, copy the order pad
//	Esc              back / cancel  (Ctrl-C always quits, app-wide)
//	E A S s c d v x r   the order-level commands, every one of them named on
//	                 the bar and status-gated exactly as the web page gates its
//	                 buttons
//
// Nothing else is bound. The j/k, g/G, ctrl+d/ctrl+u and R aliases the
// TextScroller era carried are gone: they did something while the footer named
// none of them, which is the opposite of the rule the bar exists to keep.
package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type PurchaseOrderDetailScreen struct {
	deps    Deps
	poID    string
	po      *omsapi.PurchaseOrder
	loading bool
	loadErr string
	// jdeScreen carries the pane geometry and the frames (jde_form.go), shared
	// with po_edit.go and every other columnar screen.
	jdeScreen
	// scroll is the first body line on screen. It is CLAMPED by the frame that
	// draws it and stored back, so the offset held here and the one rendered are
	// never different — which is what makes "↓ 0 more below" impossible.
	scroll int

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

	// "Void PO" modal. voiding == true renders a reason prompt over the body;
	// enter voids, esc cancels. The PO id travels to VoidPurchaseOrder.
	voiding      bool
	voidReasonIn textinput.Model
	voidErr      string
	voidPending  bool

	// "Mark delivered" modal. Four fields — delivery date (default today),
	// tracking #, carrier, receipt notes — mirroring the web mark-delivered
	// modal. This is also the PO's tracking-entry path (OMS has no separate
	// PO update-tracking endpoint; tracking/carrier ride on mark-delivered).
	delivering     bool
	deliverInputs  []textinput.Model
	deliverFocus   int
	deliverErr     string
	deliverPending bool

	// "Order pad" overlay (parity with web #855). When orderPad == true the
	// screen renders the vendor-agnostic part#/qty pad over the body; esc
	// closes. On load the pad text is also pushed to the terminal clipboard via
	// OSC 52 so an operator on SSH can paste it into a vendor site on their own
	// machine, and Enter re-copies it. padScroll is its own scroll offset.
	orderPad        bool
	orderPadLoading bool
	orderPadExport  *omsapi.OrderPadExport
	orderPadErr     string
	padScroll       int
}

// Mark-delivered field indexes.
const (
	poDeliverDate = iota
	poDeliverTracking
	poDeliverCarrier
	poDeliverNotes
	poDeliverFieldCount
)

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

// poVoidedMsg reports the result of voiding the whole PO.
type poVoidedMsg struct {
	err error
}

// poDeliveredMsg reports the result of mark-delivered.
type poDeliveredMsg struct {
	err error
}

// poOrderPadMsg reports the result of building the order pad (parity #855).
type poOrderPadMsg struct {
	export *omsapi.OrderPadExport
	err    error
}

func NewPurchaseOrderDetailScreen(deps Deps, id string) *PurchaseOrderDetailScreen {
	return &PurchaseOrderDetailScreen{
		deps:    deps,
		poID:    id,
		loading: true,
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

// WantsRawInput routes every key to the screen while any modal (mark-shipped,
// void, mark-delivered) or the order-pad overlay is open so the textinputs
// receive characters — and the overlay's scroll/close keys stay local —
// without the app dispatcher claiming letters like 'r' / 'S' / 'v' / 'd'.
func (s *PurchaseOrderDetailScreen) WantsRawInput() bool {
	return s.shipping || s.voiding || s.delivering || s.orderPad
}

// HandlesKey claims lowercase 's' (send-to-supplier) so it beats the global
// s=settings nav; the handler itself gates the action to draft POs. Any modal
// open already routes every key here via WantsRawInput.
func (s *PurchaseOrderDetailScreen) HandlesKey(key string) bool { return key == "s" }

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
		s.setSize(m)
		return s, nil
	case poDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.po = m.po
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

	case poVoidedMsg:
		s.voidPending = false
		if m.err != nil {
			s.voidErr = m.err.Error()
			return s, Status("void failed: "+m.err.Error(), StatusError)
		}
		s.voiding = false
		s.voidErr = ""
		s.loading = true
		s.loadErr = ""
		return s, tea.Batch(Status("purchase order voided", StatusOK), s.load())

	case poDeliveredMsg:
		s.deliverPending = false
		if m.err != nil {
			s.deliverErr = m.err.Error()
			return s, Status("mark delivered failed: "+m.err.Error(), StatusError)
		}
		s.delivering = false
		s.deliverErr = ""
		s.loading = true
		s.loadErr = ""
		return s, tea.Batch(Status("marked delivered", StatusOK), s.load())

	case poOrderPadMsg:
		// Ignore a result the operator already walked away from (esc during
		// load) — don't fire a surprise clipboard write + toast for a pad they
		// abandoned.
		if !s.orderPad {
			return s, nil
		}
		s.orderPadLoading = false
		if m.err != nil {
			s.orderPadErr = m.err.Error()
			return s, Status("order pad failed: "+m.err.Error(), StatusError)
		}
		s.orderPadExport = m.export
		s.orderPadErr = ""
		s.padScroll = 0
		// Push the paste-ready pad to the terminal clipboard (best-effort) and
		// summarize the result — including any lines missing a part number.
		return s, tea.Batch(copyToClipboardCmd(m.export.Text), Status(s.orderPadToast(), s.orderPadToastLevel()))

	case tea.KeyMsg:
		if s.shipping {
			return s.handleShipKey(m)
		}
		if s.voiding {
			return s.handleVoidKey(m)
		}
		if s.delivering {
			return s.handleDeliverKey(m)
		}
		if s.orderPad {
			return s.handleOrderPadKey(m)
		}
		return s.handleSheetKey(m)
	}
	return s, nil
}

// handleSheetKey drives the detail sheet itself. Every key it recognises is on
// the action bar and every key on the bar is here — the two are read together,
// because the bar is now the only place an operator learns what works.
func (s *PurchaseOrderDetailScreen) handleSheetKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "up":
		s.scrollBy(-1)
		return s, nil
	case "down":
		s.scrollBy(+1)
		return s, nil
	case "pgup":
		s.scrollBy(-s.pageStep())
		return s, nil
	case "pgdown":
		s.scrollBy(+s.pageStep())
		return s, nil
	case "home":
		s.scroll = 0
		return s, nil
	case "end":
		// The frame clamps whatever it is handed, so "past the end" is how the
		// end is asked for.
		s.scroll = s.sheetLines().Len()
		return s, nil

	case "enter":
		if s.po != nil {
			return s, SwitchTo(WSPurchasing, NewReceiveFormScreen(s.deps, s.po))
		}
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
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
	case "E":
		// Edit metadata + line items. Uppercase E because lowercase e is
		// a global ForgeKey hotkey (matches inventory-detail's E edit).
		if s.po != nil {
			return s, SwitchTo(WSPurchasing, NewPurchaseOrderEditScreen(s.deps, s.po))
		}
	case "A":
		// Attachments. Uppercase A because lowercase a is the global
		// Authorizations hotkey.
		if s.po != nil {
			return s, SwitchTo(WSPurchasing, NewPurchaseOrderAttachmentsScreen(s.deps, s.po))
		}
	case "x":
		// Export the vendor-agnostic order pad (part#/qty) — parity with
		// web #855. Lowercase x is free in the global keymap (not a nav
		// hotkey, not in the global switch), so it reaches the screen here
		// without needing HandlesKey. 'o' would collide with the global
		// Operational-Modes hotkey, so x = export.
		if s.po == nil {
			return s, nil
		}
		return s, s.openOrderPad()
	case "d":
		// Mark delivered. Gated on the backend-receivable states; the
		// server also enforces this and 400s otherwise.
		if s.po == nil || !poCanMarkDelivered(s.po.Status) {
			return s, Status("mark delivered needs a sent / confirmed / partially-received PO", StatusWarn)
		}
		s.openDeliverForm()
		return s, textinput.Blink
	case "v":
		// Void the whole PO. Gated locally on status (backend also
		// restricts to staff/COO and rejects voided/received POs).
		if s.po == nil {
			return s, nil
		}
		if s.po.Status == "voided" {
			return s, Status("purchase order is already voided", StatusWarn)
		}
		if s.po.Status == "received" || s.po.IsFullyReceived {
			return s, Status("cannot void a received PO; create a return instead", StatusWarn)
		}
		s.openVoidForm()
		return s, textinput.Blink
	}
	return s, nil
}

func (s *PurchaseOrderDetailScreen) scrollBy(delta int) {
	s.scroll += delta
	if s.scroll < 0 {
		s.scroll = 0
	}
}

// pageStep is one paneful of body, computed from the bar the pane is currently
// drawing — a wrapped bar leaves fewer rows, and a page that moved by more than
// the operator can see would skip content.
func (s *PurchaseOrderDetailScreen) pageStep() int {
	return s.scrollRows(actionBarRowsFor(s.barWidth(), s.sheetBar()), 0)
}

// poCanMarkDelivered reports whether a PO in the given status can be
// mark-delivered, matching the backend's sent/confirmed/partially_received
// precondition.
func poCanMarkDelivered(status string) bool {
	switch status {
	case "sent", "confirmed", "partially_received":
		return true
	}
	return false
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
	idx.CharLimit = 4
	idx.Focus()
	date := textinput.New()
	date.Prompt = ""
	date.CharLimit = 12
	date.SetValue(time.Now().Format("2006-01-02"))
	s.shipIdxIn = idx
	s.shipDateIn = date
	s.shipFocus = 0
	s.shipErr = ""
	s.shipping = true
}

func (s *PurchaseOrderDetailScreen) handleShipKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.shipping = false
		s.shipErr = ""
		return s, nil
	case "tab", "down", "shift+tab", "up":
		if s.shipFocus == 0 {
			s.shipIdxIn.Blur()
			s.shipDateIn.Focus()
			s.shipFocus = 1
		} else {
			s.shipDateIn.Blur()
			s.shipIdxIn.Focus()
			s.shipFocus = 0
		}
		return s, textinput.Blink
	case "enter":
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

// ---------------------------------------------------------------------------
// Void-PO modal
// ---------------------------------------------------------------------------

func (s *PurchaseOrderDetailScreen) openVoidForm() {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 300
	in.Focus()
	s.voidReasonIn = in
	s.voidErr = ""
	s.voiding = true
}

func (s *PurchaseOrderDetailScreen) handleVoidKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.voiding = false
		s.voidErr = ""
		return s, nil
	case "enter":
		if s.voidPending {
			return s, nil
		}
		reason := strings.TrimSpace(s.voidReasonIn.Value())
		poID := fmt.Sprintf("%v", s.po.ID)
		s.voidPending = true
		s.voidErr = ""
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		return s, func() tea.Msg {
			_, err := deps.OMS.VoidPurchaseOrder(ctx, poID, reason)
			return poVoidedMsg{err: err}
		}
	}
	var cmd tea.Cmd
	s.voidReasonIn, cmd = s.voidReasonIn.Update(m)
	return s, cmd
}

// ---------------------------------------------------------------------------
// Mark-delivered modal
// ---------------------------------------------------------------------------

func (s *PurchaseOrderDetailScreen) openDeliverForm() {
	s.deliverInputs = make([]textinput.Model, poDeliverFieldCount)
	date := textinput.New()
	date.Prompt = ""
	date.CharLimit = 10
	date.SetValue(time.Now().Format("2006-01-02"))
	date.Focus()
	s.deliverInputs[poDeliverDate] = date

	tracking := textinput.New()
	tracking.Prompt = ""
	tracking.CharLimit = 100
	s.deliverInputs[poDeliverTracking] = tracking

	carrier := textinput.New()
	carrier.Prompt = ""
	carrier.CharLimit = 100
	s.deliverInputs[poDeliverCarrier] = carrier

	notes := textinput.New()
	notes.Prompt = ""
	notes.CharLimit = 500
	s.deliverInputs[poDeliverNotes] = notes

	s.deliverFocus = poDeliverDate
	s.deliverErr = ""
	s.delivering = true
}

func (s *PurchaseOrderDetailScreen) handleDeliverKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.delivering = false
		s.deliverErr = ""
		return s, nil
	case "tab", "down":
		s.deliverInputs[s.deliverFocus].Blur()
		s.deliverFocus = (s.deliverFocus + 1) % poDeliverFieldCount
		s.deliverInputs[s.deliverFocus].Focus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.deliverInputs[s.deliverFocus].Blur()
		s.deliverFocus = (s.deliverFocus - 1 + poDeliverFieldCount) % poDeliverFieldCount
		s.deliverInputs[s.deliverFocus].Focus()
		return s, textinput.Blink
	case "enter":
		if s.deliverPending {
			return s, nil
		}
		return s.submitDeliver()
	}
	var cmd tea.Cmd
	s.deliverInputs[s.deliverFocus], cmd = s.deliverInputs[s.deliverFocus].Update(m)
	return s, cmd
}

func (s *PurchaseOrderDetailScreen) submitDeliver() (Screen, tea.Cmd) {
	date := strings.TrimSpace(s.deliverInputs[poDeliverDate].Value())
	if _, err := time.Parse("2006-01-02", date); err != nil {
		s.deliverErr = "delivery date is required (YYYY-MM-DD)"
		return s, nil
	}
	req := omsapi.MarkDeliveredRequest{
		DeliveryDate:   date,
		TrackingNumber: strings.TrimSpace(s.deliverInputs[poDeliverTracking].Value()),
		Carrier:        strings.TrimSpace(s.deliverInputs[poDeliverCarrier].Value()),
		ReceiptNotes:   strings.TrimSpace(s.deliverInputs[poDeliverNotes].Value()),
	}
	poID := fmt.Sprintf("%v", s.po.ID)
	s.deliverPending = true
	s.deliverErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		_, err := deps.OMS.MarkPurchaseOrderDelivered(ctx, poID, req)
		return poDeliveredMsg{err: err}
	}
}

// ---------------------------------------------------------------------------
// Order-pad overlay (parity with web #855)
// ---------------------------------------------------------------------------

// openOrderPad opens the order-pad overlay and kicks off the async fetch. The
// scroll offset is reset each open so a stale position from a previous view
// can't leave the new pad opening halfway down.
func (s *PurchaseOrderDetailScreen) openOrderPad() tea.Cmd {
	s.orderPad = true
	s.orderPadLoading = true
	s.orderPadErr = ""
	s.orderPadExport = nil
	s.padScroll = 0
	return s.fetchOrderPad()
}

func (s *PurchaseOrderDetailScreen) fetchOrderPad() tea.Cmd {
	poID := fmt.Sprintf("%v", s.po.ID)
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		export, err := deps.OMS.ExportOrderPad(ctx, poID)
		return poOrderPadMsg{export: export, err: err}
	}
}

// handleOrderPadKey drives the order-pad overlay: scroll the pad, re-copy it to
// the terminal clipboard, or close. WantsRawInput routes every key here while
// the overlay is open, so keys the overlay doesn't recognize are swallowed
// rather than leaking to the global nav behind it.
//
// Enter is the copy, not 'c': the reduced scheme gives Enter to the phase's own
// action wherever it can, and the overlay's action is putting the pad on the
// clipboard. 'c' means Confirm order one Esc away, and the same letter meaning
// two things two keystrokes apart is what the bar cannot explain.
func (s *PurchaseOrderDetailScreen) handleOrderPadKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.orderPad = false
		s.orderPadErr = ""
		return s, nil
	case "enter":
		// Re-copy on demand (mirrors the web "Copy order pad" button) in case
		// the auto-copy on open didn't land in the operator's terminal.
		if s.orderPadExport != nil && s.orderPadExport.Text != "" {
			return s, tea.Batch(
				copyToClipboardCmd(s.orderPadExport.Text),
				Status("order pad copied to clipboard", StatusOK),
			)
		}
		return s, nil
	case "up":
		s.padScrollBy(-1)
	case "down":
		s.padScrollBy(+1)
	case "pgup":
		s.padScrollBy(-s.padPageStep())
	case "pgdown":
		s.padScrollBy(+s.padPageStep())
	case "home":
		s.padScroll = 0
	case "end":
		s.padScroll = s.orderPadLines().Len()
	}
	return s, nil
}

func (s *PurchaseOrderDetailScreen) padScrollBy(delta int) {
	s.padScroll += delta
	if s.padScroll < 0 {
		s.padScroll = 0
	}
}

func (s *PurchaseOrderDetailScreen) padPageStep() int {
	return s.scrollRows(actionBarRowsFor(s.barWidth(), s.orderPadBar()), len(s.orderPadHeader()))
}

// orderPadLines formats the pad's part#/qty lines for on-screen display. The raw
// payload is tab-separated; here it's shown in aligned columns so the operator
// can scan it, while the clipboard copy always carries the exact tab-separated
// text (a paste lands correctly in a spreadsheet or vendor order pad). Returns
// an empty-state note when no line carries a supplier part number.
//
// The part column is sized from the pad AND the pane rather than pinned at 28:
// at 80 columns the body has 51 to spend, and a fixed 28-column field left the
// quantity — the half of the row an operator is actually checking — hanging off
// the right edge where clampToBox cut it; on a wide terminal the same 28 cut a
// long manufacturer part number with half the pane standing empty beside it.
func (s *PurchaseOrderDetailScreen) orderPadLines() *jdeLines {
	l := &jdeLines{}
	if s.orderPadExport == nil || strings.TrimSpace(s.orderPadExport.Text) == "" {
		l.Add(jdeIndent + StyleMuted.Render("No lines have a supplier part number — nothing to order."))
		return l
	}
	// Both columns are measured from the pad's own values. 28 and 6 are FLOORS,
	// the way the pre-columnar "%-28s %s" row used them, not ceilings: the part
	// number is the field an operator retypes into a vendor site and the
	// quantity is a NUMBER, and a shortened number is a wrong number — "100000…"
	// reads as a quantity that is not the quantity. So the quantity column takes
	// what it needs and the part column gives up the room, which is the same
	// rule the line grid follows (poFitLineGrid).
	//
	// poPadQtyMaxW is past any quantity an order can carry; a token wider than
	// that is malformed export data rather than a count, and ellipsising it is
	// then the honest signal that it is not what it looks like.
	const partMinW, qtyMinW, poPadQtyMaxW = 28, 6, 12
	partW, qtyW := partMinW, qtyMinW
	for _, line := range strings.Split(s.orderPadExport.Text, "\n") {
		part, qty, found := strings.Cut(line, "\t")
		if !found {
			continue
		}
		if w := lipgloss.Width(part); w > partW {
			partW = w
		}
		if w := lipgloss.Width(qty); w > qtyW {
			if w > poPadQtyMaxW {
				w = poPadQtyMaxW
			}
			qtyW = w
		}
	}
	if body := s.bodyWidth(); body > 0 {
		if w := body - len(jdeIndent) - 2 - qtyW; w < partW {
			partW = w
		}
		if partW < 8 {
			partW = 8
		}
	}
	l.Add(StyleMuted.Render(jdeIndent + padCell("Part #", partW, alignLeft) + "  " + padCell("Qty", qtyW, alignRight)))
	for _, line := range strings.Split(s.orderPadExport.Text, "\n") {
		part, qty, found := strings.Cut(line, "\t")
		if !found {
			l.Add(jdeIndent + fitCell(line, partW+2+qtyW))
			continue
		}
		l.Add(jdeIndent + padCell(fitCell(part, partW), partW, alignLeft) + "  " + padCell(fitCell(qty, qtyW), qtyW, alignRight))
	}
	return l
}

// orderPadToast summarizes the load for the status bar: how many usable lines
// were copied and how many were omitted for a missing supplier part number.
func (s *PurchaseOrderDetailScreen) orderPadToast() string {
	if s.orderPadExport == nil {
		return "order pad built"
	}
	n := s.orderPadExport.LineCount
	var msg string
	if n == 0 || s.orderPadExport.Text == "" {
		msg = "order pad: no lines have a supplier part number"
	} else {
		msg = fmt.Sprintf("order pad copied to clipboard (%d %s)", n, plural("line", n))
	}
	if miss := len(s.orderPadExport.MissingSku); miss > 0 {
		msg += fmt.Sprintf(" · %d %s missing part #", miss, plural("line", miss))
	}
	return msg
}

// orderPadToastLevel picks the status level: OK for a clean copy, Warn when
// nothing was orderable or some lines were dropped for a missing part number.
func (s *PurchaseOrderDetailScreen) orderPadToastLevel() StatusLevel {
	if s.orderPadExport == nil {
		return StatusInfo
	}
	if s.orderPadExport.LineCount == 0 || s.orderPadExport.Text == "" || len(s.orderPadExport.MissingSku) > 0 {
		return StatusWarn
	}
	return StatusOK
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

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
	switch {
	case s.shipping:
		return s.viewShip()
	case s.voiding:
		return s.viewVoid()
	case s.delivering:
		return s.viewDeliver()
	case s.orderPad:
		return s.viewOrderPad()
	}
	return s.viewSheet()
}

// viewSheet draws the order itself: the columnar body, windowed at the
// operator's scroll offset, over the persistent bar.
func (s *PurchaseOrderDetailScreen) viewSheet() string {
	body := s.sheetLines()
	status := jdeStatusLine(s.loading || s.transitioning, "Working…", "")
	frame, offset := s.frameScrolled(nil, body, s.scroll, status, s.sheetBar())
	s.scroll = offset
	return frame
}

// sheetBar names every key that works on the sheet, and only those. The three
// state transitions are gated exactly as the web page gates its buttons, so the
// bar never offers a key the handler is about to refuse.
//
// It runs long — a purchase order carries more commands than a form does — and
// renderActionBarWrapped folds it onto as many rows as it needs rather than
// letting the tail fall off a narrow pane.
func (s *PurchaseOrderDetailScreen) sheetBar() []actionBarItem {
	items := []actionBarItem{
		{"Enter", "Receive"},
		{"Esc", "Back"},
		{"UP/DN", "Scroll"},
		{"PgUp/PgDn", "Page"},
		{"Home/End", "Top/End"},
		{"E", "Edit"},
		{"A", "Files"},
	}
	if s.po != nil {
		switch s.po.Status {
		case "draft":
			items = append(items, actionBarItem{"s", "Send"})
		case "sent":
			items = append(items, actionBarItem{"c", "Confirm"})
		}
		if len(s.po.Items) > 0 {
			items = append(items, actionBarItem{"S", "Ship line"})
		}
		if poCanMarkDelivered(s.po.Status) {
			items = append(items, actionBarItem{"d", "Delivered"})
		}
	}
	items = append(items, actionBarItem{"x", "Order pad"})
	// Void is offered whenever the PO isn't already voided/received (the
	// backend still enforces the staff/COO permission).
	if s.po != nil && s.po.Status != "voided" && s.po.Status != "received" && !s.po.IsFullyReceived {
		items = append(items, actionBarItem{"v", "Void"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

// renderBody is the sheet's body as one string — every line of it, unwindowed.
func (s *PurchaseOrderDetailScreen) renderBody() string {
	return strings.Join(s.sheetLines().text, "\n")
}

// poDetailLabels is the sheet's label column, in one place so every band shares
// it — the metadata, the associations, the terms and the totals all hang off
// ONE leader, the way po_edit.go's header and association bands do.
//
// The prompts are terse on purpose. The label column is shared, so the longest
// label pushes every value right on every row: at 80 columns the pane is 51
// wide, and "Days since ordered" spent eighteen of them to say what "Age" says
// in three. Terse prompts are the JD Edwards World idiom and here they are also
// what keeps a work-order label on the row it belongs to.
var poDetailLabels = []string{
	"Supplier", "Status", "Agreement", "Work order", "Committee",
	"Created by", "Sent by", "Voided", "Void reason",
	"PO ID", "Supplier PO #", "Sales order #",
	"Ordered", "Expected", "Created", "Updated", "Age",
	"Estimated", "Actual", "Total", "Lines", "Quantity",
	"Priority", "Payment terms", "Freight terms", "Payment",
}

// poDetailLabelWidth is the shared leader column, computed from every label the
// sheet can draw rather than from the ones this particular order happens to
// carry: an order that grows a "Voided" row on the next reload must not shift
// every other value one column right.
func poDetailLabelWidth() int {
	fields := make([]jdeField, len(poDetailLabels))
	for i, l := range poDetailLabels {
		fields[i] = jdeField{Label: l}
	}
	return jdeLabelWidth(fields)
}

// sheetLines builds the whole detail body, band by band.
func (s *PurchaseOrderDetailScreen) sheetLines() *jdeLines {
	po := s.po
	l := &jdeLines{}
	if po == nil {
		return l
	}
	lw := poDetailLabelWidth()

	header := po.Number
	if header == "" {
		header = fmt.Sprintf("PO #%v", po.ID)
	}
	head := StyleTitle.Render(header)
	if po.Status == "voided" {
		head += "  " + StyleStatusError.Render("VOIDED")
	} else if po.IsFullyReceived {
		head += "  " + StyleStatusOK.Render("RECEIVED")
	}
	l.Add(head)

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
	// The second line is the one-glance summary: who, what state, and — only
	// when it is not the default every order starts at (op-bwo9) — how urgent.
	// "normal" on every PO would be noise, while an urgent one is the whole
	// reason the field exists. The Terms band below always names it, so nothing
	// is hidden; this is just where a rush order says so without being scrolled
	// to.
	summary := []jdeToken{
		{text: supplier, style: StyleMuted},
		{text: status, style: StyleMuted},
	}
	if po.Priority != "" && po.Priority != poDefaultPriority {
		summary = append(summary, jdeToken{
			text:  poTermsLabel(poPriorityOptions, po.Priority) + " priority",
			style: StyleStatusWarn,
		})
	}
	// Wrapped rather than trimmed: a long supplier name is exactly what pushes
	// the priority badge — the piece an ellipsis would eat — off an 80-column
	// pane, and the badge is the reason this line carries it at all.
	for _, line := range jdeWrapTokens(summary, "", s.bodyWidth()) {
		l.Add(line)
	}
	l.Add("")

	s.addBand(l, lw, "Order", s.orderFields(supplier, status))
	s.addBand(l, lw, "Identifiers", s.identifierFields())
	s.addBand(l, lw, "Dates", s.dateFields())
	s.addBand(l, lw, "Totals", s.totalFields())
	s.addBand(l, lw, "Terms", poTermsFields(po))

	if po.Notes != "" {
		l.Add(StyleJDEHeading.Render("Notes"))
		for _, line := range jdeWrapNote(po.Notes, jdeStripWidth(s.bodyWidth(), 0)) {
			l.Add(jdeIndent + line)
		}
		l.Add("")
	}

	s.addLineItems(l, supplier)
	s.addAttachments(l)
	return l
}

// addBand draws one heading and the rows under it, or nothing at all when the
// band has no rows — an order carries most of these optionally, and a heading
// over an empty band is a question the sheet is answering with silence.
func (s *PurchaseOrderDetailScreen) addBand(l *jdeLines, labelWidth int, heading string, fields []jdeField) {
	if len(fields) == 0 {
		return
	}
	l.Add(StyleJDEHeading.Render(heading))
	for _, f := range fields {
		s.addValueRow(l, f, labelWidth)
	}
	l.Add("")
}

// addValueRow draws one columnar reading, FOLDING a value too wide for the pane
// onto continuation lines indented under the input area rather than letting it
// run off the edge.
//
// clampToBox truncates an over-wide row with nothing to say it had (sc-ye0i),
// and at 80 columns this pane is 51 wide: a work-order label, a supplier name
// or the payment schedule's "$1234.56 due 2026-08-14 · Net 30 from order date"
// all overrun it. A folded value is legible at every width; a cut one is
// legible at none.
func (s *PurchaseOrderDetailScreen) addValueRow(l *jdeLines, f jdeField, labelWidth int) {
	avail := jdeStripWidth(s.bodyWidth(), labelWidth)
	if avail <= 0 || lipgloss.Width(f.Value) <= avail {
		l.Add(renderJDEField(f, labelWidth))
		return
	}
	wrapped := jdeWrapNote(f.Value, avail)
	if len(wrapped) == 0 {
		// jdeWrapNote returns NOTHING when it cannot fold: below two columns it
		// has no room for even the ellipsis it would mark a broken word with, so
		// it drops every word wider than the width and can come back empty. That
		// is documented behaviour of the shared helper — every other caller
		// ranges over the result — and indexing it here panicked the whole TUI
		// at exactly one terminal width (52, where this strip computes to one
		// column), which a terminal being dragged narrower passes through.
		//
		// A reading that vanished instead would be the same bug told quietly, so
		// the row still draws its label and whatever of the value fits, visibly
		// trimmed.
		head := f
		head.Value = fitCell(f.Value, avail)
		l.Add(renderJDEField(head, labelWidth))
		return
	}
	head := f
	head.Value = wrapped[0]
	l.Add(renderJDEField(head, labelWidth))
	for _, line := range wrapped[1:] {
		if f.Dim {
			line = StyleMuted.Render(line)
		}
		l.Add(jdeStripIndent(labelWidth) + line)
	}
}

// orderFields is the "what and who" band. Every row here is drawn only when the
// order carries it: the associations, the agreement and the void trail are
// optional on every order, and a pair of empty rows on the many that carry
// neither would crowd out the ones that do (op-shb9, op-yoos).
func (s *PurchaseOrderDetailScreen) orderFields(supplier, status string) []jdeField {
	po := s.po
	out := []jdeField{
		{Label: "Supplier", Kind: jdeValue, Value: supplier},
		{Label: "Status", Kind: jdeValue, Value: status},
	}
	if po.SupplierAgreementRef != nil && po.SupplierAgreementRef.Name != "" {
		out = append(out, jdeField{Label: "Agreement", Kind: jdeValue, Value: po.SupplierAgreementRef.Name})
	}
	// Who this order was placed for (op-shb9). They ride the PO payload, so
	// there is no separate load to distinguish from "none" — hence no loadErr,
	// unlike the edit screen's rows. E opens the editor that sets them.
	if label := po.WorkOrderRef.Label(); label != "" {
		out = append(out, poAssocValueField("Work order", label, ""))
	}
	if name := poCommitteeRefLabel(po.OwningGroupRef); name != "" {
		out = append(out, poAssocValueField("Committee", name, ""))
	}
	if po.CreatedByUsername != "" {
		out = append(out, jdeField{Label: "Created by", Kind: jdeValue, Value: po.CreatedByUsername})
	}
	if po.SentByUsername != "" {
		value := po.SentByUsername
		if po.SentAt != nil {
			value += " on " + po.SentAt.Format("2006-01-02")
		}
		out = append(out, jdeField{Label: "Sent by", Kind: jdeValue, Value: value})
	}
	if po.VoidedAt != nil {
		value := po.VoidedAt.Format("2006-01-02")
		if po.VoidedByUsername != "" {
			value += " by " + po.VoidedByUsername
		}
		out = append(out, jdeField{Label: "Voided", Kind: jdeValue, Value: value})
		if po.VoidReason != "" {
			out = append(out, jdeField{Label: "Void reason", Kind: jdeValue, Value: po.VoidReason})
		}
	}
	return out
}

func (s *PurchaseOrderDetailScreen) identifierFields() []jdeField {
	po := s.po
	out := []jdeField{{Label: "PO ID", Kind: jdeValue, Value: fmt.Sprintf("%v", po.ID)}}
	if po.SupplierOrderNumber != "" {
		out = append(out, jdeField{Label: "Supplier PO #", Kind: jdeValue, Value: po.SupplierOrderNumber})
	}
	if po.SalesOrderNumber != "" {
		out = append(out, jdeField{Label: "Sales order #", Kind: jdeValue, Value: po.SalesOrderNumber})
	}
	return out
}

func (s *PurchaseOrderDetailScreen) dateFields() []jdeField {
	po := s.po
	var out []jdeField
	if !po.OrderDate.IsZero() {
		out = append(out, jdeField{Label: "Ordered", Kind: jdeValue, Value: po.OrderDate.Format("2006-01-02")})
	}
	if po.ExpectedDeliveryDate != "" {
		out = append(out, jdeField{Label: "Expected", Kind: jdeValue, Value: po.ExpectedDeliveryDate})
	}
	if !po.CreatedAt.IsZero() {
		out = append(out, jdeField{Label: "Created", Kind: jdeValue, Value: po.CreatedAt.Format("2006-01-02 15:04")})
	}
	if !po.UpdatedAt.IsZero() {
		out = append(out, jdeField{Label: "Updated", Kind: jdeValue, Value: po.UpdatedAt.Format("2006-01-02 15:04")})
	}
	if po.DaysSinceOrdered != nil {
		// "Age" rather than "Days since ordered": the unit rides the value, where
		// it costs the shared label column nothing.
		out = append(out, jdeField{Label: "Age", Kind: jdeValue,
			Value: fmt.Sprintf("%d %s since ordered", *po.DaysSinceOrdered, plural("day", *po.DaysSinceOrdered))})
	}
	return out
}

func (s *PurchaseOrderDetailScreen) totalFields() []jdeField {
	po := s.po
	currency := po.Currency
	if currency == "" {
		currency = "USD"
	}
	var out []jdeField
	if !po.EstimatedTotal.Empty() {
		out = append(out, jdeField{Label: "Estimated", Kind: jdeValue, Value: fmt.Sprintf("$%s %s", po.EstimatedTotal, currency)})
	}
	if !po.ActualTotal.Empty() {
		out = append(out, jdeField{Label: "Actual", Kind: jdeValue, Value: fmt.Sprintf("$%s %s", po.ActualTotal, currency)})
	}
	if po.Total > 0 && po.EstimatedTotal.Empty() && po.ActualTotal.Empty() {
		out = append(out, jdeField{Label: "Total", Kind: jdeValue, Value: fmt.Sprintf("$%.2f %s", po.Total, currency)})
	}
	if po.TotalItems > 0 {
		out = append(out, jdeField{Label: "Lines", Kind: jdeValue, Value: strconv.Itoa(po.TotalItems)})
	}
	if po.TotalQuantity > 0 {
		value := strconv.Itoa(po.TotalQuantity)
		if po.TotalReceivedQuantity > 0 {
			value += fmt.Sprintf(" · received %d", po.TotalReceivedQuantity)
		}
		out = append(out, jdeField{Label: "Quantity", Kind: jdeValue, Value: value})
	}
	return out
}

// ---------------------------------------------------------------------------
// The line-item detail grid
// ---------------------------------------------------------------------------

// poLineGridFit is which of the detail grid's columns the pane can hold, and
// how wide the item column is once the rest have had their say.
//
// The grid SHEDS columns rather than being cut. po_edit.go's grid is six
// columns wide — number, item, quantity, cost, ship date, flag — and at 80
// columns this pane is 51 wide, where those six plus the twelve-column floor on
// the item name come to 59. clampToBox does not wrap: it cut the last cell off
// the end, which is the FLAG cell, so a voided line at 80 columns looked exactly
// like a live one. Nothing the grid drops is lost — it moves to the readings
// under the row, where jdeWrapTokens wraps instead of trimming.
type poLineGridFit struct {
	itemW int
	// The FIXED columns' widths, budgeted from the values this order actually
	// carries rather than assumed from the poGrid* constants. poLineGridRow pads
	// a cell to its constant and padCell never truncates, so a value wider than
	// its constant used to widen the whole ROW: QuantityOrdered 10000 is five
	// columns in a four-column budget, and at 80 the ship date reached the
	// terminal as "2026-08-1" — a date silently missing a digit. Measuring the
	// columns instead lets the ITEM column absorb the difference, which is the
	// same guarantee poFlagBudget gives the flag cell.
	numW  int
	qtyW  int
	costW int
	shipW int
	flagW int
	ship  bool
	flag  bool
}

// The ceilings on those measured widths. They are set well past any value OMS
// can legitimately send — a seven-digit quantity, a "$1234567.8900" cost, an
// ISO date with room to spare — so an ordinary line is never shortened, and
// they exist only so that a garbage value cannot eat the item column and push
// the row off the pane anyway. Past the ceiling fitCell ellipsises, which at
// least SAYS the value was cut; clampToBox says nothing.
const (
	poGridNumMaxW  = 5
	poGridQtyMaxW  = 9
	poGridCostMaxW = 14
	poGridShipMaxW = 12
)

// poGridCell fits a cell to the width its column was budgeted at and pads it
// there, so no value can widen the row and every row's columns start in the
// same screen position. poLineGridRow pads to the poGrid* CONSTANTS, which is
// both too narrow for a measured column and left alone here on purpose:
// po_edit.go owns that function and is a separate slice.
func poGridCell(s string, w int, align colAlign) string {
	return padCell(fitCell(s, w), w, align)
}

// poDetailGridRow is poLineGridRow with every cell already fitted and padded to
// this pane's budget.
func poDetailGridRow(fit poLineGridFit, num, item, qty, cost, ship, flag string) string {
	return poLineGridRow(
		poGridCell(num, fit.numW, alignRight),
		poGridCell(item, fit.itemW, alignLeft),
		poGridCell(qty, fit.qtyW, alignRight),
		poGridCell(cost, fit.costW, alignRight),
		poGridCell(ship, fit.shipW, alignLeft),
		poGridCell(flag, fit.flagW, alignLeft),
		fit.itemW,
	)
}

// Grid arithmetic: six cells joined by a two-column gutter, behind jdeIndent.
const poGridGutter = 2

// coreW is number + quantity + cost and the gutters between them, at the widths
// this order's own values need.
func (fit poLineGridFit) coreW() int {
	return len(jdeIndent) + fit.numW + poGridGutter + poGridGutter + fit.qtyW + poGridGutter + fit.costW
}

// poFlagBudget is how wide the flag cell has to be RESERVED: the widest string
// poLineFlag can actually return, in display columns.
//
// It is measured off poLineFlag rather than taken from poGridFlagW because the
// two disagree and poGridFlagW is the one that is wrong. poGridFlagW is 8 —
// exactly "[voided]" — but a received line's flag is "✓ received", which is 10,
// so budgeting 8 let the full-grid row measure two columns wider than the pane
// and clampToBox ate the tail: "✓ receiv". That is the same silent truncation
// this grid sheds cells to avoid, and the tests missed it only because the
// fixture's received line was ALSO voided, which takes the 8-wide branch.
//
// poGridFlagW itself is left alone: po_edit.go shares it, and the entry screens
// are a separate slice.
var poFlagBudget = func() int {
	widest := 0
	for _, li := range []omsapi.PurchaseOrderItem{{IsVoided: true}, {IsFullyReceived: true}} {
		if w := lipgloss.Width(poLineFlag(li)); w > widest {
			widest = w
		}
	}
	return widest
}()

// poFitLineGrid drops the flag column first and the ship-date column second,
// because that is the order of how much a narrow pane loses by it: the flag is
// one word that reads fine as a reading, the ship date is a whole reading of its
// own, and the item name is the row's subject and never goes.
//
// It is handed the lines so that the fixed columns can be budgeted at what they
// will actually hold. A number cell is not a description: "1000…" is a WRONG
// quantity rather than a shortened one, so the column grows and the item column
// gives up the room.
func poFitLineGrid(bodyWidth int, items []omsapi.PurchaseOrderItem) poLineGridFit {
	const minItemW, maxItemW = 12, 44
	if bodyWidth <= 0 {
		bodyWidth = 76
	}
	clamp := func(w int) int {
		switch {
		case w < minItemW:
			return minItemW
		case w > maxItemW:
			return maxItemW
		}
		return w
	}
	widen := func(at *int, s string, ceiling int) {
		if w := lipgloss.Width(s); w > *at {
			if w > ceiling {
				w = ceiling
			}
			*at = w
		}
	}
	fit := poLineGridFit{
		numW:  poGridNumW,
		qtyW:  poGridQtyW,
		costW: poGridCostW,
		shipW: poGridShipW,
		flagW: poFlagBudget,
	}
	for i, li := range items {
		widen(&fit.numW, strconv.Itoa(i+1), poGridNumMaxW)
		widen(&fit.qtyW, strconv.Itoa(li.QuantityOrdered), poGridQtyMaxW)
		widen(&fit.costW, poLineCostCell(li), poGridCostMaxW)
		widen(&fit.shipW, firstNonEmpty(li.ExpectedShipmentDate, "—"), poGridShipMaxW)
	}

	full := fit.coreW() + poGridGutter + fit.shipW + poGridGutter + fit.flagW
	if w := bodyWidth - full; w >= minItemW {
		fit.itemW, fit.ship, fit.flag = clamp(w), true, true
		return fit
	}
	withShip := fit.coreW() + poGridGutter + fit.shipW
	if w := bodyWidth - withShip; w >= minItemW {
		fit.itemW, fit.ship = clamp(w), true
		return fit
	}
	fit.itemW = clamp(bodyWidth - fit.coreW())
	return fit
}

// addLineItems draws the lines as the JD Edwards detail grid po_edit.go picks
// from: the same cells in the same order, off the same poGrid* widths and
// through the same poLineGridRow, so a line reads the same on the screen that
// shows it as on the screen that edits it. What differs is that the DETAIL grid
// sheds cells it cannot fit (poFitLineGrid) instead of letting the pane cut
// them — a read-only sheet has readings to fall back on and an edit grid has a
// cursor that must not move under the operator.
//
// Everything the grid has no column for rides underneath it as wrapped
// readings (jdeWrapTokens), which is what lets the row itself stay inside a
// 51-column pane. The readings WRAP rather than being trimmed because the piece
// an ellipsis eats is the last one, and the last one is where "voided" sits.
func (s *PurchaseOrderDetailScreen) addLineItems(l *jdeLines, supplier string) {
	po := s.po
	if len(po.Items) == 0 {
		l.Add(StyleJDEHeading.Render("Line items (0)"))
		l.Add(jdeIndent + StyleMuted.Render("No line items on this PO."))
		l.Add("")
		return
	}
	l.Add(StyleJDEHeading.Render(fmt.Sprintf("Line items (%d)", len(po.Items))))
	fit := poFitLineGrid(s.bodyWidth(), po.Items)
	ship := ""
	if fit.ship {
		ship = "Ship date"
	}
	l.Add(StyleMuted.Render(poDetailGridRow(fit, "#", "Item", "Qty", "Cost", ship, "")))
	for i, li := range po.Items {
		for _, line := range poLineBlock(i+1, li, supplier, fit, s.bodyWidth()) {
			l.Add(line)
		}
	}
	l.Add("")
}

// poLineBlock renders one line: the grid row, then its readings, then the
// free-text rows that deserve a line of their own.
func poLineBlock(lineNum int, li omsapi.PurchaseOrderItem, poSupplier string, fit poLineGridFit, bodyWidth int) []string {
	ship, flag := "", ""
	if fit.ship {
		ship = firstNonEmpty(li.ExpectedShipmentDate, "—")
	}
	if fit.flag {
		flag = poLineFlag(li)
	}
	out := []string{poDetailGridRow(
		fit,
		strconv.Itoa(lineNum),
		li.DisplayLabel(),
		strconv.Itoa(li.QuantityOrdered),
		poLineCostCell(li),
		ship,
		flag,
	)}
	out = append(out, jdeWrapTokens(poLineTokens(li, poSupplier, fit), poLineGridItemIndent, bodyWidth)...)

	// "Ordered for" — the job and/or committee THIS line was bought for
	// (op-bu80 / op-shb9). A line's own association overrides nothing at the
	// order level; a mixed order is exactly why lines carry their own. Drawn
	// only when the line has one, so a plain restock line stays a plain row.
	for _, row := range []struct{ label, value string }{
		{"ordered for: ", poLineOrderedFor(li)},
		{"", poLineInventoryNote(li)},
		{"", poLineAssetNote(li)},
	} {
		if row.value == "" {
			continue
		}
		out = append(out, poLineContinuation(StyleMuted, row.label+row.value, bodyWidth))
	}
	if li.IsVoided && li.VoidReason != "" {
		out = append(out, poLineContinuation(StyleStatusWarn, "void reason: "+li.VoidReason, bodyWidth))
	}
	if li.Notes != "" {
		out = append(out, poLineContinuation(StyleMuted, li.Notes, bodyWidth))
	}
	return out
}

// poLineContinuation draws one free-text line under a grid row, trimmed
// VISIBLY to the pane. clampToBox would cut it with nothing to say it had.
func poLineContinuation(style lipgloss.Style, text string, bodyWidth int) string {
	if bodyWidth > 0 {
		text = fitCell(text, bodyWidth-len(poLineGridItemIndent))
	}
	return poLineGridItemIndent + style.Render(text)
}

// poLineCostCell is the grid's Cost column: the money actually spent when there
// is any, else the estimate. "—" and not "$0.00" for a line with neither —
// "unknown" is not "free".
func poLineCostCell(li omsapi.PurchaseOrderItem) string {
	switch {
	case !li.ActualCost.Empty():
		return "$" + string(li.ActualCost)
	case !li.EstimatedCost.Empty():
		return "$" + string(li.EstimatedCost)
	}
	return "—"
}

// poLinePartNumber is the identifier a buyer quotes at the supplier: the
// inventory SKU when the line has one, else the asset tag, else nothing. The
// SKU wins because a line that has both is a catalog line and the SKU is what
// the order was placed against.
func poLinePartNumber(li omsapi.PurchaseOrderItem) string {
	if sku, ok := li.ItemDetails["sku"].(string); ok && sku != "" {
		return sku
	}
	if tag, ok := li.AssetDetails["asset_tag"].(string); ok && tag != "" {
		return tag
	}
	return ""
}

// poLineTokens are the readings drawn under a line's grid row — everything the
// grid has no column for. Ordered by what an operator scanning the order is
// looking for: what to quote, what it cost per unit, how much has landed, then
// the exceptions.
func poLineTokens(li omsapi.PurchaseOrderItem, poSupplier string, fit poLineGridFit) []jdeToken {
	var out []jdeToken
	add := func(style lipgloss.Style, format string, args ...any) {
		out = append(out, jdeToken{text: fmt.Sprintf(format, args...), style: style})
	}
	// The flag leads when the grid could not keep its column: it is the one
	// reading that changes what the whole row MEANS, and jdeWrapTokens trims
	// only the last token on an over-long line.
	if !fit.flag {
		if flag := poLineFlag(li); flag != "" {
			style := StyleStatusOK
			if li.IsVoided {
				style = StyleStatusWarn
			}
			add(style, "%s", flag)
		}
	}
	if part := poLinePartNumber(li); part != "" {
		add(StyleMuted, "PART %s", part)
	}
	if !li.UnitCostOrdered.Empty() {
		add(StyleMuted, "UNIT $%s", string(li.UnitCostOrdered))
	}
	if li.QuantityReceived > 0 {
		add(StyleMuted, "received %d", li.QuantityReceived)
	}
	if li.QuantityPending > 0 && li.QuantityPending != li.QuantityOrdered {
		add(StyleStatusWarn, "%d pending", li.QuantityPending)
	}
	if li.ItemType != "" {
		add(StyleMuted, "type %s", poLineTypeLabel(li.ItemType))
	}
	// The override the grid's Cost column cannot show: a unit price that was
	// re-agreed after the order went out.
	if !li.UnitCostActual.Empty() && li.UnitCostActual != li.UnitCostOrdered {
		add(StyleMuted, "actual @ $%s", string(li.UnitCostActual))
	}
	if li.SupplierDetails != "" && li.SupplierDetails != poSupplier {
		add(StyleMuted, "%s", li.SupplierDetails)
	}
	return append(out, poShipTokens(li, fit)...)
}

// poLineFlag is the one word that says what state the whole line is in.
func poLineFlag(li omsapi.PurchaseOrderItem) string {
	switch {
	case li.IsVoided:
		return "[voided]"
	case li.IsFullyReceived:
		return "✓ received"
	}
	return ""
}

// poShipTokens carry what the grid's plain Ship-date column cannot: that the
// ship-by has passed or is nearly here, and that the goods actually shipped.
//
//   - expected_shipment_date is repeated, coloured, when it is overdue (red) or
//     within a week (yellow) — and plainly when the pane was too narrow to keep
//     the Ship date column at all. A ship-by comfortably in the future, on a
//     pane that HAS the column, is already legible there and a second plain copy
//     of it would be noise.
//   - actual_shipment_date is always green and always drawn: it is the "done"
//     signal, and it has no column of its own at any width.
func poShipTokens(li omsapi.PurchaseOrderItem, fit poLineGridFit) []jdeToken {
	var out []jdeToken
	switch urgency := shipByUrgency(li); {
	case urgency == "overdue":
		out = append(out, jdeToken{text: "SHIP BY " + li.ExpectedShipmentDate, style: StyleStatusError})
	case urgency == "soon":
		out = append(out, jdeToken{text: "SHIP BY " + li.ExpectedShipmentDate, style: StyleStatusWarn})
	case !fit.ship && li.ExpectedShipmentDate != "":
		out = append(out, jdeToken{text: "SHIP BY " + li.ExpectedShipmentDate, style: StyleMuted})
	}
	if li.ActualShipmentDate != "" {
		out = append(out, jdeToken{text: "SHIPPED " + li.ActualShipmentDate, style: StyleStatusOK})
	}
	return out
}

// poLineInventoryNote names the inventory item behind a catalog line when its
// name says something the line label does not.
func poLineInventoryNote(li omsapi.PurchaseOrderItem) string {
	sku, ok := li.ItemDetails["sku"].(string)
	if !ok || sku == "" {
		return ""
	}
	name, ok := li.ItemDetails["name"].(string)
	if !ok || name == "" || name == li.DisplayLabel() {
		return ""
	}
	return fmt.Sprintf("inv item: %s · SKU %s", name, sku)
}

// poLineAssetNote names the asset an asset line is buying, and where it lives.
func poLineAssetNote(li omsapi.PurchaseOrderItem) string {
	tag, ok := li.AssetDetails["asset_tag"].(string)
	if !ok || tag == "" {
		return ""
	}
	out := "asset: " + tag
	if loc, ok := li.AssetDetails["location_name"].(string); ok && loc != "" {
		out += " · " + loc
	}
	return out
}

// ---------------------------------------------------------------------------
// Attachments band
// ---------------------------------------------------------------------------

// addAttachments lists the files on the order. A is the key that manages them
// (po_attachments.go); this band is the read-only sight of them.
func (s *PurchaseOrderDetailScreen) addAttachments(l *jdeLines) {
	if len(s.po.Attachments) == 0 {
		return
	}
	l.Add(StyleJDEHeading.Render(fmt.Sprintf("Attachments (%d)", len(s.po.Attachments))))
	width := s.bodyWidth()
	for _, att := range s.po.Attachments {
		for _, line := range poAttachmentLines(att, width) {
			l.Add(line)
		}
	}
}

// poAttachmentIndent puts an attachment's readings under its name, the way a
// line's readings sit under its grid row.
const poAttachmentIndent = "     "

// poAttachmentLines renders one attachment: its name and description, then when
// and by whom it landed, then the URL. Every line is trimmed VISIBLY to the
// pane — a file URL is the longest string on this screen and clampToBox would
// cut it with nothing to say it had.
func poAttachmentLines(att omsapi.PurchaseOrderAttachment, bodyWidth int) []string {
	name := att.FileName
	if name == "" {
		name = att.File
	}
	prefix := jdeIndent + "· "
	out := []string{prefix + fitCellIf(name, bodyWidth-len(prefix))}

	// The description and the uploader ride UNDER the name as wrapped readings
	// rather than after it on the same row. Glued on, the description was the
	// tail an 80-column pane cut — and the description is the whole reason
	// anyone reads an attachment row twice.
	var tokens []jdeToken
	if att.Description != "" {
		tokens = append(tokens, jdeToken{text: att.Description, style: StyleMuted})
	}
	if !att.UploadedAt.IsZero() {
		tokens = append(tokens, jdeToken{text: att.UploadedAt.Format("2006-01-02"), style: StyleMuted})
	}
	if att.UploadedByName != "" {
		tokens = append(tokens, jdeToken{text: "by " + att.UploadedByName, style: StyleMuted})
	}
	out = append(out, jdeWrapTokens(tokens, poAttachmentIndent, bodyWidth)...)
	if att.FileURL != "" {
		// A URL is one unbreakable word: there is nowhere to wrap it, so it is
		// trimmed VISIBLY rather than being cut at the pane edge with nothing to
		// say it had been.
		out = append(out, poAttachmentIndent+StyleMuted.Render(
			fitCellIf(att.FileURL, bodyWidth-len(poAttachmentIndent))))
	}
	return out
}

// fitCellIf is fitCell for a width that may not be known yet: a width of zero
// or less means the pane has not been sized, which everywhere in the columnar
// layer reads as "do not truncate".
func fitCellIf(s string, w int) string {
	if w <= 0 {
		return s
	}
	return fitCell(s, w)
}

// ---------------------------------------------------------------------------
// Modals
// ---------------------------------------------------------------------------

// poFitInputValue bounds one input box to the width jdeFitRow gives its row and
// returns the value string to hang on that row. Every text row on the four
// sheets this slice converts goes through it.
//
// LOCAL WORKAROUND — bead scantty-jde-textinput-width. DELETE this helper and
// its call sites on the four sheets (po_detail.go's ship, void and deliver
// sheets and po_attachments.go's upload sheet) when the shared-layer fix lands.
// That bead removes BOTH halves of what this function does: the ti.Width
// bounding AND the TextStyle assignment below, which only exists to repair what
// the bounding costs.
//
// jdeFitRow sizes the jdeField's FILL, not the bubbles textinput that produced
// the value, and in bubbles v1.0.0 a box left at Width 0 has no scrolling
// viewport at all — handleOverflow returns early and View() draws the ENTIRE
// value. A 60-column path typed into the upload sheet at 80 columns therefore
// drew an ~80-column row into a 51-column pane: clampToBox cut it and the caret
// sat off the end of the screen, so the operator was typing blind. Giving the
// box a Width gives it back the viewport, and the row then stays inside the
// pane with the caret on it.
//
// It lives in the purchasing files rather than in jde_form.go deliberately. The
// shared layer is being rendered through concurrently by the inventory
// conversion, and shifting its behaviour under a live review would cost rework
// on screens already signed off; keeping the workaround here makes lifting it
// one obvious deletion.
//
// The Width is set on the STORED box and not on a render-time copy because
// bubbles COMPUTES the scrolling window in Update/SetCursor and only READS it in
// View: a Width set on a copy would leave the stale whole-value window in place
// and change nothing on screen. Re-seating the cursor after the change is what
// makes the box recompute that window when the pane resizes under a value that
// has already been typed.
func poFitInputValue(ti *textinput.Model, shape jdeField, labelWidth, bodyWidth int, focused bool) string {
	// bodyWidth of 0 is "the pane is not sized yet", which everywhere in this
	// layer means "do not truncate".
	fitted, _ := jdeFitRow(shape, labelWidth, bodyWidth)
	if bodyWidth <= 0 || fitted.Width <= 0 {
		return jdeInputValue(*ti, focused)
	}

	// The box is asked for ONE COLUMN LESS than the row has, because a focused
	// bubbles box draws its caret in a cell of its own PAST its Width — so a box
	// given the row's whole width renders width+1 and puts every focused row
	// exactly one column over the pane.
	//
	// Never below 1. A Width of 0 does not mean "one column", it turns the
	// scrolling viewport OFF altogether, and an unbounded box on a one-column
	// row puts the entire value back on screen — the defect this helper exists
	// to close, reappearing at the one pane width nobody tests.
	box := fitted.Width - 1
	if box < 1 {
		box = 1
	}
	if ti.Width != box {
		ti.Width = box
		pos := ti.Position()
		ti.CursorEnd()
		ti.SetCursor(pos)
	}

	// A bounded box pads its own value out to Width, which leaves jdeFieldArea
	// nothing to draw: `fill := width - lipgloss.Width(f.Value)` comes out 0 and
	// the reverse-video block that says WHERE THE CURSOR IS disappears. The
	// padding is emitted with the box's TextStyle, so handing it
	// StyleJDEFieldFocused puts the highlight back on the exact columns
	// jdeFieldArea used to own. It is read fresh each render because a theme
	// switch reassigns the style, and it is CLEARED on a blurred row: a box that
	// kept it would highlight a field the cursor is not standing on, inverting
	// the one signal this whole layer uses to say where the operator is.
	//
	// NOTE FOR WHOEVER READS THIS NEXT: no test can catch this class of bug
	// today. lipgloss renders flat in a test binary, so a lost highlight and a
	// present one produce byte-identical strings of identical width — the
	// regression this repairs shipped through a green suite. A passing suite is
	// NOT evidence that the focused field is drawn correctly; the same blind
	// spot covers every colour and every reverse-video cue on these screens.
	//
	// KNOWN AND DELIBERATE DIVERGENCE FROM THE PILOT: bubbles applies TextStyle
	// to the value runes as well as to the pad, so a focused row here draws the
	// typed value in reverse video too, where po_edit.go reverses only the fill.
	// That was weighed and accepted — a fully reversed field is a legitimate
	// green-screen look, and every alternative was a further workaround on the
	// same root cause. It is TEMPORARY: scantty-jde-textinput-width restores
	// exact pilot parity when it lands.
	if focused {
		ti.TextStyle = StyleJDEFieldFocused

		// The focused value is the box's RENDERED View() — it carries the
		// cursor's and TextStyle's escape sequences — and it is deliberately
		// NOT cut here. Cutting a rendered string by runes drops the trailing
		// SGR reset, or lands inside a sequence, and the terminal then draws
		// everything after this row in reverse video: a garbled terminal rather
		// than a merely wrong-looking row. What keeps this row inside the pane
		// is ti.Width above, imposed BEFORE bubbles renders anything, which is
		// the only place a bound on styled output belongs.
		return jdeInputValue(*ti, true)
	}
	ti.TextStyle = lipgloss.NewStyle()

	// A blurred box USUALLY hands back its raw value — jdeEchoValue reads it
	// straight out, so that a masked field cannot give up its mask — and that
	// value has had no viewport applied to it, so the width has to be imposed
	// here. fitCell and not truncateVisible because the cut has to ANNOUNCE
	// itself, the way poLineContinuation, poAttachmentLines and orderPadHeader
	// all do: an operator proof-reading a file path before pressing Enter must
	// not be shown a shortened path that reads like a whole one.
	//
	// USUALLY, not always. jdeInputValue also returns the RENDERED View() for an
	// empty box that has a Placeholder, focused or not. No field on these four
	// sheets carries a placeholder today, but the rule about not cutting styled
	// text has to survive someone adding one, so the branch checks for itself
	// rather than trusting that. A value carrying escapes is left alone: it came
	// from View() and is therefore already bounded by ti.Width.
	blurred := jdeInputValue(*ti, false)
	if strings.ContainsRune(blurred, '\x1b') {
		return blurred
	}
	return fitCell(blurred, fitted.Width)
}

// viewShip is the mark-shipped prompt: which line, and when it went.
func (s *PurchaseOrderDetailScreen) viewShip() string {
	fields := []jdeField{
		{
			Label: "Line #", Kind: jdeText,
			Width:   6,
			Hint:    fmt.Sprintf("1-%d", len(s.po.Items)),
			Focused: s.shipFocus == 0,
		},
		{
			Label: "Shipment date", Kind: jdeText,
			Width:   12,
			Hint:    "YYYY-MM-DD · blank is today, '-' clears",
			Focused: s.shipFocus == 1,
		},
	}
	labelW := jdeLabelWidth(fields)
	for i, box := range []*textinput.Model{&s.shipIdxIn, &s.shipDateIn} {
		fields[i].Value = poFitInputValue(box, fields[i], labelW, s.bodyWidth(), fields[i].Focused)
	}
	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Mark item shipped"))
	body.Add("")
	for _, line := range jdeCaveatLines("Records the day one line left the supplier.", s.bodyWidth()) {
		body.Add(line)
	}
	body.Add("")
	body.AddFittedFields(fields, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(nil, body, s.shipFocus,
		jdeStatusLine(s.shipPending, "Submitting…", s.shipErr),
		[]actionBarItem{{"Enter", "Mark shipped"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}})
}

// viewVoid is the void-the-whole-order prompt. The reason is optional here —
// VoidPurchaseOrder accepts an empty one — unlike a line void, which requires
// it; the caveat above the field is what says which of the two this is.
func (s *PurchaseOrderDetailScreen) viewVoid() string {
	field := jdeField{
		Label: "Reason", Kind: jdeText,
		Width:   34,
		Hint:    "optional",
		Focused: true,
	}
	labelW := jdeLabelWidth([]jdeField{field})
	field.Value = poFitInputValue(&s.voidReasonIn, field, labelW, s.bodyWidth(), true)
	body := &jdeLines{}
	body.Add(StyleStatusWarn.Render("Void purchase order"))
	body.Add("")
	for _, line := range jdeCaveatLines(
		"Voids the order and cascades to every line that is not already voided. This cannot be undone.",
		s.bodyWidth()) {
		body.Add(line)
	}
	body.Add("")
	body.AddFittedFields([]jdeField{field}, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(nil, body, 0,
		jdeStatusLine(s.voidPending, "Voiding…", s.voidErr),
		[]actionBarItem{{"Enter", "Void order"}, {"Esc", "Cancel"}})
}

var poDeliverLabels = map[int]string{
	poDeliverDate:     "Delivery date",
	poDeliverTracking: "Tracking #",
	poDeliverCarrier:  "Carrier",
	poDeliverNotes:    "Receipt notes",
}

var poDeliverHints = map[int]string{
	poDeliverDate:     "YYYY-MM-DD",
	poDeliverTracking: "optional",
	poDeliverCarrier:  "optional",
	poDeliverNotes:    "optional",
}

var poDeliverWidths = map[int]int{
	poDeliverDate:  12,
	poDeliverNotes: 34,
}

// viewDeliver is the mark-delivered modal — the PO's tracking-entry path as
// well as its receipt, since OMS carries tracking and carrier on this call.
func (s *PurchaseOrderDetailScreen) viewDeliver() string {
	fields := make([]jdeField, poDeliverFieldCount)
	for i := 0; i < poDeliverFieldCount; i++ {
		fields[i] = jdeField{
			Label:   poDeliverLabels[i],
			Kind:    jdeText,
			Width:   poDeliverWidths[i],
			Hint:    poDeliverHints[i],
			Focused: s.deliverFocus == i,
		}
	}
	labelW := jdeLabelWidth(fields)
	for i := range fields {
		fields[i].Value = poFitInputValue(&s.deliverInputs[i], fields[i], labelW, s.bodyWidth(), fields[i].Focused)
	}
	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Mark delivered"))
	body.Add("")
	for _, line := range jdeCaveatLines("Receives every pending quantity as of the delivery date.", s.bodyWidth()) {
		body.Add(line)
	}
	body.Add("")
	body.AddFittedFields(fields, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(nil, body, s.deliverFocus,
		jdeStatusLine(s.deliverPending, "Submitting…", s.deliverErr),
		[]actionBarItem{{"Enter", "Mark delivered"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}})
}

// ---------------------------------------------------------------------------
// Order-pad overlay
// ---------------------------------------------------------------------------

func (s *PurchaseOrderDetailScreen) orderPadBar() []actionBarItem {
	items := []actionBarItem{{"Enter", "Copy"}, {"Esc", "Close"}}
	if s.orderPadExport != nil && s.orderPadExport.Text != "" {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

// orderPadHeader is the chrome pinned above the pad: what was built, from how
// many lines, and which of them had no part number to build from.
func (s *PurchaseOrderDetailScreen) orderPadHeader() []string {
	head := []string{StyleJDEHeading.Render("Order pad")}
	if s.orderPadExport == nil {
		return append(head, "")
	}
	var meta []string
	if s.orderPadExport.Supplier != "" {
		meta = append(meta, s.orderPadExport.Supplier)
	}
	meta = append(meta, fmt.Sprintf("%d %s", s.orderPadExport.LineCount, plural("line", s.orderPadExport.LineCount)))
	if s.orderPadExport.Filename != "" {
		meta = append(meta, s.orderPadExport.Filename)
	}
	head = append(head, jdeIndent+StyleMuted.Render(fitCellIf(strings.Join(meta, " · "), s.bodyWidth()-len(jdeIndent))))
	if miss := len(s.orderPadExport.MissingSku); miss > 0 {
		// The count leads and the names follow: at 80 columns the names are what
		// the fold eats, and "3 lines have no part #" is the half that still
		// tells the operator to go look.
		warn := fmt.Sprintf("⚠ %d %s no supplier part # (omitted): %s",
			miss, plural("line", miss), strings.Join(s.orderPadExport.MissingSku, ", "))
		for _, line := range jdeWrapNote(warn, jdeStripWidth(s.bodyWidth(), 0)) {
			head = append(head, jdeIndent+StyleStatusWarn.Render(line))
		}
	}
	return append(head, "")
}

// viewOrderPad renders the overlay: the pinned chrome, the scrollable part#/qty
// pad, and the bar. The pad text is already on the terminal clipboard (copied
// when it loaded); Enter is the always-available second chance.
func (s *PurchaseOrderDetailScreen) viewOrderPad() string {
	if s.orderPadLoading {
		body := &jdeLines{}
		body.Add(jdeIndent + StyleMuted.Render("Building order pad…"))
		return s.frameWrapped([]string{StyleJDEHeading.Render("Order pad"), ""}, body, 0, "",
			[]actionBarItem{{"Esc", "Close"}})
	}
	if s.orderPadErr != "" {
		// The error lives on the status row above the bar and NOWHERE else, as
		// on every other converted phase: a caveat body here printed the same
		// sentence twice on one frame, once plain and once with the status
		// row's "✗ " in front of it.
		body := &jdeLines{}
		return s.frameWrapped([]string{StyleJDEHeading.Render("Order pad"), ""}, body, 0,
			jdeStatusLine(false, "", s.orderPadErr),
			[]actionBarItem{{"Esc", "Close"}})
	}

	frame, offset := s.frameScrolled(s.orderPadHeader(), s.orderPadLines(), s.padScroll, "", s.orderPadBar())
	s.padScroll = offset
	return frame
}

// ---------------------------------------------------------------------------
// Shared line helpers
// ---------------------------------------------------------------------------

// poLineTypeLabel renders a PO line's wire item_type token as a human label, so
// a line's target type is readable without knowing the backend's vocabulary.
// item_supplier is a catalog line (older payloads spell it inventory_item),
// asset is an asset purchase, freeform is a line with no reference; an empty or
// unrecognized token has nothing to say, hence "—".
//
// Reorder-queue picks are deliberately not a fourth case: the backend keeps no
// reorder_request FK on the line and a reorder pick resolves to an
// item_supplier, so such a line is genuinely an inventory line on the wire and
// labels as one.
func poLineTypeLabel(itemType string) string {
	switch itemType {
	case "item_supplier", "inventory_item":
		return "Inventory item"
	case "asset":
		return "Asset"
	case "freeform":
		return "Freeform"
	}
	return "—"
}

// poLineOrderedFor renders a line's work-order / committee associations as one
// "job · committee" string, or "" when the line carries neither. Shared by the
// detail body and the edit screen's line rows so a line reads the same in both.
func poLineOrderedFor(li omsapi.PurchaseOrderItem) string {
	parts := []string{}
	if label := li.WorkOrderRef.Label(); label != "" {
		parts = append(parts, label)
	}
	if name := poCommitteeRefLabel(li.OwningGroupRef); name != "" {
		parts = append(parts, name)
	}
	return strings.Join(parts, " · ")
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
