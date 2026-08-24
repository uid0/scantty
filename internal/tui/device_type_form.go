// ForgeKey device-type CRUD — list + create/edit form.
//
// TUI counterpart to the web ForgeKeyDeviceTypesPage. A device type is the
// metadata lookup row that names a kind of device; its `code` drives
// device-enrollment matching (a device's enroll-time sensor_kind is matched to
// a DeviceType code) and firmware targeting. ScanTTY already READ device types
// (the indicator-detection lookup on the device-detail screen); this adds the
// missing management surface so a staff operator can create, edit and delete
// them without switching to the browser ([[scantty-parity-program]] Tier-3).
//
// SCOPE: device-type METADATA only. The DeviceType serializer is a flat
// `fields = "__all__"` over {name, code, description, is_active} — it exposes NO
// certificate, key-material, enrollment or credential fields (those live on
// separate models/endpoints: DeviceCertificate, DeviceEnrollment, badge
// enrollment). So there is nothing security-sensitive to skip here.
//
// The form mirrors the web form's field set exactly:
//   - name         required, unique
//   - code         required, unique choice (19 TYPE_CHOICES) — CREATE ONLY. The
//     web disables the code input on edit and omits it from the
//     PATCH; we mirror that (a fixed header line in edit mode) so a
//     code that other rows key off (enrollment/firmware) can't be
//     silently rewritten.
//   - description  optional free text
//   - is_active    toggle, defaults on
//
// Writes are staff-gated server-side (IsAdminUser); a non-staff operator sees
// the list (reads are open) but a save/delete surfaces a clean 403. Delete is
// backend-only (the web has no delete button) but the ModelViewSet destroy route
// exists, so we surface it (parity+) — with the caveat that ESP32Device.
// device_type is on_delete=PROTECT, so deleting a type still used by a device
// fails; that error is caught and shown rather than crashing.
//
// DeviceTypeListScreen is reached with the global `F` hotkey (app.go), which
// sets the ForgeKey workspace active — device types sit alongside firmware (`f`)
// and e-paper (`e`) as a ForgeKey management surface. It is a plain (non-raw)
// screen so workspace switching keeps working; it claims only `n` (new) and `G`
// (which collide with global hotkeys) via HandlesKey, and flips to raw input
// only while the delete confirmation is up so y/n land here.
//
// The FORM renders through the columnar "JD Edwards" layer (jde_form.go,
// sc-h412/sc-dnhx): one right-aligned label column, the code as a "< value >"
// choice row with its neighbours in the set spelled out underneath, and a
// persistent action bar. Enter saves, Esc cancels, Up/Down move, ←/→ change.
// In edit mode the fixed code keeps its place in the sheet as a dimmed row that
// is drawn but not navigable — the web's disabled input, in columnar form.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

// deviceTypeCodeOptions mirrors the backend DeviceType.TYPE_CHOICES exactly
// (forgekey/models.py). The stored value is the wire value — a wrong code 400s
// on save — so this list is the parity contract, not cosmetic. Order follows
// the model's declaration. Note "power_relay" carries the human label "AC Relay"
// (a historical rename; the value is power_relay, not ac_relay).
var deviceTypeCodeOptions = []selectOption{
	{"indicator", "Indicator/Status Light"},
	{"badge_reader", "Badge Reader"},
	{"epaper_screen", "E-Paper Screen"},
	{"oled_screen", "OLED Screen"},
	{"temperature_sensor", "Temperature Sensor"},
	{"generic_input", "Generic Input"},
	{"generic_output", "Generic Output"},
	{"power_relay", "AC Relay"},
	{"power_measurement", "Power Measurement"},
	{"people_counter", "People Counter"},
	{"env_sensor", "Environmental Sensor"},
	{"door_counter", "Door Counter"},
	{"locker_latch", "Locker latch controller"},
	{"door_latch", "Door latch controller"},
	{"otp_keypad", "OTP keypad"},
	{"led_strip", "WS2818 LED strip controller"},
	{"reed_switch", "Door reed switch"},
	{"ir_break", "Inventory IR-break sensor"},
	{"mortise_key", "Mortise key (admin override) sensor"},
}

// deviceTypeCodeLabel returns the human label for a code, falling back to the
// raw code when it isn't one of the known choices (forward-compatible if the
// backend adds a code we don't know yet).
func deviceTypeCodeLabel(code string) string {
	for _, o := range deviceTypeCodeOptions {
		if o.value == code {
			return o.label
		}
	}
	if code == "" {
		return "(unset)"
	}
	return code
}

// ===========================================================================
// DeviceTypeFormScreen
// ===========================================================================

const (
	dtName = iota
	dtCode
	dtDescription
	dtActive
	dtFieldMax
)

var deviceTypeFieldLabel = map[int]string{
	dtName:        "Name",
	dtCode:        "Code",
	dtDescription: "Description",
	dtActive:      "Active",
}

// deviceTypeFieldHint carries what the placeholders used to say. A placeholder
// long enough to fill the input area leaves no underscores, so an empty
// green-screen row stops reading as empty; and a columnar form marks what is
// REQUIRED rather than tagging everything else "(optional)".
var deviceTypeFieldHint = map[int]string{
	dtName: "required · unique",
}

// deviceTypeFieldWidth sizes the input areas that are not the default.
func deviceTypeFieldWidth(id int) int {
	switch id {
	case dtDescription:
		return 40
	}
	return 0
}

func deviceTypeFieldKind(id int) assetFieldKind {
	switch id {
	case dtName, dtDescription:
		return akText
	case dtCode:
		return akSelect
	case dtActive:
		return akToggle
	}
	return akText
}

func deviceTypeIsTextKind(id int) bool { return deviceTypeFieldKind(id) == akText }

type DeviceTypeFormScreen struct {
	deps   Deps
	edit   bool
	typeID int

	loading bool
	loadErr string
	saving  bool
	errMsg  string

	dt *forgekeyapi.DeviceType

	jdeScreen

	inputs   []textinput.Model
	codeIdx  int
	isActive bool

	fields []int
	cursor int
}

type deviceTypeLoadedMsg struct {
	dt  *forgekeyapi.DeviceType
	err error
}

type deviceTypeSavedMsg struct {
	dt  *forgekeyapi.DeviceType
	err error
}

// NewDeviceTypeFormScreen opens create mode when typeID == 0, else edit mode
// (hydrating from the fetched device type).
func NewDeviceTypeFormScreen(deps Deps, typeID int) *DeviceTypeFormScreen {
	edit := typeID != 0
	s := &DeviceTypeFormScreen{
		deps:     deps,
		edit:     edit,
		typeID:   typeID,
		loading:  edit, // create mode has nothing to fetch
		isActive: true, // model default
	}
	s.inputs = make([]textinput.Model, dtFieldMax)
	for id := 0; id < dtFieldMax; id++ {
		if !deviceTypeIsTextKind(id) {
			continue
		}
		ti := textinput.New()
		ti.Prompt = ""
		ti.CharLimit = deviceTypeCharLimit(id)
		ti.Placeholder = deviceTypePlaceholder(id)
		s.inputs[id] = ti
	}
	s.rebuildFields()
	s.syncFocus()
	return s
}

func deviceTypeCharLimit(id int) int {
	switch id {
	case dtName:
		return 50 // model max_length
	case dtDescription:
		return 1000
	}
	return 100
}

// deviceTypePlaceholder is empty for every field now — see deviceTypeFieldHint.
func deviceTypePlaceholder(id int) string { return "" }

// rebuildFields sets the visible/navigable field list. Code is create-only: in
// edit mode it is shown as a fixed header line (see viewForm) rather than an
// editable select, so it drops out of the field list.
func (s *DeviceTypeFormScreen) rebuildFields() {
	if s.edit {
		s.fields = []int{dtName, dtDescription, dtActive}
	} else {
		s.fields = []int{dtName, dtCode, dtDescription, dtActive}
	}
	if s.cursor >= len(s.fields) {
		s.cursor = len(s.fields) - 1
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
}

func (s *DeviceTypeFormScreen) Title() string {
	if s.edit {
		if s.dt != nil && s.dt.Name != "" {
			return "Edit device type: " + s.dt.Name
		}
		return "Edit device type"
	}
	return "New device type"
}

func (s *DeviceTypeFormScreen) WantsRawInput() bool { return true }

func (s *DeviceTypeFormScreen) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	if s.edit {
		cmds = append(cmds, s.loadRecord())
	}
	return tea.Batch(cmds...)
}

func (s *DeviceTypeFormScreen) ctx() context.Context {
	if s.deps.Ctx != nil {
		return s.deps.Ctx
	}
	return context.Background()
}

func (s *DeviceTypeFormScreen) loadRecord() tea.Cmd {
	deps := s.deps
	ctx := s.ctx()
	id := s.typeID
	return func() tea.Msg {
		dt, err := deps.ForgeKey.GetDeviceType(ctx, id)
		return deviceTypeLoadedMsg{dt: dt, err: err}
	}
}

func (s *DeviceTypeFormScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.setSize(m)
		return s, nil
	case deviceTypeLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		} else {
			s.dt = m.dt
			s.hydrate()
		}
		s.syncFocus()
		return s, nil
	case deviceTypeSavedMsg:
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
		if m.dt != nil {
			name = m.dt.Name
		}
		return s, tea.Batch(
			Status(fmt.Sprintf("device type %s: %s", verb, name), StatusOK),
			SwitchTo(WSForgeKey, NewDeviceTypeListScreen(s.deps)),
		)
	case tea.KeyMsg:
		if s.loading {
			if m.String() == "esc" {
				return s, s.cancelCmd()
			}
			return s, nil
		}
		return s.updateFormPhase(m)
	}

	if id, ok := s.currentFieldID(); ok && deviceTypeIsTextKind(id) {
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(msg)
		return s, cmd
	}
	return s, nil
}

func (s *DeviceTypeFormScreen) hydrate() {
	d := s.dt
	if d == nil {
		return
	}
	s.inputs[dtName].SetValue(d.Name)
	s.inputs[dtDescription].SetValue(d.Description)
	s.codeIdx = selectIndexOf(deviceTypeCodeOptions, d.Code)
	s.isActive = d.IsActive
}

func (s *DeviceTypeFormScreen) currentFieldID() (int, bool) {
	if s.cursor < 0 || s.cursor >= len(s.fields) {
		return 0, false
	}
	return s.fields[s.cursor], true
}

func (s *DeviceTypeFormScreen) syncFocus() {
	for id := 0; id < len(s.inputs); id++ {
		if deviceTypeIsTextKind(id) {
			s.inputs[id].Blur()
		}
	}
	if id, ok := s.currentFieldID(); ok && deviceTypeIsTextKind(id) {
		s.inputs[id].Focus()
	}
}

func (s *DeviceTypeFormScreen) updateFormPhase(m tea.KeyMsg) (Screen, tea.Cmd) {
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
	}

	id, ok := s.currentFieldID()
	if !ok {
		return s, nil
	}
	switch deviceTypeFieldKind(id) {
	case akToggle:
		// A toggle is a two-value choice row, so it flips on the same ←/→ every
		// other bounded set takes (space stays as the pilot's synonym).
		switch m.String() {
		case " ", "left", "right":
			s.isActive = !s.isActive
		}
		return s, nil
	case akSelect:
		switch m.String() {
		case " ", "right":
			s.cycleCode(+1)
		case "left":
			s.cycleCode(-1)
		}
		return s, nil
	default:
		var cmd tea.Cmd
		s.inputs[id], cmd = s.inputs[id].Update(m)
		return s, cmd
	}
}

func (s *DeviceTypeFormScreen) moveCursor(delta int) {
	n := len(s.fields)
	if n == 0 {
		return
	}
	s.cursor = (s.cursor + delta + n) % n
	s.syncFocus()
}

// pageCursor moves a whole pane's worth of rows, clamping where moveCursor
// wraps — a page is for covering ground, not for losing your place.
func (s *DeviceTypeFormScreen) pageCursor(dir int) {
	if len(s.fields) == 0 {
		return
	}
	s.cursor = jdePageCursor(s.cursor, len(s.fields), s.windowRows(s.formLines(), s.cursor, 0), dir)
	s.syncFocus()
}

func (s *DeviceTypeFormScreen) cycleCode(delta int) {
	n := len(deviceTypeCodeOptions)
	if n == 0 {
		return
	}
	s.codeIdx = (s.codeIdx + delta + n) % n
}

func (s *DeviceTypeFormScreen) submit() (Screen, tea.Cmd) {
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
	id := s.typeID
	return s, func() tea.Msg {
		var dt *forgekeyapi.DeviceType
		var e error
		if edit {
			dt, e = deps.ForgeKey.UpdateDeviceType(ctx, id, body)
		} else {
			dt, e = deps.ForgeKey.CreateDeviceType(ctx, body)
		}
		return deviceTypeSavedMsg{dt: dt, err: e}
	}
}

func (s *DeviceTypeFormScreen) buildPayload() (forgekeyapi.DeviceTypeWrite, error) {
	var w forgekeyapi.DeviceTypeWrite
	name := strings.TrimSpace(s.inputs[dtName].Value())
	if name == "" {
		return w, errors.New("name is required")
	}
	w = forgekeyapi.DeviceTypeWrite{
		Name:        name,
		Description: strings.TrimSpace(s.inputs[dtDescription].Value()),
		IsActive:    s.isActive,
	}
	// Code is create-only. On edit it's left blank so the client (omitempty)
	// drops it from the PATCH, keeping the code immutable (web parity).
	if !s.edit {
		if s.codeIdx < 0 || s.codeIdx >= len(deviceTypeCodeOptions) {
			return w, errors.New("code is required")
		}
		w.Code = deviceTypeCodeOptions[s.codeIdx].value
	}
	return w, nil
}

func (s *DeviceTypeFormScreen) cancelCmd() tea.Cmd {
	return SwitchTo(WSForgeKey, NewDeviceTypeListScreen(s.deps))
}

func (s *DeviceTypeFormScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("esc to go back")
	}
	return s.viewForm()
}

func (s *DeviceTypeFormScreen) viewForm() string {
	body := s.formLines()
	return s.frame(body, s.cursor, s.statusRow(s.saving, "Saving…", s.errMsg), s.formBar(body))
}

// formFields describes the form as columnar rows: the code and the active flag
// are bounded sets, the rest are typed into.
func (s *DeviceTypeFormScreen) formFields() []jdeField {
	out := make([]jdeField, len(s.fields))
	for i, id := range s.fields {
		f := jdeField{
			Label:   deviceTypeFieldLabel[id],
			Width:   deviceTypeFieldWidth(id),
			Hint:    deviceTypeFieldHint[id],
			Focused: i == s.cursor,
		}
		switch deviceTypeFieldKind(id) {
		case akToggle:
			f.Kind, f.Value = jdeChoice, jdeYesNo(s.isActive)
		case akSelect:
			f.Kind, f.Value = jdeChoice, deviceTypeCodeLabel(s.codeValue())
			// The wire value, not the label: this code is what device enrollment
			// and firmware targeting match on, and "AC Relay" is stored as
			// power_relay. A form whose value is a contract should show it.
			f.Hint = s.codeValue()
		default:
			f.Kind, f.Input = jdeText, &s.inputs[id]
		}
		out[i] = f
	}
	return out
}

// codeValue is the wire code the cursor is sitting on, or the saved one in edit
// mode (where the select is not on the sheet).
func (s *DeviceTypeFormScreen) codeValue() string {
	if s.edit {
		if s.dt != nil {
			return s.dt.Code
		}
		return ""
	}
	if s.codeIdx >= 0 && s.codeIdx < len(deviceTypeCodeOptions) {
		return deviceTypeCodeOptions[s.codeIdx].value
	}
	return ""
}

// fixedCodeField is the edit-mode code row: drawn in the sheet, in the place the
// select would have, but dimmed and NOT navigable — the columnar form of the
// web's disabled code input. A code other rows key off (enrollment, firmware)
// must not be silently rewritten.
func (s *DeviceTypeFormScreen) fixedCodeField() jdeField {
	return jdeField{
		Label: deviceTypeFieldLabel[dtCode],
		Kind:  jdeValue,
		Value: deviceTypeCodeLabel(s.codeValue()),
		Hint:  "fixed after creation",
		Dim:   true,
	}
}

func (s *DeviceTypeFormScreen) formLines() *jdeLines {
	fields := s.formFields()
	fixed := []jdeField{}
	if s.edit {
		fixed = append(fixed, s.fixedCodeField())
	}
	// ONE column across both bands, or the fixed row would hang off its own.
	labelWidth := jdeLabelWidth(fields, fixed)

	l := &jdeLines{}
	l.Add(StyleJDEHeading.Render("Device type"))
	for i, f := range fields {
		l.AddRow(i, renderJDEField(f, labelWidth, s.bodyWidth()))
		if s.fields[i] == dtName && len(fixed) > 0 {
			// Code sits where it would if it were editable, so the sheet reads
			// the same either way; it is a line, not a row, because there is
			// nothing to navigate to.
			l.Add(renderJDEField(fixed[0], labelWidth, s.bodyWidth()))
		}
		// The set around the FOCUSED code row, so nineteen choices are never
		// cycled blind (jdeOptionStrip returns nothing for a yes/no).
		if i == s.cursor && s.fields[i] == dtCode {
			labels := make([]string, len(deviceTypeCodeOptions))
			for j, o := range deviceTypeCodeOptions {
				labels[j] = o.label
			}
			if strip := jdeOptionStrip(labels, s.codeIdx, jdeStripWidth(s.bodyWidth(), labelWidth)); strip != "" {
				l.AddRow(i, jdeStripIndent(labelWidth)+StyleMuted.Render(strip))
			}
		}
	}
	return l
}

// formBar names the keys that apply where the cursor is standing — and only
// those, so the bar never teaches a key that does nothing here.
func (s *DeviceTypeFormScreen) formBar(body *jdeLines) []actionBarItem {
	items := []actionBarItem{{"Enter", "Save"}, {"Esc", "Cancel"}, {"UP/DN", "Fields"}}
	if id, ok := s.currentFieldID(); ok && !deviceTypeIsTextKind(id) {
		items = append(items, actionBarItem{"←→", "Change"})
	}
	if s.bodyScrolls(body, 0) {
		items = append(items, actionBarItem{"PgUp/PgDn", "Page"})
	}
	return items
}

// ===========================================================================
// DeviceTypeListScreen
// ===========================================================================

type DeviceTypeListScreen struct {
	deps           Deps
	rows           []forgekeyapi.DeviceType
	cursor         int
	windowStart    int
	windowSize     int
	loading        bool
	loadErr        string
	terminalHeight int

	confirmingDelete bool
	deleting         bool
}

type deviceTypeListLoadedMsg struct {
	rows []forgekeyapi.DeviceType
	err  error
}

type deviceTypeDeletedMsg struct {
	err error
}

func NewDeviceTypeListScreen(deps Deps) *DeviceTypeListScreen {
	return &DeviceTypeListScreen{deps: deps, loading: true, windowSize: 20}
}

func (s *DeviceTypeListScreen) Title() string { return "ForgeKey Device Types" }

// HandlesKey claims the two keys that collide with global hotkeys — `n` (global
// notifications) for new, and `G` (global categories) for bottom-of-list — so
// they act on this screen. E/x/enter/r are not global hotkeys, so they reach us
// via the root's fall-through.
func (s *DeviceTypeListScreen) HandlesKey(key string) bool {
	if s.confirmingDelete {
		return false // WantsRawInput already routes every key here
	}
	return key == "n" || key == "G"
}

// WantsRawInput claims every key only while the delete confirmation is up, so
// y/n/esc land here instead of the root's global hotkeys.
func (s *DeviceTypeListScreen) WantsRawInput() bool { return s.confirmingDelete }

func (s *DeviceTypeListScreen) Init() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		rows, err := deps.ForgeKey.ListDeviceTypes(ctx)
		return deviceTypeListLoadedMsg{rows: rows, err: err}
	}
}

func (s *DeviceTypeListScreen) computeWindowSize() int {
	const chrome = 4
	avail := screenBodyHeight(s.terminalHeight) - chrome
	if avail < 3 {
		avail = 3
	}
	return avail
}

func (s *DeviceTypeListScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		s.terminalHeight = m.Height
		s.windowSize = s.computeWindowSize()
		s.scrollIntoView()
		return s, nil
	case deviceTypeListLoadedMsg:
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
	case deviceTypeDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		s.loading = true
		return s, tea.Batch(Status("device type deleted", StatusOK), s.Init())
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
		case "n":
			return s, SwitchTo(WSForgeKey, NewDeviceTypeFormScreen(s.deps, 0))
		case "E", "enter":
			// Device types have no separate detail page (mirrors the web, which
			// links the list straight to the edit form), so edit and open both go
			// to the form.
			if row, ok := s.selected(); ok {
				return s, SwitchTo(WSForgeKey, NewDeviceTypeFormScreen(s.deps, row.IntID()))
			}
		case "x":
			if _, ok := s.selected(); ok {
				s.confirmingDelete = true
			}
		}
	}
	return s, nil
}

func (s *DeviceTypeListScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
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
		id := row.IntID()
		return s, func() tea.Msg {
			return deviceTypeDeletedMsg{err: deps.ForgeKey.DeleteDeviceType(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func (s *DeviceTypeListScreen) selected() (forgekeyapi.DeviceType, bool) {
	if s.cursor < 0 || s.cursor >= len(s.rows) {
		return forgekeyapi.DeviceType{}, false
	}
	return s.rows[s.cursor], true
}

func (s *DeviceTypeListScreen) scrollIntoView() {
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

func (s *DeviceTypeListScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading device types…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · n new · esc back")
	}
	if s.confirmingDelete {
		name := ""
		if row, ok := s.selected(); ok {
			name = row.Name
		}
		if s.deleting {
			return StyleMuted.Render("Deleting…")
		}
		// Honest heads-up: the backend PROTECTs a type still used by a device
		// (delete will fail), and cascades its firmware versions/builds.
		warn := fmt.Sprintf("Delete device type %q? Blocked if any device still uses it; firmware versions/builds cascade.  y delete · n/esc cancel", name)
		return StyleStatusWarn.Render(warn)
	}
	if len(s.rows) == 0 {
		return StyleMuted.Render("No device types.") + "\n\n" + StyleMuted.Render("n new device type · esc back")
	}

	var b strings.Builder
	b.WriteString(StyleMuted.Render(fmt.Sprintf("%d device types", len(s.rows))) + "\n")
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
	b.WriteString(StyleMuted.Render("j/k move · n new · E/enter edit · x delete · r refresh · esc back"))
	return b.String()
}

func (s *DeviceTypeListScreen) renderRow(i int) string {
	d := s.rows[i]
	marker := "  "
	if i == s.cursor {
		marker = "▸ "
	}
	meta := []string{deviceTypeCodeLabel(d.Code)}
	if !d.IsActive {
		meta = append(meta, "inactive")
	}
	if desc := strings.TrimSpace(d.Description); desc != "" {
		meta = append(meta, truncateDeviceTypeDesc(desc))
	}
	line := marker + d.Name + " " + StyleMuted.Render("("+strings.Join(meta, " · ")+")")
	if i == s.cursor {
		line = StyleSidebarItemActive.Render(line)
	}
	return line
}

// truncateDeviceTypeDesc keeps the list row to one line by clipping a long
// description to a short snippet.
func truncateDeviceTypeDesc(s string) string {
	const max = 40
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
