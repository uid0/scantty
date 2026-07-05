package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/uid0/scantty/internal/forgekeyapi"
)

type FirmwareScreen struct {
	deps     Deps
	versions []forgekeyapi.FirmwareVersion
	updates  []forgekeyapi.FirmwareUpdate
	loading  bool
	loadErr  string
}

type firmwareLoadedMsg struct {
	versions []forgekeyapi.FirmwareVersion
	updates  []forgekeyapi.FirmwareUpdate
	err      error
}

func NewFirmwareScreen(deps Deps) *FirmwareScreen {
	return &FirmwareScreen{deps: deps, loading: true}
}

func (s *FirmwareScreen) Title() string { return "Firmware" }

func (s *FirmwareScreen) Init() tea.Cmd { return s.load() }

func (s *FirmwareScreen) load() tea.Cmd {
	deps := s.deps
	ctx := deps.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		versions, err := deps.ForgeKey.ListFirmwareVersions(ctx, nil)
		if err != nil {
			return firmwareLoadedMsg{err: err}
		}
		updates, _ := deps.ForgeKey.ListFirmwareUpdates(ctx, nil)
		return firmwareLoadedMsg{versions: versions, updates: updates}
	}
}

func (s *FirmwareScreen) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch m := msg.(type) {
	case firmwareLoadedMsg:
		s.loading = false
		if m.err != nil {
			s.loadErr = m.err.Error()
		}
		s.versions = m.versions
		s.updates = m.updates
		return s, nil
	case tea.KeyMsg:
		if m.String() == "r" {
			s.loading = true
			s.loadErr = ""
			return s, s.load()
		}
	}
	return s, nil
}

// firmwareDeviceTypeLabel renders the human device-type for a firmware row.
// The raw device_type field is an integer FK id; the readable name/code arrive
// as separate serializer fields, so prefer those and show nothing when absent
// rather than a meaningless number.
func firmwareDeviceTypeLabel(v forgekeyapi.FirmwareVersion) string {
	if v.DeviceTypeName != "" {
		return v.DeviceTypeName
	}
	return v.DeviceTypeCode
}

func (s *FirmwareScreen) View() string {
	if s.loading {
		return StyleMuted.Render("Loading firmware…")
	}
	if s.loadErr != "" {
		return StyleStatusError.Render("Error: ") + s.loadErr + "\n\n" + StyleMuted.Render("r retry · esc back")
	}
	var b strings.Builder
	if len(s.versions) > 0 {
		b.WriteString(StyleTitle.Render("Versions") + "\n")
		for _, v := range s.versions {
			line := "  · " + v.Version
			if label := firmwareDeviceTypeLabel(v); label != "" {
				line += " " + StyleMuted.Render("("+label+")")
			}
			if v.IsActive {
				line += " " + StyleStatusOK.Render("active")
			}
			if v.CreatedByUsername != "" {
				line += " " + StyleMuted.Render("by "+v.CreatedByUsername)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString(StyleMuted.Render("No firmware versions registered.") + "\n\n")
	}

	if len(s.updates) > 0 {
		b.WriteString(StyleTitle.Render("Recent updates") + "\n")
		limit := len(s.updates)
		if limit > 20 {
			limit = 20
		}
		for _, u := range s.updates[:limit] {
			name := u.DeviceMACAddress
			if name == "" {
				name = fmt.Sprintf("device %v", u.Device)
			}
			ver := u.FirmwareVersionStr
			if ver == "" {
				ver = fmt.Sprintf("v#%v", u.FirmwareVersion)
			}
			line := fmt.Sprintf("  · %s → %s", name, ver)
			if u.Status != "" {
				line += " " + StyleMuted.Render("["+u.Status+"]")
			}
			if !u.RequestedAt.IsZero() {
				line += " " + StyleMuted.Render(u.RequestedAt.Format("01-02 15:04"))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(StyleMuted.Render("r refresh · esc back"))
	return b.String()
}
