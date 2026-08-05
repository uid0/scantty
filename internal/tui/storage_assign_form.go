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
	safType: "Storage type",
	// "(SIG)" moved to the hint — see storageSlotFieldLabel.
	safGroup: "Committee",
	safLabel: "Occupant",
	safNotes: "Notes",
}

// storageAssignFieldWidth sizes the input areas that are not the default.
// Occupant is narrower than Notes because it carries a hint and the two cannot
// both have the room (TestJDESweepD_RowsFitTheBody).
func storageAssignFieldWidth(id int) int {
	switch id {
	case safLabel:
		return 24
	case safNotes:
		return 40
	}
	return 0
}

// fieldHint is computed rather than looked up, because what Occupant is FOR
// depends on the storage type: a committee records who holds the slot as a SIG
// and only needs the free text when the SIG is not in the list, while logistics
// and class have no group to point at and the text is the only record there is.
func (s *StorageAssignFormScreen) fieldHint(id int) string {
	switch id {
	case safGroup:
		return "SIG"
	case safLabel:
		if s.isCommittee() {
			return "when the SIG isn't listed"
		}
		return "a crew, a cohort, a class"
	}
	return ""
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

	jdeScreen

	fields []int
	cursor int

	phase      storageAssignPhase
	pickCursor int
	pickRows   []assetPickRow
	pickSearch textinput.Model

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
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.CharLimit = 60
	s.rebuildFields()
	return s
}

func storageAssignCharLimit(id int) int {
	if id == safLabel {
		return 120 // model CharField(max_length=120)
	}
	return 1000
}

// storageAssignPlaceholder is empty for every field now — what it said moved
// into fieldHint, because a placeholder long enough to fill the input area
// leaves no underscores and an empty green-screen row stops reading as empty.
func storageAssignPlaceholder(id int) string { return "" }

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
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

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

	if s.phase == assignPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && storageAssignIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
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
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSFacilities, s.backScreen())
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
		// EDIT opens whatever the highlighted row IS — only the committee row
		// opens anything, and only a committee assignment has one.
		if id, ok := s.currentFieldID(); ok && storageAssignFieldKind(id) == akPicker {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
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
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *StorageAssignFormScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Committee picker
// ---------------------------------------------------------------------------

func (s *StorageAssignFormScreen) openPicker() {
	s.phase = assignPhasePick
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()
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

// applyPickFilter narrows the option list to what has been typed, keeping the
// CLEAR row whatever the query is — see StorageSlotFormScreen.applyPickFilter.
func (s *StorageAssignFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	rows := make([]assetPickRow, 0, len(s.sigs)+1)
	for _, r := range storageAssignGroupRows(s.sigs) {
		if r.clear || q == "" || strings.Contains(strings.ToLower(r.label), q) {
			rows = append(rows, r)
		}
	}
	s.pickRows = rows
	if s.pickCursor >= len(s.pickRows) {
		s.pickCursor = 0
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
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickRows) {
			picked := pickInt(s.pickRows[s.pickCursor])
			s.owningGroupID = picked
			s.owningGroupNm = storageAssignGroupName(s.sigs, picked)
		}
		s.closePicker()
	case jdePickMove:
		s.pickCursor = jdeClampPick(s.pickCursor+delta, len(s.pickRows))
	case jdePickPage:
		header, body := s.pickView()
		s.pickCursor = jdeClampPick(s.pickCursor+delta*s.windowRows(body, s.pickCursor, len(header)), len(s.pickRows))
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}
	return s, nil
}

func (s *StorageAssignFormScreen) closePicker() {
	s.phase = assignPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
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
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusLine(), s.formBar(body))
}

// statusLine is the row above the bar. A failed SIG list only matters to a
// COMMITTEE assignment, and even then it is a warning with a way round it (the
// free-text Occupant), not an error.
func (s *StorageAssignFormScreen) statusLine() string {
	warn := ""
	if s.isCommittee() && s.sigsErr != "" {
		warn = "SIG list unavailable — describe the committee in Occupant instead"
	}
	return storageSlotStatusLine(s.saving, "Assigning…", s.errMsg, warn,
		"the slot must be free and in service · staff / Storage Admin only")
}

// formFields describes the sheet as columnar rows: one bounded set, one FK row
// Ctrl-E opens (committee assignments only), and two typed into.
func (s *StorageAssignFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   storageAssignFieldLabel[id],
			Width:   storageAssignFieldWidth(id),
			Hint:    s.fieldHint(id),
			Focused: i == s.cursor,
		}
		switch storageAssignFieldKind(id) {
		case akSelect:
			// The BARE label between the brackets — the renderer owns them.
			f.Kind, f.Value = jdeChoice, storageAssignTypeOptions[s.typeIdx].label
		case akPicker:
			value, dim := s.groupValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		default:
			f.Kind, f.Value = jdeText, jdeInputValue(s.inputs[id], f.Focused)
		}
		out[i] = f
	}
	return out
}

func (s *StorageAssignFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Assign slot"))
	l.Add("")
	// The slot is fixed context, not a field — there is no update action, so
	// re-pointing an assignment means releasing and assigning again. It is a
	// dimmed, non-navigable row of this sheet (l.Add, not AddRow) rather than a
	// header the operator would not tie to the form.
	l.Add(renderJDEField(jdeField{
		Label: "Slot",
		Kind:  jdeValue,
		Value: s.code,
		Hint:  "no expiry — theirs until released",
	}, storageLabelWidth))
	for i, id := range s.fields {
		l.AddRow(i, renderJDEField(fields[i], storageLabelWidth))
		// The set around the FOCUSED type row: each option carries the letter
		// the grid will paint, which is what the warden reads off the rack
		// afterwards, so it must never be cycled blind.
		if i == s.cursor && id == safType {
			labels := make([]string, len(storageAssignTypeOptions))
			for j, o := range storageAssignTypeOptions {
				labels[j] = o.label
			}
			strip := jdeOptionStrip(labels, s.typeIdx, jdeStripWidth(s.bodyWidth(), storageLabelWidth))
			if strip != "" {
				l.AddRow(i, jdeStripIndent(storageLabelWidth)+StyleMuted.Render(strip))
			}
		}
	}
	return l
}

// formBar names the keys that apply where the cursor is standing. Enter is
// ASSIGN here, not Save — it is what the key does.
func (s *StorageAssignFormScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Assign"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch storageAssignFieldKind(id) {
		case akSelect:
			items = append(items, actionBarItem{"←→", "Change"})
		case akPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		}
	}
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// groupValue is the committee row's text, and whether it is an empty state
// rather than a value. PLAIN text plus a flag, not pre-styled muted text: a
// focused row has to be able to reverse-video the whole field.
func (s *StorageAssignFormScreen) groupValue() (string, bool) {
	if s.owningGroupID == nil {
		return "— none —", true
	}
	if s.owningGroupNm != "" {
		return s.owningGroupNm, false
	}
	return fmt.Sprintf("SIG #%d", *s.owningGroupID), false
}

func (s *StorageAssignFormScreen) pickView() ([]string, *jdeLines) {
	note := "Row 1 is none — describe the committee in Occupant instead."
	switch {
	case s.sigsErr != "":
		note = "SIG list unavailable — " + s.sigsErr
	case !s.sigsReady:
		note = "Loading SIGs…"
	}
	return jdePickList{
		Title:  "Committee",
		For:    s.code,
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickRows),
		Label:  func(i int) string { return s.pickRows[i].label },
		Dim:    func(i int) bool { return s.pickRows[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matching SIGs)",
	}.render()
}

func (s *StorageAssignFormScreen) viewPicker() string {
	header, body := s.pickView()
	paging := false
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail-len(header) {
		paging = true
	}
	return s.frameWithHeader(header, body, s.pickCursor,
		jdeStatusLine(false, "", ""), jdePickBar("Select", paging))
}
