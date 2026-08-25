// PurchaseOrderAddLineScreen — put a line on a draft purchase order by typing
// or scanning what is printed on the box.
//
// The captain's words: "I type in the SKU, it shows the item, gives me the
// ability to confirm the item from the supplier, and then it prompts me for the
// quantity and the price. From there, it adds the item in a new line."
//
// That is five phases and they are the whole screen (poAddPhase):
//
//	identify → looking → confirm  → price → adding → identify (added; scan again)
//	                   ↘ choose ↗
//
// A barcode scanner is a keyboard that types a burst and presses Enter, so the
// identify phase is one text row and Enter, and the whole flow is reachable
// without ever leaving the keyboard. Ten scans in a row is the intended shape:
// a successful add lands back on the identify phase with the box cleared and a
// running tally of what this visit put on the order.
//
// # The resolution logic is the SERVER's, all of it
//
// internal/omsapi/po_line_entry.go carries the API note; the rule this file
// lives by is that NOTHING here decides what an identifier means. The match
// ladder, whether an answer is obvious enough to skip the choice
// (POLineLookup.Resolves), the ambiguity set, and every refusal — supplier does
// not carry it, order is not a draft, the vendor discontinued it — come off the
// wire. This screen decides only what an operator SEES and which key does what.
//
// Two consequences worth keeping in mind before editing:
//
//   - An exact identifier does NOT go through the choose phase, and that is not
//     a shortcut this screen took. `Resolves` is the server saying the strongest
//     tier that matched holds exactly one candidate, so a scanned barcode still
//     resolves in one round trip even when a partial name match came back
//     alongside it. Recomputing it here from len(Candidates) would disagree with
//     the server on the ordinary case.
//
//   - The ADD posts `item_supplier` — the exact catalogue row the operator
//     confirmed — and never re-posts the identifier. Re-posting would re-resolve
//     it, and the catalogue can change between the lookup and the confirm: the
//     operator would have approved one item and added another. Every guard still
//     runs server-side against the row id.
//
// # The prompt's defaults ARE the server's defaults
//
// Quantity and unit cost are optional on the add, and omitting them makes the
// server derive both — the item's reorder maths rounded to a whole supplier
// package, and the supplier relationship's price falling back to what this item
// last actually cost from this supplier. The lookup reports those same two
// numbers per candidate, so the price phase PREFILLS them and an operator who
// just presses Enter gets exactly what a bare scan would have produced.
//
// Blank therefore means "let the server decide" on both rows, and the hints say
// so. That is not a hole: for a fresh line the server's answer IS the number
// that was prefilled, and for a REPEAT add it is the one thing this screen must
// not guess at — see below.
//
// # Adding something the order already carries
//
// `(purchase_order, item_supplier)` is unique, so a second line for the same
// item is impossible: a repeat add GROWS the line already there, by one
// supplier package, and LEAVES ITS PRICE ALONE unless a price is sent. So the
// price row on a repeat starts EMPTY rather than prefilled with the fresh-line
// suggestion — prefilling it and posting it would silently reprice a line the
// operator only meant to add one more box to, which is the "never silently
// overwrite" rule with money on it. The row's hint names the price the line
// carries today (read off the order in hand) so blank is an informed choice.
//
// # Every state answers every key
//
// The rules the purchasing screens are held to (AGENTS.md) apply here in full:
// the action bar names exactly the keys that act in the state being drawn; a
// key it does not name declines IN THE BODY with a sentence that NAMES the key,
// so two dead presses never redraw the same pane; a failure shows the server's
// own sentence, never a raw dump; and every bound is measured in display cells
// against the pane the terminal actually gave, not against a hard-coded 51.
//
// po_add_line_test.go drives the flow through Root.Update against a stateful
// httptest fake; po_add_line_sweep_test.go presses the whole key space at every
// phase, with the phase list derived from the iota and the state fingerprint
// derived from the struct.
package tui

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// poAddPhase is where the operator is in the flow. The iota is walked to
// poAddPhaseCount by the sweep, so a phase added here is pressed by the key
// space the day it exists rather than the day somebody remembers it.
type poAddPhase int

const (
	// poAddPhaseIdentify is the typed/scanned identifier row — the phase a scan
	// starts on and the one a successful add returns to.
	poAddPhaseIdentify poAddPhase = iota
	// poAddPhaseLooking is the lookup in flight.
	poAddPhaseLooking
	// poAddPhaseChoose is genuine ambiguity: the server could not narrow it to
	// one, so the operator picks from the candidates it sent.
	poAddPhaseChoose
	// poAddPhaseConfirm shows the resolved item with enough identity to say yes.
	poAddPhaseConfirm
	// poAddPhasePrice prompts for quantity and unit cost, prefilled.
	poAddPhasePrice
	// poAddPhaseAdding is the POST in flight. Frozen: what was confirmed has
	// already gone, so nothing may change under it.
	poAddPhaseAdding
	poAddPhaseCount
)

func (p poAddPhase) String() string {
	switch p {
	case poAddPhaseIdentify:
		return "identify"
	case poAddPhaseLooking:
		return "looking"
	case poAddPhaseChoose:
		return "choose"
	case poAddPhaseConfirm:
		return "confirm"
	case poAddPhasePrice:
		return "price"
	case poAddPhaseAdding:
		return "adding"
	}
	return fmt.Sprintf("poAddPhase(%d)", int(p))
}

// The two rows of the price phase, in the order the cursor walks them.
const (
	poAddFieldQty = iota
	poAddFieldCost
	poAddFieldCount
)

// poAddLabels is the screen's label column, in ONE place so every phase hangs
// off the same leader — the columnar rule that a value never moves sideways
// when the frame changes under it.
var poAddLabels = []string{
	"Supplier", "Order", "Scan / type",
	"Item", "Item SKU", "Supplier SKU", "Matched", "Pack", "On order",
	"Quantity", "Unit cost", "Line total",
}

func poAddLabelWidth() int {
	fields := make([]jdeField, len(poAddLabels))
	for i, l := range poAddLabels {
		fields[i] = jdeField{Label: l}
	}
	return jdeLabelWidth(fields)
}

// poAddedLine is one thing this visit put on the order, for the tally the
// identify phase draws. It records what the SERVER did rather than what was
// asked for: `created` false means an existing line was grown, which is a
// different sentence, and `after` is where that left it.
type poAddedLine struct {
	name     string
	quantity int
	unitCost string
	created  bool
	after    int
	// deltaKnown is whether `quantity` is a figure this screen could actually
	// derive. A grown line's delta is the reply's ordered quantity less the
	// before-figure the LOOKUP reported, so when the server grows a line the
	// lookup never saw — another terminal adding one in between — there is no
	// before-figure and the delta is unknowable rather than zero. Found-nothing
	// and could-not-tell are different facts here as everywhere else on this
	// screen, and the wording follows this flag rather than inventing a number.
	deltaKnown bool
}

// poAddLookupMsg is one item-lookup reply. `seq` is the stamp the request
// carried: a reply for a query the operator has already moved off is DROPPED
// rather than painted over the one they are looking at (the same guard
// ListScreen.searchSeq and the New PO asset picker's assetsSeq apply).
type poAddLookupMsg struct {
	seq   int
	query string
	res   *omsapi.POLineLookup
	err   error
}

// poAddLineMsg is the add's reply, stamped the same way.
type poAddLineMsg struct {
	seq int
	res *omsapi.POLineAdded
	err error
}

type PurchaseOrderAddLineScreen struct {
	deps Deps
	jdeScreen

	// poID / poNumber / supplier identify the order this screen is adding to.
	// They are captured at construction from the order the detail screen already
	// loaded, so the first frame names the supplier without a round trip.
	poID     string
	poNumber string
	supplier string
	// po is the order as last seen — the detail screen's copy at construction,
	// replaced by the FULL refreshed order every add returns. It is read only to
	// name the price a line already carries, which is what makes "blank keeps
	// the line's price" an informed choice rather than a shrug.
	po *omsapi.PurchaseOrder

	phase poAddPhase

	// idIn is the identifier row: what the operator typed or the scanner burst.
	idIn textinput.Model

	// lookup is the last answer, and lookupSeq stamps the request in flight.
	lookup    *omsapi.POLineLookup
	lookupSeq int

	// cursor walks the choose phase's candidate list.
	cursor int
	// chosen is the candidate being confirmed and priced.
	chosen *omsapi.POLineCandidate
	// chosenFrom is the phase Esc returns to from the confirm frame — the choose
	// list when the operator picked out of one, the identifier row when the
	// server resolved it outright.
	chosenFrom poAddPhase
	// scroll is the confirm frame's read-only offset. That frame carries the
	// provenance and caveat prose, which can outrun a 15-row body at 80x24, so
	// it scrolls rather than losing its tail.
	scroll int

	qtyIn, costIn textinput.Model
	// priceFocus is which of the two price rows holds the caret.
	priceFocus int
	// priceEdited records that the operator has typed into either price row, so
	// a back-step that drops what they entered can say it did.
	priceEdited bool

	// pending is the add POST in flight, and addSeq stamps it.
	pending bool
	addSeq  int

	// failHead / failDetail are the failure line: a headline and the server's
	// own (unbounded) detail, always written together by setFail so a stale
	// detail can never be drawn under a fresh headline.
	failHead   string
	failDetail string

	// note is the screen's answer to the last keypress, drawn in the BODY. The
	// status bar carries the same words, but a flash expires after four seconds
	// and the operator who pressed a key and saw nothing is still looking.
	note pickerNote

	// added is what this visit put on the order, newest last.
	added []poAddedLine
}

// NewPurchaseOrderAddLineScreen opens the flow against an order the caller has
// already loaded. The order is taken by value-in-hand rather than by id so the
// first frame can name the supplier the lookup will be scoped to; the screen
// still asks the server for everything it decides.
func NewPurchaseOrderAddLineScreen(deps Deps, po *omsapi.PurchaseOrder) *PurchaseOrderAddLineScreen {
	id := textinput.New()
	id.Prompt = ""
	id.CharLimit = 128
	id.Focus()

	s := &PurchaseOrderAddLineScreen{deps: deps, po: po, idIn: id, phase: poAddPhaseIdentify}
	if po != nil {
		s.poID = fmt.Sprintf("%v", po.ID)
		s.poNumber = po.Number
		s.supplier = po.SupplierDetails
	}
	return s
}

func (s *PurchaseOrderAddLineScreen) Title() string {
	if s.poNumber != "" {
		return fmt.Sprintf("Add line — %s", s.poNumber)
	}
	return "Add line"
}

func (s *PurchaseOrderAddLineScreen) Init() tea.Cmd { return textinput.Blink }

// WantsRawInput routes every key here. The identify and price phases hold a
// focused textinput, and the rest of the flow owns its own Esc: leaving is a
// step of THIS screen (back one phase, or out to the order), not the root's
// back-stack pop.
func (s *PurchaseOrderAddLineScreen) WantsRawInput() bool { return true }

// paneWidth is the columns the body really has, falling back to the width this
// project checks against when the terminal has not sized us yet.
//
// It is the live width and not a constant on purpose: clipping a row to 51
// cells on a 120-column terminal throws away forty columns the pane had room
// for, on the rows an operator reads to decide what they are buying.
func (s *PurchaseOrderAddLineScreen) paneWidth() int {
	if w := s.bodyWidth(); w > 0 {
		return w
	}
	return screenBodyWidth(80)
}

// ---------------------------------------------------------------------------
// Saying things
// ---------------------------------------------------------------------------

// say records the note and flashes the same words. BOTH: the bar is where an
// operator's eye goes for "did that work", and the body line is what is still
// there once the flash has gone.
func (s *PurchaseOrderAddLineScreen) say(text string, level StatusLevel) tea.Cmd {
	return s.note.say(text, level)
}

// decline answers a key that does not act in the state being drawn. The lead
// NAMES the key, which is not decoration: two keys sharing one sentence would
// let the second press redraw the pane the first one left, and on a frame with
// no cursor and no caret that is indistinguishable from a wedged program.
func (s *PurchaseOrderAddLineScreen) decline(key string) tea.Cmd {
	return s.say(key+" does nothing here · "+s.waysOut(), StatusWarn)
}

// waysOut names the keys that DO act, read off the bar the frame is drawing so
// a decline cannot advertise a key this phase does not honour.
func (s *PurchaseOrderAddLineScreen) waysOut() string {
	items := s.bar()
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
// headline written on its own left the previous failure's detail standing under
// it, which read as the gateway explaining a validation message.
func (s *PurchaseOrderAddLineScreen) setFail(head, detail string) {
	s.failHead, s.failDetail = head, detail
}

func (s *PurchaseOrderAddLineScreen) clearFail() { s.setFail("", "") }

// poAddFailure turns whatever came back off the wire into a HEADLINE for the
// one-line status row and a DETAIL the body folds under the note.
//
// The split matters because the status row cannot fold: it is one row of the
// frame and jdeScreen.fitStatus clips it. So the row gets a short fixed
// headline and the unbounded half goes in the body, where it is folded and
// bounded — never the other way round, which is how the server's reason came to
// be shown as "✗ Acme Fasteners no longer supplies Widget clamp (…".
//
// A REFUSAL is not routed through here at all: it carries the server's own
// operator-facing sentence, which belongs in the note rather than in a
// diagnostic tail. See handleAdded.
//
// The detail is never trusted to be short — omsapi.parseError puts the ENTIRE
// raw response body in APIError.Message whenever the JSON envelope carries no
// code, so a gateway's HTML page arrives here whole and is bounded where it is
// drawn.
func poAddFailure(what string, err error) (head, detail string) {
	if err == nil {
		return "", ""
	}
	var api *omsapi.APIError
	if errors.As(err, &api) && api.Code != "" && api.Message != "" {
		return what + " failed", api.Message
	}
	return what + " failed", err.Error()
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *PurchaseOrderAddLineScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case poAddLookupMsg:
		return s, s.handleLookup(m)
	case poAddLineMsg:
		return s, s.handleAdded(m)
	case tea.KeyMsg:
		return s.handleKey(m)
	}
	return s, nil
}

func (s *PurchaseOrderAddLineScreen) handleKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch s.phase {
	case poAddPhaseIdentify:
		return s.keyIdentify(m)
	case poAddPhaseLooking:
		return s.keyLooking(m)
	case poAddPhaseChoose:
		return s.keyChoose(m)
	case poAddPhaseConfirm:
		return s.keyConfirm(m)
	case poAddPhasePrice:
		return s.keyPrice(m)
	case poAddPhaseAdding:
		return s.keyAdding(m)
	}
	return s, nil
}

// leave returns to the purchase order, which reloads and shows the lines this
// visit added.
func (s *PurchaseOrderAddLineScreen) leave() tea.Cmd {
	return SwitchTo(WSPurchasing, NewPurchaseOrderDetailScreen(s.deps, s.poID))
}

func (s *PurchaseOrderAddLineScreen) keyIdentify(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.leave()
	case "enter":
		query := strings.TrimSpace(s.idIn.Value())
		if query == "" {
			return s, s.say("enter looked up nothing — type or scan an identifier first · "+
				"a name, an item SKU, a barcode or "+s.supplierName()+"'s own SKU", StatusWarn)
		}
		s.clearFail()
		s.note.clear()
		s.phase = poAddPhaseLooking
		s.lookupSeq++
		// No flash goes out with the request. The frame's own status ROW draws
		// the working line (jdeScreen.statusRow, naming the work and the
		// subject) for exactly as long as the lookup is out, and it is
		// state-driven — where a flash sent alongside the request races the
		// flash the REPLY sends and can land after it, leaving "looking up…" on
		// the bar over a frame that has already answered.
		return s, s.runLookup(query, s.lookupSeq)
	}
	var cmd tea.Cmd
	s.idIn, cmd = s.idIn.Update(m)
	return s, cmd
}

func (s *PurchaseOrderAddLineScreen) keyLooking(m tea.KeyMsg) (Screen, tea.Cmd) {
	if m.String() == "esc" {
		// The reply is not cancelled — nothing here can cancel an HTTP request
		// in flight — but it IS dropped: lookupSeq has moved on, so it can never
		// paint over the frame the operator went back to.
		s.lookupSeq++
		s.phase = poAddPhaseIdentify
		return s, s.say("esc stopped waiting · the identifier is still in the box · "+
			"enter looks it up again", StatusInfo)
	}
	return s, s.decline(m.String())
}

func (s *PurchaseOrderAddLineScreen) keyChoose(m tea.KeyMsg) (Screen, tea.Cmd) {
	rows := len(s.candidates())
	switch m.String() {
	case "esc":
		s.phase = poAddPhaseIdentify
		return s, s.say("esc left the matches · the identifier is still in the box · "+
			"narrow it and press enter to look again", StatusInfo)
	case "enter":
		list := s.candidates()
		if s.cursor < 0 || s.cursor >= len(list) {
			return s, s.say("enter chose nothing — no candidate is highlighted", StatusWarn)
		}
		return s, s.chooseCandidate(list[s.cursor], poAddPhaseChoose)
	case "up", "down":
		next := s.cursor + 1
		if m.String() == "up" {
			next = s.cursor - 1
		}
		if next < 0 || next >= rows {
			// Both edges stay silent on purpose: the highlight is on the pane and
			// visibly at the end, so the press has already answered itself.
			return s, nil
		}
		s.cursor = next
		return s, nil
	case "pgup", "pgdown":
		if !s.choosePages() {
			return s, s.decline(m.String())
		}
		step := s.chooseStep()
		dir := +1
		if m.String() == "pgup" {
			dir = -1
		}
		s.cursor = jdePageCursor(s.cursor, rows, step, dir)
		return s, nil
	}
	return s, s.decline(m.String())
}

func (s *PurchaseOrderAddLineScreen) keyConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		back := s.chosenFrom
		dropped := s.priceEdited
		s.chosen = nil
		s.priceEdited = false
		s.phase = back
		// Rule: never drop what the operator typed without saying so. Backing out
		// of a confirmed item throws away the quantity and price they had entered
		// for it, and only they know whether that mattered.
		if dropped {
			return s, s.say("esc went back and dropped the quantity and price you had typed", StatusWarn)
		}
		return s, s.say("esc went back · the item is not on the order", StatusInfo)
	case "enter":
		s.phase = poAddPhasePrice
		s.priceFocus = poAddFieldQty
		s.qtyIn.Focus()
		s.costIn.Blur()
		return s, tea.Batch(textinput.Blink, s.say(s.priceEntryNote(), StatusInfo))
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.confirmScrolls() {
			return s, s.decline(m.String())
		}
		body := s.confirmBody()
		step := s.scrollRows(len(s.headerLines()), s.bar())
		switch m.String() {
		case "up":
			s.scroll--
		case "down":
			s.scroll++
		case "pgup":
			s.scroll -= step
		case "pgdown":
			s.scroll += step
		case "home":
			s.scroll = 0
		case "end":
			s.scroll = body.Len()
		}
		if s.scroll < 0 {
			s.scroll = 0
		}
		return s, nil
	}
	return s, s.decline(m.String())
}

func (s *PurchaseOrderAddLineScreen) keyPrice(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = poAddPhaseConfirm
		s.qtyIn.Blur()
		s.costIn.Blur()
		// What was typed is KEPT: coming back to this phase from the confirm
		// frame finds it exactly as it was left, so a back-step to re-read the
		// item costs nothing.
		return s, s.say("esc went back to the item · what you typed is still here", StatusInfo)
	case "up", "down":
		step := 1
		if m.String() == "up" {
			step = poAddFieldCount - 1
		}
		s.priceFocus = (s.priceFocus + step) % poAddFieldCount
		s.focusPriceRow()
		return s, textinput.Blink
	case "enter":
		return s, s.submit()
	}
	// Anything else belongs to the focused box. priceEdited is set only when the
	// VALUE really moved, not on every key the box saw: it is what makes the
	// back-step say "dropped the quantity and price you had typed", and a caret
	// that merely moved is not typing anybody would mind losing.
	var cmd tea.Cmd
	before := s.qtyIn.Value() + "\x00" + s.costIn.Value()
	if s.priceFocus == poAddFieldQty {
		s.qtyIn, cmd = s.qtyIn.Update(m)
	} else {
		s.costIn, cmd = s.costIn.Update(m)
	}
	if s.qtyIn.Value()+"\x00"+s.costIn.Value() != before {
		s.priceEdited = true
	}
	return s, cmd
}

// keyAdding is the freeze. The POST carries a quantity, a price and a catalogue
// row that have already gone, so nothing on this screen may change under it —
// and the bar names the one key that still acts.
//
// Esc is deliberately NOT gated: a frame with no way out while a slow gateway
// thinks is the worse defect. Leaving does not cancel the request, so the frame
// says so rather than implying the line was abandoned.
func (s *PurchaseOrderAddLineScreen) keyAdding(m tea.KeyMsg) (Screen, tea.Cmd) {
	if m.String() == "esc" {
		return s, s.leave()
	}
	return s, s.say(m.String()+" is frozen until the add answers — "+
		s.addingSentence(poAddNoteNameCells)+" has already gone to "+s.orderName()+" · "+
		"esc goes back to the order, but the line may still be added", StatusWarn)
}

// focusPriceRow puts the caret on the row priceFocus names and nowhere else.
func (s *PurchaseOrderAddLineScreen) focusPriceRow() {
	if s.priceFocus == poAddFieldQty {
		s.qtyIn.Focus()
		s.costIn.Blur()
		return
	}
	s.costIn.Focus()
	s.qtyIn.Blur()
}

// ---------------------------------------------------------------------------
// Talking to OMS
// ---------------------------------------------------------------------------

func (s *PurchaseOrderAddLineScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PurchaseOrderAddLineScreen) runLookup(query string, seq int) tea.Cmd {
	deps, ctx, id := s.deps, s.ctx(), s.poID
	return func() tea.Msg {
		res, err := deps.OMS.LookupPurchaseOrderLine(ctx, id, query)
		return poAddLookupMsg{seq: seq, query: query, res: res, err: err}
	}
}

// handleLookup turns one lookup reply into the next phase.
//
// The four outcomes are kept APART because an operator acts differently on each
// and only one of them is "no such thing":
//
//	err            — could not tell. The catalogue was never asked.
//	!CanAddItems   — the order stopped being a draft under us; the server said so.
//	no candidates  — found nothing this supplier can put on this order, and
//	                 `unavailable` may explain items it really did name.
//	Resolves       — one obvious answer: straight to the confirm frame.
//	otherwise      — genuine ambiguity: the operator picks.
func (s *PurchaseOrderAddLineScreen) handleLookup(m poAddLookupMsg) tea.Cmd {
	if m.seq != s.lookupSeq || s.phase != poAddPhaseLooking {
		// A reply for a query the operator has moved off. Dropping it is the
		// whole point of the stamp: painting it would answer a question nobody
		// is asking any more, over the frame they are reading.
		return nil
	}
	if m.err != nil {
		s.phase = poAddPhaseIdentify
		head, detail := poAddFailure("looking up "+pickerClip(m.query, 20), m.err)
		s.setFail(head, detail)
		return s.say("could not tell whether "+s.supplierName()+" supplies that — "+
			"the lookup did not answer · enter tries again · esc goes back to the order", StatusError)
	}

	s.lookup = m.res
	s.clearFail()

	if !m.res.PurchaseOrder.CanAddItems {
		s.phase = poAddPhaseIdentify
		return s.say(s.orderName()+" is "+m.res.PurchaseOrder.Status+
			" · lines go on an order only while it is a draft · esc goes back to it", StatusWarn)
	}

	if len(m.res.Candidates) == 0 {
		s.phase = poAddPhaseIdentify
		return s.say(s.noMatchSentence(m.query, m.res), StatusWarn)
	}

	if m.res.Resolves {
		return s.chooseCandidate(m.res.Candidates[0], poAddPhaseIdentify)
	}

	s.phase = poAddPhaseChoose
	s.cursor = 0
	return s.say(s.ambiguitySentence(m.query, m.res), StatusInfo)
}

// noMatchSentence says which of the two "nothing to add" facts this is.
//
// `unavailable` is the server having FOUND the item and refusing it for this
// order — the wrong vendor's box, or a line this vendor stopped carrying — and
// its own message names the item and the supplier. An empty `unavailable` is
// the other fact: nothing matched at all. Collapsing the two would tell an
// operator holding the right part in their hand that it does not exist.
func (s *PurchaseOrderAddLineScreen) noMatchSentence(query string, res *omsapi.POLineLookup) string {
	q := pickerClip(query, 24)
	if len(res.Unavailable) == 0 {
		return fmt.Sprintf("nothing matching %q is supplied by %s · "+
			"edit the box and press enter to look again · esc goes back to the order",
			q, s.supplierName())
	}
	lead := res.Unavailable[0].Message
	if res.TotalUnavailable > 1 {
		lead = fmt.Sprintf("%q matches %d items this order cannot carry · %s",
			q, res.TotalUnavailable, res.Unavailable[0].Message)
	}
	return pickerClip(lead, poAddServerSentenceCells) +
		" · enter looks again · esc goes back to the order"
}

// ambiguitySentence reports the choice set honestly, including the part of it
// that was capped away server-side. Being told "20" when 63 matched sends an
// operator hunting for an item that was never in the list.
//
// Every number in it comes from the CANDIDATE LIST, which is what chooseBody
// draws, and that is the whole rule: a count in this sentence is a promise
// about rows the operator can count on screen. The lead used to be
// BestMatchTotal — the size of the STRONGEST tier — while the list carries
// every tier the server sent, so a partial-name search that also turned up
// weaker matches read "matches 2 items · choose one" over five rows, and the
// capped-list clause below it then quoted a total from the other source
// entirely: one sentence, two quantities, presented as one.
//
// The weaker tiers are not dropped to make the old number true. The lookup
// endpoint returns the full set precisely so a client can show the whole
// picture, and hiding the partial matches would remove exactly what an operator
// typing part of a name is looking for.
//
// The number behind "matches" is the number of MATCHES — TotalCandidates — and
// the count of rows on screen is a separate claim in the clause after it. Both
// have been wrong in turn: first the lead quoted the strongest TIER while the
// list drew every tier, then it quoted the SHOWN count, which made one sentence
// say "matches 20 items · … · 20 of 63 shown" — two different claims about the
// same figure. Sixty-three matched; twenty are on the pane; each sentence says
// exactly one of those things.
//
// The wording is OMS's own, from resolve_identifier. An operator moving between
// the web app and the terminal must not have to reconcile two vocabularies for
// one fact, so this sentence follows the server's rather than inventing a
// second — and with nothing capped it degenerates to the plain sentence with no
// special case to keep in step.
func (s *PurchaseOrderAddLineScreen) ambiguitySentence(query string, res *omsapi.POLineLookup) string {
	sentence := fmt.Sprintf("%q matches %d items %s supplies · choose one",
		pickerClip(query, 20), res.TotalCandidates, s.supplierName())
	if shown := len(res.Candidates); res.Truncated || shown < res.TotalCandidates {
		sentence += fmt.Sprintf(" · the first %d are offered here, narrow the search to see the rest",
			shown)
	}
	return sentence
}

// chooseCandidate moves onto the confirm frame with `c` staged, remembering
// which phase Esc should return to.
func (s *PurchaseOrderAddLineScreen) chooseCandidate(c omsapi.POLineCandidate, from poAddPhase) tea.Cmd {
	staged := c
	s.chosen = &staged
	s.chosenFrom = from
	s.phase = poAddPhaseConfirm
	s.scroll = 0
	s.priceEdited = false
	s.primePriceRows(staged)
	return s.say(s.confirmEntryNote(staged, from), StatusInfo)
}

// primePriceRows fills the quantity and price prompts with the defaults the
// SERVER would apply, so accepting them with one Enter reproduces exactly what
// a bare scan would have produced.
//
// The repeat case is the one that differs, and it differs because the server
// differs: a repeat add grows the existing line by one supplier package and
// leaves its price alone. So the price row starts EMPTY there — blank means
// "the server decides", and for a repeat the server's decision is "keep the
// price the line already carries".
func (s *PurchaseOrderAddLineScreen) primePriceRows(c omsapi.POLineCandidate) {
	qty := textinput.New()
	qty.Prompt = ""
	qty.CharLimit = 9
	cost := textinput.New()
	cost.Prompt = ""
	cost.CharLimit = 14

	if repeat := c.AlreadyOnOrder; repeat != nil && repeat.RepeatIncrement != nil {
		qty.SetValue(strconv.Itoa(*repeat.RepeatIncrement))
	} else {
		qty.SetValue(strconv.Itoa(c.SuggestedQuantity))
		cost.SetValue(c.SuggestedUnitCost.String())
	}
	s.qtyIn, s.costIn = qty, cost
	s.priceFocus = poAddFieldQty
}

// readQuantityRow and readCostRow are the ONE place each typed row is judged.
//
// They exist because the answer is needed twice — the submit refuses on it, and
// the Line total row must not draw a figure for an entry Enter is about to
// reject — and a rule written out twice drifts. It already had: the submit
// wanted a whole number and the total row was gated on a decimal predicate, so
// a quantity of "5.5" drew a total and was then refused, and "+5" was posted
// with the total hidden. One reader per row makes the disagreement
// unrepresentable rather than merely fixed.
//
// Each returns the value to SEND — the zero value meaning "the row is blank, so
// the server decides" — and an operator-facing refusal, empty when the row is
// acceptable. The refusal is the sentence's middle: the caller supplies the
// lead naming what the key did.
func (s *PurchaseOrderAddLineScreen) readQuantityRow() (int, string) {
	raw := strings.TrimSpace(s.qtyIn.Value())
	if raw == "" {
		return 0, ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Sprintf("quantity %q is not a whole number of 1 or more · "+
			"clear the row to take %s's own suggestion", pickerClip(raw, 12), s.supplierName())
	}
	return n, ""
}

// readCostRow judges the price row as MONEY, which big.Rat.SetString does not:
// it is a number parser and accepts "1/3", "1e9" and "-5", and this screen
// posts the row verbatim as unit_cost. A mis-keyed leading minus therefore
// either created a negative-priced line or came back as a DRF validation
// envelope — which omsapi.AsLineEntryError deliberately declines to recognise,
// so the operator read "the add did not answer — the line may or may not be on
// the order" with the raw JSON folded underneath, about a request that
// definitively answered and definitively added nothing.
//
// The scan lives INSIDE this reader rather than beside it as a predicate any
// caller could reach for, because a second, looser judge of the same row is
// exactly what this pair of functions exists to make impossible.
//
// This is FIELD PARSING of a local textbox and nothing more. Whether the
// supplier carries the item, whether the order is still a draft and what a
// price is allowed to be are the SERVER's to refuse, and none of them is
// duplicated here.
func (s *PurchaseOrderAddLineScreen) readCostRow() (string, string) {
	raw := strings.TrimSpace(s.costIn.Value())
	if raw == "" {
		return "", ""
	}
	plainDecimal := func(v string) bool {
		digits, dots := 0, 0
		for _, r := range v {
			switch {
			case r >= '0' && r <= '9':
				digits++
			case r == '.':
				if dots++; dots > 1 {
					return false
				}
			default:
				return false
			}
		}
		return digits > 0
	}
	if !plainDecimal(raw) {
		return "", fmt.Sprintf("unit cost %q is not a plain price like 4.50 · "+
			"clear the row to take the price on file", pickerClip(raw, 12))
	}
	return raw, ""
}

// submit posts the confirmed row. Quantity and cost are sent only when the box
// holds something: an empty box means "let the server derive it", which for a
// fresh line is the same number that was prefilled and for a repeat is the
// price the line already carries.
func (s *PurchaseOrderAddLineScreen) submit() tea.Cmd {
	if s.chosen == nil {
		return s.say("enter added nothing — no item is staged", StatusWarn)
	}
	req := omsapi.POLineAdd{ItemSupplier: s.chosen.ItemSupplier}

	qty, refusal := s.readQuantityRow()
	if refusal != "" {
		return s.say("enter did not add it — "+refusal, StatusWarn)
	}
	req.Quantity = qty

	cost, refusal := s.readCostRow()
	if refusal != "" {
		return s.say("enter did not add it — "+refusal, StatusWarn)
	}
	req.UnitCost = cost

	s.pending = true
	s.phase = poAddPhaseAdding
	s.qtyIn.Blur()
	s.costIn.Blur()
	s.clearFail()
	s.addSeq++
	seq := s.addSeq
	deps, ctx, id := s.deps, s.ctx(), s.poID
	// As with the lookup: the working line belongs to the frame's status row,
	// which cannot be overtaken by the reply's own flash.
	return func() tea.Msg {
		res, err := deps.OMS.AddPurchaseOrderLine(ctx, id, req)
		return poAddLineMsg{seq: seq, res: res, err: err}
	}
}

// handleAdded lands the add's reply.
//
// A refusal hands the operator back the price frame with everything they typed
// still in it — the server's answer is a reason to change something, not a
// reason to lose the entry — and shows the server's own sentence.
func (s *PurchaseOrderAddLineScreen) handleAdded(m poAddLineMsg) tea.Cmd {
	if m.seq != s.addSeq {
		return nil
	}
	s.pending = false
	if m.err != nil {
		s.phase = poAddPhasePrice
		s.focusPriceRow()
		// A refusal IS an operator-facing sentence the server composed — "Acme
		// Fasteners no longer supplies Widget bracket (marked discontinued)." —
		// so it goes in the NOTE, which folds to the pane, rather than into the
		// diagnostic detail or onto the one row that cannot fold.
		if entry, ok := omsapi.AsLineEntryError(m.err); ok {
			s.setFail("the order refused this line", "")
			return s.say(pickerClip(entry.Message, poAddServerSentenceCells)+
				" · enter tries again · esc goes back to the item", StatusError)
		}
		head, detail := poAddFailure("adding the line", m.err)
		s.setFail(head, detail)
		return s.say("the add did not answer — the line may or may not be on the order · "+
			"esc goes back to the order to check · enter tries again", StatusError)
	}

	s.clearFail()
	line := poAddedLine{name: s.chosenName(), created: m.res.Created, deltaKnown: true}
	line.unitCost = m.res.LineItem.UnitCostOrdered.String()
	line.after = m.res.LineItem.QuantityOrdered
	// A fresh line's ordered quantity IS what was added; a grown one's is where
	// it ended up, so the two numbers are recorded separately.
	line.quantity = m.res.LineItem.QuantityOrdered
	if !m.res.Created {
		if s.chosen != nil && s.chosen.AlreadyOnOrder != nil {
			line.quantity = m.res.LineItem.QuantityOrdered - s.chosen.AlreadyOnOrder.QuantityOrdered
		} else {
			// The server grew a line the LOOKUP had not reported — another
			// terminal put one there in the window (purchase_order,
			// item_supplier) uniqueness leaves open. We know where the line
			// ended up and we do not know what it grew BY, and subtracting a
			// before-figure we never had would have drawn "grew that line by 12
			// to 12": a number nobody measured, reported as fact.
			line.quantity, line.deltaKnown = 0, false
		}
	}
	s.added = append(s.added, line)
	if m.res.PurchaseOrder != nil {
		s.po = m.res.PurchaseOrder
	}

	s.chosen = nil
	s.lookup = nil
	s.priceEdited = false
	s.phase = poAddPhaseIdentify
	s.idIn.SetValue("")
	s.idIn.Focus()
	return tea.Batch(textinput.Blink, s.say(poAddedSentence(line)+" · scan the next, or esc for the order", StatusOK))
}

// poAddedSentence is what the server actually did, in one line.
//
// The item NAME is a bounded identifier here as it is everywhere else on this
// screen: an OMS catalogue name runs to sixty cells and this sentence is drawn
// with the running tally underneath it, which is the block a long name pushed
// off an 80x24 pane entirely.
func poAddedSentence(l poAddedLine) string {
	name := pickerClip(l.name, poAddNoteNameCells)
	if l.created {
		return fmt.Sprintf("added %d × %s at %s as a new line", l.quantity, name, poAddMoney(l.unitCost))
	}
	if !l.deltaKnown {
		return fmt.Sprintf("%s was already on this order — that line now stands at %d", name, l.after)
	}
	return fmt.Sprintf("%s was already here — grew that line by %d to %d", name, l.quantity, l.after)
}

// poAddNoteNameCells bounds an item name pasted into one of this screen's own
// sentences. The sentence's own words are what name the next key, so the part
// that gives is the identifier.
const poAddNoteNameCells = 28

// ---------------------------------------------------------------------------
// The action bar
// ---------------------------------------------------------------------------

// bar names exactly the keys that act in the phase being drawn, and only those.
// Every decline reads its way-out sentence off this, so the two cannot drift.
func (s *PurchaseOrderAddLineScreen) bar() []actionBarItem {
	switch s.phase {
	case poAddPhaseIdentify:
		// The Esc label says what leaving COSTS, because leaving destroys the
		// screen and with it whatever is in the box. "If input is about to be
		// dropped, say so first" is a promise a bar can keep in one word, and
		// after the fact is too late here — there is no frame left to say it on.
		back := "Back to order"
		if strings.TrimSpace(s.idIn.Value()) != "" {
			back = "Discard & back"
		}
		return []actionBarItem{{"Enter", "Look up"}, {"Esc", back}}
	case poAddPhaseLooking:
		return []actionBarItem{{"Esc", "Stop waiting"}}
	case poAddPhaseChoose:
		items := []actionBarItem{{"Enter", "Choose"}, {"Esc", "Back"}, {"UP/DN", "Move"}}
		if s.choosePages() {
			items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
		}
		return items
	case poAddPhaseConfirm:
		// Same promise one phase on: backing out of a confirmed item throws away
		// the quantity and price entered for it, and only the operator knows
		// whether that mattered.
		back := "Back"
		if s.priceEdited {
			back = "Back, drop entry"
		}
		items := []actionBarItem{{"Enter", "Quantity & price"}, {"Esc", back}}
		if s.confirmScrolls() {
			items = append(items,
				actionBarItem{"UP/DN", "Scroll"},
				actionBarItem{"PgUp/PgDn", "Page"},
				actionBarItem{"Home/End", "Top/End"})
		}
		return items
	case poAddPhasePrice:
		return []actionBarItem{{"Enter", "Add line"}, {"Esc", "Back"}, {"UP/DN", "Fields"}}
	case poAddPhaseAdding:
		return []actionBarItem{{"Esc", "Back to order"}}
	}
	return nil
}

// choosePages reports whether the candidate list is longer than the pane shows,
// which is the only state where PgUp/PgDn move anything.
func (s *PurchaseOrderAddLineScreen) choosePages() bool {
	return s.bodyScrollsForBar(s.chooseBody(), len(s.headerLines()), s.chooseBarItems(true))
}

// chooseFrameRows is the lines the choose frame really windows its body into.
// It is now a one-line call on the shared layer's bodyAvailForBar, and is kept
// only so that the reason this screen asks the question at all stays written
// down beside chooseStep, the one caller left that needs the number rather than
// the yes/no (choosePages asks bodyScrollsForBar for that directly).
//
// It exists because three things must agree about it and two of them had
// already drifted: the RENDER (frameWrapped, which takes the bar's measured
// height off the pane and then the pinned header off what is left), the BAR's
// claim that PgUp/PgDn move something, and the pager's STEP. The step was
// written before the answer to a keypress was pinned into a header, was never
// re-derived, and measured against the whole pane with no header at all — so at
// 80x24 with six candidates it stepped by the entire list and PgDn was End on
// the row list an operator picks a purchase-order line from, while the bar said
// "Page". Restating the frame's arithmetic here is what let them drift in the
// first place, so it is not restated: sc-jde-lift moved it into jde_form.go,
// next to the Window it has to agree with.
//
// The bar it measures is the PAGING bar, because a step is only ever taken when
// paging is on and that is therefore the bar being drawn.
func (s *PurchaseOrderAddLineScreen) chooseFrameRows() int {
	return s.bodyAvailForBar(len(s.headerLines()), s.chooseBarItems(true))
}

// chooseStep is how many candidate rows one page covers — measured off the same
// window the frame draws, so a page moves by exactly what the operator can see.
func (s *PurchaseOrderAddLineScreen) chooseStep() int {
	_, rows := s.chooseBody().Window(s.cursor, s.chooseFrameRows())
	if rows < 1 {
		return 1
	}
	return rows
}

// chooseBarItems is the choose bar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
func (s *PurchaseOrderAddLineScreen) chooseBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Choose"}, {"Esc", "Back"}, {"UP/DN", "Move"}}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// confirmScrolls is the confirm frame's half of the bar-honesty rule: the
// scroll keys are named when, and only when, the body actually moves.
func (s *PurchaseOrderAddLineScreen) confirmScrolls() bool {
	return s.bodyScrollsForBar(s.confirmBody(), len(s.headerLines()), s.confirmBarItems(true))
}

// confirmBarItems is the confirm bar for a given scroll state, so the bar that
// is MEASURED is the bar that is drawn. The Esc label is the live one for the
// same reason: it is two cells wider once something has been typed, which is
// enough to fold the bar onto another row and take a body row with it.
func (s *PurchaseOrderAddLineScreen) confirmBarItems(scroll bool) []actionBarItem {
	back := "Back"
	if s.priceEdited {
		back = "Back, drop entry"
	}
	items := []actionBarItem{{"Enter", "Quantity & price"}, {"Esc", back}}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *PurchaseOrderAddLineScreen) View() string {
	// The working line is BOUNDED by the same rule as the failure headline two
	// rows below it: the status row cannot fold, and clampToBox cutting it would
	// drop the closing SGR reset off the styled string and leave the terminal
	// coloured for everything drawn afterwards. Two lines the operator reads
	// together must not be bounded by different rules — which is now structural
	// rather than a promise, because BOTH go through statusRow and the bound
	// lives inside it. The failure branch used to render its own styled row with
	// its own copy of the mark and the clip; it asks the layer for an error row
	// instead, which is the only way this screen can draw one.
	status := s.statusRow(s.phase == poAddPhaseLooking || s.pending, s.workingLine(), "")
	if status == "" && s.failHead != "" {
		status = s.statusRow(false, "", s.failHead)
	}
	header := s.headerLines()
	switch s.phase {
	case poAddPhaseChoose:
		return s.frameWrapped(header, s.chooseBody(), s.cursor, status, s.bar())
	case poAddPhaseConfirm:
		frame, offset := s.frameScrolled(header, s.confirmBody(), s.scroll, status, s.bar())
		s.scroll = offset
		return frame
	case poAddPhasePrice:
		return s.frameWrapped(header, s.priceBody(), s.priceFocus, status, s.bar())
	}
	return s.frameWrapped(header, s.identifyBody(), 0, status, s.bar())
}

// headerLines is the screen's answer to the last keypress, PINNED above the
// scrollable body on every frame.
//
// Pinned rather than appended, because the answer is the one line that must not
// be able to scroll away: inside the body it sat below a cursor the operator had
// walked down a candidate list, or below the fold of a confirm frame that had
// grown two caveats — and a key that answers off the pane has not answered. The
// diagnostic DETAIL rides with it, bounded, so a failure and its reason are one
// block rather than two that can be separated by a scroll.
func (s *PurchaseOrderAddLineScreen) headerLines() []string {
	lines := append(s.noteLines(), s.failLines()...)
	if len(lines) == 0 {
		return nil
	}
	return append(lines, "")
}

// workingLine names the work AND the subject: "Loading…" tells an operator
// nothing they could act on. "Adding the line" was half of that — it named the
// order but not what was going on it, which is the one thing an operator
// watching a slow gateway wants confirmed.
//
// The identifier is what gives here, as everywhere else on this screen: the
// item name is bounded against what the fixed words and the order number leave,
// so a long catalogue name shortens rather than pushing the order off the row.
//
// BOTH branches are bounded by that one rule, which is the whole point of
// saying it. The lookup branch used to clip the query and the supplier to a
// hard-coded twenty cells each and was then clipped AGAIN by the status row's
// own bound, so at 80 columns the row read `Looking up widget in Acme
// Fasteners & In…'s cata…` — an ellipsis from the inner clip, and the
// sentence's OWN closing words destroyed by the outer one. At 120 columns the
// same constant threw away sixty-odd columns the pane had for the supplier's
// name.
//
// Neither branch reserves anything for a MARK. This is the muted branch of the
// status row and it draws none — the "✗ " belongs to the failure line — and
// the row's own bound now measures the mark it is actually handed, so a
// reservation here would be two more columns of the operator's supplier name
// thrown away on a pane that had room for them.
func (s *PurchaseOrderAddLineScreen) workingLine() string {
	if s.pending {
		lead, tail := "Adding ", " to "+s.orderName()+"…"
		room := s.barWidth() - lipgloss.Width(lead) - lipgloss.Width(tail)
		return lead + s.addingSentence(room) + tail
	}
	const (
		lead = "Looking up "
		mid  = " in "
		tail = "'s catalogue…"
	)
	room := s.barWidth() -
		lipgloss.Width(lead) - lipgloss.Width(mid) - lipgloss.Width(tail)
	query, supplier := poAddShareRow(room, strings.TrimSpace(s.idIn.Value()), s.supplierName())
	return lead + query + mid + supplier + tail
}

// poAddShareRow splits `room` between the two identifiers on the working line.
//
// Neither is a fact that never gives — they are both identifiers, and the frame
// carries each of them in full on a field row of its own — so they share evenly
// rather than one being served ahead of the other, and whatever one does not
// need is left to the other. That is what makes the common case (a short
// scanned SKU beside a long supplier name) spend the whole row on the name.
func poAddShareRow(room int, first, second string) (string, string) {
	firstRoom, secondRoom := room/2, room-room/2
	switch fw, sw := lipgloss.Width(first), lipgloss.Width(second); {
	case fw+sw <= room:
		return first, second
	case fw < firstRoom:
		firstRoom, secondRoom = fw, room-fw
	case sw < secondRoom:
		firstRoom, secondRoom = room-sw, sw
	}
	return pickerClip(first, firstRoom), pickerClip(second, secondRoom)
}

// noteLines renders the screen's answer to the last keypress, folded to the
// pane the terminal really gave. Root.View TRUNCATES rather than wrapping, and
// the tail of these sentences is where the key that gets the operator OUT is
// named — a clipped hint is worse than none, because they believe they read it.
func (s *PurchaseOrderAddLineScreen) noteLines() []string {
	lines := s.note.renderLines(s.paneWidth() - len(jdeIndent))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, jdeIndent+line)
	}
	return out
}

// failLines draws the failure line's detail under the headline the status row
// carries. The detail is an OMS response body and is unbounded, so it is CUT to
// what the pane can hold before it is folded — folding a multi-KB gateway page
// is work whose result is thrown away, on a block redrawn every keystroke.
func (s *PurchaseOrderAddLineScreen) failLines() []string {
	if s.failDetail == "" {
		return nil
	}
	width := s.paneWidth() - len(jdeIndent)
	if width < 12 {
		width = 12
	}
	rows := poAddFailDetailRows
	trimmed := cellPrefix(s.failDetail, rows*width)
	out := make([]string, 0, rows)
	for i, line := range pickerWrap(trimmed, width) {
		if i >= rows {
			break
		}
		out = append(out, jdeIndent+StyleMuted.Render(line))
	}
	return out
}

// poAddFailDetailRows caps the failure detail. The sentence naming what failed
// is on the status row above it and never gives; what a short terminal loses is
// the tail of the gateway's HTML.
const poAddFailDetailRows = 3

// poAddServerSentenceCells bounds a server-supplied sentence before it is
// pasted into one of this screen's own. Those sentences are prose OMS composed
// and are not length-bounded there; the part of ours that names the way out
// comes after them.
const poAddServerSentenceCells = 160

func (s *PurchaseOrderAddLineScreen) identifyBody() *jdeLines {
	l := &jdeLines{}
	lw := poAddLabelWidth()
	pane := s.paneWidth()

	l.Add(jdeIndent + StyleJDEHeading.Render("Add a line by scanning or typing"))
	l.Add("")
	l.AddFittedFields([]jdeField{
		{Label: "Supplier", Kind: jdeValue, Value: pickerClip(s.supplierName(), poAddValueCells(pane, lw))},
		{Label: "Order", Kind: jdeValue, Value: pickerClip(s.orderName(), poAddValueCells(pane, lw))},
	}, lw, pane, jdeNoRow)
	l.Add("")
	// Focused only on the phase the caret is really on: with a lookup out the
	// row is still drawn (it holds the query, and esc comes back to it) but the
	// keyboard belongs to that frame, and a row painted as focused while it
	// cannot be typed into is the bar-honesty rule wearing reverse video.
	l.AddFittedFields([]jdeField{{
		Label: "Scan / type", Kind: jdeText, Input: &s.idIn, Width: 24,
		Hint: "name, SKU or barcode", Focused: s.phase == poAddPhaseIdentify,
	}}, lw, pane, 0)
	s.addTally(l)
	return l
}

// addTally draws what this visit has already put on the order, NEWEST first and
// budgeted to what the pane has left.
//
// Newest first because that is the row an operator checks after a scan, and
// budgeted because the alternative — letting the window clip it — hides exactly
// the newest ones. What does not fit is COUNTED rather than dropped silently.
func (s *PurchaseOrderAddLineScreen) addTally(l *jdeLines) {
	if len(s.added) == 0 {
		return
	}
	pane := s.paneWidth()
	room := windowedListRoom(pane)

	// Two rows of chrome (the blank and the heading) plus at least one entry,
	// measured against what the frame has left rather than assumed.
	budget := s.bodyAvailForBar(len(s.headerLines()), s.bar()) - l.Len() - 2
	if budget < 1 {
		// No room to list any of them: say how many there are, which is the fact
		// the operator would otherwise lose entirely.
		l.Add(jdeIndent + StyleMuted.Render(fitCell(
			fmt.Sprintf("%d added this visit (no room to list them here)", len(s.added)), pane-len(jdeIndent))))
		return
	}
	shown := len(s.added)
	if shown > budget {
		shown = budget
	}
	hidden := len(s.added) - shown

	l.Add("")
	head := fmt.Sprintf("Added this visit: %d", len(s.added))
	if hidden > 0 {
		head += fmt.Sprintf(" (newest %d shown)", shown)
	}
	l.Add(jdeIndent + StyleJDEHeading.Render(fitCell(head, pane-len(jdeIndent))))
	for i := 0; i < shown; i++ {
		entry := s.added[len(s.added)-1-i]
		l.Add("    " + StyleMuted.Render(poAddTallyRow(entry, room)))
	}
}

// poAddTallyRow fits one tally entry: the item NAME is the bounded identifier
// and abbreviates, the quantity and the price are facts and never give.
//
// A grown line whose delta this screen could not derive reports where the line
// STANDS rather than a delta of zero — the row would otherwise read "0 @ 4.50"
// about an add that really did put stock on the order.
func poAddTallyRow(l poAddedLine, room int) string {
	if !l.deltaKnown {
		return poFitRow(room, l.name, fmt.Sprintf("  now %d @ %s", l.after, poAddMoney(l.unitCost)))
	}
	facts := fmt.Sprintf("  %d @ %s", l.quantity, poAddMoney(l.unitCost))
	grew := ""
	if !l.created {
		grew = fmt.Sprintf("  → %d", l.after)
	}
	return poFitRow(room, l.name, facts, grew)
}

// poAddValueCells is the room a jdeValue row's value has after the indent, the
// label column and the leader. Derived from the live pane, so a wide terminal
// draws a long supplier name in full.
func poAddValueCells(pane, labelWidth int) int {
	room := pane - len(jdeIndent) - labelWidth - len(jdeLeader)
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	return room
}

func (s *PurchaseOrderAddLineScreen) chooseBody() *jdeLines {
	l := &jdeLines{}
	pane := s.paneWidth()
	list := s.candidates()

	l.Add(jdeIndent + StyleJDEHeading.Render("Which one?"))
	l.Add("")
	room := windowedListRoom(pane)
	for i, c := range list {
		caret := "    "
		head := fmt.Sprintf("%d  %s", i+1, c.Item.Name)
		facts := ""
		if c.SupplierSKU != "" {
			facts = "  " + pickerClip(c.SupplierSKU, poAddSKUCells(room))
		}
		row := caret + poFitRow(room, head, facts)
		if i == s.cursor {
			row = StyleSidebarItemActive.Render("  ▸ " + poFitRow(room, head, facts))
		}
		l.AddRow(i, row)
		l.AddRow(i, "      "+StyleMuted.Render(fitCell(s.candidateFacts(c, pane-6), pane-6)))
	}
	return l
}

// poAddSKUCells bounds a supplier SKU before it reaches a row's facts column.
// A SKU is an IDENTIFIER that happens to sit among the facts — a 32-cell
// manufacturer part number is ordinary MRO data — and a bound expressed in
// terms of an unbounded value is not a bound.
func poAddSKUCells(room int) int {
	cells := room/2 - 2
	if cells < poHeaderValueFloor {
		cells = poHeaderValueFloor
	}
	return cells
}

// candidateFacts is the second line of a candidate row: why it matched, what a
// fresh line would land on, and whether the order already carries it.
//
// It obeys the same rule as poFitRow one line above it, and for the same
// reason: this is the row an operator PICKS from. The suggested quantity, the
// price and the on-order count are FACTS and never give — a price cut to
// "@ 4." reads as a whole price, which is worse than an absent one. The match
// LABEL is OMS prose ("another supplier's listing (Globex Industrial)") and is
// the identifier that abbreviates, so it is bounded against what the facts and
// their separators leave BEFORE the line is assembled. Budgeting the facts
// behind an unbounded label is not a bound at all: at 51 columns that label
// plus an on-order count pushed the price off the row entirely.
func (s *PurchaseOrderAddLineScreen) candidateFacts(c omsapi.POLineCandidate, room int) string {
	facts := []string{fmt.Sprintf("%d @ %s", c.SuggestedQuantity, poAddMoney(c.SuggestedUnitCost.String()))}
	if c.AlreadyOnOrder != nil {
		facts = append(facts, fmt.Sprintf("on order: %d", c.AlreadyOnOrder.QuantityOrdered))
	}
	if c.Item.IsKit {
		facts = append(facts, "kit")
	}
	tail := strings.Join(facts, poAddFactSep)

	label := pickerClip(c.MatchLabel, poAddMatchLabelCells(room-lipgloss.Width(tail)))
	if label == "" {
		// Nothing left for the label after the facts: it goes rather than
		// shortening them, and the confirm frame still carries it in full.
		return tail
	}
	return label + poAddFactSep + tail
}

// poAddFactSep joins the parts of a candidate's fact line.
const poAddFactSep = " · "

// poAddMatchedRow is the confirm frame's Matched row, and it is the one row on
// this screen where the sacrifice order is most costly to get backwards.
//
// This is a CONFIRM surface, so the fact being confirmed survives whole and the
// identifier abbreviates: the MATCHED VALUE is what the operator scanned or
// typed and is the very thing they are being asked to say yes to, while the
// server's MatchLabel is prose explaining WHY it matched. Assembled label-first
// and clipped as one string, the row gave up the value — an ordinary supplier
// SKU came out as `supplier SKU "AF-99-12-ZP-LH…` on the frame whose whole job
// is confirming it, and a cross-vendor label (45 cells on its own) filled the
// row with prose and dropped the scanned value entirely, with the ellipsis
// reading as a cut LABEL rather than a missing field.
//
// So the value is bounded first and the label takes what is left. Dropping the
// label costs nothing the operator cannot recover: the Supplier SKU row is
// directly above it and the pinned note reads "matched on <label>" in full,
// folded. Dropping the value has no such fallback.
//
// The label is NOT capped at a constant here, unlike the candidate list's: a
// wide terminal has room for the whole sentence and discarding what the pane
// could have shown is the other half of the same rule.
func poAddMatchedRow(label, value string, room int) string {
	quoted := pickerClip(fmt.Sprintf("%q", value), room)
	budget := room - lipgloss.Width(quoted) - len(poAddMatchedSep)
	if budget < poHeaderValueFloor {
		return quoted
	}
	return pickerClip(label, budget) + poAddMatchedSep + quoted
}

// poAddMatchedSep is the single space between the match label and the value it
// explains. One cell, because every cell it takes comes out of the label's.
const poAddMatchedSep = " "

// poAddMatchLabelCells is what the match label may draw into once the facts
// have taken theirs. `room` is what is left INCLUDING the separator, so the
// separator is charged here rather than by a caller who might forget it. The
// ceiling is the label's own reasonable width on a wide terminal; below the
// floor the label is dropped entirely rather than clipped to an ellipsis and a
// letter, which would claim a fact nobody could read.
func poAddMatchLabelCells(room int) int {
	room -= lipgloss.Width(poAddFactSep)
	if room > poAddMatchLabelMax {
		room = poAddMatchLabelMax
	}
	if room < poHeaderValueFloor {
		return 0
	}
	return room
}

// poAddMatchLabelMax is as wide as the label is ever worth drawing: on a
// 120-column terminal the facts leave far more room than the longest label OMS
// composes, and a whole line of prose beside a two-word fact reads as the row
// being ABOUT the label.
const poAddMatchLabelMax = 34

func (s *PurchaseOrderAddLineScreen) confirmBody() *jdeLines {
	l := &jdeLines{}
	lw := poAddLabelWidth()
	pane := s.paneWidth()
	c := s.chosen
	if c == nil {
		l.Add(jdeIndent + StyleMuted.Render("Nothing is staged."))
		return l
	}
	room := poAddValueCells(pane, lw)

	l.Add(jdeIndent + StyleJDEHeading.Render("Is this the right item?"))
	l.Add("")
	fields := []jdeField{
		{Label: "Item", Kind: jdeValue, Value: pickerClip(c.Item.Name, room)},
		{Label: "Item SKU", Kind: jdeValue, Value: pickerClip(orDash(c.Item.SKU), room)},
		{Label: "Supplier", Kind: jdeValue, Value: pickerClip(s.supplierName(), room)},
		{Label: "Supplier SKU", Kind: jdeValue, Value: pickerClip(orDash(c.SupplierSKU), room)},
		{Label: "Matched", Kind: jdeValue, Value: poAddMatchedRow(c.MatchLabel, c.MatchedValue, room)},
	}
	if c.QuantityPerPackage > 1 {
		fields = append(fields, jdeField{Label: "Pack", Kind: jdeValue,
			Value: fmt.Sprintf("%d per package", c.QuantityPerPackage)})
	}
	if r := c.AlreadyOnOrder; r != nil {
		fields = append(fields, jdeField{Label: "On order", Kind: jdeValue,
			Value: pickerClip(poAddOnOrderValue(*r), room)})
	}
	l.AddFittedFields(fields, lw, pane, jdeNoRow)

	for _, caveat := range s.confirmCaveats(*c) {
		l.Add("")
		for _, line := range jdeCaveatLines(caveat, pane) {
			l.Add(line)
		}
	}
	return l
}

// poAddOnOrderValue says what a repeat add will DO, not merely that the item is
// already there. A voided line reports no outcome because the add is refused
// outright rather than resurrecting it, and quoting one would be a lie.
func poAddOnOrderValue(r omsapi.POLineExisting) string {
	if r.IsVoided {
		return fmt.Sprintf("%d ordered on a VOIDED line", r.QuantityOrdered)
	}
	if r.RepeatIncrement == nil || r.QuantityOrderedAfter == nil {
		return fmt.Sprintf("%d ordered", r.QuantityOrdered)
	}
	return fmt.Sprintf("%d ordered → %d", r.QuantityOrdered, *r.QuantityOrderedAfter)
}

// confirmCaveats are the things about THIS candidate the operator would not
// otherwise know, strongest first — the frame scrolls, and what scrolls off is
// the tail.
func (s *PurchaseOrderAddLineScreen) confirmCaveats(c omsapi.POLineCandidate) []string {
	var out []string
	if c.FromAnotherVendor() {
		// The LABEL leads, because it is the fact the operator does not have:
		// OMS writes it as "another supplier's listing (Globex Industrial)", and
		// on a 51-column pane the caveat's first folded line is the only part
		// guaranteed to be on screen. Led by our own prose instead, the vendor's
		// name landed below the fold on the frame that exists to disclose it.
		out = append(out, fmt.Sprintf(
			"%s — this order's supplier has its own catalogue row for the SAME item, and that "+
				"is what will be ordered. Nothing has been substituted.",
			pickerClip(c.MatchLabel, poAddServerSentenceCells)))
	}
	if r := c.AlreadyOnOrder; r != nil && !r.IsVoided {
		out = append(out, "This order already has a line for this item, so adding grows that line "+
			"rather than making a second one. Leave the unit cost blank to keep the price it carries.")
	}
	if r := c.AlreadyOnOrder; r != nil && r.IsVoided {
		out = append(out, "The line this order has for this item is VOIDED. The add will be refused: "+
			"restore or remove that line first.")
	}
	if c.Item.IsKit {
		out = append(out, "This is a kit: it is ordered as one SKU and credits its COMPONENT items "+
			"on receipt, never its own stock.")
	}
	if !c.IsExact {
		out = append(out, "This is a partial match, not an exact identifier — check the SKU above "+
			"against what is in front of you.")
	}
	return out
}

// confirmEntryNote is what the confirm frame says on arrival. It carries no key
// lead: the whole pane changed, so a lead naming the key that got here would be
// noise.
//
// TWO states reach this frame and the tail is worded for each, because what
// `esc` DOES differs between them and a frame may only name keys as they behave
// where it is drawn. Straight off a lookup that resolved, esc goes back to the
// scan box and the other matches are nowhere on screen, so the note says how to
// reach them. Off the choice list, esc goes back to that list with those
// matches already drawn — telling that operator to narrow a search would
// describe the other path's key.
func (s *PurchaseOrderAddLineScreen) confirmEntryNote(c omsapi.POLineCandidate, from poAddPhase) string {
	extra := ""
	switch {
	case from == poAddPhaseChoose && len(s.candidates()) > 1:
		extra = fmt.Sprintf(" · esc goes back to the %d matches", len(s.candidates()))
	case from != poAddPhaseChoose && s.lookup != nil && s.lookup.TotalCandidates > 1:
		// One is the ORDINARY shape of a resolving lookup with company — an
		// exact identifier alongside a single partial-name match — not a corner.
		others, items, them := s.lookup.TotalCandidates-1, "items", "them"
		if others == 1 {
			items, them = "item", "it"
		}
		extra = fmt.Sprintf(" · %d other %s also matched — esc, then narrow the search, to see %s",
			others, items, them)
	}
	// The label is FOLDED, not clipped to a row, so its bound is the one every
	// server-supplied sentence on this screen gets rather than a row budget: at
	// 40 cells "another supplier's listing (Globex Industrial)" lost the vendor,
	// which is the single fact this note exists to carry.
	return fmt.Sprintf("matched on %s%s", pickerClip(c.MatchLabel, poAddServerSentenceCells), extra)
}

// priceEntryNote says what the two prefilled rows mean, including what an empty
// one does — which is the only way "blank keeps the price" can be an informed
// choice rather than a trap.
func (s *PurchaseOrderAddLineScreen) priceEntryNote() string {
	if s.chosen != nil && s.chosen.AlreadyOnOrder != nil && !s.chosen.AlreadyOnOrder.IsVoided {
		return "quantity is one package, as a repeat scan would add · " +
			"leave the unit cost blank to keep the line's price, or type one to reprice the whole line"
	}
	if s.chosen != nil && poAddIsZeroMoney(s.chosen.SuggestedUnitCost.String()) {
		return "both rows hold " + s.supplierName() + "'s own defaults · " +
			"there is NO price on file for this item — 0.00 is the default, not a quote"
	}
	return "both rows hold " + s.supplierName() + "'s own defaults · type over either one · " +
		"enter adds the line"
}

func (s *PurchaseOrderAddLineScreen) priceBody() *jdeLines {
	l := &jdeLines{}
	lw := poAddLabelWidth()
	pane := s.paneWidth()
	room := poAddValueCells(pane, lw)

	l.Add(jdeIndent + StyleJDEHeading.Render("Quantity and price"))
	l.Add("")
	if c := s.chosen; c != nil {
		l.AddFittedFields([]jdeField{
			{Label: "Item", Kind: jdeValue, Value: pickerClip(c.Item.Name, room)},
			{Label: "Supplier SKU", Kind: jdeValue, Value: pickerClip(orDash(c.SupplierSKU), room)},
		}, lw, pane, jdeNoRow)
		l.Add("")
	}
	l.AddFittedFields([]jdeField{
		{Label: "Quantity", Kind: jdeText, Input: &s.qtyIn, Width: 9,
			Hint: s.quantityHint(), Focused: s.priceFocus == poAddFieldQty},
		{Label: "Unit cost", Kind: jdeText, Input: &s.costIn, Width: 12,
			Hint: s.costHint(), Focused: s.priceFocus == poAddFieldCost},
	}, lw, pane, 0)
	if total := s.lineTotal(); total != "" {
		l.AddFittedFields([]jdeField{
			{Label: "Line total", Kind: jdeValue, Value: total, Dim: true},
		}, lw, pane, jdeNoRow)
	}
	return l
}

// quantityHint says what this row's number will do — grow a line, or open one.
func (s *PurchaseOrderAddLineScreen) quantityHint() string {
	if c := s.chosen; c != nil && c.AlreadyOnOrder != nil && !c.AlreadyOnOrder.IsVoided {
		return fmt.Sprintf("adds to the %d on order", c.AlreadyOnOrder.QuantityOrdered)
	}
	return "whole units"
}

// costHint names what BLANK means on this row, which differs between a fresh
// line and a repeat because the server's own default does.
func (s *PurchaseOrderAddLineScreen) costHint() string {
	if c := s.chosen; c != nil && c.AlreadyOnOrder != nil && !c.AlreadyOnOrder.IsVoided {
		if now := s.existingLinePrice(c.AlreadyOnOrder.LineItem); now != "" {
			return "blank keeps " + poAddMoney(now)
		}
		return "blank keeps the line's price"
	}
	if c := s.chosen; c != nil && poAddIsZeroMoney(c.SuggestedUnitCost.String()) {
		return "no price on file"
	}
	return "per unit"
}

// existingLinePrice reads the price the order already carries for a line, off
// the order in hand. Empty when this screen has no order or cannot find the
// line — in which case the hint says the weaker, true thing instead of naming a
// number it does not have.
func (s *PurchaseOrderAddLineScreen) existingLinePrice(lineID string) string {
	if s.po == nil || lineID == "" {
		return ""
	}
	for _, item := range s.po.Items {
		if fmt.Sprintf("%v", item.ID) == lineID {
			return item.UnitCostOrdered.String()
		}
	}
	return ""
}

// lineTotal is quantity × unit cost, computed EXACTLY (big.Rat, not float).
//
// It is drawn EXACTLY when both rows hold a value the submit would accept, and
// nothing is drawn otherwise. Both halves of that matter and neither is wider
// than the code:
//
//   - Never over an entry Enter is about to refuse. That is why the rows are
//     read through the SAME functions the submit reads them through rather than
//     through a predicate of this row's own — two judges disagreed, and "5.5"
//     drew a total that Enter then rejected.
//   - Never over a BLANK row either. Blank means "the server decides", which is
//     the ordinary repeat-add entry and is accepted, but the number it will
//     decide on is not one this screen holds. A figure invented to fill the row
//     is the fabrication this whole screen refuses everywhere else.
//
// A rounding artefact would be worse than the row's absence, hence big.Rat.
func (s *PurchaseOrderAddLineScreen) lineTotal() string {
	qty, refusal := s.readQuantityRow()
	if refusal != "" || qty == 0 {
		return ""
	}
	costRaw, refusal := s.readCostRow()
	if refusal != "" || costRaw == "" {
		return ""
	}
	cost, ok := new(big.Rat).SetString(costRaw)
	if !ok {
		return ""
	}
	return new(big.Rat).Mul(new(big.Rat).SetInt64(int64(qty)), cost).FloatString(2)
}

// addingSentence names the SUBJECT of the add — the quantity and the item that
// have already gone to the server — for the working line and the freeze's
// declines alike. Both used to name the order or nothing at all, so the one
// fact an operator watching a slow gateway wants confirmed was on neither.
//
// A blank quantity row is left unspoken rather than guessed at: blank means the
// server derives it, and this screen does not hold that number.
//
// `room` is the cells the caller has left after its own fixed words, so the
// name is bounded by what the row really has rather than by a constant that was
// true of one caller.
func (s *PurchaseOrderAddLineScreen) addingSentence(room int) string {
	lead := ""
	if qty := strings.TrimSpace(s.qtyIn.Value()); qty != "" {
		lead = qty + " × "
	}
	return lead + pickerClip(s.chosenName(), poAddSubjectCells(room-lipgloss.Width(lead)))
}

// poAddSubjectCells bounds the item name inside addingSentence: never wider
// than this screen's own note bound, never so narrow that the name disappears
// entirely — a subject clipped to nothing would undo the whole point of naming
// it.
func poAddSubjectCells(room int) int {
	if room > poAddNoteNameCells {
		room = poAddNoteNameCells
	}
	if room < poHeaderValueFloor {
		room = poHeaderValueFloor
	}
	return room
}

// ---------------------------------------------------------------------------
// Small readers
// ---------------------------------------------------------------------------

func (s *PurchaseOrderAddLineScreen) candidates() []omsapi.POLineCandidate {
	if s.lookup == nil {
		return nil
	}
	return s.lookup.Candidates
}

func (s *PurchaseOrderAddLineScreen) chosenName() string {
	if s.chosen == nil {
		return "the item"
	}
	if s.chosen.Item.Name != "" {
		return s.chosen.Item.Name
	}
	return s.chosen.Item.SKU
}

// supplierName prefers whatever the LOOKUP said, because that is the supplier
// the server actually scoped the search to; the order's own copy is the
// fallback for the first frame, before any lookup has answered.
func (s *PurchaseOrderAddLineScreen) supplierName() string {
	if s.lookup != nil && s.lookup.Supplier.Name != "" {
		return s.lookup.Supplier.Name
	}
	if s.supplier != "" {
		return s.supplier
	}
	return "this supplier"
}

func (s *PurchaseOrderAddLineScreen) orderName() string {
	if s.poNumber != "" {
		return s.poNumber
	}
	return "this order"
}

// poAddMoney renders a money string for display. An empty value is an ABSENCE
// and says so rather than rendering as a bare number the operator would read as
// a price.
func poAddMoney(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "(no price)"
	}
	return v
}

// poAddIsZeroMoney reports the server's "nothing on file" price. It is compared
// numerically, so "0", "0.00" and "0.0000" are one answer rather than three.
func poAddIsZeroMoney(v string) bool {
	r, ok := new(big.Rat).SetString(strings.TrimSpace(v))
	return ok && r.Sign() == 0
}
