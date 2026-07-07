// Location-problem reporting — the "report a problem" form.
//
// TUI counterpart to the web ReportLocationProblemModal (oms-sd1). Files a new
// LocationProblem against a location via the Location.report_problem @action.
// Mirrors the web modal's FULL field set: description (required), severity
// (low/medium/high/urgent, default medium) and an OPTIONAL reporter photo. The
// web picks the photo from a file input; the TUI accepts an absolute local file
// path (the same file-path→multipart idiom site-settings uses for logo/favicon).
// Leaving the path blank reports without a photo. There is no edit mode — a
// report is immutable except through the resolve action (see location_problems.go).
//
// Reached from the location detail screen (p → problems list → n) and, on
// success, returns to that problems list. The form is always raw-input so every
// keystroke edits the focused field.
package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// locationProblemSeverityOptions is the ordered severity cycle (codes + labels),
// matching the web modal's SEVERITY_OPTIONS. Default is medium (index 1).
var locationProblemSeverityOptions = []selectOption{
	{omsapi.LocationProblemSeverityLow, "Low"},
	{omsapi.LocationProblemSeverityMedium, "Medium"},
	{omsapi.LocationProblemSeverityHigh, "High"},
	{omsapi.LocationProblemSeverityUrgent, "Urgent"},
}

const (
	lpfDescription = iota
	lpfSeverity
	lpfPhoto
	lpfFieldMax
)

var locationProblemFieldLabel = map[int]string{
	lpfDescription: "Description",
	lpfSeverity:    "Severity",
	lpfPhoto:       "Photo path",
}

type LocationProblemFormScreen struct {
	deps    Deps
	locID   int
	locName string

	saving bool
	errMsg string

	inputs      []textinput.Model
	severityIdx int

	fields []int
	cursor int
}

type locationProblemReportedMsg struct {
	problem *omsapi.LocationProblem
	err     error
}

// NewLocationProblemFormScreen builds the report form for a location.
func NewLocationProblemFormScreen(deps Deps, locID int, locName string) *LocationProblemFormScreen {
	s := &LocationProblemFormScreen{
		deps:        deps,
		locID:       locID,
		locName:     strings.TrimSpace(locName),
		severityIdx: 1, // medium (web default)
	}
	s.inputs = make([]textinput.Model, lpfFieldMax)
	desc := textinput.New()
	desc.Prompt = ""
	desc.CharLimit = 1000
	desc.Placeholder = "describe the problem (leak, broken light, broken door)…"
	s.inputs[lpfDescription] = desc

	photo := textinput.New()
	photo.Prompt = ""
	photo.CharLimit = 500
	photo.Placeholder = "optional /absolute/path/to/photo.jpg"
	s.inputs[lpfPhoto] = photo

	s.fields = []int{lpfDescription, lpfSeverity, lpfPhoto}
	s.syncFocus()
	return s
}

func (s *LocationProblemFormScreen) Title() string {
	if s.locName != "" {
		return "Report problem: " + s.locName
	}
	return "Report location problem"
}

func (s *LocationProblemFormScreen) WantsRawInput() bool { return true }

func (s *LocationProblemFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *LocationProblemFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *LocationProblemFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case locationProblemReportedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("report failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("problem reported", StatusOK),
			SwitchTo(WSInventory, NewLocationProblemsScreen(s.deps, s.locID, s.locName)),
		)
	case tea.KeyMsg:
		return s.updateForm(m)
	}
	if id, ok := s.currentFieldID(); ok && (id == lpfDescription || id == lpfPhoto) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *LocationProblemFormScreen) updateForm(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch id {
	case lpfSeverity:
		switch m.String() {
		case " ", "right":
			s.cycleSeverity(+1)
		case "left":
			s.cycleSeverity(-1)
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *LocationProblemFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *LocationProblemFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *LocationProblemFormScreen) cycleSeverity(delta int) {
	n := len(locationProblemSeverityOptions)
	s.severityIdx = (s.severityIdx + delta + n) % n
}

func (s *LocationProblemFormScreen) syncFocus() {
	for _, id := range []int{lpfDescription, lpfPhoto} {
		s.inputs[id].Blur()
	}
	if id, ok := s.currentFieldID(); ok && (id == lpfDescription || id == lpfPhoto) {
		s.inputs[id].Focus()
	}
}

// buildReport assembles the report payload from the form, validating the one
// required field (description). Extracted so it is unit-testable without a
// network round-trip (mirrors the webhook form's buildPayload).
func (s *LocationProblemFormScreen) buildReport() (omsapi.LocationProblemReport, error) {
	desc := strings.TrimSpace(s.inputs[lpfDescription].Value())
	if desc == "" {
		return omsapi.LocationProblemReport{}, errors.New("description is required")
	}
	return omsapi.LocationProblemReport{
		Description: desc,
		Severity:    locationProblemSeverityOptions[s.severityIdx].value,
		PhotoPath:   strings.TrimSpace(s.inputs[lpfPhoto].Value()),
	}, nil
}

func (s *LocationProblemFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildReport()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	locID := strconv.Itoa(s.locID)
	return s, func() tea.Msg {
		p, err := deps.OMS.ReportLocationProblem(ctx, locID, body)
		return locationProblemReportedMsg{problem: p, err: err}
	}
}

func (s *LocationProblemFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSInventory, NewLocationProblemsScreen(s.deps, s.locID, s.locName))
}

func (s *LocationProblemFormScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Reporting…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *LocationProblemFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := locationProblemFieldLabel[id]
	var value string
	switch id {
	case lpfSeverity:
		value = elecSelectLabel(locationProblemSeverityOptions, s.severityIdx)
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *LocationProblemFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok && id == lpfSeverity {
		kindHelp = "space/←→ change"
	}
	return kindHelp + " · tab/↑↓ move · enter submit · esc cancel"
}
