package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type ForgeKeyDeviceDetailScreen struct {
	deps      Deps
	devID     string
	device    *forgekeyapi.Device
	commands  []forgekeyapi.DeviceCommand
	temp      *forgekeyapi.TemperatureResponse
	loading   bool
	loadErr   string
	actionMsg string
}

type fkDeviceLoadedMsg struct {
	device   *forgekeyapi.Device
	commands []forgekeyapi.DeviceCommand
	temp     *forgekeyapi.TemperatureResponse
	err      error
}

type fkCommandResultMsg struct {
	action string
	err    error
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
		return fkDeviceLoadedMsg{device: dev, commands: cmds, temp: temp}
	}
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
	case tea.KeyMsg:
		if s.device == nil {
			return s, nil
		}
		ctx := s.deps.Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		fk := s.deps.ForgeKey
		id := fmt.Sprint(s.device.ID)
		switch m.String() {
		case "r":
			s.loading = true
			s.loadErr = ""
			return s, s.load()
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

func runFKCmd(label string, fn func() error) tea.Cmd {
	return func() tea.Msg {
		err := fn()
		return fkCommandResultMsg{action: label, err: err}
	}
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

	help := "e enable · d disable · s status · i identify · p ping · b blink · R restart · r refresh · esc back"
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
