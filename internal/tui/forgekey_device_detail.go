package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type ForgeKeyDeviceDetailScreen struct {
	deps     Deps
	devID    int
	device   *forgekeyapi.Device
	commands []forgekeyapi.DeviceCommand
	loading  bool
	loadErr  string
	actionMsg string
}

type fkDeviceLoadedMsg struct {
	device   *forgekeyapi.Device
	commands []forgekeyapi.DeviceCommand
	err      error
}

type fkCommandResultMsg struct {
	action string
	err    error
}

func NewForgeKeyDeviceDetailScreen(deps Deps, id string) *ForgeKeyDeviceDetailScreen {
	devID, _ := strconv.Atoi(id)
	return &ForgeKeyDeviceDetailScreen{deps: deps, devID: devID, loading: true}
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
		return fkDeviceLoadedMsg{device: dev, commands: cmds}
	}
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
		id := s.device.ID
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
	b.WriteString(StyleMuted.Render(fmt.Sprintf("ID %d · %s · MAC %s", d.ID, d.DeviceType, d.MACAddress)) + "\n\n")

	if d.Description != "" {
		b.WriteString(d.Description + "\n\n")
	}

	rows := [][2]string{
		{"Location", d.Location},
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

	if len(s.commands) > 0 {
		b.WriteString(StyleTitle.Render("Recent commands") + "\n")
		for _, c := range s.commands {
			ts := c.IssuedAt.Format("15:04:05")
			line := fmt.Sprintf("  %s · %s · %s", ts, c.Command, c.Status)
			if c.IssuedBy != "" {
				line += " " + StyleMuted.Render("("+c.IssuedBy+")")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if s.actionMsg != "" {
		b.WriteString(StyleMuted.Render(s.actionMsg) + "\n\n")
	}

	b.WriteString(StyleMuted.Render("e enable · d disable · s status · i identify · p ping · b blink · R restart · r refresh · esc back"))
	return b.String()
}
