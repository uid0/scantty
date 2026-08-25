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

// projectStorageFieldHint carries what the placeholders and the "*" used to
// say. A placeholder long enough to fill the input area leaves no underscores,
// so an empty green-screen row stops reading as empty; and a columnar form
// marks what is REQUIRED rather than tagging everything else "(optional)".
// A hint is CLIPPED, not wrapped, when the row runs past the pane, so the
// widths below are what leaves room for these (TestJDESweepD_RowsFitTheBody).
var projectStorageFieldHint = map[int]string{
	psfUsername:        "required",
	psfEmail:           "member@example.com",
	psfProjectTitle:    "blank = personal storage",
	psfStorageLocation: "non-rack, e.g. by the CNC",
}

func projectStorageFieldWidth(id int) int {
	switch id {
	case psfEmail:
		return 30
	case psfSlot:
		return 0
	}
	return 24
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

// projectStoragePlaceholderFor is empty for every field now — see
// projectStorageFieldHint.
func projectStoragePlaceholderFor(id int) string { return "" }

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
	pickSearch textinput.Model

	jdeScreen
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
		s.setSize(m)
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
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && projectStorageFieldKind(id) == akText {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ProjectStorageFormScreen) updateForm(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.cancelCmd()
	case "tab", "down":
		s.moveCursor(+1)
		return s, textinput.Blink
	case "shift+tab", "up":
		s.moveCursor(-1)
		return s, textinput.Blink
	case "pgdown":
		s.pageCursor(+1)
		return s, textinput.Blink
	case "pgup":
		s.pageCursor(-1)
		return s, textinput.Blink
	case "enter":
		if s.saving {
			return s, nil
		}
		return s.submit()
	case "ctrl+e":
		// EDIT opens whatever the highlighted row IS. Only the slot row opens
		// anything, which is why the bar drops the key on the others.
		if id, ok := s.currentFieldID(); ok && projectStorageFieldKind(id) == akPicker {
			s.openSlotPick()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	if projectStorageFieldKind(id) == akPicker {
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	}
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *ProjectStorageFormScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Slot picker
// ---------------------------------------------------------------------------

func (s *ProjectStorageFormScreen) openSlotPick() {
	s.phase = psFormPhaseSlotPick
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open — which is also what makes the typed-code
	// escape hatch below a plain consequence of typing rather than a mode.
	s.pickSearch.Focus()
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
	// Ctrl-R retries a FAILED load in place, rather than leaving the operator
	// with an empty list they can't tell from a full rack. It is a control key,
	// not a letter, because the always-live filter owns every letter — and the
	// bar only offers it when there is something to retry.
	if m.String() == "ctrl+r" {
		if s.slotsErr != "" {
			s.slotsErr = ""
			return s, s.loadSlots()
		}
		return s, nil
	}
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		// esc means "done looking" and KEEPS the current claim.
		s.closeSlotPick()
	case jdePickCommit:
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickRows) {
			row := s.pickRows[s.pickCursor]
			if row.clear {
				s.slotCode, s.slotLabel = "", ""
			} else {
				s.slotCode, s.slotLabel = row.key, row.label
			}
		}
		s.closeSlotPick()
	case jdePickMove:
		s.pickCursor = jdeClampPick(s.pickCursor+delta, len(s.pickRows))
	case jdePickPage:
		header, body := s.pickView()
		s.pickCursor = jdeClampPick(s.pickCursor+delta*s.windowRows(body, s.pickCursor, len(header)), len(s.pickRows))
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember. Typing a code the free list does
		// not carry is what offers the synthetic "use <CODE>" row.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applySlotFilter()
		return s, cmd
	}
	return s, nil
}

func (s *ProjectStorageFormScreen) closeSlotPick() {
	s.phase = psFormPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
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
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusLine(), s.formBar(body))
}

func (s *ProjectStorageFormScreen) statusLine() string {
	return storageSlotStatusLine(s.jdeScreen, s.saving, "Saving…", s.errMsg, "",
		"intake self-issues a 30-day stint")
}

// formFields describes the sheet as columnar rows: one FK row Ctrl-E opens, and
// the rest typed into. The member and the ad-hoc location are denormalized
// strings on the model, not foreign keys, so they stay free text.
func (s *ProjectStorageFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   projectStorageFieldLabel[id],
			Width:   projectStorageFieldWidth(id),
			Hint:    projectStorageFieldHint[id],
			Focused: i == s.cursor,
		}
		if projectStorageFieldKind(id) == akPicker {
			value, dim := s.slotRowValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		} else {
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

func (s *ProjectStorageFormScreen) formLines() *jdeLines {
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Project storage intake"))
	l.Add("")
	l.AddFields(s.formFields(), storageLabelWidth, s.bodyWidth(), 0)
	return l
}

// formBar names the keys that apply where the cursor is standing — and only
// those, so the bar never teaches a key that does nothing here.
func (s *ProjectStorageFormScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok && projectStorageFieldKind(id) == akPicker {
		items = append(items, actionBarItem{"Ctrl-E", "Pick"})
	}
	if s.bodyScrolls(body, 0) {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// slotRowValue shows the claim, or why there is nothing to pick from, and
// whether that is an empty state rather than a value. The four states are kept
// distinct: a claim, a list still loading, a list that FAILED, and a rack with
// no free slots — silence on a failure would read as "the racking is full".
//
// PLAIN text plus a flag, not pre-styled muted text: a focused row has to be
// able to reverse-video the whole field.
func (s *ProjectStorageFormScreen) slotRowValue() (string, bool) {
	if s.slotCode != "" {
		return s.slotCode, false
	}
	switch {
	case s.slotsErr != "":
		return "— none · slot list unavailable", true
	case !s.slotsReady:
		return "— none · loading free slots…", true
	case len(s.slots) == 0:
		return "— none · no free slots in the racking", true
	default:
		return fmt.Sprintf("— none · %d free to choose from", len(s.slots)), true
	}
}

// pickView builds the open picker's pinned header and its option list. The note
// covers the two things about this list that are not self-evident: what the
// clear row does, and — when the load failed — that a code can still be typed
// straight in (the backend re-checks it either way).
func (s *ProjectStorageFormScreen) pickView() (jdeHeader, *jdeLines) {
	note := "Row 1 is no slot — ad-hoc storage. A claimed slot wins over the location below."
	if s.slotsErr != "" {
		note = "Slot list unavailable — type the code off the card, or Ctrl-R to retry."
	}
	return jdePickList{
		Title:  "Claim a rack slot",
		For:    strings.TrimSpace(s.inputs[psfUsername].Value()),
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickRows),
		Label:  func(i int) string { return s.pickRows[i].label },
		Dim:    func(i int) bool { return s.pickRows[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matching free slots)",
	}.render(s.bodyWidth())
}

func (s *ProjectStorageFormScreen) viewSlotPick() string {
	header, body := s.pickView()
	paging := false
	if s.bodyScrolls(body, len(header)) {
		paging = true
	}
	items := jdePickBar("Claim", paging)
	if s.slotsErr != "" {
		items = append(items, actionBarItem{"Ctrl-R", "Retry list"})
	}
	return s.frameWithHeader(header, body, s.pickCursor, s.statusRow(false, "", ""), items)
}
