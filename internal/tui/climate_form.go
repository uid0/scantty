// Thermostat CRUD — list + create/edit form.
//
// TUI counterpart to the web ThermostatFormPage.tsx (+ the climate thermostat
// registry). Mirrors the FULL writable field set of the web form + serializer:
// label, location (mounted-at), controls_location (conditions-room),
// controlled_asset (the HVAC/RTU that drives the safety-sign kill-breaker
// chain), manufacturer, model, notes. needs_review / created_at / updated_at
// are server-managed read-only and never sent.
//
// ThermostatListScreen is reached with the global `T` hotkey (app.go) and the
// Facilities menu. c/E/x create/edit/delete; enter opens the edit form (there
// is no read-only thermostat detail screen). Like the location list it is a
// plain screen that flips to raw input only during the delete confirm.
//
// The backend ThermostatViewSet is a plain ModelViewSet gated only by
// IsAuthenticated — any signed-in operator can create/edit/delete (no staff
// gate, unlike locations/electrical).
//
// Deferred (own follow-up bead, read-only affordance): the web form's live
// "kill-breaker preview" — when a controlled_asset is picked it calls
// GetAssetPowerChain and shows the panel/breaker badges. That is a read-only
// sanity-check, not part of the write contract; the same power chain is already
// browsable from the electrical screens.
//
// The FORM renders through the columnar "JD Edwards" layer (jde_form.go,
// sc-h412/sc-dnhx): one right-aligned label column, the three FK rows showing
// their current selection, and a persistent action bar. Enter saves, Esc
// cancels, Up/Down move, and Ctrl-E opens the picker on the row it is standing
// on — whose filter is always live, so typing narrows the list and the `/`, `j`
// and `k` it used to carry are gone.
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

// ===========================================================================
// ThermostatFormScreen
// ===========================================================================

const (
	tfLabel = iota
	tfLocation
	tfControlsLocation
	tfControlledAsset
	tfManufacturer
	tfModel
	tfNotes
	tfFieldMax
)

type thermostatFormPhase int

const (
	thermostatPhaseForm thermostatFormPhase = iota
	thermostatPhasePick
)

var thermostatFieldLabel = map[int]string{
	tfLabel:            "Label",
	tfLocation:         "Mounted at",
	tfControlsLocation: "Conditions room",
	tfControlledAsset:  "Controlled asset",
	tfManufacturer:     "Manufacturer",
	tfModel:            "Model",
	tfNotes:            "Notes",
}

// thermostatTextFields are the plain textinput-backed fields; the rest are FK
// pickers.
var thermostatTextFields = []int{tfLabel, tfManufacturer, tfModel, tfNotes}

// thermostatFieldHint carries what the placeholders used to say. A placeholder
// long enough to fill the input area leaves no underscores, so an empty
// green-screen row stops reading as empty; the note rides after the input
// instead. A columnar form marks what is REQUIRED rather than tagging
// everything else "(optional)".
var thermostatFieldHint = map[int]string{
	tfLabel:        "required",
	tfLocation:     "required",
	tfManufacturer: "e.g. Honeywell",
	tfModel:        "e.g. T6 Pro",
}

// thermostatFieldWidth sizes the input areas that are not the default.
func thermostatFieldWidth(id int) int {
	switch id {
	case tfLabel, tfNotes:
		return 40
	}
	return 0
}

type ThermostatFormScreen struct {
	deps    Deps
	edit    bool
	thermID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	locations  []omsapi.Location
	assets     []omsapi.Asset
	therm      *omsapi.Thermostat
	refArrived bool
	thArrived  bool

	jdeScreen

	inputs             []textinput.Model
	locationID         *int
	controlsLocationID *int
	controlledAssetID  *string

	fields []int
	cursor int

	phase       thermostatFormPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []assetPickRow
}

type thermostatRefLoadedMsg struct {
	locations []omsapi.Location
	assets    []omsapi.Asset
	err       error
}

type thermostatFormLoadedMsg struct {
	therm *omsapi.Thermostat
	err   error
}

type thermostatSavedMsg struct {
	therm *omsapi.Thermostat
	err   error
}

// NewThermostatFormScreen builds the create/edit form. An empty thermID opens
// create; a non-empty numeric id opens edit and hydrates from the fetched
// record.
func NewThermostatFormScreen(deps Deps, thermID string) *ThermostatFormScreen {
	id, _ := strconv.Atoi(strings.TrimSpace(thermID))
	edit := id > 0
	s := &ThermostatFormScreen{
		deps:    deps,
		edit:    edit,
		thermID: id,
		loading: true,
	}
	s.inputs = make([]textinput.Model, tfFieldMax)
	for _, fid := range thermostatTextFields {
		ti := textinput.New()
		ti.Prompt = ""
		// No placeholders: the examples and the "optional" moved to
		// thermostatFieldHint, where they ride after the input area instead of
		// filling it (see jde_form.go).
		switch fid {
		case tfLabel:
			ti.CharLimit = 120
		case tfManufacturer, tfModel:
			ti.CharLimit = 100
		case tfNotes:
			ti.CharLimit = 1000
		}
		s.inputs[fid] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{tfLabel, tfLocation, tfControlsLocation, tfControlledAsset, tfManufacturer, tfModel, tfNotes}
	s.syncFocus()
	return s
}

func (s *ThermostatFormScreen) Title() string {
	if s.edit {
		if s.therm != nil && s.therm.Label != "" {
			return "Edit thermostat: " + s.therm.Label
		}
		return "Edit thermostat"
	}
	return "New thermostat"
}

func (s *ThermostatFormScreen) WantsRawInput() bool { return true }

func (s *ThermostatFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadThermostat())
	}
	return tea.Batch(cmds...)
}

func (s *ThermostatFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *ThermostatFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return thermostatRefLoadedMsg{err: err}
		}
		// Load the full asset set (not just page 1) so the controlled-asset
		// picker can reach any HVAC/RTU — mirrors maintenance_item_form's
		// ListAllAssets picker.
		assets, err := deps.OMS.ListAllAssets(ctx)
		if err != nil {
			return thermostatRefLoadedMsg{err: err}
		}
		return thermostatRefLoadedMsg{locations: locs.Results, assets: assets}
	}
}

func (s *ThermostatFormScreen) loadThermostat() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.thermID
	return func() tea.Msg {
		th, err := deps.OMS.GetThermostat(ctx, id)
		return thermostatFormLoadedMsg{therm: th, err: err}
	}
}

func (s *ThermostatFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case thermostatRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.locations = m.locations
			s.assets = m.assets
		}
		return s, s.maybeFinalizeLoad()
	case thermostatFormLoadedMsg:
		s.thArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.therm = m.therm
		}
		return s, s.maybeFinalizeLoad()
	case thermostatSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		name := ""
		if m.therm != nil {
			name = m.therm.Label
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("thermostat %s: %s", verb, name), StatusOK),
			SwitchTo(WSFacilities, NewThermostatListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		if s.phase == thermostatPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == thermostatPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && s.isTextField(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *ThermostatFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.thArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.therm != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *ThermostatFormScreen) hydrate() {
	t := s.therm
	s.inputs[tfLabel].SetValue(t.Label)
	s.inputs[tfManufacturer].SetValue(t.Manufacturer)
	s.inputs[tfModel].SetValue(t.Model)
	s.inputs[tfNotes].SetValue(t.Notes)
	if t.Location != nil {
		v := *t.Location
		s.locationID = &v
	}
	if t.ControlsLocation != nil {
		v := *t.ControlsLocation
		s.controlsLocationID = &v
	}
	if t.ControlledAsset != nil {
		v := *t.ControlledAsset
		s.controlledAssetID = &v
	}
}

func (s *ThermostatFormScreen) isTextField(id int) bool {
	for _, t := range thermostatTextFields {
		if t == id {
			return true
		}
	}
	return false
}

func (s *ThermostatFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *ThermostatFormScreen) syncFocus() {
	for _, id := range thermostatTextFields {
		s.inputs[id].Blur()
	}
	if id, ok := s.currentFieldID(); ok && s.isTextField(id) {
		s.inputs[id].Focus()
	}
}

func (s *ThermostatFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		// EDIT opens whatever the highlighted row IS. Only the three FK rows
		// open anything, which is why the bar drops the key on the others.
		if id, ok := s.currentFieldID(); ok && s.isPickerField(id) {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	if s.isPickerField(id) {
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	}
	var cmd tea.Cmd
	s.inputs[id], cmd = s.inputs[id].Update(m)
	return s, cmd
}

// isPickerField reports whether a field is one of the three FK rows Ctrl-E
// opens.
func (s *ThermostatFormScreen) isPickerField(id int) bool {
	switch id {
	case tfLocation, tfControlsLocation, tfControlledAsset:
		return true
	}
	return false
}

func (s *ThermostatFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *ThermostatFormScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

func (s *ThermostatFormScreen) openPicker(field int) {
	s.phase = thermostatPhasePick
	s.pickField = field
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()
	s.pickCursor = 0
	// Rest the cursor on the current selection so re-picking is a no-op.
	var selKey string
	switch field {
	case tfLocation:
		if s.locationID != nil {
			selKey = strconv.Itoa(*s.locationID)
		}
	case tfControlsLocation:
		if s.controlsLocationID != nil {
			selKey = strconv.Itoa(*s.controlsLocationID)
		}
	case tfControlledAsset:
		if s.controlledAssetID != nil {
			selKey = *s.controlledAssetID
		}
	}
	if selKey != "" {
		for i, o := range s.pickOptions {
			if !o.clear && o.key == selKey {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *ThermostatFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []assetPickRow
	// The required mount location has no clear row; the two optional FKs do.
	switch s.pickField {
	case tfControlsLocation:
		opts = append(opts, assetPickRow{clear: true, label: "(none — defaults to mounted location)"})
	case tfControlledAsset:
		opts = append(opts, assetPickRow{clear: true, label: "(none)"})
	}
	add := func(key, label string) {
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, assetPickRow{key: key, label: label})
		}
	}
	switch s.pickField {
	case tfLocation, tfControlsLocation:
		for _, l := range s.locations {
			label := l.Name
			if l.Code != "" {
				label = fmt.Sprintf("%s (%s)", l.Name, l.Code)
			}
			add(strconv.Itoa(l.ID), label)
		}
	case tfControlledAsset:
		for _, a := range s.assets {
			add(thermostatAssetKey(a), thermostatAssetLabel(a))
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *ThermostatFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		s.commitPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		s.movePick(delta * s.windowRows(body, s.pickCursor, len(header)))
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

// movePick walks the option cursor, clamping at both ends — a picker list is a
// set of choices, not a ring, so running off the bottom must not reappear at the
// "(none)" row that clears the field.
func (s *ThermostatFormScreen) movePick(delta int) {
	next := s.pickCursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(s.pickOptions)-1 {
		next = len(s.pickOptions) - 1
	}
	if next < 0 {
		next = 0
	}
	s.pickCursor = next
}

func (s *ThermostatFormScreen) closePicker() {
	s.phase = thermostatPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *ThermostatFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		switch s.pickField {
		case tfLocation:
			// Required — a clear row is never offered, but guard anyway.
			if !opt.clear {
				s.locationID = pickInt(opt)
			}
		case tfControlsLocation:
			s.controlsLocationID = pickInt(opt)
		case tfControlledAsset:
			if opt.clear {
				s.controlledAssetID = nil
			} else {
				v := opt.key
				s.controlledAssetID = &v
			}
		}
	}
	s.closePicker()
}

func (s *ThermostatFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.thermID
	return s, func() tea.Msg {
		var th *omsapi.Thermostat
		var e error
		if edit {
			th, e = deps.OMS.UpdateThermostat(ctx, id, body)
		} else {
			th, e = deps.OMS.CreateThermostat(ctx, body)
		}
		return thermostatSavedMsg{therm: th, err: e}
	}
}

func (s *ThermostatFormScreen) buildPayload() (omsapi.ThermostatWrite, error) {
	var w omsapi.ThermostatWrite
	label := strings.TrimSpace(s.inputs[tfLabel].Value())
	if label == "" {
		return w, errors.New("label is required")
	}
	if s.locationID == nil {
		return w, errors.New("mounted location is required")
	}
	w = omsapi.ThermostatWrite{
		Label:            label,
		Location:         *s.locationID,
		ControlsLocation: s.controlsLocationID,
		ControlledAsset:  s.controlledAssetID,
		Manufacturer:     strings.TrimSpace(s.inputs[tfManufacturer].Value()),
		Model:            strings.TrimSpace(s.inputs[tfModel].Value()),
		Notes:            strings.TrimSpace(s.inputs[tfNotes].Value()),
	}
	return w, nil
}

func (s *ThermostatFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSFacilities, NewThermostatListScreen(s.deps))
}

func (s *ThermostatFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == thermostatPhasePick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *ThermostatFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, jdeStatusLine(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: three FK rows Ctrl-E opens,
// and four fields typed into.
func (s *ThermostatFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   thermostatFieldLabel[id],
			Width:   thermostatFieldWidth(id),
			Hint:    thermostatFieldHint[id],
			Focused: i == s.cursor,
		}
		if s.isPickerField(id) {
			value, dim := s.pickerValue(id)
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

func (s *ThermostatFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	labelWidth := jdeLabelWidth(fields)

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Thermostat"))
	l.AddFields(fields, labelWidth, s.bodyWidth(), 0)
	return l
}

// formBar names the keys that apply where the cursor is standing — and only
// those, so the bar never teaches a key that does nothing here.
func (s *ThermostatFormScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok && s.isPickerField(id) {
		items = append(items, actionBarItem{"Ctrl-E", "Pick"})
	}
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// pickerValue is an FK row's text, and whether it is an empty state rather than
// a value. It returns PLAIN text with a flag instead of pre-styled muted text,
// because a focused row has to be able to reverse-video the whole field — an
// inner reset sequence would end the highlight partway through it.
func (s *ThermostatFormScreen) pickerValue(id int) (string, bool) {
	switch id {
	case tfLocation:
		return s.locationValue()
	case tfControlsLocation:
		return s.controlsLocationValue()
	case tfControlledAsset:
		return s.controlledAssetValue()
	}
	return "", false
}

func (s *ThermostatFormScreen) locationValue() (string, bool) {
	if s.locationID == nil {
		return "(not set)", true
	}
	if name := s.locationName(*s.locationID); name != "" {
		return name, false
	}
	if s.therm != nil && s.therm.LocationName != "" {
		return s.therm.LocationName, false
	}
	return fmt.Sprintf("#%d", *s.locationID), false
}

func (s *ThermostatFormScreen) controlsLocationValue() (string, bool) {
	if s.controlsLocationID == nil {
		return "(defaults to mounted location)", true
	}
	if name := s.locationName(*s.controlsLocationID); name != "" {
		return name, false
	}
	if s.therm != nil && s.therm.ControlsLocationName != nil && *s.therm.ControlsLocationName != "" {
		return *s.therm.ControlsLocationName, false
	}
	return fmt.Sprintf("#%d", *s.controlsLocationID), false
}

func (s *ThermostatFormScreen) controlledAssetValue() (string, bool) {
	if s.controlledAssetID == nil {
		return "(none)", true
	}
	for _, a := range s.assets {
		if thermostatAssetKey(a) == *s.controlledAssetID {
			return thermostatAssetLabel(a), false
		}
	}
	if s.therm != nil && s.therm.ControlledAssetName != nil && *s.therm.ControlledAssetName != "" {
		return *s.therm.ControlledAssetName, false
	}
	return *s.controlledAssetID, false
}

func (s *ThermostatFormScreen) locationName(id int) string {
	for _, l := range s.locations {
		if l.ID == id {
			if l.Code != "" {
				return fmt.Sprintf("%s (%s)", l.Name, l.Code)
			}
			return l.Name
		}
	}
	return ""
}

// pickView builds the open picker's pinned header and its option list. The
// title names what is being picked; the note explains what the clear row does,
// which is the one thing about these three lists that is not self-evident.
func (s *ThermostatFormScreen) pickView() ([]string, *jdeLines) {
	title, note, empty := "Mounted location", "", "(no matching locations)"
	switch s.pickField {
	case tfControlsLocation:
		title = "Conditioned room"
		note = "Row 1 is none — the thermostat then conditions the room it is mounted in."
	case tfControlledAsset:
		title, empty = "Controlled asset", "(no matching assets)"
		note = "Row 1 is none — the safety-sign kill-breaker chain needs an asset."
	}
	return jdePickList{
		Title:  title,
		For:    strings.TrimSpace(s.inputs[tfLabel].Value()),
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  empty,
	}.render(s.bodyWidth())
}

func (s *ThermostatFormScreen) viewPick() string {
	header, body := s.pickView()
	paging := false
	if avail := s.bodyRows(); avail > 0 && body.Len() > avail-len(header) {
		paging = true
	}
	return s.frameWithHeader(header, body, s.pickCursor,
		jdeStatusLine(false, "", ""), jdePickBar("Select", paging))
}

// thermostatAssetKey renders an asset's UUID pk (Asset.ID is `any`) to the
// string the controlled_asset FK wants. Asset pks are UUIDs, so the JSON value
// decodes as a Go string; the fallback covers any non-string encoding without
// tripping the float64 scientific-notation trap that bites int pks.
func thermostatAssetKey(a omsapi.Asset) string {
	if s, ok := a.ID.(string); ok {
		return s
	}
	return fmt.Sprint(a.ID)
}

func thermostatAssetLabel(a omsapi.Asset) string {
	if a.AssetTag != "" {
		return fmt.Sprintf("%s (%s)", a.Name, a.AssetTag)
	}
	return a.Name
}

// ===========================================================================
// ThermostatListScreen
// ===========================================================================

type ThermostatListScreen struct {
	deps           Deps
	rows           []omsapi.Thermostat
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type thermostatListLoadedMsg struct {
	rows []omsapi.Thermostat
	err  error
}

type thermostatDeletedMsg struct {
	err error
}

func NewThermostatListScreen(deps Deps) *ThermostatListScreen {
	return &ThermostatListScreen{deps: deps, loading: true, windowSize: 20}
}

func (s *ThermostatListScreen) Title() string { return "Thermostats" }

func (s *ThermostatListScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *ThermostatListScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		page, err := deps.OMS.ListThermostats(ctx, nil)
		if err != nil {
			return thermostatListLoadedMsg{err: err}
		}
		return thermostatListLoadedMsg{rows: page.Results}
	}
}

func (s *ThermostatListScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *ThermostatListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case thermostatListLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.loadErr = ""
			s.rows = m.rows
		}
		if s.cursor >= len(s.rows) {
			s.cursor = 0
		}
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case thermostatDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("thermostat deleted", StatusOK), s.Init())
	case tea.KeyMsg:
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		switch m.String() {
		case "j", "down":
			if s.cursor < len(s.rows)-1 {
				s.cursor++
				s.scrollIntoView()
			}
		case "k", "up":
			if s.cursor > 0 {
				s.cursor--
				s.scrollIntoView()
			}
		case "ctrl+d", "pgdown":
			s.cursor += s.windowSize
			if s.cursor >= len(s.rows) {
				s.cursor = len(s.rows) - 1
			}
			s.scrollIntoView()
		case "ctrl+u", "pgup":
			s.cursor -= s.windowSize
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "g", "home":
			s.cursor = 0
			s.scrollIntoView()
		case "G", "end":
			s.cursor = len(s.rows) - 1
			if s.cursor < 0 {
				s.cursor = 0
			}
			s.scrollIntoView()
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.Init()
		case "c":
			return s, SwitchTo(WSFacilities, NewThermostatFormScreen(s.deps, ""))
		case "enter", "E":
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSFacilities, NewThermostatFormScreen(s.deps, strconv.Itoa(row.ID)))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *ThermostatListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		row, ok := s.selected()
		if !ok {
			s.confirmingDelete = false
			return s, nil
		}
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := row.ID
		return s, func() tea.Msg {
			return thermostatDeletedMsg{err: deps.OMS.DeleteThermostat(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *ThermostatListScreen) selected() (omsapi.Thermostat, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return omsapi.Thermostat{}, false
	}
	return s.rows[s.cursor], true
}

func (s *ThermostatListScreen) scrollIntoView() {
	if s.windowSize <= 0 {
		s.windowSize = 20
	}
	if s.cursor < s.windowStart {
		s.windowStart = s.cursor
	}
	if s.cursor >= s.windowStart+s.windowSize {
		s.windowStart = s.cursor - s.windowSize + 1
	}
	if s.windowStart < 0 {
		s.windowStart = 0
	}
	if len(s.rows) <= s.windowSize {
		s.windowStart = 0
	}
}

func (s *ThermostatListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading thermostats…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · c new · esc back")
	}
	if s.confirmingDelete {
		label := ""
		if row, ok := s.selected(); ok {
			label = row.Label
		}
		var prompt string
		if s.deleting {
			prompt = StyleMuted.Render("Deleting…")
		} else {
			prompt = StyleStatusWarn.Render(fmt.Sprintf("Delete thermostat %q? This can't be undone.  y delete · n/esc cancel", label))
		}
		return prompt
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No thermostats.") + "\n\n" + StyleMuted.Render("c new thermostat · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d thermostats", len(s.rows))) + "\n")
	if s.windowStart > 0 {
		b.WriteString(StyleMuted.Render("  ↑ more above") + "\n")
	}
	end := s.windowStart + s.windowSize
	if end > len(s.rows) {
		end = len(s.rows)
	}
	for i := s.windowStart; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if end < len(s.rows) {
		b.WriteString(StyleMuted.Render(fmt.Sprintf("  ↓ %d more below", len(s.rows)-end)) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render("j/k move · enter/E edit · c new · x delete · r refresh · esc back"))
	return b.String()
}

func (s *ThermostatListScreen) renderRow(i int) string {
	t := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	meta := []string{}
	// Prefer the conditioned room, falling back to the mount location — the
	// same "which room" identity the backend __str__ uses.
	room := ""
	if t.ControlsLocationName != nil && *t.ControlsLocationName != "" {
		room = *t.ControlsLocationName
	} else if t.LocationName != "" {
		room = t.LocationName
	}
	if room != "" {
		meta = append(meta, room)
	}
	if t.ControlledAssetName != nil && *t.ControlledAssetName != "" {
		meta = append(meta, "→ "+*t.ControlledAssetName)
	}
	if t.NeedsReview {
		meta = append(meta, "needs review")
	}
	line := marker + t.Label
	if len(meta) > 0 {
		line += " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	}
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}
