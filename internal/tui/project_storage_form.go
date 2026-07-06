// ProjectStorageFormScreen — intake (create) form for a project-storage stint.
//
// This is the TUI counterpart to the web ProjectStorageKioskPage self-issue
// intake (frontend/src/pages/ProjectStorageKioskPage.tsx), which is the ONLY
// create path OMS exposes: the stint viewset is a ReadOnlyModelViewSet, so there
// is no POST /stints/ and no PATCH/PUT — every write goes through fixed custom
// actions, and "start" is the create one ([[scantty-parity-program]] Tier-2).
//
// Because the backend stores the member as a denormalized `username` string and
// the location as a `storage_location_name` string — NEITHER is a foreign key
// (the model deliberately avoids FK'ing inventory.Location) — this form is pure
// free-text entry with NO pickers, unlike asset_form.go. It mirrors the exact
// StartStintSerializer field set and nothing more: username (required) plus the
// optional first/last name, email, project title, and storage location.
//
// There is intentionally no edit mode: OMS has no update endpoint for a stint
// (a PATCH 405s), so amending a stint after intake is not a capability that
// exists to mirror. Post-intake state changes are the warden lifecycle actions
// (mark-removed lives on the detail screen; notice / purgatory ride their own
// parity beads, like reprint did).
//
// Navigation: tab / ↑↓ move between fields, enter saves, esc cancels. The form is
// a raw-input screen so every key reaches the focused textinput.
package tui

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Field identifiers, in the canonical top-to-bottom render order. Every field is
// always visible (there are no conditional sections), so unlike asset_form there
// is no rebuildFields step.
const (
	psfUsername = iota
	psfFirstName
	psfLastName
	psfEmail
	psfProjectTitle
	psfStorageLocation
	psfFieldMax
)

var projectStorageFieldLabel = map[int]string{
	psfUsername:        "Username",
	psfFirstName:       "First name",
	psfLastName:        "Last name",
	psfEmail:           "Email",
	psfProjectTitle:    "Project title",
	psfStorageLocation: "Storage location",
}

func projectStorageCharLimitFor(id int) int {
	switch id {
	case psfUsername, psfFirstName, psfLastName:
		return 64 // model CharField(64)
	case psfProjectTitle, psfStorageLocation:
		return 120 // model CharField(120)
	case psfEmail:
		return 254 // RFC-max EmailField
	default:
		return 120
	}
}

func projectStoragePlaceholderFor(id int) string {
	switch id {
	case psfUsername:
		return "member username (required)"
	case psfFirstName:
		return "optional"
	case psfLastName:
		return "optional"
	case psfEmail:
		return "member@example.com (optional)"
	case psfProjectTitle:
		return "blank = personal storage"
	case psfStorageLocation:
		return "e.g. Rack B3 (optional)"
	default:
		return ""
	}
}

type ProjectStorageFormScreen struct {
	deps Deps

	saving bool
	errMsg string

	// Text field storage, indexed by field id.
	inputs []textinput.Model

	// Static visible-field navigation.
	fields []int
	cursor int

	terminalHeight int
}

type projectStorageFormSavedMsg struct {
	stint *omsapi.ProjectStorageStint
	err   error
}

// NewProjectStorageFormScreen builds the intake (create) form. There is no edit
// variant — the backend has no stint update endpoint (see the file comment).
func NewProjectStorageFormScreen(deps Deps) *ProjectStorageFormScreen {
	s := &ProjectStorageFormScreen{deps: deps}
	s.inputs = make([]textinput.Model, psfFieldMax)
	for id := 0; id < psfFieldMax; id++ {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = projectStorageCharLimitFor(id)
		ti.Placeholder = projectStoragePlaceholderFor(id)
		s.inputs[id] = ti
	}
	s.fields = []int{psfUsername, psfFirstName, psfLastName, psfEmail, psfProjectTitle, psfStorageLocation}
	s.syncFocus()
	return s
}

func (s *ProjectStorageFormScreen) Title() string { return "New project-storage stint" }

// WantsRawInput claims every keypress so field letters, tab, enter, and esc all
// reach the form instead of the root's global hotkeys.
func (s *ProjectStorageFormScreen) WantsRawInput() bool { return true }

func (s *ProjectStorageFormScreen) Init() tea.Cmd { return textinput.Blink }

func (s *ProjectStorageFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ProjectStorageFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case projectStorageFormSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("intake failed: "+m.err.Error(), StatusError)
		}
		// On success navigate to the freshly-created stint's detail. Guard the
		// (shouldn't-happen) empty-id case so we never GET /stints// — fall back
		// to the list instead.
		if m.stint == nil || strings.TrimSpace(m.stint.StintID) == "" {
			return s, tea.Batch(Status("stint created", StatusOK), SwitchTo(WSFacilities, NewProjectStorageListScreen(s.deps)))
		}
		return s, tea.Batch(
			Status("stint created for "+projectStorageOwner(m.stint), StatusOK),
			SwitchTo(WSFacilities, NewProjectStorageDetailScreen(s.deps, m.stint.StintID)),
		)

	case tea.KeyMsg:
		return s.updateForm(m)
	}

	// Non-key messages (cursor blink) go to the focused input.
	if id, ok := s.currentFieldID(); ok {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ProjectStorageFormScreen) updateForm(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

func (s *ProjectStorageFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *ProjectStorageFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *ProjectStorageFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		s.inputs[id].Blur()
	}
	if id, ok := s.currentFieldID(); ok {
		s.inputs[id].Focus()
	}
}

// buildPayload validates the form and produces the start-action body. username
// is the one required field (the serializer requires it); email, if present, is
// checked client-side for a nicer message than the backend's 400. Everything
// else is optional and carried by omitempty, so a blank field is dropped.
func (s *ProjectStorageFormScreen) buildPayload() (omsapi.ProjectStorageStintStart, error) {
	var w omsapi.ProjectStorageStintStart

	username := strings.TrimSpace(s.inputs[psfUsername].Value())
	if username == "" {
		return w, errors.New("username is required")
	}

	email := strings.TrimSpace(s.inputs[psfEmail].Value())
	if email != "" && !validStintEmail(email) {
		return w, errors.New("email must be a valid address")
	}

	w = omsapi.ProjectStorageStintStart{
		Username:            username,
		FirstName:           strings.TrimSpace(s.inputs[psfFirstName].Value()),
		LastName:            strings.TrimSpace(s.inputs[psfLastName].Value()),
		Email:               email,
		ProjectTitle:        strings.TrimSpace(s.inputs[psfProjectTitle].Value()),
		StorageLocationName: strings.TrimSpace(s.inputs[psfStorageLocation].Value()),
	}
	return w, nil
}

// validStintEmail accepts a bare address (no display-name part), matching what
// the backend EmailField expects. mail.ParseAddress tolerates "Name <a@b>"
// forms, so we additionally require the parsed address to equal the raw input.
func validStintEmail(v string) bool {
	addr, err := mail.ParseAddress(v)
	return err == nil && addr.Address == v
}

func (s *ProjectStorageFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		st, e := deps.OMS.StartProjectStorageStint(ctx, body)
		return projectStorageFormSavedMsg{stint: st, err: e}
	}
}

func (s *ProjectStorageFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSFacilities, NewProjectStorageListScreen(s.deps))
}

func (s *ProjectStorageFormScreen) View() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("type to edit · tab/↑↓ move · enter save · esc cancel") + "\n\n")

	visible := s.visibleRows()
	start, end := fieldWindow(s.cursor, len(s.fields), visible)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(s.renderField(i) + "\n")
	}
	if end < len(s.fields) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.fields)-end)) + "\n")
	}

	b.WriteString("\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	} else {
		b.WriteString(StyleMuted.Render("* required · intake self-issues a 30-day stint"))
	}
	return b.String()
}

func (s *ProjectStorageFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := projectStorageFieldLabel[id]
	if id == psfUsername {
		label += " *"
	}
	return caret + StyleTitle.Render(label+": ") + s.inputs[id].View()
}

// visibleRows returns how many field rows fit given the current terminal height,
// reserving space for the help line, spacing, status line, and scroll indicators.
func (s *ProjectStorageFormScreen) visibleRows() int {
	const chrome = 6 // help + blank + blank + status + 2 scroll indicators
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}
