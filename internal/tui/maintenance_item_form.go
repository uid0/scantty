// MaintenanceItemFormScreen — create/edit form for preventive-maintenance
// (PM) items.
//
// This is the TUI counterpart to the web MaintenanceItemFormPage
// (frontend/src/pages/MaintenanceItemFormPage.tsx + maintenanceItemFormSchema).
// It mirrors the FULL field set that page exposes — title, description,
// instructions, interval, estimated time/cost, active flag — plus, like the
// web page, an inline MATERIAL editor, and additionally a TASK-step editor
// (the web manages task steps through the same maintenance-tasks API this form
// drives) so an operator can define a complete PM item from the workstation
// without the browser ([[ship-complete-features]]).
//
// The main-field structure follows inventory_item_form.go (field-by-field
// entry, an asset sub-phase picker, a toggle, windowed scroll). Tasks and
// materials are managed in their own sub-phases: a list (add / edit / remove
// rows) that opens a small row editor. On save the item is written first, then
// tasks + materials are reconciled against what was loaded (new rows created,
// edited rows patched, removed rows deleted).
package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/omsapi"
)

// Main-field identifiers, in canonical top-to-bottom render order.
const (
	mfAsset = iota
	mfTitle
	mfDescription
	mfInstructions
	mfIntervalDays
	mfEstTimeMin
	mfEstCost
	mfIsActive
	mfTasks
	mfMaterials
	mfFieldMax
)

type mFieldKind int

const (
	mkText mFieldKind = iota
	mkNumber
	mkToggle
	mkPicker
	mkSublist
)

type mFormPhase int

const (
	mFormPhaseForm mFormPhase = iota
	mFormPhaseAssetPick
	mFormPhaseTaskList
	mFormPhaseTaskEdit
	mFormPhaseMaterialList
	mFormPhaseMaterialEdit
)

var mfLabel = map[int]string{
	mfAsset:        "Asset",
	mfTitle:        "Title",
	mfDescription:  "Description",
	mfInstructions: "Instructions",
	mfIntervalDays: "Interval (days)",
	mfEstTimeMin:   "Estimated time (min)",
	mfEstCost:      "Estimated cost ($)",
	mfIsActive:     "Active",
	mfTasks:        "Task steps",
	mfMaterials:    "Materials",
}

func mfKind(id int) mFieldKind {
	switch id {
	case mfTitle, mfDescription, mfInstructions:
		return mkText
	case mfIntervalDays, mfEstTimeMin, mfEstCost:
		return mkNumber
	case mfIsActive:
		return mkToggle
	case mfAsset:
		return mkPicker
	case mfTasks, mfMaterials:
		return mkSublist
	}
	return mkText
}

func mfIsTextKind(id int) bool {
	k := mfKind(id)
	return k == mkText || k == mkNumber
}

// taskRow is one in-memory MaintenanceTask step. id is empty for a row that
// hasn't been saved yet; loadedOrder is the order the row had when fetched (so
// save can tell whether the position changed). dirty marks an existing row the
// operator edited.
type taskRow struct {
	id          string
	title       string
	description string
	isRequired  bool
	loadedOrder int
	dirty       bool
}

// materialRow is one in-memory MaintenanceMaterial. Quantity + cost are kept as
// strings (the backend field is a DecimalField).
type materialRow struct {
	id       string
	name     string
	quantity string
	unit     string
	cost     string
	notes    string
	dirty    bool
}

type assetPickOption struct {
	id    string
	label string
}

type MaintenanceItemFormScreen struct {
	deps   Deps
	edit   bool
	itemID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	// Reference data + edit-mode hydration source.
	assets        []omsapi.Asset
	item          *omsapi.MaintenanceItem
	assetsArrived bool
	itemArrived   bool

	terminalHeight int

	// Main text/number inputs, indexed by field id.
	inputs []textinput.Model

	isActive bool

	// Asset selection (asset is required).
	assetID   string
	assetName string

	// Task + material rows, plus the ids present at load (for delete-reconcile).
	tasks           []taskRow
	materials       []materialRow
	origTaskIDs     []string
	origMaterialIDs []string

	// Visible main-field navigation.
	fields []int
	cursor int

	phase mFormPhase

	// Asset picker sub-phase.
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []assetPickOption

	// Task/material list sub-phase cursor.
	rowCursor int

	// Row-editor sub-phase (shared cursor; index -1 == adding a new row).
	editIndex  int
	editCursor int
	editErr    string
	// Task editor inputs.
	teTitle    textinput.Model
	teDesc     textinput.Model
	teRequired bool
	// Material editor inputs.
	meName  textinput.Model
	meQty   textinput.Model
	meUnit  textinput.Model
	meCost  textinput.Model
	meNotes textinput.Model
}

type mFormRefLoadedMsg struct {
	assets []omsapi.Asset
	err    error
}

type mFormItemLoadedMsg struct {
	item *omsapi.MaintenanceItem
	err  error
}

type mFormSavedMsg struct {
	itemID string
	err    error
}

// NewMaintenanceItemFormScreen builds the create/edit form. An empty itemID
// opens create mode; a non-empty id opens edit mode and hydrates every field
// (including task + material rows) from the fetched item.
func NewMaintenanceItemFormScreen(deps Deps, itemID string) *MaintenanceItemFormScreen {
	edit := strings.TrimSpace(itemID) != ""
	s := &MaintenanceItemFormScreen{
		deps:      deps,
		edit:      edit,
		itemID:    strings.TrimSpace(itemID),
		loading:   true,
		isActive:  true, // web default
		editIndex: -1,
	}

	s.inputs = make([]textinput.Model, mfFieldMax)
	for id := 0; id < mfFieldMax; id++ {
		if !mfIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = mfCharLimit(id)
		ti.Placeholder = mfPlaceholder(id)
		s.inputs[id] = ti
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	for _, ti := range []*textinput.Model{&s.teTitle, &s.teDesc, &s.meName, &s.meQty, &s.meUnit, &s.meCost, &s.meNotes} {
		*ti = textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
	}

	s.rebuildFields()
	s.syncFocus()
	return s
}

func mfCharLimit(id int) int {
	switch id {
	case mfTitle:
		return 200
	case mfDescription, mfInstructions:
		return 1000
	default:
		return 12
	}
}

func mfPlaceholder(id int) string {
	switch id {
	case mfTitle:
		return "e.g. Replace air filter"
	case mfDescription:
		return "why this maintenance is needed"
	case mfInstructions:
		return "step-by-step instructions"
	case mfIntervalDays:
		return "blank = one-time / as-needed"
	case mfEstTimeMin:
		return "optional"
	case mfEstCost:
		return "0"
	}
	return ""
}

func (s *MaintenanceItemFormScreen) Title() string {
	if s.edit {
		if s.item != nil && s.item.Title != "" {
			return fmt.Sprintf("Edit PM item: %s", s.item.Title)
		}
		return "Edit PM item"
	}
	return "New PM item"
}

func (s *MaintenanceItemFormScreen) WantsRawInput() bool { return true }

func (s *MaintenanceItemFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadItem())
	}
	return tea.Batch(cmds...)
}

func (s *MaintenanceItemFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *MaintenanceItemFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		assets, err := deps.OMS.ListAllAssets(ctx)
		return mFormRefLoadedMsg{assets: assets, err: err}
	}
}

func (s *MaintenanceItemFormScreen) loadItem() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		item, err := deps.OMS.GetMaintenanceItem(ctx, id)
		return mFormItemLoadedMsg{item: item, err: err}
	}
}

func (s *MaintenanceItemFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case mFormRefLoadedMsg:
		s.assetsArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.assets = m.assets
		}
		return s, s.maybeFinalizeLoad()

	case mFormItemLoadedMsg:
		s.itemArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.item = m.item
		}
		return s, s.maybeFinalizeLoad()

	case mFormSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("PM item %s", verb), StatusOK),
			SwitchTo(WSMaintenance, NewMaintenanceItemDetailScreen(s.deps, m.itemID)),
		)

	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		switch s.phase {
		case mFormPhaseAssetPick:
			return s.updateAssetPick(m)
		case mFormPhaseTaskList:
			return s.updateTaskList(m)
		case mFormPhaseTaskEdit:
			return s.updateTaskEdit(m)
		case mFormPhaseMaterialList:
			return s.updateMaterialList(m)
		case mFormPhaseMaterialEdit:
			return s.updateMaterialEdit(m)
		default:
			return s.updateFormPhase(m)
		}
	}

	// Non-key messages (cursor blink) go to whichever input owns the caret.
	return s, s.forwardBlink(msg)
}

// forwardBlink routes a non-key message (the textinput cursor blink tick) to
// the input focused in the current phase.
func (s *MaintenanceItemFormScreen) forwardBlink(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	switch s.phase {
	case mFormPhaseAssetPick:
		s.pickSearch, cmd = s.pickSearch.Update(msg)
	case mFormPhaseTaskEdit:
		switch s.editCursor {
		case 0:
			s.teTitle, cmd = s.teTitle.Update(msg)
		case 1:
			s.teDesc, cmd = s.teDesc.Update(msg)
		}
	case mFormPhaseMaterialEdit:
		if in := s.materialEditInput(s.editCursor); in != nil {
			*in, cmd = in.Update(msg)
		}
	case mFormPhaseForm:
		if id, ok := s.currentFieldID(); ok && mfIsTextKind(id) {
			s.inputs[id], cmd = s.inputs[id].Update(msg)
		}
	}
	return cmd
}

func (s *MaintenanceItemFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.assetsArrived {
		return nil
	}
	if s.edit && !s.itemArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.item != nil {
		s.hydrate()
	}
	s.rebuildFields()
	s.syncFocus()
	return nil
}

func (s *MaintenanceItemFormScreen) hydrate() {
	it := s.item
	set := func(id int, v string) { s.inputs[id].SetValue(v) }

	s.assetID = it.Asset
	s.assetName = it.AssetName

	set(mfTitle, it.Title)
	set(mfDescription, it.Description)
	set(mfInstructions, it.Instructions)
	if it.IntervalDays != nil {
		set(mfIntervalDays, strconv.Itoa(*it.IntervalDays))
	}
	if it.EstimatedTimeMin != nil {
		set(mfEstTimeMin, strconv.Itoa(*it.EstimatedTimeMin))
	}
	if c := strings.TrimSpace(it.EstimatedCost.String()); c != "" {
		set(mfEstCost, c)
	}
	s.isActive = it.IsActive

	// Tasks, sorted by their saved order so the list reflects run order.
	tasks := make([]taskRow, 0, len(it.Tasks))
	for _, t := range it.Tasks {
		tasks = append(tasks, taskRow{
			id:          t.ID,
			title:       t.Title,
			description: t.Description,
			isRequired:  t.IsRequired,
			loadedOrder: t.Order,
		})
		s.origTaskIDs = append(s.origTaskIDs, t.ID)
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].loadedOrder < tasks[j].loadedOrder })
	s.tasks = tasks

	for _, m := range it.Materials {
		s.materials = append(s.materials, materialRow{
			id:       m.ID,
			name:     m.Name,
			quantity: m.Quantity.String(),
			unit:     m.Unit,
			cost:     m.EstimatedCostPerUnit.String(),
			notes:    m.Notes,
		})
		s.origMaterialIDs = append(s.origMaterialIDs, m.ID)
	}
}

func (s *MaintenanceItemFormScreen) rebuildFields() {
	var focused = -1
	if id, ok := s.currentFieldID(); ok {
		focused = id
	}
	// No conditional sections in this form — every main field is always shown.
	s.fields = []int{mfAsset, mfTitle, mfDescription, mfInstructions, mfIntervalDays, mfEstTimeMin, mfEstCost, mfIsActive, mfTasks, mfMaterials}
	if focused >= 0 {
		s.setCursorToField(focused)
	}
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *MaintenanceItemFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *MaintenanceItemFormScreen) setCursorToField(id int) {
	for i, fid := range s.fields {
		if fid == id {
			s.cursor = i
			return
		}
	}
}

func (s *MaintenanceItemFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if mfIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && mfIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

// ---------------------------------------------------------------------------
// Main form phase
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		// Enter on a sub-list field opens it rather than submitting, so the
		// operator doesn't accidentally save while managing rows.
		if id, ok := s.currentFieldID(); ok && mfKind(id) == mkSublist {
			s.openSublist(id)
			return s, textinput.Blink
		}
		return s.submit()
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch mfKind(id) {
	case mkToggle:
		if m.String() == " " {
			s.isActive = !s.isActive
		}
		return s, nil
	case mkPicker:
		if m.String() == " " {
			s.openAssetPick()
			return s, textinput.Blink
		}
		return s, nil
	case mkSublist:
		if m.String() == " " {
			s.openSublist(id)
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *MaintenanceItemFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *MaintenanceItemFormScreen) openSublist(id int) {
	if id == mfTasks {
		s.phase = mFormPhaseTaskList
		if s.rowCursor >= len(s.tasks) {
			s.rowCursor = 0
		}
	} else {
		s.phase = mFormPhaseMaterialList
		if s.rowCursor >= len(s.materials) {
			s.rowCursor = 0
		}
	}
	s.rowCursor = 0
	s.syncBlurAll()
}

func (s *MaintenanceItemFormScreen) syncBlurAll() {
	for id := 0; id < len(s.inputs); id++ {
		if mfIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
}

// ---------------------------------------------------------------------------
// Asset picker sub-phase
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) openAssetPick() {
	s.phase = mFormPhaseAssetPick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncBlurAll()
	s.applyAssetFilter()
	s.pickCursor = 0
	for i, o := range s.pickOptions {
		if o.id == s.assetID {
			s.pickCursor = i
			break
		}
	}
}

func assetPickLabel(a omsapi.Asset) string {
	label := a.Name
	if a.AssetTag != "" {
		label = fmt.Sprintf("%s (%s)", a.Name, a.AssetTag)
	}
	if a.LocationName != "" {
		label += " · " + a.LocationName
	}
	return label
}

func (s *MaintenanceItemFormScreen) applyAssetFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := make([]assetPickOption, 0, len(s.assets))
	for _, a := range s.assets {
		label := assetPickLabel(a)
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickOption{id: fmt.Sprint(a.ID), label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *MaintenanceItemFormScreen) updateAssetPick(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyAssetFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyAssetFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = mFormPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.pickCursor < len(s.pickOptions)-1 {
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
	case "enter":
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
			opt := s.pickOptions[s.pickCursor]
			s.assetID = opt.id
			s.assetName = opt.label
		}
		s.phase = mFormPhaseForm
		s.pickSearch.SetValue("")
		s.pickSearch.Blur()
		s.syncFocus()
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Task list + editor sub-phases
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) updateTaskList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.rowCursor < len(s.tasks)-1 {
			s.rowCursor++
		}
	case "k", "up":
		if s.rowCursor > 0 {
			s.rowCursor--
		}
	case "a":
		s.openTaskEditor(-1)
		return s, textinput.Blink
	case "enter", "e":
		if s.rowCursor >= 0 && s.rowCursor < len(s.tasks) {
			s.openTaskEditor(s.rowCursor)
			return s, textinput.Blink
		}
	case "d", "x":
		if s.rowCursor >= 0 && s.rowCursor < len(s.tasks) {
			s.tasks = append(s.tasks[:s.rowCursor], s.tasks[s.rowCursor+1:]...)
			if s.rowCursor >= len(s.tasks) && s.rowCursor > 0 {
				s.rowCursor--
			}
		}
	}
	return s, nil
}

func (s *MaintenanceItemFormScreen) openTaskEditor(index int) {
	s.phase = mFormPhaseTaskEdit
	s.editIndex = index
	s.editCursor = 0
	s.editErr = ""
	if index >= 0 && index < len(s.tasks) {
		t := s.tasks[index]
		s.teTitle.SetValue(t.title)
		s.teDesc.SetValue(t.description)
		s.teRequired = t.isRequired
	} else {
		s.teTitle.SetValue("")
		s.teDesc.SetValue("")
		s.teRequired = true // model default
	}
	s.teTitle.Focus()
	s.teDesc.Blur()
}

func (s *MaintenanceItemFormScreen) updateTaskEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseTaskList
		return s, nil
	case "tab", "down":
		s.editCursor = (s.editCursor + 1) % 3
		s.syncTaskEditFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.editCursor = (s.editCursor + 2) % 3
		s.syncTaskEditFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitTaskEditor()
	}
	// Field 2 is the is_required toggle; space flips it.
	if s.editCursor == 2 {
		if m.String() == " " {
			s.teRequired = !s.teRequired
		}
		return s, nil
	}
	var cmd tea.Cmd
	if s.editCursor == 0 {
		s.teTitle, cmd = s.teTitle.Update(m)
	} else {
		s.teDesc, cmd = s.teDesc.Update(m)
	}
	return s, cmd
}

func (s *MaintenanceItemFormScreen) syncTaskEditFocus() {
	s.teTitle.Blur()
	s.teDesc.Blur()
	switch s.editCursor {
	case 0:
		s.teTitle.Focus()
	case 1:
		s.teDesc.Focus()
	}
}

func (s *MaintenanceItemFormScreen) commitTaskEditor() tea.Cmd {
	title := strings.TrimSpace(s.teTitle.Value())
	if title == "" {
		s.editErr = "task title is required"
		return nil
	}
	row := taskRow{
		title:       title,
		description: strings.TrimSpace(s.teDesc.Value()),
		isRequired:  s.teRequired,
	}
	if s.editIndex >= 0 && s.editIndex < len(s.tasks) {
		row.id = s.tasks[s.editIndex].id
		row.loadedOrder = s.tasks[s.editIndex].loadedOrder
		row.dirty = true
		s.tasks[s.editIndex] = row
	} else {
		s.tasks = append(s.tasks, row)
		s.rowCursor = len(s.tasks) - 1
	}
	s.phase = mFormPhaseTaskList
	return nil
}

// ---------------------------------------------------------------------------
// Material list + editor sub-phases
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) updateMaterialList(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseForm
		s.syncFocus()
	case "j", "down":
		if s.rowCursor < len(s.materials)-1 {
			s.rowCursor++
		}
	case "k", "up":
		if s.rowCursor > 0 {
			s.rowCursor--
		}
	case "a":
		s.openMaterialEditor(-1)
		return s, textinput.Blink
	case "enter", "e":
		if s.rowCursor >= 0 && s.rowCursor < len(s.materials) {
			s.openMaterialEditor(s.rowCursor)
			return s, textinput.Blink
		}
	case "d", "x":
		if s.rowCursor >= 0 && s.rowCursor < len(s.materials) {
			s.materials = append(s.materials[:s.rowCursor], s.materials[s.rowCursor+1:]...)
			if s.rowCursor >= len(s.materials) && s.rowCursor > 0 {
				s.rowCursor--
			}
		}
	}
	return s, nil
}

// materialEditInput maps the row-editor cursor to its input (nil for none).
func (s *MaintenanceItemFormScreen) materialEditInput(cursor int) *textinput.Model {
	switch cursor {
	case 0:
		return &s.meName
	case 1:
		return &s.meQty
	case 2:
		return &s.meUnit
	case 3:
		return &s.meCost
	case 4:
		return &s.meNotes
	}
	return nil
}

func (s *MaintenanceItemFormScreen) openMaterialEditor(index int) {
	s.phase = mFormPhaseMaterialEdit
	s.editIndex = index
	s.editCursor = 0
	s.editErr = ""
	if index >= 0 && index < len(s.materials) {
		mat := s.materials[index]
		s.meName.SetValue(mat.name)
		s.meQty.SetValue(mat.quantity)
		s.meUnit.SetValue(mat.unit)
		s.meCost.SetValue(mat.cost)
		s.meNotes.SetValue(mat.notes)
	} else {
		s.meName.SetValue("")
		s.meQty.SetValue("1")
		s.meUnit.SetValue("")
		s.meCost.SetValue("0")
		s.meNotes.SetValue("")
	}
	for _, in := range []*textinput.Model{&s.meName, &s.meQty, &s.meUnit, &s.meCost, &s.meNotes} {
		in.Blur()
	}
	s.meName.Focus()
}

func (s *MaintenanceItemFormScreen) updateMaterialEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseMaterialList
		return s, nil
	case "tab", "down":
		s.editCursor = (s.editCursor + 1) % 5
		s.syncMaterialEditFocus()
		return s, textinput.Blink
	case "shift+tab", "up":
		s.editCursor = (s.editCursor + 4) % 5
		s.syncMaterialEditFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitMaterialEditor()
	}
	if in := s.materialEditInput(s.editCursor); in != nil {
		var cmd tea.Cmd
		*in, cmd = in.Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *MaintenanceItemFormScreen) syncMaterialEditFocus() {
	for i, in := range []*textinput.Model{&s.meName, &s.meQty, &s.meUnit, &s.meCost, &s.meNotes} {
		if i == s.editCursor {
			in.Focus()
		} else {
			in.Blur()
		}
	}
}

func (s *MaintenanceItemFormScreen) commitMaterialEditor() tea.Cmd {
	name := strings.TrimSpace(s.meName.Value())
	if name == "" {
		s.editErr = "material name is required"
		return nil
	}
	qty, err := mfDecimalOrDefault(s.meQty.Value(), "1", "quantity")
	if err != nil {
		s.editErr = err.Error()
		return nil
	}
	cost, err := mfDecimalOrDefault(s.meCost.Value(), "0", "cost per unit")
	if err != nil {
		s.editErr = err.Error()
		return nil
	}
	row := materialRow{
		name:     name,
		quantity: qty,
		unit:     strings.TrimSpace(s.meUnit.Value()),
		cost:     cost,
		notes:    strings.TrimSpace(s.meNotes.Value()),
	}
	if s.editIndex >= 0 && s.editIndex < len(s.materials) {
		row.id = s.materials[s.editIndex].id
		row.dirty = true
		s.materials[s.editIndex] = row
	} else {
		s.materials = append(s.materials, row)
		s.rowCursor = len(s.materials) - 1
	}
	s.phase = mFormPhaseMaterialList
	return nil
}

// ---------------------------------------------------------------------------
// Submit + reconcile
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) buildItemPayload() (omsapi.MaintenanceItemWrite, error) {
	var w omsapi.MaintenanceItemWrite
	if s.assetID == "" {
		return w, errors.New("an asset is required")
	}
	title := strings.TrimSpace(s.inputs[mfTitle].Value())
	if title == "" {
		return w, errors.New("title is required")
	}
	interval, err := mfOptPositiveInt(s.inputs[mfIntervalDays].Value(), "interval (days)")
	if err != nil {
		return w, err
	}
	estTime, err := mfOptPositiveInt(s.inputs[mfEstTimeMin].Value(), "estimated time")
	if err != nil {
		return w, err
	}
	cost, err := mfDecimalOrDefault(s.inputs[mfEstCost].Value(), "0", "estimated cost")
	if err != nil {
		return w, err
	}
	w = omsapi.MaintenanceItemWrite{
		Asset:            s.assetID,
		Title:            title,
		Description:      strings.TrimSpace(s.inputs[mfDescription].Value()),
		Instructions:     strings.TrimSpace(s.inputs[mfInstructions].Value()),
		EstimatedTimeMin: estTime,
		EstimatedCost:    cost,
		IntervalDays:     interval,
		IsActive:         s.isActive,
	}
	return w, nil
}

func (s *MaintenanceItemFormScreen) submit() (Screen, tea.Cmd) {
	body, err := s.buildItemPayload()
	if err != nil {
		s.errMsg = err.Error()
		return s, Status(err.Error(), StatusError)
	}
	s.saving = true
	s.errMsg = ""

	deps := s.deps
	ctx := s.ctx()
	edit := s.edit
	itemID := s.itemID
	tasks := append([]taskRow(nil), s.tasks...)
	materials := append([]materialRow(nil), s.materials...)
	origTaskIDs := append([]string(nil), s.origTaskIDs...)
	origMaterialIDs := append([]string(nil), s.origMaterialIDs...)

	return s, func() tea.Msg {
		var id string
		if edit {
			if _, e := deps.OMS.UpdateMaintenanceItem(ctx, itemID, body); e != nil {
				return mFormSavedMsg{err: e}
			}
			id = itemID
		} else {
			created, e := deps.OMS.CreateMaintenanceItem(ctx, body)
			if e != nil {
				return mFormSavedMsg{err: e}
			}
			id = created.ID
		}
		if e := reconcileTasks(ctx, deps.OMS, id, tasks, origTaskIDs); e != nil {
			return mFormSavedMsg{itemID: id, err: e}
		}
		if e := reconcileMaterials(ctx, deps.OMS, id, materials, origMaterialIDs); e != nil {
			return mFormSavedMsg{itemID: id, err: e}
		}
		return mFormSavedMsg{itemID: id}
	}
}

// reconcileTasks deletes removed task rows, patches edited/moved ones, and
// creates new ones — assigning each surviving row an order equal to its
// position so the generated work order runs them in list order.
func reconcileTasks(ctx context.Context, oms *omsapi.Client, itemID string, rows []taskRow, origIDs []string) error {
	keep := map[string]bool{}
	for _, r := range rows {
		if r.id != "" {
			keep[r.id] = true
		}
	}
	for _, id := range origIDs {
		if !keep[id] {
			if err := oms.DeleteMaintenanceTask(ctx, id); err != nil {
				return err
			}
		}
	}
	for i, r := range rows {
		body := omsapi.MaintenanceTaskWrite{
			MaintenanceItem: itemID,
			Order:           i,
			Title:           r.title,
			Description:     r.description,
			IsRequired:      r.isRequired,
		}
		if r.id == "" {
			if _, err := oms.CreateMaintenanceTask(ctx, body); err != nil {
				return err
			}
			continue
		}
		if r.dirty || r.loadedOrder != i {
			if _, err := oms.UpdateMaintenanceTask(ctx, r.id, body); err != nil {
				return err
			}
		}
	}
	return nil
}

// reconcileMaterials deletes removed material rows, patches edited ones, and
// creates new ones.
func reconcileMaterials(ctx context.Context, oms *omsapi.Client, itemID string, rows []materialRow, origIDs []string) error {
	keep := map[string]bool{}
	for _, r := range rows {
		if r.id != "" {
			keep[r.id] = true
		}
	}
	for _, id := range origIDs {
		if !keep[id] {
			if err := oms.DeleteMaintenanceMaterial(ctx, id); err != nil {
				return err
			}
		}
	}
	for _, r := range rows {
		body := omsapi.MaintenanceMaterialWrite{
			MaintenanceItem:      itemID,
			Name:                 r.name,
			Quantity:             r.quantity,
			Unit:                 r.unit,
			EstimatedCostPerUnit: r.cost,
			Notes:                r.notes,
		}
		if r.id == "" {
			if _, err := oms.CreateMaintenanceMaterial(ctx, body); err != nil {
				return err
			}
			continue
		}
		if r.dirty {
			if _, err := oms.UpdateMaintenanceMaterial(ctx, r.id, body); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *MaintenanceItemFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.itemID != "" {
		return SwitchTo(WSMaintenance, NewMaintenanceItemDetailScreen(s.deps, s.itemID))
	}
	return SwitchTo(WSMaintenance, NewMaintenanceItemsScreen(s.deps))
}

// ---------------------------------------------------------------------------
// Parse helpers
// ---------------------------------------------------------------------------

// mfOptPositiveInt parses a nullable positive integer. Blank → nil (sent as
// JSON null to clear the field); otherwise it must be a whole number ≥ 1.
func mfOptPositiveInt(raw, label string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be a whole number", label)
	}
	if n < 1 {
		return nil, fmt.Errorf("%s must be at least 1", label)
	}
	return &n, nil
}

// mfDecimalOrDefault validates an optional decimal, returning def when blank.
// The raw string is returned verbatim on success so precision is preserved on
// the wire (the backend field is a DecimalField).
func mfDecimalOrDefault(raw, def, label string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return "", fmt.Errorf("%s must be a number", label)
	}
	if f < 0 {
		return "", fmt.Errorf("%s must be zero or greater", label)
	}
	return raw, nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	switch s.phase {
	case mFormPhaseAssetPick:
		return s.viewAssetPick()
	case mFormPhaseTaskList:
		return s.viewTaskList()
	case mFormPhaseTaskEdit:
		return s.viewTaskEdit()
	case mFormPhaseMaterialList:
		return s.viewMaterialList()
	case mFormPhaseMaterialEdit:
		return s.viewMaterialEdit()
	}
	return s.viewForm()
}

func (s *MaintenanceItemFormScreen) viewForm() string {
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

func (s *MaintenanceItemFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := mfLabel[id]

	var value string
	switch mfKind(id) {
	case mkText, mkNumber:
		value = s.inputs[id].View()
	case mkToggle:
		if s.isActive {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	case mkPicker:
		if s.assetName != "" {
			value = s.assetName
		} else if s.assetID != "" {
			value = s.assetID
		} else {
			value = StyleMuted.Render("(press space to pick — required)")
		}
	case mkSublist:
		n := len(s.tasks)
		noun := "step"
		if id == mfMaterials {
			n = len(s.materials)
			noun = "material"
		}
		value = fmt.Sprintf("%d %s", n, plural(noun, n)) + " " + StyleMuted.Render("(space to manage)")
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

func (s *MaintenanceItemFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch mfKind(id) {
		case mkToggle:
			kindHelp = "space toggle"
		case mkPicker:
			kindHelp = "space to pick"
		case mkSublist:
			kindHelp = "space/enter to manage"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

func (s *MaintenanceItemFormScreen) visibleRows() int {
	const chrome = 6
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *MaintenanceItemFormScreen) viewAssetPick() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render("Pick asset — j/k move · / filter · enter select · esc back") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
	}
	if len(s.assets) == 0 {
		b.WriteString(StyleMuted.Render("(no assets loaded)"))
		return b.String()
	}
	if len(s.pickOptions) == 0 {
		b.WriteString(StyleMuted.Render("(no matches)"))
		return b.String()
	}
	const window = 12
	start, end := fieldWindow(s.pickCursor, len(s.pickOptions), window)
	if start > 0 {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↑ %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		caret := "    "
		if i == s.pickCursor {
			caret = "  ▸ "
		}
		line := caret + s.pickOptions[i].label
		if i == s.pickCursor {
			line = StyleSidebarItemActive.Render(caret + s.pickOptions[i].label)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}

func (s *MaintenanceItemFormScreen) viewTaskList() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Task steps") + "  " + StyleMuted.Render("a add · e/enter edit · d remove · j/k move · esc back") + "\n\n")
	if len(s.tasks) == 0 {
		b.WriteString(StyleMuted.Render("(no task steps yet — press a to add)") + "\n")
		return b.String()
	}
	for i, t := range s.tasks {
		caret := "  "
		if i == s.rowCursor {
			caret = "▸ "
		}
		req := StyleMuted.Render("(optional)")
		if t.isRequired {
			req = StyleStatusOK.Render("(required)")
		}
		line := fmt.Sprintf("%s%d. %s %s", caret, i+1, t.title, req)
		if i == s.rowCursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
		if t.description != "" {
			b.WriteString("     " + StyleMuted.Render(t.description) + "\n")
		}
	}
	return b.String()
}

func (s *MaintenanceItemFormScreen) viewTaskEdit() string {
	var b strings.Builder
	verb := "Add"
	if s.editIndex >= 0 {
		verb = "Edit"
	}
	b.WriteString(StyleTitle.Render(verb+" task step") + "  " + StyleMuted.Render("tab/↑↓ move · enter save · esc back") + "\n\n")

	rows := []struct {
		label string
		value string
	}{
		{"Title", s.teTitle.View()},
		{"Description", s.teDesc.View()},
		{"Required", boolBadge(s.teRequired)},
	}
	for i, r := range rows {
		caret := "  "
		if i == s.editCursor {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(r.label+": ") + r.value + "\n")
	}
	if s.editCursor == 2 {
		b.WriteString("\n" + StyleMuted.Render("space toggles required") + "\n")
	}
	if s.editErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.editErr))
	}
	return b.String()
}

func (s *MaintenanceItemFormScreen) viewMaterialList() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Materials") + "  " + StyleMuted.Render("a add · e/enter edit · d remove · j/k move · esc back") + "\n\n")
	if len(s.materials) == 0 {
		b.WriteString(StyleMuted.Render("(no materials yet — press a to add)") + "\n")
		return b.String()
	}
	for i, mrow := range s.materials {
		caret := "  "
		if i == s.rowCursor {
			caret = "▸ "
		}
		qty := mrow.quantity
		if mrow.unit != "" {
			qty += " " + mrow.unit
		}
		line := fmt.Sprintf("%s%s — %s @ $%s", caret, mrow.name, qty, mrow.cost)
		if i == s.rowCursor {
			line = StyleSidebarItemActive.Render(line)
		}
		b.WriteString(line + "\n")
		if mrow.notes != "" {
			b.WriteString("     " + StyleMuted.Render(mrow.notes) + "\n")
		}
	}
	return b.String()
}

func (s *MaintenanceItemFormScreen) viewMaterialEdit() string {
	var b strings.Builder
	verb := "Add"
	if s.editIndex >= 0 {
		verb = "Edit"
	}
	b.WriteString(StyleTitle.Render(verb+" material") + "  " + StyleMuted.Render("tab/↑↓ move · enter save · esc back") + "\n\n")
	rows := []struct {
		label string
		value string
	}{
		{"Name", s.meName.View()},
		{"Quantity", s.meQty.View()},
		{"Unit", s.meUnit.View()},
		{"Cost per unit ($)", s.meCost.View()},
		{"Notes", s.meNotes.View()},
	}
	for i, r := range rows {
		caret := "  "
		if i == s.editCursor {
			caret = "▸ "
		}
		b.WriteString(caret + StyleTitle.Render(r.label+": ") + r.value + "\n")
	}
	if s.editErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.editErr))
	}
	return b.String()
}

func boolBadge(v bool) string {
	if v {
		return StyleStatusOK.Render("[x] yes")
	}
	return StyleMuted.Render("[ ] no")
}
