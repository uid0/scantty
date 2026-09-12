package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// The reorder queue drives a request's WHOLE lifecycle, because a workflow the
// terminal can start and not finish is worse than one it never offers: the
// operator has already committed the work before hitting the wall. Before this
// screen carried `o` and `d`, approving here made the row vanish — `pending/`
// is the only unpaginated list OMS serves and it is pending-only — and the job
// had to be finished in a browser.
//
// The four states and the key that leaves each one:
//
//	pending --a--> approved --o--> ordered --d--> received  (terminal)
//	     \           /
//	      \----x----/----------------------------------> cancelled (terminal)
//
// `x` is offered from the two states BEFORE the order leaves the shop and not
// from `ordered`, which is where the web dashboard draws it too: the server
// gates cancel on nothing at all and would happily cancel a received request,
// leaving the stock it credited behind.
//
// `d` is the one key that writes STOCK (mark_received credits the item), so it
// is the one key that asks first.
//
// QUANTITIES HERE ARE THE ITEM'S OWN COUNTING UNIT AND NEVER CASES. See
// omsapi.ReorderRequest's doc: mark_received adds `quantity` to `current_stock`
// with no conversion, and `current_stock` is the canonical base-unit count. A
// purchase-order line is the other half of the house rule and is entered in
// cases (po_case_entry.go); nothing on this screen is.

// reorderView is which slice of the queue the screen is showing. It is a VIEW
// and not a filter in the server's sense: ReorderRequestViewSet declares no
// filter backend, so `?status=` is ignored and every non-pending view is the
// whole paged list narrowed here (omsapi.ListReorderRequests says why).
type reorderView int

const (
	reorderViewPending reorderView = iota
	reorderViewApproved
	reorderViewOrdered
	reorderViewAll
	reorderViewCount // sentinel: the cycle's length, never a view
)

// label names the view on the header row and in the note a cycle answers with.
func (v reorderView) label() string {
	switch v {
	case reorderViewApproved:
		return "approved"
	case reorderViewOrdered:
		return "ordered"
	case reorderViewAll:
		return "all"
	default:
		return "pending"
	}
}

// status is the request status this view keeps, or "" for the view that keeps
// every row. It is what narrows the paged list.
func (v reorderView) status() string {
	switch v {
	case reorderViewApproved:
		return omsapi.ReorderStatusApproved
	case reorderViewOrdered:
		return omsapi.ReorderStatusOrdered
	case reorderViewAll:
		return ""
	default:
		return omsapi.ReorderStatusPending
	}
}

type reorderConfirm int

const (
	reorderConfirmNone reorderConfirm = iota
	reorderConfirmReceive
)

type ReorderQueueScreen struct {
	deps   Deps
	view   reorderView
	rows   []omsapi.ReorderRequest
	cursor int

	windowStart    int
	terminalWidth  int
	terminalHeight int

	loading bool
	loadErr string

	// busyID names the row currently waiting on a lifecycle write. Renders as
	// "(approving…)" next to the title and blocks repeat presses on that row.
	busyID     string
	busyAction string

	// confirmID pins the receive confirm to the row it was opened on, so a
	// reload that re-orders or drops rows cannot leave the prompt naming one
	// request while `y` writes to another. Moving the cursor closes it.
	confirm   reorderConfirm
	confirmID string

	// answer is what the last keypress DID, drawn on the pane because
	// StatusBar.Flash expires after four seconds and the operator who looked
	// away is still looking. Refusals reach BOTH surfaces: the status bar
	// flattens and marks them, this one survives.
	answer      string
	answerLevel StatusLevel
}

type reorderQueueLoadedMsg struct {
	view reorderView
	rows []omsapi.ReorderRequest
	err  error
}

type reorderActionMsg struct {
	id     string
	action string
	err    error
}

func NewReorderQueueScreen(deps Deps) *ReorderQueueScreen {
	return &ReorderQueueScreen{deps: deps, loading: true}
}

func (s *ReorderQueueScreen) Title() string {
	return "Reorder Queue (" + s.view.label() + ")"
}

func (s *ReorderQueueScreen) Init() tea.Cmd { return s.load() }

func (s *ReorderQueueScreen) ctx() context.Context {
	if s.deps.Ctx == nil {
		return context.Background()
	}
	return s.deps.Ctx
}

// load fetches the current view. The pending view reads the dedicated
// `pending/` action — unpaginated and already scoped by SIG ownership — and
// every other view walks the full paged list, because nothing server-side will
// narrow it (omsapi.ListReorderRequests carries that contract note). The reply
// echoes the view it was asked for so a late answer for a view the operator has
// cycled off is DROPPED rather than painted over the one they are reading.
func (s *ReorderQueueScreen) load() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	view := s.view
	want := view.status()
	return func() tea.Msg {
		if view == reorderViewPending {
			rows, err := deps.OMS.ListPendingReorders(ctx, nil)
			return reorderQueueLoadedMsg{view: view, rows: rows, err: err}
		}
		rows, err := deps.OMS.ListReorderRequests(ctx, nil)
		if err != nil {
			return reorderQueueLoadedMsg{view: view, err: err}
		}
		if want == "" {
			return reorderQueueLoadedMsg{view: view, rows: rows}
		}
		kept := make([]omsapi.ReorderRequest, 0, len(rows))
		for _, r := range rows {
			if r.Status == want {
				kept = append(kept, r)
			}
		}
		return reorderQueueLoadedMsg{view: view, rows: kept}
	}
}

func (s *ReorderQueueScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		s.scrollIntoView()
		return s, nil
	case reorderQueueLoadedMsg:
		if m.view != s.view {
			// An answer for a view the operator has cycled off. Dropping it is
			// the whole reason the view rides on the message: painting it would
			// label one status's rows with another's header and bar.
			return s, nil
		}
		s.loading = false
		s.loadErr = ""
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.rows = m.rows
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		// A confirm is pinned to a request id, so a reload that dropped that
		// row closes it rather than re-pointing it at whatever now sits there.
		if s.confirm != reorderConfirmNone && s.rowIndexByID(s.confirmID) < 0 {
			s.closeConfirm()
			s.say("the request that prompt named is no longer in this view", StatusWarn)
		}
		s.scrollIntoView()
		return s, nil
	case reorderActionMsg:
		s.busyID = ""
		s.busyAction = ""
		if m.err != nil {
			text := reorderFailure(m.action, m.err)
			s.answer, s.answerLevel = text, StatusError
			return s, Status(text, StatusError)
		}
		// Refresh: the row leaves this view on every successful transition
		// except one taken inside the `all` view.
		s.loading = true
		s.answer, s.answerLevel = m.action, StatusOK
		return s, tea.Batch(Status(m.action, StatusOK), s.load())
	case tea.KeyMsg:
		if s.confirm != reorderConfirmNone {
			return s.updateConfirm(m)
		}
		return s.updateList(m)
	}
	return s, nil
}

// say records what a key did, on the pane and on the status bar both.
func (s *ReorderQueueScreen) say(text string, level StatusLevel) tea.Cmd {
	s.answer, s.answerLevel = text, level
	return Status(text, level)
}

func (s *ReorderQueueScreen) updateList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "j", "down":
		if s.cursor < len(s.rows)-1 {
			s.cursor++
			s.scrollIntoView()
		}
	case "k", "up":
		if s.cursor > 0 {
			s.cursor--
			s.scrollIntoView()
		}
	case "pgdown":
		s.pageCursor(+1)
	case "pgup":
		s.pageCursor(-1)
	case "g", "home":
		s.cursor = 0
		s.scrollIntoView()
	case "G", "end":
		if s.cursor = len(s.rows) - 1; s.cursor < 0 {
			s.cursor = 0
		}
		s.scrollIntoView()
	case "f":
		s.view = (s.view + 1) % reorderViewCount
		s.rows, s.cursor, s.windowStart = nil, 0, 0
		s.loading, s.loadErr = true, ""
		return s, tea.Batch(s.say("showing "+s.view.label(), StatusInfo), s.load())
	case "r":
		s.loading, s.loadErr = true, ""
		return s, s.load()
	case "a":
		return s.act("approve", omsapi.ReorderStatusPending)
	case "x":
		return s.act("cancel", reorderCancellableFrom...)
	case "o":
		return s.act("mark ordered", omsapi.ReorderStatusApproved)
	case "d":
		return s.openReceiveConfirm()
	case "enter":
		row, ok := s.selected()
		if !ok {
			return s, s.say("nothing to open: this view has no rows", StatusWarn)
		}
		return s, SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, row.Item))
	}
	return s, nil
}

// reorderCancellableFrom is the set `x` is offered on: the two states BEFORE the
// order leaves the shop. The server gates cancel on nothing at all — it would
// cancel a received request and leave the stock it credited in place — so the
// boundary is drawn here, where the affordance is, and it is the same one the
// web dashboard draws by only rendering the button on an open request.
var reorderCancellableFrom = []string{
	omsapi.ReorderStatusPending,
	omsapi.ReorderStatusApproved,
}

// act fires one lifecycle write against the row under the cursor, refusing —
// and saying why — wherever the row's status is outside `from`. reorderOffers is
// the single predicate the BAR and this arm both ask, so a key the footer names
// is a key that acts and the two cannot drift.
func (s *ReorderQueueScreen) act(action string, from ...string) (Screen, tea.Cmd) {
	if s.writing() {
		return s, s.say("nothing written: the "+s.busyAction+" already sent is still out", StatusWarn)
	}
	row, ok := s.selected()
	if !ok {
		return s, s.say("nothing to "+action+": this view has no rows", StatusWarn)
	}
	if !reorderOffers(row.Status, from...) {
		return s, s.say(
			fmt.Sprintf("nothing written: %s is for %s, and this request is %s",
				action, reorderStatusList(from), reorderStatusOf(row)), StatusWarn)
	}
	id := row.IDString()
	if id == "" {
		return s, s.say("nothing written: that row carries no id", StatusError)
	}
	s.busyID, s.busyAction = id, action
	s.answer, s.answerLevel = "", StatusInfo

	deps := s.deps
	ctx := s.ctx()
	switch action {
	case "approve":
		return s, func() tea.Msg {
			_, err := deps.OMS.ApproveReorderRequest(ctx, id, "")
			return reorderActionMsg{id: id, action: "approved", err: err}
		}
	case "cancel":
		return s, func() tea.Msg {
			_, err := deps.OMS.CancelReorderRequest(ctx, id, "")
			return reorderActionMsg{id: id, action: "cancelled", err: err}
		}
	case "mark ordered":
		return s, func() tea.Msg {
			_, err := deps.OMS.MarkReorderRequestOrdered(ctx, id)
			return reorderActionMsg{id: id, action: "marked ordered", err: err}
		}
	}
	s.busyID, s.busyAction = "", ""
	return s, nil
}

// openReceiveConfirm arms the one key on this screen that writes STOCK. The
// quantity is named in the prompt, in the unit it is really credited in, and
// the id is pinned so a reload underneath cannot move the target.
func (s *ReorderQueueScreen) openReceiveConfirm() (Screen, tea.Cmd) {
	if s.writing() {
		return s, s.say("nothing asked: the "+s.busyAction+" already sent is still out", StatusWarn)
	}
	row, ok := s.selected()
	if !ok {
		return s, s.say("nothing to receive: this view has no rows", StatusWarn)
	}
	if !reorderOffers(row.Status, omsapi.ReorderStatusOrdered) {
		return s, s.say(
			fmt.Sprintf("nothing written: mark received is for ordered requests, and this request is %s",
				reorderStatusOf(row)), StatusWarn)
	}
	if row.IDString() == "" {
		return s, s.say("nothing written: that row carries no id", StatusError)
	}
	s.confirm, s.confirmID = reorderConfirmReceive, row.IDString()
	s.answer, s.answerLevel = "", StatusInfo
	return s, nil
}

func (s *ReorderQueueScreen) closeConfirm() {
	s.confirm, s.confirmID = reorderConfirmNone, ""
}

// updateConfirm drives the receive prompt. `y` writes, `n`/`esc` backs out, and
// every other key ANSWERS rather than redrawing a pane that is a pure function
// of unchanged state — including the movement keys, whose whole product is a
// position the prompt is not showing.
func (s *ReorderQueueScreen) updateConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "y", "Y":
		idx := s.rowIndexByID(s.confirmID)
		if idx < 0 {
			s.closeConfirm()
			return s, s.say("nothing written: that request is no longer in this view", StatusWarn)
		}
		row := s.rows[idx]
		id := row.IDString()
		s.closeConfirm()
		s.busyID, s.busyAction = id, "mark received"
		s.answer, s.answerLevel = "", StatusInfo
		deps := s.deps
		ctx := s.ctx()
		// Blank actual_delivery: the server stamps today. The terminal has no
		// date field, and sending an empty string would be a DIFFERENT claim.
		return s, func() tea.Msg {
			_, err := deps.OMS.MarkReorderRequestReceived(ctx, id, "")
			return reorderActionMsg{id: id, action: "marked received", err: err}
		}
	case "n", "N", "esc":
		s.closeConfirm()
		return s, s.say("nothing written: receive cancelled", StatusInfo)
	}
	return s, s.say(m.String()+" does nothing here: y receives, n/esc backs out", StatusInfo)
}

// writing reports that a lifecycle write is already out. It is the ONE gate the
// bar and all four lifecycle arms read, so a key stops being named in the same
// breath it stops acting.
//
// Without it, pressing the same key twice on the same row hit `s.busyID == id`
// and returned nil — a key the bar was still naming, doing nothing, saying
// nothing, on a frame that is a pure function of unchanged state. And `d` a
// second time opened a receive confirm over a receipt already in flight, whose
// own `y` would have posted a second one.
func (s *ReorderQueueScreen) writing() bool { return s.busyID != "" }

func (s *ReorderQueueScreen) selected() (omsapi.ReorderRequest, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.ReorderRequest{}, false
	}
	return s.rows[s.cursor], true
}

func (s *ReorderQueueScreen) rowIndexByID(id string) int {
	if id == "" {
		return -1
	}
	for i, r := range s.rows {
		if r.IDString() == id {
			return i
		}
	}
	return -1
}

// reorderOffers reports whether a row in `status` is one of `from`. A row whose
// status the server did not name is never offered anything: a blank would
// otherwise match nothing and read as a fifth state.
func reorderOffers(status string, from ...string) bool {
	if status == "" {
		return false
	}
	for _, want := range from {
		if want == status {
			return true
		}
	}
	return false
}

// reorderStatusList renders a status set the way a refusal reads it out.
func reorderStatusList(parts []string) string {
	switch len(parts) {
	case 0:
		return "no state"
	case 1:
		return parts[0] + " requests"
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1] + " requests"
	}
}

// reorderStatusOf names a row's state, keeping "the server told us nothing"
// distinct from any real status — a blank would otherwise read as a state.
func reorderStatusOf(r omsapi.ReorderRequest) string {
	if r.Status == "" {
		return "in no state the server named"
	}
	return r.Status
}

// reorderFailure turns a write's error into the one line the operator reads.
// A refusal OMS wrote by hand arrives as its own raw JSON body (parseError has
// no code to switch on), so the sentence is recovered before it is shown;
// anything else keeps the shape it arrived in. The status bar flattens and
// marks whatever this returns, so the load-bearing clause LEADS.
func reorderFailure(action string, err error) string {
	if prose, ok := omsapi.AsDetailRefusal(err); ok {
		return "nothing written: " + prose
	}
	return "nothing written: " + action + " failed: " + err.Error()
}

// ---------------------------------------------------------------------------
// Layout
//
// This screen is not on the columnar jde_form.go layer, so it owns its own
// budget — but it obeys the same two rules that layer exists for. WIDTH: every
// line it hands over is clipped to the pane the terminal REALLY gave (never a
// fixed 51, which is too narrow at 120 columns and too wide at 45), and
// whatever is cut is MARKED, because a value cut clean reads as a whole one.
// HEIGHT: the header, the answer and the action bar are drawn first and the
// ROWS are what give, because clampToBox drops from the bottom and a bar it
// took names no keys at all.

// reorderPaneCells is the columns this screen's View really gets. It is named
// for the screen and not `paneCells` on purpose: that spelling belongs to the
// columnar jde_form.go layer, which marks its budget helpers `jde:layer-only`
// so a sheet cannot grow a second answer to a question the frame already
// answers. ListScreen and the report table name their own pairs the same way.
//
// An UNSIZED
// screen (no WindowSizeMsg yet) falls back to the 80-column pane this project
// checks against rather than to no bound at all.
func (s *ReorderQueueScreen) reorderPaneCells() int {
	if s.terminalWidth <= 0 {
		return pickerPaneWidth
	}
	return screenBodyCells(s.terminalWidth)
}

// reorderPaneRows is the rows the pane really has. screenBodyRows rather than
// screenBodyHeight: the latter floors at four and is therefore a LIE below a
// terminal height of ten, and a screen that pins a bar to the bottom of what it
// assembles cannot budget against a lie.
func (s *ReorderQueueScreen) reorderPaneRows() int {
	if s.terminalHeight <= 0 {
		return screenBodyHeight(24)
	}
	return screenBodyRows(s.terminalHeight)
}

// clip bounds one assembled line to the pane and marks it where it had to cut.
// Assembled rather than part-by-part on purpose: a bound applied to one piece
// of a row that is afterwards appended to is not a bound.
func (s *ReorderQueueScreen) clip(line string) string {
	return pickerClip(line, s.reorderPaneCells())
}

// reorderPlan is how the pane is spent, decided in ONE place so the frame and
// the row window cannot disagree about it.
type reorderPlan struct {
	header   bool     // is the header row (and its blank) drawn
	answer   []string // the answer block, or nil where the pane could not hold it
	bodyRows int      // lines the rows get
	floored  bool     // the pane could not hold even the floor; see reorderFrameFits
}

// plan spends the pane in a STATED ORDER OF SACRIFICE. The ACTION BAR never
// gives — clampToBox drops from the bottom, and a pane that ate the bar names no
// key at all on a screen whose whole subject is which key to press. Then the
// ROWS give, down to one and no further. Then the ANSWER, which is the safe one
// to lose because every answer this screen writes also goes to the status bar.
// Then the HEADER. A keypress must still change something the operator can see,
// and one row of list is what carries the cursor.
//
// IT BUDGETS AGAINST THE CEILING BAR, not the bar being drawn, and that is what
// makes the answer exist rather than an approximation of it: naming the paging
// keys costs cells, cells fold the bar onto another row, a folded bar leaves the
// body one row fewer, and fewer body rows is what decides whether there is
// anything to page. Measured against the bar really drawn that is a cycle —
// plan → barLines → barSegments → reorderScrolls → fitsWholeList → plan — and
// the tallest bar is the fixed point that breaks it, the same answer
// jde_form.go's `…ForBar` pair reaches one layer over. The cost is that a list
// which does not scroll can leave one row of the pane unspent; the alternative
// does not terminate.
func (s *ReorderQueueScreen) plan() reorderPlan {
	ceiling := len(s.barLinesFor(s.barFor(true)))
	avail := s.reorderPaneRows() - 1 - ceiling // the blank above the bar, and the bar
	p := reorderPlan{}
	if avail >= 3 { // the header row, its blank, and a line of body to justify them
		p.header = true
		avail -= 2
	}
	if ans := s.answerLines(); len(ans) > 0 && avail-len(ans) >= 1 {
		p.answer = ans
		avail -= len(ans)
	}
	p.bodyRows = avail
	if p.bodyRows < 1 {
		// Below this the frame runs over and clampToBox takes the bar. The band
		// is left as it is rather than half-converted into a refusal, which is a
		// surface of its own with its own rules about which keys it holds; it is
		// RECORDED instead (reorderFrameFits), and the sweeps scope themselves
		// to the heights the frame really fits and count both sides so the
		// scoping cannot become a way of asserting nothing.
		p.bodyRows, p.floored = 1, true
	}
	return p
}

// reorderFrameFits reports whether the pane can hold the frame at all — the
// boundary the height sweeps scope themselves by. It moves with every wording on
// the screen and grows as the terminal gets NARROWER, because the bar then folds
// onto more rows, so it is DERIVED and never written down as a number.
func (s *ReorderQueueScreen) reorderFrameFits() bool { return !s.plan().floored }

// bodyLineBudget is what the rows get.
func (s *ReorderQueueScreen) bodyLineBudget() int { return s.plan().bodyRows }

// markerRows is what the "↑ N more above" / "↓ N more below" pair costs, and it
// is spent out of the ROW budget rather than taken off the assembled frame
// afterwards. Taken after, the frame claims a row the pane does not have and
// clampToBox takes it off the BOTTOM — which is the action bar.
//
// The PAIR is reserved whenever the list does not all fit, even at the top
// where only the lower marker is drawn. Reserving one and drawing the other
// when needed is the version to not reach for: at the boundary the body is
// exactly one marker short, so the first keypress that scrolls makes both apply
// with nothing left to pay the second out of, and the frame overruns on the
// press rather than at rest.
func (s *ReorderQueueScreen) markerRows() int {
	if s.fitsWholeList() {
		return 0
	}
	return 2
}

// fitsWholeList reports whether every row fits the body with no markers spent.
// It is the ONE answer behind both markerRows and reorderScrolls, and it walks
// the rows directly rather than asking rowsFittingFrom — which subtracts what
// this decides, and would recurse.
func (s *ReorderQueueScreen) fitsWholeList() bool {
	budget, used := s.bodyLineBudget(), 0
	room := s.reorderPaneCells()
	for i := range s.rows {
		if used += len(s.rowLines(i, room)); used > budget {
			return false
		}
	}
	return true
}

// rowsFittingFrom counts the rows that fit in the body when the window opens at
// `start`. Measured in LINES and re-derived on every move, because a row draws
// two lines or three depending on what it carries: a size taken once over the
// short rows at the top of a list overruns the moment the cursor reaches one
// carrying a note.
func (s *ReorderQueueScreen) rowsFittingFrom(start int) int {
	budget := s.bodyLineBudget() - s.markerRows()
	used, n := 0, 0
	room := s.reorderPaneCells()
	for i := start; i < len(s.rows); i++ {
		lines := len(s.rowLines(i, room))
		if n > 0 && used+lines > budget {
			break
		}
		used += lines
		n++
	}
	if n == 0 && start < len(s.rows) {
		n = 1 // never an empty window: the cursor's row is what a press moves
	}
	return n
}

func (s *ReorderQueueScreen) scrollIntoView() {
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.windowStart > s.cursor {
		s.windowStart = s.cursor
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	// Walk the start forward until the cursor is inside the window the start
	// produces. Because the window's SIZE depends on which rows it opens at,
	// the two have to be chosen together rather than sized once and held.
	for s.windowStart < s.cursor && s.cursor >= s.windowStart+s.rowsFittingFrom(s.windowStart) {
		s.windowStart++
	}
	if fits := s.rowsFittingFrom(0); fits >= len(s.rows) {
		s.windowStart = 0
	}
}

func (s *ReorderQueueScreen) pageCursor(dir int) {
	step := s.rowsFittingFrom(s.windowStart)
	if step < 1 {
		step = 1
	}
	s.cursor += dir * step
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.scrollIntoView()
}

// reorderScrolls reports whether the list has more rows than the body can hold,
// which is the ONE answer the paging keys and the bar segment that names them
// both read — so the bar cannot claim a page the arm refuses.
func (s *ReorderQueueScreen) reorderScrolls() bool {
	return !s.fitsWholeList()
}

// ---------------------------------------------------------------------------
// The action bar

// barSegments is the bar's words, for the callers that only want to read them.
//
// The bar itself is a RECORD (proseBar, prose_bar.go) rather than a list of
// strings: every segment carries the keystrokes its words spell, which is what
// lets TestProseBar_TheFooterNamesExactlyTheKeysThatWork press the whole key
// space at this screen instead of taking the legend on trust. This screen was
// the cheapest cursor LIST to convert because the hard half was already done —
// the fold, the ceiling and the derived row budget below all predate the record
// — so what the record adds is the reading, not the arithmetic.
func (s *ReorderQueueScreen) barSegments() []string {
	var out []string
	for _, it := range s.barFor(s.reorderScrolls()) {
		out = append(out, it.Hint)
	}
	return out
}

// proseBar is the bar this screen is DRAWING, and it is never nil: every state
// of this screen draws a bar, the confirm included.
func (s *ReorderQueueScreen) proseBar() proseBar { return s.barFor(s.reorderScrolls()) }

// barFor names EXACTLY the keys that work where the cursor is standing, with
// the paging answer supplied rather than asked — so the row budget can measure
// the CEILING bar without asking a question whose answer depends on the budget.
// Read bodyLineBudget for why.
//
// The lifecycle keys are status-conditional because the actions are: `o` on a
// pending request would skip approval, and `d` on one would credit stock for
// goods nobody ordered. Both would be accepted by the server, which gates
// neither — so the bar is where the workflow is stated, and reorderOffers is
// the same predicate the arms ask.
func (s *ReorderQueueScreen) barFor(scrolls bool) proseBar {
	if s.confirm != reorderConfirmNone {
		return proseBar{
			{Keys: []string{"y"}, Hint: "y receive"},
			{Keys: []string{"n", "esc"}, Hint: "n/esc cancel"},
		}
	}
	segs := proseNavList(listNavMoves(len(s.rows)), scrolls)
	tail := proseBar{
		{Keys: []string{"f"}, Hint: "f view"},
		proseBarRefresh,
		proseBarEsc,
	}
	if row, ok := s.selected(); ok {
		segs = append(segs, proseBarItem{Keys: []string{"enter"}, Hint: "enter open item"})
		if s.writing() {
			// A write is out: the four lifecycle keys refuse, so the bar stops
			// naming them rather than leaving the dead end on the legend.
			return append(segs, tail...)
		}
		if reorderOffers(row.Status, omsapi.ReorderStatusPending) {
			segs = append(segs, proseBarItem{Keys: []string{"a"}, Hint: "a approve"})
		}
		if reorderOffers(row.Status, reorderCancellableFrom...) {
			segs = append(segs, proseBarItem{Keys: []string{"x"}, Hint: "x cancel"})
		}
		if reorderOffers(row.Status, omsapi.ReorderStatusApproved) {
			segs = append(segs, proseBarItem{Keys: []string{"o"}, Hint: "o mark ordered"})
		}
		if reorderOffers(row.Status, omsapi.ReorderStatusOrdered) {
			segs = append(segs, proseBarItem{Keys: []string{"d"}, Hint: "d mark received"})
		}
	}
	return append(segs, tail...)
}

// barLines is the bar as it will be DRAWN — folded at the pane's `·` joints,
// never hand-counted against 51. Folding spends rows, so this is what the row
// budget above measures rather than a constant.
func (s *ReorderQueueScreen) barLines() []string {
	return s.barLinesFor(s.proseBar())
}

func (s *ReorderQueueScreen) barLinesFor(bar proseBar) []string {
	return pickerWrap(bar.hint(), s.reorderPaneCells())
}

// answerLines is what the last keypress did, folded to the pane. Empty when
// there is nothing to say, so "nothing to say" and "the line scrolled away" are
// different states rather than one blank.
func (s *ReorderQueueScreen) answerLines() []string {
	if s.answer == "" {
		return nil
	}
	return append([]string{""}, pickerWrap(s.answer, s.reorderPaneCells())...)
}

func (s *ReorderQueueScreen) answerStyle() func(...string) string {
	switch s.answerLevel {
	case StatusError:
		return StyleStatusError.Render
	case StatusWarn:
		return StyleStatusWarn.Render
	case StatusOK:
		return StyleStatusOK.Render
	default:
		return StyleMuted.Render
	}
}

// ---------------------------------------------------------------------------
// Rows

// reorderQtyFacts is the quantity as it is really credited, and the state the
// request is in. Both are FACTS and neither gives: the quantity is the number
// the operator is about to commit, and the status is what decides which key
// acts on the row — a row whose state was clipped off is a row you cannot tell
// how to act on.
//
// The unit is carried by the confirm rather than by this row, where there is no
// space for it; what this row must not do is imply the OTHER unit. It says
// "× 100" and never "100 cases", because a purchase-order line one key away
// really is in cases (po_case_entry.go) and the two numbers look identical.
func reorderQtyFacts(r omsapi.ReorderRequest) string {
	facts := fmt.Sprintf("  × %d", r.Quantity)
	if r.Status != "" {
		facts += "  " + r.Status
	}
	return facts
}

// rowLines renders one request as the lines it draws. Line one is the IDENTITY
// — an urgency flag that LEADS (a clip takes the right-hand end, so a marker
// after the name is eaten at exactly the width it matters most), the row's
// number, the item's name, the quantity and the state. Line two is the data
// row. A third line carries whatever the request says about itself, and only
// when it says something.
func (s *ReorderQueueScreen) rowLines(i int, room int) []string {
	r := s.rows[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	flag := "  "
	if r.Priority == "urgent" || r.Priority == "high" {
		flag = "! "
	}

	// The raw item UUID is the fallback when the row carries no expansion, and it
	// is the one value on this screen that must never be shortened: a cut UUID
	// matches nothing, which is the opposite of what an identifier is for. So the
	// LAYOUT changes rather than the identifier — it takes a line of its own at
	// the data indent, where the 51-cell pane an 80-column terminal gives leaves
	// 47 for the 41 it needs, and the facts that would have shared its line move
	// down with it. Clipped to the pane all the same, because below about 45
	// columns no arrangement fits 36 hex digits and an unmarked cut is worse than
	// a marked one.
	name, whole := reorderRowName(r)
	lead := caret + flag + fmt.Sprintf("%d)", i+1)
	facts := reorderQtyFacts(r)
	busy := s.busyLabel(r)
	indent := strings.Repeat(" ", reorderDataIndent)

	// The highlight's own padding is reserved on EVERY row, not just the one the
	// cursor is on: the style adds it AFTER the clip, so a row that fits until it
	// is selected is cut on exactly the keypress that selects it — and a row
	// whose width changed under the cursor would shift the columns as the
	// operator moves. Asked of the style rather than counted.
	headRoom := room - StyleSidebarItemActive.GetHorizontalPadding()
	var head string
	var extraLines []string
	if whole {
		head = pickerClip(lead+facts+busy, headRoom)
		extraLines = append(extraLines, s.clip(indent+pickerClip(name, room-reorderDataIndent)))
	} else {
		head = pickerClip(lead+" "+poFitRow(headRoom-lipgloss.Width(lead)-1, name, facts, busy), headRoom)
	}
	if i == s.cursor {
		head = StyleSidebarItemActive.Render(head)
	}

	lines := append([]string{head}, extraLines...)
	lines = append(lines, s.clip(indent+reorderQueueLine(r, room-reorderDataIndent)))
	if extra := reorderRowNote(r); extra != "" {
		lines = append(lines, s.clip(indent+StyleMuted.Render(extra)))
	}
	return lines
}

// reorderDataIndent lines the data row up under the item's name.
const reorderDataIndent = 4

// busyLabel names a write in flight on this row, so a key that went off the
// terminal says so where the operator is looking rather than only on a status
// flash that expires while they are still watching.
func (s *ReorderQueueScreen) busyLabel(r omsapi.ReorderRequest) string {
	if s.busyID == "" || r.IDString() != s.busyID {
		return ""
	}
	return "  " + StyleStatusWarn.Render("("+s.busyAction+"…)")
}

// reorderRowName is what to call the row, and whether that name is WHOLE — a
// value no clip may touch. The raw item UUID is the only such value here.
func reorderRowName(r omsapi.ReorderRequest) (string, bool) {
	if r.ItemDetails != nil && r.ItemDetails.Name != "" {
		return r.ItemDetails.Name, false
	}
	if r.Item != "" {
		return "item " + r.Item, true
	}
	return "—", false
}

// reorderRowNote is the request's own words, the requester, the supplier and
// any purchase-order number the PO domain has stamped on it — the row's least
// load-bearing line, and the one a short pane drops first.
func reorderRowNote(r omsapi.ReorderRequest) string {
	parts := []string{}
	if r.RequestedBy != "" {
		parts = append(parts, "by "+r.RequestedBy)
	}
	if r.ItemDetails != nil && r.ItemDetails.PreferredSupplier != "" {
		parts = append(parts, r.ItemDetails.PreferredSupplier)
	}
	if r.OrderNumber != "" {
		parts = append(parts, "PO "+r.OrderNumber)
	}
	if r.RequestNotes != "" {
		parts = append(parts, r.RequestNotes)
	}
	return strings.Join(parts, " · ")
}

// reorderQueueLine renders the per-row data summary: the SKU, stock against the
// minimum, the estimated cost and how long the request has waited.
//
// Every part of it is one of exactly two things. The SKU is a BOUNDED
// IDENTIFIER — a 32-cell manufacturer part number is ordinary MRO data — and it
// is what gives, marked. The stock ratio, the money and the age are FACTS THAT
// NEVER GIVE: they are what the row is read for, and a price cut to "$1234."
// reads as a price rather than as a truncation.
//
// The facts are PADDED to fixed widths and the SKU field is padded out to what
// they leave, so the columns line up down the list — which is the whole
// readability argument for a columnar screen, since an operator scans DOWN a
// column and a column that moves per row is not one. Padded as PLAIN text
// before any styling, because fmt pads by byte count and an ANSI-wrapped value
// would be padded by the length of its escape sequences.
func reorderQueueLine(r omsapi.ReorderRequest, room int) string {
	sku := "—"
	stockMin := "—"
	if r.ItemDetails != nil {
		if r.ItemDetails.SKU != "" {
			sku = r.ItemDetails.SKU
		}
		stockMin = fmt.Sprintf("%d/%d", r.ItemDetails.CurrentStock, r.ItemDetails.MinimumStock)
	}
	est := "—"
	if !r.EstimatedCost.Empty() {
		est = string(r.EstimatedCost)
	}
	// Days pending colours urgency (red at a week, amber at three days) so a
	// stale request is visible without doing the arithmetic.
	days := fmt.Sprintf("%3s", fmt.Sprintf("%dd", r.DaysPending))
	switch {
	case r.DaysPending >= 7:
		days = StyleStatusError.Render(days)
	case r.DaysPending >= 3:
		days = StyleStatusWarn.Render(days)
	}
	facts := fmt.Sprintf("  STOCK %-7s  $%-8s  %s", stockMin, est, days)

	field := "SKU " + sku
	avail := room - lipgloss.Width(facts)
	if avail < reorderSKUFloor {
		avail = reorderSKUFloor
	}
	field = pickerClip(field, avail)
	if pad := avail - lipgloss.Width(field); pad > 0 {
		field += strings.Repeat(" ", pad)
	}
	return field + facts
}

// reorderSKUFloor is the least the SKU field is ever squeezed to. Below this an
// abbreviation stops being recognisable beside the part in the operator's hand,
// and the row is better off overrunning into the pane's own mark than pretending
// to identify something.
const reorderSKUFloor = 8

// ---------------------------------------------------------------------------
// Frames

// View assembles the frame in a stated order of sacrifice: the header row, the
// answer and the action bar are drawn whatever happens, and the ROWS give —
// because clampToBox drops from the BOTTOM, and a pane that ate the bar names
// no key at all on a screen whose whole subject is which key to press.
func (s *ReorderQueueScreen) View() string {
	if s.confirm != reorderConfirmNone {
		return s.viewConfirm()
	}
	if s.loading {
		return s.frame([]string{
			s.clip(StyleMuted.Render("Loading the " + s.view.label() + " reorder requests…")),
		})
	}
	if s.loadErr != "" {
		body := []string{s.clip(StyleStatusError.Render("Could not load the " +
			s.view.label() + " reorder requests."))}
		for _, line := range failDetailLines(s.loadErr, s.reorderPaneCells(), reorderFailDetailRows) {
			body = append(body, s.clip(StyleMuted.Render(line)))
		}
		return s.frame(body)
	}
	if len(s.rows) == 0 {
		return s.frame([]string{s.clip(StyleMuted.Render(
			"No " + s.view.label() + " reorder requests. f cycles the view."))})
	}

	body := []string{}
	if s.windowStart > 0 {
		body = append(body, s.clip(StyleMuted.Render(
			fmt.Sprintf("  ↑ %d more above", s.windowStart))))
	}
	room := s.reorderPaneCells()
	end := s.windowStart + s.rowsFittingFrom(s.windowStart)
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		body = append(body, s.rowLines(i, room)...)
	}
	if end < len(s.rows) {
		body = append(body, s.clip(StyleMuted.Render(
			fmt.Sprintf("  ↓ %d more below", len(s.rows)-end))))
	}
	return s.frame(body)
}

// reorderFailDetailRows is how much of an OMS failure body the pane spends on
// the detail under the headline. Bounded because parseError fills the message
// with the ENTIRE raw payload whenever the envelope carries no code, and a
// gateway's HTML page would otherwise take the whole frame.
const reorderFailDetailRows = 3

// frame draws the header, the body it is given, the answer and the bar. ONE
// assembler for every state, so the header and the bar cannot go missing from
// the state nobody thought to check — which is how a list ends up with a frame
// that names no keys in exactly the state the operator most needs one.
func (s *ReorderQueueScreen) frame(body []string) string {
	p := s.plan()
	// The LAST bound, and the one that makes the invariant hold for every branch
	// at once rather than for the branch somebody remembered: whatever a frame
	// hands over, its body is cut to the budget and the cut is MARKED. The
	// windowed list has already fitted itself and its markers, so this only
	// bites on the states that are a fixed block — a gateway page under a failed
	// load, the receive confirm's prose — where a short pane would otherwise
	// push the bar off the bottom.
	body = foldKeepRows(body, p.bodyRows, s.reorderPaneCells())

	var b strings.Builder
	if p.header {
		b.WriteString(s.headerLine() + "\n\n")
	}
	for _, line := range body {
		b.WriteString(line + "\n")
	}
	for _, line := range p.answer {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString(s.clip(s.answerStyle()(line)) + "\n")
	}
	b.WriteString("\n")
	for i, line := range s.barLines() {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(s.clip(StyleMuted.Render(line)))
	}
	return b.String()
}

// headerLine says which view is on the pane and how many rows that is. It is
// drawn in EVERY state, the empty one included: `f` fetches nothing visible of
// its own and its only product is this row, so a view cycled onto an empty
// slice would otherwise redraw a byte-identical pane.
func (s *ReorderQueueScreen) headerLine() string {
	if s.loading {
		return s.clip(StyleStatusWarn.Render(strings.ToUpper(s.view.label()) + " — loading"))
	}
	return s.clip(StyleStatusWarn.Render(fmt.Sprintf("%s — %d request(s)",
		strings.ToUpper(s.view.label()), len(s.rows))))
}

// viewConfirm is the one prompt on this screen, guarding the one key that
// writes STOCK. It names the quantity AND the unit, because that number looks
// identical to a purchase-order line's and that one is in CASES.
func (s *ReorderQueueScreen) viewConfirm() string {
	idx := s.rowIndexByID(s.confirmID)
	if idx < 0 {
		// Only reachable between a reload dropping the row and the arm that
		// closes the prompt; say so rather than drawing an empty frame.
		return s.frame([]string{s.clip(StyleStatusWarn.Render(
			"That request is no longer in this view."))})
	}
	r := s.rows[idx]
	name, _ := reorderRowName(r)
	body := []string{
		s.clip(StyleStatusWarn.Render("Mark received: " + name)),
		"",
		s.clip(StyleMuted.Render(fmt.Sprintf(
			"Credits %d to stock — individual items, not cases.", r.Quantity))),
		s.clip(StyleMuted.Render(
			"Delivery date is recorded as today. Receiving twice credits once.")),
	}
	return s.frame(body)
}
