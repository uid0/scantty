// Bulk-generate one rack's slots — the "we just put up an aisle" path.
//
// Mirrors the generate action (GenerateRackSerializer): one rack number, a spec
// per level (letter + how many positions + whether that shelf needs a pallet
// jack), and an optional owning SIG / note applied to every slot the run
// creates. Levels are edited as an ordered list in a sub-phase, following the
// nested sub-list idiom the packaging chain and PM-item forms use.
//
// The run is IDEMPOTENT server-side: codes that already exist are left untouched
// and reported as skipped, so re-running after adding a level — or after a
// partial failure — is safe, and the result panel says exactly what happened
// rather than implying everything was made fresh. `without_tag` is surfaced too:
// a slot with no marker is usable by code but has nothing to scan, and that is
// a fact somebody has to act on rather than discover at the rack.
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
	sgRack = iota
	sgLevels
	sgOwningGroup
	sgNotes
	sgFieldMax
)

var storageGenFieldLabel = map[int]string{
	sgRack:        "Rack",
	sgLevels:      "Levels",
	sgOwningGroup: "Reserved for (SIG)",
	sgNotes:       "Notes",
}

type storageGenPhase int

const (
	genPhaseForm storageGenPhase = iota
	genPhaseLevels
	genPhaseLevelRow
	genPhasePick
	genPhaseResult
)

// storageGenLevelRow is one editable level spec. Positions is kept as typed
// text until commit so a half-typed number never has to mean zero.
type storageGenLevelRow struct {
	level      string
	positions  int
	palletJack bool
}

type StorageSlotGenerateScreen struct {
	deps Deps

	saving bool
	errMsg string

	rackInput  textinput.Model
	notesInput textinput.Model

	levels []storageGenLevelRow

	owningGroupID   *int
	owningGroupName string
	sigs            []omsapi.SIG
	sigsErr         string
	sigsReady       bool

	fields []int
	cursor int

	phase storageGenPhase

	// Level-list sub-phase.
	levelCursor int

	// Level-row editor sub-phase.
	rowIndex      int // -1 when adding
	rowLevel      textinput.Model
	rowPositions  textinput.Model
	rowPalletJack bool
	rowCursor     int
	rowErr        string

	// SIG picker sub-phase.
	pickCursor int
	pickRows   []assetPickRow

	result *omsapi.GenerateRackResult

	terminalHeight int
}

type storageGenLoadedMsg struct {
	sigs []omsapi.SIG
	err  error
}

type storageGenDoneMsg struct {
	result *omsapi.GenerateRackResult
	err    error
}

// NewStorageSlotGenerateScreen seeds the rack from the list screen's current
// scope when there is one — "generate" is almost always aimed at the rack the
// operator is already looking at.
func NewStorageSlotGenerateScreen(deps Deps, rack int) *StorageSlotGenerateScreen {
	mk := func(limit int, placeholder string) textinput.Model {
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = limit
		ti.Placeholder = placeholder
		return ti
	}
	s := &StorageSlotGenerateScreen{
		deps: deps,
		// The SIG list loads in the background and never gates the form — a
		// rack generates fine without an owner.
		rackInput:    mk(5, "pallet rack number, e.g. 1"),
		notesInput:   mk(1000, "optional — applied to every slot created"),
		rowLevel:     mk(1, "A"),
		rowPositions: mk(3, "how many positions (1-100)"),
	}
	if rack > 0 {
		s.rackInput.SetValue(strconv.Itoa(rack))
	}
	s.fields = []int{sgRack, sgLevels, sgOwningGroup, sgNotes}
	s.syncFocus()
	return s
}

func (s *StorageSlotGenerateScreen) Title() string { return "Generate rack" }

// WantsRawInput claims every key — the whole screen is a form and its
// sub-phases.
func (s *StorageSlotGenerateScreen) WantsRawInput() bool { return true }

func (s *StorageSlotGenerateScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *StorageSlotGenerateScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return tea.Batch(func() tea.Msg {
		page, err := deps.OMS.ListSIGs(ctx, nil)
		if err != nil {
			return storageGenLoadedMsg{err: err}
		}
		return storageGenLoadedMsg{sigs: page.Results}
	}, textinput.Blink)
}

func (s *StorageSlotGenerateScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case storageGenLoadedMsg:
		s.sigs = m.sigs
		s.sigsReady = m.err == nil
		if m.err != nil {
			s.sigsErr = m.err.Error()
		}
		return s, textinput.Blink

	case storageGenDoneMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = slotCardErrorText(m.err)
			return s, Status("generate failed: "+s.errMsg, StatusError)
		}
		s.result = m.result
		s.phase = genPhaseResult
		return s, Status(storageGenSummary(m.result), StatusOK)

	case tea.KeyMsg:
		switch s.phase {
		case genPhaseLevels:
			return s.updateLevelsPhase(m)
		case genPhaseLevelRow:
			return s.updateLevelRowPhase(m)
		case genPhasePick:
			return s.updatePickPhase(m)
		case genPhaseResult:
			return s.updateResultPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == genPhaseForm {
		if input := s.focusedInput(); input != nil {
			var cmd tea.Cmd
			*input, cmd = input.Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

// focusedInput returns the textinput the form cursor is on, or nil for the
// non-text rows (levels list, SIG picker).
func (s *StorageSlotGenerateScreen) focusedInput() *textinput.Model {
	id, ok := s.currentFieldID()
	if !ok {
		return nil
	}
	switch id {
	case sgRack:
		return &s.rackInput
	case sgNotes:
		return &s.notesInput
	}
	return nil
}

func (s *StorageSlotGenerateScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *StorageSlotGenerateScreen) syncFocus() {
	s.rackInput.Blur()
	s.notesInput.Blur()
	if input := s.focusedInput(); input != nil {
		input.Focus()
	}
}

func (s *StorageSlotGenerateScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *StorageSlotGenerateScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSFacilities, NewStorageSlotsScreen(s.deps))
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
	if m.String() == " " {
		switch id {
		case sgLevels:
			s.phase = genPhaseLevels
			if s.levelCursor >= len(s.levels) {
				s.levelCursor = len(s.levels) - 1
			}
			if s.levelCursor < 0 {
				s.levelCursor = 0
			}
			return s, nil
		case sgOwningGroup:
			s.openPicker()
			return s, nil
		}
	}
	if input := s.focusedInput(); input != nil {
		var cmd tea.Cmd
		*input, cmd = input.Update(m)
		return s, cmd
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Level list sub-phase
// ---------------------------------------------------------------------------

func (s *StorageSlotGenerateScreen) updateLevelsPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = genPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.levelCursor < len(s.levels)-1 {
			s.levelCursor++
		}
	case "k", "up":
		if s.levelCursor > 0 {
			s.levelCursor--
		}
	case "a":
		s.openLevelRow(-1)
		return s, textinput.Blink
	case "enter", "e":
		if len(s.levels) > 0 {
			s.openLevelRow(s.levelCursor)
			return s, textinput.Blink
		}
	case "x", "d":
		if s.levelCursor >= 0 && s.levelCursor < len(s.levels) {
			s.levels = append(s.levels[:s.levelCursor], s.levels[s.levelCursor+1:]...)
			if s.levelCursor >= len(s.levels) {
				s.levelCursor = len(s.levels) - 1
			}
			if s.levelCursor < 0 {
				s.levelCursor = 0
			}
		}
	}
	return s, nil
}

func (s *StorageSlotGenerateScreen) openLevelRow(index int) {
	s.phase = genPhaseLevelRow
	s.rowIndex = index
	s.rowCursor = 0
	s.rowErr = ""
	if index >= 0 && index < len(s.levels) {
		row := s.levels[index]
		s.rowLevel.SetValue(row.level)
		s.rowPositions.SetValue(strconv.Itoa(row.positions))
		s.rowPalletJack = row.palletJack
	} else {
		s.rowLevel.SetValue("")
		s.rowPositions.SetValue("")
		s.rowPalletJack = false
	}
	s.rowLevel.Focus()
	s.rowPositions.Blur()
}

func (s *StorageSlotGenerateScreen) updateLevelRowPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = genPhaseLevels
		s.rowLevel.Blur()
		s.rowPositions.Blur()
		return s, nil
	case "tab", "down":
		s.rowCursor = (s.rowCursor + 1) % 3
		s.syncRowFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.rowCursor = (s.rowCursor + 2) % 3
		s.syncRowFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitLevelRow()
	case " ":
		if s.rowCursor == 2 {
			s.rowPalletJack = !s.rowPalletJack
			return s, nil
		}
	}
	switch s.rowCursor {
	case 0:
		var cmd tea.Cmd
		s.rowLevel, cmd = s.rowLevel.Update(m)
		return s, cmd
	case 1:
		var cmd tea.Cmd
		s.rowPositions, cmd = s.rowPositions.Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *StorageSlotGenerateScreen) syncRowFocus() {
	s.rowLevel.Blur()
	s.rowPositions.Blur()
	switch s.rowCursor {
	case 0:
		s.rowLevel.Focus()
	case 1:
		s.rowPositions.Focus()
	}
}

// commitLevelRow validates one level against the serializer's own rules — a
// single A-Z letter, 1..100 positions, and each letter at most once per request
// — so a bad row is a message here instead of a 400 that loses the whole spec.
func (s *StorageSlotGenerateScreen) commitLevelRow() tea.Cmd {
	level := strings.ToUpper(strings.TrimSpace(s.rowLevel.Value()))
	if len(level) != 1 || level[0] < 'A' || level[0] > 'Z' {
		s.rowErr = "level must be a single letter (A-Z)"
		return nil
	}
	positions, err := strconv.Atoi(strings.TrimSpace(s.rowPositions.Value()))
	if err != nil {
		s.rowErr = "positions must be a number"
		return nil
	}
	if positions < 1 || positions > 100 {
		s.rowErr = "positions must be between 1 and 100"
		return nil
	}
	for i, row := range s.levels {
		if row.level == level && i != s.rowIndex {
			s.rowErr = "level " + level + " is already in this rack"
			return nil
		}
	}

	row := storageGenLevelRow{level: level, positions: positions, palletJack: s.rowPalletJack}
	if s.rowIndex >= 0 && s.rowIndex < len(s.levels) {
		s.levels[s.rowIndex] = row
	} else {
		s.levels = append(s.levels, row)
		s.levelCursor = len(s.levels) - 1
	}
	s.rowErr = ""
	s.phase = genPhaseLevels
	s.rowLevel.Blur()
	s.rowPositions.Blur()
	return nil
}

// ---------------------------------------------------------------------------
// SIG picker sub-phase
// ---------------------------------------------------------------------------

func (s *StorageSlotGenerateScreen) openPicker() {
	s.phase = genPhasePick
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

func (s *StorageSlotGenerateScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = genPhaseForm
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
		s.phase = genPhaseForm
		s.syncFocus()
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Result sub-phase
// ---------------------------------------------------------------------------

// updateResultPhase keeps the report on screen until the operator dismisses it:
// created/skipped/without-tag is the whole point of an idempotent bulk run and
// a status-line flash would lose it.
func (s *StorageSlotGenerateScreen) updateResultPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter", "q":
		rack := 0
		if s.result != nil {
			rack = s.result.Rack
		}
		list := NewStorageSlotsScreen(s.deps)
		if rack > 0 {
			list.scopeRack = rack
		}
		return s, SwitchTo(WSFacilities, list)
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Save
// ---------------------------------------------------------------------------

func (s *StorageSlotGenerateScreen) buildPayload() (omsapi.GenerateRackRequest, error) {
	var req omsapi.GenerateRackRequest
	rack, err := storageSlotPositiveInt(s.rackInput.Value(), "rack")
	if err != nil {
		return req, err
	}
	if len(s.levels) == 0 {
		return req, errors.New("add at least one level (space on the Levels row)")
	}
	if len(s.levels) > 26 {
		return req, errors.New("a rack has at most 26 levels")
	}
	specs := make([]omsapi.RackLevelSpec, 0, len(s.levels))
	for _, row := range s.levels {
		specs = append(specs, omsapi.RackLevelSpec{
			Level:              row.level,
			Positions:          row.positions,
			RequiresPalletJack: row.palletJack,
		})
	}
	req = omsapi.GenerateRackRequest{
		Rack:        rack,
		Levels:      specs,
		OwningGroup: s.owningGroupID,
		Notes:       strings.TrimSpace(s.notesInput.Value()),
	}
	return req, nil
}

func (s *StorageSlotGenerateScreen) submit() (Screen, tea.Cmd) {
	req, err := s.buildPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""
	deps := s.deps
	ctx := s.ctx()
	return s, func() tea.Msg {
		res, e := deps.OMS.GenerateRackSlots(ctx, req)
		return storageGenDoneMsg{result: res, err: e}
	}
}

func storageGenSummary(res *omsapi.GenerateRackResult) string {
	if res == nil {
		return "rack generated"
	}
	text := fmt.Sprintf("rack %d: %d created, %d already existed", res.Rack, res.CreatedCount, res.SkippedCount)
	if n := len(res.WithoutTag); n > 0 {
		text += fmt.Sprintf(" · %d without an AprilTag", n)
	}
	return text
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *StorageSlotGenerateScreen) View() string {
	switch s.phase {
	case genPhaseLevels:
		return s.viewLevels()
	case genPhaseLevelRow:
		return s.viewLevelRow()
	case genPhasePick:
		return s.viewPicker()
	case genPhaseResult:
		return s.viewResult()
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	b.WriteString(StyleMuted.Render("Creates every slot on the listed levels. Re-running is safe — existing codes are skipped, never overwritten.") + "\n\n")
	for i := range s.fields {
		b.WriteString(s.renderField(i) + "\n")
	}
	b.WriteString("\n")
	switch {
	case s.saving:
		b.WriteString(StyleMuted.Render("Generating…"))
	case s.errMsg != "":
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	case s.sigsErr != "":
		b.WriteString(StyleMuted.Render("SIG list unavailable — " + s.sigsErr))
	default:
		b.WriteString(StyleMuted.Render(s.previewLine()))
	}
	return b.String()
}

// previewLine says how many slots the current spec would create and what the
// first/last codes look like, so an operator sees the SIZE of the run before
// committing to it.
func (s *StorageSlotGenerateScreen) previewLine() string {
	if len(s.levels) == 0 {
		return "no levels yet — space on the Levels row to add one"
	}
	total := 0
	for _, row := range s.levels {
		total += row.positions
	}
	rack := strings.TrimSpace(s.rackInput.Value())
	if rack == "" {
		rack = "?"
	}
	first := s.levels[0]
	last := s.levels[len(s.levels)-1]
	return fmt.Sprintf("%d slot(s): %s%s1 … %s%s%d",
		total, rack, first.level, rack, last.level, last.positions)
}

func (s *StorageSlotGenerateScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := storageGenFieldLabel[id]
	var value string
	switch id {
	case sgRack:
		value = s.rackInput.View()
	case sgNotes:
		value = s.notesInput.View()
	case sgLevels:
		value = s.levelsSummary()
	case sgOwningGroup:
		if s.owningGroupID == nil {
			value = StyleMuted.Render("— not reserved —")
		} else if s.owningGroupName != "" {
			value = s.owningGroupName
		} else {
			value = fmt.Sprintf("SIG #%d", *s.owningGroupID)
		}
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *StorageSlotGenerateScreen) levelsSummary() string {
	if len(s.levels) == 0 {
		return StyleMuted.Render("none — space to add")
	}
	parts := make([]string, 0, len(s.levels))
	for _, row := range s.levels {
		part := fmt.Sprintf("%s×%d", row.level, row.positions)
		if row.palletJack {
			part += "⇞"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "  ")
}

func (s *StorageSlotGenerateScreen) helpText() string {
	id, ok := s.currentFieldID()
	if ok {
		switch id {
		case sgLevels:
			return "space opens the level list · tab/↑↓ move · enter generate · esc cancel"
		case sgOwningGroup:
			return "space opens the SIG list · tab/↑↓ move · enter generate · esc cancel"
		}
	}
	return "type to edit · tab/↑↓ move · enter generate · esc cancel"
}

func (s *StorageSlotGenerateScreen) viewLevels() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Levels on this rack") + "\n")
	b.WriteString(StyleMuted.Render("Early letters are ground-reachable, late letters are up high.") + "\n\n")
	if len(s.levels) == 0 {
		b.WriteString(StyleMuted.Render("No levels yet.") + "\n")
	} else {
		b.WriteString(renderWindowedList(len(s.levels), s.levelCursor, func(i int) string {
			row := s.levels[i]
			text := fmt.Sprintf("%s — %d position(s)", row.level, row.positions)
			if row.palletJack {
				text += " · needs a pallet jack"
			}
			return text
		}))
	}
	b.WriteString("\n" + StyleMuted.Render("a add · enter/e edit · x remove · j/k move · esc done"))
	return b.String()
}

func (s *StorageSlotGenerateScreen) viewLevelRow() string {
	var b strings.Builder
	title := "Add level"
	if s.rowIndex >= 0 {
		title = "Edit level"
	}
	b.WriteString(StyleTitle.Render(title) + "\n\n")
	caret := func(i int) string {
		if i == s.rowCursor {
			return "▸ "
		}
		return "  "
	}
	b.WriteString(caret(0) + StyleTitle.Render("Level: ") + s.rowLevel.View() + "\n")
	b.WriteString(caret(1) + StyleTitle.Render("Positions: ") + s.rowPositions.View() + "\n")
	b.WriteString(caret(2) + StyleTitle.Render("Needs pallet jack: ") + elecToggleLabel(s.rowPalletJack) + "\n")
	b.WriteString("\n")
	if s.rowErr != "" {
		b.WriteString(StyleStatusError.Render("✗ "+s.rowErr) + "\n")
	}
	b.WriteString(StyleMuted.Render("tab move · space toggles · enter save row · esc cancel"))
	return b.String()
}

func (s *StorageSlotGenerateScreen) viewPicker() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Reserve these slots for") + "\n")
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

func (s *StorageSlotGenerateScreen) viewResult() string {
	res := s.result
	var b strings.Builder
	b.WriteString(StyleTitle.Render(fmt.Sprintf("Rack %d generated", res.Rack)) + "\n\n")
	b.WriteString(StyleStatusOK.Render(fmt.Sprintf("Created %d", res.CreatedCount)) + "\n")
	b.WriteString(storageGenCodeList(res.Created) + "\n")
	if res.SkippedCount > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("Already existed (untouched): %d", res.SkippedCount)) + "\n")
		b.WriteString(storageGenCodeList(res.Skipped) + "\n")
	}
	if len(res.WithoutTag) > 0 {
		b.WriteString(StyleStatusWarn.Render(fmt.Sprintf("No AprilTag available for %d slot(s)", len(res.WithoutTag))) + "\n")
		b.WriteString(storageGenCodeList(res.WithoutTag) + "\n")
		b.WriteString(StyleMuted.Render("Those slots work by code but have nothing to scan — the tag family is exhausted.") + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("esc/enter — back to the rack · print its cards with p"))
	return b.String()
}

// storageGenCodeList prints the codes a run touched, capped so a 200-slot rack
// doesn't bury the summary — and it SAYS how many it left off rather than
// trailing away silently.
func storageGenCodeList(codes []string) string {
	if len(codes) == 0 {
		return StyleMuted.Render("  —")
	}
	const max = 24
	shown := codes
	suffix := ""
	if len(codes) > max {
		shown = codes[:max]
		suffix = fmt.Sprintf("  … and %d more", len(codes)-max)
	}
	return "  " + strings.Join(shown, " ") + StyleMuted.Render(suffix)
}
