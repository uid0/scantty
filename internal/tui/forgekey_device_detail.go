package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/forgekeyapi"
	"github.com/uid0/scantty/internal/omsapi"
)

// deviceCommandKeys are the keys on this screen that end in an MQTT publish.
// forgekey's device_commands service routes every one of them through the
// backend's "mqtt" circuit breaker, so one open breaker takes the whole set out
// — a press could only buy a broker timeout. Mirrors the web
// ForgeKeyDeviceDetailPage / DeviceControlsCard, which grey out exactly these.
//
// E (edit) and x (delete) are database writes and are deliberately NOT here:
// they keep working through an outage, the same call the web makes for Unbind.
func isDeviceCommandKey(key string) bool {
	switch key {
	case "e", "d", "s", "i", "p", "b", "R", // enable/disable/status/identify/ping/blink/restart
		"1", "2", "!", "@", // per-channel power relay
		"t": // indicator test — the submit publishes, so the form is gated shut
		return true
	}
	return false
}

// fkDetailMode selects the device-detail overlay. fkDetailView is the normal
// read-only body; fkDetailIndicatorTest is the send-a-preview form, which
// captures every key via WantsRawInput so the global hotkey layer can't steal
// the period textinput's characters or the cycle keys.
type fkDetailMode int

const (
	fkDetailView fkDetailMode = iota
	fkDetailIndicatorTest
)

// Indicator-test option sets — mirror the web IndicatorManagementCard
// (TEST_COLORS / TEST_BRIGHTNESS / TEST_PATTERNS). indicatorBlinkPatterns is
// the web isBlinkPattern() set: the patterns that carry a period_ms.
var (
	indicatorColors     = []string{"green", "red", "purple", "blue", "yellow", "white"}
	indicatorBrightness = []string{"low", "high"}
	indicatorPatterns   = []string{"solid", "blink", "slow_blink", "breathe", "off"}
)

func indicatorIsBlinkPattern(pattern string) bool {
	switch pattern {
	case "blink", "slow_blink", "breathe":
		return true
	}
	return false
}

type ForgeKeyDeviceDetailScreen struct {
	deps        Deps
	devID       string
	device      *forgekeyapi.Device
	commands    []forgekeyapi.DeviceCommand
	temp        *forgekeyapi.TemperatureResponse
	isIndicator bool
	loading     bool
	loadErr     string
	actionMsg   string

	mode fkDetailMode

	// Indicator-test form. Defaults mirror the web card: green / high / solid,
	// period 1500ms (only sent for blink patterns).
	itColorIdx      int
	itBrightnessIdx int
	itPatternIdx    int
	itPeriodIn      textinput.Model
	itFocus         int // 0=color, 1=brightness, 2=pattern, 3=period
	itErr           string
	itPending       bool

	// Delete-with-confirm (x): a y/n guard before the destructive DELETE.
	// While confirming, WantsRawInput routes every key here so y/n/esc land
	// locally instead of hitting the root's global hotkeys.
	confirmingDelete bool
	deleting         bool
}

type fkDeviceLoadedMsg struct {
	device      *forgekeyapi.Device
	commands    []forgekeyapi.DeviceCommand
	temp        *forgekeyapi.TemperatureResponse
	isIndicator bool
	err         error
}

type fkCommandResultMsg struct {
	action string
	err    error
}

type fkIndicatorTestMsg struct {
	resp *forgekeyapi.IndicatorTestResponse
	err  error
}

type fkDeviceDeletedMsg struct {
	err error
}

func NewForgeKeyDeviceDetailScreen(deps Deps, id string) *ForgeKeyDeviceDetailScreen {
	return &ForgeKeyDeviceDetailScreen{deps: deps, devID: id, loading: true}
}

func (s *ForgeKeyDeviceDetailScreen) Title() string {
	if s.device != nil {
		return fmt.Sprintf("FK Device: %s", s.device.Name)
	}
	return "ForgeKey Device"
}

func (s *ForgeKeyDeviceDetailScreen) Init() tea.Cmd { return s.load() }

// WantsRawInput routes every key to the screen while the indicator-test form is
// open (so the cycle keys and the period textinput receive characters the global
// dispatcher would otherwise claim) or while the delete confirmation is up (so
// y/n/esc land here rather than triggering global hotkeys).
func (s *ForgeKeyDeviceDetailScreen) WantsRawInput() bool {
	return s.mode != fkDetailView || s.confirmingDelete
}

func (s *ForgeKeyDeviceDetailScreen) load() tea.Cmd {
	deps := s.deps
	id := s.devID
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		dev, err := deps.ForgeKey.GetDevice(ctx, id)
		if err != nil {
			return fkDeviceLoadedMsg{err: err}
		}
		cmds, _ := deps.ForgeKey.RecentCommands(ctx, id, 10)
		var temp *forgekeyapi.TemperatureResponse
		if deviceReportsTemperature(dev) {
			// Only call the temperature action when the device announces the
			// capability. Otherwise the endpoint returns 200 with an empty
			// readings array which would still render an empty section.
			temp, _ = deps.ForgeKey.GetDeviceTemperature(ctx, id, "24h")
		}
		// Resolve whether this is an indicator device so the indicator-test
		// control is offered only where it applies — same rule as the web card
		// (device_type.code == "indicator", falling back to the type name).
		types, _ := deps.ForgeKey.ListDeviceTypes(ctx)
		return fkDeviceLoadedMsg{device: dev, commands: cmds, temp: temp, isIndicator: deviceIsIndicator(dev, types)}
	}
}

// deviceIsIndicator mirrors IndicatorManagementCard's detection: match the
// device's type against the "indicator" device-type code, falling back to the
// stable human name when the code lookup can't resolve.
func deviceIsIndicator(d *forgekeyapi.Device, types []forgekeyapi.DeviceType) bool {
	if d == nil {
		return false
	}
	if d.DeviceTypeName == "Indicator/Status Light" {
		return true
	}
	for _, t := range types {
		if t.Code == "indicator" && fmt.Sprint(t.ID) == fmt.Sprint(d.DeviceType) {
			return true
		}
	}
	return false
}

func deviceReportsTemperature(d *forgekeyapi.Device) bool {
	if d == nil {
		return false
	}
	for _, cap := range d.Capabilities {
		if cap == "temperature_sensor" || cap == "temperature" {
			return true
		}
	}
	return false
}

func (s *ForgeKeyDeviceDetailScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case fkDeviceLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.device = m.device
		s.commands = m.commands
		s.temp = m.temp
		s.isIndicator = m.isIndicator
		return s, nil
	case fkCommandResultMsg:
		if m.err != nil {
			s.actionMsg = fmt.Sprintf("%s failed: %s", m.action, m.err.Error())
			return s, Status(s.actionMsg, StatusError)
		}
		s.actionMsg = fmt.Sprintf("%s dispatched", m.action)
		return s, tea.Batch(
			Status(s.actionMsg, StatusOK),
			s.load(),
		)
	case fkIndicatorTestMsg:
		s.itPending = false
		if m.err != nil {
			s.itErr = m.err.Error()
			return s, Status("indicator test failed: "+m.err.Error(), StatusError)
		}
		s.mode = fkDetailView
		s.actionMsg = "indicator test sent"
		if m.resp != nil && m.resp.CommandID != "" {
			s.actionMsg = "indicator test sent · command " + m.resp.CommandID
		}
		return s, Status("indicator test sent", StatusOK)
	case fkDeviceDeletedMsg:
		s.deleting = false
		s.confirmingDelete = false
		if m.err != nil {
			return s, Status("delete failed: "+m.err.Error(), StatusError)
		}
		return s, tea.Batch(
			Status("device deleted", StatusOK),
			SwitchTo(WSForgeKey, newScreenFor(WSForgeKey, s.deps)),
		)
	case tea.KeyMsg:
		if s.device == nil {
			return s, nil
		}
		if s.confirmingDelete {
			return s.updateConfirmDelete(m)
		}
		if s.mode == fkDetailIndicatorTest {
			return s.handleIndicatorTestKey(m)
		}
		ctx := s.deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		fk := s.deps.ForgeKey
		id := fmt.Sprint(s.device.ID)
		// Gate every command on the capability it needs. Nothing is taken away
		// on an unknown status — IsDegraded is false unless the backend
		// positively reports the breaker open/half-open.
		if isDeviceCommandKey(m.String()) && s.deviceControlDown() {
			return s, Status(deviceControlUnavailable+" — command not sent", StatusWarn)
		}
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		case "E":
			// Edit the device record's web-editable metadata (the default
			// location). Uppercase E — lowercase e is the enable command, and E
			// is the shared "edit this record" convention (inventory/asset/SIG
			// detail screens).
			return s, SwitchTo(WSForgeKey, NewForgeKeyDeviceFormScreen(s.deps, s.device))
		case "x":
			// Delete (deregister) the device — destructive, so guard it behind a
			// y/n confirm. Mirrors the web DeviceLifecycleCard's Delete action.
			s.confirmingDelete = true
			return s, nil
		case "t":
			// Indicator preview — only where it applies (mirrors the web card
			// gating). A non-indicator device just flashes a hint.
			if !s.isIndicator {
				return s, Status("indicator test applies to indicator devices only", StatusWarn)
			}
			return s.openIndicatorTest()
		case "e":
			return s, runFKCmd("enable", func() error { return fk.EnableDevice(ctx, id) })
		case "d":
			return s, runFKCmd("disable", func() error { return fk.DisableDevice(ctx, id, forgekeyapi.DisableRequest{}) })
		case "s":
			return s, runFKCmd("status request", func() error { return fk.RequestStatus(ctx, id) })
		case "i":
			return s, runFKCmd("identify", func() error { return fk.IdentifyDevice(ctx, id, forgekeyapi.IdentifyRequest{DurationS: 30}) })
		case "p":
			return s, runFKCmd("ping", func() error { return fk.PingDevice(ctx, id) })
		case "b":
			return s, runFKCmd("blink", func() error { return fk.BlinkDevice(ctx, id, forgekeyapi.BlinkRequest{DurationS: 10}) })
		case "R":
			return s, runFKCmd("restart", func() error { return fk.RestartDevice(ctx, id) })
		case "1", "2", "!", "@":
			// Per-channel power-relay control (ga-40w): 1/2 enable ch1/ch2,
			// !/@ disable ch1/ch2. Gated to devices that announce power_relay.
			if !deviceHasCapability(s.device, "power_relay") {
				return s, nil
			}
			var req forgekeyapi.RelayChannelRequest
			switch m.String() {
			case "1":
				req = forgekeyapi.RelayChannelRequest{Channel: 1, On: true}
			case "2":
				req = forgekeyapi.RelayChannelRequest{Channel: 2, On: true}
			case "!":
				req = forgekeyapi.RelayChannelRequest{Channel: 1, On: false}
			case "@":
				req = forgekeyapi.RelayChannelRequest{Channel: 2, On: false}
			}
			verb := "enable"
			if !req.On {
				verb = "disable"
			}
			label := fmt.Sprintf("relay ch%d %s", req.Channel, verb)
			return s, runFKCmd(label, func() error { return fk.SetRelayChannel(ctx, id, req) })
		}
	}
	return s, nil
}

// deviceControlDown reports whether the MQTT breaker is open — i.e. whether a
// command sent from this screen could reach the hardware at all.
func (s *ForgeKeyDeviceDetailScreen) deviceControlDown() bool {
	return s.deps.Health.IsDegraded(omsapi.ServiceKeyDeviceControl)
}

func runFKCmd(label string, fn func() error) tea.Cmd {
	return func() tea.Msg {
		err := fn()
		return fkCommandResultMsg{action: label, err: err}
	}
}

// updateConfirmDelete handles the y/n prompt shown before deleting a device.
// The screen is in raw-input mode here (WantsRawInput), so n/esc reach us
// instead of the root's global handlers.
func (s *ForgeKeyDeviceDetailScreen) updateConfirmDelete(m tea.KeyMsg) (Screen, tea.Cmd) {
	if s.deleting {
		return s, nil
	}
	switch m.String() {
	case "y", "Y":
		s.deleting = true
		deps := s.deps
		ctx := deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		id := fmt.Sprint(s.device.ID)
		return s, func() tea.Msg {
			return fkDeviceDeletedMsg{err: deps.ForgeKey.DeleteDevice(ctx, id)}
		}
	case "n", "N", "esc":
		s.confirmingDelete = false
	}
	return s, nil
}

func deviceHasCapability(d *forgekeyapi.Device, capability string) bool {
	if d == nil {
		return false
	}
	for _, c := range d.Capabilities {
		if c == capability {
			return true
		}
	}
	return false
}

// relayChannelNumbers is the fixed 2-channel set the power relay exposes,
// matching the web PowerRelayWidget's RELAY_CHANNELS (op-2cr / ga-40w).
var relayChannelNumbers = []int{1, 2}

// renderRelayChannelState renders a channel's current on/off from the device's
// cached live sub-state (op-2cr): green ●on / red ○off, or a muted — until the
// firmware reports that channel.
func renderRelayChannelState(d *forgekeyapi.Device, channel int) string {
	for _, ch := range d.RelayChannels {
		if ch.Channel == channel {
			if ch.On {
				return StyleStatusOK.Render("● on")
			}
			return StyleStatusError.Render("○ off")
		}
	}
	return StyleMuted.Render("—")
}

// renderIndicatorState mirrors the web IndicatorStateInline (op-2cr): a colour
// swatch + colour name, plus a non-solid pattern; falls back to "State: —" until
// the firmware reports a state.
func renderIndicatorState(st forgekeyapi.IndicatorState) string {
	if st.Color == "" && st.Pattern == "" {
		return StyleMuted.Render("State: —")
	}
	label := st.Color
	if label == "" {
		label = st.Pattern
	}
	line := StyleMuted.Render("State: ") + indicatorSwatch(st.Color) + label
	if st.Color != "" && st.Pattern != "" && st.Pattern != "solid" {
		line += StyleMuted.Render(" · " + st.Pattern)
	}
	return line
}

// indicatorSwatchHex maps the firmware's indicator colour vocabulary to the same
// hex palette the web IndicatorSwatch uses (op-2cr); lipgloss degrades hex to the
// terminal's colour profile.
var indicatorSwatchHex = map[string]string{
	"green":  "#2f9e44",
	"red":    "#e03131",
	"purple": "#9c36b5",
	"blue":   "#1971c2",
	"yellow": "#f08c00",
	"orange": "#e8590c",
	"white":  "#f1f3f5",
	"off":    "#212529",
}

// indicatorSwatch renders a ● dot in the reported colour. Known colour names use
// the shared hex palette; an explicit #hex passes through; anything else renders
// an uncoloured dot so an unfamiliar name still shows a swatch.
func indicatorSwatch(color string) string {
	if color == "" {
		return ""
	}
	hex, ok := indicatorSwatchHex[strings.ToLower(color)]
	if !ok {
		if !strings.HasPrefix(color, "#") {
			return "● "
		}
		hex = color
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render("●") + " "
}

// --- Indicator-test form ---------------------------------------------------

func (s *ForgeKeyDeviceDetailScreen) openIndicatorTest() (Screen, tea.Cmd) {
	s.itColorIdx = 0      // green
	s.itBrightnessIdx = 1 // high
	s.itPatternIdx = 0    // solid
	period := textinput.New()
	period.Prompt = ""
	period.CharLimit = 5
	period.SetValue("1500")
	s.itPeriodIn = period
	s.itFocus = 0
	s.itErr = ""
	s.itPending = false
	s.mode = fkDetailIndicatorTest
	return s, nil
}

// itFieldCount is 4 (color/brightness/pattern/period) for a blink pattern and 3
// otherwise — the period only exists for blink patterns, mirroring the web
// card that shows the period input only when isBlinkPattern().
func (s *ForgeKeyDeviceDetailScreen) itFieldCount() int {
	if indicatorIsBlinkPattern(indicatorPatterns[s.itPatternIdx]) {
		return 4
	}
	return 3
}

func (s *ForgeKeyDeviceDetailScreen) handleIndicatorTestKey(m tea.KeyMsg) (Screen, tea.Cmd) {
	n := s.itFieldCount()
	switch m.Type {
	case tea.KeyEsc:
		s.mode = fkDetailView
		return s, nil
	case tea.KeyTab, tea.KeyDown:
		s.setIndicatorFocus((s.itFocus + 1) % n)
		return s, nil
	case tea.KeyShiftTab, tea.KeyUp:
		s.setIndicatorFocus((s.itFocus + n - 1) % n)
		return s, nil
	case tea.KeyEnter:
		if s.itPending {
			return s, nil
		}
		return s.submitIndicatorTest()
	case tea.KeyLeft:
		s.cycleIndicatorField(-1)
		return s, nil
	case tea.KeyRight:
		s.cycleIndicatorField(1)
		return s, nil
	}
	// The period field is a textinput; forward keystrokes to it.
	if s.itFocus == 3 {
		var cmd tea.Cmd
		s.itPeriodIn, cmd = s.itPeriodIn.Update(m)
		return s, cmd
	}
	// On a select field, space cycles the value forward.
	if m.String() == " " {
		s.cycleIndicatorField(1)
	}
	return s, nil
}

// setIndicatorFocus moves focus and keeps the period textinput's cursor in sync
// (focused only when it is the active field).
func (s *ForgeKeyDeviceDetailScreen) setIndicatorFocus(f int) {
	s.itFocus = f
	if f == 3 {
		s.itPeriodIn.Focus()
	} else {
		s.itPeriodIn.Blur()
	}
}

func (s *ForgeKeyDeviceDetailScreen) cycleIndicatorField(dir int) {
	switch s.itFocus {
	case 0:
		s.itColorIdx = wrapIdx(s.itColorIdx+dir, len(indicatorColors))
	case 1:
		s.itBrightnessIdx = wrapIdx(s.itBrightnessIdx+dir, len(indicatorBrightness))
	case 2:
		s.itPatternIdx = wrapIdx(s.itPatternIdx+dir, len(indicatorPatterns))
	}
}

func wrapIdx(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

// submitIndicatorTest builds the same body the web card sends: brightness +
// pattern always, color unless the pattern is "off", and period_ms only for a
// blink pattern.
func (s *ForgeKeyDeviceDetailScreen) submitIndicatorTest() (Screen, tea.Cmd) {
	// Re-check at submit, not just at open: the breaker can trip while the form
	// is up, and a test push is a device command like any other.
	if s.deviceControlDown() {
		s.itErr = deviceControlUnavailable
		return s, nil
	}
	pattern := indicatorPatterns[s.itPatternIdx]
	req := forgekeyapi.IndicatorTestRequest{
		Brightness: indicatorBrightness[s.itBrightnessIdx],
		Pattern:    pattern,
	}
	if pattern != "off" {
		req.Color = indicatorColors[s.itColorIdx]
	}
	if indicatorIsBlinkPattern(pattern) {
		period, err := strconv.Atoi(strings.TrimSpace(s.itPeriodIn.Value()))
		if err != nil || period < 1 || period > 60000 {
			s.itErr = "period must be an integer 1–60000 ms"
			return s, nil
		}
		req.PeriodMS = period
	}
	id := fmt.Sprint(s.device.ID)
	fk := s.deps.ForgeKey
	ctx := s.deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	s.itPending = true
	s.itErr = ""
	return s, func() tea.Msg {
		resp, err := fk.IndicatorTest(ctx, id, req)
		return fkIndicatorTestMsg{resp: resp, err: err}
	}
}

func (s *ForgeKeyDeviceDetailScreen) renderIndicatorTest() string {
	var b strings.Builder
	b.WriteString(StyleTitle.Render("Indicator test") + "\n")
	b.WriteString(StyleMuted.Render("Send an explicit color/brightness/pattern preview to this light.") + "\n\n")

	pattern := indicatorPatterns[s.itPatternIdx]
	rows := []struct {
		label string
		value string
	}{
		{"Color", indicatorColors[s.itColorIdx]},
		{"Brightness", indicatorBrightness[s.itBrightnessIdx]},
		{"Pattern", pattern},
		{"Period (ms)", s.itPeriodIn.View()},
	}
	if pattern == "off" {
		rows[0].value += " " + StyleMuted.Render("(omitted while off)")
	}
	for i := 0; i < s.itFieldCount(); i++ {
		cursor := "  "
		if s.itFocus == i {
			cursor = "▸ "
		}
		label := fmt.Sprintf("%-13s", rows[i].label+":")
		line := cursor + StyleMuted.Render(label) + " " + rows[i].value
		if s.itFocus == i {
			line = StyleTitle.Render(cursor+label) + " " + rows[i].value
		}
		b.WriteString(line + "\n")
	}

	if s.itErr != "" {
		b.WriteString("\n" + StyleStatusError.Render("✗ "+s.itErr) + "\n")
	}
	// The form is gated shut at 't', so this only shows when the breaker tripped
	// while it was already open — which is exactly when the operator needs to be
	// told before pressing enter into a timeout.
	if notice := serviceUnavailableNotice(s.deps.Health, omsapi.ServiceKeyDeviceControl, deviceControlUnavailable); notice != "" {
		b.WriteString("\n" + notice + "\n")
	}
	if s.itPending {
		b.WriteString("\n" + StyleMuted.Render("Sending…"))
	} else {
		b.WriteString("\n" + StyleMuted.Render("tab/↑↓ move · ←/→/space cycle · enter send · esc cancel"))
	}
	return b.String()
}

// renderConfirmDelete mirrors the web DeviceLifecycleCard delete modal's copy —
// a permanent-delete warning naming the device and its command history. The
// web's "use Retire instead" suggestion is dropped: ScanTTY has no retire action
// yet (is_active lifecycle is a separate concern), so pointing at it would
// dangle.
func (s *ForgeKeyDeviceDetailScreen) renderConfirmDelete() string {
	label := s.device.Name
	if label == "" {
		label = s.device.MACAddress
	}
	var b strings.Builder
	b.WriteString(StyleStatusError.Render("Delete device?") + "\n\n")
	b.WriteString("Permanently delete " + StyleTitle.Render(label) + " and its command history?\n")
	b.WriteString(StyleMuted.Render("This can’t be undone.") + "\n\n")
	if s.deleting {
		b.WriteString(StyleMuted.Render("Deleting…"))
	} else {
		b.WriteString(StyleMuted.Render("y delete · n/esc cancel"))
	}
	return b.String()
}

func (s *ForgeKeyDeviceDetailScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading device…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	if s.device == nil {
		return StyleMuted.Render("Device not found.")
	}
	if s.confirmingDelete {
		return s.renderConfirmDelete()
	}
	if s.mode == fkDetailIndicatorTest {
		return s.renderIndicatorTest()
	}
	d := s.device
	var b strings.Builder
	b.WriteString(StyleTitle.Render(d.Name) + "  ")
	if d.IsOnline {
		b.WriteString(StyleStatusOK.Render("● online"))
	} else {
		b.WriteString(StyleStatusError.Render("○ offline"))
	}
	b.WriteString("\n")
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %v · %v · MAC %s", d.ID, d.DeviceType, d.MACAddress)) + "\n\n")

	if d.Description != "" {
		b.WriteString(d.Description + "\n\n")
	}

	typeLabel := d.DeviceTypeName
	if typeLabel == "" {
		typeLabel = fmt.Sprintf("%v", d.DeviceType)
	}
	location := ""
	if d.Location != nil {
		location = fmt.Sprintf("#%d", *d.Location)
	}
	rows := [][2]string{
		{"Type", typeLabel},
		{"Location", location},
		{"Firmware", d.FirmwareVersion},
		{"IP", d.IPAddress},
	}
	if d.BootCount > 0 {
		rows = append(rows, [2]string{"Boots", fmt.Sprintf("%d", d.BootCount)})
	}
	if d.FreeHeap > 0 {
		rows = append(rows, [2]string{"Free heap", fmt.Sprintf("%d bytes", d.FreeHeap)})
	}
	if !d.LastSeen.IsZero() {
		rows = append(rows, [2]string{"Last seen", d.LastSeen.Format("2006-01-02 15:04")})
	}
	for _, kv := range rows {
		if kv[1] == "" {
			continue
		}
		b.WriteString(StyleMuted.Render(kv[0]+": ") + kv[1] + "\n")
	}

	if len(d.Capabilities) > 0 {
		b.WriteString(StyleMuted.Render("Capabilities: ") + strings.Join(d.Capabilities, ", ") + "\n")
	}
	b.WriteString("\n")

	// Live power-relay channel states (op-2cr), mirroring the web
	// PowerRelayWidget: green ●on / red ○off per channel from the cached
	// sub-state, with a note while nothing has been reported yet.
	if deviceHasCapability(d, "power_relay") {
		b.WriteString(StyleTitle.Render("Power relay") + "\n")
		for _, ch := range relayChannelNumbers {
			b.WriteString(fmt.Sprintf("  %s %s\n",
				StyleMuted.Render(fmt.Sprintf("Channel %d:", ch)),
				renderRelayChannelState(d, ch)))
		}
		if len(d.RelayChannels) == 0 {
			b.WriteString(StyleMuted.Render("  Live on/off state not reported yet.") + "\n")
		}
		b.WriteString("\n")
	}

	// Live indicator/status-LED colour (op-2cr), mirroring the web
	// IndicatorStateInline: a colour swatch + name, plus a non-solid pattern.
	if deviceHasCapability(d, "status_led") {
		b.WriteString(StyleTitle.Render("Status LED") + "\n")
		b.WriteString("  " + renderIndicatorState(d.IndicatorState) + "\n\n")
	}

	if s.temp != nil && (s.temp.LatestTemperatureC != nil || len(s.temp.Readings) > 0) {
		b.WriteString(StyleTitle.Render("Temperature (24h)") + "\n")
		if s.temp.LatestTemperatureC != nil {
			line := fmt.Sprintf("  %.1f°C", *s.temp.LatestTemperatureC)
			if s.temp.LatestHumidityPercent != nil {
				line += fmt.Sprintf("  %.0f%% RH", *s.temp.LatestHumidityPercent)
			}
			b.WriteString(line + "\n")
		}
		if spark := temperatureSparkline(s.temp.Readings); spark != "" {
			lo, hi := temperatureRange(s.temp.Readings)
			b.WriteString(fmt.Sprintf("  %s  %s\n", spark,
				StyleMuted.Render(fmt.Sprintf("%.1f–%.1f°C · %d samples", lo, hi, len(s.temp.Readings)))))
		} else if len(s.temp.Readings) == 0 {
			b.WriteString(StyleMuted.Render("  No readings in the last 24h.") + "\n")
		}
		b.WriteString("\n")
	}

	if len(s.commands) > 0 {
		b.WriteString(StyleTitle.Render("Recent commands") + "\n")
		for _, c := range s.commands {
			ts := c.SentAt.Format("15:04:05")
			status := c.EffectiveAckStatus
			if status == "" {
				status = c.AckStatus
			}
			line := fmt.Sprintf("  %s · %s · %s", ts, c.Command, status)
			if c.SentByUsername != "" {
				line += " " + StyleMuted.Render("("+c.SentByUsername+")")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if s.actionMsg != "" {
		b.WriteString(StyleMuted.Render(s.actionMsg) + "\n\n")
	}

	// Inline gate. The notice sits where the keypress would have been, not only
	// in the status bar, and the command keys come OFF the hint line while they
	// cannot work — a terminal's equivalent of the web greying the buttons out.
	// The record actions (E edit, x delete) are database writes and stay.
	if s.deviceControlDown() {
		b.WriteString(serviceUnavailableNotice(s.deps.Health, omsapi.ServiceKeyDeviceControl, deviceControlUnavailable) + "\n")
		b.WriteString(StyleMuted.Render("Device commands are unavailable until the broker is reachable; we keep retrying.") + "\n\n")
		b.WriteString(StyleMuted.Render("E edit · x delete · r refresh · esc back"))
		return b.String()
	}

	help := "E edit · x delete · e enable · d disable · s status · i identify · p ping · b blink · R restart · r refresh · esc back"
	if s.isIndicator {
		help = "t indicator-test · " + help
	}
	if deviceHasCapability(d, "power_relay") {
		help = "1/2 relay ch on · !/@ ch off · " + help
	}
	b.WriteString(StyleMuted.Render(help))
	return b.String()
}

// temperatureRange returns the min/max temp in a slice; zero/zero when
// empty so callers don't have to special-case the empty case.
func temperatureRange(readings []forgekeyapi.TemperatureReading) (float64, float64) {
	if len(readings) == 0 {
		return 0, 0
	}
	lo, hi := readings[0].TemperatureC, readings[0].TemperatureC
	for _, r := range readings[1:] {
		if r.TemperatureC < lo {
			lo = r.TemperatureC
		}
		if r.TemperatureC > hi {
			hi = r.TemperatureC
		}
	}
	return lo, hi
}

// temperatureSparkline maps a slice of readings into a one-line Unicode
// block-character sparkline. The 8 block levels span the min↔max range
// of the actual data — a degree-flat trend collapses to all level-0
// characters (▁) rather than rendering as random noise. Width caps at
// 40 columns to fit even inside the side-by-side nav layout; longer
// series subsample evenly.
func temperatureSparkline(readings []forgekeyapi.TemperatureReading) string {
	if len(readings) == 0 {
		return ""
	}
	const blocks = "▁▂▃▄▅▆▇█"
	const maxWidth = 40

	lo, hi := temperatureRange(readings)
	if hi == lo {
		// All samples equal — render one block at level 0 per column.
		width := len(readings)
		if width > maxWidth {
			width = maxWidth
		}
		out := make([]rune, width)
		for i := range out {
			out[i] = []rune(blocks)[0]
		}
		return string(out)
	}

	// Subsample to the column budget. Each output column averages the
	// span of source samples that fall in its bucket.
	samples := readings
	if len(samples) > maxWidth {
		step := float64(len(samples)) / float64(maxWidth)
		downsampled := make([]forgekeyapi.TemperatureReading, maxWidth)
		for i := 0; i < maxWidth; i++ {
			start := int(float64(i) * step)
			end := int(float64(i+1) * step)
			if end > len(samples) {
				end = len(samples)
			}
			if start >= end {
				downsampled[i] = samples[start]
				continue
			}
			var sum float64
			for j := start; j < end; j++ {
				sum += samples[j].TemperatureC
			}
			downsampled[i] = forgekeyapi.TemperatureReading{
				TemperatureC: sum / float64(end-start),
			}
		}
		samples = downsampled
	}

	bs := []rune(blocks)
	out := make([]rune, len(samples))
	for i, r := range samples {
		idx := int(((r.TemperatureC - lo) / (hi - lo)) * float64(len(bs)-1))
		if idx < 0 {
			idx = 0
		}
		if idx >= len(bs) {
			idx = len(bs) - 1
		}
		out[i] = bs[idx]
	}
	return string(out)
}
