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
	sgRack:   "Rack",
	sgLevels: "Levels",
	// "(SIG)" moved to the hint — see storageSlotFieldLabel.
	sgOwningGroup: "Reserved for",
	sgNotes:       "Notes",
}

// storageGenFieldHint carries what the placeholders and the label parenthetical
// used to say. Notes keeps a narrower input area than the slot sheet's so its
// note still fits the row: a hint is CLIPPED, not wrapped (see
// TestJDESweepD_RowsFitTheBody).
var storageGenFieldHint = map[int]string{
	sgRack:        "required · pallet rack number",
	sgOwningGroup: "SIG",
	sgNotes:       "on every slot created",
}

func storageGenFieldWidth(id int) int {
	switch id {
	case sgRack:
		return 6
	case sgNotes:
		return 30
	}
	return 0
}

// The rows of the level-row editor. A level being ADDED has nothing to remove
// yet, so it does not get the last one (see levelEditRows).
const (
	genRowLevel = iota
	genRowPositions
	genRowPalletJack
	genRowRemove
	genRowFieldCount
)

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
	pickSearch textinput.Model

	result *omsapi.GenerateRackResult
	// resultCursor scrolls the run report: an idempotent bulk run's
	// created/skipped/without-tag lists are the whole point of the screen, and a
	// 200-slot rack has more of them than the pane is tall.
	resultCursor int

	jdeScreen
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
		// rack generates fine without an owner. Every placeholder moved to a
		// hint: one long enough to fill the input area leaves no underscores, so
		// an empty green-screen row stops reading as empty.
		rackInput:    mk(5, ""),
		notesInput:   mk(1000, ""),
		rowLevel:     mk(1, ""),
		rowPositions: mk(3, ""),
	}
	if rack > 0 {
		s.rackInput.SetValue(strconv.Itoa(rack))
	}
	s.pickSearch = mk(60, "")
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
		s.setSize(m)
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
		s.resultCursor = 0
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

	switch s.phase {
	case genPhasePick:
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	case genPhaseLevelRow:
		if input := s.focusedRowInput(); input != nil {
			var cmd tea.Cmd
			*input, cmd = input.Update(msg)
			return s, cmd
		}
	case genPhaseForm:
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
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, SwitchTo(WSFacilities, NewStorageSlotsScreen(s.deps))
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
		// EDIT opens whatever the highlighted row IS: the level list, or the SIG
		// picker. The bar drops the key on the two typed rows.
		if id, ok := s.currentFieldID(); ok {
			switch id {
			case sgLevels:
				s.openLevels()
				return s, nil
			case sgOwningGroup:
				s.openPicker()
				return s, textinput.Blink
			}
		}
		return s, nil
	}

	if input := s.focusedInput(); input != nil {
		var cmd tea.Cmd
		*input, cmd = input.Update(m)
		return s, cmd
	}
	// A levels or owner row has nothing to type into and no accelerators left.
	return s, nil
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *StorageSlotGenerateScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

func (s *StorageSlotGenerateScreen) openLevels() {
	s.phase = genPhaseLevels
	s.clampLevelCursor()
}

// clampLevelCursor keeps the list cursor inside the rows PLUS the trailing
// "(add a level)" row, which is always reachable as the row after the last one.
func (s *StorageSlotGenerateScreen) clampLevelCursor() {
	if s.levelCursor > len(s.levels) {
		s.levelCursor = len(s.levels)
	}
	if s.levelCursor < 0 {
		s.levelCursor = 0
	}
}

// ---------------------------------------------------------------------------
// Level list sub-phase
// ---------------------------------------------------------------------------

// updateLevelsPhase drives the level list on the reduced key scheme, the fold
// sweep B made for the PM item's sub-lists: Up/Down move (the last row is
// always the "(add a level)" one), Ctrl-E opens whatever the row IS, and Enter
// or Esc are both done — a level list lives in memory until the RACK is
// generated, so leaving writes nothing either way and there is nothing to
// cancel. a/e/x/d/j/k are gone with the rest of the accelerators; removing
// moved into the level's OWN editor, which is the only place the thing being
// dropped is on screen.
func (s *StorageSlotGenerateScreen) updateLevelsPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter":
		s.phase = genPhaseForm
		s.syncFocus()
	case "down", "tab":
		if s.levelCursor < len(s.levels) {
			s.levelCursor++
		}
	case "up", "shift+tab":
		if s.levelCursor > 0 {
			s.levelCursor--
		}
	case "pgdown":
		s.pageLevels(+1)
	case "pgup":
		s.pageLevels(-1)
	case "ctrl+e":
		if s.levelCursor >= len(s.levels) {
			s.openLevelRow(-1) // the trailing add row
		} else {
			s.openLevelRow(s.levelCursor)
		}
		return s, textinput.Blink
	}
	return s, nil
}

// pageLevels moves a pane's worth of rows, clamping. The count excludes the add
// row, which is always reachable as the row after the last one.
func (s *StorageSlotGenerateScreen) pageLevels(dir int) {
	body := s.levelListLines()
	s.levelCursor = jdePageCursor(s.levelCursor, len(s.levels)+1, s.windowRows(body, s.levelCursor, 0), dir)
}

// removeLevel drops a level. Removing is immediate, as it always was: nothing
// is generated until the run is submitted, so there is nothing for a confirm to
// protect.
func (s *StorageSlotGenerateScreen) removeLevel(index int) {
	if index < 0 || index >= len(s.levels) {
		return
	}
	s.levels = append(s.levels[:index:index], s.levels[index+1:]...)
	s.clampLevelCursor()
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

// levelEditRows is how many rows the editor offers: a level being ADDED has
// nothing to remove yet, so it has no remove row.
func (s *StorageSlotGenerateScreen) levelEditRows() int {
	if s.rowIndex < 0 || s.rowIndex >= len(s.levels) {
		return genRowFieldCount - 1
	}
	return genRowFieldCount
}

func (s *StorageSlotGenerateScreen) updateLevelRowPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := s.levelEditRows()
	switch m.String() {
	case "esc":
		s.phase = genPhaseLevels
		s.rowLevel.Blur()
		s.rowPositions.Blur()
		return s, nil
	case "tab", "down":
		s.rowCursor = (s.rowCursor + 1) % n
		s.syncRowFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.rowCursor = (s.rowCursor + n - 1) % n
		s.syncRowFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitLevelRow()
	case "ctrl+e":
		// The remove row is the only thing Ctrl-E opens here, and it is only
		// offered on a level that already exists.
		if s.rowCursor == genRowRemove && s.rowIndex >= 0 {
			s.removeLevel(s.rowIndex)
			s.phase = genPhaseLevels
			s.rowLevel.Blur()
			s.rowPositions.Blur()
		}
		return s, nil
	}
	switch s.rowCursor {
	case genRowPalletJack:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.rowPalletJack = !s.rowPalletJack
		}
		return s, nil
	case genRowLevel:
		var cmd tea.Cmd
		s.rowLevel, cmd = s.rowLevel.Update(m)
		return s, cmd
	case genRowPositions:
		var cmd tea.Cmd
		s.rowPositions, cmd = s.rowPositions.Update(m)
		return s, cmd
	}
	return s, nil
}

// focusedRowInput is the textinput the level editor's cursor is on, or nil for
// its two non-text rows.
func (s *StorageSlotGenerateScreen) focusedRowInput() *textinput.Model {
	switch s.rowCursor {
	case genRowLevel:
		return &s.rowLevel
	case genRowPositions:
		return &s.rowPositions
	}
	return nil
}

func (s *StorageSlotGenerateScreen) syncRowFocus() {
	s.rowLevel.Blur()
	s.rowPositions.Blur()
	if input := s.focusedRowInput(); input != nil {
		input.Focus()
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
func (s *StorageSlotGenerateScreen) applyPickFilter() {
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

func (s *StorageSlotGenerateScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickRows) {
			picked := pickInt(s.pickRows[s.pickCursor])
			s.owningGroupName = storageSlotGroupName(s.sigs, picked, s.owningGroupID, s.owningGroupName)
			s.owningGroupID = picked
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

func (s *StorageSlotGenerateScreen) closePicker() {
	s.phase = genPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Result sub-phase
// ---------------------------------------------------------------------------

// updateResultPhase keeps the report on screen until the operator dismisses it:
// created/skipped/without-tag is the whole point of an idempotent bulk run and
// a status-line flash would lose it.
func (s *StorageSlotGenerateScreen) updateResultPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	body := s.resultLines()
	switch m.String() {
	case "esc", "enter":
		rack := 0
		if s.result != nil {
			rack = s.result.Rack
		}
		list := NewStorageSlotsScreen(s.deps)
		if rack > 0 {
			list.scopeRack = rack
		}
		return s, SwitchTo(WSFacilities, list)
	case "down", "tab":
		s.resultCursor = jdeClampPick(s.resultCursor+1, body.Len())
	case "up", "shift+tab":
		s.resultCursor = jdeClampPick(s.resultCursor-1, body.Len())
	case "pgdown":
		s.resultCursor = jdePageCursor(s.resultCursor, body.Len(), s.windowRows(body, s.resultCursor, 0), +1)
	case "pgup":
		s.resultCursor = jdePageCursor(s.resultCursor, body.Len(), s.windowRows(body, s.resultCursor, 0), -1)
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
		return req, errors.New("add at least one level — Ctrl-E on the Levels row")
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
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusLine(), s.formBar(body))
}

// statusLine is the row above the bar: what is in flight, what went wrong, a
// SIG list that failed — and otherwise the size of the run the current spec
// would make, which is the thing to check before committing to it.
func (s *StorageSlotGenerateScreen) statusLine() string {
	warn := ""
	if s.sigsErr != "" {
		warn = "SIG list unavailable — " + s.sigsErr
	}
	return storageSlotStatusLine(s.jdeScreen, s.saving, "Generating…", s.errMsg, warn, s.previewLine())
}

// formFields describes the sheet as columnar rows: two rows Ctrl-E opens (the
// level list and the SIG picker) and two typed into.
func (s *StorageSlotGenerateScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   storageGenFieldLabel[id],
			Width:   storageGenFieldWidth(id),
			Hint:    storageGenFieldHint[id],
			Focused: i == s.cursor,
		}
		switch id {
		case sgLevels:
			value, dim := s.levelsValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E lists them"
			}
		case sgOwningGroup:
			value, dim := s.owningGroupValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		case sgRack:
			f.Kind, f.Input = jdeText, &s.rackInput
		default:
			f.Kind, f.Input = jdeText, &s.notesInput
		}
		out[i] = f
	}
	return out
}

func (s *StorageSlotGenerateScreen) formLines() *jdeLines {
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Generate rack"))
	l.Add(jdeIndent + StyleMuted.Render("Re-running is safe — existing codes are skipped, never overwritten."))
	l.Add("")
	l.AddFields(s.formFields(), storageLabelWidth, s.bodyWidth(), 0)
	return l
}

// formBar names the keys that apply where the cursor is standing. Enter is
// GENERATE here, not Save — it is what the key does, and the bar is the only
// place left to say so.
func (s *StorageSlotGenerateScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Generate"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch id {
		case sgLevels:
			items = append(items, actionBarItem{"Ctrl-E", "Levels"})
		case sgOwningGroup:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		}
	}
	if s.bodyScrolls(body, 0) {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// levelsValue is the level row's summary, and whether it is an empty state
// rather than a value.
func (s *StorageSlotGenerateScreen) levelsValue() (string, bool) {
	if len(s.levels) == 0 {
		return "(none yet)", true
	}
	parts := make([]string, 0, len(s.levels))
	for _, row := range s.levels {
		part := fmt.Sprintf("%s×%d", row.level, row.positions)
		if row.palletJack {
			part += "⇞"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "  "), false
}

// owningGroupValue is the owner row's text, and whether it is an empty state.
// PLAIN text plus a flag, not pre-styled muted text: a focused row has to be
// able to reverse-video the whole field.
func (s *StorageSlotGenerateScreen) owningGroupValue() (string, bool) {
	if s.owningGroupID == nil {
		return "— not reserved —", true
	}
	if s.owningGroupName != "" {
		return s.owningGroupName, false
	}
	return fmt.Sprintf("SIG #%d", *s.owningGroupID), false
}

// previewLine says how many slots the current spec would create and what the
// first/last codes look like, so an operator sees the SIZE of the run before
// committing to it.
func (s *StorageSlotGenerateScreen) previewLine() string {
	if len(s.levels) == 0 {
		return "no levels yet — Ctrl-E on the Levels row adds one"
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

// ---------------------------------------------------------------------------
// The level list + its row editor
// ---------------------------------------------------------------------------

func (s *StorageSlotGenerateScreen) levelListLines() *jdeLines {
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Levels on this rack"))
	l.Add(jdeIndent + StyleMuted.Render("Early letters are ground-reachable, late letters are up high."))
	l.Add("")
	if len(s.levels) == 0 {
		l.Add(jdeIndent + StyleMuted.Render("(no levels yet)"))
	}
	for i, row := range s.levels {
		text := fmt.Sprintf("%s — %d position(s)", row.level, row.positions)
		if row.palletJack {
			text += " · needs a pallet jack"
		}
		if i == s.levelCursor {
			l.AddRow(i, StyleSidebarItemActive.Render("  ▸ "+text))
			continue
		}
		l.AddRow(i, "    "+text)
	}
	// The add row is the last navigable row, always present: adding is something
	// you move to, not a letter you have to have memorised.
	const addLabel = "(add a level)"
	if s.levelCursor >= len(s.levels) {
		l.AddRow(len(s.levels), StyleSidebarItemActive.Render("  ▸ "+addLabel))
	} else {
		l.AddRow(len(s.levels), "    "+StyleMuted.Render(addLabel))
	}
	return l
}

func (s *StorageSlotGenerateScreen) viewLevels() string {
	body := s.levelListLines()
	// Enter and Esc are both done: the levels are written with the RUN, so
	// leaving the list writes nothing either way.
	items := []actionBarItem{{"Enter", "Done"}, {"Esc", "Done"}, {"UP/DN", "Levels"}}
	if s.levelCursor >= len(s.levels) {
		items = append(items, actionBarItem{"Ctrl-E", "Add a level"})
	} else {
		items = append(items, actionBarItem{"Ctrl-E", "Edit"})
	}
	if s.bodyScrolls(body, 0) {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return s.frame(body, s.levelCursor, "", items)
}

func (s *StorageSlotGenerateScreen) viewLevelRow() string {
	fields := []jdeField{
		{
			Label:   "Level",
			Kind:    jdeText,
			Input:   &s.rowLevel,
			Width:   4,
			Hint:    "required · one letter A-Z",
			Focused: s.rowCursor == genRowLevel,
		},
		{
			Label:   "Positions",
			Kind:    jdeText,
			Input:   &s.rowPositions,
			Width:   6,
			Hint:    "required · 1-100 on this level",
			Focused: s.rowCursor == genRowPositions,
		},
		{
			Label:   "Needs pallet jack",
			Kind:    jdeChoice,
			Value:   jdeYesNo(s.rowPalletJack),
			Focused: s.rowCursor == genRowPalletJack,
		},
	}
	// Removing lives HERE, next to the level it drops — a list of one-line
	// summaries is the worst place to confirm a delete from, and a level being
	// ADDED has nothing to remove yet.
	if s.levelEditRows() > genRowRemove {
		fields = append(fields, jdeField{
			Label:   "Remove this level",
			Kind:    jdeValue,
			Value:   "drops it from the rack",
			Dim:     true,
			Focused: s.rowCursor == genRowRemove,
		})
	}

	title := "Add level"
	if s.rowIndex >= 0 {
		title = "Edit level"
	}
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render(title))
	l.Add("")
	l.AddFields(fields, storageLabelWidth, s.bodyWidth(), 0)

	items := []actionBarItem{{"Enter", "Save level"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	switch {
	case s.rowCursor == genRowPalletJack:
		items = append(items, actionBarItem{"←→", "Change"})
	case s.rowCursor == genRowRemove && s.rowIndex >= 0:
		items = append(items, actionBarItem{"Ctrl-E", "Remove"})
	}
	return s.frame(l, s.rowCursor, s.statusRow(false, "", s.rowErr), items)
}

func (s *StorageSlotGenerateScreen) pickView() ([]string, *jdeLines) {
	note := "Row 1 leaves the slots unreserved — general use."
	switch {
	case s.sigsErr != "":
		note = "SIG list unavailable — " + s.sigsErr
	case !s.sigsReady:
		note = "Loading SIGs…"
	}
	return jdePickList{
		Title:  "Owning SIG",
		For:    "every slot this run creates",
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickRows),
		Label:  func(i int) string { return s.pickRows[i].label },
		Dim:    func(i int) bool { return s.pickRows[i].clear },
		Cursor: s.pickCursor,
		Empty:  "(no matching SIGs)",
	}.render(s.bodyWidth())
}

func (s *StorageSlotGenerateScreen) viewPicker() string {
	header, body := s.pickView()
	paging := false
	if s.bodyScrolls(body, len(header)) {
		paging = true
	}
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), jdePickBar("Select", paging))
}

// resultLines is the run report. Every line is its own navigable row so the
// arrows scroll it: an idempotent run's point is what it created, skipped and
// could not tag, and a 200-slot rack has more of that than the pane is tall.
func (s *StorageSlotGenerateScreen) resultLines() *jdeLines {
	l := &jdeLines{}
	res := s.result
	if res == nil {
		return l
	}
	row := 0
	add := func(text string) {
		l.AddRow(row, text)
		row++
	}
	l.Add(StyleJDEHeading.Render(fmt.Sprintf("Rack %d generated", res.Rack)))
	l.Add("")
	add(jdeIndent + StyleStatusOK.Render(fmt.Sprintf("Created %d", res.CreatedCount)))
	for _, line := range storageGenCodeList(res.Created, s.bodyWidth()) {
		add(line)
	}
	if res.SkippedCount > 0 {
		add("")
		add(jdeIndent + StyleMuted.Render(fmt.Sprintf("Already existed (untouched): %d", res.SkippedCount)))
		for _, line := range storageGenCodeList(res.Skipped, s.bodyWidth()) {
			add(line)
		}
	}
	if n := len(res.WithoutTag); n > 0 {
		add("")
		add(jdeIndent + StyleStatusWarn.Render(fmt.Sprintf("No AprilTag available for %d slot(s)", n)))
		for _, line := range storageGenCodeList(res.WithoutTag, s.bodyWidth()) {
			add(line)
		}
		add(jdeIndent + StyleMuted.Render("They work by code but have nothing to scan — the tag family is out."))
	}
	return l
}

func (s *StorageSlotGenerateScreen) viewResult() string {
	body := s.resultLines()
	items := []actionBarItem{{"Enter", "Back to the rack"}, {"Esc", "Back"}, {"UP/DN", "Scroll"}}
	if s.bodyScrolls(body, 0) {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	// Not "press p": that key belongs to the slots list, and the bar's contract
	// is that a key it shows works HERE.
	//
	// It goes through the shared status row rather than being styled here: that
	// row is ONE unwrapped line and the pane is 51 columns at the 80-column
	// floor, so this sentence was 65 cells of it and clampToBox took the tail —
	// along with StyleMuted's closing reset, leaving the terminal muted for
	// whatever was drawn next. The wording lost "the " twice to come inside the
	// 49 an unmarked message has there, so nothing is shortened at any width
	// rather than the same tail being ellipsised instead of cut.
	return s.frame(body, s.resultCursor,
		storageSlotStatusLine(s.jdeScreen, false, "", "", "",
			"cards for these slots print from the slots list"), items)
}

// storageGenCodeList prints the codes a run touched, WRAPPED to the pane and
// capped so a 200-slot rack doesn't bury the summary — and it SAYS how many it
// left off rather than trailing away silently. A single joined line would be
// truncated by clampToBox, which drops codes with nothing on screen to say so.
func storageGenCodeList(codes []string, width int) []string {
	if len(codes) == 0 {
		return []string{StyleMuted.Render("    —")}
	}
	const max = 24
	shown := codes
	suffix := ""
	if len(codes) > max {
		shown = codes[:max]
		suffix = fmt.Sprintf("… and %d more", len(codes)-max)
	}
	const indent = "    "
	room := width - len(indent)
	if room < 8 {
		room = 8 // an unsized pane: wrap at something rather than not at all
	}
	out := []string{}
	line := ""
	flush := func() {
		if line != "" {
			out = append(out, indent+line)
			line = ""
		}
	}
	for _, code := range shown {
		if line != "" && len(line)+1+len(code) > room {
			flush()
		}
		if line == "" {
			line = code
			continue
		}
		line += " " + code
	}
	flush()
	if suffix != "" {
		out = append(out, indent+StyleMuted.Render(suffix))
	}
	return out
}
