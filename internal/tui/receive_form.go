// ReceiveFormScreen — the whole terminal flow for receiving a purchase order:
// pick the order, scan or pick the line, scan the tracking barcode, say how
// much arrived, capture serials with their optional lot and expiry, and finish
// the order off.
//
// It renders through jde_form.go's shared columnar layer, as po_edit.go (the
// pilot) and po_add_line.go (the scanner flow) already do: a fixed label column
// with a dotted leader, a body the operator scrolls, and a PERSISTENT action bar
// naming exactly the keys that act in the state being drawn. There is no local
// copy of the scroll arithmetic, the status-row bound, or the field layout — all
// three are the layer's, and jde_lift_sweep_test.go holds that door shut.
//
// # The server decides; this screen relays
//
// Mismatch flagging, partial-receipt state, the received transition and serial
// validation all belong to OMS (internal/omsapi/po_receiving.go carries the
// contract note, and docs/PO_RECEIVING_API.md on the OMS side is the
// specification). Nothing here re-derives any of them, because a second opinion
// computed on this side is a second opinion that can disagree — and on this flow
// a disagreement is a stock figure nobody can reconcile.
//
// So the screen is built around ONE fetch:
//
//	GET …/purchase-orders/{id}/receiving/   the receiving WORKSHEET
//
// which answers, before anything is drawn, whether the order may be received
// against at all and why not; which lines are outstanding and which are settled,
// each with its own `receipt_state`; what a scanner will read off each line's
// goods (`scan_codes`); and which identities each line's serials may name
// (`serial_targets`).
//
// The worksheet is not optional and there is no fall-back to the order's own
// items. A failed fetch is COULD NOT TELL, which is a different fact from an
// order with nothing receivable, and receiving off `po.Items` would mean
// guessing at both of the things this screen must not guess at: which codes
// resolve to which line, and which identity a serial belongs to. So a failed
// load refuses, says so, and offers `r`.
//
// # Seven phases, and they are the whole screen (receivePhase)
//
//	loading → blocked                     (cannot receive, or the fetch failed)
//	        → qty → serial → review → (receipt posts) → done
//	               ↘ ──────────↗          (nothing serialized on the receipt)
//	          qty → writeOff → (close-short / mark-received posts) → done
//
// # The key scheme is the columnar one
//
//	Enter        FIND on the scan row, RECEIVE from anywhere else
//	Up/Down      move between rows (the three FIELDS of a unit, on capture)
//	Tab/Shift-Tab  the same, but ONLY on the phases with fields
//	PgUp/PgDn    page, when the body is taller than the pane (units, on capture)
//	Ctrl+K       close the focused LINE short (a confirm, then the server)
//	Ctrl+R       mark the whole order received (a confirm, then the server)
//	Ctrl+E       back to serial capture from the review
//	r            re-read the worksheet (the blocked frame, and the summary)
//	Esc          back to the order (and the bar says when that DISCARDS entry)
//
// The Tab alias is the one line of that table with an exception, and the
// exception is the point: it rides alongside Up/Down on a sheet WITH FIELDS,
// which is what roughly twenty columnar forms name as UP/DN=Fields — so on the
// read-only BLOCKED and REVIEW frames, which have no fields and only a row
// cursor, it is deliberately unbound and answers "tab does nothing here". A bar
// that named UP/DN while Tab also moved the cursor would be advertising one key
// and honouring two.
//
// Ctrl+K and Ctrl+R are named by the bar for exactly as long as they would act,
// and a typed quantity is one of the things that stops them — see
// lineWriteOffRefusal for why a write-off refuses rather than absorbing it.
//
// # A mismatch is recorded and flagged, never rounded
//
// A quantity larger than the outstanding balance is sent AS TYPED. The review
// phase says so before it goes — a typo is cheaper to fix than a vendor query —
// and the summary reports the variance the server came back with. Clamping the
// figure to the ordered quantity anywhere on this screen would destroy the only
// record of the discrepancy, which is the record the whole flow exists to
// produce. SHORT is not the same fact and is not flagged as one: receiving 8 of
// 10 leaves 2 outstanding, which may simply be on a backorder, and only an
// explicit close-short says the balance is not coming.
//
// # Serials go to the identity that goes on the shelf
//
// `serial_targets` is the answer to "which identities may this line's serials
// name", and it is the ONLY thing this screen reads for that question. On a KIT
// line those targets are the kit's serialized COMPONENTS and the kit itself
// never appears — a kit is bought as one SKU and stocked as its parts, so its
// own stock is permanently zero and a serial written against it names a unit
// that can never be drawn down.
//
// That is why nothing here consults the line's own `item_details.is_serialized`,
// which on a kit line describes the KIT. The old rule that serialized items
// could not be kit components has been lifted deliberately on the OMS side, so
// receiving a kit WITH serial capture is now a live path — and the guard that
// replaced the ban is this identity rule plus `serials_outstanding`, which the
// summary and the worksheet both surface.
//
// A KIT WITH SERIALIZED COMPONENTS IS NOW BUILDABLE FROM THIS TERMINAL, which is
// what makes that live path reachable rather than theoretical: the kit-components
// editor (`inventory_item_form_kit.go`) enforced the retired ban for a release
// after this file stopped believing it, so the only serialized-component kits a
// ScanTTY operator could receive were ones somebody had built on the web. The two
// halves are one rule read from opposite ends — a serial belongs to the COMPONENT
// identity and never to the kit's own id — and that editor's share of it (a kit's
// serialized toggle frozen, `is_serialized:false` asserted on every kit save) is
// untouched by the lift.
//
// # Every state answers every key
//
// The rules the purchasing screens are held to (AGENTS.md) apply here in full:
//
//   - WHILE A REQUEST IS OUT the form is FROZEN. The freeze is an ALLOW-LIST
//     (Esc, and nothing else), because written the other way round it would
//     freeze the keys somebody thought of and leave every arm added later free
//     by default. Esc is deliberately NOT gated: a frame with no way out while a
//     slow gateway thinks is the worse defect. Leaving does not cancel the
//     request.
//
//   - A SUCCESSFUL RECEIPT ENDS THE FLOW, on the summary, with the boxes gone.
//     It used to leave the operator on the quantity form with the boxes still
//     holding what they had typed, so a reflexive second Enter booked the whole
//     delivery again.
//
//   - NOTHING THE OPERATOR TYPED IS DISCARDED SILENTLY. Serial capture keeps
//     every unit's serial, lot and expiry while the cursor walks over them, and
//     when a changed quantity shrinks a line's capture list the units that go
//     are COUNTED and named in the note.
//
// receive_form_test.go drives the phases through Root.Update at the widths this
// project checks; receive_form_sweep_test.go presses the whole key space at
// every state, with the phase list derived from the iota and the state
// fingerprint derived by reflect over the screen struct.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// receivePhase tracks the stages of the receive flow.
type receivePhase int

const (
	// phaseLoading is the worksheet fetch. It is a phase rather than a flag
	// because it has a bar of its own — esc, and nothing else — and a body that
	// names the work and the subject.
	phaseLoading receivePhase = iota
	// phaseBlocked is "no receipt can be built here", and it covers TWO facts
	// that must not be collapsed: the fetch failed (could not tell), or it
	// succeeded and said this order may not be received against (and why). Both
	// offer `r`; each says which it is. blockedBody is where the split lives.
	phaseBlocked
	phaseQty
	phaseSerial
	phaseReview
	// phaseWriteOff confirms a destructive settlement — closing one line short,
	// or closing the whole order out — with the reason that will be recorded.
	phaseWriteOff
	// phaseReopenPick chooses WHICH closed-short line a correction is about, and
	// phaseReopenConfirm carries the reason and the commit.
	//
	// TWO phases rather than one, and the reason is geometric rather than
	// stylistic. A single frame would need a line CURSOR and a focused Reason
	// BOX at once, and jdeLines.Window anchors on one block: anchored on the
	// cursor the box goes off a short pane while the caret is in it (every rune
	// redrawing an identical frame, the reported-hang class this screen is
	// written against), and anchored on the box the movement keys move a
	// highlight nobody can see. The pick is a pure read-only list, so its
	// cursor anchors it; the confirm is one field and a question, so it is
	// writeOffBody's shape exactly.
	//
	// The pick is not skipped when only one line is reopenable. It is where the
	// close-short's own REASON is read, which is what tells the operator this is
	// the line they meant, and a flow that changes shape with the data is a bar
	// that changes shape with the data.
	phaseReopenPick
	phaseReopenConfirm
	phaseDone
	// receivePhaseCount is the sentinel the sweep walks to. It exists so a
	// phase added above it is pressed by the key space the day it is written
	// rather than the day somebody remembers to extend a table — three keys
	// reached an operator's terminal in this package doing nothing because the
	// sweep that existed to stop it was reading a hand-kept roster.
	receivePhaseCount
)

func (p receivePhase) String() string {
	switch p {
	case phaseLoading:
		return "loading"
	case phaseBlocked:
		return "blocked"
	case phaseQty:
		return "qty"
	case phaseSerial:
		return "serial"
	case phaseReview:
		return "review"
	case phaseWriteOff:
		return "write-off"
	case phaseReopenPick:
		return "reopen-pick"
	case phaseReopenConfirm:
		return "reopen-confirm"
	case phaseDone:
		return "done"
	}
	return fmt.Sprintf("receivePhase(%d)", int(p))
}

// receiveScope is what a write-off confirm is about. The two are different
// endpoints with different blast radii, and the confirm frame says which.
type receiveScope int

const (
	// receiveScopeLine closes ONE line's outstanding balance short.
	receiveScopeLine receiveScope = iota
	// receiveScopeOrder closes EVERY still-outstanding line short. Deliberately
	// NOT mark-delivered, which asserts the opposite — that the outstanding
	// quantity did arrive — and stocks it.
	//
	// What the ORDER then becomes is the server's, and this screen does not
	// predict it: whether closing the last balance advances the order to
	// `received` is a rule that has already changed once (an order nothing was
	// ever received against no longer advances), so the confirm says what the
	// write DOES and the summary reports what came back.
	receiveScopeOrder
)

// receiveLine is one line of the form: the server's worksheet row, plus the
// kit-component preview that only the purchase order carries.
//
// Two sources because the worksheet does not repeat `kit_components` — it says
// `is_kit_line` and leaves the bill of materials to the order, which is where
// the order-time snapshot lives. Everything the RECEIPT depends on comes from
// the worksheet half; the kit half is a preview of what a typed quantity would
// credit, and an order fetched without it simply draws no preview.
type receiveLine struct {
	sheet omsapi.ReceivingLine
	kit   []omsapi.POKitComponent
}

// serialUnit is one serial-capture slot: a single unit of ONE serialized
// identity credited by this receipt.
//
// itemID is the identity the serial is recorded against, and on a kit line that
// is a COMPONENT — never the kit. It comes from the worksheet's
// `serial_targets`, which is the server's own answer to that question, so this
// screen cannot offer an identity the receipt would refuse.
type serialUnit struct {
	lineIdx   int    // index into ReceiveFormScreen.lines
	poItemID  any    // PurchaseOrderItem id, for grouping the payload
	lineLabel string // the line's display label, for the prompt
	itemID    string // InventoryItem UUID the serial belongs to
	itemName  string
	itemSKU   string
	unitNo    int // 1-based unit index within this (line, identity)
	unitTot   int // units of this identity the receipt credits on this line
}

// key identifies a capture slot across a re-enrolment, so a quantity edited
// somewhere else on the form does not throw away serials already typed.
func (u serialUnit) key() string {
	return fmt.Sprint(u.poItemID) + "\x00" + u.itemID + "\x00" + strconv.Itoa(u.unitNo)
}

// receiveCapture is what the operator typed against one unit. A blank
// SerialNumber means the unit was passed over — allowed on purpose, and
// reported afterwards as an outstanding serial rather than hidden.
type receiveCapture struct {
	serial string
	lot    string
	expiry string
}

func (c receiveCapture) empty() bool {
	return strings.TrimSpace(c.serial) == "" &&
		strings.TrimSpace(c.lot) == "" && strings.TrimSpace(c.expiry) == ""
}

// receiveLabels is this screen's label column, in ONE place so every phase
// hangs off the same leader — the columnar rule that a value never moves
// sideways when the frame changes under it (po_add_line's poAddLabels).
// Every label this screen draws is here, and a missing one is not cosmetic:
// jdeLabelWidth sizes the leader column from this list, so a label the list does
// not know is CLIPPED on the row it is drawn on — "Delivered" arrived as
// "Delivere", on a date field, where a clipped word reads as a different one.
var receiveLabels = []string{
	"Scan", "Tracking", "Carrier", "Delivered", "Quantity", "Notes",
	"Serial", "Lot", "Expires",
	"Reason",
	"Order", "Receipt", "Lines", "Variance", "Serials",
}

func receiveLabelWidth() int {
	fields := make([]jdeField, len(receiveLabels))
	for i, l := range receiveLabels {
		fields[i] = jdeField{Label: l}
	}
	return jdeLabelWidth(fields)
}

// receiveMetaIndent lines a line's readings up under its NAME rather than under
// the leader column: they are a continuation of the row above them, not a value
// hanging off a label (jde_form.go's detail-grid idiom).
const receiveMetaIndent = jdeIndent + "   "

// The fixed rows of the quantity form, ahead of the per-line quantity boxes.
//
// The SCAN row leads because a barcode scanner is a keyboard that fires a burst
// the moment goods are put under it, and a burst has to land somewhere it means
// something. With the cursor anywhere else the first digits of a scanned code
// would go into a QUANTITY box, which is the one field on this screen where a
// wrong number is a wrong stock figure.
//
// Tracking, Carrier and Delivered follow it because that is the operator's own
// order of work — scan the parcel's label, then count what is in it — and a
// form whose rows run in a different order from the job teaches the operator to
// skip around it.
//
// DELIVERED is a row and not an assumption. The server defaults the delivery
// date to now, and "now" is right only when the goods are booked in on the day
// they turned up — which the captain has said outright is often not the case,
// and which is the same fact that made transit duration not worth computing.
// So the date the operator STATES is typable, blank means today, and nothing on
// this screen derives a duration from it.
const (
	receiveRowScan = iota
	receiveRowTracking
	receiveRowCarrier
	receiveRowDelivered
	receiveRowFirstLine
)

// receiveKitCaveat is the warning drawn under a KIT LINE's quantity box. Its
// second half is the half that matters — an operator who reads only "kit lines
// credit" has been told nothing — so it is WRAPPED wherever it is drawn rather
// than clipped.
//
// It used to lead the whole body as a standing sentence about the ORDER, and
// that is where it could not be read: qtyBody's lead lines belong to no
// navigable row, jdeLines.Window anchors on the cursor's block, and no key on
// this screen moves a cursor above the first row — so at the canonical 80x24
// with one kit line the pane opened on "↑ 5 more above" with this sentence
// among the five and nothing able to bring it back. It is drawn where the
// number is typed instead, which is both reachable and the place it is about.
// It is as short as it can be said. jdeLines.Window keeps a BLOCK's start and
// there is no scrolling inside one, so every line of a kit line's block that is
// not the tail is a line the credit preview below it can be pushed out by — at
// 80x30 three rows of this sentence were enough to take the second component
// off the pane with no key able to fetch it. The half that had to go is the
// half already said twice elsewhere: the box's own "kits" hint and the row's
// "ordered 2 kits" both say what the quantity counts.
const receiveKitCaveat = "Receiving one credits the kit's COMPONENT items, not the kit."

type ReceiveFormScreen struct {
	deps Deps
	// jdeScreen carries the pane geometry (terminal size, body width, the row
	// budget) and the framing. Embedded rather than copied, so bodyWidth(),
	// statusRow() and frameWrapped() read here exactly as they do on every
	// other converted sheet.
	jdeScreen

	// po is the order this screen was opened from. It is read for the order's
	// id and for the kit-component previews; every fact the RECEIPT depends on
	// comes off the worksheet instead.
	po *omsapi.PurchaseOrder
	// sheet is the server's receiving answer. Nil means the fetch has not
	// landed — either still out (phaseLoading) or failed (phaseBlocked) — and
	// those two are never allowed to look alike.
	sheet *omsapi.ReceivingWorksheet
	// lines are the worksheet rows a receipt may name, in worksheet order.
	// Settled-but-open lines are here too: the server refuses only a VOIDED or
	// CLOSED-SHORT line, and an already-received line can still take an
	// over-receipt, so narrowing further would refuse a receipt OMS accepts.
	lines []receiveLine
	// closed are the rows a receipt may NOT name — voided and closed short.
	// They are drawn read-only under the notes row, because "which lines are
	// outstanding" is only answerable when the settled ones are visible too.
	closed []receiveLine

	phase   receivePhase
	loading bool

	scan      textinput.Model
	tracking  textinput.Model
	carrier   textinput.Model
	delivered textinput.Model
	qty       []textinput.Model
	notes     textinput.Model
	focused   int
	pending   bool

	// rowCursor is the cursor of whichever READ-ONLY body is on the pane — the
	// blocked frame's line list, or the review's. One field rather than two
	// because the two phases are never on screen together and each resets it on
	// entry, and because a second cursor is a second thing for a movement arm
	// to pick the wrong one of.
	rowCursor int

	// Serial capture (phase 3). Enrolment is derived from the worksheet's
	// serial_targets scaled to the quantity being received, so a kit line
	// enrols its serialized COMPONENTS and never the kit. captures is parallel
	// to serialUnits and holds what the operator typed; it survives moving
	// between units and survives a re-enrolment wherever the slot still exists.
	serialUnits  []serialUnit
	captures     []receiveCapture
	serialCursor int
	serialField  int
	serialInput  textinput.Model
	lotInput     textinput.Model
	expiryInput  textinput.Model
	// dropped counts capture slots a re-enrolment removed because the quantity
	// they belonged to shrank. It is reported rather than silently applied.
	dropped int

	// The write-off confirm (phase 6).
	scope     receiveScope
	scopeLine int
	// reason is the settlement field, shared by the write-off confirm and the
	// reopen confirm. ONE box because the two confirms are never on screen
	// together and the SERVER takes one shape for both (omsapi.POLineSettlement,
	// which mirrors OMS's own shared LineSettlementSerializer): a second box
	// would be a second thing to clear, blur and classify for the one value both
	// writes carry. Each confirm empties it on the way in.
	reason textinput.Model

	// The reopen-short correction (phases 7 and 8).
	//
	// reopenCursor is the pick list's own cursor rather than rowCursor, which
	// the blocked frame and the review share. Those two never appear together
	// and each resets it on entry; the pick can be reached FROM the blocked
	// frame and Esc'd back to it, so sharing would move the operator's place on
	// a list they had scrolled.
	//
	// reopenLine indexes s.closed — the SETTLED list, the one the quantity form
	// numbers under "N settled line(s) cannot take a receipt" — so the number
	// the confirm names is a number the operator can find.
	reopenCursor int
	reopenLine   int
	// reopened is what a reopen that LANDED did, held across the worksheet
	// refetch that follows it.
	//
	// It exists because the correction's own confirmation would otherwise be the
	// one thing the reload wipes: Update retires the note on every reply off the
	// wire, and a successful reopen answers by fetching the worksheet again — so
	// the note saying the line is back would be cleared by the very read that
	// proves it. handleSheet restates it and clears this.
	reopened string

	// parked is the box currentInput answers with on a phase that holds none —
	// the summary, the loading frame, the blocked frame, the review. It is
	// never drawn and never read.
	//
	// It exists so that "who owns the caret?" is TOTAL over the phases. The
	// alternative is a nil return and a guard at every call site, and the arms
	// that blur or focus run across several phases: one missing guard there is
	// a panic on a screen that moves stock, and the guard that is present is
	// invisible reasoning. A named, blurred, undrawn box makes the answer
	// explicit — blurAll clears it with the rest, and the sweep's focus
	// fingerprint carries it, so a caret that somehow landed here would be
	// reported rather than hidden.
	parked textinput.Model

	// result is the order as the SERVER returned it from whichever write
	// finished the visit, and receipt is the sentence the summary leads with.
	// Both are written only by a reply off the wire.
	result  *omsapi.PurchaseOrder
	receipt string

	// failHead / failDetail are the failure line's two halves, always written
	// together by setFail so a stale detail can never be drawn under a fresh
	// headline. The headline goes on the status row, which cannot fold; the
	// detail is folded and bounded in the body, because omsapi.parseError puts
	// the ENTIRE raw response body into APIError.Message whenever the JSON
	// envelope carries no code, so a gateway's HTML page arrives here whole.
	failHead   string
	failDetail string

	// note is the screen's answer to the last keypress, drawn in the BODY. The
	// status bar carries the same words, but a flash expires after four seconds
	// and the operator who pressed a key and saw nothing is still looking.
	note pickerNote
}

// receiveSheetMsg is the worksheet fetch's reply.
type receiveSheetMsg struct {
	sheet *omsapi.ReceivingWorksheet
	err   error
}

// receiveSubmittedMsg is the receipt's reply, and also the write-off's: both
// answer with the updated purchase order, and both end the visit on the
// summary. `what` names which, so the summary and the failure headline can say
// what it was without a second flag to keep in step.
type receiveSubmittedMsg struct {
	what string
	po   *omsapi.PurchaseOrder
	err  error
}

// receiveReopenedMsg is the reopen-short reply.
//
// A type of its own rather than a flag on receiveSubmittedMsg, because the two
// answers are not the same shape of event: a receipt and a write-off END the
// visit on the summary, and a correction is made so the visit can CONTINUE —
// it refetches the worksheet and hands the operator back a live form. A boolean
// discriminator would have put two flows through one arm whose whole body
// differs.
type receiveReopenedMsg struct {
	what string
	po   *omsapi.PurchaseOrder
	err  error
}

func NewReceiveFormScreen(deps Deps, po *omsapi.PurchaseOrder) *ReceiveFormScreen {
	s := &ReceiveFormScreen{deps: deps, po: po, phase: phaseLoading, loading: true}
	// None of these boxes carries a Width, and none carries a PLACEHOLDER.
	//
	// The width is the layer's: a text row hands it the BOX (jdeField.Input) and
	// jdeFitInputValue sizes a copy against the input area the row was given, so
	// the value scrolls and the caret is always on the pane. Fixing a width here
	// would tie it to one terminal and throw away the columns a wider one has.
	// Before the conversion these were unbounded in the other direction —
	// bubbles' handleOverflow returns early at Width 0 and View() emits the
	// WHOLE value — so typing past the pane edge in the notes box (CharLimit
	// 200) redrew the row byte for byte with the caret off-screen.
	//
	// The placeholders went because in a columnar row they read as VALUES: the
	// underscored fill is what says a field is empty, and a placeholder covers
	// exactly that. The quantity boxes were the harmful case — an empty box
	// showing "0" invites "it already says zero, leave it" on the form that
	// decides how much stock exists. jdePickList drops its filter box's
	// placeholder for the same reason; what the row is for is said by the label
	// and the hint, which cost the field nothing.
	for _, box := range []struct {
		field *textinput.Model
		limit int
	}{
		// The scan box takes a whole barcode; the tracking box is bounded at
		// the 100 characters the server's own field is, so a scanner that fires
		// a long code is refused HERE with a visible box that stopped taking
		// characters rather than by a 400 after the receipt was built.
		{&s.scan, 120},
		{&s.tracking, 100},
		{&s.carrier, 100},
		// An ISO date and nothing else fits, so the box stops at ten
		// characters: a scanner burst that lands here is refused by a box that
		// visibly stopped taking it rather than by a 400 after the receipt was
		// built.
		{&s.delivered, 10},
		{&s.notes, 200},
		{&s.serialInput, 200},
		{&s.lotInput, 200},
		{&s.expiryInput, 10},
		{&s.reason, 200},
		// The undrawn one is built like the rest: a zero-value textinput.Model
		// has a nil cursor inside it, so Focus() on one panics — and the whole
		// point of parked is that a caller may blur or focus it without asking
		// which phase it is.
		{&s.parked, 1},
	} {
		*box.field = textinput.New()
		box.field.Prompt = ""
		box.field.CharLimit = box.limit
	}
	s.scan.Focus()
	return s
}

// applyWorksheet installs a fetched worksheet and rebuilds the form from it.
//
// It is the ONE place the row model is derived, so the boxes, the read-only
// tail and the scan index cannot come from different readings of the same
// payload. Called by the fetch's reply and, on a reload, by the next one.
func (s *ReceiveFormScreen) applyWorksheet(w *omsapi.ReceivingWorksheet) {
	// What was typed against a line that is STILL on the form is carried
	// across by its id, and it is read BEFORE anything is rebuilt. A reload is
	// something an operator asks for mid-entry — they closed a line short in
	// another window, or the first fetch failed — and losing their counts to it
	// would be the silent discard this screen is written against.
	typed := map[string]string{}
	for i := range s.qty {
		if i < len(s.lines) {
			typed[fmt.Sprint(s.lines[i].sheet.PurchaseOrderItem)] = s.qty[i].Value()
		}
	}

	s.sheet = w
	s.lines, s.closed = nil, nil
	kits := map[string][]omsapi.POKitComponent{}
	if s.po != nil {
		for _, li := range s.po.Items {
			if len(li.KitComponents) > 0 {
				kits[fmt.Sprint(li.ID)] = li.KitComponents
			}
		}
	}
	for _, l := range w.Lines {
		row := receiveLine{sheet: l, kit: kits[fmt.Sprint(l.PurchaseOrderItem)]}
		// The split is exactly what the server refuses, and no wider. `receive`
		// rejects a VOIDED line and a CLOSED-SHORT one and nothing else — an
		// already-received line still takes an over-receipt — so a narrower
		// rule here would hide a box for a receipt OMS would have accepted, on
		// the screen whose whole job is recording what really turned up.
		if l.IsVoided || l.IsClosedShort {
			s.closed = append(s.closed, row)
			continue
		}
		s.lines = append(s.lines, row)
	}
	s.qty = make([]textinput.Model, len(s.lines))
	for i, l := range s.lines {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 8
		ti.SetValue(typed[fmt.Sprint(l.sheet.PurchaseOrderItem)])
		s.qty[i] = ti
	}
	if s.focused >= s.totalInputs() {
		s.focused = receiveRowScan
	}
	// The caret is NOT placed here. applyWorksheet is about the row model, and
	// which box owns the keyboard is a property of the PHASE — which its one
	// caller decides on the line after this, since a worksheet saying the order
	// cannot be received against puts the screen somewhere with no box at all.
	s.blurAll()
}

// Title names the order in the SAME voice the frames do, which is why it reads
// orderName rather than s.po.Number.
//
// It used to fall back to a generic "Receive Items" whenever Number was empty,
// and that became load-bearing the moment the body stopped carrying a heading
// of its own: an order with no number was then named NOWHERE on the resting
// frame — not in the title Root pins above the pane, not in the body — on the
// screen that moves stock. Every other sentence on this screen already answers
// that case (orderName falls back to "PO #<id>", the working line and the
// receipt line both go through it), so the title was the one voice disagreeing.
// The generic wording survives for the one state that genuinely has no order to
// name.
func (s *ReceiveFormScreen) Title() string {
	if s.po == nil && s.sheet == nil {
		return "Receive Items"
	}
	return "Receive " + s.orderName()
}

// WantsRawInput routes every key here. Several phases hold a focused textinput,
// and the flow owns its own Esc: leaving is a step of THIS screen (back to the
// order), not the root's back-stack pop.
func (s *ReceiveFormScreen) WantsRawInput() bool { return true }

func (s *ReceiveFormScreen) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, s.loadSheet())
}

// ---------------------------------------------------------------------------
// The quantity form's row model
// ---------------------------------------------------------------------------

// notesRow is the last navigable row of the quantity form, and it is where
// everything that is not a receivable line hangs.
func (s *ReceiveFormScreen) notesRow() int { return receiveRowFirstLine + len(s.lines) }

func (s *ReceiveFormScreen) totalInputs() int { return s.notesRow() + 1 }

// lineAt maps a navigable row to the receivable line it carries, if any.
func (s *ReceiveFormScreen) lineAt(row int) (int, bool) {
	i := row - receiveRowFirstLine
	if i < 0 || i >= len(s.lines) {
		return 0, false
	}
	return i, true
}

// inputAt is the box a row types into. It is total over the row model — every
// row of the quantity form has exactly one box — which is what lets the
// movement arms stay ignorant of which row they landed on.
func (s *ReceiveFormScreen) inputAt(row int) *textinput.Model {
	switch row {
	case receiveRowScan:
		return &s.scan
	case receiveRowTracking:
		return &s.tracking
	case receiveRowCarrier:
		return &s.carrier
	case receiveRowDelivered:
		return &s.delivered
	}
	if i, ok := s.lineAt(row); ok {
		return &s.qty[i]
	}
	return &s.notes
}

// currentInput is the box the caret belongs in for the phase being drawn. The
// phases that hold no box at all answer with a scratch field rather than nil,
// so a caller that blurs or focuses unconditionally cannot nil-deref one — the
// arms that navigate run on several phases and the one that does not is the
// exception, not the rule.
func (s *ReceiveFormScreen) currentInput() *textinput.Model {
	switch s.phase {
	case phaseSerial:
		if s.serialCursor >= len(s.serialUnits) {
			// Past the end of the queue serialBody draws NO field at all, so
			// there is no box for the caret to belong in and the scratch field
			// is the honest answer. toSerial ends in focusCurrent, so without
			// this the re-entry path above armed s.serialInput on a frame that
			// renders none of it — verbatim the lie this function's comment
			// exists to prevent, and harmless today only because keySerial's
			// past-the-end block returns before anything routes a keystroke
			// there. "Harmless because of what some other arm happens to do"
			// is not a property; this is.
			return &s.parked
		}
		return s.serialBox(s.serialField)
	case phaseWriteOff, phaseReopenConfirm:
		// Both settlement confirms draw the ONE Reason field, so the caret
		// belongs there on both. The reopen confirm is here rather than falling
		// through to the scratch field because Update routes every non-key
		// message — the cursor blink, chiefly — to whatever this answers: left
		// out, the box the frame draws focused would be the one box on the
		// screen whose caret never blinks.
		return &s.reason
	case phaseQty:
		return s.inputAt(s.focused)
	}
	return &s.parked
}

// serialBox is the capture field the serial cursor is on. The order is the
// order they are drawn in, and it is the order a scanner drives: the serial
// first, because that is the one a barcode fires into.
func (s *ReceiveFormScreen) serialBox(field int) *textinput.Model {
	switch field {
	case receiveSerialLot:
		return &s.lotInput
	case receiveSerialExpiry:
		return &s.expiryInput
	}
	return &s.serialInput
}

const (
	receiveSerialNumber = iota
	receiveSerialLot
	receiveSerialExpiry
	receiveSerialFields
)

// focusCurrent puts the caret back in the box the phase types into, and does
// nothing at all on a phase that has none.
//
// The test is pointer identity against `parked` rather than a list of phases,
// because a list is one phase away from being wrong and this one fails
// silently: an armed caret in a box no frame draws is the "who owns the
// keyboard?" question answered with a lie, and the only visible symptom is a
// keystroke going somewhere the operator cannot see.
func (s *ReceiveFormScreen) focusCurrent() {
	if box := s.currentInput(); box != &s.parked {
		box.Focus()
	}
}

// blurAll takes the caret out of every box on the screen.
//
// One function rather than a blur beside each focus, because the phases move
// between DIFFERENT boxes and a phase change that focused the new one without
// blurring the old left two reverse-video fields on the pane — the layer's
// strongest "you may type here" signal, drawn twice, on a screen where only one
// of them was listening.
func (s *ReceiveFormScreen) blurAll() {
	for _, box := range s.allBoxes() {
		box.Blur()
	}
}

// allBoxes is every textinput on the screen, in ONE list so a box added later
// cannot be left out of a blur or of a reset.
//
// It is a literal because it is on a render path, and a literal is exactly what
// went wrong: `delivered` was added as a row after this list was written and the
// list did not follow it, so for as long as that stood the screen had two
// defects with one cause. `blurAll` never blurred it, which meant leaving the
// quantity form from the Delivered row carried a live caret through the review,
// the submit and the summary — two reverse-video fields on the pane at once,
// the very thing blurAll's comment says it exists to stop. And `resetEntry`
// never cleared it, so `R` on the summary ("Receive more") handed back a form
// still holding the date the LAST delivery arrived on, under a hint reading
// "blank = today", and the next receipt posted that date for goods that came in
// on another day — wrong data on the wire with nothing on the pane saying so.
//
// So the LIST stays hand-written and the CLAIM is what is derived:
// TestReceiveFormScreen_EveryBoxIsInAllBoxes reflects over the struct for every
// textinput.Model field, looking THROUGH slices because the quantity boxes are
// one per receivable line, and fails naming any box this list does not reach.
// A tenth box added tomorrow fails the build rather than the operator.
func (s *ReceiveFormScreen) allBoxes() []*textinput.Model {
	out := []*textinput.Model{
		&s.scan, &s.tracking, &s.carrier, &s.delivered, &s.notes,
		&s.serialInput, &s.lotInput, &s.expiryInput, &s.reason, &s.parked,
	}
	for i := range s.qty {
		out = append(out, &s.qty[i])
	}
	return out
}

// paneWidth is the columns the body really has, falling back to the width this
// project checks against when the terminal has not sized us yet. The live width
// and not a constant: clipping a line's name to 51 cells on a 120-column
// terminal throws away forty columns the pane had room for, on the row an
// operator reads to decide which line they are typing a quantity into.
func (s *ReceiveFormScreen) paneWidth() int {
	if w := s.bodyWidth(); w > 0 {
		return w
	}
	return screenBodyWidth(80)
}

// orderName is the order as the frames name it, never blank. The WORKSHEET's
// number wins because it is the fresher read of the same fact.
func (s *ReceiveFormScreen) orderName() string {
	if s.sheet != nil && s.sheet.Number != "" {
		return s.sheet.Number
	}
	if s.po == nil {
		return "this order"
	}
	if s.po.Number != "" {
		return s.po.Number
	}
	return fmt.Sprintf("PO #%v", s.po.ID)
}

// poID is the order the endpoints are addressed by.
func (s *ReceiveFormScreen) poID() string {
	if s.po != nil {
		return fmt.Sprint(s.po.ID)
	}
	if s.sheet != nil {
		return fmt.Sprint(s.sheet.PurchaseOrder)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Saying things
// ---------------------------------------------------------------------------

// say records the note and flashes the same words. BOTH: the bar is where an
// operator's eye goes for "did that work", and the body line is what is still
// there once the flash has gone.
func (s *ReceiveFormScreen) say(text string, level StatusLevel) tea.Cmd {
	return s.note.say(text, level)
}

// decline answers a key that does not act in the state being drawn. The lead
// NAMES the key, which is not decoration: two keys sharing one sentence would
// let the second press redraw the pane the first one left, and on a frame with
// no cursor and no caret that is indistinguishable from a wedged program.
//
// headerRows is the pinned header of the frame the key was pressed AGAINST —
// see handleKey for why every arm carries it.
func (s *ReceiveFormScreen) decline(key string, headerRows int) tea.Cmd {
	return s.say(key+" does nothing here · "+s.waysOut(headerRows), StatusWarn)
}

// typeInto hands a key to the focused box and ANSWERS for it when the box does
// not.
//
// The three typing phases used to end their switch with a bare box.Update, and
// a fallthrough like that cannot be audited: the set of keys it swallows is
// "everything nobody thought of". What it swallowed here was ctrl+t, ctrl+p,
// ctrl+n, ctrl+x and ctrl+r — none of them bound by bubbles' textinput, all of
// them named by some other frame of this same screen — and Up and Down on the
// write-off confirm, which is the frame where the next key writes a balance
// off and the frame every route into it comes from names UP/DN. Every one of
// those presses redrew a byte-for-byte identical pane, which from the
// operator's seat is a program that has stopped responding: rule 1, broken by
// omission.
//
// The box's OWN answer is what decides, rather than a roster of the keys it
// binds: the key goes to the box, and if the box did not take it the frame
// declines by name. Derived from bubbles itself, so a binding a version bump
// adds or drops changes this answer with it, where a roster would go on
// claiming the old set — and it covers the edges a roster never reaches, like
// Right with the caret already at the end of the value, or a rune typed into a
// box that is at its CharLimit.
//
// "Took it" is asked of EVERYTHING the widget hands back: the VALUE, the CARET,
// and the COMMAND. The command is the half this was first written without, and
// leaving it out broke Ctrl+V on all three typing phases. bubbles handles its
// Paste binding as `return m, Paste` — the model comes back byte for byte
// unchanged and the whole of the work is in the returned command — so against a
// value/caret comparison a paste is indistinguishable from a key the box
// ignored: the frame printed "ctrl+v does nothing here" and threw the Paste
// command away, which is a silent discard AND a false claim in one press. Any
// binding whose effect is asynchronous has that shape, so the command is asked
// about rather than ctrl+v being named: bubbles adds bindings, and the next one
// of this shape must not have to be remembered.
//
// The box's KEY MAP is deliberately NOT the authority, and that is not an
// oversight — it was tried. It claims more than the widget can act on: Up and
// Down are its suggestion keys, live only with ShowSuggestions set, which this
// screen never sets. Routing every key the map claims would hand Up, Down,
// Ctrl+P and Ctrl+N back to a box that does nothing with them, which is
// verbatim the silence on the write-off confirm this function exists to end.
// A command is what the widget really did; a binding is only what it advertises.
//
// The value and the caret are asked about rather than the RENDERED box, for a
// reason only a real terminal shows: lipgloss draws the caret with reverse
// video, so moving it changes the render — but lipgloss strips every sequence
// when stdout is not a TTY, so with the profile off Left over "abc" renders
// "abc" either way, and the key would be declined inside a test binary and
// honoured in production. A bound that answers differently depending on whether
// anybody is watching is not a bound.
//
// Nothing is dropped on the declining path: cursor.Update ignores a KeyMsg and
// tea.Batch of nothing but nils is nil, so a key the box ignored hands back no
// command at all. That is what makes the command half safe to read as an
// answer, and TestReceive_TheFieldOwnershipProbeIsNotVacuous pins it, because a
// bubbles that started returning a blink on every keystroke would make this
// function stop declining anything and say so nowhere.
func (s *ReceiveFormScreen) typeInto(box *textinput.Model, m tea.KeyMsg, headerRows int) tea.Cmd {
	before, at := box.Value(), box.Position()
	next, cmd := box.Update(m)
	*box = next
	if cmd == nil && box.Value() == before && box.Position() == at {
		return s.decline(m.String(), headerRows)
	}
	return cmd
}

// declineFrozen is decline for a key an in-flight request has made inert. It
// says WHY rather than "does nothing", because the key does work — one second
// from now — and an operator watching a slow gateway is exactly the operator
// who will press it again.
//
// It names WHAT is out, because the frozen states on this screen are waiting on
// different requests and the sentence used to say "the receipt" in all of them.
func (s *ReceiveFormScreen) declineFrozen(key string, headerRows int) tea.Cmd {
	return s.say(key+" is frozen until "+s.inFlightSubject()+" answers · "+
		s.waysOut(headerRows), StatusWarn)
}

// inFlightSubject is the request the screen is waiting on, in the words a
// decline can be built out of. It is the same expression workingLine reads, so
// a decline and the status row above it cannot disagree about what the screen
// is waiting for.
func (s *ReceiveFormScreen) inFlightSubject() string {
	switch {
	case s.loading:
		return "the worksheet"
	// The PHASE is the whole test, and one flag is why. `pending` means "a POST
	// is out" on every phase and the phase says which POST it is — the write-off
	// confirm is the one place `pending` is not a receipt — so there is no
	// second flag to consult and nothing to keep in step with it. This arm read
	// `phase == phaseWriteOff || scopePending()` for a while, and the second
	// disjunct was `pending && phase == phaseWriteOff`: strictly implied by the
	// first, so it could never change the answer while reading as a distinction
	// the code makes and does not.
	//
	// Reaching this at all means a request IS out — declineFrozen is the only
	// caller and every arm that reaches it has already tested `pending` or
	// `loading` — so the phase is the only thing left to ask about. The shape is
	// workingLine's, which is what lets a decline and the status row above it
	// name the same request.
	case s.phase == phaseWriteOff:
		return "the write-off"
	case s.phase == phaseReopenConfirm:
		return "the reopen"
	}
	return "the receipt"
}

// waysOut names the keys that DO act, read off the bar of the frame the key was
// pressed against, so a decline cannot advertise a key that frame did not
// honour — nor omit one it did.
func (s *ReceiveFormScreen) waysOut(headerRows int) string {
	items := s.barFor(headerRows)
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, strings.ToLower(it.Key)+" "+strings.ToLower(it.Label))
	}
	if len(parts) == 0 {
		return "nothing here acts yet"
	}
	return strings.Join(parts, " · ")
}

// setFail writes the failure line's two halves TOGETHER. They are one fact: a
// headline written on its own would leave the previous failure's detail
// standing under it, which reads as the gateway explaining a validation
// message.
func (s *ReceiveFormScreen) setFail(head, detail string) {
	s.failHead, s.failDetail = head, detail
}

func (s *ReceiveFormScreen) clearFail() { s.setFail("", "") }

// receiveFailure splits whatever came back off the wire into a HEADLINE for the
// one-line status row and a DETAIL the body folds under it.
//
// The split matters because the status row cannot fold: jdeScreen.fitStatus
// flattens and clips it. So the row gets a short fixed headline and the
// unbounded half goes in the body, where it is bounded and folded — never the
// other way round, which is how a 502 came to be shown as
// "✗ oms: http 502: <!DOCTYPE html><htm" and nothing else.
func receiveFailure(what string, err error) (head, detail string) {
	if err == nil {
		return "", ""
	}
	return what + " failed", receiveReason(err)
}

// receiveReason is the half of an error worth showing an operator.
//
// THREE shapes reach here and they are tried in the order that recovers the
// most prose. The receiving endpoints write their refusals as a hand-built
// `{"error": "<prose>"}` that never reaches OMS's DRF exception handler, so
// parseError finds no code and hands over the whole raw body —
// omsapi.AsReceivingRefusal is what turns that back into the sentence the
// server actually wrote, and without it the operator reads the JSON on the one
// step where losing the reason costs the delivery. A CODED envelope keeps its
// own message, and anything else (a gateway page, a transport failure) arrives
// whole and is bounded where it is drawn.
func receiveReason(err error) string {
	if prose, ok := omsapi.AsReceivingRefusal(err); ok {
		return prose
	}
	var api *omsapi.APIError
	if errors.As(err, &api) {
		// An expired session is NAMED rather than relayed. Every receiving
		// endpoint is authenticated — the worksheet included, GET though it is,
		// because PurchaseOrderViewSet.get_permissions gates every @action —
		// so a session whose token has expired and whose refresh has also
		// failed gets a 401 from the very first fetch this screen makes. DRF
		// writes that as `{"detail": "..."}`, which is neither the hand-built
		// refusal envelope nor a coded one, so it would arrive on the blocked
		// frame as `oms: http 401: {"detail":"Authentication credentials were
		// not provided."}` — a raw dump on the frame whose whole job is
		// explaining why nothing can be received.
		//
		// This is not a second opinion about anything the server decides. It is
		// an HTTP fact with one operator-facing meaning and one next move, and
		// APIError.IsAuth is where that fact already lives. The client retries
		// once through a token refresh before this is ever reached, so getting
		// here means the refresh failed too.
		//
		// It NAMES NO KEY, and that is the correction of a real defect rather
		// than a shortening. The sentence used to end "then r re-reads the
		// worksheet", reasoning — as the paragraph above still does — about the
		// blocked frame. But receiveFailure is shared by all three failure
		// paths, and only two of six phases bind `r`: a 401 on the RECEIPT
		// leaves the screen on the review, where `r` answers "r does nothing
		// here"; a 401 on the WRITE-OFF leaves it on the confirm, where `r`
		// falls into the Reason box and types a letter into the free text that
		// gets recorded against the line — the screen's own diagnostic altering
		// what the operator is about to commit. This function is handed an
		// error and nothing else: it cannot see the frame, so it may not make a
		// claim about one. The way out is named where every other way out on
		// this screen is named — the ACTION BAR of the frame being drawn, which
		// sits directly under this line and is derived from the phase — and
		// "try again" is true on all three of them.
		if api.IsAuth() {
			return "this session is no longer signed in to OpenMakerSuite — " +
				"sign in again, then try again"
		}
		if api.Code != "" && api.Message != "" {
			return api.Message
		}
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *ReceiveFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	// A reply off the wire is what ENDS a freeze, so it is also what retires
	// the note the freeze wrote. The note answers the last keypress IN THE
	// FRAME THAT KEY WAS PRESSED AGAINST, and headerLines pins it on every
	// phase, so a decline that outlives its own state is a frame asserting
	// something the screen has stopped being true of: press any key while the
	// receipt is out and the note reads "j is frozen until the receipt answers ·
	// esc back to order"; when the reply lands the phase moves, whose bar is a
	// different bar, and that pinned sentence still claims a freeze that has
	// ended and still says esc goes back to the ORDER — contradicting the bar
	// about the one key it names. On the failure branch it is worse: the stale
	// warn line is drawn immediately above the fresh 502 detail with the bar
	// fully unfrozen again.
	//
	// Cleared HERE, once, rather than in the arms that clear pending and move
	// the phase — the same shape as the New PO screen's pendingLead, which is
	// cleared in Update's key dispatch and not in the three arms that navigate
	// (AGENTS.md). A clear per arm is a clear somebody adding the next branch
	// has to remember, and this defect fails silently.
	//
	// A RESIZE retires it too, and that is a third MESSAGE rather than a third
	// exception: the note is an answer about a FRAME, and a WindowSizeMsg
	// destroys the frame it was an answer about. It moves bodyRowsForBar, which
	// is the one input the paging claim is made of — so a 3-line order at 80x40
	// (body fits, the bar names no paging keys, pgdown declines with "pgdown
	// does nothing here") dragged down to 80x24 starts paging, the bar gains
	// PgUp/PgDn, and that pinned sentence sits immediately above a bar naming
	// the key it says does nothing. The next press then pages, so the key reads
	// as alternating between working and refusing.
	//
	// Re-deriving the way-out TAIL at render time instead — the New PO screen's
	// collapsed-cart idiom, where only the lead is kept — does not close this
	// one on its own: the LEAD is itself a claim about the frame ("pgdown does
	// nothing here"), so a re-derived tail would leave the note naming
	// pgup/pgdn as a way out in the same breath as saying pgdown does nothing.
	// What has stopped being true is the whole answer, not half of it, so the
	// whole answer goes.
	//
	// The rule is therefore not a COUNT of clearing points but what they have in
	// common: the note is retired by anything that ends the frame it answers
	// about — a reply off the wire, another keypress, or a resize. Nothing else
	// Update sees ends one. What falls through below is the cursor blink, which
	// moves a caret inside the frame rather than changing the frame's shape, and
	// a note retired by a blink would expire on a timer rather than on an event.
	//
	// The FAILURE line is deliberately not cleared with it: failHead/failDetail
	// are what came back off the wire rather than an answer to a keypress, and
	// they are the fact the operator most needs kept.
	switch msg.(type) {
	case receiveSheetMsg, receiveSubmittedMsg, receiveReopenedMsg, tea.WindowSizeMsg:
		s.note.clear()
	}

	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case receiveSheetMsg:
		return s, s.handleSheet(m)

	case receiveSubmittedMsg:
		return s, s.handleSubmitted(m)

	case receiveReopenedMsg:
		return s, s.handleReopened(m)

	case tea.KeyMsg:
		return s.handleKey(m)
	}

	// Anything that is not a key belongs to whichever box has the caret — the
	// cursor blink, chiefly. A frozen phase has no focused box, so nothing here
	// can move under a request in flight.
	if s.pending || s.loading {
		return s, nil
	}
	var cmd tea.Cmd
	box := s.currentInput()
	*box, cmd = box.Update(msg)
	return s, cmd
}

func (s *ReceiveFormScreen) handleKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The note answers the LAST keypress, so this press retires it and whatever
	// arm runs below writes the answer to THIS one. Cleared here, once, for the
	// reason the reply-side clear at the top of Update is written once: a note
	// that outlives the state it describes is a pinned line contradicting the
	// bar four rows under it, and headerLines draws it on every phase.
	//
	// Within the quantity phase that is two keystrokes away on the screen an
	// operator spends most of their time on. Press Enter on a fresh form and
	// the note reads "enter has nothing to receive — type a quantity against a
	// line"; type a 2 and qtyBarItems gains {Enter, Receive} because a box now
	// holds something, so the bar names Enter while the line above it says
	// Enter has nothing. The same shape holds for a claim about the CURSOR —
	// "pgup is already at the first row", then Down — and for the all-zero
	// refusal, which survived the operator typing a real quantity.
	//
	// Not per-arm. focusNext, pageQty and the typing fall-through would each
	// need their own clear, which is the hand-kept discipline this file's
	// history is made of: the arm somebody adds next is the one that forgets,
	// and it fails silently.
	//
	// The FAILURE line is not retired with it. failHead/failDetail came off the
	// wire rather than answering a keypress, and a gateway's reason must not be
	// dismissed by the operator pressing a key to look at it.
	//
	// The header is measured BEFORE the arms run, and every arm carries it, so
	// that a press is judged against ONE frame: the frame whose bar the operator
	// was reading when they pressed.
	//
	// The NOTE is not what makes that necessary. Since receiveNoteRows the note
	// block is a fixed allocation — noteLines returns exactly receiveNoteRows
	// entries in every state — so headerLines() is receiveNoteRows +
	// len(failDetailLines()) + 1 and retiring the note on the next line cannot
	// move it by a row. Reserving it unconditionally is what bought that, and
	// the reason is recorded there: a header that grew with the sentence let a
	// decline add PgUp/PgDn to the bar drawn under it, so the bar named a key
	// the next press refused.
	//
	// What headerRows still varies with is the reply-driven FAILURE DETAIL,
	// which an arm CAN retire mid-dispatch (submit's clearFail). It is
	// legitimately variable rather than self-referential: it comes off the wire,
	// it names no keys, and the bar and the guard see it identically — so a
	// frame that grows or loses one is a frame that genuinely changed, and
	// pinning the question to the frame the press was made against is what stops
	// an arm answering about the frame it is on its way to producing.
	headerRows := len(s.headerLines())
	s.note.clear()
	switch s.phase {
	case phaseLoading:
		return s.keyLoading(m, headerRows)
	case phaseBlocked:
		return s.keyBlocked(m, headerRows)
	case phaseSerial:
		return s.keySerial(m, headerRows)
	case phaseReview:
		return s.keyReview(m, headerRows)
	case phaseWriteOff:
		return s.keyWriteOff(m, headerRows)
	case phaseReopenPick:
		return s.keyReopenPick(m, headerRows)
	case phaseReopenConfirm:
		return s.keyReopenConfirm(m, headerRows)
	case phaseDone:
		return s.keyDone(m, headerRows)
	}
	return s.keyQty(m, headerRows)
}

// leave returns to the purchase order this screen was opened from.
func (s *ReceiveFormScreen) leave() tea.Cmd {
	return SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID()))
}

// ---------------------------------------------------------------------------
// The worksheet
// ---------------------------------------------------------------------------

func (s *ReceiveFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// loadSheet fetches the receiving worksheet.
//
// A screen built with no OMS client answers itself rather than dereferencing
// one: the fixtures this package builds for the pane-fit sweeps carry a bare
// Deps{}, and a command that panicked on them would take the whole test binary
// with it. The answer it gives is a FAILURE — could not tell — which is the
// honest one, and never an empty worksheet, which would read as an order with
// nothing on it.
func (s *ReceiveFormScreen) loadSheet() tea.Cmd {
	s.loading = true
	s.phase = phaseLoading
	s.blurAll()
	deps := s.deps
	id := s.poID()
	ctx := s.ctx()
	if deps.OMS == nil {
		return func() tea.Msg {
			return receiveSheetMsg{err: errors.New("no connection to OpenMakerSuite")}
		}
	}
	return func() tea.Msg {
		sheet, err := deps.OMS.GetReceivingWorksheet(ctx, id)
		return receiveSheetMsg{sheet: sheet, err: err}
	}
}

// handleSheet is the worksheet's reply.
//
// Three landings, and keeping them apart is the whole point: the fetch failed
// (could not tell — say so, offer `r`, and receive nothing); the order may not
// be received against (the server's own sentence, which is a different fact and
// a different next move for the operator); or the form is live.
func (s *ReceiveFormScreen) handleSheet(m receiveSheetMsg) tea.Cmd {
	s.loading = false
	s.rowCursor, s.reopenCursor = 0, 0
	if m.err != nil {
		s.phase = phaseBlocked
		s.sheet = nil
		// A correction held for this load is DROPPED rather than carried to
		// whatever load comes next: handleReopened has already flashed it, and
		// restating it minutes later on an `r` the operator pressed for another
		// reason would be an answer to a keypress they have long since made.
		s.reopened = ""
		head, detail := receiveFailure("Loading the receiving worksheet for "+s.orderName(), m.err)
		s.setFail(head, detail)
		return Status(head, StatusError)
	}
	s.clearFail()
	s.applyWorksheet(m.sheet)
	// A CORRECTION THAT LANDED IS RESTATED HERE, and this is the only place it
	// can be. Update retires the note on every reply off the wire, and the read
	// that PROVES the reopen is one — so the sentence is held on the screen
	// (handleReopened) and said once the worksheet it is about has arrived. It
	// takes the note rather than the routine summary because a write the
	// operator just made outranks a reload report they did not ask for.
	announce := s.reopened
	s.reopened = ""
	if !m.sheet.CanReceive {
		s.phase = phaseBlocked
		s.blurAll()
		if announce != "" {
			return s.say(announce, StatusOK)
		}
		return Status(s.orderName()+" cannot be received against", StatusWarn)
	}
	s.phase = phaseQty
	s.blurAll()
	s.focusCurrent()
	if announce != "" {
		return tea.Batch(s.say(announce, StatusOK), textinput.Blink)
	}
	return tea.Batch(Status(s.worksheetSummary(), StatusOK), textinput.Blink)
}

// worksheetSummary is what the load reports: the order's receiving state in the
// server's own words, and the work outstanding.
func (s *ReceiveFormScreen) worksheetSummary() string {
	w := s.sheet
	out := s.orderName() + " · " + w.StatusLabel
	out += fmt.Sprintf(" · %d %s outstanding", w.OutstandingLineCount,
		plural("line", w.OutstandingLineCount))
	if w.SerialsOutstanding > 0 {
		out += fmt.Sprintf(" · %d %s with no serial",
			w.SerialsOutstanding, plural("unit", w.SerialsOutstanding))
	}
	return out
}

// ---------------------------------------------------------------------------
// The keyboard, phase by phase
// ---------------------------------------------------------------------------

// keyLoading is the worksheet fetch's keyboard: esc, and nothing else.
//
// The freeze is an ALLOW-LIST for the reason every freeze on these screens is —
// written the other way round it freezes the keys somebody thought of and
// leaves every arm added later free by default. Esc is deliberately not gated:
// a frame with no way out while a slow gateway thinks is the worse defect.
func (s *ReceiveFormScreen) keyLoading(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if k == "esc" {
		return s, s.leave()
	}
	return s, s.declineFrozen(k, headerRows)
}

// keyBlocked is the frame for "no receipt can be built here". `r` fetches
// again, because both facts it draws can change under the operator — the
// gateway comes back, or somebody sends the draft — and the arrow keys walk the
// line list it draws when there is one.
func (s *ReceiveFormScreen) keyBlocked(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch k := m.String(); k {
	case "esc":
		return s, s.leave()
	case "r":
		return s, tea.Batch(s.loadSheet(), Status("Re-reading the worksheet for "+s.orderName()+"…", StatusInfo))
	case "ctrl+o":
		// Offered HERE and not only on the quantity form, because this is the
		// frame a settled order draws and `reopen-short/` is the one receiving
		// write accepted on one — see the section note on the correction.
		return s, s.openReopen(headerRows)
	case "up", "down":
		// Tab and Shift-Tab are deliberately NOT aliases here. They ride
		// alongside Up/Down on a sheet WITH FIELDS, where roughly twenty
		// columnar forms name the pair as UP/DN=Fields and naming the alias on
		// this one screen would make it disagree with all of them
		// (poFormNavAliases). This frame has no fields — it is a list of what
		// the order's lines say — so the exception does not reach it, and a key
		// that moved the cursor here while the bar named only UP/DN would be
		// the bar-honesty rule broken with no rule to appeal to.
		return s, s.moveRowCursor(k, s.blockedRows(), headerRows)
	case "pgup", "pgdown":
		return s, s.pageRowCursor(k, s.blockedBody(), s.blockedRows(), headerRows)
	default:
		return s, s.decline(k, headerRows)
	}
}

// keyQty is the quantity form's keyboard.
func (s *ReceiveFormScreen) keyQty(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.pending {
		if k == "esc" {
			return s, s.leave()
		}
		return s, s.declineFrozen(k, headerRows)
	}

	switch k {
	case "esc":
		return s, s.leave()
	case "enter":
		switch s.enterAction() {
		case receiveEnterFind:
			return s, s.findLine(headerRows)
		case receiveEnterReceive:
			return s.beginReceipt(headerRows)
		}
		// The bar does not name Enter here, so the press has to say why rather
		// than redraw an identical pane — and it says the useful thing rather
		// than the generic one, because what the operator has to do next
		// differs with the reason (entryRefusal).
		return s, s.say("enter has nothing to receive — "+s.entryRefusal()+" · "+
			s.waysOut(headerRows), StatusWarn)
	case "up", "shift+tab":
		s.focusNext(true, headerRows)
		return s, nil
	case "down", "tab":
		s.focusNext(false, headerRows)
		return s, nil
	case "pgup", "pgdown":
		return s, s.pageQty(k, headerRows)
	case "ctrl+k":
		return s, s.openLineWriteOff(headerRows)
	case "ctrl+r":
		return s, s.openOrderWriteOff(headerRows)
	case "ctrl+o":
		return s, s.openReopen(headerRows)
	}
	return s, s.typeInto(s.currentInput(), m, headerRows)
}

// receiveEnter is what Enter does on the quantity form, which is not one thing.
type receiveEnter int

const (
	// receiveEnterNothing — Enter can only refuse, so the bar must not name it.
	receiveEnterNothing receiveEnter = iota
	// receiveEnterFind — the cursor is in the scan box and it holds a code.
	receiveEnterFind
	// receiveEnterReceive — at least one quantity box holds something submit
	// will really attempt.
	receiveEnterReceive
)

// enterAction is the ONE predicate the bar and the Enter arm both read.
//
// FIND wins over RECEIVE when the cursor is in the scan box and the box holds
// something, and that ordering is the whole reason this is a function rather
// than a pair of ifs at the call site. A scanner fires a burst and then an
// Enter: if Enter receives while a code is still sitting unresolved in the box,
// the operator has booked a delivery instead of finding a line, on a screen
// where the difference is stock. With the box empty there is nothing to find,
// so Enter means what it means everywhere else on the form.
func (s *ReceiveFormScreen) enterAction() receiveEnter {
	if s.focused == receiveRowScan && strings.TrimSpace(s.scan.Value()) != "" {
		return receiveEnterFind
	}
	if s.entryState() == receiveAttemptable {
		return receiveEnterReceive
	}
	return receiveEnterNothing
}

// receiveEntry is what the quantity boxes amount to: the answers Enter can give,
// which are different sentences and not one.
type receiveEntry int

const (
	// receiveNoLines — every line is voided or closed short, so none can take a
	// receipt.
	receiveNoLines receiveEntry = iota
	// receiveNothingTyped — receivable lines, every box empty.
	receiveNothingTyped
	// receiveAllZero — boxes were typed into and every one of them parses as 0.
	receiveAllZero
	// receiveAttemptable — at least one box holds something submit will really
	// attempt, which includes a value the server or the parser will reject.
	receiveAttemptable
)

// entryState classifies the boxes.
//
// The distinction that matters is between a value that leaves the terminal and
// one that cannot. The predicate this replaced was "any box holds something",
// written reasoning about TYPOS: a box holding "two" is an attempt, and the
// refusal naming the line (quantityRefusal's "line 1: a quantity is a whole
// number, 0 or more — \"two\" is not") is the answer to that attempt, so Enter
// is named and Enter acts. That reasoning is right and it does not extend to a
// ZERO — which that sentence accepts, and which receiveQuantity parses for the
// same reason. A box holding "0"
// is skipped, leaves nothing to post, and comes straight back as a local
// refusal — so the bar named a key whose whole effect was to write a note,
// which is the bar-honesty rule broken on the phase the operator lives on. "0"
// stays out of the ATTEMPT and stays in hasQuantityEntry, because it is still
// something Esc would throw away.
func (s *ReceiveFormScreen) entryState() receiveEntry {
	if len(s.qty) == 0 {
		return receiveNoLines
	}
	typed := false
	for _, ti := range s.qty {
		raw := strings.TrimSpace(ti.Value())
		if raw == "" {
			continue
		}
		typed = true
		if n, err := strconv.Atoi(raw); err == nil && n == 0 {
			continue
		}
		return receiveAttemptable
	}
	if typed {
		return receiveAllZero
	}
	return receiveNothingTyped
}

// entryRefusal is WHY Enter cannot receive, in the words that fit the state.
//
// The three stay DISTINCT because they are three different facts and the
// operator's next move differs for each: nothing typed says type a quantity, a
// pad of zeroes says the zeroes are the problem, and an order with nothing
// receivable says no keystroke on this screen will help. Collapsing them into
// one sentence would leave an operator unable to tell which refusal they had
// hit.
//
// One home for the wording, read by the Enter arm and by beginReceipt's own
// backstop, so a screen that refuses in two places cannot refuse in two voices.
func (s *ReceiveFormScreen) entryRefusal() string {
	switch s.entryState() {
	case receiveNoLines:
		return "no line on this order can take a receipt"
	case receiveAllZero:
		return "every quantity entered is zero"
	}
	return "type a quantity against a line"
}

// hasQuantityEntry reports whether any quantity box holds something. It is part
// of what Esc's label is decided by — leaving destroys the screen and takes a
// typed zero with it exactly as it takes a typed 2 — and is NOT what decides
// whether the bar names Enter. enterAction answers that.
func (s *ReceiveFormScreen) hasQuantityEntry() bool {
	for _, ti := range s.qty {
		if strings.TrimSpace(ti.Value()) != "" {
			return true
		}
	}
	return false
}

// anythingTyped reports whether Esc would throw entry away — every box on the
// form and every serial captured so far, because Esc destroys the screen and
// takes all of it with it.
func (s *ReceiveFormScreen) anythingTyped() bool {
	if s.hasQuantityEntry() {
		return true
	}
	for _, box := range []textinput.Model{s.scan, s.tracking, s.carrier, s.delivered, s.notes} {
		if strings.TrimSpace(box.Value()) != "" {
			return true
		}
	}
	for _, c := range s.captures {
		if !c.empty() {
			return true
		}
	}
	return false
}

// focusNext walks the quantity form's rows, wrapping at both ends — and
// DECLINING outright on a pane the layer refuses to draw this frame into.
//
// headerRows is the pinned header of the frame the key was pressed ON, for the
// reason pageQty's comment gives: the guard and the bar the operator read have
// to be the same expression over the same frame. On a refused pane there is no
// row on screen to move the caret to, and moving it anyway leaves the operator
// typing into a different box when the terminal grows back.
func (s *ReceiveFormScreen) focusNext(reverse bool, headerRows int) {
	delta := +1
	if reverse {
		delta = -1
	}
	next, ok := s.moveRow(s.focused, s.totalInputs(), delta, headerRows, s.barFor(headerRows))
	if !ok {
		return
	}
	s.currentInput().Blur()
	s.focused = next
	s.focusCurrent()
}

// pageQty moves the cursor a paneful at a time.
//
// Both halves are measured against `headerRows` — the pinned header of the
// frame the operator pressed the key ON — so the guard here and the bar they
// read are the same expression over the same frame, and the step moves by
// exactly the rows that frame drew. Re-deriving the header here instead would
// answer about whatever frame this dispatch is on its way to producing: the
// note cannot move it (its rows are reserved unconditionally), but the
// reply-driven failure detail can, and an arm that retires one mid-dispatch
// would leave the guard describing a taller window than the operator was
// looking at (handleKey carries the reasoning).
func (s *ReceiveFormScreen) pageQty(k string, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		// The pane is too short for the layer to draw this frame at all: there
		// is no window for a page to move through, and no row on which to say
		// so. A note written here would not be drawn now and WOULD be drawn
		// when the terminal grows back, answering a press the operator made
		// before the resize.
		return nil
	}
	if !s.qtyPagesFor(headerRows) {
		return s.decline(k, headerRows)
	}
	dir := +1
	if k == "pgup" {
		dir = -1
	}
	next := jdePageCursor(s.focused, s.totalInputs(), s.qtyStepFor(headerRows), dir)
	if next == s.focused {
		// Resting against an edge the page cannot move past. The window's own
		// "↑ more above" / "↓ more below" markers are absent there, so the
		// frame has already answered — but the highlight has NOT moved, so a
		// second press would redraw the same pane. Say which edge.
		return s.say(k+" is already at "+receiveEdge(dir)+" · "+s.waysOut(headerRows), StatusInfo)
	}
	s.currentInput().Blur()
	s.focused = next
	s.focusCurrent()
	return nil
}

// receiveEdge names the end of a list a page could not move past.
func receiveEdge(dir int) string {
	if dir < 0 {
		return "the first row"
	}
	return "the last row"
}

// moveRowCursor walks a READ-ONLY body's cursor. It declines by name on a body
// with nothing to walk, because a movement key that silently does nothing on a
// frame with no caret is the wedged-program reading this screen is written
// against.
func (s *ReceiveFormScreen) moveRowCursor(k string, rows, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return nil // refused pane — see pageQty
	}
	if rows < 2 {
		return s.decline(k, headerRows)
	}
	// UP and nothing else. Shift-Tab used to be tested for here and could not
	// be reached: both callers route only `case "up", "down"`, because the
	// Tab/Shift-Tab alias belongs to sheets WITH FIELDS and these two frames are
	// read-only lists (keyBlocked's arm carries the full note). A condition
	// testing for a key its callers never deliver reads as a binding the frames
	// honour and the bars do not name, which is the bar-honesty rule broken in
	// the source rather than on the pane — and the next author would have
	// believed it.
	if k == "up" {
		s.rowCursor = (s.rowCursor + rows - 1) % rows
	} else {
		s.rowCursor = (s.rowCursor + 1) % rows
	}
	return nil
}

// pageRowCursor is pageQty for a read-only body.
func (s *ReceiveFormScreen) pageRowCursor(k string, body *jdeLines, rows, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return nil // refused pane — see pageQty
	}
	if !s.rowsPageFor(body, rows, headerRows) {
		return s.decline(k, headerRows)
	}
	dir := +1
	if k == "pgup" {
		dir = -1
	}
	step := s.windowRowsForBar(body, s.rowCursor, headerRows, s.barCeiling())
	next := jdePageCursor(s.rowCursor, rows, step, dir)
	if next == s.rowCursor {
		return s.say(k+" is already at "+receiveEdge(dir)+" · "+s.waysOut(headerRows), StatusInfo)
	}
	s.rowCursor = next
	return nil
}

// rowsPageFor is qtyPagesFor for a read-only body: BOTH that the body overflows
// and that a page has somewhere to land, because those two questions agree in
// almost every state and come apart in the one that is designed — a body of one
// row that is still taller than the pane.
//
// Both halves are the layer's (bodyPagesForBar). It is asked HERE rather than
// left to pageRow because the arm behind it answers "nothing to page" out loud
// and a refused pane in silence, and pageRow returns the same false for both.
func (s *ReceiveFormScreen) rowsPageFor(body *jdeLines, rows, headerRows int) bool {
	return s.bodyPagesForBar(body, rows, headerRows, s.barCeiling())
}

// ---------------------------------------------------------------------------
// Scanning a line
// ---------------------------------------------------------------------------

// receiveScanKindLabel turns a scan_codes kind into words an operator reads.
//
// A table rather than the raw token, because "package_upc" on the pane is the
// wire talking to itself, and the DEFAULT is the token rather than a guess: a
// kind this build has not seen is reported as it arrived, which is honest,
// instead of being flattened into "code" and losing the one fact the row adds.
func receiveScanKindLabel(kind string) string {
	switch kind {
	case omsapi.ScanCodeItemSKU:
		return "our SKU"
	case omsapi.ScanCodePackageUPC:
		return "box barcode"
	case omsapi.ScanCodeUnitUPC:
		return "unit barcode"
	case omsapi.ScanCodeSupplierSKU:
		return "supplier's number"
	}
	return kind
}

// receiveScanMatch is one line a scanned code resolved to.
//
// It carries ONE index and that index is the form's: the position in s.lines,
// which is what every number the operator reads on this screen is counted
// over — lineHeading's `1 `, openLineWriteOff's refusal, the review's blocks.
// It used to carry a second one, the position in s.sheet.Lines, and the two
// disagree the moment a settled line sits ahead of a live one, which is the
// ordinary shape of the partial-receipt flow this screen exists for: an order
// of [#11 voided, #12, #13] draws #13 as line 2 and the scan note called it
// line 3. On the multi-match path that note also said "up/dn walks there", so
// following it walked the operator to a DIFFERENT line, on the screen whose
// whole job is booking stock against the right one. A second index is a second
// authority; there is now one.
type receiveScanMatch struct {
	// EXACTLY ONE of these is set, and each is an index into a list the form
	// really draws: s.lines, whose entries carry the quantity boxes, and
	// s.closed, whose entries are listed under "N settled lines cannot take a
	// receipt". A match is in one or the other because applyWorksheet puts
	// every line in one or the other.
	line    int // index into s.lines, or -1
	closed  int // index into s.closed, or -1
	label   string
	kind    string
	settled string // why it cannot take a receipt, blank when it can
}

// live reports a match the form has a QUANTITY BOX for — the only kind a
// receipt can be typed against.
func (m receiveScanMatch) live() bool { return m.line >= 0 }

// row is where the cursor goes for a live match, and number is what the form
// DRAWS beside it. Both are derived from the one index rather than stored, so
// they cannot come apart from each other or from the body that draws the line.
func (m receiveScanMatch) row() int    { return receiveRowFirstLine + m.line }
func (m receiveScanMatch) number() int { return m.line + 1 }

// settledNumber is what the SETTLED list draws beside this match. A separate
// numbering from number() because it is a separate list with a separate count,
// which is exactly why a note may not say "line N" about one of these.
func (m receiveScanMatch) settledNumber() int { return m.closed + 1 }

// scanMatches resolves a code against the worksheet's own scan_codes.
//
// It is matched against EVERY line of the order and not only the receivable
// ones, because "that code is line 4, which was closed short" and "no line on
// this order carries that code" are different facts and the operator's next
// move differs: one is a box that should not have come, the other is a box
// whose label they should re-read.
//
// Comparison is case-insensitive on the trimmed code. A scanner delivers what
// is printed, and an operator TYPING a SKU types it in whatever case is on the
// paperwork; the server's own codes are stored as entered, so a case-sensitive
// match here would refuse a correct identifier for a reason nothing on the pane
// could explain.
func (s *ReceiveFormScreen) scanMatches(code string) []receiveScanMatch {
	want := strings.ToLower(strings.TrimSpace(code))
	if want == "" || s.sheet == nil {
		return nil
	}
	// Keyed by the line's id and valued by its place in s.lines, because that
	// is the number the form draws. Walking s.sheet.Lines is still right — a
	// settled line has to be findable, and it is only in the worksheet — but
	// its INDEX there is not a fact the operator can see anywhere.
	lineOf, closedOf := map[string]int{}, map[string]int{}
	for i, l := range s.lines {
		lineOf[fmt.Sprint(l.sheet.PurchaseOrderItem)] = i
	}
	for i, l := range s.closed {
		closedOf[fmt.Sprint(l.sheet.PurchaseOrderItem)] = i
	}
	var out []receiveScanMatch
	for _, l := range s.sheet.Lines {
		for _, c := range l.ScanCodes {
			if strings.ToLower(strings.TrimSpace(c.Code)) != want {
				continue
			}
			m := receiveScanMatch{line: -1, closed: -1, label: l.Label, kind: c.Kind}
			switch i, ok := lineOf[fmt.Sprint(l.PurchaseOrderItem)]; {
			case ok:
				m.line = i
			default:
				m.closed = closedOf[fmt.Sprint(l.PurchaseOrderItem)]
				if l.IsVoided {
					m.settled = "struck off the order"
				} else {
					m.settled = "closed short"
				}
			}
			out = append(out, m)
			break // one line matches once, however many of its codes match
		}
	}
	return out
}

// findLine is Enter in the scan box: move the cursor to the line the code names.
//
// It lands on the FIRST live match and says what the others are. Two lines of
// one order really can carry the same code — the same part ordered twice, on
// two lines with different expected dates — so the operator does have to be
// able to reach the second, and receiveOtherLineNote carries the note on why
// the key that reaches it is up/dn and not another Enter.
func (s *ReceiveFormScreen) findLine(headerRows int) tea.Cmd {
	// TWO names for the code, and the split is the point. `code` is what the
	// worksheet is MATCHED against and never reaches a sentence; `shown` is
	// what every sentence names, bounded in cells at the one place it is read
	// out of the box, so a note added below is bounded by construction rather
	// than by whoever remembers to wrap it.
	//
	// It matters because s.scan carries a CharLimit of 120 against a note
	// budget of receiveNoteRows lines of the pane, and the note is shortened
	// FROM THE END — where the tail naming the way out lives. A 90-character
	// GS1 string that matched nothing used to spend the whole budget on itself
	// and leave "pick the line with up/dn" cut off: the only instruction the
	// frame gives, lost to the value that caused the refusal. The OMS-supplied
	// label in the same sentence has been bounded since it was written
	// (receiveScanClip); a bound over half a sentence is not a bound.
	code := strings.TrimSpace(s.scan.Value())
	shown := receiveRefusalClip(code)
	matches := s.scanMatches(code)
	if len(matches) == 0 {
		return s.say(s.noScanMatchNote(shown)+" · "+s.waysOut(headerRows), StatusWarn)
	}

	var live []receiveScanMatch
	for _, m := range matches {
		if m.live() {
			live = append(live, m)
		}
	}
	if len(live) == 0 {
		// Named by its place in the SETTLED list, not by its label and not by a
		// form line number.
		//
		// A form number is out: a settled line has no quantity box, and the
		// receivable lines are numbered 1..n of their own, so "line 2" in this
		// sentence would point an operator at a line they CAN receive against —
		// the worst possible miss on the screen that books stock.
		//
		// The LABEL was the first answer and it does not survive the budget.
		// This sentence carries the way-out tail, so its values live inside
		// receiveScanRefusalRoom, and ten cells of "Backordered gasket, 10mm"
		// and ten cells of "Backordered gasket, 12mm" are the same ten cells:
		// an operator holding one of two boxes could not tell which line the
		// screen meant. Widening the clip is not available — the arithmetic at
		// receiveRefusalClip is what keeps the tail on the pane.
		//
		// The settled list's own number costs three cells, cannot be ambiguous,
		// and points at a row that draws the label IN FULL beside the state.
		// addClosedLines names that list "settled" for this sentence to refer
		// to, and "settled" is already this screen's word for these lines (the
		// multi-match tail below says "N settled lines carry it too").
		m := matches[0]
		return s.say(fmt.Sprintf("%q is settled line %d, %s — no receipt · %s",
			shown, m.settledNumber(), m.settled, s.waysOut(headerRows)), StatusWarn)
	}

	hit := live[0]
	s.currentInput().Blur()
	s.focused = hit.row()
	s.focusCurrent()

	lead := fmt.Sprintf("%q is line %d, %s (%s)", shown, hit.number(),
		receiveScanClip(hit.label), receiveScanKindLabel(hit.kind))
	switch {
	case len(live) > 1:
		lead += " · " + receiveOtherLineNote(live, hit)
	case len(matches) > len(live):
		lead += fmt.Sprintf(" · %d settled %s carry it too",
			len(matches)-len(live), plural("line", len(matches)-len(live)))
	}
	return s.say(lead, StatusOK)
}

// receiveOtherLineNote names the OTHER lines a scanned code resolved to, and
// the key that actually reaches them.
//
// It used to read "N lines carry it, enter finds the next", which was a claim
// the code did not honour in either half. Enter cannot mean FIND from a line
// row: enterAction returns receiveEnterFind only while the cursor is in the
// SCAN box, and findLine has just moved it onto a line — so the operator told
// to press Enter again was taken to the REVIEW of a receipt instead of to the
// second match, on a screen where that difference is stock. The cycling loop
// behind the sentence could not fire either, for the same reason: it looked for
// the focused row among the matches and the focused row was the scan box, so
// `next` was always 0 and no key on the screen reached the second line.
//
// Enter is not the key to fix that with. Enter from a line row has to go on
// committing into the review — that is the whole entry half of this phase — so
// what changes is the SENTENCE: it names up/dn, which the bar names, which
// focusNext honours from every row of the form, and which walks onto the lines
// the note has just listed.
//
// The positions are LISTED while the list is short and COUNTED once it is not.
// The note is folded into receiveNoteRows lines and shortened FROM THE END, and
// the end is where the way out is named — a run of line numbers is unbounded in
// the number of lines an order can have, and a bound expressed in terms of an
// unbounded value is not a bound.
func receiveOtherLineNote(live []receiveScanMatch, hit receiveScanMatch) string {
	var others []string
	for _, m := range live {
		if m.line == hit.line {
			continue
		}
		others = append(others, strconv.Itoa(m.number()))
	}
	if len(others) > receiveScanListMax {
		return fmt.Sprintf("%d more lines carry it — up/dn walks to them", len(others))
	}
	verb := "carries"
	if len(others) > 1 {
		verb = "carry"
	}
	return fmt.Sprintf("%s %s %s it too — up/dn walks there",
		plural("line", len(others)), strings.Join(others, ", "), verb)
}

// receiveScanListMax is how many other positions the note spells out before it
// gives up and counts them instead. Three is what a 51-column pane holds beside
// the lead and the way-out tail; the fourth is what pushes the tail off, and
// the tail is the half that names the key.
const receiveScanListMax = 3

// receiveScanClip bounds an OMS-supplied line label before it goes into the
// scan's SUCCESS note.
//
// The note is folded into receiveNoteRows lines and shortened from the end when
// it overruns, and the end is where the tail naming the way out lives — so a
// label of any length must not be what spends that budget. Twenty cells is
// enough to recognise a part and small enough that the sentence around it
// survives. cellPrefix rather than a rune count, because every width on these
// screens is measured in CELLS.
//
// Every value that goes into a REFUSAL is bounded harder, and receiveRefusalClip
// is where the arithmetic for that is.
func receiveScanClip(label string) string { return receiveNoteClip(label, receiveScanLabelRoom) }

// receiveRefusalClip is the same bound, tighter, for a value going into a note
// that carries a WAY-OUT TAIL.
//
// The tail is what decides, and it is most of the budget: waysOut spells the
// quantity form's whole bar, five claims and about ninety cells of the roughly
// hundred and sixty a four-row note really holds once folding waste is paid.
// A refusal lead therefore has room for about seventy, and the SCANNED CODE is
// the only part of it that is not fixed text — findLine is the one caller left,
// since the settled branch stopped naming a label at all and names the settled
// list's own number instead. Measured with a 91-character GS1 code, the
// twenty-cell bound above overran the reservation and fittedNote dropped
// "received" off the end of "ctrl+r mark received": the key that finishes the
// order, lost to the value that caused the refusal.
//
// A quoted code costs more than its cells, which is the other half of why this
// is not one number with the lead above. The fold breaks at WORDS, and a quoted
// code is one unbreakable word — where "Backordered gasket" splits across the
// break, "0195012345…" cannot — so it pushes a whole segment down a line.
//
// The SUCCESS lead keeps the twenty-cell bound because it carries no tail at
// all: `say(lead, StatusOK)` names no keys, so its whole budget is the
// sentence, and there a longer part name is worth having.
func receiveRefusalClip(v string) string { return receiveNoteClip(v, receiveScanRefusalRoom) }

const (
	receiveScanLabelRoom   = 20
	receiveScanRefusalRoom = 10
)

func receiveNoteClip(v string, room int) string {
	if lipgloss.Width(v) <= room {
		return v
	}
	return cellPrefix(v, room-1) + "…"
}

// linePickWayOut is how an operator reaches a LINE from the quantity form — or
// the ORDER's way out, when there is no line to reach.
//
// It exists because the tail is the half that keeps being wrong. "Pick the line
// with up/dn" assumes there is a line to pick, and on an order every line of
// which is voided or closed short there is not: applyWorksheet puts all of them
// in s.closed, qtyBody draws the scan row before its own empty branch, and up/dn
// then walks the fixed scan/tracking/carrier/delivered/notes rows and reaches no
// line at all. That state is REACHABLE and newly so — `can_receive: true`
// alongside `outstanding_line_count: 0` is a real answer, and the contract asks
// a client to say so and point at voiding or cancelling the ORDER rather than
// leaving a dead end.
//
// It is a function rather than a sentence repeated in four places because the
// fix has now been applied twice to the site that was reported and not to its
// siblings: scanHint was corrected first and noScanMatchNote, two functions
// away, carried the identical tail in the identical state for another round.
// Every sentence on this screen that promises a line can be reached reads this
// one, and TestReceive_NoSentencePointsAtAnEmptyLinePicker sweeps the key space
// against a settled-only order so a fifth sentence cannot be added past it.
func (s *ReceiveFormScreen) linePickWayOut() string {
	if len(s.lines) == 0 {
		return "void or cancel the ORDER to finish with it"
	}
	return "pick a line with up/dn"
}

// noScanMatchNote says WHICH kind of nothing was found.
//
// "No line carries that code" and "no line on this order carries any code at
// all" are different facts: the first sends the operator back to the label, the
// second tells them this order simply cannot be scanned to and they must pick
// the line by hand. An asset or freeform line contributes no codes at all, so
// an order made of those is exactly the second case and is not rare.
//
// `code` arrives already bounded in cells — findLine clips it once, where it is
// read out of the box — because both sentences below interpolate it beside the
// instruction that gets the operator out, and the instruction is what a long
// code used to push off the pane.
//
// The last branch says "matches no line" and not "matches no line here", and
// that word was given up rather than the reservation raised: naming the way out
// through linePickWayOut costs cells the old fixed "pick with up/dn" did not,
// and with a 91-character GS1 code in front of it the sentence folded to five
// lines against receiveNoteRows' four — so what a cut took was the tail naming
// the keys. "Here" was the cheapest thing in it that the frame already says.
func (s *ReceiveFormScreen) noScanMatchNote(code string) string {
	if len(s.lines) == 0 {
		// A THIRD nothing, and it outranks both of the others: there is no line
		// to find, so "check the label" is advice about a search that could not
		// have succeeded whatever was on the label. The code is not named for
		// the same reason the branch below does not name it.
		return "no line on this order can take a receipt — " + s.linePickWayOut()
	}
	if s.codedLines() == 0 {
		// The CODE is not named in this one, and that is deliberate rather than
		// a saving: the fact is about the ORDER, not about what was scanned —
		// no code whatever could find a line here — so naming it would spend
		// the tail's cells saying something the sentence does not turn on.
		return "no line here carries a scannable code — " + s.linePickWayOut()
	}
	return fmt.Sprintf("%q matches no line — check the label, or %s",
		code, s.linePickWayOut())
}

// codedLines is how many lines of the ORDER carry a scannable identifier at
// all, settled ones included.
//
// It is a fact about the order rather than about the receivable half of it, and
// both sentences that ask it need it that way: the scan hint uses it to tell
// "nothing here carries a code" apart from "the codes here are all on settled
// lines", and the no-match note uses it to tell "that code is wrong" apart from
// "no code could have worked". One walk, because two copies of a count is two
// answers waiting to disagree — which is the defect the hint's own count was
// just corrected for.
func (s *ReceiveFormScreen) codedLines() int {
	if s.sheet == nil {
		return 0
	}
	n := 0
	for _, l := range s.sheet.Lines {
		if len(l.ScanCodes) > 0 {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Serial capture
// ---------------------------------------------------------------------------

// enrol derives the capture slots this receipt would open, and reports how many
// slots the operator had already typed into that no longer exist.
//
// The identities come from the worksheet's `serial_targets` and from nothing
// else. That is the whole of the kit rule: on a kit line those targets are the
// kit's serialized COMPONENTS, at quantity_per_kit × ordered, and the kit's own
// id never appears — so a serial can only ever be offered against something the
// receipt really credits. Reading the line's own `item_details.is_serialized`
// instead would ask about the KIT on a kit line, and a serial written against a
// kit names a unit that can never be drawn down: the corruption path the old
// ban on serialized kit components existed to close, reached from the one
// direction the ban never covered.
//
// The COUNT is the server's published scaling of that target to the quantity
// being received (omsapi.SerialTarget.Units), so the list offered here is the
// list the receipt accepts. Over-supplying is a 400 naming the count, never a
// truncation, so an enrolment that got this wrong would be reported rather than
// silently swallowed.
func (s *ReceiveFormScreen) enrol() ([]serialUnit, []receiveCapture, int) {
	var units []serialUnit
	for i, line := range s.lines {
		qty, ok := receiveQuantity(s.qty[i].Value())
		if !ok || qty <= 0 {
			continue
		}
		for _, target := range line.sheet.SerialTargets {
			n := target.Units(line.sheet.QuantityOrdered, qty)
			for u := 1; u <= n; u++ {
				units = append(units, serialUnit{
					lineIdx:   i,
					poItemID:  line.sheet.PurchaseOrderItem,
					lineLabel: line.sheet.Label,
					itemID:    target.Item,
					itemName:  target.ItemName,
					itemSKU:   target.ItemSKU,
					unitNo:    u,
					unitTot:   n,
				})
			}
		}
	}

	// What was typed survives wherever the slot survives. A quantity edited
	// elsewhere on the form must not quietly take serials with it — and where a
	// slot genuinely goes, the loss is COUNTED so the note can say so rather
	// than the operator discovering it on the review.
	held := map[string]receiveCapture{}
	for i, u := range s.serialUnits {
		if i < len(s.captures) && !s.captures[i].empty() {
			held[u.key()] = s.captures[i]
		}
	}
	captures := make([]receiveCapture, len(units))
	kept := 0
	for i, u := range units {
		if c, ok := held[u.key()]; ok {
			captures[i] = c
			kept++
		}
	}
	return units, captures, len(held) - kept
}

// receiveQuantity parses a quantity box. ok is false for anything that is not a
// whole number of ZERO or more, so the refusal naming the line can be written
// once (quantityRefusal) and read the same way by the enrolment and by the
// payload.
//
// Zero parses. It is not a quantity anybody would send, and it never reaches
// the wire — buildReceipt and plannedLines both drop a line whose quantity is
// not above zero — but it is a whole number, and refusing it HERE would make
// the parser and the sentence beside it disagree: quantityRefusal says "a
// quantity is a whole number, 0 or more", which is what an operator reads when
// they type "two". What a typed zero really means to this screen is answered
// one level up, by entryState, which is a different question from whether the
// characters parse.
func receiveQuantity(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// quantityRefusal is the first line whose box holds something submit cannot
// send, in words naming the line. Blank when every box is fine.
func (s *ReceiveFormScreen) quantityRefusal() string {
	for i := range s.qty {
		raw := strings.TrimSpace(s.qty[i].Value())
		if raw == "" {
			continue
		}
		if _, ok := receiveQuantity(raw); !ok {
			return fmt.Sprintf("line %d: a quantity is a whole number, 0 or more — %s is not",
				i+1, poQuotedClip(raw, receiveQuotedCells))
		}
	}
	return ""
}

// receiveQuotedCells and receiveQuotedSerialCells bound what the operator TYPED
// when a refusal or a warning quotes it back to them, quotes included.
//
// They are the room the old bounds came to — cellPrefix(v, 12) and
// cellPrefix(v, 16), then quoted — and the cut is poQuotedClip's now, because
// the old shape cut the value and THEN closed the quote around it, and a
// closing quote is a claim that the value ends there. The duplicate-serial
// warning drew a 23-character serial as `"C02XK1ABJG5J-202" is already on unit
// 1`: a serial nobody typed, presented whole, on the screen whose entire job is
// recording serials exactly. poQuotedClip carries the ellipsis INSIDE the quote
// (AGENTS.md: the closing quote is positional, and a value mid-sentence
// re-appends it out of the room the clip was given), so the operator can see
// both that the value was shortened and where their typing stops.
//
// The serial box takes 200 characters, so that cut is reachable with ordinary
// data. The quantity (8) and date (10) boxes stop short of 12 characters, so
// theirs is reached by width rather than by count: eight full-width digits —
// what a Japanese IME types for "12345678" — are 16 cells.
const (
	receiveQuotedCells       = 14
	receiveQuotedSerialCells = 18
)

// beginReceipt is Enter on the quantity form: check what was typed, work out
// which units want serials, and go to whichever step comes next.
//
// It never posts. The receipt goes out from the REVIEW phase and from nowhere
// else, so that the last thing an operator sees before stock moves is what the
// server is about to be told — including the over-receipt they may have typed
// by mistake, which the contract asks a client to raise before sending and not
// after.
func (s *ReceiveFormScreen) beginReceipt(headerRows int) (Screen, tea.Cmd) {
	if bad := s.quantityRefusal(); bad != "" {
		return s, s.say(bad+" · "+s.waysOut(headerRows), StatusError)
	}
	if s.entryState() != receiveAttemptable {
		// The BACKSTOP, not the ordinary path: the bar stops naming Enter the
		// moment enterAction leaves receiveEnterReceive, and the arm above
		// refuses before this is reached. It is kept because the refusal must
		// not depend on two predicates agreeing, and it reads its sentence off
		// entryRefusal so the screen cannot refuse the same fact in two voices.
		return s, s.say("nothing to receive — "+s.entryRefusal(), StatusWarn)
	}

	units, captures, dropped := s.enrol()
	s.serialUnits, s.captures, s.dropped = units, captures, dropped
	if len(units) == 0 {
		return s, s.toReview(headerRows, "", StatusInfo)
	}
	// The note counts what is LEFT, and both of its branches are read off that
	// ONE number so they cannot disagree with each other or with the frame.
	//
	// Re-entry is an ordinary route, not a corner: capture a serial, land on the
	// review, press Esc back to the quantities to double-check a count, press
	// Enter again. enrol carries the captures across by identity, so the queue
	// comes back the same LENGTH with less work in it — and `len(units)` is the
	// length, not the work. Announcing it said "3 serialized units to capture"
	// over a body two rows down reading "capture 2 of 3 · 1 serial(s) so far":
	// two rows of one pane giving different counts of the same thing, on the
	// phase whose whole job is tracking exactly that.
	//
	// capturesLeft is also what decides WHICH frame this opened. It is zero
	// exactly when firstUncaptured walks off the end, which is when toSerial
	// draws serialBody's past-the-end branch ("Every unit has been answered") —
	// so the answered sentence and the outstanding one are two readings of one
	// count rather than two conditions somebody has to keep in step. Fixing
	// only the branch that was reported is what left this one wrong.
	//
	// Every other sentence on this screen that counts capture slots is already
	// an X-of-Y — serialBody's "capture i of N", commitUnit's "N of M captured",
	// captureSummary's "N of M units carry a serial", reviewLead's "N of M
	// serials captured" — and this was the one bare count among them. It is
	// X-of-Y now too, so the shape says which number is which.
	at, left := s.firstUncaptured(), s.capturesLeft()
	s.toSerial(at)
	lead := fmt.Sprintf("%d of %d serialized %s to capture",
		left, len(units), plural("unit", len(units)))
	if left == 0 {
		// The way out comes off the bar, as every decline's does: this frame
		// binds a different set from the capture frame (there is no box to type
		// into), and a fixed sentence here would be the same claim-about-a-
		// frame-it-cannot-see the auth detail was corrected for.
		lead = fmt.Sprintf("all %d %s already answered · %s",
			len(units), plural("unit", len(units)), s.waysOut(headerRows))
	}
	if dropped > 0 {
		lead += fmt.Sprintf(" · %d captured %s no longer fit the quantities and were dropped",
			dropped, plural("serial", dropped))
	}
	return s, s.say(lead, StatusInfo)
}

// firstUncaptured is where capture opens: the first slot nothing has been typed
// into, or the end when every slot has an answer.
func (s *ReceiveFormScreen) firstUncaptured() int {
	for i, c := range s.captures {
		if c.empty() {
			return i
		}
	}
	return len(s.captures)
}

// capturesLeft is how many slots are still empty — the same walk firstUncaptured
// makes, counted rather than stopped at.
//
// It is the count a sentence about outstanding work must use, because the QUEUE
// LENGTH is not the work: a re-entry carries captured serials across by
// identity, so len(serialUnits) stays put while the work in it falls. The two
// answers agree only on a fresh enrolment, which is exactly why naming the
// wrong one survived — it is right until the operator walks back.
//
// It is also the predicate for "is there anything left at all": zero here is
// firstUncaptured walking off the end, which is the frame serialBody draws as
// "Every unit has been answered". One count, so the note and the frame cannot
// come apart.
func (s *ReceiveFormScreen) capturesLeft() int {
	n := 0
	for _, c := range s.captures {
		if c.empty() {
			n++
		}
	}
	return n
}

// toSerial moves to serial capture with the cursor on unit i, loading whatever
// that unit already holds into the boxes.
func (s *ReceiveFormScreen) toSerial(i int) {
	s.phase = phaseSerial
	s.serialCursor = i
	s.serialField = receiveSerialNumber
	s.loadUnit()
	s.blurAll()
	s.focusCurrent()
}

// loadUnit fills the three capture boxes from the slot the cursor is on. Past
// the end of the queue they are emptied, because the frame there draws no boxes
// and a value left in one would be sent by a later enrolment nobody typed it
// into.
func (s *ReceiveFormScreen) loadUnit() {
	c := receiveCapture{}
	if s.serialCursor >= 0 && s.serialCursor < len(s.captures) {
		c = s.captures[s.serialCursor]
	}
	s.serialInput.SetValue(c.serial)
	s.lotInput.SetValue(c.lot)
	s.expiryInput.SetValue(c.expiry)
}

// storeUnit writes the three boxes back into the slot the cursor is on — or
// REFUSES, and writes nothing.
//
// Every arm that leaves the slot calls it FIRST, and that is what makes walking
// back to unit 1 to fix a typo safe: it is the whole of "never silently discard
// what the operator typed" on this phase.
//
// The VALIDATION lives here, at the one place a capture is recorded, rather
// than in the arms that call it. It used to live in the Enter arm alone, and
// the other two callers therefore recorded slots Enter would have refused —
// both of them doing the exact thing the checks were written to stop. Esc with
// a lot typed beside a blank serial recorded {serial:"", lot:"LOT-9"}, which
// buildReceipt drops (it skips every capture without a serial), so the lot went
// into nothing with no sentence anywhere saying so. And Esc or PgUp/PgDn with
// "12/31/2026" in the expiry carried it to the wire, where a 400 rolls back the
// WHOLE single-transaction receipt over one character in an optional field.
//
// Copying the two checks into those arms is not the fix, because that is the
// shape that produced the defect: the checks were written when Enter was the
// only way out of a slot, and the arms added afterwards were each a fresh
// chance to forget. Putting them where the store is means a caller added
// tomorrow is covered by construction and cannot opt out.
//
// A refusal hands back the sentence to say and leaves the three boxes exactly
// as they were typed, so whatever the arm was trying to do — go to the review,
// walk to another unit — simply does not happen and the operator can fix the
// slot in place.
func (s *ReceiveFormScreen) storeUnit(key string, headerRows int) tea.Cmd {
	if s.serialCursor < 0 || s.serialCursor >= len(s.captures) {
		// Past the end of the queue the frame draws no boxes at all, so there
		// is nothing to record and nothing to refuse.
		return nil
	}
	c := receiveCapture{
		serial: strings.TrimSpace(s.serialInput.Value()),
		lot:    strings.TrimSpace(s.lotInput.Value()),
		expiry: strings.TrimSpace(s.expiryInput.Value()),
	}
	if why, level := receiveCaptureRefusal(key, c); why != "" {
		return s.say(why+" · "+s.waysOut(headerRows), level)
	}
	s.captures[s.serialCursor] = c
	return nil
}

// receiveCaptureRefusal is why a slot cannot be recorded, worded for the key
// that is trying to leave it, or "" when it can be.
//
// ONE wording, read by every arm through storeUnit, because a screen that
// refuses the same fact in two voices leaves the operator unable to tell
// whether they hit the same refusal twice. The key LEADS the sentence for the
// reason every decline on this screen names its key: two keys sharing one
// sentence redraw each other's pane, which from the operator's seat is a
// program that stopped responding.
//
// A wholly blank slot is NOT refused. It is a deliberate skip — the contract
// accepts fewer serials than units on purpose, and the gap comes back as
// serials_outstanding rather than being hidden.
func receiveCaptureRefusal(key string, c receiveCapture) (string, StatusLevel) {
	if c.serial == "" && (c.lot != "" || c.expiry != "") {
		// There is nothing for these two to hang off: the payload carries lot
		// and expiry ON a serial and nowhere else, so recording the slot would
		// throw them away without saying so.
		return key + " needs a serial before a lot or an expiry — a lot hangs off " +
			"a serial and there is nothing here to hang it on", StatusWarn
	}
	if c.expiry != "" && !receiveIsISODate(c.expiry) {
		return key + " needs an expiry written YYYY-MM-DD — " +
			poQuotedClip(c.expiry, receiveQuotedCells) + " is not", StatusError
	}
	return "", StatusOK
}

// keySerial handles serial capture.
//
// Enter records the unit and moves ON; PgUp/PgDn walk between units without
// moving on; UP/DN move between the three boxes of the unit the cursor is on.
// Esc finishes capture and goes to the review, which is FORWARD: there is no
// arrangement of keys here that leaves the operator unable to reach the post.
func (s *ReceiveFormScreen) keySerial(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.serialCursor >= len(s.serialUnits) {
		// Every slot has an answer and there is no box on the pane. The phase
		// still exists so the operator can walk back into it, so the keys that
		// move a cursor still act and the rest say why.
		switch k {
		case "esc", "enter":
			return s, s.toReview(headerRows, "", StatusInfo)
		case "pgup":
			// PgUp and nothing else. On the capture frame UP/DN walks the three
			// FIELDS of a unit and PgUp/PgDn walks the units; this frame has no
			// fields, so the unit key is the only one that means anything here
			// — and binding Up as well while the bar named only PgUp is the
			// bar-honesty rule broken in the direction that is hardest to see,
			// a key that works and is advertised nowhere.
			//
			// The GUARD matches serialBar's, which names PgUp only when the
			// queue has a unit to step back to. They disagreed: the bar tested
			// the queue and the arm did not, so on an empty queue this walked
			// serialCursor to -1, currentInput's past-the-end test went false,
			// focusCurrent armed the serial box, and serialBody indexed
			// serialUnits[-1] and panicked. Nothing can produce that state
			// today — beginReceipt reviews instead of capturing when the queue
			// is empty, and Ctrl+E declines on the same test — but "unreachable
			// because of what some other arm happens to do" is the property
			// currentInput's own comment refuses to rest on, and a refactor is
			// exactly what makes it reachable.
			//
			// DRAWABILITY is asked first, before the queue is, for the reason
			// pageUnit asks it: this key's whole effect is the position, so on
			// a pane the layer refuses there is no unit on screen to step back
			// to and nothing to say about not stepping — a note written here
			// would be drawn when the terminal grows back, answering a press
			// the operator has moved on from.
			if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
				return s, nil // refused pane — see pageQty
			}
			if len(s.serialUnits) == 0 {
				return s, s.decline(k, headerRows)
			}
			s.toSerial(len(s.serialUnits) - 1)
			return s, s.say("back on unit "+strconv.Itoa(len(s.serialUnits))+" of "+
				strconv.Itoa(len(s.serialUnits)), StatusInfo)
		}
		return s, s.decline(k, headerRows)
	}

	switch k {
	case "esc":
		// Esc goes FORWARD to the review, so it carries the slot with it — and
		// a slot the receipt would be refused for must not be what it carries.
		// storeUnit answers for that; a refusal here leaves the cursor where it
		// is, with what was typed still in the boxes.
		if cmd := s.storeUnit(k, headerRows); cmd != nil {
			return s, cmd
		}
		return s, s.toReview(headerRows, "", StatusInfo)
	case "enter":
		return s.commitUnit(headerRows)
	case "up", "shift+tab":
		s.moveSerialField(-1, headerRows)
		return s, nil
	case "down", "tab":
		s.moveSerialField(+1, headerRows)
		return s, nil
	case "pgup", "pgdown":
		return s, s.pageUnit(k, headerRows)
	}
	return s, s.typeInto(s.currentInput(), m, headerRows)
}

func (s *ReceiveFormScreen) moveSerialField(dir, headerRows int) {
	next, ok := s.moveRow(s.serialField, receiveSerialFields, dir, headerRows, s.barFor(headerRows))
	if !ok {
		return // refused pane — see focusNext
	}
	s.currentInput().Blur()
	s.serialField = next
	s.focusCurrent()
}

// pageUnit walks between capture slots, keeping what is in the boxes.
//
// Not pageRow, and not because of the pane: PgUp/PgDn here step ONE UNIT rather
// than one paneful, so there is no window question to ask — the bar names the
// pair whenever there are two units to walk between, whatever the body is doing.
// Drawability is still the layer's, asked exactly as everywhere else.
func (s *ReceiveFormScreen) pageUnit(k string, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return nil // refused pane — see pageQty
	}
	if len(s.serialUnits) < 2 {
		return s.decline(k, headerRows)
	}
	next := s.serialCursor + 1
	if k == "pgup" {
		next = s.serialCursor - 1
	}
	if next < 0 || next >= len(s.serialUnits) {
		edge := "the last unit"
		if k == "pgup" {
			edge = "the first unit"
		}
		return s.say(k+" is already at "+edge+" · "+s.waysOut(headerRows), StatusInfo)
	}
	if cmd := s.storeUnit(k, headerRows); cmd != nil {
		return cmd
	}
	s.toSerial(next)
	return s.say(fmt.Sprintf("unit %d of %d", next+1, len(s.serialUnits)), StatusInfo)
}

// commitUnit is Enter during capture: record what is in the boxes and move to
// the next slot.
//
// Whether the slot MAY be recorded is storeUnit's answer and no longer this
// arm's — the two checks that used to stand here were bypassed by every other
// way out of a slot, which is the note on storeUnit.
func (s *ReceiveFormScreen) commitUnit(headerRows int) (Screen, tea.Cmd) {
	serial := strings.TrimSpace(s.serialInput.Value())

	warn := ""
	if serial != "" {
		if n := s.duplicateOf(serial); n > 0 {
			// A WARNING and not a refusal. The rule that a serial may appear
			// once per item in one receipt is the SERVER's, and it is the
			// server that applies it — this only says what is already on the
			// pane's own captures, so the operator can fix it now instead of
			// having the whole receipt rolled back later.
			warn = fmt.Sprintf(" · %s is already on unit %d for this item, and OMS "+
				"refuses a repeat", poQuotedClip(serial, receiveQuotedSerialCells), n)
		}
	}

	if cmd := s.storeUnit("enter", headerRows); cmd != nil {
		return s, cmd
	}
	at := s.serialCursor + 1
	if at >= len(s.serialUnits) {
		// The LAST unit goes straight to the review, because that is what the
		// bar says it does: serialBar reads "Save & review" / "Skip & review"
		// there, and stopping on a capture summary instead would be the bar
		// naming a destination the key does not reach.
		s.serialCursor = at
		lead := fmt.Sprintf("%d of %d %s captured", s.capturedCount(), len(s.serialUnits),
			plural("unit", len(s.serialUnits))) + warn
		level := StatusOK
		if warn != "" {
			level = StatusWarn
		}
		return s, s.toReview(headerRows, lead, level)
	}
	s.toSerial(at)
	verb := "captured"
	if serial == "" {
		verb = "passed over"
	}
	return s, s.say(fmt.Sprintf("unit %d %s · unit %d of %d", at, verb, at+1, len(s.serialUnits))+
		warn, StatusInfo)
}

// duplicateOf reports the 1-based unit a serial is already captured on for the
// SAME identity, or 0. Same identity, because that is the server's key: the
// same serial on two different items is two different units and is legitimate.
func (s *ReceiveFormScreen) duplicateOf(serial string) int {
	if s.serialCursor >= len(s.serialUnits) {
		return 0
	}
	item := s.serialUnits[s.serialCursor].itemID
	for i, c := range s.captures {
		if i == s.serialCursor || i >= len(s.serialUnits) {
			continue
		}
		if s.serialUnits[i].itemID == item && strings.EqualFold(c.serial, serial) {
			return i + 1
		}
	}
	return 0
}

func (s *ReceiveFormScreen) capturedCount() int {
	n := 0
	for _, c := range s.captures {
		if strings.TrimSpace(c.serial) != "" {
			n++
		}
	}
	return n
}

// receiveIsISODate reports a plain YYYY-MM-DD.
//
// A FORMAT check and not a calendar one: whether 2027-02-31 exists is the
// server's business, and this only stops a scanner burst or a typo being sent
// as a date. It is here at all because the alternative is a 400 that rolls back
// the whole receipt over one character in an optional field.
func receiveIsISODate(v string) bool {
	if len(v) != 10 || v[4] != '-' || v[7] != '-' {
		return false
	}
	for i, r := range v {
		if i == 4 || i == 7 {
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// The review, and the receipt
// ---------------------------------------------------------------------------

// toReview moves to the last frame before stock moves.
//
// `lead` is what the key that GOT here has to say for itself — the capture
// summary, when the last serial is what moved the phase — and it comes ahead of
// the review's own sentence rather than replacing it. One writer for the note
// on this transition, because two would let the arm that arrives last leave the
// other's sentence standing under a frame it is no longer about.
//
// An OVER-RECEIPT outranks both. It is raised HERE, before the post, because
// the contract asks a client to: a typo is cheaper to fix than a vendor query.
// It is a warning and never a block — once the operator confirms it, the real
// figure goes exactly as typed.
func (s *ReceiveFormScreen) toReview(headerRows int, lead string, level StatusLevel) tea.Cmd {
	s.phase = phaseReview
	s.rowCursor = 0
	s.blurAll()
	if over := s.overReceiptSummary(); over != "" {
		return s.say(receiveJoin(lead, over+" · enter sends it as typed"), StatusWarn)
	}
	return s.say(receiveJoin(lead, s.reviewLead()), level)
}

// receiveJoin puts a key's own answer ahead of the frame's, dropping the joint
// when there is only one of them.
func receiveJoin(lead, rest string) string {
	if lead == "" {
		return rest
	}
	return lead + " · " + rest
}

// reviewLead is what the review opens with when nothing is over-received.
func (s *ReceiveFormScreen) reviewLead() string {
	lines, units := s.plannedLines(), 0
	for _, c := range s.captures {
		if strings.TrimSpace(c.serial) != "" {
			units++
		}
	}
	out := fmt.Sprintf("%d %s to receive", lines, plural("line", lines))
	if len(s.serialUnits) > 0 {
		out += fmt.Sprintf(" · %d of %d serials captured", units, len(s.serialUnits))
	}
	return out + " · enter sends it"
}

// plannedLines is how many lines this receipt would name.
func (s *ReceiveFormScreen) plannedLines() int {
	n := 0
	for i := range s.qty {
		if q, ok := receiveQuantity(s.qty[i].Value()); ok && q > 0 {
			n++
		}
	}
	return n
}

// overReceiptSummary names the lines whose typed quantity would take the line
// past what was ordered, or "" when none would.
//
// It describes rather than decides: the figure is sent exactly as typed, the
// server flags the line `over_received` with a positive variance, and that flag
// is the record the whole flow exists to produce. Nothing here rounds anything.
//
// The positions are LISTED while the list is short and COUNTED once it is not,
// against receiveScanListMax — the SAME bound receiveOtherLineNote uses, not a
// second one, because two bounds over the same shape are two answers waiting to
// disagree. The shape is the one that bound exists for: a run of line numbers
// is unbounded in the number of lines an order can carry, and a bound expressed
// in terms of an unbounded value is not a bound. toReview puts this into a note
// that folds into receiveNoteRows lines and is shortened FROM THE END, and the
// end is "enter sends it as typed" — so on a seven-line order with every line
// over, the run of positions pushed the contract-mandated pre-send warning's
// own instruction off the pane, on the last frame before stock moves.
//
// Nothing is lost by counting: reviewBody draws "%d OVER the order" under every
// line it applies to, so the detail is on the frame this note is describing.
func (s *ReceiveFormScreen) overReceiptSummary() string {
	var over []string
	for i, line := range s.lines {
		q, ok := receiveQuantity(s.qty[i].Value())
		if !ok || q <= 0 {
			continue
		}
		if extra := line.sheet.QuantityReceived + q - line.sheet.QuantityOrdered; extra > 0 {
			over = append(over, fmt.Sprintf("line %d by %d", i+1, extra))
		}
	}
	if len(over) == 0 {
		return ""
	}
	if len(over) > receiveScanListMax {
		return fmt.Sprintf("over the order on %d lines, each marked below — recorded and "+
			"flagged, never rounded", len(over))
	}
	return fmt.Sprintf("over the order on %s (%s) — recorded and flagged, never rounded",
		plural("line", len(over)), strings.Join(over, ", "))
}

// keyReview is the last frame before stock moves.
func (s *ReceiveFormScreen) keyReview(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.pending {
		if k == "esc" {
			return s, s.leave()
		}
		return s, s.declineFrozen(k, headerRows)
	}
	switch k {
	case "enter":
		return s.submit()
	case "esc":
		s.phase = phaseQty
		s.blurAll()
		s.focusCurrent()
		return s, s.say("back on the quantities · nothing has been sent", StatusInfo)
	case "ctrl+e":
		if len(s.serialUnits) == 0 {
			return s, s.decline(k, headerRows)
		}
		s.toSerial(0)
		return s, s.say("back on unit 1 of "+strconv.Itoa(len(s.serialUnits)), StatusInfo)
	case "up", "down":
		// No Tab alias, for the reason keyBlocked's arm records: the alias
		// belongs to sheets with fields, and this frame has none.
		return s, s.moveRowCursor(k, s.reviewRows(), headerRows)
	case "pgup", "pgdown":
		return s, s.pageRowCursor(k, s.reviewBody(), s.reviewRows(), headerRows)
	}
	return s, s.decline(k, headerRows)
}

// buildReceipt turns the form into the payload. It is the ONE place the wire
// shape is assembled, so the review frame and the request cannot describe
// different receipts.
func (s *ReceiveFormScreen) buildReceipt() omsapi.ReceiveRequest {
	serialsByLine := map[int][]omsapi.ReceiptSerial{}
	for i, u := range s.serialUnits {
		c := s.captures[i]
		if strings.TrimSpace(c.serial) == "" {
			continue
		}
		serialsByLine[u.lineIdx] = append(serialsByLine[u.lineIdx], omsapi.ReceiptSerial{
			SerialNumber: c.serial,
			// The identity is ALWAYS sent, even where the line credits only one
			// and the contract makes it optional. On a kit line it is required,
			// and a payload whose shape depends on how many components happen
			// to be serialized is a payload that changes under a catalogue
			// edit nobody made for this receipt.
			Item:           u.itemID,
			Lot:            c.lot,
			ExpirationDate: c.expiry,
		})
	}

	var items []omsapi.ReceiptLine
	for i, line := range s.lines {
		q, ok := receiveQuantity(s.qty[i].Value())
		if !ok || q <= 0 {
			continue
		}
		items = append(items, omsapi.ReceiptLine{
			PurchaseOrderItem: line.sheet.PurchaseOrderItem,
			QuantityReceived:  q,
			Serials:           serialsByLine[i],
		})
	}
	return omsapi.ReceiveRequest{
		Items:          items,
		DeliveryDate:   strings.TrimSpace(s.delivered.Value()),
		TrackingNumber: strings.TrimSpace(s.tracking.Value()),
		Carrier:        strings.TrimSpace(s.carrier.Value()),
		ReceiptNotes:   strings.TrimSpace(s.notes.Value()),
	}
}

func (s *ReceiveFormScreen) submit() (Screen, tea.Cmd) {
	if d := strings.TrimSpace(s.delivered.Value()); d != "" && !receiveIsISODate(d) {
		return s, s.say("the delivered date is written YYYY-MM-DD — "+
			poQuotedClip(d, receiveQuotedCells)+" is not · esc goes back to the quantities",
			StatusError)
	}
	req := s.buildReceipt()
	if len(req.Items) == 0 {
		// Unreachable through the bar — the review is only entered past
		// entryState — and kept for the reason every backstop on this screen is:
		// a refusal must not depend on two predicates agreeing.
		return s, s.say("nothing to receive — "+s.entryRefusal(), StatusWarn)
	}
	s.pending = true
	s.clearFail()
	// The payload has gone, so nothing that shaped it may keep a caret: a
	// cursor blinking in a field whose contents are already on the wire says
	// the opposite of what the frozen bar says.
	s.blurAll()
	// The subject is built HERE, from what the screen already knows, and not
	// from the reply. A failed request answers with a nil order, so a name read
	// off the reply falls back to "PO #5" — and the operator then reads
	// "Receiving PO #5 failed" on a screen every other line of which says
	// PO-1001, on the one line that has to be unambiguous.
	what := "Receiving " + s.orderName()
	deps, id, ctx := s.deps, s.poID(), s.ctx()
	if deps.OMS == nil {
		return s, receiveNoClient(what)
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.ReceivePOItems(ctx, id, req)
		return receiveSubmittedMsg{what: what, po: out, err: err}
	}
}

// receiveNoClient is what a WRITE answers with when there is no OMS client to
// send it — the same shape loadSheet uses, and for the same reason: a screen
// built with a bare Deps{} must fail rather than dereference one, and it must
// fail as a FAILURE the frame can draw rather than as a panic that takes the
// program with it.
func receiveNoClient(what string) tea.Cmd {
	return func() tea.Msg {
		return receiveSubmittedMsg{what: what, err: errors.New("no connection to OpenMakerSuite")}
	}
}

// fmtOrderName names an order off the WIRE — the reply's own number, when there
// is a reply. It is for the summary, which describes what came back; a message
// about a request that may have FAILED reads the screen's orderName instead.
func fmtOrderName(po *omsapi.PurchaseOrder, id string) string {
	if po != nil && po.Number != "" {
		return po.Number
	}
	return "PO #" + id
}

// ---------------------------------------------------------------------------
// Writing a balance off
// ---------------------------------------------------------------------------

// A WRITE-OFF NEVER CONSUMES A TYPED QUANTITY.
//
// THE REASON THESE REFUSE IS THAT THE REFUSAL CAN BE SATISFIED FROM THE FRAME IT
// IS DRAWN ON, and it is worth stating first because it is NOT the reason that
// was written here originally. That one read: "a close-short cannot be taken
// back from this client — the correction is reopen-short/, which this change
// decodes and deliberately does not drive." It was load-bearing on a fact about
// ScanTTY rather than about the write, and Ctrl+O has since made it false: the
// correction IS driven now (see "Taking a close-short back" below).
//
// The refusal stays, and the re-derivation is the point rather than the verdict.
// Three things decide it, and none of them moved:
//
//   - WHAT THIS GATE PROTECTS IS NOT THE CLOSE-SHORT. It protects the
//     operator's ENTRY — a typed quantity, the tracking number, the carrier,
//     the delivered date, the receipt notes. Reopening a line puts its
//     outstanding balance back; it hands back no typed quantity and no typed
//     note, and no endpoint does. So "the operator can correct the record now"
//     is true of the write-off and false of the thing being discarded, and
//     softening on the strength of it would trade a satisfiable refusal for a
//     silent loss of input nothing can recover.
//   - THE RULE THAT DECIDES IS SATISFIABILITY. Never SILENTLY discard what the
//     operator typed demands NON-SILENCE; refusal is one way to be non-silent
//     and is the right one only where the operator can clear what is in the way
//     WITHOUT leaving the frame the refusal is drawn on. Every box counted here
//     is a backspace away on that frame. That is also why the gate already
//     SPLITS: the captured serials cannot be cleared from there, so they are
//     named on the confirm and let through (writeOffCaptureLoss).
//   - IT IS A CERTAINTY, NOT A RISK. Committing ENDS the form — see below — so
//     the entry is not endangered by the write, it is gone with it.
//
// Irreversibility was never the right lever and writeOffCaptureLoss had already
// said so about the other half of this gate: a write that cannot be taken back
// argues for making the loss UNMISSABLE, not for blocking a key nobody can
// unblock. Reopen weakens an argument this gate does not actually rest on.
//
// The DEFECT that produced it is unchanged and is what the rest of this note
// records. It happened like this. Line 2 is ordered 10, received 3. The operator walks
// to it, types 8 (the rest of the shipment is on the bench), and presses Ctrl+K
// — which the bar names exactly there, because that is the only row it acts on.
// Every figure the confirm then showed was the SERVER's: "closing line 2 short
// leaves 7 units unreceived for good". Ctrl+X posted close-short and nothing
// else. The line settled at received 3, the 8 was never sent, and no frame on
// the way through said a word about it.
//
// Ctrl+R is the same shape with the blast radius of the whole order, and its
// label makes it worse: "Mark received" is the phrase an operator is most
// likely to read as "book what I typed and finish up".
//
// Absorbing the entry — receiving it and then closing the rest — is NOT what
// these do. That is a second write on a key that says it does one thing, and it
// is out of scope besides. They refuse, they say what is in the way, and what
// was typed stays exactly where it was typed.
//
// THE GATE IS ABOUT THE WHOLE FORM, and both keys read the same one. The first
// version of it scoped Ctrl+K to the FOCUSED line and Ctrl+R to any line, and
// that asymmetry was wrong for a reason worth writing down: what makes these
// unrecoverable is that COMMITTING ENDS THE FORM. handleSubmitted lands on the
// summary, `r` there calls resetEntry before the reload, and enter/esc leave the
// screen — so the only path that hands the boxes back is a write-off that
// FAILED. A close-short against line 2 therefore destroys the 4 typed on line 1
// exactly as surely as mark-received does, and the per-line predicate could not
// see it.
//
// It is not only the quantity boxes, for the same reason. A form-ending write
// throws away the tracking number, the carrier, the delivered date, the receipt
// notes and every serial captured so far — all of it operator input, all of it
// on a path with no way back. So the gate asks what the action would ACTUALLY
// destroy, derived from the same roster allBoxes is checked against rather than
// from a fresh list of fields, because a fresh list is what this defect was
// made of.
//
// AND REFUSING IS NOT THE RULE, WHICH IS WHY THE ANSWER SPLITS: what is
// CLEARABLE from the quantity form refuses (writeOffDiscards) and what is not
// is NAMED on the confirm and let through. The satisfiability test that decides
// which side a loss falls on is stated at the head of this note; the dead end
// that produced it is writeOffCaptureLoss's own.

// receiveEntryBox is a box a form-ending write would destroy, and the word a
// refusal calls it by.
type receiveEntryBox struct {
	name string
	box  *textinput.Model
}

// entryBoxes is every box whose contents the RECEIPT carries and a write-off
// would therefore throw away, in the order the form draws them.
//
// The quantity boxes are counted separately (linesTyped) because a refusal
// naming nine of them by name says nothing an operator can act on, and the
// capture boxes are answered through s.captures, which is where storeUnit has
// already put them — as a WARNING rather than a gate, because a capture cannot
// be cleared from this frame. What is left is the delivery block and the notes.
//
// entryBoxesExcluded records every OTHER box in allBoxes() and why it is not
// entry this gate protects, so absent and deliberate are different states —
// TestReceiveFormScreen_EveryBoxIsClassifiedForTheWriteOffGate walks allBoxes
// and fails on a box in neither, which is how a box added tomorrow gets an
// answer rather than a silent exemption.
func (s *ReceiveFormScreen) entryBoxes() []receiveEntryBox {
	return []receiveEntryBox{
		{"a tracking number", &s.tracking},
		{"a carrier", &s.carrier},
		{"a delivered date", &s.delivered},
		{"receipt notes", &s.notes},
	}
}

func (s *ReceiveFormScreen) entryBoxesExcluded() map[*textinput.Model]string {
	return map[*textinput.Model]string{
		// A LOOKUP input, not something the receipt carries: Enter consumes it
		// to find a line and buildReceipt never reads it, so a write-off
		// destroys nothing an operator would want back. Esc's label still
		// counts it (anythingTyped), because Esc destroys the screen and the
		// half-typed code with it.
		&s.scan: "a lookup input Enter consumes; the receipt does not carry it",
		// The three boxes of the LIVE slot. What is typed into them is not lost
		// by being absent from entryBoxes: every arm that leaves a slot goes
		// through storeUnit first, so the value is in s.captures by the time a
		// write-off could reach it, and s.captures is what the confirm names.
		&s.serialInput: "the live capture slot; storeUnit records it into s.captures, which the confirm names",
		&s.lotInput:    "the live capture slot; storeUnit records it into s.captures, which the confirm names",
		&s.expiryInput: "the live capture slot; storeUnit records it into s.captures, which the confirm names",
		// The write-off's OWN field, typed on the confirm this gate opens. It is
		// what the write-off carries, not what it destroys, and toWriteOff
		// clears it on the way in.
		// The settlement REASON, typed on a confirm this gate has already been
		// passed to reach — the write-off's, or the reopen's, which shares the
		// box. It is what a settlement CARRIES, not what one destroys, and both
		// confirms clear it on the way in.
		&s.reason: "the settlement reason, typed after this gate has already passed",
		&s.parked: "the scratch field no frame draws (focusCurrent's null object)",
	}
}

// writeOffDiscards names what a form-ending write-off would throw away AND the
// operator can take back from the frame the refusal is drawn on, or "" when
// there is none.
//
// It names the FIRST thing and counts the rest, which is a bound rather than a
// style: the note folds into receiveNoteRows lines and is shortened from the
// end, where the way out is named, so a sentence listing nine lines and four
// fields would spend the tail on itself. The quantity count leads because it is
// the most common and the most actionable.
//
// Everything it counts is a BOX ON THIS FORM, so "clear first" is a keystroke
// away and the refusal is one the operator can act on. That is the whole
// membership test, and writeOffCaptureLoss is what fails it.
func (s *ReceiveFormScreen) writeOffDiscards() string {
	var parts []string
	if n := s.linesTyped(); n > 0 {
		parts = append(parts, fmt.Sprintf("a quantity on %d %s", n, plural("line", n)))
	}
	for _, f := range s.entryBoxes() {
		if strings.TrimSpace(f.box.Value()) != "" {
			parts = append(parts, f.name)
		}
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return fmt.Sprintf("%s and %d more", parts[0], len(parts)-1)
}

// writeOffCaptureLoss is what a write-off destroys that the operator CANNOT
// take back from the quantity form, or "" when it destroys none.
//
// A REFUSAL IS ONLY LEGITIMATE WHERE IT CAN BE SATISFIED, and this one could
// not be. The gate counted captured serials for one round, and s.captures is
// written by exactly three functions — beginReceipt, storeUnit and resetEntry —
// none of which is reachable from the quantity form once the boxes are empty.
// So: capture a serial against line 3, walk back to the quantities, decide the
// line should not be received at all, backspace the box clear. Both destructive
// keys then went permanently unnamed and answered "ctrl+k would discard 1
// captured serial — receive or clear first", where RECEIVING was impossible
// (entryState is receiveNothingTyped, so Enter refuses and the bar does not
// name it), CLEARING was impossible (no key on the phase touches s.captures),
// and the serial being protected was not even sendable, since buildReceipt
// drops a line whose quantity box is blank. The only real way out was Esc,
// which destroys everything and leaves the screen, and the sentence did not
// name it. A dead end is its own defect.
//
// Irreversibility is what made that look reasonable, and it is the wrong lever:
// a write that cannot be taken back argues for making the loss UNMISSABLE, not
// for blocking a key the operator cannot unblock. So the count is NAMED and the
// write proceeds — the operator presses Ctrl+X knowing exactly what it costs.
//
// It is drawn in the CONFIRM BODY rather than only in the note that opened the
// confirm, because a note is the answer to a keypress and is retired by the
// next one, while the confirm is the frame Ctrl+X is actually pressed on. That
// is the same reasoning reviewBody's dropped-serials caveat is there for. And
// it names the COUNT, because "captured work will be lost" is not a fact
// anybody can weigh.
func (s *ReceiveFormScreen) writeOffCaptureLoss() string {
	n := s.capturesTaken()
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d captured %s will be discarded", n, plural("serial", n))
}

// writeOffRefusal is the ONE gate both write-off keys read, worded for the key
// that is trying to act.
//
// One predicate, read by the ARM and by qtyBarItems, so the bar names the key
// for exactly as long as it would act. That is not tidiness: a bar naming a key
// whose whole effect is to write a note is the bar-honesty rule broken, and the
// sweeps count a decline as NOT acting, so the two halves have to come off the
// same answer or the sweep reports the disagreement rather than the defect.
func (s *ReceiveFormScreen) writeOffRefusal(key string) string {
	if what := s.writeOffDiscards(); what != "" {
		return fmt.Sprintf("%s would discard %s — receive or clear first", key, what)
	}
	return ""
}

// lineWriteOffRefusal is why Ctrl+K cannot act, or "" when it can. The two
// arms above it are about the CURSOR and the LINE; the shared gate below is
// about the form, and it is the same one Ctrl+R reads.
func (s *ReceiveFormScreen) lineWriteOffRefusal() string {
	i, ok := s.lineAt(s.focused)
	if !ok {
		return "ctrl+k closes a LINE short and the cursor is not on one"
	}
	if s.lines[i].sheet.QuantityPending <= 0 {
		return fmt.Sprintf("line %d has nothing outstanding to close short", i+1)
	}
	return s.writeOffRefusal("ctrl+k")
}

// orderWriteOffRefusal is why Ctrl+R cannot act, or "" when it can.
func (s *ReceiveFormScreen) orderWriteOffRefusal() string {
	if s.outstandingLines() == 0 {
		// The same way out the empty form names, for the same reason: the
		// server refuses a settled order and points at voiding or cancelling
		// it, and declining without that leaves the operator nowhere.
		return "ctrl+r finishes the order off and every line is already settled — " +
			"void or cancel the order instead"
	}
	return s.writeOffRefusal("ctrl+r")
}

// typedOn is what line i's quantity box holds, trimmed. Blank when the line has
// no box, which keeps the refusals above total over the row model.
func (s *ReceiveFormScreen) typedOn(i int) string {
	if i < 0 || i >= len(s.qty) {
		return ""
	}
	return strings.TrimSpace(s.qty[i].Value())
}

// linesTyped is how many lines hold a typed quantity. A typed ZERO counts: it
// is something the operator put there and a write-off would take it away just
// as surely as it takes a 2 — the same reasoning hasQuantityEntry records for
// what Esc costs.
func (s *ReceiveFormScreen) linesTyped() int {
	n := 0
	for i := range s.qty {
		if s.typedOn(i) != "" {
			n++
		}
	}
	return n
}

// capturesTaken is how many capture slots hold something. It is capturesLeft's
// complement over the slots that exist, and it counts a slot with a LOT or an
// expiry beside a blank serial too — storeUnit refuses to record one of those,
// so reaching here means the operator has something in a slot that a write-off
// would take. It is what the confirm WARNS about rather than what the gate
// refuses on; writeOffCaptureLoss carries why the two are different.
func (s *ReceiveFormScreen) capturesTaken() int {
	n := 0
	for _, c := range s.captures {
		if !c.empty() {
			n++
		}
	}
	return n
}

// openLineWriteOff is Ctrl+K: close the focused LINE's outstanding balance
// short. It declines from anywhere the key cannot mean that, naming which.
func (s *ReceiveFormScreen) openLineWriteOff(headerRows int) tea.Cmd {
	if why := s.lineWriteOffRefusal(); why != "" {
		return s.say(why+" · "+s.waysOut(headerRows), StatusWarn)
	}
	i, _ := s.lineAt(s.focused)
	s.scope, s.scopeLine = receiveScopeLine, i
	s.toWriteOff()
	return s.say(fmt.Sprintf("closing line %d short leaves %d %s unreceived for good",
		i+1, s.lines[i].sheet.QuantityPending, plural("unit", s.lines[i].sheet.QuantityPending)),
		StatusWarn)
}

// openOrderWriteOff is Ctrl+R: finish the order off.
func (s *ReceiveFormScreen) openOrderWriteOff(headerRows int) tea.Cmd {
	if why := s.orderWriteOffRefusal(); why != "" {
		return s.say(why+" · "+s.waysOut(headerRows), StatusWarn)
	}
	s.scope = receiveScopeOrder
	s.toWriteOff()
	n := s.outstandingLines()
	return s.say(fmt.Sprintf("marking received closes %d outstanding %s short for good",
		n, plural("line", n)), StatusWarn)
}

// outstandingLines is the server's count, never a re-derived one.
func (s *ReceiveFormScreen) outstandingLines() int {
	if s.sheet == nil {
		return 0
	}
	return s.sheet.OutstandingLineCount
}

func (s *ReceiveFormScreen) toWriteOff() {
	s.phase = phaseWriteOff
	s.reason.SetValue("")
	s.blurAll()
	s.reason.Focus()
}

// keyWriteOff is the destructive confirm.
//
// Ctrl+X and not Enter, for the reason po_create.go's supplier switch uses it:
// a reflexive double-tap of the key that OPENED the confirm must not be what
// writes a balance off. Enter is therefore NOT bound — and declining to bind it
// is not licence to leave the press silent, so it answers by naming the key
// that does act.
func (s *ReceiveFormScreen) keyWriteOff(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.pending {
		if k == "esc" {
			return s, s.leave()
		}
		return s, s.declineFrozen(k, headerRows)
	}
	switch k {
	case "esc":
		s.phase = phaseQty
		s.blurAll()
		s.focusCurrent()
		return s, s.say("nothing was closed short · back on the quantities", StatusInfo)
	case "ctrl+x":
		return s.commitWriteOff()
	case "enter":
		return s, s.say("enter is not the key here — ctrl+x is, so a reflex cannot "+
			"write a balance off · "+s.waysOut(headerRows), StatusWarn)
	case "up", "down", "pgup", "pgdown":
		// The confirm is one field and a question, so there is nothing here for
		// these to move — but the operator arrives on it from a frame whose bar
		// named UP/DN, because every other phase of this screen binds the pair,
		// and they press it out of habit. bubbles' textinput binds neither, so
		// handing them to the box redrew a byte-for-byte identical pane on the
		// ONE frame of this screen where the next key writes a balance off.
		// They decline by name instead, and writeOffBar names neither, so the
		// bar and the keys still agree.
		//
		// Named explicitly rather than left to typeInto below, because what
		// makes them inert today is a bubbles binding that does nothing without
		// suggestions — turn suggestions on and the box would start swallowing
		// Up and Down again, silently, on this frame of all of them.
		return s, s.decline(k, headerRows)
	}
	return s, s.typeInto(&s.reason, m, headerRows)
}

func (s *ReceiveFormScreen) commitWriteOff() (Screen, tea.Cmd) {
	reason := strings.TrimSpace(s.reason.Value())
	scope, line := s.scope, s.scopeLine
	s.pending = true
	s.clearFail()
	s.blurAll()
	deps, id, ctx := s.deps, s.poID(), s.ctx()
	if scope == receiveScopeOrder {
		what := "Marking " + s.orderName() + " received"
		if deps.OMS == nil {
			return s, receiveNoClient(what)
		}
		return s, func() tea.Msg {
			out, err := deps.OMS.MarkPurchaseOrderReceived(ctx, id, reason)
			return receiveSubmittedMsg{what: what, po: out, err: err}
		}
	}
	item := s.lines[line].sheet.PurchaseOrderItem
	req := omsapi.CloseShortRequest{Items: []omsapi.POLineSettlement{{PurchaseOrderItem: item, Reason: reason}}}
	what := fmt.Sprintf("Closing line %d short", line+1)
	if deps.OMS == nil {
		return s, receiveNoClient(what)
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.CloseShortPOLines(ctx, id, req)
		return receiveSubmittedMsg{what: what, po: out, err: err}
	}
}

// ---------------------------------------------------------------------------
// Taking a close-short back
// ---------------------------------------------------------------------------

// A REOPEN IS A CORRECTION, NOT AN UNDO, and every sentence on these two frames
// is worded so the operator cannot read it as one.
//
// The close-short stays on the line exactly as it was recorded — its reason, who
// recorded it and when — and the reopen is stamped beside it to the same
// standard. OMS's own model method carries that decision and this screen only
// relays it; what the terminal must not do is offer the operator a frame that
// reads like an erasure, because the record they are creating says the opposite.
// So the confirm NAMES what stays.
//
// It is reachable from the quantity form AND from the blocked frame, and the
// second is the one that matters: `reopen-short/` is accepted on an order whose
// status is already `received`, which is the ONE receiving write that is, and
// OMS says why in as many words — a line closed short in error is usually
// noticed AFTER the close settled the order. A settled order comes back with
// `can_receive: false`, so this screen draws phaseBlocked for it; offering the
// correction only where a receipt can be built would have withheld it from
// exactly the case it exists for.
//
// NOTHING HERE KEEPS A COPY OF WHICH STATUSES THE SERVER ALLOWS. The worksheet
// publishes `can_receive`, which is a NARROWER question (RECEIVABLE_STATUSES,
// which excludes `received`), and there is no flag for this one — so the key is
// offered wherever the server has said a line IS closed short, the write is
// sent, and a refusal is relayed in the server's own words. A draft or cancelled
// order refuses with a sentence; guessing at the set here would refuse with ours.
//
// A LANDED REOPEN DOES NOT END THE VISIT, which is the one place it differs
// from the write-off beside it. Both of those commit and hand the operator the
// summary, which is right for a write that finishes with a line — but a reopen
// is a correction made so that a receipt CAN be built, and ending the flow would
// throw away every quantity already typed on the way to making it. So it
// refetches the worksheet instead: applyWorksheet carries typed quantities
// across by line id, the reopened line arrives in s.lines with an empty box, and
// the operator is standing on the form with the line they just recovered.
//
// That is also why this key needs no gate of the write-off's kind. A write-off
// destroys the form; this one preserves it, so there is nothing to refuse on.

// reopenable is the SETTLED rows a reopen may name, as indexes into s.closed.
//
// The membership test is the server's published flag and nothing else. A line is
// on this list when `is_closed_short` is true — which OMS derives from the
// close-short and reopen stamps together, so a line already corrected once is
// simply not on it — and a VOIDED line is excluded only when the server says it
// is not closed short. Asking "is received < ordered?" here would offer a
// correction for a line nobody decided anything about.
func (s *ReceiveFormScreen) reopenable() []int {
	var out []int
	for i, l := range s.closed {
		if l.sheet.IsClosedShort {
			out = append(out, i)
		}
	}
	return out
}

// reopenRefusal is why Ctrl+O cannot act, or "" when it can.
//
// ONE predicate, read by the ARM and by both bars that name the key, for the
// reason lineWriteOffRefusal is: a bar naming a key whose whole effect is to
// write a note is the bar-honesty rule broken, and the sweeps count a decline as
// NOT acting, so the two halves have to come off the same answer.
func (s *ReceiveFormScreen) reopenRefusal() string {
	if s.sheet == nil {
		// COULD NOT TELL, not "there is none". The worksheet is where
		// is_closed_short comes from, so with no worksheet this screen does not
		// know whether there is anything to correct — the same split the blocked
		// frame keeps between a failed fetch and a server refusal.
		return "ctrl+o reopens a line closed short and the worksheet could not be read"
	}
	if len(s.reopenable()) == 0 {
		return "ctrl+o reopens a line closed short and no line on this order is closed short"
	}
	return ""
}

// openReopen is Ctrl+O on either frame that offers it.
func (s *ReceiveFormScreen) openReopen(headerRows int) tea.Cmd {
	if why := s.reopenRefusal(); why != "" {
		return s.say(why+" · "+s.waysOut(headerRows), StatusWarn)
	}
	s.phase = phaseReopenPick
	s.reopenCursor = 0
	s.blurAll()
	// NO NOTE. The phase change is the whole pane, which is the answer — the
	// picker-ENTRY idiom (po_create.go's itemPickEntryNote) — and a note written
	// here would squat on the note block for one keypress, displacing the
	// standing fact that has to lead it. The count rides the status FLASH, which
	// is a different surface and expires on its own.
	n := len(s.reopenable())
	return Status(fmt.Sprintf("%d %s closed short on %s",
		n, plural("line", n), s.orderName()), StatusInfo)
}

// reopenHome is the frame Esc goes back to from the pick, DERIVED from the same
// answer handleSheet routes on rather than remembered from the key that opened
// it. A worksheet that changed under the operator — they reopened a line on a
// settled order in another window — must not send them back to a frame the
// server has stopped serving.
func (s *ReceiveFormScreen) reopenHome() receivePhase {
	if s.sheet != nil && s.sheet.CanReceive {
		return phaseQty
	}
	return phaseBlocked
}

// reopenHomeLabel is what the bar calls that way back.
func (s *ReceiveFormScreen) reopenHomeLabel() string {
	if s.reopenHome() == phaseQty {
		return "Back to quantities"
	}
	return "Back to lines"
}

// toReopenHome leaves the correction without making one.
func (s *ReceiveFormScreen) toReopenHome() {
	s.phase = s.reopenHome()
	s.blurAll()
	if s.phase == phaseQty {
		s.focusCurrent()
	}
}

// reopenRows is how many rows the pick list has.
func (s *ReceiveFormScreen) reopenRows() int { return len(s.reopenable()) }

// reopenAt is the settled index the pick cursor is standing on. The bool keeps
// every reader total over a list that can empty under a reload.
func (s *ReceiveFormScreen) reopenAt(row int) (int, bool) {
	rows := s.reopenable()
	if row < 0 || row >= len(rows) {
		return 0, false
	}
	return rows[row], true
}

// keyReopenPick is the pick list's keyboard: a read-only list with a cursor.
//
// Tab and Shift-Tab are deliberately NOT aliases for Up/Down here, for
// keyBlocked's reason: the alias rides sheets WITH FIELDS, roughly twenty of
// which spell the pair as UP/DN=Fields, and this frame has none.
func (s *ReceiveFormScreen) keyReopenPick(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch k := m.String(); k {
	case "esc":
		s.toReopenHome()
		return s, s.say("nothing was reopened", StatusInfo)
	case "enter":
		i, ok := s.reopenAt(s.reopenCursor)
		if !ok {
			// A TOTAL read rather than a reachable branch: nothing empties the
			// list while this frame is up, because the only thing that rebuilds
			// s.closed is applyWorksheet and its one caller (handleSheet) always
			// sets the phase — loadSheet has already moved the screen to
			// phaseLoading before the reply lands. It answers instead of
			// indexing into nothing, because "safe because of what another arm
			// happens to do" is not a property (currentInput's note).
			s.toReopenHome()
			return s, s.say("that line is no longer closed short · nothing was reopened", StatusWarn)
		}
		s.reopenLine = i
		s.phase = phaseReopenConfirm
		s.reason.SetValue("")
		s.blurAll()
		s.reason.Focus()
		// No note, for openReopen's reason: the frame IS the answer, and the
		// note block is where the fact that must survive a short pane lives.
		return s, tea.Batch(Status(fmt.Sprintf("Reopening settled line %d of %s · ctrl+x commits it",
			i+1, s.orderName()), StatusInfo), textinput.Blink)
	case "up", "down":
		return s, s.moveReopenCursor(k, headerRows)
	case "pgup", "pgdown":
		return s, s.pageReopenCursor(k, headerRows)
	default:
		return s, s.decline(k, headerRows)
	}
}

// moveReopenCursor / pageReopenCursor are moveRowCursor and pageRowCursor over
// this phase's own cursor. They are separate functions rather than a parameter
// on those because the shared pair write to rowCursor by name, and threading a
// pointer through them would make the two read-only frames that DO share it
// harder to follow for one caller that does not.
func (s *ReceiveFormScreen) moveReopenCursor(k string, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return nil // refused pane — see pageQty
	}
	rows := s.reopenRows()
	if rows < 2 {
		return s.decline(k, headerRows)
	}
	if k == "up" {
		s.reopenCursor = (s.reopenCursor + rows - 1) % rows
	} else {
		s.reopenCursor = (s.reopenCursor + 1) % rows
	}
	return nil
}

func (s *ReceiveFormScreen) pageReopenCursor(k string, headerRows int) tea.Cmd {
	if !s.frameDrawn(headerRows, s.barFor(headerRows)) {
		return nil // refused pane — see pageQty
	}
	body, rows := s.reopenPickBody(), s.reopenRows()
	if !s.rowsPageFor(body, rows, headerRows) {
		return s.decline(k, headerRows)
	}
	dir := +1
	if k == "pgup" {
		dir = -1
	}
	step := s.windowRowsForBar(body, s.reopenCursor, headerRows, s.barCeiling())
	next := jdePageCursor(s.reopenCursor, rows, step, dir)
	if next == s.reopenCursor {
		return s.say(k+" is already at "+receiveEdge(dir)+" · "+s.waysOut(headerRows), StatusInfo)
	}
	s.reopenCursor = next
	return nil
}

// keyReopenConfirm is the correction's confirm.
//
// Ctrl+X and not Enter, for keyWriteOff's reason: Enter is the key that OPENED
// this frame, and a reflexive double-tap must not be what writes to the record —
// even a correction is a stamped, separately attributable event, and one made by
// reflex on the wrong line is a second mistake beside the first.
func (s *ReceiveFormScreen) keyReopenConfirm(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.pending {
		if k == "esc" {
			return s, s.leave()
		}
		return s, s.declineFrozen(k, headerRows)
	}
	switch k {
	case "esc":
		s.phase = phaseReopenPick
		s.blurAll()
		return s, s.say("nothing was reopened · back on the closed-short lines", StatusInfo)
	case "ctrl+x":
		return s.commitReopen()
	case "enter":
		return s, s.say("enter is not the key here — ctrl+x is, so a reflex cannot "+
			"reopen the wrong line · "+s.waysOut(headerRows), StatusWarn)
	case "up", "down", "pgup", "pgdown":
		// Declined by name for keyWriteOff's reason, which applies here twice
		// over: the operator arrives from a frame whose bar named UP/DN for a
		// LINE cursor, so the habit is live, and bubbles binds none of these
		// without suggestions — so handing them to the box would redraw a
		// byte-identical pane on a frame whose next key writes to the record.
		return s, s.decline(k, headerRows)
	}
	return s, s.typeInto(&s.reason, m, headerRows)
}

// commitReopen posts the correction.
func (s *ReceiveFormScreen) commitReopen() (Screen, tea.Cmd) {
	line, ok := s.reopenLineSheet()
	if !ok {
		// The same total read keyReopenPick's enter arm makes, and unreachable
		// for the same reason. It says so rather than posting an index into
		// nothing.
		s.toReopenHome()
		return s, s.say("that line is no longer closed short · nothing was reopened", StatusWarn)
	}
	reason := strings.TrimSpace(s.reason.Value())
	s.pending = true
	s.clearFail()
	s.blurAll()
	deps, id, ctx := s.deps, s.poID(), s.ctx()
	what := fmt.Sprintf("Reopening settled line %d", s.reopenLine+1)
	req := omsapi.ReopenShortRequest{Items: []omsapi.POLineSettlement{
		{PurchaseOrderItem: line.PurchaseOrderItem, Reason: reason},
	}}
	if deps.OMS == nil {
		// Its OWN no-client answer, so a screen built with no client fails the
		// correction as a correction: receiveNoClient answers with a
		// receiveSubmittedMsg, which would end the visit on the summary for a
		// write that never left the process.
		return s, func() tea.Msg {
			return receiveReopenedMsg{what: what,
				err: errors.New("no connection to OpenMakerSuite")}
		}
	}
	return s, func() tea.Msg {
		out, err := deps.OMS.ReopenShortPOLines(ctx, id, req)
		return receiveReopenedMsg{what: what, po: out, err: err}
	}
}

// reopenLineSheet is the guarded read every reader of s.reopenLine goes through.
func (s *ReceiveFormScreen) reopenLineSheet() (omsapi.ReceivingLine, bool) {
	if s.reopenLine < 0 || s.reopenLine >= len(s.closed) {
		return omsapi.ReceivingLine{}, false
	}
	l := s.closed[s.reopenLine].sheet
	if !l.IsClosedShort {
		return omsapi.ReceivingLine{}, false
	}
	return l, true
}

// handleReopened is the correction's reply.
//
// A SUCCESS refetches the worksheet rather than ending the visit — see the
// section note above — and holds the sentence saying what it did across that
// refetch, because Update retires the note on every reply off the wire and the
// refetch IS one.
//
// A FAILURE hands the confirm back live with the reason typed into it intact, on
// the frame the write left from, exactly as handleSubmitted does: whether the
// operator retypes a reason or goes and fetches somebody is decided by what the
// server said.
func (s *ReceiveFormScreen) handleReopened(m receiveReopenedMsg) tea.Cmd {
	s.pending = false
	if m.err != nil {
		head, detail := receiveFailure(m.what, m.err)
		s.setFail(head, detail)
		s.blurAll()
		s.reason.Focus()
		return tea.Batch(Status(head, StatusError), textinput.Blink)
	}
	s.clearFail()
	// What the ORDER became is the SERVER's answer and is reported rather than
	// predicted — a reopen drops an order that had reached `received` back to
	// `partially_received`, and that re-derivation is the server's.
	s.reopened = m.what + " succeeded — the close-short stays on the record beside it"
	if m.po != nil && m.po.StatusLabel != "" {
		s.reopened += " · " + fmtOrderName(m.po, s.poID()) + " is now " + m.po.StatusLabel
	}
	// FLASHED NOW as well as restated when the worksheet lands, because the two
	// can come apart: if the refetch fails the screen goes to the unreadable
	// frame, whose note block is the failure's, and without this the operator
	// would be looking at "the worksheet could not be read" with no word about
	// whether the correction they just made landed.
	return tea.Batch(s.loadSheet(), Status(s.reopened, StatusOK))
}

// reopenPickBody lists the lines a correction may name.
//
// Every line belongs to a navigable ROW, the rule qtyBody carries in full, and
// the frame's own explanation is NOT here: it is a fact about the frame rather
// than about any row, so it rides the note block (standingNote), which is on the
// pane at every drawable height from every row. Written as the tail of row 0's
// block it would be the first thing a short window dropped, on the frame whose
// whole job is saying what the correction does.
func (s *ReceiveFormScreen) reopenPickBody() *jdeLines {
	l := &jdeLines{}
	width := s.bodyWidth()
	rows := s.reopenable()
	if len(rows) < 2 {
		// One row: nothing moves the window, so what the operator cannot do
		// without has to BE the first line — which it is, the line's own name.
		l.DeclareLead("reopen pick")
	}
	for row, i := range rows {
		if row > 0 {
			// The separator closes the block ABOVE it, never opens the one
			// below: Window keeps a block's START, so a blank tagged to the
			// block below is the one line a short window draws for it.
			l.AddRow(row-1, "")
		}
		s.addReopenLineBlock(l, row, i, width)
	}
	return l
}

// addReopenLineBlock draws one closed-short line: which settled row it is, what
// it is, where receiving got to with it, and WHY it was written off.
//
// The close-short's own reason is on the row rather than only on the confirm,
// because it is what tells the operator this is the line they meant — picking by
// name alone is picking between two rows that may read the same.
func (s *ReceiveFormScreen) addReopenLineBlock(l *jdeLines, row, i, width int) {
	line := s.closed[i].sheet
	l.AddRow(row, jdeIndent+s.lineHeading(i+1, line, s.reopenCursor == row, width))
	for _, tok := range jdeWrapTokens(receiveLineTokens(line), receiveMetaIndent, width) {
		l.AddRow(row, tok)
	}
	if line.ClosedShortReason != "" {
		for _, cl := range receiveCaveatLines("closed short: "+line.ClosedShortReason,
			StyleMuted, width) {
			l.AddRow(row, cl)
		}
	} else {
		// ABSENT and EMPTY are different facts, and on this frame the difference
		// is what the operator is correcting: a write-off recorded with no
		// reason is a write-off nobody explained, which is itself worth knowing
		// before deciding it was a mistake.
		for _, cl := range receiveCaveatLines("closed short with no reason recorded",
			StyleMuted, width) {
			l.AddRow(row, cl)
		}
	}
	if line.WasReopened {
		for _, cl := range receiveCaveatLines("reopened before, then closed short again — "+
			"the newer stamp is the one in force", StyleMuted, width) {
			l.AddRow(row, cl)
		}
	}
}

// reopenConfirmBody is writeOffBody's shape: the FIELD leads, because a block
// that will not fit keeps its START and an operator typing into a field they
// cannot see is the reported-hang class this screen exists to remove.
func (s *ReceiveFormScreen) reopenConfirmBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	l.DeclareLead("reopen reason")
	l.AddFittedFields([]jdeField{{
		Label: "Reason",
		Kind:  jdeText,
		Input: &s.reason,
		Width: 34,
		// The hint says OPTIONAL because the server says so —
		// LineSettlementSerializer declares `required=False, allow_blank=True`
		// — and a screen that invented a required field here would refuse a
		// correction OMS accepts. It also says where the value GOES, because
		// "beside" is the whole of what makes this a correction and not an undo.
		Hint:    "optional · recorded beside the close-short",
		Focused: !s.pending,
	}}, lw, width, 0)
	l.AddRow(0, "")

	line, ok := s.reopenLineSheet()
	if !ok {
		for _, cl := range jdeCaveatLines("That line is no longer closed short, so there is "+
			"nothing to reopen. Esc goes back.", width) {
			l.AddRow(0, cl)
		}
		return l
	}

	// Fitted as ONE assembled line rather than by clipping the label against a
	// guess at what the rest costs — writeOffBody's note carries why.
	l.AddRow(0, jdeIndent+StyleStatusWarn.Render(receiveFit(
		fmt.Sprintf("Reopen settled line %d: %s", s.reopenLine+1, line.Label),
		width, len(jdeIndent))))
	for _, tok := range jdeWrapTokens(receiveLineTokens(line), receiveMetaIndent, width) {
		l.AddRow(0, tok)
	}
	// The DETAIL, and only the detail: the headline fact — that this does not
	// erase the write-off — is in the note block, which is on the pane at every
	// height (receiveReopenExplains). This body is one pinned block led by the
	// Reason field, so everything here is tail, and repeating the headline would
	// spend the rows a short pane does have on a sentence it already drew.
	//
	// The COUNT leads, because it is the one fact here that is about THIS line
	// rather than about reopening in general.
	off := receiveWrittenOff(line)
	for _, cl := range jdeCaveatLines(fmt.Sprintf(
		"The %d %s written off go back to outstanding. The close-short keeps its own "+
			"reason, actor and timestamp and this correction is stamped beside them. What "+
			"the ORDER becomes is the server's answer, and the reload reports it.",
		off, plural("unit", off)), width) {
		l.AddRow(0, cl)
	}
	return l
}

// receiveWrittenOff is how many units the close-short wrote off, from the
// server's own SIGNED variance rather than a subtraction of its own.
//
// QuantityPending is floored to zero on a settled line — that is what settling
// means — so it cannot answer this, and QuantityVariance is the honest figure
// the contract keeps for exactly this purpose. It is negative on a short line,
// so the magnitude is what a sentence names.
func receiveWrittenOff(line omsapi.ReceivingLine) int {
	if line.QuantityVariance < 0 {
		return -line.QuantityVariance
	}
	return line.QuantityVariance
}

// reopenPickBar names the keys the pick list honours.
func (s *ReceiveFormScreen) reopenPickBar(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Choose line"}, {"Esc", s.reopenHomeLabel()}}
	if s.reopenRows() > 1 {
		items = append(items, actionBarItem{"UP/DN", "Lines"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// reopenConfirmBar is the correction's bar. Ctrl+X is the commit and Enter is
// deliberately absent — see keyReopenConfirm.
func (s *ReceiveFormScreen) reopenConfirmBar() []actionBarItem {
	if s.pending {
		return []actionBarItem{{"Esc", "Back to order"}}
	}
	return []actionBarItem{{"Ctrl+X", "Reopen line"}, {"Esc", "Leave it closed"}}
}

// ---------------------------------------------------------------------------
// The reply
// ---------------------------------------------------------------------------

// handleSubmitted is the reply to whichever write finished the visit.
//
// A SUCCESS ends the flow on the summary with every box gone. It used to land
// back on the quantity form with the boxes still full, where a reflexive second
// Enter booked the whole delivery again and the only sign the first had worked
// was a flash that expires in four seconds.
//
// A FAILURE hands the form back live, on the frame the operator sent it from,
// with everything they typed intact: a failed receipt must not hold their
// counts hostage to a gateway, and the reason is what decides whether they
// retype a quantity or go and fetch somebody.
func (s *ReceiveFormScreen) handleSubmitted(m receiveSubmittedMsg) tea.Cmd {
	s.pending = false
	if m.err != nil {
		head, detail := receiveFailure(m.what, m.err)
		s.setFail(head, detail)
		s.blurAll()
		s.focusCurrent()
		return tea.Batch(Status(head, StatusError), textinput.Blink)
	}
	s.result = m.po
	s.receipt = m.what + " succeeded"
	s.phase = phaseDone
	s.blurAll()
	return Status(s.doneHeadline(), StatusOK)
}

// doneHeadline is what the status row says when a write lands: what the order
// is NOW, in the server's own words.
func (s *ReceiveFormScreen) doneHeadline() string {
	po := s.result
	if po == nil {
		return s.receipt
	}
	out := fmtOrderName(po, s.poID())
	if po.StatusLabel != "" {
		out += " · " + po.StatusLabel
	}
	if po.OutstandingLineCount > 0 {
		out += fmt.Sprintf(" · %d %s outstanding", po.OutstandingLineCount,
			plural("line", po.OutstandingLineCount))
	}
	return out
}

// keyDone is the summary's keyboard.
//
// It used to be "any key returns", which is not a claim an action bar can make
// honestly — and a scanner burst arriving on this frame would have dismissed
// the summary before anyone read it. `r` re-reads the worksheet and puts the
// operator back on the form, which is what makes the whole flow repeatable
// without leaving: receive some lines, look at what the server says, close a
// short line, receive the rest.
func (s *ReceiveFormScreen) keyDone(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch k := m.String(); k {
	case "enter", "esc":
		return s, s.leave()
	case "r":
		s.resetEntry()
		return s, tea.Batch(s.loadSheet(),
			Status("Re-reading the worksheet for "+s.orderName()+"…", StatusInfo))
	default:
		return s, s.decline(k, headerRows)
	}
}

// resetEntry empties everything a finished visit typed, so the form the reload
// lands on is a fresh one.
//
// It is the other half of "a successful receipt ends the flow": the boxes that
// booked the delivery must not come back holding the same numbers, because the
// next Enter would book it again. Only entry is cleared — the RESULT and the
// failure line stay, because they are what the server said and the operator may
// still be reading them when the reload lands.
func (s *ReceiveFormScreen) resetEntry() {
	for _, box := range s.allBoxes() {
		box.SetValue("")
	}
	s.serialUnits, s.captures, s.serialCursor, s.serialField = nil, nil, 0, receiveSerialNumber
	s.dropped, s.rowCursor, s.focused = 0, 0, receiveRowScan
	s.reopenCursor, s.reopenLine, s.reopened = 0, 0, ""
}

// ---------------------------------------------------------------------------
// The action bar
// ---------------------------------------------------------------------------

// bar names exactly the keys that act on the frame being drawn NOW. View draws
// it, and a test reading it is reading what the operator reads.
func (s *ReceiveFormScreen) bar() []actionBarItem {
	return s.barFor(len(s.headerLines()))
}

// barFor is bar for a frame with a KNOWN pinned header, and it is the one the
// key arms use.
//
// The two exist because the bar an operator obeys and the bar the screen is
// about to draw are not always the same bar: the pinned header costs the body
// rows, and the body's height is what decides whether PgUp/PgDn are named at
// all. The NOTE is not what varies it — receiveNoteRows reserves its rows
// unconditionally, so writing or retiring one moves the header by nothing — but
// the reply-driven FAILURE DETAIL does, and an arm can retire one mid-dispatch
// (submit's clearFail). So a press must be judged against the frame it was made
// ON, and handleKey measures that header before the arms run and hands it down.
// Deriving the header inside here instead is exactly the drift this pair exists
// to make impossible.
func (s *ReceiveFormScreen) barFor(headerRows int) []actionBarItem {
	switch s.phase {
	case phaseLoading:
		return []actionBarItem{{"Esc", "Back to order"}}
	case phaseBlocked:
		return s.blockedBarItems(s.rowsPageFor(s.blockedBody(), s.blockedRows(), headerRows))
	case phaseSerial:
		return s.serialBar()
	case phaseReview:
		return s.reviewBarItems(s.rowsPageFor(s.reviewBody(), s.reviewRows(), headerRows))
	case phaseWriteOff:
		return s.writeOffBar()
	case phaseReopenPick:
		return s.reopenPickBar(s.rowsPageFor(s.reopenPickBody(), s.reopenRows(), headerRows))
	case phaseReopenConfirm:
		return s.reopenConfirmBar()
	case phaseDone:
		return []actionBarItem{{"Enter/Esc", "Back to order"}, {"r", "Receive more"}}
	}
	return s.qtyBarItems(s.qtyPagesFor(headerRows))
}

// blockedBarItems is the bar of the frame that cannot receive. `r` is named in
// every state of it, because both facts the frame draws — a failed fetch and a
// server refusal — can change under the operator.
func (s *ReceiveFormScreen) blockedBarItems(paging bool) []actionBarItem {
	// LOWERCASE, because lowercase is the keystroke the handler binds and a bar
	// token is read literally by the operator. It was spelled "R" on all three
	// of this screen's re-read bars while keyBlocked and keyDone both bind "r",
	// so the frame whose ONLY recovery key is the re-read drew "R=Re-read",
	// answered Shift+R with "R does nothing here", and contradicted its own
	// body two rows up — blockedBody says "r tries again", because waysOut
	// lowercases what the bar carries. The case is not decoration on a single
	// letter: a terminal sends "R" and "r" as different keystrokes, and this
	// program already treats them as different keys (lowercase acts on the
	// screen you are on, uppercase opens a sibling surface — list.go's
	// listShortcuts). No binding changed; the bar stopped lying about one.
	items := []actionBarItem{{"r", "Re-read"}, {"Esc", "Back to order"}}
	// Named for exactly as long as it would ACT, off the arm's own predicate
	// (reopenRefusal), so the bar and the key cannot disagree. On the
	// unreadable-worksheet frame it is absent, because nothing there knows
	// whether a line is closed short.
	if s.reopenRefusal() == "" {
		items = append(items, actionBarItem{"Ctrl+O", "Reopen short"})
	}
	if s.blockedRows() > 1 {
		items = append(items, actionBarItem{"UP/DN", "Lines"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// qtyBarItems is the quantity form's bar for a given paging state, so the bar
// that is MEASURED is the bar that is drawn. Measuring against a different
// wording is how a block passes its own fit check and then overflows.
func (s *ReceiveFormScreen) qtyBarItems(paging bool) []actionBarItem {
	if s.pending {
		// Frozen: esc is the one key that acts, so it is the one key named.
		return []actionBarItem{{"Esc", "Back to order"}}
	}
	var items []actionBarItem
	switch s.enterAction() {
	case receiveEnterFind:
		items = append(items, actionBarItem{"Enter", "Find line"})
	case receiveEnterReceive:
		// Named only when there is something to ATTEMPT. With every box empty —
		// or every box holding a zero, which is skipped and which therefore
		// never leaves the terminal — Enter cannot receive anything and can
		// only refuse, and a bar that names it there teaches a key that does
		// not work. What Enter does with a quantity the PARSER or the server
		// will reject is still an act: it reports the refusal naming the line.
		items = append(items, actionBarItem{"Enter", "Receive"})
	}
	// The Esc label says what leaving COSTS, because leaving destroys the
	// screen and with it whatever is in the boxes. Saying so afterwards is too
	// late — there is no frame left to say it on.
	back := "Back to order"
	if s.anythingTyped() {
		back = "Discard & back"
	}
	items = append(items, actionBarItem{"Esc", back})
	if s.totalInputs() > 1 {
		items = append(items, actionBarItem{"UP/DN", "Fields"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	// Both destructive keys are named for exactly as long as they would ACT,
	// and the predicate is the arm's own — see lineWriteOffRefusal. A typed
	// quantity is one of the things that stops them, so the bar loses the key
	// on the keystroke that makes it refuse and gets it back when the box is
	// cleared or received.
	if s.lineWriteOffRefusal() == "" {
		items = append(items, actionBarItem{"Ctrl+K", "Close short"})
	}
	if s.orderWriteOffRefusal() == "" {
		items = append(items, actionBarItem{"Ctrl+R", "Mark received"})
	}
	// The CORRECTION, named wherever there is one to make. It carries no
	// write-off gate of its own: a reopen preserves the form rather than ending
	// it, so there is nothing typed for it to discard.
	if s.reopenRefusal() == "" {
		items = append(items, actionBarItem{"Ctrl+O", "Reopen short"})
	}
	return items
}

// qtyBarCeiling is the tallest bar the quantity phase can draw.
//
// It is a genuine FIXED POINT and that is what it is for: a taller bar is a
// smaller body, so a body that overflows the budget this leaves also overflows
// every larger one, and the header allowance measured against it cannot
// oscillate between frames. Every optional item is present — the paging pair,
// the two write-off keys — with the LONGEST wording each can take.
func (s *ReceiveFormScreen) qtyBarCeiling() []actionBarItem {
	return []actionBarItem{
		{"Enter", "Receive"},
		{"Esc", "Discard & back"},
		{"UP/DN", "Fields"},
		{"PgUp/PgDn", "Page"},
		{"Ctrl+K", "Close short"},
		{"Ctrl+R", "Mark received"},
		{"Ctrl+O", "Reopen short"},
	}
}

// qtyPagesFor reports whether PgUp/PgDn move anything on a frame whose pinned
// header is `headerRows` tall — the only state where they do, and therefore the
// only state the bar may name them in.
func (s *ReceiveFormScreen) qtyPagesFor(headerRows int) bool {
	// TWO conditions, because the keys make two claims and both have to hold.
	//
	// The body must MOVE — that is bodyScrollsForBar's question. But PgUp/PgDn
	// do not scroll the body: they move the CURSOR (jdePageCursor) and the
	// window follows it, so a page can only do anything when there is another
	// row to land on. Those two questions agree in almost every state and came
	// apart in one that was DESIGNED: a form with one navigable row that was
	// still taller than the pane, where jdePageCursor clamped and returned the
	// row it was handed while the bar printed PgUp/PgDn=Page over a key whose
	// whole effect was to write "pgdown is already at the last row". (That was
	// the nothing-receivable order before it gained its scan and delivery rows;
	// no state of this form has fewer than five rows now, and the count half is
	// what stops one coming back unnoticed.)
	//
	// Both halves are the LAYER's (bodyPagesForBar), so this screen is not
	// carrying a private copy of a rule thirty others also need. What pageRow
	// cannot express is how the answer is USED: its single false could not be
	// told apart from a refused pane, and this frame answers those two
	// differently — silence on a pane the layer refuses, a decline note when the
	// form simply has nothing to page.
	//
	// It is asked here rather than in the arm so the bar and pageQty read ONE
	// expression: two conditions that agree in most states are two conditions
	// that will eventually disagree in one.
	return s.bodyPagesForBar(s.qtyBody(), s.totalInputs(), headerRows, s.qtyBarCeiling())
}

// qtyPages is qtyPagesFor bound to the frame being drawn now, for View and for
// a test asking what the operator can see.
func (s *ReceiveFormScreen) qtyPages() bool {
	return s.qtyPagesFor(len(s.headerLines()))
}

// qtyStepFor is how many rows one page covers on a frame whose pinned header is
// `headerRows` tall — measured off the same window that frame draws, so a page
// moves by exactly what the operator could see on it. The arithmetic is the
// LAYER's (windowRowsForBar), not a local copy of it.
func (s *ReceiveFormScreen) qtyStepFor(headerRows int) int {
	return s.windowRowsForBar(s.qtyBody(), s.focused, headerRows, s.qtyBarCeiling())
}

// serialBar follows the BOX: with nothing in it, Enter passes the unit over,
// and saying "Save" there would name a key that does something else.
func (s *ReceiveFormScreen) serialBar() []actionBarItem {
	if s.serialCursor >= len(s.serialUnits) {
		// Every slot has an answer. The only keys left are the ones that move
		// on and the one that steps back into the queue.
		items := []actionBarItem{{"Enter/Esc", "Review"}}
		if len(s.serialUnits) > 0 {
			items = append(items, actionBarItem{"PgUp", "Last unit"})
		}
		return items
	}
	commit := "Skip unit"
	if strings.TrimSpace(s.serialInput.Value()) != "" {
		commit = "Save & next"
	}
	if s.serialCursor == len(s.serialUnits)-1 {
		commit = "Skip & review"
		if strings.TrimSpace(s.serialInput.Value()) != "" {
			commit = "Save & review"
		}
	}
	items := []actionBarItem{{"Enter", commit}, {"Esc", "Review"}, {"UP/DN", "Fields"}}
	if len(s.serialUnits) > 1 {
		items = append(items, actionBarItem{"PgUp/PgDn", "Unit"})
	}
	return items
}

// serialBarCeiling is the serial phase's fixed point, for the same reason
// qtyBarCeiling is the quantity phase's.
func (s *ReceiveFormScreen) serialBarCeiling() []actionBarItem {
	return []actionBarItem{
		{"Enter", "Save & review"},
		{"Esc", "Review"},
		{"UP/DN", "Fields"},
		{"PgUp/PgDn", "Unit"},
	}
}

// reviewBarItems is the last bar before stock moves.
func (s *ReceiveFormScreen) reviewBarItems(paging bool) []actionBarItem {
	if s.pending {
		return []actionBarItem{{"Esc", "Back to order"}}
	}
	items := []actionBarItem{{"Enter", "Receive"}, {"Esc", "Quantities"}}
	if len(s.serialUnits) > 0 {
		items = append(items, actionBarItem{"Ctrl+E", "Serials"})
	}
	if s.reviewRows() > 1 {
		items = append(items, actionBarItem{"UP/DN", "Lines"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// writeOffBar is the destructive confirm's bar. Ctrl+X is the commit and Enter
// is deliberately absent — see keyWriteOff.
func (s *ReceiveFormScreen) writeOffBar() []actionBarItem {
	if s.pending {
		return []actionBarItem{{"Esc", "Back to order"}}
	}
	return []actionBarItem{{"Ctrl+X", s.writeOffVerb()}, {"Esc", "Keep waiting"}}
}

// writeOffVerb names what Ctrl+X will do, in the words of the scope it is
// about. One expression, read by the bar and by the confirm's own body, so the
// key's claim and the sentence explaining it cannot come apart.
func (s *ReceiveFormScreen) writeOffVerb() string {
	if s.scope == receiveScopeOrder {
		return "Mark received"
	}
	return "Close line short"
}

// barCeiling is the tallest bar this phase can draw, and it is deliberately
// blind to headerRows: it is what the header allowance measures itself against,
// so a bar that asked the header how tall it was would close a loop.
func (s *ReceiveFormScreen) barCeiling() []actionBarItem {
	switch s.phase {
	case phaseLoading:
		return []actionBarItem{{"Esc", "Back to order"}}
	case phaseBlocked:
		return s.blockedBarItems(true)
	case phaseSerial:
		return s.serialBarCeiling()
	case phaseReview:
		return s.reviewBarItems(true)
	case phaseWriteOff:
		return s.writeOffBar()
	case phaseReopenPick:
		return s.reopenPickBar(true)
	case phaseReopenConfirm:
		return s.reopenConfirmBar()
	case phaseDone:
		return []actionBarItem{{"Enter/Esc", "Back to order"}, {"r", "Receive more"}}
	}
	return s.qtyBarCeiling()
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *ReceiveFormScreen) View() string {
	// Both the working line and the failure headline go through the LAYER's
	// status row, which flattens a multi-line body and bounds it to the pane.
	// That is not a nicety: this row is one unwrapped line of the frame, so an
	// nginx 502 page — seven lines, which omsapi.parseError hands over whole —
	// used to push the frame six rows over and clampToBox, which drops from the
	// BOTTOM, took the entire action bar with it. Every key on the screen
	// unnamed at once, with the operator staring at HTML.
	status := s.statusRow(s.pending || s.loading, s.workingLine(), "")
	if status == "" {
		status = s.statusRow(false, "", s.failLine())
	}
	body, cursor := s.body()
	return s.frameWrapped(s.headerLines(), body, cursor, status, s.bar())
}

// body is the phase's scrollable body and the row the window is anchored on,
// answered in ONE place so that a sweep asking what the frame draws cannot end
// up asking a different builder from the one View picks. (The paging guards
// still read qtyBody / reviewBody / blockedBody directly: those are questions
// about one phase in particular, asked only from arms that phase owns.)
//
// It exists because the rule these bodies are built to — EVERY LINE BELONGS TO
// A NAVIGABLE ROW, see qtyBody — was applied to the quantity form alone and the
// other bodies went on stranding their leads for a round, with nothing able to
// notice. A roster of body builders kept in a test is the hand-maintained list
// this project keeps being bitten by; a switch the sweep and the frame both
// read is not, and a phase added to the iota tomorrow arrives here or it draws
// nothing at all. TestReceive_EveryBodyLineBelongsToANavigableRow walks the
// phase cases through this.
func (s *ReceiveFormScreen) body() (*jdeLines, int) {
	switch s.phase {
	case phaseLoading:
		return s.loadingBody(), 0
	case phaseBlocked:
		return s.blockedBody(), s.rowCursor
	case phaseSerial:
		// The window follows the BOX HOLDING THE CARET. serialBody numbers its
		// three boxes as rows 0..2 — Serial, Lot, Expires, which is serialField —
		// and hangs everything that identifies the unit off row 0, so row 0's
		// block runs from the Serial box to the body's last line. Anchored there
		// whichever box held the caret, the window could only ever be drawn from
		// the Serial line: at 80x11, 80x12 and 80x14 the Lot box was below the
		// one row the body had, and every character typed into it redrew the
		// pane byte for byte (TestReceive_TheSerialBoxHoldingTheCaretIsOnThePane).
		// Past the last unit there is no box, and the frame is row 0 alone.
		if s.serialCursor >= len(s.serialUnits) {
			return s.serialBody(), 0
		}
		return s.serialBody(), s.serialField
	case phaseReview:
		return s.reviewBody(), s.rowCursor
	case phaseWriteOff:
		return s.writeOffBody(), 0
	case phaseReopenPick:
		return s.reopenPickBody(), s.reopenCursor
	case phaseReopenConfirm:
		return s.reopenConfirmBody(), 0
	case phaseDone:
		return s.doneBody(), 0
	}
	return s.qtyBody(), s.focused
}

// workingLine names the work AND the subject: "Submitting…" tells an operator
// nothing they could act on while a gateway thinks.
func (s *ReceiveFormScreen) workingLine() string {
	switch {
	case s.loading:
		return "Reading the receiving worksheet for " + s.orderName() + "…"
	case s.phase == phaseWriteOff && s.scope == receiveScopeOrder:
		return "Closing " + s.orderName() + " out…"
	case s.phase == phaseWriteOff:
		return fmt.Sprintf("Closing line %d of %s short…", s.scopeLine+1, s.orderName())
	case s.phase == phaseReopenConfirm:
		return fmt.Sprintf("Reopening settled line %d of %s…", s.reopenLine+1, s.orderName())
	}
	return "Booking the delivery against " + s.orderName() + "…"
}

// failLine is the headline the status row carries. Only the headline — the
// unbounded half is folded in the body.
func (s *ReceiveFormScreen) failLine() string { return s.failHead }

// ---------------------------------------------------------------------------
// Loading, and the frame that cannot receive
// ---------------------------------------------------------------------------

// loadingBody says what is being fetched and what it is for.
//
// Every line belongs to row 0, because no key on this phase moves a cursor at
// all: a line tagged jdeNoRow here is a line the layer would count behind an
// "↑ N more above" marker that nothing on the screen can act on.
func (s *ReceiveFormScreen) loadingBody() *jdeLines {
	l := &jdeLines{}
	width := s.bodyWidth()
	l.DeclareLead("loading")
	// Fitted, as every lead is: a pinned body's first line is the one line a
	// short pane keeps, and a literal handed to clampToBox loses its tail with
	// no mark — "Reading the receiv" at 49 columns reads as the whole sentence.
	l.AddRow(0, jdeIndent+StyleMuted.Render(receiveFit("Reading the receiving worksheet…",
		width, len(jdeIndent))))
	for _, line := range jdeCaveatLines(
		"It says which lines are still outstanding, what a scanner will read off each "+
			"one, and which items their serials belong to. Nothing can be received until "+
			"it lands.", width) {
		l.AddRow(0, line)
	}
	return l
}

// blockedRows is how many navigable rows the blocked frame has: the reason,
// then one per line of the order when there is a worksheet to list.
func (s *ReceiveFormScreen) blockedRows() int {
	if s.sheet == nil {
		return 1
	}
	return 1 + len(s.sheet.Lines)
}

// blockedReason is the server's own sentence for why this order may not be
// received against.
func (s *ReceiveFormScreen) blockedReason() string {
	if s.sheet != nil && s.sheet.UnavailableReason != "" {
		return s.sheet.UnavailableReason
	}
	// The pair is meant to arrive together and does; this is the frame refusing
	// to invent a sentence when it does not, rather than drawing a blank where
	// the explanation belongs.
	return "This order cannot be received against, and the server gave no reason."
}

// blockedBody draws "no receipt can be built here", and draws WHICH of the two
// reasons it is.
//
// A failed fetch and a server refusal are not the same fact and the operator's
// next move differs: could-not-tell means try again or fetch somebody, while a
// refusal is the server telling them something true about the order — it is
// still a draft, or receiving has already finished with it. Collapsing them
// into one "cannot receive" would leave an operator unable to tell which they
// had hit, which is the third standing rule of this codebase.
func (s *ReceiveFormScreen) blockedBody() *jdeLines {
	l := &jdeLines{}
	width := s.bodyWidth()

	if s.sheet == nil {
		l.DeclareLead("worksheet unreadable")
		l.AddRow(0, jdeIndent+StyleStatusError.Render(receiveFit(
			"The receiving worksheet could not be read.", width, len(jdeIndent))))
		for _, line := range jdeCaveatLines(
			"Nothing is known about this order's lines, so nothing can be received "+
				"against it — this is not the same as an order with nothing left to "+
				"receive. The reason is above. r tries again.", width) {
			l.AddRow(0, line)
		}
		return l
	}

	if s.blockedRows() == 1 {
		// No line list under it, so nothing moves the window off this block.
		l.DeclareLead("cannot receive, no lines")
	}
	l.AddRow(0, jdeIndent+StyleStatusWarn.Render(
		receiveFit(s.orderName()+" · "+s.sheet.StatusLabel, width, len(jdeIndent))))
	// The server's REASON is not drawn here. It used to follow the line above,
	// as the tail of row 0's block — and Window keeps a block's START, while
	// Down walks to the line list rather than into the tail, so from 80x12 to
	// 80x19 the operator refused by this frame read "PO-1001 · Draft" and a
	// list of lines and never "Send it to the supplier": a refusal they could
	// not act on. It is the note block's standing fact now (standingNote),
	// which is on the pane from every row.
	if len(s.sheet.Lines) == 0 {
		return l
	}
	l.AddRow(0, "")
	// FOLDED, as every sentence on these frames is (receiveCaveatLines and its
	// siblings): written straight to the pane, this heading and the four other
	// literals TestReceive_NothingOverflowsThePane found lost their tails to
	// clampToBox, unmarked, at every width from 49 to 67 columns.
	for _, line := range jdeCaveatLines("What the order's lines say:", width) {
		l.AddRow(0, line)
	}
	for i, line := range s.sheet.Lines {
		row := i + 1
		if i > 0 {
			// EVERY separator closes the block above it rather than opening the
			// one below. Window keeps a block's START when the block will not
			// fit, so a blank tagged to the block BELOW is the first line that
			// block draws — a short window then shows a blank where the row the
			// cursor just reached should be.
			l.AddRow(row-1, "")
		}
		s.addSheetLineBlock(l, row, i+1, line, width)
	}
	return l
}

// addSheetLineBlock draws one line of the order READ-ONLY: what it is, and
// where receiving has got to with it.
func (s *ReceiveFormScreen) addSheetLineBlock(l *jdeLines, row, num int, line omsapi.ReceivingLine, width int) {
	l.AddRow(row, jdeIndent+s.lineHeading(num, line, s.rowCursor == row, width))
	for _, tok := range jdeWrapTokens(receiveLineTokens(line), receiveMetaIndent, width) {
		l.AddRow(row, tok)
	}
	if line.IsClosedShort && line.ClosedShortReason != "" {
		for _, cl := range receiveCaveatLines("closed short: "+line.ClosedShortReason, StyleMuted, width) {
			l.AddRow(row, cl)
		}
	}
	for _, cl := range receiveSerialGapLines(line, width) {
		l.AddRow(row, cl)
	}
}

// receiveFit bounds one row's text to what the pane leaves after an indent.
//
// An UNSIZED pane means "do not truncate" everywhere in this layer, and that is
// why this exists rather than a bare fitCell: fitCell(s, 0) returns the empty
// string, so passing it an unsized pane's room would blank the row instead of
// leaving it whole. Cells, not runes, because every width on these screens is
// what the terminal draws.
func receiveFit(text string, width, indent int) string {
	if width <= 0 {
		return text
	}
	room := width - indent
	if room < 1 {
		room = 1
	}
	return fitCell(text, room)
}

// ---------------------------------------------------------------------------
// The quantity form
// ---------------------------------------------------------------------------

// qtyBody is the scrollable body of the receipt: the scan row, the delivery
// rows, one block per line a receipt may name, the notes row, and — hanging off
// the notes row — the lines a receipt may NOT name.
//
// EVERY LINE BELONGS TO A NAVIGABLE ROW. That is the rule this body is built
// to, and it is a rule rather than a tidiness because of what breaks without
// it. jdeLines.Window anchors the window on the CURSOR's block, and no key on
// this screen moves a cursor above the first row — up wraps to the last row,
// which moves the window further down, and jdePageCursor clamps at 0, so pgup
// answers "already at the first row". A line tagged jdeNoRow ahead of the first
// block is therefore a line NO key can bring onto the pane, while the layer
// goes on drawing "↑ N more above" and counting it. Measured at the canonical
// 80x24 with one kit line: the pane opened on "↑ 5 more above" with the order
// heading and the whole kit caveat among the five — the sentence that stops
// "received 2" being read as two of the thing named on the line, off the pane,
// with the frame saying it was up there and nothing able to fetch it.
//
// Every line of a block is tagged with that block's navigable ROW, so
// jdeLines.Window keeps the name, the readings, the quantity box and the kit
// breakdown on screen TOGETHER.
func (s *ReceiveFormScreen) qtyBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	s.addScanBlock(l, width, lw)
	l.AddRow(receiveRowScan, "")
	s.addDeliveryBlock(l, width, lw)
	l.AddRow(receiveRowDelivered, "")

	notesRow := s.notesRow()
	notes := []jdeField{{
		Label:   "Notes",
		Kind:    jdeText,
		Input:   &s.notes,
		Width:   40,
		Hint:    "optional",
		Focused: s.caretOn(notesRow),
	}}

	if len(s.qty) == 0 {
		// The FIELD leads its block — a block that will not fit keeps its
		// START, and an operator typing into a field they cannot see is the
		// reported-hang class this screen exists to remove — and the settled
		// lines hang off it.
		//
		// What this block does NOT carry is the explanation of the whole form,
		// and it used to. The sentence saying why nothing here can take a
		// receipt was the TAIL of this block: the last of five rows, after a
		// field that can never be submitted (there is no receipt to send it
		// with). So the frame the form opens on — the cursor on Scan — drew it
		// nowhere up to 80x30, and standing on this row a short pane kept
		// "Notes ..... optional" and dropped the why. It is a fact about the
		// FORM, so it is pinned in the header instead (standingNote), where it
		// is on the pane from every row.
		l.AddFittedFields(notes, lw, width, notesRow)
		s.addClosedLines(l, notesRow, width)
		return l
	}

	for i := range s.qty {
		if i > 0 {
			// EVERY separator closes the block above it rather than opening
			// the one below, and the two are not interchangeable. Window keeps
			// a block's START when the block will not fit, so a blank tagged
			// to the block BELOW is the first line that block draws: at 80x17
			// with three lines the body window is one row, and pressing Down
			// drew that blank — a pane of "↑ 4 more above", nothing, "↓ 8 more
			// below", with not one word about the line the cursor had just
			// moved to. At the TAIL of the block above it costs nothing, since
			// what a short window drops there is a blank.
			l.AddRow(receiveRowFirstLine+i-1, "")
		}
		s.addLineBlock(l, i, lw, width)
	}
	// The same rule for the last block: this blank closes it rather than
	// opening the notes row, which would otherwise draw a blank where the
	// FOCUSED notes field belongs.
	l.AddRow(receiveRowFirstLine+len(s.qty)-1, "")
	l.AddFittedFields(notes, lw, width, notesRow)
	s.addClosedLines(l, notesRow, width)
	return l
}

// addScanBlock draws the row a barcode fires into.
//
// The FIELD leads and what it is for follows, because Window keeps a block's
// START: a hint drawn above the box is a hint that pushes the box off a short
// pane, and this is the box a scanner is already firing into.
func (s *ReceiveFormScreen) addScanBlock(l *jdeLines, width, lw int) {
	l.AddFittedFields([]jdeField{{
		Label:   "Scan",
		Kind:    jdeText,
		Input:   &s.scan,
		Width:   28,
		Focused: s.caretOn(receiveRowScan),
	}}, lw, width, receiveRowScan)
	for _, line := range receiveCaveatLines(s.scanHint(), StyleMuted, width) {
		l.AddRow(receiveRowScan, line)
	}
}

// scanHint says what the scan row can do HERE, which is not the same sentence
// on every order.
//
// An order whose lines carry no scannable identifier at all — asset and
// freeform lines contribute none — cannot be scanned to, and a hint promising
// otherwise there would be a claim the code does not honour on the row an
// operator reaches for first.
func (s *ReceiveFormScreen) scanHint() string {
	// The count is of lines Enter can JUMP TO, which is not the same as lines
	// that carry a code. A settled line keeps its scan_codes, and findLine's
	// `live` filter is what decides: a code that resolves only to settled lines
	// leaves the cursor where it was and answers "…is settled line N, closed
	// short — no receipt". Counting over the worksheet said "1 of 2 lines can
	// be scanned to" on an order whose one coded line was closed short, beside
	// a promise that Enter jumps to it — the same sheet-lines-versus-form-lines
	// split scanMatches was corrected for, in the sentence that was left behind.
	//
	// Derived from the SAME walk findLine makes rather than a second notion of
	// codeable, so the promise and the key cannot part company: scanMatches
	// answers what a code resolves to, and `live` is the half of that answer a
	// receipt can be typed against.
	reachable := 0
	for i := range s.lines {
		if len(s.lines[i].sheet.ScanCodes) > 0 {
			reachable++
		}
	}
	if reachable == 0 {
		// The TAIL is decided first, because a way out that cannot be taken is
		// worse than none, and it comes off linePickWayOut — the one place that
		// question is answered on this screen — rather than being spelled here.
		// Both sentences below carried a fixed "pick the line with up/dn" and
		// so both pointed at a picker with nothing in it.
		out := s.linePickWayOut()
		// TWO different nothings above that tail, and they are not the same
		// next move. An order that carries no code AT ALL simply cannot be
		// scanned to; an order whose only coded lines are settled CAN be
		// scanned — the scan resolves and says which settled line it hit — it
		// just cannot reach a box. Folding the second into the first would be
		// found-nothing standing in for could-not-tell one sentence later.
		if s.codedLines() == 0 {
			return "No line on this order carries a scannable code — " + out + "."
		}
		return "Only settled lines here carry a code, so no scan reaches a box — " + out + "."
	}
	return fmt.Sprintf("Scan or type a code from the box; enter jumps to its line. %d of %d "+
		"lines can be scanned to.", reachable, len(s.lines))
}

// addDeliveryBlock draws what the parcel was, which is recorded and never
// interpreted.
func (s *ReceiveFormScreen) addDeliveryBlock(l *jdeLines, width, lw int) {
	l.AddFittedFields([]jdeField{{
		Label:   "Tracking",
		Kind:    jdeText,
		Input:   &s.tracking,
		Width:   28,
		Focused: s.caretOn(receiveRowTracking),
	}}, lw, width, receiveRowTracking)
	for _, line := range receiveCaveatLines(
		"The carrier's barcode, stored exactly as scanned. No transit time is worked "+
			"out from it.", StyleMuted, width) {
		l.AddRow(receiveRowTracking, line)
	}
	l.AddRow(receiveRowTracking, "")
	l.AddFittedFields([]jdeField{{
		Label:   "Carrier",
		Kind:    jdeText,
		Input:   &s.carrier,
		Width:   20,
		Hint:    "optional",
		Focused: s.caretOn(receiveRowCarrier),
	}}, lw, width, receiveRowCarrier)
	l.AddRow(receiveRowCarrier, "")
	l.AddFittedFields([]jdeField{{
		Label:   "Delivered",
		Kind:    jdeText,
		Input:   &s.delivered,
		Width:   12,
		Hint:    "YYYY-MM-DD · blank = today",
		Focused: s.caretOn(receiveRowDelivered),
	}}, lw, width, receiveRowDelivered)
	for _, line := range receiveCaveatLines(
		"The day the goods ARRIVED, which is not always the day they are booked in.",
		StyleMuted, width) {
		l.AddRow(receiveRowDelivered, line)
	}
}

// addClosedLines lists the lines a receipt may not name, under the notes row.
//
// They are on the pane at all because "which lines am I still waiting on?" is
// only answerable when the settled ones are visible too — a line that vanished
// reads as a line that was never ordered. They hang off the NOTES row because
// they carry no box of their own, and the notes field leads its block so that
// what a short pane drops is this tail rather than the field.
func (s *ReceiveFormScreen) addClosedLines(l *jdeLines, row, width int) {
	if len(s.closed) == 0 {
		return
	}
	l.AddRow(row, "")
	// SETTLED, said out loud, because a scan that lands on one of these answers
	// with "settled line 2" and the operator has to be able to find the list
	// that counts to two. It was "N lines cannot take a receipt:", which names
	// no list at all, and a bare "2" in a note beside a form whose own lines
	// are numbered 1..n is the ambiguity this heading exists to remove.
	for _, line := range jdeCaveatLines(fmt.Sprintf("%d settled %s cannot take a receipt:",
		len(s.closed), plural("line", len(s.closed))), width) {
		l.AddRow(row, line)
	}
	// The STATE leads and the label follows. receiveFit clips from the right,
	// and written label-first a long label took the state with it — `1. Flat
	// washer M3, A2 stainless…` on a voided line, which says neither that it
	// was voided nor that it was closed short, the two facts that put it on
	// this list and that an operator acts on differently. Whatever must
	// survive must lead, as lineHeading's tags already do on the live lines.
	for i, line := range s.closed {
		l.AddRow(row, receiveMetaIndent+StyleMuted.Render(
			receiveFit(fmt.Sprintf("%d. %s — %s", i+1, receiveStateLabel(line.sheet),
				line.sheet.Label), width, len(receiveMetaIndent))))
	}
}

// receiveStateLabel is the server's own words for where receiving has got to
// with a line, never a re-derivation of them. Asking "is received < ordered?"
// here would call a line closed short a partial one, and closed short is the
// one state that means somebody DECIDED.
func receiveStateLabel(line omsapi.ReceivingLine) string {
	if line.ReceiptStateLabel != "" {
		return line.ReceiptStateLabel
	}
	if line.ReceiptState != "" {
		return line.ReceiptState
	}
	return "state unknown"
}

// caretOn reports whether row i is the one that may be TYPED INTO right now,
// which is not the same question as which row the operator is standing on.
//
// jdeFieldArea draws a focused text row as a solid reverse-video field — the
// layer's strongest "you are standing here and may type" signal — and while a
// request is out every key but Esc declines, so a row left highlighted through
// the freeze is the bar-honesty rule broken in its most visual form: the pane
// invites the one thing the screen has just blurred the box to refuse.
//
// Nothing is lost by dropping the fill. The row's number keeps its focused
// style, and its name, its readings and its typed quantity all stay drawn, so
// the operator keeps their place — and the frozen bar plus the working status
// row already say what state the screen is in.
func (s *ReceiveFormScreen) caretOn(i int) bool {
	return s.focused == i && !s.pending
}

// What a receivable line has to say about itself beyond its readings — that it
// is a kit, that its serials are coming, that units of it are already on the
// shelf with no serial against them — is drawn on the LINE's own navigable row
// rather than once at the top of the form, which is the whole of the fix
// qtyBody's comment records: a sentence above the first row is a sentence no key
// can reach once the body overflows. It comes in two halves because they sit on
// different sides of the kit credit block (addLineBlock's sacrifice order).

// kitCaveatLines is the one sentence that must sit with the credit block below
// it, so the warning and the numbers it is about are one thing on the pane.
// Wrapped rather than clipped, and WARN rather than muted: this is the sentence
// that stops "received 2" being read as two of the thing named on the line.
func kitCaveatLines(line omsapi.ReceivingLine, width int) []string {
	if !line.IsKitLine {
		return nil
	}
	return receiveCaveatLines(receiveKitCaveat, StyleStatusWarn, width)
}

// serialCaveatLines is what the line has to say about SERIALS, and it comes
// last in the block on purpose.
//
// Window keeps a block's START, so the tail of a block that outruns the pane is
// what an operator loses — and of everything a line block carries, this is the
// part they can do without right now: it describes a phase that has not started
// and units already on the shelf. The kit credit preview cannot be the tail,
// because it is what the number being typed MEANS. At 80 columns with a kit
// line the two together are longer than the pane, so the order between them is
// a real choice and this is it.
func (s *ReceiveFormScreen) serialCaveatLines(line omsapi.ReceivingLine, width int) []string {
	out := receiveSerialTargetLines(line, width)
	return append(out, receiveSerialGapLines(line, width)...)
}

// receiveSerialTargetLines says which identities this line's serials will be
// asked for, and it names them.
//
// Naming them is the point on a KIT line: the serials go to the kit's
// COMPONENTS and never to the kit, so an operator who reads only "serials will
// be prompted" has been told the one thing that is true of both and none of
// what distinguishes them. The list is the server's `serial_targets` verbatim,
// which is the same list the receipt validates against.
func receiveSerialTargetLines(line omsapi.ReceivingLine, width int) []string {
	if len(line.SerialTargets) == 0 {
		return nil
	}
	lead := "Serialized: a serial is asked for each unit before the receipt is sent."
	if line.IsKitLine {
		lead = "Serialized COMPONENTS: the serials go to these items, never to the kit."
	}
	out := receiveCaveatLines(lead, StyleMuted, width)
	for _, t := range line.SerialTargets {
		name := t.ItemName
		if name == "" {
			name = t.Item
		}
		if t.ItemSKU != "" {
			name += " (" + t.ItemSKU + ")"
		}
		out = append(out, receiveCaveatLines(
			fmt.Sprintf("· %d per full order × %s", t.Quantity, name), StyleMuted, width)...)
	}
	return out
}

// receiveSerialGapLines surfaces serials_outstanding on the line it belongs to.
//
// This is the fact the old ban on serialized kit components existed to prevent
// and now reports instead: units of a serialized identity that a receipt has
// already put on the shelf carrying no serial number. It is not an error and it
// does not block anything — it is work somebody can still finish — but it is
// invisible unless a client draws it, which is why it is drawn on every line
// that has one and rolled up on the summary.
func receiveSerialGapLines(line omsapi.ReceivingLine, width int) []string {
	if line.SerialsOutstanding <= 0 {
		return nil
	}
	lead := fmt.Sprintf("%d %s already in stock with no serial recorded.",
		line.SerialsOutstanding, plural("unit", line.SerialsOutstanding))
	out := receiveCaveatLines(lead, StyleStatusWarn, width)
	for _, g := range line.SerialGap {
		if g.Outstanding <= 0 {
			continue
		}
		name := g.ItemName
		if name == "" {
			name = g.Item
		}
		out = append(out, receiveCaveatLines(
			fmt.Sprintf("· %s: %d of %d captured", name, g.Recorded, g.Expected),
			StyleMuted, width)...)
	}
	return out
}

// receiveCaveatLines folds one caveat under the row it belongs to, indented to
// the line's own content the way its readings and its credit block are: it is a
// continuation of the row above it, not a value hanging off a label.
func receiveCaveatLines(text string, style lipgloss.Style, width int) []string {
	// A width of 0 means the pane has not been sized yet, which everywhere in
	// this layer means "do not truncate".
	room := 0
	if width > 0 {
		if room = width - len(receiveMetaIndent); room < 1 {
			room = 1
		}
	}
	wrapped := jdeWrapNote(text, room)
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, receiveMetaIndent+style.Render(line))
	}
	return out
}

// addLineBlock draws one receivable line: its name, its readings, the quantity
// box, what the number typed there would mean, and — for a kit — what it would
// credit.
func (s *ReceiveFormScreen) addLineBlock(l *jdeLines, i, lw, width int) {
	line := s.lines[i]
	row := receiveRowFirstLine + i
	// The heading's marker and the box's fill answer DIFFERENT questions, which
	// is why they are asked separately here. The number stays styled for
	// whichever row the cursor is on — that is the operator's PLACE, and a
	// freeze that took it away would leave them hunting for it when the request
	// answers — while the box's reverse-video fill says "type here", which is
	// false for as long as a request is out (caretOn).
	l.AddRow(row, jdeIndent+s.lineHeading(i+1, line.sheet, s.focused == row, width))
	qty := jdeField{
		Label:   "Quantity",
		Kind:    jdeText,
		Input:   &s.qty[i],
		Width:   8,
		Focused: s.caretOn(row),
	}
	if line.sheet.IsKitLine {
		// The unit of the box, right beside the box. "ordered 2 kits" says it
		// once on the row above; this says it where the number is typed.
		qty.Hint = "kits"
	}
	l.AddFittedFields([]jdeField{qty}, lw, width, row)
	// AFTER the box, never before it — the READINGS included. These lines are
	// part of this row's block, so Window keeps them with the box; but a block
	// too tall for the pane keeps its START, so every row placed ahead of the
	// box is a row the box is pushed down by, and the box is what a scanner is
	// firing into.
	//
	// The readings used to lead, on the reasoning that they are what the
	// operator reads to decide the quantity. They cost ONE line when they were
	// "ordered N · received N · pending N" and they cost two once the receipt
	// STATE and the variance joined them, which put the box fourth in its own
	// block: at 80x25 with a kit on the order the window is four rows and it
	// drew the heading, both reading lines and the "↓ more below" marker, with
	// the box the cursor was on off the pane.
	//
	// What follows the box is in a SACRIFICE ORDER, and it is written down
	// because a block taller than the window loses its tail with NO key able to
	// fetch it — Window keeps a block's start and nothing scrolls inside one.
	// So the block runs from what the operator cannot do without to what they
	// can:
	//
	//	what the typed number MEANS   over or short, against the order
	//	what a KIT receipt credits    the caveat, then the components
	//	the line's own readings       which the row above already restates
	//	the SERIAL story              a phase that has not started, and units
	//	                              already on the shelf
	//
	// Measured: at 80x30 with the kit fixture the window is eleven rows, and
	// with the readings ahead of the credit the second component sat off the
	// pane — on the block whose whole point is saying what a kit receipt puts
	// into stock.
	for _, cl := range s.overShortLines(i, width) {
		l.AddRow(row, cl)
	}
	for _, cl := range kitCaveatLines(line.sheet, width) {
		l.AddRow(row, cl)
	}
	// The breakdown sits directly under the caveat it belongs to, and
	// recomputes from what is currently typed in the box above them.
	for _, kl := range s.kitCreditLines(line, s.qty[i].Value()) {
		l.AddRow(row, kl)
	}
	for _, tok := range jdeWrapTokens(receiveLineTokens(line.sheet), receiveMetaIndent, width) {
		l.AddRow(row, tok)
	}
	for _, cl := range s.serialCaveatLines(line.sheet, width) {
		l.AddRow(row, cl)
	}
}

// overShortLines is what the number currently in the box would MEAN, drawn
// under the box as it is typed.
//
// This is where the mismatch is raised, and raising it here is deliberate: the
// contract asks a client to tell the operator before the receipt goes, because
// a typo is cheaper to fix than a vendor query. Nothing here changes the
// figure. An over-receipt is sent exactly as typed and comes back flagged; what
// this row does is make sure the operator meant it.
//
// SHORT is worded as OUTSTANDING and never as a mismatch, because those are
// different facts on this API: receiving 8 of 10 leaves 2 still expected, which
// may simply be on a backorder, and only an explicit close-short says the
// balance is not coming. Calling every partial receipt short would raise a
// vendor query on every backorder.
func (s *ReceiveFormScreen) overShortLines(i, width int) []string {
	raw := strings.TrimSpace(s.qty[i].Value())
	if raw == "" {
		return nil
	}
	q, ok := receiveQuantity(raw)
	if !ok {
		return receiveCaveatLines(
			poQuotedClip(raw, receiveQuotedCells)+" is not a whole number, so this line cannot be sent.",
			StyleStatusError, width)
	}
	if q == 0 {
		return receiveCaveatLines("A zero books nothing — this line is left out of the receipt.",
			StyleMuted, width)
	}
	line := s.lines[i].sheet
	total := line.QuantityReceived + q
	switch {
	case total > line.QuantityOrdered:
		return receiveCaveatLines(fmt.Sprintf(
			"%d of %d ordered — %d OVER, recorded and flagged, never rounded.",
			total, line.QuantityOrdered, total-line.QuantityOrdered), StyleStatusWarn, width)
	case total < line.QuantityOrdered:
		// No "ctrl+k writes the rest off" here. The bar names Ctrl+K wherever
		// it acts, and a hint repeating it costs a row inside a block whose
		// tail is already what a short pane drops.
		return receiveCaveatLines(fmt.Sprintf(
			"%d of %d ordered — %d still outstanding.",
			total, line.QuantityOrdered, line.QuantityOrdered-total), StyleMuted, width)
	}
	return receiveCaveatLines("This settles the line: the whole order will have arrived.",
		StyleStatusOK, width)
}

// lineHeading is a line's name row: the number, the kit tag, the state the
// server says it is in, then as much of the label as the pane has left.
//
// The order is deliberate. The tags lead because they are what change the
// meaning of the row below them, and a tag after a long name is the first thing
// clampToBox cuts. The name is then FITTED rather than left to overrun, because
// this row is the one an operator reads to decide which line they are typing
// into, and a name silently cut at the pane edge reads as a different (shorter)
// line. The number is styled, never dropped: it is what the refusal messages
// name ("line 2: a quantity is…").
func (s *ReceiveFormScreen) lineHeading(num int, line omsapi.ReceivingLine, focused bool, width int) string {
	lead := StyleMuted.Render(fmt.Sprintf("%-2d ", num))
	if focused {
		lead = StyleJDELabelFocused.Render(fmt.Sprintf("%-2d ", num))
	}
	var tags []string
	if line.IsKitLine {
		tags = append(tags, StyleStatusWarn.Render(poKitTag))
	}
	if mark, style, ok := receiveStateTag(line); ok {
		tags = append(tags, style.Render(mark))
	}
	prefix := ""
	if len(tags) > 0 {
		prefix = strings.Join(tags, " ") + " "
	}
	label := line.Label
	if width > 0 {
		room := width - len(jdeIndent) - 3 - lipgloss.Width(prefix)
		if room > 0 {
			label = fitCell(label, room)
		}
	}
	return lead + prefix + label
}

// receiveStateTag is the short marker a line's receipt state earns on its name
// row, or no marker at all when the line has not been touched.
//
// A tag for `not_received` would be a mark on every line of a fresh order,
// which is a mark that says nothing; the states worth a glance are the ones
// where something has already happened to the line.
func receiveStateTag(line omsapi.ReceivingLine) (string, lipgloss.Style, bool) {
	switch line.ReceiptState {
	case omsapi.ReceiptStatePartially:
		return "[part]", StyleMuted, true
	case omsapi.ReceiptStateReceived:
		return "[done]", StyleStatusOK, true
	case omsapi.ReceiptStateOverReceived:
		return "[over]", StyleStatusWarn, true
	case omsapi.ReceiptStateClosedShort:
		return "[short]", StyleStatusWarn, true
	case omsapi.ReceiptStateVoided:
		return "[void]", StyleMuted, true
	}
	return "", StyleMuted, false
}

// receiveLineTokens are a line's readings, laid out under its name by
// jdeWrapTokens — which WRAPS rather than trimming, because the reading an
// ellipsis would eat is the LAST one, and that is where the variance sits.
func receiveLineTokens(line omsapi.ReceivingLine) []jdeToken {
	// "ordered 2 kits" rather than a separate "quantities are kits" clause: it
	// says the same thing where the number is, and it fits an 80-column pane,
	// which the clause did not.
	unit := ""
	if line.IsKitLine {
		unit = " " + plural("kit", line.QuantityOrdered)
	}
	toks := []jdeToken{
		{fmt.Sprintf("ordered %d%s", line.QuantityOrdered, unit), StyleMuted},
		{fmt.Sprintf("received %d", line.QuantityReceived), StyleMuted},
	}
	if line.QuantityPending > 0 {
		toks = append(toks, jdeToken{fmt.Sprintf("pending %d", line.QuantityPending), StyleMuted})
	}
	// The VARIANCE is the server's signed figure, and the two signs are drawn
	// under DIFFERENT conditions because they are different facts.
	//
	// OVER is always drawn: more arrived than was ordered, which is a mismatch
	// the moment it happens and stays one however the line ends.
	//
	// SHORT waits for the line to be SETTLED, and that gate is the receiving
	// contract's own rule rather than a nicety. On a line still being waited on,
	// less-than-ordered is OUTSTANDING and not a mismatch — the goods may still
	// be coming — and `pending` two tokens up already carries that figure; a
	// "6 short" beside "pending 6" would raise a vendor query on every ordinary
	// backorder. Once the line settles, quantity_pending has floored the
	// shortfall away, so a line closed two short and a line two over both read
	// 0 pending and the variance is the only reading that tells them apart.
	// That last case is what this token exists for.
	if line.QuantityVariance > 0 {
		toks = append(toks, jdeToken{fmt.Sprintf("%d over", line.QuantityVariance), StyleStatusWarn})
	} else if line.QuantityVariance < 0 && line.IsSettled {
		toks = append(toks, jdeToken{fmt.Sprintf("%d short", -line.QuantityVariance), StyleStatusWarn})
	}
	// A line that was closed short in ERROR and taken back reads as outstanding
	// again, which is correct and is not the whole story: the write-off stays
	// on the record beside the correction, and an operator receiving against
	// this line is receiving against one somebody has already got wrong once.
	if line.WasReopened {
		toks = append(toks, jdeToken{"reopened", StyleStatusWarn})
	}
	toks = append(toks, jdeToken{receiveStateLabel(line), StyleMuted})
	return toks
}

// kitCreditLines is the breakdown drawn under a kit line's quantity box: what
// receiving the quantity currently TYPED there would credit.
//
// An empty or unparseable box shows the per-kit ratio instead of a row of
// zeroes — before a quantity is entered the useful reading is "one kit is these
// five things", and a breakdown that read "0 × cyan ink" would say the opposite
// of what it means. Nothing at all is drawn for a non-kit line.
func (s *ReceiveFormScreen) kitCreditLines(line receiveLine, typed string) []string {
	if !line.sheet.IsKitLine {
		return nil
	}
	lead := "per kit"
	kits := 0
	if qty, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil && qty > 0 {
		lead = fmt.Sprintf("receiving %d %s credits", qty, plural("kit", qty))
		kits = qty
	}
	return poKitCreditBlock(line.kit, lead, receiveMetaIndent, s.bodyWidth(), kits)
}

// ---------------------------------------------------------------------------
// Serial capture
// ---------------------------------------------------------------------------

// serialBody is the capture phase: the boxes a serial, a lot and an expiry are
// typed into, then what those boxes are FOR.
//
// The FIELDS come first and everything identifying them follows, which is the
// opposite of how a form usually reads and is the only order this phase can
// afford. jdeLines.Window keeps a block's START when the block will not fit, so
// whatever leads the block is what a short pane keeps — and NO key on this
// phase moves a cursor between blocks: body() anchors the window on row 0 and
// every line here belongs to it. So there is exactly one block, nothing can
// ever sit above the window, and the one choice left is which end of that block
// a short pane keeps. A scanner firing a barcode into a field the operator
// cannot see, and cannot check before Enter commits it, is the worse loss.
//
// The IDENTITY is drawn immediately under the boxes and before the counters,
// because on a kit line it is the fact that makes the capture correct: the
// serial belongs to a COMPONENT, and an operator who cannot see which one is
// scanning into a field they have no way to check.
func (s *ReceiveFormScreen) serialBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()
	total := len(s.serialUnits)

	if s.serialCursor >= total {
		// Nothing left to capture, so there is no box to lead with. Both lines
		// still belong to row 0: a line tagged jdeNoRow is a line no key can
		// bring back, and this phase has no key that moves a cursor at all.
		l.DeclareLead("serial answered")
		l.AddRow(0, jdeIndent+StyleStatusOK.Render(receiveFit(
			"Every unit has been answered.", width, len(jdeIndent))))
		for _, line := range jdeCaveatLines(s.captureSummary(), width) {
			l.AddRow(0, line)
		}
		return l
	}

	unit := s.serialUnits[s.serialCursor]
	l.DeclareLead("serial box")
	l.AddFittedFields([]jdeField{
		{
			Label: "Serial", Kind: jdeText, Input: &s.serialInput, Width: 30,
			// No hint. The BAR carries this row's one fact and carries it
			// dynamically — Enter reads "Skip unit" while the box is empty and
			// "Save & next" once it is not — so a hint saying the same thing
			// would be a second, static claim about the same key, and it cost
			// the field twelve columns of a 51-column pane to make.
			Focused: s.serialField == receiveSerialNumber,
		},
		{
			Label: "Lot", Kind: jdeText, Input: &s.lotInput, Width: 20,
			Hint: "optional", Focused: s.serialField == receiveSerialLot,
		},
		{
			Label: "Expires", Kind: jdeText, Input: &s.expiryInput, Width: 12,
			Hint: "optional · YYYY-MM-DD", Focused: s.serialField == receiveSerialExpiry,
		},
	}, lw, width, 0)

	l.AddRow(0, "")
	l.AddRow(0, jdeIndent+StyleJDEHeading.Render(receiveFit(
		receiveUnitIdentity(unit), width, len(jdeIndent))))
	if unit.itemName != "" && unit.itemName != unit.lineLabel {
		// The LINE is named under the identity rather than instead of it. On a
		// kit line the two differ — the serial goes to a component, the receipt
		// goes to the kit's line — and an operator who sees only one of them
		// cannot tell which of the two they are looking at.
		l.AddRow(0, receiveMetaIndent+StyleMuted.Render(receiveFit(
			"on line: "+unit.lineLabel, width, len(receiveMetaIndent))))
	}
	for _, line := range receiveCaveatLines(fmt.Sprintf(
		"unit %d of %d for this item", unit.unitNo, unit.unitTot), StyleMuted, width) {
		l.AddRow(0, line)
	}
	for _, line := range receiveCaveatLines(fmt.Sprintf(
		"capture %d of %d · %d serial(s) so far", s.serialCursor+1, total, s.capturedCount()),
		StyleMuted, width) {
		l.AddRow(0, line)
	}
	for _, line := range receiveCaveatLines(
		"A blank serial passes the unit over — the goods are still received and the "+
			"gap comes back as an outstanding serial.", StyleMuted, width) {
		l.AddRow(0, line)
	}
	return l
}

// receiveUnitIdentity is the item a serial is being written against, named the
// way the operator has to check it: the name, then the SKU.
func receiveUnitIdentity(u serialUnit) string {
	name := u.itemName
	if name == "" {
		name = u.itemID
	}
	if u.itemSKU != "" {
		name += " · " + u.itemSKU
	}
	return name
}

// captureSummary is what capture came to, in one sentence.
func (s *ReceiveFormScreen) captureSummary() string {
	got := s.capturedCount()
	out := fmt.Sprintf("%d of %d %s carry a serial.", got, len(s.serialUnits),
		plural("unit", len(s.serialUnits)))
	if gap := len(s.serialUnits) - got; gap > 0 {
		out += fmt.Sprintf(" The other %d will be received without one and reported as "+
			"outstanding serials.", gap)
	}
	return out
}

// ---------------------------------------------------------------------------
// The review
// ---------------------------------------------------------------------------

// reviewRows is one navigable row per line the receipt names, plus one for the
// delivery block that closes it.
func (s *ReceiveFormScreen) reviewRows() int {
	if n := s.plannedLines(); n > 0 {
		return n + 1
	}
	return 1
}

// reviewBody is the last thing an operator sees before stock moves: exactly
// what the server is about to be told.
//
// It reads off buildReceipt rather than off the boxes, so the frame and the
// request cannot describe different receipts — the one thing a confirm screen
// must never get wrong.
func (s *ReceiveFormScreen) reviewBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()
	req := s.buildReceipt()

	row := 0
	for i, line := range s.lines {
		q, ok := receiveQuantity(s.qty[i].Value())
		if !ok || q <= 0 {
			continue
		}
		if row > 0 {
			l.AddRow(row-1, "")
		}
		l.AddRow(row, jdeIndent+s.lineHeading(i+1, line.sheet, s.rowCursor == row, width))
		total := line.sheet.QuantityReceived + q
		for _, cl := range receiveCaveatLines(fmt.Sprintf(
			"receiving %d · %d of %d ordered once it lands", q, total, line.sheet.QuantityOrdered),
			StyleMuted, width) {
			l.AddRow(row, cl)
		}
		if extra := total - line.sheet.QuantityOrdered; extra > 0 {
			for _, cl := range receiveCaveatLines(fmt.Sprintf(
				"%d OVER the order. It goes as typed and comes back flagged — nothing is "+
					"rounded down.", extra), StyleStatusWarn, width) {
				l.AddRow(row, cl)
			}
		} else if extra < 0 {
			for _, cl := range receiveCaveatLines(fmt.Sprintf(
				"%d still outstanding afterwards — the line stays open for it.", -extra),
				StyleMuted, width) {
				l.AddRow(row, cl)
			}
		}
		for _, cl := range s.reviewSerialLines(i, width) {
			l.AddRow(row, cl)
		}
		row++
	}

	if row == 0 {
		// Unreachable through the bar and drawn rather than left blank: a
		// review with no lines is a frame that would otherwise say nothing at
		// all about why Enter is about to refuse.
		//
		// It declares NO lead, and that is the pinned-body sweep's exclusion
		// rather than an omission: a declaration no swept state can reach is one
		// TestReceive_EveryLeadDeclarationIsJudgedOnAPinnedBody reports as
		// unswept, and no key sequence reaches this frame —
		// the review is entered only past entryState, which a receipt naming no
		// line cannot pass. Its single line is its lead by construction.
		l.AddRow(0, jdeIndent+StyleStatusWarn.Render("This receipt names no line."))
		return l
	}

	l.AddRow(row-1, "")
	// The serials a re-enrolment dropped are reported HERE and not only in the
	// note that announced them. A note expires from the status bar in four
	// seconds and is retired by the next keypress; this is the frame the
	// operator confirms the receipt on, and "some of what I typed is not going"
	// is exactly the fact that must not be gone by the time they read it.
	if s.dropped > 0 {
		for _, cl := range jdeCaveatLines(fmt.Sprintf(
			"%d captured %s no longer fit the quantities on the form and are not being "+
				"sent. ctrl+e walks the units again.", s.dropped, plural("serial", s.dropped)),
			width) {
			l.AddRow(row, cl)
		}
	}
	fields := []jdeField{
		{Label: "Tracking", Kind: jdeValue, Value: receiveOrDash(req.TrackingNumber), Dim: req.TrackingNumber == ""},
		{Label: "Carrier", Kind: jdeValue, Value: receiveOrDash(req.Carrier), Dim: req.Carrier == ""},
		{Label: "Delivered", Kind: jdeValue, Value: receiveOrDash(req.DeliveryDate), Dim: req.DeliveryDate == ""},
		{Label: "Notes", Kind: jdeValue, Value: receiveOrDash(req.ReceiptNotes), Dim: req.ReceiptNotes == ""},
	}
	for i := range fields {
		if width > 0 {
			if roomFor := jdeStripWidth(width, lw); roomFor > 0 {
				fields[i].Value = fitCell(fields[i].Value, roomFor)
			}
		}
		l.AddRow(row, renderJDEField(fields[i], lw, width))
	}
	return l
}

// receiveOrDash is a value row's text when the box was left empty. The words
// say what the server will do rather than leaving a blank, which reads as a
// value that failed to render.
func receiveOrDash(v string) string {
	if v == "" {
		return "(not recorded)"
	}
	return v
}

// reviewSerialLines is what this line's serials come to, on the review.
func (s *ReceiveFormScreen) reviewSerialLines(lineIdx, width int) []string {
	units, got := 0, 0
	for i, u := range s.serialUnits {
		if u.lineIdx != lineIdx {
			continue
		}
		units++
		if strings.TrimSpace(s.captures[i].serial) != "" {
			got++
		}
	}
	if units == 0 {
		return nil
	}
	if got == units {
		return receiveCaveatLines(fmt.Sprintf("%d %s captured, one for every unit.",
			got, plural("serial", got)), StyleStatusOK, width)
	}
	return receiveCaveatLines(fmt.Sprintf(
		"%d of %d serials captured — the other %d arrive without one and are reported as "+
			"outstanding.", got, units, units-got), StyleStatusWarn, width)
}

// ---------------------------------------------------------------------------
// Writing a balance off
// ---------------------------------------------------------------------------

// writeOffBody is the destructive confirm.
//
// The FIELD leads and the prose follows, which is serialBody's rule and is here
// for serialBody's reason. jdeLines.Window keeps a block's START, no key on this
// phase moves a cursor, and everything below belongs to row 0 — so there is
// exactly one block, nothing can sit above the window, and whatever leads it is
// the whole of what a short pane keeps. A scanner or an operator typing a
// reason into a box they cannot see is the worse loss, so the box is what
// survives and the explanation is what gives.
//
// This comment used to say the KEYS were named at the top. They are not: this
// body names no key at all. The way out is on the ACTION BAR, which is outside
// this block entirely and never gives ground, so the ordering here was never
// about protecting it — the reasoning was borrowed from a block that does carry
// its own keys, and a WHY that does not describe the code is a defect in this
// repo whether or not the code is right.
func (s *ReceiveFormScreen) writeOffBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	l.DeclareLead("write-off reason")
	l.AddFittedFields([]jdeField{{
		Label:   "Reason",
		Kind:    jdeText,
		Input:   &s.reason,
		Width:   34,
		Hint:    "optional · recorded on the line",
		Focused: !s.pending,
	}}, lw, width, 0)
	l.AddRow(0, "")

	// The headline says WHICH write, and it is computed before the branch so
	// that the loss below it can be added ONCE. Fitted as ONE line, not by
	// pre-clipping the name inside it: a bound expressed against a guess at
	// what the rest of the sentence costs is not a bound, and the order form of
	// it ran a cell over the pane.
	var headline string
	if s.scope == receiveScopeOrder {
		n := s.outstandingLines()
		headline = fmt.Sprintf("Close %s out: %d outstanding %s written off",
			s.orderName(), n, plural("line", n))
	} else {
		// Indexed only on the branch that has a line, exactly as before: the
		// order scope leaves scopeLine wherever the cursor last was, so
		// reaching for it unconditionally would be a new panic path bought for
		// nothing.
		headline = fmt.Sprintf("Close line %d short: %s",
			s.scopeLine+1, s.lines[s.scopeLine].sheet.Label)
	}
	l.AddRow(0, jdeIndent+StyleStatusWarn.Render(receiveFit(headline, width, len(jdeIndent))))

	// WHAT THIS WRITE DESTROYS THAT NO KEY CAN BRING BACK, on the frame the
	// decision is made on. It sits directly under the headline and above the
	// prose, because prose folds and gives ground and this is the fact Ctrl+X
	// turns on — and it is written at ONE site rather than inside each branch,
	// so a third scope added later cannot be the branch that forgets it. The
	// gate that used to REFUSE on this is writeOffCaptureLoss's own comment.
	if loss := s.writeOffCaptureLoss(); loss != "" {
		l.AddRow(0, jdeIndent+StyleStatusWarn.Render(receiveFit(loss, width, len(jdeIndent))))
		for _, cl := range receiveCaveatLines("A write-off sends no receipt, so nothing "+
			"typed into the capture boxes goes with it.", StyleMuted, width) {
			l.AddRow(0, cl)
		}
	}

	if s.scope == receiveScopeOrder {
		for _, line := range jdeCaveatLines(
			"Every line still being waited on is closed SHORT — what arrived stays as it "+
				"is and the rest is recorded as never arriving. This is not the same as "+
				"saying it all turned up: nothing is stocked by it. What the order is left "+
				"as is the server's answer, and the summary reports it.", width) {
			l.AddRow(0, line)
		}
		return l
	}

	line := s.lines[s.scopeLine].sheet
	for _, tok := range jdeWrapTokens(receiveLineTokens(line), receiveMetaIndent, width) {
		l.AddRow(0, tok)
	}
	for _, cl := range jdeCaveatLines(fmt.Sprintf(
		"The %d %s still outstanding are recorded as never arriving. What did arrive stays "+
			"on the line and the shortfall stays on the record — this settles the line, it "+
			"does not pretend the goods came.",
		line.QuantityPending, plural("unit", line.QuantityPending)), width) {
		l.AddRow(0, cl)
	}
	return l
}

// ---------------------------------------------------------------------------
// The summary
// ---------------------------------------------------------------------------

// receiveDoneLines is the LINE count — po_detail.go's meaning of the word, so
// the same row says the same thing on both screens.
//
// TotalItems is `omitempty`, so a reply that carries no line total at all
// arrives as 0, and 0 is also a legitimate count. The two are not the same
// fact, so an absent total is not drawn as "0 lines": the row falls back to the
// outstanding count alone, which the receiving roll-up always sends. Naming a
// figure the server did not give would be this screen inventing a second
// opinion about the order.
func receiveDoneLines(po *omsapi.PurchaseOrder) string {
	left := fmt.Sprintf("%d outstanding", po.OutstandingLineCount)
	if po.OutstandingLineCount == 0 {
		// "none" rather than "0", because this row is read at a glance beside
		// a line total and two zeroes side by side invite the subtraction the
		// old single row led people into.
		left = "none outstanding"
	}
	if po.TotalItems > 0 {
		return fmt.Sprintf("%d · %s", po.TotalItems, left)
	}
	return left
}

// receiveDoneQuantity is the UNIT count, blank when the reply carries no order
// total to report.
//
// po_detail.go appends "· received N" only when something has been received;
// this screen always appends it, because it is the screen the operator reaches
// by receiving, and "received 0" coming back from a write is the fact they are
// there to check — a close-short or a mark-received against an order nothing
// ever arrived on answers exactly that, and omitting it would read as the row
// having nothing to say.
func receiveDoneQuantity(po *omsapi.PurchaseOrder) string {
	if po.TotalQuantity <= 0 {
		return ""
	}
	return fmt.Sprintf("%d · received %d", po.TotalQuantity, po.TotalReceivedQuantity)
}

// doneBody reports what the visit did, in the SERVER's words, as columnar value
// rows.
//
// Every figure here comes off the reply rather than being counted on this side.
// That is the point of the phase: an operator who has just moved stock needs to
// know what the system now believes, and a summary assembled from what this
// screen sent would agree with itself whatever the server did with it.
//
// Rows that would report nothing are left out rather than drawn as zeroes:
// "Variance ..... 0" is a row that makes an operator look for a discrepancy
// there was none of. Every line belongs to row 0; what a pane too short for the
// summary loses is the TAIL, which is the order these rows are written in —
// what happened first, then what is left over.
//
// THE RECEIPT ROW LEADS, and nothing sits above it. Every line belongs to row 0,
// so the window is pinned and draws from line 0 on every pane; a short one keeps
// the first line and nothing else. This body used to open on a "Receiving
// complete" heading and a blank, so from 80x11 to 80x18 the pane drew the
// heading — from 80x14 over "↓ 5 more below", on a bar that names no key able
// to fetch it — and the row below the fold was the receipt, the one record of
// what the write actually did. The heading bought nothing the receipt does not say better: it
// read "Receiving complete" over a close-short and a mark-received too, where the
// receipt names the write that landed. It is declared (DeclareLead) so the
// pinned-body sweep holds it there rather than the next edit's memory.
func (s *ReceiveFormScreen) doneBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	fields := []jdeField{{Label: "Receipt", Kind: jdeValue, Value: s.receipt, Dim: s.receipt == ""}}
	if fields[0].Value == "" {
		fields[0].Value = "(nothing recorded)"
	}
	if po := s.result; po != nil {
		state := po.StatusLabel
		if state == "" {
			state = po.Status
		}
		fields = append(fields, jdeField{Label: "Order", Kind: jdeValue,
			Value: fmtOrderName(po, s.poID()) + " · " + state})
		// LINES and QUANTITY are two facts and two rows, in the words
		// po_detail.go already uses for the same order: Lines off TotalItems,
		// Quantity off TotalQuantity / TotalReceivedQuantity. They were ONE row
		// labelled "Lines" carrying a line count and two unit counts —
		// "Lines ..... 0 outstanding · 0 received of 9" on a THREE-line order —
		// and an operator reading a row of numbers under one noun reads them as
		// that noun: they came away believing the order had nine lines, on the
		// frame they read after stock has moved. Every figure was the server's
		// and correct; only the label over them was not. Two screens using one
		// word for two things is the same defect one file apart, which is why
		// the vocabulary is taken from the sibling rather than invented here.
		fields = append(fields, jdeField{Label: "Lines", Kind: jdeValue,
			Value: receiveDoneLines(po)})
		if q := receiveDoneQuantity(po); q != "" {
			fields = append(fields, jdeField{Label: "Quantity", Kind: jdeValue, Value: q})
		}
		if po.HasReceiptVariance {
			fields = append(fields, jdeField{Label: "Variance", Kind: jdeValue,
				Value: fmt.Sprintf("! %d %s short or over", po.VarianceLineCount,
					plural("line", po.VarianceLineCount))})
		}
		if po.SerialsOutstanding > 0 {
			// A VALUE row cannot fold, so what it says has to fit the 51-column
			// pane whole: "! 2 units in stock with no serial recorded" is 44
			// cells before the leader and came back as "…with no seria…", which
			// on the one figure that says stock moved without its serials is a
			// cut number's worth of harm. The sentence explaining it is the
			// caveat below, which folds.
			fields = append(fields, jdeField{Label: "Serials", Kind: jdeValue,
				Value: fmt.Sprintf("! %d %s with no serial",
					po.SerialsOutstanding, plural("unit", po.SerialsOutstanding))})
		}
	}
	for i := range fields {
		// The values here are screen-composed sentences, but several carry the
		// order's own text off the wire, so every row is fitted to the pane
		// rather than trusted to be short.
		if width > 0 {
			if room := jdeStripWidth(width, lw); room > 0 {
				fields[i].Value = fitCell(fields[i].Value, room)
			}
		}
		if i == 0 {
			l.DeclareLead("summary")
		}
		l.AddRow(0, renderJDEField(fields[i], lw, width))
	}
	if po := s.result; po != nil && po.SerialsOutstanding > 0 {
		for _, line := range jdeCaveatLines(
			"Those units are on the shelf and countable, but no serial names any of them. "+
				"r re-reads the worksheet so they can be worked off.", width) {
			l.AddRow(0, line)
		}
	}
	return l
}

// receiveNoteRows is the CEILING of the block the note occupies — drawn blank
// when there is no note, and the fixed point that makes every claim on this
// screen honest. noteRows() is the block's height on a given pane: this
// constant when the pane can afford it, less when it cannot, and never a
// function of whether a note is standing.
//
// It is UNCONDITIONAL IN THE NOTE, and that is the whole design rather than an
// oversight to tidy away. (It yields to the PANE, which is a different axis
// entirely — noteRows() carries that reasoning and why it does not reopen any
// of what follows.) The note is pinned above the body, the header is subtracted from
// the body's row budget, and the body's height is what decides whether
// PgUp/PgDn are named at all — so a note that costs rows only WHEN IT IS THERE
// makes the answer depend on the sentence, and the sentence is itself a list of
// the keys that act. That circle had two visible ends. Writing a decline could
// ADD PgUp/PgDn to the bar drawn under it, so the pane carried "pgdown does
// nothing here" immediately above a bar naming PgUp/PgDn, and the next press
// paged — the key alternating between working and refusing. A decline that
// folded to FEWER rows than the note it replaced did the mirror of it, leaving
// "pgup/pgdn page" spelled in a sentence sitting under a bar that had dropped
// the pair.
//
// Reserving CONDITIONALLY is what creates the circle, so the fixed point has to
// be unconditional. Do not "optimise" this back into rows that appear with the
// note: that is the same defect wearing a different hat.
//
// Two cheaper-looking answers were considered and refused. Unpinning the note
// into the scrollable body would buy the rows back by giving up "a key that
// answers off the pane has not answered", which is why the note is pinned in
// the first place — trading a live defect for a dormant one is not a saving.
// Writing the contradiction down as a known limitation was refused outright: a
// decline naming a key that then declines is the precise defect this screen was
// converted to remove, and recording it would be a claim the code does not
// honour. The row cost is accepted deliberately — on a screen where the
// operator is scanning goods in, a command line that tells the truth about
// which keys work is worth more than the rows of body it spends.
//
// The size is the MINIMUM that restores the fixed point, not the worst case.
// The FAILURE DETAIL is deliberately NOT reserved: it is written by a reply off
// the wire and names no keys, so a frame that grows one is a frame that
// genuinely changed, and the bar and the guard see that change identically. It
// is not self-referential and must not be paid for.
//
// FIVE text rows is what the longest sentence this screen can produce folds to
// at the narrowest pane it supports (51 cells, so 49 after the indent). This
// constant has now gone up twice, for ONE reason both times, and the reason is
// the argument it demands of anybody raising it again:
//
//   - three -> four: the receiving flow gained Ctrl+K and Ctrl+R.
//   - four -> five: it gained Ctrl+O, the reopen-short correction.
//
// Shortening the SENTENCE is the standing instruction and it does not apply to
// either, because the part that grew is not a sentence anybody wrote. waysOut
// is DERIVED from the bar so that a decline cannot advertise a key the frame
// does not honour, nor omit one it does; trimming it back to some keys would
// put the curation this file's history is made of straight back into the one
// place the rule is checked. The bar grew because the screen gained a key that
// ACTS, which is the honest reason for a longer answer.
//
// THE MARGIN AT FOUR WAS ZERO, measured rather than estimated, which is why no
// shorter wording of the new bar label could pay for it. The scan-onto-a-settled-
// line refusal folded to exactly four rows and 151 cells; every candidate label
// — "Reopen short", "Reopen line", a bare "Reopen" — put it on a fifth. The
// word that could not go is "closed short" in that lead, which the correction
// made MORE load-bearing rather than less: it is the word telling the operator
// that Ctrl+O is the key for this line rather than a void.
//
// The refusals run to about 180 cells now: a lead naming the key and the reason,
// then waysOut, which is the whole bar spelled out.
//
// The row is paid for on every frame and that is the trade, made once and
// deliberately: an operator scanning goods in needs a command line that tells
// the truth about which keys work more than they need one more row of body.
//
// TestReceive_EveryNoteFitsItsReservation drives the states that produce these
// sentences and fails if one is cut, because the tail is where the key that
// gets the operator out is named; receiveNoteDropMark makes a cut VISIBLE even
// where that probe list misses it. If a new sentence does not fit, shorten the
// SENTENCE — this constant does not go up again without the same argument.
const receiveNoteRows = 5

// receiveNoteDropMark is what the note leaves behind when it does not fit.
//
// Every other bound on these screens that CUTS a value marks the cut —
// poRowDropMark on a picker row, jdeLines.Window's hidden-row count, fitCell's
// ellipsis (which a blurred box, the status row and every grid cell go
// through), pickerClip's, failDetailLines' mark on an error body, poQuotedClip's
// ellipsis inside the quote, and foldKeepRows' on a note or caveat that a pinned
// header re-draws at the rows it has rather than cutting (jdeHeader.addFitted).
// TestFailDetail_AShortPaneRedrawsTheBlockRatherThanCuttingItsMark,
// TestHeaderFold_AFoldedValueIsWholeAbsentOrMarked and
// TestReceive_AQuotedValueTheOperatorTypedIsWholeOrMarked hold the last three
// through the rendered panes.
//
// Two things give ground unmarked and neither leaves a fragment: the header
// drops WHOLE independent rows, which claims nothing about them, and a FOCUSED
// box scrolls its value past the caret the way any text field does.
// renderJDEField's label clip is unmarked as well and no swept state reaches it,
// because the label column is the widest label.
//
// The cuts that mark nothing are Root's, not any screen's: clampToBox, the
// backstop for a row nothing bounded, and the status bar, which clips a flashed
// message at the terminal edge. Where either bites, the ROW is the defect, and
// on these screens they bite in places filed separately: po_edit.go draws rows
// wider than the pane from 80 columns up (its line editor's three prose
// sentences, the order sheet's date hints, attribution heading and work-order
// value, the association picker's prose, the void prompt's `required`, the
// delete confirm's voided-line row); below 80 every other purchasing screen but
// this one draws field and grid rows the label column makes wider than the
// pane; and a failed submit's OMS body is flashed whole, cut at the edge. On
// THIS screen TestReceive_NothingOverflowsThePane holds that clampToBox does not
// bite, at every width but 45–48, where the layer's floored bodyWidth cuts every
// columnar screen (receiveHonestWidths).
//
// This one used to be the exception too: it stopped at receiveNoteRows and drew
// nothing to say so. What it drops is the TAIL, which on these sentences is
// where the key that gets the operator out is named, and noteLines' own comment
// says a clipped hint is worse than none because they believe they read it. A
// silent cut made that sentence false of the code two lines under it.
const receiveNoteDropMark = " …"

// receiveBodyFloor is the body's target floor once the terminal has told us how
// tall it is: three rows, which is the shortest body this form has anything
// useful to say in — a line's name, its readings and its quantity box.
//
// It used to be justified as "the same three rows jde_form.go's bodyRowsForBar
// floors at, deliberately, so this agrees with the layer rather than fighting
// it". The layer floors at nothing any more, and it never should have: the
// floor did not create rows, it only made the assembled frame claim rows the
// pane did not have, and clampToBox then took the action bar off the bottom of
// it. So this number now stands on its own reasoning, which is about what a
// RECEIVING body needs rather than about what the layer will tolerate.
//
// It is a target and not a guarantee, and the difference is worth stating
// because the sentence used to claim the guarantee. headerSplit delivers it in
// full from a body budget of receiveBodyFloor+2 upward with no failure standing,
// and from receiveBodyFloor+3 with one — those are the first budgets that can
// pay for the floor AND every header floor beside it. Below that the body gives
// one row at a time, never to nothing, and the exact ladder is in headerSplit.
// Below THAT the layer refuses the frame outright (jdeScreen.tooShort), so the
// ladder's bottom rung is never the last thing between the operator and a blank
// pane.
const receiveBodyFloor = 3

// headerRoom is what the WHOLE pinned header may spend on this pane.
//
// One clamp over the whole block, because the header is what starves the body
// and it has three parts: the note, the failure detail and the blank between
// the block and the body. Clamping the note alone — which is what this was when
// it was first written — protected the resting frame and left the FAILURE frame
// exactly where it had been: at 80x16 a 502 put three rows of gateway HTML into
// the same header, bodyAvailForBar answered 0, jdeLines.Window returned nothing
// and the pane was blank rows over a status row and a bar, with no line names
// and no quantity box, while Enter and UP/DN went on being named and acting on
// rows that were not drawn.
//
// CLAMPING IS NOT RESERVING, and the difference is the whole reason this is
// allowed to measure the failure detail. RESERVING is "always subtract these
// rows whether or not the content is there" — that was decided for the NOTE,
// unconditionally, and is not reopenable; the failure detail is deliberately
// not reserved because it is reply-driven and names no keys, so the bar and the
// guard see it appear and vanish identically. CLAMPING is "do not let the
// header, whatever it happens to be carrying this frame, starve the body to
// nothing", and that is a pure function of the pane and of what is actually
// standing — never of whether a NOTE is held, which is the self-reference the
// reservation exists to break.
//
// It is only ever asked about a SIZED pane. headerSplit is the one caller and
// it answers the unsized case itself, before it gets here, off the same
// headerBudget expression — so this used to carry a branch for a budget of
// zero that could not execute, with a paragraph beside it explaining a
// behaviour nothing performed and a value (7) that disagreed with what
// headerSplit actually allocates when unsized. That branch is deleted rather
// than reworded: dead code with a justification next to it reads as considered.
func (s *ReceiveFormScreen) headerRoom() int {
	budget := s.headerBudget()
	floor := s.headerFloor()
	if room := budget - receiveBodyFloor; room > floor {
		return room
	}
	// The body keeps at least one row whatever else goes: a form that draws
	// nothing is the worst outcome on this screen, and rounds 7 and 8 were both
	// spent closing exactly that.
	if floor > budget-1 {
		floor = budget - 1
	}
	if floor < receiveHeaderFloor {
		floor = receiveHeaderFloor
	}
	return floor
}

// receiveHeaderFloor is what the header cannot give up with nothing but a note
// in it: one row for the note — so a decline always has somewhere to be drawn —
// and the blank that keeps it off the body.
const receiveHeaderFloor = 2

// headerFloor is receiveHeaderFloor plus the failure detail's own floor when a
// detail is standing. It is a floor rather than a RESERVATION: it appears only
// when there is a detail to draw, which is reply-driven and which the bar and
// the guard see identically — the note's rows are the ones reserved
// unconditionally, and that distinction is the one receiveNoteRows exists to
// keep.
func (s *ReceiveFormScreen) headerFloor() int {
	if s.failDetailText() == "" {
		return receiveHeaderFloor
	}
	return receiveHeaderFloor + 1
}

// headerBudget is the rows the WHOLE frame has for the header and the body
// together, measured against the phase's tallest bar. One expression for one
// condition: headerSplit asks it whether the pane has been sized at all and
// headerRoom asks it how much there is to divide, so a change of bar or of
// geometry cannot move one answer without moving the other.
func (s *ReceiveFormScreen) headerBudget() int {
	return s.bodyAvailForBar(0, s.barCeiling())
}

// receiveHeader is the header's row allocation on this pane, written ONCE and
// read by everything that asks. Two blocks with floors in one header cannot be
// budgeted separately: the previous shape computed the note's share and handed
// the detail whatever was left, which is how "the detail gives FIRST" became
// "the detail gives EVERYTHING".
type receiveHeader struct{ note, detail int }

// headerSplit divides headerRoom between the note and the failure detail.
//
// EVERY PARTICIPANT HAS A FLOOR, and the giving is by degree rather than by
// elimination. That is the correction: the order was right and its arithmetic
// was not. failDetailRows used to be `headerRoom - 1 - noteRows`, which gave
// the note its whole ceiling first and left the detail the remainder — so at
// every body budget of 7 or less (which at 80 columns with a quantity typed is
// every terminal 17 rows or shorter) the remainder was zero and a 502 left the
// operator reading "✗ Receiving PO-1001 failed" with no reason at all, on the
// one screen where the reason is what decides whether they retype a quantity or
// go and fetch somebody. Both this file's comments claimed a short terminal
// loses "the TAIL of the gateway's HTML"; it was losing the whole of it.
//
// So the floors are paid FIRST, in this order, and only then is the surplus
// spent:
//
//	the status headline    never gives at all — it is on the status row, which
//	                       is outside this budget entirely
//	the note               one row, always: it is the answer to a keypress, and
//	                       a decline with nowhere to be drawn is a press the
//	                       operator gets no answer to
//	the failure detail     one row whenever a detail exists, so it loses its
//	                       TAIL and never itself
//	the separator          one row, so the block is not read as body
//	the body               everything left, and never nothing
//
// Then the surplus fills the NOTE to receiveNoteRows before the DETAIL to
// receiveFailDetailRows, which is the same "detail gives first" ordering read
// from the other end: what is served last is what is given up first.
//
// ONE height cannot pay all four floors — a body budget of 3, which is
// jde_form.go's own bodyRowsForBar floor and so the smallest there is. There
// the four rows needed (note, detail, separator, body) do not exist, and what
// gives is the DETAIL: the note's row is a keypress's only answer, the body's
// row is the form itself, and the operator still has the headline on the status
// row telling them the receipt failed. Every budget from 4 up pays all four.
func (s *ReceiveFormScreen) headerSplit() receiveHeader {
	want := 0
	if s.failDetailText() != "" {
		want = receiveFailDetailRows
	}
	if s.headerBudget() <= 0 {
		// Unsized: the frame draws whole and Root's clampToBox decides, so
		// there is no geometry to divide. This is the ONE place that question
		// is answered — headerRoom below is reached only past this line, and
		// it reads the same headerBudget, so the two cannot come to different
		// views of whether the pane has told us anything.
		return receiveHeader{note: receiveNoteRows, detail: want}
	}

	content := s.headerRoom() - 1 // the separator is not divisible
	note, detail := 1, 0
	if want > 0 && content >= note+1 {
		detail = 1
	}
	spare := content - note - detail
	if grow := receiveNoteRows - note; grow > 0 && spare > 0 {
		if grow > spare {
			grow = spare
		}
		note += grow
		spare -= grow
	}
	if grow := want - detail; grow > 0 && spare > 0 {
		if grow > spare {
			grow = spare
		}
		detail += grow
	}
	return receiveHeader{note: note, detail: detail}
}

// failDetailRows is how many rows the failure detail gets on this pane —
// headerSplit's answer, so the two blocks in the header cannot be budgeted
// against different arithmetic.
func (s *ReceiveFormScreen) failDetailRows() int { return s.headerSplit().detail }

// noteRows is how many rows the note block gets ON THIS PANE.
//
// The reservation is still UNCONDITIONAL in the sense that was decided, and
// that sense is the one that matters: it is a pure function of the PANE, and it
// never asks whether a note is currently held. Writing or retiring a note
// therefore cannot move the pinned header by a row, so it cannot add or remove
// PgUp/PgDn from the bar drawn under it — the circle receiveNoteRows exists to
// break. Do NOT "restore" the constant by deleting this clamp: making the
// reservation conditional on the NOTE is the reopened circle; making it yield
// to the PANE is not, because height is an axis the bar and the guard already
// see identically and there is no self-reference in it to protect.
//
// It yields because the flat reservation had a second consequence nobody had
// measured. At 80x12 the pane keeps six rows, the bar and the status row take
// two, and the four pinned rows took the four that were left: bodyAvailForBar
// answered 0, jdeLines.Window returned nothing, and the frame was four BLANK
// rows over a status row and a bar — no line names, no quantity box, nothing.
// Not a clipped form: an empty one, built empty, with clampToBox nowhere near
// it. Every height up to 16 lost the form the same way.
//
// So when the pane cannot afford both, the RESERVATION gives and the BODY is
// kept, in that order: an operator cannot receive against a form that is not
// drawn, and the body is what they are there for. The note keeps its last row
// rather than vanishing, because a decline that draws nothing on the pane is
// the "keypress with no visible answer" defect this screen was converted to
// remove — what a short pane costs it is words, which fittedNote marks.
//
// The bar it measures against is the phase's TALLEST (barCeiling), which is the
// same fixed point qtyPagesFor uses and is what keeps this out of the recursion:
// a bar that varied with the header would make the header vary with the bar.
func (s *ReceiveFormScreen) noteRows() int { return s.headerSplit().note }

// headerLines is the screen's answer to the last keypress, PINNED above the
// scrollable body on every frame.
//
// Pinned rather than appended, because the answer is the one line that must not
// be able to scroll away: inside the body it sat below a cursor the operator
// had walked down a long line list, and a key that answers off the pane has not
// answered. The diagnostic DETAIL rides with it, bounded, so a failure and its
// reason are one block rather than two a scroll can separate.
//
// The height is CONSTANT in everything a keypress controls: noteRows() for the
// note whether or not one is standing, plus the blank that separates the block
// from the body. Only the reply-driven failure detail varies — and the PANE,
// which noteRows yields to and which the bar and the guard see identically.
// The NOTE'S FIRST LINE is the one essential row, and jdeMinBudget's own
// reasoning is why: the header floor exists on this screen because the note is
// the whole of "enter needs a quantity first", so a refusal with nowhere to be
// drawn is silent. The rest of the note folds, and the failure DETAIL is
// context — its headline is on the status row, which is outside this budget and
// never gives, so a short pane costs the reason and never the fact.
//
// One essential row and no more, because the smallest drawable budget on a
// screen with a pinned header keeps exactly one (jdeMinBudget), and a row marked
// essential that the geometry drops anyway is the same false claim jdeHeadRank
// exists to remove.
func (s *ReceiveFormScreen) headerLines() jdeHeader {
	out := jdeHeader(nil)
	for i, line := range s.noteLines() {
		rank := jdeHeadContext
		if i == 0 {
			rank = jdeHeadEssential
		}
		out = out.add(rank, line)
	}
	return out.add(jdeHeadContext, s.failDetailLines()...).add(jdeHeadDecorative, "")
}

// noteLines renders the screen's answer to the last keypress into the rows
// noteRows reserves for it, folded to the pane the terminal really gave and
// padded out when it is shorter or absent.
//
// Folded rather than clipped because Root.View TRUNCATES, and the tail of these
// sentences is where the key that gets the operator OUT is named — a clipped
// hint is worse than none, because they believe they read it. Capped at the
// same constant the reservation spends, so the two cannot part company about
// how tall the block is (po_create.go's poCartCaveat is the idiom: one
// expression read by both the renderer and the budget).
func (s *ReceiveFormScreen) noteLines() []string {
	width := s.paneWidth() - len(jdeIndent)
	rows := s.noteRows()
	out := make([]string, 0, rows)
	for _, line := range s.fittedNote(width, rows).renderLines(width) {
		if len(out) == rows {
			// fittedNote has already guaranteed this cannot happen. The bound
			// stays because the block's HEIGHT is now load-bearing — the paging
			// claim is measured against it — so an overrun must cost a word
			// rather than the header's constancy.
			break
		}
		out = append(out, jdeIndent+line)
	}
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

// fittedNote is the note shortened to the rows reserved for it, carrying the
// mark that says so — the TEXT bounded rather than the rendered lines.
//
// Bounding the text and not the lines is the point. renderLines emits styled
// runs, so dropping a rendered line can take a closing SGR reset with it and
// colour everything drawn afterwards — the defect the status row's own bound
// exists for. Words go from the END because that is the only end there is a
// choice about: the lead names the key that was pressed and IS the answer, so
// what gives is the way-out tail, and the mark is what stops that being
// invisible.
//
// renderLines is the oracle rather than a re-derived fold, so the fit is
// measured by the very function that will draw it: a mark width or a
// continuation indent changing there cannot leave this measuring something
// else. The loop runs only when a sentence overruns, which no wording this
// screen produces does today — the margin is zero, not absent.
//
// The BUDGET is spent first, in one forward pass, before anything is folded —
// the shape failDetailLines uses seven functions down, and for its reason:
// receiveNoteRows lines of `width` cells is everything that can be DRAWN, so
// folding what lies past it is work whose result is thrown away, on a block
// headerLines rebuilds two or three times a frame. cellPrefix walks forward and
// stops when the budget is spent, so the cost is the budget rather than the
// length of what it was handed, and the word walk below then runs over a string
// that is already bounded. Today every note is a screen-composed sentence of
// about 120 cells, so this buys nothing measurable; it is here because the
// unbounded shape is what the standing rule forbids, and because an OMS-supplied
// string reaching say() is one arm away — omsapi.parseError puts the ENTIRE raw
// response body into APIError.Message when the envelope carries no code, which
// is the multi-KB page behind the hang this project has already fixed once.
//
// A cut by cellPrefix implies an overrun: text longer than `rows` × width cells
// cannot fold into `rows` lines of width cells.
//
// `rows` is the block's height on THIS pane (noteRows), not the constant, because
// a short terminal makes the block smaller and a bound measured against a budget
// the frame will not give is not a bound.
//
// The LEAD SURVIVES in every branch, and the last one is why this is spelled out
// rather than left to the word walk. The walk stops at `keep > 0`, so a text with
// no word boundary at all — a minified JSON or HTML body, which is exactly the
// shape omsapi.parseError hands over — used to fall through to a note whose whole
// content was the drop mark. A note reading "…" names no key, which on this screen
// means two declining keys redraw the same pane: the defect the decline lead exists
// to prevent, reintroduced by the bound that was supposed to protect it. So the
// last resort keeps as much of the lead as the block can carry, marked, and only a
// pane too narrow to draw three cells gets the bare mark.
func (s *ReceiveFormScreen) fittedNote(width, rows int) pickerNote {
	note := s.shownNote()
	if note.text == "" || rows <= 0 {
		return note
	}
	bounded := cellPrefix(note.text, rows*width)
	if bounded == note.text && len(note.renderLines(width)) <= rows {
		return note
	}
	words := strings.Fields(bounded)
	for keep := len(words) - 1; keep > 0; keep-- {
		trial := note
		trial.text = strings.Join(words[:keep], " ") + receiveNoteDropMark
		if len(trial.renderLines(width)) <= rows {
			return trial
		}
	}
	for room := rows * width; room > 0; room-- {
		trial := note
		trial.text = cellPrefix(bounded, room) + receiveNoteDropMark
		if len(trial.renderLines(width)) <= rows {
			return trial
		}
	}
	trial := note
	trial.text = strings.TrimSpace(receiveNoteDropMark)
	return trial
}

// shownNote is what the note block draws: the answer to the last keypress
// whenever one is standing, and otherwise the phase's standing fact.
//
// The answer wins, always. It is the reply to what the operator just pressed and
// it names the key, which is what keeps two declining keys from redrawing one
// pane; a standing fact that outranked it would be a keypress with no visible
// answer. The fact comes back on the very next press that writes none, because
// handleKey retires the note on every press.
func (s *ReceiveFormScreen) shownNote() pickerNote {
	if s.note.text != "" {
		return s.note
	}
	return s.standingNote()
}

// receiveNothingReceivable is the standing fact of an order with nothing a
// receipt may name, REASON first. The note block can be one row, and a block
// that gives ground gives it from the END (fittedNote), so what a short pane
// keeps is the leading clause: why nothing here can be received. The way out —
// void or cancel the ORDER — follows it, and is said again by the scan row's
// hint and by Ctrl+R's decline.
//
// It does not assert that nothing arrived. An order every line of which was
// closed short or struck off without a single delivery never reaches
// `received` — that status means goods arrived — so it stays receivable and comes
// back here with `can_receive: true` and nothing to receive; whether anything
// did arrive is the server's own condition on that transition, and the worksheet
// carries no receipt total to read it off. So the sentence names the action that
// exists rather than diagnosing the order.
const receiveNothingReceivable = "Every line is voided or closed short, so nothing here " +
	"can take a receipt. Void or cancel the ORDER to finish with it."

// receiveReopenExplains is the standing fact of BOTH correction frames: what a
// reopen does, with the half that stops it reading as an undo LEADING, because
// the note block gives ground from the END and headerLines marks only its first
// row essential.
//
// It is in the note block rather than in either body because that is the one
// surface on this screen that is on the pane at every height the frame is drawn
// at, from every row. The confirm is why that matters rather than being a
// tidiness: its body is ONE pinned block led by the Reason field, so everything
// after the field is tail — and measured at the canonical 80x24 the pane drew
// "↓ 7 more below" with this fact among the seven, on the frame whose next key
// writes to the record. The body still carries the DETAIL underneath, which is
// setErr's headline-then-detail split applied to a caveat.
//
// ONE sentence for both frames, because they are one decision seen twice: the
// pick is where it is chosen and the confirm is where it is committed, and two
// wordings of one fact are two things to keep in step.
const receiveReopenExplains = "The close-short stays on the record; a reopen is stamped " +
	"beside it. The line goes back to outstanding so it can be received against."

// standingNote is a FACT about the frame, drawn in the note block while no
// keypress has an answer standing there — New PO's idiom (po_create.go), so that
// "nothing to say" and "the answer scrolled away" are different states.
//
// The frames that have one are the two REFUSALS OF THE WHOLE FORM on a worksheet
// that did land: the server saying the order may not be received against, and
// an order with nothing a receipt may name. A refusal is only one an operator
// can act on if they can see why, and in both the why belongs to no row — it
// used to be the tail of a block (row 0's on the blocked frame, the last row's
// on the quantity form), which a short pane drops and no key fetches. The note
// block is the one surface on this screen that is on the pane at every height
// the frame is drawn at, from every row (headerSplit's first floor, and the
// header's one essential row), so the why goes there. It costs no row: the
// block's height is reserved whether or not it holds anything, which is the
// fixed point receiveNoteRows exists for, so a standing fact cannot move the
// paging claim either.
//
// The UNREADABLE worksheet is the refusal that is deliberately not here: its
// why is a failure, whose headline is on the status row and whose detail is
// already pinned in this header, both from the wire.
func (s *ReceiveFormScreen) standingNote() pickerNote {
	switch {
	case s.sheet == nil:
		return pickerNote{}
	case s.phase == phaseBlocked:
		return pickerNote{text: s.blockedReason(), level: StatusWarn}
	case s.phase == phaseQty && len(s.qty) == 0:
		return pickerNote{text: receiveNothingReceivable, level: StatusWarn}
	case s.phase == phaseReopenPick, s.phase == phaseReopenConfirm:
		return pickerNote{text: receiveReopenExplains, level: StatusInfo}
	}
	return pickerNote{}
}

// failDetailText is the unbounded half of whatever failure is standing, and it
// is the ONE predicate for "is there a detail to draw". headerSplit asks it to
// decide whether the detail's floor is owed, and failDetailLines asks it for
// the text: a second copy of that condition would let the budget reserve a row
// the renderer does not fill, or the renderer want a row the budget never gave.
// There is one source now where there used to be two. Serial capture ran a
// request per unit and kept its own error string beside failDetail; the serials
// go inside the receipt's own transaction since the receiving contract landed,
// so a failed capture IS a failed receipt and there is nothing left to hold a
// second reason.
func (s *ReceiveFormScreen) failDetailText() string { return s.failDetail }

// receiveFailDetailRows is the CEILING of the failure detail. The sentence
// naming what failed is on the status row above it and never gives, and what a
// short terminal loses here is the TAIL of the gateway's HTML — headerSplit
// keeps the detail's first row ahead of the note's second, so the detail is
// shortened rather than dropped at every pane that can pay four rows at all.
// The single budget that cannot is named there.
const receiveFailDetailRows = 3

// failDetailLines draws the unbounded half of a failure under the headline the
// status row carries. It is CUT to what the pane can hold before it is folded —
// folding a multi-KB gateway page is work whose result is thrown away, on a
// block redrawn every keystroke (cellPrefix walks forward and stops when the
// budget is spent; truncateVisible would be O(n²) here).
//
// The fold-cut-and-MARK is pane_text.go's failDetailLines, shared with the three
// other screens that fold a failure body, and this site was the FOURTH copy —
// found by a review after the other three had been converted and after that
// helper's comment had already claimed there were only three. Its own break at
// the row limit was silent, so all four receiving endpoints (each hand-writes
// {"error": ...} and misses DRF's exception handler, which is why
// omsapi.parseError puts the ENTIRE raw payload into APIError.Message) could cut
// a gateway page mid-sentence and leave it reading as finished — on the screen
// where the reason decides whether the operator retypes a quantity or goes and
// fetches somebody.
//
// The block does NOT grow: the mark spends the LAST of the rows headerSplit
// already gave the detail, exactly as it does on the other three sites, so
// nothing here can move the pinned header by a row and nothing can change what
// the bar under it names. At the one-row budget headerSplit pays on a short
// pane, the shared helper keeps the content and marks the cut with the ellipsis
// rather than spending that row on the mark; its comment carries why.
func (s *ReceiveFormScreen) failDetailLines() []string {
	detail := s.failDetailText()
	if detail == "" {
		return nil
	}
	width := s.paneWidth() - len(jdeIndent)
	if width < 12 {
		width = 12
	}
	var out []string
	for _, line := range failDetailLines(detail, width, s.failDetailRows()) {
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	return out
}
