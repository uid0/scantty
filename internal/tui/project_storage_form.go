// ProjectStorageFormScreen — intake (create) form for a project-storage stint.
//
// This is the TUI counterpart to the web ProjectStorageKioskPage self-issue
// intake (frontend/src/pages/ProjectStorageKioskPage.tsx), which is the ONLY
// create path OMS exposes: the stint viewset is a ReadOnlyModelViewSet, so there
// is no POST /stints/ and no PATCH/PUT — every write goes through fixed custom
// actions, and "start" is the create one ([[scantty-parity-program]] Tier-2).
//
// The member is a denormalized `username` string and the ad-hoc location a
// `storage_location_name` string — NEITHER is a foreign key (the model
// deliberately avoids FK'ing inventory.Location) — so those stay free text with
// no pickers. The one real FK is the racking SLOT (op-hfw5): a stint may CLAIM
// a StorageSlot, and when it does the slot is authoritative over the free-text
// name. That row is a picker over the FREE, in-service slots, with a typed-code
// escape hatch for an operator reading the code off the card at the rack.
//
// The form mirrors the exact StartStintSerializer field set and nothing more:
// username (required) plus the optional first/last name, email, project title,
// slot and storage location.
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
	"net/url"
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
	psfSlot
	psfStorageLocation
	psfFieldMax
)

var projectStorageFieldLabel = map[int]string{
	psfUsername:        "Username",
	psfFirstName:       "First name",
	psfLastName:        "Last name",
	psfEmail:           "Email",
	psfProjectTitle:    "Project title",
	psfSlot:            "Rack slot",
	psfStorageLocation: "Storage location",
}

// psfSlot is the only non-text row: it holds a slot CODE chosen from the free
// slots (or typed), not a free-text string, so it gets the picker treatment.
func projectStorageFieldKind(id int) assetFieldKind {
	if id == psfSlot {
		return akPicker
	}
	return akText
}

type projectStorageFormPhase int

const (
	psFormPhaseForm projectStorageFormPhase = iota
	psFormPhaseSlotPick
)

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
		return "non-rack storage, e.g. floor by the CNC (optional)"
	default:
		return ""
	}
}

type ProjectStorageFormScreen struct {
	deps Deps

	saving bool
	errMsg string

	// Text field storage, indexed by field id. The picker row keeps an unused
	// slot so every psf* ordinal still indexes this slice.
	inputs []textinput.Model

	// Static visible-field navigation.
	fields []int
	cursor int

	// Claimed slot. slotCode is what goes on the wire; slotLabel is what the
	// row shows (the code plus, when it came from the list, how to reach it).
	slotCode  string
	slotLabel string

	// Free-slot options. slotsErr distinguishes a FAILED load (say so, and let
	// the operator retry or type a code) from an empty rack — a silent empty
	// picker would read as "the racking is full", which may be false.
	slots      []omsapi.StorageSlot
	slotsErr   string
	slotsReady bool

	phase      projectStorageFormPhase
	pickCursor int
	pickRows   []assetPickRow
	pickTyping bool
	pickSearch textinput.Model

	terminalHeight int
}

type projectStorageFormSavedMsg struct {
	stint *omsapi.ProjectStorageStint
	err   error
}

type projectStorageSlotsLoadedMsg struct {
	slots []omsapi.StorageSlot
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
	search := textinput.New()
	search.Prompt = ""
	search.CharLimit = 16
	search.Placeholder = "code or rack, e.g. 1A1"
	s.pickSearch = search
	s.fields = []int{psfUsername, psfFirstName, psfLastName, psfEmail, psfProjectTitle, psfSlot, psfStorageLocation}
	s.syncFocus()
	return s
}

func (s *ProjectStorageFormScreen) Title() string { return "New project-storage stint" }

// WantsRawInput claims every keypress so field letters, tab, enter, and esc all
// reach the form instead of the root's global hotkeys.
func (s *ProjectStorageFormScreen) WantsRawInput() bool { return true }

// Init loads the slots that can actually be claimed — free AND in service —
// in the background. The load never gates the form: a stint still starts with
// no slot at all (ad-hoc storage), so a slow or forbidden slot list must not
// stop an intake.
func (s *ProjectStorageFormScreen) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, s.loadSlots())
}

func (s *ProjectStorageFormScreen) loadSlots() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		q := url.Values{"occupied": {"false"}, "is_active": {"true"}}
		slots, err := deps.OMS.ListAllStorageSlots(ctx, q)
		return projectStorageSlotsLoadedMsg{slots: slots, err: err}
	}
}

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

	case projectStorageSlotsLoadedMsg:
		s.slotsReady = m.err == nil
		if m.err != nil {
			s.slotsErr = slotCardErrorText(m.err)
		} else {
			s.slotsErr = ""
			s.slots = m.slots
		}
		return s, nil

	case tea.KeyMsg:
		if s.phase == psFormPhaseSlotPick {
			return s.updateSlotPick(m)
		}
		return s.updateForm(m)
	}

	// Non-key messages (cursor blink) go to the focused input.
	if s.phase == psFormPhaseSlotPick {
		if s.pickTyping {
			var cmd tea.Cmd
			s.pickSearch, cmd = s.pickSearch.Update(msg)
			return s, cmd
		}
		return s, nil
	}
	if id, ok := s.currentFieldID(); ok && projectStorageFieldKind(id) == akText {
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
	if projectStorageFieldKind(id) == akPicker {
		// space opens the picker; the row holds no textinput to type into, so
		// every other key is a no-op rather than silent input loss.
		if m.String() == " " {
			s.openSlotPick()
		}
		return s, nil
	}
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

// ---------------------------------------------------------------------------
// Slot picker
// ---------------------------------------------------------------------------

func (s *ProjectStorageFormScreen) openSlotPick() {
	s.phase = psFormPhaseSlotPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applySlotFilter()
	// Park the cursor on the current claim so re-opening and pressing enter is
	// a no-op confirm rather than a silent reset to "no slot".
	s.pickCursor = 0
	if s.slotCode != "" {
		for i, row := range s.pickRows {
			if !row.clear && row.key == s.slotCode {
				s.pickCursor = i
				break
			}
		}
	}
}

// applySlotFilter rebuilds the option list for the current search text. Row 0
// is always the explicit "no slot" row so detaching a mis-pick is first-class.
//
// When the typed text is a VALID CODE that no listed option matches, a
// synthetic "use <CODE>" row is offered. That is the escape hatch for the two
// real cases the list can't cover: the slot list failed to load (it is a
// staff / Storage Admin surface, while the claim itself is not), or the
// operator is standing at a slot whose card they can read but which the free
// filter excluded. The backend re-checks the code either way — an unknown one
// 400s, an occupied one 409s, a retired one 400s — so the hatch can only ever
// hand over a claim the server would have accepted from any other caller.
func (s *ProjectStorageFormScreen) applySlotFilter() {
	q := strings.ToUpper(strings.TrimSpace(s.pickSearch.Value()))
	rows := []assetPickRow{{clear: true, label: "— no slot (ad-hoc storage) —"}}
	matched := false
	for _, slot := range s.slots {
		if q != "" && !strings.Contains(strings.ToUpper(slot.Code), q) {
			continue
		}
		if slot.Code == q {
			matched = true
		}
		rows = append(rows, assetPickRow{key: slot.Code, label: projectStorageSlotOption(slot)})
	}
	if !matched {
		if code, err := omsapi.NormalizeStorageSlotCode(q); err == nil {
			rows = append(rows, assetPickRow{key: code, label: "use " + code + " (typed)"})
		}
	}
	s.pickRows = rows
	if s.pickCursor >= len(s.pickRows) {
		s.pickCursor = 0
	}
}

func projectStorageSlotOption(slot omsapi.StorageSlot) string {
	label := slot.Code
	if slot.RequiresPalletJack {
		label += " · needs a pallet jack"
	}
	if slot.OwningGroupName != "" {
		label += " · " + slot.OwningGroupName
	}
	return label
}

func (s *ProjectStorageFormScreen) updateSlotPick(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applySlotFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applySlotFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		// esc means "done looking" and KEEPS the current claim.
		s.phase = psFormPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.pickCursor < len(s.pickRows)-1 {
			s.pickCursor++
		}
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
	case "/":
		s.pickTyping = true
		s.pickSearch.Focus()
		return s, textinput.Blink
	case "g":
		if s.slotsErr != "" {
			// A failed load is retried in place rather than leaving the
			// operator with an empty list they can't tell from a full rack.
			s.slotsErr = ""
			return s, s.loadSlots()
		}
	case "enter":
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickRows) {
			row := s.pickRows[s.pickCursor]
			if row.clear {
				s.slotCode, s.slotLabel = "", ""
			} else {
				s.slotCode, s.slotLabel = row.key, row.label
			}
		}
		s.phase = psFormPhaseForm
		s.pickTyping = false
		s.pickSearch.SetValue("")
		s.pickSearch.Blur()
		s.syncFocus()
	}
	return s, nil
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
	if id, ok := s.currentFieldID(); ok && projectStorageFieldKind(id) == akText {
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

	// The slot is optional, but a non-empty one has to be a real code — sending
	// a malformed one would spend a round trip on a guaranteed 400.
	slotCode := ""
	if raw := strings.TrimSpace(s.slotCode); raw != "" {
		normalized, err := omsapi.NormalizeStorageSlotCode(raw)
		if err != nil {
			return w, errors.New("rack slot must be a code like 1A1")
		}
		slotCode = normalized
	}

	w = omsapi.ProjectStorageStintStart{
		Username:            username,
		FirstName:           strings.TrimSpace(s.inputs[psfFirstName].Value()),
		LastName:            strings.TrimSpace(s.inputs[psfLastName].Value()),
		Email:               email,
		ProjectTitle:        strings.TrimSpace(s.inputs[psfProjectTitle].Value()),
		StorageLocationName: strings.TrimSpace(s.inputs[psfStorageLocation].Value()),
		SlotCode:            slotCode,
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
	if s.phase == psFormPhaseSlotPick {
		return s.viewSlotPick()
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
	if projectStorageFieldKind(id) == akPicker {
		return caret + StyleTitle.Render(label+": ") + s.slotRowValue()
	}
	return caret + StyleTitle.Render(label+": ") + s.inputs[id].View()
}

// slotRowValue shows the claim, or why there is nothing to pick from. The
// three states are kept distinct: nothing claimed, a claim, and a slot list
// that FAILED (as opposed to a rack with no free slots) — silence on a failure
// would read as "the racking is full".
func (s *ProjectStorageFormScreen) slotRowValue() string {
	if s.slotCode != "" {
		return s.slotCode + StyleMuted.Render("  (space to change)")
	}
	switch {
	case s.slotsErr != "":
		return StyleMuted.Render("— none · slot list unavailable, space to type a code")
	case !s.slotsReady:
		return StyleMuted.Render("— none · loading free slots…")
	case len(s.slots) == 0:
		return StyleMuted.Render("— none · no free slots in the racking")
	default:
		return StyleMuted.Render(fmt.Sprintf("— none · space to pick from %d free", len(s.slots)))
	}
}

func (s *ProjectStorageFormScreen) helpText() string {
	if id, ok := s.currentFieldID(); ok && projectStorageFieldKind(id) == akPicker {
		return "space opens the slot list · tab/↑↓ move · enter save · esc cancel"
	}
	return "type to edit · tab/↑↓ move · enter save · esc cancel"
}

func (s *ProjectStorageFormScreen) viewSlotPick() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Claim a rack slot") + "\n")
	b.WriteString(StyleMuted.Render("Free, in-service slots. The slot wins over the free-text location.") + "\n")
	if s.slotsErr != "" {
		b.WriteString(StyleStatusWarn.Render("slot list unavailable — "+s.slotsErr) + "\n")
		b.WriteString(StyleMuted.Render("press g to retry, or / and type the code printed on the slot's card") + "\n")
	}
	b.WriteString("\n")
	if s.pickTyping {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n")
	} else if v := strings.TrimSpace(s.pickSearch.Value()); v != "" {
		b.WriteString(StyleMuted.Render("filter: "+v) + "\n")
	}
	if len(s.pickRows) == 0 {
		b.WriteString(StyleMuted.Render("No slots to choose from.") + "\n")
	} else {
		b.WriteString(renderWindowedList(len(s.pickRows), s.pickCursor, func(i int) string {
			return s.pickRows[i].label
		}))
	}
	b.WriteString("\n")
	if s.pickTyping {
		b.WriteString(StyleMuted.Render("type to filter · enter apply · esc stop typing"))
	} else {
		b.WriteString(StyleMuted.Render("j/k move · / filter or type a code · enter pick · esc keep current"))
	}
	return b.String()
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
