package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// woMode selects which overlay (if any) the WO-detail screen is showing.
// woModeView is the normal read-only body; every other mode is a modal that
// captures all keys via WantsRawInput so the global hotkey layer can't steal
// characters from a textinput or a picker cursor.
type woMode int

const (
	woModeView         woMode = iota
	woModeTasks               // task-completion picker (space toggles the highlighted task)
	woModeMaterials           // material-usage picker (space toggles was-used)
	woModeAddMaterial         // add an ad-hoc material line (name/qty/unit/cost/stock/receipt)
	woModeMaterialCost        // re-price one material line (unit cost only)
	woModePhoto               // add-photo form (file path + optional caption)
	woModePdf                 // upload scanned-PDF form (file path)
	woModeChecklist           // pre-finalization validation checklist (3 acks + notes)
	woModeConfirm             // y/n confirm for a finalizing/destructive status transition
	woModeNotes               // edit work-order notes (updateWorkOrder)
)

// Add-material form fields, in render order. The stock-item slot is a picker
// rather than a textinput, so its entry in the inputs slice goes unused —
// the same layout asset_part_form uses for its part FK.
const (
	woMatName = iota
	woMatQuantity
	woMatUnit
	woMatUnitCost
	woMatItem
	woMatReceipt
	woMatFieldMax
)

var woMaterialFieldLabel = map[int]string{
	woMatName:     "Material",
	woMatQuantity: "Quantity used",
	woMatUnit:     "Unit",
	woMatUnitCost: "Unit cost ($)",
	woMatItem:     "Stock item",
	woMatReceipt:  "Receipt image",
}

// woItemPickRow is one row of the add-material stock picker: the "(none)" clear
// row at the top, then one row per inventory item.
type woItemPickRow struct {
	id    string // "" on the clear row
	label string
}

type WorkOrderDetailScreen struct {
	deps           Deps
	woID           string
	wo             *omsapi.WorkOrder
	loading        bool
	loadErr        string
	actionMsg      string
	actionLvl      StatusLevel
	scroller       *TextScroller
	terminalHeight int

	mode woMode

	// Task / material pickers. cursor indexes into wo.TaskCompletions /
	// wo.MaterialUsage; actionPending gates a second toggle while one is in
	// flight so an impatient double-press can't fire duplicate PATCHes.
	taskCursor     int
	materialCursor int
	actionPending  bool

	// Add-photo form. photoTaskID pins the upload to one step (evidence) and is
	// empty for a work-order-level photo; photoTaskTitle labels the form.
	// photoReturn is the mode to fall back to on cancel or success, so filing
	// evidence from the task picker lands the operator back on the step list
	// rather than dumping them out to the body.
	photoPathIn    textinput.Model
	photoCaptionIn textinput.Model
	photoFocus     int // 0 = path, 1 = caption
	photoErr       string
	photoPending   bool
	photoTaskID    string
	photoTaskTitle string
	photoReturn    woMode

	// Add-material form (op-768w) — the ad-hoc line. Field cursor + picker
	// sub-phase follow asset_part_form's idiom: tab/arrows move, space opens the
	// picker on the stock-item field, enter submits. amItemID is the linked
	// InventoryItem ("" = an out-of-pocket buy, which moves no stock), and the
	// item list is fetched only while the form is open so the work-order screen
	// never pays for a picker nobody opened.
	amInputs     []textinput.Model
	amCursor     int
	amItemID     string
	amItemLabel  string
	amItems      []omsapi.Item
	amItemsErr   string
	amPicking    bool
	amPickCursor int
	amPickSearch textinput.Model
	amPickTyping bool
	amPickRows   []woItemPickRow
	amErr        string
	amPending    bool

	// Re-price one material line. The toggle is the only write endpoint for a
	// line's cost, so saving sends the line's CURRENT was_used unchanged.
	costIn      textinput.Model
	costErr     string
	costPending bool

	// PM-template per-unit prices keyed by MaintenanceMaterial id — the only
	// thing "actual vs estimated" can be measured against, and they do not ride
	// the work-order payload. estimatesFor is the template already fetched, so
	// the extra call happens once rather than on every reload.
	estimates    map[string]omsapi.DecimalString
	estimatesFor string

	// Upload-PDF form.
	pdfPathIn  textinput.Model
	pdfErr     string
	pdfPending bool

	// Validation checklist. finalizeAfterValidate is set when the checklist
	// was opened because a complete-transition hit the 412 validation gate;
	// on a successful validate we re-attempt the completion, mirroring the
	// web WorkOrderPage finalize flow.
	ackElectrical         bool
	ackLoto               bool
	ackRequired           bool
	checklistNotesIn      textinput.Model
	checklistFocus        int // 0 = electrical, 1 = loto, 2 = required, 3 = notes
	checklistErr          string
	checklistPending      bool
	finalizeAfterValidate bool

	// Confirm overlay. confirmAction is the status to transition to when the
	// operator answers 'y'; transitioning gates the async transition.
	confirmAction string
	confirmPrompt string
	transitioning bool

	// Edit-notes form (updateWorkOrder). Mirrors the WorkOrderPage notes
	// textarea + Save button.
	notesIn      textinput.Model
	notesErr     string
	notesPending bool

	// Elapsed timer (op-m3so). The server owns every total: timerPending gates a
	// second toggle while one is in flight, and tickOffset is the seconds counted
	// locally since the last fetch so a running clock visibly advances between
	// refreshes.
	//
	// ticking + tickGen keep exactly one 1s chain alive. The generation is what
	// makes that safe across navigation: this screen instance is restored from
	// the back stack, so a tick from the chain we abandoned on the way out can
	// still land after Init has armed a fresh one. Two live chains would count
	// seconds twice as fast, so a tick from a stale generation is dropped
	// instead of re-arming.
	timerPending bool
	ticking      bool
	tickGen      int
	tickOffset   int
}

type woDetailLoadedMsg struct {
	wo  *omsapi.WorkOrder
	err error
}

type woTransitionedMsg struct {
	action string
	wo     *omsapi.WorkOrder
	err    error
}

type woTaskToggledMsg struct{ err error }
type woMaterialToggledMsg struct{ err error }

// woMaterialAddedMsg / woMaterialRemovedMsg / woMaterialCostSavedMsg report the
// three ad-hoc-material writes (op-768w). name travels on the removal so the
// status line can say what left the job after the row is already gone.
type woMaterialAddedMsg struct {
	name string
	err  error
}
type woMaterialRemovedMsg struct {
	name string
	err  error
}
type woMaterialCostSavedMsg struct{ err error }

// woItemsLoadedMsg carries the stock picker's options for the add-material form.
// A failure is not fatal: linking stock is optional, so the form says the list
// is unavailable and the line can still be added as an out-of-pocket buy.
type woItemsLoadedMsg struct {
	items []omsapi.Item
	err   error
}

// woEstimatesLoadedMsg carries the PM template's per-unit prices, keyed by
// MaintenanceMaterial id. key is the template it was fetched for. An error
// simply drops the estimate half of the totals line — the actual spend stands
// on its own — so it carries no message for the operator.
type woEstimatesLoadedMsg struct {
	key   string
	costs map[string]omsapi.DecimalString
	err   error
}
type woPhotoAddedMsg struct{ err error }
type woPdfUploadedMsg struct {
	result *omsapi.WorkOrderUploadResult
	err    error
}
type woValidatedMsg struct{ err error }
type woNotesSavedMsg struct {
	wo  *omsapi.WorkOrder
	err error
}

// woTimerToggledMsg reports a start/pause. label names what was clocked ("timer"
// or the step title) so the status line says which clock moved — starting a step
// can pause a different one, and the operator should see which action landed.
type woTimerToggledMsg struct {
	action string
	label  string
	err    error
}

// woTickMsg advances the locally-displayed seconds by one while a clock runs.
// gen identifies the chain that emitted it, so ticks from an abandoned chain can
// be told apart from the live one.
type woTickMsg struct{ gen int }

func NewWorkOrderDetailScreen(deps Deps, id string) *WorkOrderDetailScreen {
	return &WorkOrderDetailScreen{
		deps:     deps,
		woID:     id,
		loading:  true,
		scroller: NewTextScroller(defaultDetailHeight),
	}
}

func (s *WorkOrderDetailScreen) Title() string {
	if s.wo != nil && s.wo.Title != "" {
		return fmt.Sprintf("WO: %s", s.wo.Title)
	}
	return fmt.Sprintf("WO #%s", s.woID)
}

// Init (re)loads the work order. It also drops any tick chain this screen had
// running: the back stack hands the same instance back on a return visit, and
// the chain we left behind fired its last tick into whatever screen replaced us.
// Clearing the flag lets the reload arm a fresh one; the generation bump keeps
// the abandoned tick from arming a second.
func (s *WorkOrderDetailScreen) Init() tea.Cmd {
	s.stopTicking()
	return s.load()
}

// WantsRawInput routes every key to the screen while any modal is open so the
// textinputs and picker cursors receive characters the global dispatcher
// would otherwise claim (m, /, esc, uppercase shortcuts, …).
func (s *WorkOrderDetailScreen) WantsRawInput() bool { return s.mode != woModeView }

// HandlesKey claims uppercase 'M' (the material list) so it beats the global
// PM-items hotkey — the sc-k7p LocalKeyScreen pattern. Without the claim the key
// the footer advertises navigates away from the work order instead, which since
// op-768w means the ONLY way a corrective work order can record what it consumed
// is unreachable. Only consulted in the normal view: any open modal flips
// WantsRawInput true, which routes every key here first.
func (s *WorkOrderDetailScreen) HandlesKey(key string) bool { return key == "M" }

func (s *WorkOrderDetailScreen) load() tea.Cmd {
	deps := s.deps
	id := s.woID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		wo, err := deps.OMS.GetWorkOrder(ctx, id)
		return woDetailLoadedMsg{wo: wo, err: err}
	}
}

func (s *WorkOrderDetailScreen) transition(action string) tea.Cmd {
	deps := s.deps
	id := s.woID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		wo, err := deps.OMS.TransitionWorkOrder(ctx, id, action, "")
		return woTransitionedMsg{action: action, wo: wo, err: err}
	}
}

func (s *WorkOrderDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case woDetailLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.wo = m.wo
		s.clampCursors()
		// Re-anchor the stopwatch on the server's fresh totals before rendering:
		// the value that just arrived already includes the running segment, so
		// the local offset starts over from zero.
		cmd := s.syncTicking()
		s.refreshBody()
		return s, tea.Batch(cmd, s.loadEstimates())
	case woTransitionedMsg:
		s.transitioning = false
		if m.err != nil {
			// A completed-transition can trip the backend's 412 validation
			// gate (code validation_required). Mirror the web: open the
			// checklist and re-attempt the completion once it's satisfied.
			var apiErr *omsapi.APIError
			if m.action == "completed" && errors.As(m.err, &apiErr) && apiErr.Status == 412 {
				s.openChecklist(true)
				return s, Status("validation required — confirm the checklist", StatusWarn)
			}
			s.setAction(fmt.Sprintf("%s failed: %s", m.action, m.err.Error()), StatusError)
			return s, Status(s.actionMsg, StatusError)
		}
		s.setAction(fmt.Sprintf("%s OK", m.action), StatusOK)
		s.wo = m.wo
		s.clampCursors()
		// Completing a work order finalizes its clocks server-side, so the tick
		// chain has to re-read is_timing here rather than keep counting.
		cmd := s.syncTicking()
		s.refreshBody()
		return s, tea.Batch(Status(s.actionMsg, StatusOK), cmd)
	case woTaskToggledMsg:
		s.actionPending = false
		if m.err != nil {
			return s, Status("task update failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(Status("task updated", StatusOK), s.load())
	case woMaterialToggledMsg:
		s.actionPending = false
		if m.err != nil {
			return s, Status("material update failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(Status("material updated", StatusOK), s.load())
	case woMaterialAddedMsg:
		s.amPending = false
		if m.err != nil {
			s.amErr = m.err.Error()
			return s, Status("add material failed: "+m.err.Error(), StatusError)
		}
		// Back to the list, which is where the new line has to be marked used —
		// adding it records the plan, toggling it is what moves the stock.
		s.mode = woModeMaterials
		note := m.name + " added — press space to mark it used"
		s.setAction(note, StatusOK)
		return s, tea.Batch(Status(note, StatusOK), s.load())
	case woMaterialRemovedMsg:
		s.actionPending = false
		if m.err != nil {
			return s, Status("remove material failed: "+m.err.Error(), StatusError)
		}
		note := m.name + " removed"
		s.setAction(note, StatusOK)
		return s, tea.Batch(Status(note, StatusOK), s.load())
	case woMaterialCostSavedMsg:
		s.costPending = false
		if m.err != nil {
			s.costErr = m.err.Error()
			return s, Status("save cost failed: "+m.err.Error(), StatusError)
		}
		s.mode = woModeMaterials
		s.setAction("cost saved", StatusOK)
		return s, tea.Batch(Status("cost saved", StatusOK), s.load())
	case woItemsLoadedMsg:
		if m.err != nil {
			s.amItemsErr = m.err.Error()
			return s, nil
		}
		s.amItemsErr = ""
		s.amItems = m.items
		s.applyItemFilter()
		return s, nil
	case woEstimatesLoadedMsg:
		if m.err != nil {
			// Let the next explicit reload try again rather than sitting on a
			// transient failure for the life of the screen.
			if s.estimatesFor == m.key {
				s.estimatesFor = ""
			}
			return s, nil
		}
		s.estimates = m.costs
		s.refreshBody()
		return s, nil
	case woPhotoAddedMsg:
		s.photoPending = false
		if m.err != nil {
			s.photoErr = m.err.Error()
			return s, Status("add photo failed: "+m.err.Error(), StatusError)
		}
		note := "photo added"
		if s.photoTaskID != "" {
			note = "evidence photo added to step"
		}
		s.mode = s.photoReturn
		s.setAction(note, StatusOK)
		return s, tea.Batch(Status(note, StatusOK), s.load())
	case woPdfUploadedMsg:
		s.pdfPending = false
		if m.err != nil {
			s.pdfErr = m.err.Error()
			return s, Status("upload PDF failed: "+m.err.Error(), StatusError)
		}
		s.mode = woModeView
		s.setAction(uploadResultSummary(m.result), StatusOK)
		// Reload — if the scan belonged to this WO its task completions may
		// have changed.
		return s, tea.Batch(Status("PDF uploaded", StatusOK), s.load())
	case woValidatedMsg:
		s.checklistPending = false
		if m.err != nil {
			s.checklistErr = m.err.Error()
			return s, Status("validation failed: "+m.err.Error(), StatusError)
		}
		s.mode = woModeView
		if s.finalizeAfterValidate {
			s.finalizeAfterValidate = false
			s.transitioning = true
			return s, tea.Batch(Status("validated — completing", StatusOK), s.transition("completed"))
		}
		s.setAction("validation recorded", StatusOK)
		return s, tea.Batch(Status("validation recorded", StatusOK), s.load())
	case woNotesSavedMsg:
		s.notesPending = false
		if m.err != nil {
			s.notesErr = m.err.Error()
			return s, Status("save notes failed: "+m.err.Error(), StatusError)
		}
		s.mode = woModeView
		s.wo = m.wo
		s.clampCursors()
		cmd := s.syncTicking()
		s.refreshBody()
		s.setAction("notes saved", StatusOK)
		return s, tea.Batch(Status("notes saved", StatusOK), cmd)
	case woTimerToggledMsg:
		s.timerPending = false
		if m.err != nil {
			return s, Status(fmt.Sprintf("%s %s failed: %s", m.label, m.action, m.err.Error()), StatusError)
		}
		// Re-fetch rather than trust the echoed row: starting a step pauses
		// whichever other step was running and can flip an open WO to
		// in_progress, and neither shows up in the single object returned.
		note := fmt.Sprintf("%s %s", m.label, timerPastTense(m.action))
		return s, tea.Batch(Status(note, StatusOK), s.load())
	case woTickMsg:
		// A stale tick — from a chain that was stopped, superseded on the way
		// back through the nav stack, or whose clock the server has since paused
		// — dies here rather than re-arming.
		if !s.ticking || m.gen != s.tickGen {
			return s, nil
		}
		if !s.anyTiming() {
			s.stopTicking()
			return s, nil
		}
		s.tickOffset++
		s.refreshBody()
		return s, s.tickCmd()

	case tea.KeyMsg:
		switch s.mode {
		case woModeTasks:
			return s.handleTasksKey(m)
		case woModeMaterials:
			return s.handleMaterialsKey(m)
		case woModeAddMaterial:
			return s.handleAddMaterialKey(m)
		case woModeMaterialCost:
			return s.handleMaterialCostKey(m)
		case woModePhoto:
			return s.handlePhotoKey(m)
		case woModePdf:
			return s.handlePdfKey(m)
		case woModeChecklist:
			return s.handleChecklistKey(m)
		case woModeConfirm:
			return s.handleConfirmKey(m)
		case woModeNotes:
			return s.handleNotesKey(m)
		}
		if s.scroller.Handle(m) {
			return s, nil
		}
		return s.handleViewKey(m)
	}
	return s, nil
}

// handleViewKey dispatches the action hotkeys available on the read-only body.
// The keys are chosen to dodge the global hotkey layer (m/a/l/n/o/u/f/e and
// the uppercase workspace shortcuts) — see internal/tui/app.go.
func (s *WorkOrderDetailScreen) handleViewKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "r":
		s.loading = true
		s.loadErr = ""
		return s, s.load()
	case "i":
		return s, s.transition("in_progress")
	case "b":
		return s, s.transition("blocked")
	case "c":
		// Finalizing — confirm before completing.
		s.openConfirm("completed", "Mark this work order COMPLETED?")
		return s, nil
	case "x":
		// Destructive — confirm before cancelling.
		s.openConfirm("cancelled", "CANCEL this work order?")
		return s, nil
	case "s":
		// Stopwatch for the whole job. Lowercase s is free in the global hotkey
		// set, so no LocalKeyScreen claim is needed to reach this.
		return s.toggleWOTimer()
	case "t":
		if s.wo == nil || len(s.wo.TaskCompletions) == 0 {
			return s, Status("no tasks to complete", StatusWarn)
		}
		s.mode = woModeTasks
		s.taskCursor = 0
		return s, nil
	case "M":
		// Opens even with nothing in the list: a CORRECTIVE work order has no PM
		// template, so it arrives with zero material rows and adding one from
		// here is the only way it records a material at all (op-768w).
		if s.wo == nil {
			return s, nil
		}
		s.mode = woModeMaterials
		s.materialCursor = 0
		return s, nil
	case "p":
		s.openPhotoForm("", "")
		return s, textinput.Blink
	case "U":
		s.openPdfForm()
		return s, textinput.Blink
	case "v":
		s.openChecklist(false)
		return s, textinput.Blink
	case "E":
		s.openNotesForm()
		return s, textinput.Blink
	}
	return s, nil
}

// --- Task picker -----------------------------------------------------------

func (s *WorkOrderDetailScreen) handleTasksKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := len(s.wo.TaskCompletions)
	switch m.String() {
	case "esc", "q":
		s.mode = woModeView
		return s, nil
	case "up", "k":
		if s.taskCursor > 0 {
			s.taskCursor--
		}
		return s, nil
	case "down", "j":
		if s.taskCursor < n-1 {
			s.taskCursor++
		}
		return s, nil
	case " ", "enter":
		if s.actionPending {
			return s, nil
		}
		return s, s.toggleTask()
	case "p":
		// Evidence — "here is what I did" — filed under the highlighted step
		// rather than the work order as a whole.
		if n == 0 {
			return s, nil
		}
		t := s.wo.TaskCompletions[s.taskCursor]
		s.openPhotoForm(fmt.Sprintf("%v", t.ID), t.TaskTitle)
		return s, textinput.Blink
	case "s":
		// Same key as the whole-job stopwatch on the body: here it clocks the
		// highlighted step instead. The picker takes raw input, so nothing
		// upstream can claim it.
		if n == 0 {
			return s, nil
		}
		return s.toggleStepTimer()
	}
	return s, nil
}

func (s *WorkOrderDetailScreen) toggleTask() tea.Cmd {
	task := s.wo.TaskCompletions[s.taskCursor]
	taskID := fmt.Sprintf("%v", task.ID)
	woID := s.woID
	next := !task.IsCompleted
	s.actionPending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		_, err := deps.OMS.CompleteWorkOrderTask(ctx, woID, taskID, next, "")
		return woTaskToggledMsg{err: err}
	}
}

// --- Elapsed timer ---------------------------------------------------------

// woTimerAllowed reports whether the stopwatch can still be driven in this
// work-order state. Completing a WO finalizes its clocks on the backend and
// stamps the total onto the maintenance log, so restarting one afterwards would
// misreport the job — the web disables the button for the same reason.
func woTimerAllowed(status string) bool {
	switch status {
	case "open", "in_progress", "blocked":
		return true
	}
	return false
}

// toggleWOTimer starts or pauses the whole-job clock. The action is derived from
// the last-fetched is_timing, and the endpoint is idempotent, so a stale view
// (someone paused it on the web) costs a no-op round trip, never a bad total.
func (s *WorkOrderDetailScreen) toggleWOTimer() (Screen, tea.Cmd) {
	if s.wo == nil || s.timerPending {
		return s, nil
	}
	if !woTimerAllowed(s.wo.Status) {
		return s, Status("timer closed with the work order", StatusWarn)
	}
	action := omsapi.TimerStart
	if s.wo.IsTiming {
		action = omsapi.TimerPause
	}
	s.timerPending = true
	woID := s.woID
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		_, err := deps.OMS.TimerWorkOrder(ctx, woID, action)
		return woTimerToggledMsg{action: action, label: "timer", err: err}
	}
}

// toggleStepTimer starts or pauses the highlighted step's clock. Only one step
// per work order runs at a time — the backend pauses the others — so this fires
// and re-fetches rather than predicting which clocks moved.
func (s *WorkOrderDetailScreen) toggleStepTimer() (Screen, tea.Cmd) {
	if s.wo == nil || s.timerPending || len(s.wo.TaskCompletions) == 0 {
		return s, nil
	}
	if !woTimerAllowed(s.wo.Status) {
		return s, Status("timer closed with the work order", StatusWarn)
	}
	task := s.wo.TaskCompletions[s.taskCursor]
	action := omsapi.TimerStart
	if task.IsTiming {
		action = omsapi.TimerPause
	}
	label := task.TaskTitle
	if label == "" {
		label = "step"
	}
	taskID := fmt.Sprintf("%v", task.ID)
	woID := s.woID
	s.timerPending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		_, err := deps.OMS.TimerWorkOrderTask(ctx, woID, taskID, action)
		return woTimerToggledMsg{action: action, label: label, err: err}
	}
}

// timerPastTense turns a wire action into what the status line says happened.
func timerPastTense(action string) string {
	if action == omsapi.TimerPause {
		return "paused"
	}
	return "started"
}

// anyTiming reports whether any clock on this work order is running, and so
// whether a local tick has anything to advance.
func (s *WorkOrderDetailScreen) anyTiming() bool {
	if s.wo == nil {
		return false
	}
	if s.wo.IsTiming {
		return true
	}
	for _, t := range s.wo.TaskCompletions {
		if t.IsTiming {
			return true
		}
	}
	return false
}

func (s *WorkOrderDetailScreen) tickCmd() tea.Cmd {
	gen := s.tickGen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return woTickMsg{gen: gen} })
}

// syncTicking re-anchors the display on a freshly fetched work order: the local
// offset restarts at zero (the server value already includes the running
// segment) and the 1s chain runs only while something is timing. An already-live
// chain is left alone rather than joined by a second one.
func (s *WorkOrderDetailScreen) syncTicking() tea.Cmd {
	s.tickOffset = 0
	if !s.anyTiming() {
		s.stopTicking()
		return nil
	}
	if s.ticking {
		return nil
	}
	s.ticking = true
	s.tickGen++
	return s.tickCmd()
}

// stopTicking abandons the current chain: bumping the generation means any tick
// already in flight is ignored when it lands instead of restarting the clock.
func (s *WorkOrderDetailScreen) stopTicking() {
	s.ticking = false
	s.tickGen++
}

// liveSeconds is what to display for one clock: the server's total, plus the
// seconds ticked locally since it was fetched if that clock is running. The
// server value is authoritative — this only fills the gap between refreshes,
// and every fetch resets it.
func (s *WorkOrderDetailScreen) liveSeconds(elapsed int, timing bool) int {
	if !timing {
		return elapsed
	}
	return elapsed + s.tickOffset
}

// formatElapsed renders a stopwatch total as MM:SS, or H:MM:SS once a job passes
// the hour — the same clock the web widget shows.
func formatElapsed(totalSeconds int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	h := totalSeconds / 3600
	m := (totalSeconds % 3600) / 60
	sec := totalSeconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

// elapsedSummary is the comparison the stopwatch exists to produce —
// "18m / est 30m" — falling back to "18m on job" when the source PM template
// carries no estimate (or there is no template at all).
func elapsedSummary(totalSeconds int, estimateMinutes *int) string {
	if totalSeconds < 0 {
		totalSeconds = 0
	}
	minutes := (totalSeconds + 30) / 60
	if estimateMinutes != nil && *estimateMinutes > 0 {
		return fmt.Sprintf("%dm / est %dm", minutes, *estimateMinutes)
	}
	return fmt.Sprintf("%dm on job", minutes)
}

// --- Material picker -------------------------------------------------------

func (s *WorkOrderDetailScreen) handleMaterialsKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := len(s.wo.MaterialUsage)
	switch m.String() {
	case "esc", "q":
		s.mode = woModeView
		return s, nil
	case "up", "k":
		if s.materialCursor > 0 {
			s.materialCursor--
		}
		return s, nil
	case "down", "j":
		if s.materialCursor < n-1 {
			s.materialCursor++
		}
		return s, nil
	case " ", "enter":
		if s.actionPending || n == 0 {
			return s, nil
		}
		return s, s.toggleMaterial()
	case "a":
		// The point of the whole feature: a corrective work order reaches this
		// list EMPTY, and this is where it gets its first line.
		s.openAddMaterial()
		return s, tea.Batch(textinput.Blink, s.loadPickItems())
	case "c":
		if n == 0 {
			return s, nil
		}
		return s.openMaterialCost()
	case "d":
		if s.actionPending || n == 0 {
			return s, nil
		}
		return s.removeMaterial()
	}
	return s, nil
}

func (s *WorkOrderDetailScreen) toggleMaterial() tea.Cmd {
	mat := s.wo.MaterialUsage[s.materialCursor]
	matID := fmt.Sprintf("%v", mat.ID)
	woID := s.woID
	next := !mat.WasUsed
	s.actionPending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		_, err := deps.OMS.ToggleWorkOrderMaterial(ctx, woID, matID, next, omsapi.WorkOrderMaterialEdit{})
		return woMaterialToggledMsg{err: err}
	}
}

// removeMaterial deletes the highlighted ad-hoc line. Both of the backend's 400
// guards are stated here rather than sent and bounced: the operator gets the
// reason — and, for the stock case, the fix — without a round trip.
func (s *WorkOrderDetailScreen) removeMaterial() (Screen, tea.Cmd) {
	mat, ok := s.currentMaterial()
	if !ok {
		return s, nil
	}
	if !mat.IsAdHoc {
		return s, Status("only added materials can be removed — this one is the PM template's", StatusWarn)
	}
	if mat.StockApplied {
		return s, Status("un-mark it as used first to restore the stock, then remove it", StatusWarn)
	}
	matID := fmt.Sprintf("%v", mat.ID)
	name := mat.MaterialName
	woID := s.woID
	s.actionPending = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		return woMaterialRemovedMsg{name: name, err: deps.OMS.RemoveWorkOrderMaterial(ctx, woID, matID)}
	}
}

// --- Re-price one line -----------------------------------------------------

// currentMaterial is the highlighted line, or false when there is none. A
// reload can empty the list under an open modal — somebody else removing the row
// this screen was pricing — so every path that reaches for it asks first.
func (s *WorkOrderDetailScreen) currentMaterial() (omsapi.WorkOrderMaterialUsage, bool) {
	if s.wo == nil || s.materialCursor < 0 || s.materialCursor >= len(s.wo.MaterialUsage) {
		return omsapi.WorkOrderMaterialUsage{}, false
	}
	return s.wo.MaterialUsage[s.materialCursor], true
}

// openMaterialCost opens the unit-cost editor for the highlighted line. The
// price freezes once stock has moved so the recorded spend can't drift from the
// decrement it backs, so a line in that state is turned away with the fix.
func (s *WorkOrderDetailScreen) openMaterialCost() (Screen, tea.Cmd) {
	mat, ok := s.currentMaterial()
	if !ok {
		return s, nil
	}
	if mat.StockApplied {
		return s, Status("price is frozen while stock is applied — un-mark it as used first", StatusWarn)
	}
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "12.50 (blank clears the price)"
	in.CharLimit = 16
	in.SetValue(string(mat.UnitCost))
	in.CursorEnd()
	in.Focus()
	s.costIn = in
	s.costErr = ""
	s.mode = woModeMaterialCost
	return s, textinput.Blink
}

func (s *WorkOrderDetailScreen) handleMaterialCostKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = woModeMaterials
		return s, nil
	case tea.KeyEnter:
		if s.costPending {
			return s, nil
		}
		return s.submitMaterialCost()
	}
	var cmd tea.Cmd
	s.costIn, cmd = s.costIn.Update(m)
	return s, cmd
}

// submitMaterialCost writes the price through the toggle — the only endpoint
// that carries it — sending the line's CURRENT was_used so an already-marked
// out-of-pocket buy can still be priced without flipping it.
func (s *WorkOrderDetailScreen) submitMaterialCost() (Screen, tea.Cmd) {
	mat, ok := s.currentMaterial()
	if !ok {
		s.mode = woModeMaterials
		return s, Status("that material is no longer on this work order", StatusWarn)
	}
	cost := strings.TrimSpace(s.costIn.Value())
	if cost != "" {
		if _, err := strconv.ParseFloat(cost, 64); err != nil {
			s.costErr = "unit cost must be a number (or blank to clear it)"
			return s, nil
		}
	}
	matID := fmt.Sprintf("%v", mat.ID)
	wasUsed := mat.WasUsed
	woID := s.woID
	s.costPending = true
	s.costErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		edit := omsapi.WorkOrderMaterialEdit{UnitCost: &cost}
		_, err := deps.OMS.ToggleWorkOrderMaterial(ctx, woID, matID, wasUsed, edit)
		return woMaterialCostSavedMsg{err: err}
	}
}

// --- Add-material form -----------------------------------------------------

func (s *WorkOrderDetailScreen) openAddMaterial() {
	s.amInputs = make([]textinput.Model, woMatFieldMax)
	for _, f := range []struct {
		id          int
		placeholder string
		limit       int
	}{
		{woMatName, "what you used or bought", 200},
		{woMatQuantity, "1", 16},
		{woMatUnit, "each, ft, L…", 50},
		{woMatUnitCost, "price actually paid per unit", 16},
		{woMatReceipt, "/path/to/receipt.jpg (~ expands to home)", 512},
	} {
		in := textinput.New()
		in.Prompt = ""
		in.Placeholder = f.placeholder
		in.CharLimit = f.limit
		s.amInputs[f.id] = in
	}
	s.amInputs[woMatQuantity].SetValue("1")
	s.amCursor = 0
	s.amItemID, s.amItemLabel = "", ""
	s.amPicking, s.amPickTyping, s.amPickCursor = false, false, 0
	search := textinput.New()
	search.Prompt = ""
	search.Placeholder = "filter by name or SKU"
	search.CharLimit = 64
	s.amPickSearch = search
	s.amErr = ""
	s.amPending = false
	s.mode = woModeAddMaterial
	s.syncAddMaterialFocus()
}

// loadPickItems fetches the stock list the picker offers, once per open form.
// Linking stock is optional, so a failure is recorded on the form rather than
// raised — an out-of-pocket line needs no item at all.
func (s *WorkOrderDetailScreen) loadPickItems() tea.Cmd {
	if len(s.amItems) > 0 {
		return nil
	}
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		items, err := deps.OMS.ListAllItems(ctx)
		return woItemsLoadedMsg{items: items, err: err}
	}
}

func (s *WorkOrderDetailScreen) syncAddMaterialFocus() {
	for i := range s.amInputs {
		if i == s.amCursor && woMaterialIsTextField(i) {
			s.amInputs[i].Focus()
		} else {
			s.amInputs[i].Blur()
		}
	}
}

// woMaterialIsTextField reports whether a field is typed into. Only the stock
// item is not — it is a picker, opened with space.
func woMaterialIsTextField(id int) bool { return id != woMatItem }

func (s *WorkOrderDetailScreen) handleAddMaterialKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.amPicking {
		return s.handleItemPickKey(m)
	}
	switch m.String() {
	case "esc":
		s.mode = woModeMaterials
		return s, nil
	case "tab", "down":
		s.moveAddMaterialCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveAddMaterialCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.amPending {
			return s, nil
		}
		return s.submitAddMaterial()
	}
	if s.amCursor == woMatItem {
		if m.String() == " " {
			s.openItemPicker()
			return s, nil
		}
		return s, nil
	}
	var cmd tea.Cmd
	s.amInputs[s.amCursor], cmd = s.amInputs[s.amCursor].Update(m)
	return s, cmd
}

func (s *WorkOrderDetailScreen) moveAddMaterialCursor(delta int) {
	s.amCursor = (s.amCursor + delta + woMatFieldMax) % woMatFieldMax
	s.syncAddMaterialFocus()
}

func (s *WorkOrderDetailScreen) openItemPicker() {
	s.amPicking = true
	s.amPickTyping = false
	s.amPickSearch.SetValue("")
	s.amPickSearch.Blur()
	s.applyItemFilter()
	s.amPickCursor = 0
	for i, row := range s.amPickRows {
		if row.id != "" && row.id == s.amItemID {
			s.amPickCursor = i
			break
		}
	}
}

// applyItemFilter rebuilds the picker rows against the search box. Row 0 always
// clears the link, which is how a line goes back to being an out-of-pocket buy.
func (s *WorkOrderDetailScreen) applyItemFilter() {
	q := strings.ToLower(strings.TrimSpace(s.amPickSearch.Value()))
	rows := []woItemPickRow{{label: "(none — out-of-pocket, moves no stock)"}}
	for _, it := range s.amItems {
		label := it.Name
		if it.SKU != "" {
			label += " (" + it.SKU + ")"
		}
		if cost := formatMoney(it.UnitCost); cost != "" {
			label += " · " + cost + "/unit"
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			rows = append(rows, woItemPickRow{id: it.ID, label: label})
		}
	}
	s.amPickRows = rows
	if s.amPickCursor >= len(s.amPickRows) {
		s.amPickCursor = 0
	}
}

func (s *WorkOrderDetailScreen) handleItemPickKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.amPickTyping {
		switch m.Type {
		case tea.KeyEsc, tea.KeyEnter:
			s.amPickTyping = false
			s.amPickSearch.Blur()
			s.amPickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.amPickSearch, cmd = s.amPickSearch.Update(m)
		s.applyItemFilter()
		return s, cmd
	}
	switch m.String() {
	case "esc":
		s.amPicking = false
		s.syncAddMaterialFocus()
	case "j", "down":
		if s.amPickCursor < len(s.amPickRows)-1 {
			s.amPickCursor++
		}
	case "k", "up":
		if s.amPickCursor > 0 {
			s.amPickCursor--
		}
	case "/":
		s.amPickTyping = true
		s.amPickSearch.Focus()
		return s, textinput.Blink
	case "enter":
		s.commitItemPick()
	}
	return s, nil
}

// commitItemPick links (or unlinks) the stock row. Picking an item seeds the
// cost field with what that item currently costs when no price has been typed —
// the same default the backend would apply, shown so it can be overridden when
// the real price differs. Seeding the NAME too saves retyping what the picker
// already named.
func (s *WorkOrderDetailScreen) commitItemPick() {
	if s.amPickCursor >= 0 && s.amPickCursor < len(s.amPickRows) {
		row := s.amPickRows[s.amPickCursor]
		s.amItemID, s.amItemLabel = row.id, row.label
		if row.id != "" {
			for _, it := range s.amItems {
				if it.ID != row.id {
					continue
				}
				if strings.TrimSpace(s.amInputs[woMatName].Value()) == "" {
					s.amInputs[woMatName].SetValue(it.Name)
				}
				if strings.TrimSpace(s.amInputs[woMatUnitCost].Value()) == "" && !it.UnitCost.Empty() {
					s.amInputs[woMatUnitCost].SetValue(string(it.UnitCost))
				}
				break
			}
		}
	}
	s.amPicking = false
	s.amPickTyping = false
	s.amPickSearch.SetValue("")
	s.amPickSearch.Blur()
	s.syncAddMaterialFocus()
}

func (s *WorkOrderDetailScreen) submitAddMaterial() (Screen, tea.Cmd) {
	name := strings.TrimSpace(s.amInputs[woMatName].Value())
	if name == "" {
		s.amCursor = woMatName
		s.syncAddMaterialFocus()
		s.amErr = "material name is required"
		return s, nil
	}
	qty := strings.TrimSpace(s.amInputs[woMatQuantity].Value())
	if qty != "" {
		if v, err := strconv.ParseFloat(qty, 64); err != nil || v < 0 {
			s.amCursor = woMatQuantity
			s.syncAddMaterialFocus()
			s.amErr = "quantity must be a number"
			return s, nil
		}
	}
	cost := strings.TrimSpace(s.amInputs[woMatUnitCost].Value())
	if cost != "" {
		if v, err := strconv.ParseFloat(cost, 64); err != nil || v < 0 {
			s.amCursor = woMatUnitCost
			s.syncAddMaterialFocus()
			s.amErr = "unit cost must be a number"
			return s, nil
		}
	}
	in := omsapi.WorkOrderAdHocMaterial{
		MaterialName:  name,
		QuantityUsed:  qty,
		Unit:          strings.TrimSpace(s.amInputs[woMatUnit].Value()),
		UnitCost:      cost,
		InventoryItem: s.amItemID,
	}
	receipt := expandUser(strings.TrimSpace(s.amInputs[woMatReceipt].Value()))
	woID := s.woID
	s.amPending = true
	s.amErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		// Read the receipt only when one was actually picked: the request then
		// goes out as multipart, and stays plain JSON otherwise.
		if receipt != "" {
			data, err := os.ReadFile(receipt)
			if err != nil {
				return woMaterialAddedMsg{name: name, err: fmt.Errorf("read %s: %w", receipt, err)}
			}
			in.Receipt = data
			in.ReceiptFilename = filepath.Base(receipt)
		}
		_, err := deps.OMS.AddWorkOrderMaterial(ctx, woID, in)
		return woMaterialAddedMsg{name: name, err: err}
	}
}

// --- Material cost totals --------------------------------------------------

// loadEstimates pulls the PM template's per-unit prices so the materials total
// can read actual AGAINST estimate. The estimate lives on MaintenanceMaterial
// and does not ride the work-order payload, so it costs one extra fetch — done
// once per template, and skipped entirely for corrective work, which has no
// template and therefore nothing to compare against (exactly as the stopwatch
// has no estimated time).
func (s *WorkOrderDetailScreen) loadEstimates() tea.Cmd {
	if s.wo == nil {
		return nil
	}
	id := anyID(s.wo.MaintenanceItem)
	if id == "" || s.estimatesFor == id {
		return nil
	}
	s.estimatesFor = id
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		item, err := deps.OMS.GetMaintenanceItem(ctx, id)
		if err != nil {
			return woEstimatesLoadedMsg{key: id, err: err}
		}
		costs := make(map[string]omsapi.DecimalString, len(item.Materials))
		for _, mat := range item.Materials {
			costs[mat.ID] = mat.EstimatedCostPerUnit
		}
		return woEstimatesLoadedMsg{key: id, costs: costs}
	}
}

// materialCostSummary is the actual-vs-estimated pair the materials view exists
// to produce.
//
// The actual is the server's total; it is only summed here when a backend older
// than op-768w omits the field. The estimate applies the TEMPLATE's per-unit
// price to the quantities THIS work order planned, so editing the template later
// does not rewrite what this job estimated — and an ad-hoc line, which has no
// template row, is in the actual but never in the estimate.
func (s *WorkOrderDetailScreen) materialCostSummary() (actual, estimated float64, hasEstimate bool) {
	if s.wo == nil {
		return 0, 0, false
	}
	if !s.wo.ActualMaterialCost.Empty() {
		actual = decimalFloat(s.wo.ActualMaterialCost)
	} else {
		for _, mu := range s.wo.MaterialUsage {
			if mu.WasUsed {
				actual += decimalFloat(mu.ActualCost)
			}
		}
	}
	for _, mu := range s.wo.MaterialUsage {
		perUnit, ok := s.estimates[anyID(mu.Material)]
		if !ok {
			continue
		}
		hasEstimate = true
		estimated += decimalFloat(mu.QuantityPlanned) * decimalFloat(perUnit)
	}
	return actual, estimated, hasEstimate
}

// decimalFloat reads a decimal-string field as a number, treating empty and
// unparseable alike as zero — an unpriced line contributes nothing to a total
// rather than voiding it.
func decimalFloat(d omsapi.DecimalString) float64 {
	if d.Empty() {
		return 0
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(string(d)), 64)
	if err != nil {
		return 0
	}
	return f
}

// anyID stringifies one of the API's `any`-typed ids (they are UUID strings on
// today's installs, but the field is whatever the serializer echoed back).
// Absent reads as "" rather than the "<nil>" fmt would otherwise produce.
func anyID(v any) string {
	if v == nil {
		return ""
	}
	id := strings.TrimSpace(fmt.Sprintf("%v", v))
	if id == "<nil>" {
		return ""
	}
	return id
}

// --- Add-photo form --------------------------------------------------------

// openPhotoForm opens the upload form. An empty taskCompletion files the photo
// at the work-order level (the classic behaviour); a step's completion id files
// it as evidence against that one step.
func (s *WorkOrderDetailScreen) openPhotoForm(taskCompletion, taskTitle string) {
	path := textinput.New()
	path.Prompt = ""
	path.Placeholder = "/path/to/photo.jpg (~ expands to home)"
	path.CharLimit = 512
	path.Focus()
	caption := textinput.New()
	caption.Prompt = ""
	caption.Placeholder = "optional caption"
	caption.CharLimit = 255
	s.photoPathIn = path
	s.photoCaptionIn = caption
	s.photoFocus = 0
	s.photoErr = ""
	s.photoTaskID = taskCompletion
	s.photoTaskTitle = taskTitle
	s.photoReturn = s.mode
	s.mode = woModePhoto
}

func (s *WorkOrderDetailScreen) handlePhotoKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = s.photoReturn
		return s, nil
	case tea.KeyTab, tea.KeyShiftTab:
		s.photoFocus ^= 1
		if s.photoFocus == 0 {
			s.photoCaptionIn.Blur()
			s.photoPathIn.Focus()
		} else {
			s.photoPathIn.Blur()
			s.photoCaptionIn.Focus()
		}
		return s, nil
	case tea.KeyEnter:
		if s.photoPending {
			return s, nil
		}
		return s.submitPhoto()
	}
	var cmd tea.Cmd
	if s.photoFocus == 0 {
		s.photoPathIn, cmd = s.photoPathIn.Update(m)
	} else {
		s.photoCaptionIn, cmd = s.photoCaptionIn.Update(m)
	}
	return s, cmd
}

func (s *WorkOrderDetailScreen) submitPhoto() (Screen, tea.Cmd) {
	path := expandUser(strings.TrimSpace(s.photoPathIn.Value()))
	if path == "" {
		s.photoErr = "file path is required"
		return s, nil
	}
	caption := strings.TrimSpace(s.photoCaptionIn.Value())
	woID := s.woID
	taskID := s.photoTaskID
	s.photoPending = true
	s.photoErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return woPhotoAddedMsg{err: fmt.Errorf("read %s: %w", path, err)}
		}
		_, err = deps.OMS.AddWorkOrderPhoto(ctx, woID, filepath.Base(path), data, caption, taskID)
		return woPhotoAddedMsg{err: err}
	}
}

// --- Upload-PDF form -------------------------------------------------------

func (s *WorkOrderDetailScreen) openPdfForm() {
	path := textinput.New()
	path.Prompt = ""
	path.Placeholder = "/path/to/scanned-work-order.pdf (~ expands to home)"
	path.CharLimit = 512
	path.Focus()
	s.pdfPathIn = path
	s.pdfErr = ""
	s.mode = woModePdf
}

func (s *WorkOrderDetailScreen) handlePdfKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = woModeView
		return s, nil
	case tea.KeyEnter:
		if s.pdfPending {
			return s, nil
		}
		return s.submitPdf()
	}
	var cmd tea.Cmd
	s.pdfPathIn, cmd = s.pdfPathIn.Update(m)
	return s, cmd
}

func (s *WorkOrderDetailScreen) submitPdf() (Screen, tea.Cmd) {
	path := expandUser(strings.TrimSpace(s.pdfPathIn.Value()))
	if path == "" {
		s.pdfErr = "file path is required"
		return s, nil
	}
	s.pdfPending = true
	s.pdfErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		data, err := os.ReadFile(path)
		if err != nil {
			return woPdfUploadedMsg{err: fmt.Errorf("read %s: %w", path, err)}
		}
		res, err := deps.OMS.UploadWorkOrderPdf(ctx, filepath.Base(path), data)
		return woPdfUploadedMsg{result: res, err: err}
	}
}

// --- Validation checklist --------------------------------------------------

func (s *WorkOrderDetailScreen) openChecklist(finalizeAfter bool) {
	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "optional notes"
	notes.CharLimit = 500
	// Seed from any prior acknowledgement so a re-open reflects current state.
	s.ackElectrical = false
	s.ackLoto = false
	s.ackRequired = false
	if v := s.woValidation(); v != nil {
		s.ackElectrical = v.ElectricalAcknowledged
		s.ackLoto = v.LotoAcknowledged
		s.ackRequired = v.RequiredFieldsAcknowledged
		notes.SetValue(v.Notes)
	}
	s.checklistNotesIn = notes
	s.checklistFocus = 0
	s.checklistErr = ""
	s.finalizeAfterValidate = finalizeAfter
	s.mode = woModeChecklist
}

func (s *WorkOrderDetailScreen) handleChecklistKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = woModeView
		s.finalizeAfterValidate = false
		return s, nil
	case tea.KeyTab, tea.KeyDown:
		s.setChecklistFocus((s.checklistFocus + 1) % 4)
		return s, nil
	case tea.KeyShiftTab, tea.KeyUp:
		s.setChecklistFocus((s.checklistFocus + 3) % 4)
		return s, nil
	case tea.KeyEnter:
		if s.checklistPending {
			return s, nil
		}
		return s.submitChecklist()
	}
	// Space toggles the acknowledgement on rows 0-2. On the notes row it is a
	// literal character, so it falls through to the input below.
	if m.String() == " " && s.checklistFocus < 3 {
		switch s.checklistFocus {
		case 0:
			s.ackElectrical = !s.ackElectrical
		case 1:
			s.ackLoto = !s.ackLoto
		case 2:
			s.ackRequired = !s.ackRequired
		}
		return s, nil
	}
	if s.checklistFocus == 3 {
		var cmd tea.Cmd
		s.checklistNotesIn, cmd = s.checklistNotesIn.Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *WorkOrderDetailScreen) setChecklistFocus(f int) {
	s.checklistFocus = f
	if f == 3 {
		s.checklistNotesIn.Focus()
	} else {
		s.checklistNotesIn.Blur()
	}
}

func (s *WorkOrderDetailScreen) submitChecklist() (Screen, tea.Cmd) {
	if !(s.ackElectrical && s.ackLoto && s.ackRequired) {
		s.checklistErr = "all three acknowledgements are required"
		return s, nil
	}
	notes := strings.TrimSpace(s.checklistNotesIn.Value())
	id := s.woID
	s.checklistPending = true
	s.checklistErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	elec, loto, req := s.ackElectrical, s.ackLoto, s.ackRequired
	return s, func() tea.Msg {
		_, err := deps.OMS.ValidateWorkOrderChecklist(ctx, id, elec, loto, req, notes)
		return woValidatedMsg{err: err}
	}
}

// --- Confirm overlay -------------------------------------------------------

func (s *WorkOrderDetailScreen) openConfirm(action, prompt string) {
	s.confirmAction = action
	s.confirmPrompt = prompt
	s.mode = woModeConfirm
}

func (s *WorkOrderDetailScreen) handleConfirmKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "y", "Y":
		s.mode = woModeView
		if s.transitioning {
			return s, nil
		}
		s.transitioning = true
		return s, s.transition(s.confirmAction)
	case "n", "N", "esc":
		s.mode = woModeView
		return s, nil
	}
	return s, nil
}

// --- Edit-notes form -------------------------------------------------------

func (s *WorkOrderDetailScreen) openNotesForm() {
	notes := textinput.New()
	notes.Prompt = ""
	notes.Placeholder = "work order notes"
	notes.CharLimit = 2000
	if s.wo != nil {
		notes.SetValue(s.wo.Notes)
	}
	notes.CursorEnd()
	notes.Focus()
	s.notesIn = notes
	s.notesErr = ""
	s.mode = woModeNotes
}

func (s *WorkOrderDetailScreen) handleNotesKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.Type {
	case tea.KeyEsc:
		s.mode = woModeView
		return s, nil
	case tea.KeyEnter:
		if s.notesPending {
			return s, nil
		}
		return s.submitNotes()
	}
	var cmd tea.Cmd
	s.notesIn, cmd = s.notesIn.Update(m)
	return s, cmd
}

func (s *WorkOrderDetailScreen) submitNotes() (Screen, tea.Cmd) {
	notes := s.notesIn.Value()
	woID := s.woID
	s.notesPending = true
	s.notesErr = ""
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return s, func() tea.Msg {
		wo, err := deps.OMS.UpdateWorkOrder(ctx, woID, map[string]any{"notes": notes})
		return woNotesSavedMsg{wo: wo, err: err}
	}
}

// --- helpers ---------------------------------------------------------------

func (s *WorkOrderDetailScreen) setAction(msg string, lvl StatusLevel) {
	s.actionMsg = msg
	s.actionLvl = lvl
}

// refreshBody re-renders the read-only body into the scroller, but only when a
// work order is actually loaded — a failed (re)load leaves wo nil, and
// renderBody dereferences it.
func (s *WorkOrderDetailScreen) refreshBody() {
	if s.wo != nil {
		s.scroller.Set(s.renderBody())
	}
}

func (s *WorkOrderDetailScreen) woValidation() *omsapi.WorkOrderValidation {
	if s.wo == nil {
		return nil
	}
	return s.wo.Validation
}

// clampCursors keeps the picker cursors in range after a reload changes the
// task / material counts.
func (s *WorkOrderDetailScreen) clampCursors() {
	if s.wo == nil {
		s.taskCursor, s.materialCursor = 0, 0
		return
	}
	if s.taskCursor >= len(s.wo.TaskCompletions) {
		s.taskCursor = maxInt(0, len(s.wo.TaskCompletions)-1)
	}
	if s.materialCursor >= len(s.wo.MaterialUsage) {
		s.materialCursor = maxInt(0, len(s.wo.MaterialUsage)-1)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func expandUser(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func uploadResultSummary(r *omsapi.WorkOrderUploadResult) string {
	if r == nil {
		return "PDF uploaded"
	}
	parts := []string{"PDF " + r.Status}
	if r.WorkOrderID != nil && *r.WorkOrderID != "" {
		parts = append(parts, "WO "+*r.WorkOrderID)
	}
	if len(r.CompletedItems) > 0 {
		parts = append(parts, fmt.Sprintf("%d task(s) completed", len(r.CompletedItems)))
	}
	if len(r.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("%d error(s)", len(r.Errors)))
	}
	return strings.Join(parts, " · ")
}

func (s *WorkOrderDetailScreen) View() string {
	if s.loading && s.wo == nil {
		return StyleMuted.Render("Loading work order…")
	}
	if s.loadErr != "" && s.wo == nil {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("press r to retry · esc back")
	}
	if s.wo == nil {
		return StyleMuted.Render("Work order not found.")
	}

	switch s.mode {
	case woModeTasks:
		return s.renderTaskPicker()
	case woModeMaterials:
		return s.renderMaterialPicker()
	case woModeAddMaterial:
		return s.renderAddMaterialForm()
	case woModeMaterialCost:
		return s.renderMaterialCostForm()
	case woModePhoto:
		return s.renderPhotoForm()
	case woModePdf:
		return s.renderPdfForm()
	case woModeChecklist:
		return s.renderChecklistForm()
	case woModeConfirm:
		return s.renderConfirm()
	case woModeNotes:
		return s.renderNotesForm()
	}

	footerRows := detailFooterRows
	if s.actionMsg != "" {
		footerRows = detailFooterRowsWithAction
	}
	s.scroller.SetViewHeight(scrollerViewHeight(s.terminalHeight, footerRows))

	body := s.scroller.View()
	footer := ""
	if s.actionMsg != "" {
		footer += RenderStatus(s.actionMsg, s.actionLvl) + "\n\n"
	}
	footer += StyleMuted.Render(s.footerHint())
	return body + "\n\n" + footer
}

func (s *WorkOrderDetailScreen) footerHint() string {
	parts := []string{"j/k scroll"}
	// Status transitions, gated to the states in which they make sense.
	switch s.wo.Status {
	case "open", "in_progress", "blocked":
		parts = append(parts, "i in-progress", "b block", "c complete", "x cancel")
	}
	// Only offered while the clock can still move: completing a WO finalizes it
	// server-side. Gating it also keeps two words off an already-long footer on
	// the screens that don't need them — it names the action the key performs
	// rather than adding a second entry for pause.
	if woTimerAllowed(s.wo.Status) {
		if s.wo.IsTiming {
			parts = append(parts, "s pause")
		} else {
			parts = append(parts, "s start")
		}
	}
	if len(s.wo.TaskCompletions) > 0 {
		parts = append(parts, "t tasks")
	}
	// Always offered: an empty list is the corrective case, where adding the
	// first line is exactly what the operator came here to do.
	parts = append(parts, "M materials")
	parts = append(parts, "p photo", "U upload-pdf", "v validate", "E notes", "r refresh", "esc back")
	return strings.Join(parts, " · ")
}

func (s *WorkOrderDetailScreen) renderTaskPicker() string {
	var b strings.Builder
	done := 0
	for _, t := range s.wo.TaskCompletions {
		if t.IsCompleted {
			done++
		}
	}
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Toggle tasks (%d/%d complete)", done, len(s.wo.TaskCompletions))) + "\n\n")
	for i, t := range s.wo.TaskCompletions {
		cursor := "  "
		if i == s.taskCursor {
			cursor = "> "
		}
		box := "[ ]"
		if t.IsCompleted {
			box = StyleStatusOK.Render("[x]")
		}
		title := t.TaskTitle
		if !t.IsRequired {
			title += " " + StyleMuted.Render("(optional)")
		}
		line := cursor + box + " " + title
		if i == s.taskCursor {
			line = StyleTitle.Render(line)
		}
		b.WriteString(line + "\n")
		// The step's clock, so 's' has something to aim at. Shown only once the
		// step has been timed — the running one is the row worth spotting.
		if secs := s.liveSeconds(t.ElapsedSeconds, t.IsTiming); secs > 0 || t.IsTiming {
			clock := "      " + StyleMuted.Render("time: "+formatElapsed(secs))
			if t.IsTiming {
				clock += " " + StyleStatusOK.Render("● running")
			}
			b.WriteString(clock + "\n")
		}
		// Both photo halves, indented under their step: the template's
		// reference shot and whatever evidence has been filed against it.
		if t.TaskReferenceImageURL != "" {
			b.WriteString("      " + StyleMuted.Render("ref: ") + t.TaskReferenceImageURL + "\n")
		}
		for _, p := range t.EvidencePhotos {
			b.WriteString("      " + StyleMuted.Render("evidence: "+evidencePhotoLabel(p)) + "\n")
		}
	}
	b.WriteString("\n")
	switch {
	case s.actionPending:
		b.WriteString(StyleMuted.Render("Updating…"))
	case s.timerPending:
		b.WriteString(StyleMuted.Render("Timer…"))
	default:
		b.WriteString(StyleMuted.Render("j/k move · space/enter toggle · s timer · p evidence photo · esc back"))
	}
	return b.String()
}

// isPinnedToStep reports whether a work-order photo is a step's evidence.
// task_completion is null on every work-order-level photo (and on the trimmed
// projection nested under a step, where the parent already says which step it
// is), so a non-empty value is the only signal.
func isPinnedToStep(p omsapi.WorkOrderPhoto) bool {
	if p.TaskCompletion == nil {
		return false
	}
	s, ok := p.TaskCompletion.(string)
	return !ok || s != ""
}

// evidencePhotoLabel names one evidence photo in a single line: its caption if
// it has one, else the image URL, plus who/when when the server sent it.
func evidencePhotoLabel(p omsapi.WorkOrderPhoto) string {
	label := p.Caption
	if label == "" {
		label = p.ImageURL
	}
	if label == "" {
		label = "(photo)"
	}
	meta := []string{}
	if !p.UploadedAt.IsZero() {
		meta = append(meta, p.UploadedAt.Format("2006-01-02"))
	}
	if p.UploadedBy != "" {
		meta = append(meta, "by "+p.UploadedBy)
	}
	if len(meta) > 0 {
		label += " (" + strings.Join(meta, " · ") + ")"
	}
	return label
}

// maxShownRevisions caps the revision chain rendered inline. The backend walks
// at most 20 supersedes links; showing all of them would bury the current
// document under its own history, so the tail is summarised as a count.
const maxShownRevisions = 4

// woReferenceParts splits the reference bundle into its two lists. The whole
// object is absent on a work order fetched from a backend older than op-pzae
// (and from any list payload), which is an empty section rather than an error.
func woReferenceParts(wo *omsapi.WorkOrder) ([]omsapi.WorkOrderRefDoc, []omsapi.WorkOrderRefLink) {
	if wo == nil || wo.ReferenceDocuments == nil {
		return nil, nil
	}
	return wo.ReferenceDocuments.Documents, wo.ReferenceDocuments.Links
}

// refRevisionSummary compacts a document's supersedes chain (newest-first, as
// received) into one line: "rev 2 (2026-05-04), rev 1, +3 more". Returns "" for
// a document nobody has replaced, so the caller can skip the sub-line entirely.
func refRevisionSummary(revs []omsapi.WorkOrderRefDocRevision) string {
	if len(revs) == 0 {
		return ""
	}
	shown := revs
	if len(shown) > maxShownRevisions {
		shown = shown[:maxShownRevisions]
	}
	parts := make([]string, 0, len(shown)+1)
	for _, r := range shown {
		part := fmt.Sprintf("rev %d", r.Version)
		if d := refDocDate(r.UploadedAt); d != "" {
			part += " (" + d + ")"
		}
		parts = append(parts, part)
	}
	if rest := len(revs) - len(shown); rest > 0 {
		parts = append(parts, fmt.Sprintf("+%d more", rest))
	}
	return strings.Join(parts, ", ")
}

// refDocDate reduces an upload timestamp to its calendar date. These arrive as
// hand-built isoformat() text rather than a DRF datetime, so they are trimmed
// rather than parsed: anything unexpected renders as-is instead of vanishing.
func refDocDate(iso string) string {
	if i := strings.IndexByte(iso, 'T'); i > 0 {
		return iso[:i]
	}
	return iso
}

func (s *WorkOrderDetailScreen) renderMaterialPicker() string {
	var b strings.Builder
	used := 0
	for _, mu := range s.wo.MaterialUsage {
		if mu.WasUsed {
			used++
		}
	}
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Materials used (%d/%d used)", used, len(s.wo.MaterialUsage))) + "\n\n")
	if len(s.wo.MaterialUsage) == 0 {
		// The corrective case: no PM template, so nothing was copied in. Say
		// what to do about it rather than just reporting the hole.
		b.WriteString(StyleMuted.Render("Nothing recorded yet. Press a to add what you used or bought on this job.") + "\n")
	}
	for i, mu := range s.wo.MaterialUsage {
		cursor := "  "
		if i == s.materialCursor {
			cursor = "> "
		}
		box := "[ ]"
		if mu.WasUsed {
			box = StyleStatusOK.Render("[x]")
		}
		label := mu.MaterialName
		if mu.IsAdHoc {
			label += " " + StyleMuted.Render("(added)")
		}
		line := cursor + box + " " + label
		if i == s.materialCursor {
			line = StyleTitle.Render(line)
		}
		b.WriteString(line + "\n")
		if meta := materialMeta(mu); meta != "" {
			b.WriteString("      " + StyleMuted.Render(meta) + "\n")
		}
		b.WriteString("      " + StyleMuted.Render(materialCostLine(mu)) + "\n")
	}
	// Nothing recorded is not "$0.00 spent" — with no lines there is no total to
	// report, so the empty state stands alone.
	if len(s.wo.MaterialUsage) > 0 {
		b.WriteString("\n" + s.renderMaterialTotals() + "\n")
	}
	b.WriteString("\n")
	switch {
	case s.actionPending:
		b.WriteString(StyleMuted.Render("Updating…"))
	case len(s.wo.MaterialUsage) == 0:
		// Don't advertise keys that act on a highlighted row when there is none.
		b.WriteString(StyleMuted.Render("a add · esc back"))
	default:
		b.WriteString(StyleMuted.Render("j/k move · space/enter toggle · a add · c cost · d remove · esc back"))
	}
	return b.String()
}

// materialMeta is the quantities-and-stock line under a material row: what was
// planned, what was used, which stock row it draws from, and whether a decrement
// is currently applied (which freezes the line until it is un-marked).
func materialMeta(mu omsapi.WorkOrderMaterialUsage) string {
	parts := []string{}
	if !mu.QuantityPlanned.Empty() {
		parts = append(parts, "planned "+quantityWithUnit(mu.QuantityPlanned, mu.Unit))
	}
	if !mu.QuantityUsed.Empty() {
		parts = append(parts, "used "+quantityWithUnit(mu.QuantityUsed, mu.Unit))
	}
	if mu.InventoryItemName != "" {
		parts = append(parts, "stock: "+mu.InventoryItemName)
	}
	if mu.StockApplied && mu.AppliedQuantity != nil {
		parts = append(parts, fmt.Sprintf("−%d from stock", *mu.AppliedQuantity))
	}
	return strings.Join(parts, " · ")
}

// materialCostLine is the money line under a material row. An unpriced line says
// so outright: cost is optional — plenty of lines are shop stock nobody prices
// at the point of use — and a blank there must not read as free.
func materialCostLine(mu omsapi.WorkOrderMaterialUsage) string {
	parts := []string{}
	if amt := formatMoney(mu.ActualCost); amt != "" {
		if per := formatMoney(mu.UnitCost); per != "" {
			parts = append(parts, fmt.Sprintf("cost %s (%s/unit)", amt, per))
		} else {
			parts = append(parts, "cost "+amt)
		}
	} else {
		parts = append(parts, "no cost recorded")
	}
	if mu.ReceiptURL != "" {
		parts = append(parts, "receipt "+mu.ReceiptURL)
	}
	return strings.Join(parts, " · ")
}

func quantityWithUnit(q omsapi.DecimalString, unit string) string {
	if unit == "" {
		return string(q)
	}
	return string(q) + " " + unit
}

// renderMaterialTotals is the actual-vs-estimated line. A corrective work order
// has an actual and nothing to measure it against, and says so — the same shape
// the stopwatch takes when its template carries no estimated time.
func (s *WorkOrderDetailScreen) renderMaterialTotals() string {
	actual, estimated, hasEstimate := s.materialCostSummary()
	line := StyleMuted.Render("Actual material cost: ") + fmt.Sprintf("$%.2f", actual)
	if !hasEstimate {
		return line + StyleMuted.Render(" · no estimate for this job")
	}
	line += StyleMuted.Render(fmt.Sprintf(" · estimated $%.2f · ", estimated))
	if actual > estimated {
		return line + StyleStatusError.Render(fmt.Sprintf("$%.2f over", actual-estimated))
	}
	return line + StyleStatusOK.Render(fmt.Sprintf("$%.2f under", estimated-actual))
}

func (s *WorkOrderDetailScreen) renderMaterialCostForm() string {
	var b strings.Builder
	mat, ok := s.currentMaterial()
	if !ok {
		return StyleMuted.Render("That material is no longer on this work order. esc back")
	}
	b.WriteString(StyleTitle.Render("Unit cost") + "  " + StyleMuted.Render(mat.MaterialName) + "\n")
	b.WriteString(StyleMuted.Render("The real price paid per unit. "+materialCostLine(mat)) + "\n\n")
	if s.costPending {
		b.WriteString(StyleMuted.Render("Saving…"))
		return b.String()
	}
	b.WriteString("Unit cost ($):\n  " + s.costIn.View() + "\n")
	if s.costErr != "" {
		b.WriteString("\n" + StyleStatusError.Render(s.costErr) + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("enter save · esc cancel"))
	return b.String()
}

func (s *WorkOrderDetailScreen) renderAddMaterialForm() string {
	if s.amPicking {
		return s.renderItemPicker()
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Add material") + "\n")
	b.WriteString(StyleMuted.Render("Link a stock item to draw it from inventory; leave it unset for an out-of-pocket buy.") + "\n\n")
	if s.amPending {
		b.WriteString(StyleMuted.Render("Adding…"))
		return b.String()
	}
	for i := 0; i < woMatFieldMax; i++ {
		cursor := "  "
		label := woMaterialFieldLabel[i]
		if i == s.amCursor {
			cursor = "> "
			label = StyleTitle.Render(label)
		}
		b.WriteString(cursor + label + "\n")
		if i == woMatItem {
			b.WriteString("    " + s.itemFieldValue() + "\n")
			continue
		}
		b.WriteString("    " + s.amInputs[i].View() + "\n")
	}
	b.WriteString("\n")
	if s.amErr != "" {
		b.WriteString(StyleStatusError.Render(s.amErr) + "\n\n")
	}
	hint := "tab/arrows move · enter add · esc back"
	if s.amCursor == woMatItem {
		hint = "space pick stock item · " + hint
	}
	b.WriteString(StyleMuted.Render(hint))
	return b.String()
}

// itemFieldValue renders the stock-item slot: the picked row, or what the line
// means without one. A picker that never loaded says so here rather than
// pretending the shop has no stock.
func (s *WorkOrderDetailScreen) itemFieldValue() string {
	if s.amItemID != "" {
		return s.amItemLabel
	}
	if s.amItemsErr != "" {
		return StyleMuted.Render("(none — stock list unavailable: " + s.amItemsErr + ")")
	}
	return StyleMuted.Render("(none — out-of-pocket, moves no stock)")
}

func (s *WorkOrderDetailScreen) renderItemPicker() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Link a stock item") + "\n")
	b.WriteString(StyleMuted.Render("Marking the line used will decrement whichever item is linked.") + "\n\n")
	if s.amPickTyping {
		b.WriteString("Filter: " + s.amPickSearch.View() + "\n\n")
	} else if q := strings.TrimSpace(s.amPickSearch.Value()); q != "" {
		b.WriteString(StyleMuted.Render("Filter: "+q) + "\n\n")
	}
	if len(s.amItems) == 0 && s.amItemsErr == "" {
		b.WriteString(StyleMuted.Render("Loading items…") + "\n")
	}
	if s.amItemsErr != "" {
		b.WriteString(StyleStatusWarn.Render("Stock list unavailable: "+s.amItemsErr) + "\n")
	}
	for i, row := range s.amPickRows {
		line := "  " + row.label
		if i == s.amPickCursor {
			line = StyleTitle.Render("> " + row.label)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	if s.amPickTyping {
		b.WriteString(StyleMuted.Render("enter/esc stop filtering"))
	} else {
		b.WriteString(StyleMuted.Render("j/k move · / filter · enter select · esc back"))
	}
	return b.String()
}

func (s *WorkOrderDetailScreen) renderPhotoForm() string {
	var b strings.Builder
	if s.photoTaskID != "" {
		b.WriteString(StyleTitle.Render("Add evidence photo") + "\n")
		label := s.photoTaskTitle
		if label == "" {
			label = fmt.Sprintf("step %v", s.photoTaskID)
		}
		b.WriteString(StyleMuted.Render("Filed under: ") + label + "\n\n")
	} else {
		b.WriteString(StyleTitle.Render("Add photo") + "\n\n")
	}
	b.WriteString(StyleMuted.Render("File path: ") + s.photoPathIn.View() + "\n")
	b.WriteString(StyleMuted.Render("Caption:   ") + s.photoCaptionIn.View() + "\n")
	if s.photoErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.photoErr) + "\n")
	}
	if s.photoPending {
		b.WriteString("\n" + StyleMuted.Render("Uploading…"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("tab next field · enter submit · esc cancel"))
	}
	return b.String()
}

func (s *WorkOrderDetailScreen) renderPdfForm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Upload completed work-order PDF") + "\n\n")
	b.WriteString(StyleMuted.Render("The scan is parsed and matched to its work order (staff only).") + "\n\n")
	b.WriteString(StyleMuted.Render("File path: ") + s.pdfPathIn.View() + "\n")
	if s.pdfErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.pdfErr) + "\n")
	}
	if s.pdfPending {
		b.WriteString("\n" + StyleMuted.Render("Uploading…"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("enter submit · esc cancel"))
	}
	return b.String()
}

func (s *WorkOrderDetailScreen) renderChecklistForm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Validation checklist") + "\n")
	b.WriteString(StyleMuted.Render("All three must be acknowledged before the work order can be completed.") + "\n\n")

	rows := []struct {
		label   string
		checked bool
	}{
		{"Electrical safety reviewed", s.ackElectrical},
		{"LOTO (lock-out/tag-out) reviewed", s.ackLoto},
		{"Required fields complete", s.ackRequired},
	}
	for i, row := range rows {
		cursor := "  "
		if s.checklistFocus == i {
			cursor = "> "
		}
		box := "[ ]"
		if row.checked {
			box = StyleStatusOK.Render("[x]")
		}
		line := cursor + box + " " + row.label
		if s.checklistFocus == i {
			line = StyleTitle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	notesCursor := "  "
	if s.checklistFocus == 3 {
		notesCursor = "> "
	}
	b.WriteString(notesCursor + StyleMuted.Render("Notes: ") + s.checklistNotesIn.View() + "\n")

	if s.checklistErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.checklistErr) + "\n")
	}
	if s.checklistPending {
		b.WriteString("\n" + StyleMuted.Render("Submitting…"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("tab/↑↓ move · space toggle · enter submit · esc cancel"))
	}
	return b.String()
}

func (s *WorkOrderDetailScreen) renderNotesForm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Edit notes") + "\n\n")
	b.WriteString(StyleMuted.Render("Notes: ") + s.notesIn.View() + "\n")
	if s.notesErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.notesErr) + "\n")
	}
	if s.notesPending {
		b.WriteString("\n" + StyleMuted.Render("Saving…"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("enter save · esc cancel"))
	}
	return b.String()
}

func (s *WorkOrderDetailScreen) renderConfirm() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render(s.confirmPrompt) + "\n\n")
	if s.wo != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("%s · current status: %s", s.wo.Title, s.wo.Status)) + "\n\n")
	}
	b.WriteString(StyleMuted.Render("y confirm · n/esc cancel"))
	return b.String()
}

// renderTimerLine is the whole-job stopwatch: clock, a marker while it runs, and
// the actual-vs-estimate summary. Returns a whole line, newline included.
//
// It renders unconditionally, like the Tools and Documentation sections: a
// never-started clock reading 00:00 is what tells an operator the key exists,
// and an absent elapsed_seconds (a backend older than op-m3so) decodes to the
// same zero as a clock nobody started, so there is nothing to distinguish them
// by. Against such a backend the key 404s, which is honest.
func (s *WorkOrderDetailScreen) renderTimerLine() string {
	wo := s.wo
	if wo == nil {
		return ""
	}
	secs := s.liveSeconds(wo.ElapsedSeconds, wo.IsTiming)
	line := StyleMuted.Render("Elapsed: ") + formatElapsed(secs)
	if wo.IsTiming {
		line += " " + StyleStatusOK.Render("● running")
	}
	line += StyleMuted.Render(" · " + elapsedSummary(secs, wo.EstimatedTimeMin))
	return line + "\n"
}

func (s *WorkOrderDetailScreen) renderBody() string {
	wo := s.wo
	var b strings.Builder

	b.WriteString(StyleTitle.Render(wo.Title))
	if wo.IsOverdue {
		b.WriteString("  " + StyleStatusError.Render("OVERDUE"))
	}
	b.WriteString("\n")

	headerParts := []string{}
	if wo.ShortID != "" {
		headerParts = append(headerParts, "#"+wo.ShortID)
	}
	headerParts = append(headerParts, fmt.Sprintf("ID %v", wo.ID))
	if wo.Status != "" {
		headerParts = append(headerParts, "status "+wo.Status)
	}
	if wo.Priority != "" {
		headerParts = append(headerParts, "priority "+wo.Priority)
	}
	b.WriteString(StyleMuted.Render(strings.Join(headerParts, " · ")) + "\n")

	if wo.AssetName != "" {
		assetLine := "Asset: " + wo.AssetName
		if wo.AssetTag != "" {
			assetLine += " (" + wo.AssetTag + ")"
		}
		b.WriteString(StyleMuted.Render(assetLine) + "\n")
	}
	if wo.MaintenanceItemTitle != "" {
		b.WriteString(StyleMuted.Render("Maintenance item: ") + wo.MaintenanceItemTitle + "\n")
	}
	if wo.AssignedToName != "" {
		b.WriteString(StyleMuted.Render("Assigned to: ") + wo.AssignedToName + "\n")
	}
	if wo.CompletedByName != "" {
		b.WriteString(StyleMuted.Render("Completed by: ") + wo.CompletedByName + "\n")
	}

	// The stopwatch rides in the header block rather than down in Dates: a
	// running clock is the one number on this screen that changes while you look
	// at it, so it must be readable without scrolling. Actual-vs-estimate is the
	// whole point of recording it, so the estimate travels on the same line.
	b.WriteString(s.renderTimerLine())

	if wo.Description != "" {
		b.WriteString("\n" + wo.Description + "\n")
	}
	b.WriteString("\n")

	// Tools Required sits at the top of the body — this is gear to gather
	// BEFORE starting, so it has to be read before the task list, not after it.
	// Display-only: the backend builds the list from the source PM template
	// (required first, then by name) and there is nothing to check off.
	b.WriteString(StyleTitle.Render("Tools Required") + "\n")
	if len(wo.Tools) == 0 {
		b.WriteString(StyleMuted.Render("  No tools specified.") + "\n")
	} else {
		for _, t := range wo.Tools {
			line := "  · " + t.Name
			if t.Quantity > 0 {
				line += fmt.Sprintf(" ×%d", t.Quantity)
			}
			if t.LocationHint != "" {
				line += StyleMuted.Render(" · " + t.LocationHint)
			}
			if t.IsRequired {
				line += " " + StyleStatusWarn.Render("[REQ]")
			}
			b.WriteString(line + "\n")
			if t.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(t.Notes) + "\n")
			}
		}
	}
	b.WriteString("\n")

	b.WriteString(StyleTitle.Render("Dates") + "\n")
	if wo.DueDate != "" {
		b.WriteString(StyleMuted.Render("Due: ") + wo.DueDate + "\n")
	}
	if !wo.CreatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Created: %s", wo.CreatedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if !wo.UpdatedAt.IsZero() {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Updated: %s", wo.UpdatedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	// When work FIRST started (first timer start). A later resume never moves it,
	// so it is a date, not part of the running clock above.
	if wo.StartedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Started: %s", wo.StartedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if wo.CompletedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Completed: %s", wo.CompletedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	if wo.ClosedAt != nil {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Closed: %s", wo.ClosedAt.Format("2006-01-02 15:04"))) + "\n")
	}
	b.WriteString("\n")

	if wo.Notes != "" {
		b.WriteString(StyleTitle.Render("Notes") + "\n")
		b.WriteString(wo.Notes + "\n\n")
	}

	// Validation status — mirrors the web finalize gate so the operator knows
	// whether a complete-transition (or PDF) will be blocked.
	if v := wo.Validation; v != nil {
		b.WriteString(StyleTitle.Render("Validation") + "\n")
		if v.IsComplete {
			b.WriteString(StyleStatusOK.Render("  ✓ acknowledged"))
		} else {
			b.WriteString(StyleStatusWarn.Render("  ! incomplete"))
		}
		if v.ValidatedByName != "" {
			b.WriteString(StyleMuted.Render(" · by " + v.ValidatedByName))
		}
		b.WriteString("\n\n")
	}

	// Documentation & References sits beside the validation/sign-off gate for
	// the reason the web page and the printed form both put it there: whoever
	// performs and signs the job should be able to reach the manual — and see
	// which revision is current — without hunting for it.
	//
	// Display-only. It is a projection of the ASSET's document library (the work
	// order stores no links of its own), so documents are managed on the asset
	// and there is no hotkey here. ScanTTY renders no files: the URL is the
	// artifact you open elsewhere, exactly as with the step photos above.
	b.WriteString(StyleTitle.Render("Documentation & References") + "\n")
	refDocs, refLinks := woReferenceParts(wo)
	if len(refDocs) == 0 && len(refLinks) == 0 {
		b.WriteString(StyleMuted.Render("  No linked documents.") + "\n")
	} else {
		for _, d := range refDocs {
			line := "  · " + d.Title
			if d.CategoryDisplay != "" {
				line += StyleMuted.Render(" — " + d.CategoryDisplay)
			}
			if d.Version > 0 {
				line += StyleMuted.Render(fmt.Sprintf(" (rev %d)", d.Version))
			}
			b.WriteString(line + "\n")
			// The URL gets its own line — they are long enough to wrap a narrow
			// terminal if they trail the title.
			if d.FileURL != "" {
				b.WriteString("    " + d.FileURL + "\n")
			}
			if summary := refRevisionSummary(d.Revisions); summary != "" {
				b.WriteString("    " + StyleMuted.Render("revisions: "+summary) + "\n")
			}
		}
		for _, l := range refLinks {
			b.WriteString("  · " + StyleMuted.Render(l.Label+": ") + l.URL + "\n")
		}
	}
	b.WriteString("\n")

	if len(wo.TaskCompletions) > 0 {
		done, total := 0, len(wo.TaskCompletions)
		for _, t := range wo.TaskCompletions {
			if t.IsCompleted {
				done++
			}
		}
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Tasks (%d/%d)", done, total)) + "\n")
		for _, t := range wo.TaskCompletions {
			marker := "  ○ "
			if t.IsCompleted {
				marker = StyleStatusOK.Render("  ✓ ")
			}
			title := t.TaskTitle
			if !t.IsRequired {
				title += " " + StyleMuted.Render("(optional)")
			}
			b.WriteString(marker + title + "\n")
			meta := []string{}
			// The step's own clock leads its meta line: on a job being worked
			// right now it is the line's only changing value. Steps nobody timed
			// stay silent rather than printing a column of 00:00.
			if secs := s.liveSeconds(t.ElapsedSeconds, t.IsTiming); secs > 0 || t.IsTiming {
				entry := formatElapsed(secs)
				if t.IsTiming {
					entry += " ● running"
				}
				meta = append(meta, entry)
			}
			if t.CompletedAt != nil {
				meta = append(meta, t.CompletedAt.Format("2006-01-02 15:04"))
			}
			if t.CompletedBy != "" {
				meta = append(meta, "by "+t.CompletedBy)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
			if t.Notes != "" {
				b.WriteString("    " + StyleMuted.Render(t.Notes) + "\n")
			}
			// Reference = what the step should look like (set on the PM
			// template); evidence = what the tech shot while doing it. Neither
			// renders inline — the URL is the artifact you can open elsewhere.
			if t.TaskReferenceImageURL != "" {
				b.WriteString("    " + StyleMuted.Render("Reference photo: ") + t.TaskReferenceImageURL + "\n")
			}
			if len(t.EvidencePhotos) > 0 {
				b.WriteString("    " + StyleMuted.Render(fmt.Sprintf("Evidence (%d):", len(t.EvidencePhotos))) + "\n")
				for _, p := range t.EvidencePhotos {
					b.WriteString("      · " + evidencePhotoLabel(p) + "\n")
				}
			}
		}
		b.WriteString("\n")
	}

	// Materials always renders, even empty: on a corrective work order the empty
	// state is the prompt to record what the job actually consumed (op-768w).
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Materials (%d)", len(wo.MaterialUsage))) + "\n")
	if len(wo.MaterialUsage) == 0 {
		b.WriteString(StyleMuted.Render("  No materials recorded. Press M to add what you used or bought.") + "\n")
	}
	for _, m := range wo.MaterialUsage {
		line := "  · " + m.MaterialName
		if m.IsAdHoc {
			line += " " + StyleMuted.Render("(added)")
		}
		if m.WasUsed {
			line += " " + StyleStatusOK.Render("✓ used")
		}
		b.WriteString(line + "\n")
		if meta := materialMeta(m); meta != "" {
			b.WriteString("    " + StyleMuted.Render(meta) + "\n")
		}
		b.WriteString("    " + StyleMuted.Render(materialCostLine(m)) + "\n")
	}
	if len(wo.MaterialUsage) > 0 {
		b.WriteString(s.renderMaterialTotals() + "\n")
	}
	b.WriteString("\n")

	if len(wo.Photos) > 0 {
		b.WriteString(StyleTitle.Render(fmt.Sprintf("Photos (%d)", len(wo.Photos))) + "\n")
		for _, p := range wo.Photos {
			line := "  · "
			if p.Caption != "" {
				line += p.Caption
			} else {
				line += p.ImageURL
			}
			// The WO's photo list carries every photo, pinned or not, so flag
			// the ones that are really a step's evidence — they also appear
			// under their step above.
			if isPinnedToStep(p) {
				line += " " + StyleMuted.Render("· step evidence")
			}
			b.WriteString(line + "\n")
			meta := []string{}
			if !p.UploadedAt.IsZero() {
				meta = append(meta, p.UploadedAt.Format("2006-01-02"))
			}
			if p.UploadedBy != "" {
				meta = append(meta, "by "+p.UploadedBy)
			}
			if len(meta) > 0 {
				b.WriteString("    " + StyleMuted.Render(strings.Join(meta, " · ")) + "\n")
			}
		}
	}

	return b.String()
}
