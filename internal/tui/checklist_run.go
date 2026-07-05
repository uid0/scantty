package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

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
			if s.busy || s.completion == nil {
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

func (s *ChecklistRunScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading checklist run…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.checklist == nil || s.completion == nil {
		return StyleMuted.Render("Checklist run not found.")
	}

	var b strings.Builder
	statusLine := s.completion.Status
	switch s.completion.Status {
	case "completed":
		statusLine = StyleStatusOK.Render(s.completion.Status)
	case "in_progress":
		statusLine = StyleStatusWarn.Render(s.completion.Status)
	}
	progress := fmt.Sprintf("%d/%d", s.completion.CompletedStepsCount, s.completion.TotalStepsCount)
	required := fmt.Sprintf("required %d/%d", s.completion.RequiredStepsCompleted, s.completion.RequiredStepsTotal)
	b.WriteString(fmt.Sprintf("%s · %s · %s\n\n",
		statusLine,
		StyleMuted.Render(progress+" steps"),
		StyleMuted.Render(required)))

	steps := s.steps()
	if len(steps) == 0 {
		b.WriteString(StyleMuted.Render("This checklist has no steps.") + "\n")
	}
	for i, step := range steps {
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
		title := fmt.Sprintf("%s%s %d. %s", caret, marker, step.StepNumber, step.Name)
		if step.RequiresPhoto {
			title += "  " + StyleStatusWarn.Render("photo required")
		}
		if i == s.cursor && !done {
			title = StyleSidebarItemActive.Render(title)
		}
		b.WriteString(title + "\n")
		target := s.targetLabel(step)
		if target != "" {
			b.WriteString("    " + StyleMuted.Render(target) + "\n")
		}
		if step.Notes != "" {
			notes := step.Notes
			if len(notes) > 100 {
				notes = notes[:97] + "…"
			}
			b.WriteString("    " + StyleMuted.Render(notes) + "\n")
		}
	}

	if s.addingNotes {
		b.WriteString("\n" + StyleTitle.Render("Notes for this step") + "\n")
		b.WriteString(s.notesInput.View() + "\n")
		b.WriteString(StyleMuted.Render("enter submit · esc cancel"))
		return b.String()
	}

	footer := "j/k move · enter scan step · n notes then scan"
	if s.canFinalize() {
		footer += " · f finalize"
	}
	footer += " · r refresh · esc back"
	b.WriteString("\n" + StyleMuted.Render(footer))
	return b.String()
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
