// ReceiveFormScreen — book a delivery against a purchase order, then capture
// one serial number per received unit of a serialized line.
//
// This is the receiving half of the purchasing conversion (sc-jde-recv). It
// renders through jde_form.go's shared columnar layer, as po_edit.go (the
// pilot) and po_add_line.go (the scanner flow) already do: a fixed label column
// with a dotted leader, a body the operator scrolls, and a PERSISTENT action bar
// naming exactly the keys that act in the state being drawn. There is no local
// copy of the scroll arithmetic, the status-row bound, or the field layout — all
// three are the layer's, and jde_lift_sweep_test.go holds that door shut.
//
// Three phases, and they are the whole screen (receivePhase):
//
//	qty → (receipt posts) → serial → done
//	    ↘ ─────────────────────────↗   (nothing serialized: straight to done)
//
// # The key scheme is the columnar one
//
//	Enter        RECEIVE, from whichever row the cursor is on
//	Up/Down      move between the quantity rows and the notes row
//	Tab/Shift-Tab  the same, the alias ~20 columnar forms name as UP/DN=Fields
//	PgUp/PgDn    page, when the body is taller than the pane
//	Esc          back to the order (and the bar says when that DISCARDS entry)
//
// Enter used to advance a field and submit only from the last one. That is the
// binding this conversion changed and it is listed in the PR: every other
// columnar sheet in purchasing commits from any row (po_edit's Enter=Save,
// po_add_line's Enter=Add line), and a screen that reserves Enter for "next
// field" teaches the operator a rule that is false one screen over. Partial
// receipts are what this form is FOR — every line carries QuantityPending — so
// receiving what has been typed so far is a legitimate answer to Enter rather
// than a surprise.
//
// # Every state answers every key
//
// The rules the purchasing screens are held to (AGENTS.md) apply here in full,
// and three of them were not being kept before the conversion:
//
//   - WHILE THE RECEIPT IS OUT the form is FROZEN. It was live: Enter posted the
//     same receipt a second time, and typing changed quantities the request in
//     flight no longer reflected. The freeze is an ALLOW-LIST (Esc, and nothing
//     else), because written the other way round it would freeze the keys
//     somebody thought of and leave every arm added later free by default.
//     Esc is deliberately NOT gated: a frame with no way out while a slow
//     gateway thinks is the worse defect. Leaving does not cancel the request.
//
//   - A SUCCESSFUL RECEIPT ENDS THE FLOW. It used to leave the operator on the
//     quantity form with the boxes still holding what they had typed, so a
//     reflexive second Enter booked the whole delivery again. Nothing on the
//     screen said the first one had landed except a four-second flash.
//
//   - A FAILED SERIAL IS RETRIED, NOT SKIPPED. The capture used to advance past
//     the unit whatever happened, so the error was drawn under the NEXT unit's
//     prompt — describing a unit no longer on screen — and the serial the
//     operator had typed was gone with no way to enter it again.
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

// receivePhase tracks the three stages of the receive flow: entering per-line
// quantities, then (only when serialized lines were received) scanning one
// serial number per received unit, then a final summary.
type receivePhase int

const (
	phaseQty receivePhase = iota
	phaseSerial
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
	case phaseQty:
		return "qty"
	case phaseSerial:
		return "serial"
	case phaseDone:
		return "done"
	}
	return fmt.Sprintf("receivePhase(%d)", int(p))
}

// serialUnit is one serial-capture slot: a single received unit of a
// serialized line that still needs its serial number scanned in.
type serialUnit struct {
	itemID   string // InventoryItem UUID (from the PO line's item_details)
	poItemID any    // PurchaseOrderItem id, recorded as provenance
	label    string // line display label, for the prompt
	unitNo   int    // 1-based unit index within the line
	unitTot  int    // total units received on the line
}

// receiveLabels is this screen's label column, in ONE place so every phase
// hangs off the same leader — the columnar rule that a value never moves
// sideways when the frame changes under it (po_add_line's poAddLabels).
var receiveLabels = []string{
	"Quantity", "Notes",
	"Serial",
	"Receipt", "Serials", "Skipped", "Failed", "Uncaptured",
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
const receiveKitCaveat = "Receiving one credits the kit's COMPONENT items, not the kit — " +
	"the quantity here is a number of kits."

// receiveSerialCaveat is the same fact one line-kind over: a serialized line
// asks for a serial per unit AFTER the receipt posts, so an operator typing a
// quantity into it should know a second phase is coming. It travels with the
// line for the reason receiveKitCaveat does.
const receiveSerialCaveat = "This line is serialized: a serial is prompted for each unit " +
	"once the receipt posts."

type ReceiveFormScreen struct {
	deps Deps
	// jdeScreen carries the pane geometry (terminal size, body width, the row
	// budget) and the framing. Embedded rather than copied, so bodyWidth(),
	// statusRow() and frameWrapped() read here exactly as they do on every
	// other converted sheet.
	jdeScreen

	po      *omsapi.PurchaseOrder
	lines   []omsapi.PurchaseOrderItem
	qty     []textinput.Model
	notes   textinput.Model
	focused int
	pending bool

	// receipt is the sentence the summary reports: what the server said the
	// delivery did to the order. It is written only by a reply off the wire.
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
	// failedCount counts failed ATTEMPTS rather than lost units, because a
	// failure now keeps the operator on the unit to try again. A unit never
	// captured is reported by the uncaptured count instead, which is derived
	// from where the cursor stopped and so cannot disagree with it.
	failedCount int
	serialErr   string
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
		s.qty = append(s.qty, ti)
	}
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
	s.notes = textinput.New()
	s.notes.Prompt = ""
	s.notes.CharLimit = 200
	s.serialInput = textinput.New()
	s.serialInput.Prompt = ""
	s.serialInput.CharLimit = 200
	if len(s.qty) > 0 {
		s.qty[0].Focus()
	} else {
		s.notes.Focus()
	}
	return s
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
	if s.po == nil {
		return "Receive Items"
	}
	return "Receive " + s.orderName()
}

// WantsRawInput routes every key here. Two phases hold a focused textinput, and
// the flow owns its own Esc: leaving is a step of THIS screen (back to the
// order), not the root's back-stack pop.
func (s *ReceiveFormScreen) WantsRawInput() bool { return true }

func (s *ReceiveFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ReceiveFormScreen) totalInputs() int { return len(s.qty) + 1 }

func (s *ReceiveFormScreen) currentInput() *textinput.Model {
	if s.focused < len(s.qty) {
		return &s.qty[s.focused]
	}
	return &s.notes
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

// orderName is the order as the frames name it, never blank.
func (s *ReceiveFormScreen) orderName() string {
	if s.po == nil {
		return "this order"
	}
	if s.po.Number != "" {
		return s.po.Number
	}
	return fmt.Sprintf("PO #%v", s.po.ID)
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

// declineFrozen is decline for a key an in-flight request has made inert. It
// says WHY rather than "does nothing", because the key does work — one second
// from now — and an operator watching a slow gateway is exactly the operator
// who will press it again.
//
// It names WHAT is out, because the two frozen states on this screen are
// waiting on different requests and the sentence used to say "the receipt" in
// both. On serial capture the receipt has already ANSWERED — that is what
// opened the phase — and what is out is one unit's CreateSerializedComponent,
// so the pane carried "j is frozen until the receipt answers" directly under a
// status row reading "Recording the serial for unit 1 of 2…": two lines of one
// frame disagreeing about what the screen is waiting for, with the operator
// told to wait on the one that had already come back.
func (s *ReceiveFormScreen) declineFrozen(key string, headerRows int) tea.Cmd {
	return s.say(key+" is frozen until "+s.inFlightSubject()+" answers · "+
		s.waysOut(headerRows), StatusWarn)
}

// inFlightSubject is the request the screen is waiting on, in the words a
// decline can be built out of.
//
// Two arms and no default, because declineFrozen is the only caller and both of
// its call sites are past a guard on one of these flags — a third answer would
// be a branch for a state the screen is never frozen in.
func (s *ReceiveFormScreen) inFlightSubject() string {
	if s.serialPending {
		return "the serial"
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

// receiveReason is the half of an error worth showing an operator: OMS's own
// sentence when the envelope carried one, and otherwise whatever came back
// whole. Never trusted to be short — omsapi.parseError puts the ENTIRE raw
// response body into APIError.Message whenever the envelope has no code, so a
// gateway's HTML page arrives here intact and is bounded where it is drawn.
func receiveReason(err error) string {
	var api *omsapi.APIError
	if errors.As(err, &api) && api.Code != "" && api.Message != "" {
		return api.Message
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *ReceiveFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	// A reply off the wire is what ENDS a freeze, so it is also what retires
	// the note the freeze wrote. The note answers the last keypress IN THE
	// FRAME THAT KEY WAS PRESSED AGAINST, and headerLines pins it on every phase, so
	// a decline that outlives its own state is a frame asserting something the
	// screen has stopped being true of: press any key while the receipt is out
	// and the note reads "j is frozen until the receipt answers · esc back to
	// order"; when the reply lands on a serialized order the phase becomes
	// serial, whose bar is "Enter=Skip unit · Esc=Finish", and that pinned
	// sentence still claims a freeze that has ended and still says esc goes
	// back to the ORDER — contradicting the bar about the one key it names. On
	// the failure branch it is worse: the stale warn line is drawn immediately
	// above the fresh 502 detail with the bar fully unfrozen again.
	//
	// Cleared HERE, once, rather than in the arms that clear pending and move
	// the phase — the same shape as the New PO screen's pendingLead, which is
	// cleared in Update's key dispatch and not in the three arms that navigate
	// (AGENTS.md). A clear per arm is a clear somebody adding the next branch
	// has to remember, and this defect fails silently. finishSerial and submit
	// each carried a third clear until it was noticed they had been dead since
	// handleKey's was written — and finishSerial's was an arm that moves the
	// phase, which is the very thing this paragraph says the note is not
	// cleared in.
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
	case receiveSubmittedMsg, serialUnitDoneMsg, tea.WindowSizeMsg:
		s.note.clear()
	}

	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case receiveSubmittedMsg:
		return s, s.handleReceived(m)

	case serialUnitDoneMsg:
		return s, s.handleSerialUnit(m)

	case tea.KeyMsg:
		return s.handleKey(m)
	}

	// Anything that is not a key belongs to whichever box has the caret — the
	// cursor blink, chiefly. A frozen phase has no focused box, so nothing here
	// can move under a request in flight.
	var cmd tea.Cmd
	if s.phase == phaseSerial {
		s.serialInput, cmd = s.serialInput.Update(msg)
		return s, cmd
	}
	if s.phase == phaseQty && !s.pending {
		if s.focused < len(s.qty) {
			s.qty[s.focused], cmd = s.qty[s.focused].Update(msg)
		} else {
			s.notes, cmd = s.notes.Update(msg)
		}
	}
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
	// Together with the reply switch at the top of Update these are the ONLY
	// two places the note is retired. finishSerial and submit used to clear it
	// as well; both are reachable only through this dispatch, so those calls
	// were dead the moment this one was written — and one of them was an arm
	// that clears pending and moves the phase, which is precisely what the
	// comment above says the note is NOT cleared in.
	//
	// The header is measured BEFORE the arms run, and every arm carries it, so
	// that a press is judged against ONE frame: the frame whose bar the operator
	// was reading when they pressed.
	//
	// The NOTE is not what makes that necessary, and this paragraph used to say
	// it was. Since receiveNoteRows the note block is a fixed allocation —
	// noteLines returns exactly receiveNoteRows entries in every state — so
	// headerLines() is receiveNoteRows + len(failDetailLines()) + 1 and
	// retiring the note on the next line cannot move it by a row. Reserving it
	// unconditionally is what bought that, and the reason is recorded there:
	// a header that grew with the sentence let a decline add PgUp/PgDn to the
	// bar drawn under it, so the bar named a key the next press refused.
	//
	// What headerRows still varies with is the reply-driven FAILURE DETAIL,
	// which an arm CAN retire mid-dispatch (submit's clearFail). It is
	// legitimately variable rather than self-referential: it comes off the wire,
	// it names no keys, and the bar and the guard see it identically — so a
	// frame that grows or loses one is a frame that genuinely changed, and
	// pinning the question to the frame the press was made against is what stops
	// an arm answering about the frame it is on its way to producing.
	//
	// So the frame is the argument (po_create.go's barFor takes headerRows for
	// the same reason), and the bar's claim and the guard behind it read one
	// expression — qtyPagesFor — bound to the same frame.
	headerRows := len(s.headerLines())
	s.note.clear()
	switch s.phase {
	case phaseSerial:
		return s.keySerial(m, headerRows)
	case phaseDone:
		return s.keyDone(m, headerRows)
	}
	return s.keyQty(m, headerRows)
}

// leave returns to the purchase order this screen was opened from. The order
// is always in hand — po_detail.go opens this screen from one it has already
// loaded, behind an `s.po != nil` guard — so there is no second way out to
// keep working here.
func (s *ReceiveFormScreen) leave() tea.Cmd {
	return SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, fmt.Sprint(s.po.ID)))
}

// keyQty is the quantity form's keyboard.
//
// The FREEZE is written as an allow-list: while the receipt is out, esc is the
// one key that acts and everything else — including a key an arm added
// tomorrow would bind — declines by name. The other way round froze the keys
// somebody thought of, which is exactly how a key got past the New PO screen's
// freeze one round after it was written.
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
		if s.entryState() != receiveAttemptable {
			// The bar does not name Enter here, so the press has to say why
			// rather than redraw an identical pane — and it says the useful
			// thing rather than the generic one, because what the operator has
			// to do next differs with the reason (entryRefusal).
			return s, s.say(
				"enter has nothing to receive — "+s.entryRefusal()+" · "+s.waysOut(headerRows), StatusWarn)
		}
		return s.submit()
	case "up", "shift+tab":
		if s.totalInputs() < 2 {
			return s, s.decline(k, headerRows)
		}
		s.focusNext(true)
		return s, nil
	case "down", "tab":
		if s.totalInputs() < 2 {
			return s, s.decline(k, headerRows)
		}
		s.focusNext(false)
		return s, nil
	case "pgup", "pgdown":
		return s, s.pageQty(k, headerRows)
	}

	var cmd tea.Cmd
	if s.focused < len(s.qty) {
		s.qty[s.focused], cmd = s.qty[s.focused].Update(m)
	} else {
		s.notes, cmd = s.notes.Update(m)
	}
	return s, cmd
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
		edge := "the last row"
		if dir < 0 {
			edge = "the first row"
		}
		return s.say(k+" is already at "+edge+" · "+s.waysOut(headerRows), StatusInfo)
	}
	s.currentInput().Blur()
	s.focused = next
	s.currentInput().Focus()
	return nil
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

// keySerial handles keys during phase-2 serial capture.
func (s *ReceiveFormScreen) keySerial(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	k := m.String()
	if s.serialPending {
		if k == "esc" {
			// Abandon any remaining captures and show the summary. Not gated
			// while the save is out, for the reason esc is never gated on these
			// screens: the unit already sent may still land, and a frame with
			// no way out is the worse defect.
			return s, s.finishSerial()
		}
		return s, s.declineFrozen(k, headerRows)
	}
	switch k {
	case "esc":
		return s, s.finishSerial()
	case "enter":
		return s.submitSerial()
	}
	var cmd tea.Cmd
	s.serialInput, cmd = s.serialInput.Update(m)
	return s, cmd
}

// finishSerial closes capture and shows the summary.
func (s *ReceiveFormScreen) finishSerial() tea.Cmd {
	s.phase = phaseDone
	s.serialInput.Blur()
	return nil
}

// keyDone is the summary's keyboard. It used to be "any key returns", which is
// not a claim an action bar can make honestly — and a scanner burst arriving on
// this frame would have dismissed the summary before anyone read it. Enter and
// Esc both go back, both are named, and everything else says what it did.
func (s *ReceiveFormScreen) keyDone(m tea.KeyMsg, headerRows int) (Screen, tea.Cmd) {
	switch k := m.String(); k {
	case "enter", "esc":
		return s, s.leave()
	default:
		return s, s.decline(k, headerRows)
	}
}

// ---------------------------------------------------------------------------
// The receipt
// ---------------------------------------------------------------------------

func (s *ReceiveFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
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
			s.serialUnits = nil
			return s, s.say(
				fmt.Sprintf("line %d: quantity must be a whole number, 0 or more — %q is not", i+1, raw),
				StatusError)
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
		// The BACKSTOP, not the ordinary path: the bar stops naming Enter the
		// moment entryState leaves receiveAttemptable, and the arm above
		// refuses before submit is reached, so nothing an operator can type
		// arrives here today. It is kept because the refusal must not depend on
		// two predicates agreeing — a future arm that submits without asking
		// would otherwise post an empty receipt — and it reads its sentence off
		// entryRefusal so the screen cannot refuse the same fact in two voices.
		s.serialUnits = nil
		return s, s.say("nothing to receive — "+s.entryRefusal(), StatusWarn)
	}
	req := omsapi.ReceiveRequest{
		Items:        items,
		ReceiptNotes: strings.TrimSpace(s.notes.Value()),
	}
	poID := fmt.Sprint(s.po.ID)
	s.pending = true
	s.clearFail()
	// The payload has gone, so nothing that shaped it may keep a caret: a
	// cursor blinking in a field whose contents are already on the wire says
	// the opposite of what the frozen bar says.
	s.currentInput().Blur()
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		out, err := deps.OMS.ReceivePOItems(ctx, poID, req)
		return receiveSubmittedMsg{po: out, err: err}
	}
}

// handleReceived is the receipt's reply.
//
// A SUCCESS ends the flow — into serial capture when a serialized line was
// received, and otherwise straight to the summary. It used to land back on the
// quantity form with the boxes still full, where a reflexive second Enter
// booked the whole delivery again and the only sign the first had worked was a
// flash that expires in four seconds.
func (s *ReceiveFormScreen) handleReceived(m receiveSubmittedMsg) tea.Cmd {
	s.pending = false
	if m.err != nil {
		// The form comes back live: a failed receipt must not hold the
		// operator's quantities hostage to a gateway.
		s.serialUnits = nil
		s.currentInput().Focus()
		head, detail := receiveFailure("Receiving "+s.orderName(), m.err)
		s.setFail(head, detail)
		return tea.Batch(Status(head, StatusError), textinput.Blink)
	}

	label := m.po.Number
	if label == "" {
		label = fmt.Sprintf("PO #%v", m.po.ID)
	}
	if m.po.IsFullyReceived {
		s.receipt = fmt.Sprintf("%s fully received", label)
	} else {
		s.receipt = fmt.Sprintf("%s received · %d/%d units", label, m.po.TotalReceivedQuantity, m.po.TotalQuantity)
	}
	if len(s.serialUnits) > 0 {
		s.phase = phaseSerial
		s.serialCursor = 0
		s.serialInput.SetValue("")
		s.serialInput.Focus()
		return tea.Batch(Status(s.receipt, StatusOK), textinput.Blink)
	}
	s.phase = phaseDone
	return Status(s.receipt, StatusOK)
}

// ---------------------------------------------------------------------------
// Serial capture
// ---------------------------------------------------------------------------

// submitSerial creates a SerializedComponent for the current unit (blank =
// skip) and, on success, accessions it into stock via the receive action.
func (s *ReceiveFormScreen) submitSerial() (Screen, tea.Cmd) {
	if s.serialCursor >= len(s.serialUnits) {
		return s, s.finishSerial()
	}
	serial := strings.TrimSpace(s.serialInput.Value())
	if serial == "" {
		// Blank = skip this unit (serial unknown or captured elsewhere).
		s.skippedCount++
		s.serialErr = ""
		s.advanceSerial()
		return s, nil
	}
	unit := s.serialUnits[s.serialCursor]
	s.serialPending = true
	s.serialErr = ""
	// Blurred for as long as the create is out, so the frozen bar and the pane
	// agree about who owns the keyboard.
	s.serialInput.Blur()
	deps := s.deps
	ctx := s.ctx()
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

// handleSerialUnit is one capture's reply.
//
// A FAILURE stays on the unit with what was typed still in the box, so Enter
// tries again. It used to advance regardless: the error was then drawn under
// the NEXT unit's prompt, reading as a complaint about a unit that had not been
// attempted, and the serial the operator had entered was discarded with no way
// to re-enter it — "never silently discard what the operator typed", on the one
// screen whose whole job is capturing what they typed.
func (s *ReceiveFormScreen) handleSerialUnit(m serialUnitDoneMsg) tea.Cmd {
	s.serialPending = false
	if s.phase == phaseSerial {
		s.serialInput.Focus()
	}
	if m.err != nil {
		s.failedCount++
		s.serialErr = receiveReason(m.err)
		return tea.Batch(Status("serial capture failed", StatusError), textinput.Blink)
	}
	s.serialErr = ""
	if m.created {
		s.createdCount++
	}
	if m.inStock {
		s.inStockCount++
	}
	s.advanceSerial()
	return textinput.Blink
}

// advanceSerial moves to the next capture slot, finishing into the summary
// when the queue is exhausted.
//
// The caret only ever goes back into the box while CAPTURE is still the phase
// on screen. Esc pressed while a create is in flight is not gated — no screen
// here gates the way out — and it runs finishSerial, which moves to the summary
// and blurs the box; the reply then lands with units still enrolled and this
// function would have re-focused a field the summary does not draw.
// handleSerialUnit already asks the same question one line above its own
// Focus(), and a guard undone by the call underneath it protects nothing: the
// state is what decides who owns the caret, in both places and for the same
// reason.
func (s *ReceiveFormScreen) advanceSerial() {
	s.serialCursor++
	s.serialInput.SetValue("")
	if s.serialCursor >= len(s.serialUnits) {
		s.phase = phaseDone
		s.serialInput.Blur()
		return
	}
	if s.phase == phaseSerial {
		s.serialInput.Focus()
	}
}

// uncapturedUnits is how many enrolled units never got a serial — derived from
// where the cursor stopped rather than counted alongside it, so the summary
// cannot disagree with the flow.
func (s *ReceiveFormScreen) uncapturedUnits() int {
	if n := len(s.serialUnits) - s.serialCursor; n > 0 {
		return n
	}
	return 0
}

// poLineSerialized reports whether a PO line's underlying inventory item is
// serialized, returning the item's UUID (needed to create the units). Freeform
// / asset lines have no item_details and return ok=false.
//
// A KIT LINE is not such a line, whatever its item_details say, and that is the
// rule this function states rather than a condition bolted onto one caller: the
// question "does this line's units get serials?" is asked here by both submit()
// (which enrolls a capture slot per received unit) and lineCaveats (which draws
// the caveat promising phase 2 under that line's quantity box), and two
// different answers would be their own defect — the form would advertise a
// capture the receipt then refuses to open.
//
// Skipping a kit loses nothing legitimate. KitComponent.clean() REFUSES a
// serialized component — "Serialized items cannot be kit components — receiving
// the kit would credit stock without recording serial numbers" — so there is no
// valid kit receipt for which serial capture is the right behaviour. A kit line
// whose item carries is_serialized=true is carrying a flag that is already
// wrong, reachable because InventoryItem.save() never runs full_clean(), so
// _clean_kit never fires on a direct write. Acting on it would create
// SerializedComponents against the KIT's id and accession them into a stock
// figure nothing can ever draw down — and unlike every other path into that
// corruption, this one fires on SUBMIT, with no keypress for the operator to
// catch it on.
func poLineSerialized(li omsapi.PurchaseOrderItem) (itemID string, ok bool) {
	if li.IsKitLine {
		return "", false
	}
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
// to make impossible, and it is the shape po_create.go's barFor / barItems pair
// uses for the same reason: the decision is passed IN rather than recomputed
// against a frame that has moved on.
func (s *ReceiveFormScreen) barFor(headerRows int) []actionBarItem {
	switch s.phase {
	case phaseSerial:
		return s.serialBar()
	case phaseDone:
		return []actionBarItem{{"Enter/Esc", "Back to order"}}
	}
	return s.qtyBarItems(s.qtyPagesFor(headerRows))
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
	if s.entryState() == receiveAttemptable {
		// Named only when there is something to ATTEMPT. With every box empty —
		// or every box holding a zero, which submit skips and which therefore
		// never leaves the terminal — Enter cannot receive anything and can
		// only refuse, and a bar that names it there teaches a key that does
		// not work. What Enter does with a quantity the PARSER or the server
		// will reject is still an act: it reports the refusal naming the line,
		// which is the answer the operator needs.
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
	return items
}

// hasQuantityEntry reports whether any quantity box holds something. It is what
// Esc's label is decided by — leaving destroys the screen and takes a typed
// zero with it exactly as it takes a typed 2 — and is NOT what decides whether
// the bar names Enter. entryState answers that.
func (s *ReceiveFormScreen) hasQuantityEntry() bool {
	for _, ti := range s.qty {
		if strings.TrimSpace(ti.Value()) != "" {
			return true
		}
	}
	return false
}

// receiveEntry is what the quantity boxes amount to: the four different answers
// Enter can give, which are four different sentences and not one.
type receiveEntry int

const (
	// receiveNoLines — every line is voided or already received in full.
	receiveNoLines receiveEntry = iota
	// receiveNothingTyped — receivable lines, every box empty.
	receiveNothingTyped
	// receiveAllZero — boxes were typed into and every one of them parses as 0.
	receiveAllZero
	// receiveAttemptable — at least one box holds something submit will really
	// attempt, which includes a value the server or the parser will reject.
	receiveAttemptable
)

// entryState classifies the boxes, and it is the ONE predicate the bar and the
// Enter arm both read.
//
// The distinction that matters is between a value that leaves the terminal and
// one that cannot. The predicate this replaced was "any box holds something",
// written reasoning about TYPOS: a box holding "two" is an attempt, and the
// refusal naming the line ("line 1: quantity must be a whole number, 0 or more
// — \"two\" is not") is the answer to that attempt, so Enter is named and Enter
// acts. That reasoning is right and it does not extend to a ZERO. A box holding
// "0" is skipped by submit, leaves nothing to post, and comes straight back as
// a local refusal — so the bar named a key whose whole effect was to write a
// note, which is the bar-honesty rule broken on the phase the operator lives
// on. "0" stays out of the ATTEMPT and stays in hasQuantityEntry, because it is
// still something Esc would throw away.
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
// hit — the same reason the empty and all-zero cases were separate before this.
//
// One home for the wording, read by the Enter arm and by submit's own backstop,
// so a screen that refuses in two places cannot refuse in two voices.
func (s *ReceiveFormScreen) entryRefusal() string {
	switch s.entryState() {
	case receiveNoLines:
		return "no line on this order is receivable"
	case receiveAllZero:
		return "every quantity entered is zero"
	}
	return "type a quantity against a line"
}

// anythingTyped reports whether Esc would throw entry away — the quantities
// AND the notes, because Esc destroys the screen and takes both with it.
func (s *ReceiveFormScreen) anythingTyped() bool {
	return s.hasQuantityEntry() || strings.TrimSpace(s.notes.Value()) != ""
}

// qtyPagesFor reports whether the form is taller than the pane shows on a frame
// whose pinned header is `headerRows` tall — the only state where PgUp/PgDn
// move anything, and therefore the only state the bar may name them in.
//
// TWO things are held fixed here and they are fixed for different reasons.
//
// The BAR is the tallest one (`qtyBarItems(true)`) because a taller bar is a
// smaller body: a body that overflows the smallest budget also overflows the
// larger one left when the keys are dropped, so measuring against the tallest
// is a genuine fixed point and the answer cannot oscillate between frames.
//
// The HEADER is a PARAMETER, and since receiveNoteRows it is no longer the NOTE
// that makes it one — the note block is the same height in every state, so
// writing or retiring a note cannot move this answer at all. That was the whole
// point of reserving it, and the hazard this paragraph used to describe (a note
// assumed present naming a key that is dead on a note-free frame) no longer
// exists, because a note-free frame no longer exists.
//
// What is left varying is the FAILURE DETAIL, and it is legitimately variable
// for the reason it is not reserved: it is written by a reply off the wire, it
// names no keys, and both the bar and this guard see it identically — a frame
// that grows one is a frame that genuinely changed. The parameter pins the
// question to ONE frame so that reply cannot move the answer under a key arm
// mid-dispatch: View binds it to the frame it is drawing, and a key arm to the
// frame the press was made against (handleKey).
func (s *ReceiveFormScreen) qtyPagesFor(headerRows int) bool {
	// TWO conditions, because the keys make two claims and both have to hold.
	//
	// The body must MOVE — that is the binding this conversion added, and it is
	// bodyScrollsForBar's question. But PgUp/PgDn do not scroll the body: they
	// move the CURSOR (jdePageCursor) and the window follows it, so a page can
	// only do anything when there is another row to land on. Those two questions
	// agree in almost every state and come apart in one that is DESIGNED: an
	// order whose lines are all voided or already received leaves no quantity
	// boxes, so the notes row is the only navigable row, and jdePageCursor
	// clamps to count-1 = 0 and returns the row it was handed. At 80x18 the body
	// still overflowed, so the bar printed PgUp/PgDn=Page over a key whose whole
	// effect was to write "pgdown is already at the last row" — a key named on
	// the bar that cannot act, which is the one thing this screen's bar may
	// never do.
	//
	// Both halves live here rather than in the arm so the bar and pageQty read
	// ONE expression, the way they were bound together when the header was: two
	// conditions that agree in most states are two conditions that will
	// eventually disagree in one. UP/DN was already written this way —
	// qtyBarItems names it on totalInputs() > 1 and keyQty declines on the same
	// predicate — which is the shape this now matches.
	return s.totalInputs() > 1 &&
		s.bodyScrollsForBar(s.qtyBody(), headerRows, s.qtyBarItems(true))
}

// qtyPages is qtyPagesFor bound to the frame being drawn now, for View and for
// a test asking what the operator can see.
func (s *ReceiveFormScreen) qtyPages() bool {
	return s.qtyPagesFor(len(s.headerLines()))
}

// qtyStepFor is how many rows one page covers on a frame whose pinned header is
// `headerRows` tall — measured off the same window that frame draws, so a page
// moves by exactly what the operator could see on it. The arithmetic is the
// LAYER's (windowRowsForBar), not a local copy of it, for the reason that
// method's own comment records; the header is a parameter for the reason
// qtyPagesFor's is.
func (s *ReceiveFormScreen) qtyStepFor(headerRows int) int {
	return s.windowRowsForBar(s.qtyBody(), s.focused, headerRows, s.qtyBarItems(true))
}

// serialBar follows the BOX: with nothing in it, Enter skips the unit, and
// saying "Save" there would name a key that does something else.
func (s *ReceiveFormScreen) serialBar() []actionBarItem {
	if s.serialPending {
		return []actionBarItem{{"Esc", "Finish"}}
	}
	commit := "Skip unit"
	if strings.TrimSpace(s.serialInput.Value()) != "" {
		// A box the LAST attempt left filled is a retry rather than a fresh
		// save, and the two are worth different words: the operator needs to
		// know Enter will try the same serial again rather than move on.
		commit = "Save serial"
		if s.serialErr != "" {
			commit = "Retry serial"
		}
	}
	return []actionBarItem{{"Enter", commit}, {"Esc", "Finish"}}
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
	status := s.statusRow(s.pending || s.serialPending, s.workingLine(), "")
	if status == "" {
		status = s.statusRow(false, "", s.failLine())
	}
	body, cursor := s.body()
	return s.frameWrapped(s.headerLines(), body, cursor, status, s.bar())
}

// body is the phase's scrollable body and the row the window is anchored on,
// answered in ONE place so that a sweep asking what the frame draws cannot end
// up asking a different builder from the one View picks. (The paging guards
// still read qtyBody directly: those are questions about the quantity form in
// particular, asked only from arms that phase owns.)
//
// It exists because the rule these bodies are built to — EVERY LINE BELONGS TO
// A NAVIGABLE ROW, see qtyBody — was applied to the quantity form alone and the
// other two went on stranding their leads for a round, with nothing able to
// notice. A roster of body builders kept in a test is the hand-maintained list
// this project keeps being bitten by; a switch the sweep and the frame both
// read is not, and a phase added to the iota tomorrow arrives here or it draws
// nothing at all. TestReceive_EveryBodyLineBelongsToANavigableRow walks the
// phase cases through this.
func (s *ReceiveFormScreen) body() (*jdeLines, int) {
	switch s.phase {
	case phaseSerial:
		return s.serialBody(), 0
	case phaseDone:
		return s.doneBody(), 0
	}
	return s.qtyBody(), s.focused
}

// workingLine names the work AND the subject: "Submitting…" tells an operator
// nothing they could act on while a gateway thinks.
func (s *ReceiveFormScreen) workingLine() string {
	if s.pending {
		return "Booking the delivery against " + s.orderName() + "…"
	}
	if s.serialCursor < len(s.serialUnits) {
		u := s.serialUnits[s.serialCursor]
		return fmt.Sprintf("Recording the serial for unit %d of %d…", u.unitNo, u.unitTot)
	}
	return "Recording the serial…"
}

// failLine is the headline the status row carries: the receipt's failure, or a
// capture's. Only the headline — the unbounded half is folded in the body.
func (s *ReceiveFormScreen) failLine() string {
	if s.failHead != "" {
		return s.failHead
	}
	if s.serialErr != "" {
		return "Serial capture failed"
	}
	return ""
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
// Three text rows is what the longest sentence this screen can produce folds to
// at the narrowest pane it supports (51 cells, so 49 after the indent) — the
// all-zero Enter refusal and the page-edge notes, each around 110 cells with
// their way-out tail. That margin is ZERO rather than comfortable: one more bar
// item, or one more word in entryRefusal, puts either over.
// TestReceive_EveryNoteFitsItsReservation drives the states that produce them
// and fails if one is cut, because the tail is where the key that gets the
// operator out is named; receiveNoteDropMark makes a cut VISIBLE even where
// that probe list misses it. If a new sentence does not fit, shorten the
// SENTENCE rather than raising this and paying another row on every frame.
const receiveNoteRows = 3

// receiveNoteDropMark is what the note leaves behind when it does not fit.
//
// Every other bound on these screens marks what it gave up — poRowDropMark on a
// picker row, jdeLines.Window's hidden-row count, fitCell's ellipsis — and this
// one
// used to be the exception: it stopped at receiveNoteRows and drew nothing to
// say so. What it drops is the TAIL, which on these sentences is where the key
// that gets the operator out is named, and noteLines' own comment says a
// clipped hint is worse than none because they believe they read it. A silent
// cut made that sentence false of the code two lines under it.
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

// barCeiling is the tallest bar this phase can draw, and it is deliberately
// blind to headerRows: it is what the header allowance measures itself against,
// so a bar that asked the header how tall it was would close a loop. On the
// quantity phase that is the bar WITH the paging keys on it, which is the same
// fixed point qtyPagesFor measures against and for the same reason — a body
// that overflows the smallest budget also overflows the larger one.
func (s *ReceiveFormScreen) barCeiling() []actionBarItem {
	switch s.phase {
	case phaseSerial:
		return s.serialBar()
	case phaseDone:
		return []actionBarItem{{"Enter/Esc", "Back to order"}}
	}
	return s.qtyBarItems(true)
}

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
	if s.note.text == "" || rows <= 0 {
		return s.note
	}
	bounded := cellPrefix(s.note.text, rows*width)
	if bounded == s.note.text && len(s.note.renderLines(width)) <= rows {
		return s.note
	}
	words := strings.Fields(bounded)
	for keep := len(words) - 1; keep > 0; keep-- {
		trial := s.note
		trial.text = strings.Join(words[:keep], " ") + receiveNoteDropMark
		if len(trial.renderLines(width)) <= rows {
			return trial
		}
	}
	for room := rows * width; room > 0; room-- {
		trial := s.note
		trial.text = cellPrefix(bounded, room) + receiveNoteDropMark
		if len(trial.renderLines(width)) <= rows {
			return trial
		}
	}
	trial := s.note
	trial.text = strings.TrimSpace(receiveNoteDropMark)
	return trial
}

// failDetailText is the unbounded half of whatever failure is standing, and it
// is the ONE predicate for "is there a detail to draw". headerSplit asks it to
// decide whether the detail's floor is owed, and failDetailLines asks it for
// the text: a second copy of that condition would let the budget reserve a row
// the renderer does not fill, or the renderer want a row the budget never gave.
func (s *ReceiveFormScreen) failDetailText() string {
	if s.failDetail != "" {
		return s.failDetail
	}
	return s.serialErr
}

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
func (s *ReceiveFormScreen) failDetailLines() []string {
	detail := s.failDetailText()
	if detail == "" {
		return nil
	}
	width := s.paneWidth() - len(jdeIndent)
	if width < 12 {
		width = 12
	}
	rows := s.failDetailRows()
	if rows <= 0 {
		return nil
	}
	trimmed := cellPrefix(detail, rows*width)
	out := make([]string, 0, rows)
	for i, line := range pickerWrap(trimmed, width) {
		if i >= rows {
			break
		}
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	return out
}

// ---------------------------------------------------------------------------
// The quantity form
// ---------------------------------------------------------------------------

// qtyBody is the scrollable body of phase 1: one block per receivable line,
// then the notes row.
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
// So the lead is gone rather than merely re-tagged, and the difference matters.
// Tagging those five lines onto row 0 makes them reachable and pays for it at
// the other end: Window keeps a block's START when the block will not fit, so
// the quantity box — five rows further down the block — leaves the pane
// instead. At 80x22 with the same order that is exactly what it does, which is
// defect (1) of this conversion coming back by another route. What each line
// needs is drawn on that line's own row (lineCaveats), the order is named by
// the title Root pins above the pane on every frame — which is a claim Title
// had to be corrected to honour, since it answered a generic "Receive Items"
// for an order carrying no number and that is exactly the order this body no
// longer names — and what is left over belongs to the notes row.
//
// Every line of a block is tagged with that block's navigable ROW, so
// jdeLines.Window keeps the name, the readings, the quantity box and the kit
// breakdown on screen TOGETHER.
//
// Before the conversion the form did not window at all — it built one string
// and handed it over, and clampToBox cut whatever did not fit, silently, from
// the bottom. Measured on the old frame at 80x24, where the pane keeps 18 rows
// and each plain line costs four:
//
//	three lines  — 19 rows: the key hint goes. Every key on the screen is
//	               unnamed, on a form that has no other way of saying what Enter
//	               does.
//	four lines   — 23 rows: the notes field goes with it.
//	five lines   — 27 rows: the fifth line's whole block goes — its name, its
//	               readings and the quantity box the operator was about to type
//	               into. Nothing on the pane said there was a fifth line.
//
// A purchase order with five receivable lines is not an edge case, and none of
// this was visible from inside the screen: clampToBox truncates in Root.
func (s *ReceiveFormScreen) qtyBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()
	// The notes row is the last navigable row, and it is where everything that
	// is not a receivable line hangs: on an order with lines, the blank that
	// closes the block above it; on an order with none, the sentence saying so.
	notesRow := len(s.qty)
	// AddFittedFields is the LAYER's — it fits the row to the pane and keeps any
	// folded hint on the SAME navigable row, so the window cannot separate a
	// field from the note explaining it. This screen carried a line-for-line
	// copy of its body until sc-jde-recv; the copy that gets tolerated is the
	// one the next fifty grow from, which is the whole history sc-jde-lift
	// exists to record.
	notes := []jdeField{{
		Label:   "Notes",
		Kind:    jdeText,
		Input:   &s.notes,
		Width:   40,
		Hint:    "optional",
		Focused: s.caretOn(notesRow),
	}}

	if len(s.qty) == 0 {
		// The FIELD leads, and the sentence explaining the order follows it —
		// serialBody's rule, applied to the one other body on this screen that
		// is in serialBody's situation. There are no quantity boxes here, so
		// the notes row is the ONLY navigable row: up, down, pgup and pgdown
		// all decline, nothing moves the window, and Window keeps a block's
		// START — so whatever leads this block is the whole of what a short
		// pane keeps, permanently.
		//
		// Drawn the other way round it kept the prose. At 80x16 the window is
		// one row: the pane read "No receivable lines on this order." and
		// "↓ 4 more below", with the FOCUSED notes box off the pane — an
		// operator typing into a field they cannot see, every keystroke
		// redrawing the pane byte for byte, which is the reported-hang class
		// this conversion exists to remove. Between 80x13 and 80x15 it went
		// with no marker at all.
		//
		// The blank that used to sit between the prose and the box is gone with
		// the reorder: after the field it would be the second line a two-row
		// window draws, spending on nothing the row the sentence needs.
		l.AddFittedFields(notes, lw, width, notesRow)
		l.AddRow(notesRow, jdeIndent+StyleMuted.Render("No receivable lines on this order."))
		for _, line := range jdeCaveatLines(
			"Every line is voided or already received in full, so there is nothing to book here.", width) {
			l.AddRow(notesRow, line)
		}
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
			//
			// This was written the other way round for the first block only,
			// and the trailing blank below already had this reasoning beside
			// it — so the rule was honoured for the last separator and broken
			// for every other one.
			l.AddRow(i-1, "")
		}
		s.addLineBlock(l, i, lw, width)
	}
	// The same rule for the last block: this blank closes it rather than
	// opening the notes row, which would otherwise draw a blank where the
	// FOCUSED notes field belongs.
	l.AddRow(len(s.qty)-1, "")
	l.AddFittedFields(notes, lw, width, notesRow)
	return l
}

// caretOn reports whether row i is the one that may be TYPED INTO right now,
// which is not the same question as which row the operator is standing on.
//
// jdeFieldArea draws a focused text row as a solid reverse-video field — the
// layer's strongest "you are standing here and may type" signal — and while the
// receipt is out every key but Esc declines, so a row left highlighted through
// the freeze is the bar-honesty rule broken in its most visual form: the pane
// invites the one thing submit() has just blurred the box to refuse. serialBody
// answers the identical question with `Focused: !s.serialPending` one function
// over, so the rule is now stated the same way on both phases rather than
// honoured on one of them.
//
// Nothing is lost by dropping the fill. The row's number keeps its focused
// style, and its name, its readings and its typed quantity all stay drawn, so
// the operator keeps their place — and the frozen bar plus the "Booking the
// delivery against …" status row already say what state the screen is in.
func (s *ReceiveFormScreen) caretOn(i int) bool {
	return s.focused == i && !s.pending
}

// lineCaveats are what a receivable line has to say about itself beyond its
// readings: that it is a kit, that it is serialized. They are drawn on the
// LINE's own navigable row rather than once at the top of the form, which is
// the whole of the fix qtyBody's comment records — a sentence above the first
// row is a sentence no key can reach once the body overflows.
//
// The trade is stated rather than assumed. Standing, each sentence was drawn
// once for the whole order; per line it is drawn once per line it is true of,
// so an order with three kit lines carries it three times. That is more rows
// than before — but they are rows inside a windowed block, so they cost the
// pane nothing except while the cursor is on that very line, which is the one
// moment the sentence is about: it explains the box the number is going into.
func lineCaveats(line omsapi.PurchaseOrderItem, width int) []string {
	var out []string
	if line.IsKitLine {
		// Wrapped rather than clipped, and WARN rather than muted: this is the
		// sentence that stops "received 2" being read as two of the thing named
		// on the line.
		out = append(out, receiveCaveatLines(receiveKitCaveat, StyleStatusWarn, width)...)
	}
	if _, ok := poLineSerialized(line); ok {
		// The SAME predicate the enrolment reads (poLineSerialized), so the
		// form cannot promise a capture the receipt will not open: a kit line
		// can look serialized and enrols nothing, and a caveat drawn off a
		// broader test would advertise a phase that never arrives.
		out = append(out, receiveCaveatLines(receiveSerialCaveat, StyleMuted, width)...)
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
// box, and — for a kit — what the quantity currently typed would credit.
func (s *ReceiveFormScreen) addLineBlock(l *jdeLines, i, lw, width int) {
	line := s.lines[i]
	// The heading's marker and the box's fill answer DIFFERENT questions, which
	// is why they are asked separately here. The number stays styled for
	// whichever row the cursor is on — that is the operator's PLACE, and a
	// freeze that took it away would leave them hunting for it when the receipt
	// answers — while the box's reverse-video fill says "type here", which is
	// false for as long as the receipt is out (caretOn).
	l.AddRow(i, jdeIndent+s.lineHeading(i, line, s.focused == i, width))
	for _, row := range jdeWrapTokens(receiveLineTokens(line), receiveMetaIndent, width) {
		l.AddRow(i, row)
	}
	qty := jdeField{
		Label:   "Quantity",
		Kind:    jdeText,
		Input:   &s.qty[i],
		Width:   8,
		Focused: s.caretOn(i),
	}
	if line.IsKitLine {
		// The unit of the box, right beside the box. "ordered 2 kits" says it
		// once on the row above; this says it where the number is typed.
		qty.Hint = "kits"
	}
	l.AddFittedFields([]jdeField{qty}, lw, width, i)
	// AFTER the box, never before it. These lines are part of row i's block, so
	// Window keeps them with the box — but a block too tall for the pane keeps
	// its START, so anything placed ahead of the box is a row the box is pushed
	// down by, and three caveat rows ahead of it is the box off the pane at
	// 80x22. Behind it they cost the tail of the credit preview instead, which
	// is a figure that recomputes rather than a field being typed into.
	for _, cl := range lineCaveats(line, width) {
		l.AddRow(i, cl)
	}
	// The breakdown sits directly under the box it is a preview of, and
	// recomputes from what is currently typed there.
	for _, kl := range s.kitCreditLines(line, s.qty[i].Value()) {
		l.AddRow(i, kl)
	}
}

// lineHeading is a receivable line's name row: the number, the kit tag, then as
// much of the label as the pane has left.
//
// The order is deliberate. The tag leads because it is what changes the meaning
// of the quantity box below it, and a tag after a long name is the first thing
// clampToBox cuts. The name is then FITTED rather than left to overrun, because
// this row is the one an operator reads to decide which line they are typing
// into, and a name silently cut at the pane edge reads as a different (shorter)
// line. The number is styled, never dropped: it is what the refusal messages
// name ("line 2: quantity must be…").
func (s *ReceiveFormScreen) lineHeading(i int, line omsapi.PurchaseOrderItem, focused bool, width int) string {
	num := fmt.Sprintf("%-2d ", i+1)
	tag := ""
	if line.IsKitLine {
		tag = poKitTag + " "
	}
	label := line.DisplayLabel()
	if width > 0 {
		if room := width - len(jdeIndent) - lipgloss.Width(num) - lipgloss.Width(tag); room > 0 {
			label = fitCell(label, room)
		}
	}
	lead := StyleMuted.Render(num)
	if focused {
		lead = StyleJDELabelFocused.Render(num)
	}
	if tag == "" {
		return lead + label
	}
	return lead + StyleStatusWarn.Render(poKitTag) + " " + label
}

// receiveLineTokens are a line's readings, laid out under its name by
// jdeWrapTokens — which WRAPS rather than trimming, because the reading an
// ellipsis would eat is the LAST one, and that is where "pending" sits.
func receiveLineTokens(line omsapi.PurchaseOrderItem) []jdeToken {
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
	return toks
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
	return poKitCreditBlock(line.KitComponents, lead, receiveMetaIndent, s.bodyWidth(), kits)
}

// ---------------------------------------------------------------------------
// Serial capture
// ---------------------------------------------------------------------------

// serialBody is phase 2: the box a serial is scanned into, then what that box
// is FOR.
//
// The field comes FIRST and everything identifying it follows, which is the
// opposite of how a form usually reads and is the only order this phase can
// afford. Two facts decide it. jdeLines.Window keeps a block's START when the
// block will not fit, so whatever leads the block is what a short pane keeps —
// and NO key on this phase moves a cursor at all: keySerial binds Enter and Esc
// and nothing else, and body() anchors the window on row 0. So there is exactly
// one block here, nothing can ever sit above the window, and the one choice
// left is which end of that block a short pane keeps.
//
// It used to lead with a heading, a progress counter and a blank, all tagged
// jdeNoRow, with the Serial field the only line belonging to row 0. Window
// therefore anchored on the LAST line and drew "↑ N more above" over four lines
// no key could fetch: at 80x17 the item label the serial is being scanned
// AGAINST was off the pane, and at 80x18 the counter went the same way. Tagging
// that lead onto row 0 without reordering only moves the loss to the other end
// — the block starts at the heading and the FOCUSED box leaves the pane
// instead, which is the operator scanning a barcode into a field they cannot
// see and cannot check before Enter commits it.
//
// So the box leads, the item and the unit follow it, and the standalone heading
// is gone: the phase names itself on the row that carries the counter
// ("capture 1 of 3 · …"), on the field's own label, and on a bar reading
// Enter=Save serial · Esc=Finish. That heading was a row the pane paid for
// before it paid for the box a scanner is already firing into.
func (s *ReceiveFormScreen) serialBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()
	total := len(s.serialUnits)
	shown := s.serialCursor + 1
	if shown > total {
		shown = total
	}
	// "capture N of M" rather than "unit N of M": the per-line row below says
	// "unit N of M on this line", and two adjacent counters both reading
	// "unit N of M" over different denominators is a row an operator has to
	// stop and decode. It also carries the word the dropped heading carried.
	progress := jdeIndent + StyleMuted.Render(fmt.Sprintf(
		"capture %d of %d · created %d · skipped %d", shown, total, s.createdCount, s.skippedCount))

	if s.serialCursor >= total {
		// Nothing left to capture, so there is no box to lead with. Both lines
		// still belong to row 0: a line tagged jdeNoRow is a line no key can
		// bring back, and this phase has no key that moves a cursor at all.
		l.AddRow(0, jdeIndent+StyleMuted.Render("Every enrolled unit has been answered."))
		l.AddRow(0, progress)
		return l
	}

	unit := s.serialUnits[s.serialCursor]
	label := unit.label
	if width > 0 {
		if room := width - len(jdeIndent); room > 0 {
			label = fitCell(label, room)
		}
	}
	l.AddFittedFields([]jdeField{{
		Label: "Serial",
		Kind:  jdeText,
		Input: &s.serialInput,
		Width: 30,
		// No hint. The BAR carries this row's one fact and carries it
		// dynamically — Enter reads "Skip unit" while the box is empty and
		// "Save serial" once it is not — so a hint saying the same thing would
		// be a second, static claim about the same key, and it cost the field
		// twelve columns of a 51-column pane to make.
		Focused: !s.serialPending,
	}}, lw, width, 0)
	l.AddRow(0, jdeIndent+label)
	l.AddRow(0, receiveMetaIndent+StyleMuted.Render(
		fmt.Sprintf("unit %d of %d on this line", unit.unitNo, unit.unitTot)))
	l.AddRow(0, progress)
	return l
}

// ---------------------------------------------------------------------------
// The summary
// ---------------------------------------------------------------------------

// doneBody reports what the visit did, as columnar value rows. Rows that would
// report nothing are left out rather than drawn as zeroes: "Failed ..... 0" is
// a row that makes an operator look for a failure there was none of.
//
// Every line belongs to row 0, and this one is bookkeeping in the sense that it
// changes nothing DRAWN today — say so rather than dress it up. The summary
// carries no input, so block(0) used to degenerate to the layer's (0,0) answer
// for "this row owns nothing", which happens to leave the window at the top,
// which is where it belongs. What the tagging buys is that the safety stops
// being a coincidence two functions apart: the rule this screen's bodies are
// built to is that no line sits outside a block, one sweep checks it over every
// phase, and a summary line added later cannot be the one that quietly opts
// out. What a pane too short for the summary loses either way is the TAIL,
// which is the order these rows are already written in — the receipt first, the
// counts that are only drawn when they are non-zero last — and that is the
// layer's behaviour for any block bigger than the pane.
func (s *ReceiveFormScreen) doneBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	l.AddRow(0, StyleJDEHeading.Render("Receive complete"))
	l.AddRow(0, "")

	fields := []jdeField{{Label: "Receipt", Kind: jdeValue, Value: s.receipt, Dim: s.receipt == ""}}
	if fields[0].Value == "" {
		fields[0].Value = "(nothing recorded)"
	}
	if len(s.serialUnits) > 0 {
		serials := fmt.Sprintf("%d created", s.createdCount)
		if s.inStockCount > 0 {
			serials += fmt.Sprintf(" · %d accessioned into stock", s.inStockCount)
		}
		fields = append(fields, jdeField{Label: "Serials", Kind: jdeValue, Value: serials})
		if s.skippedCount > 0 {
			fields = append(fields, jdeField{
				Label: "Skipped", Kind: jdeValue,
				Value: fmt.Sprintf("%d %s", s.skippedCount, plural("unit", s.skippedCount)),
			})
		}
		if s.failedCount > 0 {
			fields = append(fields, jdeField{
				Label: "Failed", Kind: jdeValue,
				Value: fmt.Sprintf("%d %s", s.failedCount, plural("attempt", s.failedCount)),
			})
		}
		if n := s.uncapturedUnits(); n > 0 {
			fields = append(fields, jdeField{
				Label: "Uncaptured", Kind: jdeValue,
				Value: fmt.Sprintf("%d %s · finished early", n, plural("unit", n)),
			})
		}
	}
	for i := range fields {
		// The values here are screen-composed sentences, but the receipt one
		// carries the order's number off the wire, so every row is fitted to
		// the pane rather than trusted to be short.
		if width > 0 {
			if room := jdeStripWidth(width, lw); room > 0 {
				fields[i].Value = fitCell(fields[i].Value, room)
			}
		}
		l.AddRow(0, renderJDEField(fields[i], lw, width))
	}
	return l
}
