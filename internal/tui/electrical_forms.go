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
	// "(upstream circuit)" used to ride in the label. A parenthetical in the
	// LABEL widens the shared column and shoves every input area right — on
	// five forms sharing one column, it shoves them on all five. It is a Hint.
	ppFedBy:       "Fed by",
	ppNotes:       "Notes",
	ppNeedsReview: "Needs review",
}

// panelFieldHint carries what the placeholders and the label parentheticals
// used to say. A placeholder long enough to fill the input area leaves no
// underscores, so an empty green-screen row stops reading as empty; and a
// columnar form marks what is REQUIRED rather than tagging everything else
// "(optional)".
// A hint is CLIPPED, not wrapped, when the row runs past the pane
// (layout.go's clampToBox), so value width + hint has to fit what is left of
// the body after the shared label column — see TestJDESweepC_RowsFitTheBody.
var panelFieldHint = map[int]string{
	ppName:        "required · unique",
	ppLocation:    "required",
	ppVoltage:     "required · volts",
	ppMainAmp:     "amps",
	ppInstallDate: "YYYY-MM-DD",
	ppFedBy:       "upstream circuit",
}

// panelFieldWidth sizes the input areas that are not the default.
func panelFieldWidth(id int) int {
	switch id {
	case ppVoltage, ppMainAmp:
		return 8
	case ppInstallDate:
		return 12
	case ppNotes:
		return 40
	}
	return 0
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

	jdeScreen

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

// panelPlaceholder keeps only the placeholders that show a DEFAULT; the rest
// moved to panelFieldHint, where they ride after the input area instead of
// filling it (see jde_form.go).
func panelPlaceholder(id int) string {
	if id == ppVoltage {
		return "240"
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
		s.setSize(m)
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
	// The system keys, first and everywhere: they mean the same thing on every
	// row, which is the whole point of the reduced scheme (sc-h412).
	switch m.String() {
	case "esc":
		return s, s.exitCmd(s.panelID)
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
		// EDIT opens whatever the highlighted row IS. Only the two FK rows open
		// anything, which is why the bar drops the key on the others.
		if id, ok := s.currentFieldID(); ok && panelFieldKind(id) == akPicker {
			s.openPicker(id)
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch panelFieldKind(id) {
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

func (s *PowerPanelFormScreen) moveCursor(delta int) {
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
func (s *PowerPanelFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
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
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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
			s.pickBar(header, body), jdePickBar("Select", true)); ok {
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

// movePick walks the option cursor, clamping at both ends — a picker list is a
// set of choices, not a ring, so running off the bottom must not reappear at the
// "(none)" row that clears the field.
func (s *PowerPanelFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *PowerPanelFormScreen) closePicker() {
	s.phase = elecPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
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
	s.closePicker()
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
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *PowerPanelFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: three bounded sets, two FK
// rows Ctrl-E opens, and the rest typed into.
func (s *PowerPanelFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   panelFieldLabel[id],
			Width:   panelFieldWidth(id),
			Hint:    panelFieldHint[id],
			Focused: i == s.cursor,
		}
		switch panelFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.needsReview)
		case akSelect:
			f.Kind, f.Value = jdeChoice, s.selectValue(id)
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

func (s *PowerPanelFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Power panel"))
	for i, id := range s.fields {
		l.AddRow(i, renderJDEField(fields[i], elecLabelWidth, s.bodyWidth()))
		// The set around the FOCUSED choice row, so eleven breaker families are
		// never cycled blind (jdeOptionStrip returns nothing for a yes/no).
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
func (s *PowerPanelFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyScrollsForBar(body, 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *PowerPanelFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch panelFieldKind(id) {
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

// selectValue is what goes between a select row's angle brackets. It is the
// BARE label — elecSelectLabel wraps its value in ‹ › of its own, which inside
// a jdeChoice would render "< ‹ Split-phase › >".
func (s *PowerPanelFormScreen) selectValue(id int) string {
	opts, idx := s.selectOptions(id)
	if idx >= 0 && idx < len(opts) {
		return opts[idx].label
	}
	return ""
}

// selectOptions is the option set and current index behind a choice row.
func (s *PowerPanelFormScreen) selectOptions(id int) ([]selectOption, int) {
	switch id {
	case ppPhaseConfig:
		return panelPhaseConfigOptions, s.phaseCfgIdx
	case ppBreakerType:
		return panelBreakerTypeOptions, s.breakerTypIdx
	case ppNumbering:
		return panelNumberingOptions, s.numberingIdx
	}
	return nil, -1
}

// selectStrip is the option-set line drawn under the focused choice row.
func (s *PowerPanelFormScreen) selectStrip(id int) string {
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

// pickerValue is an FK row's text, and whether it is an empty state rather than
// a value. It returns PLAIN text with a flag instead of pre-styled muted text,
// because a focused row has to be able to reverse-video the whole field — an
// inner reset sequence would end the highlight partway through it.
func (s *PowerPanelFormScreen) pickerValue(id int) (string, bool) {
	switch id {
	case ppLocation:
		if s.locationID == nil {
			return "(not set)", true
		}
		for _, l := range s.locations {
			if l.ID == *s.locationID {
				return l.Name, false
			}
		}
		return fmt.Sprintf("#%d", *s.locationID), false
	case ppFedBy:
		if s.fedByID == nil {
			return "(none — utility-fed main)", true
		}
		for _, c := range s.circuits {
			if c.ID == *s.fedByID {
				if c.BreakerLabel != "" {
					return c.BreakerLabel, false
				}
				return fmt.Sprintf("circuit #%d", c.ID), false
			}
		}
		return fmt.Sprintf("circuit #%d", *s.fedByID), false
	}
	return "", false
}

// pickView builds the open picker's pinned header and its option list.
func (s *PowerPanelFormScreen) pickView() (jdeHeader, *jdeLines) {
	title, note, empty := "Location", "", "(no matching locations)"
	if s.pickField == ppFedBy {
		title, empty = "Fed by", "(no matching circuits)"
		note = "Row 1 is none — a utility-fed main is fed by no circuit here."
	}
	return jdePickList{
		Title:  title,
		For:    strings.TrimSpace(s.inputs[ppName].Value()),
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
func (s *PowerPanelFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", s.bodyScrollsForBar(body, len(header), jdePickBar("Select", true)))
}

func (s *PowerPanelFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
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
	pbPanel: "Panel",
	// "(slot)" moved out of the label for the same reason ppFedBy's did: a
	// parenthetical widens the column all five electrical forms share.
	pbPosition:         "Position",
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

// breakerFieldHint carries what the placeholders and the label parentheticals
// used to say (see panelFieldHint). The phase row's hint is computed rather
// than looked up — it reports whether auto-calc is live — so it is not here.
var breakerFieldHint = map[int]string{
	pbPanel:    "required",
	pbPosition: "required · slot, e.g. 12 or 14/16",
	pbAmperage: "required · amps",
}

// breakerFieldWidth sizes the input areas that are not the default.
func breakerFieldWidth(id int) int {
	switch id {
	case pbPosition, pbAmperage:
		return 10
	case pbLabel, pbNotes, pbReviewNote, pbCriticalNote:
		return 40
	}
	return 0
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

	jdeScreen

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

// breakerPlaceholder is empty for every field now — see breakerFieldHint.
func breakerPlaceholder(id int) string { return "" }

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
		s.setSize(m)
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
		// EDIT opens whatever the highlighted row IS — here, only the panel row.
		if id, ok := s.currentFieldID(); ok && breakerFieldKind(id) == akPicker {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch breakerFieldKind(id) {
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
		// Typing in the position field re-runs auto-calc (unless the phase was
		// manually pinned) — same as the web form.
		if id == pbPosition {
			s.maybeAutoPhase()
		}
		return s, cmd
	}
}

func (s *PowerBreakerFormScreen) moveCursor(delta int) {
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
func (s *PowerBreakerFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
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
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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
			s.pickBar(header, body), jdePickBar("Select", true)); ok {
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

func (s *PowerBreakerFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *PowerBreakerFormScreen) closePicker() {
	s.phase = elecPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *PowerBreakerFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		id := s.pickOptions[s.pickCursor].id
		s.panelID = &id
		s.maybeAutoPhase() // panel's phase config may change the auto phase
	}
	s.closePicker()
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
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *PowerBreakerFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows. The visible set is
// conditional (rebuildFields hides the review note and the critical pair), so
// this walks s.fields rather than the whole id space.
func (s *PowerBreakerFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   breakerFieldLabel[id],
			Width:   breakerFieldWidth(id),
			Hint:    s.fieldHint(id),
			Focused: i == s.cursor,
		}
		switch breakerFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.toggleState(id))
		case akSelect:
			f.Kind, f.Value = jdeChoice, s.selectValue(id)
		case akPicker:
			value, dim := s.panelValue()
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

// fieldHint is the note after a row's input area. Phase's is COMPUTED rather
// than looked up: it is where the web form's auto-calc state now shows, since
// an annotation appended to the value would sit inside the "< … >" brackets
// (and inside a focused row's reverse-video run).
func (s *PowerBreakerFormScreen) fieldHint(id int) string {
	if id != pbPhase {
		return breakerFieldHint[id]
	}
	if s.phaseManuallySet {
		return ""
	}
	if _, ok := breakerPrimarySlot(s.inputs[pbPosition].Value()); ok {
		return "auto from panel · slot · poles"
	}
	if strings.TrimSpace(s.inputs[pbPosition].Value()) != "" {
		return "auto skipped — tandem slot, set it here"
	}
	return ""
}

// toggleState is a bool row's current value.
func (s *PowerBreakerFormScreen) toggleState(id int) bool {
	if id == pbIsCritical {
		return s.isCritical
	}
	return s.needsReview
}

func (s *PowerBreakerFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Breaker"))
	for i, id := range s.fields {
		l.AddRow(i, renderJDEField(fields[i], elecLabelWidth, s.bodyWidth()))
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
func (s *PowerBreakerFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyScrollsForBar(body, 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *PowerBreakerFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch breakerFieldKind(id) {
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
func (s *PowerBreakerFormScreen) selectValue(id int) string {
	opts, idx := s.selectOptions(id)
	if idx >= 0 && idx < len(opts) {
		return opts[idx].label
	}
	return ""
}

func (s *PowerBreakerFormScreen) selectOptions(id int) ([]selectOption, int) {
	switch id {
	case pbPoleCount:
		return breakerPoleCountOptions, s.poleCountIdx
	case pbPhase:
		return breakerPhaseOptions, s.phaseIdx
	case pbStatus:
		return breakerStatusOptions, s.statusIdx
	case pbReviewStatus:
		return breakerReviewStatusOptions, s.reviewStIdx
	case pbCriticalCategory:
		return breakerCriticalCategoryOptions, s.critCatIdx
	}
	return nil, -1
}

func (s *PowerBreakerFormScreen) selectStrip(id int) string {
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

// panelValue is the panel row's text, and whether it is an empty state.
func (s *PowerBreakerFormScreen) panelValue() (string, bool) {
	if s.panelID == nil {
		return "(not set)", true
	}
	for _, p := range s.panels {
		if p.ID == *s.panelID {
			return p.Name, false
		}
	}
	if s.breaker != nil && s.breaker.PanelName != "" {
		return s.breaker.PanelName, false
	}
	return fmt.Sprintf("#%d", *s.panelID), false
}

func (s *PowerBreakerFormScreen) pickView() (jdeHeader, *jdeLines) {
	return jdePickList{
		Title:  "Panel",
		For:    strings.TrimSpace(s.inputs[pbLabel].Value()),
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Cursor: s.pickCursor,
		Empty:  "(no matching panels)",
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *PowerBreakerFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", s.bodyScrollsForBar(body, len(header), jdePickBar("Select", true)))
}

func (s *PowerBreakerFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
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
	pcBreaker:       "Breaker",
	pcLabel:         "Label",
	pcConductorSize: "Conductor size",
	// "(ft)" is a UNIT, and a unit belongs after the input area, not in the
	// label column all five electrical forms share.
	pcConductorLength: "Conductor length",
	pcNotes:           "Notes",
	pcNeedsReview:     "Needs review",
}

// circuitFieldHint carries what the placeholders and the label parentheticals
// used to say (see panelFieldHint).
var circuitFieldHint = map[int]string{
	pcBreaker:         "required",
	pcConductorSize:   "e.g. 12 AWG",
	pcConductorLength: "feet",
}

// circuitFieldWidth sizes the input areas that are not the default.
func circuitFieldWidth(id int) int {
	switch id {
	case pcConductorSize, pcConductorLength:
		return 10
	case pcLabel, pcNotes:
		return 40
	}
	return 0
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

	jdeScreen

	inputs []textinput.Model

	breakerID   *int
	needsReview bool

	fields []int
	cursor int

	phase       elecFormPhase
	pickCursor  int
	pickSearch  textinput.Model
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

// circuitPlaceholder is empty for every field now — see circuitFieldHint.
func circuitPlaceholder(id int) string { return "" }

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
		s.setSize(m)
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
		// EDIT opens whatever the highlighted row IS — here, only the breaker row.
		if id, ok := s.currentFieldID(); ok && circuitFieldKind(id) == akPicker {
			s.openPicker()
			return s, textinput.Blink
		}
		return s, nil
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch circuitFieldKind(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.needsReview = !s.needsReview
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

func (s *PowerCircuitFormScreen) moveCursor(delta int) {
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
func (s *PowerCircuitFormScreen) pageCursor(dir int) {
	body := s.formLines()
	next, ok := s.pageRow(body, s.cursor, len(s.fields), dir, 0,
		s.formBar(body), s.formBarItems(true))
	if !ok {
		return
	}
	s.cursor = next
	s.syncFocus()
}

func (s *PowerCircuitFormScreen) openPicker() {
	s.phase = elecPhasePick
	s.pickSearch.SetValue("")
	// The filter is always live in a columnar picker, so it holds the caret for
	// as long as the picker is open.
	s.pickSearch.Focus()
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
			s.pickBar(header, body), jdePickBar("Select", true)); ok {
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

func (s *PowerCircuitFormScreen) movePick(delta int) {
	header, body := s.pickView()
	next, ok := s.pickRow(s.pickCursor, len(s.pickOptions), delta, len(header), s.pickBar(header, body))
	if !ok {
		return
	}
	s.pickCursor = next
}

func (s *PowerCircuitFormScreen) closePicker() {
	s.phase = elecPhaseForm
	s.pickSearch.SetValue("")
	s.pickSearch.Blur()
	s.syncFocus()
}

func (s *PowerCircuitFormScreen) commitPick() {
	if s.pickCursor >= 0 && s.pickCursor < len(s.pickOptions) {
		id := s.pickOptions[s.pickCursor].id
		s.breakerID = &id
	}
	s.closePicker()
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
		return s.viewPick()
	}
	return s.viewForm()
}

func (s *PowerCircuitFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: one FK row Ctrl-E opens, one
// bounded set, and three fields typed into.
func (s *PowerCircuitFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   circuitFieldLabel[id],
			Width:   circuitFieldWidth(id),
			Hint:    circuitFieldHint[id],
			Focused: i == s.cursor,
		}
		switch circuitFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.needsReview)
		case akPicker:
			value, dim := s.breakerValue()
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

// maxLoadField is the backend-derived max_load_amps: a dimmed, NON-navigable
// row in the sheet rather than a footnote under it. The form deliberately does
// not prompt for it (the backend derates to 80% of the breaker), and a value
// the record carries but the operator does not set still belongs in the record
// — the device-type form's fixed code row is the same fold (sc-0zvi).
func (s *PowerCircuitFormScreen) maxLoadField() jdeField {
	return jdeField{
		Label: "Max load",
		Kind:  jdeValue,
		Value: "80% of breaker amperage",
		Hint:  "set by the backend on save",
		Dim:   true,
	}
}

func (s *PowerCircuitFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Circuit"))
	l.AddFields(fields, elecLabelWidth, s.bodyWidth(), 0)
	// Drawn with Add, not AddRow: there is nothing to navigate to.
	l.Add(renderJDEField(s.maxLoadField(), elecLabelWidth, s.bodyWidth()))
	return l
}

// formBar names the keys that work on the form, with PgUp/PgDn on it exactly
// when the body moves under the bar that is about to be drawn.
//
// The paging claim is measured against formBarItems(true) — the bar WITH the
// pair on it — because naming them costs cells, cells fold the bar onto another
// row, and a folded bar leaves the body one row fewer. The tallest bar is the
// fixed point, so the answer cannot oscillate between frames.
func (s *PowerCircuitFormScreen) formBar(body *jdeLines) []actionBarItem {
	return s.formBarItems(s.bodyScrollsForBar(body, 0, s.formBarItems(true)))
}

// formBarItems is formBar for a given paging state, so the bar that is
// MEASURED is the bar that is drawn.
//
// It names the keys that apply where the cursor is standing — and only those,
// so the bar never teaches a key that does nothing here.
func (s *PowerCircuitFormScreen) formBarItems(paging bool) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok {
		switch circuitFieldKind(id) {
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

// breakerValue is the breaker row's text, and whether it is an empty state.
func (s *PowerCircuitFormScreen) breakerValue() (string, bool) {
	if s.breakerID == nil {
		return "(not set)", true
	}
	for _, b := range s.breakers {
		if b.ID == *s.breakerID {
			lbl := "pos " + b.Position
			if b.Label != "" {
				lbl += " — " + b.Label
			}
			return lbl, false
		}
	}
	if s.circuit != nil && s.circuit.BreakerLabel != "" {
		return s.circuit.BreakerLabel, false
	}
	return fmt.Sprintf("#%d", *s.breakerID), false
}

func (s *PowerCircuitFormScreen) pickView() (jdeHeader, *jdeLines) {
	return jdePickList{
		Title:  "Breaker",
		For:    strings.TrimSpace(s.inputs[pcLabel].Value()),
		Filter: s.pickSearch,
		Count:  len(s.pickOptions),
		Label:  func(i int) string { return s.pickOptions[i].label },
		Cursor: s.pickCursor,
		Empty:  "(no matching breakers)",
	}.render(s.bodyWidth())
}

// pickBar is the picker's bar, with PgUp/PgDn on it exactly when the option
// list moves under the bar about to be drawn — measured against the bar WITH
// the pair on it, because the tallest bar is the fixed point.
func (s *PowerCircuitFormScreen) pickBar(header jdeHeader, body *jdeLines) []actionBarItem {
	return jdePickBar("Select", s.bodyScrollsForBar(body, len(header), jdePickBar("Select", true)))
}

func (s *PowerCircuitFormScreen) viewPick() string {
	header, body := s.pickView()
	return s.frameWithHeader(header, body, s.pickCursor,
		s.statusRow(false, "", ""), s.pickBar(header, body))
}
