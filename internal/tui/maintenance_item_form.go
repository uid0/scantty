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
//
// Everything here renders through the columnar "JD Edwards" layer (jde_form.go,
// sc-h412/sc-dnhx): one right-aligned label column and a persistent action bar
// naming exactly the keys that apply. The sub-lists took sweep A's chain-editor
// fold, which is what retired their a/e/d/x/j/k — adding is a trailing
// "(add a …)" ROW you navigate to and open with Ctrl-E, and REMOVING is a row of
// the item's own editor, which is the only place the thing being dropped is on
// screen. Enter and Esc both leave a sub-list: its rows live in memory until the
// ITEM is saved, so neither writes anything and there is nothing to cancel.
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
	"github.com/charmbracelet/lipgloss"

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
	mfTools
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
	mFormPhaseToolList
	mFormPhaseToolEdit
)

// Rows of the task-step editor. The first four are what enter saves; the fifth
// is where "remove this step" lives now that the list has no letter for it — it
// sits with the step it removes, which is the only place the operator can see
// what they are about to drop. A step being ADDED has nothing to remove yet, so
// it does not get that row (see taskEditRows).
const (
	taskEditTitle = iota
	taskEditDesc
	taskEditRequired
	taskEditPhoto
	taskEditRemove
	taskEditFieldCount
)

// Rows of the material editor — same shape, with the remove row last.
const (
	materialEditName = iota
	materialEditQty
	materialEditUnit
	materialEditCost
	materialEditNotes
	materialEditRemove
	materialEditFieldCount
)

// Rows of the tool editor. Row 3 is the is_required toggle, which owns no input.
const (
	toolEditName = iota
	toolEditQty
	toolEditLoc
	toolEditRequired
	toolEditNotes
	toolEditRemove
	toolEditFieldCount
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
	mfTools:        "Tools required",
}

// mfHint carries what the placeholders used to say. A placeholder long enough
// to fill the input area leaves no underscores, so an empty green-screen row
// stops reading as empty; the note rides after the input instead. A columnar
// form marks what is REQUIRED rather than tagging everything else "(optional)".
var mfHint = map[int]string{
	mfAsset:        "required",
	mfTitle:        "required",
	mfIntervalDays: "blank = one-time / as-needed",
}

// mfWidth sizes the input areas that are not the default.
func mfWidth(id int) int {
	switch id {
	case mfTitle:
		return 40
	case mfDescription, mfInstructions:
		return 44
	case mfIntervalDays, mfEstTimeMin, mfEstCost:
		return 10
	}
	return 0
}

// mfBand groups the sheet into the sections a printed PM record would have.
// They must stay CONTIGUOUS in the rebuildFields order — a heading is drawn
// where its band starts.
type mfBand int

const (
	mfBandItem mfBand = iota
	mfBandSchedule
	mfBandChecklist
)

var mfBandLabel = map[mfBand]string{
	mfBandItem:      "PM item",
	mfBandSchedule:  "Schedule & estimates",
	mfBandChecklist: "Checklist",
}

func mfBandOf(id int) mfBand {
	switch id {
	case mfIntervalDays, mfEstTimeMin, mfEstCost, mfIsActive:
		return mfBandSchedule
	case mfTasks, mfMaterials, mfTools:
		return mfBandChecklist
	}
	return mfBandItem
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
	case mfTasks, mfMaterials, mfTools:
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
//
// The step's reference photo splits in two: refImageURL is what the server
// already holds (read-only, shown as text — the TUI renders no images), while
// refImagePath is a local file the operator picked THIS session and that save
// uploads. Nothing ever hydrates refImagePath, so a non-empty one is by
// definition a new pick — it doubles as the "re-upload this" flag, and a row
// left alone never re-sends its photo.
type taskRow struct {
	id           string
	title        string
	description  string
	isRequired   bool
	refImageURL  string
	refImagePath string
	loadedOrder  int
	dirty        bool
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

// toolRow is one in-memory MaintenanceTool. Quantity is an int (the backend
// field is a PositiveIntegerField, not the DecimalField a material uses), and
// locationHint stands in for a material's unit + cost.
type toolRow struct {
	id           string
	name         string
	quantity     int
	locationHint string
	isRequired   bool
	notes        string
	dirty        bool
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

	jdeScreen

	// Main text/number inputs, indexed by field id.
	inputs []textinput.Model

	isActive bool

	// Asset selection (asset is required).
	assetID   string
	assetName string

	// Task, material + tool rows, plus the ids present at load (for
	// delete-reconcile).
	tasks           []taskRow
	materials       []materialRow
	tools           []toolRow
	origTaskIDs     []string
	origMaterialIDs []string
	origToolIDs     []string

	// Visible main-field navigation.
	fields []int
	cursor int

	phase mFormPhase

	// Asset picker sub-phase.
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []assetPickOption

	// Task/material list sub-phase cursor.
	rowCursor int

	// Row-editor sub-phase (shared cursor; index -1 == adding a new row).
	editIndex  int
	editCursor int
	editErr    string
	// Task editor inputs (cursor 2 is the is_required toggle, not an input).
	teTitle    textinput.Model
	teDesc     textinput.Model
	teRequired bool
	teRefImage textinput.Model
	// Material editor inputs.
	meName  textinput.Model
	meQty   textinput.Model
	meUnit  textinput.Model
	meCost  textinput.Model
	meNotes textinput.Model
	// Tool editor inputs (cursor 3 is the is_required toggle, not an input).
	toName     textinput.Model
	toQty      textinput.Model
	toLoc      textinput.Model
	toNotes    textinput.Model
	toRequired bool
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

	for _, ti := range []*textinput.Model{&s.teTitle, &s.teDesc, &s.meName, &s.meQty, &s.meUnit, &s.meCost, &s.meNotes, &s.toName, &s.toQty, &s.toLoc, &s.toNotes} {
		*ti = textinput.New()
		ti.Prompt = ""
		ti.CharLimit = 200
	}
	// The reference-photo field holds a filesystem path, so it needs the longer
	// limit the asset form's manual-PDF path input uses. What its placeholder
	// said now rides after the input area as a hint (see viewTaskEdit).
	s.teRefImage = textinput.New()
	s.teRefImage.Prompt = ""
	s.teRefImage.CharLimit = 512

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

// mfPlaceholder keeps only the placeholders that show a DEFAULT — the rest
// moved to mfHint, because a placeholder filling the input area hides the
// underscores that say the field is empty.
func mfPlaceholder(id int) string {
	if id == mfEstCost {
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
		s.setSize(m)
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
		case mFormPhaseToolList:
			return s.updateToolList(m)
		case mFormPhaseToolEdit:
			return s.updateToolEdit(m)
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
		case taskEditTitle:
			s.teTitle, cmd = s.teTitle.Update(msg)
		case taskEditDesc:
			s.teDesc, cmd = s.teDesc.Update(msg)
		case taskEditPhoto:
			s.teRefImage, cmd = s.teRefImage.Update(msg)
		}
	case mFormPhaseMaterialEdit:
		if in := s.materialEditInput(s.editCursor); in != nil {
			*in, cmd = in.Update(msg)
		}
	case mFormPhaseToolEdit:
		if in := s.toolEditInput(s.editCursor); in != nil {
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
			refImageURL: t.ReferenceImageURL,
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

	for _, t := range it.Tools {
		s.tools = append(s.tools, toolRow{
			id:           t.ID,
			name:         t.Name,
			quantity:     t.Quantity,
			locationHint: t.LocationHint,
			isRequired:   t.IsRequired,
			notes:        t.Notes,
		})
		s.origToolIDs = append(s.origToolIDs, t.ID)
	}
}

func (s *MaintenanceItemFormScreen) rebuildFields() {
	var focused = -1
	if id, ok := s.currentFieldID(); ok {
		focused = id
	}
	// No conditional sections in this form — every main field is always shown.
	s.fields = []int{mfAsset, mfTitle, mfDescription, mfInstructions, mfIntervalDays, mfEstTimeMin, mfEstCost, mfIsActive, mfTasks, mfMaterials, mfTools}
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
		// Enter SAVES from every row of the sheet now — Ctrl-E is what opens
		// the sub-lists, so there is no row left where enter means something
		// else (sc-dnhx did the same to the item↔supplier link's picker row).
		return s.submit()
	case "ctrl+e":
		// EDIT opens whatever the highlighted row IS.
		if id, ok := s.currentFieldID(); ok {
			switch mfKind(id) {
			case mkPicker:
				s.openAssetPick()
				return s, textinput.Blink
			case mkSublist:
				s.openSublist(id)
				return s, textinput.Blink
			}
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch mfKind(id) {
	case mkToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.isActive = !s.isActive
		}
		return s, nil
	case mkPicker, mkSublist:
		// These rows have nothing to type into and no accelerators left.
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *MaintenanceItemFormScreen) moveCursor(delta int) {
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
func (s *MaintenanceItemFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *MaintenanceItemFormScreen) openSublist(id int) {
	switch id {
	case mfMaterials:
		s.phase = mFormPhaseMaterialList
	case mfTools:
		s.phase = mFormPhaseToolList
	default:
		s.phase = mFormPhaseTaskList
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
	s.pickSearch.SetValue("")
	s.syncBlurAll()
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closeAssetPick()
	case jdePickCommit:
		if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
			opt := s.pickOptions[s.pickCursor]
			s.assetID = opt.id
			s.assetName = opt.label
		}
		s.closeAssetPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		if next, ok := s.pageRow(body, s.pickCursor, len(s.pickOptions), delta, len(header),
			s.pickBar(header, body), jdePickBarCeiling("Select", "Cancel")); ok {
			s.pickCursor = next
		}
	default:
		// Anything else is filter text: the box is always live, so there is no
		// mode to enter and no "/" to remember.
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyAssetFilter()
		return s, cmd
	}
	return s, nil
}

// movePick walks the option cursor, clamping at both ends — a picker list is a
// set of choices, not a ring.
func (s *MaintenanceItemFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *MaintenanceItemFormScreen) closeAssetPick() {
	s.phase = mFormPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Task list + editor sub-phases
// ---------------------------------------------------------------------------

// updateSublist drives every sub-list on the reduced key scheme: Up/Down move
// (the last row is always the "(add …)" one), Ctrl-E opens whatever the row IS,
// and Enter or Esc are both done — the rows live in memory until the ITEM is
// saved, so leaving writes nothing either way and there is nothing to cancel.
//
// Removing moved into the row's OWN editor, next to the row it drops; a list of
// one-line summaries is the worst place to confirm a delete from.
func (s *MaintenanceItemFormScreen) updateSublist(m tea.KeyMsg, count int, open func(index int)) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc", "enter":
		s.closeSublist()
	case "down", "tab":
		s.moveSublist(count, +1)
	case "up", "shift+tab":
		s.moveSublist(count, -1)
	case "pgdown":
		s.pageSublist(count, +1)
	case "pgup":
		s.pageSublist(count, -1)
	case "ctrl+e":
		if s.rowCursor >= count {
			open(-1) // the trailing add row
		} else {
			open(s.rowCursor)
		}
		return s, textinput.Blink
	}
	return s, nil
}

// moveSublist walks the sub-list cursor. It is a LIST cursor, so it clamps
// rather than wrapping, and it DECLINES on a pane the frame is not drawn into —
// the highlight it would move is not on screen to be seen. count excludes the
// add row, which is always the row after the last one.
func (s *MaintenanceItemFormScreen) moveSublist(count, delta int) {
	body := s.sublistBody()
	next, ok := s.pickRow(s.rowCursor, count+1, delta, 0, s.sublistBarNow(body))
	if !ok {
		return
	}
	s.rowCursor = next
}

// moveEditCursor walks a sub-editor's field cursor, WRAPPING (it is a short
// field form, not a list) and declining on a pane the editor is not drawn into.
// The bar it measures is the one drawn on the frame the key was pressed on.
func (s *MaintenanceItemFormScreen) moveEditCursor(n, delta int, noun string, canRemove bool) bool {
	next, ok := s.moveRow(s.editCursor, n, delta, 0, s.editorBar(noun, canRemove))
	if !ok {
		return false
	}
	s.editCursor = next
	return true
}

// pageSublist moves a pane's worth of rows, clamping. count excludes the add
// row, which is always reachable as the row after the last one.
func (s *MaintenanceItemFormScreen) pageSublist(count, dir int) {
	body := s.sublistBody()
	next, ok := s.pageRow(body, s.rowCursor, count+1, dir, 0,
		s.sublistBarNow(body), s.sublistBarNowCeiling())
	if !ok {
		return
	}
	s.rowCursor = next
}

// sublistBarNow is the bar of whichever sub-list is open — the same bar the
// view builds, chosen off the same phase sublistBody reads, so a page is
// measured against the frame it is being made on.
func (s *MaintenanceItemFormScreen) sublistBarNow(body *jdeLines) []actionBarItem {
	count, noun, addVerb := s.sublistScope()
	return s.sublistBar(body, count, noun, addVerb)
}

// sublistBarNowCeiling is that same bar WITH EVERY MOVEMENT KEY on it — the
// fixed point pageRow measures the scroll question against, for the reason its
// doc gives: naming the keys costs cells, and cells can fold the bar onto
// another row. UP/DN can now come OFF a bar (jdeRowMoves), so the ceiling has to
// name it back or a one-row sub-list would budget against a bar shorter than the
// one a second row restores.
func (s *MaintenanceItemFormScreen) sublistBarNowCeiling() []actionBarItem {
	count, noun, addVerb := s.sublistScope()
	return s.sublistBarItems(count, noun, addVerb, jdeCeilingRows, true)
}

// sublistScope is which sub-list is open, said ONCE: the row count and the two
// words its bar is built from. Both bars above read it, so the bar that is DRAWN
// and the ceiling it is measured against can never describe different sub-lists
// — which two copies of this switch would eventually do.
func (s *MaintenanceItemFormScreen) sublistScope() (int, string, string) {
	switch s.phase {
	case mFormPhaseMaterialList:
		return len(s.materials), "Materials", "Add a material"
	case mFormPhaseToolList:
		return len(s.tools), "Tools", "Add a tool"
	}
	return len(s.tasks), "Steps", "Add a step"
}

func (s *MaintenanceItemFormScreen) closeSublist() {
	s.phase = mFormPhaseForm
	s.syncFocus()
}

func (s *MaintenanceItemFormScreen) updateTaskList(m tea.KeyMsg) (Screen, tea.Cmd) {
	return s.updateSublist(m, len(s.tasks), s.openTaskEditor)
}

// removeTask drops a step. Removing is immediate, as it always was: nothing is
// written until the item is saved, so there is nothing for a confirm to protect.
func (s *MaintenanceItemFormScreen) removeTask(index int) {
	if index < 0 || index >= len(s.tasks) {
		return
	}
	s.tasks = append(s.tasks[:index:index], s.tasks[index+1:]...)
	s.clampRowCursor(len(s.tasks))
}

// clampRowCursor keeps the list cursor inside the rows plus the add row.
func (s *MaintenanceItemFormScreen) clampRowCursor(count int) {
	if s.rowCursor > count {
		s.rowCursor = count
	}
	if s.rowCursor < 0 {
		s.rowCursor = 0
	}
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
		// The path, not the URL: re-showing the picked file lets the operator
		// correct a typo, while an already-uploaded photo stays a display-only
		// URL under the field (clearing it is a web/admin job, not a TUI one).
		s.teRefImage.SetValue(t.refImagePath)
	} else {
		s.teTitle.SetValue("")
		s.teDesc.SetValue("")
		s.teRequired = true // model default
		s.teRefImage.SetValue("")
	}
	s.syncTaskEditFocus()
}

// taskEditRows is how many rows the editor offers: a step being ADDED has
// nothing to remove yet, so it has no remove row.
func (s *MaintenanceItemFormScreen) taskEditRows() int {
	if s.editIndex < 0 || s.editIndex >= len(s.tasks) {
		return taskEditFieldCount - 1
	}
	return taskEditFieldCount
}

func (s *MaintenanceItemFormScreen) updateTaskEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := s.taskEditRows()
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseTaskList
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		if !s.moveEditCursor(n, delta, "step", s.editCursor == taskEditRemove && s.editIndex >= 0) {
			return s, nil
		}
		s.syncTaskEditFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitTaskEditor()
	case "ctrl+e":
		if s.editCursor == taskEditRemove && s.editIndex >= 0 {
			index := s.editIndex
			s.phase = mFormPhaseTaskList
			s.blurTaskEdit()
			s.removeTask(index)
		}
		return s, nil
	}
	// The required row is a two-value choice: ←/→ flips it (space stays as the
	// pilot's synonym).
	if s.editCursor == taskEditRequired {
		switch m.String() {
		case " ", "left", "right":
			s.teRequired = !s.teRequired
		}
		return s, nil
	}
	var cmd tea.Cmd
	switch s.editCursor {
	case taskEditTitle:
		s.teTitle, cmd = s.teTitle.Update(m)
	case taskEditDesc:
		s.teDesc, cmd = s.teDesc.Update(m)
	case taskEditPhoto:
		s.teRefImage, cmd = s.teRefImage.Update(m)
	}
	return s, cmd
}

func (s *MaintenanceItemFormScreen) blurTaskEdit() {
	s.teTitle.Blur()
	s.teDesc.Blur()
	s.teRefImage.Blur()
}

func (s *MaintenanceItemFormScreen) syncTaskEditFocus() {
	s.blurTaskEdit()
	switch s.editCursor {
	case taskEditTitle:
		s.teTitle.Focus()
	case taskEditDesc:
		s.teDesc.Focus()
	case taskEditPhoto:
		s.teRefImage.Focus()
	}
}

func (s *MaintenanceItemFormScreen) commitTaskEditor() tea.Cmd {
	title := strings.TrimSpace(s.teTitle.Value())
	if title == "" {
		s.editErr = "task title is required"
		return nil
	}
	// Fail fast on a bad path here rather than at save time, when the item
	// write has already gone out and only the step reconcile would fail.
	refPath, err := validateImagePath(expandUser(strings.TrimSpace(s.teRefImage.Value())), "reference photo")
	if err != nil {
		s.editErr = err.Error()
		return nil
	}
	row := taskRow{
		title:        title,
		description:  strings.TrimSpace(s.teDesc.Value()),
		isRequired:   s.teRequired,
		refImagePath: refPath,
	}
	if s.editIndex >= 0 && s.editIndex < len(s.tasks) {
		row.id = s.tasks[s.editIndex].id
		row.loadedOrder = s.tasks[s.editIndex].loadedOrder
		// Carry the already-uploaded photo's URL across the edit — it is a
		// server-side read field the editor never touches.
		row.refImageURL = s.tasks[s.editIndex].refImageURL
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
	return s.updateSublist(m, len(s.materials), s.openMaterialEditor)
}

// removeMaterial drops a material row (see removeTask).
func (s *MaintenanceItemFormScreen) removeMaterial(index int) {
	if index < 0 || index >= len(s.materials) {
		return
	}
	s.materials = append(s.materials[:index:index], s.materials[index+1:]...)
	s.clampRowCursor(len(s.materials))
}

// materialEditInput maps the row-editor cursor to its input (nil for none).
func (s *MaintenanceItemFormScreen) materialEditInput(cursor int) *textinput.Model {
	switch cursor {
	case materialEditName:
		return &s.meName
	case materialEditQty:
		return &s.meQty
	case materialEditUnit:
		return &s.meUnit
	case materialEditCost:
		return &s.meCost
	case materialEditNotes:
		return &s.meNotes
	}
	return nil
}

// materialEditRows is how many rows the editor offers: a material being ADDED
// has nothing to remove yet.
func (s *MaintenanceItemFormScreen) materialEditRows() int {
	if s.editIndex < 0 || s.editIndex >= len(s.materials) {
		return materialEditFieldCount - 1
	}
	return materialEditFieldCount
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
	s.syncMaterialEditFocus()
}

func (s *MaintenanceItemFormScreen) updateMaterialEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := s.materialEditRows()
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseMaterialList
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		if !s.moveEditCursor(n, delta, "material", s.editCursor == materialEditRemove && s.editIndex >= 0) {
			return s, nil
		}
		s.syncMaterialEditFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitMaterialEditor()
	case "ctrl+e":
		if s.editCursor == materialEditRemove && s.editIndex >= 0 {
			index := s.editIndex
			s.phase = mFormPhaseMaterialList
			s.blurMaterialEdit()
			s.removeMaterial(index)
		}
		return s, nil
	}
	if in := s.materialEditInput(s.editCursor); in != nil {
		var cmd tea.Cmd
		*in, cmd = in.Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *MaintenanceItemFormScreen) blurMaterialEdit() {
	for _, in := range []*textinput.Model{&s.meName, &s.meQty, &s.meUnit, &s.meCost, &s.meNotes} {
		in.Blur()
	}
}

func (s *MaintenanceItemFormScreen) syncMaterialEditFocus() {
	s.blurMaterialEdit()
	if in := s.materialEditInput(s.editCursor); in != nil {
		in.Focus()
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
// Tool list + editor sub-phases
// ---------------------------------------------------------------------------

func (s *MaintenanceItemFormScreen) updateToolList(m tea.KeyMsg) (Screen, tea.Cmd) {
	return s.updateSublist(m, len(s.tools), s.openToolEditor)
}

// removeTool drops a tool row (see removeTask).
func (s *MaintenanceItemFormScreen) removeTool(index int) {
	if index < 0 || index >= len(s.tools) {
		return
	}
	s.tools = append(s.tools[:index:index], s.tools[index+1:]...)
	s.clampRowCursor(len(s.tools))
}

// toolEditInput maps the row-editor cursor to its input. toolEditRequired is
// the is_required toggle, which owns no input, so it returns nil there.
func (s *MaintenanceItemFormScreen) toolEditInput(cursor int) *textinput.Model {
	switch cursor {
	case toolEditName:
		return &s.toName
	case toolEditQty:
		return &s.toQty
	case toolEditLoc:
		return &s.toLoc
	case toolEditNotes:
		return &s.toNotes
	}
	return nil
}

// toolEditRows is how many rows the editor offers: a tool being ADDED has
// nothing to remove yet.
func (s *MaintenanceItemFormScreen) toolEditRows() int {
	if s.editIndex < 0 || s.editIndex >= len(s.tools) {
		return toolEditFieldCount - 1
	}
	return toolEditFieldCount
}

func (s *MaintenanceItemFormScreen) openToolEditor(index int) {
	s.phase = mFormPhaseToolEdit
	s.editIndex = index
	s.editCursor = 0
	s.editErr = ""
	if index >= 0 && index < len(s.tools) {
		t := s.tools[index]
		s.toName.SetValue(t.name)
		s.toQty.SetValue(strconv.Itoa(t.quantity))
		s.toLoc.SetValue(t.locationHint)
		s.toNotes.SetValue(t.notes)
		s.toRequired = t.isRequired
	} else {
		s.toName.SetValue("")
		s.toQty.SetValue("1") // model default
		s.toLoc.SetValue("")
		s.toNotes.SetValue("")
		s.toRequired = true // model default
	}
	s.syncToolEditFocus()
}

func (s *MaintenanceItemFormScreen) updateToolEdit(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := s.toolEditRows()
	switch m.String() {
	case "esc":
		s.phase = mFormPhaseToolList
		return s, nil
	case "tab", "down", "shift+tab", "up":
		delta := +1
		if m.String() == "shift+tab" || m.String() == "up" {
			delta = -1
		}
		if !s.moveEditCursor(n, delta, "tool", s.editCursor == toolEditRemove && s.editIndex >= 0) {
			return s, nil
		}
		s.syncToolEditFocus()
		return s, textinput.Blink
	case "enter":
		return s, s.commitToolEditor()
	case "ctrl+e":
		if s.editCursor == toolEditRemove && s.editIndex >= 0 {
			index := s.editIndex
			s.phase = mFormPhaseToolList
			s.blurToolEdit()
			s.removeTool(index)
		}
		return s, nil
	}
	// The required row is a two-value choice: ←/→ flips it (space stays as the
	// pilot's synonym).
	if s.editCursor == toolEditRequired {
		switch m.String() {
		case " ", "left", "right":
			s.toRequired = !s.toRequired
		}
		return s, nil
	}
	if in := s.toolEditInput(s.editCursor); in != nil {
		var cmd tea.Cmd
		*in, cmd = in.Update(m)
		return s, cmd
	}
	return s, nil
}

func (s *MaintenanceItemFormScreen) blurToolEdit() {
	for _, in := range []*textinput.Model{&s.toName, &s.toQty, &s.toLoc, &s.toNotes} {
		in.Blur()
	}
}

func (s *MaintenanceItemFormScreen) syncToolEditFocus() {
	s.blurToolEdit()
	if in := s.toolEditInput(s.editCursor); in != nil {
		in.Focus()
	}
}

func (s *MaintenanceItemFormScreen) commitToolEditor() tea.Cmd {
	name := strings.TrimSpace(s.toName.Value())
	if name == "" {
		s.editErr = "tool name is required"
		return nil
	}
	// Blank quantity falls back to the model default of 1.
	qty := 1
	if n, err := mfOptPositiveInt(s.toQty.Value(), "quantity"); err != nil {
		s.editErr = err.Error()
		return nil
	} else if n != nil {
		qty = *n
	}
	row := toolRow{
		name:         name,
		quantity:     qty,
		locationHint: strings.TrimSpace(s.toLoc.Value()),
		isRequired:   s.toRequired,
		notes:        strings.TrimSpace(s.toNotes.Value()),
	}
	if s.editIndex >= 0 && s.editIndex < len(s.tools) {
		row.id = s.tools[s.editIndex].id
		row.dirty = true
		s.tools[s.editIndex] = row
	} else {
		s.tools = append(s.tools, row)
		s.rowCursor = len(s.tools) - 1
	}
	s.phase = mFormPhaseToolList
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
	tools := append([]toolRow(nil), s.tools...)
	origTaskIDs := append([]string(nil), s.origTaskIDs...)
	origMaterialIDs := append([]string(nil), s.origMaterialIDs...)
	origToolIDs := append([]string(nil), s.origToolIDs...)

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
		if e := reconcileTools(ctx, deps.OMS, id, tools, origToolIDs); e != nil {
			return mFormSavedMsg{itemID: id, err: e}
		}
		return mFormSavedMsg{itemID: id}
	}
}

// reconcileTasks deletes removed task rows, patches edited/moved ones, and
// creates new ones — assigning each surviving row an order equal to its
// position so the generated work order runs them in list order.
//
// A row carrying a picked reference photo sends its path through to the write,
// which turns that one call into a multipart upload. Rows the operator did not
// touch have an empty path and so stay JSON, leaving any photo already on the
// step alone (a PATCH without the key can't clear it).
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
			MaintenanceItem:    itemID,
			Order:              i,
			Title:              r.title,
			Description:        r.description,
			IsRequired:         r.isRequired,
			ReferenceImagePath: r.refImagePath,
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

// reconcileTools deletes removed tool rows, patches edited ones, and creates
// new ones — the material reconcile, for the tools sub-resource.
func reconcileTools(ctx context.Context, oms *omsapi.Client, itemID string, rows []toolRow, origIDs []string) error {
	keep := map[string]bool{}
	for _, r := range rows {
		if r.id != "" {
			keep[r.id] = true
		}
	}
	for _, id := range origIDs {
		if !keep[id] {
			if err := oms.DeleteMaintenanceTool(ctx, id); err != nil {
				return err
			}
		}
	}
	for _, r := range rows {
		body := omsapi.MaintenanceToolWrite{
			MaintenanceItem: itemID,
			Name:            r.name,
			Quantity:        r.quantity,
			LocationHint:    r.locationHint,
			IsRequired:      r.isRequired,
			Notes:           r.notes,
		}
		if r.id == "" {
			if _, err := oms.CreateMaintenanceTool(ctx, body); err != nil {
				return err
			}
			continue
		}
		if r.dirty {
			if _, err := oms.UpdateMaintenanceTool(ctx, r.id, body); err != nil {
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
	case mFormPhaseToolList:
		return s.viewToolList()
	case mFormPhaseToolEdit:
		return s.viewToolEdit()
	}
	return s.viewForm()
}

func (s *MaintenanceItemFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the visible fields as columnar rows: the active flag is a
// bounded set, the asset row and the three sub-lists show what they hold today
// and are opened with Ctrl-E.
func (s *MaintenanceItemFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   mfLabel[id],
			Width:   mfWidth(id),
			Hint:    mfHint[id],
			Focused: i == s.cursor,
		}
		switch mfKind(id) {
		case mkToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.isActive)
		case mkPicker:
			value, dim := s.assetValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		case mkSublist:
			value, dim := s.sublistValue(id)
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E manages"
			}
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

func (s *MaintenanceItemFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	labelWidth := jdeLabelWidth(fields)

	l := &jdeLines{}
	band := mfBand(-1)
	for i, id := range s.fields {
		if b := mfBandOf(id); b != band {
			if band != mfBand(-1) {
				l.Add("")
			}
			l.Add(StyleJDEHeading.Render(mfBandLabel[b]))
			band = b
		}
		l.AddFittedField(i, fields[i], labelWidth, s.bodyWidth())
	}
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *MaintenanceItemFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *MaintenanceItemFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch mfKind(id) {
		case mkToggle:
			items = append(items, actionBarItem{"←→", "Change"})
		case mkPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		case mkSublist:
			items = append(items, actionBarItem{"Ctrl-E", "Manage"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// assetValue is the asset row's text, and whether it is an empty state rather
// than a value. PLAIN text plus a flag, so a focused row can reverse-video the
// whole field without an inner reset sequence cutting the highlight short.
func (s *MaintenanceItemFormScreen) assetValue() (string, bool) {
	switch {
	case s.assetName != "":
		return s.assetName, false
	case s.assetID != "":
		return s.assetID, false
	}
	return "(not set)", true
}

// sublistValue is a sub-list row's summary: how many rows it holds.
func (s *MaintenanceItemFormScreen) sublistValue(id int) (string, bool) {
	n, noun := len(s.tasks), "step"
	switch id {
	case mfMaterials:
		n, noun = len(s.materials), "material"
	case mfTools:
		n, noun = len(s.tools), "tool"
	}
	if n == 0 {
		return "(none)", true
	}
	return fmt.Sprintf("%d %s", n, plural(noun, n)), false
}

func plural(noun string, n int) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// pickView builds the asset picker's pinned header and its option list.
func (s *MaintenanceItemFormScreen) pickView() (jdeHeader, *jdeLines) {
	empty := "(no matches)"
	if len(s.assets) == 0 {
		empty = "(no assets loaded)"
	}
	return jdePickList{
		Title:  "Asset",
		For:    strings.TrimSpace(s.inputs[mfTitle].Value()),
		Note:   "The asset this preventive-maintenance item is scheduled against.",
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Cursor: s.pickCursor,
		Empty:  empty,
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *MaintenanceItemFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", len(s.pickOptions), s.bodyPagesForBar(body, len(s.pickOptions), len(header), jdePickBarCeiling("Select", "Cancel")))
}

func (s *MaintenanceItemFormScreen) viewAssetPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}

// ---------------------------------------------------------------------------
// The sub-lists
// ---------------------------------------------------------------------------

// mfSublistRow is one row of a sub-list as the renderer needs it: the summary
// line, plus any detail lines that belong to the SAME navigable row (so the
// window keeps a row's whole block on screen rather than its first line). It is
// held as its PARTS rather than as one rendered string, so the pane can be
// fitted without cutting through an escape sequence.
//
// Every part of it is one of exactly two things, which is the rule this package
// already applies to a picker row and a report table: `name` is the IDENTIFIER
// and abbreviates with a mark; `facts` are drawn after it and NEVER give. The
// row was `fmt.Sprintf("%d. %s %s", i+1, title, req)` written straight out, so
// at 80 columns — the width this interface is designed to — an ordinary step
// title drew `▸ 1. Drain the sump and check the filter screen (required)` at 62
// cells against 47, and clampToBox took `(required)` off the end with no mark:
// a step that MUST be signed off reading as optional.
//
// What is NOT a fact is anything OMS supplies whose length nobody controls — a
// tool's location hint, a step's description — which is why those are detail
// lines of their own rather than a tail on this row. A bound expressed in terms
// of an unbounded value is not a bound.
type mfSublistRow struct {
	name   string
	facts  []jdeToken
	detail []mfDetailLine
}

// mfDetailLine is a continuation line under a sub-list row, held as its styled
// parts for the same reason: a bound applied to RENDERED text cuts through an
// escape sequence and takes the closing reset with it, colouring everything
// drawn afterwards.
type mfDetailLine []jdeToken

// render draws the line into `room` cells (0 meaning the pane is not sized yet,
// which everywhere in this layer means "do not truncate"), marking the cut where
// it makes one. The parts are clipped as PLAIN text and styled afterwards, and
// the mark is drawn outside every styled span.
func (d mfDetailLine) render(room int) string {
	plain := ""
	for _, tok := range d {
		plain += tok.text
	}
	if room <= 0 || lipgloss.Width(plain) <= room {
		out := ""
		for _, tok := range d {
			out += tok.render()
		}
		return out
	}
	budget, out := room-1, ""
	for _, tok := range d {
		if budget <= 0 {
			break
		}
		text := cellPrefix(tok.text, budget)
		budget -= lipgloss.Width(text)
		out += tok.style.Render(text)
	}
	return out + StyleMuted.Render(paneCutMark)
}

// line assembles the row into `room` cells: the facts keep their room and the
// name abbreviates into what is left. `room` of 0 is "not sized yet", so nothing
// is bounded.
func (r mfSublistRow) line(room int) string {
	facts, factsWidth := r.renderFacts(0)
	name := r.name
	if room > 0 {
		if facts != "" && factsWidth+2 > room {
			if room < 3 {
				return StyleMuted.Render(paneCutMark)
			}
			facts, factsWidth = r.renderFacts(room - 2)
		}
		avail := room - factsWidth
		if factsWidth > 0 {
			avail--
		}
		if avail < 1 {
			avail = 1
		}
		name = fitCell(name, avail)
	}
	if facts == "" {
		return name
	}
	return name + " " + facts
}

func (r mfSublistRow) renderFacts(room int) (string, int) {
	plain := ""
	for _, tok := range r.facts {
		if plain != "" {
			plain += " "
		}
		plain += tok.text
	}
	if room > 0 && lipgloss.Width(plain) > room {
		fitted := fitFactCell(plain, room)
		return StyleMuted.Render(fitted), lipgloss.Width(fitted)
	}
	out := ""
	for _, tok := range r.facts {
		if out != "" {
			out += " "
		}
		out += tok.render()
	}
	return out, lipgloss.Width(plain)
}

// mfSublistRoom is what a sub-list line has left once its lead-in is spent.
// A bodyWidth of 0 is "the pane is not sized yet", which everywhere in this
// layer means "do not truncate", and is passed straight through as 0.
func mfSublistRoom(bodyWidth, lead int) int {
	if bodyWidth <= 0 {
		return 0
	}
	if room := bodyWidth - lead; room > 0 {
		return room
	}
	return 1
}

// sublistLines lays a sub-list out: a heading, its rows, and the trailing
// "(add …)" row that replaced the `a` key.
func (s *MaintenanceItemFormScreen) sublistLines(heading, empty, addLabel string, rows []mfSublistRow) *jdeLines {
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render(heading))
	l.Add("")
	if len(rows) == 0 {
		for _, line := range jdeCaveatLines(empty, s.bodyWidth()) {
			l.Add(line)
		}
	}
	for i, r := range rows {
		// The highlight's own padding is reserved on EVERY row, not just the
		// highlighted one: a row that fits until it is selected is cut on
		// exactly the keypress that selects it.
		room := mfSublistRoom(s.bodyWidth(), len("  ▸ ")+
			StyleSidebarItemActive.GetHorizontalPadding())
		if i == s.rowCursor {
			l.AddRow(i, StyleSidebarItemActive.Render("  ▸ "+r.line(room)))
		} else {
			l.AddRow(i, "    "+r.line(room))
		}
		for _, d := range r.detail {
			l.AddRow(i, "      "+d.render(mfSublistRoom(s.bodyWidth(), len("      "))))
		}
	}
	// The add row is the last navigable row, always present: adding is
	// something you move to, not a letter you have to have memorised.
	if s.rowCursor >= len(rows) {
		l.AddRow(len(rows), StyleSidebarItemActive.Render("  ▸ "+addLabel))
	} else {
		l.AddRow(len(rows), "    "+StyleMuted.Render(addLabel))
	}
	return l
}

// sublistBar names the keys that apply. Enter and Esc are both done: the rows
// are written with the ITEM, so leaving the list writes nothing either way.
func (s *MaintenanceItemFormScreen) sublistBar(body *jdeLines, count int, noun, addVerb string) []actionBarItem {
	return s.sublistBarItems(count, noun, addVerb, count+1,
		s.bodyPagesForBar(body, count+1, 0, s.sublistBarItems(count, noun, addVerb, jdeCeilingRows, true)))
}

// sublistBarItems is sublistBar for a given paging state, so the bar that is
// MEASURED against the pane is the bar that is drawn on it.
// `moveRows` is the NAVIGABLE row count and `count` is the row count, and they
// are two arguments because a ceiling bar has to force the movement pair on
// without also flipping the Ctrl-E label from "Edit" to the add verb — the two
// are different widths, and a ceiling measured with the wrong one is not the
// tallest bar it claims to be. The cursor also stands on the trailing add row,
// so the live caller passes count+1, the same +1 sublistBar hands
// bodyPagesForBar.
func (s *MaintenanceItemFormScreen) sublistBarItems(count int, noun, addVerb string, moveRows int, paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Done"}, {"Esc", "Done"}}
	items = append(items, jdeMoveItem(noun, moveRows)...)
	if s.rowCursor >= count {
		items = append(items, actionBarItem{"Ctrl-E", addVerb})
	} else {
		items = append(items, actionBarItem{"Ctrl-E", "Edit"})
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// sublistBody is the lines of whichever sub-list is open — what paging measures
// a page against.
func (s *MaintenanceItemFormScreen) sublistBody() *jdeLines {
	switch s.phase {
	case mFormPhaseMaterialList:
		return s.materialListLines()
	case mFormPhaseToolList:
		return s.toolListLines()
	}
	return s.taskListLines()
}

func (s *MaintenanceItemFormScreen) taskListLines() *jdeLines {
	rows := make([]mfSublistRow, 0, len(s.tasks))
	for i, t := range s.tasks {
		req := jdeToken{text: "(optional)", style: StyleMuted}
		if t.isRequired {
			req = jdeToken{text: "(required)", style: StyleStatusOK}
		}
		r := mfSublistRow{name: fmt.Sprintf("%d. %s", i+1, t.title), facts: []jdeToken{req}}
		if t.description != "" {
			r.detail = append(r.detail, mfDetailLine{{text: t.description, style: StyleMuted}})
		}
		if p := taskRefPhotoLine(t); len(p) > 0 {
			r.detail = append(r.detail, p)
		}
		rows = append(rows, r)
	}
	return s.sublistLines("Task steps", "(no task steps yet)", "(add a task step)", rows)
}

func (s *MaintenanceItemFormScreen) viewTaskList() string {
	body := s.taskListLines()
	return s.frame(body, s.rowCursor, "", s.sublistBar(body, len(s.tasks), "Steps", "Add a step"))
}

// taskRefPhotoLine describes a step's reference photo in one line, or nil when
// it has none. A path the operator just picked wins over the stored URL — it is
// what the next save will upload. It hands back the line's styled PARTS rather
// than a rendered string so the pane can bound it without cutting through an
// escape sequence (mfDetailLine).
func taskRefPhotoLine(t taskRow) mfDetailLine {
	if t.refImagePath != "" {
		return mfDetailLine{{text: "photo: " + t.refImagePath, style: StyleStatusWarn},
			{text: " (uploads on save)", style: StyleMuted}}
	}
	if t.refImageURL != "" {
		return mfDetailLine{{text: "photo: ", style: StyleMuted}, {text: t.refImageURL}}
	}
	return nil
}

// mfRemoveField is the "remove this row" row every sub-editor ends with, once
// the row it removes actually exists. It is a jdeValue, not an input: Ctrl-E on
// it is what drops the row, and the bar says so.
func mfRemoveField(label, what string, focused bool) jdeField {
	return jdeField{
		Label:   label,
		Kind:    jdeValue,
		Value:   "drops it from the " + what,
		Dim:     true,
		Focused: focused,
	}
}

// editorTitle is "Add …" or "Edit …" for a sub-editor.
func (s *MaintenanceItemFormScreen) editorTitle(noun string) string {
	if s.editIndex >= 0 {
		return "Edit " + noun
	}
	return "Add " + noun
}

// editorBar is the bar every sub-editor draws: enter saves the row back into
// the list in memory, esc abandons the edit, and Ctrl-E appears only on the
// remove row.
func (s *MaintenanceItemFormScreen) editorBar(noun string, onRemove bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save " + noun}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if onRemove {
		items = append(items, actionBarItem{"Ctrl-E", "Remove"})
	}
	return items
}

func (s *MaintenanceItemFormScreen) viewTaskEdit() string {
	fields := []jdeField{
		{
			Label:   "Title",
			Kind:    jdeText,
			Input:   &s.teTitle,
			Width:   40,
			Hint:    "required",
			Focused: s.editCursor == taskEditTitle,
		},
		{
			Label:   "Description",
			Kind:    jdeText,
			Input:   &s.teDesc,
			Width:   44,
			Focused: s.editCursor == taskEditDesc,
		},
		{
			Label:   "Required",
			Kind:    jdeChoice,
			Value:   jdeYesNo(s.teRequired),
			Focused: s.editCursor == taskEditRequired,
		},
		{
			Label:   "Reference photo",
			Kind:    jdeText,
			Input:   &s.teRefImage,
			Width:   40,
			Hint:    "absolute path · blank keeps current",
			Focused: s.editCursor == taskEditPhoto,
		},
	}
	if s.taskEditRows() > taskEditRemove {
		fields = append(fields, mfRemoveField("Remove this step", "checklist", s.editCursor == taskEditRemove))
	}
	labelWidth := jdeLabelWidth(fields)

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render(s.editorTitle("task step")))
	for _, line := range jdeCaveatLines(
		"Shown against this step on every work order the item generates.", s.bodyWidth()) {
		l.Add(line)
	}
	l.Add("")
	l.AddFittedFields(fields, labelWidth, s.bodyWidth(), 0)
	// The photo already on the step: read-only, and left in place unless a new
	// path above replaces it.
	if s.editIndex >= 0 && s.editIndex < len(s.tasks) {
		if u := s.tasks[s.editIndex].refImageURL; u != "" {
			l.AddRow(taskEditPhoto, jdeStripIndent(labelWidth)+StyleMuted.Render("current: ")+u)
		}
	}
	return s.frame(l, s.editCursor, s.statusRow(false, "", s.editErr),
		s.editorBar("step", s.editCursor == taskEditRemove && s.editIndex >= 0))
}

func (s *MaintenanceItemFormScreen) materialListLines() *jdeLines {
	rows := make([]mfSublistRow, 0, len(s.materials))
	for _, mrow := range s.materials {
		qty := mrow.quantity
		if mrow.unit != "" {
			qty += " " + mrow.unit
		}
		r := mfSublistRow{name: mrow.name,
			facts: []jdeToken{{text: fmt.Sprintf("— %s @ $%s", qty, mrow.cost)}}}
		if mrow.notes != "" {
			r.detail = append(r.detail, mfDetailLine{{text: mrow.notes, style: StyleMuted}})
		}
		rows = append(rows, r)
	}
	return s.sublistLines("Materials", "(no materials yet)", "(add a material)", rows)
}

func (s *MaintenanceItemFormScreen) viewMaterialList() string {
	body := s.materialListLines()
	return s.frame(body, s.rowCursor, "", s.sublistBar(body, len(s.materials), "Materials", "Add a material"))
}

func (s *MaintenanceItemFormScreen) viewMaterialEdit() string {
	fields := []jdeField{
		{
			Label:   "Name",
			Kind:    jdeText,
			Input:   &s.meName,
			Width:   36,
			Hint:    "required",
			Focused: s.editCursor == materialEditName,
		},
		{
			Label:   "Quantity",
			Kind:    jdeText,
			Input:   &s.meQty,
			Width:   10,
			Focused: s.editCursor == materialEditQty,
		},
		{
			Label:   "Unit",
			Kind:    jdeText,
			Input:   &s.meUnit,
			Width:   12,
			Focused: s.editCursor == materialEditUnit,
		},
		{
			Label:   "Cost per unit ($)",
			Kind:    jdeText,
			Input:   &s.meCost,
			Width:   10,
			Focused: s.editCursor == materialEditCost,
		},
		{
			Label:   "Notes",
			Kind:    jdeText,
			Input:   &s.meNotes,
			Width:   44,
			Focused: s.editCursor == materialEditNotes,
		},
	}
	if s.materialEditRows() > materialEditRemove {
		fields = append(fields, mfRemoveField("Remove this material", "list", s.editCursor == materialEditRemove))
	}

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render(s.editorTitle("material")))
	l.Add("")
	l.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frame(l, s.editCursor, s.statusRow(false, "", s.editErr),
		s.editorBar("material", s.editCursor == materialEditRemove && s.editIndex >= 0))
}

func (s *MaintenanceItemFormScreen) toolListLines() *jdeLines {
	rows := make([]mfSublistRow, 0, len(s.tools))
	for _, t := range s.tools {
		r := mfSublistRow{name: t.name, facts: []jdeToken{{text: fmt.Sprintf("×%d", t.quantity)}}}
		if t.isRequired {
			r.facts = append(r.facts, jdeToken{text: "[REQ]", style: StyleStatusWarn})
		}
		// The location hint is OMS-supplied and its length is nobody's to
		// promise, so it is a detail line rather than a tail on the row above:
		// a fact that never gives has to be one the pane can always hold.
		if t.locationHint != "" {
			r.detail = append(r.detail, mfDetailLine{{text: t.locationHint, style: StyleMuted}})
		}
		if t.notes != "" {
			r.detail = append(r.detail, mfDetailLine{{text: t.notes, style: StyleMuted}})
		}
		rows = append(rows, r)
	}
	return s.sublistLines("Tools required", "(no tools yet)", "(add a tool)", rows)
}

func (s *MaintenanceItemFormScreen) viewToolList() string {
	body := s.toolListLines()
	return s.frame(body, s.rowCursor, "", s.sublistBar(body, len(s.tools), "Tools", "Add a tool"))
}

func (s *MaintenanceItemFormScreen) viewToolEdit() string {
	fields := []jdeField{
		{
			Label:   "Name",
			Kind:    jdeText,
			Input:   &s.toName,
			Width:   36,
			Hint:    "required",
			Focused: s.editCursor == toolEditName,
		},
		{
			Label:   "Quantity",
			Kind:    jdeText,
			Input:   &s.toQty,
			Width:   10,
			Focused: s.editCursor == toolEditQty,
		},
		{
			Label:   "Where to find it",
			Kind:    jdeText,
			Input:   &s.toLoc,
			Width:   36,
			Focused: s.editCursor == toolEditLoc,
		},
		{
			Label:   "Required",
			Kind:    jdeChoice,
			Value:   jdeYesNo(s.toRequired),
			Focused: s.editCursor == toolEditRequired,
		},
		{
			Label:   "Notes",
			Kind:    jdeText,
			Input:   &s.toNotes,
			Width:   44,
			Focused: s.editCursor == toolEditNotes,
		},
	}
	if s.toolEditRows() > toolEditRemove {
		fields = append(fields, mfRemoveField("Remove this tool", "list", s.editCursor == toolEditRemove))
	}

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render(s.editorTitle("tool")))
	l.Add("")
	l.AddFittedFields(fields, jdeLabelWidth(fields), s.bodyWidth(), 0)
	return s.frame(l, s.editCursor, s.statusRow(false, "", s.editErr),
		s.editorBar("tool", s.editCursor == toolEditRemove && s.editIndex >= 0))
}
