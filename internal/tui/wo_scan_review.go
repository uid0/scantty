// WorkOrderScanReviewScreen — the terminal's half of the scanned-work-order
// human gate.
//
// THE DEAD END THIS CLOSES. `U` on the work-order detail uploads a scanned or
// emailed sheet (UploadWorkOrderPdf). The backend reads the marks off it and
// parks every one it is not certain of on a WorkOrderSubmission, leaving the
// submission `pending_review` — deliberately, because "a scan never closes a WO
// on its own" (inventory/services/work_order_ingest). ScanTTY drove neither
// half of that gate and did not even decode the flags that say a sheet is
// waiting, so a work order could be scanned in from the bench and only finished
// in a browser. Everything here is the reviewer's side of that gate; nothing
// here weakens it or changes when it fires.
//
// WHY A SCREEN OF ITS OWN, and not another mode on the work-order detail: a
// review is a LIST with a per-row decision on it, and the detail sheet is a
// TextScroller under a prose footer already naming fourteen keys. This rides
// the columnar layer (jde_form.go) — the standing convention — so the pane
// budget, the folded action bar, the movement gating and the short-pane refusal
// are the layer's rather than hand-rolled a second time.
//
// KEY SCHEME, following po_edit.go (the pilot) and po_attachments.go:
//
//	Up/Down       move between rows
//	PgUp/PgDn     page, named only where the body outruns the pane
//	Space         mark / unmark the reading under the cursor
//	Enter         open the APPLY confirm
//	Ctrl-X        open the DISCARD confirm
//	Ctrl-F        open the COMPLETE confirm (apply everything and close the job)
//	Ctrl-X        commit, on any of the three confirms
//	Esc           back (the root's own back step) / cancel a confirm
//	r             refresh
//
// Ctrl-X and not Enter is what COMMITS on a confirm, for the reason po_edit
// records: the key that OPENS a confirm is the key a hand reaches for next, and
// binding an irreversible write to it makes a reflex enough to fire one.
//
// SELECTIVE, NOT ALL-OR-NOTHING, and the reason is the shape of the job. A
// sheet is read one box at a time and a reader gets some right and some wrong;
// forced to choose for the whole sheet, an operator either accepts a mark they
// know is wrong or throws away four they know are right and re-does them by
// hand. The server takes an optional `target_ids` list precisely so it need not
// be all-or-nothing, and the web surface offers per-row accept/reject too. What
// the terminal adds is that the marks are collected FIRST and committed once:
// one write, one confirm, and the whole sheet reviewed before anything moves.
//
// WHAT SELECTION CANNOT REACH, said on the row rather than hidden. The server
// matches `target_ids` against a change's `target_id`, and a signature or a
// handwritten note carries none — there is no spelling of `target_ids` that
// names one. Those rows are drawn, are not markable, and say so; they go with a
// WHOLE-QUEUE apply or discard, which is what Enter does when nothing is
// marked.
//
// THE SCAN IMAGE IS OUT OF SCOPE and that is a real limit rather than an
// omission: `scan-image` and `mark-crop` serve PNGs, which a terminal cannot
// draw. Every reading carries a LABEL, what the reader saw, and a confidence,
// which is what the decision is made on — but a reviewer who wants to check a
// doubtful mark against the paper has the paper in their hand at the bench, and
// where they do not, the row's confidence is the fact that says so. The frame
// names the limit rather than implying the confidence is the whole story.
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

type woReviewPhase int

const (
	woReviewPhaseList woReviewPhase = iota
	woReviewPhaseConfirm
)

// woReviewAction is which write a confirm is about. Three actions, one confirm
// frame: the frame's wording, its caveats and the request it sends all come off
// this one value, so a fourth cannot be added without answering for all three.
type woReviewAction int

const (
	woReviewApply woReviewAction = iota
	woReviewDiscard
	woReviewComplete
)

// woReviewRowKind distinguishes the two row shapes. BOTH are navigable: a body
// line that belongs to no navigable row is a line no key can reach, because
// jdeLines.Window anchors on the cursor's block and a columnar cursor cannot go
// above its first row (AGENTS.md). A submission's own row is where the
// whole-sheet facts live — its id, when it arrived, why it could not be read —
// so it is a row rather than a heading above one.
type woReviewRowKind int

const (
	woReviewSubmissionRow woReviewRowKind = iota
	// woReviewNoteRow is a degraded sheet's REASON, and it is a row of its own
	// rather than more lines under the sheet's.
	//
	// The reason is unbounded prose from the server, so hung off the sheet's row
	// it made one block taller than a short pane — and a sheet parked with
	// nothing queued has exactly ONE other row, so the bar rightly named no
	// movement pair and the frame drew "↓ more below" over a legend offering
	// nothing to press. Standing rule 11, on the one state a degraded read has.
	// Split out, the reason is a place the cursor can go, so the window can
	// reach it and the bar can honestly say so.
	woReviewNoteRow
	woReviewChangeRow
)

// woReviewRow addresses one drawable row by POSITION in the flattened list, and
// carries the indices it was built from. Nothing outside rebuildRows constructs
// one, and no index here outlives a reload: the cursor and the marks are both
// re-seated by IDENTITY (submission id + target id), for the reason po_edit
// records — an index carried across a reload re-points at whatever now sits
// there, and here that would mean confirming one reading and applying another.
type woReviewRow struct {
	kind   woReviewRowKind
	sub    int
	change int // -1 on a submission row
}

// woReviewMark keys a SELECTION by identity rather than by position. Only a
// change row can be marked, so a mark is (sheet, target) and nothing else.
type woReviewMark struct {
	submission string
	target     string
}

// woReviewSeat keys a CURSOR POSITION by identity, which needs the row's KIND
// too: a sheet row and its reason row are both (sheet, "") and re-seating onto
// the wrong one would put the cursor somewhere the operator did not leave it.
type woReviewSeat struct {
	kind       woReviewRowKind
	submission string
	target     string
}

type WorkOrderScanReviewScreen struct {
	deps    Deps
	woID    string
	woLabel string
	wo      *omsapi.WorkOrder
	loading bool
	loadErr string
	// jdeScreen carries the pane geometry and the frames (jde_form.go).
	jdeScreen

	phase  woReviewPhase
	rows   []woReviewRow
	subs   []omsapi.WorkOrderSubmission
	cursor int
	marked map[woReviewMark]bool

	// The screen's answer to the last keypress. Every arm that declines writes
	// one, because this frame draws no caret and a silent return redraws a pane
	// that is a pure function of unchanged state.
	note string

	// The confirm. confirmSub is the submission it is about, confirmTargets the
	// changes it will name (nil for a whole-queue write), and confirmScroll the
	// offset over a body nothing navigates.
	confirmAction  woReviewAction
	confirmSub     string
	confirmTargets []string
	confirmScroll  int
	writing        bool
	// The result of the last write, kept so the list can report what the SERVER
	// said rather than what was asked for — the completion in particular, which
	// is conditional server-side and is never predicted here.
	writeErr string
}

type woReviewLoadedMsg struct {
	wo  *omsapi.WorkOrder
	err error
}

type woReviewWroteMsg struct {
	applied  *omsapi.WorkOrderApplyResult
	discard  *omsapi.WorkOrderDiscardResult
	err      error
	complete bool
}

// NewWorkOrderScanReviewScreen seeds from the work order the detail screen
// already loaded — the submissions ride that payload — and reloads by id after
// each write so what is on the pane is what the server now holds.
func NewWorkOrderScanReviewScreen(deps Deps, woID string, wo *omsapi.WorkOrder) *WorkOrderScanReviewScreen {
	s := &WorkOrderScanReviewScreen{
		deps:   deps,
		woID:   woID,
		marked: map[woReviewMark]bool{},
	}
	s.adopt(wo)
	return s
}

func (s *WorkOrderScanReviewScreen) Title() string {
	if s.woLabel != "" {
		return "Scan review · " + s.woLabel
	}
	return "Scan review"
}

// WantsRawInput claims the keyboard on the CONFIRM frames only.
//
// The confirms own an irreversible write and their own esc, so nothing there
// may reach the root. The list deliberately does not: `esc` there is the root's
// back step, which restores the work-order detail the operator came from with
// its scroll position intact, and `tab` still reaches the sidebar while
// browsing. The bar names Esc because Esc really leaves — asserted by pressing
// it through a real Root rather than read off the switch below.
func (s *WorkOrderScanReviewScreen) WantsRawInput() bool {
	return s.phase == woReviewPhaseConfirm
}

func (s *WorkOrderScanReviewScreen) Init() tea.Cmd { return s.load() }

func (s *WorkOrderScanReviewScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *WorkOrderScanReviewScreen) load() tea.Cmd {
	s.loading = true
	deps, id, ctx := s.deps, s.woID, s.ctx()
	return func() tea.Msg {
		wo, err := deps.OMS.GetWorkOrder(ctx, id)
		return woReviewLoadedMsg{wo: wo, err: err}
	}
}

// adopt takes a work order and rebuilds everything derived from it.
func (s *WorkOrderScanReviewScreen) adopt(wo *omsapi.WorkOrder) {
	if wo == nil {
		return
	}
	s.wo = wo
	if wo.ShortID != "" {
		s.woLabel = wo.ShortID
	} else if wo.DisplayTitle != "" {
		s.woLabel = wo.DisplayTitle
	}
	s.rebuildRows()
}

// rebuildRows flattens the parked submissions into the drawable list, drops
// marks whose change is gone, and re-seats the cursor by identity.
//
// A write CHANGES the queue — that is what it is for — so a reload landing
// under the operator is the ordinary case rather than a corner. Re-seating by
// position would leave the cursor on whatever moved up into its index, which on
// a screen whose next keypress applies a reading is the po_edit defect exactly.
func (s *WorkOrderScanReviewScreen) rebuildRows() {
	was, hadCursor := s.seatOf(s.cursor)

	s.subs = nil
	s.rows = nil
	for _, sub := range s.wo.Submissions {
		if !sub.AwaitsReview() {
			continue
		}
		s.subs = append(s.subs, sub)
	}
	live := map[woReviewMark]bool{}
	for i, sub := range s.subs {
		s.rows = append(s.rows, woReviewRow{kind: woReviewSubmissionRow, sub: i, change: -1})
		if sub.ParseError != "" {
			s.rows = append(s.rows, woReviewRow{kind: woReviewNoteRow, sub: i, change: -1})
		}
		for j, c := range sub.PendingChanges {
			s.rows = append(s.rows, woReviewRow{kind: woReviewChangeRow, sub: i, change: j})
			if c.Selectable() {
				live[woReviewMark{sub.ID, c.TargetID}] = true
			}
		}
	}
	for m := range s.marked {
		if !live[m] {
			delete(s.marked, m)
		}
	}

	s.cursor = 0
	if hadCursor {
		for i := range s.rows {
			if seat, ok := s.seatOf(i); ok && seat == was {
				s.cursor = i
				break
			}
		}
	}
	if s.cursor >= len(s.rows) {
		s.cursor = len(s.rows) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

// seatOf is the identity of the row at index i — kind, sheet, and target where
// it has one — which is what the cursor is re-seated onto after a reload.
func (s *WorkOrderScanReviewScreen) seatOf(i int) (woReviewSeat, bool) {
	if i < 0 || i >= len(s.rows) {
		return woReviewSeat{}, false
	}
	r := s.rows[i]
	if r.sub >= len(s.subs) {
		return woReviewSeat{}, false
	}
	sub := s.subs[r.sub]
	if r.kind != woReviewChangeRow {
		return woReviewSeat{kind: r.kind, submission: sub.ID}, true
	}
	if r.change < 0 || r.change >= len(sub.PendingChanges) {
		return woReviewSeat{}, false
	}
	return woReviewSeat{woReviewChangeRow, sub.ID, sub.PendingChanges[r.change].TargetID}, true
}

// currentSubmission is the submission the cursor is standing in — the one every
// write on this screen is about. A write is per submission server-side, so
// "which sheet" is answered by where the cursor is rather than by a second
// selection the operator would have to keep in their head.
func (s *WorkOrderScanReviewScreen) currentSubmission() (omsapi.WorkOrderSubmission, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.WorkOrderSubmission{}, false
	}
	i := s.rows[s.cursor].sub
	if i < 0 || i >= len(s.subs) {
		return omsapi.WorkOrderSubmission{}, false
	}
	return s.subs[i], true
}

// currentChange is the reading under the cursor, if the cursor is on one.
func (s *WorkOrderScanReviewScreen) currentChange() (omsapi.WorkOrderPendingChange, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.WorkOrderPendingChange{}, false
	}
	r := s.rows[s.cursor]
	if r.kind != woReviewChangeRow || r.sub >= len(s.subs) {
		return omsapi.WorkOrderPendingChange{}, false
	}
	sub := s.subs[r.sub]
	if r.change < 0 || r.change >= len(sub.PendingChanges) {
		return omsapi.WorkOrderPendingChange{}, false
	}
	return sub.PendingChanges[r.change], true
}

// selectionIn is the marked targets of ONE submission, in the order the server
// sent the changes so the confirm reads down the sheet.
func (s *WorkOrderScanReviewScreen) selectionIn(sub omsapi.WorkOrderSubmission) []string {
	var out []string
	for _, c := range sub.PendingChanges {
		if c.Selectable() && s.marked[woReviewMark{sub.ID, c.TargetID}] {
			out = append(out, c.TargetID)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

func (s *WorkOrderScanReviewScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case woReviewLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, Status("scan review reload failed: "+m.err.Error(), StatusError)
		}
		s.loadErr = ""
		s.adopt(m.wo)
		return s, nil

	case woReviewWroteMsg:
		return s.wrote(m)

	case tea.KeyMsg:
		if s.phase == woReviewPhaseConfirm {
			return s.keyConfirm(m)
		}
		return s.keyList(m)
	}
	return s, nil
}

func (s *WorkOrderScanReviewScreen) keyList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "up", "down":
		delta := 1
		if m.String() == "up" {
			delta = -1
		}
		next, ok := s.pickRow(s.cursor, len(s.rows), delta, len(s.listHeader()), s.listBar())
		if !ok {
			// A gated movement arm answers with NOTHING: its whole product WAS
			// the position, and a note written on a refused pane is not drawn
			// now and IS drawn when the terminal grows back (the layer's
			// Movement block).
			return s, nil
		}
		s.cursor = next
		s.note = ""
		return s, nil
	case "pgup", "pgdown":
		dir := 1
		if m.String() == "pgup" {
			dir = -1
		}
		body, _ := s.listLines()
		next, ok := s.pageRow(body, s.cursor, len(s.rows), dir, len(s.listHeader()),
			s.listBar(), s.listBarItems(true))
		if !ok {
			return s, nil
		}
		s.cursor = next
		s.note = ""
		return s, nil
	case " ":
		return s, s.toggleMark()
	case "enter":
		return s, s.openConfirm(woReviewApply)
	case "ctrl+x":
		return s, s.openConfirm(woReviewDiscard)
	case "ctrl+f":
		return s, s.openConfirm(woReviewComplete)
	case "r":
		s.note = ""
		s.loadErr = ""
		return s, s.load()
	}
	return s, nil
}

// toggleMark is the per-reading selection, and it DECLINES OUT LOUD on the two
// rows it cannot mark — a submission row, and a reading the server gives no
// target to. Silence there would redraw an identical pane on a frame with no
// caret to move, which is this project's oldest reported defect.
func (s *WorkOrderScanReviewScreen) toggleMark() tea.Cmd {
	if s.writing {
		s.note = woReviewBusyNote
		return nil
	}
	c, ok := s.currentChange()
	if !ok {
		s.note = "space marks a reading — " + s.currentRowIs() + "."
		return nil
	}
	if !c.Selectable() {
		s.note = woReviewUnselectableNote
		return nil
	}
	sub, _ := s.currentSubmission()
	key := woReviewMark{sub.ID, c.TargetID}
	if s.marked[key] {
		delete(s.marked, key)
		s.note = "unmarked."
		return nil
	}
	s.marked[key] = true
	s.note = "marked."
	return nil
}

// currentRowIs names what the cursor is standing on, for the arms that decline
// on it. Two rows are not readings and each is a different thing to be told.
func (s *WorkOrderScanReviewScreen) currentRowIs() string {
	if s.cursor >= 0 && s.cursor < len(s.rows) && s.rows[s.cursor].kind == woReviewNoteRow {
		return "this row is why the sheet could not be read"
	}
	return "this row is the sheet itself"
}

const (
	woReviewBusyNote = "a write is already out — wait for it to answer."
	// The one refusal a reviewer cannot satisfy from this row, so it names the
	// way that DOES reach the reading rather than only saying no.
	woReviewUnselectableNote = "the server gives this reading no id, so it can only go with the " +
		"whole sheet: leave everything unmarked and press Enter."
)

// openConfirm decides the SCOPE and refuses where the write would be a no-op,
// saying which. Nothing is sent from here.
func (s *WorkOrderScanReviewScreen) openConfirm(action woReviewAction) tea.Cmd {
	if s.writing {
		s.note = woReviewBusyNote
		return nil
	}
	sub, ok := s.currentSubmission()
	if !ok {
		s.note = "nothing to review on this work order."
		return nil
	}
	targets := s.selectionIn(sub)
	if action == woReviewComplete {
		// The completion applies the WHOLE queue and asks the server to close
		// the job, exactly as the web surface's own confirm does. A selection
		// would be a different write wearing the same key.
		targets = nil
	}
	if action != woReviewComplete && len(sub.PendingChanges) == 0 {
		s.note = woReviewNothingQueued
		return nil
	}

	s.phase = woReviewPhaseConfirm
	s.confirmAction = action
	s.confirmSub = sub.ID
	s.confirmTargets = targets
	s.confirmScroll = 0
	s.note = ""
	s.writeErr = ""
	return nil
}

const woReviewNothingQueued = "nothing is queued on this sheet — only the reason it could not be read."

func (s *WorkOrderScanReviewScreen) keyConfirm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "ctrl+x":
		if !s.confirmWrites() {
			// The bar has dropped the key and the status row says a write is
			// out, so the frame has answered already.
			return s, nil
		}
		return s, s.commit()
	case "esc":
		// Esc LEAVES while the write is out, deliberately: a frame with no way
		// off it while a slow gateway thinks is the worse defect, and it is
		// what the sibling purchasing confirms do.
		s.phase = woReviewPhaseList
		s.note = ""
		return s, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		if !s.frameDrawn(len(s.confirmHeader()), s.confirmBar()) {
			return s, nil
		}
		if !s.confirmScrolls() {
			s.note = m.String() + " moves nothing — the whole confirm is on the pane."
			return s, nil
		}
		s.confirmScroll = jdeScrollStep(m.String(), s.confirmScroll,
			s.confirmBody().Len(), s.scrollRows(len(s.confirmHeader()), s.confirmBar()))
		return s, nil
	}
	// Every other key ANSWERS. This frame holds no caret and a handful of keys,
	// so a silent return redraws a pane that is a pure function of unchanged
	// state. The note says what the KEY DID and names none — the bar makes that
	// claim, where no budget can trim it.
	s.note = m.String() + " does nothing here — this frame only confirms or cancels."
	return s, nil
}

// confirmWrites is the ONE expression behind "may Ctrl-X write". The bar reads
// it to decide whether to name the key and the arm reads it to decide whether
// to act, so the two cannot come apart.
func (s *WorkOrderScanReviewScreen) confirmWrites() bool { return !s.writing }

func (s *WorkOrderScanReviewScreen) commit() tea.Cmd {
	s.writing = true
	s.note = ""
	s.writeErr = ""
	deps, ctx, woID := s.deps, s.ctx(), s.woID
	subID, action := s.confirmSub, s.confirmAction
	// Copied before the command runs: the confirm's scope is what the operator
	// read and confirmed, and nothing a later keypress does may change what is
	// already in flight.
	targets := append([]string(nil), s.confirmTargets...)
	if len(targets) == 0 {
		// nil, never an empty slice: the client refuses an empty target_ids and
		// the server would read one as "apply nothing" (omsapi.ErrEmptyTargetIDs).
		targets = nil
	}
	return func() tea.Msg {
		switch action {
		case woReviewDiscard:
			res, err := deps.OMS.DiscardWorkOrderPendingChanges(ctx, woID, subID, targets)
			return woReviewWroteMsg{discard: res, err: err}
		case woReviewComplete:
			res, err := deps.OMS.ApplyWorkOrderPendingChanges(ctx, woID, subID, nil, true)
			return woReviewWroteMsg{applied: res, err: err, complete: true}
		default:
			res, err := deps.OMS.ApplyWorkOrderPendingChanges(ctx, woID, subID, targets, false)
			return woReviewWroteMsg{applied: res, err: err}
		}
	}
}

// wrote reports what the SERVER said. The completion in particular is never
// predicted: omr_confirm_completion closes the job only when every required
// task is done and answers 200 either way, so a confirm that asked for one and
// did not get it has to say so.
func (s *WorkOrderScanReviewScreen) wrote(m woReviewWroteMsg) (Screen, tea.Cmd) {
	s.writing = false
	if m.err != nil {
		s.writeErr = woReviewRefusal(m.err)
		return s, Status(s.writeErr, StatusError)
	}
	s.phase = woReviewPhaseList
	summary := woReviewSummary(m)
	s.note = summary
	return s, tea.Batch(Status(summary, StatusOK), s.load())
}

// woReviewRefusal recovers the server's own sentence out of these endpoints'
// refusals. Both answer a missing submission with DRF's bare {"detail": …},
// which carries no `code`, so parseError hands the whole raw body over and the
// operator would otherwise read JSON on the status row.
func woReviewRefusal(err error) string {
	if prose, ok := omsapi.AsSubmissionRefusal(err); ok {
		return prose
	}
	return err.Error()
}

// woReviewSummary is the one sentence the list reports a finished write with,
// built from the reply rather than from what was asked for.
func woReviewSummary(m woReviewWroteMsg) string {
	if m.discard != nil {
		return fmt.Sprintf("discarded %s.", woReviewPlural(m.discard.DroppedCount, "reading"))
	}
	if m.applied == nil {
		return "the server answered nothing."
	}
	out := fmt.Sprintf("applied %s.", woReviewPlural(m.applied.AppliedCount, "reading"))
	if !m.complete {
		return out
	}
	if m.applied.WorkOrderCompleted {
		return out + " the work order is completed."
	}
	// The refusal is silent server-side, so the reason is named here: it is the
	// only precondition omr_confirm_completion has.
	return out + fmt.Sprintf(" the work order is still %s — it closes only once every "+
		"required task is complete.", woReviewStatusWord(m.applied.WorkOrderStatus))
}

func woReviewStatusWord(status string) string {
	if status == "" {
		return "open"
	}
	return strings.ReplaceAll(status, "_", " ")
}

func woReviewPlural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// Grid geometry. At 80 columns the pane gives 51 cells, so the row is built
// from a fixed lead and a fixed fact with the LABEL taking whatever is left:
//
//	jdeIndent(2) + selection(3) + gap(1) + label + gap(2) + confidence(4)
//
// The confidence is a FACT and never gives — a cut percentage reads as a
// different number rather than as a shortened one — and the label is the
// bounded identifier, abbreviated with an ellipsis when it will not fit.
const (
	woReviewSelW       = 3
	woReviewConfW      = 4
	woReviewLabelFloor = 12
	woReviewLabelCeil  = 52
)

// woReviewSub is the indent a submission's or a reading's continuation lines
// hang at, under the label column.
var woReviewSubIndent = strings.Repeat(" ", len(jdeIndent)+woReviewSelW+1)

func (s *WorkOrderScanReviewScreen) labelWidth() int {
	width := 76
	if w := s.bodyWidth(); w > 0 {
		width = w
	}
	switch w := width - (len(jdeIndent) + woReviewSelW + 1 + 2 + woReviewConfW); {
	case w < woReviewLabelFloor:
		return woReviewLabelFloor
	case w > woReviewLabelCeil:
		return woReviewLabelCeil
	default:
		return w
	}
}

func woReviewGridRow(sel, label, conf string, labelW int) string {
	return jdeIndent + strings.TrimRight(strings.Join([]string{
		padCell(sel, woReviewSelW, alignLeft),
		padCell(label, labelW, alignLeft),
		padCell(conf, woReviewConfW, alignRight),
	}, " "), " ")
}

// woReviewSelCell is the three-cell selection column, and it has THREE states
// rather than two: marked, markable-and-unmarked, and a reading the server
// gives no id to — which cannot be marked at all and would otherwise be
// indistinguishable from one the operator simply has not marked yet.
func (s *WorkOrderScanReviewScreen) woReviewSelCell(sub omsapi.WorkOrderSubmission, c omsapi.WorkOrderPendingChange) string {
	if !c.Selectable() {
		return " - "
	}
	if s.marked[woReviewMark{sub.ID, c.TargetID}] {
		return "[x]"
	}
	return "[ ]"
}

// woReviewName is what the row calls a reading. _omr_label falls back to the
// raw target_id when it cannot resolve a title, and that string carries a UUID
// — so where the label IS the id, the row says what KIND of reading it is and
// the id rides its own continuation line WHOLE. An identifier is never drawn
// abbreviated; where one will not fit the layout gives, not the identifier.
func woReviewName(c omsapi.WorkOrderPendingChange) string {
	if c.Label != "" && c.Label != c.TargetID {
		return c.Label
	}
	switch c.Kind {
	case omsapi.WorkOrderChangeCheckbox, omsapi.WorkOrderChangeInk:
		return "unnamed mark"
	case omsapi.WorkOrderChangeSignature:
		return "signature"
	case omsapi.WorkOrderChangeHandwritten:
		return "handwritten note"
	case "":
		return "unnamed reading"
	}
	return c.Kind
}

// woReviewReading is what the reader SAW, which is not what applying does.
// apply_pending_changes marks a selected box DONE whatever the reading says, so
// both facts have to be on the pane or the operator decides on the wrong one —
// the caveat on the apply confirm carries the other half.
func woReviewReading(c omsapi.WorkOrderPendingChange) string {
	if c.IsMark() {
		if c.MarkedInScan() {
			return "reader: marked"
		}
		return "reader: blank"
	}
	if text := c.Text(); text != "" {
		return "reads: " + text
	}
	if c.Kind == omsapi.WorkOrderChangeSignature {
		if b, ok := c.Value.(bool); ok && b {
			return "reader: a signature is on the sheet"
		}
		return "reader: no signature found"
	}
	return "reader: " + c.Kind
}

// listLines builds the grid, tagging every line with the row it belongs to so a
// short window keeps a whole entry rather than the first line of one.
func (s *WorkOrderScanReviewScreen) listLines() (*jdeLines, int) {
	l := &jdeLines{}
	labelW := s.labelWidth()
	for i, r := range s.rows {
		sub := s.subs[r.sub]
		switch r.kind {
		case woReviewSubmissionRow:
			s.addSubmissionRow(l, i, sub, r.sub, labelW)
			continue
		case woReviewNoteRow:
			for _, line := range jdeCaveatLines(sub.ParseError, s.bodyWidth()) {
				row := StyleStatusWarn.Render(line)
				if i == s.cursor {
					row = StyleJDEFieldFocused.Render(line)
				}
				l.AddRow(i, row)
			}
			continue
		}
		c := sub.PendingChanges[r.change]
		row := woReviewGridRow(s.woReviewSelCell(sub, c),
			fitCell(woReviewName(c), labelW), woReviewPercent(c.Confidence), labelW)
		if i == s.cursor {
			row = StyleJDEFieldFocused.Render(row)
		}
		l.AddRow(i, row)

		tokens := []jdeToken{{text: woReviewReading(c), style: StyleMuted}}
		if c.AutoApplied {
			tokens = append(tokens, jdeToken{text: "pre-checked on the job", style: StyleStatusWarn})
		}
		if !c.Selectable() {
			tokens = append(tokens, jdeToken{text: "whole sheet only", style: StyleMuted})
		}
		for _, line := range jdeWrapTokens(tokens, woReviewSubIndent, s.bodyWidth()) {
			l.AddRow(i, line)
		}
		if c.TargetID != "" && (c.Label == "" || c.Label == c.TargetID) {
			// The id is the only name this reading has, so it is drawn WHOLE.
			for _, line := range woReviewIDLines(c.TargetID, woReviewSubIndent, s.bodyWidth()) {
				l.AddRow(i, line)
			}
		}
	}
	return l, len(s.rows)
}

// addSubmissionRow draws one parked sheet: what it is, when it arrived, and —
// on its own lines, whole — its UUID and the reason it could not be read.
func (s *WorkOrderScanReviewScreen) addSubmissionRow(
	l *jdeLines, i int, sub omsapi.WorkOrderSubmission, n, labelW int,
) {
	name := fmt.Sprintf("Sheet %d · %s", n+1, woReviewSource(sub.Source))
	if !sub.ReceivedAt.IsZero() {
		name += " · " + sub.ReceivedAt.Local().Format("2006-01-02 15:04")
	}
	row := woReviewGridRow("", fitCell(name, labelW),
		fmt.Sprintf("%d", len(sub.PendingChanges)), labelW)
	if i == s.cursor {
		row = StyleJDEFieldFocused.Render(row)
	}
	l.AddRow(i, row)

	// The submission id is the identifier that ties this pane to the web
	// surface and to the URL every write here posts to, so it is drawn whole on
	// a line of its own rather than abbreviated into the row above.
	for _, line := range woReviewIDLines(sub.ID, woReviewSubIndent, s.bodyWidth()) {
		l.AddRow(i, line)
	}
	if len(sub.PendingChanges) == 0 && sub.ParseError == "" {
		l.AddRow(i, woReviewSubIndent+StyleMuted.Render(
			"parked for review with nothing queued."))
	}
}

// woReviewIDLines draws an identifier WHOLE, WRAPPED rather than clipped.
//
// A UUID is 36 cells and `id: ` plus this indent is another ten, so at 80
// columns it fits the 51-cell pane exactly and at 79 it does not. A clipped
// identifier is worse than an absent one — it reads as a different record, the
// way a cut price reads as a different price — so where it will not fit the
// LAYOUT gives: the id runs on to the next line, whole, and there is no
// ellipsis because nothing was dropped. It is hard-wrapped rather than
// word-wrapped because an id has no spaces to break at.
func woReviewIDLines(id, indent string, width int) []string {
	text := "id: " + id
	if width <= 0 {
		return []string{indent + StyleMuted.Render(text)}
	}
	room := width - lipgloss.Width(indent)
	if room < 1 {
		room = 1
	}
	var out []string
	for r := []rune(text); len(r) > 0; {
		n := room
		if n > len(r) {
			n = len(r)
		}
		out = append(out, indent+StyleMuted.Render(string(r[:n])))
		r = r[n:]
	}
	return out
}

func woReviewSource(source string) string {
	switch source {
	case "scan":
		return "flatbed scan"
	case "email":
		return "emailed"
	case "manual":
		return "uploaded here"
	case "":
		return "sheet"
	}
	return source
}

// woReviewPercent renders a confidence as whole percent. It never gives: a cut
// percentage reads as a different number, and this is the figure a reviewer
// decides a doubtful mark on.
func woReviewPercent(confidence float64) string {
	return fmt.Sprintf("%d%%", int(confidence*100+0.5))
}

// listHeader is the grid's chrome, PINNED above the rows.
//
// The COLUMN HEADER is essential: an operator reading a grid needs to know
// which column is which, and on the empty frame the "nothing to review"
// sentence takes that row because it is the only thing on the pane that says
// what to do next. Everything else is context — a short pane may have it.
func (s *WorkOrderScanReviewScreen) listHeader() jdeHeader {
	h := jdeHeader(nil)
	if s.loadErr != "" {
		h = h.add(jdeHeadContext,
			StyleStatusError.Render("Error: ")+fitCellIf(s.loadErr, s.bodyWidth()-poErrPrefixW), "")
	}
	if len(s.rows) == 0 {
		// Bounded against the LIVE pane: this is the header's one essential row
		// on the empty frame, jdeFitHeader does no width fitting at all, and an
		// over-wide essential row is what clampToBox cuts from the right with no
		// ellipsis — taking the closing SGR reset with it.
		return h.add(jdeHeadContext, StyleJDEHeading.Render("Scan review")).
			add(jdeHeadEssential, jdeIndent+StyleMuted.Render(fitCellIf(
				"No scanned sheet is waiting here.", s.headerRoom())))
	}
	h = h.add(jdeHeadContext, StyleJDEHeading.Render(fmt.Sprintf(
		"Scan review · %s", woReviewPlural(len(s.subs), "sheet"))))
	// The one thing a confidence figure does NOT say, on the frame where it is
	// the only evidence: there is no picture here to check it against.
	width := s.bodyWidth()
	h = h.addFittedBlock(jdeHeadContext, jdeCaveatLines(woScanImageCaveat, width),
		func(rows int) []string { return jdeCaveatLinesIn(woScanImageCaveat, width, rows) })
	return h.add(jdeHeadEssential, StyleMuted.Render(
		woReviewGridRow("Sel", "Reading", "Conf", s.labelWidth())))
}

// woScanImageCaveat is named rather than written inline so the builder and the
// fold-mark sweep read one string.
const woScanImageCaveat = "The scanned image is not drawable in a terminal — judge a reading " +
	"by its confidence and the paper in your hand."

// listBar names the keys that work on the grid, and only those.
func (s *WorkOrderScanReviewScreen) listBar() []actionBarItem {
	return s.listBarItems(s.listPages())
}

// listBarItems builds the grid's bar for a given paging state, so the bar that
// is MEASURED against the pane is the bar that is DRAWN on it.
// The three write keys and Space are named PER ROW, because that is where each
// of them can act. A sheet the reader could not align has nothing to apply or
// discard, and a sheet row is not a reading to mark — so on those rows the keys
// only decline, and a bar naming them would be advertising a key that cannot
// work where the cursor is standing. The arms still ANSWER when pressed
// anyway: declining and saying why is not acting, and silence on a frame with
// no caret is the reported hang.
func (s *WorkOrderScanReviewScreen) listBarItems(paging bool) []actionBarItem {
	var items []actionBarItem
	if s.queueUnderCursor() {
		items = append(items, actionBarItem{"Enter", "Apply"}, actionBarItem{"Ctrl-X", "Discard"})
	}
	if _, ok := s.currentSubmission(); ok {
		items = append(items, actionBarItem{"Ctrl-F", "Complete"})
	}
	if s.markUnderCursor() {
		items = append(items, actionBarItem{"Space", "Mark"})
	}
	items = append(items, actionBarItem{"Esc", "Back"})
	if jdeRowMoves(len(s.rows)) {
		items = append(items, actionBarItem{"UP/DN", "Move"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return append(items, actionBarItem{"r", "Refresh"})
}

// queueUnderCursor and markUnderCursor are the ONE expression each behind
// "may Enter/Ctrl-X open a confirm" and "may Space mark": the bar reads them to
// decide whether to name the key and the arms read the same state to decide
// whether to act, so the two cannot come apart.
func (s *WorkOrderScanReviewScreen) queueUnderCursor() bool {
	sub, ok := s.currentSubmission()
	return ok && len(sub.PendingChanges) > 0
}

func (s *WorkOrderScanReviewScreen) markUnderCursor() bool {
	c, ok := s.currentChange()
	return ok && c.Selectable()
}

// listPages is the ONE condition both the bar and the paging arm read, settled
// against the bar WITH the paging entry on it — the tallest bar and therefore
// the smallest body budget, which is the fixed point.
func (s *WorkOrderScanReviewScreen) listPages() bool {
	if len(s.rows) == 0 {
		return false
	}
	body, _ := s.listLines()
	return s.bodyPagesForBar(body, len(s.rows), len(s.listHeader()), s.listBarItems(true))
}

// listStatus reports what is in flight and what the last keypress DID. A write
// beats a load: both can be out at once (`r` mid-write), and of the two the one
// an operator would act on is the one that changes the job.
func (s *WorkOrderScanReviewScreen) listStatus() string {
	if s.writing {
		return s.leadOnto("Working…")
	}
	if s.writeErr != "" {
		return s.statusRow(false, "", s.writeErr)
	}
	if s.loading {
		return s.leadOnto("Loading…")
	}
	return s.statusAnswer(StatusInfo, s.note)
}

// leadOnto puts the screen's answer ahead of the work in flight without
// displacing it — statusRow's saving branch wins outright, so a note handed to
// it while a write is out would be drawn by nothing at all.
func (s *WorkOrderScanReviewScreen) leadOnto(verb string) string {
	switch room := s.bodyWidth(); {
	case s.note == "":
	case room > 0:
		verb = poLeadOnto(s.note, verb, room)
	default:
		verb = s.note + poLeadJoint + verb
	}
	return s.statusRow(true, verb, "")
}

func (s *WorkOrderScanReviewScreen) viewList() string {
	body, _ := s.listLines()
	return s.frameWrapped(s.listHeader(), body, s.cursor, s.listStatus(), s.listBar())
}

func (s *WorkOrderScanReviewScreen) View() string {
	if s.phase == woReviewPhaseConfirm {
		return s.viewConfirm()
	}
	return s.viewList()
}

// ---------------------------------------------------------------------------
// The confirm
// ---------------------------------------------------------------------------

// confirmSubmission is the sheet the open confirm is about, found by ID rather
// than by the cursor: the cursor is free to have moved, and a reload can
// reorder the list under an open confirm.
func (s *WorkOrderScanReviewScreen) confirmSubmission() (omsapi.WorkOrderSubmission, bool) {
	for _, sub := range s.subs {
		if sub.ID == s.confirmSub {
			return sub, true
		}
	}
	return omsapi.WorkOrderSubmission{}, false
}

// confirmChanges is exactly what the write will act on, in the order the server
// sent them — which is what the operator is being asked to approve. A
// whole-queue write lists the whole queue; a selective one lists its selection.
func (s *WorkOrderScanReviewScreen) confirmChanges() []omsapi.WorkOrderPendingChange {
	sub, ok := s.confirmSubmission()
	if !ok {
		return nil
	}
	if len(s.confirmTargets) == 0 {
		return sub.PendingChanges
	}
	named := map[string]bool{}
	for _, t := range s.confirmTargets {
		named[t] = true
	}
	var out []omsapi.WorkOrderPendingChange
	for _, c := range sub.PendingChanges {
		if c.Selectable() && named[c.TargetID] {
			out = append(out, c)
		}
	}
	return out
}

// confirmVerb is what the frame is asking about, said once.
func (s *WorkOrderScanReviewScreen) confirmVerb() string {
	switch s.confirmAction {
	case woReviewDiscard:
		return "Discard"
	case woReviewComplete:
		return "Complete from scan"
	}
	return "Apply"
}

// confirmHeadline names WHAT is about to happen and to HOW MANY readings, and
// it is the header's one essential row: wherever this frame is drawn at all it
// says what Ctrl-X will do. The scope word is what separates the two writes an
// operator has to keep apart — a selection, or the whole sheet.
func (s *WorkOrderScanReviewScreen) confirmHeadline() string {
	n := len(s.confirmChanges())
	scope := "the whole sheet"
	if len(s.confirmTargets) > 0 {
		scope = "your selection"
	}
	switch s.confirmAction {
	case woReviewDiscard:
		return fmt.Sprintf("Discard %s — %s", woReviewPlural(n, "reading"), scope)
	case woReviewComplete:
		// The CONSEQUENCE leads and the count follows, because fitCellIf trims
		// from the tail: a headline cut after "Apply 4 readings and close the
		// work ord…" states the harmless half of what Ctrl-X is about to do and
		// loses the half an operator would stop for.
		return fmt.Sprintf("Close the work order · applies %s", woReviewPlural(n, "reading"))
	}
	return fmt.Sprintf("Apply %s — %s", woReviewPlural(n, "reading"), scope)
}

// headerRoom is what an indented header row has to say something in. It is the
// pane less this file's own indent and nothing else: jdeStripWidth is for a row
// that sits under a LABEL COLUMN and reserves one, which none of these do — used
// here it cost the completion headline eleven cells and cut "close the work
// order" off the one row the frame may not be drawn without.
func (s *WorkOrderScanReviewScreen) headerRoom() int {
	if w := s.bodyWidth(); w > 0 {
		return w - len(jdeIndent)
	}
	return 0
}

func (s *WorkOrderScanReviewScreen) confirmHeader() jdeHeader {
	h := jdeHeader(nil).add(jdeHeadDecorative, StyleStatusWarn.Render(s.confirmVerb()), "")
	h = h.add(jdeHeadEssential, jdeIndent+StyleJDEHeading.Render(
		fitCellIf(s.confirmHeadline(), s.headerRoom())))
	return h.add(jdeHeadDecorative, "")
}

// confirmBody is WHAT IS ABOUT TO BE WRITTEN, listed reading by reading, then
// the caveats. Said ONCE, so the lines the bar is measured against are the
// lines the frame draws.
//
// The list is the whole point of the frame: an "apply" an operator cannot read
// first is the same dead end this screen exists to remove, moved one keypress
// later. It owns no navigable row — there is nothing here to choose — so the
// window over it is positioned by an OFFSET (frameScrolled) rather than by a
// cursor, which is what keeps a caveat below the fold reachable.
func (s *WorkOrderScanReviewScreen) confirmBody() *jdeLines {
	body := &jdeLines{}
	changes := s.confirmChanges()
	if len(changes) == 0 {
		body.Add(jdeIndent + StyleMuted.Render("No readings — nothing is queued on this sheet."))
	}
	labelW := s.labelWidth()
	for _, c := range changes {
		body.Add(woReviewGridRow("", fitCell(woReviewName(c), labelW),
			woReviewPercent(c.Confidence), labelW))
		for _, line := range jdeWrapTokens(
			[]jdeToken{{text: woReviewReading(c), style: StyleMuted}},
			woReviewSubIndent, s.bodyWidth()) {
			body.Add(line)
		}
		if c.TargetID != "" {
			for _, line := range woReviewIDLines(c.TargetID, woReviewSubIndent, s.bodyWidth()) {
				body.Add(line)
			}
		}
	}
	body.Add("")
	for _, caveat := range s.confirmCaveats() {
		for _, line := range jdeCaveatLines(caveat, s.bodyWidth()) {
			body.Add(line)
		}
	}
	return body
}

// confirmCaveats is what the operator could not work out from the list above.
//
// Every one of them is a consequence measured on a real backend rather than
// read off a docstring, and each is here because it is the OPPOSITE of what the
// row above it appears to say.
func (s *WorkOrderScanReviewScreen) confirmCaveats() []string {
	switch s.confirmAction {
	case woReviewDiscard:
		return []string{
			"Discarding drops these readings for good — the sheet would have to be " +
				"scanned again.",
			"A reading already pre-checked on the job is UNDONE, so discarding one is a " +
				"correction rather than a no-op.",
		}
	case woReviewComplete:
		return []string{
			"Applies every reading on this sheet and asks the server to close the work " +
				"order. It closes only once every required task is complete, and says which " +
				"happened.",
			"Accepting a mark records its task or material as DONE whatever the reader " +
				"saw in the box, and a material mark moves stock.",
		}
	}
	return []string{
		"Accepting a mark records its task or material as DONE whatever the reader saw " +
			"in the box — a row reading `blank` ends up complete.",
		"A material mark moves stock: accepting one draws the item down and logs the usage.",
		"This does NOT close the work order. Ctrl-F on the list is the key that asks for that.",
	}
}

func (s *WorkOrderScanReviewScreen) confirmScrolls() bool {
	return s.bodyScrollsForBar(s.confirmBody(),
		len(s.confirmHeader()), s.confirmBarItems(s.confirmWrites(), true))
}

func (s *WorkOrderScanReviewScreen) confirmBar() []actionBarItem {
	return s.confirmBarItems(s.confirmWrites(), s.confirmScrolls())
}

// confirmBarItems is confirmBar for a given write and scroll state, so the bar
// MEASURED against the pane is the bar DRAWN on it.
func (s *WorkOrderScanReviewScreen) confirmBarItems(writes, scroll bool) []actionBarItem {
	items := []actionBarItem{{"Ctrl-X", s.confirmVerb()}, {"Esc", "Cancel"}}
	if !writes {
		// Esc still LEAVES while the write is out; the bar keeps the key that
		// acts and drops the one that would not.
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

// confirmStatus is the confirm's one status row: what is in flight, what the
// last write refused with, and what the last keypress DID — the last of those
// at StatusInfo, because "this key is inert here" is not a failure and must not
// wear the error mark on the frame where the difference decides whether the
// operator presses Ctrl-X.
func (s *WorkOrderScanReviewScreen) confirmStatus() string {
	if s.writing {
		return s.leadOnto(s.confirmVerb() + "…")
	}
	if s.writeErr != "" {
		return s.statusRow(false, "", s.writeErr)
	}
	return s.statusAnswer(StatusInfo, s.note)
}

func (s *WorkOrderScanReviewScreen) viewConfirm() string {
	frame, offset := s.frameScrolled(
		s.confirmHeader(), s.confirmBody(), s.confirmScroll,
		s.confirmStatus(), s.confirmBar())
	// Stored back so the offset this screen holds is the one that was DRAWN:
	// `end` asks for the whole body and the frame clamps it against the pane it
	// has, which is what makes "↓ 0 more below" impossible.
	s.confirmScroll = offset
	return frame
}
