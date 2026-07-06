// Maker Box CRUD — create/edit form.
//
// TUI counterpart to the maker-box row-CRUD the backend MakerBoxViewSet exposes.
// The React web app has NO row-CRUD form (a bin's lifecycle there is scan →
// pre-convert → convert), so there is no web form to mirror field-for-field;
// this instead covers the FULL writable MakerBoxSerializer set — the same
// surface the Django admin gives staff ([[scantty-makerbox-backend]]) — so a
// Logistics operator can create or correct any bin field without the browser.
//
// Reached from MakerBoxesScreen (global `B`): `n` opens create, `E` edits the
// row under the cursor. Every verb is staff-gated; a non-Logistics user sees a
// clean 4xx on save rather than a crash. The form is a raw-input screen (all
// keys reach the inputs) and returns to the maker-box list on save or cancel.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// makerBoxStatusOptions mirror the model STATUS_CHOICES. "unassigned" is the
// model default and leads so a fresh create defaults to it.
var makerBoxStatusOptions = []selectOption{
	{"unassigned", "Unassigned"},
	{"valid", "Valid"},
	{"grace", "Grace"},
	{"expired", "Expired"},
	{"unknown", "Unknown"},
	{"pre_conversion", "Pre-conversion"},
}

// makerBoxIdentityOptions mirror the model IDENTITY_SOURCE_CHOICES, with a
// leading blank so the field is optional (model blank=True default="").
var makerBoxIdentityOptions = []selectOption{
	{"", "(none)"},
	{"whmcs", "WHMCS"},
	{"common_api", "Common API (badge / AD)"},
	{"manual", "Manual admin entry"},
}

func makerBoxStatusIndex(v string) int {
	for i, o := range makerBoxStatusOptions {
		if o.value == v {
			return i
		}
	}
	return 0
}

func makerBoxIdentityIndex(v string) int {
	for i, o := range makerBoxIdentityOptions {
		if o.value == v {
			return i
		}
	}
	return 0
}

// makerBoxParseDateTime normalizes a datetime field's raw input for the wire.
// Blank → nil (JSON null clears the field). A bare date (YYYY-MM-DD) is promoted
// to midnight UTC because DRF's DateTimeField rejects a date-only ISO string; a
// full RFC3339 timestamp passes through unchanged. Anything else is a
// validation error surfaced before the request goes out.
func makerBoxParseDateTime(raw string) (*string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil, nil
	}
	if _, err := time.Parse(time.RFC3339, v); err == nil {
		out := v
		return &out, nil
	}
	if _, err := time.Parse("2006-01-02", v); err == nil {
		out := v + "T00:00:00Z"
		return &out, nil
	}
	return nil, errors.New("use YYYY-MM-DD or an RFC3339 timestamp")
}

// fmtMakerBoxDateTime renders a stored datetime back into the editable field on
// hydrate. RFC3339 round-trips losslessly through makerBoxParseDateTime.
func fmtMakerBoxDateTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

const (
	mbfBinID = iota
	mbfStatus
	mbfAssignedUsername
	mbfFirstName
	mbfLastName
	mbfEmail
	mbfIdentitySource
	mbfAssignedAt
	mbfExpiresAt
	mbfConversionCompletedAt
	mbfPaidAt
	mbfNotes
	mbfFieldMax
)

var makerBoxFieldLabel = map[int]string{
	mbfBinID:                 "Bin id",
	mbfStatus:                "Status",
	mbfAssignedUsername:      "Assigned username",
	mbfFirstName:             "First name",
	mbfLastName:              "Last name",
	mbfEmail:                 "Email",
	mbfIdentitySource:        "Identity source",
	mbfAssignedAt:            "Assigned at",
	mbfExpiresAt:             "Expires at",
	mbfConversionCompletedAt: "Conversion completed at",
	mbfPaidAt:                "Paid at",
	mbfNotes:                 "Notes",
}

func makerBoxFieldIsText(id int) bool {
	switch id {
	case mbfStatus, mbfIdentitySource:
		return false
	}
	return true
}

func makerBoxFieldIsDateTime(id int) bool {
	switch id {
	case mbfAssignedAt, mbfExpiresAt, mbfConversionCompletedAt, mbfPaidAt:
		return true
	}
	return false
}

type MakerBoxFormScreen struct {
	deps  Deps
	edit  bool
	boxID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	box *omsapi.MakerBox

	terminalHeight int

	inputs      []textinput.Model
	statusIdx   int
	identityIdx int

	fields []int
	cursor int
}

type makerBoxFormLoadedMsg struct {
	box *omsapi.MakerBox
	err error
}

type makerBoxFormSavedMsg struct {
	box *omsapi.MakerBox
	err error
}

// NewMakerBoxFormScreen opens create mode when boxID is 0, otherwise edit mode
// (hydrating from the fetched bin). MakerBox ids are ints (BigAutoField), so 0
// is a safe "no id yet" sentinel — mirrors the electrical forms' 0=create.
func NewMakerBoxFormScreen(deps Deps, boxID int) *MakerBoxFormScreen {
	s := &MakerBoxFormScreen{
		deps:  deps,
		edit:  boxID != 0,
		boxID: boxID,
	}
	s.inputs = make([]textinput.Model, mbfFieldMax)
	for id := 0; id < mbfFieldMax; id++ {
		if !makerBoxFieldIsText(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = makerBoxCharLimit(id)
		ti.Placeholder = makerBoxPlaceholder(id)
		s.inputs[id] = ti
	}
	s.fields = []int{
		mbfBinID, mbfStatus, mbfAssignedUsername, mbfFirstName, mbfLastName,
		mbfEmail, mbfIdentitySource, mbfAssignedAt, mbfExpiresAt,
		mbfConversionCompletedAt, mbfPaidAt, mbfNotes,
	}
	if s.edit {
		s.loading = true
	}
	s.syncFocus()
	return s
}

func makerBoxCharLimit(id int) int {
	switch id {
	case mbfBinID:
		return 20
	case mbfAssignedUsername, mbfFirstName, mbfLastName:
		return 64
	case mbfEmail:
		return 254
	case mbfNotes:
		return 2000
	}
	if makerBoxFieldIsDateTime(id) {
		return 40
	}
	return 64
}

func makerBoxPlaceholder(id int) string {
	switch id {
	case mbfBinID:
		return "PSB-007 / MBX-001 (blank = unallocated)"
	case mbfAssignedUsername:
		return "WHMCS username (optional)"
	case mbfFirstName, mbfLastName:
		return "optional"
	case mbfEmail:
		return "member@example.com (optional)"
	case mbfNotes:
		return "optional"
	}
	if makerBoxFieldIsDateTime(id) {
		return "YYYY-MM-DD (optional)"
	}
	return ""
}

func (s *MakerBoxFormScreen) Title() string {
	if s.edit {
		if s.box != nil && s.box.BinID != "" {
			return "Edit maker box: " + s.box.BinID
		}
		return "Edit maker box"
	}
	return "New maker box"
}

func (s *MakerBoxFormScreen) WantsRawInput() bool { return true }

func (s *MakerBoxFormScreen) Init() tea.Cmd {
	if s.edit {
		return tea.Batch(s.loadBox(), textinput.Blink)
	}
	return textinput.Blink
}

func (s *MakerBoxFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *MakerBoxFormScreen) loadBox() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.boxID
	return func() tea.Msg {
		box, err := deps.OMS.GetMakerBox(ctx, id)
		return makerBoxFormLoadedMsg{box: box, err: err}
	}
}

func (s *MakerBoxFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case makerBoxFormLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.box = m.box
			s.hydrate()
		}
		s.syncFocus()
		return s, nil
	case makerBoxFormSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		label := ""
		if m.box != nil {
			label = m.box.BinID
			if label == "" {
				label = m.box.AssignedUsername
			}
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("maker box %s: %s", verb, label), StatusOK),
			SwitchTo(WSFacilities, NewMakerBoxesScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		return s.updateFormPhase(m)
	}

	if id, ok := s.currentFieldID(); ok && makerBoxFieldIsText(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *MakerBoxFormScreen) hydrate() {
	if s.box == nil {
		return
	}
	b := s.box
	s.inputs[mbfBinID].SetValue(b.BinID)
	s.statusIdx = makerBoxStatusIndex(b.Status)
	s.inputs[mbfAssignedUsername].SetValue(b.AssignedUsername)
	s.inputs[mbfFirstName].SetValue(b.FirstName)
	s.inputs[mbfLastName].SetValue(b.LastName)
	s.inputs[mbfEmail].SetValue(b.Email)
	s.identityIdx = makerBoxIdentityIndex(b.IdentitySource)
	s.inputs[mbfAssignedAt].SetValue(fmtMakerBoxDateTime(b.AssignedAt))
	s.inputs[mbfExpiresAt].SetValue(fmtMakerBoxDateTime(b.ExpiresAt))
	s.inputs[mbfConversionCompletedAt].SetValue(fmtMakerBoxDateTime(b.ConversionCompletedAt))
	s.inputs[mbfPaidAt].SetValue(fmtMakerBoxDateTime(b.PaidAt))
	s.inputs[mbfNotes].SetValue(b.Notes)
}

func (s *MakerBoxFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *MakerBoxFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if makerBoxFieldIsText(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && makerBoxFieldIsText(id) {
		s.inputs[id].Focus()
	}
}

func (s *MakerBoxFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	case mbfStatus:
		switch m.String() {
		case " ", "right":
			s.statusIdx = (s.statusIdx + 1) % len(makerBoxStatusOptions)
		case "left":
			s.statusIdx = (s.statusIdx - 1 + len(makerBoxStatusOptions)) % len(makerBoxStatusOptions)
		}
		return s, nil
	case mbfIdentitySource:
		switch m.String() {
		case " ", "right":
			s.identityIdx = (s.identityIdx + 1) % len(makerBoxIdentityOptions)
		case "left":
			s.identityIdx = (s.identityIdx - 1 + len(makerBoxIdentityOptions)) % len(makerBoxIdentityOptions)
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *MakerBoxFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *MakerBoxFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	edit := s.edit
	id := s.boxID
	return s, func() tea.Msg {
		var box *omsapi.MakerBox
		var e error
		if edit {
			box, e = deps.OMS.UpdateMakerBox(ctx, id, body)
		} else {
			box, e = deps.OMS.CreateMakerBox(ctx, body)
		}
		return makerBoxFormSavedMsg{box: box, err: e}
	}
}

func (s *MakerBoxFormScreen) buildPayload() (omsapi.MakerBoxWrite, error) {
	var w omsapi.MakerBoxWrite
	if s.statusIdx < 0 || s.statusIdx >= len(makerBoxStatusOptions) {
		return w, errors.New("status is required")
	}
	if s.identityIdx < 0 || s.identityIdx >= len(makerBoxIdentityOptions) {
		return w, errors.New("identity source is invalid")
	}
	binRaw := strings.TrimSpace(s.inputs[mbfBinID].Value())
	username := strings.TrimSpace(s.inputs[mbfAssignedUsername].Value())
	// A bin row with neither an id nor an assignee has no identity; the backend
	// permits it but it is never useful, so guard the accidental empty submit.
	if binRaw == "" && username == "" {
		return w, errors.New("bin id or assigned username is required")
	}
	email := strings.TrimSpace(s.inputs[mbfEmail].Value())
	if email != "" && !strings.Contains(email, "@") {
		return w, errors.New("email must contain @ (or leave blank)")
	}
	assignedAt, err := makerBoxParseDateTime(s.inputs[mbfAssignedAt].Value())
	if err != nil {
		return w, fmt.Errorf("assigned-at: %w", err)
	}
	expiresAt, err := makerBoxParseDateTime(s.inputs[mbfExpiresAt].Value())
	if err != nil {
		return w, fmt.Errorf("expires-at: %w", err)
	}
	conversionAt, err := makerBoxParseDateTime(s.inputs[mbfConversionCompletedAt].Value())
	if err != nil {
		return w, fmt.Errorf("conversion-completed-at: %w", err)
	}
	paidAt, err := makerBoxParseDateTime(s.inputs[mbfPaidAt].Value())
	if err != nil {
		return w, fmt.Errorf("paid-at: %w", err)
	}
	var binPtr *string
	if binRaw != "" {
		binPtr = &binRaw
	}
	w = omsapi.MakerBoxWrite{
		BinID:                 binPtr,
		AssignedUsername:      username,
		FirstName:             strings.TrimSpace(s.inputs[mbfFirstName].Value()),
		LastName:              strings.TrimSpace(s.inputs[mbfLastName].Value()),
		Email:                 email,
		Status:                makerBoxStatusOptions[s.statusIdx].value,
		IdentitySource:        makerBoxIdentityOptions[s.identityIdx].value,
		AssignedAt:            assignedAt,
		ExpiresAt:             expiresAt,
		ConversionCompletedAt: conversionAt,
		PaidAt:                paidAt,
		Notes:                 strings.TrimSpace(s.inputs[mbfNotes].Value()),
	}
	return w, nil
}

func (s *MakerBoxFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSFacilities, NewMakerBoxesScreen(s.deps))
}

func (s *MakerBoxFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")

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
	}
	return b.String()
}

func (s *MakerBoxFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := makerBoxFieldLabel[id]
	var value string
	switch id {
	case mbfStatus:
		value = "‹ " + makerBoxStatusOptions[s.statusIdx].label + " ›"
	case mbfIdentitySource:
		value = "‹ " + makerBoxIdentityOptions[s.identityIdx].label + " ›"
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *MakerBoxFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case mbfStatus, mbfIdentitySource:
			kindHelp = "space/←→ change"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *MakerBoxFormScreen) visibleRows() int {
	const chrome = 6
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}
