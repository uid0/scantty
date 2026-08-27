// Create / edit ONE storage slot.
//
// The field set is StorageSlotSerializer's writable half, exactly:
// rack, level, position, requires_pallet_jack, is_active, owning_group, notes.
// `code` is NOT here — it is read-only on the wire and the model recomputes it
// from the three components on every save, so the components ARE the identity
// and the code follows. Edit mode shows the current code as a fixed header and
// says so, because changing a component silently renames the slot (and the card
// on the upright then reads wrong until it is reprinted).
//
// Creating a slot allocates its permanent AprilTag server-side, so the save
// confirmation reports the marker the operator now has to print.
//
// Bulk racking lives on the sibling generate screen (`b` from the list); this
// form is the "one more slot at the end of the row" case.
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
	ssfRack = iota
	ssfLevel
	ssfPosition
	ssfPalletJack
	ssfActive
	ssfOwningGroup
	ssfNotes
	ssfFieldMax
)

var storageSlotFieldLabel = map[int]string{
	ssfRack:       "Rack",
	ssfLevel:      "Level",
	ssfPosition:   "Position",
	ssfPalletJack: "Needs pallet jack",
	ssfActive:     "Active",
	// "(SIG)" moved to the hint: a parenthetical in a LABEL widens the column
	// all four storage forms share, so it shoves the input areas right on every
	// one of them (jde_form.go).
	ssfOwningGroup: "Reserved for",
	ssfNotes:       "Notes",
}

// storageSlotFieldHint carries what the placeholders and the label
// parenthetical used to say. A placeholder long enough to fill the input area
// leaves no underscores, so an empty green-screen row stops reading as empty;
// and a columnar form marks what is REQUIRED rather than tagging everything
// else "(optional)".
//
// A hint is CLIPPED, not wrapped, when the row runs past the pane (layout.go's
// clampToBox), so a wide input area and a long hint cannot both fit — which is
// why the three components keep narrow fields and their notes, while Notes
// keeps the room to type and has none. See TestJDESweepD_RowsFitTheBody.
var storageSlotFieldHint = map[int]string{
	ssfRack:        "required · pallet rack number",
	ssfLevel:       "required · A-Z, early = low",
	ssfPosition:    "required · counted from South/East",
	ssfOwningGroup: "SIG",
}

// storageSlotFieldWidth sizes the input areas that are not the default. The
// three components are short by contract (a rack is a small integer, a level is
// ONE letter), and a field wider than its value reads as room the operator does
// not have.
func storageSlotFieldWidth(id int) int {
	switch id {
	case ssfRack, ssfPosition:
		return 6
	case ssfLevel:
		return 4
	case ssfNotes:
		return 40
	}
	return 0
}

func storageSlotFieldKind(id int) assetFieldKind {
	switch id {
	case ssfRack, ssfPosition:
		return akNumber
	case ssfPalletJack, ssfActive:
		return akToggle
	case ssfOwningGroup:
		return akPicker
	}
	return akText
}

func storageSlotIsTextKind(id int) bool {
	k := storageSlotFieldKind(id)
	return k == akText || k == akNumber
}

type storageSlotFormPhase int

const (
	slotPhaseForm storageSlotFormPhase = iota
	slotPhasePick
)

type StorageSlotFormScreen struct {
	deps Deps
	edit bool
	code string // the code being edited; empty in create mode

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	slot *omsapi.StorageSlot

	inputs        []textinput.Model
	palletJack    bool
	isActive      bool
	owningGroupID *int
	// owningGroupName remembers what the slot reported so an owner missing from
	// the loaded SIG list can still be shown and grafted into the picker.
	owningGroupName string

	sigs      []omsapi.SIG
	sigsErr   string
	sigsReady bool

	jdeScreen

	fields []int
	cursor int

	phase      storageSlotFormPhase
	pickCursor int
	pickRows   []assetPickRow
	pickSearch textinput.Model
}

type storageSlotFormLoadedMsg struct {
	slot    *omsapi.StorageSlot
	err     error
	sigs    []omsapi.SIG
	sigsErr error
}

type storageSlotSavedMsg struct {
	slot    *omsapi.StorageSlot
	created bool
	err     error
}

// NewStorageSlotFormScreen opens create mode for an empty code, else edit mode
// (hydrating from the fetched slot).
func NewStorageSlotFormScreen(deps Deps, code string) *StorageSlotFormScreen {
	edit := strings.TrimSpace(code) != ""
	s := &StorageSlotFormScreen{
		deps: deps,
		edit: edit,
		code: strings.TrimSpace(code),
		// Only an EDIT has something to wait for. The SIG list loads in the
		// background and never gates the form — a slot saves fine without an
		// owner, so a slow (or forbidden) SIG list must not hold up typing.
		loading:  edit,
		isActive: true, // model default
	}
	s.inputs = make([]textinput.Model, ssfFieldMax)
	for id := 0; id < ssfFieldMax; id++ {
		if !storageSlotIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = storageSlotCharLimit(id)
		ti.Placeholder = storageSlotPlaceholder(id)
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.CharLimit = 60
	s.fields = []int{ssfRack, ssfLevel, ssfPosition, ssfPalletJack, ssfActive, ssfOwningGroup, ssfNotes}
	s.syncFocus()
	return s
}

func storageSlotCharLimit(id int) int {
	switch id {
	case ssfRack, ssfPosition:
		return 5 // PositiveSmallIntegerField
	case ssfLevel:
		return 1 // model CharField(max_length=1)
	}
	return 1000
}

// storageSlotPlaceholder is empty for every field now — see
// storageSlotFieldHint.
func storageSlotPlaceholder(id int) string { return "" }

func (s *StorageSlotFormScreen) Title() string {
	if s.edit {
		return "Edit slot " + s.code
	}
	return "New storage slot"
}

// WantsRawInput claims every key so field letters, tab, enter and esc reach the
// form instead of the root's globals.
func (s *StorageSlotFormScreen) WantsRawInput() bool { return true }

func (s *StorageSlotFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *StorageSlotFormScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	code := s.code
	edit := s.edit
	load := func() tea.Msg {
		msg := storageSlotFormLoadedMsg{}
		// The SIG list is a separate permission surface from the slots; a
		// failure there must not block editing the slot itself, so it is
		// reported beside the form rather than as a load error.
		if sigs, err := deps.OMS.ListSIGs(ctx, nil); err == nil {
			msg.sigs = sigs.Results
		} else {
			msg.sigsErr = err
		}
		if edit {
			msg.slot, msg.err = deps.OMS.GetStorageSlot(ctx, code)
		}
		return msg
	}
	return tea.Batch(load, textinput.Blink)
}

func (s *StorageSlotFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil

	case storageSlotFormLoadedMsg:
		s.loading = false
		s.sigs = m.sigs
		s.sigsReady = m.sigsErr == nil
		if m.sigsErr != nil {
			s.sigsErr = m.sigsErr.Error()
		}
		if m.err != nil {
			s.loadErr = slotCardErrorText(m.err)
			return s, nil
		}
		if m.slot != nil {
			s.slot = m.slot
			s.hydrate()
		}
		return s, textinput.Blink

	case storageSlotSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = slotCardErrorText(m.err)
			return s, Status("save failed: "+s.errMsg, StatusError)
		}
		return s, tea.Batch(
			Status(storageSlotSavedText(m.slot, m.created), StatusOK),
			SwitchTo(WSFacilities, NewStorageSlotDetailScreen(s.deps, storageSlotSavedCode(m.slot, s.code))),
		)

	case tea.KeyMsg:
		if s.phase == slotPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == slotPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && storageSlotIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

// storageSlotSavedCode prefers the code the SERVER computed — editing
// rack/level/position renames the slot, and navigating to the old code would
// 404 on a slot that saved fine.
func storageSlotSavedCode(slot *omsapi.StorageSlot, fallback string) string {
	if slot != nil && slot.Code != "" {
		return slot.Code
	}
	return fallback
}

func storageSlotSavedText(slot *omsapi.StorageSlot, created bool) string {
	verb := "slot updated"
	if created {
		verb = "slot created"
	}
	if slot == nil {
		return verb
	}
	text := verb + ": " + slot.Code
	if created {
		if slot.AprilTagID != nil {
			text += fmt.Sprintf(" · AprilTag %d — print its card", *slot.AprilTagID)
		} else {
			text += " · no AprilTag was available — the tag family is exhausted"
		}
	}
	return text
}

func (s *StorageSlotFormScreen) hydrate() {
	slot := s.slot
	s.inputs[ssfRack].SetValue(strconv.Itoa(slot.Rack))
	s.inputs[ssfLevel].SetValue(slot.Level)
	s.inputs[ssfPosition].SetValue(strconv.Itoa(slot.Position))
	s.inputs[ssfNotes].SetValue(slot.Notes)
	s.palletJack = slot.RequiresPalletJack
	s.isActive = slot.IsActive
	s.owningGroupID = slot.OwningGroup
	s.owningGroupName = slot.OwningGroupName
	s.syncFocus()
}

func (s *StorageSlotFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		if s.saving || s.loading {
			return s, nil
		}
		return s.submit()
	case "ctrl+e":
		// EDIT opens whatever the highlighted row IS. Only the owner row opens
		// anything, which is why the bar drops the key on the others.
		if id, ok := s.currentFieldID(); ok && storageSlotFieldKind(id) == akPicker {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch storageSlotFieldKind(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			switch id {
			case ssfPalletJack:
				s.palletJack = !s.palletJack
			case ssfActive:
				s.isActive = !s.isActive
			}
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

func (s *StorageSlotFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *StorageSlotFormScreen) moveCursor(delta int) {
	body := s.formLines()
	next, ok := s.moveRow(s.cursor, len(s.fields), delta, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *StorageSlotFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0, s.formBar(body))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *StorageSlotFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if storageSlotIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && storageSlotIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

// ---------------------------------------------------------------------------
// Owning-SIG picker
// ---------------------------------------------------------------------------

func (s *StorageSlotFormScreen) openPicker() {
	s.phase = slotPhasePick
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

// applyPickFilter narrows the option list to what has been typed. The CLEAR row
// is never filtered out: detaching the owner is an action, not one of the
// values being searched, and a query that hid it would strand the operator on a
// SIG they can no longer remove.
func (s *StorageSlotFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	rows := make([]assetPickRow, 0, len(s.sigs)+2)
	for _, r := range storageSlotGroupRows(s.sigs, s.owningGroupID, s.owningGroupName) {
		if r.clear || q == "" || strings.Contains(strings.ToLower(r.label), q) {
			rows = append(rows, r)
		}
	}
	s.pickRows = rows
	if s.pickCursor >= len(s.pickRows) {
		s.pickCursor = 0
	}
}

// storageSlotGroupRows builds the picker options: an explicit clear row first,
// then every SIG. The slot's CURRENT owner is grafted in when the loaded list
// doesn't contain it — the list is one page and can also fail outright, and
// without the graft an edit aimed at another field would rest the cursor on
// "(none)" and silently un-reserve the slot.
func storageSlotGroupRows(sigs []omsapi.SIG, currentID *int, currentName string) []assetPickRow {
	rows := []assetPickRow{{clear: true, label: "— not reserved —"}}
	found := false
	for _, g := range sigs {
		if currentID != nil && g.ID == *currentID {
			found = true
		}
		rows = append(rows, assetPickRow{key: strconv.Itoa(g.ID), label: g.Name})
	}
	if currentID != nil && !found {
		label := currentName
		if strings.TrimSpace(label) == "" {
			label = fmt.Sprintf("SIG #%d", *currentID)
		}
		rows = append(rows, assetPickRow{key: strconv.Itoa(*currentID), label: label + " (current)"})
	}
	return rows
}

// storageSlotGroupName resolves the display name for a picked SIG. It reads the
// loaded list rather than the row LABEL, because the grafted row's label
// carries a " (current)" suffix and a SIG genuinely named that way would be
// mangled by trimming it back off. Falls back to the name the slot itself
// reported when the pick is the grafted one.
func storageSlotGroupName(sigs []omsapi.SIG, picked *int, graftedID *int, graftedName string) string {
	if picked == nil {
		return ""
	}
	for _, g := range sigs {
		if g.ID == *picked {
			return g.Name
		}
	}
	if graftedID != nil && *graftedID == *picked {
		return graftedName
	}
	return ""
}

func (s *StorageSlotFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		// esc means "done looking" and KEEPS the current pick.
		s.closePicker()
	case jdePickCommit:
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickRows) {
			picked := pickInt(s.pickRows[s.pickCursor])
			s.owningGroupName = storageSlotGroupName(s.sigs, picked, s.owningGroupID, s.owningGroupName)
			s.owningGroupID = picked
		}
		s.closePicker()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		s.movePick(delta * s.windowRowsForBar(body, s.pickCursor, len(header), s.pickBar(header, body)))
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

func (s *StorageSlotFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickRows), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *StorageSlotFormScreen) closePicker() {
	s.phase = slotPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

// buildPayload validates locally so a typo is a message rather than a 400. The
// three components are required and the backend's own constraints (rack ≥ 1,
// a single A-Z level, position ≥ 1) are checked here in the same terms.
func (s *StorageSlotFormScreen) buildPayload() (omsapi.StorageSlotWrite, error) {
	var w omsapi.StorageSlotWrite

	rack, err := storageSlotPositiveInt(s.inputs[ssfRack].Value(), "rack")
	if err != nil {
		return w, err
	}
	level := strings.ToUpper(strings.TrimSpace(s.inputs[ssfLevel].Value()))
	if len(level) != 1 || level[0] < 'A' || level[0] > 'Z' {
		return w, errors.New("level must be a single letter (A-Z)")
	}
	position, err := storageSlotPositiveInt(s.inputs[ssfPosition].Value(), "position")
	if err != nil {
		return w, err
	}

	w = omsapi.StorageSlotWrite{
		Rack:               rack,
		Level:              level,
		Position:           position,
		RequiresPalletJack: s.palletJack,
		IsActive:           s.isActive,
		OwningGroup:        s.owningGroupID,
		Notes:              strings.TrimSpace(s.inputs[ssfNotes].Value()),
	}
	return w, nil
}

func storageSlotPositiveInt(raw, field string) (int, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, fmt.Errorf("%s is required", field)
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number", field)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be 1 or more", field)
	}
	return n, nil
}

func (s *StorageSlotFormScreen) submit() (Screen, tea.Cmd) {
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
	code := s.code
	return s, func() tea.Msg {
		if edit {
			slot, e := deps.OMS.UpdateStorageSlot(ctx, code, body)
			return storageSlotSavedMsg{slot: slot, err: e}
		}
		slot, e := deps.OMS.CreateStorageSlot(ctx, body)
		return storageSlotSavedMsg{slot: slot, created: true, err: e}
	}
}

func (s *StorageSlotFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.code != "" {
		return SwitchTo(WSFacilities, NewStorageSlotDetailScreen(s.deps, s.code))
	}
	return SwitchTo(WSFacilities, NewStorageSlotsScreen(s.deps))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *StorageSlotFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc back")
	}
	if s.phase == slotPhasePick {
		return s.viewPicker()
	}
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusLine(), s.formBar(body))
}

// statusLine is the row above the bar. A SIG list that failed is a WARNING
// beside the form rather than an error on it — the slot saves fine without an
// owner, which is why the load never gated the form in the first place.
func (s *StorageSlotFormScreen) statusLine() string {
	warn := ""
	if s.sigsErr != "" {
		warn = "SIG list unavailable — " + s.sigsErr
	}
	return storageSlotStatusLine(s.jdeScreen, s.saving, "Saving…", s.errMsg, warn,
		"rack + level + position must be unique · staff / Storage Admin only")
}

// formFields describes the sheet as columnar rows: two bounded sets, one FK row
// Ctrl-E opens, and the rest typed into.
func (s *StorageSlotFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   storageSlotFieldLabel[id],
			Width:   storageSlotFieldWidth(id),
			Hint:    storageSlotFieldHint[id],
			Focused: i == s.cursor,
		}
		switch storageSlotFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.toggleState(id))
		case akPicker:
			value, dim := s.owningGroupValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

// toggleState is a bool row's current value.
func (s *StorageSlotFormScreen) toggleState(id int) bool {
	if id == ssfPalletJack {
		return s.palletJack
	}
	return s.isActive
}

func (s *StorageSlotFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Storage slot"))
	if s.edit {
		l.Add(jdeIndent + StyleMuted.Render("Changing a component renames the slot — its card then needs reprinting."))
	}
	l.Add("")
	// The code is derived from the three rows below it, so it sits above them as
	// a dimmed, non-navigable row of the same sheet (l.Add, not AddRow) rather
	// than as a header the operator would not tie to any field.
	l.Add(renderJDEField(jdeField{
		Label: "Code",
		Kind:  jdeValue,
		Value: s.currentCodePreview(),
		Dim:   true,
		Hint:  s.codeHint(),
	}, storageLabelWidth, s.bodyWidth()))
	l.AddFields(fields, storageLabelWidth, s.bodyWidth(), 0)
	return l
}

// codeHint says where the code comes from, and — on a create — what saving it
// also allocates.
func (s *StorageSlotFormScreen) codeHint() string {
	if s.edit {
		return "computed from rack + level + position"
	}
	return "computed · save allocates an AprilTag"
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *StorageSlotFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyScrollsForBar(body, 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *StorageSlotFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch storageSlotFieldKind(id) {
		case akToggle:
			items = append(items, actionBarItem{"←→", "Change"})
		case akPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// currentCodePreview shows what the components spell right now, so the operator
// sees the identity they are about to create or rename before saving. Falls
// back to the stored code when the fields aren't a valid code yet.
func (s *StorageSlotFormScreen) currentCodePreview() string {
	rack := strings.TrimSpace(s.inputs[ssfRack].Value())
	level := strings.ToUpper(strings.TrimSpace(s.inputs[ssfLevel].Value()))
	position := strings.TrimSpace(s.inputs[ssfPosition].Value())
	if rack != "" && level != "" && position != "" {
		if code, err := omsapi.NormalizeStorageSlotCode(rack + level + position); err == nil {
			return code
		}
	}
	if s.slot != nil {
		return s.slot.Code
	}
	return "—"
}

// owningGroupValue is the owner row's text, and whether it is an empty state
// rather than a value. It returns PLAIN text with a flag instead of pre-styled
// muted text, because a focused row has to be able to reverse-video the whole
// field — an inner reset sequence would end the highlight partway through it.
func (s *StorageSlotFormScreen) owningGroupValue() (string, bool) {
	if s.owningGroupID == nil {
		return "— not reserved —", true
	}
	if s.owningGroupName != "" {
		return s.owningGroupName, false
	}
	return fmt.Sprintf("SIG #%d", *s.owningGroupID), false
}

// pickView builds the open picker's pinned header and its option list. The note
// explains what the clear row does — the one thing about the list that is not
// self-evident — or why the list is short.
func (s *StorageSlotFormScreen) pickView() (jdeHeader, *jdeLines) {
	note := "Row 1 releases the reservation — the slot goes back to general use."
	switch {
	case s.sigsErr != "":
		note = "SIG list unavailable — " + s.sigsErr
	case !s.sigsReady:
		note = "Loading SIGs…"
	}
	// The title names WHAT is being picked and For names what for — "Reserve
	// this slot for" would render as "… for  for 1A1", because jdePickList
	// supplies the "for" itself.
	forCode := s.currentCodePreview()
	if forCode == "—" {
		forCode = ""
	}
	return jdePickList{
		Title:  "Owning SIG",
		For:    forCode,
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickRows),
		Label:  func(i int) string { return s.pickRows[i].label },
		Dim:    func(i int) bool { return s.pickRows[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matching SIGs)",
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *StorageSlotFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", s.bodyScrollsForBar(body, len(header), jdePickBar("Select", true)))
}

func (s *StorageSlotFormScreen) viewPicker() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}
