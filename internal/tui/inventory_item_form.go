// InventoryItemFormScreen — create/edit form for inventory items.
//
// This is the TUI counterpart to the web InventoryItemFormPage
// (frontend/src/pages/InventoryItemFormPage.tsx + inventoryItemSchema). It
// mirrors the FULL field set the web form exposes so an operator at the
// workstation can register or amend an item without switching to the browser
// ([[ship-complete-features]]). The structure follows po_create.go's
// field-by-field entry with sub-phase pickers for the foreign keys.
//
// Field kinds and how each is edited:
//
//	text / number — a bubbles textinput; type to edit, validated on submit
//	toggle        — a bool; space flips it (case-based / hazardous / serialized
//	                / active). Flipping a toggle shows or hides its dependent
//	                fields (minimum_cases, the NFPA block, serial_tracking_mode).
//	select        — a fixed option list; space or ←/→ cycles (shelf_position,
//	                serial_tracking_mode)
//	picker        — a foreign key chosen from a searchable list in a sub-phase;
//	                space opens it (category, location)
//
// Navigation: tab / ↑↓ move between the visible fields, enter saves, esc
// cancels. The form is longer than the pane, so it scrolls to keep the focused
// field on screen.
//
// This is bead #1 of the ScanTTY parity program; the pattern here is meant to
// be reused by the follow-on create/edit forms (assets, category/location/
// supplier CRUD, …).
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

// Field identifiers. The order here is the canonical top-to-bottom order the
// form renders (conditional fields are filtered out when their toggle is off —
// see rebuildFields).
const (
	fName = iota
	fDescription
	fSKU
	fImageURL
	fCurrentStock
	fMinimumStock
	fReorderQuantity
	fUseCaseBasedReorder
	fMinimumCases
	fReorderCases
	fCategory
	fLocation
	fShelfPosition
	fIsHazardous
	fMSDSURL
	fNFPAHealth
	fNFPAFire
	fNFPAInstability
	fNFPASpecial
	fIsSerialized
	fSerialTrackingMode
	fNotes
	fIsActive
	fIsRetired
	fFieldMax
)

type itemFieldKind int

const (
	kindText itemFieldKind = iota
	kindNumber
	kindToggle
	kindSelect
	kindPicker
)

type itemFormPhase int

const (
	itemFormPhaseForm itemFormPhase = iota
	itemFormPhaseCategoryPick
	itemFormPhaseLocationPick
)

type selectOption struct{ value, label string }

// shelfPositionOptions mirrors the web form's Shelf Position select. The empty
// value ("Not specified") is omitted from the payload.
var shelfPositionOptions = []selectOption{
	{"", "Not specified"},
	{"top", "Top shelf"},
	{"bottom", "Bottom shelf"},
}

// serialModeOptions mirrors the web form's Tracking mode select. Only sent when
// the item is flagged serialized.
var serialModeOptions = []selectOption{
	{"consumable", "Consumable (used up)"},
	{"reusable", "Reusable (installed / removed)"},
}

var itemFieldLabel = map[int]string{
	fName:                "Name",
	fDescription:         "Description",
	fSKU:                 "SKU",
	fImageURL:            "Image URL",
	fCurrentStock:        "Current stock",
	fMinimumStock:        "Minimum stock",
	fReorderQuantity:     "Reorder quantity",
	fUseCaseBasedReorder: "Case-based reordering",
	fMinimumCases:        "Minimum cases",
	fReorderCases:        "Reorder cases",
	fCategory:            "Category",
	fLocation:            "Location",
	fShelfPosition:       "Shelf position",
	fIsHazardous:         "Hazardous material",
	fMSDSURL:             "MSDS/SDS URL",
	fNFPAHealth:          "NFPA health (0-4)",
	fNFPAFire:            "NFPA fire (0-4)",
	fNFPAInstability:     "NFPA instability (0-4)",
	fNFPASpecial:         "NFPA special hazards",
	fIsSerialized:        "Track serial numbers",
	fSerialTrackingMode:  "Tracking mode",
	fNotes:               "Notes",
	fIsActive:            "Active",
	fIsRetired:           "Retired",
}

func fieldKind(id int) itemFieldKind {
	switch id {
	case fName, fDescription, fSKU, fImageURL, fMSDSURL, fNFPASpecial, fNotes:
		return kindText
	case fCurrentStock, fMinimumStock, fReorderQuantity, fMinimumCases, fReorderCases,
		fNFPAHealth, fNFPAFire, fNFPAInstability:
		return kindNumber
	case fUseCaseBasedReorder, fIsHazardous, fIsSerialized, fIsActive, fIsRetired:
		return kindToggle
	case fShelfPosition, fSerialTrackingMode:
		return kindSelect
	case fCategory, fLocation:
		return kindPicker
	}
	return kindText
}

func isTextKind(id int) bool {
	k := fieldKind(id)
	return k == kindText || k == kindNumber
}

// itemPickOption is one row in the category/location sub-picker. clear marks the
// synthetic "(none)" row that unsets the field.
type itemPickOption struct {
	id    int
	label string
	clear bool
}

type InventoryItemFormScreen struct {
	deps   Deps
	edit   bool
	itemID string

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	// Loaded reference data for the pickers and edit-mode hydration.
	categories  []omsapi.Category
	locations   []omsapi.Location
	item        *omsapi.Item
	refArrived  bool
	itemArrived bool

	terminalHeight int

	// Text/number field storage, indexed by field id. Non-text slots are left
	// as zero-value models and never rendered/updated.
	inputs []textinput.Model

	// Toggle + select state.
	useCaseBased bool
	isHazardous  bool
	isSerialized bool
	isActive     bool
	isRetired    bool
	shelfPos     int
	serialMode   int

	// Picker selections (nil == unset).
	categoryID *int
	locationID *int

	// Visible-field navigation.
	fields []int
	cursor int

	// Category/location sub-picker state.
	phase       itemFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemPickOption
}

type itemFormRefLoadedMsg struct {
	categories []omsapi.Category
	locations  []omsapi.Location
	err        error
}

type itemFormItemLoadedMsg struct {
	item *omsapi.Item
	err  error
}

type itemFormSavedMsg struct {
	item *omsapi.Item
	err  error
}

// NewInventoryItemFormScreen builds the create/edit form. An empty itemID opens
// create mode with sensible defaults; a non-empty id opens edit mode and
// hydrates every field from the fetched item.
func NewInventoryItemFormScreen(deps Deps, itemID string) *InventoryItemFormScreen {
	edit := strings.TrimSpace(itemID) != ""
	s := &InventoryItemFormScreen{
		deps:      deps,
		edit:      edit,
		itemID:    strings.TrimSpace(itemID),
		loading:   true,
		isActive:  true,  // web default
		isRetired: false, // web default (a new item starts un-retired)
	}

	s.inputs = make([]textinput.Model, fFieldMax)
	for id := 0; id < fFieldMax; id++ {
		if !isTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = itemCharLimitFor(id)
		ti.Placeholder = itemPlaceholderFor(id)
		s.inputs[id] = ti
	}

	// Create-mode numeric defaults mirror inventoryItemSchema.
	if !edit {
		s.inputs[fCurrentStock].SetValue("0")
		s.inputs[fMinimumStock].SetValue("0")
		s.inputs[fReorderQuantity].SetValue("1")
	}

	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.rebuildFields()
	s.syncFocus()
	return s
}

func itemCharLimitFor(id int) int {
	switch id {
	case fName:
		return 200
	case fSKU:
		return 100
	case fDescription:
		return 500
	case fImageURL, fMSDSURL:
		return 500
	case fNFPASpecial:
		return 20
	case fNotes:
		return 1000
	case fNFPAHealth, fNFPAFire, fNFPAInstability:
		return 1
	default:
		return 12
	}
}

func itemPlaceholderFor(id int) string {
	switch id {
	case fName:
		return "item name"
	case fSKU:
		return "auto-generated if blank"
	case fImageURL:
		return "https://… (optional)"
	case fMSDSURL:
		return "https://… safety data sheet"
	case fNFPASpecial:
		return "W, OX, COR, ACID…"
	case fDescription, fNotes:
		return "optional"
	case fCurrentStock, fMinimumStock:
		return "0"
	case fReorderQuantity, fMinimumCases, fReorderCases:
		return "1"
	case fNFPAHealth, fNFPAFire, fNFPAInstability:
		return "0-4"
	default:
		return ""
	}
}

func (s *InventoryItemFormScreen) Title() string {
	if s.edit {
		if s.item != nil && s.item.Name != "" {
			return fmt.Sprintf("Edit item: %s", s.item.Name)
		}
		return "Edit item"
	}
	return "New inventory item"
}

func (s *InventoryItemFormScreen) WantsRawInput() bool { return true }

func (s *InventoryItemFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadItem())
	}
	return tea.Batch(cmds...)
}

func (s *InventoryItemFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

// loadRefData fetches the first page of categories and locations for the
// pickers. Like the New-PO supplier picker this loads a single page — installs
// rarely have more than a page of either, and the web item form likewise reads
// only results[0..n] of the first page.
func (s *InventoryItemFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		cats, err := deps.OMS.ListCategories(ctx, nil)
		if err != nil {
			return itemFormRefLoadedMsg{err: err}
		}
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return itemFormRefLoadedMsg{err: err}
		}
		return itemFormRefLoadedMsg{categories: cats.Results, locations: locs.Results}
	}
}

func (s *InventoryItemFormScreen) loadItem() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.itemID
	return func() tea.Msg {
		item, err := deps.OMS.GetItem(ctx, id)
		return itemFormItemLoadedMsg{item: item, err: err}
	}
}

func (s *InventoryItemFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil

	case itemFormRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.categories = m.categories
			s.locations = m.locations
		}
		return s, s.maybeFinalizeLoad()

	case itemFormItemLoadedMsg:
		s.itemArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.item = m.item
		}
		return s, s.maybeFinalizeLoad()

	case itemFormSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		id := s.itemID
		name := ""
		if m.item != nil {
			id = m.item.ID
			name = m.item.Name
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("item %s: %s", verb, name), StatusOK),
			SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, id)),
		)

	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		switch s.phase {
		case itemFormPhaseCategoryPick, itemFormPhaseLocationPick:
			return s.updatePickPhase(m)
		default:
			return s.updateFormPhase(m)
		}
	}

	// Non-key messages (cursor blink) go to whichever input owns the caret.
	if s.phase == itemFormPhaseCategoryPick || s.phase == itemFormPhaseLocationPick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && isTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

// maybeFinalizeLoad flips out of the loading state once every in-flight fetch
// has reported. In edit mode that's both the reference data and the item; in
// create mode just the reference data.
func (s *InventoryItemFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
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

// hydrate fills every field from the fetched item (edit mode).
func (s *InventoryItemFormScreen) hydrate() {
	it := s.item
	set := func(id int, v string) { s.inputs[id].SetValue(v) }

	set(fName, it.Name)
	set(fDescription, it.Description)
	set(fSKU, it.SKU)
	if strings.HasPrefix(it.Image, "http") {
		set(fImageURL, it.Image)
	}
	set(fCurrentStock, strconv.Itoa(it.Stock))
	set(fMinimumStock, strconv.Itoa(it.MinimumStock))
	set(fReorderQuantity, strconv.Itoa(it.ReorderQuantity))

	s.useCaseBased = it.UseCaseBasedReorder
	if it.MinimumCases != nil {
		set(fMinimumCases, strconv.Itoa(int(*it.MinimumCases)))
	}
	if it.ReorderCases != nil {
		set(fReorderCases, strconv.Itoa(int(*it.ReorderCases)))
	}

	s.categoryID = it.Category
	// The serializer returns location as a name string, so resolve it back to
	// an id against the loaded locations. If it isn't on the loaded page the
	// picker stays unset and a PATCH simply omits location (leaving it intact).
	if it.Location != "" {
		for _, loc := range s.locations {
			if loc.Name == it.Location {
				id := loc.ID
				s.locationID = &id
				break
			}
		}
	}

	s.isHazardous = it.IsHazardous
	set(fMSDSURL, it.MSDSURL)
	if it.NFPAHealthHazard != nil {
		set(fNFPAHealth, strconv.Itoa(*it.NFPAHealthHazard))
	}
	if it.NFPAFireHazard != nil {
		set(fNFPAFire, strconv.Itoa(*it.NFPAFireHazard))
	}
	if it.NFPAInstabilityHazard != nil {
		set(fNFPAInstability, strconv.Itoa(*it.NFPAInstabilityHazard))
	}
	set(fNFPASpecial, it.NFPASpecialHazards)

	s.isSerialized = it.IsSerialized
	s.serialMode = serialModeIndex(it.SerialTrackingMode)

	set(fNotes, it.Notes)
	// is_active defaults true on the model; honour the fetched value.
	s.isActive = it.IsActive
	// is_retired defaults false; honour the fetched phase-out state.
	s.isRetired = it.IsRetired
}

func serialModeIndex(mode string) int {
	for i, o := range serialModeOptions {
		if o.value == mode {
			return i
		}
	}
	return 0
}

// rebuildFields recomputes the visible-field list from the current toggle
// state, preserving the cursor on the same field id where possible.
func (s *InventoryItemFormScreen) rebuildFields() {
	var focused int = -1
	if id, ok := s.currentFieldID(); ok {
		focused = id
	}

	f := []int{fName, fDescription, fSKU, fImageURL, fCurrentStock, fMinimumStock, fReorderQuantity, fUseCaseBasedReorder}
	if s.useCaseBased {
		f = append(f, fMinimumCases, fReorderCases)
	}
	f = append(f, fCategory, fLocation, fShelfPosition, fIsHazardous)
	if s.isHazardous {
		f = append(f, fMSDSURL, fNFPAHealth, fNFPAFire, fNFPAInstability, fNFPASpecial)
	}
	f = append(f, fIsSerialized)
	if s.isSerialized {
		f = append(f, fSerialTrackingMode)
	}
	f = append(f, fNotes, fIsActive, fIsRetired)
	s.fields = f

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

func (s *InventoryItemFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *InventoryItemFormScreen) setCursorToField(id int) {
	for i, fid := range s.fields {
		if fid == id {
			s.cursor = i
			return
		}
	}
}

func (s *InventoryItemFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if isTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && isTextKind(id) {
		s.inputs[id].Focus()
	}
}

// ---------------------------------------------------------------------------
// Form phase
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	switch fieldKind(id) {
	case kindToggle:
		if m.String() == " " {
			s.flipToggle(id)
		}
		return s, nil
	case kindSelect:
		switch m.String() {
		case " ", "right":
			s.cycleSelect(id, +1)
		case "left":
			s.cycleSelect(id, -1)
		}
		return s, nil
	case kindPicker:
		if m.String() == " " {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *InventoryItemFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *InventoryItemFormScreen) flipToggle(id int) {
	switch id {
	case fUseCaseBasedReorder:
		s.useCaseBased = !s.useCaseBased
	case fIsHazardous:
		s.isHazardous = !s.isHazardous
	case fIsSerialized:
		s.isSerialized = !s.isSerialized
	case fIsActive:
		s.isActive = !s.isActive
	case fIsRetired:
		s.isRetired = !s.isRetired
	}
	s.rebuildFields()
	s.syncFocus()
}

func (s *InventoryItemFormScreen) cycleSelect(id, delta int) {
	switch id {
	case fShelfPosition:
		n := len(shelfPositionOptions)
		s.shelfPos = (s.shelfPos + delta + n) % n
	case fSerialTrackingMode:
		n := len(serialModeOptions)
		s.serialMode = (s.serialMode + delta + n) % n
	}
}

func (s *InventoryItemFormScreen) toggleState(id int) bool {
	switch id {
	case fUseCaseBasedReorder:
		return s.useCaseBased
	case fIsHazardous:
		return s.isHazardous
	case fIsSerialized:
		return s.isSerialized
	case fIsActive:
		return s.isActive
	case fIsRetired:
		return s.isRetired
	}
	return false
}

// ---------------------------------------------------------------------------
// Category / location picker sub-phase
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) openPicker(id int) {
	if id == fCategory {
		s.phase = itemFormPhaseCategoryPick
	} else {
		s.phase = itemFormPhaseLocationPick
	}
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()

	// Start the cursor on the currently-selected option so re-picking is a
	// no-op keystroke.
	s.pickCursor = 0
	sel := s.categoryID
	if s.phase == itemFormPhaseLocationPick {
		sel = s.locationID
	}
	if sel != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.id == *sel {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *InventoryItemFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	opts := []itemPickOption{{clear: true, label: "(none)"}}
	if s.phase == itemFormPhaseCategoryPick {
		for _, c := range s.categories {
			if q == "" || strings.Contains(strings.ToLower(c.Name), q) {
				opts = append(opts, itemPickOption{id: c.ID, label: c.Name})
			}
		}
	} else {
		for _, l := range s.locations {
			label := l.Name
			if l.Code != "" {
				label = fmt.Sprintf("%s (%s)", l.Name, l.Code)
			}
			if q == "" || strings.Contains(strings.ToLower(label), q) {
				opts = append(opts, itemPickOption{id: l.ID, label: label})
			}
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *InventoryItemFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.pickTyping {
		switch m.Type {
		case tea.KeyEsc:
			s.pickTyping = false
			s.pickSearch.Blur()
			return s, nil
		case tea.KeyEnter:
			s.pickTyping = false
			s.pickSearch.Blur()
			s.applyPickFilter()
			s.pickCursor = 0
			return s, nil
		}
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(m)
		s.applyPickFilter()
		return s, cmd
	}

	switch m.String() {
	case "esc":
		s.phase = itemFormPhaseForm
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
		s.commitPick()
	}
	return s, nil
}

func (s *InventoryItemFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		switch s.phase {
		case itemFormPhaseCategoryPick:
			if opt.clear {
				s.categoryID = nil
			} else {
				id := opt.id
				s.categoryID = &id
			}
		case itemFormPhaseLocationPick:
			if opt.clear {
				s.locationID = nil
			} else {
				id := opt.id
				s.locationID = &id
			}
		}
	}
	s.phase = itemFormPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

// ---------------------------------------------------------------------------
// Submit
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.itemID
	return s, func() tea.Msg {
		var item *omsapi.Item
		var e error
		if edit {
			item, e = deps.OMS.UpdateInventoryItem(ctx, id, body)
		} else {
			item, e = deps.OMS.CreateInventoryItem(ctx, body)
		}
		return itemFormSavedMsg{item: item, err: e}
	}
}

func (s *InventoryItemFormScreen) buildPayload() (omsapi.ItemWrite, error) {
	var w omsapi.ItemWrite

	name := strings.TrimSpace(s.inputs[fName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	cur, err := parseCount(s.inputs[fCurrentStock].Value(), "current stock", 0)
	if err != nil {
		return w, err
	}
	minStock, err := parseCount(s.inputs[fMinimumStock].Value(), "minimum stock", 0)
	if err != nil {
		return w, err
	}
	roq, err := parseCount(s.inputs[fReorderQuantity].Value(), "reorder quantity", 1)
	if err != nil {
		return w, err
	}

	w = omsapi.ItemWrite{
		Name:                name,
		Description:         strPtrTrim(s.inputs[fDescription].Value()),
		SKU:                 strPtrTrim(s.inputs[fSKU].Value()),
		ImageURL:            strPtrTrim(s.inputs[fImageURL].Value()),
		CurrentStock:        cur,
		MinimumStock:        minStock,
		ReorderQuantity:     roq,
		UseCaseBasedReorder: s.useCaseBased,
		Category:            s.categoryID,
		Location:            intPtrToStr(s.locationID),
		ShelfPosition:       selectValuePtr(shelfPositionOptions, s.shelfPos),
		IsHazardous:         s.isHazardous,
		IsSerialized:        s.isSerialized,
		IsActive:            s.isActive,
		IsRetired:           s.isRetired,
		Notes:               strPtrTrim(s.inputs[fNotes].Value()),
	}

	if s.useCaseBased {
		mc, err := parseCount(s.inputs[fMinimumCases].Value(), "minimum cases", 1)
		if err != nil {
			return w, err
		}
		rc, err := parseCount(s.inputs[fReorderCases].Value(), "reorder cases", 1)
		if err != nil {
			return w, err
		}
		w.MinimumCases = &mc
		w.ReorderCases = &rc
	}

	if s.isHazardous {
		msds := strings.TrimSpace(s.inputs[fMSDSURL].Value())
		if msds == "" {
			return w, errors.New("MSDS/SDS URL is required for hazardous materials")
		}
		w.MSDSURL = &msds
		if v, ok, err := parseNFPA(s.inputs[fNFPAHealth].Value(), "NFPA health"); err != nil {
			return w, err
		} else if ok {
			w.NFPAHealthHazard = &v
		}
		if v, ok, err := parseNFPA(s.inputs[fNFPAFire].Value(), "NFPA fire"); err != nil {
			return w, err
		} else if ok {
			w.NFPAFireHazard = &v
		}
		if v, ok, err := parseNFPA(s.inputs[fNFPAInstability].Value(), "NFPA instability"); err != nil {
			return w, err
		} else if ok {
			w.NFPAInstabilityHazard = &v
		}
		w.NFPASpecialHazards = strPtrTrim(s.inputs[fNFPASpecial].Value())
	}

	if s.isSerialized {
		mode := serialModeOptions[s.serialMode].value
		w.SerialTrackingMode = &mode
	}

	return w, nil
}

// parseCount parses a non-negative integer with a minimum. An empty value is
// treated as 0 when min is 0 (matching the web form's default-0 stock fields);
// when min > 0 an empty value is an error.
func parseCount(raw, label string, min int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if min <= 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("%s is required", label)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", label)
	}
	if n < min {
		return 0, fmt.Errorf("%s must be at least %d", label, min)
	}
	return n, nil
}

// parseNFPA parses an optional 0-4 rating. ok is false when the field is blank.
func parseNFPA(raw, label string) (val int, ok bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil {
		return 0, false, fmt.Errorf("%s must be a number 0-4", label)
	}
	if n < 0 || n > 4 {
		return 0, false, fmt.Errorf("%s must be between 0 and 4", label)
	}
	return n, true, nil
}

func strPtrTrim(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}

func intPtrToStr(id *int) *string {
	if id == nil {
		return nil
	}
	v := strconv.Itoa(*id)
	return &v
}

func selectValuePtr(opts []selectOption, idx int) *string {
	if idx < 0 || idx >= len(opts) {
		return nil
	}
	v := opts[idx].value
	if v == "" {
		return nil
	}
	return &v
}

func (s *InventoryItemFormScreen) cancelCmd() tea.Cmd {
	if s.edit && s.itemID != "" {
		return SwitchTo(WSInventory, NewInventoryDetailScreen(s.deps, s.itemID))
	}
	return SwitchTo(WSInventory, newScreenFor(WSInventory, s.deps))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (s *InventoryItemFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	// A load error means the reference data or the edited item didn't arrive,
	// so the form can't be used — show the error and let the operator back out.
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	switch s.phase {
	case itemFormPhaseCategoryPick, itemFormPhaseLocationPick:
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *InventoryItemFormScreen) viewForm() string {
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

func (s *InventoryItemFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := itemFieldLabel[id]

	var value string
	switch fieldKind(id) {
	case kindText, kindNumber:
		value = s.inputs[id].View()
	case kindToggle:
		if s.toggleState(id) {
			value = StyleStatusOK.Render("[x] yes")
		} else {
			value = StyleMuted.Render("[ ] no")
		}
	case kindSelect:
		value = s.selectLabel(id)
	case kindPicker:
		value = s.pickerLabel(id)
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *InventoryItemFormScreen) selectLabel(id int) string {
	switch id {
	case fShelfPosition:
		if s.shelfPos >= 0 && s.shelfPos < len(shelfPositionOptions) {
			return "‹ " + shelfPositionOptions[s.shelfPos].label + " ›"
		}
	case fSerialTrackingMode:
		if s.serialMode >= 0 && s.serialMode < len(serialModeOptions) {
			return "‹ " + serialModeOptions[s.serialMode].label + " ›"
		}
	}
	return ""
}

func (s *InventoryItemFormScreen) pickerLabel(id int) string {
	if id == fCategory {
		if s.categoryID == nil {
			return StyleMuted.Render("(none)")
		}
		for _, c := range s.categories {
			if c.ID == *s.categoryID {
				return c.Name
			}
		}
		return fmt.Sprintf("#%d", *s.categoryID)
	}
	// location
	if s.locationID == nil {
		return StyleMuted.Render("(none)")
	}
	for _, l := range s.locations {
		if l.ID == *s.locationID {
			return l.Name
		}
	}
	return fmt.Sprintf("#%d", *s.locationID)
}

func (s *InventoryItemFormScreen) helpText() string {
	kindHelp := "type to edit"
	if id, ok := s.currentFieldID(); ok {
		switch fieldKind(id) {
		case kindToggle:
			kindHelp = "space toggle"
		case kindSelect:
			kindHelp = "space/←→ change"
		case kindPicker:
			kindHelp = "space to pick"
		}
	}
	return kindHelp + " · tab/↑↓ move · enter save · esc cancel"
}

// visibleRows returns how many field rows fit given the current terminal
// height, reserving space for the help line, indicators, spacing, and the
// status line.
func (s *InventoryItemFormScreen) visibleRows() int {
	const chrome = 6 // help + blank + blank + status + 2 scroll indicators
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

// fieldWindow returns [start,end) so cursor stays roughly centred within a
// window of at most `visible` rows.
func fieldWindow(cursor, total, visible int) (int, int) {
	if visible >= total {
		return 0, total
	}
	start := cursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > total {
		end = total
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func (s *InventoryItemFormScreen) viewPick() string {
	var b strings.Builder
	what := "category"
	if s.phase == itemFormPhaseLocationPick {
		what = "location"
	}
	b.WriteString(StyleMuted.Render("Pick "+what+" — j/k move · / filter · enter select · esc back") + "\n\n")
	if s.pickTyping || s.pickSearch.Value() != "" {
		b.WriteString(StyleMuted.Render("filter: ") + s.pickSearch.View() + "\n\n")
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
		opt := s.pickOptions[i]
		label := opt.label
		if opt.clear {
			label = StyleMuted.Render(opt.label)
		}
		line := caret + label
		if i == s.pickCursor {
			line = StyleSidebarItemActive.Render(caret + opt.label)
		}
		b.WriteString(line + "\n")
	}
	if end < len(s.pickOptions) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.pickOptions)-end)) + "\n")
	}
	return b.String()
}
