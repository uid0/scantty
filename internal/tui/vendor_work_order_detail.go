// VendorWorkOrderDetailScreen — the seven-step third-party work-order workflow,
// driven from the terminal.
//
// TUI counterpart to the web's ThirdPartyWorkOrderPage stepper
// (frontend/src/pages/ThirdPartyWorkOrderPage.tsx). Before this, ScanTTY could
// CREATE a vendor work order (asset_problems.go's promote-to-vendor) and then
// drive none of the actions that move it: the terminal made work it could not
// list, advance or close, and the operator finished in the browser.
//
// THE STATE MACHINE IS THE SERVER'S. backend/maintenance_orders/transitions.py
// owns every gate and this screen re-derives none of them — what it reads is
// the `workflow` block the serializer computes, which is the same block the web
// stepper's gates render from. Two of those facts cannot be computed here at
// all (the 24-hour emergency window needs the server's clock; the attachment
// gates need the attachment rows), and one of them would be WRONG if computed:
// "three quotes" is not the rule, it is one of three ways to satisfy it.
//
// KEY SCHEME. One idea: ENTER IS THE NEXT STEP. Each live status has exactly
// one forward transition, so Enter carries it and the bar says which one
// ("Enter=To sourcing", "Enter=Vendor arrived", "Enter=Close"). Everything that
// is NOT the next step is a letter:
//
//	Enter      the one forward transition this status has
//	n          set the Not-To-Exceed amount        (requested / sourcing)
//	q          add a vendor quote                  (sourcing)
//	w          waive the three-quote requirement   (sourcing)
//	e          authorize a 24h emergency bypass    (any live status)
//	a          attach a file                       (any live status)
//	k          record the keyfob return            (while one is outstanding)
//	o          override a blocked variance         (financial review, blocked)
//	r          refresh
//	Esc        back
//	UP/DN · PgUp/PgDn · Home/End   scroll the sheet, when it outruns the pane
//
// NOTHING WRITES ON ONE KEYSTROKE. Every action opens either a form or a
// confirm; Enter on a form goes to the CONFIRM, never to the wire; and the
// confirm commits on Ctrl-X. That is the pilot's own reasoning (po_edit.go):
// the key that OPENS a prompt must not be the key that commits it, or a
// reflexive double-tap is enough. It matters more here than it does there,
// because these writes are money — an NTE is a spending ceiling, an emergency
// authorization removes one, a waiver ends the shopping around, and an invoice
// capture is what the makerspace is billed against. THE CONFIRM STATES THE
// FIGURES IT IS ABOUT TO SEND, so what is being committed to is on the pane
// before it goes.
//
// EVERY FORWARD TRANSITION IS IRREVERSIBLE, which is why they are all
// confirmed rather than only the money-shaped ones: transitions.py has no
// backward transition at all, no unsign, no unwaive, no reopen, and
// authorize_emergency's window can only expire. There is nothing on this screen
// that a second keypress undoes.
package tui

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type vwoPhase int

const (
	// vwoPhaseSheet is the read-only sheet: the order, its money, its gates and
	// its paperwork. It owns no navigable ROW — there is nothing to choose and
	// nothing to type — so it is windowed by an OFFSET through frameScrolled
	// rather than by a cursor, which is the layer's instrument for a body a
	// cursor cannot anchor (jdeLines.block answers (0,0) for such a body and a
	// cursor-anchored window would pin it at line 0 forever).
	vwoPhaseSheet vwoPhase = iota
	// vwoPhaseForm collects whatever the pending action needs typing into it.
	vwoPhaseForm
	// vwoPhaseConfirm states what is about to be sent and waits for Ctrl-X.
	vwoPhaseConfirm
	vwoPhaseCount
)

// vwoAction names one write this screen can make. vwoNone is the resting state
// and is never a pending action.
type vwoAction int

const (
	vwoNone vwoAction = iota
	vwoSetNTE
	vwoAuthorizeEmergency
	vwoAdvanceSourcing
	vwoAddQuote
	vwoWaiveQuotes
	vwoAdvanceScheduled
	vwoVendorArrived
	vwoSignOff
	vwoKeyfobReturn
	vwoAdvanceFinancial
	vwoOverrideVariance
	vwoClose
	vwoAttach
	vwoActionCount
)

// vwoField is one typed row on an action's form.
type vwoField struct {
	label string
	hint  string
	// money marks a row that is validated as a decimal amount and posted
	// VERBATIM. The value is never reformatted on the way out: the server
	// parses it with Decimal(str(value)), and a float round-trip on this side
	// is how a decimal stops being the number the operator typed.
	money bool
	// required refuses a blank row before the confirm is reached, so the
	// operator is never walked onto a frame whose Ctrl-X can only 400.
	required bool
	// choice, when non-empty, makes this a jdeChoice row cycled with ←/→
	// instead of a typed box.
	choice []string
	limit  int
}

// vwoSpec is everything about one action that does not depend on the order.
type vwoSpec struct {
	// key is the keystroke that opens it from the sheet. "enter" marks the
	// status's one forward transition.
	key string
	// bar is what the action bar calls it. Short on purpose: at 80 columns the
	// bar has 49 cells and this screen names up to eight keys.
	bar string
	// title heads the form and the confirm.
	title string
	// verb is the status row's working line while the write is out.
	verb   string
	fields []vwoField
	// caveat is what the confirm says the write DOES, in the operator's terms.
	// Every one of them names the irreversibility, because every one of them
	// is irreversible.
	caveat string
}

// vwoSpecs is the action table. The url_paths and the bodies they produce are
// omsapi/maintenance_orders.go's; what lives here is the OPERATOR's half.
var vwoSpecs = map[vwoAction]vwoSpec{
	vwoSetNTE: {
		key: "n", bar: "Set NTE", title: "Set the Not-To-Exceed amount",
		verb: "Setting the NTE…",
		fields: []vwoField{
			{label: "NTE amount", hint: "dollars", money: true, required: true, limit: 14},
		},
		caveat: "The NTE is the ceiling this vendor may bill against, and it is " +
			"what the final invoice is scored on. It can be re-set while the " +
			"order is still requested or sourcing, and not after.",
	},
	vwoAuthorizeEmergency: {
		key: "e", bar: "Emergency", title: "Authorize a 24-hour emergency bypass",
		verb: "Authorizing the emergency bypass…",
		fields: []vwoField{
			{label: "Reason", hint: "recorded in the audit log", limit: 200},
		},
		caveat: "Bypasses BOTH money gates for 24 hours: this order can then " +
			"advance with no NTE and with no quotes. It also marks the order " +
			"an emergency permanently. There is no revoke — the window only " +
			"expires, and the emergency mark never does.",
	},
	vwoAdvanceSourcing: {
		key: "enter", bar: "To sourcing", title: "Advance to sourcing",
		verb: "Advancing to sourcing…",
		caveat: "Moves the order from intake to sourcing, where quotes are " +
			"collected. There is no transition back.",
	},
	vwoAddQuote: {
		key: "q", bar: "Add quote", title: "Record a vendor quote",
		verb: "Recording the quote…",
		fields: []vwoField{
			{label: "Amount", hint: "dollars", money: true, required: true, limit: 14},
			{label: "Notes", hint: "optional", limit: 200},
		},
		caveat: "Records what this vendor quoted. Three quotes satisfy the " +
			"sourcing gate; a signed waiver or an emergency authorization " +
			"satisfies it instead.",
	},
	vwoWaiveQuotes: {
		key: "w", bar: "Waive quotes", title: "Waive the three-quote requirement",
		verb: "Signing the waiver…",
		fields: []vwoField{
			{label: "Reason", hint: "required for audit", required: true, limit: 300},
		},
		caveat: "Signs off on buying without shopping around, under your name " +
			"and with this reason on the record. There is no unsign.",
	},
	vwoAdvanceScheduled: {
		key: "enter", bar: "To scheduled", title: "Advance to scheduled",
		verb: "Advancing to scheduled…",
		caveat: "Commits to this vendor and notifies Ops that the visit is " +
			"booked. There is no transition back.",
	},
	vwoVendorArrived: {
		key: "enter", bar: "Vendor arrived", title: "Record the vendor's arrival",
		verb: "Recording the arrival…",
		fields: []vwoField{
			{label: "Keyfob ID", hint: "optional — blank keeps what is on file", limit: 64},
		},
		caveat: "STARTS THE DOWNTIME CLOCK, which stops only at sign-off and " +
			"is what the asset's downtime is reported from. A keyfob recorded " +
			"here must be returned before the order can close.",
	},
	vwoSignOff: {
		key: "enter", bar: "Sign off", title: "Ops sign-off",
		verb: "Signing off…",
		caveat: "STOPS THE DOWNTIME CLOCK and stores the total against the " +
			"order. There is no transition back.",
	},
	vwoKeyfobReturn: {
		key: "k", bar: "Keyfob back", title: "Record the keyfob return",
		verb: "Recording the keyfob return…",
		caveat: "Records that the vendor handed the fob back, which is what " +
			"clears the closure gate. Recording it a second time keeps the " +
			"original timestamp.",
	},
	vwoAdvanceFinancial: {
		key: "enter", bar: "To financial", title: "Capture the invoice",
		verb: "Capturing the invoice…",
		fields: []vwoField{
			{label: "Invoice total", hint: "dollars", money: true, required: true, limit: 14},
			{label: "Dispatch fee", hint: "optional — blank keeps what is on file",
				money: true, limit: 14},
		},
		caveat: "This is the figure the makerspace is billed. The server scores " +
			"it against the NTE, splits the dispatch fee across the linked " +
			"assets, and alerts Finance if the variance is over budget. It " +
			"cannot be re-entered from here.",
	},
	vwoOverrideVariance: {
		key: "o", bar: "Override", title: "Override the variance block",
		verb: "Overriding the variance…",
		fields: []vwoField{
			{label: "Reason", hint: "required for audit", required: true, limit: 300},
		},
		caveat: "Accepts an overage the NTE did not cover, under your name and " +
			"with this reason on the record, so the order can close. Staff " +
			"only, and there is no un-override.",
	},
	vwoClose: {
		key: "enter", bar: "Close", title: "Close the work order",
		verb: "Closing…",
		caveat: "Closes the order for good — there is no reopen. It also " +
			"resolves every problem report that was promoted into it, and on " +
			"a warranty-recovery order opens a Logistics recovery task.",
	},
	vwoAttach: {
		key: "a", bar: "Attach", title: "Attach a file",
		verb: "Uploading…",
		fields: []vwoField{
			{label: "File path", hint: "a path on this machine", required: true, limit: 500},
			{label: "Kind", choice: omsapi.MaintenanceOrderAttachmentKinds},
			{label: "Caption", hint: "optional", limit: 255},
		},
		caveat: "The KIND is a gate, not a filing label: sign-off needs a " +
			"photo, and closing needs an invoice AND an FSR. A file put under " +
			"the wrong kind leaves the gate shut.",
	},
}

// vwoAdvanceFor is the ONE forward transition a status has, or vwoNone where it
// has none. Derived from transitions.py's _ensure_status calls, which are what
// refuse every other pairing.
func vwoAdvanceFor(status string) vwoAction {
	switch status {
	case omsapi.MaintenanceOrderRequested:
		return vwoAdvanceSourcing
	case omsapi.MaintenanceOrderSourcing:
		return vwoAdvanceScheduled
	case omsapi.MaintenanceOrderScheduled:
		return vwoVendorArrived
	case omsapi.MaintenanceOrderInProgress:
		return vwoSignOff
	case omsapi.MaintenanceOrderValidated:
		return vwoAdvanceFinancial
	case omsapi.MaintenanceOrderFinancialReview:
		return vwoClose
	}
	return vwoNone
}

// vwoLive reports whether the order is still moving. A closed or cancelled
// order takes no write at all, so the sheet drops every action key on it
// rather than naming keys that can only be refused.
func vwoLive(wo *omsapi.MaintenanceOrder) bool {
	return wo != nil &&
		wo.Status != omsapi.MaintenanceOrderClosed &&
		wo.Status != omsapi.MaintenanceOrderCancelled
}

// VendorWorkOrderDetailScreen is the stepper.
type VendorWorkOrderDetailScreen struct {
	deps Deps
	id   string
	// jdeScreen carries the pane geometry and the frames (jde_form.go).
	jdeScreen

	wo      *omsapi.MaintenanceOrder
	loading bool
	loadErr string

	phase  vwoPhase
	action vwoAction

	inputs []textinput.Model
	focus  int
	// kindIx is the attachment-kind choice row's selection, an index into
	// omsapi.MaintenanceOrderAttachmentKinds.
	kindIx int

	sending bool
	// errMsg is the headline of the last failed write, cleared by the next
	// success and by leaving the phase it happened on.
	errMsg string
	// note is what the last keypress DID. It is the thing that stops a key
	// which declines from redrawing a byte-identical pane, which is this
	// project's oldest reported defect ("it just kinda hangs there").
	note string
	// sheetScroll / confirmScroll are the two scrolled bodies' offsets. Each is
	// stored back from the frame that drew it, so `end` cannot leave an offset
	// the pane never reached.
	sheetScroll   int
	confirmScroll int
}

type vwoLoadedMsg struct {
	wo  *omsapi.MaintenanceOrder
	err error
}

// vwoWroteMsg is the answer to any of the writes. done names the action so the
// status line can say which one landed; refreshed carries the order the action
// answered with, which every action but authorize-emergency supplies.
type vwoWroteMsg struct {
	done      vwoAction
	refreshed *omsapi.MaintenanceOrder
	err       error
}

func NewVendorWorkOrderDetailScreen(deps Deps, id string) *VendorWorkOrderDetailScreen {
	return &VendorWorkOrderDetailScreen{deps: deps, id: id, loading: true}
}

func (s *VendorWorkOrderDetailScreen) Title() string {
	if s.wo != nil && s.wo.ShortID != "" {
		return "Vendor WO · " + s.wo.ShortID
	}
	return "Vendor work order"
}

// WantsRawInput claims the keyboard on the two phases that own it: the form has
// a caret in a box, and the confirm owns its own esc so backing out of a
// destructive prompt returns to the form rather than leaving the screen. The
// SHEET stays non-raw, so tab reaches the sidebar, ctrl+k reaches the search
// palette and esc is the global back step the bar names.
func (s *VendorWorkOrderDetailScreen) WantsRawInput() bool {
	return s.phase == vwoPhaseForm || s.phase == vwoPhaseConfirm
}

func (s *VendorWorkOrderDetailScreen) Init() tea.Cmd {
	return tea.Batch(s.load(), textinput.Blink)
}

func (s *VendorWorkOrderDetailScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *VendorWorkOrderDetailScreen) load() tea.Cmd {
	deps, ctx, id := s.deps, s.ctx(), s.id
	return func() tea.Msg {
		wo, err := deps.OMS.GetMaintenanceOrder(ctx, id)
		return vwoLoadedMsg{wo: wo, err: err}
	}
}

// ---------------------------------------------------------------------------
// What is offered
// ---------------------------------------------------------------------------

// vwoOffered reports whether an action is available on this order right now —
// the one predicate the BAR and the key ARMS both read, so "a key the bar does
// not name does nothing" holds by construction rather than by two expressions
// staying in step.
//
// Every clause here is a status or a workflow flag the SERVER supplied. None of
// them re-derives a rule: where transitions.py would refuse, this declines to
// offer, and where it might refuse for a reason this side cannot see (a
// permission, a race with the web) the write still goes and the refusal comes
// back as the server's own sentence.
func (s *VendorWorkOrderDetailScreen) vwoOffered(a vwoAction) bool {
	wo := s.wo
	if wo == nil || a == vwoNone || a >= vwoActionCount {
		return false
	}
	// Nothing writes while a write is out: a second POST against a state
	// machine mid-transition is a race whose loser reads as a random refusal.
	if s.sending {
		return false
	}
	if !vwoLive(wo) {
		return false
	}
	wf := wo.Workflow
	switch a {
	case vwoSetNTE:
		return wo.Status == omsapi.MaintenanceOrderRequested ||
			wo.Status == omsapi.MaintenanceOrderSourcing
	case vwoAuthorizeEmergency, vwoAttach:
		return true
	case vwoAddQuote, vwoWaiveQuotes:
		return wo.Status == omsapi.MaintenanceOrderSourcing
	case vwoKeyfobReturn:
		return wf.KeyfobOutstanding
	case vwoOverrideVariance:
		return wo.Status == omsapi.MaintenanceOrderFinancialReview &&
			wo.VarianceStatus == omsapi.MaintenanceOrderVarianceBlocked
	}
	// The forward transitions: offered only for the status they belong to, and
	// only once the gate the server will check is already satisfied. Offering
	// one through a shut gate would name a key that can only be refused — and
	// every gate below is one the operator can open from THIS screen, so
	// declining to offer it is never a dead end.
	if a != vwoAdvanceFor(wo.Status) {
		return false
	}
	return s.vwoBlockedBy(a) == ""
}

// vwoBlockedBy is why the forward transition cannot run yet, or "" when it can.
//
// The sentence names the REMEDY as well as the obstacle, because a gate an
// operator cannot see their way past is the dead end this whole screen exists
// to remove. Every remedy here is a key on this same frame.
func (s *VendorWorkOrderDetailScreen) vwoBlockedBy(a vwoAction) string {
	wo := s.wo
	if wo == nil {
		return ""
	}
	wf := wo.Workflow
	switch a {
	case vwoAdvanceSourcing:
		// Mirrors transitions.advance_to_sourcing: an NTE, the permanent
		// is_emergency mark, OR a live emergency authorization opens the gate.
		// IsEmergency has no expiry and is not redundant with the active-window
		// flag; dropping it here would refuse a transition the server accepts.
		if wf.HasNTE || wf.HasActiveEmergencyAuthorization || wo.IsEmergency {
			return ""
		}
		return "Needs an NTE (n) or an emergency authorization (e) first."
	case vwoAdvanceScheduled:
		if wf.HasRequiredQuotes {
			return ""
		}
		return fmt.Sprintf(
			"Needs 3 quotes, a waiver or an emergency authorization — %d on file. Add one with q, waive with w.",
			wf.QuoteCount)
	case vwoSignOff:
		if wf.HasPhotoEvidence {
			return ""
		}
		return "Needs at least one photo of the work. Attach one with a, kind photo."
	case vwoClose:
		switch {
		case wo.VarianceStatus == omsapi.MaintenanceOrderVarianceBlocked:
			return "The variance is over budget and blocks closure. Override it with o, or have the invoice reduced."
		case !wf.HasInvoiceAndFSR:
			return "Needs an invoice AND an FSR attached. Attach them with a."
		case wf.KeyfobOutstanding:
			return "Keyfob " + wo.KeyfobID + " is still out. Record its return with k."
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (s *VendorWorkOrderDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case vwoLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = vwoReason(m.err)
			return s, Status("load failed: "+s.loadErr, StatusError)
		}
		s.loadErr = ""
		s.wo = m.wo
		// A refresh can land under an open prompt — `r` on the sheet, or the
		// reload every write fires — and the order may have moved on in the
		// browser while it was out. A prompt whose action the refreshed order
		// no longer allows is closed, saying so, rather than left offering a
		// key the server would now refuse: the flag it was opened on is read
		// AGAIN, never cached across a refresh.
		if s.phase != vwoPhaseSheet && !s.sending && !s.vwoOffered(s.action) {
			s.note = vwoSpecs[s.action].bar + " is no longer available on this order."
			s.closePrompt()
		}
		return s, nil

	case vwoWroteMsg:
		return s.handleWrote(m)

	case tea.KeyMsg:
		switch s.phase {
		case vwoPhaseForm:
			return s.updateForm(m)
		case vwoPhaseConfirm:
			return s.updateConfirm(m)
		default:
			return s.updateSheet(m)
		}
	}

	if s.phase == vwoPhaseForm && s.focus < len(s.inputs) {
		var cmd tea.Cmd
		s.inputs[s.focus], cmd = s.inputs[s.focus].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *VendorWorkOrderDetailScreen) handleWrote(m vwoWroteMsg) (Screen, tea.Cmd) {
	s.sending = false
	if m.err != nil {
		// The prompt STAYS UP on a failure so the operator can fix what they
		// typed in place and send again — the same choice the vendor-promote
		// overlay makes. The headline rides the status row, bounded; the
		// server's own sentence is what it carries.
		s.errMsg = vwoReason(m.err)
		return s, Status(vwoSpecs[m.done].bar+" failed: "+s.errMsg, StatusError)
	}
	s.errMsg = ""
	done := vwoSpecs[m.done].bar
	s.closePrompt()
	if m.refreshed != nil {
		// Every action but authorize-emergency answers with the refreshed
		// order, so the sheet is correct before the reload lands.
		s.wo = m.refreshed
	}
	s.loading = true
	s.note = ""
	return s, tea.Batch(Status(done+" done", StatusOK), s.load())
}

// closePrompt returns to the sheet and blurs whatever box had the caret, so a
// caret is never left blinking in a field whose contents have already gone.
func (s *VendorWorkOrderDetailScreen) closePrompt() {
	for i := range s.inputs {
		s.inputs[i].Blur()
	}
	s.phase = vwoPhaseSheet
	s.action = vwoNone
	s.inputs = nil
	s.focus = 0
	s.confirmScroll = 0
}

// --- the sheet -------------------------------------------------------------

func (s *VendorWorkOrderDetailScreen) updateSheet(m tea.KeyMsg) (Screen, tea.Cmd) {
	key := m.String()
	switch key {
	case "r":
		s.loading = true
		s.loadErr = ""
		// The last write's failure headline goes with the refresh. It survives
		// an Esc off the confirm on purpose — StatusBar.Flash expires after
		// four seconds and the operator who looked away is still owed the
		// reason — but a failure standing over a sheet that has just been
		// re-read from the server is a claim about a state that no longer
		// exists, which is the same staleness from the other side.
		s.errMsg = ""
		s.note = ""
		return s, s.load()
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.sheetHeader()), s.sheetBar()) {
			// Refused pane: the sheet is not drawn, so moving the window would
			// move the operator's place invisibly and a terminal grown back
			// would open somewhere they never scrolled to. An arm whose whole
			// product is a POSITION says nothing rather than declining aloud.
			return s, nil
		}
		if !s.sheetScrolls() {
			s.note = key + " moves nothing — the whole sheet is on the pane."
			return s, nil
		}
		s.sheetScroll = jdeScrollStep(key, s.sheetScroll, s.sheetBody().Len(),
			s.scrollRows(len(s.sheetHeader()), s.sheetBar()))
		return s, nil
	}
	// An action key. vwoOffered is the same predicate the bar read, so a key
	// the bar named opens something and a key it did not name falls through to
	// the answer below.
	for a := vwoNone + 1; a < vwoActionCount; a++ {
		if vwoSpecs[a].key == key && s.vwoOffered(a) {
			return s, s.openAction(a)
		}
	}
	// Every other key ANSWERS. This frame holds no cursor and no caret, so a
	// silent return redraws a pane that is a pure function of unchanged state —
	// byte for byte identical, which is what reads as a wedged program. The
	// note says what the KEY DID and names none: the bar makes that claim,
	// where no budget can trim it.
	s.note = key + " does nothing on this work order."
	return s, nil
}

// openAction opens an action's form, or its confirm where it has no fields.
func (s *VendorWorkOrderDetailScreen) openAction(a vwoAction) tea.Cmd {
	spec := vwoSpecs[a]
	s.action = a
	s.errMsg = ""
	s.note = ""
	s.confirmScroll = 0
	s.focus = 0
	s.kindIx = 0
	s.inputs = nil
	if len(spec.fields) == 0 {
		s.phase = vwoPhaseConfirm
		return nil
	}
	s.phase = vwoPhaseForm
	s.inputs = make([]textinput.Model, len(spec.fields))
	for i, f := range spec.fields {
		in := textinput.New()
		// No placeholder: in a fixed-width columnar field a placeholder fills
		// the input area and hides the underscores that say the field is
		// empty, so what the field wants rides beside it as a Hint.
		in.Prompt = ""
		in.CharLimit = f.limit
		s.inputs[i] = in
	}
	s.focusRow(0)
	return textinput.Blink
}

func (s *VendorWorkOrderDetailScreen) focusRow(i int) {
	for j := range s.inputs {
		s.inputs[j].Blur()
	}
	s.focus = i
	if i < len(s.inputs) && !s.fieldAt(i).isChoice() {
		s.inputs[i].Focus()
	}
}

func (f vwoField) isChoice() bool { return len(f.choice) > 0 }

func (s *VendorWorkOrderDetailScreen) fields() []vwoField { return vwoSpecs[s.action].fields }

func (s *VendorWorkOrderDetailScreen) fieldAt(i int) vwoField {
	fs := s.fields()
	if i < 0 || i >= len(fs) {
		return vwoField{}
	}
	return fs[i]
}

// --- the form --------------------------------------------------------------

func (s *VendorWorkOrderDetailScreen) updateForm(m tea.KeyMsg) (Screen, tea.Cmd) {
	key := m.String()
	switch key {
	case "esc":
		s.note = ""
		s.closePrompt()
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if key == "shift+tab" || key == "up" {
			delta = -1
		}
		// A FIELD form WRAPS: these are three or four rows and the cursor
		// reaching the end has somewhere obvious to go. Paging is not offered
		// at all — jdePageCursor clamps, so PgDn on a form this short blurs and
		// re-focuses the same row, which is a named key that moves nothing.
		next, ok := s.moveRow(s.focus, len(s.fields()), delta, len(s.formHeader()), s.formBar())
		if !ok {
			return s, nil
		}
		s.focusRow(next)
		return s, textinput.Blink
	case "left", "right":
		f := s.fieldAt(s.focus)
		if !f.isChoice() {
			s.note = key + " moves nothing — this row is typed, not chosen."
			return s, nil
		}
		if key == "right" {
			s.kindIx = (s.kindIx + 1) % len(f.choice)
		} else {
			s.kindIx = (s.kindIx - 1 + len(f.choice)) % len(f.choice)
		}
		s.note = ""
		return s, nil
	case "enter":
		// ENTER DOES NOT WRITE. It reads the rows and moves to the confirm,
		// where Ctrl-X is what reaches the wire.
		if refusal, row := s.readForm(); refusal != "" {
			s.focusRow(row)
			s.note = refusal
			return s, textinput.Blink
		}
		s.note = ""
		s.confirmScroll = 0
		s.phase = vwoPhaseConfirm
		for i := range s.inputs {
			s.inputs[i].Blur()
		}
		return s, nil
	}
	if s.focus < len(s.inputs) && !s.fieldAt(s.focus).isChoice() {
		var cmd tea.Cmd
		s.inputs[s.focus], cmd = s.inputs[s.focus].Update(m)
		return s, cmd
	}
	return s, nil
}

// readForm judges every row and returns the first refusal with the row to put
// the caret back on, or "" when the form is ready to confirm.
//
// This is FIELD PARSING of local boxes and nothing more. Whether the order is
// in the right state, whether the actor may act and what an amount is allowed
// to be are the SERVER's to refuse, and none of them is duplicated here. What
// IS judged here is what the server can only answer with a 500-shaped surprise
// or a silent coercion: a blank required row, and a "money" row that is not a
// plain decimal.
func (s *VendorWorkOrderDetailScreen) readForm() (string, int) {
	for i, f := range s.fields() {
		if f.isChoice() {
			continue
		}
		raw := strings.TrimSpace(s.inputs[i].Value())
		if raw == "" {
			if f.required {
				return f.label + " is required — nothing was sent.", i
			}
			continue
		}
		if f.money && !vwoPlainDecimal(raw) {
			return f.label + " must be a plain amount like 1200.00 — nothing was sent.", i
		}
		if f.label == "File path" {
			if refusal := vwoCheckFile(raw); refusal != "" {
				return refusal, i
			}
		}
	}
	return "", 0
}

// vwoPlainDecimal judges a row as MONEY, which big.Rat.SetString does not: it
// is a number parser and accepts "1/3", "1e9" and "-5", and this screen posts
// the row VERBATIM. A mis-keyed leading minus would otherwise reach the server
// as a negative NTE or a negative invoice — the first of which its
// MinValueValidator refuses with a DRF field envelope this screen would show as
// raw JSON, and the second of which it would score a variance against.
func vwoPlainDecimal(v string) bool {
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

// vwoCheckFile is the upload row's local judgement: the bytes are read on this
// machine, so an unreadable path is this side's refusal to make rather than a
// request to send and have fail.
func vwoCheckFile(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "cannot read that file: " + err.Error()
	}
	if info.IsDir() {
		return "that path is a directory, not a file — nothing was sent."
	}
	return ""
}

// --- the confirm -----------------------------------------------------------

func (s *VendorWorkOrderDetailScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	key := m.String()
	switch key {
	case "ctrl+x":
		if s.sending {
			// A write is already out. The status row draws the working line
			// and the bar has dropped the key (confirmCommits, the one
			// expression both read), so the frame has answered already.
			return s, nil
		}
		s.sending = true
		s.note = ""
		s.errMsg = ""
		return s, s.send()
	case "esc":
		// Esc LEAVES even while the write is out — a frame with no way off it
		// while a slow gateway thinks is the worse defect, and it is what the
		// purchasing confirms do. It does not cancel the request: the write
		// may still land with nobody watching, which is why the sheet reloads
		// when the answer comes back wherever the operator has gone.
		s.note = ""
		if len(s.fields()) > 0 && !s.sending {
			// Back to the form, with what was typed still in the boxes.
			s.phase = vwoPhaseForm
			s.focusRow(s.focus)
			return s, textinput.Blink
		}
		s.closePrompt()
		return s, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.confirmHeader()), s.confirmBar()) {
			return s, nil
		}
		if !s.confirmScrolls() {
			s.note = key + " moves nothing — the whole prompt is on the pane."
			return s, nil
		}
		s.confirmScroll = jdeScrollStep(key, s.confirmScroll, s.confirmBody().Len(),
			s.scrollRows(len(s.confirmHeader()), s.confirmBar()))
		return s, nil
	}
	s.note = key + " does nothing here — this frame only commits or cancels."
	return s, nil
}

// value returns the typed row i, trimmed. Rows are posted verbatim from here.
func (s *VendorWorkOrderDetailScreen) value(i int) string {
	if i < 0 || i >= len(s.inputs) {
		return ""
	}
	return strings.TrimSpace(s.inputs[i].Value())
}

// send fires the pending action. It closes over everything it needs BEFORE the
// command runs, so a later keypress cannot change what was confirmed.
func (s *VendorWorkOrderDetailScreen) send() tea.Cmd {
	deps, ctx, id, a := s.deps, s.ctx(), s.id, s.action
	switch a {
	case vwoSetNTE:
		amount := s.value(0)
		return func() tea.Msg {
			wo, err := deps.OMS.SetMaintenanceOrderNTE(ctx, id, amount)
			return vwoWroteMsg{done: a, refreshed: wo, err: err}
		}
	case vwoAuthorizeEmergency:
		reason := s.value(0)
		return func() tea.Msg {
			// The ONE action that answers with something other than the order
			// (the 24-hour authorization row), so there is nothing to seat the
			// sheet from and the reload is what refreshes it.
			_, err := deps.OMS.AuthorizeMaintenanceOrderEmergency(ctx, id, reason)
			return vwoWroteMsg{done: a, err: err}
		}
	case vwoAdvanceSourcing:
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.AdvanceMaintenanceOrderToSourcing(ctx, id)
		})
	case vwoAddQuote:
		amount, notes, vendor := s.value(0), s.value(1), ""
		if s.wo != nil {
			vendor = s.wo.Vendor
		}
		return func() tea.Msg {
			_, err := deps.OMS.AddMaintenanceOrderQuote(ctx, id, vendor, amount, notes)
			return vwoWroteMsg{done: a, err: err}
		}
	case vwoWaiveQuotes:
		reason := s.value(0)
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.WaiveMaintenanceOrderQuoteRequirement(ctx, id, reason)
		})
	case vwoAdvanceScheduled:
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.AdvanceMaintenanceOrderToScheduled(ctx, id)
		})
	case vwoVendorArrived:
		keyfob := s.value(0)
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.MarkMaintenanceOrderVendorArrived(ctx, id, keyfob)
		})
	case vwoSignOff:
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.SignOffMaintenanceOrder(ctx, id)
		})
	case vwoKeyfobReturn:
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.RecordMaintenanceOrderKeyfobReturn(ctx, id)
		})
	case vwoAdvanceFinancial:
		invoice, fee := s.value(0), s.value(1)
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.AdvanceMaintenanceOrderToFinancialReview(ctx, id, invoice, fee)
		})
	case vwoOverrideVariance:
		reason := s.value(0)
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.OverrideMaintenanceOrderVariance(ctx, id, reason)
		})
	case vwoClose:
		return vwoCmd(a, func() (*omsapi.MaintenanceOrder, error) {
			return deps.OMS.CloseMaintenanceOrder(ctx, id)
		})
	case vwoAttach:
		path, caption := s.value(0), s.value(2)
		kind := omsapi.MaintenanceOrderAttachmentKinds[s.kindIx]
		return func() tea.Msg {
			f, err := os.Open(path)
			if err != nil {
				return vwoWroteMsg{done: a, err: err}
			}
			defer f.Close()
			_, err = deps.OMS.UploadMaintenanceOrderAttachment(
				ctx, id, filepath.Base(path), f, kind, caption)
			return vwoWroteMsg{done: a, err: err}
		}
	}
	return nil
}

// vwoCmd wraps the actions that answer with the refreshed order.
func vwoCmd(a vwoAction, call func() (*omsapi.MaintenanceOrder, error)) tea.Cmd {
	return func() tea.Msg {
		wo, err := call()
		return vwoWroteMsg{done: a, refreshed: wo, err: err}
	}
}

// vwoReason turns an error into the sentence the operator reads.
//
// The maintenance_orders views write `{"detail": ...}` by hand and never reach
// DRF's exception handler, so omsapi.parseError hands the WHOLE RAW BODY over:
// without this the bench reads `oms: http 400: {"detail": "Work order is in
// 'requested' state; expected 'sourcing'."}` on a row that cannot fold.
// AsMaintenanceOrderRefusal is deliberately narrow, so a gateway page and a
// field-validation envelope keep the shape they arrived in.
func vwoReason(err error) string {
	if err == nil {
		return ""
	}
	if sentence, ok := omsapi.AsMaintenanceOrderRefusal(err); ok {
		return sentence
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *VendorWorkOrderDetailScreen) View() string {
	switch s.phase {
	case vwoPhaseForm:
		return s.viewForm()
	case vwoPhaseConfirm:
		return s.viewConfirm()
	}
	return s.viewSheet()
}

// vwoChoiceCells is what the layer's `< value >` wrapper costs a choice row
// beyond the value itself (jdeFieldArea).
const vwoChoiceCells = 4

// vwoLabelW is the label column every value row on this screen shares, so the
// money block and the gate block line up under one leader. It is a CEILING
// rather than a constant: the blocks are built separately and a column measured
// per block would step between them, but a column wider than the pane can hold
// is not a column at all.
const vwoLabelW = 13

// vwoLabelWidth is that ceiling narrowed to what the pane really has.
//
// A jdeValue row is indent + label + leader + value, and jdeStripWidth answers
// ZERO when the label column and the leader have already spent the pane — which
// every clip in this layer reads as "do not truncate". At a terminal width of
// 51 the pane is 22 cells and the fixed column left exactly nothing, so the
// Title row went out at its full 70 cells and clampToBox took 48 of them with
// no mark. Root draws from a width of 45 (a pane of 16), which is the narrowest
// this has to answer for.
func vwoLabelWidth(bodyWidth int) int {
	if bodyWidth <= 0 {
		return vwoLabelW
	}
	// One cell for the value, or the row says nothing at all.
	room := bodyWidth - (len(jdeIndent) + len(jdeLeader) + 1)
	switch {
	case room < 1:
		return 1
	case room < vwoLabelW:
		return room
	}
	return vwoLabelW
}

// vwoRow is one columnar value row, clipped to what the label column leaves and
// marked when it is clipped.
func vwoRow(label, value string, dim bool, bodyWidth int) string {
	labelW := vwoLabelWidth(bodyWidth)
	room := jdeStripWidth(bodyWidth, labelW)
	if bodyWidth > 0 && room < 1 {
		room = 1
	}
	return renderJDEField(jdeField{
		Label: label, Kind: jdeValue, Dim: dim,
		Value: fitCellIf(value, room),
	}, labelW, bodyWidth)
}

// vwoMoneyRow draws a money figure, or "(none)" where the server recorded
// nothing.
//
// Empty() is "null/unset" and is NOT zero: a null NTE is an emergency order
// with no ceiling at all, and a recorded 0.00 is a ceiling of nothing. Drawing
// "$0.00" for the first would be stating a fact the server did not.
func vwoMoneyRow(label string, d omsapi.DecimalString, bodyWidth int) string {
	if d.Empty() {
		return vwoRow(label, "(none)", true, bodyWidth)
	}
	return vwoRow(label, vwoMoney(d), false, bodyWidth)
}

// vwoMoney renders a decimal money string at two places, EXACTLY.
//
// big.Rat and not float64: this is money, and the figures beside it are what a
// vendor is paid. A value the parser cannot read is passed through verbatim
// rather than silently becoming zero — an unparseable figure is the server's
// own digits and the operator should see them, not a number this side invented.
func vwoMoney(d omsapi.DecimalString) string {
	raw := strings.TrimSpace(string(d))
	if raw == "" {
		return ""
	}
	r, ok := new(big.Rat).SetString(raw)
	if !ok {
		return raw
	}
	return "$" + r.FloatString(2)
}

// vwoDelta is b − a at two places, computed in big.Rat. ok is false where
// either side is missing or unreadable, so a caller draws nothing rather than a
// difference against a number it guessed.
func vwoDelta(a, b omsapi.DecimalString) (string, bool) {
	ra, oka := new(big.Rat).SetString(strings.TrimSpace(string(a)))
	rb, okb := new(big.Rat).SetString(strings.TrimSpace(string(b)))
	if !oka || !okb {
		return "", false
	}
	diff := new(big.Rat).Sub(rb, ra)
	sign := ""
	if diff.Sign() > 0 {
		sign = "+"
	}
	return sign + "$" + diff.FloatString(2), true
}

// vwoIDLines draws the order's UUID WHOLE, across as many lines as the pane
// needs, and never clips or abbreviates it.
//
// A UUID is 36 cells and a labelled row at 80 columns leaves 34, so the
// identifier does not fit beside its own label. The answer is to change the
// LAYOUT rather than the identifier: the label takes a row of its own and the
// id sits under it, indented. Where even that will not do — the narrowest pane
// Root draws leaves 16 cells — it is packed across lines at the hyphen
// boundaries, trailing hyphens kept so a reader can see the joins, because a
// UUID an operator retypes has to be the id and not a prefix of it.
func vwoIDLines(id string, bodyWidth int) []string {
	const indent = "    "
	room := bodyWidth - len(indent)
	if bodyWidth <= 0 || room >= lipgloss.Width(id) {
		return []string{indent + id}
	}
	if room < 1 {
		room = 1
	}
	var out []string
	groups := strings.Split(id, "-")
	line := ""
	for i, g := range groups {
		piece := g
		if i < len(groups)-1 {
			piece += "-"
		}
		switch {
		case line == "":
			line = piece
		case lipgloss.Width(line)+lipgloss.Width(piece) <= room:
			line += piece
		default:
			out = append(out, indent+line)
			line = piece
		}
		// A group that alone outruns the room is still emitted whole rather
		// than cut: an over-long line is a legibility cost, a cut id is a wrong
		// id.
	}
	if line != "" {
		out = append(out, indent+line)
	}
	return out
}

// --- the sheet -------------------------------------------------------------

// sheetHeader is what the sheet may not be drawn without.
//
// The IDENTITY row is essential: a frame that offers Close, Override and
// Emergency has to say which order it is about at every height the frame is
// drawn at, and jdeFitHeader keeps the essential row last. The blocked reason
// is CONTEXT — an operator who cannot see it can still reach it by growing the
// terminal, and the key it names is not on the bar to be pressed by mistake
// (vwoOffered drops a gated transition from both).
func (s *VendorWorkOrderDetailScreen) sheetHeader() jdeHeader {
	h := jdeHeader(nil)
	if s.wo == nil {
		// Bounded against the LIVE pane, not written short and hoped for:
		// jdeFitHeader trims by ROW and does no width fitting at all, so an
		// over-wide essential row is cut by clampToBox from the right with no
		// mark and its closing SGR reset goes with it. At a terminal width of
		// 59 the pane is 30 cells and the failure sentence is 31.
		if s.loadErr != "" {
			return h.add(jdeHeadEssential, StyleStatusError.Render(
				fitCellIf("Could not load this work order.", s.bodyWidth())))
		}
		return h.add(jdeHeadEssential, StyleMuted.Render(
			fitCellIf("Loading the work order…", s.bodyWidth())))
	}
	h = h.add(jdeHeadEssential, StyleJDEHeading.Render(vwoIdentityLine(s.wo, s.bodyWidth())))
	if blocked := s.vwoBlockedBy(vwoAdvanceFor(s.wo.Status)); blocked != "" {
		for _, line := range jdeCaveatLines(blocked, s.bodyWidth()) {
			h = h.add(jdeHeadContext, StyleStatusWarn.Render(line))
		}
	}
	return h.add(jdeHeadDecorative, "")
}

// vwoIdentityLine is the one row that says WHICH order and WHERE it is. The
// short id leads because it is the handle the audit log and the recovery tasks
// use, then the status, then the emergency mark — which is not a status and is
// what says the money gates were bypassed.
func vwoIdentityLine(wo *omsapi.MaintenanceOrder, bodyWidth int) string {
	parts := []string{wo.ShortID}
	if label := wo.StatusDisplay; label != "" {
		parts = append(parts, label)
	} else {
		parts = append(parts, wo.Status)
	}
	if wo.IsEmergency {
		parts = append(parts, "EMERGENCY")
	}
	return fitCellIf(strings.Join(parts, " · "), bodyWidth)
}

// sheetBody is the sheet itself, in blocks: what this order IS, what it COSTS,
// which gates are open, and the paperwork behind them.
//
// It owns NO navigable row on purpose — nothing here is chosen or typed — so
// the window over it is an OFFSET and every line is reachable by scrolling.
func (s *VendorWorkOrderDetailScreen) sheetBody() *jdeLines {
	l := &jdeLines{}
	wo := s.wo
	w := s.bodyWidth()
	if wo == nil {
		return l
	}

	l.Add(StyleMuted.Render("Order id"))
	for _, line := range vwoIDLines(wo.ID, w) {
		l.Add(line)
	}
	l.Add(vwoRow("Title", wo.Title, wo.Title == "", w))
	l.Add(vwoRow("Work type", firstNonEmpty(wo.WorkTypeDisplay, wo.WorkType), false, w))
	l.Add(vwoRow("Vendor", firstNonEmpty(wo.VendorName, "(unnamed)"), wo.VendorName == "", w))
	switch {
	case wo.AssetName != "":
		l.Add(vwoRow("Asset", wo.AssetName, false, w))
	case wo.LocationName != "":
		l.Add(vwoRow("Location", wo.LocationName, false, w))
	default:
		l.Add(vwoRow("Asset", "(none)", true, w))
	}
	if !wo.OpenedAt.IsZero() {
		l.Add(vwoRow("Opened", wo.OpenedAt.Format("2006-01-02 15:04"), false, w))
	}
	if wo.ClosedAt != nil {
		l.Add(vwoRow("Closed", wo.ClosedAt.Format("2006-01-02 15:04"), false, w))
	}
	if wo.WarrantyRecovery {
		l.Add(jdeIndent + StyleStatusWarn.Render(fitCellIf(
			"Warranty recovery — costs may be reclaimable from the provider.", w-len(jdeIndent))))
	}

	l.Add("")
	l.Add(StyleJDEHeading.Render("Money"))
	l.Add(vwoMoneyRow("NTE", wo.NTEAmount, w))
	l.Add(vwoMoneyRow("Par buffer", wo.ParCostBuffer, w))
	l.Add(vwoMoneyRow("Invoice", wo.ActualInvoiceTotal, w))
	l.Add(vwoMoneyRow("Dispatch fee", wo.DispatchFee, w))
	if delta, ok := vwoDelta(wo.NTEAmount, wo.ActualInvoiceTotal); ok {
		l.Add(vwoRow("Over NTE", delta, false, w))
	}
	l.Add(vwoRow("Variance", vwoVarianceText(wo.VarianceStatus),
		wo.VarianceStatus == "", w))

	l.Add("")
	l.Add(StyleJDEHeading.Render("Gates"))
	wf := wo.Workflow
	l.Add(vwoRow("NTE set", jdeYesNo(wf.HasNTE), !wf.HasNTE, w))
	l.Add(vwoRow("Quotes", fmt.Sprintf("%d on file · requirement %s",
		wf.QuoteCount, vwoMet(wf.HasRequiredQuotes)), !wf.HasRequiredQuotes, w))
	l.Add(vwoRow("Photo", jdeYesNo(wf.HasPhotoEvidence), !wf.HasPhotoEvidence, w))
	l.Add(vwoRow("Invoice+FSR", jdeYesNo(wf.HasInvoiceAndFSR), !wf.HasInvoiceAndFSR, w))
	l.Add(vwoRow("Emergency", jdeYesNo(wf.HasActiveEmergencyAuthorization),
		!wf.HasActiveEmergencyAuthorization, w))
	switch {
	case wf.KeyfobOutstanding:
		l.Add(vwoRow("Keyfob", wo.KeyfobID+" — still out", false, w))
	case wo.KeyfobID != "":
		l.Add(vwoRow("Keyfob", wo.KeyfobID+" — returned", false, w))
	}
	if wo.QuoteWaiverSignedAt != nil {
		l.Add(vwoRow("Waiver", wo.QuoteWaiverSignedAt.Format("2006-01-02"), false, w))
		for _, line := range jdeCaveatLines(wo.QuoteWaiverReason, w) {
			l.Add(line)
		}
	}

	if len(wo.Quotes) > 0 {
		l.Add("")
		l.Add(StyleJDEHeading.Render(fmt.Sprintf("Quotes (%d)", len(wo.Quotes))))
		for i, q := range wo.Quotes {
			l.Add(vwoRow(fmt.Sprintf("%d.", i+1),
				firstNonEmpty(q.VendorName, "(vendor)")+"  "+vwoMoney(q.Amount), false, w))
		}
	}
	if len(wo.Attachments) > 0 {
		l.Add("")
		l.Add(StyleJDEHeading.Render(fmt.Sprintf("Attachments (%d)", len(wo.Attachments))))
		for i, a := range wo.Attachments {
			label := firstNonEmpty(a.Caption, filepath.Base(a.File))
			l.Add(vwoRow(fmt.Sprintf("%d.", i+1),
				firstNonEmpty(a.KindDisplay, a.Kind)+"  "+label, false, w))
		}
	}
	if len(wo.AssetLinks) > 0 {
		l.Add("")
		l.Add(StyleJDEHeading.Render("Cost split"))
		for _, link := range wo.AssetLinks {
			share := "—"
			if !link.SharePct.Empty() {
				share = string(link.SharePct) + "%"
			}
			cost := "(unallocated)"
			if !link.AllocatedCost.Empty() {
				cost = vwoMoney(link.AllocatedCost)
			}
			l.Add(vwoRow(firstNonEmpty(link.AssetTag, "asset"),
				firstNonEmpty(link.AssetName, link.Asset)+"  "+share+"  "+cost, false, w))
		}
	}

	if wo.TotalDowntime != "" || wo.DowntimeStart != nil {
		l.Add("")
		l.Add(StyleJDEHeading.Render("Downtime"))
		if wo.DowntimeStart != nil {
			l.Add(vwoRow("Started", wo.DowntimeStart.Format("2006-01-02 15:04"), false, w))
		}
		if wo.DowntimeEnd != nil {
			l.Add(vwoRow("Ended", wo.DowntimeEnd.Format("2006-01-02 15:04"), false, w))
		}
		if wo.TotalDowntime != "" {
			// Verbatim: this is the figure the server stamped at sign-off, and
			// re-deriving it from the two timestamps would disagree with the
			// record whenever they were adjusted.
			l.Add(vwoRow("Total", wo.TotalDowntime, false, w))
		}
	}

	for _, block := range []struct{ head, text string }{
		{"Notes", wo.Notes},
		{"Internal notes", wo.InternalNotes},
	} {
		if strings.TrimSpace(block.text) == "" {
			continue
		}
		l.Add("")
		l.Add(StyleJDEHeading.Render(block.head))
		for _, line := range jdeCaveatLines(block.text, w) {
			l.Add(line)
		}
	}
	return l
}

// vwoVarianceText names the three variance states in the operator's terms. ""
// is NOT "approved": it means the reconciliation could not be scored at all,
// which is what an emergency order with no NTE gets, and reading it as approval
// would be inventing a verdict.
func vwoVarianceText(status string) string {
	switch status {
	case "auto_approved":
		return "within budget"
	case omsapi.MaintenanceOrderVarianceBlocked:
		return "OVER BUDGET — blocks closure"
	}
	return "not scored"
}

func vwoMet(ok bool) string {
	if ok {
		return "met"
	}
	return "not met"
}

// sheetScrolls is the sheet's half of the bar-honesty rule: the scroll keys are
// named when, and only when, the body outruns the window.
//
// Measured against the bar that will really be DRAWN with the scroll keys on
// it, because THAT is the fixed point: naming them costs cells, cells can fold
// the bar onto another row, and a folded bar leaves the body one row fewer.
func (s *VendorWorkOrderDetailScreen) sheetScrolls() bool {
	return s.bodyScrollsForBar(s.sheetBody(), len(s.sheetHeader()), s.sheetBarItems(true))
}

func (s *VendorWorkOrderDetailScreen) sheetBar() []actionBarItem {
	return s.sheetBarItems(s.sheetScrolls())
}

// sheetBarItems is the sheet's bar for a given scroll state, so the bar that is
// MEASURED against the pane is the bar that is DRAWN on it.
//
// It names exactly what vwoOffered allows, in the order an operator meets them:
// the next step first, the way out second, then the side actions in the order
// the workflow reaches them, then movement, then refresh.
func (s *VendorWorkOrderDetailScreen) sheetBarItems(scroll bool) []actionBarItem {
	items := []actionBarItem{}
	if a := vwoAdvanceFor(s.status()); s.vwoOffered(a) {
		items = append(items, actionBarItem{"Enter", vwoSpecs[a].bar})
	}
	items = append(items, actionBarItem{"Esc", "Back"})
	for _, a := range []vwoAction{
		vwoSetNTE, vwoAddQuote, vwoWaiveQuotes, vwoAuthorizeEmergency,
		vwoAttach, vwoKeyfobReturn, vwoOverrideVariance,
	} {
		if s.vwoOffered(a) {
			items = append(items, actionBarItem{vwoSpecs[a].key, vwoSpecs[a].bar})
		}
	}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

func (s *VendorWorkOrderDetailScreen) status() string {
	if s.wo == nil {
		return ""
	}
	return s.wo.Status
}

// sheetStatus is the sheet's status row: what is in flight, what failed, or
// what the last keypress DID.
//
// A WRITE wins over a load when both are out — they overlap when `r` is pressed
// while an action is running — because of the two facts the one an operator
// would act on is the one that changes the order.
func (s *VendorWorkOrderDetailScreen) sheetStatus() string {
	switch {
	case s.sending:
		return s.statusRow(true, vwoSpecs[s.action].verb, "")
	case s.loading:
		return s.statusRow(true, "Loading the work order…", "")
	case s.errMsg != "":
		return s.statusRow(false, "", s.errMsg)
	case s.loadErr != "":
		return s.statusRow(false, "", s.loadErr)
	}
	return s.statusAnswer(StatusInfo, s.note)
}

func (s *VendorWorkOrderDetailScreen) viewSheet() string {
	frame, offset := s.frameScrolled(s.sheetHeader(), s.sheetBody(), s.sheetScroll,
		s.sheetStatus(), s.sheetBar())
	// Stored back so the offset this screen holds is the one that was DRAWN:
	// `end` asks for the whole body and the frame clamps it against the pane it
	// has, which is what makes "↓ 0 more below" impossible.
	s.sheetScroll = offset
	return frame
}

// --- the form --------------------------------------------------------------

// formBar is the form's bar, said ONCE: the movement arm asks the layer whether
// the frame is drawn before it moves the caret, and a second literal beside the
// view's would be a bar measured that is not the bar drawn.
//
// Enter says CONTINUE and not the action's own name, because it does not write:
// a key labelled "Close" that opens a prompt would be the very reflex this
// screen is built to prevent.
func (s *VendorWorkOrderDetailScreen) formBar() []actionBarItem {
	items := []actionBarItem{{"Enter", "Continue"}, {"Esc", "Cancel"}}
	if jdeRowMoves(len(s.fields())) {
		items = append(items, actionBarItem{"UP/DN", "Fields"})
	}
	for _, f := range s.fields() {
		if f.isChoice() {
			items = append(items, actionBarItem{"←→", "Choose"})
			break
		}
	}
	return items
}

// formHeader names the action and the order it is about. The ORDER is
// essential: a money prompt that does not say which work order it is about is
// the one thing a short pane may not take away.
func (s *VendorWorkOrderDetailScreen) formHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative,
		StyleJDEHeading.Render(fitCellIf(vwoSpecs[s.action].title, s.bodyWidth())), "")
	return h.add(jdeHeadEssential, vwoRow("Work order", s.shortID(), false, s.bodyWidth())).
		add(jdeHeadDecorative, "")
}

func (s *VendorWorkOrderDetailScreen) shortID() string {
	if s.wo == nil {
		return s.id
	}
	return firstNonEmpty(s.wo.ShortID, s.wo.ID)
}

func (s *VendorWorkOrderDetailScreen) viewForm() string {
	fs := s.fields()
	fields := make([]jdeField, len(fs))
	for i, f := range fs {
		row := jdeField{Label: f.label, Hint: f.hint, Focused: s.focus == i}
		if f.isChoice() {
			row.Kind = jdeChoice
			row.Value = f.choice[s.kindIx]
		} else {
			row.Kind = jdeText
			row.Input = &s.inputs[i]
			row.Width = 26
		}
		fields[i] = row
	}
	// A jdeChoice row draws its value at whatever width the value IS —
	// jdePaneFieldWidth caps only a TEXT row's input area, and says in as many
	// words that shortening a choice value is the sheet's own content decision.
	// Left unclipped, `Kind ..... < invoice >` ran past the pane from a terminal
	// width of 57 down, where clampToBox takes the tail with no mark.
	//
	// Clipping the value alone is not enough, which is the assetScopeRows rule:
	// a bound applied to one PART of a row that is afterwards added to is not a
	// bound. The layer wraps a choice in `< ` and ` >`, so those four cells plus
	// one for the value are what the LABEL COLUMN has to leave — and at a
	// terminal width of 51 the pane is 22 cells, where the column measured from
	// the labels alone left four.
	labelW := jdeLabelWidth(fields)
	if w := s.bodyWidth(); w > 0 {
		floor := 1
		for _, f := range fs {
			if f.isChoice() {
				floor = vwoChoiceCells + 1
				break
			}
		}
		if cap := w - (len(jdeIndent) + len(jdeLeader) + floor); labelW > cap {
			labelW = cap
		}
		if labelW < 1 {
			labelW = 1
		}
	}
	for i, f := range fs {
		if !f.isChoice() {
			continue
		}
		room := jdeStripWidth(s.bodyWidth(), labelW) - vwoChoiceCells
		if room < 1 {
			room = 1
		}
		fields[i].Value = fitCellIf(f.choice[s.kindIx], room)
	}
	body := &jdeLines{}
	body.AddFittedFields(fields, labelW, s.bodyWidth(), 0)
	return s.frameWrapped(s.formHeader(), body, s.focus,
		s.statusAnswer(StatusInfo, s.note), s.formBar())
}

// --- the confirm -----------------------------------------------------------

// confirmCommits is the ONE expression behind "may Ctrl-X write". The bar reads
// it to decide whether to name the key and the arm reads it to decide whether
// to act — two copies of that condition is how a sibling confirm's bar and arm
// came apart, with the bar naming a key the handler had stopped honouring.
func (s *VendorWorkOrderDetailScreen) confirmCommits() bool { return !s.sending }

func (s *VendorWorkOrderDetailScreen) confirmScrolls() bool {
	return s.bodyScrollsForBar(s.confirmBody(), len(s.confirmHeader()),
		s.confirmBarItems(s.confirmCommits(), true))
}

func (s *VendorWorkOrderDetailScreen) confirmBar() []actionBarItem {
	return s.confirmBarItems(s.confirmCommits(), s.confirmScrolls())
}

func (s *VendorWorkOrderDetailScreen) confirmBarItems(commit, scroll bool) []actionBarItem {
	items := []actionBarItem{{"Ctrl-X", vwoSpecs[s.action].bar}, {"Esc", "Cancel"}}
	if !commit {
		// Esc still LEAVES while the write is out — a frame with no way off it
		// is the worse defect — so the bar keeps one key and drops the one that
		// would not act.
		items = []actionBarItem{{"Esc", "Back"}}
	}
	if scroll {
		items = append(items,
			actionBarItem{"UP/DN", "Scroll"},
			actionBarItem{"PgUp/PgDn", "Page"},
			actionBarItem{"Home/End", "Top/End"})
	}
	return items
}

// confirmHeader is what the confirm may not be drawn without: WHICH order, and
// what is about to happen to it.
//
// The order row is essential and the heading is decorative, for the reason the
// attachment confirm's file row is: jdeFitHeader gives ground by RANK and keeps
// the essential row last, so wherever this frame is drawn at all it names the
// work order Ctrl-X is about to change.
func (s *VendorWorkOrderDetailScreen) confirmHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative,
		StyleStatusWarn.Render(fitCellIf(vwoSpecs[s.action].title, s.bodyWidth())), "")
	return h.add(jdeHeadEssential, vwoRow("Work order", s.shortID(), false, s.bodyWidth())).
		add(jdeHeadDecorative, "")
}

// confirmBody is WHAT IS BEING COMMITTED, then what it does.
//
// The FIGURES LEAD. This is the one frame between an operator and a write that
// spends the makerspace's money, and a caveat read above the amount is a caveat
// read about a number the reader has not seen yet; a short pane keeps a body's
// START, so leading with the values is what puts them on the pane at every
// height the frame is drawn at.
func (s *VendorWorkOrderDetailScreen) confirmBody() *jdeLines {
	l := &jdeLines{}
	w := s.bodyWidth()
	for _, row := range s.confirmValues() {
		l.Add(vwoRow(row.label, row.value, row.dim, w))
	}
	if len(s.confirmValues()) > 0 {
		l.Add("")
	}
	for _, line := range jdeCaveatLines(vwoSpecs[s.action].caveat, w) {
		l.Add(line)
	}
	return l
}

type vwoConfirmRow struct {
	label, value string
	dim          bool
}

// confirmValues is what this Ctrl-X will send, in the operator's terms.
//
// A row is drawn for every field the action has, INCLUDING the blank optional
// ones: "Reason ..... (none)" is the difference between an emergency
// authorization whose reason nobody wrote and one whose reason scrolled off a
// pane. An absence stated is not the same as an absence unmentioned.
//
// The money rows carry the DERIVED difference beside them where both figures
// are known, because that is the number the operator is actually deciding
// about. It is the raw subtraction and NOT a verdict: whether it is approved is
// the server's to score against the par buffer and the 15%-of-NTE cap, and a
// client that guessed would eventually guess differently from it.
func (s *VendorWorkOrderDetailScreen) confirmValues() []vwoConfirmRow {
	fs := s.fields()
	rows := make([]vwoConfirmRow, 0, len(fs)+2)
	for i, f := range fs {
		switch {
		case f.isChoice():
			rows = append(rows, vwoConfirmRow{f.label, f.choice[s.kindIx], false})
			continue
		}
		raw := s.value(i)
		if raw == "" {
			rows = append(rows, vwoConfirmRow{f.label, "(none)", true})
			continue
		}
		if f.money {
			raw = vwoMoney(omsapi.DecimalString(raw))
		}
		rows = append(rows, vwoConfirmRow{f.label, raw, false})
	}
	if s.wo == nil {
		return rows
	}
	switch s.action {
	case vwoSetNTE:
		if !s.wo.NTEAmount.Empty() {
			rows = append(rows, vwoConfirmRow{"Replaces", vwoMoney(s.wo.NTEAmount), false})
		}
	case vwoAdvanceFinancial:
		rows = append(rows, vwoConfirmRow{"NTE on file", vwoMoneyText(s.wo.NTEAmount), s.wo.NTEAmount.Empty()})
		if delta, ok := vwoDelta(s.wo.NTEAmount, omsapi.DecimalString(s.value(0))); ok {
			rows = append(rows, vwoConfirmRow{"Over NTE", delta, false})
		}
	case vwoOverrideVariance:
		rows = append(rows,
			vwoConfirmRow{"NTE on file", vwoMoneyText(s.wo.NTEAmount), s.wo.NTEAmount.Empty()},
			vwoConfirmRow{"Invoice", vwoMoneyText(s.wo.ActualInvoiceTotal), s.wo.ActualInvoiceTotal.Empty()})
		if delta, ok := vwoDelta(s.wo.NTEAmount, s.wo.ActualInvoiceTotal); ok {
			rows = append(rows, vwoConfirmRow{"Over NTE", delta, false})
		}
	case vwoClose:
		rows = append(rows, vwoConfirmRow{"Invoice", vwoMoneyText(s.wo.ActualInvoiceTotal), s.wo.ActualInvoiceTotal.Empty()})
	case vwoAddQuote:
		rows = append(rows, vwoConfirmRow{"Vendor", firstNonEmpty(s.wo.VendorName, s.wo.Vendor), false})
	}
	return rows
}

// vwoMoneyText is vwoMoney with the absence named rather than left blank.
func vwoMoneyText(d omsapi.DecimalString) string {
	if d.Empty() {
		return "(none)"
	}
	return vwoMoney(d)
}

// confirmStatus is the confirm's one status row: what is in flight, what the
// last write failed with, and what the last keypress DID.
//
// The ANSWER LEADS the working line rather than replacing it, through the same
// poLeadOnto the sibling confirms use: statusRow's saving branch wins outright,
// so a note handed to it while a write is out would be drawn by nothing at all —
// and every key this frame declines is still pressable while the write is in
// flight, which is exactly when a swallowed answer reads as a wedged program.
func (s *VendorWorkOrderDetailScreen) confirmStatus() string {
	if !s.sending {
		if s.errMsg != "" {
			return s.statusRow(false, "", s.errMsg)
		}
		return s.statusAnswer(StatusInfo, s.note)
	}
	verb := vwoSpecs[s.action].verb
	switch room := s.bodyWidth(); {
	case s.note == "":
	case room > 0:
		verb = poLeadOnto(s.note, verb, room)
	default:
		// An unsized pane means "do not truncate" everywhere in this layer, and
		// fitStatus leaves the row alone there too — but poLeadOnto would read
		// a room of 0 as no room at all and drop the subject entirely.
		verb = s.note + poLeadJoint + verb
	}
	return s.statusRow(true, verb, "")
}

func (s *VendorWorkOrderDetailScreen) viewConfirm() string {
	frame, offset := s.frameScrolled(s.confirmHeader(), s.confirmBody(), s.confirmScroll,
		s.confirmStatus(), s.confirmBar())
	s.confirmScroll = offset
	return frame
}
