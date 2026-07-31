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
	ssfRack:        "Rack",
	ssfLevel:       "Level",
	ssfPosition:    "Position",
	ssfPalletJack:  "Needs pallet jack",
	ssfActive:      "Active",
	ssfOwningGroup: "Reserved for (SIG)",
	ssfNotes:       "Notes",
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

	fields []int
	cursor int

	phase      storageSlotFormPhase
	pickCursor int
	pickRows   []assetPickRow

	terminalHeight int
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

func storageSlotPlaceholder(id int) string {
	switch id {
	case ssfRack:
		return "pallet rack number, e.g. 1"
	case ssfLevel:
		return "single letter — early = low, late = high"
	case ssfPosition:
		return "position along the rack, from South/East"
	case ssfNotes:
		return "optional"
	}
	return ""
}

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
		s.terminalHeight = m.Height
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

	if s.phase == slotPhaseForm {
		if id, ok := s.currentFieldID(); ok && storageSlotIsTextKind(id) {
			var cmd tea.Cmd
			s.inputs[id], cmd = s.inputs[id].Update(msg)
			return s, cmd
		}
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
		if s.saving || s.loading {
			return s, nil
		}
		return s.submit()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch storageSlotFieldKind(id) {
	case akToggle:
		if m.String() == " " {
			switch id {
			case ssfPalletJack:
				s.palletJack = !s.palletJack
			case ssfActive:
				s.isActive = !s.isActive
			}
		}
		return s, nil
	case akPicker:
		if m.String() == " " {
			s.openPicker()
			return s, nil
		}
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
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
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
	s.pickRows = storageSlotGroupRows(s.sigs, s.owningGroupID, s.owningGroupName)
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
	switch m.String() {
	case "esc":
		// esc means "done looking" and KEEPS the current pick.
		s.phase = slotPhaseForm
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
			s.owningGroupName = storageSlotGroupName(s.sigs, picked, s.owningGroupID, s.owningGroupName)
			s.owningGroupID = picked
		}
		s.phase = slotPhaseForm
		s.syncFocus()
	}
	return s, nil
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

	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	if s.edit {
		b.WriteString(StyleMuted.Render("code: ") + s.currentCodePreview() + "\n")
		b.WriteString(StyleMuted.Render("the code is computed from rack + level + position — changing one renames the slot, and its printed card then reads wrong until reprinted") + "\n\n")
	} else {
		b.WriteString(StyleMuted.Render("code: ") + s.currentCodePreview() +
			StyleMuted.Render("  (computed — an AprilTag is allocated on save)") + "\n\n")
	}

	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	switch {
	case s.saving:
		b.WriteString(StyleMuted.Render("Saving…"))
	case s.errMsg != "":
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	case s.sigsErr != "":
		b.WriteString(StyleMuted.Render("SIG list unavailable — " + s.sigsErr))
	default:
		b.WriteString(StyleMuted.Render("rack + level + position must be unique · writes are staff / Storage Admin only"))
	}
	return b.String()
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

func (s *StorageSlotFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := storageSlotFieldLabel[id]
	var value string
	switch storageSlotFieldKind(id) {
	case akToggle:
		switch id {
		case ssfPalletJack:
			value = elecToggleLabel(s.palletJack)
		case ssfActive:
			value = elecToggleLabel(s.isActive)
		}
	case akPicker:
		value = s.owningGroupLabel()
	default:
		value = s.inputs[id].View()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *StorageSlotFormScreen) owningGroupLabel() string {
	if s.owningGroupID == nil {
		return StyleMuted.Render("— not reserved —")
	}
	if s.owningGroupName != "" {
		return s.owningGroupName
	}
	return fmt.Sprintf("SIG #%d", *s.owningGroupID)
}

func (s *StorageSlotFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch storageSlotFieldKind(id) {
		case akToggle:
			kindHelp = "space toggle"
		case akPicker:
			kindHelp = "space opens the SIG list"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *StorageSlotFormScreen) viewPicker() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Reserve this slot for") + "\n")
	switch {
	case s.sigsErr != "":
		b.WriteString(StyleStatusWarn.Render("SIG list unavailable — "+s.sigsErr) + "\n")
		b.WriteString(StyleMuted.Render("esc keeps the current setting") + "\n\n")
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
