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
//	E A S n s c d v x r  the order-level commands, every one of them named on
//	                 the bar and status-gated exactly as the web page gates its
//	                 buttons. `n` opens the scan-a-SKU add-line flow
//	                 (po_add_line.go) and is named only on a draft, because that
//	                 is the only status the backend will take a new line in.
//
// Tab and Shift-Tab ride alongside Up/Down for field movement on the modal
// sheets (mark shipped, mark delivered, and the attachments upload sheet). The
// bar names the CANONICAL key of a pair rather than every alias of it — UP/DN,
// as the pilot po_edit.go does with the same two — so "a key the bar does not
// name does nothing" is a rule about COMMANDS, and Tab is a second spelling of
// one the bar already names rather than a key that escaped the audit. That pair
// is the only alias left in these files.
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
	return s.orderModal() != poNoModal || s.orderPad
}

// poOrderModal names the order-level sheet that owns the frame, if any.
type poOrderModal int

const (
	poNoModal poOrderModal = iota
	poShipModal
	poVoidModal
	poDeliverModal
)

// orderModal is the ONE place that decides which of the order-level sheets is
// open, read by View to pick the frame and by Update to pick the key handler so
// the frame on screen and the keys that act are always the same sheet's.
//
// All three of them read and submit against the loaded order — viewShip sizes
// its "1-N" hint off len(po.Items), and submitShip, handleVoidKey and
// submitDeliver all send po.ID — so none of them is open without one. That is
// not defensiveness: a reload can take the order away UNDER an open sheet
// (press r, press S while it is in flight, then let the reload fail — OMS being
// unreachable is the ordinary shop-floor case — and poDetailLoadedMsg stores
// the nil order), and the next frame then dereferenced it and killed the TUI
// with the terminal left in raw mode. The operator gets the not-found frame
// instead, which names the two keys that still work there.
func (s *PurchaseOrderDetailScreen) orderModal() poOrderModal {
	if s.po == nil {
		return poNoModal
	}
	switch {
	case s.shipping:
		return poShipModal
	case s.voiding:
		return poVoidModal
	case s.delivering:
		return poDeliverModal
	}
	return poNoModal
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
		switch s.orderModal() {
		case poShipModal:
			return s.handleShipKey(m)
		case poVoidModal:
			return s.handleVoidKey(m)
		case poDeliverModal:
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
	case "up", "down", "pgup", "pgdown", "home", "end":
		// Gated on the bar's own predicate AND on the frame being drawn at all.
		// The two are different questions: sheetScrolls asks whether the body
		// has more than fits, which stays true on a pane the layer refuses, and
		// scrolling a body nobody can see loses the operator's place silently.
		if !s.sheetMoves() {
			return s, nil
		}
		// The frame clamps whatever it is handed, so "past the end" is how the
		// end is asked for — see jdeScrollStep, which is the one mapping every
		// scrolled body on the columnar layer goes through.
		s.scroll = jdeScrollStep(m.String(), s.scroll, s.sheetBody().Len(), s.pageStep())
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
	case "n":
		// Add a line by typing or SCANNING what is on the box (po_add_line.go).
		// Gated on draft exactly as the backend gates it (assert_addable), and
		// the bar names `n` only while that holds — but a press in the wrong
		// status still answers, the way s / c / d / v do, rather than redrawing
		// the same pane. Lowercase n is free in the global keymap: since phase 3
		// the root holds no letter at all.
		if s.po == nil || s.po.Status != "draft" {
			return s, Status("add line is only available on draft POs", StatusWarn)
		}
		return s, SwitchTo(WSPurchasing, NewPurchaseOrderAddLineScreen(s.deps, s.po))
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

// pageStep is one paneful of body, computed from the bar the pane is currently
// drawing — a wrapped bar leaves fewer rows, and a page that moved by more than
// the operator can see would skip content.
func (s *PurchaseOrderDetailScreen) pageStep() int {
	return s.scrollRows(0, s.sheetBar())
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
		// A field form's cursor WRAPS, and it declines outright on a pane the
		// modal is not drawn into: moving the caret between two boxes nobody
		// can see leaves the operator typing into the other one when the
		// terminal grows back.
		//
		// The delta is COMPUTED rather than fixed at +1, even though this modal
		// has exactly two rows and +1 ≡ -1 there: a hard-coded forward step is
		// right by an accident of the field count, not by anything about the
		// arm, so adding a third row — a note, a carrier — would silently send
		// Shift-Tab and Up the wrong way. No sweep would report it either: they
		// press keys and compare a position, which is a fact about where the
		// cursor is and not about which way it went.
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		next, ok := s.moveRow(s.shipFocus, poShipFieldCount, delta, 0, poShipBar)
		if !ok {
			return s, nil
		}
		s.shipFocus = next
		if s.shipFocus == 0 {
			s.shipDateIn.Blur()
			s.shipIdxIn.Focus()
		} else {
			s.shipIdxIn.Blur()
			s.shipDateIn.Focus()
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
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		next, ok := s.moveRow(s.deliverFocus, poDeliverFieldCount, delta, 0, poDeliverBar)
		if !ok {
			return s, nil
		}
		s.deliverInputs[s.deliverFocus].Blur()
		s.deliverFocus = next
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
		if s.padHasText() {
			return s, tea.Batch(
				copyToClipboardCmd(s.orderPadExport.Text),
				Status("order pad copied to clipboard", StatusOK),
			)
		}
		return s, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.padMoves() {
			return s, nil
		}
		s.padScroll = jdeScrollStep(m.String(), s.padScroll,
			s.orderPadLines().Len(), s.padPageStep())
	}
	return s, nil
}

func (s *PurchaseOrderDetailScreen) padPageStep() int {
	return s.scrollRows(len(s.orderPadHeader()), s.orderPadBar())
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
	if !s.padHasText() {
		// Through jdeCaveatLines and not hand-rolled: the sentence is 56 columns
		// against the 51 an 80-column pane gives, so written straight it was cut
		// by clampToBox — and because that cut drops runes off the END of a
		// STYLED string it took StyleMuted's closing SGR reset with them and
		// left the terminal dimmed for everything drawn after. A one-line styled
		// string that only clampToBox ever bounds is unsafe by construction.
		for _, line := range jdeCaveatLines("No lines have a supplier part number — nothing to order.", s.bodyWidth()) {
			l.Add(line)
		}
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
	switch s.orderModal() {
	case poShipModal:
		return s.viewShip()
	case poVoidModal:
		return s.viewVoid()
	case poDeliverModal:
		return s.viewDeliver()
	}
	if s.orderPad {
		return s.viewOrderPad()
	}
	return s.viewSheet()
}

// viewSheet draws the order itself: the columnar body, windowed at the
// operator's scroll offset, over the persistent bar.
//
// The clamped offset is stored back only when the ORDER is what was framed. The
// note frames are one line, so clamping against one of them returns 0, and
// writing that back threw the operator's reading position away every time the
// sheet passed through a transient state: scroll down into the line items,
// press r — or send / confirm / void / mark delivered, all of which reload —
// and the sheet came back at the top. The offset belongs to the order's body
// and is only ever re-measured against it.
func (s *PurchaseOrderDetailScreen) viewSheet() string {
	note := s.sheetNote()
	body := note
	if body == nil {
		body = s.sheetLines()
	}
	status := s.statusRow(s.loading || s.transitioning, "Working…", "")
	frame, offset := s.frameScrolled(nil, body, s.scroll, status, s.sheetBar())
	if note == nil {
		s.scroll = offset
	}
	return frame
}

// sheetBody is what the sheet draws in its CURRENT state — the order itself
// once it has loaded, and before that the one-line loading, load-error or
// not-found note.
//
// Those three used to return early out of View() as bare styled strings with no
// jdeLines, no status row and no bar, while handleSheetKey stayed fully live
// behind them: press r on a loaded sheet and then E while the reload is in
// flight, and you left for the edit screen from a frame that named no keys at
// all. They are frames of this screen like any other, so they are drawn like
// any other and the bar names what works on them.
//
// Every frame reads its body from here, including sheetScrolls, so "does this
// body move?" is asked about the body actually on screen.
func (s *PurchaseOrderDetailScreen) sheetBody() *jdeLines {
	if note := s.sheetNote(); note != nil {
		return note
	}
	return s.sheetLines()
}

// sheetNote is the one-line stand-in body for a sheet that has no order to
// draw — loading, load error, not found — and nil once the order is there. It
// is separate from sheetBody so viewSheet can tell a stand-in from the order
// itself and leave the scroll offset alone while one is on screen.
func (s *PurchaseOrderDetailScreen) sheetNote() *jdeLines {
	note := func(line string) *jdeLines {
		l := &jdeLines{}
		l.Add(line)
		return l
	}
	switch {
	case s.loading:
		return note(jdeIndent + StyleMuted.Render("Loading purchase order…"))
	case s.loadErr != "":
		return note(jdeIndent + StyleStatusError.Render("Error: ") +
			fitCellIf(s.loadErr, s.bodyWidth()-len(jdeIndent)-poErrPrefixW))
	case s.po == nil:
		return note(jdeIndent + StyleMuted.Render("Purchase order not found."))
	}
	return nil
}

// sheetBar names every key that works on the sheet, and only those. The three
// state transitions are gated exactly as the web page gates its buttons, so the
// bar never offers a key the handler is about to refuse.
//
// It runs long — a purchase order carries more commands than a form does — and
// renderActionBarWrapped folds it onto as many rows as it needs rather than
// letting the tail fall off a narrow pane.
func (s *PurchaseOrderDetailScreen) sheetBar() []actionBarItem {
	return s.sheetBarItems(s.sheetScrolls())
}

// sheetScrolls is the sheet's half of the bar-honesty rule: the scroll keys are
// named when, and only when, the body actually moves. A freshly created draft
// order with no lines, dates or terms builds a body well inside the pane, and
// naming UP/DN, PgUp/PgDn and Home/End there spent 42 of the bar's 49 columns on
// keys that do nothing — enough to push the bar onto a second row and take a
// body row with it.
func (s *PurchaseOrderDetailScreen) sheetScrolls() bool {
	return s.bodyScrollsForBar(s.sheetBody(), 0, s.sheetBarItems(true))
}

// sheetMoves is the HANDLER's half where sheetScrolls is the BAR's: the body
// must move AND the frame must be on the pane.
//
// The bar goes on naming the scroll keys at a refused height on purpose — it is
// not drawn there, and its only remaining job is to be measured, so shrinking
// it would make the refusal notice name a height that does not work
// (jdeTooShortRows). The frame it is measured against is the one the sheet
// really draws, which is why this asks sheetBar rather than sheetBarItems(true).
func (s *PurchaseOrderDetailScreen) sheetMoves() bool {
	return s.frameDrawn(0, s.sheetBar()) && s.sheetScrolls()
}

// sheetBarItems builds the bar for a given scroll state. sheetBar and
// sheetScrolls both go through it so the bar that is MEASURED is the bar that is
// drawn.
func (s *PurchaseOrderDetailScreen) sheetBarItems(scroll bool) []actionBarItem {
	// Enter, E, A and x every one guard on a loaded order and do nothing
	// without one, so on the loading and not-found frames the bar names only Esc
	// and r — which are the only two keys that work there.
	items := []actionBarItem{}
	if s.po != nil {
		items = append(items, actionBarItem{"Enter", "Receive"})
	}
	items = append(items, actionBarItem{"Esc", "Back"})
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	if s.po != nil {
		items = append(items,
			actionBarItem{"E", "Edit"},
			actionBarItem{"A", "Files"})
	}
	if s.po != nil {
		switch s.po.Status {
		case "draft":
			// Adding a line is a DRAFT-only action server-side, so the key that
			// opens the flow is named only where it works.
			items = append(items, actionBarItem{"n", "Add line"})
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
	if s.po != nil {
		items = append(items, actionBarItem{"x", "Order pad"})
	}
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
		for _, line := range jdeWrapNote(po.Notes, poMarginWidth(s.bodyWidth())) {
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

// poErrPrefixW is the width of the "Error: " a load failure is drawn behind on
// the two screens that render one as a body line rather than on the status row.
// Those lines are single Add()s, so like the status row they cannot fold and
// carry the same dropped-SGR-reset hazard if clampToBox is left to cut them.
const poErrPrefixW = 7

// poMinFoldWidth is the narrowest line jdeWrapNote can fold onto without losing
// content invisibly: at two columns it can still spend one on the ellipsis that
// marks an over-long word, and at one it cannot, so it drops the word instead.
const poMinFoldWidth = 2

// poMarginWidth is how wide a line drawn behind jdeIndent ALONE may be — the
// budget jdeCaveatLines wraps to, for the callers on this sheet that carry
// their own style and so cannot go through it. jdeStripWidth is the wrong
// helper for them: it also subtracts the leader column, which a line hanging
// off the margin rather than off a label does not have, and folding to it threw
// away seven columns of pane on every wrapped note.
func poMarginWidth(bodyWidth int) int {
	if bodyWidth <= 0 {
		return 0
	}
	if w := bodyWidth - len(jdeIndent); w > 0 {
		return w
	}
	return 1
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
		l.Add(renderJDEField(f, labelWidth, s.bodyWidth()))
		return
	}
	// Whether the fold is USABLE is decided from the width, before asking for
	// it. jdeWrapNote is lossless-or-visible only from poMinFoldWidth up: there
	// it ellipsises a word too wide for the line, so the cut announces itself.
	// Below that it has no room for even the ellipsis and its narrow-width arm
	// DROPS such a word silently, keeping only whatever single-column tokens the
	// value happens to contain — "Acme Fasteners & Industrial Supply Co." comes
	// back as "&", and the sheet then draws a value that is not the value with
	// nothing on screen to say so. Which is worse than the empty return that
	// used to panic here, because it looks like it worked.
	var wrapped []string
	if avail >= poMinFoldWidth {
		wrapped = jdeWrapNote(f.Value, avail)
	}
	if len(wrapped) == 0 {
		// No usable fold — too narrow for one, or nothing to fold. The row still
		// draws its label and as much of the value as fits, visibly trimmed: a
		// reading that vanished, or one silently reduced to a fragment, is the
		// same bug told quietly.
		head := f
		head.Value = fitCell(f.Value, avail)
		l.Add(renderJDEField(head, labelWidth, s.bodyWidth()))
		return
	}
	head := f
	head.Value = wrapped[0]
	l.Add(renderJDEField(head, labelWidth, s.bodyWidth()))
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
// too narrow for a measured column, so every cell reaches it already padded.
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

// The item column's floor and its ceiling, shared by BOTH forms of this grid —
// the block form below and the one-line form (poBuildOneLineGrid). A narrow
// pane shortens the description rather than collapsing the column; a wide one
// does not strand the figures out at the far right of an otherwise empty row.
//
// They are package constants rather than two locals because the one-line form
// has to shorten a description on exactly the same terms the block form does:
// a name that reads one way on a 100-column pane and another way on a 140-column
// one would be the two forms disagreeing about the same line, which is the thing
// this grid is built not to do. The edit screen's grid is fitted by the same
// function (po_edit.go's lineGrid), so it shortens on the same terms too.
const (
	poGridItemMinW = 12
	poGridItemMaxW = 44
)

// coreW is number + quantity + cost and the gutters between them, at the widths
// this order's own values need.
func (fit poLineGridFit) coreW() int {
	return len(jdeIndent) + fit.numW + poGridGutter + poGridGutter + fit.qtyW + poGridGutter + fit.costW
}

// poFlagBudget is how wide the flag cell has to be RESERVED: the widest string
// poLineFlag can actually return, in display columns.
//
// It is measured off poLineFlag rather than taken from a constant because the
// constant this used to share with the edit grid was 8 — exactly "[voided]" —
// while a received line's flag is "✓ received", which is 10, so budgeting 8 let
// the full-grid row measure two columns wider than the pane and clampToBox ate
// the tail: "✓ receiv". That is the same silent truncation this grid sheds cells
// to avoid, and the tests missed it only because the fixture's received line was
// ALSO voided, which takes the 8-wide branch. (The edit grid, which flags only a
// void, is fitted by this same budget now and so reserves the two cells too.)
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
	if bodyWidth <= 0 {
		bodyWidth = 76
	}
	clamp := func(w int) int {
		switch {
		case w < poGridItemMinW:
			return poGridItemMinW
		case w > poGridItemMaxW:
			return poGridItemMaxW
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
	if w := bodyWidth - full; w >= poGridItemMinW {
		fit.itemW, fit.ship, fit.flag = clamp(w), true, true
		return fit
	}
	withShip := fit.coreW() + poGridGutter + fit.shipW
	if w := bodyWidth - withShip; w >= poGridItemMinW {
		fit.itemW, fit.ship = clamp(w), true
		return fit
	}
	fit.itemW = clamp(bodyWidth - fit.coreW())
	return fit
}

// ---------------------------------------------------------------------------
// The one-line form of the line grid
// ---------------------------------------------------------------------------

// A purchase order line normally reads as a BLOCK: the grid row, then the
// readings the grid has no column for, then the free-text rows that deserve one
// of their own (poLineBlock). At 80 columns — the width this interface is
// modelled on — that is the only way it fits, and it is not changing.
//
// Give the pane enough cells and the whole block goes on ONE row, with three
// facts the block form never showed at all: the SUPPLIER's part number, the
// part's full internal UUID, and what the supplier is charging for the line.
// That is what poBuildOneLineGrid decides and builds.
//
// THE THRESHOLD IS THE CONTENT'S, NOT A NUMBER. Nothing here compares the pane
// against a width somebody chose; the rows are ASSEMBLED, measured in display
// cells, and the widest one is the threshold. So a spare order of short-named
// parts collapses on a pane a busier one will not, and a field added to the row
// later moves the threshold by exactly what it costs — where a constant would
// have gone on claiming a fit the row had stopped having.
//
// IT ANSWERS FOR THE WHOLE ORDER, NOT FOR THE ROW BEING DRAWN. The maximum over
// every line is what decides, so the lines collapse together or not at all —
// which makes the threshold single BY CONSTRUCTION rather than by every line
// happening to earn the same one. Asked per line, an order would draw line 1 as
// a row and line 2 as a five-row block, in a COLUMNAR grid whose columns are
// budgeted from the set: the two would not even line up. (voidCaveatsFit takes
// the same maximum, for the same reason.)
//
// AND IT REFUSES RATHER THAN MUTILATES. There is no shortened form: a row that
// will not fit is not drawn narrower, it is not drawn at all, and the block form
// stands. That is what lets this be a pure ADDITION — at every width where the
// one-line form is withheld, including 80, the pane is byte for byte what it was
// before this existed, and at every width where it is drawn nothing the block
// form said has been dropped. The two facts a wide pane gains are the SKU and
// the UUID; the third, the supplier's line cost, is the Cost column relabelled
// to what it is now carrying, with the money actually spent moved to a reading
// beside it so that figure survives too.

// The one-line grid's column headings, in order. They are measured into the
// column budget alongside the values, so a heading is never the thing an
// ellipsis eats — a column budgeted at what its VALUES need would draw
// "Supplier cos…" over a perfectly ordinary price.
const (
	poOneLineNumHead  = "#"
	poOneLineItemHead = "Item"
	poOneLineQtyHead  = "Qty"
	poOneLineSKUHead  = "Supplier SKU"
	poOneLineUUIDHead = "Part UUID"
	poOneLineCostHead = "Supplier cost"
	poOneLineShipHead = "Ship date"
)

// poOneLineFit is the one-line grid's column budget. Every width in it is
// MEASURED off the order's own values and headings; none is a constant, which
// is why there is no minimum and no ceiling anywhere in it. A column too wide
// for the pane does not shorten a value — it makes the whole row not fit, and
// the grid falls back to the block form, where that value has always had room.
//
// The UUID column is the reason that stance is not merely tidy. A part's id is
// 36 cells and the captain's instruction is that it is never abbreviated, so a
// ceiling on it would be a promise to cut the one value that must not be cut.
// The supplier SKU is the same shape from the other end: OMS allows 100
// characters, and an order carrying one of those simply does not collapse.
type poOneLineFit struct {
	numW, itemW, qtyW, skuW, uuidW, costW, shipW, flagW int
}

// row lays one row out in those columns. Numbers right-align under their
// headings the way poLineGridRow does, and the flag rides at the end unpadded
// so a row without one ends at its ship date.
//
// Every cell still goes through fitCell even though the budget was measured
// from these very values: a column that has been under-derived then shows an
// ELLIPSIS rather than shunting every column to its right out of alignment, and
// an ellipsis is a defect a reader can see.
func (f poOneLineFit) row(num, item, qty, sku, uuid, cost, ship, flag string) string {
	cells := []string{
		padCell(fitCell(num, f.numW), f.numW, alignRight),
		padCell(fitCell(item, f.itemW), f.itemW, alignLeft),
		padCell(fitCell(qty, f.qtyW), f.qtyW, alignRight),
		padCell(fitCell(sku, f.skuW), f.skuW, alignLeft),
		padCell(fitCell(uuid, f.uuidW), f.uuidW, alignLeft),
		padCell(fitCell(cost, f.costW), f.costW, alignRight),
		padCell(fitCell(ship, f.shipW), f.shipW, alignLeft),
		flag,
	}
	return jdeIndent + strings.TrimRight(strings.Join(cells, "  "), " ")
}

// poOneLineGrid is a built one-line grid: the heading row and one row per line,
// styled and ready to add. It is returned already built rather than as a
// decision to build later, because the DECISION is the measurement of the built
// rows — handing back a "yes" and assembling them again afterwards would be two
// implementations of one bound, which is how a bound and its predictor drift.
type poOneLineGrid struct {
	header string
	rows   []string
}

// poOneLineNone is what a column draws for a line that has nothing for it to
// show, and poOneLineUnknown for a line that HAS the thing the column is about
// where the server did not say what it is.
//
// THEY ARE DIFFERENT ANSWERS AND THE COLUMN MAY NOT MERGE THEM. "This line
// names no part" is a fact about the order — a freeform line for shop rags has
// no vendor part number and never will — while "we were not told which part
// this line names" is a fact about the reply, and the operator does different
// things about them: the first is the order as written, the second is a lookup
// to go and do. An em dash covering both would report a payload that arrived
// without the block as an order somebody typed by hand.
//
// The state is reachable rather than theoretical: item_supplier_details is a
// nested block on PurchaseOrderItemSerializer, so an OMS predating it answers
// every line with the relationship id and no block, and a column reading only
// the block would then draw "no SKU" down a whole order of catalogue lines.
const (
	poOneLineNone    = "—"
	poOneLineUnknown = "(not sent)"
)

// poLineNamesAPart reports whether this line points at something that HAS an
// internal id — a catalogue line's item-supplier relationship or an asset
// line's asset. A freeform line points at neither, which is why its empty
// columns are an absence and not a silence.
func poLineNamesAPart(li omsapi.PurchaseOrderItem) bool {
	return li.ItemSupplier != nil || li.Asset != nil
}

// poLineSupplierSKUCell is the one-line grid's Supplier SKU column, told apart
// three ways: the SKU, "no supplier part number on this line" (which covers
// both a line with no relationship at all and a relationship whose SKU is
// blank — OMS's own export_order treats those the same, reporting each in
// missing_sku), and "the reply did not carry the block".
func poLineSupplierSKUCell(li omsapi.PurchaseOrderItem) string {
	if li.ItemSupplierDetails != nil {
		if sku := poLineSupplierSKU(li); sku != "" {
			return sku
		}
		return poOneLineNone
	}
	if li.ItemSupplier != nil {
		return poOneLineUnknown
	}
	return poOneLineNone
}

// poLinePartUUIDCell is the Part UUID column, told apart the same three ways.
func poLinePartUUIDCell(li omsapi.PurchaseOrderItem) string {
	if id := poLinePartUUID(li); id != "" {
		return id
	}
	if poLineNamesAPart(li) {
		return poOneLineUnknown
	}
	return poOneLineNone
}

// poLineSupplierSKU is the part number the SUPPLIER knows this line by: the
// string a buyer quotes when placing the order.
//
// It is NOT poLinePartNumber, which is the makerspace's own internal SKU, and
// it is not item_details.supplier_sku either — that key is a flat accessor for
// the item's PRIMARY supplier, which is the wrong vendor's part number on any
// order placed with anyone else. OpenMakerSuite settles which is which on its
// own order-pad export: every row of it is built from
// po_item.item_supplier.supplier_sku (backend/reorder_queue/views.py,
// export_order), and a blank one is reported in missing_sku rather than
// dropped.
//
// "" for an asset line and a freeform one, neither of which names an
// ItemSupplier at all.
func poLineSupplierSKU(li omsapi.PurchaseOrderItem) string {
	if li.ItemSupplierDetails == nil {
		return ""
	}
	return strings.TrimSpace(li.ItemSupplierDetails.SupplierSKU)
}

// poLinePartUUID is the internal id of the thing this line buys — an
// InventoryItem's, else an Asset's. Both are UUIDField primary keys in OMS
// (inventory/models/core.py, inventory/models/asset.py), so both arrive as
// strings and neither can be mangled by a number's decoding.
//
// It falls through item then asset for the reason poLinePartNumber does: a line
// that has both is a catalogue line and the item is what it was placed against.
// "" for a freeform line, which names neither.
func poLinePartUUID(li omsapi.PurchaseOrderItem) string {
	for _, details := range []map[string]any{li.ItemDetails, li.AssetDetails} {
		if id, ok := details["id"].(string); ok {
			if id = strings.TrimSpace(id); id != "" {
				return id
			}
		}
	}
	return ""
}

// poLineSupplierCost is what the SUPPLIER is charging for this line: OMS's
// estimated_cost, which is quantity_ORDERED × unit_cost_ordered
// (reorder_queue/models.py). It is the line's own total and not a per-unit or
// per-case figure, and it is the price the order was PLACED at rather than the
// money that has since been spent — actual_cost is quantity_RECEIVED ×
// unit_cost_actual, which is null on every line of an order nobody has received
// against yet, and that is most of the lines an operator is ever looking at.
//
// "—" and not "$0.00" when the field is absent, exactly as poLineCostCell has
// it: unknown is not free. A served 0.00 IS a price and prints as one — OMS
// returns a real zero for a line with nothing on the supplier relationship — so
// this asks Empty(), never whether the value is zero.
func poLineSupplierCost(li omsapi.PurchaseOrderItem) string {
	if li.EstimatedCost.Empty() {
		return poOneLineNone
	}
	return "$" + string(li.EstimatedCost)
}

// poOneLineTokens is everything about a line that the one-line grid has no
// COLUMN for: the block form's readings and its free-text rows, as one flat
// list of tokens to ride the tail of the row.
//
// It is poLineBlock's own content, in poLineBlock's own order, and that is the
// point — the one-line form owes the operator every fact the block form draws,
// so this is derived from that function rather than chosen afresh. What it adds
// is the money actually SPENT: the Cost column now carries the supplier's price
// for the line, so actual_cost would otherwise be the one thing a collapse lost.
func poOneLineTokens(li omsapi.PurchaseOrderItem, poSupplier string, fit poLineGridFit) []jdeToken {
	out := poLineTokens(li, poSupplier, fit)
	if !li.ActualCost.Empty() {
		out = append(out, jdeToken{text: "actual $" + string(li.ActualCost), style: StyleMuted})
	}
	for _, row := range []struct {
		text  string
		style lipgloss.Style
	}{
		{poLineOrderedForToken(li), StyleMuted},
		{poLineInventoryNote(li), StyleMuted},
		{poLineAssetNote(li), StyleMuted},
		{poLineVoidReasonToken(li), StyleStatusWarn},
		{li.Notes, StyleMuted},
	} {
		if row.text != "" {
			out = append(out, jdeToken{text: row.text, style: row.style})
		}
	}
	return out
}

// poLineOrderedForToken and poLineVoidReasonToken are the two block-form rows
// whose text is a LEAD plus a value, spelled once so the row form and the block
// form cannot word them differently.
func poLineOrderedForToken(li omsapi.PurchaseOrderItem) string {
	if v := poLineOrderedFor(li); v != "" {
		return "ordered for: " + v
	}
	return ""
}

func poLineVoidReasonToken(li omsapi.PurchaseOrderItem) string {
	if li.IsVoided && li.VoidReason != "" {
		return "void reason: " + li.VoidReason
	}
	return ""
}

// poBuildOneLineGrid builds the one-line grid for an order and reports whether
// this pane is wide enough to draw it.
//
// false means the block form stands, and it is the answer in four cases, each
// of which is a fact about the CONTENT rather than a refusal to try:
//
//   - the pane is unsized (bodyWidth <= 0), so there is no width to be
//     sufficient. Guessing one and collapsing on it would settle a layout
//     against a terminal nobody has measured;
//   - the order carries no lines, so there is nothing to collapse;
//   - a KIT line is on it. A kit's credit block is prose naming several OTHER
//     items and how many of each one kit puts on the shelf — poKitCreditBlock
//     wraps it precisely because an ellipsis eats the trailing components — so
//     there is no row for it to ride and nowhere for it to go. One kit line and
//     the whole order keeps the block form, because the grid answers for the
//     order;
//   - the widest assembled row is wider than the pane.
//
// The rows are built ONCE. Their widths are measured on the PLAIN text, in
// display cells, as they are assembled — a single forward pass with no value
// measured twice and nothing re-fitted afterwards.
func poBuildOneLineGrid(bodyWidth int, items []omsapi.PurchaseOrderItem, supplier string) (poOneLineGrid, bool) {
	if bodyWidth <= 0 || len(items) == 0 {
		return poOneLineGrid{}, false
	}
	for _, li := range items {
		if li.IsKitLine {
			return poOneLineGrid{}, false
		}
	}

	// The block form's fit, with both optional columns present: the one-line
	// row has room for the flag and the ship date by construction, and
	// poLineTokens reads those two flags to decide which readings the columns
	// have already made redundant.
	tokenFit := poLineGridFit{ship: true, flag: true}

	fit := poOneLineFit{flagW: poFlagBudget}
	widen := func(at *int, s string) {
		if w := lipgloss.Width(s); w > *at {
			*at = w
		}
	}
	widen(&fit.numW, poOneLineNumHead)
	widen(&fit.itemW, poOneLineItemHead)
	widen(&fit.qtyW, poOneLineQtyHead)
	widen(&fit.skuW, poOneLineSKUHead)
	widen(&fit.uuidW, poOneLineUUIDHead)
	widen(&fit.costW, poOneLineCostHead)
	widen(&fit.shipW, poOneLineShipHead)

	type builtRow struct {
		num, item, qty, sku, uuid, cost, ship, flag string
		tokens                                      []jdeToken
	}
	built := make([]builtRow, 0, len(items))
	for i, li := range items {
		item := li.DisplayLabel()
		r := builtRow{
			num:  strconv.Itoa(i + 1),
			item: item,
			qty:  strconv.Itoa(li.QuantityOrdered),
			sku:  poLineSupplierSKUCell(li),
			uuid: poLinePartUUIDCell(li),
			cost: poLineSupplierCost(li),
			ship: firstNonEmpty(li.ExpectedShipmentDate, "—"),
			flag: poLineFlag(li),
		}
		r.tokens = poOneLineTokens(li, supplier, tokenFit)
		built = append(built, r)

		widen(&fit.numW, r.num)
		widen(&fit.qtyW, r.qty)
		widen(&fit.skuW, r.sku)
		widen(&fit.uuidW, r.uuid)
		widen(&fit.costW, r.cost)
		widen(&fit.shipW, r.ship)
		// The item column is the ONE that is clamped, and it is clamped to the
		// bounds the block form already uses — so a description reads the same
		// on both forms and neither can shorten one the other keeps whole.
		if w := lipgloss.Width(item); w > fit.itemW {
			if w > poGridItemMaxW {
				w = poGridItemMaxW
			}
			fit.itemW = w
		}
	}
	if fit.itemW < poGridItemMinW {
		fit.itemW = poGridItemMinW
	}

	grid := poOneLineGrid{
		header: fit.row(poOneLineNumHead, poOneLineItemHead, poOneLineQtyHead,
			poOneLineSKUHead, poOneLineUUIDHead, poOneLineCostHead, poOneLineShipHead, ""),
	}
	if lipgloss.Width(grid.header) > bodyWidth {
		return poOneLineGrid{}, false
	}
	sepW := lipgloss.Width(jdeTokenSep)
	for _, r := range built {
		row := fit.row(r.num, r.item, r.qty, r.sku, r.uuid, r.cost, r.ship, r.flag)
		width := lipgloss.Width(row)
		for _, tok := range r.tokens {
			width += sepW + lipgloss.Width(tok.text)
			if width > bodyWidth {
				return poOneLineGrid{}, false
			}
			row += StyleMuted.Render(jdeTokenSep) + tok.render()
		}
		if width > bodyWidth {
			return poOneLineGrid{}, false
		}
		grid.rows = append(grid.rows, row)
	}
	return grid, true
}

// addLineItems draws the lines as the JD Edwards detail grid po_edit.go picks
// from: the same cells in the same order, off the same poGrid* widths and
// through the same poLineGridRow, so a line reads the same on the screen that
// shows it as on the screen that edits it. Both shed the cells they cannot fit
// (poFitLineGrid) rather than letting the pane cut them; what differs is where a
// dropped FLAG goes — onto the readings here, onto the row's own item cell on
// the edit grid, whose window is anchored on a cursor (po_edit.go's lineGrid
// says why).
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
	// Wide enough and the whole of every block goes on its own row, carrying
	// three facts no width of the block form shows. Narrower — 80 columns
	// included — this is not reached and nothing below it changed.
	if grid, ok := poBuildOneLineGrid(s.bodyWidth(), po.Items, supplier); ok {
		l.Add(StyleMuted.Render(grid.header))
		for _, row := range grid.rows {
			l.Add(row)
		}
		l.Add("")
		return
	}
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
	// A kit line is not what its label says it is: it buys one SKU and credits
	// several other items on receipt, so it is marked where the name is read —
	// AHEAD of the name, inside the Item cell, which is the part a narrow pane
	// truncates. The tag rides in the cell rather than in the flag column
	// because that column is the first one poFitLineGrid sheds, and it is
	// already spoken for by "[voided]" / "✓ received"; it goes in PLAIN, like
	// every other cell of this grid, because poGridCell fits by runes and would
	// cut a style's escape sequence in half.
	item := li.DisplayLabel()
	if li.IsKitLine {
		item = poKitTag + " " + item
	}
	out := []string{poDetailGridRow(
		fit,
		strconv.Itoa(lineNum),
		item,
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
	for _, text := range []string{
		poLineOrderedForToken(li),
		poLineInventoryNote(li),
		poLineAssetNote(li),
	} {
		if text == "" {
			continue
		}
		out = append(out, poLineContinuation(StyleMuted, text, bodyWidth))
	}
	if reason := poLineVoidReasonToken(li); reason != "" {
		out = append(out, poLineContinuation(StyleStatusWarn, reason, bodyWidth))
	}
	if li.Notes != "" {
		out = append(out, poLineContinuation(StyleMuted, li.Notes, bodyWidth))
	}

	// What this line actually puts on the shelf. Drawn from the ORDERED
	// quantity here — this screen is a record of the order, not a receipt — so
	// the lead-in names the denominator rather than leaving the multiplication
	// implied. The block WRAPS against the pane (poKitCreditBlock) instead of
	// riding the grid: the trailing components are as important as the leading
	// ones and an ellipsis eats the last.
	//
	// Both leads are TENSE-NEUTRAL, and that is the whole point of them. They
	// used to assert an action ("receiving all 2 kits credits", "credits on full
	// receipt"), computed from the ordered quantity alone — so a fully received
	// line rendered "✓ received" in its flag cell and, four lines below it, a
	// sentence saying the receipt was still to come. No number was wrong; the
	// sentence was, and a wrong sentence beside a right number is how an
	// operator ends up trusting the wrong one.
	//
	// Making the tense follow the line state was refused: a phrasing that has to
	// be revisited whenever the state model grows a case is a defect scheduled
	// for later, which is the shape of nearly every defect this screen has
	// already had. Wording true in every state — pending, part-received, done —
	// needs no maintenance and cannot contradict the flag above it. The RECEIVE
	// form keeps its future tense on purpose: there the action really is
	// pending, and there is no completed-state mark to disagree with.
	if li.IsKitLine {
		lead := "component breakdown per kit"
		if li.QuantityOrdered > 0 {
			lead = fmt.Sprintf("component breakdown for all %d %s",
				li.QuantityOrdered, plural("kit", li.QuantityOrdered))
		}
		out = append(out, poKitCreditBlock(li.KitComponents, lead,
			poLineGridItemIndent, bodyWidth, li.QuantityOrdered)...)
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

// poLinePartNumber is the makerspace's OWN identifier for what a line buys: the
// inventory SKU when the line has one, else the asset tag, else nothing. The
// SKU wins because a line that has both is a catalog line and the SKU is what
// the order was placed against.
//
// IT IS NOT WHAT A BUYER QUOTES AT THE SUPPLIER, which is what this comment
// used to claim. item_details.sku is generated by OMS when nobody supplies one
// and means nothing to a vendor; the string a vendor knows the part by is
// ItemSupplier.supplier_sku, which is what OMS builds every row of its own
// order-pad export from (poLineSupplierSKU carries the reference). The two are
// different values and the block form draws only this one, under the label
// PART — a reading of the shop's own catalogue, not of the vendor's.
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
	out := []string{prefix + fitCellIf(name, bodyWidth-lipgloss.Width(prefix))}

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

// poShipFieldCount / poShipBar and poDeliverBar are the two modals' shapes said
// ONCE: the handler needs the bar to ask the layer whether the frame is drawn
// before it moves the caret, and a second literal beside the view's would be a
// bar measured that is not the bar drawn.
const poShipFieldCount = 2

var (
	poShipBar = []actionBarItem{{"Enter", "Mark shipped"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}

	poDeliverBar = []actionBarItem{{"Enter", "Mark delivered"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
)

// viewShip is the mark-shipped prompt: which line, and when it went.
func (s *PurchaseOrderDetailScreen) viewShip() string {
	fields := []jdeField{
		{
			Label: "Line #", Kind: jdeText,
			Input:   &s.shipIdxIn,
			Width:   6,
			Hint:    fmt.Sprintf("1-%d", len(s.po.Items)),
			Focused: s.shipFocus == 0,
		},
		{
			Label: "Shipment date", Kind: jdeText,
			Input:   &s.shipDateIn,
			Width:   12,
			Hint:    "YYYY-MM-DD · blank is today, '-' clears",
			Focused: s.shipFocus == 1,
		},
	}
	labelW := jdeLabelWidth(fields)
	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Mark item shipped"))
	body.Add("")
	for _, line := range jdeCaveatLines("Records the day one line left the supplier.", s.bodyWidth()) {
		body.Add(line)
	}
	body.Add("")
	body.AddFittedFields(fields, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(nil, body, s.shipFocus,
		s.statusRow(s.shipPending, "Submitting…", s.shipErr), poShipBar)
}

// viewVoid is the void-the-whole-order prompt. The reason is optional here —
// VoidPurchaseOrder accepts an empty one — unlike a line void, which requires
// it; the caveat above the field is what says which of the two this is.
func (s *PurchaseOrderDetailScreen) viewVoid() string {
	field := jdeField{
		Label: "Reason", Kind: jdeText,
		Input:   &s.voidReasonIn,
		Width:   34,
		Hint:    "optional",
		Focused: true,
	}
	labelW := jdeLabelWidth([]jdeField{field})
	body := &jdeLines{}
	body.AddFittedFields([]jdeField{field}, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(s.voidHeader(), body, 0,
		s.statusRow(s.voidPending, "Voiding…", s.voidErr),
		[]actionBarItem{{"Enter", "Void order"}, {"Esc", "Cancel"}})
}

// voidHeader is the prompt's heading and caveat, PINNED above the Reason box.
//
// They used to lead the BODY, and a body's lead-in belongs to no navigable row:
// jdeLines.Window anchors on the cursor's block, the only block here is the
// Reason field, and nothing on this frame moves a cursor — so at 80x14 the pane
// opened `↑ 3 more above` with the sentence cut to "is not already voided. This
// cannot be undone." and no key able to fetch the half that says the void
// CASCADES TO EVERY LINE. That is the one fact this prompt exists to state, and
// the frame was advertising its absence.
//
// Pinned, they are trimmed by jdeFitHeader instead — which gives ground BY RANK
// and promises no remainder a key could fetch. The caveat is FITTED, so what a
// short pane loses is marked: trimmed row by row it read "…every line that is
// not already voided. This cannot be" at 49x12, one word short of the clause
// that says there is no undo. The heading is DECORATIVE and the caveat CONTEXT:
// where only one may survive it is the consequence and not the title, for the
// same reason the line-void prompt one screen over ranks its own rows that way.
// The essential row is left to the layer's minimum — see jdeMinBudget — because
// the Reason box is in the BODY here and the body's floor already keeps it.
// poVoidOrderCaveat is the order-void prompt's caveat, said once so the header
// and its refit cannot word it differently.
const poVoidOrderCaveat = "Voids the order and cascades to every line that is not already voided. This cannot be undone."

func (s *PurchaseOrderDetailScreen) voidHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleStatusWarn.Render("Void purchase order"))
	// addBlock brings the separator with it, so the heading does not add one of
	// its own — two blanks is a row of the budget spent twice.
	// FITTED, so a pane too short for all of it re-draws the caveat with its
	// cut marked rather than dropping its tail: the tail is "This cannot be
	// undone", and a caveat cut clean before it reads as the whole warning.
	width := s.bodyWidth()
	h = h.addFittedBlock(jdeHeadContext, jdeCaveatLines(poVoidOrderCaveat, width),
		func(rows int) []string { return jdeCaveatLinesIn(poVoidOrderCaveat, width, rows) })
	return h.add(jdeHeadDecorative, "")
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
			Input:   &s.deliverInputs[i],
			Width:   poDeliverWidths[i],
			Hint:    poDeliverHints[i],
			Focused: s.deliverFocus == i,
		}
	}
	labelW := jdeLabelWidth(fields)
	body := &jdeLines{}
	body.Add(StyleJDEHeading.Render("Mark delivered"))
	body.Add("")
	for _, line := range jdeCaveatLines("Receives every pending quantity as of the delivery date.", s.bodyWidth()) {
		body.Add(line)
	}
	body.Add("")
	body.AddFittedFields(fields, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(nil, body, s.deliverFocus,
		s.statusRow(s.deliverPending, "Submitting…", s.deliverErr), poDeliverBar)
}

// ---------------------------------------------------------------------------
// Order-pad overlay
// ---------------------------------------------------------------------------

func (s *PurchaseOrderDetailScreen) orderPadBar() []actionBarItem {
	return s.orderPadBarItems(s.padScrolls())
}

// orderPadBarItems builds the pad's bar for a given scroll state, and is what
// both orderPadBar and padScrolls go through — the bar that is MEASURED is the
// bar that is drawn.
//
// Esc is the only key that works on a pad with nothing on it, and the scroll
// keys are named only when the pad is taller than the overlay leaves it: a
// two-line pad fits with room to spare, and naming UP/DN, PgUp/PgDn and Home/End
// there cost 42 of the bar's 49 columns on keys that write a clamped offset
// straight back.
func (s *PurchaseOrderDetailScreen) orderPadBarItems(scroll bool) []actionBarItem {
	if !s.padHasText() {
		return []actionBarItem{{"Esc", "Close"}}
	}
	items := []actionBarItem{
		{"Enter", "Copy"},
		{"Esc", "Close"},
	}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

// padScrolls is the pad's half of the bar-honesty rule, read by the bar and by
// handleOrderPadKey alike.
func (s *PurchaseOrderDetailScreen) padScrolls() bool {
	if !s.padHasText() {
		return false
	}
	return s.bodyScrollsForBar(s.orderPadLines(), len(s.orderPadHeader()), s.orderPadBarItems(true))
}

// padMoves is the HANDLER's half where padScrolls is the BAR's — see
// sheetMoves. This is the pair the whole refused-pane rule was reported on:
// `end` set padScroll to the length of a forty-line pad while the pane drew
// nothing but the too-short notice, and growing the terminal back landed the
// operator at the bottom of the pad instead of where they left it.
func (s *PurchaseOrderDetailScreen) padMoves() bool {
	return s.frameDrawn(len(s.orderPadHeader()), s.orderPadBar()) && s.padScrolls()
}

// padHasText is the single condition the pad overlay's Enter turns on, and the
// same one orderPadLines uses to decide it has nothing to draw. TrimSpace and
// not a bare != "" for exactly that reason: the two used to disagree, so a
// whitespace-only export would have drawn "nothing to order" under a bar
// offering Copy.
func (s *PurchaseOrderDetailScreen) padHasText() bool {
	return s.orderPadExport != nil && strings.TrimSpace(s.orderPadExport.Text) != ""
}

// orderPadHeader is the chrome pinned above the pad: what was built, from how
// many lines, and which of them had no part number to build from.
// The RANKS are the whole of this screen's answer to a short pane. The ⚠ is
// ESSENTIAL: the pad text is already on the clipboard by the time this frame is
// drawn, so an operator who pastes it without that row pastes an order with
// lines silently missing from it — wrong data, not lost decoration. The
// supplier/count/filename meta is CONTEXT and the "Order pad" heading names
// nothing, so it goes first. Trimming by POSITION dropped the ⚠ and kept the
// heading at 80x12 and 80x13.
//
// Only the warning's FIRST line is essential, and its own wording is what makes
// that honest: the count leads and the names follow, so "3 lines have no part #"
// is the half that still tells the operator to go and look. The smallest
// drawable budget keeps one header row, so a builder claiming two would be
// making a promise the geometry cannot keep.
func (s *PurchaseOrderDetailScreen) orderPadHeader() jdeHeader {
	head := jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render("Order pad"))
	if s.orderPadExport == nil {
		return head.add(jdeHeadDecorative, "")
	}
	var meta []string
	if s.orderPadExport.Supplier != "" {
		meta = append(meta, s.orderPadExport.Supplier)
	}
	meta = append(meta, fmt.Sprintf("%d %s", s.orderPadExport.LineCount, plural("line", s.orderPadExport.LineCount)))
	if s.orderPadExport.Filename != "" {
		meta = append(meta, s.orderPadExport.Filename)
	}
	head = head.add(jdeHeadContext,
		jdeIndent+StyleMuted.Render(fitCellIf(strings.Join(meta, " · "), s.bodyWidth()-len(jdeIndent))))
	if miss := len(s.orderPadExport.MissingSku); miss > 0 {
		warn := fmt.Sprintf("⚠ %d %s no supplier part # (omitted): %s",
			miss, plural("line", miss), strings.Join(s.orderPadExport.MissingSku, ", "))
		for i, line := range jdeWrapNote(warn, poMarginWidth(s.bodyWidth())) {
			rank := jdeHeadContext
			if i == 0 {
				rank = jdeHeadEssential
			}
			head = head.add(rank, jdeIndent+StyleStatusWarn.Render(line))
		}
	}
	return head.add(jdeHeadDecorative, "")
}

// viewOrderPad renders the overlay: the pinned chrome, the scrollable part#/qty
// pad, and the bar. The pad text is already on the terminal clipboard (copied
// when it loaded); Enter is the always-available second chance.
func (s *PurchaseOrderDetailScreen) viewOrderPad() string {
	if s.orderPadLoading {
		body := &jdeLines{}
		body.Add(jdeIndent + StyleMuted.Render("Building order pad…"))
		return s.frameWrapped(jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render("Order pad"), ""), body, 0, "",
			[]actionBarItem{{"Esc", "Close"}})
	}
	if s.orderPadErr != "" {
		// The error lives on the status row above the bar and NOWHERE else, as
		// on every other converted phase: a caveat body here printed the same
		// sentence twice on one frame, once plain and once with the status
		// row's "✗ " in front of it.
		body := &jdeLines{}
		return s.frameWrapped(jdeHeader(nil).add(jdeHeadDecorative, StyleJDEHeading.Render("Order pad"), ""), body, 0,
			s.statusRow(false, "", s.orderPadErr),
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
