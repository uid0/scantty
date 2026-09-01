// Electrical CRUD forms — the leaf tier: PowerOutlet + Disconnect create+edit.
//
// TUI counterpart to the web PowerOutletFormPage (and the disconnect write
// surface the web only exposes as an API service — ScanTTY ships the first
// Disconnect UI). They complete the power-topology create/edit/delete parity
// begun by electrical_forms.go (panel/breaker/circuit) so an operator can build
// out a circuit's outlets + disconnects from the workstation
// ([[scantty-parity-program]] Tier-3). Structure mirrors electrical_forms.go
// exactly (field-id iota + field kinds + form/pick sub-phase); the Disconnect
// form adds the required_loto_devices MULTI-picker (space toggles membership,
// enter closes — the asset_form.go required_certifications idiom) that emits the
// write-only required_loto_device_ids []int list.
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

// ---------------------------------------------------------------------------
// Choice options — exact codes from the electrical serializers / models. A
// wrong code 400s at save, so these are the parity contract.
// ---------------------------------------------------------------------------

// nemaOutletTypeOptions is the authoritative NEMA/IEC receptacle set from the
// backend NEMA_PORT_TYPE_CHOICES (models.py). The legacy lowercase "other" alias
// is intentionally omitted from the picker (it exists only to decode old rows);
// the form always writes the canonical "OTHER". This is a superset of the web
// PowerOutletFormPage's list, which drops L5-20R/L5-30R and the C13–C20 IEC
// codes — completeness is measured against the serializer, not that page.
var nemaOutletTypeOptions = []selectOption{
	{"5-15R", "NEMA 5-15R (120V 15A)"},
	{"5-20R", "NEMA 5-20R (120V 20A)"},
	{"6-15R", "NEMA 6-15R (240V 15A)"},
	{"6-20R", "NEMA 6-20R (240V 20A)"},
	{"L5-15R", "NEMA L5-15R (120V 15A locking)"},
	{"L5-20R", "NEMA L5-20R (120V 20A locking)"},
	{"L5-30R", "NEMA L5-30R (120V 30A locking)"},
	{"L6-20R", "NEMA L6-20R (240V 20A locking)"},
	{"L6-30R", "NEMA L6-30R (240V 30A locking)"},
	{"14-30R", "NEMA 14-30R (240V 30A)"},
	{"14-50R", "NEMA 14-50R (240V 50A)"},
	{"C13", "IEC C13 (PDU appliance, ≤10A)"},
	{"C14", "IEC C14 (PDU inlet, ≤10A)"},
	{"C19", "IEC C19 (PDU high-current, ≤16A)"},
	{"C20", "IEC C20 (PDU high-current inlet)"},
	{"USB", "USB charging"},
	{"OTHER", "Other"},
}

var outletStatusOptions = []selectOption{
	{"active", "Active"},
	{"inactive", "Inactive"},
	{"capped", "Capped / decommissioned"},
}

// normalizeOutletType folds the backend's legacy lowercase "other" alias
// (NEMA_PORT_TYPE_CHOICES carries both "OTHER" and a legacy "other" for rows the
// old frontend wrote) onto the canonical "OTHER" the picker offers, so an edit
// of a legacy-typed outlet hydrates + round-trips correctly instead of silently
// remapping to the index-0 default.
func normalizeOutletType(code string) string {
	if code == "other" {
		return "OTHER"
	}
	return code
}

var disconnectTypeOptions = []selectOption{
	{"fused", "Fused safety switch"},
	{"unfused", "Unfused safety switch"},
	{"toggle", "Toggle / snap switch"},
	{"integral", "Integral to the equipment"},
	{"none", "No separate disconnect (breaker serves)"},
}

// ===========================================================================
// PowerOutletFormScreen
// ===========================================================================

const (
	poCircuit = iota
	poLocation
	poDisconnect
	poOutletType
	poStatus
	poLabel
	poLocationDesc
	poNotes
	poNeedsReview
	poFieldMax
)

var outletFieldLabel = map[int]string{
	poCircuit:  "Circuit",
	poLocation: "Location",
	// The parentheticals moved to outletFieldHint: a note in the LABEL widens
	// the column all five electrical forms share, so it shoves the input areas
	// right on every one of them (jde_form.go).
	poDisconnect:   "Disconnect",
	poOutletType:   "Outlet type",
	poStatus:       "Status",
	poLabel:        "Label",
	poLocationDesc: "Location description",
	poNotes:        "Notes",
	poNeedsReview:  "Needs review",
}

// outletFieldHint carries what the placeholders and the label parentheticals
// used to say. A placeholder long enough to fill the input area leaves no
// underscores, so an empty green-screen row stops reading as empty; and a
// columnar form marks what is REQUIRED rather than tagging everything else
// "(optional)".
// A hint is CLIPPED, not wrapped, when the row runs past the pane
// (layout.go's clampToBox), so a wide input area and a long hint cannot both
// fit — location description keeps the room to type and loses its example,
// which its label already implies. See TestJDESweepC_RowsFitTheBody.
var outletFieldHint = map[int]string{
	poCircuit:    "required",
	poLocation:   "required",
	poOutletType: "NEMA / IEC",
	poLabel:      "e.g. NW-bench-1",
}

// outletFieldWidth sizes the input areas that are not the default.
func outletFieldWidth(id int) int {
	switch id {
	case poLocationDesc, poNotes:
		return 40
	}
	return 0
}

func outletFieldKind(id int) assetFieldKind {
	switch id {
	case poLabel, poLocationDesc, poNotes:
		return akText
	case poNeedsReview:
		return akToggle
	case poOutletType, poStatus:
		return akSelect
	case poCircuit, poLocation, poDisconnect:
		return akPicker
	}
	return akText
}

func outletIsTextKind(id int) bool {
	k := outletFieldKind(id)
	return k == akText || k == akNumber
}

type PowerOutletFormScreen struct {
	deps     Deps
	edit     bool
	outletID int
	// presetCircuitID pre-selects the circuit when opened from a circuit's outlets
	// list; panelID is carried through for exit routing back to that list.
	presetCircuitID int
	panelID         int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	circuits    []omsapi.PowerCircuitDetail
	locations   []omsapi.Location
	disconnects []omsapi.DisconnectDetail
	outlet      *omsapi.PowerOutletDetail
	refArrived  bool
	recArrived  bool

	jdeScreen

	inputs []textinput.Model

	needsReview   bool
	outletTypeIdx int
	statusIdx     int

	circuitID    *int
	locationID   *int
	disconnectID *int

	fields []int
	cursor int

	phase       elecFormPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []itemPickOption
}

type outletRefLoadedMsg struct {
	circuits    []omsapi.PowerCircuitDetail
	locations   []omsapi.Location
	disconnects []omsapi.DisconnectDetail
	err         error
}

type outletRecordLoadedMsg struct {
	outlet *omsapi.PowerOutletDetail
	err    error
}

type outletSavedMsg struct {
	outlet *omsapi.PowerOutletDetail
	err    error
}

// NewPowerOutletFormScreen opens create mode when outletID == 0. presetCircuitID
// pre-selects the parent circuit; panelID scopes the exit route.
func NewPowerOutletFormScreen(deps Deps, outletID, presetCircuitID, panelID int) *PowerOutletFormScreen {
	s := &PowerOutletFormScreen{
		deps:            deps,
		edit:            outletID != 0,
		outletID:        outletID,
		presetCircuitID: presetCircuitID,
		panelID:         panelID,
		loading:         true,
	}
	if !s.edit && presetCircuitID != 0 {
		id := presetCircuitID
		s.circuitID = &id
	}
	s.inputs = make([]textinput.Model, poFieldMax)
	for id := 0; id < poFieldMax; id++ {
		if !outletIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = outletCharLimit(id)
		ti.Placeholder = outletPlaceholder(id)
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{poCircuit, poLocation, poDisconnect, poOutletType, poStatus, poLabel, poLocationDesc, poNotes, poNeedsReview}
	s.syncFocus()
	return s
}

func outletCharLimit(id int) int {
	switch id {
	case poLabel:
		return 80
	case poLocationDesc:
		return 200
	case poNotes:
		return 1000
	}
	return 200
}

// outletPlaceholder is empty for every field now — see outletFieldHint.
func outletPlaceholder(id int) string { return "" }

func (s *PowerOutletFormScreen) Title() string {
	if s.edit {
		return "Edit outlet"
	}
	return "New outlet"
}

func (s *PowerOutletFormScreen) WantsRawInput() bool { return true }

func (s *PowerOutletFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *PowerOutletFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PowerOutletFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return outletRefLoadedMsg{err: err}
		}
		msg := outletRefLoadedMsg{locations: locs.Results}
		// Circuit + disconnect catalogues are best-effort: a hiccup loading them
		// leaves the picker empty rather than blocking outlet creation.
		if cks, err := deps.OMS.ListAllPowerCircuits(ctx); err == nil {
			msg.circuits = cks
		}
		if discs, err := deps.OMS.ListDisconnects(ctx, 0); err == nil {
			msg.disconnects = discs
		}
		return msg
	}
}

func (s *PowerOutletFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.outletID
	return func() tea.Msg {
		o, err := deps.OMS.GetPowerOutlet(ctx, id)
		return outletRecordLoadedMsg{outlet: o, err: err}
	}
}

func (s *PowerOutletFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case outletRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.circuits = m.circuits
			s.locations = m.locations
			s.disconnects = m.disconnects
		}
		return s, s.maybeFinalizeLoad()
	case outletRecordLoadedMsg:
		s.recArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.outlet = m.outlet
		}
		return s, s.maybeFinalizeLoad()
	case outletSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		return s, tea.Batch(Status("outlet "+verb, StatusOK), s.exitCmd())
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.exitCmd()
			}
			return s, nil
		}
		if s.phase == elecPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == elecPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && outletIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *PowerOutletFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.recArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.outlet != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *PowerOutletFormScreen) hydrate() {
	o := s.outlet
	cir := o.Circuit
	s.circuitID = &cir
	loc := o.Location
	s.locationID = &loc
	s.disconnectID = copyIntPtr(o.Disconnect)
	// normalizeOutletType folds the backend's legacy lowercase "other" alias onto
	// the canonical "OTHER" the picker offers. Without this, selectIndexOf can't
	// find "other", silently defaults to index 0 ("5-15R"), and a save would
	// corrupt a legacy-typed outlet into a 15A receptacle.
	s.outletTypeIdx = selectIndexOf(nemaOutletTypeOptions, normalizeOutletType(o.OutletType))
	s.statusIdx = selectIndexOf(outletStatusOptions, o.Status)
	s.inputs[poLabel].SetValue(o.Label)
	s.inputs[poLocationDesc].SetValue(o.LocationDescription)
	s.inputs[poNotes].SetValue(o.Notes)
	s.needsReview = o.NeedsReview
}

func (s *PowerOutletFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *PowerOutletFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if outletIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && outletIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *PowerOutletFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.exitCmd()
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
		if id, ok := s.currentFieldID(); ok && outletFieldKind(id) == akPicker {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch outletFieldKind(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.needsReview = !s.needsReview
		}
		return s, nil
	case akSelect:
		switch m.String() {
		case " ", "right":
			s.cycleSelect(id, +1)
		case "left":
			s.cycleSelect(id, -1)
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

func (s *PowerOutletFormScreen) moveCursor(delta int) {
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
func (s *PowerOutletFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *PowerOutletFormScreen) cycleSelect(id, delta int) {
	switch id {
	case poOutletType:
		n := len(nemaOutletTypeOptions)
		s.outletTypeIdx = (s.outletTypeIdx + delta + n) % n
	case poStatus:
		n := len(outletStatusOptions)
		s.statusIdx = (s.statusIdx + delta + n) % n
	}
}

func (s *PowerOutletFormScreen) openPicker(id int) {
	s.phase = elecPhasePick
	s.pickField = id
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()
	s.pickCursor = 0
	var sel *int
	switch id {
	case poCircuit:
		sel = s.circuitID
	case poLocation:
		sel = s.locationID
	case poDisconnect:
		sel = s.disconnectID
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

func (s *PowerOutletFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []itemPickOption
	// disconnect is optional → a "(none)" row clears it; circuit + location are
	// required and get no clear row.
	if s.pickField == poDisconnect {
		opts = append(opts, itemPickOption{clear: true, label: "(none — no dedicated disconnect)"})
	}
	add := func(id int, label string) {
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemPickOption{id: id, label: label})
		}
	}
	switch s.pickField {
	case poCircuit:
		for _, c := range s.circuits {
			add(c.ID, circuitPickLabel(c))
		}
	case poLocation:
		for _, l := range s.locations {
			add(l.ID, l.Name)
		}
	case poDisconnect:
		for _, d := range s.disconnects {
			add(d.ID, disconnectPickLabel(d))
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *PowerOutletFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		s.closePicker()
	case jdePickCommit:
		s.commitPick()
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
		s.applyPickFilter()
		return s, cmd
	}
	return s, nil
}

func (s *PowerOutletFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *PowerOutletFormScreen) closePicker() {
	s.phase = elecPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *PowerOutletFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		var target **int
		switch s.pickField {
		case poCircuit:
			target = &s.circuitID
		case poLocation:
			target = &s.locationID
		case poDisconnect:
			target = &s.disconnectID
		}
		if target != nil {
			if opt.clear {
				*target = nil
			} else {
				id := opt.id
				*target = &id
			}
		}
	}
	s.closePicker()
}

func (s *PowerOutletFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.outletID
	return s, func() tea.Msg {
		var o *omsapi.PowerOutletDetail
		var e error
		if edit {
			o, e = deps.OMS.UpdatePowerOutlet(ctx, id, body)
		} else {
			o, e = deps.OMS.CreatePowerOutlet(ctx, body)
		}
		return outletSavedMsg{outlet: o, err: e}
	}
}

func (s *PowerOutletFormScreen) buildPayload() (omsapi.PowerOutletWrite, error) {
	var w omsapi.PowerOutletWrite
	if s.circuitID == nil {
		return w, errors.New("circuit is required")
	}
	if s.locationID == nil {
		return w, errors.New("location is required")
	}
	w = omsapi.PowerOutletWrite{
		Circuit:             *s.circuitID,
		Location:            *s.locationID,
		Disconnect:          s.disconnectID,
		OutletType:          nemaOutletTypeOptions[s.outletTypeIdx].value,
		Label:               strings.TrimSpace(s.inputs[poLabel].Value()),
		LocationDescription: strings.TrimSpace(s.inputs[poLocationDesc].Value()),
		Status:              outletStatusOptions[s.statusIdx].value,
		Notes:               strings.TrimSpace(s.inputs[poNotes].Value()),
		NeedsReview:         s.needsReview,
	}
	return w, nil
}

// exitCmd routes back to the outlet's circuit outlets list (create + edit are
// always reached from there), falling back to the panels list when the circuit
// is somehow unknown.
func (s *PowerOutletFormScreen) exitCmd() tea.Cmd {
	circuitID := s.presetCircuitID
	if s.circuitID != nil {
		circuitID = *s.circuitID
	}
	if circuitID != 0 {
		return SwitchTo(WSFacilities, NewCircuitOutletsScreen(s.deps, circuitID, s.exitPanelID(circuitID), s.circuitLabel(circuitID)))
	}
	return SwitchTo(WSFacilities, NewElectricalPanelsScreen(s.deps))
}

// exitPanelID resolves the panel owning the given circuit from the loaded
// catalogue (the circuit may have been changed in the form), falling back to the
// panelID the form was opened with.
func (s *PowerOutletFormScreen) exitPanelID(circuitID int) int {
	for _, c := range s.circuits {
		if c.ID == circuitID {
			return c.PanelID
		}
	}
	return s.panelID
}

func (s *PowerOutletFormScreen) circuitLabel(circuitID int) string {
	for _, c := range s.circuits {
		if c.ID == circuitID {
			return circuitShortLabel(c)
		}
	}
	if s.outlet != nil && s.outlet.CircuitLabel != "" {
		return s.outlet.CircuitLabel
	}
	return ""
}

func (s *PowerOutletFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == elecPhasePick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *PowerOutletFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: three FK rows Ctrl-E opens,
// two bounded sets, and the rest typed into.
func (s *PowerOutletFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   outletFieldLabel[id],
			Width:   outletFieldWidth(id),
			Hint:    outletFieldHint[id],
			Focused: i == s.cursor,
		}
		switch outletFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.needsReview)
		case akSelect:
			f.Kind, f.Value = jdeChoice, s.selectValue(id)
			if id == poOutletType {
				// The wire value, not the label: a NEMA code is what the record
				// stores and what a wrong pick 400s on.
				f.Hint = s.outletTypeCode()
			}
		case akPicker:
			value, dim := s.pickerValue(id)
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

func (s *PowerOutletFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Outlet"))
	for i, id := range s.fields {
		l.AddRow(i, renderJDEField(fields[i], elecLabelWidth, s.bodyWidth()))
		// The set around the FOCUSED choice row, so seventeen receptacle types
		// are never cycled blind (jdeOptionStrip returns nothing for a yes/no).
		if i == s.cursor {
			if strip := s.selectStrip(id); strip != "" {
				l.AddRow(i, jdeStripIndent(elecLabelWidth)+StyleMuted.Render(strip))
			}
		}
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
func (s *PowerOutletFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *PowerOutletFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch outletFieldKind(id) {
		case akToggle, akSelect:
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

// selectValue is the BARE label between a choice row's angle brackets — the
// renderer owns the brackets.
func (s *PowerOutletFormScreen) selectValue(id int) string {
	opts, idx := s.selectOptions(id)
	if idx >= 0 && idx < len(opts) {
		return opts[idx].label
	}
	return ""
}

func (s *PowerOutletFormScreen) selectOptions(id int) ([]selectOption, int) {
	switch id {
	case poOutletType:
		return nemaOutletTypeOptions, s.outletTypeIdx
	case poStatus:
		return outletStatusOptions, s.statusIdx
	}
	return nil, -1
}

func (s *PowerOutletFormScreen) selectStrip(id int) string {
	opts, idx := s.selectOptions(id)
	if len(opts) == 0 {
		return ""
	}
	labels := make([]string, len(opts))
	for i, o := range opts {
		labels[i] = o.label
	}
	return jdeOptionStrip(labels, idx, jdeStripWidth(s.bodyWidth(), elecLabelWidth))
}

// outletTypeCode is the wire code the outlet-type cursor is sitting on.
func (s *PowerOutletFormScreen) outletTypeCode() string {
	if s.outletTypeIdx >= 0 && s.outletTypeIdx < len(nemaOutletTypeOptions) {
		return nemaOutletTypeOptions[s.outletTypeIdx].value
	}
	return ""
}

// pickerValue is an FK row's text, and whether it is an empty state rather than
// a value. It returns PLAIN text with a flag instead of pre-styled muted text,
// because a focused row has to be able to reverse-video the whole field — an
// inner reset sequence would end the highlight partway through it.
func (s *PowerOutletFormScreen) pickerValue(id int) (string, bool) {
	switch id {
	case poCircuit:
		if s.circuitID == nil {
			return "(not set)", true
		}
		for _, c := range s.circuits {
			if c.ID == *s.circuitID {
				return circuitPickLabel(c), false
			}
		}
		if s.outlet != nil && s.outlet.CircuitLabel != "" {
			return s.outlet.CircuitLabel, false
		}
		return fmt.Sprintf("circuit #%d", *s.circuitID), false
	case poLocation:
		if s.locationID == nil {
			return "(not set)", true
		}
		for _, l := range s.locations {
			if l.ID == *s.locationID {
				return l.Name, false
			}
		}
		if s.outlet != nil && s.outlet.LocationName != "" {
			return s.outlet.LocationName, false
		}
		return fmt.Sprintf("#%d", *s.locationID), false
	case poDisconnect:
		if s.disconnectID == nil {
			return "(none — breaker serves)", true
		}
		for _, d := range s.disconnects {
			if d.ID == *s.disconnectID {
				return disconnectPickLabel(d), false
			}
		}
		if s.outlet != nil && s.outlet.DisconnectLabel != "" {
			return s.outlet.DisconnectLabel, false
		}
		return fmt.Sprintf("disconnect #%d", *s.disconnectID), false
	}
	return "", false
}

// pickView builds the open picker's pinned header and its option list. The note
// explains what the clear row does, which is the one thing about these lists
// that is not self-evident.
func (s *PowerOutletFormScreen) pickView() (jdeHeader, *jdeLines) {
	title, note, empty := "Location", "", "(no matching locations)"
	switch s.pickField {
	case poCircuit:
		title, empty = "Circuit", "(no matching circuits)"
	case poDisconnect:
		title, empty = "Disconnect", "(no matching disconnects)"
		note = "Row 1 is none — the outlet is then killed by its breaker alone."
	}
	return jdePickList{
		Title:  title,
		For:    strings.TrimSpace(s.inputs[poLabel].Value()),
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  empty,
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *PowerOutletFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", len(s.pickOptions), s.bodyPagesForBar(body, len(s.pickOptions), len(header), jdePickBarCeiling("Select", "Cancel")))
}

func (s *PowerOutletFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}

// ===========================================================================
// DisconnectFormScreen
// ===========================================================================

const (
	dcCircuit = iota
	dcLocation
	dcLabel
	dcDisconnectType
	dcAmperage
	dcFuseSize
	dcIsLockable
	dcLOTODevices
	dcNotes
	dcNeedsReview
	dcFieldMax
)

var disconnectFieldLabel = map[int]string{
	dcCircuit: "Circuit",
	// "(optional)" is not a label. A columnar form marks what is REQUIRED and
	// lets the rest read as optional — and a parenthetical here would widen the
	// column all five electrical forms share.
	dcLocation:       "Location",
	dcLabel:          "Label",
	dcDisconnectType: "Disconnect type",
	dcAmperage:       "Amperage",
	dcFuseSize:       "Fuse size",
	dcIsLockable:     "Lockable",
	dcLOTODevices:    "Required LOTO devices",
	dcNotes:          "Notes",
	dcNeedsReview:    "Needs review",
}

// disconnectFieldHint carries what the placeholders and the label
// parentheticals used to say (see outletFieldHint). needs_review's hint is what
// the old footer note said: the backend's clean() also raises the flag for
// inconsistent fuse/lock combinations, so an operator is not surprised by a
// review flag they did not set — and it belongs ON the row it is about.
var disconnectFieldHint = map[int]string{
	dcCircuit:     "required",
	dcLabel:       "required",
	dcAmperage:    "switch rating, amps",
	dcFuseSize:    "fused only · e.g. 30A class J",
	dcNeedsReview: "may be auto-set for odd fuse/lock combos",
}

// disconnectFieldWidth sizes the input areas that are not the default.
func disconnectFieldWidth(id int) int {
	switch id {
	case dcAmperage:
		return 10
	case dcFuseSize:
		return 18
	case dcLabel, dcNotes:
		return 40
	}
	return 0
}

func disconnectFieldKind(id int) assetFieldKind {
	switch id {
	case dcLabel, dcFuseSize, dcNotes:
		return akText
	case dcAmperage:
		return akNumber
	case dcIsLockable, dcNeedsReview:
		return akToggle
	case dcDisconnectType:
		return akSelect
	case dcCircuit, dcLocation:
		return akPicker
	case dcLOTODevices:
		return akMultiPicker
	}
	return akText
}

func disconnectIsTextKind(id int) bool {
	k := disconnectFieldKind(id)
	return k == akText || k == akNumber
}

type DisconnectFormScreen struct {
	deps         Deps
	edit         bool
	disconnectID int
	// presetCircuitID pre-selects the circuit when opened from a circuit's
	// disconnect list; panelID is carried through for exit routing.
	presetCircuitID int
	panelID         int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	circuits    []omsapi.PowerCircuitDetail
	locations   []omsapi.Location
	lotoDevices []omsapi.LOTODevice
	disconnect  *omsapi.DisconnectDetail
	refArrived  bool
	recArrived  bool

	jdeScreen

	inputs []textinput.Model

	isLockable       bool
	needsReview      bool
	disconnectTypIdx int

	circuitID  *int
	locationID *int
	// lotoDeviceIDs is the sorted set of selected LOTODevice pks sent as the
	// write-only required_loto_device_ids list.
	lotoDeviceIDs []int

	fields []int
	cursor int

	phase       elecFormPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickOptions []itemPickOption
}

type disconnectRefLoadedMsg struct {
	circuits    []omsapi.PowerCircuitDetail
	locations   []omsapi.Location
	lotoDevices []omsapi.LOTODevice
	err         error
}

type disconnectRecordLoadedMsg struct {
	disconnect *omsapi.DisconnectDetail
	err        error
}

type disconnectSavedMsg struct {
	disconnect *omsapi.DisconnectDetail
	err        error
}

// NewDisconnectFormScreen opens create mode when disconnectID == 0.
// presetCircuitID pre-selects the parent circuit; panelID scopes the exit route.
func NewDisconnectFormScreen(deps Deps, disconnectID, presetCircuitID, panelID int) *DisconnectFormScreen {
	s := &DisconnectFormScreen{
		deps:            deps,
		edit:            disconnectID != 0,
		disconnectID:    disconnectID,
		presetCircuitID: presetCircuitID,
		panelID:         panelID,
		loading:         true,
		// Model default: is_lockable=True. Create mode starts lockable; edit mode
		// hydrates the persisted value.
		isLockable: true,
	}
	if !s.edit && presetCircuitID != 0 {
		id := presetCircuitID
		s.circuitID = &id
	}
	s.inputs = make([]textinput.Model, dcFieldMax)
	for id := 0; id < dcFieldMax; id++ {
		if !disconnectIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = disconnectCharLimit(id)
		ti.Placeholder = disconnectPlaceholder(id)
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{dcCircuit, dcLocation, dcLabel, dcDisconnectType, dcAmperage, dcFuseSize, dcIsLockable, dcLOTODevices, dcNotes, dcNeedsReview}
	s.syncFocus()
	return s
}

func disconnectCharLimit(id int) int {
	switch id {
	case dcLabel:
		return 120
	case dcFuseSize:
		return 20
	case dcAmperage:
		return 6
	case dcNotes:
		return 1000
	}
	return 120
}

// disconnectPlaceholder is empty for every field now — see
// disconnectFieldHint.
func disconnectPlaceholder(id int) string { return "" }

func (s *DisconnectFormScreen) Title() string {
	if s.edit {
		return "Edit disconnect"
	}
	return "New disconnect"
}

func (s *DisconnectFormScreen) WantsRawInput() bool { return true }

func (s *DisconnectFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *DisconnectFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *DisconnectFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return disconnectRefLoadedMsg{err: err}
		}
		msg := disconnectRefLoadedMsg{locations: locs.Results}
		if cks, err := deps.OMS.ListAllPowerCircuits(ctx); err == nil {
			msg.circuits = cks
		}
		// The LOTO device catalogue is best-effort: an empty picker still lets the
		// operator save a disconnect with no required devices.
		if devs, err := deps.OMS.ListAllLOTODevices(ctx); err == nil {
			msg.lotoDevices = devs
		}
		return msg
	}
}

func (s *DisconnectFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.disconnectID
	return func() tea.Msg {
		d, err := deps.OMS.GetDisconnect(ctx, id)
		return disconnectRecordLoadedMsg{disconnect: d, err: err}
	}
}

func (s *DisconnectFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case disconnectRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.circuits = m.circuits
			s.locations = m.locations
			s.lotoDevices = m.lotoDevices
		}
		return s, s.maybeFinalizeLoad()
	case disconnectRecordLoadedMsg:
		s.recArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.disconnect = m.disconnect
		}
		return s, s.maybeFinalizeLoad()
	case disconnectSavedMsg:
		s.saving = false
		if m.err != nil {
			s.errMsg = m.err.Error()
			return s, Status("save failed: "+m.err.Error(), StatusError)
		}
		verb := "created"
		if s.edit {
			verb = "updated"
		}
		return s, tea.Batch(Status("disconnect "+verb, StatusOK), s.exitCmd())
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.exitCmd()
			}
			return s, nil
		}
		if s.phase == elecPhasePick {
			return s.updatePickPhase(m)
		}
		return s.updateFormPhase(m)
	}

	if s.phase == elecPhasePick {
		var cmd tea.Cmd
		s.pickSearch, cmd = s.pickSearch.Update(msg)
		return s, cmd
	}
	if id, ok := s.currentFieldID(); ok && disconnectIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *DisconnectFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.recArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.disconnect != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *DisconnectFormScreen) hydrate() {
	d := s.disconnect
	cir := d.Circuit
	s.circuitID = &cir
	s.locationID = copyIntPtr(d.Location)
	s.inputs[dcLabel].SetValue(d.Label)
	s.disconnectTypIdx = selectIndexOf(disconnectTypeOptions, d.DisconnectType)
	if d.Amperage != nil {
		s.inputs[dcAmperage].SetValue(strconv.Itoa(*d.Amperage))
	}
	s.inputs[dcFuseSize].SetValue(d.FuseSize)
	s.isLockable = d.IsLockable
	s.inputs[dcNotes].SetValue(d.Notes)
	s.needsReview = d.NeedsReview
	// Seed the multi-picker selection from the embedded device objects.
	s.lotoDeviceIDs = s.lotoDeviceIDs[:0]
	for _, dev := range d.RequiredLOTODevices {
		if id, ok := dev.IntID(); ok {
			s.lotoDeviceIDs = append(s.lotoDeviceIDs, id)
		}
	}
	sort.Ints(s.lotoDeviceIDs)
}

func (s *DisconnectFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *DisconnectFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if disconnectIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && disconnectIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *DisconnectFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.exitCmd()
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
		// EDIT opens whatever the highlighted row IS: the two FK pickers and the
		// LOTO multi picker.
		if id, ok := s.currentFieldID(); ok {
			switch disconnectFieldKind(id) {
			case akPicker, akMultiPicker:
				s.openPicker(id)
				return s, textinput.Blink
			}
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch disconnectFieldKind(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.flipToggle(id)
		}
		return s, nil
	case akSelect:
		switch m.String() {
		case " ", "right":
			s.cycleSelect(+1)
		case "left":
			s.cycleSelect(-1)
		}
		return s, nil
	case akPicker, akMultiPicker:
		// A picker row has nothing to type into and no accelerators left.
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *DisconnectFormScreen) moveCursor(delta int) {
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
func (s *DisconnectFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *DisconnectFormScreen) flipToggle(id int) {
	switch id {
	case dcIsLockable:
		s.isLockable = !s.isLockable
	case dcNeedsReview:
		s.needsReview = !s.needsReview
	}
}

func (s *DisconnectFormScreen) cycleSelect(delta int) {
	n := len(disconnectTypeOptions)
	s.disconnectTypIdx = (s.disconnectTypIdx + delta + n) % n
}

func (s *DisconnectFormScreen) openPicker(id int) {
	s.phase = elecPhasePick
	s.pickField = id
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
	s.applyPickFilter()
	s.pickCursor = 0
	var sel *int
	switch id {
	case dcCircuit:
		sel = s.circuitID
	case dcLocation:
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

func (s *DisconnectFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []itemPickOption
	// location is optional → a "(none)" clear row; circuit is required.
	if s.pickField == dcLocation {
		opts = append(opts, itemPickOption{clear: true, label: "(none — integral / no separate switch)"})
	}
	add := func(id int, label string) {
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemPickOption{id: id, label: label})
		}
	}
	switch s.pickField {
	case dcCircuit:
		for _, c := range s.circuits {
			add(c.ID, circuitPickLabel(c))
		}
	case dcLocation:
		for _, l := range s.locations {
			add(l.ID, l.Name)
		}
	case dcLOTODevices:
		for _, dev := range s.lotoDevices {
			id, ok := dev.IntID()
			if !ok {
				continue
			}
			add(id, lotoDevicePickLabel(dev))
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *DisconnectFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch act, delta := jdePickKey(m); act {
	case jdePickCancel:
		// In the MULTI picker esc is "done", not "cancel": every toggle was
		// applied as it was made, so there is nothing to roll back. The bar says
		// so rather than pretending one of the two exits is special.
		s.closePicker()
	case jdePickCommit:
		if s.pickField == dcLOTODevices {
			// Enter toggles membership here — the always-live filter owns space,
			// and a multi picker's "select" IS the toggle (sc-0zvi).
			s.toggleCurrentLOTO()
			return s, nil
		}
		s.commitPick()
	case jdePickMove:
		s.movePick(delta)
	case jdePickPage:
		header, body := s.pickView()
		if next, ok := s.pageRow(body, s.pickCursor, len(s.pickOptions), delta, len(header),
			s.pickBar(header, body), s.pickBarItems(jdeCeilingRows, true)); ok {
			s.pickCursor = next
		}
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

func (s *DisconnectFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *DisconnectFormScreen) closePicker() {
	s.phase = elecPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *DisconnectFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		var target **int
		switch s.pickField {
		case dcCircuit:
			target = &s.circuitID
		case dcLocation:
			target = &s.locationID
		}
		if target != nil {
			if opt.clear {
				*target = nil
			} else {
				id := opt.id
				*target = &id
			}
		}
	}
	s.closePicker()
}

func (s *DisconnectFormScreen) toggleCurrentLOTO() {
	if s.pickCursor < 0 || s.pickCursor >= len(s.pickOptions) {
		return
	}
	opt := s.pickOptions[s.pickCursor]
	if opt.clear {
		return
	}
	for i, id := range s.lotoDeviceIDs {
		if id == opt.id {
			s.lotoDeviceIDs = append(s.lotoDeviceIDs[:i], s.lotoDeviceIDs[i+1:]...)
			return
		}
	}
	s.lotoDeviceIDs = append(s.lotoDeviceIDs, opt.id)
	sort.Ints(s.lotoDeviceIDs)
}

func (s *DisconnectFormScreen) lotoSelected(id int) bool {
	for _, d := range s.lotoDeviceIDs {
		if d == id {
			return true
		}
	}
	return false
}

func (s *DisconnectFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.disconnectID
	return s, func() tea.Msg {
		var d *omsapi.DisconnectDetail
		var e error
		if edit {
			d, e = deps.OMS.UpdateDisconnect(ctx, id, body)
		} else {
			d, e = deps.OMS.CreateDisconnect(ctx, body)
		}
		return disconnectSavedMsg{disconnect: d, err: e}
	}
}

func (s *DisconnectFormScreen) buildPayload() (omsapi.DisconnectWrite, error) {
	var w omsapi.DisconnectWrite
	if s.circuitID == nil {
		return w, errors.New("circuit is required")
	}
	label := strings.TrimSpace(s.inputs[dcLabel].Value())
	if label == "" {
		return w, errors.New("label is required")
	}
	amperage, err := parseOptionalPositiveInt(s.inputs[dcAmperage].Value(), "amperage")
	if err != nil {
		return w, err
	}
	// Copy the selection into a fresh non-nil slice so it always marshals to a
	// JSON array (never null) even when empty.
	ids := append([]int{}, s.lotoDeviceIDs...)
	w = omsapi.DisconnectWrite{
		Circuit:               *s.circuitID,
		Location:              s.locationID,
		Label:                 label,
		DisconnectType:        disconnectTypeOptions[s.disconnectTypIdx].value,
		Amperage:              amperage,
		FuseSize:              strings.TrimSpace(s.inputs[dcFuseSize].Value()),
		IsLockable:            s.isLockable,
		Notes:                 strings.TrimSpace(s.inputs[dcNotes].Value()),
		RequiredLOTODeviceIDs: ids,
		NeedsReview:           s.needsReview,
	}
	return w, nil
}

func (s *DisconnectFormScreen) exitCmd() tea.Cmd {
	circuitID := s.presetCircuitID
	if s.circuitID != nil {
		circuitID = *s.circuitID
	}
	if circuitID != 0 {
		return SwitchTo(WSFacilities, NewCircuitDisconnectsScreen(s.deps, circuitID, s.exitPanelID(circuitID), s.circuitLabel(circuitID)))
	}
	return SwitchTo(WSFacilities, NewElectricalPanelsScreen(s.deps))
}

func (s *DisconnectFormScreen) exitPanelID(circuitID int) int {
	for _, c := range s.circuits {
		if c.ID == circuitID {
			return c.PanelID
		}
	}
	return s.panelID
}

func (s *DisconnectFormScreen) circuitLabel(circuitID int) string {
	for _, c := range s.circuits {
		if c.ID == circuitID {
			return circuitShortLabel(c)
		}
	}
	if s.disconnect != nil && s.disconnect.CircuitLabel != "" {
		return s.disconnect.CircuitLabel
	}
	return ""
}

func (s *DisconnectFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == elecPhasePick {
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *DisconnectFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: two FK rows and one MULTI
// picker Ctrl-E opens, three bounded sets, and the rest typed into.
func (s *DisconnectFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   disconnectFieldLabel[id],
			Width:   disconnectFieldWidth(id),
			Hint:    disconnectFieldHint[id],
			Focused: i == s.cursor,
		}
		switch disconnectFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.toggleState(id))
		case akSelect:
			f.Kind, f.Value = jdeChoice, s.selectValue()
		case akPicker:
			value, dim := s.pickerValue(id)
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E picks"
			}
		case akMultiPicker:
			value, dim := s.lotoValue()
			f.Kind, f.Value, f.Dim = jdeValue, value, dim
			if f.Focused {
				f.Hint = "Ctrl-E chooses"
			}
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

// toggleState is a bool row's current value.
func (s *DisconnectFormScreen) toggleState(id int) bool {
	if id == dcIsLockable {
		return s.isLockable
	}
	return s.needsReview
}

func (s *DisconnectFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Disconnect"))
	for i, id := range s.fields {
		l.AddRow(i, renderJDEField(fields[i], elecLabelWidth, s.bodyWidth()))
		if i == s.cursor && id == dcDisconnectType {
			labels := make([]string, len(disconnectTypeOptions))
			for j, o := range disconnectTypeOptions {
				labels[j] = o.label
			}
			strip := jdeOptionStrip(labels, s.disconnectTypIdx, jdeStripWidth(s.bodyWidth(), elecLabelWidth))
			if strip != "" {
				l.AddRow(i, jdeStripIndent(elecLabelWidth)+StyleMuted.Render(strip))
			}
		}
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
func (s *DisconnectFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyPagesForBar(body, len(s.fields), 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *DisconnectFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch disconnectFieldKind(id) {
		case akToggle, akSelect:
			items = append(items, actionBarItem{"←→", "Change"})
		case akPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Pick"})
		case akMultiPicker:
			items = append(items, actionBarItem{"Ctrl-E", "Choose"})
		}
	}
	if paging {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// selectValue is the BARE label between the type row's angle brackets — the
// renderer owns the brackets.
func (s *DisconnectFormScreen) selectValue() string {
	if s.disconnectTypIdx >= 0 && s.disconnectTypIdx < len(disconnectTypeOptions) {
		return disconnectTypeOptions[s.disconnectTypIdx].label
	}
	return ""
}

// pickerValue is an FK row's text, and whether it is an empty state rather than
// a value. It returns PLAIN text with a flag instead of pre-styled muted text,
// because a focused row has to be able to reverse-video the whole field.
func (s *DisconnectFormScreen) pickerValue(id int) (string, bool) {
	switch id {
	case dcCircuit:
		if s.circuitID == nil {
			return "(not set)", true
		}
		for _, c := range s.circuits {
			if c.ID == *s.circuitID {
				return circuitPickLabel(c), false
			}
		}
		if s.disconnect != nil && s.disconnect.CircuitLabel != "" {
			return s.disconnect.CircuitLabel, false
		}
		return fmt.Sprintf("circuit #%d", *s.circuitID), false
	case dcLocation:
		if s.locationID == nil {
			// Terse on purpose: with the focused row's "Ctrl-E picks" hint after
			// it, the picker's fuller wording would run past the pane and be
			// clipped (TestJDESweepC_RowsFitTheBody).
			return "(none — no separate switch)", true
		}
		for _, l := range s.locations {
			if l.ID == *s.locationID {
				return l.Name, false
			}
		}
		if s.disconnect != nil && s.disconnect.LocationName != "" {
			return s.disconnect.LocationName, false
		}
		return fmt.Sprintf("#%d", *s.locationID), false
	}
	return "", false
}

// lotoValue is the multi-picker row's text, and whether it is an empty state.
func (s *DisconnectFormScreen) lotoValue() (string, bool) {
	if len(s.lotoDeviceIDs) == 0 {
		return "(none)", true
	}
	names := make([]string, 0, len(s.lotoDeviceIDs))
	for _, id := range s.lotoDeviceIDs {
		name := fmt.Sprintf("#%d", id)
		for _, dev := range s.lotoDevices {
			if did, ok := dev.IntID(); ok && did == id {
				name = lotoDeviceShortLabel(dev)
				break
			}
		}
		names = append(names, name)
	}
	return strings.Join(names, ", "), false
}

// pickView builds the open picker's pinned header and its option list. The LOTO
// picker marks membership with a checkbox in the label itself — the layer never
// learns what is being picked, so the caller draws the mark.
func (s *DisconnectFormScreen) pickView() (jdeHeader, *jdeLines) {
	title, note, empty := "Circuit", "", "(no matching circuits)"
	switch s.pickField {
	case dcLocation:
		title, empty = "Location", "(no matching locations)"
		note = "Row 1 is none — an integral switch has no location of its own."
	case dcLOTODevices:
		title, empty = "Required LOTO devices", "(no devices — add them in the OMS web LOTO inventory)"
		note = "Enter toggles a device on and off; esc is done."
	}
	return jdePickList{
		Title:  title,
		For:    strings.TrimSpace(s.inputs[dcLabel].Value()),
		Note:   note,
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  s.pickRowLabel,
		Dim:    func(i int) bool { return s.pickOptions[i].clear },
		Cursor: s.pickCursor,
		Empty:  empty,
	}.render(s.bodyWidth())
}

func (s *DisconnectFormScreen) pickRowLabel(i int) string {
	opt := s.pickOptions[i]
	if s.pickField != dcLOTODevices || opt.clear {
		return opt.label
	}
	mark := "[ ] "
	if s.lotoSelected(opt.id) {
		mark = "[x] "
	}
	return mark + opt.label
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *DisconnectFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	n := len(s.pickOptions)
	return s.pickBarItems(n, s.bodyPagesForBar(body, n, len(header), s.pickBarItems(jdeCeilingRows, true)))
}

// pickBarItems is pickBar for a given paging state. The multi picker's esc is
// "done", not "cancel" — its toggles were applied as they were made, so there
// is nothing left to undo.
func (s *DisconnectFormScreen) pickBarItems(count int, paging bool) []actionBarItem {
	if s.pickField == dcLOTODevices {
		return jdePickBarWith("Toggle", "Done", count, paging)
	}
	return jdePickBar("Select", count, paging)
}

func (s *DisconnectFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}

// ---------------------------------------------------------------------------
// Shared pick-row label helpers (circuit / disconnect / LOTO device).
// ---------------------------------------------------------------------------

// circuitPickLabel is the rich circuit row for a picker: "PanelName / pos N ·
// label". circuitShortLabel is the terse form for breadcrumbs.
func circuitPickLabel(c omsapi.PowerCircuitDetail) string {
	label := circuitShortLabel(c)
	if c.PanelName != "" {
		return c.PanelName + " · " + label
	}
	return label
}

func circuitShortLabel(c omsapi.PowerCircuitDetail) string {
	if c.Label != "" {
		return c.Label
	}
	if c.BreakerLabel != "" {
		return c.BreakerLabel
	}
	return fmt.Sprintf("circuit #%d", c.ID)
}

func disconnectPickLabel(d omsapi.DisconnectDetail) string {
	label := d.Label
	if label == "" {
		label = fmt.Sprintf("disconnect #%d", d.ID)
	}
	if d.DisconnectType != "" {
		label += " (" + d.DisconnectType + ")"
	}
	return label
}

func lotoDevicePickLabel(dev omsapi.LOTODevice) string {
	kind := dev.DeviceTypeDisplay
	if kind == "" {
		kind = dev.DeviceType
	}
	label := lotoDeviceShortLabel(dev)
	if dev.Status != "" && dev.Status != "available" {
		label += " — " + dev.Status
	}
	if kind != "" {
		return kind + " · " + label
	}
	return label
}

func lotoDeviceShortLabel(dev omsapi.LOTODevice) string {
	if dev.Label != "" {
		return dev.Label
	}
	if id, ok := dev.IntID(); ok {
		return fmt.Sprintf("device #%d", id)
	}
	return "device"
}
