package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/omsapi"
)

// ChecklistRunScreen is the per-completion stepper: shows every step
// in the checklist with its scan target + completion status, lets the
// operator tap through them with Enter (sends a /scan/ with the
// prescribed asset/location/item id), optionally captures notes, and
// calls /complete/ when every required step has been scanned.
//
// Photo uploads are intentionally not handled here — the TUI can't
// capture images; steps with requires_photo=true surface a "needs
// web" hint and refuse to be completed via Enter. The web
// ForgeKeyEPaperServicePage / ChecklistCompletionPage are still the
// path for those.
type ChecklistRunScreen struct {
	deps         Deps
	completionID string

	checklist  *omsapi.Checklist
	completion *omsapi.ChecklistCompletion
	cursor     int
	loading    bool
	loadErr    string
	busy       bool

	// Optional notes capture before scanning the cursor step. The
	// textinput is only shown while addingNotes is true; the rest of
	// the time the screen is read-only.
	addingNotes bool
	notesInput  textinput.Model

	terminalWidth  int
	terminalHeight int
	windowStart    int
}

type checklistRunLoadedMsg struct {
	checklist  *omsapi.Checklist
	completion *omsapi.ChecklistCompletion
	err        error
}

type checklistScanResultMsg struct {
	completion *omsapi.ChecklistCompletion
	err        error
}

type checklistCompleteResultMsg struct {
	completion *omsapi.ChecklistCompletion
	err        error
}

func NewChecklistRunScreen(deps Deps, completionID string) *ChecklistRunScreen {
	return &ChecklistRunScreen{deps: deps, completionID: completionID, loading: true}
}

func (s *ChecklistRunScreen) Title() string {
	if s.checklist != nil {
		return "Checklist: " + s.checklist.Name
	}
	return "Checklist run"
}

func (s *ChecklistRunScreen) WantsRawInput() bool { return s.addingNotes }

// canFinalize reports whether the run is ready to be finalized: every required
// step scanned and not already completed. Gates both the 'f finalize' footer
// hint and the HandlesKey('f') claim so 'f' only overrides the global
// f=firmware nav when finalizing is actually on offer.
func (s *ChecklistRunScreen) canFinalize() bool {
	return s.completion != nil &&
		s.completion.RequiredStepsCompleted >= s.completion.RequiredStepsTotal &&
		s.completion.Status != "completed"
}

// HandlesKey claims the stepper shortcuts so they beat the colliding global
// nav keys — 'n' (add notes) over n=notifications, and 'f' (finalize) over
// f=firmware when the run is finalizable. When 'f' is not on offer it falls
// through to the global firmware nav. While the notes input is open
// WantsRawInput already routes every key here.
func (s *ChecklistRunScreen) HandlesKey(key string) bool {
	switch key {
	case "n", "r", "j", "k":
		return true
	case "f":
		return s.canFinalize()
	}
	return false
}

func (s *ChecklistRunScreen) Init() tea.Cmd { return s.load() }

func (s *ChecklistRunScreen) load() tea.Cmd {
	deps := s.deps
	id := s.completionID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		completion, err := deps.OMS.GetChecklistCompletion(ctx, id)
		if err != nil {
			return checklistRunLoadedMsg{err: err}
		}
		checklist, _ := deps.OMS.GetChecklist(ctx, completion.Checklist)
		return checklistRunLoadedMsg{checklist: checklist, completion: completion}
	}
}

func (s *ChecklistRunScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalWidth, s.terminalHeight = m.Width, m.Height
		return s, nil
	case checklistRunLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
			return s, nil
		}
		s.loadErr = ""
		s.checklist = m.checklist
		s.completion = m.completion
		if s.checklist != nil && s.cursor >= len(s.checklist.Steps) {
			s.cursor = 0
		}
		// Jump the cursor to the first non-completed step on first
		// load so the operator lands on the active row instead of
		// the top.
		if s.cursor == 0 {
			for i, step := range s.steps() {
				if !s.stepDone(step.ID) {
					s.cursor = i
					break
				}
			}
		}
		return s, nil
	case checklistScanResultMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("scan failed: "+m.err.Error(), StatusError)
		}
		// The scan endpoint returns the full completion with updated
		// step_completions; refresh both that and the cached checklist
		// so the new row reflects in the UI.
		s.completion = m.completion
		// Move cursor to the next not-yet-done step if one exists.
		steps := s.steps()
		for i := s.cursor + 1; i < len(steps); i++ {
			if !s.stepDone(steps[i].ID) {
				s.cursor = i
				break
			}
		}
		return s, Status("step scanned", StatusOK)
	case checklistCompleteResultMsg:
		s.busy = false
		if m.err != nil {
			return s, Status("complete failed: "+m.err.Error(), StatusError)
		}
		s.completion = m.completion
		return s, Status("checklist completed", StatusOK)
	case tea.KeyMsg:
		if s.addingNotes {
			switch m.Type {
			case tea.KeyEsc:
				s.addingNotes = false
				return s, nil
			case tea.KeyEnter:
				notes := strings.TrimSpace(s.notesInput.Value())
				s.addingNotes = false
				return s, s.scanCursor(notes)
			}
			var cmd tea.Cmd
			s.notesInput, cmd = s.notesInput.Update(msg)
			return s, cmd
		}
		if proseLoadKeyHidden(s.loading, s.loadErr, s.loadBar(), m.String()) {
			return s, nil
		}
		steps := s.steps()
		switch m.String() {
		case "j", "down":
			if s.cursor < len(steps)-1 {
				s.cursor++
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
			}
		case "r":
			s.loading = true
			return s, s.load()
		case "enter":
			if s.busy || s.cursor >= len(steps) {
				return s, nil
			}
			return s, s.scanCursor("")
		case "n":
			if s.busy || s.cursor >= len(steps) {
				return s, nil
			}
			ti := textinput.New()
			ti.Prompt = ""
			ti.Placeholder = "notes (optional, enter to submit)"
			ti.CharLimit = 500
			ti.Focus()
			s.notesInput = ti
			s.addingNotes = true
			return s, textinput.Blink
		case "f":
			// canFinalize, not a nil check, because that is the only state Root
			// ever delivers `f` in: HandlesKey claims it there and nowhere else, so
			// the looser guard this replaced was an arm no operator could reach —
			// and a bar sweep pressing the screen directly found it finalizing a
			// run whose bar, rightly, names no such key.
			if s.busy || !s.canFinalize() {
				return s, nil
			}
			s.busy = true
			deps := s.deps
			ctx := deps.Ctx
			if ctx == nil {
				ctx = context.Background()
			}
			completionID := s.completionID
			return s, func() tea.Msg {
				out, err := deps.OMS.CompleteChecklist(ctx, completionID)
				return checklistCompleteResultMsg{completion: out, err: err}
			}
		}
	}
	return s, nil
}

// scanCursor builds the scan request for the step under the cursor and
// fires it. The step's prescribed asset/location/item id is what we
// send — that's also what the backend validates against.
func (s *ChecklistRunScreen) scanCursor(notes string) tea.Cmd {
	steps := s.steps()
	if s.cursor >= len(steps) {
		return nil
	}
	step := steps[s.cursor]
	if step.RequiresPhoto {
		return Status("step requires a photo — complete via the web UI", StatusError)
	}
	req := omsapi.ChecklistStepScanRequest{StepID: step.ID, Notes: notes}
	switch {
	case step.Asset != nil:
		assetID := *step.Asset
		req.AssetID = &assetID
	case step.Location != nil:
		locID := *step.Location
		req.LocationID = &locID
	case step.InventoryItem != nil:
		itemID := *step.InventoryItem
		req.ItemID = &itemID
	default:
		return Status("step has no scan target", StatusError)
	}
	s.busy = true
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	completionID := s.completionID
	return func() tea.Msg {
		out, err := deps.OMS.ScanChecklistStep(ctx, completionID, req)
		return checklistScanResultMsg{completion: out, err: err}
	}
}

func (s *ChecklistRunScreen) steps() []omsapi.ChecklistStep {
	if s.checklist == nil {
		return nil
	}
	return s.checklist.Steps
}

func (s *ChecklistRunScreen) stepDone(stepID string) bool {
	if s.completion == nil {
		return false
	}
	for _, c := range s.completion.StepCompletions {
		if c.Step == stepID {
			return true
		}
	}
	return false
}

// checklistRunBar names every key that acts on a run of `steps` steps, as a
// record the honesty sweep can press (prose_bar.go).
//
// It used to be one literal under every step with no window — "j/k move · enter
// scan step · n notes then scan · f finalize · r refresh · esc back" — so a
// checklist longer than the pane took the footer off the bottom, and the arrows
// moved the cursor unnamed. It also went on naming `enter` and `n` while a scan
// or a finalize was out, where both arms return without acting, and over a
// checklist with no steps, where there is nothing for either to act on: the
// "footer that changes with the submit state" this screen was recorded for.
// Those segments follow `busy` and the step count now, and `f` is named exactly
// where HandlesKey claims it.
func checklistRunBar(steps int, busy, finalize bool) proseBar {
	out := proseNavStep(listNavMoves(steps))
	if steps > 0 && !busy {
		out = append(out,
			proseBarItem{Keys: []string{"enter"}, Hint: "enter scan step"},
			proseBarItem{Keys: []string{"n"}, Hint: "n notes then scan"})
	}
	if finalize && !busy {
		out = append(out, proseBarItem{Keys: []string{"f"}, Hint: "f finalize"})
	}
	return append(out, proseBarRefresh, proseBarEsc)
}

// checklistRunNotesBar is the notes prompt's bar. The box has the focus, so
// every printable key is a character in the note and only these two are not.
var checklistRunNotesBar = proseBar{
	{Keys: []string{"enter"}, Hint: "enter scan with notes"},
	{Keys: []string{"esc"}, Hint: "esc cancel"},
}

// proseBar is the bar this screen is DRAWING, in every state: the notes prompt
// draws its own, and a load in flight or failed draws loadBar's.
func (s *ChecklistRunScreen) proseBar() proseBar {
	if s.addingNotes {
		return checklistRunNotesBar
	}
	if s.loading || s.loadErr != "" {
		return s.loadBar()
	}
	return checklistRunBar(len(s.steps()), s.busy, s.canFinalize())
}

// loadBar is this run's bar while its load is out or has failed — what its key
// switch still answers with no steps drawn (prose_bar.go carries the defect and
// the decision). A refresh keeps the checklist and the completion, so `enter`
// still scans the step under the cursor and `f` still finalizes: named because
// they act, and candidates for gating. j/k move a cursor the frame does not draw
// and `n` opens a notes box it does not draw either, so they are not named and
// are ignored.
func (s *ChecklistRunScreen) loadBar() proseBar {
	var out proseBar
	if len(s.steps()) > 0 && !s.busy {
		out = append(out, proseBarItem{Keys: []string{"enter"}, Hint: "enter scan step"})
	}
	if s.canFinalize() && !s.busy {
		out = append(out, proseBarItem{Keys: []string{"f"}, Hint: "f finalize"})
	}
	return append(out, proseBarReloadFor(s.loadErr != ""), proseBarEsc)
}

func (s *ChecklistRunScreen) paneCells() int { return proseBarCells(s.terminalWidth) }

// View draws the steps as a line-packed window (proseFlatListFrameFoot) under
// the run's status line, with the bar — or the notes prompt and ITS bar — as the
// foot the window is budgeted around.
//
// THE NOTES PROMPT WAS DRAWN UNDER EVERY STEP, so on a checklist longer than the
// pane `n` opened a box nobody could see: the keystrokes went into it, `enter`
// scanned the step with whatever had been typed, and the pane showed none of it.
// It is the foot now, spent before the window gets a line.
func (s *ChecklistRunScreen) View() string {
	cells := s.paneCells()
	if s.loading {
		return proseLoadingFrame("Loading checklist run…", cells, s.proseBar())
	}
	if s.loadErr != "" {
		return proseFailedFrame(s.loadErr, s.terminalHeight, cells, s.proseBar())
	}
	if s.checklist == nil || s.completion == nil {
		return StyleMuted.Render("Checklist run not found.") + "\n\n" + s.proseBar().render(cells)
	}

	var head strings.Builder
	statusLine := s.completion.Status
	switch s.completion.Status {
	case "completed":
		statusLine = StyleStatusOK.Render(s.completion.Status)
	case "in_progress":
		statusLine = StyleStatusWarn.Render(s.completion.Status)
	}
	progress := fmt.Sprintf("%d/%d", s.completion.CompletedStepsCount, s.completion.TotalStepsCount)
	required := fmt.Sprintf("required %d/%d", s.completion.RequiredStepsCompleted, s.completion.RequiredStepsTotal)
	head.WriteString(fmt.Sprintf("%s · %s · %s\n",
		statusLine,
		StyleMuted.Render(progress+" steps"),
		StyleMuted.Render(required)))
	if s.busy {
		// The keys that write come off the bar while a scan or a finalize is
		// out, and the pane says why rather than leaving the operator to read the
		// absence as a program that stopped listening.
		head.WriteString(StyleMuted.Render("Sending to OMS…") + "\n")
	}
	head.WriteString("\n")

	steps := s.steps()
	if len(steps) == 0 {
		return head.String() + StyleMuted.Render("This checklist has no steps.") + "\n\n" +
			s.proseBar().render(cells)
	}
	rows := make([]string, len(steps))
	for i, step := range steps {
		rows[i] = s.stepRow(i, step, cells)
	}

	footRows := checklistRunBar(proseFlatCeilingRows, false, true).rows(cells)
	foot := s.proseBar().render(cells)
	if s.addingNotes {
		// Title, box, the blank line above the bar, and the bar's own rows.
		footRows = 3 + s.proseBar().rows(cells)
		foot = StyleTitle.Render("Notes for this step") + "\n" +
			woBoxView(s.notesInput, cells, "") + "\n\n" + foot
	}
	return proseFlatListFrameFoot(head.String(), rows, s.cursor, &s.windowStart,
		s.terminalHeight, footRows, foot)
}

// stepRow is one step: its title, its scan target and its notes.
//
// The NAME and the NOTES are OMS values and are clipped to the pane line by line
// with the cut marked (proseClipEachLine). The notes used to be cut at 97 BYTES,
// which is neither the pane — 51 cells at 80 columns, so clampToBox took the rest
// unmarked — nor a character boundary, so a multi-byte character straddling the
// cut drew as a broken glyph. The highlight's padding is reserved on every row,
// for the reason item_suppliers.go gives: a row that fits until it is selected is
// cut on exactly the keypress that selects it.
func (s *ChecklistRunScreen) stepRow(i int, step omsapi.ChecklistStep, cells int) string {
	const indent = "    "
	done := s.stepDone(step.ID)
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	var marker string
	if done {
		marker = StyleStatusOK.Render("[✓]")
	} else if step.Required {
		marker = StyleStatusError.Render("[!]")
	} else {
		marker = StyleMuted.Render("[ ]")
	}
	prefix := fmt.Sprintf("%s%s %d. ", caret, marker, step.StepNumber)
	photo := ""
	if step.RequiresPhoto {
		photo = "  " + StyleStatusWarn.Render("photo required")
	}
	room := cells - StyleSidebarItemActive.GetHorizontalPadding() - lipgloss.Width(prefix) - lipgloss.Width(photo)
	title := prefix + proseClipEachLine(step.Name, room) + photo
	if i == s.cursor && !done {
		title = StyleSidebarItemActive.Render(title)
	}
	out := title
	if target := s.targetLabel(step); target != "" {
		out += "\n" + indent + StyleMuted.Render(pickerClip(target, cells-len(indent)))
	}
	if step.Notes != "" {
		for _, line := range strings.Split(step.Notes, "\n") {
			out += "\n" + indent + StyleMuted.Render(pickerClip(line, cells-len(indent)))
		}
	}
	return out
}

// targetLabel produces a short description of the step's prescribed
// scan target. Uses the FK ids since the step serializer doesn't ship
// pre-joined display names; the operator's existing context (the
// step's Name field) covers the human-readable hint.
func (s *ChecklistRunScreen) targetLabel(step omsapi.ChecklistStep) string {
	switch {
	case step.Asset != nil:
		id := *step.Asset
		if len(id) > 12 {
			id = id[:12] + "…"
		}
		return "scan target: asset " + id
	case step.Location != nil:
		return fmt.Sprintf("scan target: location #%d", *step.Location)
	case step.InventoryItem != nil:
		id := *step.InventoryItem
		if len(id) > 12 {
			id = id[:12] + "…"
		}
		return "scan target: item " + id
	}
	return ""
}
