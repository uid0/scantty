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

// receiveKitCaveat is the standing warning for an order carrying a kit. Its
// second half is the half that matters — an operator who reads only "kit lines
// credit" has been told nothing — so it is WRAPPED wherever it is drawn rather
// than clipped.
const receiveKitCaveat = "This order contains kit lines. Receiving one credits the kit's " +
	"COMPONENT items, not the kit — the quantity you type is a number of kits."

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

func (s *ReceiveFormScreen) Title() string {
	if s.po != nil && s.po.Number != "" {
		return fmt.Sprintf("Receive %s", s.po.Number)
	}
	return "Receive Items"
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

// declineFrozen is decline for a key the in-flight receipt has made inert. It
// says WHY rather than "does nothing", because the key does work — one second
// from now — and an operator watching a slow gateway is exactly the operator
// who will press it again.
func (s *ReceiveFormScreen) declineFrozen(key string, headerRows int) tea.Cmd {
	return s.say(key+" is frozen until the receipt answers · "+s.waysOut(headerRows), StatusWarn)
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
	// STATE THAT KEY WAS PRESSED IN, and headerLines pins it on every phase, so
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
	// has to remember, and this defect fails silently. There are exactly TWO
	// clearing points, this one and handleKey's, and between them they cover
	// every way the note's state can end: a reply off the wire and a keypress.
	// finishSerial and submit each carried a third until it was noticed they had
	// been dead since handleKey's was written — and finishSerial's was an arm
	// that moves the phase, which is the very thing this paragraph says the note
	// is not cleared in.
	//
	// The FAILURE line is deliberately not cleared with it: failHead/failDetail
	// are what came back off the wire rather than an answer to a keypress, and
	// they are the fact the operator most needs kept.
	switch msg.(type) {
	case receiveSubmittedMsg, serialUnitDoneMsg:
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
	// The header is measured BEFORE the clear, and every arm carries it, because
	// clearing the note SHRINKS the pinned header and so GROWS the body's row
	// budget. A key is pressed to honour the bar the operator was reading, and
	// that bar was drawn on a frame that had the note on it: asking "does the
	// body scroll?" after the clear answers about a taller window than the one
	// they were looking at. In the two-row band where the answers differ the bar
	// named PgUp/PgDn and the press then declined — the bar-honesty rule broken
	// by the fix for the stale note — and because the decline rewrote the same
	// sentence the second press redrew a byte-for-byte identical pane. It also
	// made the page STEP a lie: the cursor would move by the rows a note-free
	// window holds, past rows that were never drawn.
	//
	// So the frame is the argument (po_create.go's sourceHelpText takes
	// cartListed for the same reason), and the bar's claim and the guard behind
	// it read one expression — qtyPagesFor — bound to the same frame.
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
// exactly the rows that frame drew. Measuring either against the header the
// dispatch has just emptied answers about a different frame from the one the
// press was made against (handleKey carries the reasoning).
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
// (which enrolls a capture slot per received unit) and hasSerializedLine()
// (which promises phase 2 in the banner), and two different answers would be
// their own defect.
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
// about to draw are not always the same bar: the note is part of the pinned
// header, the header costs the body rows, and the body's height is what decides
// whether PgUp/PgDn are named at all. A press must be judged against the frame
// it was made ON — the frame whose bar the operator read — so handleKey
// measures that header before it retires the note and hands it down. Deriving
// the header inside here instead is exactly the drift this pair exists to make
// impossible, and it is the shape po_create.go's sourceHelpText uses for the
// same reason: the decision is passed IN rather than recomputed against a frame
// that has moved on.
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
// The HEADER is not monotone that way and so cannot be pinned to a constant in
// either direction — a note assumed present names a key that is dead on a
// note-free frame, and one assumed absent omits a key that works. It is a
// PARAMETER instead, and the caller binds it to the frame the question is
// really about: View to the frame it is drawing, and a key arm to the frame the
// press was made against (handleKey).
func (s *ReceiveFormScreen) qtyPagesFor(headerRows int) bool {
	return s.bodyScrollsForBar(s.qtyBody(), headerRows, s.qtyBarItems(true))
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
	header := s.headerLines()
	switch s.phase {
	case phaseSerial:
		return s.frameWrapped(header, s.serialBody(), 0, status, s.bar())
	case phaseDone:
		return s.frameWrapped(header, s.doneBody(), 0, status, s.bar())
	}
	return s.frameWrapped(header, s.qtyBody(), s.focused, status, s.bar())
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

// headerLines is the screen's answer to the last keypress, PINNED above the
// scrollable body on every frame.
//
// Pinned rather than appended, because the answer is the one line that must not
// be able to scroll away: inside the body it sat below a cursor the operator
// had walked down a long line list, and a key that answers off the pane has not
// answered. The diagnostic DETAIL rides with it, bounded, so a failure and its
// reason are one block rather than two a scroll can separate.
func (s *ReceiveFormScreen) headerLines() []string {
	lines := append(s.noteLines(), s.failDetailLines()...)
	if len(lines) == 0 {
		return nil
	}
	return append(lines, "")
}

// noteLines renders the screen's answer to the last keypress, folded to the
// pane the terminal really gave. Root.View TRUNCATES rather than wrapping, and
// the tail of these sentences is where the key that gets the operator OUT is
// named — a clipped hint is worse than none, because they believe they read it.
func (s *ReceiveFormScreen) noteLines() []string {
	lines := s.note.renderLines(s.paneWidth() - len(jdeIndent))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, jdeIndent+line)
	}
	return out
}

// receiveFailDetailRows caps the failure detail. The sentence naming what
// failed is on the status row above it and never gives; what a short terminal
// loses is the tail of the gateway's HTML.
const receiveFailDetailRows = 3

// failDetailLines draws the unbounded half of a failure under the headline the
// status row carries. It is CUT to what the pane can hold before it is folded —
// folding a multi-KB gateway page is work whose result is thrown away, on a
// block redrawn every keystroke (cellPrefix walks forward and stops when the
// budget is spent; truncateVisible would be O(n²) here).
func (s *ReceiveFormScreen) failDetailLines() []string {
	detail := s.failDetail
	if detail == "" {
		detail = s.serialErr
	}
	if detail == "" {
		return nil
	}
	width := s.paneWidth() - len(jdeIndent)
	if width < 12 {
		width = 12
	}
	trimmed := cellPrefix(detail, receiveFailDetailRows*width)
	out := make([]string, 0, receiveFailDetailRows)
	for i, line := range pickerWrap(trimmed, width) {
		if i >= receiveFailDetailRows {
			break
		}
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	return out
}

// ---------------------------------------------------------------------------
// The quantity form
// ---------------------------------------------------------------------------

// qtyBody is the scrollable body of phase 1: the standing caveats, then one
// block per receivable line, then the notes row.
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

	l.Add(StyleJDEHeading.Render("Receive into " + s.orderName()))
	for _, line := range s.standingCaveats(width) {
		l.Add(line)
	}
	l.Add("")

	if len(s.qty) == 0 {
		l.Add(jdeIndent + StyleMuted.Render("No receivable lines on this order."))
		for _, line := range jdeCaveatLines(
			"Every line is voided or already received in full, so there is nothing to book here.", width) {
			l.Add(line)
		}
		l.Add("")
	}
	for i := range s.qty {
		if i > 0 {
			// The separator travels with the block below it, so a window that
			// starts on this line still shows the whole block.
			l.AddRow(i, "")
		}
		s.addLineBlock(l, i, lw, width)
	}
	if len(s.qty) > 0 {
		l.Add("")
	}
	s.addField(l, len(s.qty), jdeField{
		Label:   "Notes",
		Kind:    jdeText,
		Input:   &s.notes,
		Width:   40,
		Hint:    "optional",
		Focused: s.caretOn(len(s.qty)),
	}, lw, width)
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

// standingCaveats are the facts about the ORDER that the per-line rows cannot
// carry on their own.
func (s *ReceiveFormScreen) standingCaveats(width int) []string {
	var out []string
	if s.hasKitLine() {
		// Wrapped rather than clipped, and WARN rather than muted: this is the
		// sentence that stops "received 2" being read as two of the thing named
		// on the line.
		room := 0
		if width > 0 {
			if room = width - len(jdeIndent); room < 1 {
				room = 1
			}
		}
		for _, line := range jdeWrapNote(receiveKitCaveat, room) {
			out = append(out, jdeIndent+StyleStatusWarn.Render(line))
		}
	}
	if s.hasSerializedLine() {
		out = append(out, jdeCaveatLines(
			"Serialized lines prompt for a serial per unit once the receipt posts.", width)...)
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
	s.addField(l, i, qty, lw, width)
	// The breakdown sits directly under the box it is a preview of, and
	// recomputes from what is currently typed there.
	for _, kl := range s.kitCreditLines(line, s.qty[i].Value()) {
		l.AddRow(i, kl)
	}
}

// addField renders one columnar row sized to the pane, and any hint that had to
// be folded under it as further lines of the SAME row — so the window keeps a
// field and the note explaining it on screen together (jdeLines.AddFittedFields
// for a band; this is the same thing for a row interleaved with others).
func (s *ReceiveFormScreen) addField(l *jdeLines, row int, f jdeField, lw, width int) {
	fitted, notes := jdeFitRow(f, lw, width)
	l.AddRow(row, renderJDEField(fitted, lw, width))
	for _, note := range notes {
		l.AddRow(row, note)
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
	return poKitCreditBlock(line.KitComponents, lead, receiveMetaIndent, s.bodyWidth(), kits)
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

// ---------------------------------------------------------------------------
// Serial capture
// ---------------------------------------------------------------------------

func (s *ReceiveFormScreen) serialBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	l.Add(StyleJDEHeading.Render("Capture serial numbers"))
	total := len(s.serialUnits)
	shown := s.serialCursor + 1
	if shown > total {
		shown = total
	}
	l.Add(jdeIndent + StyleMuted.Render(fmt.Sprintf(
		"unit %d of %d · created %d · skipped %d", shown, total, s.createdCount, s.skippedCount)))
	l.Add("")

	if s.serialCursor >= total {
		l.Add(jdeIndent + StyleMuted.Render("Every enrolled unit has been answered."))
		return l
	}

	unit := s.serialUnits[s.serialCursor]
	label := unit.label
	if width > 0 {
		if room := width - len(jdeIndent); room > 0 {
			label = fitCell(label, room)
		}
	}
	l.Add(jdeIndent + label)
	l.Add(receiveMetaIndent + StyleMuted.Render(
		fmt.Sprintf("unit %d of %d on this line", unit.unitNo, unit.unitTot)))
	s.addField(l, 0, jdeField{
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
	}, lw, width)
	return l
}

// ---------------------------------------------------------------------------
// The summary
// ---------------------------------------------------------------------------

// doneBody reports what the visit did, as columnar value rows. Rows that would
// report nothing are left out rather than drawn as zeroes: "Failed ..... 0" is
// a row that makes an operator look for a failure there was none of.
func (s *ReceiveFormScreen) doneBody() *jdeLines {
	l := &jdeLines{}
	lw := receiveLabelWidth()
	width := s.bodyWidth()

	l.Add(StyleJDEHeading.Render("Receive complete"))
	l.Add("")

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
		l.Add(renderJDEField(fields[i], lw, width))
	}
	return l
}
