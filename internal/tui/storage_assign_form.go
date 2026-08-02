// Hand ONE slot to a committee, the logistics crew, or a class.
//
// The staff half of the racking. A member claims a slot at the kiosk and a
// 30-day clock starts; these slots are handed out by staff and stay handed out
// — no expiry, no violation notice, no purgatory. So this form has no dates on
// it: the whole lifecycle is assign here, release from the grid (or the slot).
//
// The field set is AssignSlotSerializer's, exactly: storage_type, owning_group,
// occupant_label, notes. The slot itself is fixed context (the cell the warden
// was standing on) rather than a field, because there is no update action —
// re-pointing an assignment at another slot means releasing and assigning again.
//
// Who holds it is recorded one of two ways, because the three types name their
// occupants differently: a COMMITTEE is a SIG (a Django group), while logistics
// and class carry free text ("Winter welding cohort", "Ana's CNC class") —
// neither is a durable object in the system. The form follows that: the SIG
// picker only appears for a committee, and the backend refuses a committee that
// names neither a group nor a label.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

const (
	safType = iota
	safGroup
	safLabel
	safNotes
	safFieldMax
)

var storageAssignFieldLabel = map[int]string{
	safType:  "Storage type",
	safGroup: "Committee (SIG)",
	safLabel: "Occupant",
	safNotes: "Notes",
}

// storageAssignTypeOptions is the model's STORAGE_TYPE_CHOICES with its grid
// letter shown, so the operator picking "Class" can see it will paint E — the
// letter is what they will be reading off the rack afterwards. A bounded choice
// set is a cycling select here, not a picker sub-phase (the repo's rule: the
// sub-phase list is for unbounded, server-loaded options).
var storageAssignTypeOptions = []selectOption{
	{omsapi.StorageAssignmentTypeCommittee, "Committee (C)"},
	{omsapi.StorageAssignmentTypeLogistics, "Logistics (L)"},
	{omsapi.StorageAssignmentTypeClass, "Class (E)"},
}

type storageAssignPhase int

const (
	assignPhaseForm storageAssignPhase = iota
	assignPhasePick
)

type StorageAssignFormScreen struct {
	deps Deps
	code string

	// back is where esc and a successful save go. The caller supplies it so the
	// warden returns to the cell they were standing on rather than to the top
	// of the racking.
	back func(Deps) Screen

	typeIdx       int
	owningGroupID *int
	owningGroupNm string

	inputs []textinput.Model

	sigs      []omsapi.SIG
	sigsErr   string
	sigsReady bool

	fields []int
	cursor int

	phase      storageAssignPhase
	pickCursor int
	pickRows   []assetPickRow

	saving bool
	errMsg string
}

type storageAssignSIGsLoadedMsg struct {
	sigs []omsapi.SIG
	err  error
}

type storageAssignedMsg struct {
	assignment *omsapi.StorageAssignment
	err        error
}

// NewStorageAssignFormScreen opens the assign form for one slot code. back may
// be nil, in which case the form returns to the slot's own detail screen.
func NewStorageAssignFormScreen(deps Deps, code string, back func(Deps) Screen) *StorageAssignFormScreen {
	s := &StorageAssignFormScreen{
		deps: deps,
		code: strings.ToUpper(strings.TrimSpace(code)),
		back: back,
	}
	s.inputs = make([]textinput.Model, safFieldMax)
	for _, id := range []int{safLabel, safNotes} {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = storageAssignCharLimit(id)
		ti.Placeholder = storageAssignPlaceholder(id)
		s.inputs[id] = ti
	}
	s.rebuildFields()
	return s
}

func storageAssignCharLimit(id int) int {
	if id == safLabel {
		return 120 // model CharField(max_length=120)
	}
	return 1000
}

func storageAssignPlaceholder(id int) string {
	switch id {
	case safLabel:
		return "a crew, a cohort, a class — free text"
	case safNotes:
		return "optional"
	}
	return ""
}

func (s *StorageAssignFormScreen) Title() string { return "Assign slot " + s.code }

// WantsRawInput claims every key so field letters, tab, enter and esc reach the
// form instead of the root's globals.
func (s *StorageAssignFormScreen) WantsRawInput() bool { return true }

func (s *StorageAssignFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *StorageAssignFormScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	load := func() tea.Msg {
		// The SIG list is a separate permission surface, and only a COMMITTEE
		// assignment needs it — so a failure is reported beside the field
		// rather than blocking a logistics assignment that never wanted it.
		page, err := deps.OMS.ListSIGs(ctx, nil)
		if err != nil {
			return storageAssignSIGsLoadedMsg{err: err}
		}
		return storageAssignSIGsLoadedMsg{sigs: page.Results}
	}
	return tea.Batch(load, textinput.Blink)
}

func (s *StorageAssignFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case storageAssignSIGsLoadedMsg:
		s.sigs = m.sigs
		s.sigsReady = m.err == nil
		if m.err != nil {
			s.sigsErr = slotCardErrorText(m.err)
		}
		return s, nil

	case storageAssignedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = slotCardErrorText(m.err)
			return s, Status("assign failed: "+s.errMsg, StatusError)
		}
		return s, tea.Batch(
			Status(storageAssignedText(m.assignment, s.code), StatusOK),
			SwitchTo(WSFacilities, s.backScreen()),
		)

	case tea.KeyMsg:
		if s.phase == assignPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == assignPhaseForm {
		if id, ok := s.currentFieldID(); ok && storageAssignIsTextKind(id) {
			var cmd tea.Cmd
			s.inputs[id], cmd = s.inputs[id].Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

func storageAssignedText(a *omsapi.StorageAssignment, fallbackCode string) string {
	if a == nil {
		return "slot " + fallbackCode + " assigned"
	}
	who := strings.TrimSpace(a.OccupantDisplay)
	if who == "" {
		who = a.StorageTypeDisplay
	}
	return fmt.Sprintf("%s assigned to %s — the grid paints %s", a.SlotCode, who,
		firstNonEmpty(a.TypeLetter, "it"))
}

func (s *StorageAssignFormScreen) backScreen() Screen {
	if s.back != nil {
		return s.back(s.deps)
	}
	return NewStorageSlotDetailScreen(s.deps, s.code)
}

// ---------------------------------------------------------------------------
// Fields
// ---------------------------------------------------------------------------

func storageAssignFieldKind(id int) assetFieldKind {
	switch id {
	case safType:
		return akSelect
	case safGroup:
		return akPicker
	}
	return akText
}

func storageAssignIsTextKind(id int) bool { return storageAssignFieldKind(id) == akText }

func (s *StorageAssignFormScreen) storageType() string {
	if s.typeIdx >= 0 && s.typeIdx < len(storageAssignTypeOptions) {
		return storageAssignTypeOptions[s.typeIdx].value
	}
	return omsapi.StorageAssignmentTypeCommittee
}

func (s *StorageAssignFormScreen) isCommittee() bool {
	return s.storageType() == omsapi.StorageAssignmentTypeCommittee
}

// rebuildFields shows the SIG picker only for a committee: logistics and class
// have no group to point at, and offering one would invite an assignment whose
// occupant is recorded in the wrong half of the model. The cursor is kept on
// the same FIELD across the change (the breaker-form idiom) so cycling the type
// doesn't teleport it.
func (s *StorageAssignFormScreen) rebuildFields() {
	var current int = -1
	if s.cursor >= 0 && s.cursor < len(s.fields) {
		current = s.fields[s.cursor]
	}
	s.fields = []int{safType}
	if s.isCommittee() {
		s.fields = append(s.fields, safGroup)
	}
	s.fields = append(s.fields, safLabel, safNotes)

	if idx := indexOfField(s.fields, current); idx >= 0 {
		s.cursor = idx
	} else if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	s.syncFocus()
}

func (s *StorageAssignFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *StorageAssignFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *StorageAssignFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if storageAssignIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && storageAssignIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *StorageAssignFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSFacilities, s.backScreen())
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
	switch storageAssignFieldKind(id) {
	case akSelect:
		switch m.String() {
		case " ", "right":
			s.typeIdx = (s.typeIdx + 1) % len(storageAssignTypeOptions)
			s.rebuildFields()
		case "left":
			s.typeIdx = (s.typeIdx - 1 + len(storageAssignTypeOptions)) % len(storageAssignTypeOptions)
			s.rebuildFields()
		}
		return s, nil
	case akPicker:
		if m.String() == " " {
			s.openPicker()
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

// ---------------------------------------------------------------------------
// Committee picker
// ---------------------------------------------------------------------------

func (s *StorageAssignFormScreen) openPicker() {
	s.phase = assignPhasePick
	s.pickRows = storageAssignGroupRows(s.sigs)
	s.pickCursor = 0
	if s.owningGroupID != nil {
		want := strconv.Itoa(*s.owningGroupID)
		for i, r := range s.pickRows {
			if !r.clear && r.key == want {
				s.pickCursor = i
				break
			}
		}
	}
}

// storageAssignGroupRows lists a clear row then every SIG. Unlike the slot
// form's owner picker there is nothing to graft: this is a CREATE, so the only
// value the picker can hold is one it just offered.
func storageAssignGroupRows(sigs []omsapi.SIG) []assetPickRow {
	rows := []assetPickRow{{clear: true, label: "— none (describe it below instead) —"}}
	for _, g := range sigs {
		rows = append(rows, assetPickRow{key: strconv.Itoa(g.ID), label: g.Name})
	}
	return rows
}

func (s *StorageAssignFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = assignPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.pickCursor < len(s.pickRows)-1 {
			s.pickCursor++
		}
	case "k", "up":
		if s.pickCursor > 0 {
			s.pickCursor--
		}
	case "enter":
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickRows) {
			picked := pickInt(s.pickRows[s.pickCursor])
			s.owningGroupID = picked
			s.owningGroupNm = storageAssignGroupName(s.sigs, picked)
		}
		s.phase = assignPhaseForm
		s.syncFocus()
	}
	return s, nil
}

func storageAssignGroupName(sigs []omsapi.SIG, picked *int) string {
	if picked == nil {
		return ""
	}
	for _, g := range sigs {
		if g.ID == *picked {
			return g.Name
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

// buildPayload validates in the backend's own terms so a miss is a message
// rather than a 400: a committee assignment that names neither a SIG nor a
// label is just a blocked slot, and the serializer refuses it.
func (s *StorageAssignFormScreen) buildPayload() (omsapi.StorageAssignmentWrite, error) {
	label := strings.TrimSpace(s.inputs[safLabel].Value())
	w := omsapi.StorageAssignmentWrite{
		SlotCode:      s.code,
		StorageType:   s.storageType(),
		OccupantLabel: label,
		Notes:         strings.TrimSpace(s.inputs[safNotes].Value()),
	}
	if s.isCommittee() {
		w.OwningGroup = s.owningGroupID
		if s.owningGroupID == nil && label == "" {
			return w, errors.New("a committee assignment must name the committee — pick a SIG, or describe it in Occupant")
		}
	}
	return w, nil
}

func (s *StorageAssignFormScreen) submit() (Screen, tea.Cmd) {
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
		a, e := deps.OMS.AssignStorageSlot(ctx, body)
		return storageAssignedMsg{assignment: a, err: e}
	}
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *StorageAssignFormScreen) View() string {
	if s.phase == assignPhasePick {
		return s.viewPicker()
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(elecFieldHelp(storageAssignFieldKind, s.currentFieldID)) + "\n\n")
	b.WriteString(StyleMuted.Render("slot: ") + StyleTitle.Render(s.code) +
		StyleMuted.Render("  (staff storage — no expiry, no purgatory; it stays theirs until released)") + "\n\n")

	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	switch {
	case s.saving:
		b.WriteString(StyleMuted.Render("Assigning…"))
	case s.errMsg != "":
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	case s.isCommittee() && s.sigsErr != "":
		b.WriteString(StyleMuted.Render("SIG list unavailable — " + s.sigsErr + " · describe the committee in Occupant instead"))
	default:
		b.WriteString(StyleMuted.Render("the slot must be free and in service · assigning is staff / Storage Admin only"))
	}
	return b.String()
}

func (s *StorageAssignFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := storageAssignFieldLabel[id]
	var value string
	switch storageAssignFieldKind(id) {
	case akSelect:
		value = elecSelectLabel(storageAssignTypeOptions, s.typeIdx)
	case akPicker:
		value = s.groupLabel()
	default:
		value = s.inputs[id].View()
	}
	line := caret + StyleTitle.Render(label+": ") + value
	if id == safLabel && s.isCommittee() {
		line += StyleMuted.Render("  (only needed when the SIG isn't in the list)")
	}
	return line
}

func (s *StorageAssignFormScreen) groupLabel() string {
	if s.owningGroupID == nil {
		return StyleMuted.Render("— none —")
	}
	if s.owningGroupNm != "" {
		return s.owningGroupNm
	}
	return fmt.Sprintf("SIG #%d", *s.owningGroupID)
}

func (s *StorageAssignFormScreen) viewPicker() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Which committee holds "+s.code+"?") + "\n")
	switch {
	case s.sigsErr != "":
		b.WriteString(StyleStatusWarn.Render("SIG list unavailable — "+s.sigsErr) + "\n\n")
	case !s.sigsReady:
		b.WriteString(StyleMuted.Render("Loading SIGs…") + "\n\n")
	}
	if len(s.pickRows) == 0 {
		b.WriteString(StyleMuted.Render("No SIGs to choose from.") + "\n")
	} else {
		b.WriteString(renderWindowedList(len(s.pickRows), s.pickCursor, func(i int) string {
			return s.pickRows[i].label
		}))
	}
	b.WriteString("\n" + StyleMuted.Render("j/k move · enter pick · esc keep current"))
	return b.String()
}
