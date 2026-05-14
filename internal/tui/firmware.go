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
			if v.DeviceType != "" {
				line += " " + StyleMuted.Render("("+v.DeviceType+")")
			}
			if v.IsActive {
				line += " " + StyleStatusOK.Render("active")
			}
			if v.CreatedBy != "" {
				line += " " + StyleMuted.Render("by "+v.CreatedBy)
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
			name := u.DeviceName
			if name == "" {
				name = fmt.Sprintf("device %d", u.Device)
			}
			ver := u.FirmwareVersionStr
			if ver == "" {
				ver = fmt.Sprintf("v#%d", u.FirmwareVersion)
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
