// Electrical CRUD forms — PowerPanel / PowerBreaker / PowerCircuit create+edit.
//
// TUI counterpart to the web PowerPanelFormPage / PowerBreakerFormPage /
// PowerCircuitFormPage. They mirror the FULL writable serializer field set for
// the power-distribution backbone so an operator can build out a panel →
// breaker → circuit tree from the workstation without the browser
// ([[scantty-parity-program]]). The structure follows asset_form.go's
// field-id-iota + field-kind (text/number/toggle/select/picker) + form/pick
// sub-phase idiom; the pickers reuse itemPickOption (all electrical FKs are int
// pks) and the field kinds reuse asset_form.go's ak* constants.
//
// The leaf tier — PowerOutlet + Disconnect CRUD and the LOTO
// required_loto_devices multi-picker — lives in electrical_leaf_forms.go (Bead
// B); the breaker serializer here doesn't expose required_loto_devices.
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

// ---------------------------------------------------------------------------
// Choice options — exact codes from the electrical serializers. A wrong code
// 400s at save, so these are the parity contract, not cosmetic.
// ---------------------------------------------------------------------------

var panelPhaseConfigOptions = []selectOption{
	{"single", "Single-phase"},
	{"split", "Split-phase"},
	{"three", "Three-phase"},
}

var panelBreakerTypeOptions = []selectOption{
	{"", "(unspecified)"},
	{"SQUARE_D_QO", "Square D QO"},
	{"SQUARE_D_HOMELINE", "Square D Homeline"},
	{"EATON_CH", "Eaton CH"},
	{"EATON_BR", "Eaton BR"},
	{"SIEMENS_QP", "Siemens QP"},
	{"GE_Q_LINE", "GE Q-Line"},
	{"FEDERAL_PACIFIC", "Federal Pacific"},
	{"PUSHMATIC", "Pushmatic"},
	{"DIN_RAIL", "DIN rail"},
	{"OTHER", "Other"},
}

var panelNumberingOptions = []selectOption{
	{"top_down", "Top-down"},
	{"bottom_up", "Bottom-up"},
}

var breakerPoleCountOptions = []selectOption{
	{"1", "1-pole"},
	{"2", "2-pole"},
	{"3", "3-pole"},
}

var breakerPhaseOptions = []selectOption{
	{"A", "A"},
	{"B", "B"},
	{"C", "C"},
	{"AB", "AB"},
	{"BC", "BC"},
	{"AC", "AC"},
	{"ABC", "ABC"},
}

var breakerStatusOptions = []selectOption{
	{"active", "Active"},
	{"spare", "Spare"},
	{"locked_out", "Locked out"},
}

var breakerReviewStatusOptions = []selectOption{
	{"ok", "OK"},
	{"needs_attention", "Needs attention"},
	{"circuit_moved", "Circuit moved"},
}

// breakerCriticalCategoryOptions has NO blank row: when is_critical is true a
// category is required (the backend 400s on is_critical=true + blank category),
// so the select only offers the five real codes and defaults to the first.
var breakerCriticalCategoryOptions = []selectOption{
	{"fire_alarm", "Fire alarm"},
	{"emergency_lighting", "Emergency lighting"},
	{"exit_sign", "Exit sign"},
	{"egress_door", "Egress door"},
	{"life_safety_other", "Life safety (other)"},
}

func selectIndexOf(opts []selectOption, value string) int {
	for i, o := range opts {
		if o.value == value {
			return i
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// computeBreakerPhase — Go replica of the web computePhase helper. The backend
// never derives phase (it stores what you POST, default "A"), so to match the
// web UI the breaker form computes phase from the panel's phase_configuration,
// the breaker's primary slot (parsed from position) and pole_count. ok is false
// for a blank / tandem ("14/16") / non-numeric / <1 position, in which case the
// caller leaves the phase select untouched and shows a "set manually" hint.
// ---------------------------------------------------------------------------

func computeBreakerPhase(position string, poleCount int, phaseConfig string) (string, bool) {
	slot, ok := breakerPrimarySlot(position)
	if !ok {
		return "", false
	}
	poles := poleCount
	if poles < 1 {
		poles = 1
	}
	if poles > 3 {
		poles = 3
	}
	startRow := (slot - 1) / 2
	covered := map[byte]bool{}
	for i := 0; i < poles; i++ {
		covered[phaseForRow(startRow+i, phaseConfig)] = true
	}
	var out strings.Builder
	for _, p := range []byte{'A', 'B', 'C'} {
		if covered[p] {
			out.WriteByte(p)
		}
	}
	return out.String(), true
}

// breakerPrimarySlot mirrors the web primarySlot: bail (auto-calc skipped) on an
// empty, tandem ('/' or '-') or non-numeric/<1 position.
func breakerPrimarySlot(position string) (int, bool) {
	position = strings.TrimSpace(position)
	if position == "" || strings.ContainsAny(position, "/-") {
		return 0, false
	}
	n, err := strconv.Atoi(position)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// phaseForRow maps a bus row to its single phase: single → always A; split
// (modulus 2) → A,B,A,B…; three (modulus 3) → A,B,C,A,B,C…
func phaseForRow(row int, config string) byte {
	if config == "single" {
		return 'A'
	}
	modulus := 2
	if config == "three" {
		modulus = 3
	}
	idx := ((row % modulus) + modulus) % modulus
	return []byte{'A', 'B', 'C'}[idx]
}

// ---------------------------------------------------------------------------
// Shared small parse helpers.
// ---------------------------------------------------------------------------

func parsePositiveInt(raw, label string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s is required", label)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number", label)
	}
	if n < 1 {
		return 0, fmt.Errorf("%s must be at least 1", label)
	}
	return n, nil
}

func parseOptionalPositiveInt(raw, label string) (*int, error) {
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

// ===========================================================================
// PowerPanelFormScreen
// ===========================================================================

const (
	ppName = iota
	ppLocation
	ppPhaseConfig
	ppVoltage
	ppMainAmp
	ppBreakerType
	ppNumbering
	ppManufacturer
	ppModel
	ppInstallDate
	ppFedBy
	ppNotes
	ppNeedsReview
	ppFieldMax
)

var panelFieldLabel = map[int]string{
	ppName:         "Name",
	ppLocation:     "Location",
	ppPhaseConfig:  "Phase configuration",
	ppVoltage:      "Voltage",
	ppMainAmp:      "Main breaker amperage",
	ppBreakerType:  "Breaker family",
	ppNumbering:    "Numbering direction",
	ppManufacturer: "Manufacturer",
	ppModel:        "Model",
	ppInstallDate:  "Install date",
	ppFedBy:        "Fed by (upstream circuit)",
	ppNotes:        "Notes",
	ppNeedsReview:  "Needs review",
}

func panelFieldKind(id int) assetFieldKind {
	switch id {
	case ppName, ppManufacturer, ppModel, ppInstallDate, ppNotes:
		return akText
	case ppVoltage, ppMainAmp:
		return akNumber
	case ppNeedsReview:
		return akToggle
	case ppPhaseConfig, ppBreakerType, ppNumbering:
		return akSelect
	case ppLocation, ppFedBy:
		return akPicker
	}
	return akText
}

func panelIsTextKind(id int) bool {
	k := panelFieldKind(id)
	return k == akText || k == akNumber
}

type PowerPanelFormScreen struct {
	deps    Deps
	edit    bool
	panelID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	locations  []omsapi.Location
	circuits   []omsapi.PowerCircuitDetail
	panel      *omsapi.PowerPanelDetail
	refArrived bool
	recArrived bool

	terminalHeight int

	inputs []textinput.Model

	needsReview   bool
	phaseCfgIdx   int
	breakerTypIdx int
	numberingIdx  int

	locationID *int
	fedByID    *int

	fields []int
	cursor int

	phase       elecFormPhase
	pickField   int
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemPickOption
}

type elecFormPhase int

const (
	elecPhaseForm elecFormPhase = iota
	elecPhasePick
)

type panelRefLoadedMsg struct {
	locations []omsapi.Location
	circuits  []omsapi.PowerCircuitDetail
	err       error
}

type panelRecordLoadedMsg struct {
	panel *omsapi.PowerPanelDetail
	err   error
}

type panelSavedMsg struct {
	panel *omsapi.PowerPanelDetail
	err   error
}

// NewPowerPanelFormScreen opens create mode when panelID == 0, else edit mode.
func NewPowerPanelFormScreen(deps Deps, panelID int) *PowerPanelFormScreen {
	s := &PowerPanelFormScreen{
		deps:          deps,
		edit:          panelID != 0,
		panelID:       panelID,
		loading:       true,
		phaseCfgIdx:   selectIndexOf(panelPhaseConfigOptions, "split"),
		numberingIdx:  selectIndexOf(panelNumberingOptions, "top_down"),
		breakerTypIdx: 0,
	}
	s.inputs = make([]textinput.Model, ppFieldMax)
	for id := 0; id < ppFieldMax; id++ {
		if !panelIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = panelCharLimit(id)
		ti.Placeholder = panelPlaceholder(id)
		s.inputs[id] = ti
	}
	if !s.edit {
		s.inputs[ppVoltage].SetValue("240") // serializer default
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{ppName, ppLocation, ppPhaseConfig, ppVoltage, ppMainAmp, ppBreakerType,
		ppNumbering, ppManufacturer, ppModel, ppInstallDate, ppFedBy, ppNotes, ppNeedsReview}
	s.syncFocus()
	return s
}

func panelCharLimit(id int) int {
	switch id {
	case ppName, ppManufacturer, ppModel:
		return 100
	case ppInstallDate:
		return 10
	case ppVoltage, ppMainAmp:
		return 6
	case ppNotes:
		return 1000
	}
	return 100
}

func panelPlaceholder(id int) string {
	switch id {
	case ppName:
		return "panel name (unique per location)"
	case ppVoltage:
		return "240"
	case ppMainAmp:
		return "optional (e.g. 200)"
	case ppInstallDate:
		return "YYYY-MM-DD (optional)"
	case ppManufacturer, ppModel:
		return "optional"
	case ppNotes:
		return "optional"
	}
	return ""
}

func (s *PowerPanelFormScreen) Title() string {
	if s.edit {
		if s.panel != nil && s.panel.Name != "" {
			return "Edit panel: " + s.panel.Name
		}
		return "Edit panel"
	}
	return "New panel"
}

func (s *PowerPanelFormScreen) WantsRawInput() bool { return true }

func (s *PowerPanelFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *PowerPanelFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PowerPanelFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		locs, err := deps.OMS.ListLocations(ctx, nil)
		if err != nil {
			return panelRefLoadedMsg{err: err}
		}
		msg := panelRefLoadedMsg{locations: locs.Results}
		// fed_by is an optional sub-panel feeder; a hiccup loading circuits
		// leaves the picker empty rather than blocking panel creation.
		if cks, err := deps.OMS.ListAllPowerCircuits(ctx); err == nil {
			msg.circuits = cks
		}
		return msg
	}
}

func (s *PowerPanelFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.panelID
	return func() tea.Msg {
		p, err := deps.OMS.GetPowerPanelRecord(ctx, id)
		return panelRecordLoadedMsg{panel: p, err: err}
	}
}

func (s *PowerPanelFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case panelRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.locations = m.locations
			s.circuits = m.circuits
		}
		return s, s.maybeFinalizeLoad()
	case panelRecordLoadedMsg:
		s.recArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.panel = m.panel
		}
		return s, s.maybeFinalizeLoad()
	case panelSavedMsg:
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
		id := s.panelID
		if m.panel != nil {
			name = m.panel.Name
			id = m.panel.ID
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("panel %s: %s", verb, name), StatusOK),
			s.exitCmd(id),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.exitCmd(s.panelID)
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
	if id, ok := s.currentFieldID(); ok && panelIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *PowerPanelFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.recArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.panel != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *PowerPanelFormScreen) hydrate() {
	p := s.panel
	s.inputs[ppName].SetValue(p.Name)
	loc := p.Location
	s.locationID = &loc
	s.phaseCfgIdx = selectIndexOf(panelPhaseConfigOptions, p.PhaseConfiguration)
	s.inputs[ppVoltage].SetValue(strconv.Itoa(p.Voltage))
	if p.MainBreakerAmperage != nil {
		s.inputs[ppMainAmp].SetValue(strconv.Itoa(*p.MainBreakerAmperage))
	}
	s.breakerTypIdx = selectIndexOf(panelBreakerTypeOptions, p.BreakerType)
	s.numberingIdx = selectIndexOf(panelNumberingOptions, p.NumberingDirection)
	s.inputs[ppManufacturer].SetValue(p.Manufacturer)
	s.inputs[ppModel].SetValue(p.Model)
	s.inputs[ppInstallDate].SetValue(p.InstallDate)
	s.inputs[ppNotes].SetValue(p.Notes)
	s.needsReview = p.NeedsReview
	s.fedByID = copyIntPtr(p.FedBy)
}

func (s *PowerPanelFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *PowerPanelFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if panelIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && panelIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *PowerPanelFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.exitCmd(s.panelID)
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
	switch panelFieldKind(id) {
	case akToggle:
		if m.String() == " " {
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

func (s *PowerPanelFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *PowerPanelFormScreen) cycleSelect(id, delta int) {
	switch id {
	case ppPhaseConfig:
		n := len(panelPhaseConfigOptions)
		s.phaseCfgIdx = (s.phaseCfgIdx + delta + n) % n
	case ppBreakerType:
		n := len(panelBreakerTypeOptions)
		s.breakerTypIdx = (s.breakerTypIdx + delta + n) % n
	case ppNumbering:
		n := len(panelNumberingOptions)
		s.numberingIdx = (s.numberingIdx + delta + n) % n
	}
}

func (s *PowerPanelFormScreen) openPicker(id int) {
	s.phase = elecPhasePick
	s.pickField = id
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()
	s.pickCursor = 0
	var sel *int
	switch id {
	case ppLocation:
		sel = s.locationID
	case ppFedBy:
		sel = s.fedByID
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

func (s *PowerPanelFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []itemPickOption
	// Location is required (no clear row); fed_by is optional, so it gets a
	// "(none)" row to unset a sub-panel feeder / return to a utility-fed main.
	if s.pickField == ppFedBy {
		opts = append(opts, itemPickOption{clear: true, label: "(none — utility-fed main)"})
	}
	add := func(id int, label string) {
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemPickOption{id: id, label: label})
		}
	}
	switch s.pickField {
	case ppLocation:
		for _, l := range s.locations {
			add(l.ID, l.Name)
		}
	case ppFedBy:
		// Exclude this panel's own circuits in edit mode — the backend rejects a
		// self-feed, and the web picker filters them out likewise.
		for _, c := range s.circuits {
			if s.edit && c.PanelID == s.panelID {
				continue
			}
			label := c.BreakerLabel
			if c.Label != "" {
				label = c.Label + " — " + label
			}
			if label == "" {
				label = fmt.Sprintf("circuit #%d", c.ID)
			}
			add(c.ID, label)
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *PowerPanelFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		s.phase = elecPhaseForm
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

func (s *PowerPanelFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		opt := s.pickOptions[s.pickCursor]
		var target **int
		switch s.pickField {
		case ppLocation:
			target = &s.locationID
		case ppFedBy:
			target = &s.fedByID
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
	s.phase = elecPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *PowerPanelFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.panelID
	return s, func() tea.Msg {
		var p *omsapi.PowerPanelDetail
		var e error
		if edit {
			p, e = deps.OMS.UpdatePowerPanel(ctx, id, body)
		} else {
			p, e = deps.OMS.CreatePowerPanel(ctx, body)
		}
		return panelSavedMsg{panel: p, err: e}
	}
}

func (s *PowerPanelFormScreen) buildPayload() (omsapi.PowerPanelWrite, error) {
	var w omsapi.PowerPanelWrite
	name := strings.TrimSpace(s.inputs[ppName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	if s.locationID == nil {
		return w, errors.New("location is required")
	}
	voltage, err := parsePositiveInt(s.inputs[ppVoltage].Value(), "voltage")
	if err != nil {
		return w, err
	}
	mainAmp, err := parseOptionalPositiveInt(s.inputs[ppMainAmp].Value(), "main breaker amperage")
	if err != nil {
		return w, err
	}
	var installDate *string
	if d := strings.TrimSpace(s.inputs[ppInstallDate].Value()); d != "" {
		if !validAssetDate(d) {
			return w, errors.New("install date must be YYYY-MM-DD")
		}
		installDate = &d
	}
	w = omsapi.PowerPanelWrite{
		Location:            *s.locationID,
		Name:                name,
		PhaseConfiguration:  panelPhaseConfigOptions[s.phaseCfgIdx].value,
		Voltage:             voltage,
		MainBreakerAmperage: mainAmp,
		BreakerType:         panelBreakerTypeOptions[s.breakerTypIdx].value,
		NumberingDirection:  panelNumberingOptions[s.numberingIdx].value,
		Manufacturer:        strings.TrimSpace(s.inputs[ppManufacturer].Value()),
		Model:               strings.TrimSpace(s.inputs[ppModel].Value()),
		InstallDate:         installDate,
		Notes:               strings.TrimSpace(s.inputs[ppNotes].Value()),
		NeedsReview:         s.needsReview,
		FedBy:               s.fedByID,
	}
	return w, nil
}

// exitCmd routes back after save/cancel: edit → the panel's topology detail
// (preserving context), create → the panels list.
func (s *PowerPanelFormScreen) exitCmd(panelID int) tea.Cmd {
	if s.edit && panelID != 0 {
		return SwitchTo(WSFacilities, NewElectricalPanelDetailScreen(s.deps, panelID))
	}
	return SwitchTo(WSFacilities, NewElectricalPanelsScreen(s.deps))
}

func (s *PowerPanelFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == elecPhasePick {
		return elecPickView(s.pickWhat(), &s.pickSearch, s.pickTyping, s.pickOptions, s.pickCursor)
	}
	return s.viewForm()
}

func (s *PowerPanelFormScreen) pickWhat() string {
	if s.pickField == ppFedBy {
		return "upstream circuit"
	}
	return "location"
}

func (s *PowerPanelFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(s.helpText()) + "\n\n")
	visible := elecVisibleRows(s.terminalHeight)
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

func (s *PowerPanelFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := panelFieldLabel[id]
	var value string
	switch panelFieldKind(id) {
	case akText, akNumber:
		value = s.inputs[id].View()
	case akToggle:
		value = elecToggleLabel(s.needsReview)
	case akSelect:
		value = s.selectLabel(id)
	case akPicker:
		value = s.pickerLabel(id)
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *PowerPanelFormScreen) selectLabel(id int) string {
	switch id {
	case ppPhaseConfig:
		return elecSelectLabel(panelPhaseConfigOptions, s.phaseCfgIdx)
	case ppBreakerType:
		return elecSelectLabel(panelBreakerTypeOptions, s.breakerTypIdx)
	case ppNumbering:
		return elecSelectLabel(panelNumberingOptions, s.numberingIdx)
	}
	return ""
}

func (s *PowerPanelFormScreen) pickerLabel(id int) string {
	switch id {
	case ppLocation:
		if s.locationID == nil {
			return StyleMuted.Render("(required — space to pick)")
		}
		for _, l := range s.locations {
			if l.ID == *s.locationID {
				return l.Name
			}
		}
		return fmt.Sprintf("#%d", *s.locationID)
	case ppFedBy:
		if s.fedByID == nil {
			return StyleMuted.Render("(none — utility-fed main)")
		}
		for _, c := range s.circuits {
			if c.ID == *s.fedByID {
				if c.BreakerLabel != "" {
					return c.BreakerLabel
				}
				return fmt.Sprintf("circuit #%d", c.ID)
			}
		}
		return fmt.Sprintf("circuit #%d", *s.fedByID)
	}
	return ""
}

func (s *PowerPanelFormScreen) helpText() string {
	return elecFieldHelp(panelFieldKind, s.currentFieldID)
}

// ===========================================================================
// PowerBreakerFormScreen
// ===========================================================================

const (
	pbPanel = iota
	pbPosition
	pbPoleCount
	pbAmperage
	pbPhase
	pbStatus
	pbReviewStatus
	pbReviewNote // conditional: review_status != "ok"
	pbIsCritical
	pbCriticalCategory // conditional: is_critical
	pbCriticalNote     // conditional: is_critical
	pbLabel
	pbNotes
	pbNeedsReview
	pbFieldMax
)

var breakerFieldLabel = map[int]string{
	pbPanel:            "Panel",
	pbPosition:         "Position (slot)",
	pbPoleCount:        "Pole count",
	pbAmperage:         "Amperage",
	pbPhase:            "Phase",
	pbStatus:           "Status",
	pbReviewStatus:     "Review flag",
	pbReviewNote:       "Review note",
	pbIsCritical:       "Critical load",
	pbCriticalCategory: "Critical category",
	pbCriticalNote:     "Critical note",
	pbLabel:            "Label",
	pbNotes:            "Notes",
	pbNeedsReview:      "Needs review",
}

func breakerFieldKind(id int) assetFieldKind {
	switch id {
	case pbPosition, pbReviewNote, pbCriticalNote, pbLabel, pbNotes:
		return akText
	case pbAmperage:
		return akNumber
	case pbIsCritical, pbNeedsReview:
		return akToggle
	case pbPoleCount, pbPhase, pbStatus, pbReviewStatus, pbCriticalCategory:
		return akSelect
	case pbPanel:
		return akPicker
	}
	return akText
}

func breakerIsTextKind(id int) bool {
	k := breakerFieldKind(id)
	return k == akText || k == akNumber
}

type PowerBreakerFormScreen struct {
	deps          Deps
	edit          bool
	breakerID     int
	presetPanelID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	panels     []omsapi.PowerPanel
	breaker    *omsapi.PowerBreakerDetail
	refArrived bool
	recArrived bool

	terminalHeight int

	inputs []textinput.Model

	panelID      *int
	poleCountIdx int
	phaseIdx     int
	statusIdx    int
	reviewStIdx  int
	critCatIdx   int
	isCritical   bool
	needsReview  bool

	// phaseManuallySet mirrors the web flag: once the user edits the Phase
	// select (or in edit mode from the start) auto-calc stops clobbering it.
	phaseManuallySet bool

	fields []int
	cursor int

	phase       elecFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemPickOption
}

type breakerRefLoadedMsg struct {
	panels []omsapi.PowerPanel
	err    error
}

type breakerRecordLoadedMsg struct {
	breaker *omsapi.PowerBreakerDetail
	err     error
}

type breakerSavedMsg struct {
	breaker *omsapi.PowerBreakerDetail
	err     error
}

// NewPowerBreakerFormScreen opens create mode when breakerID == 0. presetPanelID
// (non-zero) pre-selects the panel when the form is opened from a panel's
// breaker list, so the operator doesn't re-pick it.
func NewPowerBreakerFormScreen(deps Deps, breakerID, presetPanelID int) *PowerBreakerFormScreen {
	s := &PowerBreakerFormScreen{
		deps:          deps,
		edit:          breakerID != 0,
		breakerID:     breakerID,
		presetPanelID: presetPanelID,
		loading:       true,
		poleCountIdx:  selectIndexOf(breakerPoleCountOptions, "1"),
		phaseIdx:      selectIndexOf(breakerPhaseOptions, "A"),
		statusIdx:     selectIndexOf(breakerStatusOptions, "active"),
		reviewStIdx:   selectIndexOf(breakerReviewStatusOptions, "ok"),
	}
	// Edit mode starts with phase locked so hydration isn't auto-clobbered;
	// create mode leaves it unlocked so computePhase pre-fills as fields change.
	s.phaseManuallySet = s.edit
	if !s.edit && presetPanelID != 0 {
		id := presetPanelID
		s.panelID = &id
	}
	s.inputs = make([]textinput.Model, pbFieldMax)
	for id := 0; id < pbFieldMax; id++ {
		if !breakerIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = breakerCharLimit(id)
		ti.Placeholder = breakerPlaceholder(id)
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.rebuildFields()
	s.syncFocus()
	return s
}

func breakerCharLimit(id int) int {
	switch id {
	case pbPosition:
		return 20
	case pbAmperage:
		return 6
	case pbLabel:
		return 200
	case pbReviewNote, pbCriticalNote, pbNotes:
		return 1000
	}
	return 200
}

func breakerPlaceholder(id int) string {
	switch id {
	case pbPosition:
		return "slot e.g. 12 (or 14/16 tandem)"
	case pbAmperage:
		return "required (e.g. 20)"
	case pbLabel:
		return "optional label"
	case pbReviewNote, pbCriticalNote, pbNotes:
		return "optional"
	}
	return ""
}

func (s *PowerBreakerFormScreen) Title() string {
	if s.edit {
		if s.breaker != nil && s.breaker.Position != "" {
			return "Edit breaker: pos " + s.breaker.Position
		}
		return "Edit breaker"
	}
	return "New breaker"
}

func (s *PowerBreakerFormScreen) WantsRawInput() bool { return true }

func (s *PowerBreakerFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *PowerBreakerFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PowerBreakerFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	return func() tea.Msg {
		panels, err := deps.OMS.ListPowerPanels(ctx)
		return breakerRefLoadedMsg{panels: panels, err: err}
	}
}

func (s *PowerBreakerFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.breakerID
	return func() tea.Msg {
		b, err := deps.OMS.GetPowerBreaker(ctx, id)
		return breakerRecordLoadedMsg{breaker: b, err: err}
	}
}

func (s *PowerBreakerFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case breakerRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.panels = m.panels
		}
		return s, s.maybeFinalizeLoad()
	case breakerRecordLoadedMsg:
		s.recArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.breaker = m.breaker
		}
		return s, s.maybeFinalizeLoad()
	case breakerSavedMsg:
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
			Status("breaker "+verb, StatusOK),
			s.exitCmd(),
		)
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
	if id, ok := s.currentFieldID(); ok && breakerIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *PowerBreakerFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.recArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.breaker != nil {
		s.hydrate()
	}
	s.rebuildFields()
	s.syncFocus()
	return nil
}

func (s *PowerBreakerFormScreen) hydrate() {
	b := s.breaker
	panel := b.Panel
	s.panelID = &panel
	s.inputs[pbPosition].SetValue(b.Position)
	if b.PoleCount > 0 {
		s.poleCountIdx = selectIndexOf(breakerPoleCountOptions, strconv.Itoa(b.PoleCount))
	}
	s.inputs[pbAmperage].SetValue(strconv.Itoa(b.Amperage))
	s.phaseIdx = selectIndexOf(breakerPhaseOptions, b.Phase)
	s.statusIdx = selectIndexOf(breakerStatusOptions, b.Status)
	s.reviewStIdx = selectIndexOf(breakerReviewStatusOptions, b.ReviewStatus)
	s.inputs[pbReviewNote].SetValue(b.ReviewNote)
	s.isCritical = b.IsCritical
	if b.CriticalCategory != "" {
		s.critCatIdx = selectIndexOf(breakerCriticalCategoryOptions, b.CriticalCategory)
	}
	s.inputs[pbCriticalNote].SetValue(b.CriticalNote)
	s.inputs[pbLabel].SetValue(b.Label)
	s.inputs[pbNotes].SetValue(b.Notes)
	s.needsReview = b.NeedsReview
}

func (s *PowerBreakerFormScreen) rebuildFields() {
	focused := -1
	if id, ok := s.currentFieldID(); ok {
		focused = id
	}
	f := []int{pbPanel, pbPosition, pbPoleCount, pbAmperage, pbPhase, pbStatus, pbReviewStatus}
	if breakerReviewStatusOptions[s.reviewStIdx].value != "ok" {
		f = append(f, pbReviewNote)
	}
	f = append(f, pbIsCritical)
	if s.isCritical {
		f = append(f, pbCriticalCategory, pbCriticalNote)
	}
	f = append(f, pbLabel, pbNotes, pbNeedsReview)
	s.fields = f
	if focused >= 0 {
		if idx := indexOfField(s.fields, focused); idx >= 0 {
			s.cursor = idx
		}
	}
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *PowerBreakerFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *PowerBreakerFormScreen) setCursorToField(id int) {
	if idx := indexOfField(s.fields, id); idx >= 0 {
		s.cursor = idx
	}
}

func (s *PowerBreakerFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if breakerIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && breakerIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *PowerBreakerFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.exitCmd()
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
	switch breakerFieldKind(id) {
	case akToggle:
		if m.String() == " " {
			s.flipToggle(id)
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
		if m.String() == " " {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		// Typing in the position field re-runs auto-calc (unless the phase was
		// manually pinned) — same as the web form.
		if id == pbPosition {
			s.maybeAutoPhase()
		}
		return s, cmd
	}
}

func (s *PowerBreakerFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *PowerBreakerFormScreen) flipToggle(id int) {
	switch id {
	case pbIsCritical:
		s.isCritical = !s.isCritical
		s.rebuildFields()
		s.syncFocus()
	case pbNeedsReview:
		s.needsReview = !s.needsReview
	}
}

func (s *PowerBreakerFormScreen) cycleSelect(id, delta int) {
	switch id {
	case pbPoleCount:
		n := len(breakerPoleCountOptions)
		s.poleCountIdx = (s.poleCountIdx + delta + n) % n
		s.maybeAutoPhase()
	case pbPhase:
		n := len(breakerPhaseOptions)
		s.phaseIdx = (s.phaseIdx + delta + n) % n
		s.phaseManuallySet = true // user pinned it
	case pbStatus:
		n := len(breakerStatusOptions)
		s.statusIdx = (s.statusIdx + delta + n) % n
	case pbReviewStatus:
		n := len(breakerReviewStatusOptions)
		s.reviewStIdx = (s.reviewStIdx + delta + n) % n
		s.rebuildFields()
		s.syncFocus()
	case pbCriticalCategory:
		n := len(breakerCriticalCategoryOptions)
		s.critCatIdx = (s.critCatIdx + delta + n) % n
	}
}

// maybeAutoPhase re-derives the phase from the selected panel's configuration,
// the position and pole count — but only while auto-calc is live (create mode,
// phase not yet manually pinned) and a panel is selected. A tandem/blank
// position yields no result and leaves the phase untouched (hint shown).
func (s *PowerBreakerFormScreen) maybeAutoPhase() {
	if s.phaseManuallySet || s.panelID == nil {
		return
	}
	cfg := s.selectedPanelPhaseConfig()
	if cfg == "" {
		return
	}
	pole, _ := strconv.Atoi(breakerPoleCountOptions[s.poleCountIdx].value)
	phase, ok := computeBreakerPhase(s.inputs[pbPosition].Value(), pole, cfg)
	if !ok {
		return
	}
	s.phaseIdx = selectIndexOf(breakerPhaseOptions, phase)
}

func (s *PowerBreakerFormScreen) selectedPanelPhaseConfig() string {
	if s.panelID == nil {
		return ""
	}
	for _, p := range s.panels {
		if p.ID == *s.panelID {
			return p.PhaseConfiguration
		}
	}
	return ""
}

func (s *PowerBreakerFormScreen) openPicker() {
	s.phase = elecPhasePick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()
	s.pickCursor = 0
	if s.panelID != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.id == *s.panelID {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *PowerBreakerFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	// Panel is required — no "(none)" row.
	var opts []itemPickOption
	for _, p := range s.panels {
		label := p.Name
		meta := []string{}
		if p.LocationName != "" {
			meta = append(meta, p.LocationName)
		}
		if p.PhaseConfiguration != "" {
			meta = append(meta, p.PhaseConfiguration)
		}
		if len(meta) > 0 {
			label = fmt.Sprintf("%s (%s)", p.Name, strings.Join(meta, ", "))
		}
		if q == "" || strings.Contains(strings.ToLower(label), q) {
			opts = append(opts, itemPickOption{id: p.ID, label: label})
		}
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func (s *PowerBreakerFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		s.phase = elecPhaseForm
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

func (s *PowerBreakerFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		id := s.pickOptions[s.pickCursor].id
		s.panelID = &id
		s.maybeAutoPhase() // panel's phase config may change the auto phase
	}
	s.phase = elecPhaseForm
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *PowerBreakerFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.breakerID
	return s, func() tea.Msg {
		var b *omsapi.PowerBreakerDetail
		var e error
		if edit {
			b, e = deps.OMS.UpdatePowerBreaker(ctx, id, body)
		} else {
			b, e = deps.OMS.CreatePowerBreaker(ctx, body)
		}
		return breakerSavedMsg{breaker: b, err: e}
	}
}

func (s *PowerBreakerFormScreen) buildPayload() (omsapi.PowerBreakerWrite, error) {
	var w omsapi.PowerBreakerWrite
	if s.panelID == nil {
		return w, errors.New("panel is required")
	}
	position := strings.TrimSpace(s.inputs[pbPosition].Value())
	if position == "" {
		return w, errors.New("position is required")
	}
	amperage, err := parsePositiveInt(s.inputs[pbAmperage].Value(), "amperage")
	if err != nil {
		return w, err
	}
	pole, _ := strconv.Atoi(breakerPoleCountOptions[s.poleCountIdx].value)

	// Critical pairing: the backend 400s on is_critical XOR critical_category
	// mismatch, so send a category only when critical, and blank both otherwise.
	criticalCategory := ""
	criticalNote := ""
	if s.isCritical {
		criticalCategory = breakerCriticalCategoryOptions[s.critCatIdx].value
		criticalNote = strings.TrimSpace(s.inputs[pbCriticalNote].Value())
	}

	// review_note only meaningful when the review flag is off "ok".
	reviewNote := ""
	if breakerReviewStatusOptions[s.reviewStIdx].value != "ok" {
		reviewNote = strings.TrimSpace(s.inputs[pbReviewNote].Value())
	}

	w = omsapi.PowerBreakerWrite{
		Panel:            *s.panelID,
		Position:         position,
		PoleCount:        pole,
		Amperage:         amperage,
		Phase:            breakerPhaseOptions[s.phaseIdx].value,
		Status:           breakerStatusOptions[s.statusIdx].value,
		ReviewStatus:     breakerReviewStatusOptions[s.reviewStIdx].value,
		ReviewNote:       reviewNote,
		Label:            strings.TrimSpace(s.inputs[pbLabel].Value()),
		Notes:            strings.TrimSpace(s.inputs[pbNotes].Value()),
		NeedsReview:      s.needsReview,
		IsCritical:       s.isCritical,
		CriticalCategory: criticalCategory,
		CriticalNote:     criticalNote,
	}
	return w, nil
}

func (s *PowerBreakerFormScreen) exitCmd() tea.Cmd {
	panelID := 0
	if s.panelID != nil {
		panelID = *s.panelID
	} else if s.presetPanelID != 0 {
		panelID = s.presetPanelID
	}
	if panelID != 0 {
		return SwitchTo(WSFacilities, NewPanelBreakersScreen(s.deps, panelID, s.panelName(panelID)))
	}
	return SwitchTo(WSFacilities, NewElectricalPanelsScreen(s.deps))
}

func (s *PowerBreakerFormScreen) panelName(id int) string {
	for _, p := range s.panels {
		if p.ID == id {
			return p.Name
		}
	}
	if s.breaker != nil && s.breaker.PanelName != "" {
		return s.breaker.PanelName
	}
	return ""
}

func (s *PowerBreakerFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == elecPhasePick {
		return elecPickView("panel", &s.pickSearch, s.pickTyping, s.pickOptions, s.pickCursor)
	}
	return s.viewForm()
}

func (s *PowerBreakerFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(elecFieldHelp(breakerFieldKind, s.currentFieldID)) + "\n\n")
	visible := elecVisibleRows(s.terminalHeight)
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

func (s *PowerBreakerFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := breakerFieldLabel[id]
	var value string
	switch breakerFieldKind(id) {
	case akText, akNumber:
		value = s.inputs[id].View()
	case akToggle:
		switch id {
		case pbIsCritical:
			value = elecToggleLabel(s.isCritical)
		case pbNeedsReview:
			value = elecToggleLabel(s.needsReview)
		}
	case akSelect:
		value = s.selectLabel(id)
	case akPicker:
		value = s.panelLabel()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *PowerBreakerFormScreen) selectLabel(id int) string {
	switch id {
	case pbPoleCount:
		return elecSelectLabel(breakerPoleCountOptions, s.poleCountIdx)
	case pbPhase:
		lbl := elecSelectLabel(breakerPhaseOptions, s.phaseIdx)
		if !s.phaseManuallySet {
			if _, ok := breakerPrimarySlot(s.inputs[pbPosition].Value()); ok {
				lbl += StyleMuted.Render("  (auto)")
			} else if strings.TrimSpace(s.inputs[pbPosition].Value()) != "" {
				lbl += StyleMuted.Render("  (auto skipped — tandem, set manually)")
			}
		}
		return lbl
	case pbStatus:
		return elecSelectLabel(breakerStatusOptions, s.statusIdx)
	case pbReviewStatus:
		return elecSelectLabel(breakerReviewStatusOptions, s.reviewStIdx)
	case pbCriticalCategory:
		return elecSelectLabel(breakerCriticalCategoryOptions, s.critCatIdx)
	}
	return ""
}

func (s *PowerBreakerFormScreen) panelLabel() string {
	if s.panelID == nil {
		return StyleMuted.Render("(required — space to pick)")
	}
	for _, p := range s.panels {
		if p.ID == *s.panelID {
			return p.Name
		}
	}
	if s.breaker != nil && s.breaker.PanelName != "" {
		return s.breaker.PanelName
	}
	return fmt.Sprintf("#%d", *s.panelID)
}

// ===========================================================================
// PowerCircuitFormScreen
// ===========================================================================

const (
	pcBreaker = iota
	pcLabel
	pcConductorSize
	pcConductorLength
	pcNotes
	pcNeedsReview
	pcFieldMax
)

var circuitFieldLabel = map[int]string{
	pcBreaker:         "Breaker",
	pcLabel:           "Label",
	pcConductorSize:   "Conductor size",
	pcConductorLength: "Conductor length (ft)",
	pcNotes:           "Notes",
	pcNeedsReview:     "Needs review",
}

func circuitFieldKind(id int) assetFieldKind {
	switch id {
	case pcLabel, pcConductorSize, pcNotes:
		return akText
	case pcConductorLength:
		return akNumber
	case pcNeedsReview:
		return akToggle
	case pcBreaker:
		return akPicker
	}
	return akText
}

func circuitIsTextKind(id int) bool {
	k := circuitFieldKind(id)
	return k == akText || k == akNumber
}

type PowerCircuitFormScreen struct {
	deps      Deps
	edit      bool
	circuitID int
	// panelID scopes the breaker picker to one panel's breakers (the form is
	// always reached from a breaker's circuit list, so the panel is known).
	panelID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	breakers   []omsapi.PowerBreakerDetail
	circuit    *omsapi.PowerCircuitDetail
	refArrived bool
	recArrived bool

	terminalHeight int

	inputs []textinput.Model

	breakerID   *int
	needsReview bool

	fields []int
	cursor int

	phase       elecFormPhase
	pickCursor  int
	pickSearch  textinput.Model
	pickTyping  bool
	pickOptions []itemPickOption
}

type circuitRefLoadedMsg struct {
	breakers []omsapi.PowerBreakerDetail
	err      error
}

type circuitRecordLoadedMsg struct {
	circuit *omsapi.PowerCircuitDetail
	err     error
}

type circuitSavedMsg struct {
	circuit *omsapi.PowerCircuitDetail
	err     error
}

// NewPowerCircuitFormScreen opens create mode when circuitID == 0.
// presetBreakerID pre-selects the breaker; panelID scopes the breaker picker.
func NewPowerCircuitFormScreen(deps Deps, circuitID, presetBreakerID, panelID int) *PowerCircuitFormScreen {
	s := &PowerCircuitFormScreen{
		deps:      deps,
		edit:      circuitID != 0,
		circuitID: circuitID,
		panelID:   panelID,
		loading:   true,
	}
	if !s.edit && presetBreakerID != 0 {
		id := presetBreakerID
		s.breakerID = &id
	}
	s.inputs = make([]textinput.Model, pcFieldMax)
	for id := 0; id < pcFieldMax; id++ {
		if !circuitIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = circuitCharLimit(id)
		ti.Placeholder = circuitPlaceholder(id)
		s.inputs[id] = ti
	}
	s.pickSearch = textinput.New()
	s.pickSearch.Prompt = ""
	s.pickSearch.Placeholder = "filter"
	s.pickSearch.CharLimit = 60

	s.fields = []int{pcBreaker, pcLabel, pcConductorSize, pcConductorLength, pcNotes, pcNeedsReview}
	s.syncFocus()
	return s
}

func circuitCharLimit(id int) int {
	switch id {
	case pcLabel:
		return 200
	case pcConductorSize:
		return 20
	case pcConductorLength:
		return 6
	case pcNotes:
		return 1000
	}
	return 200
}

func circuitPlaceholder(id int) string {
	switch id {
	case pcLabel:
		return "optional label"
	case pcConductorSize:
		return "e.g. 12 AWG (optional)"
	case pcConductorLength:
		return "optional feet"
	case pcNotes:
		return "optional"
	}
	return ""
}

func (s *PowerCircuitFormScreen) Title() string {
	if s.edit {
		return "Edit circuit"
	}
	return "New circuit"
}

func (s *PowerCircuitFormScreen) WantsRawInput() bool { return true }

func (s *PowerCircuitFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{s.loadRefData(), textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *PowerCircuitFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *PowerCircuitFormScreen) loadRefData() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	panelID := s.panelID
	return func() tea.Msg {
		if panelID == 0 {
			return circuitRefLoadedMsg{}
		}
		brs, err := deps.OMS.ListPowerBreakers(ctx, panelID)
		return circuitRefLoadedMsg{breakers: brs, err: err}
	}
}

func (s *PowerCircuitFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.circuitID
	return func() tea.Msg {
		c, err := deps.OMS.GetPowerCircuit(ctx, id)
		return circuitRecordLoadedMsg{circuit: c, err: err}
	}
}

func (s *PowerCircuitFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		return s, nil
	case circuitRefLoadedMsg:
		s.refArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.breakers = m.breakers
		}
		return s, s.maybeFinalizeLoad()
	case circuitRecordLoadedMsg:
		s.recArrived = true
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.circuit = m.circuit
		}
		return s, s.maybeFinalizeLoad()
	case circuitSavedMsg:
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
			Status("circuit "+verb, StatusOK),
			s.exitCmd(),
		)
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
	if id, ok := s.currentFieldID(); ok && circuitIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *PowerCircuitFormScreen) maybeFinalizeLoad() tea.Cmd {
	if !s.refArrived {
		return nil
	}
	if s.edit && !s.recArrived {
		return nil
	}
	s.loading = false
	if s.loadErr == "" && s.edit && s.circuit != nil {
		s.hydrate()
	}
	s.syncFocus()
	return nil
}

func (s *PowerCircuitFormScreen) hydrate() {
	c := s.circuit
	brk := c.Breaker
	s.breakerID = &brk
	s.inputs[pcLabel].SetValue(c.Label)
	s.inputs[pcConductorSize].SetValue(c.ConductorSize)
	if c.ConductorLengthFt != nil {
		s.inputs[pcConductorLength].SetValue(strconv.Itoa(*c.ConductorLengthFt))
	}
	s.inputs[pcNotes].SetValue(c.Notes)
	s.needsReview = c.NeedsReview
}

func (s *PowerCircuitFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *PowerCircuitFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if circuitIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && circuitIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *PowerCircuitFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
	switch m.String() {
	case "esc":
		return s, s.exitCmd()
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
	switch circuitFieldKind(id) {
	case akToggle:
		if m.String() == " " {
			s.needsReview = !s.needsReview
		}
		return s, nil
	case akPicker:
		if m.String() == " " {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *PowerCircuitFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

func (s *PowerCircuitFormScreen) openPicker() {
	s.phase = elecPhasePick
	s.pickTyping = false
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.applyPickFilter()
	s.pickCursor = 0
	if s.breakerID != nil {
		for i, o := range s.pickOptions {
			if !o.clear && o.id == *s.breakerID {
				s.pickCursor = i
				break
			}
		}
	}
}

func (s *PowerCircuitFormScreen) applyPickFilter() {
	q := strings.ToLower(strings.TrimSpace(s.pickSearch.Value()))
	var opts []itemPickOption
	for _, b := range s.breakers {
		opts = appendBreakerPickOption(opts, b, q)
	}
	s.pickOptions = opts
	if s.pickCursor >= len(s.pickOptions) {
		s.pickCursor = 0
	}
}

func appendBreakerPickOption(opts []itemPickOption, b omsapi.PowerBreakerDetail, q string) []itemPickOption {
	label := fmt.Sprintf("pos %s · %dA · %dp", b.Position, b.Amperage, b.PoleCount)
	if b.Label != "" {
		label += " — " + b.Label
	}
	if q == "" || strings.Contains(strings.ToLower(label), q) {
		return append(opts, itemPickOption{id: b.ID, label: label})
	}
	return opts
}

func (s *PowerCircuitFormScreen) updatePickPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		s.phase = elecPhaseForm
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
			id := s.pickOptions[s.pickCursor].id
			s.breakerID = &id
		}
		s.phase = elecPhaseForm
		s.pickTyping = false
		s.pickSearch.SetValue("")
		s.pickSearch.Blur()
		s.syncFocus()
	}
	return s, nil
}

func (s *PowerCircuitFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.circuitID
	return s, func() tea.Msg {
		var c *omsapi.PowerCircuitDetail
		var e error
		if edit {
			c, e = deps.OMS.UpdatePowerCircuit(ctx, id, body)
		} else {
			c, e = deps.OMS.CreatePowerCircuit(ctx, body)
		}
		return circuitSavedMsg{circuit: c, err: e}
	}
}

func (s *PowerCircuitFormScreen) buildPayload() (omsapi.PowerCircuitWrite, error) {
	var w omsapi.PowerCircuitWrite
	if s.breakerID == nil {
		return w, errors.New("breaker is required")
	}
	length, err := parseOptionalPositiveInt(s.inputs[pcConductorLength].Value(), "conductor length")
	if err != nil {
		return w, err
	}
	w = omsapi.PowerCircuitWrite{
		Breaker:           *s.breakerID,
		Label:             strings.TrimSpace(s.inputs[pcLabel].Value()),
		ConductorSize:     strings.TrimSpace(s.inputs[pcConductorSize].Value()),
		ConductorLengthFt: length,
		Notes:             strings.TrimSpace(s.inputs[pcNotes].Value()),
		NeedsReview:       s.needsReview,
		// MaxLoadAmps deliberately left nil → omitted → backend 80% derate.
	}
	return w, nil
}

func (s *PowerCircuitFormScreen) exitCmd() tea.Cmd {
	breakerID := 0
	if s.breakerID != nil {
		breakerID = *s.breakerID
	}
	if breakerID != 0 {
		return SwitchTo(WSFacilities, NewBreakerCircuitsScreen(s.deps, breakerID, s.panelID, s.breakerLabel(breakerID)))
	}
	return SwitchTo(WSFacilities, NewElectricalPanelsScreen(s.deps))
}

func (s *PowerCircuitFormScreen) breakerLabel(id int) string {
	for _, b := range s.breakers {
		if b.ID == id {
			return "pos " + b.Position
		}
	}
	if s.circuit != nil && s.circuit.BreakerLabel != "" {
		return s.circuit.BreakerLabel
	}
	return ""
}

func (s *PowerCircuitFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	if s.phase == elecPhasePick {
		return elecPickView("breaker", &s.pickSearch, s.pickTyping, s.pickOptions, s.pickCursor)
	}
	return s.viewForm()
}

func (s *PowerCircuitFormScreen) viewForm() string {
	var b strings.Builder
	b.WriteString(StyleMuted.Render(elecFieldHelp(circuitFieldKind, s.currentFieldID)) + "\n\n")
	visible := elecVisibleRows(s.terminalHeight)
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
	// max_load_amps is intentionally not prompted — surface the backend default
	// so the operator knows it isn't a missing field.
	b.WriteString(StyleMuted.Render("max load auto-set to 80% of breaker amperage on save") + "\n")
	if s.saving {
		b.WriteString(StyleMuted.Render("Saving…"))
	} else if s.errMsg != "" {
		b.WriteString(StyleStatusError.Render("✗ " + s.errMsg))
	}
	return b.String()
}

func (s *PowerCircuitFormScreen) renderField(i int) string {
	id := s.fields[i]
	caret := "  "
	if i == s.cursor {
		caret = "▸ "
	}
	label := circuitFieldLabel[id]
	var value string
	switch circuitFieldKind(id) {
	case akText, akNumber:
		value = s.inputs[id].View()
	case akToggle:
		value = elecToggleLabel(s.needsReview)
	case akPicker:
		value = s.breakerPickerLabel()
	}
	return caret + StyleTitle.Render(label+": ") + value
}

func (s *PowerCircuitFormScreen) breakerPickerLabel() string {
	if s.breakerID == nil {
		return StyleMuted.Render("(required — space to pick)")
	}
	for _, b := range s.breakers {
		if b.ID == *s.breakerID {
			lbl := "pos " + b.Position
			if b.Label != "" {
				lbl += " — " + b.Label
			}
			return lbl
		}
	}
	if s.circuit != nil && s.circuit.BreakerLabel != "" {
		return s.circuit.BreakerLabel
	}
	return fmt.Sprintf("#%d", *s.breakerID)
}
